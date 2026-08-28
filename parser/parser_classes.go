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
// initializer block, or a typed field (`name: type;`). defaultName, when
// non-empty, is used as the class's name if no IDENT immediately follows
// `class` — the anonymous `export default class { ... }` form (TDD-00042);
// every other caller passes "" and gets the ordinary "name required" check.
// isClassMemberNameStart reports whether tok can begin a class member name
// right after a contextual `async` modifier (TDD-00063 Stage 2) — a plain or
// private identifier, or a generator `*` (for `async *gen()`). Used to tell
// the modifier `async` apart from a method literally named `async`.
func isClassMemberNameStart(tok lexer.Token) bool {
	switch tok.Type {
	case lexer.IDENT, lexer.PRIVATE_NAME, lexer.STAR:
		return true
	}
	// A reserved word is a valid member name (IdentifierName), so `async throw`
	// / `async return` etc. read as an async method, not a field named `async`.
	return lexer.IsKeyword(tok.Type)
}

// constMemberName resolves a computed class member key `[expr]` (TDD-00063
// Stage 3) to its member-name text when the key is a compile-time constant —
// a plain string literal (`['foo']` → "foo") or numeric literal (`[1]` →
// "1"). Anything else (an identifier, a call, `Symbol.iterator`, a template
// with interpolation) returns ok=false and is rejected at the call site,
// since this compiler resolves member names statically and has no runtime
// key evaluation.
func constMemberName(expr ast.Expression) (string, bool) {
	switch e := expr.(type) {
	case *ast.StringLiteral:
		return e.Value, true
	case *ast.NumberLiteral:
		return e.Value, true
	}
	return "", false
}

// wellKnownSymbolMemberName recognizes a computed class-member key that is a
// well-known symbol (`[Symbol.asyncIterator]`) and desugars it to a reserved
// internal member name (`@@asyncIterator`, the spec's `@@`-prefix convention).
// The `@@` prefix is not a lexable identifier, so it can never collide with a
// user-declared method — the same trick the accessor keys (`"get x"`) use
// (TDD-00089). `Symbol.asyncIterator` and `Symbol.iterator` are both
// recognized — the sync protocol's `[Symbol.iterator]()` desugars to
// `@@iterator`, dispatched by `for...of`/`for await...of` alongside the
// structural `next(): T | null` shape.
func wellKnownSymbolMemberName(expr ast.Expression) (string, bool) {
	me, ok := expr.(*ast.MemberExpression)
	if !ok {
		return "", false
	}
	obj, ok := me.Object.(*ast.Identifier)
	if !ok || obj.Name != "Symbol" {
		return "", false
	}
	if me.Property == "asyncIterator" {
		return "@@asyncIterator", true
	}
	if me.Property == "iterator" {
		return "@@iterator", true
	}
	return "", false
}

func (p *Parser) parseClassDecl(isAbstract bool, defaultName string) (*ast.ClassDeclaration, error) {
	tok := p.advance() // consume 'class'
	pos := posOf(tok)
	name := defaultName
	if p.check(lexer.IDENT) {
		name = p.advance().Literal
	} else if defaultName == "" {
		if _, err := p.expect(lexer.IDENT); err != nil {
			return nil, err
		}
	}
	// Optional `<T>` type-parameter list (TDD-00010 V1).
	var typeParams []string
	var typeParamConstraints []*ast.TypeAnnotation
	if p.check(lexer.LT) {
		tp, tc, err := p.parseTypeParamList(name + "<T>")
		if err != nil {
			return nil, err
		}
		typeParams = tp
		typeParamConstraints = tc
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
		// Qualified base: `extends events.EventEmitter` / `extends
		// stream.Readable` — the namespace-import shape. Like qualified `new`
		// (parseNew), the qualifier segments are consumed and the final name
		// is the base; a name registerClasses doesn't know still gets its
		// usual "extends unknown class" rejection there.
		for p.check(lexer.DOT) {
			p.advance() // '.'
			segTok, err := p.expect(lexer.IDENT)
			if err != nil {
				return nil, err
			}
			baseClass = segTok.Literal
		}
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

		// Async / generator method modifiers (TDD-00063 Stage 2). `async` is
		// contextual (a method may itself be named `async`), so it's a modifier
		// only when a real member name — or a generator `*` — follows; a bare
		// `async(` is the method literally named `async`. `*` here is
		// unambiguous: a generator method.
		var isAsyncMethod, isGeneratorMethod bool
		if p.peek().Type == lexer.ASYNC && isClassMemberNameStart(p.peekNth(1)) {
			isAsyncMethod = true
			p.advance()
		}
		if p.check(lexer.STAR) {
			isGeneratorMethod = true
			p.advance()
		}

		// Contextual `get`/`set` (TDD-00030): like `in` (ADR-00091), not a
		// reserved keyword — a field/method/variable literally named
		// `get`/`set` must keep working everywhere outside this one
		// position. Requires a 2-token lookahead (mirroring for...in's own
		// disambiguation, parseForInBody below) rather than in's simpler
		// 1-token check: unlike `in` (only ever a binary operator), a bare
		// `get`/`set` is *also* a completely valid method name on its own
		// (`get(): number { ... }`), so the token peek must confirm a real
		// member name follows before committing to accessor parsing. The
		// member name may be a private name too (`get #x()` — TDD-00021
		// private accessors), not just IDENT.
		// An `async`/generator modifier already precludes an accessor (`async
		// get() {}` is an async method literally named `get`, not a getter), so
		// this contextual detection is skipped once either is set.
		var accessorKind string
		if !isAsyncMethod && !isGeneratorMethod &&
			p.peek().Type == lexer.IDENT && (p.peek().Literal == "get" || p.peek().Literal == "set") &&
			(p.peekNth(1).Type == lexer.IDENT || p.peekNth(1).Type == lexer.PRIVATE_NAME || p.peekNth(1).Type == lexer.LBRACKET) {
			accessorKind = p.peek().Literal
			p.advance()
		}

		// A member name is either a plain IDENT or a private name (`#x` —
		// TDD-00021); the `#` itself fully determines visibility, so an
		// explicit accessibility modifier alongside it is rejected below
		// rather than silently accepted or silently overridden, matching
		// real TypeScript.
		var memberTok lexer.Token
		if p.check(lexer.LBRACKET) {
			// Computed member name (TDD-00063 Stage 3): V1 supports only a
			// compile-time-constant string or numeric literal, desugared to
			// the equivalent named member — `['foo']() {}` is exactly `foo()
			// {}`. A dynamic key (an identifier, a call like `[ID('d')]`, a
			// `Symbol.*`, or a template with interpolation) is a clean
			// rejection, not silently mishandled.
			lb := p.advance() // '['
			keyExpr, err := p.parseAssignment()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.RBRACKET); err != nil {
				return nil, err
			}
			name, ok := constMemberName(keyExpr)
			if !ok {
				// A well-known symbol key (`[Symbol.asyncIterator]`) desugars to a
				// reserved internal member name (TDD-00089); any other dynamic key
				// stays a clean rejection.
				if wk, wkOk := wellKnownSymbolMemberName(keyExpr); wkOk {
					name = wk
				} else {
					return nil, fmt.Errorf("%d:%d: a computed class member name must be a constant string or number literal — a dynamic key (identifier, call, Symbol, or interpolation) is not yet supported (see docs/tdd/TDD-00063.md Stage 3)", lb.Line, lb.Col)
				}
			}
			memberTok = lexer.Token{Type: lexer.IDENT, Literal: name, Line: lb.Line, Col: lb.Col}
		} else if p.check(lexer.PRIVATE_NAME) {
			memberTok = p.advance()
			if visibility != "" {
				return nil, fmt.Errorf("%d:%d: an accessibility modifier cannot be used with a private identifier", memberTok.Line, memberTok.Col)
			}
			// `#constructor` is a reserved private name — an early SyntaxError
			// regardless of static/instance or method/field position.
			if memberTok.Literal == "#constructor" {
				return nil, fmt.Errorf("%d:%d: '#constructor' is a reserved class member name", memberTok.Line, memberTok.Col)
			}
			visibility = "private"
		} else if lexer.IsKeyword(p.peek().Type) {
			// A class member name is an IdentifierName — any reserved word is a
			// valid method/property name (JS PropertyName), the same way a reserved
			// word is a valid member after `.` (`promise.catch`). This is what lets a
			// user async iterator declare the iterator-protocol methods `throw`/
			// `return` (delegated to by `yield*`), among others.
			kw := p.advance()
			memberTok = lexer.Token{Type: lexer.IDENT, Literal: kw.Literal, Line: kw.Line, Col: kw.Col}
		} else {
			var err error
			memberTok, err = p.expect(lexer.IDENT)
			if err != nil {
				return nil, err
			}
		}
		if accessorKind != "" && memberTok.Literal == "constructor" {
			return nil, fmt.Errorf("%d:%d: a constructor cannot be a getter/setter", memberTok.Line, memberTok.Col)
		}
		// A static class member named `prototype` is an early SyntaxError, for
		// a method or a field alike (a non-static `prototype` member is fine).
		if isStatic && memberTok.Literal == "prototype" {
			return nil, fmt.Errorf("%d:%d: a static class member cannot be named 'prototype'", memberTok.Line, memberTok.Col)
		}

		// Method or constructor: `name(...) { ... }` (or, if isMemberAbstract,
		// `name(...): T;` with no body).
		if p.check(lexer.LPAREN) {
			if memberTok.Literal == "constructor" && (isAsyncMethod || isGeneratorMethod) {
				return nil, fmt.Errorf("%d:%d: a constructor cannot be async or a generator", memberTok.Line, memberTok.Col)
			}
			fn, err := p.parseFunctionRest(memberTok.Literal, isAsyncMethod, isMemberAbstract)
			if err != nil {
				return nil, err
			}
			// A class body is always strict mode, so `eval`/`arguments` can
			// never be a method/constructor parameter name — an early error
			// unconditionally here, unlike a plain function where it only
			// applies under an explicit "use strict" directive.
			for _, prm := range fn.Params {
				if prm.Name == "eval" || prm.Name == "arguments" {
					return nil, fmt.Errorf("%d:%d: '%s' cannot be a parameter name in a class method (strict mode)", memberTok.Line, memberTok.Col, prm.Name)
				}
			}
			// A class body is always strict, so a let/const/var (or for-of/
			// for-in loop variable) in the method body may not bind
			// `eval`/`arguments` either — same rule the use-strict function
			// path enforces.
			if fn.Body != nil {
				if err := strictBindingError(fn.Body.Body); err != nil {
					return nil, err
				}
			}
			fn.IsGenerator = isGeneratorMethod
			fn.IsStatic = isStatic
			fn.Visibility = visibility
			fn.AccessorKind = accessorKind
			if memberTok.Literal == "constructor" {
				if ctor != nil {
					return nil, fmt.Errorf("%d:%d: class '%s' declares more than one constructor", memberTok.Line, memberTok.Col, name)
				}
				ctor = fn
			} else {
				methods = append(methods, fn)
			}
			continue
		}
		if accessorKind != "" {
			return nil, fmt.Errorf("%d:%d: '%s %s' must be a method (missing '()')", memberTok.Line, memberTok.Col, accessorKind, memberTok.Literal)
		}
		if isAsyncMethod || isGeneratorMethod {
			return nil, fmt.Errorf("%d:%d: '%s' is not a method (an async/generator modifier requires a '()' method body)", memberTok.Line, memberTok.Col, memberTok.Literal)
		}

		// Otherwise a field: `name: type;`, `name: type = expr;`, or
		// `name = expr;` (initializer, unannotated — type inferred at codegen,
		// TDD-00063 Stage 1). The `: type` is optional only when an `= expr`
		// initializer follows to give the field its type.
		p.match(lexer.QUESTION)
		var ft *ast.TypeAnnotation
		if p.check(lexer.COLON) {
			p.advance()
			var err error
			ft, err = p.parseTypeAnnotation("ts")
			if err != nil {
				return nil, err
			}
		}
		if doc != nil {
			if t := doc.GetType(); t != "" {
				ft = jsdocTypeAnnotation(t)
			}
		}
		var initializer ast.Expression
		if p.check(lexer.ASSIGN) {
			p.advance()
			var err error
			initializer, err = p.parseAssignment()
			if err != nil {
				return nil, err
			}
		}
		if ft == nil && initializer == nil {
			return nil, fmt.Errorf("%d:%d: class field '%s' requires a type annotation or an initializer", memberTok.Line, memberTok.Col, memberTok.Literal)
		}
		fields = append(fields, ast.AnnotField{Name: memberTok.Literal, Type: ft, Initializer: initializer, Static: isStatic, Visibility: visibility})
		p.match(lexer.SEMICOLON, lexer.COMMA)
	}
	if _, err := p.expect(lexer.RBRACE); err != nil {
		return nil, err
	}
	decl := ast.NewClassDeclaration(name, baseClass, baseTypeArgs, isAbstract, implementsNames, fields, ctor, methods, staticBlocks, pos)
	decl.TypeParams = typeParams
	decl.TypeParamConstraints = typeParamConstraints
	return decl, nil
}
