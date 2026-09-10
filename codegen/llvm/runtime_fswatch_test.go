package llvm

import (
	"strings"
	"testing"
)

// The darwin kqueue fs.watch dispatch emits
//
//	%evstr = select i1 %isrename, ptr <"rename">, ptr <"change">
//
// where both operands must be interned-string pointers. A Sprintf arg-order slip
// (all the FSWatcher-struct GEPs listed before the two strings, when one GEP
// actually follows the select in the template) put fsWatcherStructIR into the
// select's first operand, producing invalid `select … ptr { … }` IR — which only
// failed when compiling an fs.watch program on macOS. This exercises the darwin
// emitter directly (no GOOS gate on the method), so the arg alignment is guarded
// on every platform, not just when a Mac runner happens to compile it.
func TestFsWatchDarwinEventSelectWellFormed(t *testing.T) {
	e := NewEmitter()
	e.emitFsWatchDarwin()
	ir := e.globals.String()

	var sel string
	for _, ln := range strings.Split(ir, "\n") {
		if strings.Contains(ln, "%evstr = select") {
			sel = strings.TrimSpace(ln)
			break
		}
	}
	if sel == "" {
		t.Fatal("no evstr select emitted by emitFsWatchDarwin")
	}
	// A struct-type token where a value belongs (the bug) => invalid IR.
	if strings.Contains(sel, "ptr {") {
		t.Fatalf("evstr select has a struct-type operand (Sprintf arg misalignment): %s", sel)
	}
	// Both operands must be interned-string pointers (getelementptr into @.s*).
	if strings.Count(sel, "ptr getelementptr") != 2 {
		t.Fatalf("evstr select operands are not two string-GEP pointers: %s", sel)
	}
}
