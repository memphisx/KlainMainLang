package llvm

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// userErrorRe is a codegen error positioned in the user's source: a
// rejection of a program, which P2.7 (TDD-00230) moves onto the checker
// as a diagnostic or replaces with a supported feature.
var userErrorRe = regexp.MustCompile(`fmt\.Errorf\("%d:%d: `)

// userErrorCeiling is the number of user-error sites codegen has left. It
// only falls: a site removed lowers it, and none is added.
const userErrorCeiling = 0

// barErrorRe is an emission error with no source position: one a user's
// program can reach reports nowhere. Internal errors and wrappers of
// another error are not counted.
var barErrorRe = regexp.MustCompile(`fmt\.Errorf\("([^"\\]|\\.)*"`)

// barErrorCeiling is the number of position-less emission errors left. It
// only falls: give a site its position (errorAt / unsupportedAt / internalAt)
// or remove it.
const barErrorCeiling = 25

func TestEmitErrorsCarryPositions(t *testing.T) {
	files, _ := filepath.Glob("emit*.go")
	n := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range barErrorRe.FindAll(src, -1) {
			lit := string(m[len(`fmt.Errorf("`) : len(m)-1])
			if strings.HasPrefix(lit, "%d:%d:") || strings.HasPrefix(lit, "internal") || strings.HasPrefix(lit, "%s internal") || strings.Contains(lit, "%w") || strings.Contains(lit, ": %v") || lit == "%s" {
				continue
			}
			n++
		}
	}
	switch {
	case n > barErrorCeiling:
		t.Errorf("%d position-less emission errors, ceiling %d — give the error its source position", n, barErrorCeiling)
	case n < barErrorCeiling:
		t.Errorf("%d position-less emission errors, ceiling %d — lower the ceiling", n, barErrorCeiling)
	}
}

func TestCodegenUserErrorsOnlyShrink(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	n := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		n += len(userErrorRe.FindAllIndex(src, -1))
	}
	switch {
	case n > userErrorCeiling:
		t.Errorf("%d codegen user-error sites, ceiling %d — reject in the checker with a diagnostic, or support the feature", n, userErrorCeiling)
	case n < userErrorCeiling:
		t.Errorf("%d codegen user-error sites, ceiling %d — lower the ceiling", n, userErrorCeiling)
	}
}
