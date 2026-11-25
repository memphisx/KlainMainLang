package llvm

import (
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestCachedUnitObjectFailedCompileWaiters: callers that wait on another
// caller's compile of the same unit take its outcome. A compile that fails
// (invalid IR) hands every waiter its error once, instead of each retrying
// the compile without end, which recursed until the stack overflowed.
func TestCachedUnitObjectFailedCompileWaiters(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not on PATH")
	}
	dir := t.TempDir()
	bad := "define void @f() {\n  ret i32 0\n}\n" // a void function returning a value
	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = cachedUnitObject(dir, "bad", bad, nil)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err == nil || !strings.Contains(err.Error(), "compiling library module bad") {
			t.Fatalf("caller %d: want the compile error, got %v", i, err)
		}
	}
	// A failed compile keeps its text for the report: one file, one compile.
	if kept, _ := filepath.Glob(filepath.Join(dir, "*.ll")); len(kept) != 1 {
		t.Fatalf("want the unit compiled once, got %d compiles", len(kept))
	}
}
