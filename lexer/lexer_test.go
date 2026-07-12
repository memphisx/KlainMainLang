package lexer_test

import (
	"testing"

	"KlainMainLang/lexer"
)

// tok is a compact token representation for assertions.
type tok struct {
	typ lexer.TokenType
	lit string
}

func tokenize(t *testing.T, src string) []tok {
	t.Helper()
	ts, err := lexer.Tokenize(src)
	if err != nil {
		t.Fatalf("Tokenize(%q): %v", src, err)
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
	assertTokens(t, "a / b", []tok{{lexer.IDENT, "a"}, {lexer.SLASH, "/"}, {lexer.IDENT, "b"}})
	assertTokens(t, "a % b", []tok{{lexer.IDENT, "a"}, {lexer.PERCENT, "%"}, {lexer.IDENT, "b"}})
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

func TestJSDocComment(t *testing.T) {
	toks := tokenize(t, "/** @type {number} */")
	if len(toks) != 1 || toks[0].typ != lexer.JSDOC {
		t.Fatalf("expected one JSDOC token, got %v", toks)
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

func TestUnexpectedCharError(t *testing.T) {
	_, err := lexer.Tokenize("@bad")
	if err == nil {
		t.Fatal("expected error for '@', got nil")
	}
}
