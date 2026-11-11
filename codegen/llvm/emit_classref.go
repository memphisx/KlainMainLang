package llvm

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/lib"
	"KlainMainLang/sema"
)

// emit_classref.go — a class as a value, and `instanceof` against one held
// in `any`.
//
// A class name in value position (`const C: any = Foo`, `assert.throws(fn,
// Foo)`) boxes as a constructor reference, tag kmlTagFuncRef, as the
// built-in Error constructors do. Its payload is a string constant of its
// own holding the class's name: `.name` reads it, and the pointer stays
// distinct from every other class's (two modules' `Foo`s). `x instanceof C`
// with `C` a value dispatches at run time (__kml_dyn_instanceof) on that
// pointer to the static test of the class it names.

// classRefPtr is the payload of class mangled's constructor reference.
func (e *Emitter) classRefPtr(mangled string) string {
	if e.classRefs == nil {
		e.classRefs = map[string]string{}
	}
	if ref, ok := e.classRefs[mangled]; ok {
		return ref
	}
	// Named after the class, so a library class's reference has the same
	// symbol in every program (TDD-00238).
	name := "@__kml_classref." + llvmSafeSymbol(mangled)
	display := e.classDisplayName(mangled)
	esc, length := escapeLLVM(display)
	e.emitGlobal(fmt.Sprintf("%s = private"+irGlobalAS+" constant { i64, [%d x i8] } { i64 %d, [%d x i8] c\"%s\" }, align 8", name, length, strHeaderWord(display), length, esc))
	ref := fmt.Sprintf("getelementptr inbounds ({ i64, [%d x i8] }, "+irPtr+" %s, i32 0, i32 1)", length, name)
	e.classRefs[mangled] = ref
	// Its source text, for String(K), keyed by the reference's payload.
	if src, ok := e.classSrc[mangled]; ok {
		e.registerClassSrc(ref, e.classDisplayName(mangled), src)
	}
	return ref
}

// emitClassRef boxes class mangled as a constructor reference.
func (e *Emitter) emitClassRef(mangled string) Value {
	return Value{Ref: e.emitNbTagPtr(e.classRefPtr(mangled), kmlTagFuncRef), Ty: TypeAny}
}

// instanceofNeedsRuntime reports an `instanceof` whose right-hand side is a
// value rather than a class name: a binding, a member, a call.
func (e *Emitter) instanceofNeedsRuntime(right ast.Expression) bool {
	id, ok := right.(*ast.Identifier)
	if !ok {
		return true
	}
	_, found := e.lookup(id.Name)
	return found || e.generators[id.Name] != nil
}

// emitDynInstanceOf is `left instanceof right` with right a value: the
// run-time dispatch on the constructor it holds.
func (e *Emitter) emitDynInstanceOf(ex *ast.BinaryExpression) (Value, error) {
	l, err := e.emitExpr(ex.Left)
	if err != nil {
		return Value{}, err
	}
	lb, err := e.emitBoxValue(l)
	if err != nil {
		return Value{}, err
	}
	r, err := e.emitExpr(ex.Right)
	if err != nil {
		return Value{}, err
	}
	rb, err := e.emitBoxValue(r)
	if err != nil {
		return Value{}, err
	}
	e.ensureNanBox()
	// InstanceofOperator: the target's own @@hasInstance first, called on it
	// with the value, its result made a boolean.
	pos := ex.GetPos()
	target := e.bindValue(Value{Ref: rb.Ref, Ty: TypeAny}, pos)
	// A target that is no object: TypeError, before its @@hasInstance.
	tt := ast.NewUnaryExpression("typeof", true, target, pos)
	objTest, err := e.emitExpr(ast.NewBinaryExpression("||",
		ast.NewBinaryExpression("===", tt, ast.NewStringLiteral("function", pos), pos),
		ast.NewBinaryExpression("&&",
			ast.NewBinaryExpression("===", tt, ast.NewStringLiteral("object", pos), pos),
			ast.NewBinaryExpression("!==", target, ast.NewNullLiteral(false, pos), pos), pos), pos))
	if err != nil {
		return Value{}, err
	}
	objTest = e.convert(objTest, TypeBool)
	objL, primL := e.freshLabel("instof.obj"), e.freshLabel("instof.prim")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", objTest.Ref, objL, primL))
	e.emitLabel(primL)
	e.emitThrowTypeError("Right-hand side of 'instanceof' is not an object")
	e.emitLabel(objL)
	h, err := e.emitExpr(ast.NewIndexExpression(target, ast.NewMemberExpression(ast.NewIdentifier("Symbol", pos), "hasInstance", pos), pos))
	if err != nil {
		return Value{}, err
	}
	tag, _ := e.emitUnboxTagPayload(Value{Ref: h.Ref, Ty: TypeAny})
	isNull, isUndef, absent := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", absent, isNull, isUndef))
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", slot))
	callL, ordL, joinL := e.freshLabel("instof.hasinst"), e.freshLabel("instof.ordinary"), e.freshLabel("instof.join")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", absent, ordL, callL))
	e.emitLabel(callL)
	handler := e.bindValue(Value{Ref: h.Ref, Ty: TypeAny}, pos)
	value := e.bindValue(Value{Ref: lb.Ref, Ty: TypeAny}, pos)
	hv, err := e.emitExpr(ast.NewUnaryExpression("!", true, ast.NewUnaryExpression("!", true,
		ast.NewCallExpression(ast.NewMemberExpression(handler, "call", pos), []ast.Expression{target, value}, pos), pos), pos))
	if err != nil {
		return Value{}, err
	}
	hv = e.convert(hv, TypeBool)
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", hv.Ref, slot))
	e.emitTerminator("br label %" + joinL)
	e.emitLabel(ordL)
	// Without @@hasInstance the target must be callable (a TypeError).
	fnTest, err := e.emitExpr(ast.NewBinaryExpression("===", ast.NewUnaryExpression("typeof", true, target, pos), ast.NewStringLiteral("function", pos), pos))
	if err != nil {
		return Value{}, err
	}
	fnTest = e.convert(fnTest, TypeBool)
	okL, badL := e.freshLabel("instof.callable"), e.freshLabel("instof.notcallable")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", fnTest.Ref, okL, badL))
	e.emitLabel(badL)
	e.emitThrowTypeError("Right-hand side of 'instanceof' is not callable")
	e.emitLabel(okL)
	res := e.emitOrdinaryHasInstance(lb.Ref, rb.Ref)
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", res, slot))
	e.emitTerminator("br label %" + joinL)
	e.emitLabel(joinL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", out, slot))
	return Value{Ref: out, Ty: TypeBool}, nil
}

// emitOrdinaryHasInstance is OrdinaryHasInstance(c, v) for boxed values v
// and c: the instanceof test without c's own @@hasInstance, as an i1.
func (e *Emitter) emitOrdinaryHasInstance(v, c string) string {
	e.usedDynInstanceof = true
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dyn_instanceof(i64 %s, i64 %s)", res, v, c))
	{
		// Against a generator or constructor function: its `prototype` on
		// the chain.
		e.ensureFnMeta()
		e.ensureShapeRuntime()
		e.declareFn("__kml_gen_has_instance", "declare i32 @__kml_gen_has_instance(i64, i64)")
		gi := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_gen_has_instance(i64 %s, i64 %s)", gi, v, c))
		gb := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", gb, gi))
		either := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", either, res, gb))
		res = either
	}
	if len(e.classRecords) > 0 {
		// Against an evaluation's class: the instance's own (TDD-00242).
		lp, rp := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", lp, v))
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", rp, c))
		ok := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_classrec_is("+irPtr+" %s, "+irPtr+" %s)", ok, e.emitIntToPtr(lp), e.emitIntToPtr(rp)))
		okb := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", okb, ok))
		both := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", both, res, okb))
		res = both
	}
	if len(e.valueBases) > 0 {
		// An instance of a class extending a function value: the value's
		// `prototype` on the chain its class's prototype starts.
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", slot))
		e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", res, slot))
		pos := ast.Pos{}
		if err := e.emitValueBaseHasInstance(v, e.bindValue(Value{Ref: c, Ty: TypeAny}, pos), slot, pos); err == nil {
			res = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", res, slot))
		}
	}
	return res
}

// ensureCtorKind marks __kml_ctor_kind (lib/native.d.ts ctorKind) used.
func (e *Emitter) ensureCtorKind() {
	e.ensureNanBox()
	e.usedCtorKind = true
}

// ctorRefTargets are the constructors a reference can name: every Error
// kind, the builtin constructors `instanceof` decides (`Object`,
// `Function`, `Map`, …), and every class boxed as a value, as (payload,
// name) pairs.
func (e *Emitter) ctorRefTargets() (ptrs, names []string) {
	var kinds []string
	for k := range errorKindIDs {
		kinds = append(kinds, k)
	}
	for k := range builtinInstanceofTypes {
		if _, user := e.classes[k]; !user {
			kinds = append(kinds, k)
		}
	}
	for _, k := range []string{"Object", "Function", "Symbol"} {
		if _, user := e.classes[k]; !user {
			kinds = append(kinds, k)
		}
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		ptrs = append(ptrs, e.internString(k))
		names = append(names, k)
	}
	var classes []string
	for c := range e.classRefs {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	for _, c := range classes {
		ptrs = append(ptrs, e.classRefs[c])
		names = append(names, c)
	}
	return ptrs, names
}

// classShown is util.inspect's rendering of class c as a value.
func classShown(e *Emitter, c string) string {
	name := e.classDisplayName(c)
	if name == "" {
		name = "(anonymous)"
	}
	shown := "[class " + name
	if base := e.classes[c].BaseClass; base != "" {
		shown += " extends " + e.classDisplayName(base)
	}
	return shown + "]"
}

// dynNewInstanceType reports `new K(…)` with K a value rather than a class
// name (a `typeof C` binding, a parameter, an `any`), and the type its
// instance takes: the checker's class when it names one, else `any`.
func (e *Emitter) dynNewInstanceType(ex *ast.NewExpression) (Type, bool) {
	if ex.Callee != nil {
		return e.checkerInstanceType(ex), true
	}
	if ex.Qualified {
		// `new obj.K()` with obj a value, not a namespace: a binding, or a
		// function carrying K as a property (`new assert.Assert()`).
		_, found := e.lookup(ex.Qualifier)
		if !found {
			_, found = e.funcs[ex.Qualifier]
		}
		if !found || ex.Qualifier == "" {
			return Type{}, false
		}
		return e.checkerInstanceType(ex), true
	}
	if _, isClass := e.classes[ex.ClassName]; isClass {
		return Type{}, false
	}
	if _, generic := e.genericClasses[ex.ClassName]; generic {
		return Type{}, false
	}
	if _, found := e.lookup(ex.ClassName); !found {
		return Type{}, false
	}
	return e.checkerInstanceType(ex), true
}

// checkerInstanceType is the type the checker gives `new K(…)`: a class's
// instance, else `any`.
func (e *Emitter) checkerInstanceType(ex *ast.NewExpression) Type {
	if c := e.front(); c != nil {
		if t := c.TypeOf(ex); t != nil && t.Symbol != nil {
			// Through the declaration codegen registered (a local class's
			// name is made unique), else by name.
			if ct, ok := e.classRepr(t.Symbol); ok && ct.IsObject() && ct.IsClass() {
				return ct
			}
			if info, ok := e.classes[t.Symbol.Name]; ok && info.Ty.IsObject() {
				return info.Ty
			}
		}
	}
	return TypeAny
}

// unresolvedNewTarget reports a `new X(…)` whose X, under -compat=js, the
// binder looked up and found no declaration for.
func (e *Emitter) unresolvedNewTarget(ex *ast.NewExpression) bool {
	c := e.front()
	if c == nil {
		return false
	}
	if _, ns := namespaceValueExport(ex.ClassName); ns {
		return false
	}
	b := c.Binding()
	if b.LookupScope(ex) == nil {
		return false
	}
	t := b.NewTarget(ex)
	if e.compatJS() {
		return t == nil
	}
	// TypeScript: a global tsc's library declares as a value that this
	// compiler's library gives no value (`new FormData()`, declared only
	// as an interface), as unresolvedReference reads one.
	return (t == nil || t.Flags&binder.Value == 0) && lib.GlobalValue(ex.ClassName)
}

// unresolvedQualifier is the root of `new Q.C()` (`new Q.a.C()`) when no
// scope or builtin binds Q, in JavaScript; nil otherwise.
func (e *Emitter) unresolvedQualifier(ex *ast.NewExpression) *ast.Identifier {
	c := e.front()
	if c == nil || !e.compatJS() || !ex.Qualified || ex.Callee != nil {
		return nil
	}
	root := qualifierRoot(ex)
	if _, ns := namespaceValueExport(root); ns {
		return nil
	}
	if _, ok := e.lookup(root); ok {
		return nil
	}
	b := c.Binding()
	sc := b.LookupScope(ex)
	if sc == nil {
		return nil
	}
	for s := sc; s != nil; s = s.Parent {
		if s.Symbols.Get(root) != nil {
			return nil
		}
	}
	if b.Globals != nil && b.Globals.Symbols.Get(root) != nil {
		return nil
	}
	return ast.NewIdentifier(root, ex.GetPos())
}

// qualifierRoot is the first name of a qualified `new`'s path.
func qualifierRoot(ex *ast.NewExpression) string {
	if len(ex.QualifierPath) > 0 {
		return ex.QualifierPath[0]
	}
	return ex.Qualifier
}

// emitDynNew is `new K(args)` with K a value: the arguments boxed into an
// array, __kml_dyn_construct dispatching on the constructor K holds, and the
// boxed instance converted to the instance type.
func (e *Emitter) emitDynNew(ex *ast.NewExpression, instTy Type) (Value, error) {
	callee := ex.Callee
	switch {
	case callee != nil:
	case ex.Qualified:
		callee = ast.NewMemberExpression(ast.NewIdentifier(ex.Qualifier, ex.GetPos()), ex.ClassName, ex.GetPos())
	default:
		callee = ast.NewIdentifier(ex.ClassName, ex.GetPos())
	}
	var k Value
	var err error
	if q := e.unresolvedQualifier(ex); q != nil {
		// `new Nope.C()` in JavaScript: the qualifier's global property,
		// else its ReferenceError, before any argument runs.
		qv, qerr := e.emitGlobalPropertyRead(q)
		if qerr != nil {
			return Value{}, qerr
		}
		segs := append([]string{}, ex.QualifierPath...)
		if len(segs) == 0 {
			segs = []string{ex.Qualifier}
		}
		k = qv
		for _, seg := range append(segs[1:], ex.ClassName) {
			if k, err = e.emitAnyMemberOrUndefined(k, seg); err != nil {
				break
			}
		}
	} else if id, synth := callee.(*ast.Identifier); synth && ex.Callee == nil && !ex.Qualified && e.unresolvedNewTarget(ex) {
		// A name no scope declares (`new Nope()` in JavaScript): the global
		// property, else its ReferenceError, before any argument runs.
		k, err = e.emitGlobalPropertyRead(id)
	} else {
		k, err = e.emitExpr(callee)
	}
	if err != nil {
		return Value{}, err
	}
	kb, err := e.emitBoxValue(k)
	if err != nil {
		return Value{}, err
	}
	n := len(ex.Args)
	argv := "null"
	argc := fmt.Sprint(n)
	spread := false
	for _, a := range ex.Args {
		if _, ok := a.(*ast.SpreadElement); ok {
			spread = true
		}
	}
	if spread {
		// A spread argument: the arguments as one array of boxes.
		data, length, err := e.emitSpreadArrayLitData(ast.NewArrayLiteral(ex.Args, ex.GetPos()), TypeAny)
		if err != nil {
			return Value{}, err
		}
		argv, argc, n = data, length, 0
	}
	if n > 0 {
		argv = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca [%d x i64], align 8", argv, n))
		for i, a := range ex.Args {
			v, err := e.emitExpr(a)
			if err != nil {
				return Value{}, err
			}
			b, err := e.emitBoxValue(v)
			if err != nil {
				return Value{}, err
			}
			slot := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds [%d x i64], "+irPtr+" %s, i64 0, i64 %d", slot, n, argv, i))
			e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", b.Ref, slot))
		}
	}
	e.ensureNanBox()
	e.usedDynConstruct = true
	res := e.freshReg()
	if len(e.classRecords) > 0 {
		// A record's class reads its bindings from it (TDD-00242).
		pay := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pay, kb.Ref))
		pp := e.emitIntToPtr(pay)
		e.emitInstr(fmt.Sprintf("call void @__kml_classrec_enter("+irPtr+" %s)", pp))
	}
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dyn_construct(i64 %s, "+irPtr+" %s, i64 %s, "+irPtr+" %s)", res, kb.Ref, argv, argc, e.internString(calleeText(callee)+" is not a constructor")))
	v := Value{Ref: res, Ty: TypeAny}
	if instTy.IsDynamic() {
		return v, nil
	}
	return e.convertChecked(v, instTy, ex.GetPos(), "constructed instance")
}

// ctorArity is the number of parameters class c's constructor takes, its
// own or the nearest inherited one's.
// ctorParamList is the parameter list of the constructor class c runs, its
// own or the nearest base's; nil for none.
func (e *Emitter) ctorParamList(c string) []ast.Param {
	for i := 0; c != "" && i < 64; i++ {
		info, ok := e.classes[c]
		if !ok {
			return nil
		}
		if info.Constructor != nil {
			return info.Constructor.Params
		}
		c = info.BaseClass
	}
	return nil
}

func (e *Emitter) ctorArity(c string) int {
	for i := 0; c != "" && i < 64; i++ {
		info, ok := e.classes[c]
		if !ok {
			return 0
		}
		if info.Constructor != nil {
			return len(info.Constructor.Params)
		}
		c = info.BaseClass
	}
	return 0
}

// classStaticKeys are the static members a reference to class c reads: its
// own and inherited static fields, then its static methods and accessors.
func (e *Emitter) classStaticKeys(c string) []string {
	seen := map[string]bool{}
	var keys []string
	add := func(k string) {
		if !seen[k] && !strings.HasPrefix(k, "#") {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for b, i := c, 0; b != "" && i < 64; i++ {
		info, ok := e.classes[b]
		if !ok {
			break
		}
		for _, f := range info.OwnStaticFieldOrder {
			add(f)
		}
		b = info.BaseClass
	}
	var methods []string
	for m := range e.classes[c].StaticMethodSigs {
		if rest, ok := strings.CutPrefix(m, "__kml_get_"); ok {
			methods = append(methods, rest)
		} else if rest, ok := strings.CutPrefix(m, "__kml_set_"); ok {
			methods = append(methods, rest) // (a setter-only accessor's key)
		} else {
			methods = append(methods, m)
		}
	}
	sort.Strings(methods)
	for _, m := range methods {
		add(m)
	}
	return keys
}

func seenStatic(keys []string, k string) bool {
	for _, x := range keys {
		if x == k {
			return true
		}
	}
	return false
}

// ctorLength is class c's `length`: its own constructor's parameters
// before the first one with a default or a rest; a class without one has
// the implicit `constructor(...args)`, length 0.
func (e *Emitter) ctorLength(c string) int {
	info, ok := e.classes[c]
	if !ok || info.Constructor == nil || info.ImplicitCtor {
		return 0
	}
	n := 0
	for _, p := range info.Constructor.Params {
		if p.Default != nil || p.Rest {
			break
		}
		n++
	}
	return n
}

// emitClassRefGet reads property keyRef of the constructor reference whose
// payload is pay.
func (e *Emitter) emitClassRefGet(pay, keyRef string) string {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_classref_get("+irPtr+" %s, "+irPtr+" %s)", r, e.emitIntToPtr(pay), keyRef))
	return r
}

// hostCtorArity is the most arguments builtin constructor h takes.
func hostCtorArity(h string) int {
	switch {
	case h == "Date":
		return 7
	case isErrorKindName(h), h == "AggregateError", h == "DOMException", h == "URL", h == "Request", h == "RegExp", h == "ArrayBuffer", h == "SharedArrayBuffer":
		return 2
	case h == "DataView", strings.HasSuffix(h, "Array") && h != "Array":
		return 3 // a view's (buffer, byteOffset, length)
	case h == "ReadableStream", h == "WritableStream":
		return 2 // (underlying, strategy)
	case h == "TransformStream":
		return 3 // (transformer, writableStrategy, readableStrategy)
	}
	return 1
}

// emitHostConstructArms constructs builtin h over argc of the boxed
// arguments (__kml_dyn_construct's argv), one arm per argument count; a
// count h does not take throws.
func (e *Emitter) emitHostConstructArms(h string) {
	e.emitConstructArgcArms(hostCtorArity(h), func(args []ast.Expression) (Value, error) {
		k := len(args)
		lowered, ok, err := sema.BuiltinNew(ast.NewNewExpression(h, args, ast.Pos{}))
		if h == "Promise" && k == 1 {
			// The executor is called through its boxed value.
			anyT := ast.AnyAnnotation("ts")
			call := ast.NewCallExpression(args[0], []ast.Expression{ast.NewIdentifier("__kml_pr", ast.Pos{}), ast.NewIdentifier("__kml_pj", ast.Pos{})}, ast.Pos{})
			exec := ast.NewArrowFunction([]ast.Param{{Name: "__kml_pr", Type: anyT}, {Name: "__kml_pj", Type: anyT}}, nil, call, nil, ast.Pos{})
			np := ast.NewNewExpression("Promise", []ast.Expression{exec}, ast.Pos{})
			np.TypeArgs = []ast.TypeNode{anyT}
			lowered, ok, err = np, true, nil
		}
		if typedArrayNames[h] && k == 0 {
			// new Uint8Array() is a view of length 0.
			lowered, ok, err = sema.BuiltinNew(ast.NewNewExpression(h, []ast.Expression{ast.NewNumberLiteral("0", ast.Pos{})}, ast.Pos{}))
		}
		if _, lock := streamLockCtors[h]; lock {
			if k == 0 {
				args = []ast.Expression{ast.NewNullLiteral(true, ast.Pos{})}
			}
			lowered, ok, err = ast.NewNewExpression(h, args, ast.Pos{}), true, nil
		}
		if h == "Object" && !ok {
			// new Object(v) is Object(v) (ES 20.1.1.1, NewTarget Object).
			lowered, ok, err = synthCall("Object", ast.Pos{}, args...), true, nil
		}
		if !ok || err != nil {
			return Value{}, fmt.Errorf("no form")
		}
		return e.emitExpr(lowered)
	}, h)
}

// emitConstructArgcArms constructs (through build) over argc of the boxed
// arguments (__kml_dyn_construct's argv), up to n of them, one arm per
// count; a count build does not take throws.
func (e *Emitter) emitConstructArgcArms(n int, build func([]ast.Expression) (Value, error), h string) {
	var args []ast.Expression
	for i := 0; i < n; i++ {
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
		e.emitInstr(fmt.Sprintf("store i64 %d, "+irPtr+" %s, align 8", nbUndefined, slot))
		name := fmt.Sprintf("__kml_ha%d", i)
		e.define(name, Symbol{Ptr: slot, Ty: TypeAny})
		args = append(args, ast.NewIdentifier(name, ast.Pos{}))
	}
	for k := 0; k <= n; k++ {
		arm, next := e.freshLabel("dynnew.argc"), e.freshLabel("dynnew.argcnext")
		eq := e.freshReg()
		cmp := "eq"
		if k == n {
			cmp = "sge"
		}
		e.emitInstr(fmt.Sprintf("%s = icmp %s i64 %%argc, %d", eq, cmp, k))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", eq, arm, next))
		e.emitLabel(arm)
		for i := 0; i < k; i++ {
			at, ld := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds i64, "+irPtr+" %%argv, i64 %d", at, i))
			e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", ld, at))
			sym, _ := e.lookup(fmt.Sprintf("__kml_ha%d", i))
			e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", ld, sym.Ptr))
		}
		if v, err := build(args[:k]); err == nil && !e.blockDone {
			if b, berr := e.emitBoxValue(v); berr == nil {
				e.emitTerminator(fmt.Sprintf("ret i64 %s", b.Ref))
			}
		}
		if !e.blockDone {
			e.emitThrowTypeError(fmt.Sprintf("new %s with %d argument(s) through a value is not supported", h, k))
		}
		e.emitLabel(next)
	}
	e.emitTerminator("unreachable")
}

// constructedType is the instance type `new` builds through constructor
// expression x: a class's instance, or a builtin's (`Set`), found by
// following constants bound to it.
func (e *Emitter) constructedType(x ast.Expression) (Type, bool) {
	id, ok := x.(*ast.Identifier)
	if !ok {
		return Type{}, false
	}
	name := id.Name
	if target, ok := e.identClassAlias(id); ok {
		name = target
	}
	if info, ok := e.classes[name]; ok {
		return info.Ty, true
	}
	if _, found := e.lookup(name); found {
		return Type{}, false
	}
	if lowered, ok, err := sema.BuiltinNew(ast.NewNewExpression(name, nil, id.GetPos())); ok && err == nil {
		return e.typeOfExpr(lowered), true
	}
	return Type{}, false
}

// calleeText is a constructor expression as an error message names it.
func calleeText(x ast.Expression) string {
	switch x := x.(type) {
	case *ast.Identifier:
		return demangleModuleName(x.Name)
	case *ast.AsExpression: // type syntax is erased
		return calleeText(x.Expr)
	case *ast.NonNullExpression:
		return calleeText(x.Arg)
	case *ast.ThisExpression:
		return "this"
	case *ast.MemberExpression:
		return calleeText(x.Object) + "." + x.Property
	case *ast.IndexExpression:
		return calleeText(x.Object) + "[" + calleeText(x.Index) + "]"
	case *ast.NumberLiteral:
		return x.Value
	case *ast.StringLiteral:
		return strconv.Quote(x.Value)
	}
	return "expression"
}

// noteHostRef records builtin constructor name boxed as a value: `new`
// through a reference to it dispatches to its construction.
func (e *Emitter) noteHostRef(name string) {
	if e.hostRefs == nil {
		e.hostRefs = map[string]bool{}
	}
	e.hostRefs[name] = true
}

// emitDynConstructOnce defines __kml_dyn_construct once, when used.
func (e *Emitter) emitDynConstructOnce() {
	if e.usedDynConstruct && !e.emittedDynConstruct {
		e.emittedDynConstruct = true
		e.emitDynConstructDispatch()
	}
}

// emitConstructorOf is `obj.constructor` for a class instance: the class it
// was constructed as, a run-time question for a subclass's instance.
func (e *Emitter) emitConstructorOf(obj Value) Value {
	e.usedConstructorOf = true
	e.ensureNanBox()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_constructor_of("+irPtr+" %s)", r, obj.Ref))
	return Value{Ref: r, Ty: TypeAny}
}

// emitAnyInstanceOfClass is `v instanceof C` for v held in any and C the
// class named cls.
func (e *Emitter) emitAnyInstanceOfClass(v Value, cls string) (Value, error) {
	name := fmt.Sprintf("__kml_iof_%d", e.dynFnCtr)
	e.dynFnCtr++
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", v.Ref, slot))
	e.define(name, Symbol{Ptr: slot, Ty: TypeAny})
	// One the class's constructor built, with its layout: not an object
	// whose chain merely holds the prototype (`Object.create(C.prototype)`).
	saved := e.brandOnly
	e.brandOnly = true
	defer func() { e.brandOnly = saved }()
	return e.emitInstanceOf(ast.NewBinaryExpression("instanceof", ast.NewIdentifier(name, ast.Pos{}), ast.NewIdentifier(cls, ast.Pos{}), ast.Pos{}))
}

// isClassName reports x naming a class itself (its statics resolve
// statically), not a binding holding a constructor reference.
func (e *Emitter) isClassName(x ast.Expression) bool {
	id, ok := x.(*ast.Identifier)
	if !ok {
		return false
	}
	if _, bound := e.lookup(id.Name); bound {
		return false
	}
	_, isClass := e.classes[id.Name]
	_, generic := e.genericClasses[id.Name]
	return isClass || generic
}

// erasedGenericClass is generic class decl instantiated with every type
// parameter `any`: the one class JavaScript has, as a value.
func (e *Emitter) erasedGenericClass(decl *ast.ClassDeclaration) (string, error) {
	subs := make(map[string]Type, len(decl.TypeParams))
	for _, tp := range decl.TypeParams {
		subs[tp] = TypeAny
	}
	return e.instantiateGenericClass(decl, subs)
}
