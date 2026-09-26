package parser

import (
	"KlainMainLang/ast"
	"KlainMainLang/diag"
	"KlainMainLang/lexer"
)

// parseDecorators consumes a run of `@<LeftHandSideExpression>` decorator
// prefixes (TDD-00161 Stage 1) and returns them in source order. A decorator
// expression is an identifier / property-access chain / call
// (`@foo`, `@foo.bar`, `@foo(1, 2)`, `@(expr)`) — exactly parseCallMember's
// grammar. Returns nil when no `@` is present, so it is safe to call
// unconditionally at any decoratable position.
func (p *Parser) parseDecorators() ([]ast.Expression, error) {
	var decs []ast.Expression
	for p.check(lexer.AT) {
		p.advance() // consume '@'
		expr, err := p.parseCallMember()
		if err != nil {
			return nil, err
		}
		decs = append(decs, expr)
	}
	return decs, nil
}

// unwrapClassDecl returns the *ast.ClassDeclaration a statement carries — the
// statement itself, or the one wrapped in an `export [default]` declaration —
// or nil if the statement is not a class. Used to attach class-level
// decorators (TDD-00161) regardless of an intervening `export`.
func unwrapClassDecl(stmt ast.Statement) *ast.ClassDeclaration {
	switch s := stmt.(type) {
	case *ast.ClassDeclaration:
		return s
	case *ast.ExportDeclaration:
		if cd, ok := s.Decl.(*ast.ClassDeclaration); ok {
			return cd
		}
	}
	return nil
}

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
	// [Symbol.toPrimitive](hint) — the ToPrimitive ladder (TDD-00201) checks this
	// reserved method field before valueOf/toString. Same static string-alias
	// mechanism, no dynamic-property-bag dependency.
	if me.Property == "toPrimitive" {
		return "@@toPrimitive", true
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
	// Inside the class, the `this` type lowers to the class for code
	// generation; the checker reads it as the polymorphic receiver.
	savedThis := p.thisClass
	p.thisClass = name
	defer func() { p.thisClass = savedThis }()
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
	baseQualified := false
	baseQualifier := ""
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
			baseQualifier = baseClass
			baseClass = segTok.Literal
			baseQualified = true
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
	// pendingOverload holds the member name of an open TS overload group —
	// body-less signatures (`bar(x: string): void;`) already seen and erased,
	// still awaiting their implementation. TS requires the group to be
	// consecutive and closed by an implementation; both are enforced here.
	var pendingOverload string
	var pendingOverloadTok lexer.Token
	var pendingSigs []*ast.FunctionDeclaration // the open group's signatures
	savedContexts := p.contexts
	p.contexts |= 1 << ctxClassMembers
	defer func() { p.contexts = savedContexts }()
	for !p.check(lexer.RBRACE) && !p.check(lexer.EOF) {
		mstart := p.pos
		if err := p.recoverList(ctxClassMembers, mstart, func() error {
			// A `;` between members is an empty class element.
			if p.match(lexer.SEMICOLON) {
				return nil
			}
			doc := p.takeDoc()

			// `@decorator` prefixes on this member (TDD-00161 Stage 1), before the
			// modifier run — the canonical position. Attached to the produced
			// method (FunctionDeclaration.Decorators) or field
			// (AnnotField.Decorators) below.
			memberDecorators, err := p.parseDecorators()
			if err != nil {
				return err
			}

			// `static { ... }` initializer block — distinguished from a
			// `static`-modified member by checking for `{` immediately after
			// `static`, before any member name has been consumed.
			if p.check(lexer.STATIC) && p.peekNth(1).Type == lexer.LBRACE {
				if len(memberDecorators) > 0 {
					return p.errAt(p.peek(), diag.DecoratorOnStaticBlock)
				}
				p.advance() // static
				block, err := p.parseBlock()
				if err != nil {
					return err
				}
				staticBlocks = append(staticBlocks, block)
				return nil
			}

			// Zero or more modifiers, any order. `readonly` is contextual (a
			// field may itself be named `readonly`), so it's a modifier only
			// when a member name follows; like the ctor-param form (ADR-00447)
			// it is parsed and recorded but not enforced as immutability
			// (ADR-00480).
			var isStatic, isMemberAbstract, isReadonly, isAutoAccessor bool
			var visibility string
			for {
				if p.peek().Type == lexer.IDENT && p.peek().Literal == "readonly" &&
					(isClassMemberNameStart(p.peekNth(1)) || p.peekNth(1).Type == lexer.LBRACKET) {
					isReadonly = true
					p.advance()
					continue
				}
				// `accessor x` — a TC39 auto-accessor field (contextual, like
				// `readonly`). Only before a member name; a field literally named
				// `accessor` keeps working.
				if p.peek().Type == lexer.IDENT && p.peek().Literal == "accessor" &&
					(isClassMemberNameStart(p.peekNth(1)) || p.peekNth(1).Type == lexer.LBRACKET) {
					isAutoAccessor = true
					p.advance()
					continue
				}
				// `abstract` is contextual: a modifier only when a member name follows
				// on the same line (a member may itself be named `abstract`).
				if p.isWord(0, "abstract") && p.sameLine(1) && isClassMemberNameStart(p.peekNth(1)) {
					isMemberAbstract = true
					p.advance()
					continue
				}
				switch p.peek().Type {
				case lexer.STATIC:
					isStatic = true
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
			if p.isWord(0, "async") && p.sameLine(1) && isClassMemberNameStart(p.peekNth(1)) {
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
			// A computed key is never the constructor: `["constructor"]() {}`
			// is an ordinary method of that name.
			computedName := false
			// A class-body index signature (`[n: number]: T;` — ADR-00476) is
			// parsed and dropped: classes have fixed field layouts (no map
			// backing), so the signature declares nothing reachable; indexing
			// still gets its ordinary rejection at the use site. Distinguished
			// from a computed member name (`[expr]…`) by the `IDENT :` prefix.
			if p.check(lexer.LBRACKET) && p.peekNth(1).Type == lexer.IDENT && p.peekNth(2).Type == lexer.COLON {
				if _, err := p.parseIndexSignature("ts"); err != nil {
					return err
				}
				if err := p.parseSemicolon(); err != nil {
					return err
				}
				return nil
			}
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
					return err
				}
				if _, err := p.expect(lexer.RBRACKET); err != nil {
					return err
				}
				name, ok := constMemberName(keyExpr)
				if !ok {
					// A well-known symbol key (`[Symbol.asyncIterator]`) desugars to a
					// reserved internal member name (TDD-00089); any other dynamic key
					// stays a clean rejection.
					if wk, wkOk := wellKnownSymbolMemberName(keyExpr); wkOk {
						name = wk
					} else {
						return p.errAt(lb, diag.ComputedClassMemberName)
					}
				}
				memberTok = lexer.Token{Type: lexer.IDENT, Literal: name, Line: lb.Line, Col: lb.Col}
				computedName = true
			} else if p.check(lexer.PRIVATE_NAME) {
				memberTok = p.advance()
				if visibility != "" {
					return p.errAt(memberTok, diag.AccessibilityOnPrivate)
				}
				// `#constructor` is a reserved private name — an early SyntaxError
				// regardless of static/instance or method/field position.
				if memberTok.Literal == "#constructor" {
					return p.errAt(memberTok, diag.HashConstructor)
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
					return err
				}
			}
			isCtor := memberTok.Literal == "constructor" && !computedName
			if accessorKind != "" && isCtor {
				return p.errAt(memberTok, diag.ConstructorAccessor)
			}
			// A static class member named `prototype` is an early SyntaxError, for
			// a method or a field alike (a non-static `prototype` member is fine).
			if isStatic && memberTok.Literal == "prototype" {
				return p.errAt(memberTok, diag.StaticPrototype)
			}

			// An optional method (`name?(...): T;`): a signature a subclass may
			// implement; the member is undefined on a class that does not.
			isOptionalMethod := false
			if !isCtor && accessorKind == "" && p.check(lexer.QUESTION) &&
				(p.peekNth(1).Type == lexer.LPAREN || p.peekNth(1).Type == lexer.LT) {
				p.advance()
				isOptionalMethod = true
			}
			// Method or constructor: `name(...) { ... }` (or, if isMemberAbstract,
			// `name(...): T;` with no body).
			if p.check(lexer.LPAREN) || (p.check(lexer.LT) && p.peekNth(1).Type == lexer.IDENT) {
				if isCtor && (isAsyncMethod || isGeneratorMethod) {
					return p.errAt(memberTok, diag.ConstructorAsyncGenerator)
				}
				if pendingOverload != "" && memberTok.Literal != pendingOverload {
					return p.overloadError(memberTok, &pendingOverload)
				}
				p.inCtorParams = isCtor
				if isAutoAccessor {
					return p.errAt(memberTok, diag.AccessorOnMethod)
				}
				fn, err := p.parseFunctionRest(memberTok.Literal, isAsyncMethod, isMemberAbstract || isOptionalMethod, !isMemberAbstract && !isOptionalMethod && accessorKind == "")
				p.inCtorParams = false
				if err != nil {
					return err
				}
				if isOptionalMethod {
					fn.IsAbstract = false
					fn.IsOptional = true
				}
				// A generic *method* (`map<U>(f): U`) erases its type parameters
				// to `any` and compiles as a plain method — the ADR-00469 stance,
				// unblocked by ADR-00477's closure adapter trampolines (the
				// mistagged-return bug ADR-00475 deferred is fixed; a concrete
				// callback passed for an `any`-signature parameter now adapts).
				// A parameter constrained by a plain name (`T extends Stream`)
				// erases to it instead, keeping its representation. The type
				// parameters stay for the checker, which reads the type nodes.
				if len(fn.TypeParams) > 0 && !isCtor {
					to := map[string]string{}
					for i, tp := range fn.TypeParams {
						to[tp] = "any"
						if i < len(fn.TypeParamConstraints) {
							if c := fn.TypeParamConstraints[i]; c != nil && simpleTypeName(c) {
								to[tp] = c.Name
							}
						}
					}
					for i := range fn.Params {
						ast.EraseTypeParamsTo(fn.Params[i].Type, to)
					}
					ast.EraseTypeParamsTo(fn.ReturnType, to)
					fn.ErasedMethod = true
				}

				// A body-less overload signature: erase it and hold the group open
				// until the implementation (same name, with a body) arrives.
				if fn.IsOverloadSig {
					if pendingOverload == "" {
						pendingSigs = nil
					}
					pendingOverload, pendingOverloadTok = memberTok.Literal, memberTok
					pendingSigs = append(pendingSigs, fn)
					return nil
				}
				if pendingOverload != "" {
					fn.Overloads = pendingSigs
				}
				pendingOverload, pendingSigs = "", nil
				// A class body is always strict mode, so `eval`/`arguments` can
				// never be a method/constructor parameter name — an early error
				// unconditionally here, unlike a plain function where it only
				// applies under an explicit "use strict" directive.
				for _, prm := range fn.Params {
					if prm.Name == "eval" || prm.Name == "arguments" {
						return p.errAt(memberTok, diag.StrictClassParameterName, prm.Name)
					}
				}
				// A class body is always strict, so a let/const/var (or for-of/
				// for-in loop variable) in the method body may not bind
				// `eval`/`arguments` either — same rule the use-strict function
				// path enforces.
				if fn.Body != nil {
					if err := strictBindingError(fn.Body.Body); err != nil {
						return err
					}
				}
				fn.IsGenerator = isGeneratorMethod
				fn.IsStatic = isStatic
				fn.Visibility = visibility
				fn.AccessorKind = accessorKind
				if isCtor {
					if ctor != nil {
						return p.errAt(memberTok, diag.DuplicateConstructor, name)
					}
					// Desugar TS parameter properties: each modified parameter
					// becomes a declared field plus a `this.x = x` assignment
					// prepended to the constructor body (before user statements,
					// matching TS emit order).
					var propStmts []ast.Statement
					for _, prm := range fn.Params {
						if !prm.IsPropertyParam() {
							continue
						}
						if prm.ArrayPattern != nil || prm.ObjectPattern != nil || prm.Rest {
							return p.errAt(memberTok, diag.ParamPropertyPattern)
						}
						ft := prm.Type
						if ft == nil && prm.Default == nil {
							// An unannotated plain parameter defaults to `number`
							// (registerFunctions' rule); mirror it on the field so
							// the two stay the same type.
							ft = &ast.TypeAnnotation{Name: "number"}
						}
						vis := prm.PropVisibility
						if vis == "public" {
							vis = ""
						}
						fieldPos := ast.Pos{Line: memberTok.Line, Col: memberTok.Col}
						var init ast.Expression
						if ft == nil {
							// Untyped with a default: let the field infer its type
							// from the same default expression.
							init = prm.Default
						}
						// `public p?: T` declares an optional field (`p?: T`): an
						// omitted argument stores undefined, as TypeScript has it.
						fields = append(fields, ast.AnnotField{Name: prm.Name, Type: ft, Optional: prm.Optional, Initializer: init, Visibility: vis, Readonly: prm.PropReadonly})
						assign := ast.NewExpressionStatement(
							ast.NewAssignmentExpression("=",
								ast.NewMemberExpression(ast.NewThisExpression(fieldPos), prm.Name, fieldPos),
								ast.NewIdentifier(prm.Name, fieldPos), fieldPos), fieldPos)
						propStmts = append(propStmts, assign)
					}
					if len(propStmts) > 0 && fn.Body != nil {
						fn.Body.Body = append(propStmts, fn.Body.Body...)
					}
					fn.Decorators = memberDecorators
					ctor = fn
				} else {
					fn.Decorators = memberDecorators
					methods = append(methods, fn)
				}
				return nil
			}
			if pendingOverload != "" {
				return p.overloadError(memberTok, &pendingOverload)
			}
			if accessorKind != "" {
				return p.errAt(memberTok, diag.MethodMissingParens, accessorKind, memberTok.Literal)
			}
			if isAsyncMethod || isGeneratorMethod {
				return p.errAt(memberTok, diag.ModifierNeedsMethod, memberTok.Literal)
			}

			// Otherwise a field: `name: type;`, `name: type = expr;`, or
			// `name = expr;` (initializer, unannotated — type inferred at codegen,
			// TDD-00063 Stage 1). The `: type` is optional only when an `= expr`
			// initializer follows to give the field its type.
			optionalField := p.match(lexer.QUESTION)
			if !optionalField && p.check(lexer.NOT) {
				// `name!: T`, a definite assignment assertion: it only waives
				// the initialisation check, and needs the annotation.
				p.advance()
				if !p.check(lexer.COLON) {
					_, err := p.expect(lexer.COLON)
					return err
				}
			}
			var ft *ast.TypeAnnotation
			if p.check(lexer.COLON) {
				p.advance()
				var err error
				ft, err = p.parseTypeAnnotation("ts")
				if err != nil {
					return err
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
					return err
				}
			}
			if ft == nil && initializer == nil {
				// A bare field (`class C { x; }`) defaults to `number` — the
				// same convention an unannotated parameter and an untyped
				// variable already follow (ADR-00474); a JSDoc `@type` above
				// the field still overrides. TS infers implicit `any` here, so
				// a later non-numeric assignment is a shifted (typed) error
				// rather than accepted — a disclosed narrowing.
				ft = &ast.TypeAnnotation{Name: "number", Source: "ts"}
			}
			fields = append(fields, ast.AnnotField{Name: memberTok.Literal, Type: ft, Optional: optionalField, Initializer: initializer, Static: isStatic, Visibility: visibility, Readonly: isReadonly, Decorators: memberDecorators, IsAutoAccessor: isAutoAccessor})
			// a field declaration ends like a statement: `;`, or ASI before `}` /
			// at a line break (`x = 1 y = 2` on one line is an error, as in JS)
			if err := p.parseSemicolon(); err != nil {
				return err
			}
			return nil
		}()); err != nil {
			return nil, err
		}
	}
	if pendingOverload != "" {
		return nil, p.errAt(pendingOverloadTok, diag.OverloadClassNoImpl, pendingOverload, name)
	}
	if _, err := p.expect(lexer.RBRACE); err != nil {
		return nil, err
	}
	// Desugar a *decorated* `accessor x` auto-field to a private backing field
	// plus generated get/set, and record the accessor decorators as a unit
	// (TDD-00161 — the TC39 `{get,set,init}` protocol). A non-decorated
	// `accessor x` stays a plain field (observably identical).
	var autoAccessors []ast.AutoAccessorSpec
	if hasDecoratedAutoAccessor(fields) {
		var kept []ast.AnnotField
		for _, f := range fields {
			if !f.IsAutoAccessor || len(f.Decorators) == 0 {
				kept = append(kept, f)
				continue
			}
			backing := "__kml_acc_" + f.Name
			bf := f
			bf.Name = backing
			bf.Decorators = nil
			bf.IsAutoAccessor = false
			bf.Visibility = "private"
			kept = append(kept, bf)
			getBody := ast.NewBlockStatement([]ast.Statement{
				ast.NewReturnStatement(ast.NewMemberExpression(ast.NewThisExpression(pos), backing, pos), pos),
			}, pos)
			getFn := &ast.FunctionDeclaration{Name: f.Name, AccessorKind: "get", ReturnType: f.Type, Body: getBody}
			getFn.SetPos(pos)
			setBody := ast.NewBlockStatement([]ast.Statement{
				ast.NewExpressionStatement(ast.NewAssignmentExpression("=",
					ast.NewMemberExpression(ast.NewThisExpression(pos), backing, pos),
					ast.NewIdentifier("value", pos), pos), pos),
			}, pos)
			setFn := &ast.FunctionDeclaration{Name: f.Name, AccessorKind: "set", Params: []ast.Param{{Name: "value", Type: f.Type}}, Body: setBody}
			setFn.SetPos(pos)
			methods = append(methods, getFn, setFn)
			autoAccessors = append(autoAccessors, ast.AutoAccessorSpec{Name: f.Name, Backing: backing, Decorators: f.Decorators})
		}
		fields = kept
	}

	decl := ast.NewClassDeclaration(name, baseClass, baseTypeArgs, isAbstract, implementsNames, fields, ctor, methods, staticBlocks, pos)
	decl.BaseQualified = baseQualified
	decl.BaseQualifier = baseQualifier
	decl.TypeParams = typeParams
	decl.TypeParamConstraints = typeParamConstraints
	decl.AutoAccessors = autoAccessors
	return decl, nil
}

// hasDecoratedAutoAccessor reports whether any field is a decorated `accessor`
// auto-field (needing the desugar).
func hasDecoratedAutoAccessor(fields []ast.AnnotField) bool {
	for _, f := range fields {
		if f.IsAutoAccessor && len(f.Decorators) > 0 {
			return true
		}
	}
	return false
}

// overloadError reports an open overload group that was not closed by its
// implementation, and closes it: the class-level check must not report the
// same group again.
func (p *Parser) overloadError(at lexer.Token, pending *string) error {
	name := *pending
	*pending = ""
	return p.errAt(at, diag.OverloadNoImplementation, name)
}

// simpleTypeName reports whether ta is a bare type name (`Stream`), with no
// arguments, elements, members or modifiers.
func simpleTypeName(ta *ast.TypeAnnotation) bool {
	return ta.Name != "" && ta.ElemType == nil && ta.KeyType == nil && len(ta.TypeArgs) == 0 &&
		ta.UnionMembers == nil && ta.IntersectionMembers == nil && !ta.IsFuncType && !ta.Nullable &&
		ta.Name != "any" && ta.Name != "unknown" && ta.Name != "object"
}
