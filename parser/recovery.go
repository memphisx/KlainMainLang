package parser

import (
	"errors"
	"sort"

	"KlainMainLang/ast"
	"KlainMainLang/diag"
	"KlainMainLang/lexer"
)

// Error recovery. A list the parser reads element by element (the statements
// of a file, a block or a switch clause; the members of a class) catches an
// element's error, reports it, and resynchronises: it skips tokens until one
// that can start its next element or ends it, and resumes. A token that
// belongs to an enclosing list instead (a class member after a method body
// missing its `}`) aborts the inner list, and the enclosing one resumes there.
// Parsing thus continues past an error, and every error in the file is
// reported, one per position.
//
// An element can fail part-way through valid syntax this compiler does not
// implement (`[await x]() { … }`), so the rest of it is skipped as a unit:
// a bracketed group is skipped whole, and the list resumes or aborts only at
// an element boundary — after a `;` or a closing `}`, or at a line break.
//
// A speculative parse that fails must fail as before, so the caller can try
// its other reading: while one is in progress, errors are not recovered.

// parsingContext is a kind of list error recovery resynchronises to.
type parsingContext uint

const (
	ctxSourceElements parsingContext = iota
	ctxBlockStatements
	ctxSwitchClauseStatements
	ctxClassMembers
	ctxCount
)

// errAborted is returned by a list that stopped at a token of an enclosing
// list; its error has already been reported.
var errAborted = errors.New("list aborted at an enclosing list's token")

// otherError carries a failure that is not a diagnostic (its text already
// includes any position).
var otherError = &diag.Message{Code: diag.FirstOwnCode, Kind: diag.SyntaxError, Phase: diag.PhaseParse, Text: "%s"}

// parseStatementList parses the statements of list ctx up to its terminator,
// handing each to add. A statement that fails, or that add rejects, is
// reported and skipped. It returns errAborted when the list gave way to an
// enclosing one, or the error itself during a speculative parse.
func (p *Parser) parseStatementList(ctx parsingContext, add func(ast.Statement) error) error {
	saved := p.contexts
	p.contexts |= 1 << ctx
	defer func() { p.contexts = saved }()
	for !p.isListTerminator(ctx) {
		start := p.pos
		stmt, err := p.parseStatement()
		if err == nil {
			err = add(stmt)
		}
		if err != nil {
			if err := p.recoverList(ctx, start, err); err != nil {
				return err
			}
		}
	}
	return nil
}

// recoverList reports err, an element of list ctx that began at token start
// failed with, and resynchronises. nil resumes the list (at an element or its
// terminator); errAborted means a token of an enclosing list was reached.
func (p *Parser) recoverList(ctx parsingContext, start int, err error) error {
	if err == nil || p.speculating > 0 {
		return err
	}
	p.report(err)
	// An element that failed on its first token, although that token can
	// start one, cannot be retried there: skip it.
	if p.pos == start && !p.isListTerminator(ctx) && p.isListElement(ctx, true) {
		p.skipGroup()
	}
	for !p.check(lexer.EOF) {
		if p.isListTerminator(ctx) {
			return nil
		}
		if p.atElementBoundary() {
			if p.isListElement(ctx, true) {
				return nil
			}
			for c := ctxSourceElements; c < ctxCount; c++ {
				if c != ctx && p.contexts&(1<<c) != 0 && (p.isListElement(c, true) || p.isListTerminator(c)) {
					return errAborted
				}
			}
		}
		p.skipGroup()
	}
	return nil
}

// atElementBoundary reports whether a list element can start at the current
// token: after a `;` or a `}`, or on a new line.
func (p *Parser) atElementBoundary() bool {
	if p.peek().HasPrecedingLineBreak() || p.pos == 0 {
		return true
	}
	prev := p.at(p.pos - 1).Type
	return prev == lexer.SEMICOLON || prev == lexer.RBRACE
}

// skipGroup skips the current token, and when it opens a bracketed group,
// everything up to and including the bracket that closes it.
func (p *Parser) skipGroup() {
	depth := 0
	for {
		switch p.advance().Type {
		case lexer.LPAREN, lexer.LBRACKET, lexer.LBRACE, lexer.TEMPLATE_HEAD:
			depth++
		case lexer.RPAREN, lexer.RBRACKET, lexer.RBRACE, lexer.TEMPLATE_TAIL:
			depth--
		case lexer.EOF:
			return
		}
		if depth <= 0 || p.check(lexer.EOF) {
			return
		}
	}
}

// report adds err's diagnostics to the file's. Input the scanner could not
// read is the real problem once it has been reached: past it the parser sees
// only the end of input, so an error reported after it was scanned is
// replaced by the scanner's own (once).
func (p *Parser) report(err error) {
	if err == nil || err == errAborted {
		return
	}
	for _, t := range p.tokens {
		if t.Type == lexer.ILLEGAL && t.Err != nil {
			if !p.scanReported {
				p.scanReported = true
				p.add(t.Err)
			}
			return
		}
	}
	ds := diag.As(err)
	if ds == nil {
		ds = []*diag.Diagnostic{diag.New(otherError, diag.Span{Start: -1, End: -1}, err.Error())}
	}
	for _, d := range ds {
		p.add(d)
	}
}

// add records d, keeping the list in source order.
func (p *Parser) add(d *diag.Diagnostic) {
	p.fillOffset(d)
	items := p.diags.Items()
	if n := len(items); n == 0 || items[n-1].Start <= d.Start {
		p.diags.Add(d)
		return
	}
	// A diagnostic earlier in the source than the last one (the scanner's,
	// found by look-ahead): insert it in order.
	var sorted diag.List
	all := append(append([]*diag.Diagnostic(nil), items...), d)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Start < all[j].Start })
	for _, x := range all {
		sorted.Add(x)
	}
	p.diags = sorted
}

// isListTerminator reports whether the current token ends list ctx.
func (p *Parser) isListTerminator(ctx parsingContext) bool {
	t := p.peek().Type
	if t == lexer.EOF {
		return true
	}
	switch ctx {
	case ctxBlockStatements, ctxClassMembers:
		return t == lexer.RBRACE
	case ctxSwitchClauseStatements:
		return t == lexer.RBRACE || t == lexer.CASE || t == lexer.DEFAULT
	}
	return false
}

// isListElement reports whether the current token can start an element of
// list ctx. In recovery a `;` is not taken as an (empty) element: it shows up
// in too many places to resynchronise on.
func (p *Parser) isListElement(ctx parsingContext, inRecovery bool) bool {
	t := p.peek().Type
	if t == lexer.SEMICOLON {
		return !inRecovery
	}
	switch ctx {
	case ctxClassMembers:
		return isClassMemberStart(t)
	}
	return isStatementStart(t)
}

// isStatementStart reports whether a token can begin a statement.
func isStatementStart(t lexer.TokenType) bool {
	switch t {
	case lexer.LBRACE, lexer.LET, lexer.CONST, lexer.VAR, lexer.FUNCTION, lexer.CLASS,
		lexer.IF, lexer.FOR, lexer.WHILE, lexer.DO, lexer.RETURN, lexer.BREAK, lexer.CONTINUE,
		lexer.THROW, lexer.TRY, lexer.SWITCH, lexer.IMPORT, lexer.EXPORT, lexer.AT,
		// expression starts
		lexer.IDENT, lexer.NUMBER, lexer.BIGINT, lexer.STRING, lexer.PRIVATE_NAME,
		lexer.TRUE, lexer.FALSE, lexer.NULL, lexer.UNDEFINED, lexer.NEW, lexer.TYPEOF,
		lexer.VOID, lexer.THIS, lexer.SUPER, lexer.AWAIT, lexer.YIELD, lexer.LPAREN,
		lexer.LBRACKET, lexer.TEMPLATE_NO_SUB, lexer.TEMPLATE_HEAD, lexer.REGEX,
		lexer.SLASH, lexer.SLASH_ASSIGN, lexer.PLUS, lexer.MINUS, lexer.NOT, lexer.BITNOT,
		lexer.INC, lexer.DEC, lexer.LT:
		return true
	}
	return false
}

// isClassMemberStart reports whether a token can begin a class member: a
// name (an identifier, a keyword, a string or number, a computed `[`, a
// private `#name`), a modifier, a decorator, or a generator's `*`.
func isClassMemberStart(t lexer.TokenType) bool {
	switch t {
	case lexer.IDENT, lexer.STRING, lexer.NUMBER, lexer.BIGINT, lexer.LBRACKET,
		lexer.PRIVATE_NAME, lexer.AT, lexer.STAR, lexer.STATIC, lexer.PUBLIC,
		lexer.PRIVATE, lexer.PROTECTED:
		return true
	}
	return lexer.IsKeyword(t)
}
