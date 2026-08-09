package resolver

// This file implements TDD-00041's scope-aware rename pass: every top-level
// declaration in a file gets a file-private mangled name (mangleFileDecls,
// in resolver.go), and renameFile below rewrites every reference within that
// same file that resolves — through real lexical scoping, not a blind
// find-and-replace — to one of that file's own top-level declarations or to
// an imported binding. See docs/tdd/TDD-00041.md for the design.

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

// renameFile rewrites every top-level statement in prog using lookup — a
// combined table of this file's own mangled declaration names plus its
// import bindings (local name -> the imported declaration's mangled name in
// its own file, already resolved by the caller). Local (non-top-level)
// bindings are deliberately never added to lookup and never consulted
// through it — they're tracked purely via the scope stack, which always
// wins when a name is shadowed.
func renameFile(prog *ast.Program, lookup map[string]string) {
	for _, stmt := range prog.Body {
		rewriteTopLevelStmt(stmt, lookup)
	}
}

func rewriteTopLevelStmt(stmt ast.Statement, lu map[string]string) {
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
			rewriteExpr(s.Init, sc, lu)
		}
	case *ast.InterfaceDeclaration:
		rewriteInterfaceDecl(s, lu)
	case *ast.TypeAliasDeclaration:
		rewriteType(s.Type, newScope(), lu)
	case *ast.EnumDeclaration:
		sc := newScope()
		for i := range s.Members {
			if s.Members[i].Value != nil {
				rewriteExpr(s.Members[i].Value, sc, lu)
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
func rewriteFunctionLike(f *ast.FunctionDeclaration, sc *scope, lu map[string]string) {
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
			rewriteExpr(f.Params[i].Default, sc, lu)
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

func rewriteInterfaceDecl(i *ast.InterfaceDeclaration, lu map[string]string) {
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

func rewriteClassDecl(c *ast.ClassDeclaration, lu map[string]string) {
	sc := newScope()
	sc.push()
	for _, tp := range c.TypeParams {
		sc.bind(tp)
	}

	if c.BaseClass != "" && !sc.bound(c.BaseClass) {
		if m, ok := lu[c.BaseClass]; ok {
			c.BaseClass = m
		}
	}
	for _, bta := range c.BaseTypeArgs {
		rewriteType(bta, sc, lu)
	}
	for i := range c.Implements {
		if !sc.bound(c.Implements[i]) {
			if m, ok := lu[c.Implements[i]]; ok {
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

func rewriteBlock(b *ast.BlockStatement, sc *scope, lu map[string]string) {
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
func rewriteStmt(stmt ast.Statement, sc *scope, lu map[string]string) {
	switch s := stmt.(type) {
	case *ast.BlockStatement:
		rewriteBlock(s, sc, lu)
	case *ast.VarDeclaration:
		if s.TypeAnnot != nil {
			rewriteType(s.TypeAnnot, sc, lu)
		}
		if s.Init != nil {
			rewriteExpr(s.Init, sc, lu)
		}
		sc.bind(s.Name)
	case *ast.ArrayDestructuring:
		if s.Init != nil {
			rewriteExpr(s.Init, sc, lu)
		}
		for _, n := range s.Names {
			sc.bind(n)
		}
	case *ast.ObjectDestructuring:
		if s.Init != nil {
			rewriteExpr(s.Init, sc, lu)
		}
		for _, p := range s.Props {
			sc.bind(p.Local)
		}
	case *ast.ExpressionStatement:
		rewriteExpr(s.Expr, sc, lu)
	case *ast.ReturnStatement:
		if s.Value != nil {
			rewriteExpr(s.Value, sc, lu)
		}
	case *ast.ThrowStatement:
		rewriteExpr(s.Argument, sc, lu)
	case *ast.IfStatement:
		rewriteExpr(s.Test, sc, lu)
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
			rewriteExpr(s.Test, sc, lu)
		}
		if s.Update != nil {
			rewriteExpr(s.Update, sc, lu)
		}
		rewriteBlock(s.Body, sc, lu)
		sc.pop()
	case *ast.ForOfStatement:
		rewriteExpr(s.Iterable, sc, lu)
		sc.push()
		sc.bind(s.VarName)
		rewriteBlock(s.Body, sc, lu)
		sc.pop()
	case *ast.ForInStatement:
		rewriteExpr(s.Object, sc, lu)
		sc.push()
		sc.bind(s.VarName)
		rewriteBlock(s.Body, sc, lu)
		sc.pop()
	case *ast.WhileStatement:
		rewriteExpr(s.Test, sc, lu)
		rewriteBlock(s.Body, sc, lu)
	case *ast.DoWhileStatement:
		rewriteBlock(s.Body, sc, lu)
		rewriteExpr(s.Test, sc, lu)
	case *ast.SwitchStatement:
		rewriteExpr(s.Discriminant, sc, lu)
		for i := range s.Cases {
			if s.Cases[i].Test != nil {
				rewriteExpr(s.Cases[i].Test, sc, lu)
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

func rewriteExpr(expr ast.Expression, sc *scope, lu map[string]string) {
	switch e := expr.(type) {
	case *ast.Identifier:
		if !sc.bound(e.Name) {
			if m, ok := lu[e.Name]; ok {
				e.Name = m
			}
		}
	case *ast.AwaitExpression:
		rewriteExpr(e.Argument, sc, lu)
	case *ast.BinaryExpression:
		rewriteExpr(e.Left, sc, lu)
		rewriteExpr(e.Right, sc, lu)
	case *ast.ConditionalExpression:
		rewriteExpr(e.Test, sc, lu)
		rewriteExpr(e.Consequent, sc, lu)
		rewriteExpr(e.Alternate, sc, lu)
	case *ast.SpreadElement:
		rewriteExpr(e.Arg, sc, lu)
	case *ast.UnaryExpression:
		rewriteExpr(e.Arg, sc, lu)
	case *ast.UpdateExpression:
		rewriteExpr(e.Arg, sc, lu)
	case *ast.AssignmentExpression:
		rewriteExpr(e.Left, sc, lu)
		rewriteExpr(e.Right, sc, lu)
	case *ast.CallExpression:
		rewriteExpr(e.Callee, sc, lu)
		for _, a := range e.Args {
			rewriteExpr(a, sc, lu)
		}
	case *ast.MemberExpression:
		rewriteExpr(e.Object, sc, lu)
	case *ast.ArrayLiteral:
		for _, el := range e.Elements {
			rewriteExpr(el, sc, lu)
		}
	case *ast.IndexExpression:
		rewriteExpr(e.Object, sc, lu)
		rewriteExpr(e.Index, sc, lu)
	case *ast.NewArrayExpression:
		if e.ElemType != nil {
			rewriteType(e.ElemType, sc, lu)
		}
		if e.Size != nil {
			rewriteExpr(e.Size, sc, lu)
		}
	case *ast.ObjectLiteral:
		for i := range e.Properties {
			if e.Properties[i].KeyExpr != nil {
				rewriteExpr(e.Properties[i].KeyExpr, sc, lu)
			}
			if e.Properties[i].Value != nil {
				rewriteExpr(e.Properties[i].Value, sc, lu)
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
				rewriteExpr(e.Params[i].Default, sc, lu)
			}
		}
		if e.RetType != nil {
			rewriteType(e.RetType, sc, lu)
		}
		if e.Body != nil {
			rewriteExpr(e.Body, sc, lu)
		}
		if e.Block != nil {
			rewriteBlock(e.Block, sc, lu)
		}
		sc.pop()
	case *ast.TemplateLiteral:
		for _, ex := range e.Exprs {
			rewriteExpr(ex, sc, lu)
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
			rewriteExpr(e.Message, sc, lu)
		}
	case *ast.NewDateExpression:
		if e.Millis != nil {
			rewriteExpr(e.Millis, sc, lu)
		}
		for _, a := range e.Args {
			rewriteExpr(a, sc, lu)
		}
	case *ast.NewURLExpression:
		rewriteExpr(e.URL, sc, lu)
	case *ast.NewEventSourceExpression:
		rewriteExpr(e.URL, sc, lu)
	case *ast.NewWebSocketExpression:
		rewriteExpr(e.URL, sc, lu)
	case *ast.NewURLSearchParamsExpression:
		if e.Init != nil {
			rewriteExpr(e.Init, sc, lu)
		}
	case *ast.NewHeadersExpression:
		if e.Init != nil {
			rewriteExpr(e.Init, sc, lu)
		}
	case *ast.NewRequestExpression:
		rewriteExpr(e.URL, sc, lu)
		if e.Init != nil {
			rewriteExpr(e.Init, sc, lu)
		}
	case *ast.NewArrayBufferExpression:
		rewriteExpr(e.ByteLength, sc, lu)
	case *ast.NewTypedArrayExpression:
		if e.Arg != nil {
			rewriteExpr(e.Arg, sc, lu)
		}
	case *ast.NewTextDecoderExpression:
		if e.Label != nil {
			rewriteExpr(e.Label, sc, lu)
		}
	case *ast.NewRegExpExpression:
		rewriteExpr(e.Pattern, sc, lu)
		if e.Flags != nil {
			rewriteExpr(e.Flags, sc, lu)
		}
	case *ast.NewExpression:
		if !sc.bound(e.ClassName) {
			if m, ok := lu[e.ClassName]; ok {
				e.ClassName = m
			}
		}
		for _, ta := range e.TypeArgs {
			rewriteType(ta, sc, lu)
		}
		for _, a := range e.Args {
			rewriteExpr(a, sc, lu)
		}
	}
	// Every other expression kind (literals, ThisExpression, SuperExpression,
	// NewXMLHttpRequestExpression/NewTextEncoderExpression — both zero-arg)
	// carries no identifier or type reference to rewrite.
}

func rewriteType(ta *ast.TypeAnnotation, sc *scope, lu map[string]string) {
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
// rather than represented via ElemType, so a plain lu[ta.Name] lookup would
// silently miss "Foo[]" even though "Foo" itself has a mangled name.
func rewriteTypeName(ta *ast.TypeAnnotation, sc *scope, lu map[string]string) {
	name := ta.Name
	suffix := ""
	for strings.HasSuffix(name, "[]") {
		name = name[:len(name)-2]
		suffix += "[]"
	}
	if sc.bound(name) {
		return
	}
	if m, ok := lu[name]; ok {
		ta.Name = m + suffix
	}
}
