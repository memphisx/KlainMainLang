package parser

import (
	"KlainMainLang/ast"
	"KlainMainLang/lexer"
	"fmt"
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

	switch {
	case p.check(lexer.STAR):
		p.advance() // '*'
		if !(p.peek().Type == lexer.IDENT && p.peek().Literal == "as") {
			return nil, fmt.Errorf("%d:%d: expected 'as' after '*' in namespace import, got %s", p.peek().Line, p.peek().Col, p.peek().Type)
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
		return nil, fmt.Errorf("%d:%d: expected 'from' after import specifier list, got %s", p.peek().Line, p.peek().Col, p.peek().Type)
	}
	p.advance() // 'from'

	srcTok, err := p.expect(lexer.STRING)
	if err != nil {
		return nil, err
	}
	if p.check(lexer.SEMICOLON) {
		p.advance()
	}
	return ast.NewImportDeclaration(specs, namespace, srcTok.Literal, pos), nil
}

// parseImportSpecifierList parses the `{ a, b as c }` named-specifier list.
func (p *Parser) parseImportSpecifierList() ([]ast.ImportSpecifier, error) {
	if _, err := p.expect(lexer.LBRACE); err != nil {
		return nil, err
	}
	var specs []ast.ImportSpecifier
	for !p.check(lexer.RBRACE) {
		nameTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}
		spec := ast.ImportSpecifier{Imported: nameTok.Literal, Local: nameTok.Literal}
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
// let/const, interface, type alias, enum, or class declaration — or
// `export default <target>` (TDD-00042). `export { ... }` (re-export
// lists) is not supported yet (left for a future TDD, see TDD-00042's
// Context section).
func (p *Parser) parseExportDeclaration() (*ast.ExportDeclaration, error) {
	tok := p.advance() // 'export'
	pos := posOf(tok)

	if p.check(lexer.DEFAULT) {
		p.advance() // 'default'
		decl, err := p.parseDefaultExportTarget()
		if err != nil {
			return nil, err
		}
		return ast.NewExportDeclaration(decl, true, pos), nil
	}

	decl, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	switch decl.(type) {
	case *ast.FunctionDeclaration, *ast.VarDeclaration, *ast.InterfaceDeclaration,
		*ast.TypeAliasDeclaration, *ast.EnumDeclaration, *ast.ClassDeclaration:
		return ast.NewExportDeclaration(decl, false, pos), nil
	default:
		return nil, fmt.Errorf("%d:%d: 'export' can only precede a function, variable, interface, type alias, enum, or class declaration", pos.Line, pos.Col)
	}
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
	case lexer.FUNCTION:
		return p.parseFunctionDecl(false, "default")
	case lexer.CLASS:
		return p.parseClassDecl(false, "default")
	case lexer.ABSTRACT:
		if p.peekNth(1).Type == lexer.CLASS {
			p.advance() // 'abstract'
			return p.parseClassDecl(true, "default")
		}
	case lexer.ASYNC:
		if p.peekNth(1).Type == lexer.FUNCTION {
			p.advance() // 'async'
			return p.parseFunctionDecl(true, "default")
		}
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
	if p.check(lexer.SEMICOLON) {
		p.advance()
	}
	return ast.NewVarDeclaration("const", "default", nil, expr, pos), nil
}
