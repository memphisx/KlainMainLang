package tests

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// syncBuffer is a bytes.Buffer safe for the "one goroutine writes while the
// process runs, main goroutine reads after/concurrently" pattern these
// tests need — a plain bytes.Buffer isn't safe for that.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startAndWaitForFile starts binFile (which must fs.writeFileSync(readyPath,
// ...) as its very first action, right after any process.on(...) calls),
// capturing its combined stdout+stderr into a syncBuffer, and blocks until
// readyPath exists on disk — the file-based equivalent of
// startHTTPServer's/startBackgroundServer's port-dial polling loop, for a
// program with no TCP port to poll instead (e.g. a plain setInterval-only
// program). A stdout marker line would not work here: console.log goes
// through fully-buffered libc stdio when stdout is a pipe (not a TTY,
// confirmed directly — a line written that way sits in the buffer and is
// never visible to a reader until the process exits or the buffer fills),
// whereas fs.writeFileSync's fopen/fwrite/fclose (runtime_fs.go) closes the
// file immediately, guaranteeing the write is visible to another process
// polling for it. Without some real readiness signal, sending a signal
// immediately after Start() races the process's own startup —
// process.on('SIGINT', ...) hasn't necessarily run (and installed the real
// OS handler) yet, so the signal could still hit the OS's default
// disposition instead.
func startAndWaitForFile(t *testing.T, binFile, readyPath string) (*exec.Cmd, *syncBuffer) {
	t.Helper()
	cmd := exec.Command(binFile)
	out := &syncBuffer{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(readyPath); err == nil {
			return cmd, out
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never saw readiness file %s; output so far:\n%s", readyPath, out.String())
	return nil, nil
}

// startBackgroundServer compiles src (expected to call http.listen and never
// return) and runs it as a background process with its stdout captured into
// the returned buffer, waiting for the given port to accept TCP connections
// before returning. Unlike startHTTPServer (http_test.go), the caller is
// responsible for terminating the process — this is used by tests that send
// a real signal and assert on how the process reacts, not just that it can
// be forcibly killed during cleanup.
func startBackgroundServer(t *testing.T, src string, port int) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	binFile := buildBinary(t, src)
	cmd := exec.Command(binFile)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	deadline := time.Now().Add(5 * time.Second)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return cmd, &out
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server never started listening on %s", addr)
	return nil, nil
}

// waitExit waits for cmd to exit on its own within timeout, returning the
// process's exit code (0 on a clean exit) and its final stdout+stderr. Fails
// the test if the process is still running once timeout elapses — signal
// delivery/handling not working would otherwise just hang the test.
func waitExit(t *testing.T, cmd *exec.Cmd, out fmt.Stringer, timeout time.Duration) int {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err == nil {
			return 0
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		t.Fatalf("wait: %v", err)
		return -1
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		t.Fatalf("process did not exit within %s after signal; captured output:\n%s", timeout, out.String())
		return -1
	}
}

// --- process.on('SIGINT'/'SIGTERM', handler) — TDD-00019 ---
//
// A registered handler must fire from ordinary control flow inside the
// event loop (never from real signal context — see
// ensureSignalHandlerRuntime's doc comment, runtime_process.go), so these
// tests assert on an actual side effect the handler performs (a printed
// marker), not just that the process eventually dies — that would pass
// even if the OS's own default signal disposition killed it instead of the
// registered handler ever running.

func TestE2ESignalSigintGracefulShutdown(t *testing.T) {
	src := `
process.on('SIGINT', () => {
  console.log("handled:SIGINT");
  process.exit(0);
});
http.listen(8231, (req: HttpRequest): { status: number; body: string } => {
  return { status: 200, body: "ok" };
});
`
	cmd, out := startBackgroundServer(t, src, 8231)
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	code := waitExit(t, cmd, out, 5*time.Second)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; output:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "handled:SIGINT") {
		t.Errorf("handler never ran; output:\n%s", out.String())
	}
}

func TestE2ESignalSigtermGracefulShutdown(t *testing.T) {
	src := `
process.on('SIGTERM', () => {
  console.log("handled:SIGTERM");
  process.exit(0);
});
http.listen(8232, (req: HttpRequest): { status: number; body: string } => {
  return { status: 200, body: "ok" };
});
`
	cmd, out := startBackgroundServer(t, src, 8232)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	code := waitExit(t, cmd, out, 5*time.Second)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; output:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "handled:SIGTERM") {
		t.Errorf("handler never ran; output:\n%s", out.String())
	}
}

// TestE2ESignalBothRegisteredIndependently confirms registering a handler
// for one signal doesn't accidentally also intercept the other — SIGTERM
// still reaches its own, distinct handler when both are registered.
func TestE2ESignalBothRegisteredIndependently(t *testing.T) {
	src := `
process.on('SIGINT', () => {
  console.log("handled:SIGINT");
  process.exit(0);
});
process.on('SIGTERM', () => {
  console.log("handled:SIGTERM");
  process.exit(0);
});
http.listen(8233, (req: HttpRequest): { status: number; body: string } => {
  return { status: 200, body: "ok" };
});
`
	cmd, out := startBackgroundServer(t, src, 8233)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	code := waitExit(t, cmd, out, 5*time.Second)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; output:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "handled:SIGTERM") {
		t.Errorf("SIGTERM handler never ran; output:\n%s", out.String())
	}
	if strings.Contains(out.String(), "handled:SIGINT") {
		t.Errorf("SIGINT handler ran when only SIGTERM was sent; output:\n%s", out.String())
	}
}

// TestE2ESignalNoHandlerDefaultDisposition confirms a program that never
// calls process.on for a given signal leaves that signal's OS-level
// disposition untouched (SIG_DFL) — this change must not alter existing
// behavior for programs that don't use the new feature at all. No
// call ptr @signal(...) should ever be emitted without a matching
// process.on call (verified directly via --emit-llvm's output, not just
// inferred from this test); here we confirm the externally-observable
// side of that: the process still terminates on SIGINT via the OS's own
// default action, same as before this feature existed.
func TestE2ESignalNoHandlerDefaultDisposition(t *testing.T) {
	src := `
http.listen(8234, (req: HttpRequest): { status: number; body: string } => {
  return { status: 200, body: "ok" };
});
`
	cmd, out := startBackgroundServer(t, src, 8234)
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		// Terminated by the OS's default SIGINT disposition, as expected.
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("process ignored SIGINT with no handler registered — default disposition should still apply; output:\n%s", out.String())
	}
}

// TestE2ESignalSetIntervalOnlyGracefulShutdown confirms process.on also
// works for a program that keeps the process alive via setInterval alone,
// with no http.listen — this drives __kml_timer_drain (emit_timers.go)
// rather than __kml_event_loop_run (runtime_http.go), a separate loop that
// needed the identical signal-check block inserted.
func TestE2ESignalSetIntervalOnlyGracefulShutdown(t *testing.T) {
	readyPath := filepath.Join(t.TempDir(), "ready")
	binFile := buildBinary(t, fmt.Sprintf(`
process.on('SIGINT', () => {
  console.log("handled:SIGINT");
  process.exit(0);
});
fs.writeFileSync(%q, "ready");
setInterval(() => { console.log("tick"); }, 100000);
`, readyPath))
	cmd, out := startAndWaitForFile(t, binFile, readyPath)
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	code := waitExit(t, cmd, out, 5*time.Second)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; output:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "handled:SIGINT") {
		t.Errorf("handler never ran; output:\n%s", out.String())
	}
}

// TestE2ETimerNotPrematurelyFiredBySignal is a regression test for a bug
// found while building this feature (see TDD-00019/ADR-00079): nanosleep()
// interrupted by a signal returns early, and __kml_timer_drain's dofire
// block used to run unconditionally afterward regardless of whether the
// timer was actually due yet — so any registered signal handler would
// cause every pending setInterval/setTimeout to fire far too early. A
// 100-second interval must NOT fire just because a SIGINT arrives right
// after the process is up and its nanosleep-based wait has started.
func TestE2ETimerNotPrematurelyFiredBySignal(t *testing.T) {
	readyPath := filepath.Join(t.TempDir(), "ready")
	binFile := buildBinary(t, fmt.Sprintf(`
process.on('SIGINT', () => {
  console.log("handled:SIGINT");
  process.exit(0);
});
fs.writeFileSync(%q, "ready");
setInterval(() => { console.log("tick"); }, 100000);
`, readyPath))
	cmd, out := startAndWaitForFile(t, binFile, readyPath)
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	waitExit(t, cmd, out, 5*time.Second)
	if strings.Contains(out.String(), "tick") {
		t.Errorf("100s setInterval fired prematurely after a signal interrupted nanosleep(); output:\n%s", out.String())
	}
}

// TestE2ESignalDynamicEventNameRejected confirms process.on's event-name
// argument must be a compile-time string literal, the same precedent
// Object.hasOwn's dynamic-key rejection already sets.
func TestE2ESignalDynamicEventNameRejected(t *testing.T) {
	_, err := parseAndCompile(`
const name: string = "SIGINT";
process.on(name, () => { console.log("x"); });
`)
	if err == nil {
		t.Fatal("expected a compile error for a dynamic event name, got none")
	}
	if !strings.Contains(err.Error(), "string literal") {
		t.Errorf("error = %q, want it to mention requiring a string literal event name", err.Error())
	}
}

// TestE2ESignalUnsupportedEventNameRejected confirms an event name other
// than 'SIGINT'/'SIGTERM' is a clean compile error, not silently ignored.
func TestE2ESignalUnsupportedEventNameRejected(t *testing.T) {
	_, err := parseAndCompile(`
process.on('exit', () => { console.log("x"); });
`)
	if err == nil {
		t.Fatal("expected a compile error for an unsupported event name, got none")
	}
	if !strings.Contains(err.Error(), "SIGINT") {
		t.Errorf("error = %q, want it to mention the supported event names", err.Error())
	}
}
