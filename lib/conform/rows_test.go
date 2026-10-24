package conform_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"KlainMainLang/lib/conform"
)

// languageAreas are the status areas of language features, not builtins:
// their rows have no declaration.
var languageAreas = map[string]bool{
	"_index": true, "language-constructs": true, "jsdoc": true, "type-system": true,
	"typescript-features": true, "modules": true, "memory-management": true,
}

type statusRow struct {
	Feature string   `json:"feature"`
	Status  string   `json:"status"`
	Decl    []string `json:"decl"`
}

type statusArea struct {
	Segments []struct {
		Table *struct {
			Rows []statusRow `json:"rows"`
		} `json:"table"`
	} `json:"segments"`
}

// TestImplementedBuiltinsHaveDeclarations: every ✅ row of a builtin status
// area names the declarations it covers, each resolves, and each declared
// name is exercised by a test or an example (TDD-00230 phase 3's exit
// criterion).
func TestImplementedBuiltinsHaveDeclarations(t *testing.T) {
	root := filepath.Join("..", "..")
	exercised := testCorpus(t, root)
	files, _ := filepath.Glob(filepath.Join(root, "docs", "status", "data", "*.json"))
	for _, f := range files {
		area := strings.TrimSuffix(filepath.Base(f), ".json")
		if languageAreas[area] {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var a statusArea
		if err := json.Unmarshal(data, &a); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, s := range a.Segments {
			if s.Table == nil {
				continue
			}
			for _, r := range s.Table.Rows {
				if r.Status != "implemented" {
					continue
				}
				if len(r.Decl) == 0 {
					t.Errorf("%s: %s: no decl", area, r.Feature)
					continue
				}
				for _, d := range r.Decl {
					if err := conform.ResolveDecl(d); err != nil {
						t.Errorf("%s: %s: %v", area, r.Feature, err)
						continue
					}
					if n := exercisedName(d); n != "" && !exercised(n) {
						t.Errorf("%s: %s: %s: no test or example names %q", area, r.Feature, d, n)
					}
				}
			}
		}
	}
}

// exercisedName is the identifier a test of declaration d must name.
func exercisedName(d string) string {
	if d == "syntax" {
		return ""
	}
	if mod, export, ok := strings.Cut(d, "#"); ok {
		if export == "" {
			return mod
		}
		return export
	}
	if i := strings.LastIndex(d, "."); i > 0 {
		return d[i+1:]
	}
	return d
}

// testCorpus reports whether a name appears as a word in the E2E tests or
// the examples.
func testCorpus(t *testing.T, root string) func(string) bool {
	t.Helper()
	var sb strings.Builder
	for _, pat := range []string{"tests/*.go", "examples/*.ts", "examples/*/*.ts", "examples/*/*/*.ts", "examples/*.js"} {
		files, _ := filepath.Glob(filepath.Join(root, pat))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err == nil {
				sb.Write(b)
				sb.WriteByte('\n')
			}
		}
	}
	corpus := sb.String()
	cache := map[string]bool{}
	return func(name string) bool {
		if v, ok := cache[name]; ok {
			return v
		}
		v := regexp.MustCompile(`(^|[^A-Za-z0-9_$])` + regexp.QuoteMeta(name) + `($|[^A-Za-z0-9_$])`).MatchString(corpus)
		cache[name] = v
		return v
	}
}
