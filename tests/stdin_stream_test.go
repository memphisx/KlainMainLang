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
// The property under test: a `setInterval` timer keeps firing while `process.stdin`
// is idle — i.e. the runtime never parks the event loop inside a blocking stdin
// read. The proof is driven by *feedback*, not wall-clock timing: the child emits
// a `tick` heartbeat on every interval fire, and the driver advances the protocol
// only after it has *observed* fresh heartbeats during each idle window (bounded
// only by a generous overall deadline). A slow or contended host simply produces
// heartbeats more slowly — the test still passes — whereas a genuinely blocking
// read produces *no* heartbeats during the idle window, which the per-window wait
// catches as a timeout. This deliberately replaces the earlier fixed-80ms-gap
// design (ADR-00867), whose strict "count advanced across an 80ms wall-clock
// window" assertion could false-fail on an oversubscribed CI runner that starved
// the child of CPU for the whole window (ADR-00935).
func TestE2EStdinDoesNotBlockLoop(t *testing.T) {
	bin := buildBinary(t, `
console.error("READY")
let ticks = 0
const iv = setInterval(() => { ticks = ticks + 1; console.log("tick " + ticks) }, 15)
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
	// Handshake: wait for READY on stderr (unbuffered) before writing.
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

	// One goroutine drains stdout into a channel so the child never blocks on a
	// full pipe; the driver consumes lines as the protocol needs them.
	lineCh := make(chan string, 4096)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lineCh <- sc.Text()
		}
		close(lineCh)
	}()

	var transcript []string
	tickRe := regexp.MustCompile(`^tick (\d+)$`)
	// nextLine reads the next stdout line, failing the test on a generous timeout
	// (a stalled loop, not a slow one, is what that timeout means).
	nextLine := func(what string) string {
		select {
		case ln, ok := <-lineCh:
			if !ok {
				t.Fatalf("stdout closed early while waiting for %s:\n%s", what, strings.Join(transcript, "\n"))
			}
			transcript = append(transcript, ln)
			return ln
		case <-time.After(15 * time.Second):
			t.Fatalf("timed out waiting for %s — the event loop is stalled (stdin read blocked it):\n%s", what, strings.Join(transcript, "\n"))
			return ""
		}
	}
	// awaitTicks consumes stdout until it has seen `n` fresh `tick` heartbeats,
	// returning the last tick value observed. Any non-tick line (a `chunk`/`end`
	// echo) encountered along the way is handed to `onOther`. This is the core
	// proof: it returns iff the timer actually fired `n` more times, however long
	// the host took to schedule them.
	awaitTicks := func(n int, what string, onOther func(string)) int {
		seen, last := 0, 0
		for seen < n {
			ln := nextLine(what)
			if m := tickRe.FindStringSubmatch(ln); m != nil {
				last, _ = strconv.Atoi(m[1])
				seen++
				continue
			}
			if onOther != nil {
				onOther(ln)
			}
		}
		return last
	}

	// Prove the loop is alive before any stdin activity.
	awaitTicks(2, "initial heartbeats", nil)

	chunkRe := regexp.MustCompile(`^chunk (\w+) ticks=(\d+)$`)
	chunkTicks := map[string]int{}
	for _, s := range []string{"one", "two", "three"} {
		if _, err := io.WriteString(stdin, s+"\n"); err != nil {
			t.Fatalf("write %q: %v", s, err)
		}
		// Wait for this chunk's echo, tolerating heartbeats interleaved before it.
		var echoed bool
		for !echoed {
			ln := nextLine("chunk " + s + " echo")
			if m := chunkRe.FindStringSubmatch(ln); m != nil {
				if m[1] != s {
					t.Fatalf("expected echo for chunk %q, got %q:\n%s", s, ln, strings.Join(transcript, "\n"))
				}
				chunkTicks[s], _ = strconv.Atoi(m[2])
				echoed = true
			}
			// otherwise it's a heartbeat: drain and keep waiting.
		}
		// The idle window: stdin is quiet until the next write. Requiring fresh
		// heartbeats here is the non-block proof — a blocking read yields none and
		// nextLine's timeout fires.
		awaitTicks(2, "heartbeats during idle window after chunk "+s, nil)
	}

	if err := stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}
	// Drain to the 'end' line (EOF handler), tolerating trailing heartbeats that
	// may race ahead of it.
	var endTicks int
	endRe := regexp.MustCompile(`^end ticks=(\d+)$`)
	for {
		ln := nextLine("end line")
		if m := endRe.FindStringSubmatch(ln); m != nil {
			endTicks, _ = strconv.Atoi(m[1])
			break
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait: %v\n%s", err, strings.Join(transcript, "\n"))
	}

	// Sanity: the per-chunk tick counts must be non-decreasing in arrival order,
	// and 'end' must not regress. (Strict advance is already guaranteed by the
	// awaitTicks gating between chunks; this just guards the observed values.)
	if !(chunkTicks["one"] <= chunkTicks["two"] && chunkTicks["two"] <= chunkTicks["three"] && chunkTicks["three"] <= endTicks) {
		t.Fatalf("tick counts not monotonic: one=%d two=%d three=%d end=%d:\n%s",
			chunkTicks["one"], chunkTicks["two"], chunkTicks["three"], endTicks, strings.Join(transcript, "\n"))
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
