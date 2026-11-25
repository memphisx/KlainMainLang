package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/checker"
)

// emit_anyarg.go — an `any` argument at a typed parameter. JavaScript
// passes a value as it is: `f(5 as any)` hands a `(s: string)` parameter
// the number 5, which its `typeof s` sees. A direct call whose argument is
// an `any` held for a parameter of a fixed primitive kind checks the box's
// kind at run time: when every such box fits, the call takes the typed
// entry; otherwise it takes the function's any-argument entry, its body
// again with those parameters bound as boxes (the dynamic entry's widening,
// emit_dynentry.go), emitted on demand.

// anyArgScalarFits reports a primitive type whose box kind emitBoxFits
// checks.
func anyArgScalarFits(t Type) bool {
	if t.IsObject() || t.IsArray() || t.IsFunc() || t.IsMap() || t.IsSet() || t.IsDynamic() || t.IsTuple() {
		return false
	}
	return t.IR == "i1" || isStringTy(t) || t.Float || t.IsInteger()
}

// anyArgParamFits reports a type whose box kind emitAnyArgFits checks: a
// primitive, an array or a typed array.
func anyArgParamFits(t Type) bool {
	if anyArgScalarFits(t) {
		return true
	}
	if t.IsDynamic() || t.IsTuple() || t.IsFlatArray() || !t.IsArray() || t.ElemType == nil {
		return false
	}
	return !t.IsTypedArray() || typedArrayConstructorName(t) != ""
}

// emitAnyArgFits is whether the box word holds a value t holds as it is: a
// primitive of t's kind; for an array or a typed array, a static array box
// of t's class (its typed byte) and element kind. A dynamic array, which
// converting would copy, never fits, nor does a box whose element kind
// cannot be told apart (BigInt64Array from BigUint64Array).
func (e *Emitter) emitAnyArgFits(word string, t Type) string {
	if c, ok := e.emitBoxFits(word, t); ok {
		return c
	}
	kind, typed := anyArrayBoxBytes(t)
	if kind < 0 && typed != anyArrayPlain {
		return "false"
	}
	kinds := []int{kind}
	if kind < 0 {
		kinds = nil // any element kind the box can describe
	}
	if typed == anyArrayPlain && e.hasArrayClass() {
		// An instance of a class extending Array is the array its body
		// holds, as a static call passes it.
		word = e.emitArrayClassReceiver(Value{Ref: word, Ty: TypeAny}, "").Ref
	}
	return e.emitBoxIsArrayOf(Value{Ref: word, Ty: TypeAny}, []int{typed}, kinds)
}

// emitBoxIsArrayOf is an i1 that v is a non-null static array box whose
// typed byte is one of typed and whose element kind is one of kinds (any,
// when kinds is nil).
func (e *Emitter) emitBoxIsArrayOf(v Value, typed, kinds []int) string {
	tag, payload := e.emitUnboxTagPayload(v)
	isArr, nonNull, both := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isArr, tag, kmlTagArray))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", nonNull, payload))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", both, isArr, nonNull))
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", slot))
	e.emitInstr(fmt.Sprintf("store i1 false, "+irPtr+" %s, align 1", slot))
	readL, doneL := e.freshLabel("boxarr.read"), e.freshLabel("boxarr.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", both, readL, doneL))
	e.emitLabel(readL)
	box := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, box, payload))
	anyOf := func(field int, want []int) string {
		gp, b := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gp, anyArrayBoxTy, box, field))
		e.emitInstr(fmt.Sprintf("%s = load i8, "+irPtr+" %s, align 1", b, gp))
		r := "false"
		for _, w := range want {
			c, o := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", c, b, w))
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", o, r, c))
			r = o
		}
		return r
	}
	ok := anyOf(2, typed)
	if kinds != nil {
		k, both2 := anyOf(1, kinds), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", both2, ok, k))
		ok = both2
	}
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", ok, slot))
	e.emitTerminator("br label %" + doneL)
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", r, slot))
	return r
}

// anyArgOrderSafe reports an argument evaluating later than its position
// cannot tell: a literal or a plain name.
func anyArgOrderSafe(x ast.Expression) bool {
	switch x.(type) {
	case *ast.Identifier, *ast.NumberLiteral, *ast.StringLiteral, *ast.BooleanLiteral, *ast.NullLiteral:
		return true
	}
	return false
}

// anyArgParamAt is the type sig gives argument i (a rest argument its
// element type).
func anyArgParamAt(sig FuncSig, i int) (Type, bool) {
	regular := len(sig.ParamTypes)
	if sig.HasRest {
		regular--
	}
	if i < regular {
		return sig.ParamTypes[i], true
	}
	if sig.HasRest && sig.ParamTypes[regular].ElemType != nil {
		return *sig.ParamTypes[regular].ElemType, true
	}
	return Type{}, false
}

// anyArgMarks reports the arguments of a call of the top-level function
// name that the any-argument check covers: an `any` at a parameter of a
// primitive kind. ok is false when the call takes no check.
func (e *Emitter) anyArgMarks(name string, sig FuncSig, args []ast.Expression) (marked []bool, ok bool) {
	if e.anyArgTyped || sig.This || sig.IsAsync || sig.MaySuspend {
		return nil, false
	}
	if rt := sig.RetType; rt.IsArray() || rt.IsTuple() {
		return nil, false
	}
	decl, found := e.topFuncDecls[name]
	if !found || decl.Body == nil || decl.IsGenerator || decl.IsAsync {
		return nil, false
	}
	marked = make([]bool, len(args))
	last := -1
	for i, a := range args {
		if _, spread := a.(*ast.SpreadElement); spread {
			return nil, false
		}
		pt, has := anyArgParamAt(sig, i)
		if has && e.classAtObjectParam(a, pt) {
			// A class itself at an object-typed parameter: the parameter
			// holds the class's box, whose statics its reads and writes
			// reach (a layout's copy would take none of them).
			marked[i] = true
			last = i
			continue
		}
		if has && e.anyAtClassParam(pt) && isUnconstrainedDynamic(e.typeOfExpr(a)) {
			// An any at a class-typed parameter: a box that is no instance
			// of the class (a plain object) is read as itself.
			marked[i] = true
			last = i
			continue
		}
		if !has || !anyArgParamFits(pt) {
			continue
		}
		if at := e.typeOfExpr(a); !isUnconstrainedDynamic(at) && !(anyElemArray(at) && !anyElemArray(pt)) {
			continue
		}
		if nl, isNL := a.(*ast.NullLiteral); isNL && nl.IsUndefined {
			continue
		}
		marked[i] = true
		last = i
	}
	if last < 0 {
		return nil, false
	}
	for i := 0; i < last; i++ {
		if !marked[i] && !anyArgOrderSafe(args[i]) {
			return nil, false
		}
	}
	return marked, true
}

// anyArgReturnsBox reports a call by name whose any-argument check makes
// its result a box: the callee returns a primitive, which its body may
// return from a parameter that holds another kind (`id(5 as any)` for
// `id(s: string): string` returns the number). Inference and emission
// (identifierCallRule) both ask it.
func (e *Emitter) anyArgReturnsBox(ex *ast.CallExpression) bool {
	id, ok := ex.Callee.(*ast.Identifier)
	if !ok || e.jsProtoCtor[id.Name] {
		return false
	}
	if _, gen := e.lookupGenerator(id.Name); gen {
		return false
	}
	mangled, sig, found := e.resolveFuncRef(id.Name)
	if !found || !anyArgScalarFits(sig.RetType) {
		return false
	}
	_, ok = e.anyArgMarks(mangled, sig, ex.Args)
	return ok
}

// emitAnyArgDispatch emits the call through the any-argument check when an
// argument needs one; ok is false otherwise.
func (e *Emitter) emitAnyArgDispatch(name string, sig FuncSig, args []ast.Expression, pos ast.Pos, keepTupleAgg bool) (Value, bool, error) {
	boxRet := e.anyArgBoxRet
	e.anyArgBoxRet = false
	if keepTupleAgg {
		return Value{}, false, nil
	}
	marked, ok := e.anyArgMarks(name, sig, args)
	if !ok {
		return Value{}, false, nil
	}
	decl := e.topFuncDecls[name]
	regular := len(sig.ParamTypes)
	if sig.HasRest {
		regular--
	}
	paramAt := func(i int) (Type, bool) { return anyArgParamAt(sig, i) }
	retTy := sig.RetType
	if boxRet {
		retTy = TypeAny
	}

	// The any-argument entry: the marked parameters (the rest parameter
	// when a rest argument is marked) as boxes.
	alt := append([]Type(nil), sig.ParamTypes...)
	var mask strings.Builder
	for i := range alt {
		w := false
		if i < regular {
			w = i < len(marked) && marked[i]
		} else {
			for j := regular; j < len(marked); j++ {
				w = w || marked[j]
			}
		}
		if w {
			if i < regular {
				alt[i] = TypeAny
			} else {
				alt[i] = ArrayOf(TypeAny)
			}
			mask.WriteByte('1')
		} else {
			mask.WriteByte('0')
		}
	}
	altName := name + "__kml_anyarg_m" + mask.String()
	altSig := sig
	altSig.ParamTypes = alt
	if anyArgScalarFits(sig.RetType) {
		// The body may return a parameter of another kind: it returns the
		// box, whichever caller asked.
		altName += "_r"
		altSig.RetType = TypeAny
	}
	if _, done := e.funcs[altName]; !done {
		e.funcs[altName] = altSig
		restore := e.libScope(decl)
		restoreWidened := e.widenDynEntryParams(decl.Params, sig.ParamTypes, alt)
		err := e.emitFunctionDeclAs(decl, altName, altSig)
		restoreWidened()
		restore()
		if err != nil {
			delete(e.funcs, altName)
			return Value{}, true, err
		}
	}

	// Each marked argument evaluated once, in order, into a name both
	// calls read.
	e.pushScope()
	defer e.popScope()
	callArgs := append([]ast.Expression(nil), args...)
	fits := "true"
	for i, a := range args {
		if !marked[i] {
			continue
		}
		v, err := e.emitExpr(a)
		if err != nil {
			return Value{}, true, err
		}
		if !v.Ty.IsDynamic() {
			if v, err = e.emitBoxValue(v); err != nil {
				return Value{}, true, err
			}
		}
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", v.Ref, slot))
		tmp := "__kml_anyarg" + strings.TrimPrefix(slot, "%")
		e.define(tmp, Symbol{Ptr: slot, Ty: TypeAny})
		id := ast.NewIdentifier(tmp, a.GetPos())
		callArgs[i] = id
		pt, _ := paramAt(i)
		c := "false" // a class's box never fits an object layout
		switch {
		case e.anyAtClassParam(pt) && !e.classAtObjectParam(a, pt):
			c = e.emitBoxIsInstanceOf(v.Ref, pt)
		case !e.classAtObjectParam(a, pt):
			c = e.emitAnyArgFits(v.Ref, pt)
		}
		f := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", f, fits, c))
		fits = f
	}
	typedL, dynL, joinL := e.freshLabel("anyarg.typed"), e.freshLabel("anyarg.dyn"), e.freshLabel("anyarg.join")
	var res string
	if retTy.IR != "void" {
		res = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", res, retTy.IR, retTy.Align()))
	}
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", fits, typedL, dynL))
	branch := func(label, callee string, s FuncSig, typed bool) error {
		e.emitLabel(label)
		saved := e.anyArgTyped
		e.anyArgTyped = typed
		v, err := e.emitCallToFuncSig(callee, s, callArgs, pos)
		e.anyArgTyped = saved
		if err != nil {
			return err
		}
		if res != "" {
			if retTy.IsDynamic() && !v.Ty.IsDynamic() {
				if v, err = e.emitBoxValue(v); err != nil {
					return err
				}
			} else {
				v = e.convert(v, retTy)
			}
			e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", retTy.IR, v.Ref, res, retTy.Align()))
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
		return nil
	}
	if err := branch(typedL, name, sig, true); err != nil {
		return Value{}, true, err
	}
	if err := branch(dynL, altName, altSig, false); err != nil {
		return Value{}, true, err
	}
	e.emitLabel(joinL)
	if res == "" {
		return Value{Ty: TypeVoid}, true, nil
	}
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", out, retTy.IR, res, retTy.Align()))
	return Value{Ref: out, Ty: retTy}, true, nil
}

// anyHeldDecl reports a binding that holds a box whatever the checker gives
// it: an unannotated one initialized by a call anyArgReturnsBox boxes, or an
// annotated one initialized from an `any` whose
// annotation names a kind the value may not have: a primitive or a typed
// array (`const c: Uint8Array = anyU16`). It holds the box, so the value
// keeps its own kind, as in JavaScript, where an annotation converts
// nothing.
func (e *Emitter) anyHeldDecl(v *ast.VarDeclaration) bool {
	if e.anyHeldEmitting[v] {
		return true // emitted now, under the `any` annotation it is held by
	}
	if v != nil && e.funcSeeded[v.Name] && e.promotedGlobalDecls[v] {
		return true // a var sharing a function's name (seedFuncGlobals)
	}
	if v != nil && e.jsHeldGlobals[v] {
		return true // an untyped global a function reads (registerModuleGlobals)
	}
	if v != nil && v.TypeAnnot == nil && v.Init != nil {
		// An unsound scalar cast (boxUnsoundScalarCasts): the value as it is.
		if as, ok := v.Init.(*ast.AsExpression); ok && e.unsoundCasts[as] {
			return true
		}
		// A proxy itself (`const p = new Proxy(t, h)`): its box, wherever
		// it is read (a function reading the module's binding too).
		if o := e.reprOracle(); o != nil && o.proxyValue(v.Init) {
			return true
		}
		// A read through a proxy: its get trap's result, whatever it is.
		if obj := dynamicReadReceiver(v.Init); obj != nil {
			if o := e.reprOracle(); o != nil && o.proxyValue(obj) {
				return true
			}
		}
	}
	if v != nil && !v.Ambient && v.Init != nil && e.assignRetypedDecls()[v] {
		// A binding an Object.assign gives a field of another type: no
		// layout holds both, so a box does (its object becomes dynamic).
		return true
	}
	if v != nil && !v.Ambient {
		// An object literal's or a class instance's binding whose prototype
		// the program changes (`Object.setPrototypeOf(o, p)`), annotated or
		// not: an object whose methods its chain decides at run time.
		switch init := v.Init.(type) {
		case *ast.ObjectLiteral:
			if e.protoRetargetedDecls()[v] {
				return true
			}
		case *ast.NewExpression:
			if _, user := e.classes[init.ClassName]; user && !init.Qualified && e.protoRetargetedDecls()[v] {
				return true
			}
		}
	}
	if v != nil && v.TypeAnnot == nil && !v.Ambient {
		// A JavaScript array intrinsic's result held boxed, as it is
		// emitted (`find` over an any[]): intrinsicHeldType.
		if it, ok := e.intrinsicHeldType(v, TypePtr); ok && isUnconstrainedDynamic(it) {
			return true
		}
		// An unannotated binding of a call the any-argument check boxes, or
		// of a function returning a read through a proxy.
		call, ok := v.Init.(*ast.CallExpression)
		if ok {
			if e.callReturnsProxyRead(call) {
				return true
			}
		}
		return ok && e.anyArgReturnsBox(call)
	}
	if v == nil || v.TypeAnnot == nil || v.Ambient || v.Init == nil && !e.anyAssignedDecls()[v] {
		return false
	}
	if v.Init != nil {
		// A class itself bound to an object type (`const c: { x: number } =
		// K`): the class's box, so its statics are read and written there.
		if o := e.reprOracle(); o != nil && o.classValue(v.Init) {
			if t := e.annotationType(v.TypeAnnot); t.IsObject() && !t.IsClass() && !t.IsDynamic() {
				return true
			}
		}
	}
	if r, done := e.anyHeldMemo[v]; done {
		return r
	}
	if e.anyHeldMemo == nil {
		e.anyHeldMemo = map[*ast.VarDeclaration]bool{}
	}
	e.anyHeldMemo[v] = false // a cycle through the initializer answers no
	t := e.annotationType(v.TypeAnnot)
	// (A class-typed binding of a box holds the box: it may be a plain
	// object, with no instance layout to read.)
	r := (anyArgParamFits(t) || t.IsClass() && !t.IsDynamic()) &&
		(e.anyAssignedDecls()[v] || v.Init != nil && e.boxesInto(e.typeOfExpr(v.Init), t))
	e.anyHeldMemo[v] = r
	return r
}

// anyHeldIdent reports id as bound by an anyHeldDecl binding.
func (e *Emitter) anyHeldIdent(id *ast.Identifier) bool {
	o := e.reprOracle()
	if o == nil {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil {
		return false
	}
	v, ok := singleVarDecl(sym)
	return ok && e.anyHeldDecl(v)
}

// anyAssignedDecls is the declarations an assignment gives an `any` (the
// checker's type of its right side): `let n: number = 0; n = anyStr`.
func (e *Emitter) anyAssignedDecls() map[*ast.VarDeclaration]bool {
	if e.anyAssigned != nil {
		return e.anyAssigned
	}
	e.anyAssigned = map[*ast.VarDeclaration]bool{}
	o := e.reprOracle()
	if o == nil || e.prog == nil {
		return e.anyAssigned
	}
	ast.Inspect(e.prog, func(n ast.Node) bool {
		a, ok := n.(*ast.AssignmentExpression)
		if !ok {
			return true
		}
		id, ok := a.Left.(*ast.Identifier)
		if !ok {
			return true
		}
		call, isCall := a.Right.(*ast.CallExpression)
		t := o.c.TypeOf(a.Right)
		anyElems := !o.c.Unanswered(t) && t.Flags&checker.Object != 0 && t.Kind == checker.Array && t.Elem != nil && t.Elem.Flags&checker.Any != 0
		if (o.c.Unanswered(t) || t.Flags&checker.Any == 0) && !anyElems && !(isCall && (e.anyArgReturnsBox(call) || e.callReturnsProxyRead(call))) {
			return true
		}
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, ok := singleVarDecl(sym); ok {
				e.anyAssigned[v] = true
			}
		}
		return true
	})
	return e.anyAssigned
}

// anyElemArray reports an array whose elements are boxes (`any[]`), never a
// typed array.
func anyElemArray(t Type) bool {
	return t.IsArray() && !t.IsTypedArray() && t.ElemType != nil && isUnconstrainedDynamic(*t.ElemType)
}

// boxesInto reports a value of type from that a binding of type to holds
// as a box: an unconstrained any, or an any[] where to's elements are
// typed (the array the same, its elements kept as they are).
func (e *Emitter) boxesInto(from, to Type) bool {
	return isUnconstrainedDynamic(from) || anyElemArray(from) && to.IsArray() && !anyElemArray(to)
}

// assignRetypedDecls is the variable bindings (strict lane) an
// Object.assign call names as its target while giving one of its fields a
// value of a type the field does not admit (the checker's AssignRetypes).
func (e *Emitter) assignRetypedDecls() map[*ast.VarDeclaration]bool {
	if e.assignRetyped != nil {
		return e.assignRetyped
	}
	e.assignRetyped = map[*ast.VarDeclaration]bool{}
	o := e.reprOracle()
	if o == nil || e.prog == nil || e.compatJS() {
		return e.assignRetyped
	}
	ast.Inspect(e.prog, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpression)
		if !ok || len(call.Args) < 2 {
			return true
		}
		id, ok := call.Args[0].(*ast.Identifier)
		if !ok || e.declIntrinsic(call) != "Object.assign" {
			return true
		}
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, ok := singleVarDecl(sym); ok && o.c.AssignRetypes(call) {
				e.assignRetyped[v] = true
			}
		}
		return true
	})
	return e.assignRetyped
}

// protoRetargetedDecls is every single-declaration binding named as the
// target or the prototype of Object.setPrototypeOf or
// Reflect.setPrototypeOf, or the prototype of Object.create: an object in a
// prototype chain the program builds.
func (e *Emitter) protoRetargetedDecls() map[*ast.VarDeclaration]bool {
	if e.protoRetargeted != nil {
		return e.protoRetargeted
	}
	e.protoRetargeted = map[*ast.VarDeclaration]bool{}
	o := e.reprOracle()
	if o == nil || e.prog == nil {
		return e.protoRetargeted
	}
	ast.Inspect(e.prog, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpression)
		if !ok || len(call.Args) == 0 {
			return true
		}
		m, ok := call.Callee.(*ast.MemberExpression)
		if !ok {
			return true
		}
		if _, ok := m.Object.(*ast.Identifier); !ok {
			return true // a builtin namespace's call: the cheap filter first
		}
		var named []ast.Expression
		switch k := e.declIntrinsic(call); {
		case (k == "Object.setPrototypeOf" || k == "Reflect.setPrototypeOf") && len(call.Args) >= 2:
			named = call.Args[:2]
		case k == "Object.create" && len(call.Args) >= 1:
			named = call.Args[:1]
		}
		for _, a := range named {
			if id, ok := a.(*ast.Identifier); ok {
				if sym, _ := o.b.Resolve(id); sym != nil {
					if v, ok := singleVarDecl(sym); ok {
						e.protoRetargeted[v] = true
					}
				}
			}
		}
		return true
	})
	return e.protoRetargeted
}

// classAtObjectParam reports argument a, a class itself (`f(K)`), at a
// parameter of a plain object type (`o: { x: number }`).
func (e *Emitter) classAtObjectParam(a ast.Expression, pt Type) bool {
	if !pt.IsObject() || pt.IsClass() || pt.IsDynamic() {
		return false
	}
	o := e.reprOracle()
	return o != nil && o.classValue(a)
}

// anyAtClassParam reports a parameter of a program class's type whose
// instances a box test tells apart (no error or host base).
func (e *Emitter) anyAtClassParam(pt Type) bool {
	if !pt.IsClass() || pt.IsDynamic() || pt.Nullable() {
		return false
	}
	info, ok := e.classes[pt.ClassName]
	return ok && !info.IsErrorSubclass && info.HostBase == nil
}

// emitBoxIsInstanceOf is an i1 that the box word holds an instance of
// pt's class (or a subclass's), laid out as the class is.
func (e *Emitter) emitBoxIsInstanceOf(word string, pt Type) string {
	tag, payload := e.emitUnboxTagPayload(Value{Ref: word, Ty: TypeAny})
	isObj, nonNull, both := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", nonNull, payload))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", both, isObj, nonNull))
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", slot))
	e.emitInstr(fmt.Sprintf("store i1 false, "+irPtr+" %s, align 1", slot))
	readL, doneL := e.freshLabel("boxinst.read"), e.freshLabel("boxinst.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", both, readL, doneL))
	e.emitLabel(readL)
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", hdr, e.emitIntToPtr(payload)))
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", e.emitClassIs(hdr, e.classes[pt.ClassName].TagID), slot))
	e.emitTerminator("br label %" + doneL)
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", r, slot))
	return r
}

// returnsProxyRead is the oracle's returnsProxyRead.
func (e *Emitter) returnsProxyRead(body ast.Node) bool {
	o := e.reprOracle()
	return o != nil && o.returnsProxyRead(body)
}

// callReturnsProxyRead reports a call of an unannotated top-level function
// whose body returns a read through a proxy.
func (e *Emitter) callReturnsProxyRead(call *ast.CallExpression) bool {
	id, ok := call.Callee.(*ast.Identifier)
	if !ok {
		return false
	}
	if o := e.reprOracle(); o != nil {
		// A binding of an unannotated arrow or function expression.
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, ok := singleVarDecl(sym); ok && v.Kind == "const" && v.TypeAnnot == nil {
				switch f := v.Init.(type) {
				case *ast.ArrowFunction:
					var body ast.Node = f.Block
					if f.Body != nil {
						body = f.Body
					}
					return f.RetType == nil && !f.IsAsync && e.returnsProxyRead(body)
				case *ast.FunctionExpression:
					return f.RetType == nil && !f.IsAsync && !f.IsGenerator && e.returnsProxyRead(f.Body)
				}
			}
		}
	}
	mangled, _, found := e.resolveFuncRef(id.Name)
	if !found {
		return false
	}
	fd, ok := e.topFuncDecls[mangled]
	if !ok {
		return false
	}
	return fd.ReturnType == nil && !fd.IsAsync && !fd.IsGenerator && e.returnsProxyRead(fd.Body)
}
