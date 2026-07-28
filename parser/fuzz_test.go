package parser_test

import (
	"os"
	"path/filepath"
	"testing"

	"KlainMainLang/parser"
)

// FuzzParse checks that Parse never panics or hangs on arbitrary input.
// A non-nil error is an acceptable outcome for malformed source; a panic
// (e.g. an index-out-of-bounds from an unbounded token-stream lookahead) is not.
func FuzzParse(f *testing.F) {
	seeds := []string{
		"",
		"const x = 42;",
		"let s: string = \"hi\";",
		"1 + 2 * 3;",
		"a | b & c;",
		"function f(a: number, b: number): number { return a + b; }",
		"x => x + 1;",
		"class Foo { constructor(x: number) { this.x = x; } bar(): number { return this.x; } }",
		"new Foo(1);",
		"for (let i = 0; i < 10; i++) { console.log(i); }",
		"for (const x of arr) { }",
		"while (true) { break; }",
		"try { throw new Error(\"x\"); } catch (e) { }",
		"switch (x) { case 1: break; default: break; }",
		"const obj = { a: 1, b: 2 };",
		"const arr = [1, 2, 3];",
		"async function f() { await g(); }",
		"/** @type {int32} */\nlet x = 1;",
		"(",
		")",
		"{{{{{{{{{{",
		"function f( f(",
		"const x = ;",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	_ = filepath.Walk("../examples", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".ts" {
			return nil
		}
		if data, err := os.ReadFile(path); err == nil {
			f.Add(string(data))
		}
		return nil
	})

	f.Fuzz(func(t *testing.T, src string) {
		_, _ = parser.Parse(src)
	})
}
