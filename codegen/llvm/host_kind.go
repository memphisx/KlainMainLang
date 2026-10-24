package llvm

import "reflect"

// host_kind.go — the host class a value is an instance of (TDD-00230 P3.4).
// One field, Type.Host, says which builtin class a value's native layout
// belongs to; the IsX() predicates read it. A host derived from another
// (a Buffer is a Uint8Array; a SharedArrayBuffer an ArrayBuffer; a
// dynamic object literal a Map) answers its base's predicate too.

// hostKind is a value's host class; hostNone for every non-host value.
type hostKind uint8

const (
	hostNone hostKind = iota
	// hostPromise marks Promise<T> types (the coroutine handle, IR type ptr).
	// PromiseType is the T in Promise<T>; nil means Promise<void>.
	hostPromise
	// hostMap / IsSet mark Map<K,V> and Set<T> heap objects.
	// MapKey holds the key type; MapVal holds the value type (nil for Set).
	hostMap
	hostSet
	// hostDynamicObject marks an object literal that had at least one computed
	// property key (`{ [expr]: value }`). Storage-wise it IS a real
	// Map<string,V> (it derives from hostMap; MapKey is TypePtr) — hostDynamicObject
	// only additionally enables `.field` / `[expr]` dot/bracket sugar over
	// the existing Map .get()/.set() machinery at emitMember/emitIndex/
	// emitAssign. Plain Map<K,V> variables never set this flag, so their
	// behavior is unchanged. See docs/tdd/TDD-00012.md.
	hostDynamicObject
	// hostDate marks Date: a pointer to a one-word body holding the time value
	// (milliseconds since the epoch, dateInvalid for an Invalid Date), with
	// its host header word at body - 8 (TDD-00247). See emit_date.go.
	hostDate
	// hostGenerator marks a `function* name(): T {}`'s instance value (the
	// result of calling the generator function, TDD-00061/ADR-00172) — a
	// ptr to a heap struct carrying its own fiber (ucontext_t + stack),
	// yield/sent value slots, and its own declared parameters. Deliberately
	// does NOT set IsObject, same opaque-handle reasoning
	// comment gives: the struct's fields are internal plumbing (accessed by
	// codegen via GeneratorType's own Fields, built with ObjectType purely
	// to reuse its StructIR/StructSize/FieldIndex machinery), not a
	// user-facing object shape — a generator's only real surface is
	// `.next()`. GeneratorElemType is the T in `function* f(): T` — every
	// yield/`.next()` call site needs it to know the yielded/sent value's
	// own IR shape.
	hostGenerator
	// hostCollIter marks an Array, Map or Set iterator (`arr.values()`,
	// `m.keys()`, `s.entries()`): a ptr to a {next, pos, source} node
	// (ensureMapIters).
	hostCollIter
	// hostTypedArray marks a TypedArray (Int8Array/Uint8Array/Int16Array/
	// Uint16Array/Int32Array/Uint32Array/Float32Array/Float64Array — no
	// Uint8ClampedArray/BigInt64Array/BigUint64Array, see the TDD's
	// "Deliberately out of scope" section). Storage-wise it IS a plain
	// ArrayOf(elemTy) — IsArray/ElemType are set exactly like a `number[]`
	// — so indexing, `.length`, `.fill`/`.slice`/`.reverse`/`.at`/
	// `.indexOf`/`.includes`/`.map`/`.filter`/`.reduce`/`.forEach`/`.some`/
	// `.every`, for-of, and `.keys()`/`.values()`/`.entries()` all come for
	// free from the existing array machinery with no changes at all.
	// hostTypedArray only additionally enables `.set()`/`.subarray()`/
	// `.byteLength` dispatch at emitCall/emitMember, which a plain
	// `number[]` does not get. See emit_arraybuffer.go and
	// docs/tdd/TDD-00018.md.
	hostTypedArray
	// hostBuffer marks a Node Buffer (TDD-00103): storage-wise it IS a
	// Uint8Array (IsTypedArray/IsArray/ElemType u8), so the whole array/
	// TypedArray surface comes free; the flag only additionally enables
	// Buffer's instance-method dispatch (.toString/.write/.copy/.equals/
	// .compare and the read*/write* accessors) at emitCall. See
	// emit_buffer.go.
	hostBuffer
	// hostSymbol marks Symbol(...)'s result (TDD-00044 V1): an ordinary
	// 1-field heap object ({description: string}, description read via the
	// existing object field-access machinery, no dynamic property keys or
	// well-known symbols). Uniqueness/=== come for free from pointer
	// identity — no id/counter field needed. hostSymbol exists so the handful
	// of dispatch sites that would otherwise mishandle it via their generic
	// IsObject path (JSON.stringify, structuredClone, typeof, binary
	// operators, console.log/template-literal formatting) can special-case
	// it instead. See emit_symbol.go.
	hostSymbol
	// hostBigInt marks a `bigint` value (TDD-00074): an opaque `ptr` handle to a
	// heap-allocated arbitrary-precision integer owned by the selected backend
	// library (libtommath/gmp/…), reached only through the __kml_bigint_* ABI —
	// never field-accessed, so deliberately NOT IsObject. The flag exists so the
	// dispatch sites that must treat it specially (operators, typeof,
	// stringification, JSON) branch on it instead of the generic ptr path. See
	// emit_bigint.go.
	hostBigInt
	// hostArrayBuffer marks `new ArrayBuffer(byteLength)`: a fixed-length,
	// zero-initialized raw byte buffer. Deliberately not IsObject — the
	// runtime value is a ptr to a hidden 2-word heap struct ({i64
	// byteLength, ptr data}), never exposed via the generic FieldIndex/
	// Object.keys/JSON reflection path (same reasoning Map/Set's own hidden
	// layout already uses). `.byteLength` gets its own dedicated property
	// read in emitMember, the same pattern `.size` already uses for
	// Map/Set. See emit_arraybuffer.go and docs/tdd/TDD-00018.md.
	hostArrayBuffer
	// hostSharedArrayBuffer marks `new SharedArrayBuffer(byteLength)`
	// (TDD-00099). It derives from hostArrayBuffer (IsArrayBuffer() is true) — layout,
	// `.byteLength`, TypedArray views, and DataView all work through the
	// IsArrayBuffer paths unchanged; this flag only changes the worker
	// boundary (shared by reference, never deep-copied) and, under -mm=gc,
	// the allocation (kml_gc_alloc_pinned, since the only live
	// reference may be on another thread).
	hostSharedArrayBuffer
	// hostError marks Error and its built-in subtypes (TypeError, RangeError,
	// ...) — TDD-00013 Option A. Storage-wise it's an ordinary IsObject heap
	// struct with a hidden kind tag at field 0 (same ClassTagField-style
	// convention IsClass uses, see VisibleFields), then message/name. Every
	// kind shares this one Type value; only the runtime kind tag (and
	// message/name's stored contents) differ between e.g. a TypeError and a
	// RangeError. See emit_exceptions.go's errorKinds/errorKindIDs.
	hostError
	// hostRegExp marks `new RegExp(pattern, flags?)`'s result (and a
	// `/pattern/flags` literal, which desugars to the same construction at
	// parse time) — a real heap object: avoiding pattern recompilation on every
	// method call and the `.lastIndex` mutable state the `g`-flag iteration
	// idiom needs both require real per-instance storage. A hidden field at
	// index 0 (named RegexHandleField, skipped by VisibleFields() — same
	// convention IsError's hidden kind tag uses) holds the compiled
	// pcre2_code* handle, never exposed via Object.keys/JSON. See
	// RegExpType and docs/tdd/TDD-00035.md.
	hostRegExp
	// hostResponse marks fetch()'s Response type: an ordinary heap object
	// (status, ok, body fields — all plain field reads via the existing
	// object machinery) with two extra dispatched methods, text()/json().
	// See emit_fetch.go.
	hostResponse
	// hostFetchRequest marks `new Request(...)`'s result (TDD-00040): an
	// ordinary heap object (url/method/headers/body, all plain field reads)
	// like Response/URL. Named to avoid colliding with the pre-existing,
	// unrelated IsRequest/RequestType (http.listen's server-side `req`
	// object) — both are legitimately called "Request" in user-facing text,
	// but are otherwise unconnected types. See emit_fetch_request.go.
	hostFetchRequest
	// hostXHR marks `new XMLHttpRequest()`'s result (TDD-00040): a real heap
	// object, with hidden fields (method/
	// url/headers, built up by open()/setRequestHeader()) plus visible
	// readyState/status/responseText/response and three zero-argument
	// callback fields (onreadystatechange/onload/onerror — deliberately
	// zero-arg, since a self-referential FuncType field isn't representable here; see
	// the TDD's Design section). send() is synchronous-looking but reuses
	// fetch()'s own non-blocking __kml_fetch_async primitive underneath, so
	// it still yields the current fiber rather than blocking when called
	// from inside an http.listen connection handler. See emit_xhr.go.
	hostXHR
	// hostReadableStream marks `new ReadableStream<T>(...)`'s result (TDD-00097
	// Stage 1): a ptr to a hidden 18-field heap struct (%kml.rstream in
	// runtime_streams.go — state machine, chunk queue with high-water mark,
	// pending-read promise FIFO, underlying-source closures, a per-site
	// "fulfill thunk" that builds typed {value,done} records). Deliberately
	// NOT IsObject, the same opaque-handle reasoning
	// gives. StreamChunk is the T — every enqueue/read site needs it to know
	// the chunk's IR shape (array-shaped chunks span the two queue words).
	// A controller value is the stream itself, distinguished by its flag; a
	// reader or writer is its own object holding the stream (newStreamHandle).
	hostReadableStream
	// hostStreamReader marks getReader()'s/values()' result, whose
	// read()/releaseLock()/cancel()/.closed work on its stream.
	hostStreamReader
	// hostRSController marks the controller passed to the underlying source's
	// start/pull callbacks — the stream pointer retyped so enqueue()/close()/
	// error()/.desiredSize dispatch.
	hostRSController
	// hostWritableStream / IsStreamWriter / IsWSController are the writable
	// side's mirror trio (TDD-00097 Stage 2): one %kml.wstream pointer
	// (runtime_streams_writable.go), retyped at compile time for the stream,
	// getWriter()'s writer, and the sink callbacks' controller.
	hostWritableStream
	hostStreamWriter
	hostWSController
	// hostTransformStream marks `new TransformStream<I, O>(...)` (TDD-00097
	// Stage 3): a ptr to the runtime ts context whose first two fields are
	// the readable (chunk O) and writable (chunk I) stream pointers.
	// StreamChunk carries I, StreamOut carries O.
	hostTransformStream
	// hostFinalizationRegistry marks a FinalizationRegistry<T> handle
	// (TDD-00163); the held-value type T lives in ElemType.
	hostFinalizationRegistry
	// hostPrimWrapper marks a primitive's wrapper object (`new Number(3)`):
	// a one-word body holding the boxed primitive, its host header word at
	// body - 8. HostTag names its class (Number, String, Boolean). See
	// emit_wrappers.go.
	hostPrimWrapper
)

// hostParent is the host a derived host also answers for.
var hostParent = map[hostKind]hostKind{
	hostBuffer:            hostTypedArray,
	hostSharedArrayBuffer: hostArrayBuffer,
	hostDynamicObject:     hostMap,
}

// isHost reports whether t's host is h or derives from it.
func (t Type) isHost(h hostKind) bool {
	for k := t.Host; k != hostNone; k = hostParent[k] {
		if k == h {
			return true
		}
	}
	return false
}

// setHost marks (on) or unmarks (off) t as an instance of h. Marking keeps
// a host already derived from h; unmarking a derived host leaves its base.
func (t *Type) setHost(h hostKind, on bool) {
	switch {
	case on && !t.isHost(h):
		t.Host = h
	case !on && t.Host == h:
		t.Host = hostParent[h]
	case !on && t.isHost(h):
		t.Host = hostNone
	}
}

func (t Type) IsPromise() bool              { return t.isHost(hostPromise) }
func (t Type) IsMap() bool                  { return t.isHost(hostMap) }
func (t Type) IsSet() bool                  { return t.isHost(hostSet) }
func (t Type) IsDynamicObject() bool        { return t.isHost(hostDynamicObject) }
func (t Type) IsDate() bool                 { return t.isHost(hostDate) }
func (t Type) IsGenerator() bool            { return t.isHost(hostGenerator) }
func (t Type) IsCollIter() bool             { return t.isHost(hostCollIter) }
func (t Type) IsTypedArray() bool           { return t.isHost(hostTypedArray) }
func (t Type) IsBuffer() bool               { return t.isHost(hostBuffer) }
func (t Type) IsSymbol() bool               { return t.isHost(hostSymbol) }
func (t Type) IsBigInt() bool               { return t.isHost(hostBigInt) }
func (t Type) IsArrayBuffer() bool          { return t.isHost(hostArrayBuffer) }
func (t Type) IsSharedArrayBuffer() bool    { return t.isHost(hostSharedArrayBuffer) }
func (t Type) IsError() bool                { return t.isHost(hostError) }
func (t Type) IsRegExp() bool               { return t.isHost(hostRegExp) }
func (t Type) IsResponse() bool             { return t.isHost(hostResponse) }
func (t Type) IsFetchRequest() bool         { return t.isHost(hostFetchRequest) }
func (t Type) IsXHR() bool                  { return t.isHost(hostXHR) }
func (t Type) IsReadableStream() bool       { return t.isHost(hostReadableStream) }
func (t Type) IsStreamReader() bool         { return t.isHost(hostStreamReader) }
func (t Type) IsRSController() bool         { return t.isHost(hostRSController) }
func (t Type) IsWritableStream() bool       { return t.isHost(hostWritableStream) }
func (t Type) IsStreamWriter() bool         { return t.isHost(hostStreamWriter) }
func (t Type) IsWSController() bool         { return t.isHost(hostWSController) }
func (t Type) IsTransformStream() bool      { return t.isHost(hostTransformStream) }
func (t Type) IsFinalizationRegistry() bool { return t.isHost(hostFinalizationRegistry) }
func (t Type) IsPrimWrapper() bool          { return t.isHost(hostPrimWrapper) }

// hostFlagOf names each host by its predicate.
var hostFlagOf = map[string]hostKind{
	"IsPromise":              hostPromise,
	"IsMap":                  hostMap,
	"IsSet":                  hostSet,
	"IsDynamicObject":        hostDynamicObject,
	"IsDate":                 hostDate,
	"IsGenerator":            hostGenerator,
	"IsBuffer":               hostBuffer,
	"IsTypedArray":           hostTypedArray,
	"IsSymbol":               hostSymbol,
	"IsBigInt":               hostBigInt,
	"IsArrayBuffer":          hostArrayBuffer,
	"IsSharedArrayBuffer":    hostSharedArrayBuffer,
	"IsError":                hostError,
	"IsRegExp":               hostRegExp,
	"IsResponse":             hostResponse,
	"IsReadableStream":       hostReadableStream,
	"IsCollIter":             hostCollIter,
	"IsStreamReader":         hostStreamReader,
	"IsFetchRequest":         hostFetchRequest,
	"IsTransformStream":      hostTransformStream,
	"IsStreamWriter":         hostStreamWriter,
	"IsWritableStream":       hostWritableStream,
	"IsRSController":         hostRSController,
	"IsWSController":         hostWSController,
	"IsXHR":                  hostXHR,
	"IsFinalizationRegistry": hostFinalizationRegistry,
	"IsPrimWrapper":          hostPrimWrapper,
}

// typeFlagOrder is every Is* predicate of Type in declaration order, host
// predicates interleaved where their flags stood: the order hostClassName
// and hasHandleFlag try them in.
var typeFlagOrder = []string{
	"IsArray",
	"IsObject",
	"IsTuple",
	"IsFunc",
	"IsPromiseResolver",
	"IsMap",
	"IsSet",
	"IsDynamicObject",
	"IsNull",
	"IsUndefined",
	"IsNever",
	"IsPromise",
	"IsDynamic",
	"IsStrLiteral",
	"IsDate",
	"IsResponse",
	"IsClass",
	"IsError",
	"IsCaught",
	"IsSymbol",
	"IsBigInt",
	"IsGenerator",
	"IsCollIter",
	"IsArrayBuffer",
	"IsSharedArrayBuffer",
	"IsTypedArray",
	"IsBuffer",
	"IsRegExp",
	"IsFinalizationRegistry",
	"IsFlatArray",
	"IsFetchRequest",
	"IsXHR",
	"IsReadableStream",
	"IsStreamReader",
	"IsRSController",
	"IsWritableStream",
	"IsStreamWriter",
	"IsWSController",
	"IsTransformStream",
	"IsPrimWrapper",
}

// typeFlag reports Type predicate name for t, a host predicate or a flag.
func typeFlag(t Type, name string) bool {
	if h, ok := hostFlagOf[name]; ok {
		return t.isHost(h)
	}
	if k, ok := kindFlagOf[name]; ok {
		return t.isKind(k)
	}
	return reflect.ValueOf(t).FieldByName(name).Bool()
}
