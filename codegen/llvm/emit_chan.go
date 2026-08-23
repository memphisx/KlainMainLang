// emit_chan.go — codegen for BroadcastChannel and MessageChannel/MessagePort
// (TDD-00099). The runtime half lives in runtime_chan.go; the payload
// encode/decode and listener-adapter machinery is shared with the Worker
// channel (emit_worker.go).
//
// Typing: a BroadcastChannel carries one message type per channel NAME,
// program-wide — the first postMessage or annotated onmessage site fixes it
// and every later site is checked (the worker single-type-per-direction
// rule, keyed by name instead of path). A MessagePort's message type is
// declared at the MessageChannel construction site (`new MessageChannel<T>()`)
// and carried in the port's ElemType.
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// bcChannelInfo is one BroadcastChannel name's compile-time message-type
// record, keyed by channel name in e.bcChannels.
type bcChannelInfo struct {
	MsgTy Type
	Set   bool
}

func (e *Emitter) bcInfo(name string) *bcChannelInfo {
	if e.bcChannels == nil {
		e.bcChannels = map[string]*bcChannelInfo{}
	}
	info, ok := e.bcChannels[name]
	if !ok {
		info = &bcChannelInfo{}
		e.bcChannels[name] = info
	}
	return info
}

// emitNewBroadcastChannelExpression emits `new BroadcastChannel('name')`.
func (e *Emitter) emitNewBroadcastChannelExpression(ex *ast.NewBroadcastChannelExpression) (Value, error) {
	e.ensureChanRuntime()
	e.bcInfo(ex.Name)
	ep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_chan_new(ptr %s, i64 0)", ep, e.internString(ex.Name)))
	return Value{Ref: ep, Ty: BroadcastChannelType(ex.Name)}, nil
}

// emitNewMessageChannelExpression emits `new MessageChannel<T>()`: two
// peer-linked port endpoints boxed into a { port1, port2 } pair struct.
func (e *Emitter) emitNewMessageChannelExpression(ex *ast.NewMessageChannelExpression) (Value, error) {
	e.ensureChanRuntime()
	msgTy := TypeI64
	if ex.TypeArg != nil {
		msgTy = e.resolveType(ex.TypeArg)
	}
	pos := ex.GetPos()
	if err := workerPayloadOK(msgTy, pos, "a MessageChannel message type"); err != nil {
		return Value{}, err
	}
	p1 := e.freshReg()
	p2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_chan_new(ptr null, i64 1)", p1))
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_chan_new(ptr null, i64 1)", p2))
	pr1 := e.freshReg()
	pr2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 7", pr1, chanEpIR, p1))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", p2, pr1))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 7", pr2, chanEpIR, p2))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", p1, pr2))
	pair := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", pair))
	s2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", p1, pair))
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 1", s2, pair))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", p2, s2))
	return Value{Ref: pair, Ty: MessageChannelType(msgTy)}, nil
}

// emitMessageChannelPortRead reads `.port1`/`.port2` off a MessageChannel
// value.
func (e *Emitter) emitMessageChannelPortRead(chVal Value, prop string) (Value, error) {
	idx := 0
	if prop == "port2" {
		idx = 1
	}
	slot := e.freshReg()
	port := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", slot, chVal.Ref, idx))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", port, slot))
	return Value{Ref: port, Ty: MessagePortType(*chVal.Ty.ElemType)}, nil
}

// emitChanCloneThunk synthesizes `{i64,i64} @__kml_chanclone_N(i64, i64)` —
// decode payload, deep-clone, re-encode — which __kml_chan_post_bc calls
// once per subscriber so every receiver owns a private copy.
func (e *Emitter) emitChanCloneThunk(payloadTy Type, pos ast.Pos) (string, error) {
	e.workerAdaptCtr++
	name := fmt.Sprintf("__kml_chanclone_%d", e.workerAdaptCtr)
	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedBlockDone := e.blockDone
	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.blockDone = false

	v := e.decodeWorkerPayload("%w0", "%w1", payloadTy)
	cloned, cloneErr := e.emitDeepClone(v, payloadTy, pos)
	var c0, c1 string
	if cloneErr == nil {
		c0, c1 = e.encodeWorkerPayload(cloned, payloadTy)
		a0 := e.freshReg()
		a1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue { i64, i64 } undef, i64 %s, 0", a0, c0))
		e.emitInstr(fmt.Sprintf("%s = insertvalue { i64, i64 } %s, i64 %s, 1", a1, a0, c1))
		e.emitTerminator(fmt.Sprintf("ret { i64, i64 } %s", a1))
		e.functions.WriteString(fmt.Sprintf("\ndefine { i64, i64 } @%s(i64 %%w0, i64 %%w1) {\nentry:\n", name))
		e.functions.WriteString(e.allocas.String())
		e.functions.WriteString(e.body.String())
		e.functions.WriteString("}\n")
	}
	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.blockDone = savedBlockDone
	if cloneErr != nil {
		return "", cloneErr
	}
	return name, nil
}

// emitBroadcastChannelMethodCall dispatches bc.postMessage/close.
func (e *Emitter) emitBroadcastChannelMethodCall(obj ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(obj)
	if err != nil {
		return Value{}, err
	}
	info := e.bcInfo(objVal.Ty.BCName)
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
		if info.Set {
			if !workerTypesCompatible(val.Ty, info.MsgTy) {
				return Value{}, fmt.Errorf("%d:%d: postMessage payload type differs from channel '%s''s established message type — one message type per channel name", pos.Line, pos.Col, objVal.Ty.BCName)
			}
			val = e.coerce(val, info.MsgTy)
		} else {
			info.MsgTy = val.Ty
			info.Set = true
		}
		thunk, err := e.emitChanCloneThunk(info.MsgTy, pos)
		if err != nil {
			return Value{}, err
		}
		w0, w1 := e.encodeWorkerPayload(val, info.MsgTy)
		e.emitInstr(fmt.Sprintf("call void @__kml_chan_post_bc(ptr %s, i64 %s, i64 %s, ptr @%s)", objVal.Ref, w0, w1, thunk))
		return Value{Ty: TypeVoid}, nil
	case "close":
		e.emitInstr(fmt.Sprintf("call void @__kml_chan_close(ptr %s)", objVal.Ref))
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown BroadcastChannel method '%s' (postMessage/close)", pos.Line, pos.Col, method)
}

// emitMessagePortMethodCall dispatches port.postMessage/close.
func (e *Emitter) emitMessagePortMethodCall(obj ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(obj)
	if err != nil {
		return Value{}, err
	}
	msgTy := *objVal.Ty.ElemType
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
		if !workerTypesCompatible(val.Ty, msgTy) {
			return Value{}, fmt.Errorf("%d:%d: postMessage payload type does not match this MessagePort's declared message type", pos.Line, pos.Col)
		}
		val = e.coerce(val, msgTy)
		cloned, err := e.emitDeepClone(val, msgTy, pos)
		if err != nil {
			return Value{}, err
		}
		w0, w1 := e.encodeWorkerPayload(cloned, msgTy)
		e.emitInstr(fmt.Sprintf("call void @__kml_chan_post_port(ptr %s, i64 %s, i64 %s)", objVal.Ref, w0, w1))
		return Value{Ty: TypeVoid}, nil
	case "close":
		e.emitInstr(fmt.Sprintf("call void @__kml_chan_close(ptr %s)", objVal.Ref))
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown MessagePort method '%s' (postMessage/close)", pos.Line, pos.Col, method)
}

// emitChanOnMessageAssign is `bc.onmessage = (e) => ... e.data ...` /
// `port.onmessage = ...` — registers the adapter on the endpoint and claims
// it for this thread's event loop.
func (e *Emitter) emitChanOnMessageAssign(obj ast.Expression, rhs ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(obj)
	if err != nil {
		return Value{}, err
	}
	af, ok := rhs.(*ast.ArrowFunction)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: onmessage must be assigned an arrow function literal", pos.Line, pos.Col)
	}
	var payloadTy Type
	if objVal.Ty.IsBroadcastChannel {
		info := e.bcInfo(objVal.Ty.BCName)
		// The handler's `(e: { data: T })` annotation declares the channel
		// type when no postMessage has established it yet.
		if len(af.Params) == 1 && af.Params[0].Type != nil {
			evtTy := e.resolveType(af.Params[0].Type)
			idx, dataTy, hasData := evtTy.FieldIndex("data")
			if !hasData || idx != 0 || len(evtTy.VisibleFields()) != 1 {
				return Value{}, fmt.Errorf("%d:%d: onmessage's event parameter must be annotated as { data: T }", pos.Line, pos.Col)
			}
			if info.Set && !workerTypesCompatible(dataTy, info.MsgTy) {
				return Value{}, fmt.Errorf("%d:%d: channel '%s' already carries a different message type", pos.Line, pos.Col, objVal.Ty.BCName)
			}
			info.MsgTy = dataTy
			info.Set = true
		}
		if !info.Set {
			return Value{}, fmt.Errorf("%d:%d: channel '%s' has no established message type yet — annotate the handler (`(e: { data: T }) => ...`) or post first", pos.Line, pos.Col, objVal.Ty.BCName)
		}
		payloadTy = info.MsgTy
	} else {
		payloadTy = *objVal.Ty.ElemType
	}
	if payloadTy.IsArray {
		return Value{}, fmt.Errorf("%d:%d: an array message payload is not supported through onmessage", pos.Line, pos.Col)
	}
	evtTy := workerBrowserEventType("data", payloadTy)
	clos, err := e.emitArrowFunctionWithHints(af, []Type{evtTy})
	if err != nil {
		return Value{}, err
	}
	adapter := e.emitWorkerListenerAdapterWrapped(clos, payloadTy, "data")
	e.emitInstr(fmt.Sprintf("call void @__kml_chan_listen(ptr %s, ptr %s)", objVal.Ref, adapter))
	return Value{Ty: TypeVoid}, nil
}
