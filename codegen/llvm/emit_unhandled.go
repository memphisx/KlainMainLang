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
	// Node's --unhandled-rejections mode (NODE_OPTIONS, microtask.c): 0
	// throw, 1 strict, 2 warn, 3 none, 4 warn-with-error-code.
	mode := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_unhandled_mode()", mode))
	hookL, strictL := e.freshLabel("unh.hook"), e.freshLabel("unh.strict")
	isStrict := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 1", isStrict, mode))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isStrict, strictL, hookL))
	retL, warnL, warnCodeL, uncaughtL := e.freshLabel("unh.ret"), e.freshLabel("unh.warn"), e.freshLabel("unh.warncode"), e.freshLabel("unh.uncaught")
	// The process 'unhandledRejection' event, (reason, promise), first in
	// every mode but strict; then, unless a listener took it: throw raises
	// it, warn-with-error-code warns and sets the exit code; warn always
	// warns; none does nothing more.
	e.emitLabel(hookL)
	hasH := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_process_hook_call(i32 2, i64 %s, i64 %s)", hasH, rb.Ref, pb.Ref))
	unheardL := e.freshLabel("unh.unheard")
	e.emitTerminator(fmt.Sprintf("switch i32 %s, label %%%s [ i32 2, label %%%s i32 3, label %%%s ]", mode, unheardL, warnL, retL))
	e.emitLabel(unheardL)
	isCode := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 4", isCode, mode))
	unheardNext := e.freshLabel("unh.unheard.next")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasH, retL, unheardNext))
	e.emitLabel(unheardNext)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isCode, warnCodeL, uncaughtL))
	e.emitLabel(warnCodeL)
	if err := e.emitUnhandledWarning(reason, rb); err != nil {
		panic("unhandled warning: " + err.Error())
	}
	e.emitInstr("call void @__kml_store_exit_code(i64 1)")
	e.emitTerminator(fmt.Sprintf("br label %%%s", retL))
	e.emitLabel(warnL)
	if err := e.emitUnhandledWarning(reason, rb); err != nil {
		panic("unhandled warning: " + err.Error())
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", retL))
	e.emitLabel(retL)
	e.emitTerminator("ret void")
	// strict: the uncaught exception first; when process.on(
	// 'uncaughtException') took it, the 'unhandledRejection' event, and a
	// warning when nothing listens to that.
	pslot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca { i8, i64 }, align 8", pslot))
	e.emitLabel(strictL)
	sReason, err := e.emitUnhandledRejectionError(reason, rb)
	if err != nil {
		sReason = Value{Ref: agg, Ty: TypeCaught}
	}
	stag, spay := e.caughtParts(sReason)
	sHandled := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_process_uncaught_guarded(i8 %s, i64 %s, i1 1)", sHandled, stag, spay))
	sTakenL, printL := e.freshLabel("unh.strict.taken"), e.freshLabel("unh.print")
	e.emitInstr(fmt.Sprintf("store { i8, i64 } %s, "+irPtr+" %s, align 8", sReason.Ref, pslot))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", sHandled, sTakenL, printL))
	e.emitLabel(sTakenL)
	sHasH := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_process_hook_call(i32 2, i64 %s, i64 %s)", sHasH, rb.Ref, pb.Ref))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", sHasH, retL, warnL))
	// throw: an uncaught exception — process.on('uncaughtException') gets
	// the Error (the program goes on), else the reason is printed as an
	// uncaught throw's and the process exits 1.
	e.emitLabel(uncaughtL)
	reason, err = e.emitUnhandledRejectionError(reason, rb)
	if err != nil {
		reason = Value{Ref: agg, Ty: TypeCaught}
	}
	tag, pay := e.caughtParts(reason)
	handled := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_process_uncaught_guarded(i8 %s, i64 %s, i1 1)", handled, tag, pay))
	e.emitInstr(fmt.Sprintf("store { i8, i64 } %s, "+irPtr+" %s, align 8", reason.Ref, pslot))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", handled, retL, printL))
	// Printed as an uncaught throw's (the reason, or its
	// UnhandledPromiseRejection), and the process exits 1. The strict and
	// throw paths meet here with their own exception.
	e.emitLabel(printL)
	pv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load { i8, i64 }, "+irPtr+" %s, align 8", pv, pslot))
	tag, pay = e.caughtParts(Value{Ref: pv, Ty: TypeCaught})
	e.declareWorkerUncaught()
	e.emitInstr(fmt.Sprintf("call void @__kml_worker_uncaught(i8 %s, i64 %s)", tag, pay))
	e.emitInstr(fmt.Sprintf("call void @__kml_print_uncaught(i8 %s, i64 %s)", tag, pay))
	e.emitInstr("call void @__kml_store_exit_code(i64 1)") // process.exitCode, as the 'exit' listeners see it
	e.emitInstr("call void @__kml_run_exit_handlers(i64 1)")
	e.emitInstr(e.exitCall("1"))
	e.emitTerminator("unreachable")
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("void @__kml_unhandled_report("+irPtr+" %p)", body)
}

// emitUnhandledWarning prints Node's unhandled-rejection warning for reason
// (box, its boxed form): an Error's stack, another value's V8 text
// (noSideEffectsToString), then the rejection's notice (microtask.c).
func (e *Emitter) emitUnhandledWarning(reason, box Value) error {
	tag, pay := e.caughtParts(reason)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
	isRaw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isRaw, tag, kmlTagError))
	stackL, objChkL, textL, doneL := e.freshLabel("unhw.stack"), e.freshLabel("unhw.obj"), e.freshLabel("unhw.text"), e.freshLabel("unhw.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isRaw, stackL, objChkL))
	e.emitLabel(objChkL)
	isObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	probeL := e.freshLabel("unhw.probe")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, probeL, textL))
	e.emitLabel(probeL)
	_, boxedErr := e.emitBoxedErrorProbe(pay)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", boxedErr, stackL, textL))
	e.emitLabel(stackL)
	sv, err := e.emitDynAnyMemberGet(box, e.internString("stack"), ast.Pos{})
	if err != nil {
		return err
	}
	st, err := e.emitValueToString(sv)
	if err != nil {
		return err
	}
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", st.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(textL)
	tv, err := e.emitV8ValueText(box)
	if err != nil {
		return err
	}
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", tv.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	text := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", text, slot))
	m, linked := e.libExports["internal_process_warning:emitWarning"]
	if !linked {
		// No process emitter to listen: the default listener's lines.
		e.emitInstr(fmt.Sprintf("call void @__kml_unhandled_warn("+irPtr+" %s)", text))
		return nil
	}
	// process.emitWarning, as Node's emitUnhandledRejectionWarning calls
	// it: the reason as an UnhandledPromiseRejectionWarning, then the
	// notice, each an Error of that name ('warning' listeners see both).
	pos := ast.Pos{}
	tmp := "__kml_unhw_" + e.freshReg()[1:]
	e.define(tmp, Symbol{Ptr: slot, Ty: TypePtr})
	kind := ast.NewStringLiteral("UnhandledPromiseRejectionWarning", pos)
	if _, err := e.emitExpr(ast.NewCallExpression(ast.NewIdentifier(m, pos), []ast.Expression{ast.NewIdentifier(tmp, pos), kind}, pos)); err != nil {
		return err
	}
	id := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_unhandled_id()", id))
	idSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca double, align 8", idSlot))
	idf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", idf, id))
	e.emitInstr(fmt.Sprintf("store double %s, "+irPtr+" %s, align 8", idf, idSlot))
	idName := "__kml_unhid_" + e.freshReg()[1:]
	e.define(idName, Symbol{Ptr: idSlot, Ty: TypeF64})
	msg := ast.NewBinaryExpression("+", ast.NewBinaryExpression("+", ast.NewStringLiteral(unhandledNotice, pos), ast.NewIdentifier(idName, pos), pos), ast.NewStringLiteral(")", pos), pos)
	// (emitWarning(message, type) makes the Error of that name Node's
	// getErrorWithoutStack does.)
	_, err = e.emitExpr(ast.NewCallExpression(ast.NewIdentifier(m, pos), []ast.Expression{msg, kind}, pos))
	return err
}

// unhandledNotice is Node's unhandled-rejection notice, before its id.
const unhandledNotice = "Unhandled promise rejection. This error originated either by throwing inside of an async function without a catch block, or by rejecting a promise which was not handled with .catch(). To terminate the node process on unhandled promise rejection, use the CLI flag `--unhandled-rejections=strict` (see https://nodejs.org/api/cli.html#cli_unhandled_rejections_mode). (rejection id: "

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
