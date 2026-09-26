package llvm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnnotateClangOutputNamesTheEmittingSite(t *testing.T) {
	dir := t.TempDir()
	ll := filepath.Join(dir, "m.ll")
	ir := strings.Join([]string{
		"; @site runtime_x.go:10 < emitter.go:20",
		"define void @rt() {",
		"entry:",
		"  %a = add i64 1, 2",
		"  ret void",
		"}",
		"define void @user() {",
		"entry:",
		"  %b = call i32 @f(ptr 0x0)  ; @site emit_stmts.go:1427 < emit_stmts.go:181",
		"  ret void",
		"}",
	}, "\n")
	if err := os.WriteFile(ll, []byte(ir), 0644); err != nil {
		t.Fatal(err)
	}
	out := ll + ":9:26: error: floating point constant invalid for type\n" +
		ll + ":4:8: error: something inside a runtime chunk\n" +
		"1 error generated.\n"
	got := AnnotateClangOutput([]byte(out))
	for _, want := range []string{
		"invalid for type\n  emitted by emit_stmts.go:1427 < emit_stmts.go:181\n",
		"runtime chunk\n  emitted by runtime_x.go:10 < emitter.go:20\n",
		"1 error generated.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestAnnotateClangOutputLeavesOtherOutputAlone(t *testing.T) {
	out := "ld: library not found for -lfoo\nclang: error: linker command failed\n"
	if got := AnnotateClangOutput([]byte(out)); got != out {
		t.Errorf("changed output without IR errors:\n%s", got)
	}
}

func TestWithSiteRecordsTheCallerOutsideTheWriters(t *testing.T) {
	old := irSites
	irSites = true
	defer func() { irSites = old }()
	line := withSite("%x = add i64 1, 2")
	if !strings.Contains(line, "; @site ir_sites_test.go:") {
		t.Errorf("site not recorded: %q", line)
	}
	chunk := withSite("define void @f() {\n  ret void\n}")
	if !strings.HasPrefix(chunk, "; @site ir_sites_test.go:") || !strings.Contains(chunk, "\ndefine void @f()") {
		t.Errorf("multi-line chunk not prefixed: %q", chunk)
	}
}
