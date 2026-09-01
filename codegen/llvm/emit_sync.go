package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emit_sync.go — codegen for the klain:sync goroutine runtime (TDD-00143).
//
// Surface:
//   import { go, Channel } from 'klain:sync'
//   go(() => { ... })                 // spawn a goroutine
//   const ch = new Channel<T>(cap)    // CSP channel (parse-time constructor)
//   ch.send(v)   ch.receive()  ch.close()
//
// `go` is a function-member call (sync__kml_builtin.go); Channel binds as
// identity, so its methods dispatch on inferExprType(...).IsChannel. Channel
// elements are a fixed 8-byte i64 slot — send bitcasts T→i64, receive bitcasts
// i64→T (see channelSlotIn/channelSlotOut).

// selCaseInfo is one classified select case (emitSelect's internal model).
type selCaseInfo struct {
	dir      int            // 0 = receive, 1 = send
	chanExpr ast.Expression // the channel
	elemTy   Type           // T (from the channel's ElemType)
	sendArg  ast.Expression // send case: the value expression
	handler  ast.Expression // the case's handler closure
}

// emitSelect lowers `select(...cases)` (TDD-00143 Stage 3). Each argument must
// be `ch.recvCase(v => ...)`, `ch.sendCase(value, () => ...)`, or
// `defaultCase(() => ...)`. It builds a `[n x ks_selcase]` array, calls
// klainsync_select to pick a ready case (or block until one fires), then
// dispatches to the chosen case's handler — passing the received value to a
// recv handler.
func (e *Emitter) emitSelect(args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureSyncRuntime()
	var cases []selCaseInfo
	var defaultHandler ast.Expression
	for _, arg := range args {
		call, ok := arg.(*ast.CallExpression)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: each select argument must be ch.recvCase(...), ch.sendCase(...), or defaultCase(...)", pos.Line, pos.Col)
		}
		mem, ok := call.Callee.(*ast.MemberExpression)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: invalid select case", pos.Line, pos.Col)
		}
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "sync__kml_builtin" && mem.Property == "defaultCase" {
			if len(call.Args) != 1 {
				return Value{}, fmt.Errorf("%d:%d: defaultCase(handler) takes one argument", pos.Line, pos.Col)
			}
			if defaultHandler != nil {
				return Value{}, fmt.Errorf("%d:%d: select has more than one defaultCase", pos.Line, pos.Col)
			}
			defaultHandler = call.Args[0]
			continue
		}
		objTy := e.inferExprType(mem.Object)
		if !objTy.IsChannel || objTy.ElemType == nil {
			return Value{}, fmt.Errorf("%d:%d: a select case must be recvCase/sendCase on a Channel", pos.Line, pos.Col)
		}
		switch mem.Property {
		case "recvCase":
			if len(call.Args) != 1 {
				return Value{}, fmt.Errorf("%d:%d: recvCase(handler) takes one argument", pos.Line, pos.Col)
			}
			cases = append(cases, selCaseInfo{dir: 0, chanExpr: mem.Object, elemTy: *objTy.ElemType, handler: call.Args[0]})
		case "sendCase":
			if len(call.Args) != 2 {
				return Value{}, fmt.Errorf("%d:%d: sendCase(value, handler) takes two arguments", pos.Line, pos.Col)
			}
			cases = append(cases, selCaseInfo{dir: 1, chanExpr: mem.Object, elemTy: *objTy.ElemType, sendArg: call.Args[0], handler: call.Args[1]})
		default:
			return Value{}, fmt.Errorf("%d:%d: a select case must be recvCase or sendCase", pos.Line, pos.Col)
		}
	}
	n := len(cases)
	if n == 0 && defaultHandler == nil {
		return Value{}, fmt.Errorf("%d:%d: select needs at least one case", pos.Line, pos.Col)
	}
	hasDefault := int32(0)
	if defaultHandler != nil {
		hasDefault = 1
	}

	// ks_selcase = { ptr ch, i64 sendval, i64 recvval, i32 dir, i32 recv_ok } — 32 bytes.
	const caseIR = "{ ptr, i64, i64, i32, i32 }"
	arrPtr := "null"
	if n > 0 {
		arr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca [%d x %s], align 8", arr, n, caseIR))
		arrPtr = arr
		for i, c := range cases {
			// field 0: channel ptr
			chVal, err := e.emitExpr(c.chanExpr)
			if err != nil {
				return Value{}, err
			}
			base := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr [%d x %s], ptr %s, i64 0, i64 %d", base, n, caseIR, arr, i))
			f0 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", f0, caseIR, base))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", chVal.Ref, f0))
			// field 1: sendval (0 for recv)
			sendReg := "0"
			if c.dir == 1 {
				sv, err := e.emitExprWithObjectHint(c.sendArg, c.elemTy)
				if err != nil {
					return Value{}, err
				}
				sendReg = e.chanSlotFromValue(e.coerce(sv, c.elemTy), c.elemTy)
			}
			f1 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", f1, caseIR, base))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sendReg, f1))
			// field 2: recvval = 0
			f2 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", f2, caseIR, base))
			e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", f2))
			// field 3: dir
			f3 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 3", f3, caseIR, base))
			e.emitInstr(fmt.Sprintf("store i32 %d, ptr %s, align 4", c.dir, f3))
			// field 4: recv_ok = 0
			f4 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 4", f4, caseIR, base))
			e.emitInstr(fmt.Sprintf("store i32 0, ptr %s, align 4", f4))
		}
	}

	idx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @klainsync_select(ptr %s, i32 %d, i32 %d)", idx, arrPtr, n, hasDefault))

	endL := e.freshLabel("select.end")
	// Dispatch cascade: for each case i, if idx==i run its handler; the final
	// fallthrough (idx == -1) runs the default handler (or nothing).
	for i, c := range cases {
		hitL := e.freshLabel("select.case")
		nextL := e.freshLabel("select.next")
		cmp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, %d", cmp, idx, i))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cmp, hitL, nextL))
		e.emitLabel(hitL)
		if err := e.emitSelectHandler(c, arrPtr, caseIR, n, i); err != nil {
			return Value{}, err
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", endL))
		e.emitLabel(nextL)
	}
	// fallthrough: default (idx == -1) or no-op
	if defaultHandler != nil {
		if err := e.emitVoidHandlerCall(defaultHandler); err != nil {
			return Value{}, err
		}
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", endL))
	e.emitLabel(endL)
	return Value{Ty: TypeVoid}, nil
}

// emitSelectHandler invokes one select case's handler. A recv handler receives
// the case's value (loaded from the array, bitcast to T); a send handler takes
// no argument.
func (e *Emitter) emitSelectHandler(c selCaseInfo, arrPtr, caseIR string, n, i int) error {
	hv, err := e.emitExpr(c.handler)
	if err != nil {
		return err
	}
	if !hv.Ty.IsFunc {
		return fmt.Errorf("select case handler must be a function")
	}
	fp := e.freshReg()
	fpSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpSlot, hv.Ref))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpSlot))
	ep := e.freshReg()
	epSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", epSlot, hv.Ref))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epSlot))
	if c.dir == 0 {
		// load recvval (field 2), bitcast to T, pass to handler(env, v)
		base := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr [%d x %s], ptr %s, i64 0, i64 %d", base, n, caseIR, arrPtr, i))
		f2 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", f2, caseIR, base))
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", raw, f2))
		val := e.chanSlotToValue(raw, c.elemTy)
		e.emitInstr(fmt.Sprintf("call void %s(ptr %s, %s %s)", fp, ep, c.elemTy.IR, val.Ref))
	} else {
		e.emitInstr(fmt.Sprintf("call void %s(ptr %s)", fp, ep))
	}
	return nil
}

// emitVoidHandlerCall invokes a no-argument handler closure (default case).
func (e *Emitter) emitVoidHandlerCall(handler ast.Expression) error {
	hv, err := e.emitExpr(handler)
	if err != nil {
		return err
	}
	if !hv.Ty.IsFunc {
		return fmt.Errorf("defaultCase handler must be a function")
	}
	fp := e.freshReg()
	fpSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpSlot, hv.Ref))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpSlot))
	ep := e.freshReg()
	epSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", epSlot, hv.Ref))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epSlot))
	e.emitInstr(fmt.Sprintf("call void %s(ptr %s)", fp, ep))
	return nil
}

// emitForOfChannel emits `for (const v of ch) { ... }` — receive until the
// channel is closed and drained (TDD-00143 Stage 3). The channel is evaluated
// once; each iteration blocks in klainsync_chan_recv, and a zero ok flag (a
// drained closed channel) ends the loop.
func (e *Emitter) emitForOfChannel(s *ast.ForOfStatement, objTy Type, condL, bodyL, incL, endL string) error {
	if s.VarName == "" {
		return fmt.Errorf("%d:%d: ranging a Channel requires a simple loop variable", s.GetPos().Line, s.GetPos().Col)
	}
	e.ensureSyncRuntime()
	elemTy := TypeI64
	if objTy.ElemType != nil {
		elemTy = *objTy.ElemType
	}
	chVal, err := e.emitExpr(s.Iterable)
	if err != nil {
		return err
	}
	okSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", okSlot))
	varPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", varPtr, elemTy.IR, elemTy.Align()))
	e.define(s.VarName, Symbol{Ptr: varPtr, Ty: elemTy})

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	e.emitSafepoint() // loop back-edge preempt check
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @klainsync_chan_recv(ptr %s, ptr %s)", raw, chVal.Ref, okSlot))
	okReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", okReg, okSlot))
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", done, okReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, endL, bodyL))

	e.emitLabel(bodyL)
	val := e.chanSlotToValue(raw, elemTy)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val.Ref, varPtr, elemTy.Align()))
	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))
	e.emitLabel(incL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(endL)
	return nil
}

// emitSafepoint emits a cooperative preempt check (TDD-00143 Stage 2) — a call
// into the runtime that yields the goroutine iff sysmon has flagged it. Emitted
// at function entry and loop back-edges, but only when the program imports
// klain:sync (usesSyncProgram); a non-klain program emits nothing here. A no-op
// after a terminator (the dead-code path drops it, which is fine).
func (e *Emitter) emitSafepoint() {
	if !e.usesSyncProgram || e.blockDone {
		return
	}
	e.emitInstr("call void @klainsync_safepoint()")
}

// emitSyncModuleCall dispatches the function-member exports of klain:sync (only
// `go` today; `Channel` is a constructor handled elsewhere).
func (e *Emitter) emitSyncModuleCall(member string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch member {
	case "go":
		return e.emitGoSpawn(args, pos)
	case "select":
		return e.emitSelect(args, pos)
	case "defaultCase":
		return Value{}, fmt.Errorf("%d:%d: defaultCase(...) is only valid as an argument to select(...)", pos.Line, pos.Col)
	default:
		return Value{}, fmt.Errorf("%d:%d: klain:sync has no export %q", pos.Line, pos.Col, member)
	}
}

// emitGoSpawn lowers `go(fn)` to klainsync_go(fnptr, envptr), decomposing the
// closure header {fnptr, envptr} the goroutine body is invoked through.
func (e *Emitter) emitGoSpawn(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: go(fn) takes exactly one argument (a function)", pos.Line, pos.Col)
	}
	fnVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !fnVal.Ty.IsFunc {
		return Value{}, fmt.Errorf("%d:%d: go(fn)'s argument must be a function", pos.Line, pos.Col)
	}
	if len(fnVal.Ty.FuncParams) != 0 {
		return Value{}, fmt.Errorf("%d:%d: go(fn)'s function must take no parameters", pos.Line, pos.Col)
	}
	e.ensureSyncRuntime()

	// Decompose the closure header: header[0] = fnptr, header[1] = envptr.
	fpSlot := e.freshReg()
	fpVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpSlot, fnVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fpVal, fpSlot))
	epSlot := e.freshReg()
	epVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", epSlot, fnVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", epVal, epSlot))

	e.emitInstr(fmt.Sprintf("call void @klainsync_go(ptr %s, ptr %s)", fpVal, epVal))
	return Value{Ty: TypeVoid}, nil
}

// emitNewChannelExpression lowers `new Channel<T>(cap)` to a klainsync_chan_new
// handle. The element type T is tracked in the result Type's ElemType.
func (e *Emitter) emitNewChannelExpression(ex *ast.NewChannelExpression) (Value, error) {
	e.ensureSyncRuntime()
	msgTy := TypeI64
	if ex.TypeArg != nil {
		msgTy = e.resolveType(ex.TypeArg)
	}
	capReg := "0"
	if ex.Capacity != nil {
		capVal, err := e.emitExpr(ex.Capacity)
		if err != nil {
			return Value{}, err
		}
		capReg = e.coerce(capVal, TypeI64).Ref
	}
	ch := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @klainsync_chan_new(i64 %s)", ch, capReg))
	return Value{Ref: ch, Ty: ChannelType(msgTy)}, nil
}

// emitChannelMethod dispatches ch.send(v) / ch.receive() / ch.close(). Channel
// elements are a fixed 8-byte slot, so send bitcasts T→i64 and receive
// bitcasts i64→T (see chanSlotFromValue / chanSlotToValue).
func (e *Emitter) emitChannelMethod(obj ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureSyncRuntime()
	objVal, err := e.emitExpr(obj)
	if err != nil {
		return Value{}, err
	}
	if objVal.Ty.ElemType == nil {
		return Value{}, fmt.Errorf("%d:%d: channel has no element type", pos.Line, pos.Col)
	}
	msgTy := *objVal.Ty.ElemType

	switch method {
	case "send":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: Channel.send(v) takes exactly one argument", pos.Line, pos.Col)
		}
		val, err := e.emitExprWithObjectHint(args[0], msgTy)
		if err != nil {
			return Value{}, err
		}
		slot := e.chanSlotFromValue(e.coerce(val, msgTy), msgTy)
		e.emitInstr(fmt.Sprintf("call void @klainsync_chan_send(ptr %s, i64 %s)", objVal.Ref, slot))
		return Value{Ty: TypeVoid}, nil

	case "receive":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: Channel.receive() takes no arguments", pos.Line, pos.Col)
		}
		okSlot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", okSlot))
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @klainsync_chan_recv(ptr %s, ptr %s)", raw, objVal.Ref, okSlot))
		return e.chanSlotToValue(raw, msgTy), nil

	case "close":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: Channel.close() takes no arguments", pos.Line, pos.Col)
		}
		e.emitInstr(fmt.Sprintf("call void @klainsync_chan_close(ptr %s)", objVal.Ref))
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: Channel has no method %q", pos.Line, pos.Col, method)
}

// chanSlotFromValue widens a channel element of type ty into the runtime's
// 8-byte i64 slot, returning the SSA register holding the i64.
func (e *Emitter) chanSlotFromValue(v Value, ty Type) string {
	r := e.freshReg()
	switch ty.IR {
	case "i64":
		return v.Ref
	case "double":
		e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", r, v.Ref))
	case "ptr":
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", r, v.Ref))
	default: // i1/i8/i16/i32 — zero-extend; the receive side truncs back
		e.emitInstr(fmt.Sprintf("%s = zext %s %s to i64", r, ty.IR, v.Ref))
	}
	return r
}

// chanSlotToValue narrows the runtime's 8-byte i64 slot back to a channel
// element of type ty.
func (e *Emitter) chanSlotToValue(raw string, ty Type) Value {
	r := e.freshReg()
	switch ty.IR {
	case "i64":
		return Value{Ref: raw, Ty: ty}
	case "double":
		e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", r, raw))
	case "ptr":
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", r, raw))
	default: // i1/i8/i16/i32
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to %s", r, raw, ty.IR))
	}
	return Value{Ref: r, Ty: ty}
}
