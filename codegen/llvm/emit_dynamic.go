// emit_dynamic.go — real runtime-polymorphic support for any/unknown (TypeAny)
// and, since TDD-00043, general union types beyond T | null: both share the
// exact same runtime representation, boxing concrete values into a
// { i8 tag, i64 payload } struct, and the three operations that must dispatch
// on the runtime tag instead of a compile-time type: printing (console.log/
// template literals), typeof, and ===/!==. A union is just that same box with
// a compile-time-tracked member set (Type.UnionMembers) checked at every
// assignment/call/return boundary — see unionAllowsAssignmentFrom below.
//
// Tags: 0=int, 1=float, 2=string, 3=boolean, 4=null, 5=undefined, 6=object.
//
// Deliberately out of scope (see docs/adr for the any/unknown ADR, and
// docs/tdd/TDD-00043.md for unions): arithmetic operators; bare any/unknown
// as a function parameter/return/array/object-field type (a *constrained*
// union is fine in those positions — see isUnconstrainedDynamic); union
// members beyond number/string/boolean/null/undefined (no object/array/
// interface members yet); and flow-based narrowing (`typeof x === "string"`
// narrowing x's effective type inside the branch). Those positions get a
// clean compiler error rather than silently accepting a wider shape than the
// codegen here actually handles — see the guards in emit_func.go/emitter.go.
package llvm

import "fmt"

// isUnconstrainedDynamic reports whether ty is bare any/unknown — IsDynamic
// with no UnionMembers set. A *constrained* union (IsDynamic with a non-nil
// UnionMembers, TDD-00043) is fully checkable wherever this returns false for
// it: its member set makes assignment/call/return positions fully verifiable
// (see unionAllowsAssignmentFrom), which is the entire reason general unions
// are worth having — bare any/unknown stays rejected in those positions
// exactly as ADR-00008 left it.
func isUnconstrainedDynamic(ty Type) bool {
	return ty.IsDynamic && ty.UnionMembers == nil
}

// containsDynamicElement reports whether ty contains, as an array element or
// object field, ANY dynamic type — bare any/unknown or a constrained union
// alike. Used to reject the out-of-scope positions (array element, object
// field) with a clean compiler error instead of silently producing broken
// IR. Deliberately does NOT check ty itself at the top level — callers
// combine it with their own top-level isUnconstrainedDynamic check, since
// some call sites (e.g. a bare `let x: any`, or a constrained union as a
// function param/return — see isUnconstrainedDynamic) allow a dynamic type
// at the top level but still need to reject it nested inside a container.
//
// Note this rejects a *constrained* union nested inside an array/object too,
// unlike the top-level positions isUnconstrainedDynamic's callers allow a
// union through for (var decl, function param/return): member-set checking
// (unionAllowsAssignmentFrom) is only wired up at those top-level
// assignment/call/return boundaries, not at array-literal-element
// construction, HOF callback element passing, or object-literal field
// assignment — so a union nested in a container would silently skip that
// checking today rather than being rejected, worse than not supporting it at
// all. A real, deliberate scope cut for V1 (TDD-00043's Open Questions
// already defers object/array *members* past V1 for a related reason); union
// *elements*/*fields* nested in an otherwise-concrete container are a
// separate, still-open gap from that, left for whenever this is revisited.
func containsDynamicElement(ty Type) bool {
	if ty.IsArray && ty.ElemType != nil {
		return ty.ElemType.IsDynamic || containsDynamicElement(*ty.ElemType)
	}
	if ty.IsObject {
		for _, f := range ty.Fields {
			if f.Ty.IsDynamic || containsDynamicElement(f.Ty) {
				return true
			}
		}
	}
	return false
}

// scalarTypeKind classifies a concrete (non-dynamic) type into one of the
// three scalar kinds a union member can currently be (TDD-00043 V1 scope):
// "number" (any integer or float width, including JSDoc-extended int8…
// uint64/float32/float64 — a union member is always the canonical "number"
// resolution, TypeI64, but a value being checked against it may carry a
// narrower JSDoc-extended type), "string", or "boolean". Returns "" for
// anything else (dynamic, array, object, null/undefined sentinel) — those
// aren't valid union members in V1, and null/undefined are handled
// separately via Type.Nullable rather than appearing here.
func scalarTypeKind(t Type) string {
	if t.IsDynamic || t.IsArray || t.IsObject || t.IsNull || t.IsUndefined {
		return ""
	}
	switch {
	case t.IR == "i1":
		return "boolean"
	case t.IR == "ptr":
		return "string"
	case t.Float, t.IR == "i8", t.IR == "i16", t.IR == "i32", t.IR == "i64":
		return "number"
	}
	return ""
}

// validateUnionMembers rejects a union type whose members go beyond
// TDD-00043's V1 scope — number/string/boolean only, no object/array/
// interface members yet (a real, deliberate gap; see the TDD's Open
// Questions). Called at every checkpoint that already rejects bare
// any/unknown in that position (isUnconstrainedDynamic), so a union with an
// out-of-scope member gets the same clean compile-time rejection instead of
// silently falling through to broken codegen once its runtime tag turns out
// to be one containsDynamicElement's/emitBoxValue's/etc. narrower callers
// don't expect.
func validateUnionMembers(ty Type, line, col int) error {
	if ty.UnionMembers == nil {
		return nil
	}
	for _, m := range ty.UnionMembers {
		if scalarTypeKind(m) == "" {
			return fmt.Errorf("%d:%d: union member types are currently limited to number, string, and boolean (plus null/undefined)", line, col)
		}
	}
	return nil
}

// unionAllowsAssignmentFrom reports whether a value of type valTy may be
// assigned/passed/returned into a slot declared as the constrained union
// unionTy (unionTy.UnionMembers must be non-nil — callers only reach here
// once isUnconstrainedDynamic(unionTy) is already known false). A
// null/undefined value is allowed exactly when the union itself is Nullable,
// matching how T | null already behaves for a concrete T. Otherwise valTy
// must scalar-match one of the declared members (see scalarTypeKind) —
// assigning a value whose type isn't in the declared set is a clean compile
// error, the actual type-safety win a union has over bare any/unknown.
func unionAllowsAssignmentFrom(unionTy Type, valTy Type) bool {
	if valTy.IsNull || valTy.IsUndefined {
		return unionTy.Nullable
	}
	// A value that's already boxed dynamic (e.g. assigning one union-typed
	// variable to another, or a bare any/unknown expression) can't be
	// statically checked against the member set — its real tag is only known
	// at runtime. Allow it through here; it's the same trust boundary bare
	// any/unknown already crosses everywhere else (ADR-00008).
	if valTy.IsDynamic {
		return true
	}
	valKind := scalarTypeKind(valTy)
	if valKind == "" {
		return false
	}
	for _, m := range unionTy.UnionMembers {
		if scalarTypeKind(m) == valKind {
			return true
		}
	}
	return false
}

const (
	kmlTagInt       = 0
	kmlTagFloat     = 1
	kmlTagString    = 2
	kmlTagBoolean   = 3
	kmlTagNull      = 4
	kmlTagUndefined = 5
	kmlTagObject    = 6
	kmlTagArray     = 7
	// kmlTagFuncRef boxes a reference to a built-in constructor/function used
	// as a first-class value (`assert.throws(TypeError, fn)`, `f === TypeError`).
	// The payload is the constructor's interned name string (ptrtoint'd), so
	// same-tag equality in __kml_any_eq — a plain payload compare — is exact:
	// interned strings are deduplicated per module, one global per name.
	kmlTagFuncRef = 8
)

// emitBoxValue converts any concrete Value into a Value{Ty: TypeAny}. Boxing
// is idempotent: if v is already dynamic, it's returned unchanged, so callers
// never need to check first.
func (e *Emitter) emitBoxValue(v Value) (Value, error) {
	if v.Ty.IsDynamic {
		return v, nil
	}
	// An array value is a { ptr, i64 } aggregate (data pointer + length), which
	// doesn't fit the box's single i64 payload slot. Box its *data pointer* as
	// the identity: arrays are reference types in JS, so `===` on two boxed
	// arrays is reference equality (kmlTagArray, compared by pointer in
	// __kml_any_eq), and the data pointer is the stable per-array identity a
	// value-typed array has (`a === a` is the same pointer boxed twice → true;
	// two distinct literals have distinct buffers → false). The length and
	// element type are *not* preserved — a boxed array supports `===`/`!==` and
	// `typeof` (→ "object"), but not indexing, `.length`, or content-accurate
	// printing (its toString is the `[object Array]` tag string, TDD-00062).
	if v.Ty.IsArray {
		dataPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 0", dataPtr, v.Ref))
		payload := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", payload, dataPtr))
		r0 := e.freshReg()
		r1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue { i8, i64 } undef, i8 %d, 0", r0, kmlTagArray))
		e.emitInstr(fmt.Sprintf("%s = insertvalue { i8, i64 } %s, i64 %s, 1", r1, r0, payload))
		return Value{Ref: r1, Ty: TypeAny}, nil
	}

	var tag int
	var payload string
	switch {
	case v.Ty.IsUndefined:
		tag = kmlTagUndefined
		payload = "0"
	case v.Ty.IsNull:
		tag = kmlTagNull
		payload = "0"
	case v.Ty.IR == "i1":
		tag = kmlTagBoolean
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", r, v.Ref))
		payload = r
	case v.Ty.Float:
		tag = kmlTagFloat
		val := v
		if v.Ty.IR == "float" {
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fpext float %s to double", r, v.Ref))
			val = Value{Ref: r, Ty: TypeF64}
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", r, val.Ref))
		payload = r
	case v.Ty.IsObject:
		tag = kmlTagObject
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", r, v.Ref))
		payload = r
	case v.Ty.IR == "ptr":
		tag = kmlTagString
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", r, v.Ref))
		payload = r
	default:
		tag = kmlTagInt
		payload = e.coerce(v, TypeI64).Ref
	}

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue { i8, i64 } undef, i8 %d, 0", r0, tag))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { i8, i64 } %s, i64 %s, 1", r1, r0, payload))
	return Value{Ref: r1, Ty: TypeAny}, nil
}

// emitUnboxTagPayload extracts the tag (i8) and payload (i64) registers from
// a boxed any/unknown Value.
func (e *Emitter) emitUnboxTagPayload(v Value) (tag, payload string) {
	tag = e.freshReg()
	payload = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i8, i64 } %s, 0", tag, v.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i8, i64 } %s, 1", payload, v.Ref))
	return tag, payload
}

// emitTagCheck emits `br i1 (icmp eq i8 tag, want), label matchL, label nextL`
// and returns the fresh match/next labels — the common per-tag dispatch step
// shared by emitDynamicToString/emitDynamicTypeof.
func (e *Emitter) emitTagCheck(tag string, want int, prefix string) (matchL, nextL string) {
	cond := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", cond, tag, want))
	matchL = e.freshLabel(prefix + ".match")
	nextL = e.freshLabel(prefix + ".next")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond, matchL, nextL))
	return matchL, nextL
}

// emitDynamicToString formats the current runtime value of a boxed any/unknown
// for console.log/template literals. Mirrors emitOptionalMember's shape: a
// result slot, one branch block per tag storing into it, and a merge block
// that loads the result — generalized from 2 branches to 7 (one per tag).
func (e *Emitter) emitDynamicToString(v Value) (Value, error) {
	tag, payload := e.emitUnboxTagPayload(v)

	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resPtr))
	mergeL := e.freshLabel("dynstr.merge")

	store := func(ref string) {
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ref, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	}

	e.ensureSprintf()
	e.ensureMalloc()

	matchL, nextL := e.emitTagCheck(tag, kmlTagInt, "dynstr.int")
	e.emitLabel(matchL)
	scratch := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 32)", scratch))
	fmtInt := e.internString("%lld")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s)", scratch, fmtInt, payload))
	store(scratch)
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagFloat, "dynstr.float")
	e.emitLabel(matchL)
	fscratch := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 32)", fscratch))
	fdouble := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", fdouble, payload))
	e.ensureDtoa() // JS-faithful shortest round-trip (TDD-00080)
	e.emitInstr(fmt.Sprintf("call void @__kml_dtoa(ptr %s, double %s)", fscratch, fdouble))
	store(fscratch)
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagString, "dynstr.string")
	e.emitLabel(matchL)
	sptr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", sptr, payload))
	store(sptr)
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagBoolean, "dynstr.bool")
	e.emitLabel(matchL)
	isTrue := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isTrue, payload))
	boolPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", boolPtr, isTrue, e.internString("true"), e.internString("false")))
	store(boolPtr)
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagNull, "dynstr.null")
	e.emitLabel(matchL)
	store(e.internString("null"))
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagUndefined, "dynstr.undef")
	e.emitLabel(matchL)
	store(e.internString("undefined"))
	e.emitLabel(nextL)

	// A boxed array stringifies to the `[object Array]` tag: the box holds only
	// the array's data pointer (see emitBoxValue), so the length and element
	// type needed to render its contents (`1,2,3`) are not recoverable — this
	// fixed tag string is the honest, non-garbage stand-in (a deviation from
	// JS's `String([1,2,3]) === "1,2,3"`, documented in TDD-00062).
	matchL, nextL = e.emitTagCheck(tag, kmlTagArray, "dynstr.array")
	e.emitLabel(matchL)
	store(e.internString("[object Array]"))
	e.emitLabel(nextL)

	// A boxed built-in-constructor reference stringifies the way real JS
	// stringifies a native function — the payload is the interned name.
	matchL, nextL = e.emitTagCheck(tag, kmlTagFuncRef, "dynstr.funcref")
	e.emitLabel(matchL)
	fnName := e.freshReg()
	fnBuf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", fnName, payload))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 64)", fnBuf))
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, ptr %s)", fnBuf, e.internString("function %s() { [native code] }"), fnName))
	store(fnBuf)
	e.emitLabel(nextL)

	// Remaining tag: object → "[object Object]", matching JS's
	// `String({}) === "[object Object]"` (a plain object has no useful
	// value-string, and its field contents aren't recovered from the boxed
	// pointer). Previously this branch `inttoptr`'d the payload and stored it
	// as a string pointer, printing the object's raw struct bytes as a C
	// string (garbage/empty) — a now-reachable bug once any-typed parameters
	// can carry an object.
	store(e.internString("[object Object]"))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", result, resPtr))
	return Value{Ref: result, Ty: TypePtr}, nil
}

// emitDynamicTypeof implements `typeof x` for a boxed any/unknown value: a
// genuine runtime tag dispatch, unlike every other typeof case (which stays
// fully compile-time — see emitUnary). null maps to "object", matching the
// well-known JS quirk (typeof null === "object").
func (e *Emitter) emitDynamicTypeof(v Value) (Value, error) {
	tag, _ := e.emitUnboxTagPayload(v)

	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resPtr))
	mergeL := e.freshLabel("dyntypeof.merge")

	store := func(label string) {
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString(label), resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	}

	matchL, nextL := e.emitTagCheck(tag, kmlTagInt, "dyntypeof.int")
	e.emitLabel(matchL)
	store("number")
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagFloat, "dyntypeof.float")
	e.emitLabel(matchL)
	store("number")
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagString, "dyntypeof.string")
	e.emitLabel(matchL)
	store("string")
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagBoolean, "dyntypeof.bool")
	e.emitLabel(matchL)
	store("boolean")
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagNull, "dyntypeof.null")
	e.emitLabel(matchL)
	store("object")
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagUndefined, "dyntypeof.undef")
	e.emitLabel(matchL)
	store("undefined")
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagFuncRef, "dyntypeof.funcref")
	e.emitLabel(matchL)
	store("function")
	e.emitLabel(nextL)

	// Remaining tag: object.
	store("object")

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", result, resPtr))
	return Value{Ref: result, Ty: TypePtr}, nil
}

// emitAnyEquals implements === / !== when either operand is any/unknown-typed:
// boxes whichever side isn't already dynamic (idempotent, so this works
// whether one or both sides are any-typed) and delegates to the runtime
// tag-aware comparison helper.
func (e *Emitter) emitAnyEquals(a, b Value, negate bool) (Value, error) {
	boxedA, err := e.emitBoxValue(a)
	if err != nil {
		return Value{}, err
	}
	boxedB, err := e.emitBoxValue(b)
	if err != nil {
		return Value{}, err
	}
	e.ensureAnyEq()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_any_eq({ i8, i64 } %s, { i8, i64 } %s)", result, boxedA.Ref, boxedB.Ref))
	if negate {
		neg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", neg, result))
		return Value{Ref: neg, Ty: TypeBool}, nil
	}
	return Value{Ref: result, Ty: TypeBool}, nil
}
