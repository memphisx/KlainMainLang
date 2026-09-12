package tests

import (
	"bufio"
	"io"
	"os/exec"
	"regexp"
	"strconv"
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
//
// Each chunk reports the *absolute* tick count at the moment it is read, and the
// check is that the count strictly INCREASES across the quiet 80ms gaps — the
// direct statement of "the loop kept running while stdin was quiet". A blocking
// read, the regression this guards, would instead freeze the count (no tick
// between chunks), which the strict-increase check catches.
//
// The producer must not begin writing until the child's loop is actually
// running: process startup (dyld/GC/runtime init — hundreds of ms on a cold
// exec of a freshly built binary) otherwise overlaps the first writes, so they
// batch into one early read at ticks=0 and the test sees too few gaps, through
// no fault of the reactor (ADR-00867 investigation). The child prints READY on
// *stderr* (unbuffered, unlike piped stdout which flushes only at exit) and the
// producer waits for it — a handshake, not a fixed sleep, so the check stays a
// real non-blocking assertion independent of startup latency.
func TestE2EStdinDoesNotBlockLoop(t *testing.T) {
	bin := buildBinary(t, `
console.error("READY")
let ticks = 0
const iv = setInterval(() => { ticks = ticks + 1 }, 15)
process.stdin.on('data', (c: string) => {
  const s = c.trim()
  if (s.length > 0) console.log("chunk " + s + " ticks=" + ticks)
})
process.stdin.on('end', () => { clearInterval(iv); console.log("end ticks=" + ticks) })
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
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Handshake: wait for READY on stderr (with a generous cap) before writing.
	readyCh := make(chan string, 1)
	go func() {
		esc := bufio.NewScanner(stderr)
		if esc.Scan() {
			readyCh <- esc.Text()
		} else {
			readyCh <- ""
		}
	}()
	select {
	case first := <-readyCh:
		if first != "READY" {
			t.Fatalf("expected READY handshake on stderr, got %q", first)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for READY handshake")
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

	// Parse the per-chunk tick counts in arrival order.
	tickRe := regexp.MustCompile(`ticks=(\d+)`)
	var counts []int
	for _, ln := range lines {
		if m := tickRe.FindStringSubmatch(ln); m != nil {
			n, _ := strconv.Atoi(m[1])
			counts = append(counts, n)
		}
	}
	if len(counts) != 4 { // three chunks + end
		t.Fatalf("expected 4 tick-count lines (3 chunks + end), got %d:\n%s", len(counts), got)
	}
	// The two 80ms gaps between the three chunks are the quiet windows: a count
	// that strictly advances across each ⇒ the timer fired while stdin was idle
	// ⇒ the read never blocked the loop. ('end' follows the last chunk with no
	// quiet gap — stdin closes right after "three" — so it need only not regress.)
	for i := 1; i <= 2; i++ {
		if counts[i] <= counts[i-1] {
			t.Fatalf("tick count did not advance across gap %d (%d ⇒ %d) — stdin read blocked the loop:\n%s",
				i, counts[i-1], counts[i], got)
		}
	}
	if counts[3] < counts[2] {
		t.Fatalf("tick count regressed at 'end' (%d ⇒ %d):\n%s", counts[2], counts[3], got)
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
