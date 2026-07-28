package lexer_test

import (
	"os"
	"path/filepath"
	"testing"

	"KlainMainLang/lexer"
)

// FuzzTokenize checks that Tokenize never panics or hangs on arbitrary input,
// and that a successful tokenization always ends in a trailing EOF token.
func FuzzTokenize(f *testing.F) {
	seeds := []string{
		"",
		"42",
		"3.14",
		`"hello"`,
		`'world'`,
		"let x = 1;",
		"a + b * c - d / e % f",
		"x => x + 1",
		"`x = ${x}`",
		"`nested ${`${a}`}`",
		"class Foo { constructor() {} }",
		"for (let i = 0; i < 10; i++) {}",
		"/** @type {int32} */",
		"/* unterminated block comment",
		"// line comment",
		"a &&= b ||= c ??= d",
		"a >>>= 1",
		"...args",
		"@bad",
		"\"unterminated string",
		"\"escaped \\\" quote\"",
		"\x00\x01\x02",
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
		toks, err := lexer.Tokenize(src)
		if err != nil {
			return // rejecting malformed input is fine; panicking is not
		}
		if len(toks) == 0 || toks[len(toks)-1].Type != lexer.EOF {
			t.Fatalf("token stream for %q missing trailing EOF: %v", src, toks)
		}
	})
}
