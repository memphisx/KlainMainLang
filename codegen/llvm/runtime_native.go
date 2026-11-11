package llvm

import _ "embed"

// runtime_native.go — the native primitives the builtin modules written in
// TypeScript call (lib/native.d.ts, TDD-00231): the ones the runtime defines
// outside the pool. The asynchronous fs operations live in the pool
// (threadpoolsrc/klainpool.c), which calls each one's callback on the loop
// thread.

// ensureNativeErrno declares the errno lookups a builtin module builds Node's
// system errors from: the code name ("ECONNREFUSED"), libuv's description,
// and err.errno (libuv's negative number). Defined in nativesrc/native.c.
func (e *Emitter) ensureNativeErrno() {
	if e.fnDecls["__kml_native_errno_name"] {
		return
	}
	e.fnDecls["__kml_native_errno_name"] = true
	e.ensureStrHeaderRuntime()
}

// ensureNativeFsError declares __kml_native_fs_error(errno, syscall, path?,
// dest?): the Error Node's fs raises for a failed syscall (`ENOENT: no such
// file or directory, rename 'x' -> 'y'`, with its code, errno, syscall, path
// and dest). Defined in nativesrc/native.c.
func (e *Emitter) ensureNativeFsError() {
	if e.fnDecls["__kml_native_fs_error"] {
		return
	}
	e.fnDecls["__kml_native_fs_error"] = true
	e.ensureFsThrow()
}

//go:embed nativesrc/native.c
var nativeSource string

// NativeSource is the native primitives' C source.
func NativeSource() string { return nativeSource }

//go:embed nativesrc/entrypath.c
var entryPathSource string

// UsesNative reports whether the program links native.c.
func (e *Emitter) UsesNative() bool {
	return e.fnDecls["__kml_native_errno_name"] || e.fnDecls["__kml_native_fs_error"]
}

// NativeCFlags: native.c is compiled the same way for every program; the
// link drops what a program does not use (DeadStripFlags).
func (e *Emitter) NativeCFlags() []string { return DeadStripFlags(e.exportsRuntime()) }
