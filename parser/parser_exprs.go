package parser

import (
	"KlainMainLang/ast"
	"KlainMainLang/lexer"
	"fmt"
)

// --- Expression parsing (precedence climbing) ---
//
// Precedence (low → high):
//   1  assignment  = += -= *= **= /= &= |= ^= <<= >>= >>>= &&= ||= ??=   (right-assoc)
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
//  12  ** (right-assoc)
//  13  unary prefix: ! ~ - + ++ --
//  14  postfix ++ --  then call/member chains

func (p *Parser) parseExpression() (ast.Expression, error) {
	return p.parseAssignment()
}

func (p *Parser) parseAssignment() (ast.Expression, error) {
	// `yield` (TDD-00061) binds at assignment precedence, same as real JS's
	// own grammar (`YieldExpression` is itself an `AssignmentExpression`
	// production, lower precedence than the ternary/logical/etc. chain
	// parseTernary below covers) — checked before descending into that
	// chain, not as one more case alongside it, since `yield`'s own operand
	// is a full AssignmentExpression, not something any higher-precedence
	// level here could parse as an operand of its own.
	if p.check(lexer.YIELD) {
		return p.parseYield()
	}

	left, err := p.parseTernary()
	if err != nil {
		return nil, err
	}

	// TypeScript type assertions (ADR-00371): `expr as T`, `expr as const`,
	// `expr satisfies T`. `as`/`satisfies` are contextual keywords (plain
	// IDENT tokens). Left-associative postfix, looser than the ternary chain.
	for p.check(lexer.IDENT) && (p.peek().Literal == "as" || p.peek().Literal == "satisfies") {
		kw := p.advance()
		// TypeScript type assertions are **erased**: `as T`, `as const`, and
		// `satisfies T` are all identity at runtime, and this compiler keeps the
		// expression's own inferred type rather than adopting the asserted one
		// (a sound reinterpret across differing representations isn't modeled
		// here). The syntax is consumed and dropped so real TS compiles; the
		// assertion has no static effect (ADR-00371).
		//
		// One carve-out: `as T` written directly on a call whose result is
		// otherwise `any` and whose emission consults a target type —
		// `JSON.parse(s) as Rec[]`, `res.json() as Rec` — is kept on the call
		// node (CallExpression.AssertedType), supplying the projection target
		// exactly as `const p: Rec[] = JSON.parse(s)` would. That matches the
		// assertion's real static effect in TypeScript (narrowing `any` to T).
		// `satisfies` never narrows in TS and stays fully erased.
		if kw.Literal == "as" && p.check(lexer.CONST) {
			p.advance() // `as const`
			continue
		}
		ta, terr := p.parseTypeAnnotation("as")
		if terr != nil {
			return nil, terr
		}
		if kw.Literal == "as" {
			if ce := assertableCall(left); ce != nil {
				ce.AssertedType = ta // chained `as A as B`: the outermost wins
			}
		}
	}

	switch p.peek().Type {
	case lexer.ASSIGN,
		lexer.PLUS_ASSIGN, lexer.MINUS_ASSIGN, lexer.STAR_ASSIGN, lexer.POW_ASSIGN, lexer.SLASH_ASSIGN, lexer.PERCENT_ASSIGN,
		lexer.AND_ASSIGN, lexer.OR_ASSIGN, lexer.XOR_ASSIGN,
		lexer.LSHIFT_ASSIGN, lexer.RSHIFT_ASSIGN, lexer.URSHIFT_ASSIGN,
		lexer.LOGICAL_AND_ASSIGN, lexer.LOGICAL_OR_ASSIGN, lexer.NULLISH_ASSIGN:
		opTok := p.advance()
		right, err := p.parseAssignment() // right-assoc
		if err != nil {
			return nil, err
		}
		return ast.NewAssignmentExpression(opTok.Literal, left, right, posOf(opTok)), nil
	}
	return left, nil
}

// parseYield parses `yield`, `yield expr`, and `yield* expr` (TDD-00061).
// No context check here for whether we're actually inside a generator
// function body — this compiler's parser doesn't track that kind of
// enclosing-function-kind state anywhere else either (the analogous "break
// outside a loop"/"this outside a method" checks are both done at codegen
// time, against the emitter's own per-function context, not here); codegen
// has no generator machinery at all yet regardless, so every YieldExpression
// reaching it is rejected uniformly (see emitFunctionDecl's IsGenerator
// check) rather than needing a parser-level distinction between "wrong
// context" and "not implemented" that codegen can give more precisely once
// real generator support exists.
func (p *Parser) parseYield() (ast.Expression, error) {
	tok := p.advance() // 'yield'
	pos := posOf(tok)
	delegate := p.match(lexer.STAR) // `yield*`

	// Same no-operand shape as a bare `return`/`break`/`continue`: a line
	// terminator right after `yield` (ASI) means no operand, and so does
	// any token that can't start an expression here (closing the
	// surrounding construct, or a comma/colon separating this yield from
	// whatever comes next) — `yield` can appear inside a call's argument
	// list or an array/object literal, unlike return/break/continue, which
	// are always a full statement on their own.
	if p.peek().Line != tok.Line {
		return ast.NewYieldExpression(nil, delegate, pos), nil
	}
	switch p.peek().Type {
	case lexer.SEMICOLON, lexer.RPAREN, lexer.RBRACKET, lexer.RBRACE, lexer.COMMA, lexer.COLON, lexer.EOF:
		return ast.NewYieldExpression(nil, delegate, pos), nil
	}

	arg, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	return ast.NewYieldExpression(arg, delegate, pos), nil
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
	// `in` (`key in obj`) sits at the same precedence as instanceof/</>/etc
	// in real JS grammar too. Deliberately NOT a reserved lexer.IN token —
	// "in" isn't reserved anywhere else in this compiler either (the for-in
	// loop already recognizes it the same contextual way, matching a plain
	// IDENT's literal text — parser_stmts.go's parseFor), so a variable or
	// field actually named "in" keeps working everywhere outside this one
	// operator position.
	for p.peek().Type == lexer.LT || p.peek().Type == lexer.GT ||
		p.peek().Type == lexer.LTE || p.peek().Type == lexer.GTE ||
		p.peek().Type == lexer.INSTANCEOF ||
		(p.peek().Type == lexer.IDENT && p.peek().Literal == "in") {
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
	left, err := p.parseExponentiation()
	if err != nil {
		return nil, err
	}
	for p.peek().Type == lexer.STAR || p.peek().Type == lexer.SLASH || p.peek().Type == lexer.PERCENT {
		op := p.advance()
		right, err := p.parseExponentiation()
		if err != nil {
			return nil, err
		}
		left = ast.NewBinaryExpression(op.Literal, left, right, posOf(op))
	}
	return left, nil
}

// parseExponentiation parses `a ** b`. `**` binds tighter than `* / %` and is
// right-associative (`2 ** 3 ** 2` === `2 ** (3 ** 2)` === 512), so the right
// operand recurses back here while the left is a single unary/postfix operand.
// JS makes an unparenthesized unary expression on the *left* a SyntaxError
// (`-2 ** 2` is ambiguous — parenthesize as `(-2) ** 2` or `-(2 ** 2)`); that
// early error is enforced here rather than silently picking one grouping.
func (p *Parser) parseExponentiation() (ast.Expression, error) {
	// The start token distinguishes an unparenthesized unary (`-2 ** 2`, a
	// SyntaxError) from a parenthesized one (`(-2) ** 2`, valid): both parse to
	// the same UnaryExpression node — parentheses aren't kept in the AST — so
	// only a leading `(` tells them apart. Prefix `++`/`--` are UpdateExpression
	// nodes, not UnaryExpression, and stay allowed on the left, matching JS.
	startTok := p.peek()
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	if p.peek().Type == lexer.POW {
		if u, ok := left.(*ast.UnaryExpression); ok && u.Prefix && startTok.Type != lexer.LPAREN {
			return nil, fmt.Errorf("%d:%d: unary operator '%s' before '**' is ambiguous — parenthesize as '(%s x) ** y' or '%s(x ** y)'", startTok.Line, startTok.Col, u.Op, u.Op, u.Op)
		}
		op := p.advance()
		right, err := p.parseExponentiation()
		if err != nil {
			return nil, err
		}
		left = ast.NewBinaryExpression("**", left, right, posOf(op))
	}
	return left, nil
}

func (p *Parser) parseUnary() (ast.Expression, error) {
	// `delete expr` (ADR-00487): `delete` is a reserved word in JS, so an
	// IDENT spelling it in prefix position is unambiguously the operator.
	if p.check(lexer.IDENT) && p.peek().Literal == "delete" {
		op := p.advance()
		arg, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return ast.NewUnaryExpression("delete", true, arg, posOf(op)), nil
	}

	// Old-style TS type assertion `<T>expr` (ADR-00451): a `<` can never
	// begin an expression otherwise, so this position is unambiguous in .ts
	// (no JSX here). Erased exactly like the postfix `expr as T` form
	// (ADR-00371) — the angle-bracketed type is parsed and dropped, and the
	// operand's own inferred type is kept.
	if p.check(lexer.LT) {
		p.advance() // consume '<'
		if _, err := p.parseTypeAnnotation("as"); err != nil {
			return nil, err
		}
		if err := p.expectGT("type assertion"); err != nil {
			return nil, err
		}
		return p.parseUnary()
	}
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

// expectPropertyName consumes the token right after a `.`/`?.` as a
// property name. Almost always a plain IDENT, but `default` is a reserved
// lexer keyword (lexer.DEFAULT — used by `switch`'s `default:` clause and,
// as of TDD-00042, as the synthetic name of an `export default`), so
// `ns.default` — the natural way to reach a default export through a
// namespace import — would otherwise be permanently unparseable. Narrowly
// scoped to just this one keyword rather than every reserved word as a
// general "any keyword can be a property name" fix (real JS/TS allow that
// broadly); widen later if another keyword-named property need shows up.
func (p *Parser) expectPropertyName() (lexer.Token, error) {
	if p.check(lexer.DEFAULT) {
		return p.advance(), nil
	}
	// A private name (`this.#x`, `obj.#x` — TDD-00021) is syntactically
	// valid in member-access position; whether it names a field the
	// enclosing class actually declares is a semantic check, not a parse
	// one (checkMemberVisibility, codegen/llvm/emit_classes.go).
	if p.check(lexer.PRIVATE_NAME) {
		return p.advance(), nil
	}
	// A reserved word is a valid property name after `.` (e.g. `promise.catch`,
	// `promise.finally`) — the keyword token carries the word as its Literal.
	if lexer.IsKeyword(p.peek().Type) {
		return p.advance(), nil
	}
	return p.expect(lexer.IDENT)
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
			propTok, err := p.expectPropertyName()
			if err != nil {
				return nil, err
			}
			mem := ast.NewMemberExpression(expr, propTok.Literal, posOf(propTok))
			mem.Optional = true
			expr = mem
		case lexer.DOT:
			p.advance()
			propTok, err := p.expectPropertyName()
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
		case lexer.LT:
			// Explicit call-site type arguments `f<string>(x)` (ADR-00473).
			// Ambiguous with comparison (`a < b > (c)`), resolved by
			// backtracking: only when a type-annotation list closes with `>`
			// and `(` follows is it a call; otherwise the `<` is left for
			// the binary-operator level. Only tried on an identifier/member
			// callee, mirroring TS's own disambiguation.
			if _, isIdent := expr.(*ast.Identifier); !isIdent {
				if _, isMem := expr.(*ast.MemberExpression); !isMem {
					return expr, nil
				}
			}
			save := p.pos
			p.advance() // '<'
			var targs []*ast.TypeAnnotation
			okParse := true
			for {
				ta, err := p.parseTypeAnnotation("ts")
				if err != nil {
					okParse = false
					break
				}
				targs = append(targs, ta)
				if !p.match(lexer.COMMA) {
					break
				}
			}
			if okParse {
				if err := p.expectGT("call-site type arguments"); err != nil {
					okParse = false
				}
			}
			if !okParse || !p.check(lexer.LPAREN) {
				p.pos = save
				return expr, nil
			}
			lparen := p.advance()
			args, err := p.parseArgList()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.RPAREN); err != nil {
				return nil, err
			}
			call := ast.NewCallExpression(expr, args, posOf(lparen))
			call.TypeArgs = targs
			expr = call
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
		case lexer.TEMPLATE_NO_SUB:
			// `` tag`plain text, no ${} `` — a tagged template with a
			// single quasi and no interpolated expressions.
			tok := p.advance()
			expr = ast.NewTaggedTemplateExpression(expr, []string{tok.Literal}, []string{tok.Raw}, nil, posOf(tok))
		case lexer.TEMPLATE_HEAD:
			tok := p.advance()
			pos := posOf(tok)
			quasis, rawQuasis, exprs, err := p.parseTemplateRestRaw(tok.Literal, tok.Raw)
			if err != nil {
				return nil, err
			}
			expr = ast.NewTaggedTemplateExpression(expr, quasis, rawQuasis, exprs, pos)
		default:
			return expr, nil
		}
	}
}

// assertableCall returns the CallExpression an `expr as T` assertion may
// attach its type to (CallExpression.AssertedType): a `JSON.parse(...)` call
// or a `.json()` method call — the call shapes whose result is otherwise
// `any` and whose emission consults a target type. `await` is looked
// through, since it is identity over these synchronous calls
// (`await res.json() as T`). Anything else returns nil and the assertion
// stays erased (ADR-00371). Whether the attachment is actually honored is
// decided at emission time (compat mode, receiver type); the parser only
// recognizes the shape.
func assertableCall(expr ast.Expression) *ast.CallExpression {
	if aw, ok := expr.(*ast.AwaitExpression); ok {
		expr = aw.Argument
	}
	ce, ok := expr.(*ast.CallExpression)
	if !ok {
		return nil
	}
	mem, ok := ce.Callee.(*ast.MemberExpression)
	if !ok {
		return nil
	}
	if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "JSON" && mem.Property == "parse" {
		return ce
	}
	if mem.Property == "json" {
		return ce
	}
	return nil
}
