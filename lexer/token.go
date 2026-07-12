package lexer

import "fmt"

type TokenType int

const (
	NUMBER TokenType = iota
	STRING
	IDENT

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
	IMPORT
	EXPORT
	ELLIPSIS

	// Operators
	PLUS         // +
	MINUS        // -
	STAR         // *
	SLASH        // /
	PERCENT      // %
	ASSIGN       // =
	EQ           // ==
	NEQ          // !=
	STRICT_EQ    // ===
	STRICT_NEQ   // !==
	LT           // <
	GT           // >
	LTE          // <=
	GTE          // >=
	AND          // &&
	OR           // ||
	NOT          // !
	INC          // ++
	DEC          // --
	PLUS_ASSIGN  // +=
	MINUS_ASSIGN // -=
	STAR_ASSIGN  // *=
	SLASH_ASSIGN // /=

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

	// Punctuation
	LPAREN    // (
	RPAREN    // )
	LBRACE    // {
	RBRACE    // }
	LBRACKET  // [
	RBRACKET  // ]
	SEMICOLON    // ;
	COLON        // :
	COMMA        // ,
	DOT          // .
	QUESTION     // ?
	NULLISH      // ??
	OPTIONAL_DOT // ?.
	ARROW        // =>

	// Template literal tokens
	TEMPLATE_NO_SUB // `plain text` (no substitutions)
	TEMPLATE_HEAD   // `text ${
	TEMPLATE_MIDDLE // } text ${
	TEMPLATE_TAIL   // } text`

	JSDOC // /** ... */
	EOF
)

var tokenNames = map[TokenType]string{
	NUMBER: "NUMBER", STRING: "STRING", IDENT: "IDENT",
	LET: "let", CONST: "const", VAR: "var", FUNCTION: "function",
	RETURN: "return", FOR: "for", WHILE: "while", IF: "if", ELSE: "else",
	TRUE: "true", FALSE: "false", NULL: "null", UNDEFINED: "undefined",
	NEW: "new", TYPEOF: "typeof", VOID: "void",
	SWITCH: "switch", CASE: "case", DEFAULT: "default", BREAK: "break", CONTINUE: "continue",
	THROW: "throw", TRY: "try", CATCH: "catch", FINALLY: "finally", DO: "do",
	ASYNC: "async", AWAIT: "await",
	IMPORT: "import", EXPORT: "export",
	ELLIPSIS: "...",
	PLUS: "+", MINUS: "-", STAR: "*", SLASH: "/", PERCENT: "%",
	ASSIGN: "=", EQ: "==", NEQ: "!=", STRICT_EQ: "===", STRICT_NEQ: "!==",
	LT: "<", GT: ">", LTE: "<=", GTE: ">=",
	AND: "&&", OR: "||", NOT: "!",
	INC: "++", DEC: "--", PLUS_ASSIGN: "+=", MINUS_ASSIGN: "-=",
	STAR_ASSIGN: "*=", SLASH_ASSIGN: "/=",
	BITAND: "&", BITOR: "|", BITXOR: "^", BITNOT: "~",
	LSHIFT: "<<", RSHIFT: ">>", URSHIFT: ">>>",
	AND_ASSIGN: "&=", OR_ASSIGN: "|=", XOR_ASSIGN: "^=",
	LSHIFT_ASSIGN: "<<=", RSHIFT_ASSIGN: ">>=", URSHIFT_ASSIGN: ">>>=",
	LPAREN: "(", RPAREN: ")", LBRACE: "{", RBRACE: "}",
	LBRACKET: "[", RBRACKET: "]",
	SEMICOLON: ";", COLON: ":", COMMA: ",", DOT: ".", QUESTION: "?", NULLISH: "??", OPTIONAL_DOT: "?.", ARROW: "=>",
	TEMPLATE_NO_SUB: "TEMPLATE_NO_SUB", TEMPLATE_HEAD: "TEMPLATE_HEAD",
	TEMPLATE_MIDDLE: "TEMPLATE_MIDDLE", TEMPLATE_TAIL: "TEMPLATE_TAIL",
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
	"async": ASYNC, "await": AWAIT,
	"import": IMPORT, "export": EXPORT,
}

func LookupIdent(s string) TokenType {
	if t, ok := keywords[s]; ok {
		return t
	}
	return IDENT
}

type Token struct {
	Type    TokenType
	Literal string
	Line    int
	Col     int
}

func (t Token) String() string {
	return fmt.Sprintf("Token{%s %q %d:%d}", t.Type, t.Literal, t.Line, t.Col)
}
