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
	"strings"
)

// promiseArrayElemType validates that expr is an Array<Promise<T>> (the
// single argument every Promise.all/.race/.allSettled call takes) and
// returns T (TypeVoid for Promise<void>, though a void-array-of-promises
// isn't a meaningful call in practice).
func (e *Emitter) promiseArrayElemType(expr ast.Expression, name string, pos ast.Pos) (Type, error) {
	arrTy := e.typeOfExpr(expr)
	if !arrTy.IsArray() || arrTy.ElemType == nil {
		return Type{}, internalAt(pos, "%s over a non-array (emitCombinatorOverIterable)", name)
	}
	elemTy := *arrTy.ElemType
	if !elemTy.IsPromise() {
		// Plain values: each is Promise.resolve(value) (combinatorMembers).
		if isUnconstrainedDynamic(elemTy) {
			return TypeAny, nil
		}
		// A union with promise members (`[Promise.resolve("a"), 2]`): each
		// member awaited, as TypeScript's Awaited<T> over the union.
		if elemTy.IsDynamic() && len(elemTy.UnionMembers) > 0 {
			return e.awaitedUnion(elemTy), nil
		}
		if sig, ok := e.thenableClass(elemTy); ok {
			return thenableValueType(sig), nil // adopted through its `then`
		}
		if vt, ok := e.promiseSubclassValue(elemTy); ok {
			return vt, nil // a Promise subclass's instance: its value, awaited
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
	if innerTy.IsNever() || innerTy.IR == "void" {
		if lit, ok := expr.(*ast.ArrayLiteral); ok {
			for _, el := range lit.Elements {
				et := e.typeOfExpr(el)
				if !et.IsPromise() || et.PromiseType == nil {
					continue
				}
				pt := *et.PromiseType
				if !pt.IsNever() && pt.IR != "void" {
					innerTy = pt
					break
				}
			}
		}
	}
	return innerTy, nil
}

// awaitedUnion is a union's members each awaited (a promise member its
// value), deduplicated: the one left, else their union.
func (e *Emitter) awaitedUnion(u Type) Type {
	var ms []Type
	seen := map[string]bool{}
	for _, m := range u.UnionMembers {
		if vt, ok := e.promiseSubclassValue(m); ok {
			m = vt // a Promise subclass's instance, awaited
		}
		if m.IsPromise() {
			if m.PromiseType == nil || m.PromiseType.IR == "void" {
				m = TypeUndefined
			} else if m.PromiseType.IsNever() {
				continue // a rejected member yields no value
			} else {
				m = *m.PromiseType
			}
		}
		if isUnconstrainedDynamic(m) {
			return TypeAny
		}
		if j, ok := sameButAbsence(ms, m); ok {
			ms[j].Absent = joinAbsence(ms[j].Absent, m.Absent) // `T | null` and `T | undefined`
			continue
		}
		if !seen[reprKey(m)] {
			seen[reprKey(m)] = true
			ms = append(ms, m)
		}
	}
	switch len(ms) {
	case 0:
		return TypeAny
	case 1:
		return ms[0]
	}
	out := u
	out.UnionMembers = ms
	return out
}

// promiseArrayIsTask reports whether the argument is an array of task promises
// (from may-suspend async functions, TDD-00083 Stage 2) — as opposed to fetch's
// Promise<Response> or an already-resolved Promise<T>. The tasks are already
// spawned (running concurrently), so a combinator just waits on them and, since
// __kml_task_await_ready drives the whole scheduler, they overlap.
func (e *Emitter) promiseArrayIsTask(expr ast.Expression) bool {
	arrTy := e.typeOfExpr(expr)
	if !arrTy.IsArray() || arrTy.ElemType == nil {
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
	return el.IsPromise() && (el.PromiseType == nil || !el.PromiseType.IsResponse())
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
	e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", idxAlloca))

	condL := e.freshLabel("promiseloop.cond")
	bodyL := e.freshLabel("promiseloop.body")
	doneL := e.freshLabel("promiseloop.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	bodyFn(idxVal)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", idxNext, idxAlloca))
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
	bytesReg := e.freshReg()
	outPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytesReg, lenReg, elemTy.Align()))
	e.heapMallocInto(outPtr, elemAllocKind(elemTy), bytesReg)
	return outPtr
}

func (e *Emitter) storeArrayElement(outPtr, idxVal, valRef string, elemTy Type) {
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %s", gep, elemTy.IR, outPtr, idxVal))
	e.heapStore(elemTy.IR, valRef, gep, elemTy.Align())
}

// storeArrayElementValue stores a combinator's resolved element into the result
// buffer. An array element (`Promise.all<Promise<T[]>[]>` → T[][]) is a header
// pointer per the reference model (TDD-00213), so store the array's 8-byte
// header pointer. Every other
// element type stores its plain value.
func (e *Emitter) storeArrayElementValue(outPtr, idxVal string, val Value, elemTy Type) {
	// A nullable scalar element is stored as the array stores it: boxed
	// (arrayElemType).
	if isNullableScalar(elemTy) {
		st := arrayElemType(elemTy)
		e.storeArrayElement(outPtr, idxVal, e.convert(val, st).Ref, st)
		return
	}
	if elemTy.IsArray() {
		e.storeArrayElement(outPtr, idxVal, e.arrayReturnHeader(val), elemTy)
		return
	}
	e.storeArrayElement(outPtr, idxVal, val.Ref, elemTy)
}

func (e *Emitter) wrapArrayAggregate(outPtr, lenReg string, elemTy Type) Value {
	r1 := e.newArrayHeader(outPtr, lenReg)
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}
}

// emitPromiseResolve implements `Promise.resolve(v)` — a settled (fulfilled)
// task-shaped promise, so `.then`/`.catch`/`.finally`/`await` all work on it. An
// already-promise argument is returned as-is (JS flattens a thenable);
// `Promise.resolve()` with no argument is a fulfilled `Promise<void>`.
func (e *Emitter) emitPromiseResolve(args []ast.Expression, pos ast.Pos, hint Type) (Value, error) {
	e.ensureExceptionHelpers()
	e.ensureMicrotasks()
	e.ensureUnhandledRejections()
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
	if val.Ty.IR == "void" || val.Ty.IR == "" {
		// `Promise.resolve(f())` of a call returning nothing: evaluated, then
		// fulfilled with undefined, as Promise.resolve() is.
		return e.emitPromiseResolve(nil, pos, hint)
	}
	if val.Ty.IsPromise() {
		return val, nil
	}
	// A thenable class instance is adopted through its `then`.
	if sig, ok := e.thenableClass(val.Ty); ok {
		return e.emitThenableToPromise(val, sig, pos)
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
	if hint.IsPromise() && hint.PromiseType != nil && hint.PromiseType.IsDynamic() && !val.Ty.IsDynamic() {
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
	e.ensureExceptionHelpers()
	e.ensureMicrotasks()
	e.ensureUnhandledRejections()
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
	e.emitRejectPromise(q, tag, true, pay)
	qt := PromiseOf(TypeNever)
	qt.PromiseTask = true
	return Value{Ref: q, Ty: qt}, nil
}

// emitAllocSettledPromise allocates a fresh task promise (pending; the caller sets
// its state). Shared by Promise.resolve/reject.
func (e *Emitter) emitAllocSettledPromise() string {
	q := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_task_alloc_promise()", q))
	return q
}

// emitSetPromiseState stores a task promise's resolved-state field (1 fulfilled,
// 2 rejected).
func (e *Emitter) emitSetPromiseState(q string, state int) {
	rp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 0", rp, promiseStructIR, q))
	e.heapStore("i64", fmt.Sprint(state), rp, 8)
}

// buildSettlement mallocs and fills a SettlementType(valueTy) object:
// {status, value, reason}. valueRef/reasonRef are the SSA registers (or
// the literal "null") for whichever of the two doesn't apply to this
// element — this compiler has no optional fields, so both are always
// written, per SettlementType's own doc comment.
func (e *Emitter) buildSettlement(settleTy Type, statusStr, valueRef, reasonRef string) string {
	// A settlement reaches the checker's union view of it (a structural
	// slot whose layout is not this one): its layout has a row.
	e.noteBoxedLayout(settleTy)
	obj := e.freshReg()
	e.emitObjMallocInto(obj, settleTy)
	structIR := settleTy.StructIR()
	storeField := func(name, ref string) {
		idx, fieldTy, _ := settleTy.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gep, structIR, obj, idx))
		// The absent `reason` of a fulfilled entry passes the literal "null"; the
		// slot is now a NaN-boxed `any` (TDD-00169), so store a boxed `undefined`
		// rather than a null pointer, so JSON omits it and `typeof reason` reads
		// "undefined".
		if fieldTy.IsDynamic() && ref == "null" {
			e.heapStore("i64", fmt.Sprint(nbUndefined), gep, 8)
			return
		}
		if fieldTy.IsArray() {
			// Array-typed slot holds a header pointer (TDD-00213 Stage 2). The
			// absent side of a settlement (a rejected result's `value`, a fulfilled
			// result's array `reason`) passes the literal "null" — store a null
			// header pointer directly.
			if ref == "null" {
				e.heapStore(irPtr, "null", gep, 8)
				return
			}
			e.storeArrayFieldHeader(gep, Value{Ref: ref, Ty: fieldTy})
			return
		}
		if ref == "null" && isNullableScalar(fieldTy) {
			// A nullable scalar's absent side is its undefined aggregate.
			ref = e.makeNullableScalarAgg(fieldTy, "false", zeroRef(fieldTy.withoutNullable()))
		}
		e.heapStore(StructFieldIR(fieldTy), ref, gep, fieldTy.Align())
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

var combinatorLibExport = map[string]string{
	"all": "_kmlPromiseAllOf", "allSettled": "_kmlPromiseAllSettledOf", "any": "_kmlPromiseAnyOf", "race": "_kmlPromiseRaceOf",
}

// firstArg is args[0], or nil when there is none.
func firstArg(args []ast.Expression) ast.Expression {
	if len(args) == 0 {
		return nil
	}
	return args[0]
}

func (e *Emitter) emitCombinator(kind, name string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if nl, isNull := stripAsAny(firstArg(args)).(*ast.NullLiteral); len(args) == 0 || isNull {
		// No iterable: the promise it returns rejects.
		what := "undefined"
		if isNull && !nl.IsUndefined {
			what = "object null"
		}
		reject := ast.NewMemberExpression(e.builtinIdent("Promise", pos), "reject", pos)
		err := ast.NewNewErrorExpression("TypeError", ast.NewStringLiteral(what+" is not iterable (cannot read property Symbol(Symbol.iterator))", pos), pos)
		return e.emitExpr(ast.NewCallExpression(reject, []ast.Expression{err}, pos))
	}
	if len(args) != 1 {
		return Value{}, internalAt(pos, "%s arity", name)
	}
	// Any other iterable (a Set, a generator, a dynamic value) is read
	// through the iteration protocol first, as `[...iterable]`.
	if at := e.typeOfExpr(args[0]); !at.IsArray() || at.ElemType == nil {
		return e.emitCombinatorOverIterable(kind, name, args[0], pos)
	}
	innerTy, err := e.promiseArrayElemType(args[0], name, pos)
	if err != nil {
		return Value{}, err
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(args[0], pos)
	if err != nil {
		return Value{}, err
	}
	members, innerTy := e.combinatorMembers(args[0], ptrReg, lenReg, innerTy, elemTy)
	return e.emitAsyncCombinator(kind, members, lenReg, innerTy), nil
}

// combinatorMembers is the list as task promises, and their value type:
// task promises as they are, a raw fetch promise through its bridge, a
// plain value as a fulfilled promise (the spec's PromiseResolve), a value
// held in `any` through Promise.resolve of it.
// elemTy is how the emitted array holds its elements.
func (e *Emitter) combinatorMembers(arr ast.Expression, ptrReg, lenReg string, innerTy, elemTy Type) (string, Type) {
	el := innerTy
	if at := e.typeOfExpr(arr); at.IsArray() && at.ElemType != nil {
		el = *at.ElemType
	} else if elemTy.IR != "" {
		// Another iterable (a Set, an iterator): its members as drained.
		el = elemTy
	}
	// `{}[]` is `any[]` to the checker, while a literal of empty objects
	// holds pointers: the members are read as the array holds them.
	if el.IsDynamic() && elemTy.IR != "" && !elemTy.IsDynamic() {
		el = elemTy
	}
	switch {
	case !el.IsPromise():
		return e.wrapValueMembers(ptrReg, lenReg, el)
	case e.promiseArrayIsTask(arr):
		return ptrReg, innerTy
	case innerTy.IsResponse():
		return e.bridgeFetchMembers(ptrReg, lenReg), innerTy
	}
	return ptrReg, innerTy
}

// wrapValueMembers is an array of promises fulfilled with each value of
// (ptrReg, lenReg) — or, for `any` values, Promise.resolve of each (a
// promise held in `any` is itself).
func (e *Emitter) wrapValueMembers(ptrReg, lenReg string, el Type) (string, Type) {
	e.ensureExceptionHelpers()
	e.ensureMicrotasks()
	e.ensureUnhandledRejections()
	// A boxed member (`any`, or a union, which may hold a promise or a
	// thenable) resolves at run time.
	dyn := el.IsDynamic() && !el.IsArray()
	sig, thenable := e.thenableClass(el)
	bytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytes, lenReg))
	out := e.freshReg()
	e.heapMallocInto(out, allocElems, bytes)
	e.emitPromiseLoop(lenReg, func(idxVal string) {
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %s", g, StructFieldIR(el), ptrReg, idxVal))
		var v Value
		if el.IsArray() {
			v = e.loadArraySlotAggregate(g, el)
		} else {
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", r, StructFieldIR(el), g, el.Align()))
			v = Value{Ref: r, Ty: el}
		}
		var p string
		if dyn {
			p = e.emitAnyToPromiseAny(v).Ref
		} else if thenable {
			// A thenable class instance, adopted through its `then`.
			pv, _ := e.emitThenableToPromise(v, sig, ast.Pos{})
			p = pv.Ref
		} else {
			p = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_task_alloc_promise()", p))
			e.storePromiseValue(p, v)
			e.emitSetPromiseState(p, 1)
		}
		d := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr "+irPtr+", "+irPtr+" %s, i64 %s", d, out, idxVal))
		e.heapStore(irPtr, p, d, 8)
	})
	if dyn {
		if isUnconstrainedDynamic(el) {
			return out, TypeAny
		}
		return out, el
	}
	if thenable {
		return out, thenableValueType(sig)
	}
	return out, el
}

// bridgeFetchMembers is an array of the task promises bridging each raw
// fetch promise of (ptrReg, lenReg) (@__kml_fetch_slot_to_promise).
func (e *Emitter) bridgeFetchMembers(ptrReg, lenReg string) string {
	e.ensureFetchAsync()
	e.ensureMicrotasks()
	e.ensureExceptionHelpers()
	e.ensureUnhandledRejections()
	e.ensureNanBox()
	e.ensureTaskRuntime()
	bytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytes, lenReg))
	arr := e.freshReg()
	e.heapMallocInto(arr, allocElems, bytes)
	e.emitPromiseLoop(lenReg, func(idxVal string) {
		g, slot := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr "+irPtr+", "+irPtr+" %s, i64 %s", g, ptrReg, idxVal))
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", slot, g))
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_fetch_slot_to_promise("+irPtr+" %s)", p, slot))
		d := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr "+irPtr+", "+irPtr+" %s, i64 %s", d, arr, idxVal))
		e.heapStore(irPtr, p, d, 8)
	})
	return arr
}

// promiseSubclassValue is the value an instance of a class extending
// Promise settles with: its own carried type, else its base's `Promise<T>`.
func (e *Emitter) promiseSubclassValue(t Type) (Type, bool) {
	if !t.IsClass() || t.Nullable() {
		return Type{}, false
	}
	info, ok := e.classes[t.ClassName]
	if !ok || info.HostBase == nil || !info.HostBase.IsPromise() {
		return Type{}, false
	}
	if t.PromiseType != nil {
		return *t.PromiseType, true
	}
	if info.HostBase.PromiseType != nil {
		return *info.HostBase.PromiseType, true
	}
	return TypeAny, true
}

// sameButAbsence finds the member of ms that is a nullable scalar of m's own
// payload type with different absent states.
func sameButAbsence(ms []Type, m Type) (int, bool) {
	if !isNullableScalar(m) {
		return 0, false
	}
	for j, x := range ms {
		if isNullableScalar(x) && x.Absent != m.Absent && reprKey(x.withoutNullable()) == reprKey(m.withoutNullable()) {
			return j, true
		}
	}
	return 0, false
}

// joinAbsence is the absent states of either.
func joinAbsence(a, b absence) absence {
	switch {
	case a == b:
		return a
	case a == absNone:
		return b
	case b == absNone:
		return a
	}
	return absBoth
}

// emitCombinatorOverIterable is a combinator over a value no array type
// describes: the value evaluated, then — inside the combinator, where an
// abrupt completion rejects the promise it returns — its iterator read
// (GetIterator's TypeError for a value without one) and its members
// collected as `[...iterable]`.
func (e *Emitter) emitCombinatorOverIterable(kind, name string, arg ast.Expression, pos ast.Pos) (Value, error) {
	// An untyped iterable runs the specification's steps in the library;
	// a typed one's members are known to be promises of one type.
	if at := e.typeOfExpr(arg); !at.IsMap() && !at.IsSet() && !at.IsGenerator() {
		if fn, ok := e.libExports["internal_promise_combine:"+combinatorLibExport[kind]]; ok {
			return e.emitExpr(ast.NewCallExpression(ast.NewIdentifier(fn, pos), []ast.Expression{arg}, pos))
		}
	}
	v, err := e.emitExpr(arg)
	if err != nil {
		return Value{}, err
	}
	at := v.Ty
	it := e.bindValue(v, pos)
	e.ensureExceptionHelpers()
	e.ensureTaskRuntime()
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
	tryL, catchL, doneL := e.freshLabel("comb.iter"), e.freshLabel("comb.iter.threw"), e.freshLabel("comb.iter.done")
	e.excEnter(catchL, tryL)
	e.emitLabel(tryL)
	if at.IsDynamic() || at.Nullable() || at.IsUndefined() || at.IsNull() {
		box, err := e.emitBoxValue(v)
		if err != nil {
			return Value{}, err
		}
		anyIt := e.bindValue(box, pos)
		cond := ast.NewBinaryExpression("||",
			ast.NewBinaryExpression("==", anyIt, ast.NewNullLiteral(false, pos), pos),
			ast.NewBinaryExpression("!==",
				ast.NewUnaryExpression("typeof", true, ast.NewIndexExpression(anyIt, ast.NewMemberExpression(ast.NewIdentifier("Symbol", pos), "iterator", pos), pos), pos),
				ast.NewStringLiteral("function", pos), pos), pos)
		c, err := e.emitExpr(cond)
		if err != nil {
			return Value{}, err
		}
		c = e.emitToBool(c)
		badL, okL := e.freshLabel("comb.noiter"), e.freshLabel("comb.iterok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", c.Ref, badL, okL))
		e.emitLabel(badL)
		if err := e.emitThrowNotIterableValue(box); err != nil {
			return Value{}, err
		}
		e.emitLabel(okL)
	}
	list := ast.NewArrayLiteral([]ast.Expression{&ast.SpreadElement{Arg: it}}, pos)
	innerTy, err := e.promiseArrayElemType(list, name, pos)
	if err != nil {
		return Value{}, err
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(list, pos)
	if err != nil {
		return Value{}, err
	}
	members, innerTy := e.combinatorMembers(list, ptrReg, lenReg, innerTy, elemTy)
	r := e.emitAsyncCombinator(kind, members, lenReg, innerTy)
	e.excLeave()
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", r.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(catchL)
	tag, pay, q := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_get_thrown_tag()", tag))
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_get_thrown_pay()", pay))
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_task_alloc_promise()", q))
	e.emitRejectPromise(q, tag, true, pay)
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", q, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", out, slot))
	return Value{Ref: out, Ty: r.Ty}, nil
}

// combinatorCallOn is the library export that runs `Promise.<combinator>
// .call(C, iterable)` for a receiver other than Promise itself, "" for any
// other call.
func (e *Emitter) combinatorCallOn(mem *ast.MemberExpression, args []ast.Expression) string {
	if mem.Property != "call" || len(args) == 0 {
		return ""
	}
	inner, ok := mem.Object.(*ast.MemberExpression)
	if !ok {
		return ""
	}
	p, ok := inner.Object.(*ast.Identifier)
	if !ok || p.Name != "Promise" || e.isShadowedByLocal("Promise") {
		return ""
	}
	export, ok := combinatorLibExport[inner.Property]
	if !ok {
		return ""
	}
	if recv, ok := args[0].(*ast.Identifier); ok && recv.Name == "Promise" {
		return "" // on Promise: the combinator's own path
	}
	fn, ok := e.libExports["internal_promise_combine:"+strings.TrimSuffix(export, "Of")+"On"]
	if !ok {
		return ""
	}
	return fn
}

// emitCombinatorCallOn emits `Promise.<combinator>.call(C, iterable)` for a
// receiver other than Promise itself: the library's combinator given C, so
// the promise it returns is C's (NewPromiseCapability(C)) and each member is
// resolved through C.resolve. Its value is C's, so held in any. ok is false
// for any other call.
func (e *Emitter) emitCombinatorCallOn(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, bool, error) {
	fn := e.combinatorCallOn(mem, args)
	if fn == "" {
		return Value{}, false, nil
	}
	var iterable ast.Expression = ast.NewNullLiteral(true, pos)
	if len(args) > 1 {
		iterable = args[1]
	}
	v, err := e.emitExpr(ast.NewCallExpression(ast.NewIdentifier(fn, pos), []ast.Expression{args[0], iterable}, pos))
	if err == nil && !v.Ty.IsDynamic() {
		v, err = e.emitBoxValue(v)
	}
	return v, true, err
}
