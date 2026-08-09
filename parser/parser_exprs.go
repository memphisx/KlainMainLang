package parser

import (
	"KlainMainLang/ast"
	"KlainMainLang/lexer"
)

// --- Expression parsing (precedence climbing) ---
//
// Precedence (low → high):
//   1  assignment  = += -= *= /= &= |= ^= <<= >>= >>>= &&= ||= ??=   (right-assoc)
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
		lexer.PLUS_ASSIGN, lexer.MINUS_ASSIGN, lexer.STAR_ASSIGN, lexer.SLASH_ASSIGN, lexer.PERCENT_ASSIGN,
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
