package parser

import (
	"KlainMainLang/ast"
	"KlainMainLang/lexer"
	"fmt"
)

// parseTrailingArrayBrackets consumes zero or more trailing `[]` after a
// parenthesized function type or an object type ({...}[]), wrapping ta in a
// nested array TypeAnnotation for each pair found.
func parseTrailingArrayBrackets(p *Parser, source string, ta *ast.TypeAnnotation) (*ast.TypeAnnotation, error) {
	for p.check(lexer.LBRACKET) {
		p.advance()
		if _, err := p.expect(lexer.RBRACKET); err != nil {
			return nil, fmt.Errorf("expected ] in array type annotation")
		}
		ta = &ast.TypeAnnotation{Source: source, ElemType: ta}
	}
	return ta, nil
}

func (p *Parser) parseTypeAnnotation(source string) (*ast.TypeAnnotation, error) {
	tok := p.peek()

	// Function type annotation: (param: type, ...) => retType
	if tok.Type == lexer.LPAREN {
		p.advance() // consume '('
		var funcParams []ast.TypeAnnotation
		singleUnnamed := true
		for !p.check(lexer.RPAREN) && !p.check(lexer.EOF) {
			// Optional param name (for documentation only)
			if p.check(lexer.IDENT) && p.peekNth(1).Type == lexer.COLON {
				p.advance() // name
				p.advance() // colon
				singleUnnamed = false
			}
			pt, err := p.parseTypeAnnotation(source)
			if err != nil {
				return nil, err
			}
			funcParams = append(funcParams, *pt)
			p.match(lexer.COMMA)
		}
		if _, err := p.expect(lexer.RPAREN); err != nil {
			return nil, err
		}
		// `(SomeFuncType)` used purely to group/disambiguate a function type
		// (e.g. as a return-type annotation: `(): (() => void) => { ... }`)
		// parses identically up to here as a real one-parameter curried
		// function type `(SomeFuncType) => retType` — the two are only
		// distinguishable by whether an actual type follows the '=>', since
		// a real curried return type can never be a statement block. Try the
		// curried-function-type reading first; if it doesn't pan out and
		// there was exactly one unnamed parameter, treat the parens as pure
		// grouping instead, backtracking to just before the '=>' so it's
		// left for whatever follows (e.g. an enclosing arrow function's own
		// body arrow) to consume.
		if p.check(lexer.ARROW) {
			beforeArrow := p.pos
			p.advance() // consume '=>' tentatively
			retType, err := p.parseTypeAnnotation(source)
			if err == nil {
				ta := &ast.TypeAnnotation{Source: source, IsFuncType: true, FuncParams: funcParams, FuncRetType: retType}
				return parseTrailingArrayBrackets(p, source, ta)
			}
			if len(funcParams) == 1 && singleUnnamed {
				p.pos = beforeArrow
				return &funcParams[0], nil
			}
			return nil, err
		}
		if len(funcParams) == 1 && singleUnnamed {
			return parseTrailingArrayBrackets(p, source, &funcParams[0])
		}
		return nil, fmt.Errorf("%d:%d: expected =>, got %s", p.peek().Line, p.peek().Col, p.peek().Type)
	}

	// Object type annotation: { field: type; field: type }
	if tok.Type == lexer.LBRACE {
		p.advance() // consume '{'
		var fields []ast.AnnotField
		for !p.check(lexer.RBRACE) && !p.check(lexer.EOF) {
			nameTok, err := p.expect(lexer.IDENT)
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.COLON); err != nil {
				return nil, err
			}
			fieldType, err := p.parseTypeAnnotation(source)
			if err != nil {
				return nil, err
			}
			fields = append(fields, ast.AnnotField{Name: nameTok.Literal, Type: fieldType})
			p.match(lexer.SEMICOLON, lexer.COMMA)
		}
		if _, err := p.expect(lexer.RBRACE); err != nil {
			return nil, err
		}
		ta := &ast.TypeAnnotation{Source: source, Fields: fields}
		return parseTrailingArrayBrackets(p, source, ta)
	}

	// Accept identifier OR keyword-as-type (void, null, undefined, …)
	isTypeName := tok.Type == lexer.IDENT ||
		tok.Type == lexer.VOID ||
		tok.Type == lexer.NULL ||
		tok.Type == lexer.UNDEFINED ||
		tok.Type == lexer.TRUE ||
		tok.Type == lexer.FALSE
	if !isTypeName {
		return nil, fmt.Errorf("%d:%d: expected type name, got %s", tok.Line, tok.Col, tok.Type)
	}
	nameTok := p.advance()
	name := nameTok.Literal

	// Promise<T> / Array<T> / Set<T> / EventEmitter<T>: single type
	// parameter — parse it for real instead of skipping, same as the
	// T[] / new Array<T>() forms.
	if (name == "Promise" || name == "Array" || name == "Set" || name == "EventEmitter") && p.check(lexer.LT) {
		p.advance() // consume '<'
		inner, err := p.parseTypeAnnotation(source)
		if err != nil {
			return nil, err
		}
		if err := p.expectGT(name + "<T>"); err != nil {
			return nil, err
		}
		return &ast.TypeAnnotation{Name: name, ElemType: inner, Source: source}, nil
	}

	// Map<K,V>: two type parameters.
	if name == "Map" && p.check(lexer.LT) {
		p.advance() // consume '<'
		keyTy, err := p.parseTypeAnnotation(source)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.COMMA); err != nil {
			return nil, fmt.Errorf("expected ',' in Map<K,V>")
		}
		valTy, err := p.parseTypeAnnotation(source)
		if err != nil {
			return nil, err
		}
		if err := p.expectGT("Map<K,V>"); err != nil {
			return nil, err
		}
		return &ast.TypeAnnotation{Name: "Map", KeyType: keyTy, ElemType: valTy, Source: source}, nil
	}

	// Skip any other/unrecognized generic (a genuinely unsupported one —
	// user-defined generics aren't implemented yet, see docs/tdd/TDD-00010.md).
	if p.check(lexer.LT) {
		depth := 0
		for !p.check(lexer.EOF) {
			if p.check(lexer.LT) {
				depth++
			} else if p.check(lexer.GT) {
				depth--
			}
			p.advance()
			if depth == 0 {
				break
			}
		}
	}

	// Array suffix: T[]  (may repeat for multi-dimensional, future use)
	for p.check(lexer.LBRACKET) {
		p.advance() // consume [
		if _, err := p.expect(lexer.RBRACKET); err != nil {
			return nil, fmt.Errorf("expected ] in array type annotation")
		}
		name += "[]"
	}

	ta := &ast.TypeAnnotation{Name: name, Source: source}

	// Union type: T | null / T | undefined — consume the null/undefined side and mark Nullable.
	for p.check(lexer.BITOR) {
		p.advance() // consume '|'
		right, err := p.parseTypeAnnotation(source)
		if err != nil {
			return nil, err
		}
		if right.Name == "null" || right.Name == "undefined" {
			ta.Nullable = true
		} else if ta.Name == "null" || ta.Name == "undefined" {
			right.Nullable = true
			ta = right
		}
		// For other union members we silently accept the syntax but use the first type.
	}

	return ta, nil
}

func (p *Parser) parseInterfaceDecl() (*ast.InterfaceDeclaration, error) {
	tok := p.advance() // consume 'interface'
	pos := posOf(tok)
	nameTok, err := p.expect(lexer.IDENT)
	if err != nil {
		return nil, err
	}
	// Skip optional `extends Base` clause.
	if p.peek().Type == lexer.EXTENDS {
		p.advance() // extends
		p.advance() // base name
	}
	if _, err := p.expect(lexer.LBRACE); err != nil {
		return nil, err
	}
	var fields []ast.AnnotField
	var methods []ast.InterfaceMethodSig
	for !p.check(lexer.RBRACE) && !p.check(lexer.EOF) {
		doc := p.takeDoc()
		fieldTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}

		// Method signature (TDD-00009 Stage 4, for `implements` conformance
		// checking): `name(...): T;` — no body, ever.
		if p.check(lexer.LPAREN) {
			p.advance()
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
			methods = append(methods, ast.InterfaceMethodSig{Name: fieldTok.Literal, Params: params, ReturnType: retType})
			p.match(lexer.SEMICOLON, lexer.COMMA)
			continue
		}

		// Optional marker (name?: type) — accepted but treated as required for codegen.
		p.match(lexer.QUESTION)
		if _, err := p.expect(lexer.COLON); err != nil {
			return nil, err
		}
		ft, err := p.parseTypeAnnotation("ts")
		if err != nil {
			return nil, err
		}
		// JSDoc overrides the TS annotation — same convention parseVarDecl
		// already uses for variable declarations, e.g. a field declared only
		// `number` can be pinned to `float64`/`int32`/etc. via a preceding
		// `/** @type {float64} */` comment.
		if doc != nil {
			if t := doc.GetType(); t != "" {
				ft = &ast.TypeAnnotation{Name: t, Source: "jsdoc"}
			}
		}
		fields = append(fields, ast.AnnotField{Name: fieldTok.Literal, Type: ft})
		p.match(lexer.SEMICOLON, lexer.COMMA)
	}
	if _, err := p.expect(lexer.RBRACE); err != nil {
		return nil, err
	}
	return ast.NewInterfaceDeclaration(nameTok.Literal, fields, methods, pos), nil
}

func (p *Parser) parseTypeAliasDecl() (*ast.TypeAliasDeclaration, error) {
	tok := p.advance() // consume 'type'
	pos := posOf(tok)
	nameTok, err := p.expect(lexer.IDENT)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.ASSIGN); err != nil {
		return nil, err
	}
	ta, err := p.parseTypeAnnotation("ts")
	if err != nil {
		return nil, err
	}
	p.consumeSemicolon()
	return ast.NewTypeAliasDeclaration(nameTok.Literal, ta, pos), nil
}

// parseEnumDeclaration parses `[const] enum Name { A [= expr], ... }`.
// The optional `const` keyword must already have been consumed before calling
// this; `isConst` reports whether it was present.
func (p *Parser) parseEnumDeclaration() (*ast.EnumDeclaration, error) {
	isConst := false
	if p.peek().Type == lexer.CONST {
		isConst = true
		p.advance() // consume 'const'
	}
	tok := p.advance() // consume 'enum'
	pos := posOf(tok)
	nameTok, err := p.expect(lexer.IDENT)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.LBRACE); err != nil {
		return nil, err
	}
	var members []ast.EnumMember
	for !p.check(lexer.RBRACE) && !p.check(lexer.EOF) {
		memberTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}
		var val ast.Expression
		if p.match(lexer.ASSIGN) {
			val, err = p.parseAssignment()
			if err != nil {
				return nil, err
			}
		}
		members = append(members, ast.EnumMember{Name: memberTok.Literal, Value: val})
		if !p.match(lexer.COMMA) {
			break
		}
		// Trailing comma allowed.
	}
	if _, err := p.expect(lexer.RBRACE); err != nil {
		return nil, err
	}
	return ast.NewEnumDeclaration(nameTok.Literal, isConst, members, pos), nil
}
