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
}

func New(src string) *Lexer {
	return &Lexer{src: []rune(src), pos: 0, line: 1, col: 1}
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
	for l.pos < len(l.src) && unicode.IsSpace(l.peek()) {
		l.advance()
	}
}

func (l *Lexer) tok(typ TokenType, lit string, line, col int) Token {
	return Token{Type: typ, Literal: lit, Line: line, Col: col}
}

func (l *Lexer) NextToken() (Token, error) {
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
			return l.NextToken()
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
			return l.NextToken()
		}
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

func (l *Lexer) readNumber(line, col int) (Token, error) {
	var buf strings.Builder
	// Hex / binary / octal prefixes
	if l.peek() == '0' {
		switch l.peekAt(1) {
		case 'x', 'X':
			buf.WriteRune(l.advance()) // '0'
			buf.WriteRune(l.advance()) // 'x'
			for l.pos < len(l.src) {
				c := l.peek()
				if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
					buf.WriteRune(l.advance())
				} else {
					break
				}
			}
			return l.tok(NUMBER, buf.String(), line, col), nil
		case 'b', 'B':
			buf.WriteRune(l.advance()) // '0'
			buf.WriteRune(l.advance()) // 'b'
			for l.pos < len(l.src) && (l.peek() == '0' || l.peek() == '1') {
				buf.WriteRune(l.advance())
			}
			return l.tok(NUMBER, buf.String(), line, col), nil
		case 'o', 'O':
			buf.WriteRune(l.advance()) // '0'
			buf.WriteRune(l.advance()) // 'o'
			for l.pos < len(l.src) && l.peek() >= '0' && l.peek() <= '7' {
				buf.WriteRune(l.advance())
			}
			return l.tok(NUMBER, buf.String(), line, col), nil
		}
	}
	// Regular decimal number
	hasDot := false
	for l.pos < len(l.src) {
		c := l.peek()
		if unicode.IsDigit(c) {
			buf.WriteRune(l.advance())
		} else if c == '.' && !hasDot && unicode.IsDigit(l.peekAt(1)) {
			hasDot = true
			buf.WriteRune(l.advance())
		} else {
			break
		}
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
			esc := l.advance()
			switch esc {
			case 'n':
				buf.WriteByte('\n')
			case 't':
				buf.WriteByte('\t')
			case 'r':
				buf.WriteByte('\r')
			case '\\':
				buf.WriteByte('\\')
			case '"':
				buf.WriteByte('"')
			case '\'':
				buf.WriteByte('\'')
			default:
				buf.WriteByte('\\')
				buf.WriteRune(esc)
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
			esc := l.advance()
			switch esc {
			case 'n':
				buf.WriteByte('\n')
			case 't':
				buf.WriteByte('\t')
			case 'r':
				buf.WriteByte('\r')
			case '\\':
				buf.WriteByte('\\')
			case '`':
				buf.WriteByte('`')
			case '$':
				buf.WriteByte('$')
			default:
				buf.WriteByte('\\')
				buf.WriteRune(esc)
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
