// runtime_os.go — process.env's libc helpers. The os module is
// lib/node/os.ts over osinfo.c.
package llvm

// ensureUnsetenv declares unsetenv(3) — `delete process.env.KEY` (ADR-00487).
func (e *Emitter) ensureUnsetenv() {
	if e.usedUnsetenv {
		return
	}
	e.usedUnsetenv = true
	e.emitGlobal("declare i32 @unsetenv(ptr noundef)")
}
