package checker

import (
	"KlainMainLang/lib"
	"reflect"
	"strconv"
	"strings"

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
	}
	return c.diags
}

// CheckFiles is Check over a program the resolver merged from several
// files: fileOf names the file each top-level statement came from, and every
// diagnostic is attributed to it. (A worker module's body is not bound yet,
// so not checked.)
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
	}
	c.file = ""
	return c.diags
}

// strictHint tells the user what the strict lane's type errors are.
const strictHint = "TypeScript rejects this program; -compat=js compiles it as JavaScript"

func (c *Checker) report(m *diag.Message, pos ast.Pos, args ...any) {
	d := diag.New(m, diag.Span{File: c.file, Pos: diag.Pos{Line: pos.Line, Col: pos.Col}}, args...)
	d.Hint = strictHint
	c.diags = append(c.diags, d)
}

// checkNode checks n and its children; ret is the declared return type of
// the function n is in (nil when it has none, or is not a function's body).
func (c *Checker) checkNode(n ast.Node, ret *Type) {
	switch n := n.(type) {
	case *ast.FunctionDeclaration:
		c.checkParams(n.Params, n)
		ret = c.declaredReturn(n, n.ReturnType, n.IsAsync || n.IsGenerator)
		if n.Body != nil {
			c.checkNode(n.Body, ret)
		}
		return
	case *ast.FunctionExpression:
		c.checkParams(n.Params, n)
		if n.Body != nil {
			c.checkNode(n.Body, c.declaredReturn(n, n.RetType, n.IsAsync || n.IsGenerator))
		}
		return
	case *ast.ArrowFunction:
		c.checkParams(n.Params, n)
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
		if n.BaseClass != "" && !n.BaseQualified {
			if sym := c.symbolOf(n); sym != nil && lookupName(n.BaseClass, sym.Scope) == nil {
				c.cannotFind(n.BaseClass, n.GetPos())
			}
		}
	case *ast.InterfaceDeclaration:
		c.checkIndexedProperties(n)
	case *ast.TryStatement:
		// `catch ({ message })`: the caught value is unknown under
		// useUnknownInCatchVariables, which has no properties (TS2339).
		if cl := n.Catch; cl != nil && cl.ObjectPattern != nil && cl.ParamType == nil && !c.CatchVariablesAny {
			for _, p := range cl.ObjectPattern {
				c.report(diag.PropertyNotExist, cl.Pos, p.Key, c.unknownT)
			}
		}
	case *ast.NewExpression:
		// A bare `new X(…)` the binder did not resolve: a builtin sema did
		// not map (a module-only one never imported) or an undeclared name.
		if !n.Qualified && c.b.NewTarget(n) == nil && !strings.Contains(n.ClassName, ".") {
			c.unresolvedValue(n.ClassName, n, n.GetPos(), c.b.LookupScope(n))
		}
		if sym := c.b.NewTarget(n); sym != nil && sym.Flags&binder.Class != 0 {
			c.checkConstructorAccess(n, sym)
		}
	case *ast.VarDeclaration:
		if n.TypeAnnot != nil && n.Init != nil {
			if sym := c.symbolOf(n); sym != nil {
				c.checkAssign(n.Init, c.typeOfSymbol(sym), diag.NotAssignable, n.GetPos())
			}
		}
	case *ast.AssignmentExpression:
		if c.checkTarget(n.Left) && n.Op == "=" {
			switch n.Left.(type) {
			case *ast.Identifier, *ast.MemberExpression:
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
			c.checkNonNull(n.Arg)
		}
	case *ast.UnaryExpression:
		switch n.Op {
		case "-", "+", "~":
			if _, lit := n.Arg.(*ast.NumberLiteral); !lit {
				c.checkNonNull(n.Arg)
			}
		}
	case *ast.IndexExpression:
		if !n.Optional {
			c.checkNonNull(n.Object)
		}
	case *ast.ReturnStatement:
		if ret != nil && n.Value != nil {
			c.checkAssign(n.Value, ret, diag.NotAssignable, n.GetPos())
		}
	case *ast.MemberExpression:
		c.checkProperty(n)
		c.checkAccessibility(n)
	case *ast.CallExpression:
		if _, super := n.Callee.(*ast.SuperExpression); super && len(n.TypeArgs) > 0 {
			c.report(diag.SuperTypeArgs, n.Callee.GetPos())
			return // tsc resolves nothing further of `super<T>(…)`
		}
		c.checkCall(n)
	case *ast.BinaryExpression:
		c.checkOperator(n)
	case *ast.Identifier:
		// Every reference resolves, whether or not anything asks its type.
		if sym, _ := c.b.Resolve(n); sym == nil {
			c.unresolvedValue(n.Name, n, n.GetPos(), c.b.LookupScope(n))
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
func (c *Checker) declaredReturn(fn ast.Node, ta *ast.TypeAnnotation, wrapped bool) *Type {
	if ta == nil || wrapped || ta.TypeNode() == nil {
		return nil
	}
	if _, pred := ta.TypeNode().(*ast.TypePredicate); pred {
		return nil
	}
	scope := c.b.ScopeOf(fn)
	if scope == nil {
		scope = c.b.Module
	}
	t := c.typeFromNode(ta.TypeNode(), scope)
	if c.Unanswered(t) {
		return nil
	}
	return t
}

// checkParams checks each annotated parameter's default value.
func (c *Checker) checkParams(params []ast.Param, fn ast.Node) {
	scope := c.b.ScopeOf(fn)
	if scope == nil {
		scope = c.b.Module
	}
	for _, p := range params {
		if p.Default == nil || p.Type == nil || p.Type.TypeNode() == nil {
			continue
		}
		c.checkAssign(p.Default, c.typeFromNode(p.Type.TypeNode(), scope), diag.NotAssignable, p.Default.GetPos())
	}
}

// checkClassFields checks each annotated field's initializer.
func (c *Checker) checkClassFields(cd *ast.ClassDeclaration) {
	if len(cd.TypeParams) > 0 {
		return // a field's type may name the class's parameters
	}
	sym := c.symbolOf(cd)
	if sym == nil {
		return
	}
	for _, f := range cd.Fields {
		if f.Initializer == nil || f.Type == nil || f.Type.TypeNode() == nil {
			continue
		}
		t := c.typeFromNode(f.Type.TypeNode(), sym.Scope)
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
	if c.elaborate(value, target, m, pos) {
		return
	}
	src := c.TypeOf(value)
	switch c.assignableTo(src, target) {
	case no:
		if c.isGlobalObject(src) {
			c.report(diag.ObjectToFewTypes, pos) // tsc's head message for `Object`
			return
		}
		if !someMember(target, Literal) {
			src = widen(c, src) // tsc names `"x"` as string against number
		}
		c.report(m, pos, src, target)
	case maybe:
		c.checkMissing(src, target, m, pos)
	}
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
	if src.Flags&Object == 0 || target.Flags&Object == 0 || src.Flags&Union != 0 || nominalOpen(src) || nominalOpen(target) ||
		(src.Kind != Anonymous && src.Kind != Interface && src.Kind != Instance) ||
		(target.Kind != Anonymous && target.Kind != Interface) {
		return
	}
	var missing []string
	for _, tp := range target.Props {
		sp := src.Prop(tp.Name)
		if sp == nil {
			sp = c.objectMember(tp.Name)
		}
		if sp == nil {
			if !tp.Optional {
				missing = append(missing, tp.Name)
			}
			continue
		}
		if c.assignableTo(sp.Type, tp.Type) != yes {
			return // another reason the checker cannot decide
		}
	}
	c.reportMissing(missing, src, target, m, pos)
}

func (c *Checker) reportMissing(missing []string, src, target *Type, m *diag.Message, pos ast.Pos) {
	switch {
	case len(missing) == 0:
	case m == diag.ArgNotAssignable, target.intersection:
		// An intersection's missing property is elaborated under TS2322.
		c.report(m, pos, src, target)
	case len(missing) == 1:
		c.report(diag.PropertyMissing, pos, missing[0], src, target)
	case len(missing) <= 5: // tsc lists up to five
		c.report(diag.PropertiesMissing, pos, src, target, strings.Join(missing, ", "))
	default:
		c.report(diag.PropertiesMissingMore, pos, src, target, strings.Join(missing[:4], ", "), len(missing)-4)
	}
}

// checkTarget reports a store to a name that is not a variable (TS2588 for
// a constant, TS2630 a function, TS2629 a class, TS2628 an enum); it
// reports whether the target is one a value may be stored to.
func (c *Checker) checkTarget(target ast.Expression) bool {
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
func nominalOpen(t *Type) bool {
	for seen := 0; t != nil && seen < 64; seen++ {
		if t.partial || len(t.Calls) > 0 || len(t.Constructs) > 0 {
			return true // a callable type also has Function's members
		}
		if t.Symbol != nil {
			name := sourceName(t.Symbol.Name)
			if libGlobals[name] || strings.HasPrefix(name, "HTML") || strings.HasPrefix(name, "SVG") || mergedWithInterface(t.Symbol) || classDecls(t.Symbol) > 1 {
				return true
			}
		}
		t = t.Base
	}
	return false
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
		c.checkNonNull(e.Object)
	}
	if objectProto[e.Property] || strings.HasPrefix(e.Property, "#") || strings.HasPrefix(e.Property, "__kml_") {
		return // Object.prototype's, a private name's, or this compiler's own
	}
	if c.namespaceMember(e) != nil {
		return // a namespace's member
	}
	if sym := c.calleeSymbol(e.Object); sym != nil && (len(sym.Declarations) > 1 || c.b.NamespaceScope(sym) != nil) {
		return // a merged declaration (an enum and a namespace, …)
	}
	obj := c.TypeOf(e.Object)
	if c.Unanswered(obj) || obj.Flags&Object == 0 || obj.Flags&Union != 0 || nominalOpen(obj) {
		return
	}
	switch obj.Kind {
	case Anonymous, Interface, Instance:
	default:
		return
	}
	if obj.Prop(e.Property) == nil && obj.StringIndex == nil {
		c.report(diag.PropertyNotExist, e.GetPos(), e.Property, obj)
	}
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
				c.checkAssign(p.Value, tt, diag.NotAssignable, p.Value.GetPos())
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
		case Tuple:
			if len(v.Elements) != len(target.Elems) {
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
// type parameters, no overloads, no spread argument.
func (c *Checker) checkCall(e *ast.CallExpression) {
	if e.Optional {
		return
	}
	c.checkCallee(e.Callee)
	for _, a := range e.Args {
		if _, spread := a.(*ast.SpreadElement); spread {
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
	fn := c.signaturesOf(c.TypeOf(e.Callee), false)
	if c.Unanswered(fn) || fn.Flags&Object == 0 || fn.Kind != Function {
		return
	}
	if c.typeArgArityError(e, fn) {
		return
	}
	if id, ok := e.Callee.(*ast.Identifier); ok && len(fn.Overloads) == 0 {
		if sym, _ := c.b.Resolve(id); sym != nil && len(sym.Declarations) > 1 {
			return // merged declarations
		}
	}
	if len(fn.Overloads) > 0 {
		c.checkOverloadCall(e, fn)
		return
	}
	if len(fn.TypeParams) > 0 {
		return
	}
	if fn.ThisType != nil {
		// The receiver must be the signature's `this` (TS2684); a call with
		// none passes void.
		if m, ok := skipOuter(e.Callee).(*ast.MemberExpression); ok {
			c.checkAssign(m.Object, fn.ThisType, diag.ThisContext, m.Object.GetPos())
		} else if !c.Unanswered(fn.ThisType) && fn.ThisType.Flags&(Any|Unknown) == 0 {
			c.report(diag.ThisContext, e.Callee.GetPos(), c.voidT, fn.ThisType)
		}
	}
	min, max := 0, len(fn.Params)
	for i := range fn.Params {
		switch {
		case fn.restParam && i == len(fn.Params)-1:
			max = -1
		case i < len(fn.optionals) && fn.optionals[i]:
		default:
			min = i + 1
		}
	}
	if len(e.Args) < min {
		if max < 0 {
			c.report(diag.ArgCountAtLeast, e.Callee.GetPos(), min, len(e.Args))
		} else {
			c.report(diag.ArgCount, e.Callee.GetPos(), arity(min, max), len(e.Args))
		}
		return
	}
	if max >= 0 && len(e.Args) > max {
		c.report(diag.ArgCount, e.Args[max].GetPos(), arity(min, max), len(e.Args))
		return
	}
	for i, a := range e.Args {
		if p := paramAt(fn, i); p != nil {
			c.checkAssign(a, p, diag.ArgNotAssignable, a.GetPos())
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
	c.report(diag.TypeArgCount, e.Callee.GetPos(), arity(lo, hi), len(e.TypeArgs))
	return true
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
	case "<", ">", "<=", ">=":
		c.checkNonNull(e.Left)
		c.checkNonNull(e.Right)
		return
	case "-", "*", "/", "%", "**", "&", "|", "^", "<<", ">>", ">>>":
		// Each operand must be a value first (checkNonNullType), then a
		// number.
		nl, nr := c.checkNonNull(e.Left), c.checkNonNull(e.Right)
		if nl == nil || nr == nil {
			return
		}
	case "+":
		if !c.Unanswered(l) && !c.Unanswered(r) && l.Flags&Any == 0 && r.Flags&Any == 0 && !isStringLike(l) && !isStringLike(r) {
			if nl, nr := c.checkNonNull(e.Left), c.checkNonNull(e.Right); nl == nil || nr == nil {
				return
			}
		}
	}
	if c.Unanswered(l) || c.Unanswered(r) || !plainOperand(l) || !plainOperand(r) {
		return
	}
	switch e.Op {
	case "-", "*", "/", "%", "**", "&", "|", "^", "<<", ">>", ">>>":
		if (e.Op == "&" || e.Op == "|" || e.Op == "^") && allMembers(l, BooleanLike) && allMembers(r, BooleanLike) {
			return // TypeScript's TS2447 ("use && instead"), not reported here
		}
		lok, rok := arithmetic(l), arithmetic(r)
		if !lok {
			c.report(diag.ArithmeticLeft, e.Left.GetPos())
		}
		if !rok {
			c.report(diag.ArithmeticRight, e.Right.GetPos())
		}
		if lok && rok && mixesBigInt(l, r) {
			c.report(diag.OperatorNotApplied, e.Left.GetPos(), e.Op, widen(c, l), widen(c, r))
		}
	case "+":
		if isStringLike(l) || isStringLike(r) {
			return
		}
		if !arithmetic(l) || !arithmetic(r) || mixesBigInt(l, r) {
			// tsc names the operands as typed when a bigint is involved,
			// widened otherwise (`5n + 1`, but `true + 1` is boolean and
			// number).
			if !isBigIntLike(l) && !isBigIntLike(r) {
				l, r = widen(c, l), widen(c, r)
			}
			c.report(diag.OperatorNotApplied, e.Left.GetPos(), e.Op, l, r)
		}
	}
}

// plainOperand reports whether t is an operand this check decides: not any
// or unknown, nothing nullish, no symbol (tsc's TS2469, not modelled), no
// type parameter, and a string-like member
// only where every member is one (`string | number` is TS2365 territory
// with its own rules for `+`).
func plainOperand(t *Type) bool {
	for _, m := range members(t) {
		if m.Flags&(Any|Unknown|Nullish|Never|ESSymbol) != 0 || m.Flags&TypeParam != 0 && m.Constraint != nil {
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
	switch op {
	case "-", "*", "/", "%", "**", "&", "|", "^", "<<", ">>", ">>>":
		c.checkNonNull(left)
		c.checkNonNull(right)
	case "+":
		if !c.Unanswered(l) && !c.Unanswered(r) && l.Flags&Any == 0 && r.Flags&Any == 0 && !isStringLike(l) && !isStringLike(r) {
			c.checkNonNull(left)
			c.checkNonNull(right)
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
	ast.Inspect(n, func(x ast.Node) bool {
		if call, ok := x.(*ast.CallExpression); ok {
			if _, super := call.Callee.(*ast.SuperExpression); super && len(call.TypeArgs) > 0 {
				return true // `super<T>(…)` is TS2754; its type arguments resolve nothing
			}
		}
		forEachAnnotation(reflect.ValueOf(x), func(ta *ast.TypeAnnotation) {
			if ta.Source == "jsdoc" || ta.TypeNode() == nil {
				return
			}
			ast.Inspect(ta.TypeNode(), func(tn ast.Node) bool {
				if r, ok := tn.(*ast.TypeReference); ok && len(r.Qualifier) == 0 {
					c.cannotFindType(r.Name, r.Range.Pos)
				}
				return true
			})
		})
		return true
	})
}

var typeAnnotationPtr = reflect.TypeOf((*ast.TypeAnnotation)(nil))

// forEachAnnotation calls fn with each type annotation a node's fields hold,
// directly or inside a non-node struct or slice (a parameter, a field).
func forEachAnnotation(v reflect.Value, fn func(*ast.TypeAnnotation)) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		if v.Type() == typeAnnotationPtr {
			fn(v.Interface().(*ast.TypeAnnotation))
			return
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if !v.Type().Field(i).IsExported() {
				continue
			}
			switch f.Kind() {
			case reflect.Pointer:
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
	if suggest && c.nearKnownName(src) {
		return // tsc's TS2552, not reported
	}
	c.cannotFind(name, pos)
}

// compilerTypeNames are the type names codegen resolves by name
// (ResolveTypeName) that neither TypeScript's library nor Node declares:
// this compiler's own surfaces', and `intrinsic` (tsc's `Uppercase` body).
var compilerTypeNames = map[string]bool{
	"ClusterAddress": true, "ClusterWorker": true, "HttpRequest": true, "WSConnection": true,
	"WSMessageEvent": true, "URLPattern": true, "intrinsic": true,
}

// nearKnownName reports whether name is within tsc's spelling-suggestion
// distance of a known name (getSpellingSuggestion): tsc then reports TS2552.
func (c *Checker) nearKnownName(name string) bool {
	var cands []string
	for n := range c.knownTypeNames {
		cands = append(cands, n)
	}
	if spellingSuggestion(name, cands) != "" {
		return true
	}
	names, _ := lib.GlobalNames()
	return spellingSuggestion(name, names) != ""
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
