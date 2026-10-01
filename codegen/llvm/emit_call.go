package llvm

import (
	"KlainMainLang/ast"
	"KlainMainLang/checker"
	"fmt"
	"strings"
)

// Call dispatch (emitCall router): routes every call expression to the
// relevant built-in implementation, most of which live in their own
// emit_<domain>.go files (emit_strings.go, emit_date.go, emit_fetch.go,
// emit_classes.go, emit_objects.go, emit_promise.go, emit_process.go,
// emit_fs.go, emit_memory.go, emit_http.go, emit_timers.go, emit_func.go,
// emit_collections.go, emit_arrays_*.go, emit_call_console.go,
// emit_call_json.go, emit_call_math.go, emit_call_number.go,
// emit_call_encoding.go) — this file is only the dispatcher itself plus the
// named (top-level) function / closure call-site machinery that has nowhere
// else to live.

// desugarTaggedTemplate builds the plain call “ tag`a${x}b` “ is
// equivalent to — `tag(["a","b"], x)` — as a synthetic *ast.CallExpression:
// a real array literal of the cooked quasis as the first argument, then
// every interpolated expression untouched (no implicit stringification —
// unlike a plain, un-tagged template literal's own interpolation) as the
// remaining arguments. See TDD-00059: this is the only new logic tagged
// templates need — every existing call-dispatch/coercion/rest-param-
// packing path handles the result exactly like a hand-written call.
// isStringRawTag reports whether a tagged-template tag is the built-in
// `String.raw` (and not a user binding shadowing `String`).
func (e *Emitter) isStringRawTag(tag ast.Expression) bool {
	mem, ok := tag.(*ast.MemberExpression)
	if !ok || mem.Property != "raw" {
		return false
	}
	id, ok := mem.Object.(*ast.Identifier)
	return ok && id.Name == "String" && !e.isShadowedByLocal("String")
}

// emitStringRaw implements the `String.raw` tag: it interleaves the RAW
// (undecoded) quasi text with the string-coerced interpolations, so escape
// sequences appear verbatim (`String.raw`\n“ is the two characters `\` and
// `n`). ADR-00562.
func (e *Emitter) emitStringRaw(tt *ast.TaggedTemplateExpression) (Value, error) {
	raw := tt.RawQuasis
	if len(raw) != len(tt.Quasis) {
		// Defensive: fall back to cooked if raw wasn't threaded (never expected).
		raw = tt.Quasis
	}
	acc := Value{Ref: e.internString(raw[0]), Ty: TypePtr}
	for i, expr := range tt.Exprs {
		val, err := e.emitExpr(expr)
		if err != nil {
			return Value{}, err
		}
		strVal, err := e.emitValueToString(val)
		if err != nil {
			return Value{}, fmt.Errorf("%d:%d: %w", tt.GetPos().Line, tt.GetPos().Col, err)
		}
		if acc, err = e.emitStringConcat(acc, strVal); err != nil {
			return Value{}, err
		}
		tail := Value{Ref: e.internString(raw[i+1]), Ty: TypePtr}
		if acc, err = e.emitStringConcat(acc, tail); err != nil {
			return Value{}, err
		}
	}
	return acc, nil
}

func desugarTaggedTemplate(tt *ast.TaggedTemplateExpression) *ast.CallExpression {
	quasiExprs := make([]ast.Expression, len(tt.Quasis))
	for i, q := range tt.Quasis {
		quasiExprs[i] = ast.NewStringLiteral(q, tt.GetPos())
	}
	args := append([]ast.Expression{ast.NewArrayLiteral(quasiExprs, tt.GetPos())}, tt.Exprs...)
	call := ast.NewCallExpression(tt.Tag, args, tt.GetPos())
	call.TypeArgs = tt.TypeArgs // `` tag<T>`…` ``
	return call
}

// emitOptionalCall implements `a?.m(...)` (ADR-00682): the receiver is
// evaluated once; if it is a null/undefined pointer the whole call
// short-circuits to the undefined/zero sentinel and `m` is not invoked
// (mirroring emitOptionalMember for reads). Otherwise the receiver is bound to a
// throwaway local and the call runs normally through it (no re-evaluation of a
// side-effecting receiver). A nested chain (`a?.b?.m()`) composes: a nullish
// inner link already produces the null sentinel this guard catches.
func (e *Emitter) emitOptionalCall(ex *ast.CallExpression, mem *ast.MemberExpression) (Value, error) {
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	// A nullable-scalar receiver (`{ i1, T }`, e.g. `map.get(k)?.toFixed()`):
	// the `absent` state short-circuits. Unwrap to the bare scalar, guard on the
	// presence bit, and run the method on the unwrapped value.
	if isNullableScalar(objVal.Ty) {
		return e.emitOptionalCallNullableScalar(ex, mem, objVal)
	}
	// An `any` receiver short-circuits where it holds null or undefined.
	if isUnconstrainedDynamic(objVal.Ty) {
		e.optionalCallCtr++
		recvName := fmt.Sprintf("__optc_recv_%d", e.optionalCallCtr)
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", objVal.Ref, slot))
		e.define(recvName, Symbol{Ptr: slot, Ty: objVal.Ty})
		through := ast.NewCallExpression(&ast.MemberExpression{Object: ast.NewIdentifier(recvName, mem.GetPos()), Property: mem.Property}, ex.Args, ex.GetPos())
		through.TypeArgs = ex.TypeArgs
		return e.emitNullGuardedExpr(e.anyIsNullish(objVal), through)
	}
	// A non-pointer (or aggregate-array) receiver can never be a null pointer —
	// run the call normally, non-optional. (Rare; matches emitOptionalMember.)
	if objVal.Ty.IR != "ptr" || objVal.Ty.IsArray {
		plain := ast.NewCallExpression(&ast.MemberExpression{Object: mem.Object, Property: mem.Property}, ex.Args, ex.GetPos())
		plain.TypeArgs = ex.TypeArgs
		return e.emitCall(plain)
	}

	// Bind the already-evaluated receiver to a throwaway local so the actual
	// call reuses it (evaluated exactly once).
	e.optionalCallCtr++
	recvName := fmt.Sprintf("__optc_recv_%d", e.optionalCallCtr)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", objVal.Ref, slot))
	e.define(recvName, Symbol{Ptr: slot, Ty: objVal.Ty})

	through := ast.NewCallExpression(&ast.MemberExpression{Object: ast.NewIdentifier(recvName, mem.GetPos()), Property: mem.Property}, ex.Args, ex.GetPos())
	through.TypeArgs = ex.TypeArgs
	return e.emitNullGuardedExpr(e.isAbsentPtr(objVal.Ref, objVal.Ty), through)
}

// optionalCalleeKind classifies the callee of an optional call `f?.(...)`.
type optionalCalleeKind int

const (
	// optCalleePresent: a declared function, method or built-in — never
	// nullish, so the call is an ordinary one.
	optCalleePresent optionalCalleeKind = iota
	// optCalleeValue: a function-typed variable or field, which may hold
	// null/undefined at run time — guarded by a null check.
	optCalleeValue
	// optCalleeHostDependent: a built-in Node defines on some hosts only
	// (process.getuid/geteuid/getgid/getegid: POSIX yes, Windows `undefined`).
	// Its optional call is `T | undefined` on every host so a program's types
	// do not change with the target.
	optCalleeHostDependent
)

func (e *Emitter) classifyOptionalCallee(callee ast.Expression) optionalCalleeKind {
	switch c := callee.(type) {
	case *ast.Identifier:
		if sym, ok := e.lookup(c.Name); ok && sym.Ty.IsFunc && sym.Ty.IR == "ptr" {
			return optCalleeValue
		}
	case *ast.MemberExpression:
		if id, ok := c.Object.(*ast.Identifier); ok && id.Name == "process" && !e.isShadowedByLocal("process") {
			switch c.Property {
			case "getuid", "geteuid", "getgid", "getegid":
				return optCalleeHostDependent
			}
			return optCalleePresent
		}
		objTy := e.inferExprType(c.Object)
		if objTy.IsObject || objTy.IsClass {
			if _, fty, ok := objTy.FieldIndex(c.Property); ok && fty.IsFunc && fty.IR == "ptr" {
				return optCalleeValue
			}
		}
	}
	return optCalleePresent
}

// optionalCallResultType is the static type of `f?.(...)` given the type of
// the same call written without `?.` — shared by emitOptionalCalleeCall and
// inferExprType so the two cannot disagree.
func optionalCallResultType(kind optionalCalleeKind, inner Type) Type {
	if kind == optCalleePresent || inner.IR == "void" || inner.IR == "" || inner.IsArray {
		return inner
	}
	if u := undefinedableElem(inner); u.Nullable {
		return u
	}
	return inner
}

// emitOptionalCalleeCall implements the optional call `f?.(...)` /
// `a.b?.(...)`: a nullish callee short-circuits the whole call to `undefined`
// without evaluating the arguments. The callee is evaluated exactly once.
func (e *Emitter) emitOptionalCalleeCall(ex *ast.CallExpression) (Value, error) {
	plain := *ex
	plain.Optional = false
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok && !mem.Optional {
		// `o.m?.(…)` of an optional method: called where the instance's class
		// implements it. The receiver is evaluated once.
		if ot := e.inferExprType(mem.Object); ot.IsClass && e.isOptionalMethod(ot.ClassName, mem.Property) {
			ov, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			e.optionalCallCtr++
			recv := fmt.Sprintf("__optc_recv_%d", e.optionalCallCtr)
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ov.Ref, slot))
			e.define(recv, Symbol{Ptr: slot, Ty: ov.Ty})
			pres, _ := e.optionalMethodPresence(ov, mem.Property)
			through := ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier(recv, mem.GetPos()), mem.Property, mem.GetPos()), ex.Args, ex.GetPos())
			through.TypeArgs = ex.TypeArgs
			return e.emitNullGuardedExpr(e.ptrIsNull(pres.Ref), through)
		}
	}
	// `o.m?.(…)` / `o?.m?.(…)` on an `any` receiver: undefined where o.m (or
	// o) is null or undefined, else the method call. The receiver is
	// evaluated once.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok && isUnconstrainedDynamic(e.inferExprType(mem.Object)) {
		ov, err := e.emitExpr(mem.Object)
		if err != nil {
			return Value{}, err
		}
		e.optionalCallCtr++
		recv := fmt.Sprintf("__optc_recv_%d", e.optionalCallCtr)
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ov.Ref, slot))
		e.define(recv, Symbol{Ptr: slot, Ty: ov.Ty})
		res := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", res))
		e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, res))
		getL, callL, doneL := e.freshLabel("optanycall.get"), e.freshLabel("optanycall.call"), e.freshLabel("optanycall.done")
		if mem.Optional {
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.anyIsNullish(ov), doneL, getL))
		} else {
			e.emitTerminator(fmt.Sprintf("br label %%%s", getL))
		}
		e.emitLabel(getL)
		fv, err := e.emitDynAnyMemberGetNamed(ov, e.internString(mem.Property), mem.Property, mem.GetPos())
		if err != nil {
			return Value{}, err
		}
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.anyIsNullish(fv), doneL, callL))
		e.emitLabel(callL)
		through := ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier(recv, mem.GetPos()), mem.Property, mem.GetPos()), ex.Args, ex.GetPos())
		through.TypeArgs = ex.TypeArgs
		v, err := e.emitCall(through)
		if err != nil {
			return Value{}, err
		}
		if !e.blockDone {
			if v.Ty.IR != "void" && v.Ty.IR != "" {
				b, err := e.emitBoxValue(v)
				if err != nil {
					return Value{}, err
				}
				e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", b.Ref, res))
			}
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		}
		e.emitLabel(doneL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, res))
		return Value{Ref: r, Ty: TypeAny}, nil
	}
	switch e.classifyOptionalCallee(ex.Callee) {
	case optCalleeHostDependent:
		inner := e.inferExprType(&plain)
		resTy := optionalCallResultType(optCalleeHostDependent, inner)
		if e.opts.Target.OS() == "windows" {
			// Node leaves these undefined on Windows: the call is skipped.
			if isNullableScalar(resTy) {
				return Value{Ref: e.makeNullableScalarAgg(resTy, "false", zeroRef(inner)), Ty: resTy}, nil
			}
			return Value{Ref: zeroRef(inner), Ty: resTy}, nil
		}
		v, err := e.emitCall(&plain)
		if err != nil {
			return Value{}, err
		}
		if isNullableScalar(resTy) {
			v = e.coerce(v, inner)
			return Value{Ref: e.makeNullableScalarAgg(resTy, "true", v.Ref), Ty: resTy}, nil
		}
		return v, nil
	case optCalleeValue:
		fv, err := e.emitExpr(ex.Callee)
		if err != nil {
			return Value{}, err
		}
		if fv.Ty.IR != "ptr" {
			return e.emitCall(&plain)
		}
		// Bind the evaluated callee to a throwaway local: the guarded call goes
		// through it, so a side-effecting callee expression runs once.
		e.optionalCallCtr++
		fnName := fmt.Sprintf("__optc_fn_%d", e.optionalCallCtr)
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", fv.Ref, slot))
		fnTy := fv.Ty
		fnTy.Nullable = false
		fnTy.IsUndefined = false
		e.define(fnName, Symbol{Ptr: slot, Ty: fnTy})
		through := ast.NewCallExpression(ast.NewIdentifier(fnName, ex.Callee.GetPos()), ex.Args, ex.GetPos())
		through.TypeArgs = ex.TypeArgs
		return e.emitNullGuardedExpr(e.ptrIsNull(fv.Ref), through)
	}
	return e.emitCall(&plain)
}

// anyIsNullish emits the i1 "this any holds null or undefined" condition.
func (e *Emitter) anyIsNullish(v Value) string {
	tag, _ := e.emitUnboxTagPayload(v)
	isNull, isUndef, nullish := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", nullish, isNull, isUndef))
	return nullish
}

// ptrIsNull emits the i1 "this pointer is null" condition emitNullGuardedExpr
// branches on.
func (e *Emitter) ptrIsNull(ref string) string {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", r, ref))
	return r
}

// emitNullGuardedExpr evaluates `through` unless the i1 `isNull` is set, in
// which case it is skipped entirely (a call's arguments, an index's key
// expression, the rest of a chain) and the result is a real `undefined`. The
// shared tail of `a?.m(...)` (guard = the receiver), `f?.(...)` (guard = the
// callee value), `a?.[k]` and a chain continuing past a `?.`
// (emitOptionalChain), whose guard may also be a nullable scalar's presence bit.
func (e *Emitter) emitNullGuardedExpr(isNull string, through ast.Expression) (Value, error) {
	retTy := e.inferExprType(through)
	isVoid := retTy.IR == "void" || retTy.IR == ""
	// `a?.m()` is `RetType | undefined` — a nullish receiver short-circuits to a
	// real `undefined` (as in TS), not the return type's zero (ADR-00834). Same
	// scalar `{ i1, T }` / pointer-null / array-passthrough shapes as `a?.x`.
	undefTy := undefinedableElem(retTy)
	wrapUndef := !isVoid && undefTy.Nullable && !retTy.IsArray

	var resPtr, resIR string
	if !isVoid {
		resIR = StructFieldIR(undefTy)
		resPtr = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", resPtr, resIR, undefTy.Align()))
	}

	nullL := e.freshLabel("optcall.null")
	nnL := e.freshLabel("optcall.nn")
	mergeL := e.freshLabel("optcall.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nullL, nnL))

	// null branch: a real `undefined` (method NOT called) — a `{present=false}`
	// optional for a scalar, the null pointer for a pointer, the zero-shaped
	// {null,0} for an array (no absent state).
	e.emitLabel(nullL)
	if !isVoid {
		if retTy.IsArray {
			// The array result slot holds a header pointer (TDD-00213 Stage 2/3);
			// a nullish receiver yields an empty array — a fresh {null,0} header.
			hdr := e.newArrayHeader("null", "0")
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, resPtr))
		} else if isNullableScalar(undefTy) {
			agg := e.makeNullableScalarAgg(undefTy, "false", zeroRef(retTy.withoutNullable()))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", resIR, agg, resPtr, undefTy.Align()))
		} else if retTy.IsDynamic {
			e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, resPtr))
		} else {
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", resIR, zeroRef(retTy), resPtr, undefTy.Align()))
		}
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	// non-null branch: the real call, through the bound receiver.
	e.emitLabel(nnL)
	callVal, err := e.emitExpr(through)
	if err != nil {
		return Value{}, err
	}
	if !isVoid {
		if retTy.IsArray {
			// Store the call result's shared header pointer so the merged result
			// aliases the returned array (TDD-00213 Stage 3).
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.arrayReturnHeader(callVal), resPtr))
		} else {
			stored := e.coerce(callVal, retTy)
			storeRef := stored.Ref
			// A guarded expression that is itself `T | undefined` (a further `?.`
			// to its right) already carries the { i1, T } aggregate — its own
			// absence passes through; wrapping it again is a type error.
			if isNullableScalar(undefTy) && !isNullableScalar(stored.Ty) {
				storeRef = e.makeNullableScalarAgg(undefTy, "true", stored.Ref)
			}
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", resIR, storeRef, resPtr, undefTy.Align()))
		}
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	if isVoid {
		return Value{Ty: TypeVoid}, nil
	}
	if retTy.IsArray {
		// resPtr holds a header pointer; load it and deref into an array Value.
		hdr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hdr, resPtr))
		return e.arrayValueFromHeaderReg(hdr, retTy), nil
	}
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", out, resIR, resPtr, undefTy.Align()))
	if wrapUndef {
		return Value{Ref: out, Ty: undefTy}, nil
	}
	return Value{Ref: out, Ty: retTy}, nil
}

// emitOptionalCallNullableScalar handles `recv?.m(...)` where recv is a
// nullable-scalar `{ i1, T }` value (e.g. `map.get(k)?.toFixed()`): if the
// presence bit is false the call short-circuits to `undefined`; otherwise the
// method runs on the unwrapped scalar. Mirrors emitOptionalCall's merge shape,
// guarding on the presence bit instead of a pointer-null test.
func (e *Emitter) emitOptionalCallNullableScalar(ex *ast.CallExpression, mem *ast.MemberExpression, objVal Value) (Value, error) {
	present, payload := e.nullableScalarAggParts(objVal)

	// Bind the unwrapped scalar to a throwaway local so the real call dispatches
	// on the bare payload type (evaluated exactly once).
	e.optionalCallCtr++
	recvName := fmt.Sprintf("__optcs_recv_%d", e.optionalCallCtr)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, payload.Ty.IR, payload.Ty.Align()))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", payload.Ty.IR, payload.Ref, slot, payload.Ty.Align()))
	e.define(recvName, Symbol{Ptr: slot, Ty: payload.Ty})

	through := ast.NewCallExpression(&ast.MemberExpression{Object: ast.NewIdentifier(recvName, mem.GetPos()), Property: mem.Property}, ex.Args, ex.GetPos())
	through.TypeArgs = ex.TypeArgs

	retTy := e.inferExprType(through)
	isVoid := retTy.IR == "void" || retTy.IR == ""
	undefTy := undefinedableElem(retTy)
	wrapUndef := !isVoid && undefTy.Nullable && !retTy.IsArray

	var resPtr, resIR string
	if !isVoid {
		resIR = StructFieldIR(undefTy)
		resPtr = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", resPtr, resIR, undefTy.Align()))
	}

	absentL := e.freshLabel("optcalls.absent")
	presentL := e.freshLabel("optcalls.present")
	mergeL := e.freshLabel("optcalls.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, presentL, absentL))

	// absent branch: a real `undefined` — the method is never called.
	e.emitLabel(absentL)
	if !isVoid {
		if retTy.IsArray {
			hdr := e.newArrayHeader("null", "0")
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, resPtr))
		} else if isNullableScalar(undefTy) {
			agg := e.makeNullableScalarAgg(undefTy, "false", zeroRef(retTy))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", resIR, agg, resPtr, undefTy.Align()))
		} else {
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", resIR, zeroRef(retTy), resPtr, undefTy.Align()))
		}
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	// present branch: the real call, through the unwrapped scalar receiver.
	e.emitLabel(presentL)
	callVal, err := e.emitCall(through)
	if err != nil {
		return Value{}, err
	}
	if !isVoid {
		if retTy.IsArray {
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.arrayReturnHeader(callVal), resPtr))
		} else {
			stored := e.coerce(callVal, retTy)
			storeRef := stored.Ref
			if isNullableScalar(undefTy) {
				storeRef = e.makeNullableScalarAgg(undefTy, "true", stored.Ref)
			}
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", resIR, storeRef, resPtr, undefTy.Align()))
		}
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	if isVoid {
		return Value{Ty: TypeVoid}, nil
	}
	if retTy.IsArray {
		hdr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hdr, resPtr))
		return e.arrayValueFromHeaderReg(hdr, retTy), nil
	}
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", out, resIR, resPtr, undefTy.Align()))
	if wrapUndef {
		return Value{Ref: out, Ty: undefTy}, nil
	}
	return Value{Ref: out, Ty: retTy}, nil
}

func (e *Emitter) emitCall(ex *ast.CallExpression) (Value, error) {
	// `Object.prototype.m.call(o, …)`: Object.prototype's own methods.
	if r, ok := e.objectProtoCall(ex); ok {
		return r()
	}
	// A call of assert's ok passes its source as a hidden last argument
	// (lib/node/assert.ts callSite): marked by the resolver, or a method
	// declared `/** @callsite */`.
	if !ex.PassSource && ex.Source != "" && e.calleeWantsSource(ex) {
		c := *ex
		c.PassSource = true
		return e.emitCall(&c)
	}
	if ex.PassSource {
		c := *ex
		c.PassSource = false
		c.Source = ""
		c.Args = append(append([]ast.Expression(nil), ex.Args...), ast.NewStringLiteral("\x00kml:callsite\x00"+ex.Source, ex.GetPos()))
		return e.emitCall(&c)
	}
	// A function returning `any` whose call the checker types as a function
	// (an overload returning its argument's type, `mustCall(fn)`): the
	// result converts to that function type.
	if ft, ok := e.checkerRefinedCallType(ex); ok && !e.refiningCall[ex] {
		e.refiningCall[ex] = true
		v, err := e.emitCall(ex)
		delete(e.refiningCall, ex)
		if err != nil || !isUnconstrainedDynamic(v.Ty) {
			return v, err
		}
		if c, ok := e.emitAnyToClosure(v, ft); ok {
			return c, nil
		}
		return v, nil
	}
	if m := indexCalleeAsMember(ex); m != nil {
		return e.emitCall(m)
	}
	if c := e.receiverlessThisCall(ex); c != nil {
		return e.emitCall(c)
	}
	// super(args) / super.method(args) (TDD-00009 Stage 3) — checked first,
	// since a SuperExpression callee/receiver never reaches the generic
	// mem.Object-based dispatch chain below (inferExprType has no case for
	// it, and it shouldn't: super is only ever meaningful directly in call
	// position).
	if _, ok := ex.Callee.(*ast.SuperExpression); ok {
		return e.emitSuperCall(ex)
	}
	// process.on(...), process.emitWarning(...): the members TypeScript
	// modules implement (processMemberRewrite).
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if r, ok := e.processMemberRewrite(mem); ok {
			rewritten := ast.NewCallExpression(r, ex.Args, ex.GetPos())
			rewritten.TypeArgs = ex.TypeArgs
			rewritten.Optional = ex.Optional
			return e.emitCall(rewritten)
		}
	}
	// Optional call `f?.(...)`: guard the callee, then dispatch the plain call.
	if ex.Optional {
		return e.emitOptionalCalleeCall(ex)
	}
	// `globalThis.setTimeout(...)` / `globalThis.JSON.stringify(...)` — peel the
	// `globalThis.` alias off the callee so it dispatches as the bare global.
	if unwrapped := e.unwrapGlobalThis(ex.Callee); unwrapped != ex.Callee {
		// A builtin's declaration is found through the original, bound
		// callee; the rewritten one is synthetic.
		if l, ok := e.loweredCall(ex); ok {
			return e.emitLowered(ex, l)
		}
		rewritten := ast.NewCallExpression(unwrapped, ex.Args, ex.GetPos())
		rewritten.TypeArgs = ex.TypeArgs
		return e.emitCall(rewritten)
	}
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if _, ok := mem.Object.(*ast.SuperExpression); ok {
			return e.emitSuperMethodCall(mem.Property, ex.Args, ex.GetPos())
		}
		// Optional-chaining method call `a?.m(...)` (ADR-00682): if the receiver
		// is null/undefined the ENTIRE call short-circuits to undefined and `m`
		// is never invoked. Only member *reads* honored `?.` before; a call went
		// through the unguarded dispatch below and ran the method on null.
		if mem.Optional {
			return e.emitOptionalCall(ex, mem)
		}
		// A method call on a run-time undefined/null receiver throws
		// (emit_nullderef.go).
		undo, err := e.guardBase(mem.Object, mem.Property, false)
		if err != nil {
			return Value{}, err
		}
		defer undo()
	}
	// A call through a builtin declaration's @lower target (TDD-00230 P3.2),
	// its receiver guarded like any method call's.
	if l, ok := e.loweredCall(ex); ok {
		return e.emitLowered(ex, l)
	}
	// A plain object's function-typed field (`posix.join(…)` on `{ join:
	// … }`), before the methods dispatched by name, which such an object has
	// none of.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok && !mem.Optional {
		if ot := e.inferExprType(mem.Object); ot.IsObject && !ot.IsClass && !ot.IsArray && !ot.IsDynamic && !isHostHandle(ot) && !ot.IsRegExp {
			if _, fty, isField := ot.FieldIndex(mem.Property); isField && fty.IsFunc {
				if mt := e.inferExprType(mem); mt.IsFunc {
					return e.emitFuncFieldCall(ex, mem, mt)
				}
			}
		}
	}
	// A function's own function-valued property (`assert.match(…)`, a
	// TypeScript expando), called through the function object's bag before
	// the methods dispatched by name, which a function has none of.
	if r, ok := e.funcOwnPropCall(ex); ok && e.isExpandoCall(ex) {
		mem := r.Callee.(*ast.MemberExpression)
		fn, err := e.emitExpr(mem.Object)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyMethodCallPlain(fn, mem.Property, ex.Args, ex.GetPos())
	}
	// Node's chained `http.createServer((req, res) => …).listen(port[, cb])`
	// (TDD-00131) — the callee is `<createServer call>.listen`. Routed through
	// the bound-handle machinery; listen() returns the server handle.
	if createArgs, listenArgs, ok := chainedCreateServerListen(ex); ok {
		return e.emitChainedCreateServerListen(createArgs, listenArgs, nil, ex.GetPos())
	}
	// A `res.writeHead/setHeader/write/end(...)` call on Node's http.createServer
	// `res` object (TDD-00131).
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if e.inferExprType(mem.Object).IsServerResponse {
			return e.emitServerResponseMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
	}
	// http.get/request response (TDD-00138): res.on('data'|'end'), setEncoding, …
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if e.inferExprType(mem.Object).IsIncomingMessage {
			return e.emitIncomingMessageCall(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
	}
	// Static method call: ClassName.staticMethod(args) (TDD-00009 Stage
	// 4). Checked before every mem.Property-name-based/inferExprType-based
	// dispatch below, for the same reason super's own checks above are: a
	// bare class-name identifier is a compile-time namespace, never a real
	// runtime value bindable via e.lookup/inferExprType.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if id, ok := mem.Object.(*ast.Identifier); ok {
			// TS namespace member call `X.member(args)` (TDD-00095): resolve
			// through the desugared flat function — checked before every
			// other member dispatch, since a namespace name (like a class
			// name) is a compile-time construct, never a runtime value. A
			// local binding shadowing the namespace name wins.
			if members, nsName := e.namespaceMembers(id.Name); members != nil {
				if exported, present := members[mem.Property]; present && !e.isShadowedByLocal(id.Name) {
					if !exported && e.curNamespace != nsName {
						return Value{}, fmt.Errorf("%d:%d: '%s.%s' is not exported from namespace '%s'", ex.GetPos().Line, ex.GetPos().Col, id.Name, mem.Property, nsName)
					}
					mangled := ast.NamespaceMangle(nsName, mem.Property)
					rewritten := ast.NewCallExpression(ast.NewIdentifier(mangled, ex.GetPos()), ex.Args, ex.GetPos())
					return e.emitCall(rewritten)
				}
			}
			if info, found := e.classes[id.Name]; found {
				return e.emitStaticMethodCall(info, id.Name, mem.Property, ex.Args, ex.GetPos())
			}
			if _, generic := e.genericClasses[id.Name]; generic && !e.isShadowedByLocal(id.Name) {
				if info, found := e.classes[genericStaticsClass(id.Name)]; found {
					return e.emitStaticMethodCall(info, genericStaticsClass(id.Name), mem.Property, ex.Args, ex.GetPos())
				}
			}
		}
		// `Base.call(this, args)` on a recognized prototype constructor
		// (TDD-00155 Stage 4): the pre-ES6 inheritance chain — the receiver
		// is passed through, unlike the generic .call path (which discards
		// thisArg, ADR-00398 Stage A).
		if id, ok := mem.Object.(*ast.Identifier); ok && e.compatJS() && e.jsProtoCtor[id.Name] && mem.Property == "call" {
			return e.emitProtoCtorChainCall(id.Name, ex.Args, ex.GetPos())
		}
		// Symbol.for / Symbol.keyFor (ADR-00488) — before class dispatch, a
		// user binding named Symbol still wins via the shadow check.
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Symbol" && !e.isShadowedByLocal("Symbol") &&
			(mem.Property == "for" || mem.Property == "keyFor") {
			return e.emitSymbolStatic(mem.Property, ex.Args, ex.GetPos())
		}
		// A namespace-qualified static call (`X.C.method()` — ADR-00480).
		if bare := e.stripNSTypeQualifier(mem.Object); bare != nil {
			if bid, ok := bare.(*ast.Identifier); ok {
				if info, found := e.classes[bid.Name]; found {
					return e.emitStaticMethodCall(info, bid.Name, mem.Property, ex.Args, ex.GetPos())
				}
			}
		}
		// Nested-namespace member call `A.B.f(args)` (TDD-00148 V3).
		if members, nsName := e.namespaceByChain(mem.Object); members != nil {
			if exported, present := members[mem.Property]; present {
				if !exported && e.curNamespace != nsName {
					return Value{}, fmt.Errorf("%d:%d: '%s.%s' is not exported from namespace '%s'", ex.GetPos().Line, ex.GetPos().Col, nsName, mem.Property, nsName)
				}
				mangled := ast.NamespaceMangle(nsName, mem.Property)
				rewritten := ast.NewCallExpression(ast.NewIdentifier(mangled, ex.GetPos()), ex.Args, ex.GetPos())
				return e.emitCall(rewritten)
			}
		}
	}
	// Special-case: console.log(...) and array.push(...)
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		// URLSearchParams: the ordered pair-list backs the whole method surface
		// (TDD-00203). Checked first so a shared name (sort/keys/values/entries/
		// forEach) routes to the __kml_usp_* ABI rather than the array/Map paths
		// below, which would reject the non-array/non-Map receiver.
		if objTy := e.inferExprType(mem.Object); objTy.IsURL && !objTy.IsDynamic && (mem.Property == "toString" || mem.Property == "toJSON") && len(ex.Args) == 0 {
			// URL.prototype.toString/toJSON: the href.
			return e.emitMember(ast.NewMemberExpression(mem.Object, "href", mem.GetPos()))
		}
		if objTy := e.inferExprType(mem.Object); objTy.IsURLSearchParams {
			if v, handled, err := e.emitURLSearchParamsCall(mem.Object, mem.Property, ex.Args, ex.GetPos()); handled {
				return v, err
			}
			return Value{}, fmt.Errorf("%d:%d: URLSearchParams has no method '%s'", ex.GetPos().Line, ex.GetPos().Col, mem.Property)
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "URL" && !e.isShadowedByLocal(id.Name) &&
			(mem.Property == "canParse" || mem.Property == "parse") {
			// WHATWG statics URL.canParse / URL.parse (TDD-00203) — distinct from
			// the legacy `url.parse()` module function (a lowercase `url` import).
			return e.emitURLStaticCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "BigInt" && !e.isShadowedByLocal(id.Name) &&
			(mem.Property == "asIntN" || mem.Property == "asUintN") {
			return e.emitBigIntAsN(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Buffer" && !e.isShadowedByLocal(id.Name) {
			return e.emitBufferStaticCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "ArrayBuffer" && mem.Property == "isView" && len(ex.Args) == 1 && !e.isShadowedByLocal(id.Name) {
			return e.emitArrayBufferIsView(ex.Args[0])
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Atomics" && !e.isShadowedByLocal(id.Name) {
			return e.emitAtomicsCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "JSON" && !e.isShadowedByLocal(id.Name) {
			switch mem.Property {
			case "stringify":
				return e.emitJSONStringify(ex.Args, ex.GetPos())
			case "parse":
				// Context-free JSON.parse is JS-faithful untyped parse: the
				// result is a dynamic (`any`) tree — tag-10 objects / tag-11
				// arrays / boxed scalars (TDD-00155 Stage 2). A typed target
				// (declared annotation) routes through emitDeclJSONProjection
				// with the real target type instead of reaching here; an
				// `as T` written on the call supplies the target the same way.
				if ty, ok := e.callAssertedTargetTy(ex); ok {
					return e.emitJSONParse(ex.Args, ty, ex.GetPos())
				}
				return e.emitJSONParse(ex.Args, TypeAny, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Date" && mem.Property == "now" {
			return e.emitDateNow()
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Date" && mem.Property == "parse" {
			return e.emitDateParse(ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Date" && mem.Property == "UTC" && !e.isShadowedByLocal("Date") {
			// Date.UTC: the same fields as new Date(y, m, …), read as UTC, as
			// a time value number.
			v, err := e.emitDateFields(ex.Args)
			if err != nil {
				return Value{}, err
			}
			return Value{Ref: v.Ref, Ty: TypeI64}, nil
		}
		if mem.Property == "toString" && e.inferExprType(mem.Object).IsSymbol {
			objVal, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitSymbolToString(objVal)
		}
		if mem.Property == "toString" && e.inferExprType(mem.Object).IsBigInt {
			return e.emitBigIntToStringMethod(mem.Object, ex.Args, ex.GetPos())
		}
		if mem.Property == "toString" && e.inferExprType(mem.Object).IsError {
			objVal, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitErrorToString(objVal)
		}
		if objTy := e.inferExprType(mem.Object); objTy.IsReadableStream || objTy.IsStreamReader || objTy.IsRSController {
			return e.emitStreamMethodCall(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if objTy := e.inferExprType(mem.Object); objTy.IsWritableStream || objTy.IsStreamWriter || objTy.IsWSController {
			return e.emitWStreamMethodCall(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if (mem.Property == "then" || mem.Property == "catch" || mem.Property == "finally") && e.inferExprType(mem.Object).IsPromise {
			return e.emitPromiseThen(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if isResponseMethodName(mem.Property) && hasBodyMixin(e.inferExprType(mem.Object)) {
			objVal, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			if ty, ok := e.callAssertedTargetTy(ex); ok && mem.Property == "json" {
				// `res.json() as T` supplies the parse target (a carve-out in
				// the same spirit as `JSON.parse(s) as T`); the result is still
				// a Promise<T> you await (TDD-00186 Part B).
				return e.emitResponseCall(objVal, mem.Property, ex.GetPos(), ty)
			}
			return e.emitResponseCall(objVal, mem.Property, ex.GetPos())
		}
		if mem.Property == "encode" && e.inferExprType(mem.Object).IsTextEncoder {
			return e.emitTextEncoderEncode(mem.Object, ex.Args, ex.GetPos())
		}
		if mem.Property == "encodeInto" && e.inferExprType(mem.Object).IsTextEncoder {
			return e.emitTextEncoderEncodeInto(mem.Object, ex.Args, ex.GetPos())
		}
		if mem.Property == "decode" && e.inferExprType(mem.Object).IsTextDecoder {
			return e.emitTextDecoderDecode(mem.Object, ex.Args, ex.GetPos())
		}
		if mem.Property == "test" && e.inferExprType(mem.Object).IsURLPattern {
			return e.emitURLPatternTest(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "exec" && e.inferExprType(mem.Object).IsURLPattern {
			return e.emitURLPatternExec(mem, ex.Args, ex.GetPos())
		}

		if mem.Property == "toString" && len(ex.Args) == 0 && e.inferExprType(mem.Object).IsRegExp {
			rv, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitValueToString(rv)
		}

		if objTy := e.inferExprType(mem.Object); objTy.IsChildProcess || objTy.IsCPStream || objTy.IsCPStdin {
			return e.emitChildProcessMethodCall(mem.Object, objTy, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsClientRequest {
			return e.emitClientRequestMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsHTTPAgent {
			return e.emitHTTPAgentMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsWebview {
			return e.emitWebviewMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsEmbeddedAssets {
			return e.emitEmbeddedAssetsMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "assets__kml_builtin" {
			return e.emitAssetsModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "tty__kml_builtin" {
			return e.emitTtyModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "tui__kml_builtin" {
			return e.emitTuiModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "sync__kml_builtin" {
			return e.emitSyncModuleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsChannel {
			return e.emitChannelMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsHTTPServer {
			return e.emitHTTPServerMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if e.inferExprType(mem.Object).IsFinalizationRegistry {
			return e.emitFinalizationRegistryMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		// `/** @value */` flat array (TDD-00134 Stage 2): push is the one
		// supported method; everything else needs the pointer-slot layout.
		if objTy := e.inferExprType(mem.Object); objTy.IsFlatArray {
			if mem.Property == "push" {
				return e.emitFlatArrayPush(objTy, mem, ex.Args, ex.GetPos())
			}
			return Value{}, fmt.Errorf("%d:%d: a @value array supports index read/write, .length, for...of, and .push — '%s' needs a regular (pointer-element) array", ex.GetPos().Line, ex.GetPos().Col, mem.Property)
		}
		if e.inferExprType(mem.Object).IsNetSocket {
			return e.emitNetSocketMethod(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		if mem.Property == "stream" && e.inferExprType(mem.Object).IsRequest {
			objVal, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitRequestStream(objVal, ex.GetPos())
		}
		// TDD-00195 Stage 1: a server `req` is a Node Readable — `.on`/`.once`/
		// `.pipe` forward to its cached wrapped readable (Node-stream dispatch).
		if (mem.Property == "on" || mem.Property == "once" || mem.Property == "pipe") && e.inferExprType(mem.Object).IsRequest {
			objVal, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			nr, err := e.reqAsNodeReadable(objVal, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			return e.emitDynAnyMethodCall(nr, mem.Property, ex.Args, ex.GetPos())
		}
		if mem.Property == "bodyBytes" && e.inferExprType(mem.Object).IsRequest {
			objVal, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitRequestBodyBytes(objVal, ex.GetPos())
		}
		// User-defined class method call: instance.method(args). Checked
		// before the long unguarded mem.Property == "<name>" chain below
		// (push/slice/map/join/...), several of which match purely on
		// property name with no receiver-type guard at all — a class method
		// sharing a name with one of those built-ins must not be shadowed.
		// Only fires when the class actually declares a method by that name,
		// so a field that happens to hold a closure (cb: () => void) still
		// falls through to the generic IsFunc field-call dispatch below.
		// Also — crucially — checked before the generic hasOwnProperty/
		// toString checks right below, since a class instance is IsObject
		// too: a class that declares its own toString()/hasOwnProperty()
		// must win over the generic built-in behavior, exactly like real JS
		// prototype-chain method resolution would.
		if objTy := e.inferExprType(mem.Object); objTy.IsClass {
			// `new Box<string>(…).get()`: the instantiation the receiver's
			// `new` makes, registered before its methods are looked up.
			if _, ok := e.classes[objTy.ClassName]; !ok {
				if ne, isNew := mem.Object.(*ast.NewExpression); isNew {
					if genDecl, generic := e.genericClasses[ne.ClassName]; generic {
						if targs, ok := classTypeArgs(genDecl, ne.TypeArgs); ok {
							if _, err := e.instantiateGenericClass(genDecl, e.buildTypeArgSubs(genDecl.TypeParams, targs)); err != nil {
								return Value{}, err
							}
						}
					}
				}
			}
			if info, ok := e.classes[objTy.ClassName]; ok {
				if _, ok := info.MethodSigs[mem.Property]; ok {
					v, err := e.emitClassMethodCall(objTy, mem.Object, mem.Property, ex.Args, ex.GetPos())
					if err == nil && v.Ty.IsClass {
						v.Ty = e.genericMethodResult(ex, objTy, mem.Property, v.Ty)
					} else if err == nil && isUnconstrainedDynamic(v.Ty) {
						if ct := e.genericMethodResult(ex, objTy, mem.Property, v.Ty); ct.IsClass {
							v = e.coerce(v, ct)
						}
					}
					return v, err
				}
				// A field of type `any` holding a function (`this.onError =
				// (e) => …`): called dynamically, with the instance as `this`.
				if _, fty, isField := info.Ty.FieldIndex(mem.Property); isField && isUnconstrainedDynamic(fty) {
					return e.emitClassMethodCall(objTy, mem.Object, mem.Property, ex.Args, ex.GetPos())
				}
			}
		}
		if mem.Property == "hasOwnProperty" && e.inferExprType(mem.Object).IsObject {
			if len(ex.Args) != 1 {
				return Value{}, fmt.Errorf("%d:%d: hasOwnProperty takes 1 argument", ex.GetPos().Line, ex.GetPos().Col)
			}
			return e.emitHasOwnProperty(mem.Object, ex.Args[0], "hasOwnProperty", true, ex.GetPos())
		}
		if mem.Property == "toString" && len(ex.Args) == 0 && isNumberTy(e.inferExprType(mem.Object)) {
			// A boolean (or a number the declarations' lowering does not
			// take): String(x).
			v, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitValueToString(v)
		}
		// str.toString() is the identity — Node code calls it habitually on
		// values that are Buffers there but strings here (spawnSync results,
		// stream chunks), so this keeps that idiom compiling.
		if mem.Property == "toString" && len(ex.Args) == 0 && isPlainStringType(e.inferExprType(mem.Object).staticIndexType()) {
			return e.emitExpr(mem.Object)
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Array" && !e.isShadowedByLocal(id.Name) {
			switch mem.Property {
			case "isArray":
				if len(ex.Args) != 1 {
					return Value{}, fmt.Errorf("%d:%d: Array.isArray takes exactly 1 argument", ex.GetPos().Line, ex.GetPos().Col)
				}
				// A dynamic (`any`) argument's array-ness is only known at
				// runtime — a NaN-box can carry an array now and a plain object
				// the next line. Consult the box tag (a static-array box is
				// kmlTagArray, a D1 dynamic array kmlTagDynArray) rather than the
				// compile-time IsArray, which is always false for `any` and made
				// `Array.isArray(x)` wrongly return false for a genuine boxed
				// array (ADR-00934).
				argTy := e.inferExprType(ex.Args[0])
				if argTy.IsDynamic {
					v, err := e.emitExpr(ex.Args[0])
					if err != nil {
						return Value{}, err
					}
					v, err = e.emitBoxValue(v)
					if err != nil {
						return Value{}, err
					}
					tag, payload := e.emitUnboxTagPayload(v)
					isArr := e.freshReg()
					isDyn := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isArr, tag, kmlTagArray))
					e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isDyn, tag, kmlTagDynArray))
					// A boxed TypedArray is kmlTagArray too, but `Array.isArray(new
					// Int32Array(1))` is false — read the box's typed byte (ADR-01059).
					// Only dereferenced when the tag says the payload is a box.
					resPtr := e.freshReg()
					e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resPtr))
					e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", isDyn, resPtr))
					typedL := e.freshLabel("isarray.typed")
					mergeL := e.freshLabel("isarray.merge")
					e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, typedL, mergeL))
					e.emitLabel(typedL)
					box := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", box, payload))
					typedGep := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", typedGep, anyArrayBoxTy, box))
					typedB := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", typedB, typedGep))
					plain := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", plain, typedB, anyArrayPlain))
					e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", plain, resPtr))
					e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
					e.emitLabel(mergeL)
					res := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", res, resPtr))
					return Value{Ref: res, Ty: TypeBool}, nil
				}
				if argTy.IsTypedArray || argTy.IsBuffer {
					// A TypedArray/Buffer is IsArray storage-wise but not a JS Array.
					return Value{Ref: "false", Ty: TypeBool}, nil
				}
				if argTy.IsArray {
					// A `T[] | undefined` value (a nested-array element absence,
					// TDD-00221): a miss is `undefined`, and `Array.isArray(undefined)`
					// is false — decide at runtime on the null data-ptr.
					if argTy.Nullable {
						v, err := e.emitExpr(ex.Args[0])
						if err != nil {
							return Value{}, err
						}
						dataPtr := e.freshReg()
						e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", dataPtr, v.Ref))
						isArr := e.freshReg()
						e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isArr, dataPtr))
						return Value{Ref: isArr, Ty: TypeBool}, nil
					}
					return Value{Ref: "true", Ty: TypeBool}, nil
				}
				return Value{Ref: "false", Ty: TypeBool}, nil
			case "of":
				return e.emitArrayOf(ex.Args, ex.GetPos())
			case "from":
				return e.emitArrayFrom(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "ReadableStream" && mem.Property == "from" {
			if _, found := e.lookup(id.Name); !found {
				return e.emitReadableStreamFrom(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Response" && (mem.Property == "json" || mem.Property == "redirect" || mem.Property == "error") {
			if _, user := e.classes[id.Name]; !user {
				if _, found := e.lookup(id.Name); !found {
					return e.emitResponseStatic(mem.Property, ex.Args, ex.GetPos())
				}
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Promise" {
			switch mem.Property {
			case "all":
				return e.emitPromiseAll(ex.Args, ex.GetPos())
			case "race":
				return e.emitPromiseRace(ex.Args, ex.GetPos())
			case "allSettled":
				return e.emitPromiseAllSettled(ex.Args, ex.GetPos())
			case "any":
				return e.emitPromiseAny(ex.Args, ex.GetPos())
			case "resolve":
				return e.emitPromiseResolve(ex.Args, ex.GetPos(), Type{})
			case "reject":
				return e.emitPromiseReject(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Reflect" && !e.isShadowedByLocal(id.Name) {
			return e.emitReflectCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Object" && !e.isShadowedByLocal(id.Name) {
			switch mem.Property {
			case "groupBy":
				return e.emitObjectGroupBy(ex.Args, ex.GetPos())
			case "keys":
				return e.emitObjectKeys(ex.Args, ex.GetPos())
			case "values":
				return e.emitObjectValues(ex.Args, ex.GetPos())
			case "entries":
				return e.emitObjectEntries(ex.Args, ex.GetPos())
			case "fromEntries":
				return e.emitObjectFromEntries(ex.Args, ex.GetPos())
			case "assign":
				return e.emitObjectAssign(ex.Args, ex.GetPos())
			case "freeze":
				return e.emitObjectFreeze(ex.Args, ex.GetPos())
			case "seal":
				return e.emitObjectSeal(ex.Args, ex.GetPos())
			case "hasOwn":
				if len(ex.Args) != 2 {
					return Value{}, fmt.Errorf("%d:%d: Object.hasOwn takes 2 arguments", ex.GetPos().Line, ex.GetPos().Col)
				}
				return e.emitHasOwnProperty(ex.Args[0], ex.Args[1], "Object.hasOwn", true, ex.GetPos())
			case "create":
				return e.emitObjectCreate(ex.Args, ex.GetPos())
			case "getPrototypeOf":
				return e.emitObjectGetPrototypeOf(ex.Args, ex.GetPos())
			case "setPrototypeOf":
				return e.emitObjectSetPrototypeOf(ex.Args, ex.GetPos())
			case "defineProperty":
				return e.emitObjectDefineProperty(ex.Args, ex.GetPos())
			case "defineProperties":
				return e.emitObjectDefineProperties(ex.Args, ex.GetPos())
			case "getOwnPropertyDescriptor":
				return e.emitObjectGetOwnPropertyDescriptor(ex.Args, ex.GetPos())
			case "getOwnPropertyNames":
				return e.emitObjectGetOwnPropertyNames(ex.Args, ex.GetPos())
			case "preventExtensions", "isExtensible", "isSealed", "isFrozen":
				// Dynamic-object forms (TDD-00155 Stage 5); the static-object
				// freeze/seal paths keep their own handlers below.
				if len(ex.Args) == 1 && isUnconstrainedDynamic(e.inferExprType(ex.Args[0])) {
					v, err := e.emitExprWithObjectHint(ex.Args[0], TypeAny)
					if err != nil {
						return Value{}, err
					}
					switch mem.Property {
					case "preventExtensions":
						return e.emitDynPrevent(v, 0)
					case "isExtensible":
						return e.emitDynFlagsTest(v, 0)
					case "isSealed":
						return e.emitDynFlagsTest(v, 1)
					case "isFrozen":
						return e.emitDynFlagsTest(v, 2)
					}
				}
				// A static object's integrity level (TDD-00229).
				if len(ex.Args) == 1 && e.inferExprType(ex.Args[0]).IsObject {
					v, err := e.emitExpr(ex.Args[0])
					if err != nil {
						return Value{}, err
					}
					if mem.Property == "preventExtensions" {
						e.emitStaticIntegrity(v.Ref, staticIntegrityNonExtensible)
						return v, nil
					}
					return e.emitStaticIntegrityTest(v, mem.Property), nil
				}
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "process" && !e.isShadowedByLocal(id.Name) {
			switch mem.Property {
			case "nextTick":
				return e.emitProcessNextTick(ex.Args, ex.GetPos())
			case "getuid", "geteuid", "getgid", "getegid":
				// Node has no process.getuid/getgid family on Windows (they are
				// undefined there, so the bare call is a TypeError in Node);
				// reject at compile time (TDD-00177). The portable spelling is
				// the optional call, which evaluates to `undefined` here
				// (emitOptionalCalleeCall). Elsewhere they are
				// internal_process_methods.ts's (processMemberRewrite).
				return Value{}, fmt.Errorf("%d:%d: process.%s is not available on Windows (Node defines it only on POSIX) — use the optional call `process.%s?.()`, which is `undefined` on Windows", ex.GetPos().Line, ex.GetPos().Col, mem.Property, mem.Property)
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "url__reexport_kml_builtin" {
			// TDD-00165 Stage 4: the legacy `url` module functions. The primary
			// exports (URL/URLSearchParams) are reexports of the globals handled in
			// the resolver; only the module-only functions reach codegen here.
			switch mem.Property {
			case "fileURLToPath":
				return e.emitFileURLToPath(ex.Args, ex.GetPos())
			case "pathToFileURL":
				return e.emitPathToFileURL(ex.Args, ex.GetPos())
			case "urlToHttpOptions":
				return e.emitUrlToHttpOptions(ex.Args, ex.GetPos())
			case "domainToASCII":
				return e.emitUrlDomainConvert(ex.Args, ex.GetPos(), curluPunycode)
			case "domainToUnicode":
				return e.emitUrlDomainConvert(ex.Args, ex.GetPos(), curluPuny2IDN)
			}
		}
		if e.isCryptoSubtle(mem.Object) {
			return e.emitCryptoSubtleCall(mem.Property, ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "crypto" && !e.isShadowedByLocal(id.Name) {
			switch mem.Property {
			case "getRandomValues":
				return e.emitCryptoGetRandomValues(ex.Args, ex.GetPos())
			case "randomUUID":
				return e.emitCryptoRandomUUID(ex.Args, ex.GetPos())
			}
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "Memory__kml_builtin" && mem.Property == "free" {
			return e.emitMemoryFree(ex.Args, ex.GetPos())
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "http__kml_builtin" {
			switch mem.Property {
			case "listen", "close", "closeAllConnections":
				// Bespoke server functions moved to klain:http (ADR-00635) — Node
				// has these as Server methods, not `http.*` functions.
				return Value{}, fmt.Errorf("%d:%d: http.%s is not a Node API — use `http.createServer(handler).listen(port)` for the faithful server, or import the bespoke handler-returns-response model from klain:http: `import http from 'klain:http'`", ex.GetPos().Line, ex.GetPos().Col, mem.Property)
			case "get", "request":
				return e.emitHTTPClientGet(ex.Args, ex.GetPos(), mem.Property == "request")
			case "createServer":
				// The chained createServer(cb).listen(...) expression is
				// intercepted earlier; reaching here means the variable-bound
				// handle form (TDD-00131 follow-on).
				return e.emitHTTPCreateServer(ex.Args, ex.GetPos())
			}
		}
		// klain:http — the bespoke handler-returns-response server model
		// (`listen`/`close`/`closeAllConnections`), on its own marker so it is
		// reachable ONLY via `import … from 'klain:http'`, not the Node `http`
		// namespace (ADR-00635). Same implementations as http's former ones.
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "klainhttp__kml_builtin" {
			switch mem.Property {
			case "listen":
				return e.emitHTTPListen(ex.Args, ex.GetPos())
			case "close":
				return e.emitHTTPClose(ex.Args, ex.GetPos())
			case "closeAllConnections":
				return e.emitHTTPCloseAllConnections(ex.Args, ex.GetPos())
			}
			return Value{}, fmt.Errorf("%d:%d: klain:http has no method '%s' (supported: listen, close, closeAllConnections) — the Node client/server surface (createServer/get/request) is under 'http'", ex.GetPos().Line, ex.GetPos().Col, mem.Property)
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "https__kml_builtin" {
			switch mem.Property {
			case "get", "request":
				// TLS is the libcurl client's native ground — same emitter as
				// http.get/request; the options-object form composes https URLs.
				return e.emitHTTPClientGetScheme(ex.Args, ex.GetPos(), "https", mem.Property == "request")
			case "createServer":
				// HTTPS/1.1 server (TDD-00111): a TLS-wrapped accept path serving
				// the same (req,res) core as http.createServer over the SSL shims.
				return e.emitHTTPSCreateServer(ex.Args, ex.GetPos())
			}
			return Value{}, fmt.Errorf("%d:%d: https has no method '%s' (supported: get, request)", ex.GetPos().Line, ex.GetPos().Col, mem.Property)
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "console" && !e.isShadowedByLocal(id.Name) {
			switch mem.Property {
			case "log", "info", "debug":
				return e.emitConsolePrint(ex.Args, 1, "")
			case "error":
				return e.emitConsolePrint(ex.Args, 2, "")
			case "warn":
				// Real console.warn prints the arguments to stderr with no
				// prefix of any kind — identical to console.error.
				return e.emitConsolePrint(ex.Args, 2, "")
			case "trace":
				return e.emitConsolePrint(ex.Args, 2, "Trace: ")
			case "assert":
				return e.emitConsoleAssert(ex.Args, ex.GetPos())
			case "dir":
				return e.emitConsoleDir(ex.Args, ex.GetPos())
			case "time":
				return e.emitConsoleTime(ex.Args, ex.GetPos())
			case "timeEnd":
				return e.emitConsoleTimeEnd(ex.Args, ex.GetPos())
			case "count":
				return e.emitConsoleCount(ex.Args, ex.GetPos())
			case "countReset":
				return e.emitConsoleCountReset(ex.Args, ex.GetPos())
			case "group", "groupCollapsed":
				return e.emitConsoleGroup(ex.Args, ex.GetPos())
			case "groupEnd":
				return e.emitConsoleGroupEnd(ex.Args, ex.GetPos())
			case "table":
				return e.emitConsoleTable(ex.Args, ex.GetPos())
			}
		}
		// TDD-00101: a BigInt64Array/BigUint64Array supports only an explicit
		// allow-list of array methods — the generic HOF/search/sort/mutator
		// machinery passes raw i64 scalars into callbacks and comparisons, so
		// any unlisted method is rejected here rather than silently surfacing
		// a raw scalar as if it were a bigint.
		if bigIntElemRejectedMethods[mem.Property] && e.inferExprType(mem.Object).BigIntElem {
			return Value{}, fmt.Errorf("%d:%d: .%s() is not supported on a BigInt64Array/BigUint64Array (supported: indexing, .length/.byteLength, .at/.set/.subarray/.slice/.fill/.reverse, for-of, Atomics.*)", ex.GetPos().Line, ex.GetPos().Col, mem.Property)
		}
		// An Array method on an `any`: the run-time array's own, else the
		// receiver's property (emitDynArrayMethodCall).
		if _, ok := dynArrayMethods[mem.Property]; ok && isUnconstrainedDynamic(e.inferExprType(mem.Object)) {
			objVal, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitDynAnyMethodCall(objVal, mem.Property, ex.Args, ex.GetPos())
		}
		// Name-dispatched fallbacks of the Array intrinsics (TDD-00230 P3.2),
		// for a receiver no Array declaration reaches (a Buffer's own
		// declarations, an undeclared method).
		switch mem.Property {
		case "push":
			return e.emitPush(mem, ex.Args, ex.GetPos())
		case "pop":
			return e.emitPop(mem, ex.Args, ex.GetPos())
		case "shift":
			return e.emitShift(mem, ex.Args, ex.GetPos())
		case "unshift":
			return e.emitUnshift(mem, ex.Args, ex.GetPos())
		case "splice":
			return e.emitSplice(mem, ex.Args, ex.GetPos())
		}
		// SharedArrayBuffer.grow / ArrayBuffer.resize (ADR-00494) — only on
		// buffers constructed with {maxByteLength}.
		if mem.Property == "grow" || mem.Property == "resize" {
			if objTy := e.inferExprType(mem.Object); objTy.IsArrayBuffer {
				objVal, err := e.emitExpr(mem.Object)
				if err != nil {
					return Value{}, err
				}
				return e.emitBufferGrow(objVal, ex.Args, ex.GetPos(), mem.Property == "resize")
			}
		}
		if mem.Property == "slice" {
			objTy := e.inferExprType(mem.Object)
			if objTy.IsBlob {
				return e.emitBlobCall(mem, "slice", ex.Args, ex.GetPos())
			}
			if objTy.IsArrayBuffer {
				return e.emitArrayBufferSlice(mem, ex.Args, ex.GetPos())
			}
			if objTy.IsArray {
				return e.emitArraySlice(mem, ex.Args, ex.GetPos())
			}
		}
		// Buffer.indexOf/includes/lastIndexOf with a STRING argument searches the
		// needle's bytes over the buffer (number args stay on the shared array
		// path). Checked before the generic array dispatch below (ADR-00558).
		if mem.Property == "indexOf" || mem.Property == "includes" || mem.Property == "lastIndexOf" {
			if objTy := e.inferExprType(mem.Object); objTy.IsBuffer && len(ex.Args) >= 1 && isStringTy(e.inferExprType(ex.Args[0])) {
				return e.emitBufferStringSearch(mem, mem.Property, ex.Args, ex.GetPos())
			}
		}
		if e.inferExprType(mem.Object).IsArray {
			switch mem.Property {
			case "indexOf":
				return e.emitArrayIndexOf(mem, ex.Args, ex.GetPos())
			case "lastIndexOf":
				return e.emitArrayLastIndexOf(mem, ex.Args, ex.GetPos())
			case "includes":
				return e.emitArrayIncludes(mem, ex.Args, ex.GetPos())
			case "at":
				return e.emitArrayAt(mem, ex.Args, ex.GetPos())
			}
		}
		if mem.Property == "concat" {
			objTy := e.inferExprType(mem.Object)
			if objTy.IsArray {
				return e.emitArrayConcat(mem, ex.Args, ex.GetPos())
			}
			if isForOfStringTy(objTy) {
				return e.emitStringConcatMethod(mem, ex.Args, ex.GetPos())
			}
		}
		switch mem.Property {
		case "reverse":
			return e.emitArrayReverse(mem, ex.Args, ex.GetPos())
		case "toReversed":
			return e.emitArrayToReversed(mem, ex.Args, ex.GetPos())
		case "toSorted":
			return e.emitArrayToSorted(mem, ex.Args, ex.GetPos())
		case "toSpliced":
			return e.emitArrayToSpliced(mem, ex.Args, ex.GetPos())
		case "with":
			return e.emitArrayWith(mem, ex.Args, ex.GetPos())
		case "copyWithin":
			return e.emitArrayCopyWithin(mem, ex.Args, ex.GetPos())
		case "findIndex":
			return e.emitArrayFindIndex(mem, ex.Args, ex.GetPos())
		case "findLast":
			return e.emitArrayFindLast(mem, ex.Args, ex.GetPos())
		case "findLastIndex":
			return e.emitArrayFindLastIndex(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "fill" {
			// Buffer.fill(string, ...) repeats the needle's bytes (ADR-00559);
			// number fills stay on the shared array path.
			if objTy := e.inferExprType(mem.Object); objTy.IsBuffer && len(ex.Args) >= 1 && isStringTy(e.inferExprType(ex.Args[0])) {
				return e.emitBufferStringFill(mem, ex.Args, ex.GetPos())
			}
			return e.emitArrayFill(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "toLocaleString" && isNumberTy(e.inferExprType(mem.Object)) {
			return e.emitNumberToLocaleString(mem, ex.Args, ex.GetPos())
		}
		switch mem.Property {
		case "search":
			return e.emitStringSearch(mem, ex.Args, ex.GetPos())
		case "localeCompare":
			return e.emitStringLocaleCompare(mem, ex.Args, ex.GetPos())
		case "replace":
			return e.emitStringReplace(mem, ex.Args, ex.GetPos())
		case "replaceAll":
			return e.emitStringReplaceAll(mem, ex.Args, ex.GetPos())
		case "split":
			return e.emitStringSplit(mem, ex.Args, ex.GetPos())
		case "map":
			return e.emitArrayMap(mem, ex.Args, ex.GetPos())
		case "filter":
			return e.emitArrayFilter(mem, ex.Args, ex.GetPos())
		case "reduce":
			return e.emitArrayReduce(mem, ex.Args, ex.GetPos(), false)
		case "reduceRight":
			return e.emitArrayReduce(mem, ex.Args, ex.GetPos(), true)
		case "find":
			return e.emitArrayFind(mem, ex.Args, ex.GetPos())
		case "some":
			return e.emitArraySome(mem, ex.Args, ex.GetPos())
		case "every":
			return e.emitArrayEvery(mem, ex.Args, ex.GetPos())
		case "join":
			return e.emitArrayJoin(mem, ex.Args, ex.GetPos())
		case "sort":
			return e.emitArraySort(mem, ex.Args, ex.GetPos())
		case "flat":
			return e.emitArrayFlat(mem, ex.Args, ex.GetPos())
		case "flatMap":
			return e.emitArrayFlatMap(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "match" {
			return e.emitStringMatch(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "matchAll" {
			return e.emitStringMatchAll(mem, ex.Args, ex.GetPos())
		}
		// Buffer instance methods (TDD-00103) — checked before the generic
		// string/array chains can claim .toString/.write/.copy/.equals/
		// .compare; everything not named here (indexing, .fill, .indexOf,
		// .slice, HOFs, …) deliberately falls through to the shared
		// TypedArray/array machinery.
		if isBufferMethodName(mem.Property) && e.inferExprType(mem.Object).IsBuffer {
			return e.emitBufferInstanceCall(mem, mem.Property, ex.Args, ex.GetPos())
		}
		// Blob-only methods (TDD-00102) — checked before Response's own
		// .arrayBuffer()/.text() dispatch below can claim the same names.
		if e.inferExprType(mem.Object).IsBlob {
			switch mem.Property {
			case "arrayBuffer", "bytes", "text", "stream":
				return e.emitBlobCall(mem, mem.Property, ex.Args, ex.GetPos())
			}
		}
		// DataView accessors (getInt16/setFloat64/..., emit_dataview.go).
		if op, kind, ok := dataViewMethodKind(mem.Property); ok {
			if e.inferExprType(mem.Object).IsDataView {
				if op == "get" {
					return e.emitDataViewGet(mem, kind, ex.Args, ex.GetPos())
				}
				return e.emitDataViewSet(mem, kind, ex.Args, ex.GetPos())
			}
		}
		// TypedArray-only methods. TypedArray IS a plain array (IsArray/
		// ElemType — see IsTypedArray's doc comment), so indexing/.length/
		// .fill/.slice/.reverse/.at/.indexOf/.includes/.map/.filter/
		// .reduce/.forEach/.some/.every/for-of/.keys()/.values()/.entries()
		// all already dispatch correctly via the unguarded array-property
		// checks above and the generic array-HOF checks below with zero
		// changes; only these two names (no `number[]` equivalent to
		// collide with) need TypedArray-specific behavior.
		if objTy := e.inferExprType(mem.Object); objTy.IsTypedArray {
			switch mem.Property {
			case "set":
				return e.emitTypedArraySet(mem, ex.Args, ex.GetPos())
			case "subarray":
				return e.emitTypedArraySubarray(mem, ex.Args, ex.GetPos())
			}
		}
		// XMLHttpRequest-only methods (TDD-00040).
		if e.inferExprType(mem.Object).IsXHR {
			switch mem.Property {
			case "open":
				return e.emitXHROpen(mem.Object, ex.Args, ex.GetPos())
			case "setRequestHeader":
				return e.emitXHRSetRequestHeader(mem.Object, ex.Args, ex.GetPos())
			case "send":
				return e.emitXHRSend(mem.Object, ex.Args, ex.GetPos())
			case "abort":
				return e.emitXHRAbort(mem.Object, ex.Args, ex.GetPos())
			case "getResponseHeader":
				return e.emitXHRGetResponseHeader(mem.Object, ex.Args, ex.GetPos())
			case "getAllResponseHeaders":
				return e.emitXHRGetAllResponseHeaders(mem.Object, ex.Args, ex.GetPos())
			}
		}
		// Headers-only methods (TDD-00040), checked before the generic Map
		// dispatch right below — same "narrower flag first" ordering
		// IsURLSearchParams already establishes just above (Headers IS a
		// Map<string,string> too: get/set/has/delete are case-insensitive,
		// append has no Map equivalent, and forEach/entries/keys/values are
		// the backing Map's).
		if objTy := e.inferExprType(mem.Object); objTy.IsHeaders {
			switch mem.Property {
			case "get", "set", "has", "delete", "append":
				return e.emitHeadersCall(mem.Object, mem.Property, ex.Args, ex.GetPos())
			case "forEach", "entries", "keys", "values":
				// The rest of the surface is the backing Map<string, string>'s.
				ty, ptr, err := e.resolveMapOrSetForCall(mem.Object, ex.GetPos())
				if err != nil {
					return Value{}, err
				}
				return e.emitMapCall(ty, ptr, mem.Property, ex.Args, ex.GetPos())
			}
		}
		// Map<K,V> and Set<T> method dispatch. Checked before the generic
		// "forEach" name below, since both Array and Map/Set have a
		// forEach — the array codegen must not run for a Map/Set receiver.
		// Not limited to a plain named variable (`m.get(...)`) — a cheap
		// inferExprType pre-check (no side effects, same idiom "slice"/
		// "indexOf"/"at" already use to disambiguate array vs. string) also
		// catches a Map/Set-typed field access, array index, or call result
		// (e.g. `c.scores.get(...)` where `scores: Map<K,V>`), which
		// resolveMapOrSetForCall then evaluates for real.
		// Weak collections (TDD-00112) — checked before the plain Map/Set
		// dispatch below, since WeakMap/WeakSet also carry IsMap/IsSet. WeakRef
		// carries neither, so it gets its own check.
		if objTy := e.inferExprType(mem.Object); objTy.IsWeakRef {
			ptr, err := e.resolveWeakRefForCall(mem.Object, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			return e.emitWeakRefCall(objTy, ptr, mem.Property, ex.Args, ex.GetPos())
		}
		// A Map or Set the checker has no declaration for: a klain: module's
		// compiler-typed value (klain:http's HttpRequest.query), until those
		// modules are declared (TDD-00230 ports them last).
		if objTy := e.inferExprType(mem.Object); objTy.IsMap || objTy.IsSet {
			ty, ptr, err := e.resolveMapOrSetForCall(mem.Object, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			switch {
			case ty.Weak:
				return e.emitWeakCall(ty, ptr, mem.Property, ex.Args, ex.GetPos())
			case ty.IsMap:
				return e.emitMapCall(ty, ptr, mem.Property, ex.Args, ex.GetPos())
			}
			return e.emitSetCall(ty, ptr, mem.Property, ex.Args, ex.GetPos())
		}
		if objTy := e.inferExprType(mem.Object); objTy.IsCollIter {
			return e.emitCollIterCall(mem, ex.Args, ex.GetPos())
		}
		// gen.next(value) (TDD-00061/ADR-00172) — gated the same way every
		// other type-tag-dispatched method above is, before the unguarded
		// generic chain below (a `.next` name has no other meaning
		// elsewhere in this compiler today, but matching the established
		// pattern here rather than assuming that stays true).
		if gt := e.inferExprType(mem.Object); gt.IsGenerator && (mem.Property == "next" || mem.Property == "throw" || mem.Property == "return") {
			return e.emitGeneratorUserCall(mem, gt, ex.Args, ex.GetPos())
		}
		if mem.Property == "forEach" {
			return e.emitArrayForEach(mem, ex.Args, ex.GetPos())
		}
		// arr.keys()/.values()/.entries() — same names Map/Set already use
		// above (handled there for Map/Set receivers), so guard on IsArray
		// the same way "slice"/"indexOf"/"at" already disambiguate against
		// their string-method namesakes.
		if mem.Property == "keys" && e.inferExprType(mem.Object).IsArray {
			return e.emitArrayKeys(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "values" && e.inferExprType(mem.Object).IsArray {
			return e.emitArrayValues(mem, ex.Args, ex.GetPos())
		}
		if mem.Property == "entries" && e.inferExprType(mem.Object).IsArray {
			return e.emitArrayEntries(mem, ex.Args, ex.GetPos())
		}
		// Function.prototype.call / .apply on a first-class function value
		// (TDD-00137 Stage A): fn.call(thisArg, a, b) and fn.apply(thisArg,
		// [a, b]) lower to a direct fn(a, b). Checked before the generic
		// field-call below so `fn.call`/`fn.apply` aren't misread as calling a
		// field literally named "call"/"apply".
		if (mem.Property == "call" || mem.Property == "apply") && e.inferExprType(mem.Object).IsFunc {
			return e.emitFunctionCallApply(mem.Object, mem.Property, ex.Args, ex.GetPos())
		}
		// Function.prototype.bind (TDD-00137 Stage C): fn.bind(thisArg, …bound)
		// returns a new partially-applied function value.
		if mem.Property == "bind" && e.inferExprType(mem.Object).IsFunc {
			return e.emitFunctionBind(mem.Object, ex.Args, ex.GetPos())
		}
		// Calling a function-typed object field: obj.callback(...), none of
		// the hardcoded built-in method names above matched, so treat mem as
		// a plain value expression and call it as a closure if its static
		// type says it is one.
		if mt := e.inferExprType(mem); mt.IsFunc {
			return e.emitFuncFieldCall(ex, mem, mt)
		}
	}

	// `obj[key](...)` on a bare any/unknown receiver: a dynamic method call,
	// the receiver passed as `this`.
	if idxEx, ok := ex.Callee.(*ast.IndexExpression); ok && !idxEx.Optional && isUnconstrainedDynamic(e.inferExprType(idxEx.Object)) {
		objVal, err := e.emitExpr(idxEx.Object)
		if err != nil {
			return Value{}, err
		}
		keyRef, err := e.dynAnyKeyRef(idxEx.Index, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
		fnBox, err := e.emitDynAnyMemberGet(objVal, keyRef, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
		argv, n, err := e.emitDynArgv(ex.Args, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
		return e.emitDynFnBoxCallN(fnBox, objVal, argv, n, forOfIterableName(idxEx)+" is not a function", ex.GetPos())
	}
	// Calling a function value stored in an array element: arr[i](...).
	if idxEx, ok := ex.Callee.(*ast.IndexExpression); ok {
		if e.inferExprType(idxEx).IsFunc {
			idxVal, err := e.emitExpr(idxEx)
			if err != nil {
				return Value{}, err
			}
			return e.emitClosureCallByPtr(idxVal.Ref, idxVal.Ty, ex.Args, ex.GetPos())
		}
	}

	// Global built-in functions.
	if id, ok := ex.Callee.(*ast.Identifier); ok && !e.isShadowedByLocal(id.Name) {
		switch id.Name {
		case "String":
			return e.emitGlobalStringConv(ex.Args, ex.GetPos())
		case "Number":
			return e.emitGlobalNumberConv(ex.Args, ex.GetPos())
		case "Boolean":
			return e.emitGlobalBooleanConv(ex.Args, ex.GetPos())
		case "fetch":
			return e.emitFetch(ex.Args, ex.GetPos())
		case "queueMicrotask":
			return e.emitQueueMicrotask(ex.Args, ex.GetPos())
		case "setTimeout":
			return e.emitSetTimeout(ex.Args, ex.GetPos())
		case "setInterval":
			return e.emitSetInterval(ex.Args, ex.GetPos())
		case "setImmediate":
			return e.emitSetImmediate(ex.Args, ex.GetPos())
		case "clearTimeout":
			return e.emitClearTimer(ex.Args, "clearTimeout", ex.GetPos())
		case "clearInterval":
			return e.emitClearTimer(ex.Args, "clearInterval", ex.GetPos())
		case "clearImmediate":
			return e.emitClearTimer(ex.Args, "clearImmediate", ex.GetPos())
		case "gc":
			return e.emitGlobalGC(ex.Args, ex.GetPos())
		case "structuredClone":
			return e.emitStructuredClone(ex.Args, ex.GetPos())
		case "Symbol":
			return e.emitSymbolConstructor(ex.Args, ex.GetPos())
		case "BigInt":
			return e.emitBigIntConstructor(ex.Args, ex.GetPos())
		}
	}

	// Immediately-invoked arrow function: ((x: number) => x+1)(5)
	if af, ok := ex.Callee.(*ast.ArrowFunction); ok {
		closureVal, err := e.emitArrowFunction(af)
		if err != nil {
			return Value{}, err
		}
		return e.emitClosureCallByPtr(closureVal.Ref, closureVal.Ty, ex.Args, ex.GetPos())
	}

	// Immediately-invoked function expression: (function(x: number) { return x+1; })(5)
	if fe, ok := ex.Callee.(*ast.FunctionExpression); ok {
		closureVal, err := e.emitFunctionExpression(fe, nil)
		if err != nil {
			return Value{}, err
		}
		// Default/omitted-parameter filling for the IIFE (originally ADR-00911's
		// own call-site padding, now subsumed): the closure value carries its
		// parameter defaults on its Type (setFuncParamDefaults), and
		// emitClosureCallByPtr fills any omitted trailing parameter from them —
		// or with `undefined` when there is no default — so `(function(f = 123)
		// {...})()` no longer emits a `call` with fewer operands than the
		// callee's arity (TDD-00206 Stage 1). No call-site padding needed here.
		return e.emitClosureCallByPtr(closureVal.Ref, closureVal.Ty, ex.Args, ex.GetPos())
	}

	// Call via bare identifier: named function or closure variable.
	if id, ok := ex.Callee.(*ast.Identifier); ok {
		// Generator construction (TDD-00061/ADR-00172, top-level only in
		// V1) — checked before the ordinary named-function dispatch just
		// below, since a generator is never entered into e.funcs/
		// resolveFuncRef at all: calling one doesn't emit an ordinary
		// `call`, it builds a fiber-backed instance struct instead.
		if info, found := e.lookupGenerator(id.Name); found {
			return e.emitGeneratorConstruction(info, ex.Args, ex.GetPos())
		}
		// Named function — a nested one (TDD-00057) shadows an outer/
		// top-level function of the same name, same as real JS/TS scoping.
		if mangled, sig, found := e.resolveFuncRef(id.Name); found {
			// A recognized prototype constructor's ABI carries a hidden boxed
			// `this` — a plain call has no receiver to pass (JS strict-mode
			// `this === undefined` inside would make every `this.x =` throw
			// anyway), so it stays a clean rejection.
			if e.compatJS() && e.jsProtoCtor[id.Name] {
				return Value{}, fmt.Errorf("%d:%d: '%s' is a prototype constructor — call it with `new %s(...)` (or chain it via `%s.call(this, ...)`)", ex.GetPos().Line, ex.GetPos().Col, id.Name, id.Name, id.Name)
			}
			return e.emitCallToFuncSig(mangled, sig, ex.Args, ex.GetPos())
		}
		// Generic (TDD-00010 V1) function: infer the type argument from
		// whichever call-site argument lines up with the generic's own
		// type-parameter-typed parameter, instantiate (or reuse a memoized
		// prior instantiation) on demand, then dispatch exactly like a
		// concrete named function.
		if decl, found := e.genericFuncs[id.Name]; found {
			return e.emitGenericFuncCall(decl, ex.Args, ex.TypeArgs, ex.GetPos())
		}
		// Closure variable — including a named function expression's own
		// self-reference binding (TDD-00060).
		if sym, found := e.lookup(id.Name); found && sym.Ty.IsFunc {
			return e.emitClosureCall(sym, ex.Args, ex.GetPos())
		}
		// A union-typed binding a guard narrowed to its function member
		// (`typeof o === 'function'`): its unboxed closure, called.
		if sym, found := e.lookup(id.Name); found && sym.NarrowedTo != nil && sym.NarrowedTo.IsFunc {
			fv, err := e.emitExpr(id)
			if err != nil {
				return Value{}, err
			}
			return e.emitClosureCallByPtr(fv.Ref, fv.Ty, ex.Args, ex.GetPos())
		}
		// A variable holding an `any`/`unknown` value, called: the runtime
		// dynamic call (a non-function box throws JS's "is not a function").
		// Reached before this only through a member/expression callee
		// (TDD-00229).
		if sym, found := e.lookup(id.Name); found && isUnconstrainedDynamic(sym.Ty) {
			fv, err := e.emitExpr(id)
			if err != nil {
				return Value{}, err
			}
			return e.emitDynCallValue(fv, ex.Args, ex.GetPos())
		}
		// Static-string eval fast path (TDD-00046 static subset): a
		// compile-time-constant `eval("<expression>")` is compiled through
		// this compiler's own parser + codegen, in place — no embedded
		// engine. Checked after every user-binding lookup, so a
		// user-defined function named `eval` still wins.
		if id.Name == "eval" {
			return e.emitStaticEval(ex.Args, ex.GetPos())
		}
		// Sibling namespace member call by bare name from inside a member
		// function body (TDD-00148 Stage 4) — retried under the mangled
		// name. Checked after every user-binding lookup, so locals shadow.
		if m := e.nsSibling(id.Name); m != "" {
			rewritten := ast.NewCallExpression(ast.NewIdentifier(m, ex.GetPos()), ex.Args, ex.GetPos())
			return e.emitCall(rewritten)
		}
		// TDD-00129 Stage 2: a capturing nested function declaration called
		// before its declaration statement hoists — emit it on demand here,
		// then dispatch as the closure value it now is.
		if ok, lerr := e.letrecEmitCapturingNested(id.Name); lerr != nil {
			return Value{}, lerr
		} else if ok {
			if sym, found := e.lookup(id.Name); found && sym.Ty.IsFunc {
				return e.emitClosureCall(sym, ex.Args, ex.GetPos())
			}
		}
		// Node's function-style constructors (`http.Server(…)`,
		// `stream.Readable(…)`) construct when called without `new`.
		if e.callableClasses[id.Name] {
			return e.emitExpr(ast.NewNewExpression(id.Name, ex.Args, ex.GetPos()))
		}
		return Value{}, fmt.Errorf("%d:%d: undefined function or closure '%s'", ex.GetPos().Line, ex.GetPos().Col, id.Name)
	}

	// TDD-00049: a friendlier diagnostic for the single most likely cause of
	// reaching this fallback — writing e.g. `fs.readFileSync(...)` without
	// the now-required `import fs from 'fs'`. Checked last, only once every
	// real dispatch path (including a legitimately-imported builtin, which
	// never reaches here at all) has already failed to match.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if id, ok := mem.Object.(*ast.Identifier); ok {
			if specifier, known := builtinModuleSpecifiers[id.Name]; known {
				return Value{}, fmt.Errorf("%d:%d: '%s' is not defined — did you forget \"import %s from '%s'\"?",
					ex.GetPos().Line, ex.GetPos().Col, id.Name, id.Name, specifier)
			}
		}
	}

	// General fallback: call the result of any other expression whose
	// static type is a function value — `f()()`, `(cond ? f : g)()`,
	// `obj.getHandler()()`, a parenthesized expression of any of the
	// above (parens have no wrapper node in this parser, so the callee
	// here is already whatever was inside them), and so on. The dispatch
	// mechanism itself (emitClosureCallByPtr) already handles any
	// function-typed value regardless of which expression shape produced
	// it — every branch above this one is a narrower, more specific
	// pattern checked first only because it can skip the general
	// inferExprType call, not because the general path can't handle it
	// too. Checked last so a more specific/helpful error (like the
	// import-forgot diagnostic just above) still wins when both could
	// apply.
	if e.inferExprType(ex.Callee).IsFunc {
		val, err := e.emitExpr(ex.Callee)
		if err != nil {
			return Value{}, err
		}
		return e.emitClosureCallByPtr(val.Ref, val.Ty, ex.Args, ex.GetPos())
	}

	// A method call on a bare any/unknown receiver is a runtime prototype
	// dispatch (TDD-00155 Stage 4): chain-walking property read, then an
	// indirect call through the dynamic-function record with the receiver
	// bound as `this`.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if objTy := e.inferExprType(mem.Object); isUnconstrainedDynamic(objTy) {
			objVal, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitDynAnyMethodCall(objVal, mem.Property, ex.Args, ex.GetPos())
		}
	}

	// A method call whose receiver type doesn't have that method reaches here
	// too (the callee `obj.prop` isn't function-typed). Report it as the real
	// missing-method gap it is — a far more useful diagnostic than the generic
	// "only simple function calls" fallback, and (for the conformance leverage
	// map) it splits that catch-all bucket by the receiver's type instead of
	// lumping every unrecognized method call together.
	// A timer handle's methods (NodeJS.Timeout / Immediate): unref, ref,
	// hasRef, close.
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok && e.isTimerHandle(mem.Object) {
		switch mem.Property {
		case "unref", "ref", "hasRef", "close":
			h, err := e.emitExpr(mem.Object)
			if err != nil {
				return Value{}, err
			}
			id := e.coerce(h, TypeI64)
			e.ensureTimerRuntime()
			switch mem.Property {
			case "unref", "ref":
				on := "true"
				if mem.Property == "unref" {
					on = "false"
				}
				e.emitInstr(fmt.Sprintf("call void @__kml_timer_set_ref(i64 %s, i1 %s)", id.Ref, on))
				return h, nil
			case "hasRef":
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_timer_has_ref(i64 %s)", r, id.Ref))
				return Value{Ref: r, Ty: TypeBool}, nil
			default:
				e.emitInstr(fmt.Sprintf("call void @__kml_timer_clear(i64 %s)", id.Ref))
				return h, nil
			}
		}
	}
	// A function's own function-valued property the checker did not see
	// declared (`f.p(…)`), when nothing else claimed the call.
	if r, ok := e.funcOwnPropCall(ex); ok {
		return e.emitCall(r)
	}
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		recv := describeReceiverType(e.inferExprType(mem.Object))
		// A bare namespace/global identifier receiver (Object.setPrototypeOf,
		// process.send, cluster.fork, Math.foo, …) infers as the i64 default;
		// name it explicitly so the diagnostic — and the conformance histogram —
		// attributes the missing member to the right namespace.
		if id, ok := mem.Object.(*ast.Identifier); ok && !e.isShadowedByLocal(id.Name) {
			if n := strings.TrimSuffix(id.Name, "__kml_builtin"); n != id.Name {
				recv = n
			} else if knownGlobalNamespace[id.Name] {
				recv = id.Name
			}
		}
		return Value{}, fmt.Errorf("%d:%d: %s has no method '%s'", ex.GetPos().Line, ex.GetPos().Col, recv, mem.Property)
	}

	// A callee that is an arbitrary expression producing a callable value — a
	// factory-returned decorator `@tag(...)` (which lowers to `(tag(...))(args)`,
	// TDD-00161), `getHandler()(x)`, or a function pulled out of an any-typed
	// value — is evaluated and called through. Last, after every named-function/
	// method/closure-variable/arrow/IIFE form above, so those keep their specific
	// paths and diagnostics; member callees already returned above.
	if calleeTy := e.inferExprType(ex.Callee); calleeTy.IsFunc || calleeTy.IsDynamic {
		calleeVal, err := e.emitExpr(ex.Callee)
		if err != nil {
			return Value{}, err
		}
		if calleeVal.Ty.IsFunc {
			return e.emitClosureCallByPtr(calleeVal.Ref, calleeVal.Ty, ex.Args, ex.GetPos())
		}
		return e.emitDynCallValue(calleeVal, ex.Args, ex.GetPos())
	}

	return Value{}, fmt.Errorf("%d:%d: only simple function calls are supported (the callee is not a named function, a function value, or a supported method)", ex.GetPos().Line, ex.GetPos().Col)
}

// knownGlobalNamespace lists the bare-identifier globals/namespaces whose
// members are static (so a receiver of this name infers as the numeric default,
// not a real value) — named explicitly in the "no method" diagnostic.
var knownGlobalNamespace = map[string]bool{
	"Object": true, "Math": true, "JSON": true, "Reflect": true, "Number": true,
	"Array": true, "String": true, "Boolean": true, "Symbol": true, "Promise": true,
	"process": true, "console": true, "Date": true, "RegExp": true, "Proxy": true,
}

// describeReceiverType names a value's type for a "<type> has no method 'x'"
// diagnostic — short human phrases for the common builtin/receiver types, so
// the error (and the conformance histogram it feeds) attributes a missing
// method to the right type rather than a generic catch-all.
func describeReceiverType(ty Type) string {
	switch {
	case ty.IsBuffer:
		return "Buffer"
	case ty.IsTypedArray:
		return "a TypedArray"
	case ty.IsArrayBuffer:
		return "an ArrayBuffer"
	case ty.IsDataView:
		return "a DataView"
	case ty.IsArray:
		return "an array"
	case ty.IsMap:
		return "a Map"
	case ty.IsSet:
		return "a Set"
	case ty.IsPromise:
		return "a Promise"
	case ty.IsDate:
		return "a Date"
	case ty.IsRegExp:
		return "a RegExp"
	case ty.IsBigInt:
		return "a bigint"
	case ty.IsNetSocket:
		return "a net socket"
	case ty.IsChildProcess:
		return "a ChildProcess"
	case ty.IsResponse:
		return "a Response"
	case ty.IsRequest:
		return "a Request"
	case ty.IsClass:
		return "class '" + ty.ClassName + "'"
	case isStringTy(ty):
		return "a string"
	case ty.IsObject:
		return "an object"
	case ty.IR != "ptr":
		return "a number"
	}
	return "a value of this type"
}

// builtinModuleSpecifiers maps the conventional bare identifier name a
// program would write for a built-in module (fs.*, path.*, ...) to the
// virtual specifier it must now be imported from (TDD-00049) — used only to
// build a helpful "did you forget to import this?" diagnostic above, not
// for any real dispatch decision (that's resolver/virtual_modules.go's
// job, entirely before codegen ever runs).
var builtinModuleSpecifiers = map[string]string{
	"fs":          "fs",
	"path":        "path",
	"os":          "os",
	"querystring": "querystring",
	"assert":      "assert",
	"http":        "http",
	"cluster":     "cluster",
	"Memory":      "memory",
}

// emitCallToFuncSig emits a call to name (a concrete, already-registered
// LLVM function — either a plain top-level function or a TDD-00010 V1
// generic function's specific instantiation) against sig, evaluating args
// and applying the same per-parameter rules a named top-level call always
// has: array-parameter special handling, per-parameter coercion, an
// unannotated ("Inferred") parameter rejecting a non-numeric argument,
// default-expression fallback for a missing trailing argument, and rest-
// parameter packing into a temporary heap array.
// checkSpreadArgs enforces TDD-00106's V1 spread rule: at most one spread
// argument, which must be the last argument and land exactly on a rest
// parameter (after the fixed arguments). Returns nil when there is no spread.
// singleSpread reports whether restArgs is exactly one spread argument
// (`f(...arr)`), returning it — the case a rest slot forwards directly.
func singleSpread(restArgs []ast.Expression) (*ast.SpreadElement, bool) {
	if len(restArgs) == 1 {
		if sp, ok := restArgs[0].(*ast.SpreadElement); ok {
			return sp, true
		}
	}
	return nil, false
}

// anySpread reports whether restArgs contains at least one spread argument —
// used to pick the runtime-concat rest buffer (TDD-00106 V2) over the plain
// malloc-and-store-N-scalars path a spread-free trailing arg list uses.
func anySpread(restArgs []ast.Expression) bool {
	for _, a := range restArgs {
		if _, ok := a.(*ast.SpreadElement); ok {
			return true
		}
	}
	return false
}

// emitRestArgBuffer builds the rest-parameter backing buffer for a call whose
// rest region mixes ordinary positional arguments with one or more spread
// arguments (`f(...a, ...b)`, `f(x, ...arr, y)` — TDD-00106 V2). It allocates
// one contiguous buffer sized at runtime (each static arg counts as 1, each
// spread adds its runtime length) and fills it with a write cursor — memcpy per
// spread, store per static arg — returning the (ptr, len) operands the rest ABI
// takes. Every argument is evaluated once, left to right, before any copy, so
// JS evaluation order is preserved. Mirrors emitSpreadArrayLitData's cursor
// technique, but keyed off a call's arg list (resolveArrayForHOF spreads, so an
// array-returning expression works, not only a bare array variable).
func (e *Emitter) emitRestArgBuffer(restArgs []ast.Expression, elemTy Type) (dataReg, lenReg string, err error) {
	type restItem struct {
		spread bool
		ptr    string // spread: source data pointer
		length string // spread: source length register
		val    Value  // static: coerced element value
	}
	// Pass 1: evaluate every argument once, in source order.
	items := make([]restItem, 0, len(restArgs))
	staticCount := int64(0)
	for _, arg := range restArgs {
		if sp, ok := arg.(*ast.SpreadElement); ok {
			ptrReg, srcLenReg, srcElemTy, rerr := e.resolveArrayForHOF(sp.Arg, sp.Arg.GetPos())
			if rerr != nil {
				return "", "", rerr
			}
			if srcElemTy.IR != elemTy.IR || srcElemTy.IsArray != elemTy.IsArray || srcElemTy.IsObject != elemTy.IsObject {
				return "", "", fmt.Errorf("%d:%d: spread array's element type does not match the rest parameter's element type", sp.Arg.GetPos().Line, sp.Arg.GetPos().Col)
			}
			items = append(items, restItem{spread: true, ptr: ptrReg, length: srcLenReg})
		} else {
			val, verr := e.emitExprWithObjectHint(arg, elemTy)
			if verr != nil {
				return "", "", verr
			}
			val = e.coerce(val, elemTy)
			items = append(items, restItem{val: val})
			staticCount++
		}
	}
	// Total length = staticCount + sum(spread lengths).
	totalReg := fmt.Sprintf("%d", staticCount)
	for _, it := range items {
		if !it.spread {
			continue
		}
		nt := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", nt, totalReg, it.length))
		totalReg = nt
	}
	// Allocate one contiguous buffer.
	e.ensureMalloc()
	bytesReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytesReg, totalReg, elemTy.Align()))
	dataReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", dataReg, bytesReg))
	// Fill via a write cursor.
	cursorPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", cursorPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", cursorPtr))
	for _, it := range items {
		cVal := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cVal, cursorPtr))
		dstReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", dstReg, elemTy.IR, dataReg, cVal))
		if it.spread {
			copyBytes := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", copyBytes, it.length, elemTy.Align()))
			e.ensureMemcpy()
			e.emitInstr(fmt.Sprintf("call void @memcpy(ptr %s, ptr %s, i64 %s)", dstReg, it.ptr, copyBytes))
			newC := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newC, cVal, it.length))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newC, cursorPtr))
		} else {
			e.storeArrayElem(dstReg, elemTy, it.val)
			newC := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", newC, cVal))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newC, cursorPtr))
		}
	}
	return dataReg, totalReg, nil
}

func (e *Emitter) checkSpreadArgs(args []ast.Expression, hasRest bool, regularCount int, pos ast.Pos) error {
	// V2 (TDD-00106): any number of spreads, in any order, freely mixed with
	// ordinary positional arguments — but only within the rest region. A spread
	// still cannot fill a fixed parameter slot (that needs a runtime split
	// against static arity), and a callee with no rest parameter can't take a
	// spread at all.
	for i, a := range args {
		sp, ok := a.(*ast.SpreadElement)
		if !ok {
			continue
		}
		if !hasRest {
			return fmt.Errorf("%d:%d: spread requires the called function to have a rest parameter (`...`) — spreading into a fixed-arity function is not supported", sp.Arg.GetPos().Line, sp.Arg.GetPos().Col)
		}
		if i < regularCount {
			return fmt.Errorf("%d:%d: a spread argument may only fill the rest parameter, not a fixed parameter slot — place it after the %d fixed argument(s)", sp.Arg.GetPos().Line, sp.Arg.GetPos().Col, regularCount)
		}
	}
	return nil
}

func (e *Emitter) emitCallToFuncSig(name string, sig FuncSig, args []ast.Expression, pos ast.Pos) (Value, error) {
	// TDD-00134 Stage 3: the destructuring fast path sets wantTupleAggregate
	// right before emitting its (outermost) call; grab-and-clear at entry so
	// a nested call inside an argument expression never sees it.
	keepTupleAgg := e.wantTupleAggregate
	e.wantTupleAggregate = false
	// A may-suspend async function is not called directly — it is spawned as a
	// coroutine task, returning a pending promise (TDD-00083 Stage 2).
	if sig.MaySuspend {
		return e.emitMaySuspendCall(name, sig, args, pos)
	}
	var argParts []string
	// How many args map to regular (non-rest) params.
	regularCount := len(sig.ParamTypes)
	if sig.HasRest {
		regularCount-- // last param slot is the rest array
	}
	// Spread argument (TDD-00106): V1 supports a spread only as the sole filler
	// of a rest parameter, after exactly the fixed arguments — f(...arr),
	// f(a, b, ...arr). Anything else (spread into a fixed-arity callee, a
	// non-last spread, multiple spreads) is a clean error rather than a
	// miscompile now that the parser accepts it in any argument position.
	if err := e.checkSpreadArgs(args, sig.HasRest, regularCount, pos); err != nil {
		return Value{}, err
	}
	// A parameter default may reference an earlier parameter
	// (`function f(a, b = a * 2)`). Defaults are evaluated at the call site, so
	// each earlier *scalar* parameter's final value (whether the passed argument
	// or its own default) is materialized into a scratch symbol keyed by name;
	// scratchSyms is made visible only while a default is emitted (arguments are
	// still evaluated in the caller's scope, seeing no sibling parameters).
	scratchSyms := map[string]Symbol{}
	pushScratch := func() {
		if len(scratchSyms) == 0 {
			return
		}
		e.pushScope()
		for n, s := range scratchSyms {
			e.define(n, s)
		}
	}
	popScratch := func() {
		if len(scratchSyms) > 0 {
			e.popScope()
		}
	}
	for i := 0; i < regularCount; i++ {
		var paramScalar *Value      // set for a scalar/pointer param, to bind for later defaults
		var paramArrayHeader string // set for an array param (its header ptr), to bind for later defaults (ADR-00610)
		var paramNullableAgg string // set for a nullable-scalar param (its {i1,T} agg), to bind for later defaults (ADR-00611)
		var paramTy Type
		if i < len(sig.ParamTypes) {
			paramTy = sig.ParamTypes[i]
		}
		// Use provided arg or fall back to the default expression.
		argProvided := i < len(args) && !(sig.HasRest && i >= regularCount)
		// `f(undefined)` on a defaulted parameter triggers the default: JS treats
		// an explicit `undefined` argument as omitted for default substitution.
		if argProvided {
			if nl, ok := args[i].(*ast.NullLiteral); ok && nl.IsUndefined &&
				i < len(sig.Defaults) && sig.Defaults[i] != nil {
				argProvided = false
			}
		}
		if argProvided {
			arg := args[i]
			if paramTy.IsArray {
				if arrId, ok := arg.(*ast.Identifier); ok && !isSelfDescribingBox(e.inferExprType(arg)) && !e.isDynamicBinding(arrId.Name) {
					sym, ok := e.lookup(arrId.Name)
					if !ok {
						return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", arg.GetPos().Line, arg.GetPos().Col, arrId.Name)
					}
					if !sym.Ty.IsArray {
						return Value{}, fmt.Errorf("%d:%d: '%s' is not an array", arg.GetPos().Line, arg.GetPos().Col, arrId.Name)
					}
					// A concrete array passed as `any[]` (or the reverse): its
					// elements converted, in a copy.
					if toAny, fromAny := arrayElemsConvert(sym.Ty, paramTy); toAny || fromAny {
						// A named array is its two slots: rebuild the aggregate.
						dp, dl, _, err := e.resolveArrayForHOF(arg, arg.GetPos())
						if err != nil {
							return Value{}, err
						}
						a0, a1 := e.freshReg(), e.freshReg()
						e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", a0, dp))
						e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", a1, a0, dl))
						converted, _ := e.coerceArrayElems(Value{Ref: a1, Ty: sym.Ty}, paramTy)
						header, lenReg := e.arrayArgFromAggregate(converted)
						argParts = append(argParts, "ptr "+header, "i64 "+lenReg)
						paramArrayHeader = header
					} else {
						// Object-reference model (TDD-00127): pass the array's header
						// pointer so mutations inside the callee (push/splice)
						// propagate back to this caller. The i64 length is redundant
						// (kept for ABI stability).
						header, lenReg := e.packArrayArg(arg, Value{}, paramTy)
						argParts = append(argParts, "ptr "+header, "i64 "+lenReg)
						paramArrayHeader = header
					}
				} else {
					// Hint-aware (TDD-00028): an array-literal argument
					// (or `new Array<T>(n)` with no explicit `<T>`) is
					// built/coerced against paramTy directly instead of
					// self-inferring its own element type — the exact bug
					// class TDD-00007 already fixed for object literals.
					// Found via a genuinely wrong result (not just a
					// compile error): `sum([1, 2])` against a
					// `float64[]` parameter silently built an i64 array
					// and reinterpreted its raw bit pattern as a double.
					val, err := e.emitExprWithObjectHint(arg, paramTy)
					if err != nil {
						return Value{}, err
					}
					if val.Ty.IsNull && paramTy.Nullable {
						val = e.emitAbsentArrayValue(paramTy) // f(null) / f(undefined)
					}
					if isSelfDescribingBox(val.Ty) {
						// An `any` holding an array (a Buffer, …) unboxes to it.
						val = e.emitUnboxBoxToType(val.Ref, paramTy)
					}
					if !val.Ty.IsArray {
						return Value{}, fmt.Errorf("%d:%d: expression does not yield an array", arg.GetPos().Line, arg.GetPos().Col)
					}
					if converted, ok := e.coerceArrayElems(val, paramTy); ok {
						header, lenReg := e.arrayArgFromAggregate(converted)
						argParts = append(argParts, "ptr "+header, "i64 "+lenReg)
						paramArrayHeader = header
					} else {
						// A member/index/field array shares its live header so callee
						// mutation propagates; a true transient gets a fresh one
						// (TDD-00127).
						header, lenReg := e.packArrayArg(arg, val, paramTy)
						argParts = append(argParts, "ptr "+header, "i64 "+lenReg)
						paramArrayHeader = header
					}
				}
			} else if isNullableScalar(paramTy) {
				// A nullable-scalar parameter takes its boxed { i1, T }
				// aggregate (TDD-00064 Stage 3).
				agg, err := e.emitNullableScalarBoxedValue(arg, paramTy)
				if err != nil {
					return Value{}, err
				}
				argParts = append(argParts, fmt.Sprintf("%s %s", nullableScalarStorageIR(paramTy), agg))
				paramNullableAgg = agg
			} else if v, ok, err := e.emitArgOrDefault(arg, paramTy, sig, i, pushScratch, popScratch); ok || err != nil {
				// A defaulted parameter given an argument that may be
				// undefined at run time: the default when it is.
				if err != nil {
					return Value{}, err
				}
				argParts = append(argParts, fmt.Sprintf("%s %s", v.Ty.IR, v.Ref))
				pv := v
				paramScalar = &pv
			} else {
				val, err := e.emitExprWithObjectHint(arg, paramTy)
				if err != nil {
					return Value{}, err
				}
				if paramTy.Inferred && !isSafeNumericArg(val.Ty) {
					paramName := fmt.Sprintf("%d", i+1)
					if i < len(sig.ParamNames) {
						paramName = "'" + sig.ParamNames[i] + "'"
					}
					return Value{}, fmt.Errorf("%d:%d: parameter %s of '%s' has no type annotation (defaults to number) but was called with a non-numeric argument here — add an explicit type annotation", arg.GetPos().Line, arg.GetPos().Col, paramName, name)
				}
				if paramTy.IsDynamic {
					if paramTy.UnionMembers != nil && !unionAllowsAssignmentFrom(paramTy, val.Ty) {
						paramName := fmt.Sprintf("%d", i+1)
						if i < len(sig.ParamNames) {
							paramName = "'" + sig.ParamNames[i] + "'"
						}
						return Value{}, fmt.Errorf("%d:%d: argument's type is not a member of parameter %s's declared union type", arg.GetPos().Line, arg.GetPos().Col, paramName)
					}
					if paramTy.UnionMembers != nil {
						val = e.relayoutForUnion(val, paramTy)
					}
					// TDD-00010 V2: a call to an `@erased` generic function —
					// coerce (unlike this) has no notion of boxing, it only
					// converts between concrete scalar IR types, so a bare-T
					// param must be boxed explicitly instead.
					if val, err = e.emitBoxValueWidened(val, arg); err != nil {
						return Value{}, err
					}
				} else if paramTy.IR != "" {
					if !coerciblePure(val.Ty, paramTy) {
						return Value{}, fmt.Errorf("%d:%d: argument %d has a type incompatible with the parameter's declared type — this compiler is a typed subset", arg.GetPos().Line, arg.GetPos().Col, i+1)
					}
					val = e.coerce(val, paramTy)
				}
				argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
				pv := val
				paramScalar = &pv
			}
		} else if i < len(sig.Defaults) && sig.Defaults[i] != nil {
			// Evaluate default expression at call site. Array-typed
			// defaults need the same {ptr,i64} -> (ptr, i64) decomposition
			// the direct-arg path above uses — found in passing while
			// wiring optional params below: an array-typed default
			// (`a: number[] = [1,2,3]`) was passing the whole aggregate
			// struct where the callee's LLVM signature expects two scalar
			// params, a hard clang-stage type mismatch, not a silent bug.
			// Earlier scalar parameters are in scope for this default.
			pushScratch()
			if paramTy.IsArray {
				val, err := e.emitExprWithObjectHint(sig.Defaults[i], paramTy)
				if err != nil {
					return Value{}, fmt.Errorf("default value for param %d: %w", i, err)
				}
				if !val.Ty.IsArray {
					return Value{}, fmt.Errorf("default value for param %d does not yield an array", i)
				}
				header, lenReg := e.arrayArgFromAggregate(val)
				argParts = append(argParts, "ptr "+header, "i64 "+lenReg)
				paramArrayHeader = header
			} else if isNullableScalar(paramTy) {
				agg, err := e.emitNullableScalarBoxedValue(sig.Defaults[i], paramTy)
				if err != nil {
					return Value{}, fmt.Errorf("default value for param %d: %w", i, err)
				}
				argParts = append(argParts, fmt.Sprintf("%s %s", nullableScalarStorageIR(paramTy), agg))
				paramNullableAgg = agg
			} else {
				val, err := e.emitExprWithObjectHint(sig.Defaults[i], paramTy)
				if err != nil {
					return Value{}, fmt.Errorf("default value for param %d: %w", i, err)
				}
				if paramTy.IsDynamic {
					if val, err = e.emitBoxValueWidened(val, sig.Defaults[i]); err != nil {
						return Value{}, err
					}
				} else if paramTy.IR != "" {
					val = e.coerce(val, paramTy)
				}
				argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
				pv := val
				paramScalar = &pv
			}
			popScratch()
		} else if i < len(sig.Optional) && sig.Optional[i] {
			// ADR-00164: an omitted `param?: T` argument gets T's zero
			// value, the same undefined stand-in ADR-00157/ADR-00158 use.
			// Array-typed params decompose into two LLVM params (ptr, i64
			// len) at the callee side, so their "zero value" is an empty
			// array (null ptr, 0 len), not a single zeroLiteral() operand.
			// A nullable scalar's omitted value is a genuinely absent
			// { i1, T } aggregate (present = false).
			if paramTy.IsArray {
				argParts = append(argParts, "ptr "+e.omittedArrayArgHeader(paramTy), "i64 0")
			} else if isNullableScalar(paramTy) {
				argParts = append(argParts, nullableScalarStorageIR(paramTy)+" zeroinitializer")
				paramNullableAgg = "zeroinitializer"
			} else if paramTy.IsDynamic {
				// An omitted `param?: any` argument is `undefined` in JS, and an
				// `any` slot is a NaN-boxed word — so it must be the boxed
				// `undefined` sentinel, not a raw i64 zero. A zero word is the
				// boxed integer 0 (read back as a double it is 5e-324), which
				// makes `param === undefined`/`=== null` wrongly false and
				// corrupts any downstream numeric read (ADR-00164 predates the
				// NaN-box `any` model, TDD-00076).
				undef := fmt.Sprintf("%d", nbUndefined)
				argParts = append(argParts, "i64 "+undef)
				pv := Value{Ref: undef, Ty: paramTy}
				paramScalar = &pv
			} else {
				argParts = append(argParts, fmt.Sprintf("%s %s", paramTy.IR, paramTy.zeroLiteral()))
				pv := Value{Ref: paramTy.zeroLiteral(), Ty: paramTy}
				paramScalar = &pv
			}
		} else {
			return Value{}, fmt.Errorf("%d:%d: missing argument %d with no default", pos.Line, pos.Col, i+1)
		}
		// Materialize this parameter's final value into a scratch symbol so a
		// *later* parameter's default can reference it by name.
		if paramScalar != nil && i < len(sig.ParamNames) {
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, paramScalar.Ty.IR, paramScalar.Ty.Align()))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", paramScalar.Ty.IR, paramScalar.Ref, slot, paramScalar.Ty.Align()))
			scratchSyms[sig.ParamNames[i]] = Symbol{Ptr: slot, Ty: paramScalar.Ty}
		}
		// An array parameter binds via a slot holding its header pointer
		// (arrayDataLenSlots derives data/len from it) — so a later default can
		// read `a.length`, `a[i]`, etc. (ADR-00610).
		if paramArrayHeader != "" && i < len(sig.ParamNames) {
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", paramArrayHeader, slot))
			scratchSyms[sig.ParamNames[i]] = Symbol{Ptr: slot, Ty: paramTy}
		}
		// A nullable-scalar parameter binds via its { i1, T } aggregate slot, so a
		// later default can `?? `/`=== null`/narrow it (ADR-00611).
		if paramNullableAgg != "" && i < len(sig.ParamNames) {
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, nullableScalarStorageIR(paramTy), storageAlign(paramTy)))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", nullableScalarStorageIR(paramTy), paramNullableAgg, slot, storageAlign(paramTy)))
			scratchSyms[sig.ParamNames[i]] = Symbol{Ptr: slot, Ty: paramTy, NullableBoxed: true}
		}
	}
	// Pack rest args into a temporary heap array.
	if sig.HasRest {
		restStart := regularCount
		if restStart > len(args) {
			restStart = len(args)
		}
		restArgs := args[restStart:]
		restTy := sig.ParamTypes[len(sig.ParamTypes)-1]
		elemTy := TypeI64
		if restTy.ElemType != nil {
			elemTy = *restTy.ElemType
		}
		if spread, ok := singleSpread(restArgs); ok {
			// f(fixed..., ...arr): forward the array's own (ptr, len) buffer
			// straight into the rest slot — the rest-param ABI is (ptr, i64),
			// exactly what an array argument already lowers to (TDD-00106).
			ptrReg, lenReg, srcElemTy, err := e.resolveArrayForHOF(spread.Arg, spread.Arg.GetPos())
			if err != nil {
				return Value{}, err
			}
			if srcElemTy.IR != elemTy.IR || srcElemTy.IsArray != elemTy.IsArray || srcElemTy.IsObject != elemTy.IsObject {
				return Value{}, fmt.Errorf("%d:%d: spread array's element type does not match the rest parameter's element type", spread.Arg.GetPos().Line, spread.Arg.GetPos().Col)
			}
			restHdr := e.newArrayHeader(ptrReg, lenReg)
			argParts = append(argParts, "ptr "+restHdr, "i64 "+lenReg)
		} else if len(restArgs) == 0 {
			argParts = append(argParts, "ptr "+e.emptyArrayArgHeader(), "i64 0")
		} else if anySpread(restArgs) {
			// f(fixed..., ...a, x, ...b): a runtime-length mix of spreads and
			// positional args feeding the rest slot — concat into one buffer.
			dataReg, lenReg, err := e.emitRestArgBuffer(restArgs, elemTy)
			if err != nil {
				return Value{}, err
			}
			restHdr := e.newArrayHeader(dataReg, lenReg)
			argParts = append(argParts, "ptr "+restHdr, "i64 "+lenReg)
		} else {
			n := int64(len(restArgs))
			e.ensureMalloc()
			dataReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*int64(elemTy.Align())))
			for i, arg := range restArgs {
				val, err := e.emitExprWithObjectHint(arg, elemTy)
				if err != nil {
					return Value{}, err
				}
				val = e.coerce(val, elemTy)
				gepReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataReg, i))
				e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val.Ref, gepReg, elemTy.Align()))
			}
			restHdr := e.newArrayHeader(dataReg, fmt.Sprintf("%d", n))
			argParts = append(argParts, "ptr "+restHdr, fmt.Sprintf("i64 %d", n))
		}
	}
	argsStr := strings.Join(argParts, ", ")
	if sig.RetType.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void @%s(%s)", name, argsStr))
		return Value{Ty: TypeVoid}, nil
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s @%s(%s)", reg, sig.RetType.LLVMRetType(), name, argsStr))
	retTy := sig.RetType
	// A by-value small-tuple result (TDD-00134 Stage 3): hand the raw
	// aggregate to the destructuring fast path (flag kept as its marker);
	// every other consumer gets it spilled back to the heap-pointer shape.
	if retTy.IsTuple && retTy.TupleByVal {
		if keepTupleAgg {
			return Value{Ref: reg, Ty: retTy}, nil
		}
		return e.spillTupleAggregate(reg, retTy), nil
	}
	// A non-suspending async fn (didn't take the MaySuspend fiber path above)
	// now returns a settled task-shaped promise (TDD-00084 Part A) — tag it so
	// `await`/`.then` take the task path, matching the may-suspend result.
	if sig.IsAsync && retTy.IsPromise {
		retTy.PromiseTask = true
	}
	if retTy.IsArray {
		// The array return ABI is a header pointer (TDD-00213 Stage 3): deref it
		// into the {ptr,i64} aggregate and carry the header so the result aliases.
		return e.arrayValueFromHeaderReg(reg, retTy), nil
	}
	return Value{Ref: reg, Ty: retTy}, nil
}

// namespaceMembers resolves name against the TS-namespace table (TDD-00095).
// The resolver rewrites references to a merged function+namespace name with
// its per-file `__kml_modN` suffix (`greet` → `greet__kml_mod0`), while the
// table stays keyed by the source name — so a miss retries with that suffix
// stripped. Returns the member set (or nil) and the source-level namespace
// name to mangle members against.
// nsDottedChain flattens a member-expression chain of pure identifiers
// (`A.B.C`) into its dotted name (TDD-00148 V3). ok is false when any link
// is not a plain identifier/member step.
func nsDottedChain(expr ast.Expression) (string, bool) {
	switch ex := expr.(type) {
	case *ast.Identifier:
		return ex.Name, true
	case *ast.MemberExpression:
		if base, ok := nsDottedChain(ex.Object); ok {
			return base + "." + ex.Property, true
		}
	}
	return "", false
}

// namespaceByChain resolves a nested-namespace member-expression object
// (`A.B` in `A.B.f()`) against the namespace table, including the
// resolver's `__kml_modN` suffix on the root segment. Returns the member
// table and the dotted namespace name, or nil. Single identifiers are
// namespaceMembers' job — this only matches genuinely dotted chains.
func (e *Emitter) namespaceByChain(obj ast.Expression) (map[string]bool, string) {
	chain, ok := nsDottedChain(obj)
	if !ok || !strings.Contains(chain, ".") {
		return nil, ""
	}
	if m, ok := e.namespaces[chain]; ok {
		return m, chain
	}
	if i := strings.Index(chain, "."); i > 0 {
		root := chain[:i]
		if j := strings.LastIndex(root, "__kml_mod"); j > 0 {
			c2 := root[:j] + chain[i:]
			if m, ok := e.namespaces[c2]; ok {
				return m, c2
			}
			chain = c2
		}
	}
	// Expand the longest alias prefix (`M.X.f` where `M.X` aliases `M.N` —
	// ADR-00456), up to a small fixed number of expansions.
	for range [4]int{} {
		expanded := false
		for p := chain; p != ""; {
			if t, ok := e.nsAliases[p]; ok {
				chain = t + chain[len(p):]
				expanded = true
				break
			}
			i := strings.LastIndex(p, ".")
			if i < 0 {
				break
			}
			p = p[:i]
		}
		if !expanded {
			break
		}
		if m, ok := e.namespaces[chain]; ok {
			return m, chain
		}
	}
	return nil, ""
}

// stripNSTypeQualifier rewrites `ns.TypeName` — a namespace-qualified
// reference to a *type* member (enum/class), which desugars to a bare
// top-level name (ADR-00450) — to the bare `TypeName` identifier, so
// chains like `X.Color.Red` and `X.C.staticMethod()` resolve (ADR-00480).
// Returns nil when expr isn't that shape (value members, shadowed names,
// and unknown properties are untouched).
func (e *Emitter) stripNSTypeQualifier(expr ast.Expression) ast.Expression {
	mem, ok := expr.(*ast.MemberExpression)
	if !ok {
		return nil
	}
	id, ok := mem.Object.(*ast.Identifier)
	if !ok || e.isShadowedByLocal(id.Name) {
		return nil
	}
	members, _ := e.namespaceMembers(id.Name)
	if members == nil {
		return nil
	}
	if _, isValueMember := members[mem.Property]; isValueMember {
		return nil
	}
	// The type member's desugared bare name carries the resolver's
	// per-file suffix (`Color__kml_mod0`) while the source chain holds the
	// written name (the namespace root itself is never renamed, so there is
	// no suffix to borrow) — match exact first, then by mangled prefix.
	if _, isEnum := e.enums[mem.Property]; isEnum {
		return ast.NewIdentifier(mem.Property, mem.GetPos())
	}
	if _, isClass := e.classes[mem.Property]; isClass {
		return ast.NewIdentifier(mem.Property, mem.GetPos())
	}
	prefix := mem.Property + "__kml_mod"
	for name := range e.enums {
		if strings.HasPrefix(name, prefix) {
			return ast.NewIdentifier(name, mem.GetPos())
		}
	}
	for name := range e.classes {
		if strings.HasPrefix(name, prefix) {
			return ast.NewIdentifier(name, mem.GetPos())
		}
	}
	return nil
}

// nsSibling maps a bare identifier referenced inside a namespace member
// function to its sibling's mangled name (TDD-00148 Stage 4), or "" when
// not in a namespace context or no such member exists. Exportedness is
// irrelevant inside the namespace, so presence alone matches.
func (e *Emitter) nsSibling(name string) string {
	if e.curNamespace == "" {
		return ""
	}
	if members, ok := e.namespaces[e.curNamespace]; ok {
		if _, present := members[name]; present {
			return ast.NamespaceMangle(e.curNamespace, name)
		}
	}
	return ""
}

func (e *Emitter) namespaceMembers(name string) (map[string]bool, string) {
	if m, ok := e.namespaces[name]; ok {
		return m, name
	}
	// An import-equals alias for a namespace (ADR-00456).
	if t, ok := e.nsAliases[name]; ok {
		if m, ok := e.namespaces[t]; ok {
			return m, t
		}
	}
	if i := strings.LastIndex(name, "__kml_mod"); i > 0 {
		base := name[:i]
		if m, ok := e.namespaces[base]; ok {
			return m, base
		}
	}
	// Relative resolution from inside a namespace member (TDD-00148 V3):
	// `B.f()` written inside namespace `A` resolves to `A.B`, innermost
	// enclosing scope outward, matching TS's own lookup order.
	if e.curNamespace != "" {
		parts := strings.Split(e.curNamespace, ".")
		for k := len(parts); k >= 1; k-- {
			cand := strings.Join(parts[:k], ".") + "." + name
			if m, ok := e.namespaces[cand]; ok {
				return m, cand
			}
			if t, ok := e.nsAliases[cand]; ok {
				if m, ok := e.namespaces[t]; ok {
					return m, t
				}
			}
		}
	}
	return nil, ""
}

// indexCalleeAsMember is `o["m"](…)` as the `o.m(…)` it means (the same
// receiver and member), or nil when the callee is not an element access by
// a name.
func indexCalleeAsMember(ex *ast.CallExpression) *ast.CallExpression {
	ix, ok := ex.Callee.(*ast.IndexExpression)
	if !ok || ix.Optional || ex.Optional {
		return nil
	}
	name := ""
	switch k := ix.Index.(type) {
	case *ast.StringLiteral:
		if !isIdentifierName(k.Value) {
			return nil
		}
		name = k.Value
	case *ast.MemberExpression:
		// `obj[Symbol.iterator]()` / `obj[Symbol.asyncIterator]()` call the
		// iteration protocol's member (`@@iterator` / `@@asyncIterator`).
		id, ok := k.Object.(*ast.Identifier)
		if !ok || id.Name != "Symbol" || (k.Property != "iterator" && k.Property != "asyncIterator") {
			return nil
		}
		name = "@@" + k.Property
	default:
		return nil
	}
	m := ast.NewCallExpression(ast.NewMemberExpression(ix.Object, name, ix.GetPos()), ex.Args, ex.GetPos())
	m.TypeArgs = ex.TypeArgs
	return m
}

func isIdentifierName(s string) bool {
	for i, r := range s {
		if r == '_' || r == '$' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return s != ""
}

// receiverTemp evaluates a call's receiver once into a fresh local and
// returns a reference to it, for a call that reads a member of the receiver
// and also passes it (a `this: T` function field). An identifier or `this`
// is returned as is.
func (e *Emitter) receiverTemp(obj ast.Expression) (ast.Expression, error) {
	switch obj.(type) {
	case *ast.Identifier, *ast.ThisExpression:
		return obj, nil
	}
	v, err := e.emitExpr(obj)
	if err != nil {
		return nil, err
	}
	if v.Ty.IsArray {
		return obj, nil // no single-register receiver; evaluated again
	}
	e.recvTempCtr++
	name := fmt.Sprintf("__kml_recv%d", e.recvTempCtr)
	slot := "%" + name
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, v.Ty.IR, v.Ty.Align()))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", v.Ty.IR, v.Ref, slot, v.Ty.Align()))
	e.define(name, Symbol{Ptr: slot, Ty: v.Ty})
	return ast.NewIdentifier(name, obj.GetPos()), nil
}

// receiverlessThisCall is a call of a `this: T` function with no receiver
// (`f(1)`, where `this: unknown` allows it) with `undefined` passed as the
// receiver, or nil for any other call.
func (e *Emitter) receiverlessThisCall(ex *ast.CallExpression) *ast.CallExpression {
	if ex.Optional || e.receiverPassed[ex] {
		return nil
	}
	switch ex.Callee.(type) {
	case *ast.MemberExpression, *ast.SuperExpression, *ast.IndexExpression:
		return nil
	}
	if !e.inferExprType(ex.Callee).FuncThis {
		return nil
	}
	c := ast.NewCallExpression(ex.Callee, append([]ast.Expression{ast.NewNullLiteral(true, ex.GetPos())}, ex.Args...), ex.GetPos())
	c.TypeArgs = ex.TypeArgs
	if e.receiverPassed == nil {
		e.receiverPassed = map[*ast.CallExpression]bool{}
	}
	e.receiverPassed[c] = true
	return c
}

// isTimerHandle reports whether the checker types expr as a timer handle
// (NodeJS.Timeout, NodeJS.Immediate), which is the timer's id here.
func (e *Emitter) isTimerHandle(expr ast.Expression) bool {
	c := e.front()
	if c == nil {
		return false
	}
	t := c.TypeOf(expr)
	if c.Unanswered(t) || t.Flags&checker.Object == 0 || t.Symbol == nil {
		return false
	}
	switch t.Symbol.Name {
	case "Timeout", "Immediate":
		return true
	}
	return false
}

// emitArgOrDefault passes a defaulted scalar or `any` parameter an argument
// that may be undefined at run time (an `any`, a `T | undefined`): the
// argument, or — when it is undefined, as JavaScript substitutes — the
// default, evaluated only then. ok is false when the parameter has no
// default or the argument cannot be undefined.
func (e *Emitter) emitArgOrDefault(arg ast.Expression, paramTy Type, sig FuncSig, i int, pushScratch, popScratch func()) (Value, bool, error) {
	if i >= len(sig.Defaults) || sig.Defaults[i] == nil || paramTy.IR == "" || paramTy.Inferred || paramTy.IsArray || isNullableScalar(paramTy) {
		return Value{}, false, nil
	}
	argTy := e.inferExprType(arg)
	if !argTy.IsDynamic && !argTy.IsUndefined {
		return Value{}, false, nil
	}
	if argTy.IsDynamic && paramTy.IsDynamic && argTy.UnionMembers != nil {
		return Value{}, false, nil
	}
	raw, err := e.emitPreserveNullableOperand(arg)
	if err != nil {
		return Value{}, true, err
	}
	var isU string
	switch {
	case raw.Ty.IsDynamic:
		isU = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isU, raw.Ref, nbUndefined))
	case isNullableScalar(raw.Ty) && raw.Ty.IsUndefined:
		present, _ := e.nullableScalarAggParts(raw)
		isU = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", isU, present))
	case raw.Ty.IR == "ptr" && raw.Ty.IsUndefined:
		isU = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isU, raw.Ref))
	default:
		v, err := e.convertArgTo(raw, arg, paramTy)
		return v, true, err
	}
	argL, defL, mergeL := e.freshLabel("arg.given"), e.freshLabel("arg.default"), e.freshLabel("arg.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isU, defL, argL))
	e.emitLabel(argL)
	given, err := e.convertArgTo(raw, arg, paramTy)
	if err != nil {
		return Value{}, true, err
	}
	argEnd := e.freshLabel("arg.givenend")
	e.emitTerminator(fmt.Sprintf("br label %%%s", argEnd))
	e.emitLabel(argEnd)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(defL)
	pushScratch()
	dv, err := e.emitExprWithObjectHint(sig.Defaults[i], paramTy)
	if err != nil {
		popScratch()
		return Value{}, true, err
	}
	dv, err = e.convertArgTo(dv, sig.Defaults[i], paramTy)
	popScratch()
	if err != nil {
		return Value{}, true, err
	}
	defEnd := e.freshLabel("arg.defaultend")
	e.emitTerminator(fmt.Sprintf("br label %%%s", defEnd))
	e.emitLabel(defEnd)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(mergeL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi %s [ %s, %%%s ], [ %s, %%%s ]", r, paramTy.IR, given.Ref, argEnd, dv.Ref, defEnd))
	return Value{Ref: r, Ty: paramTy}, true, nil
}

// convertArgTo converts an evaluated argument to its parameter's type: boxed
// for an `any` parameter, coerced otherwise.
func (e *Emitter) convertArgTo(v Value, src ast.Expression, paramTy Type) (Value, error) {
	if paramTy.IsDynamic {
		if paramTy.UnionMembers != nil {
			v = e.relayoutForUnion(v, paramTy)
		}
		return e.emitBoxValueWidened(v, src)
	}
	if !coerciblePure(v.Ty, paramTy) && !v.Ty.IsDynamic && !isNullableScalar(v.Ty) {
		return Value{}, fmt.Errorf("%d:%d: argument has a type incompatible with the parameter's declared type — this compiler is a typed subset", src.GetPos().Line, src.GetPos().Col)
	}
	if isNullableScalar(v.Ty) {
		_, payload := e.nullableScalarAggParts(v)
		v = payload
	}
	c := e.coerce(v, paramTy)
	c.Ty = paramTy
	return c, nil
}

// funcOwnPropCall is `f.p(args)` on a function value — not one of
// Function.prototype's methods — as a call through the boxed function
// (`(f as any).p(args)`), whose own-property bag holds p.
func (e *Emitter) funcOwnPropCall(ex *ast.CallExpression) (*ast.CallExpression, bool) {
	mem, ok := ex.Callee.(*ast.MemberExpression)
	if !ok {
		return nil, false
	}
	switch mem.Property {
	case "call", "apply", "bind", "toString":
		return nil, false
	}
	if _, isAs := mem.Object.(*ast.AsExpression); isAs {
		return nil, false
	}
	ty := e.inferExprType(mem.Object)
	if !ty.IsFunc || ty.IsDynamic {
		return nil, false
	}
	// A namespace merged into the function declares its own members.
	if c := e.front(); c != nil && !c.ExpandoMember(mem) {
		switch c.MemberDecl(mem).(type) {
		case *ast.FunctionDeclaration, *ast.VarDeclaration:
			return nil, false
		}
	}
	boxed := ast.NewAsExpression(mem.Object, &ast.TypeAnnotation{Name: "any"}, mem.GetPos())
	callee := ast.NewMemberExpression(boxed, mem.Property, mem.GetPos())
	callee.Optional = mem.Optional
	r := ast.NewCallExpression(callee, ex.Args, ex.GetPos())
	r.Optional = ex.Optional
	return r, true
}

// checkerRefinedCallType is the function type a call of a named function
// returning `any` has when the checker types the call as one function
// signature of numbers, strings, booleans, `any`s and void: the closure type
// its result converts to.
func (e *Emitter) checkerRefinedCallType(ex *ast.CallExpression) (Type, bool) {
	id, ok := ex.Callee.(*ast.Identifier)
	if !ok || e.isShadowedByLocal(id.Name) {
		return Type{}, false
	}
	if _, isGeneric := e.genericFuncs[id.Name]; isGeneric {
		return Type{}, false
	}
	_, sig, found := e.resolveFuncRef(id.Name)
	if !found || !isUnconstrainedDynamic(sig.RetType) {
		return Type{}, false
	}
	c := e.front()
	if c == nil {
		return Type{}, false
	}
	t := c.TypeOf(ex)
	if c.Unanswered(t) || t.Flags&checker.Object == 0 || t.Kind != checker.Function || len(t.Overloads) > 0 || len(t.TypeParams) > 0 || t.HasRestParam() {
		return Type{}, false
	}
	params := make([]Type, 0, len(t.Params))
	for _, pt := range t.Params {
		r, ok := lowerRepr(pt)
		if !ok || r.IsArray || r.IsObject {
			return Type{}, false
		}
		params = append(params, r)
	}
	ret := TypeVoid
	if t.Result != nil {
		r, ok := lowerRepr(t.Result)
		if !ok || r.IsArray || r.IsObject {
			return Type{}, false
		}
		ret = r
	}
	return FuncType(params, ret), true
}

// objectProtoCall is `Object.prototype.m.call(o, …)` for Object.prototype's
// hasOwnProperty (Object.hasOwn), propertyIsEnumerable (o's own enumerable
// string keys: Object.keys) and toString (`[object Tag]`).
func (e *Emitter) objectProtoCall(ex *ast.CallExpression) (func() (Value, error), bool) {
	call, ok := ex.Callee.(*ast.MemberExpression)
	if !ok || call.Property != "call" || len(ex.Args) == 0 {
		return nil, false
	}
	m, ok := call.Object.(*ast.MemberExpression)
	if !ok {
		return nil, false
	}
	proto, ok := m.Object.(*ast.MemberExpression)
	if !ok || proto.Property != "prototype" {
		return nil, false
	}
	if id, ok := proto.Object.(*ast.Identifier); !ok || id.Name != "Object" || e.isShadowedByLocal("Object") {
		return nil, false
	}
	pos := ex.GetPos()
	recv := ex.Args[0]
	arg := func(i int) ast.Expression {
		if i < len(ex.Args) {
			return ex.Args[i]
		}
		return ast.NewIdentifier("undefined", pos)
	}
	switch m.Property {
	case "hasOwnProperty":
		return func() (Value, error) {
			return e.emitCall(ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("Object", pos), "hasOwn", pos), []ast.Expression{recv, arg(1)}, pos))
		}, true
	case "propertyIsEnumerable":
		return func() (Value, error) {
			keys := ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("Object", pos), "keys", pos), []ast.Expression{recv}, pos)
			key := ast.NewCallExpression(ast.NewIdentifier("String", pos), []ast.Expression{arg(1)}, pos)
			return e.emitCall(ast.NewCallExpression(ast.NewMemberExpression(keys, "includes", pos), []ast.Expression{key}, pos))
		}, true
	case "toString":
		return func() (Value, error) {
			v, err := e.emitExprWithObjectHint(recv, TypeAny)
			if err != nil {
				return Value{}, err
			}
			b, err := e.emitBoxValue(v)
			if err != nil {
				return Value{}, err
			}
			e.ensureDynJSONC()
			e.declareFn("__kml_object_tostring", "declare ptr @__kml_object_tostring(i64)")
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_object_tostring(i64 %s)", r, b.Ref))
			return Value{Ref: r, Ty: TypePtr}, nil
		}, true
	}
	return nil, false
}

// calleeWantsSource reports a method call whose declaration is
// `/** @callsite */`.
func (e *Emitter) calleeWantsSource(ex *ast.CallExpression) bool {
	mem, ok := ex.Callee.(*ast.MemberExpression)
	if !ok {
		return false
	}
	c := e.front()
	if c == nil {
		return false
	}
	if fd, ok := c.MemberDecl(mem).(*ast.FunctionDeclaration); ok && fd.CallSite {
		return true
	}
	for _, d := range c.MemberOverloadDecls(mem) {
		if fd, ok := d.(*ast.FunctionDeclaration); ok && fd.CallSite {
			return true
		}
	}
	return false
}

// isExpandoCall reports a call of a function's expando property (`f.p =
// g` in the scope declaring f), as the checker resolves it.
func (e *Emitter) isExpandoCall(ex *ast.CallExpression) bool {
	mem, ok := ex.Callee.(*ast.MemberExpression)
	if !ok {
		return false
	}
	c := e.front()
	return c != nil && c.ExpandoMember(mem)
}

// emitFuncFieldCall calls the function a member names (`obj.callback(…)`),
// a `this: T` one with the object as its receiver.
func (e *Emitter) emitFuncFieldCall(ex *ast.CallExpression, mem *ast.MemberExpression, mt Type) (Value, error) {
	if mt.FuncThis {
		recv, err := e.receiverTemp(mem.Object)
		if err != nil {
			return Value{}, err
		}
		memVal, err := e.emitExpr(ast.NewMemberExpression(recv, mem.Property, mem.GetPos()))
		if err != nil {
			return Value{}, err
		}
		return e.emitClosureCallByPtr(memVal.Ref, memVal.Ty, append([]ast.Expression{recv}, ex.Args...), ex.GetPos())
	}
	memVal, err := e.emitExpr(mem)
	if err != nil {
		return Value{}, err
	}
	return e.emitClosureCallByPtr(memVal.Ref, memVal.Ty, ex.Args, ex.GetPos())
}
