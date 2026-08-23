// emit_worker.go — codegen for Worker (worker_threads, TDD-00098): worker
// entry-function emission, `new Worker(...)`, the parent-side
// postMessage/on/terminate surface, and the worker-side parentPort/
// workerData surface. The runtime half lives in runtime_worker.go.
//
// Channel typing is fully static (values carry no runtime type tags): the
// worker file declares its inbound message type via the annotated handler
// `parentPort.on('message', (msg: In) => ...)` and its workerData type via
// an annotated `const cfg: Cfg = workerData`; its outbound type is inferred
// from its `parentPort.postMessage(...)` sites. Worker entry functions are
// emitted BEFORE main, so every parent-side use is checked against the
// recorded types.
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// workerEntryInfo is one worker module's compile-time channel record, keyed
// by canonical file path in e.workerEntries.
type workerEntryInfo struct {
	Symbol string // entry function name, __kml_worker_entry_<i>
	InTy   Type   // parent → worker message type (worker's .on annotation)
	InSet  bool
	OutTy  Type // worker → parent message type (worker's postMessage args)
	OutSet bool
	DataTy  Type // workerData type (worker's annotated decl)
	DataSet bool
}

// workerPayloadOK validates a postMessage/workerData payload type: the
// structured-clone rules, minus the types clone would pass but the two-word
// pipe envelope can't carry.
func workerPayloadOK(ty Type, pos ast.Pos, what string) error {
	if kind := structuredCloneUnsupportedKind(ty); kind != "" {
		return fmt.Errorf("%d:%d: %s cannot be %s — only structured-clone-safe values (numbers, strings, booleans, plain arrays and objects) cross a worker boundary", pos.Line, pos.Col, what, kind)
	}
	return nil
}

// workerTypesCompatible is the loose static check between a payload's type
// and the declared channel type — same encoded envelope shape.
func workerTypesCompatible(a, b Type) bool {
	if a.IsArray != b.IsArray {
		return false
	}
	if a.IsArray {
		return a.ElemType != nil && b.ElemType != nil && a.ElemType.IR == b.ElemType.IR
	}
	return a.IR == b.IR
}

// emitWorkerModules emits every worker module's top-level statements into a
// dedicated `define void @__kml_worker_entry_<i>()` using the same
// builder-swap seam emitClosureFunc uses. Called before Pass 3 (main), so
// the channel types recorded here gate every parent-side use.
func (e *Emitter) emitWorkerModules(prog *ast.Program) error {
	if len(prog.WorkerModules) == 0 {
		return nil
	}
	e.ensureWorkerRuntime()
	if e.workerEntries == nil {
		e.workerEntries = map[string]*workerEntryInfo{}
	}
	// Pre-register every module first: a worker's entry symbol/type slots
	// must exist before any module body is emitted.
	for i, wm := range prog.WorkerModules {
		info := &workerEntryInfo{Symbol: fmt.Sprintf("__kml_worker_entry_%d", i)}
		// Pre-scan the annotated `const cfg: Cfg = workerData` declaration —
		// the annotation must be known before the statement itself emits.
		for _, stmt := range wm.Body {
			decls := []*ast.VarDeclaration{}
			switch s := stmt.(type) {
			case *ast.VarDeclaration:
				decls = append(decls, s)
			case *ast.VarDeclarationList:
				decls = append(decls, s.Decls...)
			}
			for _, d := range decls {
				if id, ok := d.Init.(*ast.Identifier); ok && id.Name == "workerData" && d.TypeAnnot != nil {
					info.DataTy = e.resolveType(d.TypeAnnot)
					info.DataSet = true
				}
			}
		}
		e.workerEntries[wm.Path] = info
	}

	for _, wm := range prog.WorkerModules {
		info := e.workerEntries[wm.Path]
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
		e.currentRetType = TypeVoid
		e.blockDone = false
		e.pushScope()
		e.currentWorkerMod = wm.Path

		var emitErr error
		for _, stmt := range wm.Body {
			if emitErr = e.emitStmt(stmt); emitErr != nil {
				break
			}
		}
		if emitErr == nil {
			e.emitTerminator("ret void")
			e.functions.WriteString(fmt.Sprintf("\ndefine void @%s() {\nentry:\n", info.Symbol))
			e.functions.WriteString(e.allocas.String())
			e.functions.WriteString(e.body.String())
			e.functions.WriteString("}\n")
		}

		e.currentWorkerMod = ""
		e.allocas = savedAllocas
		e.body = savedBody
		e.regCtr = savedRegCtr
		e.labelCtr = savedLabelCtr
		e.scopes = savedScopes
		e.currentRetType = savedRetType
		e.blockDone = savedBlockDone
		if emitErr != nil {
			return fmt.Errorf("worker module %s: %w", wm.Path, emitErr)
		}
	}
	return nil
}

// emitNewWorkerExpression emits `new Worker('./w.ts', { workerData: v })`:
// clone + encode workerData, then spawn the pthread against the module's
// entry function. Parent-side only.
func (e *Emitter) emitNewWorkerExpression(ex *ast.NewWorkerExpression) (Value, error) {
	pos := ex.GetPos()
	if e.currentWorkerMod != "" {
		return Value{}, fmt.Errorf("%d:%d: a worker module cannot spawn workers of its own", pos.Line, pos.Col)
	}
	info := e.workerEntries[ex.ResolvedPath]
	if info == nil {
		return Value{}, fmt.Errorf("%d:%d: internal error: worker module '%s' was not registered", pos.Line, pos.Col, ex.Path)
	}
	e.ensureWorkerRuntime()

	w0, w1 := "0", "0"
	if ex.WorkerData != nil {
		if !info.DataSet {
			return Value{}, fmt.Errorf("%d:%d: worker '%s' never reads workerData — add an annotated `const cfg: T = workerData` to the worker module", pos.Line, pos.Col, ex.Path)
		}
		val, err := e.emitExpr(ex.WorkerData)
		if err != nil {
			return Value{}, err
		}
		if err := workerPayloadOK(val.Ty, pos, "workerData"); err != nil {
			return Value{}, err
		}
		if !workerTypesCompatible(val.Ty, info.DataTy) {
			return Value{}, fmt.Errorf("%d:%d: workerData type does not match the worker's declared type", pos.Line, pos.Col)
		}
		val = e.coerce(val, info.DataTy)
		cloned, err := e.emitDeepClone(val, info.DataTy, pos)
		if err != nil {
			return Value{}, err
		}
		w0, w1 = e.encodeWorkerPayload(cloned, info.DataTy)
	} else if info.DataSet {
		return Value{}, fmt.Errorf("%d:%d: worker '%s' declares workerData but none was passed — use new Worker(path, { workerData: ... })", pos.Line, pos.Col, ex.Path)
	}

	ctrl := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_worker_spawn(ptr @%s, i64 %s, i64 %s)", ctrl, info.Symbol, w0, w1))
	return Value{Ref: ctrl, Ty: WorkerType(ex.ResolvedPath)}, nil
}

// encodeWorkerPayload flattens a (cloned) payload value into the envelope's
// two i64 words. Strings/objects travel as their (freshly cloned) pointer;
// arrays as (ptr, len).
func (e *Emitter) encodeWorkerPayload(val Value, ty Type) (string, string) {
	if ty.IsArray {
		p := e.freshReg()
		l := e.freshReg()
		pi := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", p, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", l, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", pi, p))
		return pi, l
	}
	switch ty.IR {
	case "ptr":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", r, val.Ref))
		return r, "0"
	case "double":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", r, val.Ref))
		return r, "0"
	case "float":
		d := e.freshReg()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fpext float %s to double", d, val.Ref))
		e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", r, d))
		return r, "0"
	case "i64":
		return val.Ref, "0"
	default: // i1/i8/i16/i32
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext %s %s to i64", r, ty.IR, val.Ref))
		return r, "0"
	}
}

// decodeWorkerPayload is encodeWorkerPayload's inverse, rebuilding a typed
// Value from the two envelope words (register names).
func (e *Emitter) decodeWorkerPayload(w0, w1 string, ty Type) Value {
	if ty.IsArray {
		p := e.freshReg()
		a0 := e.freshReg()
		a1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", p, w0))
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", a0, p))
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", a1, a0, w1))
		return Value{Ref: a1, Ty: ty}
	}
	switch ty.IR {
	case "ptr":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", r, w0))
		return Value{Ref: r, Ty: ty}
	case "double":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", r, w0))
		return Value{Ref: r, Ty: ty}
	case "float":
		d := e.freshReg()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", d, w0))
		e.emitInstr(fmt.Sprintf("%s = fptrunc double %s to float", r, d))
		return Value{Ref: r, Ty: ty}
	case "i64":
		return Value{Ref: w0, Ty: ty}
	default:
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to %s", r, w0, ty.IR))
		return Value{Ref: r, Ty: ty}
	}
}

// emitWorkerListenerAdapter synthesizes the uniform-signature dispatch shim
// the runtime calls for a delivered envelope — void(ptr env, i64 w0, i64 w1)
// where env is the user listener's own closure header — and returns the
// heap adapter closure header to store in the control block. wrapField != ""
// is the browser-events variant (TDD-00098 stage 6): the decoded payload is
// wrapped in a one-field heap object ({ data: T } for onmessage,
// { message: string } for onerror) before invoking the listener, so the
// handler sees `e.data`/`e.message` like real browser events.
func (e *Emitter) emitWorkerListenerAdapterWrapped(userClosure Value, payloadTy Type, wrapField string) string {
	if wrapField == "" {
		return e.emitWorkerListenerAdapter(userClosure, payloadTy)
	}
	// Wrapper closure: decode raw payload, box it into the event struct,
	// call the user handler with the event pointer.
	e.workerAdaptCtr++
	name := fmt.Sprintf("__kml_wadapt_%d", e.workerAdaptCtr)
	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedBlockDone := e.blockDone
	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.blockDone = false

	fp := e.freshReg()
	epSlot := e.freshReg()
	ep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %%uc, align 8", fp))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%uc, i32 0, i32 1", epSlot))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epSlot))
	v := e.decodeWorkerPayload("%w0", "%w1", payloadTy)
	evt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8)", evt))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", payloadTy.IR, v.Ref, evt))
	e.emitInstr(fmt.Sprintf("call void %s(ptr %s, ptr %s)", fp, ep, evt))
	e.emitTerminator("ret void")
	e.functions.WriteString(fmt.Sprintf("\ndefine void @%s(ptr %%uc, i64 %%w0, i64 %%w1) {\nentry:\n", name))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")
	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.blockDone = savedBlockDone

	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))
	fpSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", fpSlot, hdr))
	e.emitInstr(fmt.Sprintf("store ptr @%s, ptr %s, align 8", name, fpSlot))
	epSlot2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", epSlot2, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", userClosure.Ref, epSlot2))
	return hdr
}

func (e *Emitter) emitWorkerListenerAdapter(userClosure Value, payloadTy Type) string {
	e.workerAdaptCtr++
	name := fmt.Sprintf("__kml_wadapt_%d", e.workerAdaptCtr)

	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedBlockDone := e.blockDone
	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.blockDone = false

	fp := e.freshReg()
	ep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %%uc, align 8", fp))
	ep2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%uc, i32 0, i32 1", ep2))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, ep2))
	if payloadTy.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void %s(ptr %s)", fp, ep))
	} else {
		v := e.decodeWorkerPayload("%w0", "%w1", payloadTy)
		if payloadTy.IsArray {
			p := e.freshReg()
			l := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", p, v.Ref))
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", l, v.Ref))
			e.emitInstr(fmt.Sprintf("call void %s(ptr %s, ptr %s, i64 %s)", fp, ep, p, l))
		} else {
			e.emitInstr(fmt.Sprintf("call void %s(ptr %s, %s %s)", fp, ep, payloadTy.IR, v.Ref))
		}
	}
	e.emitTerminator("ret void")
	e.functions.WriteString(fmt.Sprintf("\ndefine void @%s(ptr %%uc, i64 %%w0, i64 %%w1) {\nentry:\n", name))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.blockDone = savedBlockDone

	// Build the adapter closure header {@name, userClosure}.
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))
	fpSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", fpSlot, hdr))
	e.emitInstr(fmt.Sprintf("store ptr @%s, ptr %s, align 8", name, fpSlot))
	epSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", epSlot, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", userClosure.Ref, epSlot))
	return hdr
}

// stringLiteralArg extracts a string-literal argument or errors.
func stringLiteralArg(args []ast.Expression, idx int, what string, pos ast.Pos) (string, error) {
	if idx >= len(args) {
		return "", fmt.Errorf("%d:%d: %s requires a string-literal event name", pos.Line, pos.Col, what)
	}
	lit, ok := args[idx].(*ast.StringLiteral)
	if !ok {
		return "", fmt.Errorf("%d:%d: %s requires a string-literal event name", pos.Line, pos.Col, what)
	}
	return lit.Value, nil
}

// emitWorkerMethodCall dispatches w.postMessage/on/terminate on a
// Worker-typed receiver (parent side).
func (e *Emitter) emitWorkerMethodCall(obj ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(obj)
	if err != nil {
		return Value{}, err
	}
	info := e.workerEntries[objVal.Ty.WorkerPath]
	if info == nil {
		return Value{}, fmt.Errorf("%d:%d: internal error: unknown worker module for method '%s'", pos.Line, pos.Col, method)
	}
	switch method {
	case "postMessage":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: postMessage takes exactly 1 argument", pos.Line, pos.Col)
		}
		if !info.InSet {
			return Value{}, fmt.Errorf("%d:%d: this worker never registers parentPort.on('message', (msg: T) => ...) — it cannot receive messages", pos.Line, pos.Col)
		}
		val, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if err := workerPayloadOK(val.Ty, pos, "a postMessage payload"); err != nil {
			return Value{}, err
		}
		if !workerTypesCompatible(val.Ty, info.InTy) {
			return Value{}, fmt.Errorf("%d:%d: postMessage payload type does not match the worker's declared message type", pos.Line, pos.Col)
		}
		val = e.coerce(val, info.InTy)
		cloned, err := e.emitDeepClone(val, info.InTy, pos)
		if err != nil {
			return Value{}, err
		}
		w0, w1 := e.encodeWorkerPayload(cloned, info.InTy)
		e.emitInstr(fmt.Sprintf("call void @__kml_worker_post(ptr %s, i64 0, i64 %s, i64 %s)", objVal.Ref, w0, w1))
		return Value{Ty: TypeVoid}, nil
	case "on":
		evt, err := stringLiteralArg(args, 0, "worker.on", pos)
		if err != nil {
			return Value{}, err
		}
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: worker.on takes (event, listener)", pos.Line, pos.Col)
		}
		af, ok := args[1].(*ast.ArrowFunction)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: worker.on's listener must be an arrow function literal", pos.Line, pos.Col)
		}
		var payloadTy Type
		var slot int
		switch evt {
		case "message":
			if !info.OutSet {
				return Value{}, fmt.Errorf("%d:%d: this worker never calls parentPort.postMessage — it sends no messages", pos.Line, pos.Col)
			}
			payloadTy = info.OutTy
			slot = 10
		case "exit":
			payloadTy = TypeI64
			slot = 11
		case "error":
			// The payload is the uncaught error's message string (not an
			// Error object — see the status page caveat).
			payloadTy = TypePtr
			slot = 12
		default:
			return Value{}, fmt.Errorf("%d:%d: worker.on supports 'message', 'error' and 'exit' (got '%s')", pos.Line, pos.Col, evt)
		}
		hints := []Type{payloadTy}
		if len(af.Params) == 0 {
			hints = nil
			payloadTy = Type{IR: "void"}
		}
		clos, err := e.emitArrowFunctionWithHints(af, hints)
		if err != nil {
			return Value{}, err
		}
		adapter := e.emitWorkerListenerAdapter(clos, payloadTy)
		e.emitInstr(fmt.Sprintf("call void @__kml_worker_set_cb(ptr %s, i64 %d, ptr %s)", objVal.Ref, slot, adapter))
		return Value{Ty: TypeVoid}, nil
	case "terminate":
		e.emitInstr(fmt.Sprintf("call void @__kml_worker_post(ptr %s, i64 2, i64 0, i64 0)", objVal.Ref))
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown Worker method '%s' (postMessage/on/terminate)", pos.Line, pos.Col, method)
}

// emitParentPortCall dispatches parentPort.on/postMessage inside a worker
// module.
func (e *Emitter) emitParentPortCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if e.currentWorkerMod == "" {
		return Value{}, fmt.Errorf("%d:%d: parentPort is only available inside a worker module (a file loaded via new Worker(...))", pos.Line, pos.Col)
	}
	info := e.workerEntries[e.currentWorkerMod]
	e.ensureWorkerRuntime()
	self := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_worker_self, align 8", self))
	switch method {
	case "postMessage":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: postMessage takes exactly 1 argument", pos.Line, pos.Col)
		}
		val, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if err := workerPayloadOK(val.Ty, pos, "a postMessage payload"); err != nil {
			return Value{}, err
		}
		if info.OutSet {
			if !workerTypesCompatible(val.Ty, info.OutTy) {
				return Value{}, fmt.Errorf("%d:%d: parentPort.postMessage payload type differs from an earlier postMessage in this worker — one message type per direction", pos.Line, pos.Col)
			}
			val = e.coerce(val, info.OutTy)
		} else {
			info.OutTy = val.Ty
			info.OutSet = true
		}
		cloned, err := e.emitDeepClone(val, info.OutTy, pos)
		if err != nil {
			return Value{}, err
		}
		w0, w1 := e.encodeWorkerPayload(cloned, info.OutTy)
		e.emitInstr(fmt.Sprintf("call void @__kml_worker_post(ptr %s, i64 0, i64 %s, i64 %s)", self, w0, w1))
		return Value{Ty: TypeVoid}, nil
	case "on":
		evt, err := stringLiteralArg(args, 0, "parentPort.on", pos)
		if err != nil {
			return Value{}, err
		}
		if evt != "message" {
			return Value{}, fmt.Errorf("%d:%d: parentPort.on supports only 'message'", pos.Line, pos.Col)
		}
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: parentPort.on takes (event, listener)", pos.Line, pos.Col)
		}
		af, ok := args[1].(*ast.ArrowFunction)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: parentPort.on's listener must be an arrow function literal", pos.Line, pos.Col)
		}
		if len(af.Params) != 1 || af.Params[0].Type == nil {
			return Value{}, fmt.Errorf("%d:%d: parentPort.on('message', (msg: T) => ...) requires an annotated message parameter — it declares this worker's inbound message type", pos.Line, pos.Col)
		}
		inTy := e.resolveType(af.Params[0].Type)
		if info.InSet && !workerTypesCompatible(inTy, info.InTy) {
			return Value{}, fmt.Errorf("%d:%d: this worker already declared a different inbound message type", pos.Line, pos.Col)
		}
		info.InTy = inTy
		info.InSet = true
		clos, err := e.emitArrowFunctionWithHints(af, []Type{inTy})
		if err != nil {
			return Value{}, err
		}
		adapter := e.emitWorkerListenerAdapter(clos, inTy)
		e.emitInstr(fmt.Sprintf("call void @__kml_worker_set_cb(ptr %s, i64 13, ptr %s)", self, adapter))
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown parentPort method '%s' (postMessage/on)", pos.Line, pos.Col, method)
}

// emitWorkerDataRead reads + decodes workerData inside a worker module.
func (e *Emitter) emitWorkerDataRead(pos ast.Pos) (Value, error) {
	if e.currentWorkerMod == "" {
		return Value{}, fmt.Errorf("%d:%d: workerData is only available inside a worker module", pos.Line, pos.Col)
	}
	info := e.workerEntries[e.currentWorkerMod]
	if !info.DataSet {
		return Value{}, fmt.Errorf("%d:%d: annotate the workerData read (`const cfg: T = workerData`) — it declares this worker's workerData type", pos.Line, pos.Col)
	}
	self := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_worker_self, align 8", self))
	w0p := e.freshReg()
	w1p := e.freshReg()
	w0 := e.freshReg()
	w1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 8", w0p, workerCtrlIR, self))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 9", w1p, workerCtrlIR, self))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w0, w0p))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w1, w1p))
	return e.decodeWorkerPayload(w0, w1, info.DataTy), nil
}

// workerBrowserEventType is the one-field browser event object a stage-6
// handler receives: { data: T } for message events, { message: string } for
// error events.
func workerBrowserEventType(field string, payloadTy Type) Type {
	return ObjectType([]Field{{Name: field, Ty: payloadTy}})
}

// emitWorkerHandlerAssign is the parent-side browser surface (TDD-00098
// stage 6): `w.onmessage = (e) => ... e.data ...` and `w.onerror = (e) =>
// ... e.message ...` on a Worker-typed receiver.
func (e *Emitter) emitWorkerHandlerAssign(obj ast.Expression, prop string, rhs ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(obj)
	if err != nil {
		return Value{}, err
	}
	info := e.workerEntries[objVal.Ty.WorkerPath]
	if info == nil {
		return Value{}, fmt.Errorf("%d:%d: internal error: unknown worker module for '%s'", pos.Line, pos.Col, prop)
	}
	af, ok := rhs.(*ast.ArrowFunction)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: %s must be assigned an arrow function literal", pos.Line, pos.Col, prop)
	}
	var payloadTy Type
	var field string
	var slot int
	switch prop {
	case "onmessage":
		if !info.OutSet {
			return Value{}, fmt.Errorf("%d:%d: this worker never posts messages", pos.Line, pos.Col)
		}
		if info.OutTy.IsArray {
			return Value{}, fmt.Errorf("%d:%d: an array message payload is not supported through onmessage — use worker.on('message', ...) instead", pos.Line, pos.Col)
		}
		payloadTy, field, slot = info.OutTy, "data", 10
	case "onerror":
		payloadTy, field, slot = TypePtr, "message", 12
	default:
		return Value{}, fmt.Errorf("%d:%d: unknown Worker handler property '%s' (onmessage/onerror)", pos.Line, pos.Col, prop)
	}
	evtTy := workerBrowserEventType(field, payloadTy)
	clos, err := e.emitArrowFunctionWithHints(af, []Type{evtTy})
	if err != nil {
		return Value{}, err
	}
	adapter := e.emitWorkerListenerAdapterWrapped(clos, payloadTy, field)
	e.emitInstr(fmt.Sprintf("call void @__kml_worker_set_cb(ptr %s, i64 %d, ptr %s)", objVal.Ref, slot, adapter))
	return Value{Ty: TypeVoid}, nil
}

// emitWorkerSideOnMessageAssign is the worker-side browser surface: a bare
// `onmessage = (e: { data: T }) => ...` (or `self.onmessage = ...`) at the
// worker module's top level. The parameter annotation declares the worker's
// inbound message type, exactly like parentPort.on's annotation does.
func (e *Emitter) emitWorkerSideOnMessageAssign(rhs ast.Expression, pos ast.Pos) (Value, error) {
	if e.currentWorkerMod == "" {
		return Value{}, fmt.Errorf("%d:%d: onmessage is only assignable inside a worker module", pos.Line, pos.Col)
	}
	info := e.workerEntries[e.currentWorkerMod]
	af, ok := rhs.(*ast.ArrowFunction)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: onmessage must be assigned an arrow function literal", pos.Line, pos.Col)
	}
	if len(af.Params) != 1 || af.Params[0].Type == nil {
		return Value{}, fmt.Errorf("%d:%d: onmessage = (e: { data: T }) => ... requires an annotated event parameter — it declares this worker's inbound message type", pos.Line, pos.Col)
	}
	evtTy := e.resolveType(af.Params[0].Type)
	idx, dataTy, hasData := evtTy.FieldIndex("data")
	if !hasData || idx != 0 || len(evtTy.VisibleFields()) != 1 {
		return Value{}, fmt.Errorf("%d:%d: onmessage's event parameter must be annotated as { data: T }", pos.Line, pos.Col)
	}
	if dataTy.IsArray {
		return Value{}, fmt.Errorf("%d:%d: an array message payload is not supported through onmessage — use parentPort.on('message', ...) instead", pos.Line, pos.Col)
	}
	if info.InSet && !workerTypesCompatible(dataTy, info.InTy) {
		return Value{}, fmt.Errorf("%d:%d: this worker already declared a different inbound message type", pos.Line, pos.Col)
	}
	info.InTy = dataTy
	info.InSet = true
	e.ensureWorkerRuntime()
	clos, err := e.emitArrowFunctionWithHints(af, []Type{evtTy})
	if err != nil {
		return Value{}, err
	}
	adapter := e.emitWorkerListenerAdapterWrapped(clos, dataTy, "data")
	self := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_worker_self, align 8", self))
	e.emitInstr(fmt.Sprintf("call void @__kml_worker_set_cb(ptr %s, i64 13, ptr %s)", self, adapter))
	return Value{Ty: TypeVoid}, nil
}
