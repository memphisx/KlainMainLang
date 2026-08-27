package jsdoc

import (
	"regexp"
	"strings"
)

type Annotation struct {
	Tag      string
	Value    string // the `{...}` brace body (a type) when present, else the trailing word (lenient @type/@erased)
	Text     string // the trailing free text after the brace (e.g. a @param name + description)
	HasBrace bool   // whether a `{...}` type body was actually present
}

type Comment struct {
	Annotations []Annotation
}

var tagNameRe = regexp.MustCompile(`@(\w+)`)

func Parse(raw string) *Comment {
	c := &Comment{}
	for _, loc := range tagNameRe.FindAllStringSubmatchIndex(raw, -1) {
		tag := raw[loc[2]:loc[3]]
		rest := raw[loc[1]:] // everything after the tag name
		// A `{...}` type body may itself contain nested braces (an inline
		// object type `{{x: number}}`), so it is extracted by balanced-brace
		// scanning rather than a regex — the old `\{[^}]+\}` truncated at the
		// first `}`. The type body ends at its matching close brace; the
		// trailing free text runs to the end of the line (or the next `@`).
		braceTy, after, hasBrace := scanBraceType(rest)
		trailing := strings.TrimSpace(lineHead(after))
		val := strings.TrimSpace(braceTy)
		if val == "" {
			val = trailing
		}
		c.Annotations = append(c.Annotations, Annotation{Tag: tag, Value: val, Text: trailing, HasBrace: hasBrace})
	}
	return c
}

// scanBraceType consumes leading whitespace then, if the next character is `{`,
// returns the balanced-brace body (without the outer braces), the remainder
// after the closing brace, and true. Otherwise returns "", the input after
// leading whitespace, and false.
func scanBraceType(s string) (body, after string, has bool) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i >= len(s) || s[i] != '{' {
		return "", s[i:], false
	}
	depth := 0
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[i+1 : j], s[j+1:], true
			}
		case '\n':
			// An unterminated brace body doesn't span lines — bail out.
			return "", s[i:], false
		}
	}
	return "", s[i:], false
}

// lineHead returns s up to the first newline or `@` (the next tag), so a tag's
// trailing text never bleeds into the following line or annotation.
func lineHead(s string) string {
	end := len(s)
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' || s[i] == '@' {
			end = i
			break
		}
	}
	return s[:end]
}

func (c *Comment) GetType() string {
	for _, a := range c.Annotations {
		if a.Tag == "type" {
			return a.Value
		}
	}
	return ""
}

// ParamType returns the declared type of the `@param {T} name` (or its
// `@arg`/`@argument` aliases) whose parameter name matches, with JSDoc-only
// decorations stripped, or "" if there is no such tag. The parameter name in a
// `@param` may itself carry JSDoc's optional-bracket forms (`[name]`,
// `[name=default]`); those are unwrapped before matching (TDD-00125).
func (c *Comment) ParamType(name string) string {
	for _, a := range c.Annotations {
		if a.Tag != "param" && a.Tag != "arg" && a.Tag != "argument" {
			continue
		}
		if a.Value != "" && paramName(a.Text) == name {
			return stripJSDocDecorations(a.Value)
		}
	}
	return ""
}

// ReturnType returns the declared type of `@returns`/`@return`, with
// JSDoc-only decorations stripped, or "" (TDD-00125).
func (c *Comment) ReturnType() string {
	for _, a := range c.Annotations {
		if a.Tag == "returns" || a.Tag == "return" {
			return stripJSDocDecorations(a.Value)
		}
	}
	return ""
}

// TemplateParam is one type parameter declared by `@template` (TDD-00125
// Stage 3). Constraint is the `@template {Base} T` bound, or "".
type TemplateParam struct {
	Name       string
	Constraint string
}

// Templates returns the type parameters a function declares via `@template`
// tags — the JSDoc form of a `<T>` list. A single tag may list several
// comma-separated names (`@template T, U`); per TypeScript, a `{Base}`
// constraint on such a tag applies only to the first name.
func (c *Comment) Templates() []TemplateParam {
	var out []TemplateParam
	for _, a := range c.Annotations {
		if a.Tag != "template" {
			continue
		}
		constraint := ""
		if a.HasBrace {
			constraint = stripJSDocDecorations(a.Value)
		}
		for i, n := range strings.Split(a.Text, ",") {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			tp := TemplateParam{Name: n}
			if i == 0 {
				tp.Constraint = constraint
			}
			out = append(out, tp)
		}
	}
	return out
}

// TypedefField is one member of a synthesized JSDoc type: an object `@property`
// or a `@callback` `@param`. Rest marks a `{...T}` varargs entry.
type TypedefField struct {
	Name string
	Type string
	Rest bool
}

// TypedefDecl is a named type declared by `@typedef` or `@callback` (TDD-00125
// Stage 2). Kind is "alias" (a direct `@typedef {T} Name`), "object" (a
// `@typedef {Object} Name` fleshed out by `@property` tags), or "callback".
type TypedefDecl struct {
	Name   string
	Kind   string
	Base   string         // Kind=="alias": the aliased type string
	Fields []TypedefField // Kind=="object": properties; Kind=="callback": params
	Return string         // Kind=="callback": the @returns type ("" → void)
}

// Typedefs extracts every `@typedef`/`@callback` in the comment, each with the
// `@property`/`@param`/`@returns` tags that follow it (association is
// positional, the JSDoc convention). Returns nil when there are none.
func (c *Comment) Typedefs() []TypedefDecl {
	var out []TypedefDecl
	cur := -1 // index into out of the typedef/callback currently accreting members
	for _, a := range c.Annotations {
		switch a.Tag {
		case "typedef":
			name := paramName(a.Text)
			base := ""
			kind := "object"
			if a.HasBrace {
				b := stripJSDocDecorations(a.Value)
				if lb := strings.ToLower(b); lb != "object" && b != "" {
					kind, base = "alias", b
				}
			}
			out = append(out, TypedefDecl{Name: name, Kind: kind, Base: base})
			cur = len(out) - 1
		case "callback":
			out = append(out, TypedefDecl{Name: paramName(a.Text), Kind: "callback"})
			cur = len(out) - 1
		case "property", "prop":
			if cur >= 0 && out[cur].Kind == "object" && a.Value != "" {
				out[cur].Fields = append(out[cur].Fields, TypedefField{
					Name: paramName(a.Text), Type: stripJSDocDecorations(a.Value),
					Rest: strings.HasPrefix(strings.TrimSpace(a.Value), "..."),
				})
			}
		case "param", "arg", "argument":
			if cur >= 0 && out[cur].Kind == "callback" && a.Value != "" {
				out[cur].Fields = append(out[cur].Fields, TypedefField{
					Name: paramName(a.Text), Type: stripJSDocDecorations(a.Value),
					Rest: strings.HasPrefix(strings.TrimSpace(a.Value), "..."),
				})
			}
		case "returns", "return":
			if cur >= 0 && out[cur].Kind == "callback" {
				out[cur].Return = stripJSDocDecorations(a.Value)
			}
		}
	}
	return out
}

// paramName extracts the bare parameter name from a `@param` tag's post-type
// text: the first whitespace-delimited token, with JSDoc's optional-bracket
// wrapper removed (`[opts]` → `opts`, `[opts=5]` → `opts`).
func paramName(rest string) string {
	first := rest
	if i := strings.IndexAny(first, " \t"); i >= 0 {
		first = first[:i]
	}
	first = strings.TrimSpace(first)
	if strings.HasPrefix(first, "[") && strings.HasSuffix(first, "]") {
		first = first[1 : len(first)-1]
		if i := strings.IndexByte(first, '='); i >= 0 {
			first = first[:i]
		}
	}
	return strings.TrimSpace(first)
}

// stripJSDocDecorations removes the JSDoc-only markers a type body can carry so
// what remains is the plain type string the type resolver already understands:
// a leading `...` (varargs/rest, `@param {...number}`) and a trailing `=`
// (optional, `@param {number=}`). TDD-00125.
func stripJSDocDecorations(ty string) string {
	ty = strings.TrimSpace(ty)
	ty = strings.TrimPrefix(ty, "...")
	ty = strings.TrimSuffix(ty, "=")
	return strings.TrimSpace(ty)
}

// HasTag reports whether c carries a bare annotation tag (e.g. "erased" for
// `@erased`), regardless of any accompanying value.
func (c *Comment) HasTag(tag string) bool {
	for _, a := range c.Annotations {
		if a.Tag == tag {
			return true
		}
	}
	return false
}
