// emit_dynamic.go — real runtime-polymorphic support for any/unknown (TypeAny):
// boxing concrete values into a { i8 tag, i64 payload } struct, and the three
// operations that must dispatch on the runtime tag instead of a compile-time
// type: printing (console.log/template literals), typeof, and ===/!==.
//
// Tags: 0=int, 1=float, 2=string, 3=boolean, 4=null, 5=undefined, 6=object.
//
// Deliberately out of scope (see docs/adr for the any/unknown ADR): arithmetic
// operators, any/unknown as a function parameter/return/array/object-field
// type, and general unions beyond T | null. Those positions get a clean
// compiler error rather than silently accepting TypeAny and producing broken
// IR — see the guards in emit_func.go/emitter.go.
package llvm

import "fmt"

// containsDynamicElement reports whether ty is, or contains as an array
// element or object field, an any/unknown type. Used to reject the
// out-of-scope positions (array element, object field, function param/return)
// with a clean compiler error instead of silently producing broken IR — a
// top-level `ty.IsDynamic` check alone would miss e.g. `x: any[]`, where only
// ElemType is dynamic.
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

const (
	kmlTagInt       = 0
	kmlTagFloat     = 1
	kmlTagString    = 2
	kmlTagBoolean   = 3
	kmlTagNull      = 4
	kmlTagUndefined = 5
	kmlTagObject    = 6
)

// emitBoxValue converts any concrete Value into a Value{Ty: TypeAny}. Boxing
// is idempotent: if v is already dynamic, it's returned unchanged, so callers
// never need to check first.
func (e *Emitter) emitBoxValue(v Value) (Value, error) {
	if v.Ty.IsDynamic {
		return v, nil
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
	fmtFloat := e.internString("%g")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, double %s)", fscratch, fmtFloat, fdouble))
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

	// Remaining tag: object (not reachable in V1, no in-scope path boxes one,
	// but handled for completeness rather than left as undefined behavior).
	objPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", objPtr, payload))
	store(objPtr)

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
