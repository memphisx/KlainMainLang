package llvm

import (
	"fmt"
	"strings"
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
func (e *Emitter) httpSockConstants() (solSocket, soReuseAddr int) {
	if e.opts.Target.OS() == "darwin" {
		return 0xffff, 4
	}
	return 1, 2
}

// httpReusePortConst returns SO_REUSEPORT — genuinely platform-specific like
// SO_REUSEADDR above (Darwin 0x200 = 512, Linux 15, both verified on the
// machine via a throwaway C probe). Set on a listening socket ONLY by a cluster
// worker (worker id != 0), so multiple workers can each independently bind the
// same port (the kernel then distributes accepted connections across them). It
// is deliberately NOT set for an ordinary single server: there, a second bind
// to an already-used port must still fail and throw (SO_REUSEPORT would silently
// let it succeed). The shared-fd `http.listen({workers})` model doesn't need it
// either (it binds once and inherits the fd).
func (e *Emitter) httpReusePortConst() int {
	if e.opts.Target.OS() == "darwin" {
		return 0x200
	}
	return 15
}

// httpSockaddrFamilyBytes returns the first two bytes of a struct
// sockaddr_in, which differ by platform even though the struct's total
// size (16 bytes) and every field after it are identical: Linux packs
// sin_family as a plain 2-byte field (family=2 for AF_INET, low byte
// first on this project's little-endian targets); Darwin/BSD instead
// split those same two bytes into sin_len (=16, the struct's own total
// size) followed by a 1-byte sin_family. Port and address fields (offset
// 2 and 4) are identical on both, so only these two bytes need branching.
func (e *Emitter) httpSockaddrFamilyBytes() (byte0, byte1 int) {
	if e.opts.Target.OS() == "darwin" {
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
func (e *Emitter) httpNonblockFlag() int {
	if e.opts.Target.OS() == "darwin" {
		return 0x4
	}
	return 0x800
}

// stdoutGlobalSymbol names the libc extern `FILE *stdout` global for the target.
// macOS exposes it as `__stdoutp` (with `stdout` a macro over it); glibc and the
// other Linux libcs export `stdout` directly. Used to setvbuf() stdout at startup
// (ADR-00867). Not meaningful on Windows, which this is never called for.
func (e *Emitter) stdoutGlobalSymbol() string {
	if e.opts.Target.OS() == "darwin" {
		return "__stdoutp"
	}
	return "stdout"
}

// mmapSharedAnonFlags returns MAP_SHARED|MAP_ANONYMOUS for the host. MAP_SHARED
// is 0x1 on both; MAP_ANONYMOUS is 0x1000 on Darwin, 0x20 on Linux — the cluster
// close-flag page (TDD-00117) is mmap'd with these before the worker fork so all
// forked processes share one physical word.
func (e *Emitter) mmapSharedAnonFlags() int {
	if e.opts.Target.OS() == "darwin" {
		return 0x1001
	}
	return 0x21
}

// httpEagainErrno returns EAGAIN/EWOULDBLOCK's numeric value (35 on Darwin;
// 11 on Linux, where EAGAIN and EWOULDBLOCK are the same value — both
// verified the same way as httpNonblockFlag). A per-connection fiber's read
// loop checks the current errno against this after a failed non-blocking
// read to distinguish "no data yet, yield and retry later" from a real
// error.
func (e *Emitter) httpEagainErrno() int {
	if e.opts.Target.OS() == "darwin" {
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
	e.ensureStrHeaderRuntime()
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
  %%buf = call ptr @__kml_str_alloc(i64 %%bufsize)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%buf, ptr %s, ptr %%opdesc, ptr %%errmsg)
  call void @__kml_str_finalize(ptr %%buf)
  %%errobj = call ptr @malloc(i64 24)
  %%errobj.kind = getelementptr { i64, ptr, ptr }, ptr %%errobj, i32 0, i32 0
  store i64 281474976710656, ptr %%errobj.kind, align 8
  %%errobj.msg = getelementptr { i64, ptr, ptr }, ptr %%errobj, i32 0, i32 1
  store ptr %%buf, ptr %%errobj.msg, align 8
  %%errobj.name = getelementptr { i64, ptr, ptr }, ptr %%errobj, i32 0, i32 2
  store ptr %s, ptr %%errobj.name, align 8
  call void @__kml_throw(ptr %%errobj)
  ret void
}`, e.errnoAccessor(), fmtPtr, errNamePtr))
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
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`
define {ptr, ptr} @__kml_split_first(ptr %s, ptr %sep) {
entry:
  %found = call ptr @strstr(ptr %s, ptr %sep)
  %hit = icmp ne ptr %found, null
  br i1 %hit, label %split, label %nosep
nosep:
  ; TDD-00120: return a length-prefixed copy of %s (it may be a raw request-line
  ; pointer with no header).
  %ns_len = call i64 @strlen(ptr %s)
  %ns_copy = call ptr @__kml_str_alloc(i64 %ns_len)
  %ns_len1 = add i64 %ns_len, 1
  call ptr @memcpy(ptr %ns_copy, ptr %s, i64 %ns_len1)
  %r0 = insertvalue {ptr, ptr} undef, ptr %ns_copy, 0
  %r1 = insertvalue {ptr, ptr} %r0, ptr null, 1
  ret {ptr, ptr} %r1
split:
  %sep_len = call i64 @strlen(ptr %sep)
  %s_int = ptrtoint ptr %s to i64
  %f_int = ptrtoint ptr %found to i64
  %before_len = sub i64 %f_int, %s_int
  %before_buf = call ptr @__kml_str_alloc(i64 %before_len)
  call ptr @memcpy(ptr %before_buf, ptr %s, i64 %before_len)
  %nullp = getelementptr i8, ptr %before_buf, i64 %before_len
  store i8 0, ptr %nullp, align 1
  ; TDD-00120: after is a fresh length-prefixed copy (not an interior pointer
  ; into s), so both halves behave like every other length-prefixed string.
  %after_start = getelementptr i8, ptr %found, i64 %sep_len
  %after_len = call i64 @strlen(ptr %after_start)
  %after_buf = call ptr @__kml_str_alloc(i64 %after_len)
  %after_len1 = add i64 %after_len, 1
  call ptr @memcpy(ptr %after_buf, ptr %after_start, i64 %after_len1)
  %r2 = insertvalue {ptr, ptr} undef, ptr %before_buf, 0
  %r3 = insertvalue {ptr, ptr} %r2, ptr %after_buf, 1
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
  %hblen = call i64 @strlen(ptr %headerBlock)
  %crlflen = call i64 @strlen(ptr ` + crlf + `)
  %lines = call {ptr, i64} @__kml_split(ptr %headerBlock, i64 %hblen, ptr ` + crlf + `, i64 %crlflen)
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
  %qlen = call i64 @strlen(ptr %q)
  %amplen = call i64 @strlen(ptr ` + amp + `)
  %pairs = call {ptr, i64} @__kml_split(ptr %q, i64 %qlen, ptr ` + amp + `, i64 %amplen)
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
  ; NUL-terminate up front: with an empty map the fill loop never runs, so
  ; nothing else would ever write the terminator and send_response's strlen
  ; would walk uninitialized heap (glibc hands back dirty memory; macOS's
  ; zeroed small allocations masked this). The last sprintf below writes its
  ; own NUL at this same final offset, so pre-writing is always correct.
  %endp = getelementptr i8, ptr %buf, i64 %total
  store i8 0, ptr %endp, align 1
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
//	__kml_http_bind_and_listen(i32 port, i32 hostaddr, i32 backlog) -> i32
//	  hostaddr is a network-order IPv4 s_addr word (0 = INADDR_ANY, the
//	  default); backlog is the listen(2) queue depth (128 by default). Both
//	  come from a Node server.listen({ host, backlog }) options literal
//	  (ADR-00801); klain:http's http.listen always passes 0/128.
//	  socket()+setsockopt(SO_REUSEADDR)+bind()+listen(); throws a catchable
//	  Error (via __kml_http_throw) on any failure instead of returning -1,
//	  so the Go-emitted call site never needs its own error check.
//	__kml_http_send_response(i32 connfd, i64 status, ptr body, i64 bodylen, ptr extraHeaders)
//	  Formats a minimal HTTP/1.1 response header block (fixed "OK" reason
//	  phrase regardless of status — real clients determine success/failure
//	  from the numeric code, not the phrase) with Content-Length: bodylen/
//	  Connection: close plus extraHeaders (empty string if the handler's
//	  return type has no `headers` field — see ensureHTTPSerializeHeaders),
//	  writes it, then writes body[0:bodylen] via a second, explicit-length
//	  write() (not folded into the header sprintf — see this function's own
//	  doc comment below for why), closes the connection. bodylen is
//	  caller-supplied — either body's own strlen, or (TDD-00026/ADR-00106)
//	  an ArrayBuffer's real byteLength when the handler's return type has a
//	  `bodyBytes` field — so a binary response body with an embedded null
//	  byte reaches the socket whole instead of being silently truncated.
//	__kml_event_loop_run()
//	  The one event loop, in loopsrc/loop.c (TDD-00244).
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
	ctxSize, _, _, _ := e.ucontextLayout()
	e.emitGlobal(fmt.Sprintf("@__kml_main_ctx = thread_local global [%d x i8] zeroinitializer, align 16", ctxSize))
	e.emitGlobal("@__kml_conn_data = thread_local global ptr null, align 8")
	e.emitGlobal("@__kml_conn_len = thread_local global i64 0, align 8")
	e.emitGlobal("@__kml_conn_cap = thread_local global i64 0, align 8")
	e.emitGlobal("@__kml_current_conn_idx = thread_local global i64 -1, align 8")
	e.emitGlobal("@__kml_conn_active = thread_local global i64 0, align 8")
	// ADR-00986: set whenever a connection fiber was just resumed/run (append or
	// resume-scan). A fiber that ran may have written a response an in-process
	// http client (same curl-multi, same loop) now needs to read — so the very
	// next select() must not block: it forces one non-blocking poll so the
	// following docurlperform delivers those bytes. Without it, an async handler
	// that yielded (a task-shaped await) and then wrote its response left the loop
	// blocking in select() with no JS timer and an idle curl deadline, and the
	// in-process client's completion reaction was never fired (the process hung).
	// Consumed and cleared in the select-timeout decision (one poll per run).
	e.emitGlobal("@__kml_conn_ran = thread_local global i8 0, align 1")
	// TDD-00195 Stage 2 (Layer A backpressure): a connection fiber parked in
	// __kml_res_write_all on EAGAIN marks its fd write-parked (loop.c's
	// growable set, any descriptor number); the event loop waits for it to be
	// writable and resumes the fiber, so a slow-consuming client parks that one
	// fiber instead of blocking the whole reactor in a synchronous write().
	e.emitGlobal("declare void @__kml_conn_park_write(i32, i32)")
	// TDD-00197: the primary server's `'close'` event + deferred `close(cb)`.
	// __kml_http_close() clears the listener immediately but lets in-flight
	// connections drain; the real 'close' moment is when both the listener is
	// gone and the last connection has finished. These hold the registered
	// handlers and a "close requested, not yet fired" flag; the reactor calls
	// __kml_http_fire_close() each iteration, which fires exactly once at drain.
	e.emitGlobal("@__kml_http_close_cb = internal thread_local global ptr null, align 8")
	e.emitGlobal("@__kml_http_close_evt = internal thread_local global ptr null, align 8")
	// TDD-00215: the server 'clientError' listener (primary server only in V1),
	// read by buildHTTPDispatcher's noReqL protocol-error path.
	e.emitGlobal("@__kml_http_clienterror_evt = internal thread_local global ptr null, align 8")
	// TDD-00215 Stage 2: the server 'error' listener (primary server only in V1),
	// read inside __kml_http_bind_and_listen's failure edges so a bind failure
	// (EADDRINUSE/EACCES) routes to it instead of throwing.
	e.emitGlobal("@__kml_http_error_evt = internal thread_local global ptr null, align 8")
	e.emitGlobal("@__kml_http_close_pending = internal thread_local global i64 0, align 8")
}

// emitHTTPBindErrorFireHelper defines @__kml_http_fire_bind_error(errno): builds
// an Error carrying a Node-style message ("listen EADDRINUSE: <strerror>"), the
// bucketed .code ('EADDRINUSE'/'EACCES'/…) and numeric errno, then invokes the
// registered server 'error' listener `{fn, env}` as `fn(env, err)` (TDD-00215
// Stage 2). Only reached from __kml_http_bind_and_listen's failure edges after
// they confirm @__kml_http_error_evt is non-null, so no null-guard here.
func (e *Emitter) emitHTTPBindErrorFireHelper() {
	if e.usedHTTPBindErrorFire {
		return
	}
	e.usedHTTPBindErrorFire = true
	e.ensureStrerror()
	e.ensureErrnoCode()
	e.ensureStrlen()
	e.ensureSprintf()
	e.ensureStrHeaderRuntime()
	e.emitStandaloneFunc("void @__kml_http_fire_bind_error(i32 %errno_val)", func() string {
		errmsg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @strerror(i32 %%errno_val)", errmsg))
		codeOrNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_errno_code(i32 %%errno_val)", codeOrNull))
		codeNN := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", codeNN, codeOrNull))
		// A bind failure is practically always EADDRINUSE/EACCES (both in the
		// errno-code table). Fall back to a headered "UNKNOWN" only if a stray
		// errno escapes the table — never the raw strerror() text, which has no
		// string header and would fault the moment err.code is concatenated.
		codeStr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", codeStr, codeNN, codeOrNull, e.internString("UNKNOWN")))
		// message = "listen <CODE>: <strerror>" in a headered buffer.
		mlen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", mlen, errmsg))
		clen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", clen, codeStr))
		sum := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", sum, mlen, clen))
		bufsize := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 16", bufsize, sum))
		buf := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_alloc(i64 %s)", buf, bufsize))
		e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, ptr %s, ptr %s)", buf, e.internString("listen %s: %s"), codeStr, errmsg))
		e.emitInstr(fmt.Sprintf("call void @__kml_str_finalize(ptr %s)", buf))
		// .errno is Node's negative errno; .errcode field carries the same.
		errnoNeg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_uv_errno(i32 %%errno_val)", errnoNeg))
		errnoD := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sitofp i32 %s to double", errnoD, errnoNeg))
		errobj := e.buildErrorObjWithCode(errorKindIDs["Error"], buf, e.internString("Error"), codeStr, errnoD, errmsg)
		h := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_http_error_evt, align 8", h))
		fpGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpGep, h))
		fp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpGep))
		envGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", envGep, h))
		env := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", env, envGep))
		e.emitInstr(fmt.Sprintf("call void %s(ptr %s, ptr %s)", fp, env, errobj))
		return "ret void"
	})
}

// ensureHTTPFireClose declares __kml_http_fire_close(): called once per reactor
// iteration, it fires the primary server's `'close'` event handler and the
// deferred `server.close(cb)` callback — but only once the listener is gone
// (`@__kml_listen_fd < 0`) AND every in-flight connection has drained
// (`@__kml_conn_active == 0`) AND a close was actually requested
// (`@__kml_http_close_pending != 0`). It clears the pending flag and both
// handler slots before invoking them, so it never re-fires and a handler that
// itself re-enters the reactor sees a clean slate. Each handler is a
// zero-argument closure `{ fn, env }` invoked as `fn(env)`. Declared
// unconditionally from ensureHTTPRuntime (the guard is three cheap loads per
// idle iteration when no close was requested), so no whole-program pre-scan is
// needed to decide whether the reactor references it.
func (e *Emitter) ensureHTTPFireClose() {
	if e.usedHTTPFireClose {
		return
	}
	e.usedHTTPFireClose = true
	e.emitGlobal(`
define void @__kml_http_fire_close() {
entry:
  %pend = load i64, ptr @__kml_http_close_pending, align 8
  %requested = icmp ne i64 %pend, 0
  br i1 %requested, label %chkdrain, label %done
chkdrain:
  %lfd = load i32, ptr @__kml_listen_fd, align 4
  %gone = icmp slt i32 %lfd, 0
  %active = load i64, ptr @__kml_conn_active, align 8
  %drained = icmp eq i64 %active, 0
  %ready = and i1 %gone, %drained
  br i1 %ready, label %fire, label %done
fire:
  ; Clear state up front so a handler re-entering the reactor can't re-fire.
  store i64 0, ptr @__kml_http_close_pending, align 8
  %evt = load ptr, ptr @__kml_http_close_evt, align 8
  %cb = load ptr, ptr @__kml_http_close_cb, align 8
  store ptr null, ptr @__kml_http_close_evt, align 8
  store ptr null, ptr @__kml_http_close_cb, align 8
  ; 'close' event listener first (registration order: .on('close') before the
  ; close(cb) one-shot, the common shape), then the close(cb) callback.
  %hasevt = icmp ne ptr %evt, null
  br i1 %hasevt, label %fireevt, label %chkcb
fireevt:
  %efn = load ptr, ptr %evt, align 8
  %eenvp = getelementptr { ptr, ptr }, ptr %evt, i32 0, i32 1
  %eenv = load ptr, ptr %eenvp, align 8
  call void %efn(ptr %eenv)
  br label %chkcb
chkcb:
  %hascb = icmp ne ptr %cb, null
  br i1 %hascb, label %firecb, label %done
firecb:
  %cfn = load ptr, ptr %cb, align 8
  %cenvp = getelementptr { ptr, ptr }, ptr %cb, i32 0, i32 1
  %cenv = load ptr, ptr %cenvp, align 8
  call void %cfn(ptr %cenv)
  br label %done
done:
  ret void
}`)
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
	e.ensureMmapDecl()
	e.emitGlobal("@__kml_cluster_worker_id = internal global i64 0, align 8")
	// The primary's pid in a forked worker (0 in the primary and in a
	// single-process program): what the worker's orphan poll compares its parent
	// against (httpClusterOrphanIR).
	e.emitGlobal("@__kml_cluster_primary_pid = global i32 0, align 4")
	e.ensureGetpid()
	e.ensureHTTPClusterSeed()
	// TDD-00117: shared close flags. Null until __kml_http_cluster_fork mmaps a
	// MAP_SHARED region (only when it actually forks workers); every worker's
	// event loop polls it. Two i64 words: [0] set by http.close() (close the
	// listener), [1] set by http.closeAllConnections() (force-close in-flight
	// connections). Null in a single-process program, where every access below
	// is a guarded no-op.
	e.emitGlobal("@__kml_cluster_close_flag = global ptr null, align 8")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_http_cluster_fork(i64 %%numWorkers, i32 %%lfd) {
entry:
%s  %%ip = alloca i64, align 8
  store i64 1, ptr %%ip, align 8
  %%needsfork = icmp sgt i64 %%numWorkers, 1
  br i1 %%needsfork, label %%mkflag, label %%done

mkflag:
  ; Allocate the shared close-flag page (two i64 words) BEFORE forking so every
  ; worker inherits the same physical mapping. MAP_FAILED ((void*)-1) leaves the
  ; flag null — cross-worker close degrades to the caller's own process, no boot
  ; fail.
%s  %%mmfail = icmp eq ptr %%mm, inttoptr (i64 -1 to ptr)
  br i1 %%mmfail, label %%forkloop, label %%storeflag

storeflag:
  store i64 0, ptr %%mm, align 8
  %%mmw1 = getelementptr i64, ptr %%mm, i64 1
  store i64 0, ptr %%mmw1, align 8
  store ptr %%mm, ptr @__kml_cluster_close_flag, align 8
  br label %%forkloop

forkloop:
  %%i = load i64, ptr %%ip, align 8
  %%cont = icmp slt i64 %%i, %%numWorkers
  br i1 %%cont, label %%doforkw, label %%done

doforkw:
%sparentnext:
  %%inext = add i64 %%i, 1
  store i64 %%inext, ptr %%ip, align 8
  br label %%forkloop

done:
  ret void
}`, e.httpClusterEntryIR(), e.httpClusterFlagIR(), e.httpClusterForkIR()))
}

// ensureHTTPDate declares __kml_http_date(ptr %buf): formats the current wall
// time into %buf as an RFC 7231 IMF-fixdate ("Sun, 06 Nov 1994 08:49:37 GMT"),
// the exact shape Node stamps on every response's `Date` header. Uses
// gmtime()+strftime() (both libc, both platforms); the caller supplies a
// >=40-byte buffer. The C locale's %a/%b abbreviations are the English
// day/month names RFC 7231 mandates.
func (e *Emitter) ensureHTTPDate() {
	if e.usedHTTPDate {
		return
	}
	e.usedHTTPDate = true
	e.ensureTime()
	e.emitGlobal("declare ptr @gmtime(ptr noundef)")
	e.emitGlobal("declare i64 @strftime(ptr noundef, i64 noundef, ptr noundef, ptr noundef)")
	dateFmt := e.internString("%a, %d %b %Y %H:%M:%S GMT")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_http_date(ptr %%buf) {
entry:
  %%tp = alloca i64, align 8
  %%t = call i64 @time(ptr null)
  store i64 %%t, ptr %%tp, align 8
  %%tm = call ptr @gmtime(ptr %%tp)
  call i64 @strftime(ptr %%buf, i64 40, ptr %s, ptr %%tm)
  ret void
}`, dateFmt))
}

// ensureHTTPKeepAliveDecision declares __kml_http_keepalive_decision(ptr
// reqHeaders, ptr extraHeaders) -> i1: HTTP/1.1 defaults to a persistent
// connection, so this returns 1 (keep-alive) unless either the client sent a
// `Connection: close` request header (reqHeaders is the lowercased-key map the
// dispatcher already built) or the handler set `Connection: close` on the
// response (extraHeaders is the already-serialized "Key: Value\r\n" block).
// strcasestr matches case-insensitively (both a raw `close` token in the
// request value and a `Connection: close` line in the response block); it is
// present on glibc and Darwin libc alike.
func (e *Emitter) ensureHTTPKeepAliveDecision() {
	if e.usedHTTPKeepAlive {
		return
	}
	e.usedHTTPKeepAlive = true
	e.ensureMapStrHelpers()
	e.emitGlobal("declare ptr @strcasestr(ptr noundef, ptr noundef)")
	connKey := e.internString("connection")
	closeTok := e.internString("close")
	connClose := e.internString("connection: close")
	e.emitGlobal(fmt.Sprintf(`
define i1 @__kml_http_keepalive_decision(ptr %%reqhdrs, ptr %%extra) {
entry:
  ; server.close() already ran (listener gone) → don't hold this connection
  ; open waiting for a next request that can never be dispatched; Node >=19
  ; likewise closes idle keep-alive connections on close().
  %%lfd = load i32, ptr @__kml_listen_fd, align 4
  %%listening = icmp sge i32 %%lfd, 0
  br i1 %%listening, label %%chkextra, label %%no
chkextra:
  %%exhit = call ptr @strcasestr(ptr %%extra, ptr %s)
  %%exclose = icmp ne ptr %%exhit, null
  br i1 %%exclose, label %%no, label %%chkreq
chkreq:
  %%hasreq = icmp ne ptr %%reqhdrs, null
  br i1 %%hasreq, label %%getval, label %%yes
getval:
  %%has = call i1 @__kml_map_str_has(ptr %%reqhdrs, ptr %s)
  br i1 %%has, label %%loadval, label %%yes
loadval:
  %%vi = call i64 @__kml_map_str_get(ptr %%reqhdrs, ptr %s)
  %%vp = inttoptr i64 %%vi to ptr
  %%cl = call ptr @strcasestr(ptr %%vp, ptr %s)
  %%clhit = icmp ne ptr %%cl, null
  br i1 %%clhit, label %%no, label %%yes
yes:
  ret i1 1
no:
  ret i1 0
}`, connClose, connKey, connKey, closeTok))
}

func (e *Emitter) ensureHTTPRuntime() {
	if e.usedHTTP {
		return
	}
	e.usedHTTP = true
	e.ensureSigpipeIgnored() // __kml_http_bind_and_listen calls it at entry (TDD-00214)

	// The Node event loop / reactor is thread-affine: its ucontext connection
	// fibers cannot legally swapcontext across OS threads, and its per-loop
	// state lives in thread-local storage. When the loop runs inside a
	// klain:sync goroutine (e.g. `go(() => http.listen(...))`), a migrating
	// goroutine would resume on a different M — reading a *different* thread's
	// (default) reactor state and, e.g., seeing @__kml_listen_fd == -1 — so the
	// keep-alive check fails and http.listen returns out from under a live
	// server. Lock the goroutine to its M (Go's runtime.LockOSThread) at loop
	// entry to make it non-migrating (it still preempts, just on the same M).
	// __kml_reactor_thread_lock is a no-op unless klain:sync is linked and we're
	// actually on a goroutine.
	if e.usesSyncProgram {
		e.emitGlobal("declare void @klainsync_lock_os_thread()")
		e.emitGlobal(`
define void @__kml_reactor_thread_lock() {
  call void @klainsync_lock_os_thread()
  ret void
}`)
	} else {
		e.emitGlobal(`
define void @__kml_reactor_thread_lock() {
  ret void
}`)
	}
	e.ensureTimerRuntime()
	e.ensureFiberRuntime()
	e.ensureHTTPClientReactions() // defines __kml_httpc_fire_ready, called by the loop below
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
	e.ensureConnPokeGlobal() // the loop's conn scan + timeout decision read it
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
	e.ensureWriteDecl()
	e.ensureCloseDecl()
	e.emitGlobal("declare i32 @select(i32 noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i16 @htons(i16 noundef)")
	e.ensureFcntlDecl()
	e.ensureForkDecl()

	e.ensureListenFdGlobal()
	e.ensureHTTPFireClose() // the loop calls __kml_http_fire_close() each iteration
	e.emitGlobal("@__kml_listen_dispatch = thread_local global ptr null, align 8")
	e.emitGlobal("@__kml_listen_handler = internal thread_local global ptr null, align 8")
	// @__kml_listen_upgrade_handler (TDD-00158): the Node-faithful `'upgrade'`
	// event handler closure from `server.on('upgrade', …)`, null when none —
	// declared unconditionally like the others; the dispatcher's upgrade block
	// (gated at codegen time on e.usedHTTPUpgrade) reads it and, when non-null
	// on an `Upgrade:` request, builds (req, socket, head) and calls it.
	e.emitGlobal("@__kml_listen_upgrade_handler = internal thread_local global ptr null, align 8")
	// @__kml_listen_connection_handler (TDD-00198): the `'connection'` event
	// handler closure from `server.on('connection', …)`, null when none. The
	// dispatcher fires it once per connection at fiber entry (a cheap null-check,
	// so declared + read unconditionally — no pre-scan), handing it a net.Socket
	// over the accepted fd before HTTP parsing begins.
	e.emitGlobal("@__kml_listen_connection_handler = internal thread_local global ptr null, align 8")

	solSocket, soReuseAddr := e.httpSockConstants()
	fam0, fam1 := e.httpSockaddrFamilyBytes()

	// TDD-00215 Stage 2: a bind/listen failure routes to a registered server
	// 'error' listener (return -1) instead of throwing. errno is captured before
	// close() (which can clobber it) and handed to the fire helper.
	accessor := e.errnoAccessor()
	e.ensureErrnoAccessor()

	e.ensureListeningAnnounceHook()

	// A worker of Node's cluster (lib/node/cluster.ts) shares the port the
	// same way as one of http.listen({ workers })'s.
	e.ensureGetenv()
	nodeUniqueID := e.internString("NODE_UNIQUE_ID")

	e.emitGlobal(fmt.Sprintf(`
define i32 @__kml_http_bind_and_listen(i32 %%port, i32 %%hostaddr, i32 %%backlog) {
entry:
  ; TDD-00214: ignore SIGPIPE process-wide so a client disconnecting mid-write
  ; surfaces as a write() EPIPE (handled by unwinding the connection) instead of
  ; killing the whole server. One-shot; idempotent across multiple listeners.
  call void @__kml_ignore_sigpipe()
%s  %%fd = call i32 @socket(i32 2, i32 1, i32 0)
  %%fdok = icmp sge i32 %%fd, 0
  br i1 %%fdok, label %%setopt, label %%failnofd

setopt:
  %%one = alloca i32, align 4
  store i32 1, ptr %%one, align 4
  call i32 @setsockopt(i32 %%fd, i32 %d, i32 %d, ptr %%one, i32 4)
  ; SO_REUSEPORT — set ONLY in a cluster worker (worker id != 0), so several
  ; workers can each bind the same port (kernel-distributed accepts). It is
  ; deliberately NOT set for an ordinary single server, where a second bind to
  ; an in-use port must still fail (that failure is a real, thrown error).
  %%wid = load i64, ptr @__kml_cluster_worker_id, align 8
  %%isworkerid = icmp ne i64 %%wid, 0
  %%nodeuid = call ptr @getenv(ptr `+nodeUniqueID+`)
  %%isnodeworker = icmp ne ptr %%nodeuid, null
  %%isworker = or i1 %%isworkerid, %%isnodeworker
  br i1 %%isworker, label %%reuseport, label %%afteropt
reuseport:
  call i32 @setsockopt(i32 %%fd, i32 %d, i32 %d, ptr %%one, i32 4)
  br label %%afteropt
afteropt:
  %%addr = alloca [16 x i8], align 4
  call ptr @memset(ptr %%addr, i32 0, i64 16)
  store i8 %d, ptr %%addr, align 1
  %%b1p = getelementptr i8, ptr %%addr, i64 1
  store i8 %d, ptr %%b1p, align 1
  %%portu16 = trunc i32 %%port to i16
  %%portn = call i16 @htons(i16 %%portu16)
  %%portp = getelementptr i8, ptr %%addr, i64 2
  store i16 %%portn, ptr %%portp, align 1
  ; sin_addr.s_addr (bytes 4..7): the requested host as a network-order IPv4
  ; word (0 = INADDR_ANY, the default when host is omitted). server.address()'s
  ; getsockname() reflects it back, so binding 127.0.0.1 reports 127.0.0.1.
  %%saddrp = getelementptr i8, ptr %%addr, i64 4
  store i32 %%hostaddr, ptr %%saddrp, align 1

  %%bindrc = call i32 @bind(i32 %%fd, ptr %%addr, i32 16)
  %%bindok = icmp eq i32 %%bindrc, 0
  br i1 %%bindok, label %%dolisten, label %%failwithfd

dolisten:
  %%listenrc = call i32 @listen(i32 %%fd, i32 %%backlog)
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
  ; listening-announce hook (null unless the cluster runtime armed it at
  ; startup): a worker tells the primary it bound, driving the cluster-level
  ; 'listening' event
  %%lhook = load ptr, ptr @__kml_http_listening_announce, align 8
  %%lhset = icmp ne ptr %%lhook, null
  br i1 %%lhset, label %%announce, label %%retfd
announce:
  call void %%lhook(i32 %%port)
  br label %%retfd
retfd:
  ret i32 %%fd

failwithfd:
  %%fwerrptr = call ptr @%s()
  %%fwerrv = load i32, ptr %%fwerrptr, align 4
  call i32 @close(i32 %%fd)
  %%fwh = load ptr, ptr @__kml_http_error_evt, align 8
  %%fwhas = icmp ne ptr %%fwh, null
  br i1 %%fwhas, label %%fwfire, label %%fwthrow
fwfire:
  call void @__kml_http_fire_bind_error(i32 %%fwerrv)
  ret i32 -1
fwthrow:
  call void @__kml_http_throw(ptr %s)
  unreachable

failnofd:
  %%nferrptr = call ptr @%s()
  %%nferrv = load i32, ptr %%nferrptr, align 4
  %%nfh = load ptr, ptr @__kml_http_error_evt, align 8
  %%nfhas = icmp ne ptr %%nfh, null
  br i1 %%nfhas, label %%nffire, label %%nfthrow
nffire:
  call void @__kml_http_fire_bind_error(i32 %%nferrv)
  ret i32 -1
nfthrow:
  call void @__kml_http_throw(ptr %s)
  unreachable
}`, e.httpListenInheritIR(), solSocket, soReuseAddr, solSocket, e.httpReusePortConst(), fam0, fam1, e.httpNonblockFlag(), accessor, e.internString("http.listen: failed to bind or listen"),
		accessor, e.internString("http.listen: failed to create socket")))

	e.emitHTTPBindErrorFireHelper()

	e.ensureHTTPClusterFork()
	// __kml_event_loop_run below unconditionally calls
	// @__kml_http_shutdown_local_conns in its cluster-flag poll (whether or not
	// this program ever calls http.closeAllConnections), so its definition must
	// exist — same "always pull in the machinery the loop references" reasoning
	// as ensureFetchAsync/ensurePromiseCombinators above.
	e.ensureHTTPCloseAllConns()

	// The connection fibers' start (__kml_http_append_conn) and the accepts
	// are loop.c's; ucontext_t's size and field offsets go in as cflags.
	e.emitGlobal(`declare void @__kml_http_append_conn(i32, ptr)
@__kml_http_xl_data = external thread_local global ptr, align 8
@__kml_http_xl_len = external thread_local global i64, align 8
declare void @__kml_http_register_extra_listener(i32, ptr, ptr, ptr)
declare i1 @__kml_http_xl_keepalive()
declare void @__kml_http_close_extra_listener(i32)`)

	// extraHeaders already ends in "\r\n" per entry (or is "" when the
	// handler's return type has no `headers` field — see
	// ensureHTTPSerializeHeaders/emitHTTPListen), so this format produces
	// byte-identical output to before extraHeaders existed when it's empty,
	// and a correct single blank-line header/body separator either way.
	//
	// bodylen is now caller-supplied (emit_http.go's buildHTTPDispatcher —
	// either body's own strlen, or bodyBytes' real byteLength, TDD-00026/
	// ADR-00106) rather than computed internally via strlen(body), and the
	// body itself is written via a second, explicit-length write() instead
	// of being folded into the header sprintf's own "%s" — sprintf's %s is
	// NUL-terminated-aware regardless of what Content-Length claims, so
	// folding a binary body in there would still truncate it at its first
	// embedded null byte even with a correct bodylen. Two writes (headers,
	// then raw body bytes) is what actually makes bodyBytes' promise of
	// surviving an embedded null byte true end to end, not just up to the
	// Content-Length header.
	// __kml_http_reason: the standard reason phrase for a status code
	// (ADR-00486 — previously every status rendered a fixed "OK"). Codes
	// outside the table fall back to "OK", which real clients ignore anyway.
	reasons := [][2]string{
		{"100", "Continue"}, {"101", "Switching Protocols"},
		{"200", "OK"}, {"201", "Created"}, {"202", "Accepted"}, {"204", "No Content"}, {"206", "Partial Content"},
		{"301", "Moved Permanently"}, {"302", "Found"}, {"303", "See Other"}, {"304", "Not Modified"}, {"307", "Temporary Redirect"}, {"308", "Permanent Redirect"},
		{"400", "Bad Request"}, {"401", "Unauthorized"}, {"403", "Forbidden"}, {"404", "Not Found"}, {"405", "Method Not Allowed"},
		{"406", "Not Acceptable"}, {"408", "Request Timeout"}, {"409", "Conflict"}, {"410", "Gone"}, {"411", "Length Required"},
		{"412", "Precondition Failed"}, {"413", "Payload Too Large"}, {"414", "URI Too Long"}, {"415", "Unsupported Media Type"},
		{"418", "I'm a Teapot"}, {"422", "Unprocessable Entity"}, {"429", "Too Many Requests"},
		{"500", "Internal Server Error"}, {"501", "Not Implemented"}, {"502", "Bad Gateway"}, {"503", "Service Unavailable"},
		{"504", "Gateway Timeout"}, {"505", "HTTP Version Not Supported"},
	}
	var swCases, swBlocks strings.Builder
	for i, rp := range reasons {
		swCases.WriteString(fmt.Sprintf("    i64 %s, label %%r%d\n", rp[0], i))
		swBlocks.WriteString(fmt.Sprintf("r%d:\n  ret ptr %s\n", i, e.internString(rp[1])))
	}
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_http_reason(i64 %%status) {
entry:
  switch i64 %%status, label %%dflt [
%s  ]
%sdflt:
  ret ptr %s
}`, swCases.String(), swBlocks.String(), e.internString("OK")))

	// Node stamps a `Date` header on every response and, for HTTP/1.1, keeps the
	// connection persistent by default. The %s connection line is selected at
	// runtime from the caller's keepalive flag (see __kml_http_keepalive_decision):
	// `Connection: keep-alive` + `Keep-Alive: timeout=5` when persistent, plain
	// `Connection: close` when not. Date and connection line are the two
	// per-response headers real clients (and Node's own test suite) expect.
	respFmt := e.internString("HTTP/1.1 %lld %s\r\nDate: %s\r\nContent-Length: %lld\r\n%s%s\r\n")
	kaLine := e.internString("Connection: keep-alive\r\nKeep-Alive: timeout=5\r\n")
	closeLine := e.internString("Connection: close\r\n")
	e.ensureHTTPDate()
	writeHdr := "call i64 @write(i32 %connfd, ptr %respbuf, i64 %n64)"
	writeBody := "call i64 @write(i32 %connfd, ptr %body, i64 %bodylen)"
	closeConn := "call i32 @close(i32 %connfd)"
	// The connection is closed here only on the non-keepalive path; a keepalive
	// response leaves the fd open for the dispatcher's next-request read loop.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_http_send_response(i32 %%connfd, i64 %%status, ptr %%body, i64 %%bodylen, ptr %%extraHeaders, i1 %%keepalive) {
entry:
  %%reason = call ptr @__kml_http_reason(i64 %%status)
  %%datebuf = alloca [40 x i8], align 1
  call void @__kml_http_date(ptr %%datebuf)
  %%connline = select i1 %%keepalive, ptr %s, ptr %s
  %%hdrlen = call i64 @strlen(ptr %%extraHeaders)
  %%bufsize1 = add i64 %%hdrlen, 256
  %%respbuf = call ptr @malloc(i64 %%bufsize1)
  %%n = call i32 (ptr, ptr, ...) @sprintf(ptr %%respbuf, ptr %s, i64 %%status, ptr %%reason, ptr %%datebuf, i64 %%bodylen, ptr %%connline, ptr %%extraHeaders)
  %%n64 = sext i32 %%n to i64
  %s
  call void @free(ptr %%respbuf)
  %s
  br i1 %%keepalive, label %%done, label %%doclose
doclose:
  %s
  br label %%done
done:
  ret void
}`, kaLine, closeLine, respFmt, writeHdr, writeBody, closeConn))

	// gc mode: the loop (loopsrc/loop.c) points GC_stackbottom at a resumed
	// fiber's stack and back after the swap (ADR-00071), the same way
	// __kml_http_append_conn does for a new fiber; which way to set it is
	// gcSBStore's choice, passed as KML_GC_SB.
	ucSize, ucSP, ucSSize, ucLink := e.ucontextLayout()
	e.loopCFlags = []string{fmt.Sprintf("-DKML_FIBER_STACK=%d", fiberStackBytes),
		fmt.Sprintf("-DKML_UC_SIZE=%d", ucSize), fmt.Sprintf("-DKML_UC_SP=%d", ucSP),
		fmt.Sprintf("-DKML_UC_SSIZE=%d", ucSSize), fmt.Sprintf("-DKML_UC_LINK=%d", ucLink)}
	if e.isGCMode() {
		e.gcSBStore("%p") // declares what the chosen way needs
		mode := 1
		if e.opts.Target.OS() == "windows" {
			mode = 3
		} else if e.hasWorkers {
			mode = 2
		}
		e.loopCFlags = append(e.loopCFlags, fmt.Sprintf("-DKML_GC_SB=%d", mode))
	}
	if e.opts.Target.OS() != "windows" {
		e.loopCFlags = append(e.loopCFlags, "-DKML_CLUSTER_ORPHAN")
	}

	e.ensureLoopTurn()
	e.usedEventLoop = true
	e.emitGlobal("declare void @__kml_event_loop_run()")
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
	e.emitGlobal("declare void @__kml_http_close()") // httpsrv.c
}

// ensureHTTPCloseAllConns declares http.closeAllConnections() (TDD-00118): the
// forceful counterpart to http.close()'s graceful drain — it terminates every
// in-flight connection instead of letting it finish. Rather than tear down each
// connection's suspended fiber (a ucontext_t + malloc'd stack, parked mid-read)
// from outside — the dangerous path — it shutdown(fd, SHUT_RDWR)s each active
// connection's socket. The socket then reads EOF, so the fiber unwinds through
// its own existing end-of-request finish path (read()==0 → close + active--) the
// next time the event loop resumes it, which happens promptly since a shutdown
// socket becomes readable immediately. No external fiber/stack teardown.
//
// __kml_http_shutdown_local_conns does the local pass; __kml_http_close_all_conns
// is the public entry, which first sets the shared force-close word (word[1] of
// the cluster flag region, TDD-00117) so every worker force-closes its own
// connections too — symmetric with how http.close() propagates. Both are no-ops
// when there are no active connections / no cluster.
func (e *Emitter) ensureHTTPCloseAllConns() {
	if e.usedHTTPCloseAllConns {
		return
	}
	e.usedHTTPCloseAllConns = true
	e.emitGlobal(`declare void @__kml_http_shutdown_local_conns()
declare void @__kml_http_close_all_conns()`) // httpsrv.c
}

// ensureListeningAnnounceHook defines @__kml_http_listening_announce once —
// the null-until-armed hook __kml_http_bind_and_listen fires on a successful
// bind. Shared by the HTTP runtime (the call site) and the cluster runtime
// (which arms it at startup with the worker's announce function), either of
// which may be emitted without the other.
func (e *Emitter) ensureListeningAnnounceHook() {
	if e.usedListeningAnnounceHook {
		return
	}
	e.usedListeningAnnounceHook = true
	e.emitGlobal("@__kml_http_listening_announce = internal global ptr null, align 8")
}
