// runtime_http_stream.go — TDD-00097 Stage 5: chunked HTTP responses from a
// ReadableStream body. When a handler's `body` field is statically a
// ReadableStream, the connection dispatcher sends the chunked response head
// and starts an %kml.hws writer context, then returns — the writer is driven
// entirely by promise reactions (read → write framed chunk → read …), so a
// slow producer never blocks other connections; the connection's fd is owned
// by the writer, which closes it (and decrements the active-connection
// count http.close() watches) when the stream ends.
//
// %kml.hws (40 B): 0 fd i64 · 1 stream ptr · 2 decode thunk ptr · 3 curProm
// ptr · 4 isText i64 (1 ⇒ chunk length via strlen, 0 ⇒ the {ptr,len} word).
package llvm

import (
	"fmt"
	"strings"
)

func (e *Emitter) ensureHTTPStreamRuntime() {
	if e.usedHTTPStreamRuntime {
		return
	}
	e.usedHTTPStreamRuntime = true
	e.ensureStreamRuntime()
	e.ensureStreamPipeRuntime() // __kml_mkclo
	e.ensureMalloc()
	e.ensureStrlen()

	// fcntl is already declared by the http runtime this always rides on.
	// Chunked responses stamp a Date header like the buffered path, but stay
	// `Connection: close` (the writer closes the fd at stream end).
	e.ensureHTTPDate()
	headFmt := e.internString("HTTP/1.1 %lld OK\r\nDate: %s\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n%s\r\n")
	chunkFmt := e.internString("%llx\r\n")
	crlf := e.internString("\r\n")
	terminator := e.internString("0\r\n\r\n")
	hws := "{ i64, ptr, ptr, ptr, i64 }"

	streamBlock := fmt.Sprintf(`
define void @__kml_http_send_stream_head(i32 %%connfd, i64 %%status, ptr %%extraHeaders) {
entry:
  %%hdrlen = call i64 @strlen(ptr %%extraHeaders)
  %%bufsize = add i64 %%hdrlen, 160
  %%buf = call ptr @malloc(i64 %%bufsize)
  %%datebuf = alloca [40 x i8], align 1
  call void @__kml_http_date(ptr %%datebuf)
  %%n = call i32 (ptr, ptr, ...) @sprintf(ptr %%buf, ptr %s, i64 %%status, ptr %%datebuf, ptr %%extraHeaders)
  %%n64 = sext i32 %%n to i64
  %%ign = call i64 @write(i32 %%connfd, ptr %%buf, i64 %%n64)
  call void @free(ptr %%buf)
  ret void
}

define void @__kml_hws_start(i64 %%fd, ptr %%stream, ptr %%decode, i64 %%isText) {
entry:
  ; Clear O_NONBLOCK for the writer's lifetime (the fd is closed when the
  ; stream ends): chunk writes block instead of silently dropping on
  ; EAGAIN — the same fully-blocking write() semantics the buffered
  ; response path already has.
  %%fdi = trunc i64 %%fd to i32
  %%fl = call i32 (i32, i32, ...) @fcntl(i32 %%fdi, i32 3)
  %%flnb = and i32 %%fl, %s
  %%ign0 = call i32 (i32, i32, ...) @fcntl(i32 %%fdi, i32 4, i32 %%flnb)
  %%ctx = call ptr @malloc(i64 40)
  %%f0 = getelementptr %s, ptr %%ctx, i32 0, i32 0
  store i64 %%fd, ptr %%f0, align 8
  %%f1 = getelementptr %s, ptr %%ctx, i32 0, i32 1
  store ptr %%stream, ptr %%f1, align 8
  %%f2 = getelementptr %s, ptr %%ctx, i32 0, i32 2
  store ptr %%decode, ptr %%f2, align 8
  %%f3 = getelementptr %s, ptr %%ctx, i32 0, i32 3
  store ptr null, ptr %%f3, align 8
  %%f4 = getelementptr %s, ptr %%ctx, i32 0, i32 4
  store i64 %%isText, ptr %%f4, align 8
  call void @__kml_hws_step(ptr %%ctx)
  ret void
}

define void @__kml_hws_step(ptr %%ctx) {
entry:
  %%f1 = getelementptr %s, ptr %%ctx, i32 0, i32 1
  %%stream = load ptr, ptr %%f1, align 8
  %%p = call ptr @__kml_rs_read(ptr %%stream)
  %%f3 = getelementptr %s, ptr %%ctx, i32 0, i32 3
  store ptr %%p, ptr %%f3, align 8
  %%clo = call ptr @__kml_mkclo(ptr @__kml_hws_on_read, ptr %%ctx)
  call void @__kml_promise_add_reaction(ptr %%p, ptr %%clo)
  ret void
}

define void @__kml_hws_on_read(ptr %%ctx) {
entry:
  %%f0 = getelementptr %s, ptr %%ctx, i32 0, i32 0
  %%fd64 = load i64, ptr %%f0, align 8
  %%fd = trunc i64 %%fd64 to i32
  %%f3 = getelementptr %s, ptr %%ctx, i32 0, i32 3
  %%p = load ptr, ptr %%f3, align 8
  %%pst_p = getelementptr %s, ptr %%p, i32 0, i32 0
  %%pst = load i64, ptr %%pst_p, align 8
  %%rejected = icmp eq i64 %%pst, 2
  br i1 %%rejected, label %%finish, label %%ok
ok:
  %%rv0_p = getelementptr %s, ptr %%p, i32 0, i32 2
  %%recBits = load i64, ptr %%rv0_p, align 8
  %%rec = inttoptr i64 %%recBits to ptr
  %%f2 = getelementptr %s, ptr %%ctx, i32 0, i32 2
  %%decode = load ptr, ptr %%f2, align 8
  %%dv = call { i64, i64, i64 } %%decode(ptr %%rec)
  %%v0 = extractvalue { i64, i64, i64 } %%dv, 0
  %%v1 = extractvalue { i64, i64, i64 } %%dv, 1
  %%done = extractvalue { i64, i64, i64 } %%dv, 2
  %%isDone = icmp ne i64 %%done, 0
  br i1 %%isDone, label %%term, label %%chunk
term:
  %%ignt = call i64 @write(i32 %%fd, ptr %s, i64 5)
  br label %%finish
chunk:
  %%data = inttoptr i64 %%v0 to ptr
  %%f4 = getelementptr %s, ptr %%ctx, i32 0, i32 4
  %%isText = load i64, ptr %%f4, align 8
  %%text = icmp ne i64 %%isText, 0
  br i1 %%text, label %%textlen, label %%binlen
textlen:
  %%slen = call i64 @strlen(ptr %%data)
  br label %%havelen
binlen:
  br label %%havelen
havelen:
  %%len = phi i64 [ %%slen, %%textlen ], [ %%v1, %%binlen ]
  ; A zero-length chunk would be the chunked-encoding terminator — skip it.
  %%empty = icmp eq i64 %%len, 0
  br i1 %%empty, label %%next, label %%dowrite
dowrite:
  %%hdrbuf = call ptr @malloc(i64 32)
  %%hn = call i32 (ptr, ptr, ...) @sprintf(ptr %%hdrbuf, ptr %s, i64 %%len)
  %%hn64 = sext i32 %%hn to i64
  %%ign1 = call i64 @write(i32 %%fd, ptr %%hdrbuf, i64 %%hn64)
  call void @free(ptr %%hdrbuf)
  %%ign2 = call i64 @write(i32 %%fd, ptr %%data, i64 %%len)
  %%ign3 = call i64 @write(i32 %%fd, ptr %s, i64 2)
  br label %%next
next:
  call void @__kml_hws_step(ptr %%ctx)
  ret void
finish:
  %%ign4 = call i32 @close(i32 %%fd)
  %%active = load i64, ptr @__kml_conn_active, align 8
  %%active2 = sub i64 %%active, 1
  store i64 %%active2, ptr @__kml_conn_active, align 8
  ret void
}`, headFmt, fmt.Sprintf("%d", ^httpNonblockFlag()), hws, hws, hws, hws, hws, hws, hws, hws, hws, promiseStructIR, promiseStructIR, hws, terminator, hws, chunkFmt, crlf)

	// Over an HTTPS/1.1 connection the fd is TLS-wrapped: route the chunk writes
	// and the final close through the SSL-aware shims (a plain fd falls through
	// to raw write/close inside them). Without this a streaming body would emit
	// plaintext frames onto the TLS socket.
	if e.usedHTTPS1Server {
		e.emitHTTPSConnShims()
		streamBlock = strings.ReplaceAll(streamBlock, "call i64 @write(i32 %connfd,", "call i64 @__kml_http_conn_send(i32 %connfd,")
		streamBlock = strings.ReplaceAll(streamBlock, "call i64 @write(i32 %fd,", "call i64 @__kml_http_conn_send(i32 %fd,")
		streamBlock = strings.ReplaceAll(streamBlock, "%ign4 = call i32 @close(i32 %fd)", "call void @__kml_http_conn_close(i32 %fd)")
	}
	e.emitGlobal(streamBlock)
}
