package parser

import (
	"KlainMainLang/ast"
	"KlainMainLang/lexer"
	"fmt"
)

// parseClassDecl parses `class Name { field: type; ...; constructor(...) {...} method(...) {...} }`.
// Stage 0 of TDD-00009: no `extends`/`super` and no modifier keywords
// (private/protected/static/implements/abstract) — all deferred to later
// stages, so any of those produce a plain parse error here rather than being
// silently accepted and ignored.
func (p *Parser) parseClassDecl() (*ast.ClassDeclaration, error) {
	tok := p.advance() // consume 'class'
	pos := posOf(tok)
	nameTok, err := p.expect(lexer.IDENT)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.LBRACE); err != nil {
		return nil, err
	}

	var fields []ast.AnnotField
	var ctor *ast.FunctionDeclaration
	var methods []*ast.FunctionDeclaration
	for !p.check(lexer.RBRACE) && !p.check(lexer.EOF) {
		doc := p.takeDoc()
		memberTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}

		// Method or constructor: `name(...) { ... }`.
		if p.check(lexer.LPAREN) {
			fn, err := p.parseFunctionRest(memberTok.Literal, false)
			if err != nil {
				return nil, err
			}
			if memberTok.Literal == "constructor" {
				if ctor != nil {
					return nil, fmt.Errorf("%d:%d: class '%s' declares more than one constructor", memberTok.Line, memberTok.Col, nameTok.Literal)
				}
				ctor = fn
			} else {
				methods = append(methods, fn)
			}
			continue
		}

		// Otherwise a typed field: `name: type;` (no initializer — see
		// ast.ClassDeclaration's doc comment).
		p.match(lexer.QUESTION)
		if _, err := p.expect(lexer.COLON); err != nil {
			return nil, err
		}
		ft, err := p.parseTypeAnnotation("ts")
		if err != nil {
			return nil, err
		}
		if doc != nil {
			if t := doc.GetType(); t != "" {
				ft = &ast.TypeAnnotation{Name: t, Source: "jsdoc"}
			}
		}
		fields = append(fields, ast.AnnotField{Name: memberTok.Literal, Type: ft})
		p.match(lexer.SEMICOLON, lexer.COMMA)
	}
	if _, err := p.expect(lexer.RBRACE); err != nil {
		return nil, err
	}
	return ast.NewClassDeclaration(nameTok.Literal, fields, ctor, methods, pos), nil
}
