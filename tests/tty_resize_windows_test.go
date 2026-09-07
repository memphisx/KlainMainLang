package tests

import (
	"strings"
	"testing"
	"time"
)

// process.on('SIGWINCH') fires when the console is resized. Windows has no
// SIGWINCH signal, so a read-only console-size watcher thread queues the
// event through the same path as SIGINT (ADR-00749); the handler runs on the
// main thread from the event loop and re-reads process.stdout.columns/.rows.
// Verified under the pseudo-console by resizing it mid-run.
func TestE2ETtyConsoleResizeSIGWINCH(t *testing.T) {
	bin := buildBinary(t, `
process.on('SIGWINCH', () => {
  console.log("RESIZE " + process.stdout.columns + "x" + process.stdout.rows)
})
setTimeout(() => {}, 3000)
`)
	res := runInConPTY(t, bin, 80, 24, 20*time.Second, func(write func(string)) {
		time.Sleep(700 * time.Millisecond)
		conptyResize(120, 40)
		time.Sleep(700 * time.Millisecond)
	})
	if !strings.Contains(res.Output, "RESIZE 120x40") {
		t.Fatalf("SIGWINCH did not fire with the new size:\n%q", res.Output)
	}
}
