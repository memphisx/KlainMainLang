// runtime_fs_stream.go — the runtime backing fs.createReadStream /
// fs.createWriteStream (TDD-00108).
//
// A read stream: fopen("rb") on the loop thread; the chunked, demand-driven read
// itself runs on the thread pool (threadpoolsrc/klainpool.c, TDD-00186).
// A write stream: fopen once, and two sink thunks the Node Writable drives —
// a per-chunk fwrite and an fclose on close. Both throw a catchable Error (via
// @__kml_fs_throw) if the file can't be opened, matching readFileSync.
package llvm

import "fmt"

// ensureFsOpenRead declares @__kml_fs_open_read(path) -> FILE*: fopen("rb"),
// throwing the Node-shaped Error synchronously on a missing file (matching the
// eager path's throw-at-creation), and returning the open handle otherwise. The
// pool then reads from it off-thread (TDD-00186).
func (e *Emitter) ensureFsOpenRead() {
	if e.usedFsOpenRead {
		return
	}
	e.usedFsOpenRead = true
	e.ensureFsThrow()
	e.ensureFopen()
	modePtr := e.internString("rb")
	opDescPtr := e.internString("cannot open file for reading")
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_fs_open_read(ptr %%path) {
entry:
  %%f = call ptr @fopen(ptr %%path, ptr %s)
  %%isnull = icmp eq ptr %%f, null
  br i1 %%isnull, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable
ok:
  ret ptr %%f
}`, modePtr, opDescPtr, e.internString("open")))
}

// ensureFsWriteStream declares the createWriteStream runtime: an open helper
// (fopen, throw on failure, return the FILE*) and the two Writable sink thunks
// (fwrite one chunk; fclose on close). The FILE* is the closure env of both
// sinks (buildBuiltinClosure), so no separate handle struct is needed.
func (e *Emitter) ensureFsWriteStream() {
	if e.usedFsWriteStream {
		return
	}
	e.usedFsWriteStream = true
	e.ensureFsThrow()
	e.ensureFopen()
	e.ensureFclose()
	e.ensureFwrite()
	e.ensureStrlen()
	wbPtr := e.internString("wb")
	abPtr := e.internString("ab")
	opDescPtr := e.internString("cannot open file for writing")

	// @__kml_fs_open_write(path, append) -> FILE*  (throws on failure).
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_fs_open_write(ptr %%path, i64 %%append) {
entry:
  %%isapp = icmp ne i64 %%append, 0
  %%mode = select i1 %%isapp, ptr %s, ptr %s
  %%f = call ptr @fopen(ptr %%path, ptr %%mode)
  %%isnull = icmp eq ptr %%f, null
  br i1 %%isnull, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable
ok:
  ret ptr %%f
}`, abPtr, wbPtr, opDescPtr, e.internString("open")))

	// Write sink (wstream field 9 ABI): ptr(ptr env, i64 v0, i64 v1). env is the
	// FILE*. v0 is the string chunk ptr; write strlen(v0) bytes. Returns null
	// (synchronous completion — no backpressure promise).
	e.emitGlobal(`
define ptr @__kml_fs_stream_write(ptr %env, i64 %v0, i64 %v1) {
entry:
  %s = inttoptr i64 %v0 to ptr
  %len = call i64 @strlen(ptr %s)
  %ig = call i64 @fwrite(ptr %s, i64 1, i64 %len, ptr %env)
  ret ptr null
}`)

	// Close sink (wstream field 10 ABI): ptr(ptr env). fclose the FILE*.
	e.emitGlobal(`
define ptr @__kml_fs_stream_close(ptr %env) {
entry:
  %ig = call i32 @fclose(ptr %env)
  ret ptr null
}`)
}
