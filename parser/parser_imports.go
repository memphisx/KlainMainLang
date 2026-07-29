package parser

import (
	"KlainMainLang/ast"
	"KlainMainLang/lexer"
	"fmt"
)

// parseImportDeclaration parses `import { a, b as c } from './path'`.
// Only the named-import form is supported (V1 scope) — no default import
// (`import x from ...`), no namespace import (`import * as ns from ...`),
// and no bare/package-style specifiers (only relative paths make sense
// here; there's no package ecosystem). `from`/`as` are contextual (real
// TS/JS treats them as ordinary identifiers everywhere else), matched as
// plain IDENT tokens with a literal-string check, the same way `interface`/
// `type`/`enum` already are elsewhere in this parser — not reserved
// lexer keywords.
func (p *Parser) parseImportDeclaration() (*ast.ImportDeclaration, error) {
	tok := p.advance() // 'import'
	pos := posOf(tok)

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
	return ast.NewImportDeclaration(specs, srcTok.Literal, pos), nil
}

// parseExportDeclaration parses `export <declaration>` — a function, var/
// let/const, interface, type alias, or enum declaration. `export default`
// and `export { ... }` (re-export lists) are not supported yet (V1 scope).
func (p *Parser) parseExportDeclaration() (*ast.ExportDeclaration, error) {
	tok := p.advance() // 'export'
	pos := posOf(tok)

	decl, err := p.parseStatement()
	if err != nil {
		return nil, err
	}
	switch decl.(type) {
	case *ast.FunctionDeclaration, *ast.VarDeclaration, *ast.InterfaceDeclaration,
		*ast.TypeAliasDeclaration, *ast.EnumDeclaration, *ast.ClassDeclaration:
		return ast.NewExportDeclaration(decl, pos), nil
	default:
		return nil, fmt.Errorf("%d:%d: 'export' can only precede a function, variable, interface, type alias, enum, or class declaration", pos.Line, pos.Col)
	}
}
