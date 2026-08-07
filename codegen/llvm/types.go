package llvm

import (
	"KlainMainLang/ast"
	"strings"
)

// Field is one field in an object type.
type Field struct {
	Name string
	Ty   Type
}

// Type represents an LLVM IR type.
type Type struct {
	IR       string // e.g. "i64", "i32", "float", "double", "i1", "ptr"
	Signed   bool
	Float    bool
	IsArray  bool
	ElemType *Type // non-nil when IsArray
	IsObject bool
	Fields   []Field // non-nil when IsObject
	// Function/closure type: all closures are passed as ptr.
	IsFunc      bool
	FuncParams  []Type
	FuncRetType *Type // nil means void
	// IsGroupMap marks the result of Object.groupBy: a heap ptr to a dynamic
	// string-keyed map of typed sub-arrays. ElemType is the element type of
	// each bucket. Bracket-notation access returns ArrayOf(*ElemType).
	IsGroupMap bool
	// IsMap / IsSet mark Map<K,V> and Set<T> heap objects.
	// MapKey holds the key type; MapVal holds the value type (nil for Set).
	IsMap  bool
	IsSet  bool
	MapKey *Type
	MapVal *Type
	// IsDynamicObject marks an object literal that had at least one computed
	// property key (`{ [expr]: value }`). Storage-wise it IS a real
	// Map<string,V> (IsMap is also set, MapKey is TypePtr) — IsDynamicObject
	// only additionally enables `.field` / `[expr]` dot/bracket sugar over
	// the existing Map .get()/.set() machinery at emitMember/emitIndex/
	// emitAssign. Plain Map<K,V> variables never set this flag, so their
	// behavior is unchanged. See docs/tdd/TDD-00012.md.
	IsDynamicObject bool
	// IsNull marks the null/undefined literal sentinel type (ptr null at IR level).
	// IsUndefined distinguishes `undefined` from `null` for string rendering.
	// Nullable marks T | null / T | undefined type annotations.
	IsNull      bool
	IsUndefined bool
	Nullable    bool
	// IsPromise marks Promise<T> types (the coroutine handle, IR type ptr).
	// PromiseType is the T in Promise<T>; nil means Promise<void>.
	IsPromise   bool
	PromiseType *Type
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
	// IsDynamic marks any/unknown: a runtime-tagged { i8, i64 } box (tag +
	// payload) instead of one fixed concrete storage type. See emit_dynamic.go.
	IsDynamic bool
	// IsDate marks Date: a plain i64 milliseconds-since-epoch timestamp, same
	// storage as number, distinguished only so method dispatch (getFullYear,
	// toISOString, etc.) can recognize it. See emit_date.go.
	IsDate bool
	// IsResponse marks fetch()'s Response type: an ordinary heap object
	// (status, ok, body fields — all plain field reads via the existing
	// object machinery) with two extra dispatched methods, text()/json().
	// See emit_fetch.go.
	IsResponse bool
	// IsClass marks a user-defined `class` instance (TDD-00009 Stage 1):
	// storage-wise it's an ordinary IsObject heap struct (ClassType always
	// also sets IsObject, so every existing generic object mechanism —
	// StructIR, FieldIndex, emitMember, Object.* statics, JSON — applies with
	// no changes). IsClass/ClassName exist purely so method-call dispatch
	// (which needs a name to look up a method table) and, later,
	// `instanceof` (Stage 2) can recognize a class instance as distinct from
	// a plain structural object literal.
	IsClass   bool
	ClassName string
	// HasVTable marks a class whose instances carry a hidden vtable-pointer
	// field at index 1 (right after the tag field), for TDD-00009 Stage 3
	// dynamic dispatch. Only set for a class belonging to an inheritance
	// tree where at least one method is overridden somewhere in that tree —
	// see registerClasses' override analysis. A class with no inheritance
	// relationship at all, or one whose whole tree has zero overrides,
	// leaves this false and keeps the exact Stage 1/2 single-tag layout.
	HasVTable bool
	// IsError marks Error and its built-in subtypes (TypeError, RangeError,
	// ...) — TDD-00013 Option A. Storage-wise it's an ordinary IsObject heap
	// struct with a hidden kind tag at field 0 (same ClassTagField-style
	// convention IsClass uses, see VisibleFields), then message/name. Every
	// kind shares this one Type value; only the runtime kind tag (and
	// message/name's stored contents) differ between e.g. a TypeError and a
	// RangeError. See emit_exceptions.go's errorKinds/errorKindIDs.
	IsError bool
	// IsRequest marks http.listen()'s Request object (RequestType): an
	// ordinary heap object like Response/URL, plus this flag so method-call
	// dispatch can recognize a Request receiver — needed for
	// `req.bodyBytes(): ArrayBuffer` (TDD-00026/ADR-00106), the one
	// dispatched method a Request has; every other property is a plain
	// field read via the existing object machinery, same as Response/URL.
	IsRequest bool
	// IsURL marks `new URL(...)`'s result: an ordinary heap object (href,
	// protocol, host, hostname, port, pathname, search, hash, origin,
	// searchParams — all plain field reads via the existing object
	// machinery, no dispatched methods of its own) built by parsing through
	// libcurl's URL API. See emit_url.go.
	IsURL bool
	// IsURLSearchParams marks `new URLSearchParams(...)` and `URL`'s own
	// `.searchParams` field: storage-wise it IS a real Map<string,string>
	// (IsMap is also set, MapKey/MapVal both TypePtr) — get/set/has/delete/
	// size/keys()/values()/entries()/forEach() all come for free from the
	// existing Map machinery with no changes. IsURLSearchParams only
	// additionally enables `.toString()`/`.getAll()` dispatch at emitCall,
	// which a plain Map<string,string> (e.g. http.listen's `req.query`,
	// built the same way) does not get. V1 scope narrowing: single value
	// per key, like `req.query` already has — a repeated query-string key
	// silently keeps only the last value, so `.getAll()` never returns more
	// than one element. See emit_url.go.
	IsURLSearchParams bool
	// IsEventEmitter marks `new EventEmitter<T>()`'s result (TDD-00023):
	// storage-wise it's a ptr to a Map<string,ptr> handle (event name →
	// listener-list heap struct), but deliberately does NOT set IsMap —
	// unlike IsURLSearchParams, EventEmitter's method surface (on/once/emit/
	// off/removeListener/removeAllListeners/listenerCount/eventNames) shares
	// no names with Map's, and letting .get()/.set()/.forEach() leak onto an
	// EventEmitter value would be a real, silent correctness gap. The
	// underlying __kml_map_str_* helpers are called directly by name from
	// emit_eventemitter.go instead. EventEmitterPayload is the T in
	// EventEmitter<T> — every listener/emit call site needs it to know the
	// payload's IR shape. See emit_eventemitter.go and docs/tdd/TDD-00023.md.
	IsEventEmitter      bool
	EventEmitterPayload *Type
	// HasEventEmitter marks a class whose instances carry a hidden
	// listener-map-handle field (TDD-00023) — set for a class that directly
	// `extends EventEmitter<T>`, and propagated to every descendant the same
	// way HasVTable propagates across an inheritance tree. See
	// ClassEventEmitterField and registerClasses.
	HasEventEmitter bool
	// IsArrayBuffer marks `new ArrayBuffer(byteLength)`: a fixed-length,
	// zero-initialized raw byte buffer. Deliberately not IsObject — the
	// runtime value is a ptr to a hidden 2-word heap struct ({i64
	// byteLength, ptr data}), never exposed via the generic FieldIndex/
	// Object.keys/JSON reflection path (same reasoning Map/Set's own hidden
	// layout already uses). `.byteLength` gets its own dedicated property
	// read in emitMember, the same pattern `.size` already uses for
	// Map/Set. See emit_arraybuffer.go and docs/tdd/TDD-00018.md.
	IsArrayBuffer bool
	// IsTypedArray marks a TypedArray (Int8Array/Uint8Array/Int16Array/
	// Uint16Array/Int32Array/Uint32Array/Float32Array/Float64Array — no
	// Uint8ClampedArray/BigInt64Array/BigUint64Array, see the TDD's
	// "Deliberately out of scope" section). Storage-wise it IS a plain
	// ArrayOf(elemTy) — IsArray/ElemType are set exactly like a `number[]`
	// — so indexing, `.length`, `.fill`/`.slice`/`.reverse`/`.at`/
	// `.indexOf`/`.includes`/`.map`/`.filter`/`.reduce`/`.forEach`/`.some`/
	// `.every`, for-of, and `.keys()`/`.values()`/`.entries()` all come for
	// free from the existing array machinery with no changes at all.
	// IsTypedArray only additionally enables `.set()`/`.subarray()`/
	// `.byteLength` dispatch at emitCall/emitMember, which a plain
	// `number[]` does not get. See emit_arraybuffer.go and
	// docs/tdd/TDD-00018.md.
	IsTypedArray bool
	// IsTextEncoder marks `new TextEncoder()`'s result: a stateless marker
	// value (Ref is always the constant "null" — nothing is ever allocated
	// or read through it) whose only purpose is letting `.encode(str)`
	// dispatch at emitCall. See emit_call_encoding.go and
	// docs/status/ENCODING-TEXT.md.
	IsTextEncoder bool
	// IsTextDecoder marks `new TextDecoder(label?)`'s result — same
	// stateless-marker shape as IsTextEncoder (label is evaluated for side
	// effects at construction but never stored: V1 scope is UTF-8 only, see
	// NewTextDecoderExpression's doc comment). Enables `.decode(bytes)`
	// dispatch at emitCall. See emit_call_encoding.go and
	// docs/status/ENCODING-TEXT.md.
	IsTextDecoder bool
	// IsRegExp marks `new RegExp(pattern, flags?)`'s result (and a
	// `/pattern/flags` literal, which desugars to the same construction at
	// parse time) — a real heap object, not a stateless marker like
	// IsTextEncoder/IsTextDecoder: avoiding pattern recompilation on every
	// method call and the `.lastIndex` mutable state the `g`-flag iteration
	// idiom needs both require real per-instance storage. A hidden field at
	// index 0 (named RegexHandleField, skipped by VisibleFields() — same
	// convention IsError's hidden kind tag uses) holds the compiled
	// pcre2_code* handle, never exposed via Object.keys/JSON. See
	// RegExpType and docs/tdd/TDD-00035.md.
	IsRegExp bool
	// Inferred marks a parameter type that defaulted to TypeI64 because no
	// explicit annotation was given, as opposed to a real `number`/`int32`/
	// etc. annotation that happens to also resolve to i64. Call sites use
	// this to reject a non-numeric argument against an unannotated
	// parameter at compile time, instead of silently bit-reinterpreting it
	// as an i64 (see docs/adr/ADR-00042.md).
	Inferred bool
}

// ArrayOf returns an array type whose elements are of the given type.
func ArrayOf(elem Type) Type {
	return Type{IR: "ptr", IsArray: true, ElemType: &elem}
}

// ObjectType returns an object type with the given fields.
func ObjectType(fields []Field) Type {
	return Type{IR: "ptr", IsObject: true, Fields: fields}
}

// MapType returns a Map<key,val> type.
func MapType(key, val Type) Type {
	return Type{IR: "ptr", IsMap: true, MapKey: &key, MapVal: &val}
}

// SetType returns a Set<elem> type.
func SetType(elem Type) Type {
	return Type{IR: "ptr", IsSet: true, MapKey: &elem}
}

// PromiseOf returns a Promise<T> type (the coroutine handle ptr).
// Pass TypeVoid for Promise<void>.
func PromiseOf(inner Type) Type {
	if inner.IR == "void" {
		return Type{IR: "ptr", IsPromise: true}
	}
	innerCopy := inner
	return Type{IR: "ptr", IsPromise: true, PromiseType: &innerCopy}
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
	ty := ObjectType([]Field{
		{Name: "status", Ty: TypeI64},
		{Name: "ok", Ty: TypeBool},
		{Name: "body", Ty: TypePtr},
		{Name: "bodyLength", Ty: TypeI64},
	})
	ty.IsResponse = true
	return ty
}

// URLSearchParamsType returns the type behind `new URLSearchParams(...)`
// and `URL`'s own `.searchParams` field — see IsURLSearchParams's doc
// comment for why this is just a flagged Map<string,string>.
func URLSearchParamsType() Type {
	ty := MapType(TypePtr, TypePtr)
	ty.IsURLSearchParams = true
	return ty
}

// URLType returns `new URL(...)`'s result type: a plain heap object whose
// fields are all built once at construction time by emit_url.go (via
// libcurl's URL-parsing API), plus IsURL so nothing else needs to special-
// case it — field reads go through the ordinary object machinery exactly
// like Response's status/ok/body already do.
func URLType() Type {
	ty := ObjectType([]Field{
		{Name: "href", Ty: TypePtr},
		{Name: "protocol", Ty: TypePtr},
		{Name: "host", Ty: TypePtr},
		{Name: "hostname", Ty: TypePtr},
		{Name: "port", Ty: TypePtr},
		{Name: "pathname", Ty: TypePtr},
		{Name: "search", Ty: TypePtr},
		{Name: "hash", Ty: TypePtr},
		{Name: "origin", Ty: TypePtr},
		{Name: "searchParams", Ty: URLSearchParamsType()},
	})
	ty.IsURL = true
	return ty
}

// EventEmitterType returns `new EventEmitter<T>()`'s result type — see
// IsEventEmitter's doc comment for why this is a fully independent flag
// rather than a flavor of Map, despite reusing Map's runtime helpers under
// the hood. See docs/tdd/TDD-00023.md.
func EventEmitterType(payload Type) Type {
	payloadCopy := payload
	return Type{IR: "ptr", IsEventEmitter: true, EventEmitterPayload: &payloadCopy}
}

// ArrayBufferType returns `new ArrayBuffer(...)`'s result type — see
// IsArrayBuffer's doc comment for the hidden-struct representation.
func ArrayBufferType() Type {
	return Type{IR: "ptr", IsArrayBuffer: true}
}

// TextEncoderType returns `new TextEncoder()`'s result type — see
// IsTextEncoder's doc comment for why this holds no real storage.
func TextEncoderType() Type {
	return Type{IR: "ptr", IsTextEncoder: true}
}

// TextDecoderType returns `new TextDecoder(...)`'s result type — see
// IsTextDecoder's doc comment for why this holds no real storage.
func TextDecoderType() Type {
	return Type{IR: "ptr", IsTextDecoder: true}
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
// u/y/d are deferred) so no method needs to re-parse the flags string;
// lastIndex is mutable, `g`-flag iteration state.
func RegExpType() Type {
	ty := ObjectType([]Field{
		{Name: RegexHandleField, Ty: TypePtr},
		{Name: "source", Ty: TypePtr},
		{Name: "flags", Ty: TypePtr},
		{Name: "global", Ty: TypeBool},
		{Name: "ignoreCase", Ty: TypeBool},
		{Name: "multiline", Ty: TypeBool},
		{Name: "dotAll", Ty: TypeBool},
		{Name: "lastIndex", Ty: TypeI64},
	})
	ty.IsRegExp = true
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
	ty.IsTypedArray = true
	return ty
}

// SettlementType returns Promise.allSettled()'s per-element result shape:
// { status: string, value: T, reason: Error }. Both value and reason are
// always allocated regardless of which branch is live (this compiler has no
// optional/union fields) — codegen must explicitly zero-fill whichever one
// doesn't apply per element (null ptr, matching errorObjType/most T's own
// ptr-sized IR) rather than leave it uninitialized, so e.g. a fulfilled
// entry's .reason reads a defined null instead of garbage. reason reuses
// emit_exceptions.go's existing errorObjType (the same shape thrown/caught
// values already use), so a rejected entry's .reason.message/.reason.name
// is readable exactly like any caught Error's.
func SettlementType(valueTy Type) Type {
	return ObjectType([]Field{
		{Name: "status", Ty: TypePtr},
		{Name: "value", Ty: valueTy},
		{Name: "reason", Ty: errorObjType},
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
// carries at index 1 (right after the tag) when HasVTable is set
// (TDD-00009 Stage 3) — a plain ptr to that concrete class's own
// @ClassName_vtable global. Reserved the same way ClassTagField is: a
// user-declared field with this name is a compile-time error.
const ClassVTableField = "__kml_vtable"

// ClassEventEmitterField is the name of the hidden ptr field a class carries
// (TDD-00023) when HasEventEmitter is set — positioned right after the tag
// (and vtable pointer, if present). Holds a Map<string,ptr> handle (event
// name → listener-list heap struct). Set for a class that directly `extends
// EventEmitter<T>`, and every descendant down its inheritance chain.
// Reserved the same way ClassTagField/ClassVTableField are: a user-declared
// field with this name is a compile-time error.
const ClassEventEmitterField = "__kml_ee_listeners"

// ClassType returns a user-defined class's instance type: an ordinary
// object type (see IsObject's doc comment on why this is enough for field
// access, JSON, Object.* etc. to work unmodified) plus IsClass/ClassName so
// method-call dispatch can find the class's registered method table.
//
// Field order is: hidden tag (always, index 0) → hidden vtable pointer
// (only when hasVTable, index 1) → hidden EventEmitter listener-map handle
// (only when hasEventEmitter, TDD-00023 — index 1 or 2 depending on
// hasVTable) → inherited fields (already-flattened, base-first, empty for a
// root class — TDD-00009 Stage 3) → this class's own newly-declared fields.
// FieldIndex/StructIR/StructSize all derive from Fields' order generically,
// so every named field access shifts for free with no changes needed at any
// of those call sites — this is also exactly what makes base-first layout
// work: a Derived* struct's prefix is byte-identical to Base*'s own layout,
// so a Base-typed field access on a Derived instance needs no adjustment.
// Callers that enumerate *all* fields for reflection (Object.keys/values/
// entries, JSON, for...in, spread) must use VisibleFields() instead of
// Fields directly, or the hidden fields leak out as fake user-visible ones.
func ClassType(name string, inherited []Field, own []Field, hasVTable, hasEventEmitter bool) Type {
	tagged := make([]Field, 0, 3+len(inherited)+len(own))
	tagged = append(tagged, Field{Name: ClassTagField, Ty: TypeI64})
	if hasVTable {
		tagged = append(tagged, Field{Name: ClassVTableField, Ty: TypePtr})
	}
	if hasEventEmitter {
		tagged = append(tagged, Field{Name: ClassEventEmitterField, Ty: TypePtr})
	}
	tagged = append(tagged, inherited...)
	tagged = append(tagged, own...)
	ty := ObjectType(tagged)
	ty.IsClass = true
	ty.ClassName = name
	ty.HasVTable = hasVTable
	ty.HasEventEmitter = hasEventEmitter
	return ty
}

// VisibleFields returns the fields a user should ever see: identical to
// Fields for every non-class/non-error object type, but with the hidden
// leading fields (tag, plus vtable pointer when HasVTable, plus the
// EventEmitter listener-map handle when HasEventEmitter — TDD-00023)
// stripped. Use this instead of Fields directly at any reflection/
// enumeration call site (Object.keys, Object.values, Object.entries,
// Object.assign, JSON.stringify, for...in, object-literal/spread field
// copying) — GEP/field-access code should keep using Fields (via
// FieldIndex) unchanged, since the hidden fields' presence is what makes
// those indices correct in the first place.
func (t Type) VisibleFields() []Field {
	if t.IsClass && len(t.Fields) > 0 {
		skip := 1
		if t.HasVTable {
			skip++
		}
		if t.HasEventEmitter {
			skip++
		}
		return t.Fields[skip:]
	}
	if t.IsError && len(t.Fields) > 0 {
		return t.Fields[1:]
	}
	if t.IsRegExp && len(t.Fields) > 0 {
		return t.Fields[1:]
	}
	return t.Fields
}

// RequestType returns http.listen()'s Request object type: a plain heap
// object (readable via the ordinary object field-access path, plus one
// dispatched method — `.bodyBytes(): ArrayBuffer`, TDD-00026/ADR-00106)
// whose fields are built by buildHTTPDispatcher, not by user code.
// query/headers are Map<string,string> — reading them (.get(), .has(),
// iteration, ...) needs no HTTP-specific dispatch at all, since
// resolveMapOrSetForCall's generic arbitrary-expression branch already
// handles any Map-typed field access. headers' keys are lowercased
// (case-insensitive HTTP header names); query keys/values are
// percent-decoded via the same __kml_decode_uri_component decodeURIComponent
// already uses. bodyLength is the real byte count of body (as opposed to
// body's own strlen, which undercounts if the request body contained an
// embedded null byte) — an implementation-only field feeding .bodyBytes(),
// the same "hidden field backing a binary accessor" shape ResponseType's own
// bodyLength already uses for Response.arrayBuffer(). See emit_http.go.
func RequestType() Type {
	ty := ObjectType([]Field{
		{Name: "method", Ty: TypePtr},
		{Name: "path", Ty: TypePtr},
		{Name: "query", Ty: MapType(TypePtr, TypePtr)},
		{Name: "headers", Ty: MapType(TypePtr, TypePtr)},
		{Name: "body", Ty: TypePtr},
		{Name: "bodyLength", Ty: TypeI64},
	})
	ty.IsRequest = true
	return ty
}

// PathParsedType returns path.parse(p)'s result type: a plain heap object
// with root/dir/base/ext/name string fields, recomposed by path.format(obj)
// — no PATH-specific dispatch needed for field reads, same as Response's
// status/ok/body.
func PathParsedType() Type {
	return ObjectType([]Field{
		{Name: "root", Ty: TypePtr},
		{Name: "dir", Ty: TypePtr},
		{Name: "base", Ty: TypePtr},
		{Name: "ext", Ty: TypePtr},
		{Name: "name", Ty: TypePtr},
	})
}

// CPUTimesType returns os.cpus()'s per-core `times` field: cumulative
// milliseconds spent in each state since boot, matching real Node's
// {user, nice, sys, idle, irq} shape (a subset of /proc/stat's own fields —
// `iowait` is read but not reported, matching libuv's own field selection).
func CPUTimesType() Type {
	return ObjectType([]Field{
		{Name: "user", Ty: TypeI64},
		{Name: "nice", Ty: TypeI64},
		{Name: "sys", Ty: TypeI64},
		{Name: "idle", Ty: TypeI64},
		{Name: "irq", Ty: TypeI64},
	})
}

// CPUInfoType returns os.cpus()'s per-core element type — a plain heap
// object (readable via the ordinary object field-access path — no
// dispatched methods), the same "nested object as a field" shape
// SettlementType's `reason: errorObjType` field already establishes.
// speed is MHz (0 on Apple Silicon, where there's no fixed clock-speed
// sysctl — a documented Node.js behavior on M-series Macs too, not a gap).
func CPUInfoType() Type {
	return ObjectType([]Field{
		{Name: "model", Ty: TypePtr},
		{Name: "speed", Ty: TypeI64},
		{Name: "times", Ty: CPUTimesType()},
	})
}

// FuncType returns a closure/function type. All closures are represented as ptr
// at the LLVM level (a pointer to a {funcPtr, envPtr} header on the heap).
func FuncType(params []Type, ret Type) Type {
	retCopy := ret
	return Type{IR: "ptr", IsFunc: true, FuncParams: params, FuncRetType: &retCopy}
}

// StructFieldIR returns the LLVM type string used for a field's own storage
// slot inside a struct GEP/load/store — normally the same as ty.IR, except
// an array-typed field, which needs its length stored alongside the data
// pointer (ty.IR alone is just "ptr", 8 bytes, with nowhere for the length
// to go). Array fields instead use the same {ptr, i64} aggregate shape
// arrays already carry in every other context that evaluates one as a
// plain value — function returns (see LLVMRetType), .slice()/HOF results,
// Object.keys()-shaped helpers — so once a field is stored/loaded this way,
// every downstream consumer (indexing, .length, for...of, return) already
// knows exactly what to do with it; only the storage slot itself needed
// fixing, not how an array Value is represented once you have one.
// See docs/adr/ADR-00061.md.
func StructFieldIR(ty Type) string {
	if ty.IsArray {
		return "{ptr, i64}"
	}
	return ty.IR
}

// StructFieldSize mirrors StructFieldIR's reasoning for struct-layout
// purposes: an array field needs a 16-byte slot (ptr + i64), not the 8 bytes
// ty.Align() alone would suggest for every other field type, where
// size == align already holds.
func StructFieldSize(ty Type) int64 {
	if ty.IsArray {
		return 16
	}
	return int64(ty.Align())
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

// IsInteger returns true for integer (non-float) types.
func (t Type) IsInteger() bool { return !t.Float && t.IR != "ptr" && t.IR != "void" }

// isSafeNumericArg reports whether v can be safely passed to an inferred
// (unannotated, defaulted-to-i64) parameter without silently corrupting
// data — see docs/adr/ADR-00042.md. IsInteger()/Float already exclude
// ptr-backed types (string/object/array/closure/Promise all use IR "ptr"),
// but a boxed any/unknown value's IR is a distinct aggregate ("{ i8, i64 }")
// that's neither ptr nor float, so IsDynamic needs its own explicit check —
// otherwise it would slip through as if it were already a plain number.
func isSafeNumericArg(t Type) bool {
	return (t.IsInteger() || t.Float) && !t.IsDynamic
}

// LLVMRetType returns the LLVM IR type string used in function definitions and
// call instructions. Arrays are returned as an aggregate {ptr, i64}.
func (t Type) LLVMRetType() string {
	if t.IsArray {
		return "{ptr, i64}"
	}
	return t.IR
}

// PrintfFmt returns the printf format specifier for this type, or "" for types
// that cannot be printed with a single printf call (e.g. arrays).
func (t Type) PrintfFmt() string {
	if t.IsArray {
		return ""
	}
	switch t.IR {
	case "i8", "i16", "i32":
		return "%d"
	case "i64":
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
	TypeNull      = Type{IR: "ptr", IsNull: true}
	TypeUndefined = Type{IR: "ptr", IsNull: true, IsUndefined: true}
	// TypeAny backs any/unknown: an anonymous/literal LLVM struct { tag, payload },
	// following the same "literal struct type used directly, no named-type
	// declaration needed" convention ObjectType()'s StructIR() already relies on.
	TypeAny = Type{IR: "{ i8, i64 }", IsDynamic: true}
	// TypeDate backs Date: a plain i64 milliseconds-since-epoch timestamp.
	TypeDate = Type{IR: "i64", Signed: true, IsDate: true}
)

// FuncSig holds the signature of a user-defined function.
type FuncSig struct {
	ParamTypes []Type
	ParamNames []string // for error messages only (e.g. an inferred-parameter type mismatch)
	RetType    Type
	HasRest    bool             // last param is a rest (variadic) parameter
	Defaults   []ast.Expression // per-param default expression; nil entry means no default
}

// ResolveTypeName maps a TypeScript or JSDoc type name to an LLVM Type.
// Handles array suffixes (e.g. "int32[]") and falls back to i64 for unknowns.
func ResolveTypeName(name string) Type {
	// Array suffix: T[]
	if len(name) > 2 && name[len(name)-2:] == "[]" {
		elem := ResolveTypeName(name[:len(name)-2])
		return ArrayOf(elem)
	}
	switch name {
	case "number":
		return TypeI64
	case "string":
		return TypePtr
	case "boolean":
		return TypeBool
	case "void":
		return TypeVoid
	case "null":
		return TypeNull
	case "undefined":
		return TypeUndefined
	case "any", "unknown":
		return TypeAny
	case "Date":
		return TypeDate
	case "Response":
		return ResponseType()
	case "Request":
		return RequestType()
	case "URL":
		return URLType()
	case "URLSearchParams":
		return URLSearchParamsType()
	case "RegExp":
		return RegExpType()
	case "ArrayBuffer":
		return ArrayBufferType()
	case "Int8Array":
		return TypedArrayType("int8")
	case "Uint8Array":
		return TypedArrayType("uint8")
	case "Int16Array":
		return TypedArrayType("int16")
	case "Uint16Array":
		return TypedArrayType("uint16")
	case "Int32Array":
		return TypedArrayType("int32")
	case "Uint32Array":
		return TypedArrayType("uint32")
	case "Float32Array":
		return TypedArrayType("float32")
	case "Float64Array":
		return TypedArrayType("float64")
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
	}
	return TypeI64 // default
}
