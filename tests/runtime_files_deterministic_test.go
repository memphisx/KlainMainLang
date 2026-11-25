package tests

import (
	"os/exec"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/resolver"
)

// The runtime files a program links are the same on every compile: the
// choice of which file answers a symbol depends on what is linked so far,
// so it must not follow a map's order (ADR-02073). examples/date/date.ts
// linked task.c on about one compile in four before.
func TestE2ERuntimeFilesDeterministic(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	var first string
	for i := 0; i < 12; i++ {
		prog, err := resolver.ResolveProgram("../examples/date/date.ts")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		em := llvm.NewEmitter()
		if _, err := em.EmitProgram(prog); err != nil {
			t.Fatalf("codegen: %v", err)
		}
		srcs, err := em.EmbeddedCSources()
		if err != nil {
			t.Fatalf("runtime sources: %v", err)
		}
		var names []string
		for _, cs := range srcs {
			names = append(names, cs.Name)
		}
		got := strings.Join(names, " ")
		if i == 0 {
			first = got
		} else if got != first {
			t.Fatalf("compile %d links %q, compile 1 linked %q", i+1, got, first)
		}
	}
}
