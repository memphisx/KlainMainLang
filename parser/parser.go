package parser

import (
	"KlainMainLang/diag"
	"slices"
	"sort"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/jsdoc"
	"KlainMainLang/lexer"
)

type Parser struct {
	// The scanner runs on demand: tokens holds what has been scanned so far
	// (filled by at()), states[i] the scanner state just before tokens[i], so
	// the parser can re-read a token under another ScanMode (rescanAt) and
	// undo that on backtracking (rewind).
	lx      *lexer.Lexer
	tokens  []lexer.Token
	states  []lexer.State
	rescans []int // indexes re-read under a non-default mode, ascending
	// bareArrow is the last block-bodied arrow function parsed outside
	// parentheses: no call, member or index access may follow it.
	bareArrow *ast.ArrowFunction
	// assertions collects the erased `as const` / `satisfies T` assertions.
	assertions map[ast.Expression]ast.Assertion
	// thisParam is the last parameter list's `this: T`; thisParams keeps
	// each function's.
	thisParam  *ast.TypeAnnotation
	thisParams map[ast.Node]*ast.TypeAnnotation
	pos        int
	// docAt is the token index whose Doc was last loaded into pendingDoc.
	docAt      int
	pendingDoc *jsdoc.Comment
	// namespaces accumulates TS `namespace X {...}` member sets (TDD-00095);
	// pendingTopLevel carries a namespace's desugared member declarations
	// past parseStatement's single-return shape — ParseProgram drains it
	// after every statement.
	namespaces      map[string]map[string]bool
	namespaceGroups []ast.NamespaceGroup
	pendingTopLevel []ast.Statement
	// nsAliases collects `import X = Y.Z` alias declarations (ADR-00456).
	nsAliases []ast.NSAliasDecl
	// ambientNames are the names of the erased ambient declarations.
	ambientNames []string
	// typeParamNames is every type parameter name parsed (Program.TypeParamNames).
	typeParamNames map[string]bool
	// evalCode marks a direct eval's source (ParseEval).
	evalCode bool
	// modules keeps a declaration file's ambient module blocks
	// (ParseLibrary).
	modules bool
	// noInFrom is the token index a `for` initializer starts at while it is
	// parsed (-1 otherwise): there, a relational `in` outside any bracket
	// ends the expression (the grammar's [~In]).
	noInFrom int
	// thisClass is the class whose body is being parsed: the `this` type in
	// an annotation there lowers to it.
	thisClass string
	// inCtorParams is set by parseClassDecl for the duration of a
	// constructor's parameter list, gating TS parameter-property modifiers
	// (`constructor(public x: number)`) — the only position TS allows them.
	inCtorParams bool

	// diags collects the file's diagnostics. contexts is the set of lists
	// being parsed (a bit per parsingContext), which error recovery
	// resynchronises to; speculating counts the speculative parses in
	// progress, where an error must reach the caller instead.
	diags        diag.List
	contexts     uint32
	speculating  int
	scanReported bool
	// declarations parses a declaration file (the builtin library,
	// TDD-00230 P3.1): its annotations stay type nodes, which only the
	// checker reads, so every TypeScript type form parses.
	declarations bool
}

func New(src string) *Parser {
	return &Parser{lx: lexer.New(src), docAt: -1, noInFrom: -1}
}

// inExcluded reports whether a relational `in` at the current token is
// excluded: a `for` initializer's, outside any bracket it opened.
func (p *Parser) inExcluded() bool {
	if p.noInFrom < 0 {
		return false
	}
	depth := 0
	for i := p.noInFrom; i < p.pos; i++ {
		switch p.at(i).Type {
		case lexer.LPAREN, lexer.LBRACKET, lexer.LBRACE, lexer.TEMPLATE_HEAD:
			depth++
		case lexer.RPAREN, lexer.RBRACKET, lexer.RBRACE, lexer.TEMPLATE_TAIL:
			depth--
		}
	}
	return depth == 0
}

func Parse(src string) (*ast.Program, error) {
	return New(src).ParseProgram()
}

// ParseEval parses the source of a direct eval: code that sees the names
// its call site sees, such as an enclosing class's private names.
func ParseEval(src string) (*ast.Program, error) {
	p := New(src)
	p.evalCode = true
	return p.ParseProgram()
}

// ParseLibrary parses one of this compiler's own declaration files
// (lib/*.d.ts): a declaration file whose `declare module "name" { … }`
// blocks are kept as the modules' declarations rather than erased.
func ParseLibrary(src string) (*ast.Program, error) {
	p := New(src)
	p.declarations = true
	p.modules = true
	return p.ParseProgram()
}

// ParseDeclarations parses a declaration file (`.d.ts`): every type stays a
// type node for the checker, with no conversion for code generation.
func ParseDeclarations(src string) (*ast.Program, error) {
	p := New(src)
	p.declarations = true
	return p.ParseProgram()
}

// --- Token stream helpers ---

// at returns token i, scanning up to it on demand. Scanning stops at EOF or
// at a scanning error (an ILLEGAL token); past either, EOF — so every parser
// loop terminates, and ParseProgram reports the ILLEGAL token's error.
func (p *Parser) at(i int) lexer.Token {
	for len(p.tokens) <= i {
		if n := len(p.tokens); n > 0 && (p.tokens[n-1].Type == lexer.EOF || p.tokens[n-1].Type == lexer.ILLEGAL) {
			last := p.tokens[n-1]
			return lexer.Token{Type: lexer.EOF, Line: last.Line, Col: last.Col, Pos: last.End, End: last.End}
		}
		p.states = append(p.states, p.lx.Mark())
		p.tokens = append(p.tokens, p.lx.Scan(lexer.ScanDefault))
	}
	return p.tokens[i]
}

func (p *Parser) peek() lexer.Token {
	t := p.at(p.pos)
	if t.Doc != "" && p.docAt != p.pos {
		p.pendingDoc = jsdoc.Parse(t.Doc)
		p.docAt = p.pos
	}
	return t
}

// rescanAt re-reads token i under mode, discarding everything scanned after
// it. The parser calls it where the grammar settles an ambiguity the scanner
// could not: a `>` that is part of `>>`/`>=`/`>>=`, a `/` that starts a regex.
func (p *Parser) rescanAt(i int, mode lexer.ScanMode) lexer.Token {
	p.at(i)
	st := p.states[i]
	p.tokens, p.states = p.tokens[:i], p.states[:i]
	p.lx.Rewind(st)
	p.states = append(p.states, st)
	p.tokens = append(p.tokens, p.lx.Scan(mode))
	p.rescans = append(p.rescans[:sort.SearchInts(p.rescans, i)], i)
	return p.tokens[i]
}

// rewind backtracks to token index i. Tokens from i on that a speculative
// parse re-read under another mode are dropped, so the next attempt scans
// them afresh.
func (p *Parser) rewind(i int) {
	p.pos = i
	k := sort.SearchInts(p.rescans, i)
	if k == len(p.rescans) {
		return
	}
	r := p.rescans[k]
	p.lx.Rewind(p.states[r])
	p.tokens, p.states, p.rescans = p.tokens[:r], p.states[:r], p.rescans[:k]
}

// peekOperator returns the current token read as an operator: a `>` glued
// with what follows it (`>>`, `>>>`, `>=`, `>>=`, `>>>=`), a `/` as division.
func (p *Parser) peekOperator() lexer.Token {
	switch t := p.peek(); t.Type {
	case lexer.GT:
		return p.rescanAt(p.pos, lexer.ScanGlueGreater)
	case lexer.REGEX:
		return p.rescanAt(p.pos, lexer.ScanNoRegex)
	default:
		return t
	}
}

// peekPrimary returns the current token read as the start of an operand: a
// `/` or `/=` there begins a regular-expression literal.
func (p *Parser) peekPrimary() lexer.Token {
	switch t := p.peek(); t.Type {
	case lexer.SLASH, lexer.SLASH_ASSIGN:
		return p.rescanAt(p.pos, lexer.ScanRegex)
	default:
		return t
	}
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
		return lexer.Token{}, p.errAt(t, diag.ExpectedToken, typ, t.Type)
	}
	return p.advance(), nil
}

// expectGT consumes the `>` closing a type-argument or type-parameter list.
// The scanner never merges `>`s (only an operator position glues them), so
// `Array<Promise<T>>` closes with two plain GT tokens.
func (p *Parser) expectGT(context string) error {
	t := p.peek()
	if t.Type != lexer.GT {
		return p.errAt(t, diag.ExpectedCloseAngle, context)
	}
	p.advance()
	return nil
}

// parseTypeParamList parses a declaration-site `<T>` or `<K, V>`
// type-parameter list — shared by function/interface/class/type-alias
// declarations (TDD-00010 V1, extended to N parameters by TDD-00037). Assumes
// the caller has already checked the current token is '<'. A type parameter may
// carry a `extends X` constraint (TDD-00113); the returned constraints slice is
// positionally aligned with names (a nil entry means unconstrained).
func (p *Parser) parseTypeParamList(context string) ([]string, []*ast.TypeAnnotation, error) {
	p.advance() // consume '<'
	var names []string
	var constraints []*ast.TypeAnnotation
	for {
		nameTok, err := p.expect(lexer.IDENT)
		if err != nil {
			return nil, nil, err
		}
		names = append(names, nameTok.Literal)
		p.declareTypeParam(nameTok.Literal)
		var constraint *ast.TypeAnnotation
		if p.check(lexer.EXTENDS) {
			p.advance() // consume 'extends'
			constraint, err = p.parseTypeAnnotation("ts")
			if err != nil {
				return nil, nil, err
			}
		}
		constraints = append(constraints, constraint)
		if p.match(lexer.ASSIGN) {
			// A default (`<T = string>`): parsed; a missing type argument is
			// erased here as before.
			if _, err := p.parseType(); err != nil {
				return nil, nil, err
			}
		}
		if !p.match(lexer.COMMA) || p.check(lexer.GT) {
			break // a trailing comma may end the list
		}
	}
	if err := p.expectGT(context); err != nil {
		return nil, nil, err
	}
	return names, constraints, nil
}

// parseSemicolon ends a statement: an explicit `;`, or — automatic semicolon
// insertion — none, when the next token is `}`, the end of input, or starts a
// new line. Anything else on the same line is an error.
func (p *Parser) parseSemicolon() error {
	if !p.canParseSemicolon() {
		t := p.peek()
		return p.errAt(t, diag.ExpectedSemicolon, t.Type)
	}
	p.match(lexer.SEMICOLON)
	return nil
}

// canParseSemicolon reports whether a statement can end here: at a `;`, or by
// automatic insertion before `}`, at the end of input, or at a line break.
func (p *Parser) canParseSemicolon() bool {
	t := p.peek()
	return t.Type == lexer.SEMICOLON || t.Type == lexer.RBRACE || t.Type == lexer.EOF || t.HasPrecedingLineBreak()
}

func posOf(t lexer.Token) ast.Pos { return ast.Pos{Line: t.Line, Col: t.Col} }

// peekNth returns the n-th token from the current position (0 = peek()).
func (p *Parser) peekNth(n int) lexer.Token {
	return p.at(p.pos + n)
}

// isWord reports whether the n-th token ahead is the identifier w. Contextual
// keywords (`async`, `abstract`, `get`, `set`, `of`, …) are scanned as plain
// identifiers and recognised here, only where their keyword reading applies,
// so a variable, field or function may still be named `async`.
func (p *Parser) isWord(n int, w string) bool {
	t := p.peekNth(n)
	return t.Type == lexer.IDENT && t.Literal == w
}

// sameLineAs reports whether the n-th token ahead has no line break before it
// — the contextual-keyword forms `async function`, `async x =>` and
// `abstract class` require their words on one line.
func (p *Parser) sameLine(n int) bool { return !p.peekNth(n).HasPrecedingLineBreak() }

// asyncFunctionAhead reports whether the current `async` starts an async
// function or arrow rather than naming an identifier: `async function`,
// `async x =>`, `async (…) =>`, `async <T>(…) =>` — with no line break after
// `async`.
func (p *Parser) asyncFunctionAhead() bool {
	if !p.isWord(0, "async") || !p.sameLine(1) {
		return false
	}
	switch p.peekNth(1).Type {
	case lexer.FUNCTION, lexer.LT:
		return true
	case lexer.IDENT:
		return p.peekNth(2).Type == lexer.ARROW
	case lexer.LPAREN:
		return p.parenGroupFollowedByArrowAt(1)
	}
	return false
}

// scanErrorOr reports a scanning error the parse ran into in place of the
// parse error it caused: the unreadable input is the real problem.
func (p *Parser) scanErrorOr(err error) error {
	for _, t := range p.tokens {
		if t.Type == lexer.ILLEGAL && t.Err != nil {
			return t.Err
		}
	}
	return err
}

// errAt reports m at token t. A token built by the parser rather than
// scanned has no byte range; its offset comes from its line and column.
func (p *Parser) errAt(t lexer.Token, m *diag.Message, args ...any) *diag.Diagnostic {
	start, end := t.Pos, t.End
	if end == 0 {
		start, end = -1, -1
	}
	return diag.New(m, diag.Span{Pos: diag.Pos{Line: t.Line, Col: t.Col}, Start: start, End: end}, args...)
}

// errAtPos reports m at a node's position. Its byte offset is filled in when
// the parser collects it (fillOffset); it spans nothing.
func errAtPos(pos ast.Pos, m *diag.Message, args ...any) *diag.Diagnostic {
	return diag.New(m, diag.Span{Pos: diag.Pos{Line: pos.Line, Col: pos.Col}, Start: -1, End: -1}, args...)
}

// fillOffset gives a diagnostic reported at a node position its byte offset.
func (p *Parser) fillOffset(d *diag.Diagnostic) {
	if d.Start < 0 {
		d.Start = p.lx.Offset(d.Pos.Line, d.Pos.Col)
		d.End = d.Start
	}
}

func (p *Parser) takeDoc() *jsdoc.Comment {
	d := p.pendingDoc
	p.pendingDoc = nil
	return d
}

// collectTypedefs turns every `@typedef`/`@callback` JSDoc comment the
// scanner met into a synthesized `type Name = ...` declaration (TDD-00125
// Stage 2), in source order. A typedef declares a type rather than documenting
// the next token, so it is read from the scanner's record of all comments, not
// from the one comment a token carries.
func (p *Parser) collectTypedefs() []ast.Statement {
	offs := make([]int, 0, len(p.lx.Docs))
	for off := range p.lx.Docs {
		offs = append(offs, off)
	}
	sort.Ints(offs)
	var out []ast.Statement
	for _, off := range offs {
		for _, d := range jsdoc.Parse(p.lx.Docs[off]).Typedefs() {
			if stmt := p.synthTypedef(d, ast.Pos{}); stmt != nil {
				out = append(out, stmt)
			}
		}
	}
	return out
}

// synthTypedef builds the `type Name = ...` AST node for one JSDoc typedef.
func (p *Parser) synthTypedef(d jsdoc.TypedefDecl, pos ast.Pos) ast.Statement {
	if d.Name == "" {
		return nil
	}
	var ta *ast.TypeAnnotation
	switch d.Kind {
	case "alias":
		ta = jsdocTypeAnnotation(d.Base)
	case "object":
		fields := make([]ast.AnnotField, 0, len(d.Fields))
		for _, f := range d.Fields {
			fields = append(fields, ast.AnnotField{Name: f.Name, Type: jsdocTypeAnnotation(f.Type)})
		}
		ta = &ast.TypeAnnotation{Source: "jsdoc", Fields: fields}
	case "callback":
		params := make([]ast.TypeAnnotation, 0, len(d.Fields))
		for _, f := range d.Fields {
			params = append(params, *jsdocTypeAnnotation(f.Type))
		}
		ret := jsdocTypeAnnotation(d.Return)
		if ret == nil {
			ret = &ast.TypeAnnotation{Name: "void", Source: "jsdoc"}
		}
		ta = &ast.TypeAnnotation{
			Source:      "jsdoc",
			IsFuncType:  true,
			FuncParams:  params,
			FuncRetType: ret,
		}
	default:
		return nil
	}
	return ast.NewTypeAliasDeclaration(d.Name, ta, pos)
}

// --- Program ---

func (p *Parser) ParseProgram() (*ast.Program, error) {
	prog := &ast.Program{}
	p.parseStatementList(ctxSourceElements, func(stmt ast.Statement) error {
		// Desugar a top-level static `require('<literal>')` into the equivalent
		// import declaration so the resolver/codegen handle it unchanged. Only
		// at top level — a nested `require` stays an ordinary call (the lazy
		// form, out of scope for this static rewrite).
		stmt, err := p.desugarRequire(stmt)
		if err != nil {
			p.pendingTopLevel = nil
			return err
		}
		prog.Body = append(prog.Body, stmt)
		if len(p.pendingTopLevel) > 0 {
			prog.Body = append(prog.Body, p.pendingTopLevel...)
			p.pendingTopLevel = nil
		}
		return nil
	})
	// Input the scanner could not read is an error even when the parse got
	// past it.
	if err := p.scanErrorOr(nil); err != nil {
		p.report(err)
	}
	if err := p.diags.Err(); err != nil {
		return nil, err
	}
	// A `@typedef`/`@callback` comment declares a named type, not documentation
	// for the next statement: prepend the synthesized aliases so they are
	// registered before any use site. Every comment has been scanned by now.
	prog.Body = append(p.collectTypedefs(), prog.Body...)
	prog.Namespaces = p.namespaces
	prog.NamespaceGroups = p.namespaceGroups
	prog.Assertions = p.assertions
	prog.ThisParams = p.thisParams
	prog.NSAliases = p.nsAliases
	prog.AmbientNames = p.ambientNames
	prog.TypeParamNames = p.typeParamNames
	if err := yieldOutsideGenerator(prog, false); err != nil {
		return nil, err
	}
	if !p.evalCode {
		// Direct eval code sees the enclosing class's private names.
		if err := privateNameErrors(prog, nil); err != nil {
			return nil, err
		}
	}
	for _, st := range prog.Body {
		if err := nestedModuleDecl(st, true); err != nil {
			return nil, err
		}
	}
	if err := labelErrors(prog, nil); err != nil {
		return nil, err
	}
	if err := coverInitErrors(prog, false); err != nil {
		return nil, err
	}
	if programIsStrict(prog) {
		if err := strictBindingError(prog.Body); err != nil {
			return nil, err
		}
		if err := strictErrors(prog); err != nil {
			return nil, err
		}
	}
	return prog, nil
}

// yieldOutsideGenerator is the early error for a `yield` expression outside
// a generator body (tsc's TS1163): a function declaration or expression sets
// the context by being a generator or not, and an arrow is never one.
func yieldOutsideGenerator(n ast.Node, inGenerator bool) error {
	switch x := n.(type) {
	case *ast.YieldExpression:
		if !inGenerator {
			return errAtPos(x.GetPos(), diag.YieldOutsideGenerator)
		}
	case *ast.FunctionDeclaration:
		// A method's decorators run where the class is defined, not in it.
		outer := map[ast.Node]bool{}
		for _, d := range x.Decorators {
			if err := yieldOutsideGenerator(d, inGenerator); err != nil {
				return err
			}
			outer[d] = true
		}
		var err error
		ast.ForEachChild(n, func(ch ast.Node) bool {
			if err == nil && !outer[ch] {
				err = yieldOutsideGenerator(ch, x.IsGenerator)
			}
			return err == nil
		})
		return err
	case *ast.FunctionExpression:
		inGenerator = x.IsGenerator
	case *ast.ArrowFunction:
		inGenerator = false
	}
	var err error
	ast.ForEachChild(n, func(ch ast.Node) bool {
		if err == nil {
			err = yieldOutsideGenerator(ch, inGenerator)
		}
		return err == nil
	})
	return err
}

// privateNameErrors is the early errors of private names: a `#name` access
// must name a member an enclosing class declares, and `delete` cannot take
// one. scopes are the enclosing classes' private names, innermost last.
func privateNameErrors(n ast.Node, scopes []map[string]bool) error {
	switch x := n.(type) {
	case *ast.ClassExpression:
		return privateNameErrors(x.Decl, scopes)
	case *ast.ClassDeclaration:
		names := map[string]bool{}
		for _, f := range x.Fields {
			names[f.Name] = true
		}
		for _, m := range x.Methods {
			names[m.Name] = true
		}
		for _, a := range x.AutoAccessors {
			names[a.Name] = true
		}
		scopes = append(scopes[:len(scopes):len(scopes)], names)
	case *ast.MemberExpression:
		if strings.HasPrefix(x.Property, "#") {
			declared := false
			for _, s := range scopes {
				declared = declared || s[x.Property]
			}
			if !declared {
				return errAtPos(x.GetPos(), diag.PrivateNameUndeclared, x.Property)
			}
		}
	case *ast.UnaryExpression:
		if m, ok := x.Arg.(*ast.MemberExpression); ok && x.Op == "delete" && strings.HasPrefix(m.Property, "#") {
			return errAtPos(x.GetPos(), diag.DeletePrivateName)
		}
	}
	var err error
	ast.ForEachChild(n, func(ch ast.Node) bool {
		if err == nil {
			err = privateNameErrors(ch, scopes)
		}
		return err == nil
	})
	return err
}

// programIsStrict reports whether the whole program is strict code: it
// opens with a "use strict" directive, or is a module (it imports or
// exports).
func programIsStrict(prog *ast.Program) bool {
	if len(prog.Body) > 0 {
		if es, ok := prog.Body[0].(*ast.ExpressionStatement); ok {
			if sl, ok := es.Expr.(*ast.StringLiteral); ok && sl.Value == "use strict" {
				return true
			}
		}
	}
	for _, st := range prog.Body {
		switch st.(type) {
		case *ast.ImportDeclaration, *ast.ExportDeclaration, *ast.ExportFromDeclaration:
			return true
		}
	}
	return false
}

// strictErrors is the early errors of strict code the parser does not
// raise while parsing: `delete` of a plain name, and a call as the target of
// an assignment or update (web-compatible only in sloppy code).
func strictErrors(n ast.Node) error {
	switch x := n.(type) {
	case *ast.UnaryExpression:
		if _, ok := x.Arg.(*ast.Identifier); ok && x.Op == "delete" {
			return errAtPos(x.GetPos(), diag.StrictDeleteName)
		}
	case *ast.AssignmentExpression:
		if _, ok := x.Left.(*ast.CallExpression); ok {
			return errAtPos(x.GetPos(), diag.InvalidAssignTarget)
		}
	case *ast.UpdateExpression:
		if _, ok := x.Arg.(*ast.CallExpression); ok {
			return errAtPos(x.GetPos(), diag.InvalidAssignTarget)
		}
	}
	var err error
	ast.ForEachChild(n, func(ch ast.Node) bool {
		if err == nil {
			err = strictErrors(ch)
		}
		return err == nil
	})
	return err
}

// labelErrors is the early error of a `break` or `continue` naming a label
// no enclosing statement of the same function has. labels are the
// enclosing labels; a function starts with none.
func labelErrors(n ast.Node, labels []string) error {
	switch x := n.(type) {
	case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration:
		labels = nil
	case *ast.LabeledStatement:
		labels = append(labels[:len(labels):len(labels)], x.Label)
	case *ast.BreakStatement:
		if x.Label != "" && !slices.Contains(labels, x.Label) {
			return errAtPos(x.GetPos(), diag.UndefinedLabel, "break", x.Label)
		}
	case *ast.ContinueStatement:
		if x.Label != "" && !slices.Contains(labels, x.Label) {
			return errAtPos(x.GetPos(), diag.UndefinedLabel, "continue", x.Label)
		}
	}
	var err error
	ast.ForEachChild(n, func(ch ast.Node) bool {
		if err == nil {
			err = labelErrors(ch, labels)
		}
		return err == nil
	})
	return err
}

// coverInitErrors is the early error of a shorthand property with a default
// (`{ x = 1 }`) anywhere but an assignment pattern, where it is a target's
// default. inTarget is whether n is (part of) an assignment target.
func coverInitErrors(n ast.Node, inTarget bool) error {
	switch x := n.(type) {
	case *ast.AssignmentExpression:
		if x.Op == "=" {
			if err := coverInitErrors(x.Left, true); err != nil {
				return err
			}
			return coverInitErrors(x.Right, false)
		}
	case *ast.ObjectLiteral:
		for _, p := range x.Properties {
			if p.CoverInit && !inTarget {
				return errAtPos(p.Value.GetPos(), diag.CoverInitializedName)
			}
			if p.KeyExpr != nil {
				if err := coverInitErrors(p.KeyExpr, false); err != nil {
					return err
				}
			}
			if p.Value != nil {
				if err := coverInitErrors(p.Value, inTarget && p.AccessorKind == ""); err != nil {
					return err
				}
			}
		}
		return nil
	case *ast.ArrayLiteral:
		for _, el := range x.Elements {
			if err := coverInitErrors(el, inTarget); err != nil {
				return err
			}
		}
		return nil
	case *ast.SpreadElement:
		return coverInitErrors(x.Arg, inTarget)
	}
	var err error
	ast.ForEachChild(n, func(ch ast.Node) bool {
		if err == nil {
			err = coverInitErrors(ch, false)
		}
		return err == nil
	})
	return err
}

// nestedModuleDecl is the early error of an import or export declaration
// anywhere but a module's top level (`for (;;) export default 1`).
func nestedModuleDecl(n ast.Node, top bool) error {
	if m, ok := n.(*ast.AmbientModuleDeclaration); ok {
		// A declared module's body is that module's own top level.
		for _, st := range m.Body {
			if err := nestedModuleDecl(st, true); err != nil {
				return err
			}
		}
		return nil
	}
	switch n.(type) {
	case *ast.ImportDeclaration, *ast.ExportDeclaration, *ast.ExportFromDeclaration:
		if !top {
			return errAtPos(n.GetPos(), diag.ModuleDeclNotTopLevel)
		}
	}
	var err error
	ast.ForEachChild(n, func(ch ast.Node) bool {
		if err == nil {
			err = nestedModuleDecl(ch, false)
		}
		return err == nil
	})
	return err
}

// declareTypeParam records a type parameter name (Program.TypeParamNames).
func (p *Parser) declareTypeParam(name string) {
	if p.typeParamNames == nil {
		p.typeParamNames = map[string]bool{}
	}
	p.typeParamNames[name] = true
}
