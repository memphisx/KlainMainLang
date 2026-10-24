package llvm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExceptionLoweringIsOnePrimitive: codegen opens and closes a catch
// frame only through excEnter/excLeave, throws through excThrow/
// excThrowAny, and switches fibers only through
// fiberSwitch (exc_prims.go). The runtime_*.go IR-text helpers are frozen
// by the IR-text ratchet and leave as they move to C.
func TestExceptionLoweringIsOnePrimitive(t *testing.T) {
	files, _ := filepath.Glob("emit_*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, ln := range strings.Split(string(src), "\n") {
			for _, raw := range []string{"@__kml_push_jmpbuf()", "@__kml_pop_jmpbuf()", "setjmpCall(", "@swapcontext(", `emitInstr(fmt.Sprintf("call void @__kml_throw(`, `emitInstr(fmt.Sprintf("call void @__kml_throw_any(`} {
				if strings.Contains(ln, raw) && !strings.Contains(ln, "declare ") {
					t.Errorf("%s:%d: %s outside exc_prims.go: %s", f, i+1, raw, strings.TrimSpace(ln))
				}
			}
		}
	}
}
