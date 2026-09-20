package tests

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestWin32ReactorEmulatedCompletion drives the Windows shim's emulated-
// completion path natively (ADR-01022). A socket whose handle is already bound
// to another completion port — a listener a cluster peer process associated
// first, or a socket watched from a second reactor thread — cannot join this
// reactor's port, so its AcceptEx / zero-read run with an event-backed
// OVERLAPPED whose completion a registered wait forwards to the port.
//
// Nothing in the emitted runtime currently shares a socket that way (each
// Windows cluster worker binds for itself), so no E2E program reaches this
// path; testdata/win32/emulated_completion.c creates the condition directly —
// it associates the sockets with a foreign port first — and goes through the
// shim's own socket()/select()/accept()/read(). It also checks that nothing of
// ours is ever queued to the foreign port, which is the bug the path exists to
// prevent.
func TestWin32ReactorEmulatedCompletion(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	shim := filepath.Join("..", "codegen", "llvm", "win32src")
	exe := filepath.Join(t.TempDir(), "emulated_completion.exe")
	args := []string{"-O1", "-w", "-o", exe,
		filepath.Join("testdata", "win32", "emulated_completion.c"),
		filepath.Join(shim, "win32io.c"), filepath.Join(shim, "win32proc.c"),
		filepath.Join(shim, "win32shim.c"), filepath.Join(shim, "win32fs.c"),
		filepath.Join(shim, "win32fswatch.c"),
		"-lws2_32", "-lntdll", "-lbcrypt", "-luserenv", "-ladvapi32", "-lshell32", "-lole32", "-liphlpapi", "-lpsapi"}
	if out, err := exec.Command("clang", args...).CombinedOutput(); err != nil {
		t.Fatalf("building the native shim test: %v\n%s", err, out)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(cmd.Environ(), "KML_IO_TRACE=1")
	out, err := cmd.CombinedOutput()
	s := string(out)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, s)
	}
	for _, want := range []string{
		"belongs to another port: emulated completion",
		"accepted through an emulated AcceptEx",
		"read 4 bytes through an emulated zero-read: ping",
		"foreign port received nothing",
		"EOF read -> 0",
		"OK",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}
