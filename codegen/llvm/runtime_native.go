package llvm

import _ "embed"

// runtime_native.go — the native primitives the builtin modules written in
// TypeScript call (lib/native.d.ts, TDD-00231): the ones the runtime defines
// outside the pool. The asynchronous fs operations live in the pool
// (threadpoolsrc/klainpool.c), which calls each one's callback on the loop
// thread.

// ensureNativePool links the pool for a native operation: its C entry points,
// and the tick and promise-job drain it runs after each callback.
func (e *Emitter) ensureNativePool() {
	e.ensureThreadPool()
	e.ensureMicrotasks()
	e.ensureStrHeaderRuntime() // __kml_str_alloc/__kml_str_finalize for C string results
}

// ensureNativeErrno declares the errno lookups a builtin module builds Node's
// system errors from: the code name ("ECONNREFUSED"), libuv's description,
// and err.errno (libuv's negative number). Defined in nativesrc/native.c.
func (e *Emitter) ensureNativeErrno() {
	if e.fnDecls["__kml_native_errno_name"] {
		return
	}
	e.fnDecls["__kml_native_errno_name"] = true
	e.ensureErrnoCode()
	e.ensureErrnoDesc()
	e.ensureStrerror()
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`declare ptr @__kml_native_errno_name(double)
declare ptr @__kml_native_errno_desc(double)
declare double @__kml_native_uv_errno(double)`)
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
	e.emitGlobal("declare ptr @__kml_native_fs_error(double, ptr, i1 zeroext, ptr, i1 zeroext, ptr)")
}

//go:embed nativesrc/native.c
var nativeSource string

// NativeSource is the native primitives' C source.
func NativeSource() string { return nativeSource }

// UsesNative reports whether the program links native.c.
func (e *Emitter) UsesNative() bool {
	return e.fnDecls["__kml_native_errno_name"] || e.fnDecls["__kml_native_fs_error"]
}

// NativeCFlags select the routines of native.c the program declared.
func (e *Emitter) NativeCFlags() []string {
	var flags []string
	if e.fnDecls["__kml_native_errno_name"] {
		flags = append(flags, "-DKML_NATIVE_ERRNO")
	}
	if e.fnDecls["__kml_native_fs_error"] {
		flags = append(flags, "-DKML_NATIVE_FS_ERROR")
	}
	return flags
}

// ensureNativeTLS compiles in tlssrc/tlshandle.c (and links libssl) for the
// TLS primitives over the pool's TCP handles.
func (e *Emitter) ensureNativeTLS() {
	e.ensureNativePool()
	e.usedTLSHandles = true
}
