package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// --- Streaming process.stdin (ADR-00339) ---
//
// process.stdin.on('data', cb) / .on('end', cb) — the flowing Readable over
// fd 0. Non-blocking stdin folds into the same select() loop as readline and
// child_process. 'data' delivers each read chunk as a UTF-8 string; 'end' fires
// once on EOF. process is ambient (no import needed).

// runStdinStream builds a (non-import) program and feeds it stdin.
func runStdinStream(t *testing.T, src, stdin string) string {
	t.Helper()
	bin := buildBinary(t, src)
	cmd := exec.Command(bin)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.TrimRight(string(out), "\n")
}

func TestE2EStdinDataAndEnd(t *testing.T) {
	got := runStdinStream(t, `
let total = 0
process.stdin.on('data', (chunk: string) => { total = total + chunk.length })
process.stdin.on('end', () => { console.log("bytes " + total) })
`, "hello\nworld\n")
	if got != "bytes 12" {
		t.Fatalf("got: %q, want %q", got, "bytes 12")
	}
}

// A 'data' listener that transforms and echoes each chunk (the canonical
// pipe/filter shape). Piped input arrives in one read here.
func TestE2EStdinTransform(t *testing.T) {
	got := runStdinStream(t, `
process.stdin.on('data', (c: string) => { process.stdout.write(c.toUpperCase()) })
`, "abc\ndef")
	if got != "ABC\nDEF" {
		t.Fatalf("got: %q", got)
	}
}

// EOF on empty input still fires 'end'.
func TestE2EStdinEmptyEnd(t *testing.T) {
	got := runStdinStream(t, `
process.stdin.on('data', (c: string) => { process.stdout.write(c) })
process.stdin.on('end', () => { console.log("done") })
`, "")
	if got != "done" {
		t.Fatalf("got: %q", got)
	}
}

// Multi-chunk input (larger than one 4096 read) accumulates correctly.
func TestE2EStdinMultiChunk(t *testing.T) {
	got := runStdinStream(t, `
let bytes = 0
process.stdin.on('data', (c: string) => { bytes = bytes + c.length })
process.stdin.on('end', () => { console.log(bytes) })
`, strings.Repeat("x", 100000))
	if got != "100000" {
		t.Fatalf("got: %q, want 100000", got)
	}
}

// An unsupported event is a clean compile error.
func TestE2EStdinUnsupportedEventRejected(t *testing.T) {
	_, err := parseAndCompile(`
process.stdin.on('close', () => {})
`)
	if err == nil {
		t.Fatal("expected a compile error for an unsupported process.stdin event, got none")
	}
}
