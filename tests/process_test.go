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
	compareLines(t, got, "got: hello\ngot: world\ntrue")
}
func TestE2EProcessReadLineSyncNoTrailingNewline(t *testing.T) {
	src := `
const line1 = process.readLineSync()
console.log("got: " + line1)
const line2 = process.readLineSync()
console.log(line2 === null)
`
	got := compileAndRunWithStdin(t, src, "last line no newline")
	compareLines(t, got, "got: last line no newline\ntrue")
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

// ADR-00589: the { cwd } option runs the child in a different directory.
func TestE2EExecFileSyncCwd(t *testing.T) {
	assertOutput(t, `
const out: string = process.execFileSync("pwd", [], { cwd: "/" })
console.log(out.trim())
`, "/")
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
`, "true")
}

func TestE2EProcessChdirWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`process.chdir()`)
	if err == nil {
		t.Fatal("expected a compile error for process.chdir() with no arguments, got none")
	}
}

func TestE2EProcessPidIsPositive(t *testing.T) {
	assertOutput(t, `console.log(process.pid > 0)`, "true")
}

func TestE2EProcessMemoryUsageRssPositive(t *testing.T) {
	// rss is the real instantaneous resident set (ADR-00570: Darwin task_info /
	// Linux /proc/self/statm), so it grows as memory is allocated; the V8-heap
	// fields have no native analogue and report 0.
	assertOutput(t, `
const before = process.memoryUsage().rss;
let arr: number[] = [];
for (let i = 0; i < 1000000; i++) { arr.push(i); }
const m = process.memoryUsage();
console.log(m.rss > 0);
console.log(m.rss >= before);
console.log(m.heapUsed);
console.log(m.external);
console.log(m.arrayBuffers);`, "true\ntrue\n0\n0\n0")
}

func TestE2EProcessMemoryUsageWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`process.memoryUsage(1)`)
	if err == nil {
		t.Fatal("expected a compile error for process.memoryUsage(1), got none")
	}
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
`, "true")
}

func TestE2EProcessKillWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`process.kill()`)
	if err == nil {
		t.Fatal("expected a compile error for process.kill() with no arguments, got none")
	}
}

// --- process.stdout.write / process.stderr.write ---

func TestE2EProcessStdoutWriteNoAutoNewline(t *testing.T) {
	stdout, _ := compileAndRunCaptureStderr(t, `
process.stdout.write("a")
process.stdout.write("b")
process.stdout.write("c\n")
`)
	if stdout != "abc\n" {
		t.Errorf("stdout: got %q, want %q", stdout, "abc\n")
	}
}

func TestE2EProcessStderrWriteNoAutoNewline(t *testing.T) {
	_, stderr := compileAndRunCaptureStderr(t, `
process.stderr.write("x")
process.stderr.write("y")
`)
	if stderr != "xy" {
		t.Errorf("stderr: got %q, want %q", stderr, "xy")
	}
}

func TestE2EProcessStdoutWriteDoesNotGoToStderr(t *testing.T) {
	stdout, stderr := compileAndRunCaptureStderr(t, `process.stdout.write("only-stdout")`)
	if stdout != "only-stdout" {
		t.Errorf("stdout: got %q, want %q", stdout, "only-stdout")
	}
	if stderr != "" {
		t.Errorf("stderr: got %q, want empty", stderr)
	}
}

func TestE2EProcessStdoutWriteInterleavesWithConsoleLog(t *testing.T) {
	// Both go through buffered stdio on fd 1 (emitProcessStreamWrite's own
	// doc comment explains why process.stdout.write must use printf, not
	// dprintf, for exactly this reason) so source order is preserved.
	stdout, _ := compileAndRunCaptureStderr(t, `
process.stdout.write("before-")
console.log("logged")
process.stdout.write("after")
`)
	if stdout != "before-logged\nafter" {
		t.Errorf("stdout: got %q, want %q", stdout, "before-logged\nafter")
	}
}

func TestE2EProcessStdoutWriteWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`process.stdout.write()`)
	if err == nil {
		t.Fatal("expected a compile error for process.stdout.write() with no arguments, got none")
	}
}

func TestE2EProcessStderrWriteWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`process.stderr.write("a", "b")`)
	if err == nil {
		t.Fatal("expected a compile error for process.stderr.write with 2 arguments, got none")
	}
}

// --- process introspection + nextTick (ADR-00332) ---

func TestE2EProcessArch(t *testing.T) {
	// arch is a compile-time constant matching the build machine.
	want := map[string]string{"amd64": "x64", "386": "ia32"}[runtime.GOARCH]
	if want == "" {
		want = runtime.GOARCH
	}
	assertOutput(t, `console.log(process.arch)`, want)
}

func TestE2EProcessNextTickOrdering(t *testing.T) {
	assertOutput(t, `
const order: string[] = []
process.nextTick(() => { order.push("tick") })
order.push("sync")
Promise.resolve().then(() => { console.log(order.join(",")) })
`, "sync,tick")
}

func TestE2EProcessUptimeNonNegative(t *testing.T) {
	assertOutput(t, `console.log(process.uptime() >= 0)`, "true")
}

func TestE2EProcessHrtime(t *testing.T) {
	assertOutput(t, `
const hr = process.hrtime()
console.log(hr.length)
console.log(hr[0] >= 0 && hr[1] >= 0)
`, "2\ntrue")
}

// ADR-00582: process.hrtime(prev) returns the elapsed diff, nsec in [0, 1e9).
func TestE2EProcessHrtimeDiff(t *testing.T) {
	assertOutput(t, `
const start = process.hrtime()
let sum = 0
for (let i = 0; i < 100000; i++) { sum += i }
const diff = process.hrtime(start)
console.log(diff.length)
console.log(diff[0] >= 0)
console.log(diff[1] >= 0 && diff[1] < 1000000000)
console.log(sum > 0)
`, "2\ntrue\ntrue\ntrue")
}

func TestE2EProcessHrtimeBigintMonotonic(t *testing.T) {
	assertOutput(t, `
const a = process.hrtime.bigint()
const b = process.hrtime.bigint()
console.log(b >= a)
console.log(typeof a === "bigint")
`, "true\ntrue")
}

// --- process.env write (ADR-00333) ---

func TestE2EProcessEnvWrite(t *testing.T) {
	assertOutput(t, `
process.env.KML_TEST_VAR = "hello"
console.log(process.env.KML_TEST_VAR)
const k = "KML_DYN"
process.env[k] = "world"
console.log(process.env["KML_DYN"])
`, "hello\nworld")
}

// A written env var is inherited by a child process (the real use case).
func TestE2EProcessEnvWriteInheritedByChild(t *testing.T) {
	assertOutput(t, `
process.env.KML_CHILD_SEES = "yes"
const out = process.execFileSync("printenv", ["KML_CHILD_SEES"])
console.log(out.trim())
`, "yes")
}

func TestE2EProcessEnvCompoundRejected(t *testing.T) {
	_, err := parseAndCompile(`process.env.X += "y"`)
	if err == nil {
		t.Fatal("expected a compile error for compound assignment to process.env, got none")
	}
}

// --- process lifecycle: on('exit'/'uncaughtException') + exitCode (ADR-00334) ---

func TestE2EProcessExitHandlerAndExitCode(t *testing.T) {
	// The 'exit' listener runs at normal end with the exit code, and the
	// process returns process.exitCode.
	out, code := compileAndRunExpectExit(t, `
process.on('exit', (code: number) => { console.log("exit:" + code) })
process.exitCode = 3
console.log("done")
`)
	if got := strings.TrimSpace(out); got != "done\nexit:3" {
		t.Errorf("output: got %q, want %q", got, "done\nexit:3")
	}
	if code != 3 {
		t.Errorf("exit code: got %d, want 3", code)
	}
}

func TestE2EProcessExitRunsHandler(t *testing.T) {
	out, code := compileAndRunExpectExit(t, `
process.on('exit', (code: number) => { console.log("bye:" + code) })
process.exit(5)
console.log("unreachable")
`)
	if got := strings.TrimSpace(out); got != "bye:5" {
		t.Errorf("output: got %q, want %q", got, "bye:5")
	}
	if code != 5 {
		t.Errorf("exit code: got %d, want 5", code)
	}
}

func TestE2EProcessUncaughtException(t *testing.T) {
	// The handler runs (skipping the default "Uncaught: ..." print); the process
	// still exits 1 (this exception model has already unwound to the top).
	out, code := compileAndRunExpectExit(t, `
process.on('uncaughtException', (err) => { console.log("caught:" + err.message) })
throw new Error("boom")
`)
	if got := strings.TrimSpace(out); got != "caught:boom" {
		t.Errorf("output: got %q, want %q", got, "caught:boom")
	}
	if code != 1 {
		t.Errorf("exit code: got %d, want 1", code)
	}
}

// TDD-00131 (process fidelity): process.execPath is the running binary's own
// absolute, symlink-resolved path — always absolute like Node's, and a
// length-prefixed string so `.length` works (ADR-00395).
func TestE2EProcessExecPath(t *testing.T) {
	assertOutput(t, `console.log(process.execPath.length > 0 && process.execPath.startsWith("/"))`, "true")
}

// Regression: process.cwd() must be a length-prefixed string (previously
// returned a raw getcwd() pointer with no header, so `.length` read garbage).
func TestE2EProcessCwdHasLength(t *testing.T) {
	assertOutput(t, `console.log(process.cwd().length > 0)`, "true")
}

// process.emitWarning(message, type?) writes Node's `(node:<pid>) <type>:
// <message>` format to stderr, defaulting the type to "Warning".
func TestE2EProcessEmitWarning(t *testing.T) {
	_, stderr := compileAndRunCaptureStderr(t, `
process.emitWarning("careful now");
process.emitWarning("old api", "DeprecationWarning");
`)
	if !strings.Contains(stderr, "Warning: careful now\n") {
		t.Errorf("stderr missing default warning: %q", stderr)
	}
	if !strings.Contains(stderr, "DeprecationWarning: old api\n") {
		t.Errorf("stderr missing typed warning: %q", stderr)
	}
}

// ADR-00580: process.emitWarning's options-object form { type, code, detail }
// prints `(node:<pid>) [<code>] <type>: <message>` plus a detail line.
func TestE2EProcessEmitWarningOptions(t *testing.T) {
	_, stderr := compileAndRunCaptureStderr(t, `
process.emitWarning("msg here", { type: "DeprecationWarning", code: "MY001", detail: "extra detail" });
process.emitWarning("m3", { code: "C1" });
`)
	if !strings.Contains(stderr, "[MY001] DeprecationWarning: msg here\n") {
		t.Errorf("stderr missing code+type warning: %q", stderr)
	}
	if !strings.Contains(stderr, "extra detail\n") {
		t.Errorf("stderr missing detail line: %q", stderr)
	}
	if !strings.Contains(stderr, "[C1] Warning: m3\n") {
		t.Errorf("stderr missing code-only warning: %q", stderr)
	}
}

// TDD-00136: process.version / process.versions report the pinned Node
// compatibility baseline (v22.11.0) plus this compiler's own klain version;
// versions.node/v8 match that Node release verbatim, no fabricated bundled-lib
// versions.
func TestE2EProcessVersion(t *testing.T) {
	assertOutput(t, `console.log(process.version)`, "v22.11.0")
}

func TestE2EProcessVersionsObject(t *testing.T) {
	assertOutput(t, `
console.log(process.versions.node);
console.log(process.versions.v8);
console.log(process.versions.klain);
console.log(JSON.stringify(process.versions));
`, "22.11.0\n12.4.254.21-node.21\n0.52.0\n{\"node\":\"22.11.0\",\"v8\":\"12.4.254.21-node.21\",\"klain\":\"0.52.0\"}")
}

// TDD-00131: process.on('warning', h) fires h with the warning as an Error
// when process.emitWarning is called (the stderr print still happens too).
func TestE2EProcessOnWarning(t *testing.T) {
	assertOutput(t, `
process.on('warning', (w: Error) => { console.log("warn:", w.name, w.message); });
process.emitWarning("careful", "CustomWarning");
`, "warn: CustomWarning careful")
}

func TestE2EProcessStdioIsTTY(t *testing.T) {
	// process.stdout/.stderr/.stdin .isTTY — a real isatty(fd) probe
	// (ADR-00424). Under the test harness all three are pipes, so false;
	// the binding form checks bool-typed inference.
	assertOutput(t, `
const t = process.stdout.isTTY
console.log(t)
console.log(process.stderr.isTTY)
console.log(!process.stdin.isTTY)
`, "false\nfalse\ntrue")
}

func TestE2EProcessGetUIDFamily(t *testing.T) {
	// POSIX credential reads (ADR-00428) — non-negative numbers, and the
	// effective ids match the real ones under a normal (non-setuid) run.
	assertOutput(t, `
console.log(process.getuid() >= 0)
console.log(process.getgid() >= 0)
console.log(process.geteuid() === process.getuid())
console.log(process.getegid() === process.getgid())
`, "true\ntrue\ntrue\ntrue")
}
