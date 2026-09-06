package tests

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// signals_windows_test.go — the Windows variants of signals_test.go's
// delivery tests (ADR-00728). Go's os.Process.Signal cannot send SIGINT or
// SIGTERM on Windows, so the POSIX tests skip there; but the way a Windows
// user actually raises SIGINT is Ctrl+C at the console, which Node maps to
// process.on('SIGINT') through a console control handler — and the
// pseudo-console harness (conpty_windows_test.go) can press Ctrl+C. SIGTERM
// has no console counterpart: Node accepts the listener and never fires it,
// and process.kill(pid, 'SIGTERM') is an unconditional terminate — the last
// test below asserts exactly that, matching `node` on this machine.

// ctrlCWhenReady returns a feed that presses Ctrl+C once readyPath exists —
// the same readiness contract startAndWaitForFile uses on POSIX.
func ctrlCWhenReady(t *testing.T, readyPath string) func(write func(string)) {
	return func(write func(string)) {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(readyPath); err == nil {
				// The ready file is written right after the listener is
				// registered; give the loop a moment to enter its wait.
				time.Sleep(300 * time.Millisecond)
				pressCtrlC(write)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Errorf("program never signalled readiness at %s", readyPath)
	}
}

func TestE2ESignalSigintGracefulShutdownConsole(t *testing.T) {
	readyPath := filepath.Join(tempDir(t), "ready")
	bin := buildBinaryImports(t, fmt.Sprintf(`
import http from 'klain:http'
import fs from 'fs'
process.on('SIGINT', () => {
  console.log("handled:SIGINT");
  process.exit(0);
});
fs.writeFileSync(%q, "ready");
http.listen(8241, (req: HttpRequest): { status: number; body: string } => {
  return { status: 200, body: "ok" };
});
`, readyPath))
	res := runInConPTY(t, bin, 80, 24, 30*time.Second, ctrlCWhenReady(t, readyPath))
	if res.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0; output:\n%q", res.ExitCode, res.Output)
	}
	if !strings.Contains(res.Output, "handled:SIGINT") {
		t.Errorf("handler never ran; output:\n%q", res.Output)
	}
}

// Ctrl+C is SIGINT only: a SIGTERM listener registered alongside must not run.
func TestE2ESignalBothRegisteredIndependentlyConsole(t *testing.T) {
	readyPath := filepath.Join(tempDir(t), "ready")
	bin := buildBinaryImports(t, fmt.Sprintf(`
import http from 'klain:http'
import fs from 'fs'
process.on('SIGINT', () => {
  console.log("handled:SIGINT");
  process.exit(0);
});
process.on('SIGTERM', () => {
  console.log("handled:SIGTERM");
  process.exit(0);
});
fs.writeFileSync(%q, "ready");
http.listen(8242, (req: HttpRequest): { status: number; body: string } => {
  return { status: 200, body: "ok" };
});
`, readyPath))
	res := runInConPTY(t, bin, 80, 24, 30*time.Second, ctrlCWhenReady(t, readyPath))
	if !strings.Contains(res.Output, "handled:SIGINT") {
		t.Errorf("SIGINT handler never ran; output:\n%q", res.Output)
	}
	if strings.Contains(res.Output, "handled:SIGTERM") {
		t.Errorf("SIGTERM handler ran on Ctrl+C; output:\n%q", res.Output)
	}
}

// With no handler registered, Ctrl+C keeps the console's default action:
// the process is terminated (STATUS_CONTROL_C_EXIT) without running any
// program code.
func TestE2ESignalNoHandlerDefaultDispositionConsole(t *testing.T) {
	readyPath := filepath.Join(tempDir(t), "ready")
	bin := buildBinaryImports(t, fmt.Sprintf(`
import http from 'klain:http'
import fs from 'fs'
fs.writeFileSync(%q, "ready");
http.listen(8243, (req: HttpRequest): { status: number; body: string } => {
  return { status: 200, body: "ok" };
});
`, readyPath))
	res := runInConPTY(t, bin, 80, 24, 30*time.Second, ctrlCWhenReady(t, readyPath))
	if res.ExitCode == 0 {
		t.Errorf("process exited 0 after Ctrl+C with no handler; output:\n%q", res.Output)
	}
	if res.ExitCode == 124 {
		t.Errorf("process ignored Ctrl+C with no handler registered (killed by the harness); output:\n%q", res.Output)
	}
}

// A setInterval-only program (the __kml_timer_drain loop, not the fd loop)
// also honours the handler, and a 100-second interval does not fire just
// because the wait was interrupted.
func TestE2ESignalSetIntervalOnlyGracefulShutdownConsole(t *testing.T) {
	readyPath := filepath.Join(tempDir(t), "ready")
	bin := buildBinaryImports(t, fmt.Sprintf(`
import fs from 'fs'
process.on('SIGINT', () => {
  console.log("handled:SIGINT");
  process.exit(0);
});
fs.writeFileSync(%q, "ready");
setInterval(() => { console.log("tick"); }, 100000);
`, readyPath))
	res := runInConPTY(t, bin, 80, 24, 30*time.Second, ctrlCWhenReady(t, readyPath))
	if res.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0; output:\n%q", res.ExitCode, res.Output)
	}
	if !strings.Contains(res.Output, "handled:SIGINT") {
		t.Errorf("handler never ran; output:\n%q", res.Output)
	}
	if strings.Contains(res.Output, "tick") {
		t.Errorf("100s setInterval fired prematurely after Ctrl+C; output:\n%q", res.Output)
	}
}

// process.kill(pid, 'SIGTERM') on Windows terminates unconditionally, as in
// Node (libuv's uv_kill is TerminateProcess): the SIGTERM listener is
// accepted, never runs, nothing after the call executes, exit code 1.
func TestE2ESignalSigtermIsTerminateOnWindows(t *testing.T) {
	bin := buildBinary(t, `
process.on('SIGTERM', () => { console.log("handled:SIGTERM") })
console.log("before")
process.kill(process.pid, 'SIGTERM')
console.log("after")
`)
	out, err := exec.Command(bin).CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (TerminateProcess); output:\n%s", code, out)
	}
	if strings.Contains(string(out), "handled:SIGTERM") || strings.Contains(string(out), "after") {
		t.Errorf("SIGTERM must terminate without running the handler or the rest of the program; output:\n%s", out)
	}
}

// win32InputCtrlBreak is Ctrl+Break in the console's win32-input-mode
// encoding (VK_CANCEL = 3, scan code 70, no character, LEFT_CTRL_PRESSED).
const win32InputCtrlBreak = "\x1b[3;70;0;1;8;1_\x1b[3;70;0;0;8;1_"

// Ctrl+Break is Node's 'SIGBREAK' on Windows (libuv maps CTRL_BREAK_EVENT to
// it, distinct from SIGINT); a SIGINT listener registered alongside must not
// run for it.
func TestE2ESignalSigbreakConsole(t *testing.T) {
	readyPath := filepath.Join(tempDir(t), "ready")
	bin := buildBinaryImports(t, fmt.Sprintf(`
import fs from 'fs'
process.on('SIGINT', () => {
  console.log("handled:SIGINT");
  process.exit(0);
});
process.on('SIGBREAK', () => {
  console.log("handled:SIGBREAK");
  process.exit(0);
});
fs.writeFileSync(%q, "ready");
setInterval(() => { console.log("tick"); }, 100000);
`, readyPath))
	res := runInConPTY(t, bin, 80, 24, 30*time.Second, func(write func(string)) {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(readyPath); err == nil {
				time.Sleep(300 * time.Millisecond)
				write(win32InputCtrlBreak)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
	if res.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0; output:\n%q", res.ExitCode, res.Output)
	}
	if !strings.Contains(res.Output, "handled:SIGBREAK") {
		t.Errorf("SIGBREAK handler never ran; output:\n%q", res.Output)
	}
	if strings.Contains(res.Output, "handled:SIGINT") {
		t.Errorf("SIGINT handler ran on Ctrl+Break; output:\n%q", res.Output)
	}
}
