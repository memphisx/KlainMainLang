package tests

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
)

// The program's own unit is what every compile runs clang over; the
// builtin library's modules, its function literals and the helpers
// generated for its types are separate units, cached once (TDD-00248). An
// http2 server pulls in most of the library, so its program unit growing
// past the budget means library code is landing in it again: before
// TDD-00248 it was ~30 MB and every build of it took 11-14 s of clang.
func TestProgramUnitStaysSmall(t *testing.T) {
	const budget = 6 << 20
	bin := buildCLI(t)
	// From the module root: the suite runs from tests/ or from the root.
	root := findRepoRoot(t)
	emit := func(src string) string {
		var stderr strings.Builder
		cmd := exec.Command(bin, "-emit-llvm", filepath.Join(root, src))
		cmd.Stderr = &stderr
		ir, err := cmd.Output()
		if err != nil {
			t.Fatalf("emit %s: %v\n%s", src, err, stderr.String())
		}
		return string(ir)
	}
	ir := emit("examples/http2/streams_server.ts")
	units, err := llvm.SplitLibUnits(ir)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(units.Program); n > budget {
		t.Fatalf("program unit is %d bytes (budget %d): library code is compiled with the program again", n, budget)
	}
	// Another program shares every unit both use, byte for byte, so the
	// cache answers it.
	units2, err := llvm.SplitLibUnits(emit("examples/http2/client_session.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for k, text := range units.Modules {
		if other, ok := units2.Modules[k]; ok && other != text {
			t.Errorf("unit %s differs between two programs: its cached object is not reused", k)
		}
	}
}
