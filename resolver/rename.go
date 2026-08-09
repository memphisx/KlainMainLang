package resolver

// This file implements TDD-00041's scope-aware rename pass: every top-level
// declaration in a file gets a file-private mangled name (mangleFileDecls,
// in resolver.go), and renameFile below rewrites every reference within that
// same file that resolves — through real lexical scoping, not a blind
// find-and-replace — to one of that file's own top-level declarations or to
// an imported binding. See docs/tdd/TDD-00041.md for the design.
//
// TDD-00042 extended this pass to also resolve namespace-import member
// access (`ns.foo` for `import * as ns from '...'`) entirely at this
// compile-time stage — `ns` itself is never a runtime value (see the TDD's
// Design section for why), so `ns.foo` is rewritten directly into a
// reference to foo's mangled name, indistinguishable after this pass from a
// normal named import of just that one member. This is why rewriteExpr
// below returns the (possibly replaced) expression rather than mutating
// only in place: replacing a *ast.MemberExpression node with a plain
// *ast.Identifier requires the caller to reassign the field/slice element
// that held it, which a bare in-place mutation can't do.

import (
	"strings"

	"KlainMainLang/ast"
)

// scope is a stack of locally-bound names (function/arrow parameters,
// block-scoped let/const, loop variables, catch parameters, generic type
// parameters) that shadow top-level lookups during the rename walk. It is
// deliberately separate from the file-level lookup table: a name found here
// means "do not rewrite," full stop, regardless of what the lookup table
// says.
type scope struct{ frames []map[string]bool }

func newScope() *scope { return &scope{} }

func (s *scope) push() { s.frames = append(s.frames, map[string]bool{}) }
func (s *scope) pop()  { s.frames = s.frames[:len(s.frames)-1] }

func (s *scope) bind(name string) {
	if name == "" || len(s.frames) == 0 {
		return // top-level bindings are never bound into a scope frame — see renameFile's doc
	}
	s.frames[len(s.frames)-1][name] = true
}

func (s *scope) bound(name string) bool {
	for i := len(s.frames) - 1; i >= 0; i-- {
		if s.frames[i][name] {
			return true
		}
	}
	return false
}

// lookupTable bundles the two tables a file's rename pass needs: names
// (this file's own mangled declarations plus its ordinary/default import
// bindings — local name -> the mangled name it refers to, exactly what
// TDD-00041 already built) and ns (TDD-00042: namespace-import bindings —
// local alias -> {exported member's original name -> its mangled name},
// one entry per `import * as ns from '...'`). Both are built once by the
// caller (resolver.go) and passed down unchanged through the whole walk.
type lookupTable struct {
	names map[string]string
	ns    map[string]map[string]string
}

// renameFile rewrites every top-level statement in prog using lu — see
// lookupTable's own doc comment. Local (non-top-level) bindings are
// deliberately never added to lu.names and never consulted through it —
// they're tracked purely via the scope stack, which always wins when a name
// is shadowed.
func renameFile(prog *ast.Program, lu lookupTable) {
	for _, stmt := range prog.Body {
		rewriteTopLevelStmt(stmt, lu)
	}
}

func rewriteTopLevelStmt(stmt ast.Statement, lu lookupTable) {
	switch s := stmt.(type) {
	case *ast.ExportDeclaration:
		rewriteTopLevelStmt(s.Decl, lu)
	case *ast.FunctionDeclaration:
		sc := newScope()
		rewriteFunctionLike(s, sc, lu)
	case *ast.VarDeclaration:
		sc := newScope()
		if s.TypeAnnot != nil {
			rewriteType(s.TypeAnnot, sc, lu)
		}
		if s.Init != nil {
			s.Init = rewriteExpr(s.Init, sc, lu)
		}
	case *ast.InterfaceDeclaration:
		rewriteInterfaceDecl(s, lu)
	case *ast.TypeAliasDeclaration:
		rewriteType(s.Type, newScope(), lu)
	case *ast.EnumDeclaration:
		sc := newScope()
		for i := range s.Members {
			if s.Members[i].Value != nil {
				s.Members[i].Value = rewriteExpr(s.Members[i].Value, sc, lu)
			}
		}
	case *ast.ClassDeclaration:
		rewriteClassDecl(s, lu)
	case *ast.ImportDeclaration:
		// Dropped at merge time — nothing to rewrite.
	default:
		// Any other top-level statement is executable code, only ever legal
		// in the entry file (see validateDeclarationsOnly) — rewrite it like
		// any nested statement, starting from a fresh (empty) local scope.
		rewriteStmt(stmt, newScope(), lu)
	}
}

// rewriteFunctionLike handles a FunctionDeclaration's own parameters/return
// type/body. Used both for a top-level function (called with a brand new
// scope) and a class method/constructor (called with the class's own scope,
// already carrying its TypeParams — see rewriteClassDecl).
func rewriteFunctionLike(f *ast.FunctionDeclaration, sc *scope, lu lookupTable) {
	sc.push()
	for _, tp := range f.TypeParams {
		sc.bind(tp)
	}
	bindParams(f.Params, sc)
	for i := range f.Params {
		if f.Params[i].Type != nil {
			rewriteType(f.Params[i].Type, sc, lu)
		}
		if f.Params[i].Default != nil {
			f.Params[i].Default = rewriteExpr(f.Params[i].Default, sc, lu)
		}
	}
	if f.ReturnType != nil {
		rewriteType(f.ReturnType, sc, lu)
	}
	if f.Body != nil {
		rewriteBlock(f.Body, sc, lu)
	}
	sc.pop()
}

// bindParams adds every name a parameter list binds — a plain name, or the
// individual names of a destructured array/object parameter pattern — into
// the current (already-pushed) scope frame.
func bindParams(params []ast.Param, sc *scope) {
	for _, p := range params {
		switch {
		case p.ArrayPattern != nil:
			for _, n := range p.ArrayPattern {
				sc.bind(n)
			}
		case p.ObjectPattern != nil:
			for _, dp := range p.ObjectPattern {
				sc.bind(dp.Local)
			}
		default:
			sc.bind(p.Name)
		}
	}
}

func rewriteInterfaceDecl(i *ast.InterfaceDeclaration, lu lookupTable) {
	sc := newScope()
	sc.push()
	for _, tp := range i.TypeParams {
		sc.bind(tp)
	}
	for fi := range i.Fields {
		rewriteType(i.Fields[fi].Type, sc, lu)
	}
	for mi := range i.Methods {
		sc.push()
		bindParams(i.Methods[mi].Params, sc)
		for pi := range i.Methods[mi].Params {
			if i.Methods[mi].Params[pi].Type != nil {
				rewriteType(i.Methods[mi].Params[pi].Type, sc, lu)
			}
		}
		if i.Methods[mi].ReturnType != nil {
			rewriteType(i.Methods[mi].ReturnType, sc, lu)
		}
		sc.pop()
	}
	sc.pop()
}

func rewriteClassDecl(c *ast.ClassDeclaration, lu lookupTable) {
	sc := newScope()
	sc.push()
	for _, tp := range c.TypeParams {
		sc.bind(tp)
	}

	if c.BaseClass != "" && !sc.bound(c.BaseClass) {
		if m, ok := lu.names[c.BaseClass]; ok {
			c.BaseClass = m
		}
	}
	for _, bta := range c.BaseTypeArgs {
		rewriteType(bta, sc, lu)
	}
	for i := range c.Implements {
		if !sc.bound(c.Implements[i]) {
			if m, ok := lu.names[c.Implements[i]]; ok {
				c.Implements[i] = m
			}
		}
	}
	for i := range c.Fields {
		rewriteType(c.Fields[i].Type, sc, lu)
	}
	if c.Constructor != nil {
		rewriteFunctionLike(c.Constructor, sc, lu)
	}
	for _, m := range c.Methods {
		rewriteFunctionLike(m, sc, lu)
	}
	for _, blk := range c.StaticBlocks {
		rewriteBlock(blk, sc, lu)
	}
	sc.pop()
}

func rewriteBlock(b *ast.BlockStatement, sc *scope, lu lookupTable) {
	if b == nil {
		return
	}
	sc.push()
	for _, st := range b.Body {
		rewriteStmt(st, sc, lu)
	}
	sc.pop()
}

// rewriteStmt handles every statement kind that can appear nested inside a
// block. FunctionDeclaration/ClassDeclaration/InterfaceDeclaration/
// EnumDeclaration/TypeAliasDeclaration/ImportDeclaration/ExportDeclaration
// never appear here — this language only allows those at Program top level
// (nested function declarations, and the same restriction for the other
// declaration kinds, aren't supported) — so there is no case for them
// below; they're handled solely by rewriteTopLevelStmt.
func rewriteStmt(stmt ast.Statement, sc *scope, lu lookupTable) {
	switch s := stmt.(type) {
	case *ast.BlockStatement:
		rewriteBlock(s, sc, lu)
	case *ast.VarDeclaration:
		if s.TypeAnnot != nil {
			rewriteType(s.TypeAnnot, sc, lu)
		}
		if s.Init != nil {
			s.Init = rewriteExpr(s.Init, sc, lu)
		}
		sc.bind(s.Name)
	case *ast.ArrayDestructuring:
		if s.Init != nil {
			s.Init = rewriteExpr(s.Init, sc, lu)
		}
		for _, n := range s.Names {
			sc.bind(n)
		}
	case *ast.ObjectDestructuring:
		if s.Init != nil {
			s.Init = rewriteExpr(s.Init, sc, lu)
		}
		for _, p := range s.Props {
			sc.bind(p.Local)
		}
	case *ast.ExpressionStatement:
		s.Expr = rewriteExpr(s.Expr, sc, lu)
	case *ast.ReturnStatement:
		if s.Value != nil {
			s.Value = rewriteExpr(s.Value, sc, lu)
		}
	case *ast.ThrowStatement:
		s.Argument = rewriteExpr(s.Argument, sc, lu)
	case *ast.IfStatement:
		s.Test = rewriteExpr(s.Test, sc, lu)
		rewriteBlock(s.Consequent, sc, lu)
		if s.Alternate != nil {
			rewriteStmt(s.Alternate, sc, lu)
		}
	case *ast.ForStatement:
		sc.push()
		if s.Init != nil {
			rewriteStmt(s.Init, sc, lu)
		}
		if s.Test != nil {
			s.Test = rewriteExpr(s.Test, sc, lu)
		}
		if s.Update != nil {
			s.Update = rewriteExpr(s.Update, sc, lu)
		}
		rewriteBlock(s.Body, sc, lu)
		sc.pop()
	case *ast.ForOfStatement:
		s.Iterable = rewriteExpr(s.Iterable, sc, lu)
		sc.push()
		sc.bind(s.VarName)
		rewriteBlock(s.Body, sc, lu)
		sc.pop()
	case *ast.ForInStatement:
		s.Object = rewriteExpr(s.Object, sc, lu)
		sc.push()
		sc.bind(s.VarName)
		rewriteBlock(s.Body, sc, lu)
		sc.pop()
	case *ast.WhileStatement:
		s.Test = rewriteExpr(s.Test, sc, lu)
		rewriteBlock(s.Body, sc, lu)
	case *ast.DoWhileStatement:
		rewriteBlock(s.Body, sc, lu)
		s.Test = rewriteExpr(s.Test, sc, lu)
	case *ast.SwitchStatement:
		s.Discriminant = rewriteExpr(s.Discriminant, sc, lu)
		for i := range s.Cases {
			if s.Cases[i].Test != nil {
				s.Cases[i].Test = rewriteExpr(s.Cases[i].Test, sc, lu)
			}
			sc.push()
			for _, cs := range s.Cases[i].Body {
				rewriteStmt(cs, sc, lu)
			}
			sc.pop()
		}
	case *ast.BreakStatement, *ast.ContinueStatement:
		// labels aren't identifier references
	case *ast.LabeledStatement:
		rewriteStmt(s.Body, sc, lu)
	case *ast.TryStatement:
		rewriteBlock(s.Body, sc, lu)
		if s.Catch != nil {
			sc.push()
			sc.bind(s.Catch.Param)
			rewriteBlock(s.Catch.Body, sc, lu)
			sc.pop()
		}
		if s.Finally != nil {
			rewriteBlock(s.Finally, sc, lu)
		}
	}
}

// rewriteExpr rewrites expr and returns the expression the caller should
// keep in its place — almost always expr itself (mutated in place, same as
// before TDD-00042), except for a namespace-import member access
// (`ns.foo`), which is replaced wholesale by a fresh *ast.Identifier
// referencing foo's mangled name. Every call site must assign the return
// value back into whatever field/slice element held the original
// expression — a bare `rewriteExpr(x, sc, lu)` with the result discarded
// silently drops any such replacement.
func rewriteExpr(expr ast.Expression, sc *scope, lu lookupTable) ast.Expression {
	switch e := expr.(type) {
	case *ast.Identifier:
		if !sc.bound(e.Name) {
			if m, ok := lu.names[e.Name]; ok {
				e.Name = m
			}
		}
	case *ast.AwaitExpression:
		e.Argument = rewriteExpr(e.Argument, sc, lu)
	case *ast.BinaryExpression:
		e.Left = rewriteExpr(e.Left, sc, lu)
		e.Right = rewriteExpr(e.Right, sc, lu)
	case *ast.ConditionalExpression:
		e.Test = rewriteExpr(e.Test, sc, lu)
		e.Consequent = rewriteExpr(e.Consequent, sc, lu)
		e.Alternate = rewriteExpr(e.Alternate, sc, lu)
	case *ast.SpreadElement:
		e.Arg = rewriteExpr(e.Arg, sc, lu)
	case *ast.UnaryExpression:
		e.Arg = rewriteExpr(e.Arg, sc, lu)
	case *ast.UpdateExpression:
		e.Arg = rewriteExpr(e.Arg, sc, lu)
	case *ast.AssignmentExpression:
		e.Left = rewriteExpr(e.Left, sc, lu)
		e.Right = rewriteExpr(e.Right, sc, lu)
	case *ast.CallExpression:
		e.Callee = rewriteExpr(e.Callee, sc, lu)
		for i := range e.Args {
			e.Args[i] = rewriteExpr(e.Args[i], sc, lu)
		}
	case *ast.MemberExpression:
		e.Object = rewriteExpr(e.Object, sc, lu)
		// TDD-00042: `ns.foo` for a namespace-import alias `ns` resolves
		// entirely at compile time into a direct reference to foo's
		// mangled name — ns is never a runtime value (see the TDD's
		// Design section), so this whole node is replaced rather than
		// merely mutated. Guarded by !sc.bound so a local variable that
		// happens to share a namespace alias's name (shadowing it) is left
		// as an ordinary, unresolved member access instead.
		if id, ok := e.Object.(*ast.Identifier); ok && !sc.bound(id.Name) {
			if members, ok := lu.ns[id.Name]; ok {
				if mangled, ok := members[e.Property]; ok {
					return ast.NewIdentifier(mangled, e.GetPos())
				}
			}
		}
		return e
	case *ast.ArrayLiteral:
		for i := range e.Elements {
			e.Elements[i] = rewriteExpr(e.Elements[i], sc, lu)
		}
	case *ast.IndexExpression:
		e.Object = rewriteExpr(e.Object, sc, lu)
		e.Index = rewriteExpr(e.Index, sc, lu)
	case *ast.NewArrayExpression:
		if e.ElemType != nil {
			rewriteType(e.ElemType, sc, lu)
		}
		if e.Size != nil {
			e.Size = rewriteExpr(e.Size, sc, lu)
		}
	case *ast.ObjectLiteral:
		for i := range e.Properties {
			if e.Properties[i].KeyExpr != nil {
				e.Properties[i].KeyExpr = rewriteExpr(e.Properties[i].KeyExpr, sc, lu)
			}
			if e.Properties[i].Value != nil {
				e.Properties[i].Value = rewriteExpr(e.Properties[i].Value, sc, lu)
			}
		}
	case *ast.ArrowFunction:
		sc.push()
		bindParams(e.Params, sc)
		for i := range e.Params {
			if e.Params[i].Type != nil {
				rewriteType(e.Params[i].Type, sc, lu)
			}
			if e.Params[i].Default != nil {
				e.Params[i].Default = rewriteExpr(e.Params[i].Default, sc, lu)
			}
		}
		if e.RetType != nil {
			rewriteType(e.RetType, sc, lu)
		}
		if e.Body != nil {
			e.Body = rewriteExpr(e.Body, sc, lu)
		}
		if e.Block != nil {
			rewriteBlock(e.Block, sc, lu)
		}
		sc.pop()
	case *ast.TemplateLiteral:
		for i := range e.Exprs {
			e.Exprs[i] = rewriteExpr(e.Exprs[i], sc, lu)
		}
	case *ast.NewMapExpression:
		if e.KeyType != nil {
			rewriteType(e.KeyType, sc, lu)
		}
		if e.ValType != nil {
			rewriteType(e.ValType, sc, lu)
		}
	case *ast.NewSetExpression:
		if e.ElemType != nil {
			rewriteType(e.ElemType, sc, lu)
		}
	case *ast.NewEventEmitterExpression:
		if e.PayloadType != nil {
			rewriteType(e.PayloadType, sc, lu)
		}
	case *ast.NewErrorExpression:
		if e.Message != nil {
			e.Message = rewriteExpr(e.Message, sc, lu)
		}
	case *ast.NewDateExpression:
		if e.Millis != nil {
			e.Millis = rewriteExpr(e.Millis, sc, lu)
		}
		for i := range e.Args {
			e.Args[i] = rewriteExpr(e.Args[i], sc, lu)
		}
	case *ast.NewURLExpression:
		e.URL = rewriteExpr(e.URL, sc, lu)
	case *ast.NewEventSourceExpression:
		e.URL = rewriteExpr(e.URL, sc, lu)
	case *ast.NewWebSocketExpression:
		e.URL = rewriteExpr(e.URL, sc, lu)
	case *ast.NewURLSearchParamsExpression:
		if e.Init != nil {
			e.Init = rewriteExpr(e.Init, sc, lu)
		}
	case *ast.NewHeadersExpression:
		if e.Init != nil {
			e.Init = rewriteExpr(e.Init, sc, lu)
		}
	case *ast.NewRequestExpression:
		e.URL = rewriteExpr(e.URL, sc, lu)
		if e.Init != nil {
			e.Init = rewriteExpr(e.Init, sc, lu)
		}
	case *ast.NewArrayBufferExpression:
		e.ByteLength = rewriteExpr(e.ByteLength, sc, lu)
	case *ast.NewTypedArrayExpression:
		if e.Arg != nil {
			e.Arg = rewriteExpr(e.Arg, sc, lu)
		}
	case *ast.NewTextDecoderExpression:
		if e.Label != nil {
			e.Label = rewriteExpr(e.Label, sc, lu)
		}
	case *ast.NewRegExpExpression:
		e.Pattern = rewriteExpr(e.Pattern, sc, lu)
		if e.Flags != nil {
			e.Flags = rewriteExpr(e.Flags, sc, lu)
		}
	case *ast.NewExpression:
		if !sc.bound(e.ClassName) {
			if m, ok := lu.names[e.ClassName]; ok {
				e.ClassName = m
			}
		}
		for _, ta := range e.TypeArgs {
			rewriteType(ta, sc, lu)
		}
		for i := range e.Args {
			e.Args[i] = rewriteExpr(e.Args[i], sc, lu)
		}
	}
	// Every other expression kind (literals, ThisExpression, SuperExpression,
	// NewXMLHttpRequestExpression/NewTextEncoderExpression — both zero-arg)
	// carries no identifier or type reference to rewrite.
	return expr
}

func rewriteType(ta *ast.TypeAnnotation, sc *scope, lu lookupTable) {
	if ta == nil {
		return
	}
	rewriteTypeName(ta, sc, lu)
	for i := range ta.Fields {
		rewriteType(ta.Fields[i].Type, sc, lu)
	}
	if ta.ElemType != nil {
		rewriteType(ta.ElemType, sc, lu)
	}
	if ta.KeyType != nil {
		rewriteType(ta.KeyType, sc, lu)
	}
	for _, t := range ta.TypeArgs {
		rewriteType(t, sc, lu)
	}
	for i := range ta.FuncParams {
		rewriteType(&ta.FuncParams[i], sc, lu)
	}
	if ta.FuncRetType != nil {
		rewriteType(ta.FuncRetType, sc, lu)
	}
}

// rewriteTypeName rewrites ta.Name, accounting for parser_types.go's flat
// "Foo[]" (or multi-dimensional "Foo[][]") encoding of a named type followed
// by an array suffix — the suffix is baked directly into the Name string
// rather than represented via ElemType, so a plain lu.names[ta.Name] lookup
// would silently miss "Foo[]" even though "Foo" itself has a mangled name.
func rewriteTypeName(ta *ast.TypeAnnotation, sc *scope, lu lookupTable) {
	name := ta.Name
	suffix := ""
	for strings.HasSuffix(name, "[]") {
		name = name[:len(name)-2]
		suffix += "[]"
	}
	if sc.bound(name) {
		return
	}
	if m, ok := lu.names[name]; ok {
		ta.Name = m + suffix
	}
}
