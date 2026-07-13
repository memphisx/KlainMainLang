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
// registered with the event loop (called each time the listening socket is
// ready): accepts one connection, parses its request line, builds a Request
// object, calls the handler closure (read from @__kml_listen_handler),
// extracts status/body off whatever it returns (paramTy/retTy are captured
// from the call site — this is why the dispatcher is built per call site
// rather than being one fully generic hand-written IR helper like the timer
// trampoline), and writes the response. A fixed name is safe: only one
// http.listen call site is ever reachable in V1, since the first one never
// returns (any second call in the same program is dead code).
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

	lfd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr @__kml_listen_fd, align 4", lfd))
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { i32, ptr, ptr } @__kml_http_accept_and_read(i32 %s)", raw, lfd))
	connfd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i32, ptr, ptr } %s, 0", connfd, raw))
	methodPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i32, ptr, ptr } %s, 1", methodPtr, raw))
	pathPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i32, ptr, ptr } %s, 2", pathPtr, raw))

	okReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i32 %s, 0", okReg, connfd))
	handleL := e.freshLabel("http.handle")
	skipL := e.freshLabel("http.skip")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", okReg, handleL, skipL))

	e.emitLabel(handleL)
	reqTy := RequestType()
	e.ensureMalloc()
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

	e.emitInstr(fmt.Sprintf("call void @__kml_http_send_response(i32 %s, i64 %s, ptr %s)", connfd, statusVal.Ref, bodyReg))
	e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))

	e.emitLabel(skipL)
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
