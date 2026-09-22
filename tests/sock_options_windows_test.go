package tests

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestWin32SockOptionTranslation drives the Windows shim's Linux→Winsock
// translation of message flags and socket options natively. The C helpers and
// the emitted IR speak the Linux ABI; untranslated, MSG_PEEK consumes its data
// and option 7 is not SO_SNDBUF. No emitted program passes these today, so
// testdata/win32/sock_options.c goes through the shim's own POSIX-named API.
func TestWin32SockOptionTranslation(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	shim := filepath.Join("..", "codegen", "llvm", "win32src")
	exe := filepath.Join(t.TempDir(), "sock_options.exe")
	args := []string{"-O1", "-w", "-o", exe,
		filepath.Join("testdata", "win32", "sock_options.c"),
		filepath.Join(shim, "win32io.c"), filepath.Join(shim, "win32proc.c"),
		filepath.Join(shim, "win32shim.c"), filepath.Join(shim, "win32fs.c"),
		filepath.Join(shim, "win32fswatch.c"),
		"-lws2_32", "-lntdll", "-lbcrypt", "-luserenv", "-ladvapi32", "-lshell32", "-lole32", "-liphlpapi", "-lpsapi"}
	if out, err := exec.Command("clang", args...).CombinedOutput(); err != nil {
		t.Fatalf("building the native shim test: %v\n%s", err, out)
	}
	out, err := exec.Command(exe).CombinedOutput()
	s := string(out)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, s)
	}
	for _, want := range []string{
		"MSG_PEEK left the data in place",
		"SO_SNDBUF/SO_RCVBUF accepted",
		"SO_LINGER accepted",
		"SO_RCVTIMEO accepted",
		"OK",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}
