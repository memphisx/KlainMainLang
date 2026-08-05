// emit_http.go — http.listen(port, handler): a minimal single-threaded HTTP
// server (TDD-00004 V1) built on the generalized event loop (TDD-00006 Part
// 1, see runtime.go's ensureHTTPRuntime). Bare global function, like fetch —
// not a namespace with multiple methods, since V1 has no need for multiple
// servers, inspecting server state, or a .close(). http.listen never
// returns, the same category of thing as process.exit().
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// isPlainStringType reports whether t is a bare string (IR "ptr" with none
// of the other flags that mark a more specific ptr-shaped type — object,
// array, Map/Set, closure, Promise, Date, class instance, ...). Used to
// validate that a http.listen response's optional `headers` field really is
// Map<string, string>, since (unlike status/body, which just get coerced)
// treating a wrong-shaped value as a Map and walking its "entries" at
// runtime would corrupt memory rather than just misbehave.
func isPlainStringType(t Type) bool {
	return t.IR == "ptr" && !t.IsArray && !t.IsObject && !t.IsMap && !t.IsSet &&
		!t.IsFunc && !t.IsDynamic && !t.IsPromise && !t.IsDate && !t.IsResponse &&
		!t.IsClass && !t.IsGroupMap && !t.IsNull && !t.IsUndefined
}

// emitHTTPListen validates its arguments (port: number, handler:
// (req: Request) => T where T has status/body fields, and optionally a
// headers: Map<string,string> field, plus an optional third { workers: N }
// options object — TDD-00025), binds and listens on the given port, builds a
// dispatcher function specialized to the handler's own closure/return type
// (since reading status/body/headers off an arbitrary user-declared return
// type needs Go-side knowledge of its field offsets, unlike the fully
// generic timer/qsort trampolines), forks into N worker processes sharing
// that one listening socket (a no-op when the third argument is omitted or
// N <= 1 — today's single-process behavior, unchanged), registers the
// dispatcher with the event loop, and hands control to it.
func (e *Emitter) emitHTTPListen(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 && len(args) != 3 {
		return Value{}, fmt.Errorf("%d:%d: http.listen takes 2 arguments (port, handler) or 3 (port, handler, { workers: N })", pos.Line, pos.Col)
	}
	portVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	portVal = e.coerce(portVal, TypeI64)

	handlerVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	if !handlerVal.Ty.IsFunc {
		return Value{}, fmt.Errorf("%d:%d: http.listen's second argument must be a function", pos.Line, pos.Col)
	}
	if len(handlerVal.Ty.FuncParams) != 1 {
		return Value{}, fmt.Errorf("%d:%d: http.listen's handler must take exactly one parameter (req: Request)", pos.Line, pos.Col)
	}
	if handlerVal.Ty.FuncRetType == nil || (!handlerVal.Ty.FuncRetType.IsObject && !handlerVal.Ty.FuncRetType.IsPromise) {
		return Value{}, fmt.Errorf("%d:%d: http.listen's handler must return an object type (or Promise of one, for an async handler) with status/body fields", pos.Line, pos.Col)
	}
	// An async handler (needed for `await fetch(...)` inside it, the main
	// reason to want one) declares itself as returning Promise<T>; the
	// dispatcher calls it exactly like a sync handler (the call doesn't
	// return until the handler's body has actually finished — any internal
	// await yields the *connection's own fiber* via swapcontext, transparent
	// to this call) and then unwraps T from the returned promise slot, the
	// same load+free shape emitAwait's own generic (non-fetch) unwrap uses.
	isAsyncHandler := handlerVal.Ty.FuncRetType.IsPromise
	retTy := *handlerVal.Ty.FuncRetType
	if isAsyncHandler {
		if retTy.PromiseType == nil {
			return Value{}, fmt.Errorf("%d:%d: http.listen's async handler must return Promise<T> where T has status/body fields", pos.Line, pos.Col)
		}
		retTy = *retTy.PromiseType
	}
	if _, _, ok := retTy.FieldIndex("status"); !ok {
		return Value{}, fmt.Errorf("%d:%d: http.listen's handler return type must have a 'status: number' field", pos.Line, pos.Col)
	}
	if _, _, ok := retTy.FieldIndex("body"); !ok {
		return Value{}, fmt.Errorf("%d:%d: http.listen's handler return type must have a 'body: string' field", pos.Line, pos.Col)
	}
	if _, hTy, ok := retTy.FieldIndex("headers"); ok {
		if !hTy.IsMap || hTy.MapKey == nil || hTy.MapVal == nil ||
			!isPlainStringType(*hTy.MapKey) || !isPlainStringType(*hTy.MapVal) {
			return Value{}, fmt.Errorf("%d:%d: http.listen's handler return type's 'headers' field must be Map<string, string>", pos.Line, pos.Col)
		}
	}
	paramTy := handlerVal.Ty.FuncParams[0]

	// Third argument, { workers: N } (TDD-00025): any value whose inferred
	// type has a numeric "workers" field — no shared ListenOptions interface
	// has to exist, matching the same FieldIndex-on-the-inferred-type
	// pattern fetch's own optional init object already uses (emit_fetch.go).
	// Absent (the two-argument form) means 1 worker, byte-identical to
	// today's single-process behavior — the interned "1" is a literal
	// operand, not a register, since nothing needed evaluating.
	workersRef := "1"
	if len(args) == 3 {
		optsVal, err := e.emitExpr(args[2])
		if err != nil {
			return Value{}, err
		}
		if !optsVal.Ty.IsObject {
			return Value{}, fmt.Errorf("%d:%d: http.listen's third argument must be an object with a 'workers' field", pos.Line, pos.Col)
		}
		idx, fieldTy, ok := optsVal.Ty.FieldIndex("workers")
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: http.listen's third argument must have a 'workers: number' field", pos.Line, pos.Col)
		}
		if fieldTy.IR != "i64" || fieldTy.Float {
			return Value{}, fmt.Errorf("%d:%d: http.listen's 'workers' field must be a number", pos.Line, pos.Col)
		}
		workersRef = e.loadFieldValue(optsVal, idx, fieldTy).Ref
	}

	e.ensureHTTPRuntime()

	port32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", port32, portVal.Ref))
	listenfd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_http_bind_and_listen(i32 %s)", listenfd, port32))

	// Fork right here — after bind+listen succeeds, before any
	// connection-fiber state exists (@__kml_conn_data/len/cap, set up by
	// buildHTTPDispatcher/the event loop below). Every process that falls
	// through (the original plus every fork) shares this same listenfd and
	// proceeds identically from here on — see TDD-00025's Design section.
	e.emitInstr(fmt.Sprintf("call void @__kml_http_cluster_fork(i64 %s)", workersRef))

	if err := e.buildHTTPDispatcher(paramTy, retTy, isAsyncHandler); err != nil {
		return Value{}, err
	}

	e.emitInstr(fmt.Sprintf("store i32 %s, ptr @__kml_listen_fd, align 4", listenfd))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_listen_handler, align 8", handlerVal.Ref))
	e.emitInstr("store ptr @__kml_http_dispatch, ptr @__kml_listen_dispatch, align 8")
	e.emitInstr("call void @__kml_event_loop_run()")
	e.emitTerminator("unreachable")
	return Value{Ty: TypeVoid}, nil
}

// emitClusterIsPrimary/emitClusterWorkerID implement the read-only
// cluster.isPrimary/cluster.workerId globals (TDD-00025) — 0 for the
// original process, 1..N-1 for each fork spawned by
// __kml_http_cluster_fork. ensureHTTPClusterFork() is called here too (not
// just from http.listen itself) so @__kml_cluster_worker_id is guaranteed
// declared even in the (unusual but valid) case a program reads cluster.*
// textually before its http.listen call.
func (e *Emitter) emitClusterIsPrimary() (Value, error) {
	e.ensureHTTPClusterFork()
	id := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_cluster_worker_id, align 8", id))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", r, id))
	return Value{Ref: r, Ty: TypeBool}, nil
}

func (e *Emitter) emitClusterWorkerID() (Value, error) {
	e.ensureHTTPClusterFork()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_cluster_worker_id, align 8", r))
	return Value{Ref: r, Ty: TypeI64}, nil
}

// maxHTTPRequestBytes bounds how far buildHTTPDispatcher's read buffer will
// grow for a single request (headers + body together) — a safety cap
// against a hostile/malformed Content-Length (or a client that never sends
// the blank-line header terminator) trying to make this fiber grow its
// buffer without bound. Hitting the cap aborts the connection the same way
// any other malformed-request case here does (noReqL: close, no response) —
// not a proper 413 response yet, a documented V1 limitation.
const maxHTTPRequestBytes = 10 * 1024 * 1024

// buildHTTPDispatcher emits @__kml_http_dispatch, a void() top-level function
// that becomes each accepted connection's own fiber entry point (via
// makecontext, in runtime.go's __kml_http_append_conn) — not a single
// shared dispatcher called once per event-loop wakeup the way V1 originally
// worked, but a per-connection fiber body that can yield (swapcontext back
// to the scheduler) and be resumed later, exactly where it left off, when
// its socket has no data yet. Finds "which connection is this" via
// @__kml_current_conn_idx (set by the scheduler immediately before
// resuming a fiber — safe since fibers are cooperative, never preempted).
//
// Only the read path is fiber-aware (non-blocking read + yield-on-EAGAIN):
// write() is kept as a single blocking call in __kml_http_send_response,
// a deliberate V1 simplification — local socket writes essentially never
// block for responses this small, so making them fiber-aware too would add
// real complexity for a case that doesn't come up in practice at this scope.
//
// The read buffer accumulates across any number of individual read()
// calls (growing via realloc as needed, up to maxHTTPRequestBytes) rather
// than assuming one read() call returns an entire request — necessary once
// a request's headers+body can legitimately exceed one read()'s worth of
// kernel buffer (ADR-00072). The buffer pointer/capacity/bytes-read-so-far,
// and header-parsing state, all live as plain allocas in this function's
// entry block: since this whole function is the fiber's own permanent
// stack frame (only ever entered once, via makecontext, not re-entered per
// event-loop tick), those allocas persist naturally across any number of
// internal swapcontext suspends — exactly like the fd/ctx GEPs below
// already do.
//
// paramTy/retTy are captured from the call site — this is why the
// dispatcher is built per call site rather than being one fully generic
// hand-written IR helper like the timer trampoline: reading status/body off
// an arbitrary user-declared return type needs Go-side knowledge of its
// field offsets. A fixed name is safe: only one http.listen call site is
// ever reachable in V1, since the first one never returns (any second call
// in the same program is dead code). isAsyncHandler is true when the
// handler itself is `async` (needed to `await fetch(...)` inside it, the
// main reason to want one) — retTy is already unwrapped from Promise<T> to
// T by the caller (emitHTTPListen) either way.
func (e *Emitter) buildHTTPDispatcher(paramTy, retTy Type, isAsyncHandler bool) error {
	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedScopes := e.scopes
	savedRetType := e.currentRetType
	savedBlockDone := e.blockDone

	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.scopes = nil
	e.blockDone = false
	e.currentRetType = TypeVoid
	e.pushScope()

	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemcpy()
	e.ensureStrstr()
	e.ensureAtoll()
	e.ensureMapStrHelpers()
	e.ensureHTTPParseHeaders()
	e.ensureHTTPParseQuery()
	e.ensureSplitFirst()

	bufPtrA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", bufPtrA))
	bufCapA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", bufCapA))
	totalReadA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", totalReadA))
	headersParsedA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", headersParsedA))
	headerEndA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", headerEndA))
	contentLenA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", contentLenA))
	headersMapA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", headersMapA))

	initBuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8192)", initBuf))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", initBuf, bufPtrA))
	e.emitInstr(fmt.Sprintf("store i64 8192, ptr %s, align 8", bufCapA))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", totalReadA))
	e.emitInstr(fmt.Sprintf("store i1 0, ptr %s, align 1", headersParsedA))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", headerEndA))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", contentLenA))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", headersMapA))

	readLoopL := e.freshLabel("http.readloop")
	growL := e.freshLabel("http.grow")
	doGrowL := e.freshLabel("http.dogrow")
	haveCapL := e.freshLabel("http.havecap")
	checkErrL := e.freshLabel("http.checkerr")
	checkEagainL := e.freshLabel("http.checkeagain")
	doYieldL := e.freshLabel("http.doyield")
	accumulateL := e.freshLabel("http.accumulate")
	checkCompleteL := e.freshLabel("http.checkcomplete")
	findHeaderEndL := e.freshLabel("http.findheaderend")
	gotHeaderEndL := e.freshLabel("http.gotheaderend")
	haveCLL := e.freshLabel("http.havecl")
	noCLL := e.freshLabel("http.nocl")
	mergeCLL := e.freshLabel("http.mergecl")
	checkBodyCompleteL := e.freshLabel("http.checkbodycomplete")
	parseL := e.freshLabel("http.parse")
	haveQueryL := e.freshLabel("http.havequery")
	noQueryL := e.freshLabel("http.noquery")
	mergeQueryL := e.freshLabel("http.mergequery")
	noReqL := e.freshLabel("http.noreq")
	e.emitTerminator(fmt.Sprintf("br label %%%s", readLoopL))

	// readLoopL: is there enough spare capacity to read into? Grow first if
	// not, then issue the read() at the current end-of-buffer offset.
	// fd64/fd32 are computed here (not in haveCapL) so they dominate every
	// path that can reach noReqL, including growL's "too big" abort path,
	// which never passes through haveCapL at all.
	e.emitLabel(readLoopL)
	// selfIdx/connData/selfSlot/fdPtr/ctxPtrSlot are recomputed here, at the
	// top of the read loop, rather than once at fiber-entry — this label is
	// re-entered both on first entry and on every resume-after-yield
	// (doYieldL's own "br label %readLoopL" below), and @__kml_conn_data's
	// backing buffer can be realloc()'d (moved) by __kml_http_append_conn
	// while this fiber is suspended, if enough new connections are accepted
	// concurrently to grow the array past its current capacity. A pointer
	// derived from a pre-suspend snapshot of that buffer would dangle after
	// such a move — a real, confirmed use-after-free (found chasing an
	// intermittent request-never-answered hang under concurrent load from a
	// persistent-connection HTTP client; see docs/adr for the ADR fixing
	// this). Recomputing here instead means every use of fdPtr/ctxPtrSlot
	// between here and the next swapcontext (checkErrL/checkEagainL/
	// doYieldL, and — since no further yield happens once the request is
	// fully buffered — all the way through parseL/noReqL's own final
	// `store i64 -1, ptr fdPtr`) is always derived from the current buffer.
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
	usedPlus1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", usedPlus1, trNow0))
	remain := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", remain, capNow0, usedPlus1))
	needGrow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 4096", needGrow, remain))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", needGrow, growL, haveCapL))

	// growL: double the buffer, aborting instead (closing the connection,
	// same as any other malformed-request case here) if that would exceed
	// maxHTTPRequestBytes — see its doc comment.
	e.emitLabel(growL)
	curCap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curCap, bufCapA))
	newCap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 2", newCap, curCap))
	tooBig := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %d", tooBig, newCap, maxHTTPRequestBytes))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", tooBig, noReqL, doGrowL))

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
	readCapMinus1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", readCapMinus1, capForRead, trForRead))
	readCap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", readCap, readCapMinus1))
	nReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @read(i32 %s, ptr %s, i64 %s)", nReg, fd32, readPtr, readCap))
	gotData := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 0", gotData, nReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", gotData, accumulateL, checkErrL))

	// checkErrL/checkEagainL/doYieldL: unchanged from before this feature —
	// they only ever reference nReg/fdPtr/ctxPtrSlot, never buf itself, so
	// doYieldL's own "br label %readLoopL" is already the right resume
	// point now that readLoopL itself is offset-aware.
	e.emitLabel(checkErrL)
	isZero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isZero, nReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isZero, noReqL, checkEagainL))

	e.emitLabel(checkEagainL)
	e.ensureErrnoAccessor()
	errnoPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @%s()", errnoPtr, errnoAccessor()))
	errnoVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", errnoVal, errnoPtr))
	isEagain := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, %d", isEagain, errnoVal, httpEagainErrno()))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isEagain, doYieldL, noReqL))

	e.emitLabel(doYieldL)
	ctxPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ctxPtr, ctxPtrSlot))
	// gc mode: GC_stackbottom is restored on the *resumer's* side (see the
	// gcRestoreStackbottom/gcRestoreRStackbottom comments in
	// runtime_http.go's __kml_http_append_conn/__kml_event_loop_run), right
	// after this swapcontext call returns there — not here, since a fiber
	// that runs to completion instead of yielding again has no swapcontext
	// call of its own to hang a restore off of, so the resumer has to
	// handle it unconditionally either way.
	e.emitInstr(fmt.Sprintf("call i32 @swapcontext(ptr %s, ptr @__kml_main_ctx)", ctxPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", readLoopL))

	// accumulateL: nReg new bytes arrived — extend totalRead and keep the
	// buffer NUL-terminated at the new end, then check completeness.
	e.emitLabel(accumulateL)
	trOld := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", trOld, totalReadA))
	trNew := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", trNew, trOld, nReg))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", trNew, totalReadA))
	bufForTerm := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bufForTerm, bufPtrA))
	termPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", termPtr, bufForTerm, trNew))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", termPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", checkCompleteL))

	// checkCompleteL: have we already found the header terminator? If not,
	// look for it now; if so, skip straight to the body-length check.
	e.emitLabel(checkCompleteL)
	hp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", hp, headersParsedA))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hp, checkBodyCompleteL, findHeaderEndL))

	e.emitLabel(findHeaderEndL)
	bufForFind := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bufForFind, bufPtrA))
	blankLine := e.internString("\r\n\r\n")
	foundBlank := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @strstr(ptr %s, ptr %s)", foundBlank, bufForFind, blankLine))
	blankNotFound := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", blankNotFound, foundBlank))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", blankNotFound, readLoopL, gotHeaderEndL))

	// gotHeaderEndL (reached exactly once per request): record where the
	// header block ends, parse every header line (skipping the request
	// line itself — found via the first, always-present "\r\n"), and pull
	// out Content-Length if present.
	e.emitLabel(gotHeaderEndL)
	foundInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", foundInt, foundBlank))
	bufInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", bufInt, bufForFind))
	headerEndVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", headerEndVal, foundInt, bufInt))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", headerEndVal, headerEndA))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", foundBlank))
	crlf := e.internString("\r\n")
	reqLineEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @strstr(ptr %s, ptr %s)", reqLineEnd, bufForFind, crlf))
	headerBlockStart := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 2", headerBlockStart, reqLineEnd))
	newMap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", newMap))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newMap, headersMapA))
	e.emitInstr(fmt.Sprintf("call void @__kml_http_parse_headers(ptr %s, ptr %s)", headerBlockStart, newMap))
	clKey := e.internString("content-length")
	hasCL := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", hasCL, newMap, clKey))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasCL, haveCLL, noCLL))

	e.emitLabel(haveCLL)
	clInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", clInt, newMap, clKey))
	clStr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", clStr, clInt))
	clParsed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @atoll(ptr %s)", clParsed, clStr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeCLL))

	e.emitLabel(noCLL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeCLL))

	e.emitLabel(mergeCLL)
	clFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi i64 [ %s, %%%s ], [ 0, %%%s ]", clFinal, clParsed, haveCLL, noCLL))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", clFinal, contentLenA))
	e.emitInstr(fmt.Sprintf("store i1 1, ptr %s, align 1", headersParsedA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", checkBodyCompleteL))

	// checkBodyCompleteL: do we have Content-Length bytes past the header
	// block yet? If not, go read more; if so, the request is fully
	// buffered and parsing can proceed.
	e.emitLabel(checkBodyCompleteL)
	headerEndNow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", headerEndNow, headerEndA))
	contentLenNow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", contentLenNow, contentLenA))
	trNow1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", trNow1, totalReadA))
	bodyStart0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 4", bodyStart0, headerEndNow))
	haveBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", haveBytes, trNow1, bodyStart0))
	bodyComplete := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, %s", bodyComplete, haveBytes, contentLenNow))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bodyComplete, parseL, readLoopL))

	// parseL: the request is now fully buffered — parse the request line,
	// split the query string out of the path, load the already-parsed
	// headers map, extract the body, and dispatch to the handler.
	e.emitLabel(parseL)
	bufFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bufFinal, bufPtrA))
	e.ensureSscanf()
	methodPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", methodPtr))
	pathPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 2048)", pathPtr))
	scanFmt := e.internString("%15s %2047s")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sscanf(ptr %s, ptr %s, ptr %s, ptr %s)", bufFinal, scanFmt, methodPtr, pathPtr))

	qMark := e.internString("?")
	qSplit := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call {ptr, ptr} @__kml_split_first(ptr %s, ptr %s)", qSplit, pathPtr, qMark))
	pathOnly := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, ptr} %s, 0", pathOnly, qSplit))
	queryRaw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, ptr} %s, 1", queryRaw, qSplit))
	hasQuery := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasQuery, queryRaw))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasQuery, haveQueryL, noQueryL))

	e.emitLabel(haveQueryL)
	qMap1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", qMap1))
	e.emitInstr(fmt.Sprintf("call void @__kml_http_parse_query(ptr %s, ptr %s)", queryRaw, qMap1))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeQueryL))

	e.emitLabel(noQueryL)
	qMap2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", qMap2))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeQueryL))

	e.emitLabel(mergeQueryL)
	queryMapFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi ptr [ %s, %%%s ], [ %s, %%%s ]", queryMapFinal, qMap1, haveQueryL, qMap2, noQueryL))

	headersMapFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", headersMapFinal, headersMapA))

	headerEndFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", headerEndFinal, headerEndA))
	contentLenFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", contentLenFinal, contentLenA))
	bodyStartFinal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 4", bodyStartFinal, headerEndFinal))
	bodySrc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", bodySrc, bufFinal, bodyStartFinal))
	bodyAlloc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", bodyAlloc, contentLenFinal))
	bodyBuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", bodyBuf, bodyAlloc))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", bodyBuf, bodySrc, contentLenFinal))
	bodyTerm := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", bodyTerm, bodyBuf, contentLenFinal))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", bodyTerm))

	reqTy := RequestType()
	reqReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", reqReg, reqTy.StructSize()))
	reqStructIR := reqTy.StructIR()
	storeReqField := func(name, ref string) {
		idx, fieldTy, _ := reqTy.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, reqStructIR, reqReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, ref, gep, fieldTy.Align()))
	}
	storeReqField("method", methodPtr)
	storeReqField("path", pathOnly)
	storeReqField("query", queryMapFinal)
	storeReqField("headers", headersMapFinal)
	storeReqField("body", bodyBuf)
	reqVal := e.coerce(Value{Ref: reqReg, Ty: reqTy}, paramTy)

	handlerPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_listen_handler, align 8", handlerPtr))
	fpSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpSlot, handlerPtr))
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpSlot))
	epSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", epSlot, handlerPtr))
	ep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epSlot))

	callReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s (ptr, %s) %s(ptr %s, %s %s)",
		callReg, retTy.LLVMRetType(), paramTy.IR, fp, ep, paramTy.IR, reqVal.Ref))

	// An async handler's call above doesn't return until its body has fully
	// run (any internal `await` yields this same connection fiber via
	// swapcontext, transparent to this call) — callReg is then a Promise<T>
	// slot pointer, not T directly, needing one more unwrap (matching
	// emitAwait's own generic, non-fetch unwrap: load then free the slot).
	// Promise<T> and a plain object T share IR="ptr", so the call syntax
	// above is identical either way — only this extra indirection differs.
	respReg := callReg
	if isAsyncHandler {
		respReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", respReg, callReg))
		e.ensureFree()
		e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", callReg))
	}

	statusIdx, statusTy, _ := retTy.FieldIndex("status")
	statusGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", statusGep, retTy.StructIR(), respReg, statusIdx))
	statusReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", statusReg, statusTy.IR, statusGep, statusTy.Align()))
	statusVal := e.coerce(Value{Ref: statusReg, Ty: statusTy}, TypeI64)

	bodyIdx, bodyTy, _ := retTy.FieldIndex("body")
	bodyGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", bodyGep, retTy.StructIR(), respReg, bodyIdx))
	bodyReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", bodyReg, bodyTy.IR, bodyGep, bodyTy.Align()))

	// Optional response headers: absent (the common case) needs zero extra
	// branches — extraHeadersRef is just the interned empty string, so
	// __kml_http_send_response's output is byte-identical to before this
	// field existed. Present means load it, and (only then) pull in
	// ensureHTTPSerializeHeaders — matching this file's existing "only pull
	// in what's used" discipline.
	var extraHeadersRef string
	if hIdx, hTy, ok := retTy.FieldIndex("headers"); ok {
		hGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", hGep, retTy.StructIR(), respReg, hIdx))
		hReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", hReg, hTy.IR, hGep, hTy.Align()))
		hNotNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hNotNull, hReg))

		haveRespHeadersL := e.freshLabel("http.haverespheaders")
		noRespHeadersL := e.freshLabel("http.norespheaders")
		mergeRespHeadersL := e.freshLabel("http.mergerespheaders")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hNotNull, haveRespHeadersL, noRespHeadersL))

		e.emitLabel(haveRespHeadersL)
		e.ensureHTTPSerializeHeaders()
		serialized := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_http_serialize_headers(ptr %s)", serialized, hReg))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeRespHeadersL))

		e.emitLabel(noRespHeadersL)
		emptyHdrs := e.internString("")
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeRespHeadersL))

		e.emitLabel(mergeRespHeadersL)
		extraHeadersReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = phi ptr [ %s, %%%s ], [ %s, %%%s ]", extraHeadersReg, serialized, haveRespHeadersL, emptyHdrs, noRespHeadersL))
		extraHeadersRef = extraHeadersReg
	} else {
		extraHeadersRef = e.internString("")
	}

	e.emitInstr(fmt.Sprintf("call void @__kml_http_send_response(i32 %s, i64 %s, ptr %s, ptr %s)", fd32, statusVal.Ref, bodyReg, extraHeadersRef))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", fdPtr))
	e.emitTerminator("ret void")

	e.emitLabel(noReqL)
	e.emitInstr(fmt.Sprintf("call i32 @close(i32 %s)", fd32))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", fdPtr))
	e.emitTerminator("ret void")

	e.functions.WriteString("\ndefine void @__kml_http_dispatch() {\nentry:\n")
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.labelCtr = savedLabelCtr
	e.scopes = savedScopes
	e.currentRetType = savedRetType
	e.blockDone = savedBlockDone
	return nil
}
