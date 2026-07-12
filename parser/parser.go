package parser

import (
	"KlainMainLang/ast"
	"KlainMainLang/jsdoc"
	"KlainMainLang/lexer"
	"fmt"
)

type Parser struct {
	tokens     []lexer.Token
	pos        int
	pendingDoc *jsdoc.Comment
}

func New(tokens []lexer.Token) *Parser {
	return &Parser{tokens: tokens}
}

func Parse(src string) (*ast.Program, error) {
	tokens, err := lexer.Tokenize(src)
	if err != nil {
		return nil, err
	}
	return New(tokens).ParseProgram()
}

// --- Token stream helpers ---

func (p *Parser) peek() lexer.Token {
	for p.pos < len(p.tokens) && p.tokens[p.pos].Type == lexer.JSDOC {
		p.pendingDoc = jsdoc.Parse(p.tokens[p.pos].Literal)
		p.pos++
	}
	if p.pos >= len(p.tokens) {
		return lexer.Token{Type: lexer.EOF}
	}
	return p.tokens[p.pos]
}

func (p *Parser) advance() lexer.Token {
	t := p.peek()
	p.pos++
	return t
}

func (p *Parser) check(typ lexer.TokenType) bool {
	return p.peek().Type == typ
}

func (p *Parser) match(types ...lexer.TokenType) bool {
	for _, t := range types {
		if p.check(t) {
			p.advance()
			return true
		}
	}
	return false
}

func (p *Parser) expect(typ lexer.TokenType) (lexer.Token, error) {
	t := p.peek()
	if t.Type != typ {
		return lexer.Token{}, fmt.Errorf("%d:%d: expected %s, got %s", t.Line, t.Col, typ, t.Type)
	}
	return p.advance(), nil
}

func (p *Parser) consumeSemicolon() {
	p.match(lexer.SEMICOLON)
}

func posOf(t lexer.Token) ast.Pos { return ast.Pos{Line: t.Line, Col: t.Col} }

// peekNth returns the n-th non-JSDOC token from the current position (0 = peek()).
func (p *Parser) peekNth(n int) lexer.Token {
	count := 0
	for i := p.pos; i < len(p.tokens); i++ {
		if p.tokens[i].Type == lexer.JSDOC {
			continue
		}
		if count == n {
			return p.tokens[i]
		}
		count++
	}
	return lexer.Token{Type: lexer.EOF}
}

func (p *Parser) takeDoc() *jsdoc.Comment {
	d := p.pendingDoc
	p.pendingDoc = nil
	return d
}

// --- Program ---

func (p *Parser) ParseProgram() (*ast.Program, error) {
	prog := &ast.Program{}
	for !p.check(lexer.EOF) {
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		prog.Body = append(prog.Body, stmt)
	}
	return prog, nil
}

// --- Statements ---

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
		return p.parseFunctionDecl(false)
	case lexer.IMPORT:
		return p.parseImportDeclaration()
	case lexer.EXPORT:
		return p.parseExportDeclaration()
	case lexer.ASYNC:
		if p.peekNth(1).Type == lexer.FUNCTION {
			p.advance() // consume 'async'
			return p.parseFunctionDecl(true)
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
		}
		// label: statement (e.g. `outer: for (...) { ... }`)
		if p.peekNth(1).Type == lexer.COLON {
			return p.parseLabeledStatement()
		}
	}
	return p.parseExpressionStatement()
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
		*ast.TypeAliasDeclaration, *ast.EnumDeclaration:
		return ast.NewExportDeclaration(decl, pos), nil
	default:
		return nil, fmt.Errorf("%d:%d: 'export' can only precede a function, variable, interface, type alias, or enum declaration", pos.Line, pos.Col)
	}
}

func (p *Parser) parseVarDecl(consumeSemi bool) (*ast.VarDeclaration, error) {
	doc := p.takeDoc()
	tok := p.advance() // let / const / var
	pos := posOf(tok)

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
			ta = &ast.TypeAnnotation{Name: t, Source: "jsdoc"}
		}
	}

	var init ast.Expression
	if p.match(lexer.ASSIGN) {
		init, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}

	if consumeSemi {
		p.consumeSemicolon()
	}

	return ast.NewVarDeclaration(tok.Literal, nameTok.Literal, ta, init, pos), nil
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
				return &ast.TypeAnnotation{Source: source, IsFuncType: true, FuncParams: funcParams, FuncRetType: retType}, nil
			}
			if len(funcParams) == 1 && singleUnnamed {
				p.pos = beforeArrow
				return &funcParams[0], nil
			}
			return nil, err
		}
		if len(funcParams) == 1 && singleUnnamed {
			return &funcParams[0], nil
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
		// Support { ... }[] array-of-object type annotations.
		for p.check(lexer.LBRACKET) {
			p.advance()
			if _, err := p.expect(lexer.RBRACKET); err != nil {
				return nil, fmt.Errorf("expected ] in array type annotation")
			}
			ta = &ast.TypeAnnotation{Source: source, ElemType: ta}
		}
		return ta, nil
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

	// Promise<T>: parse the type parameter instead of skipping.
	if name == "Promise" && p.check(lexer.LT) {
		p.advance() // consume '<'
		inner, err := p.parseTypeAnnotation(source)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.GT); err != nil {
			return nil, fmt.Errorf("expected '>' to close Promise<T>")
		}
		return &ast.TypeAnnotation{Name: "Promise", ElemType: inner, Source: source}, nil
	}

	// Skip other generics like Array<number>, Map<K,V>, etc.
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
	if p.peek().Type == lexer.IDENT && p.peek().Literal == "extends" {
		p.advance() // extends
		p.advance() // base name
	}
	if _, err := p.expect(lexer.LBRACE); err != nil {
		return nil, err
	}
	var fields []ast.AnnotField
	for !p.check(lexer.RBRACE) && !p.check(lexer.EOF) {
		doc := p.takeDoc()
		fieldTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
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
	return ast.NewInterfaceDeclaration(nameTok.Literal, fields, pos), nil
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

func (p *Parser) parseArrayLiteral() (*ast.ArrayLiteral, error) {
	tok := p.advance() // consume [
	pos := posOf(tok)
	var elems []ast.Expression
	for !p.check(lexer.RBRACKET) && !p.check(lexer.EOF) {
		var elem ast.Expression
		if p.check(lexer.ELLIPSIS) {
			spreadTok := p.advance()
			arg, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			elem = ast.NewSpreadElement(arg, posOf(spreadTok))
		} else {
			var err error
			elem, err = p.parseAssignment()
			if err != nil {
				return nil, err
			}
		}
		elems = append(elems, elem)
		if !p.match(lexer.COMMA) {
			break
		}
	}
	if _, err := p.expect(lexer.RBRACKET); err != nil {
		return nil, err
	}
	return ast.NewArrayLiteral(elems, pos), nil
}

func (p *Parser) parseFunctionDecl(isAsync bool) (*ast.FunctionDeclaration, error) {
	p.advance() // 'function'

	nameTok, err := p.expect(lexer.IDENT)
	if err != nil {
		return nil, err
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

	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}

	return &ast.FunctionDeclaration{
		Name: nameTok.Literal, Params: params, ReturnType: retType, Body: body, IsAsync: isAsync,
	}, nil
}

func (p *Parser) parseParamList() ([]ast.Param, error) {
	var params []ast.Param
	for !p.check(lexer.RPAREN) && !p.check(lexer.EOF) {
		rest := p.match(lexer.ELLIPSIS)
		nameTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
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
		params = append(params, ast.Param{Name: nameTok.Literal, Type: ta, Rest: rest, Default: dflt, Optional: optional})
		if rest {
			break // rest param must be last
		}
		if !p.match(lexer.COMMA) {
			break
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

	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}

	// Detect for-of and for-in: for (let/const/var name of/in ...)
	if p.check(lexer.LET) || p.check(lexer.CONST) || p.check(lexer.VAR) {
		if p.peekNth(2).Type == lexer.IDENT && p.peekNth(2).Literal == "of" {
			return p.parseForOfBody(pos)
		}
		if p.peekNth(2).Type == lexer.IDENT && p.peekNth(2).Literal == "in" {
			return p.parseForInBody(pos)
		}
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

	// Update (optional)
	var update ast.Expression
	if !p.check(lexer.RPAREN) {
		var err error
		update, err = p.parseExpression()
		if err != nil {
			return nil, err
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
		p.advance() // consume 'catch'
		if _, err := p.expect(lexer.LPAREN); err != nil {
			return nil, err
		}
		paramTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}
		// Optional type annotation on catch param — skip it.
		if p.check(lexer.COLON) {
			p.advance()
			if _, err := p.parseTypeAnnotation("ts"); err != nil {
				return nil, err
			}
		}
		if _, err := p.expect(lexer.RPAREN); err != nil {
			return nil, err
		}
		cbody, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		catch = &ast.CatchClause{Param: paramTok.Literal, Body: cbody}
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

func (p *Parser) parseArrayDestructuring() (*ast.ArrayDestructuring, error) {
	tok := p.advance() // let/const/var
	pos := posOf(tok)
	p.advance() // [
	var names []string
	for !p.check(lexer.RBRACKET) && !p.check(lexer.EOF) {
		if p.check(lexer.COMMA) {
			p.advance() // hole — consume comma, record skip
			names = append(names, "")
		} else {
			nameTok, err := p.expect(lexer.IDENT)
			if err != nil {
				return nil, err
			}
			names = append(names, nameTok.Literal)
			if !p.match(lexer.COMMA) {
				break
			}
		}
	}
	if _, err := p.expect(lexer.RBRACKET); err != nil {
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
	return ast.NewArrayDestructuring(tok.Literal, names, init, pos), nil
}

func (p *Parser) parseObjectDestructuring() (*ast.ObjectDestructuring, error) {
	tok := p.advance() // let/const/var
	pos := posOf(tok)
	p.advance() // {
	var props []ast.DestructProp
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
		props = append(props, ast.DestructProp{Key: keyTok.Literal, Local: local})
		if !p.match(lexer.COMMA) {
			break
		}
	}
	if _, err := p.expect(lexer.RBRACE); err != nil {
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
	p.consumeSemicolon()
	return ast.NewExpressionStatement(expr, expr.GetPos()), nil
}

// --- Expression parsing (precedence climbing) ---
//
// Precedence (low → high):
//   1  assignment  = += -= *= /= &= |= ^= <<= >>= >>>=   (right-assoc)
//   2  ||
//   3  &&
//   4  |
//   5  ^
//   6  &
//   7  == != === !==
//   8  < > <= >=
//   9  << >> >>>
//  10  + -
//  11  * / %
//  12  unary prefix: ! ~ - + ++ --
//  13  postfix ++ --  then call/member chains

func (p *Parser) parseExpression() (ast.Expression, error) {
	return p.parseAssignment()
}

func (p *Parser) parseAssignment() (ast.Expression, error) {
	left, err := p.parseTernary()
	if err != nil {
		return nil, err
	}

	switch p.peek().Type {
	case lexer.ASSIGN,
		lexer.PLUS_ASSIGN, lexer.MINUS_ASSIGN, lexer.STAR_ASSIGN, lexer.SLASH_ASSIGN,
		lexer.AND_ASSIGN, lexer.OR_ASSIGN, lexer.XOR_ASSIGN,
		lexer.LSHIFT_ASSIGN, lexer.RSHIFT_ASSIGN, lexer.URSHIFT_ASSIGN:
		opTok := p.advance()
		right, err := p.parseAssignment() // right-assoc
		if err != nil {
			return nil, err
		}
		return ast.NewAssignmentExpression(opTok.Literal, left, right, posOf(opTok)), nil
	}
	return left, nil
}

func (p *Parser) parseTernary() (ast.Expression, error) {
	cond, err := p.parseNullish()
	if err != nil {
		return nil, err
	}
	if !p.check(lexer.QUESTION) {
		return cond, nil
	}
	p.advance()                      // consume '?'
	then, err := p.parseAssignment() // right-associative
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.COLON); err != nil {
		return nil, err
	}
	alt, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	return ast.NewConditionalExpression(cond, then, alt, cond.GetPos()), nil
}

func (p *Parser) parseNullish() (ast.Expression, error) {
	left, err := p.parseLogicalOr()
	if err != nil {
		return nil, err
	}
	for p.check(lexer.NULLISH) {
		op := p.advance()
		right, err := p.parseLogicalOr()
		if err != nil {
			return nil, err
		}
		left = ast.NewBinaryExpression(op.Literal, left, right, posOf(op))
	}
	return left, nil
}

func (p *Parser) parseLogicalOr() (ast.Expression, error) {
	left, err := p.parseLogicalAnd()
	if err != nil {
		return nil, err
	}
	for p.check(lexer.OR) {
		op := p.advance()
		right, err := p.parseLogicalAnd()
		if err != nil {
			return nil, err
		}
		left = ast.NewBinaryExpression(op.Literal, left, right, posOf(op))
	}
	return left, nil
}

func (p *Parser) parseLogicalAnd() (ast.Expression, error) {
	left, err := p.parseBitwiseOr()
	if err != nil {
		return nil, err
	}
	for p.check(lexer.AND) {
		op := p.advance()
		right, err := p.parseBitwiseOr()
		if err != nil {
			return nil, err
		}
		left = ast.NewBinaryExpression(op.Literal, left, right, posOf(op))
	}
	return left, nil
}

func (p *Parser) parseBitwiseOr() (ast.Expression, error) {
	left, err := p.parseBitwiseXor()
	if err != nil {
		return nil, err
	}
	for p.check(lexer.BITOR) {
		op := p.advance()
		right, err := p.parseBitwiseXor()
		if err != nil {
			return nil, err
		}
		left = ast.NewBinaryExpression(op.Literal, left, right, posOf(op))
	}
	return left, nil
}

func (p *Parser) parseBitwiseXor() (ast.Expression, error) {
	left, err := p.parseBitwiseAnd()
	if err != nil {
		return nil, err
	}
	for p.check(lexer.BITXOR) {
		op := p.advance()
		right, err := p.parseBitwiseAnd()
		if err != nil {
			return nil, err
		}
		left = ast.NewBinaryExpression(op.Literal, left, right, posOf(op))
	}
	return left, nil
}

func (p *Parser) parseBitwiseAnd() (ast.Expression, error) {
	left, err := p.parseEquality()
	if err != nil {
		return nil, err
	}
	for p.check(lexer.BITAND) {
		op := p.advance()
		right, err := p.parseEquality()
		if err != nil {
			return nil, err
		}
		left = ast.NewBinaryExpression(op.Literal, left, right, posOf(op))
	}
	return left, nil
}

func (p *Parser) parseEquality() (ast.Expression, error) {
	left, err := p.parseRelational()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == lexer.EQ || p.peek().Type == lexer.NEQ ||
		p.peek().Type == lexer.STRICT_EQ || p.peek().Type == lexer.STRICT_NEQ {
		op := p.advance()
		right, err := p.parseRelational()
		if err != nil {
			return nil, err
		}
		left = ast.NewBinaryExpression(op.Literal, left, right, posOf(op))
	}
	return left, nil
}

func (p *Parser) parseRelational() (ast.Expression, error) {
	left, err := p.parseShift()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == lexer.LT || p.peek().Type == lexer.GT ||
		p.peek().Type == lexer.LTE || p.peek().Type == lexer.GTE {
		op := p.advance()
		right, err := p.parseShift()
		if err != nil {
			return nil, err
		}
		left = ast.NewBinaryExpression(op.Literal, left, right, posOf(op))
	}
	return left, nil
}

func (p *Parser) parseShift() (ast.Expression, error) {
	left, err := p.parseAdditive()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == lexer.LSHIFT || p.peek().Type == lexer.RSHIFT || p.peek().Type == lexer.URSHIFT {
		op := p.advance()
		right, err := p.parseAdditive()
		if err != nil {
			return nil, err
		}
		left = ast.NewBinaryExpression(op.Literal, left, right, posOf(op))
	}
	return left, nil
}

func (p *Parser) parseAdditive() (ast.Expression, error) {
	left, err := p.parseMultiplicative()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == lexer.PLUS || p.peek().Type == lexer.MINUS {
		op := p.advance()
		right, err := p.parseMultiplicative()
		if err != nil {
			return nil, err
		}
		left = ast.NewBinaryExpression(op.Literal, left, right, posOf(op))
	}
	return left, nil
}

func (p *Parser) parseMultiplicative() (ast.Expression, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == lexer.STAR || p.peek().Type == lexer.SLASH || p.peek().Type == lexer.PERCENT {
		op := p.advance()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = ast.NewBinaryExpression(op.Literal, left, right, posOf(op))
	}
	return left, nil
}

func (p *Parser) parseUnary() (ast.Expression, error) {
	switch p.peek().Type {
	case lexer.NOT, lexer.BITNOT:
		op := p.advance()
		arg, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return ast.NewUnaryExpression(op.Literal, true, arg, posOf(op)), nil
	case lexer.MINUS:
		op := p.advance()
		arg, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return ast.NewUnaryExpression(op.Literal, true, arg, posOf(op)), nil
	case lexer.TYPEOF:
		op := p.advance()
		arg, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return ast.NewUnaryExpression("typeof", true, arg, posOf(op)), nil
	case lexer.INC, lexer.DEC:
		op := p.advance()
		arg, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return ast.NewUpdateExpression(op.Literal, true, arg, posOf(op)), nil
	case lexer.AWAIT:
		op := p.advance()
		arg, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return ast.NewAwaitExpression(arg, posOf(op)), nil
	}
	return p.parsePostfix()
}

func (p *Parser) parsePostfix() (ast.Expression, error) {
	expr, err := p.parseCallMember()
	if err != nil {
		return nil, err
	}
	if p.peek().Type == lexer.INC || p.peek().Type == lexer.DEC {
		op := p.advance()
		return ast.NewUpdateExpression(op.Literal, false, expr, posOf(op)), nil
	}
	return expr, nil
}

// parseCallMember handles left-recursive .prop and (args) chains.
func (p *Parser) parseCallMember() (ast.Expression, error) {
	expr, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	for {
		switch p.peek().Type {
		case lexer.OPTIONAL_DOT:
			p.advance()
			propTok, err := p.expect(lexer.IDENT)
			if err != nil {
				return nil, err
			}
			mem := ast.NewMemberExpression(expr, propTok.Literal, posOf(propTok))
			mem.Optional = true
			expr = mem
		case lexer.DOT:
			p.advance()
			propTok, err := p.expect(lexer.IDENT)
			if err != nil {
				return nil, err
			}
			expr = ast.NewMemberExpression(expr, propTok.Literal, posOf(propTok))
		case lexer.LBRACKET:
			lbrak := p.advance()
			index, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.RBRACKET); err != nil {
				return nil, err
			}
			expr = ast.NewIndexExpression(expr, index, posOf(lbrak))
		case lexer.LPAREN:
			lparen := p.advance()
			args, err := p.parseArgList()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.RPAREN); err != nil {
				return nil, err
			}
			expr = ast.NewCallExpression(expr, args, posOf(lparen))
		default:
			return expr, nil
		}
	}
}

func (p *Parser) parseObjectLiteral() (*ast.ObjectLiteral, error) {
	tok := p.advance() // consume '{'
	pos := posOf(tok)
	var props []ast.ObjectProperty
	for !p.check(lexer.RBRACE) && !p.check(lexer.EOF) {
		if p.check(lexer.ELLIPSIS) {
			// Object spread `{ ...obj, key: val }` — stored as an
			// ObjectProperty with an empty Key sentinel and a *SpreadElement
			// Value, so emitObjectLiteral can distinguish it from a regular
			// (possibly shorthand) property without a separate AST node.
			spreadTok := p.advance()
			arg, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			props = append(props, ast.ObjectProperty{Key: "", Value: ast.NewSpreadElement(arg, posOf(spreadTok))})
			if !p.match(lexer.COMMA) {
				break
			}
			continue
		}
		keyTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}
		var val ast.Expression
		if p.check(lexer.COLON) {
			p.advance() // ':'
			val, err = p.parseAssignment()
			if err != nil {
				return nil, err
			}
		} else {
			// Shorthand property `{ x }` — sugar for `{ x: x }`, referencing
			// the in-scope variable/binding of the same name.
			val = ast.NewIdentifier(keyTok.Literal, posOf(keyTok))
		}
		props = append(props, ast.ObjectProperty{Key: keyTok.Literal, Value: val})
		if !p.match(lexer.COMMA) {
			break
		}
	}
	if _, err := p.expect(lexer.RBRACE); err != nil {
		return nil, err
	}
	return ast.NewObjectLiteral(props, pos), nil
}

func (p *Parser) parseNew() (ast.Expression, error) {
	tok := p.advance() // consume 'new'
	pos := posOf(tok)

	nameTok := p.peek()
	if nameTok.Type != lexer.IDENT {
		return nil, fmt.Errorf("%d:%d: expected constructor name after 'new'", nameTok.Line, nameTok.Col)
	}
	switch nameTok.Literal {
	case "Array":
		return p.parseNewArrayBody(pos)
	case "Map":
		return p.parseNewMapBody(pos)
	case "Set":
		return p.parseNewSetBody(pos)
	case "Error":
		return p.parseNewErrorBody(pos)
	case "Date":
		return p.parseNewDateBody(pos)
	default:
		return nil, fmt.Errorf("%d:%d: 'new %s' is not supported", nameTok.Line, nameTok.Col, nameTok.Literal)
	}
}

func (p *Parser) parseNewDateBody(pos ast.Pos) (*ast.NewDateExpression, error) {
	p.advance() // consume 'Date'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	var args []ast.Expression
	for !p.check(lexer.RPAREN) && !p.check(lexer.EOF) {
		arg, err := p.parseAssignment()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if !p.match(lexer.COMMA) {
			break
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	switch {
	case len(args) == 0:
		return ast.NewNewDateExpression(nil, pos), nil
	case len(args) == 1:
		return ast.NewNewDateExpression(args[0], pos), nil
	case len(args) > 7:
		return nil, fmt.Errorf("%d:%d: new Date(...) accepts at most 7 arguments (year, month, day, hours, minutes, seconds, milliseconds)", pos.Line, pos.Col)
	default:
		return ast.NewNewDateExpressionMulti(args, pos), nil
	}
}

func (p *Parser) parseNewErrorBody(pos ast.Pos) (*ast.NewErrorExpression, error) {
	p.advance() // consume 'Error'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	var msg ast.Expression
	if !p.check(lexer.RPAREN) {
		var err error
		msg, err = p.parseAssignment()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewErrorExpression(msg, pos), nil
}

func (p *Parser) parseNewArrayBody(pos ast.Pos) (*ast.NewArrayExpression, error) {
	p.advance() // consume 'Array'

	var elemType *ast.TypeAnnotation
	if p.check(lexer.LT) {
		p.advance() // consume '<'
		var err error
		elemType, err = p.parseTypeAnnotation("ts")
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.GT); err != nil {
			return nil, err
		}
	}

	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	size, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}

	return ast.NewNewArrayExpression(elemType, size, pos), nil
}

func (p *Parser) parseNewMapBody(pos ast.Pos) (*ast.NewMapExpression, error) {
	p.advance() // consume 'Map'

	var keyType, valType *ast.TypeAnnotation
	if p.check(lexer.LT) {
		p.advance() // consume '<'
		var err error
		keyType, err = p.parseTypeAnnotation("ts")
		if err != nil {
			return nil, err
		}
		if p.match(lexer.COMMA) {
			valType, err = p.parseTypeAnnotation("ts")
			if err != nil {
				return nil, err
			}
		}
		if _, err := p.expect(lexer.GT); err != nil {
			return nil, err
		}
	}

	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	// Optional initial entries (we don't support them yet; just consume closing paren)
	if !p.check(lexer.RPAREN) {
		return nil, fmt.Errorf("%d:%d: new Map() does not accept arguments", pos.Line, pos.Col)
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}

	return ast.NewNewMapExpression(keyType, valType, pos), nil
}

func (p *Parser) parseNewSetBody(pos ast.Pos) (*ast.NewSetExpression, error) {
	p.advance() // consume 'Set'

	var elemType *ast.TypeAnnotation
	if p.check(lexer.LT) {
		p.advance() // consume '<'
		var err error
		elemType, err = p.parseTypeAnnotation("ts")
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.GT); err != nil {
			return nil, err
		}
	}

	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	if !p.check(lexer.RPAREN) {
		return nil, fmt.Errorf("%d:%d: new Set() does not accept arguments", pos.Line, pos.Col)
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}

	return ast.NewNewSetExpression(elemType, pos), nil
}

func (p *Parser) parseArrowFunction() (*ast.ArrowFunction, error) {
	tok := p.advance() // consume '('
	pos := posOf(tok)

	var params []ast.Param
	for !p.check(lexer.RPAREN) && !p.check(lexer.EOF) {
		nameTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}
		optional := p.match(lexer.QUESTION)
		var pty *ast.TypeAnnotation
		if p.check(lexer.COLON) {
			p.advance()
			pty, err = p.parseTypeAnnotation("ts")
			if err != nil {
				return nil, err
			}
		}
		var dflt ast.Expression
		if p.match(lexer.ASSIGN) {
			dflt, err = p.parseAssignment()
			if err != nil {
				return nil, err
			}
		}
		params = append(params, ast.Param{Name: nameTok.Literal, Type: pty, Default: dflt, Optional: optional})
		p.match(lexer.COMMA)
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}

	// Optional return type annotation
	var retType *ast.TypeAnnotation
	if p.check(lexer.COLON) {
		p.advance()
		var err error
		retType, err = p.parseTypeAnnotation("ts")
		if err != nil {
			return nil, err
		}
	}

	if _, err := p.expect(lexer.ARROW); err != nil {
		return nil, err
	}

	// Block body or expression body
	if p.check(lexer.LBRACE) {
		block, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		return ast.NewArrowFunction(params, retType, nil, block, pos), nil
	}
	body, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	return ast.NewArrowFunction(params, retType, body, nil, pos), nil
}

func (p *Parser) parseTemplateLiteral() (ast.Expression, error) {
	tok := p.advance() // consume TEMPLATE_HEAD
	pos := posOf(tok)
	quasis := []string{tok.Literal}
	var exprs []ast.Expression

	for {
		expr, err := p.parseAssignment()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, expr)

		next := p.peek()
		switch next.Type {
		case lexer.TEMPLATE_MIDDLE:
			quasis = append(quasis, next.Literal)
			p.advance()
		case lexer.TEMPLATE_TAIL:
			quasis = append(quasis, next.Literal)
			p.advance()
			return ast.NewTemplateLiteral(quasis, exprs, pos), nil
		default:
			return nil, fmt.Errorf("%d:%d: expected template continuation, got %s", next.Line, next.Col, next.Type)
		}
	}
}

func (p *Parser) parseArgList() ([]ast.Expression, error) {
	var args []ast.Expression
	for !p.check(lexer.RPAREN) && !p.check(lexer.EOF) {
		arg, err := p.parseAssignment()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if !p.match(lexer.COMMA) {
			break
		}
	}
	return args, nil
}

func (p *Parser) parsePrimary() (ast.Expression, error) {
	tok := p.peek()

	switch tok.Type {
	case lexer.NUMBER:
		p.advance()
		return ast.NewNumberLiteral(tok.Literal, posOf(tok)), nil

	case lexer.STRING:
		p.advance()
		return ast.NewStringLiteral(tok.Literal, posOf(tok)), nil

	case lexer.TEMPLATE_NO_SUB:
		p.advance()
		return ast.NewTemplateLiteral([]string{tok.Literal}, nil, posOf(tok)), nil

	case lexer.TEMPLATE_HEAD:
		return p.parseTemplateLiteral()

	case lexer.TRUE:
		p.advance()
		return ast.NewBooleanLiteral(true, posOf(tok)), nil

	case lexer.FALSE:
		p.advance()
		return ast.NewBooleanLiteral(false, posOf(tok)), nil

	case lexer.NULL:
		p.advance()
		return ast.NewNullLiteral(false, posOf(tok)), nil

	case lexer.UNDEFINED:
		p.advance()
		return ast.NewNullLiteral(true, posOf(tok)), nil

	case lexer.IDENT:
		p.advance()
		// Bare arrow function: x => expr  or  x => { ... }
		if p.check(lexer.ARROW) {
			p.advance() // consume '=>'
			pos := posOf(tok)
			params := []ast.Param{{Name: tok.Literal, Type: nil}}
			if p.check(lexer.LBRACE) {
				block, err := p.parseBlock()
				if err != nil {
					return nil, err
				}
				return ast.NewArrowFunction(params, nil, nil, block, pos), nil
			}
			body, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			return ast.NewArrowFunction(params, nil, body, nil, pos), nil
		}
		return ast.NewIdentifier(tok.Literal, posOf(tok)), nil

	case lexer.LPAREN:
		// Detect arrow function: () => ..., (): T => ..., (name: type, ...) => ...,
		// (name) => ..., or (name, name, ...) => ...
		t1 := p.peekNth(1)
		isArrow := (t1.Type == lexer.RPAREN &&
			(p.peekNth(2).Type == lexer.ARROW || p.peekNth(2).Type == lexer.COLON)) ||
			(t1.Type == lexer.IDENT && p.peekNth(2).Type == lexer.COLON) ||
			(t1.Type == lexer.IDENT && p.peekNth(2).Type == lexer.RPAREN && p.peekNth(3).Type == lexer.ARROW) ||
			(t1.Type == lexer.IDENT && p.peekNth(2).Type == lexer.COMMA)
		if isArrow {
			return p.parseArrowFunction()
		}
		p.advance()
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.RPAREN); err != nil {
			return nil, err
		}
		return expr, nil

	case lexer.ASYNC:
		// async (params) => expr / async (params): RetType => { ... }
		p.advance() // consume 'async'
		af, err := p.parseArrowFunction()
		if err != nil {
			return nil, err
		}
		af.IsAsync = true
		return af, nil

	case lexer.LBRACKET:
		return p.parseArrayLiteral()

	case lexer.LBRACE:
		return p.parseObjectLiteral()

	case lexer.NEW:
		return p.parseNew()
	}

	return nil, fmt.Errorf("%d:%d: unexpected token %s in expression", tok.Line, tok.Col, tok.Type)
}
