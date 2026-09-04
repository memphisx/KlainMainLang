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
)

func (e *Emitter) emitNewAbortControllerExpression() (Value, error) {
	e.ensureMapStrHelpers()
	e.ensureMalloc()

	// The signal: aborted=false, reason=null, a fresh listener map.
	sigTy := AbortSignalType()
	sigReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", sigReg, sigTy.StructSize()))
	listenersMap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", listenersMap))
	e.storeEventField(sigTy, sigReg, "aborted", "i1", "0")
	e.storeEventField(sigTy, sigReg, "reason", "ptr", "null")
	e.storeEventField(sigTy, sigReg, "listeners", "ptr", listenersMap)
	e.storeEventField(sigTy, sigReg, "deadlineNs", "i64", "0")

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
		reasonVal = e.coerce(reasonVal, TypePtr)
		e.storeEventField(sigTy, sigPtr, "reason", "ptr", reasonVal.Ref)
	} else {
		// Node defaults a no-argument abort() to an "AbortError" DOMException.
		e.storeEventField(sigTy, sigPtr, "reason", "ptr", e.buildDefaultAbortReason())
	}

	// Dispatch an "abort" event to the signal's listeners.
	lIdx, _, _ := sigTy.FieldIndex("listeners")
	lGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", lGep, sigTy.StructIR(), sigPtr, lIdx))
	listenersMap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", listenersMap, lGep))

	eventVal := e.buildAbortEvent()
	if _, err := e.emitDispatchToMap(listenersMap, eventVal); err != nil {
		return Value{}, err
	}
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
	e.storeEventField(sigTy, sigReg, "reason", "ptr", "null")
	e.storeEventField(sigTy, sigReg, "listeners", "ptr", listenersMap)
	e.storeEventField(sigTy, sigReg, "deadlineNs", "i64", deadline)
	return Value{Ref: sigReg, Ty: sigTy}, nil
}

// buildDefaultAbortReason builds the "AbortError" DOMException Node uses as the
// default reason for a no-argument abort() (controller.abort() and the static
// AbortSignal.abort()). Returns the error-object pointer register.
func (e *Emitter) buildDefaultAbortReason() string {
	return e.buildErrorObj(errorKindIDs["DOMException"], e.internString("This operation was aborted"), e.internString("AbortError"))
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

	if len(args) >= 1 {
		reasonVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		reasonVal = e.coerce(reasonVal, TypePtr)
		e.storeEventField(sigTy, sigReg, "reason", "ptr", reasonVal.Ref)
	} else {
		e.storeEventField(sigTy, sigReg, "reason", "ptr", e.buildDefaultAbortReason())
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
	e.storeEventField(sigTy, sigReg, "reason", "ptr", "null")
	e.storeEventField(sigTy, sigReg, "listeners", "ptr", listenersMap)
	e.storeEventField(sigTy, sigReg, "deadlineNs", "i64", "0")

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
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", srcAborted, doSetL, nextL))

	e.emitLabel(doSetL)
	// Latch aborted and copy the source's reason.
	e.storeEventField(sigTy, sigReg, "aborted", "i1", "1")
	srcReasonIdx, _, _ := sigTy.FieldIndex("reason")
	srcReasonGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", srcReasonGep, sigTy.StructIR(), srcPtr, srcReasonIdx))
	srcReason := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", srcReason, srcReasonGep))
	e.storeEventField(sigTy, sigReg, "reason", "ptr", srcReason)
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
