package tests

import (
	"bufio"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Piped stdout must be delivered incrementally, not withheld until exit
// (ADR-00867). C stdio full-buffers a non-TTY stream, so before the fix a
// program printing to a pipe (`prog | tee`, a Docker log collector) showed
// nothing until it exited — unlike Node, which flushes stdout as produced.
//
// This is a deterministic handshake rather than a timing assertion: the child
// writes a newline-less marker via process.stdout.write and only then waits for
// a stdin line; the producer reads the marker off the pipe before sending that
// line. If stdout were withheld until exit, the marker would never arrive and
// the child would block on stdin forever — a deadlock the test would hit as a
// timeout. A newline-less write also exercises the per-write flush (line
// buffering alone would hold it until a newline).
func TestE2EStdoutFlushesIncrementally(t *testing.T) {
	bin := buildBinary(t, `
process.stdout.write("MARKER")
process.stdin.on('data', (c: string) => {
  if (c.trim().length > 0) { console.log("\nDONE"); }
})
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
	// Read the newline-less MARKER off the pipe. If stdout were buffered until
	// exit it would never arrive (the child is blocked on stdin), so cap the
	// read with a deadline to fail as a clear error instead of hanging.
	markerCh := make(chan string, 1)
	reader := bufio.NewReader(stdout)
	var tail strings.Builder
	go func() {
		buf := make([]byte, 6) // len("MARKER")
		if _, err := io.ReadFull(reader, buf); err != nil {
			markerCh <- ""
			return
		}
		markerCh <- string(buf)
		rest, _ := io.ReadAll(reader)
		tail.Write(rest)
	}()
	select {
	case got := <-markerCh:
		if got != "MARKER" {
			t.Fatalf("stdout not flushed incrementally: expected %q before exit, got %q", "MARKER", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for MARKER — stdout was withheld until exit")
	}
	// The handshake held: trigger the child to finish.
	io.WriteString(stdin, "go\n")
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !strings.Contains(tail.String(), "DONE") {
		t.Fatalf("missing DONE after trigger, got tail %q", tail.String())
	}
}
