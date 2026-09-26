package conform_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"KlainMainLang/lib/conform"
)

// TestDeclarationsConformToTypeScript: every builtin declaration exists in
// TypeScript's library with the same arity and readonly-ness (allowlisted
// divergences aside). Skipped when the corpus has not been fetched.
func TestDeclarationsConformToTypeScript(t *testing.T) {
	tsLib := filepath.Join("..", "..", ".ts-tests", "src", "lib")
	if _, err := os.Stat(tsLib); err != nil {
		t.Skip("TypeScript library not fetched (tools/conformance/fetch.sh)")
	}
	problems, _, err := conform.Diff(tsLib, allowlist(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

func allowlist(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "tools", "libconform", "allow.txt"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, l := range splitLines(string(data)) {
		if l != "" && l[0] != '#' {
			out[l] = true
		}
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			line := s[start:i]
			for len(line) > 0 && (line[len(line)-1] == ' ' || line[len(line)-1] == '\r') {
				line = line[:len(line)-1]
			}
			out = append(out, line)
			start = i + 1
		}
	}
	return out
}

// TestGlobalNamesUpToDate: lib/tsglobals.txt is what tools/libconform
// -globals generates from the pinned TypeScript library and @types/node.
// Skipped when they have not been fetched.
func TestGlobalNamesUpToDate(t *testing.T) {
	tsLib := filepath.Join("..", "..", ".ts-tests", "src", "lib")
	typesNode := filepath.Join("..", "..", ".types-node")
	for _, d := range []string{tsLib, typesNode} {
		if _, err := os.Stat(d); err != nil {
			t.Skip("TypeScript library or @types/node not fetched (tools/conformance/fetch.sh)")
		}
	}
	names, err := conform.GlobalNames(tsLib, typesNode)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "tsglobals.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var have []string
	for _, l := range splitLines(string(data)) {
		if l != "" && l[0] != '#' {
			have = append(have, l)
		}
	}
	if strings.Join(have, "\n") != strings.Join(conform.GlobalLines(names), "\n") {
		t.Error("lib/tsglobals.txt is stale: go run ./tools/libconform -globals lib/tsglobals.txt")
	}
}
