package parser

import (
	"KlainMainLang/ast"
	"KlainMainLang/jsdoc"
	"KlainMainLang/lexer"
	"fmt"
)

// parseAmbientDeclaration parses a `declare ...` ambient declaration and erases
// it to an empty statement. TypeScript ambient declarations describe types the
// surrounding environment provides — they carry no implementation — so this
// compiler emits no storage or code for them; a runtime *use* of a declared
// binding this compiler doesn't itself provide then surfaces as an ordinary
// undefined-variable error. The value is unblocking whole `.d.ts`-style files
// (ambient `const`/`function` signatures, `declare global`/`module`/`namespace`
// blocks) that previously failed to parse. The tokens are consumed by balance:
// a brace-bodied form is its `{ … }` block; a valueless `const`/`let`/`var` or
// a bodiless `function`/`type` ends at a `;` or a line break (ASI).
func (p *Parser) parseAmbientDeclaration() (ast.Statement, error) {
	pos := posOf(p.peek())
	p.advance() // consume 'declare'

	// ADR-00471: `declare function` and `declare var/let/const` become real
	// declarations instead of blanket erasure. An ambient function gets a
	// synthesized throwing body ("no implementation") — the program
	// compiles and links, matching TS's accept, and fails with a clear
	// error only if the ambient is actually called; consecutive redeclared
	// signatures collapse via the existing var/function-redeclaration rule.
	// An ambient variable is an ordinary uninitialized `var` (zero/undefined
	// default). Everything else (`declare class/namespace/module/global`)
	// keeps the balanced-token erasure below.
	if p.check(lexer.FUNCTION) {
		p.advance() // 'function'
		nameTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}
		fd, err := p.parseFunctionRest(nameTok.Literal, false, true, false)
		if err != nil {
			return nil, err
		}
		if fd.Body == nil {
			throwStmt := ast.NewThrowStatement(
				ast.NewNewErrorExpression("Error",
					ast.NewStringLiteral("ambient function '"+nameTok.Literal+"' has no implementation", pos), pos), pos)
			fd.Body = ast.NewBlockStatement([]ast.Statement{throwStmt}, pos)
			fd.IsAbstract = false
		}
		return fd, nil
	}
	// `declare namespace X {}` / `declare module X {}` (identifier-named —
	// the string-named external-module and `global` forms keep the erasure
	// below) route through the real namespace parser with ambient member
	// semantics (ADR-00474): var members zero-init, function members become
	// throwing stubs, so `Foo.Bar.foo = 5` after an ambient declaration works.
	if p.check(lexer.IDENT) && (p.peek().Literal == "namespace" || p.peek().Literal == "module") &&
		p.peekNth(1).Type == lexer.IDENT &&
		(p.peekNth(2).Type == lexer.LBRACE || p.peekNth(2).Type == lexer.DOT) {
		return p.parseNamespaceDecl(true)
	}

	// `declare [const] enum E { … }` is a real enum (ADR-00476) — members
	// without initializers auto-increment exactly like a non-ambient enum.
	if (p.check(lexer.IDENT) && p.peek().Literal == "enum") ||
		(p.check(lexer.CONST) && p.peekNth(1).Type == lexer.IDENT && p.peekNth(1).Literal == "enum") {
		return p.parseEnumDeclaration()
	}

	if p.check(lexer.VAR) || p.check(lexer.LET) || p.check(lexer.CONST) {
		p.advance() // kind (ambient bindings are all zero-initialized vars here)
		nameTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}
		var ta *ast.TypeAnnotation
		if p.match(lexer.COLON) {
			ta, err = p.parseTypeAnnotation("ts")
			if err != nil {
				return nil, err
			}
		}
		p.consumeSemicolon()
		return ast.NewVarDeclaration("var", nameTok.Literal, ta, nil, pos), nil
	}
	startLine := p.peek().Line
	depth := 0
	sawBrace := false
	for !p.check(lexer.EOF) {
		t := p.peek()
		switch t.Type {
		case lexer.LBRACE:
			depth++
			sawBrace = true
			p.advance()
			continue
		case lexer.RBRACE:
			if depth > 0 {
				depth--
				p.advance()
				if depth == 0 && sawBrace {
					return ast.NewBlockStatement(nil, pos), nil
				}
				continue
			}
			// A depth-0 '}' belongs to an enclosing block — stop before it.
			return ast.NewBlockStatement(nil, pos), nil
		case lexer.SEMICOLON:
			if depth == 0 {
				p.advance()
				return ast.NewBlockStatement(nil, pos), nil
			}
			p.advance()
			continue
		}
		// ASI: a non-brace ambient declaration ends at the next line break, once
		// we're outside any brace body (guard against a body brace opening on its
		// own line by not stopping on a leading '{').
		if depth == 0 && !sawBrace && t.Line > startLine && t.Type != lexer.LBRACE {
			return ast.NewBlockStatement(nil, pos), nil
		}
		p.advance()
	}
	return ast.NewBlockStatement(nil, pos), nil
}

func (p *Parser) parseStatement() (ast.Statement, error) {
	switch p.peek().Type {
	case lexer.LET, lexer.CONST, lexer.VAR:
		// `const enum Name { … }` — treat as an enum declaration, not a var.
		if p.peek().Type == lexer.CONST &&
			p.peekNth(1).Type == lexer.IDENT && p.peekNth(1).Literal == "enum" {
			return p.parseEnumDeclaration()
		}
		switch p.peekNth(1).Type {
		case lexer.LBRACKET:
			return p.parseArrayDestructuring()
		case lexer.LBRACE:
			return p.parseObjectDestructuring()
		}
		return p.parseVarDecl(true)
	case lexer.FUNCTION:
		return p.parseFunctionDecl(false, "")
	case lexer.CLASS:
		return p.parseClassDecl(false, "")
	case lexer.ABSTRACT:
		if p.peekNth(1).Type == lexer.CLASS {
			p.advance() // consume 'abstract'
			return p.parseClassDecl(true, "")
		}
	case lexer.IMPORT:
		// `import.meta.url` / dynamic `import(...)` (TDD-00055) reached at
		// statement-initial position (e.g. `import.meta.url;` alone, or
		// more realistically as part of a larger expression statement) —
		// neither is a real import *declaration*, so route to ordinary
		// expression parsing instead, where parseImportExpr gives each its
		// own clear handling (or rejection, for the not-yet-implemented
		// dynamic-call form) rather than parseImportDeclaration's
		// specifier-list parsing producing a confusing error.
		if p.peekNth(1).Type == lexer.DOT || p.peekNth(1).Type == lexer.LPAREN {
			return p.parseExpressionStatement()
		}
		// TS import-equals alias `import X = Y.Z;` (ADR-00456).
		if p.peekNth(1).Type == lexer.IDENT && p.peekNth(2).Type == lexer.ASSIGN {
			return p.parseImportEquals("", false)
		}
		return p.parseImportDeclaration()
	case lexer.EXPORT:
		return p.parseExportDeclaration()
	case lexer.ASYNC:
		if p.peekNth(1).Type == lexer.FUNCTION {
			p.advance() // consume 'async'
			return p.parseFunctionDecl(true, "")
		}
		// async arrow function as a statement (e.g., immediately invoked)
		expr, err := p.parseExpressionStatement()
		return expr, err
	case lexer.RETURN:
		return p.parseReturnStatement()
	case lexer.FOR:
		return p.parseForStatement()
	case lexer.DO:
		return p.parseDoWhileStatement()
	case lexer.WHILE:
		return p.parseWhileStatement()
	case lexer.IF:
		return p.parseIfStatement()
	case lexer.SWITCH:
		return p.parseSwitchStatement()
	case lexer.BREAK:
		return p.parseBreakStatement()
	case lexer.CONTINUE:
		return p.parseContinueStatement()
	case lexer.THROW:
		return p.parseThrowStatement()
	case lexer.TRY:
		return p.parseTryStatement()
	case lexer.LBRACE:
		return p.parseBlock()
	case lexer.SEMICOLON:
		p.advance()
		return ast.NewBlockStatement(nil, ast.Pos{}), nil
	}
	// Contextual keywords parsed as identifiers by the lexer.
	if p.peek().Type == lexer.IDENT {
		switch p.peek().Literal {
		case "interface":
			return p.parseInterfaceDecl()
		case "type":
			return p.parseTypeAliasDecl()
		case "enum":
			return p.parseEnumDeclaration()
		case "namespace":
			if p.peekNth(1).Type == lexer.IDENT {
				return p.parseNamespaceDecl(false)
			}
		case "module":
			// The pre-ES2015 internal-module spelling `module X { }` — a
			// synonym for `namespace X { }` (TDD-00148 Stage 1). Contextual:
			// requires IDENT `{` (or a dotted name, cleanly rejected inside)
			// so `module.exports`-style expressions and a variable named
			// `module` keep parsing as before.
			if p.peekNth(1).Type == lexer.IDENT &&
				(p.peekNth(2).Type == lexer.LBRACE || p.peekNth(2).Type == lexer.DOT) {
				return p.parseNamespaceDecl(false)
			}
		case "declare":
			return p.parseAmbientDeclaration()
		case "debugger":
			// `debugger;` — a no-op in AOT-compiled native output (there is no
			// attached inspector to break into), so parse and erase it to an
			// empty statement. `debugger` is a reserved word in JS/TS, so it can
			// never legitimately be an identifier here. See ADR-00372.
			pos := posOf(p.peek())
			p.advance()
			p.consumeSemicolon()
			return ast.NewBlockStatement(nil, pos), nil
		}
		// label: statement (e.g. `outer: for (...) { ... }`)
		if p.peekNth(1).Type == lexer.COLON {
			return p.parseLabeledStatement()
		}
	}
	return p.parseExpressionStatement()
}

// parseNamespaceDecl parses `namespace X { ... }` / `module X { ... }`
// (TDD-00095 V1, extended by TDD-00148 V2). Value members (function,
// const/let/var — exported or not) desugar to ordinary top-level
// declarations named ast.NamespaceMangle(X, member), recorded with their
// exportedness so `X.member` use sites resolve (and non-exported outside
// access is a clean rejection). Type members (class/interface/type/enum)
// desugar to *bare-name* top-level declarations, matching the qualified
// `new X.C(...)`/`extends X.C` final-segment precedent (ADR-00408).
// Returns the first desugared declaration (or an empty block for an empty
// namespace); the rest flow through pendingTopLevel. Dotted names
// (`module A.B.C`) stay a clean rejection (TDD-00148 V3).
func (p *Parser) parseNamespaceDecl(ambient bool) (ast.Statement, error) {
	nsTok := p.advance() // 'namespace' / 'module'
	ns, err := p.parseNamespaceName()
	if err != nil {
		return nil, err
	}
	decls, err := p.parseNamespaceBody(nsTok, ns, ambient)
	if err != nil {
		return nil, err
	}
	if len(decls) == 0 {
		return ast.NewBlockStatement(nil, ast.Pos{Line: nsTok.Line, Col: nsTok.Col}), nil
	}
	p.pendingTopLevel = append(p.pendingTopLevel, decls[1:]...)
	return decls[0], nil
}

// parseNamespaceName parses the (possibly dotted, `A.B.C` — TDD-00148 V3)
// name after `namespace`/`module`, returning the dotted string.
func (p *Parser) parseNamespaceName() (string, error) {
	nameTok, err := p.expect(lexer.IDENT)
	if err != nil {
		return "", err
	}
	ns := nameTok.Literal
	for p.check(lexer.DOT) {
		p.advance()
		seg, err := p.expect(lexer.IDENT)
		if err != nil {
			return "", err
		}
		ns = ns + "." + seg.Literal
	}
	return ns, nil
}

// parseNamespaceBody parses `{ ...members }` for the namespace named ns
// (dotted for a nested/dotted namespace) and returns the desugared
// declarations. Nested `namespace`/`module` members recurse with the
// extended dotted name.
func (p *Parser) parseNamespaceBody(nsTok lexer.Token, ns string, ambient bool) ([]ast.Statement, error) {
	if _, err := p.expect(lexer.LBRACE); err != nil {
		return nil, err
	}
	if p.namespaces == nil {
		p.namespaces = map[string]map[string]bool{}
	}
	if p.namespaces[ns] == nil {
		p.namespaces[ns] = map[string]bool{}
	}
	var decls []ast.Statement
	for !p.check(lexer.RBRACE) && !p.check(lexer.EOF) {
		exported := p.match(lexer.EXPORT)
		isAsync := false
		if p.check(lexer.ASYNC) && p.peekNth(1).Type == lexer.FUNCTION {
			p.advance()
			isAsync = true
		}
		isAbstract := false
		if p.check(lexer.ABSTRACT) && p.peekNth(1).Type == lexer.CLASS {
			p.advance()
			isAbstract = true
		}
		switch p.peek().Type {
		case lexer.IMPORT:
			// `[export] import X = N;` — a namespace-scoped alias (ADR-00456).
			if p.peekNth(1).Type == lexer.IDENT && p.peekNth(2).Type == lexer.ASSIGN {
				if _, err := p.parseImportEquals(ns, exported); err != nil {
					return nil, err
				}
				continue
			}
		case lexer.FUNCTION:
			// An ambient namespace's function member gets the same
			// throwing-stub treatment a top-level `declare function` does
			// (ADR-00471/ADR-00474).
			if ambient {
				p.advance() // 'function'
				nameTok, err := p.expect(lexer.IDENT)
				if err != nil {
					return nil, err
				}
				fd, err := p.parseFunctionRest(nameTok.Literal, false, true, false)
				if err != nil {
					return nil, err
				}
				if fd.Body == nil {
					throwStmt := ast.NewThrowStatement(
						ast.NewNewErrorExpression("Error",
							ast.NewStringLiteral("ambient function '"+nameTok.Literal+"' has no implementation", posOf(nsTok)), posOf(nsTok)), posOf(nsTok))
					fd.Body = ast.NewBlockStatement([]ast.Statement{throwStmt}, posOf(nsTok))
					fd.IsAbstract = false
				}
				p.namespaces[ns][fd.Name] = exported
				fd.Name = ast.NamespaceMangle(ns, fd.Name)
				decls = append(decls, fd)
				continue
			}
			fd, err := p.parseFunctionDecl(isAsync, "")
			if err != nil {
				return nil, err
			}
			p.namespaces[ns][fd.Name] = exported
			fd.Name = ast.NamespaceMangle(ns, fd.Name)
			decls = append(decls, fd)
			continue
		case lexer.CONST, lexer.LET, lexer.VAR:
			// `const enum Name {…}` is an enum member, not a const binding —
			// the same disambiguation parseStatement does.
			if p.peek().Type == lexer.CONST && p.peekNth(1).Type == lexer.IDENT && p.peekNth(1).Literal == "enum" {
				ed, err := p.parseEnumDeclaration()
				if err != nil {
					return nil, err
				}
				decls = append(decls, ed)
				continue
			}
			// An ambient namespace's binding member (`const y: number;`) is
			// a zero-initialized var, like a top-level `declare var`
			// (ADR-00474) — no initializer required, `const`-ness dropped.
			if ambient {
				p.advance() // kind
				nameTok, err := p.expect(lexer.IDENT)
				if err != nil {
					return nil, err
				}
				var ta *ast.TypeAnnotation
				if p.match(lexer.COLON) {
					ta, err = p.parseTypeAnnotation("ts")
					if err != nil {
						return nil, err
					}
				}
				p.consumeSemicolon()
				vd := ast.NewVarDeclaration("var", nameTok.Literal, ta, nil, posOf(nsTok))
				p.namespaces[ns][vd.Name] = exported
				vd.Name = ast.NamespaceMangle(ns, vd.Name)
				decls = append(decls, vd)
				continue
			}
			vd, err := p.parseVarDecl(true)
			if err != nil {
				return nil, err
			}
			switch d := vd.(type) {
			case *ast.VarDeclaration:
				p.namespaces[ns][d.Name] = exported
				d.Name = ast.NamespaceMangle(ns, d.Name)
				decls = append(decls, d)
			default:
				return nil, fmt.Errorf("%d:%d: a namespace const/let member must declare exactly one binding", nsTok.Line, nsTok.Col)
			}
			continue
		case lexer.CLASS:
			cd, err := p.parseClassDecl(isAbstract, "")
			if err != nil {
				return nil, err
			}
			decls = append(decls, cd)
			continue
		}
		if p.check(lexer.IDENT) {
			switch p.peek().Literal {
			case "namespace", "module":
				// Nested namespace (TDD-00148 V3): recurse with the extended
				// dotted name; its desugared members flatten into this list.
				if p.peekNth(1).Type == lexer.IDENT {
					p.advance() // 'namespace'/'module'
					sub, err := p.parseNamespaceName()
					if err != nil {
						return nil, err
					}
					subDecls, err := p.parseNamespaceBody(nsTok, ns+"."+sub, ambient)
					if err != nil {
						return nil, err
					}
					decls = append(decls, subDecls...)
					continue
				}
			case "declare":
				// An ambient member (`export declare var x;` — ADR-00462):
				// erased exactly like a top-level ambient declaration.
				if _, err := p.parseAmbientDeclaration(); err != nil {
					return nil, err
				}
				continue
			case "interface":
				id, err := p.parseInterfaceDecl()
				if err != nil {
					return nil, err
				}
				decls = append(decls, id)
				continue
			case "type":
				td, err := p.parseTypeAliasDecl()
				if err != nil {
					return nil, err
				}
				decls = append(decls, td)
				continue
			case "enum":
				ed, err := p.parseEnumDeclaration()
				if err != nil {
					return nil, err
				}
				decls = append(decls, ed)
				continue
			}
		}
		// Anything else is an executable statement (`module M { doInit(); }`)
		// — real TS runs namespace-body statements at initialization; here
		// they flatten to top level in declaration order like every other
		// desugared member (ADR-00468). Genuinely invalid input still errors
		// from parseStatement itself.
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		decls = append(decls, stmt)
	}
	if _, err := p.expect(lexer.RBRACE); err != nil {
		return nil, err
	}
	return decls, nil
}

func (p *Parser) parseLabeledStatement() (*ast.LabeledStatement, error) {
	tok := p.advance() // label identifier
	label := tok.Literal
	if _, err := p.expect(lexer.COLON); err != nil {
		return nil, err
	}
	body, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	return ast.NewLabeledStatement(label, body, posOf(tok)), nil
}

// parseVarDecl parses `let/const/var name[: type][= init][, name2...];` —
// one or more comma-separated declarators sharing one let/const/var. A
// single declarator returns a plain *ast.VarDeclaration (unchanged from
// before multi-declarator support existed); two or more return an
// *ast.VarDeclarationList instead (see its own doc comment for why that's
// not just a BlockStatement of several VarDeclarations). Returns
// ast.Statement rather than a concrete type since callers (parseStatement,
// parseForStatement's init clause) only ever use the result as a generic
// statement — neither destructures a concrete *ast.VarDeclaration field
// afterward.
func (p *Parser) parseVarDecl(consumeSemi bool) (ast.Statement, error) {
	doc := p.takeDoc()
	tok := p.advance() // let / const / var
	pos := posOf(tok)

	first, err := p.parseOneVarDeclarator(tok.Literal, pos, doc)
	if err != nil {
		return nil, err
	}
	decls := []*ast.VarDeclaration{first}
	for p.match(lexer.COMMA) {
		declTok := p.peek()
		d, err := p.parseOneVarDeclarator(tok.Literal, posOf(declTok), nil)
		if err != nil {
			return nil, err
		}
		decls = append(decls, d)
	}

	if consumeSemi {
		p.consumeSemicolon()
	}

	if len(decls) == 1 {
		return decls[0], nil
	}
	return ast.NewVarDeclarationList(decls, pos), nil
}

// parseOneVarDeclarator parses a single `name[: type][= init]` declarator —
// either the first one right after let/const/var, or a subsequent one after
// a comma. doc is only ever non-nil for the first declarator: a JSDoc
// comment precedes the statement as a whole, not each individual name.
func (p *Parser) parseOneVarDeclarator(kind string, pos ast.Pos, doc *jsdoc.Comment) (*ast.VarDeclaration, error) {
	nameTok, err := p.expect(lexer.IDENT)
	if err != nil {
		return nil, err
	}

	var ta *ast.TypeAnnotation
	// TS type annotation
	if p.check(lexer.COLON) {
		p.advance()
		ta, err = p.parseTypeAnnotation("ts")
		if err != nil {
			return nil, err
		}
	}
	// JSDoc overrides TS annotation
	if doc != nil {
		if t := doc.GetType(); t != "" {
			ta = jsdocTypeAnnotation(t)
		}
	}

	var init ast.Expression
	if p.match(lexer.ASSIGN) {
		init, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	} else if kind == "const" {
		// A `const` with no initializer is an early SyntaxError in JS
		// (strict or sloppy alike). The for-of/for-in loop-variable forms
		// (`for (const x of …)`), which legitimately have no `= init`, go
		// through their own dedicated parsers and never reach here.
		return nil, fmt.Errorf("%d:%d: 'const' declaration '%s' must be initialized", nameTok.Line, nameTok.Col, nameTok.Literal)
	}

	return ast.NewVarDeclaration(kind, nameTok.Literal, ta, init, pos), nil
}

// defaultName, when non-empty, is used as the function's name if no IDENT
// immediately follows `function` — the anonymous `export default function()
// { ... }` form (TDD-00042); every other caller passes "" and gets the
// ordinary "name required" check.
func (p *Parser) parseFunctionDecl(isAsync bool, defaultName string) (*ast.FunctionDeclaration, error) {
	doc := p.takeDoc()
	p.advance() // 'function'
	// `function* name() {}` / `async function* name() {}` (TDD-00061) — V1
	// scope is a top-level/nested function declaration only, so this check
	// lives here rather than in parseFunctionRest (also called for class
	// methods and, via parser_literals.go's own separate `case
	// lexer.FUNCTION:`, function expressions — neither gets generator
	// support in V1).
	isGenerator := p.match(lexer.STAR)

	name := defaultName
	if p.check(lexer.IDENT) {
		name = p.advance().Literal
	} else if defaultName == "" {
		if _, err := p.expect(lexer.IDENT); err != nil {
			return nil, err
		}
	}
	fd, err := p.parseFunctionRest(name, isAsync, false, true)
	if err != nil {
		return nil, err
	}
	fd.IsGenerator = isGenerator
	// TS overload group: one or more body-less signatures immediately followed
	// by the implementation, all sharing one name. Signatures are erased —
	// only the implementation survives into the AST. (The corpus's exported
	// overload groups repeat `export` per line, so a leading `export` between
	// group members is tolerated and folds into the caller's own export
	// handling of the first declaration.)
	for fd.IsOverloadSig {
		sigTok := p.peek()
		p.match(lexer.EXPORT)
		nextAsync := false
		if p.check(lexer.ASYNC) && p.peekNth(1).Type == lexer.FUNCTION {
			p.advance()
			nextAsync = true
		}
		if !p.check(lexer.FUNCTION) {
			return nil, fmt.Errorf("%d:%d: overload signature for '%s' must be followed by another overload signature or its implementation", sigTok.Line, sigTok.Col, name)
		}
		p.advance() // 'function'
		isGen := p.match(lexer.STAR)
		nTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}
		if nTok.Literal != name {
			return nil, fmt.Errorf("%d:%d: expected the implementation of overloaded function '%s', got 'function %s'", nTok.Line, nTok.Col, name, nTok.Literal)
		}
		fd, err = p.parseFunctionRest(name, nextAsync, false, true)
		if err != nil {
			return nil, err
		}
		fd.IsGenerator = isGen
	}
	// TDD-00125: type an otherwise-untyped parameter / return from a leading
	// `@param {T} name` / `@returns {T}`, the "typed JS" workflow. Fills in
	// only where there is no inline annotation (an inline `: T` wins).
	applyJSDocFuncTypes(fd, doc)
	// TDD-00010 V2: `/** @erased */` opts a generic function out of default
	// monomorphization. Validated here (not deferred to codegen) so the
	// error points at the declaration itself, same as every other JSDoc
	// annotation's validation shape in this parser.
	if doc != nil && doc.HasTag("erased") {
		if len(fd.TypeParams) == 0 {
			return nil, fmt.Errorf("%d:%d: @erased requires '%s' to declare a type parameter, e.g. 'function %s<T>(...)' (see docs/tdd/TDD-00010.md)", fd.GetPos().Line, fd.GetPos().Col, fd.Name, fd.Name)
		}
		fd.Erased = true
	}
	return fd, nil
}

// applyJSDocFuncTypes fills an untyped function's parameter and return types
// from its leading JSDoc `@param {T} name` / `@returns {T}` tags (TDD-00125).
// It only fills a slot that has no inline annotation — an inline `: T` always
// wins — and skips destructured parameters (a pattern has no single name to
// key a @param by, and already requires an explicit annotation). A JSDoc type
// string flows through the same `TypeAnnotation{Name, Source:"jsdoc"}` seam an
// inline annotation and `@type` produce, so codegen needs no change and the
// supported type grammar is exactly `@type`'s.
func applyJSDocFuncTypes(fd *ast.FunctionDeclaration, doc *jsdoc.Comment) {
	if fd == nil || doc == nil {
		return
	}
	// `@template T` declares a generic type parameter — the JSDoc form of a
	// `<T>` list (TDD-00125 Stage 3). An inline `<T>` wins; otherwise the
	// function becomes generic, driving the same monomorphization a TS generic
	// does (its `@param {T}`/`@returns {T}` positions are filled below).
	if len(fd.TypeParams) == 0 {
		if tps := doc.Templates(); len(tps) > 0 {
			for _, tp := range tps {
				fd.TypeParams = append(fd.TypeParams, tp.Name)
				if tp.Constraint != "" {
					fd.TypeParamConstraints = append(fd.TypeParamConstraints,
						&ast.TypeAnnotation{Name: tp.Constraint, Source: "jsdoc"})
				} else {
					fd.TypeParamConstraints = append(fd.TypeParamConstraints, nil)
				}
			}
		}
	}
	for i := range fd.Params {
		prm := &fd.Params[i]
		if prm.Type != nil || prm.ArrayPattern != nil || prm.ObjectPattern != nil {
			continue
		}
		if t := doc.ParamType(prm.Name); t != "" {
			prm.Type = jsdocTypeAnnotation(t)
		}
	}
	if fd.ReturnType == nil {
		if t := doc.ReturnType(); t != "" {
			fd.ReturnType = jsdocTypeAnnotation(t)
		}
	}
}

// parseFunctionRest parses the `(params) : retType? { body }` tail shared by
// a top-level `function NAME` declaration and a class method/constructor
// (whose name is already known and consumed by the caller before this runs).
// bodyOptional is TDD-00009 Stage 4: an abstract method signature
// (`abstract foo(): T;`) has no body at all — terminated by `;` instead of
// a `{...}` block. Never set by a top-level function (always false there).
// overloadOK permits a body-less TS overload *signature* (`f(x: string):
// void;` — terminated by `;`, not abstract): the declaration comes back with
// Body nil and IsOverloadSig set, and the caller is responsible for erasing
// it and verifying an implementation follows. Only the two declaration
// contexts where TS permits overloads set it (top-level `function` and class
// members); function expressions and object-literal methods never do.
func (p *Parser) parseFunctionRest(name string, isAsync, bodyOptional, overloadOK bool) (*ast.FunctionDeclaration, error) {
	// Position of whatever comes right after the already-consumed name (a
	// `<` or the opening `(`) — close enough to the declaration's own
	// position for error-reporting purposes. Backfilled via SetPos on every
	// returned FunctionDeclaration below, since the struct literals here
	// can't set the unexported pos field directly (see SetPos's own doc
	// comment for why this was a real, pre-existing "always 0:0" bug).
	pos := posOf(p.peek())
	// Optional `<T>` type-parameter list (TDD-00010 V1). Parsed uniformly for
	// both a top-level function and a class method/constructor — codegen
	// only acts on it for the former today; a generic method parses cleanly
	// but isn't specially instantiated yet (same "parse ahead of codegen"
	// shape parseNewGenericBody already uses for classes).
	var typeParams []string
	var typeParamConstraints []*ast.TypeAnnotation
	if p.check(lexer.LT) {
		tp, tc, err := p.parseTypeParamList(name + "<T>")
		if err != nil {
			return nil, err
		}
		typeParams = tp
		typeParamConstraints = tc
	}
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	params, err := p.parseParamList()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}

	var retType *ast.TypeAnnotation
	if p.check(lexer.COLON) {
		p.advance()
		retType, err = p.parseTypeAnnotation("ts")
		if err != nil {
			return nil, err
		}
	}

	if bodyOptional && p.check(lexer.SEMICOLON) {
		p.advance()
		fd := &ast.FunctionDeclaration{
			Name: name, TypeParams: typeParams, TypeParamConstraints: typeParamConstraints, Params: params, ReturnType: retType, Body: nil, IsAsync: isAsync, IsAbstract: true,
		}
		fd.SetPos(pos)
		return fd, nil
	}

	// A TS overload signature: no body, terminated by `;`. Comes back flagged
	// for the caller to erase after verifying the implementation follows.
	if overloadOK && !bodyOptional && p.check(lexer.SEMICOLON) {
		p.advance()
		fd := &ast.FunctionDeclaration{
			Name: name, TypeParams: typeParams, TypeParamConstraints: typeParamConstraints, Params: params, ReturnType: retType, Body: nil, IsAsync: isAsync, IsOverloadSig: true,
		}
		fd.SetPos(pos)
		return fd, nil
	}

	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}

	// A function whose body's own first statement is a literal "use strict"
	// directive is early-error-checked the same way real strict-mode code
	// is: a non-simple parameter list (default/rest/destructured) is a
	// SyntaxError, and so is binding 'eval' or 'arguments' as a parameter
	// name. This only fires on the textually-first-statement case (real
	// JS's directive prologue can hold more than one string-literal
	// statement before "use strict") — narrower than the full spec rule,
	// but covers the shape every real "use strict" function actually uses.
	if bodyStartsWithUseStrict(body) {
		for _, prm := range params {
			if prm.ArrayPattern != nil || prm.ObjectPattern != nil || prm.Rest || prm.Default != nil {
				return nil, fmt.Errorf("%d:%d: a strict-mode function cannot have a non-simple parameter list", pos.Line, pos.Col)
			}
			if prm.Name == "eval" || prm.Name == "arguments" {
				return nil, fmt.Errorf("%d:%d: '%s' cannot be a parameter name in strict mode", pos.Line, pos.Col, prm.Name)
			}
		}
		// ...and neither may a let/const/var (or for-of/for-in loop variable)
		// inside the body bind those two names.
		if err := strictBindingError(body.Body); err != nil {
			return nil, err
		}
	}

	fd := &ast.FunctionDeclaration{
		Name: name, TypeParams: typeParams, TypeParamConstraints: typeParamConstraints, Params: params, ReturnType: retType, Body: body, IsAsync: isAsync,
	}
	fd.SetPos(pos)
	return fd, nil
}

// bodyStartsWithUseStrict reports whether body's first statement is a bare
// "use strict" directive (an expression-statement string literal) — the
// shape every real "use strict" function body uses in practice.
func bodyStartsWithUseStrict(body *ast.BlockStatement) bool {
	if body == nil || len(body.Body) == 0 {
		return false
	}
	es, ok := body.Body[0].(*ast.ExpressionStatement)
	if !ok {
		return false
	}
	sl, ok := es.Expr.(*ast.StringLiteral)
	return ok && sl.Value == "use strict"
}

// strictReservedBinding reports whether name is one of the two identifiers a
// strict-mode BindingIdentifier may not bind — `eval` / `arguments` (an early
// SyntaxError, ES BindingIdentifier static semantics). Checked only inside a
// context this parser has already established as strict (a "use strict"
// function body or an always-strict class body), matching the same narrow,
// no-global-strict-context subset the parameter-name check already covers —
// see docs/status/LANGUAGE-CONSTRUCTS.md.
func strictReservedBinding(name string) bool {
	return name == "eval" || name == "arguments"
}

// strictBindingError walks the statements of a strict-mode function/method body
// and returns an early SyntaxError if any let/const/var declaration (or a
// for-of/for-in loop variable) binds `eval` or `arguments`. It recurses through
// the ordinary control-flow statements that share the body's own lexical scope,
// but stops at a nested function/class boundary: this parser tracks strictness
// per function locally (no inherited global strict context — see the status
// doc), so a nested function is re-checked on its own terms when it is parsed.
func strictBindingError(stmts []ast.Statement) error {
	for _, s := range stmts {
		if err := strictBindingErrorStmt(s); err != nil {
			return err
		}
	}
	return nil
}

func strictBindingReject(name string, pos ast.Pos) error {
	return fmt.Errorf("%d:%d: '%s' cannot be used as a binding name in strict mode", pos.Line, pos.Col, name)
}

func strictBindingErrorStmt(s ast.Statement) error {
	switch n := s.(type) {
	case nil:
		return nil
	case *ast.VarDeclaration:
		if strictReservedBinding(n.Name) {
			return strictBindingReject(n.Name, n.GetPos())
		}
	case *ast.VarDeclarationList:
		for _, d := range n.Decls {
			if strictReservedBinding(d.Name) {
				return strictBindingReject(d.Name, d.GetPos())
			}
		}
	case *ast.BlockStatement:
		if n == nil {
			return nil
		}
		return strictBindingError(n.Body)
	case *ast.IfStatement:
		if err := strictBindingErrorStmt(n.Consequent); err != nil {
			return err
		}
		return strictBindingErrorStmt(n.Alternate)
	case *ast.ForStatement:
		if err := strictBindingErrorStmt(n.Init); err != nil {
			return err
		}
		return strictBindingErrorStmt(n.Body)
	case *ast.ForOfStatement:
		if strictReservedBinding(n.VarName) {
			return strictBindingReject(n.VarName, n.GetPos())
		}
		return strictBindingErrorStmt(n.Body)
	case *ast.ForInStatement:
		if strictReservedBinding(n.VarName) {
			return strictBindingReject(n.VarName, n.GetPos())
		}
		return strictBindingErrorStmt(n.Body)
	case *ast.WhileStatement:
		return strictBindingErrorStmt(n.Body)
	case *ast.DoWhileStatement:
		return strictBindingErrorStmt(n.Body)
	case *ast.SwitchStatement:
		for _, c := range n.Cases {
			if err := strictBindingError(c.Body); err != nil {
				return err
			}
		}
	case *ast.TryStatement:
		if err := strictBindingErrorStmt(n.Body); err != nil {
			return err
		}
		if n.Catch != nil {
			if err := strictBindingErrorStmt(n.Catch.Body); err != nil {
				return err
			}
		}
		return strictBindingErrorStmt(n.Finally)
	case *ast.LabeledStatement:
		return strictBindingErrorStmt(n.Body)
	}
	return nil
}

func (p *Parser) parseParamList() ([]ast.Param, error) {
	// Capture-and-clear the constructor-parameter gate immediately: it must
	// apply to this list only, never to a nested function/arrow parameter
	// list encountered inside a default-value expression or the body.
	allowPropParams := p.inCtorParams
	p.inCtorParams = false
	var params []ast.Param
	for !p.check(lexer.RPAREN) && !p.check(lexer.EOF) {
		// A leading `this` parameter (`function f(this: T, x: number)`) is TS's
		// explicit-`this` typing form — purely a type-checker annotation that
		// real TS erases at emit and that is never a runtime argument. It is only
		// valid as the first parameter. Consume it (and its annotation) and drop
		// it, so the remaining parameters bind by their real positions. `this` is
		// the reserved THIS token, not an IDENT. See ADR-00372.
		if len(params) == 0 && p.check(lexer.THIS) {
			p.advance() // consume 'this'
			if p.check(lexer.COLON) {
				p.advance()
				if _, err := p.parseTypeAnnotation("ts"); err != nil {
					return nil, err
				}
			}
			if !p.match(lexer.COMMA) {
				break
			}
			continue
		}

		// TS parameter properties: `constructor(public x: number, readonly y:
		// string)` — an accessibility modifier and/or contextual `readonly`
		// before the parameter name. Only legal in a constructor's parameter
		// list (p.inCtorParams); `readonly` is a modifier only when a real
		// parameter name follows, so a parameter literally named `readonly`
		// keeps working.
		var propVis string
		var propSeen, propReadonly bool
		for {
			switch p.peek().Type {
			case lexer.PUBLIC:
				propSeen, propVis = true, "public"
				p.advance()
				continue
			case lexer.PRIVATE:
				propSeen, propVis = true, "private"
				p.advance()
				continue
			case lexer.PROTECTED:
				propSeen, propVis = true, "protected"
				p.advance()
				continue
			}
			if p.check(lexer.IDENT) && p.peek().Literal == "readonly" && p.peekNth(1).Type == lexer.IDENT {
				propSeen, propReadonly = true, true
				p.advance()
				continue
			}
			break
		}
		if propSeen && !allowPropParams {
			return nil, fmt.Errorf("%d:%d: a parameter property (public/private/protected/readonly) is only allowed in a class constructor", p.peek().Line, p.peek().Col)
		}

		rest := p.match(lexer.ELLIPSIS)

		// Destructured parameter (`{x, y}: T` / `[a, b]: T[]`, and nested
		// shapes like `[[a, b]]: number[][]` — TDD-00065 Stage 2) — always
		// requires an explicit type annotation, since unlike a plain scalar
		// param there's no sensible unannotated default to fall back to
		// (registerFunctions defaults a bare param to `number`; there's no
		// "number" shape for a pattern). Combining a pattern with `...`/`?`/a
		// whole-parameter `= default` is real, valid TS but adds real
		// complexity (rest must bind a collector, not a nested pattern at
		// all; a pattern default needs a null/undefined check on the whole
		// incoming value before unpacking) — out of scope, rejected below
		// with a clear error rather than silently mishandled. Uses the same
		// shared pattern-element grammar every other destructuring position
		// does, so holes / renames / `...rest` / nesting stay identical.
		if p.check(lexer.LBRACE) || p.check(lexer.LBRACKET) {
			if rest {
				return nil, fmt.Errorf("%d:%d: a rest parameter cannot be a destructuring pattern", p.peek().Line, p.peek().Col)
			}
			var arrPat []ast.ArrayPatternElem
			var objPat []ast.DestructProp
			if p.check(lexer.LBRACE) {
				var err error
				objPat, err = p.parseObjectPatternProps()
				if err != nil {
					return nil, err
				}
			} else {
				var err error
				arrPat, err = p.parseArrayPatternElems()
				if err != nil {
					return nil, err
				}
			}
			var ta *ast.TypeAnnotation
			if p.check(lexer.COLON) {
				p.advance()
				var err error
				ta, err = p.parseTypeAnnotation("ts")
				if err != nil {
					return nil, err
				}
			}
			if ta == nil {
				return nil, fmt.Errorf("%d:%d: a destructured parameter requires an explicit type annotation", p.peek().Line, p.peek().Col)
			}
			if p.check(lexer.ASSIGN) {
				return nil, fmt.Errorf("%d:%d: a default value on a destructured parameter is not yet supported", p.peek().Line, p.peek().Col)
			}
			syntheticName := fmt.Sprintf("__param%d", len(params))
			params = append(params, ast.Param{Name: syntheticName, Type: ta, ArrayPattern: arrPat, ObjectPattern: objPat})
			if !p.match(lexer.COMMA) {
				break
			}
			continue
		}

		nameTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}
		// Duplicate parameter names are a SyntaxError in real JS for any
		// non-simple parameter list (and always for TypeScript, which is
		// this compiler's actual source language) — reject unconditionally
		// rather than modeling sloppy-mode's narrower allowance.
		for _, prm := range params {
			if prm.Name == nameTok.Literal && prm.ArrayPattern == nil && prm.ObjectPattern == nil {
				return nil, fmt.Errorf("%d:%d: duplicate parameter name '%s'", nameTok.Line, nameTok.Col, nameTok.Literal)
			}
		}
		optional := p.match(lexer.QUESTION)
		var ta *ast.TypeAnnotation
		if p.check(lexer.COLON) {
			p.advance()
			ta, err = p.parseTypeAnnotation("ts")
			if err != nil {
				return nil, err
			}
		}
		var dflt ast.Expression
		if !rest && p.match(lexer.ASSIGN) {
			dflt, err = p.parseAssignment()
			if err != nil {
				return nil, err
			}
		}
		params = append(params, ast.Param{Name: nameTok.Literal, Type: ta, Rest: rest, Default: dflt, Optional: optional, PropVisibility: propVis, PropReadonly: propReadonly})
		if rest {
			break // rest param must be last
		}
		if !p.match(lexer.COMMA) {
			break
		}
	}
	// Duplicate parameter names are a SyntaxError in real JS for any
	// non-simple parameter list (and always for TypeScript, which is this
	// compiler's actual source language) — reject unconditionally rather
	// than modeling sloppy-mode's narrower allowance. Checked against each
	// param's real bound name(s): a destructured param's synthetic Name
	// (e.g. "__param0") never collides with anything, but its pattern's
	// own field/element names do bind in the function body and must be
	// checked the same as a plain param name would be.
	seen := make(map[string]bool, len(params))
	for _, prm := range params {
		var names []string
		switch {
		case prm.ArrayPattern != nil:
			for _, elem := range prm.ArrayPattern {
				if elem.Name != "" {
					names = append(names, elem.Name)
				}
			}
		case prm.ObjectPattern != nil:
			for _, prop := range prm.ObjectPattern {
				names = append(names, prop.Local)
			}
		default:
			names = []string{prm.Name}
		}
		for _, n := range names {
			if seen[n] {
				return nil, fmt.Errorf("%d:%d: duplicate parameter name '%s'", p.peek().Line, p.peek().Col, n)
			}
			seen[n] = true
		}
	}
	return params, nil
}

func (p *Parser) parseReturnStatement() (*ast.ReturnStatement, error) {
	tok := p.advance() // 'return'
	pos := posOf(tok)
	var val ast.Expression
	// JS's ASI restriction: no line terminator is allowed between `return`
	// and its expression. `return\nfoo()` is `return;` followed by its own
	// `foo();` statement, NOT `return foo();` — without this check,
	// anything after a bare `return` on the next line would get silently
	// parsed as the return's own value expression instead of becoming the
	// dead code it looks like, which is a much more confusing failure mode
	// than a clean parse error would be.
	if p.peek().Line == tok.Line && !p.check(lexer.SEMICOLON) && !p.check(lexer.RBRACE) && !p.check(lexer.EOF) {
		var err error
		val, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}
	p.consumeSemicolon()
	return ast.NewReturnStatement(val, pos), nil
}

func (p *Parser) parseForStatement() (ast.Statement, error) {
	tok := p.advance() // 'for'
	pos := posOf(tok)

	// `for await (const x of asyncIter)` — async iteration (TDD-00085). The
	// `await` keyword sits between `for` and `(`; it is only meaningful on a
	// for-of loop, so a `for await` that isn't a for-of is a clean error below.
	isAwait := false
	if p.check(lexer.AWAIT) {
		p.advance()
		isAwait = true
	}

	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}

	// Detect for-of and for-in: for (let/const/var name of/in ...)
	if p.check(lexer.LET) || p.check(lexer.CONST) || p.check(lexer.VAR) {
		if p.peekNth(2).Type == lexer.IDENT && p.peekNth(2).Literal == "of" {
			f, err := p.parseForOfBody(pos)
			if f != nil {
				f.Await = isAwait
			}
			return f, err
		}
		if p.peekNth(2).Type == lexer.IDENT && p.peekNth(2).Literal == "in" {
			if isAwait {
				return nil, fmt.Errorf("%d:%d: 'for await' requires a for-of loop, not for-in", pos.Line, pos.Col)
			}
			return p.parseForInBody(pos)
		}
		// Destructuring loop variable — `for (const [a, b] of …)` /
		// `for (const { x, y } of …)` (TDD-00065 Stage 1). A pattern in
		// for-*in* stays unsupported (falls through to the C-style path,
		// which cleanly rejects it) — near-useless in practice, and this
		// compiler's for-in is already narrow.
		if p.peekNth(1).Type == lexer.LBRACKET || p.peekNth(1).Type == lexer.LBRACE {
			f, err := p.parseForOfPatternBody(pos)
			if f != nil {
				f.Await = isAwait
			}
			return f, err
		}
	}

	if isAwait {
		return nil, fmt.Errorf("%d:%d: 'for await' requires a for-of loop over an async iterable (TDD-00085)", pos.Line, pos.Col)
	}

	// Init (optional)
	var init ast.Statement
	if !p.check(lexer.SEMICOLON) {
		var err error
		if p.check(lexer.LET) || p.check(lexer.CONST) || p.check(lexer.VAR) {
			init, err = p.parseVarDecl(false) // no semicolon
		} else {
			var expr ast.Expression
			expr, err = p.parseExpression()
			if err == nil {
				init = ast.NewExpressionStatement(expr, expr.GetPos())
			}
		}
		if err != nil {
			return nil, err
		}
	}

	if _, err := p.expect(lexer.SEMICOLON); err != nil {
		return nil, err
	}

	// Test (optional)
	var test ast.Expression
	if !p.check(lexer.SEMICOLON) {
		var err error
		test, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}

	if _, err := p.expect(lexer.SEMICOLON); err != nil {
		return nil, err
	}

	// Update (optional) — one or more comma-separated expressions
	// (`i++, j--`), each evaluated purely for side effects every
	// iteration; not the general comma operator (still out of scope
	// everywhere else), just this one well-known idiom's grammar position.
	var update []ast.Expression
	if !p.check(lexer.RPAREN) {
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		update = append(update, expr)
		for p.match(lexer.COMMA) {
			expr, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			update = append(update, expr)
		}
	}

	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}

	body, err := p.parseBlockOrStatement()
	if err != nil {
		return nil, err
	}

	return ast.NewForStatement(init, test, update, body, pos), nil
}

func (p *Parser) parseForOfBody(pos ast.Pos) (*ast.ForOfStatement, error) {
	kindTok := p.advance() // let/const/var
	nameTok, err := p.expect(lexer.IDENT)
	if err != nil {
		return nil, err
	}
	p.advance() // consume 'of'
	iterable, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	body, err := p.parseBlockOrStatement()
	if err != nil {
		return nil, err
	}
	return ast.NewForOfStatement(kindTok.Literal, nameTok.Literal, iterable, body, pos), nil
}

// parseForOfPatternBody parses a for-of whose loop variable is a
// destructuring pattern — `for (const [a, b] of …)` /
// `for (const { x, y } of …)` (TDD-00065 Stage 1). Reuses the same
// pattern-element grammar the statement-declaration position uses, then
// requires `of` (for-in with a pattern is not supported).
func (p *Parser) parseForOfPatternBody(pos ast.Pos) (*ast.ForOfStatement, error) {
	kindTok := p.advance() // let/const/var
	stmt := ast.NewForOfStatement(kindTok.Literal, "", nil, nil, pos)
	if p.check(lexer.LBRACKET) {
		elems, err := p.parseArrayPatternElems()
		if err != nil {
			return nil, err
		}
		stmt.ArrayPattern = elems
	} else {
		props, err := p.parseObjectPatternProps()
		if err != nil {
			return nil, err
		}
		stmt.ObjectPattern = props
	}
	if !(p.check(lexer.IDENT) && p.peek().Literal == "of") {
		tok := p.peek()
		return nil, fmt.Errorf("%d:%d: expected 'of' after a for-of destructuring pattern, got %s", tok.Line, tok.Col, tok.Type)
	}
	p.advance() // consume 'of'
	iterable, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	body, err := p.parseBlockOrStatement()
	if err != nil {
		return nil, err
	}
	stmt.Iterable = iterable
	stmt.Body = body
	return stmt, nil
}

func (p *Parser) parseForInBody(pos ast.Pos) (*ast.ForInStatement, error) {
	kindTok := p.advance() // let/const/var
	nameTok, err := p.expect(lexer.IDENT)
	if err != nil {
		return nil, err
	}
	p.advance() // consume 'in'
	object, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	body, err := p.parseBlockOrStatement()
	if err != nil {
		return nil, err
	}
	return ast.NewForInStatement(kindTok.Literal, nameTok.Literal, object, body, pos), nil
}

func (p *Parser) parseDoWhileStatement() (*ast.DoWhileStatement, error) {
	tok := p.advance() // 'do'
	pos := posOf(tok)
	body, err := p.parseBlockOrStatement()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.WHILE); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	test, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	p.consumeSemicolon()
	return ast.NewDoWhileStatement(body, test, pos), nil
}

func (p *Parser) parseSwitchStatement() (*ast.SwitchStatement, error) {
	tok := p.advance() // 'switch'
	pos := posOf(tok)

	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	disc, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.LBRACE); err != nil {
		return nil, err
	}

	var cases []ast.SwitchCase
	for !p.check(lexer.RBRACE) && !p.check(lexer.EOF) {
		var sc ast.SwitchCase
		switch p.peek().Type {
		case lexer.CASE:
			p.advance()
			test, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			sc.Test = test
			if _, err := p.expect(lexer.COLON); err != nil {
				return nil, err
			}
		case lexer.DEFAULT:
			p.advance()
			if _, err := p.expect(lexer.COLON); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("%d:%d: expected 'case' or 'default' in switch", p.peek().Line, p.peek().Col)
		}
		for !p.check(lexer.CASE) && !p.check(lexer.DEFAULT) &&
			!p.check(lexer.RBRACE) && !p.check(lexer.EOF) {
			stmt, err := p.parseStatement()
			if err != nil {
				return nil, err
			}
			sc.Body = append(sc.Body, stmt)
		}
		cases = append(cases, sc)
	}
	if _, err := p.expect(lexer.RBRACE); err != nil {
		return nil, err
	}
	return ast.NewSwitchStatement(disc, cases, pos), nil
}

func (p *Parser) parseBreakStatement() (*ast.BreakStatement, error) {
	tok := p.advance() // 'break'
	label := ""
	// A label must be on the same line (JS's "no LineTerminator here" rule) —
	// otherwise `break` on its own line followed by an unrelated statement
	// starting with an identifier (e.g. `break\nconsole.log(x)`) would
	// wrongly consume that identifier as a label.
	if p.check(lexer.IDENT) && p.peek().Line == tok.Line {
		label = p.advance().Literal
	}
	p.consumeSemicolon()
	return ast.NewBreakStatement(label, posOf(tok)), nil
}

func (p *Parser) parseContinueStatement() (*ast.ContinueStatement, error) {
	tok := p.advance() // 'continue'
	label := ""
	if p.check(lexer.IDENT) && p.peek().Line == tok.Line {
		label = p.advance().Literal
	}
	p.consumeSemicolon()
	return ast.NewContinueStatement(label, posOf(tok)), nil
}

func (p *Parser) parseThrowStatement() (*ast.ThrowStatement, error) {
	tok := p.advance() // consume 'throw'
	pos := posOf(tok)
	arg, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	p.consumeSemicolon()
	return ast.NewThrowStatement(arg, pos), nil
}

func (p *Parser) parseTryStatement() (*ast.TryStatement, error) {
	tok := p.advance() // consume 'try'
	pos := posOf(tok)
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}

	var catch *ast.CatchClause
	var finally *ast.BlockStatement

	if p.check(lexer.CATCH) {
		catchTok := p.advance() // consume 'catch'
		catchPos := posOf(catchTok)
		var paramName string
		var objPattern []ast.DestructProp
		// Optional catch binding: `catch { ... }` with no `(e)` at all.
		if p.check(lexer.LPAREN) {
			p.advance()
			if p.check(lexer.LBRACE) {
				// Destructured catch binding: `catch ({ message, name }) {
				// ... }` — same object-pattern shape parseObjectDestructuring
				// parses for a `const {..} = ..` declaration, minus the
				// trailing `= init` (the caught value is the implicit source).
				p.advance() // '{'
				for !p.check(lexer.RBRACE) && !p.check(lexer.EOF) {
					keyTok, err := p.expect(lexer.IDENT)
					if err != nil {
						return nil, err
					}
					local := keyTok.Literal
					if p.check(lexer.COLON) {
						p.advance()
						aliasTok, err := p.expect(lexer.IDENT)
						if err != nil {
							return nil, err
						}
						local = aliasTok.Literal
					}
					var dflt ast.Expression
					if p.match(lexer.ASSIGN) {
						dflt, err = p.parseAssignment()
						if err != nil {
							return nil, err
						}
					}
					objPattern = append(objPattern, ast.DestructProp{Key: keyTok.Literal, Local: local, Default: dflt})
					if !p.match(lexer.COMMA) {
						break
					}
				}
				if _, err := p.expect(lexer.RBRACE); err != nil {
					return nil, err
				}
			} else {
				paramTok, err := p.expect(lexer.IDENT)
				if err != nil {
					return nil, err
				}
				paramName = paramTok.Literal
				// Optional type annotation on catch param — skip it.
				if p.check(lexer.COLON) {
					p.advance()
					if _, err := p.parseTypeAnnotation("ts"); err != nil {
						return nil, err
					}
				}
			}
			if _, err := p.expect(lexer.RPAREN); err != nil {
				return nil, err
			}
		}
		cbody, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		catch = &ast.CatchClause{Param: paramName, ObjectPattern: objPattern, Body: cbody, Pos: catchPos}
	}

	if p.check(lexer.FINALLY) {
		p.advance() // consume 'finally'
		finally, err = p.parseBlock()
		if err != nil {
			return nil, err
		}
	}

	if catch == nil && finally == nil {
		return nil, fmt.Errorf("%d:%d: try statement requires at least a catch or finally clause", pos.Line, pos.Col)
	}
	return ast.NewTryStatement(body, catch, finally, pos), nil
}

// parseArrayPatternElems parses an array destructuring pattern's element
// list — the opening `[` through the matching `]`, both consumed — and is
// shared by every position an array pattern can appear: the statement
// declaration (parseArrayDestructuring) and the for-of loop variable
// (parseForOfPatternBody, TDD-00065). Kept in one place so the grammar of
// holes / `= default` / `...rest` can't drift between positions.
func (p *Parser) parseArrayPatternElems() ([]ast.ArrayPatternElem, error) {
	p.advance() // [
	var elems []ast.ArrayPatternElem
	for !p.check(lexer.RBRACKET) && !p.check(lexer.EOF) {
		if p.check(lexer.ELLIPSIS) {
			// Rest element (`[a, ...rest]`, ADR-00161) — always last:
			// break out immediately after, rather than looping for a
			// trailing comma/further elements, so `[...rest, x]` hits a
			// clean "expected ], got ," at the closing bracket instead of
			// silently accepting a real syntax error.
			p.advance() // consume '...'
			nameTok, err := p.expect(lexer.IDENT)
			if err != nil {
				return nil, err
			}
			elems = append(elems, ast.ArrayPatternElem{Name: nameTok.Literal, Rest: true})
			break
		}
		if p.check(lexer.COMMA) {
			p.advance() // hole — consume comma, record skip
			elems = append(elems, ast.ArrayPatternElem{})
		} else if p.check(lexer.LBRACKET) || p.check(lexer.LBRACE) {
			// Nested sub-pattern at this position (`[[a, b], c]` /
			// `[{ x }, { y }]`, TDD-00065 Stage 2) — recurse, then allow
			// an optional `= default` on the whole nested position.
			elem, err := p.parseNestedPatternElem()
			if err != nil {
				return nil, err
			}
			elems = append(elems, elem)
			if !p.match(lexer.COMMA) {
				break
			}
		} else {
			nameTok, err := p.expect(lexer.IDENT)
			if err != nil {
				return nil, err
			}
			var dflt ast.Expression
			if p.match(lexer.ASSIGN) {
				dflt, err = p.parseAssignment()
				if err != nil {
					return nil, err
				}
			}
			elems = append(elems, ast.ArrayPatternElem{Name: nameTok.Literal, Default: dflt})
			if !p.match(lexer.COMMA) {
				break
			}
		}
	}
	if _, err := p.expect(lexer.RBRACKET); err != nil {
		return nil, err
	}
	return elems, nil
}

// parseNestedPatternElem parses a nested array/object sub-pattern sitting at
// one array-pattern position (`[a, b]` or `{ x }` inside a bigger `[...]`),
// plus an optional `= default` on the whole nested position. The caller has
// already confirmed the current token is `[` or `{`.
func (p *Parser) parseNestedPatternElem() (ast.ArrayPatternElem, error) {
	var elem ast.ArrayPatternElem
	if p.check(lexer.LBRACKET) {
		sub, err := p.parseArrayPatternElems()
		if err != nil {
			return elem, err
		}
		elem.SubArray = sub
	} else {
		sub, err := p.parseObjectPatternProps()
		if err != nil {
			return elem, err
		}
		elem.SubObject = sub
	}
	if p.match(lexer.ASSIGN) {
		dflt, err := p.parseAssignment()
		if err != nil {
			return elem, err
		}
		elem.Default = dflt
	}
	return elem, nil
}

// parseObjectPatternProps parses an object destructuring pattern's property
// list — the opening `{` through the matching `}`, both consumed — the
// object-pattern counterpart to parseArrayPatternElems, shared by the same
// two positions.
func (p *Parser) parseObjectPatternProps() ([]ast.DestructProp, error) {
	p.advance() // {
	var props []ast.DestructProp
	for !p.check(lexer.RBRACE) && !p.check(lexer.EOF) {
		// Object rest element `{ ...rest }` (TDD-00065 Stage 3b) — binds every
		// source field not named by an earlier property. Must be the last
		// element (real JS forbids anything after it) and a plain identifier
		// (no rename, default, or nested sub-pattern).
		if p.check(lexer.ELLIPSIS) {
			p.advance() // ...
			restTok, err := p.expect(lexer.IDENT)
			if err != nil {
				return nil, err
			}
			props = append(props, ast.DestructProp{Local: restTok.Literal, Rest: true})
			if !p.check(lexer.RBRACE) {
				return nil, fmt.Errorf("%d:%d: a rest element must be the last property in an object pattern, got %s", p.peek().Line, p.peek().Col, p.peek().Type)
			}
			break
		}
		// PropertyName: IDENT, or a STRING/NUMBER literal used as the key text
		// (`{ "k": v }`, `{ 0: v }`, TDD-00065 Stage 3a) — matching the
		// object-literal key grammar (parseObjectLiteral). Only the identifier
		// form supports shorthand (`{ x }`); a string/number key has no
		// shorthand, so it must bind through an explicit `: local` (or a nested
		// pattern). Codegen keys off DestructProp.Key = keyTok.Literal, the same
		// field name the object-literal side stores, so no codegen change.
		if !p.check(lexer.IDENT) && !p.check(lexer.STRING) && !p.check(lexer.NUMBER) {
			return nil, fmt.Errorf("%d:%d: expected property name, got %s", p.peek().Line, p.peek().Col, p.peek().Type)
		}
		keyTok := p.advance()
		nonIdentKey := keyTok.Type != lexer.IDENT
		local := keyTok.Literal
		var err error
		var subArr []ast.ArrayPatternElem
		var subObj []ast.DestructProp
		if p.check(lexer.COLON) {
			p.advance()
			switch {
			case p.check(lexer.LBRACKET):
				// Nested array sub-pattern: `{ key: [a, b] }` (Stage 2).
				subArr, err = p.parseArrayPatternElems()
				if err != nil {
					return nil, err
				}
			case p.check(lexer.LBRACE):
				// Nested object sub-pattern: `{ key: { a } }` (Stage 2).
				subObj, err = p.parseObjectPatternProps()
				if err != nil {
					return nil, err
				}
			default:
				aliasTok, err := p.expect(lexer.IDENT)
				if err != nil {
					return nil, err
				}
				local = aliasTok.Literal
			}
		} else if nonIdentKey {
			return nil, fmt.Errorf("%d:%d: a string or numeric destructuring key ('%s') must be bound with `: name`, got %s", p.peek().Line, p.peek().Col, keyTok.Literal, p.peek().Type)
		}
		var dflt ast.Expression
		if p.match(lexer.ASSIGN) {
			dflt, err = p.parseAssignment()
			if err != nil {
				return nil, err
			}
		}
		props = append(props, ast.DestructProp{Key: keyTok.Literal, Local: local, Default: dflt, SubArray: subArr, SubObject: subObj})
		if !p.match(lexer.COMMA) {
			break
		}
	}
	if _, err := p.expect(lexer.RBRACE); err != nil {
		return nil, err
	}
	return props, nil
}

func (p *Parser) parseArrayDestructuring() (*ast.ArrayDestructuring, error) {
	tok := p.advance() // let/const/var
	pos := posOf(tok)
	elems, err := p.parseArrayPatternElems()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.ASSIGN); err != nil {
		return nil, err
	}
	init, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	p.consumeSemicolon()
	return ast.NewArrayDestructuring(tok.Literal, elems, init, pos), nil
}

func (p *Parser) parseObjectDestructuring() (*ast.ObjectDestructuring, error) {
	tok := p.advance() // let/const/var
	pos := posOf(tok)
	props, err := p.parseObjectPatternProps()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.ASSIGN); err != nil {
		return nil, err
	}
	init, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	p.consumeSemicolon()
	return ast.NewObjectDestructuring(tok.Literal, props, init, pos), nil
}

func (p *Parser) parseWhileStatement() (*ast.WhileStatement, error) {
	tok := p.advance() // 'while'
	pos := posOf(tok)

	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	test, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	body, err := p.parseBlockOrStatement()
	if err != nil {
		return nil, err
	}
	return ast.NewWhileStatement(test, body, pos), nil
}

func (p *Parser) parseIfStatement() (*ast.IfStatement, error) {
	tok := p.advance() // 'if'
	pos := posOf(tok)

	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	test, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	cons, err := p.parseBlockOrStatement()
	if err != nil {
		return nil, err
	}

	var alt ast.Statement
	if p.check(lexer.ELSE) {
		p.advance()
		if p.check(lexer.IF) {
			alt, err = p.parseIfStatement()
		} else {
			alt, err = p.parseBlockOrStatement()
		}
		if err != nil {
			return nil, err
		}
	}

	return ast.NewIfStatement(test, cons, alt, pos), nil
}

func (p *Parser) parseBlock() (*ast.BlockStatement, error) {
	tok, err := p.expect(lexer.LBRACE)
	if err != nil {
		return nil, err
	}
	pos := posOf(tok)
	var body []ast.Statement
	for !p.check(lexer.RBRACE) && !p.check(lexer.EOF) {
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		body = append(body, stmt)
	}
	if _, err := p.expect(lexer.RBRACE); err != nil {
		return nil, err
	}
	return ast.NewBlockStatement(body, pos), nil
}

// parseBlockOrStatement parses either a braced block or a single statement,
// returning it wrapped in a *BlockStatement either way. This allows
// braceless bodies in if/while/for/do constructs.
func (p *Parser) parseBlockOrStatement() (*ast.BlockStatement, error) {
	if p.check(lexer.LBRACE) {
		return p.parseBlock()
	}
	pos := posOf(p.peek())
	stmt, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	return ast.NewBlockStatement([]ast.Statement{stmt}, pos), nil
}

func (p *Parser) parseExpressionStatement() (*ast.ExpressionStatement, error) {
	expr, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	// Statement-level comma operator (`a(), b();` — ADR-00476): folds into
	// the existing SequenceExpression, evaluating left to right.
	if p.check(lexer.COMMA) {
		exprs := []ast.Expression{expr}
		for p.match(lexer.COMMA) {
			next, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			exprs = append(exprs, next)
		}
		expr = ast.NewSequenceExpression(exprs, expr.GetPos())
	}
	p.consumeSemicolon()
	return ast.NewExpressionStatement(expr, expr.GetPos()), nil
}
