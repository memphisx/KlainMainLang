package llvm

// emit_reject_reason.go — a Promise's rejection reason as a caught value
// (TDD-00207). A rejection is the async twin of a `throw`, so its reason rides
// the same channel as `try/catch`: the { i8 tag, i64 payload } caught-value
// record (TypeCaught, TDD-00202). The task-promise struct
// ({ i64 state, ptr waiter, i64 v0, i64 v1, ptr reactions }) already has a spare
// slot on the rejected path — v1 is only used by *fulfilled* two-slot values —
// so the reason is stored with no struct growth: v0 (slot 2) = payload,
// v1 (slot 3) = tag. This preserves every reason type (numbers, strings, objects,
// and real Errors with their message/name/instanceof), matching Node.

import (
	"fmt"

	"KlainMainLang/ast"
)

// clearDynamicRejectParam nils out a single-parameter reject handler's type
// annotation when it is `any`/`unknown`, so emitRejectCallback's TypeCaught hint
// applies and the parameter binds as a lenient caught value (TDD-00207). Returns
// a restore closure that puts the annotation back. A no-op (returning an
// identity restore) for anything else.
func (e *Emitter) clearDynamicRejectParam(arg ast.Expression) func() {
	var params []ast.Param
	switch a := arg.(type) {
	case *ast.ArrowFunction:
		params = a.Params
	case *ast.FunctionExpression:
		params = a.Params
	default:
		return func() {}
	}
	if len(params) != 1 || params[0].Type == nil {
		return func() {}
	}
	if !e.resolveType(params[0].Type).IsDynamic {
		return func() {}
	}
	saved := params[0].Type
	params[0].Type = nil
	return func() { params[0].Type = saved }
}

// emitRejectAdapter wraps a reject handler whose declared parameter type is not
// TypeCaught in a trampoline closure that the .then/.catch runner can always call
// with a { i8, i64 } caught-value reason (TDD-00207). The trampoline coerces the
// caught reason to the handler's real parameter type (string / Error / any / …)
// and forwards the call, returning the handler's result so `.then(f, onR)`
// chaining still works. `userClo` is the emitted user-handler closure value; its
// FuncType gives the param/return types.
func (e *Emitter) emitRejectAdapter(userClo Value) Value {
	hasParam := len(userClo.Ty.FuncParams) >= 1
	paramTy := TypeCaught
	if hasParam {
		paramTy = userClo.Ty.FuncParams[0]
	}
	retTy := TypeVoid
	if userClo.Ty.FuncRetType != nil {
		retTy = *userClo.Ty.FuncRetType
	}
	retIR := thenCallRetIR(retTy)

	e.closureCtr++
	fn := fmt.Sprintf("@__kml_reject_adapt_%d", e.closureCtr)

	restore := e.beginThunkEmit()
	// Load the user handler's fn/env.
	fp := e.freshReg()
	fpS := e.freshReg()
	ep := e.freshReg()
	epS := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%uclo, i32 0, i32 0", fpS))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpS))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%uclo, i32 0, i32 1", epS))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epS))
	// A handler that ignores the reason (`.catch(() => …)`) takes env only; one
	// that names it gets the caught reason coerced to its declared param type.
	callArgs := "ptr " + ep
	if hasParam {
		reason := Value{Ref: "%reason", Ty: TypeCaught}
		arg := e.coerce(reason, paramTy)
		callArgs = fmt.Sprintf("ptr %s, %s %s", ep, StructFieldIR(arg.Ty), arg.Ref)
	}
	if retIR == "void" {
		e.emitInstr(fmt.Sprintf("call void %s(%s)", fp, callArgs))
		e.emitInstr("ret void")
	} else {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call %s %s(%s)", r, retIR, fp, callArgs))
		e.emitInstr(fmt.Sprintf("ret %s %s", retIR, r))
	}
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine %s %s(ptr %%uclo, { i8, i64 } %%reason) {\nentry:\n%s}\n", retIR, fn, body))

	// Trampoline closure { adaptFn, userClo } — the runner calls adaptFn(userClo,
	// reason). The param type it advertises is TypeCaught (what the runner passes).
	clo := e.buildBuiltinClosure(fn, userClo.Ref)
	ty := FuncType([]Type{TypeCaught}, retTy)
	return Value{Ref: clo, Ty: ty}
}

// emitValueToCaughtParts derives the (tag, payload) of an arbitrary value for the
// throw/reject channel — the single source of truth mirrored by emitThrow. A
// caught value passes its parts straight through; an Error (or a
// `class X extends Error`) becomes tag kmlTagError with the errorObj pointer's
// bits as payload; anything else boxes to a NaN-box `any` and splits into
// (tag, payload) via emitUnboxTagPayload.
func (e *Emitter) emitValueToCaughtParts(val Value) (tag, pay string, err error) {
	if val.Ty.IsCaught {
		t, p := e.caughtParts(val)
		return t, p, nil
	}
	isErrSubclass := false
	if val.Ty.ClassName != "" {
		if info, ok := e.classes[val.Ty.ClassName]; ok && info.IsErrorSubclass {
			isErrSubclass = true
		}
	}
	if val.Ty.IsError || isErrSubclass {
		errPtr := e.coerce(val, TypePtr)
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", p, errPtr.Ref))
		return fmt.Sprintf("%d", kmlTagError), p, nil
	}
	boxed, berr := e.emitBoxValue(val)
	if berr != nil {
		return "", "", berr
	}
	t, p := e.emitUnboxTagPayload(boxed)
	return t, p, nil
}

// storeRejectReason writes a caught value's (tag, payload) into a task promise's
// rejection slots: payload → v0 (slot 2), tag (widened to i64) → v1 (slot 3).
// It does NOT set the state or wake awaiters — the caller settles.
func (e *Emitter) storeRejectReason(prom, tag, pay string) {
	v0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", v0, promiseStructIR, prom))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", pay, v0))
	tagWide := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i8 %s to i64", tagWide, tag))
	v1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 3", v1, promiseStructIR, prom))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", tagWide, v1))
}

// storeRejectReasonI64Tag is storeRejectReason when the tag is already an i64
// register (e.g. straight from __kml_get_thrown_tag zext'd, or a literal).
func (e *Emitter) storeRejectReasonI64Tag(prom, tagI64, pay string) {
	v0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", v0, promiseStructIR, prom))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", pay, v0))
	v1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 3", v1, promiseStructIR, prom))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", tagI64, v1))
}

// loadRejectReasonCaught reconstructs a TypeCaught value from a rejected promise's
// v0 (payload) + v1 (tag) slots — the mirror of storeRejectReason, used by
// anything that reads a rejection reason as a first-class value (allSettled, etc.).
func (e *Emitter) loadRejectReasonCaught(prom string) Value {
	v0p := e.freshReg()
	pay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", v0p, promiseStructIR, prom))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", pay, v0p))
	v1p := e.freshReg()
	tagWide := e.freshReg()
	tag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 3", v1p, promiseStructIR, prom))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", tagWide, v1p))
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i8", tag, tagWide))
	return e.emitCaughtAggregate(tag, pay)
}
