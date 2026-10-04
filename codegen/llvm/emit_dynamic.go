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

import (
	"fmt"

	"KlainMainLang/ast"
)

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

// isSelfDescribingBox reports whether a value of type ty is a NaN box whose
// run-time tag alone says what it holds: bare any/unknown, or a constrained
// union of scalars (`number | string`). Such a value renders/serializes through
// the dynamic walkers exactly like `any`. A union with an object member is not
// one — its tag-6 payload is a static struct only the member types describe.
func isSelfDescribingBox(ty Type) bool {
	if !ty.IsDynamic {
		return false
	}
	for _, m := range ty.UnionMembers {
		// An array member's box carries its element kind, as an any's does.
		if scalarTypeKind(m) == "" && !m.IsArray {
			return false
		}
	}
	return true
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
// objectFieldDynamicRejected reports whether a dynamic type is disallowed as an
// OBJECT FIELD. Bare any/unknown (nil UnionMembers) is rejected; a *constrained*
// union (TDD-00119) is allowed, since its member set is checked and boxed at
// object-literal-field construction (storeScalarOrNullableField +
// unionAllowsAssignmentFrom) and at the return boundary.
// objectFieldDynamicRejected reports whether a field type is a dynamic type this
// compiler cannot yet represent in an object/class field slot. Both dynamic
// field kinds are now supported: a constrained-union field (`string | number`,
// TDD-00119) has long held an { i8, i64 } box, and a bare `any`/`unknown` field
// (TDD-00208) is now a NaN-box slot too (box-on-write / unbox-on-read, the
// field-shaped counterpart of the boxed-element array `any[]`, TDD-00200). So no
// dynamic field type is rejected here. (A constrained-union *array element*
// remains rejected — see containsDynamicElement — a distinct, still-open case.)
func objectFieldDynamicRejected(ty Type) bool {
	return false
}

func containsDynamicElement(ty Type) bool {
	if ty.IsArray && ty.ElemType != nil {
		et := *ty.ElemType
		// A bare any/unknown element is the boxed-element array (TDD-00200): a
		// real, supported representation (one NaN box per slot, box-on-write /
		// unbox-on-read over the normal array machinery), legal in BOTH -compat
		// lanes — an explicit `any[]` is the developer opting into boxing. A
		// *constrained union* element is still rejected: element-level union
		// member-set checking/boxing isn't wired (array-literal element
		// construction and HOF element passing would skip the check) — a
		// deliberate scope cut (TDD-00043).
		if et.IsDynamic {
			return false
		}
		return containsDynamicElement(et)
	}
	if ty.IsObject {
		for _, f := range ty.Fields {
			if objectFieldDynamicRejected(f.Ty) || containsDynamicElement(f.Ty) {
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
	if t.IsSymbol && !t.IsDynamic && !t.Nullable {
		return "symbol" // a primitive, though a struct and a pointer
	}
	if t.IsDynamic || t.IsArray || t.IsObject || t.IsNull || t.IsUndefined {
		return ""
	}
	switch {
	case t.IR == "i1":
		return "boolean"
	case t.IsBigInt:
		return "bigint" // a pointer, but no string
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
	var objectMembers []Type
	arrays, byteArrays := 0, 0
	for _, m := range ty.UnionMembers {
		if scalarTypeKind(m) != "" {
			continue
		}
		// A ReadableStream member (TDD-00119) is boxed under its own tag
		// (kmlTagStream), so it is runtime-distinguishable on its own and does not
		// count toward the "2+ object members need a discriminant" rule below.
		if m.IsReadableStream {
			continue
		}
		// One array member: boxed under the array tag, so runtime-distinguishable
		// too (`string | string[]`, narrowed by Array.isArray or the checker).
		if m.IsArray {
			// A plain array and a byte array (`string[] | Buffer`) box apart
			// (the box's typed byte); two of either kind do not.
			if m.IsTypedArray || m.IsBuffer {
				byteArrays++
			} else {
				arrays++
			}
			if arrays > 1 || byteArrays > 1 {
				return fmt.Errorf("%d:%d: a union with two or more array members of one kind is not supported yet", line, col)
			}
			continue
		}
		// An object/interface/class member is allowed (TDD-00115), boxed as tag 6.
		if isUnionObjectMember(m) {
			objectMembers = append(objectMembers, m)
			continue
		}
		return fmt.Errorf("%d:%d: union member types are limited to number, string, boolean (plus null/undefined), object/interface/class, and ReadableStream types", line, col)
	}
	// One object member: usable via `typeof x === "object"` narrowing (TDD-00115).
	// Two or more: allowed only as a *discriminated* union (TDD-00116) — every
	// object member shares a first-position string-literal tag field with a
	// distinct value, narrowed by `x.tag === "..."`.
	if len(objectMembers) >= 2 && !selfIdentifyingObjects(objectMembers) {
		if _, ok := unionDiscriminant(objectMembers); !ok {
			return fmt.Errorf("%d:%d: a union with two or more object members must be a discriminated union — every member needs a common first-position string-literal tag field with a distinct value (e.g. `{ kind: \"a\", ... } | { kind: \"b\", ... }`)", line, col)
		}
	}
	return nil
}

// unionDiscriminantField reports a union type's discriminant tag field: its name
// and its (string) value type. ok=false unless the union's object members form a
// discriminated union (TDD-00116).
func unionDiscriminantField(u Type) (name string, valTy Type, ok bool) {
	var objs []Type
	for _, m := range u.UnionMembers {
		if isUnionObjectMember(m) {
			objs = append(objs, m)
		}
	}
	n, okd := unionDiscriminant(objs)
	if !okd {
		return "", Type{}, false
	}
	return n, TypePtr, true
}

// unionDiscriminant returns the discriminant tag field name shared by a set of
// object union members, or ok=false if they don't form a discriminated union
// (TDD-00116). V1 rule: every member's FIRST field has the same name and a
// string-literal type, and the literal values are all distinct.
func unionDiscriminant(members []Type) (string, bool) {
	if len(members) < 2 {
		return "", false
	}
	var name string
	seen := map[string]bool{}
	for i, m := range members {
		if len(m.UserFields()) == 0 {
			return "", false
		}
		f := m.UserFields()[0]
		if !f.Ty.IsStrLiteral {
			return "", false
		}
		if i == 0 {
			name = f.Name
		} else if f.Name != name {
			return "", false
		}
		if seen[f.Ty.LitValue] {
			return "", false // duplicate tag value
		}
		seen[f.Ty.LitValue] = true
	}
	return name, true
}

// isUnionObjectMember reports whether a union member is a plain
// object/interface/class type (boxable as tag 6, usable via narrowing) — as
// opposed to an array, Map/Set, or other non-boxable aggregate.
func isUnionObjectMember(m Type) bool {
	// An index-signature dictionary is a plain object too (its box holds
	// the map pointer).
	if m.IsDynamicObject && !m.IsArray {
		return true
	}
	return m.IsObject && !m.IsArray && !m.IsMap && !m.IsSet && !m.IsTuple &&
		!m.IsDynamicObject
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
		if unionTy.Nullable {
			return true
		}
		if !valTy.Nullable || valTy.IR == "" || valTy.IR == "void" {
			return false
		}
		// A possibly-absent T (`T | undefined`, narrowed where the checker
		// proved it present): T is what must be a member.
		valTy.Nullable, valTy.IsNull, valTy.IsUndefined = false, false, false
	}
	// A dynamic member (`ArrayBufferView`, `any`) holds any value.
	for _, m := range unionTy.UnionMembers {
		if m.IsDynamic && m.UnionMembers == nil {
			return true
		}
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
	if valKind != "" {
		for _, m := range unionTy.UnionMembers {
			if scalarTypeKind(m) == valKind {
				return true
			}
		}
		return false
	}
	// An array value matches the union's array member.
	if valTy.IsArray {
		for _, m := range unionTy.UnionMembers {
			if m.IsArray {
				return true
			}
		}
		return false
	}
	// A ReadableStream value matches a ReadableStream member (TDD-00119). Chunk
	// element type isn't part of the match — the response writer treats any
	// stream member uniformly, and stream chunk types aren't otherwise narrowed.
	if valTy.IsReadableStream {
		for _, m := range unionTy.UnionMembers {
			if m.IsReadableStream {
				return true
			}
		}
		return false
	}
	// An object value matches the union's object member when it is structurally
	// assignable to it (width subtyping — extra fields ok), the same test used
	// for generic constraints (TDD-00115).
	if isUnionObjectMember(valTy) {
		for _, m := range unionTy.UnionMembers {
			if isUnionObjectMember(m) && (objectStructurallyAssignable(valTy, m) || needsObjectRelayoutBoxing(valTy, m)) {
				return true
			}
		}
	}
	// A class instance satisfies a union's plain object member through the
	// checked view (TDD-00233); the checker proved the members are there.
	if valTy.IsClass {
		for _, m := range unionTy.UnionMembers {
			if isRecordView(m) {
				return true
			}
		}
	}
	return false
}

// relayoutForUnion copies an object value into the layout of the union's
// object member it is assigned to, when the two layouts differ (`{cert, key}`
// passed as a `TlsOptions | fn`): the box carries the member's layout, which
// is how every read of the union's object member sees it.
func (e *Emitter) relayoutForUnion(v Value, unionTy Type) Value {
	if v.Ty.IsClass {
		// Kept as itself; a narrowed member reads it through the view.
		for _, m := range unionTy.UnionMembers {
			if isRecordView(m) {
				e.noteRecordViewSource(v.Ty, m)
			}
		}
		return v
	}
	if !isUnionObjectMember(v.Ty) {
		return v
	}
	for _, m := range unionTy.UnionMembers {
		if !isUnionObjectMember(m) {
			continue
		}
		if plainRecordType(m) && sameFieldLayout(v.Ty, m) {
			e.noteRecordViewSource(v.Ty, m)
			return v
		}
	}
	for _, m := range unionTy.UnionMembers {
		if isUnionObjectMember(m) && needsObjectRelayoutBoxing(v.Ty, m) {
			// Kept as itself; the narrowed member reads it through the view.
			e.noteRecordViewSource(v.Ty, m)
			return v
		}
	}
	return v
}

// objectStructurallyAssignable reports whether an object value of type val may
// be assigned to an object member type m — width subtyping: val must carry every
// field m declares, with a matching kind (scalar kind, or a recursively
// assignable object; arrays match on element kind). val may have extra fields.
func objectStructurallyAssignable(val, m Type) bool {
	for _, mf := range m.UserFields() {
		vf, ok := fieldByName(val, mf.Name)
		if !ok {
			return false
		}
		switch {
		case isUnionObjectMember(mf.Ty):
			if !isUnionObjectMember(vf.Ty) || !objectStructurallyAssignable(vf.Ty, mf.Ty) {
				return false
			}
		case mf.Ty.IsArray:
			if !vf.Ty.IsArray {
				return false
			}
		case scalarTypeKind(mf.Ty) != "":
			if scalarTypeKind(vf.Ty) != scalarTypeKind(mf.Ty) {
				return false
			}
		default:
			if vf.Ty.IR != mf.Ty.IR {
				return false
			}
		}
	}
	return true
}

func fieldByName(t Type, name string) (Field, bool) {
	for _, f := range t.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
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
	// kmlTagStream boxes a ReadableStream (TDD-00119): a ptr-shaped runtime value
	// that must NOT collide with kmlTagString (both are `IR=="ptr"`). The payload
	// is the stream pointer ptrtoint'd; `typeof` → "object", `===` → reference
	// equality on the pointer (like an array). Lets `string | ReadableStream`
	// round-trip through a union box — e.g. an http.listen response `body` field.
	kmlTagStream = 9
	// kmlTagDynObject boxes a D1 dynamic object (TDD-00155): the payload is a
	// ptrtoint'd pointer to a __kml_dynobj bag (runtime_dynobj.go) — a
	// per-instance property table whose values are themselves { i8, i64 }
	// boxes. `typeof` → "object" and toString → "[object Object]" fall out of
	// the existing remaining-tag arms; `===` is reference equality on the bag
	// pointer (a check_obj_ref case in __kml_any_eq).
	kmlTagDynObject = 10
	// kmlTagDynArray boxes a D1 dynamic array (TDD-00155 Stage 2): the payload
	// is a ptrtoint'd __kml_dynarr header whose elements are themselves boxes
	// (runtime_dynarr.go) — the element universe untyped JSON.parse needs.
	// `typeof` → "object"; `===` is reference equality on the header pointer;
	// toString is the JS Array join (via __kml_dynarr_join).
	kmlTagDynArray = 11
	// kmlTagDynFunc boxes a dynamic function (TDD-00155 Stage 4): the payload
	// is a ptrtoint'd { fnptr, env, i64 arity } record compiled under the
	// uniform dynamic ABI (emit_dynfunc.go) — the value behind vanilla-JS
	// prototype methods. `typeof` → "function"; `===` is record identity
	// (one record per function-expression evaluation, like a JS closure).
	kmlTagDynFunc = 12
	// kmlTagError is the logical tag for a caught Error in the unpacked
	// thrown-value record (TDD-00202). It has NO packed NaN-box encoding (the
	// 3-bit pointer-kind space is full) — it exists only in the {i8 tag, i64
	// payload} record used for the thrown slot and catch variable, where the
	// payload is a ptrtoint'd errorObjType pointer. Packing a caught Error into a
	// real NaN-box `any` downgrades it to kmlTagObject (it is an object); the
	// Error shape is only available through the record, in catch-local scope.
	kmlTagError = 13
)

// emitBoxValue converts any concrete Value into a Value{Ty: TypeAny}. Boxing
// is idempotent: if v is already dynamic, it's returned unchanged, so callers
// never need to check first.
func (e *Emitter) emitBoxValue(v Value) (Value, error) {
	if v.Ty.IsDynamic {
		return v, nil
	}
	if v.Ty.NullAndUndef {
		boxed, err := e.emitBoxValue(e.fromThreeState(v))
		if err != nil {
			return Value{}, err
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", r, e.isTriNull(v), nbNull, boxed.Ref))
		return Value{Ref: r, Ty: boxed.Ty}, nil
	}
	// A void-typed operand reaching a value position is exactly `undefined` in JS
	// — a call to a spec-undefined-returning method (`set.clear()`, `arr.forEach()`)
	// used as an argument or a compared value. Box it as undefined rather than
	// falling through to the numeric default, which would emit `sitofp i64 <no
	// reg>` from the absent void result (ADR-00900).
	if v.Ty.IR == "void" {
		return Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}, nil
	}
	// A caught value (TypeCaught, TDD-00202) is a { i8 tag, i64 payload }
	// record: pack it (an Error becomes an object box) — the numeric default
	// below read the aggregate as a number, so a caught error passed to an
	// `any` parameter arrived as `typeof "number"` (TDD-00229).
	if v.Ty.IsCaught {
		return e.emitCaughtToAny(v), nil
	}
	// A nullable scalar (`number | null`, …) is a { i1, T } aggregate, not a bare
	// scalar — box the payload (recursively, as its own scalar) when present, or
	// its absence when absent: `undefined` for a `T | undefined`, `null` for a
	// `T | null` (TDD-00123).
	if isNullableScalar(v.Ty) {
		present, payload := e.nullableScalarAggParts(v)
		boxedVal, err := e.emitBoxValue(payload)
		if err != nil {
			return Value{}, err
		}
		absent := int64(nbUndefined)
		if !v.Ty.IsUndefined {
			absent = nbNull
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %d", r, present, boxedVal.Ref, absent))
		return Value{Ref: r, Ty: TypeAny}, nil
	}
	// An array value is a { ptr, i64 } aggregate (data pointer + length), which
	// doesn't fit the box's single i64 payload slot. The payload is a small box
	// cell (anyArrayBoxTy) whose field 0 is the LIVE array header pointer and
	// field 1 the element-kind byte the render/stringify site consults (TDD-00212).
	// Storing the live header (Stage 3) — the same {data,len} cell the source
	// array mutates through (TDD-00127) — rather than a snapshot makes a post-box
	// `push` visible through the box; identity is that header, so __kml_any_eq's
	// array arm (a field-0 compare) stays stable across a reallocating push.
	// `typeof` stays "object". Before TDD-00212 the element type was dropped and
	// toString fell back to `[object Array]` (TDD-00062); now a flat
	// number/string/boolean/int array renders its contents, and an element kind
	// not representable at box time stores the sentinel -1 and keeps the
	// `[object Array]` stand-in.
	// A tuple is an array at run time: through `any` it is an any[] of its
	// elements, boxed (a copy — the tuple's struct has no array header to
	// share).
	if v.Ty.IsTuple && !v.Ty.IsArray {
		return e.boxTupleAsArray(v)
	}
	if v.Ty.IsArray {
		hdr := e.boxAnyArray(v)
		boxed := e.emitNbTagPtr(hdr, kmlTagArray)
		if v.Ty.Nullable {
			// An absent `T[] | null` (a missed RegExp exec) boxes as its
			// keyword, not an empty array.
			absentWord := int64(nbNull)
			if v.Ty.IsUndefined {
				absentWord = nbUndefined
			}
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", sel, e.emitArrayIsAbsent(v), absentWord, boxed))
			return Value{Ref: sel, Ty: TypeAny}, nil
		}
		return Value{Ref: boxed, Ty: TypeAny}, nil
	}

	// The null pointer of a `T | null` / `T | undefined` pointer (a string, an
	// object) boxes as that flavour's sentinel; a present one as itself.
	absent := int64(nbNull)
	if v.Ty.IsUndefined {
		absent = nbUndefined
	}
	switch {
	case v.Ty.IsUndefined && !v.Ty.Nullable: // the `undefined` literal type
		return Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}, nil
	case v.Ty.IsNull:
		return Value{Ref: fmt.Sprintf("%d", nbNull), Ty: TypeAny}, nil
	case v.Ty.IR == "i1":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %d", r, v.Ref, nbTrue, nbFalse))
		return Value{Ref: r, Ty: TypeAny}, nil
	case v.Ty.Float:
		val := v
		if v.Ty.IR == "float" {
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fpext float %s to double", r, v.Ref))
			val = Value{Ref: r, Ty: TypeF64}
		}
		return Value{Ref: e.emitNbEncodeDouble(val.Ref), Ty: TypeAny}, nil
	case v.Ty.IsFunc:
		// A statically-typed closure boxes through a per-signature dynamic-ABI
		// adapter (emit_dynfunc.go): the tag-12 record's env carries the
		// closure header, the adapter unboxes arguments to the concrete
		// parameter types and boxes the result. No closure (a `(() => T) |
		// null` holding null) is null.
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
		nullL, boxL, joinL := e.freshLabel("boxfn.null"), e.freshLabel("boxfn.box"), e.freshLabel("boxfn.join")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nullL, boxL))
		e.emitLabel(boxL)
		boxed, err := e.emitDynClosureAdapter(v)
		if err != nil {
			// Close the branch, so a caller that takes another route from
			// here still emits well-formed IR.
			e.emitTerminator("unreachable")
			e.emitLabel(nullL)
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(joinL)
			return Value{}, err
		}
		boxEnd := e.freshLabel("boxfn.boxed")
		e.emitTerminator(fmt.Sprintf("br label %%%s", boxEnd))
		e.emitLabel(boxEnd)
		e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
		e.emitLabel(nullL)
		e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
		e.emitLabel(joinL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = phi i64 [ %s, %%%s ], [ %d, %%%s ]", r, boxed.Ref, boxEnd, nbNull, nullL))
		return Value{Ref: r, Ty: TypeAny}, nil
	case hostCellObject(v.Ty):
		// A headerless host object (a RegExp): a host cell (emit_hostbox.go).
		return e.emitBoxHost(v), nil
	case isStringDict(v.Ty) && v.Ty.Nullable:
		// An optional dictionary: absent boxes as undefined (or null).
		absentWord := int64(nbUndefined)
		if !v.Ty.IsUndefined {
			absentWord = nbNull
		}
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
		presL, absL, joinL := e.freshLabel("dictbox.present"), e.freshLabel("dictbox.absent"), e.freshLabel("dictbox.join")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, absL, presL))
		e.emitLabel(presL)
		present := v
		present.Ty = v.Ty.withoutNullable()
		bag, err := e.emitDictToBag(present)
		if err != nil {
			return Value{}, err
		}
		presEnd := e.freshLabel("dictbox.boxed")
		e.emitTerminator(fmt.Sprintf("br label %%%s", presEnd))
		e.emitLabel(presEnd)
		e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
		e.emitLabel(absL)
		e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
		e.emitLabel(joinL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = phi i64 [ %s, %%%s ], [ %d, %%%s ]", r, bag.Ref, presEnd, absentWord, absL))
		return Value{Ref: r, Ty: TypeAny}, nil
	case isStringDict(v.Ty):
		// An index-signature dictionary boxes as a dynamic object of its
		// entries (its keys, values and null prototype), which every dynamic
		// read, Object.keys and console.log understand; unboxing it into a
		// dictionary copies the entries back (emitBoxedObjToDict).
		return e.emitDictToBag(v)
	case v.Ty.IsObject || v.Ty.IsDynamicObject:
		// An index-signature dictionary boxes as its map pointer, as an object
		// does: a union holding it unboxes it back.
		e.noteBoxedLayout(v.Ty)
		tagged := e.emitNbTagPtr(v.Ref, kmlTagObject)
		// A nullable object (`C | null`) that is null at runtime must box as the
		// nbNull sentinel, not an object-tagged 0 payload; otherwise `x === null`
		// reads false once the value crosses into an `any` slot (BACKLOG §3 residue,
		// mirroring the `string | null` ptr arm below — ADR-00958).
		if v.Ty.Nullable {
			isNull := e.freshReg()
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", sel, isNull, absent, tagged))
			return Value{Ref: sel, Ty: TypeAny}, nil
		}
		return Value{Ref: tagged, Ty: TypeAny}, nil
	case v.Ty.IsPromise:
		// A promise: its Promise<any> behind a wrapper (emit_anyprom.go). A raw
		// fetch handle is bridged to a task promise first.
		pv := v
		if !pv.Ty.PromiseTask && pv.Ty.PromiseType != nil && pv.Ty.PromiseType.IsResponse && !pv.Ty.PromiseResolved {
			pv = e.emitFetchHandleToPendingPromise(pv.Ref)
		}
		// Every other promise value is task-shaped (TDD-00084 Part A: an async
		// callable returns one even where its type does not say so).
		return e.emitBoxPromise(pv), nil
	case v.Ty.IsGenerator:
		// A generator is a headered object; its layout row exposes next/return/
		// throw and the iterator protocol (emit_shape.go).
		e.noteBoxedLayout(v.Ty)
		return Value{Ref: e.emitNbTagPtr(v.Ref, kmlTagObject), Ty: TypeAny}, nil
	case v.Ty.IsReadableStream:
		return Value{Ref: e.emitNbTagPtr(v.Ref, kmlTagStream), Ty: TypeAny}, nil
	case v.Ty.IsBigInt:
		// A bigint boxes as a { magic, ptr } cell (emit_bigint_box.go) — the
		// string-kind fall-through below read it as a string (TDD-00229).
		return e.emitBoxBigInt(v), nil
	case isHostHandle(v.Ty):
		// A host handle: a headered cell (emit_hostbox.go).
		return e.emitBoxHost(v), nil
	case v.Ty.IR == "ptr":
		// String: kind bits 0 — the value IS the pointer.
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", r, v.Ref))
		// A NULLABLE pointer (a `string | null` — e.g. `URLSearchParams.get(missing)`
		// — whose null representation is the null pointer) that is null at runtime
		// must box as the `nbNull` sentinel, not a string-tagged box with a 0
		// payload; otherwise `x === null` reads false once the value crosses into
		// an `any` slot (ADR-00958, the WPT-blocking half of BACKLOG §3).
		if v.Ty.Nullable {
			isNull := e.freshReg()
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", sel, isNull, absent, r))
			return Value{Ref: sel, Ty: TypeAny}, nil
		}
		return Value{Ref: r, Ty: TypeAny}, nil
	default:
		// Integers become encoded doubles — a JS number IS a double
		// (TDD-00156); precision past 2^53 rounds exactly as JS does.
		iv := e.coerce(v, TypeI64)
		d := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", d, iv.Ref))
		return Value{Ref: e.emitNbEncodeDouble(d), Ty: TypeAny}, nil
	}
}

// anyArrayBoxTy is the LLVM type of the any-boxed array box (TDD-00212): field 0
// is the LIVE array header pointer (arrayHeaderTy — the shared {data,len} cell an
// array's push/reassign writes through, object-reference model TDD-00127), field
// 1 the element-kind byte the stringify/inspect site consults. Storing the header
// pointer rather than a {data,len} snapshot (Stage 3) makes a post-box mutation
// visible through the box and makes identity the shared header, so __kml_any_eq's
// array arm — which compares field 0 — stays stable across a reallocating push.
// Render/unbox sites deref field 0 to reach data/len (one extra load).
// Field 2 (ADR-01059) is the typed-array byte — 0 plain array, 1 TypedArray,
// 2 Uint8ClampedArray — so a boxed `Int32Array` keeps its identity through
// `any` (inspect prefix, JSON object form) instead of collapsing into a plain
// int32 array. dynjson.c's KjBox mirrors this layout.
// Fields 3/4 describe the elements of a nested-array element (kind KJ_ARRAY
// = 13, `T[][]`): the inner arrays' own element kind and typed byte, so one
// more level reads/renders through the box.
// Fields 5-7 serve an element kind the byte cannot describe (KJ_BOXED = 14:
// an object, a class instance, a Map, a `T | null` number, …): the element's
// storage size, and routines generated for its element type that box element
// i (`i64 (ptr data, i64 i)`) and store a box into element i
// (`void (ptr data, i64 i, i64 box)`) — anyArrayElemRoutines.
const anyArrayBoxTy = "{ ptr, i8, i8, i8, i8, i32, ptr, ptr }"

// anyArrayBoxSize is anyArrayBoxTy's size in bytes.
const anyArrayBoxSize = 32

// kjBoxed is dynjson.c's KJ_BOXED element kind.
const kjBoxed = 14

// Values of anyArrayBoxTy field 2.
const (
	anyArrayPlain   = 0
	anyArrayTyped   = 1
	anyArrayClamped = 2
	anyArrayBuffer  = 3 // a Node Buffer (a Uint8Array to the element walker)
)

// emitBoxIsTypedArray is an i1 that the boxed value v is a static array box
// whose typed byte is typed (a Buffer, anyArrayBuffer). The box is read only
// when its tag says it is one.
func (e *Emitter) emitBoxIsTypedArray(v Value, typed int) string {
	tag, payload := e.emitUnboxTagPayload(v)
	isArr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isArr, tag, kmlTagArray))
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resPtr))
	e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", resPtr))
	readL := e.freshLabel("boxtyped.read")
	mergeL := e.freshLabel("boxtyped.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, readL, mergeL))
	e.emitLabel(readL)
	box := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", box, payload))
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", gep, anyArrayBoxTy, box))
	b := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", b, gep))
	is := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", is, b, typed))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", is, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(mergeL)
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", res, resPtr))
	return res
}

// arrayElemKind maps a static array element type to the descriptor
// __kml_array_join strides/formats by, or (-1, false) when the kind isn't a flat
// scalar/string this stage renders — a nested array, object, boxed-any, or other
// element, for which the box carries -1 and the render keeps `[object Array]`.
// Keep the returned integers in sync with the KJ_* enum in dynjson.c.
func arrayElemKind(elemTy Type) (int, bool) {
	switch {
	case elemTy.Float && elemTy.IR == "double":
		return 0, true // KJ_F64
	case elemTy.Float && elemTy.IR == "float":
		return 1, true // KJ_F32
	case elemTy.IR == "i1":
		return 10, true // KJ_BOOL
	case isForOfStringTy(elemTy):
		return 11, true // KJ_STRING
	case elemTy.IsDynamic:
		// An `any[]` element is itself a NaN-boxed word (ADR-01059).
		return 12, true // KJ_ANY
	}
	if elemTy.IsInteger() {
		signed := elemTy.Signed
		switch elemTy.IR {
		case "i64":
			if signed {
				return 2, true
			}
			return 3, true
		case "i32":
			if signed {
				return 4, true
			}
			return 5, true
		case "i16":
			if signed {
				return 6, true
			}
			return 7, true
		case "i8":
			if signed {
				return 8, true
			}
			return 9, true
		}
	}
	return -1, false
}

// anyArrayBoxBytes computes an array type's box kind and typed bytes. A
// BigInt64Array's raw i64 elements are bigint handles at the language boundary,
// not numbers — the walker would print/convert them wrongly, so the box
// carries -1 (renders as a named placeholder, refuses element reads).
func anyArrayBoxBytes(t Type) (kind, typed int) {
	kind = -1
	if t.ElemType != nil && !t.BigIntElem {
		if k, ok := arrayElemKind(*t.ElemType); ok {
			kind = k
		}
	}
	typed = anyArrayPlain
	switch {
	case t.IsBuffer:
		typed = anyArrayBuffer
	case t.Clamped:
		typed = anyArrayClamped
	case t.IsTypedArray && !t.IsBuffer:
		typed = anyArrayTyped
	}
	return kind, typed
}

// boxAnyArray heap-allocates the any-array box header (anyArrayBoxTy), stores the
// array aggregate ({ ptr, i64 }) at offset 0 and the element-kind descriptor at
// offset 16, and returns the header pointer — the payload of a kmlTagArray box
// (TDD-00212).
func (e *Emitter) boxAnyArray(v Value) string {
	e.ensureMalloc()
	e.ensureCalloc()
	kind, typed := anyArrayBoxBytes(v.Ty)
	if typed != anyArrayPlain {
		e.ensureBoxedViews()
	}
	innerKind, innerTyped := -1, anyArrayPlain
	if v.Ty.ElemType != nil && v.Ty.ElemType.IsArray {
		kind = 13 // KJ_ARRAY: elements are header pointers
		innerKind, innerTyped = anyArrayBoxBytes(*v.Ty.ElemType)
	}
	// The box holds the LIVE array header pointer (Stage 3). When the value came
	// from a named array, reuse its header (v.ArrayHeader) so mutations through
	// the original array show up; a transient array expression mints a fresh
	// header from its {data,len} aggregate (no caller-visible identity to share).
	header := v.ArrayHeader
	if header == "" {
		data := e.freshReg()
		length := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", data, v.Ref))
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", length, v.Ref))
		header = e.newArrayHeader(data, length)
	}
	boxer, unboxer := "null", "null"
	if kind < 0 && v.Ty.ElemType != nil && !v.Ty.BigIntElem && !v.Ty.IsTypedArray {
		if b, u, ok := e.anyArrayElemRoutines(*v.Ty.ElemType); ok {
			kind, boxer, unboxer = kjBoxed, b, u
		}
	}
	box := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", box, anyArrayBoxSize))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", header, box))
	if kind == kjBoxed {
		elemIR := StructFieldIR(*v.Ty.ElemType)
		szp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr null, i64 1", szp, elemIR))
		sz := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i32", sz, szp))
		for i, f := range []struct{ ir, v string }{{"i32", sz}, {"ptr", boxer}, {"ptr", unboxer}} {
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, anyArrayBoxTy, box, 5+i))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", f.ir, f.v, gep, map[string]int{"i32": 4, "ptr": 8}[f.ir]))
		}
	}
	kindGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", kindGep, anyArrayBoxTy, box))
	e.emitInstr(fmt.Sprintf("store i8 %d, ptr %s, align 1", kind, kindGep))
	for i, b := range []int{typed, innerKind, innerTyped} {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, anyArrayBoxTy, box, i+2))
		e.emitInstr(fmt.Sprintf("store i8 %d, ptr %s, align 1", b, gep))
	}
	return box
}

// anyArrayElemRoutines are the KJ_BOXED routines for an element type: one
// boxing element i of an array's data, one storing a box into element i
// (converted as an assignment from `any` converts). ok is false for an
// element the box describes otherwise (a flat @value struct).
func (e *Emitter) anyArrayElemRoutines(elem Type) (boxer, unboxer string, ok bool) {
	if elem.Inline || elem.IsArray || elem.IR == "" || elem.IR == "void" {
		return "", "", false
	}
	key := layoutFieldKey(elem) + "|" + elem.IR
	if e.anyArrElemFns == nil {
		e.anyArrElemFns = map[string][2]string{}
	}
	if fns, ok := e.anyArrElemFns[key]; ok {
		return fns[0], fns[1], true
	}
	elemIR := StructFieldIR(elem)

	restore := e.beginDetachedFunc()
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%data, i64 %%i", gep, elemIR))
	val := e.loadArrayElem(gep, elem)
	boxed, err := e.emitBoxValue(val)
	if err != nil {
		boxed = Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}
	}
	e.emitTerminator(fmt.Sprintf("ret i64 %s", boxed.Ref))
	body := e.allocas.String() + e.body.String()
	restore()
	boxer = e.defineContentNamed("@__kml_arrelem_box.", "i64", "ptr %data, i64 %i", body)

	restore = e.beginDetachedFunc()
	gep = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%data, i64 %%i", gep, elemIR))
	conv := e.coerce(Value{Ref: "%w", Ty: TypeAny}, elem)
	e.storeArrayElem(gep, elem, conv)
	e.emitTerminator("ret void")
	body = e.allocas.String() + e.body.String()
	restore()
	unboxer = e.defineContentNamed("@__kml_arrelem_unbox.", "void", "ptr %data, i64 %i, i64 %w", body)
	e.anyArrElemFns[key] = [2]string{boxer, unboxer}
	return boxer, unboxer, true
}

// emitNbEncodeDouble encodes a double register as a NaN-boxed number
// (canonicalize NaN, add the double-encode offset).
func (e *Emitter) emitNbEncodeDouble(dReg string) string {
	isnan := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp uno double %s, %s", isnan, dReg, dReg))
	bits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", bits, dReg))
	canon := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 9221120237041090560, i64 %s", canon, isnan, bits))
	enc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", enc, canon, nbDoubleOffset))
	// The slot's undefined (TDD-00241) boxes as undefined.
	isU := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isU, bits, undefF64))
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", out, isU, nbUndefined, enc))
	return out
}

// emitNbTagPtr tags a pointer register with its NaN-box kind bits.
func (e *Emitter) emitNbTagPtr(ptrReg string, tag int) string {
	kind := map[int]int64{kmlTagString: 0, kmlTagObject: 1, kmlTagArray: 2, kmlTagFuncRef: 3,
		kmlTagStream: 4, kmlTagDynObject: 5, kmlTagDynArray: 6, kmlTagDynFunc: 7}[tag]
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", r, ptrReg))
	if kind == 0 {
		return r
	}
	o := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i64 %s, %d", o, r, kind))
	return o
}

// emitUnboxTagPayload decodes a NaN-boxed value into the logical (tag,
// payload) pair every dispatch site switches on — a number always decodes
// to kmlTagFloat with its double bits as the payload.
func (e *Emitter) emitUnboxTagPayload(v Value) (tag, payload string) {
	e.ensureNanBox()
	tag = e.freshReg()
	payload = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_nb_tag(i64 %s)", tag, v.Ref))
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", payload, v.Ref))
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
	return e.emitDynamicRender(v, false)
}

// emitDynamicInspect is the console.log rendering of a boxed any/unknown value:
// the Node util.inspect form (`[ 1, 2, 3 ]`, quoted string elements) rather than
// the flat String() join (`1,2,3`). Scalars print identically to ToString at the
// top level (numbers/booleans bare, strings unquoted); only a boxed array differs
// (TDD-00212 Stage 2). Everything else routes through the shared dispatch.
func (e *Emitter) emitDynamicInspect(v Value) (Value, error) {
	return e.emitDynamicRenderAt(v, true, 0)
}

// emitDynamicInspectAt is emitDynamicInspect for a value nested `depth` levels
// inside a statically inspected container, so the dynamic walker continues
// Node's depth/indentation instead of restarting at the top (ADR-01067).
func (e *Emitter) emitDynamicInspectAt(v Value, depth int) (Value, error) {
	return e.emitDynamicRenderAt(v, true, depth)
}

// emitDynamicRender is the shared tag dispatch behind emitDynamicToString and
// emitDynamicInspect. `inspect` selects the console.log bracket form for a boxed
// array; every other tag renders the same in both modes.
func (e *Emitter) emitDynamicRender(v Value, inspect bool) (Value, error) {
	return e.emitDynamicRenderAt(v, inspect, 0)
}

func (e *Emitter) emitDynamicRenderAt(v Value, inspect bool, depth int) (Value, error) {
	if !inspect {
		// TDD-00201 Stage 4: a dynamic object stringifies via its own toString /
		// @@toPrimitive (string hint) rather than the "[object Object]" default —
		// `String({toString(){return "hi"}})` is "hi". Runtime ToPrimitive returns a
		// primitive box (or "[object Object]" when the object has no such method); a
		// non-object box passes through, so this is a no-op for scalars. console.log
		// (inspect) does NOT call toString — it inspects — so this step is skipped.
		v = Value{Ref: e.emitAnyToPrimitive(v.Ref, true), Ty: TypeAny}
	}

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
	scratch := e.emitStringScratch(32) // TDD-00120
	fmtInt := e.internString("%lld")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s)", scratch, fmtInt, payload))
	e.emitStringFinalizeLen(scratch)
	store(scratch)
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagFloat, "dynstr.float")
	e.emitLabel(matchL)
	fscratch := e.emitStringScratch(32) // TDD-00120
	fdouble := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", fdouble, payload))
	e.ensureDtoa() // JS-faithful shortest round-trip (TDD-00080)
	e.emitInstr(fmt.Sprintf("call void @__kml_dtoa(ptr %s, double %s)", fscratch, fdouble))
	e.emitStringFinalizeLen(fscratch)
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

	// A boxed array stringifies via the JS Array join (`String([1,2,3]) ===
	// "1,2,3"`): the box header carries the (ptr, len) pair plus an element-kind
	// byte (TDD-00212), so __kml_array_join can stride the buffer and format each
	// element. An element kind not representable at box time carries -1, for which
	// the helper returns the honest `[object Array]` stand-in (TDD-00062).
	matchL, nextL = e.emitTagCheck(tag, kmlTagArray, "dynstr.array")
	e.emitLabel(matchL)
	e.ensureDynJSONC()
	aBox := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", aBox, payload))
	// The walker reads the box itself: field 0 is the live array header (so a
	// post-box push is reflected — TDD-00212 Stage 3), field 1 the element
	// kind, field 2 the typed-array byte (ADR-01059).
	aStr := e.freshReg()
	// console.log → the util.inspect bracket form (`[ 1, 2, 3 ]`, a TypedArray
	// as `Int32Array(3) [ … ]`); String() / interpolation → the flat Array join
	// (`1,2,3`). Both share the element stride/format on the same box.
	if inspect {
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_array_inspect_at(ptr %s, i64 %d)", aStr, aBox, depth))
	} else {
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_array_join(ptr %s)", aStr, aBox))
	}
	store(aStr)
	e.emitLabel(nextL)

	// A boxed built-in-constructor reference stringifies the way real JS
	// stringifies a native function — the payload is the interned name.
	matchL, nextL = e.emitTagCheck(tag, kmlTagFuncRef, "dynstr.funcref")
	e.emitLabel(matchL)
	fnName := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", fnName, payload))
	fnBuf := e.emitStringScratch(64) // TDD-00120
	// console.log inspects (`[Function: TypeError]`); String() gives the
	// native-function source form.
	fnFmt := "function %s() { [native code] }"
	if inspect {
		fnFmt = "[Function: %s]"
		// A class boxed as a value inspects as `[class C] { statics }`.
		e.ensureDynJSONC()
		e.ensureClassRefInspect()
		cls := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_classref_shown(ptr %s)", cls, fnName))
		isCls := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isCls, cls))
		clsL, fnL := e.freshLabel("dynstr.classref"), e.freshLabel("dynstr.nativefn")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isCls, clsL, fnL))
		e.emitLabel(clsL)
		full := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_classref_inspect_at(ptr %s, i64 %d)", full, fnName, depth))
		store(full)
		e.emitLabel(fnL)
	}
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, ptr %s)", fnBuf, e.internString(fnFmt), fnName))
	e.emitStringFinalizeLen(fnBuf)
	store(fnBuf)
	e.emitLabel(nextL)

	// A dynamic (prototype-method) function stringifies like a native
	// function — the source text isn't retained.
	matchL, nextL = e.emitTagCheck(tag, kmlTagDynFunc, "dynstr.dynfn")
	e.emitLabel(matchL)
	if inspect {
		// util.inspect's `[Function: name]` form (TDD-00229).
		e.ensureFnMeta()
		rec := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", rec, payload))
		fs := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fn_inspect_dyn(ptr %s, i64 %d)", fs, rec, depth))
		store(fs)
	} else {
		store(e.internString("function () { [native code] }"))
	}
	e.emitLabel(nextL)

	// A dynamic array stringifies as its JS Array join ("1,hi,true"), matching
	// `String([1,'hi',true])` — the C walker handles nesting/null/undefined.
	matchL, nextL = e.emitTagCheck(tag, kmlTagDynArray, "dynstr.dynarr")
	e.emitLabel(matchL)
	e.ensureDynJSONC()
	daHdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", daHdr, payload))
	daStr := e.freshReg()
	// console.log → bracket inspect form (`[ 1, 'hi', true ]`); String() → the
	// flat comma join (`1,hi,true`). TDD-00212 Stage 2.
	if inspect {
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_inspect_at(ptr %s, i64 %d)", daStr, daHdr, depth))
	} else {
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_join(ptr %s)", daStr, daHdr))
	}
	store(daStr)
	e.emitLabel(nextL)

	// A dynamic object under console.log renders in its util.inspect form
	// (`{ a: 1, b: 'x' }` — keys in enumeration order, accessors as [Getter],
	// Node's default depth-2 collapse). String()/interpolation never reaches
	// this tag with a plain object: emitAnyToPrimitive above already resolved
	// it (own toString, else the "[object Object]" primitive).
	if inspect {
		matchL, nextL = e.emitTagCheck(tag, kmlTagDynObject, "dynstr.dynobj")
		e.emitLabel(matchL)
		e.ensureDynJSONC()
		doPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", doPtr, payload))
		doStr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_inspect_at(ptr %s, i64 %d)", doStr, doPtr, depth))
		store(doStr)
		e.emitLabel(nextL)
	}

	// A boxed built-in Error (field-0 type-id flag set, low bits a builtin
	// kind) renders as `name: message` via emitErrorToString — matching
	// `String(new Error("x")) === "Error: x"`. This recovers the Error shape a
	// boxed `any` otherwise loses (TDD-00222); a boxed plain object (or an
	// error-subclass instance, whose fields don't line up) keeps the default.
	matchL, nextL = e.emitTagCheck(tag, kmlTagObject, "dynstr.obj")
	e.emitLabel(matchL)
	// A boxed bigint (emit_bigint_box.go): its digits, with console.log's
	// trailing `n` (TDD-00229).
	bigCell, isBig := e.emitBoxedBigIntProbe(payload)
	bigL := e.freshLabel("dynstr.obj.bigint")
	notBigL := e.freshLabel("dynstr.obj.notbig")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBig, bigL, notBigL))
	e.emitLabel(bigL)
	// Through the finalize-time hook, so rendering an `any` never drags the
	// bigint runtime into a program that has no bigints.
	e.ensureBoxedBigIntHooks()
	bigDigits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_boxed_bigint_str(ptr %s)", bigDigits, bigCell))
	bigStr := Value{Ref: bigDigits, Ty: TypePtr}
	if inspect {
		var err error
		bigStr, err = e.emitStringConcat(bigStr, Value{Ref: e.internString("n"), Ty: TypePtr})
		if err != nil {
			return Value{}, err
		}
	}
	store(bigStr.Ref)
	e.emitLabel(notBigL)
	// A boxed host handle (emit_hostbox.go): Node's inspect form under
	// console.log, `[object Map]` as a string. Probed whether or not a host
	// box was made yet: one made later reaches here too, and the builtin
	// library's code cannot know (TDD-00238).
	{
		e.ensureHostBoxHooks()
		hostCell, isHost := e.emitHostProbe(payload)
		hostL, notHostL := e.freshLabel("dynstr.obj.host"), e.freshLabel("dynstr.obj.nothost")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isHost, hostL, notHostL))
		e.emitLabel(hostL)
		if inspect {
			store(e.emitHostInspectCall(hostCell, depth))
		} else {
			store(e.emitHostToStringTag(hostCell))
		}
		e.emitLabel(notHostL)
	}
	errObjPtr, errIsErr := e.emitBoxedErrorProbe(payload)
	errL := e.freshLabel("dynstr.obj.err")
	plainL := e.freshLabel("dynstr.obj.plain")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", errIsErr, errL, plainL))
	e.emitLabel(errL)
	if inspect {
		// util.inspect's form: the header with Node's `Ctor [Name]`, then
		// its own properties (dynjson.c inspect_error).
		e.ensureDynJSONC()
		e.declareFn("__kml_obj_inspect_at", "declare ptr @__kml_obj_inspect_at(ptr, i64)")
		es := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_obj_inspect_at(ptr %s, i64 %d)", es, errObjPtr, depth))
		store(es)
	} else {
		errStr, err := e.emitErrorToString(Value{Ref: errObjPtr, Ty: TypePtr})
		if err != nil {
			return Value{}, err
		}
		store(errStr.Ref)
	}
	e.emitLabel(plainL)
	// A boxed Symbol (hidden field-0 flag, ADR-01059) renders as
	// `Symbol(desc)` — console.log's and String(sym)'s form — instead of the
	// plain-object stand-in. (Implicit conversion in `+`/template literals
	// throws in JS; that stays the shared V1 leniency TDD-00044 documents.)
	symObjPtr, isSym := e.emitBoxedSymbolProbe(payload)
	symL := e.freshLabel("dynstr.obj.symbol")
	objL := e.freshLabel("dynstr.obj.object")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isSym, symL, objL))
	e.emitLabel(symL)
	symStr, err := e.emitSymbolToString(Value{Ref: symObjPtr, Ty: SymbolType()})
	if err != nil {
		return Value{}, err
	}
	store(symStr.Ref)
	e.emitLabel(objL)
	if inspect {
		// A static object renders through its layout row (TDD-00233).
		e.ensureDynJSONC()
		e.declareFn("__kml_obj_inspect_at", "declare ptr @__kml_obj_inspect_at(ptr, i64)")
		op := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", op, payload))
		os := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_obj_inspect_at(ptr %s, i64 %d)", os, op, depth))
		store(os)
	} else {
		// Object.prototype.toString: "[object Tag]" for a class with a
		// Symbol.toStringTag, else "[object Object]".
		e.ensureDynJSONC()
		e.declareFn("__kml_object_tostring", "declare ptr @__kml_object_tostring(i64)")
		os := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_object_tostring(i64 %s)", os, v.Ref))
		store(os)
	}
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
	tag, payload := e.emitUnboxTagPayload(v)

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

	matchL, nextL = e.emitTagCheck(tag, kmlTagDynFunc, "dyntypeof.dynfn")
	e.emitLabel(matchL)
	store("function")
	e.emitLabel(nextL)

	// A boxed Symbol is a kmlTagObject payload whose hidden field 0 carries
	// symbolTypeIDFlag (ADR-01059) — "symbol", not "object".
	matchL, nextL = e.emitTagCheck(tag, kmlTagObject, "dyntypeof.obj")
	e.emitLabel(matchL)
	_, isBig := e.emitBoxedBigIntProbe(payload)
	bigL := e.freshLabel("dyntypeof.bigint")
	notBigL := e.freshLabel("dyntypeof.notbig")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBig, bigL, notBigL))
	e.emitLabel(bigL)
	store("bigint")
	e.emitLabel(notBigL)
	_, isSym := e.emitBoxedSymbolProbe(payload)
	symL := e.freshLabel("dyntypeof.symbol")
	plainL := e.freshLabel("dyntypeof.plainobj")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isSym, symL, plainL))
	e.emitLabel(symL)
	store("symbol")
	e.emitLabel(plainL)
	store("object")
	e.emitLabel(nextL)

	// Remaining tags: object.
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
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_any_eq(i64 %s, i64 %s)", result, boxedA.Ref, boxedB.Ref))
	if negate {
		neg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", neg, result))
		return Value{Ref: neg, Ty: TypeBool}, nil
	}
	return Value{Ref: result, Ty: TypeBool}, nil
}

// emitArrayBufferIsView implements ArrayBuffer.isView(x): whether x is a
// typed array (a Buffer included) or a DataView — from its type, or for an
// `any`, from its box's array kind.
func (e *Emitter) emitArrayBufferIsView(arg ast.Expression) (Value, error) {
	v, err := e.emitExpr(arg)
	if err != nil {
		return Value{}, err
	}
	if isSelfDescribingBox(v.Ty) {
		plain := e.emitBoxIsTypedArray(v, anyArrayPlain)
		isArr := e.emitBoxIsArrayTag(v)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", r, plain))
		out := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", out, r, isArr))
		// A boxed DataView (the global module's class) is a view too.
		cls, ok := e.globalClass("DataView")
		if !ok {
			return Value{Ref: out, Ty: TypeBool}, nil
		}
		isDV, err := e.emitAnyInstanceOfClass(v, cls.ClassName)
		if err != nil {
			return Value{}, err
		}
		either := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", either, out, isDV.Ref))
		return Value{Ref: either, Ty: TypeBool}, nil
	}
	return Value{Ref: fmt.Sprint(v.Ty.IsTypedArray || e.isGlobalClassInstance(v.Ty, "DataView")), Ty: TypeBool}, nil
}

// emitBoxIsArrayTag is whether an `any` holds a static array's box.
func (e *Emitter) emitBoxIsArrayTag(v Value) string {
	tag, _ := e.emitUnboxTagPayload(v)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", r, tag, kmlTagArray))
	return r
}

// selfIdentifyingObjects reports object union members whose values identify
// their own type at run time, so narrowing needs no discriminant field: a
// class instance (its TagID header), a plain object (its layout id, read
// through the checked view), a host handle (its host box). The checker
// narrows (`instanceof`, `in`, a type guard); a member read before
// narrowing goes by name through the layout rows.
func selfIdentifyingObjects(members []Type) bool {
	for _, m := range members {
		switch {
		case m.IsDynamicObject || m.IsError:
			return false
		case m.IsClass, isHostHandle(m):
		case plainRecordType(m) && hasObjHeader(m):
		case m.IsObject && !hasObjHeader(m) && hostClassName(m) != "Object":
			// A headerless host object (URL): told apart by lacking a header;
			// headerlessHostMember allows one per union.
		default:
			return false
		}
	}
	return true
}

// boxTupleAsArray boxes a tuple as an any[] holding its boxed elements; an
// absent tuple (a null pointer) boxes as undefined.
func (e *Emitter) boxTupleAsArray(v Value) (Value, error) {
	n := len(v.Ty.Fields)
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
	nullL, boxL, doneL := e.freshLabel("boxtup.null"), e.freshLabel("boxtup.box"), e.freshLabel("boxtup.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nullL, boxL))
	e.emitLabel(nullL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(boxL)
	boxed, err := e.boxTupleElems(v, n)
	if err != nil {
		return Value{}, err
	}
	boxEnd := e.freshLabel("boxtup.boxend")
	e.emitTerminator(fmt.Sprintf("br label %%%s", boxEnd))
	e.emitLabel(boxEnd)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi i64 [ %d, %%%s ], [ %s, %%%s ]", r, nbUndefined, nullL, boxed.Ref, boxEnd))
	return Value{Ref: r, Ty: TypeAny}, nil
}

// boxTupleElems is boxTupleAsArray's present case.
func (e *Emitter) boxTupleElems(v Value, n int) (Value, error) {
	e.ensureMalloc()
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", data, max(n, 1)*8))
	structIR := v.Ty.StructIR()
	for i, f := range v.Ty.Fields {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, v.Ref, i))
		var fv Value
		if f.Ty.IsArray {
			fv = e.loadArrayFieldValue(gep, f.Ty)
		} else {
			fv = e.loadScalarOrNullableField(gep, f.Ty)
		}
		bv, err := e.emitBoxValue(fv)
		if err != nil {
			return Value{}, err
		}
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", slot, data, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", bv.Ref, slot))
	}
	r0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, data))
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %d, 1", r1, r0, n))
	return e.emitBoxValue(Value{Ref: r1, Ty: ArrayOf(TypeAny)})
}
