// runtime_fs_stream.go — the runtime backing fs.createReadStream /
// fs.createWriteStream (TDD-00108).
//
// A read stream: fopen("rb"), read the file in highWaterMark-sized chunks with
// fread, enqueue each as a string chunk into an already-alloc'd WHATWG rstream,
// then fclose — eager read-to-EOF, no select() (a regular file is always ready).
// A write stream: fopen once, and two sink thunks the Node Writable drives —
// a per-chunk fwrite and an fclose on close. Both throw a catchable Error (via
// @__kml_fs_throw) if the file can't be opened, matching readFileSync.
package llvm

import "fmt"

// ensureFsReadStream declares @__kml_fs_read_stream(path, rstream, hwm): drains
// the file into the rstream as string chunks. The rstream is closed by the
// caller (emit_fs_stream.go) after this returns.
func (e *Emitter) ensureFsReadStream() {
	if e.usedFsReadStream {
		return
	}
	e.usedFsReadStream = true
	e.ensureFsThrow()
	e.ensureMalloc()
	e.ensureFree()
	e.ensureFopen()
	e.ensureFclose()
	e.ensureStreamRuntime() // @__kml_rs_enqueue
	e.ensureFread()
	modePtr := e.internString("rb")
	opDescPtr := e.internString("cannot open file for reading")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_read_stream(ptr %%path, ptr %%rs, i64 %%hwm) {
entry:
  %%f = call ptr @fopen(ptr %%path, ptr %s)
  %%isnull = icmp eq ptr %%f, null
  br i1 %%isnull, label %%fail, label %%loop
fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable
loop:
  %%sz = add i64 %%hwm, 1
  %%buf = call ptr @malloc(i64 %%sz)
  %%n = call i64 @fread(ptr %%buf, i64 1, i64 %%hwm, ptr %%f)
  %%has = icmp sgt i64 %%n, 0
  br i1 %%has, label %%enq, label %%done
enq:
  %%term = getelementptr i8, ptr %%buf, i64 %%n
  store i8 0, ptr %%term, align 1
  %%pi = ptrtoint ptr %%buf to i64
  %%ig = call i64 @__kml_rs_enqueue(ptr %%rs, i64 %%pi, i64 0)
  br label %%loop
done:
  call void @free(ptr %%buf)
  call i32 @fclose(ptr %%f)
  ret void
}`, modePtr, opDescPtr))
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
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable
ok:
  ret ptr %%f
}`, abPtr, wbPtr, opDescPtr))

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
