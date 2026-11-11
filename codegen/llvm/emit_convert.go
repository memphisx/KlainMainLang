package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
)

// convert inserts a type conversion instruction if necessary.
func (e *Emitter) convert(v Value, target Type) Value {
	// Into `never`: no value reaches it (the code is unreachable), so
	// whatever arrives is passed as the never word, unconverted.
	if target.Kind == kindNever && v.Ty.Kind != kindNever && v.Ty.IR != "" {
		return Value{Ref: "0", Ty: target}
	}
	// A void call's result read as a value is undefined (`f()` as an
	// argument), never its empty register.
	if v.Ty.IR == "void" && target.IR != "void" && target.IR != "" {
		if u, err := e.emitExpr(ast.NewNullLiteral(true, ast.Pos{})); err == nil && u.Ty.IR != "void" {
			return e.convert(u, target)
		}
	}
	// An instance of a class extending Map or Set where the host is
	// expected: its body, after its tag word (TDD-00247).
	if v.Ty.IsClass() && (headeredHost(target) || target.IsPromise() && !target.IsDynamic()) && !target.IsClass() {
		if info, ok := e.classes[v.Ty.ClassName]; ok && info.HostBase != nil {
			body := e.freshReg()
			e.emitBodyAddr(body, v.Ref)
			return Value{Ref: body, Ty: target}
		}
	}
	// An instance of a class extending Array where an array is expected:
	// the array its body is, sharing the instance's header.
	if v.Ty.IsClass() && !v.Ty.IsDynamic() && target.IsArray() && !target.IsClass() {
		if info, ok := e.classes[v.Ty.ClassName]; ok && info.HostBase != nil && info.HostBase.IsArray() {
			body := e.freshReg()
			e.emitBodyAddr(body, v.Ref)
			return e.convert(e.arrayValueFromHeaderReg(body, *info.HostBase), target)
		}
	}
	// An array of one numeric storage kind read as another (`Array.from`
	// of a Uint8Array returned as `number[]`): its elements converted, in
	// a copy, since the two layouts cannot share one store.
	if numericElemsDiffer(v.Ty, target) {
		return e.convertNumericArray(v, target)
	}
	// Into or out of a three-state `T | null | undefined` pointer: null
	// becomes (or stops being) the null-reference sentinel.
	if target.NullAndUndef() && target.IR == irPtr && v.Ty.IsDynamic() && v.Ty.IR == "i64" {
		return e.emitUnboxBoxToType(v.Ref, target) // a box that held null is null
	}
	// (A box into a three-state scalar is unboxed below: its tag says which
	// absence it holds.)
	if target.NullAndUndef() && !v.Ty.NullAndUndef() && (v.Ty.IR == irPtr || target.IR != irPtr) &&
		!(v.Ty.IsDynamic() && target.IR != irPtr) {
		return e.toThreeState(v, target)
	}
	if v.Ty.NullAndUndef() && !target.NullAndUndef() && !target.IsDynamic() {
		if triStr(target) && v.Ty.IR == irPtr {
			// A triStr pointer's null is nullRef too (TDD-00257).
			v.Ty.setNullAndUndef(false)
		} else {
			v = e.fromThreeState(v)
		}
	}
	// A Promise<any> read as a Promise<T> (or the reverse): its value
	// converted when it settles (TDD-00230 P3.3 convert).
	if promiseElemsNeedConvert(v.Ty, target) {
		return e.emitConvertPromise(v, target)
	}
	// A web stream of T's chunks read as a stream of `any` chunks.
	if streamChunksNeedConvert(v.Ty, target) {
		return e.emitConvertStreamToAny(v, target)
	}
	// A plain object of one layout where another is expected (a
	// `ReadableOptions` passed on as `DuplexOptions`): its fields, by name,
	// in the target's layout.
	if needsObjectRelayout(v.Ty, target) {
		return e.emitObjectAsRecord(v, target)
	}
	// A class instance (or a plain object of an equal-storage layout) where a
	// structural type is expected stays itself; its members are read through
	// its layout row (TDD-00233).
	// A host value (a Date, a Map) where a structural type is expected is
	// its box's object, whose header the view reads it through by name.
	if hostIntoRecordView(v.Ty, target) {
		if box, err := e.emitBoxValue(v); err == nil {
			e.hostRecordViews = true
			_, pay := e.emitUnboxTagPayload(box)
			obj := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, obj, pay))
			return Value{Ref: obj, Ty: target}
		}
	}
	if isRecordView(target) {
		e.noteRecordViewSource(v.Ty, target)
	}
	// A plain object where a string-keyed dictionary is expected: its fields
	// become the dictionary's entries.
	if target.IsDynamicObject() && target.IsMap() && target.MapKey != nil && isStringTy(*target.MapKey) &&
		plainRecordType(v.Ty) {
		return e.emitObjectToDict(v, target)
	}
	// A tuple where an array or another tuple is expected, or an array where
	// a tuple is: the same array when the two hold their elements in one
	// storage type, else a copy, element by element.
	if c, ok := e.convertTupleArray(v, target); ok {
		return c
	}
	// A concrete array where a boxed-element array (`any[]`) is expected, or
	// the reverse: a copy with each element boxed (unboxed). The two
	// representations differ per element, so the copy does not alias.
	if conv, ok := e.convertArrayElems(v, target); ok {
		return conv
	}
	// A tuple where an object type of its numeric members is expected
	// (`const c: { 0: A; 1: B } = pair`): read through its box, member by
	// member, as the any→object conversion reads them.
	if tupleIntoRecord(v.Ty, target) {
		if r, err := e.tupleRecord(v, target); err == nil {
			return r
		}
	}
	// A caught value (TypeCaught, TDD-00202) flowing out of catch-local scope:
	// to itself it passes through; to `any` it packs to a NaN-box (Error →
	// object); to a concrete Error it unwraps the payload (valid after an
	// `instanceof Error` narrowing); to anything else it packs then re-coerces.
	if v.Ty.IsCaught() && !target.IsCaught() {
		if target.IsError() {
			p := e.freshReg()
			_, pay := e.caughtParts(v)
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, p, pay))
			return Value{Ref: p, Ty: target}
		}
		anyVal := e.emitCaughtToAny(v)
		if target.IsDynamic() {
			return anyVal
		}
		return e.convert(anyVal, target)
	}
	// The reverse: a non-caught value assigned INTO a caught slot — reassigning a
	// catch parameter (`catch (e) { e = 4 }`, TDD-00202). Box it to `any`, then
	// split into the caught record's (tag, payload) with the same nb_tag/nb_pay the
	// initial catch binding uses. Without this the generic scalar path below would
	// fptoui/fptosi the value straight into the { i8, i64 } aggregate — invalid IR
	// (ADR-00903).
	if target.IsCaught() && !v.Ty.IsCaught() {
		// Route through emitValueToCaughtParts so an Error keeps its kmlTagError
		// tag (message/name/instanceof survive) rather than downgrading to an
		// object box — matters for a rejection reason and a reassigned catch var
		// alike (TDD-00207).
		if tag, pay, err := e.emitValueToCaughtParts(v); err == nil {
			return e.emitCaughtAggregate(tag, pay)
		}
	}
	// A dynamic (NaN-boxed, TDD-00156) source coerced into a concrete
	// numeric/boolean target goes through the real JS conversion — never a
	// raw reinterpretation of the encoded word (which is what the generic
	// scalar paths below would do now that TypeAny's IR is i64).
	if v.Ty.IsDynamic() && target.IR != "" && !target.IsDynamic() {
		// A box flowing into a `T | undefined` slot: a null/undefined tag is the
		// absent optional, not a present zero (`const v: number | undefined =
		// xs[i]` off a `(number | undefined)[]`).
		if isNullableScalar(target) {
			tag, _ := e.emitUnboxTagPayload(v)
			isNull := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
			isUndef := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
			nullish := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", nullish, isNull, isUndef))
			present := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", present, nullish))
			payload := e.convert(v, target.withoutNullable())
			pay := payload.Ref
			if target.NullAndUndef() && triScalar(target.withoutNullable()) {
				// A three-state slot tells null from undefined: null's
				// absent payload is the marker.
				ir := target.withoutNullable().IR
				sel := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, %s %s, %s %s", sel, isNull, ir, triMarker(target), ir, pay))
				pay = sel
			}
			return Value{Ref: e.makeNullableScalarAgg(target, present, pay), Ty: target}
		}
		switch {
		case target.Float:
			d := e.emitAnyToF64Slot(v)
			return e.convert(Value{Ref: d, Ty: TypeF64}, target)
		case target.IR == "i1":
			return e.emitAnyTruthy(v)
		case target.IR == "i64" || target.IR == "i32" || target.IR == "i16" || target.IR == "i8":
			d := e.emitAnyToNum(v)
			return e.convert(Value{Ref: d, Ty: TypeF64}, target)
		case target.IsBigInt():
			// A boxed bigint is a { magic, ptr } cell (TDD-00229), not the
			// pointer itself.
			return e.emitUnboxBoxToType(v.Ref, target)
		case target.IsArray() && !target.IsFlatArray():
			// The box's array (its live header carried along).
			return e.emitUnboxBoxToType(v.Ref, target)
		case target.IsFunc() && !isUnconstrainedDynamic(v.Ty):
			// A union's function member: unboxed as a narrowing does.
			return e.emitUnboxBoxToType(v.Ref, target)
		case target.IsPromise() && isUnconstrainedDynamic(v.Ty):
			// A boxed promise into a Promise-typed slot: the promise itself
			// when it is a Promise<any>, else one resolved with the value
			// (PromiseResolve), converted to the target's element type.
			p := e.emitAnyToPromiseAny(v)
			if target.PromiseType != nil && !isUnconstrainedDynamic(*target.PromiseType) && target.PromiseType.IR != "void" {
				// (A task promise, never a raw fetch handle.)
				target.PromiseTask = true
				return e.emitConvertPromise(p, target)
			}
			return p
		case target.IsFunc() && isUnconstrainedDynamic(v.Ty):
			// A boxed function into a function-typed slot: a thunk calls it
			// through the dynamic ABI.
			if c, ok := e.emitAnyToClosure(v, target); ok {
				return c
			}
			_, payload := e.emitUnboxTagPayload(v)
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, r, payload))
			return Value{Ref: r, Ty: target}
		case target.IsSymbol():
			// A boxed symbol (an object carrying symbolTypeIDFlag): the
			// symbol itself, not its ToString.
			_, payload := e.emitUnboxTagPayload(v)
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, r, payload))
			return Value{Ref: r, Ty: target}
		case isStringDict(target):
			// A dictionary as itself; a static object as a dictionary of its
			// keys.
			return e.emitBoxedObjToDict(v, target)
		case isHostHandle(target):
			// A host handle: the handle a host box carries (emit_hostbox.go).
			return e.emitUnboxHost(v, target)
		case (isForOfStringTy(target) || triStr(target) && isForOfStringTy(target.withoutNullable())) && !target.IsClass():
			// A string-typed slot holding another kind at run time (an `any`
			// Buffer handed to a `(chunk: string) => …` listener): JS keeps the
			// value itself, which every string use then converts with
			// ToString; a string, null or undefined passes through.
			if s, ok := e.emitAnyIntoString(v, target); ok {
				return s
			}
			_, payload := e.emitUnboxTagPayload(v)
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, r, payload))
			return Value{Ref: r, Ty: target}
		case target.IsGenerator():
			// A generator instance is its own state: the instance the box
			// holds, never a copy of its fields.
			_, payload := e.emitUnboxTagPayload(v)
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, r, payload))
			return Value{Ref: r, Ty: target}
		case hasObjHeader(target) && !target.IsClass() && !target.IsError() && !v.Ty.IsDynamicObject():
			// A dynamic value (or a union narrowed to its object member) into
			// a static object layout: the object itself when its header names
			// that layout, else a copy of the layout read field by field
			// (emit_shape.go) — a dynamic object from a spread included.
			return Value{Ref: e.emitAnyToLayout(v, target), Ty: target}
		case target.IsClass() && target.IR == irPtr && !target.IsArray():
			// A class-instance slot holds an instance: a box with no pointer
			// (a number, a boolean, a string, null, undefined) reads as null —
			// which `instanceof` and a member read then see — rather than its
			// payload's bits. Every heap kind (an object, a stream) keeps its
			// pointer.
			tag, payload := e.emitUnboxTagPayload(v)
			isObj, p, r := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp uge i8 %s, %d", isObj, tag, kmlTagObject))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", p, isObj, payload))
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, r, p))
			return Value{Ref: r, Ty: target}
		case target.IR == irPtr && !target.IsArray():
			// A dynamic value flowing into a string- (or other single-pointer-)
			// typed target reinterprets its boxed payload as that pointer:
			// TS's `any`→`string` (and `any`→object) is an unchecked
			// assignment with no runtime conversion, so we extract the pointer
			// the NaN box carries (string kind is 0, so the payload IS the data
			// pointer) rather than leave the raw i64 word where a `ptr` is
			// required — which the generic scalar paths below would emit as
			// invalid IR (`i64` value used where `ptr` is expected).
			_, payload := e.emitUnboxTagPayload(v)
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, r, payload))
			return Value{Ref: r, Ty: target}
		}
	}
	// A nullable-scalar aggregate ({ i1, T }, TDD-00064 Stage 3) demotes to its
	// bare payload whenever a plain scalar is wanted — the single point that
	// lets arithmetic, stores, returns, and argument passing consume a boundary
	// value (a T|null return/param/field) without knowing the aggregate shape.
	// Keep it intact only when the target is itself a matching nullable scalar.
	//
	// A nullable scalar flowing into a nullable scalar of a *different* payload
	// (`{ i1, i64 }` → `{ i1, double }`: an integer-valued `T | undefined` passed
	// as a `number | undefined`) converts the payload and carries the presence
	// bit across. Demoting it like the plain-scalar case lost the absent state:
	// the bare zero was then re-wrapped as *present* by the scalar→nullable
	// branch below, so `undefined` arrived as `0` (ADR-01037).
	if isNullableScalar(v.Ty) && isNullableScalar(target) && target.IR != v.Ty.IR {
		present, payload := e.nullableScalarAggParts(v)
		conv := e.convert(payload, target.withoutNullable())
		return Value{Ref: e.makeNullableScalarAgg(target, present, conv.Ref), Ty: target}
	}
	// A nullable scalar flowing into a box (`any`, a union, a boxed array element)
	// keeps its absence: an absent `{ i1, T }` boxes as `undefined`/`null`, not as
	// the payload zero the demotion below would box as a present number.
	if v.Ty.NullAndUndef() && target.IsDynamic() && !target.NullAndUndef() {
		b, err := e.emitBoxValue(v) // null and undefined box apart
		if err == nil {
			return Value{Ref: b.Ref, Ty: target}
		}
	}
	if isNullableScalar(v.Ty) && target.IsDynamic() {
		present, payload := e.nullableScalarAggParts(v)
		boxed := e.convert(payload, target)
		absentLit, _ := e.emitExpr(ast.NewNullLiteral(v.Ty.IsUndefined(), ast.Pos{}))
		absent := e.convert(absentLit, target)
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, %s %s, %s %s", sel, present, target.IR, boxed.Ref, target.IR, absent.Ref))
		return Value{Ref: sel, Ty: target}
	}
	if isNullableScalar(v.Ty) && !isNullableScalar(target) {
		if v.Ty.IsUndefined() && target.IR == "double" && !target.IsDynamic() && scalarTypeKind(v.Ty.withoutNullable()) == "number" {
			// An absent `number | undefined` stored as a number stays
			// undefined: the double slot's sentinel (TDD-00241). Only an
			// operator applies ToNumber (nullableScalarOperand).
			present, payload := e.nullableScalarAggParts(v)
			p := e.convert(payload, target)
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, double %s, double bitcast (i64 %d to double)", r, present, p.Ref, undefF64))
			return Value{Ref: r, Ty: target}
		}
		v = e.nullableScalarPayloadOf(v)
	}
	// null/undefined coerced *to* a nullable scalar becomes an absent { i1, T }
	// aggregate, keeping the invariant that a nullable-scalar-typed Value always
	// carries the aggregate (never a bare zero) in its register.
	// The null literal into a `string | null` is nullRef; undefined is the
	// null pointer (TDD-00257). A bare pointer target (an opaque argument)
	// keeps the null pointer.
	if v.Ty.IsNull() && triStr(target) && target.Absent == absNull {
		if v.Ty.Kind == kindUndefined {
			return Value{Ref: "null", Ty: target}
		}
		return Value{Ref: e.nullRef(), Ty: target}
	}
	if v.Ty.IsNull() && isNullableScalar(target) {
		return Value{Ref: e.makeNullableScalarAgg(target, "false", zeroRef(target.withoutNullable())), Ty: target}
	}
	// null/undefined assigned to an array-typed target becomes the null
	// header (nullable target) or an empty array — checked before the plain
	// non-ptr null-to-zero-value branch below, since an IsArray target also
	// reports IR == irPtr and would otherwise fall through untouched into
	// the unchanged-value case further down — a real bug found assigning a
	// literal `null` to a `T[] | null` interface field (ADR-00158).
	if v.Ty.IsNull() && target.IsArray() {
		if target.Nullable() {
			return e.emitAbsentArrayValue(v.Ty, target) // carries the null header
		}
		return Value{Ref: "null", Ty: target}
	}
	// null/undefined assigned to a non-ptr type becomes the zero value —
	// EXCEPT a dynamic (`any`) target, where they must become the NaN-boxed
	// `null`/`undefined` sentinels (nbNull/nbUndefined via emitBoxValue below),
	// not a raw i64 0. TypeAny's IR is "i64" (non-ptr), so without this guard
	// `null`/`undefined` boxed into an `any` slot — e.g. an `any[]` literal
	// element — stored 0 (the boxed integer 0), reading back as a value that
	// renders "undefined" but is `!== undefined` and `!== null` (ADR-00958).
	if v.Ty.IsNull() && target.IR != irPtr && !target.IsDynamic() {
		return Value{Ref: zeroRef(target), Ty: target}
	}
	// `never` (a Promise.reject's value type — Promise<never>) assigns to any
	// target: the value is never produced (await re-throws before it's read), so a
	// zero of the target type keeps the IR well-typed and stays dead.
	if v.Ty.IsNever() && !target.IsNever() {
		if target.IsArray() {
			return Value{Ref: "null", Ty: target}
		}
		if isNullableScalar(target) {
			return Value{Ref: e.makeNullableScalarAgg(target, "false", zeroRef(target.withoutNullable())), Ty: target}
		}
		return Value{Ref: zeroRef(target), Ty: target}
	}
	// A closure whose signature disagrees with the target function type in
	// dynamism gets an adapter trampoline (ADR-00477) — a raw header copy
	// would let the call site reinterpret concrete words as dynamic boxes
	// (or vice versa). Checked before the same-IR passthrough, since both
	// sides are irPtr.
	if v.Ty.IsFunc() && target.IsFunc() {
		if needed, supported := funcAdapterPlan(v.Ty, target); needed && supported {
			if adapted, ok := e.emitClosureAdapter(v, target); ok {
				return adapted
			}
		} else if needed || v.Ty.FuncThis != target.FuncThis {
			// A conversion the direct adapter does not take: through the box.
			if adapted, ok := e.emitBoxedClosureAs(v, target); ok {
				return adapted
			}
		}
	}
	// A bare scalar flowing into a nullable-scalar slot wraps as a present
	// { i1, T } aggregate (ADR-00478). Type.IR is the *bare* T for both
	// sides, so without this the IR-equality passthrough below returned the
	// unwrapped payload where the aggregate was expected — a real clang
	// error found via an expression-bodied arrow returning its own
	// `number | null` parameter.
	if isNullableScalar(target) && !isNullableScalar(v.Ty) && !v.Ty.IsNull() && v.Ty.IR == target.IR {
		return Value{Ref: e.makeNullableScalarAgg(target, "true", v.Ref), Ty: target}
	}
	// Same IR is the same representation, except a machine integer into a
	// dynamic box (both i64): the number is boxed below.
	if v.Ty.IR == target.IR && !(target.IsDynamic() && !v.Ty.IsDynamic()) {
		return v
	}
	// Boxing a concrete scalar into a dynamic/union box ({ i8, i64 }): the
	// target is NOT a machine integer, but IsInteger() reports true for it
	// (any non-ptr/non-float/non-void IR, the aggregate box included), so
	// without this guard the int->int branch below would emit an invalid
	// `trunc i64 ... to { i8, i64 }`. Boxing is emitBoxValue's job, not a
	// cast; callers that must surface a boxing error do so before reaching
	// coerce (see emit_call.go / emitStaticMethodCall) — here it is a
	// best-effort backstop so no coerce call site can produce that bad IR.
	if target.IsDynamic() && !v.Ty.IsDynamic() {
		if boxed, err := e.emitBoxValue(v); err == nil {
			return boxed
		}
	}
	// A statically-typed object used in a numeric context runs the ToPrimitive
	// ladder (TDD-00201 Stage 1): Symbol.toPrimitive/valueOf/toString. Engages
	// only when the object literal declares one of those callable method fields —
	// otherwise the existing behavior is preserved (special object-likes like
	// Date/Map carry their methods as builtins, not struct fields, so they never
	// match here). Without this the object's `ptr` reaches the numeric switch
	// below and is emitted where an i64/double is expected — invalid IR.
	if objectMayToPrimitive(v.Ty) &&
		(target.Float || target.IR == "i64" || target.IR == "i32" || target.IR == "i16" || target.IR == "i8") {
		if res, ok, err := e.emitObjectToPrimitive(v, "number"); err == nil && ok {
			return e.convert(res, target)
		}
	}
	// A string, array, function or other object where a number (a double)
	// is expected:
	// JavaScript's ToNumber (through ToPrimitive for an object), never the
	// pointer reinterpreted. A boolean target takes ToBoolean.
	// Under -compat=js only: the strict lane rejects such a value as tsc
	// does, at its own checks.
	if e.compatJS() && !v.Ty.IsDynamic() && !target.IsDynamic() && !v.Ty.IsSymbol() && !v.Ty.IsBigInt() && !isNullableScalar(v.Ty) &&
		(isStringTy(v.Ty) || v.Ty.IsArray() || v.Ty.IsObject() || v.Ty.IsFunc()) {
		switch {
		case target.IR == "i1" && !isNullableScalar(target):
			return e.toBool(v)
		case target.Float && !isNullableScalar(target):
			if n, err := e.emitUnaryPlus(v, ast.Pos{}); err == nil && n.Ty.IR == "double" {
				return e.convert(n, target)
			}
		}
	}
	// ToNumber(Symbol) is a TypeError in real JS ("Cannot convert a Symbol
	// value to a number"). A Symbol-typed value flowing into a concrete numeric
	// scalar (double / machine integer) therefore throws unconditionally rather
	// than reinterpreting the symbol pointer as a number — which the default
	// "return unchanged" fall-through below would have emitted as invalid IR
	// (`ptr` where an `i64`/`double` is required). Guarded to genuine numeric
	// machine targets so equality/dynamic/boxing paths (which route elsewhere)
	// are untouched.
	if v.Ty.IsSymbol() && target.IR == "i1" && !isNullableScalar(target) {
		return Value{Ref: "true", Ty: target} // ToBoolean(Symbol) is true
	}
	if v.Ty.IsSymbol() && !target.IsDynamic() && (target.Float || (target.IsInteger() && target.IR != "" && target.IR != irPtr)) {
		e.emitThrowTypeError("Cannot convert a Symbol value to a number")
		// The caller goes on emitting: into an unreachable block, so nothing
		// after the throw is dropped while a later block still uses it.
		e.emitLabel(e.freshLabel("dead"))
		return Value{Ref: zeroRef(target), Ty: target}
	}

	reg := e.freshReg()

	srcInt := v.Ty.IsInteger()
	dstInt := target.IsInteger()

	switch {
	// int → int (same size handled above, so either widen or narrow)
	case srcInt && dstInt:
		srcBits := typeBits(v.Ty.IR)
		dstBits := typeBits(target.IR)
		if dstBits > srcBits {
			ext := "sext"
			if !v.Ty.Signed {
				ext = "zext"
			}
			e.emitInstr(fmt.Sprintf("%s = %s %s %s to %s", reg, ext, v.Ty.IR, v.Ref, target.IR))
		} else {
			e.emitInstr(fmt.Sprintf("%s = trunc %s %s to %s", reg, v.Ty.IR, v.Ref, target.IR))
		}

	// int → float
	case srcInt && target.Float:
		op := "sitofp"
		if !v.Ty.Signed {
			op = "uitofp"
		}
		e.emitInstr(fmt.Sprintf("%s = %s %s %s to %s", reg, op, v.Ty.IR, v.Ref, target.IR))

	// float → int. A direct fptosi/fptoui into a target narrower than 64 bits is
	// undefined for an out-of-range value (LLVM yields poison, so `300.0` into an
	// i8 came out `0`). Convert to i64 first, then truncate — this both avoids the
	// UB and gives JS's modular wraparound for narrow integer / TypedArray element
	// stores (ToUint8/ToInt32: `300 → 44`, `-1 → 255`). TDD-00123.
	case v.Ty.Float && dstInt:
		// NaN and ±Infinity are poison under a plain fptosi; the saturating
		// intrinsic gives NaN → 0 and ±Infinity → INT64_MIN/MAX (ADR-01061).
		if typeBits(target.IR) < 64 {
			// Always via a signed i64 intermediate: fptoui of a negative value is
			// itself UB, and the modular trunc's 2's-complement bit pattern is the
			// same regardless of the intermediate's signedness (-1 → i64 -1 → i8
			// 0xFF = 255, matching JS ToUint8). ToInt32/ToUint8 map a non-finite
			// value to 0, which the saturated word would not truncate to.
			wide := e.emitFloatToI64(v.Ty.IR, v.Ref)
			finite := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fcmp one %s %s, %s", finite, v.Ty.IR, v.Ref, floatInfLiteral(v.Ty.IR)))
			finiteNeg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fcmp one %s %s, %s", finiteNeg, v.Ty.IR, v.Ref, floatNegInfLiteral(v.Ty.IR)))
			both := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", both, finite, finiteNeg))
			zeroed := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", zeroed, both, wide))
			e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to %s", reg, zeroed, target.IR))
		} else if target.Signed {
			wide := e.emitFloatToI64(v.Ty.IR, v.Ref)
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 0", reg, wide))
		} else {
			e.emitInstr(fmt.Sprintf("%s = fptoui %s %s to %s", reg, v.Ty.IR, v.Ref, target.IR))
		}

	// float → float
	case v.Ty.Float && target.Float:
		srcBits := typeBits(v.Ty.IR)
		dstBits := typeBits(target.IR)
		if dstBits > srcBits {
			e.emitInstr(fmt.Sprintf("%s = fpext %s %s to %s", reg, v.Ty.IR, v.Ref, target.IR))
		} else {
			e.emitInstr(fmt.Sprintf("%s = fptrunc %s %s to %s", reg, v.Ty.IR, v.Ref, target.IR))
		}

	default:
		// The same representation needs no conversion. Any other pair is one
		// convert does not define: a compiler bug, never a reinterpretation.
		if from, to := storageIR(v.Ty), storageIR(target); from != to && from != "" && to != "" && from != "void" && to != "void" {
			e.convertGaps = append(e.convertGaps, fmt.Sprintf("from %s (%s) to %s (%s)", tsTypeName(v.Ty), from, tsTypeName(target), to))
			if os.Getenv("KML_TYPEGAP_STACK") != "" {
				debug.PrintStack()
			}
		}
		return v
	}

	return Value{Ref: reg, Ty: target}
}

// conversionIsSound reports whether convert(v, target) actually produced a value of
// the target's storage type — i.e. the conversion was real, not the default
// "return v unchanged" fallthrough for two genuinely-incompatible scalar types
// (a `number` where a string is expected, a string where a number is expected,
// etc.). Composite targets (array, object, Map/Set, dynamic/any, nullable
// scalar, function) legitimately carry a storage IR that differs from a bare
// scalar's, so they're never flagged — their own emit paths handle the value.
// This is the guard behind convertChecked: this compiler is a typed subset, so a
// genuine cross-kind mismatch (the kind untyped JS produces) should be a clean
// compile error, never an invalid `store`/arithmetic operand.
func conversionIsSound(coerced, target Type) bool {
	if coerced.IR == target.IR {
		return true
	}
	if target.IsArray() || target.IsObject() || target.IsDynamic() || target.IsDynamicObject() ||
		target.IsMap() || target.IsSet() || target.IsFunc() || target.UnionMembers != nil ||
		isNullableScalar(target) || target.IR == "void" {
		return true
	}
	return false
}

// convertiblePure predicts, without emitting any IR, whether convert(src→target)
// yields a value of the target's storage type — a real conversion, not the
// silent "return unchanged" fallthrough. Used to reject a genuine cross-kind
// mismatch (a number stored into a string field, etc.) at a store/consume site
// before it emits invalid IR. Mirrors coerce's own handled cases: same IR,
// composite/dynamic/nullable/union/void target (own path), a null/undefined/
// never/dynamic/nullable source coerce specially rewrites, or numeric↔numeric.
// objectFieldBoxingCompatible reports whether two object/class shapes agree on
// the boxedness of every field they share by name — an `any` field (a NaN box)
// on one side must be `any` on the other, never a concrete raw field. Reading a
// raw field as a box (or vice versa) yields garbage, so a mismatch is not a
// sound structural coercion (TDD-00208).
func objectFieldBoxingCompatible(src, target Type) bool {
	srcField := map[string]Type{}
	for _, f := range src.Fields {
		srcField[f.Name] = f.Ty
	}
	for _, tf := range target.Fields {
		if sf, ok := srcField[tf.Name]; ok {
			if sf.IsDynamic() != tf.Ty.IsDynamic() {
				return false
			}
		}
	}
	return true
}

func convertiblePure(src, target Type) bool {
	if target.Kind == kindNever {
		return true // unreachable: convert passes the never word
	}
	// A synthetic object-literal accessor class's accessors are methods, not
	// fields: a structural target reads it through the checked view
	// (TDD-00233), by name, its accessors included; any other object or class
	// target would read a field it does not have.
	if isSyntheticObjLitClass(src) && !isRecordView(target) {
		if (target.IsObject() && !target.IsClass()) ||
			(target.IsClass() && target.ClassName != src.ClassName) {
			return false
		}
	}
	// A structural object/class coercion must not cross an `any`-field boundary
	// (TDD-00208): an `any` field is a NaN box, a concrete field is its raw
	// value, so reading one as the other yields garbage. Object/class types all
	// share IR irPtr, so this must be checked before the IR-equality shortcut —
	// require each shared field's boxedness to match, else reject cleanly
	// (`{x:number}` passed where `{x:any}` was wanted).
	// A structural target reads another layout by name through the checked
	// view (TDD-00233), converting each member, so the boundary is its.
	if (target.IsObject() || target.IsClass()) && (src.IsObject() || src.IsClass()) &&
		!objectFieldBoxingCompatible(src, target) &&
		!(isRecordView(target) && (src.IsClass() || plainRecordType(src) || emptyRecordType(src))) {
		return false
	}
	// An array's value is its header pointer: no string, number or
	// boolean slot holds it (`const s: string = xs`, which only -compat=js
	// reaches).
	if src.IsArray() && !target.IsArray() && !target.IsDynamic() && (isStringTy(target) || target.IsInteger() || target.Float || target.IR == "i1") {
		return false
	}
	// Nor does an array slot hold a string, number or boolean (`{ argv:
	// 'foo' }` for an any[] field, which only -compat=js reaches).
	if target.IsArray() && !src.IsArray() && !src.IsDynamic() && !src.IsNull() && !src.IsUndefined() && (isStringTy(src) || src.IsInteger() || src.Float || src.IR == "i1") {
		return false
	}
	if src.IR == target.IR {
		return true
	}
	// A caught-value target (a throw/reject reason, TDD-00202/00207) accepts any
	// value — the coercion packs it into the { i8 tag, i64 payload } record.
	if target.IsCaught() {
		return true
	}
	if target.IsArray() || target.IsObject() || target.IsDynamic() || target.IsDynamicObject() ||
		target.IsMap() || target.IsSet() || target.IsFunc() || target.UnionMembers != nil ||
		isNullableScalar(target) || target.IR == "void" {
		return true
	}
	if src.IsNull() || src.IsUndefined() || src.IsNever() || src.IsDynamic() || isNullableScalar(src) {
		return true
	}
	srcNum := src.IsInteger() || src.Float
	tgtNum := target.IsInteger() || target.Float
	return srcNum && tgtNum
}

// convertChecked is coerce plus a soundness gate: if the coercion couldn't
// convert v to target (a genuine cross-kind type mismatch), it returns a clean
// compile error instead of a value whose IR silently disagrees with target and
// would emit invalid IR at the consuming store/op. `what` names the context for
// the message (e.g. "assignment", "argument"). See conversionIsSound.
func (e *Emitter) convertChecked(v Value, target Type, pos ast.Pos, what string) (Value, error) {
	// ToBoolean(Symbol) is true. In a numeric position (one the checker
	// let through, a cast over `any`) a Symbol is coerce's run-time
	// TypeError, as Node throws it.
	if v.Ty.IsSymbol() && target.IR == "i1" && !isNullableScalar(target) {
		return Value{Ref: "true", Ty: target}, nil
	}
	// A closure whose body can fall off the end returns `T | undefined`
	// (ADR-01065); binding it to a declared `(...) => T` is tsc's "Type 'T |
	// undefined' is not assignable to type 'T'". The two return ABIs differ
	// (`{ i1, T }` vs bare T), so this must be a rejection, not a passthrough.
	if err := closureFallOffMismatch(v.Ty, target, pos, what); err != nil {
		return Value{}, err
	}
	if isNullableScalar(target) && !isNullableScalar(v.Ty) && !v.Ty.IsDynamic() && !v.Ty.IsNull() && !v.Ty.IsUndefined() && v.Ty.IR != "" {
		// A present value into an optional slot (a defaulted parameter's):
		// checked against the payload, then wrapped.
		pay, err := e.convertChecked(v, target.withoutNullable(), pos, what)
		if err != nil {
			return Value{}, err
		}
		return e.convert(pay, target), nil
	}
	out := e.convert(v, target)
	if !conversionIsSound(out.Ty, target) {
		return Value{}, internalAt(pos, "checker-rejected: type mismatch in %s", what)
	}
	return out, nil
}

// emitToBool converts any scalar value to i1 for use in a branch — a thin
// alias for toBool (emit_exprs_types.go), kept as a separate name since
// call sites elsewhere already use it. Previously its own separate
// (near-duplicate) implementation that had quietly diverged from toBool's:
// this one used `fcmp une` for float truthiness, which treats NaN as
// truthy (an "unordered" comparison is true whenever either operand is
// NaN) — wrong, since real JS's Boolean(NaN) === false. toBool's `fcmp
// one` (ordered-and-not-equal, false for NaN) is correct; consolidating
// onto one implementation fixes this rather than carrying two subtly
// different bugs in two places. See ADR-00116.
func (e *Emitter) emitToBool(v Value) Value {
	return e.toBool(v)
}

// typeBits returns the bit-width of the given LLVM IR type string.
func typeBits(ir string) int {
	switch ir {
	case "i1":
		return 1
	case "i8":
		return 8
	case "i16":
		return 16
	case "i32", "float":
		return 32
	case "i64", "double":
		return 64
	}
	return 64
}

// closureFallOffMismatch rejects a closure whose inferred return is `T |
// undefined` (a body that can fall off the end, ADR-01065) bound to a declared
// function type returning the bare T — tsc's "Type 'T | undefined' is not
// assignable to type 'T'", which the checker reports. The two return ABIs
// differ (`{ i1, T }` vs T), so the pair never reaches emission. A void/any/nullable expected return
// accepts the closure unchanged.
func closureFallOffMismatch(vTy, target Type, pos ast.Pos, what string) error {
	if !vTy.IsFunc() || !target.IsFunc() || vTy.FuncRetType == nil || target.FuncRetType == nil {
		return nil
	}
	tr := *target.FuncRetType
	if !isNullableScalar(*vTy.FuncRetType) || isNullableScalar(tr) || tr.IsDynamic() || tr.IR == "void" || tr.IR == "" {
		return nil
	}
	// The checker rejects it (TS2322); JavaScript has no declared slot.
	return internalAt(pos, "%s: a closure returning %s | undefined for one returning %s", what, tsTypeName(vTy.FuncRetType.withoutNullable()), tsTypeName(tr))
}

// plainRecordType reports whether t is a plain static object (an interface,
// type literal or object literal), not a class instance or a host object.
func plainRecordType(t Type) bool {
	return isUnionObjectMember(t) && !t.IsClass() && t.ClassName == "" && !t.IsDynamic() &&
		!t.Inline && !t.IsError() && !t.IsSymbol() && len(t.UserFields()) > 0
}

// emptyRecordType reports a plain object type with no fields (`{}` built
// as a literal): every field of a target is absent in it.
func emptyRecordType(t Type) bool {
	return isUnionObjectMember(t) && !t.IsClass() && t.ClassName == "" && !t.IsDynamic() &&
		!t.Inline && !t.IsError() && !t.IsTuple() && hasObjHeader(t) && len(t.UserFields()) == 0
}

// needsObjectRelayout reports whether a src value must be copied to be read
// as target: both plain objects whose layouts differ, where every field the
// target requires is in the source.
func needsObjectRelayout(src, target Type) bool {
	return objectRelayoutNeeded(src, target, false)
}

// needsObjectRelayoutBoxing is needsObjectRelayout where a concrete source
// field may box into a union/any target field — a union member's copy.
func needsObjectRelayoutBoxing(src, target Type) bool {
	return objectRelayoutNeeded(src, target, true)
}

func objectRelayoutNeeded(src, target Type, boxing bool) bool {
	if !(plainRecordType(src) || emptyRecordType(src)) || !plainRecordType(target) {
		return false
	}
	if sameFieldLayout(src, target) {
		return false
	}
	for _, tf := range target.UserFields() {
		sf, ok := fieldByName(src, tf.Name)
		if !ok {
			// An optional field the source lacks: absent (undefined) in a
			// box, null in a pointer slot, empty in an array slot.
			if !tf.Ty.IsUndefined() && !isNullableScalar(tf.Ty) && !tf.Ty.Nullable() &&
				!tf.Ty.IsDynamic() && !tf.Ty.IsArray() && tf.Ty.IR != irPtr {
				return false
			}
			continue
		}
		// A concrete source field boxes into a union/any target field.
		if boxing && tf.Ty.IsDynamic() && !tf.Ty.IsArray() && !sf.Ty.IsDynamic() {
			continue
		}
		if sf.Ty.IsDynamic() != tf.Ty.IsDynamic() || sf.Ty.IsArray() != tf.Ty.IsArray() {
			return false
		}
	}
	return true
}

func sameFieldLayout(a, b Type) bool {
	if len(a.Fields) != len(b.Fields) {
		return false
	}
	for i := range a.Fields {
		if a.Fields[i].Name != b.Fields[i].Name || StructFieldIR(a.Fields[i].Ty) != StructFieldIR(b.Fields[i].Ty) {
			return false
		}
	}
	return true
}

// emitObjectAsRecord is a plain object of another layout where the plain
// object type target is expected: the object itself, read through the
// checked view (TDD-00233 Stage 3), so identity and writes are its own.
func (e *Emitter) emitObjectAsRecord(v Value, target Type) Value {
	e.noteRecordViewSource(v.Ty, target)
	return Value{Ref: v.Ref, Ty: target}
}

// emitAnyIntoString is a box's value in a string-typed slot: its string
// pointer when it holds a string (or null/undefined, the null pointer), else
// its ToString.
func (e *Emitter) emitAnyIntoString(v Value, target Type) (Value, bool) {
	tag, payload := e.emitUnboxTagPayload(v)
	isStr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isStr, tag, kmlTagString))
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
	isUndef := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
	n1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", n1, isNull, isUndef))
	direct := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", direct, isStr, n1))
	directL := e.freshLabel("anystr.direct")
	convL := e.freshLabel("anystr.conv")
	joinL := e.freshLabel("anystr.join")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", direct, directL, convL))
	e.emitLabel(directL)
	p0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, p0, payload))
	// A plain string's null is nullRef; its null pointer is undefined
	// (TDD-00257).
	p := p0
	if triStr(target) {
		p = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", p, isNull, e.nullRef(), p0))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(convL)
	sv, err := e.emitDynamicToString(Value{Ref: v.Ref, Ty: TypeAny})
	if err != nil {
		return Value{}, false
	}
	convEnd := e.freshLabel("anystr.convend")
	e.emitTerminator(fmt.Sprintf("br label %%%s", convEnd))
	e.emitLabel(convEnd)
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(joinL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi "+irPtr+" [ %s, %%%s ], [ %s, %%%s ]", r, p, directL, sv.Ref, convEnd))
	return Value{Ref: r, Ty: target}, true
}

// emitObjectToDict builds a string-keyed dictionary from a plain object's
// fields (a null object stays null).
func (e *Emitter) emitObjectToDict(v Value, target Type) Value {
	e.ensureMapStrHelpers()
	valTy := TypeAny
	if target.MapVal != nil {
		valTy = *target.MapVal
	}
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", isNull, v.Ref))
	copyL, doneL := e.freshLabel("obj2dict.copy"), e.freshLabel("obj2dict.done")
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" null, "+irPtr+" %s, align 8", slot))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, doneL, copyL))
	e.emitLabel(copyL)
	m := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_map_str_create()", m))
	for i, f := range v.Ty.Fields {
		if isHiddenField(f.Name) {
			continue
		}
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", g, v.Ty.StructIR(), v.Ref, i))
		fv := e.loadScalarOrNullableField(g, f.Ty)
		ft := valTy
		if dt, ok := dictFieldType(target, ast.NewStringLiteral(f.Name, ast.Pos{})); ok {
			ft = dt
		}
		if ft.IsDynamic() && !fv.Ty.IsDynamic() {
			if b, err := e.emitBoxValue(fv); err == nil {
				fv = b
			}
		} else {
			fv = e.convert(fv, ft)
		}
		ref := e.valueToMapVal(fv, ft)
		e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set("+irPtr+" %s, "+irPtr+" %s, i64 %s)", m, e.internString(f.Name), ref))
	}
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", m, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", out, slot))
	return Value{Ref: out, Ty: target}
}

// convertArrayElems converts between a concrete-element array and an
// `any[]` (one box per slot); ok is false for any other pair.
func (e *Emitter) convertArrayElems(v Value, target Type) (Value, bool) {
	if numericElemsDiffer(v.Ty, target) {
		return e.convertNumericArray(v, target), true // another numeric storage kind
	}
	toAny, fromAny := arrayElemsConvert(v.Ty, target)
	if !toAny && !fromAny {
		return Value{}, false
	}
	src, dst := *v.Ty.ElemType, *target.ElemType
	data, n := e.freshReg(), e.freshReg()
	data = e.arrayData(v)
	n = e.arrayLen(v)
	elemSize := int64(8)
	if fromAny {
		elemSize = int64(dst.Align())
	}
	bytes, out := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytes, n, elemSize))
	convKind := allocElems
	if fromAny {
		convKind = elemAllocKind(dst)
	}
	e.heapMallocInto(out, convKind, bytes)
	var err error
	if toAny {
		err = e.emitSpreadBoxLoop(data, n, src, out)
	} else {
		err = e.emitSpreadUnboxLoop(data, n, dst, out)
	}
	if err != nil {
		return Value{}, false
	}
	r1 := e.newArrayHeader(out, n)
	return Value{Ref: r1, Ty: target}, true
}

// arrayElemsConvert reports whether an array of from, read as target, needs
// its elements boxed (toAny) or unboxed (fromAny).
func arrayElemsConvert(from, target Type) (toAny, fromAny bool) {
	if !from.IsArray() || !target.IsArray() || from.ElemType == nil || target.ElemType == nil ||
		from.IsTuple() || target.IsTuple() || from.IsTypedArray() || target.IsTypedArray() || from.IsBuffer() || target.IsBuffer() ||
		from.IsFlatArray() || target.IsFlatArray() || from.IsDynamic() || target.IsDynamic() {
		return false, false
	}
	src, dst := *from.ElemType, *target.ElemType
	toAny = isUnconstrainedDynamic(dst) && !src.IsDynamic() && src.IR != "" && src.IR != "void"
	fromAny = isUnconstrainedDynamic(src) && !dst.IsDynamic() && dst.IR != "" && dst.IR != "void" && !dst.IsArray()
	return toAny, fromAny
}

// relayoutTuple copies tuple v into a new tuple of target's element layout,
// each element converted (a boxed element unboxed, or the reverse).
func (e *Emitter) relayoutTuple(v Value, target Type) Value {
	vals := make([]Value, len(target.Fields))
	for i := range target.Fields {
		vals[i] = e.convert(e.tupleElem(v, i), target.Fields[i].Ty)
	}
	return e.tupleOf(vals, target)
}

// convertTupleArray converts between a tuple and an array or another tuple
// (convert): the value itself when both hold their elements in one storage
// type, else a new array of the elements converted.
func (e *Emitter) convertTupleArray(v Value, target Type) (Value, bool) {
	if !(v.Ty.IsTuple() || target.IsTuple()) || !v.Ty.IsArray() || !target.IsArray() || v.Ty.IsDynamic() || target.IsDynamic() ||
		v.Ty.IsTypedArray() || target.IsTypedArray() || v.Ty.IsBuffer() || target.IsBuffer() || v.Ty.IsFlatArray() || target.IsFlatArray() ||
		v.Ty.ElemType == nil || target.ElemType == nil {
		return Value{}, false
	}
	if reprKey(*v.Ty.ElemType) == reprKey(*target.ElemType) {
		return Value{Ref: v.Ref, Ty: target}, true
	}
	switch {
	case v.Ty.IsTuple() && target.IsTuple() && len(v.Ty.Fields) == len(target.Fields):
		return e.relayoutTuple(v, target), true
	case v.Ty.IsTuple() && !target.IsTuple():
		vals := make([]Value, len(v.Ty.Fields))
		for i := range v.Ty.Fields {
			vals[i] = e.convert(e.tupleElem(v, i), *target.ElemType)
		}
		return e.tupleOf(vals, target), true
	case target.IsTuple():
		// An array (or a longer or shorter tuple): its first elements, an
		// element past its end undefined.
		data, n := e.arrayParts(v)
		vals := make([]Value, len(target.Fields))
		for i := range target.Fields {
			vals[i] = e.convert(e.arrayElemOrUndefined(data, n, *v.Ty.ElemType, i), target.Fields[i].Ty)
		}
		return e.tupleOf(vals, target), true
	}
	return Value{}, false
}

// arrayElemOrUndefined is element i of an array of data and length n,
// boxed, or undefined when i is past its end.
func (e *Emitter) arrayElemOrUndefined(data, n string, elemTy Type, i int) Value {
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %d, "+irPtr+" %s, align 8", nbUndefined, slot))
	in := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %d", in, n, i))
	readL, doneL := e.freshLabel("tupel.read"), e.freshLabel("tupel.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", in, readL, doneL))
	e.emitLabel(readL)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %d", gep, elemTy.IR, data, i))
	if b, err := e.emitBoxValue(e.loadArrayElem(gep, elemTy)); err == nil {
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", b.Ref, slot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", r, slot))
	return Value{Ref: r, Ty: TypeAny}
}

// tupleRecord builds the object type target from a tuple's elements, its
// field "i" the tuple's element i (an object literal of index reads).
func (e *Emitter) tupleRecord(v Value, target Type) (Value, error) {
	pos := ast.Pos{}
	tmp := e.tupleSpreadTemp(v, pos)
	var props []ast.ObjectProperty
	for _, f := range target.Fields {
		i, err := strconv.Atoi(f.Name)
		if err != nil || i < 0 || i >= len(v.Ty.Fields) {
			continue
		}
		props = append(props, ast.ObjectProperty{Key: f.Name, Value: ast.NewIndexExpression(tmp, ast.NewNumberLiteral(f.Name, pos), pos)})
	}
	return e.emitExprWithObjectHint(ast.NewObjectLiteral(props, pos), target)
}
