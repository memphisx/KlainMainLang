package llvm

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"KlainMainLang/ast"
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
	e.emitGlobal(fmt.Sprintf("%s = private constant { i64, [%d x i8] } { i64 %d, [%d x i8] c\"%s\" }, align 8", name, length, strHeaderWord(display), length, esc))
	ref := fmt.Sprintf("getelementptr inbounds ({ i64, [%d x i8] }, ptr %s, i32 0, i32 1)", length, name)
	e.classRefs[mangled] = ref
	// Its source text, for String(K), keyed by the reference's payload.
	if src, ok := e.classSrc[mangled]; ok && src.String() != "" {
		e.registerFnMeta(ref, e.classDisplayName(mangled), 0, fnKindPlain)
		e.fnMetaSource(ref, src)
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
	e.usedDynInstanceof = true
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dyn_instanceof(i64 %s, i64 %s)", res, lb.Ref, rb.Ref))
	if e.genProtos {
		// Against a generator function: its `prototype` on the chain.
		e.ensureGenProtoRuntime()
		e.ensureShapeRuntime()
		e.declareFn("__kml_gen_has_instance", "declare i32 @__kml_gen_has_instance(i64, i64)")
		gi := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_gen_has_instance(i64 %s, i64 %s)", gi, lb.Ref, rb.Ref))
		gb := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", gb, gi))
		either := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", either, res, gb))
		res = either
	}
	if len(e.classRecords) > 0 {
		// Against an evaluation's class: the instance's own (TDD-00242).
		e.ensureClassRec()
		lp, rp := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", lp, lb.Ref))
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", rp, rb.Ref))
		ok := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_classrec_is(ptr %s, ptr %s)", ok, e.emitIntToPtr(lp), e.emitIntToPtr(rp)))
		okb := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", okb, ok))
		both := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", both, res, okb))
		res = both
	}
	return Value{Ref: res, Ty: TypeBool}, nil
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

// ensureClassRefInspect declares the runtime's rendering of a constructor
// reference at a depth (dynjson.c).
func (e *Emitter) ensureClassRefInspect() {
	if e.declaredClassRefInspect {
		return
	}
	e.declaredClassRefInspect = true
	e.emitGlobal("declare ptr @__kml_classref_inspect_at(ptr, i64)")
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
	k, err := e.emitExpr(callee)
	if err != nil {
		return Value{}, err
	}
	kb, err := e.emitBoxValue(k)
	if err != nil {
		return Value{}, err
	}
	n := len(ex.Args)
	argv := "null"
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
			e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds [%d x i64], ptr %s, i64 0, i64 %d", slot, n, argv, i))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", b.Ref, slot))
		}
	}
	e.ensureNanBox()
	e.usedDynConstruct = true
	res := e.freshReg()
	if len(e.classRecords) > 0 {
		// A record's class reads its bindings from it (TDD-00242).
		e.ensureClassRec()
		pay := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pay, kb.Ref))
		pp := e.emitIntToPtr(pay)
		e.emitInstr(fmt.Sprintf("call void @__kml_classrec_enter(ptr %s)", pp))
	}
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dyn_construct(i64 %s, ptr %s, i64 %d, ptr %s)", res, kb.Ref, argv, n, e.internString(calleeText(callee)+" is not a constructor")))
	v := Value{Ref: res, Ty: TypeAny}
	if instTy.IsDynamic() {
		return v, nil
	}
	return e.convertChecked(v, instTy, ex.GetPos(), "constructed instance")
}

// ctorArity is the number of parameters class c's constructor takes, its
// own or the nearest inherited one's.
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
		} else if !strings.HasPrefix(m, "__kml_set_") {
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
	e.usedClassRefGet = true
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_classref_get(ptr %s, ptr %s)", r, e.emitIntToPtr(pay), keyRef))
	return r
}

// hostCtorArity is the most arguments builtin constructor h takes.
func hostCtorArity(h string) int {
	switch {
	case h == "Date":
		return 7
	case isErrorKindName(h), h == "AggregateError", h == "DOMException", h == "URL", h == "Request", h == "RegExp":
		return 2
	}
	return 1
}

// emitHostConstructArms constructs builtin h over argc of the boxed
// arguments (__kml_dyn_construct's argv), one arm per argument count; a
// count h does not take throws.
func (e *Emitter) emitHostConstructArms(h string) {
	n := hostCtorArity(h)
	var args []ast.Expression
	for i := 0; i < n; i++ {
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
		e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, slot))
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
			e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds i64, ptr %%argv, i64 %d", at, i))
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", ld, at))
			sym, _ := e.lookup(fmt.Sprintf("__kml_ha%d", i))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ld, sym.Ptr))
		}
		lowered, ok, err := sema.BuiltinNew(ast.NewNewExpression(h, args[:k], ast.Pos{}))
		if h == "Promise" && k == 1 {
			// The executor is called through its boxed value.
			anyT := &ast.TypeAnnotation{Name: "any", Source: "ts"}
			call := ast.NewCallExpression(args[0], []ast.Expression{ast.NewIdentifier("__kml_pr", ast.Pos{}), ast.NewIdentifier("__kml_pj", ast.Pos{})}, ast.Pos{})
			exec := ast.NewArrowFunction([]ast.Param{{Name: "__kml_pr", Type: anyT}, {Name: "__kml_pj", Type: anyT}}, nil, call, nil, ast.Pos{})
			np := ast.NewNewExpression("Promise", []ast.Expression{exec}, ast.Pos{})
			np.TypeArgs = []*ast.TypeAnnotation{anyT}
			lowered, ok, err = np, true, nil
		}
		if ok && err == nil {
			if v, err := e.emitExpr(lowered); err == nil && !e.blockDone {
				if b, berr := e.emitBoxValue(v); berr == nil {
					e.emitTerminator(fmt.Sprintf("ret i64 %s", b.Ref))
				}
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
		return e.inferExprType(lowered), true
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
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_constructor_of(ptr %s)", r, obj.Ref))
	return Value{Ref: r, Ty: TypeAny}
}

// emitAnyInstanceOfClass is `v instanceof C` for v held in any and C the
// class named cls.
func (e *Emitter) emitAnyInstanceOfClass(v Value, cls string) (Value, error) {
	name := fmt.Sprintf("__kml_iof_%d", e.dynFnCtr)
	e.dynFnCtr++
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v.Ref, slot))
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
