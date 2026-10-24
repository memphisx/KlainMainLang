package llvm

// fiber_runtime.go — the ucontext pieces the task coroutines and generators
// share, and two host constants.

// fiberStackBytes is a coroutine's stack size: 1 MiB, enough for a handler's
// deep call chain (64 KiB overflowed in practice, ADR-00100).
const fiberStackBytes = 1024 * 1024

// isPlainStringType reports whether t is a string, not any other pointer-
// represented type (array, Map/Set, closure, Promise, Date, class instance…).
func isPlainStringType(t Type) bool {
	return t.IR == "ptr" && !t.IsArray() && !t.IsObject() && !t.IsMap() && !t.IsSet() &&
		!t.IsFunc() && !t.IsDynamic() && !t.IsPromise() && !t.IsDate() && !t.IsResponse() &&
		!t.IsClass() && !t.IsNull() && !t.IsUndefined()
}

// stdoutGlobalSymbol is libc's stdout FILE* global: `__stdoutp` on macOS
// (`stdout` is a macro over it), `stdout` on the Linux libcs. Used to setvbuf
// stdout at startup (ADR-00867); never called for Windows.
func (e *Emitter) stdoutGlobalSymbol() string {
	if e.opts.Target.OS() == "darwin" {
		return "__stdoutp"
	}
	return "stdout"
}

// ucontextLayout returns sizeof(ucontext_t) and the offsets of
// uc_stack.ss_sp, uc_stack.ss_size and uc_link, for building one by hand.
// They differ per libc and, on Linux, the size per architecture (x86-64 968,
// arm64 4560), measured with a sizeof/offsetof probe on each target.
func (e *Emitter) ucontextLayout() (size, ssSpOff, ssSizeOff, ucLinkOff int64) {
	if e.opts.Target.OS() == "windows" {
		// win32io.c's kml_ucontext: {fiber, ss_sp, ss_size, uc_link, fn, argc}.
		return 64, 8, 16, 24
	}
	if e.opts.Target.OS() == "darwin" {
		return 880, 8, 16, 32
	}
	// Linux (glibc): offsets are identical across architectures; size isn't.
	if e.opts.Target.Arch() == "arm64" {
		return 4560, 16, 32, 8
	}
	return 968, 16, 32, 8 // amd64 and other 64-bit Linux targets
}

// ensureFiberRuntime declares the ucontext calls once.
func (e *Emitter) ensureFiberRuntime() {
	if e.usedFiber {
		return
	}
	e.usedFiber = true
	e.emitGlobal("declare void @getcontext(ptr noundef)")
	e.emitGlobal("declare void @makecontext(ptr noundef, ptr noundef, i32 noundef, ...)")
	e.emitGlobal("declare i32 @swapcontext(ptr noundef, ptr noundef)")
	e.emitGlobal("declare void @__kml_swapcontext_kept(ptr, ptr)")
}
