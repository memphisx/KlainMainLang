package llvm

import (
	"fmt"
	"strings"
)

// constPtrRef reports a pointer operand that is a constant (a string
// literal's global, null), which is never absent at run time.
func constPtrRef(ref string) bool {
	return ref == "null" || strings.HasPrefix(ref, "@") || strings.HasPrefix(ref, "getelementptr")
}

// absence.go — which absent values a type's representation holds
// (TDD-00230 P3.4): none, null, undefined (the nullable wrapper's two
// flavours: `T | null`, `T | undefined`), or both (the three-state
// wrapper, `T | null | undefined`). The null literal's own type is a kind,
// kindNull, with kindUndefined for `undefined`.

// absence is a type's absent states.
type absence uint8

const (
	absNone  absence = iota
	absNull          // T | null: one absent state, null
	absUndef         // T | undefined: one absent state, undefined
	// absBoth: T | null | undefined, a string or object pointer that may
	// hold both (`x?: T | null`): the null pointer is undefined (a missing
	// field reads it), and nullRef, a zeroed sentinel cell, is null
	// (TDD-00230 P3.3's three states).
	absBoth
)

// Nullable reports a type holding an absent value: T | null, T | undefined
// or both.
func (t Type) Nullable() bool { return t.Absent != absNone }

// IsUndefined reports a type whose absent value is (or includes)
// undefined, or the undefined literal's type.
func (t Type) IsUndefined() bool {
	return t.Absent == absUndef || t.Absent == absBoth || t.Kind == kindUndefined
}

// NullAndUndef reports the three-state representation.
func (t Type) NullAndUndef() bool { return t.Absent == absBoth }

// asTriStr retypes a plain string as a three-state pointer, for the readers
// that render one (renderThreeState); ok is false for anything else,
// including fromThreeState's own result, which is never nullRef.
func (e *Emitter) asTriStr(v Value) (Value, bool) {
	if !triStr(v.Ty) || constPtrRef(v.Ref) || e.notNullRef[v.Ref] {
		return v, false
	}
	v.Ty.setNullAndUndef(true)
	return v, true
}

// triStr reports a `string`, `string | undefined` or `string | null`
// pointer (TDD-00257): its null pointer is undefined and nullRef is null,
// as a three-state pointer's are, whichever of them its type admits.
func triStr(t Type) bool {
	return t.IR == "ptr" && t.Absent != absBoth && isStringTy(t) && !hasHandleFlag(t) && !t.IsDynamic() && !t.IsNull() && t.Kind != kindUndefined
}

// absenceCheck reports a state the old three flags allowed and absence
// cannot hold (with KML_KINDCHECK).
func absenceCheck(what string) {
	if kindCheck {
		panic("absence: " + what)
	}
}

// setNullable makes t nullable, keeping its flavour, or not nullable.
func (t *Type) setNullable(on bool) {
	switch {
	case on && t.Absent == absNone:
		t.Absent = absNull
	case !on:
		t.Absent = absNone
	}
}

// setUndefined makes t's absent value undefined (on) or null (off). On the
// null literal's type it picks between the null and undefined literals.
func (t *Type) setUndefined(on bool) {
	if t.isKind(kindNull) {
		if on {
			t.Kind = kindUndefined
		} else {
			t.Kind = kindNull
		}
		return
	}
	switch {
	case on && t.Absent == absNull:
		t.Absent = absUndef
	case on && t.Absent == absNone:
		absenceCheck(fmt.Sprintf("undefined on a non-nullable %s", t.IR))
	case !on && t.Absent == absUndef:
		t.Absent = absNull
	case !on && t.Absent == absBoth:
		absenceCheck("null-only three-state")
	}
}

// setNullAndUndef makes t three-state (on), or leaves the undefined flavour
// (off).
func (t *Type) setNullAndUndef(on bool) {
	switch {
	case on && t.Absent == absNone:
		absenceCheck(fmt.Sprintf("three-state on a non-nullable %s", t.IR))
		t.Absent = absBoth
	case on:
		t.Absent = absBoth
	case !on && t.Absent == absBoth:
		t.Absent = absUndef
	}
}

// absenceOf is the absence a literal's nullable, undefined and three-state
// settings make, applied in that order.
func absenceOf(nullable, undef, both bool) absence {
	var t Type
	t.setNullable(nullable)
	t.setUndefined(undef)
	t.setNullAndUndef(both)
	return t.Absent
}
