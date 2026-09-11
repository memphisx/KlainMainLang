package tests

import (
	"bufio"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
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

// A slow producer on stdin must not block the event loop: a setInterval keeps
// firing while the program waits for the next chunk. On the pre-reactor
// Windows path (TDD-00180 §1) the loop fell into a blocking CRT `_read` and
// the timer starved until the next line arrived; the IOCP reactor
// (TDD-00183 Stage 1) makes stdin non-blocking and blocks the loop only in
// the completion-port wait, so ticks interleave with the chunks. Driven from
// Go with real delays between writes — a strings.NewReader would present all
// input at once and never exercise the wait.
func TestE2EStdinDoesNotBlockLoop(t *testing.T) {
	bin := buildBinary(t, `
let ticks = 0
const iv = setInterval(() => { ticks = ticks + 1 }, 15)
process.stdin.on('data', (c: string) => {
  const s = c.trim()
  if (s.length > 0) console.log("chunk " + s + " ticks>0:" + (ticks > 0))
})
process.stdin.on('end', () => { clearInterval(iv); console.log("end ticks>0:" + (ticks > 0)) })
`)
	cmd := exec.Command(bin)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	go func() {
		for _, s := range []string{"one", "two", "three"} {
			time.Sleep(80 * time.Millisecond) // longer than the 15ms tick
			io.WriteString(stdin, s+"\n")
		}
		stdin.Close()
	}()
	var lines []string
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	got := strings.Join(lines, "\n")
	// Every chunk must arrive, each having seen the timer tick at least once
	// (proof the loop kept running while stdin was quiet), and 'end' too.
	for _, want := range []string{
		"chunk one ticks>0:true",
		"chunk two ticks>0:true",
		"chunk three ticks>0:true",
		"end ticks>0:true",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in output:\n%s", want, got)
		}
	}
}

// setEncoding('utf8') is a faithful no-op (chunks already arrive as UTF-8
// strings) and returns the stream, so the canonical
// setEncoding().on('data').on('end') chain works (ADR-00793).
func TestE2EStdinSetEncodingAndChaining(t *testing.T) {
	got := runStdinStream(t, `
let data = ''
process.stdin.setEncoding('utf8')
  .on('data', (chunk: string) => { data = data + chunk })
  .on('end', () => { console.log('len ' + data.length) })
`, "hello\nworld\n")
	if got != "len 12" {
		t.Fatalf("got: %q, want %q", got, "len 12")
	}
}

// A non-utf8 encoding is a clean compile error, not a silently wrong decode
// (there is no Buffer chunk to re-decode).
func TestE2EStdinSetEncodingNonUtf8Rejected(t *testing.T) {
	_, err := parseAndCompile(`process.stdin.setEncoding('latin1')`)
	if err == nil {
		t.Fatal("expected a compile error for a non-utf8 process.stdin.setEncoding, got none")
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
