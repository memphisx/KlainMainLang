// emit_promise.go — Promise.all/.race/.allSettled (ADR-00073, TDD-00016).
//
// This compiler has no concept of Promise rejection: an ordinary async
// function's body runs to completion synchronously at call time (see
// emit_async.go's own doc comment), so every Promise<T> a program can hold
// is, by construction, already-fulfilled — except fetch()'s Promise<Response>,
// the one genuinely-pending value (emit_fetch.go/runtime.go's
// __kml_await_fetch). Since this compiler also has no heterogeneous arrays,
// `promises: Array<Promise<T>>` has one concrete T for the whole array,
// known at compile time — every function below branches once on whether T
// is Response (real concurrency: N pending fetches, waited on together via
// runtime.go's group primitives) or anything else (nothing to parallelize:
// every element is already resolved, so the honest behavior is a plain
// sequential collection, not fake parallelism).
//
// Each of the three builtins itself does the real waiting synchronously,
// then wraps its already-resolved result in a Promise using the exact same
// convention emitAsyncPrologue/emitReturn use for an ordinary async
// function — so a later `await Promise.all(...)` reads the result back via
// emitAwait's completely unmodified generic branch.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// promiseArrayElemType validates that expr is an Array<Promise<T>> (the
// single argument every Promise.all/.race/.allSettled call takes) and
// returns T (TypeVoid for Promise<void>, though a void-array-of-promises
// isn't a meaningful call in practice).
func (e *Emitter) promiseArrayElemType(expr ast.Expression, name string, pos ast.Pos) (Type, error) {
	arrTy := e.inferExprType(expr)
	if !arrTy.IsArray || arrTy.ElemType == nil {
		return Type{}, fmt.Errorf("%d:%d: %s takes an array of promises", pos.Line, pos.Col, name)
	}
	elemTy := *arrTy.ElemType
	if !elemTy.IsPromise {
		// Plain values: each is Promise.resolve(value) (combinatorMembers).
		if isUnconstrainedDynamic(elemTy) {
			return TypeAny, nil
		}
		return elemTy, nil
	}
	innerTy := TypeVoid
	if elemTy.PromiseType != nil {
		innerTy = *elemTy.PromiseType
	}
	// inferArrayType keys the element type off the *first* array element. When
	// that first element is a `Promise.reject(...)` its resolved type is `never`
	// (a rejected promise never yields a value), which carries no value-encoding
	// information — a later concrete member's value would then be read back with
	// the wrong shape (e.g. a `double` slot decoded as a raw `i64`). Faithful TS
	// widens `never` out of the union, so scan the literal for the first concrete
	// member and adopt its resolved type instead.
	if innerTy.IsNever || innerTy.IR == "void" {
		if lit, ok := expr.(*ast.ArrayLiteral); ok {
			for _, el := range lit.Elements {
				et := e.inferExprType(el)
				if !et.IsPromise || et.PromiseType == nil {
					continue
				}
				pt := *et.PromiseType
				if !pt.IsNever && pt.IR != "void" {
					innerTy = pt
					break
				}
			}
		}
	}
	return innerTy, nil
}

// promiseArrayIsTask reports whether the argument is an array of task promises
// (from may-suspend async functions, TDD-00083 Stage 2) — as opposed to fetch's
// Promise<Response> or an already-resolved Promise<T>. The tasks are already
// spawned (running concurrently), so a combinator just waits on them and, since
// __kml_task_await_ready drives the whole scheduler, they overlap.
func (e *Emitter) promiseArrayIsTask(expr ast.Expression) bool {
	arrTy := e.inferExprType(expr)
	if !arrTy.IsArray || arrTy.ElemType == nil {
		return false
	}
	el := *arrTy.ElemType
	if el.PromiseTask {
		return true
	}
	// Every non-fetch promise is task-shaped now (TDD-00084 Part A). A bare
	// `Array<Promise<T>>` annotation doesn't carry the PromiseTask tag on its
	// element type, so also treat any non-Response `Promise<T>` element as a task
	// promise — its runtime value is a settled/pending task promise, not the old
	// bare value slot the already-resolved combinator path would misread.
	return el.IsPromise && (el.PromiseType == nil || !el.PromiseType.IsResponse)
}

// emitPromiseLoop emits a Go-side "for i in 0..lenReg" loop (same
// idxAlloca/cond/body/done shape emit_arrays.go's emitArrayMap already
// uses for its own result loop) whose body is supplied by bodyFn(idxVal) —
// called once per iteration. Shared by every branch below that needs to
// visit all n array elements (every one except .race's Response branch,
// which only ever looks at the winning element).
func (e *Emitter) emitPromiseLoop(lenReg string, bodyFn func(idxVal string)) {
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("promiseloop.cond")
	bodyL := e.freshLabel("promiseloop.body")
	doneL := e.freshLabel("promiseloop.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	bodyFn(idxVal)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
}

// mallocArrayBuffer mallocs an n-element output buffer for elemTy, using
// the same elemTy.IR/Align()-per-slot convention every other array HOF
// (map/filter/...) already uses for its own result buffer — required so
// the array this produces reads back correctly through every existing
// array code path (indexing, for...of, .length), which all assume that
// same convention.
func (e *Emitter) mallocArrayBuffer(lenReg string, elemTy Type) string {
	e.ensureMalloc()
	bytesReg := e.freshReg()
	outPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytesReg, lenReg, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", outPtr, bytesReg))
	return outPtr
}

func (e *Emitter) storeArrayElement(outPtr, idxVal, valRef string, elemTy Type) {
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, outPtr, idxVal))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, valRef, gep, elemTy.Align()))
}

// storeArrayElementValue stores a combinator's resolved element into the result
// buffer. An array element (`Promise.all<Promise<T[]>[]>` → T[][]) is a header
// pointer per the reference model (TDD-00213), so box the {ptr,i64} array
// aggregate into a shared header and store that 8-byte pointer — storing the
// 16-byte aggregate into the "ptr"-shaped slot is invalid IR. Every other
// element type stores its plain value.
func (e *Emitter) storeArrayElementValue(outPtr, idxVal string, val Value, elemTy Type) {
	if elemTy.IsArray {
		e.storeArrayElement(outPtr, idxVal, e.arrayReturnHeader(val), elemTy)
		return
	}
	e.storeArrayElement(outPtr, idxVal, val.Ref, elemTy)
}

func (e *Emitter) wrapArrayAggregate(outPtr, lenReg string, elemTy Type) Value {
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, outPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}
}

// emitPromiseResolve implements `Promise.resolve(v)` — a settled (fulfilled)
// task-shaped promise, so `.then`/`.catch`/`.finally`/`await` all work on it. An
// already-promise argument is returned as-is (JS flattens a thenable);
// `Promise.resolve()` with no argument is a fulfilled `Promise<void>`.
func (e *Emitter) emitPromiseResolve(args []ast.Expression, pos ast.Pos, hint Type) (Value, error) {
	e.ensurePromiseRuntime()
	if len(args) == 0 {
		q := e.emitAllocSettledPromise()
		e.emitSetPromiseState(q, 1)
		qt := PromiseOf(TypeVoid)
		qt.PromiseTask = true
		return Value{Ref: q, Ty: qt}, nil
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if val.Ty.IsPromise {
		return val, nil
	}
	// A dynamic value holding a promise resolves as that promise
	// (PromiseResolve); anything else is a fulfilled Promise<any>.
	if isUnconstrainedDynamic(val.Ty) {
		return e.emitAnyToPromiseAny(val), nil
	}
	// Honor a contextual `Promise<any>` hint (e.g. `const p: Promise<any> =
	// Promise.resolve(42)`): the promise's value slot is read back at `.then`/
	// `await` as whatever the promise's *declared* element type is, so a
	// concrete value widened into a `Promise<any>` must be NaN-boxed at the
	// store — otherwise the raw scalar bits (e.g. a double) are later decoded
	// as a box and produce garbage (a string value is unaffected: its box IS
	// the pointer). The async-function return path already boxes to its
	// declared `Promise<any>`; this brings Promise.resolve to parity.
	if hint.IsPromise && hint.PromiseType != nil && hint.PromiseType.IsDynamic && !val.Ty.IsDynamic {
		boxed, berr := e.emitBoxValue(val)
		if berr != nil {
			return Value{}, berr
		}
		val = boxed
	}
	q := e.emitAllocSettledPromise()
	e.storePromiseValue(q, val)
	e.emitSetPromiseState(q, 1)
	qt := PromiseOf(val.Ty)
	qt.PromiseTask = true
	return Value{Ref: q, Ty: qt}, nil
}

// emitPromiseReject implements `Promise.reject(e)` — a settled (rejected)
// task-shaped promise carrying the error, so `.catch`/`await` re-surface it. The
// promise's value type is irrelevant (a rejected promise never produces a value);
// it is typed `Promise<number>` by default, since await re-throws before the value
// type is ever observed.
func (e *Emitter) emitPromiseReject(args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensurePromiseRuntime()
	e.ensureExceptionHelpers()
	// The reason is the real value, carried as a caught-value (tag, payload) —
	// the async twin of `throw v` (TDD-00207). An absent reason is `undefined`,
	// matching Node's `Promise.reject()`.
	var tag, pay string
	if len(args) >= 1 {
		val, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		tag, pay, err = e.emitValueToCaughtParts(val)
		if err != nil {
			return Value{}, err
		}
	} else {
		tag = fmt.Sprintf("%d", kmlTagUndefined)
		pay = "0"
	}
	q := e.emitAllocSettledPromise()
	e.storeRejectReason(q, tag, pay)
	e.ensurePromiseSettle()
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle(ptr %s, i64 2)", q))
	qt := PromiseOf(TypeNever)
	qt.PromiseTask = true
	return Value{Ref: q, Ty: qt}, nil
}

// emitAllocSettledPromise allocates a fresh task promise (pending; the caller sets
// its state). Shared by Promise.resolve/reject.
func (e *Emitter) emitAllocSettledPromise() string {
	q := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", q))
	return q
}

// emitSetPromiseState stores a task promise's resolved-state field (1 fulfilled,
// 2 rejected).
func (e *Emitter) emitSetPromiseState(q string, state int) {
	rp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", rp, promiseStructIR, q))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", state, rp))
}

// buildSettlement mallocs and fills a SettlementType(valueTy) object:
// {status, value, reason}. valueRef/reasonRef are the SSA registers (or
// the literal "null") for whichever of the two doesn't apply to this
// element — this compiler has no optional fields, so both are always
// written, per SettlementType's own doc comment.
func (e *Emitter) buildSettlement(settleTy Type, statusStr, valueRef, reasonRef string) string {
	e.ensureMalloc()
	obj := e.freshReg()
	e.emitObjMallocInto(obj, settleTy)
	structIR := settleTy.StructIR()
	storeField := func(name, ref string) {
		idx, fieldTy, _ := settleTy.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, obj, idx))
		// The absent `reason` of a fulfilled entry passes the literal "null"; the
		// slot is now a NaN-boxed `any` (TDD-00169), so store a boxed `undefined`
		// rather than a null pointer, so JSON omits it and `typeof reason` reads
		// "undefined".
		if fieldTy.IsDynamic && ref == "null" {
			e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, gep))
			return
		}
		if fieldTy.IsArray {
			// Array-typed slot holds a header pointer (TDD-00213 Stage 2). The
			// absent side of a settlement (a rejected result's `value`, a fulfilled
			// result's array `reason`) passes the literal "null" — store a null
			// header pointer directly rather than boxing it as a {ptr,i64}
			// aggregate (`store {ptr,i64} null`, invalid IR).
			if ref == "null" {
				e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", gep))
				return
			}
			e.storeArrayFieldHeader(gep, Value{Ref: ref, Ty: fieldTy})
			return
		}
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(fieldTy), ref, gep, fieldTy.Align()))
	}
	storeField("status", statusStr)
	storeField("value", valueRef)
	storeField("reason", reasonRef)
	return obj
}

// emitPromiseAll / Race / Any / AllSettled implement the four combinators:
// the members become task promises (combinatorMembers), then the spec's
// algorithm runs over them (emit_promise_combinators.go, ADR-01193).
func (e *Emitter) emitPromiseAll(args []ast.Expression, pos ast.Pos) (Value, error) {
	return e.emitCombinator("all", "Promise.all", args, pos)
}

func (e *Emitter) emitPromiseRace(args []ast.Expression, pos ast.Pos) (Value, error) {
	return e.emitCombinator("race", "Promise.race", args, pos)
}

func (e *Emitter) emitPromiseAny(args []ast.Expression, pos ast.Pos) (Value, error) {
	return e.emitCombinator("any", "Promise.any", args, pos)
}

func (e *Emitter) emitPromiseAllSettled(args []ast.Expression, pos ast.Pos) (Value, error) {
	return e.emitCombinator("allSettled", "Promise.allSettled", args, pos)
}

func (e *Emitter) emitCombinator(kind, name string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: %s takes exactly 1 argument", pos.Line, pos.Col, name)
	}
	innerTy, err := e.promiseArrayElemType(args[0], name, pos)
	if err != nil {
		return Value{}, err
	}
	ptrReg, lenReg, _, err := e.resolveArrayForHOF(args[0], pos)
	if err != nil {
		return Value{}, err
	}
	members, innerTy := e.combinatorMembers(args[0], ptrReg, lenReg, innerTy)
	return e.emitAsyncCombinator(kind, members, lenReg, innerTy), nil
}

// combinatorMembers is the list as task promises, and their value type:
// task promises as they are, a raw fetch promise through its bridge, a
// plain value as a fulfilled promise (the spec's PromiseResolve), a value
// held in `any` through Promise.resolve of it.
func (e *Emitter) combinatorMembers(arr ast.Expression, ptrReg, lenReg string, innerTy Type) (string, Type) {
	el := innerTy
	if at := e.inferExprType(arr); at.IsArray && at.ElemType != nil {
		el = *at.ElemType
	}
	switch {
	case !el.IsPromise:
		return e.wrapValueMembers(ptrReg, lenReg, el)
	case e.promiseArrayIsTask(arr):
		return ptrReg, innerTy
	case innerTy.IsResponse:
		return e.bridgeFetchMembers(ptrReg, lenReg), innerTy
	}
	return ptrReg, innerTy
}

// wrapValueMembers is an array of promises fulfilled with each value of
// (ptrReg, lenReg) — or, for `any` values, Promise.resolve of each (a
// promise held in `any` is itself).
func (e *Emitter) wrapValueMembers(ptrReg, lenReg string, el Type) (string, Type) {
	e.ensurePromiseRuntime()
	e.ensureMalloc()
	dyn := isUnconstrainedDynamic(el)
	bytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytes, lenReg))
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", out, bytes))
	e.emitPromiseLoop(lenReg, func(idxVal string) {
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", g, StructFieldIR(el), ptrReg, idxVal))
		var v Value
		if el.IsArray {
			v = e.loadArraySlotAggregate(g, el)
		} else {
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", r, StructFieldIR(el), g, el.Align()))
			v = Value{Ref: r, Ty: el}
		}
		var p string
		if dyn {
			p = e.emitAnyToPromiseAny(v).Ref
		} else {
			p = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", p))
			e.storePromiseValue(p, v)
			e.emitSetPromiseState(p, 1)
		}
		d := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", d, out, idxVal))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", p, d))
	})
	if dyn {
		return out, TypeAny
	}
	return out, el
}

// bridgeFetchMembers is an array of the task promises bridging each raw
// fetch promise of (ptrReg, lenReg) (@__kml_fetch_slot_to_promise).
func (e *Emitter) bridgeFetchMembers(ptrReg, lenReg string) string {
	e.ensureFetchSlotToPromise()
	e.ensureMalloc()
	bytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytes, lenReg))
	arr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", arr, bytes))
	e.emitPromiseLoop(lenReg, func(idxVal string) {
		g, slot := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", g, ptrReg, idxVal))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", slot, g))
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fetch_slot_to_promise(ptr %s)", p, slot))
		d := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", d, arr, idxVal))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", p, d))
	})
	return arr
}
