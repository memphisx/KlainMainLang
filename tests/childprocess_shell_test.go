package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ADR-00740: shell command lines reach the shell VERBATIM. On Windows the
// command previously went through quote_arg, so embedded quotes were
// escaped: exec('echo "hi"') printed \hi\ instead of "hi" (cmd's echo
// prints the quotes; sh's strips them — both expectations are Node's).
func TestE2EChildProcessExecPreservesQuotes(t *testing.T) {
	want := "hi"
	if runtime.GOOS == "windows" {
		want = `"hi"`
	}
	assertOutputImports(t, `
import { exec } from 'child_process'
exec('echo "hi"', (err, stdout, stderr) => { console.log(stdout.trim()) })
`, want)
}

func TestE2EChildProcessExecSyncPreservesQuotes(t *testing.T) {
	want := "hi"
	if runtime.GOOS == "windows" {
		want = `"hi"`
	}
	assertOutputImports(t, `
import { execSync } from 'child_process'
console.log(execSync('echo "hi"').trim())
`, want)
}

// ADR-00740: spawn(command, { shell: true }) routes the whole command
// through the platform shell (previously the option was silently discarded
// and the command spawned directly, so built-ins and pipelines failed).
func TestE2EChildProcessSpawnShellTrue(t *testing.T) {
	assertOutputImports(t, `
import { spawn } from 'child_process'
const child = spawn('echo klainshell', { shell: true })
child.stdout.on('data', (d: string) => { console.log(d.trim()) })
child.on('exit', (code: number) => { console.log('exit:', code) })
`, "klainshell\nexit: 0")
}

// ADR-00740: a .bat/.cmd spawned WITHOUT a shell must fail — CreateProcessW
// would otherwise hand it to an implicit cmd.exe whose parsing turns quoted
// arguments into command injection (CVE-2024-27980; Node answers EINVAL).
// The failed-spawn convention here reports exit 127; the batch file's body
// must never run. With shell: true it runs normally.
func TestE2EChildProcessSpawnBatWithoutShellFails(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows .bat semantics")
	}
	dir := tempDir(t)
	bat := filepath.Join(dir, "kml-guard.bat")
	if err := os.WriteFile(bat, []byte("@echo pwned\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	batFwd := filepath.ToSlash(bat)
	// A .bat/.cmd spawned WITHOUT a shell is refused (CVE-2024-27980): Node
	// emits an 'error' event (EINVAL), not an 'exit' — the spawn 'error' path
	// (ADR-00754). The 'close' that follows drives the shelled retry, which
	// runs the batch correctly through cmd.exe.
	assertOutputImports(t, fmt.Sprintf(`
import { spawn } from 'child_process'
const direct = spawn(%q, [])
direct.on('error', (e) => { console.log('direct error:', e.message.indexOf('spawn') === 0) })
direct.on('close', () => {
  const shelled = spawn(%q, { shell: true })
  shelled.stdout.on('data', (d: string) => { console.log('shelled:', d.trim()) })
  shelled.on('exit', (c2: number) => { console.log('shelled exit:', c2) })
})
`, batFwd, batFwd), "direct error: true\nshelled: pwned\nshelled exit: 0")
}

// ADR-00759: a child's exit code is the full 32-bit value Windows reports,
// not the 8-bit POSIX wait-status field. `cmd /c exit 300` / `sh -c "exit
// 300"` yields 300 on Windows but 300 & 0xff = 44 on POSIX (where exit codes
// are genuinely 8-bit, as Node also reports there). Both the async 'exit'
// event and spawnSync's `.status` take the wide value.
func TestE2EChildProcessWideExitCode(t *testing.T) {
	shell, flag, want := "sh", "-c", 44
	if runtime.GOOS == "windows" {
		shell, flag, want = "cmd", "/c", 300
	}
	assertOutputImports(t, fmt.Sprintf(`
import { spawn, spawnSync } from 'child_process'
const r = spawnSync(%q, [%q, "exit 300"])
console.log("sync:", r.status)
const c = spawn(%q, [%q, "exit 300"])
c.on('exit', (code: number) => { console.log("async:", code) })
`, shell, flag, shell, flag), fmt.Sprintf("sync: %d\nasync: %d", want, want))
}
