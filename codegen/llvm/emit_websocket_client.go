// emit_websocket_client.go — `new WebSocket(url)` (TDD-00039 Stage 3,
// `ws://` only): URL parsing/validation, request-string building, and
// `.send()`/`.close()`. See runtime_websocket_client.go for the C-runtime
// side (outbound connect, the blocking handshake, the client-scan array)
// this file's IR calls into, and WebSocketClientType's own doc comment in
// types.go for the synchronous-connect/deferred-notify design this
// function implements the first half of.
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// emitNewWebSocketClientExpression implements `new WebSocket(url)`. Parses
// url via the same libcurl URL API emit_url.go's `new URL(...)` already
// uses (curlURLGetPart), passing CURLU_NON_SUPPORT_SCHEME (8) to
// curl_url_set so a "ws://" URL parses even on a libcurl build that
// doesn't itself recognize WebSocket as a transport scheme — this function
// never asks curl to actually fetch anything, only to split the URL into
// parts. Builds the KML-level object first (so its own pointer can be
// passed to __kml_ws_client_open as `instance`, the same two-way-link
// ordering emitNewEventSourceExpression already establishes), then a
// fresh Sec-WebSocket-Key (16 random bytes via the existing
// __kml_crypto_random_bytes, base64-encoded) and the full HTTP upgrade
// request string, then calls __kml_ws_client_open — which performs the
// actual (synchronous) connect + handshake — and stores the returned entry
// pointer into the object's hidden WebSocketClientHandleField.
func (e *Emitter) emitNewWebSocketClientExpression(ex *ast.NewWebSocketExpression) (Value, error) {
	e.ensureWSClientRuntime()
	e.ensureCurlURL()
	e.ensureMalloc()
	e.ensureExceptionHelpers()
	e.ensureStrcmp()
	e.ensureCryptoRandomBytes()
	e.ensureAtoll()

	urlVal, err := e.emitExpr(ex.URL)
	if err != nil {
		return Value{}, err
	}
	urlVal = e.coerce(urlVal, TypePtr)

	handle := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @curl_url()", handle))
	setCode := e.freshReg()
	// flags=8 (CURLU_NON_SUPPORT_SCHEME): see this function's own doc
	// comment on why — verified directly against curl/urlapi.h, same
	// standard every other libcurl constant in this codebase documents.
	e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_set(ptr %s, i32 %d, ptr %s, i32 8)", setCode, handle, curluPartURL, urlVal.Ref))
	badURL := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", badURL, setCode))

	badL := e.freshLabel("wsc.badurl")
	okL := e.freshLabel("wsc.okurl")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", badURL, badL, okL))

	e.emitLabel(badL)
	e.emitInstr(fmt.Sprintf("call void @curl_url_cleanup(ptr %s)", handle))
	e.emitInternalThrow(e.internString("Invalid WebSocket URL"))

	e.emitLabel(okL)

	schemeRaw, _ := e.curlURLGetPart(handle, curluPartScheme)
	wantScheme := e.internString("ws")
	schemeCmp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", schemeCmp, schemeRaw, wantScheme))
	badScheme := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", badScheme, schemeCmp))

	schemeBadL := e.freshLabel("wsc.badscheme")
	schemeOkL := e.freshLabel("wsc.okscheme")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", badScheme, schemeBadL, schemeOkL))

	e.emitLabel(schemeBadL)
	wssScheme := e.internString("wss")
	wssCmp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", wssCmp, schemeRaw, wssScheme))
	isWss := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isWss, wssCmp))
	wssL := e.freshLabel("wsc.wssnotsupported")
	otherSchemeL := e.freshLabel("wsc.otherscheme")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isWss, wssL, otherSchemeL))

	e.emitLabel(wssL)
	e.emitInternalThrow(e.internString("wss:// is not supported yet (TDD-00039 Stage 3 is ws:// only)"))

	e.emitLabel(otherSchemeL)
	e.emitInternalThrow(e.internString("WebSocket URL must use the ws:// scheme"))

	e.emitLabel(schemeOkL)

	hostRaw, hostPresent := e.curlURLGetPart(handle, curluPartHost)
	hostMissingL := e.freshLabel("wsc.nohost")
	hostOkL := e.freshLabel("wsc.havehost")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hostPresent, hostOkL, hostMissingL))

	e.emitLabel(hostMissingL)
	e.emitInternalThrow(e.internString("Invalid WebSocket URL: missing host"))

	e.emitLabel(hostOkL)

	portRaw, portPresent := e.curlURLGetPart(handle, curluPartPort)
	defaultPort := e.internString("80")
	portStr, err := e.emitStrBranch(portPresent,
		func() (string, error) { return portRaw, nil },
		func() (string, error) { return defaultPort, nil })
	if err != nil {
		return Value{}, err
	}
	portNum := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @atoll(ptr %s)", portNum, portStr))
	portNum32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", portNum32, portNum))

	pathRaw, pathPresent := e.curlURLGetPart(handle, curluPartPath)
	defaultPath := e.internString("/")
	pathStr, err := e.emitStrBranch(pathPresent,
		func() (string, error) { return pathRaw, nil },
		func() (string, error) { return defaultPath, nil })
	if err != nil {
		return Value{}, err
	}
	queryRaw, queryPresent := e.curlURLGetPart(handle, curluPartQuery)
	qMark := e.internString("?")
	fullPath, err := e.emitStrBranch(queryPresent,
		func() (string, error) {
			withMark, err := e.emitStringConcat(Value{Ref: pathStr, Ty: TypePtr}, Value{Ref: qMark, Ty: TypePtr})
			if err != nil {
				return "", err
			}
			withQuery, err := e.emitStringConcat(withMark, Value{Ref: queryRaw, Ty: TypePtr})
			if err != nil {
				return "", err
			}
			return withQuery.Ref, nil
		},
		func() (string, error) { return pathStr, nil })
	if err != nil {
		return Value{}, err
	}

	// Build the KML-level object first — its own pointer becomes
	// __kml_ws_client_open's `instance` argument below, the two-way link
	// the deferred onopen/onerror/onclose notification (WebSocketClientType's
	// own doc comment) and every later onmessage dispatch need.
	wsTy := WebSocketClientType()
	wsStructIR := wsTy.StructIR()
	objReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", objReg, wsTy.StructSize()))
	storeField := func(name, ir, val string) {
		idx, fieldTy, _ := wsTy.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, wsStructIR, objReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ir, val, gep, fieldTy.Align()))
	}
	storeField("url", "ptr", urlVal.Ref)
	storeField("readyState", "i64", "0")
	storeField("onopen", "ptr", "null")
	storeField("onmessage", "ptr", "null")
	storeField("onclose", "ptr", "null")
	storeField("onerror", "ptr", "null")

	// Sec-WebSocket-Key: 16 random bytes, base64-encoded (RFC 6455 §1.3) —
	// reuses the existing crypto.getRandomValues-backing helper directly,
	// not a fresh PRNG.
	keyBuf := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca [16 x i8], align 1", keyBuf))
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_random_bytes(ptr %s, i64 16)", keyBuf))
	e.ensureBase64EncodeBytes()
	keyVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_base64_encode_bytes(ptr %s, i64 16)", keyVal, keyBuf))

	expectedAccept, err := e.emitComputeWSAccept(Value{Ref: keyVal, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}

	reqPrefix := e.internString("GET ")
	req1, err := e.emitStringConcat(Value{Ref: reqPrefix, Ty: TypePtr}, Value{Ref: fullPath, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	reqMid1 := e.internString(" HTTP/1.1\r\nHost: ")
	req2, err := e.emitStringConcat(req1, Value{Ref: reqMid1, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	req3, err := e.emitStringConcat(req2, Value{Ref: hostRaw, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	reqMid2 := e.internString(":")
	req4, err := e.emitStringConcat(req3, Value{Ref: reqMid2, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	req5, err := e.emitStringConcat(req4, Value{Ref: portStr, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	reqMid3 := e.internString("\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: ")
	req6, err := e.emitStringConcat(req5, Value{Ref: reqMid3, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	req7, err := e.emitStringConcat(req6, Value{Ref: keyVal, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	reqSuffix := e.internString("\r\nSec-WebSocket-Version: 13\r\n\r\n")
	req8, err := e.emitStringConcat(req7, Value{Ref: reqSuffix, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}

	e.ensureStrlen()
	reqLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", reqLen, req8.Ref))

	entryReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ws_client_open(ptr %s, i32 %s, ptr %s, i64 %s, ptr %s, ptr %s)",
		entryReg, hostRaw, portNum32, req8.Ref, reqLen, expectedAccept.Ref, objReg))
	storeField(WebSocketClientHandleField, "ptr", entryReg)

	e.usedWSClient = true
	return Value{Ref: objReg, Ty: wsTy}, nil
}

// emitWSClientSend implements `ws.send(data: string)` — a single masked
// text frame (opcode 1): client-to-server frames MUST be masked (RFC 6455
// §5.1), the one asymmetry from WSConnection's own (server-side, unmasked)
// `.send()`. The mask key is 4 random bytes read as a single big-endian
// i32 via the shared bigEndianLoad helper (runtime_websocket.go) — its
// "meaning" as a number doesn't matter, only that __kml_ws_frame_encode's
// own masking loop and this call agree on what bytes it represents, which
// bigEndianLoad's own convention guarantees regardless of byte order
// semantics.
func (e *Emitter) emitWSClientSend(objExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
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
	e.ensureCryptoRandomBytes()

	fd32, err := e.wsClientLoadFd(objVal)
	if err != nil {
		return Value{}, err
	}

	maskBuf := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca [4 x i8], align 1", maskBuf))
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_random_bytes(ptr %s, i64 4)", maskBuf))
	var maskIR strings.Builder
	maskKey := bigEndianLoad(&maskIR, "wssendmask", maskBuf, 0, 4, "i32")
	e.emitInstr(maskIR.String())

	dataLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", dataLen, dataVal.Ref))
	frame := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { ptr, i64 } @__kml_ws_frame_encode(i32 1, i1 true, ptr %s, i64 %s, i32 %s)", frame, dataVal.Ref, dataLen, maskKey))
	frameBuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 0", frameBuf, frame))
	frameLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 1", frameLen, frame))
	e.emitInstr(fmt.Sprintf("call i64 @write(i32 %s, ptr %s, i64 %s)", fd32, frameBuf, frameLen))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", frameBuf))

	return Value{Ty: TypeVoid}, nil
}

// emitWSClientClose implements `ws.close()`: sends a Close frame (status
// 1000, masked — client-to-server, same requirement `.send()` has above),
// closes the fd directly (without waiting for the server's own echo back —
// the same deliberate simplification WSConnection.close() already makes
// server-side, emit_websocket.go, for the same reason: a real wait would
// need a new timeout state machine), and — unlike a first draft of this
// function — immediately marks the entry CLOSED (state=2, fd=-1,
// decrementing @__kml_wsc_active) and fires onclose itself, rather than
// leaving that to __kml_wsclient_scan's own next pass noticing a failed
// read. Found the first version was a real bug, not just a cosmetic
// difference: leaving the entry's state as OPEN with a now-closed fd meant
// __kml_event_loop_run's own fd-set-registration loop (runtime_http.go)
// kept re-adding that stale fd to select()'s watch set every iteration,
// and select() is permitted (and was, in practice) to keep failing
// (EBADF) as long as an already-closed fd sits in a set it's given —
// which, per that loop's own existing "failed select -> skip straight back
// to outerloop" handling, starved the loop from ever reaching its timer
// check again at all, hanging the whole program.
func (e *Emitter) emitWSClientClose(objExpr ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	e.ensureWSFrameEncode()
	e.ensureFree()
	e.ensureMalloc()
	e.ensureCloseDecl()
	e.ensureCryptoRandomBytes()

	wsTy := WebSocketClientType()
	handleIdx, _, _ := wsTy.FieldIndex(WebSocketClientHandleField)
	handleGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", handleGep, wsTy.StructIR(), objVal.Ref, handleIdx))
	entryReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", entryReg, handleGep))

	statePtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", statePtr, wsClientEntryStructIR, entryReg))
	stateVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", stateVal, statePtr))
	alreadyClosed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 2", alreadyClosed, stateVal))

	doCloseL := e.freshLabel("wsc.doclose")
	doneL := e.freshLabel("wsc.closedone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", alreadyClosed, doneL, doCloseL))

	e.emitLabel(doCloseL)
	fdPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", fdPtr, wsClientEntryStructIR, entryReg))
	fd64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fd64, fdPtr))
	fd32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", fd32, fd64))

	maskBuf := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca [4 x i8], align 1", maskBuf))
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_random_bytes(ptr %s, i64 4)", maskBuf))
	var maskIR strings.Builder
	maskKey := bigEndianLoad(&maskIR, "wsclosemask", maskBuf, 0, 4, "i32")
	e.emitInstr(maskIR.String())

	codeBuf := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca [2 x i8], align 1", codeBuf))
	codeP0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 0", codeP0, codeBuf))
	e.emitInstr(fmt.Sprintf("store i8 3, ptr %s, align 1", codeP0))
	codeP1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 1", codeP1, codeBuf))
	e.emitInstr(fmt.Sprintf("store i8 232, ptr %s, align 1", codeP1))

	closeEnc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { ptr, i64 } @__kml_ws_frame_encode(i32 8, i1 true, ptr %s, i64 2, i32 %s)", closeEnc, codeBuf, maskKey))
	closeEncBuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 0", closeEncBuf, closeEnc))
	closeEncLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 1", closeEncLen, closeEnc))
	e.emitInstr(fmt.Sprintf("call i64 @write(i32 %s, ptr %s, i64 %s)", fd32, closeEncBuf, closeEncLen))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", closeEncBuf))
	e.emitInstr(fmt.Sprintf("call i32 @close(i32 %s)", fd32))

	e.emitInstr(fmt.Sprintf("store i64 2, ptr %s, align 8", statePtr))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", fdPtr))
	activeNow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_wsc_active, align 8", activeNow))
	activeNew := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", activeNew, activeNow))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr @__kml_wsc_active, align 8", activeNew))
	rsIdx, _, _ := wsTy.FieldIndex("readyState")
	rsGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", rsGep, wsTy.StructIR(), objVal.Ref, rsIdx))
	e.emitInstr(fmt.Sprintf("store i64 2, ptr %s, align 8", rsGep))

	oncloseIdx, _, _ := wsTy.FieldIndex("onclose")
	oncloseGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", oncloseGep, wsTy.StructIR(), objVal.Ref, oncloseIdx))
	onclosePtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", onclosePtr, oncloseGep))
	hasOnClose := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasOnClose, onclosePtr))
	callOnCloseL := e.freshLabel("wsc.callonclose")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasOnClose, callOnCloseL, doneL))

	e.emitLabel(callOnCloseL)
	cb := Callback{kind: cbClosure, hdrPtr: onclosePtr, ty: FuncType([]Type{WSMessageEventType()}, TypeVoid)}
	emptyMsg, err := e.buildEmptyWSMessageEvent()
	if err != nil {
		return Value{}, err
	}
	if _, err := e.emitCBCall(cb, []Value{emptyMsg}); err != nil {
		return Value{}, err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	return Value{Ty: TypeVoid}, nil
}

// buildEmptyWSMessageEvent allocates a WSMessageEventType() with data set
// to the empty string — the payload shape onclose/onopen/onerror all share
// (WebSocketClientType's own doc comment in types.go).
func (e *Emitter) buildEmptyWSMessageEvent() (Value, error) {
	msgTy := WSMessageEventType()
	msgReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", msgReg, msgTy.StructSize()))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString(""), msgReg))
	return Value{Ref: msgReg, Ty: msgTy}, nil
}

// wsClientLoadFd loads the current fd out of ws's hidden runtime entry, for
// .send() — unlike .close() (which also needs to update the entry's own
// state/readyState and fire onclose, so it loads the entry pointer itself
// directly rather than going through this helper), .send() only ever needs
// the raw fd.
func (e *Emitter) wsClientLoadFd(objVal Value) (string, error) {
	wsTy := WebSocketClientType()
	handleIdx, _, _ := wsTy.FieldIndex(WebSocketClientHandleField)
	handleGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", handleGep, wsTy.StructIR(), objVal.Ref, handleIdx))
	entryReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", entryReg, handleGep))

	fdGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", fdGep, wsClientEntryStructIR, entryReg))
	fd64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fd64, fdGep))
	fd32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", fd32, fd64))
	return fd32, nil
}
