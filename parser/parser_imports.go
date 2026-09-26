package parser

import (
	"KlainMainLang/ast"
	"KlainMainLang/diag"
	"KlainMainLang/lexer"
)

// parseImportDeclaration parses `import { a, b as c } from './path'`, a
// default import (`import Foo from './path'`, optionally combined with a
// named list: `import Foo, { a, b } from './path'`), or a namespace import
// (`import * as ns from './path'`, TDD-00042). No bare/package-style
// specifiers (only relative paths make sense here; there's no package
// ecosystem). `import Foo, * as ns from ...` (combining default +
// namespace) is deliberately not supported — see TDD-00042. `from`/`as`
// are contextual (real TS/JS treats them as ordinary identifiers
// everywhere else), matched as plain IDENT tokens with a literal-string
// check, the same way `interface`/`type`/`enum` already are elsewhere in
// this parser — not reserved lexer keywords.
func (p *Parser) parseImportDeclaration() (*ast.ImportDeclaration, error) {
	tok := p.advance() // 'import'
	pos := posOf(tok)

	var specs []ast.ImportSpecifier
	var namespace string
	// `import type { A }`, `import type * as ns`, `import type D from`; but
	// `import type from './x'` is a default import named `type`.
	typeOnly := p.isWord(0, "type") && (p.peekNth(1).Type == lexer.LBRACE || p.peekNth(1).Type == lexer.STAR ||
		(p.peekNth(1).Type == lexer.IDENT && !(p.peekNth(1).Literal == "from" && p.peekNth(2).Type == lexer.STRING)))
	if typeOnly {
		p.advance() // 'type'
	}

	switch {
	case p.check(lexer.STAR):
		p.advance() // '*'
		if !(p.peek().Type == lexer.IDENT && p.peek().Literal == "as") {
			return nil, p.errAt(p.peek(), diag.ExpectedAsNamespace, p.peek().Type)
		}
		p.advance() // 'as'
		nsTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}
		namespace = nsTok.Literal
	case p.check(lexer.IDENT):
		nameTok := p.advance() // default binding name
		specs = append(specs, ast.ImportSpecifier{Imported: "default", Local: nameTok.Literal})
		if p.check(lexer.COMMA) {
			p.advance()
			named, err := p.parseImportSpecifierList()
			if err != nil {
				return nil, err
			}
			specs = append(specs, named...)
		}
	default:
		named, err := p.parseImportSpecifierList()
		if err != nil {
			return nil, err
		}
		specs = named
	}

	if !(p.peek().Type == lexer.IDENT && p.peek().Literal == "from") {
		return nil, p.errAt(p.peek(), diag.ExpectedFromImport, p.peek().Type)
	}
	p.advance() // 'from'

	srcTok, err := p.expect(lexer.STRING)
	if err != nil {
		return nil, err
	}
	if err := p.parseSemicolon(); err != nil {
		return nil, err
	}
	decl := ast.NewImportDeclaration(specs, namespace, srcTok.Literal, pos)
	if typeOnly {
		decl.TypeOnly = true
		for i := range decl.Specifiers {
			decl.Specifiers[i].TypeOnly = true
		}
	}
	return decl, nil
}

// isTypeModifier reports whether the `type` at the cursor is a specifier's
// type-only modifier (`{ type A }`, `{ type A as B }`) rather than a name
// (`{ type }`, `{ type as B }`).
func (p *Parser) isTypeModifier() bool {
	if !p.isWord(0, "type") {
		return false
	}
	next := p.peekNth(1)
	if next.Type == lexer.DEFAULT {
		return true
	}
	if next.Type != lexer.IDENT {
		return false
	}
	// `{ type as B }` imports `type` as B; `{ type as as B }` and
	// `{ type as }` are the modifier on a name `as`.
	if next.Literal == "as" {
		after := p.peekNth(2)
		return after.Type != lexer.IDENT || after.Literal == "as"
	}
	return true
}

// parseImportSpecifierList parses the `{ a, b as c }` named-specifier list.
func (p *Parser) parseImportSpecifierList() ([]ast.ImportSpecifier, error) {
	if _, err := p.expect(lexer.LBRACE); err != nil {
		return nil, err
	}
	var specs []ast.ImportSpecifier
	for !p.check(lexer.RBRACE) {
		typeOnly := p.isTypeModifier()
		if typeOnly {
			p.advance() // 'type'
		}
		nameTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}
		spec := ast.ImportSpecifier{Imported: nameTok.Literal, Local: nameTok.Literal, TypeOnly: typeOnly}
		if p.peek().Type == lexer.IDENT && p.peek().Literal == "as" {
			p.advance() // 'as'
			aliasTok, err := p.expect(lexer.IDENT)
			if err != nil {
				return nil, err
			}
			spec.Local = aliasTok.Literal
		}
		specs = append(specs, spec)
		if p.check(lexer.COMMA) {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expect(lexer.RBRACE); err != nil {
		return nil, err
	}
	return specs, nil
}

// parseExportDeclaration parses `export <declaration>` — a function, var/
// let/const, interface, type alias, enum, or class declaration —
// `export default <target>` (TDD-00042), or a re-export
// (`export { a, b as c } from './path'` / `export * from './path'`,
// TDD-00051), or a local export list (`export { a, b as c }`).
func (p *Parser) parseExportDeclaration() (ast.Statement, error) {
	tok := p.advance() // 'export'
	pos := posOf(tok)

	if p.check(lexer.LBRACE) || p.check(lexer.STAR) {
		return p.parseExportFromDeclaration(pos)
	}
	// `export type { A }` / `export type { A } from './x'` / `export type *
	// from './x'`: a type-only list (`export type A = …` is an alias).
	if p.isWord(0, "type") && (p.peekNth(1).Type == lexer.LBRACE || p.peekNth(1).Type == lexer.STAR) {
		p.advance() // 'type'
		decl, err := p.parseExportFromDeclaration(pos)
		if err != nil {
			return nil, err
		}
		for i := range decl.Specifiers {
			decl.Specifiers[i].TypeOnly = true
		}
		return decl, nil
	}

	if p.check(lexer.DEFAULT) {
		p.advance() // 'default'
		decl, err := p.parseDefaultExportTarget()
		if err != nil {
			return nil, err
		}
		return ast.NewExportDeclaration(decl, true, pos), nil
	}

	// `export declare …`: ambient — parse it directly (the ambient parser
	// may return a namespace's desugared declarations, which must not be
	// re-wrapped) with exportness dropped, as for namespaces below (ADR-00474).
	if p.check(lexer.IDENT) && p.peek().Literal == "declare" {
		return p.parseAmbientDeclaration()
	}

	// `export namespace X {}` / `export module X {}`: exportness is
	// meaningless in the single-file namespace scope (TDD-00148 Stage 1) —
	// parse as the plain declaration rather than rejecting.
	if p.check(lexer.IDENT) && (p.peek().Literal == "namespace" || p.peek().Literal == "module") &&
		p.peekNth(1).Type == lexer.IDENT {
		return p.parseNamespaceDecl(false)
	}

	decl, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	switch decl.(type) {
	case *ast.FunctionDeclaration, *ast.VarDeclaration, *ast.InterfaceDeclaration,
		*ast.TypeAliasDeclaration, *ast.EnumDeclaration, *ast.ClassDeclaration:
		return ast.NewExportDeclaration(decl, false, pos), nil
	case *ast.BlockStatement:
		// The erased forms — `export declare …` (ambient) and an exported
		// namespace's own empty-desugar — arrive as the empty block their
		// parsers return; pass it through unwrapped (ADR-00468).
		return decl, nil
	default:
		return nil, errAtPos(pos, diag.ExportNotDeclaration)
	}
}

// parseExportFromDeclaration parses the two re-export forms (TDD-00051):
// `export { a, b as c } from './path'` and `export * from './path'`. pos is
// the position of the already-consumed 'export' token.
func (p *Parser) parseExportFromDeclaration(pos ast.Pos) (*ast.ExportFromDeclaration, error) {
	var specs []ast.ImportSpecifier
	all := false

	if p.check(lexer.STAR) {
		p.advance() // '*'
		if p.peek().Type == lexer.IDENT && p.peek().Literal == "as" {
			return nil, p.errAt(p.peek(), diag.NamespaceReexport)
		}
		all = true
	} else {
		var err error
		specs, err = p.parseExportFromSpecifierList()
		if err != nil {
			return nil, err
		}
	}

	if !(p.peek().Type == lexer.IDENT && p.peek().Literal == "from") {
		if all {
			return nil, p.errAt(p.peek(), diag.ExpectedFromExport, p.peek().Type)
		}
		// `export { a, b as c }`: an export list of this file's own
		// declarations (no Source).
		if err := p.parseSemicolon(); err != nil {
			return nil, err
		}
		return ast.NewExportFromDeclaration(specs, false, "", pos), nil
	}
	p.advance() // 'from'

	srcTok, err := p.expect(lexer.STRING)
	if err != nil {
		return nil, err
	}
	if err := p.parseSemicolon(); err != nil {
		return nil, err
	}
	return ast.NewExportFromDeclaration(specs, all, srcTok.Literal, pos), nil
}

// parseExportFromSpecifierList parses the `{ a, b as c }` list of a
// re-export. Unlike parseImportSpecifierList, either side of an entry may
// be the literal `default` (lexer.DEFAULT, a reserved keyword, not an
// IDENT) — `export { default } from './x'` and `export { foo as default }
// from './x'` are both real, meaningful re-export forms (re-exporting
// another module's default, and re-exporting a named export as this
// module's own default, respectively).
func (p *Parser) parseExportFromSpecifierList() ([]ast.ImportSpecifier, error) {
	if _, err := p.expect(lexer.LBRACE); err != nil {
		return nil, err
	}
	var specs []ast.ImportSpecifier
	for !p.check(lexer.RBRACE) {
		typeOnly := p.isTypeModifier()
		if typeOnly {
			p.advance() // 'type'
		}
		name, err := p.expectIdentOrDefault()
		if err != nil {
			return nil, err
		}
		spec := ast.ImportSpecifier{Imported: name, Local: name, TypeOnly: typeOnly}
		if p.peek().Type == lexer.IDENT && p.peek().Literal == "as" {
			p.advance() // 'as'
			alias, err := p.expectIdentOrDefault()
			if err != nil {
				return nil, err
			}
			spec.Local = alias
		}
		specs = append(specs, spec)
		if p.check(lexer.COMMA) {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expect(lexer.RBRACE); err != nil {
		return nil, err
	}
	return specs, nil
}

// expectIdentOrDefault consumes and returns the literal of either an IDENT
// or the reserved `default` keyword token — see parseExportFromSpecifierList.
func (p *Parser) expectIdentOrDefault() (string, error) {
	if p.check(lexer.DEFAULT) {
		return p.advance().Literal, nil
	}
	tok, err := p.expect(lexer.IDENT)
	if err != nil {
		return "", err
	}
	return tok.Literal, nil
}

// parseDefaultExportTarget parses whatever follows `export default`: a
// named or anonymous function/class declaration (anonymous gets the
// synthetic name "default" — see parseFunctionDecl/parseClassDecl's own
// defaultName parameter and TDD-00042), a named interface/type
// alias/enum declaration, or an arbitrary expression — wrapped as
// `const default = <expr>` (a plain ast.VarDeclaration) so it reuses the
// exact same "default" mangling/export-aliasing path as every other case
// here, with no separate resolver handling needed for the expression form.
func (p *Parser) parseDefaultExportTarget() (ast.Statement, error) {
	switch p.peek().Type {
	case lexer.AT:
		// `export default @dec class …` (TDD-00161) — decorators after
		// `default`, before the class.
		decs, err := p.parseDecorators()
		if err != nil {
			return nil, err
		}
		pos := posOf(p.peek())
		stmt, err := p.parseDefaultExportTarget()
		if err != nil {
			return nil, err
		}
		cd := unwrapClassDecl(stmt)
		if cd == nil {
			return nil, errAtPos(pos, diag.DecoratorsNotValid)
		}
		cd.Decorators = decs
		return stmt, nil
	case lexer.FUNCTION:
		return p.parseFunctionDecl(false, "default")
	case lexer.CLASS:
		return p.parseClassDecl(false, "default")
	}
	if p.isWord(0, "abstract") && p.peekNth(1).Type == lexer.CLASS && p.sameLine(1) {
		p.advance() // 'abstract'
		return p.parseClassDecl(true, "default")
	}
	if p.isWord(0, "async") && p.peekNth(1).Type == lexer.FUNCTION && p.sameLine(1) {
		p.advance() // 'async'
		return p.parseFunctionDecl(true, "default")
	}
	if p.peek().Type == lexer.IDENT {
		switch p.peek().Literal {
		case "interface":
			return p.parseInterfaceDecl()
		case "type":
			return p.parseTypeAliasDecl()
		case "enum":
			return p.parseEnumDeclaration()
		}
	}
	pos := posOf(p.peek())
	expr, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if err := p.parseSemicolon(); err != nil {
		return nil, err
	}
	return ast.NewVarDeclaration("const", "default", nil, expr, pos), nil
}

// parseImportExpr parses `import` reached in expression position
// (TDD-00055) — currently only `import.meta.url` (Stage 1); dynamic
// `import(...)` (Stage 2) isn't implemented yet and gets its own clear,
// dedicated rejection rather than falling through to a generic parse
// error, so it's obvious the syntax was recognized but isn't supported yet.
// Reached via parsePrimary's `case lexer.IMPORT`, the same dedicated-parser
// pattern `case lexer.NEW: return p.parseNew()` already uses for a keyword
// with its own argument-shape rules rather than the generic call-postfix
// machinery.
func (p *Parser) parseImportExpr() (ast.Expression, error) {
	tok := p.advance() // 'import'
	pos := posOf(tok)

	switch {
	case p.check(lexer.DOT):
		p.advance() // '.'
		if !(p.peek().Type == lexer.IDENT && p.peek().Literal == "meta") {
			return nil, p.errAt(p.peek(), diag.ExpectedImportMeta)
		}
		p.advance() // 'meta'
		if !p.check(lexer.DOT) || !(p.peekNth(1).Type == lexer.IDENT && p.peekNth(1).Literal == "url") {
			return nil, p.errAt(p.peek(), diag.ImportMetaOnlyURL)
		}
		p.advance() // '.'
		p.advance() // 'url'
		return ast.NewImportMetaUrl(pos), nil
	case p.check(lexer.LPAREN):
		// Dynamic import(specifier) (TDD-00055/TDD-00056). The specifier stays a
		// general expression here; the resolver enforces string-literal-only and
		// treats it as a dependency edge.
		p.advance() // '('
		spec, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.RPAREN); err != nil {
			return nil, err
		}
		return ast.NewImportCallExpression(spec, pos), nil
	default:
		return nil, p.errAt(p.peek(), diag.ExpectedImportDotOr, p.peek().Type)
	}
}

// parseImportEquals parses a TS import-equals alias declaration
// (`import X = Y.Z;`, ADR-00456) — positioned at the `import` keyword.
// scope is the dotted name of the declaring namespace ("" at top level);
// exported marks the namespace-scoped `export import` form. The alias is
// recorded for codegen to resolve once every namespace is known; the
// statement itself desugars to nothing. The CommonJS form
// (`import x = require('...')`) is a clean rejection.
func (p *Parser) parseImportEquals(scope string, exported bool) (ast.Statement, error) {
	tok := p.advance() // 'import'
	nameTok, err := p.expect(lexer.IDENT)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.ASSIGN); err != nil {
		return nil, err
	}
	if p.check(lexer.IDENT) && p.peek().Literal == "require" && p.peekNth(1).Type == lexer.LPAREN {
		return nil, p.errAt(tok, diag.ImportRequireAssignment, nameTok.Literal)
	}
	target, err := p.parseNamespaceName()
	if err != nil {
		return nil, err
	}
	if err := p.parseSemicolon(); err != nil {
		return nil, err
	}
	p.nsAliases = append(p.nsAliases, ast.NSAliasDecl{Scope: scope, Name: nameTok.Literal, Target: target, Exported: exported})
	return ast.NewBlockStatement(nil, posOf(tok)), nil
}
