package resolver

import (
	"fmt"
	"reflect"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
)

// superUncalledMsg is V8's ReferenceError for a derived constructor's
// `this` read (or end) before its super call.
const superUncalledMsg = "Must call super constructor in derived class before accessing 'this' or returning from derived constructor"

// lowerFunctionSubclasses lowers each class extending the global Function
// to what JavaScript makes of it: an instance is a function the Function
// constructor builds from the construction's (super call's) arguments,
// whose prototype is the class's, its fields defined on it. Its methods
// stay the class's, on a prototype that inherits Function.prototype. Each
// `new C(…)` of such a class is lowered in place to a call of the class's
// helpers (emitted beside it), so the Function constructor sees its
// arguments where they are written: a constant body compiles, one computed
// at run time is the Function constructor's EvalError. A class this
// cannot express (a private name, a `super` member, a constructor
// `return`) is left to the compiler's diagnostic.
func lowerFunctionSubclasses(prog *ast.Program) {
	declared := map[string]int{}
	ast.Inspect(prog, func(x ast.Node) bool {
		switch d := x.(type) {
		case *ast.ClassDeclaration:
			declared[d.Name]++
		case *ast.FunctionDeclaration:
			declared[d.Name]++
			for _, p := range d.Params {
				declared[p.Name]++
			}
		case *ast.VarDeclaration:
			declared[d.Name]++
		case *ast.ArrowFunction:
			for _, p := range d.Params {
				declared[p.Name]++
			}
		}
		return true
	})
	if declared["Function"] > 0 {
		return
	}
	// `var C = class extends Function {}`: the declaration `class C`, its
	// helpers after the binding.
	bound := map[*ast.ClassDeclaration]*ast.VarDeclaration{}
	ast.Inspect(prog, func(x ast.Node) bool {
		if v, ok := x.(*ast.VarDeclaration); ok && v.TypeAnnot == nil {
			if ce, ok := v.Init.(*ast.ClassExpression); ok && ce.Decl.BaseClass == "Function" {
				switch ce.Decl.Name {
				case "$ClassExpr":
					ce.Decl.Name = v.Name // (the binding, counted once)
				case v.Name:
					declared[v.Name]-- // (counted as the binding and the class)
				}
				bound[ce.Decl] = v
			}
		}
		return true
	})
	subs := map[string]*ast.ClassDeclaration{}
	ast.Inspect(prog, func(x ast.Node) bool {
		if cd, ok := x.(*ast.ClassDeclaration); ok && cd.BaseClass == "Function" && !cd.BaseQualified && declared[cd.Name] == 1 && functionSubclassLowerable(cd) {
			subs[cd.Name] = cd
		}
		return true
	})
	if len(subs) == 0 {
		return
	}
	// `var S = class K extends Function {}`: `new S(…)` constructs K.
	byName := map[string]*ast.ClassDeclaration{}
	for name, cd := range subs {
		byName[name] = cd
	}
	for cd, v := range bound {
		if subs[cd.Name] == cd && v.Name != cd.Name && declared[v.Name] == 1 {
			byName[v.Name] = cd
		}
	}
	var sub func(ast.Node) ast.Node
	sub = func(x ast.Node) ast.Node {
		ast.RewriteChildren(x, sub)
		if ne, ok := x.(*ast.NewExpression); ok && ne.Callee == nil && !ne.Qualified {
			if cd, ok := byName[ne.ClassName]; ok {
				return functionSubclassNew(cd, ne)
			}
		}
		return x
	}
	sub(prog)
	helpers := map[*ast.ClassDeclaration][]ast.Statement{}
	for _, cd := range subs {
		// (A named class expression's own name is the class's only: the
		// helpers outside it reach it through its binding.)
		ref := cd.Name
		if v, ok := bound[cd]; ok {
			ref = v.Name
		}
		helpers[cd] = append(functionSubclassHelpers(cd, ref), functionSubclassReceivers(prog, cd, ref)...)
		// A construction this lowering does not see (`new K()` with K
		// holding the class) passes its arguments at run time.
		ctor := fnSubTemplate(`class X { constructor() { throw new EvalError("Function of a string computed at run time is not supported: it needs an embedded JavaScript engine"); } }`).(*ast.ClassDeclaration).Constructor
		if cd.Constructor != nil {
			// (Its length: the parameters before the first default or rest.)
			for _, p := range cd.Constructor.Params {
				if p.Default != nil || p.Rest || p.ArrayPattern != nil || p.ObjectPattern != nil {
					break
				}
				ctor.Params = append(ctor.Params, ast.Param{Name: p.Name, Pos: p.Pos})
			}
		}
		cd.Constructor = ctor
		var fields []ast.AnnotField
		for _, f := range cd.Fields {
			if f.Static {
				fields = append(fields, f)
			}
		}
		cd.Fields = fields
		cd.BaseClass = ""
		if prog.FunctionSubclasses == nil {
			prog.FunctionSubclasses = map[string]bool{}
		}
		prog.FunctionSubclasses[cd.Name] = true
	}
	insertAfterClasses(prog, helpers, bound)
	// An instance is a function: the class as a type is any.
	retypeAny(reflect.ValueOf(prog), func(t ast.TypeNode) bool {
		tr, ok := t.(*ast.TypeReference)
		return ok && len(tr.TypeArgs) == 0 && len(tr.Qualifier) == 0 && byName[tr.Name] != nil
	}, map[uintptr]bool{})
	// The constructions' Function calls, as the files' own were.
	rewriteFunctionConstructor(prog, functionSubclassFileIdx)
}

// functionSubclassFileIdx names the Function-constructor factories of the
// lowered constructions apart from every file's.
const functionSubclassFileIdx = 1 << 20

// typeNodeType is the ast.TypeNode interface, for retypeAny.
var typeNodeType = reflect.TypeOf((*ast.TypeNode)(nil)).Elem()

// retypeAny replaces, everywhere under v, each type node match reports
// with any. (The node walkers do not visit type annotations.)
func retypeAny(v reflect.Value, match func(ast.TypeNode) bool, seen map[uintptr]bool) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		retypeAny(v.Elem(), match, seen)
	case reflect.Interface:
		if !v.IsNil() {
			retypeAny(v.Elem(), match, seen)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if !f.CanSet() {
				continue
			}
			if f.Type() == typeNodeType && !f.IsNil() {
				if match(f.Interface().(ast.TypeNode)) {
					f.Set(reflect.ValueOf(ast.TypeNode(&ast.KeywordType{Keyword: "any"})))
					continue
				}
			}
			retypeAny(f, match, seen)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			e := v.Index(i)
			if e.Type() == typeNodeType && !e.IsNil() && e.CanSet() && match(e.Interface().(ast.TypeNode)) {
				e.Set(reflect.ValueOf(ast.TypeNode(&ast.KeywordType{Keyword: "any"})))
				continue
			}
			retypeAny(e, match, seen)
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			retypeAny(iter.Value(), match, seen)
		}
	}
}

// insertAfterClasses places each class's statements right after its
// declaration, in whatever statement list holds it.
func insertAfterClasses(prog *ast.Program, after map[*ast.ClassDeclaration][]ast.Statement, bound map[*ast.ClassDeclaration]*ast.VarDeclaration) {
	byVar := map[*ast.VarDeclaration]*ast.ClassDeclaration{}
	for cd, v := range bound {
		byVar[v] = cd
	}
	place := func(stmts []ast.Statement) []ast.Statement {
		var out []ast.Statement
		changed := false
		for _, st := range stmts {
			out = append(out, st)
			cd, ok := st.(*ast.ClassDeclaration)
			if v, isVar := st.(*ast.VarDeclaration); isVar {
				cd, ok = byVar[v]
			}
			if ok && after[cd] != nil {
				out = append(out, after[cd]...)
				changed = true
			}
		}
		if !changed {
			return stmts
		}
		return out
	}
	prog.Body = place(prog.Body)
	ast.Inspect(prog, func(x ast.Node) bool {
		switch b := x.(type) {
		case *ast.BlockStatement:
			b.Body = place(b.Body)
		case *ast.SwitchStatement:
			for i := range b.Cases {
				b.Cases[i].Body = place(b.Cases[i].Body)
			}
		}
		return true
	})
}

// functionSubclassLowerable reports a class extending Function whose
// members the lowering expresses.
func functionSubclassLowerable(cd *ast.ClassDeclaration) bool {
	if len(cd.TypeParams) > 0 || len(cd.Decorators) > 0 || len(cd.AutoAccessors) > 0 {
		return false
	}
	for _, f := range cd.Fields {
		if f.Key != nil || f.KeyExpr != nil || len(f.Name) > 0 && f.Name[0] == '#' {
			return false
		}
	}
	ok := true
	for _, m := range cd.Methods {
		if len(m.Name) > 0 && m.Name[0] == '#' || m.KeyExpr != nil {
			return false
		}
		ast.Inspect(m, func(x ast.Node) bool {
			if mem, isMem := x.(*ast.MemberExpression); isMem {
				if _, sup := mem.Object.(*ast.SuperExpression); sup {
					ok = false
				}
			}
			return ok
		})
	}
	if c := cd.Constructor; c != nil && ok {
		for _, p := range c.Params {
			if p.Name != "" && p.Name[0] == '#' {
				return false
			}
		}
		walkOwnBody(c.Body, func(x ast.Node) {
			switch v := x.(type) {
			case *ast.ReturnStatement:
				ok = false
			case *ast.MemberExpression:
				if _, sup := v.Object.(*ast.SuperExpression); sup {
					ok = false
				}
			}
		})
	}
	return ok
}

// walkOwnBody visits the nodes of a function body that are its own: not
// those of a nested non-arrow function or class, which have their own
// `this`, super and return.
func walkOwnBody(n ast.Node, visit func(ast.Node)) {
	if n == nil {
		return
	}
	ast.Inspect(n, func(x ast.Node) bool {
		switch x.(type) {
		case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
			return x == n
		}
		visit(x)
		return true
	})
}

// rewriteOwn rewrites a function body's own nodes (walkOwnBody's), each
// child before its parent.
func rewriteOwn(n ast.Node, rw func(ast.Node) ast.Node) ast.Node {
	var sub func(ast.Node) ast.Node
	sub = func(x ast.Node) ast.Node {
		switch x.(type) {
		case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
			return x
		}
		ast.RewriteChildren(x, sub)
		return rw(x)
	}
	ast.RewriteChildren(n, sub)
	return rw(n)
}

// functionSubclassReceivers makes the receiver of C's instance members
// what an instance is, a function: a method takes `this: any`, and an
// accessor (which TypeScript gives no `this` parameter) is defined on
// C.prototype as a function expression's, returned as statements to
// follow the class.
func functionSubclassReceivers(prog *ast.Program, cd *ast.ClassDeclaration, ref string) []ast.Statement {
	ev, err := parser.ParseEval(`function f(this: any) {}`)
	if err != nil {
		panic(err)
	}
	var thisAny ast.TypeNode
	for _, t := range ev.ThisParams {
		thisAny = t
	}
	var out []ast.Statement
	var methods []*ast.FunctionDeclaration
	for _, m := range cd.Methods {
		if m.IsStatic || m.Body == nil {
			methods = append(methods, m)
			continue
		}
		if m.AccessorKind == "" {
			if prog.ThisParams == nil {
				prog.ThisParams = map[ast.Node]ast.TypeNode{}
			}
			if _, has := prog.ThisParams[m]; !has {
				prog.ThisParams[m] = thisAny
			}
			methods = append(methods, m)
			continue
		}
		key := strings.TrimPrefix(strings.TrimPrefix(m.Name, "__kml_get_"), "__kml_set_")
		def := fnSubTemplate(fmt.Sprintf(`Object.defineProperty(%s.prototype, %q, { %s: function () { BODY; }, enumerable: false, configurable: true });`, ref, key, m.AccessorKind))
		ast.Inspect(def, func(x ast.Node) bool {
			if fe, ok := x.(*ast.FunctionExpression); ok {
				fe.Params = m.Params
				splice(fe.Body, "BODY", m.Body.Body)
				return false
			}
			return true
		})
		out = append(out, def)
	}
	cd.Methods = methods
	return out
}

// functionSubclassNew is `new C(args)` for C a class extending Function:
// the Function constructor's function of args made C's, or C's
// constructor run on args.
func functionSubclassNew(cd *ast.ClassDeclaration, ne *ast.NewExpression) ast.Expression {
	pos := ne.GetPos()
	if c := cd.Constructor; c != nil && c.Body != nil {
		// Its first argument: the super call's function, made by a thunk
		// whose Function arguments are the construction's own constant
		// strings, when the constructor only forwards its parameters to
		// super (forwardedSuperArgs); else undefined, the super call
		// building it.
		var made ast.Expression = ast.NewNullLiteral(true, pos)
		if args, ok := forwardedSuperArgs(c, ne.Args); ok {
			th := fnSubTemplate(`(): any => 0;`).(*ast.ExpressionStatement).Expr.(*ast.ArrowFunction)
			th.Body = ast.NewNewExpression("Function", args, pos)
			made = th
		}
		return ast.NewCallExpression(ast.NewIdentifier("__kml_fsc_"+cd.Name, pos), append([]ast.Expression{made}, ne.Args...), pos)
	}
	return functionSubclassMake(cd, ne.Args, pos)
}

// forwardedSuperArgs are the arguments c's one super call passes, resolved
// against a construction's arguments args, when each is a constant string
// or a parameter (its rest parameter spread) the body never assigns, and
// each argument it reaches is a constant string: fresh literals, for the
// Function constructor to compile.
func forwardedSuperArgs(c *ast.FunctionDeclaration, args []ast.Expression) ([]ast.Expression, bool) {
	var supers []*ast.CallExpression
	assigned := map[string]bool{}
	walkOwnBody(c.Body, func(x ast.Node) {
		switch v := x.(type) {
		case *ast.CallExpression:
			if _, sup := v.Callee.(*ast.SuperExpression); sup {
				supers = append(supers, v)
			}
		case *ast.AssignmentExpression:
			if id, ok := v.Left.(*ast.Identifier); ok {
				assigned[id.Name] = true
			}
		case *ast.UpdateExpression:
			if id, ok := v.Arg.(*ast.Identifier); ok {
				assigned[id.Name] = true
			}
		}
	})
	if len(supers) != 1 {
		return nil, false
	}
	str := func(x ast.Expression) (string, bool) {
		switch a := x.(type) {
		case *ast.StringLiteral:
			return a.Value, true
		case *ast.TemplateLiteral:
			if len(a.Exprs) == 0 && len(a.Quasis) == 1 {
				return a.Quasis[0], true
			}
		}
		return "", false
	}
	for _, a := range args {
		if _, ok := str(a); !ok {
			return nil, false
		}
	}
	param := map[string]int{}
	rest := -1
	for i, p := range c.Params {
		if p.ArrayPattern != nil || p.ObjectPattern != nil || p.Default != nil {
			return nil, false
		}
		if p.Rest {
			rest = i
		}
		param[p.Name] = i
	}
	var out []ast.Expression
	for _, a := range supers[0].Args {
		pos := a.GetPos()
		if v, ok := str(a); ok {
			out = append(out, ast.NewStringLiteral(v, pos))
			continue
		}
		if sp, ok := a.(*ast.SpreadElement); ok {
			id, isID := sp.Arg.(*ast.Identifier)
			if !isID || assigned[id.Name] || rest < 0 || param[id.Name] != rest {
				return nil, false
			}
			for _, x := range args[min(rest, len(args)):] {
				v, _ := str(x)
				out = append(out, ast.NewStringLiteral(v, pos))
			}
			continue
		}
		id, isID := a.(*ast.Identifier)
		i, isParam := param[id.Name]
		if !isID || !isParam || i == rest || assigned[id.Name] {
			return nil, false
		}
		if i >= len(args) {
			return nil, false // (undefined: "undefined", not a constant here)
		}
		v, _ := str(args[i])
		out = append(out, ast.NewStringLiteral(v, pos))
	}
	return out, true
}

// functionSubclassMake is `__kml_fsmk_C(new Function(args))`.
func functionSubclassMake(cd *ast.ClassDeclaration, args []ast.Expression, pos ast.Pos) ast.Expression {
	fn := ast.NewNewExpression("Function", args, pos)
	return ast.NewCallExpression(ast.NewIdentifier("__kml_fsmk_"+cd.Name, pos), []ast.Expression{fn}, pos)
}

// functionSubclassHelpers are class C's two functions: __kml_fsmk_C(f),
// making function f an instance of C (its prototype C's, C's instance
// fields defined on it), and with an explicit constructor
// __kml_fsc_C(params), that constructor's body with its super call making
// the instance and `this` reading it (V8's ReferenceError before then).
func functionSubclassHelpers(cd *ast.ClassDeclaration, ref string) []ast.Statement {
	const f = "__kml_fsf"
	mk := fnSubTemplate(fmt.Sprintf(`function __kml_fsmk_%[2]s(%[1]s: any): any { Object.setPrototypeOf(%[1]s, %[3]s.prototype); FIELDS; return %[1]s; }`, f, cd.Name, ref)).(*ast.FunctionDeclaration)
	var defs []ast.Statement
	for _, fld := range cd.Fields {
		if fld.Static {
			continue
		}
		var init ast.Expression = ast.NewNullLiteral(true, cd.GetPos())
		if fld.Initializer != nil {
			init = rewriteOwn(fld.Initializer, func(x ast.Node) ast.Node {
				if v, ok := x.(*ast.ThisExpression); ok {
					return ast.NewIdentifier(f, v.GetPos())
				}
				return x
			}).(ast.Expression)
		}
		def := fnSubTemplate(fmt.Sprintf(`Object.defineProperty(%s, %q, { value: INIT, writable: true, enumerable: true, configurable: true });`, f, fld.Name))
		rewriteOwn(def, func(x ast.Node) ast.Node {
			if id, ok := x.(*ast.Identifier); ok && id.Name == "INIT" {
				return init
			}
			return x
		})
		defs = append(defs, def)
	}
	splice(mk.Body, "FIELDS", defs)
	// C.prototype inherits Function.prototype.
	out := []ast.Statement{fnSubTemplate(fmt.Sprintf(`Object.setPrototypeOf(%s.prototype, Function.prototype);`, ref)), mk}
	c := cd.Constructor
	if c == nil || c.Body == nil {
		return out
	}
	const t, th = "__kml_fst", "__kml_fsth"
	ctor := fnSubTemplate(fmt.Sprintf(`function __kml_fsc_%[4]s(__kml_fsmade: any): any { var %[1]s: any = undefined; var %[2]s = (): any => { if (%[1]s === undefined) throw new ReferenceError(%[3]q); return %[1]s; }; BODY; return %[2]s(); }`, t, th, superUncalledMsg, cd.Name)).(*ast.FunctionDeclaration)
	ctor.Params = append(ctor.Params[:1:1], c.Params...)
	body := rewriteOwn(c.Body, func(x ast.Node) ast.Node {
		switch v := x.(type) {
		case *ast.NewTargetExpression:
			return ast.NewIdentifier(ref, v.GetPos())
		case *ast.ThisExpression:
			return ast.NewCallExpression(ast.NewIdentifier(th, v.GetPos()), nil, v.GetPos())
		case *ast.CallExpression:
			if _, sup := v.Callee.(*ast.SuperExpression); sup {
				// The construction's function when it made one, else the
				// super call's arguments' (super-must-be-called: once).
				p := v.GetPos()
				fn := ast.NewConditionalExpression(ast.NewIdentifier("__kml_fsmade", p),
					ast.NewCallExpression(ast.NewIdentifier("__kml_fsmade", p), nil, p),
					ast.NewNewExpression("Function", v.Args, p), p)
				mk := ast.NewCallExpression(ast.NewIdentifier("__kml_fsmk_"+cd.Name, p), []ast.Expression{fn}, p)
				return ast.NewAssignmentExpression("=", ast.NewIdentifier(t, p), mk, p)
			}
		}
		return x
	}).(*ast.BlockStatement)
	splice(ctor.Body, "BODY", body.Body)
	return append(out, ctor)
}

// splice replaces block's statement `name;` with stmts.
func splice(block *ast.BlockStatement, name string, stmts []ast.Statement) {
	var out []ast.Statement
	for _, st := range block.Body {
		if es, ok := st.(*ast.ExpressionStatement); ok {
			if id, ok := es.Expr.(*ast.Identifier); ok && id.Name == name {
				out = append(out, stmts...)
				continue
			}
		}
		out = append(out, st)
	}
	block.Body = out
}

// fnSubTemplate parses one statement of the lowering.
func fnSubTemplate(src string) ast.Statement {
	ev, err := parser.ParseEval(src)
	if err != nil || len(ev.Body) != 1 {
		panic("Function subclass template: " + src)
	}
	return ev.Body[0]
}
