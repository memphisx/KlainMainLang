package lexer

import "fmt"

type TokenType int

const (
	NUMBER TokenType = iota
	BIGINT // integer literal with a trailing `n` suffix (123n) — TDD-00074
	STRING
	IDENT
	PRIVATE_NAME // #foo — a class private field/method name (TDD-00021)

	// Keywords
	LET
	CONST
	VAR
	FUNCTION
	RETURN
	FOR
	WHILE
	IF
	ELSE
	TRUE
	FALSE
	NULL
	UNDEFINED
	NEW
	TYPEOF
	VOID
	SWITCH
	CASE
	DEFAULT
	BREAK
	CONTINUE
	THROW
	TRY
	CATCH
	FINALLY
	DO
	ASYNC
	AWAIT
	YIELD
	IMPORT
	EXPORT
	ELLIPSIS
	CLASS
	THIS
	INSTANCEOF
	EXTENDS
	SUPER
	STATIC
	PRIVATE
	PROTECTED
	PUBLIC
	ABSTRACT
	IMPLEMENTS

	// Operators
	PLUS           // +
	MINUS          // -
	STAR           // *
	POW            // **
	SLASH          // /
	PERCENT        // %
	ASSIGN         // =
	EQ             // ==
	NEQ            // !=
	STRICT_EQ      // ===
	STRICT_NEQ     // !==
	LT             // <
	GT             // >
	LTE            // <=
	GTE            // >=
	AND            // &&
	OR             // ||
	NOT            // !
	INC            // ++
	DEC            // --
	PLUS_ASSIGN    // +=
	MINUS_ASSIGN   // -=
	STAR_ASSIGN    // *=
	POW_ASSIGN     // **=
	SLASH_ASSIGN   // /=
	PERCENT_ASSIGN // %=

	// Bitwise operators
	BITAND  // &
	BITOR   // |
	BITXOR  // ^
	BITNOT  // ~
	LSHIFT  // <<
	RSHIFT  // >>
	URSHIFT // >>>

	// Bitwise compound assignment
	AND_ASSIGN     // &=
	OR_ASSIGN      // |=
	XOR_ASSIGN     // ^=
	LSHIFT_ASSIGN  // <<=
	RSHIFT_ASSIGN  // >>=
	URSHIFT_ASSIGN // >>>=

	// Logical compound assignment
	LOGICAL_AND_ASSIGN // &&=
	LOGICAL_OR_ASSIGN  // ||=
	NULLISH_ASSIGN     // ??=

	// Punctuation
	LPAREN       // (
	RPAREN       // )
	LBRACE       // {
	RBRACE       // }
	LBRACKET     // [
	RBRACKET     // ]
	SEMICOLON    // ;
	COLON        // :
	COMMA        // ,
	DOT          // .
	QUESTION     // ?
	NULLISH      // ??
	OPTIONAL_DOT // ?.
	ARROW        // =>
	AT           // @ — decorator prefix (TDD-00161)

	// Template literal tokens
	TEMPLATE_NO_SUB // `plain text` (no substitutions)
	TEMPLATE_HEAD   // `text ${
	TEMPLATE_MIDDLE // } text ${
	TEMPLATE_TAIL   // } text`

	REGEX // /pattern/flags

	JSDOC // /** ... */
	EOF
)

var tokenNames = map[TokenType]string{
	NUMBER: "NUMBER", BIGINT: "BIGINT", STRING: "STRING", IDENT: "IDENT", PRIVATE_NAME: "PRIVATE_NAME",
	LET: "let", CONST: "const", VAR: "var", FUNCTION: "function",
	RETURN: "return", FOR: "for", WHILE: "while", IF: "if", ELSE: "else",
	TRUE: "true", FALSE: "false", NULL: "null", UNDEFINED: "undefined",
	NEW: "new", TYPEOF: "typeof", VOID: "void",
	SWITCH: "switch", CASE: "case", DEFAULT: "default", BREAK: "break", CONTINUE: "continue",
	THROW: "throw", TRY: "try", CATCH: "catch", FINALLY: "finally", DO: "do",
	ASYNC: "async", AWAIT: "await", YIELD: "yield",
	IMPORT: "import", EXPORT: "export",
	ELLIPSIS: "...",
	CLASS:    "class", THIS: "this", INSTANCEOF: "instanceof",
	EXTENDS: "extends", SUPER: "super",
	STATIC: "static", PRIVATE: "private", PROTECTED: "protected", PUBLIC: "public",
	ABSTRACT: "abstract", IMPLEMENTS: "implements",
	PLUS: "+", MINUS: "-", STAR: "*", POW: "**", SLASH: "/", PERCENT: "%",
	ASSIGN: "=", EQ: "==", NEQ: "!=", STRICT_EQ: "===", STRICT_NEQ: "!==",
	LT: "<", GT: ">", LTE: "<=", GTE: ">=",
	AND: "&&", OR: "||", NOT: "!",
	INC: "++", DEC: "--", PLUS_ASSIGN: "+=", MINUS_ASSIGN: "-=",
	STAR_ASSIGN: "*=", POW_ASSIGN: "**=", SLASH_ASSIGN: "/=", PERCENT_ASSIGN: "%=",
	BITAND: "&", BITOR: "|", BITXOR: "^", BITNOT: "~",
	LSHIFT: "<<", RSHIFT: ">>", URSHIFT: ">>>",
	AND_ASSIGN: "&=", OR_ASSIGN: "|=", XOR_ASSIGN: "^=",
	LSHIFT_ASSIGN: "<<=", RSHIFT_ASSIGN: ">>=", URSHIFT_ASSIGN: ">>>=",
	LOGICAL_AND_ASSIGN: "&&=", LOGICAL_OR_ASSIGN: "||=", NULLISH_ASSIGN: "??=",
	LPAREN: "(", RPAREN: ")", LBRACE: "{", RBRACE: "}",
	LBRACKET: "[", RBRACKET: "]",
	SEMICOLON: ";", COLON: ":", COMMA: ",", DOT: ".", QUESTION: "?", NULLISH: "??", OPTIONAL_DOT: "?.", ARROW: "=>", AT: "@",
	TEMPLATE_NO_SUB: "TEMPLATE_NO_SUB", TEMPLATE_HEAD: "TEMPLATE_HEAD",
	TEMPLATE_MIDDLE: "TEMPLATE_MIDDLE", TEMPLATE_TAIL: "TEMPLATE_TAIL",
	REGEX: "REGEX",
	JSDOC: "JSDOC", EOF: "EOF",
}

func (t TokenType) String() string {
	if name, ok := tokenNames[t]; ok {
		return name
	}
	return fmt.Sprintf("TOKEN(%d)", int(t))
}

var keywords = map[string]TokenType{
	"let": LET, "const": CONST, "var": VAR, "function": FUNCTION,
	"return": RETURN, "for": FOR, "while": WHILE, "if": IF, "else": ELSE,
	"true": TRUE, "false": FALSE, "null": NULL, "undefined": UNDEFINED,
	"new": NEW, "typeof": TYPEOF, "void": VOID,
	"switch": SWITCH, "case": CASE, "default": DEFAULT, "break": BREAK, "continue": CONTINUE,
	"throw": THROW, "try": TRY, "catch": CATCH, "finally": FINALLY, "do": DO,
	"async": ASYNC, "await": AWAIT, "yield": YIELD,
	"import": IMPORT, "export": EXPORT,
	"class": CLASS, "this": THIS, "instanceof": INSTANCEOF,
	"extends": EXTENDS, "super": SUPER,
	"static": STATIC, "private": PRIVATE, "protected": PROTECTED, "public": PUBLIC,
	"abstract": ABSTRACT, "implements": IMPLEMENTS,
}

func LookupIdent(s string) TokenType {
	if t, ok := keywords[s]; ok {
		return t
	}
	return IDENT
}

var keywordTypes = func() map[TokenType]bool {
	m := make(map[TokenType]bool, len(keywords))
	for _, t := range keywords {
		m[t] = true
	}
	return m
}()

// IsKeyword reports whether t is a reserved-word token type. Reserved words are
// valid property names after `.` (e.g. `promise.catch`, `promise.finally`), so
// the parser accepts them in that position — a keyword token carries the word
// itself as its Literal (see readIdent).
func IsKeyword(t TokenType) bool { return keywordTypes[t] }

type Token struct {
	Type    TokenType
	Literal string
	Flags   string // regex literal flags only (Type == REGEX); empty for every other token
	Raw     string // undecoded source text of a template segment (template tokens only); backs String.raw
	Line    int
	Col     int
}

func (t Token) String() string {
	return fmt.Sprintf("Token{%s %q %d:%d}", t.Type, t.Literal, t.Line, t.Col)
}
