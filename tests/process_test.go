package tests

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- process.argv / process.exit / process.env ---

func TestE2EProcessArgv(t *testing.T) {
	t.Helper()
	got := compileAndRunWithArgs(t, `
const args: string[] = process.argv
console.log(args.length)
console.log(args[1])
console.log(args[2])
`, "hello", "world")
	want := "3\nhello\nworld"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestE2EProcessExit(t *testing.T) {
	stdout, code := compileAndRunExpectExit(t, `
console.log("before")
process.exit(42)
console.log("after")
`)
	if stdout != "before" {
		t.Errorf("stdout: got %q, want %q", stdout, "before")
	}
	if code != 42 {
		t.Errorf("exit code: got %d, want 42", code)
	}
}

func TestE2EProcessEnv(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	binFile := buildBinary(t, `
const fromDot: string = process.env.KML_TEST_VAR
console.log(fromDot)
const key: string = "KML_TEST_VAR"
const fromBracket: string = process.env[key]
console.log(fromBracket)
const missing = process.env.KML_TEST_VAR_MISSING ?? "default"
console.log(missing)
`)
	cmd := exec.Command(binFile)
	cmd.Env = append(os.Environ(), "KML_TEST_VAR=hello-env")
	result, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := strings.TrimRight(string(result), "\n")
	want := "hello-env\nhello-env\ndefault"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- process.readLineSync ---

func TestE2EProcessReadLineSync(t *testing.T) {
	src := `
const line1 = process.readLineSync()
console.log("got: " + line1)
const line2 = process.readLineSync()
console.log("got: " + line2)
const line3 = process.readLineSync()
console.log(line3 === null)
`
	got := compileAndRunWithStdin(t, src, "hello\nworld\n")
	compareLines(t, got, "got: hello\ngot: world\n1")
}
func TestE2EProcessReadLineSyncNoTrailingNewline(t *testing.T) {
	src := `
const line1 = process.readLineSync()
console.log("got: " + line1)
const line2 = process.readLineSync()
console.log(line2 === null)
`
	got := compileAndRunWithStdin(t, src, "last line no newline")
	compareLines(t, got, "got: last line no newline\n1")
}

// --- process.execFileSync ---
//
// Spawns real child processes via fork+execvp — /bin/echo, /bin/sh, and
// PATH-resolved bare names are used since they're present on every POSIX
// system this compiler targets (macOS, Linux), unlike httpbin.org-style
// external-network tests which stay in examples/, not here.

func TestE2EExecFileSyncCapturesStdout(t *testing.T) {
	assertOutput(t, `
const args: string[] = ["hello", "world"]
const out: string = process.execFileSync("/bin/echo", args)
console.log(out)
`, "hello world\n")
}

func TestE2EExecFileSyncNoArgs(t *testing.T) {
	assertOutput(t, `
const out: string = process.execFileSync("/bin/echo")
console.log(out.length)
`, "1")
}

func TestE2EExecFileSyncResolvesViaPath(t *testing.T) {
	assertOutput(t, `
const args: string[] = ["via", "path"]
const out: string = process.execFileSync("echo", args)
console.log(out)
`, "via path\n")
}

func TestE2EExecFileSyncDoesNotInvokeAShell(t *testing.T) {
	// Real execFileSync semantics: argv is passed straight to execvp, no
	// shell involved — shell metacharacters must come back out verbatim,
	// not get expanded/interpreted.
	assertOutput(t, `
const args: string[] = ["$(echo pwned); ls"]
const out: string = process.execFileSync("/bin/echo", args)
console.log(out)
`, "$(echo pwned); ls\n")
}

func TestE2EExecFileSyncNonZeroExitThrows(t *testing.T) {
	assertOutput(t, `
try {
    process.execFileSync("/usr/bin/false")
    console.log("should not print")
} catch (e) {
    console.log(e.message)
}
`, "Command failed with exit code 1: /usr/bin/false")
}

func TestE2EExecFileSyncSignalDeathThrows(t *testing.T) {
	assertOutput(t, `
const args: string[] = ["-c", "kill -9 $$"]
try {
    process.execFileSync("/bin/sh", args)
    console.log("should not print")
} catch (e) {
    console.log(e.message)
}
`, "Command was terminated by signal 9: /bin/sh")
}

func TestE2EExecFileSyncMissingBinaryThrows(t *testing.T) {
	assertOutput(t, `
try {
    process.execFileSync("/no/such/binary/at/all")
    console.log("should not print")
} catch (e) {
    console.log(e.message)
}
`, "Command failed with exit code 127: /no/such/binary/at/all")
}

func TestE2EExecFileSyncLargeOutputGrowsBuffer(t *testing.T) {
	// Forces output past a single pipe read (and the growable buffer's
	// initial capacity), exercising the realloc-doubling path.
	assertOutput(t, `
const args: string[] = ["-c", "for i in $(seq 1 5000); do printf '0123456789'; done"]
const out: string = process.execFileSync("/bin/sh", args)
console.log(out.length)
`, "50000")
}

func TestE2EExecFileSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`process.execFileSync()`)
	if err == nil {
		t.Fatal("expected a compile error for process.execFileSync() with no arguments, got none")
	}
}

func TestE2EExecFileSyncNonStringArrayArgsRejected(t *testing.T) {
	_, err := parseAndCompile(`
const args: number[] = [1, 2, 3]
process.execFileSync("/bin/echo", args)
`)
	if err == nil {
		t.Fatal("expected a compile error for process.execFileSync with a non-string[] args argument, got none")
	}
}

// --- process.cwd/chdir/pid/platform/kill ---

func TestE2EProcessCwdAndChdir(t *testing.T) {
	dir := t.TempDir()
	// Resolve symlinks the same way the OS's own getcwd() would (macOS's
	// /tmp is itself a symlink to /private/tmp) so the comparison is exact,
	// not just "close enough".
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", dir, err)
	}
	src := fmt.Sprintf(`
process.chdir(%q)
console.log(process.cwd())
`, resolved)
	assertOutput(t, src, resolved)
}

func TestE2EProcessChdirNonexistentThrows(t *testing.T) {
	assertOutput(t, `
try {
    process.chdir("/definitely/does/not/exist/kml-test-dir")
    console.log("should not print")
} catch (e) {
    console.log(e.message.startsWith("cannot change directory to '/definitely/does/not/exist/kml-test-dir': "))
}
`, "1")
}

func TestE2EProcessChdirWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`process.chdir()`)
	if err == nil {
		t.Fatal("expected a compile error for process.chdir() with no arguments, got none")
	}
}

func TestE2EProcessPidIsPositive(t *testing.T) {
	assertOutput(t, `console.log(process.pid > 0)`, "1")
}

func TestE2EProcessPlatform(t *testing.T) {
	want := runtime.GOOS
	if want == "windows" {
		want = "win32"
	}
	assertOutput(t, `console.log(process.platform)`, want)
}

func TestE2EProcessKillSignalZeroOnSelfSucceeds(t *testing.T) {
	// Signal 0 is the POSIX "existence check" convention: no signal is
	// actually delivered, kill() just reports whether it could have been.
	assertOutput(t, `
process.kill(process.pid, 0)
console.log("no throw")
`, "no throw")
}

func TestE2EProcessKillDefaultsToSigterm(t *testing.T) {
	_, err := parseAndCompile(`process.kill(1)`)
	if err != nil {
		t.Fatalf("expected process.kill with a single argument (implicit SIGTERM) to compile, got: %v", err)
	}
}

func TestE2EProcessKillNonexistentPidThrows(t *testing.T) {
	assertOutput(t, `
try {
    process.kill(999999999, 0)
    console.log("should not print")
} catch (e) {
    console.log(e.message.startsWith("kill(pid=999999999, signal=0): "))
}
`, "1")
}

func TestE2EProcessKillWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`process.kill()`)
	if err == nil {
		t.Fatal("expected a compile error for process.kill() with no arguments, got none")
	}
}
