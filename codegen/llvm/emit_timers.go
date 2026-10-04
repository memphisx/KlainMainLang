// emit_timers.go — setTimeout/clearTimeout/setInterval/clearInterval.
// Bare global functions (like fetch/btoa/parseInt), not a namespace.
//
// Needs no general-purpose (I/O-multiplexing) event loop — just a
// sleep-until-next-due queue, drained once by EmitProgram after the
// program's own top-level code finishes (see ensureTimerRuntime below
// for the full design). An active setInterval with
// nothing ever calling clearInterval on it means that drain loop never
// finishes, matching real Node's behavior: the process only exits once
// every timer has fired-and-not-repeated or been cleared.
package llvm

import (
	_ "embed"
	"fmt"

	"KlainMainLang/ast"
)

// timerCallbackPtr evaluates and validates arg as a Memory.free-free zero-
// argument, void-returning closure — the only callback shape this V1
// supports, matching the fixed `call void (ptr) %fp(ptr %ep)` trampoline
// shape __kml_timer_drain uses to call it back later.
func (e *Emitter) timerCallbackPtr(arg ast.Expression, fnName string, pos ast.Pos, extra ...ast.Expression) (string, error) {
	val, err := e.emitExpr(arg)
	if err != nil {
		return "", err
	}
	if val.Ty.IsDynamic {
		// A function held in `any` (a timer used as a value receives every
		// callback so): anything else is Node's validateFunction TypeError.
		e.emitThrowUnlessFunction(val, "callback")
		if len(extra) > 0 {
			// Called through the dynamic ABI with the arguments, gathered
			// into one any[] now.
			arr, err := e.emitExprWithObjectHint(ast.NewArrayLiteral(extra, pos), ArrayOf(TypeAny))
			if err != nil {
				return "", err
			}
			if arr.Ty.ElemType == nil || !arr.Ty.ElemType.IsDynamic {
				return "", fmt.Errorf("%d:%d: %s's arguments must be an any[]", pos.Line, pos.Col, fnName)
			}
			hdr, _ := e.arrayArgFromAggregate(arr)
			adapted, err := e.emitDynArgsAdapter(val, hdr, pos)
			if err != nil {
				return "", err
			}
			if e.programUsesALS {
				return e.wrapTimerClosureWithAsyncCtx(adapted.Ref), nil
			}
			return adapted.Ref, nil
		}
		val = e.coerce(val, FuncType(nil, TypeVoid))
	}
	// The arguments after the callback (and delay) are evaluated now, at the
	// schedule, and passed when it fires: `setTimeout(f, 10, a, b)`.
	var bound []Value
	var spreadHdr string
	if hasSpreadElem(extra) {
		// A spread among them (`setTimeout(f, 0, ...xs)`, or a timer used
		// as a value forwarding its rest): one any[] of them all.
		arr, err := e.emitExprWithObjectHint(ast.NewArrayLiteral(extra, pos), ArrayOf(TypeAny))
		if err != nil {
			return "", err
		}
		if arr.Ty.ElemType == nil || !arr.Ty.ElemType.IsDynamic {
			return "", fmt.Errorf("%d:%d: %s's spread arguments must be an any[]", pos.Line, pos.Col, fnName)
		}
		spreadHdr, _ = e.arrayArgFromAggregate(arr)
		extra = nil
	}
	for _, x := range extra {
		v, err := e.emitExpr(x)
		if err != nil {
			return "", err
		}
		boxed, err := e.emitBoxValue(v)
		if err != nil {
			return "", err
		}
		bound = append(bound, boxed)
	}
	if !val.Ty.IsFunc {
		return "", fmt.Errorf("%d:%d: %s's first argument must be a function", pos.Line, pos.Col, fnName)
	}
	if spreadHdr != "" {
		adapted, err := e.emitSpreadArgsAdapter(val, spreadHdr)
		if err != nil {
			return "", err
		}
		val = adapted
	} else if len(bound) > 0 {
		adapted, err := e.emitBoundArgsAdapter(val, bound)
		if err != nil {
			return "", err
		}
		val = adapted
	} else if len(val.Ty.FuncParams) != 0 {
		// Called with no arguments, as JavaScript calls it: each parameter
		// is undefined (or its default).
		adapted, err := e.emitNoArgAdapter(val)
		if err != nil {
			return "", err
		}
		val = adapted
	}
	// A timer ignores what its callback returns — `setTimeout(async () => { … })`
	// and `setTimeout(() => count++)` are ordinary JS. The drain calls the
	// closure as `void (ptr)`, so a value-returning callback goes through an
	// adapter with the real return type rather than a mismatched call.
	if val.Ty.FuncRetType != nil && val.Ty.FuncRetType.IR != "void" && val.Ty.FuncRetType.IR != "" {
		val = e.emitDiscardReturnAdapter(val)
	}
	// TDD-00168 Stage 3: when the program uses AsyncLocalStorage, wrap the
	// callback so it carries the async context captured at *this* schedule point
	// to its deferred fire (a setTimeout inside als.run(...) still sees the
	// store). No-op when the program has no AsyncLocalStorage.
	if e.programUsesALS {
		return e.wrapTimerClosureWithAsyncCtx(val.Ref), nil
	}
	return val.Ref, nil
}

// timerDelayArg resolves the optional delayMs argument (0 if omitted).
func (e *Emitter) timerDelayArg(args []ast.Expression, idx int) (string, error) {
	if idx >= len(args) {
		return "1", nil
	}
	val, err := e.emitExpr(args[idx])
	if err != nil {
		return "", err
	}
	val = e.coerce(val, TypeI64)
	// Node's clamp (lib/internal/timers.js): a delay outside [1, 2^31-1] —
	// including the omitted/0/negative/NaN cases — becomes 1 ms. This is what
	// makes 20,000 sequential `setTimeout(f, 0)` take ≥ 20 s there; a flat 0
	// here fired them all in a burst. setImmediate does not go through this.
	tooSmall := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 1", tooSmall, val.Ref))
	tooBig := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 2147483647", tooBig, val.Ref))
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", out, tooSmall, tooBig))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 1, i64 %s", r, out, val.Ref))
	return r, nil
}

func (e *Emitter) emitSetTimeout(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 {
		return Value{}, fmt.Errorf("%d:%d: setTimeout takes a callback", pos.Line, pos.Col)
	}
	closurePtr, err := e.timerCallbackPtr(args[0], "setTimeout", pos, timerExtraArgs(args, 2)...)
	if err != nil {
		return Value{}, err
	}
	delayRef, err := e.timerDelayArg(args, 1)
	if err != nil {
		return Value{}, err
	}
	e.ensureTimerRuntime()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_timer_schedule(ptr %s, i64 %s, i64 0)", r, closurePtr, delayRef))
	return Value{Ref: r, Ty: TypeI64}, nil
}

func (e *Emitter) emitSetInterval(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 {
		return Value{}, fmt.Errorf("%d:%d: setInterval takes a callback", pos.Line, pos.Col)
	}
	closurePtr, err := e.timerCallbackPtr(args[0], "setInterval", pos, timerExtraArgs(args, 2)...)
	if err != nil {
		return Value{}, err
	}
	delayRef, err := e.timerDelayArg(args, 1)
	if err != nil {
		return Value{}, err
	}
	e.ensureTimerRuntime()
	r := e.freshReg()
	// intervalMs == delayMs: the same cadence used for the first fire is
	// reused for every subsequent one, matching real JS's setInterval.
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_timer_schedule(ptr %s, i64 %s, i64 %s)", r, closurePtr, delayRef, delayRef))
	return Value{Ref: r, Ty: TypeI64}, nil
}

// emitSetImmediate implements setImmediate(callback): schedules callback to
// fire via the same timer queue setTimeout/setInterval already use, with
// delayMs hardcoded to 0 (no delay argument — real Node's setImmediate
// doesn't take one either). Known scope narrowing, not a bug: real Node
// guarantees a setImmediate callback fires before a same-tick
// setTimeout(fn, 0) when both are scheduled from inside an I/O callback,
// because its event loop has distinct phases (check vs. timers) — this
// compiler's __kml_timer_drain is a single flat fire-time-ordered queue
// with no phase concept, so setImmediate(fn) and setTimeout(fn, 0) are
// genuinely indistinguishable here (both fire at "now"). Documented in
// docs/status/TIMERS.md rather than silently assumed equivalent.
func (e *Emitter) emitSetImmediate(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 {
		return Value{}, fmt.Errorf("%d:%d: setImmediate takes a callback", pos.Line, pos.Col)
	}
	closurePtr, err := e.timerCallbackPtr(args[0], "setImmediate", pos, args[1:]...)
	if err != nil {
		return Value{}, err
	}
	e.ensureTimerRuntime()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_timer_schedule(ptr %s, i64 0, i64 0)", r, closurePtr))
	return Value{Ref: r, Ty: TypeI64}, nil
}

func (e *Emitter) emitClearTimer(args []ast.Expression, fnName string, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: %s takes exactly 1 argument (id)", pos.Line, pos.Col, fnName)
	}
	idVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	idVal = e.coerce(idVal, TypeI64)
	e.ensureTimerRuntime()
	e.emitInstr(fmt.Sprintf("call void @__kml_timer_clear(i64 %s)", idVal.Ref))
	return Value{Ty: TypeVoid}, nil
}

// emitTimerFireNext: @__kml_timer_fire_next() -> i1 (fire the single
// earliest-due pending timer, 0 when none; TDD-00087) is defined in
// timerssrc/timers.c with the rest of the timer runtime and declared by
// ensureTimerRuntime, so there is nothing left to emit.
func (e *Emitter) emitTimerFireNext() {}

//go:embed timerssrc/timers.c
var timersSource string

// TimersSource is the timer runtime's C source.
func TimersSource() string { return timersSource }

// UsesTimers reports whether the program links the timer runtime.
func (e *Emitter) UsesTimers() bool { return e.usedTimers }

// ensureTimerRuntime declares the timer runtime (timerssrc/timers.c): the
// per-thread queue (read by the generated loop code), schedule/clear/ref
// control, the monotonic clock, and the drains — run-to-empty, one step, and
// the non-blocking tick. The drain checks the pending-signal flags each
// pass whether or not the program uses process.on, so the signal dispatch
// is declared too.
func (e *Emitter) ensureTimerRuntime() {
	if e.usedTimers {
		return
	}
	e.usedTimers = true
	// Generated code that co-occurs with timers leans on these decls.
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureClockGettime()
	e.emitGlobal("declare i32 @nanosleep(ptr noundef, ptr noundef)")
	e.ensureSignalHandlerRuntime()
	e.emitGlobal(`@__kml_timer_data = external thread_local global ptr, align 8
@__kml_timer_len = external thread_local global i64, align 8
@__kml_timer_cap = external thread_local global i64, align 8
@__kml_timer_next_id = external thread_local global i64, align 8
declare i64 @__kml_monotonic_ns()
declare i64 @__kml_timer_schedule(ptr, i64, i64)
declare void @__kml_timer_clear(i64)
declare void @__kml_timer_set_ref(i64, i1)
declare zeroext i1 @__kml_timer_has_ref(i64)
declare void @__kml_timer_refresh(i64)
declare zeroext i1 @__kml_timer_any_ref()
declare zeroext i1 @__kml_timer_fire_next()
declare void @__kml_timer_drain()
declare void @__kml_timer_tick()`)
}

// emitDiscardReturnAdapter wraps a zero-argument closure that returns a value in
// a `void (ptr)` closure that calls it and drops the result. env of the adapter
// is the original closure header { fp, env }.
// emitNoArgAdapter wraps a closure that declares parameters as the
// zero-argument, void closure the timer and tick drains call: the adapter
// calls it with no arguments, each parameter getting what a missing argument
// gets, and drops its result.
func (e *Emitter) emitNoArgAdapter(val Value) (Value, error) {
	e.ensureMalloc()
	restore := e.beginThunkEmit()
	if _, err := e.emitCBCall(Callback{kind: cbClosure, hdrPtr: "%orig", ty: val.Ty}, nil); err != nil {
		restore()
		return Value{}, err
	}
	e.emitInstr("ret void")
	body := e.allocas.String() + e.body.String()
	restore()
	name := e.defineContentNamed("@__kml_noarg.", "void", "ptr %orig", body)
	clo := e.freshReg()
	fpP := e.freshReg()
	epP := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", clo))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", fpP, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", name, fpP))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", epP, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, epP))
	voidTy := TypeVoid
	ty := val.Ty
	ty.FuncParams = nil
	ty.FuncParamDefaults = nil
	ty.FuncHasRest = false
	ty.FuncRetType = &voidTy
	return Value{Ref: clo, Ty: ty}, nil
}

// timerExtraArgs are the arguments a timer passes its callback: those
// after the callback and the delay.
func timerExtraArgs(args []ast.Expression, from int) []ast.Expression {
	if len(args) <= from {
		return nil
	}
	return args[from:]
}

// emitBoundArgsAdapter wraps a callback as a no-argument closure that calls
// it with the already-evaluated, boxed arguments: its environment holds the
// callback's closure and then one box per argument.
func (e *Emitter) emitBoundArgsAdapter(val Value, bound []Value) (Value, error) {
	e.ensureMalloc()
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", env, 8*(len(bound)+1)))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, env))
	for i, b := range bound {
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", slot, env, i+1))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", b.Ref, slot))
	}
	restore := e.beginThunkEmit()
	orig := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %%env, align 8", orig))
	cb := Callback{kind: cbClosure, hdrPtr: orig, ty: val.Ty}
	var args []Value
	for i := range bound {
		if !cb.acceptsArgAt(i) {
			break
		}
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%env, i64 %d", slot, i+1))
		a := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", a, slot))
		args = append(args, Value{Ref: a, Ty: TypeAny})
	}
	if _, err := e.emitCBCall(cb, args); err != nil {
		restore()
		return Value{}, err
	}
	e.emitInstr("ret void")
	body := e.allocas.String() + e.body.String()
	restore()
	name := e.defineContentNamed("@__kml_boundargs.", "void", "ptr %env", body)
	clo := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", name, clo))
	ep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", ep, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", env, ep))
	voidTy := TypeVoid
	ty := val.Ty
	ty.FuncParams = nil
	ty.FuncParamDefaults = nil
	ty.FuncHasRest = false
	ty.FuncRetType = &voidTy
	return Value{Ref: clo, Ty: ty}, nil
}

// emitSpreadArgsAdapter is emitBoundArgsAdapter over arguments only known
// at run time: its environment holds the callback's closure and the header
// of an any[] of them. Each fixed parameter takes its element (undefined
// past the end), a rest parameter the elements after them.
func (e *Emitter) emitSpreadArgsAdapter(val Value, hdr string) (Value, error) {
	e.ensureMalloc()
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, env))
	hdrSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 1", hdrSlot, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, hdrSlot))
	restore := e.beginThunkEmit()
	orig := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %%env, align 8", orig))
	hp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %%env, i64 1", hp))
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", h, hp))
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", data, h))
	lp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, i64 }, ptr %s, i32 0, i32 1", lp, h))
	n := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", n, lp))
	cb := Callback{kind: cbClosure, hdrPtr: orig, ty: val.Ty}
	fixed := len(val.Ty.FuncParams)
	if cb.hasRest() {
		fixed--
	}
	// A missing argument reads a local slot holding undefined, so no load
	// touches the array past its end.
	undef := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", undef))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, undef))
	var args []Value
	for i := 0; i < fixed; i++ {
		present := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %d, %s", present, i, n))
		elem := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", elem, data, i))
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", slot, present, elem, undef))
		a := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", a, slot))
		args = append(args, Value{Ref: a, Ty: TypeAny})
	}
	if cb.hasRest() {
		// The rest: the elements from `fixed` on, as their own header.
		over := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %d", over, n, fixed))
		neg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, over))
		rlen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", rlen, neg, over))
		rdata := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", rdata, data, fixed))
		cb.restArray = &[2]string{e.newArrayHeader(rdata, rlen), rlen}
	}
	if _, err := e.emitCBCall(cb, args); err != nil {
		restore()
		return Value{}, err
	}
	e.emitInstr("ret void")
	body := e.allocas.String() + e.body.String()
	restore()
	name := e.defineContentNamed("@__kml_spreadargs.", "void", "ptr %env", body)
	clo := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", name, clo))
	ep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", ep, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", env, ep))
	voidTy := TypeVoid
	ty := val.Ty
	ty.FuncParams = nil
	ty.FuncParamDefaults = nil
	ty.FuncHasRest = false
	ty.FuncRetType = &voidTy
	return Value{Ref: clo, Ty: ty}, nil
}

// emitDynArgsAdapter wraps a function held in `any` as a no-argument
// closure that calls it through the dynamic ABI with the any[] whose header
// hdr is.
func (e *Emitter) emitDynArgsAdapter(fn Value, hdr string, pos ast.Pos) (Value, error) {
	e.ensureMalloc()
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", fn.Ref, env))
	hdrSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 1", hdrSlot, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, hdrSlot))
	restore := e.beginThunkEmit()
	box := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %%env, align 8", box))
	hp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %%env, i64 1", hp))
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", h, hp))
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", data, h))
	lp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, i64 }, ptr %s, i32 0, i32 1", lp, h))
	n := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", n, lp))
	undef := Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}
	if _, err := e.emitDynFnBoxCallN(Value{Ref: box, Ty: TypeAny}, undef, data, n, "callback is not a function", pos); err != nil {
		restore()
		return Value{}, err
	}
	e.emitInstr("ret void")
	body := e.allocas.String() + e.body.String()
	restore()
	name := e.defineContentNamed("@__kml_dynargs.", "void", "ptr %env", body)
	clo := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", name, clo))
	ep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", ep, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", env, ep))
	return Value{Ref: clo, Ty: FuncType(nil, TypeVoid)}, nil
}

func (e *Emitter) emitDiscardReturnAdapter(val Value) Value {
	e.ensureMalloc()
	retIR := val.Ty.FuncRetType.LLVMRetType()
	name := e.defineContentNamed("@__kml_discard_ret.", "void", "ptr %orig", fmt.Sprintf(`  %%fp_p = getelementptr { ptr, ptr }, ptr %%orig, i32 0, i32 0
  %%fp = load ptr, ptr %%fp_p, align 8
  %%ep_p = getelementptr { ptr, ptr }, ptr %%orig, i32 0, i32 1
  %%ep = load ptr, ptr %%ep_p, align 8
  %%ignored = call %s %%fp(ptr %%ep)
  ret void
`, retIR))
	clo := e.freshReg()
	fpP := e.freshReg()
	epP := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", clo))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", fpP, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", name, fpP))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", epP, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, epP))
	voidTy := TypeVoid
	ty := val.Ty
	ty.FuncRetType = &voidTy
	return Value{Ref: clo, Ty: ty}
}

// emitThrowUnlessFunction throws Node's ERR_INVALID_ARG_TYPE for argument
// name when the box v holds no function.
func (e *Emitter) emitThrowUnlessFunction(v Value, name string) {
	tag, _ := e.emitUnboxTagPayload(v)
	isDyn, isFn, ok := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isDyn, tag, kmlTagDynFunc))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isFn, tag, kmlTagFuncRef))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", ok, isDyn, isFn))
	throwL, contL := e.freshLabel("fnarg.throw"), e.freshLabel("fnarg.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, contL, throwL))
	e.emitLabel(throwL)
	e.ensureDynJSONC()
	suffix := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_received(i64 %s)", suffix, v.Ref))
	msg, err := e.emitStringConcat(Value{Ref: e.internString("The \"" + name + "\" argument must be of type function."), Ty: TypePtr}, Value{Ref: suffix, Ty: TypePtr})
	if err == nil {
		e.emitThrowCoded("TypeError", "ERR_INVALID_ARG_TYPE", msg.Ref)
	} else {
		e.emitTerminator("unreachable")
	}
	e.emitLabel(contL)
}

// The timer handle's methods (`NodeJS.Timeout`/`Immediate`, whose value is
// the timer's id), reached through their `@intrinsic` declarations.
func init() {
	for _, owner := range []string{"Timeout", "Immediate"} {
		for _, name := range []string{"ref", "unref", "hasRef", "refresh", "close"} {
			if owner == "Immediate" && (name == "refresh" || name == "close") {
				continue
			}
			name := name
			intrinsics[owner+".prototype."+name] = intrinsic{
				emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
					return e.emitTimerHandleCall(ex.Callee.(*ast.MemberExpression), name)
				},
				ty: func(e *Emitter, ex *ast.CallExpression) Type {
					if name == "hasRef" {
						return TypeBool
					}
					return e.inferExprType(ex.Callee.(*ast.MemberExpression).Object)
				},
			}
		}
	}
}

func (e *Emitter) emitTimerHandleCall(mem *ast.MemberExpression, name string) (Value, error) {
	h, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	id := e.coerce(h, TypeI64)
	e.ensureTimerRuntime()
	switch name {
	case "ref", "unref":
		on := "true"
		if name == "unref" {
			on = "false"
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_timer_set_ref(i64 %s, i1 %s)", id.Ref, on))
	case "hasRef":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_timer_has_ref(i64 %s)", r, id.Ref))
		return Value{Ref: r, Ty: TypeBool}, nil
	case "refresh":
		e.emitInstr(fmt.Sprintf("call void @__kml_timer_refresh(i64 %s)", id.Ref))
	default:
		e.emitInstr(fmt.Sprintf("call void @__kml_timer_clear(i64 %s)", id.Ref))
	}
	return h, nil
}
