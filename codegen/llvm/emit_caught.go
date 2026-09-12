package llvm

// emit_caught.go — operations on a catch-clause variable (TypeCaught, TDD-00202).
// The value is the unpacked { i8 tag, i64 payload } thrown-value record: a
// logical kmlTag* tag plus its payload (a NaN-box payload for primitives, a
// ptrtoint'd errorObjType pointer for kmlTagError=13). These helpers implement
// the operations TypeScript allows on `unknown` — typeof / instanceof / === /
// member-after-narrow — by reusing the existing any and Error machinery, with
// the Error case (which the packed NaN-box can't represent) handled directly off
// the tag.

import (
	"fmt"

	"KlainMainLang/ast"
)

// caughtParts extracts the (tag, payload) registers from a TypeCaught aggregate.
func (e *Emitter) caughtParts(v Value) (tag, pay string) {
	tag = e.freshReg()
	pay = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i8, i64 } %s, 0", tag, v.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i8, i64 } %s, 1", pay, v.Ref))
	return tag, pay
}

// emitCaughtAggregate builds a TypeCaught aggregate from tag/payload registers.
func (e *Emitter) emitCaughtAggregate(tag, pay string) Value {
	a0 := e.freshReg()
	a1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue { i8, i64 } undef, i8 %s, 0", a0, tag))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { i8, i64 } %s, i64 %s, 1", a1, a0, pay))
	return Value{Ref: a1, Ty: TypeCaught}
}

// emitCaughtToAny packs a caught value into a real NaN-box `any`. A caught Error
// (tag 13) has no packed encoding, so it downgrades to a kmlTagObject box (it IS
// an object — typeof "object", reference `===`); its Error shape is only
// reachable through the record before this packing. Every other tag round-trips
// exactly via __kml_nb_pack.
func (e *Emitter) emitCaughtToAny(v Value) Value {
	e.ensureNanBox()
	tag, pay := e.caughtParts(v)
	isErr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isErr, tag, kmlTagError))
	objBox := e.freshReg() // errObj ptr tagged as kmlTagObject (kind 1)
	e.emitInstr(fmt.Sprintf("%s = or i64 %s, 1", objBox, pay))
	nonErr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pack(i8 %s, i64 %s)", nonErr, tag, pay))
	box := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", box, isErr, objBox, nonErr))
	return Value{Ref: box, Ty: TypeAny}
}

// emitCaughtTypeof implements `typeof e`. Packing to `any` gives the right answer
// for every tag (a caught Error packs to an object box → "object", matching JS).
func (e *Emitter) emitCaughtTypeof(v Value) (Value, error) {
	return e.emitDynamicTypeof(e.emitCaughtToAny(v))
}

// emitCaughtEquals implements `e === x` / `e !== x` / `e == x` / `e != x` by
// packing the caught value to `any` and reusing the any equality (strict or
// loose). A caught Error packs to its object box, so `e === e` stays reference
// equality on the same pointer.
func (e *Emitter) emitCaughtEquals(v, other Value, negate, loose bool) (Value, error) {
	a := e.emitCaughtToAny(v)
	b, err := e.emitBoxValue(other)
	if err != nil {
		return Value{}, err
	}
	if loose {
		return e.emitAnyLooseEquals(a, b, negate)
	}
	return e.emitAnyEquals(a, b, negate)
}

// emitCaughtMemberGet implements `e.prop`. A caught Error reads the errorObjType
// field by name (message/name/code/errno/syscall/path) and boxes it; any other
// caught value packs to `any` and goes through the dynamic member path
// (primitives → undefined, static objects → the Stage-6 TypeError). Result is
// `any`, matching `unknown`'s member-access result type.
func (e *Emitter) emitCaughtMemberGet(v Value, propName string, pos ast.Pos) (Value, error) {
	tag, pay := e.caughtParts(v)
	isErr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isErr, tag, kmlTagError))

	// `.errors` is AggregateError's array accessor, not a plain errorObjType
	// field — route a caught Error to the same accessor the static path uses
	// (it kind-guards internally, yielding an empty array for a non-aggregate);
	// a non-Error caught value has no `.errors`, so an empty array.
	if propName == "errors" {
		resPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca { ptr, i64 }, align 8", resPtr))
		errL := e.freshLabel("caught.errs")
		elseL := e.freshLabel("caught.noerrs")
		mergeL := e.freshLabel("caught.errsmerge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isErr, errL, elseL))
		e.emitLabel(errL)
		errObj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", errObj, pay))
		arr := e.emitErrorErrorsAccess(errObj)
		e.emitInstr(fmt.Sprintf("store { ptr, i64 } %s, ptr %s, align 8", arr.Ref, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(elseL)
		e.emitInstr(fmt.Sprintf("store { ptr, i64 } { ptr null, i64 0 }, ptr %s, align 8", resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(mergeL)
		res := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load { ptr, i64 }, ptr %s, align 8", res, resPtr))
		return Value{Ref: res, Ty: ArrayOf(errorObjType)}, nil
	}

	// A known Error field (message/name/code/errno/syscall/path) returns that
	// field's real type (string/number) rather than `any`, so existing lenient
	// catch code — `e.message.length`, `"caught " + e.message` — keeps working
	// exactly as when the catch variable was typed errorObjType. A caught value
	// that is NOT an Error reads the field's zero value (empty string / 0); real
	// JS yields undefined there, a minor divergence for the rare non-Error throw
	// that is no worse than the prior errorObjType-assumed behavior.
	if idx, fieldTy, ok := errorObjType.FieldIndex(propName); ok && propName != "kind" {
		resPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", resPtr, fieldTy.IR, fieldTy.Align()))
		errL := e.freshLabel("caught.errfld")
		elseL := e.freshLabel("caught.nofld")
		mergeL := e.freshLabel("caught.fldmerge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isErr, errL, elseL))
		e.emitLabel(errL)
		errObj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", errObj, pay))
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, errorObjType.StructIR(), errObj, idx))
		fld := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", fld, StructFieldIR(fieldTy), gep, fieldTy.Align()))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, fld, resPtr, fieldTy.Align()))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(elseL)
		zero := zeroRef(fieldTy)
		if fieldTy.IR == "ptr" {
			zero = e.internString("") // empty string rather than a null deref downstream
		}
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, zero, resPtr, fieldTy.Align()))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(mergeL)
		res := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", res, fieldTy.IR, resPtr, fieldTy.Align()))
		return Value{Ref: res, Ty: fieldTy}, nil
	}

	// Unknown property: dynamic member access on the packed value — a primitive
	// reads undefined, a static object hits the Stage-6 TypeError (same as any).
	anyVal := e.emitCaughtToAny(v)
	return e.emitDynAnyMemberGetNamed(anyVal, e.internString(propName), propName, pos)
}

// emitCaughtInstanceOfError implements `e instanceof Error` / a built-in subtype:
// true only for a caught Error (tag 13) whose field-0 kind matches. "Error" is
// the base every kind satisfies; a specific kind requires an exact kind match.
func (e *Emitter) emitCaughtInstanceOfError(v Value, kindName string, kindID int64) Value {
	tag, pay := e.caughtParts(v)
	isErr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isErr, tag, kmlTagError))
	if kindName == "Error" {
		return Value{Ref: isErr, Ty: TypeBool}
	}
	// Specific kind: guard the field read on isErr (a non-Error payload is not an
	// errorObjType pointer), so compute the kind only when it is an Error.
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resPtr))
	e.emitInstr(fmt.Sprintf("store i1 0, ptr %s, align 1", resPtr))
	chkL := e.freshLabel("caught.iokind")
	mergeL := e.freshLabel("caught.iomerge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isErr, chkL, mergeL))
	e.emitLabel(chkL)
	errObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", errObj, pay))
	kgep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", kgep, errorObjType.StructIR(), errObj))
	kval := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", kval, kgep))
	km := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", km, kval, kindID))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", km, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(mergeL)
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", res, resPtr))
	return Value{Ref: res, Ty: TypeBool}
}

// emitCaughtInstanceOfClassTag implements `e instanceof UserErrorClass` for a
// `class X extends Error` (TDD-00202): true only for a caught Error (tag 13)
// whose kind slot (field 0) holds the class's TagID — the same runtime identity
// the static error-subclass instanceof compares (emit_classes.go).
func (e *Emitter) emitCaughtInstanceOfClassTag(v Value, tagID int64) Value {
	tag, pay := e.caughtParts(v)
	isErr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isErr, tag, kmlTagError))
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resPtr))
	e.emitInstr(fmt.Sprintf("store i1 0, ptr %s, align 1", resPtr))
	chkL := e.freshLabel("caught.iotag")
	mergeL := e.freshLabel("caught.iotagmerge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isErr, chkL, mergeL))
	e.emitLabel(chkL)
	errObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", errObj, pay))
	kgep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", kgep, errorObjType.StructIR(), errObj))
	kval := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", kval, kgep))
	km := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", km, kval, tagID))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", km, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(mergeL)
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", res, resPtr))
	return Value{Ref: res, Ty: TypeBool}
}

// emitCaughtToString renders a caught value as a string (for String(e), template
// literals, concatenation, console.log): a caught Error uses Error.prototype
// .toString ("Name: message"); anything else packs to `any` and uses the dynamic
// toString.
func (e *Emitter) emitCaughtToString(v Value) (Value, error) {
	tag, pay := e.caughtParts(v)
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resPtr))
	isErr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isErr, tag, kmlTagError))
	errL := e.freshLabel("caughtstr.err")
	elseL := e.freshLabel("caughtstr.other")
	mergeL := e.freshLabel("caughtstr.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isErr, errL, elseL))

	e.emitLabel(errL)
	errObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", errObj, pay))
	es, err := e.emitErrorToString(Value{Ref: errObj, Ty: errorObjType})
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", es.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(elseL)
	ds, err := e.emitDynamicToString(e.emitCaughtToAny(v))
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ds.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", res, resPtr))
	return Value{Ref: res, Ty: TypePtr}, nil
}
