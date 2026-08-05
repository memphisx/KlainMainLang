package llvm

import (
	"fmt"
	"runtime"
)

// fiberStackBytes is the size of each connection fiber's own malloc'd stack
// (ucontext.h-based, see ensureFiberRuntime/__kml_http_append_conn). Was
// 64KB (65536) originally; confirmed too small specifically under `-mm=gc`
// — this stack is itself GC_malloc'd (routed through the Boehm shim like
// every other allocation), and a collection triggered mid-fiber runs
// Boehm's own mark/sweep machinery *on that same stack*, silently
// overflowing it into adjacent heap memory with no crash and no signal:
// found via a real, hard-to-reproduce intermittent hang under
// http.listen({workers: N}) clustering + concurrent load in -mm=gc builds
// (a worker would receive a complete HTTP request in one read(), then
// immediately issue an inexplicable second read() with a corrupted byte
// count instead of responding). Bisected by directly comparing failure
// rates at the old 64KB against several larger sizes; 1MB (1048576)
// eliminated it in 200/200 repeated runs under conditions that reliably
// reproduced the bug within 40-150 runs at 64KB. Applied unconditionally
// (both manual and gc mode) rather than gating by memory mode, to avoid a
// mode-specific footgun if a manual-mode handler ever needs comparable
// depth. See docs/adr/ADR-00100.md.
const fiberStackBytes = 1024 * 1024

// httpSockConstants returns the platform-specific setsockopt() level/option
// values for SOL_SOCKET/SO_REUSEADDR — unlike AF_INET (2) and SOCK_STREAM
// (1), which are the same numeric value on every POSIX target this project
// builds for, these two genuinely differ: Linux defines SOL_SOCKET=1,
// SO_REUSEADDR=2, while Darwin/BSD define SOL_SOCKET=0xffff, SO_REUSEADDR=4.
// Same Go-side-runtime.GOOS-branch approach as monotonicClockID()/
// errnoAccessor() above — this compiler always builds and runs on the same
// host, so a compile-time Go-side branch is sufficient, no IR-level
// conditional needed.
func httpSockConstants() (solSocket, soReuseAddr int) {
	if runtime.GOOS == "darwin" {
		return 0xffff, 4
	}
	return 1, 2
}

// httpSockaddrFamilyBytes returns the first two bytes of a struct
// sockaddr_in, which differ by platform even though the struct's total
// size (16 bytes) and every field after it are identical: Linux packs
// sin_family as a plain 2-byte field (family=2 for AF_INET, low byte
// first on this project's little-endian targets); Darwin/BSD instead
// split those same two bytes into sin_len (=16, the struct's own total
// size) followed by a 1-byte sin_family. Port and address fields (offset
// 2 and 4) are identical on both, so only these two bytes need branching.
func httpSockaddrFamilyBytes() (byte0, byte1 int) {
	if runtime.GOOS == "darwin" {
		return 16, 2 // sin_len=16, sin_family=AF_INET
	}
	return 2, 0 // sin_family=AF_INET as a little-endian i16
}

// httpNonblockFlag returns O_NONBLOCK's numeric value — another genuine
// platform difference (Darwin: 0x4, Linux: 0x800 on both x86-64 and arm64,
// the two architectures this project targets), verified on this machine via
// a throwaway C probe (`printf("%x", O_NONBLOCK)`) rather than trusted from
// memory, matching every other libc constant this project hardcodes. Used
// by the event loop's accept path to make a freshly-accepted connection's
// fd non-blocking before handing it to its own fiber.
func httpNonblockFlag() int {
	if runtime.GOOS == "darwin" {
		return 0x4
	}
	return 0x800
}

// httpEagainErrno returns EAGAIN/EWOULDBLOCK's numeric value (35 on Darwin;
// 11 on Linux, where EAGAIN and EWOULDBLOCK are the same value — both
// verified the same way as httpNonblockFlag). A per-connection fiber's read
// loop checks the current errno against this after a failed non-blocking
// read to distinguish "no data yet, yield and retry later" from a real
// error.
func httpEagainErrno() int {
	if runtime.GOOS == "darwin" {
		return 35
	}
	return 11
}

// ucontextLayout returns sizeof(ucontext_t) and the byte offsets of
// uc_stack.ss_sp / uc_stack.ss_size / uc_link needed to hand-build one (see
// ensureFiberRuntime) — a real, confirmed platform difference found the
// hard way: this project's own CI (GitHub Actions' ubuntu-latest, x86-64)
// hung/reset connections under the fiber-based event loop until this was
// fixed, because the original implementation only ever verified these
// numbers on this dev machine (arm64 Darwin, sizeof 880) and assumed they'd
// carry over. They do not: Linux's glibc ucontext_t is a completely
// different struct, and even differs *between Linux architectures*
// (x86-64: 968 bytes; arm64: 4560 bytes — verified directly via a
// throwaway sizeof/offsetof C probe compiled and run in Docker containers
// for each target, `docker run --platform linux/amd64|linux/arm64
// ubuntu:24.04`, the same "never trust from memory" standard every other
// platform constant in this codebase already follows), while the four
// offsets happen to be identical across both Linux architectures (only the
// struct's total size differs, presumably due to a differently-sized
// register/FPU save area later in the struct) but are still completely
// different from Darwin's. Undersizing this buffer on Linux meant
// getcontext/makecontext/swapcontext wrote past the end of a too-small
// malloc'd (or, for @__kml_main_ctx, global) buffer — silent heap/global
// corruption, manifesting unpredictably depending on what happened to be
// laid out next in memory (which is exactly what the observed symptoms —
// connection resets, hangs — looked like).
func ucontextLayout() (size, ssSpOff, ssSizeOff, ucLinkOff int64) {
	if runtime.GOOS == "darwin" {
		return 880, 8, 16, 32
	}
	// Linux (glibc): offsets are identical across architectures; size isn't.
	if runtime.GOARCH == "arm64" {
		return 4560, 16, 32, 8
	}
	return 968, 16, 32, 8 // amd64 and other 64-bit Linux targets
}

// ensureHTTPThrow declares __kml_http_throw: builds "<opdesc>: <reason>"
// from the current errno via strerror() and throws it as a catchable Error
// — same shape as ensureFsThrow, just without a path argument (a bind/listen
// failure has no associated file path to report).
func (e *Emitter) ensureHTTPThrow() {
	if e.usedHTTPThrow {
		return
	}
	e.usedHTTPThrow = true
	e.ensureMalloc()
	e.ensureStrlen()
	e.ensureSprintf()
	e.ensureExceptionHelpers()
	e.ensureErrnoAccessor()
	e.ensureStrerror()
	fmtPtr := e.internString("%s: %s")
	errNamePtr := e.internString("Error")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_http_throw(ptr %%opdesc) {
entry:
  %%errno_ptr = call ptr @%s()
  %%errno_val = load i32, ptr %%errno_ptr, align 4
  %%errmsg = call ptr @strerror(i32 %%errno_val)
  %%len_op = call i64 @strlen(ptr %%opdesc)
  %%len_err = call i64 @strlen(ptr %%errmsg)
  %%sum = add i64 %%len_op, %%len_err
  %%bufsize = add i64 %%sum, 8
  %%buf = call ptr @malloc(i64 %%bufsize)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%buf, ptr %s, ptr %%opdesc, ptr %%errmsg)
  %%errobj = call ptr @malloc(i64 24)
  %%errobj.kind = getelementptr { i64, ptr, ptr }, ptr %%errobj, i32 0, i32 0
  store i64 0, ptr %%errobj.kind, align 8
  %%errobj.msg = getelementptr { i64, ptr, ptr }, ptr %%errobj, i32 0, i32 1
  store ptr %%buf, ptr %%errobj.msg, align 8
  %%errobj.name = getelementptr { i64, ptr, ptr }, ptr %%errobj, i32 0, i32 2
  store ptr %s, ptr %%errobj.name, align 8
  call void @__kml_throw(ptr %%errobj)
  ret void
}`, errnoAccessor(), fmtPtr, errNamePtr))
}

// ensureSplitFirst declares __kml_split_first(ptr s, ptr sep) -> {ptr, ptr},
// splitting on the FIRST occurrence of sep only — unlike ensureStringSplit's
// @__kml_split (which splits on every occurrence), this is what parsing a
// single "Key: Value" header line or a "path?query" URL needs, since a
// header's value or a query string can itself legitimately contain the
// separator again. `after` aliases into s itself (fine — every call site
// below keeps s alive at least as long as `after` is used); `before` is a
// fresh malloc'd+NUL-terminated copy. `after` is null if sep isn't found.
func (e *Emitter) ensureSplitFirst() {
	if e.usedSplitFirst {
		return
	}
	e.usedSplitFirst = true
	e.ensureStrstr()
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	e.emitGlobal(`
define {ptr, ptr} @__kml_split_first(ptr %s, ptr %sep) {
entry:
  %found = call ptr @strstr(ptr %s, ptr %sep)
  %hit = icmp ne ptr %found, null
  br i1 %hit, label %split, label %nosep
nosep:
  %r0 = insertvalue {ptr, ptr} undef, ptr %s, 0
  %r1 = insertvalue {ptr, ptr} %r0, ptr null, 1
  ret {ptr, ptr} %r1
split:
  %sep_len = call i64 @strlen(ptr %sep)
  %s_int = ptrtoint ptr %s to i64
  %f_int = ptrtoint ptr %found to i64
  %before_len = sub i64 %f_int, %s_int
  %alloc = add i64 %before_len, 1
  %before_buf = call ptr @malloc(i64 %alloc)
  call ptr @memcpy(ptr %before_buf, ptr %s, i64 %before_len)
  %nullp = getelementptr i8, ptr %before_buf, i64 %before_len
  store i8 0, ptr %nullp, align 1
  %after = getelementptr i8, ptr %found, i64 %sep_len
  %r2 = insertvalue {ptr, ptr} undef, ptr %before_buf, 0
  %r3 = insertvalue {ptr, ptr} %r2, ptr %after, 1
  ret {ptr, ptr} %r3
}`)
}

// ensureHTTPParseHeaders declares __kml_http_parse_headers(ptr headerBlock,
// ptr map): splits headerBlock (already NUL-terminated exactly at the end
// of the header block by the caller — see buildHTTPDispatcher) on "\r\n"
// into independent per-line copies (safe even after the source read buffer
// later moves via realloc — ensureStringSplit's @__kml_split always
// produces fresh copies, never aliases), each line split on the FIRST
// "': '" via __kml_split_first (a header value can itself legally contain
// ": "), lowercases the header name (case-insensitive per HTTP semantics)
// and trims the value, then __kml_map_str_set's it. A line with no "': '"
// is silently skipped — malformed input, not something to fail the whole
// request over.
func (e *Emitter) ensureHTTPParseHeaders() {
	if e.usedHTTPParseHeaders {
		return
	}
	e.usedHTTPParseHeaders = true
	e.ensureStringSplit()
	e.ensureSplitFirst()
	e.ensureStringToLower()
	e.ensureStringTrim()
	e.ensureMapStrHelpers()
	e.ensureStrlen()
	crlf := e.internString("\r\n")
	colonSp := e.internString(": ")
	e.emitGlobal(`
define void @__kml_http_parse_headers(ptr %headerBlock, ptr %map) {
entry:
  %lines = call {ptr, i64} @__kml_split(ptr %headerBlock, ptr ` + crlf + `)
  %ldata = extractvalue {ptr, i64} %lines, 0
  %lcount = extractvalue {ptr, i64} %lines, 1
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %i1, %next ]
  %done = icmp sge i64 %i, %lcount
  br i1 %done, label %ret, label %body
body:
  %lslot = getelementptr ptr, ptr %ldata, i64 %i
  %line = load ptr, ptr %lslot, align 8
  %llen = call i64 @strlen(ptr %line)
  %empty = icmp eq i64 %llen, 0
  br i1 %empty, label %next, label %split
split:
  %kv = call {ptr, ptr} @__kml_split_first(ptr %line, ptr ` + colonSp + `)
  %val = extractvalue {ptr, ptr} %kv, 1
  %hasval = icmp ne ptr %val, null
  br i1 %hasval, label %store, label %next
store:
  %key = extractvalue {ptr, ptr} %kv, 0
  %keylower = call ptr @__kml_tolower(ptr %key)
  %valtrim = call ptr @__kml_trim(ptr %val)
  %valint = ptrtoint ptr %valtrim to i64
  call void @__kml_map_str_set(ptr %map, ptr %keylower, i64 %valint)
  br label %next
next:
  %i1 = add i64 %i, 1
  br label %loop
ret:
  ret void
}`)
}

// ensureHTTPParseQuery declares __kml_http_parse_query(ptr q, ptr map):
// splits q (the raw "a=b&c=d" tail of a request path after "?") on "&"
// (every occurrence — correct here, since each &-delimited segment is a
// whole pair), then each pair on the FIRST "=" via __kml_split_first (a
// value may itself legally contain "="). Both key and value are
// percent-decoded via the same __kml_decode_uri_component
// decodeURIComponent itself uses. A bare flag with no "=" (e.g. "?debug")
// stores an empty string rather than a null value.
func (e *Emitter) ensureHTTPParseQuery() {
	if e.usedHTTPParseQuery {
		return
	}
	e.usedHTTPParseQuery = true
	e.ensureStringSplit()
	e.ensureSplitFirst()
	e.ensureDecodeURIComponent()
	e.ensureMapStrHelpers()
	e.ensureStrlen()
	e.ensureMalloc()
	amp := e.internString("&")
	eq := e.internString("=")
	e.emitGlobal(`
define void @__kml_http_parse_query(ptr %q, ptr %map) {
entry:
  %pairs = call {ptr, i64} @__kml_split(ptr %q, ptr ` + amp + `)
  %pdata = extractvalue {ptr, i64} %pairs, 0
  %pcount = extractvalue {ptr, i64} %pairs, 1
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %i1, %next ]
  %done = icmp sge i64 %i, %pcount
  br i1 %done, label %ret, label %body
body:
  %pslot = getelementptr ptr, ptr %pdata, i64 %i
  %pair = load ptr, ptr %pslot, align 8
  %plen = call i64 @strlen(ptr %pair)
  %empty = icmp eq i64 %plen, 0
  br i1 %empty, label %next, label %split
split:
  %kv = call {ptr, ptr} @__kml_split_first(ptr %pair, ptr ` + eq + `)
  %keyraw = extractvalue {ptr, ptr} %kv, 0
  %valraw = extractvalue {ptr, ptr} %kv, 1
  %hasval = icmp ne ptr %valraw, null
  br i1 %hasval, label %usereal, label %useempty
useempty:
  %eb = call ptr @malloc(i64 1)
  store i8 0, ptr %eb, align 1
  br label %store
usereal:
  br label %store
store:
  %val = phi ptr [ %valraw, %usereal ], [ %eb, %useempty ]
  %keydec = call ptr @__kml_decode_uri_component(ptr %keyraw)
  %valdec = call ptr @__kml_decode_uri_component(ptr %val)
  %valint = ptrtoint ptr %valdec to i64
  call void @__kml_map_str_set(ptr %map, ptr %keydec, i64 %valint)
  br label %next
next:
  %i1 = add i64 %i, 1
  br label %loop
ret:
  ret void
}`)
}

// ensureHTTPSerializeHeaders declares __kml_http_serialize_headers(ptr map)
// -> ptr: builds a response's optional extra header block ("Key: Value\r\n"
// per entry, concatenated) from a Map<string,string>, for
// __kml_http_send_response's extraHeaders parameter. Two-pass
// (size-then-fill), mirroring __kml_split's own cnt_loop/fill_loop idiom.
// Only pulled in when a handler's return type actually declares a
// `headers` field — see emitHTTPListen.
func (e *Emitter) ensureHTTPSerializeHeaders() {
	if e.usedHTTPSerializeHeaders {
		return
	}
	e.usedHTTPSerializeHeaders = true
	e.ensureMapStrHelpers()
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureSprintf()
	hdrFmt := e.internString("%s: %s\r\n")
	e.emitGlobal(`
define ptr @__kml_http_serialize_headers(ptr %map) {
entry:
  %keys = call {ptr, i64} @__kml_map_str_keys(ptr %map)
  %kdata = extractvalue {ptr, i64} %keys, 0
  %kcount = extractvalue {ptr, i64} %keys, 1
  %vals = call {ptr, i64} @__kml_map_str_vals(ptr %map)
  %vdata = extractvalue {ptr, i64} %vals, 0
  br label %sizeloop
sizeloop:
  %si = phi i64 [ 0, %entry ], [ %si1, %sizebody ]
  %total = phi i64 [ 0, %entry ], [ %total1, %sizebody ]
  %sdone = icmp sge i64 %si, %kcount
  br i1 %sdone, label %alloc, label %sizebody
sizebody:
  %kslot = getelementptr ptr, ptr %kdata, i64 %si
  %kptr = load ptr, ptr %kslot, align 8
  %klen = call i64 @strlen(ptr %kptr)
  %vslot = getelementptr i64, ptr %vdata, i64 %si
  %vint = load i64, ptr %vslot, align 8
  %vptr = inttoptr i64 %vint to ptr
  %vlen = call i64 @strlen(ptr %vptr)
  %line = add i64 %klen, %vlen
  %line2 = add i64 %line, 4
  %total1 = add i64 %total, %line2
  %si1 = add i64 %si, 1
  br label %sizeloop
alloc:
  %bufsz = add i64 %total, 1
  %buf = call ptr @malloc(i64 %bufsz)
  br label %fillloop
fillloop:
  %fi = phi i64 [ 0, %alloc ], [ %fi1, %fillbody ]
  %cursor = phi ptr [ %buf, %alloc ], [ %cursor1, %fillbody ]
  %fdone = icmp sge i64 %fi, %kcount
  br i1 %fdone, label %ret, label %fillbody
fillbody:
  %fkslot = getelementptr ptr, ptr %kdata, i64 %fi
  %fkptr = load ptr, ptr %fkslot, align 8
  %fvslot = getelementptr i64, ptr %vdata, i64 %fi
  %fvint = load i64, ptr %fvslot, align 8
  %fvptr = inttoptr i64 %fvint to ptr
  %n = call i32 (ptr, ptr, ...) @sprintf(ptr %cursor, ptr ` + hdrFmt + `, ptr %fkptr, ptr %fvptr)
  %n64 = sext i32 %n to i64
  %cursor1 = getelementptr i8, ptr %cursor, i64 %n64
  %fi1 = add i64 %fi, 1
  br label %fillloop
ret:
  ret ptr %buf
}`)
}

// ensureHTTPRuntime declares everything http.listen needs: raw POSIX socket
// primitives, a bind-and-listen helper that throws a catchable Error on
// failure, an accept-and-parse-request-line helper, a send-response-and-close
// helper, and the generalized event loop (TDD-00006 Part 1) that lets the
// listening socket's readiness and the existing timer queue (ensureTimerRuntime)
// share one select() wait instead of two competing loops.
//
// V1 scope (TDD-00004): single listener (no user-facing "close" — the two
// globals below hold at most one registered listener at a time, matching
// "V1 has no need for multiple servers"). Concurrent connection handling
// (TDD-00006 Part 2, ADR-00049) and full request parsing (ADR-00072:
// headers, query string, request body, response headers beyond
// status/body — see buildHTTPDispatcher in emit_http.go) are both real now.
//
//	__kml_http_bind_and_listen(i32 port) -> i32
//	  socket()+setsockopt(SO_REUSEADDR)+bind()+listen(); throws a catchable
//	  Error (via __kml_http_throw) on any failure instead of returning -1,
//	  so the Go-emitted call site never needs its own error check.
//	__kml_http_send_response(i32 connfd, i64 status, ptr body, ptr extraHeaders)
//	  Formats a minimal HTTP/1.1 response (fixed "OK" reason phrase
//	  regardless of status — real clients determine success/failure from
//	  the numeric code, not the phrase) with Content-Length/Connection:
//	  close plus extraHeaders (empty string if the handler's return type
//	  has no `headers` field — see ensureHTTPSerializeHeaders), writes it,
//	  closes the connection.
//	__kml_event_loop_run()
//	  The generalized drain loop: each iteration, scans the timer queue for
//	  the earliest-due entry exactly like __kml_timer_drain, builds an
//	  fd_set containing the registered listener (if any, via
//	  @__kml_listen_fd), and calls select() with a timeout computed from
//	  that earliest-due timer (blocking indefinitely if a listener is
//	  registered but no timer is pending, since select() alone can't return
//	  "nothing to wait for" the way an empty queue could return instantly).
//	  On wake: dispatches through @__kml_listen_dispatch if the listener is
//	  ready, then fires/reschedules/retires the due timer exactly like
//	  __kml_timer_drain. Loops forever once a listener is registered
//	  (matching http.listen's own "never returns" contract — no user code
//	  ever unregisters it in V1); with no listener registered it behaves
//	  identically to plain nanosleep-based timer draining, just implemented
//	  via a zero-timeout-capable select() instead.
//
// ensureFiberRuntime declares the fiber-context-switching primitive
// (ucontext.h's getcontext/makecontext/swapcontext, called directly via
// declare/call — no hand-written assembly, confirmed by direct prototyping
// during TDD-00006 Part 2) and the connection-fiber array shared by both
// http.listen (ADR-00049, one entry per accepted connection) and
// await fetch(...) (ADR-00050, reuses whichever connection fiber is
// currently running to yield/resume around an in-flight libcurl transfer —
// there is no separate fiber kind in this compiler, a fetch awaited from
// inside a connection handler just parks and resumes that same fiber).
// Entry layout ({ i64 fd, ptr ctx, ptr stack, ptr pendingFetch, ptr
// pendingGroup }, 40 bytes): pendingFetch is null under normal HTTP-read
// waiting (resume when fd is readable, the original ADR-00049 behavior) and
// non-null while this fiber is specifically parked on a still-in-flight
// fetch (resume when that fetch's own "done" flag is set, regardless of
// fd_set readiness). pendingGroup (ADR-00073) is the same idea one level up
// — non-null while this fiber is parked on a Promise.all/.race/.allSettled
// group-wait (__kml_await_group_wait), resumed when __kml_group_satisfied
// says the group as a whole is ready. The two fields are independent, not a
// union — a fiber is only ever parked on at most one of them at a time, but
// they're kept as separate fields rather than overlaid to avoid any
// byte-layout coupling between the single-pending and group-wait paths.
//
// @__kml_conn_active (TDD-00027) is a separate i64 counter, not derivable
// from @__kml_conn_len alone (len only ever grows — a finished connection's
// slot is reused, never removed): it tracks how many entries currently have
// fd >= 0, incremented once in __kml_http_append_conn and decremented at
// both of buildHTTPDispatcher's connection-finish sites (emit_http.go's
// parseL/noReqL). __kml_event_loop_run's scandone folds it into its exit
// condition so http.close() — which only stops *new* accepts by clearing
// @__kml_listen_fd — lets already-open connections finish naturally instead
// of being silently orphaned (leaked ucontext_t/stack, an unflushed open
// socket) the instant the listener disappears.
func (e *Emitter) ensureFiberRuntime() {
	if e.usedFiber {
		return
	}
	e.usedFiber = true
	e.emitGlobal("declare void @getcontext(ptr noundef)")
	e.emitGlobal("declare void @makecontext(ptr noundef, ptr noundef, i32 noundef, ...)")
	e.emitGlobal("declare i32 @swapcontext(ptr noundef, ptr noundef)")
	ctxSize, _, _, _ := ucontextLayout()
	e.emitGlobal(fmt.Sprintf("@__kml_main_ctx = internal global [%d x i8] zeroinitializer, align 16", ctxSize))
	e.emitGlobal("@__kml_conn_data = internal global ptr null, align 8")
	e.emitGlobal("@__kml_conn_len = internal global i64 0, align 8")
	e.emitGlobal("@__kml_conn_cap = internal global i64 0, align 8")
	e.emitGlobal("@__kml_current_conn_idx = internal global i64 -1, align 8")
	e.emitGlobal("@__kml_conn_active = internal global i64 0, align 8")
}

// ensureHTTPClusterFork declares __kml_http_cluster_fork(i64 numWorkers) and
// its two supporting globals (TDD-00025): a flat "spawn N-1 additional
// peers" fan-out loop, NOT each child re-entering the loop (which would
// fork exponentially). Called unconditionally from ensureHTTPRuntime,
// always right after __kml_http_bind_and_listen succeeds and before any
// connection-fiber state exists — forking after a fiber's ucontext_t/stack
// has ever been created is unreasoned-about territory this design
// deliberately avoids. numWorkers <= 1 (the http.listen(port, handler) form
// with no third argument, or an explicit { workers: 1 }) is a no-op: the
// loop condition is never true, so the process falls straight through with
// @__kml_cluster_worker_id left at its zeroed default — today's
// single-process behavior, byte-for-byte.
//
// Unlike ensureExecFileSync's fork() (runtime_process.go), whose child
// always immediately execvp()s or _exit()s, a cluster worker must instead
// fall through into ordinary emitted code (the caller's own
// __kml_event_loop_run call) — so this is a genuinely new fork() call site,
// not a reuse of that one.
func (e *Emitter) ensureHTTPClusterFork() {
	if e.usedHTTPClusterFork {
		return
	}
	e.usedHTTPClusterFork = true
	e.ensureForkDecl()
	e.ensureFflushDecl()
	e.emitGlobal("@__kml_cluster_worker_id = internal global i64 0, align 8")
	e.emitGlobal(`
define void @__kml_http_cluster_fork(i64 %numWorkers) {
entry:
  %ip = alloca i64, align 8
  store i64 1, ptr %ip, align 8
  %needsfork = icmp sgt i64 %numWorkers, 1
  br i1 %needsfork, label %forkloop, label %done

forkloop:
  %i = load i64, ptr %ip, align 8
  %cont = icmp slt i64 %i, %numWorkers
  br i1 %cont, label %doforkw, label %done

doforkw:
  ; fflush(NULL) (flushes every open output stream, including stdout) right
  ; before fork() is required, not optional: fork() copies libc's stdio
  ; buffers verbatim, so any console.log output still sitting unflushed in
  ; stdout's buffer at fork time (the common case once stdout isn't a TTY —
  ; e.g. piped to a container's log collector, this project's own
  ; microservice target) would otherwise get flushed independently by every
  ; worker that inherits the copy, printing the same line once per worker
  ; instead of once. Found via this feature's own example
  ; (examples/http/http_cluster.ts) printing its startup banner N times
  ; instead of once when piped (not run at a real terminal).
  call i32 @fflush(ptr null)
  %pid = call i32 @fork()
  %ischild = icmp eq i32 %pid, 0
  br i1 %ischild, label %child, label %parentnext

child:
  store i64 %i, ptr @__kml_cluster_worker_id, align 8
  br label %done

parentnext:
  %inext = add i64 %i, 1
  store i64 %inext, ptr %ip, align 8
  br label %forkloop

done:
  ret void
}`)
}

func (e *Emitter) ensureHTTPRuntime() {
	if e.usedHTTP {
		return
	}
	e.usedHTTP = true
	e.ensureTimerRuntime()
	e.ensureFiberRuntime()
	// __kml_event_loop_run below unconditionally references
	// @__kml_curl_multi/curl_multi_fdset/curl_multi_perform/
	// __kml_curl_drain_messages (its own "does curl have work to do"
	// checks are a runtime branch, not something Go-side codegen can
	// decide in advance — a fetch() call inside this very handler's body
	// is only discovered by buildHTTPDispatcher, called *after* this
	// function). Every symbol the loop's IR mentions must still be
	// declared/defined for the .ll to link, whether or not the program
	// ever actually calls fetch() — so http.listen always pulls in the
	// full async-fetch machinery (and, transitively, libcurl) alongside
	// its own socket runtime, not just when fetch() is textually present.
	e.ensureFetchAsync()
	// Same reasoning again (ADR-00073): __kml_event_loop_run's rcheckgroup
	// block below unconditionally calls @__kml_group_satisfied to check a
	// fiber's pendingGroup field, whether or not this program ever calls
	// Promise.all/.race/.allSettled.
	e.ensurePromiseCombinators()
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemset()
	e.ensureFree()
	e.ensureSscanf()
	e.ensureSprintf()
	e.ensureStrlen()
	e.ensureHTTPThrow()
	// Every request now parses headers/query/body regardless of whether
	// the handler reads them — same "always pull in the full machinery"
	// reasoning as ensureFetchAsync above, not a per-feature opt-in.
	e.ensureSplitFirst()
	e.ensureHTTPParseHeaders()
	e.ensureHTTPParseQuery()
	e.ensureMapStrHelpers()
	e.ensureAtoll()

	e.ensureErrnoAccessor()

	e.emitGlobal("declare i32 @socket(i32 noundef, i32 noundef, i32 noundef)")
	e.emitGlobal("declare i32 @setsockopt(i32 noundef, i32 noundef, i32 noundef, ptr noundef, i32 noundef)")
	e.emitGlobal("declare i32 @bind(i32 noundef, ptr noundef, i32 noundef)")
	e.emitGlobal("declare i32 @listen(i32 noundef, i32 noundef)")
	e.emitGlobal("declare i32 @accept(i32 noundef, ptr noundef, ptr noundef)")
	e.ensureReadDecl()
	e.emitGlobal("declare i64 @write(i32 noundef, ptr noundef, i64 noundef)")
	e.ensureCloseDecl()
	e.emitGlobal("declare i32 @select(i32 noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i16 @htons(i16 noundef)")
	e.emitGlobal("declare i32 @fcntl(i32 noundef, i32 noundef, ...)")
	e.ensureForkDecl()

	e.emitGlobal("@__kml_listen_fd = internal global i32 -1, align 4")
	e.emitGlobal("@__kml_listen_dispatch = internal global ptr null, align 8")
	e.emitGlobal("@__kml_listen_handler = internal global ptr null, align 8")

	solSocket, soReuseAddr := httpSockConstants()
	fam0, fam1 := httpSockaddrFamilyBytes()

	e.emitGlobal(fmt.Sprintf(`
define i32 @__kml_http_bind_and_listen(i32 %%port) {
entry:
  %%fd = call i32 @socket(i32 2, i32 1, i32 0)
  %%fdok = icmp sge i32 %%fd, 0
  br i1 %%fdok, label %%setopt, label %%failnofd

setopt:
  %%one = alloca i32, align 4
  store i32 1, ptr %%one, align 4
  call i32 @setsockopt(i32 %%fd, i32 %d, i32 %d, ptr %%one, i32 4)

  %%addr = alloca [16 x i8], align 4
  call ptr @memset(ptr %%addr, i32 0, i64 16)
  store i8 %d, ptr %%addr, align 1
  %%b1p = getelementptr i8, ptr %%addr, i64 1
  store i8 %d, ptr %%b1p, align 1
  %%portu16 = trunc i32 %%port to i16
  %%portn = call i16 @htons(i16 %%portu16)
  %%portp = getelementptr i8, ptr %%addr, i64 2
  store i16 %%portn, ptr %%portp, align 1

  %%bindrc = call i32 @bind(i32 %%fd, ptr %%addr, i32 16)
  %%bindok = icmp eq i32 %%bindrc, 0
  br i1 %%bindok, label %%dolisten, label %%failwithfd

dolisten:
  %%listenrc = call i32 @listen(i32 %%fd, i32 128)
  %%listenok = icmp eq i32 %%listenrc, 0
  br i1 %%listenok, label %%setnonblocklistener, label %%failwithfd

setnonblocklistener:
  ; Required once http.listen({workers: N}) can fork() multiple peers that
  ; all select()/accept() on this same inherited fd (TDD-00025): with a
  ; blocking listener, a worker that loses the accept() race after select()
  ; reports readiness would hang its entire event loop, not just skip a
  ; connection. Harmless in the single-process (workers omitted/1) case too
  ; — the existing accept path already treats any accept() failure as "no
  ; connection this round, keep looping" (doaccept/scanconn below), which is
  ; exactly correct EAGAIN/EWOULDBLOCK behavior once this fd is non-blocking.
  %%listencurflags = call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 3)
  %%listennewflags = or i32 %%listencurflags, %d
  call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 4, i32 %%listennewflags)
  br label %%success

success:
  ret i32 %%fd

failwithfd:
  call i32 @close(i32 %%fd)
  call void @__kml_http_throw(ptr %s)
  unreachable

failnofd:
  call void @__kml_http_throw(ptr %s)
  unreachable
}`, solSocket, soReuseAddr, fam0, fam1, httpNonblockFlag(),
		e.internString("http.listen: failed to bind or listen"),
		e.internString("http.listen: failed to create socket")))

	e.ensureHTTPClusterFork()

	// __kml_http_append_conn: appends a new { i64 fd, ptr ctx, ptr stack,
	// ptr pendingFetch, ptr pendingGroup } entry (growable, realloc-doubling,
	// same shape as the timer queue) for a freshly-accepted connection,
	// builds its fiber (a fresh ucontext_t + a 64KB stack, uc_link back to
	// the main/scheduler context so the fiber function returning normally
	// resumes the scheduler automatically), and immediately swaps into it
	// once — the same "launch it now" step confirmed working in this
	// feature's prototyping spike. The fiber entry point is always
	// @__kml_listen_dispatch's stored pointer (the per-call-site-specialized
	// dispatcher built by emit_http.go). pendingFetch/pendingGroup both
	// start null (normal fd-readiness-based waiting) — see
	// ensureFiberRuntime's doc comment.
	// ctxSize/ssSpOff/ssSizeOff/ucLinkOff: see ucontextLayout's doc comment
	// — sizeof(ucontext_t) and its field offsets are NOT portable across
	// platforms (a real bug found via a failing Linux CI run, fixed here).
	ctxSize, ssSpOff, ssSizeOff, ucLinkOff := ucontextLayout()

	// gc mode: repoint Boehm's GC_stackbottom at this fiber's own stack
	// (stacks grow down, so the high end of the fiberStackBytes block is the
	// "bottom" as far as GC_stackbottom's naming convention is concerned)
	// right before swapping into it — a collection triggered while this
	// fiber is running would otherwise have Boehm's root-stack scan walk
	// from the live SP (now inside this malloc'd block) to the *original*
	// process stack's address, an unrelated and likely-unmapped range. See
	// docs/adr/ADR-00071.md.
	gcSetStackbottom := ""
	// gc mode: restore GC_stackbottom back to the real process stack right
	// after swapcontext returns control here — covers both ways a fiber can
	// hand control back: an explicit yield (mid-request, waiting on more
	// data/a pending fetch) *and* running to completion in one shot (the
	// common case: request fully buffered, handler returns, response sent,
	// no further yield — control returns here automatically via uc_link,
	// with no swapcontext call of our own to hang a restore off of on the
	// fiber's side). Only the explicit-yield case used to be handled (via a
	// symmetric restore placed right before each such yield's own
	// swapcontext call, in emit_http.go/runtime_fetch.go) — the
	// runs-to-completion case left GC_stackbottom dangling at this now-freed
	// fiber's stack until the *next* resume overwrote it, so any collection
	// triggered in that window scanned the wrong stack range and could
	// silently free memory still live only on the real process stack. Fixed
	// here at the resume site instead, unconditionally, so it's correct
	// regardless of why the fiber gave control back — see docs/adr for the
	// ADR documenting this fix (follow-up to ADR-00071/ADR-00099).
	gcRestoreStackbottom := ""
	if e.isGCMode() {
		gcSetStackbottom = fmt.Sprintf(`
  %%stackhigh = getelementptr i8, ptr %%stack, i64 %d
  store ptr %%stackhigh, ptr @GC_stackbottom, align 8`, fiberStackBytes)
		gcRestoreStackbottom = `
  %origbottom0 = load ptr, ptr @__kml_gc_orig_stackbottom, align 8
  store ptr %origbottom0, ptr @GC_stackbottom, align 8`
	}

	e.emitGlobal(`
define void @__kml_http_append_conn(i32 %fd) {
entry:
  %len = load i64, ptr @__kml_conn_len, align 8
  %cap = load i64, ptr @__kml_conn_cap, align 8
  %data = load ptr, ptr @__kml_conn_data, align 8
  %neededp1 = add i64 %len, 1
  %needgrow = icmp sgt i64 %neededp1, %cap
  br i1 %needgrow, label %grow, label %doappend

grow:
  %cap2 = mul i64 %cap, 2
  %atleast8 = icmp sgt i64 %cap2, 8
  %newcap = select i1 %atleast8, i64 %cap2, i64 8
  %newcapbytes = mul i64 %newcap, 40
  %newdata = call ptr @realloc(ptr %data, i64 %newcapbytes)
  store ptr %newdata, ptr @__kml_conn_data, align 8
  store i64 %newcap, ptr @__kml_conn_cap, align 8
  br label %doappend

doappend:
  %dataNow = load ptr, ptr @__kml_conn_data, align 8
  %slot = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %dataNow, i64 %len

  %fd64 = sext i32 %fd to i64
  %fd_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %slot, i32 0, i32 0
  store i64 %fd64, ptr %fd_p, align 8

  %ctx = call ptr @malloc(i64 ` + fmt.Sprintf("%d", ctxSize) + `)
  %stack = call ptr @malloc(i64 ` + fmt.Sprintf("%d", fiberStackBytes) + `)
  call void @getcontext(ptr %ctx)
  %ss_sp_p = getelementptr i8, ptr %ctx, i64 ` + fmt.Sprintf("%d", ssSpOff) + `
  store ptr %stack, ptr %ss_sp_p, align 8
  %ss_size_p = getelementptr i8, ptr %ctx, i64 ` + fmt.Sprintf("%d", ssSizeOff) + `
  store i64 ` + fmt.Sprintf("%d", fiberStackBytes) + `, ptr %ss_size_p, align 8
  %uc_link_p = getelementptr i8, ptr %ctx, i64 ` + fmt.Sprintf("%d", ucLinkOff) + `
  store ptr @__kml_main_ctx, ptr %uc_link_p, align 8
  %dfp = load ptr, ptr @__kml_listen_dispatch, align 8
  call void (ptr, ptr, i32, ...) @makecontext(ptr %ctx, ptr %dfp, i32 0)

  %ctx_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %slot, i32 0, i32 1
  store ptr %ctx, ptr %ctx_p, align 8
  %stack_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %slot, i32 0, i32 2
  store ptr %stack, ptr %stack_p, align 8
  %pf_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %slot, i32 0, i32 3
  store ptr null, ptr %pf_p, align 8
  %pg_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %slot, i32 0, i32 4
  store ptr null, ptr %pg_p, align 8

  %newlen = add i64 %len, 1
  store i64 %newlen, ptr @__kml_conn_len, align 8
  %activeNow = load i64, ptr @__kml_conn_active, align 8
  %activeNew = add i64 %activeNow, 1
  store i64 %activeNew, ptr @__kml_conn_active, align 8

  store i64 %len, ptr @__kml_current_conn_idx, align 8` + gcSetStackbottom + `
  %swaprc = call i32 @swapcontext(ptr @__kml_main_ctx, ptr %ctx)` + gcRestoreStackbottom + `
  ret void
}`)

	// extraHeaders already ends in "\r\n" per entry (or is "" when the
	// handler's return type has no `headers` field — see
	// ensureHTTPSerializeHeaders/emitHTTPListen), so this format produces
	// byte-identical output to before extraHeaders existed when it's empty,
	// and a correct single blank-line header/body separator either way.
	respFmt := e.internString("HTTP/1.1 %lld OK\r\nContent-Length: %lld\r\nConnection: close\r\n%s\r\n%s")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_http_send_response(i32 %%connfd, i64 %%status, ptr %%body, ptr %%extraHeaders) {
entry:
  %%bodylen = call i64 @strlen(ptr %%body)
  %%hdrlen = call i64 @strlen(ptr %%extraHeaders)
  %%bufsize0 = add i64 %%bodylen, %%hdrlen
  %%bufsize1 = add i64 %%bufsize0, 128
  %%respbuf = call ptr @malloc(i64 %%bufsize1)
  %%n = call i32 (ptr, ptr, ...) @sprintf(ptr %%respbuf, ptr %s, i64 %%status, i64 %%bodylen, ptr %%extraHeaders, ptr %%body)
  %%n64 = sext i32 %%n to i64
  call i64 @write(i32 %%connfd, ptr %%respbuf, i64 %%n64)
  call void @free(ptr %%respbuf)
  call i32 @close(i32 %%connfd)
  ret void
}`, respFmt))

	// gc mode: same GC_stackbottom repointing as __kml_http_append_conn's
	// initial fiber launch, needed here too since this is the *other* place
	// the event loop swaps into a fiber (a resumed, previously-yielded one
	// rather than a freshly-created one) — see docs/adr/ADR-00071.md.
	gcSetRStackbottom := ""
	// gc mode: restore GC_stackbottom to the real process stack right after
	// this swapcontext returns, unconditionally — see the identical
	// gcRestoreStackbottom comment at __kml_http_append_conn's own resume
	// site above for why this has to happen on the resumer's side rather
	// than relying on the fiber to restore it before yielding (that only
	// covers an explicit yield; a fiber that runs a request to completion
	// in one shot returns here via uc_link with no swapcontext call of its
	// own to hang a restore off of).
	gcRestoreRStackbottom := ""
	if e.isGCMode() {
		gcSetRStackbottom = fmt.Sprintf(`
  %%rstack_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %%rslot, i32 0, i32 2
  %%rstack = load ptr, ptr %%rstack_p, align 8
  %%rstackhigh = getelementptr i8, ptr %%rstack, i64 %d
  store ptr %%rstackhigh, ptr @GC_stackbottom, align 8`, fiberStackBytes)
		gcRestoreRStackbottom = `
  %rorigbottom = load ptr, ptr @__kml_gc_orig_stackbottom, align 8
  store ptr %rorigbottom, ptr @GC_stackbottom, align 8`
	}

	e.emitGlobal(`
define void @__kml_event_loop_run() {
entry:
  %besti = alloca i64, align 8
  %bestfire = alloca i64, align 8
  %scani = alloca i64, align 8
  %fdset = alloca [128 x i8], align 8
  %wfdset = alloca [128 x i8], align 8
  %efdset = alloca [128 x i8], align 8
  %maxfd = alloca i32, align 4
  %fsi = alloca i64, align 8
  %curlmaxfd = alloca i32, align 4
  %tv = alloca { i64, i64 }, align 8
  %runningp2 = alloca i32, align 4
  %rsi = alloca i64, align 8
  br label %outerloop

outerloop:
  ; TDD-00019: check for a pending signal before anything else this
  ; iteration — this single check point, re-entered after every iteration,
  ; covers both a signal that arrived since the last check (seen before
  ; computing select()'s timeout below) and a signal that interrupts a
  ; blocking select() call directly (seen immediately on looping back
  ; after EINTR — see the afterselect: fix further down).
  %sigintp = load volatile i8, ptr @__kml_sigint_pending, align 1
  %sigintset = icmp ne i8 %sigintp, 0
  br i1 %sigintset, label %sigintfire, label %checksigterm

sigintfire:
  store volatile i8 0, ptr @__kml_sigint_pending, align 1
  %sigintclos = load ptr, ptr @__kml_sigint_closure, align 8
  %hassigint = icmp ne ptr %sigintclos, null
  br i1 %hassigint, label %sigintcall, label %checksigterm

sigintcall:
  %sigintfp_p = getelementptr { ptr, ptr }, ptr %sigintclos, i32 0, i32 0
  %sigintep_p = getelementptr { ptr, ptr }, ptr %sigintclos, i32 0, i32 1
  %sigintfp = load ptr, ptr %sigintfp_p, align 8
  %sigintep = load ptr, ptr %sigintep_p, align 8
  call void %sigintfp(ptr %sigintep)
  br label %checksigterm

checksigterm:
  %sigtermp = load volatile i8, ptr @__kml_sigterm_pending, align 1
  %sigtermset = icmp ne i8 %sigtermp, 0
  br i1 %sigtermset, label %sigtermfire, label %timerscan

sigtermfire:
  store volatile i8 0, ptr @__kml_sigterm_pending, align 1
  %sigtermclos = load ptr, ptr @__kml_sigterm_closure, align 8
  %hassigterm = icmp ne ptr %sigtermclos, null
  br i1 %hassigterm, label %sigtermcall, label %timerscan

sigtermcall:
  %sigtermfp_p = getelementptr { ptr, ptr }, ptr %sigtermclos, i32 0, i32 0
  %sigtermep_p = getelementptr { ptr, ptr }, ptr %sigtermclos, i32 0, i32 1
  %sigtermfp = load ptr, ptr %sigtermfp_p, align 8
  %sigtermep = load ptr, ptr %sigtermep_p, align 8
  call void %sigtermfp(ptr %sigtermep)
  br label %timerscan

timerscan:
  %len = load i64, ptr @__kml_timer_len, align 8
  %data = load ptr, ptr @__kml_timer_data, align 8
  store i64 -1, ptr %besti, align 8
  store i64 0, ptr %bestfire, align 8
  store i64 0, ptr %scani, align 8
  br label %scanloop

scanloop:
  %si = load i64, ptr %scani, align 8
  %sinbounds = icmp slt i64 %si, %len
  br i1 %sinbounds, label %scanbody, label %scandone

scanbody:
  %sslot = getelementptr { i64, i64, i64, ptr }, ptr %data, i64 %si
  %sinterval_p = getelementptr { i64, i64, i64, ptr }, ptr %sslot, i32 0, i32 2
  %sinterval = load i64, ptr %sinterval_p, align 8
  %sdone = icmp eq i64 %sinterval, -1
  br i1 %sdone, label %scannext, label %scanconsider

scanconsider:
  %sfire_p = getelementptr { i64, i64, i64, ptr }, ptr %sslot, i32 0, i32 1
  %sfire = load i64, ptr %sfire_p, align 8
  %curbesti = load i64, ptr %besti, align 8
  %noneyet = icmp eq i64 %curbesti, -1
  br i1 %noneyet, label %scantakebest, label %scancompare

scancompare:
  %curbestfire = load i64, ptr %bestfire, align 8
  %better = icmp slt i64 %sfire, %curbestfire
  br i1 %better, label %scantakebest, label %scannext

scantakebest:
  store i64 %si, ptr %besti, align 8
  store i64 %sfire, ptr %bestfire, align 8
  br label %scannext

scannext:
  %sinext = add i64 %si, 1
  store i64 %sinext, ptr %scani, align 8
  br label %scanloop

scandone:
  %foundbest = load i64, ptr %besti, align 8
  %havetimer = icmp ne i64 %foundbest, -1
  %listenfd = load i32, ptr @__kml_listen_fd, align 4
  %haslistener = icmp sge i32 %listenfd, 0
  ; TDD-00027: http.close() clears @__kml_listen_fd immediately but leaves
  ; any already-accepted connection to finish naturally — so the loop's own
  ; exit condition must also check for those, not just the listener/timers,
  ; or a connection still parked mid-request the instant close() runs would
  ; be silently orphaned (leaked ucontext_t/stack, an unflushed open
  ; socket). checklistener below already gates new accept()s on haslistener
  ; alone, so this doesn't reopen accepting new connections.
  %activeconns = load i64, ptr @__kml_conn_active, align 8
  %hasactiveconns = icmp sgt i64 %activeconns, 0
  %anywork0 = or i1 %havetimer, %haslistener
  %anywork = or i1 %anywork0, %hasactiveconns
  br i1 %anywork, label %dowork, label %alldone

dowork:
  call ptr @memset(ptr %fdset, i32 0, i64 128)
  call ptr @memset(ptr %wfdset, i32 0, i64 128)
  call ptr @memset(ptr %efdset, i32 0, i64 128)
  store i32 -1, ptr %maxfd, align 4
  br i1 %haslistener, label %setfd, label %skipsetfd

setfd:
  %fddiv8 = sdiv i32 %listenfd, 8
  %fdmod8 = srem i32 %listenfd, 8
  %fddiv8_64 = sext i32 %fddiv8 to i64
  %byteptr = getelementptr i8, ptr %fdset, i64 %fddiv8_64
  %bitpos8 = trunc i32 %fdmod8 to i8
  %bitmask = shl i8 1, %bitpos8
  %oldbyte = load i8, ptr %byteptr, align 1
  %newbyte = or i8 %oldbyte, %bitmask
  store i8 %newbyte, ptr %byteptr, align 1
  store i32 %listenfd, ptr %maxfd, align 4
  br label %skipsetfd

skipsetfd:
  ; Add every still-active (fd >= 0) connection's fd into the same fd_set,
  ; tracking the overall highest fd for select()'s nfds argument.
  %clen = load i64, ptr @__kml_conn_len, align 8
  %cdata = load ptr, ptr @__kml_conn_data, align 8
  store i64 0, ptr %fsi, align 8
  br label %fsetloop

fsetloop:
  %fi = load i64, ptr %fsi, align 8
  %finb = icmp slt i64 %fi, %clen
  br i1 %finb, label %fsetbody, label %fsetdone

fsetbody:
  %fslot0 = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %cdata, i64 %fi
  %ffd_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %fslot0, i32 0, i32 0
  %ffdv = load i64, ptr %ffd_p, align 8
  %factive = icmp sge i64 %ffdv, 0
  br i1 %factive, label %fsetbit, label %fsetnext

fsetbit:
  %ffdiv8 = sdiv i64 %ffdv, 8
  %ffmod8 = srem i64 %ffdv, 8
  %ffbyteptr = getelementptr i8, ptr %fdset, i64 %ffdiv8
  %ffmod8_8 = trunc i64 %ffmod8 to i8
  %ffmask = shl i8 1, %ffmod8_8
  %ffoldbyte = load i8, ptr %ffbyteptr, align 1
  %ffnewbyte = or i8 %ffoldbyte, %ffmask
  store i8 %ffnewbyte, ptr %ffbyteptr, align 1
  %ffdv32 = trunc i64 %ffdv to i32
  %fcurmax = load i32, ptr %maxfd, align 4
  %fisbigger = icmp sgt i32 %ffdv32, %fcurmax
  br i1 %fisbigger, label %fupdatemax, label %fsetnext

fupdatemax:
  store i32 %ffdv32, ptr %maxfd, align 4
  br label %fsetnext

fsetnext:
  %finext = add i64 %fi, 1
  store i64 %finext, ptr %fsi, align 8
  br label %fsetloop

fsetdone:
  ; Merge libcurl's own fd_sets (its in-flight transfers' sockets) into the
  ; same read/write/exc sets, if any await fetch(...) has ever created the
  ; multi handle — curl_multi_fdset ORs its bits in rather than clearing
  ; the sets first, so this is safe to call after our own fds are already
  ; set. See ensureFetchAsync (emit_async.go's fetch-await path).
  %curlmulti = load ptr, ptr @__kml_curl_multi, align 8
  %hascurl = icmp ne ptr %curlmulti, null
  br i1 %hascurl, label %mergecurlfds, label %skipmergecurlfds

mergecurlfds:
  store i32 -1, ptr %curlmaxfd, align 4
  call i32 @curl_multi_fdset(ptr %curlmulti, ptr %fdset, ptr %wfdset, ptr %efdset, ptr %curlmaxfd)
  %curlmaxfdv = load i32, ptr %curlmaxfd, align 4
  %curmaxfd1 = load i32, ptr %maxfd, align 4
  %curlbigger = icmp sgt i32 %curlmaxfdv, %curmaxfd1
  br i1 %curlbigger, label %takecurlmax, label %skipmergecurlfds

takecurlmax:
  store i32 %curlmaxfdv, ptr %maxfd, align 4
  br label %skipmergecurlfds

skipmergecurlfds:
  %maxfdv = load i32, ptr %maxfd, align 4
  %nfds = add i32 %maxfdv, 1

  br i1 %havetimer, label %timeoutpath, label %notimeoutpath

timeoutpath:
  %targetfire = load i64, ptr %bestfire, align 8
  %now1 = call i64 @__kml_monotonic_ns()
  %rawwait = sub i64 %targetfire, %now1
  %negwait = icmp slt i64 %rawwait, 0
  %waitns = select i1 %negwait, i64 0, i64 %rawwait
  %waitsec = sdiv i64 %waitns, 1000000000
  %waitnsrem = srem i64 %waitns, 1000000000
  %waitusec = sdiv i64 %waitnsrem, 1000
  %tv_sec = getelementptr { i64, i64 }, ptr %tv, i32 0, i32 0
  %tv_usec = getelementptr { i64, i64 }, ptr %tv, i32 0, i32 1
  store i64 %waitsec, ptr %tv_sec, align 8
  store i64 %waitusec, ptr %tv_usec, align 8
  %selrc1 = call i32 @select(i32 %nfds, ptr %fdset, ptr %wfdset, ptr %efdset, ptr %tv)
  br label %afterselect

notimeoutpath:
  %selrc2 = call i32 @select(i32 %nfds, ptr %fdset, ptr %wfdset, ptr %efdset, ptr null)
  br label %afterselect

afterselect:
  ; TDD-00019: select()'s return value must be checked. POSIX leaves the
  ; fd_sets *unmodified* on an EINTR return (e.g. a signal arriving while
  ; blocked here) — they'd still hold the watch set this iteration built,
  ; not real readiness info. Without this check, a spurious accept() on
  ; the (blocking) listener socket could hang the whole loop indefinitely
  ; right when a signal was trying to wake it up. On failure, skip
  ; straight back to outerloop's own pending-signal check instead of
  ; trusting a fd_set POSIX only guarantees is meaningful on success.
  %selrc = phi i32 [ %selrc1, %timeoutpath ], [ %selrc2, %notimeoutpath ]
  %selfailed = icmp slt i32 %selrc, 0
  br i1 %selfailed, label %outerloop, label %afterselectok

afterselectok:
  br i1 %hascurl, label %docurlperform, label %checklistener

docurlperform:
  call i32 @curl_multi_perform(ptr %curlmulti, ptr %runningp2)
  call void @__kml_curl_drain_messages()
  br label %checklistener

checklistener:
  br i1 %haslistener, label %checkisset, label %scanconn

checkisset:
  %fddiv8b = sdiv i32 %listenfd, 8
  %fdmod8b = srem i32 %listenfd, 8
  %fddiv8b_64 = sext i32 %fddiv8b to i64
  %byteptrb = getelementptr i8, ptr %fdset, i64 %fddiv8b_64
  %bitpos8b = trunc i32 %fdmod8b to i8
  %bitmaskb = shl i8 1, %bitpos8b
  %bytevalb = load i8, ptr %byteptrb, align 1
  %maskedb = and i8 %bytevalb, %bitmaskb
  %ready = icmp ne i8 %maskedb, 0
  br i1 %ready, label %doaccept, label %scanconn

doaccept:
  %newfd = call i32 @accept(i32 %listenfd, ptr null, ptr null)
  %acceptok = icmp sge i32 %newfd, 0
  br i1 %acceptok, label %setnonblock, label %scanconn

setnonblock:
  %curflags = call i32 (i32, i32, ...) @fcntl(i32 %newfd, i32 3)
  %newflags = or i32 %curflags, ` + fmt.Sprintf("%d", httpNonblockFlag()) + `
  call i32 (i32, i32, ...) @fcntl(i32 %newfd, i32 4, i32 %newflags)
  call void @__kml_http_append_conn(i32 %newfd)
  br label %scanconn

scanconn:
  ; Resume every connection fiber whose fd came back ready in the fd_set
  ; select() just populated (a fiber that finished sets its own entry's fd
  ; to -1 right before returning, so "still >= 0 after resume" means it
  ; genuinely yielded again and should keep being watched next iteration).
  store i64 0, ptr %rsi, align 8
  br label %rscanloop

rscanloop:
  %ri = load i64, ptr %rsi, align 8
  %rclen = load i64, ptr @__kml_conn_len, align 8
  %rinb = icmp slt i64 %ri, %rclen
  br i1 %rinb, label %rscanbody, label %checktimerfire

rscanbody:
  %rcdata = load ptr, ptr @__kml_conn_data, align 8
  %rslot = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %rcdata, i64 %ri
  %rfd_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %rslot, i32 0, i32 0
  %rfdv = load i64, ptr %rfd_p, align 8
  %ractive = icmp sge i64 %rfdv, 0
  br i1 %ractive, label %rcheckpending, label %rscannext

rcheckpending:
  ; A fiber parked on await fetch(...) (pendingFetch != null) is resumed
  ; when that specific fetch is done, regardless of fd_set readiness —
  ; its own connection fd isn't what it's waiting on right now.
  %rpf_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %rslot, i32 0, i32 3
  %rpf = load ptr, ptr %rpf_p, align 8
  %rhaspending = icmp ne ptr %rpf, null
  br i1 %rhaspending, label %rcheckfetchdone, label %rcheckgroup

rcheckfetchdone:
  %rpf_done_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %rpf, i32 0, i32 2
  %rpf_done = load i64, ptr %rpf_done_p, align 8
  %rfetchready = icmp ne i64 %rpf_done, 0
  br i1 %rfetchready, label %rresume, label %rscannext

rcheckgroup:
  ; A fiber parked on a Promise.all/.race/.allSettled group-wait
  ; (pendingGroup != null, ADR-00073) is resumed once __kml_group_satisfied
  ; says the group as a whole is ready — same "not what our own fd is
  ; waiting on" reasoning as rcheckpending above. __kml_curl_multi_perform +
  ; drain already ran earlier this iteration (docurlperform, before this
  ; scan), so every member's done flag is already fresh by the time this
  ; runs.
  %rpg_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %rslot, i32 0, i32 4
  %rpg = load ptr, ptr %rpg_p, align 8
  %rhasgroup = icmp ne ptr %rpg, null
  br i1 %rhasgroup, label %rcheckgroupdone, label %rcheckready

rcheckgroupdone:
  %rgroupsat = call i1 @__kml_group_satisfied(ptr %rpg)
  br i1 %rgroupsat, label %rresume, label %rscannext

rcheckready:
  %rdiv8 = sdiv i64 %rfdv, 8
  %rmod8 = srem i64 %rfdv, 8
  %rbyteptr = getelementptr i8, ptr %fdset, i64 %rdiv8
  %rmod8_8 = trunc i64 %rmod8 to i8
  %rmask = shl i8 1, %rmod8_8
  %rbyteval = load i8, ptr %rbyteptr, align 1
  %rmasked = and i8 %rbyteval, %rmask
  %rready = icmp ne i8 %rmasked, 0
  br i1 %rready, label %rresume, label %rscannext

rresume:
  store i64 %ri, ptr @__kml_current_conn_idx, align 8
  %rctx_p = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %rslot, i32 0, i32 1
  %rctxptr = load ptr, ptr %rctx_p, align 8` + gcSetRStackbottom + `
  call i32 @swapcontext(ptr @__kml_main_ctx, ptr %rctxptr)` + gcRestoreRStackbottom + `
  br label %rscannext

rscannext:
  %rinext = add i64 %ri, 1
  store i64 %rinext, ptr %rsi, align 8
  br label %rscanloop

checktimerfire:
  br i1 %havetimer, label %checkdue, label %outerloop

checkdue:
  %targetfire2 = load i64, ptr %bestfire, align 8
  %now2 = call i64 @__kml_monotonic_ns()
  %isdue = icmp sge i64 %now2, %targetfire2
  br i1 %isdue, label %dofire, label %outerloop

dofire:
  %data2 = load ptr, ptr @__kml_timer_data, align 8
  %fireidx = load i64, ptr %besti, align 8
  %fslot = getelementptr { i64, i64, i64, ptr }, ptr %data2, i64 %fireidx
  %fclosure_p = getelementptr { i64, i64, i64, ptr }, ptr %fslot, i32 0, i32 3
  %fclosure = load ptr, ptr %fclosure_p, align 8
  %fp_p = getelementptr { ptr, ptr }, ptr %fclosure, i32 0, i32 0
  %fp = load ptr, ptr %fp_p, align 8
  %ep_p = getelementptr { ptr, ptr }, ptr %fclosure, i32 0, i32 1
  %ep = load ptr, ptr %ep_p, align 8
  call void (ptr) %fp(ptr %ep)

  %data3 = load ptr, ptr @__kml_timer_data, align 8
  %fslot2 = getelementptr { i64, i64, i64, ptr }, ptr %data3, i64 %fireidx
  %finterval_p = getelementptr { i64, i64, i64, ptr }, ptr %fslot2, i32 0, i32 2
  %finterval = load i64, ptr %finterval_p, align 8
  %stillrepeating = icmp sgt i64 %finterval, 0
  br i1 %stillrepeating, label %reschedule, label %maybemarkdone

reschedule:
  %now3 = call i64 @__kml_monotonic_ns()
  %intervalns = mul i64 %finterval, 1000000
  %newfire = add i64 %now3, %intervalns
  %ffire_p = getelementptr { i64, i64, i64, ptr }, ptr %fslot2, i32 0, i32 1
  store i64 %newfire, ptr %ffire_p, align 8
  br label %outerloop

maybemarkdone:
  %alreadycancelled = icmp eq i64 %finterval, -1
  br i1 %alreadycancelled, label %outerloop, label %markdone

markdone:
  store i64 -1, ptr %finterval_p, align 8
  br label %outerloop

alldone:
  ret void
}`)
}

// ensureHTTPClose declares __kml_http_close (TDD-00027): a direct,
// unconditional mutation of @__kml_listen_fd, not a pending-flag deferred
// like SIGINT/SIGTERM (runtime_process.go) — http.close() is always invoked
// from ordinary control flow already running inside the event loop (a
// request handler, a timer callback, or a process.on(...) closure), never
// from real signal context, so there's no async-signal-safety hazard to
// route around; mutating the global immediately, inline, is correct.
// Idempotent/safe to call when there's no active listener (fd already -1,
// e.g. called twice, or called in a program that never actually called
// http.listen()) — a no-op in that case, matching real Node's own tolerance
// of a redundant .close().
func (e *Emitter) ensureHTTPClose() {
	if e.usedHTTPClose {
		return
	}
	e.usedHTTPClose = true
	e.ensureCloseDecl()
	e.emitGlobal(`
define void @__kml_http_close() {
entry:
  %fd = load i32, ptr @__kml_listen_fd, align 4
  %active = icmp sge i32 %fd, 0
  br i1 %active, label %doclose, label %done

doclose:
  call i32 @close(i32 %fd)
  store i32 -1, ptr @__kml_listen_fd, align 4
  br label %done

done:
  ret void
}`)
}
