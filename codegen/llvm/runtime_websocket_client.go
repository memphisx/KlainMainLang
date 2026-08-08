package llvm

import (
	"fmt"
	"runtime"
)

// runtime_websocket_client.go — `new WebSocket(url)` C-runtime helpers
// (TDD-00039 Stage 3, `ws://` only). See emit_websocket_client.go for the
// Go-side URL parsing/validation/request-string-building this file's own
// IR is called from, and runtime_websocket.go (Stage 0, ADR-00125) for the
// shared frame codec/SHA-1 this file's scan step calls into unchanged.
//
// Unlike EventSource (whose connection is asynchronous by construction,
// riding libcurl's own non-blocking multi-interface), `new WebSocket(url)`
// performs its TCP connect + HTTP upgrade handshake *synchronously* — a
// deliberate V1 simplification avoiding new non-blocking-connect machinery
// in the event loop's hand-rolled select() call (extending it to also
// monitor a connect-in-progress fd's writability would be real, additional
// core-loop surgery, out of scope for this stage — see TDD-00039's Design
// section, which explicitly left this sequencing as "implementation
// detail, not a design fork"). Only the *ongoing* post-handshake read side
// is non-blocking and event-loop-integrated, via a new client-entry array
// mirroring EventSource's own "grow-only array, scanned every iteration,
// not fiber-parked" shape (TDD-00038) — this is that pattern's fifth
// instance (timers, the connection-fiber array, fetch's curl multi-handle,
// EventSource, and now this).
//
// Entry struct (`{ i64 fd, i64 state, i64 pendingNotify, ptr buf, i64
// consumedOffset, ptr instance }`, 48 bytes): fd is -1 once the connection
// ends; state is 0=CONNECTING (never actually observed by the scan, since
// connect+handshake finish before an entry is ever appended — kept for
// naming symmetry with WSConnection/EventSource's own state numbering),
// 1=OPEN, 2=CLOSED. pendingNotify is the fix for a real ordering problem
// (see WebSocketClientType's own doc comment in types.go): `.onopen`/
// `.onerror`/`.onclose` can only be assigned *after* `new WebSocket(url)`
// returns, but the connection outcome is already known by then — so the
// *callback* firing is deferred to this entry's first scan pass (1 =
// "not yet notified", cleared to 0 once fired), even though state/readyState
// themselves reflect the real outcome immediately. buf is the same
// {ptr,i64,i64} growable accumulator shape used everywhere else in this
// codebase for a byte stream of unknown final length; consumedOffset tracks
// how much of it has been parsed into complete frames, exactly mirroring
// __kml_eventsource_process_available's own realloc-safety convention
// (reload buf's data pointer/length fresh every call, never cache a raw
// pointer across a scan boundary). instance is the KML-level WebSocket
// object pointer, the two-way link the scan needs to write readyState and
// call onmessage/onclose/onerror.
//
//	__kml_ws_client_connect(ptr host, i32 port) -> i32
//	  Resolves host to an IPv4 address (inet_pton first, for the common
//	  numeric-literal/already-an-IP case, no struct-layout risk at all;
//	  falling back to getaddrinfo for a real hostname — Linux-only for now,
//	  see below) and performs a *blocking* connect() on a fresh socket,
//	  returning the connected fd or -1 on any failure (invalid host,
//	  connection refused, etc.) — never throws itself, the caller decides
//	  what a failed connect means for the KML-level object.
//	__kml_ws_client_open(ptr host, i32 port, ptr request, i64 requestLen,
//	                      ptr expectedAccept, ptr instance) -> ptr
//	  The main entry point (emit_websocket_client.go's counterpart to
//	  __kml_eventsource_open): connects, sends the already-built HTTP
//	  upgrade request, blocking-reads the response looking for the
//	  \r\n\r\n header terminator, checks for "101" in the status line and
//	  the Sec-WebSocket-Accept header matching expectedAccept (computed
//	  Go-side via the same SHA-1+base64 chain the server side already
//	  uses), and either: (success) switches the fd to non-blocking, builds
//	  an OPEN entry whose buf starts pre-loaded with whatever bytes arrived
//	  past the header terminator in the same read (a real peer could
//	  pipeline a frame immediately after its own 101 response — dropping
//	  those bytes would be a real, if rare, data-loss bug), and registers
//	  it in the scanned array; or (any failure along the way) builds a
//	  CLOSED entry and registers it anyway, so the scan's deferred
//	  onerror+onclose still fires. Always returns a real entry pointer,
//	  never null — emit_websocket_client.go doesn't need to branch on
//	  success/failure itself, just read back the entry's own state.
//	__kml_wsclient_scan()
//	  Called once per __kml_event_loop_run iteration, mirroring
//	  __kml_eventsource_scan's own call site exactly. For each entry: fire
//	  the deferred onopen or onerror+onclose if pendingNotify is still set
//	  (first pass since __kml_ws_client_open registered it); otherwise, if
//	  OPEN, non-blocking-read available bytes and run the shared
//	  __kml_ws_frame_decode over the unconsumed tail exactly like
//	  emit_websocket.go's server-side loop does, dispatching text/binary
//	  frames to onmessage and a close frame to onclose (closing the fd and
//	  marking the entry CLOSED — no echo-back close handshake on the
//	  client side yet, a real, undesigned follow-on symmetric with the
//	  server's own Stage 2).
func (e *Emitter) ensureWSClientRuntime() {
	if e.usedWSClientRuntime {
		return
	}
	e.usedWSClientRuntime = true
	e.ensureHTTPRuntime()
	e.ensureWSFrameDecode()
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemset()
	e.ensureMemcpy()
	e.ensureFree()
	e.ensureStrlen()
	e.ensureStrstr()
	e.ensureStrncmp()
	e.ensureErrnoAccessor()

	e.emitGlobal("declare i32 @inet_pton(i32 noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @connect(i32 noundef, ptr noundef, i32 noundef)")
	if runtime.GOOS != "darwin" {
		e.emitGlobal("declare i32 @getaddrinfo(ptr noundef, ptr noundef, ptr noundef, ptr noundef)")
		e.emitGlobal("declare void @freeaddrinfo(ptr noundef)")
	}

	e.emitGlobal("@__kml_wsc_data = internal global ptr null, align 8")
	e.emitGlobal("@__kml_wsc_len = internal global i64 0, align 8")
	e.emitGlobal("@__kml_wsc_cap = internal global i64 0, align 8")
	e.emitGlobal("@__kml_wsc_active = internal global i64 0, align 8")

	e.emitWSClientConnect()
	e.emitWSClientOpen()
	e.emitWSClientScan()
}

// emitWSClientConnect declares __kml_ws_client_connect — see this file's
// own doc comment for the full design.
func (e *Emitter) emitWSClientConnect() {
	fam0, fam1 := httpSockaddrFamilyBytes()

	resolveIR := `
  %ptonrc = call i32 @inet_pton(i32 2, ptr %host, ptr %ipbuf)
  %ptonok = icmp eq i32 %ptonrc, 1
  br i1 %ptonok, label %haveaddr, label %tryresolve

tryresolve:`
	if runtime.GOOS == "darwin" {
		// Hostname resolution (getaddrinfo) is deliberately not attempted
		// on Darwin yet: struct addrinfo's own layout (as opposed to
		// sockaddr_in's, which IS POSIX-stable and already used elsewhere
		// in this codebase without a platform branch) isn't a standardized
		// byte layout, and this project's own standing rule is to verify
		// such an offset directly on the machine before shipping it rather
		// than trust it from memory — not yet done for Darwin. A numeric
		// IP host (the inet_pton fast path above) still works fine; a real
		// hostname fails cleanly here instead of risking a wrong-offset
		// struct read.
		resolveIR += `
  br label %fail`
	} else {
		resolveIR += `
  %hints = alloca [48 x i8], align 8
  call ptr @memset(ptr %hints, i32 0, i64 48)
  %hints_family = getelementptr i8, ptr %hints, i64 4
  store i32 2, ptr %hints_family, align 4
  %hints_socktype = getelementptr i8, ptr %hints, i64 8
  store i32 1, ptr %hints_socktype, align 4
  %resslot = alloca ptr, align 8
  store ptr null, ptr %resslot, align 8
  %gairc = call i32 @getaddrinfo(ptr %host, ptr null, ptr %hints, ptr %resslot)
  %gaiok = icmp eq i32 %gairc, 0
  br i1 %gaiok, label %extractaddr, label %fail

extractaddr:
  %res = load ptr, ptr %resslot, align 8
  %ai_addr_p = getelementptr i8, ptr %res, i64 24
  %ai_addr = load ptr, ptr %ai_addr_p, align 8
  %sin_addr_p = getelementptr i8, ptr %ai_addr, i64 4
  call ptr @memcpy(ptr %ipbuf, ptr %sin_addr_p, i64 4)
  call void @freeaddrinfo(ptr %res)
  br label %haveaddr`
	}

	e.emitGlobal(fmt.Sprintf(`
define i32 @__kml_ws_client_connect(ptr %%host, i32 %%port) {
entry:
  %%ipbuf = alloca [4 x i8], align 4
  call ptr @memset(ptr %%ipbuf, i32 0, i64 4)
%s

haveaddr:
  %%sockfd = call i32 @socket(i32 2, i32 1, i32 0)
  %%fdok = icmp sge i32 %%sockfd, 0
  br i1 %%fdok, label %%buildaddr, label %%fail

buildaddr:
  %%connaddr = alloca [16 x i8], align 4
  call ptr @memset(ptr %%connaddr, i32 0, i64 16)
  store i8 %d, ptr %%connaddr, align 1
  %%cb1p = getelementptr i8, ptr %%connaddr, i64 1
  store i8 %d, ptr %%cb1p, align 1
  %%portu16 = trunc i32 %%port to i16
  %%portn = call i16 @htons(i16 %%portu16)
  %%portp = getelementptr i8, ptr %%connaddr, i64 2
  store i16 %%portn, ptr %%portp, align 1
  %%ipdstp = getelementptr i8, ptr %%connaddr, i64 4
  call ptr @memcpy(ptr %%ipdstp, ptr %%ipbuf, i64 4)
  %%connrc = call i32 @connect(i32 %%sockfd, ptr %%connaddr, i32 16)
  %%connok = icmp eq i32 %%connrc, 0
  br i1 %%connok, label %%success, label %%failwithfd

success:
  ret i32 %%sockfd

failwithfd:
  call i32 @close(i32 %%sockfd)
  ret i32 -1

fail:
  ret i32 -1
}`, resolveIR, fam0, fam1))
}

// wsClientArrayAppend returns the IR snippet that grows (if needed) and
// appends entryReg to @__kml_wsc_data and increments @__kml_wsc_active —
// mirrors __kml_eventsource_open's own esgrow/esappend block shape
// (runtime_eventsource.go) exactly, just parameterized by a uniq label
// prefix since this snippet is inlined at three separate call sites within
// __kml_ws_client_open (one per outcome), each needing its own labels.
func wsClientArrayAppend(uniq, entryReg string) string {
	return fmt.Sprintf(`
  %%%[1]slen = load i64, ptr @__kml_wsc_len, align 8
  %%%[1]scap = load i64, ptr @__kml_wsc_cap, align 8
  %%%[1]sdata = load ptr, ptr @__kml_wsc_data, align 8
  %%%[1]sneedp1 = add i64 %%%[1]slen, 1
  %%%[1]sneedgrow = icmp sgt i64 %%%[1]sneedp1, %%%[1]scap
  br i1 %%%[1]sneedgrow, label %%%[1]sgrow, label %%%[1]sappend

%[1]sgrow:
  %%%[1]scap2 = mul i64 %%%[1]scap, 2
  %%%[1]satleast8 = icmp sgt i64 %%%[1]scap2, 8
  %%%[1]snewcap = select i1 %%%[1]satleast8, i64 %%%[1]scap2, i64 8
  %%%[1]snewcapbytes = mul i64 %%%[1]snewcap, 8
  %%%[1]snewdata = call ptr @realloc(ptr %%%[1]sdata, i64 %%%[1]snewcapbytes)
  store ptr %%%[1]snewdata, ptr @__kml_wsc_data, align 8
  store i64 %%%[1]snewcap, ptr @__kml_wsc_cap, align 8
  br label %%%[1]sappend

%[1]sappend:
  %%%[1]sdatanow = load ptr, ptr @__kml_wsc_data, align 8
  %%%[1]sslot = getelementptr ptr, ptr %%%[1]sdatanow, i64 %%%[1]slen
  store ptr %[2]s, ptr %%%[1]sslot, align 8
  %%%[1]snewlen = add i64 %%%[1]slen, 1
  store i64 %%%[1]snewlen, ptr @__kml_wsc_len, align 8
  %%%[1]scuractive = load i64, ptr @__kml_wsc_active, align 8
  %%%[1]snewactive = add i64 %%%[1]scuractive, 1
  store i64 %%%[1]snewactive, ptr @__kml_wsc_active, align 8
`, uniq, entryReg)
}

// wsClientEntryStructIR is the raw LLVM struct-type string for a client
// entry — see this file's own top doc comment for field meanings.
const wsClientEntryStructIR = "{ i64, i64, i64, ptr, i64, ptr }"

// emitWSClientOpen declares __kml_ws_client_open — see this file's own doc
// comment for the full design, including why a failed connect/handshake
// still builds and registers a (CLOSED) entry rather than returning null.
func (e *Emitter) emitWSClientOpen() {
	crlfcrlf := e.internString("\r\n\r\n")
	crlf := e.internString("\r\n")
	status101 := e.internString(" 101")
	acceptHeaderName := "Sec-WebSocket-Accept:"
	acceptHeaderNameRef := e.internString(acceptHeaderName)
	acceptHeaderNameLen := len(acceptHeaderName)
	nonblockFlag := httpNonblockFlag()

	closedEntryIR := func(uniq string) string {
		entryReg := "%" + uniq + "entry"
		s := fmt.Sprintf(`
  %[1]s = call ptr @malloc(i64 48)
  %%%[2]s_fd = getelementptr %[3]s, ptr %[1]s, i32 0, i32 0
  store i64 -1, ptr %%%[2]s_fd, align 8
  %%%[2]s_state = getelementptr %[3]s, ptr %[1]s, i32 0, i32 1
  store i64 2, ptr %%%[2]s_state, align 8
  %%%[2]s_pending = getelementptr %[3]s, ptr %[1]s, i32 0, i32 2
  store i64 1, ptr %%%[2]s_pending, align 8
  %%%[2]s_buf = getelementptr %[3]s, ptr %[1]s, i32 0, i32 3
  store ptr null, ptr %%%[2]s_buf, align 8
  %%%[2]s_consumed = getelementptr %[3]s, ptr %[1]s, i32 0, i32 4
  store i64 0, ptr %%%[2]s_consumed, align 8
  %%%[2]s_instance = getelementptr %[3]s, ptr %[1]s, i32 0, i32 5
  store ptr %%instance, ptr %%%[2]s_instance, align 8
`, entryReg, uniq, wsClientEntryStructIR)
		s += wsClientArrayAppend(uniq, entryReg)
		return s
	}

	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_ws_client_open(ptr %%host, i32 %%port, ptr %%request, i64 %%requestLen, ptr %%expectedAccept, ptr %%instance) {
entry:
  %%fd = call i32 @__kml_ws_client_connect(ptr %%host, i32 %%port)
  %%connfailed = icmp slt i32 %%fd, 0
  br i1 %%connfailed, label %%closedentry, label %%dohandshake

dohandshake:
  call i64 @write(i32 %%fd, ptr %%request, i64 %%requestLen)
  %%bufptrA = alloca ptr, align 8
  %%bufcapA = alloca i64, align 8
  %%buflenA = alloca i64, align 8
  %%initbuf = call ptr @malloc(i64 4096)
  store ptr %%initbuf, ptr %%bufptrA, align 8
  store i64 4096, ptr %%bufcapA, align 8
  store i64 0, ptr %%buflenA, align 8
  br label %%readloop

readloop:
  %%curcap = load i64, ptr %%bufcapA, align 8
  %%curlen = load i64, ptr %%buflenA, align 8
  %%remain = sub i64 %%curcap, %%curlen
  %%needgrow = icmp slt i64 %%remain, 1024
  br i1 %%needgrow, label %%growbuf, label %%doread

growbuf:
  %%newcap = mul i64 %%curcap, 2
  %%toobig = icmp sgt i64 %%newcap, 65536
  br i1 %%toobig, label %%handshakefail, label %%dogrow

dogrow:
  %%oldbuf = load ptr, ptr %%bufptrA, align 8
  %%newbuf = call ptr @realloc(ptr %%oldbuf, i64 %%newcap)
  store ptr %%newbuf, ptr %%bufptrA, align 8
  store i64 %%newcap, ptr %%bufcapA, align 8
  br label %%doread

doread:
  %%rbuf = load ptr, ptr %%bufptrA, align 8
  %%rlen = load i64, ptr %%buflenA, align 8
  %%rcap = load i64, ptr %%bufcapA, align 8
  %%rptr = getelementptr i8, ptr %%rbuf, i64 %%rlen
  %%rspace = sub i64 %%rcap, %%rlen
  %%rspacem1 = sub i64 %%rspace, 1
  %%n = call i64 @read(i32 %%fd, ptr %%rptr, i64 %%rspacem1)
  %%ngood = icmp sgt i64 %%n, 0
  br i1 %%ngood, label %%accum, label %%handshakefail

accum:
  %%newlen = add i64 %%rlen, %%n
  store i64 %%newlen, ptr %%buflenA, align 8
  %%abuf0 = load ptr, ptr %%bufptrA, align 8
  %%termp = getelementptr i8, ptr %%abuf0, i64 %%newlen
  store i8 0, ptr %%termp, align 1
  %%hdrend = call ptr @strstr(ptr %%abuf0, ptr %[1]s)
  %%found = icmp ne ptr %%hdrend, null
  br i1 %%found, label %%parseresp, label %%readloop

parseresp:
  %%pbuf = load ptr, ptr %%bufptrA, align 8
  %%has101 = call ptr @strstr(ptr %%pbuf, ptr %[2]s)
  %%has101ok = icmp ne ptr %%has101, null
  br i1 %%has101ok, label %%checkaccept, label %%handshakefail

checkaccept:
  %%acceptbuf = load ptr, ptr %%bufptrA, align 8
  %%accepthdr = call ptr @strstr(ptr %%acceptbuf, ptr %[3]s)
  %%hasaccepthdr = icmp ne ptr %%accepthdr, null
  br i1 %%hasaccepthdr, label %%extractaccept, label %%handshakefail

extractaccept:
  %%acceptvalstart0 = getelementptr i8, ptr %%accepthdr, i64 %[4]d
  %%maybespace = load i8, ptr %%acceptvalstart0, align 1
  %%isspace = icmp eq i8 %%maybespace, 32
  %%spaceskip = select i1 %%isspace, i64 1, i64 0
  %%acceptvalstart = getelementptr i8, ptr %%acceptvalstart0, i64 %%spaceskip
  %%acceptend = call ptr @strstr(ptr %%acceptvalstart, ptr %[5]s)
  %%hasacceptend = icmp ne ptr %%acceptend, null
  br i1 %%hasacceptend, label %%compareaccept, label %%handshakefail

compareaccept:
  %%avstarti = ptrtoint ptr %%acceptvalstart to i64
  %%avendi = ptrtoint ptr %%acceptend to i64
  %%avlen = sub i64 %%avendi, %%avstarti
  %%expectedlen = call i64 @strlen(ptr %%expectedAccept)
  %%samelen = icmp eq i64 %%avlen, %%expectedlen
  br i1 %%samelen, label %%strncmpaccept, label %%handshakefail

strncmpaccept:
  %%cmpres = call i32 @strncmp(ptr %%acceptvalstart, ptr %%expectedAccept, i64 %%avlen)
  %%matches = icmp eq i32 %%cmpres, 0
  br i1 %%matches, label %%openentry, label %%handshakefail

openentry:
  %%oebuf = load ptr, ptr %%bufptrA, align 8
  %%oetotallen = load i64, ptr %%buflenA, align 8
  %%hdrendi = ptrtoint ptr %%hdrend to i64
  %%oebufi = ptrtoint ptr %%oebuf to i64
  %%hdrendoff = sub i64 %%hdrendi, %%oebufi
  %%bodystart = add i64 %%hdrendoff, 4
  %%leftoverlen = sub i64 %%oetotallen, %%bodystart
  %%leftoversrc = getelementptr i8, ptr %%oebuf, i64 %%bodystart
  %%leftoverbuf = call ptr @malloc(i64 %%leftoverlen)
  call ptr @memcpy(ptr %%leftoverbuf, ptr %%leftoversrc, i64 %%leftoverlen)
  call void @free(ptr %%oebuf)
  %%curflags = call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 3)
  %%nbflags = or i32 %%curflags, %[10]d
  call i32 (i32, i32, ...) @fcntl(i32 %%fd, i32 4, i32 %%nbflags)
  %%bufstruct = call ptr @malloc(i64 24)
  %%bs_data = getelementptr { ptr, i64, i64 }, ptr %%bufstruct, i32 0, i32 0
  store ptr %%leftoverbuf, ptr %%bs_data, align 8
  %%bs_len = getelementptr { ptr, i64, i64 }, ptr %%bufstruct, i32 0, i32 1
  store i64 %%leftoverlen, ptr %%bs_len, align 8
  %%bs_cap = getelementptr { ptr, i64, i64 }, ptr %%bufstruct, i32 0, i32 2
  store i64 %%leftoverlen, ptr %%bs_cap, align 8
  %%fd64 = zext i32 %%fd to i64
  %%oe_entry = call ptr @malloc(i64 48)
  %%oe_fd = getelementptr %[6]s, ptr %%oe_entry, i32 0, i32 0
  store i64 %%fd64, ptr %%oe_fd, align 8
  %%oe_state = getelementptr %[6]s, ptr %%oe_entry, i32 0, i32 1
  store i64 1, ptr %%oe_state, align 8
  %%oe_pending = getelementptr %[6]s, ptr %%oe_entry, i32 0, i32 2
  store i64 1, ptr %%oe_pending, align 8
  %%oe_buf = getelementptr %[6]s, ptr %%oe_entry, i32 0, i32 3
  store ptr %%bufstruct, ptr %%oe_buf, align 8
  %%oe_consumed = getelementptr %[6]s, ptr %%oe_entry, i32 0, i32 4
  store i64 0, ptr %%oe_consumed, align 8
  %%oe_instance = getelementptr %[6]s, ptr %%oe_entry, i32 0, i32 5
  store ptr %%instance, ptr %%oe_instance, align 8
%[7]s
  ret ptr %%oe_entry

handshakefail:
  %%failbuf = load ptr, ptr %%bufptrA, align 8
  call void @free(ptr %%failbuf)
  call i32 @close(i32 %%fd)
%[8]s
  ret ptr %%hsfailentry

closedentry:
%[9]s
  ret ptr %%cfentry
}`,
		crlfcrlf, status101, acceptHeaderNameRef, acceptHeaderNameLen, crlf,
		wsClientEntryStructIR, wsClientArrayAppend("oe", "%oe_entry"),
		closedEntryIR("hsfail"), closedEntryIR("cf"),
		nonblockFlag))
}

// wsClientCallCallback returns the IR snippet that builds a
// WSMessageEventType() ({data: ptr}, a single 8-byte field — malloc+direct
// store, no GEP needed) carrying dataRef and calls the {ptr,ptr} closure
// header in closureReg with it — the one call shape __kml_wsclient_scan
// needs five times (onopen/onmessage/onclose/onerror, the last two counted
// once each from two different call sites: a fresh connection failure and
// a later mid-session close), factored out once rather than repeated
// five times by hand.
func wsClientCallCallback(uniq, closureReg, dataRef string) string {
	return fmt.Sprintf(`
  %%%[1]smsgevt = call ptr @malloc(i64 8)
  store ptr %[3]s, ptr %%%[1]smsgevt, align 8
  %%%[1]sfp_p = getelementptr { ptr, ptr }, ptr %[2]s, i32 0, i32 0
  %%%[1]sfp = load ptr, ptr %%%[1]sfp_p, align 8
  %%%[1]sep_p = getelementptr { ptr, ptr }, ptr %[2]s, i32 0, i32 1
  %%%[1]sep = load ptr, ptr %%%[1]sep_p, align 8
  call void %%%[1]sfp(ptr %%%[1]sep, ptr %%%[1]smsgevt)
`, uniq, closureReg, dataRef)
}

// emitWSClientScan declares __kml_wsclient_scan — see this file's own top
// doc comment for the full design. wsTy's field indices are looked up via
// FieldIndex rather than hardcoded, the same defensive habit every other
// object-shaped built-in type's emitter code already uses (WSConnectionType
// in emit_websocket.go, EventSourceType in emit_eventsource.go).
func (e *Emitter) emitWSClientScan() {
	wsTy := WebSocketClientType()
	wsStructIR := wsTy.StructIR()
	rsIdx, _, _ := wsTy.FieldIndex("readyState")
	onopenIdx, _, _ := wsTy.FieldIndex("onopen")
	onmessageIdx, _, _ := wsTy.FieldIndex("onmessage")
	oncloseIdx, _, _ := wsTy.FieldIndex("onclose")
	onerrorIdx, _, _ := wsTy.FieldIndex("onerror")

	emptyStr := e.internString("")
	entryTy := wsClientEntryStructIR

	e.emitGlobal(fmt.Sprintf(`
define void @__kml_wsclient_scan() {
entry:
  %%len = load i64, ptr @__kml_wsc_len, align 8
  %%data = load ptr, ptr @__kml_wsc_data, align 8
  br label %%scanloop

scanloop:
  %%i = phi i64 [ 0, %%entry ], [ %%inext, %%scannext ]
  %%inbounds = icmp slt i64 %%i, %%len
  br i1 %%inbounds, label %%scanbody, label %%scandone

scanbody:
  %%slot = getelementptr ptr, ptr %%data, i64 %%i
  %%entryp = load ptr, ptr %%slot, align 8
  %%pending_p = getelementptr %[1]s, ptr %%entryp, i32 0, i32 2
  %%pending = load i64, ptr %%pending_p, align 8
  %%needsnotify = icmp eq i64 %%pending, 1
  br i1 %%needsnotify, label %%donotify, label %%checkopen

donotify:
  store i64 0, ptr %%pending_p, align 8
  %%dnstate_p = getelementptr %[1]s, ptr %%entryp, i32 0, i32 1
  %%dnstate = load i64, ptr %%dnstate_p, align 8
  %%dninstance_p = getelementptr %[1]s, ptr %%entryp, i32 0, i32 5
  %%dninstance = load ptr, ptr %%dninstance_p, align 8
  %%dnrs_p = getelementptr %[2]s, ptr %%dninstance, i32 0, i32 %[3]d
  store i64 %%dnstate, ptr %%dnrs_p, align 8
  %%dnisopen = icmp eq i64 %%dnstate, 1
  br i1 %%dnisopen, label %%fireopen, label %%fireerror

fireopen:
  %%fo_onopen_p = getelementptr %[2]s, ptr %%dninstance, i32 0, i32 %[4]d
  %%fo_onopen = load ptr, ptr %%fo_onopen_p, align 8
  %%fo_has = icmp ne ptr %%fo_onopen, null
  br i1 %%fo_has, label %%callonopen, label %%checkopen

callonopen:
%[5]s
  br label %%checkopen

fireerror:
  %%fe_onerror_p = getelementptr %[2]s, ptr %%dninstance, i32 0, i32 %[6]d
  %%fe_onerror = load ptr, ptr %%fe_onerror_p, align 8
  %%fe_has = icmp ne ptr %%fe_onerror, null
  br i1 %%fe_has, label %%callonerror, label %%checkonclose1

callonerror:
%[7]s
  br label %%checkonclose1

checkonclose1:
  %%co1_onclose_p = getelementptr %[2]s, ptr %%dninstance, i32 0, i32 %[8]d
  %%co1_onclose = load ptr, ptr %%co1_onclose_p, align 8
  %%co1_has = icmp ne ptr %%co1_onclose, null
  br i1 %%co1_has, label %%callonclose1, label %%scannext

callonclose1:
%[9]s
  br label %%scannext

checkopen:
  %%co_state_p = getelementptr %[1]s, ptr %%entryp, i32 0, i32 1
  %%co_state = load i64, ptr %%co_state_p, align 8
  %%co_isopen = icmp eq i64 %%co_state, 1
  br i1 %%co_isopen, label %%doread, label %%scannext

doread:
  %%dr_fd_p = getelementptr %[1]s, ptr %%entryp, i32 0, i32 0
  %%dr_fd64 = load i64, ptr %%dr_fd_p, align 8
  %%dr_fd32 = trunc i64 %%dr_fd64 to i32
  %%dr_buf_p = getelementptr %[1]s, ptr %%entryp, i32 0, i32 3
  %%dr_bufstruct = load ptr, ptr %%dr_buf_p, align 8
  %%bs_data_p = getelementptr { ptr, i64, i64 }, ptr %%dr_bufstruct, i32 0, i32 0
  %%bs_len_p = getelementptr { ptr, i64, i64 }, ptr %%dr_bufstruct, i32 0, i32 1
  %%bs_cap_p = getelementptr { ptr, i64, i64 }, ptr %%dr_bufstruct, i32 0, i32 2
  %%bscap0 = load i64, ptr %%bs_cap_p, align 8
  %%bslen0 = load i64, ptr %%bs_len_p, align 8
  %%bsremain = sub i64 %%bscap0, %%bslen0
  %%needgrow = icmp slt i64 %%bsremain, 4096
  br i1 %%needgrow, label %%growbuf, label %%doreadactual

growbuf:
  %%gb_newcap0 = mul i64 %%bscap0, 2
  %%gb_atleast = icmp sgt i64 %%gb_newcap0, 4096
  %%gb_newcap = select i1 %%gb_atleast, i64 %%gb_newcap0, i64 4096
  %%gb_olddata = load ptr, ptr %%bs_data_p, align 8
  %%gb_newdata = call ptr @realloc(ptr %%gb_olddata, i64 %%gb_newcap)
  store ptr %%gb_newdata, ptr %%bs_data_p, align 8
  store i64 %%gb_newcap, ptr %%bs_cap_p, align 8
  br label %%doreadactual

doreadactual:
  %%ra_data = load ptr, ptr %%bs_data_p, align 8
  %%ra_cap = load i64, ptr %%bs_cap_p, align 8
  %%ra_len = load i64, ptr %%bs_len_p, align 8
  %%ra_ptr = getelementptr i8, ptr %%ra_data, i64 %%ra_len
  %%ra_space = sub i64 %%ra_cap, %%ra_len
  %%n = call i64 @read(i32 %%dr_fd32, ptr %%ra_ptr, i64 %%ra_space)
  %%ngood = icmp sgt i64 %%n, 0
  br i1 %%ngood, label %%accumulate, label %%checkreaderr

accumulate:
  %%acc_newlen = add i64 %%ra_len, %%n
  store i64 %%acc_newlen, ptr %%bs_len_p, align 8
  br label %%decodeloop

checkreaderr:
  %%niszero = icmp eq i64 %%n, 0
  br i1 %%niszero, label %%doclose, label %%checkeagain2

checkeagain2:
  %%errnop2 = call ptr @%[10]s()
  %%errnov2 = load i32, ptr %%errnop2, align 4
  %%iseagain2 = icmp eq i32 %%errnov2, %[11]d
  br i1 %%iseagain2, label %%scannext, label %%doclose

decodeloop:
  %%dl_data = load ptr, ptr %%bs_data_p, align 8
  %%dl_len = load i64, ptr %%bs_len_p, align 8
  %%consumed_p = getelementptr %[1]s, ptr %%entryp, i32 0, i32 4
  %%dl_consumed = load i64, ptr %%consumed_p, align 8
  %%dl_avail = sub i64 %%dl_len, %%dl_consumed
  %%dl_curptr = getelementptr i8, ptr %%dl_data, i64 %%dl_consumed
  %%decres = call { i32, i32, ptr, i64, i64 } @__kml_ws_frame_decode(ptr %%dl_curptr, i64 %%dl_avail)
  %%dstatus = extractvalue { i32, i32, ptr, i64, i64 } %%decres, 0
  %%dincomplete = icmp eq i32 %%dstatus, 0
  br i1 %%dincomplete, label %%scannext, label %%gotframe

gotframe:
  %%ddelta = extractvalue { i32, i32, ptr, i64, i64 } %%decres, 4
  %%dnewconsumed = add i64 %%dl_consumed, %%ddelta
  store i64 %%dnewconsumed, ptr %%consumed_p, align 8
  %%dproto = icmp eq i32 %%dstatus, 2
  br i1 %%dproto, label %%doclose, label %%dcheckopcode

dcheckopcode:
  %%dopcode = extractvalue { i32, i32, ptr, i64, i64 } %%decres, 1
  %%disclose = icmp eq i32 %%dopcode, 8
  br i1 %%disclose, label %%doclose, label %%dcheckdata

dcheckdata:
  %%distext = icmp eq i32 %%dopcode, 1
  %%disbinary = icmp eq i32 %%dopcode, 2
  %%disdata = or i1 %%distext, %%disbinary
  br i1 %%disdata, label %%ddispatch, label %%decodeloop

ddispatch:
  %%dpayload = extractvalue { i32, i32, ptr, i64, i64 } %%decres, 2
  %%dpayloadlen = extractvalue { i32, i32, ptr, i64, i64 } %%decres, 3
  %%dstrlen = add i64 %%dpayloadlen, 1
  %%dstrbuf = call ptr @malloc(i64 %%dstrlen)
  call ptr @memcpy(ptr %%dstrbuf, ptr %%dpayload, i64 %%dpayloadlen)
  %%dtermp = getelementptr i8, ptr %%dstrbuf, i64 %%dpayloadlen
  store i8 0, ptr %%dtermp, align 1
  call void @free(ptr %%dpayload)
  %%dm_onmsg_p = getelementptr %[2]s, ptr %%dninstance, i32 0, i32 %[12]d
  %%dm_onmsg = load ptr, ptr %%dm_onmsg_p, align 8
  %%dm_has = icmp ne ptr %%dm_onmsg, null
  br i1 %%dm_has, label %%dcallonmsg, label %%decodeloop

dcallonmsg:
%[13]s
  br label %%decodeloop

doclose:
  %%dc_fd_p = getelementptr %[1]s, ptr %%entryp, i32 0, i32 0
  %%dc_fd64 = load i64, ptr %%dc_fd_p, align 8
  %%dc_fd32 = trunc i64 %%dc_fd64 to i32
  call i32 @close(i32 %%dc_fd32)
  store i64 -1, ptr %%dc_fd_p, align 8
  %%dc_state_p = getelementptr %[1]s, ptr %%entryp, i32 0, i32 1
  store i64 2, ptr %%dc_state_p, align 8
  %%dc_curactive = load i64, ptr @__kml_wsc_active, align 8
  %%dc_newactive = sub i64 %%dc_curactive, 1
  store i64 %%dc_newactive, ptr @__kml_wsc_active, align 8
  %%dc_rs_p = getelementptr %[2]s, ptr %%dninstance, i32 0, i32 %[3]d
  store i64 2, ptr %%dc_rs_p, align 8
  %%dc_onclose_p = getelementptr %[2]s, ptr %%dninstance, i32 0, i32 %[8]d
  %%dc_onclose = load ptr, ptr %%dc_onclose_p, align 8
  %%dc_has = icmp ne ptr %%dc_onclose, null
  br i1 %%dc_has, label %%callonclose2, label %%scannext

callonclose2:
%[14]s
  br label %%scannext

scannext:
  %%inext = add i64 %%i, 1
  br label %%scanloop

scandone:
  ret void
}`,
		entryTy, wsStructIR, rsIdx, onopenIdx,
		wsClientCallCallback("fo", "%fo_onopen", emptyStr),
		onerrorIdx,
		wsClientCallCallback("fe", "%fe_onerror", emptyStr),
		oncloseIdx,
		wsClientCallCallback("co1", "%co1_onclose", emptyStr),
		errnoAccessor(), httpEagainErrno(),
		onmessageIdx,
		wsClientCallCallback("dm", "%dm_onmsg", "%dstrbuf"),
		wsClientCallCallback("co2", "%dc_onclose", emptyStr),
	))
}
