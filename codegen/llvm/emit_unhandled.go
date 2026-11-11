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

import (
	"fmt"

	"KlainMainLang/ast"
)

const (
	promiseFlagsSlot   = 6
	promiseFlagHandled = 1
	promiseFlagQueued  = 2
	// promiseFlagBoxedValue marks the body of an instance of a class
	// extending Promise<T> that settles to a boxed `any` rather than a T (a
	// species construction through a callback held in `any`, TDD-00247):
	// what a reader through a box decodes its value as.
	promiseFlagBoxedValue = 8
	// promiseFlagSubclass marks the body of an instance of a class extending
	// Promise: its constructor is not %Promise%, so an `await` of it held as
	// a typed Promise adopts it through its `then` (emitAwaitSubclassSplit).
	promiseFlagSubclass = 16
)

// ensureUnhandledRejections emits the tracking runtime once.
func (e *Emitter) ensureUnhandledRejections() {
	if e.usedUnhandledRuntime {
		return
	}
	e.usedUnhandledRuntime = true
	e.ensureExceptionHelpers()
	e.ensureMicrotasks()
	e.ensureUnhandledRejections()
	e.ensureProcessHooks()
	// The report names a non-Error reason's constructor
	// (emitUnhandledRejectionError), a prototype read generated before the
	// report itself is.
	e.ensureShapeRuntime()
	e.usedProtoOf = true
	// The queue and the checkpoint are microtask.c's (TDD-00240).
}

// emitUnhandledFinalize defines @__kml_unhandled_report when rejections are
// tracked, else @__kml_unhandled_check as a no-op for the microtask
// checkpoint. Run at finalize, before the layout table (the report boxes a
// Promise).
func (e *Emitter) emitUnhandledFinalize() {
	// Runs again after the late finalize steps (a class extending Promise's
	// inspect routine reaches the promise runtime there): once defined, done.
	if e.unhandledReportDone {
		return
	}
	if e.usedUnhandledRuntime {
		e.unhandledReportDone = true
		e.emitUnhandledReport()
		return
	}
	// Otherwise microtask.c's checkpoint calls hooks.c's default, which
	// reports nothing.
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
	reason, err = e.emitUnhandledRejectionError(reason, rb)
	if err != nil {
		reason = Value{Ref: agg, Ty: TypeCaught}
	}
	tag, pay := e.caughtParts(reason)
	handled := e.freshReg()
	rtag, rpay := tag, pay
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_process_uncaught_guarded(i8 %s, i64 %s, i1 1)", handled, rtag, rpay))
	goOnL, printL := e.freshLabel("unh.handled"), e.freshLabel("unh.print")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", handled, goOnL, printL))
	e.emitLabel(goOnL)
	e.emitTerminator("ret void")
	e.emitLabel(printL)
	e.declareWorkerUncaught()
	e.emitInstr(fmt.Sprintf("call void @__kml_worker_uncaught(i8 %s, i64 %s)", tag, pay))
	e.emitInstr(fmt.Sprintf("call void @__kml_print_uncaught(i8 %s, i64 %s)", tag, pay))
	e.emitInstr("call void @__kml_run_exit_handlers(i64 1)")
	e.emitInstr(e.exitCall("1"))
	e.emitTerminator("unreachable")
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("void @__kml_unhandled_report("+irPtr+" %p)", body)
}

// emitMarkPromiseHandled marks promise p consumed (a reaction, an await, a
// combinator).
func (e *Emitter) emitMarkPromiseHandled(p string) {
	e.ensureUnhandledRejections()
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_mark_handled("+irPtr+" %s)", p))
}

// unhandledRejectionMsg is Node's UnhandledPromiseRejection message, before
// the reason's text and the closing `".`.
const unhandledRejectionMsg = "This error originated either by throwing inside of an async function without a catch block, or by rejecting a promise which was not handled with .catch(). The promise rejected with the reason \""

// emitUnhandledRejectionError is the exception an unhandled rejection
// raises (TypeCaught): an Error reason itself; any other reason Node's
// UnhandledPromiseRejection Error (code ERR_UNHANDLED_REJECTION), naming
// the reason as noSideEffectsToString does — a primitive's String, an
// array's [object Array], another object's #<its constructor's name>.
// box is the reason boxed.
func (e *Emitter) emitUnhandledRejectionError(reason, box Value) (Value, error) {
	tag, pay := e.caughtParts(reason)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca { i8, i64 }, align 8", slot))
	e.emitInstr(fmt.Sprintf("store { i8, i64 } %s, "+irPtr+" %s, align 8", reason.Ref, slot))
	isRaw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isRaw, tag, kmlTagError))
	doneL, objChkL, wrapL := e.freshLabel("unh.err.done"), e.freshLabel("unh.err.obj"), e.freshLabel("unh.err.wrap")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isRaw, doneL, objChkL))
	e.emitLabel(objChkL)
	isObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	probeL := e.freshLabel("unh.err.probe")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, probeL, wrapL))
	e.emitLabel(probeL)
	_, boxedErr := e.emitBoxedErrorProbe(pay)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", boxedErr, doneL, wrapL))
	e.emitLabel(wrapL)
	textV, err := e.emitV8ValueText(box)
	if err != nil {
		return Value{}, err
	}
	text := textV.Ref
	m1, err := e.emitStringConcat(Value{Ref: e.internString(unhandledRejectionMsg), Ty: TypePtr}, Value{Ref: text, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	msg, err := e.emitStringConcat(m1, Value{Ref: e.internString("\"."), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	errReg := e.buildErrorObj(errorKindIDs["Error"], msg.Ref, e.internString("UnhandledPromiseRejection"))
	codeGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 3", codeGep, errorObjType.StructIR(), errReg))
	e.heapStore(irPtr, e.internString("ERR_UNHANDLED_REJECTION"), codeGep, 8)
	ep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint "+irPtr+" %s to i64", ep, errReg))
	wrapped := e.emitCaughtAggregate(fmt.Sprint(kmlTagError), ep)
	e.emitInstr(fmt.Sprintf("store { i8, i64 } %s, "+irPtr+" %s, align 8", wrapped.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load { i8, i64 }, "+irPtr+" %s, align 8", out, slot))
	return Value{Ref: out, Ty: TypeCaught}, nil
}

// emitV8ValueText is how V8 names a value in a message (its %ToString-or-
// description): a primitive's text (a symbol's `Symbol(s)`), an array's
// `[object Array]`, any other object's `#<ConstructorName>`.
func (e *Emitter) emitV8ValueText(box Value) (Value, error) {
	btag, _ := e.emitUnboxTagPayload(box)
	// An object (typeof "object" but null, or "function"); a symbol's or
	// bigint's box is a primitive.
	ty, err := e.emitDynamicTypeof(box)
	if err != nil {
		return Value{}, err
	}
	isO, isF, notNull, o2, isObjish := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, %s", isO, ty.Ref, e.internString("object")))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", notNull, box.Ref, nbNull))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", o2, isO, notNull))
	e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, %s", isF, ty.Ref, e.internString("function")))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isObjish, o2, isF))
	textSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", textSlot))
	primL, objL, arrL, ctorL, textL := e.freshLabel("unh.txt.prim"), e.freshLabel("unh.txt.obj"), e.freshLabel("unh.txt.arr"), e.freshLabel("unh.txt.ctor"), e.freshLabel("unh.txt")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObjish, objL, primL))
	e.emitLabel(primL)
	ps, err := e.emitValueToString(box)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", ps.Ref, textSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", textL))
	e.emitLabel(objL)
	isArr, isDynArr, anyArr := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isArr, btag, kmlTagArray))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isDynArr, btag, kmlTagDynArray))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", anyArr, isArr, isDynArr))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", anyArr, arrL, ctorL))
	e.emitLabel(arrL)
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", e.internString("[object Array]"), textSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", textL))
	e.emitLabel(ctorL)
	pos := ast.Pos{}
	ctor, _ := e.emitDynAnyMemberGetNamed(box, e.internString("constructor"), "constructor", pos)
	cname, _ := e.emitDynAnyMemberGetNamed(ctor, e.internString("name"), "name", pos)
	cs, err := e.emitValueToString(cname)
	if err != nil {
		return Value{}, err
	}
	t1, err := e.emitStringConcat(Value{Ref: e.internString("#<"), Ty: TypePtr}, cs)
	if err != nil {
		return Value{}, err
	}
	t2, err := e.emitStringConcat(t1, Value{Ref: e.internString(">"), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", t2.Ref, textSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", textL))
	e.emitLabel(textL)
	text := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", text, textSlot))
	return Value{Ref: text, Ty: TypePtr}, nil
}
