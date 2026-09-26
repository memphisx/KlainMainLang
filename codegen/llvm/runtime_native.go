package llvm

import "fmt"

// runtime_native.go — the native primitives the builtin modules written in
// TypeScript call (lib/native.d.ts, TDD-00231): the ones the IR runtime
// defines. The asynchronous fs operations live in the pool
// (threadpoolsrc/klainpool.c), which calls each one's callback on the loop
// thread.

// ensureNativePool links the pool for a native operation: its C entry points,
// and the tick and promise-job drain it runs after each callback.
func (e *Emitter) ensureNativePool() {
	e.ensureThreadPool()
	e.ensureMicrotasks()
	e.ensureStrHeaderRuntime() // __kml_str_alloc/__kml_str_finalize for C string results
}

// ensureNativeErrno defines the errno lookups a builtin module builds Node's
// system errors from: the code name ("ECONNREFUSED"), libuv's description,
// and err.errno (libuv's negative number).
func (e *Emitter) ensureNativeErrno() {
	if e.fnDecls["__kml_native_errno_name"] {
		return
	}
	e.fnDecls["__kml_native_errno_name"] = true
	e.ensureErrnoCode()
	e.ensureErrnoDesc()
	e.ensureStrerror()
	e.ensureStrHeaderRuntime()
	e.ensureStrlen()
	e.ensureMemcpy()
	unknown := e.internString("UNKNOWN")
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_native_errno_name(double %%e) {
entry:
  %%n = fptosi double %%e to i32
  %%c = call ptr @__kml_errno_code(i32 %%n)
  %%isnull = icmp eq ptr %%c, null
  %%r = select i1 %%isnull, ptr %s, ptr %%c
  %%h = call ptr @__kml_native_headered(ptr %%r)
  ret ptr %%h
}
define ptr @__kml_native_errno_desc(double %%e) {
entry:
  %%n = fptosi double %%e to i32
  %%d = call ptr @__kml_errno_desc(i32 %%n)
  %%isnull = icmp eq ptr %%d, null
  br i1 %%isnull, label %%os, label %%lib
os:
  %%s = call ptr @strerror(i32 %%n)
  %%hs = call ptr @__kml_native_headered(ptr %%s)
  ret ptr %%hs
lib:
  %%hd = call ptr @__kml_native_headered(ptr %%d)
  ret ptr %%hd
}
define double @__kml_native_uv_errno(double %%e) {
entry:
  %%n = fptosi double %%e to i32
  %%u = call i32 @__kml_uv_errno(i32 %%n)
  %%d = sitofp i32 %%u to double
  ret double %%d
}
define ptr @__kml_native_headered(ptr %%s) {
entry:
  %%len = call i64 @strlen(ptr %%s)
  %%n1 = add i64 %%len, 1
  %%out = call ptr @__kml_str_alloc(i64 %%n1)
  call ptr @memcpy(ptr %%out, ptr %%s, i64 %%n1)
  call void @__kml_str_finalize(ptr %%out)
  ret ptr %%out
}`, unknown))
}

// ensureNativeFsError defines __kml_native_fs_error(errno, syscall, path?):
// the Error Node's fs raises for a failed syscall (`ENOENT: no such file or
// directory, open 'x'`, with its code, errno, syscall and path).
func (e *Emitter) ensureNativeFsError() {
	if e.fnDecls["__kml_native_fs_error"] {
		return
	}
	e.fnDecls["__kml_native_fs_error"] = true
	e.ensureFsThrow()
	e.emitGlobal(`
define ptr @__kml_native_fs_error(double %errno, ptr %syscall, i1 zeroext %has_path, ptr %path) {
entry:
  %n = fptosi double %errno to i32
  %p = select i1 %has_path, ptr %path, ptr null
  %err = call ptr @__kml_fs_error_new(i32 %n, ptr %syscall, ptr %p, ptr null)
  ret ptr %err
}`)
}

// ensureNativeTLS compiles in tlssrc/tlshandle.c (and links libssl) for the
// TLS primitives over the pool's TCP handles.
func (e *Emitter) ensureNativeTLS() {
	e.ensureNativePool()
	e.usedTLSHandles = true
}
