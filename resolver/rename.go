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

// builtinMemberRef is one named import of a single member from a virtual
// built-in module (TDD-00049 Stage 2, `import { readFileSync } from 'fs'`)
// — Marker is the module's own reserved identifier (`fs__kml_builtin`, see
// virtual_modules.go), Member is the real property name to synthesize a
// member access for (`readFileSync`, `spec.Imported` — independent of
// whichever local alias the import bound it to).
type builtinMemberRef struct {
	Marker, Member string
}

// lookupTable bundles the tables a file's rename pass needs: names (this
// file's own mangled declarations plus its ordinary/default import
// bindings — local name -> the mangled name it refers to, exactly what
// TDD-00041 already built), ns (TDD-00042: namespace-import bindings —
// local alias -> {exported member's original name -> its mangled name},
// one entry per `import * as ns from '...'`), builtinMembers (TDD-00049
// Stage 2: local alias -> which virtual module member it names), and
// allowGlobalShadowing/reservedErr (TDD-00050: `-compat=strict|permissive`
// — see checkBinding's own doc comment). All are built once by the caller
// (resolver.go) and passed down unchanged through the whole walk.
type lookupTable struct {
	names                map[string]string
	ns                   map[string]map[string]string
	builtinMembers       map[string]builtinMemberRef
	allowGlobalShadowing bool
	reservedErr          *error // first-write-wins: set by the first reserved-name violation found anywhere in the walk, checked by the caller once renameFile returns
	filePath             string // this file's own absolute path (TDD-00055 Stage 1) — backs import.meta.url's rewrite, see rewriteExpr's *ast.ImportMetaUrl case
}

// checkBinding is TDD-00050's hook, called at every point a local binding
// is introduced (every scope.bind(...) call site below). It never halts
// the walk — none of rename.go's functions return an error today, and this
// intentionally doesn't change that (see the TDD's Design section for why
// a first-write-wins pointer was chosen over restructuring every function
// here to propagate one) — it only ever records the *first* violation
// found, which resolver.go checks once the whole walk completes.
func (lu lookupTable) checkBinding(name string, pos ast.Pos) {
	if lu.reservedErr == nil || *lu.reservedErr != nil {
		return // no error slot given (shouldn't happen from resolver.go), or already recorded one
	}
	if err := checkReservedBinding(name, pos.Line, pos.Col, lu.allowGlobalShadowing); err != nil {
		*lu.reservedErr = err
	}
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
	case *ast.VarDeclarationList:
		for _, d := range s.Decls {
			sc := newScope()
			if d.TypeAnnot != nil {
				rewriteType(d.TypeAnnot, sc, lu)
			}
			if d.Init != nil {
				d.Init = rewriteExpr(d.Init, sc, lu)
			}
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
	bindParams(f.Params, sc, lu, f.GetPos())
	rewritePatternDefaults(f.Params, sc, lu)
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
// the current (already-pushed) scope frame. pos is the enclosing
// function/arrow's own position — ast.Param carries no position of its
// own, so a reserved-name violation on a parameter is reported at the
// function's position rather than the individual parameter's (TDD-00050).
func bindParams(params []ast.Param, sc *scope, lu lookupTable, pos ast.Pos) {
	for _, p := range params {
		switch {
		case p.ArrayPattern != nil:
			for _, elem := range p.ArrayPattern {
				sc.bind(elem.Name)
				lu.checkBinding(elem.Name, pos)
			}
		case p.ObjectPattern != nil:
			for _, dp := range p.ObjectPattern {
				sc.bind(dp.Local)
				lu.checkBinding(dp.Local, pos)
			}
		default:
			sc.bind(p.Name)
			lu.checkBinding(p.Name, pos)
		}
	}
}

// rewritePatternDefaults rewrites every `= expr` default inside params'
// own destructuring patterns (ADR-00158) — sc must already have every
// pattern name bound (call after bindParams), so a default referencing an
// earlier-bound sibling in the same pattern (`[a, b = a]`) resolves
// correctly rather than as an outer/imported name of the same spelling.
func rewritePatternDefaults(params []ast.Param, sc *scope, lu lookupTable) {
	for i := range params {
		for j := range params[i].ArrayPattern {
			if d := params[i].ArrayPattern[j].Default; d != nil {
				params[i].ArrayPattern[j].Default = rewriteExpr(d, sc, lu)
			}
		}
		for j := range params[i].ObjectPattern {
			if d := params[i].ObjectPattern[j].Default; d != nil {
				params[i].ObjectPattern[j].Default = rewriteExpr(d, sc, lu)
			}
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
		bindParams(i.Methods[mi].Params, sc, lu, i.GetPos())
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
	// Hoist: bind every directly-nested function declaration's name into this
	// block's frame before walking any statement, so a reference to a nested
	// function (a forward call, or a mutual sibling call) resolves as a local
	// binding rather than being mis-renamed to a top-level mangled name — matching
	// the emitter's own pre-scan of nested functions (TDD-00094).
	for _, st := range b.Body {
		if fd, ok := st.(*ast.FunctionDeclaration); ok {
			sc.bind(fd.Name)
		}
	}
	for _, st := range b.Body {
		rewriteStmt(st, sc, lu)
	}
	sc.pop()
}

// rewriteStmt handles every statement kind that can appear nested inside a
// block. A nested *function declaration* is walked here (its name is already
// hoisted into the block frame by rewriteBlock, so a reference to it stays local
// — TDD-00094; its body's own top-level references still get renamed). The other
// declaration kinds (Class/Interface/Enum/TypeAlias/Import/Export) only appear at
// Program top level and are handled solely by rewriteTopLevelStmt.
func rewriteStmt(stmt ast.Statement, sc *scope, lu lookupTable) {
	switch s := stmt.(type) {
	case *ast.FunctionDeclaration:
		// The name is already bound by rewriteBlock's hoist pass; walk the
		// params/return type/body so a top-level reference inside gets mangled.
		rewriteFunctionLike(s, sc, lu)
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
		lu.checkBinding(s.Name, s.GetPos())
	case *ast.VarDeclarationList:
		for _, d := range s.Decls {
			if d.TypeAnnot != nil {
				rewriteType(d.TypeAnnot, sc, lu)
			}
			if d.Init != nil {
				d.Init = rewriteExpr(d.Init, sc, lu)
			}
			sc.bind(d.Name)
			lu.checkBinding(d.Name, d.GetPos())
		}
	case *ast.ArrayDestructuring:
		if s.Init != nil {
			s.Init = rewriteExpr(s.Init, sc, lu)
		}
		// A later element's default may reference an earlier one in the
		// same pattern (`[a, b = a]`, real JS) — rewrite each element's
		// own Default before binding it, so it still resolves as whatever
		// it would outside the pattern, then bind so the *next* element's
		// default sees it as local.
		for i := range s.Elems {
			if s.Elems[i].Default != nil {
				s.Elems[i].Default = rewriteExpr(s.Elems[i].Default, sc, lu)
			}
			sc.bind(s.Elems[i].Name)
			lu.checkBinding(s.Elems[i].Name, s.GetPos())
		}
	case *ast.ObjectDestructuring:
		if s.Init != nil {
			s.Init = rewriteExpr(s.Init, sc, lu)
		}
		for i := range s.Props {
			if s.Props[i].Default != nil {
				s.Props[i].Default = rewriteExpr(s.Props[i].Default, sc, lu)
			}
			sc.bind(s.Props[i].Local)
			lu.checkBinding(s.Props[i].Local, s.GetPos())
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
		for i, upd := range s.Update {
			s.Update[i] = rewriteExpr(upd, sc, lu)
		}
		rewriteBlock(s.Body, sc, lu)
		sc.pop()
	case *ast.ForOfStatement:
		s.Iterable = rewriteExpr(s.Iterable, sc, lu)
		sc.push()
		sc.bind(s.VarName)
		lu.checkBinding(s.VarName, s.GetPos())
		rewriteBlock(s.Body, sc, lu)
		sc.pop()
	case *ast.ForInStatement:
		s.Object = rewriteExpr(s.Object, sc, lu)
		sc.push()
		sc.bind(s.VarName)
		lu.checkBinding(s.VarName, s.GetPos())
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
			if s.Catch.Param != "" {
				sc.bind(s.Catch.Param)
				lu.checkBinding(s.Catch.Param, s.GetPos())
			}
			for i, dp := range s.Catch.ObjectPattern {
				sc.bind(dp.Local)
				lu.checkBinding(dp.Local, s.Catch.Pos)
				if dp.Default != nil {
					s.Catch.ObjectPattern[i].Default = rewriteExpr(dp.Default, sc, lu)
				}
			}
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
				break
			}
			// TDD-00049 Stage 2: a named import of one built-in module
			// member (`import { readFileSync } from 'fs'`) resolves
			// entirely at compile time into a synthesized member access on
			// that module's reserved marker identifier — indistinguishable
			// after this pass from hand-written `fs.readFileSync`, so it
			// reaches every existing Stage 1 dispatch site
			// (emit_call.go/emit_exprs_member.go/emit_exprs_types.go) with
			// no codegen changes at all. Guarded by the same !sc.bound
			// check as every other lookup here, so a local variable that
			// happens to share the imported name still shadows it.
			if ref, ok := lu.builtinMembers[e.Name]; ok {
				return ast.NewMemberExpression(ast.NewIdentifier(ref.Marker, e.GetPos()), ref.Member, e.GetPos())
			}
		}
	case *ast.AwaitExpression:
		e.Argument = rewriteExpr(e.Argument, sc, lu)
	case *ast.YieldExpression:
		if e.Argument != nil {
			e.Argument = rewriteExpr(e.Argument, sc, lu)
		}
	case *ast.BinaryExpression:
		e.Left = rewriteExpr(e.Left, sc, lu)
		e.Right = rewriteExpr(e.Right, sc, lu)
	case *ast.ConditionalExpression:
		e.Test = rewriteExpr(e.Test, sc, lu)
		e.Consequent = rewriteExpr(e.Consequent, sc, lu)
		e.Alternate = rewriteExpr(e.Alternate, sc, lu)
	case *ast.SequenceExpression:
		for i := range e.Exprs {
			e.Exprs[i] = rewriteExpr(e.Exprs[i], sc, lu)
		}
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
		bindParams(e.Params, sc, lu, e.GetPos())
		rewritePatternDefaults(e.Params, sc, lu)
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
	case *ast.FunctionExpression:
		// Same treatment as ArrowFunction — the body's identifiers must be
		// rewritten against the lookup table so they match mangled names
		// registered in the emitter's scope stack.
		sc.push()
		bindParams(e.Params, sc, lu, e.GetPos())
		rewritePatternDefaults(e.Params, sc, lu)
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
		rewriteBlock(e.Body, sc, lu)
		sc.pop()
	case *ast.TemplateLiteral:
		for i := range e.Exprs {
			e.Exprs[i] = rewriteExpr(e.Exprs[i], sc, lu)
		}
	case *ast.TaggedTemplateExpression:
		e.Tag = rewriteExpr(e.Tag, sc, lu)
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
		if e.Init != nil {
			e.Init = rewriteExpr(e.Init, sc, lu)
		}
	case *ast.NewEventEmitterExpression:
		if e.PayloadType != nil {
			rewriteType(e.PayloadType, sc, lu)
		}
	case *ast.NewErrorExpression:
		if e.Message != nil {
			e.Message = rewriteExpr(e.Message, sc, lu)
		}
		if e.Name != nil {
			e.Name = rewriteExpr(e.Name, sc, lu)
		}
		if e.Errors != nil {
			e.Errors = rewriteExpr(e.Errors, sc, lu)
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
	case *ast.ImportMetaUrl:
		// TDD-00055 Stage 1: resolved entirely at this stage, per-file,
		// into a plain string literal — codegen never sees this node.
		// "file://" + absolute path matches real Node/browser
		// import.meta.url's own convention.
		return ast.NewStringLiteral("file://"+lu.filePath, e.GetPos())
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
	// Composite-member descent. These were all previously skipped: a named type
	// used *only* inside a union (`A | B`), tuple (`[A, B]`), or intersection
	// (`A & B`, TDD-00078) never had its member names rewritten, so a mangled
	// declaration (any cross-file/renamed interface) went unresolved at that
	// member position. rewriteTypeName is idempotent (a mangled name is never
	// itself a lookup key), so the union/intersection head-copy sharing
	// members[0]'s Name/Fields with this node is harmless — each name resolves
	// once and a redundant second pass is a no-op.
	for _, m := range ta.UnionMembers {
		rewriteType(m, sc, lu)
	}
	for _, t := range ta.TupleElems {
		rewriteType(t, sc, lu)
	}
	for _, m := range ta.IntersectionMembers {
		rewriteType(m, sc, lu)
	}
	// keyof / indexed access / mapped types (TDD-00079). A named type referenced
	// only inside one of these would otherwise stay unrenamed while its
	// declaration got mangled — the same gap the composite members above had.
	rewriteType(ta.KeyofOperand, sc, lu)
	rewriteType(ta.IndexObject, sc, lu)
	rewriteType(ta.IndexKey, sc, lu)
	rewriteType(ta.MappedSource, sc, lu)
	rewriteType(ta.MappedValue, sc, lu)
	rewriteType(ta.CheckType, sc, lu)
	rewriteType(ta.ExtendsType, sc, lu)
	rewriteType(ta.TrueType, sc, lu)
	rewriteType(ta.FalseType, sc, lu)
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
