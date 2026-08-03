package parser

import (
	"KlainMainLang/ast"
	"KlainMainLang/lexer"
	"fmt"
)

// parseClassDecl parses `[abstract] class Name [extends Base] [implements
// I, ...] { ... }`. A class body member may be prefixed by any of
// static/private/protected/public/abstract (any order, TDD-00009 Stage 4),
// then is either a method/constructor (`name(...) { ... }` or, for an
// abstract method, `name(...): T;` with no body), a `static { ... }`
// initializer block, or a typed field (`name: type;`).
func (p *Parser) parseClassDecl(isAbstract bool) (*ast.ClassDeclaration, error) {
	tok := p.advance() // consume 'class'
	pos := posOf(tok)
	nameTok, err := p.expect(lexer.IDENT)
	if err != nil {
		return nil, err
	}
	var baseClass string
	var baseTypeArgs []*ast.TypeAnnotation
	if p.check(lexer.EXTENDS) {
		p.advance() // extends
		baseTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}
		baseClass = baseTok.Literal
		// Generic extends (TDD-00023): `extends EventEmitter<T>` is
		// currently the only base that accepts a type argument — but the
		// parse itself is generic (any `extends <Ident><T>` shape), with
		// the "is this base actually generic" check deferred to
		// registerClasses, matching the existing "extends unknown class"
		// precedent of validating at registration time, not parse time.
		if p.check(lexer.LT) {
			p.advance() // consume '<'
			arg, err := p.parseTypeAnnotation("ts")
			if err != nil {
				return nil, err
			}
			baseTypeArgs = append(baseTypeArgs, arg)
			if err := p.expectGT(baseClass + "<T>"); err != nil {
				return nil, err
			}
		}
	}
	var implementsNames []string
	if p.check(lexer.IMPLEMENTS) {
		p.advance() // implements
		for {
			ifaceTok, err := p.expect(lexer.IDENT)
			if err != nil {
				return nil, err
			}
			implementsNames = append(implementsNames, ifaceTok.Literal)
			if !p.match(lexer.COMMA) {
				break
			}
		}
	}
	if _, err := p.expect(lexer.LBRACE); err != nil {
		return nil, err
	}

	var fields []ast.AnnotField
	var ctor *ast.FunctionDeclaration
	var methods []*ast.FunctionDeclaration
	var staticBlocks []*ast.BlockStatement
	for !p.check(lexer.RBRACE) && !p.check(lexer.EOF) {
		doc := p.takeDoc()

		// `static { ... }` initializer block — distinguished from a
		// `static`-modified member by checking for `{` immediately after
		// `static`, before any member name has been consumed.
		if p.check(lexer.STATIC) && p.peekNth(1).Type == lexer.LBRACE {
			p.advance() // static
			block, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			staticBlocks = append(staticBlocks, block)
			continue
		}

		// Zero or more modifiers, any order.
		var isStatic, isMemberAbstract bool
		var visibility string
		for {
			switch p.peek().Type {
			case lexer.STATIC:
				isStatic = true
				p.advance()
				continue
			case lexer.ABSTRACT:
				isMemberAbstract = true
				p.advance()
				continue
			case lexer.PRIVATE:
				visibility = "private"
				p.advance()
				continue
			case lexer.PROTECTED:
				visibility = "protected"
				p.advance()
				continue
			case lexer.PUBLIC:
				visibility = ""
				p.advance()
				continue
			}
			break
		}

		memberTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}

		// Method or constructor: `name(...) { ... }` (or, if isMemberAbstract,
		// `name(...): T;` with no body).
		if p.check(lexer.LPAREN) {
			fn, err := p.parseFunctionRest(memberTok.Literal, false, isMemberAbstract)
			if err != nil {
				return nil, err
			}
			fn.IsStatic = isStatic
			fn.Visibility = visibility
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
		fields = append(fields, ast.AnnotField{Name: memberTok.Literal, Type: ft, Static: isStatic, Visibility: visibility})
		p.match(lexer.SEMICOLON, lexer.COMMA)
	}
	if _, err := p.expect(lexer.RBRACE); err != nil {
		return nil, err
	}
	return ast.NewClassDeclaration(nameTok.Literal, baseClass, baseTypeArgs, isAbstract, implementsNames, fields, ctor, methods, staticBlocks, pos), nil
}
