package llvm

import (
	"fmt"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
)

// emit_heritage.go — a class extending a value a variable holds (`var B =
// function () {}.bind(); class C extends B {}`, valueBases): the class's
// definition evaluates the heritage as JavaScript's ClassDefinitionEvaluation
// does. A value that is no constructor is V8's TypeError; otherwise its
// `prototype`, read once, must be an object or null, and is the class
// prototype's [[Prototype]]. super(…) calls the value with the new instance
// as `this`.

// ensureHeritageProto is the global holding the `prototype` of base's
// value when the latest class extending it was defined; ensureHeritageValue
// the value itself, which super(…) calls.
func (e *Emitter) ensureHeritageProto(base string) string { return e.heritageGlobal("hproto", base) }

func (e *Emitter) ensureHeritageValue(base string) string { return e.heritageGlobal("hbase", base) }

func (e *Emitter) heritageGlobal(kind, base string) string {
	g := "@__kml_" + kind + "." + llvmSafeSymbol(base)
	if e.heritageGlobals == nil {
		e.heritageGlobals = map[string]bool{}
	}
	if !e.heritageGlobals[g] {
		e.heritageGlobals[g] = true
		e.emitGlobal(fmt.Sprintf("%s = internal %sglobal i64 %d, align 8", g, e.isolateTLS(), nbNull))
	}
	return g
}

// emitClassHeritage runs class cd's heritage evaluation where its
// definition runs.
func (e *Emitter) emitClassHeritage(cd *ast.ClassDeclaration) error {
	pos := cd.GetPos()
	n := e.weakSeedCtr
	e.weakSeedCtr++
	hv, hp := fmt.Sprintf("__kml_hv_%d", n), fmt.Sprintf("__kml_hp_%d", n)
	id := func(name string) ast.Expression { return ast.NewIdentifier(name, pos) }
	str := func(s string) ast.Expression { return ast.NewStringLiteral(s, pos) }
	throwType := func(msg ast.Expression) *ast.BlockStatement {
		return ast.NewBlockStatement([]ast.Statement{ast.NewThrowStatement(ast.NewNewErrorExpression("TypeError", msg, pos), pos)}, pos)
	}
	isCtor := ast.NewCallExpression(id("__kml_is_constructor"), []ast.Expression{id(hv)}, pos)
	isCtor.Intrinsic = "kml.isConstructor"
	typeIsNot := func(t string) ast.Expression {
		return ast.NewBinaryExpression("!==", ast.NewUnaryExpression("typeof", true, id(hp), pos), str(t), pos)
	}
	var baseVal ast.Expression = id(cd.BaseClass)
	if builtinValueBases[cd.BaseClass] && e.isGlobalBuiltinRef(cd.BaseClass) {
		// The builtin global's function value.
		v, ok, err := e.builtinCtorValue(cd.BaseClass)
		if err != nil {
			return err
		}
		if !ok {
			// One without a call signature (Proxy): a constructor with no
			// `prototype`, which the heritage rejects.
			e.emitThrowTypeError("Class extends value does not have valid prototype property undefined")
			e.emitLabel(e.freshLabel("heritage.noproto.dead"))
			return nil
		}
		if v, err = e.emitBoxValue(v); err != nil {
			return err
		}
		baseVal = e.bindValue(v, pos)
	}
	stmts := []ast.Statement{
		ast.NewVarDeclaration("const", hv, ast.AnyAnnotation(""), baseVal, pos),
		// null: the prototype's [[Prototype]] is null (ECMA-262
		// ClassDefinitionEvaluation step 8.b).
		ast.NewIfStatement(ast.NewBinaryExpression("&&", ast.NewBinaryExpression("!==", id(hv), ast.NewNullLiteral(false, pos), pos),
			ast.NewUnaryExpression("!", true, isCtor, pos), pos), throwType(
			ast.NewBinaryExpression("+", ast.NewBinaryExpression("+", str("Class extends value "), id(hv), pos), str(" is not a constructor or null"), pos)), nil, pos),
		ast.NewVarDeclaration("const", hp, ast.AnyAnnotation(""), ast.NewConditionalExpression(
			ast.NewBinaryExpression("===", id(hv), ast.NewNullLiteral(false, pos), pos), ast.NewNullLiteral(false, pos),
			ast.NewMemberExpression(id(hv), "prototype", pos), pos), pos),
		ast.NewIfStatement(ast.NewBinaryExpression("&&", ast.NewBinaryExpression("&&",
			ast.NewBinaryExpression("!==", id(hp), ast.NewNullLiteral(false, pos), pos), typeIsNot("object"), pos), typeIsNot("function"), pos),
			throwType(ast.NewBinaryExpression("+", str("Class extends value does not have valid prototype property "), id(hp), pos)), nil, pos),
	}
	for _, st := range stmts {
		if err := e.emitStmt(st); err != nil {
			return err
		}
	}
	// In a fixed order: the emitted IR is the same on every compile.
	for _, ng := range [][2]string{{hv, e.ensureHeritageValue(cd.BaseClass)}, {hp, e.ensureHeritageProto(cd.BaseClass)}} {
		name, g := ng[0], ng[1]
		sym, ok := e.lookup(name)
		if !ok {
			return internalAt(pos, "class heritage of '%s' unbound", cd.BaseClass)
		}
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", v, sym.Ptr))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", v, g))
	}
	if _, ok := e.classes[cd.Name]; ok && e.compatJS() {
		// An evaluation after the first: its prototype object, made on the
		// first read, takes the prototype this one read.
		proto := e.emitClassProto(cd.Name)
		hpSym, _ := e.lookup(hp)
		hpv := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", hpv, hpSym.Ptr))
		e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_relink_proto(i64 %s, i64 %s)", proto.Ref, hpv))
	}
	return nil
}

// emitValueBaseSuper is super(…args) to a value base: the value called with
// the instance as `this`.
func (e *Emitter) emitValueBaseSuper(base string, thisBox Value, args []ast.Expression, pos ast.Pos) error {
	if nonConstructingBuiltins[base] && e.isGlobalBuiltinRef(base) {
		// The arguments, then the base's construct's TypeError.
		for _, a := range args {
			if sp, ok := a.(*ast.SpreadElement); ok {
				a = sp.Arg // a constructor's forwarded rest
			}
			if _, err := e.emitExpr(a); err != nil {
				return err
			}
		}
		e.emitThrowTypeError(base + " is not a constructor")
		e.emitLabel(e.freshLabel("nonctor.dead"))
		return nil
	}
	self := e.bindValue(thisBox, pos)
	bv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", bv, e.ensureHeritageValue(base)))
	callee := e.bindValue(Value{Ref: bv, Ty: TypeAny}, pos)
	if !e.compatJS() {
		call := ast.NewCallExpression(ast.NewMemberExpression(callee, "call", pos), append([]ast.Expression{self}, args...), pos)
		_, err := e.emitExpr(call)
		return err
	}
	// The arguments once, then by the base: a constructor function of the
	// program runs on the instance; any other constructor constructs, and
	// what it built is `this` (the constructor's return override slot).
	argArr, err := e.emitExpr(ast.NewArrayLiteral(args, pos))
	if err != nil {
		return err
	}
	argBox, err := e.emitBoxValue(argArr)
	if err != nil {
		return err
	}
	argv := e.bindValue(argBox, pos)
	plain, isPlain := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_is_plain_ctor_fn(i64 %s)", plain, bv))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isPlain, plain))
	callL, newL, endL := e.freshLabel("vbase.call"), e.freshLabel("vbase.new"), e.freshLabel("vbase.end")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isPlain, callL, newL))
	e.emitLabel(callL)
	apply := ast.NewCallExpression(ast.NewMemberExpression(callee, "apply", pos), []ast.Expression{self, argv}, pos)
	rv, err := e.emitExpr(apply)
	if err != nil {
		return err
	}
	// [[Construct]] of a plain function: an object it returns is the
	// result, so `this` (emitOverrideThis takes only an object).
	if rv, err = e.emitBoxValue(rv); err != nil {
		return err
	}
	// (A primitive, null or undefined it returns leaves `this`: the slot
	// stays unset.)
	isObj, err := e.emitIsObjectValue(rv)
	if err != nil {
		return err
	}
	keep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", keep, isObj, rv.Ref))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" @__kml_ctor_ret_any, align 8", keep))
	e.emitTerminator("br label %" + endL)
	e.emitLabel(newL)
	// SuperCall step 4: a super constructor that is not a constructor
	// (`extends null`'s Function.prototype) throws before anything is
	// constructed, leaving no pending prototype set.
	e.declareFn("__kml_is_constructor", "declare i64 @__kml_is_constructor(i64)")
	isC, isCb := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_is_constructor(i64 %s)", isC, bv))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isCb, isC))
	ctorL, notCtorL := e.freshLabel("vbase.isctor"), e.freshLabel("vbase.notctor")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isCb, ctorL, notCtorL))
	e.emitLabel(notCtorL)
	e.emitThrowTypeError("Super constructor is not a constructor")
	e.emitLabel(ctorL)
	// It takes the prototype of the instance being constructed (the
	// base's construct with newTarget the class): a class instance the
	// base makes takes it as its header is written (Reflect.construct's
	// pending prototype), a dynamic object once built.
	proto, err := e.emitObjectGetPrototypeOf([]ast.Expression{self}, pos)
	if err != nil {
		return err
	}
	if proto, err = e.emitBoxValue(proto); err != nil {
		return err
	}
	pp, isObj, err := e.emitDynProtoOperand(proto, false)
	if err != nil {
		return err
	}
	nonNull, take := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", nonNull, pp))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", take, isObj, nonNull))
	setL, goL := e.freshLabel("vbase.nt.set"), e.freshLabel("vbase.nt.go")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", take, setL, goL))
	e.emitLabel(setL)
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" @__kml_pending_proto, align 8", pp))
	e.emitInstr("store i32 1, " + irPtr + " @__kml_pending_proto_set, align 4")
	e.emitTerminator("br label %" + goL)
	e.emitLabel(goL)
	ne := ast.NewNewExpression("", []ast.Expression{ast.NewSpreadElement(argv, pos)}, pos)
	ne.Callee = callee
	e.lowered(ne)
	built, err := e.emitExpr(ne)
	if err != nil {
		return err
	}
	e.emitInstr("store i32 0, " + irPtr + " @__kml_pending_proto_set, align 4")
	if built, err = e.emitBoxValue(built); err != nil {
		return err
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_adopt_proto(i64 %s, i64 %s)", built.Ref, proto.Ref))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" @__kml_ctor_ret_any, align 8", built.Ref))
	e.emitTerminator("br label %" + endL)
	e.emitLabel(endL)
	return nil
}

// kml.isConstructor(v): IsConstructor(v), a boolean. kml.protoOf(v):
// Object.getPrototypeOf(v).
func init() {
	intrinsics["kml.protoOf"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			v, err := e.emitObjectGetPrototypeOf(ex.Args, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			return e.emitBoxValue(v)
		},
		ty: func(e *Emitter, ex *ast.CallExpression) Type { return TypeAny },
	}
	intrinsics["kml.isConstructor"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			v, err := e.emitExpr(ex.Args[0])
			if err != nil {
				return Value{}, err
			}
			if v, err = e.emitBoxValue(v); err != nil {
				return Value{}, err
			}
			e.declareFn("__kml_is_constructor", "declare i64 @__kml_is_constructor(i64)")
			r, b := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_is_constructor(i64 %s)", r, v.Ref))
			e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", b, r))
			return Value{Ref: b, Ty: TypeBool}, nil
		},
		ty: func(e *Emitter, ex *ast.CallExpression) Type { return TypeBool },
	}
}

// emitValueBaseHasInstance ors into slot whether v, boxed, is an instance of
// a class of the program whose prototype chain holds ctor's `prototype`:
// OrdinaryHasInstance's walk, by Object.getPrototypeOf, for a static
// instance.
func (e *Emitter) emitValueBaseHasInstance(v string, ctor ast.Expression, slot string, pos ast.Pos) error {
	tag, _ := e.emitUnboxTagPayload(Value{Ref: v, Ty: TypeAny})
	stL, doneL := e.emitTagCheck(tag, kmlTagObject, "vbinst.static")
	e.emitLabel(stL)
	n := e.weakSeedCtr
	e.weakSeedCtr++
	pn, tn, fn := fmt.Sprintf("__kml_vbp_%d", n), fmt.Sprintf("__kml_vbt_%d", n), fmt.Sprintf("__kml_vbf_%d", n)
	id := func(s string) ast.Expression { return ast.NewIdentifier(s, pos) }
	getProto := func(x ast.Expression) ast.Expression {
		c := ast.NewCallExpression(id("__kml_proto_of"), []ast.Expression{x}, pos)
		c.Intrinsic = "kml.protoOf"
		return c
	}
	val := e.bindValue(Value{Ref: v, Ty: TypeAny}, pos)
	stmts := []ast.Statement{
		ast.NewVarDeclaration("const", tn, ast.AnyAnnotation(""), ast.NewMemberExpression(ctor, "prototype", pos), pos),
		ast.NewVarDeclaration("let", fn, nil, ast.NewBooleanLiteral(false, pos), pos),
		ast.NewVarDeclaration("let", pn, ast.AnyAnnotation(""), getProto(val), pos),
		ast.NewWhileStatement(ast.NewBinaryExpression("&&", ast.NewUnaryExpression("!", true, id(fn), pos),
			ast.NewBinaryExpression("!==", id(pn), ast.NewNullLiteral(false, pos), pos), pos),
			ast.NewBlockStatement([]ast.Statement{
				ast.NewExpressionStatement(ast.NewAssignmentExpression("=", id(fn), ast.NewBinaryExpression("===", id(pn), id(tn), pos), pos), pos),
				ast.NewExpressionStatement(ast.NewAssignmentExpression("=", id(pn), getProto(id(pn)), pos), pos),
			}, pos), pos),
	}
	for _, st := range stmts {
		if err := e.emitStmt(st); err != nil {
			return err
		}
	}
	r, err := e.emitExpr(id(fn))
	if err != nil {
		return err
	}
	r = e.convert(r, TypeBool)
	old, out := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", old, slot))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", out, old, r.Ref))
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", out, slot))
	e.emitTerminator("br label %" + doneL)
	e.emitLabel(doneL)
	return nil
}

// isGlobalBuiltinRef reports whether name, read at the top level, is the
// builtin global (no program declaration of its own).
func (e *Emitter) isGlobalBuiltinRef(name string) bool {
	for _, st := range e.prog.Body {
		if e.libStmts[st] != "" {
			continue
		}
		switch d := st.(type) {
		case *ast.VarDeclaration:
			if d.Name == name {
				return false
			}
		case *ast.FunctionDeclaration:
			if d.Name == name {
				return false
			}
		case *ast.ClassDeclaration:
			if d.Name == name {
				return false
			}
		}
	}
	return true
}

// heritageAtSite reports a hoisted class expression whose heritage runs
// where the expression is evaluated (classHeritageSites).
func (e *Emitter) heritageAtSite(cd *ast.ClassDeclaration) bool {
	for _, c := range e.classHeritageSites {
		if c == cd {
			return true
		}
	}
	return false
}

// defaultCtorsOverReturningBases gives a class with no constructor of its
// own, extending a constructor function whose body returns a value, its
// default derived constructor written out (`constructor(...args) {
// super(...args); }`, ECMA-262 ClassDefinitionEvaluation 14.a): the
// object the base returns is then `this`, its fields and private names
// installed on it, as an explicit constructor's are.
func (e *Emitter) defaultCtorsOverReturningBases(prog *ast.Program) {
	for _, st := range prog.Body {
		cd, ok := st.(*ast.ClassDeclaration)
		if !ok || cd.Constructor != nil || cd.BaseClass == "" || e.libStmts[st] != "" || !e.fnBaseReturns(cd.BaseClass) {
			continue
		}
		src, err := parser.Parse("class __kml_dc extends __kml_db { constructor(...args) { super(...args); } }")
		if err != nil || len(src.Body) != 1 {
			continue
		}
		if tmpl, ok := src.Body[0].(*ast.ClassDeclaration); ok && tmpl.Constructor != nil {
			cd.Constructor = tmpl.Constructor
		}
	}
}
