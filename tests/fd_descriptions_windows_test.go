package tests

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestWin32FdDescriptions drives the Windows shim's refcounted fd descriptions
// natively (TDD-00183 Stage 3). dup2() shares one description between two fds:
// the handle is torn down by the last close rather than the first, readiness is
// seen through every alias, and sockets, pipe ends and foreign sockets all
// allocate from one 512-slot pool (the former 384-socket cap), which answers
// EMFILE when full.
//
// Nothing in the emitted runtime calls dup2 on this platform — a child's stdio
// is handed over by handle — so no E2E program reaches the aliasing path;
// testdata/win32/fd_descriptions.c goes through the shim's own
// socket()/pipe()/dup2()/select()/close().
func TestWin32FdDescriptions(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	shim := filepath.Join("..", "codegen", "llvm", "win32src")
	exe := filepath.Join(t.TempDir(), "fd_descriptions.exe")
	args := []string{"-O1", "-w", "-o", exe,
		filepath.Join("testdata", "win32", "fd_descriptions.c"),
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
		"socket alias survived the original's close: ping/pong",
		"socket closed once, by the last reference",
		"readiness seen through both aliases",
		"pipe alias: write end closed once, by the last reference",
		"opened 512 sockets from the shared pool, then EMFILE",
		"OK",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

// TestE2EWindowsManySockets: a program can hold more sockets than the former
// fixed 384-slot socket range. 450 bound UDP sockets, all live at once, each
// reporting its own ephemeral port; then all closed and the loop exits.
// Windows-only: the ceiling being lifted was this platform's, and 450 open
// descriptors is above macOS's default RLIMIT_NOFILE.
func TestE2EWindowsManySockets(t *testing.T) {
	assertOutputImports(t, `
import dgram from 'dgram'
const first = dgram.createSocket('udp4')
first.bind(0)
const socks = [first]
let bound = first.address().port > 0 ? 1 : 0
for (let i = 1; i < 450; i++) {
  const s = dgram.createSocket('udp4')
  s.bind(0)
  if (s.address().port > 0) bound = bound + 1
  socks.push(s)
}
console.log("bound", bound)
for (const s of socks) s.close()
console.log("closed")
`, "bound 450\nclosed")
}
