// runtime_stdin.go — Node's streaming `process.stdin`: the 'data' and 'end'
// events over fd 0, as a flowing Readable.
//
// Like `readline` (ADR-00323), non-blocking stdin (fd 0) folds into the central
// select() event loop via three hooks — @__kml_stdin_fdset_add,
// @__kml_stdin_dispatch, @__kml_stdin_keepalive — with no-op stubs
// (emitLoopTaskStubs) when the program never touches process.stdin. A single
// handle is stored in a global (there is one stdin).
//
// The dispatch drains whatever stdin has and fires 'data' once per read chunk
// (delivered as a NUL-terminated string copy — UTF-8 text, not a Buffer); on
// EOF it fires 'end' once. Attaching a 'data' listener is what puts the stream
// in flowing mode, so the loop only reads (and stays alive) once one is present.
package llvm

// %kml.stdin: 0 ptr 'data' listener · 1 ptr 'end' listener · 2 i64 ended flag.
const stdinStructIR = "{ ptr, ptr, i64 }"

func (e *Emitter) ensureStdinRuntime() {
	if e.usedStdinRuntime {
		return
	}
	e.usedStdinRuntime = true
	e.ensureMalloc()
	e.ensureCalloc()
	e.ensureMemcpy()
	e.ensureReadDecl()
	e.ensureFcntlDecl()
	e.ensureWorkerFdSetbit() // shared @__kml_worker_fd_setbit

	nonblock := httpNonblockFlag()

	e.emitGlobal("@__kml_stdin_active = internal global ptr null, align 8")

	// __kml_stdin_create(): idempotent — allocate the handle, set fd 0
	// non-blocking, and store it as the active handle on first access; return
	// the (existing or new) active handle. process.stdin evaluates to this.
	e.emitGlobal(`
define ptr @__kml_stdin_create() {
entry:
  %cur = load ptr, ptr @__kml_stdin_active, align 8
  %have = icmp ne ptr %cur, null
  br i1 %have, label %ret, label %make
make:
  %h = call ptr @calloc(i64 1, i64 24)
  %fl = call i32 (i32, i32, ...) @fcntl(i32 0, i32 3)
  %fln = or i32 %fl, ` + itoaRL(nonblock) + `
  call i32 (i32, i32, ...) @fcntl(i32 0, i32 4, i32 %fln)
  store ptr %h, ptr @__kml_stdin_active, align 8
  ret ptr %h
ret:
  ret ptr %cur
}`)

	// __kml_stdin_dispatch(): drain stdin, firing 'data' per chunk; on EOF fire
	// 'end' once. A read of -1 (EAGAIN) simply means "nothing more right now".
	e.emitGlobal(`
define void @__kml_stdin_dispatch() {
entry:
  %h = load ptr, ptr @__kml_stdin_active, align 8
  %active = icmp ne ptr %h, null
  br i1 %active, label %ckdone, label %ret
ckdone:
  %end_p = getelementptr { ptr, ptr, i64 }, ptr %h, i32 0, i32 2
  %ended = load i64, ptr %end_p, align 8
  %isdone = icmp ne i64 %ended, 0
  br i1 %isdone, label %ret, label %ckflow
ckflow:
  ; only read once a 'data' listener has put the stream in flowing mode.
  %d_p = getelementptr { ptr, ptr, i64 }, ptr %h, i32 0, i32 0
  %d = load ptr, ptr %d_p, align 8
  %flowing = icmp ne ptr %d, null
  br i1 %flowing, label %readloop, label %ret
readloop:
  %chunk = alloca [4096 x i8], align 1
  %cp = getelementptr [4096 x i8], ptr %chunk, i32 0, i32 0
  %n = call i64 @read(i32 0, ptr %cp, i64 4096)
  %hasdata = icmp sgt i64 %n, 0
  br i1 %hasdata, label %fire, label %ckeof
fire:
  ; deliver a NUL-terminated string copy of the chunk to the 'data' listener.
  %sz = add i64 %n, 1
  %s = call ptr @malloc(i64 %sz)
  call ptr @memcpy(ptr %s, ptr %cp, i64 %n)
  %term = getelementptr i8, ptr %s, i64 %n
  store i8 0, ptr %term, align 1
  %dl_p = getelementptr { ptr, ptr, i64 }, ptr %h, i32 0, i32 0
  %dl = load ptr, ptr %dl_p, align 8
  %dfp_p = getelementptr { ptr, ptr }, ptr %dl, i32 0, i32 0
  %dfp = load ptr, ptr %dfp_p, align 8
  %dep_p = getelementptr { ptr, ptr }, ptr %dl, i32 0, i32 1
  %dep = load ptr, ptr %dep_p, align 8
  call void %dfp(ptr %dep, ptr %s)
  br label %readloop
ckeof:
  %iseof = icmp eq i64 %n, 0
  br i1 %iseof, label %doend, label %ret
doend:
  store i64 1, ptr %end_p, align 8
  %el_p = getelementptr { ptr, ptr, i64 }, ptr %h, i32 0, i32 1
  %el = load ptr, ptr %el_p, align 8
  %hasel = icmp ne ptr %el, null
  br i1 %hasel, label %fireend, label %ret
fireend:
  %efp_p = getelementptr { ptr, ptr }, ptr %el, i32 0, i32 0
  %efp = load ptr, ptr %efp_p, align 8
  %eep_p = getelementptr { ptr, ptr }, ptr %el, i32 0, i32 1
  %eep = load ptr, ptr %eep_p, align 8
  call void %efp(ptr %eep)
  ret void
ret:
  ret void
}`)

	// __kml_stdin_fdset_add(fdset, maxfd): add stdin (fd 0) while open + flowing.
	e.emitGlobal(`
define i1 @__kml_stdin_fdset_add(ptr %fdset, ptr %maxfd) {
entry:
  %h = load ptr, ptr @__kml_stdin_active, align 8
  %active = icmp ne ptr %h, null
  br i1 %active, label %ckdone, label %no
ckdone:
  %end_p = getelementptr { ptr, ptr, i64 }, ptr %h, i32 0, i32 2
  %ended = load i64, ptr %end_p, align 8
  %isdone = icmp ne i64 %ended, 0
  br i1 %isdone, label %no, label %ckflow
ckflow:
  %d_p = getelementptr { ptr, ptr, i64 }, ptr %h, i32 0, i32 0
  %d = load ptr, ptr %d_p, align 8
  %flowing = icmp ne ptr %d, null
  br i1 %flowing, label %add, label %no
add:
  call void @__kml_worker_fd_setbit(i32 0, ptr %fdset, ptr %maxfd)
  ret i1 0
no:
  ret i1 0
}`)

	// __kml_stdin_keepalive(): an open, flowing stream holds the loop alive.
	e.emitGlobal(`
define i1 @__kml_stdin_keepalive() {
entry:
  %h = load ptr, ptr @__kml_stdin_active, align 8
  %active = icmp ne ptr %h, null
  br i1 %active, label %ckdone, label %no
ckdone:
  %end_p = getelementptr { ptr, ptr, i64 }, ptr %h, i32 0, i32 2
  %ended = load i64, ptr %end_p, align 8
  %isdone = icmp ne i64 %ended, 0
  br i1 %isdone, label %no, label %ckflow
ckflow:
  %d_p = getelementptr { ptr, ptr, i64 }, ptr %h, i32 0, i32 0
  %d = load ptr, ptr %d_p, align 8
  %flowing = icmp ne ptr %d, null
  ret i1 %flowing
no:
  ret i1 0
}`)
}
