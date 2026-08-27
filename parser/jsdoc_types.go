package parser

import (
	"regexp"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/lexer"
)

// jsdocTypeAnnotation parses a JSDoc type-expression string into a full
// TypeAnnotation (TDD-00125 Stage 4). Closure-only syntax is normalized into
// the equivalent TypeScript type syntax, then the string is lexed and run
// through the real TS type parser — so union / `T[]` / object shapes / function
// types / `Array<T>` / `Record<K,V>` all resolve exactly as an inline `: T`
// would, instead of being stuffed verbatim into a name. If parsing fails the
// result falls back to the lenient `{Name: raw}` form (never worse than the
// pre-Stage-4 behavior).
func jsdocTypeAnnotation(raw string) *ast.TypeAnnotation {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if ta := parseJSDocTypeString(raw); ta != nil {
		return ta
	}
	return &ast.TypeAnnotation{Name: raw, Source: "jsdoc"}
}

func parseJSDocTypeString(raw string) *ast.TypeAnnotation {
	norm := normalizeClosureType(raw)
	if norm == "" {
		return nil
	}
	toks, err := lexer.Tokenize(norm)
	if err != nil {
		return nil
	}
	sub := New(toks)
	ta, err := sub.parseTypeAnnotation("jsdoc")
	if err != nil || ta == nil {
		return nil
	}
	// Reject a partial parse (trailing tokens the type grammar didn't consume),
	// so a malformed expression falls back to the lenient name form rather than
	// silently dropping part of the type.
	if !sub.check(lexer.EOF) {
		return nil
	}
	return ta
}

var (
	closureDotGeneric = regexp.MustCompile(`([A-Za-z_$][\w$]*)\.<`)
	legacySynonyms    = map[string]string{
		"String": "string", "Number": "number", "Boolean": "boolean",
		"Void": "void", "Undefined": "undefined", "Null": "null",
	}
	legacyWord = regexp.MustCompile(`\b(String|Number|Boolean|Void|Undefined|Null)\b`)
	// `import("./mod").Name` (a type imported from another module) → `Name`.
	// Under whole-program compilation a type exported by a module in the build
	// is registered by its bare name, so the qualifier can be dropped; the name
	// resolves when that module is part of the program (typically because the
	// file also imports from it normally). A `typeof import(...)` value query is
	// left untouched (out of scope).
	importType = regexp.MustCompile(`import\s*\(\s*["'][^"']*["']\s*\)\s*\.\s*([A-Za-z_$][\w$]*)`)
)

// normalizeClosureType rewrites Google-Closure JSDoc type syntax into the
// equivalent TypeScript type syntax the parser understands (TDD-00125 Stage 4).
func normalizeClosureType(s string) string {
	s = strings.TrimSpace(s)
	// Standalone Closure any/unknown.
	if s == "*" || s == "?" {
		return "any"
	}
	// `import("./mod").Name` type reference → the bare `Name` (whole-program
	// compilation registers it by name; the `typeof import(...)` value form is
	// left as-is — only a `.Name` type qualifier is rewritten).
	if !strings.Contains(s, "typeof") {
		s = importType.ReplaceAllString(s, "$1")
	}
	// `function(A, B): C` / `function(): C` → `(arg0: A, arg1: B) => C`.
	s = rewriteClosureFunctionType(s)
	// `Array.<T>` / `Foo.<T>` → `Array<T>` (drop Closure's dot before `<`).
	// `Object.<K, V>` becomes `Record<K, V>` (the TS index-map equivalent).
	s = strings.ReplaceAll(s, "Object.<", "Record<")
	s = closureDotGeneric.ReplaceAllString(s, "$1<")
	s = strings.ReplaceAll(s, "Object<", "Record<")
	// Non-null `!T` → `T`; nullable `?T` → `T | null` (only the leading marker,
	// the common form — a `?` buried mid-expression is left for the parser).
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "!") {
		s = strings.TrimSpace(s[1:])
	} else if strings.HasPrefix(s, "?") {
		s = strings.TrimSpace(s[1:]) + " | null"
	}
	// Legacy capitalized primitive synonyms (String → string, …).
	s = legacyWord.ReplaceAllStringFunc(s, func(w string) string { return legacySynonyms[w] })
	return strings.TrimSpace(s)
}

var closureFuncType = regexp.MustCompile(`^function\s*\(([^)]*)\)\s*(?::\s*(.+))?$`)

// rewriteClosureFunctionType turns `function(A, B): C` into the arrow form
// `(arg0: A, arg1: B) => C` (return `void` when omitted). A non-matching input
// is returned unchanged. Nested function types are not handled (rare).
func rewriteClosureFunctionType(s string) string {
	m := closureFuncType.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return s
	}
	ret := strings.TrimSpace(m[2])
	if ret == "" {
		ret = "void"
	}
	var params []string
	for i, p := range strings.Split(m[1], ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		params = append(params, "arg"+itoa(i)+": "+p)
	}
	return "(" + strings.Join(params, ", ") + ") => " + ret
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
