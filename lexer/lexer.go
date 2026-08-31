package lexer

import (
	"fmt"
	"strings"
	"unicode"
)

type Lexer struct {
	src           []rune
	pos           int
	line          int
	col           int
	templateStack []int // brace depth for each open ${ expression
	// lastSig is the most recently returned token's type, used only to
	// disambiguate a `/` as the start of a regex literal (an expression is
	// expected) vs. the division operator (a value just ended) — see
	// regexIllegalAfter and regexAllowed. Starts as SEMICOLON (a statement
	// boundary) since start-of-input is exactly a regex-legal position too,
	// the same as after any other statement boundary.
	lastSig TokenType
}

func New(src string) *Lexer {
	return &Lexer{src: []rune(src), pos: 0, line: 1, col: 1, lastSig: SEMICOLON}
}

func (l *Lexer) peek() rune {
	if l.pos >= len(l.src) {
		return 0
	}
	return l.src[l.pos]
}

func (l *Lexer) peekAt(n int) rune {
	i := l.pos + n
	if i >= len(l.src) {
		return 0
	}
	return l.src[i]
}

func (l *Lexer) advance() rune {
	if l.pos >= len(l.src) {
		return 0
	}
	ch := l.src[l.pos]
	l.pos++
	if ch == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return ch
}

func (l *Lexer) skipWhitespace() {
	// U+FEFF (BOM / ZWNBSP) is part of ECMAScript's WhiteSpace production but is
	// not covered by unicode.IsSpace, so a leading BOM (common in editor output)
	// or a stray one would otherwise be an "unexpected character" — treat it as
	// whitespace, matching real JS/TS.
	for l.pos < len(l.src) && (unicode.IsSpace(l.peek()) || l.peek() == '\uFEFF') {
		l.advance()
	}
}

func (l *Lexer) tok(typ TokenType, lit string, line, col int) Token {
	return Token{Type: typ, Literal: lit, Line: line, Col: col}
}

// NextToken returns the next token, tracking lastSig across calls (used to
// disambiguate a `/` as the start of a regex literal vs. the division
// operator — see regexIllegalAfter/regexAllowed). The real scanning logic
// lives in nextToken; this wrapper exists purely so every return path
// through it (including the comment-skipping recursive calls inside
// nextToken itself, which call nextToken directly rather than this
// wrapper) only updates lastSig once per real token actually handed back
// to the caller.
func (l *Lexer) NextToken() (Token, error) {
	tok, err := l.nextToken()
	if err != nil {
		return tok, err
	}
	l.lastSig = tok.Type
	return tok, nil
}

func (l *Lexer) nextToken() (Token, error) {
	l.skipWhitespace()

	if l.pos >= len(l.src) {
		return l.tok(EOF, "", l.line, l.col), nil
	}

	line, col := l.line, l.col
	ch := l.peek()

	// Comments
	if ch == '/' {
		switch l.peekAt(1) {
		case '/':
			for l.pos < len(l.src) && l.peek() != '\n' {
				l.advance()
			}
			return l.nextToken()
		case '*':
			l.advance() // /
			l.advance() // *
			isJSDoc := l.peek() == '*'
			var buf strings.Builder
			if isJSDoc {
				l.advance() // second *
			}
			for l.pos < len(l.src) {
				if l.peek() == '*' && l.peekAt(1) == '/' {
					l.advance()
					l.advance()
					break
				}
				buf.WriteRune(l.advance())
			}
			if isJSDoc {
				return l.tok(JSDOC, strings.TrimSpace(buf.String()), line, col), nil
			}
			return l.nextToken()
		}
	}

	if ch == '/' && l.regexAllowed() {
		return l.readRegex(line, col)
	}

	if unicode.IsDigit(ch) || (ch == '.' && unicode.IsDigit(l.peekAt(1))) {
		return l.readNumber(line, col)
	}
	if ch == '`' {
		return l.readTemplateHead(line, col)
	}
	if ch == '"' || ch == '\'' {
		return l.readString(line, col)
	}
	if unicode.IsLetter(ch) || ch == '_' || ch == '$' {
		return l.readIdent(line, col)
	}
	if ch == '#' && (unicode.IsLetter(l.peekAt(1)) || l.peekAt(1) == '_' || l.peekAt(1) == '$') {
		return l.readPrivateName(line, col)
	}

	l.advance()
	switch ch {
	case '+':
		if l.peek() == '+' {
			l.advance()
			return l.tok(INC, "++", line, col), nil
		}
		if l.peek() == '=' {
			l.advance()
			return l.tok(PLUS_ASSIGN, "+=", line, col), nil
		}
		return l.tok(PLUS, "+", line, col), nil
	case '-':
		if l.peek() == '-' {
			l.advance()
			return l.tok(DEC, "--", line, col), nil
		}
		if l.peek() == '=' {
			l.advance()
			return l.tok(MINUS_ASSIGN, "-=", line, col), nil
		}
		return l.tok(MINUS, "-", line, col), nil
	case '*':
		if l.peek() == '*' {
			l.advance()
			if l.peek() == '=' {
				l.advance()
				return l.tok(POW_ASSIGN, "**=", line, col), nil
			}
			return l.tok(POW, "**", line, col), nil
		}
		if l.peek() == '=' {
			l.advance()
			return l.tok(STAR_ASSIGN, "*=", line, col), nil
		}
		return l.tok(STAR, "*", line, col), nil
	case '/':
		if l.peek() == '=' {
			l.advance()
			return l.tok(SLASH_ASSIGN, "/=", line, col), nil
		}
		return l.tok(SLASH, "/", line, col), nil
	case '%':
		if l.peek() == '=' {
			l.advance()
			return l.tok(PERCENT_ASSIGN, "%=", line, col), nil
		}
		return l.tok(PERCENT, "%", line, col), nil
	case '=':
		if l.peek() == '=' {
			l.advance()
			if l.peek() == '=' {
				l.advance()
				return l.tok(STRICT_EQ, "===", line, col), nil
			}
			return l.tok(EQ, "==", line, col), nil
		}
		if l.peek() == '>' {
			l.advance()
			return l.tok(ARROW, "=>", line, col), nil
		}
		return l.tok(ASSIGN, "=", line, col), nil
	case '!':
		if l.peek() == '=' {
			l.advance()
			if l.peek() == '=' {
				l.advance()
				return l.tok(STRICT_NEQ, "!==", line, col), nil
			}
			return l.tok(NEQ, "!=", line, col), nil
		}
		return l.tok(NOT, "!", line, col), nil
	case '<':
		if l.peek() == '<' {
			l.advance()
			if l.peek() == '=' {
				l.advance()
				return l.tok(LSHIFT_ASSIGN, "<<=", line, col), nil
			}
			return l.tok(LSHIFT, "<<", line, col), nil
		}
		if l.peek() == '=' {
			l.advance()
			return l.tok(LTE, "<=", line, col), nil
		}
		return l.tok(LT, "<", line, col), nil
	case '>':
		if l.peek() == '>' {
			l.advance()
			if l.peek() == '>' {
				l.advance()
				if l.peek() == '=' {
					l.advance()
					return l.tok(URSHIFT_ASSIGN, ">>>=", line, col), nil
				}
				return l.tok(URSHIFT, ">>>", line, col), nil
			}
			if l.peek() == '=' {
				l.advance()
				return l.tok(RSHIFT_ASSIGN, ">>=", line, col), nil
			}
			return l.tok(RSHIFT, ">>", line, col), nil
		}
		if l.peek() == '=' {
			l.advance()
			return l.tok(GTE, ">=", line, col), nil
		}
		return l.tok(GT, ">", line, col), nil
	case '&':
		if l.peek() == '&' {
			l.advance()
			if l.peek() == '=' {
				l.advance()
				return l.tok(LOGICAL_AND_ASSIGN, "&&=", line, col), nil
			}
			return l.tok(AND, "&&", line, col), nil
		}
		if l.peek() == '=' {
			l.advance()
			return l.tok(AND_ASSIGN, "&=", line, col), nil
		}
		return l.tok(BITAND, "&", line, col), nil
	case '|':
		if l.peek() == '|' {
			l.advance()
			if l.peek() == '=' {
				l.advance()
				return l.tok(LOGICAL_OR_ASSIGN, "||=", line, col), nil
			}
			return l.tok(OR, "||", line, col), nil
		}
		if l.peek() == '=' {
			l.advance()
			return l.tok(OR_ASSIGN, "|=", line, col), nil
		}
		return l.tok(BITOR, "|", line, col), nil
	case '^':
		if l.peek() == '=' {
			l.advance()
			return l.tok(XOR_ASSIGN, "^=", line, col), nil
		}
		return l.tok(BITXOR, "^", line, col), nil
	case '~':
		return l.tok(BITNOT, "~", line, col), nil
	case '(':
		return l.tok(LPAREN, "(", line, col), nil
	case ')':
		return l.tok(RPAREN, ")", line, col), nil
	case '{':
		if len(l.templateStack) > 0 {
			l.templateStack[len(l.templateStack)-1]++
		}
		return l.tok(LBRACE, "{", line, col), nil
	case '}':
		if len(l.templateStack) > 0 {
			top := len(l.templateStack) - 1
			if l.templateStack[top] == 0 {
				l.templateStack = l.templateStack[:top]
				return l.readTemplatePart(line, col)
			}
			l.templateStack[top]--
		}
		return l.tok(RBRACE, "}", line, col), nil
	case '[':
		return l.tok(LBRACKET, "[", line, col), nil
	case ']':
		return l.tok(RBRACKET, "]", line, col), nil
	case ';':
		return l.tok(SEMICOLON, ";", line, col), nil
	case ':':
		return l.tok(COLON, ":", line, col), nil
	case ',':
		return l.tok(COMMA, ",", line, col), nil
	case '.':
		if l.peek() == '.' && l.peekAt(1) == '.' {
			l.advance()
			l.advance()
			return l.tok(ELLIPSIS, "...", line, col), nil
		}
		return l.tok(DOT, ".", line, col), nil
	case '?':
		if l.peek() == '?' {
			l.advance()
			if l.peek() == '=' {
				l.advance()
				return l.tok(NULLISH_ASSIGN, "??=", line, col), nil
			}
			return l.tok(NULLISH, "??", line, col), nil
		}
		if l.peek() == '.' {
			l.advance()
			return l.tok(OPTIONAL_DOT, "?.", line, col), nil
		}
		return l.tok(QUESTION, "?", line, col), nil
	}

	return Token{}, fmt.Errorf("%d:%d: unexpected character %q", line, col, ch)
}

// readDigitRun consumes a run of characters matching isDigit into buf,
// allowing a single '_' numeric separator between two digits (e.g.
// 1_000_000) — the separator itself is discarded, never written to buf, so
// downstream parsing/codegen never has to know it existed. A leading,
// trailing, or doubled separator is a lexer error.
func (l *Lexer) readDigitRun(buf *strings.Builder, isDigit func(rune) bool, line, col int) error {
	lastWasDigit := false
	for l.pos < len(l.src) {
		c := l.peek()
		if isDigit(c) {
			buf.WriteRune(l.advance())
			lastWasDigit = true
		} else if c == '_' {
			if !lastWasDigit || !isDigit(l.peekAt(1)) {
				return fmt.Errorf("%d:%d: numeric separator '_' must be between two digits", line, col)
			}
			l.advance()
			lastWasDigit = false
		} else {
			break
		}
	}
	return nil
}

func (l *Lexer) readNumber(line, col int) (Token, error) {
	var buf strings.Builder
	isHexDigit := func(c rune) bool {
		return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
	}
	isBinDigit := func(c rune) bool { return c == '0' || c == '1' }
	isOctDigit := func(c rune) bool { return c >= '0' && c <= '7' }
	// Hex / binary / octal prefixes
	if l.peek() == '0' {
		switch l.peekAt(1) {
		case 'x', 'X':
			buf.WriteRune(l.advance()) // '0'
			buf.WriteRune(l.advance()) // 'x'
			if err := l.readDigitRun(&buf, isHexDigit, line, col); err != nil {
				return Token{}, err
			}
			if l.peek() == 'n' {
				l.advance()
				return l.tok(BIGINT, buf.String(), line, col), nil
			}
			return l.tok(NUMBER, buf.String(), line, col), nil
		case 'b', 'B':
			buf.WriteRune(l.advance()) // '0'
			buf.WriteRune(l.advance()) // 'b'
			if err := l.readDigitRun(&buf, isBinDigit, line, col); err != nil {
				return Token{}, err
			}
			if l.peek() == 'n' {
				l.advance()
				return l.tok(BIGINT, buf.String(), line, col), nil
			}
			return l.tok(NUMBER, buf.String(), line, col), nil
		case 'o', 'O':
			buf.WriteRune(l.advance()) // '0'
			buf.WriteRune(l.advance()) // 'o'
			if err := l.readDigitRun(&buf, isOctDigit, line, col); err != nil {
				return Token{}, err
			}
			if l.peek() == 'n' {
				l.advance()
				return l.tok(BIGINT, buf.String(), line, col), nil
			}
			return l.tok(NUMBER, buf.String(), line, col), nil
		}
	}
	// Regular decimal number.
	//
	// A leading '0' directly followed by another decimal digit or a '_'
	// (e.g. 010, 08, 0_0, 08_0) is a LegacyOctalIntegerLiteral or
	// NonOctalDecimalIntegerLiteral — Annex B — which forbids numeric
	// separators entirely, even in non-strict mode (`0_0`, `08_0`, … are
	// SyntaxErrors). A lone `0`, or `0.`/`0e`, is an ordinary decimal and is
	// unaffected. (This compiler doesn't otherwise model legacy-octal
	// *value* semantics — `010` still reads as decimal 10 — but the
	// separator rule is an unconditional early error worth catching.)
	nextC := l.peekAt(1)
	legacyLeadingZero := l.peek() == '0' && ((nextC >= '0' && nextC <= '9') || nextC == '_')
	hasDot := false
	lastWasDigit := false
	for l.pos < len(l.src) {
		c := l.peek()
		if unicode.IsDigit(c) {
			buf.WriteRune(l.advance())
			lastWasDigit = true
		} else if c == '.' && !hasDot && unicode.IsDigit(l.peekAt(1)) {
			hasDot = true
			buf.WriteRune(l.advance())
			lastWasDigit = false
		} else if c == '_' {
			if legacyLeadingZero {
				return Token{}, fmt.Errorf("%d:%d: a numeric separator '_' is not allowed in a legacy octal or non-octal-decimal literal", line, col)
			}
			if !lastWasDigit || !unicode.IsDigit(l.peekAt(1)) {
				return Token{}, fmt.Errorf("%d:%d: numeric separator '_' must be between two digits", line, col)
			}
			l.advance()
			lastWasDigit = false
		} else {
			break
		}
	}
	// Optional exponent part (ES DecimalLiteral): `e`/`E`, an optional sign,
	// then one or more digits (numeric separators allowed between them) —
	// `1e3`, `1.5E-2`, `2e+10`. Only an `e`/`E` immediately followed by a digit
	// (optionally after a sign) starts an exponent; otherwise the `e` belongs to
	// a following identifier/token, so `x in y` and a member like `1 .toFixed`
	// are unaffected.
	hasExp := false
	if c := l.peek(); c == 'e' || c == 'E' {
		n1 := l.peekAt(1)
		if unicode.IsDigit(n1) || ((n1 == '+' || n1 == '-') && unicode.IsDigit(l.peekAt(2))) {
			hasExp = true
			buf.WriteRune(l.advance()) // 'e' / 'E'
			if l.peek() == '+' || l.peek() == '-' {
				buf.WriteRune(l.advance())
			}
			lastWasDigit = false
			for l.pos < len(l.src) {
				c := l.peek()
				if unicode.IsDigit(c) {
					buf.WriteRune(l.advance())
					lastWasDigit = true
				} else if c == '_' {
					if !lastWasDigit || !unicode.IsDigit(l.peekAt(1)) {
						return Token{}, fmt.Errorf("%d:%d: numeric separator '_' must be between two digits", line, col)
					}
					l.advance()
					lastWasDigit = false
				} else {
					break
				}
			}
		}
	}
	if l.peek() == 'n' {
		if hasDot {
			return Token{}, fmt.Errorf("%d:%d: a BigInt literal cannot contain a decimal point", line, col)
		}
		if hasExp {
			return Token{}, fmt.Errorf("%d:%d: a BigInt literal cannot contain an exponent", line, col)
		}
		if legacyLeadingZero {
			return Token{}, fmt.Errorf("%d:%d: a BigInt literal cannot use a legacy-octal / non-octal-decimal form", line, col)
		}
		l.advance() // consume the 'n' suffix
		return l.tok(BIGINT, buf.String(), line, col), nil
	}
	return l.tok(NUMBER, buf.String(), line, col), nil
}

func (l *Lexer) readString(line, col int) (Token, error) {
	quote := l.advance()
	var buf strings.Builder
	for l.pos < len(l.src) {
		c := l.peek()
		if c == quote {
			l.advance()
			break
		}
		if c == '\\' {
			l.advance()
			if err := l.scanStringEscape(&buf, line, col); err != nil {
				return Token{}, err
			}
			continue
		}
		if c == '\n' && quote != '`' {
			return Token{}, fmt.Errorf("%d:%d: unterminated string literal", line, col)
		}
		buf.WriteRune(l.advance())
	}
	return l.tok(STRING, buf.String(), line, col), nil
}

// scanStringEscape decodes one string-literal escape sequence (the leading
// backslash already consumed) and appends the result to buf. Implements the
// ES EscapeSequence grammar (`\n \t \r \b \f \v`, `\xHH`, `\uHHHH`,
// `\u{H…}`), the Annex B LegacyOctalEscapeSequence (`\0`–`\377`) and
// NonOctalDecimalEscapeSequence (`\8`/`\9` → the digit), LineContinuation
// (a backslash before any line terminator, including U+2028/U+2029, yields
// nothing), and the NonEscapeCharacter fallback (`\A` → `A`, dropping the
// backslash — the previous behavior of emitting `\A` verbatim was wrong).
// Strings are this compiler's UTF-8 bytes, so a decoded code point is
// written via WriteRune (its UTF-8 encoding), matching the UTF-8 string
// model (see docs/tdd/TDD-00034.md).
func (l *Lexer) scanStringEscape(buf *strings.Builder, line, col int) error {
	esc := l.advance()
	switch esc {
	case 'n':
		buf.WriteByte('\n')
	case 't':
		buf.WriteByte('\t')
	case 'r':
		buf.WriteByte('\r')
	case 'b':
		buf.WriteByte('\b')
	case 'f':
		buf.WriteByte('\f')
	case 'v':
		buf.WriteByte('\v')
	case '\\', '"', '\'', '`':
		buf.WriteRune(esc)
	case '0', '1', '2', '3', '4', '5', '6', '7':
		// LegacyOctalEscapeSequence (Annex B) — `\0` with no following octal
		// digit is the ES NUL escape. Up to three octal digits, capped at
		// \377 (255): a leading digit of 4–7 allows only one more.
		val := int(esc - '0')
		more := 2
		if esc > '3' {
			more = 1
		}
		for i := 0; i < more; i++ {
			n := l.peek()
			if n < '0' || n > '7' {
				break
			}
			val = val*8 + int(l.advance()-'0')
		}
		buf.WriteRune(rune(val))
	case 'x':
		v, ok := l.readHexEscape(2)
		if !ok {
			return fmt.Errorf("%d:%d: invalid hexadecimal escape sequence", line, col)
		}
		buf.WriteRune(rune(v))
	case 'u':
		if l.peek() == '{' {
			l.advance() // consume '{'
			val, n := 0, 0
			for l.peek() != '}' && l.pos < len(l.src) {
				d, ok := hexDigitVal(l.peek())
				if !ok {
					return fmt.Errorf("%d:%d: invalid Unicode escape sequence", line, col)
				}
				l.advance()
				val = val*16 + d
				n++
				if val > 0x10FFFF {
					return fmt.Errorf("%d:%d: Unicode code point out of range", line, col)
				}
			}
			if n == 0 || l.peek() != '}' {
				return fmt.Errorf("%d:%d: invalid Unicode escape sequence", line, col)
			}
			l.advance() // consume '}'
			buf.WriteRune(rune(val))
		} else {
			v, ok := l.readHexEscape(4)
			if !ok {
				return fmt.Errorf("%d:%d: invalid Unicode escape sequence", line, col)
			}
			buf.WriteRune(rune(v))
		}
	case '\n', '\u2028', '\u2029':
		// LineContinuation — the escaped line terminator yields nothing.
	case '\r':
		// LineContinuation; a CRLF pair counts as one terminator.
		if l.peek() == '\n' {
			l.advance()
		}
	case 0:
		return fmt.Errorf("%d:%d: unterminated string literal", line, col)
	default:
		// NonEscapeCharacter — the character itself, without the backslash.
		buf.WriteRune(esc)
	}
	return nil
}

// readHexEscape reads exactly n hex digits and returns their value. Used by
// the `\xHH` and `\uHHHH` escape forms.
func (l *Lexer) readHexEscape(n int) (int, bool) {
	val := 0
	for i := 0; i < n; i++ {
		d, ok := hexDigitVal(l.peek())
		if !ok {
			return 0, false
		}
		l.advance()
		val = val*16 + d
	}
	return val, true
}

// hexDigitVal returns the numeric value of a single hex digit.
func hexDigitVal(c rune) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

// regexIllegalAfter lists the token types after which a `/` cannot start a
// regex literal — i.e. positions where a value just ended, so `/` can only
// be the division operator. Every token type NOT listed here defaults to
// "regex-legal" (an expression is expected) — deliberately an inverted,
// smaller list to maintain (operators, keywords, and punctuation that
// precede an expression vastly outnumber the handful of "a value just
// ended" token types), matching how real JS engines actually implement
// this same heuristic. See docs/tdd/TDD-00035.md's Stage 0 design.
//
// One known, deliberately accepted gap: `in`/`of` are lexed as plain IDENT
// tokens with contextual parser-side checks (parser/parser_exprs.go), not
// their own TokenType, so `x in /foo/` mis-lexes the `/` as division. Rare
// enough to document rather than fix — fixing it would mean threading
// parser-level context back into the lexer, a much bigger change than this
// feature justifies.
var regexIllegalAfter = map[TokenType]bool{
	IDENT: true, NUMBER: true, BIGINT: true, STRING: true, PRIVATE_NAME: true,
	TEMPLATE_NO_SUB: true, TEMPLATE_TAIL: true,
	TRUE: true, FALSE: true, NULL: true, UNDEFINED: true,
	THIS: true, SUPER: true,
	RPAREN: true, RBRACKET: true, RBRACE: true,
	INC: true, DEC: true,
}

func (l *Lexer) regexAllowed() bool {
	return !regexIllegalAfter[l.lastSig]
}

// readRegex scans a /pattern/flags literal. Called with the current
// position at the opening '/' (not yet consumed) once regexAllowed() has
// already confirmed this position expects an expression, not a division
// operator — comment detection (`//`, `/*`) in nextToken already ran first
// and would have won, matching real JS's own grammar restriction that a
// regex's first character can never be `*` or `/` (so an empty regex
// literal is inexpressible as `//`; `new RegExp("")` is the only way to
// write one — not a bug to solve here).
//
// Backslash escapes are preserved verbatim (unlike readString, which
// translates \n/\t/etc. — PCRE2 needs to see the real \d, \/, etc., not a
// translated form), and an unescaped '/' inside a [...] character class
// does not terminate the pattern (matches the real RegularExpressionClass
// grammar production). Flag letters after the closing '/' are scanned with
// no validation at this layer — deferred to the parser/emitter, consistent
// with how this lexer never validates keyword-ness of identifiers either.
func (l *Lexer) readRegex(line, col int) (Token, error) {
	l.advance() // consume opening '/'
	var pattern strings.Builder
	inClass := false
	for {
		if l.pos >= len(l.src) || l.peek() == '\n' {
			return Token{}, fmt.Errorf("%d:%d: unterminated regular expression literal", line, col)
		}
		c := l.peek()
		if c == '\\' {
			pattern.WriteRune(l.advance())
			if l.pos >= len(l.src) || l.peek() == '\n' {
				return Token{}, fmt.Errorf("%d:%d: unterminated regular expression literal", line, col)
			}
			pattern.WriteRune(l.advance())
			continue
		}
		if c == '[' {
			inClass = true
			pattern.WriteRune(l.advance())
			continue
		}
		if c == ']' {
			inClass = false
			pattern.WriteRune(l.advance())
			continue
		}
		if c == '/' && !inClass {
			l.advance()
			break
		}
		pattern.WriteRune(l.advance())
	}
	var flags strings.Builder
	for l.pos < len(l.src) && unicode.IsLetter(l.peek()) {
		flags.WriteRune(l.advance())
	}
	return Token{Type: REGEX, Literal: pattern.String(), Flags: flags.String(), Line: line, Col: col}, nil
}

func (l *Lexer) readIdent(line, col int) (Token, error) {
	var buf strings.Builder
	for l.pos < len(l.src) {
		c := l.peek()
		if unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '$' {
			buf.WriteRune(l.advance())
		} else {
			break
		}
	}
	lit := buf.String()
	return l.tok(LookupIdent(lit), lit, line, col), nil
}

// readPrivateName reads a class private name `#foo` — real JS's own
// PrivateIdentifier token kind, syntactically distinct from a plain
// identifier and never usable as one (TDD-00021). The literal keeps the
// leading `#` so `#foo` and `foo` never collide as field names.
func (l *Lexer) readPrivateName(line, col int) (Token, error) {
	var buf strings.Builder
	buf.WriteRune(l.advance()) // '#'
	for l.pos < len(l.src) {
		c := l.peek()
		if unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '$' {
			buf.WriteRune(l.advance())
		} else {
			break
		}
	}
	return l.tok(PRIVATE_NAME, buf.String(), line, col), nil
}

// readTemplateSegment reads template content from the current position until
// a closing backtick (atEnd=true) or an opening ${ (atEnd=false).
func (l *Lexer) readTemplateSegment() (string, bool, error) {
	var buf strings.Builder
	for l.pos < len(l.src) {
		c := l.peek()
		if c == '`' {
			l.advance()
			return buf.String(), true, nil
		}
		if c == '$' && l.peekAt(1) == '{' {
			l.advance() // $
			l.advance() // {
			return buf.String(), false, nil
		}
		if c == '\\' {
			l.advance()
			// Same escape grammar as a plain string literal — `\xHH`,
			// `\uHHHH`/`\u{…}`, `\0`/octal, line continuations, and the
			// NonEscapeCharacter fallback (which correctly turns `\$` into
			// `$` and `` \` `` into a backtick without the leading
			// backslash). See scanStringEscape.
			if err := l.scanStringEscape(&buf, l.line, l.col); err != nil {
				return "", false, err
			}
			continue
		}
		buf.WriteRune(l.advance())
	}
	return "", false, fmt.Errorf("unterminated template literal")
}

// readTemplateHead is called when a backtick is seen (not yet consumed).
func (l *Lexer) readTemplateHead(line, col int) (Token, error) {
	l.advance() // consume `
	seg, atEnd, err := l.readTemplateSegment()
	if err != nil {
		return Token{}, err
	}
	if atEnd {
		return l.tok(TEMPLATE_NO_SUB, seg, line, col), nil
	}
	l.templateStack = append(l.templateStack, 0)
	return l.tok(TEMPLATE_HEAD, seg, line, col), nil
}

// readTemplatePart is called after the } that closes a ${ expression.
func (l *Lexer) readTemplatePart(line, col int) (Token, error) {
	seg, atEnd, err := l.readTemplateSegment()
	if err != nil {
		return Token{}, err
	}
	if atEnd {
		return l.tok(TEMPLATE_TAIL, seg, line, col), nil
	}
	l.templateStack = append(l.templateStack, 0)
	return l.tok(TEMPLATE_MIDDLE, seg, line, col), nil
}

func Tokenize(src string) ([]Token, error) {
	l := New(src)
	var tokens []Token
	for {
		tok, err := l.NextToken()
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, tok)
		if tok.Type == EOF {
			break
		}
	}
	return tokens, nil
}
