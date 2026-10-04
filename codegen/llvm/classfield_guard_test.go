package llvm

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A private field (`#reason`) exists only on a class, and a class type a
// value carries may predate its final layout: such a field is reached
// through classField, never a raw FieldIndex.
func TestPrivateFieldsGoThroughClassField(t *testing.T) {
	raw := regexp.MustCompile(`\.FieldIndex\("#`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, ln := range strings.Split(string(src), "\n") {
			if raw.MatchString(ln) {
				t.Errorf("%s:%d: a private field by raw FieldIndex; use e.classField", f, i+1)
			}
		}
	}
}
