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
const c: number = process.stdout.columns
const r: number = process.stdout.rows
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
