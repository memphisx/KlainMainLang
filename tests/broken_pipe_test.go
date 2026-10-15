package tests

import (
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A write to a child's stdin after the child has exited fails with Node's
// EPIPE on 'error' (TDD-00180 §5: a broken anonymous pipe once surfaced as
// EBADF on Windows).
func TestE2EChildStdinBrokenPipeSameAsNode(t *testing.T) {
	src := `
import { spawn } from 'child_process'
const child = process.platform === 'win32'
  ? spawn('cmd', ['/c', 'ping -n 2 127.0.0.1 >nul'])
  : spawn('sleep', ['1'])
let reported = false
child.stdin.on('error', (e: any) => {
  if (!reported) console.log('stdin error', e.code, e.syscall)
  reported = true
})
const big = 'x'.repeat(65536)
let n = 0
const t = setInterval(() => {
  child.stdin.write(big)
  if (++n === 60) { clearInterval(t); console.log('done writing') }
}, 50)
`
	theirs := runNodeTS(t, src)
	ours := compileAndRunImports(t, src)
	if ours != theirs || !strings.Contains(ours, "stdin error EPIPE write") {
		t.Fatalf("output differs from node:\n--- ours ---\n%s\n--- node ---\n%s", ours, theirs)
	}
}

// Writing stdout into a reader that has gone fails with EPIPE on
// process.stdout's 'error', as in Node.
func TestE2EStdoutBrokenPipeSameAsNode(t *testing.T) {
	src := `
process.stdout.on('error', (e: any) => {
  process.stderr.write('stdout error ' + e.code + ' ' + e.syscall + '\n')
  process.exit(3)
})
const line = 'y'.repeat(1023) + '\n'
let n = 0
const t = setInterval(() => {
  for (let i = 0; i < 64; i++) process.stdout.write(line)
  if (++n === 100) { clearInterval(t); process.stderr.write('no error\n') }
}, 10)
`
	// run starts cmd, reads a few bytes of its stdout, closes the pipe and
	// reports what it wrote to stderr and its exit code.
	run := func(cmd *exec.Cmd) (string, int) {
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 10)
		io.ReadFull(out, buf)
		out.Close()
		code := 0
		if err := cmd.Wait(); err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		return strings.TrimSpace(stderr.String()), code
	}
	bin := buildBinaryImports(t, src)
	ours, oursCode := run(exec.Command(bin))
	file := filepath.Join(tempDir(t), "prog.ts")
	writeFile(t, file, src)
	theirs, theirsCode := run(exec.Command(nodeStripTypesBin(t), "--experimental-strip-types", "--no-warnings", file))
	if ours != theirs || oursCode != theirsCode || ours != "stdout error EPIPE write" {
		t.Fatalf("differs from node:\nours:  %q exit %d\nnode:  %q exit %d", ours, oursCode, theirs, theirsCode)
	}
}
