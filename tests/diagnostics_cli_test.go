package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The driver reports every syntax error in a file, one line each, and
// -diagnostics=json writes them as JSON on stdout.
func TestE2EDiagnosticsCLI(t *testing.T) {
	cli := buildCLI(t)
	src := filepath.Join(t.TempDir(), "bad.ts")
	if err := os.WriteFile(src, []byte("let a = ;\nlet b = 1 2\nclass C { m() { return ) } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(cli, src).CombinedOutput()
	if err == nil {
		t.Fatalf("compiled a broken file:\n%s", out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	want := []string{"1:9: unexpected token ; in expression", "2:11: expected ';', got NUMBER", "3:24: unexpected token ) in expression"}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines:\n%s", len(lines), out)
	}
	for i, w := range want {
		if !strings.HasPrefix(lines[i], "klainmain: parse error: "+src+": ") || !strings.HasSuffix(lines[i], w) {
			t.Errorf("line %d: %q, want …%s: %s", i, lines[i], src, w)
		}
	}

	cmd := exec.Command(cli, "-diagnostics=json", src)
	stdout, _ := cmd.Output()
	var ds []struct {
		Code       int
		Line, Col  int
		Start, End int
		File, Kind string
		Phase      string
		Message    string
	}
	if err := json.Unmarshal(stdout, &ds); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if len(ds) != 3 || ds[0].Code != 1109 || ds[1].Code != 1005 || ds[0].Start != 8 || ds[0].End != 9 ||
		ds[0].File != src || ds[0].Kind != "SyntaxError" || ds[0].Phase != "parse" {
		t.Errorf("got %+v", ds)
	}
}
