package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// Field is one field in an object type.
type Field struct {
	Name string
	Ty   Type
	// Optional marks a `name?: T` member: its key may be missing from an
	// object, so an undefined value there means no property. Any other
	// field always has its key, an undefined value included (a class
	// field, an object literal's property, `name: T | undefined`).
	Optional bool
}

// Type represents an LLVM IR type.
type Type struct {
	// Host is the builtin class the value is an instance of, hostNone for
	// any other value (host_kind.go).
	Host hostKind
	// Kind is the representation the value lowers to (repr_kind.go).
	Kind reprKind
	// Absent is which absent values the representation holds (absence.go).
	Absent      absence
	IR          string // e.g. "i64", "i32", "float", "double", "i1", "ptr"
	Signed      bool
	Float       bool
	ElemType    *Type   // non-nil when IsArray
	Fields      []Field // non-nil when IsObject
	FuncParams  []Type
	FuncRetType *Type // nil means void
	// FuncHasRest marks the last entry of FuncParams as a rest slot (its
	// own declared type is the collected array type, e.g. number[] for
	// `(...args: number[])`) — added alongside ADR-00151/TDD-00059's
	// array-typed-closure-parameter fix so a closure *value*'s call sites
	// (emitClosureCallByPtr, emitCBCall) can tell "one array-typed
	// parameter" apart from "a rest parameter collecting N individual
	// trailing call arguments," the same distinction FuncSig.HasRest
	// already lets a named function's call sites make.
	FuncHasRest bool
	// FuncCountsArgs: the function reads `arguments`, so a call stores how
	// many declared parameters it passes (noteArgc).
	FuncCountsArgs bool
	// FuncThis marks a function taking its `this` as FuncParams[0] (a
	// `this: T` parameter): a call passes the receiver there.
	FuncThis bool
	// FuncParamNames and FuncParamDefaults let a first-class function value's
	// call site (emitClosureCallByPtr) fill an omitted trailing parameter with
	// its default expression, the same way a named/class/IIFE call site already
	// does from FuncSig.Defaults (TDD-00206 Stage 1). Populated when a closure
	// *value* is built (arrow / function-expression emission); ride the same
	// binding-preserved path FuncHasRest does. FuncParamNames keys the
	// paramDefaultScratch scope so a default can reference an earlier parameter
	// (`b = a`); a nil FuncParamDefaults[i] means parameter i has no default.
	FuncParamNames []string
	// FuncParamOptional[i] marks parameter i as omittable at the call site — a
	// `?`-declared parameter, a defaulted parameter, or a rest slot. A first-class
	// call site (emitClosureCallByPtr/emitCBCall) rejects a *missing* argument for
	// a required (non-optional, non-defaulted) parameter instead of zero-filling
	// it, matching a named function's arity check (ADR-00963). Parallel to
	// FuncParams; nil/short means "not optional".
	FuncParamOptional []bool
	FuncParamDefaults []ast.Expression
	// FuncBodyDefaults holds the defaults that must be evaluated in the closure
	// *body* prologue rather than at the call site — those referencing a variable
	// captured from the defining scope or a module global (TDD-00206 Stage 2). A
	// closure with any such default carries a hidden trailing `i32` argument-
	// presence mask (FuncHasDefaultMask): the body fills param i from
	// FuncBodyDefaults[i] when the mask's bit i is clear. A given param has at most
	// one of FuncParamDefaults[i] / FuncBodyDefaults[i] set.
	FuncBodyDefaults   []ast.Expression
	FuncHasDefaultMask bool
	// MapKey holds a Map's key type (a Set's element type); MapVal holds a
	// Map's value type (nil for a Set).
	MapKey *Type
	MapVal *Type
	// Weak marks a WeakMap<K,V>/WeakSet<T> (IsMap()/IsSet() true): keyed on
	// object-pointer identity, not iterable, mode-dependent backing (TDD-00112).
	Weak bool
	// UncheckedIndex marks the `T | undefined` an array element read yields: an
	// out-of-range `a[i]` reads `undefined` at run time, but tsc types the
	// expression as plain `T` (no noUncheckedIndexedAccess), so the strict
	// `possibly undefined` gates (TDD-00187) let it through unnarrowed.
	UncheckedIndex bool
	PromiseType    *Type
	// PromiseResolved marks a Promise<Response> whose slot already holds a
	// fully-built Response object (emit_promise.go's wrapResolvedPromise,
	// e.g. Promise.race's Response branch), as opposed to every other
	// Promise<Response> — a plain fetch() call — whose slot holds a still-
	// pending fetch handle to be waited on. Both share the exact same static
	// type (IsResponse=true) and IR ("ptr"), so emitAwait's IsResponse
	// branch, which always expects the "raw fetch handle" shape, needs this
	// flag to know when it must NOT re-run __kml_await_fetch on an
	// already-resolved Response — a real bug found and fixed while adding
	// Promise.all/.race/.allSettled (ADR-00073): without it, `await
	// Promise.race(responses)` reinterpreted the finished Response struct's
	// own bytes as a { ptr, ptr, i64, i64, i64 } pending-fetch struct,
	// corrupting memory (segfaults / spurious "Unknown error" throws whose
	// symptom varied run to run, per garbage read from whatever bytes landed
	// where the pending struct's `result` field would be).
	PromiseResolved bool

	// PromiseTask marks a Promise<T> returned by a may-suspend async function
	// (TDD-00083 Stage 2): its slot is a { i64 resolved, ptr waiter, i64 v0, i64
	// v1 } pending-promise object driven by the task scheduler (runtime_task.go),
	// not a bare value slot. emitAwait uses this to wait via
	// @__kml_task_await_ready and then read v0/v1, rather than the plain
	// load-and-free path.
	PromiseTask bool
	// UnionMembers marks a general union type beyond T | null (TDD-00043):
	// non-nil means this IsDynamic type is a *constrained* union — the exact
	// same runtime { i8, i64 } box any/unknown use, but restricted to this
	// set of concrete member types at every assignment/call/return boundary
	// (see unionAllowsAssignmentFrom, emit_dynamic.go). IsDynamic with a nil
	// UnionMembers is bare any/unknown: fully unconstrained, and still
	// rejected as a function param/return/array-element/object-field type
	// (ADR-00008); IsDynamic with a non-nil UnionMembers is allowed in those
	// positions instead, since the member set makes it fully checkable there.
	UnionMembers []Type
	LitValue     string
	// IntersectionMembers carries the resolved members of an A & B & ...
	// intersection (TDD-00078). Unlike UnionMembers, this is NOT a runtime
	// shape: an object-type intersection is already collapsed into this Type's
	// own IsObject/Fields (the merged struct), so downstream object machinery
	// uses it as an ordinary object type and ignores this field. It is retained
	// only so validateIntersectionMembers can re-check the members (all-object,
	// no field conflicts) at each use site, mirroring how UnionMembers is
	// validated — resolveType itself has no error return.
	IntersectionMembers []Type
	ClassName           string
	// RefName is a nominal re-resolution handle for a *named structural* type
	// (a top-level `interface`, or a `type` alias of an object shape) — set on
	// the ObjectType registered in e.interfaces, NOT implying IsClass. It exists
	// solely so canonicalizeClassTy can swap a stale by-value field snapshot for
	// the live registry entry, exactly as ClassName does for a class: a
	// self/mutually-referential interface field (`interface N { next: N | null }`,
	// `children: N[]`) captures the empty placeholder seeded before N's own
	// fields exist, so drilling `n.next.val` / `r.children[0].val` needs the
	// live N re-resolved on demand. Unlike ClassName it drives no method
	// dispatch, instanceof, decorator-metadata, or monomorphization keying —
	// those all gate on IsClass or read ClassName directly, which RefName never
	// sets.
	RefName string
	// HasVTable marks a class whose instances carry a hidden vtable-pointer
	// field at index 1 (right after the tag field), for TDD-00009 Stage 3
	// dynamic dispatch. Only set for a class belonging to an inheritance
	// tree where at least one method is overridden somewhere in that tree —
	// see registerClasses' override analysis. A class with no inheritance
	// relationship at all, or one whose whole tree has zero overrides,
	// leaves this false and keeps the exact Stage 1/2 single-tag layout.
	HasVTable bool

	// DynPropTy, on a dynamic (`any`) object, is the declared type of every
	// property it holds — an index-signature view (`{ [k: string]: bigint }`,
	// node:ffi's `lib.symbols`, TDD-00229): a member/bracket read unboxes to
	// it instead of staying `any`.
	DynPropTy *Type
	// DictFields are an index-signature dictionary's named properties
	// (`interface H { [k: string]: string | string[]; host?: string }`): a
	// read of one has its declared type.
	DictFields        []Field
	GeneratorIsAsync  bool // an `async function*` — .next() returns Promise<{value,done}> (TDD-00085)
	GeneratorElemType *Type

	// ExecArray marks a RegExp exec() result: an array whose header carries
	// index, input and groups after its {data, len} (40 bytes: data, len,
	// index i64, input ptr, groups ptr).
	ExecArray bool
	// ExecMaybePlain marks a str.match() result whose RegExp may be global:
	// a global match's header stores index -1 and a null input, read as
	// undefined (and not listed by console.log), as Node's plain array.
	ExecMaybePlain bool

	// A collection iterator's (hostCollIter) IterSrc is the iterated
	// collection's type and IterKind what each step yields
	// (mapIterKeys/Values/Entries).
	IterKind mapIterKind
	IterSrc  *Type
	// IterRegExp marks matchAll's RegExp String Iterator: an Array
	// Iterator over its exec steps in every other respect.
	IterRegExp bool

	// BufferGrowable marks a buffer constructed with `{maxByteLength}`
	// (ADR-00494): its header is the 24-byte {len, data, max} shape, so
	// `.growable`/`.maxByteLength`/`.grow()` may read word 2. Buffers from
	// every other producer keep the 16-byte header and must never read it.
	BufferGrowable bool
	// BigIntElem marks a BigInt64Array/BigUint64Array (TDD-00101): storage
	// is a raw i64/u64 (so all pointer/stride machinery is unchanged), but
	// every element crossing the language boundary is a `bigint` handle —
	// loads wrap via __kml_bigint_from_i64/from_u64, stores require an
	// IsBigInt value and unwrap via to_i64/to_u64. Array methods that would
	// leak the raw scalar (HOFs/search/sort/join/iterator objects) are a
	// compile-time rejection; see emit_arraybuffer.go's conversion helpers.
	BigIntElem bool
	// Clamped marks a Uint8ClampedArray (TDD-00101): storage is a plain u8
	// (reads and all read-side methods are unchanged), but stores go through
	// the spec's ToUint8Clamp — clamp to [0,255], floats round-half-to-even,
	// NaN → 0 — instead of e.convert's mod-2^width trunc.
	Clamped bool
	// Inline marks an object element VALUE that lives inline inside its
	// container's buffer (a flat-array element): its Ref is an interior
	// pointer, loads return that pointer directly, and stores memcpy the
	// struct bytes instead of storing a pointer word.
	Inline bool
	// TupleByVal marks a small tuple RETURN type compiled by value (TDD-00134
	// Stage 3, -optimize-memory): the function's IR signature returns the
	// tuple's struct aggregate (register-passable, ≤2 scalar fields) instead
	// of a pointer to a heap struct. Set only on a FuncSig.RetType copy by
	// planTupleByValReturns — never on a variable's or field's type — and
	// cleared on the Value a call site hands downstream (the aggregate is
	// spilled back to a pointer there unless destructured directly).
	TupleByVal bool
	// Inferred marks a parameter type that defaulted to TypeI64 because no
	// explicit annotation was given, as opposed to a real `number`/`int32`/
	// etc. annotation that happens to also resolve to i64. Call sites use
	// this to reject a non-numeric argument against an unannotated
	// parameter at compile time, instead of silently bit-reinterpreting it
	// as an i64 (see docs/adr/00/ADR-00042.md).
	Inferred    bool
	StreamChunk *Type
	StreamOut   *Type
	// HostTag names a host value's JS class where its flags alone do not:
	// a CompressionStream is a TransformStream by layout.
	HostTag string
}

// ArrayOf returns an array type whose elements are of the given type.
func ArrayOf(elem Type) Type {
	elem = arrayElemType(elem)
	return Type{IR: "ptr", Kind: kindArray, ElemType: &elem}
}

// arrayElemType is the type an array stores for a declared/produced element
// type. A `(number | undefined)[]` / `(boolean | null)[]` element: the { i1, T }
// optional is a local-slot shape with no array-element storage, so the element
// rides the boxed-element array instead — one self-describing NaN box per slot,
// the union `T | undefined` (TDD-00200's `any[]` storage). Every other element
// type is stored as itself. A producer that fills a buffer by hand (a HOF's
// result array) must size, coerce and store against this type, not the raw one.
func arrayElemType(elem Type) Type {
	elem = elem.staticIndexType()
	if !isNullableScalar(elem) {
		return elem
	}
	return Type{IR: TypeAny.IR, Kind: kindDynamic, UnionMembers: []Type{elem.withoutNullable()},
		Absent: absenceOf(true, elem.IsUndefined(), false)}
}

// staticIndexType is the type tsc gives an element read: the plain `T` behind
// an UncheckedIndex `T | undefined`. Inferred *declarations* — an array's
// element type, a function's return type, an object literal's field — take it,
// so `idx.map(i => vals[i])` stays a `number[]`; the run-time absence survives
// only where the read is consumed directly. Any other type is returned as is.
func (t Type) staticIndexType() Type {
	if !t.UncheckedIndex {
		return t
	}
	t.UncheckedIndex = false
	t.setNullable(false)
	t.setUndefined(false)
	return t
}

// ObjectType returns an object type with the given fields, behind the header
// word every object layout starts with (TDD-00230 phase 5): field 0, named
// ClassTagField, holds the layout's type id (see objHeaderWord). Fields that
// already start with the header (a class, or a type rebuilt from another
// type's Fields) are not given a second one.
func ObjectType(fields []Field) Type {
	if len(fields) == 0 || fields[0].Name != ClassTagField {
		withHdr := make([]Field, 0, len(fields)+1)
		withHdr = append(withHdr, Field{Name: ClassTagField, Ty: TypeI64})
		fields = append(withHdr, fields...)
	}
	// A void member (`{ x = f() }` of a function returning nothing) holds
	// undefined, as JavaScript stores it.
	copied := false
	for i := range fields {
		if fields[i].Ty.IR == "void" {
			if !copied {
				fields, copied = append([]Field(nil), fields...), true
			}
			fields[i].Ty = TypeUndefined
		}
	}
	return Type{IR: "ptr", Kind: kindObject, Fields: withPresence(fields)}
}

// PresenceField is the hidden last field of a layout with optional members
// (TDD-00256): bit k is set when the k-th optional field has its key, so
// one holding undefined is still an own property. A key is present when its
// bit is set or its value is not undefined. Reserved like ClassTagField.
const PresenceField = "__kml_present"

// OrderField follows the presence word: the object's own-key order when it
// is not the layout's (keyordersrc/keyorder.c), else null. Reserved too.
const OrderField = "__kml_order"

// isHiddenField reports a layout's reserved bookkeeping fields, which are
// not keys.
func isHiddenField(name string) bool {
	return name == PresenceField || name == OrderField || name == ClassTagField
}

// withPresence gives fields exactly one presence word (and the order slot
// after it) when they have an optional member, and none otherwise: a
// builtin layout, whose readers and allocators in C know its fields. A
// single word already in the list stays where it is: a subclass's layout
// extends its base's, whose code finds the word at its own index (the
// subclass's members take the following bits). Otherwise any carried word
// is dropped and one is appended.
func withPresence(fields []Field) []Field { return presenceLayout(fields, false, true) }

// withKeyPresence is withPresence for a layout the program declares (an
// interface, a type literal, a class, a literal's inferred type): any of its
// members can lack its key — an optional one, or a required one a JSON
// input or a `declare` left out — and its keys can list in another order
// than its fields, so it has the word whenever it has a member.
func withKeyPresence(fields []Field) []Field { return presenceLayout(fields, true, false) }

// withClassKeyPresence is withKeyPresence for a class: a subclass keeps its
// base's word where the base's code finds it.
func withClassKeyPresence(fields []Field) []Field { return presenceLayout(fields, true, true) }

// presenceLayout: keep says a word already in fields stays where it is;
// otherwise (an intersection or an extending interface merging its
// members' fields) any carried word is dropped and one appended.
func presenceLayout(fields []Field, required, keep bool) []Field {
	n := len(fields)
	carried, optional := 0, false
	for _, f := range fields {
		switch {
		case f.Name == PresenceField:
			carried++
		case f.Name == ClassTagField || f.Name == OrderField:
		case f.Optional || required:
			optional = true
		}
	}
	// A word already placed (a class's, kept by ClassType; a subclass's
	// base's) stays where it is.
	if carried == 1 && keep {
		return fields
	}
	if carried == 0 && !optional {
		return fields
	}
	out := make([]Field, 0, n+2)
	for _, f := range fields {
		if f.Name != PresenceField && f.Name != OrderField {
			out = append(out, f)
		}
	}
	if optional {
		out = append(out, Field{Name: PresenceField, Ty: TypeI64})
		// Only a declared layout lists its keys in its own order: a
		// builtin one's allocators (some in C, some malloc) know no slot.
		if required {
			out = append(out, Field{Name: OrderField, Ty: TypePtr})
		}
	}
	return out
}

// UserObjectType is ObjectType for a layout the program declares
// (withKeyPresence).
func UserObjectType(fields []Field) Type {
	t := ObjectType(fields)
	t.Fields = withKeyPresence(t.Fields)
	return t
}

func classObjectType(fields []Field) Type {
	t := ObjectType(fields)
	t.Fields = withClassKeyPresence(t.Fields)
	return t
}

// presenceBit is the bit of field name in t's presence word, or -1 when t
// has no word or name is not one of its first 64 fields.
func (t Type) presenceBit(name string) int {
	if t.presenceIndex() < 0 {
		return -1
	}
	k := 0
	for _, f := range t.Fields {
		if isHiddenField(f.Name) {
			continue
		}
		if f.Name == name {
			if k >= 64 {
				return -1
			}
			return k
		}
		k++
	}
	return -1
}

// hostObjectType is ObjectType for a host class's layout (an Is*-flagged
// runtime object whose fields the hand-written runtime reads at fixed
// offsets): headerless until the host classes move to declared layouts
// (TDD-00230 P3.4), when their header is added with the runtime's readers.
// Boxed into `any`, one reads as "no shape".
func hostObjectType(fields []Field) Type {
	return Type{IR: "ptr", Kind: kindObject, Fields: fields}
}

// UserFields returns t's fields after the header word: the layout's own
// fields, in declaration order (a tuple's elements are UserFields()[i]).
// Struct indices of these fields are one more than their position here.
func (t Type) UserFields() []Field {
	fs := t.Fields
	if i := t.presenceIndex(); i >= 0 {
		// The presence word and the order slot after it are not fields of
		// the object's own (they sit last, or mid-list in a subclass after
		// its base's fields).
		j := i + 1
		if j < len(fs) && fs[j].Name == OrderField {
			j++
		}
		out := make([]Field, 0, len(fs)-(j-i))
		out = append(out, fs[:i]...)
		fs = append(out, fs[j:]...)
	}
	if len(fs) > 0 && fs[0].Name == ClassTagField {
		return fs[1:]
	}
	return fs
}

// orderIndex is the struct index of t's order slot, or -1.
func (t Type) orderIndex() int {
	if i := t.presenceIndex(); i >= 0 && i+1 < len(t.Fields) && t.Fields[i+1].Name == OrderField {
		return i + 1
	}
	return -1
}

// presenceIndex is the struct index of t's presence word, or -1.
func (t Type) presenceIndex() int {
	for i := len(t.Fields) - 1; i >= 0; i-- {
		if t.Fields[i].Name == PresenceField {
			return i
		}
	}
	return -1
}

// TupleType returns a tuple type `[T0, T1, ...]` (TDD-00066): an object struct
// with synthetic positional field names "0","1",..., flagged IsTuple.
func TupleType(elems []Type) Type {
	fields := make([]Field, len(elems))
	for i, ety := range elems {
		fields[i] = Field{Name: fmt.Sprintf("%d", i), Ty: ety}
	}
	// Headerless: a tuple is value-like data (TDD-00230 phase 5); boxed into
	// `any` it becomes an array box, not an object with a shape.
	return Type{IR: "ptr", Kind: kindTuple, Fields: fields}
}

// MapType returns a Map<key,val> type.
func MapType(key, val Type) Type {
	return Type{IR: "ptr", Host: hostMap, MapKey: &key, MapVal: &val}
}

// SetType returns a Set<elem> type.
func SetType(elem Type) Type {
	return Type{IR: "ptr", Host: hostSet, MapKey: &elem}
}

// WeakMapType returns a WeakMap<key,val> type (TDD-00112) — an object-identity-
// keyed, non-iterable map with mode-dependent backing.
func WeakMapType(key, val Type) Type {
	return Type{IR: "ptr", Host: hostMap, Weak: true, MapKey: &key, MapVal: &val}
}

// WeakSetType returns a WeakSet<elem> type (TDD-00112).
func WeakSetType(elem Type) Type {
	return Type{IR: "ptr", Host: hostSet, Weak: true, MapKey: &elem}
}

// PromiseOf returns a Promise<T> type (the coroutine handle ptr).
// Pass TypeVoid for Promise<void>.
func PromiseOf(inner Type) Type {
	if inner.IR == "void" {
		return Type{IR: "ptr", Host: hostPromise}
	}
	innerCopy := inner
	return Type{IR: "ptr", Host: hostPromise, PromiseType: &innerCopy}
}

// ReadableStreamType returns a ReadableStream<chunk> type (TDD-00097).
func ReadableStreamType(chunk Type) Type {
	c := chunk
	return Type{IR: "ptr", Host: hostReadableStream, StreamChunk: &c}
}

// StreamReaderType returns the reader type getReader() yields.
func StreamReaderType(chunk Type) Type {
	c := chunk
	return Type{IR: "ptr", Host: hostStreamReader, StreamChunk: &c}
}

// RSControllerType returns the controller type start/pull callbacks receive.
func RSControllerType(chunk Type) Type {
	c := chunk
	return Type{IR: "ptr", Host: hostRSController, StreamChunk: &c}
}

// WritableStreamType / WSWriterType / WSControllerType are the writable-side
// mirrors (TDD-00097 Stage 2).
func WritableStreamType(chunk Type) Type {
	c := chunk
	return Type{IR: "ptr", Host: hostWritableStream, StreamChunk: &c}
}

func WSWriterType(chunk Type) Type {
	c := chunk
	return Type{IR: "ptr", Host: hostStreamWriter, StreamChunk: &c}
}

func WSControllerType(chunk Type) Type {
	c := chunk
	return Type{IR: "ptr", Host: hostWSController, StreamChunk: &c}
}

// ResponseType returns fetch()'s Response object type: a plain heap object
// with status/ok/body fields (readable via the ordinary object field-access
// path — no special dispatch needed for those three), plus IsResponse set so
// emit_fetch.go's text()/json()/arrayBuffer() method dispatch can recognize
// it. bodyLength is the real byte count of body (as opposed to body's own
// strlen, which undercounts if the response contained an embedded null byte)
// — an implementation-only field feeding .arrayBuffer(), not part of the
// documented public surface (real Fetch has no such field either).
func ResponseType() Type {
	ty := hostObjectType([]Field{
		{Name: "status", Ty: TypeF64}, // a `number` (TDD-00123)
		{Name: "ok", Ty: TypeBool},
		{Name: "body", Ty: TypePtr},
		{Name: "bodyLength", Ty: TypeI64},
		// The pending-fetch handle backing lazy body access (TDD-00097
		// Stage 4): non-null on a headers-resolved Response, so
		// .text()/.json()/.arrayBuffer() drive the rest of the transfer and
		// .body can stream it. Null on combinator-built Responses.
		{Name: "__kml_pending", Ty: TypePtr},
		// A constructed Response's headers (a Headers) and statusText;
		// null on a fetched one, whose are read from its captured header
		// text.
		{Name: "__kml_headers", Ty: TypePtr},
		{Name: "__kml_status_text", Ty: TypePtr},
		// A constructed Response's type ("default", "error"); null on a
		// fetched one ("basic").
		{Name: "__kml_type", Ty: TypePtr},
		// The ReadableStream `.body` returns, once built (a constructed
		// Response's stream body from the start), so every read is the same
		// stream.
		{Name: "__kml_stream", Ty: TypePtr},
		// responseNullBody: `.body` is null (a constructed Response without
		// a body); responseStreamBody: the body is __kml_stream's bytes,
		// not yet read into body/bodyLength.
		{Name: "__kml_bodyflags", Ty: TypeI64},
	})
	ty.setHost(hostResponse, true)
	return ty
}

// SymbolType returns Symbol(...)'s result type (TDD-00044 V1) — a plain
// 1-field heap object, same "flag a generic ObjectType" shape URLType uses
// above. Uniqueness and === come from the struct's own pointer identity, not
// from anything stored in the struct — see IsSymbol's doc comment.
func SymbolType() Type {
	// Field 0 is a hidden type-id word carrying symbolTypeIDFlag (ADR-01059):
	// once a Symbol is boxed into `any` it is a kmlTagObject payload like any
	// other struct, and the flag is what lets typeof / ToNumber / ToString /
	// `=== "symbol"` narrowing tell it apart from a plain object at runtime —
	// the same field-0 probe Errors use, on a different bit. Every allocation
	// site (Symbol(), Symbol.for) stores it.
	// description is undefined for `Symbol()` (a null pointer).
	desc := TypePtr
	desc.setNullable(true)
	desc.setUndefined(true)
	ty := ObjectType([]Field{{Name: "description", Ty: desc}})
	ty.setHost(hostSymbol, true)
	return ty
}

// BigIntType returns a `bigint` value's type (TDD-00074): an opaque `ptr` handle
// to a backend-owned arbitrary-precision integer. Unlike SymbolType this is NOT
// an IsObject struct — nothing reads fields off it; every operation goes through
// the __kml_bigint_* ABI in emit_bigint.go.
func BigIntType() Type {
	return Type{IR: "ptr", Host: hostBigInt}
}

// FetchRequestType returns `new Request(...)`'s result type (TDD-00040): a
// plain heap object like Response/URL — url/method/headers/body are all
// plain field reads via the existing object machinery, no dispatched
// methods of its own. See IsFetchRequest's doc comment for the naming
// choice (avoiding the pre-existing, unrelated IsRequest/RequestType) and
// emit_fetch_request.go.
func FetchRequestType() Type {
	ty := hostObjectType([]Field{
		{Name: "url", Ty: TypePtr},
		{Name: "method", Ty: TypePtr},
		{Name: "__kml_headers", Ty: TypePtr}, // its Headers
		// The body's bytes (null without a body) and the Body members'
		// state, as Response has them (emitResponseCall and
		// emitResponseBodyStream serve both).
		{Name: "body", Ty: TypePtr},
		{Name: "bodyLength", Ty: TypeI64},
		{Name: "__kml_pending", Ty: TypePtr}, // always null
		{Name: "__kml_stream", Ty: TypePtr},
		{Name: "__kml_bodyflags", Ty: TypeI64},
	})
	ty.setHost(hostFetchRequest, true)
	return ty
}

// XHRMethodField/XHRURLField/XHRHeadersField are the names of the hidden
// fields every `new XMLHttpRequest()` instance carries at indices 0-2 —
// written by open()/setRequestHeader(), read by send() — never exposed via
// VisibleFields()/Object.keys/JSON, the same convention
// RegexHandleField uses.
const (
	XHRMethodField  = "__kml_xhr_method"
	XHRURLField     = "__kml_xhr_url"
	XHRHeadersField = "__kml_xhr_headers"
	// XHRRespHeadersField holds the parsed response-header map after send()
	// (ADR-00490) — null until DONE; getResponseHeader()/
	// getAllResponseHeaders() read it.
	XHRRespHeadersField = "__kml_xhr_resp_headers"
)

// XMLHttpRequestType returns `new XMLHttpRequest()`'s result type
// (TDD-00040). readyState/status/responseText/response are plain visible
// fields (readyState: 0 UNSENT, 1 OPENED, 4 DONE — this implementation
// skips the real spec's 2 HEADERS_RECEIVED/3 LOADING, which have no
// meaning for a send() that runs to completion before returning).
// onreadystatechange/onload/onerror are zero-argument callback fields (see
// IsXHR's doc comment for why) — assigning to any of them is a plain
// FuncType field assignment needing no dedicated codegen.
func XMLHttpRequestType() Type {
	cb := FuncType(nil, TypeVoid)
	ty := hostObjectType([]Field{
		{Name: XHRMethodField, Ty: TypePtr},
		{Name: XHRURLField, Ty: TypePtr},
		{Name: XHRHeadersField, Ty: TypePtr},
		{Name: XHRRespHeadersField, Ty: TypePtr},
		{Name: "readyState", Ty: TypeI64},
		{Name: "status", Ty: TypeI64},
		{Name: "responseText", Ty: TypePtr},
		{Name: "response", Ty: TypePtr},
		{Name: "onreadystatechange", Ty: cb},
		{Name: "onload", Ty: cb},
		{Name: "onerror", Ty: cb},
	})
	ty.setHost(hostXHR, true)
	return ty
}

// Generator instance struct field names (TDD-00061/ADR-00172) — a fixed
// prologue shared by every generator, followed by one field per the
// generator function's own declared parameter (named "__param0",
// "__param1", ... — same synthetic-name convention a destructured function
// parameter's own pattern fields already use, see ast.Param's doc comment).
const (
	GeneratorProtoField      = "__proto"      // the prototype the instance inherits (a boxed object; 0 = the root, genproto.go) — first, at one offset in every layout
	GeneratorCtxField        = "__ctx"        // this generator's own ucontext_t (ptr, malloc'd ucontextLayout()-sized)
	GeneratorStackField      = "__stack"      // this generator's own fiber stack (ptr, malloc'd fiberStackBytes)
	GeneratorCallerCtxField  = "__callerCtx"  // where to swapcontext back to on yield/return — reset before every .next() call
	GeneratorStartedField    = "__started"    // false until the first .next() call
	GeneratorDoneField       = "__done"       // true once the body has returned or fallen off the end
	GeneratorYieldedField    = "__yielded"    // what the most recent yield/return produced
	GeneratorSentField       = "__sent"       // what the current .next(value)/.return(value) call is passing in, boxed (TNext is its own type, `any` by default)
	GeneratorResumeModeField = "__resumeMode" // how the current resume behaves: 0 next (return __sent), 1 throw __thrown, 2 return __sent (TDD-00086)
	GeneratorThrownField     = "__thrown"     // the error object a .throw(e) injects at the suspension point (ptr, TDD-00086)
	GeneratorJmpStkField     = "__jmpStk"     // this generator's own isolated jmpbuf stack (ptr, so caller/body try-frames never interleave — TDD-00086)
	GeneratorJmpTopField     = "__jmpTop"     // the generator's jmpbuf-stack top saved across suspension (i64, TDD-00086)
	GeneratorGenErrorField   = "__genError"   // an uncaught body throw the outer catch-all captured, re-thrown on the caller side (ptr, TDD-00086)
	GeneratorThisField       = "__this"       // a generator method's receiver (ptr), bound to `this` at body entry (TDD-00063 Stage 2b)
	GeneratorEnvField        = "__env"        // a nested generator's closure environment (ptr to the captured-cell struct, null when it captures nothing) — TDD-00094
	GeneratorHasRetField     = "__hasret"     // true once the body completed with a value (`return expr`) not yet handed to a caller's next()

	// Async-generator step state (the spec-faithful step model: synchronous
	// body start, park-at-await, request queueing). Present on every
	// generator's layout for uniformity; sync generators never touch them.
	GeneratorPendingQField = "__pendingQ" // the in-flight step's result promise (ptr; null = no step running)
	GeneratorParkedField   = "__parked"   // i64 1 while the fiber is suspended at an await-park (vs a yield/completion)
	GeneratorReqHeadField  = "__reqHead"  // queued .next/.throw/.return requests while a step is in flight (ptr to {i64 mode, ptr sentSlot, ptr thrown, ptr q, ptr next})
	GeneratorReqTailField  = "__reqTail"  // tail of the request FIFO (ptr)
	// How many declared parameters the call passed, plus one (0: not
	// known, every declared one counts) — the body's `arguments` length.
	GeneratorArgcField = "__argc"
)

// GeneratorType returns a `function* f(params): T {}`'s instance value type
// — see IsGenerator's own doc comment for why this reuses ObjectType purely
// for its struct-layout machinery rather than actually being IsObject.
// paramTypes is the generator function's own declared parameter types, in
// order, stored as trailing fields so construction can populate them once
// and the generator body can read them back on its own fiber stack.
// CollIterType is the type of an iterator over src (an array, Map or Set)
// yielding kind.
func CollIterType(src Type, kind mapIterKind) Type {
	srcCopy := src
	return Type{IR: "ptr", Host: hostCollIter, IterKind: kind, IterSrc: &srcCopy}
}

func GeneratorType(elem Type, paramTypes []Type, thisTy *Type, isAsync bool) Type {
	elemCopy := elem
	fields := []Field{
		{Name: GeneratorProtoField, Ty: TypeAny},
		{Name: GeneratorCtxField, Ty: TypePtr},
		{Name: GeneratorStackField, Ty: TypePtr},
		{Name: GeneratorCallerCtxField, Ty: TypePtr},
		{Name: GeneratorStartedField, Ty: TypeBool},
		{Name: GeneratorDoneField, Ty: TypeBool},
		{Name: GeneratorYieldedField, Ty: elem},
		{Name: GeneratorSentField, Ty: TypeAny},
		{Name: GeneratorResumeModeField, Ty: TypeI64},
		{Name: GeneratorThrownField, Ty: TypePtr},
		{Name: GeneratorJmpStkField, Ty: TypePtr},
		{Name: GeneratorJmpTopField, Ty: TypeI64},
		{Name: GeneratorGenErrorField, Ty: TypePtr},
		{Name: GeneratorEnvField, Ty: TypePtr},
		{Name: GeneratorHasRetField, Ty: TypeBool},
		{Name: GeneratorPendingQField, Ty: TypePtr},
		{Name: GeneratorParkedField, Ty: TypeI64},
		{Name: GeneratorReqHeadField, Ty: TypePtr},
		{Name: GeneratorReqTailField, Ty: TypePtr},
		{Name: GeneratorArgcField, Ty: TypeI64},
	}
	// A generator *method* (TDD-00063 Stage 2b) carries its receiver in a
	// dedicated __this slot (a ptr to the class instance), stored at
	// construction and read back once at the body's entry to bind `this` —
	// the exact same fiber-stack-persistence story the __paramN slots use.
	// nil for a free-function generator, which has no receiver.
	if thisTy != nil {
		fields = append(fields, Field{Name: GeneratorThisField, Ty: *thisTy})
	}
	for i, pt := range paramTypes {
		fields = append(fields, Field{Name: fmt.Sprintf("__param%d", i), Ty: pt})
	}
	ty := ObjectType(fields)
	ty.setKind(kindObject, false)
	ty.setHost(hostGenerator, true)
	ty.GeneratorIsAsync = isAsync
	ty.GeneratorElemType = &elemCopy
	return ty
}

// ArrayBufferType returns `new ArrayBuffer(...)`'s result type — see
// IsArrayBuffer's doc comment for the hidden-struct representation.
func ArrayBufferType() Type {
	return Type{IR: "ptr", Host: hostArrayBuffer}
}

// SharedArrayBufferType returns `new SharedArrayBuffer(...)`'s result type —
// the ArrayBuffer representation plus the shared-across-workers flag.
func SharedArrayBufferType() Type {
	return Type{IR: "ptr", Host: hostSharedArrayBuffer}
}

// RegexHandleField is the name of the hidden ptr field every RegExp
// instance carries at index 0, holding the compiled pcre2_code* handle —
// never exposed via VisibleFields()/Object.keys/JSON, same convention
// ClassTagField uses for a class instance's hidden tag. RegExp has no
// user-declarable fields to collide with (unlike a class), so this exists
// purely for readability at GEP call sites, not collision safety.
const RegexHandleField = "__kml_regex_handle"

// RegExpType returns `new RegExp(pattern, flags?)`'s (and a
// `/pattern/flags` literal's) result type — see IsRegExp's doc comment for
// why this is a real heap object rather than a stateless marker.
// source/flags carry the original constructor arguments verbatim;
// global/ignoreCase/multiline/dotAll are decomposed once at construction
// (V1's supported flag set — see docs/tdd/TDD-00035.md's flag scope table;
// u/d are deferred; `y` since ADR-01062) so no method needs to re-parse the flags string;
// lastIndex is mutable, `g`-flag iteration state.
func RegExpType() Type {
	ty := hostObjectType([]Field{
		{Name: RegexHandleField, Ty: TypePtr},
		{Name: "source", Ty: TypePtr},
		{Name: "flags", Ty: TypePtr},
		{Name: "global", Ty: TypeBool},
		{Name: "ignoreCase", Ty: TypeBool},
		{Name: "multiline", Ty: TypeBool},
		{Name: "dotAll", Ty: TypeBool},
		{Name: "sticky", Ty: TypeBool},
		{Name: "lastIndex", Ty: TypeI64},
	})
	ty.setHost(hostRegExp, true)
	return ty
}

// BufferType returns a Node Buffer's type (TDD-00103): a Uint8Array
// (TypedArrayType("uint8")) with IsBuffer set — see the flag's doc comment.
func BufferType() Type {
	ty := TypedArrayType("uint8")
	ty.setHost(hostBuffer, true)
	return ty
}

// typedArrayElemKindToType maps the element-kind strings the parser already
// produces (parser/parser_literals.go's typedArrayElemKinds, and the same
// names ResolveTypeName below already understands for JSDoc @type
// annotations) to the concrete numeric Type each TypedArray variant stores.
var typedArrayElemKindToType = map[string]Type{
	"int8":    TypeI8,
	"uint8":   TypeU8,
	"int16":   TypeI16,
	"uint16":  TypeU16,
	"int32":   TypeI32,
	"uint32":  TypeU32,
	"float32": TypeF32,
	"float64": TypeF64,
	// TDD-00101: raw storage scalars; the language-level element semantics
	// (bigint handles / clamp-on-write) ride on BigIntElem/Clamped, set by
	// TypedArrayType below.
	"bigint64":     TypeI64,
	"biguint64":    TypeU64,
	"uint8clamped": TypeU8,
}

// TypedArrayType returns the type behind `new Int8Array(...)`/.../
// `new Float64Array(...)` for the given element kind (e.g. "uint8") — see
// IsTypedArray's doc comment for why this is just a flagged ArrayOf(elemTy).
// Panics on an unrecognized kind — callers only ever pass one of the 8
// strings typedArrayElemKindToType covers, sourced from the parser's own
// typedArrayElemKinds map, so an unknown kind here would be a compiler bug,
// not a user-facing error.
func TypedArrayType(elemKind string) Type {
	elemTy, ok := typedArrayElemKindToType[elemKind]
	if !ok {
		panic("TypedArrayType: unknown element kind " + elemKind)
	}
	ty := ArrayOf(elemTy)
	ty.setHost(hostTypedArray, true)
	switch elemKind {
	case "bigint64", "biguint64":
		ty.BigIntElem = true
	case "uint8clamped":
		ty.Clamped = true
	}
	return ty
}

// SettlementType returns Promise.allSettled()'s per-element result shape:
// { status: string, value: T, reason: any }. Both value and reason are always
// allocated regardless of which branch is live (this compiler has no
// optional/union fields) — codegen zero-fills whichever one doesn't apply per
// element (a fulfilled entry's reason is boxed `undefined`; a rejected entry's
// value is T's zero) rather than leave it uninitialized. `reason` is a
// NaN-boxed `any` carrying the ORIGINAL rejected value with full type fidelity
// (TDD-00169): `reject(42)` keeps `typeof reason === "number"` and serializes
// as `42`, and a rejected Error is recovered at the render/JSON/member sites
// via the field-0 Error type-id (TDD-00222) — `reason.message`/`.name` read
// the real fields, `JSON.stringify` yields `{}`. It shares field 2's i64 width
// with the former errorObjType pointer, so the struct layout is unchanged.
func SettlementType(valueTy Type) Type {
	return ObjectType([]Field{
		{Name: "status", Ty: TypePtr},
		{Name: "value", Ty: valueTy},
		{Name: "reason", Ty: TypeAny},
	})
}

// ClassTagField is the name of the hidden i64 tag every class instance
// carries at field index 0 (TDD-00009 Stage 2), identifying which class an
// instance actually is at runtime — needed for instanceof against an
// any/unknown value, where the static type alone can't tell you. Reserved:
// a user-declared field with this name is a compile-time error
// (registerClasses).
const ClassTagField = "__kml_tag"

// ClassVTableField is the name of the hidden vtable-pointer field a class
// carries when HasVTable is set: at index 1, right after the tag, or after
// the error fields in an Error subclass (VTableIndex)
// (TDD-00009 Stage 3) — a plain ptr to that concrete class's own
// @ClassName_vtable global. Reserved the same way ClassTagField is: a
// user-declared field with this name is a compile-time error.
const ClassVTableField = "__kml_vtable"

// ClassType returns a user-defined class's instance type: an ordinary
// object type (see IsObject's doc comment on why this is enough for field
// access, JSON, Object.* etc. to work unmodified) plus IsClass/ClassName so
// method-call dispatch can find the class's registered method table.
//
// Field order is: hidden tag (always, index 0) → hidden vtable pointer
// (only when hasVTable; index 1, or after the error fields an Error
// subclass inherits) → inherited fields (already-flattened, base-first, empty for a
// root class — TDD-00009 Stage 3) → this class's own newly-declared fields.
// FieldIndex/StructIR/StructSize all derive from Fields' order generically,
// so every named field access shifts for free with no changes needed at any
// of those call sites — this is also exactly what makes base-first layout
// work: a Derived* struct's prefix is byte-identical to Base*'s own layout,
// so a Base-typed field access on a Derived instance needs no adjustment.
// Callers that enumerate *all* fields for reflection (Object.keys/values/
// entries, JSON, for...in, spread) must use VisibleFields() instead of
// Fields directly, or the hidden fields leak out as fake user-visible ones.
func ClassType(name string, inherited []Field, own []Field, hasVTable bool) Type {
	tagged := make([]Field, 0, 4+len(inherited)+len(own))
	tagged = append(tagged, Field{Name: ClassTagField, Ty: TypeI64})
	// An Error subclass keeps the error object's fields where every error
	// reader finds them: its vtable pointer comes after them.
	keep := errorFieldsPrefix(inherited)
	if keep == 0 {
		// A host base's body sits at +8 the same way (TDD-00247).
		keep = hostBodyFieldsPrefix(inherited)
	}
	tagged = append(tagged, inherited[:keep]...)
	if hasVTable {
		tagged = append(tagged, Field{Name: ClassVTableField, Ty: TypePtr})
	}
	tagged = append(tagged, inherited[keep:]...)
	tagged = append(tagged, own...)
	ty := classObjectType(tagged) // a class instance's keys may be absent or reordered
	ty.setKind(kindClass, true)
	ty.ClassName = name
	ty.HasVTable = hasVTable
	return ty
}

// errorFieldsPrefix is how many leading fields of inherited are the error
// object's own (an Error subclass's), 0 for any other class. A system slot
// a subclass's own field displaced is renamed `__kml_err_<name>`
// (registerClasses) and still counts.
func errorFieldsPrefix(inherited []Field) int {
	errFields := errorObjType.Fields[1:]
	if len(inherited) < len(errFields) {
		return 0
	}
	for i, f := range errFields {
		if n := inherited[i].Name; n != f.Name && n != "__kml_err_"+f.Name {
			return 0
		}
	}
	return len(errFields)
}

// VTableIndex is the index of a class instance's vtable-pointer field.
func (t Type) VTableIndex() int {
	i, _, _ := t.FieldIndex(ClassVTableField)
	return i
}

// VisibleFields returns the fields a user should ever see: identical to
// Fields for every non-class/non-error object type, but with the hidden
// leading fields (tag, plus vtable pointer when HasVTable) stripped, and (TDD-00021) any `#`-named private field/method filtered out
// regardless of position — real JS reflection never sees a private name at
// all, unlike `private`/`protected` (a TypeScript-only, compile-time-erased
// concept real JS reflection doesn't know exists). Use this instead of
// Fields directly at any reflection/enumeration call site (Object.keys,
// Object.values, Object.entries, Object.assign, JSON.stringify, for...in,
// object-literal/spread field copying) — GEP/field-access code should keep
// using Fields (via FieldIndex) unchanged, since the hidden fields'
// presence is what makes those indices correct in the first place.
// SpreadFields are the own enumerable properties a spread or Object.assign
// copies: the string-keyed ones (VisibleFields), then the symbol-keyed ones
// (TDD-00246).
func (t Type) SpreadFields() []Field {
	sf := symbolFields(t)
	if len(sf) == 0 {
		return t.VisibleFields()
	}
	return append(append([]Field{}, t.VisibleFields()...), sf...)
}

func (t Type) VisibleFields() []Field {
	fields := t.UserFields()
	if n := hostBodyFieldsPrefix(fields); t.IsClass() && n > 0 {
		fields = fields[n:] // a host base's body
	}
	switch {
	case t.IsClass() && t.HasVTable:
		var kept []Field
		for _, f := range fields {
			if f.Name != ClassVTableField {
				kept = append(kept, f)
			}
		}
		fields = kept
	case t.IsRegExp() && len(fields) > 0:
		fields = fields[1:]
	case t.IsXHR() && len(fields) > 3:
		fields = fields[4:]
	case t.IsResponse() || t.IsFetchRequest():
		// A Response's and a Request's properties are accessors on their
		// prototypes: no own enumerable ones (`Object.keys(res)` is `[]`,
		// JSON `{}`).
		fields = nil
	}
	// A private field (`#x`) and a symbol-keyed one (TDD-00246) are no
	// string keys.
	hidden := func(f Field) bool {
		return strings.HasPrefix(f.Name, "#") || strings.HasPrefix(f.Name, symbolFieldPrefix)
	}
	hasHidden := false
	for _, f := range fields {
		if hidden(f) {
			hasHidden = true
			break
		}
	}
	if !hasHidden {
		return fields
	}
	visible := make([]Field, 0, len(fields))
	for _, f := range fields {
		if !hidden(f) {
			visible = append(visible, f)
		}
	}
	return visible
}

// FlatArrayType is a `/** @value */` flat value-type array of elem (TDD-00134
// Stage 2) — see Type.IsFlatArray.
func FlatArrayType(elem Type) Type {
	el := elem
	return Type{IR: "ptr", Kind: kindFlatArray, ElemType: &el}
}

// FinalizationRegistryType is a FinalizationRegistry<T> handle (TDD-00163): a
// pointer to the runtime record { ptr closure, ptr trampoline, ptr reportFn }.
// The held-value type T is carried in ElemType so register(target, held)
// type-checks its held argument against the cleanup callback's parameter.
func FinalizationRegistryType(held Type) Type {
	h := held
	ty := hostObjectType([]Field{{Name: "__finreg", Ty: TypePtr}})
	ty.setHost(hostFinalizationRegistry, true)
	ty.ElemType = &h
	return ty
}

// FuncType returns a closure/function type. All closures are represented as ptr
// at the LLVM level (a pointer to a {funcPtr, envPtr} header on the heap).
func FuncType(params []Type, ret Type) Type {
	retCopy := ret
	return Type{IR: "ptr", Kind: kindFunc, FuncParams: params, FuncRetType: &retCopy}
}

// StructFieldIR returns the LLVM type string used for a field's own storage
// slot inside a struct GEP/load/store — normally the same as ty.IR, except
// an array-typed field, whose slot holds the array's shared header pointer
// (the same representation an array Value has in every other context —
// function returns (see LLVMRetType), .slice()/HOF results, Object.keys()-
// shaped helpers). See docs/adr/00/ADR-00061.md.
func StructFieldIR(ty Type) string {
	if ty.IsArray() {
		// TDD-00213 Stage 2: an array field holds a POINTER to a shared {data,len}
		// header (the object-reference model, TDD-00127) — so a field read aliases
		// the array and a mutation through the
		// field propagates. Load/store of an array field goes through
		// loadArrayFieldValue / storeArrayFieldHeader, which deref/mint the header.
		return "ptr"
	}
	// A nullable non-pointer scalar field carries its presence bit alongside
	// the value (TDD-00064 Stage 3), so a null field is distinguishable from a
	// real 0 — the same { i1, T } slot a nullable-scalar local/param/return uses.
	if isNullableScalar(ty) {
		return nullableScalarStorageIR(ty)
	}
	return ty.IR
}

// StructFieldSize mirrors StructFieldIR's reasoning for struct-layout
// purposes: an array field is an 8-byte header pointer; a nullable scalar
// needs more than ty.Align() alone would suggest (below).
func StructFieldSize(ty Type) int64 {
	if ty.IsArray() {
		// A header pointer (TDD-00213 Stage 2) — 8 bytes.
		return 8
	}
	// A dynamic/union field's storage is the NaN-boxed word (TDD-00156) —
	// one 8-byte i64, same as every other scalar slot.
	if ty.IsDynamic() {
		return 8
	}
	// A nullable-scalar field's { i1, T } slot occupies the payload's alignment
	// (for the leading i1 + padding) plus the payload itself — 2*align for every
	// scalar T, since size == align holds for each (i8..i64, float, double).
	if isNullableScalar(ty) {
		return 2 * int64(ty.Align())
	}
	return int64(ty.Align())
}

// isNullableScalar reports whether ty is a `T | null` / `T | undefined` where
// T is a *non-pointer scalar* (number/i64, boolean/i1, an annotated
// float32/float64, Date/i64) — the one case a plain null pointer cannot stand
// in for "absent," so TDD-00064 Option A gives it a presence-flagged
// { i1 present, T value } storage slot instead. A Nullable *pointer* type
// (string/object/array/class/Map/Set/... — IR "ptr") keeps its existing
// null-pointer representation and is deliberately excluded here; so is a
// Nullable constrained union (IsDynamic — its own { i8, i64 } box already
// carries a null tag) and void.
func isNullableScalar(ty Type) bool {
	return ty.Nullable() && !ty.IsDynamic() && ty.IR != "ptr" && ty.IR != "void"
}

// nullableScalarStorageIR returns the LLVM storage type of a nullable-scalar
// slot: a two-field { i1, T } presence-flagged aggregate. See isNullableScalar.
func nullableScalarStorageIR(ty Type) string {
	return fmt.Sprintf("{ i1, %s }", ty.IR)
}

// storageIR returns the LLVM type a value of ty occupies in its own storage
// slot (a local alloca, and — from Stage 3 on — a parameter/field slot):
// normally ty.IR, except a nullable scalar, which is the { i1, T } aggregate
// above. Plain (non-nullable) scalars, pointers, and the union box are
// unchanged, so nothing on the overwhelmingly common non-nullable path shifts.
func storageIR(ty Type) string {
	if isNullableScalar(ty) {
		return nullableScalarStorageIR(ty)
	}
	return ty.IR
}

// storageAlign returns the alignment of ty's storage slot. A { i1, T }
// nullable-scalar aggregate aligns to its payload T (i1 is align 1), so this
// is just ty.Align() in every case — but named separately from Align() so
// storage-slot call sites read intentionally and stay correct if the layout
// ever gains a wider tag.
func storageAlign(ty Type) int {
	return ty.Align()
}

// StructIR returns the LLVM struct type string, e.g. "{ i64, i32 }".
func (t Type) StructIR() string {
	parts := make([]string, len(t.Fields))
	for i, f := range t.Fields {
		parts[i] = StructFieldIR(f.Ty)
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// StructSize returns the byte size of the struct following natural alignment rules
// (same rules LLVM applies for the same field sequence).
// Assumes size == align for every field type except IsArray (see
// StructFieldSize) — holds for i8..i64, float, double, ptr, and every
// object/Map/Set/closure/Promise field, all of which are a single ptr-sized
// slot regardless of what they point to.
func (t Type) StructSize() int64 {
	offset := int64(0)
	maxAlign := int64(1)
	for _, f := range t.Fields {
		fa := int64(f.Ty.Align())
		fsize := StructFieldSize(f.Ty)
		if fa > maxAlign {
			maxAlign = fa
		}
		if offset%fa != 0 {
			offset = (offset/fa + 1) * fa
		}
		offset += fsize
	}
	// round up to struct alignment
	if offset%maxAlign != 0 {
		offset = (offset/maxAlign + 1) * maxAlign
	}
	return offset
}

// FieldOffset is field i's byte offset, by StructSize's rules.
func (t Type) FieldOffset(i int) int64 {
	offset := int64(0)
	for j, f := range t.Fields {
		fa := int64(f.Ty.Align())
		if offset%fa != 0 {
			offset = (offset/fa + 1) * fa
		}
		if j == i {
			return offset
		}
		offset += StructFieldSize(f.Ty)
	}
	return offset
}

// FieldIndex returns the index, type, and ok of a named field.
func (t Type) FieldIndex(name string) (int, Type, bool) {
	for i, f := range t.Fields {
		if f.Name == name {
			return i, f.Ty, true
		}
	}
	return 0, Type{}, false
}

func (t Type) Align() int {
	switch t.IR {
	case "i8":
		return 1
	case "i16":
		return 2
	case "i32", "float":
		return 4
	case "i64", "double", "ptr":
		return 8
	case "i1":
		return 1
	}
	return 8
}

// zeroLiteral returns t's LLVM zero-value literal text — used by
// unpackArrayPatternInto's out-of-bounds fallback (a destructured position
// past the source array's actual length reads as zero, a deliberate
// simplification documented in ADR-00157, not real JS's `undefined`, which
// this compiler has no general sentinel for on a concrete scalar type).
func (t Type) zeroLiteral() string {
	if t.Float {
		return "0.0"
	}
	if t.IsDynamic() && t.IR == "i64" {
		// A NaN-box's "nothing yet" is `undefined`, not the word 0 (which also
		// decodes as undefined today, but only by the immediate-range fallback).
		return fmt.Sprintf("%d", nbUndefined)
	}
	if t.IR == "ptr" {
		return "null"
	}
	return "0"
}

// IsInteger returns true for integer (non-float) types.
func (t Type) IsInteger() bool { return !t.Float && t.IR != "ptr" && t.IR != "void" }

// isSafeNumericArg reports whether v can be safely passed to an inferred
// (unannotated, defaulted-to-i64) parameter without silently corrupting
// data — see docs/adr/00/ADR-00042.md. IsInteger()/Float already exclude
// ptr-backed types (string/object/array/closure/Promise all use IR "ptr"),
// but a boxed any/unknown value's IR is a distinct aggregate ("{ i8, i64 }")
// that's neither ptr nor float, so IsDynamic needs its own explicit check —
// otherwise it would slip through as if it were already a plain number.
func isSafeNumericArg(t Type) bool {
	return (t.IsInteger() || t.Float) && !t.IsDynamic()
}

// LLVMRetType returns the LLVM IR type string used in function definitions and
// call instructions. Arrays are returned as a pointer to their shared {data,len}
// header (TDD-00213 Stage 3, so identity/aliasing survive the boundary); a
// nullable non-pointer scalar as its presence-flagged { i1, T } aggregate
// (TDD-00064 Stage 3), so `T | null` survives a function boundary with its
// null-ness.
func (t Type) LLVMRetType() string {
	if t.IsArray() {
		// An array is returned as a pointer to its shared {data,len} header
		// (TDD-00213 Stage 3) — so a returned
		// field/global/captured/named array keeps its identity and the caller
		// aliases the same array (mirrors the header-pointer param and field ABI).
		return "ptr"
	}
	if isNullableScalar(t) {
		return nullableScalarStorageIR(t)
	}
	// A by-value small-tuple return (TDD-00134 Stage 3) is the struct
	// aggregate itself, register-passable (unlike the array, which returns
	// its header pointer above).
	if t.IsTuple() && t.TupleByVal {
		return t.StructIR()
	}
	return t.IR
}

// PrintfFmt returns the printf format specifier for this type, or "" for types
// that cannot be printed with a single printf call (e.g. arrays).
func (t Type) PrintfFmt() string {
	if t.IsArray() {
		return ""
	}
	switch t.IR {
	case "i8", "i16", "i32":
		return "%d"
	case "i64":
		// An unsigned 64-bit value with its high bit set (`uint64` above 2^63)
		// needs `%llu`; `%lld` would print it as negative (TDD-00123 — the
		// integer escape hatch).
		if !t.Signed {
			return "%llu"
		}
		return "%lld"
	case "float", "double":
		return "%g"
	case "i1":
		return "%d"
	case "ptr":
		return "%s"
	}
	return "%d"
}

var (
	TypeVoid      = Type{IR: "void"}
	TypeNever     = Type{IR: "i64", Kind: kindNever}
	TypeBool      = Type{IR: "i1", Signed: false}
	TypeI8        = Type{IR: "i8", Signed: true}
	TypeI16       = Type{IR: "i16", Signed: true}
	TypeI32       = Type{IR: "i32", Signed: true}
	TypeI64       = Type{IR: "i64", Signed: true}
	TypeU8        = Type{IR: "i8", Signed: false}
	TypeU16       = Type{IR: "i16", Signed: false}
	TypeU32       = Type{IR: "i32", Signed: false}
	TypeU64       = Type{IR: "i64", Signed: false}
	TypeF32       = Type{IR: "float", Float: true}
	TypeF64       = Type{IR: "double", Float: true}
	TypePtr       = Type{IR: "ptr"}
	TypeNull      = Type{IR: "ptr", Kind: kindNull}
	TypeUndefined = Type{IR: "ptr", Kind: kindUndefined}
	// TypeAny backs any/unknown: the NaN-boxed single-word dynamic value
	// (TDD-00156) — numbers offset-encoded doubles, heap references
	// low-3-bit-kind-tagged raw pointers, undefined/null/true/false small
	// immediates. The logical kmlTag* model decodes from it via
	// __kml_nb_tag/__kml_nb_pay (runtime_nanbox.go).
	TypeAny = Type{IR: "i64", Kind: kindDynamic}
	// TypeCaught backs a catch-clause variable (TDD-00202): the unpacked
	// { i8 tag, i64 payload } thrown-value record (TypeScript `unknown`).
	TypeCaught = Type{IR: "{ i8, i64 }", Kind: kindCaught}
	// TypeDate backs Date: a headered heap object holding the time value.
	TypeDate = Type{IR: "ptr", Host: hostDate}
)

// FuncSig holds the signature of a user-defined function.
type FuncSig struct {
	// This marks a function declared with a `this: T` parameter, which is
	// ParamTypes[0] (maybeAddThisParam).
	This bool
	// RetThis marks a method returning the polymorphic `this`: a call's
	// result has the receiver's type (`http.Server`'s inherited
	// `listen(…)` returns the http.Server).
	RetThis    bool
	ParamTypes []Type
	ParamNames []string // for error messages only (e.g. an inferred-parameter type mismatch)
	RetType    Type
	// MaySuspend marks an async function that can actually suspend at an await
	// (it awaits a fetch, a Promise combinator over fetch, or another
	// may-suspend async function — transitively). Such functions are compiled as
	// coroutine tasks (runtime_task.go / TDD-00083 Stage 2) rather than the
	// inlined synchronous malloc-slot model. A purely-synchronous async function
	// (no such await) keeps the fast path unchanged, so non-suspending programs
	// are byte-for-byte identical.
	MaySuspend bool
	// IsAsync marks an `async` function (declaration or arrow). Every async
	// function returns a real task-shaped promise now (TDD-00084 Part A): a
	// may-suspend one via a coroutine fiber, a non-suspending one via an inline
	// catch-and-settle wrapper that settles the promise on return / rejects it on
	// throw. Used to tag a call's result type PromiseTask regardless of MaySuspend.
	IsAsync  bool
	HasRest  bool             // last param is a rest (variadic) parameter
	Defaults []ast.Expression // per-param default expression; nil entry means no default
	// HasDefault marks each parameter declared with a default, whether its
	// caller (Defaults, a destructured one) or the body fills it.
	HasDefault []bool
	// Optional marks a `param?: T` parameter (ADR-00164) — a call site
	// omitting it (and with no Defaults[i] expression either) gets T's own
	// zero value (Type.zeroLiteral()) rather than a "missing argument"
	// error, this compiler's established stand-in for real JS's
	// `undefined` on a concrete type with no general sentinel for it (the
	// same simplification ADR-00157's calloc fix and ADR-00158's
	// destructuring defaults both already document).
	Optional []bool
}

// ResolveTypeName maps a primitive TypeScript or JSDoc type name (and the
// native widths) to an LLVM Type. Handles array suffixes (e.g. "int32[]")
// and falls back to i64 for unknowns. A builtin interface's layout comes from
// its declaration (Emitter.resolveTypeName).
func ResolveTypeName(name string) Type {
	// Array suffix: T[]
	if len(name) > 2 && name[len(name)-2:] == "[]" {
		elem := ResolveTypeName(name[:len(name)-2])
		return ArrayOf(elem)
	}
	switch name {
	case "number":
		// TDD-00123 Stage 1: `number` is a JS-faithful IEEE-754 double. The
		// integer types (`int8..int64`, `uint8..uint64`) remain the opt-in
		// escape hatch for real integers.
		return TypeF64
	case "string":
		return TypePtr
	case "boolean", "true", "false":
		// A boolean literal type (`(): true`) is a boolean.
		return TypeBool
	case "void":
		return TypeVoid
	case "never":
		return TypeNever // a function that never returns (its IR the i64 default's)
	case "null":
		return TypeNull
	case "undefined":
		return TypeUndefined
	case "symbol":
		return SymbolType()
	case "bigint":
		return BigIntType()
	case "any", "unknown", "object":
		return TypeAny
	case "RegExpExecArray":
		// What `exec` returns (`| null` is the nullable form).
		return execArrayType()
	case "RegExpMatchArray":
		return matchArrayType()
	case "int8":
		return TypeI8
	case "int16":
		return TypeI16
	case "int32":
		return TypeI32
	case "int64":
		return TypeI64
	case "uint8":
		return TypeU8
	case "uint16":
		return TypeU16
	case "uint32":
		return TypeU32
	case "uint64":
		return TypeU64
	case "float32":
		return TypeF32
	case "float64":
		return TypeF64
	case "ArrayBufferLike":
		// An ArrayBuffer or a SharedArrayBuffer: one representation.
		return ArrayBufferType()
	case "ArrayBufferView", "BufferSource", "AllowSharedBufferSource":
		// A typed array or a DataView (or, for the last two, a buffer):
		// held boxed, each value as its own class.
		return TypeAny
	}
	return TypeI64 // default
}

// nonPtrScalar reports a present, unboxed scalar (a number or boolean): the
// other side a conditional's element-read branch keeps its absence against.
func nonPtrScalar(t Type) bool {
	return !t.IsNull() && !t.IsDynamic() && !t.IsArray() && !t.IsObject() &&
		t.IR != "ptr" && t.IR != "void" && !t.Nullable()
}

// absentMask is t's presence bits that mark a key's absence rather than
// its presence: a required field's. A zeroed word then has every required
// key, whichever path allocated the object; only a JSON input leaving the
// key out, a `declare` field and delete set the bit.
func (t Type) absentMask() uint64 {
	var m uint64
	for _, f := range t.Fields {
		if b := t.presenceBit(f.Name); b >= 0 && !f.Optional && !f.Ty.UncheckedIndex {
			m |= uint64(1) << uint(b)
		}
	}
	return m
}
