package parser

import (
	"KlainMainLang/ast"
	"KlainMainLang/lexer"
	"fmt"
)

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
		if p.check(lexer.LBRACKET) {
			// Computed property key `{ [expr]: value }`.
			p.advance() // '['
			keyExpr, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.RBRACKET); err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.COLON); err != nil {
				return nil, err
			}
			val, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			props = append(props, ast.ObjectProperty{KeyExpr: keyExpr, Value: val})
			if !p.match(lexer.COMMA) {
				break
			}
			continue
		}
		// PropertyName: IDENT, or a STRING/NUMBER literal used as the key
		// text (`{ "foo": 1 }`, `{ 0: 'a' }`) — real JS/TS allow both,
		// only the identifier form supports shorthand.
		if !p.check(lexer.IDENT) && !p.check(lexer.STRING) && !p.check(lexer.NUMBER) {
			return nil, fmt.Errorf("%d:%d: expected property name, got %s", p.peek().Line, p.peek().Col, p.peek().Type)
		}
		keyTok := p.advance()
		var val ast.Expression
		if p.check(lexer.COLON) {
			p.advance() // ':'
			var err error
			val, err = p.parseAssignment()
			if err != nil {
				return nil, err
			}
		} else if keyTok.Type != lexer.IDENT {
			return nil, fmt.Errorf("%d:%d: expected :, got %s", p.peek().Line, p.peek().Col, p.peek().Type)
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
	case "EventEmitter":
		return p.parseNewEventEmitterBody(pos)
	case "Error":
		return p.parseNewErrorBody(pos, "Error")
	case "TypeError":
		return p.parseNewErrorBody(pos, "TypeError")
	case "RangeError":
		return p.parseNewErrorBody(pos, "RangeError")
	case "SyntaxError":
		return p.parseNewErrorBody(pos, "SyntaxError")
	case "EvalError":
		return p.parseNewErrorBody(pos, "EvalError")
	case "URIError":
		return p.parseNewErrorBody(pos, "URIError")
	case "ReferenceError":
		return p.parseNewErrorBody(pos, "ReferenceError")
	case "Date":
		return p.parseNewDateBody(pos)
	case "URL":
		return p.parseNewURLBody(pos)
	case "EventSource":
		return p.parseNewEventSourceBody(pos)
	case "WebSocket":
		return p.parseNewWebSocketBody(pos)
	case "URLSearchParams":
		return p.parseNewURLSearchParamsBody(pos)
	case "Headers":
		return p.parseNewHeadersBody(pos)
	case "Request":
		return p.parseNewRequestBody(pos)
	case "XMLHttpRequest":
		return p.parseNewXMLHttpRequestBody(pos)
	case "ArrayBuffer":
		return p.parseNewArrayBufferBody(pos)
	case "TextEncoder":
		return p.parseNewTextEncoderBody(pos)
	case "TextDecoder":
		return p.parseNewTextDecoderBody(pos)
	case "RegExp":
		return p.parseNewRegExpBody(pos)
	default:
		if elemKind, ok := typedArrayElemKinds[nameTok.Literal]; ok {
			return p.parseNewTypedArrayBody(pos, elemKind)
		}
		return p.parseNewGenericBody(pos)
	}
}

// typedArrayElemKinds maps each of the 8 supported TypedArray constructor
// names to the element-kind string codegen resolves into a concrete Type
// (see docs/tdd/TDD-00018.md) — the same lowercase names ResolveTypeName
// (codegen/llvm/types.go) already understands for JSDoc @type annotations.
// Uint8ClampedArray/BigInt64Array/BigUint64Array are deliberately absent —
// out of scope, see the TDD's "Deliberately out of scope" section.
var typedArrayElemKinds = map[string]string{
	"Int8Array":    "int8",
	"Uint8Array":   "uint8",
	"Int16Array":   "int16",
	"Uint16Array":  "uint16",
	"Int32Array":   "int32",
	"Uint32Array":  "uint32",
	"Float32Array": "float32",
	"Float64Array": "float64",
}

// parseNewGenericBody parses `new ClassName(args)` for anything that isn't
// one of the hardcoded builtin forms above. Codegen doesn't act on this
// yet (TDD-00009 Stage 1) — it's front-end groundwork only.
func (p *Parser) parseNewGenericBody(pos ast.Pos) (*ast.NewExpression, error) {
	nameTok := p.advance() // consume class name
	// Optional explicit `<T>` or `<K, V>` type argument list (TDD-00010 V1 /
	// TDD-00037 generic classes). Unlike a bare generic function call, `new`
	// unambiguously starts a constructor call, so this doesn't hit the
	// `a<b>(c)` grammar ambiguity that keeps explicit type arguments out of
	// V1 for plain calls — the same reasoning `new Map<K,V>()`/
	// `new Set<T>()` already rely on.
	var typeArgs []*ast.TypeAnnotation
	if p.check(lexer.LT) {
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
		if err := p.expectGT(nameTok.Literal + "<T>"); err != nil {
			return nil, err
		}
	}
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
	ne := ast.NewNewExpression(nameTok.Literal, args, pos)
	ne.TypeArgs = typeArgs
	return ne, nil
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

func (p *Parser) parseNewErrorBody(pos ast.Pos, kind string) (*ast.NewErrorExpression, error) {
	p.advance() // consume the constructor name ('Error', 'TypeError', ...)
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
	return ast.NewNewErrorExpression(kind, msg, pos), nil
}

func (p *Parser) parseNewURLBody(pos ast.Pos) (*ast.NewURLExpression, error) {
	p.advance() // consume 'URL'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	url, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewURLExpression(url, pos), nil
}

func (p *Parser) parseNewEventSourceBody(pos ast.Pos) (*ast.NewEventSourceExpression, error) {
	p.advance() // consume 'EventSource'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	url, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewEventSourceExpression(url, pos), nil
}

func (p *Parser) parseNewWebSocketBody(pos ast.Pos) (*ast.NewWebSocketExpression, error) {
	p.advance() // consume 'WebSocket'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	url, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewWebSocketExpression(url, pos), nil
}

func (p *Parser) parseNewURLSearchParamsBody(pos ast.Pos) (*ast.NewURLSearchParamsExpression, error) {
	p.advance() // consume 'URLSearchParams'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	var init ast.Expression
	if !p.check(lexer.RPAREN) {
		var err error
		init, err = p.parseAssignment()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewURLSearchParamsExpression(init, pos), nil
}

func (p *Parser) parseNewHeadersBody(pos ast.Pos) (*ast.NewHeadersExpression, error) {
	p.advance() // consume 'Headers'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	var init ast.Expression
	if !p.check(lexer.RPAREN) {
		var err error
		init, err = p.parseAssignment()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewHeadersExpression(init, pos), nil
}

func (p *Parser) parseNewRequestBody(pos ast.Pos) (*ast.NewRequestExpression, error) {
	p.advance() // consume 'Request'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	url, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	var init ast.Expression
	if p.match(lexer.COMMA) {
		init, err = p.parseAssignment()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewRequestExpression(url, init, pos), nil
}

func (p *Parser) parseNewXMLHttpRequestBody(pos ast.Pos) (*ast.NewXMLHttpRequestExpression, error) {
	p.advance() // consume 'XMLHttpRequest'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewXMLHttpRequestExpression(pos), nil
}

func (p *Parser) parseNewTextEncoderBody(pos ast.Pos) (*ast.NewTextEncoderExpression, error) {
	p.advance() // consume 'TextEncoder'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewTextEncoderExpression(pos), nil
}

func (p *Parser) parseNewTextDecoderBody(pos ast.Pos) (*ast.NewTextDecoderExpression, error) {
	p.advance() // consume 'TextDecoder'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	var label ast.Expression
	if !p.check(lexer.RPAREN) {
		var err error
		label, err = p.parseAssignment()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewTextDecoderExpression(label, pos), nil
}

func (p *Parser) parseNewRegExpBody(pos ast.Pos) (*ast.NewRegExpExpression, error) {
	p.advance() // consume 'RegExp'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	pattern, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	var flags ast.Expression
	if p.match(lexer.COMMA) {
		flags, err = p.parseAssignment()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewRegExpExpression(pattern, flags, pos), nil
}

func (p *Parser) parseNewArrayBufferBody(pos ast.Pos) (*ast.NewArrayBufferExpression, error) {
	p.advance() // consume 'ArrayBuffer'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	byteLength, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewArrayBufferExpression(byteLength, pos), nil
}

// parseNewTypedArrayBody parses `new Int8Array(...)`/.../`new
// Float64Array(...)` — a single argument whose meaning (size / existing
// ArrayBuffer / array-like to copy) is only knowable at codegen time, once
// static types are available (see docs/tdd/TDD-00018.md), so the parser
// just captures the one expression generically.
func (p *Parser) parseNewTypedArrayBody(pos ast.Pos, elemKind string) (*ast.NewTypedArrayExpression, error) {
	p.advance() // consume the constructor name (e.g. 'Uint8Array')
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	arg, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewTypedArrayExpression(elemKind, arg, pos), nil
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

func (p *Parser) parseNewEventEmitterBody(pos ast.Pos) (*ast.NewEventEmitterExpression, error) {
	p.advance() // consume 'EventEmitter'

	var payloadType *ast.TypeAnnotation
	if p.check(lexer.LT) {
		p.advance() // consume '<'
		var err error
		payloadType, err = p.parseTypeAnnotation("ts")
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
		return nil, fmt.Errorf("%d:%d: new EventEmitter() does not accept arguments", pos.Line, pos.Col)
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}

	return ast.NewNewEventEmitterExpression(payloadType, pos), nil
}

func (p *Parser) parseArrowFunction() (*ast.ArrowFunction, error) {
	tok := p.advance() // consume '('
	pos := posOf(tok)

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
		rest := p.match(lexer.ELLIPSIS)

		// Destructured parameter (`({x, y}: T) => ...` / `([a, b]: T[]) =>
		// ...`) — same restricted V1 shape parseParamList's own destructured
		// branch documents (no nesting, no per-field default, an explicit
		// type annotation always required); a destructured *array* param is
		// further rejected downstream in codegen (emit_func.go's
		// emitClosureFunc) since array-typed closure parameters aren't
		// supported at all yet, independent of destructuring. Arrow
		// functions parse their own parameter list separately from
		// parseParamList (used by named function declarations) rather than
		// sharing it, so this mirrors that function's pattern-parsing
		// branch rather than calling it directly.
		if p.check(lexer.LBRACE) || p.check(lexer.LBRACKET) {
			if rest {
				return nil, fmt.Errorf("%d:%d: a rest parameter cannot be a destructuring pattern", p.peek().Line, p.peek().Col)
			}
			isObject := p.check(lexer.LBRACE)
			var arrPat []string
			var objPat []ast.DestructProp
			p.advance() // consume '{' or '['
			if isObject {
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
					objPat = append(objPat, ast.DestructProp{Key: keyTok.Literal, Local: local})
					if !p.match(lexer.COMMA) {
						break
					}
				}
				if _, err := p.expect(lexer.RBRACE); err != nil {
					return nil, err
				}
			} else {
				for !p.check(lexer.RBRACKET) && !p.check(lexer.EOF) {
					if p.check(lexer.COMMA) {
						p.advance() // hole — consume comma, record skip
						arrPat = append(arrPat, "")
						continue
					}
					nameTok, err := p.expect(lexer.IDENT)
					if err != nil {
						return nil, err
					}
					arrPat = append(arrPat, nameTok.Literal)
					if !p.match(lexer.COMMA) {
						break
					}
				}
				if _, err := p.expect(lexer.RBRACKET); err != nil {
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
			if pty == nil {
				return nil, fmt.Errorf("%d:%d: a destructured parameter requires an explicit type annotation", p.peek().Line, p.peek().Col)
			}
			if p.check(lexer.ASSIGN) {
				return nil, fmt.Errorf("%d:%d: a default value on a destructured parameter is not yet supported", p.peek().Line, p.peek().Col)
			}
			syntheticName := fmt.Sprintf("__param%d", len(params))
			params = append(params, ast.Param{Name: syntheticName, Type: pty, ArrayPattern: arrPat, ObjectPattern: objPat})
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
		params = append(params, ast.Param{Name: nameTok.Literal, Type: pty, Rest: rest, Default: dflt, Optional: optional})
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

// destructuredArrowParamLookahead reports whether the LPAREN at the
// current position begins an arrow function whose first parameter is a
// destructuring pattern (`({x, y}: T) => ...` / `([a, b]: T[]) => ...`) —
// distinguished from a parenthesized object/array literal expression
// (`({a: 1})`, `([1, 2])`), which starts identically, by this compiler's
// own requirement that a destructured parameter always carries an explicit
// type annotation (see parseArrowFunction's pattern branch): scans forward
// to the matching close brace/bracket (tracking nesting depth, even though
// V1 patterns are themselves always flat — a cheap, robust check either
// way) and looks for a ':' immediately after it. Assumes p.peek() is
// LPAREN and peekNth(1) is LBRACE or LBRACKET. Pre-lexed token buffer
// (parser.go's peekNth) makes unbounded-distance lookahead cheap — no
// re-lexing, just array indexing.
func (p *Parser) destructuredArrowParamLookahead() bool {
	open := p.peekNth(1).Type
	closeType := lexer.RBRACE
	if open == lexer.LBRACKET {
		closeType = lexer.RBRACKET
	}
	depth := 0
	for n := 1; ; n++ {
		tok := p.peekNth(n)
		if tok.Type == lexer.EOF {
			return false
		}
		switch tok.Type {
		case open:
			depth++
		case closeType:
			depth--
			if depth == 0 {
				return p.peekNth(n+1).Type == lexer.COLON
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
	quasis := []string{headQuasi}
	var exprs []ast.Expression

	for {
		expr, err := p.parseAssignment()
		if err != nil {
			return nil, nil, err
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
			return quasis, exprs, nil
		default:
			return nil, nil, fmt.Errorf("%d:%d: expected template continuation, got %s", next.Line, next.Col, next.Type)
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

	case lexer.REGEX:
		p.advance()
		// Desugars to the same node `new RegExp(pattern, flags)` produces —
		// codegen only ever has one shape to handle. See
		// ast.NewRegExpExpression's doc comment.
		return ast.NewNewRegExpExpression(
			ast.NewStringLiteral(tok.Literal, posOf(tok)),
			ast.NewStringLiteral(tok.Flags, posOf(tok)),
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
			(t1.Type == lexer.IDENT && p.peekNth(2).Type == lexer.COMMA) ||
			((t1.Type == lexer.LBRACE || t1.Type == lexer.LBRACKET) && p.destructuredArrowParamLookahead())
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

	case lexer.IMPORT:
		return p.parseImportExpr()

	case lexer.THIS:
		p.advance()
		return ast.NewThisExpression(posOf(tok)), nil

	case lexer.SUPER:
		p.advance()
		return ast.NewSuperExpression(posOf(tok)), nil
	}

	return nil, fmt.Errorf("%d:%d: unexpected token %s in expression", tok.Line, tok.Col, tok.Type)
}
