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
	// close() fires 'close' immediately, before EOF.
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
	if got != "got keep\nclosed after 1" {
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
