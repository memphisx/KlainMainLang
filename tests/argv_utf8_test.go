package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// process.argv carries a non-ASCII argument intact. On Windows the CRT's
// argv is decoded in the ANSI code page, so "café"/"naïve"/Greek/emoji would
// arrive as '?'; the shim reconstructs argv as UTF-8 from GetCommandLineW at
// startup, as Node does (ADR-00745). Runs on every host — POSIX passes argv
// bytes through unchanged, so the same assertion holds there.
func TestE2EProcessArgvNonASCII(t *testing.T) {
	// A compiled binary's argv is [exePath, ...userArgs], so the two user
	// arguments land at argv[1] and argv[2].
	bin := buildBinary(t, `
console.log(process.argv[1])
console.log(process.argv[2])
console.log(process.argv.length)
`)
	arg1 := "café-Ω-日本語"
	arg2 := "naïve🚀"
	out, err := exec.Command(bin, arg1, arg2).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := strings.TrimRight(string(out), "\r\n")
	want := arg1 + "\n" + arg2 + "\n3"
	if got != want {
		t.Fatalf("non-ASCII argv not preserved:\n got %q\nwant %q", got, want)
	}
}
