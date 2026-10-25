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
// equivalent to — `tag(strings, x)` — as a synthetic *ast.CallExpression:
// the site's template object (the cooked quasis, their raw text as `raw`)
// as the first argument, then every interpolated expression untouched (no
// implicit stringification) as the remaining arguments (TDD-00059). The
// built-in `String.raw` tag is such a call too.
func (e *Emitter) desugarTaggedTemplate(tt *ast.TaggedTemplateExpression) *ast.CallExpression {
	if call, ok := e.tmplCalls[tt]; ok {
		return call
	}
	lit := func(qs []string) *ast.ArrayLiteral {
		xs := make([]ast.Expression, len(qs))
		for i, q := range qs {
			xs[i] = ast.NewStringLiteral(q, tt.GetPos())
		}
		return ast.NewArrayLiteral(xs, tt.GetPos())
	}
	raw := tt.RawQuasis
	if len(raw) != len(tt.Quasis) {
		raw = tt.Quasis
	}
	// The template object: the cooked strings, carrying the raw ones.
	tmpl := ast.NewCallExpression(nil, []ast.Expression{lit(tt.Quasis), lit(raw)}, tt.GetPos())
	tmpl.Intrinsic = templateObjectIntrinsic
	args := append([]ast.Expression{tmpl}, tt.Exprs...)
	call := ast.NewCallExpression(tt.Tag, args, tt.GetPos())
	call.TypeArgs = tt.TypeArgs // `` tag<T>`…` ``
	if e.tmplCalls == nil {
		e.tmplCalls = map[*ast.TaggedTemplateExpression]*ast.CallExpression{}
	}
	e.tmplCalls[tt] = call
	return call
}

// templateObjectIntrinsic is a tagged template's first argument: its
// template object, made once per site (GetTemplateObject caches it in the
// realm's template map), the cooked strings with the raw strings registered
// as their `raw` (__kml_template_raw).
const templateObjectIntrinsic = "%TemplateObject%"

func init() {
	intrinsics[templateObjectIntrinsic] = intrinsic{
		emit: (*Emitter).emitTemplateObject,
		ty:   func(*Emitter, *ast.CallExpression) Type { return ArrayOf(TypePtr) },
	}
}

func (e *Emitter) emitTemplateObject(ex *ast.CallExpression) (Value, error) {
	if e.tmplSites == nil {
		e.tmplSites = map[*ast.CallExpression]string{}
	}
	g, ok := e.tmplSites[ex]
	if !ok {
		g = fmt.Sprintf("@__kml_tmpl.%d", len(e.tmplSites))
		e.tmplSites[ex] = g
		e.emitGlobal(fmt.Sprintf("%s = internal %sglobal ptr null, align 8", g, e.isolateTLS()))
	}
	e.ensureDynJSONC() // __kml_template_register
	cached := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cached, g))
	isNew := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNew, cached))
	makeL, doneL := e.freshLabel("tmpl.make"), e.freshLabel("tmpl.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNew, makeL, doneL))
	e.emitLabel(makeL)
	var hdrs [2]string
	for i, a := range ex.Args {
		v, err := e.emitExprWithObjectHint(a, ArrayOf(TypePtr))
		if err != nil {
			return Value{}, err
		}
		hdrs[i] = e.arrayReturnHeader(v)
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_template_register(ptr %s, ptr %s)", hdrs[0], hdrs[1]))
	// GetTemplateObject freezes both the strings and their raw array.
	e.emitArraySetLevel(hdrs[0], 1)
	e.emitArraySetLevel(hdrs[1], 1)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdrs[0], g))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", h, g))
	return e.arrayValueFromHeaderReg(h, ArrayOf(TypePtr)), nil
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
	// A double slot short-circuits on its undefined (TDD-00241).
	if isF64Slot(objVal.Ty) {
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", b, objVal.Ref))
		present := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", present, b, undefF64))
		aggTy := undefinedableElem(objVal.Ty)
		return e.emitOptionalCallNullableScalar(ex, mem, Value{Ref: e.makeNullableScalarAgg(aggTy, present, objVal.Ref), Ty: aggTy})
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
	// A non-pointer (or array) receiver can never be a null pointer —
	// run the call normally, non-optional. (Rare; matches emitOptionalMember.)
	if objVal.Ty.IR != "ptr" || objVal.Ty.IsArray() {
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
	if c := e.front(); c != nil {
		// The checker typed the written receiver, not the throwaway local:
		// a builtin method keeps the intrinsic it resolved to.
		if key, ok := e.methodIntrinsic(c, mem); ok {
			through.Intrinsic = key
		}
	}
	if through.Intrinsic == "" && (objVal.Ty.IsDate() || e.isDateClass(objVal.Ty)) {
		// The checker leaves `?.` untyped; a Date's methods dispatch on the
		// receiver's representation.
		if _, ok := intrinsics["Date.prototype."+mem.Property]; ok {
			through.Intrinsic = "Date.prototype." + mem.Property
		}
	}
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
		if sym, ok := e.lookup(c.Name); ok && sym.Ty.IsFunc() && sym.Ty.IR == "ptr" {
			return optCalleeValue
		}
	case *ast.MemberExpression:
		if id, ok := c.Object.(*ast.Identifier); ok && id.Name == "process" && !e.isShadowedByLocal("process") {
			if processCredentialMethods[c.Property] {
				return optCalleeHostDependent
			}
			return optCalleePresent
		}
		objTy := e.inferExprType(c.Object)
		if objTy.IsObject() || objTy.IsClass() {
			if _, fty, ok := objTy.FieldIndex(c.Property); ok && fty.IsFunc() && fty.IR == "ptr" {
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
	if kind == optCalleePresent || inner.IR == "void" || inner.IR == "" || inner.IsArray() {
		return inner
	}
	// A `T | null` read short-circuited to undefined holds all three
	// states (`r?.inner` of a null `inner` is null).
	if u := optionalFieldType(inner); u.Nullable() {
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
	e.noteChainCopy(&plain, ex)
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok && !mem.Optional {
		// `o.m?.(…)` of an optional method: called where the instance's class
		// implements it. The receiver is evaluated once.
		if ot := e.inferExprType(mem.Object); ot.IsClass() && e.isOptionalMethod(ot.ClassName, mem.Property) {
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
			v = e.convert(v, inner)
			return Value{Ref: e.makeNullableScalarAgg(resTy, "true", v.Ref), Ty: resTy}, nil
		}
		return v, nil
	case optCalleeValue:
		callee := ex.Callee
		var recv ast.Expression
		if mem, isMem := callee.(*ast.MemberExpression); isMem {
			// A field holding a method that takes its `this` (an object
			// literal's method reading it) is called with the object as its
			// receiver, evaluated once (`o.m?.()`, `(o?.m)?.()`).
			if ot := e.inferExprType(mem.Object); ot.IsObject() && !ot.IsDynamic() {
				if _, fty, ok := ot.FieldIndex(mem.Property); ok && fty.FuncThis {
					r, err := e.receiverTemp(mem.Object)
					if err != nil {
						return Value{}, err
					}
					rm := ast.NewMemberExpression(r, mem.Property, mem.GetPos())
					rm.Optional = mem.Optional
					callee, recv = rm, r
				}
			}
		}
		fv, err := e.emitExpr(callee)
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
		fnTy.setNullable(false)
		fnTy.setUndefined(false)
		e.define(fnName, Symbol{Ptr: slot, Ty: fnTy})
		var through *ast.CallExpression
		if recv != nil {
			// Called as the receiver's method (its field read again, from
			// the bound receiver), so the method gets its `this`.
			through = ast.NewCallExpression(ast.NewMemberExpression(recv, callee.(*ast.MemberExpression).Property, callee.GetPos()), ex.Args, ex.GetPos())
		} else {
			through = ast.NewCallExpression(ast.NewIdentifier(fnName, ex.Callee.GetPos()), ex.Args, ex.GetPos())
		}
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
	undefTy := optionalFieldType(retTy)
	wrapUndef := !isVoid && undefTy.Nullable() && !retTy.IsArray()

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
	// optional for a scalar, the null pointer for a pointer, an empty array
	// for an array.
	e.emitLabel(nullL)
	if !isVoid {
		if retTy.IsArray() {
			// The array result slot holds a header pointer (TDD-00213 Stage 2/3);
			// a nullish receiver yields an empty array — a fresh empty header.
			hdr := e.newArrayHeader("null", "0")
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, resPtr))
		} else if isNullableScalar(undefTy) {
			agg := e.makeNullableScalarAgg(undefTy, "false", zeroRef(retTy.withoutNullable()))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", resIR, agg, resPtr, undefTy.Align()))
		} else if retTy.IsDynamic() {
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
		if retTy.IsArray() {
			// Store the call result's shared header pointer so the merged result
			// aliases the returned array (TDD-00213 Stage 3).
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.arrayReturnHeader(callVal), resPtr))
		} else {
			stored := e.convert(callVal, retTy)
			if undefTy.NullAndUndef() && !stored.Ty.NullAndUndef() {
				// Its own null stays null beside the chain's undefined.
				stored = e.toThreeState(stored, undefTy)
			}
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
	if retTy.IsArray() {
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
	undefTy := optionalFieldType(retTy)
	wrapUndef := !isVoid && undefTy.Nullable() && !retTy.IsArray()

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
		if retTy.IsArray() {
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
		if retTy.IsArray() {
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.arrayReturnHeader(callVal), resPtr))
		} else {
			stored := e.convert(callVal, retTy)
			if undefTy.NullAndUndef() && !stored.Ty.NullAndUndef() {
				// Its own null stays null beside the chain's undefined.
				stored = e.toThreeState(stored, undefTy)
			}
			storeRef := stored.Ref
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
	if retTy.IsArray() {
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

// emitCall emits a call: the rewrite rules first, then the receiver guard
// of a method call (super and optional calls aside), then the target rules
// — the builtin's declaration, the callee's owner, the value fallbacks.
func (e *Emitter) emitCall(ex *ast.CallExpression) (Value, error) {
	// The running frame is at this call while it runs (frames.go).
	defer e.callSite(callSitePos(ex))()
	savedCallee := e.curCallee
	e.curCallee = ex.Callee
	defer func() { e.curCallee = savedCallee }()
	e.noteWebviewBindTyped(ex)
	if v, done, err := e.applyCallRules(callRewrites, ex); done {
		return v, err
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
	if v, done, err := e.applyCallRules(callTargets, ex); done {
		return v, err
	}
	return Value{}, fmt.Errorf("%d:%d: only simple function calls are supported (the callee is not a named function, a function value, or a supported method)", ex.GetPos().Line, ex.GetPos().Col)
}

// callRule emits a call it recognizes; done is false for one it does not.
type callRule func(e *Emitter, ex *ast.CallExpression) (Value, bool, error)

// callRewrites run before a method call's receiver is guarded: calls
// recognized by shape, and desugarings that emit the call they rewrite to.
// callTargets run after it, in order: the builtin's declaration, the
// callee's owner, then the value fallbacks and diagnostics.
var callRewrites, callTargets []callRule

func init() {
	callRewrites = []callRule{
		(*Emitter).moduleNamespaceRule,
		(*Emitter).liveBindingsRule,
		(*Emitter).tupleSpreadRule,
		(*Emitter).primitiveCtorCallRule,
		(*Emitter).thenableResolveRule,
		(*Emitter).thenConstRule,
		(*Emitter).resolveThenRule,
		(*Emitter).queueMicrotaskRule,
		(*Emitter).objectCtorRule,
		(*Emitter).builtinIteratorRule,
		(*Emitter).erasedAssertionMutatorRule,
		(*Emitter).objectProtoRule,
		(*Emitter).tupleArrayMethodRule,
		(*Emitter).arrayBodySpeciesRule,
		(*Emitter).callSiteSourceRule,
		(*Emitter).passSourceRule,
		(*Emitter).checkerRefinedRule,
		(*Emitter).indexCalleeRule,
		(*Emitter).receiverlessThisRule,
		(*Emitter).superCallRule,
		(*Emitter).processMemberRule,
		(*Emitter).optionalCalleeRule,
		(*Emitter).globalThisRule,
	}
	callTargets = []callRule{
		(*Emitter).loweredRule,
		(*Emitter).objectFuncFieldRule,
		(*Emitter).funcExpandoRule,
		(*Emitter).staticMemberRule,
		(*Emitter).receiverRule,
		(*Emitter).dynIndexCallRule,
		(*Emitter).indexFuncCallRule,
		(*Emitter).globalGCRule,
		(*Emitter).arrowIIFERule,
		(*Emitter).functionIIFERule,
		(*Emitter).identifierCallRule,
		(*Emitter).missingImportRule,
		(*Emitter).funcValueCalleeRule,
		(*Emitter).dynMethodRule,
		(*Emitter).funcOwnPropRule,
		(*Emitter).missingMethodRule,
		(*Emitter).callableValueRule,
	}
}

// applyCallRules emits ex through the first rule that takes it.
func (e *Emitter) applyCallRules(rules []callRule, ex *ast.CallExpression) (Value, bool, error) {
	for _, r := range rules {
		if v, done, err := r(e, ex); done {
			return v, true, err
		}
	}
	return Value{}, false, nil
}

// thenableResolveRule is the lowering's `__kml_thenable_resolve(x)`:
// PromiseResolve(%Promise%, x), a thenable class instance adopted through
// its `then`.
func (e *Emitter) thenableResolveRule(ex *ast.CallExpression) (Value, bool, error) {
	if isThenableResolveCall(ex) {
		return handled(e.emitThenableResolveCall(ex))
	}
	return Value{}, false, nil
}

// thenConstRule is the lowering's `__kml_then_const(p, v, throws)`:
// `p.then(() => v)`, or `p.then(() => { throw v })`.
func (e *Emitter) thenConstRule(ex *ast.CallExpression) (Value, bool, error) {
	if isThenConstCall(ex) {
		return handled(e.emitThenConstCall(ex))
	}
	return Value{}, false, nil
}

// resolveThenRule is `C.resolve(x).then(onF, onR)` (emitResolveThenCall).
func (e *Emitter) resolveThenRule(ex *ast.CallExpression) (Value, bool, error) {
	if isResolveThenCall(ex) {
		return handled(e.emitResolveThenCall(ex))
	}
	return Value{}, false, nil
}

// queueMicrotaskRule is queueMicrotask(fn).
func (e *Emitter) queueMicrotaskRule(ex *ast.CallExpression) (Value, bool, error) {
	if id, ok := ex.Callee.(*ast.Identifier); ok && id.Name == queueMicrotaskName {
		return handled(e.emitQueueMicrotask(ex.Args, ex.GetPos()))
	}
	return Value{}, false, nil
}

// objectCtorRule is `Object(v)` called as a function.
func (e *Emitter) objectCtorRule(ex *ast.CallExpression) (Value, bool, error) {
	if id, ok := ex.Callee.(*ast.Identifier); ok && !ex.Optional && e.isGlobalObjectCtor(id.Name, false) {
		return handled(e.emitObjectCtor(ex.Args, ex.GetPos()))
	}
	return Value{}, false, nil
}

// builtinIteratorRule is `x[Symbol.iterator]()` on a builtin iterable, as
// the method it is (builtinIteratorCall).
func (e *Emitter) builtinIteratorRule(ex *ast.CallExpression) (Value, bool, error) {
	if r, ok := e.builtinIteratorCall(ex); ok {
		return handled(e.emitCall(r))
	}
	return Value{}, false, nil
}

// erasedAssertionMutatorRule: `(x as T[]).push(v)` with x held as any: the assertion is erased, so
// the mutation is x's own (the dynamic call), not a converted copy's.
func (e *Emitter) erasedAssertionMutatorRule(ex *ast.CallExpression) (Value, bool, error) {
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok && arrayMutators[mem.Property] {
		if as, ok := mem.Object.(*ast.AsExpression); ok && isUnconstrainedDynamic(e.inferExprType(as.Expr)) {
			if at := e.inferExprType(as); at.IsArray() && !at.IsTypedArray() && !at.IsBuffer() {
				return handled(e.emitCall(ast.NewCallExpression(ast.NewMemberExpression(as.Expr, mem.Property, mem.GetPos()), ex.Args, ex.GetPos())))
			}
		}
	}
	return Value{}, false, nil
}

// objectProtoRule: `Object.prototype.m.call(o, …)`: Object.prototype's own methods.
func (e *Emitter) objectProtoRule(ex *ast.CallExpression) (Value, bool, error) {
	if r, ok := e.objectProtoCall(ex); ok {
		return handled(r())
	}
	return Value{}, false, nil
}

// callSiteSourceRule: A call of assert's ok passes its source as a hidden last argument
// (lib/node/assert.ts callSite): marked by the resolver, or a method
// declared `/** @callsite */`.
func (e *Emitter) callSiteSourceRule(ex *ast.CallExpression) (Value, bool, error) {
	if !ex.PassSource && ex.Source != "" && e.calleeWantsSource(ex) {
		c := *ex
		c.PassSource = true
		return handled(e.emitCall(&c))
	}
	return Value{}, false, nil
}

// passSourceRule appends a call site's source text as the hidden last
// argument callSiteSourceRule marked it for.
func (e *Emitter) passSourceRule(ex *ast.CallExpression) (Value, bool, error) {
	if ex.PassSource {
		c := *ex
		c.PassSource = false
		c.Source = ""
		c.Args = append(append([]ast.Expression(nil), ex.Args...), ast.NewStringLiteral("\x00kml:callsite\x00"+ex.Source, ex.GetPos()))
		return handled(e.emitCall(&c))
	}
	return Value{}, false, nil
}

// checkerRefinedRule: A function returning `any` whose call the checker types as a function
// (an overload returning its argument's type, `mustCall(fn)`): the
// result converts to that function type.
func (e *Emitter) checkerRefinedRule(ex *ast.CallExpression) (Value, bool, error) {
	if ft, ok := e.checkerRefinedCallType(ex); ok && !e.refiningCall[ex] {
		e.refiningCall[ex] = true
		v, err := e.emitCall(ex)
		delete(e.refiningCall, ex)
		if err != nil || !isUnconstrainedDynamic(v.Ty) {
			return handled(v, err)
		}
		if c, ok := e.emitAnyToClosure(v, ft); ok {
			return handled(c, nil)
		}
		return handled(v, nil)
	}
	return Value{}, false, nil
}

// indexCalleeRule is `o["m"](…)` with a constant key, the member call
// `o.m(…)`.
func (e *Emitter) indexCalleeRule(ex *ast.CallExpression) (Value, bool, error) {
	if m := indexCalleeAsMember(ex); m != nil {
		return handled(e.emitCall(m))
	}
	return Value{}, false, nil
}

// receiverlessThisRule is a call of a `this: T` function with no receiver,
// `undefined` passed as the receiver (receiverlessThisCall).
func (e *Emitter) receiverlessThisRule(ex *ast.CallExpression) (Value, bool, error) {
	if c := e.receiverlessThisCall(ex); c != nil {
		return handled(e.emitCall(c))
	}
	return Value{}, false, nil
}

// superCallRule: super(args) / super.method(args) (TDD-00009 Stage 3) — checked first,
// since a SuperExpression callee/receiver never reaches the generic
// mem.Object-based dispatch chain below (inferExprType has no case for
// it, and it shouldn't: super is only ever meaningful directly in call
// position).
func (e *Emitter) superCallRule(ex *ast.CallExpression) (Value, bool, error) {
	if _, ok := ex.Callee.(*ast.SuperExpression); ok {
		return handled(e.emitSuperCall(ex))
	}
	return Value{}, false, nil
}

// processMemberRule: process.on(...), process.emitWarning(...): the members TypeScript
// modules implement (processMemberRewrite).
func (e *Emitter) processMemberRule(ex *ast.CallExpression) (Value, bool, error) {
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if r, ok := e.processMemberRewrite(mem); ok {
			rewritten := ast.NewCallExpression(r, ex.Args, ex.GetPos())
			rewritten.TypeArgs = ex.TypeArgs
			rewritten.Optional = ex.Optional
			return handled(e.emitCall(rewritten))
		}
	}
	return Value{}, false, nil
}

// optionalCalleeRule: Optional call `f?.(...)`: guard the callee, then dispatch the plain call.
func (e *Emitter) optionalCalleeRule(ex *ast.CallExpression) (Value, bool, error) {
	if ex.Optional {
		return handled(e.emitOptionalCalleeCall(ex))
	}
	return Value{}, false, nil
}

// globalThisRule: `globalThis.setTimeout(...)` / `globalThis.JSON.stringify(...)` — peel the
// `globalThis.` alias off the callee so it dispatches as the bare global.
func (e *Emitter) globalThisRule(ex *ast.CallExpression) (Value, bool, error) {
	if unwrapped := e.unwrapGlobalThis(ex.Callee); unwrapped != ex.Callee {
		// A builtin's declaration is found through the original, bound
		// callee; the rewritten one is synthetic.
		if l, ok := e.loweredCall(ex); ok {
			return handled(e.emitLowered(ex, l))
		}
		rewritten := ast.NewCallExpression(unwrapped, ex.Args, ex.GetPos())
		rewritten.TypeArgs = ex.TypeArgs
		return handled(e.emitCall(rewritten))
	}
	return Value{}, false, nil
}

// loweredRule: A call through a builtin declaration's @lower target (TDD-00230 P3.2),
// its receiver guarded like any method call's.
func (e *Emitter) loweredRule(ex *ast.CallExpression) (Value, bool, error) {
	if l, ok := e.loweredCall(ex); ok {
		return handled(e.emitLowered(ex, l))
	}
	return Value{}, false, nil
}

// objectFuncFieldRule: A plain object's function-typed field (`posix.join(…)` on `{ join:
// … }`), before the methods dispatched by name, which such an object has
// none of.
func (e *Emitter) objectFuncFieldRule(ex *ast.CallExpression) (Value, bool, error) {
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok && !mem.Optional {
		if ot := e.inferExprType(mem.Object); ot.IsObject() && !ot.IsClass() && !ot.IsArray() && !ot.IsDynamic() && !isHostHandle(ot) && !ot.IsRegExp() {
			if _, fty, isField := ot.FieldIndex(mem.Property); isField && fty.IsFunc() {
				mt := e.inferExprType(mem)
				if isUnconstrainedDynamic(mt) {
					mt = fty // the layout's member, where the read answers no type
				}
				if mt.IsFunc() {
					return handled(e.emitFuncFieldCall(ex, mem, mt))
				}
			}
		}
	}
	return Value{}, false, nil
}

// funcExpandoRule: A function's own function-valued property (`assert.match(…)`, a
// TypeScript expando), called through the function object's bag before
// the methods dispatched by name, which a function has none of.
func (e *Emitter) funcExpandoRule(ex *ast.CallExpression) (Value, bool, error) {
	if r, ok := e.funcOwnPropCall(ex); ok && e.isExpandoCall(ex) {
		mem := r.Callee.(*ast.MemberExpression)
		fn, err := e.emitExpr(mem.Object)
		if err != nil {
			return handled(Value{}, err)
		}
		return handled(e.emitDynAnyMethodCallPlain(fn, mem.Property, ex.Args, ex.GetPos()))
	}
	return Value{}, false, nil
}

// staticMemberRule: Static method call: ClassName.staticMethod(args) (TDD-00009 Stage
// 4). Checked before every mem.Property-name-based/inferExprType-based
// dispatch below, for the same reason super's own checks above are: a
// bare class-name identifier is a compile-time namespace, never a real
// runtime value bindable via e.lookup/inferExprType.
func (e *Emitter) staticMemberRule(ex *ast.CallExpression) (Value, bool, error) {
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
						return handled(Value{}, fmt.Errorf("%d:%d: '%s.%s' is not exported from namespace '%s'", ex.GetPos().Line, ex.GetPos().Col, id.Name, mem.Property, nsName))
					}
					mangled := ast.NamespaceMangle(nsName, mem.Property)
					rewritten := ast.NewCallExpression(ast.NewIdentifier(mangled, ex.GetPos()), ex.Args, ex.GetPos())
					return handled(e.emitCall(rewritten))
				}
			}
			if info, found := e.classes[id.Name]; found {
				return handled(e.emitStaticMethodCall(info, id.Name, mem.Property, ex.Args, ex.GetPos()))
			}
			if _, generic := e.genericClasses[id.Name]; generic && !e.isShadowedByLocal(id.Name) {
				if info, found := e.classes[genericStaticsClass(id.Name)]; found {
					return handled(e.emitStaticMethodCall(info, genericStaticsClass(id.Name), mem.Property, ex.Args, ex.GetPos()))
				}
			}
		}
		// `Base.call(this, args)` on a recognized prototype constructor
		// (TDD-00155 Stage 4): the pre-ES6 inheritance chain — the receiver
		// is passed through, unlike the generic .call path (which discards
		// thisArg, ADR-00398 Stage A).
		if id, ok := mem.Object.(*ast.Identifier); ok && e.jsProtoCtor[id.Name] && mem.Property == "call" {
			return handled(e.emitProtoCtorChainCall(id.Name, ex.Args, ex.GetPos()))
		}
		// A namespace-qualified static call (`X.C.method()` — ADR-00480).
		if bare := e.stripNSTypeQualifier(mem.Object); bare != nil {
			if bid, ok := bare.(*ast.Identifier); ok {
				if info, found := e.classes[bid.Name]; found {
					return handled(e.emitStaticMethodCall(info, bid.Name, mem.Property, ex.Args, ex.GetPos()))
				}
			}
		}
		// Nested-namespace member call `A.B.f(args)` (TDD-00148 V3).
		if members, nsName := e.namespaceByChain(mem.Object); members != nil {
			if exported, present := members[mem.Property]; present {
				if !exported && e.curNamespace != nsName {
					return handled(Value{}, fmt.Errorf("%d:%d: '%s.%s' is not exported from namespace '%s'", ex.GetPos().Line, ex.GetPos().Col, nsName, mem.Property, nsName))
				}
				mangled := ast.NamespaceMangle(nsName, mem.Property)
				rewritten := ast.NewCallExpression(ast.NewIdentifier(mangled, ex.GetPos()), ex.Args, ex.GetPos())
				return handled(e.emitCall(rewritten))
			}
		}
	}
	return Value{}, false, nil
}

// receiverRule is a method call dispatched on its receiver's owner
// (emitReceiverMethodCall).
func (e *Emitter) receiverRule(ex *ast.CallExpression) (Value, bool, error) {
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if v, done, err := e.emitReceiverMethodCall(ex, mem); done {
			return handled(v, err)
		}
	}
	return Value{}, false, nil
}

// dynIndexCallRule: `obj[key](...)` on a bare any/unknown receiver: a dynamic method call,
// the receiver passed as `this`.
func (e *Emitter) dynIndexCallRule(ex *ast.CallExpression) (Value, bool, error) {
	if idxEx, ok := ex.Callee.(*ast.IndexExpression); ok && !idxEx.Optional && isUnconstrainedDynamic(e.inferExprType(idxEx.Object)) {
		objVal, err := e.emitExpr(idxEx.Object)
		if err != nil {
			return handled(Value{}, err)
		}
		keyRef, err := e.dynAnyKeyRef(idxEx.Index, ex.GetPos())
		if err != nil {
			return handled(Value{}, err)
		}
		fnBox, err := e.emitDynAnyMemberGet(objVal, keyRef, ex.GetPos())
		if err != nil {
			return handled(Value{}, err)
		}
		argv, n, err := e.emitDynArgv(ex.Args, ex.GetPos())
		if err != nil {
			return handled(Value{}, err)
		}
		return handled(e.emitDynFnBoxCallN(fnBox, objVal, argv, n, forOfIterableName(idxEx)+" is not a function", ex.GetPos()))
	}
	return Value{}, false, nil
}

// indexFuncCallRule: Calling a function value stored in an array element: arr[i](...).
func (e *Emitter) indexFuncCallRule(ex *ast.CallExpression) (Value, bool, error) {
	if idxEx, ok := ex.Callee.(*ast.IndexExpression); ok {
		if e.inferExprType(idxEx).IsFunc() {
			idxVal, err := e.emitExpr(idxEx)
			if err != nil {
				return handled(Value{}, err)
			}
			return handled(e.emitClosureCallByPtr(idxVal.Ref, idxVal.Ty, ex.Args, ex.GetPos()))
		}
	}
	return Value{}, false, nil
}

// globalGCRule: Global built-in functions.
func (e *Emitter) globalGCRule(ex *ast.CallExpression) (Value, bool, error) {
	if id, ok := ex.Callee.(*ast.Identifier); ok && !e.isShadowedByLocal(id.Name) {
		if id.Name == "gc" {
			return handled(e.emitGlobalGC(ex.Args, ex.GetPos()))
		}
	}
	return Value{}, false, nil
}

// arrowIIFERule: Immediately-invoked arrow function: ((x: number) => x+1)(5)
func (e *Emitter) arrowIIFERule(ex *ast.CallExpression) (Value, bool, error) {
	if af, ok := ex.Callee.(*ast.ArrowFunction); ok {
		closureVal, err := e.emitArrowFunction(af)
		if err != nil {
			return handled(Value{}, err)
		}
		return handled(e.emitClosureCallByPtr(closureVal.Ref, closureVal.Ty, ex.Args, ex.GetPos()))
	}
	return Value{}, false, nil
}

// functionIIFERule: Immediately-invoked function expression: (function(x: number) { return x+1; })(5)
// A generator expression's value is a boxed constructor: its call is
// the dynamic call below, as for `const g = function* () {}; g()`.
func (e *Emitter) functionIIFERule(ex *ast.CallExpression) (Value, bool, error) {
	if fe, ok := ex.Callee.(*ast.FunctionExpression); ok && !fe.IsGenerator {
		closureVal, err := e.emitFunctionExpression(fe, nil)
		if err != nil {
			return handled(Value{}, err)
		}
		// Default/omitted-parameter filling for the IIFE (originally ADR-00911's
		// own call-site padding, now subsumed): the closure value carries its
		// parameter defaults on its Type (setFuncParamDefaults), and
		// emitClosureCallByPtr fills any omitted trailing parameter from them —
		// or with `undefined` when there is no default — so `(function(f = 123)
		// {...})()` no longer emits a `call` with fewer operands than the
		// callee's arity (TDD-00206 Stage 1). No call-site padding needed here.
		return handled(e.emitClosureCallByPtr(closureVal.Ref, closureVal.Ty, ex.Args, ex.GetPos()))
	}
	return Value{}, false, nil
}

// identifierCallRule: Call via bare identifier: named function or closure variable.
func (e *Emitter) identifierCallRule(ex *ast.CallExpression) (Value, bool, error) {
	if id, ok := ex.Callee.(*ast.Identifier); ok {
		// Generator construction (TDD-00061/ADR-00172, top-level only in
		// V1) — checked before the ordinary named-function dispatch just
		// below, since a generator is never entered into e.funcs/
		// resolveFuncRef at all: calling one doesn't emit an ordinary
		// `call`, it builds a fiber-backed instance struct instead.
		if info, found := e.lookupGenerator(id.Name); found {
			return handled(e.emitGeneratorConstruction(info, ex.Args, ex.GetPos()))
		}
		// An ambient function declared more than once (`declare function
		// f(…)` overloads) has no implementation to call: its arguments are
		// evaluated, then the call throws what the stub would, with no one
		// signature to convert them to.
		if e.ambientOverloaded(id.Name) {
			if _, local := e.lookup(id.Name); !local {
				for _, a := range ex.Args {
					if _, err := e.emitExpr(a); err != nil {
						return handled(Value{}, err)
					}
				}
				pos := ex.GetPos()
				thr := ast.NewThrowStatement(ast.NewNewErrorExpression("Error",
					ast.NewStringLiteral("ambient function '"+demangleModuleName(id.Name)+"' has no implementation", pos), pos), pos)
				if err := e.emitStmt(thr); err != nil {
					return handled(Value{}, err)
				}
				ty := e.inferExprType(ex)
				if ty.IR == "" || ty.IR == "void" {
					return handled(Value{Ty: TypeVoid}, nil)
				}
				return handled(Value{Ref: ty.zeroLiteral(), Ty: ty}, nil)
			}
		}
		// A var sharing a function's name holds the function only until it
		// is assigned (seedFuncGlobals): a call reads the binding.
		if e.funcSeededRead(id) {
			fv, err := e.emitExpr(id)
			if err != nil {
				return handled(Value{}, err)
			}
			return handled(e.emitDynCallValueOf(fv, ex.Callee, ex.Args, ex.GetPos()))
		}
		// Named function — a nested one (TDD-00057) shadows an outer/
		// top-level function of the same name, same as real JS/TS scoping.
		if mangled, sig, found := e.resolveFuncRef(id.Name); found {
			// A recognized prototype constructor's ABI carries a hidden boxed
			// `this`: a plain call passes the sloppy-mode receiver,
			// globalThis.
			if e.jsProtoCtor[id.Name] && !e.protoCtorOwnThis[id.Name] {
				return handled(e.emitProtoCtorPlainCall(id.Name, ex.Args, ex.GetPos()))
			}
			e.anyArgBoxRet = e.anyArgReturnsBox(ex)
			return handled(e.emitCallToFuncSig(mangled, sig, ex.Args, ex.GetPos()))
		}
		// Generic (TDD-00010 V1) function: infer the type argument from
		// whichever call-site argument lines up with the generic's own
		// type-parameter-typed parameter, instantiate (or reuse a memoized
		// prior instantiation) on demand, then dispatch exactly like a
		// concrete named function.
		if decl, found := e.genericFuncs[id.Name]; found {
			return handled(e.emitGenericFuncCall(decl, ex.Args, ex.TypeArgs, ex.GetPos(), ex))
		}
		// Closure variable — including a named function expression's own
		// self-reference binding (TDD-00060).
		if sym, found := e.lookup(id.Name); found && sym.Ty.IsFunc() {
			return handled(e.emitClosureCall(sym, ex.Args, ex.GetPos()))
		}
		// A union-typed binding a guard narrowed to its function member
		// (`typeof o === 'function'`): its unboxed closure, called.
		if sym, found := e.lookup(id.Name); found && sym.NarrowedTo != nil && sym.NarrowedTo.IsFunc() {
			fv, err := e.emitExpr(id)
			if err != nil {
				return handled(Value{}, err)
			}
			return handled(e.emitClosureCallByPtr(fv.Ref, fv.Ty, ex.Args, ex.GetPos()))
		}
		// A variable holding an `any`/`unknown` value, called: the runtime
		// dynamic call (a non-function box throws JS's "is not a function").
		// Reached before this only through a member/expression callee
		// (TDD-00229).
		// A union of function types (`(() => number) | (() => string)`)
		// is held boxed too: the same dynamic call.
		if sym, found := e.lookup(id.Name); found && (isUnconstrainedDynamic(sym.Ty) || sym.Ty.IsDynamic() && e.unionOfFunctions(id)) {
			fv, err := e.emitExpr(id)
			if err != nil {
				return handled(Value{}, err)
			}
			return handled(e.emitDynCallValueOf(fv, ex.Callee, ex.Args, ex.GetPos()))
		}
		// Static-string eval fast path (TDD-00046 static subset): a
		// compile-time-constant `eval("<expression>")` is compiled through
		// this compiler's own parser + codegen, in place — no embedded
		// engine. Checked after every user-binding lookup, so a
		// user-defined function named `eval` still wins.
		if id.Name == "eval" {
			return handled(e.emitStaticEval(ex.Args, ex.GetPos()))
		}
		// Sibling namespace member call by bare name from inside a member
		// function body (TDD-00148 Stage 4) — retried under the mangled
		// name. Checked after every user-binding lookup, so locals shadow.
		if m := e.nsSibling(id.Name); m != "" {
			rewritten := ast.NewCallExpression(ast.NewIdentifier(m, ex.GetPos()), ex.Args, ex.GetPos())
			return handled(e.emitCall(rewritten))
		}
		// TDD-00129 Stage 2: a capturing nested function declaration called
		// before its declaration statement hoists — emit it on demand here,
		// then dispatch as the closure value it now is.
		if ok, lerr := e.letrecEmitCapturingNested(id.Name); lerr != nil {
			return handled(Value{}, lerr)
		} else if ok {
			if sym, found := e.lookup(id.Name); found && sym.Ty.IsFunc() {
				return handled(e.emitClosureCall(sym, ex.Args, ex.GetPos()))
			}
		}
		// Node's function-style constructors (`http.Server(…)`,
		// `stream.Readable(…)`) construct when called without `new`.
		if e.callableClasses[id.Name] {
			return handled(e.emitExpr(ast.NewNewExpression(id.Name, ex.Args, ex.GetPos())))
		}
		// A class called without `new`: its arguments are evaluated, then
		// the call throws, as in JavaScript (the strict lane has refused it
		// as tsc does).
		if _, isClass := e.classes[id.Name]; isClass {
			for _, a := range ex.Args {
				if sp, ok := a.(*ast.SpreadElement); ok {
					a = sp.Arg
				}
				if _, err := e.emitExpr(a); err != nil {
					return handled(Value{}, err)
				}
			}
			e.emitThrowTypeError("Class constructor " + e.classDisplayName(id.Name) + " cannot be invoked without 'new'")
			return handled(Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil)
		}
		if e.unresolvedReference(id) {
			return handled(e.emitNotDefined(id), nil)
		}
		return handled(Value{}, fmt.Errorf("%d:%d: undefined function or closure '%s'", ex.GetPos().Line, ex.GetPos().Col, id.Name))
	}
	return Value{}, false, nil
}

// missingImportRule: TDD-00049: a friendlier diagnostic for the single most likely cause of
// reaching this fallback — writing e.g. `fs.readFileSync(...)` without
// the now-required `import fs from 'fs'`. Checked last, only once every
// real dispatch path (including a legitimately-imported builtin, which
// never reaches here at all) has already failed to match.
func (e *Emitter) missingImportRule(ex *ast.CallExpression) (Value, bool, error) {
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if id, ok := mem.Object.(*ast.Identifier); ok {
			if _, bound := e.lookup(id.Name); bound {
				return Value{}, false, nil // a binding of that name (a parameter `path`)
			}
			if specifier, known := builtinModuleSpecifiers[id.Name]; known {
				return handled(Value{}, fmt.Errorf("%d:%d: '%s' is not defined — did you forget \"import %s from '%s'\"?",
					ex.GetPos().Line, ex.GetPos().Col, id.Name, id.Name, specifier))
			}
		}
	}
	return Value{}, false, nil
}

// funcValueCalleeRule: General fallback: call the result of any other expression whose
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
func (e *Emitter) funcValueCalleeRule(ex *ast.CallExpression) (Value, bool, error) {
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok && e.unionClassMethod(mem) {
		return Value{}, false, nil
	}
	if e.inferExprType(ex.Callee).IsFunc() {
		val, err := e.emitExpr(ex.Callee)
		if err != nil {
			return handled(Value{}, err)
		}
		e.callAs = e.memberCallAs(ex.Callee)
		return handled(e.emitClosureCallByPtr(val.Ref, val.Ty, ex.Args, ex.GetPos()))
	}
	return Value{}, false, nil
}

// dynMethodRule: A method call on a bare any/unknown receiver is a runtime prototype
// dispatch (TDD-00155 Stage 4): chain-walking property read, then an
// indirect call through the dynamic-function record with the receiver
// bound as `this`.
func (e *Emitter) dynMethodRule(ex *ast.CallExpression) (Value, bool, error) {
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		// A union of scalars is the same self-describing box (an un-narrowed
		// `number | string` receiver: the member its tag holds).
		// So is a union of class instances, each boxed with its class.
		if objTy := e.inferExprType(mem.Object); isUnconstrainedDynamic(objTy) || objTy.IsDynamic() && (isSelfDescribingBox(objTy) || isClassUnionBox(objTy)) {
			objVal, err := e.emitExpr(mem.Object)
			if err != nil {
				return handled(Value{}, err)
			}
			return handled(e.emitDynAnyMethodCall(objVal, mem.Property, ex.Args, ex.GetPos()))
		}
	}
	return Value{}, false, nil
}

// funcOwnPropRule: A method call whose receiver type doesn't have that method reaches here
// too (the callee `obj.prop` isn't function-typed). Report it as the real
// missing-method gap it is — a far more useful diagnostic than the generic
// "only simple function calls" fallback, and (for the conformance leverage
// map) it splits that catch-all bucket by the receiver's type instead of
// lumping every unrecognized method call together.
// A function's own function-valued property the checker did not see
// declared (`f.p(…)`), when nothing else claimed the call.
func (e *Emitter) funcOwnPropRule(ex *ast.CallExpression) (Value, bool, error) {
	if r, ok := e.funcOwnPropCall(ex); ok {
		return handled(e.emitCall(r))
	}
	return Value{}, false, nil
}

// missingMethodRule is a method the receiver's type lacks: a class
// instance's own-property method, a fixed-layout object's boxed field, or
// the "has no method" diagnostic, named by the receiver's type.
func (e *Emitter) missingMethodRule(ex *ast.CallExpression) (Value, bool, error) {
	if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
		if ot := e.inferExprType(mem.Object); ot.IsClass() {
			// A method the instance was given as its own property.
			return handled(e.emitClassMethodCall(ot, mem.Object, mem.Property, ex.Args, ex.GetPos()))
		}
		// A fixed-layout object's boxed field (a generator method, held as
		// `any`): the boxed object's dynamic method call, `this` bound to it.
		if ot := e.inferExprType(mem.Object); ot.IsObject() && !ot.IsDynamic() {
			for _, f := range ot.Fields {
				if f.Name == mem.Property && isUnconstrainedDynamic(f.Ty) {
					objVal, err := e.emitExpr(mem.Object)
					if err != nil {
						return handled(Value{}, err)
					}
					boxed, err := e.emitBoxValue(objVal)
					if err != nil {
						return handled(Value{}, err)
					}
					return handled(e.emitDynAnyMethodCall(boxed, mem.Property, ex.Args, ex.GetPos()))
				}
			}
		}
		// An array's own function property (`a.f = function () {}`): the
		// boxed array's dynamic method call, `this` bound to it.
		if ot := e.inferExprType(mem.Object); ot.IsArray() && !ot.IsObject() && !ot.IsDynamic() && ex.GetPos().Line > 0 {
			objVal, err := e.emitExpr(mem.Object)
			if err != nil {
				return handled(Value{}, err)
			}
			boxed, err := e.emitBoxValue(objVal)
			if err != nil {
				return handled(Value{}, err)
			}
			return handled(e.emitDynAnyMethodCall(boxed, mem.Property, ex.Args, ex.GetPos()))
		}
		// JavaScript (-compat=js): a method the receiver's type does not
		// declare at all (the builtin library has no such member, as
		// `ArrayBuffer.prototype.transferToImmutable` in this runtime) reads
		// undefined, so the call throws once its arguments are evaluated. A
		// method the library declares but this compiler lacks stays the
		// diagnostic below.
		if c := e.front(); e.compatJS() && c != nil && !e.inferExprType(mem.Object).IsDynamic() && c.MemberDecl(mem) == nil && !c.ExpandoMember(mem) {
			if _, err := e.emitExpr(mem.Object); err != nil {
				return handled(Value{}, err)
			}
			for _, a := range ex.Args {
				if sp, ok := a.(*ast.SpreadElement); ok {
					a = sp.Arg
				}
				if _, err := e.emitExpr(a); err != nil {
					return handled(Value{}, err)
				}
			}
			e.emitThrowTypeError(calleeDisplayName(mem) + " is not a function")
			return handled(Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil)
		}
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
		return handled(Value{}, fmt.Errorf("%d:%d: %s has no method '%s'", ex.GetPos().Line, ex.GetPos().Col, recv, mem.Property))
	}
	return Value{}, false, nil
}

// callableValueRule: A callee that is an arbitrary expression producing a callable value — a
// factory-returned decorator `@tag(...)` (which lowers to `(tag(...))(args)`,
// TDD-00161), `getHandler()(x)`, or a function pulled out of an any-typed
// value — is evaluated and called through. Last, after every named-function/
// method/closure-variable/arrow/IIFE form above, so those keep their specific
// paths and diagnostics; member callees already returned above.
func (e *Emitter) callableValueRule(ex *ast.CallExpression) (Value, bool, error) {
	if calleeTy := e.inferExprType(ex.Callee); calleeTy.IsFunc() || calleeTy.IsDynamic() {
		calleeVal, err := e.emitExpr(ex.Callee)
		if err != nil {
			return handled(Value{}, err)
		}
		if calleeVal.Ty.IsFunc() {
			e.callAs = e.memberCallAs(ex.Callee)
			return handled(e.emitClosureCallByPtr(calleeVal.Ref, calleeVal.Ty, ex.Args, ex.GetPos()))
		}
		return handled(e.emitDynCallValueOf(calleeVal, ex.Callee, ex.Args, ex.GetPos()))
	}
	return Value{}, false, nil
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
	case ty.IsBuffer():
		return "Buffer"
	case ty.IsTypedArray():
		return "a TypedArray"
	case ty.IsArrayBuffer():
		return "an ArrayBuffer"
	case ty.IsArray():
		return "an array"
	case ty.IsMap():
		return "a Map"
	case ty.IsSet():
		return "a Set"
	case ty.IsPromise():
		return "a Promise"
	case ty.IsDate():
		return "a Date"
	case ty.IsRegExp():
		return "a RegExp"
	case ty.IsBigInt():
		return "a bigint"
	case ty.IsResponse():
		return "a Response"
	case ty.IsClass():
		return "class '" + ty.ClassName + "'"
	case isStringTy(ty):
		return "a string"
	case ty.IsObject():
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
	for _, arg := range restArgs {
		if sp, ok := arg.(*ast.SpreadElement); ok && !sameRestElem(e.inferExprType(sp.Arg), elemTy) {
			// A spread of other elements (numbers into an any[] rest): the
			// arguments as an array literal of the rest's element type,
			// which converts each element.
			v, err := e.emitExprWithObjectHint(ast.NewArrayLiteral(restArgs, arg.GetPos()), ArrayOf(elemTy))
			if err != nil {
				return "", "", err
			}
			if !v.Ty.IsArray() || v.Ty.ElemType == nil || v.Ty.ElemType.IR != elemTy.IR {
				return "", "", fmt.Errorf("%d:%d: spread array's element type does not match the rest parameter's element type", sp.Arg.GetPos().Line, sp.Arg.GetPos().Col)
			}
			data, n := e.freshReg(), e.freshReg()
			data = e.arrayData(v)
			n = e.arrayLen(v)
			return data, n, nil
		}
	}
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
			if srcElemTy.IR != elemTy.IR || srcElemTy.IsArray() != elemTy.IsArray() || srcElemTy.IsObject() != elemTy.IsObject() {
				return "", "", fmt.Errorf("%d:%d: spread array's element type does not match the rest parameter's element type", sp.Arg.GetPos().Line, sp.Arg.GetPos().Col)
			}
			items = append(items, restItem{spread: true, ptr: ptrReg, length: srcLenReg})
		} else {
			val, verr := e.emitExprWithObjectHint(arg, elemTy)
			if verr != nil {
				return "", "", verr
			}
			val = e.convert(val, elemTy)
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
	e.heapMallocInto(dataReg, bytesReg)
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
	if v, ok, err := e.emitAnyArgDispatch(name, sig, args, pos, keepTupleAgg); ok || err != nil {
		return v, err
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
			if paramTy.IsArray() && !paramTy.IsTypedArray() {
				// An instance of a class extending Array passes its array,
				// so the callee's writes are the instance's.
				body, ok, err := e.hostArrayReceiver(arg)
				if err != nil {
					return Value{}, err
				}
				if ok {
					arg = body
				}
			}
			if paramTy.IsArray() {
				if arrId, ok := arg.(*ast.Identifier); ok && !isSelfDescribingBox(e.inferExprType(arg)) && !e.isDynamicBinding(arrId.Name) {
					sym, ok := e.lookup(arrId.Name)
					if !ok {
						return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", arg.GetPos().Line, arg.GetPos().Col, arrId.Name)
					}
					if !sym.Ty.IsArray() {
						return Value{}, fmt.Errorf("%d:%d: '%s' is not an array", arg.GetPos().Line, arg.GetPos().Col, arrId.Name)
					}
					// A concrete array passed as `any[]` (or the reverse): its
					// elements converted, in a copy.
					if toAny, fromAny := arrayElemsConvert(sym.Ty, paramTy); toAny || fromAny {
						// A named array: a fresh header over its current data/len.
						dp, dl, _, err := e.resolveArrayForHOF(arg, arg.GetPos())
						if err != nil {
							return Value{}, err
						}
						a1 := e.newArrayHeader(dp, dl)
						converted, _ := e.convertArrayElems(Value{Ref: a1, Ty: sym.Ty}, paramTy)
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
					if val.Ty.IsNull() && paramTy.Nullable() {
						val = e.emitAbsentArrayValue(paramTy) // f(null) / f(undefined)
					}
					if isSelfDescribingBox(val.Ty) {
						// An `any` holding an array (a Buffer, …) unboxes to it.
						val = e.emitUnboxBoxToType(val.Ref, paramTy)
					}
					if !val.Ty.IsArray() {
						return Value{}, fmt.Errorf("%d:%d: expression does not yield an array", arg.GetPos().Line, arg.GetPos().Col)
					}
					if converted, ok := e.convertArrayElems(val, paramTy); ok {
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
				if paramTy.IsDynamic() {
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
					if !convertiblePure(val.Ty, paramTy) {
						return Value{}, fmt.Errorf("%d:%d: argument %d has a type incompatible with the parameter's declared type — this compiler is a typed subset", arg.GetPos().Line, arg.GetPos().Col, i+1)
					}
					if e.compatJS() && paramTy.IsFunc() && paramTy.Nullable() && !val.Ty.IsFunc() && !val.Ty.IsDynamic() && val.Ty.IR != "ptr" {
						// Untyped JavaScript passing a number or boolean
						// where a builtin takes an optional callback
						// (`http.createServer(listener, 1)`): a function
						// slot cannot hold it, so the callee sees no
						// callback, as it ignores a non-function one.
						val = Value{Ref: "null", Ty: paramTy}
					} else {
						val = e.convert(val, paramTy)
					}
				}
				argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
				pv := val
				paramScalar = &pv
			}
		} else if i < len(sig.Defaults) && sig.Defaults[i] != nil {
			// Evaluate default expression at call site. Array-typed
			// defaults need the same (header, len) pair the direct-arg path
			// above passes — an array-typed default
			// (`a: number[] = [1,2,3]`) must cross as the callee's two
			// params, not one value, or clang rejects the call.
			// Earlier scalar parameters are in scope for this default.
			pushScratch()
			if paramTy.IsArray() {
				val, err := e.emitExprWithObjectHint(sig.Defaults[i], paramTy)
				if err != nil {
					return Value{}, fmt.Errorf("default value for param %d: %w", i, err)
				}
				if !val.Ty.IsArray() {
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
				if paramTy.IsDynamic() {
					if val, err = e.emitBoxValueWidened(val, sig.Defaults[i]); err != nil {
						return Value{}, err
					}
				} else if paramTy.IR != "" {
					val = e.convert(val, paramTy)
				}
				argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
				pv := val
				paramScalar = &pv
			}
			popScratch()
		} else if i < len(sig.Optional) && sig.Optional[i] || paramTy.IsDynamic() && !paramTy.IsArray() || e.compatJS() {
			// A required `any` parameter left out (plain JS; tsc rejects the
			// call) is undefined, as an omitted optional one is; untyped
			// JavaScript may omit any argument (as the closure call does).
			// ADR-00164: an omitted `param?: T` argument gets T's zero
			// value, the same undefined stand-in ADR-00157/ADR-00158 use.
			// Array-typed params decompose into two LLVM params (ptr, i64
			// len) at the callee side, so their "zero value" is an empty
			// array (null ptr, 0 len), not a single zeroLiteral() operand.
			// A nullable scalar's omitted value is a genuinely absent
			// { i1, T } aggregate (present = false).
			if paramTy.IsArray() {
				argParts = append(argParts, "ptr "+e.omittedArrayArgHeader(paramTy), "i64 0")
			} else if isNullableScalar(paramTy) {
				argParts = append(argParts, nullableScalarStorageIR(paramTy)+" zeroinitializer")
				paramNullableAgg = "zeroinitializer"
			} else if paramTy.IsDynamic() {
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
			scratchSyms[sig.ParamNames[i]] = Symbol{Ptr: slot, Ty: paramTy}
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
		if spread, ok := singleSpread(restArgs); ok && sameRestElem(e.inferExprType(spread.Arg), elemTy) {
			// f(fixed..., ...arr): forward the array's own (ptr, len) buffer
			// straight into the rest slot — the rest-param ABI is (ptr, i64),
			// exactly what an array argument already lowers to (TDD-00106).
			ptrReg, lenReg, _, err := e.resolveArrayForHOF(spread.Arg, spread.Arg.GetPos())
			if err != nil {
				return Value{}, err
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
			e.heapMallocInto(dataReg, fmt.Sprint(n*int64(elemTy.Align())))
			for i, arg := range restArgs {
				val, err := e.emitExprWithObjectHint(arg, elemTy)
				if err != nil {
					return Value{}, err
				}
				val = e.convert(val, elemTy)
				gepReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataReg, i))
				e.storeArrayElem(gepReg, elemTy, val)
			}
			restHdr := e.newArrayHeader(dataReg, fmt.Sprintf("%d", n))
			argParts = append(argParts, "ptr "+restHdr, fmt.Sprintf("i64 %d", n))
		}
	}
	e.noteArgc(countsArgs(sig.HasRest, sig.ParamNames), args, regularCount)
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
	if retTy.IsTuple() && retTy.TupleByVal {
		if keepTupleAgg {
			return Value{Ref: reg, Ty: retTy}, nil
		}
		return e.spillTupleAggregate(reg, retTy), nil
	}
	// A non-suspending async fn (didn't take the MaySuspend fiber path above)
	// now returns a settled task-shaped promise (TDD-00084 Part A) — tag it so
	// `await`/`.then` take the task path, matching the may-suspend result.
	if sig.IsAsync && retTy.IsPromise() {
		retTy.PromiseTask = true
	}
	if retTy.IsArray() {
		// The array return ABI is a header pointer (TDD-00213 Stage 3), which is
		// the array value itself, so the result aliases.
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
		if base := ast.Unmangle(root); base != root {
			c2 := base + chain[i:]
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

// nsSiblingTarget is the mangled binding a bare store target names inside
// a namespace member function, when no local binds the name; else nil.
func (e *Emitter) nsSiblingTarget(id *ast.Identifier) *ast.Identifier {
	if _, ok := e.lookup(id.Name); ok {
		return nil
	}
	m := e.nsSibling(id.Name)
	if m == "" || m == id.Name {
		return nil
	}
	syn := ast.NewIdentifier(m, id.GetPos())
	e.originate(syn, id)
	return syn
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
	if base := ast.Unmangle(name); base != name {
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
	if ex.Optional || e.receiverPassed[ex] || ex.Intrinsic != "" {
		return nil // an intrinsic (a template object) has no callee
	}
	switch c := ex.Callee.(type) {
	case *ast.MemberExpression, *ast.SuperExpression, *ast.IndexExpression:
		return nil
	case *ast.Identifier:
		// A generic function called by name is instantiated for the call;
		// its erased value (and the instantiation its type builds) is not.
		if _, generic := e.genericFuncs[c.Name]; generic && !e.isShadowedByLocal(c.Name) {
			return nil
		}
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

// emitArgOrDefault passes a defaulted scalar or `any` parameter an argument
// that may be undefined at run time (an `any`, a `T | undefined`): the
// argument, or — when it is undefined, as JavaScript substitutes — the
// default, evaluated only then. ok is false when the parameter has no
// default or the argument cannot be undefined.
func (e *Emitter) emitArgOrDefault(arg ast.Expression, paramTy Type, sig FuncSig, i int, pushScratch, popScratch func()) (Value, bool, error) {
	if i >= len(sig.Defaults) || sig.Defaults[i] == nil || paramTy.IR == "" || paramTy.Inferred || paramTy.IsArray() || isNullableScalar(paramTy) {
		return Value{}, false, nil
	}
	argTy := e.inferExprType(arg)
	if !argTy.IsDynamic() && !argTy.IsUndefined() {
		return Value{}, false, nil
	}
	if argTy.IsDynamic() && paramTy.IsDynamic() && argTy.UnionMembers != nil {
		return Value{}, false, nil
	}
	raw, err := e.emitPreserveNullableOperand(arg)
	if err != nil {
		return Value{}, true, err
	}
	var isU string
	switch {
	case raw.Ty.IsDynamic():
		isU = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isU, raw.Ref, nbUndefined))
	case isNullableScalar(raw.Ty) && raw.Ty.IsUndefined():
		present, _ := e.nullableScalarAggParts(raw)
		isU = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", isU, present))
	case raw.Ty.IR == "ptr" && raw.Ty.IsUndefined():
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
	if paramTy.IsDynamic() {
		if paramTy.UnionMembers != nil {
			v = e.relayoutForUnion(v, paramTy)
		}
		return e.emitBoxValueWidened(v, src)
	}
	if !convertiblePure(v.Ty, paramTy) && !v.Ty.IsDynamic() && !isNullableScalar(v.Ty) {
		return Value{}, fmt.Errorf("%d:%d: argument has a type incompatible with the parameter's declared type — this compiler is a typed subset", src.GetPos().Line, src.GetPos().Col)
	}
	if isNullableScalar(v.Ty) {
		_, payload := e.nullableScalarAggParts(v)
		v = payload
	}
	c := e.convert(v, paramTy)
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
	if _, isAs := mem.Object.(*ast.AsExpression); isAs {
		return nil, false
	}
	ty := e.inferExprType(mem.Object)
	if !ty.IsFunc() || ty.IsDynamic() || e.isNamespaceMember(mem) {
		return nil, false
	}
	// A namespace merged into the function declares its own members, a
	// builtin constructor's interface its statics (`String.fromCharCode`),
	// and lib's Function interface `call`, `apply`, `bind` and `toString`.
	if c := e.front(); c != nil && !c.ExpandoMember(mem) {
		switch c.MemberDecl(mem).(type) {
		case *ast.FunctionDeclaration, *ast.VarDeclaration, *ast.MethodSignature, *ast.PropertySignature:
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
		if !ok || r.IsArray() || r.IsObject() {
			return Type{}, false
		}
		params = append(params, r)
	}
	ret := TypeVoid
	if t.Result != nil {
		r, ok := lowerRepr(t.Result)
		if !ok || r.IsArray() || r.IsObject() {
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
			return e.emitObjectStatic("hasOwn", ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("Object", pos), "hasOwn", pos), []ast.Expression{recv, arg(1)}, pos))
		}, true
	case "propertyIsEnumerable":
		return func() (Value, error) {
			v, err := e.emitExprWithObjectHint(recv, TypeAny)
			if err != nil {
				return Value{}, err
			}
			return e.emitPropertyIsEnumerable(v, arg(1), pos)
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

// builtinIteratorCall is `x[Symbol.iterator]()` on a builtin iterable as
// the method that method is: an array's, typed array's or Set's
// `values()`, a Map's `entries()`, and a string's iterator over its code
// points (`[...s].values()`).
func (e *Emitter) builtinIteratorCall(ex *ast.CallExpression) (*ast.CallExpression, bool) {
	if m := indexCalleeAsMember(ex); m != nil {
		ex = m
	}
	mem, ok := ex.Callee.(*ast.MemberExpression)
	if !ok || mem.Property != syncIteratorMethodName || len(ex.Args) != 0 {
		return nil, false
	}
	t := e.inferExprType(mem.Object)
	if c := e.front(); c != nil && !t.IsArray() && !t.IsSet() && !t.IsMap() && !isForOfStringTy(t) {
		// Asked before the receiver's own declaration was registered (a
		// module global's pre-pass): the checker's type.
		if ct := c.TypeOf(mem.Object); !c.Unanswered(ct) {
			if rt, ok := e.repr().of(ct); ok {
				t = rt
			}
		}
	}
	if t.Nullable() || t.IsDynamic() || t.IsClass() {
		return nil, false
	}
	pos := ex.GetPos()
	call := func(obj ast.Expression, m string) *ast.CallExpression {
		return ast.NewCallExpression(ast.NewMemberExpression(obj, m, pos), nil, pos)
	}
	switch {
	case t.IsArray() || t.IsSet():
		return call(mem.Object, "values"), true
	case t.IsMap():
		return call(mem.Object, "entries"), true
	case isForOfStringTy(t):
		chars := ast.NewArrayLiteral([]ast.Expression{ast.NewSpreadElement(mem.Object, pos)}, pos)
		return call(chars, "values"), true
	}
	return nil, false
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

// fnInvokeMethods are Function.prototype's invocation methods, which a
// function's typed call path handles.
var fnInvokeMethods = map[string]bool{"call": true, "apply": true, "bind": true}

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
	// A method of a value held boxed, or of a function's own object (a
	// builtin function's `parseInt.toString()`): the dynamic method call,
	// its receiver bound, its result as the member's type says.
	// An array's own named function property, or one its prototype inherits
	// from Object.prototype, called: likewise.
	arrOwn := func(t Type) bool {
		if !t.IsArray() || t.IsObject() || t.IsDynamic() {
			return false
		}
		_, ok := e.classMethod(valueProtoClass(t), mem.Property)
		return !ok
	}
	if objTy := e.inferExprType(mem.Object); (objTy.IsDynamic() || objTy.IsFunc() && !fnInvokeMethods[mem.Property]) && !objTy.IsArray() || arrOwn(objTy) {
		obj, err := e.emitExpr(mem.Object)
		if err != nil {
			return Value{}, err
		}
		if obj, err = e.emitBoxValue(obj); err != nil {
			return Value{}, err
		}
		v, err := e.emitDynAnyMethodCall(obj, mem.Property, ex.Args, ex.GetPos())
		if err != nil || mt.FuncRetType == nil || mt.FuncRetType.IR == "void" {
			return v, err
		}
		return e.convert(v, *mt.FuncRetType), nil
	}
	if objTy := e.inferExprType(mem.Object); objTy.IsObject() && !objTy.IsDynamic() && !objTy.IsArray() && !mem.Optional {
		return e.emitFuncFieldCallReceiving(ex, mem, mt)
	}
	memVal, err := e.emitExpr(mem)
	if err != nil {
		return Value{}, err
	}
	e.callAs = e.memberCallAs(mem)
	return e.emitClosureCallByPtr(memVal.Ref, memVal.Ty, ex.Args, ex.GetPos())
}

// emitFuncFieldCallReceiving is `obj.f(…)` through a function type without
// `this`, whose value may still take one: a method shorthand reading `this`
// stored in an interface's method slot is its boxed function behind a thunk
// (emitAnyToClosure), whose env starts with @__kml_d2s_env. That one is
// called by name with obj as its receiver, as JavaScript calls every
// method; any other function directly. One branch runs, so each argument
// is evaluated once.
func (e *Emitter) emitFuncFieldCallReceiving(ex *ast.CallExpression, mem *ast.MemberExpression, mt Type) (Value, error) {
	recv, err := e.receiverTemp(mem.Object)
	if err != nil {
		return Value{}, err
	}
	rmem := ast.NewMemberExpression(recv, mem.Property, mem.GetPos())
	memVal, err := e.emitExpr(rmem)
	if err != nil {
		return Value{}, err
	}
	if memVal.Ty.FuncThis {
		// The field holds a method taking its `this` (an object literal's
		// method reading it): called with the object as its receiver.
		return e.emitClosureCallByPtr(memVal.Ref, memVal.Ty, append([]ast.Expression{recv}, ex.Args...), ex.GetPos())
	}
	// Each argument is evaluated once, here, against its parameter's type:
	// both branches then read the same values (a closure argument emitted
	// twice would box its captures in only one of them).
	pure := func(a ast.Expression) bool {
		switch a.(type) {
		case *ast.Identifier, *ast.StringLiteral, *ast.NumberLiteral, *ast.BooleanLiteral, *ast.NullLiteral, *ast.ThisExpression:
			return true
		}
		return false
	}
	for i, a := range ex.Args {
		if pure(a) {
			continue
		}
		t := e.inferExprType(a)
		if i < len(mt.FuncParams) {
			t = mt.FuncParams[i]
		}
		if _, spread := a.(*ast.SpreadElement); spread || isNullableScalar(t) || t.IR == "" || t.IR == "void" {
			// Not one register each: the direct call only.
			e.callAs = e.memberCallAs(mem)
			return e.emitClosureCallByPtr(memVal.Ref, memVal.Ty, ex.Args, ex.GetPos())
		}
	}
	args := make([]ast.Expression, len(ex.Args))
	for i, a := range ex.Args {
		if pure(a) {
			args[i] = a
			continue
		}
		var v Value
		if i < len(mt.FuncParams) {
			v, err = e.emitExprWithObjectHint(a, mt.FuncParams[i])
		} else {
			v, err = e.emitExpr(a)
		}
		if err != nil {
			return Value{}, err
		}
		if i < len(mt.FuncParams) {
			v = e.convert(v, mt.FuncParams[i])
		}
		args[i] = e.bindValue(v, a.GetPos())
	}
	e.ensureD2SEnvTag()
	var retTy Type
	if mt.FuncRetType != nil {
		retTy = *mt.FuncRetType
	}
	hasRet := retTy.IR != "" && retTy.IR != "void"
	var slot string
	if hasRet {
		slot = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, storageIR(retTy)))
	}
	chkL, envL, dynL, statL, doneL := e.freshLabel("ffc.chk"), e.freshLabel("ffc.env"), e.freshLabel("ffc.dyn"), e.freshLabel("ffc.static"), e.freshLabel("ffc.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(memVal.Ref), statL, chkL))
	e.emitLabel(chkL)
	epp, env := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", epp, memVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", env, epp))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(env), statL, envL))
	e.emitLabel(envL)
	tagw, isD2S := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", tagw, env))
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, @__kml_d2s_env", isD2S, tagw))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isD2S, dynL, statL))

	e.emitLabel(dynL)
	ov, err := e.emitExpr(recv)
	if err != nil {
		return Value{}, err
	}
	box, err := e.emitBoxValue(ov)
	if err != nil {
		return Value{}, err
	}
	// The receiver is this object, never an array or string: the method is
	// its property, whatever its name (`join`, `slice`).
	dv, err := e.emitDynAnyMethodCallPlain(box, mem.Property, args, ex.GetPos())
	if err != nil {
		return Value{}, err
	}
	if hasRet {
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", storageIR(retTy), e.convert(dv, retTy).Ref, slot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(statL)
	e.callAs = e.memberCallAs(mem)
	sv, err := e.emitClosureCallByPtr(memVal.Ref, memVal.Ty, args, ex.GetPos())
	if err != nil {
		return Value{}, err
	}
	if hasRet {
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", storageIR(retTy), e.convert(sv, retTy).Ref, slot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	if !hasRet {
		return Value{Ref: "", Ty: TypeVoid}, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, storageIR(retTy), slot))
	return Value{Ref: r, Ty: retTy}, nil
}

// isTemplateRaw reports whether a member read is `strings.raw` on a
// template object (the checker types the base `TemplateStringsArray`).
func (e *Emitter) isTemplateRaw(ex *ast.MemberExpression) bool {
	if ex.Property != "raw" || ex.Optional {
		return false
	}
	c := e.front()
	if c == nil {
		return false
	}
	t := c.TypeOf(ex.Object)
	return t != nil && t.Symbol != nil && t.Symbol.Name == "TemplateStringsArray" && e.inferExprType(ex.Object).IsArray()
}

// emitTemplateRaw reads a template object's raw strings (registered by
// emitTemplateObject); an array the runtime never registered reads as its
// own strings.
func (e *Emitter) emitTemplateRaw(ex *ast.MemberExpression) (Value, error) {
	v, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}
	e.ensureDynJSONC()
	cooked := e.arrayReturnHeader(v)
	raw, has, h := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_template_raw(ptr %s)", raw, cooked))
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", has, raw))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", h, has, raw, cooked))
	return e.arrayValueFromHeaderReg(h, ArrayOf(TypePtr)), nil
}

// arrayMutators are the Array methods that change their receiver.
var arrayMutators = map[string]bool{
	"push": true, "pop": true, "shift": true, "unshift": true, "splice": true,
	"reverse": true, "sort": true, "fill": true, "copyWithin": true,
}

// emitReceiverMethodCall emits `recv.m(…)` dispatched on the receiver's
// type: the method of the receiver's owner (a class, a host object, a
// collection, a generator) or a function-valued field. done is false when
// no owner takes the call, for the dispatch after it.
func (e *Emitter) emitReceiverMethodCall(ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
	for _, owner := range receiverOwners {
		if v, done, err := owner(e, ex, mem); done {
			return v, true, err
		}
	}
	return Value{}, false, nil
}

// receiverOwner emits a method call its receiver's owner takes; done is
// false for a call it does not own.
type receiverOwner func(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error)

// receiverOwners are tried in order. A class comes before the host owners
// that dispatch by name, so a class method sharing a builtin's name wins,
// as JavaScript's prototype-chain lookup does; a dictionary's
// hasOwnProperty before the Map owner a dictionary also answers to.
var receiverOwners []receiverOwner

func init() {
	receiverOwners = []receiverOwner{
		(*Emitter).flatArrayCall,
		(*Emitter).namespaceCall,
		(*Emitter).classReceiverCall,
		(*Emitter).bigIntArrayRejectedCall,
		(*Emitter).dynArrayCall,
		(*Emitter).emitByteReceiverCall,
		(*Emitter).dictHasOwnCall,
		(*Emitter).collectionCall,
		(*Emitter).collIterCall,
		(*Emitter).generatorCall,
		(*Emitter).funcFieldCall,
	}
}

// flatArrayCall: a `/** @value */` flat array (TDD-00134 Stage 2) supports
// push; everything else needs the pointer-slot layout.
func (e *Emitter) flatArrayCall(ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
	objTy := e.inferExprType(mem.Object)
	if !objTy.IsFlatArray() {
		return Value{}, false, nil
	}
	if mem.Property == "push" {
		return handled(e.emitFlatArrayPush(objTy, mem, ex.Args, ex.GetPos()))
	}
	return handled(Value{}, fmt.Errorf("%d:%d: a @value array supports index read/write, .length, for...of, and .push — '%s' needs a regular (pointer-element) array", ex.GetPos().Line, ex.GetPos().Col, mem.Property))
}

func (e *Emitter) namespaceCall(ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
	if id, ok := mem.Object.(*ast.Identifier); ok && !e.isShadowedByLocal(id.Name) {
		if f, ok := namespaceMethods[id.Name]; ok {
			return f(e, ex, mem)
		}
	}
	return Value{}, false, nil
}

// classReceiverCall is a user class's method, an `any` field holding a
// function (called with the instance as `this`), or the method of the Map
// or Set the class extends. A field holding a typed closure falls through
// to funcFieldCall.
func (e *Emitter) classReceiverCall(ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
	objTy := e.inferExprType(mem.Object)
	if !objTy.IsClass() {
		return Value{}, false, nil
	}
	// `new Box<string>(…).get()`: the instantiation the receiver's `new`
	// makes, registered before its methods are looked up.
	if _, err := e.ensureGenericInstance(objTy.ClassName); err != nil {
		return handled(Value{}, err)
	}
	if _, ok := e.classes[objTy.ClassName]; !ok {
		if ne, isNew := mem.Object.(*ast.NewExpression); isNew {
			if genDecl, generic := e.genericClasses[ne.ClassName]; generic {
				if targs, ok := e.newTypeArgs(genDecl, ne); ok {
					if _, err := e.instantiateGenericClass(genDecl, e.buildTypeArgSubs(genDecl.TypeParams, targs)); err != nil {
						return handled(Value{}, err)
					}
				}
			}
		}
	}
	info, ok := e.classes[objTy.ClassName]
	if !ok {
		return Value{}, false, nil
	}
	if _, ok := info.MethodSigs[mem.Property]; ok {
		v, err := e.emitClassMethodCall(objTy, mem.Object, mem.Property, ex.Args, ex.GetPos())
		if err == nil && v.Ty.IsClass() {
			v.Ty = e.genericMethodResult(ex, objTy, mem.Property, v.Ty)
		} else if err == nil && isUnconstrainedDynamic(v.Ty) {
			if ct := e.genericMethodResult(ex, objTy, mem.Property, v.Ty); ct.IsClass() {
				v = e.convert(v, ct)
			}
		}
		return handled(v, err)
	}
	if _, fty, isField := info.Ty.FieldIndex(mem.Property); isField && isUnconstrainedDynamic(fty) {
		return handled(e.emitClassMethodCall(objTy, mem.Object, mem.Property, ex.Args, ex.GetPos()))
	}
	if info.HostBase != nil {
		inst, err := e.emitExpr(mem.Object)
		if err != nil {
			return handled(Value{}, err)
		}
		return handled(e.emitHostBaseCall(inst, *info.HostBase, mem.Property, ex.Args, ex.GetPos()))
	}
	return Value{}, false, nil
}

// bigIntArrayRejectedCall: a BigInt64Array/BigUint64Array supports only an
// allow-list of array methods (TDD-00101) — the generic HOF/search/sort
// machinery passes raw i64 scalars to callbacks and comparisons.
func (e *Emitter) bigIntArrayRejectedCall(ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
	if bigIntElemRejectedMethods[mem.Property] && e.inferExprType(mem.Object).BigIntElem {
		return handled(Value{}, fmt.Errorf("%d:%d: a BigInt64Array/BigUint64Array has no .%s() (no TypedArray does)", ex.GetPos().Line, ex.GetPos().Col, mem.Property))
	}
	return Value{}, false, nil
}

// dynArrayCall: an Array method on an `any` is the run-time array's own,
// else the receiver's property (emitDynArrayMethodCall).
func (e *Emitter) dynArrayCall(ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
	if _, ok := dynArrayMethods[mem.Property]; !ok || !isUnconstrainedDynamic(e.inferExprType(mem.Object)) {
		return Value{}, false, nil
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return handled(Value{}, err)
	}
	return handled(e.emitDynAnyMethodCall(objVal, mem.Property, ex.Args, ex.GetPos()))
}

// dictHasOwnCall: a dictionary is a plain object, and an array inherits
// from one, so hasOwnProperty is Object.prototype's.
func (e *Emitter) dictHasOwnCall(ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
	if ot := e.inferExprType(mem.Object); mem.Property == "hasOwnProperty" && len(ex.Args) == 1 && (ot.IsDynamicObject() || ot.IsArray() && !ot.IsTuple() && !ot.Nullable()) {
		return handled(e.emitHasOwnProperty(mem.Object, ex.Args[0], "hasOwnProperty", true, ex.GetPos()))
	}
	return Value{}, false, nil
}

// collectionCall is a Map's, Set's, WeakMap's or WeakSet's method, on any
// receiver expression of that type (a field, an element, a call result).
func (e *Emitter) collectionCall(ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
	if objTy := e.inferExprType(mem.Object); !objTy.IsMap() && !objTy.IsSet() {
		return Value{}, false, nil
	}
	ty, ptr, err := e.resolveMapOrSetForCall(mem.Object, ex.GetPos())
	if err != nil {
		return handled(Value{}, err)
	}
	switch {
	case ty.Weak:
		return handled(e.emitWeakCall(ty, ptr, mem.Property, ex.Args, ex.GetPos()))
	case ty.IsMap():
		return handled(e.emitMapCall(ty, ptr, mem.Property, ex.Args, ex.GetPos()))
	}
	return handled(e.emitSetCall(ty, ptr, mem.Property, ex.Args, ex.GetPos()))
}

func (e *Emitter) collIterCall(ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
	if e.inferExprType(mem.Object).IsCollIter() {
		return handled(e.emitCollIterCall(mem, ex.Args, ex.GetPos()))
	}
	return Value{}, false, nil
}

// generatorCall is a generator's next, throw or return (TDD-00061).
func (e *Emitter) generatorCall(ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
	if _, own := generatorMethods[mem.Property]; own {
		if gt := e.inferExprType(mem.Object); gt.IsGenerator() {
			return handled(e.emitGeneratorUserCall(mem, gt, ex.Args, ex.GetPos()))
		}
	} else if e.genProtos && !ex.Optional && !mem.Optional {
		// Another method is its prototype chain's (`h.prototype.m = …`):
		// called on the boxed instance.
		if gt := e.inferExprType(mem.Object); gt.IsGenerator() {
			ov, err := e.emitExpr(mem.Object)
			if err != nil {
				return handled(Value{}, err)
			}
			boxed, err := e.emitBoxValue(ov)
			if err != nil {
				return handled(Value{}, err)
			}
			return handled(e.emitDynAnyMethodCall(boxed, mem.Property, ex.Args, ex.GetPos()))
		}
	}
	return Value{}, false, nil
}

// funcFieldCall calls a function-typed field (`obj.callback(…)`) as a
// closure.
func (e *Emitter) funcFieldCall(ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
	if e.unionClassMethod(mem) {
		return Value{}, false, nil
	}
	if mt := e.inferExprType(mem); mt.IsFunc() {
		return handled(e.emitFuncFieldCall(ex, mem, mt))
	}
	return Value{}, false, nil
}

// handled marks a call emitReceiverMethodCall took.
func handled(v Value, err error) (Value, bool, error) { return v, true, err }

// namespaceMethods are the calls on a builtin namespace compiled here, by
// the namespace's binding name: done is false for a member left to the
// namespace's declaration.
var namespaceMethods map[string]func(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error)

func init() {
	namespaceMethods = map[string]func(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error){
		"process":             (*Emitter).processCredentialCall,
		"Memory__kml_builtin": (*Emitter).memoryNamespaceCall,
		"http__kml_builtin":   (*Emitter).httpNamespaceCall,
	}
}

// processCredentialCall rejects process.getuid/geteuid/getgid/getegid,
// which Node has no form of on Windows (they are undefined there, so the
// bare call is a TypeError in Node), at compile time (TDD-00177). The
// portable spelling is the optional call, which evaluates to `undefined`
// here (emitOptionalCalleeCall). Elsewhere they are
// internal_process_methods.ts's (processMemberRewrite). Untyped JS
// (-compat=js) has no such check: the call is Node's runtime TypeError,
// after its arguments, and only when reached (a program may test the
// platform first).
// processCredentialMethods are process's POSIX-only credential getters.
var processCredentialMethods = map[string]bool{"getuid": true, "geteuid": true, "getgid": true, "getegid": true}

func (e *Emitter) processCredentialCall(ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
	if !processCredentialMethods[mem.Property] {
		return Value{}, false, nil
	}
	if e.compatJS() {
		for _, a := range ex.Args {
			if _, err := e.emitExpr(a); err != nil {
				return handled(Value{}, err)
			}
		}
		ty := e.inferExprType(ex)
		e.emitThrowTypeError("process." + mem.Property + " is not a function")
		e.emitLabel(e.freshLabel("process.cred.dead"))
		return handled(Value{Ref: zeroRef(ty), Ty: ty}, nil)
	}
	return handled(Value{}, fmt.Errorf("%d:%d: process.%s is not available on Windows (Node defines it only on POSIX) — use the optional call `process.%s?.()`, which is `undefined` on Windows", ex.GetPos().Line, ex.GetPos().Col, mem.Property, mem.Property))
}

func (e *Emitter) memoryNamespaceCall(ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
	if mem.Property != "free" {
		return Value{}, false, nil
	}
	return handled(e.emitMemoryFree(ex.Args, ex.GetPos()))
}

// httpNamespaceCall rejects the bespoke server functions that moved to
// klain:http (ADR-00635): Node has these as Server methods, not `http.*`
// functions.
func (e *Emitter) httpNamespaceCall(ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
	switch mem.Property {
	case "listen", "close", "closeAllConnections":
		return handled(Value{}, fmt.Errorf("%d:%d: http.%s is not a Node API — use `http.createServer(handler).listen(port)` for the faithful server, or import the bespoke handler-returns-response model from klain:http: `import http from 'klain:http'`", ex.GetPos().Line, ex.GetPos().Col, mem.Property))
	}
	return Value{}, false, nil
}

// emitByteReceiverCall is a method an ArrayBuffer, a Buffer or a number
// owns over the shared array and object paths: an ArrayBuffer's grow,
// resize (ADR-00494) and slice; a Buffer's string search (ADR-00558) and
// fill (ADR-00559); a number's toLocaleString.
func (e *Emitter) emitByteReceiverCall(ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
	objTy := e.inferExprType(mem.Object)
	var table map[string]receiverOwner
	switch {
	case objTy.IsArrayBuffer():
		table = arrayBufferOwnMethods
	case objTy.IsBuffer():
		table = bufferOwnMethods
	case isNumberTy(objTy):
		table = numberOwnMethods
	}
	if f, ok := table[mem.Property]; ok {
		return f(e, ex, mem)
	}
	return Value{}, false, nil
}

// arrayBufferOwnMethods, bufferOwnMethods and numberOwnMethods are the
// methods emitByteReceiverCall takes for those receivers.
var arrayBufferOwnMethods, bufferOwnMethods, numberOwnMethods map[string]receiverOwner

func init() {
	grow := func(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
		objVal, err := e.emitExpr(mem.Object)
		if err != nil {
			return handled(Value{}, err)
		}
		return handled(e.emitBufferGrow(objVal, ex.Args, ex.GetPos(), mem.Property == "resize"))
	}
	arrayBufferOwnMethods = map[string]receiverOwner{
		"grow":   grow,
		"resize": grow,
		"slice": func(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
			return handled(e.emitArrayBufferSlice(mem, ex.Args, ex.GetPos()))
		},
	}
	// A string needle searches its bytes; a number stays on the shared
	// array path.
	search := func(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
		if len(ex.Args) >= 1 && isStringTy(e.inferExprType(ex.Args[0])) {
			return handled(e.emitBufferStringSearch(mem, mem.Property, ex.Args, ex.GetPos()))
		}
		return Value{}, false, nil
	}
	bufferOwnMethods = map[string]receiverOwner{
		"indexOf":     search,
		"includes":    search,
		"lastIndexOf": search,
		"fill": func(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
			return handled(e.emitBufferFill(mem, ex.Args, ex.GetPos()))
		},
	}
	numberOwnMethods = map[string]receiverOwner{
		"toLocaleString": func(e *Emitter, ex *ast.CallExpression, mem *ast.MemberExpression) (Value, bool, error) {
			return handled(e.emitNumberToLocaleString(mem, ex.Args, ex.GetPos()))
		},
	}
}

// moduleNamespaceRule builds a module namespace object (the resolver's
// `Object.freeze({ … })` marked ModuleNamespace): the exports' literal as a
// dynamic object, frozen, with a null prototype, shown as `[Module: null
// prototype]`.
func (e *Emitter) moduleNamespaceRule(ex *ast.CallExpression) (Value, bool, error) {
	if ex.Intrinsic != ast.ModuleNamespaceIntrinsic || len(ex.Args) != 1 {
		return Value{}, false, nil
	}
	// A `let` export is a live accessor (the resolver's): the bag is built
	// with it and marks it a live binding.
	if lit, isLit := ex.Args[0].(*ast.ObjectLiteral); isLit && hasAccessor(lit) {
		bag, err := e.emitDynObjLiteral(lit)
		if err != nil {
			return Value{}, true, err
		}
		e.declareFn("__kml_dynobj_mark_live", "declare void @__kml_dynobj_mark_live(ptr)")
		e.declareFn("__kml_dynobj_module_ns", "declare void @__kml_dynobj_module_ns(ptr)")
		e.ensureNanBox()
		pay := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pay, bag.Ref))
		p := e.emitIntToPtr(pay)
		e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_mark_live(ptr %s)", p))
		e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_module_ns(ptr %s)", p))
		return bag, true, nil
	}
	v, err := e.emitExpr(ex.Args[0])
	if err != nil {
		return Value{}, true, err
	}
	// An object literal of untyped values is already a dynamic object.
	bag := v
	if !v.Ty.IsDynamic() {
		if bag, err = e.emitStaticObjToBag(v); err != nil {
			return Value{}, true, err
		}
	}
	e.declareFn("__kml_dynobj_module_ns", "declare void @__kml_dynobj_module_ns(ptr)")
	e.ensureNanBox()
	pay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pay, bag.Ref))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_module_ns(ptr %s)", e.emitIntToPtr(pay)))
	return bag, true, nil
}

// emitPropertyIsEnumerable is Object.prototype.propertyIsEnumerable with
// receiver v: a dynamic object answers from its own property's attributes
// (symbol keys included); any other value by whether Object.keys lists the
// key.
func (e *Emitter) emitPropertyIsEnumerable(v Value, keyExpr ast.Expression, pos ast.Pos) (Value, error) {
	byKeys := func(recv ast.Expression) (Value, error) {
		kv, err := e.emitObjectStatic("keys", ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("Object", pos), "keys", pos), []ast.Expression{recv}, pos))
		if err != nil {
			return Value{}, err
		}
		keys := e.bindValue(kv, pos)
		return e.emitCall(ast.NewCallExpression(ast.NewMemberExpression(keys, "includes", pos), []ast.Expression{stringCall(keyExpr, pos)}, pos))
	}
	if !v.Ty.IsDynamic() && v.Ty.IsArray() {
		box, err := e.emitBoxValue(v)
		if err != nil {
			return Value{}, err
		}
		v = box
	}
	if !v.Ty.IsDynamic() {
		return byKeys(e.bindValue(v, pos))
	}
	e.ensureDynObj()
	e.declareFn("__kml_dynobj_prop_enumerable", "declare i32 @__kml_dynobj_prop_enumerable(i64, ptr)")
	recv := e.bindValue(v, pos)
	key, err := e.dynAnyKeyRef(keyExpr, pos)
	if err != nil {
		return Value{}, err
	}
	r, isBag := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_dynobj_prop_enumerable(i64 %s, ptr %s)", r, v.Ref, key))
	e.emitInstr(fmt.Sprintf("%s = icmp sge i32 %s, 0", isBag, r))
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", slot))
	bagL, otherL, doneL := e.freshLabel("pie.bag"), e.freshLabel("pie.other"), e.freshLabel("pie.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBag, bagL, otherL))
	e.emitLabel(bagL)
	yes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 1", yes, r))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", yes, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(otherL)
	o, err := byKeys(recv)
	if err != nil {
		return Value{}, err
	}
	o = e.convert(o, TypeBool)
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", o.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", out, slot))
	return Value{Ref: out, Ty: TypeBool}, nil
}

// unionClassMethod reports `recv.m` on a union of class instances that all
// declare m: their methods, dispatched per class, never a function value
// read off the receiver.
func (e *Emitter) unionClassMethod(mem *ast.MemberExpression) bool {
	ot := e.inferExprType(mem.Object)
	if len(ot.UnionMembers) == 0 {
		return false
	}
	for _, m := range ot.UnionMembers {
		if m.ClassName == "" || !e.classHasMethodInherited(m.ClassName, mem.Property) {
			return false
		}
	}
	return true
}

// ambientOverloaded reports a top-level function name declared more than
// once, ambiently (a run of `declare function` signatures).
func (e *Emitter) ambientOverloaded(name string) bool {
	if e.ambientOverloads == nil {
		e.ambientOverloads = map[string]int{}
		for _, st := range e.prog.Body {
			if fd, ok := st.(*ast.FunctionDeclaration); ok && fd.Ambient && e.libStmts[st] == "" {
				e.ambientOverloads[fd.Name]++
			}
		}
	}
	return e.ambientOverloads[name] > 1
}

// unionOfFunctions reports that the checker types e as a union whose
// members are all function types.
func (e *Emitter) unionOfFunctions(x ast.Expression) bool {
	c := e.front()
	if c == nil {
		return false
	}
	t := c.TypeOf(x)
	if c.Unanswered(t) || t.Flags&checker.Union == 0 {
		return false
	}
	for _, m := range t.Types {
		if m.Flags&checker.Object == 0 || m.Kind != checker.Function && len(m.Calls) == 0 {
			return false
		}
	}
	return true
}
