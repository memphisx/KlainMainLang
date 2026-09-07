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
  try { process.kill(child.pid); console.log("killed") } catch (e) { console.log("threw") }
})
`, "threw")
}
