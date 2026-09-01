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
		if p.check(lexer.COMMA) {
			// Elision (`[1,,3]`, `[,,]` — ADR-00467): a hole reads as
			// `undefined` (the element type's zero value here) and counts
			// toward the length, matching JS. Desugars to the same
			// undefined literal an explicit `undefined` element uses; the
			// comma is consumed by the shared loop tail below.
			elems = append(elems, ast.NewNullLiteral(true, posOf(p.peek())))
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
			// A well-known symbol key (`[Symbol.asyncIterator]` /
			// `[Symbol.iterator]`) desugars to a reserved *static* key
			// (`@@asyncIterator` / `@@iterator`) — same trick the class-member
			// grammar uses — so the literal stays a static struct (a closure-
			// typed field) instead of collapsing to a dynamic Map. Both the
			// `[Symbol.x]: fn` and the `[Symbol.x]() {...}` method-shorthand
			// forms are accepted.
			if wk, wkOk := wellKnownSymbolMemberName(keyExpr); wkOk {
				var val ast.Expression
				if p.check(lexer.LPAREN) {
					fnPos := posOf(p.peek())
					fd, err := p.parseFunctionRest("", false, false, false)
					if err != nil {
						return nil, err
					}
					val = ast.NewFunctionExpression("", fd.Params, fd.ReturnType, fd.Body, false, fnPos)
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
		// Accessor property `{ get x() {...} }` / `{ set x(v) {...} }`
		// (TDD-00153). Contextual, exactly like the class-member accessor
		// grammar: a bare `get`/`set` followed by another member name commits to
		// accessor parsing; `get: 1`, `get() {}`, `get` (shorthand) keep working
		// because the 2-token lookahead requires an IDENT name to follow.
		if p.peek().Type == lexer.IDENT && (p.peek().Literal == "get" || p.peek().Literal == "set") &&
			p.peekNth(1).Type == lexer.IDENT {
			accessorKind := p.advance().Literal
			nameTok := p.advance()
			fnPos := posOf(p.peek())
			fd, err := p.parseFunctionRest("", false, false, false)
			if err != nil {
				return nil, err
			}
			fnVal := ast.NewFunctionExpression("", fd.Params, fd.ReturnType, fd.Body, false, fnPos)
			props = append(props, ast.ObjectProperty{Key: nameTok.Literal, Value: fnVal, AccessorKind: accessorKind})
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
		if p.check(lexer.LPAREN) {
			// Method shorthand `{ foo() { ... } }` — sugar for `{ foo:
			// function() { ... } }`, reusing the same parseFunctionRest tail
			// a class method/named function declaration already shares, and
			// the same FunctionExpression value a `key: function(){}` field
			// already works with. V1 scope matches this compiler's own class
			// methods: no `async`/generator method shorthand (neither is
			// supported for class methods either — a separate, larger gap).
			fnPos := posOf(p.peek())
			fd, err := p.parseFunctionRest("", false, false, false)
			if err != nil {
				return nil, err
			}
			val = ast.NewFunctionExpression("", fd.Params, fd.ReturnType, fd.Body, false, fnPos)
		} else if p.check(lexer.COLON) {
			p.advance() // ':'
			var err error
			val, err = p.parseAssignment()
			if err != nil {
				return nil, err
			}
		} else if keyTok.Type != lexer.IDENT {
			return nil, fmt.Errorf("%d:%d: expected :, got %s", p.peek().Line, p.peek().Col, p.peek().Type)
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
	// Qualified constructor: `new mod.Class(...)` (`new stream.Readable(...)`,
	// `new http.ClientRequest(...)` — the standard Node namespace-import shape).
	// Consume the qualifier segments and dispatch on the final class name
	// through the same switch below, so `new stream.Readable(opts)` behaves
	// exactly like `new Readable(opts)`. The qualifier is validated only
	// syntactically; a final name no case matches falls to parseNewGenericBody
	// and gets codegen's per-class rejection rather than a parse error.
	for p.peekNth(1).Type == lexer.DOT && p.peekNth(2).Type == lexer.IDENT {
		p.advance() // qualifier ident
		p.advance() // '.'
		nameTok = p.peek()
	}
	switch nameTok.Literal {
	case "Array":
		return p.parseNewArrayBody(pos)
	case "Map":
		return p.parseNewMapBody(pos)
	case "Set":
		return p.parseNewSetBody(pos)
	case "WeakMap":
		return p.parseNewWeakMapBody(pos)
	case "WeakSet":
		return p.parseNewWeakSetBody(pos)
	case "WeakRef":
		return p.parseNewWeakRefBody(pos)
	case "EventEmitter":
		return p.parseNewEventEmitterBody(pos)
	case "ReadableStream":
		return p.parseNewReadableStreamBody(pos)
	case "WritableStream":
		return p.parseNewWritableStreamBody(pos)
	case "TransformStream":
		return p.parseNewTransformStreamBody(pos)
	case "Readable":
		return p.parseNewNodeStreamBody(pos, "readable")
	case "Writable":
		return p.parseNewNodeStreamBody(pos, "writable")
	case "Transform":
		return p.parseNewNodeStreamBody(pos, "transform")
	case "PassThrough":
		return p.parseNewNodeStreamBody(pos, "passthrough")
	case "Duplex":
		return p.parseNewNodeStreamBody(pos, "duplex")
	case "Agent":
		return p.parseNewAgentBody(pos)
	case "Webview":
		return p.parseNewWebviewBody(pos)
	case "DatabaseSync":
		return p.parseNewDatabaseSyncBody(pos)
	case "CompressionStream":
		return p.parseNewCompressionStreamBody(pos, false)
	case "DecompressionStream":
		return p.parseNewCompressionStreamBody(pos, true)
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
	case "DOMException":
		return p.parseNewDOMExceptionBody(pos)
	case "AggregateError":
		return p.parseNewAggregateErrorBody(pos)
	case "Date":
		return p.parseNewDateBody(pos)
	case "URL":
		return p.parseNewURLBody(pos)
	case "EventSource":
		return p.parseNewEventSourceBody(pos)
	case "EventTarget":
		return p.parseNewEventTargetBody(pos)
	case "AbortController":
		return p.parseNewAbortControllerBody(pos)
	case "Event":
		return p.parseNewEventBody(pos)
	case "CustomEvent":
		return p.parseNewCustomEventBody(pos)
	case "WebSocket":
		return p.parseNewWebSocketBody(pos)
	case "Worker":
		return p.parseNewWorkerBody(pos)
	case "URLSearchParams":
		return p.parseNewURLSearchParamsBody(pos)
	case "URLPattern":
		return p.parseNewURLPatternBody(pos)
	case "Headers":
		return p.parseNewHeadersBody(pos)
	case "Request":
		return p.parseNewRequestBody(pos)
	case "XMLHttpRequest":
		return p.parseNewXMLHttpRequestBody(pos)
	case "ArrayBuffer", "SharedArrayBuffer":
		return p.parseNewArrayBufferBody(pos)
	case "BroadcastChannel":
		return p.parseNewBroadcastChannelBody(pos)
	case "MessageChannel":
		return p.parseNewMessageChannelBody(pos)
	case "Channel":
		return p.parseNewChannelBody(pos)
	case "DataView":
		return p.parseNewDataViewBody(pos)
	case "TextEncoder":
		return p.parseNewTextEncoderBody(pos)
	case "TextDecoder":
		return p.parseNewTextDecoderBody(pos)
	case "RegExp":
		return p.parseNewRegExpBody(pos)
	case "Blob":
		return p.parseNewBlobBody(pos)
	default:
		if elemKind, ok := typedArrayElemKinds[nameTok.Literal]; ok {
			return p.parseNewTypedArrayBody(pos, elemKind)
		}
		return p.parseNewGenericBody(pos)
	}
}

// typedArrayElemKinds maps each of the 11 supported TypedArray constructor
// names to the element-kind string codegen resolves into a concrete Type
// (see docs/tdd/TDD-00018.md, and TDD-00101 for the BigInt64Array/
// BigUint64Array/Uint8ClampedArray additions) — the same lowercase names
// ResolveTypeName (codegen/llvm/types.go) already understands for JSDoc
// @type annotations.
var typedArrayElemKinds = map[string]string{
	"Int8Array":         "int8",
	"Uint8Array":        "uint8",
	"Uint8ClampedArray": "uint8clamped",
	"Int16Array":        "int16",
	"Uint16Array":       "uint16",
	"Int32Array":        "int32",
	"Uint32Array":       "uint32",
	"Float32Array":      "float32",
	"Float64Array":      "float64",
	"BigInt64Array":     "bigint64",
	"BigUint64Array":    "biguint64",
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

// parseNewDOMExceptionBody parses `new DOMException(message?, name?)` — unlike
// the fixed-name Error kinds, its runtime `.name` is the optional second
// argument (defaulting to "Error"), so the parsed name expression rides on the
// node's Name field (TDD-00081).
func (p *Parser) parseNewDOMExceptionBody(pos ast.Pos) (*ast.NewErrorExpression, error) {
	p.advance() // consume 'DOMException'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	var msg, name ast.Expression
	if !p.check(lexer.RPAREN) {
		var err error
		if msg, err = p.parseAssignment(); err != nil {
			return nil, err
		}
		if p.check(lexer.COMMA) {
			p.advance()
			if name, err = p.parseAssignment(); err != nil {
				return nil, err
			}
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	ne := ast.NewNewErrorExpression("DOMException", msg, pos)
	ne.Name = name
	return ne, nil
}

// parseNewAggregateErrorBody parses `new AggregateError(errors, message?)` — the
// first argument is the aggregated errors (an array), the optional second is the
// message. Mirrors real JS's `AggregateError(errors, message?)` signature; also
// what `Promise.any` throws on all-reject (TDD-00083).
func (p *Parser) parseNewAggregateErrorBody(pos ast.Pos) (*ast.NewErrorExpression, error) {
	p.advance() // consume 'AggregateError'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	var errors, msg ast.Expression
	if !p.check(lexer.RPAREN) {
		var err error
		if errors, err = p.parseAssignment(); err != nil {
			return nil, err
		}
		if p.check(lexer.COMMA) {
			p.advance()
			if msg, err = p.parseAssignment(); err != nil {
				return nil, err
			}
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	ne := ast.NewNewErrorExpression("AggregateError", msg, pos)
	ne.Errors = errors
	return ne, nil
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
	var base ast.Expression
	if p.peek().Type == lexer.COMMA {
		p.advance() // consume ','
		base, err = p.parseAssignment()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	if base != nil {
		return ast.NewNewURLExpressionWithBase(url, base, pos), nil
	}
	return ast.NewNewURLExpression(url, pos), nil
}

// parseNewDatabaseSyncBody parses `new DatabaseSync(path, options?)` from
// node:sqlite (ADR-00540): a required path expression and an optional
// options-object literal, validated in codegen.
func (p *Parser) parseNewDatabaseSyncBody(pos ast.Pos) (*ast.NewDatabaseSyncExpression, error) {
	p.advance() // consume 'DatabaseSync'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	path, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	var options ast.Expression
	if p.check(lexer.COMMA) {
		p.advance()
		options, err = p.parseAssignment()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewDatabaseSyncExpression(path, options, pos), nil
}

// parseNewAbortControllerBody parses `new AbortController()` (TDD-00081 Stage 3).
func (p *Parser) parseNewAbortControllerBody(pos ast.Pos) (*ast.NewAbortControllerExpression, error) {
	p.advance() // consume 'AbortController'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewAbortControllerExpression(pos), nil
}

// parseNewEventTargetBody parses `new EventTarget()` (TDD-00081 Stage 2).
func (p *Parser) parseNewEventTargetBody(pos ast.Pos) (*ast.NewEventTargetExpression, error) {
	p.advance() // consume 'EventTarget'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewEventTargetExpression(pos), nil
}

// parseNewAgentBody parses `new Agent(options?)` / `new http.Agent(options?)`.
func (p *Parser) parseNewAgentBody(pos ast.Pos) (*ast.NewHTTPAgentExpression, error) {
	p.advance() // consume 'Agent'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	var options ast.Expression
	if !p.check(lexer.RPAREN) {
		var err error
		options, err = p.parseAssignment()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewHTTPAgentExpression(options, pos), nil
}

// parseNewWebviewBody parses `new Webview(options?)` (TDD-00142) — the system
// webview window. Options is an optional `{ title, width, height, debug }`
// object literal, validated in codegen.
func (p *Parser) parseNewWebviewBody(pos ast.Pos) (*ast.NewWebviewExpression, error) {
	p.advance() // consume 'Webview'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	var options ast.Expression
	if !p.check(lexer.RPAREN) {
		var err error
		options, err = p.parseAssignment()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewWebviewExpression(options, pos), nil
}

// parseNewEventBody parses `new Event(type)` (TDD-00081). A second init argument
// (`{ cancelable, bubbles }`) is accepted but ignored in V1.
func (p *Parser) parseNewEventBody(pos ast.Pos) (*ast.NewEventExpression, error) {
	p.advance() // consume 'Event'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	typeArg, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	var cancelable ast.Expression
	if p.match(lexer.COMMA) {
		initExpr, err := p.parseAssignment()
		if err != nil {
			return nil, err
		}
		if obj, ok := initExpr.(*ast.ObjectLiteral); ok {
			for _, prop := range obj.Properties {
				if prop.Key == "cancelable" {
					cancelable = prop.Value
				}
			}
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	if cancelable != nil {
		return ast.NewNewEventExpressionWithInit(typeArg, cancelable, pos), nil
	}
	return ast.NewNewEventExpression(typeArg, pos), nil
}

// parseNewCustomEventBody parses `new CustomEvent(type, { detail })` (TDD-00081).
// The `detail` property is pulled from the init object literal at parse time; any
// other init properties are accepted but ignored in V1.
func (p *Parser) parseNewCustomEventBody(pos ast.Pos) (*ast.NewCustomEventExpression, error) {
	p.advance() // consume 'CustomEvent'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	typeArg, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	var detail, cancelable ast.Expression
	if p.match(lexer.COMMA) {
		initExpr, err := p.parseAssignment()
		if err != nil {
			return nil, err
		}
		if obj, ok := initExpr.(*ast.ObjectLiteral); ok {
			for _, prop := range obj.Properties {
				if prop.Key == "detail" {
					detail = prop.Value
				}
				if prop.Key == "cancelable" {
					cancelable = prop.Value
				}
			}
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	if cancelable != nil {
		return ast.NewNewCustomEventExpressionWithInit(typeArg, detail, cancelable, pos), nil
	}
	return ast.NewNewCustomEventExpression(typeArg, detail, pos), nil
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

// parseNewWorkerBody parses `new Worker('./file.ts')` and
// `new Worker('./file.ts', { workerData: expr })` (TDD-00098). The path must
// be a string literal — the worker file is compiled into the same binary, so
// a runtime-computed path has nothing to load. Only the `workerData`
// property is recognized in the options object.
func (p *Parser) parseNewWorkerBody(pos ast.Pos) (*ast.NewWorkerExpression, error) {
	p.advance() // consume 'Worker'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	pathTok := p.peek()
	if pathTok.Type != lexer.STRING {
		return nil, fmt.Errorf("%d:%d: new Worker(...) requires a compile-time string-literal path — the worker file is compiled into the binary, so a runtime-computed path cannot be loaded", pathTok.Line, pathTok.Col)
	}
	p.advance()
	var workerData ast.Expression
	if p.check(lexer.COMMA) {
		p.advance()
		obj, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		lit, ok := obj.(*ast.ObjectLiteral)
		if !ok {
			return nil, fmt.Errorf("%d:%d: new Worker's second argument must be an object literal (e.g. { workerData: ... })", pos.Line, pos.Col)
		}
		for _, prop := range lit.Properties {
			if prop.Key != "workerData" {
				return nil, fmt.Errorf("%d:%d: new Worker options: only 'workerData' is supported (found '%s')", pos.Line, pos.Col, prop.Key)
			}
			workerData = prop.Value
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	p.workerPaths = append(p.workerPaths, pathTok.Literal)
	return ast.NewNewWorkerExpression(pathTok.Literal, workerData, pos), nil
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

// parseNewURLPatternBody parses `new URLPattern()` / `new URLPattern(init)`
// (TDD-00100). The init's object-literal shape is validated by codegen, which
// owns the supported-component list; a second (baseURL) argument is rejected
// here since it's a constructor-form scope cut, not a component question.
func (p *Parser) parseNewURLPatternBody(pos ast.Pos) (*ast.NewURLPatternExpression, error) {
	p.advance() // consume 'URLPattern'
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
		if p.check(lexer.COMMA) {
			return nil, fmt.Errorf("%d:%d: new URLPattern does not take a baseURL second argument (single object-init form only)", pos.Line, pos.Col)
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewURLPatternExpression(init, pos), nil
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

// parseNewDataViewBody parses `new DataView(buffer, byteOffset?, byteLength?)`.
func (p *Parser) parseNewDataViewBody(pos ast.Pos) (*ast.NewDataViewExpression, error) {
	p.advance() // consume 'DataView'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	buffer, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	var byteOffset, byteLength ast.Expression
	if p.match(lexer.COMMA) {
		byteOffset, err = p.parseAssignment()
		if err != nil {
			return nil, err
		}
		if p.match(lexer.COMMA) {
			byteLength, err = p.parseAssignment()
			if err != nil {
				return nil, err
			}
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewDataViewExpression(buffer, byteOffset, byteLength, pos), nil
}

// parseNewBlobBody parses `new Blob(parts?, options?)` (TDD-00102).
func (p *Parser) parseNewBlobBody(pos ast.Pos) (*ast.NewBlobExpression, error) {
	p.advance() // consume 'Blob'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	var parts, options ast.Expression
	var err error
	if !p.check(lexer.RPAREN) {
		if parts, err = p.parseAssignment(); err != nil {
			return nil, err
		}
		if p.match(lexer.COMMA) {
			if options, err = p.parseAssignment(); err != nil {
				return nil, err
			}
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewBlobExpression(parts, options, pos), nil
}

func (p *Parser) parseNewArrayBufferBody(pos ast.Pos) (*ast.NewArrayBufferExpression, error) {
	shared := p.peek().Literal == "SharedArrayBuffer"
	p.advance() // consume 'ArrayBuffer' / 'SharedArrayBuffer'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	byteLength, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	// Optional options literal: `{maxByteLength: m}` marks the buffer
	// growable (ADR-00494).
	var maxByteLength ast.Expression
	if p.peek().Type == lexer.COMMA {
		p.advance()
		lit, err := p.parseObjectLiteral()
		if err != nil {
			return nil, err
		}
		if len(lit.Properties) != 1 || lit.Properties[0].Key != "maxByteLength" {
			return nil, fmt.Errorf("%d:%d: the buffer options literal supports exactly {maxByteLength: n}", pos.Line, pos.Col)
		}
		maxByteLength = lit.Properties[0].Value
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	ex := ast.NewNewArrayBufferExpression(byteLength, pos)
	ex.Shared = shared
	ex.MaxByteLength = maxByteLength
	return ex, nil
}

// parseNewBroadcastChannelBody parses `new BroadcastChannel('name')` — the
// name must be a string literal (it keys the compile-time channel-type
// registry; TDD-00099).
func (p *Parser) parseNewBroadcastChannelBody(pos ast.Pos) (*ast.NewBroadcastChannelExpression, error) {
	p.advance() // consume 'BroadcastChannel'
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	nameTok, err := p.expect(lexer.STRING)
	if err != nil {
		return nil, fmt.Errorf("%d:%d: new BroadcastChannel(...) requires a string-literal channel name", pos.Line, pos.Col)
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewBroadcastChannelExpression(nameTok.Literal, pos), nil
}

// parseNewChannelBody parses `new Channel<T>(capacity?)` (TDD-00143) — a
// klain:sync CSP channel. The optional <T> gives the element type; the
// optional capacity argument gives the buffer size (0/unbuffered by default).
func (p *Parser) parseNewChannelBody(pos ast.Pos) (*ast.NewChannelExpression, error) {
	p.advance() // consume 'Channel'
	var typeArg *ast.TypeAnnotation
	if p.check(lexer.LT) {
		p.advance() // consume '<'
		arg, err := p.parseTypeAnnotation("ts")
		if err != nil {
			return nil, err
		}
		typeArg = arg
		if err := p.expectGT("Channel<T>"); err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	var capacity ast.Expression
	if !p.check(lexer.RPAREN) {
		cap, err := p.parseAssignment()
		if err != nil {
			return nil, err
		}
		capacity = cap
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewChannelExpression(typeArg, capacity, pos), nil
}

// parseNewMessageChannelBody parses `new MessageChannel<T>()` (TDD-00099).
func (p *Parser) parseNewMessageChannelBody(pos ast.Pos) (*ast.NewMessageChannelExpression, error) {
	p.advance() // consume 'MessageChannel'
	var typeArg *ast.TypeAnnotation
	if p.check(lexer.LT) {
		p.advance() // consume '<'
		arg, err := p.parseTypeAnnotation("ts")
		if err != nil {
			return nil, err
		}
		typeArg = arg
		if err := p.expectGT("MessageChannel<T>"); err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewMessageChannelExpression(typeArg, pos), nil
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
	nta := ast.NewNewTypedArrayExpression(elemKind, arg, pos)
	// Optional 2nd/3rd arguments: the sub-range view form
	// `new XArray(buffer, byteOffset, length?)`.
	if p.check(lexer.COMMA) {
		p.advance()
		if nta.ByteOffset, err = p.parseAssignment(); err != nil {
			return nil, err
		}
		if p.check(lexer.COMMA) {
			p.advance()
			if nta.Length, err = p.parseAssignment(); err != nil {
				return nil, err
			}
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return nta, nil
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
	// Zero-arg `new Array<T>()` is an empty array (ADR-00463) — size 0.
	var size ast.Expression = ast.NewNumberLiteral("0", pos)
	if !p.check(lexer.RPAREN) {
		var err error
		size, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
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
	// Optional initial-entries argument (`new Map([[k, v], ...])`) — an
	// iterable of [K, V] pairs per the real spec, narrowed here to "an array
	// of 2-tuples" (this compiler's own array + tuple machinery, TDD-00066,
	// is the only iterable/pair concept it has for a general expression here).
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

	return ast.NewNewMapExpression(keyType, valType, init, pos), nil
}

func (p *Parser) parseNewWeakMapBody(pos ast.Pos) (*ast.NewWeakMapExpression, error) {
	p.advance() // consume 'WeakMap'
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
	// WeakMap's initial-entries argument is out of V1 scope (an iterable of
	// [obj, V] pairs) — a bare `new WeakMap()` only.
	if !p.check(lexer.RPAREN) {
		return nil, fmt.Errorf("%d:%d: new WeakMap() does not accept arguments", pos.Line, pos.Col)
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewWeakMapExpression(keyType, valType, pos), nil
}

func (p *Parser) parseNewWeakSetBody(pos ast.Pos) (*ast.NewWeakSetExpression, error) {
	p.advance() // consume 'WeakSet'
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
		return nil, fmt.Errorf("%d:%d: new WeakSet() does not accept arguments", pos.Line, pos.Col)
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewWeakSetExpression(elemType, pos), nil
}

func (p *Parser) parseNewWeakRefBody(pos ast.Pos) (*ast.NewWeakRefExpression, error) {
	p.advance() // consume 'WeakRef'
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
	// The referent argument is required.
	init, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewWeakRefExpression(elemType, init, pos), nil
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
	// Optional initial-elements argument (`new Set([1, 2, 3])`) — an
	// iterable per the real spec, narrowed here to "an array expression"
	// (this compiler's own array/HOF machinery is the only iterable
	// concept it has for a general expression in this position).
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

	return ast.NewNewSetExpression(elemType, init, pos), nil
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

// parseNewReadableStreamBody parses `new ReadableStream<T>(source?, strategy?)`
// (TDD-00097 Stage 1). Both arguments are ordinary expressions here — codegen
// validates that the source is an object literal and destructures it.
func (p *Parser) parseNewWritableStreamBody(pos ast.Pos) (*ast.NewWritableStreamExpression, error) {
	chunkType, sink, strategy, err := p.parseNewStreamArgs()
	if err != nil {
		return nil, err
	}
	return ast.NewNewWritableStreamExpression(chunkType, sink, strategy, pos), nil
}

func (p *Parser) parseNewReadableStreamBody(pos ast.Pos) (*ast.NewReadableStreamExpression, error) {
	chunkType, source, strategy, err := p.parseNewStreamArgs()
	if err != nil {
		return nil, err
	}
	return ast.NewNewReadableStreamExpression(chunkType, source, strategy, pos), nil
}

// parseNewTransformStreamBody parses `new TransformStream<I, O>(transformer?,
// writableStrategy?, readableStrategy?)` (TDD-00097 Stage 3).
func (p *Parser) parseNewTransformStreamBody(pos ast.Pos) (*ast.NewTransformStreamExpression, error) {
	p.advance() // consume 'TransformStream'
	var inTy, outTy *ast.TypeAnnotation
	if p.check(lexer.LT) {
		p.advance()
		var err error
		inTy, err = p.parseTypeAnnotation("ts")
		if err != nil {
			return nil, err
		}
		if p.match(lexer.COMMA) {
			outTy, err = p.parseTypeAnnotation("ts")
			if err != nil {
				return nil, err
			}
		} else {
			outTy = inTy
		}
		if _, err := p.expect(lexer.GT); err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	var exprs []ast.Expression
	for !p.check(lexer.RPAREN) && !p.check(lexer.EOF) {
		ex, err := p.parseAssignment()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, ex)
		if !p.match(lexer.COMMA) {
			break
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	if len(exprs) > 3 {
		return nil, fmt.Errorf("%d:%d: new TransformStream takes at most 3 arguments", pos.Line, pos.Col)
	}
	var transformer, wstrat, rstrat ast.Expression
	if len(exprs) > 0 {
		transformer = exprs[0]
	}
	if len(exprs) > 1 {
		wstrat = exprs[1]
	}
	if len(exprs) > 2 {
		rstrat = exprs[2]
	}
	return ast.NewNewTransformStreamExpression(inTy, outTy, transformer, wstrat, rstrat, pos), nil
}

// parseNewNodeStreamBody parses `new Readable<T>(opts?)` / `new
// Writable<T>(opts?)` / `new Transform<I, O>(opts?)` (TDD-00097 Stage 8).
func (p *Parser) parseNewNodeStreamBody(pos ast.Pos, kind string) (*ast.NewNodeStreamExpression, error) {
	p.advance() // consume the constructor name
	var inTy, outTy *ast.TypeAnnotation
	if p.check(lexer.LT) {
		p.advance()
		var err error
		first, err := p.parseTypeAnnotation("ts")
		if err != nil {
			return nil, err
		}
		if p.match(lexer.COMMA) {
			second, err := p.parseTypeAnnotation("ts")
			if err != nil {
				return nil, err
			}
			inTy, outTy = first, second
		} else if kind == "readable" {
			outTy = first
		} else {
			inTy = first
			if kind == "transform" || kind == "passthrough" {
				outTy = first
			}
		}
		if _, err := p.expect(lexer.GT); err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	var options ast.Expression
	if !p.check(lexer.RPAREN) {
		var err error
		options, err = p.parseAssignment()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewNodeStreamExpression(kind, inTy, outTy, options, pos), nil
}

// parseNewCompressionStreamBody parses `new CompressionStream(format)` /
// `new DecompressionStream(format)` (TDD-00097 Stage 6).
func (p *Parser) parseNewCompressionStreamBody(pos ast.Pos, decompress bool) (*ast.NewCompressionStreamExpression, error) {
	p.advance() // consume the constructor name
	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, err
	}
	format, err := p.parseAssignment()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, err
	}
	return ast.NewNewCompressionStreamExpression(decompress, format, pos), nil
}

// parseNewStreamArgs parses the shared `<T>(sourceOrSink?, strategy?)` tail
// of both stream constructors (the constructor name token is still current).
func (p *Parser) parseNewStreamArgs() (*ast.TypeAnnotation, ast.Expression, ast.Expression, error) {
	p.advance() // consume the constructor name

	var chunkType *ast.TypeAnnotation
	if p.check(lexer.LT) {
		p.advance() // consume '<'
		var err error
		chunkType, err = p.parseTypeAnnotation("ts")
		if err != nil {
			return nil, nil, nil, err
		}
		if _, err := p.expect(lexer.GT); err != nil {
			return nil, nil, nil, err
		}
	}

	if _, err := p.expect(lexer.LPAREN); err != nil {
		return nil, nil, nil, err
	}
	var source, strategy ast.Expression
	if !p.check(lexer.RPAREN) {
		var err error
		source, err = p.parseAssignment()
		if err != nil {
			return nil, nil, nil, err
		}
		if p.match(lexer.COMMA) {
			strategy, err = p.parseAssignment()
			if err != nil {
				return nil, nil, nil, err
			}
		}
	}
	if _, err := p.expect(lexer.RPAREN); err != nil {
		return nil, nil, nil, err
	}

	return chunkType, source, strategy, nil
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
		// ...`, and nested shapes — TDD-00065 Stage 2) — same shape
		// parseParamList's own destructured branch documents (an explicit
		// type annotation always required, no whole-parameter default); a
		// destructured *array* param is further rejected downstream in
		// codegen (emit_func.go's emitClosureFunc) since array-typed closure
		// parameters aren't supported at all yet, independent of
		// destructuring. Shares the same pattern-element grammar every other
		// destructuring position uses.
		if p.check(lexer.LBRACE) || p.check(lexer.LBRACKET) {
			if rest {
				return nil, fmt.Errorf("%d:%d: a rest parameter cannot be a destructuring pattern", p.peek().Line, p.peek().Col)
			}
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
			return nil, nil, nil, fmt.Errorf("%d:%d: expected template continuation, got %s", next.Line, next.Col, next.Type)
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

func (p *Parser) parsePrimary() (ast.Expression, error) {
	tok := p.peek()

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
			// A parameter list starting with `...` is a rest parameter — a
			// parenthesized expression can never begin with `...`, so this is
			// unambiguously an arrow (`(...xs) => …`, `(a, ...xs) => …`).
			(t1.Type == lexer.ELLIPSIS) ||
			((t1.Type == lexer.LBRACE || t1.Type == lexer.LBRACKET) && p.destructuredArrowParamLookahead())
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
		if p.check(lexer.IDENT) {
			name = p.advance().Literal
		}
		fd, err := p.parseFunctionRest(name, false, false, false)
		if err != nil {
			return nil, err
		}
		fe := ast.NewFunctionExpression(name, fd.Params, fd.ReturnType, fd.Body, false, fPos)
		fe.IsGenerator = isGen
		return fe, nil

	case lexer.ASYNC:
		// async (params) => expr / async (params): RetType => { ... }
		// async function(x) { ... } — function expression variant
		p.advance() // consume 'async'
		if p.check(lexer.FUNCTION) {
			p.advance() // consume 'function'
			fPos := posOf(tok)
			isGen := p.match(lexer.STAR) // async generator expression (TDD-00096)
			var name string
			if p.check(lexer.IDENT) {
				name = p.advance().Literal
			}
			fd, err := p.parseFunctionRest(name, true, false, false)
			if err != nil {
				return nil, err
			}
			fe := ast.NewFunctionExpression(name, fd.Params, fd.ReturnType, fd.Body, true, fPos)
			fe.IsGenerator = isGen
			return fe, nil
		}
		af, err := p.parseArrowFunction()
		if err != nil {
			return nil, err
		}
		af.IsAsync = true
		return af, nil
	}

	return nil, fmt.Errorf("%d:%d: unexpected token %s in expression", tok.Line, tok.Col, tok.Type)
}
