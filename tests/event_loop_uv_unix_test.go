//go:build !windows

package tests

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A signal wakes a loop blocked in its wait: the watcher runs at once, not
// when the 10 s timer would have ended the wait.
func TestE2ELoopSignalWakesTheWait(t *testing.T) {
	bin := buildBinaryImports(t, `
const t = setTimeout(() => console.log("timer"), 10000)
process.on("SIGUSR2", () => { console.log("signal"); clearTimeout(t) })
console.log("ready")
`)
	cmd := exec.Command(bin)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 6)
	if _, err := stdout.Read(buf); err != nil || string(buf) != "ready\n" {
		t.Fatalf("no ready line: %q %v", buf, err)
	}
	start := time.Now()
	cmd.Process.Signal(syscall.SIGUSR2)
	rest := new(strings.Builder)
	b := make([]byte, 64)
	for {
		n, err := stdout.Read(b)
		rest.Write(b[:n])
		if err != nil {
			break
		}
	}
	cmd.Wait()
	if rest.String() != "signal\n" {
		t.Fatalf("output after the signal: %q", rest.String())
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Errorf("the watcher ran %v after the signal: the wait was not woken", el)
	}
}
