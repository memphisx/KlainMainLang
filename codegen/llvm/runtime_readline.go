// runtime_readline.go — Node's interactive `readline`: createInterface over
// stdin, the 'line' event, question(query, cb), close() and the 'close' event.
//
// Non-blocking stdin (fd 0) is folded into the central select() event loop the
// same way child_process folds a spawned child's pipes (ADR-00322): three
// hooks — @__kml_rl_fdset_add, @__kml_rl_dispatch, @__kml_rl_keepalive — with
// no-op stubs (emitLoopTaskStubs) when the program never opens an interface.
// A single active interface is supported (one stdin), stored in a global.
//
// The dispatch reads whatever stdin has, buffers it, and emits one 'line' per
// newline (CR stripped); a pending question(cb) consumes the next line
// one-shot instead of firing 'line'. EOF flushes any unterminated final line,
// then fires 'close'.
package llvm

// %kml.rl: 0 ptr 'line' listener · 1 ptr 'close' listener · 2 ptr pending
// question callback (one-shot, null when none) · 3 i64 closed · 4 ptr line
// buffer {ptr,i64,i64}.
const rlStructIR = "{ ptr, ptr, ptr, i64, ptr }"

func (e *Emitter) ensureReadlineRuntime() {
	if e.usedReadlineRuntime {
		return
	}
	e.usedReadlineRuntime = true
	e.ensureMalloc()
	e.ensureCalloc()
	e.ensureRealloc()
	e.ensureFree()
	e.ensureMemcpy()
	e.ensureMemmove()
	e.ensureReadDecl()
	e.ensureWriteDecl()
	e.ensureStrlen()
	e.ensureFcntlDecl()
	e.ensureWorkerFdSetbit() // shared @__kml_worker_fd_setbit

	nonblock := httpNonblockFlag()

	e.ensureStrHeaderRuntime()
	e.emitGlobal("@__kml_rl_active = internal global ptr null, align 8")

	// __kml_rl_append(rl, src, n): append n bytes to the line buffer (field 4).
	e.emitGlobal(`
define void @__kml_rl_append(ptr %buf, ptr %src, i64 %n) {
entry:
  %data_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 0
  %len_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 1
  %cap_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 2
  %curlen = load i64, ptr %len_p, align 8
  %curcap = load i64, ptr %cap_p, align 8
  %curdata = load ptr, ptr %data_p, align 8
  %needed = add i64 %curlen, %n
  %needgrow = icmp sgt i64 %needed, %curcap
  br i1 %needgrow, label %grow, label %copy
grow:
  %cap2 = mul i64 %curcap, 2
  %pick1 = icmp sgt i64 %needed, %cap2
  %newcap_a = select i1 %pick1, i64 %needed, i64 %cap2
  %atleast64 = icmp sgt i64 %newcap_a, 64
  %newcap = select i1 %atleast64, i64 %newcap_a, i64 64
  %newdata = call ptr @realloc(ptr %curdata, i64 %newcap)
  store ptr %newdata, ptr %data_p, align 8
  store i64 %newcap, ptr %cap_p, align 8
  br label %copy
copy:
  %dataNow = load ptr, ptr %data_p, align 8
  %destptr = getelementptr i8, ptr %dataNow, i64 %curlen
  call ptr @memcpy(ptr %destptr, ptr %src, i64 %n)
  %newlen = add i64 %curlen, %n
  store i64 %newlen, ptr %len_p, align 8
  ret void
}`)

	// __kml_rl_emit(rl, line): a pending question callback consumes the line
	// one-shot; otherwise the 'line' listener fires.
	e.emitGlobal(`
define void @__kml_rl_emit(ptr %rl, ptr %line) {
entry:
  ; a closed interface (e.g. rl.close() called from a prior 'line' callback)
  ; delivers no further line/question events, even for already-buffered input.
  %clsd_p = getelementptr { ptr, ptr, ptr, i64, ptr }, ptr %rl, i32 0, i32 3
  %clsd = load i64, ptr %clsd_p, align 8
  %isclosed = icmp ne i64 %clsd, 0
  br i1 %isclosed, label %skip, label %go
skip:
  ret void
go:
  %q_p = getelementptr { ptr, ptr, ptr, i64, ptr }, ptr %rl, i32 0, i32 2
  %q = load ptr, ptr %q_p, align 8
  %hasq = icmp ne ptr %q, null
  br i1 %hasq, label %question, label %linev
question:
  store ptr null, ptr %q_p, align 8
  %qfp_p = getelementptr { ptr, ptr }, ptr %q, i32 0, i32 0
  %qfp = load ptr, ptr %qfp_p, align 8
  %qep_p = getelementptr { ptr, ptr }, ptr %q, i32 0, i32 1
  %qep = load ptr, ptr %qep_p, align 8
  call void %qfp(ptr %qep, ptr %line)
  ret void
linev:
  %l_p = getelementptr { ptr, ptr, ptr, i64, ptr }, ptr %rl, i32 0, i32 0
  %l = load ptr, ptr %l_p, align 8
  %hasl = icmp ne ptr %l, null
  br i1 %hasl, label %fire, label %ret
fire:
  %lfp_p = getelementptr { ptr, ptr }, ptr %l, i32 0, i32 0
  %lfp = load ptr, ptr %lfp_p, align 8
  %lep_p = getelementptr { ptr, ptr }, ptr %l, i32 0, i32 1
  %lep = load ptr, ptr %lep_p, align 8
  call void %lfp(ptr %lep, ptr %line)
  ret void
ret:
  ret void
}`)

	// __kml_rl_fireclose(rl): set closed and fire the 'close' listener once.
	e.emitGlobal(`
define void @__kml_rl_fireclose(ptr %rl) {
entry:
  %c_p = getelementptr { ptr, ptr, ptr, i64, ptr }, ptr %rl, i32 0, i32 3
  %already = load i64, ptr %c_p, align 8
  %isclosed = icmp ne i64 %already, 0
  br i1 %isclosed, label %ret, label %doclose
doclose:
  store i64 1, ptr %c_p, align 8
  %cl_p = getelementptr { ptr, ptr, ptr, i64, ptr }, ptr %rl, i32 0, i32 1
  %cl = load ptr, ptr %cl_p, align 8
  %hascl = icmp ne ptr %cl, null
  br i1 %hascl, label %fire, label %ret
fire:
  %cfp_p = getelementptr { ptr, ptr }, ptr %cl, i32 0, i32 0
  %cfp = load ptr, ptr %cfp_p, align 8
  %cep_p = getelementptr { ptr, ptr }, ptr %cl, i32 0, i32 1
  %cep = load ptr, ptr %cep_p, align 8
  call void %cfp(ptr %cep)
  ret void
ret:
  ret void
}`)

	// __kml_rl_extract1(rl): if the buffer holds a complete line, copy it out
	// (CR stripped, NUL-terminated), shift the buffer, and emit it. Returns 1
	// if a line was emitted, 0 otherwise.
	e.emitGlobal(`
define i1 @__kml_rl_extract1(ptr %rl) {
entry:
  %buf_p = getelementptr { ptr, ptr, ptr, i64, ptr }, ptr %rl, i32 0, i32 4
  %buf = load ptr, ptr %buf_p, align 8
  %data_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 0
  %len_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 1
  %data = load ptr, ptr %data_p, align 8
  %len = load i64, ptr %len_p, align 8
  %i = alloca i64, align 8
  store i64 0, ptr %i, align 8
  br label %scan
scan:
  %iv = load i64, ptr %i, align 8
  %inb = icmp slt i64 %iv, %len
  br i1 %inb, label %check, label %none
check:
  %bp = getelementptr i8, ptr %data, i64 %iv
  %b = load i8, ptr %bp, align 1
  %isnl = icmp eq i8 %b, 10
  br i1 %isnl, label %found, label %advance
advance:
  %inext = add i64 %iv, 1
  store i64 %inext, ptr %i, align 8
  br label %scan
found:
  ; line content is [0, iv); strip a trailing CR
  %linelen0 = load i64, ptr %i, align 8
  %hascr = icmp sgt i64 %linelen0, 0
  br i1 %hascr, label %ckcr, label %mkline
ckcr:
  %prev = sub i64 %linelen0, 1
  %pp = getelementptr i8, ptr %data, i64 %prev
  %pb = load i8, ptr %pp, align 1
  %iscr = icmp eq i8 %pb, 13
  %linelen = select i1 %iscr, i64 %prev, i64 %linelen0
  br label %mkline
mkline:
  %llen = phi i64 [ %linelen0, %found ], [ %linelen, %ckcr ]
  %line = call ptr @__kml_str_alloc(i64 %llen)
  call ptr @memcpy(ptr %line, ptr %data, i64 %llen)
  %term = getelementptr i8, ptr %line, i64 %llen
  store i8 0, ptr %term, align 1
  ; shift the buffer past the newline
  %consumed = add i64 %linelen0, 1
  %remain = sub i64 %len, %consumed
  %rest = getelementptr i8, ptr %data, i64 %consumed
  call ptr @memmove(ptr %data, ptr %rest, i64 %remain)
  store i64 %remain, ptr %len_p, align 8
  call void @__kml_rl_emit(ptr %rl, ptr %line)
  ret i1 1
none:
  ret i1 0
}`)

	// __kml_rl_dispatch(): drain stdin, emit complete lines, flush + close on EOF.
	e.emitGlobal(`
define void @__kml_rl_dispatch() {
entry:
  %rl = load ptr, ptr @__kml_rl_active, align 8
  %active = icmp ne ptr %rl, null
  br i1 %active, label %ckclosed, label %ret
ckclosed:
  %c_p = getelementptr { ptr, ptr, ptr, i64, ptr }, ptr %rl, i32 0, i32 3
  %closed = load i64, ptr %c_p, align 8
  %isclosed = icmp ne i64 %closed, 0
  br i1 %isclosed, label %ret, label %setup
setup:
  %buf_p = getelementptr { ptr, ptr, ptr, i64, ptr }, ptr %rl, i32 0, i32 4
  %buf = load ptr, ptr %buf_p, align 8
  %chunk = alloca [4096 x i8], align 1
  %chunkptr = getelementptr [4096 x i8], ptr %chunk, i32 0, i32 0
  %eof = alloca i1, align 1
  store i1 0, ptr %eof, align 1
  br label %readloop
readloop:
  %n = call i64 @read(i32 0, ptr %chunkptr, i64 4096)
  %hasdata = icmp sgt i64 %n, 0
  br i1 %hasdata, label %append, label %ckeof
append:
  call void @__kml_rl_append(ptr %buf, ptr %chunkptr, i64 %n)
  br label %readloop
ckeof:
  %iseof = icmp eq i64 %n, 0
  br i1 %iseof, label %seteof, label %extract
seteof:
  store i1 1, ptr %eof, align 1
  br label %extract
extract:
  %got = call i1 @__kml_rl_extract1(ptr %rl)
  br i1 %got, label %extract, label %posteof
posteof:
  %e = load i1, ptr %eof, align 1
  br i1 %e, label %flush, label %ret
flush:
  ; emit any unterminated trailing content as a final line, then close
  %len_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 1
  %len = load i64, ptr %len_p, align 8
  %hastail = icmp sgt i64 %len, 0
  br i1 %hastail, label %tail, label %doclose
tail:
  %data_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 0
  %data = load ptr, ptr %data_p, align 8
  %tailline = call ptr @__kml_str_alloc(i64 %len)
  call ptr @memcpy(ptr %tailline, ptr %data, i64 %len)
  %tterm = getelementptr i8, ptr %tailline, i64 %len
  store i8 0, ptr %tterm, align 1
  store i64 0, ptr %len_p, align 8
  call void @__kml_rl_emit(ptr %rl, ptr %tailline)
  br label %doclose
doclose:
  call void @__kml_rl_fireclose(ptr %rl)
  br label %ret
ret:
  ret void
}`)

	// __kml_rl_fdset_add(fdset, maxfd): add stdin (fd 0) while open.
	e.emitGlobal(`
define i1 @__kml_rl_fdset_add(ptr %fdset, ptr %maxfd) {
entry:
  %rl = load ptr, ptr @__kml_rl_active, align 8
  %active = icmp ne ptr %rl, null
  br i1 %active, label %ckclosed, label %no
ckclosed:
  %c_p = getelementptr { ptr, ptr, ptr, i64, ptr }, ptr %rl, i32 0, i32 3
  %closed = load i64, ptr %c_p, align 8
  %isclosed = icmp ne i64 %closed, 0
  br i1 %isclosed, label %no, label %add
add:
  call void @__kml_worker_fd_setbit(i32 0, ptr %fdset, ptr %maxfd)
  ret i1 0
no:
  ret i1 0
}`)

	// __kml_rl_keepalive(): an open interface holds the loop alive.
	e.emitGlobal(`
define i1 @__kml_rl_keepalive() {
entry:
  %rl = load ptr, ptr @__kml_rl_active, align 8
  %active = icmp ne ptr %rl, null
  br i1 %active, label %ckclosed, label %no
ckclosed:
  %c_p = getelementptr { ptr, ptr, ptr, i64, ptr }, ptr %rl, i32 0, i32 3
  %closed = load i64, ptr %c_p, align 8
  %isopen = icmp eq i64 %closed, 0
  ret i1 %isopen
no:
  ret i1 0
}`)

	// __kml_rl_create(): allocate the interface, set stdin non-blocking, store
	// it as the active interface.
	e.emitGlobal(`
define ptr @__kml_rl_create() {
entry:
  %rl = call ptr @calloc(i64 1, i64 40)
  %buf = call ptr @calloc(i64 1, i64 24)
  %buf_p = getelementptr { ptr, ptr, ptr, i64, ptr }, ptr %rl, i32 0, i32 4
  store ptr %buf, ptr %buf_p, align 8
  %fl = call i32 (i32, i32, ...) @fcntl(i32 0, i32 3)
  %fln = or i32 %fl, ` + itoaRL(nonblock) + `
  call i32 (i32, i32, ...) @fcntl(i32 0, i32 4, i32 %fln)
  store ptr %rl, ptr @__kml_rl_active, align 8
  ret ptr %rl
}`)

	// __kml_rl_question(rl, query): write the prompt, arm the one-shot callback.
	e.emitGlobal(`
define void @__kml_rl_question(ptr %rl, ptr %query, ptr %cb) {
entry:
  %qlen = call i64 @strlen(ptr %query)
  call i64 @write(i32 1, ptr %query, i64 %qlen)
  %q_p = getelementptr { ptr, ptr, ptr, i64, ptr }, ptr %rl, i32 0, i32 2
  store ptr %cb, ptr %q_p, align 8
  ret void
}`)

	// __kml_rl_close(rl): fire 'close' and mark closed.
	e.emitGlobal(`
define void @__kml_rl_close(ptr %rl) {
entry:
  call void @__kml_rl_fireclose(ptr %rl)
  ret void
}`)
}

// itoaRL renders a small non-negative int for inlining into an IR literal.
func itoaRL(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
