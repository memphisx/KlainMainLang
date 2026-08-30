package tests

import (
	"strings"
	"testing"
)

// TDD-00031 terminal-control primitives. The interactive path (raw single-key
// reads off a real terminal, live ioctl(TIOCGWINSZ), SIGWINCH resize) was
// verified manually under a pty on macOS — see ADR-00518. These E2E tests
// cover the deterministic, dep-free surface: that the klain:tty shim links and
// runs, that klain:tty.readByte/readKey pull bytes off a piped stdin, that the
// columns/rows fall back to 80x24 off a non-TTY, and that setRawMode is a clean
// no-op on a pipe (tcgetattr fails, so the terminal is left untouched).

// klain:tty.readByte reads single bytes off fd 0, returning -1 at EOF.
func TestE2ETtyReadBytePiped(t *testing.T) {
	got := runImportsStdin(t, `
import { readByte } from 'klain:tty'
let out = ""
let b: number = readByte()
while (b !== -1) {
  out = out + b + " "
  b = readByte()
}
console.log(out.trim())
`, "AB")
	if got != "65 66" {
		t.Fatalf("got %q, want %q", got, "65 66")
	}
}

// klain:tty.readKey returns the whole burst of one read() as a string; over a
// pipe the buffered bytes arrive together.
func TestE2ETtyReadKeyPiped(t *testing.T) {
	got := runImportsStdin(t, `
import { readKey } from 'klain:tty'
const k: string = readKey()
let codes = ""
for (let i = 0; i < k.length; i++) { codes = codes + k.charCodeAt(i) + " " }
console.log(codes.trim())
`, "\x1b[A")
	if got != "27 91 65" {
		t.Fatalf("got %q, want %q (ESC [ A)", got, "27 91 65")
	}
}

// process.stdout.columns/.rows fall back to 80x24 when stdout is not a TTY
// (piped/redirected), a documented divergence from Node's `undefined`.
func TestE2ETtyWinSizeFallback(t *testing.T) {
	got := compileAndRun(t, `
const c: number = process.stdout.columns
const r: number = process.stdout.rows
console.log(c + "x" + r)
`)
	if got != "80x24" {
		t.Fatalf("got %q, want 80x24", got)
	}
}

// setRawMode is a clean no-op on a non-terminal fd 0 (tcgetattr fails), so a
// program pairing it with a piped read still works — and, crucially, does not
// flip fd 0 to the O_NONBLOCK mode the streaming .on('data') reader uses, which
// would make the synchronous read see EAGAIN as EOF.
func TestE2ETtySetRawModePipedNoop(t *testing.T) {
	got := runImportsStdin(t, `
import { readByte } from 'klain:tty'
process.stdin.setRawMode(true)
const b: number = readByte()
process.stdin.setRawMode(false)
console.log("byte " + b)
`, "Z")
	if got != "byte 90" {
		t.Fatalf("got %q, want %q", got, "byte 90")
	}
}

// process.on('SIGWINCH', handler) compiles (the resize handler is added to the
// existing signal allowlist). A program that only registers it and exits
// immediately never receives the signal — this just asserts acceptance.
func TestE2ESigwinchAccepted(t *testing.T) {
	got := compileAndRun(t, `
process.on('SIGWINCH', () => { console.log("resized") })
console.log("ok")
`)
	if got != "ok" {
		t.Fatalf("got %q, want ok", got)
	}
}

// An unknown signal name is still a clean compile error, now naming SIGWINCH
// among the supported set.
func TestE2EProcessOnUnknownSignalRejected(t *testing.T) {
	_, err := parseAndCompile(`process.on('SIGHUP', () => {})`)
	if err == nil {
		t.Fatal("expected a compile error for an unsupported signal name")
	}
	if !strings.Contains(err.Error(), "SIGWINCH") {
		t.Fatalf("error should list the supported signals including SIGWINCH: %v", err)
	}
}

// klain:tty.readKey(timeoutMs) polls: it returns the buffered key when one is
// ready and an empty string once the stream is drained, without blocking
// forever — the tick a self-refreshing TUI loop redraws on. Over a pipe the
// bytes are ready immediately, then EOF reads back as "".
func TestE2ETtyReadKeyTimeout(t *testing.T) {
	got := runImportsStdin(t, `
import { readKey } from 'klain:tty'
const a: string = readKey(1000)
const b: string = readKey(50)
console.log("a=" + a.charCodeAt(0) + " blen=" + b.length)
`, "A")
	if got != "a=65 blen=0" {
		t.Fatalf("got %q, want %q", got, "a=65 blen=0")
	}
}
