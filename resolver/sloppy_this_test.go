package resolver

import (
	"testing"

	"KlainMainLang/parser"
)

// A sloppy script reads the global object as `this` only from a function
// that is sloppy code: the word in a comment or a string, a class body, an
// arrow at the top level and a "use strict" function are no such read.
func TestSloppyThisUse(t *testing.T) {
	for src, want := range map[string]bool{
		"// so this runs as JavaScript\nconsole.log(1);\n": false,
		"const s = 'this';\nconsole.log(s);\n":             false,
		"class C { m() { return this; } }\n":               false,
		"const f = () => this;\n":                          false,
		"function g() { 'use strict'; return this; }\n":    false,
		"function F(x) { this.x = x; }\n":                  true,
		"function h() { return () => this; }\n":            true,
		"const o = { m: function () { return this; } };\n": true,
	} {
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if got := sloppyThisUse("main.js", []byte(src), prog); got != want {
			t.Errorf("%q: got %v, want %v", src, got, want)
		}
	}
}
