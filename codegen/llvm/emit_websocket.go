// emit_websocket.go — server-side WebSocket support (TDD-00039 Stages 1-2):
// upgrade detection + the RFC 6455 handshake, WSConnection construction, and
// the persistent per-connection frame read/decode/dispatch loop — including
// Stage 2's automatic Ping/Pong replies and the Close-frame echo handshake
// (RFC 6455 §5.5) — all generated as part of `@__kml_http_dispatch`
// (emit_http.go's buildHTTPDispatcher) alongside the ordinary HTTP request
// path. `.send()`/`.close()` (called from arbitrary user code holding a
// WSConnection, not necessarily from inside the loop below) live here too.
// See runtime_websocket.go (TDD-00039 Stage 0, ADR-00125) for the frame
// codec/SHA-1 this file calls into, unchanged.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// wsGUID is RFC 6455 §1.3's fixed handshake GUID, concatenated onto a
// client's Sec-WebSocket-Key before SHA-1 + base64 to produce
// Sec-WebSocket-Accept.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// maxWSFrameBufferBytes bounds how far the persistent read loop's
// accumulator buffer grows — the same "cap against a hostile/runaway peer"
// reasoning maxHTTPRequestBytes already applies to the ordinary HTTP read
// path, just sized for a WS connection that's expected to live far longer
// and carry many messages (16MiB, vs. HTTP's 10MiB single-request cap).
const maxWSFrameBufferBytes = 16 * 1024 * 1024

// emitWSUpgradeDetect emits the case-insensitive `Upgrade: websocket` +
// `Connection: ...upgrade...` check (RFC 6455 §4.2.1) against the
// already-parsed, already-lowercased-key headers map, branching to upgradeL
// on a match or normalL otherwise. Header VALUES aren't guaranteed
// lowercase the way keys are (`ensureHTTPParseHeaders` only lowercases
// keys), so both checks lowercase the value first via the same
// `__kml_tolower` header-key lowercasing already uses.
func (e *Emitter) emitWSUpgradeDetect(headersMapFinal, upgradeL, normalL string) {
	e.ensureMapStrHelpers()
	e.ensureStringToLower()
	e.ensureStrcmp()
	e.ensureStrstr()

	upgradeKey := e.internString("upgrade")
	connectionKey := e.internString("connection")
	websocketVal := e.internString("websocket")
	upgradeToken := e.internString("upgrade")

	checkUpgradeValL := e.freshLabel("http.wscheckupgradeval")
	checkConnHdrL := e.freshLabel("http.wscheckconnhdr")
	checkConnValL := e.freshLabel("http.wscheckconnval")

	hasUpgradeHdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", hasUpgradeHdr, headersMapFinal, upgradeKey))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasUpgradeHdr, checkUpgradeValL, normalL))

	e.emitLabel(checkUpgradeValL)
	upgradeValInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", upgradeValInt, headersMapFinal, upgradeKey))
	upgradeValPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", upgradeValPtr, upgradeValInt))
	upgradeLower := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_tolower(ptr %s)", upgradeLower, upgradeValPtr))
	cmpRes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", cmpRes, upgradeLower, websocketVal))
	isWebSocket := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isWebSocket, cmpRes))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isWebSocket, checkConnHdrL, normalL))

	e.emitLabel(checkConnHdrL)
	hasConnHdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", hasConnHdr, headersMapFinal, connectionKey))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasConnHdr, checkConnValL, normalL))

	e.emitLabel(checkConnValL)
	connValInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", connValInt, headersMapFinal, connectionKey))
	connValPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", connValPtr, connValInt))
	connLower := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_tolower(ptr %s)", connLower, connValPtr))
	foundTok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @strstr(ptr %s, ptr %s)", foundTok, connLower, upgradeToken))
	hasTok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasTok, foundTok))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasTok, upgradeL, normalL))
}

// emitComputeWSAccept computes Sec-WebSocket-Accept (RFC 6455 §1.3:
// base64(SHA1(key + GUID))) from a Sec-WebSocket-Key value — shared by the
// server's own handshake response (emitWSHandshakeAndLoop below) and the
// client's request-verification step (emit_websocket_client.go), since both
// sides need to independently compute the exact same value from the exact
// same key (the server to send it, the client to check the server's own
// answer matches).
func (e *Emitter) emitComputeWSAccept(keyVal Value) (Value, error) {
	e.ensureWSSHA1()
	e.ensureBase64EncodeBytes()
	e.ensureStrlen()

	guidRef := e.internString(wsGUID)
	concatVal, err := e.emitStringConcat(keyVal, Value{Ref: guidRef, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	concatLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", concatLen, concatVal.Ref))

	digestBuf := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca [20 x i8], align 1", digestBuf))
	e.emitInstr(fmt.Sprintf("call void @__kml_ws_sha1(ptr %s, i64 %s, ptr %s)", concatVal.Ref, concatLen, digestBuf))
	acceptVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_base64_encode_bytes(ptr %s, i64 20)", acceptVal, digestBuf))
	return Value{Ref: acceptVal, Ty: TypePtr}, nil
}

// emitWSHandshakeAndLoop emits the rest of a successful upgrade: compute
// and send Sec-WebSocket-Accept (RFC 6455 §1.3), build the WSConnection
// object, call the user's `ws` handler once, then run the persistent frame
// loop until the connection ends. Always ends in a terminator (either
// falling into noReqL for a malformed request, or wsEndL's own `ret void`)
// — the caller (buildHTTPDispatcher) never needs to emit anything after
// calling this.
//
// fd32/fd64/fdPtr/ctxPtrSlot are the same registers buildHTTPDispatcher's
// own readLoopL computed for the current pass — safe to reuse directly here
// (no yield has happened since), exactly like the ordinary parseL/noReqL
// paths already do (see buildHTTPDispatcher's own doc comment on this).
// noReqL is buildHTTPDispatcher's existing malformed-request cleanup label
// (close + mark done + decrement conn_active), reused as-is for a missing
// Sec-WebSocket-Key rather than duplicating that cleanup a third time.
func (e *Emitter) emitWSHandshakeAndLoop(headersMapFinal, fd32, fd64, fdPtr, noReqL string) error {
	e.ensureWSSHA1()
	e.ensureBase64EncodeBytes()
	e.ensureMapStrHelpers()
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureFree()

	haveKeyL := e.freshLabel("http.wshavekey")
	keyName := e.internString("sec-websocket-key")
	hasKey := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", hasKey, headersMapFinal, keyName))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasKey, haveKeyL, noReqL))

	e.emitLabel(haveKeyL)
	keyValInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", keyValInt, headersMapFinal, keyName))
	keyValPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", keyValPtr, keyValInt))

	acceptVal, err := e.emitComputeWSAccept(Value{Ref: keyValPtr, Ty: TypePtr})
	if err != nil {
		return err
	}

	respPrefix := e.internString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ")
	respSuffix := e.internString("\r\n\r\n")
	resp1, err := e.emitStringConcat(Value{Ref: respPrefix, Ty: TypePtr}, acceptVal)
	if err != nil {
		return err
	}
	resp2, err := e.emitStringConcat(resp1, Value{Ref: respSuffix, Ty: TypePtr})
	if err != nil {
		return err
	}
	respLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", respLen, resp2.Ref))
	e.emitInstr(fmt.Sprintf("call i64 @write(i32 %s, ptr %s, i64 %s)", fd32, resp2.Ref, respLen))

	// Build the WSConnection object and call the user's `ws` handler once
	// — mirrors how buildHTTPDispatcher builds and passes a Request object
	// to the ordinary handler, just with a fixed (not user-declared) type.
	wsTy := WSConnectionType()
	wsStructIR := wsTy.StructIR()
	wsConnReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", wsConnReg, wsTy.StructSize()))
	fdIdx, _, _ := wsTy.FieldIndex(WSConnFdField)
	fdGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", fdGep, wsStructIR, wsConnReg, fdIdx))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", fd64, fdGep))
	onmsgIdx, _, _ := wsTy.FieldIndex("onmessage")
	onmsgGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", onmsgGep, wsStructIR, wsConnReg, onmsgIdx))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", onmsgGep))

	wsHandlerPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_listen_ws_handler, align 8", wsHandlerPtr))
	cb := Callback{kind: cbClosure, hdrPtr: wsHandlerPtr, ty: FuncType([]Type{wsTy}, TypeVoid)}
	if _, err := e.emitCBCall(cb, []Value{{Ref: wsConnReg, Ty: wsTy}}); err != nil {
		return err
	}

	return e.emitWSServerLoop(wsConnReg)
}

// emitWSServerLoop emits the persistent frame read/decode/dispatch loop
// that runs for the rest of this connection's life, reusing the exact
// swapcontext-yield-on-EAGAIN pattern buildHTTPDispatcher's own HTTP read
// loop already established (doYieldL below) — same fiber, same stack frame,
// just a structurally different body (loop forever parsing frames, not
// "read one request then return"). Every value that needs to survive a
// yield lives in an alloca, not a bare SSA register — required, not just
// stylistic: a swapcontext call can resume this fiber after arbitrary other
// code ran on this OS thread in between, which only this fiber's own stack
// memory (allocas) is guaranteed to survive intact (see
// buildHTTPDispatcher's own doc comment on exactly this point). fd/ctx are
// deliberately NOT among those allocas — they're re-derived fresh from
// @__kml_current_conn_idx/@__kml_conn_data at the top of every loop pass
// instead (mirroring readLoopL), since that array's backing storage can
// itself move (realloc) while this fiber is suspended.
func (e *Emitter) emitWSServerLoop(wsConnReg string) error {
	e.ensureWSFrameDecode()
	e.ensureWSFrameEncode()
	e.ensureReadDecl()
	e.ensureCloseDecl()
	e.ensureErrnoAccessor()
	e.ensureRealloc()
	e.ensureFree()

	bufPtrA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", bufPtrA))
	bufCapA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", bufCapA))
	totalReadA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", totalReadA))
	consumedA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", consumedA))
	connObjA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", connObjA))

	initBuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 4096)", initBuf))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", initBuf, bufPtrA))
	e.emitInstr(fmt.Sprintf("store i64 4096, ptr %s, align 8", bufCapA))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", totalReadA))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", consumedA))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", wsConnReg, connObjA))

	readLoopL := e.freshLabel("ws.readloop")
	growL := e.freshLabel("ws.grow")
	doGrowL := e.freshLabel("ws.dogrow")
	haveCapL := e.freshLabel("ws.havecap")
	accumulateL := e.freshLabel("ws.accumulate")
	checkErrL := e.freshLabel("ws.checkerr")
	checkEagainL := e.freshLabel("ws.checkeagain")
	doYieldL := e.freshLabel("ws.doyield")
	decodeLoopL := e.freshLabel("ws.decodeloop")
	gotFrameL := e.freshLabel("ws.gotframe")
	checkOpcodeL := e.freshLabel("ws.checkopcode")
	closeRecvL := e.freshLabel("ws.closerecv")
	checkPingL := e.freshLabel("ws.checkping")
	pingRecvL := e.freshLabel("ws.pingrecv")
	checkDataL := e.freshLabel("ws.checkdata")
	dispatchL := e.freshLabel("ws.dispatch")
	callOnMsgL := e.freshLabel("ws.callonmsg")
	endL := e.freshLabel("ws.end")

	e.emitTerminator(fmt.Sprintf("br label %%%s", readLoopL))

	// readLoopL: re-derive this connection's fd/ctx fresh every pass (see
	// doc comment above), then try to read more bytes, growing the
	// accumulator first if it's nearly full.
	e.emitLabel(readLoopL)
	selfIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_current_conn_idx, align 8", selfIdx))
	connData := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_conn_data, align 8", connData))
	selfSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %s, i64 %s", selfSlot, connData, selfIdx))
	fdPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %s, i32 0, i32 0", fdPtr, selfSlot))
	ctxPtrSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr, ptr, ptr, ptr }, ptr %s, i32 0, i32 1", ctxPtrSlot, selfSlot))
	fd64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fd64, fdPtr))
	fd32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", fd32, fd64))

	capNow0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", capNow0, bufCapA))
	trNow0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", trNow0, totalReadA))
	remain := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", remain, capNow0, trNow0))
	needGrow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 4096", needGrow, remain))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", needGrow, growL, haveCapL))

	e.emitLabel(growL)
	curCap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curCap, bufCapA))
	newCap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 2", newCap, curCap))
	tooBig := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %d", tooBig, newCap, maxWSFrameBufferBytes))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", tooBig, endL, doGrowL))

	e.emitLabel(doGrowL)
	curBuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curBuf, bufPtrA))
	newBuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @realloc(ptr %s, i64 %s)", newBuf, curBuf, newCap))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newBuf, bufPtrA))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newCap, bufCapA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", haveCapL))

	e.emitLabel(haveCapL)
	bufForRead := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bufForRead, bufPtrA))
	trForRead := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", trForRead, totalReadA))
	capForRead := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", capForRead, bufCapA))
	readPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", readPtr, bufForRead, trForRead))
	readCap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", readCap, capForRead, trForRead))
	nReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @read(i32 %s, ptr %s, i64 %s)", nReg, fd32, readPtr, readCap))
	gotData := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 0", gotData, nReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", gotData, accumulateL, checkErrL))

	e.emitLabel(accumulateL)
	trOld := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", trOld, totalReadA))
	trNew := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", trNew, trOld, nReg))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", trNew, totalReadA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", decodeLoopL))

	e.emitLabel(checkErrL)
	isZero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isZero, nReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isZero, endL, checkEagainL))

	e.emitLabel(checkEagainL)
	errnoPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @%s()", errnoPtr, errnoAccessor()))
	errnoVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", errnoVal, errnoPtr))
	isEagain := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, %d", isEagain, errnoVal, httpEagainErrno()))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isEagain, doYieldL, endL))

	e.emitLabel(doYieldL)
	ctxPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ctxPtr, ctxPtrSlot))
	e.emitInstr(fmt.Sprintf("call i32 @swapcontext(ptr %s, ptr @__kml_main_ctx)", ctxPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", readLoopL))

	// decodeLoopL: try to parse one more complete frame out of whatever's
	// unconsumed — reloads buf/totalRead/consumed fresh every pass (same
	// realloc-safety convention __kml_eventsource_process_available already
	// established, TDD-00038/ADR-00123), so this is correct regardless of
	// how many times readLoopL has grown/moved the buffer since the last
	// pass through here.
	e.emitLabel(decodeLoopL)
	bufForDecode := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bufForDecode, bufPtrA))
	trForDecode := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", trForDecode, totalReadA))
	consumedNow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", consumedNow, consumedA))
	avail := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", avail, trForDecode, consumedNow))
	curPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", curPtr, bufForDecode, consumedNow))
	decodeRes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { i32, i32, ptr, i64, i64 } @__kml_ws_frame_decode(ptr %s, i64 %s)", decodeRes, curPtr, avail))
	status := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i32, i32, ptr, i64, i64 } %s, 0", status, decodeRes))
	isIncomplete := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isIncomplete, status))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isIncomplete, readLoopL, gotFrameL))

	e.emitLabel(gotFrameL)
	consumedDelta := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i32, i32, ptr, i64, i64 } %s, 4", consumedDelta, decodeRes))
	newConsumed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newConsumed, consumedNow, consumedDelta))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newConsumed, consumedA))
	isProtoErr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 2", isProtoErr, status))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isProtoErr, endL, checkOpcodeL))

	e.emitLabel(checkOpcodeL)
	opcode := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i32, i32, ptr, i64, i64 } %s, 1", opcode, decodeRes))
	isClose := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 8", isClose, opcode))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isClose, closeRecvL, checkPingL))

	// closeRecvL: RFC 6455 §5.5.1 — an endpoint that receives a Close frame
	// (and hasn't already sent one of its own) must complete the closing
	// handshake by sending a Close frame back before actually closing the
	// connection. Echoes the received frame's payload verbatim (whatever
	// status code/reason the peer sent, if any) — a real, simple, compliant
	// choice, not a shortcut: the spec doesn't mandate a specific code in
	// the response, only that one is sent.
	e.emitLabel(closeRecvL)
	closePayload := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i32, i32, ptr, i64, i64 } %s, 2", closePayload, decodeRes))
	closePayloadLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i32, i32, ptr, i64, i64 } %s, 3", closePayloadLen, decodeRes))
	closeEnc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { ptr, i64 } @__kml_ws_frame_encode(i32 8, i1 false, ptr %s, i64 %s, i32 0)", closeEnc, closePayload, closePayloadLen))
	closeEncBuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 0", closeEncBuf, closeEnc))
	closeEncLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 1", closeEncLen, closeEnc))
	e.emitInstr(fmt.Sprintf("call i64 @write(i32 %s, ptr %s, i64 %s)", fd32, closeEncBuf, closeEncLen))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", closeEncBuf))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", closePayload))
	e.emitTerminator(fmt.Sprintf("br label %%%s", endL))

	// checkPingL/pingRecvL: RFC 6455 §5.5.2 — a Pong sent in response to a
	// Ping must carry identical application data. Transparent to user code
	// (never reaches `.onmessage`), matching every real WebSocket server.
	e.emitLabel(checkPingL)
	isPing := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 9", isPing, opcode))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isPing, pingRecvL, checkDataL))

	e.emitLabel(pingRecvL)
	pingPayload := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i32, i32, ptr, i64, i64 } %s, 2", pingPayload, decodeRes))
	pingPayloadLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i32, i32, ptr, i64, i64 } %s, 3", pingPayloadLen, decodeRes))
	pongEnc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { ptr, i64 } @__kml_ws_frame_encode(i32 10, i1 false, ptr %s, i64 %s, i32 0)", pongEnc, pingPayload, pingPayloadLen))
	pongEncBuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 0", pongEncBuf, pongEnc))
	pongEncLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 1", pongEncLen, pongEnc))
	e.emitInstr(fmt.Sprintf("call i64 @write(i32 %s, ptr %s, i64 %s)", fd32, pongEncBuf, pongEncLen))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", pongEncBuf))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", pingPayload))
	e.emitTerminator(fmt.Sprintf("br label %%%s", decodeLoopL))

	e.emitLabel(checkDataL)
	isText := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 1", isText, opcode))
	isBinary := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 2", isBinary, opcode))
	isData := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isData, isText, isBinary))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isData, dispatchL, decodeLoopL))

	// dispatchL: an unsolicited Pong (opcode 10) or any other unknown
	// opcode still just falls through to decodeLoopL above with its payload
	// leaked (small, bounded by one frame — acceptable V1 cost, matching
	// this project's general "manual mode never frees most transient
	// allocations" posture elsewhere) — Ping and Close are the only two
	// control opcodes this stage needs to act on.
	e.emitLabel(dispatchL)
	payload := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i32, i32, ptr, i64, i64 } %s, 2", payload, decodeRes))
	payloadLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i32, i32, ptr, i64, i64 } %s, 3", payloadLen, decodeRes))
	strBuf := e.emitStringAlloc(payloadLen) // TDD-00120: message payload is length-prefixed
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", strBuf, payload, payloadLen))
	termPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", termPtr, strBuf, payloadLen))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", termPtr))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", payload))

	msgTy := WSMessageEventType()
	msgReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", msgReg, msgTy.StructSize()))
	dataIdx, _, _ := msgTy.FieldIndex("data")
	dataGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dataGep, msgTy.StructIR(), msgReg, dataIdx))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", strBuf, dataGep))

	connReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", connReg, connObjA))
	wsTy := WSConnectionType()
	onmsgIdx, _, _ := wsTy.FieldIndex("onmessage")
	onmsgGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", onmsgGep, wsTy.StructIR(), connReg, onmsgIdx))
	onmsgPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", onmsgPtr, onmsgGep))
	hasOnMsg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasOnMsg, onmsgPtr))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasOnMsg, callOnMsgL, decodeLoopL))

	e.emitLabel(callOnMsgL)
	cb := Callback{kind: cbClosure, hdrPtr: onmsgPtr, ty: FuncType([]Type{msgTy}, TypeVoid)}
	if _, err := e.emitCBCall(cb, []Value{{Ref: msgReg, Ty: msgTy}}); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", decodeLoopL))

	// endL: reuses whichever fd32/fdPtr this pass through readLoopL most
	// recently derived — always valid here, since every path into endL
	// (directly from readLoopL, or via the decode cluster) passes through
	// readLoopL's own fd/ctx computation first on this dynamic execution,
	// even though decodeLoopL's own self-loop (multiple frames per read)
	// never re-enters readLoopL in between. Mirrors buildHTTPDispatcher's
	// own noReqL/parseL cleanup: close the fd, mark this fiber-array slot
	// done, decrement @__kml_conn_active.
	e.emitLabel(endL)
	e.emitInstr(fmt.Sprintf("call i32 @close(i32 %s)", fd32))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", fdPtr))
	activeNow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_conn_active, align 8", activeNow))
	activeNew := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", activeNew, activeNow))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr @__kml_conn_active, align 8", activeNew))
	e.emitTerminator("ret void")

	return nil
}

// emitWSConnectionSend implements `socket.send(data: string)`: a single
// unmasked text frame (opcode 1) — server-to-client frames MUST NOT be
// masked (RFC 6455 §5.1), the one asymmetry from a future client-side
// `.send()`. Binary send (an ArrayBuffer payload) isn't scoped for Stage 1
// — matches this stage's "unfragmented echo" goal, which only needs text.
func (e *Emitter) emitWSConnectionSend(objExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: send() requires 1 argument (data: string)", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	dataVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	dataVal = e.coerce(dataVal, TypePtr)

	e.ensureWSFrameEncode()
	e.ensureStrlen()
	e.ensureFree()
	e.ensureCloseDecl()

	ty := WSConnectionType()
	fdIdx, _, _ := ty.FieldIndex(WSConnFdField)
	fdGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", fdGep, ty.StructIR(), objVal.Ref, fdIdx))
	fd64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fd64, fdGep))
	fd32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", fd32, fd64))

	dataLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", dataLen, dataVal.Ref))
	frame := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { ptr, i64 } @__kml_ws_frame_encode(i32 1, i1 false, ptr %s, i64 %s, i32 0)", frame, dataVal.Ref, dataLen))
	frameBuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 0", frameBuf, frame))
	frameLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 1", frameLen, frame))
	e.emitInstr(fmt.Sprintf("call i64 @write(i32 %s, ptr %s, i64 %s)", fd32, frameBuf, frameLen))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", frameBuf))

	return Value{Ty: TypeVoid}, nil
}

// emitWSConnectionCloseMethod implements `socket.close()` (TDD-00039 Stage
// 2): sends a Close frame (status 1000, Normal Closure, RFC 6455 §7.4.1 —
// no reason text) initiating the closing handshake, then closes the raw fd
// directly without waiting for the peer's own echo back — a real, deliberate
// simplification (waiting would need a new timeout-based state machine, out
// of scope for this stage) rather than the full spec-mandated wait. The
// persistent read loop's own existing error handling (an already-closed fd
// fails `read()` with EBADF, routed the same as any other non-EAGAIN error)
// naturally ends the fiber on its next pass — no new state needed there. If
// called synchronously from inside `onmessage` (mid-dispatch, with more
// already-buffered frames still pending), any frames already fully decoded
// this pass still get dispatched before the loop's next read() notices the
// close.
func (e *Emitter) emitWSConnectionCloseMethod(objExpr ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	e.ensureCloseDecl()
	e.ensureWSFrameEncode()
	e.ensureMalloc()
	e.ensureFree()

	ty := WSConnectionType()
	fdIdx, _, _ := ty.FieldIndex(WSConnFdField)
	fdGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", fdGep, ty.StructIR(), objVal.Ref, fdIdx))
	fd64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fd64, fdGep))
	fd32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", fd32, fd64))

	// Close frame body: 2-byte big-endian status code, 1000 = 0x03E8, no
	// reason text (RFC 6455 §5.5.1's optional reason is skipped — not
	// meaningful to expose without a `.close(code, reason)` overload, a
	// real, undesigned follow-on).
	codeBuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 2)", codeBuf))
	codeP0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 0", codeP0, codeBuf))
	e.emitInstr(fmt.Sprintf("store i8 3, ptr %s, align 1", codeP0))
	codeP1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 1", codeP1, codeBuf))
	e.emitInstr(fmt.Sprintf("store i8 232, ptr %s, align 1", codeP1))

	closeEnc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { ptr, i64 } @__kml_ws_frame_encode(i32 8, i1 false, ptr %s, i64 2, i32 0)", closeEnc, codeBuf))
	closeEncBuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 0", closeEncBuf, closeEnc))
	closeEncLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 1", closeEncLen, closeEnc))
	e.emitInstr(fmt.Sprintf("call i64 @write(i32 %s, ptr %s, i64 %s)", fd32, closeEncBuf, closeEncLen))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", closeEncBuf))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", codeBuf))

	e.emitInstr(fmt.Sprintf("call i32 @close(i32 %s)", fd32))

	return Value{Ty: TypeVoid}, nil
}
