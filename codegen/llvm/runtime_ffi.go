package llvm

import "runtime"

// runtime_ffi.go — libdl decls for node:ffi (TDD-00164). dlopen/dlsym/dlclose
// are the exact ensure*()-declared-libc-primitive shape os/fs already use. On
// Linux the historical -ldl is still requested (a no-op stub archive on glibc
// ≥ 2.34, required on older ones); on macOS the symbols live in libSystem.
// Windows has no dlopen — the emitter rejects node:ffi there cleanly until a
// LoadLibrary-backed shim exists.

// ffiRTLDFlags returns the host's RTLD_NOW|RTLD_LOCAL value for dlopen(2).
// RTLD_NOW is 0x2 on both Linux (glibc/musl) and macOS; RTLD_LOCAL is 0 on
// Linux and 0x4 on macOS (both are the default there anyway — passed
// explicitly so the emitted IR states the intended semantics).
func ffiRTLDFlags() int {
	if runtime.GOOS == "darwin" {
		return 0x2 | 0x4
	}
	return 0x2
}

// ensureFFIDl declares the libdl surface exactly once.
func (e *Emitter) ensureFFIDl() {
	if e.usedFFIDl {
		return
	}
	e.usedFFIDl = true
	if runtime.GOOS == "linux" {
		e.requireLink("dl")
	}
	e.ensureStrHeaderRuntime() // __kml_str_from_cstr for dlerror()/string returns
	e.emitGlobal("declare ptr @dlopen(ptr noundef, i32 noundef)")
	e.emitGlobal("declare ptr @dlsym(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @dlclose(ptr noundef)")
	e.emitGlobal("declare ptr @dlerror()")
}
