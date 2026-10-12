package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// console.countReset of a label never counted is Node's process warning
// "Count for '<label>' does not exist", and resets nothing (BACKLOG 158).
func TestConsoleCountResetUnknownLabelWarns(t *testing.T) {
	bin := buildBinaryImports(t, `
console.count("a");
console.countReset("a");
console.countReset("zz");
console.countReset();
console.count();
console.count("a");
const l: any = 5;
console.count(l);
console.countReset(l);
console.count(l);
`)
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := string(out)
	for _, want := range []string{"a: 1\n", "default: 1\n", "Warning: Count for 'zz' does not exist", "Warning: Count for 'default' does not exist"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Count(s, "a: 1\n") != 2 {
		t.Errorf("countReset('a') should restart a at 1:\n%s", s)
	}
	// A non-string label is looked up as given: it never matches, so 5
	// keeps counting and the reset warns.
	if !strings.Contains(s, "5: 2\n") || !strings.Contains(s, "Count for '5' does not exist") {
		t.Errorf("countReset(5) should warn and leave the count:\n%s", s)
	}
}
