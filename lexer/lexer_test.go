package lexer_test

import (
	"errors"
	"testing"

	"KlainMainLang/lexer"
)

// tok is a compact token representation for assertions.
type tok struct {
	typ lexer.TokenType
	lit string
}

// scanAll scans src the way the parser does in operator position: a `>` is
// rescanned with gluing so `>>`, `>=`, `>>>=` come back whole. An ILLEGAL
// token is returned as an error.
func scanAll(src string) ([]lexer.Token, error) {
	l := lexer.New(src)
	var out []lexer.Token
	for {
		st := l.Mark()
		t := l.Scan(lexer.ScanDefault)
		if t.Type == lexer.GT {
			l.Rewind(st)
			t = l.Scan(lexer.ScanGlueGreater)
		}
		if t.Type == lexer.ILLEGAL {
			return nil, errors.New(t.Literal)
		}
		out = append(out, t)
		if t.Type == lexer.EOF {
			return out, nil
		}
	}
}

func tokenize(t *testing.T, src string) []tok {
	t.Helper()
	ts, err := scanAll(src)
	if err != nil {
		t.Fatalf("scan(%q): %v", src, err)
	}
	var out []tok
	for _, token := range ts {
		if token.Type == lexer.EOF {
			break
		}
		out = append(out, tok{token.Type, token.Literal})
	}
	return out
}

func assertTokens(t *testing.T, src string, want []tok) {
	t.Helper()
	got := tokenize(t, src)
	if len(got) != len(want) {
		t.Fatalf("src=%q: got %d tokens, want %d\n  got:  %v\n  want: %v", src, len(got), len(want), got, want)
	}
	for i, w := range want {
		g := got[i]
		if g.typ != w.typ || g.lit != w.lit {
			t.Errorf("token[%d]: got {%v %q}, want {%v %q}", i, g.typ, g.lit, w.typ, w.lit)
		}
	}
}

func TestNumbers(t *testing.T) {
	assertTokens(t, "42", []tok{{lexer.NUMBER, "42"}})
	assertTokens(t, "3.14", []tok{{lexer.NUMBER, "3.14"}})
	assertTokens(t, "0", []tok{{lexer.NUMBER, "0"}})
}

// TestExponentNumbers covers ES DecimalLiteral exponent notation (e/E, optional
// sign, digits, with numeric separators) — the whole literal is one NUMBER
// token, and a bare `e` at a non-exponent position stays an identifier.
func TestExponentNumbers(t *testing.T) {
	assertTokens(t, "1e3", []tok{{lexer.NUMBER, "1e3"}})
	assertTokens(t, "1.5e3", []tok{{lexer.NUMBER, "1.5e3"}})
	assertTokens(t, "1E3", []tok{{lexer.NUMBER, "1E3"}})
	assertTokens(t, "2e-2", []tok{{lexer.NUMBER, "2e-2"}})
	assertTokens(t, "6.022e+23", []tok{{lexer.NUMBER, "6.022e+23"}})
	assertTokens(t, "1_000e1", []tok{{lexer.NUMBER, "1000e1"}}) // separators stripped in the token literal
	// A trailing `e` with no following digit is not an exponent: `1` then `e`.
	assertTokens(t, "1 + e", []tok{{lexer.NUMBER, "1"}, {lexer.PLUS, "+"}, {lexer.IDENT, "e"}})
}

func TestStrings(t *testing.T) {
	assertTokens(t, `"hello"`, []tok{{lexer.STRING, "hello"}})
	assertTokens(t, `'world'`, []tok{{lexer.STRING, "world"}})
	assertTokens(t, `"tab\there"`, []tok{{lexer.STRING, "tab\there"}})
	assertTokens(t, `"new\nline"`, []tok{{lexer.STRING, "new\nline"}})
}

func TestKeywords(t *testing.T) {
	cases := []struct {
		src string
		typ lexer.TokenType
	}{
		{"let", lexer.LET},
		{"const", lexer.CONST},
		{"var", lexer.VAR},
		{"function", lexer.FUNCTION},
		{"return", lexer.RETURN},
		{"if", lexer.IF},
		{"else", lexer.ELSE},
		{"for", lexer.FOR},
		{"while", lexer.WHILE},
		{"true", lexer.TRUE},
		{"false", lexer.FALSE},
		{"null", lexer.NULL},
		{"new", lexer.NEW},
		{"typeof", lexer.TYPEOF},
		{"switch", lexer.SWITCH},
		{"case", lexer.CASE},
		{"break", lexer.BREAK},
		{"continue", lexer.CONTINUE},
		{"class", lexer.CLASS},
		{"this", lexer.THIS},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			assertTokens(t, c.src, []tok{{c.typ, c.src}})
		})
	}
}

func TestArithmeticOperators(t *testing.T) {
	assertTokens(t, "a + b", []tok{{lexer.IDENT, "a"}, {lexer.PLUS, "+"}, {lexer.IDENT, "b"}})
	assertTokens(t, "a - b", []tok{{lexer.IDENT, "a"}, {lexer.MINUS, "-"}, {lexer.IDENT, "b"}})
	assertTokens(t, "a * b", []tok{{lexer.IDENT, "a"}, {lexer.STAR, "*"}, {lexer.IDENT, "b"}})
	assertTokens(t, "a *= b", []tok{{lexer.IDENT, "a"}, {lexer.STAR_ASSIGN, "*="}, {lexer.IDENT, "b"}})
	assertTokens(t, "a ** b", []tok{{lexer.IDENT, "a"}, {lexer.POW, "**"}, {lexer.IDENT, "b"}})
	assertTokens(t, "a **= b", []tok{{lexer.IDENT, "a"}, {lexer.POW_ASSIGN, "**="}, {lexer.IDENT, "b"}})
	assertTokens(t, "a / b", []tok{{lexer.IDENT, "a"}, {lexer.SLASH, "/"}, {lexer.IDENT, "b"}})
	assertTokens(t, "a % b", []tok{{lexer.IDENT, "a"}, {lexer.PERCENT, "%"}, {lexer.IDENT, "b"}})
	assertTokens(t, "a %= b", []tok{{lexer.IDENT, "a"}, {lexer.PERCENT_ASSIGN, "%="}, {lexer.IDENT, "b"}})
}

func TestComparisonOperators(t *testing.T) {
	assertTokens(t, "a === b", []tok{{lexer.IDENT, "a"}, {lexer.STRICT_EQ, "==="}, {lexer.IDENT, "b"}})
	assertTokens(t, "a !== b", []tok{{lexer.IDENT, "a"}, {lexer.STRICT_NEQ, "!=="}, {lexer.IDENT, "b"}})
	assertTokens(t, "a <= b", []tok{{lexer.IDENT, "a"}, {lexer.LTE, "<="}, {lexer.IDENT, "b"}})
	assertTokens(t, "a >= b", []tok{{lexer.IDENT, "a"}, {lexer.GTE, ">="}, {lexer.IDENT, "b"}})
}

func TestLogicalOperators(t *testing.T) {
	assertTokens(t, "a && b", []tok{{lexer.IDENT, "a"}, {lexer.AND, "&&"}, {lexer.IDENT, "b"}})
	assertTokens(t, "a || b", []tok{{lexer.IDENT, "a"}, {lexer.OR, "||"}, {lexer.IDENT, "b"}})
	assertTokens(t, "!a", []tok{{lexer.NOT, "!"}, {lexer.IDENT, "a"}})
}

func TestBitwiseOperators(t *testing.T) {
	assertTokens(t, "a & b", []tok{{lexer.IDENT, "a"}, {lexer.BITAND, "&"}, {lexer.IDENT, "b"}})
	assertTokens(t, "a | b", []tok{{lexer.IDENT, "a"}, {lexer.BITOR, "|"}, {lexer.IDENT, "b"}})
	assertTokens(t, "a ^ b", []tok{{lexer.IDENT, "a"}, {lexer.BITXOR, "^"}, {lexer.IDENT, "b"}})
	assertTokens(t, "~a", []tok{{lexer.BITNOT, "~"}, {lexer.IDENT, "a"}})
	assertTokens(t, "a << b", []tok{{lexer.IDENT, "a"}, {lexer.LSHIFT, "<<"}, {lexer.IDENT, "b"}})
	assertTokens(t, "a >> b", []tok{{lexer.IDENT, "a"}, {lexer.RSHIFT, ">>"}, {lexer.IDENT, "b"}})
	assertTokens(t, "a >>> b", []tok{{lexer.IDENT, "a"}, {lexer.URSHIFT, ">>>"}, {lexer.IDENT, "b"}})
}

func TestBitwiseCompoundAssign(t *testing.T) {
	assertTokens(t, "a &= b", []tok{{lexer.IDENT, "a"}, {lexer.AND_ASSIGN, "&="}, {lexer.IDENT, "b"}})
	assertTokens(t, "a |= b", []tok{{lexer.IDENT, "a"}, {lexer.OR_ASSIGN, "|="}, {lexer.IDENT, "b"}})
	assertTokens(t, "a ^= b", []tok{{lexer.IDENT, "a"}, {lexer.XOR_ASSIGN, "^="}, {lexer.IDENT, "b"}})
	assertTokens(t, "a <<= b", []tok{{lexer.IDENT, "a"}, {lexer.LSHIFT_ASSIGN, "<<="}, {lexer.IDENT, "b"}})
	assertTokens(t, "a >>= b", []tok{{lexer.IDENT, "a"}, {lexer.RSHIFT_ASSIGN, ">>="}, {lexer.IDENT, "b"}})
	assertTokens(t, "a >>>= b", []tok{{lexer.IDENT, "a"}, {lexer.URSHIFT_ASSIGN, ">>>="}, {lexer.IDENT, "b"}})
}

func TestShiftAmbiguity(t *testing.T) {
	// >>> must not be parsed as >> then >
	assertTokens(t, "1 >>> 0", []tok{{lexer.NUMBER, "1"}, {lexer.URSHIFT, ">>>"}, {lexer.NUMBER, "0"}})
	// >>= must not be parsed as >> then =
	assertTokens(t, "x >>= 2", []tok{{lexer.IDENT, "x"}, {lexer.RSHIFT_ASSIGN, ">>="}, {lexer.NUMBER, "2"}})
}

func TestIncrementDecrement(t *testing.T) {
	assertTokens(t, "i++", []tok{{lexer.IDENT, "i"}, {lexer.INC, "++"}})
	assertTokens(t, "i--", []tok{{lexer.IDENT, "i"}, {lexer.DEC, "--"}})
}

func TestArrow(t *testing.T) {
	assertTokens(t, "x => x", []tok{{lexer.IDENT, "x"}, {lexer.ARROW, "=>"}, {lexer.IDENT, "x"}})
}

func TestArrowNotEquals(t *testing.T) {
	// => is ARROW, == is EQ — make sure => doesn't lex as = then >
	assertTokens(t, "=>", []tok{{lexer.ARROW, "=>"}})
	assertTokens(t, "==", []tok{{lexer.EQ, "=="}})
}

func TestTemplateLiteralNoSub(t *testing.T) {
	assertTokens(t, "`hello`", []tok{{lexer.TEMPLATE_NO_SUB, "hello"}})
}

func TestTemplateLiteralWithSub(t *testing.T) {
	toks := tokenize(t, "`x = ${x}`")
	if len(toks) != 3 {
		t.Fatalf("want 3 tokens, got %d: %v", len(toks), toks)
	}
	if toks[0].typ != lexer.TEMPLATE_HEAD || toks[0].lit != "x = " {
		t.Errorf("token[0]: got {%v %q}", toks[0].typ, toks[0].lit)
	}
	if toks[1].typ != lexer.IDENT || toks[1].lit != "x" {
		t.Errorf("token[1]: got {%v %q}", toks[1].typ, toks[1].lit)
	}
	if toks[2].typ != lexer.TEMPLATE_TAIL || toks[2].lit != "" {
		t.Errorf("token[2]: got {%v %q}", toks[2].typ, toks[2].lit)
	}
}

func TestLineComment(t *testing.T) {
	// Comments are skipped; only the identifier survives.
	assertTokens(t, "x // ignored\ny", []tok{{lexer.IDENT, "x"}, {lexer.IDENT, "y"}})
}

func TestBlockComment(t *testing.T) {
	assertTokens(t, "x /* ignored */ y", []tok{{lexer.IDENT, "x"}, {lexer.IDENT, "y"}})
}

func TestJSDocCommentAttachesToTheNextToken(t *testing.T) {
	ts, err := scanAll("/** @type {number} */\nlet x")
	if err != nil {
		t.Fatal(err)
	}
	if ts[0].Type != lexer.LET || ts[0].Doc != "@type {number}" {
		t.Fatalf("doc not attached to the next token: %+v", ts[0])
	}
	if ts[1].Doc != "" {
		t.Fatalf("doc leaked onto a later token: %+v", ts[1])
	}
}

func TestGreaterIsSingleUnlessGlued(t *testing.T) {
	l := lexer.New("a >>= b")
	l.Scan(lexer.ScanDefault)
	st := l.Mark()
	if g := l.Scan(lexer.ScanDefault); g.Type != lexer.GT || g.End-g.Pos != 1 {
		t.Fatalf("default scan of `>>=` should give one GT, got %+v", g)
	}
	l.Rewind(st)
	if g := l.Scan(lexer.ScanGlueGreater); g.Type != lexer.RSHIFT_ASSIGN {
		t.Fatalf("glued scan should give >>=, got %+v", g)
	}
}

func TestRegexOrDivisionByMode(t *testing.T) {
	// after `)` the default reading of `/` is division
	l := lexer.New(") /x/g")
	l.Scan(lexer.ScanDefault)
	st := l.Mark()
	if d := l.Scan(lexer.ScanDefault); d.Type != lexer.SLASH {
		t.Fatalf("default after ')' should be SLASH, got %+v", d)
	}
	l.Rewind(st)
	if r := l.Scan(lexer.ScanRegex); r.Type != lexer.REGEX || r.Literal != "x" || r.RegexFlags != "g" {
		t.Fatalf("regex scan: %+v", r)
	}
}

func TestPrecedingLineBreakAndOffsets(t *testing.T) {
	ts, err := scanAll("a\n++b /* x\n */ c\u2028d\r\ne é")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		lit string
		nl  bool
	}{{"a", false}, {"++", true}, {"b", false}, {"c", true}, {"d", true}, {"e", true}, {"é", false}}
	for i, w := range want {
		if ts[i].Literal != w.lit || ts[i].HasPrecedingLineBreak() != w.nl {
			t.Errorf("token %d: got %q nl=%v, want %q nl=%v", i, ts[i].Literal, ts[i].HasPrecedingLineBreak(), w.lit, w.nl)
		}
	}
	src := "a\n++b /* x\n */ c\u2028d\r\ne é"
	last := ts[6]
	if src[last.Pos:last.End] != "é" {
		t.Errorf("byte offsets wrong: [%d,%d) = %q", last.Pos, last.End, src[last.Pos:last.End])
	}
}

func TestEllipsis(t *testing.T) {
	assertTokens(t, "...args", []tok{{lexer.ELLIPSIS, "..."}, {lexer.IDENT, "args"}})
}

func TestPunctuation(t *testing.T) {
	assertTokens(t, "{ }", []tok{{lexer.LBRACE, "{"}, {lexer.RBRACE, "}"}})
	assertTokens(t, "[ ]", []tok{{lexer.LBRACKET, "["}, {lexer.RBRACKET, "]"}})
	assertTokens(t, "( )", []tok{{lexer.LPAREN, "("}, {lexer.RPAREN, ")"}})
	assertTokens(t, ";", []tok{{lexer.SEMICOLON, ";"}})
	assertTokens(t, ":", []tok{{lexer.COLON, ":"}})
	assertTokens(t, ",", []tok{{lexer.COMMA, ","}})
	assertTokens(t, ".", []tok{{lexer.DOT, "."}})
	assertTokens(t, "?", []tok{{lexer.QUESTION, "?"}})
}

func TestPrivateName(t *testing.T) {
	assertTokens(t, "#foo", []tok{{lexer.PRIVATE_NAME, "#foo"}})
	assertTokens(t, "this.#foo", []tok{{lexer.THIS, "this"}, {lexer.DOT, "."}, {lexer.PRIVATE_NAME, "#foo"}})
	// A bare '#' not immediately followed by an identifier-start character
	// isn't a private name — still an unhandled character, same as before
	// this token existed.
	_, err := scanAll("#1")
	if err == nil {
		t.Fatal("expected error for '#1' ('#' not followed by an identifier-start char), got nil")
	}
}

func TestUnexpectedCharError(t *testing.T) {
	// `\` outside a string/template is not a valid token start (`@` now lexes
	// as the decorator prefix, TDD-00161).
	_, err := scanAll("\\bad")
	if err == nil {
		t.Fatal("expected error for '\\', got nil")
	}
}

func TestLeadingBOM(t *testing.T) {
	// A UTF-8 BOM (U+FEFF) at the start of a file is common in editor output and
	// is whitespace in ECMAScript; it must not derail tokenization.
	assertTokens(t, "\uFEFFconst x = 5", []tok{
		{lexer.CONST, "const"},
		{lexer.IDENT, "x"},
		{lexer.ASSIGN, "="},
		{lexer.NUMBER, "5"},
	})
	// A stray BOM mid-stream is likewise treated as whitespace.
	assertTokens(t, "a\uFEFFb", []tok{
		{lexer.IDENT, "a"},
		{lexer.IDENT, "b"},
	})
}

// A template's CR LF and lone CR are LF, in the cooked and the raw text
// alike (ECMAScript's TV and TRV).
func TestTemplateLineTerminatorsNormalized(t *testing.T) {
	l := lexer.New("`a\r\nb\rc`")
	tk, err := l.NextToken()
	if err != nil {
		t.Fatal(err)
	}
	if tk.Type != lexer.TEMPLATE_NO_SUB || tk.Literal != "a\nb\nc" || tk.Raw != "a\nb\nc" {
		t.Errorf("got {%v %q raw %q}, want cooked and raw \"a\\nb\\nc\"", tk.Type, tk.Literal, tk.Raw)
	}
}
