package llvm

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A rejection writes the reason's payload and its tag, then settles: one
// function does that (__kml_promise_reject_with, behind emitRejectPromise
// and its runtime forms). A rejecting settle anywhere else is a reason
// written by hand, the shape that once left the tag unset.
func TestRejectionsGoThroughOneWriter(t *testing.T) {
	settle := regexp.MustCompile(`__kml_promise_settle\(ptr [^,]*, i64 2\)`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		in := ""
		for i, ln := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(ln, "define ") {
				in = ln
			}
			if settle.MatchString(ln) && !strings.Contains(in, "@__kml_promise_reject_with(") {
				t.Errorf("%s:%d: a rejecting settle outside __kml_promise_reject_with; use emitRejectPromise or a __kml_promise_reject_* form", f, i+1)
			}
		}
	}
}
