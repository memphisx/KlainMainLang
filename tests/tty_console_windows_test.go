package tests

import (
	"strings"
	"testing"
	"time"
)

// tty_console_windows_test.go — the console-mode branches of klain:tty and
// klain:tui, exercised under a pseudo-console (conpty_windows_test.go) so
// they are verified by the suite, not only by hand at a real console
// (ADR-00727). Each program is the same one a user would run in a terminal;
// the harness stands in for the terminal.

// A console reports its real size: the pseudo-console is created 100×30 and
// process.stdout.columns/rows read it through GetConsoleScreenBufferInfo (the
// piped run of the same program falls back to 80x24, TestE2ETtyWinSizeFallback).
func TestE2ETtyConsoleSize(t *testing.T) {
	bin := buildBinary(t, `
const c: number = process.stdout.columns!
const r: number = process.stdout.rows!
console.log("size=" + c + "x" + r + " tty=" + process.stdout.isTTY)
`)
	res := runInConPTY(t, bin, 100, 30, 20*time.Second, nil)
	if !strings.Contains(res.Output, "size=100x30 tty=true") {
		t.Fatalf("console size/isTTY not reported; output:\n%q", res.Output)
	}
}

// Raw mode at a console: line input and echo are off, so a single key press
// reaches readKey without Enter, and with ENABLE_VIRTUAL_TERMINAL_INPUT an
// arrow key arrives as the same ESC [ A bytes a POSIX terminal sends.
func TestE2ETtyConsoleRawReadKey(t *testing.T) {
	bin := buildBinaryImports(t, `
import { readKey } from 'klain:tty'
process.stdin.setRawMode(true)
const a: string = readKey()
const b: string = readKey()
process.stdin.setRawMode(false)
let codes = ""
for (let i = 0; i < b.length; i++) { codes = codes + b.charCodeAt(i) + " " }
console.log("first=" + a.charCodeAt(0) + " second=" + codes.trim())
`)
	res := runInConPTY(t, bin, 80, 24, 20*time.Second, func(write func(string)) {
		time.Sleep(700 * time.Millisecond)
		write("q")
		time.Sleep(300 * time.Millisecond)
		write("\x1b[A")
	})
	if !strings.Contains(res.Output, "first=113 second=27 91 65") {
		t.Fatalf("raw-mode keys not delivered as expected; output:\n%q", res.Output)
	}
}

// readKey(ms) at a console polls the input queue: nothing pressed within the
// timeout yields "" (the redraw tick of a self-refreshing TUI loop), and a
// key pressed later is returned by the next call.
func TestE2ETtyConsoleReadKeyTimeout(t *testing.T) {
	bin := buildBinaryImports(t, `
import { readKey } from 'klain:tty'
process.stdin.setRawMode(true)
const a: string = readKey(200)
const b: string = readKey(5000)
process.stdin.setRawMode(false)
console.log("alen=" + a.length + " b=" + b)
`)
	res := runInConPTY(t, bin, 80, 24, 20*time.Second, func(write func(string)) {
		time.Sleep(1500 * time.Millisecond)
		write("z")
	})
	if !strings.Contains(res.Output, "alen=0 b=z") {
		t.Fatalf("readKey timeout semantics off; output:\n%q", res.Output)
	}
}

// process.stdin as a flowing Readable over a REAL console (not a pipe): the
// IOCP reactor's console reader thread (TDD-00183 Stage 1) does ReadConsoleW
// in cooked mode and decodes UTF-16→UTF-8, so a typed line — including its
// non-ASCII characters — is delivered to the 'data' listener intact, and the
// event loop is never blocked waiting for it. The piped E2E stdin tests can't
// reach this path (a pipe fd is KFD_PIPE, never KFD_CONSOLE); only a
// pseudo-console exercises the console reader. Fixes TDD-00180 §2 (non-ASCII
// console input via the CRT code page) in the same move.
func TestE2EStdinConsoleReaderUTF8(t *testing.T) {
	bin := buildBinary(t, `
process.stdin.on('data', (chunk: string) => {
  const s = chunk.trim()
  if (s.length > 0) console.log("got[" + s + "]")
})
process.stdin.on('end', () => { console.log("end") })
`)
	res := runInConPTY(t, bin, 80, 24, 20*time.Second, func(write func(string)) {
		time.Sleep(700 * time.Millisecond)
		write("café\r") // "café" + Enter — the é must survive as one char
		time.Sleep(300 * time.Millisecond)
		write("\x1a\r") // Ctrl+Z at line start = console EOF, so the loop ends
	})
	// The é must survive as UTF-8 (cooked ReadConsoleW → UTF-16 → UTF-8), and
	// EOF must arrive so the flowing stream ends and the program exits.
	if !strings.Contains(res.Output, "got[café]") {
		t.Fatalf("console stdin did not deliver a UTF-8 line intact; output:\n%q", res.Output)
	}
	if !strings.Contains(res.Output, "end") {
		t.Fatalf("console stdin EOF (Ctrl+Z) did not end the stream; output:\n%q", res.Output)
	}
}

// A klain:tui program at a console: the frame is laid out to the console's
// width, painted on the alternate screen with VT processing enabled, and the
// update loop reacts to keys — an arrow moves the selection, 'q' quits and
// the program leaves the alternate screen and prints its farewell.
func TestE2ETuiConsoleLoop(t *testing.T) {
	bin := buildBinaryImports(t, `
import { Box, Text, List, render, enter, leave } from 'klain:tui'
import { readKey } from 'klain:tty'
const items = ['alpha', 'beta', 'gamma']
let sel = 0
enter()
process.stdin.setRawMode(true)
let running = true
while (running) {
  render(Box({ flexDirection: 'column', border: 'round', width: process.stdout.columns }, [
    Text('Pick one (' + process.stdout.columns + ' cols)', { bold: true }),
    List(items, { selected: sel }),
  ]))
  const k: string = readKey(2000)
  if (k === 'q') { running = false }
  else if (k === '\x1b[B') { sel = (sel + 1) % items.length }
}
process.stdin.setRawMode(false)
leave()
console.log("chose " + items[sel])
`)
	res := runInConPTY(t, bin, 60, 20, 30*time.Second, func(write func(string)) {
		time.Sleep(900 * time.Millisecond)
		write("\x1b[B")
		time.Sleep(400 * time.Millisecond)
		write("\x1b[B")
		time.Sleep(400 * time.Millisecond)
		write("q")
	})
	for _, want := range []string{"Pick one (60 cols)", "gamma", "chose gamma"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("console TUI run missing %q; output:\n%q", want, res.Output)
		}
	}
	if res.ExitCode != 0 {
		t.Errorf("exit code %d", res.ExitCode)
	}
}
