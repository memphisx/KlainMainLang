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
	assertOutputImports(t, fmt.Sprintf(`
import { spawn } from 'child_process'
const direct = spawn(%q, [])
direct.stdout.on('data', (d: string) => { console.log('direct:', d.trim()) })
direct.on('exit', (code: number) => {
  console.log('direct exit:', code)
  const shelled = spawn(%q, { shell: true })
  shelled.stdout.on('data', (d: string) => { console.log('shelled:', d.trim()) })
  shelled.on('exit', (c2: number) => { console.log('shelled exit:', c2) })
})
`, batFwd, batFwd), "direct exit: 127\nshelled: pwned\nshelled exit: 0")
}
