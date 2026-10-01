package llvm

// emit_iter_collect.go — the iteration protocol over a value held in `any`,
// collected into an array: what spread (`[...v]`, `f(...v)`), array
// destructuring and Array.from do with an iterable that is not an array (a
// generator, an object with a [Symbol.iterator]() method).

import (
	"fmt"

	"KlainMainLang/ast"
)

// ensureAnyIterCollect defines @__kml_any_iter_collect(v): the values the
// iteration protocol yields for v, as a boxed `any[]` — the routine is
// `const r = []; for (const x of v) r.push(x); return r`, compiled once
// through the `for…of`-over-`any` lowering, so a value that is not
// iterable throws its TypeError.
func (e *Emitter) ensureAnyIterCollect() {
	if e.usedAnyIterCollect {
		return
	}
	e.usedAnyIterCollect = true
	e.ensureDynArr()
	restore := e.beginDetachedFunc()
	pos := ast.Pos{}

	vSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", vSlot))
	e.emitInstr(fmt.Sprintf("store i64 %%p_v, ptr %s, align 8", vSlot))
	e.define("__kml_collect_v", Symbol{Ptr: vSlot, Ty: TypeAny})
	arr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_new(i64 4)", arr))
	rSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", rSlot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.emitNbTagPtr(arr, kmlTagDynArray), rSlot))
	e.define("__kml_collect_r", Symbol{Ptr: rSlot, Ty: TypeAny})

	push := ast.NewExpressionStatement(ast.NewCallExpression(
		ast.NewMemberExpression(ast.NewIdentifier("__kml_collect_r", pos), "push", pos),
		[]ast.Expression{ast.NewIdentifier("__kml_collect_x", pos)}, pos), pos)
	loop := ast.NewForOfStatement("const", "__kml_collect_x", ast.NewIdentifier("__kml_collect_v", pos),
		ast.NewBlockStatement([]ast.Statement{push}, pos), pos)
	condL, bodyL := e.freshLabel("collect.cond"), e.freshLabel("collect.body")
	incL, endL := e.freshLabel("collect.inc"), e.freshLabel("collect.end")
	e.pushScope()
	err := e.emitForOfAnyValue(loop, Value{Ref: "%p_v", Ty: TypeAny}, condL, bodyL, incL, endL)
	e.popScope()
	if err != nil {
		panic(fmt.Sprintf("__kml_any_iter_collect: %v", err))
	}
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, rSlot))
	e.emitTerminator(fmt.Sprintf("ret i64 %s", out))

	e.functions.WriteString("\ndefine i64 @__kml_any_iter_collect(i64 %p_v) {\nentry:\n")
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")
	restore()
}

// hasIteratorMember reports whether a static object or class instance has a
// [Symbol.iterator] member: iterating it runs the protocol at run time.
func (e *Emitter) hasIteratorMember(t Type) bool {
	if canon := e.canonicalizeClassTy(t); canon.IsClass {
		if info, ok := e.classes[canon.ClassName]; ok {
			_, has := info.MethodSigs[syncIteratorMethodName]
			return has
		}
		return false
	}
	if t.IsObject && !t.IsArray && !t.IsDynamic {
		_, _, ok := t.FieldIndex(syncIteratorMethodName)
		return ok
	}
	return false
}

// ensureAnyArrayLikeCollect defines @__kml_any_arraylike_collect(v):
// Array.from's array-like reading of a non-iterable value — its `length`
// (ToLength) elements `v[0]`, `v[1]`, … as a boxed `any[]`.
func (e *Emitter) ensureAnyArrayLikeCollect() {
	if e.usedAnyArrayLikeCollect {
		return
	}
	e.usedAnyArrayLikeCollect = true
	e.ensureDynArr()
	e.ensureMalloc()
	e.ensureSprintf()
	restore := e.beginDetachedFunc()
	pos := ast.Pos{}
	v := Value{Ref: "%p_v", Ty: TypeAny}
	arr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_new(i64 4)", arr))
	lenBox, err := e.emitDynAnyMemberGetNamed(v, e.internString("length"), "length", pos)
	if err != nil {
		panic(fmt.Sprintf("__kml_any_arraylike_collect: %v", err))
	}
	d := e.emitAnyToNum(lenBox)
	// ToLength: NaN and negatives are 0; the integer part otherwise.
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp ogt double %s, 0.0", ok, d))
	clamped := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, double %s, double 0.0", clamped, ok, d))
	n := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", n, clamped))
	iSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", iSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", iSlot))
	condL, bodyL, endL := e.freshLabel("arrlike.cond"), e.freshLabel("arrlike.body"), e.freshLabel("arrlike.end")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, iSlot))
	more := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, i, n))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, endL))
	e.emitLabel(bodyL)
	key := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", key))
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s)", key, e.internString("%lld"), i))
	elem, err := e.emitDynAnyMemberGet(v, key, pos)
	if err != nil {
		panic(fmt.Sprintf("__kml_any_arraylike_collect: %v", err))
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_push(ptr %s, i64 %s)", arr, elem.Ref))
	ni := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", ni, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ni, iSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(endL)
	e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitNbTagPtr(arr, kmlTagDynArray)))

	e.functions.WriteString("\ndefine i64 @__kml_any_arraylike_collect(i64 %p_v) {\nentry:\n")
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")
	restore()
}

// emitAnyArrayFrom is Array.from over a value held in `any` (or a static
// iterable, boxed): an array's elements, the iteration protocol's values,
// else the array-like reading — a fresh `any[]`.
func (e *Emitter) emitAnyArrayFrom(val Value) (Value, error) {
	boxed, err := e.emitBoxValue(val)
	if err != nil {
		return Value{}, err
	}
	e.ensureDynJSONC()
	if !e.declaredAnyArrView {
		e.declaredAnyArrView = true
		e.emitGlobal("declare ptr @__kml_anyarr_view(i64)")
		e.emitGlobal("declare void @__kml_anyarr_sync(i64, ptr)")
	}
	// A string walks its code points.
	boxed = e.anyStringAsCodePoints(boxed)
	src := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", src))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.emitHostIterValue(boxed.Ref), src))
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, src))
	direct := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_anyarr_view(i64 %s)", direct, cur))
	isArr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isArr, direct))
	viewSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", viewSlot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", direct, viewSlot))
	haveL, otherL := e.freshLabel("afrom.have"), e.freshLabel("afrom.other")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, haveL, otherL))
	e.emitLabel(otherL)
	iterable := Value{Ref: cur, Ty: TypeAny}
	method, _ := e.emitAnyMemberOrUndefined(iterable, "@@iterator")
	mtag, _ := e.emitUnboxTagPayload(method)
	isFn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isFn, mtag, kmlTagDynFunc))
	protoL, likeL := e.freshLabel("afrom.proto"), e.freshLabel("afrom.like")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFn, protoL, likeL))
	e.emitLabel(protoL)
	e.ensureAnyIterCollect()
	c1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_any_iter_collect(i64 %s)", c1, iterable.Ref))
	v1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_anyarr_view(i64 %s)", v1, c1))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", v1, viewSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", haveL))
	e.emitLabel(likeL)
	e.ensureAnyArrayLikeCollect()
	c2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_any_arraylike_collect(i64 %s)", c2, cur))
	v2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_anyarr_view(i64 %s)", v2, c2))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", v2, viewSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", haveL))
	e.emitLabel(haveL)
	view := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", view, viewSlot))
	data, n := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", data, view))
	lp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", lp, arrayHeaderTy, view))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", n, lp))
	copied := e.emitArrayCopy(data, n, TypeAny)
	r0, r1 := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, copied))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, n))
	return Value{Ref: r1, Ty: ArrayOf(TypeAny)}, nil
}

// emitEmptyAnyArray is a fresh, empty `any[]`.
func (e *Emitter) emitEmptyAnyArray() Value {
	e.ensureMalloc()
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8)", data))
	r0, r1 := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, data))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 0, 1", r1, r0))
	return Value{Ref: r1, Ty: ArrayOf(TypeAny)}
}

// emitUnboxAnyArray copies an `any[]` into a fresh array of elem, each
// element unboxed.
func (e *Emitter) emitUnboxAnyArray(arr Value, elem Type) (Value, error) {
	src, n := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", src, arr.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", n, arr.Ref))
	e.ensureMalloc()
	bytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytes, n, elem.Align()))
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", data, bytes))
	if err := e.emitSpreadUnboxLoop(src, n, elem, data); err != nil {
		return Value{}, err
	}
	r0, r1 := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, data))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, n))
	return Value{Ref: r1, Ty: ArrayOf(elem)}, nil
}

// errorOwnKeyOrder is the order Node's errors carry their own enumerable
// system fields: uvException's errno, code, syscall, path, dest; the host
// and port form's address and port; node:sqlite's errcode and errstr.
var errorOwnKeyOrder = []string{"errno", "code", "syscall", "address", "port", "path", "dest", "errcode", "errstr"}

// emitErrorOwnKeysHooks defines the hooks the object walkers (inspect's
// keys, JSON, Object.keys) use for an Error's own enumerable fields beyond
// its layout: the system fields set on it (errorOwnKeyOrder, each only when
// present), then the keys of its `extra` bag.
//
//	i64 @__kml_error_nextra(ptr o)
//	ptr @__kml_error_extra_key(ptr o, i64 i)
//	i64 @__kml_error_extra_get(ptr o, ptr key, ptr found)
func (e *Emitter) emitErrorOwnKeysHooks() {
	e.ensureDynObj()
	e.ensureStrcmp()
	sir := errorObjType.StructIR()
	restore := e.beginThunkEmit()
	// present(name) is an i1 register: whether field name is set on %o.
	present := func(name string) (string, Type, int) {
		idx, ty, _ := errorObjType.FieldIndex(name)
		gep, v, p := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%o, i32 0, i32 %d", gep, sir, idx))
		if ty.Float {
			e.emitInstr(fmt.Sprintf("%s = load double, ptr %s, align 8", v, gep))
			e.emitInstr(fmt.Sprintf("%s = fcmp une double %s, 0.0", p, v))
		} else {
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", v, gep))
			e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", p, v))
		}
		// errcode/errstr are node:sqlite's (an error without errno); a
		// system error's copies of them are internal.
		if name == "errcode" || name == "errstr" {
			eidx, _, _ := errorObjType.FieldIndex("errno")
			eg, ev, noErrno, both := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%o, i32 0, i32 %d", eg, sir, eidx))
			e.emitInstr(fmt.Sprintf("%s = load double, ptr %s, align 8", ev, eg))
			e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, 0.0", noErrno, ev))
			e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", both, p, noErrno))
			p = both
		}
		return p, ty, idx
	}
	extraBag := func() string {
		idx, _, _ := errorObjType.FieldIndex("extra")
		gep, bag := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%o, i32 0, i32 %d", gep, sir, idx))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bag, gep))
		return bag
	}

	// nextra: the count of present fields plus the bag's keys.
	n := "0"
	for _, name := range errorOwnKeyOrder {
		p, _, _ := present(name)
		z, s := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", z, p))
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", s, n, z))
		n = s
	}
	bag := extraBag()
	hasBag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasBag, bag))
	bagL, doneL := e.freshLabel("errx.bag"), e.freshLabel("errx.done")
	entryL := "entry" // no label precedes it: present() branches nowhere
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasBag, bagL, doneL))
	e.emitLabel(bagL)
	bn, total := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_count(ptr %s)", bn, bag))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", total, n, bn))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi i64 [ %s, %%%s ], [ %s, %%%s ]", res, n, entryL, total, bagL))
	e.emitTerminator(fmt.Sprintf("ret i64 %s", res))
	body := e.allocas.String() + e.body.String()
	restore()
	e.emitGlobal(fmt.Sprintf("define i64 @__kml_error_nextra(ptr %%o) {\nentry:\n%s}", body))

	// extra_key: the i-th present field, else the bag's (i - present)-th key.
	restore = e.beginThunkEmit()
	iSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", iSlot))
	e.emitInstr(fmt.Sprintf("store i64 %%i, ptr %s, align 8", iSlot))
	for _, name := range errorOwnKeyOrder {
		p, _, _ := present(name)
		cur, isZero, hit := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, iSlot))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isZero, cur))
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", hit, p, isZero))
		retL, nextL := e.freshLabel("errk.ret"), e.freshLabel("errk.next")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hit, retL, nextL))
		e.emitLabel(retL)
		e.emitTerminator(fmt.Sprintf("ret ptr %s", e.internString(name)))
		e.emitLabel(nextL)
		dec, nv := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", dec, p))
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", nv, cur, dec))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nv, iSlot))
	}
	bag = extraBag()
	noBag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", noBag, bag))
	nullL, keyL := e.freshLabel("errk.null"), e.freshLabel("errk.bag")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", noBag, nullL, keyL))
	e.emitLabel(nullL)
	e.emitTerminator("ret ptr null")
	e.emitLabel(keyL)
	bi, k := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", bi, iSlot))
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_key_at(ptr %s, i64 %s)", k, bag, bi))
	e.emitTerminator(fmt.Sprintf("ret ptr %s", k))
	body = e.allocas.String() + e.body.String()
	restore()
	e.emitGlobal(fmt.Sprintf("define ptr @__kml_error_extra_key(ptr %%o, i64 %%i) {\nentry:\n%s}", body))

	// extra_get: a present field's boxed value, else the bag's.
	restore = e.beginThunkEmit()
	for _, name := range errorOwnKeyOrder {
		c, eq := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %%key, ptr %s)", c, e.internString(name)))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", eq, c))
		isL, nextL := e.freshLabel("errg.is"), e.freshLabel("errg.next")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", eq, isL, nextL))
		e.emitLabel(isL)
		p, ty, idx := present(name)
		gotL := e.freshLabel("errg.got")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", p, gotL, nextL))
		e.emitLabel(gotL)
		gep, v := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%o, i32 0, i32 %d", gep, sir, idx))
		loadTy := ty
		loadTy.Nullable, loadTy.IsUndefined = false, false
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", v, loadTy.IR, gep))
		boxed, err := e.emitBoxValue(Value{Ref: v, Ty: loadTy})
		if err != nil {
			panic(err)
		}
		e.emitInstr("store i32 1, ptr %found, align 4")
		e.emitTerminator(fmt.Sprintf("ret i64 %s", boxed.Ref))
		e.emitLabel(nextL)
	}
	bag = extraBag()
	noBag = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", noBag, bag))
	missL, bagGetL := e.freshLabel("errg.miss"), e.freshLabel("errg.bag")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", noBag, missL, bagGetL))
	e.emitLabel(missL)
	e.emitInstr("store i32 0, ptr %found, align 4")
	e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
	e.emitLabel(bagGetL)
	bv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get(ptr %s, ptr %%key)", bv, bag))
	e.emitInstr("store i32 1, ptr %found, align 4")
	e.emitTerminator(fmt.Sprintf("ret i64 %s", bv))
	body = e.allocas.String() + e.body.String()
	restore()
	e.emitGlobal(fmt.Sprintf("define i64 @__kml_error_extra_get(ptr %%o, ptr %%key, ptr %%found) {\nentry:\n%s}", body))
}
