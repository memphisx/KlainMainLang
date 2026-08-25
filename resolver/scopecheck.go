package resolver

import (
	"KlainMainLang/ast"
	"fmt"
)

// checkLexicalScopes enforces the block-scoped redeclaration early-errors that
// mangleFileDecls (which only sees a file's *top-level* declarations) can't
// reach: a duplicate let/const/class/enum in the same nested block, and a
// let/const colliding with a var of the same name declared directly in the
// same scope. Real JS raises these as SyntaxErrors before the program runs.
//
// It runs per file on the pre-mangle AST so error messages carry the original
// binding name, and it deliberately only descends into *nested* scopes —
// module-top-level direct declarations remain mangleFileDecls's responsibility,
// so the two never double-report the same collision.
//
// V1 scope (see TDD-00070): only same-scope collisions are checked. The
// cross-nested-block case (`let x; { var x }`, a var in an inner block clashing
// with an outer lexical name) and function-parameter-vs-body collisions are
// deliberately not checked here — under-reporting is safe, whereas a false
// positive would reject valid code. Temporal-dead-zone reads before a
// declaration are already rejected by codegen (as an undefined variable), so
// they aren't re-checked here either.
func checkLexicalScopes(path string, prog *ast.Program) error {
	// Cross-block intersection at module top level: a `var` in a nested block
	// collides with a top-level `let`/`const`/`class`/`enum` of the same name
	// (`let x; { var x }`). Direct top-level `let x; var x` is mangleFileDecls's
	// job (it runs right after this), so only nested vars are gathered here.
	if err := checkVarLexicalIntersection(prog.Body, topLevelLexicalNames(prog.Body)); err != nil {
		return err
	}
	for _, stmt := range prog.Body {
		if err := descendScopes(path, stmt); err != nil {
			return err
		}
	}
	return nil
}

// topLevelLexicalNames returns the names directly declared with a lexical kind
// (let/const/class/enum) among a body's own statements.
func topLevelLexicalNames(body []ast.Statement) map[string]bool {
	names := map[string]bool{}
	for _, stmt := range body {
		for _, d := range declaredNamesOf(stmt) {
			if isLexicalKind(d.kind) {
				names[d.name] = true
			}
		}
	}
	return names
}

// isLexicalKind reports whether a declaration kind is block-scoped and lexical
// (as opposed to a function-scoped `var`/`function`) — the kinds a nested `var`
// of the same name is forbidden from shadowing.
func isLexicalKind(kind string) bool {
	return kind == "let" || kind == "const" || kind == "class" || kind == "enum"
}

// checkVarLexicalIntersection rejects a `var` declared anywhere in a nested
// scope of body whose name matches one of lexicalNames (the enclosing scope's
// own lexical bindings) — the spec's "VarDeclaredNames ∩ LexicallyDeclaredNames
// must be empty" early error, e.g. `let x; { var x }`. Only nested scopes are
// scanned; a `var` declared directly alongside the lexical binding is a
// same-scope collision already caught by checkScopeBody / mangleFileDecls.
func checkVarLexicalIntersection(body []ast.Statement, lexicalNames map[string]bool) error {
	if len(lexicalNames) == 0 {
		return nil
	}
	nested := map[string]ast.Pos{}
	gatherNestedVarNames(body, nested)
	for name := range lexicalNames {
		if pos, ok := nested[name]; ok {
			return fmt.Errorf("%d:%d: identifier '%s' has already been declared", pos.Line, pos.Col, name)
		}
	}
	return nil
}

// checkScopeBody applies the same-scope redeclaration rule to the direct
// declarations of one block/function body, then recurses into every nested
// scope each of those statements opens.
func checkScopeBody(path string, body []ast.Statement) error {
	seen := map[string]string{}  // name -> kind of its first declaration in this scope
	lexical := map[string]bool{} // subset of seen declared with a lexical kind
	for _, stmt := range body {
		for _, d := range declaredNamesOf(stmt) {
			if prev, dup := seen[d.name]; dup {
				// A repeated var, or a var and a same-named function, coexist as
				// one binding — everything else in the same scope is a real
				// redeclaration (this mirrors mangleFileDecls's top-level rule).
				if !isVarOrFuncKind(prev) || !isVarOrFuncKind(d.kind) {
					return fmt.Errorf("%d:%d: identifier '%s' has already been declared", stmt.GetPos().Line, stmt.GetPos().Col, d.name)
				}
			} else {
				seen[d.name] = d.kind
			}
			if isLexicalKind(d.kind) {
				lexical[d.name] = true
			}
		}
	}
	// Cross-block: a var in a nested scope colliding with a lexical binding here.
	if err := checkVarLexicalIntersection(body, lexical); err != nil {
		return err
	}
	for _, stmt := range body {
		if err := descendScopes(path, stmt); err != nil {
			return err
		}
	}
	return nil
}

// descendScopes recurses into whatever nested scopes a single statement opens,
// running checkScopeBody on each. It does not itself check the statement's own
// declaration (the enclosing checkScopeBody already did).
func descendScopes(path string, stmt ast.Statement) error {
	switch s := stmt.(type) {
	case *ast.BlockStatement:
		return checkScopeBody(path, s.Body)
	case *ast.IfStatement:
		if s.Consequent != nil {
			if err := checkScopeBody(path, s.Consequent.Body); err != nil {
				return err
			}
		}
		if s.Alternate != nil {
			// `else if` chains as a nested IfStatement; a bare `else { }` as a
			// BlockStatement — descendScopes handles both.
			return descendScopes(path, s.Alternate)
		}
		return nil
	case *ast.ForStatement:
		if s.Body != nil {
			return checkScopeBody(path, s.Body.Body)
		}
		return nil
	case *ast.ForOfStatement:
		if s.Body != nil {
			return checkScopeBody(path, s.Body.Body)
		}
		return nil
	case *ast.ForInStatement:
		if s.Body != nil {
			return checkScopeBody(path, s.Body.Body)
		}
		return nil
	case *ast.WhileStatement:
		if s.Body != nil {
			return checkScopeBody(path, s.Body.Body)
		}
		return nil
	case *ast.DoWhileStatement:
		if s.Body != nil {
			return checkScopeBody(path, s.Body.Body)
		}
		return nil
	case *ast.SwitchStatement:
		// A switch's entire case list shares one lexical scope in JS, so all
		// case bodies are checked together rather than per-case.
		var all []ast.Statement
		for _, c := range s.Cases {
			all = append(all, c.Body...)
		}
		return checkScopeBody(path, all)
	case *ast.TryStatement:
		if s.Body != nil {
			if err := checkScopeBody(path, s.Body.Body); err != nil {
				return err
			}
		}
		if s.Catch != nil && s.Catch.Body != nil {
			if err := checkScopeBody(path, s.Catch.Body.Body); err != nil {
				return err
			}
		}
		if s.Finally != nil {
			return checkScopeBody(path, s.Finally.Body)
		}
		return nil
	case *ast.LabeledStatement:
		return descendScopes(path, s.Body)
	case *ast.FunctionDeclaration:
		if s.Body != nil {
			return checkScopeBody(path, s.Body.Body)
		}
		return nil
	}
	return nil
}

// scopeDecl is one name a statement binds directly into its enclosing scope,
// tagged with the kind it was declared under (for the var/let/const/... rule).
type scopeDecl struct {
	name string
	kind string
}

// declaredNamesOf returns every name a single statement binds directly into its
// own enclosing scope. Nested blocks/bodies are not descended (their names
// belong to their own scopes). A destructuring pattern's leaf names are
// flattened out; a pattern shape too involved to read cleanly contributes no
// names rather than risking a wrong one (under-reporting is the safe failure).
func declaredNamesOf(stmt ast.Statement) []scopeDecl {
	switch s := stmt.(type) {
	case *ast.VarDeclaration:
		return []scopeDecl{{s.Name, s.Kind}}
	case *ast.VarDeclarationList:
		out := make([]scopeDecl, 0, len(s.Decls))
		for _, d := range s.Decls {
			out = append(out, scopeDecl{d.Name, d.Kind})
		}
		return out
	case *ast.ArrayDestructuring:
		var names []string
		for _, e := range s.Elems {
			collectArrayPatternNames(e, &names)
		}
		return tagAll(names, s.Kind)
	case *ast.ObjectDestructuring:
		var names []string
		for _, p := range s.Props {
			collectObjectPatternNames(p, &names)
		}
		return tagAll(names, s.Kind)
	case *ast.FunctionDeclaration:
		return []scopeDecl{{s.Name, "function"}}
	case *ast.ClassDeclaration:
		return []scopeDecl{{s.Name, "class"}}
	case *ast.EnumDeclaration:
		return []scopeDecl{{s.Name, "enum"}}
	}
	return nil
}

func tagAll(names []string, kind string) []scopeDecl {
	out := make([]scopeDecl, 0, len(names))
	for _, n := range names {
		if n != "" {
			out = append(out, scopeDecl{n, kind})
		}
	}
	return out
}

func collectArrayPatternNames(e ast.ArrayPatternElem, out *[]string) {
	if e.SubArray != nil {
		for _, sub := range e.SubArray {
			collectArrayPatternNames(sub, out)
		}
		return
	}
	if e.SubObject != nil {
		for _, sub := range e.SubObject {
			collectObjectPatternNames(sub, out)
		}
		return
	}
	if e.Name != "" {
		*out = append(*out, e.Name)
	}
}

func collectObjectPatternNames(p ast.DestructProp, out *[]string) {
	if p.SubArray != nil {
		for _, sub := range p.SubArray {
			collectArrayPatternNames(sub, out)
		}
		return
	}
	if p.SubObject != nil {
		for _, sub := range p.SubObject {
			collectObjectPatternNames(sub, out)
		}
		return
	}
	if p.Local != "" {
		*out = append(*out, p.Local)
	}
}

// gatherNestedVarNames collects every `var`-kind binding declared in a scope
// *nested* inside body (a block, loop, `if`, `switch`, `try`, or labeled body),
// but not one declared directly among body's own statements — those are a
// same-scope concern, handled separately. Recursion stops at a nested function
// boundary, since a `var` there belongs to that function's own environment.
func gatherNestedVarNames(body []ast.Statement, out map[string]ast.Pos) {
	for _, stmt := range body {
		switch stmt.(type) {
		case *ast.BlockStatement, *ast.IfStatement, *ast.ForStatement,
			*ast.ForOfStatement, *ast.ForInStatement, *ast.WhileStatement,
			*ast.DoWhileStatement, *ast.SwitchStatement, *ast.TryStatement,
			*ast.LabeledStatement:
			gatherVars(stmt, out)
		}
	}
}

// gatherVars adds every `var`-kind binding introduced by stmt or any scope
// nested inside it (recursively), stopping at a nested function declaration.
func gatherVars(stmt ast.Statement, out map[string]ast.Pos) {
	addName := func(name string, pos ast.Pos) {
		if name != "" {
			if _, exists := out[name]; !exists {
				out[name] = pos
			}
		}
	}
	switch s := stmt.(type) {
	case *ast.VarDeclaration:
		if s.Kind == "var" {
			addName(s.Name, s.GetPos())
		}
	case *ast.VarDeclarationList:
		for _, d := range s.Decls {
			if d.Kind == "var" {
				addName(d.Name, d.GetPos())
			}
		}
	case *ast.ArrayDestructuring:
		if s.Kind == "var" {
			var ns []string
			for _, e := range s.Elems {
				collectArrayPatternNames(e, &ns)
			}
			for _, n := range ns {
				addName(n, s.GetPos())
			}
		}
	case *ast.ObjectDestructuring:
		if s.Kind == "var" {
			var ns []string
			for _, p := range s.Props {
				collectObjectPatternNames(p, &ns)
			}
			for _, n := range ns {
				addName(n, s.GetPos())
			}
		}
	case *ast.BlockStatement:
		for _, c := range s.Body {
			gatherVars(c, out)
		}
	case *ast.IfStatement:
		if s.Consequent != nil {
			for _, c := range s.Consequent.Body {
				gatherVars(c, out)
			}
		}
		if s.Alternate != nil {
			gatherVars(s.Alternate, out)
		}
	case *ast.ForStatement:
		if s.Init != nil {
			gatherVars(s.Init, out)
		}
		if s.Body != nil {
			for _, c := range s.Body.Body {
				gatherVars(c, out)
			}
		}
	case *ast.ForOfStatement:
		if s.Kind == "var" {
			addName(s.VarName, s.GetPos())
		}
		if s.Body != nil {
			for _, c := range s.Body.Body {
				gatherVars(c, out)
			}
		}
	case *ast.ForInStatement:
		if s.Kind == "var" {
			addName(s.VarName, s.GetPos())
		}
		if s.Body != nil {
			for _, c := range s.Body.Body {
				gatherVars(c, out)
			}
		}
	case *ast.WhileStatement:
		if s.Body != nil {
			for _, c := range s.Body.Body {
				gatherVars(c, out)
			}
		}
	case *ast.DoWhileStatement:
		if s.Body != nil {
			for _, c := range s.Body.Body {
				gatherVars(c, out)
			}
		}
	case *ast.SwitchStatement:
		for _, cs := range s.Cases {
			for _, c := range cs.Body {
				gatherVars(c, out)
			}
		}
	case *ast.TryStatement:
		if s.Body != nil {
			for _, c := range s.Body.Body {
				gatherVars(c, out)
			}
		}
		if s.Catch != nil && s.Catch.Body != nil {
			for _, c := range s.Catch.Body.Body {
				gatherVars(c, out)
			}
		}
		if s.Finally != nil {
			for _, c := range s.Finally.Body {
				gatherVars(c, out)
			}
		}
	case *ast.LabeledStatement:
		gatherVars(s.Body, out)
	case *ast.FunctionDeclaration:
		// function boundary — its own `var`s belong to its environment
	}
}
