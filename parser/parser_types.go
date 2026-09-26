package parser

import (
	"KlainMainLang/ast"
	"KlainMainLang/diag"
	"KlainMainLang/lexer"
)

// Type syntax is parsed into ast type nodes (ast/type_nodes.go). The parser
// only recognises the grammar; what the annotation model makes of a type —
// and which shapes it cannot represent — is decided by ast.TypeAnnotationOf.

// parseTypeAnnotation parses a type and converts it to the annotation code
// generation reads. source is stamped into the annotation ("ts", "jsdoc",
// "as").
func (p *Parser) parseTypeAnnotation(source string) (*ast.TypeAnnotation, error) {
	n, err := p.parseType()
	if err != nil {
		return nil, err
	}
	if p.declarations {
		return ast.NodeAnnotation(n, source), nil
	}
	return ast.TypeAnnotationOfIn(n, source, p.thisClass)
}

// loc is the location of a node that began at start and ends with the last
// consumed token.
func (p *Parser) loc(start lexer.Token) ast.Loc {
	return ast.Loc{Pos: posOf(start), Start: start.Pos, End: p.at(p.pos - 1).End}
}

// keywordTypes are the identifiers TypeScript reads as keyword types.
var keywordTypes = map[string]bool{
	"any": true, "unknown": true, "never": true, "string": true, "number": true,
	"boolean": true, "bigint": true, "symbol": true, "object": true,
}

// parseTrailingArrayBrackets consumes zero or more trailing `[]` after a
// parenthesized, literal, object, tuple, generic or template type, wrapping n
// in an ArrayType for each pair found.
func (p *Parser) parseTrailingArrayBrackets(start lexer.Token, n ast.TypeNode) (ast.TypeNode, error) {
	for p.check(lexer.LBRACKET) && !p.peek().HasPrecedingLineBreak() {
		p.advance()
		if !p.check(lexer.RBRACKET) {
			// An indexed access `T[K]` on any type (`[A, B][0]`,
			// `{ a: A }["a"]`).
			idx, err := p.parseType()
			if err != nil {
				return nil, err
			}
			if !p.check(lexer.RBRACKET) {
				return nil, p.errAt(p.peek(), diag.ExpectedCloseArray)
			}
			p.advance()
			n = &ast.IndexedAccessType{ObjectType: n, IndexType: idx, Range: p.loc(start)}
			continue
		}
		p.advance()
		n = &ast.ArrayType{ElementType: n, Range: p.loc(start)}
	}
	return n, nil
}

// parseTypeParameterNodes parses a `<T extends C, U>` list inside a type (a
// generic function type or method signature), positioned at the `<`.
func (p *Parser) parseTypeParameterNodes(context string) ([]*ast.TypeParameter, error) {
	p.advance() // consume '<'
	var tps []*ast.TypeParameter
	for {
		nameTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}
		tp := &ast.TypeParameter{Name: nameTok.Literal}
		p.declareTypeParam(nameTok.Literal)
		if p.check(lexer.EXTENDS) {
			p.advance() // consume 'extends'
			if tp.Constraint, err = p.parseType(); err != nil {
				return nil, err
			}
		}
		if p.match(lexer.ASSIGN) {
			if tp.Default, err = p.parseType(); err != nil {
				return nil, err
			}
		}
		tp.Range = p.loc(nameTok)
		tps = append(tps, tp)
		if !p.match(lexer.COMMA) || p.check(lexer.GT) {
			break // a trailing comma may end the list
		}
	}
	if err := p.expectGT(context); err != nil {
		return nil, err
	}
	return tps, nil
}

// parseIndexSignatureNode parses an index signature `[ name : K ] : V`,
// positioned at the opening `[`.
func (p *Parser) parseIndexSignatureNode() (*ast.IndexSignature, error) {
	start := p.advance() // consume '['
	nameTok, err := p.expect(lexer.IDENT)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.COLON); err != nil {
		return nil, err
	}
	key, err := p.parseType()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RBRACKET); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.COLON); err != nil {
		return nil, err
	}
	val, err := p.parseType()
	if err != nil {
		return nil, err
	}
	return &ast.IndexSignature{KeyName: nameTok.Literal, KeyType: key, Type: val, Range: p.loc(start)}, nil
}

// parseIndexSignature parses an index signature and returns its value type's
// annotation (interfaces read members one at a time).
func (p *Parser) parseIndexSignature(source string) (*ast.TypeAnnotation, error) {
	n, err := p.parseIndexSignatureNode()
	if err != nil {
		return nil, err
	}
	return ast.IndexSignatureValue(n, source)
}

// parseSignatureTail parses the `(params): R` tail of a method, call or
// construct signature, positioned at the opening `(`. The return type is nil
// when omitted.
func (p *Parser) parseSignatureTail() ([]*ast.SignatureParameter, ast.TypeNode, error) {
	p.advance() // consume '('
	var params []*ast.SignatureParameter
	hasRest := false
	for !p.check(lexer.RPAREN) && !p.check(lexer.EOF) {
		start := p.peek()
		prm := &ast.SignatureParameter{}
		if p.check(lexer.ELLIPSIS) {
			if hasRest {
				return nil, nil, p.errAt(p.peek(), diag.RestParamLastMethodSig)
			}
			p.advance()
			hasRest = true
			prm.Rest = true
		} else if hasRest {
			return nil, nil, p.errAt(p.peek(), diag.RestParamLastMethodSig)
		}
		// A parameter always has a name — `x`, `x?`, `this`, or a binding
		// pattern `{ a, b }` / `[a, b]` — and an optional `: T`; one without
		// a type is any (TypeScript has no type-only parameter).
		switch {
		case p.check(lexer.IDENT) || p.check(lexer.THIS) || p.check(lexer.UNDEFINED):
			prm.Name = p.advance().Literal
		case p.check(lexer.LBRACE) || p.check(lexer.LBRACKET):
			n := p.balancedLength()
			if n == 0 {
				return nil, nil, p.errAt(p.peek(), diag.ExpectedParamName, p.peek().Type)
			}
			for i := 0; i < n; i++ {
				p.advance()
			}
			prm.Name = "__pattern"
		default:
			return nil, nil, p.errAt(p.peek(), diag.ExpectedParamName, p.peek().Type)
		}
		prm.Optional = p.match(lexer.QUESTION)
		if p.match(lexer.COLON) {
			pt, err := p.parseType()
			if err != nil {
				return nil, nil, err
			}
			prm.Type = pt
		}
		if p.match(lexer.ASSIGN) {
			// An initializer is not allowed in a signature (tsc TS2371), but
			// parses: skip the expression.
			if _, err := p.parseAssignment(); err != nil {
				return nil, nil, err
			}
		}
		if prm.Name == "this" && len(params) == 0 && !prm.Rest {
			// A leading `this: T` types the signature's `this`; it is not a
			// parameter.
			p.match(lexer.COMMA)
			continue
		}
		prm.Range = p.loc(start)
		params = append(params, prm)
		p.match(lexer.COMMA)
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, nil, err
	}
	var ret ast.TypeNode
	if p.check(lexer.COLON) {
		p.advance()
		var err error
		if ret, err = p.parseType(); err != nil {
			return nil, nil, err
		}
	}
	return params, ret, nil
}

// parseObjectTypeSignatureTail parses a call-signature tail and returns the
// equivalent function-type annotation (interfaces read members one at a
// time).
func (p *Parser) parseObjectTypeSignatureTail(source string) (*ast.TypeAnnotation, error) {
	start := p.peek()
	params, ret, err := p.parseSignatureTail()
	if err != nil {
		return nil, err
	}
	return ast.SignatureMember(&ast.CallSignature{Parameters: params, Type: ret, Range: p.loc(start)}, source)
}

// parseType parses a full type: the union level, plus the forms only a whole
// type can take — a conditional type `C extends E ? T : F` (whose branches
// are full types, so conditionals nest right-associatively) and the
// return-type predicates `x is T`, `asserts x [is T]`, `asserts this [is T]`.
func (p *Parser) parseType() (ast.TypeNode, error) {
	start := p.peek()
	if p.check(lexer.IDENT) && start.Literal == "asserts" &&
		(p.peekNth(1).Type == lexer.IDENT || p.peekNth(1).Type == lexer.THIS) {
		p.advance() // 'asserts'
		subject := p.advance()
		pred := &ast.TypePredicate{Asserts: true}
		if subject.Type == lexer.THIS {
			pred.This = true
		} else {
			pred.ParameterName = subject.Literal
		}
		if p.check(lexer.IDENT) && p.peek().Literal == "is" {
			p.advance()
			t, err := p.parseUnionType()
			if err != nil {
				return nil, err
			}
			pred.Type = t
		}
		pred.Range = p.loc(start)
		return pred, nil
	}

	left, err := p.parseUnionType()
	if err != nil {
		return nil, err
	}

	if p.check(lexer.IDENT) && p.peek().Literal == "is" && p.peekNth(1).Type != lexer.COLON {
		pred := &ast.TypePredicate{}
		switch l := left.(type) {
		case *ast.TypeReference:
			if len(l.Qualifier) == 0 && len(l.TypeArgs) == 0 {
				pred.ParameterName = l.Name
			}
		case *ast.KeywordType:
			if l.Keyword == "this" {
				pred.This = true
			} else {
				pred.ParameterName = l.Keyword
			}
		}
		if pred.ParameterName == "" && !pred.This {
			return nil, p.errAt(start, diag.ExpectedParamBeforeIs)
		}
		p.advance() // 'is'
		if pred.Type, err = p.parseUnionType(); err != nil {
			return nil, err
		}
		pred.Range = p.loc(start)
		return pred, nil
	}
	if !p.check(lexer.EXTENDS) {
		return left, nil
	}
	p.advance() // consume 'extends'
	ext, err := p.parseUnionType()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.QUESTION); err != nil {
		return nil, err
	}
	trueT, err := p.parseType()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.COLON); err != nil {
		return nil, err
	}
	falseT, err := p.parseType()
	if err != nil {
		return nil, err
	}
	return &ast.ConditionalType{CheckType: left, ExtendsType: ext, TrueType: trueT, FalseType: falseT, Range: p.loc(start)}, nil
}

// parseUnionType parses `A | B | …`; `|` binds looser than `&`, so
// `A & B | C` is `(A & B) | C`, as in TypeScript.
func (p *Parser) parseUnionType() (ast.TypeNode, error) {
	start := p.peek()
	p.match(lexer.BITOR) // a leading `|` (a union split over lines)
	first, err := p.parseIntersectionType()
	if err != nil || !p.check(lexer.BITOR) {
		return first, err
	}
	types := []ast.TypeNode{first}
	for p.check(lexer.BITOR) {
		p.advance() // consume '|'
		t, err := p.parseIntersectionType()
		if err != nil {
			return nil, err
		}
		types = append(types, t)
	}
	return &ast.UnionType{Types: types, Range: p.loc(start)}, nil
}

// parseIntersectionType parses `A & B & …`.
func (p *Parser) parseIntersectionType() (ast.TypeNode, error) {
	start := p.peek()
	p.match(lexer.BITAND) // a leading `&`
	first, err := p.parseTypeAtom()
	if err != nil || !p.check(lexer.BITAND) {
		return first, err
	}
	types := []ast.TypeNode{first}
	for p.check(lexer.BITAND) {
		p.advance() // consume '&'
		t, err := p.parseTypeAtom()
		if err != nil {
			return nil, err
		}
		types = append(types, t)
	}
	return &ast.IntersectionType{Types: types, Range: p.loc(start)}, nil
}

// parseTypeAtom parses one operand of `|`/`&`, including its `[]` and `[K]`
// suffixes.
func (p *Parser) parseTypeAtom() (ast.TypeNode, error) {
	tok := p.peek()

	// Numeric-literal type: `1` / `-1.5`.
	if tok.Type == lexer.NUMBER ||
		(tok.Type == lexer.MINUS && p.peekNth(1).Type == lexer.NUMBER) {
		lit := ""
		if tok.Type == lexer.MINUS {
			p.advance()
			lit = "-"
		}
		lit += p.advance().Literal
		return p.parseTrailingArrayBrackets(tok, &ast.LiteralType{Kind: "number", Value: lit, Range: p.loc(tok)})
	}

	// String-literal type: "north".
	if tok.Type == lexer.STRING {
		p.advance()
		return p.parseTrailingArrayBrackets(tok, &ast.LiteralType{Kind: "string", Value: tok.Literal, Range: p.loc(tok)})
	}

	// `readonly T[]` / `readonly [T, U]`. `readonly` is contextual; the guard
	// requires a type to follow. The mapped-type `{ readonly [K in T]: V }`
	// form is consumed in the object-type path and never reaches here.
	if tok.Type == lexer.IDENT && tok.Literal == "readonly" {
		switch p.peekNth(1).Type {
		case lexer.IDENT, lexer.STRING, lexer.LBRACKET, lexer.LPAREN, lexer.LBRACE:
			p.advance() // consume 'readonly'
			operand, err := p.parseTypeAtom()
			if err != nil {
				return nil, err
			}
			return &ast.TypeOperator{Operator: "readonly", Type: operand, Range: p.loc(tok)}, nil
		}
	}

	// keyof T. Contextual, like `readonly`.
	if tok.Type == lexer.IDENT && tok.Literal == "keyof" {
		p.advance() // consume 'keyof'
		operand, err := p.parseTypeAtom()
		if err != nil {
			return nil, err
		}
		return &ast.TypeOperator{Operator: "keyof", Type: operand, Range: p.loc(tok)}, nil
	}

	// `unique symbol`. Contextual: `unique` is otherwise a name.
	if tok.Type == lexer.IDENT && tok.Literal == "unique" && p.peekNth(1).Type == lexer.IDENT && p.peekNth(1).Literal == "symbol" {
		p.advance() // 'unique'
		operand, err := p.parseTypeAtom()
		if err != nil {
			return nil, err
		}
		return &ast.TypeOperator{Operator: "unique", Type: operand, Range: p.loc(tok)}, nil
	}

	// infer R, inside a conditional's extends clause. Contextual.
	if tok.Type == lexer.IDENT && tok.Literal == "infer" && p.peekNth(1).Type == lexer.IDENT {
		p.advance() // 'infer'
		nameTok := p.advance()
		p.declareTypeParam(nameTok.Literal)
		return &ast.InferType{Name: nameTok.Literal, Range: p.loc(tok)}, nil
	}

	// Constructor type `new (params) => R`, `new <T>(params) => R`; an
	// `abstract` one names an abstract class's constructor.
	if tok.Type == lexer.IDENT && tok.Literal == "abstract" && p.peekNth(1).Type == lexer.NEW {
		p.advance() // 'abstract'
		tok = p.peek()
	}
	if tok.Type == lexer.NEW && (p.peekNth(1).Type == lexer.LPAREN || p.peekNth(1).Type == lexer.LT) {
		p.advance() // consume 'new'
		inner, err := p.parseTypeAtom()
		if err != nil {
			return nil, err
		}
		ft, ok := inner.(*ast.FunctionType)
		if !ok {
			return nil, p.errAt(p.peek(), diag.ExpectedCtorArrow)
		}
		return &ast.ConstructorType{TypeParameters: ft.TypeParameters, Parameters: ft.Parameters, Type: ft.Type, Range: p.loc(tok)}, nil
	}

	// Generic function or constructor type `<T>(x: T) => R`.
	if tok.Type == lexer.LT {
		tps, err := p.parseTypeParameterNodes("generic function type")
		if err != nil {
			return nil, err
		}
		inner, err := p.parseTypeAtom()
		if err != nil {
			return nil, err
		}
		switch f := inner.(type) {
		case *ast.FunctionType:
			f.TypeParameters, f.Range = tps, p.loc(tok)
			return f, nil
		case *ast.ConstructorType:
			f.TypeParameters, f.Range = tps, p.loc(tok)
			return f, nil
		}
		return nil, p.errAt(p.peek(), diag.ExpectedFunctionType)
	}

	if tok.Type == lexer.LPAREN {
		return p.parseParenOrFunctionType()
	}
	if tok.Type == lexer.LBRACE {
		return p.parseObjectType()
	}

	// Tuple type [T0, T1, ...]. A '[' at the start of a type is a tuple; an
	// array type carries its brackets as a suffix.
	if tok.Type == lexer.LBRACKET {
		p.advance() // consume '['
		var elems []ast.TypeNode
		for !p.check(lexer.RBRACKET) && !p.check(lexer.EOF) {
			et, err := p.parseTupleElement()
			if err != nil {
				return nil, err
			}
			elems = append(elems, et)
			if !p.match(lexer.COMMA) {
				break
			}
		}
		if _, err := p.expect(lexer.RBRACKET); err != nil {
			return nil, err
		}
		return p.parseTrailingArrayBrackets(tok, &ast.TupleType{Elements: elems, Range: p.loc(tok)})
	}

	// `import("mod").A.B<T>` import type.
	if tok.Type == lexer.IMPORT && p.peekNth(1).Type == lexer.LPAREN {
		it, err := p.parseImportType()
		if err != nil {
			return nil, err
		}
		return p.parseTrailingArrayBrackets(tok, it)
	}

	// `typeof a.b.c` type query.
	if tok.Type == lexer.TYPEOF {
		p.advance() // consume 'typeof'
		if p.check(lexer.IMPORT) && p.peekNth(1).Type == lexer.LPAREN {
			// `typeof import("mod").A`: the module member's value type, not
			// modelled beyond its syntax.
			it, err := p.parseImportType()
			if err != nil {
				return nil, err
			}
			return p.parseTrailingArrayBrackets(tok, it)
		}
		baseTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}
		q := &ast.TypeQuery{Name: baseTok.Literal}
		for p.check(lexer.DOT) {
			p.advance() // consume '.'
			seg, err := p.expect(lexer.IDENT)
			if err != nil {
				return nil, err
			}
			q.Path = append(q.Path, seg.Literal)
		}
		q.Range = p.loc(tok)
		return p.parseTrailingArrayBrackets(tok, q)
	}

	// Template literal type `` `a-${T}-b` ``.
	if tok.Type == lexer.TEMPLATE_NO_SUB {
		p.advance()
		return p.parseTrailingArrayBrackets(tok, &ast.TemplateLiteralType{Head: tok.Literal, Range: p.loc(tok)})
	}
	if tok.Type == lexer.TEMPLATE_HEAD {
		p.advance() // TEMPLATE_HEAD
		tl := &ast.TemplateLiteralType{Head: tok.Literal}
		for {
			spanStart := p.peek()
			t, err := p.parseType()
			if err != nil {
				return nil, err
			}
			nxt := p.advance()
			if nxt.Type != lexer.TEMPLATE_TAIL && nxt.Type != lexer.TEMPLATE_MIDDLE {
				return nil, p.errAt(nxt, diag.MalformedTemplateType, nxt.Type)
			}
			tl.Spans = append(tl.Spans, &ast.TemplateLiteralTypeSpan{Type: t, Literal: nxt.Literal, Range: p.loc(spanStart)})
			if nxt.Type == lexer.TEMPLATE_TAIL {
				break
			}
		}
		tl.Range = p.loc(tok)
		return p.parseTrailingArrayBrackets(tok, tl)
	}

	return p.parseTypeReference()
}

// parseImportType parses `import("mod").A.B<T>`, positioned at `import`.
func (p *Parser) parseImportType() (*ast.ImportType, error) {
	tok := p.advance() // 'import'
	p.advance()        // '('
	spec, err := p.expect(lexer.STRING)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	it := &ast.ImportType{Argument: spec.Literal}
	for p.check(lexer.DOT) {
		p.advance() // '.'
		seg, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}
		it.Qualifier = append(it.Qualifier, seg.Literal)
	}
	if p.check(lexer.LT) {
		p.advance() // '<'
		for {
			arg, err := p.parseType()
			if err != nil {
				return nil, err
			}
			it.TypeArgs = append(it.TypeArgs, arg)
			if !p.check(lexer.COMMA) {
				break
			}
			p.advance()
		}
		if err := p.expectGT("import(...)<T>"); err != nil {
			return nil, err
		}
	}
	it.Range = p.loc(tok)
	return it, nil
}

// parseTypeReference parses a named type: a keyword type, `true`/`false`, a
// possibly qualified name with optional type arguments, then `[]` and `[K]`
// suffixes.
func (p *Parser) parseTypeReference() (ast.TypeNode, error) {
	tok := p.peek()
	if tok.Type == lexer.THIS {
		// The polymorphic `this` type.
		p.advance()
		return p.parseTrailingArrayBrackets(tok, &ast.KeywordType{Keyword: "this", Range: p.loc(tok)})
	}
	isTypeName := tok.Type == lexer.IDENT ||
		tok.Type == lexer.VOID ||
		tok.Type == lexer.NULL ||
		tok.Type == lexer.UNDEFINED ||
		tok.Type == lexer.TRUE ||
		tok.Type == lexer.FALSE
	if !isTypeName {
		return nil, p.errAt(tok, diag.ExpectedTypeName, tok.Type)
	}
	p.advance()
	name := tok.Literal
	var qualifier []string
	for p.check(lexer.DOT) && p.peekNth(1).Type == lexer.IDENT {
		p.advance() // '.'
		qualifier = append(qualifier, name)
		name = p.advance().Literal
	}

	if p.check(lexer.LT) {
		p.advance() // consume '<'
		ref := &ast.TypeReference{Qualifier: qualifier, Name: name}
		for {
			arg, err := p.parseType()
			if err != nil {
				return nil, err
			}
			ref.TypeArgs = append(ref.TypeArgs, arg)
			if !p.check(lexer.COMMA) {
				break
			}
			ref.ArgSeparators = append(ref.ArgSeparators, posOf(p.advance()))
		}
		if err := p.expectGT(name + "<T>"); err != nil {
			return nil, err
		}
		ref.Range = p.loc(tok)
		return p.parseTrailingArrayBrackets(tok, ref)
	}

	var n ast.TypeNode
	switch {
	case len(qualifier) > 0 || (tok.Type == lexer.IDENT && !keywordTypes[name]):
		n = &ast.TypeReference{Qualifier: qualifier, Name: name, Range: p.loc(tok)}
	case tok.Type == lexer.TRUE || tok.Type == lexer.FALSE:
		n = &ast.LiteralType{Kind: "boolean", Value: name, Range: p.loc(tok)}
	default:
		n = &ast.KeywordType{Keyword: name, Range: p.loc(tok)}
	}
	// Array suffix T[] (may repeat), then indexed access T[K] (may repeat).
	for p.check(lexer.LBRACKET) && p.peekNth(1).Type == lexer.RBRACKET {
		p.advance() // consume [
		p.advance() // consume ]
		n = &ast.ArrayType{ElementType: n, Range: p.loc(tok)}
	}
	for p.check(lexer.LBRACKET) {
		p.advance() // consume [
		idx, err := p.parseType()
		if err != nil {
			return nil, err
		}
		if !p.check(lexer.RBRACKET) {
			return nil, p.errAt(p.peek(), diag.ExpectedCloseIndex)
		}
		p.advance()
		n = &ast.IndexedAccessType{ObjectType: n, IndexType: idx, Range: p.loc(tok)}
	}
	return n, nil
}

// parseTupleElement parses one tuple element: `T`, `T?`, `...T`, or a
// labelled `name: T`, `name?: T`, `...name: T`.
func (p *Parser) parseTupleElement() (ast.TypeNode, error) {
	start := p.peek()
	rest := p.match(lexer.ELLIPSIS)
	if p.check(lexer.IDENT) && (p.peekNth(1).Type == lexer.COLON ||
		(p.peekNth(1).Type == lexer.QUESTION && p.peekNth(2).Type == lexer.COLON)) {
		m := &ast.NamedTupleMember{Name: p.advance().Literal, Rest: rest}
		m.Optional = p.match(lexer.QUESTION)
		p.advance() // ':'
		var err error
		if m.Type, err = p.parseType(); err != nil {
			return nil, err
		}
		m.Range = p.loc(start)
		return m, nil
	}
	t, err := p.parseType()
	if err != nil {
		return nil, err
	}
	if rest {
		return &ast.RestType{Type: t, Range: p.loc(start)}, nil
	}
	if p.match(lexer.QUESTION) {
		return &ast.OptionalType{Type: t, Range: p.loc(start)}, nil
	}
	return t, nil
}

// isFunctionTypeStart reports, positioned at a type's `(`, whether a
// function type follows rather than a parenthesized type: TypeScript's
// isUnambiguouslyStartOfFunctionType. `()` and `(...` start one, as does a
// parameter (a name, `this` or a binding pattern) followed by `:`, `,`, `?`
// or `=`, or by `) =>`. `(number[]) => []` is therefore the parenthesized
// `number[]`, and in `(string) => void` `string` is a parameter's name.
func (p *Parser) isFunctionTypeStart() bool {
	switch p.peekNth(1).Type {
	case lexer.RPAREN, lexer.ELLIPSIS:
		return true
	}
	i := 1
	switch t := p.peekNth(1); t.Type {
	case lexer.IDENT, lexer.THIS:
		i = 2
	case lexer.LBRACE, lexer.LBRACKET:
		depth := 0
		for ; ; i++ {
			switch p.peekNth(i).Type {
			case lexer.LBRACE, lexer.LBRACKET, lexer.LPAREN:
				depth++
			case lexer.RBRACE, lexer.RBRACKET, lexer.RPAREN:
				depth--
			case lexer.EOF:
				return false
			}
			if depth == 0 {
				break
			}
		}
		i++
	default:
		return false
	}
	switch p.peekNth(i).Type {
	case lexer.COLON, lexer.COMMA, lexer.QUESTION, lexer.ASSIGN:
		return true
	case lexer.RPAREN:
		return p.peekNth(i+1).Type == lexer.ARROW
	}
	return false
}

// parseParenOrFunctionType parses a function type `(a: A, b?: B, ...c: C[]) =>
// R` or a parenthesized type `(T)`, positioned at the `(`.
func (p *Parser) parseParenOrFunctionType() (ast.TypeNode, error) {
	if !p.isFunctionTypeStart() {
		start := p.advance() // consume '('
		inner, err := p.parseType()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.RPAREN); err != nil {
			return nil, err
		}
		return p.parseTrailingArrayBrackets(start, &ast.ParenthesizedType{Type: inner, Range: p.loc(start)})
	}
	start := p.advance() // consume '('
	var params []*ast.SignatureParameter
	var thisType ast.TypeNode
	hasRest := false
	for !p.check(lexer.RPAREN) && !p.check(lexer.EOF) {
		pstart := p.peek()
		prm := &ast.SignatureParameter{}
		// Optional leading `...` rest marker; only the final parameter may be
		// rest.
		if p.check(lexer.ELLIPSIS) {
			if hasRest {
				return nil, p.errAt(p.peek(), diag.RestParamLastFunctionType)
			}
			p.advance() // consume '...'
			hasRest = true
			prm.Rest = true
		} else if hasRest {
			return nil, p.errAt(p.peek(), diag.RestParamLastFunctionType)
		}
		// The parameter's name (a binding pattern's type is what matters
		// here), an optional marker, then its type; a parameter with no
		// type is `any`: `(x) => void`. `this: T` types the function's `this`.
		switch {
		case p.check(lexer.IDENT) || p.check(lexer.THIS):
			prm.Name = p.advance().Literal
		case p.check(lexer.LBRACE) || p.check(lexer.LBRACKET):
			depth := 0
			for {
				t := p.advance()
				switch t.Type {
				case lexer.LBRACE, lexer.LBRACKET, lexer.LPAREN:
					depth++
				case lexer.RBRACE, lexer.RBRACKET, lexer.RPAREN:
					depth--
				}
				if depth == 0 || t.Type == lexer.EOF {
					break
				}
			}
		default:
			return nil, p.errAt(p.peek(), diag.ExpectedArrow, p.peek().Type)
		}
		if p.match(lexer.QUESTION) {
			prm.Optional = true
		}
		if p.match(lexer.COLON) {
			pt, err := p.parseType()
			if err != nil {
				return nil, err
			}
			prm.Type = pt
		} else {
			prm.Type = &ast.KeywordType{Keyword: "any", Range: p.loc(pstart)}
		}
		prm.Range = p.loc(pstart)
		if prm.Name == "this" && len(params) == 0 && thisType == nil {
			// A leading `this: T` types the function's `this`; it is not a
			// parameter.
			thisType = prm.Type
		} else {
			params = append(params, prm)
		}
		if !p.match(lexer.COMMA) {
			break
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.ARROW); err != nil {
		return nil, err
	}
	ret, err := p.parseType()
	if err != nil {
		return nil, err
	}
	ft := &ast.FunctionType{Parameters: params, This: thisType, Type: ret, Range: p.loc(start)}
	return p.parseTrailingArrayBrackets(start, ft)
}

// parseObjectType parses an object type `{ … }` or a mapped type
// `{ readonly [K in C]?: V }`, positioned at the `{`.
func (p *Parser) parseObjectType() (ast.TypeNode, error) {
	start := p.advance() // consume '{'

	// Mapped type, detected by lookahead so a plain object type is
	// unaffected. `readonly`/`in` are contextual identifiers; `+readonly` and
	// `-readonly` add or remove the modifier.
	roOff, sign := 0, 0
	if (p.check(lexer.PLUS) || p.check(lexer.MINUS)) && p.peekNth(1).Type == lexer.IDENT && p.peekNth(1).Literal == "readonly" {
		sign = 1
	}
	if p.peekNth(sign).Type == lexer.IDENT && p.peekNth(sign).Literal == "readonly" && p.peekNth(sign+1).Type == lexer.LBRACKET {
		roOff = sign + 1
	}
	if p.peekNth(roOff).Type == lexer.LBRACKET &&
		p.peekNth(roOff+1).Type == lexer.IDENT &&
		p.peekNth(roOff+2).Type == lexer.IDENT && p.peekNth(roOff+2).Literal == "in" {
		m := &ast.MappedType{Readonly: roOff > 0 && !p.check(lexer.MINUS)}
		for i := 0; i < roOff; i++ {
			p.advance() // `+`/`-`, 'readonly'
		}
		p.advance()                     // '['
		m.KeyName = p.advance().Literal // K
		p.declareTypeParam(m.KeyName)
		p.advance() // 'in'
		var err error
		if m.Constraint, err = p.parseType(); err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.RBRACKET); err != nil {
			return nil, err
		}
		// `?`, `+?` adds optionality; `-?` removes it.
		switch {
		case p.check(lexer.MINUS) && p.peekNth(1).Type == lexer.QUESTION:
			p.advance()
			p.advance()
		case p.check(lexer.PLUS) && p.peekNth(1).Type == lexer.QUESTION:
			p.advance()
			m.Optional = p.match(lexer.QUESTION)
		default:
			m.Optional = p.match(lexer.QUESTION)
		}
		if _, err := p.expect(lexer.COLON); err != nil {
			return nil, err
		}
		if m.Type, err = p.parseType(); err != nil {
			return nil, err
		}
		p.match(lexer.SEMICOLON, lexer.COMMA)
		if _, err := p.expect(lexer.RBRACE); err != nil {
			return nil, err
		}
		m.Range = p.loc(start)
		return m, nil
	}

	members, err := p.parseTypeMembers()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RBRACE); err != nil {
		return nil, err
	}
	return p.parseTrailingArrayBrackets(start, &ast.TypeLiteral{Members: members, Range: p.loc(start)})
}

func (p *Parser) parseInterfaceDecl() (*ast.InterfaceDeclaration, error) {
	tok := p.advance() // consume 'interface'
	pos := posOf(tok)
	nameTok, err := p.expect(lexer.IDENT)
	if err != nil {
		return nil, err
	}
	// Optional `<T extends C = D>` type-parameter list (TDD-00010 V1): the
	// nodes as written, and the names and constraints code generation reads.
	var typeParams []string
	var typeParamConstraints []*ast.TypeAnnotation
	var typeParamNodes []*ast.TypeParameter
	if p.check(lexer.LT) {
		tps, err := p.parseTypeParameterNodes(nameTok.Literal + "<T>")
		if err != nil {
			return nil, err
		}
		typeParamNodes = tps
		for _, tp := range tps {
			typeParams = append(typeParams, tp.Name)
			var c *ast.TypeAnnotation
			if tp.Constraint != nil {
				if c, err = ast.TypeAnnotationOf(tp.Constraint, "ts"); err != nil {
					c = nil // unrepresentable for code generation; the checker reads the node
				}
			}
			typeParamConstraints = append(typeParamConstraints, c)
		}
	}
	// `extends Base, Other<T>, ns.Qualified`: every base as written
	// (Heritage); the simple (unqualified, non-generic) names are merged
	// field/method-wise for code generation (ADR-00451 batch).
	var heritage []ast.TypeNode
	var extendsNames []string
	extendsDropped := false
	if p.peek().Type == lexer.EXTENDS {
		p.advance() // extends
		for {
			base, err := p.parseTypeReference()
			if err != nil {
				return nil, err
			}
			heritage = append(heritage, base)
			if ref, ok := base.(*ast.TypeReference); ok && len(ref.Qualifier) == 0 && len(ref.TypeArgs) == 0 {
				extendsNames = append(extendsNames, ref.Name)
			} else {
				extendsDropped = true
			}
			if !p.match(lexer.COMMA) {
				break
			}
		}
	}
	if _, err := p.expect(lexer.LBRACE); err != nil {
		return nil, err
	}
	members, err := p.parseTypeMembers()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RBRACE); err != nil {
		return nil, err
	}
	fields, methods, indexSig, callSig, legacyErr := ast.InterfaceLegacy(members, "ts", jsdocTypeAnnotation)
	decl := ast.NewInterfaceDeclaration(nameTok.Literal, fields, methods, pos)
	decl.TypeParams = typeParams
	decl.TypeParamConstraints = typeParamConstraints
	decl.TypeParameters = typeParamNodes
	decl.IndexSig = indexSig
	decl.CallSig = callSig
	decl.Extends = extendsNames
	decl.ExtendsDropped = extendsDropped
	decl.Members = members
	decl.Heritage = heritage
	decl.LegacyErr = legacyErr
	return decl, nil
}

// parseTypeMembers parses the members of an object type or an interface
// body, positioned after its `{`, up to its `}`.
func (p *Parser) parseTypeMembers() ([]ast.TypeMember, error) {
	var members []ast.TypeMember
	for !p.check(lexer.RBRACE) && !p.check(lexer.EOF) {
		doc := p.takeDoc()
		m, err := p.parseTypeMember()
		if err != nil {
			return nil, err
		}
		if ps, ok := m.(*ast.PropertySignature); ok && doc != nil {
			// A JSDoc width overrides the member's TS type for code
			// generation, as for a variable (`/** @type {int32} */`).
			ps.JSDocType = doc.GetType()
		}
		if ms, ok := m.(*ast.MethodSignature); ok && doc != nil {
			// A builtin declaration's lowering (TDD-00230 P3.2).
			for _, a := range doc.Annotations {
				switch a.Tag {
				case "lower":
					ms.Lower = a.Value
				case "link":
					ms.Link = append(ms.Link, a.Value)
				}
			}
		}
		members = append(members, m)
		p.match(lexer.SEMICOLON, lexer.COMMA)
	}
	return members, nil
}

// parseTypeMember parses one member of an object type or interface body:
// an index signature, a call or construct signature, a `get`/`set`
// accessor signature, a method signature or a property signature.
func (p *Parser) parseTypeMember() (ast.TypeMember, error) {
	start := p.peek()
	readonly := false
	if p.check(lexer.IDENT) && start.Literal == "readonly" && p.startsMemberName(1) {
		p.advance()
		readonly = true
	}
	if p.check(lexer.LBRACKET) && p.peekNth(1).Type == lexer.IDENT && p.peekNth(2).Type == lexer.COLON {
		sig, err := p.parseIndexSignatureNode()
		if err != nil {
			return nil, err
		}
		sig.Readonly = readonly
		return sig, nil
	}
	construct := p.check(lexer.NEW) && (p.peekNth(1).Type == lexer.LPAREN || p.peekNth(1).Type == lexer.LT)
	if construct {
		p.advance() // 'new'
	}
	if p.check(lexer.LPAREN) || p.check(lexer.LT) {
		var tps []*ast.TypeParameter
		if p.check(lexer.LT) {
			var err error
			if tps, err = p.parseTypeParameterNodes("signature<T>"); err != nil {
				return nil, err
			}
		}
		params, ret, err := p.parseSignatureTail()
		if err != nil {
			return nil, err
		}
		if construct {
			return &ast.ConstructSignature{TypeParameters: tps, Parameters: params, Type: ret, Range: p.loc(start)}, nil
		}
		return &ast.CallSignature{TypeParameters: tps, Parameters: params, Type: ret, Range: p.loc(start)}, nil
	}
	// `get name(): T` / `set name(v: T)`: a property of the accessor's type.
	if p.check(lexer.IDENT) && (start.Literal == "get" || start.Literal == "set") && p.startsMemberName(1) &&
		p.peekNth(1).Type != lexer.LPAREN {
		p.advance()
		name, computed, err := p.parseMemberName()
		if err != nil {
			return nil, err
		}
		params, ret, err := p.parseSignatureTail()
		if err != nil {
			return nil, err
		}
		t := ret
		if start.Literal == "set" && len(params) > 0 {
			t = params[0].Type
		}
		return &ast.PropertySignature{Name: name, Computed: computed, Readonly: readonly, Type: t, Accessor: start.Literal, Range: p.loc(start)}, nil
	}
	name, computed, err := p.parseMemberName()
	if err != nil {
		return nil, err
	}
	optional := p.match(lexer.QUESTION)
	if p.check(lexer.LPAREN) || p.check(lexer.LT) {
		m := &ast.MethodSignature{Name: name, Optional: optional, Computed: computed}
		if p.check(lexer.LT) {
			if m.TypeParameters, err = p.parseTypeParameterNodes(name + "<T>"); err != nil {
				return nil, err
			}
		}
		if m.Parameters, m.Type, err = p.parseSignatureTail(); err != nil {
			return nil, err
		}
		m.Range = p.loc(start)
		return m, nil
	}
	ps := &ast.PropertySignature{Name: name, Optional: optional, Readonly: readonly, Computed: computed}
	if p.match(lexer.COLON) {
		if ps.Type, err = p.parseType(); err != nil {
			return nil, err
		}
	}
	ps.Range = p.loc(start)
	return ps, nil
}

// startsMemberName reports whether the token n ahead can begin a member name
// (after a `readonly`, `get` or `set` modifier): anything but the tokens
// that would make the modifier itself the name.
func (p *Parser) startsMemberName(n int) bool {
	switch p.peekNth(n).Type {
	case lexer.COLON, lexer.QUESTION, lexer.LPAREN, lexer.LT, lexer.SEMICOLON, lexer.COMMA, lexer.RBRACE, lexer.EOF:
		return false
	}
	return !p.peekNth(n).HasPrecedingLineBreak() || p.peekNth(n).Type == lexer.LBRACKET
}

// parseMemberName parses a member name: an identifier or keyword, a string
// or numeric literal (its value), or a computed `[expr]` (its source text in
// brackets).
func (p *Parser) parseMemberName() (string, bool, error) {
	tok := p.peek()
	switch {
	case tok.Type == lexer.STRING || tok.Type == lexer.NUMBER || isIdentifierName(tok.Literal):
		p.advance()
		return tok.Literal, false, nil
	case tok.Type == lexer.LBRACKET:
		p.advance()
		e, err := p.parseAssignment()
		if err != nil {
			return "", false, err
		}
		if _, err := p.expect(lexer.RBRACKET); err != nil {
			return "", false, err
		}
		return "[" + computedName(e) + "]", true, nil
	}
	return "", false, p.errAt(tok, diag.ExpectedPropertyName, tok.Type)
}

// computedName renders a computed member name's expression: a dotted name
// (`Symbol.iterator`) or a literal's value.
func computedName(e ast.Expression) string {
	switch x := e.(type) {
	case *ast.Identifier:
		return x.Name
	case *ast.MemberExpression:
		return computedName(x.Object) + "." + x.Property
	case *ast.StringLiteral:
		return x.Value
	case *ast.NumberLiteral:
		return x.Value
	}
	return "?"
}

// isIdentifierName reports whether s is an IdentifierName: an identifier or
// a reserved word, both valid as a member name.
func isIdentifierName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		ok := r == '_' || r == '$' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 127
		if i > 0 {
			ok = ok || r >= '0' && r <= '9'
		}
		if !ok {
			return false
		}
	}
	return true
}

// balancedLength is the number of tokens a bracketed group starting here
// spans, through its matching close (0 when it never closes).
func (p *Parser) balancedLength() int {
	depth := 0
	for i := 0; ; i++ {
		switch p.peekNth(i).Type {
		case lexer.LBRACE, lexer.LBRACKET, lexer.LPAREN:
			depth++
		case lexer.RBRACE, lexer.RBRACKET, lexer.RPAREN:
			depth--
			if depth == 0 {
				return i + 1
			}
		case lexer.EOF:
			return 0
		}
	}
}

func (p *Parser) parseTypeAliasDecl() (*ast.TypeAliasDeclaration, error) {
	tok := p.advance() // consume 'type'
	pos := posOf(tok)
	nameTok, err := p.expect(lexer.IDENT)
	if err != nil {
		return nil, err
	}
	// Optional `<T>` type-parameter list — a generic type alias (TDD-00079):
	// the nodes as written (defaults included), and the names and
	// constraints code generation reads.
	var typeParams []string
	var typeParamConstraints []*ast.TypeAnnotation
	var typeParamNodes []*ast.TypeParameter
	if p.check(lexer.LT) {
		tps, err := p.parseTypeParameterNodes(nameTok.Literal + "<T>")
		if err != nil {
			return nil, err
		}
		typeParamNodes = tps
		for _, tp := range tps {
			typeParams = append(typeParams, tp.Name)
			var c *ast.TypeAnnotation
			if tp.Constraint != nil {
				if c, err = ast.TypeAnnotationOf(tp.Constraint, "ts"); err != nil {
					c = nil // unrepresentable for code generation; the checker reads the node
				}
			}
			typeParamConstraints = append(typeParamConstraints, c)
		}
	}
	if _, err := p.expect(lexer.ASSIGN); err != nil {
		return nil, err
	}
	ta, err := p.parseTypeAnnotation("ts")
	if err != nil {
		return nil, err
	}
	if err := p.parseSemicolon(); err != nil {
		return nil, err
	}
	decl := ast.NewTypeAliasDeclaration(nameTok.Literal, ta, pos)
	decl.TypeParams = typeParams
	decl.TypeParamConstraints = typeParamConstraints
	decl.TypeParameters = typeParamNodes
	return decl, nil
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
		// A member name may be a string literal (`enum E { A, "non id" }`) —
		// only reachable via the bracketless `E.A` form for identifier-shaped
		// names; a string-named member is declarable but its value is only
		// reachable through positions this compiler doesn't model
		// (`E["non id"]`), so declaring it just advances the counter
		// (ADR-00459).
		var memberTok lexer.Token
		if p.check(lexer.STRING) {
			memberTok = p.advance()
		} else {
			var err error
			memberTok, err = p.expect(lexer.IDENT)
			if err != nil {
				return nil, err
			}
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
