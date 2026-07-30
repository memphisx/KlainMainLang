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
		return Type{}, fmt.Errorf("%d:%d: %s takes an array of promises", pos.Line, pos.Col, name)
	}
	innerTy := TypeVoid
	if elemTy.PromiseType != nil {
		innerTy = *elemTy.PromiseType
	}
	return innerTy, nil
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

func (e *Emitter) wrapArrayAggregate(outPtr, lenReg string, elemTy Type) Value {
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, outPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}
}

// wrapResolvedPromise mallocs a Promise slot for val and stores it — every
// builtin below does its real waiting (if any) synchronously and then
// wraps an already-resolved value in a Promise using the exact same
// convention emitAsyncPrologue/emitReturn use for an ordinary async
// function's return, so a later `await` on the result reads it back via
// emitAwait's generic branch. Uses StructFieldSize/StructFieldIR (not
// Align()/.IR) so an array-typed val (Promise<Array<T>>, .all's/
// .allSettled's own return shape) gets the correct 16-byte {ptr,i64} slot —
// see emit_async.go's ADR-00073 fix this depends on. When val is itself a
// Response (.race's Response branch — the only caller that can hit this;
// .all/.allSettled always wrap an Array, never a bare Response), the
// resulting PromiseType is additionally marked PromiseResolved so
// emitAwait knows this slot holds the finished Response object itself, not
// a still-pending fetch handle to wait on — see PromiseResolved's own doc
// comment in types.go for the corruption this prevents.
func (e *Emitter) wrapResolvedPromise(val Value) Value {
	e.ensureMalloc()
	size := StructFieldSize(val.Ty)
	slotReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", slotReg, size))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d",
		StructFieldIR(val.Ty), val.Ref, slotReg, val.Ty.Align()))
	promiseTy := PromiseOf(val.Ty)
	if val.Ty.IsResponse && promiseTy.PromiseType != nil {
		promiseTy.PromiseType.PromiseResolved = true
	}
	return Value{Ref: slotReg, Ty: promiseTy}
}

// buildSettlement mallocs and fills a SettlementType(valueTy) object:
// {status, value, reason}. valueRef/reasonRef are the SSA registers (or
// the literal "null") for whichever of the two doesn't apply to this
// element — this compiler has no optional fields, so both are always
// written, per SettlementType's own doc comment.
func (e *Emitter) buildSettlement(settleTy Type, statusStr, valueRef, reasonRef string) string {
	e.ensureMalloc()
	obj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", obj, settleTy.StructSize()))
	structIR := settleTy.StructIR()
	storeField := func(name, ref string) {
		idx, fieldTy, _ := settleTy.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, obj, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(fieldTy), ref, gep, fieldTy.Align()))
	}
	storeField("status", statusStr)
	storeField("value", valueRef)
	storeField("reason", reasonRef)
	return obj
}

// buildPendingMembersArr derefs each array element's Promise<Response>
// slot to its pending-fetch handle (same deref emitAwait's IsResponse
// branch already does), collects them into a malloc'd ptr[n] "members"
// array, builds the 24-byte group struct ({ptr membersArr, i64 count, i64
// mode}) around it, and waits on the whole group via
// __kml_await_group_wait before returning — so by the time this returns,
// every member this group cares about (all of them, for mode 0; the first
// one to finish, for mode 1) is done. Each element's own Promise slot
// (distinct from the pending-fetch handle inside it) is freed once its
// handle has been extracted, matching emitAwait's own free-after-read
// convention.
func (e *Emitter) buildPendingMembersArr(ptrReg, lenReg string, mode int64) (membersArr, group string) {
	e.ensurePromiseCombinators()
	e.ensureFree()

	bytesReg := e.freshReg()
	membersArr = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytesReg, lenReg))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", membersArr, bytesReg))

	e.emitPromiseLoop(lenReg, func(idxVal string) {
		slotGep := e.freshReg()
		promiseHandle := e.freshReg()
		pendingPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", slotGep, ptrReg, idxVal))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", promiseHandle, slotGep))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", pendingPtr, promiseHandle))
		e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", promiseHandle))

		memberGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", memberGep, membersArr, idxVal))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", pendingPtr, memberGep))
	})

	group = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", group))
	membersFieldGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, i64, i64 }, ptr %s, i32 0, i32 0", membersFieldGep, group))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", membersArr, membersFieldGep))
	countFieldGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, i64, i64 }, ptr %s, i32 0, i32 1", countFieldGep, group))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenReg, countFieldGep))
	modeFieldGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, i64, i64 }, ptr %s, i32 0, i32 2", modeFieldGep, group))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", mode, modeFieldGep))

	e.emitInstr(fmt.Sprintf("call void @__kml_await_group_wait(ptr %s)", group))
	return membersArr, group
}

// emitPromiseAll implements Promise.all(promises).
func (e *Emitter) emitPromiseAll(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Promise.all takes exactly 1 argument", pos.Line, pos.Col)
	}
	innerTy, err := e.promiseArrayElemType(args[0], "Promise.all", pos)
	if err != nil {
		return Value{}, err
	}
	ptrReg, lenReg, _, err := e.resolveArrayForHOF(args[0], pos)
	if err != nil {
		return Value{}, err
	}

	var outTy Type
	var outPtr string
	if innerTy.IsResponse {
		outTy = ResponseType()
		membersArr, _ := e.buildPendingMembersArr(ptrReg, lenReg, 0)
		outPtr = e.mallocArrayBuffer(lenReg, outTy)
		e.emitPromiseLoop(lenReg, func(idxVal string) {
			memberGep := e.freshReg()
			pendingPtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", memberGep, membersArr, idxVal))
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", pendingPtr, memberGep))
			// Throws (via __kml_pending_finish's own @__kml_throw/unreachable)
			// on the first array-order transport failure it hits — matching
			// real Promise.all rejecting the whole combinator on any member
			// failing, with "first in array order" standing in for "first to
			// be processed" once every member is already known to be settled
			// (see buildPendingMembersArr's own group-wait above).
			raw := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call { i64, ptr } @__kml_pending_finish(ptr %s)", raw, pendingPtr))
			status := e.freshReg()
			body := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, ptr } %s, 0", status, raw))
			e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, ptr } %s, 1", body, raw))
			respVal := e.buildResponseFromStatusBody(status, body)
			e.storeArrayElement(outPtr, idxVal, respVal.Ref, outTy)
		})
	} else {
		// Nothing to parallelize: every element is already resolved by
		// construction (see this file's own header comment) — .all's
		// honest behavior here is a plain, order-preserving collection.
		outTy = innerTy
		outPtr = e.mallocArrayBuffer(lenReg, outTy)
		e.ensureFree()
		e.emitPromiseLoop(lenReg, func(idxVal string) {
			slotGep := e.freshReg()
			promiseHandle := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", slotGep, ptrReg, idxVal))
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", promiseHandle, slotGep))
			valReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d",
				valReg, StructFieldIR(innerTy), promiseHandle, innerTy.Align()))
			e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", promiseHandle))
			e.storeArrayElement(outPtr, idxVal, valReg, outTy)
		})
	}

	resultArr := e.wrapArrayAggregate(outPtr, lenReg, outTy)
	return e.wrapResolvedPromise(resultArr), nil
}

// emitPromiseRace implements Promise.race(promises).
func (e *Emitter) emitPromiseRace(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Promise.race takes exactly 1 argument", pos.Line, pos.Col)
	}
	innerTy, err := e.promiseArrayElemType(args[0], "Promise.race", pos)
	if err != nil {
		return Value{}, err
	}
	ptrReg, lenReg, _, err := e.resolveArrayForHOF(args[0], pos)
	if err != nil {
		return Value{}, err
	}

	if innerTy.IsResponse {
		// Real race: whichever of the N fetches settles first (success or
		// transport failure) decides the result — matching real
		// Promise.race settling to whichever input settles first,
		// regardless of fulfilled/rejected. Note: a runtime-empty array
		// here hangs forever, exactly like real Promise.race([]) never
		// settling — a compile-time-unknowable array length means this
		// can't be rejected any earlier than that (documented in
		// TDD-00016, not silently guarded against).
		membersArr, group := e.buildPendingMembersArr(ptrReg, lenReg, 1)
		winnerIdx := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_first_done_index(ptr %s)", winnerIdx, group))
		winnerGep := e.freshReg()
		winnerPending := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", winnerGep, membersArr, winnerIdx))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", winnerPending, winnerGep))
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call { i64, ptr } @__kml_pending_finish(ptr %s)", raw, winnerPending))
		status := e.freshReg()
		body := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, ptr } %s, 0", status, raw))
		e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, ptr } %s, 1", body, raw))
		respVal := e.buildResponseFromStatusBody(status, body)
		return e.wrapResolvedPromise(respVal), nil
	}

	// Nothing to race: every promise is already resolved by construction
	// (see this file's own header comment) — the first element's value is,
	// honestly, the first one "settled." Documented limitation, not a fake
	// race; matches real Promise.race([]) hanging forever for an
	// (unsupported-here) empty array the same way the Response branch does.
	e.ensureFree()
	firstGep := e.freshReg()
	promiseHandle := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 0", firstGep, ptrReg))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", promiseHandle, firstGep))
	valReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d",
		valReg, StructFieldIR(innerTy), promiseHandle, innerTy.Align()))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", promiseHandle))
	return e.wrapResolvedPromise(Value{Ref: valReg, Ty: innerTy}), nil
}

// emitPromiseAllSettled implements Promise.allSettled(promises).
func (e *Emitter) emitPromiseAllSettled(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Promise.allSettled takes exactly 1 argument", pos.Line, pos.Col)
	}
	innerTy, err := e.promiseArrayElemType(args[0], "Promise.allSettled", pos)
	if err != nil {
		return Value{}, err
	}
	ptrReg, lenReg, _, err := e.resolveArrayForHOF(args[0], pos)
	if err != nil {
		return Value{}, err
	}

	fulfilledStr := e.internString("fulfilled")
	var settleTy Type
	var outPtr string

	if innerTy.IsResponse {
		settleTy = SettlementType(ResponseType())
		rejectedStr := e.internString("rejected")
		membersArr, _ := e.buildPendingMembersArr(ptrReg, lenReg, 0)
		outPtr = e.mallocArrayBuffer(lenReg, settleTy)
		e.emitPromiseLoop(lenReg, func(idxVal string) {
			memberGep := e.freshReg()
			pendingPtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", memberGep, membersArr, idxVal))
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", pendingPtr, memberGep))
			raw := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call { i1, i64, ptr, ptr } @__kml_pending_finish_settled(ptr %s)", raw, pendingPtr))
			failed := e.freshReg()
			status := e.freshReg()
			body := e.freshReg()
			reasonMsg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = extractvalue { i1, i64, ptr, ptr } %s, 0", failed, raw))
			e.emitInstr(fmt.Sprintf("%s = extractvalue { i1, i64, ptr, ptr } %s, 1", status, raw))
			e.emitInstr(fmt.Sprintf("%s = extractvalue { i1, i64, ptr, ptr } %s, 2", body, raw))
			e.emitInstr(fmt.Sprintf("%s = extractvalue { i1, i64, ptr, ptr } %s, 3", reasonMsg, raw))

			okL := e.freshLabel("settled.ok")
			failL := e.freshLabel("settled.fail")
			mergeL := e.freshLabel("settled.merge")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", failed, failL, okL))

			e.emitLabel(okL)
			respVal := e.buildResponseFromStatusBody(status, body)
			settleOk := e.buildSettlement(settleTy, fulfilledStr, respVal.Ref, "null")
			e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

			e.emitLabel(failL)
			errObj := e.buildErrorObj(0, reasonMsg, e.internString("Error"))
			settleFail := e.buildSettlement(settleTy, rejectedStr, "null", errObj)
			e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

			e.emitLabel(mergeL)
			merged := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = phi ptr [ %s, %%%s ], [ %s, %%%s ]", merged, settleOk, okL, settleFail, failL))
			e.storeArrayElement(outPtr, idxVal, merged, settleTy)
		})
	} else {
		// Nothing to parallelize, and nothing that can fail either (see
		// this file's own header comment) — every element is, honestly,
		// always "fulfilled."
		settleTy = SettlementType(innerTy)
		outPtr = e.mallocArrayBuffer(lenReg, settleTy)
		e.ensureFree()
		e.emitPromiseLoop(lenReg, func(idxVal string) {
			slotGep := e.freshReg()
			promiseHandle := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", slotGep, ptrReg, idxVal))
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", promiseHandle, slotGep))
			valReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d",
				valReg, StructFieldIR(innerTy), promiseHandle, innerTy.Align()))
			e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", promiseHandle))
			settled := e.buildSettlement(settleTy, fulfilledStr, valReg, "null")
			e.storeArrayElement(outPtr, idxVal, settled, settleTy)
		})
	}

	resultArr := e.wrapArrayAggregate(outPtr, lenReg, settleTy)
	return e.wrapResolvedPromise(resultArr), nil
}
