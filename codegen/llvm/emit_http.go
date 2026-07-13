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

// emitHTTPListen validates its two arguments (port: number, handler:
// (req: Request) => T where T has status/body fields), binds and listens on
// the given port, builds a dispatcher function specialized to the handler's
// own closure/return type (since reading status/body off an arbitrary
// user-declared return type needs Go-side knowledge of its field offsets,
// unlike the fully generic timer/qsort trampolines), registers that
// dispatcher with the event loop, and hands control to it.
func (e *Emitter) emitHTTPListen(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: http.listen takes exactly 2 arguments (port, handler)", pos.Line, pos.Col)
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
	if handlerVal.Ty.FuncRetType == nil || !handlerVal.Ty.FuncRetType.IsObject {
		return Value{}, fmt.Errorf("%d:%d: http.listen's handler must return an object type with status/body fields", pos.Line, pos.Col)
	}
	retTy := *handlerVal.Ty.FuncRetType
	if _, _, ok := retTy.FieldIndex("status"); !ok {
		return Value{}, fmt.Errorf("%d:%d: http.listen's handler return type must have a 'status: number' field", pos.Line, pos.Col)
	}
	if _, _, ok := retTy.FieldIndex("body"); !ok {
		return Value{}, fmt.Errorf("%d:%d: http.listen's handler return type must have a 'body: string' field", pos.Line, pos.Col)
	}
	paramTy := handlerVal.Ty.FuncParams[0]

	e.ensureHTTPRuntime()

	port32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", port32, portVal.Ref))
	listenfd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_http_bind_and_listen(i32 %s)", listenfd, port32))

	if err := e.buildHTTPDispatcher(paramTy, retTy); err != nil {
		return Value{}, err
	}

	e.emitInstr(fmt.Sprintf("store i32 %s, ptr @__kml_listen_fd, align 4", listenfd))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_listen_handler, align 8", handlerVal.Ref))
	e.emitInstr("store ptr @__kml_http_dispatch, ptr @__kml_listen_dispatch, align 8")
	e.emitInstr("call void @__kml_event_loop_run()")
	e.emitTerminator("unreachable")
	return Value{Ty: TypeVoid}, nil
}

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
// paramTy/retTy are captured from the call site — this is why the
// dispatcher is built per call site rather than being one fully generic
// hand-written IR helper like the timer trampoline: reading status/body off
// an arbitrary user-declared return type needs Go-side knowledge of its
// field offsets. A fixed name is safe: only one http.listen call site is
// ever reachable in V1, since the first one never returns (any second call
// in the same program is dead code).
func (e *Emitter) buildHTTPDispatcher(paramTy, retTy Type) error {
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

	selfIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_current_conn_idx, align 8", selfIdx))
	connData := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_conn_data, align 8", connData))
	selfSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr, ptr }, ptr %s, i64 %s", selfSlot, connData, selfIdx))
	fdPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr, ptr }, ptr %s, i32 0, i32 0", fdPtr, selfSlot))
	ctxPtrSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr, ptr }, ptr %s, i32 0, i32 1", ctxPtrSlot, selfSlot))
	e.ensureMalloc()
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8192)", buf))

	readLoopL := e.freshLabel("http.readloop")
	checkErrL := e.freshLabel("http.checkerr")
	checkEagainL := e.freshLabel("http.checkeagain")
	doYieldL := e.freshLabel("http.doyield")
	parseL := e.freshLabel("http.parse")
	noReqL := e.freshLabel("http.noreq")
	e.emitTerminator(fmt.Sprintf("br label %%%s", readLoopL))

	e.emitLabel(readLoopL)
	fd64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fd64, fdPtr))
	fd32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", fd32, fd64))
	nReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @read(i32 %s, ptr %s, i64 8191)", nReg, fd32, buf))
	gotData := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 0", gotData, nReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", gotData, parseL, checkErrL))

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
	e.emitInstr(fmt.Sprintf("call i32 @swapcontext(ptr %s, ptr @__kml_main_ctx)", ctxPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", readLoopL))

	e.emitLabel(parseL)
	termPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", termPtr, buf, nReg))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", termPtr))
	e.ensureSscanf()
	methodPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", methodPtr))
	pathPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 2048)", pathPtr))
	scanFmt := e.internString("%15s %2047s")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sscanf(ptr %s, ptr %s, ptr %s, ptr %s)", buf, scanFmt, methodPtr, pathPtr))

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
	storeReqField("path", pathPtr)
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

	respReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s (ptr, %s) %s(ptr %s, %s %s)",
		respReg, retTy.LLVMRetType(), paramTy.IR, fp, ep, paramTy.IR, reqVal.Ref))

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

	e.emitInstr(fmt.Sprintf("call void @__kml_http_send_response(i32 %s, i64 %s, ptr %s)", fd32, statusVal.Ref, bodyReg))
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
