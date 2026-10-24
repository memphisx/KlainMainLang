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

	c := combinators[kind]
	outTy := e.combinatorOutElem(kind, innerTy)
	out := "null"
	if c.finish != nil {
		out = e.mallocArrayBuffer(lenReg, outTy)
	}
	st := e.freshReg()
	e.heapMallocInto(st, "32")
	field := func(i int) string {
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, combStateIR, st, i))
		return g
	}
	e.heapStore("ptr", q, field(0), 8)
	e.heapStore("ptr", out, field(1), 8)
	e.heapStore("i64", lenReg, field(2), 8)
	e.heapStore("i64", lenReg, field(3), 8)

	// An empty list settles at once: all/allSettled fulfil with [], any
	// rejects with an AggregateError, race stays pending.
	if c.finish != nil {
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
		e.heapMallocInto(env, "24")
		e.heapStore("ptr", st, env, 8)
		ip := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", ip, env))
		e.heapStore("i64", idxVal, ip, 8)
		mp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", mp, env))
		e.heapStore("ptr", m, mp, 8)
		clo := e.buildBuiltinClosure(runner, env)
		e.emitAttachPromiseReaction(m, clo)
	})
	qt := PromiseOf(e.combinatorResultType(kind, innerTy))
	qt.PromiseTask = true
	return Value{Ref: q, Ty: qt}
}

// combinator is one of the Promise combinators' rules.
//   - outElem: the element type of its collected array — the values (all),
//     the settlement records (allSettled), the rejection reasons (any); nil
//     for race, which collects nothing.
//   - collects: its result is that array (all, allSettled).
//   - finish settles q when every member has been seen.
//   - fulfilled and rejected are a member's reaction, at its label.
type combinator struct {
	outElem   func(innerTy Type) Type
	collects  bool
	finish    func(e *Emitter, q, out, n string, innerTy Type)
	fulfilled func(e *Emitter, r *combRun)
	rejected  func(e *Emitter, r *combRun)
}

// combRun is a member reaction being generated: the result q, the
// collected array, the member's index and promise, and the three ways the
// reaction ends.
type combRun struct {
	out, idx, m string
	innerTy     Type
	countDown   func()
	rejectWith  func()
	resolveWith func()
}

// combinators are Promise.all, allSettled, race and any (ADR-01193).
var combinators map[string]*combinator

// fulfilWithArray settles q with the collected array.
func fulfilWithArray(e *Emitter, q, out, n string, elem Type) {
	arr := e.wrapArrayAggregate(out, n, elem)
	e.storePromiseValue(q, arr)
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle(ptr %s, i64 1)", q))
}

func init() {
	voidAsAny := func(t Type) Type {
		if isVoidTy(t) {
			return TypeAny // each Promise<void> fulfils with undefined
		}
		return t
	}
	combinators = map[string]*combinator{
		"all": {
			outElem:  voidAsAny,
			collects: true,
			finish: func(e *Emitter, q, out, n string, innerTy Type) {
				fulfilWithArray(e, q, out, n, voidAsAny(innerTy))
			},
			fulfilled: func(e *Emitter, r *combRun) {
				if isVoidTy(r.innerTy) {
					e.storeArrayElementValue(r.out, r.idx, Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, TypeAny)
				} else {
					e.storeArrayElementValue(r.out, r.idx, e.loadPromiseValue(r.m, r.innerTy), r.innerTy)
				}
				r.countDown()
			},
			rejected: func(e *Emitter, r *combRun) { r.rejectWith() },
		},
		"allSettled": {
			outElem:  func(t Type) Type { return SettlementType(voidAsAny(t)) },
			collects: true,
			finish: func(e *Emitter, q, out, n string, innerTy Type) {
				fulfilWithArray(e, q, out, n, SettlementType(voidAsAny(innerTy)))
			},
			fulfilled: func(e *Emitter, r *combRun) {
				settleTy := SettlementType(voidAsAny(r.innerTy))
				val := Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}
				if !isVoidTy(r.innerTy) {
					val = e.loadPromiseValue(r.m, r.innerTy)
				}
				ok := e.buildSettlement(settleTy, e.internString("fulfilled"), val.Ref, "null")
				e.storeArrayElement(r.out, r.idx, ok, settleTy)
				r.countDown()
			},
			rejected: func(e *Emitter, r *combRun) {
				valTy := voidAsAny(r.innerTy)
				settleTy := SettlementType(valTy)
				reason := e.emitCaughtToAny(e.loadRejectReasonCaught(r.m))
				absent := valTy.zeroLiteral()
				if valTy.IsDynamic() {
					absent = fmt.Sprint(nbUndefined)
				}
				bad := e.buildSettlement(settleTy, e.internString("rejected"), absent, reason.Ref)
				e.storeArrayElement(r.out, r.idx, bad, settleTy)
				r.countDown()
			},
		},
		"race": {
			fulfilled: func(e *Emitter, r *combRun) { r.resolveWith() },
			rejected:  func(e *Emitter, r *combRun) { r.rejectWith() },
		},
		"any": {
			outElem: func(Type) Type { return TypeAny }, // the rejection reasons, boxed
			finish: func(e *Emitter, q, out, n string, innerTy Type) {
				e.ensureExceptionHelpers()
				agg := e.buildAggregateErrorObj(e.internString("All promises were rejected"), e.internString("AggregateError"), out, n)
				bits := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", bits, agg))
				e.emitRejectPromise(q, fmt.Sprintf("%d", kmlTagError), false, bits)
			},
			fulfilled: func(e *Emitter, r *combRun) { r.resolveWith() },
			rejected: func(e *Emitter, r *combRun) {
				// The reason, boxed: AggregateError's `errors` is any[].
				reason := e.emitCaughtToAny(e.loadRejectReasonCaught(r.m))
				e.storeArrayElement(r.out, r.idx, reason.Ref, TypeAny)
				r.countDown()
			},
		},
	}
}

// combinatorOutElem is the element type of kind's collected array.
func (e *Emitter) combinatorOutElem(kind string, innerTy Type) Type {
	if c := combinators[kind]; c.outElem != nil {
		return c.outElem(innerTy)
	}
	return innerTy
}

// combinatorResultType is the combinator's promise value type.
func (e *Emitter) combinatorResultType(kind string, innerTy Type) Type {
	if combinators[kind].collects {
		return ArrayOf(e.combinatorOutElem(kind, innerTy))
	}
	return innerTy
}

// combinatorFinish settles q when every member has been seen.
func (e *Emitter) combinatorFinish(kind, q, out, n string, innerTy Type) {
	combinators[kind].finish(e, q, out, n, innerTy)
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
		e.heapStore("i64", r1, remP, 8)
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

	c := combinators[kind]
	r := &combRun{out: out, idx: idx, m: m, innerTy: innerTy, countDown: countDown, rejectWith: rejectWith, resolveWith: resolveWith}
	e.emitLabel(fulL)
	c.fulfilled(e, r)
	e.emitLabel(rejL)
	c.rejected(e, r)
	e.emitLabel(retL)
	e.emitTerminator("ret void")
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc(fmt.Sprintf("internal void %s(ptr %%env)", name), body)
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
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				args := ex.Args
				if name != "resolve" && name != "reject" && len(args) == 1 {
					// A combinator iterates its argument (array_iter_patch.go).
					if a, ok := e.patchedIterInit(args[0]); ok {
						args = []ast.Expression{a}
					}
				}
				return emit(e, args, ex.GetPos())
			},
			ty: func(e *Emitter, ex *ast.CallExpression) Type {
				t, _ := e.promiseStaticType(name, ex)
				return t
			},
		}
	}
}

// isVoidTy reports a void (or never) value type: a Promise<void>'s.
func isVoidTy(t Type) bool { return t.IR == "void" || t.IR == "" }
