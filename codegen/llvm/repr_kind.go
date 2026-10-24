package llvm

import (
	"fmt"
	"os"
)

// repr_kind.go — the representation a value lowers to (TDD-00230 P3.4).
// One field, Type.Kind, names it; the IsX() predicates read it. A kind
// derived from another (a tuple or a class instance is an object; the
// promise resolver a closure) answers its base's predicate too. Scalars
// (numbers, booleans) and strings are kindNone, told apart by IR.

// reprKind is a value's representation kind.
type reprKind uint8

const (
	kindNone reprKind = iota
	// kindArray: an array, a pointer to its shared header; ElemType is the
	// element type.
	kindArray
	// kindFlatArray marks a `/** @value */` flat value-type array (TDD-00134
	// Stage 2): elements are laid out INLINE in one contiguous buffer
	// (stride = ElemType.StructSize()), not as pointers to separate heap
	// objects. Deliberately NOT IsArray, so every ptr-slot array operation
	// rejects it instead of corrupting the buffer; the supported surface
	// (index read/write, .length, for...of, .push) has explicit branches.
	// Storage matches a named array: a slot pointing at a {data,len} header.
	kindFlatArray
	// kindObject: a statically shaped object; Fields is its layout.
	kindObject
	// kindTuple marks a tuple type `[T0, T1, ...]` (TDD-00066). Storage-wise it
	// IS an IsObject struct with synthetic positional field names "0","1",...
	// (so StructIR/StructSize/FieldIndex/GEP, object-literal construction,
	// object param/return/field passing, spread, and structuredClone all apply
	// unchanged). kindTuple only additionally: maps a constant index `t[i]` to
	// field "i", lets an array-destructuring pattern bind positionally, and
	// renders/serializes as an array literal rather than an object.
	kindTuple
	// kindClass marks a user-defined `class` instance (TDD-00009 Stage 1):
	// storage-wise it's an ordinary IsObject heap struct (ClassType always
	// also sets IsObject, so every existing generic object mechanism —
	// StructIR, FieldIndex, emitMember, Object.* statics, JSON — applies with
	// no changes). kindClass/ClassName exist purely so method-call dispatch
	// (which needs a name to look up a method table) and, later,
	// `instanceof` (Stage 2) can recognize a class instance as distinct from
	// a plain structural object literal.
	kindClass
	// Function/closure type: all closures are passed as ptr.
	kindFunc
	// kindPromiseResolver marks the `resolve` closure `new Promise`'s executor
	// receives (emit_promise_new.go). It carries the closure's `{fnptr, env}`
	// shape like any callback, but a call site passing a Promise argument
	// (`resolve(anotherPromise)`) adopts that thenable — settling the target
	// when the argument settles — instead of coercing a promise to the value
	// type (TDD-00091). The target promise is the closure's env.
	kindPromiseResolver
	// kindDynamic marks any/unknown: a runtime-tagged { i8, i64 } box (tag +
	// payload) instead of one fixed concrete storage type. See emit_dynamic.go.
	kindDynamic
	// kindNever marks the bottom type — the value type of a `Promise.reject(...)`
	// (`Promise<never>`), which never actually produces a value (await re-throws).
	// It assigns to any target: coerce turns it into a zero of the target type,
	// keeping the IR well-typed while it stays dead (TDD-00087 follow-up).
	kindNever
	// kindCaught marks the catch-clause variable's type (TDD-00202): the unpacked
	// thrown-value record `{ i8 tag, i64 payload }` — TypeScript's `unknown`.
	// The tag is a logical kmlTag* value (kmlTagError=13 for a caught Error,
	// whose payload is the errorObjType pointer). Operations narrow it
	// (typeof/instanceof/===/member-after-narrow); coercing to `any` packs it to
	// a NaN-box (Error→object). Only produced by the catch binding.
	kindCaught
	// kindNull marks the null/undefined literal sentinel type (ptr null at IR
	// level); IsUndefined tells the undefined literal from null.
	kindNull
	// kindUndefined is the undefined literal's type.
	kindUndefined
	// kindStrLiteral marks a string-literal type (`"circle"`): string-shaped at
	// runtime (IR "ptr", isStringTy true), with the literal in LitValue. Used as
	// the discriminant of a discriminated union (TDD-00116); elsewhere it behaves
	// exactly as string.
	kindStrLiteral
)

// kindParent is the kind a derived kind also answers for.
var kindParent = map[reprKind]reprKind{
	kindUndefined:       kindNull,
	kindTuple:           kindObject,
	kindClass:           kindObject,
	kindPromiseResolver: kindFunc,
}

// isKind reports whether t's kind is k or derives from it.
func (t Type) isKind(k reprKind) bool {
	for c := t.Kind; c != kindNone; c = kindParent[c] {
		if c == k {
			return true
		}
	}
	return false
}

// kindCheck makes setKind panic on a write that replaces an unrelated kind
// (a type that would have carried two representation flags at once).
var kindCheck = os.Getenv("KML_KINDCHECK") != ""

// setKind marks (on) or unmarks (off) t as of kind k. Marking keeps a kind
// already derived from k; unmarking a derived kind leaves its base.
func (t *Type) setKind(k reprKind, on bool) {
	switch {
	case on && !t.isKind(k):
		if kindCheck && t.Kind != kindNone && kindParent[k] != t.Kind {
			panic(fmt.Sprintf("setKind: %d over unrelated %d", k, t.Kind))
		}
		t.Kind = k
	case !on && t.Kind == k:
		t.Kind = kindParent[k]
	case !on && t.isKind(k):
		t.Kind = kindNone
	}
}

func (t Type) IsArray() bool           { return t.isKind(kindArray) }
func (t Type) IsFlatArray() bool       { return t.isKind(kindFlatArray) }
func (t Type) IsObject() bool          { return t.isKind(kindObject) }
func (t Type) IsTuple() bool           { return t.isKind(kindTuple) }
func (t Type) IsClass() bool           { return t.isKind(kindClass) }
func (t Type) IsFunc() bool            { return t.isKind(kindFunc) }
func (t Type) IsPromiseResolver() bool { return t.isKind(kindPromiseResolver) }
func (t Type) IsDynamic() bool         { return t.isKind(kindDynamic) }
func (t Type) IsNever() bool           { return t.isKind(kindNever) }
func (t Type) IsCaught() bool          { return t.isKind(kindCaught) }
func (t Type) IsNull() bool            { return t.isKind(kindNull) }
func (t Type) IsStrLiteral() bool      { return t.isKind(kindStrLiteral) }

// kindFlagOf names each kind by its predicate.
var kindFlagOf = map[string]reprKind{
	"IsArray":           kindArray,
	"IsFlatArray":       kindFlatArray,
	"IsObject":          kindObject,
	"IsTuple":           kindTuple,
	"IsClass":           kindClass,
	"IsFunc":            kindFunc,
	"IsPromiseResolver": kindPromiseResolver,
	"IsDynamic":         kindDynamic,
	"IsNever":           kindNever,
	"IsCaught":          kindCaught,
	"IsNull":            kindNull,
	"IsStrLiteral":      kindStrLiteral,
}
