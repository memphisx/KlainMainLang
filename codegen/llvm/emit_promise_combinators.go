package llvm

// emit_promise_combinators.go — Promise.all / allSettled / race / any over
// task promises, as the spec runs them (ADR-01193): the combinator returns a
// pending promise at once, attaches one reaction per member, and each
// reaction settles the result when the combinator's rule is met — all: every
// member fulfilled (the first rejection rejects it); allSettled: every member
// settled; race: the first to settle; any: the first to fulfill (every
// rejection rejects it with an AggregateError). Nothing blocks at the call,
// so `.then`/`.catch` chain on the result like on any promise.
//
// The state is a heap block { ptr q, ptr out, i64 remaining, i64 n }; a
// member's reaction env is { ptr state, i64 index, ptr member }.

import (
	"fmt"

	"KlainMainLang/ast"
)

const combStateIR = "{ ptr, ptr, i64, i64 }"

// emitAsyncCombinator builds the combinator's result for members (ptrReg,
// lenReg) of Promise<innerTy>.
func (e *Emitter) emitAsyncCombinator(kind, ptrReg, lenReg string, innerTy Type) Value {
	e.ensurePromiseRuntime()
	e.ensurePromiseSettle()
	e.ensureMicrotasks()
	e.ensureMalloc()
	q := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", q))

	outTy := e.combinatorOutElem(kind, innerTy)
	out := "null"
	if kind != "race" {
		out = e.mallocArrayBuffer(lenReg, outTy)
	}
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 32)", st))
	field := func(i int) string {
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, combStateIR, st, i))
		return g
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", q, field(0)))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", out, field(1)))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenReg, field(2)))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenReg, field(3)))

	// An empty list settles at once: all/allSettled fulfil with [], any
	// rejects with an AggregateError, race stays pending.
	if kind != "race" {
		empty := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", empty, lenReg))
		emptyL, goL := e.freshLabel("comb.empty"), e.freshLabel("comb.members")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", empty, emptyL, goL))
		e.emitLabel(emptyL)
		e.combinatorFinish(kind, q, out, "0", innerTy)
		e.emitTerminator(fmt.Sprintf("br label %%%s", goL))
		e.emitLabel(goL)
	}

	runner := e.emitCombinatorRunner(kind, innerTy)
	e.emitPromiseLoop(lenReg, func(idxVal string) {
		g, m := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", g, ptrReg, idxVal))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", m, g))
		env := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", env))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", st, env))
		ip := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", ip, env))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxVal, ip))
		mp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", mp, env))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", m, mp))
		clo := e.buildBuiltinClosure(runner, env)
		e.emitAttachPromiseReaction(m, clo)
	})
	qt := PromiseOf(e.combinatorResultType(kind, innerTy))
	qt.PromiseTask = true
	return Value{Ref: q, Ty: qt}
}

// combinatorOutElem is the element type of the combinator's collected
// array: the values (all), the settlement records (allSettled), the
// rejection reasons (any).
func (e *Emitter) combinatorOutElem(kind string, innerTy Type) Type {
	switch kind {
	case "all":
		if isVoidTy(innerTy) {
			return TypeAny // each Promise<void> fulfils with undefined
		}
		return innerTy
	case "allSettled":
		if isVoidTy(innerTy) {
			return SettlementType(TypeAny)
		}
		return SettlementType(innerTy)
	case "any":
		return TypePtr
	}
	return innerTy
}

// combinatorResultType is the combinator's promise value type.
func (e *Emitter) combinatorResultType(kind string, innerTy Type) Type {
	switch kind {
	case "all":
		return ArrayOf(e.combinatorOutElem(kind, innerTy))
	case "allSettled":
		return ArrayOf(e.combinatorOutElem(kind, innerTy))
	}
	return innerTy
}

// combinatorFinish settles q when every member has been seen: all and
// allSettled fulfil with the collected array; any rejects with an
// AggregateError of the reasons.
func (e *Emitter) combinatorFinish(kind, q, out, n string, innerTy Type) {
	switch kind {
	case "all", "allSettled":
		arr := e.wrapArrayAggregate(out, n, e.combinatorOutElem(kind, innerTy))
		e.storePromiseValue(q, arr)
		e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle(ptr %s, i64 1)", q))
	case "any":
		e.ensureExceptionHelpers()
		agg := e.buildAggregateErrorObj(e.internString("All promises were rejected"), e.internString("AggregateError"), out, n)
		bits := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", bits, agg))
		e.emitRejectPromise(q, fmt.Sprintf("%d", kmlTagError), false, bits)
	}
}

// emitCombinatorRunner generates the member reaction `void runner(ptr env)`
// for kind over Promise<innerTy> members.
func (e *Emitter) emitCombinatorRunner(kind string, innerTy Type) string {
	e.combinatorCtr++
	name := fmt.Sprintf("@__kml_comb_%s_%d", kind, e.combinatorCtr)
	restore := e.beginDetachedFunc()
	st, idx, m := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %%env, align 8", st))
	ip, mp := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %%env, i64 8", ip))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idx, ip))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %%env, i64 16", mp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", m, mp))
	field := func(i int) string {
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, combStateIR, st, i))
		return g
	}
	q, out, n := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", q, field(0)))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", out, field(1)))
	remP := field(2)
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", n, field(3)))
	// A result already decided ignores later members.
	qsP, qs := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", qsP, promiseStructIR, q))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", qs, qsP))
	decided := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", decided, qs))
	retL, liveL := e.freshLabel("comb.ret"), e.freshLabel("comb.live")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", decided, retL, liveL))
	e.emitLabel(liveL)
	msP, ms := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", msP, promiseStructIR, m))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", ms, msP))
	rej := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 2", rej, ms))
	fulL, rejL := e.freshLabel("comb.fulfilled"), e.freshLabel("comb.rejected")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", rej, rejL, fulL))

	// countDown decrements the remaining members and finishes at zero.
	countDown := func() {
		r, r1 := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, remP))
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", r1, r))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r1, remP))
		zero := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", zero, r1))
		finL := e.freshLabel("comb.finish")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", zero, finL, retL))
		e.emitLabel(finL)
		e.combinatorFinish(kind, q, out, n, innerTy)
		e.emitTerminator(fmt.Sprintf("br label %%%s", retL))
	}
	// rejectWith copies the member's reason into q and rejects it.
	rejectWith := func() {
		e.emitInstr(fmt.Sprintf("call void @__kml_promise_reject_from(ptr %s, ptr %s)", q, m))
		e.emitTerminator(fmt.Sprintf("br label %%%s", retL))
	}
	resolveWith := func() {
		val := e.loadPromiseValue(m, innerTy)
		e.storePromiseValue(q, val)
		e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle(ptr %s, i64 1)", q))
		e.emitTerminator(fmt.Sprintf("br label %%%s", retL))
	}

	switch kind {
	case "all":
		e.emitLabel(fulL)
		if isVoidTy(innerTy) {
			e.storeArrayElementValue(out, idx, Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, TypeAny)
		} else {
			e.storeArrayElementValue(out, idx, e.loadPromiseValue(m, innerTy), innerTy)
		}
		countDown()
		e.emitLabel(rejL)
		rejectWith()
	case "allSettled":
		valTy := innerTy
		if isVoidTy(innerTy) {
			valTy = TypeAny // a Promise<void> fulfils with undefined
		}
		settleTy := SettlementType(valTy)
		e.emitLabel(fulL)
		val := Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}
		if !isVoidTy(innerTy) {
			val = e.loadPromiseValue(m, innerTy)
		}
		ok := e.buildSettlement(settleTy, e.internString("fulfilled"), val.Ref, "null")
		e.storeArrayElement(out, idx, ok, settleTy)
		countDown()
		e.emitLabel(rejL)
		reason := e.emitCaughtToAny(e.loadRejectReasonCaught(m))
		absent := valTy.zeroLiteral()
		if valTy.IsDynamic {
			absent = fmt.Sprint(nbUndefined)
		}
		bad := e.buildSettlement(settleTy, e.internString("rejected"), absent, reason.Ref)
		e.storeArrayElement(out, idx, bad, settleTy)
		countDown()
	case "race":
		e.emitLabel(fulL)
		resolveWith()
		e.emitLabel(rejL)
		rejectWith()
	case "any":
		e.emitLabel(fulL)
		resolveWith()
		e.emitLabel(rejL)
		rp, rv, rptr := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", rp, promiseStructIR, m))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", rv, rp))
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", rptr, rv))
		e.storeArrayElement(out, idx, rptr, TypePtr)
		countDown()
	}
	e.emitLabel(retL)
	e.emitTerminator("ret void")
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine internal void %s(ptr %%env) {\nentry:\n%s}\n", name, body))
	return name
}

// The Promise statics, reached through their `@intrinsic` declarations.
func init() {
	for name, emit := range map[string]func(e *Emitter, args []ast.Expression, pos ast.Pos) (Value, error){
		"all":        (*Emitter).emitPromiseAll,
		"race":       (*Emitter).emitPromiseRace,
		"allSettled": (*Emitter).emitPromiseAllSettled,
		"any":        (*Emitter).emitPromiseAny,
		"reject":     (*Emitter).emitPromiseReject,
		"resolve": func(e *Emitter, args []ast.Expression, pos ast.Pos) (Value, error) {
			return e.emitPromiseResolve(args, pos, Type{})
		},
	} {
		name, emit := name, emit
		intrinsics["Promise."+name] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) { return emit(e, ex.Args, ex.GetPos()) },
			ty: func(e *Emitter, ex *ast.CallExpression) Type {
				t, _ := e.promiseStaticType(name, ex)
				return t
			},
		}
	}
}

// isVoidTy reports a void (or never) value type: a Promise<void>'s.
func isVoidTy(t Type) bool { return t.IR == "void" || t.IR == "" }
