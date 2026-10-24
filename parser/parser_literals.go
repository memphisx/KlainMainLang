package parser

import (
	"KlainMainLang/ast"
	"KlainMainLang/diag"
	"KlainMainLang/lexer"
	"fmt"
)

func (p *Parser) parseArrayLiteral() (*ast.ArrayLiteral, error) {
	tok := p.advance() // consume [
	pos := posOf(tok)
	var elems []ast.Expression
	restTrailing := false
	for !p.check(lexer.RBRACKET) && !p.check(lexer.EOF) {
		var elem ast.Expression
		if p.check(lexer.COMMA) {
			// Elision (`[1,,3]`, `[,,]` — ADR-00467): a hole reads as
			// `undefined` (the element type's zero value here) and counts
			// toward the length, matching JS. Desugars to the same
			// undefined literal an explicit `undefined` element uses; the
			// comma is consumed by the shared loop tail below.
			hole := ast.NewNullLiteral(true, posOf(p.peek()))
			hole.Elision = true
			elems = append(elems, hole)
			p.advance() // consume ','
			continue
		}
		if p.check(lexer.ELLIPSIS) {
			spreadTok := p.advance()
			arg, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			elem = ast.NewSpreadElement(arg, posOf(spreadTok))
			if p.check(lexer.COMMA) && p.peekNth(1).Type == lexer.RBRACKET {
				restTrailing = true
			}
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
	al := ast.NewArrayLiteral(elems, pos)
	al.RestTrailingComma = restTrailing
	return al, nil
}

func (p *Parser) parseObjectLiteral() (*ast.ObjectLiteral, error) {
	tok := p.advance() // consume '{'
	pos := posOf(tok)
	var props []ast.ObjectProperty
	restTrailing := false
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
			restTrailing = restTrailing || p.check(lexer.RBRACE)
			continue
		}
		// Async / generator method shorthand: `async m() {}`, `*m() {}`,
		// `async *m() {}`, `*[Symbol.iterator]() {}`. `async` is contextual —
		// a modifier only when a member name, `[` or `*` follows on the same
		// line (`{ async: 1 }`, `{ async() {} }` keep their plain meaning).
		memberStart := p.peek().Pos // a method's source text begins here
		isAsyncMethod := p.isWord(0, "async") && p.sameLine(1) && objectMethodNameStart(p.peekNth(1))
		if isAsyncMethod {
			p.advance() // 'async'
		}
		isGeneratorMethod := p.match(lexer.STAR)
		if p.check(lexer.LBRACKET) {
			// Computed property key `{ [expr]: value }` or method `{ [expr]() {} }`.
			p.advance() // '['
			keyExpr, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.RBRACKET); err != nil {
				return nil, err
			}
			// A well-known symbol key (`[Symbol.asyncIterator]` /
			// `[Symbol.iterator]`) desugars to a reserved *static* key
			// (`@@asyncIterator` / `@@iterator`) — same trick the class-member
			// grammar uses — so the literal stays a static struct (a closure-
			// typed field) instead of collapsing to a dynamic Map. Both the
			// `[Symbol.x]: fn` and the `[Symbol.x]() {...}` method-shorthand
			// forms are accepted.
			isMethod := isAsyncMethod || isGeneratorMethod || p.check(lexer.LPAREN) || p.check(lexer.LT)
			methodValue := func() (ast.Expression, error) {
				fnPos := posOf(p.peek())
				p.memberStart = memberStart
				p.nextGen = isGeneratorMethod
				fd, err := p.parseFunctionRest("", isAsyncMethod, false, false)
				if err != nil {
					return nil, err
				}
				eraseTypeParams(fd)
				fe := p.newObjectMethod(fd, isAsyncMethod, fnPos)
				fe.IsGenerator = isGeneratorMethod
				return fe, nil
			}
			if wk, wkOk := wellKnownSymbolMemberName(keyExpr); wkOk {
				var val ast.Expression
				if isMethod {
					fe, err := methodValue()
					if err != nil {
						return nil, err
					}
					val = fe
				} else {
					if _, err := p.expect(lexer.COLON); err != nil {
						return nil, err
					}
					var err error
					val, err = p.parseAssignment()
					if err != nil {
						return nil, err
					}
				}
				props = append(props, ast.ObjectProperty{Key: wk, Value: val})
				if !p.match(lexer.COMMA) {
					break
				}
				continue
			}
			var val ast.Expression
			if isMethod {
				val, err = methodValue()
			} else {
				if _, err := p.expect(lexer.COLON); err != nil {
					return nil, err
				}
				val, err = p.parseAssignment()
			}
			if err != nil {
				return nil, err
			}
			props = append(props, ast.ObjectProperty{KeyExpr: keyExpr, Value: val})
			if !p.match(lexer.COMMA) {
				break
			}
			continue
		}
		// Accessor property `{ get x() {...} }` / `{ set x(v) {...} }`
		// (TDD-00153). Contextual, exactly like the class-member accessor
		// grammar: a bare `get`/`set` followed by another member name commits to
		// accessor parsing; `get: 1`, `get() {}`, `get` (shorthand) keep working
		// because the 2-token lookahead requires an IDENT name to follow.
		// The name is any property name: an identifier, a string or number,
		// a reserved word, or a computed `[expr]`.
		if !isAsyncMethod && !isGeneratorMethod && p.peek().Type == lexer.IDENT &&
			(p.peek().Literal == "get" || p.peek().Literal == "set") &&
			objectMethodNameStart(p.peekNth(1)) && p.peekNth(1).Type != lexer.STAR {
			accessorKind := p.advance().Literal
			nameTok := p.peek()
			prop := ast.ObjectProperty{AccessorKind: accessorKind}
			if p.match(lexer.LBRACKET) {
				keyExpr, err := p.parseAssignment()
				if err != nil {
					return nil, err
				}
				if _, err := p.expect(lexer.RBRACKET); err != nil {
					return nil, err
				}
				if wk, ok := wellKnownSymbolMemberName(keyExpr); ok {
					prop.Key = wk
				} else {
					prop.KeyExpr = keyExpr
				}
			} else {
				p.advance()
				prop.Key = propertyKeyText(nameTok)
			}
			fnPos := posOf(p.peek())
			p.memberStart = memberStart
			fd, err := p.parseFunctionRest("", false, false, false)
			if err != nil {
				return nil, err
			}
			eraseTypeParams(fd)
			fnVal := p.newObjectMethod(fd, false, fnPos)
			fnVal.NamePos = posOf(nameTok) // where a diagnostic about the accessor points
			prop.Value = fnVal
			props = append(props, prop)
			if !p.match(lexer.COMMA) {
				break
			}
			continue
		}
		if isAsyncMethod || isGeneratorMethod {
			keyTok := p.advance()
			if keyTok.Type != lexer.IDENT && keyTok.Type != lexer.STRING && keyTok.Type != lexer.NUMBER && !lexer.IsKeyword(keyTok.Type) {
				return nil, p.errAt(keyTok, diag.ExpectedMethodName, keyTok.Type)
			}
			fnPos := posOf(p.peek())
			p.memberStart = memberStart
			p.nextGen = isGeneratorMethod
			fd, err := p.parseFunctionRest("", isAsyncMethod, false, false)
			if err != nil {
				return nil, err
			}
			eraseTypeParams(fd)
			fe := p.newObjectMethod(fd, isAsyncMethod, fnPos)
			fe.IsGenerator = isGeneratorMethod
			props = append(props, ast.ObjectProperty{Key: propertyKeyText(keyTok), Value: fe})
			if !p.match(lexer.COMMA) {
				break
			}
			continue
		}
		// PropertyName: IDENT, or a STRING/NUMBER literal used as the key
		// text (`{ "foo": 1 }`, `{ 0: 'a' }`) — real JS/TS allow both,
		// only the identifier form supports shorthand. A reserved word is
		// also a valid unquoted property name in real JS/TS (`{ return:
		// 'int32' }`, `{ new: true }`) — accepted contextually when a ':' or
		// '(' follows, so keyword statements after a brace still parse.
		if !p.check(lexer.IDENT) && !p.check(lexer.STRING) && !p.check(lexer.NUMBER) &&
			!(lexer.IsKeyword(p.peek().Type) && (p.peekNth(1).Type == lexer.COLON || p.peekNth(1).Type == lexer.LPAREN)) {
			return nil, p.errAt(p.peek(), diag.ExpectedPropertyName, p.peek().Type)
		}
		keyTok := p.advance()
		var val ast.Expression
		coverInit, shorthand := false, false
		if p.check(lexer.LPAREN) || p.check(lexer.LT) {
			// Method shorthand `{ foo() { ... } }`, generic `{ foo<T>() {…} }`
			// too — sugar for `{ foo:
			// function() { ... } }`, reusing the same parseFunctionRest tail
			// a class method/named function declaration already shares, and
			// the same FunctionExpression value a `key: function(){}` field
			// already works with (async/generator forms above).
			fnPos := posOf(p.peek())
			p.memberStart = memberStart
			fd, err := p.parseFunctionRest("", false, false, false)
			if err != nil {
				return nil, err
			}
			eraseTypeParams(fd)
			val = p.newObjectMethod(fd, false, fnPos)
		} else if p.check(lexer.COLON) {
			p.advance() // ':'
			var err error
			val, err = p.parseAssignment()
			if err != nil {
				return nil, err
			}
		} else if keyTok.Type != lexer.IDENT {
			return nil, p.errAt(p.peek(), diag.ExpectedColon, p.peek().Type)
		} else if p.check(lexer.ASSIGN) {
			// Shorthand-with-default `{ x = default }` — valid only as a
			// destructuring-assignment target (a cover-grammar production; the
			// codegen for an object *value* rejects it, while the destructuring
			// path uses the default). Represented as `x = default`, an
			// AssignmentExpression the destructuring codegen already unwraps.
			p.advance() // '='
			def, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			ident := ast.NewIdentifier(keyTok.Literal, posOf(keyTok))
			val = ast.NewAssignmentExpression("=", ident, def, posOf(keyTok))
			coverInit, shorthand = true, true
		} else {
			// Shorthand property `{ x }` — sugar for `{ x: x }`, referencing
			// the in-scope variable/binding of the same name.
			val = ast.NewIdentifier(keyTok.Literal, posOf(keyTok))
			shorthand = true
		}
		props = append(props, ast.ObjectProperty{Key: propertyKeyText(keyTok), Value: val, CoverInit: coverInit, Shorthand: shorthand})
		if !p.match(lexer.COMMA) {
			break
		}
	}
	if _, err := p.expect(lexer.RBRACE); err != nil {
		return nil, err
	}
	ol := ast.NewObjectLiteral(props, pos)
	ol.RestTrailingComma = restTrailing
	return ol, nil
}

// objectMethodNameStart reports whether t can follow a method modifier in an
// object literal: a member name or the generator `*`.
func objectMethodNameStart(t lexer.Token) bool {
	return t.Type == lexer.IDENT || t.Type == lexer.STRING || t.Type == lexer.NUMBER ||
		t.Type == lexer.STAR || t.Type == lexer.LBRACKET || lexer.IsKeyword(t.Type)
}

func (p *Parser) parseNew() (ast.Expression, error) {
	tok := p.advance() // consume 'new'
	pos := posOf(tok)

	nameTok := p.peek()
	// `new.target`: the meta-property.
	if nameTok.Type == lexer.DOT && p.peekNth(1).Type == lexer.IDENT && p.peekNth(1).Literal == "target" {
		p.advance()
		p.advance()
		return ast.NewNewTargetExpression(pos), nil
	}
	if p.newTargetIsExpression() {
		return p.parseNewOfExpression(pos)
	}
	if nameTok.Type != lexer.IDENT {
		return nil, p.errAt(nameTok, diag.ExpectedConstructor)
	}
	// Qualified constructor: `new mod.Class(...)` (`new stream.Readable(...)`,
	// `new http.ClientRequest(...)` — the standard Node namespace-import
	// shape). The qualifier is consumed and the class name kept; which class
	// that is (a user class or a builtin) is decided after name resolution
	// (sema), never here.
	qualified := false
	qualifier := ""
	var path []string
	for p.peekNth(1).Type == lexer.DOT && p.peekNth(2).Type == lexer.IDENT {
		qualifier = p.advance().Literal // qualifier ident
		path = append(path, qualifier)
		p.advance() // '.'
		nameTok = p.peek()
		qualified = true
	}
	ne, err := p.parseNewGenericBody(pos)
	if ne != nil {
		ne.Qualified = qualified
		ne.Qualifier = qualifier
		ne.QualifierPath = path
	}
	return ne, err
}

// parseNewGenericBody parses `new ClassName<TypeArgs>(args)` — every `new`,
// builtin or user class alike; sema decides which.
func (p *Parser) parseNewGenericBody(pos ast.Pos) (*ast.NewExpression, error) {
	nameTok := p.advance() // consume class name
	return p.parseNewTail(nameTok.Literal, pos)
}

// newTargetIsExpression reports a `new` whose constructor is not a name or a
// dotted name: `new this.K()`, `new ctors[0]()`, `new (f())()`.
func (p *Parser) newTargetIsExpression() bool {
	if p.peek().Type != lexer.IDENT {
		switch p.peek().Type {
		case lexer.THIS, lexer.LPAREN, lexer.CLASS, lexer.FUNCTION:
			return true // `new class {…}()`, `new function () {…}()`
		}
		return false
	}
	i := 1
	for p.peekNth(i).Type == lexer.DOT && p.peekNth(i+1).Type == lexer.IDENT {
		i += 2
	}
	return p.peekNth(i).Type == lexer.LBRACKET
}

// parseNewOfExpression parses `new <member expression>(args)`: the
// constructor is the value of a member chain without calls.
func (p *Parser) parseNewOfExpression(pos ast.Pos) (ast.Expression, error) {
	callee, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		if p.match(lexer.DOT) {
			propTok, err := p.expectPropertyName()
			if err != nil {
				return nil, err
			}
			callee = ast.NewMemberExpression(callee, propTok.Literal, posOf(propTok))
			continue
		}
		if p.check(lexer.LBRACKET) {
			lbrak := p.advance()
			index, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.RBRACKET); err != nil {
				return nil, err
			}
			callee = ast.NewIndexExpression(callee, index, posOf(lbrak))
			continue
		}
		break
	}
	ne, err := p.parseNewTail("", pos)
	if ne != nil {
		ne.Callee = callee
	}
	return ne, err
}

// parseNewTail parses a `new`'s optional type arguments and argument list.
func (p *Parser) parseNewTail(name string, pos ast.Pos) (*ast.NewExpression, error) {
	// Optional explicit `<T>` or `<K, V>` type argument list (TDD-00010 V1 /
	// TDD-00037 generic classes). Unlike a bare generic function call, `new`
	// unambiguously starts a constructor call, so this doesn't hit the
	// `a<b>(c)` grammar ambiguity that keeps explicit type arguments out of
	// V1 for plain calls — the same reasoning `new Map<K,V>()`/
	// `new Set<T>()` already rely on.
	var typeArgs []*ast.TypeAnnotation
	if p.check(lexer.LT) {
		ltStart := p.peek().Pos
		p.advance() // consume '<'
		for {
			arg, err := p.parseTypeAnnotation("ts")
			if err != nil {
				return nil, err
			}
			typeArgs = append(typeArgs, arg)
			if !p.match(lexer.COMMA) {
				break
			}
		}
		if err := p.expectGT(name + "<T>"); err != nil {
			return nil, err
		}
		p.blank(ltStart, p.at(p.pos-1).End)
	}
	// `new X` without an argument list is `new X()`.
	var args []ast.Expression
	if p.match(lexer.LPAREN) {
		for !p.check(lexer.RPAREN) && !p.check(lexer.EOF) {
			var arg ast.Expression
			if p.check(lexer.ELLIPSIS) {
				spreadTok := p.advance() // `new X(...args)`
				inner, err := p.parseAssignment()
				if err != nil {
					return nil, err
				}
				arg = ast.NewSpreadElement(inner, posOf(spreadTok))
			} else {
				var err error
				if arg, err = p.parseAssignment(); err != nil {
					return nil, err
				}
			}
			args = append(args, arg)
			if !p.match(lexer.COMMA) {
				break
			}
		}
		if _, err := p.expect(lexer.RPAREN); err != nil {
			return nil, err
		}
	}
	ne := ast.NewNewExpression(name, args, pos)
	ne.TypeArgs = typeArgs
	return ne, nil
}

func (p *Parser) parseArrowFunction() (*ast.ArrowFunction, error) {
	isAsync := p.nextAsyncArrow
	p.nextAsyncArrow = false
	tok := p.advance() // consume '('
	pos := posOf(tok)
	// An async arrow's parameters are read as its body is: `await` is the
	// operator there, never a name.
	if isAsync {
		savedAwait := p.awaitCtx
		p.awaitCtx = awaitAsync
		defer func() { p.awaitCtx = savedAwait }()
	}

	var params []ast.Param
	for !p.check(lexer.RPAREN) && !p.check(lexer.EOF) {
		// Rest parameter (`(...args: T[]) => ...`) — parses successfully
		// (matching parseParamList's own `...` handling below), but every
		// rest param is inherently array-typed, so it's rejected downstream
		// in codegen (emitClosureFunc) with a clean error the same way any
		// other array-typed arrow-function parameter is — see this file's
		// own comment there (TDD-00059's notes) for why a parse-time
		// rejection isn't used instead: keeping the syntax itself accepted
		// here, consistent with the destructured-parameter branch below,
		// which also parses fine and is rejected later for the same reason.
		restTok := p.peek()
		rest := p.match(lexer.ELLIPSIS)

		// Destructured parameter (`({x, y}: T) => ...` / `([a, b]: T[]) =>
		// ...`, and nested shapes — TDD-00065 Stage 2) — same shape
		// parseParamList's own destructured branch documents (an explicit
		// type annotation always required, no whole-parameter default); a
		// destructured *array* param is further rejected downstream in
		// codegen (emit_func.go's emitClosureFunc) since array-typed closure
		// parameters aren't supported at all yet, independent of
		// destructuring. Shares the same pattern-element grammar every other
		// destructuring position uses.
		if p.check(lexer.LBRACE) || p.check(lexer.LBRACKET) {
			patTok := p.peek()
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
			var pty *ast.TypeAnnotation
			if p.check(lexer.COLON) {
				p.advance()
				var err error
				pty, err = p.parseTypeAnnotation("ts")
				if err != nil {
					return nil, err
				}
			}
			// A missing annotation is allowed here: an un-annotated
			// destructured arrow parameter (`([k, v]) => ...`) has its type
			// supplied by contextual typing from the HOF call site — the
			// element type flows through the `hints []Type` channel into
			// emitArrowFunctionWithHints, which fills p.Type == nil params.
			var pdflt ast.Expression
			if !rest && p.match(lexer.ASSIGN) {
				d, err := p.parseAssignment()
				if err != nil {
					return nil, err
				}
				pdflt = d
			}
			// A rest pattern (`...[a, b]`) unpacks the collected array.
			syntheticName := fmt.Sprintf("__param%d", len(params))
			params = append(params, ast.Param{Name: syntheticName, Pos: ast.Pos{Line: patTok.Line, Col: patTok.Col}, Type: pty, Rest: rest, ArrayPattern: arrPat, ObjectPattern: objPat, Default: pdflt})
			p.match(lexer.COMMA)
			continue
		}

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
		if !rest && p.match(lexer.ASSIGN) {
			dflt, err = p.parseAssignment()
			if err != nil {
				return nil, err
			}
		}
		params = append(params, ast.Param{Name: nameTok.Literal, Pos: paramPos(rest, restTok, nameTok), Type: pty, Rest: rest, Default: dflt, Optional: optional})
		if rest {
			break // rest param must be last
		}
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

	// restricted production: no line terminator between the parameters and `=>`
	if t := p.peek(); t.Type == lexer.ARROW && t.HasPrecedingLineBreak() {
		return nil, p.errAt(t, diag.LineBreakBeforeArrow)
	}
	arrowTok, err := p.expect(lexer.ARROW)
	if err != nil {
		return nil, err
	}
	// An arrow's parameters may never repeat a name (an early error in
	// sloppy code too).
	if n := duplicateParamName(params); n != "" {
		return nil, p.errAt(arrowTok, diag.DuplicateParameter, n)
	}
	if isAsync {
		for _, prm := range params {
			if prm.Name == "await" {
				return nil, errAtPos(prm.Pos, diag.ReservedWordIdent, "await")
			}
			if aw := paramInitAwait(prm.Default); aw != nil {
				return nil, errAtPos(aw.GetPos(), diag.AwaitInParamInit)
			}
		}
	}

	// An async arrow's body awaits; any other keeps its context.
	if isAsync {
		savedAwait := p.awaitCtx
		p.awaitCtx = awaitAsync
		defer func() { p.awaitCtx = savedAwait }()
	}
	// Block body or expression body
	if p.check(lexer.LBRACE) {
		block, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		if bodyStartsWithUseStrict(block) && nonSimpleParams(params) {
			return nil, errAtPos(pos, diag.StrictNonSimpleParams)
		}
		arrow := ast.NewArrowFunction(params, retType, nil, block, pos)
		arrow.Src = p.srcRange(pos)
		p.bareArrow = arrow
		return arrow, nil
	}
	body, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	arrow := ast.NewArrowFunction(params, retType, body, nil, pos)
	arrow.Src = p.srcRange(pos)
	return arrow, nil
}

// destructuredArrowParamLookahead reports whether the LPAREN at the
// current position begins an arrow function whose first parameter is a
// destructuring pattern (`([a, b]) => ...`, `({x, y}: T) => ...`,
// `([a, b]: T[]) => ...`) — distinguished from a parenthesized object/array
// literal expression (`({a: 1})`, `([1, 2])`), which starts identically.
// The distinguishing signal is what follows the parameter list: an arrow
// (optionally past a `: returnType`). Scans to the RPAREN that closes the
// parameter list, tracking paren depth (nested parens in a type annotation
// or a `() => T` sub-type balance out, so paren depth alone is robust — the
// pattern brackets/braces never affect it), then checks the following token.
// Assumes p.peek() is LPAREN and peekNth(1) is LBRACE or LBRACKET. The
// pre-lexed token buffer (parser.go's peekNth) makes unbounded-distance
// lookahead cheap — no re-lexing, just array indexing. An un-annotated
// pattern param leaves its type to contextual typing from the HOF call site
// (emitArrowFunctionWithHints); see parseArrowFunction's pattern branch.
// parenGroupFollowedByArrow reports whether the parenthesized group starting
// at the current `(` is closed by a `)` immediately followed by `=>` or by a
// `:` return-type annotation — i.e. whether it is an arrow parameter list.
func (p *Parser) parenGroupFollowedByArrow() bool { return p.parenGroupFollowedByArrowAt(0) }

// parenGroupFollowedByArrowAt is parenGroupFollowedByArrow for a `(` at the
// k-th token ahead.
func (p *Parser) parenGroupFollowedByArrowAt(k int) bool {
	depth := 0
	for i := k; ; i++ {
		t := p.peekNth(i)
		switch t.Type {
		case lexer.EOF:
			return false
		case lexer.LPAREN, lexer.LBRACE, lexer.LBRACKET:
			depth++
		case lexer.RPAREN, lexer.RBRACE, lexer.RBRACKET:
			depth--
			if depth == 0 {
				next := p.peekNth(i + 1).Type
				return next == lexer.ARROW || next == lexer.COLON && p.typeThenArrowAt(i+2)
			}
		}
	}
}

// typeThenArrowAt reports whether a type parses from the k-th token ahead
// and `=>` follows it, as TypeScript decides an arrow's return annotation:
// by parsing it, then backtracking.
func (p *Parser) typeThenArrowAt(k int) bool {
	save := p.pos
	p.pos = save + k
	_, err := p.parseType()
	ok := err == nil && p.check(lexer.ARROW)
	p.rewind(save)
	return ok
}

func (p *Parser) destructuredArrowParamLookahead() bool {
	open := p.peekNth(1).Type
	closeType := lexer.RBRACE
	if open == lexer.LBRACKET {
		closeType = lexer.RBRACKET
	}
	// Phase 1 — find the first pattern's matching close brace/bracket.
	depth := 0
	n := 1
	for ; ; n++ {
		tok := p.peekNth(n)
		if tok.Type == lexer.EOF {
			return false
		}
		if tok.Type == open {
			depth++
		} else if tok.Type == closeType {
			depth--
			if depth == 0 {
				break
			}
		}
	}
	// An annotated pattern (`({x, y}: T)` / `([a, b]: T[])`): a ':' immediately
	// after the close bracket is unambiguous — a parenthesized object/array
	// literal never carries a ':' in that position.
	if p.peekNth(n+1).Type == lexer.COLON {
		return true
	}
	// Phase 2 — un-annotated pattern (`([a, b]) => ...`): scan to the ')' that
	// closes the parameter list and require '=>' directly after it. A trailing
	// ':' is deliberately NOT accepted here: it is ambiguous with a ternary's
	// ':' branch (`cond ? ({a: 1}) : alt`), where the parenthesized object/array
	// literal is followed by the conditional's colon. Only an unmistakable '=>'
	// marks an arrow. (A rare un-annotated pattern param that also carries an
	// explicit return type, `([a, b]): R => ...`, is not recognized — genuinely
	// ambiguous with the ternary form at this boundary; annotate the pattern
	// (`([a, b]: T): R =>`) to disambiguate, which Phase 1 catches.)
	parenDepth := 0
	for m := 0; ; m++ {
		tok := p.peekNth(m)
		switch tok.Type {
		case lexer.EOF:
			return false
		case lexer.LPAREN:
			parenDepth++
		case lexer.RPAREN:
			parenDepth--
			if parenDepth == 0 {
				return p.peekNth(m+1).Type == lexer.ARROW
			}
		}
	}
}

func (p *Parser) parseTemplateLiteral() (ast.Expression, error) {
	tok := p.advance() // consume TEMPLATE_HEAD
	pos := posOf(tok)
	quasis, exprs, err := p.parseTemplateRest(tok.Literal)
	if err != nil {
		return nil, err
	}
	return ast.NewTemplateLiteral(quasis, exprs, pos), nil
}

// parseTemplateRest scans the TEMPLATE_MIDDLE*/TEMPLATE_TAIL continuation of
// a template literal whose TEMPLATE_HEAD (literal text headQuasi) has
// already been consumed by the caller — shared by parseTemplateLiteral (a
// bare template literal) and parseCallMember's tagged-template case
// (TDD-00059), so the interleaved quasi/expression scan exists in one
// place rather than twice.
func (p *Parser) parseTemplateRest(headQuasi string) ([]string, []ast.Expression, error) {
	quasis, _, exprs, err := p.parseTemplateRestRaw(headQuasi, "")
	return quasis, exprs, err
}

// parseTemplateRestRaw is parseTemplateRest that also returns the raw
// (undecoded) source of each quasi, parallel to the cooked ones — used by the
// tagged-template path so String.raw can see the verbatim text (ADR-00562).
func (p *Parser) parseTemplateRestRaw(headQuasi, headRaw string) ([]string, []string, []ast.Expression, error) {
	quasis := []string{headQuasi}
	rawQuasis := []string{headRaw}
	var exprs []ast.Expression

	for {
		expr, err := p.parseAssignment()
		if err != nil {
			return nil, nil, nil, err
		}
		exprs = append(exprs, expr)

		next := p.peek()
		switch next.Type {
		case lexer.TEMPLATE_MIDDLE:
			quasis = append(quasis, next.Literal)
			rawQuasis = append(rawQuasis, next.Raw)
			p.advance()
		case lexer.TEMPLATE_TAIL:
			quasis = append(quasis, next.Literal)
			rawQuasis = append(rawQuasis, next.Raw)
			p.advance()
			return quasis, rawQuasis, exprs, nil
		default:
			return nil, nil, nil, p.errAt(next, diag.ExpectedTemplateCont, next.Type)
		}
	}
}

func (p *Parser) parseArgList() ([]ast.Expression, error) {
	var args []ast.Expression
	for !p.check(lexer.RPAREN) && !p.check(lexer.EOF) {
		var arg ast.Expression
		// A spread argument `f(...arr)` — parsed like an array-literal spread;
		// codegen restricts which positions it accepts (TDD-00106).
		if p.check(lexer.ELLIPSIS) {
			spreadTok := p.advance()
			inner, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			arg = ast.NewSpreadElement(inner, posOf(spreadTok))
		} else {
			var err error
			arg, err = p.parseAssignment()
			if err != nil {
				return nil, err
			}
		}
		args = append(args, arg)
		if !p.match(lexer.COMMA) {
			break
		}
	}
	return args, nil
}

// reservedWords are the ECMAScript reserved words the scanner reads as
// identifiers (the rest are keyword tokens): never an identifier reference.
var reservedWords = map[string]bool{"in": true, "with": true, "debugger": true, "delete": true, "enum": true}

func (p *Parser) parsePrimary() (ast.Expression, error) {
	tok := p.peekPrimary()

	switch tok.Type {
	case lexer.NUMBER:
		p.advance()
		return ast.NewNumberLiteral(tok.Literal, posOf(tok)), nil

	case lexer.BIGINT:
		p.advance()
		return ast.NewBigIntLiteral(tok.Literal, posOf(tok)), nil

	case lexer.STRING:
		p.advance()
		return ast.NewStringLiteral(tok.Literal, posOf(tok)), nil

	case lexer.TEMPLATE_NO_SUB:
		p.advance()
		return ast.NewTemplateLiteral([]string{tok.Literal}, nil, posOf(tok)), nil

	case lexer.TEMPLATE_HEAD:
		return p.parseTemplateLiteral()

	case lexer.REGEX:
		p.advance()
		// Desugars to the same node `new RegExp(pattern, flags)` produces —
		// codegen only ever has one shape to handle. See
		// ast.NewRegExpExpression's doc comment.
		return ast.NewNewRegExpExpression(
			ast.NewStringLiteral(tok.Literal, posOf(tok)),
			ast.NewStringLiteral(tok.RegexFlags, posOf(tok)),
			posOf(tok),
		), nil

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
		if p.asyncFunctionAhead() {
			return p.parseAsyncFunctionOrArrow(tok)
		}
		if reservedWords[tok.Literal] {
			// A reserved word the scanner leaves an identifier (`in`, `with`,
			// …) cannot be an identifier reference.
			return nil, p.errAt(tok, diag.ReservedWordIdent, tok.Literal)
		}
		p.advance()
		// Bare arrow function: x => expr  or  x => { ... }
		if t := p.peek(); t.Type == lexer.ARROW && t.HasPrecedingLineBreak() {
			return nil, p.errAt(t, diag.LineBreakBeforeArrow)
		}
		if p.check(lexer.ARROW) {
			p.advance() // consume '=>'
			pos := posOf(tok)
			params := []ast.Param{{Name: tok.Literal, Pos: ast.Pos{Line: tok.Line, Col: tok.Col}, Type: nil}}
			if p.check(lexer.LBRACE) {
				block, err := p.parseBlock()
				if err != nil {
					return nil, err
				}
				arrow := ast.NewArrowFunction(params, nil, nil, block, pos)
				arrow.Src = p.srcRange(pos)
				p.bareArrow = arrow
				return arrow, nil
			}
			body, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			arrow := ast.NewArrowFunction(params, nil, body, nil, pos)
			arrow.Src = p.srcRange(pos)
			return arrow, nil
		}
		return ast.NewIdentifier(tok.Literal, posOf(tok)), nil

	case lexer.LPAREN:
		// Detect arrow function: () => ..., (): T => ..., (name: type, ...) => ...,
		// (name) => ..., (name, name, ...) => ..., or a destructured first
		// parameter ({x,y}: T) => .../([a,b]: T[]) => ... — the last case
		// needs its own, less trivial lookahead (below) to tell it apart
		// from a parenthesized object/array literal expression like
		// ({a: 1}) or ([1, 2]), since both start identically with `({`/`([`.
		t1 := p.peekNth(1)
		isArrow := (t1.Type == lexer.RPAREN &&
			(p.peekNth(2).Type == lexer.ARROW || p.peekNth(2).Type == lexer.COLON)) ||
			(t1.Type == lexer.IDENT && p.peekNth(2).Type == lexer.COLON) ||
			(t1.Type == lexer.IDENT && p.peekNth(2).Type == lexer.RPAREN && p.peekNth(3).Type == lexer.ARROW) ||
			// `(x): T => …`: an untyped parameter and a return annotation,
			// not `c ? (x) : alt`.
			(t1.Type == lexer.IDENT && p.peekNth(2).Type == lexer.RPAREN && p.peekNth(3).Type == lexer.COLON && p.typeThenArrowAt(4)) ||
			// `(a, b) => …`, not the comma operator `(a, b)`.
			(t1.Type == lexer.IDENT && p.peekNth(2).Type == lexer.COMMA && p.parenGroupFollowedByArrow()) ||
			// Optional first parameter `(x?: T) => …`, `(x?) => …`, `(x?, y) => …`.
			(t1.Type == lexer.IDENT && p.peekNth(2).Type == lexer.QUESTION &&
				(p.peekNth(3).Type == lexer.COLON || p.peekNth(3).Type == lexer.RPAREN || p.peekNth(3).Type == lexer.COMMA)) ||
			// A parameter list starting with `...` is a rest parameter — a
			// parenthesized expression can never begin with `...`, so this is
			// unambiguously an arrow (`(...xs) => …`, `(a, ...xs) => …`).
			(t1.Type == lexer.ELLIPSIS) ||
			((t1.Type == lexer.LBRACE || t1.Type == lexer.LBRACKET) && p.destructuredArrowParamLookahead()) ||
			// A defaulted first parameter `(p = '!') => …` reads like a
			// parenthesized assignment until the matching `)` — an arrow only
			// when `=>` (or a `: T` return annotation) follows it.
			(t1.Type == lexer.IDENT && p.peekNth(2).Type == lexer.ASSIGN && p.parenGroupFollowedByArrow())
		if isArrow {
			return p.parseArrowFunction()
		}
		lparen := p.advance()
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		// The comma operator inside a parenthesized group: `(a, b, c)` evaluates
		// each operand left to right and yields the last. Each operand is a
		// single assignment expression (not a nested sequence), so the commas
		// here never conflict with call-argument/array-element commas, which are
		// parsed elsewhere one assignment apiece.
		if p.check(lexer.COMMA) {
			seq := []ast.Expression{expr}
			for p.check(lexer.COMMA) {
				p.advance() // consume ','
				next, err := p.parseAssignment()
				if err != nil {
					return nil, err
				}
				seq = append(seq, next)
			}
			expr = ast.NewSequenceExpression(seq, posOf(lparen))
		}
		if _, err := p.expect(lexer.RPAREN); err != nil {
			return nil, err
		}
		p.bareArrow = nil // `(() => {})()` calls the parenthesized arrow
		switch expr.(type) {
		case *ast.ObjectLiteral, *ast.ArrayLiteral:
			// `({}) = 1` is no destructuring pattern (parseAssignment).
			if p.parenLiterals == nil {
				p.parenLiterals = map[ast.Expression]bool{}
			}
			p.parenLiterals[expr] = true
		}
		// Parentheses end an optional chain: `(a?.b).c` reads `.c` off the
		// chain's *result*, where `a?.b.c` short-circuits as a whole.
		switch n := expr.(type) {
		case *ast.MemberExpression:
			n.ChainEnd = true
		case *ast.IndexExpression:
			n.ChainEnd = true
		case *ast.CallExpression:
			n.ChainEnd = true
		}
		return expr, nil

	case lexer.LBRACKET:
		return p.parseArrayLiteral()

	case lexer.LBRACE:
		return p.parseObjectLiteral()

	case lexer.NEW:
		return p.parseNew()

	case lexer.IMPORT:
		return p.parseImportExpr()

	case lexer.THIS:
		p.advance()
		return ast.NewThisExpression(posOf(tok)), nil

	case lexer.SUPER:
		p.advance()
		return ast.NewSuperExpression(posOf(tok)), nil

	case lexer.CLASS:
		// Class expression `class [Name] { ... }` (TDD-00063 Stage 4).
		// parseClassDecl consumes `class` itself and accepts an anonymous
		// class via its defaultName parameter (a placeholder here — the real
		// name comes from the LHS at rewrite time, since classes are nominal).
		// V1 is binding-position only: a codegen pre-pass rewrites a top-level
		// `const X = class {...}` into a class named X; anywhere else this node
		// reaches emitExpr and is cleanly rejected.
		cPos := posOf(tok)
		decl, err := p.parseClassDecl(false, "$ClassExpr")
		if err != nil {
			return nil, err
		}
		decl.Src = p.srcRange(cPos)
		return ast.NewClassExpression(decl, cPos), nil

	case lexer.FUNCTION:
		// Function expression: var f = function(x): T { return x; }
		// A named function expression (var f = function fact(n) { ... }) binds
		// its own name only inside its body, for self-reference/recursion —
		// wired up in codegen (emitFunctionExpression, TDD-00060/ADR-00178).
		p.advance() // consume 'function'
		fPos := posOf(tok)
		isGen := p.match(lexer.STAR) // generator expression (TDD-00096)
		var name string
		// Its own name is read in its own context: `function yield() {}`
		// inside a generator names a plain function.
		// `await` names one too, outside module code.
		savedGen, savedAwait := p.inGen, p.awaitCtx
		p.inGen, p.awaitCtx = isGen, awaitFn
		if p.check(lexer.IDENT) || p.check(lexer.AWAIT) && p.awaitCtx == awaitFn {
			name = p.advance().Literal
		}
		p.inGen, p.awaitCtx = savedGen, savedAwait
		p.nextGen = isGen
		fd, err := p.parseFunctionRest(name, false, false, false)
		if err != nil {
			return nil, err
		}
		eraseTypeParams(fd)
		fe := p.newFuncExpr(name, fd, false, fPos)
		fe.IsGenerator = isGen
		return fe, nil

	}

	return nil, p.errAt(tok, diag.UnexpectedTokenInExpr, tok.Type)
}

// parseAsyncFunctionOrArrow parses `async function (…) {…}` or an async arrow
// (`async (params) => …`, `async x => …`); the current token is `async`.
func (p *Parser) parseAsyncFunctionOrArrow(tok lexer.Token) (ast.Expression, error) {
	{
		p.advance() // consume 'async'
		if p.check(lexer.FUNCTION) {
			p.advance() // consume 'function'
			fPos := posOf(tok)
			isGen := p.match(lexer.STAR) // async generator expression (TDD-00096)
			var name string
			if p.check(lexer.IDENT) || p.check(lexer.AWAIT) {
				nt := p.advance()
				name = nt.Literal
				// Its name is bound in its own scope: never `await`, nor
				// `yield` for an async generator.
				if name == "await" || name == "yield" && isGen {
					return nil, p.errAt(nt, diag.ReservedWordIdent, name)
				}
			}
			p.nextGen = isGen
			fd, err := p.parseFunctionRest(name, true, false, false)
			if err != nil {
				return nil, err
			}
			eraseTypeParams(fd)
			fe := p.newFuncExpr(name, fd, true, fPos)
			fe.IsGenerator = isGen
			return fe, nil
		}
		if t := p.peek(); t.Type == lexer.IDENT && p.peekNth(1).Type == lexer.ARROW {
			// `async x => …`: one bare parameter.
			if p.peekNth(1).HasPrecedingLineBreak() {
				return nil, p.errAt(p.peekNth(1), diag.LineBreakBeforeArrow)
			}
			p.advance()
			p.advance() // =>
			savedAwait := p.awaitCtx
			p.awaitCtx = awaitAsync
			defer func() { p.awaitCtx = savedAwait }()
			params := []ast.Param{{Name: t.Literal, Pos: ast.Pos{Line: t.Line, Col: t.Col}}}
			var af *ast.ArrowFunction
			if p.check(lexer.LBRACE) {
				block, err := p.parseBlock()
				if err != nil {
					return nil, err
				}
				af = ast.NewArrowFunction(params, nil, nil, block, posOf(tok))
			} else {
				body, err := p.parseAssignment()
				if err != nil {
					return nil, err
				}
				af = ast.NewArrowFunction(params, nil, body, nil, posOf(tok))
			}
			af.Src = p.srcRange(posOf(tok))
			af.IsAsync = true
			return af, nil
		}
		p.nextAsyncArrow = true
		af, err := p.parseArrowFunction()
		if err != nil {
			return nil, err
		}
		af.IsAsync = true
		if af.Src.Text != nil {
			af.Src.Start = tok.Pos // its source text begins at `async`
		}
		return af, nil
	}
}

// genericArrowAhead reports whether the `<` at the current token opens a
// generic arrow's type parameters (`<T>(x: T) => x`, `<K, V extends C>(…) =>`)
// rather than a `<T>expr` assertion: a name, then a balanced `<…>`, then a
// parenthesized group followed by `=>` or a return type, as tsc reads a
// .ts file.
func (p *Parser) genericArrowAhead() bool {
	if p.peekNth(1).Type != lexer.IDENT {
		return false
	}
	switch p.peekNth(2).Type {
	case lexer.COMMA, lexer.GT, lexer.EXTENDS, lexer.ASSIGN:
	default:
		return false
	}
	depth := 0
	for i := 0; ; i++ {
		switch p.peekNth(i).Type {
		case lexer.EOF, lexer.SEMICOLON, lexer.LBRACE, lexer.RBRACE:
			return false
		case lexer.LT:
			depth++
		case lexer.GT:
			depth--
		case lexer.RSHIFT:
			depth -= 2
		case lexer.URSHIFT:
			depth -= 3
		}
		if depth <= 0 {
			return depth == 0 && p.peekNth(i+1).Type == lexer.LPAREN && p.parenGroupFollowedByArrowAt(i+1)
		}
	}
}

// parseGenericArrow parses `<T, …>(params) => body`. The type parameters are
// erased to any in the arrow's annotations, as a generic method's are
// (codegen types it as any); its type nodes keep them.
func (p *Parser) parseGenericArrow() (ast.Expression, error) {
	tps, err := p.parseTypeParameterNodes("generic arrow function")
	if err != nil {
		return nil, err
	}
	af, err := p.parseArrowFunction()
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, tp := range tps {
		set[tp.Name] = true
		af.TypeParams = append(af.TypeParams, tp.Name)
		var c, d *ast.TypeAnnotation
		if tp.Constraint != nil {
			c = ast.NodeAnnotation(tp.Constraint, "ts")
		}
		if tp.Default != nil {
			d = ast.NodeAnnotation(tp.Default, "ts")
		}
		af.TypeParamConstraints = append(af.TypeParamConstraints, c)
		af.TypeParamDefaults = append(af.TypeParamDefaults, d)
	}
	for i := range af.Params {
		ast.EraseTypeParams(af.Params[i].Type, set)
	}
	ast.EraseTypeParams(af.RetType, set)
	return af, nil
}

// eraseTypeParams erases a function's type parameters to any in its
// annotations, as a generic method's are: a generic function expression or
// object-literal method is typed as any in codegen; its type nodes keep them.
func eraseTypeParams(fd *ast.FunctionDeclaration) {
	if len(fd.TypeParams) == 0 {
		return
	}
	set := map[string]bool{}
	for _, tp := range fd.TypeParams {
		set[tp] = true
	}
	for i := range fd.Params {
		ast.EraseTypeParams(fd.Params[i].Type, set)
	}
	ast.EraseTypeParams(fd.ReturnType, set)
}

// newFuncExpr is the function expression fd was parsed for; a `this: T`
// parameter recorded against fd moves to it.
// newObjectMethod is an object literal's method, getter or setter.
func (p *Parser) newObjectMethod(fd *ast.FunctionDeclaration, isAsync bool, pos ast.Pos) *ast.FunctionExpression {
	fe := p.newFuncExpr("", fd, isAsync, pos)
	fe.Method = true
	return fe
}

func (p *Parser) newFuncExpr(name string, fd *ast.FunctionDeclaration, isAsync bool, pos ast.Pos) *ast.FunctionExpression {
	fe := ast.NewFunctionExpression(name, fd.Params, fd.ReturnType, fd.Body, isAsync, pos)
	fe.NamePos = fd.NamePos
	fe.TypeParams, fe.TypeParamConstraints, fe.TypeParamDefaults = fd.TypeParams, fd.TypeParamConstraints, fd.TypeParamDefaults
	fe.Src = p.srcRange(pos)
	if fd.Src.Text != nil && (fe.Src.Text == nil || fd.Src.Start < fe.Src.Start) {
		fe.Src = fd.Src // a method: its source begins at its modifiers and name
	}
	if ta, ok := p.thisParams[fd]; ok {
		delete(p.thisParams, fd)
		p.thisParams[fe] = ta
	}
	return fe
}

// propertyKeyText is the key a property name token names: a numeric
// literal's is its value as JavaScript writes it (`0x10` is "16").
func propertyKeyText(tok lexer.Token) string {
	if tok.Type == lexer.NUMBER {
		return ast.NumberKey(tok.Literal)
	}
	return tok.Literal
}

// paramPos is where a parameter is reported: its `...` for a rest, else
// its name.
func paramPos(rest bool, restTok, nameTok lexer.Token) ast.Pos {
	if rest {
		return ast.Pos{Line: restTok.Line, Col: restTok.Col}
	}
	return ast.Pos{Line: nameTok.Line, Col: nameTok.Col}
}

// paramInitAwait is an `await` a parameter default evaluates (not one in a
// function, arrow or class it holds).
func paramInitAwait(init ast.Expression) ast.Node {
	if init == nil {
		return nil
	}
	var found ast.Node
	ast.Inspect(init, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		switch n.(type) {
		case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ArrowFunction, *ast.ClassExpression, *ast.ClassDeclaration:
			return false
		case *ast.AwaitExpression:
			found = n
			return false
		}
		return true
	})
	return found
}
