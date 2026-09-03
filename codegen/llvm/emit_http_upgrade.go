// emit_http_upgrade.go — the Node-faithful HTTP `'upgrade'` event
// (TDD-00158 Stage 1): `server.on('upgrade', (req, socket, head) => …)` on an
// http/https server. `socket` is a real `net.Socket` over the connection fd
// (TLS-aware via the same conn shims https.createServer uses, so `wss://`
// falls out); the connection fiber, after the handler returns, runs a generic
// raw read loop firing the socket's `'data'`/`'end'` listeners.
//
// Two halves live here: a whole-program pre-scan (programUsesHTTPUpgrade,
// called from EmitProgram) that decides whether buildHTTPDispatcher emits the
// upgrade block at all — needed because `.on('upgrade')` is a separate
// statement that may be emitted after the dispatcher — and the dispatcher-side
// emission (emit_http.go calls into it at the upgrade seam).
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitUpgradeReqDetect branches to upgradeL when the request is an HTTP
// upgrade (an `Upgrade:` header present and a `Connection:` header carrying an
// `upgrade` token), else to normalL. Unlike emitWSUpgradeDetect (which also
// requires the `websocket` token), this is protocol-agnostic — Node fires
// `'upgrade'` for any upgrade request and lets the handler inspect
// `req.headers.upgrade` itself (RFC 7230 §6.7).
func (e *Emitter) emitUpgradeReqDetect(headersMapFinal, upgradeL, normalL string) {
	e.ensureMapStrHelpers()
	e.ensureStringToLower()
	e.ensureStrstr()

	upgradeKey := e.internString("upgrade")
	connectionKey := e.internString("connection")
	upgradeToken := e.internString("upgrade")

	checkConnHdrL := e.freshLabel("http.upgcheckconnhdr")
	checkConnValL := e.freshLabel("http.upgcheckconnval")

	hasUpgradeHdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", hasUpgradeHdr, headersMapFinal, upgradeKey))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasUpgradeHdr, checkConnHdrL, normalL))

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

// emitHTTPUpgradeBlock emits the Node-faithful `'upgrade'` dispatch at the
// connection fiber's header-parse seam (TDD-00158 Stage 1). When an upgrade
// handler is registered at runtime AND the request is an upgrade, it builds
// (req, socket, head), invokes the handler, then runs a generic raw read loop
// that drives the socket's `'data'`/`'end'`/`'close'` listeners for the rest
// of the connection — the exact swapcontext-yield-on-EAGAIN structure the WS
// server loop uses, minus frame decoding. Control never returns to normal
// request handling on the upgrade path; a non-upgrade (or no-handler) request
// falls through to normalL unchanged. TLS I/O falls out: when usedHTTPS1Server
// the read routes through __kml_http_conn_recv and socket.write already routes
// through the SSL* in the socket's field 5 (so wss:// works with no extra
// code). noReqL is the connection-teardown label reused on EOF/error.
func (e *Emitter) emitHTTPUpgradeBlock(headersMapFinal, methodPtr, pathOnly, queryMapFinal, fd32, fd64, bufFinal, headerEndA, totalReadA, noReqL string) {
	e.ensureHTTPRuntime()
	// The upgrade `socket` is a net.Socket; user code calls .write/.end/.on on
	// it, whose runtime helpers are emitted by ensureNetRuntime — normally
	// triggered by a net.* call, but an upgrade socket reaches them without one.
	e.ensureNetRuntime()
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureReadDecl()
	e.ensureCloseDecl()
	e.ensureErrnoAccessor()
	if e.usedHTTPS1Server {
		e.emitHTTPSConnShims()
	}

	handlerNullL := e.freshLabel("http.upgnohandler")
	detectL := e.freshLabel("http.upgdetect")
	doL := e.freshLabel("http.upgdo")
	normalL := e.freshLabel("http.upgnormal")

	// Runtime guard: only bother detecting an upgrade when a handler exists.
	upH := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_listen_upgrade_handler, align 8", upH))
	hasUpH := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasUpH, upH))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasUpH, detectL, handlerNullL))

	e.emitLabel(handlerNullL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", normalL))

	e.emitLabel(detectL)
	e.emitUpgradeReqDetect(headersMapFinal, doL, normalL)

	e.emitLabel(doL)

	// --- head: the bytes already read past the header terminator ---
	headerEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", headerEnd, headerEndA))
	bodyStart := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 4", bodyStart, headerEnd))
	totalRead := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", totalRead, totalReadA))
	headLenRaw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", headLenRaw, totalRead, bodyStart))
	headNeg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", headNeg, headLenRaw))
	headLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", headLen, headNeg, headLenRaw))
	headBuf := e.emitStringAlloc(headLen)
	headSrc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", headSrc, bufFinal, bodyStart))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", headBuf, headSrc, headLen))
	headTerm := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", headTerm, headBuf, headLen))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", headTerm))

	// --- socket: a net.Socket (netSocketIR) over this fd, TLS-aware ---
	sockPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 64)", sockPtr))
	sockFdGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", sockFdGep, netSocketIR, sockPtr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", fd64, sockFdGep))
	if e.usedHTTPS1Server {
		sslReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_http_conn_ssl_get(i32 %s)", sslReg, fd32))
		sockSSLGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 5", sockSSLGep, netSocketIR, sockPtr))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", sslReg, sockSSLGep))
	}

	// --- req: an HttpRequest/IncomingMessage over the parsed request line ---
	reqTy := RequestType()
	reqReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", reqReg, reqTy.StructSize()))
	reqIR := reqTy.StructIR()
	storeReqField := func(name, ir, ref string) {
		idx, fieldTy, _ := reqTy.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, reqIR, reqReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ir, ref, gep, fieldTy.Align()))
	}
	storeReqField("method", "ptr", methodPtr)
	storeReqField("path", "ptr", pathOnly)
	storeReqField("query", "ptr", queryMapFinal)
	storeReqField("headers", "ptr", headersMapFinal)
	storeReqField("body", "ptr", e.internString(""))
	storeReqField("bodyLength", "i64", "0")
	storeReqField("__kml_bodyctx", "ptr", "null")

	// --- invoke handler(req, socket, head) ---
	fpSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpSlot, upH))
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpSlot))
	epSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", epSlot, upH))
	ep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epSlot))
	e.emitInstr(fmt.Sprintf("call void %s(ptr %s, ptr %s, ptr %s, ptr %s)", fp, ep, reqReg, sockPtr, headBuf))

	// --- the generic raw read loop, driving socket data/end/close ---
	e.emitHTTPRawSocketLoop(sockPtr, noReqL)

	e.emitLabel(normalL)
}

// emitHTTPRawSocketLoop is the post-upgrade read pump running on this
// connection's fiber: read raw bytes (TLS-aware when usedHTTPS1Server), fire
// the socket's `'data'` listener with each chunk as a length-prefixed Buffer,
// yield the fiber on EAGAIN, and on EOF/error fire `'end'` then `'close'`
// before tearing the connection down (br noReqL). Every value crossing the
// swapcontext yield lives in an alloca; fd/ctx are re-derived from the conn
// array each pass (its backing storage can move while suspended) — the exact
// invariants emitWSServerLoop documents. sockPtr is a stable malloc, held in
// an alloca so the listener slots survive the yield.
func (e *Emitter) emitHTTPRawSocketLoop(sockPtr, noReqL string) {
	e.ensureRealloc()

	sockA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", sockA))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", sockPtr, sockA))
	rbufA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", rbufA))
	rbuf0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 4096)", rbuf0))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", rbuf0, rbufA))

	loopL := e.freshLabel("http.upgloop")
	gotL := e.freshLabel("http.upggot")
	checkErrL := e.freshLabel("http.upgcheckerr")
	eagainL := e.freshLabel("http.upgeagain")
	yieldL := e.freshLabel("http.upgyield")
	eofL := e.freshLabel("http.upgeof")

	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))

	// loopL: re-derive fd/ctx fresh (conn array may have moved across a yield).
	e.emitLabel(loopL)
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
	rbuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rbuf, rbufA))

	nReg := e.freshReg()
	readFn := fmt.Sprintf("@read(i32 %s, ptr %s, i64 4096)", fd32, rbuf)
	if e.usedHTTPS1Server {
		readFn = fmt.Sprintf("@__kml_http_conn_recv(i32 %s, ptr %s, i64 4096)", fd32, rbuf)
	}
	e.emitInstr(fmt.Sprintf("%s = call i64 %s", nReg, readFn))
	gotData := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 0", gotData, nReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", gotData, gotL, checkErrL))

	// gotL: fire the 'data' listener (field 2) with a fresh length-prefixed
	// Buffer chunk, then loop for more.
	e.emitLabel(gotL)
	chunk := e.emitStringAlloc(nReg)
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", chunk, rbuf, nReg))
	chunkTerm := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", chunkTerm, chunk, nReg))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", chunkTerm))
	sockNow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", sockNow, sockA))
	dlGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", dlGep, netSocketIR, sockNow))
	dl := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dl, dlGep))
	hasDL := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasDL, dl))
	fireL := e.freshLabel("http.upgfiredata")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasDL, fireL, loopL))
	e.emitLabel(fireL)
	dfpGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", dfpGep, dl))
	dfp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dfp, dfpGep))
	depGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", depGep, dl))
	dep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dep, depGep))
	e.emitInstr(fmt.Sprintf("call void %s(ptr %s, ptr %s, i64 %s)", dfp, dep, chunk, nReg))
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))

	// checkErrL: 0 = EOF → teardown; <0 → EAGAIN yields, else teardown.
	e.emitLabel(checkErrL)
	isZero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isZero, nReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isZero, eofL, eagainL))

	e.emitLabel(eagainL)
	errnoPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @%s()", errnoPtr, errnoAccessor()))
	errnoVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", errnoVal, errnoPtr))
	isEagain := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, %d", isEagain, errnoVal, httpEagainErrno()))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isEagain, yieldL, eofL))

	e.emitLabel(yieldL)
	ctxPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ctxPtr, ctxPtrSlot))
	e.emitInstr(fmt.Sprintf("call i32 @swapcontext(ptr %s, ptr @__kml_main_ctx)", ctxPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))

	// eofL: fire 'end' (field 3) then 'close' (field 6), then tear down.
	e.emitLabel(eofL)
	sockEof := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", sockEof, sockA))
	e.emitFireSocketVoidListener(sockEof, 3)
	e.emitFireSocketVoidListener(sockEof, 6)
	e.emitTerminator(fmt.Sprintf("br label %%%s", noReqL))
}

// emitFireSocketVoidListener fires a zero-arg listener closure stored at
// field idx of a netSocketIR socket, if present (the 'end'/'close' shape).
func (e *Emitter) emitFireSocketVoidListener(sockPtr string, idx int) {
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, netSocketIR, sockPtr, idx))
	cl := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cl, gep))
	has := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", has, cl))
	fireL := e.freshLabel("http.upgfirevoid")
	skipL := e.freshLabel("http.upgskipvoid")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, fireL, skipL))
	e.emitLabel(fireL)
	fpGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpGep, cl))
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpGep))
	epGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", epGep, cl))
	epr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", epr, epGep))
	e.emitInstr(fmt.Sprintf("call void %s(ptr %s)", fp, epr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
	e.emitLabel(skipL)
}

// programUsesHTTPUpgrade reports whether the program registers a Node
// `'upgrade'` handler anywhere (`server.on('upgrade', …)` / `.once`). A
// recursive whole-program walk rather than a shallow top-level scan: the
// registration can sit inside any function body, block, or expression, and a
// miss would silently drop the handler (the dispatcher would omit the upgrade
// block, so the stored handler global is never read). The `.on('upgrade')`
// registration site (emitHTTPServerMethod) carries a compile-error safety net
// for the theoretical case this walk misses a container type.
func programUsesHTTPUpgrade(prog *ast.Program) bool {
	found := false
	for _, s := range prog.Body {
		walkStmtForUpgrade(s, &found)
		if found {
			return true
		}
	}
	return false
}

// isUpgradeOnCall reports whether ex is `<obj>.on("upgrade", …)` or
// `<obj>.once("upgrade", …)` — the shape that registers a Node upgrade
// handler. The receiver type isn't checked here (that's the registration
// site's job); this is a conservative pre-scan, so a false positive only costs
// a little dead IR, never correctness.
func isUpgradeOnCall(ex *ast.CallExpression) bool {
	mem, ok := ex.Callee.(*ast.MemberExpression)
	if !ok || (mem.Property != "on" && mem.Property != "once") {
		return false
	}
	if len(ex.Args) < 1 {
		return false
	}
	lit, ok := ex.Args[0].(*ast.StringLiteral)
	return ok && lit.Value == "upgrade"
}

func walkStmtForUpgrade(s ast.Statement, found *bool) {
	if *found || s == nil {
		return
	}
	switch n := s.(type) {
	case *ast.BlockStatement:
		walkBlockForUpgrade(n, found)
	case *ast.ExpressionStatement:
		walkExprForUpgrade(n.Expr, found)
	case *ast.VarDeclaration:
		walkExprForUpgrade(n.Init, found)
	case *ast.VarDeclarationList:
		for _, d := range n.Decls {
			walkStmtForUpgrade(d, found)
		}
	case *ast.ReturnStatement:
		walkExprForUpgrade(n.Value, found)
	case *ast.ThrowStatement:
		walkExprForUpgrade(n.Argument, found)
	case *ast.IfStatement:
		walkExprForUpgrade(n.Test, found)
		walkStmtForUpgrade(n.Consequent, found)
		walkStmtForUpgrade(n.Alternate, found)
	case *ast.ForStatement:
		walkStmtForUpgrade(n.Init, found)
		walkExprForUpgrade(n.Test, found)
		for _, u := range n.Update {
			walkExprForUpgrade(u, found)
		}
		walkStmtForUpgrade(n.Body, found)
	case *ast.ForOfStatement:
		walkExprForUpgrade(n.Iterable, found)
		walkStmtForUpgrade(n.Body, found)
	case *ast.ForInStatement:
		walkStmtForUpgrade(n.Body, found)
	case *ast.WhileStatement:
		walkExprForUpgrade(n.Test, found)
		walkStmtForUpgrade(n.Body, found)
	case *ast.DoWhileStatement:
		walkStmtForUpgrade(n.Body, found)
		walkExprForUpgrade(n.Test, found)
	case *ast.SwitchStatement:
		walkExprForUpgrade(n.Discriminant, found)
		for _, c := range n.Cases {
			walkExprForUpgrade(c.Test, found)
			for _, cs := range c.Body {
				walkStmtForUpgrade(cs, found)
			}
		}
	case *ast.TryStatement:
		walkBlockForUpgrade(n.Body, found)
		if n.Catch != nil {
			walkBlockForUpgrade(n.Catch.Body, found)
		}
		walkBlockForUpgrade(n.Finally, found)
	case *ast.LabeledStatement:
		walkStmtForUpgrade(n.Body, found)
	case *ast.FunctionDeclaration:
		walkBlockForUpgrade(n.Body, found)
	case *ast.ExportDeclaration:
		walkStmtForUpgrade(n.Decl, found)
	}
}

func walkBlockForUpgrade(b *ast.BlockStatement, found *bool) {
	if b == nil || *found {
		return
	}
	for _, s := range b.Body {
		walkStmtForUpgrade(s, found)
		if *found {
			return
		}
	}
}

func walkExprForUpgrade(ex ast.Expression, found *bool) {
	if *found || ex == nil {
		return
	}
	switch n := ex.(type) {
	case *ast.CallExpression:
		if isUpgradeOnCall(n) {
			*found = true
			return
		}
		walkExprForUpgrade(n.Callee, found)
		for _, a := range n.Args {
			walkExprForUpgrade(a, found)
		}
	case *ast.MemberExpression:
		walkExprForUpgrade(n.Object, found)
	case *ast.IndexExpression:
		walkExprForUpgrade(n.Object, found)
		walkExprForUpgrade(n.Index, found)
	case *ast.BinaryExpression:
		walkExprForUpgrade(n.Left, found)
		walkExprForUpgrade(n.Right, found)
	case *ast.AssignmentExpression:
		walkExprForUpgrade(n.Left, found)
		walkExprForUpgrade(n.Right, found)
	case *ast.ConditionalExpression:
		walkExprForUpgrade(n.Test, found)
		walkExprForUpgrade(n.Consequent, found)
		walkExprForUpgrade(n.Alternate, found)
	case *ast.SequenceExpression:
		for _, e := range n.Exprs {
			walkExprForUpgrade(e, found)
		}
	case *ast.UnaryExpression:
		walkExprForUpgrade(n.Arg, found)
	case *ast.UpdateExpression:
		walkExprForUpgrade(n.Arg, found)
	case *ast.SpreadElement:
		walkExprForUpgrade(n.Arg, found)
	case *ast.AwaitExpression:
		walkExprForUpgrade(n.Argument, found)
	case *ast.YieldExpression:
		walkExprForUpgrade(n.Argument, found)
	case *ast.ArrayLiteral:
		for _, e := range n.Elements {
			walkExprForUpgrade(e, found)
		}
	case *ast.ObjectLiteral:
		for _, p := range n.Properties {
			walkExprForUpgrade(p.KeyExpr, found)
			walkExprForUpgrade(p.Value, found)
		}
	case *ast.TemplateLiteral:
		for _, e := range n.Exprs {
			walkExprForUpgrade(e, found)
		}
	case *ast.NewExpression:
		for _, a := range n.Args {
			walkExprForUpgrade(a, found)
		}
	case *ast.ArrowFunction:
		walkExprForUpgrade(n.Body, found)
		walkBlockForUpgrade(n.Block, found)
	case *ast.FunctionExpression:
		walkBlockForUpgrade(n.Body, found)
	}
}
