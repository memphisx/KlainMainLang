package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Cross-compilation flag (TDD-00146 Stage 1). These drive the real klainmain
// CLI as a subprocess: --target/--sysroot resolution and the guard messages
// live in main.go, not the in-process emitter the other E2E helpers use. Every
// case uses -emit-llvm so no cross toolchain (lld / a target sysroot) is needed
// — the triple stamping and the guards are all observable from the IR and the
// exit status alone. The end-to-end cross compile+run is exercised separately
// against an aarch64 Linux container (the ADR-00351 Docker-as-Linux lane).

// writeTinyTS drops a trivial program into a fresh temp dir and returns both
// paths; the dir doubles as a stand-in --sysroot (it just has to exist, since
// -emit-llvm never opens it).
func writeTinyTS(t *testing.T) (dir, src string) {
	t.Helper()
	dir = tempDir(t)
	src = filepath.Join(dir, "t.ts")
	if err := os.WriteFile(src, []byte("console.log(\"hi\");\n"), 0644); err != nil {
		t.Fatalf("write ts: %v", err)
	}
	return dir, src
}

// sameOSTriple is a clang triple for a *different* CPU architecture on the same
// OS as the test host — the cross target the OS-mismatch guard permits, so the
// triple-stamping cases exercise a real accepted cross build on any host.
func sameOSTriple() (triple, stamped string) {
	switch runtime.GOOS {
	case "linux":
		return "riscv64-linux-gnu", "riscv64-linux-gnu"
	case "darwin":
		return "x86_64-apple-darwin", "x86_64-apple-darwin"
	case "windows":
		return "aarch64-w64-windows-gnu", "aarch64-w64-windows-gnu"
	default:
		return "riscv64-linux-gnu", "riscv64-linux-gnu"
	}
}

func TestCrossTargetStampsTriple(t *testing.T) {
	bin := buildCLI(t)
	dir, src := writeTinyTS(t)
	triple, stamped := sameOSTriple()
	out, err := exec.Command(bin, "--target", triple, "--sysroot", dir, "-emit-llvm", src).CombinedOutput()
	if err != nil {
		t.Fatalf("emit-llvm cross build failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `target triple = "`+stamped+`"`) {
		t.Fatalf("bare triple %q was not stamped verbatim:\n%s", triple, out)
	}
}

func TestCrossTargetPresetResolves(t *testing.T) {
	bin := buildCLI(t)
	dir, src := writeTinyTS(t)
	out, _ := exec.Command(bin, "--target", "sfos-aarch64", "--sysroot", dir, "-emit-llvm", src).CombinedOutput()
	// Sailfish's native triple is aarch64-meego-linux-gnu (see crossPresets). On a
	// Linux host this is a same-OS cross build; on a macOS host it is the enabled
	// macOS→Linux cross-OS build (TDD-00146 Stage 1 completion) — both resolve the
	// preset and stamp the triple. Only a Windows host is a still-rejected pair.
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		if !strings.Contains(string(out), `target triple = "aarch64-meego-linux-gnu"`) {
			t.Fatalf("sfos-aarch64 preset did not resolve to the aarch64-meego-linux-gnu triple:\n%s", out)
		}
		return
	}
	if !strings.Contains(string(out), "is not supported yet") {
		t.Fatalf("expected the cross-OS guard to reject sfos-aarch64 on %s:\n%s", runtime.GOOS, out)
	}
}

// TestCrossTargetMacToLinuxCodegen is the IR-level proof that a cross-OS build
// retargets the *codegen*, not just the clang triple: an async program built for
// the Sailfish (Linux/aarch64) preset must stamp the Linux ucontext task-context
// size (4560 bytes for glibc aarch64, vs macOS's 880) and bake process.platform /
// process.arch as the target's "linux"/"arm64". Runs on Linux (same-OS) and macOS
// (the enabled cross-OS pair) hosts; a Windows host would hit the rejected pair.
func TestCrossTargetMacToLinuxCodegen(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("sfos-aarch64 is only an accepted target from a Linux or macOS host")
	}
	bin := buildCLI(t)
	dir := tempDir(t)
	src := filepath.Join(dir, "a.ts")
	prog := "async function f(): Promise<number> { return 1; }\n" +
		"async function main(): Promise<void> { const n = await f(); console.log(process.platform + process.arch + n); }\n" +
		"main();\n"
	if err := os.WriteFile(src, []byte(prog), 0644); err != nil {
		t.Fatalf("write ts: %v", err)
	}
	out, err := exec.Command(bin, "--target", "sfos-aarch64", "--sysroot", dir, "-emit-llvm", src).CombinedOutput()
	if err != nil {
		t.Fatalf("cross emit-llvm failed: %v\n%s", err, out)
	}
	s := string(out)
	for _, want := range []string{"[4560 x i8]", `c"linux\00"`, `c"arm64\00"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("cross build did not retarget codegen: missing %q in IR (async ucontext size / platform / arch)\n%s", want, s)
		}
	}
	if strings.Contains(s, "[880 x i8]") {
		t.Fatalf("cross build emitted the macOS ucontext layout (880 bytes) instead of the Linux one")
	}
}

func TestCrossTargetRejectsForeignOS(t *testing.T) {
	bin := buildCLI(t)
	dir, src := writeTinyTS(t)
	// A triple whose OS is a still-rejected cross-OS pair for this host. macOS→Linux
	// is the one enabled pair, so from a macOS host the rejected foreign OS is
	// Windows; from Linux/Windows hosts, a macOS target is always rejected.
	foreign := "x86_64-apple-darwin"
	if runtime.GOOS == "darwin" {
		foreign = "x86_64-w64-windows-gnu"
	}
	out, err := exec.Command(bin, "--target", foreign, "--sysroot", dir, "-emit-llvm", src).CombinedOutput()
	if err == nil {
		t.Fatalf("a cross-OS target (%s on %s host) should be rejected, got success:\n%s", foreign, runtime.GOOS, out)
	}
	if !strings.Contains(string(out), "is not supported yet") {
		t.Fatalf("expected a cross-OS not-supported message, got:\n%s", out)
	}
}

func TestHostBuildStampsNoTriple(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows host build intentionally stamps its own mingw triple")
	}
	bin := buildCLI(t)
	_, src := writeTinyTS(t)
	out, err := exec.Command(bin, "-emit-llvm", src).CombinedOutput()
	if err != nil {
		t.Fatalf("host emit-llvm failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "target triple") {
		t.Fatalf("host build should rely on clang's default triple, but stamped one:\n%s", out)
	}
}

func TestCrossTargetRequiresSysroot(t *testing.T) {
	bin := buildCLI(t)
	_, src := writeTinyTS(t)
	out, err := exec.Command(bin, "--target", "sfos-aarch64", "-emit-llvm", src).CombinedOutput()
	if err == nil {
		t.Fatalf("--target without --sysroot should fail, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "requires --sysroot") {
		t.Fatalf("expected a --sysroot-required message, got:\n%s", out)
	}
}

func TestSysrootWithoutTargetRejected(t *testing.T) {
	bin := buildCLI(t)
	dir, src := writeTinyTS(t)
	out, err := exec.Command(bin, "--sysroot", dir, "-emit-llvm", src).CombinedOutput()
	if err == nil {
		t.Fatalf("--sysroot without --target should fail, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "no effect without --target") {
		t.Fatalf("expected a sysroot-without-target message, got:\n%s", out)
	}
}

func TestCrossTargetMissingSysrootDirRejected(t *testing.T) {
	bin := buildCLI(t)
	_, src := writeTinyTS(t)
	missing := filepath.Join(tempDir(t), "does-not-exist")
	out, err := exec.Command(bin, "--target", "aarch64-linux-gnu", "--sysroot", missing, "-emit-llvm", src).CombinedOutput()
	if err == nil {
		t.Fatalf("a nonexistent --sysroot should fail, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "is not a directory") {
		t.Fatalf("expected a not-a-directory message, got:\n%s", out)
	}
}

func TestCrossTargetRejectsStatic(t *testing.T) {
	bin := buildCLI(t)
	dir, src := writeTinyTS(t)
	out, err := exec.Command(bin, "--target", "aarch64-linux-gnu", "--sysroot", dir, "--static", "-emit-llvm", src).CombinedOutput()
	if err == nil {
		t.Fatalf("--target + --static should fail, got success:\n%s", out)
	}
	// On macOS --static is rejected earlier (no static libc); on Linux the
	// cross-static guard fires. Both messages mention static linking.
	if !strings.Contains(string(out), "static") {
		t.Fatalf("expected a static-linking rejection, got:\n%s", out)
	}
}
