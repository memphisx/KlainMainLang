package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// --- Node interactive `readline` (ADR-00323) ---
//
// createInterface over stdin, the 'line' event, question(query, cb), close()
// and the 'close' event. Non-blocking stdin folds into the same select() loop
// as child_process's pipes.

// runImportsStdin builds an import-using program and feeds it stdin.
func runImportsStdin(t *testing.T, src, stdin string) string {
	t.Helper()
	bin := buildBinaryImports(t, src)
	cmd := exec.Command(bin)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.TrimRight(string(out), "\n")
}

func TestE2EReadlineLineEvents(t *testing.T) {
	got := runImportsStdin(t, `
import readline from 'readline'
const rl = readline.createInterface({ input: process.stdin })
let n = 0
rl.on('line', (line: string) => { n = n + 1; console.log(n + ": " + line) })
rl.on('close', () => { console.log("total " + n) })
`, "alpha\nbeta\ngamma\n")
	want := "1: alpha\n2: beta\n3: gamma\ntotal 3"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestE2EReadlineCloseOnEOF(t *testing.T) {
	got := runImportsStdin(t, `
import readline from 'readline'
const rl = readline.createInterface({ input: process.stdin })
rl.on('line', (l: string) => { console.log("line") })
rl.on('close', () => { console.log("closed") })
`, "one\ntwo\n")
	if got != "line\nline\nclosed" {
		t.Fatalf("got: %q", got)
	}
}

func TestE2EReadlineQuestion(t *testing.T) {
	// question writes the prompt to stdout and routes the next line to its
	// one-shot callback (nested here to chain two prompts).
	got := runImportsStdin(t, `
import readline from 'readline'
const rl = readline.createInterface({ input: process.stdin, output: process.stdout })
rl.question("Q1: ", (a: string) => {
  console.log("A1=" + a)
  rl.question("Q2: ", (b: string) => { console.log("A2=" + b); rl.close() })
})
`, "first\nsecond\n")
	// Prompts are written before the answers (piped stdin arrives at once).
	if !strings.Contains(got, "A1=first") || !strings.Contains(got, "A2=second") {
		t.Fatalf("got: %q", got)
	}
}

func TestE2EReadlineFlushesUnterminatedFinalLine(t *testing.T) {
	// Input with no trailing newline still emits its last line on EOF.
	got := runImportsStdin(t, `
import readline from 'readline'
const rl = readline.createInterface({ input: process.stdin })
rl.on('line', (l: string) => { console.log("[" + l + "]") })
`, "x\ny\nno_newline")
	if got != "[x]\n[y]\n[no_newline]" {
		t.Fatalf("got: %q", got)
	}
}

func TestE2EReadlineStripsCarriageReturn(t *testing.T) {
	got := runImportsStdin(t, `
import readline from 'readline'
const rl = readline.createInterface({ input: process.stdin })
rl.on('line', (l: string) => { console.log("len=" + l.length) })
`, "abc\r\n")
	if got != "len=3" {
		t.Fatalf("got: %q (CR should be stripped)", got)
	}
}

func TestE2EReadlineExplicitClose(t *testing.T) {
	// close() fires 'close' immediately, before EOF; the lines already read
	// in the same chunk are still emitted, as in Node.
	got := runImportsStdin(t, `
import readline from 'readline'
const rl = readline.createInterface({ input: process.stdin })
let n = 0
rl.on('line', (l: string) => {
  n = n + 1
  console.log("got " + l)
  if (n === 1) { rl.close() }
})
rl.on('close', () => { console.log("closed after " + n) })
`, "keep\ndrop\ndrop\n")
	if got != "got keep\nclosed after 1\ngot drop\ngot drop" {
		t.Fatalf("got: %q", got)
	}
}

func TestE2EReadlineMissingImportRejected(t *testing.T) {
	err := resolveAndEmitMultiFile(t, map[string]string{
		"main.ts": `
const rl = readline.createInterface({ input: process.stdin })
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for using readline without importing it, got none")
	}
}

// readline over any Readable: a file stream, line by line with for await
// (crlfDelay: Infinity treats \r\n as one line ending), and readline/promises'
// question.
func TestE2EReadlineFileStreamAndPromises(t *testing.T) {
	got := runImportsStdin(t, `
import * as fs from 'fs'
import * as os from 'os'
import * as path from 'path'
import * as readline from 'readline'
import { createInterface } from 'readline/promises'
async function main() {
  const f = path.join(os.tmpdir(), 'kml_rl_' + process.pid + '.txt')
  fs.writeFileSync(f, 'one\ntwo\r\nthree')
  const rl = readline.createInterface({ input: fs.createReadStream(f), crlfDelay: Infinity })
  for await (const line of rl) console.log('file', JSON.stringify(line))
  fs.unlinkSync(f)
  const q = createInterface({ input: process.stdin })
  const a = await q.question('name? ')
  console.log('answer', a)
  q.close()
}
main()
`, "bob\n")
	if got != "file \"one\"\nfile \"two\"\nfile \"three\"\nanswer bob" {
		t.Fatalf("got: %q", got)
	}
}

// A terminal-mode Interface decodes keys (arrows, Ctrl-A/E, backspace),
// edits the line, keeps history and tab-completes.
func TestE2EReadlineTerminalEditing(t *testing.T) {
	assertOutputImports(t, `
import * as readline from 'readline'
import { PassThrough } from 'stream'
const input = new PassThrough()
const output = new PassThrough()
const rl = readline.createInterface({ input, output, terminal: true, completer: (line: string) => [['alpha', 'beta'].filter((c) => c.startsWith(line)), line] })
rl.on('line', (l: string) => console.log('line', JSON.stringify(l), rl.history.length))
input.write('abc\x1b[D\x1b[DX\r')
input.write('\x1b[A\r')
input.write('al\t\r')
input.write('xy\x7f\x01Z\x05!\r')
setTimeout(() => rl.close(), 10)
`, "line \"aXbc\" 1\nline \"aXbc\" 1\nline \"al\\t\" 2\nline \"Zx!\" 3")
}
