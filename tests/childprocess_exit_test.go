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
// inherited stdout/stderr open for two more seconds. Node reports 'exit' (7)
// immediately and 'close' (7) only when the grandchild lets go of the pipes.
func TestE2EChildProcessExitFiresBeforeStdioCloses(t *testing.T) {
	needShAndSleep(t)
	bin := buildBinaryImports(t, `
import { spawn } from 'node:child_process'
const t0 = Date.now()
const c = spawn('sh', ['-c', 'sleep 2 & exit 7'])
c.on('exit', (code: number) => {
  console.log('exit ' + code + (Date.now() - t0 < 1200 ? ' early' : ' LATE'))
})
c.on('close', (code: number) => {
  console.log('close ' + code + (Date.now() - t0 >= 1500 ? ' after-stdio' : ' TOO-SOON'))
})
`)
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	got := strings.ReplaceAll(strings.TrimSpace(string(out)), "\r\n", "\n")
	if want := "exit 7 early\nclose 7 after-stdio"; got != want {
		t.Errorf("exit/close sequence:\n got %q\nwant %q", got, want)
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
