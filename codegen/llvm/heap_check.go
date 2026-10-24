package llvm

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// heap_check.go — the check that every allocation and every store into the
// heap is emitted by a heap primitive (heap_prims.go), TDD-00230 P3.3a.
// KML_HEAPCHECK=panic rejects any other; KML_HEAPCHECK=<file> appends the
// emitting Go site of each to the file instead, once per site. Off, it costs
// one flag test per instruction. Read per emitter, so a test harness that
// sets it before compiling turns it on. The E2E harness runs under panic.

var (
	reHeapAlloc   = regexp.MustCompile(`call (ptr @(malloc|calloc|realloc)|void @free)\(`)
	heapCheckSeen = map[string]bool{} // sites already recorded by this process
)

// heapCheckInstr checks one instruction emitInstr emits outside a primitive.
func (e *Emitter) heapCheckInstr(line string) {
	kind := ""
	switch {
	case strings.HasPrefix(line, "store "):
		i := strings.LastIndex(line, ", ptr ")
		if i < 0 {
			return
		}
		dst := line[i+len(", ptr "):]
		if j := strings.IndexAny(dst, ", "); j >= 0 {
			dst = dst[:j]
		}
		if e.isStackPtr(dst, 0) {
			return
		}
		kind = "store"
	case reHeapAlloc.MatchString(line):
		kind = "alloc"
	default:
		return
	}
	var pcs [8]uintptr
	n := runtime.Callers(3, pcs[:])
	f, _ := runtime.CallersFrames(pcs[:n]).Next()
	site := fmt.Sprintf("%s:%d", filepath.Base(f.File), f.Line)
	if e.heapCheck == "panic" {
		panic(fmt.Sprintf("heap %s outside a heap primitive at %s: %s", kind, site, line))
	}
	if heapCheckSeen[kind+site] {
		return
	}
	heapCheckSeen[kind+site] = true
	if w, err := os.OpenFile(e.heapCheck, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintf(w, "%s\t%s\t%s\n", kind, site, line)
		w.Close()
	}
}

// isStackPtr reports whether pointer operand p addresses the current
// function's stack (an alloca, or an element of one) or a global.
func (e *Emitter) isStackPtr(p string, depth int) bool {
	if strings.HasPrefix(p, "@") || strings.HasPrefix(p, "getelementptr") {
		return true // a global, or a constant expression over one
	}
	if !strings.HasPrefix(p, "%") || depth > 8 {
		return false
	}
	if strings.Contains(e.allocas.String(), p+" = alloca ") {
		return true
	}
	body := "\n" + e.body.String()
	// A shadow-stack thread-local's address (frameSlot): a global's.
	if strings.Contains(body, "\n  "+p+" = call ptr @__kml_frame_top_slot()") ||
		strings.Contains(body, "\n  "+p+" = call ptr @__kml_frame_base_slot()") {
		return true
	}
	def := "\n  " + p + " = getelementptr "
	i := strings.Index(body, def)
	if i < 0 {
		return false
	}
	rest := body[i+len(def):]
	if j := strings.IndexByte(rest, '\n'); j >= 0 {
		rest = rest[:j]
	}
	k := strings.Index(rest, ", ptr ")
	if k < 0 {
		return false
	}
	base := rest[k+len(", ptr "):]
	if j := strings.IndexAny(base, ", "); j >= 0 {
		base = base[:j]
	}
	return e.isStackPtr(base, depth+1)
}
