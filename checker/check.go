package checker

import (
	"KlainMainLang/lib"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/diag"
)

// Check checks the whole program and returns the type errors it finds, as
// TypeScript reports them under --strict (TDD-00230 P2.7). A relation the
// checker does not model fully is never reported: only a definite error is.
func (c *Checker) Check() []*diag.Diagnostic {
	if c.checked {
		return c.diags
	}
	c.checked = true
	if c.b.Module != nil && c.b.Module.Node != nil {
		c.checkNode(c.b.Module.Node, nil)
		c.checkTypeNames(c.b.Module.Node)
		c.checkStrictCode(c.b.Module.Node)
	}
	return c.diags
}

// CheckFiles is Check over a program the resolver merged from several
// files: fileOf names the file each top-level statement came from, and every
// diagnostic is attributed to it.
func (c *Checker) CheckFiles(fileOf func(ast.Statement) string) []*diag.Diagnostic {
	if c.checked {
		return c.diags
	}
	c.checked = true
	prog, ok := c.b.Module.Node.(*ast.Program)
	if !ok {
		return nil
	}
	for _, st := range prog.Body {
		c.file = fileOf(st)
		c.checkNode(st, nil)
		c.checkTypeNames(st)
		c.checkStrictCode(st)
	}
	for _, wm := range prog.WorkerModules {
		for _, st := range wm.Body {
			c.file = fileOf(st)
			c.checkNode(st, nil)
			c.checkTypeNames(st)
			c.checkStrictCode(st)
		}
	}
	c.file = ""
	return c.diags
}

// constructsTakeTypeArgs reports a construct signature among sigs that
// takes n type arguments: at least its required ones, at most all.
func (c *Checker) constructsTakeTypeArgs(sigs []*Type, n int) bool {
	for _, sig := range sigs {
		min := 0
		for _, tp := range sig.TypeParams {
			if tp.Default == nil {
				min++
			}
		}
		if n >= min && n <= len(sig.TypeParams) {
			return true
		}
	}
	return false
}

// classIsGeneric reports a class declaring type parameters.
func (c *Checker) classIsGeneric(sym *binder.Symbol) bool {
	for _, d := range sym.Declarations {
		if cd, ok := d.Node.(*ast.ClassDeclaration); ok && (len(cd.TypeParams) > 0 || cd.Declared != nil && len(cd.Declared.Names) > 0) {
			return true
		}
	}
	return false
}

// strictHint tells the user what the strict lane's type errors are.
const strictHint = "TypeScript rejects this program; -compat=js compiles it as JavaScript"

func (c *Checker) report(m *diag.Message, pos ast.Pos, args ...any) {
	if c.evalDepth > 0 {
		return
	}
	d := diag.New(m, diag.Span{File: c.file, Pos: diag.Pos{Line: pos.Line, Col: pos.Col}}, args...)
	if m.Kind != diag.Unsupported {
		d.Hint = strictHint // (a limitation of this compiler's says its own)
	}
	c.diags = append(c.diags, d)
}

// checkNode checks n and its children; ret is the declared return type of
// the function n is in (nil when it has none, or is not a function's body).
func (c *Checker) checkNode(n ast.Node, ret *Type) {
	if e, ok := n.(ast.Expression); ok && c.isEvalRoot(e) {
		// Code spliced in from a string, which tsc never checks: walked for
		// its types, its diagnostics dropped.
		c.evalDepth++
		defer func() { c.evalDepth-- }()
	}
	if a, ok := n.(*ast.AssignmentExpression); ok && a.Op == "=" {
		if id, ok := a.Left.(*ast.Identifier); ok {
			c.implicitAnyReported[id] = true // a store, not a read
		}
	}
	c.checkSyntacticUse(n)
	switch n := n.(type) {
	case *ast.SuperExpression:
		if cd := c.superClass(n); cd != nil && cd.BaseClass == "" {
			c.report(diag.SuperNotDerived, n.GetPos())
		}
	case *ast.ObjectLiteral:
		for _, p := range n.Properties {
			if fe, ok := p.Value.(*ast.FunctionExpression); ok && p.AccessorKind == "get" {
				c.checkGetterReturns(fe.Body, namePos(fe.NamePos, fe.GetPos()))
			}
			if sp, ok := p.Value.(*ast.SpreadElement); ok && p.KeyExpr == nil && p.Key == "" && !c.opts.CompatJS() {
				if st := c.TypeOf(sp.Arg); !c.Unanswered(st) && c.invalidSpread(st) {
					c.report(diag.InvalidSpreadType, sp.GetPos())
				}
			}
		}
	case *ast.FunctionDeclaration:
		c.checkParams(n.Params, n)
		c.checkImplicitReturn(n)
		ret = c.declaredReturn(n, n.ReturnType, n.IsAsync || n.IsGenerator)
		if n.Body != nil {
			c.checkNode(n.Body, ret)
		}
		// After the body: reachability types the body's calls, and they must
		// be typed in their own context first (the memo keeps the answer).
		if n.AccessorKind == "get" {
			c.checkGetterReturns(n.Body, namePos(n.NamePos, n.GetPos()))
		}
		c.checkReturnPaths(n, n.ReturnType, n.Body, n.IsAsync, n.IsGenerator)
		return
	case *ast.FunctionExpression:
		c.resolveResult(n)
		c.checkParams(n.Params, n)
		if n.Body != nil {
			c.checkNode(n.Body, c.declaredReturn(n, n.RetType, n.IsAsync || n.IsGenerator))
		}
		c.checkReturnPaths(n, n.RetType, n.Body, n.IsAsync, n.IsGenerator)
		return
	case *ast.ArrowFunction:
		c.resolveResult(n)
		c.checkParams(n.Params, n)
		defer c.checkReturnPaths(n, n.RetType, n.Block, n.IsAsync, false)
		r := c.declaredReturn(n, n.RetType, n.IsAsync)
		if n.Block != nil {
			c.checkNode(n.Block, r)
		} else if n.Body != nil {
			c.checkNode(n.Body, nil)
			if r != nil {
				c.checkAssign(n.Body, r, diag.NotAssignable, n.Body.GetPos())
			}
		}
		return
	case *ast.ClassDeclaration:
		c.checkClassFields(n)
		c.checkDuplicateMembers(n)
		c.checkImplements(n)
		c.checkOverrides(n)
		c.checkOverrideModifiers(n)
		c.checkDerivedSuper(n)
		c.checkAbstractImplemented(n)
		if n.BaseClass != "" && !n.BaseQualified {
			if sym := c.symbolOf(n); sym != nil {
				switch base := lookupName(n.BaseClass, sym.Scope); {
				case base == nil:
					c.cannotFind(n.BaseClass, n.GetPos())
				case !c.opts.CompatJS() && base.Flags&binder.Class != 0 && len(n.BaseTypeArgs) > 0 && !c.classIsGeneric(base):
					// `class B extends A<number>` with A declaring no type
					// parameters.
					c.report(diag.TypeNotGeneric, n.GetPos(), ast.Unmangle(n.BaseClass))
				case !c.opts.CompatJS() && base.Flags&binder.Function != 0 && base.Flags&(binder.Class|binder.Variable|binder.Interface) == 0:
					// A plain function has no construct signature (TS2507);
					// JavaScript extends it as a constructor.
					c.report(diag.NotConstructorType, n.GetPos(), c.typeOfSymbol(base))
				case !c.opts.CompatJS() && base.Flags&binder.Variable != 0 && base.Flags&binder.Class == 0:
					// A value callable but not constructible (`Symbol`, `BigInt`).
					if t := c.typeOfSymbol(base); !c.Unanswered(t) && t.Flags&Object != 0 && t.Flags&Union == 0 && len(t.Calls) > 0 && len(t.Constructs) == 0 {
						c.report(diag.NotConstructorType, n.GetPos(), t)
					} else if !c.Unanswered(t) && t.Flags&Object != 0 && t.Flags&Union == 0 && len(t.Constructs) > 0 && !c.constructsTakeTypeArgs(t.Constructs, len(n.BaseTypeArgs)) {
						// Every construct signature needs a different number
						// of type arguments (`extends Proxy`).
						c.report(diag.BaseTypeArgCount, n.GetPos())
					} else if !c.Unanswered(t) && t.Flags&Union != 0 && len(n.BaseTypeArgs) > 0 && !c.unionConstructsTakeTypeArgs(t, len(n.BaseTypeArgs)) {
						// A union of constructors some of which take another
						// number of type arguments (`Map | Set` given two):
						// no signature of the union does (TS2507).
						c.report(diag.NotConstructorType, n.GetPos(), t)
					}
				}
			}
		}
	case *ast.InterfaceDeclaration:
		c.checkIndexedProperties(n)
		if !c.opts.CompatJS() {
			// noImplicitAny: a method or call signature with no return type.
			for _, m := range n.Members {
				switch sig := m.(type) {
				case *ast.MethodSignature:
					if sig.Type == nil {
						c.report(diag.ReturnImplicitAny, sig.Range.Pos, signatureName(sig.Name))
					}
				case *ast.CallSignature:
					if sig.Type == nil {
						c.report(diag.CallSigReturnImplicitAny, sig.Range.Pos)
					}
				}
			}
		}
	case *ast.EnumDeclaration:
		c.checkEnumMembers(n)
	case *ast.ImportCallExpression:
		if _, lit := n.Specifier.(*ast.StringLiteral); !lit && !c.opts.CompatJS() {
			if st := c.TypeOf(n.Specifier); !c.Unanswered(st) && c.assignableTo(st, c.strT) == no {
				c.report(diag.DynamicImportSpecifier, startOf(n.Specifier), st)
			}
		}
	case *ast.TemplateLiteral:
		// A symbol has no implicit string (TS2731).
		for _, x := range n.Exprs {
			if t := c.TypeOf(x); !c.opts.CompatJS() && !c.Unanswered(t) && c.symbolLike(t) {
				c.report(diag.SymbolToStringImplicit, startOf(x))
			}
		}
	case *ast.AwaitExpression:
		if !c.opts.CompatJS() && c.anyThenable(c.TypeOf(n.Argument)) {
			c.report(diag.BadAwaitOperand, startOf(n.Argument))
		}
	case *ast.SpreadElement:
		// An array literal's or an argument list's spread iterates its
		// operand (an object literal's copies its properties).
		if _, inObj := c.ParentOf(n).(*ast.ObjectLiteral); !inObj {
			c.checkNeverIterated(n.Arg, false)
		}
	case *ast.YieldExpression:
		if !c.opts.CompatJS() && c.yieldResultImplicitAny(n) {
			c.report(diag.YieldImplicitAny, n.GetPos())
		}
		if n.Delegate && n.Argument != nil {
			c.checkNeverIterated(n.Argument, c.inAsyncFunction(n))
		}
	case *ast.TryStatement:
		if cl := n.Catch; cl != nil && cl.ParamType != nil && !c.opts.CompatJS() {
			if k, ok := cl.ParamType.(*ast.KeywordType); !ok || k.Keyword != "any" && k.Keyword != "unknown" {
				c.report(diag.CatchAnnotation, cl.ParamType.GetLoc().Pos)
			}
		}
		// `catch ({ message })`: the caught value is unknown under
		// useUnknownInCatchVariables, which has no properties (TS2339).
		if cl := n.Catch; cl != nil && cl.ObjectPattern != nil && cl.ParamType == nil && !c.CatchVariablesAny && !c.opts.CompatJS() {
			for _, p := range cl.ObjectPattern {
				c.report(diag.PropertyNotExist, cl.Pos, p.Key, c.unknownT)
			}
		}
	case *ast.NewExpression:
		if c.namespaceObjectNew(n) {
			if !c.opts.CompatJS() {
				c.report(diag.NotConstructable, n.GetPos())
			}
			return
		}
		// A bare `new X(…)` the binder did not resolve: a builtin sema did
		// not map (a module-only one never imported) or an undeclared name.
		if n.Callee == nil && !n.Qualified && c.b.NewTarget(n) == nil && !strings.Contains(n.ClassName, ".") {
			c.unresolvedValue(n.ClassName, n, n.GetPos(), c.b.LookupScope(n))
		}
		if sym := c.b.NewTarget(n); sym != nil && sym.Flags&binder.Class != 0 {
			c.checkConstructorAccess(n, sym)
			if cd := firstClassDecl(sym); cd != nil && cd.IsAbstract && !c.opts.CompatJS() {
				c.report(diag.AbstractNew, n.GetPos())
			}
		}
		// A target with call signatures but no construct signature.
		if c.TypeOf(n); c.newNoCtor[n] && !c.opts.CompatJS() {
			c.report(diag.NewImplicitAny, n.GetPos())
		}
		if c.newUntyped[n] && !c.opts.CompatJS() {
			at := n.GetPos()
			if n.Callee != nil {
				at = n.Callee.GetPos()
			}
			c.report(diag.NotConstructable, at)
		}
		if len(n.TypeArgs) > 0 && !c.newTypeArgArityError(n) {
			if ctor := c.newCtor(n); ctor != nil {
				c.typeArgConstraintError(n.TypeArgs, len(n.Args), ctor)
			}
		}
		// `new X(…)` of a class, or through a value's construct signatures
		// (`declare var DataView: DataViewConstructor`), is checked as the
		// call it is.
		if n.Callee == nil && !n.Qualified && len(n.TypeArgs) == 0 {
			if sym := c.b.NewTarget(n); sym != nil && (sym.Flags&binder.Class != 0 || sym.Flags&binder.Variable != 0) {
				if ctor := c.newCtor(n); ctor != nil && !c.hasSpread(n.Args) {
					fake := ast.NewCallExpression(ast.NewIdentifier(n.ClassName, n.GetPos()), n.Args, n.GetPos())
					c.checkSignatureCall(fake, ctor)
				}
			}
		}
	case *ast.NewTypedArrayExpression:
		// Checked as the call it is, against the declared constructor's
		// signatures (`Uint8ArrayConstructor`).
		if name, args, _, _ := builtinNewParts(n); name != "" && !c.hasSpread(args) {
			if ctor := c.builtinCtor(name); ctor != nil {
				c.checkSignatureCall(ast.NewCallExpression(ast.NewIdentifier(name, n.GetPos()), args, n.GetPos()), ctor)
			}
		}
	case *ast.NewDateExpression, *ast.NewMapExpression, *ast.NewSetExpression, *ast.NewWeakMapExpression, *ast.NewWeakSetExpression:
		if name, args, targs, _ := builtinNewParts(n); len(targs) > 0 {
			if ctor := c.builtinCtor(name); ctor != nil {
				c.typeArgConstraintError(targs, len(args), ctor)
			}
		} else if _, isDate := n.(*ast.NewDateExpression); !isDate && len(args) > 0 && !c.hasSpread(args) {
			// A collection's seed is checked as the call it is
			// (`new Map([["a", 1], [2, "x"]])` is TS2769).
			if ctor := c.builtinCtor(name); ctor != nil {
				c.checkSignatureCall(ast.NewCallExpression(ast.NewIdentifier(name, n.GetPos()), args, n.GetPos()), ctor)
			}
		}
	case *ast.ForInStatement:
		if n.Kind == "var" {
			c.checkVarRedeclared(n, n.VarName, n.GetPos())
		}
	case *ast.VarDeclaration:
		if n.Ambient && n.TypeAnnot == nil && n.Init == nil && n.Name != "" && !c.opts.CompatJS() && !c.ImplicitAnyVariables && n.GetPos().Line > 0 {
			// noImplicitAny: an ambient variable with no type is any.
			c.report(diag.VarImplicitAny, namePos(n.NamePos, n.GetPos()), unqualified(sourceName(n.Name)))
		}
		if n.Kind == "var" && (n.TypeAnnot != nil || n.Init != nil) {
			pos := n.NamePos
			if pos.Line == 0 {
				pos = n.GetPos()
			}
			c.checkVarRedeclared(n, n.Name, pos)
		}
		if c.opts.NoAny && n.TypeAnnot == nil && n.Name != "" {
			// A binding initialized to null or undefined is TypeScript's
			// auto-typed `any`, whatever its later stores evolve it to.
			_, null := n.Init.(*ast.NullLiteral)
			undef := false
			if id, ok := n.Init.(*ast.Identifier); ok && id.Name == "undefined" {
				undef = true
			}
			if sym := c.symbolOf(n); sym != nil && (n.Kind != "const" && (null || undef) || c.typeOfSymbol(sym).Flags&Any != 0) {
				pos := n.NamePos
				if pos.Line == 0 {
					pos = n.GetPos()
				}
				c.report(diag.NoAnyInferred, pos, n.Name)
			}
		}
		if n.TypeAnnot != nil && n.Init != nil {
			if sym := c.symbolOf(n); sym != nil {
				pos := n.NamePos
				if pos.Line == 0 {
					pos = n.GetPos()
				}
				c.checkAssign(n.Init, c.typeOfSymbol(sym), diag.NotAssignable, pos)
			}
		}
	case *ast.AssignmentExpression:
		if lit, ok := n.Left.(*ast.ArrayLiteral); ok && n.Op == "=" {
			c.checkAssignArrayPattern(lit, n.Right, nil)
		}
		if lit, ok := n.Left.(*ast.ObjectLiteral); ok && n.Op == "=" {
			if _, srcLit := n.Right.(*ast.ObjectLiteral); !srcLit {
				c.checkPatternProps(assignPatternProps(lit), c.TypeOf(n.Right), lit.GetPos())
			} else if !c.opts.CompatJS() {
				// As a declaration's literal initializer (undefaultedProps).
				c.checkPatternProps(undefaultedProps(assignPatternProps(lit)), c.TypeOf(n.Right), lit.GetPos())
			}
		}
		if c.checkTarget(n.Left) && n.Op == "=" {
			switch n.Left.(type) {
			case *ast.Identifier, *ast.MemberExpression, *ast.IndexExpression:
				target := c.writeType(n.Left)
				if _, id := n.Left.(*ast.Identifier); id && compoundLike(n) {
					target = widen(c, target) // `x = x + 1` assigns to x's base type
				}
				c.checkAssign(n.Right, target, diag.NotAssignable, n.Left.GetPos())
			}
		} else if op := strings.TrimSuffix(n.Op, "="); op != n.Op && op != "&&" && op != "||" && op != "??" {
			c.checkCompound(op, n.Left, n.Right)
		}
	case *ast.UpdateExpression:
		if c.checkTarget(n.Arg) {
			if c.checkNonNull(n.Arg) != nil {
				// `++x` takes a number, a bigint, an enum or any (TS2356).
				if t := c.TypeOf(n.Arg); !c.Unanswered(t) && plainOperand(t) && !arithmetic(t) {
					c.report(diag.ArithmeticOperand, n.Arg.GetPos())
				}
			}
		}
	case *ast.UnaryExpression:
		switch n.Op {
		case "-", "+", "~":
			if _, lit := n.Arg.(*ast.NumberLiteral); !lit {
				c.checkNonNull(n.Arg)
			}
		case "delete":
			if !c.opts.CompatJS() {
				c.checkDeleteOperand(n.Arg)
			}
		}
	case *ast.IndexExpression:
		if kt := c.TypeOf(n.Index); c.invalidIndexKey(kt) {
			if !c.opts.CompatJS() { // JavaScript converts it (ToPropertyKey)
				c.report(diag.InvalidIndexType, n.Index.GetPos(), kt)
			}
			break
		}
		if lit, ok := n.Index.(*ast.NumberLiteral); ok && !c.opts.CompatJS() {
			// A fixed-length tuple read past its end (TS2493).
			if ot := c.TypeOf(n.Object); !c.Unanswered(ot) && ot.Flags&Object != 0 && ot.Flags&Union == 0 && ot.Kind == Tuple {
				if i, err := strconv.Atoi(lit.Value); err == nil && i >= len(ot.Elems) {
					c.report(diag.TupleNoElement, n.Index.GetPos(), ot, len(ot.Elems), i)
				}
			}
		}
		if !n.Optional {
			c.checkReceiver(n.Object)
		}
		c.checkIndexKey(n)
	case *ast.ReturnStatement:
		// A module-level return is CommonJS; TypeScript rejects it (the js
		// lane takes the CommonJS meaning).
		if prog, ok := c.b.Module.Node.(*ast.Program); ok && !c.opts.CompatJS() {
			if _, top := prog.TopLevelReturns[n]; top {
				c.report(diag.ReturnOutsideFunction, n.GetPos())
			}
		}
		if ret != nil && n.Value != nil {
			c.checkAssign(n.Value, ret, diag.NotAssignable, n.GetPos())
		}
	case *ast.ForOfStatement:
		// tsc's checkForOfStatement: the iterable is checkNonNullExpression'd.
		c.checkNonNull(n.Iterable)
		if !n.Await {
			c.checkIterable(n.Iterable)
		}
		c.checkNeverIterated(n.Iterable, n.Await)
		if n.ArrayPattern != nil {
			// (`for await` over an array: each element awaited, which a
			// null or undefined element stays.)
			if it := c.TypeOf(n.Iterable); !c.Unanswered(it) && (!n.Await || it.Flags&Object != 0 && it.Kind == Array) {
				if el := c.iteratedType(it); el != nil && !c.Unanswered(el) {
					c.checkArrayPattern(n.ArrayPattern, nil, el, n.GetPos())
				}
			}
		}
		if n.Kind == "var" && n.ArrayPattern == nil && n.ObjectPattern == nil {
			c.checkVarRedeclared(n, n.VarName, n.GetPos())
		}
	case *ast.ObjectDestructuring:
		if st := c.patternAnnotation(n.TypeAnnot); st != nil {
			// An empty pattern checks its initializer is not nullish, not
			// that it fits (tsc's needCheckWidenedType).
			if n.Init != nil && !c.Unanswered(st) && len(n.Props) > 0 {
				c.checkAssign(n.Init, st, diag.NotAssignable, n.GetPos())
			}
			c.checkPatternProps(n.Props, st, n.GetPos())
		} else if _, lit := n.Init.(*ast.ObjectLiteral); !lit && n.Init != nil {
			c.checkPatternProps(n.Props, c.TypeOf(n.Init), n.GetPos())
		} else if lit && !c.opts.CompatJS() {
			// An object literal is contextually typed by the pattern: a
			// property with a default is implied, one without must exist.
			c.checkPatternProps(undefaultedProps(n.Props), c.TypeOf(n.Init), n.GetPos())
		}
	case *ast.ArrayDestructuring:
		if st := c.patternAnnotation(n.TypeAnnot); st != nil {
			if n.Init != nil && !c.Unanswered(st) && len(n.Elems) > 0 {
				c.checkAssign(n.Init, st, diag.NotAssignable, n.GetPos())
			}
			c.checkArrayPattern(n.Elems, nil, st, n.GetPos())
		} else if it := c.TypeOf(n.Init); n.Init != nil && !c.opts.CompatJS() && it != nil && it.Flags&Unknown != 0 {
			// `unknown` has no iterator (a strict catch variable's).
			c.report(diag.NotIterable, n.Init.GetPos(), it)
		} else {
			c.checkArrayPattern(n.Elems, n.Init, nil, n.GetPos())
		}
	case *ast.MemberExpression:
		c.checkProperty(n)
		c.checkAccessibility(n)
	case *ast.CallExpression:
		if c.namespaceObjectRef(n.Callee) {
			if !c.opts.CompatJS() {
				c.report(diag.NotCallable, n.Callee.GetPos())
			}
			return
		}
		if _, super := n.Callee.(*ast.SuperExpression); super && len(n.TypeArgs) > 0 {
			c.report(diag.SuperTypeArgs, n.Callee.GetPos())
			return // tsc resolves nothing further of `super<T>(…)`
		}
		c.checkCall(n)
	case *ast.TaggedTemplateExpression:
		c.checkTaggedTemplate(n)
	case *ast.BinaryExpression:
		c.checkOperator(n)
	case *ast.Identifier:
		// Every reference resolves, whether or not anything asks its type.
		if sym, _ := c.b.Resolve(n); sym == nil {
			c.unresolvedValue(n.Name, n, n.GetPos(), c.b.LookupScope(n))
		} else if evolvingDecl(sym) && !c.implicitAnyReported[n] {
			c.TypeOf(n) // a read in another function is TS7005
		} else if !c.opts.CompatJS() && c.unassignedVar(n, sym) {
			c.report(diag.UsedBeforeAssigned, n.GetPos(), sourceName(n.Name))
		}
	}
	ast.ForEachChild(n, func(ch ast.Node) bool {
		c.checkNode(ch, ret)
		return true
	})
}

// checkIndexedProperties reports each property an interface declares whose
// type does not fit its string index signature (TS2411); an optional one is
// T | undefined.
func (c *Checker) checkIndexedProperties(n *ast.InterfaceDeclaration) {
	if len(n.TypeParameters) > 0 {
		return
	}
	sym := c.symbolOf(n)
	if sym == nil {
		return
	}
	t := c.interfaceOf(sym, nil)
	if t == nil || c.Unanswered(t) || t.StringIndex == nil || c.Unanswered(t.StringIndex) {
		return
	}
	for _, m := range n.Members {
		ps, ok := m.(*ast.PropertySignature)
		if !ok || ps.Computed {
			continue
		}
		for _, p := range t.Props {
			if p.Name != ps.Name || p.Type == nil || c.Unanswered(p.Type) {
				continue
			}
			pt := p.Type
			if p.Optional {
				pt = c.in.union(pt, c.undefinedT)
			}
			if c.assignableTo(pt, t.StringIndex) == no {
				c.report(diag.PropertyNotIndex, ps.GetPos(), ps.Name, pt, t.StringIndex)
			}
		}
	}
}

// declaredReturn is the annotated return type of fn, or nil when it has
// none, is async or a generator (its annotation names a Promise or a
// Generator), or declares a type predicate.
func (c *Checker) declaredReturn(fn ast.Node, ta ast.TypeNode, wrapped bool) *Type {
	if ta == nil || wrapped {
		return nil
	}
	if _, pred := ta.(*ast.TypePredicate); pred {
		return nil
	}
	scope := c.b.ScopeOf(fn)
	if scope == nil {
		scope = c.b.Module
	}
	t := c.typeFromNode(ta, scope)
	if c.Unanswered(t) {
		return nil
	}
	return t
}

// checkReturnPaths is tsc's checkAllCodePathsInNonVoidFunctionReturnOrThrow
// for a function with a return annotation and a block body: one that can
// complete without a value must allow undefined. A generator's annotation
// must be an iterator type (the yields are what it produces).
func (c *Checker) checkReturnPaths(fn ast.Node, ta ast.TypeNode, body *ast.BlockStatement, async, generator bool) {
	if c.opts.CompatJS() || ta == nil || body == nil {
		return
	}
	if _, pred := ta.(*ast.TypePredicate); pred {
		return
	}
	// Resolve the annotation only when the answer is needed: a generator's
	// named iterator type needs no resolving, and a body whose end is
	// unreachable needs no return type. (Resolving every annotation also
	// expands types the rest of the check never asks for.)
	if generator && iteratorAnnotation(ta) || !generator && !c.endReachable(body) {
		return
	}
	scope := c.b.ScopeOf(fn)
	if scope == nil {
		scope = c.b.Module
	}
	t := c.typeFromNode(ta, scope)
	if c.Unanswered(t) {
		return
	}
	pos := ta.GetPos()
	if generator {
		if t.Flags&Void != 0 && t.Flags&Union == 0 {
			c.report(diag.GeneratorVoidAnnotation, pos)
			return
		}
		if !c.generatorFits(t, async) {
			gen := "Generator<any, any, unknown>"
			if async {
				gen = "AsyncGenerator<any, any, unknown>"
			}
			c.report(diag.NotAssignable, pos, gen, t)
		}
		return
	}
	if async {
		t = c.awaited(t, 0)
		if c.Unanswered(t) {
			return
		}
	}
	if t.Flags&(Any|Undefined) != 0 || c.maybeVoid(t) || !c.endReachable(body) {
		return
	}
	switch {
	case t.Flags&Never != 0:
		c.report(diag.NeverReachableEnd, pos)
	case !hasReturn(body):
		c.report(diag.MustReturnValue, pos)
	case !c.assignable(c.undefinedT, t):
		c.report(diag.LacksEndingReturn, pos)
	}
}

// checkGetterReturns is tsc's TS2378: a getter whose end is reachable and
// that has no return statement at all.
func (c *Checker) checkGetterReturns(body *ast.BlockStatement, pos ast.Pos) {
	if c.opts.CompatJS() || body == nil || hasReturn(body) || !c.endReachable(body) {
		return
	}
	c.report(diag.GetterMustReturn, pos)
}

// iteratorAnnotation reports n as a generator annotation tsc accepts by its
// name alone: an iterator type of the standard library, any or unknown.
func iteratorAnnotation(n ast.TypeNode) bool {
	switch t := n.(type) {
	case *ast.KeywordType:
		return t.Keyword == "any" || t.Keyword == "unknown"
	case *ast.TypeReference:
		if len(t.Qualifier) > 0 {
			return false
		}
		switch t.Name {
		case "Generator", "AsyncGenerator", "Iterable", "IterableIterator", "Iterator",
			"AsyncIterable", "AsyncIterableIterator", "AsyncIterator", "IteratorObject", "AsyncIteratorObject":
			return true
		}
	}
	return false
}

// maybeVoid reports t as void or a union with a void member (tsc's
// maybeTypeOfKind(t, Void)).
func (c *Checker) maybeVoid(t *Type) bool {
	if t.Flags&Void != 0 {
		return true
	}
	if t.Flags&Union != 0 {
		for _, m := range t.Types {
			if m.Flags&Void != 0 {
				return true
			}
		}
	}
	return false
}

// checkParams checks each annotated parameter's default value.
func (c *Checker) checkParams(params []ast.Param, fn ast.Node) {
	scope := c.b.ScopeOf(fn)
	if scope == nil {
		scope = c.b.Module
	}
	for _, p := range params {
		if p.ArrayPattern != nil && p.Type != nil {
			c.checkArrayPattern(p.ArrayPattern, nil, c.typeFromNode(p.Type, scope), p.Pos)
		}
	}
	if c.privateInAmbient(fn) {
		return // tsc reports no implicit any on an ambient class's private member
	}
	for i, p := range params {
		if p.Type == nil && p.Default == nil && p.ArrayPattern == nil && p.ObjectPattern == nil &&
			p.Name != "this" && p.Pos.Line > 0 && !c.opts.CompatJS() && !c.contextuallyTyped(fn, i) && !c.setterOfGetter(fn) {
			// noImplicitAny (TS7006, TS7019 for a rest).
			if p.Rest {
				c.report(diag.RestParamImplicitAny, p.Pos, p.Name)
			} else {
				c.report(diag.ParamImplicitAny, p.Pos, p.Name)
			}
		}
		if p.Type != nil && p.ObjectPattern != nil {
			ps := scope
			if s := c.b.ScopeOf(fn); s != nil {
				ps = s
			}
			c.checkPatternProps(p.ObjectPattern, c.typeFromNode(p.Type, ps), p.Pos)
		}
		if p.Type == nil && p.Default == nil && (p.ArrayPattern != nil || p.ObjectPattern != nil) &&
			p.Pos.Line > 0 && !c.opts.CompatJS() && !c.contextuallyTyped(fn, i) {
			// noImplicitAny on a destructuring parameter: each binding
			// element without a default (TS7031).
			c.reportImplicitAnyLeaves(p.ArrayPattern, p.ObjectPattern, p.Pos)
		}
		if p.Default != nil && p.Type == nil && (p.ArrayPattern != nil || p.ObjectPattern != nil) && p.Pos.Line > 0 && !c.opts.CompatJS() {
			c.checkNullishPatternDefault(p)
			// A computed key on the default's type (a named key is no
			// error: the pattern types the default's literal).
			if dt := c.TypeOf(p.Default); !c.Unanswered(dt) && dt.Flags&Object != 0 && dt.Flags&Union == 0 {
				for _, d := range p.ObjectPattern {
					if d.KeyExpr != nil && !d.Rest {
						at := p.Pos
						if d.Pos.Line > 0 {
							at = d.Pos
						}
						c.checkPatternIndexKey(d.KeyExpr, dt, at)
					}
				}
			}
		}
		if p.Default == nil {
			continue
		}
		if p.Type == nil {
			// An unannotated default is checked as it is written, called or
			// not (its names, its own expression errors).
			c.checkExpr(p.Default)
			continue
		}
		c.checkAssign(p.Default, c.typeFromNode(p.Type, scope), diag.NotAssignable, p.Default.GetPos())
	}
}

// checkNullishPatternDefault reports an unannotated destructuring
// parameter whose default is `null` or `undefined`, the parameter's type:
// an array pattern reads no iterator (TS2488), an empty pattern destructures
// a possibly-absent object (TS2531/TS2532), and each named key is no
// property of it (TS2339).
func (c *Checker) checkNullishPatternDefault(p ast.Param) {
	dt := c.TypeOf(p.Default)
	if lit, ok := p.Default.(*ast.ArrayLiteral); ok && p.ArrayPattern != nil {
		c.checkNullishPatternElems(p.ArrayPattern, lit, p.Pos)
	}
	if c.Unanswered(dt) || dt.Flags&Union != 0 || dt.Flags&(Null|Undefined) == 0 {
		return
	}
	absent := diag.ObjectPossiblyNull
	if dt.Flags&Undefined != 0 {
		absent = diag.ObjectPossiblyUndef
	}
	if p.ArrayPattern != nil {
		c.report(diag.NotIterable, p.Pos, dt)
		if len(p.ArrayPattern) == 0 {
			c.report(absent, p.Pos)
		}
		return
	}
	if len(p.ObjectPattern) == 0 {
		c.report(absent, p.Pos)
		return
	}
	for _, d := range p.ObjectPattern {
		if d.Rest || d.KeyExpr != nil || d.Key == "" {
			continue
		}
		at := p.Pos
		if d.Pos.Line > 0 {
			at = d.Pos
		}
		c.report(diag.PropertyNotExist, at, d.Key, dt)
	}
}

// checkNullishPatternElems reports each nested pattern of an array pattern
// with no default of its own whose element of the literal default lit is
// `null` or `undefined` (`([[x]] = [null])`): an array pattern reads no
// iterator (TS2488), an object pattern's keys are no property (TS2339).
func (c *Checker) checkNullishPatternElems(elems []ast.ArrayPatternElem, lit *ast.ArrayLiteral, pos ast.Pos) {
	for i, e := range elems {
		if e.Rest || i >= len(lit.Elements) {
			return
		}
		if e.Default != nil || e.SubArray == nil && e.SubObject == nil || lit.Elements[i] == nil {
			continue
		}
		at := pos
		if e.Pos.Line > 0 {
			at = e.Pos
		}
		if sub, ok := lit.Elements[i].(*ast.ArrayLiteral); ok && e.SubArray != nil {
			c.checkNullishPatternElems(e.SubArray, sub, at)
			continue
		}
		et := c.TypeOf(lit.Elements[i])
		if c.Unanswered(et) || et.Flags&Union != 0 || et.Flags&(Null|Undefined) == 0 {
			continue
		}
		if e.SubArray != nil {
			c.report(diag.NotIterable, at, et)
			continue
		}
		for _, d := range e.SubObject {
			if d.Rest || d.KeyExpr != nil || d.Key == "" {
				continue
			}
			dat := at
			if d.Pos.Line > 0 {
				dat = d.Pos
			}
			c.report(diag.PropertyNotExist, dat, d.Key, et)
		}
	}
}

// reportImplicitAnyLeaves reports TS7031 for each binding element of an
// unannotated destructuring parameter that has no default of its own.
func (c *Checker) reportImplicitAnyLeaves(elems []ast.ArrayPatternElem, props []ast.DestructProp, pos ast.Pos) {
	for _, e := range elems {
		switch {
		case (e.SubArray != nil || e.SubObject != nil) && e.Default != nil:
			// A defaulted sub-pattern takes its leaves' types from the
			// default: only the positions an array literal default leaves
			// out are implicitly any (`[[a, b] = [1]]`: b).
			if lit, ok := e.Default.(*ast.ArrayLiteral); ok && e.SubArray != nil && !hasSpread(lit) && len(e.SubArray) > len(lit.Elements) {
				c.reportImplicitAnyLeaves(e.SubArray[len(lit.Elements):], nil, pos)
			}
			if e.SubObject != nil {
				// (`[{ k } = {}]`: k is no property of the default's type.)
				c.checkPatternProps(e.SubObject, c.TypeOf(e.Default), pos)
			}
		case e.SubArray != nil || e.SubObject != nil:
			c.reportImplicitAnyLeaves(e.SubArray, e.SubObject, pos)
		case e.Name != "" && e.Default == nil && !e.Rest:
			c.report(diag.BindingImplicitAny, pos, e.Name)
		}
	}
	for _, p := range props {
		switch {
		case (p.SubArray != nil || p.SubObject != nil) && p.Default != nil:
			if lit, ok := p.Default.(*ast.ArrayLiteral); ok && p.SubArray != nil && !hasSpread(lit) && len(p.SubArray) > len(lit.Elements) {
				c.reportImplicitAnyLeaves(p.SubArray[len(lit.Elements):], nil, pos)
			}
		case p.SubArray != nil || p.SubObject != nil:
			c.reportImplicitAnyLeaves(p.SubArray, p.SubObject, pos)
		case p.Local != "" && p.Default == nil && !p.Rest:
			c.report(diag.BindingImplicitAny, pos, p.Local)
		}
	}
}

// yieldResultImplicitAny reports tsc's TS7057: a `yield` whose value is
// used, in a generator with neither a declared return type nor a contextual
// one, implicitly results in any.
func (c *Checker) yieldResultImplicitAny(y *ast.YieldExpression) bool {
	if y.Delegate {
		return false // `yield*` results in its delegate's return type
	}
	// A context types it (`const v: string = yield`, `f<string>(yield)`).
	if ct := c.contextualType(y); ct != nil && !c.Unanswered(ct) && ct.Flags&Any == 0 {
		return false
	}
	switch p := c.parentOf(y).(type) {
	case *ast.ObjectLiteral, *ast.ClassDeclaration, *ast.ImportCallExpression,
		*ast.ArrayDestructuring, *ast.ObjectDestructuring:
		// A computed key, a decorator, an `import()` specifier, a binding
		// pattern: positions tsc types by context this checker does not
		// model yet.
		return false
	case *ast.VarDeclaration:
		if p.TypeAnnot != nil {
			return false // annotated, though its type may not be answered (`T`)
		}
	case *ast.ExpressionStatement:
		return false
	case *ast.SequenceExpression:
		if len(p.Exprs) > 0 && p.Exprs[len(p.Exprs)-1] != ast.Expression(y) {
			return false
		}
	case *ast.UnaryExpression:
		if p.Op == "void" {
			return false
		}
	case *ast.ForStatement:
		for _, u := range p.Update {
			if u == ast.Expression(y) {
				return false // an update's value is never used
			}
		}
	}
	for n := c.parentOf(y); n != nil; n = c.parentOf(n) {
		switch f := n.(type) {
		case *ast.FunctionDeclaration:
			return f.ReturnType == nil
		case *ast.FunctionExpression:
			if f.RetType != nil {
				return false
			}
			ct := c.contextualType(f)
			return ct == nil || c.Unanswered(ct)
		case *ast.ArrowFunction:
			return false
		}
	}
	return false
}

// setterOfGetter reports whether fn is a set accessor whose class declares a
// get accessor of the same name: its parameter has the getter's type.
func (c *Checker) setterOfGetter(fn ast.Node) bool {
	fd, ok := fn.(*ast.FunctionDeclaration)
	if !ok || fd.AccessorKind != "set" {
		return false
	}
	for n := c.parentOf(fn); n != nil; n = c.parentOf(n) {
		cd, ok := n.(*ast.ClassDeclaration)
		if !ok {
			continue
		}
		for _, m := range cd.Methods {
			if m.AccessorKind == "get" && m.Name == fd.Name && m.IsStatic == fd.IsStatic {
				return true
			}
		}
		return false
	}
	return false
}

// checkOverrides reports a member of a derived class that is not assignable
// to the base class's member of the same name (TS2416); methods compare
// bivariantly, as relate does. Private members, accessors and generic
// classes are left alone.
func (c *Checker) checkOverrides(cd *ast.ClassDeclaration) {
	if cd.BaseClass == "" || len(cd.DeclaredTypeParams().Names) > 0 {
		return
	}
	sym := c.symbolOf(cd)
	if sym == nil {
		return
	}
	inst, base := c.instanceType(sym), c.baseInstance(cd, sym)
	if inst == nil || base == nil || c.Unanswered(inst) || c.Unanswered(base) {
		return
	}
	for _, p := range inst.Props {
		if p.Owner != sym || p.Visibility == "private" || p.Accessor {
			continue
		}
		at := ast.Pos{}
		if p.Decl != nil {
			at = p.Decl.GetPos()
		} else {
			// A field: its name's position.
			for _, f := range cd.Fields {
				if f.Name == p.Name && !f.Static && f.Pos.Line > 0 {
					at = f.Pos
				}
			}
		}
		if at.Line == 0 {
			continue
		}
		bp := base.Prop(p.Name)
		if bp == nil || bp.Visibility == "private" || bp.Accessor {
			continue
		}
		if c.Unanswered(p.Type) || c.Unanswered(bp.Type) || hasTypeParam(p.Type) || hasTypeParam(bp.Type) {
			continue
		}
		if !c.assignable(p.Type, bp.Type) {
			c.report(diag.OverrideNotAssignable, at, p.Name, sourceName(cd.Name), base)
		}
	}
}

// checkImplements reports a class whose instance type is not assignable to
// an interface (or class, or object type alias) it implements (TS2420).
func (c *Checker) checkImplements(cd *ast.ClassDeclaration) {
	if len(cd.Implements) == 0 || len(cd.DeclaredTypeParams().Names) > 0 {
		return
	}
	sym := c.symbolOf(cd)
	if sym == nil {
		return
	}
	inst := c.instanceType(sym)
	if inst == nil || c.Unanswered(inst) {
		return
	}
	for _, name := range cd.Implements {
		if strings.ContainsAny(name, "<.") {
			continue // a generic or qualified one: its arguments are not kept
		}
		isym := resolveTypeName(name, sym.Scope)
		if isym == nil {
			continue
		}
		var target *Type
		switch {
		case isym.Flags&binder.Interface != 0:
			target = c.interfaceOf(isym, nil)
		case isym.Flags&binder.Class != 0:
			target = c.instanceOf(isym, nil)
		case isym.Flags&binder.TypeAlias != 0:
			target = c.aliasOf(isym, nil)
		}
		if target == nil || c.Unanswered(target) || hasTypeParam(target) {
			continue
		}
		if !c.assignable(inst, target) {
			c.report(diag.IncorrectlyImplements, cd.GetPos(), sourceName(cd.Name), sourceName(name))
		}
	}
}

// checkOverrideModifiers reports a member written `override` in a class
// without a base (TS4112) or whose base does not declare it (TS4113): an
// instance member against the base's instance type, a static one against
// the base's constructor.
func (c *Checker) checkOverrideModifiers(cd *ast.ClassDeclaration) {
	type member struct {
		name   string
		pos    ast.Pos
		static bool
	}
	var marked []member
	for _, m := range cd.Methods {
		if m.IsOverride {
			marked = append(marked, member{m.Name, m.GetPos(), m.IsStatic})
		}
	}
	for _, f := range cd.Fields {
		if f.Override {
			marked = append(marked, member{f.Name, f.Pos, f.Static})
		}
	}
	if len(marked) == 0 {
		return
	}
	if cd.BaseClass == "" {
		for _, m := range marked {
			c.report(diag.OverrideNoBase, m.pos, sourceName(cd.Name))
		}
		return
	}
	sym := c.symbolOf(cd)
	if sym == nil {
		return
	}
	base := c.baseInstance(cd, sym)
	var statics *Type
	if vsym := lookupName(cd.BaseClass, sym.Scope); vsym != nil {
		statics = c.typeOfSymbol(vsym)
	} else if vsym := c.builtinImport(cd.BaseClass); vsym != nil {
		statics = c.typeOfSymbol(vsym)
	}
	for _, m := range marked {
		side := base
		if m.static {
			side = statics
		}
		if side == nil || c.Unanswered(side) {
			continue
		}
		if side.Prop(m.name) == nil {
			c.report(diag.OverrideNotInBase, m.pos, sourceName(cd.BaseClass))
		}
	}
}

// checkDerivedSuper reports a derived class's constructor that never calls
// super() (TS2377).
func (c *Checker) checkDerivedSuper(cd *ast.ClassDeclaration) {
	if cd.BaseClass == "" || cd.Constructor == nil || cd.Constructor.Body == nil {
		return
	}
	found := false
	ast.Inspect(cd.Constructor.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		case *ast.CallExpression:
			if _, ok := n.Callee.(*ast.SuperExpression); ok {
				found = true
			}
		}
		return !found
	})
	if !found {
		c.report(diag.DerivedNoSuper, cd.Constructor.GetPos())
	}
}

// ctorAssigned is the type cd's constructor assigns to this.name, or
// unanswered when it assigns none.
func (c *Checker) ctorAssigned(cd *ast.ClassDeclaration, name string) *Type {
	if cd.Constructor == nil || cd.Constructor.Body == nil {
		return c.unanswered
	}
	for _, p := range c.constructorFields(cd) {
		if p.Name == name {
			return p.Type
		}
	}
	return c.unanswered
}

// checkDuplicateMembers reports a class member declared twice under one
// name (instance and static members apart): a later field, or a field or
// accessor beside another member, is TS2300 there; two method
// implementations are TS2393 at each. A getter and a setter make one
// accessor. Private names are the parser's early error; JavaScript allows
// the rest, so the -compat=js lane does too.
func (c *Checker) checkDuplicateMembers(cd *ast.ClassDeclaration) {
	if c.opts.CompatJS() {
		return
	}
	type entry struct {
		name   string
		static bool
		kind   string // "field", "method", "get", "set"
		pos    ast.Pos
	}
	var es []entry
	for _, f := range cd.Fields {
		if f.Key == nil && f.Pos.Line > 0 && !strings.HasPrefix(f.Name, "#") {
			es = append(es, entry{f.Name, f.Static, "field", f.Pos})
		}
	}
	for _, m := range cd.Methods {
		if m.Key != nil || m.Body == nil || strings.HasPrefix(m.Name, "#") {
			continue
		}
		pos := m.NamePos
		if pos.Line == 0 {
			pos = m.GetPos()
		}
		kind := "method"
		if m.AccessorKind != "" {
			kind = m.AccessorKind
		}
		es = append(es, entry{m.Name, m.IsStatic, kind, pos})
	}
	sort.SliceStable(es, func(i, j int) bool {
		a, b := es[i].pos, es[j].pos
		return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col
	})
	type member struct {
		kind string // as entry's, or "accessor" for a get/set pair
		pos  ast.Pos
		done bool // its TS2393 is reported
	}
	seen := map[bool]map[string]*member{false: {}, true: {}}
	for _, x := range es {
		prev, dup := seen[x.static][x.name]
		switch {
		case !dup:
			seen[x.static][x.name] = &member{kind: x.kind, pos: x.pos}
		case prev.kind == "get" && x.kind == "set" || prev.kind == "set" && x.kind == "get":
			prev.kind = "accessor"
		case prev.kind == "method" && x.kind == "method":
			if !prev.done {
				c.report(diag.DuplicateFunctionImpl, prev.pos, sourceName(x.name))
				prev.done = true
			}
			c.report(diag.DuplicateFunctionImpl, x.pos, sourceName(x.name))
		default:
			c.report(diag.DuplicateIdentifier, x.pos, sourceName(x.name))
		}
	}
}

// checkClassFields checks each annotated field's initializer.
// thisAssigns reports whether cd's constructor definitely assigns this.name
// (a parameter property counts): a statement of its body, or both arms of
// an if, assigns it. Assignments behind a call are not followed, as in tsc.
func thisAssigns(cd *ast.ClassDeclaration, name string) bool {
	ctor := cd.Constructor
	if ctor == nil || ctor.Body == nil {
		return false
	}
	for _, p := range ctor.Params {
		if p.IsPropertyParam() && p.Name == name {
			return true
		}
	}
	return stmtsAssignThis(ctor.Body.Body, name)
}

func stmtsAssignThis(sts []ast.Statement, name string) bool {
	for _, st := range sts {
		if stmtAssignsThis(st, name) {
			return true
		}
	}
	return false
}

func stmtAssignsThis(st ast.Statement, name string) bool {
	switch x := st.(type) {
	case *ast.BlockStatement:
		return stmtsAssignThis(x.Body, name)
	case *ast.IfStatement:
		return x.Alternate != nil && stmtAssignsThis(x.Consequent, name) && stmtAssignsThis(x.Alternate, name)
	case *ast.TryStatement:
		// The try block's assignment holds when every catch path assigns
		// too or exits (a rethrow).
		if x.Finally != nil && stmtsAssignThis(x.Finally.Body, name) {
			return true
		}
		if !stmtsAssignThis(x.Body.Body, name) {
			return false
		}
		if x.Catch == nil || x.Catch.Body == nil {
			return true
		}
		cb := x.Catch.Body.Body
		return stmtsAssignThis(cb, name) || len(cb) > 0 && endsInThrow(cb[len(cb)-1])
	case *ast.ExpressionStatement:
		return exprAssignsThis(x.Expr, name)
	case *ast.ReturnStatement:
		// (`return (this.self = f(this))`)
		return x.Value != nil && exprAssignsThis(x.Value, name)
	}
	return false
}

// exprAssignsThis reports an assignment in e whose target, or a target
// it destructures into (`({ key: this.x } = arg)`), is `this.name`.
func exprAssignsThis(e ast.Expression, name string) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch y := n.(type) {
		case *ast.FunctionExpression, *ast.ArrowFunction, *ast.ConditionalExpression:
			return false
		case *ast.AssignmentExpression:
			ast.Inspect(y.Left, func(t ast.Node) bool {
				if m, ok := t.(*ast.MemberExpression); ok {
					// A member target is itself the target: `this.x.y = 1`
					// assigns y of this.x, not x.
					if _, this := m.Object.(*ast.ThisExpression); this && m.Property == name {
						found = true
					}
					return false
				}
				return !found
			})
		}
		return !found
	})
	return found
}

// endsInThrow reports whether st always ends in a throw.
func endsInThrow(st ast.Statement) bool {
	switch x := st.(type) {
	case *ast.ThrowStatement:
		return true
	case *ast.BlockStatement:
		return len(x.Body) > 0 && endsInThrow(x.Body[len(x.Body)-1])
	case *ast.IfStatement:
		return x.Alternate != nil && endsInThrow(x.Consequent) && endsInThrow(x.Alternate)
	}
	return false
}

// admitsUndefined reports whether undefined is a value of t (any, unknown,
// a union with undefined or void).
func (c *Checker) admitsUndefined(t *Type) bool {
	if t.Flags&(Any|Unknown|Undefined) != 0 {
		return true
	}
	if t.Flags&Union != 0 {
		for _, m := range t.Types {
			if c.admitsUndefined(m) {
				return true
			}
		}
	}
	return false
}

func (c *Checker) checkClassFields(cd *ast.ClassDeclaration) {
	if cd.Ambient || len(cd.DeclaredTypeParams().Names) > 0 {
		return // a field's type may name the class's parameters
	}
	sym := c.symbolOf(cd)
	if sym == nil {
		return
	}
	for _, f := range cd.Fields {
		if f.Initializer == nil && f.Type == nil && !f.Static && f.Pos.Line > 0 && !c.opts.CompatJS() &&
			c.Unanswered(c.ctorAssigned(cd, f.Name)) {
			// Neither typed nor assigned by the constructor (TS7008).
			c.report(diag.MemberImplicitAny, f.Pos, sourceName(f.Name))
		}
		if f.Initializer == nil && f.Type != nil && !f.Static && !f.Optional && !f.Definite && !f.LiteralName &&
			!f.Declare && !f.Abstract && f.Pos.Line > 0 && !c.opts.CompatJS() && !thisAssigns(cd, f.Name) {
			// --strictPropertyInitialization (TS2564), for a field no
			// constructor statement assigns.
			if t := c.typeFromNode(f.Type, sym.Scope); !c.Unanswered(t) && !c.admitsUndefined(t) {
				c.report(diag.PropertyNotInitialized, f.Pos, sourceName(f.Name))
			}
		}
		if f.Initializer == nil || f.Type == nil {
			continue
		}
		t := c.typeFromNode(f.Type, sym.Scope)
		if f.Optional && !c.Unanswered(t) {
			t = c.in.union(t, c.missingT)
		}
		c.checkAssign(f.Initializer, t, diag.NotAssignable, f.Initializer.GetPos())
	}
}

// checkAssign reports value's type not being assignable to target. An
// object or array literal is elaborated as TypeScript does: the error lands
// on the property or element that does not fit.
func (c *Checker) checkAssign(value ast.Expression, target *Type, m *diag.Message, pos ast.Pos) {
	if target == nil || c.Unanswered(target) {
		return
	}
	// `await x` of a value that is not a promise is x: its literal is
	// checked as a literal.
	if a, ok := value.(*ast.AwaitExpression); ok {
		if t := c.TypeOf(a.Argument); !c.Unanswered(t) && c.awaited(t, 0) == t {
			c.checkAssign(a.Argument, target, m, pos)
			return
		}
	}
	if m == diag.NotAssignable {
		// A callback parameter whose result does not fit: tsc's head is
		// the parameters' (TS2328), as its callback check reports.
		if vt := c.TypeOf(value); !c.Unanswered(vt) && c.assignableTo(vt, target) != yes {
			if sn, tn, ok := c.callbackResultMismatch(vt, target); ok {
				c.report(diag.ParamsIncompatible, pos, sn, tn)
				return
			}
		}
	}
	if c.elaborate(value, target, m, pos) {
		return
	}
	src := c.TypeOf(value)
	if c.Unanswered(src) && isEmptyArrayLiteral(value) && target.Flags&Union == 0 && target.Flags&Object != 0 &&
		target.Kind == Tuple && target.minLen() > 0 {
		// `[]` against a tuple requiring an element: tsc's `[]`, too short.
		c.report(m, pos, c.in.tuple(nil), target)
		return
	}
	if c.Unanswered(src) && isEmptyArrayLiteral(value) && target.Flags&Union == 0 && target.Flags&Object != 0 &&
		(target.Kind == Interface || target.Kind == Instance) && target.StringIndex == nil {
		// `[]` against a non-array object type: tsc's never[], which lacks
		// the target's own members (TS2739, TS2741).
		src = c.in.array(c.neverT)
		if at := c.apparentType(src); at != nil {
			var missing []string
			for _, tp := range target.Props {
				if !tp.Optional && at.Prop(tp.Name) == nil && c.objectMember(tp.Name) == nil {
					if at.partial && !c.libLacks(at, tp.Name) {
						return // TypeScript's Array may have it
					}
					missing = append(missing, tp.Name)
				}
			}
			if len(missing) > 0 {
				c.reportMissing(missing, src, target, m, pos)
			}
		}
		return
	}
	switch c.assignableTo(src, target) {
	case no:
		if c.isGlobalObject(src) {
			c.report(diag.ObjectToFewTypes, pos) // tsc's head message for `Object`
			return
		}
		// Against the target the message names (`string` of `string |
		// undefined`).
		if dt := c.displayTarget(src, target); !c.couldHaveSingletons(dt, 0) || c.elabWiden > 0 && !c.literalOfContext(src, dt) {
			src = widen(c, src) // tsc names `"x"` as string against number
		}
		c.report(m, pos, src, c.displayTarget(src, target))
	case maybe:
		if c.weakMismatch(src, target) {
			// A weak type (every property optional) takes no primitive or
			// array: tsc's TS2559, for an argument as for any other target.
			c.report(diag.NoCommonProperties, pos, src, c.nonNullable(target))
			return
		}
		if (target.Flags&Union != 0 || target.Kind == Function || c.classValueToConstructType(src, target)) && c.relate(src, target, relation{missingIsNo: true}, 0) == no {
			// No union member (no function return) takes the source even
			// counting what it lacks: tsc's TS2322, not a missing-property
			// message.
			if !c.couldHaveSingletons(c.displayTarget(src, target), 0) {
				src = widen(c, src)
			}
			c.report(m, pos, src, c.displayTarget(src, target))
			return
		}
		if c.isLibPromise(target) && src.Flags&Object != 0 && src.Flags&Union == 0 && src.Symbol != nil && !c.inLibrary(src.Symbol.Scope) &&
			c.relate(src, target, relation{missingIsNo: true}, 0) == no {
			// A Promise's members are the library's own: an object of the
			// program's lacking them is none (TS2739).
			var missing []string
			for _, tp := range target.Props {
				if !tp.Optional && src.Prop(tp.Name) == nil {
					missing = append(missing, tp.Name)
				}
			}
			if len(missing) > 0 {
				c.reportMissing(missing, src, target, m, pos)
			} else {
				c.report(m, pos, src, c.displayTarget(src, target))
			}
			return
		}
		c.checkMissing(src, target, m, pos)
	}
}

// isLibPromise reports the library's Promise or PromiseLike interface.
func (c *Checker) isLibPromise(t *Type) bool {
	return t != nil && t.Flags&Object != 0 && t.Kind == Interface && t.Symbol != nil &&
		(t.Symbol.Name == "Promise" || t.Symbol.Name == "PromiseLike") && c.inLibrary(t.Symbol.Scope)
}

// isGlobalObject reports the library's `Object` interface type.
func (c *Checker) isGlobalObject(t *Type) bool {
	return t != nil && t.Flags&Object != 0 && t.Kind == Interface && t.Symbol != nil &&
		t.Symbol.Name == "Object" && c.inLibrary(t.Symbol.Scope)
}

// checkMissing reports the required properties of target an object source
// lacks, when that is all that keeps it from fitting: TS2741 for one,
// TS2739 for up to five, TS2740 past that; an argument is TS2345.
func (c *Checker) checkMissing(src, target *Type, m *diag.Message, pos ast.Pos) {
	c.reportMissing(c.missingProps(src, target), src, target, m, pos)
}

// missingProps is the required properties of target an object source
// lacks, when that is all that keeps it from fitting; nil when that is not
// known.
func (c *Checker) missingProps(src, target *Type) []string {
	if src.Flags&Object == 0 || target.Flags&Object == 0 || src.Flags&Union != 0 || openType(src, false) || nominalOpenTarget(target) ||
		(src.Kind != Anonymous && src.Kind != Interface && src.Kind != Instance) ||
		(target.Kind != Anonymous && target.Kind != Interface) {
		return nil
	}
	// A builtin declaration lists only what this compiler implements: a
	// member it lacks is missing only when TypeScript's lacks it too.
	partial := nominalOpen(src)
	var missing []string
	for _, tp := range target.Props {
		sp := src.Prop(tp.Name)
		if sp == nil {
			sp = c.objectMember(tp.Name)
		}
		if sp == nil {
			if tp.Optional {
				continue
			}
			if partial && !c.libLacks(src, tp.Name) {
				return nil
			}
			missing = append(missing, tp.Name)
			continue
		}
		if c.assignableTo(sp.Type, tp.Type) != yes {
			return nil // another reason the checker cannot decide
		}
	}
	return missing
}

func (c *Checker) reportMissing(missing []string, src, target *Type, m *diag.Message, pos ast.Pos) {
	switch {
	case len(missing) == 0:
	case m == diag.ArgNotAssignable, target.intersection:
		// An intersection's missing property is elaborated under TS2322.
		c.report(m, pos, src, target)
	case len(missing) == 1:
		c.report(diag.PropertyMissing, pos, memberDisplayName(missing[0]), src, target)
	case len(missing) <= 5: // tsc lists up to five
		c.report(diag.PropertiesMissing, pos, src, target, strings.Join(missing, ", "))
	default:
		c.report(diag.PropertiesMissingMore, pos, src, target, strings.Join(missing[:4], ", "), len(missing)-4)
	}
}

// superClass is the class whose member holds super (through arrow
// functions), or nil when an object literal's method, or no class, does.
func (c *Checker) superClass(n ast.Node) *ast.ClassDeclaration {
	for cur := c.parentOf(n); cur != nil; cur = c.parentOf(cur) {
		switch x := cur.(type) {
		case *ast.ClassDeclaration:
			return x
		case *ast.FunctionExpression:
			if _, lit := c.parentOf(x).(*ast.ObjectLiteral); lit {
				return nil
			}
			if !x.Method {
				return nil
			}
		case *ast.FunctionDeclaration:
			if _, cls := c.parentOf(x).(*ast.ClassDeclaration); !cls {
				return nil
			}
		}
	}
	return nil
}

// privateMethod reports whether class sym declares the private instance
// method name (not an accessor or a field).
func privateMethod(sym *binder.Symbol, name string) bool {
	if sym == nil {
		return false
	}
	for _, d := range sym.Declarations {
		if cd, ok := d.Node.(*ast.ClassDeclaration); ok {
			for _, m := range cd.Methods {
				if m.Name == name && !m.IsStatic && m.AccessorKind == "" {
					return true
				}
			}
		}
	}
	return false
}

// checkTarget reports a store to a name that is not a variable (TS2588 for
// a constant, TS2630 a function, TS2629 a class, TS2628 an enum); it
// reports whether the target is one a value may be stored to.
func (c *Checker) checkTarget(target ast.Expression) bool {
	if m, ok := target.(*ast.MemberExpression); ok {
		// A read-only property of an object type (a getter without a
		// setter, a `Readonly<T>` member) takes no write (TS2540).
		if ot := c.TypeOf(m.Object); !c.Unanswered(ot) && ot.Flags&Object != 0 && (ot.Kind == Anonymous || ot.Kind == Instance) {
			// (A class's `readonly` field takes its constructor's writes:
			// only its getter-only accessors here.)
			if p := ot.Prop(m.Property); p != nil && p.Readonly && (ot.Kind == Anonymous || p.Accessor || !c.ownConstructorWrite(m, p)) {
				c.report(diag.ReadonlyProperty, m.GetPos(), m.Property)
				return false
			}
		}
		// A private method is not writable (TS2803).
		if strings.HasPrefix(m.Property, "#") {
			if ot := c.TypeOf(m.Object); !c.Unanswered(ot) && ot.Flags&Object != 0 && ot.Flags&Union == 0 && ot.Kind == Instance && privateMethod(ot.Symbol, m.Property) {
				c.report(diag.PrivateMethodAssign, m.GetPos(), m.Property)
				return false
			}
		}
		return true
	}
	id, ok := target.(*ast.Identifier)
	if !ok {
		return true
	}
	sym, _ := c.b.Resolve(id)
	if sym == nil || len(sym.Declarations) == 0 {
		return true
	}
	name := sourceName(id.Name)
	switch d := sym.Declarations[0].Node.(type) {
	case *ast.VarDeclaration:
		if d.Kind == "const" {
			c.report(diag.AssignToConst, id.GetPos(), name)
			return false
		}
	case *ast.ArrayDestructuring:
		if d.Kind == "const" {
			c.report(diag.AssignToConst, id.GetPos(), name)
			return false
		}
	case *ast.ObjectDestructuring:
		if d.Kind == "const" {
			c.report(diag.AssignToConst, id.GetPos(), name)
			return false
		}
	case *ast.FunctionDeclaration:
		if sym.Flags&binder.Function != 0 && sym.Declarations[0].Flags&binder.Function != 0 {
			c.report(diag.AssignToFunction, id.GetPos(), name)
			return false
		}
	case *ast.ClassDeclaration:
		c.report(diag.AssignToClass, id.GetPos(), name)
		return false
	case *ast.EnumDeclaration:
		c.report(diag.AssignToEnum, id.GetPos(), name)
		return false
	}
	return true
}

// writeType is the type a store to target takes: its storage type, or an
// accessor's setter type.
func (c *Checker) writeType(target ast.Expression) *Type {
	if m, ok := target.(*ast.MemberExpression); ok {
		if obj := c.TypeOf(m.Object); obj.Flags&Object != 0 && obj.Flags&Union == 0 {
			if p := obj.Prop(m.Property); p != nil && p.Write != nil {
				return p.Write
			}
		}
	}
	return c.StorageTypeOf(target)
}

// libGlobals are the global types TypeScript's standard library declares: a
// program's own declaration of one merges with the library's members, which
// the checker does not have until the builtin library is declared (TDD-00230
// P3.1), so it decides nothing about their members.
var libGlobals = map[string]bool{
	"Object": true, "Function": true, "String": true, "Number": true, "Boolean": true, "Symbol": true,
	"BigInt": true, "Array": true, "ReadonlyArray": true, "Date": true, "RegExp": true, "Error": true,
	"TypeError": true, "RangeError": true, "SyntaxError": true, "ReferenceError": true, "EvalError": true,
	"URIError": true, "Math": true, "JSON": true, "Promise": true, "PromiseLike": true, "Map": true,
	"Set": true, "WeakMap": true, "WeakSet": true, "WeakRef": true, "Iterator": true, "Iterable": true,
	"IterableIterator": true, "Generator": true, "ArrayBuffer": true, "DataView": true, "Proxy": true,
	"Reflect": true, "Intl": true, "Console": true, "Element": true, "Node": true, "Document": true,
	"Window": true, "Event": true, "EventTarget": true, "Request": true, "Response": true, "Headers": true,
	"URL": true, "Blob": true, "TextEncoder": true, "TextDecoder": true, "Buffer": true, "ArrayLike": true,
	"PropertyDescriptor": true, "Record": true, "Partial": true, "Required": true, "Readonly": true,
	"Int8Array": true, "Uint8Array": true, "Uint8ClampedArray": true, "Int16Array": true, "Uint16Array": true,
	"Int32Array": true, "Uint32Array": true, "Float32Array": true, "Float64Array": true,
	"BigInt64Array": true, "BigUint64Array": true, "NodeJS": true, "CallableFunction": true,
	"NewableFunction": true, "IArguments": true, "TemplateStringsArray": true,
}

// nominalOpen reports an object type the checker does not know the whole
// member list of: a library global's name, or a class merged with an
// interface, itself or up its base chain.
// closedClassValue reports a class value whose static side this checker
// holds whole: a source class declared once, with no base class and no
// declaration merged into it.
func (c *Checker) closedClassValue(t *Type) bool {
	sym := t.ClassOf
	if t.Base != nil || t.StringIndex != nil || sym == nil || c.inLibrary(sym.Scope) || mergedWithInterface(sym) || classDecls(sym) != 1 {
		return false
	}
	for _, d := range sym.Declarations {
		cd, ok := d.Node.(*ast.ClassDeclaration)
		if !ok || cd.BaseClass != "" {
			return false
		}
	}
	return true
}

func nominalOpen(t *Type) bool { return openType(t, true) }

// nominalOpenTarget is nominalOpen for the target of a missing-member
// check: a builtin declaration's unlisted members only add requirements,
// so a listed member the source lacks is missing all the same.
func nominalOpenTarget(t *Type) bool { return openType(t, false) }

func openType(t *Type, partialOpen bool) bool {
	for seen := 0; t != nil && seen < 64; seen++ {
		if partialOpen && t.partial || len(t.Calls) > 0 || len(t.Constructs) > 0 {
			return true // a callable type also has Function's members
		}
		if t.Symbol != nil {
			name := sourceName(t.Symbol.Name)
			if partialOpen && (libGlobals[name] || strings.HasPrefix(name, "HTML") || strings.HasPrefix(name, "SVG")) || mergedWithInterface(t.Symbol) || classDecls(t.Symbol) > 1 {
				return true
			}
		}
		t = t.Base
	}
	return false
}

// libLacks reports a member read on a builtin interface's type that neither
// the builtin declarations nor TypeScript's own declarations of it and its
// bases have (lib/tsmembers.txt): the declarations list only what this
// compiler implements, so a member they lack is TS2339 only when
// TypeScript's library lacks it too.
func (c *Checker) libLacks(t *Type, name string) bool {
	if t.Symbol == nil || t.Prop(name) != nil || t.StringIndex != nil || t.NumberIndex != nil && numericName(name) {
		return false
	}
	if t.Kind != Interface && t.Kind != Instance {
		return false
	}
	if !c.inLibrary(t.Symbol.Scope) {
		return false // (a program's augmentation of it declares its own members, found above)
	}
	known, has := lib.TSInterfaceHas(sourceName(t.Symbol.Name), name)
	if !known || has {
		return false
	}
	if len(t.Calls) > 0 || len(t.Constructs) > 0 {
		// A callable value also has Function's members.
		if _, fn := lib.TSInterfaceHas("Function", name); fn {
			return false
		}
	}
	return true
}

// checkPatternProps reports each named key of an object destructuring
// pattern that its source type src lacks (TS2339), as checkProperty does
// for a member read, through nested patterns.
// checkArrayPattern reports an array pattern's element past the end of a
// tuple source (TS2493). An array literal source is the tuple of its
// elements, an element with a default adding an optional one past them, as
// tsc types the literal in the pattern's context.
func (c *Checker) checkArrayPattern(elems []ast.ArrayPatternElem, src ast.Expression, st *Type, pos ast.Pos) {
	at := func(el ast.ArrayPatternElem) ast.Pos {
		if el.Pos.Line > 0 {
			return el.Pos
		}
		return pos
	}
	hole := func(el ast.ArrayPatternElem) bool { return el.Name == "" && el.SubArray == nil && el.SubObject == nil }
	// A sub-pattern over a null or undefined element reads no iterator.
	nullish := func(el ast.ArrayPatternElem, t *Type) bool {
		if el.SubArray == nil || el.Default != nil || t == nil || c.Unanswered(t) || t.Flags&Union != 0 || t.Flags&(Null|Undefined) == 0 {
			return false
		}
		c.report(diag.NotIterable, at(el), t)
		return true
	}
	if lit, ok := src.(*ast.ArrayLiteral); ok && !hasSpreadElement(lit.Elements) {
		for i, el := range elems {
			if el.Rest {
				break
			}
			if i < len(lit.Elements) {
				if el.SubArray != nil && !nullish(el, c.TypeOf(lit.Elements[i])) {
					c.checkArrayPattern(el.SubArray, lit.Elements[i], nil, at(el))
				}
				continue
			}
			if el.Default == nil && !hole(el) {
				c.report(diag.TupleNoElement, at(el), c.literalTuple(lit), len(lit.Elements), i)
				return
			}
		}
		return
	}
	if st == nil && src != nil {
		st = c.TypeOf(src)
	}
	if st != nil && !c.Unanswered(st) && st.Flags&Object != 0 && st.Kind == Array && st.Elem != nil {
		// An array source: each element its element type.
		for _, el := range elems {
			if el.Rest {
				break
			}
			if el.SubArray != nil && !nullish(el, st.Elem) {
				c.checkArrayPattern(el.SubArray, nil, st.Elem, at(el))
			}
		}
		return
	}
	if st == nil || c.Unanswered(st) || st.Flags&Object == 0 || st.Kind != Tuple {
		return
	}
	for i, el := range elems {
		if el.Rest {
			break
		}
		if i < len(st.Elems) {
			if el.SubArray != nil && !nullish(el, st.Elems[i]) {
				c.checkArrayPattern(el.SubArray, nil, st.Elems[i], at(el))
			}
			continue
		}
		if !hole(el) {
			c.report(diag.TupleNoElement, at(el), st, len(st.Elems), i)
			return
		}
	}
}

// checkAssignArrayPattern is checkArrayPattern for an assignment's array
// pattern (`[a, b] = …`), whose elements are expressions.
func (c *Checker) checkAssignArrayPattern(pat *ast.ArrayLiteral, src ast.Expression, st *Type) {
	if hasSpreadElement(pat.Elements) {
		c.checkAssignRestTarget(pat, src, st)
		return
	}
	hole := func(x ast.Expression) bool { return x == nil || isOmitted(x) }
	sub := func(x ast.Expression) (*ast.ArrayLiteral, bool) {
		if as, ok := x.(*ast.AssignmentExpression); ok && as.Op == "=" {
			x = as.Left
		}
		l, ok := x.(*ast.ArrayLiteral)
		return l, ok
	}
	defaulted := func(x ast.Expression) bool {
		as, ok := x.(*ast.AssignmentExpression)
		return ok && as.Op == "="
	}
	if lit, ok := src.(*ast.ArrayLiteral); ok && !hasSpreadElement(lit.Elements) {
		for i, x := range pat.Elements {
			if i < len(lit.Elements) {
				if l, ok := sub(x); ok {
					c.checkAssignArrayPattern(l, lit.Elements[i], nil)
				}
				continue
			}
			if !defaulted(x) && !hole(x) {
				c.report(diag.TupleNoElement, x.GetPos(), c.literalTuple(lit), len(lit.Elements), i)
				return
			}
		}
		return
	}
	if st == nil && src != nil {
		st = c.TypeOf(src)
	}
	if st == nil || c.Unanswered(st) || st.Flags&Object == 0 || st.Kind != Tuple {
		return
	}
	for i, x := range pat.Elements {
		if i < len(st.Elems) {
			if l, ok := sub(x); ok {
				c.checkAssignArrayPattern(l, nil, st.Elems[i])
			}
			continue
		}
		if !hole(x) {
			c.report(diag.TupleNoElement, x.GetPos(), st, len(st.Elems), i)
			return
		}
	}
}

// checkAssignRestTarget checks a pattern's trailing `...target` against
// the tuple of the source elements it collects (TS2322), for a source that
// is an array literal or a tuple of fixed elements.
func (c *Checker) checkAssignRestTarget(pat *ast.ArrayLiteral, src ast.Expression, st *Type) {
	k := len(pat.Elements) - 1
	sp, ok := pat.Elements[k].(*ast.SpreadElement)
	if !ok || hasSpreadElement(pat.Elements[:k]) {
		return
	}
	switch sp.Arg.(type) {
	case *ast.Identifier, *ast.MemberExpression:
	default:
		return
	}
	var rest *Type
	if lit, ok := src.(*ast.ArrayLiteral); ok && !hasSpreadElement(lit.Elements) {
		full := c.literalTuple(lit)
		if k > len(full.Elems) {
			k = len(full.Elems)
		}
		rest = c.in.tuple(full.Elems[k:])
	} else {
		if st == nil && src != nil {
			st = c.TypeOf(src)
		}
		if st == nil || c.Unanswered(st) || st.Flags&Object == 0 || st.Kind != Tuple || st.Opt != 0 {
			return
		}
		if k > len(st.Elems) {
			k = len(st.Elems)
		}
		rest = c.in.tuple(st.Elems[k:])
	}
	target := c.writeType(sp.Arg)
	if c.Unanswered(target) || target.Flags&Any != 0 {
		return
	}
	if c.assignableTo(rest, target) == no {
		c.report(diag.NotAssignable, sp.Arg.GetPos(), rest, c.displayTarget(rest, target))
	}
}

// literalTuple is the tuple an array literal is in a pattern's context: its
// elements' widened types.
func (c *Checker) literalTuple(lit *ast.ArrayLiteral) *Type {
	elems := make([]*Type, len(lit.Elements))
	for i, x := range lit.Elements {
		if x == nil || isOmitted(x) {
			elems[i] = c.undefinedT
			continue
		}
		elems[i] = widen(c, c.TypeOf(x))
	}
	return c.in.tuple(elems)
}

func hasSpreadElement(xs []ast.Expression) bool {
	for _, x := range xs {
		if _, ok := x.(*ast.SpreadElement); ok {
			return true
		}
	}
	return false
}

// assignPatternProps is an assignment pattern's (`({ x, y: t } = o)`)
// static keys, as a declaration's pattern holds them, at each target.
func assignPatternProps(lit *ast.ObjectLiteral) []ast.DestructProp {
	var out []ast.DestructProp
	for _, p := range lit.Properties {
		if _, spread := p.Value.(*ast.SpreadElement); spread || p.KeyExpr != nil || p.Key == "" || p.AccessorKind != "" {
			continue
		}
		dp := ast.DestructProp{Key: p.Key, Local: p.Key, Pos: p.Value.GetPos()}
		if ae, ok := p.Value.(*ast.AssignmentExpression); ok && ae.Op == "=" {
			dp.Default = ae.Right // `{ x = 1 }`, `{ x: y = 1 }`
		}
		out = append(out, dp)
	}
	return out
}

func (c *Checker) checkPatternProps(props []ast.DestructProp, src *Type, pos ast.Pos) {
	if src != nil && !c.Unanswered(src) && src.Flags&Union == 0 && src.Member == "" && src.Flags&(StringLike|NumberLike|BooleanLike|BigIntLike) != 0 {
		// A primitive reads its wrapper interface's members (TS2339).
		if ap := c.apparentType(src); ap != nil {
			for _, p := range props {
				at := pos
				if p.Pos.Line > 0 {
					at = p.Pos
				}
				if !p.Rest && p.KeyExpr == nil && p.Key != "" && !objectProto[p.Key] && c.libLacks(ap, p.Key) {
					c.report(diag.PropertyNotExist, at, p.Key, src)
				}
			}
		}
		return
	}
	if src != nil && !c.Unanswered(src) && src.Flags&Union != 0 && !c.opts.CompatJS() {
		// A union some member of which has no such property (null, or a
		// closed object type without it): TS2339 on the union.
		for _, p := range props {
			if p.Rest || p.KeyExpr != nil || p.Key == "" || objectProto[p.Key] || strings.HasPrefix(p.Key, "__kml_") {
				continue
			}
			at := pos
			if p.Pos.Line > 0 {
				at = p.Pos
			}
			if c.unionLacksProp(src, p.Key) {
				c.report(diag.PropertyNotExist, at, p.Key, src)
			}
		}
		return
	}
	if src == nil || c.Unanswered(src) || src.Flags&Object == 0 || src.Flags&Union != 0 {
		return
	}
	if src == c.globalThisT {
		// The global object's members are the globals, as `globalThis.x`
		// reads them.
		for _, p := range props {
			if p.Rest || p.KeyExpr != nil || p.Key == "" || objectProto[p.Key] || c.opts.CompatJS() {
				continue
			}
			at := pos
			if p.Pos.Line > 0 {
				at = p.Pos
			}
			switch c.globalObjectMember(p.Key) {
			case globalMissing, globalBlockScoped:
				c.report(diag.PropertyNotExist, at, p.Key, src)
			}
		}
		return
	}
	for _, p := range props {
		at := pos
		if p.Pos.Line > 0 {
			at = p.Pos
		}
		if p.KeyExpr != nil && !p.Rest {
			c.checkPatternIndexKey(p.KeyExpr, src, at)
			continue
		}
		if p.Rest || p.Key == "" || objectProto[p.Key] || strings.HasPrefix(p.Key, "__kml_") {
			continue
		}
		if c.arrayLacks(src, p.Key) || c.libLacks(src, p.Key) {
			c.report(diag.PropertyNotExist, at, p.Key, src)
			continue
		}
		switch src.Kind {
		case Anonymous, Interface, Instance:
		default:
			continue
		}
		if nominalOpen(src) || src.StringIndex != nil || src.Symbol != nil && c.inLibrary(src.Symbol.Scope) {
			continue
		}
		prop := src.Prop(p.Key)
		if prop == nil {
			c.report(diag.PropertyNotExist, at, p.Key, src)
			continue
		}
		if p.SubObject != nil {
			c.checkPatternProps(p.SubObject, prop.Type, at)
		}
	}
}

// checkPatternIndexKey reports a pattern's computed key of type string or
// number on a user object type with no index signature it matches (TS2537),
// where an element access reports TS7053.
func (c *Checker) checkPatternIndexKey(key ast.Expression, src *Type, at ast.Pos) {
	switch src.Kind {
	case Anonymous, Interface, Instance:
	default:
		return
	}
	if nominalOpen(src) || src.Symbol != nil && c.inLibrary(src.Symbol.Scope) || src.StringIndex != nil {
		return
	}
	kt := c.TypeOf(key)
	if c.Unanswered(kt) || kt.Flags&Union != 0 {
		return
	}
	switch {
	case kt.Flags&String != 0:
		c.report(diag.NoMatchingIndexSig, at, src, "string")
	case kt.Flags&Number != 0 && src.NumberIndex == nil:
		c.report(diag.NoMatchingIndexSig, at, src, "number")
	}
}

// arrayLacks reports a named member read on an array or tuple type that
// TypeScript's Array declares nowhere, neither in its own library
// (lib/tsmembers.txt) nor in any declaration of the global Array (the
// builtin declarations and a program's `declare global` augmentation).
func (c *Checker) arrayLacks(t *Type, name string) bool {
	if t.Kind != Array && t.Kind != Tuple || numericName(name) {
		return false
	}
	known, has := lib.TSInterfaceHas("Array", name)
	if !known || has {
		return false
	}
	if c.b.Globals == nil {
		return false
	}
	sym := c.b.Globals.Symbols.Get("Array")
	if sym == nil {
		return false
	}
	for _, d := range sym.Declarations {
		iface, ok := d.Node.(*ast.InterfaceDeclaration)
		if !ok {
			continue
		}
		for _, f := range iface.Fields {
			if f.Name == name {
				return false
			}
		}
		for _, m := range iface.Methods {
			if m.Name == name {
				return false
			}
		}
	}
	return true
}

// mergesEnum reports whether sym merges an enum with another declaration,
// whose members the enum's type does not list.
func mergesEnum(sym *binder.Symbol) bool {
	if len(sym.Declarations) < 2 {
		return false
	}
	for _, d := range sym.Declarations {
		if _, ok := d.Node.(*ast.EnumDeclaration); ok {
			return true
		}
	}
	return false
}

// What the global object holds under a name.
const (
	globalMissing = iota
	globalProperty
	globalBlockScoped // a script's let, const or class: a global, no property
)

// globalObjectMember is what the global object has under name: a builtin
// or declared global, or a script's `var` or function, is its property.
func (c *Checker) globalObjectMember(name string) int {
	if name == "globalThis" || objectProto[name] || c.builtinValueType(name) != nil {
		return globalProperty
	}
	if c.b.Globals != nil && c.b.Globals.Symbols.Get(name) != nil {
		return globalProperty
	}
	sym := c.b.Module.Symbols.Get(name)
	if sym == nil {
		// A script's top-level name carries its file's suffix.
		c.b.Module.Symbols.Each(func(s *binder.Symbol) {
			if sym == nil && s.Flags&binder.Value != 0 && ast.TrimModuleSuffix(s.Name) == name {
				sym = s
			}
		})
	}
	if sym != nil && len(sym.Declarations) > 0 {
		if prog, ok := c.b.Module.Node.(*ast.Program); ok {
			if top := c.topStatement(sym.Declarations[0].Node); top != nil && !prog.IsESM(top) {
				if sym.Flags&(binder.BlockScopedVariable|binder.Class) != 0 {
					return globalBlockScoped
				}
				return globalProperty
			}
		}
	}
	return globalMissing
}

// topStatement is the program-level statement that holds n, or nil.
func (c *Checker) topStatement(n ast.Node) ast.Statement {
	child := n
	for p := c.parentOf(n); p != nil; child, p = p, c.parentOf(p) {
		if _, ok := p.(*ast.Program); ok {
			st, _ := child.(ast.Statement)
			return st
		}
	}
	return nil
}

// objectProto are the members every object has from Object.prototype.
var objectProto = map[string]bool{
	"toString": true, "toLocaleString": true, "valueOf": true, "hasOwnProperty": true,
	"isPrototypeOf": true, "propertyIsEnumerable": true, "constructor": true, "__proto__": true,
}

// checkProperty reports a property read on a value that may be absent,
// and one the checker's object type does not declare (TS2339): a single
// object literal type, interface or class instance it models whole.
func (c *Checker) checkProperty(e *ast.MemberExpression) {
	if !e.Optional {
		c.checkReceiver(e.Object)
	} else if obj := c.TypeOf(e.Object); c.optionalOnNullish(obj) {
		// `a?.b` of an a that is only ever null or undefined: b of never
		// (tsc); JavaScript short-circuits it.
		if !c.opts.CompatJS() {
			c.report(diag.PropertyNotExist, e.GetPos(), e.Property, c.neverT)
		}
		return
	}
	// `never` (a value flow proves cannot exist) has no members.
	// (A `x!` of a null is never too, but tsc reads its members unchecked.)
	if _, nonNull := e.Object.(*ast.NonNullExpression); nonNull {
	} else if obj := c.TypeOf(e.Object); !c.Unanswered(obj) && obj.Flags&Never != 0 && obj.Flags&Union == 0 {
		c.reportMissingProp(e.GetPos(), e.Property, obj)
		return
	}
	if strings.HasPrefix(e.Property, "#") {
		// A private name an instance lacks: its class declares it static.
		if obj := c.TypeOf(e.Object); !c.Unanswered(obj) && obj.Flags&Object != 0 && obj.Flags&Union == 0 && obj.Kind == Instance && obj.Prop(e.Property) == nil && c.declaresStatic(obj.Symbol, e.Property) {
			c.reportMissingProp(e.GetPos(), e.Property, obj)
		}
		// … and one the class lacks: its instances declare it.
		if obj := c.TypeOf(e.Object); !c.Unanswered(obj) && obj.Flags&Object != 0 && obj.Flags&Union == 0 && obj.Kind == Anonymous && obj.ClassOf != nil && obj.Prop(e.Property) == nil && classDeclares(obj.ClassOf, e.Property, false) {
			c.reportMissingProp(e.GetPos(), e.Property, obj)
		}
		return
	}
	if objectProto[e.Property] || strings.HasPrefix(e.Property, "__kml_") {
		return // Object.prototype's or this compiler's own
	}
	if ns, ok := c.namespaceExports(e.Object); ok && !ns[e.Property] && !c.opts.CompatJS() {
		if m := c.namespaceMember(e); m == nil || m.Flags&binder.Value != 0 && !c.declaredExported(m) {
			// A member the namespace does not export is no property of
			// its object (tsc's TS2339 on `typeof N`).
			c.report(diag.PropertyNotExist, e.GetPos(), e.Property, "typeof "+dottedName(e.Object))
			return
		}
	}
	if c.namespaceMember(e) != nil {
		return // a namespace's member
	}
	if sym := c.calleeSymbol(e.Object); sym != nil && (mergesEnum(sym) || c.b.NamespaceScope(sym) != nil) {
		return // a merged declaration (an enum and a namespace, …)
	}
	obj := c.TypeOf(e.Object)
	if obj == c.globalThisT {
		// No global of that name: any, an implicit one under noImplicitAny
		// (tsc's TS7017); a script's `let`, `const` or class is no
		// property of it (TS2339). A `this` an arrow captures is TS7041
		// alone.
		if th, ok := e.Object.(*ast.ThisExpression); c.opts.CompatJS() || ok && c.capturesGlobalThis(th) {
			return
		}
		switch c.globalObjectMember(e.Property) {
		case globalMissing:
			c.report(diag.GlobalThisImplicitAny, e.GetPos())
		case globalBlockScoped:
			c.reportMissingProp(e.GetPos(), e.Property, obj)
		}
		return
	}
	// A primitive reads its wrapper interface's members (`(5).x`).
	if !c.Unanswered(obj) && obj.Flags&Union == 0 && obj.Member == "" && obj.Flags&(StringLike|NumberLike|BooleanLike|BigIntLike) != 0 {
		if ap := c.apparentType(obj); ap != nil && c.libLacks(ap, e.Property) {
			c.reportMissingProp(e.GetPos(), e.Property, obj)
		}
		return
	}
	if !c.Unanswered(obj) && obj.Flags&Union != 0 && !c.opts.CompatJS() {
		// A union reads only what every member has (tsc's
		// getPropertyOfUnionOrIntersectionType): one member lacking it is
		// TS2339 against the whole union.
		for _, m := range obj.Types {
			if c.unionMemberLacks(m, e.Property) {
				c.reportMissingProp(e.GetPos(), e.Property, obj)
				return
			}
		}
		return
	}
	if c.Unanswered(obj) || obj.Flags&Object == 0 || obj.Flags&Union != 0 {
		return
	}
	if c.arrayLacks(obj, e.Property) {
		c.reportMissingProp(e.GetPos(), e.Property, obj)
		return
	}
	if c.libLacks(obj, e.Property) {
		c.reportMissingProp(e.GetPos(), e.Property, obj)
		return
	}
	if obj.ClassOf != nil && !c.opts.CompatJS() && c.closedClassValue(obj) {
		// A class value (`typeof A`) has its statics and Function's
		// members, nothing else (TS2339).
		if obj.Prop(e.Property) == nil && e.Property != "prototype" && !objectProto[e.Property] && c.functionMember(e.Property) == nil {
			c.reportMissingProp(e.GetPos(), e.Property, obj)
		}
		return
	}
	if nominalOpen(obj) {
		return
	}
	switch obj.Kind {
	case Anonymous, Interface, Instance:
	case Function:
		// A function declared here: its expando properties and Function's.
		if c.expandoFunction(e.Object) && c.expandoType(e.Object, e.Property) == nil && c.functionMember(e.Property) == nil {
			c.reportMissingProp(e.GetPos(), e.Property, obj)
		}
		return
	default:
		return
	}
	if obj.Prop(e.Property) == nil && obj.StringIndex == nil {
		c.reportMissingProp(e.GetPos(), e.Property, obj)
	}
}

// unionMemberLacks reports a union member that certainly has no property
// name: a primitive whose wrapper lacks it, or an object type this checker
// models whole that declares neither it nor an index signature.
func (c *Checker) unionMemberLacks(m *Type, name string) bool {
	if m.Member != "" || m.Flags&(Any|Unknown|Never|Null|Undefined|Void|TypeParam|Deferred) != 0 {
		return false
	}
	if m.Flags&(StringLike|NumberLike|BooleanLike|BigIntLike) != 0 {
		ap := c.apparentType(m)
		return ap != nil && c.libLacks(ap, name)
	}
	if m.Flags&Object == 0 {
		return false
	}
	if c.arrayLacks(m, name) || c.libLacks(m, name) {
		return true
	}
	if nominalOpen(m) || m.Symbol != nil && c.inLibrary(m.Symbol.Scope) {
		return false
	}
	switch m.Kind {
	case Anonymous, Interface, Instance:
		return m.Prop(name) == nil && m.StringIndex == nil
	}
	return false
}

// checkIndexKey reports an element access by a constant key (a string or
// number literal, a const holding one, or a const `unique symbol`) that the
// object's type has neither as a member nor through an index signature
// (TS7053), for the types checkProperty models whole.
func (c *Checker) checkIndexKey(e *ast.IndexExpression) {
	if id, ok := e.Object.(*ast.Identifier); ok && c.constEnumRef(id) {
		if _, str := e.Index.(*ast.StringLiteral); !str {
			if tl, isT := e.Index.(*ast.TemplateLiteral); !isT || len(tl.Exprs) > 0 {
				c.report(diag.ConstEnumIndexKey, startOf(e.Index)) // TS2476
				return
			}
		}
	}
	key, literal := c.accessKey(e.Index)
	if !literal {
		c.checkComputedIndexKey(e)
		return
	}
	if objectProto[key] || strings.HasPrefix(key, "__kml_") {
		return
	}
	obj := c.TypeOf(e.Object)
	if obj == c.globalThisT {
		switch c.globalObjectMember(key) {
		case globalProperty:
			return
		case globalBlockScoped:
			c.report(diag.PropertyNotExist, e.Object.GetPos(), key, obj)
			return
		}
	}
	if c.Unanswered(obj) || obj.Flags&Object == 0 || obj.Flags&Union != 0 || nominalOpen(obj) {
		return
	}
	switch obj.Kind {
	case Anonymous, Interface, Instance:
	default:
		return
	}
	if obj.Symbol != nil && c.inLibrary(obj.Symbol.Scope) {
		return
	}
	if obj.Prop(key) != nil {
		return
	}
	sym := strings.HasPrefix(key, ast.SymbolMemberPrefix)
	if !sym && obj.StringIndex != nil {
		return
	}
	kt := c.TypeOf(e.Index)
	if !sym && obj.NumberIndex != nil && (kt.Flags&(Number|NumberLiteral) != 0 || numericLiteralName(key)) {
		return // (`o["3"]` reads the number index too: tsc's isNumericLiteralName)
	}
	shown := kt.String()
	if sym {
		shown = "unique symbol"
		if obj.StringIndex != nil || obj.NumberIndex != nil {
			// No key index signatures take (TS2538).
			c.report(diag.InvalidIndexType, e.Index.GetPos(), shown)
			return
		}
	}
	if !sym && c.freshLiteralRead(e) {
		// A literal key read off an object literal itself (`({ a: 1
		// })["b"]`): tsc's TS2339, as a property access reports.
		c.report(diag.PropertyNotExist, startOf(e.Object), key, obj)
		return
	}
	if !sym {
		if m := c.indexCallSuggestion(e, obj, kt); m != "" {
			c.report(diag.IndexCallSuggestion, startOf(e.Object), obj, m)
			return
		}
		if id, ok := e.Object.(*ast.Identifier); ok && c.constEnumRef(id) {
			// A const enum object has no reverse mapping: a key it lacks
			// is a missing property (TS2339).
			c.report(diag.PropertyNotExist, startOf(e.Index), key, obj)
			return
		}
		if obj.NumberIndex != nil {
			// Only a number index signature, keyed by no number (tsc's
			// TS7015, at the key).
			c.report(diag.IndexNotNumber, startOf(e.Index))
			return
		}
	}
	if !sym {
		if sug := c.propSuggestion(key, obj); sug != "" {
			c.report(diag.PropertyDidYouMean, startOf(e.Index), key, obj, sug)
			return
		}
	}
	c.report(diag.IndexImplicitAny, e.Object.GetPos(), shown, obj)
}

// reportMissingProp reports name missing on obj: TS2551 with the closest
// property name tsc's getSuggestedSymbolForNonexistentProperty would
// offer, else TS2339.
func (c *Checker) reportMissingProp(at ast.Pos, name string, obj *Type) {
	if sug := c.propSuggestion(name, obj); sug != "" {
		c.report(diag.PropertyDidYouMean, at, name, obj, sug)
		return
	}
	c.report(diag.PropertyNotExist, at, name, obj)
}

// propSuggestion is the spelling suggestion for name among obj's
// properties, for an object, interface or instance type (not a union).
func (c *Checker) propSuggestion(name string, obj *Type) string {
	if obj == nil || obj.Flags&Object == 0 || obj.Flags&Union != 0 {
		return ""
	}
	switch obj.Kind {
	case Anonymous, Interface, Instance:
	default:
		return ""
	}
	var cands []string
	for _, p := range obj.Props {
		if !strings.HasPrefix(p.Name, ast.SymbolMemberPrefix) && !strings.HasPrefix(p.Name, "__kml_") {
			cands = append(cands, p.Name)
		}
	}
	return spellingSuggestion(name, cands)
}

// indexCallSuggestion is the method tsc names in TS7052 for an element
// access no index signature takes: `x.get` for a read, `x.set` for a
// write, when obj has that method with one signature whose first
// parameter takes the key (tsc's getSuggestionForNonexistentIndexSignature);
// "" when it has none.
func (c *Checker) indexCallSuggestion(e *ast.IndexExpression, obj, kt *Type) string {
	name := "get"
	switch p := c.parentOf(e).(type) {
	case *ast.AssignmentExpression:
		if p.Left == ast.Expression(e) {
			name = "set"
		}
	case *ast.UpdateExpression:
		name = "set"
	}
	pr := obj.Prop(name)
	if pr == nil || c.Unanswered(pr.Type) || pr.Type.Flags&Object == 0 || pr.Type.Kind != Function || len(pr.Type.Overloads) > 0 {
		return ""
	}
	sig := pr.Type
	if len(sig.Params) == 0 || sig.OptionalParam(0) || c.assignableTo(kt, sig.Params[0]) == no {
		return ""
	}
	if s := dottedName(e.Object); s != "" {
		return s + "." + name
	}
	return name
}

// freshLiteralRead reports e reads (not writes) an object literal
// expression's own element.
func (c *Checker) freshLiteralRead(e *ast.IndexExpression) bool {
	root := e.Object
	for {
		m, ok := root.(*ast.MemberExpression)
		if !ok {
			break
		}
		root = m.Object // a fresh literal's own property's (`({ f: {…} }).f[k]`)
	}
	if _, ok := root.(*ast.ObjectLiteral); !ok {
		return false
	}
	switch p := c.parentOf(e).(type) {
	case *ast.AssignmentExpression:
		return p.Left != ast.Expression(e)
	case *ast.UpdateExpression:
		return false
	}
	return true
}

// dottedName is an access chain's source (`a.b.c`), "" when it is not one
// of identifiers.
func dottedName(e ast.Expression) string {
	switch x := e.(type) {
	case *ast.Identifier:
		return ast.TrimModuleSuffix(x.Name)
	case *ast.ThisExpression:
		return "this"
	case *ast.MemberExpression:
		if b := dottedName(x.Object); b != "" {
			return b + "." + x.Property
		}
	}
	return ""
}

// checkComputedIndexKey reports a read through a string-, number- or
// symbol-typed key on a user object type without an index signature
// (TS7053).
func (c *Checker) checkComputedIndexKey(e *ast.IndexExpression) {
	obj := c.TypeOf(e.Object)
	if c.forInKeyOfIntersection(e, obj) {
		// tsc types the key `Extract<keyof X, string>` only where `keyof
		// X` is one type variable's keys; an intersection's are a union, so
		// the key is a string, which indexes no generic object.
		c.report(diag.IndexImplicitAny, e.Object.GetPos(), "string", obj)
		return
	}
	if obj.Flags&NonPrimitive != 0 && obj.Flags&Union == 0 {
		// `object` has no index signature: a string or number key reads
		// an implicit any.
		if kt := c.TypeOf(e.Index); !c.Unanswered(kt) && kt.Flags&Union == 0 && (isStringLike(kt) || kt.Flags&(Number|NumberLiteral|Any) != 0) {
			c.report(diag.IndexImplicitAny, e.Object.GetPos(), kt.String(), obj)
		}
		return
	}
	// A builtin with a number index only (a Buffer, a typed array) read by
	// a string key no literal fixes: no index it takes.
	if !c.Unanswered(obj) && obj.Flags&Object != 0 && obj.Flags&Union == 0 && obj.Kind != Array && obj.Kind != Tuple && obj.StringIndex == nil && obj.NumberIndex != nil && obj.Symbol != nil && c.inLibrary(obj.Symbol.Scope) {
		if kt := c.TypeOf(e.Index); !c.Unanswered(kt) && kt.Flags&Union == 0 && isStringLike(kt) && kt.Flags&StringLiteral == 0 {
			c.report(diag.IndexImplicitAny, e.Object.GetPos(), kt.String(), obj)
			return
		}
	}
	if c.Unanswered(obj) || obj.Flags&Object == 0 || obj.Flags&Union != 0 || nominalOpen(obj) {
		return
	}
	switch obj.Kind {
	case Anonymous, Interface, Instance:
	default:
		return
	}
	if obj.Symbol != nil && c.inLibrary(obj.Symbol.Scope) {
		return
	}
	kt := c.TypeOf(e.Index)
	if c.freshLiteralRead(e) && kt.Flags&(String|Number) != 0 {
		// An object literal itself read by a string or number key is the
		// union of its property types (tsc's getPropertyTypeForIndexType).
		return
	}
	if !c.Unanswered(kt) && kt.Flags&Union == 0 && kt.Flags&(StringLiteral|NumberLiteral) != 0 {
		// A key of one literal type (an enum member, `o[E.A]`): the
		// member it names, as a literal key reads.
		if obj.Prop(kt.Value) != nil || obj.NumberIndex != nil && (kt.Flags&NumberLiteral != 0 || numericLiteralName(kt.Value)) || obj.StringIndex != nil {
			return
		}
	}
	if obj.StringIndex != nil || obj.NumberIndex != nil {
		if symbolKey(kt) && !c.wellKnownSymbolRead(e.Index) {
			c.report(diag.InvalidIndexType, e.Index.GetPos(), kt)
		}
		return
	}
	// An `any` key reads no known member either (TS7053 names it `any`).
	anyKey := kt.Flags&Any != 0 && kt.Flags&Union == 0
	if c.Unanswered(kt) || kt.Flags&Union != 0 || !(anyKey || isStringLike(kt) || kt.Flags&(Number|NumberLiteral|ESSymbol) != 0) {
		return
	}
	if symbolKey(kt) && (c.wellKnownSymbolRead(e.Index) || c.symbolIndexed(obj)) {
		return
	}
	if !symbolKey(kt) {
		if m := c.indexCallSuggestion(e, obj, kt); m != "" {
			c.report(diag.IndexCallSuggestion, startOf(e.Object), obj, m)
			return
		}
	}
	c.report(diag.IndexImplicitAny, e.Object.GetPos(), kt.String(), obj)
}

// classDecls counts sym's class declarations: more than one is two
// namespaces' classes of one name (ADR-00450), and the checker cannot tell
// which a reference means.
func classDecls(sym *binder.Symbol) int {
	n := 0
	for _, d := range sym.Declarations {
		if _, ok := d.Node.(*ast.ClassDeclaration); ok {
			n++
		}
	}
	return n
}

// mergedWithInterface reports a class merged with an interface of its
// name: the interface's members are not in the instance type.
func mergedWithInterface(sym *binder.Symbol) bool {
	class, iface := false, false
	for _, d := range sym.Declarations {
		switch d.Node.(type) {
		case *ast.ClassDeclaration:
			class = true
		case *ast.InterfaceDeclaration:
			iface = true
		}
	}
	return class && iface
}

// elaborate checks a literal against target piece by piece; it reports
// whether it took the literal over (then the whole is not also reported).
// A literal lacking a required property is reported at pos, as the whole.
func (c *Checker) elaborate(value ast.Expression, target *Type, m *diag.Message, pos ast.Pos) bool {
	if target.Flags&Union != 0 {
		// A literal is never null or undefined: `T | undefined` (an optional
		// parameter's) elaborates against T.
		// Nor is an object or array literal a primitive: `Stuff | string`
		// elaborates against Stuff.
		_, objLit := value.(*ast.ObjectLiteral)
		_, arrLit := value.(*ast.ArrayLiteral)
		var only *Type
		for _, mt := range target.Types {
			if mt.Flags&(Null|Undefined) != 0 || mt == c.missingT {
				continue
			}
			if (objLit || arrLit) && mt.Flags&(StringLike|NumberLike|BooleanLike|BigIntLike|ESSymbol) != 0 {
				continue
			}
			if only != nil {
				return false
			}
			only = mt
		}
		if only == nil {
			return false
		}
		target = only
	}
	if target.Flags&Object == 0 {
		return false
	}
	if a, ok := c.assertion(value); ok && a.Cast != nil {
		return false // `<T>{…}` is not a fresh literal
	}
	switch v := value.(type) {
	case *ast.ObjectLiteral:
		if target.Kind != Anonymous && target.Kind != Interface {
			return false
		}
		for _, p := range v.Properties {
			if p.KeyExpr != nil || p.AccessorKind != "" || p.Key == "" {
				return false
			}
		}
		if !nominalOpen(target) && len(target.Props) > 0 && target.StringIndex == nil && target.NumberIndex == nil {
			// A fresh literal may not name a property the target lacks
			// (TS2353; `{}` takes any); TypeScript reports that before
			// anything missing.
			for _, p := range v.Properties {
				if target.Prop(p.Key) == nil && !objectProto[p.Key] {
					c.report(diag.ExcessProperty, p.Value.GetPos(), p.Key, target)
					return true
				}
			}
		}
		if src := c.TypeOf(v); !c.Unanswered(src) && c.assignableTo(src, target) == yes {
			// tsc elaborates only a literal that does not fit; a fresh
			// object inside still has its excess properties checked.
			for _, p := range v.Properties {
				if tt := memberTarget(target, p.Key); tt != nil {
					c.checkExcess(p.Value, tt)
				}
			}
			return true
		}
		for _, p := range v.Properties {
			if tt := memberTarget(target, p.Key); tt != nil {
				// A property's literal is widened outside a literal
				// context of its kind, as the literal's type has it.
				c.elabWiden++
				c.checkAssign(p.Value, tt, diag.NotAssignable, p.Value.GetPos())
				c.elabWiden--
			}
		}
		if nominalOpen(target) {
			return true
		}
		var missing []string
		for _, tp := range target.Props {
			if !tp.Optional && !hasKey(v, tp.Name) {
				missing = append(missing, tp.Name)
			}
		}
		if len(missing) > 0 {
			if src := c.TypeOf(v); !c.Unanswered(src) {
				c.reportMissing(missing, widen(c, src), target, m, pos)
			}
		}
		return true
	case *ast.ArrayLiteral:
		var elem func(i int) *Type
		switch target.Kind {
		case Array:
			elem = func(int) *Type { return target.Elem }
			if target.Head != nil {
				// A rest tuple: tsc elaborates into its leading positions
				// only; a literal too short, or wrong past them, is
				// reported whole.
				if len(v.Elements) < len(target.Head) {
					return false
				}
				for _, x := range v.Elements[len(target.Head):] {
					if xt := c.TypeOf(x); c.Unanswered(xt) || c.assignableTo(xt, target.RestElem) == no {
						return false
					}
				}
				elem = target.elemAt
			}
		case Tuple:
			if len(v.Elements) < target.minLen() || len(v.Elements) > len(target.Elems) {
				return false
			}
			elem = func(i int) *Type { return target.Elems[i] }
		default:
			return false
		}
		for _, x := range v.Elements {
			if _, spread := x.(*ast.SpreadElement); spread {
				return false
			}
		}
		if src := c.TypeOf(v); !c.Unanswered(src) && c.assignableTo(src, target) == yes {
			// tsc elaborates only a literal that does not fit; a fresh
			// object inside still has its excess properties checked.
			for i, x := range v.Elements {
				c.checkExcess(x, elem(i))
			}
			return true
		}
		for i, x := range v.Elements {
			c.checkAssign(x, elem(i), diag.NotAssignable, x.GetPos())
		}
		return true
	}
	return false
}

// checkExcess reports the excess properties of the fresh object literals in
// value (itself, or nested in array and object literals) against target,
// for a value that is otherwise assignable.
func (c *Checker) checkExcess(value ast.Expression, target *Type) {
	switch value.(type) {
	case *ast.ObjectLiteral, *ast.ArrayLiteral:
		c.elaborate(value, target, diag.NotAssignable, value.GetPos())
	}
}

// checkCall checks a call to a function type the checker models whole: no
// overloads, no spread argument; a generic one's argument count only.
func (c *Checker) checkCall(e *ast.CallExpression) {
	if e.Optional {
		// `f?.()` of an f that is only ever null or undefined: a call of
		// never (tsc).
		if !c.opts.CompatJS() && c.optionalOnNullish(c.TypeOf(e.Callee)) {
			c.report(diag.NotCallable, e.Callee.GetPos())
		}
		return
	}
	c.checkCallee(e.Callee)
	if m, ok := e.Callee.(*ast.MemberExpression); ok && m.Property == "setPrototypeOf" && len(e.Args) > 0 {
		if id, isID := m.Object.(*ast.Identifier); isID && id.Name == "Object" {
			if t := c.TypeOf(e.Args[0]); !c.Unanswered(t) && t.Flags&Object != 0 && t.Flags&(Any|Unknown|Union) == 0 && !c.opts.CompatJS() && (t.Kind == Anonymous || t.Kind == Interface) && !c.literalBinding(e.Args[0]) {
				// (an untyped JavaScript binding of an object is held in
				// any; a class instance takes a replaced prototype)
				c.report(diag.SetPrototypeStatic, e.Args[0].GetPos())
			}
		}
	}
	if m, ok := e.Callee.(*ast.MemberExpression); ok && m.Property == "assign" && len(e.Args) > 1 && !c.opts.CompatJS() {
		if id, isID := m.Object.(*ast.Identifier); isID && id.Name == "Object" {
			c.checkAssignRetypes(e)
		}
	}
	if m, ok := e.Callee.(*ast.MemberExpression); ok && m.Property == "toLocaleString" && len(e.Args) > 0 {
		// A number's locale/options: Intl.
		if t := c.TypeOf(m.Object); !c.Unanswered(t) && t.Flags&(Any|Unknown|Never) == 0 && len(members(t)) > 0 && allMembers(t, NumberLike) {
			c.report(diag.NumberLocaleArgs, e.GetPos())
		}
	}
	if t := c.TypeOf(e.Callee); !c.opts.CompatJS() && !c.Unanswered(t) && t.Flags&(Any|Unknown|Never) == 0 &&
		len(members(t)) > 0 && allMembers(t, StringLike|NumberLike|BooleanLike|BigIntLike) {
		// A primitive has no call signatures (`true()`, `x()` of a number).
		c.report(diag.NotCallable, e.Callee.GetPos())
		return
	}
	if t := c.TypeOf(e.Callee); !c.opts.CompatJS() && !c.Unanswered(t) && t.Flags&Object != 0 &&
		(t.Kind == Anonymous && t.Symbol == nil && len(t.Props) == 0 || t.Kind == Array || t.Kind == Tuple) {
		// An empty object literal's type or an array has none either.
		if fn := c.signaturesOf(t, false); c.Unanswered(fn) || fn.Kind != Function {
			c.report(diag.NotCallable, e.Callee.GetPos())
			return
		}
	}
	for _, a := range e.Args {
		if _, spread := a.(*ast.SpreadElement); spread {
			c.checkSpreadArity(e)
			return
		}
	}
	// A class called without `new`: constructible, not callable (TS2348).
	if id, ok := e.Callee.(*ast.Identifier); ok {
		if sym, _ := c.b.Resolve(id); sym != nil && sym.Flags&binder.Class != 0 && sym.Flags&binder.Function == 0 {
			c.report(diag.ClassNotCallable, e.Callee.GetPos(), "typeof "+sourceName(sym.Name))
			return
		}
	}
	if sig := c.boundCallSignature(e); sig != nil {
		c.checkSignatureCall(e, sig)
		return
	}
	fn := c.signaturesOf(c.TypeOf(e.Callee), false)
	if c.Unanswered(fn) || fn.Flags&Object == 0 || fn.Kind != Function {
		return
	}
	c.checkSignatureCall(e, fn)
}

// boundCallSignature is what `f.call(thisArg, …)` / `f.apply(thisArg, args)`
// is checked against where f has one non-generic signature: CallableFunction
// instantiated by it (strictBindCallApply), `(thisArg: T, ...params) => R` /
// `(thisArg: T, args: [params]) => R`. An apply over optional or rest
// parameters (a tuple with optional elements) is not modelled.
func (c *Checker) boundCallSignature(e *ast.CallExpression) *Type {
	if c.boundCallResult(e) == nil {
		return nil
	}
	m := e.Callee.(*ast.MemberExpression)
	ft := c.TypeOf(m.Object)
	this := ft.ThisType
	if this == nil {
		this = c.unknownT
	}
	if m.Property == "call" {
		params := append([]*Type{this}, ft.Params...)
		var optionals []bool
		if len(ft.optionals) > 0 {
			optionals = append([]bool{false}, ft.optionals...)
		}
		return c.in.function(params, optionals, ft.restParam, ft.Ret(), nil, nil)
	}
	if ft.restParam || ft.MinArgs() != len(ft.Params) {
		return nil
	}
	if len(ft.Params) == 0 {
		// `f.apply(thisArg)` or with an empty argument list.
		return c.in.function([]*Type{this, c.in.tuple(nil)}, []bool{false, true}, false, ft.Ret(), nil, nil)
	}
	return c.in.function([]*Type{this, c.in.tuple(ft.Params)}, nil, false, ft.Ret(), nil, nil)
}

// checkSignatureCall checks a call's arguments against fn, a function or
// construct signature (or an overload list): type-argument arity and
// constraints, the argument count, and each argument's type.
func (c *Checker) checkSignatureCall(e *ast.CallExpression, fn *Type) {
	if c.typeArgArityError(e, fn) || c.typeArgConstraintError(e.TypeArgs, len(e.Args), fn) {
		return
	}
	if id, ok := e.Callee.(*ast.Identifier); ok && len(fn.Overloads) == 0 {
		if sym, _ := c.b.Resolve(id); sym != nil && valueDecls(sym) > 1 {
			return // merged value declarations
		}
	}
	if len(fn.Overloads) == 1 && fn.Overloads[0] != nil && fn.Overloads[0].Flags&Object != 0 && fn.Overloads[0].Kind == Function && len(fn.Overloads[0].Overloads) == 0 {
		// One signature: checked as tsc checks a plain call, its messages
		// those of an ordinary call.
		c.checkSignatureCall(e, fn.Overloads[0])
		return
	}
	if len(fn.Overloads) > 0 {
		c.checkOverloadCall(e, fn)
		return
	}
	generic := len(fn.TypeParams) > 0
	if !generic {
		if c.checkNeverRest(e, fn) {
			return
		}
		fn = c.expandTupleRest(fn)
	} else if c.checkGenericTupleRest(e, fn) {
		return
	}
	if fn.ThisType != nil && !generic {
		// The receiver must be the signature's `this` (TS2684); a call with
		// none passes void.
		if m, ok := skipOuter(e.Callee).(*ast.MemberExpression); ok {
			c.checkAssign(m.Object, fn.ThisType, diag.ThisContext, m.Object.GetPos())
		} else if !c.Unanswered(fn.ThisType) && fn.ThisType.Flags&(Any|Unknown) == 0 {
			c.report(diag.ThisContext, e.Callee.GetPos(), c.voidT, fn.ThisType)
		}
	}
	min, max := fn.MinArgs(), len(fn.Params)
	if fn.restParam {
		max = -1
	}
	if len(e.Args) < min {
		if max < 0 {
			c.report(diag.ArgCountAtLeast, e.Callee.GetPos(), min, len(e.Args))
		} else if len(e.Args) == 0 && c.isPromiseResolve(e.Callee) {
			c.report(diag.PromiseResolveArity, e.Callee.GetPos(), arity(min, max), len(e.Args))
		} else {
			c.report(diag.ArgCount, e.Callee.GetPos(), arity(min, max), len(e.Args))
		}
		return
	}
	if max >= 0 && len(e.Args) > max {
		c.report(diag.ArgCount, e.Args[max].GetPos(), arity(min, max), len(e.Args))
		return
	}
	before := len(c.diags)
	if generic {
		// The arguments are checked against the inferred instantiation only
		// where an argument alone answers a constrained type parameter
		// (`f<T extends C>(x: T)`): the inferred type must satisfy C (tsc's
		// TS2345 against the constraint). The rest is not modelled.
		if len(e.TypeArgs) == 0 {
			c.checkInferredConstraints(e, fn)
		}
		// A parameter whose type names no type parameter is checked as in
		// a plain call (`byteOffset?: number` of `new DataView<T>(…)`). A
		// primitive passed for a function-typed one fails under every
		// instantiation (`setTimeout(5)`).
		inst := c.inferredInstantiation(e, fn)
		for i, a := range e.Args {
			p := paramAt(fn, i)
			if p == nil {
				continue
			}
			if i > 0 && len(c.diags) > before {
				break // tsc reports the first argument that does not fit
			}
			if !hasTypeParam(p) {
				c.checkAssign(a, p, diag.ArgNotAssignable, startOf(a))
			} else if c.primitiveForFunction(a, p) {
				c.report(diag.ArgNotAssignable, startOf(a), c.TypeOf(a), p)
			} else if ip := c.instantiatedParam(inst, p, a); ip != nil {
				// Inference does not take a type argument from a callback's
				// parameters (tsc's contravariant candidates): against the
				// instantiation it gives, parameters compare both ways.
				c.bivariantArgs++
				c.checkAssign(a, ip, diag.ArgNotAssignable, startOf(a))
				c.bivariantArgs--
			}
		}
		return
	}
	for i, a := range e.Args {
		if len(c.diags) > before {
			break // tsc reports the first argument that does not fit
		}
		if p := paramAt(fn, i); p != nil {
			c.checkAssign(a, p, diag.ArgNotAssignable, startOf(a))
		}
	}
}

// checkTaggedTemplate checks a tagged template as the call it is: the
// template object (a TemplateStringsArray) and each substitution against
// the tag's parameters, for a tag the checker models whole (one non-generic
// signature).
func (c *Checker) checkTaggedTemplate(e *ast.TaggedTemplateExpression) {
	fn := c.signaturesOf(c.TypeOf(e.Tag), false)
	if c.Unanswered(fn) || fn.Flags&Object == 0 || fn.Kind != Function || len(fn.Overloads) > 0 ||
		len(fn.TypeParams) > 0 || len(e.TypeArgs) > 0 {
		return
	}
	// The call passes the template object and each substitution (TS2554).
	n, min, max := 1+len(e.Exprs), fn.MinArgs(), len(fn.Params)
	if fn.restParam {
		max = -1
	}
	if n < min && max < 0 {
		c.report(diag.ArgCountAtLeast, e.GetPos(), min, n)
		return
	}
	if n < min {
		c.report(diag.ArgCount, e.GetPos(), arity(min, max), n)
		return
	}
	if max >= 0 && n > max {
		// At the first argument past the parameters, as tsc reports it.
		pos := e.GetPos()
		if max > 0 {
			pos = e.Exprs[max-1].GetPos()
		}
		c.report(diag.ArgCount, pos, arity(min, max), n)
		return
	}
	if p := paramAt(fn, 0); p != nil && !c.Unanswered(p) && c.b.Globals != nil {
		if sym := c.b.Globals.Symbols.Get("TemplateStringsArray"); sym != nil && sym.Flags&binder.Interface != 0 {
			if tsa := c.interfaceOf(sym, nil); !c.Unanswered(tsa) && !c.assignable(tsa, p) {
				c.report(diag.ArgNotAssignable, e.GetPos(), tsa, p)
			}
		}
	}
	for i, x := range e.Exprs {
		if p := paramAt(fn, i+1); p != nil {
			c.checkAssign(x, p, diag.ArgNotAssignable, x.GetPos())
		}
	}
}

// checkInferredConstraints reports an argument whose parameter is a bare
// constrained type parameter and whose inferred type does not satisfy the
// constraint (TS2345, with the instantiated constraint as the parameter).
func (c *Checker) checkInferredConstraints(e *ast.CallExpression, fn *Type) {
	m := c.inferArgsRecv(callRecv(e), e.Args, nil, fn, true)
	for i, a := range e.Args {
		if _, spread := a.(*ast.SpreadElement); spread {
			return
		}
		p := paramAt(fn, i)
		if p == nil || p.Flags&TypeParam == 0 || p.Constraint == nil {
			continue
		}
		at, con := m[p], c.instantiate(p.Constraint, m)
		if at == nil || c.Unanswered(at) || c.Unanswered(con) || hasTypeParam(con) || hasTypeParam(at) {
			continue
		}
		if at.Flags&(Any|Unknown|Never) != 0 {
			continue
		}
		if !c.assignable(at, con) {
			// tsc falls back to the constraint and checks the argument
			// against it: an object literal is elaborated (TS2353/TS2741).
			c.checkAssign(a, con, diag.ArgNotAssignable, startOf(a))
		}
	}
}

// typeArgArityError reports explicit type arguments no signature of fn
// takes that many of (TS2558): more than every signature declares type
// parameters. (Fewer may be defaulted, which the signature types do not
// record.)
func (c *Checker) typeArgArityError(e *ast.CallExpression, fn *Type) bool {
	if len(e.TypeArgs) == 0 {
		return false
	}
	sigs := fn.Overloads
	if len(sigs) == 0 {
		sigs = []*Type{fn}
	}
	lo, hi := -1, 0
	for _, s := range sigs {
		n := len(s.TypeParams)
		if n >= len(e.TypeArgs) {
			return false
		}
		if lo < 0 || n < lo {
			lo = n
		}
		hi = max(hi, n)
	}
	c.report(diag.TypeArgCount, typeArgsPos(e.TypeArgs, e.Callee.GetPos()), arity(lo, hi), len(e.TypeArgs))
	return true
}

// typeArgsPos is where TypeScript reports a type-argument list: its first
// argument, else at.
func typeArgsPos(args []ast.TypeNode, at ast.Pos) ast.Pos {
	if len(args) > 0 && args[0] != nil {
		return args[0].GetLoc().Pos
	}
	return at
}

// newTypeArgArityError reports `new C<…>(…)` of a program class given
// fewer type arguments than it has type parameters without defaults, or
// more than it has (TS2558).
func (c *Checker) newTypeArgArityError(n *ast.NewExpression) bool {
	sym := c.b.NewTarget(n)
	if sym == nil || sym.Flags&binder.Class == 0 || len(sym.Declarations) != 1 {
		return false
	}
	cd, ok := sym.Declarations[0].Node.(*ast.ClassDeclaration)
	if !ok {
		return false
	}
	tp := cd.DeclaredTypeParams()
	hi, lo := len(tp.Names), 0
	for i := range tp.Names {
		if i >= len(tp.Defaults) || tp.Defaults[i] == nil {
			lo = i + 1
		}
	}
	if len(n.TypeArgs) >= lo && len(n.TypeArgs) <= hi {
		return false
	}
	c.report(diag.TypeArgCount, typeArgsPos(n.TypeArgs, n.GetPos()), arity(lo, hi), len(n.TypeArgs))
	return true
}

// checkRefTypeArgs reports a type reference's argument that does not
// satisfy its type parameter's constraint (TS2344): `Holder<number>` for
// `interface Holder<T extends object>`.
func (c *Checker) checkRefTypeArgs(r *ast.TypeReference, scope *binder.Scope) {
	if len(r.TypeArgs) == 0 {
		return
	}
	sym := resolveTypeName(r.Name, scope)
	if sym == nil {
		return
	}
	var names []string
	var cons []ast.TypeNode
	for _, d := range sym.Declarations {
		switch n := d.Node.(type) {
		case *ast.InterfaceDeclaration:
			if len(n.TypeParameters) == 0 {
				continue
			}
			names, cons = nil, nil
			for _, tp := range n.TypeParameters {
				names, cons = append(names, tp.Name), append(cons, tp.Constraint)
			}
		case *ast.TypeAliasDeclaration:
			names, cons = nil, nil
			for _, tp := range n.TypeParameters {
				names, cons = append(names, tp.Name), append(cons, tp.Constraint)
			}
		case *ast.ClassDeclaration:
			names, cons = append([]string(nil), n.TypeParams...), nil
			for i := range n.TypeParams {
				var tn ast.TypeNode
				if i < len(n.TypeParamConstraints) && n.TypeParamConstraints[i] != nil {
					tn = n.TypeParamConstraints[i]
				}
				cons = append(cons, tn)
			}
		}
		if names != nil {
			break
		}
	}
	if len(r.TypeArgs) > len(names) {
		return // TS2314's arity, not a constraint
	}
	var args []*Type
	for _, a := range r.TypeArgs {
		args = append(args, c.typeFromNode(a, scope))
	}
	pop := c.declEnv(names[:len(args)], args)
	defer pop()
	for i, at := range args {
		if cons[i] == nil || c.Unanswered(at) || hasTypeParam(at) {
			continue
		}
		con := c.typeFromNode(cons[i], sym.Scope)
		if c.Unanswered(con) || hasTypeParam(con) {
			continue
		}
		if rel := c.assignableTo(at, con); rel == no || rel == maybe && len(c.missingProps(at, con)) > 0 {
			if !someMember(con, Literal) {
				at = widen(c, at)
			}
			c.report(diag.TypeArgConstraint, r.TypeArgs[i].GetLoc().Pos, at, con)
			return
		}
	}
}

// newCtor is the construct signature(s) a `new X(…)` calls: a class's
// constructor, or a value's construct signatures (`declare var Map:
// MapConstructor`); nil when neither is modelled.
func (c *Checker) newCtor(n *ast.NewExpression) *Type {
	sym := c.b.NewTarget(n)
	if sym == nil {
		sym = c.builtinImport(n.ClassName)
	}
	switch {
	case sym == nil:
		return nil
	case sym.Flags&binder.Class != 0:
		return c.constructorType(sym)
	case sym.Flags&binder.Variable != 0:
		ctor := c.signaturesOf(c.typeOfSymbol(sym), true)
		if c.Unanswered(ctor) || ctor.Flags&Object == 0 || ctor.Kind != Function {
			return nil
		}
		return ctor
	}
	return nil
}

// typeArgConstraintError reports an explicit type argument that does not
// satisfy its type parameter's constraint (TS2344), the constraint
// instantiated by the arguments given. An overloaded fn is checked against
// its first signature taking that many type and value arguments.
func (c *Checker) typeArgConstraintError(typeArgs []ast.TypeNode, nargs int, fn *Type) bool {
	if len(typeArgs) == 0 {
		return false
	}
	sig := fn
	if len(fn.Overloads) > 0 {
		sig = nil
		for _, s := range fn.Overloads {
			if len(s.TypeParams) >= len(typeArgs) && (s.restParam || nargs <= len(s.Params)) {
				sig = s
				break
			}
		}
		if sig == nil {
			return false
		}
	}
	m := c.inferArgs(nil, typeArgs, sig, false)
	for i, tp := range sig.TypeParams {
		if i >= len(typeArgs) || tp.Constraint == nil {
			continue
		}
		at, con := m[tp], c.instantiate(tp.Constraint, m)
		if at == nil || c.Unanswered(at) || c.Unanswered(con) || hasTypeParam(con) {
			continue
		}
		if rel := c.assignableTo(at, con); rel == no || rel == maybe && len(c.missingProps(at, con)) > 0 {
			if !someMember(con, Literal) {
				at = widen(c, at) // tsc names `1` as number against string
			}
			pos := typeArgs[i].GetLoc().Pos
			c.report(diag.TypeArgConstraint, pos, at, con)
			return true
		}
	}
	return false
}

func hasKey(o *ast.ObjectLiteral, key string) bool {
	for _, p := range o.Properties {
		if p.Key == key {
			return true
		}
	}
	return false
}

func arity(min, max int) string {
	switch {
	case min == max:
		return strconv.Itoa(min)
	}
	return strconv.Itoa(min) + "-" + strconv.Itoa(max)
}

// checkOperator checks an arithmetic, bitwise or `+` operator's operands.
// An operand whose type includes null or undefined is TypeScript's
// "possibly null" family (TS18047…), not reported here.
func (c *Checker) checkOperator(e *ast.BinaryExpression) {
	l, r := c.TypeOf(e.Left), c.TypeOf(e.Right)
	switch e.Op {
	case "==", "!=", "===", "!==":
		c.checkEquality(e, l, r)
		return
	case "instanceof":
		// tsc's checkInstanceOfExpression: a primitive on the left (TS2358),
		// and on the right a plain interface object with neither a call nor
		// a construct signature nor @@hasInstance (TS2359).
		if !c.Unanswered(l) && l.Flags&Union == 0 && l.Flags&(StringLike|NumberLike|BooleanLike|BigIntLike|ESSymbol) != 0 {
			c.report(diag.InstanceofLeft, startOf(e.Left))
		}
		if !c.Unanswered(r) && r.Flags&Union == 0 && r.Flags&Object != 0 && r.Kind == Interface &&
			len(r.Calls) == 0 && len(r.Constructs) == 0 && r.Prop("@@hasInstance") == nil &&
			!(r.Symbol != nil && r.Symbol.Name == "Function") {
			c.report(diag.InstanceofRight, startOf(e.Right))
		}
		return
	case "in":
		// The right side is an object (tsc's checkInExpression: assignable
		// to `object`): a primitive one is TS2322.
		if !c.Unanswered(r) && r.Flags&Union == 0 && r.Flags&(StringLike|NumberLike|BooleanLike|BigIntLike|ESSymbol) != 0 {
			c.report(diag.NotAssignable, startOf(e.Right), widen(c, r), c.objectT)
		}
		return
	case "<", ">", "<=", ">=":
		if c.symbolOperand(e.Op, e.Left, e.Right, l, r) {
			return
		}
		nl, nr := c.checkNonNull(e.Left), c.checkNonNull(e.Right)
		if nl != nil && nr != nil && !c.Unanswered(l) && !c.Unanswered(r) {
			// tsc: comparable either way, or both number/bigint.
			bl, br := widen(c, l), widen(c, r)
			if c.comparable(bl, br, 0) == no && c.comparable(br, bl, 0) == no && !(arithmetic(bl) && arithmetic(br)) {
				c.report(diag.OperatorNotApplied, e.Left.GetPos(), e.Op, bl, br)
				return
			}
		}
		// A string against a number or bigint is no comparison TypeScript
		// allows (TS2365); other pairs are left to its comparability rules.
		if nl != nil && nr != nil && plainOperand(l) && plainOperand(r) &&
			(isStringLike(l) && arithmetic(r) || arithmetic(l) && isStringLike(r)) {
			c.report(diag.OperatorNotApplied, e.Left.GetPos(), e.Op, widen(c, l), widen(c, r))
		} else if nl != nil && nr != nil && !c.Unanswered(l) && !c.Unanswered(r) &&
			(objectOperand(l) && arithmetic(r) || arithmetic(l) && objectOperand(r)) {
			// An object is not assignable to number or bigint, which the
			// other operand is: no comparison TypeScript allows.
			c.report(diag.OperatorNotApplied, e.Left.GetPos(), e.Op, widen(c, l), widen(c, r))
		}
		return
	}
	c.checkArithmetic(e.Op, e.Op, e.Left, e.Right, l, r)
}

// checkArithmetic checks the operands of the arithmetic, bitwise or `+`
// operator op (written as shown, `+=` for a compound assignment) and
// returns its result type, nil where an operand was reported or the result
// is not decided here (any, unknown, a type parameter).
func (c *Checker) checkArithmetic(op, shown string, left, right ast.Expression, l, r *Type) *Type {
	switch op {
	case "-", "*", "/", "%", "**", "&", "|", "^", "<<", ">>", ">>>", "+":
		if c.symbolOperand(shown, left, right, l, r) {
			return nil
		}
	}
	switch op {
	case "-", "*", "/", "%", "**", "&", "|", "^", "<<", ">>", ">>>":
		// Each operand must be a value first (checkNonNullType), then a
		// number.
		nl, nr := c.checkNonNull(left), c.checkNonNull(right)
		if nl == nil || nr == nil {
			return nil
		}
	case "+":
		if !c.Unanswered(l) && !c.Unanswered(r) && l.Flags&Any == 0 && r.Flags&Any == 0 && !isStringLike(l) && !isStringLike(r) {
			if nl, nr := c.checkNonNull(left), c.checkNonNull(right); nl == nil || nr == nil {
				return nil
			}
		}
	default:
		return nil
	}
	if c.Unanswered(l) || c.Unanswered(r) || !plainOperand(l) || !plainOperand(r) {
		return nil
	}
	switch op {
	case "-", "*", "/", "%", "**", "&", "|", "^", "<<", ">>", ">>>":
		if (op == "&" || op == "|" || op == "^") && allMembers(l, BooleanLike) && allMembers(r, BooleanLike) {
			return nil // TypeScript's TS2447 ("use && instead"), not reported here
		}
		lok, rok := arithmetic(l), arithmetic(r)
		if !lok {
			c.report(diag.ArithmeticLeft, left.GetPos())
		}
		if !rok {
			c.report(diag.ArithmeticRight, right.GetPos())
		}
		if lok && rok && mixesBigInt(l, r) {
			c.report(diag.OperatorNotApplied, left.GetPos(), shown, widen(c, l), widen(c, r))
			return nil
		}
		if !lok || !rok {
			return nil
		}
		if op == ">>>" && (isBigIntLike(l) || isBigIntLike(r)) {
			// No unsigned shift on bigint (tsc names the operands as typed).
			c.report(diag.OperatorNotApplied, left.GetPos(), shown, l, r)
			return nil
		}
		if isBigIntLike(l) && isBigIntLike(r) {
			return c.bigintT
		}
		return c.numT
	case "+":
		if isStringLike(l) || isStringLike(r) {
			return c.strT
		}
		if !arithmetic(l) || !arithmetic(r) || mixesBigInt(l, r) {
			// tsc names the operands as typed when a bigint is involved,
			// widened otherwise (`5n + 1`, but `true + 1` is boolean and
			// number).
			if !isBigIntLike(l) && !isBigIntLike(r) {
				l, r = widen(c, l), widen(c, r)
			}
			c.report(diag.OperatorNotApplied, left.GetPos(), shown, l, r)
			return nil
		}
		if isBigIntLike(l) && isBigIntLike(r) {
			return c.bigintT
		}
		return c.numT
	}
	return nil
}

// plainOperand reports whether t is an operand this check decides: not any
// or unknown, nothing nullish, no symbol (tsc's TS2469, not modelled), no
// type parameter, and a string-like member
// only where every member is one (`string | number` is TS2365 territory
// with its own rules for `+`).
func plainOperand(t *Type) bool {
	for _, m := range members(t) {
		if m.Flags&(Any|Unknown|Nullish|Never|ESSymbol|Deferred) != 0 || m.Flags&TypeParam != 0 && m.Constraint != nil {
			return false
		}
	}
	return true
}

// arithmetic reports whether t may be an arithmetic operand: a number
// (native widths and numeric enums included) or a bigint.
func arithmetic(t *Type) bool { return isNumberLike(t) || isBigIntLike(t) }

func mixesBigInt(l, r *Type) bool {
	return isBigIntLike(l) && isNumberLike(r) || isNumberLike(l) && isBigIntLike(r)
}

// checkEquality reports an equality whose operand types have no overlap
// (TS2367): neither is comparable to the other, and neither is exactly null
// or undefined, which every type is comparable with in an equality
// (isTypeEqualityComparableTo).
func (c *Checker) checkEquality(e *ast.BinaryExpression, l, r *Type) {
	if c.Unanswered(l) || c.Unanswered(r) || nullableUnit(l) || nullableUnit(r) || c.comparable(l, r, 0) != no || c.comparable(r, l, 0) != no {
		return
	}
	// tsc names the operands by their base types when those are unrelated
	// too (`1 === "a"` is number and string).
	if bl, br := widen(c, l), widen(c, r); c.comparable(bl, br, 0) == no && c.comparable(br, bl, 0) == no {
		l, r = bl, br
	}
	c.report(diag.NoOverlap, e.Left.GetPos(), l, r)
}

// comparable is TypeScript's comparable relation, which equality checks
// use: a union is comparable when some member is. It answers for
// primitives, their literals, null and undefined, and the array, tuple and
// function shapes against a primitive; anything else is maybe.
func (c *Checker) comparable(s, t *Type, depth int) ternary {
	switch {
	case s == nil || t == nil || c.Unanswered(s) || c.Unanswered(t) || depth > 8:
		return maybe
	case s == t:
		return yes
	case s.Flags&(Any|Unknown|Never) != 0 || t.Flags&(Any|Unknown|Never) != 0:
		return yes
	case s.Flags&TypeParam != 0 || t.Flags&TypeParam != 0:
		return maybe
	case s.Flags&Union != 0 || t.Flags&Union != 0:
		out := no
		for _, sm := range members(s) {
			for _, tm := range members(t) {
				switch c.comparable(sm, tm, depth+1) {
				case yes:
					return yes
				case maybe:
					out = maybe
				}
			}
		}
		return out
	case (s.Member != "" || t.Member != "") && s.Flags&Literal != 0 && t.Flags&Literal != 0:
		// Two enum members are different values; an enum member and a plain
		// literal are comparable when their values are equal.
		return boolTern((s.Member == "" || t.Member == "") && s.Flags&Literal == t.Flags&Literal && s.Value == t.Value)
	}
	sk, tk := primKind(s), primKind(t)
	switch {
	case sk == 0 && tk == 0:
		return maybe // two object types: structural, not modelled here
	case sk == 0 || tk == 0:
		// An object against a primitive: an array, tuple or function has
		// no primitive's members; another object type may be a primitive's
		// apparent type (`{}`, `{ length: number }`).
		o, prim := s, t
		if sk != 0 {
			o, prim = t, s
		}
		if o.Flags&Object != 0 && (o.Kind == Array || o.Kind == Tuple || o.Kind == Function) {
			return no
		}
		if o.Flags&Object != 0 && !o.partial && (o.Kind == Anonymous || o.Kind == Interface || o.Kind == Instance) {
			// A required property the primitive's apparent type lacks: no
			// value is both (`{ a: 1 }` and 0).
			if at := c.apparentType(prim); at != nil {
				for _, p := range o.Props {
					if !p.Optional && at.Prop(p.Name) == nil {
						return no
					}
				}
			}
		}
		return maybe
	case sk != tk:
		return no
	case s.Flags&Literal != 0 && t.Flags&Literal != 0:
		return no // two different literals of one kind
	}
	return yes
}

// primKind is the primitive a type is one of, or 0 for an object type.
func primKind(t *Type) TypeFlags {
	switch {
	case t.Flags&StringLike != 0:
		return String
	case t.Flags&NumberLike != 0:
		return Number
	case t.Flags&BooleanLike != 0:
		return Boolean
	case t.Flags&BigIntLike != 0:
		return BigInt
	case t.Flags&ESSymbol != 0:
		return ESSymbol
	case t.Flags&(Undefined|Void) != 0:
		return Undefined
	case t.Flags&Null != 0:
		return Null
	}
	return 0
}

func nullableUnit(t *Type) bool {
	return t.Flags&(Undefined|Null) != 0 && t.Flags&Union == 0 && !t.IsMissing()
}

// checkCompound checks a compound assignment's operands as its operator's
// (`x -= 1` as `x - 1`): each must be a value, and a number for arithmetic.
func (c *Checker) checkCompound(op string, left, right ast.Expression) {
	l, r := c.TypeOf(left), c.TypeOf(right)
	res := c.checkArithmetic(op, op+"=", left, right, l, r)
	if res == nil {
		return
	}
	// tsc's checkAssignmentOperator: the result is stored to the left.
	switch left.(type) {
	case *ast.Identifier, *ast.MemberExpression:
		target := c.writeType(left)
		if c.Unanswered(target) || target.Flags&Any != 0 {
			return
		}
		if _, id := left.(*ast.Identifier); id {
			target = widen(c, target) // as `x = x + 1` assigns to x's base type
		}
		if c.assignableTo(res, target) == no {
			c.report(diag.NotAssignable, left.GetPos(), res, c.displayTarget(res, target))
		}
	}
}

// checkTypeNames reports TS2304 for each type reference in n that names
// nothing: no declaration of the program's, no type parameter it declares
// anywhere, and no library or Node name (cannotFind). A name within tsc's
// spelling-suggestion distance of a known one is left alone, since tsc
// reports it as TS2552 instead.
func (c *Checker) checkTypeNames(n ast.Node) {
	if c.opts.CompatJS() {
		return
	}
	scope := c.b.Module
	if st, ok := n.(ast.Statement); ok {
		if s := c.b.StatementScope(st); s != nil {
			scope = s
		}
	}
	c.checkTypeNamesIn(n, scope)
}

// checkTypeNamesIn is checkTypeNames over x, whose innermost enclosing scope
// is scope.
func (c *Checker) checkTypeNamesIn(x ast.Node, scope *binder.Scope) {
	if x == nil || (reflect.ValueOf(x).Kind() == reflect.Ptr && reflect.ValueOf(x).IsNil()) {
		return
	}
	if s := c.b.OpenedScope(x); s != nil {
		scope = s
	}
	if t, ok := x.(*ast.ThisExpression); ok && !t.NewTarget && !c.thisReported[t] && c.thisInEnum(t) {
		c.thisReported[t] = true
		c.report(diag.ThisInvalidLocation, t.GetPos())
	}
	if t, ok := x.(*ast.ThisExpression); ok && !t.NewTarget && !c.thisReported[t] && c.thisInNamespace(t) {
		c.thisReported[t] = true
		c.report(diag.ThisInNamespace, t.GetPos())
	}
	if t, ok := x.(*ast.ThisExpression); ok && !t.NewTarget && !c.ImplicitThis && !c.implicitThis[t] && c.thisImplicitlyAny(t) {
		c.implicitThis[t] = true
		c.report(diag.ThisImplicitlyAny, t.GetPos())
	}
	if t, ok := x.(*ast.ThisExpression); ok && !t.NewTarget && !c.opts.CompatJS() && !c.implicitThis[t] && c.capturesGlobalThis(t) {
		c.implicitThis[t] = true
		c.report(diag.ArrowCapturesGlobalThis, t.GetPos())
	}
	if call, ok := x.(*ast.CallExpression); ok {
		if _, super := call.Callee.(*ast.SuperExpression); super && len(call.TypeArgs) > 0 {
			// `super<T>(…)` is TS2754; its type arguments resolve nothing
			ast.ForEachChild(x, func(ch ast.Node) bool { c.checkTypeNamesIn(ch, scope); return true })
			return
		}
	}
	forEachAnnotation(reflect.ValueOf(x), func(ta ast.TypeNode) {
		if ta == nil || ast.IsJSDoc(ta) || isTypeSyntax(x) {
			return // type syntax is checked from the annotation it is part of
		}
		tscope := scope
		if s := c.b.LookupScope(x); s != nil {
			tscope = s
		}
		if s := c.b.AnnotationScope(ta); s != nil {
			tscope = s // where the annotation is written (an interface member's)
		}
		ast.Inspect(ta, func(tn ast.Node) bool {
			if k, ok := tn.(*ast.KeywordType); ok && c.opts.NoAny && (k.Keyword == "any" || k.Keyword == "unknown") {
				c.report(diag.NoAnyType, k.Range.Pos, k.Keyword)
			}
			if r, ok := tn.(*ast.TypeReference); ok && len(r.Qualifier) == 0 {
				if !c.valueAsType(r, tscope) && !c.typeOutOfScope(r, tscope) {
					c.cannotFindType(r.Name, r.Range.Pos)
				}
				c.checkRefTypeArgs(r, tscope)
			}
			switch sig := tn.(type) {
			case *ast.FunctionType:
				c.checkSignatureParams(sig.Parameters, tscope)
			case *ast.MethodSignature:
				c.checkSignatureParams(sig.Parameters, tscope)
				if sig.Type == nil && !c.opts.CompatJS() {
					// noImplicitAny: a method signature with no return type.
					c.report(diag.ReturnImplicitAny, sig.Range.Pos, signatureName(sig.Name))
				}
			case *ast.CallSignature:
				c.checkSignatureParams(sig.Parameters, tscope)
				if sig.Type == nil && !c.opts.CompatJS() {
					c.report(diag.CallSigReturnImplicitAny, sig.Range.Pos)
				}
			}
			return true
		})
	})
	ast.ForEachChild(x, func(ch ast.Node) bool { c.checkTypeNamesIn(ch, scope); return true })
}

// typeOutOfScope reports a type reference that only names types the program
// declares in scopes not enclosing it (a namespace's `Kind` used outside it,
// a block's type used after the block) as TS2304, and says whether it did.
func (c *Checker) typeOutOfScope(r *ast.TypeReference, scope *binder.Scope) bool {
	if resolveTypeName(r.Name, scope) != nil || lookupName(r.Name, scope) != nil {
		return false
	}
	src := sourceName(r.Name)
	found := false
	for _, sym := range c.b.Symbols() {
		if sym.Name != r.Name && sourceName(sym.Name) != src {
			continue
		}
		if sym.Scope == nil || c.inLibrary(sym.Scope) || sym.Scope == c.b.Module || sym.Scope == c.b.Globals || sym.Flags&binder.TypeParameter != 0 {
			return false
		}
		if sym.Flags&binder.Type == 0 {
			continue
		}
		for s := scope; s != nil; s = s.Parent {
			if s == sym.Scope {
				return false
			}
		}
		found = true
	}
	if !found {
		return false
	}
	c.cannotFind(r.Name, r.Range.Pos)
	return true
}

// valueAsType reports a type reference that names a value of the program's
// with no type meaning (`const C = class {}` used as `C`) as TS2749, and
// says whether it did.
func (c *Checker) valueAsType(r *ast.TypeReference, scope *binder.Scope) bool {
	if resolveTypeName(r.Name, scope) != nil {
		return false
	}
	sym := lookupName(r.Name, scope)
	if sym == nil || sym.Flags&binder.Type != 0 || sym.Flags&(binder.Variable|binder.Function) == 0 {
		return false
	}
	if sym.Scope == nil || c.inLibrary(sym.Scope) {
		return false
	}
	src := sourceName(r.Name)
	c.report(diag.ValueUsedAsType, r.Range.Pos, src, src)
	return true
}

// checkImplicitReturn reports a function with no body to infer from (an
// ambient declaration, an overload signature, an abstract method) and no
// return type, as tsc does under noImplicitAny (TS7010).
func (c *Checker) checkImplicitReturn(fd *ast.FunctionDeclaration) {
	if c.opts.CompatJS() || fd.Name == "constructor" || fd.AccessorKind == "set" || c.privateInAmbient(fd) {
		return
	}
	if fd.ReturnType == nil && (fd.Ambient || fd.IsAbstract) && fd.GetPos().Line > 0 {
		c.report(diag.ReturnImplicitAny, namePos(fd.NamePos, fd.GetPos()), signatureName(fd.Name))
	}
	for _, sig := range fd.Overloads {
		if sig.ReturnType == nil && sig.GetPos().Line > 0 {
			c.report(diag.ReturnImplicitAny, namePos(sig.NamePos, sig.GetPos()), signatureName(sig.Name))
		}
	}
}

// privateInAmbient reports a private method of a `declare class`, whose
// implicit anys tsc never reports (isPrivateWithinAmbient).
func (c *Checker) privateInAmbient(fn ast.Node) bool {
	fd, ok := fn.(*ast.FunctionDeclaration)
	if !ok || fd.Visibility != "private" {
		return false
	}
	cd, ok := c.parentOf(fd).(*ast.ClassDeclaration)
	return ok && cd.Ambient
}

// signatureName is a member's name as tsc's diagnostics quote it: a name
// no identifier spells (`"a-b"`) in double quotes.
func signatureName(name string) string {
	name = memberDisplayName(unqualified(sourceName(name)))
	if strings.HasPrefix(name, "[") {
		return name
	}
	for i, r := range name {
		if !(r == '_' || r == '$' || unicode.IsLetter(r) || i > 0 && unicode.IsDigit(r)) {
			if _, err := strconv.ParseFloat(name, 64); err == nil {
				return name
			}
			return strconv.Quote(name)
		}
	}
	return name
}

// unqualified is a namespace member's own name (`M.f` is f).
func unqualified(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 && !strings.HasPrefix(name, "[") {
		return name[i+1:]
	}
	return name
}

// typeKeywords are the type keywords a parameter name can spell
// (tsc's isTypeNodeKind for a keyword).
var typeKeywords = map[string]bool{
	"any": true, "unknown": true, "number": true, "bigint": true, "object": true, "boolean": true,
	"string": true, "symbol": true, "void": true, "undefined": true, "never": true, "null": true,
}

// checkSignatureParams reports a signature's parameter written without a
// type, as tsc's reportImplicitAny does under noImplicitAny: TS7051 when its
// name is a type (`(string) => void`), else TS7006 (TS7019 for a rest).
func (c *Checker) checkSignatureParams(params []*ast.SignatureParameter, scope *binder.Scope) {
	for i, p := range params {
		if p == nil || (p.Type != nil && !p.Untyped) || p.Name == "" || p.Name == "this" || p.Name == "__pattern" {
			continue
		}
		pos := p.Range.Pos
		isType := typeKeywords[p.Name]
		if !isType {
			if sym := lookupName(p.Name, scope); sym != nil && sym.Flags&binder.Type != 0 {
				isType = true
			}
		}
		if isType {
			typeName := p.Name
			if p.Rest {
				typeName += "[]"
			}
			c.report(diag.ParamNameNoType, pos, fmt.Sprintf("arg%d", i), typeName)
			continue
		}
		if p.Rest {
			c.report(diag.RestParamImplicitAny, pos, p.Name)
		} else {
			c.report(diag.ParamImplicitAny, pos, p.Name)
		}
	}
}

var typeAnnotationPtr = reflect.TypeOf((*ast.TypeNode)(nil)).Elem()

// forEachAnnotation calls fn with each type annotation a node's fields hold,
// directly or inside a non-node struct or slice (a parameter, a field).
func forEachAnnotation(v reflect.Value, fn func(ast.TypeNode)) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		if v.Type() == typeAnnotationPtr {
			fn(v.Interface().(ast.TypeNode))
			return
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if !v.Type().Field(i).IsExported() || v.Type().Name() == "InterfaceDeclaration" && v.Type().Field(i).Name == "Heritage" {
				continue // an interface's heritage is checked as written, not as an annotation
			}
			switch f.Kind() {
			case reflect.Interface:
				if f.Type() == typeAnnotationPtr {
					forEachAnnotation(f, fn)
				}
			case reflect.Struct:
				forEachAnnotation(f, fn)
			case reflect.Slice:
				for j := 0; j < f.Len(); j++ {
					e := f.Index(j)
					if e.Kind() == reflect.Struct || e.Type() == typeAnnotationPtr {
						forEachAnnotation(e, fn)
					}
				}
			}
		}
	}
}

// cannotFindType is cannotFind for a type position.
func (c *Checker) cannotFindType(name string, pos ast.Pos) {
	src := sourceName(name)
	if c.knownTypeNames == nil {
		c.knownTypeNames = map[string]bool{}
		for _, s := range c.b.Symbols() {
			if c.b.Program.LibDeclNames[s.Name] {
				c.knownTypeNames[s.Name] = true // a builtin module's: bound by an import only
				continue
			}
			c.knownTypeNames[sourceName(s.Name)] = true
		}
		for n := range c.b.Program.TypeParamNames {
			c.knownTypeNames[n] = true
		}
		for local := range c.b.Program.NodeTypeImports {
			c.knownTypeNames[local] = true
		}
	}
	_, width := nativeWidths[src]
	if c.knownTypeNames[src] || c.knownTypeNames[name] || width || compilerTypeNames[src] || src == "float32" || src == "float64" || lib.NodeTypeName(src) {
		return
	}
	if _, ok := c.unresolvable(name); !ok {
		return
	}
	suggest := c.suggestionCount < 10 // tsc's maximumSuggestionCount
	c.suggestionCount++
	if suggest {
		if near := c.nearKnownName(src); near != "" {
			key := cannotFindKey{src, pos}
			if !c.cannotFound[key] {
				c.cannotFound[key] = true
				c.report(diag.CannotFindNameDidYouMean, pos, src, near)
			}
			return
		}
	}
	c.cannotFind(name, pos)
}

// compilerTypeNames are the type names codegen resolves by name
// (ResolveTypeName) that neither TypeScript's library nor Node declares:
// this compiler's own surfaces', and `intrinsic` (tsc's `Uppercase` body).
var compilerTypeNames = map[string]bool{
	"ClusterAddress": true, "ClusterWorker": true,
	"intrinsic": true,
}

// nearKnownName reports whether name is within tsc's spelling-suggestion
// distance of a known name (getSpellingSuggestion): tsc then reports TS2552.
func (c *Checker) nearKnownName(name string) string {
	var cands []string
	for n := range c.knownTypeNames {
		cands = append(cands, n)
	}
	sort.Strings(cands)
	if s := spellingSuggestion(name, cands); s != "" {
		return s
	}
	names, _ := lib.GlobalNames()
	return spellingSuggestion(name, names)
}

// skipOuter is e without its non-null assertions (skipOuterExpressions;
// the parser keeps no parentheses): `(o.m!)()` calls o's m, with o as
// `this`.
func skipOuter(e ast.Expression) ast.Expression {
	for {
		switch x := e.(type) {
		case *ast.NonNullExpression:
			e = x.Arg
		default:
			return e
		}
	}
}

// memberTarget is the type a literal's property key must have in target:
// its property's, else its index signature's (a numeric key the number
// index's, then the string index's); nil when target has neither.
func memberTarget(target *Type, key string) *Type {
	if tp := target.Prop(key); tp != nil {
		return tp.Type
	}
	if target.NumberIndex != nil && numericName(key) {
		return target.NumberIndex
	}
	return target.StringIndex
}

// hasSpread reports a spread among args.
func (c *Checker) hasSpread(args []ast.Expression) bool {
	for _, a := range args {
		if _, ok := a.(*ast.SpreadElement); ok {
			return true
		}
	}
	return false
}

// displayTarget is the target type tsc names when src is not assignable to
// it: a union that is one type plus null or undefined is named as that type
// when src is neither (`number` for an optional `x?: number`); any other
// target as it is.
func (c *Checker) displayTarget(src, target *Type) *Type {
	if target.Flags&Union == 0 || someMember(src, Null|Undefined) {
		return target
	}
	if nn := c.nonNullable(target); nn != nil && nn.Flags&Union == 0 && nn != target {
		return nn
	}
	return target
}

// checkVarRedeclared reports a `var` declaration node of name whose type is
// not the one the variable's first declaration gave it (TS2403). A
// declaration with neither a type nor an initializer takes the variable's.
func (c *Checker) checkVarRedeclared(node ast.Node, name string, pos ast.Pos) {
	sym := c.symbolOf(node)
	if sym == nil || sym.Name != name || len(sym.Declarations) < 2 || sym.Flags&binder.BlockScopedVariable != 0 {
		return // (a block-scoped one's redeclaration is TS2451, the binder's)
	}
	var self binder.Declaration
	first := true
	for i, d := range sym.Declarations {
		if d.Node == node {
			self, first = d, i == 0
			break
		}
	}
	if first || self.Node == nil {
		return
	}
	want := c.typeOfSymbol(sym)
	got := c.declType(sym, self)
	// tsc's identity relation, approximated as assignability both ways: a
	// structurally identical type (an intersection and its flattening, a
	// mapped type and its expansion) is the same, however it is interned.
	if c.Unanswered(want) || c.Unanswered(got) || want == got {
		return
	}
	gw, wg := c.assignableTo(got, want), c.assignableTo(want, got)
	if gw != no && wg != no {
		return
	}
	// A definite no either way is never identity, whatever the types: an
	// object literal whose method returns an object where the first
	// declaration's returned a number is not the variable's type, and the
	// variable's storage is the first declaration's (`var o = { f() { return
	// 1 } }; var o = { f() { return {} } }`). Short of that, only where the
	// checker's types are exact: an object type (a mapped `Required<T>`, an
	// intersection) may be identical to tsc and not to this checker.
	plain := func(t *Type) bool { return t.Flags&Object != 0 && t.Kind == Anonymous }
	if !(plain(want) && plain(got)) && (!c.exactForIdentity(want) || !c.exactForIdentity(got)) {
		// … except an object type against a primitive one, which no
		// identity makes the same.
		ew, eg := c.exactForIdentity(want), c.exactForIdentity(got)
		if !(ew && got.Flags&Object != 0 || eg && want.Flags&Object != 0) {
			return
		}
	}
	c.report(diag.VarRedeclaredType, pos, sourceName(name), want, got)
}

// exactForIdentity reports a type whose identity this checker decides as
// tsc does: a primitive or literal, or a union, array or tuple of them.
func (c *Checker) exactForIdentity(t *Type) bool {
	switch {
	case t == nil || c.Unanswered(t):
		return false
	case t.Flags&Union != 0:
		for _, m := range t.Types {
			if !c.exactForIdentity(m) {
				return false
			}
		}
		return true
	case t.Flags&Object != 0 && t.Kind == Array:
		return c.exactForIdentity(t.Elem)
	case t.Flags&Object != 0 && t.Kind == Tuple:
		for _, el := range t.Elems {
			if !c.exactForIdentity(el) {
				return false
			}
		}
		return true
	case t.Flags&(Object|TypeParam|Deferred|NonPrimitive) != 0:
		return false
	}
	return true
}

// evolvingDecl reports whether sym is declared by one unannotated variable
// declaration without a value (a binding that may evolve).
func evolvingDecl(sym *binder.Symbol) bool {
	if len(sym.Declarations) != 1 {
		return false
	}
	v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration)
	if !ok || v.TypeAnnot != nil || v.Ambient {
		return false
	}
	a, empty := v.Init.(*ast.ArrayLiteral)
	return v.Init == nil || empty && len(a.Elements) == 0 // `[]`: an evolving array
}

// isPromiseResolve reports tsc's isPromiseResolveArityError callee: the
// first parameter of an executor passed to the global `new Promise`.
func (c *Checker) isPromiseResolve(callee ast.Expression) bool {
	id, ok := callee.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := c.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return false
	}
	fn := sym.Declarations[0].Node
	var first string
	switch f := fn.(type) {
	case *ast.ArrowFunction:
		if len(f.Params) > 0 {
			first = f.Params[0].Name
		}
	case *ast.FunctionExpression:
		if len(f.Params) > 0 {
			first = f.Params[0].Name
		}
	}
	if first != id.Name {
		return false
	}
	n, ok := c.parentOf(fn).(*ast.NewExpression)
	if !ok || len(n.Args) == 0 || ast.Node(n.Args[0]) != fn {
		return false
	}
	target := c.b.NewTarget(n)
	return target != nil && target.Name == "Promise" && c.inLibrary(target.Scope)
}

// checkNeverIterated is tsc's TS2488 (TS2504 when async) for iterating a
// value of type never (`yield* f()` where f only throws), or an object
// type with no iterator method (lacksIterator).
func (c *Checker) checkNeverIterated(x ast.Expression, async bool) {
	t := c.TypeOf(x)
	if t == nil || c.opts.CompatJS() || c.Unanswered(t) {
		return
	}
	// An async iterable without a sync iterator (an async generator's
	// object) in a sync iteration.
	if !async && t.Flags&Object != 0 && t.Flags&Union == 0 && t.Prop("@@asyncIterator") != nil && t.Prop("@@iterator") == nil {
		c.report(diag.NotIterable, startOf(x), t)
		return
	}
	// A primitive other than a string has no iterator (`yield* true`).
	prim := t.Flags&Union == 0 && t.Flags&(NumberLike|BooleanLike|BigIntLike|ESSymbol) != 0
	if t.Flags&Never == 0 && !prim && !c.lacksIterator(t, async) {
		if c.primitiveNextResult(t, async) {
			if async && t.Prop("@@asyncIterator") != nil {
				c.report(diag.AsyncNextResultNoValue, startOf(x))
			} else {
				c.report(diag.NextResultNoValue, startOf(x))
			}
		}
		return
	}
	if async {
		c.report(diag.NotAsyncIterable, startOf(x), t)
	} else {
		c.report(diag.NotIterable, startOf(x), t)
	}
}

// lacksIterator reports an object literal type the checker models whole
// that declares an iterator key but no `[Symbol.iterator]()` method (nor,
// when async, a `[Symbol.asyncIterator]()`): `{ get [Symbol.iterator]()
// {…} }` is not iterable. An object with a `next` method is left to the
// protocol reading.
func (c *Checker) lacksIterator(t *Type, async bool) bool {
	if t.Flags != Object || t.Kind != Anonymous || t.partial || len(t.Calls) > 0 || len(t.Constructs) > 0 ||
		t.StringIndex != nil || t.NumberIndex != nil || t.Prop("next") != nil || t.Symbol != nil && c.inLibrary(t.Symbol.Scope) {
		return false
	}
	// An iterator method: callable, returning an object with a callable
	// `next` (`{ [Symbol.iterator]() { return true } }` is not iterable).
	method := func(name string) bool {
		p := t.Prop(name)
		if p == nil {
			return false
		}
		ft := p.Type
		if ft == nil || c.Unanswered(ft) || ft.Flags&(Any|Unknown) != 0 {
			return true
		}
		if ft.Flags&Object == 0 {
			return false
		}
		var r *Type
		switch {
		case ft.Kind == Function:
			r = ft.Ret()
		case len(ft.Calls) > 0:
			r = ft.Calls[0].Ret()
		default:
			return false
		}
		return c.iteratorResult(r)
	}
	// Only an object declaring an iterator key, not as a method: inference
	// reads `{ length: 2 }` against `Iterable<B>` through iteratedType.
	if t.Prop("@@iterator") == nil && t.Prop("@@asyncIterator") == nil {
		return false
	}
	return !method("@@iterator") && !(async && method("@@asyncIterator"))
}

// primitiveNextResult reports an object literal type whose iterator
// method returns an iterator whose `next()` returns a primitive (tsc's
// TS2490: no `value` property).
func (c *Checker) primitiveNextResult(t *Type, async bool) bool {
	if t.Flags != Object || t.Kind != Anonymous || t.partial {
		return false
	}
	key := "@@iterator"
	if async && t.Prop("@@asyncIterator") != nil {
		key = "@@asyncIterator"
	}
	p := t.Prop(key)
	if p == nil || p.Type == nil || p.Type.Flags&Object == 0 || p.Type.Kind != Function || p.Type.Ret() == nil {
		return false
	}
	it := p.Type.Ret()
	if it.Flags != Object || it.Kind != Anonymous || it.partial {
		return false
	}
	n := it.Prop("next")
	if n == nil || n.Type == nil || n.Type.Flags&Object == 0 || n.Type.Kind != Function || n.Type.Ret() == nil {
		return false
	}
	r := n.Type.Ret()
	if key == "@@asyncIterator" {
		r = c.awaited(r, 0)
	}
	if c.Unanswered(r) || r.Flags&(Any|Unknown|Object|TypeParam) != 0 || r.Flags&Union != 0 {
		return false // (a `next()` that only throws, never, has no value either)
	}
	// tsc caches an iterable's iteration types with the error: reported
	// once per type and protocol.
	k := memberKey{t, key}
	if c.nextReported[k] {
		return false
	}
	c.nextReported[k] = true
	return true
}

// iteratorResult reports whether r, an iterator method's result, may be an
// iterator: an object with a callable `next`, or a type the checker cannot
// rule out (any, unanswered, a member list it does not model whole).
func (c *Checker) iteratorResult(r *Type) bool {
	if r == nil || c.Unanswered(r) || r.Flags&(Any|Unknown) != 0 {
		return true
	}
	for _, m := range members(r) {
		if m.Flags&Object == 0 {
			return false // a primitive, null, undefined or never
		}
		if m.Kind == Anonymous && !m.partial {
			n := m.Prop("next")
			if n == nil || n.Type == nil || n.Type.Flags&Object == 0 || n.Type.Kind != Function && len(n.Type.Calls) == 0 {
				if n == nil || n.Type == nil || !c.Unanswered(n.Type) && n.Type.Flags&(Any|Unknown) == 0 {
					return false
				}
			}
		}
	}
	return true
}

// inAsyncFunction reports whether n's nearest enclosing function is async.
func (c *Checker) inAsyncFunction(n ast.Node) bool {
	for p := c.parentOf(n); p != nil; p = c.parentOf(p) {
		switch f := p.(type) {
		case *ast.FunctionDeclaration:
			return f.IsAsync
		case *ast.FunctionExpression:
			return f.IsAsync
		case *ast.ArrowFunction:
			return f.IsAsync
		}
	}
	return false
}

// checkIterable is tsc's TS2488 for a class instance iterated with no
// [Symbol.iterator], its own or a base's (a class with only `next()`); a
// builtin base (Array, Map, …) iterates as itself.
func (c *Checker) checkIterable(x ast.Expression) {
	t := c.TypeOf(x)
	if t == nil || t.Flags&Object == 0 || t.Kind != Instance || t.Symbol == nil {
		return
	}
	for b := t; b != nil; b = b.Base {
		if b.Prop("@@iterator") != nil {
			return
		}
		// A builtin base iterates as itself; a `next()` alone does not
		// make an iterable.
		if b.Kind != Instance && !c.Unanswered(c.iteratedType(b)) {
			return
		}
	}
	c.report(diag.NotIterable, x.GetPos(), t)
}

// expandTupleRest is fn with a rest parameter of a fixed tuple type spread
// into one parameter per element (`...a: [number, string]` takes two
// arguments), as tsc reads such a signature.
func (c *Checker) expandTupleRest(fn *Type) *Type {
	n := len(fn.Params)
	if !fn.restParam || n == 0 {
		return fn
	}
	rp := fn.Params[n-1]
	if rp.Flags&Object == 0 || rp.Kind != Tuple {
		return fn
	}
	params := append(append([]*Type{}, fn.Params[:n-1]...), rp.Elems...)
	var optionals []bool
	if len(fn.optionals) > 0 || rp.Opt > 0 {
		optionals = make([]bool, n-1, len(params))
		copy(optionals, fn.optionals)
		for i := range rp.Elems {
			optionals = append(optionals, i >= rp.minLen()) // `[x: T, y?: U]`
		}
	}
	out := c.in.function(params, optionals, false, fn.Ret(), fn.Predicate, nil)
	if fn.ThisType != nil {
		cp := *out
		cp.ThisType = fn.ThisType
		out = &cp
	}
	return out
}

// checkNeverRest reports a call of a signature whose rest parameter is
// `never`: no arguments fit it, not even none (TS2345 with the tuple of the
// rest arguments, at the first of them or at the call).
func (c *Checker) checkNeverRest(e *ast.CallExpression, fn *Type) bool {
	n := len(fn.Params)
	if !fn.restParam || n == 0 || fn.Params[n-1].Flags&Never == 0 || len(e.Args) < n-1 {
		return false
	}
	rest := e.Args[n-1:]
	var ts []*Type
	for _, a := range rest {
		t := c.widenUnit(c.TypeOf(a))
		if c.Unanswered(t) {
			return false
		}
		ts = append(ts, t)
	}
	pos := startOf(e)
	if len(rest) > 0 {
		pos = startOf(rest[0])
	}
	c.report(diag.ArgNotAssignable, pos, c.in.tuple(ts), c.neverT)
	return true
}

// checkGenericTupleRest checks a generic call whose rest parameter's type
// names its type parameters (`...args: Args<K, T>`) against the inferred
// instantiation, once that is a tuple or never: the arguments after the
// fixed ones, by count and type.
func (c *Checker) checkGenericTupleRest(e *ast.CallExpression, fn *Type) bool {
	n := len(fn.Params)
	if !fn.restParam || n == 0 || len(e.TypeArgs) > 0 || !hasTypeParam(fn.Params[n-1]) || len(e.Args) < n-1 {
		return false
	}
	for _, a := range e.Args {
		if _, spread := a.(*ast.SpreadElement); spread {
			return false
		}
	}
	m := c.inferArgsRecv(callRecv(e), e.Args, nil, fn, true)
	for _, t := range m {
		if t == nil || c.Unanswered(t) {
			return false
		}
	}
	rp := c.instantiate(fn.Params[n-1], m)
	if c.Unanswered(rp) || hasTypeParam(rp) {
		return false
	}
	inst := c.in.function(append(append([]*Type{}, fn.Params[:n-1]...), rp), nil, true, fn.Ret(), nil, nil)
	if rp.Flags&Never != 0 {
		return c.checkNeverRest(e, inst)
	}
	if rp.Flags&Object == 0 || rp.Kind != Tuple {
		return false
	}
	want := n - 1 + len(rp.Elems)
	if len(e.Args) != want {
		pos := e.Callee.GetPos()
		if len(e.Args) > want {
			pos = e.Args[want].GetPos()
		}
		c.report(diag.ArgCount, pos, arity(want, want), len(e.Args))
		return true
	}
	for i, el := range rp.Elems {
		a := e.Args[n-1+i]
		if at := c.TypeOf(a); !c.Unanswered(at) && !c.assignable(at, el) {
			c.checkAssign(a, el, diag.ArgNotAssignable, startOf(a))
			return true // tsc reports a call's first mismatch
		}
	}
	return false
}

// inferredInstantiation is the substitution a generic call's arguments
// infer, nil when any type parameter is left unanswered or uninferred, or
// type arguments are explicit (checked elsewhere).
func (c *Checker) inferredInstantiation(e *ast.CallExpression, fn *Type) map[*Type]*Type {
	if len(e.TypeArgs) > 0 {
		return nil
	}
	for _, a := range e.Args {
		if _, spread := a.(*ast.SpreadElement); spread {
			return nil
		}
	}
	m, uninferred := c.inferArgsDetailRecv(callRecv(e), e.Args, nil, fn, true)
	for _, tp := range fn.TypeParams {
		if t := m[tp]; t == nil || c.Unanswered(t) || uninferred[tp] {
			return nil
		}
	}
	return m
}

// instantiatedParam is generic parameter p under inst, for checking
// argument a against it as tsc checks a call against its inferred
// signature: nil where that is not modelled (no instantiation, a
// context-sensitive argument, a result still naming a type parameter).
func (c *Checker) instantiatedParam(inst map[*Type]*Type, p *Type, a ast.Expression) *Type {
	if inst == nil {
		return nil
	}
	if contextSensitive(a) {
		// A callback typed from the instantiation is checked against it
		// (tsc's second pass): its result must fit what the other arguments
		// fixed. Other context-sensitive arguments are not modelled.
		switch skipOuter(a).(type) {
		case *ast.ArrowFunction, *ast.FunctionExpression:
		default:
			return nil
		}
	}
	// A literal takes its type from the parameter (a tuple from `[['a',
	// 1]]`), which the argument's own type does not show.
	switch skipOuter(a).(type) {
	case *ast.ArrayLiteral, *ast.ObjectLiteral:
		return nil
	}
	ip := c.instantiate(p, inst)
	if c.Unanswered(ip) || hasTypeParam(ip) || ip.Flags&(Any|Unknown) != 0 {
		return nil
	}
	return ip
}

// hasSpread reports an array literal with a spread element.
func hasSpread(lit *ast.ArrayLiteral) bool {
	for _, x := range lit.Elements {
		if _, ok := x.(*ast.SpreadElement); ok {
			return true
		}
	}
	return false
}

// isOmitted reports an array literal's hole (`[1, , 3]`).
func isOmitted(x ast.Expression) bool {
	n, ok := x.(*ast.NullLiteral)
	return ok && n.Elision
}

// checkEnumMembers reports TypeScript's enum member rules: a computed
// (non-constant) initializer is no const or ambient enum's (TS2474,
// TS1066) and must be a number (TS18033); a member after a computed one
// needs an initializer (TS1061); a const enum's value is finite (TS2477,
// TS2478).
func (c *Checker) checkEnumMembers(n *ast.EnumDeclaration) {
	prior := map[string]ast.EnumConst{}
	afterComputed := false
	// next is the value an initializer-less member takes: one past the
	// previous numeric member, unknown after a string or computed one.
	next, nextOK := 0.0, true
	for _, m := range n.Members {
		if m.Value == nil {
			if afterComputed {
				c.report(diag.EnumMemberNeedsInit, n.GetPos())
			}
			if nextOK {
				prior[m.Name] = ast.EnumConst{Num: next}
				next++
			}
			continue
		}
		v, ok := ast.FoldEnumConst(m.Value, n.Name, prior)
		next, nextOK = v.Num+1, ok && !v.IsStr
		if !ok {
			afterComputed = true
			switch {
			case n.Const:
				c.report(diag.ConstEnumComputed, startOf(m.Value))
			case n.Ambient:
				c.report(diag.EnumAmbientComputed, startOf(m.Value))
			default:
				if t := c.TypeOf(m.Value); !c.Unanswered(t) && t.Flags&(Any|Never) == 0 && !everyMember(t, NumberLike|Int|Float32) {
					c.report(diag.EnumComputedNotNumber, startOf(m.Value), t.String())
				}
			}
			continue
		}
		afterComputed = false
		prior[m.Name] = v
		if n.Const && !v.IsStr {
			switch {
			case math.IsNaN(v.Num):
				c.report(diag.ConstEnumNaN, startOf(m.Value))
			case math.IsInf(v.Num, 0):
				c.report(diag.ConstEnumNonFinite, startOf(m.Value))
			}
		}
	}
}

// inEvalCode reports n as part of code spliced in from a constant string,
// which tsc never checks.
func (c *Checker) inEvalCode(n ast.Node) bool {
	if c.evalNodes == nil {
		c.evalNodes = map[ast.Node]bool{}
		if prog, ok := c.b.Module.Node.(*ast.Program); ok {
			for e := range prog.EvalCode {
				ast.Inspect(e, func(x ast.Node) bool { c.evalNodes[x] = true; return true })
			}
		}
	}
	return n != nil && c.evalNodes[n]
}

// InEvalCode reports n as code spliced in from a constant string (an
// `eval` argument, a Function body): JavaScript whatever the lane.
func (c *Checker) InEvalCode(n ast.Node) bool { return c.inEvalCode(n) }

// isEvalRoot reports e as code spliced in from a constant string (an
// `eval` argument, a Function body).
func (c *Checker) isEvalRoot(e ast.Expression) bool {
	prog, ok := c.b.Module.Node.(*ast.Program)
	return ok && prog.EvalCode[e]
}

// checkSyntacticUse reports what tsc rejects from an expression's form
// alone: a comma operand whose value is dropped and that has no side
// effects, and a condition that is always truthy or always falsy.
func (c *Checker) checkSyntacticUse(n ast.Node) {
	if c.inEvalCode(n) {
		return
	}
	switch n := n.(type) {
	case *ast.SequenceExpression:
		if isVoidSequence(n) {
			return // `void e`, as the parser lowers it
		}
		for i, x := range n.Exprs[:max(len(n.Exprs)-1, 0)] {
			// `(0, obj.f)()` calls f without obj as its receiver.
			if lit, ok := x.(*ast.NumberLiteral); ok && lit.Value == "0" && i == 0 && len(n.Exprs) == 2 {
				if call, ok := c.parentOf(n).(*ast.CallExpression); ok && call.Callee == ast.Expression(n) {
					continue
				}
			}
			if sideEffectFree(x) {
				c.report(diag.CommaLeftUnused, startOf(x))
			}
		}
	case *ast.FunctionDeclaration:
		if n.AccessorKind == "set" && len(n.Params) == 1 && n.Params[0].Default != nil {
			c.report(diag.SetterParamInit, n.Params[0].Default.GetPos())
		}
	case *ast.ObjectLiteral:
		for _, p := range n.Properties {
			if fe, ok := p.Value.(*ast.FunctionExpression); ok && p.AccessorKind == "set" && len(fe.Params) == 1 && fe.Params[0].Default != nil {
				c.report(diag.SetterParamInit, fe.Params[0].Default.GetPos())
			}
		}
	case *ast.IfStatement:
		c.checkTruthiness(n.Test)
	case *ast.WhileStatement:
		c.checkTruthiness(n.Test)
	case *ast.DoWhileStatement:
		c.checkTruthiness(n.Test)
	case *ast.ForStatement:
		c.checkTruthiness(n.Test)
	case *ast.ConditionalExpression:
		c.checkTruthiness(n.Test)
	case *ast.UnaryExpression:
		if n.Op == "!" {
			c.checkTruthiness(n.Arg)
		}
	case *ast.BinaryExpression:
		if n.Op == "&&" || n.Op == "||" {
			c.checkTruthiness(n.Left)
		}
	}
}

// truthiness is a condition's syntactic truthiness: 1 always truthy, -1
// always falsy, 0 either.
func truthiness(e ast.Expression) int {
	switch x := skipAssertions(e).(type) {
	case *ast.NumberLiteral:
		// `while (1)` and `while (0)` are idioms tsc allows.
		return 0
	case *ast.ArrayLiteral, *ast.ObjectLiteral, *ast.ArrowFunction, *ast.FunctionExpression,
		*ast.ClassExpression, *ast.NewRegExpExpression:
		return 1
	case *ast.NullLiteral:
		return -1
	case *ast.SequenceExpression:
		if isVoidSequence(x) {
			return -1
		}
	case *ast.StringLiteral:
		if x.Value == "" {
			return -1
		}
		return 1
	case *ast.TemplateLiteral:
		if len(x.Exprs) == 0 {
			if len(x.Quasis) == 0 || x.Quasis[0] == "" {
				return -1
			}
			return 1
		}
	case *ast.ConditionalExpression:
		if a, b := truthiness(x.Consequent), truthiness(x.Alternate); a == b {
			return a
		}
	case *ast.Identifier:
		if x.Name == "undefined" {
			return -1
		}
	}
	return 0
}

func (c *Checker) checkTruthiness(e ast.Expression) {
	if e == nil {
		return
	}
	switch truthiness(e) {
	case 1:
		c.report(diag.AlwaysTruthy, startOf(e))
	case -1:
		c.report(diag.AlwaysFalsy, startOf(e))
	}
}

// skipAssertions is e without the type assertions around it.
func skipAssertions(e ast.Expression) ast.Expression {
	for {
		switch x := e.(type) {
		case *ast.AsExpression:
			e = x.Expr
		case *ast.NonNullExpression:
			e = x.Arg
		default:
			return e
		}
	}
}

// sideEffectFree is tsc's isSideEffectFree: evaluating e can do nothing
// but yield its value.
func sideEffectFree(e ast.Expression) bool {
	switch x := skipAssertions(e).(type) {
	case *ast.Identifier, *ast.StringLiteral, *ast.NumberLiteral, *ast.BooleanLiteral, *ast.NullLiteral,
		*ast.NewRegExpExpression, *ast.FunctionExpression, *ast.ClassExpression, *ast.ArrowFunction,
		*ast.ArrayLiteral, *ast.ObjectLiteral, *ast.TaggedTemplateExpression:
		return true
	case *ast.TemplateLiteral:
		return true
	case *ast.ConditionalExpression:
		return sideEffectFree(x.Consequent) && sideEffectFree(x.Alternate)
	case *ast.BinaryExpression:
		return sideEffectFree(x.Left) && sideEffectFree(x.Right)
	case *ast.UnaryExpression:
		switch x.Op {
		case "!", "+", "-", "~":
			return sideEffectFree(x.Arg)
		case "typeof":
			return true
		}
	}
	return false
}

// isVoidSequence reports whether s is `void e`: the parser lowers it to
// `e, undefined`, the undefined marked Void.
func isVoidSequence(s *ast.SequenceExpression) bool {
	if len(s.Exprs) != 2 {
		return false
	}
	u, ok := s.Exprs[1].(*ast.NullLiteral)
	return ok && u.Void
}

// resolveResult resolves a function literal's deferred result, as tsc
// resolves each function's return type while checking it: one its own
// return asks for (`const g = () => g()`) is TS7023.
func (c *Checker) resolveResult(fn ast.Expression) {
	if t := c.TypeOf(fn); !c.Unanswered(t) && t.Flags&Object != 0 && t.Kind == Function {
		t.Ret()
	}
}

// methodComputedKey reports e inside the computed name of an object
// literal's method or accessor, a name tsc's binder gives no flow node.
func (c *Checker) methodComputedKey(e ast.Node) bool {
	for n, p := ast.Node(e), c.ParentOf(e); p != nil; n, p = p, c.ParentOf(p) {
		if o, ok := p.(*ast.ObjectLiteral); ok {
			for _, pr := range o.Properties {
				if pr.KeyExpr == n {
					fe, isFE := pr.Value.(*ast.FunctionExpression)
					return isFE && fe.Method
				}
			}
			return false
		}
	}
	return false
}

// unassignedVar reports a read of a var some path reaches before any value
// is stored in it, its type holding no undefined (TS2454; tsc types a
// hoisted var undefined until assigned, and reports the read).
func (c *Checker) unassignedVar(id *ast.Identifier, sym *binder.Symbol) bool {
	if c.evolving[sym] || c.evolvingArray[sym] || c.ImplicitAnyVariables || c.ownInitializerRead(id, sym) || c.methodComputedKey(id) {
		return false
	}
	r := ref{sym: sym}
	if !c.b.UnassignedVarRead(id, func(f *binder.FlowNode) bool {
		if sw, ok := f.Node.(*ast.SwitchStatement); ok {
			return c.switchMayNarrow(sw, r)
		}
		cond, ok := f.Node.(ast.Expression)
		if lit, isLit := cond.(*ast.BooleanLiteral); isLit && lit.Value == (f.Flags&binder.FlowFalseCondition != 0) {
			return true // `if (true)`'s else: unreachable, as tsc's binder builds it
		}
		return ok && c.condMayNarrow(cond, r, 0)
	}) {
		return false
	}
	t := c.typeOfSymbol(sym)
	if c.Unanswered(t) || t.Flags&(Any|Unknown|Void|Undefined) != 0 {
		return false
	}
	for _, m := range members(t) {
		if m.Flags&(Any|Unknown|Void|Undefined) != 0 {
			return false
		}
	}
	return true
}

// checkSpreadArity reports a call whose first spread argument is not a
// tuple and has no place among the callee's parameters (TS2556): before
// the arguments it must fill, or past every parameter of a signature with
// no rest (tsc's hasCorrectArity for a spread).
func (c *Checker) checkSpreadArity(e *ast.CallExpression) {
	if c.opts.CompatJS() {
		return
	}
	fn := c.TypeOf(e.Callee)
	if c.Unanswered(fn) || fn.Flags&Object == 0 || fn.Kind != Function || len(fn.Overloads) > 0 || len(fn.TypeParams) > 0 {
		return
	}
	// i is the argument's position as tsc's getEffectiveCallArguments
	// counts it: a spread tuple's elements, and a rest tuple's head (`...
	// [string, string, ...string[]]`), each an argument of their own.
	i := 0
	for _, a := range e.Args {
		sp, ok := a.(*ast.SpreadElement)
		if !ok {
			i++
			continue
		}
		st := c.TypeOf(sp.Arg)
		if c.Unanswered(st) || st.Flags&Object == 0 || st.Flags&Union != 0 {
			return
		}
		if st.Kind == Tuple {
			i += len(st.Elems)
			continue
		}
		if st.Kind != Array || c.contextualRest(sp.Arg) {
			return
		}
		i += len(st.Head)
		if i < fn.MinArgs() || !fn.restParam && i >= len(fn.Params) {
			c.report(diag.SpreadArgument, sp.GetPos())
		}
		return
	}
}

// contextualRest reports x naming an unannotated rest parameter of a
// function expression or arrow, which its context types (a tuple, from
// `Parameters<F>`) where this checker may infer an array.
func (c *Checker) contextualRest(x ast.Expression) bool {
	id, ok := x.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := c.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return false
	}
	var params []ast.Param
	switch f := sym.Declarations[0].Node.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	default:
		return false
	}
	for _, p := range params {
		if p.Name == id.Name {
			return p.Rest && p.Type == nil
		}
	}
	return false
}

// callbackResultMismatch reports the parameter names of the first pair of
// callback parameters (non-method function types on both sides) that fails
// only in its result: the source callback's result against the target's.
func (c *Checker) callbackResultMismatch(s, t *Type) (string, string, bool) {
	fn := func(x *Type) bool {
		return x != nil && x.Flags&Object != 0 && x.Kind == Function && !x.method && len(x.Overloads) == 0 && len(x.TypeParams) == 0
	}
	if !fn(s) || !fn(t) || s.restParam || t.restParam {
		return "", "", false
	}
	for i := range s.Params {
		if i >= len(t.Params) {
			break
		}
		sp, tp := s.Params[i], t.Params[i]
		if !fn(sp) || !fn(tp) || c.relate(tp, sp, relation{missingIsNo: true}, 0) != no {
			continue
		}
		// The parameters fit; the results do not.
		if c.relate(c.in.withResult(tp, c.anyT), c.in.withResult(sp, c.anyT), relation{missingIsNo: true}, 0) == yes && c.relate(tp.Ret(), sp.Ret(), relation{missingIsNo: true}, 0) == no {
			return paramName(s, i), paramName(t, i), true
		}
	}
	return "", "", false
}

// paramName is a signature's i'th parameter's declared name, else argN.
func paramName(f *Type, i int) string {
	if i < len(f.ParamNames) && f.ParamNames[i] != "" {
		return f.ParamNames[i]
	}
	return "arg" + strconv.Itoa(i)
}

// isTypeSyntax reports a type node or type member: its type fields are
// parts of the annotation it belongs to, not annotations of their own.
func isTypeSyntax(n ast.Node) bool {
	switch n.(type) {
	case ast.TypeNode, ast.TypeMember, *ast.SignatureParameter, *ast.TypeParameter, *ast.TemplateLiteralTypeSpan:
		return true
	}
	return false
}

// forInKeyOfIntersection reports e reading obj, a deferred intersection
// with a type parameter in it, by the variable of a `for…in` over it.
func (c *Checker) forInKeyOfIntersection(e *ast.IndexExpression, obj *Type) bool {
	id, ok := e.Index.(*ast.Identifier)
	if !ok || obj.Flags&Deferred == 0 {
		return false
	}
	if _, isIn := obj.deferNode.(*ast.IntersectionType); !isIn || !c.generic(obj) {
		return false
	}
	sym, _ := c.b.Resolve(id)
	if sym == nil {
		return false
	}
	for _, d := range sym.Declarations {
		if fi, ok := d.Node.(*ast.ForInStatement); ok && fi.VarName == sym.Name {
			return c.TypeOf(fi.Object) == obj
		}
	}
	return false
}

// couldHaveSingletons reports a target a literal source is named as itself
// against, not its base type (tsc's typeCouldHaveTopLevelSingletonTypes): a
// unit type (a literal, null, undefined, void), a union with one, a type
// parameter constrained to one; plain boolean is not.
func (c *Checker) couldHaveSingletons(t *Type, depth int) bool {
	switch {
	case t == nil || depth > 8:
		return false
	case t.Flags&Union != 0:
		for _, m := range t.Types {
			if c.couldHaveSingletons(m, depth+1) {
				return true
			}
		}
		return false
	case t.Flags&TypeParam != 0:
		return t.Constraint != nil && c.couldHaveSingletons(t.Constraint, depth+1)
	case t.Flags&Deferred != 0:
		b := c.baseConstraint(t, 0)
		return b != nil && b != t && c.couldHaveSingletons(b, depth+1)
	}
	// A null or undefined target names a literal as itself (`'5' … to
	// type 'null'`); one beside other members (`BodyInit | null`) does not.
	return t.Flags&Literal != 0 || depth == 0 && t.Flags&(Nullish|Void) != 0
}

// numericLiteralName reports a property name a number writes as itself
// (`"3"`, `"1.5"`, not `"03"`): tsc's isNumericLiteralName.
func numericLiteralName(name string) bool {
	v, err := strconv.ParseFloat(name, 64)
	return err == nil && ast.JSNumberString(v) == name
}

// constEnumRef reports id naming a const enum.
func (c *Checker) constEnumRef(id *ast.Identifier) bool {
	sym, _ := c.b.Resolve(id)
	return sym != nil && sym.Flags&binder.ConstEnum != 0
}

// symbolIndexed reports obj an instance of a class (or one it extends) with
// a member keyed by a `symbol`-typed expression, which gives the class tsc's
// implicit `[x: symbol]` index signature.
func (c *Checker) symbolIndexed(obj *Type) bool {
	for depth := 0; obj != nil && obj.Symbol != nil && depth < 32; depth++ {
		var cd *ast.ClassDeclaration
		for _, d := range obj.Symbol.Declarations {
			if x, ok := d.Node.(*ast.ClassDeclaration); ok {
				cd = x
			}
		}
		if cd == nil {
			return false
		}
		for _, m := range cd.Methods {
			if m.Key != nil && symbolKey(c.TypeOf(m.Key)) {
				return true
			}
		}
		for i := range cd.Fields {
			if k := cd.Fields[i].Key; k != nil && symbolKey(c.TypeOf(k)) {
				return true
			}
		}
		obj = c.baseInstance(cd, obj.Symbol)
	}
	return false
}

// generatorFits reports a generator's annotation t one its generator object
// is assignable to: an iterator type, or a union with one of the
// generator's kind (`Generator<…> | AsyncGenerator<…>`).
func (c *Checker) generatorFits(t *Type, async bool) bool {
	if t.Flags&Union == 0 && c.iteratorLike(t) {
		return true
	}
	if t.Flags&(Void|Union) == 0 {
		// Another object type (`{}`, an interface extending Iterator):
		// the generator object assigned to it.
		if g := c.generatorOf(c.anyT, async); g != nil && !c.Unanswered(g) {
			return c.assignableTo(g, t) != no
		}
		return false
	}
	for _, m := range t.Types {
		if m.Flags&(Any|Unknown) != 0 {
			return true
		}
		if !c.iteratorLike(m) || m.Symbol == nil {
			continue
		}
		if strings.HasPrefix(m.Symbol.Name, "Async") == async {
			return true
		}
	}
	return false
}

// checkDeleteOperand is tsc's checkDeleteExpression: the operand is a
// property reference (TS2703), not a read-only property (TS2704), and one
// whose type admits undefined (TS2790, under strictNullChecks).
func (c *Checker) checkDeleteOperand(arg ast.Expression) {
	switch a := arg.(type) {
	case *ast.IndexExpression:
		return
	case *ast.MemberExpression:
		if strings.HasPrefix(a.Property, "#") {
			return // a private name is its own early error
		}
		ot := c.TypeOf(a.Object)
		if !c.Unanswered(ot) && ot.Flags&Union == 0 && ot.Flags&Object == 0 {
			if am := c.apparentType(ot); am != nil {
				ot = am // a primitive's (`s.length`)
			}
		}
		if c.Unanswered(ot) || ot.Flags&Object == 0 || ot.Flags&Union != 0 {
			return
		}
		p := ot.Prop(a.Property)
		if p == nil && c.expandoMember(a) {
			// An expando property: optional only when it may be undefined.
			if et := c.expandoType(a.Object, a.Property); et != nil && !c.Unanswered(et) && !someMember(et, Undefined|Any|Unknown|Void) {
				c.report(diag.DeleteNotOptional, startOf(a))
			}
			return
		}
		if p == nil || p.Type == nil || c.Unanswered(p.Type) {
			return
		}
		if p.Readonly {
			c.report(diag.DeleteReadonly, startOf(a))
			return
		}
		if p.Optional || p.Type.Flags&(Any|Unknown|Never) != 0 {
			return
		}
		admits := p.Type.Flags&(Undefined|Void) != 0
		if p.Type.Flags&Union != 0 {
			for _, m := range p.Type.Types {
				admits = admits || m.Flags&(Undefined|Void|Any|Unknown) != 0
			}
		}
		if !admits {
			c.report(diag.DeleteNotOptional, startOf(a))
		}
	case *ast.SuperExpression, *ast.Identifier:
		return // (an identifier's is strict mode's TS1102)
	default:
		c.report(diag.DeleteNotReference, arg.GetPos())
	}
}

// objectOperand reports an object type (no union, no type parameter), which
// is never assignable to number or bigint.
func objectOperand(t *Type) bool {
	return t != nil && t.Flags&Object != 0 && t.Flags&(Union|Any|Unknown|TypeParam|Deferred) == 0
}

// symbolLike reports a type that is, or has a member that is, a symbol
// (tsc's maybeTypeOfKind ESSymbolLike).
func (c *Checker) symbolLike(t *Type) bool {
	if t.Flags&ESSymbol != 0 {
		return true
	}
	if t.Flags&Union != 0 {
		for _, m := range t.Types {
			if c.symbolLike(m) {
				return true
			}
		}
	}
	return false
}

// symbolOperand reports a symbol operand of an operator that converts it
// (tsc's checkForDisallowedESSymbolOperand, TS2469): the left one first.
func (c *Checker) symbolOperand(op string, left, right ast.Expression, l, r *Type) bool {
	sym := func(t *Type) bool {
		return t != nil && !c.Unanswered(t) && t.Flags&Any == 0 && someMember(t, ESSymbol)
	}
	switch {
	case sym(l):
		c.report(diag.SymbolOperand, startOf(left), op)
	case sym(r):
		c.report(diag.SymbolOperand, startOf(right), op)
	default:
		return false
	}
	return true
}

// ownConstructorWrite reports a store to readonly field p as `this.p` in
// the constructor of the class that declares it (not a function nested in
// it, an arrow included), the one place tsc lets it be written.
func (c *Checker) ownConstructorWrite(m *ast.MemberExpression, p *Property) bool {
	if _, isThis := m.Object.(*ast.ThisExpression); !isThis || p.Owner == nil {
		return false
	}
	decl := classDecl(p.Owner)
	if decl == nil || decl.Constructor == nil {
		return false
	}
	for cur := c.parentOf(m); cur != nil; cur = c.parentOf(cur) {
		switch cur.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction:
			return cur == ast.Node(decl.Constructor)
		case *ast.ClassDeclaration:
			return false
		}
	}
	return false
}

// checkAbstractImplemented reports a concrete class leaving an abstract
// member of a base class unimplemented (TS2515, TS2654 for several of one
// base): a member is implemented by a concrete declaration in the class or
// in a class between it and the base.
func (c *Checker) checkAbstractImplemented(cd *ast.ClassDeclaration) {
	if cd.IsAbstract || cd.BaseClass == "" || cd.BaseQualified || c.opts.CompatJS() {
		return
	}
	sym := c.symbolOf(cd)
	if sym == nil || mergedWithInterface(sym) {
		// (A merged interface's own bases may implement the members.)
		return
	}
	concrete := map[string]bool{}
	note := func(d *ast.ClassDeclaration) {
		for _, m := range d.Methods {
			if !m.IsAbstract && !m.IsStatic {
				concrete[m.Name] = true
			}
		}
		for _, f := range d.Fields {
			if !f.Abstract && !f.Static {
				concrete[f.Name] = true
			}
		}
	}
	note(cd)
	cur, scope := cd, sym.Scope
	for depth := 0; depth < 64 && cur.BaseClass != "" && !cur.BaseQualified; depth++ {
		bs := resolveTypeName(cur.BaseClass, scope)
		if bs == nil || bs.Flags&binder.Class == 0 {
			return
		}
		base := firstClassDecl(bs)
		if base == nil {
			return
		}
		var missing []string
		if !base.IsAbstract {
			// Only an abstract class declares abstract members; a
			// declared class's bodyless methods are implemented elsewhere.
			note(base)
			cur, scope = base, bs.Scope
			continue
		}
		seen := map[string]bool{}
		add := func(name string) {
			if !concrete[name] && !seen[name] {
				seen[name] = true
				missing = append(missing, name)
			}
		}
		for _, m := range base.Methods {
			if m.IsAbstract && !m.IsStatic {
				add(m.Name)
			}
		}
		for _, f := range base.Fields {
			if f.Abstract && !f.Static {
				add(f.Name)
			}
		}
		if len(missing) > 0 {
			c.reportAbstractMissing(cd, ast.Unmangle(base.Name), missing)
			return
		}
		note(base)
		cur, scope = base, bs.Scope
	}
}

// valueDecls counts sym's declarations that are values, not an interface or
// type alias merged with them (`interface Symbol` beside `var Symbol`).
func valueDecls(sym *binder.Symbol) int {
	n := 0
	for _, d := range sym.Declarations {
		switch d.Node.(type) {
		case *ast.InterfaceDeclaration, *ast.TypeAliasDeclaration:
		default:
			n++
		}
	}
	return n
}

// firstClassDecl is sym's first class declaration: a merged class is
// abstract exactly when that one is, as in tsc.
func firstClassDecl(sym *binder.Symbol) *ast.ClassDeclaration {
	for _, d := range sym.Declarations {
		if cd, ok := d.Node.(*ast.ClassDeclaration); ok {
			return cd
		}
	}
	return nil
}

// reportAbstractMissing reports cd leaving base's abstract members missing
// unimplemented, with tsc's message for the count and for a class
// expression, at the class's name (an expression's `class` keyword).
func (c *Checker) reportAbstractMissing(cd *ast.ClassDeclaration, base string, missing []string) {
	quote := func(ms []string) string {
		q := make([]string, len(ms))
		for i, m := range ms {
			q[i] = "'" + m + "'"
		}
		return strings.Join(q, ", ")
	}
	name := ast.Unmangle(cd.Name)
	expr := strings.HasPrefix(name, "$ClassExpr")
	at := cd.GetPos()
	if !expr && cd.NamePos.Line > 0 {
		at = cd.NamePos
	}
	switch {
	case len(missing) == 1 && expr:
		c.report(diag.AbstractExprMissing, at, missing[0], base)
	case len(missing) == 1:
		c.report(diag.AbstractMissing, at, name, missing[0], base)
	case len(missing) > 5 && expr:
		c.report(diag.AbstractExprMissingMore, at, base, quote(missing[:4]), len(missing)-4)
	case len(missing) > 5:
		c.report(diag.AbstractMissingMore, at, name, base, quote(missing[:4]), len(missing)-4)
	case expr:
		c.report(diag.AbstractExprMissingMany, at, base, quote(missing))
	default:
		c.report(diag.AbstractMissingMany, at, name, base, quote(missing))
	}
}

// undefaultedProps is a pattern's properties without a default, nested
// patterns likewise: the ones an object literal source must have.
func undefaultedProps(props []ast.DestructProp) []ast.DestructProp {
	var out []ast.DestructProp
	for _, p := range props {
		if p.Default != nil {
			continue
		}
		if p.SubObject != nil {
			p.SubObject = undefaultedProps(p.SubObject)
		}
		out = append(out, p)
	}
	return out
}

// CtorReturnsForeign reports whether cd's constructor may return an object
// that is not an instance of its class (or of a subclass): `new` then
// yields that object, which the class's instance layout cannot stand for.
// An instance, a primitive, null and undefined are not foreign.
func (c *Checker) CtorReturnsForeign(cd *ast.ClassDeclaration) bool {
	if cd.Constructor == nil || cd.Constructor.Body == nil {
		return false
	}
	self := c.classInstance(cd)
	foreign := false
	ast.Inspect(cd.Constructor.Body, func(m ast.Node) bool {
		switch r := m.(type) {
		case *ast.FunctionExpression, *ast.ArrowFunction, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
			return false // another function's returns
		case *ast.ReturnStatement:
			if r.Value == nil {
				return true
			}
			// A cast changes the static type, not the value returned.
			v := r.Value
			for as, ok := v.(*ast.AsExpression); ok; as, ok = v.(*ast.AsExpression) {
				v = as.Expr
			}
			t := c.TypeOf(v)
			if c.Unanswered(t) || c.Unanswered(self) {
				return true
			}
			for _, mt := range members(t) {
				if mt.Flags&(Any|Unknown) == 0 && mt.Flags&Object == 0 {
					continue // a primitive, null, undefined
				}
				if mt.Flags&Object != 0 && mt.Kind == Instance && c.derivesFrom(mt, self) {
					continue
				}
				foreign = true
			}
		}
		return !foreign
	})
	return foreign
}

// namespaceExports is the set of members the namespace x names exports
// (its object's properties), and whether x names a namespace of the
// program.
func (c *Checker) namespaceExports(x ast.Expression) (map[string]bool, bool) {
	prog, ok := c.b.Module.Node.(*ast.Program)
	name := nsPath(x)
	if !ok || name == "" {
		return nil, false
	}
	var sym *binder.Symbol
	switch v := x.(type) {
	case *ast.Identifier:
		sym, _ = c.b.Resolve(v)
	case *ast.MemberExpression:
		sym = c.namespaceMember(v)
	}
	if sym == nil || c.b.NamespaceScope(sym) == nil || mergesEnum(sym) || sym.Flags&(binder.Function|binder.Class|binder.Enum) != 0 {
		return nil, false // no namespace, or one merged into another value
	}
	declared := false
	for _, g := range prog.NamespaceGroups {
		declared = declared || g.Name == name
	}
	if !declared {
		return nil, false // an ambient one (`declare namespace`): its own members
	}
	list := prog.NamespaceExports[name]
	set := map[string]bool{}
	for _, ex := range list {
		set[ex.Name] = true
	}
	return set, true
}

// nsPath is `A.B.C` for a chain of identifier member reads as the
// program names them (module suffixes kept), "" for any other expression.
func nsPath(x ast.Expression) string {
	switch v := x.(type) {
	case *ast.Identifier:
		return v.Name
	case *ast.MemberExpression:
		if head := nsPath(v.Object); head != "" {
			return head + "." + v.Property
		}
	}
	return ""
}

// declaredExported reports a declaration of sym carries `export`.
func (c *Checker) declaredExported(sym *binder.Symbol) bool {
	for _, d := range sym.Declarations {
		if c.b.Exported(d.Node) {
			return true
		}
		// An ambient member (`export declare var n`) has no run-time
		// property to list; its `export` is not kept apart, so it reads
		// as exported.
		switch n := d.Node.(type) {
		case *ast.VarDeclaration:
			if n.Ambient {
				return true
			}
		case *ast.FunctionDeclaration:
			if n.Ambient {
				return true
			}
		case *ast.ClassDeclaration:
			if n.Ambient {
				return true
			}
		}
	}
	return false
}

// checkAssignRetypes reports an Object.assign whose source gives a field of
// its (statically laid out) target a value its type does not admit.
func (c *Checker) checkAssignRetypes(e *ast.CallExpression) {
	if _, fresh := e.Args[0].(*ast.ObjectLiteral); fresh {
		return // a fresh target takes the merged layout
	}
	tt := c.TypeOf(e.Args[0])
	if c.Unanswered(tt) || tt.Flags&Object == 0 || tt.Flags&(Any|Unknown|Union) != 0 || tt.Kind != Anonymous && tt.Kind != Interface {
		return
	}
	for _, a := range e.Args[1:] {
		if _, spread := a.(*ast.SpreadElement); spread {
			return
		}
		st := c.TypeOf(a)
		if c.Unanswered(st) || st.Flags&Object == 0 || st.Flags&(Any|Unknown|Union) != 0 {
			continue
		}
		for _, sp := range st.Props {
			if tp := tt.Prop(sp.Name); tp != nil && sp.Type != nil && tp.Type != nil && c.assignableTo(sp.Type, tp.Type) == no {
				c.report(diag.AssignRetypesField, a.GetPos(), sp.Name)
				return
			}
		}
	}
}

// literalBinding reports a name whose one declaration binds an object
// literal: a binding the compiler can give a layout of its own.
func (c *Checker) literalBinding(x ast.Expression) bool {
	id, ok := x.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := c.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return false
	}
	vd, ok := sym.Declarations[0].Node.(*ast.VarDeclaration)
	if !ok {
		return false
	}
	_, lit := vd.Init.(*ast.ObjectLiteral)
	return lit
}

// unionConstructsTakeTypeArgs reports that every member of the union t
// has a construct signature taking n type arguments.
func (c *Checker) unionConstructsTakeTypeArgs(t *Type, n int) bool {
	for _, m := range t.Types {
		if m.Flags&Object == 0 || len(m.Constructs) == 0 || !c.constructsTakeTypeArgs(m.Constructs, n) {
			return false
		}
	}
	return true
}

// classValueToConstructType reports a program class's value (`typeof Q`),
// its instance fully known, against an object type with construct
// signatures (`new (...a: any[]) => { hi(): string }`): what the instance
// lacks decides, as for a function's result.
func (c *Checker) classValueToConstructType(src, target *Type) bool {
	if target.Flags&Object == 0 || target.Flags&Union != 0 || len(target.Constructs) == 0 || len(target.Calls) > 0 {
		return false
	}
	if src.Flags&Object == 0 || src.Flags&Union != 0 {
		return false
	}
	ct := c.signaturesOf(src, true)
	if c.Unanswered(ct) || ct.Ret() == nil {
		return false
	}
	inst := ct.Ret()
	return inst.Flags&Object != 0 && inst.Kind == Instance && !inst.partial && inst.Symbol != nil &&
		inst.Symbol.Flags&binder.Class != 0 && !c.inLibrary(inst.Symbol.Scope)
}
