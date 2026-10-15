package tests

import "testing"

// ADR-00737: killing a child this process already reaped must throw (ESRCH),
// answered from the child table — on Windows the pid may already belong to an
// unrelated process (pid reuse), which the old OpenProcess(pid) fallback
// would have terminated. libuv's uv_kill answers UV_ESRCH for an exited
// child the same way, on every host.
func TestE2EChildProcessKillAfterExitThrowsESRCH(t *testing.T) {
	assertOutputImports(t, `
import { spawn } from 'child_process'
const child = spawn("echo", ["x"])
child.on('exit', (code: number) => {
  try { process.kill(child.pid!); console.log("killed") } catch (e) { console.log("threw") }
})
`, "threw")
}

// process.kill(pid, 0) is a health check: true while the child runs, ESRCH
// once it has exited — on Windows as well, where the exited process's handle
// may still open (libuv reads its exit code).
func TestE2EProcessKillZeroAfterExitThrowsESRCH(t *testing.T) {
	assertOutputImports(t, `
import { spawn } from 'child_process'
// A child that runs until its stdin closes.
const child = process.platform === 'win32' ? spawn('cmd.exe', ['/d', '/c', 'more']) : spawn('cat')
console.log(process.kill(child.pid!, 0))
child.on('exit', () => {
  try { process.kill(child.pid!, 0); console.log("alive") } catch (e: any) { console.log(e.code) }
})
child.stdin!.end()
`, "true\nESRCH")
}
