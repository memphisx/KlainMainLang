package llvm

// emit_classes_jsinfer.go — JS-compat class-field inference (TDD-00022
// sub-problem 2, `-compat=js` only). A vanilla-JS class declares no fields;
// they come into being via `this.NAME = expr` in the constructor:
//
//	class Point { constructor(x, y) { this.x = x; this.y = y } }
//
// Under `-compat=js`, a class with a constructor and no declared instance
// fields gets its field list collected from the constructor body: every
// `this.NAME = expr` assignment target (a recursive statement walk, so
// branches/loops/try count), typed by the expression — a bare constructor
// parameter takes the parameter's type (annotated, or the ADR-00042 number
// default), anything else goes through inferExprType. First-assignment-wins
// with a hard error when a later assignment clearly disagrees, and a hard
// error when no type can be inferred — "best-effort" means the rest needs an
// annotation, not silent misbehavior. Constructor-only by design (the usual
// place JS introduces fields); a method inventing a new field stays a clean
// "no field" rejection. Strict mode (`-compat=strict`) is untouched.

import (
	"fmt"

	"KlainMainLang/ast"
)

// jsCollectCtorParamTypes is the `-compat=js` call-site inference pre-pass
// for constructor parameters (the constructor slice of TDD-00005's
// "option 2", adopted by TDD-00022 sub-problem 1): before registerClasses,
// walk the whole program for `new ClassName(args)` sites and infer each
// *unannotated* constructor parameter's type from the arguments actually
// passed. First-inferrable-site-wins; two sites disagreeing on a parameter's
// kind is a hard error asking for an annotation. An argument whose type
// can't be inferred pre-scope (a local variable, a call result) contributes
// nothing — a parameter no site can type keeps the ADR-00042 number default,
// and a non-numeric argument to it is caught by the constructor-call guard.
// jsParamSlot is one collected call-site argument type: the first inferred
// type for that name/index, whether any later site disagreed, and where.
type jsParamSlot struct {
	ty       Type
	conflict bool
	pos      ast.Pos
}

func (e *Emitter) jsCollectCtorParamTypes(prog *ast.Program) error {
	e.jsCtorParamTy = map[string][]Type{}
	record := func(nx *ast.NewExpression) error {
		slots := e.jsCtorParamTy[nx.ClassName]
		for i, a := range nx.Args {
			t := e.inferExprType(a)
			if t.IR == "" || t.IR == "void" || t.IsDynamic {
				continue
			}
			for len(slots) <= i {
				slots = append(slots, Type{})
			}
			if slots[i].IR == "" {
				slots[i] = t
			} else if slots[i].IR != t.IR && !slots[i].IsDynamic {
				// Call sites disagree on this parameter's type. Under -compat=js
				// a constructor parameter legitimately receives different types
				// at different call sites (vanilla JS: `new E('msg')` and
				// `new E('msg' + n)` both valid) — so widen it to `any` rather
				// than rejecting. Strict mode has no call-site-inference pass and
				// no such rejection, and -compat=js must be at least as
				// permissive as strict: a hard error here made the js lane fail
				// files the strict lane accepts (e.g. Test262Error, called with
				// both plain strings and string+number concatenations across the
				// harness). The any-typed parameter stores through the D1
				// dynamic model. Once widened it stays `any` (IsDynamic), so a
				// third disagreeing site doesn't re-trigger.
				slots[i] = TypeAny
			}
		}
		e.jsCtorParamTy[nx.ClassName] = slots
		return nil
	}
	return e.jsWalkProgram(prog, record, nil)
}

// jsCollectFuncParamTypes is the plain-function collection pass. It runs
// AFTER registerClasses (so `new C(...)` and class-instance bindings infer
// to real class types) and before registerFunctions (which consumes the
// slots in buildFunctionSig). Identifier arguments — invisible to the bare
// inferExprType pre-scope — are resolved through a map of *top-level*
// binding initializer types, which covers the common
// `const home = new City(...); describe(home, 2)` shape.
func (e *Emitter) jsCollectFuncParamTypes(prog *ast.Program) error {
	e.jsFuncParamTy = map[string][]jsParamSlot{}

	topBindings := map[string]Type{}
	recordBinding := func(d *ast.VarDeclaration) {
		if d.Init == nil {
			// An untyped, uninitialized binding is dynamic under js
			// (implicit-`any`), so an argument reading it marks the
			// parameter polymorphic rather than contributing nothing.
			if d.TypeAnnot == nil {
				topBindings[d.Name] = TypeAny
			}
			return
		}
		if d.TypeAnnot != nil {
			topBindings[d.Name] = e.resolveType(d.TypeAnnot)
			return
		}
		// An untyped object literal is a dynamic object under js
		// (TDD-00022 break shape 4) — the collector must agree with the
		// vardecl's typing, not the static inference.
		if _, isLit := d.Init.(*ast.ObjectLiteral); isLit {
			topBindings[d.Name] = TypeAny
			return
		}
		if t := e.inferExprType(d.Init); t.IR != "" && t.IR != "void" {
			topBindings[d.Name] = t
		}
	}
	for _, s := range prog.Body {
		switch st := s.(type) {
		case *ast.VarDeclaration:
			recordBinding(st)
		case *ast.VarDeclarationList:
			for _, d := range st.Decls {
				recordBinding(d)
			}
		}
	}

	argType := func(a ast.Expression) Type {
		if id, ok := a.(*ast.Identifier); ok {
			// Only a top-level binding resolves; any other identifier (a
			// local, a parameter of the enclosing function) contributes
			// nothing — inferExprType would answer the number *default* for
			// an unresolvable name, which is a wrong signal, not a neutral
			// one (it silently mistyped `Base.call(this, name)` chains).
			if t, ok := topBindings[id.Name]; ok {
				return t
			}
			return Type{}
		}
		return e.inferExprType(a)
	}
	fnDecls := map[string]*ast.FunctionDeclaration{}
	for _, s := range prog.Body {
		if fd, ok := s.(*ast.FunctionDeclaration); ok {
			fnDecls[fd.Name] = fd
		}
	}

	// A call argument that is the *enclosing function's own parameter*
	// (`function Dog(name) { Animal.call(this, name) }`) becomes a
	// propagation edge — once Dog's `name` resolves (from Dog's own call
	// sites), Animal's parameter takes the same type.
	type depEdge struct {
		fromFn  string
		fromIdx int
		toFn    string
		toIdx   int
		pos     ast.Pos
	}
	var edges []depEdge

	addSlot := func(name string, i int, t Type, pos ast.Pos) {
		slots := e.jsFuncParamTy[name]
		for len(slots) <= i {
			slots = append(slots, jsParamSlot{})
		}
		if slots[i].ty.IR == "" {
			slots[i] = jsParamSlot{ty: t, pos: pos}
		} else if (slots[i].ty.IR != t.IR || slots[i].ty.ClassName != t.ClassName) && !slots[i].conflict {
			slots[i].conflict = true
			slots[i].pos = pos
		}
		e.jsFuncParamTy[name] = slots
	}

	// Disagreement is only *recorded* here — whether the name is a user
	// function with an unannotated parameter at that index is unknown until
	// registerFunctions validates (builtins share the callee namespace).
	recordCall := func(name string, args []ast.Expression, encl *ast.FunctionDeclaration) error {
		for i, a := range args {
			if _, spread := a.(*ast.SpreadElement); spread {
				break // positions shift at runtime; nothing reliable to infer
			}
			if id, ok := a.(*ast.Identifier); ok && encl != nil {
				edgeAdded := false
				for j, p := range encl.Params {
					if p.Name == id.Name {
						edges = append(edges, depEdge{fromFn: encl.Name, fromIdx: j, toFn: name, toIdx: i, pos: a.GetPos()})
						edgeAdded = true
						break
					}
				}
				if edgeAdded {
					continue
				}
			}
			t := argType(a)
			if t.IR == "" || t.IR == "void" {
				continue
			}
			// A dynamic argument at any site makes the parameter genuinely
			// polymorphic — mark the slot conflicted so the implicit-`any`
			// fallback (TDD-00076 A1) takes it, instead of a concrete type
			// from another site silently mis-coercing this one.
			if t.IsDynamic {
				slots := e.jsFuncParamTy[name]
				for len(slots) <= i {
					slots = append(slots, jsParamSlot{})
				}
				slots[i].conflict = true
				slots[i].pos = a.GetPos()
				e.jsFuncParamTy[name] = slots
				continue
			}
			addSlot(name, i, t, a.GetPos())
		}
		return nil
	}
	if err := e.jsWalkProgram(prog, nil, recordCall); err != nil {
		return err
	}

	// Propagate along the edges to a fixed point (chains are shallow; the
	// loop is bounded by the edge count). A source resolves from its
	// annotation first, else its collected slot.
	resolve := func(fn string, idx int) Type {
		if fd := fnDecls[fn]; fd != nil && idx < len(fd.Params) && fd.Params[idx].Type != nil {
			return e.resolveType(fd.Params[idx].Type)
		}
		slots := e.jsFuncParamTy[fn]
		if idx < len(slots) && !slots[idx].conflict && slots[idx].ty.IR != "" {
			return slots[idx].ty
		}
		// A prototype constructor's `new` sites were collected into the
		// class-keyed map (jsCollectCtorParamTypes) — consult it too, so
		// `Base.call(this, x)` chains resolve from `new Derived(...)` sites.
		if cs := e.jsCtorParamTy[fn]; idx < len(cs) {
			return cs[idx]
		}
		return Type{}
	}
	for changed := true; changed; {
		changed = false
		for _, ed := range edges {
			t := resolve(ed.fromFn, ed.fromIdx)
			if t.IR == "" || t.IR == "void" || t.IsDynamic {
				continue
			}
			slots := e.jsFuncParamTy[ed.toFn]
			if ed.toIdx < len(slots) {
				if slots[ed.toIdx].conflict {
					continue // settled (as a conflict) — don't loop forever
				}
				if slots[ed.toIdx].ty.IR == t.IR && slots[ed.toIdx].ty.ClassName == t.ClassName {
					continue
				}
			}
			addSlot(ed.toFn, ed.toIdx, t, ed.pos)
			changed = true
		}
	}
	return nil
}

// jsWalkProgram is the shared whole-program AST walk behind the two
// `-compat=js` call-site collection passes: onNew fires per `new` expression,
// onCall per identifier-callee call (either may be nil). onCall also
// receives the enclosing *top-level* function declaration (nil at module
// level) so an argument that is that function's own parameter can become a
// type-propagation edge instead of a dead end.
func (e *Emitter) jsWalkProgram(prog *ast.Program, onNew func(*ast.NewExpression) error, onCall func(string, []ast.Expression, *ast.FunctionDeclaration) error) error {
	var curFn *ast.FunctionDeclaration
	var walkExpr func(x ast.Expression) error
	var walkStmt func(s ast.Statement) error
	walkBlock := func(b *ast.BlockStatement) error {
		if b == nil {
			return nil
		}
		for _, s := range b.Body {
			if err := walkStmt(s); err != nil {
				return err
			}
		}
		return nil
	}
	walkExprs := func(xs []ast.Expression) error {
		for _, x := range xs {
			if err := walkExpr(x); err != nil {
				return err
			}
		}
		return nil
	}
	walkExpr = func(x ast.Expression) error {
		switch ex := x.(type) {
		case *ast.NewExpression:
			if err := walkExprs(ex.Args); err != nil {
				return err
			}
			if onNew != nil {
				return onNew(ex)
			}
			return nil
		case *ast.CallExpression:
			if err := walkExpr(ex.Callee); err != nil {
				return err
			}
			if err := walkExprs(ex.Args); err != nil {
				return err
			}
			if id, ok := ex.Callee.(*ast.Identifier); ok && onCall != nil {
				return onCall(id.Name, ex.Args, curFn)
			}
			// `F.call(thisArg, args...)` types F's parameters from args[1:] —
			// the pre-ES6 constructor-chaining site (`Base.call(this, name)`)
			// is often a function's *only* call site.
			if mem, ok := ex.Callee.(*ast.MemberExpression); ok && onCall != nil && mem.Property == "call" && len(ex.Args) >= 1 {
				if id, ok := mem.Object.(*ast.Identifier); ok {
					return onCall(id.Name, ex.Args[1:], curFn)
				}
			}
			return nil
		case *ast.MemberExpression:
			return walkExpr(ex.Object)
		case *ast.IndexExpression:
			if err := walkExpr(ex.Object); err != nil {
				return err
			}
			return walkExpr(ex.Index)
		case *ast.BinaryExpression:
			if err := walkExpr(ex.Left); err != nil {
				return err
			}
			return walkExpr(ex.Right)
		case *ast.UnaryExpression:
			return walkExpr(ex.Arg)
		case *ast.AssignmentExpression:
			if err := walkExpr(ex.Left); err != nil {
				return err
			}
			return walkExpr(ex.Right)
		case *ast.ConditionalExpression:
			if err := walkExpr(ex.Test); err != nil {
				return err
			}
			if err := walkExpr(ex.Consequent); err != nil {
				return err
			}
			return walkExpr(ex.Alternate)
		case *ast.ArrayLiteral:
			return walkExprs(ex.Elements)
		case *ast.ObjectLiteral:
			for _, p := range ex.Properties {
				if p.KeyExpr != nil {
					if err := walkExpr(p.KeyExpr); err != nil {
						return err
					}
				}
				if err := walkExpr(p.Value); err != nil {
					return err
				}
			}
		case *ast.SpreadElement:
			return walkExpr(ex.Arg)
		case *ast.AwaitExpression:
			return walkExpr(ex.Argument)
		case *ast.TemplateLiteral:
			return walkExprs(ex.Exprs)
		case *ast.ArrowFunction:
			if ex.Body != nil {
				return walkExpr(ex.Body)
			}
			return walkBlock(ex.Block)
		case *ast.FunctionExpression:
			return walkBlock(ex.Body)
		}
		return nil
	}
	walkStmt = func(s ast.Statement) error {
		switch st := s.(type) {
		case *ast.ExpressionStatement:
			return walkExpr(st.Expr)
		case *ast.VarDeclaration:
			if st.Init != nil {
				return walkExpr(st.Init)
			}
		case *ast.VarDeclarationList:
			for _, d := range st.Decls {
				if d.Init != nil {
					if err := walkExpr(d.Init); err != nil {
						return err
					}
				}
			}
		case *ast.ReturnStatement:
			if st.Value != nil {
				return walkExpr(st.Value)
			}
		case *ast.ThrowStatement:
			return walkExpr(st.Argument)
		case *ast.BlockStatement:
			return walkBlock(st)
		case *ast.IfStatement:
			if err := walkExpr(st.Test); err != nil {
				return err
			}
			if err := walkBlock(st.Consequent); err != nil {
				return err
			}
			if st.Alternate != nil {
				return walkStmt(st.Alternate)
			}
		case *ast.ForStatement:
			if st.Init != nil {
				if err := walkStmt(st.Init); err != nil {
					return err
				}
			}
			if st.Test != nil {
				if err := walkExpr(st.Test); err != nil {
					return err
				}
			}
			if err := walkExprs(st.Update); err != nil {
				return err
			}
			return walkBlock(st.Body)
		case *ast.ForInStatement:
			return walkBlock(st.Body)
		case *ast.ForOfStatement:
			return walkBlock(st.Body)
		case *ast.WhileStatement:
			if err := walkExpr(st.Test); err != nil {
				return err
			}
			return walkBlock(st.Body)
		case *ast.DoWhileStatement:
			if err := walkExpr(st.Test); err != nil {
				return err
			}
			return walkBlock(st.Body)
		case *ast.TryStatement:
			if err := walkBlock(st.Body); err != nil {
				return err
			}
			if st.Catch != nil {
				if err := walkBlock(st.Catch.Body); err != nil {
					return err
				}
			}
			return walkBlock(st.Finally)
		case *ast.SwitchStatement:
			if err := walkExpr(st.Discriminant); err != nil {
				return err
			}
			for _, c := range st.Cases {
				for _, cs := range c.Body {
					if err := walkStmt(cs); err != nil {
						return err
					}
				}
			}
		case *ast.FunctionDeclaration:
			saved := curFn
			curFn = st
			err := walkBlock(st.Body)
			curFn = saved
			return err
		case *ast.ClassDeclaration:
			if st.Constructor != nil {
				if err := walkBlock(st.Constructor.Body); err != nil {
					return err
				}
			}
			for _, m := range st.Methods {
				if err := walkBlock(m.Body); err != nil {
					return err
				}
			}
			for _, f := range st.Fields {
				if f.Initializer != nil {
					if err := walkExpr(f.Initializer); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	for _, s := range prog.Body {
		if err := walkStmt(s); err != nil {
			return err
		}
	}
	return nil
}

// jsApplyCtorParamOverride overlays call-site-inferred types onto a
// constructor sig's *unannotated* parameters (annotated ones always win).
func (e *Emitter) jsApplyCtorParamOverride(className string, params []ast.Param, sig *FuncSig) {
	slots := e.jsCtorParamTy[className]
	for i := range sig.ParamTypes {
		if i >= len(params) || i >= len(slots) {
			break
		}
		if params[i].Type != nil || params[i].Rest || slots[i].IR == "" {
			continue
		}
		sig.ParamTypes[i] = slots[i]
	}
}

// jsInferConstructorFields collects the inferred instance fields for cd.
// seen holds inherited/declared field names — an assignment to one of those
// is a normal field write, not a new field, and is skipped.
func (e *Emitter) jsInferConstructorFields(cd *ast.ClassDeclaration, seen map[string]bool) ([]Field, error) {
	paramTy := map[string]Type{}
	override := e.jsCtorParamTy[cd.Name]
	for i, p := range cd.Constructor.Params {
		if p.ArrayPattern != nil || p.ObjectPattern != nil {
			continue
		}
		switch {
		case p.Type != nil:
			paramTy[p.Name] = e.resolveType(p.Type)
		case p.Rest:
			paramTy[p.Name] = ArrayOf(TypeI64)
		case i < len(override) && override[i].IR != "":
			// Call-site-inferred (jsCollectCtorParamTypes).
			paramTy[p.Name] = override[i]
		default:
			// The unannotated-parameter default (ADR-00042).
			paramTy[p.Name] = TypeI64
		}
	}

	inferredIdx := map[string]int{}
	var fields []Field

	record := func(name string, rhs ast.Expression, pos ast.Pos) error {
		if seen[name] || name == "__proto__" {
			return nil // inherited/declared field write, not a new field
		}
		switch name {
		case ClassTagField, ClassVTableField, ClassEventEmitterField, ClassNodeStreamField:
			return fmt.Errorf("%d:%d: class '%s' cannot introduce a field named '%s' — reserved for the compiler's internal use", pos.Line, pos.Col, cd.Name, name)
		}
		var ty Type
		if id, ok := rhs.(*ast.Identifier); ok {
			if pt, isParam := paramTy[id.Name]; isParam {
				ty = pt
			}
		}
		// An object-literal field keeps its *static* shape even though bare
		// literals are dynamic under js — a class field can't be `any`
		// (objectFieldDynamicRejected), and the static struct is the whole
		// point of the class tier.
		if lit, isLit := rhs.(*ast.ObjectLiteral); isLit {
			ty = e.inferObjectType(lit)
		}
		if ty.IR == "" {
			ty = e.inferExprType(rhs)
		}
		if ty.IR == "" || ty.IR == "void" {
			return fmt.Errorf("%d:%d: cannot infer a type for field '%s' of class '%s' from its constructor assignment — declare the field with a type annotation", pos.Line, pos.Col, name, cd.Name)
		}
		if prev, dup := inferredIdx[name]; dup {
			// First-assignment-wins; a later assignment of a clearly different
			// kind is a hard error rather than a silent pick.
			if fields[prev].Ty.IR != ty.IR {
				return fmt.Errorf("%d:%d: field '%s' of class '%s' is assigned conflicting types in the constructor — declare the field with a type annotation", pos.Line, pos.Col, name, cd.Name)
			}
			return nil
		}
		inferredIdx[name] = len(fields)
		fields = append(fields, Field{Name: name, Ty: ty})
		return nil
	}

	var walkStmt func(s ast.Statement) error
	walkBlock := func(b *ast.BlockStatement) error {
		if b == nil {
			return nil
		}
		for _, s := range b.Body {
			if err := walkStmt(s); err != nil {
				return err
			}
		}
		return nil
	}
	walkStmt = func(s ast.Statement) error {
		switch st := s.(type) {
		case *ast.ExpressionStatement:
			assign, ok := st.Expr.(*ast.AssignmentExpression)
			if !ok || assign.Op != "=" {
				return nil
			}
			mem, ok := assign.Left.(*ast.MemberExpression)
			if !ok {
				return nil
			}
			if _, isThis := mem.Object.(*ast.ThisExpression); !isThis {
				return nil
			}
			return record(mem.Property, assign.Right, assign.GetPos())
		case *ast.BlockStatement:
			return walkBlock(st)
		case *ast.IfStatement:
			if err := walkBlock(st.Consequent); err != nil {
				return err
			}
			if st.Alternate != nil {
				return walkStmt(st.Alternate)
			}
		case *ast.ForStatement:
			return walkBlock(st.Body)
		case *ast.ForInStatement:
			return walkBlock(st.Body)
		case *ast.ForOfStatement:
			return walkBlock(st.Body)
		case *ast.WhileStatement:
			return walkBlock(st.Body)
		case *ast.DoWhileStatement:
			return walkBlock(st.Body)
		case *ast.TryStatement:
			if err := walkBlock(st.Body); err != nil {
				return err
			}
			if st.Catch != nil {
				if err := walkBlock(st.Catch.Body); err != nil {
					return err
				}
			}
			return walkBlock(st.Finally)
		case *ast.SwitchStatement:
			for _, c := range st.Cases {
				for _, cs := range c.Body {
					if err := walkStmt(cs); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}

	if err := walkBlock(cd.Constructor.Body); err != nil {
		return nil, err
	}
	return fields, nil
}
