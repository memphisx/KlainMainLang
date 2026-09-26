package lexer_test

import (
	"os"
	"path/filepath"
	"testing"

	"KlainMainLang/lexer"
)

// FuzzScan checks that scanning arbitrary input never panics or loops: it
// always reaches EOF or an ILLEGAL token.
func FuzzScan(f *testing.F) {
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
		// Scanning must terminate at EOF or an ILLEGAL token, never panic or loop.
		l := lexer.New(src)
		for i := 0; ; i++ {
			tok := l.Scan(lexer.ScanDefault)
			if tok.Type == lexer.EOF || tok.Type == lexer.ILLEGAL {
				return
			}
			if i > len(src)+1 {
				t.Fatalf("scan of %q did not reach EOF", src)
			}
		}
	})
}
