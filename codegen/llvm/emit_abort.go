// emit_abort.go — AbortController / AbortSignal (TDD-00081 Stage 3a). An
// AbortSignal is an object with `aborted`/`reason` fields plus a hidden listener
// registry, so it behaves as an EventTarget that fires "abort"; an
// AbortController wraps one in `signal` and fires it via `abort()`. The signal's
// EventTarget methods (addEventListener/removeEventListener/dispatchEvent) reuse
// the Stage-2 machinery via resolveEventTargetMap. Wiring `signal` into fetch and
// timer cancellation is a follow-on.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

func (e *Emitter) emitNewAbortControllerExpression() (Value, error) {
	e.ensureMapStrHelpers()
	e.ensureMalloc()

	// The signal: aborted=false, reason=undefined, a fresh listener map.
	sigTy := AbortSignalType()
	sigReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", sigReg, sigTy.StructSize()))
	listenersMap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", listenersMap))
	e.storeEventField(sigTy, sigReg, "aborted", "i1", "0")
	e.storeEventField(sigTy, sigReg, "reason", "i64", fmt.Sprintf("%d", nbUndefined))
	e.storeEventField(sigTy, sigReg, "listeners", "ptr", listenersMap)
	e.storeEventField(sigTy, sigReg, "deadlineNs", "i64", "0")
	e.storeEventField(sigTy, sigReg, "onabort", "ptr", "null")
	e.storeEventField(sigTy, sigReg, "followers", "ptr", "null")

	// The controller wraps the signal.
	ctrlTy := AbortControllerType()
	ctrlReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", ctrlReg, ctrlTy.StructSize()))
	e.storeEventField(ctrlTy, ctrlReg, "signal", "ptr", sigReg)

	return Value{Ref: ctrlReg, Ty: ctrlTy}, nil
}

// emitAbortControllerAbort implements `controller.abort(reason?)`: mark the
// signal aborted, record the reason, and dispatch an "abort" event to its
// listeners.
func (e *Emitter) emitAbortControllerAbort(objExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	ctrlVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	// Load the signal object out of the controller.
	sigTy := AbortSignalType()
	sigIdx, _, _ := ctrlVal.Ty.FieldIndex("signal")
	sigGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", sigGep, ctrlVal.Ty.StructIR(), ctrlVal.Ref, sigIdx))
	sigPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", sigPtr, sigGep))

	e.storeEventField(sigTy, sigPtr, "aborted", "i1", "1")
	if len(args) >= 1 {
		reasonVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		reasonVal, err = e.emitBoxValue(reasonVal)
		if err != nil {
			return Value{}, err
		}
		e.storeEventField(sigTy, sigPtr, "reason", "i64", reasonVal.Ref)
	} else {
		// Node defaults a no-argument abort() to an "AbortError" DOMException.
		e.storeEventField(sigTy, sigPtr, "reason", "i64", e.buildDefaultAbortReason())
	}

	// Dispatch an "abort" event to the signal's listeners.
	lIdx, _, _ := sigTy.FieldIndex("listeners")
	lGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", lGep, sigTy.StructIR(), sigPtr, lIdx))
	listenersMap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", listenersMap, lGep))

	eventVal := e.buildAbortEvent()
	// Fire the `onabort` event-handler property first (a listener slot the DOM
	// registers alongside addEventListener listeners), then the addEventListener
	// listeners. They share one event object, so a stopImmediatePropagation() in
	// onabort suppresses the rest (emitDispatchToMap re-reads the stop flag).
	e.emitFireOnabort(sigTy, sigPtr, eventVal)
	if _, err := e.emitDispatchToMap(listenersMap, eventVal); err != nil {
		return Value{}, err
	}
	// Live-propagate to any AbortSignal.any composites following this signal.
	e.ensureAbortPropagate()
	e.emitInstr(fmt.Sprintf("call void @__kml_abort_propagate(ptr %s)", sigPtr))
	return Value{Ty: TypeVoid}, nil
}

// emitFireOnabort loads a signal's `onabort` handler slot and, when non-null,
// invokes it with the abort event — the same closure-header/emitCBCall path
// emitDispatchToMap uses for an addEventListener listener (ADR-00978).
func (e *Emitter) emitFireOnabort(sigTy Type, sigPtr string, eventVal Value) {
	oaIdx, _, _ := sigTy.FieldIndex("onabort")
	oaGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", oaGep, sigTy.StructIR(), sigPtr, oaIdx))
	oaPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", oaPtr, oaGep))
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, oaPtr))
	callL := e.freshLabel("onabort.call")
	afterL := e.freshLabel("onabort.after")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, afterL, callL))
	e.emitLabel(callL)
	cb := Callback{kind: cbClosure, hdrPtr: oaPtr, ty: FuncType([]Type{eventVal.Ty}, TypeVoid)}
	// emitCBCall can fail only on a malformed callback shape, which the assignment
	// path (resolveEventTargetListenerArg) already rejected; ignore the error to
	// keep this a void helper matching emitDispatchToMap's inline listener call.
	_, _ = e.emitCBCall(cb, []Value{eventVal})
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
	e.emitLabel(afterL)
}

// emitAbortSignalOnabortAssign implements `signal.onabort = cb` (ADR-00978):
// store the listener's closure-header ptr into the signal's `onabort` field, so
// emitAbortControllerAbort fires it alongside the addEventListener listeners.
// `= null` clears the slot. The listener is resolved through
// resolveEventTargetListenerArg, giving it the same Event-typed param hint and
// arity check an addEventListener listener gets.
func (e *Emitter) emitAbortSignalOnabortAssign(objExpr, rhs ast.Expression, pos ast.Pos) (Value, error) {
	sigVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	sigTy := sigVal.Ty
	oaIdx, _, _ := sigTy.FieldIndex("onabort")
	oaGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", oaGep, sigTy.StructIR(), sigVal.Ref, oaIdx))

	handlerPtr := "null"
	if _, isNull := rhs.(*ast.NullLiteral); !isNull {
		handlerPtr, err = e.resolveEventTargetListenerArg(rhs, pos)
		if err != nil {
			return Value{}, err
		}
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", handlerPtr, oaGep))
	return Value{Ty: TypeVoid}, nil
}

// ensureSignalAborted emits @__kml_signal_aborted(signal) -> i1: true if the
// signal's aborted flag is set, OR its AbortSignal.timeout deadline has elapsed
// (in which case it latches the aborted flag). Null signal → false. Shared by the
// fetch await loop and the event-loop resume scan (TDD-00081 Stage 3c).
func (e *Emitter) ensureSignalAborted() {
	if e.usedSignalAborted {
		return
	}
	e.usedSignalAborted = true
	e.ensureTimerRuntime() // for @__kml_monotonic_ns
	e.emitGlobal(fmt.Sprintf(`
define i1 @__kml_signal_aborted(ptr %%sig) {
entry:
  %%isnull = icmp eq ptr %%sig, null
  br i1 %%isnull, label %%no, label %%chk
chk:
  %%ab = load i8, ptr %%sig, align 1
  %%isab = icmp ne i8 %%ab, 0
  br i1 %%isab, label %%yes, label %%chkdl
chkdl:
  %%dl_p = getelementptr %s, ptr %%sig, i32 0, i32 3
  %%dl = load i64, ptr %%dl_p, align 8
  %%hasdl = icmp ne i64 %%dl, 0
  br i1 %%hasdl, label %%cmpdl, label %%no
cmpdl:
  %%now = call i64 @__kml_monotonic_ns()
  %%elapsed = icmp sge i64 %%now, %%dl
  br i1 %%elapsed, label %%setab, label %%no
setab:
  store i8 1, ptr %%sig, align 1
  br label %%yes
yes:
  ret i1 1
no:
  ret i1 0
}`, AbortSignalType().StructIR()))
}

// emitAbortSignalTimeout implements the static `AbortSignal.timeout(ms)`: a
// standalone signal that aborts once `ms` milliseconds have elapsed. The deadline
// is checked by whatever awaits on it (the fetch loop); a bare `signal.aborted`
// read only reflects the timeout after such a check has run.
func (e *Emitter) emitAbortSignalTimeout(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: AbortSignal.timeout(ms) requires 1 argument", pos.Line, pos.Col)
	}
	msVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	msVal = e.coerce(msVal, TypeI64)
	e.ensureTimerRuntime()
	e.ensureMapStrHelpers()
	e.ensureMalloc()

	now := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_monotonic_ns()", now))
	msNs := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 1000000", msNs, msVal.Ref))
	deadline := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", deadline, now, msNs))

	sigTy := AbortSignalType()
	sigReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", sigReg, sigTy.StructSize()))
	listenersMap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", listenersMap))
	e.storeEventField(sigTy, sigReg, "aborted", "i1", "0")
	e.storeEventField(sigTy, sigReg, "reason", "i64", fmt.Sprintf("%d", nbUndefined))
	e.storeEventField(sigTy, sigReg, "listeners", "ptr", listenersMap)
	e.storeEventField(sigTy, sigReg, "deadlineNs", "i64", deadline)
	e.storeEventField(sigTy, sigReg, "onabort", "ptr", "null")
	e.storeEventField(sigTy, sigReg, "followers", "ptr", "null")
	// TDD-00216: register the deadline so the event loop / timer drain fire the
	// abort (aborted + reason + listeners/onabort) in the background — without
	// keeping the loop alive for it (Node unref parity).
	e.ensureAbortTimeoutRuntime()
	e.emitInstr(fmt.Sprintf("call void @__kml_abort_to_register(i64 %s, ptr %s)", deadline, sigReg))
	return Value{Ref: sigReg, Ty: sigTy}, nil
}

// ensureAbortRegistryGlobals emits the registry state + the two loop-facing
// helpers (soonest, fire_due) that the event loop / timer drain call every
// iteration. They are ALWAYS emitted (from ensureTimerRuntime and the event
// loop) so the loop hooks compile regardless of whether AbortSignal.timeout is
// ever used, and are cheap no-ops on an empty registry: soonest returns 0 ("no
// constraint"), fire_due's loop is empty. fire_due dispatches through the
// @__kml_abort_to_fire_fn function pointer (null until AbortSignal.timeout is
// used, which is also the only way an entry ever gets registered), so the
// abort-specific dispatcher symbol is referenced only where it is defined
// (ADR-00979 / TDD-00216).
func (e *Emitter) ensureAbortRegistryGlobals() {
	if e.usedAbortRegistry {
		return
	}
	e.usedAbortRegistry = true
	e.ensureTimerRuntime() // @__kml_monotonic_ns

	// Registry: a growable array of { i64 deadlineNs, ptr sig } (16 bytes/entry),
	// thread-local like the timer queue. A fired entry sets deadlineNs = -1.
	e.emitGlobal("@__kml_abort_to_data = internal thread_local global ptr null, align 8")
	e.emitGlobal("@__kml_abort_to_len = internal thread_local global i64 0, align 8")
	e.emitGlobal("@__kml_abort_to_cap = internal thread_local global i64 0, align 8")
	e.emitGlobal("@__kml_abort_to_fire_fn = internal thread_local global ptr null, align 8")

	// soonest(): the earliest unfired (deadlineNs != -1) deadline, or 0 if none.
	e.emitGlobal(`
define i64 @__kml_abort_to_soonest() {
entry:
  %len = load i64, ptr @__kml_abort_to_len, align 8
  %data = load ptr, ptr @__kml_abort_to_data, align 8
  %best = alloca i64, align 8
  %i = alloca i64, align 8
  store i64 0, ptr %best, align 8
  store i64 0, ptr %i, align 8
  br label %loop
loop:
  %iv = load i64, ptr %i, align 8
  %inb = icmp slt i64 %iv, %len
  br i1 %inb, label %body, label %done
body:
  %slot = getelementptr { i64, ptr }, ptr %data, i64 %iv
  %dl_p = getelementptr { i64, ptr }, ptr %slot, i32 0, i32 0
  %dl = load i64, ptr %dl_p, align 8
  %fired = icmp eq i64 %dl, -1
  br i1 %fired, label %next, label %consider
consider:
  %bv = load i64, ptr %best, align 8
  %empty = icmp eq i64 %bv, 0
  %sooner = icmp slt i64 %dl, %bv
  %take = or i1 %empty, %sooner
  br i1 %take, label %settake, label %next
settake:
  store i64 %dl, ptr %best, align 8
  br label %next
next:
  %in = add i64 %iv, 1
  store i64 %in, ptr %i, align 8
  br label %loop
done:
  %r = load i64, ptr %best, align 8
  ret i64 %r
}`)

	// fire_due(): fire every entry whose deadline has passed, marking it done.
	// Dispatch is indirect through @__kml_abort_to_fire_fn (null-guarded), which
	// AbortSignal.timeout sets to @__kml_abort_timeout_fire on first register.
	e.emitGlobal(`
define void @__kml_abort_to_fire_due() {
entry:
  %len = load i64, ptr @__kml_abort_to_len, align 8
  %fn = load ptr, ptr @__kml_abort_to_fire_fn, align 8
  %nofn = icmp eq ptr %fn, null
  br i1 %nofn, label %done, label %scan
scan:
  %now = call i64 @__kml_monotonic_ns()
  %i = alloca i64, align 8
  store i64 0, ptr %i, align 8
  br label %loop
loop:
  %iv = load i64, ptr %i, align 8
  %inb = icmp slt i64 %iv, %len
  br i1 %inb, label %body, label %done
body:
  %data = load ptr, ptr @__kml_abort_to_data, align 8
  %slot = getelementptr { i64, ptr }, ptr %data, i64 %iv
  %dl_p = getelementptr { i64, ptr }, ptr %slot, i32 0, i32 0
  %dl = load i64, ptr %dl_p, align 8
  %fired = icmp eq i64 %dl, -1
  br i1 %fired, label %next, label %chkdue
chkdue:
  %due = icmp sle i64 %dl, %now
  br i1 %due, label %fire, label %next
fire:
  %sig_p = getelementptr { i64, ptr }, ptr %slot, i32 0, i32 1
  %sig = load ptr, ptr %sig_p, align 8
  store i64 -1, ptr %dl_p, align 8
  call void %fn(ptr %sig)
  br label %next
next:
  %in = add i64 %iv, 1
  store i64 %in, ptr %i, align 8
  br label %loop
done:
  ret void
}`)
}

// ensureAbortTimeoutRuntime emits the AbortSignal.timeout-specific machinery
// (TDD-00216): the compile-time dispatcher and __kml_abort_to_register, which
// also arms @__kml_abort_to_fire_fn (so the always-present fire_due dispatches).
func (e *Emitter) ensureAbortTimeoutRuntime() {
	if e.usedAbortTimeout {
		return
	}
	e.usedAbortTimeout = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureAbortRegistryGlobals()

	// The single compile-time dispatcher (fires one signal's listeners+onabort).
	e.ensureAbortPropagate()
	e.emitAbortTimeoutDispatcher()

	// register(deadline, sig): append the entry and arm the fire fn pointer so
	// the (always-emitted) fire_due starts dispatching due timeouts.
	e.emitGlobal(`
define void @__kml_abort_to_register(i64 %dl, ptr %sig) {
entry:
  store ptr @__kml_abort_timeout_fire, ptr @__kml_abort_to_fire_fn, align 8
  %len = load i64, ptr @__kml_abort_to_len, align 8
  %cap = load i64, ptr @__kml_abort_to_cap, align 8
  %data = load ptr, ptr @__kml_abort_to_data, align 8
  %need = add i64 %len, 1
  %needgrow = icmp sgt i64 %need, %cap
  br i1 %needgrow, label %grow, label %doappend
grow:
  %cap2 = mul i64 %cap, 2
  %atleast8 = icmp sgt i64 %cap2, 8
  %newcap = select i1 %atleast8, i64 %cap2, i64 8
  %newbytes = mul i64 %newcap, 16
  %newdata = call ptr @realloc(ptr %data, i64 %newbytes)
  store ptr %newdata, ptr @__kml_abort_to_data, align 8
  store i64 %newcap, ptr @__kml_abort_to_cap, align 8
  br label %doappend
doappend:
  %dataNow = load ptr, ptr @__kml_abort_to_data, align 8
  %slot = getelementptr { i64, ptr }, ptr %dataNow, i64 %len
  %dl_p = getelementptr { i64, ptr }, ptr %slot, i32 0, i32 0
  store i64 %dl, ptr %dl_p, align 8
  %sig_p = getelementptr { i64, ptr }, ptr %slot, i32 0, i32 1
  store ptr %sig, ptr %sig_p, align 8
  %newlen = add i64 %len, 1
  store i64 %newlen, ptr @__kml_abort_to_len, align 8
  ret void
}`)
}

// emitAbortTimeoutDispatcher emits @__kml_abort_timeout_fire(ptr %sig) once: set
// aborted, store a TimeoutError DOMException reason, and dispatch the "abort"
// event to the signal's listeners + onabort. Its body uses the compile-time
// emitters (emitDispatchToMap/emitFireOnabort) against the %sig parameter, so no
// runtime listener-map/closure/reason helpers are needed. The emitter's builder
// state is swapped out for this synthetic function and restored after, mirroring
// emitClosureFunc.
func (e *Emitter) emitAbortTimeoutDispatcher() {
	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedScopes := e.scopes
	savedBlockDone := e.blockDone
	savedRetType := e.currentRetType

	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.scopes = nil
	e.blockDone = false
	e.currentRetType = TypeVoid

	sigTy := AbortSignalType()
	// aborted = 1
	e.storeEventField(sigTy, "%sig", "aborted", "i1", "1")
	// reason = TimeoutError DOMException (Node's AbortSignal.timeout reason),
	// boxed as a kmlTagObject `any` for the reason slot.
	reason := e.buildErrorObj(errorKindIDs["DOMException"], e.internString("The operation timed out"), e.internString("TimeoutError"))
	e.storeEventField(sigTy, "%sig", "reason", "i64", e.emitNbTagPtr(reason, kmlTagObject))
	// Dispatch the "abort" event to onabort + the addEventListener listeners.
	eventVal := e.buildAbortEvent()
	e.emitFireOnabort(sigTy, "%sig", eventVal)
	lIdx, _, _ := sigTy.FieldIndex("listeners")
	lGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%sig, i32 0, i32 %d", lGep, sigTy.StructIR(), lIdx))
	listenersMap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", listenersMap, lGep))
	// emitDispatchToMap may fail only on a malformed event shape (fixed here).
	_, _ = e.emitDispatchToMap(listenersMap, eventVal)
	// Live-propagate to AbortSignal.any composites following this signal.
	e.emitInstr("call void @__kml_abort_propagate(ptr %sig)")
	e.emitTerminator("ret void")

	e.functions.WriteString("\ndefine void @__kml_abort_timeout_fire(ptr %sig) {\nentry:\n")
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.labelCtr = savedLabelCtr
	e.scopes = savedScopes
	e.blockDone = savedBlockDone
	e.currentRetType = savedRetType
}

// buildDefaultAbortReason builds the "AbortError" DOMException Node uses as the
// default reason for a no-argument abort() (controller.abort() and the static
// AbortSignal.abort()). Returns an i64 register holding the error boxed as a
// kmlTagObject `any`, ready to store into the signal's reason slot.
func (e *Emitter) buildDefaultAbortReason() string {
	err := e.buildErrorObj(errorKindIDs["DOMException"], e.internString("This operation was aborted"), e.internString("AbortError"))
	return e.emitNbTagPtr(err, kmlTagObject)
}

// emitAbortSignalStaticAbort implements the static `AbortSignal.abort(reason?)`:
// an already-aborted signal. With no argument the reason defaults to an
// "AbortError" DOMException, matching Node.
func (e *Emitter) emitAbortSignalStaticAbort(args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureMapStrHelpers()
	e.ensureMalloc()

	sigTy := AbortSignalType()
	sigReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", sigReg, sigTy.StructSize()))
	listenersMap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", listenersMap))
	e.storeEventField(sigTy, sigReg, "aborted", "i1", "1")
	e.storeEventField(sigTy, sigReg, "listeners", "ptr", listenersMap)
	e.storeEventField(sigTy, sigReg, "deadlineNs", "i64", "0")
	e.storeEventField(sigTy, sigReg, "onabort", "ptr", "null")
	e.storeEventField(sigTy, sigReg, "followers", "ptr", "null")

	if len(args) >= 1 {
		reasonVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		reasonVal, err = e.emitBoxValue(reasonVal)
		if err != nil {
			return Value{}, err
		}
		e.storeEventField(sigTy, sigReg, "reason", "i64", reasonVal.Ref)
	} else {
		e.storeEventField(sigTy, sigReg, "reason", "i64", e.buildDefaultAbortReason())
	}
	return Value{Ref: sigReg, Ty: sigTy}, nil
}

// emitAbortSignalAny implements the static `AbortSignal.any(signals)`: a
// composite signal that is aborted as soon as any input signal is aborted. The
// input signals' aborted flags are snapshotted at construction; if any is already
// aborted the composite inherits that signal's reason. Live propagation for
// sources aborted after construction is a follow-on (needs listener wiring).
func (e *Emitter) emitAbortSignalAny(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: AbortSignal.any(signals) requires 1 argument", pos.Line, pos.Col)
	}
	e.ensureMapStrHelpers()
	e.ensureMalloc()

	sigTy := AbortSignalType()
	sigReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", sigReg, sigTy.StructSize()))
	listenersMap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", listenersMap))
	e.storeEventField(sigTy, sigReg, "aborted", "i1", "0")
	e.storeEventField(sigTy, sigReg, "reason", "i64", fmt.Sprintf("%d", nbUndefined))
	e.storeEventField(sigTy, sigReg, "listeners", "ptr", listenersMap)
	e.storeEventField(sigTy, sigReg, "deadlineNs", "i64", "0")
	e.storeEventField(sigTy, sigReg, "onabort", "ptr", "null")
	e.storeEventField(sigTy, sigReg, "followers", "ptr", "null")

	// Resolve the input array to (ptr, len) of AbortSignal pointers.
	ptr, length, _, err := e.resolveArrayForHOF(args[0], pos)
	if err != nil {
		return Value{}, err
	}

	// Loop over the sources; the first already-aborted one latches the composite.
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("absany.cond")
	bodyL := e.freshLabel("absany.body")
	setL := e.freshLabel("absany.set")
	nextL := e.freshLabel("absany.next")
	afterL := e.freshLabel("absany.after")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	idxVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	// Stop once we run off the end OR the composite is already latched.
	atEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, %s", atEnd, idxVal, length))
	abrGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", abrGep, sigTy.StructIR(), sigReg))
	abrNow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", abrNow, abrGep))
	abrSet := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i8 %s, 0", abrSet, abrNow))
	stop := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", stop, atEnd, abrSet))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", stop, afterL, bodyL))

	e.emitLabel(bodyL)
	srcGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", srcGep, ptr, idxVal))
	srcPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", srcPtr, srcGep))
	// A null source contributes nothing.
	srcNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", srcNull, srcPtr))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", srcNull, nextL, setL))

	e.emitLabel(setL)
	e.ensureSignalAborted()
	srcAborted := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_signal_aborted(ptr %s)", srcAborted, srcPtr))
	doSetL := e.freshLabel("absany.doset")
	regL := e.freshLabel("absany.reg")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", srcAborted, doSetL, regL))

	// Not (yet) aborted: register the composite as a follower of this source,
	// so a later abort of the source live-propagates (ensureAbortPropagate).
	e.emitLabel(regL)
	e.ensureAbortPropagate()
	folIdx, _, _ := sigTy.FieldIndex("followers")
	folGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", folGep, sigTy.StructIR(), srcPtr, folIdx))
	oldHead := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", oldHead, folGep))
	node := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", node))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", sigReg, node))
	nodeNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", nodeNext, node))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", oldHead, nodeNext))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", node, folGep))
	e.emitTerminator(fmt.Sprintf("br label %%%s", nextL))

	e.emitLabel(doSetL)
	// Latch aborted and copy the source's reason.
	e.storeEventField(sigTy, sigReg, "aborted", "i1", "1")
	srcReasonIdx, _, _ := sigTy.FieldIndex("reason")
	srcReasonGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", srcReasonGep, sigTy.StructIR(), srcPtr, srcReasonIdx))
	srcReason := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", srcReason, srcReasonGep))
	e.storeEventField(sigTy, sigReg, "reason", "i64", srcReason)
	e.emitTerminator(fmt.Sprintf("br label %%%s", nextL))

	e.emitLabel(nextL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(afterL)
	return Value{Ref: sigReg, Ty: sigTy}, nil
}

// buildAbortEvent constructs a plain Event whose type is "abort".
func (e *Emitter) buildAbortEvent() Value {
	e.ensureMalloc()
	ty := EventType()
	objReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", objReg, ty.StructSize()))
	e.storeEventField(ty, objReg, "type", "ptr", e.internString("abort"))
	e.storeEventField(ty, objReg, "defaultPrevented", "i1", "0")
	e.storeEventField(ty, objReg, "stopImmediate", "i1", "0")
	return Value{Ref: objReg, Ty: ty}
}

// ensureAbortPropagate emits @__kml_abort_propagate(ptr %sig) once: walk the
// signal's `followers` linked list ({ ptr composite, ptr next } nodes) and, for
// each not-yet-aborted follower, latch it with the source's reason, fire its
// onabort + addEventListener listeners, then recurse so a composite that is
// itself a source of another composite propagates onward. Called after every
// abort that fires listeners (controller.abort(), the timeout dispatcher), so
// a source aborted AFTER an AbortSignal.any(...) composite was built still
// reaches it.
func (e *Emitter) ensureAbortPropagate() {
	if e.usedAbortPropagate {
		return
	}
	e.usedAbortPropagate = true

	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedScopes := e.scopes
	savedBlockDone := e.blockDone
	savedRetType := e.currentRetType

	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.scopes = nil
	e.blockDone = false
	e.currentRetType = TypeVoid

	sigTy := AbortSignalType()
	reasonIdx, _, _ := sigTy.FieldIndex("reason")
	followersIdx, _, _ := sigTy.FieldIndex("followers")

	// The source's (already-stored) reason, copied to each follower.
	srGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%sig, i32 0, i32 %d", srGep, sigTy.StructIR(), reasonIdx))
	srcReason := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", srcReason, srGep))

	nodePtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", nodePtr))
	fGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%sig, i32 0, i32 %d", fGep, sigTy.StructIR(), followersIdx))
	head := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", head, fGep))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", head, nodePtr))

	condL := e.freshLabel("absprop.cond")
	bodyL := e.freshLabel("absprop.body")
	fireL := e.freshLabel("absprop.fire")
	nextL := e.freshLabel("absprop.next")
	doneL := e.freshLabel("absprop.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cur, nodePtr))
	isEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isEnd, cur))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isEnd, doneL, bodyL))

	e.emitLabel(bodyL)
	fol := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fol, cur))
	// Skip a follower already aborted (its own abort won the race).
	abGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", abGep, sigTy.StructIR(), fol))
	ab := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", ab, abGep))
	already := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i8 %s, 0", already, ab))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", already, nextL, fireL))

	e.emitLabel(fireL)
	e.storeEventField(sigTy, fol, "aborted", "i1", "1")
	e.storeEventField(sigTy, fol, "reason", "i64", srcReason)
	eventVal := e.buildAbortEvent()
	e.emitFireOnabort(sigTy, fol, eventVal)
	lIdx, _, _ := sigTy.FieldIndex("listeners")
	lGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", lGep, sigTy.StructIR(), fol, lIdx))
	lMap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", lMap, lGep))
	_, _ = e.emitDispatchToMap(lMap, eventVal)
	// A composite can itself be another composite's source — recurse.
	e.emitInstr(fmt.Sprintf("call void @__kml_abort_propagate(ptr %s)", fol))
	e.emitTerminator(fmt.Sprintf("br label %%%s", nextL))

	e.emitLabel(nextL)
	nGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", nGep, cur))
	nxt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", nxt, nGep))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", nxt, nodePtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	e.emitTerminator("ret void")

	e.functions.WriteString("\ndefine void @__kml_abort_propagate(ptr %sig) {\nentry:\n")
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.labelCtr = savedLabelCtr
	e.scopes = savedScopes
	e.blockDone = savedBlockDone
	e.currentRetType = savedRetType
}
