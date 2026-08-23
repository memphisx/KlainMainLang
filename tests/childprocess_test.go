package tests

import "testing"

// --- Node `child_process`: async spawn/exec/execFile (ADR-00322) ---
//
// spawn (streaming EventEmitter: stdout/stderr 'data'/'end', 'close'/'exit'),
// exec (shell, buffered (err, stdout, stderr)), execFile (no shell, buffered).
// The child pipes fold into the same select() event loop as Worker/channel
// message pipes; callbacks fire after the top-level code, like Node.

func TestE2EChildProcessSpawnStreaming(t *testing.T) {
	assertOutputImports(t, `
import { spawn } from 'child_process'
const dec = new TextDecoder()
const child = spawn("printf", ["a\nb\nc\n"])
let acc = ""
child.stdout.on('data', (chunk: Uint8Array) => { acc = acc + dec.decode(chunk) })
child.on('close', (code: number) => {
  console.log("code:", code)
  console.log("lines:", acc.split("\n").length - 1)
})
`, "code: 0\nlines: 3")
}

func TestE2EChildProcessExecShell(t *testing.T) {
	assertOutputImports(t, `
import { exec } from 'child_process'
exec("echo hello from shell", (err, stdout, stderr) => {
  console.log("err null:", err === null)
  console.log("out:", stdout.trim())
})
`, "err null: true\nout: hello from shell")
}

func TestE2EChildProcessExecFileNoShell(t *testing.T) {
	assertOutputImports(t, `
import { execFile } from 'child_process'
execFile("echo", ["one", "two"], (err, stdout, stderr) => {
  console.log(stdout.trim())
})
`, "one two")
}

func TestE2EChildProcessExecNonZeroExitErrors(t *testing.T) {
	assertOutputImports(t, `
import { exec } from 'child_process'
exec("exit 3", (err, stdout, stderr) => {
  console.log("err set:", err !== null)
})
`, "err set: true")
}

func TestE2EChildProcessExecCapturesStderr(t *testing.T) {
	assertOutputImports(t, `
import { exec } from 'child_process'
exec("echo boom 1>&2", (err, stdout, stderr) => {
  console.log("stderr:", stderr.trim())
  console.log("stdout empty:", stdout.trim().length === 0)
})
`, "stderr: boom\nstdout empty: true")
}

func TestE2EChildProcessStdinWrite(t *testing.T) {
	// Pipe input into `cat`, which echoes it back on stdout.
	assertOutputImports(t, `
import { spawn } from 'child_process'
const dec = new TextDecoder()
const cat = spawn("cat", [])
let out = ""
cat.stdout.on('data', (c: Uint8Array) => { out = out + dec.decode(c) })
cat.on('close', (code: number) => { console.log(out.trim()) })
cat.stdin.write("piped input")
cat.stdin.end()
`, "piped input")
}

func TestE2EChildProcessSpawnExitCode(t *testing.T) {
	assertOutputImports(t, `
import { spawn } from 'child_process'
const child = spawn("sh", ["-c", "exit 7"])
child.on('exit', (code: number) => { console.log("exit:", code) })
`, "exit: 7")
}

func TestE2EChildProcessStreamEnd(t *testing.T) {
	assertOutputImports(t, `
import { spawn } from 'child_process'
const child = spawn("printf", ["x"])
child.stdout.on('end', () => { console.log("stdout ended") })
child.on('close', (code: number) => { console.log("closed:", code) })
`, "stdout ended\nclosed: 0")
}

func TestE2EChildProcessCallbacksAreAsync(t *testing.T) {
	// The exec callback must fire AFTER the synchronous top-level line, the
	// way Node defers it to the event loop.
	assertOutputImports(t, `
import { exec } from 'child_process'
exec("echo second", (err, stdout, stderr) => { console.log(stdout.trim()) })
console.log("first")
`, "first\nsecond")
}

func TestE2EChildProcessLargeOutput(t *testing.T) {
	// Output larger than one 4096-byte read chunk must be fully captured.
	assertOutputImports(t, `
import { exec } from 'child_process'
exec("for i in $(seq 1 2000); do echo line$i; done", (err, stdout, stderr) => {
  console.log("lines:", stdout.split("\n").length - 1)
})
`, "lines: 2000")
}

func TestE2EChildProcessMissingImportRejected(t *testing.T) {
	err := resolveAndEmitMultiFile(t, map[string]string{
		"main.ts": `
const c = spawn("echo", ["hi"])
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for using spawn without importing 'child_process', got none")
	}
}
