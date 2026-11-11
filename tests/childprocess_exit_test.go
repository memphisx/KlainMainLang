package tests

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// childprocess_exit_test.go — a child's end is two events, as in Node
// (TDD-00223 §5): 'exit' when the process ends, whatever its stdio is doing,
// and 'close' once the process has ended AND its stdio streams have closed.
// The loop learns of the exit from a wake (SIGCHLD self-pipe / a registered
// wait on the process handle), never by polling.

func needShAndSleep(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"sh", "sleep"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s on PATH", tool)
		}
	}
}

// The shell exits at once with status 7 while a background grandchild keeps the
// inherited stdout/stderr open until it is killed. Node reports 'exit' (7)
// immediately and 'close' (7) only when the grandchild lets go of the pipes.
func TestE2EChildProcessExitFiresBeforeStdioCloses(t *testing.T) {
	needShAndSleep(t)
	bin := buildBinaryImports(t, `
import { spawn } from 'node:child_process'
// The grandchild holds the pipes until the 'exit' handler kills it: 'close'
// can only come after that, and an 'exit' held back by the pipes never comes.
let pid = 0
let exited = false
const release = () => { if (pid !== 0 && exited) process.kill(pid) }
const c = spawn('sh', ['-c', 'sleep 60 & echo $!; exit 7'])
c.stdout.on('data', (d: any) => { pid = parseInt(String(d)); release() })
c.on('exit', (code: number) => {
  console.log('exit ' + code)
  exited = true
  release()
})
c.on('close', (code: number) => {
  console.log('close ' + code + (exited ? ' after-stdio' : ' TOO-SOON'))
})
`)
	start := time.Now()
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	got := strings.ReplaceAll(strings.TrimSpace(string(out)), "\r\n", "\n")
	if want := "exit 7\nclose 7 after-stdio"; got != want {
		t.Errorf("exit/close sequence:\n got %q\nwant %q", got, want)
	}
	// Only the grandchild's own 60 s end would release an 'exit' the pipes
	// held back.
	if wall := time.Since(start); wall > 45*time.Second {
		t.Errorf("'exit' waited for the grandchild (%v)", wall)
	}
}

// A child that closes its stdio and keeps running: the parent has nothing to
// read and nothing to reap for two seconds. It must spend them asleep. The
// reap used to be polled through a zero-timeout select() — a full core for as
// long as the child lingered — so the bound is on the parent's own CPU time.
func TestE2EChildProcessLingeringChildDoesNotSpin(t *testing.T) {
	needShAndSleep(t)
	bin := buildBinaryImports(t, `
import { spawn } from 'node:child_process'
const c = spawn('sh', ['-c', 'exec 1>&- 2>&-; sleep 2'])
c.on('exit', (code: number) => { console.log('exit ' + code) })
c.on('close', (code: number) => { console.log('close ' + code) })
`)
	cmd := exec.Command(bin)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	got := strings.ReplaceAll(strings.TrimSpace(string(out)), "\r\n", "\n")
	if want := "exit 0\nclose 0"; got != want {
		t.Errorf("events:\n got %q\nwant %q", got, want)
	}
	if wall := time.Since(start); wall < 1500*time.Millisecond {
		t.Errorf("parent finished in %v — it did not wait for the child", wall)
	}
	cpu := cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()
	if cpu > 500*time.Millisecond {
		t.Errorf("parent burned %v of CPU waiting ~2 s for a silent child; the exit is being polled, not awaited", cpu)
	}
}
