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

func TestE2EChildProcessSpawnSync(t *testing.T) {
	// spawnSync blocks and returns { status, stdout, stderr, pid }.
	assertOutputImports(t, `
import { spawnSync } from 'child_process'
const r = spawnSync("echo", ["hello", "world"])
console.log("status", r.status)
console.log("stdout", r.stdout.trim())
const bad = spawnSync("false")
console.log("badstatus", bad.status)
`, "status 0\nstdout hello world\nbadstatus 1")
}

func TestE2EChildProcessExecSyncAndExecFileSync(t *testing.T) {
	// execSync runs through /bin/sh -c; execFileSync execvp's with no shell.
	// Both return captured stdout as a string.
	assertOutputImports(t, `
import { execSync, execFileSync } from 'child_process'
console.log("exec", execSync("echo shell-$((2+3))").trim())
console.log("execfile", execFileSync("printf", ["%s-%s", "a", "b"]))
`, "exec shell-5\nexecfile a-b")
}

func TestE2EChildProcessSpawnSyncLargeInterleavedOutput(t *testing.T) {
	// Both pipes are poll-multiplexed: a child writing well past the pipe
	// buffer on stdout AND stderr must not deadlock, and both captures must be
	// complete.
	assertOutputImports(t, `
import { spawnSync } from 'child_process'
const r = spawnSync("/bin/sh", ["-c", "for i in $(seq 1 3000); do echo out$i; echo err$i 1>&2; done"])
console.log("outlines:", r.stdout.split("\n").length - 1)
console.log("errlines:", r.stderr.split("\n").length - 1)
`, "outlines: 3000\nerrlines: 3000")
}

func TestE2EChildProcessSyncOptionsCwdEncoding(t *testing.T) {
	// { cwd } chdir's the child before exec; encoding: 'utf8' is accepted
	// (results are already strings); other options are clean rejections.
	assertOutputImports(t, `
import { spawnSync, execSync } from 'child_process'
console.log("a", spawnSync("pwd", [], { cwd: "/" }).stdout.trim())
console.log("b", execSync("pwd", { cwd: "/", encoding: "utf8" }).trim())
console.log("c", spawnSync("pwd", { cwd: "/" }).stdout.trim())
`, "a /\nb /\nc /")
}
