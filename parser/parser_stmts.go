package parser

import (
	"KlainMainLang/ast"
	"KlainMainLang/lexer"
	"fmt"
)

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
	case lexer.CLASS:
		return p.parseClassDecl(false)
	case lexer.ABSTRACT:
		if p.peekNth(1).Type == lexer.CLASS {
			p.advance() // consume 'abstract'
			return p.parseClassDecl(true)
		}
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

func (p *Parser) parseFunctionDecl(isAsync bool) (*ast.FunctionDeclaration, error) {
	p.advance() // 'function'

	nameTok, err := p.expect(lexer.IDENT)
	if err != nil {
		return nil, err
	}
	return p.parseFunctionRest(nameTok.Literal, isAsync, false)
}

// parseFunctionRest parses the `(params) : retType? { body }` tail shared by
// a top-level `function NAME` declaration and a class method/constructor
// (whose name is already known and consumed by the caller before this runs).
// bodyOptional is TDD-00009 Stage 4: an abstract method signature
// (`abstract foo(): T;`) has no body at all — terminated by `;` instead of
// a `{...}` block. Never set by a top-level function (always false there).
func (p *Parser) parseFunctionRest(name string, isAsync, bodyOptional bool) (*ast.FunctionDeclaration, error) {
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
	if p.check(lexer.LT) {
		tp, err := p.parseTypeParamList(name + "<T>")
		if err != nil {
			return nil, err
		}
		typeParams = tp
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
			Name: name, TypeParams: typeParams, Params: params, ReturnType: retType, Body: nil, IsAsync: isAsync, IsAbstract: true,
		}
		fd.SetPos(pos)
		return fd, nil
	}

	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}

	fd := &ast.FunctionDeclaration{
		Name: name, TypeParams: typeParams, Params: params, ReturnType: retType, Body: body, IsAsync: isAsync,
	}
	fd.SetPos(pos)
	return fd, nil
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
		var paramName string
		// Optional catch binding: `catch { ... }` with no `(e)` at all.
		if p.check(lexer.LPAREN) {
			p.advance()
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
			if _, err := p.expect(lexer.RPAREN); err != nil {
				return nil, err
			}
		}
		cbody, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		catch = &ast.CatchClause{Param: paramName, Body: cbody}
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
