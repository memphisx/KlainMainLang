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
	// namespaces accumulates TS `namespace X {...}` member sets (TDD-00095);
	// pendingTopLevel carries a namespace's desugared member declarations
	// past parseStatement's single-return shape — ParseProgram drains it
	// after every statement.
	namespaces      map[string]map[string]bool
	pendingTopLevel []ast.Statement
	// workerPaths accumulates every `new Worker('...')` path literal seen in
	// this file (TDD-00098), surfaced as Program.WorkerPaths so the resolver
	// can treat worker entry files as dependency edges without an AST walk.
	workerPaths []string
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

// expectGT consumes a '>' that closes a generic type parameter list,
// tolerating the case where it's fused with the *next* nesting level's own
// closing '>' into a single RSHIFT/URSHIFT token — the lexer has no way to
// know Array<Promise<T>>'s trailing ">>" should be two closing angle
// brackets rather than a right-shift operator; it always emits one RSHIFT
// token there (the same >> ambiguity C++/Rust/TypeScript's own parsers all
// have to resolve once parsing context makes clear it's actually nested
// generics closing, not an operator). If the current token is RSHIFT or
// URSHIFT, this "un-merges" one '>' worth of it in place — rewriting the
// token to one fewer '>' (RSHIFT -> GT, URSHIFT -> RSHIFT) without
// advancing the cursor — so the *next* enclosing level's own expectGT call
// still finds a real closing token instead of nothing. Found and fixed
// while implementing Promise.all (ADR-00073): every existing call site
// used p.expect(lexer.GT) directly, so Array<Promise<T>> — the exact
// syntax Promise.all's argument type needs — failed to parse at all.
func (p *Parser) expectGT(context string) error {
	t := p.peek()
	switch t.Type {
	case lexer.GT:
		p.advance()
		return nil
	case lexer.RSHIFT:
		p.tokens[p.pos] = lexer.Token{Type: lexer.GT, Literal: ">", Line: t.Line, Col: t.Col + 1}
		return nil
	case lexer.URSHIFT:
		p.tokens[p.pos] = lexer.Token{Type: lexer.RSHIFT, Literal: ">>", Line: t.Line, Col: t.Col + 1}
		return nil
	}
	return fmt.Errorf("%d:%d: expected '>' to close %s", t.Line, t.Col, context)
}

// parseTypeParamList parses a declaration-site `<T>` or `<K, V>`
// type-parameter list — shared by function/interface/class declarations
// (TDD-00010 V1, extended to N parameters by TDD-00037). Assumes the caller
// has already checked the current token is '<'. Type parameters remain
// unconstrained — `<T extends Base>` produces a clear error rather than
// being silently mis-parsed, still out of scope (see docs/tdd/TDD-00010.md's
// and TDD-00037's Open Questions).
func (p *Parser) parseTypeParamList(context string) ([]string, error) {
	p.advance() // consume '<'
	var names []string
	for {
		nameTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, err
		}
		names = append(names, nameTok.Literal)
		if p.check(lexer.EXTENDS) {
			t := p.peek()
			return nil, fmt.Errorf("%d:%d: type parameter constraints on %s are not yet supported", t.Line, t.Col, context)
		}
		if !p.match(lexer.COMMA) {
			break
		}
	}
	if err := p.expectGT(context); err != nil {
		return nil, err
	}
	return names, nil
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
		if len(p.pendingTopLevel) > 0 {
			prog.Body = append(prog.Body, p.pendingTopLevel...)
			p.pendingTopLevel = nil
		}
	}
	prog.Namespaces = p.namespaces
	prog.WorkerPaths = p.workerPaths
	return prog, nil
}
