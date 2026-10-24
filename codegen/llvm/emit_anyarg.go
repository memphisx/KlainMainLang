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
	e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", slot))
	readL, doneL := e.freshLabel("boxarr.read"), e.freshLabel("boxarr.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", both, readL, doneL))
	e.emitLabel(readL)
	box := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", box, payload))
	anyOf := func(field int, want []int) string {
		gp, b := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gp, anyArrayBoxTy, box, field))
		e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", b, gp))
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
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", ok, slot))
	e.emitTerminator("br label %" + doneL)
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", r, slot))
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
		if !has || !anyArgParamFits(pt) {
			continue
		}
		if at := e.inferExprType(a); !isUnconstrainedDynamic(at) && !(anyElemArray(at) && !anyElemArray(pt)) {
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
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v.Ref, slot))
		tmp := "__kml_anyarg" + strings.TrimPrefix(slot, "%")
		e.define(tmp, Symbol{Ptr: slot, Ty: TypeAny})
		id := ast.NewIdentifier(tmp, a.GetPos())
		callArgs[i] = id
		pt, _ := paramAt(i)
		c := e.emitAnyArgFits(v.Ref, pt)
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
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", retTy.IR, v.Ref, res, retTy.Align()))
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
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", out, retTy.IR, res, retTy.Align()))
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
	if v != nil && e.funcSeeded[v.Name] && e.promotedGlobalDecls[v] {
		return true // a var sharing a function's name (seedFuncGlobals)
	}
	if v != nil && v.TypeAnnot == nil && !v.Ambient {
		// An unannotated binding of a call the any-argument check boxes.
		call, ok := v.Init.(*ast.CallExpression)
		return ok && e.anyArgReturnsBox(call)
	}
	if v == nil || v.TypeAnnot == nil || v.Ambient || v.Init == nil && !e.anyAssignedDecls()[v] {
		return false
	}
	if r, done := e.anyHeldMemo[v]; done {
		return r
	}
	if e.anyHeldMemo == nil {
		e.anyHeldMemo = map[*ast.VarDeclaration]bool{}
	}
	e.anyHeldMemo[v] = false // a cycle through the initializer answers no
	t := e.resolveType(v.TypeAnnot)
	r := anyArgParamFits(t) &&
		(e.anyAssignedDecls()[v] || v.Init != nil && e.boxesInto(e.inferExprType(v.Init), t))
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
		if (o.c.Unanswered(t) || t.Flags&checker.Any == 0) && !anyElems && !(isCall && e.anyArgReturnsBox(call)) {
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
