package llvm

// emit_unhandled.go — unhandled promise rejections, as Node's default
// (`--unhandled-rejections=throw`) treats them. A promise rejected while
// nothing consumes it (no reaction, no awaiter) is queued; every consumer
// (a then/catch/finally reaction, an await, a combinator, an adoption) marks
// the promise handled. After each microtask checkpoint (the end of
// __kml_drain_microtasks, Node's processTicksAndRejections) a queued promise
// still unhandled is reported: to process.on('unhandledRejection') listeners
// (reason, promise) when one is registered, else as an uncaught exception —
// to process.on('uncaughtException') when one is registered (the program
// continues), else printed and the process exits 1.
//
// The promise struct's slot promiseFlagsSlot holds the flags: bit 0 handled,
// bit 1 queued.

import "fmt"

const (
	promiseFlagsSlot   = 6
	promiseFlagHandled = 1
	promiseFlagQueued  = 2
)

// ensureUnhandledRejections emits the tracking runtime once.
func (e *Emitter) ensureUnhandledRejections() {
	if e.usedUnhandledRuntime {
		return
	}
	e.usedUnhandledRuntime = true
	e.ensurePromiseRuntime()
	e.ensureMicrotasks()
	e.ensureExceptionHelpers()
	e.ensureProcessHooks()
	// The queue and the checkpoint are microtask.c's (TDD-00240).
}

// emitUnhandledFinalize defines @__kml_unhandled_report when rejections are
// tracked, else @__kml_unhandled_check as a no-op for the microtask
// checkpoint. Run at finalize, before the layout table (the report boxes a
// Promise).
func (e *Emitter) emitUnhandledFinalize() {
	if e.usedUnhandledRuntime {
		e.emitUnhandledReport()
		return
	}
	if e.usedMicrotasks {
		// microtask.c's checkpoint calls it; nothing is ever reported here.
		e.emitGlobal("define void @__kml_unhandled_report(ptr %p) {\nentry:\n  ret void\n}")
	}
}

// emitUnhandledReport generates @__kml_unhandled_report(ptr p): the report
// of one unhandled rejection.
func (e *Emitter) emitUnhandledReport() {
	restore := e.beginDetachedFunc()
	raw := e.loadRejectReasonCaught("%p")
	// A runtime rejection that stored no reason (an Error tag over a null
	// payload) is `undefined`.
	tag0, pay := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i8, i64 } %s, 0", tag0, raw.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i8, i64 } %s, 1", pay, raw.Ref))
	nullErr, isE, isZ := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, 13", isE, tag0))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isZ, pay))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", nullErr, isE, isZ))
	ntag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i8 %d, i8 %s", ntag, nullErr, kmlTagUndefined, tag0))
	agg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue { i8, i64 } %s, i8 %s, 0", agg, raw.Ref, ntag))
	reason := Value{Ref: agg, Ty: TypeCaught}
	// The process 'unhandledRejection' event, (reason, promise), when
	// something listens.
	rb, err := e.emitBoxValue(reason)
	if err != nil {
		rb = Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}
	}
	pb, err := e.emitBoxValue(Value{Ref: "%p", Ty: PromiseOf(TypeAny)})
	if err != nil {
		pb = Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}
	}
	hasH := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_process_hook_call(i32 2, i64 %s, i64 %s)", hasH, rb.Ref, pb.Ref))
	listenerL, uncaughtL := e.freshLabel("unh.listener"), e.freshLabel("unh.uncaught")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasH, listenerL, uncaughtL))
	e.emitLabel(listenerL)
	e.emitTerminator("ret void")
	// Otherwise an uncaught exception: process.on('uncaughtException') gets
	// the Error (the program goes on), else the reason is printed as an
	// uncaught throw's and the process exits 1.
	e.emitLabel(uncaughtL)
	tag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i8, i64 } %s, 0", tag, reason.Ref))
	handled := e.freshReg()
	rtag, rpay := e.caughtParts(reason)
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_process_uncaught(i8 %s, i64 %s, i1 1)", handled, rtag, rpay))
	goOnL, printL := e.freshLabel("unh.handled"), e.freshLabel("unh.print")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", handled, goOnL, printL))
	e.emitLabel(goOnL)
	e.emitTerminator("ret void")
	e.emitLabel(printL)
	e.emitInstr(fmt.Sprintf("call void @__kml_worker_uncaught(i8 %s, i64 %s)", tag, pay))
	msg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_caught_unc_msg(i8 %s, i64 %s)", msg, tag, pay))
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ...) @printf(ptr @.kml_unc_fmt, ptr %s)", msg))
	e.emitInstr("call void @__kml_run_exit_handlers(i64 1)")
	e.emitInstr(e.exitCall("1"))
	e.emitTerminator("unreachable")
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine void @__kml_unhandled_report(ptr %%p) {\nentry:\n%s}\n", body))
}

// emitMarkPromiseHandled marks promise p consumed (a reaction, an await, a
// combinator).
func (e *Emitter) emitMarkPromiseHandled(p string) {
	e.ensureUnhandledRejections()
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_mark_handled(ptr %s)", p))
}
