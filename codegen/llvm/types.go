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
	// IsTuple marks a tuple type `[T0, T1, ...]` (TDD-00066). Storage-wise it
	// IS an IsObject struct with synthetic positional field names "0","1",...
	// (so StructIR/StructSize/FieldIndex/GEP, object-literal construction,
	// object param/return/field passing, spread, and structuredClone all apply
	// unchanged). IsTuple only additionally: maps a constant index `t[i]` to
	// field "i", lets an array-destructuring pattern bind positionally, and
	// renders/serializes as an array literal rather than an object.
	IsTuple bool
	// Function/closure type: all closures are passed as ptr.
	IsFunc      bool
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
	// IsPromiseResolver marks the `resolve` closure `new Promise`'s executor
	// receives (emit_promise_new.go). It carries the closure's `{fnptr, env}`
	// shape like any callback, but a call site passing a Promise argument
	// (`resolve(anotherPromise)`) adopts that thenable — settling the target
	// when the argument settles — instead of coercing a promise to the value
	// type (TDD-00091). The target promise is the closure's env.
	IsPromiseResolver bool
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
	// Weak marks a WeakMap<K,V>/WeakSet<T> (IsMap/IsSet also set): keyed on
	// object-pointer identity, not iterable, mode-dependent backing (TDD-00112).
	// IsWeakRef marks a WeakRef<T> (a one-word referent box); MapKey holds the
	// referent type.
	Weak      bool
	IsWeakRef bool
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
	// IsNever marks the bottom type — the value type of a `Promise.reject(...)`
	// (`Promise<never>`), which never actually produces a value (await re-throws).
	// It assigns to any target: coerce turns it into a zero of the target type,
	// keeping the IR well-typed while it stays dead (TDD-00087 follow-up).
	IsNever  bool
	Nullable bool
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

	// PromiseTask marks a Promise<T> returned by a may-suspend async function
	// (TDD-00083 Stage 2): its slot is a { i64 resolved, ptr waiter, i64 v0, i64
	// v1 } pending-promise object driven by the task scheduler (runtime_task.go),
	// not a bare value slot. emitAwait uses this to wait via
	// @__kml_task_await_ready and then read v0/v1, rather than the plain
	// load-and-free path.
	PromiseTask bool
	// IsDynamic marks any/unknown: a runtime-tagged { i8, i64 } box (tag +
	// payload) instead of one fixed concrete storage type. See emit_dynamic.go.
	IsDynamic bool
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
	// IsStrLiteral marks a string-literal type (`"circle"`): string-shaped at
	// runtime (IR "ptr", isStringTy true), with the literal in LitValue. Used as
	// the discriminant of a discriminated union (TDD-00116); elsewhere it behaves
	// exactly as string.
	IsStrLiteral bool
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
	// IsRequest marks http.listen()'s request object (RequestType), spelled
	// `HttpRequest` in source (TDD-00040 renamed the annotation from
	// `Request` to free that name up for the real client-side Request class
	// — see IsFetchRequest below; the Go-side RequestType/IsRequest names are
	// unchanged, only the user-facing TypeScript annotation moved): an
	// ordinary heap object like Response/URL, plus this flag so method-call
	// dispatch can recognize a receiver of this type — needed for
	// `req.bodyBytes(): ArrayBuffer` (TDD-00026/ADR-00106), the one
	// dispatched method it has; every other property is a plain field read
	// via the existing object machinery, same as Response/URL.
	IsRequest bool
	// IsServerResponse marks Node's `http.createServer((req, res) => …)` `res`
	// object (TDD-00131): an ordinary heap object whose fields are exactly the
	// {status, body, headers} the existing http dispatcher reads, plus this flag
	// so method-call dispatch recognizes `res.writeHead`/`setHeader`/`write`/
	// `end` and the `res.statusCode =` assignment, translating them to mutations
	// of those fields. The dispatcher then reads the accumulated response off it
	// exactly as it reads a bespoke handler's returned object.
	IsServerResponse bool

	// IsIncomingMessage marks Node's http client response object (TDD-00138).
	IsIncomingMessage bool
	// IsSQLiteDatabase marks node:sqlite's `new DatabaseSync(...)` result
	// (ADR-00540): a heap object holding the raw `sqlite3*` handle (__kml_handle)
	// plus an `isOpen` bool field (a plain field read via the object machinery);
	// exec/prepare/close dispatch on this flag in emit_call.go. See emit_sqlite.go.
	IsSQLiteDatabase bool
	// IsSQLiteStatement marks a StatementSync returned by db.prepare() (ADR-00540):
	// a heap object holding the `sqlite3_stmt*` handle, a back-pointer to the
	// owning database (for changes()/last_insert_rowid()), and a `sourceSQL`
	// field. get/all/run dispatch on this flag.
	IsSQLiteStatement bool
	// IsURL marks `new URL(...)`'s result: an ordinary heap object (href,
	// protocol, host, hostname, port, pathname, search, hash, origin,
	// searchParams — all plain field reads via the existing object
	// machinery, no dispatched methods of its own) built by parsing through
	// libcurl's URL API. See emit_url.go.
	IsURL bool
	// IsURLPattern marks `new URLPattern(...)`'s result (TDD-00100): a heap
	// object whose six visible fields are the (defaulted) component pattern
	// strings, plus a hidden __kml_handle ptr to the C-side compiled state
	// (urlpatternsrc/urlpattern.c) that .test/.exec dispatch on. Same
	// flagged-ObjectType shape as URL/Response.
	IsURLPattern bool
	// IsSymbol marks Symbol(...)'s result (TDD-00044 V1): an ordinary
	// 1-field heap object ({description: string}, description read via the
	// existing object field-access machinery, no dynamic property keys or
	// well-known symbols). Uniqueness/=== come for free from pointer
	// identity — no id/counter field needed. IsSymbol exists so the handful
	// of dispatch sites that would otherwise mishandle it via their generic
	// IsObject path (JSON.stringify, structuredClone, typeof, binary
	// operators, console.log/template-literal formatting) can special-case
	// it instead. See emit_symbol.go.
	IsSymbol bool
	// IsBigInt marks a `bigint` value (TDD-00074): an opaque `ptr` handle to a
	// heap-allocated arbitrary-precision integer owned by the selected backend
	// library (libtommath/gmp/…), reached only through the __kml_bigint_* ABI —
	// never field-accessed, so deliberately NOT IsObject. The flag exists so the
	// dispatch sites that must treat it specially (operators, typeof,
	// stringification, JSON) branch on it instead of the generic ptr path. See
	// emit_bigint.go.
	IsBigInt bool
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
	// IsGenerator marks a `function* name(): T {}`'s instance value (the
	// result of calling the generator function, TDD-00061/ADR-00172) — a
	// ptr to a heap struct carrying its own fiber (ucontext_t + stack),
	// yield/sent value slots, and its own declared parameters. Deliberately
	// does NOT set IsObject, same reasoning IsEventEmitter's own doc
	// comment gives: the struct's fields are internal plumbing (accessed by
	// codegen via GeneratorType's own Fields, built with ObjectType purely
	// to reuse its StructIR/StructSize/FieldIndex machinery), not a
	// user-facing object shape — a generator's only real surface is
	// `.next()`. GeneratorElemType is the T in `function* f(): T` — every
	// yield/`.next()` call site needs it to know the yielded/sent value's
	// own IR shape.
	IsGenerator       bool
	GeneratorIsAsync  bool // an `async function*` — .next() returns Promise<{value,done}> (TDD-00085)
	GeneratorElemType *Type
	// HasEventEmitter marks a class whose instances carry a hidden
	// listener-map-handle field (TDD-00023) — set for a class that directly
	// `extends EventEmitter<T>`, and propagated to every descendant the same
	// way HasVTable propagates across an inheritance tree. See
	// ClassEventEmitterField and registerClasses.
	HasEventEmitter bool

	// HasNodeStream marks a class that `extends Readable/Writable/Duplex/
	// Transform` (TDD-00132) — its instances carry a hidden Node-stream
	// handle field (ClassNodeStreamField), positioned right after the tag
	// (and vtable pointer, if present). The readable-vs-writable split and
	// the chunk/out element types live on the class's ClassInfo, not here —
	// this flag only governs the hidden field's presence and VisibleFields'
	// skip count.
	HasNodeStream bool
	// IsArrayBuffer marks `new ArrayBuffer(byteLength)`: a fixed-length,
	// zero-initialized raw byte buffer. Deliberately not IsObject — the
	// runtime value is a ptr to a hidden 2-word heap struct ({i64
	// byteLength, ptr data}), never exposed via the generic FieldIndex/
	// Object.keys/JSON reflection path (same reasoning Map/Set's own hidden
	// layout already uses). `.byteLength` gets its own dedicated property
	// read in emitMember, the same pattern `.size` already uses for
	// Map/Set. See emit_arraybuffer.go and docs/tdd/TDD-00018.md.
	IsArrayBuffer bool
	// IsSharedArrayBuffer marks `new SharedArrayBuffer(byteLength)`
	// (TDD-00099). Always set together with IsArrayBuffer — layout,
	// `.byteLength`, TypedArray views, and DataView all work through the
	// IsArrayBuffer paths unchanged; this flag only changes the worker
	// boundary (shared by reference, never deep-copied) and, under -mm=gc,
	// the allocation (GC_malloc_uncollectable, since the only live
	// reference may be on another thread).
	IsSharedArrayBuffer bool
	// IsStats marks fs.statSync's result (ADR-00495): a plain heap object
	// whose hidden first field is the raw st_mode word, backing the
	// isFile()/isDirectory() method dispatch.
	IsStats bool
	// BufferGrowable marks a buffer constructed with `{maxByteLength}`
	// (ADR-00494): its header is the 24-byte {len, data, max} shape, so
	// `.growable`/`.maxByteLength`/`.grow()` may read word 2. Buffers from
	// every other producer keep the 16-byte header and must never read it.
	BufferGrowable bool
	// IsDataView marks `new DataView(buffer, byteOffset?, byteLength?)`: an
	// arbitrary-endian read/write view over an ArrayBuffer sub-range. Same
	// hidden-heap-struct convention as IsArrayBuffer — a ptr to
	// {ptr data(base+offset), i64 byteLength, i64 byteOffset, ptr bufHdr},
	// with dedicated property/method dispatch. See emit_dataview.go.
	IsDataView bool
	// IsBlob marks `new Blob(parts?, {type}?)` (TDD-00102): an immutable
	// binary value with a MIME type. Same hidden-heap-struct convention as
	// IsArrayBuffer — a ptr to { i64 size, ptr data, ptr type }, with
	// dedicated .size/.type property reads and .slice()/.arrayBuffer()/
	// .bytes()/.text() method dispatch. See emit_blob.go.
	IsBlob bool
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
	// IsCryptoKey marks a Web Crypto CryptoKey (TDD-00104): a ptr to a
	// hidden { i64 algId, i64 hashId, i64 usages, i64 extractable,
	// i64 kind, ptr keyData, i64 keyLen } heap struct — same hidden-layout
	// convention as IsArrayBuffer/IsBlob, with dedicated .type/
	// .extractable property reads. keyData is raw bytes for symmetric
	// keys, DER (PKCS#8/SPKI) for asymmetric. See emit_call_crypto.go.
	IsCryptoKey bool
	// IsCryptoKeyPair marks generateKey's RSA/EC result (TDD-00104): a ptr
	// to { ptr publicKey, ptr privateKey }, with dedicated .publicKey/
	// .privateKey property reads yielding IsCryptoKey values.
	IsCryptoKeyPair bool
	// IsBuffer marks a Node Buffer (TDD-00103): storage-wise it IS a
	// Uint8Array (IsTypedArray/IsArray/ElemType u8), so the whole array/
	// TypedArray surface comes free; the flag only additionally enables
	// Buffer's instance-method dispatch (.toString/.write/.copy/.equals/
	// .compare and the read*/write* accessors) at emitCall. See
	// emit_buffer.go.
	IsBuffer bool
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
	// NaN → 0 — instead of e.coerce's mod-2^width trunc.
	Clamped bool
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
	// IsEventSource marks `new EventSource(url)`'s result (TDD-00038,
	// staged — Stage 0 only so far: connection plumbing/readyState/close,
	// no SSE parsing/dispatch yet). Storage-wise a real heap object like
	// Response/URL/RegExp, with a hidden field (EventSourceHandleField, same
	// convention RegExpType's hidden handle uses) pointing at the runtime's
	// own entry struct — the two-way link the event loop's per-iteration
	// scan (__kml_eventsource_scan, runtime_eventsource.go) needs to write
	// readyState transitions back into this object. See emit_eventsource.go.
	IsEventSource bool
	// IsEvent marks a WHATWG Event/CustomEvent object (TDD-00081): a plain
	// fixed-shape object (type/detail/defaultPrevented fields) plus the
	// preventDefault/stop* method surface. CustomEvent carries an extra `detail`
	// field whose type varies, so the object type itself is the source of truth
	// for its fields; this flag only enables method dispatch.
	IsEvent bool
	// IsEventTarget marks a WHATWG EventTarget (TDD-00081 Stage 2): under the
	// hood a `Map<string, listener-list>` pointer (the same registry EventEmitter
	// uses), reached only through addEventListener/removeEventListener/
	// dispatchEvent.
	IsEventTarget bool
	// IsAbortSignal / IsAbortController mark the WHATWG cancellation token
	// (TDD-00081 Stage 3). An AbortSignal is an object with `aborted`/`reason`
	// fields plus a hidden `listeners` map (so it behaves as an EventTarget that
	// fires "abort"); an AbortController wraps one in its `signal` field.
	IsAbortSignal     bool
	IsAbortController bool
	// IsWSConnection marks the object passed to an `http.listen(port,
	// handler, { ws })` upgrade handler (TDD-00039 Stage 1) — a real heap
	// object like EventSource, with a hidden field (WSConnFdField) holding
	// the raw socket fd `.send()`/`.close()` write to directly, independent
	// of the connection-fiber array's own bookkeeping (see
	// emit_websocket.go). Never constructed via a `new` expression — the
	// compiler builds one internally right after a successful upgrade
	// handshake and passes it to the user's `ws` callback exactly once,
	// mirroring how a Request object is built and passed to the ordinary
	// HTTP handler.
	IsWSConnection bool
	// IsWebSocketClient marks `new WebSocket(url)`'s result (TDD-00039
	// Stage 3, `ws://` only — `wss://` is rejected at construction) — a
	// real heap object like EventSource/WSConnection, with a hidden field
	// (WebSocketClientHandleField) pointing at the runtime's own client
	// entry struct in the new client-scan array (mirroring EventSource's
	// own "fourth scanned resource" pattern, this one a fifth) the event
	// loop walks each iteration to deliver `.onmessage`/`.onclose`/
	// `.onerror`. See emit_websocket_client.go.
	IsWebSocketClient bool
	// IsWorker marks `new Worker(path)`'s result (TDD-00098): a ptr to the
	// runtime control block (runtime_worker.go's workerCtrlIR). WorkerPath
	// is the worker module's canonical file path — the key into
	// e.workerEntries that carries the statically-declared channel types,
	// which is how each parent-side postMessage/on site is type-checked.
	IsWorker   bool
	WorkerPath string
	// child_process handles: IsChildProcess marks a spawn()/exec()/execFile()
	// ChildProcess value (a ptr to runtime_childprocess.go's cpStructIR);
	// IsCPStream marks child.stdout/child.stderr (CPWhich 0/1 picks the
	// listener slots); IsCPStdin marks child.stdin (.write/.end).
	IsChildProcess bool
	IsCPStream     bool
	IsCPStdin      bool
	CPWhich        int
	// IsReadline marks a readline.createInterface() handle (a ptr to
	// runtime_readline.go's rlStructIR), for .on/.question/.close dispatch.
	IsReadline bool
	// IsStdin marks the process.stdin streaming handle (a ptr to
	// runtime_stdin.go's stdinStructIR), for .on('data'|'end') dispatch.
	IsStdin bool
	// IsNetServer marks a net.createServer() handle and IsNetSocket a TCP
	// connection socket (both ptr to runtime_net.go structs), for .listen/
	// .on/.write/.end/.close dispatch.
	IsNetServer bool
	// IsHTTPServer marks a variable-bound http.createServer() handle
	// (.listen/.close/.closeAllConnections/.address).
	IsHTTPServer bool
	// IsClientRequest marks the http.get/request return handle (ADR-00430):
	// a { ptr url, ptr cb, i64 state } struct for .end()/.abort()/.on().
	IsClientRequest bool
	// IsHTTPAgent marks the inert `new http.Agent(...)` token (ADR-00432).
	IsHTTPAgent bool
	// IsEmbeddedAssets marks an `embedDir(...)` handle (TDD-00142 Stage 7).
	IsEmbeddedAssets bool
	// IsWebview marks a `new Webview(...)` handle (TDD-00142): a calloc'd
	// `{ ptr webview_t, ptr boundListHead }` struct dispatching the window
	// methods (navigate/html/setTitle/setSize/init/eval/run/terminate/
	// destroy/bind/unbind).
	IsWebview bool
	// IsH2ServerStream marks the http2 server-side stream object
	// (.respond/.end/.write/.on('data'|'end'), TDD-00139 Stage 2).
	IsH2ServerStream bool
	// IsH2ClientSession / IsH2ClientStream mark the http2 client surface
	// (http2.connect / session.request, TDD-00139 Stage 3).
	IsH2ClientSession bool
	IsH2ClientStream  bool
	// IsH2Constants marks a binding of the compile-time http2.constants
	// namespace (TDD-00139 Stage 4).
	IsH2Constants bool
	// IsTestContext marks the node:test runner's `t` (TDD-00140).
	IsTestContext bool
	// IsDCChannel marks a diagnostics_channel Channel handle.
	IsDCChannel bool
	IsNetSocket bool
	// IsDgramSocket marks a dgram.createSocket() handle (a ptr to
	// runtime_dgram.go's dgramSocketIR), for .bind/.on('message')/.send/.close.
	IsDgramSocket bool
	// IsClusterWorker marks a cluster.fork() Worker handle (a ptr to
	// runtime_cluster.go's clusterWorkerIR { id, pid }), for .id access.
	IsClusterWorker bool
	// IsBroadcastChannel / IsMessageChannel / IsMessagePort (TDD-00099):
	// each is a ptr to a runtime channel-endpoint block (runtime_chan.go's
	// chanEpIR; a MessageChannel value is the port1 half, port1/port2
	// resolved by member access). BCName is the BroadcastChannel's
	// compile-time name — the key into e.bcChannels' per-name message type.
	// A MessagePort's message type lives in ElemType (like Set<T>).
	IsBroadcastChannel bool
	IsMessageChannel   bool
	IsMessagePort      bool
	BCName             string
	// IsChannel marks a klain:sync `new Channel<T>(cap)` handle (TDD-00143):
	// a ptr to the C runtime's hchan. The element type T lives in ElemType
	// (like MessagePort/Set<T>); channel elements are a fixed 8-byte slot, so
	// send/receive bitcast T through i64.
	IsChannel bool
	// Inferred marks a parameter type that defaulted to TypeI64 because no
	// explicit annotation was given, as opposed to a real `number`/`int32`/
	// etc. annotation that happens to also resolve to i64. Call sites use
	// this to reject a non-numeric argument against an unannotated
	// parameter at compile time, instead of silently bit-reinterpreting it
	// as an i64 (see docs/adr/ADR-00042.md).
	Inferred bool
	// IsHeaders marks `new Headers(...)`'s result (TDD-00040): storage-wise
	// it IS a real Map<string,string> (IsMap also set, MapKey/MapVal both
	// TypePtr) — exactly IsURLSearchParams's own precedent. get/set/has/
	// delete/forEach/entries/keys/values all come for free from the existing
	// Map machinery; IsHeaders only additionally enables case-insensitive
	// (lowercased-key) get/set/has/delete and the one genuinely new method,
	// append(), all dispatched in emit_call.go ahead of the generic IsMap
	// branch. See emit_headers.go.
	IsHeaders bool
	// IsFetchRequest marks `new Request(...)`'s result (TDD-00040): an
	// ordinary heap object (url/method/headers/body, all plain field reads)
	// like Response/URL. Named to avoid colliding with the pre-existing,
	// unrelated IsRequest/RequestType (http.listen's server-side `req`
	// object) — both are legitimately called "Request" in user-facing text,
	// but are otherwise unconnected types. See emit_fetch_request.go.
	IsFetchRequest bool
	// IsXHR marks `new XMLHttpRequest()`'s result (TDD-00040): a real heap
	// object like EventSource/WebSocketClient, with hidden fields (method/
	// url/headers, built up by open()/setRequestHeader()) plus visible
	// readyState/status/responseText/response and three zero-argument
	// callback fields (onreadystatechange/onload/onerror — deliberately
	// zero-arg, unlike EventSource/WebSocket's payload-carrying onmessage,
	// since a self-referential FuncType field isn't representable here; see
	// the TDD's Design section). send() is synchronous-looking but reuses
	// fetch()'s own non-blocking __kml_fetch_async primitive underneath, so
	// it still yields the current fiber rather than blocking when called
	// from inside an http.listen connection handler. See emit_xhr.go.
	IsXHR bool
	// IsReadableStream marks `new ReadableStream<T>(...)`'s result (TDD-00097
	// Stage 1): a ptr to a hidden 18-field heap struct (%kml.rstream in
	// runtime_streams.go — state machine, chunk queue with high-water mark,
	// pending-read promise FIFO, underlying-source closures, a per-site
	// "fulfill thunk" that builds typed {value,done} records). Deliberately
	// NOT IsObject, same opaque-handle reasoning IsEventEmitter's doc comment
	// gives. StreamChunk is the T — every enqueue/read site needs it to know
	// the chunk's IR shape (array-shaped chunks span the two queue words).
	// The reader, controller, and stream are all the SAME runtime pointer,
	// distinguished only by these compile-time flags — getReader() returns
	// its receiver retyped, and a controller value is the stream itself.
	IsReadableStream bool
	// IsStreamReader marks getReader()'s/values()' result — the same stream
	// pointer retyped so read()/releaseLock()/cancel()/.closed dispatch.
	IsStreamReader bool
	// IsRSController marks the controller passed to the underlying source's
	// start/pull callbacks — the stream pointer retyped so enqueue()/close()/
	// error()/.desiredSize dispatch.
	IsRSController bool
	// IsWritableStream / IsStreamWriter / IsWSController are the writable
	// side's mirror trio (TDD-00097 Stage 2): one %kml.wstream pointer
	// (runtime_streams_writable.go), retyped at compile time for the stream,
	// getWriter()'s writer, and the sink callbacks' controller.
	IsWritableStream bool
	IsStreamWriter   bool
	IsWSController   bool
	// IsTransformStream marks `new TransformStream<I, O>(...)` (TDD-00097
	// Stage 3): a ptr to the runtime ts context whose first two fields are
	// the readable (chunk O) and writable (chunk I) stream pointers.
	// StreamChunk carries I, StreamOut carries O.
	IsTransformStream bool
	// IsNodeReadable / IsNodeWritable mark Node's `stream` classes (TDD-00097
	// Stage 8): a ptr to the %kml.nodestream wrapper over the WHATWG
	// internals. A Transform sets both. StreamChunk carries the writable-in
	// type, StreamOut the readable-out type.
	IsNodeReadable bool
	IsNodeWritable bool
	StreamChunk    *Type
	StreamOut      *Type
}

// ArrayOf returns an array type whose elements are of the given type.
func ArrayOf(elem Type) Type {
	return Type{IR: "ptr", IsArray: true, ElemType: &elem}
}

// ObjectType returns an object type with the given fields.
func ObjectType(fields []Field) Type {
	return Type{IR: "ptr", IsObject: true, Fields: fields}
}

// TupleType returns a tuple type `[T0, T1, ...]` (TDD-00066): an object struct
// with synthetic positional field names "0","1",..., flagged IsTuple.
func TupleType(elems []Type) Type {
	fields := make([]Field, len(elems))
	for i, ety := range elems {
		fields[i] = Field{Name: fmt.Sprintf("%d", i), Ty: ety}
	}
	ty := ObjectType(fields)
	ty.IsTuple = true
	return ty
}

// MapType returns a Map<key,val> type.
func MapType(key, val Type) Type {
	return Type{IR: "ptr", IsMap: true, MapKey: &key, MapVal: &val}
}

// SetType returns a Set<elem> type.
func SetType(elem Type) Type {
	return Type{IR: "ptr", IsSet: true, MapKey: &elem}
}

// WeakMapType returns a WeakMap<key,val> type (TDD-00112) — an object-identity-
// keyed, non-iterable map with mode-dependent backing.
func WeakMapType(key, val Type) Type {
	return Type{IR: "ptr", IsMap: true, Weak: true, MapKey: &key, MapVal: &val}
}

// WeakSetType returns a WeakSet<elem> type (TDD-00112).
func WeakSetType(elem Type) Type {
	return Type{IR: "ptr", IsSet: true, Weak: true, MapKey: &elem}
}

// WeakRefType returns a WeakRef<referent> type (TDD-00112) — a one-word box
// whose referent may be collected under -mm=gc (MapKey holds the referent type).
func WeakRefType(referent Type) Type {
	return Type{IR: "ptr", IsWeakRef: true, MapKey: &referent}
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

// ReadableStreamType returns a ReadableStream<chunk> type (TDD-00097).
func ReadableStreamType(chunk Type) Type {
	c := chunk
	return Type{IR: "ptr", IsReadableStream: true, StreamChunk: &c}
}

// StreamReaderType returns the reader type getReader() yields.
func StreamReaderType(chunk Type) Type {
	c := chunk
	return Type{IR: "ptr", IsStreamReader: true, StreamChunk: &c}
}

// RSControllerType returns the controller type start/pull callbacks receive.
func RSControllerType(chunk Type) Type {
	c := chunk
	return Type{IR: "ptr", IsRSController: true, StreamChunk: &c}
}

// WritableStreamType / WSWriterType / WSControllerType are the writable-side
// mirrors (TDD-00097 Stage 2).
func WritableStreamType(chunk Type) Type {
	c := chunk
	return Type{IR: "ptr", IsWritableStream: true, StreamChunk: &c}
}

func WSWriterType(chunk Type) Type {
	c := chunk
	return Type{IR: "ptr", IsStreamWriter: true, StreamChunk: &c}
}

func WSControllerType(chunk Type) Type {
	c := chunk
	return Type{IR: "ptr", IsWSController: true, StreamChunk: &c}
}

// NodeReadableType / NodeWritableType / NodeTransformType (TDD-00097 St. 8).
func NodeReadableType(out Type) Type {
	o := out
	return Type{IR: "ptr", IsNodeReadable: true, StreamOut: &o}
}

func NodeWritableType(in Type) Type {
	i := in
	return Type{IR: "ptr", IsNodeWritable: true, StreamChunk: &i}
}

func NodeTransformType(in, out Type) Type {
	i, o := in, out
	return Type{IR: "ptr", IsNodeReadable: true, IsNodeWritable: true, StreamChunk: &i, StreamOut: &o}
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
		{Name: "status", Ty: TypeF64}, // a `number` (TDD-00123)
		{Name: "ok", Ty: TypeBool},
		{Name: "body", Ty: TypePtr},
		{Name: "bodyLength", Ty: TypeI64},
		// The pending-fetch handle backing lazy body access (TDD-00097
		// Stage 4): non-null on a headers-resolved Response, so
		// .text()/.json()/.arrayBuffer() drive the rest of the transfer and
		// .body can stream it. Null on combinator-built Responses.
		{Name: "__kml_pending", Ty: TypePtr},
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
		{Name: "username", Ty: TypePtr},
		{Name: "password", Ty: TypePtr},
		{Name: "searchParams", Ty: URLSearchParamsType()},
	})
	ty.IsURL = true
	return ty
}

// SQLiteDatabaseType returns node:sqlite's `new DatabaseSync(...)` result type
// (ADR-00540): the raw sqlite3* handle in a hidden __kml_handle field plus a
// plain `isOpen` bool read through the ordinary object machinery. exec/prepare/
// close dispatch on IsSQLiteDatabase in emit_call.go.
func SQLiteDatabaseType() Type {
	ty := ObjectType([]Field{
		{Name: "__kml_handle", Ty: TypePtr},
		{Name: "isOpen", Ty: TypeBool},
		{Name: "__kml_path", Ty: TypePtr},
		{Name: "__kml_flags", Ty: TypeI32},
	})
	ty.IsSQLiteDatabase = true
	return ty
}

// SQLiteStatementType returns a StatementSync's type (ADR-00540): the
// sqlite3_stmt* handle, a back-pointer to the owning database's sqlite3* (for
// changes()/last_insert_rowid() after run()), and the source SQL string as a
// plain field. get/all/run dispatch on IsSQLiteStatement.
func SQLiteStatementType() Type {
	ty := ObjectType([]Field{
		{Name: "__kml_handle", Ty: TypePtr},
		{Name: "__kml_db", Ty: TypePtr},
		{Name: "sourceSQL", Ty: TypePtr},
	})
	ty.IsSQLiteStatement = true
	return ty
}

// SQLiteColumnMetaType is one entry of stmt.columns() (ADR-00540): the
// column/table/database origin plus declared type and result name. Absent
// origins (an expression column) read back as null, matching node:sqlite.
func SQLiteColumnMetaType() Type {
	return ObjectType([]Field{
		{Name: "column", Ty: nullablePtr()},
		{Name: "database", Ty: nullablePtr()},
		{Name: "table", Ty: nullablePtr()},
		{Name: "type", Ty: nullablePtr()},
		{Name: "name", Ty: TypePtr},
	})
}

func nullablePtr() Type {
	t := TypePtr
	t.Nullable = true
	return t
}

// SQLiteRunResultType is the `{ changes, lastInsertRowid }` object stmt.run()
// returns (ADR-00540). Both are `number` (TDD-00123 f64); real Node returns
// bigint-or-number, narrowed to number for V1 with the >2^53 caveat documented.
func SQLiteRunResultType() Type {
	return ObjectType([]Field{
		{Name: "changes", Ty: TypeF64},
		{Name: "lastInsertRowid", Ty: TypeF64},
	})
}

// URLPatternType returns `new URLPattern(...)`'s result type (TDD-00100): the
// six component pattern strings as readable fields (matching the spec's
// readonly component accessors — an omitted init component reads back as its
// "*" default), plus the hidden __kml_handle carrying the compiled per-
// component PCRE2 state, same hidden-field trick as Response.__kml_pending.
func URLPatternType() Type {
	ty := ObjectType([]Field{
		{Name: "protocol", Ty: TypePtr},
		{Name: "hostname", Ty: TypePtr},
		{Name: "port", Ty: TypePtr},
		{Name: "pathname", Ty: TypePtr},
		{Name: "search", Ty: TypePtr},
		{Name: "hash", Ty: TypePtr},
		{Name: "username", Ty: TypePtr},
		{Name: "password", Ty: TypePtr},
		{Name: "__kml_handle", Ty: TypePtr},
	})
	ty.IsURLPattern = true
	return ty
}

// SymbolType returns Symbol(...)'s result type (TDD-00044 V1) — a plain
// 1-field heap object, same "flag a generic ObjectType" shape URLType uses
// above. Uniqueness and === come from the struct's own pointer identity, not
// from anything stored in the struct — see IsSymbol's doc comment.
func SymbolType() Type {
	ty := ObjectType([]Field{{Name: "description", Ty: TypePtr}})
	ty.IsSymbol = true
	return ty
}

// BigIntType returns a `bigint` value's type (TDD-00074): an opaque `ptr` handle
// to a backend-owned arbitrary-precision integer. Unlike SymbolType this is NOT
// an IsObject struct — nothing reads fields off it; every operation goes through
// the __kml_bigint_* ABI in emit_bigint.go.
func BigIntType() Type {
	return Type{IR: "ptr", IsBigInt: true}
}

// HeadersType returns `new Headers(...)`'s result type (TDD-00040) — see
// IsHeaders's doc comment for why this is just a flagged Map<string,string>.
func HeadersType() Type {
	ty := MapType(TypePtr, TypePtr)
	ty.IsHeaders = true
	return ty
}

// FetchRequestType returns `new Request(...)`'s result type (TDD-00040): a
// plain heap object like Response/URL — url/method/headers/body are all
// plain field reads via the existing object machinery, no dispatched
// methods of its own. See IsFetchRequest's doc comment for the naming
// choice (avoiding the pre-existing, unrelated IsRequest/RequestType) and
// emit_fetch_request.go.
func FetchRequestType() Type {
	ty := ObjectType([]Field{
		{Name: "url", Ty: TypePtr},
		{Name: "method", Ty: TypePtr},
		{Name: "headers", Ty: HeadersType()},
		{Name: "body", Ty: TypePtr},
	})
	ty.IsFetchRequest = true
	return ty
}

// XHRMethodField/XHRURLField/XHRHeadersField are the names of the hidden
// fields every `new XMLHttpRequest()` instance carries at indices 0-2 —
// written by open()/setRequestHeader(), read by send() — never exposed via
// VisibleFields()/Object.keys/JSON, the same convention
// EventSourceHandleField/WSConnFdField already use.
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
// meaning for a send() that runs to completion before returning, matching
// EventSourceType's own "simplified state model" precedent).
// onreadystatechange/onload/onerror are zero-argument callback fields (see
// IsXHR's doc comment for why, unlike EventSource/WebSocket's
// payload-carrying onmessage) — assigning to any of them is a plain
// FuncType field assignment needing no dedicated codegen, same as
// EventSource/WSConnection's own onmessage.
func XMLHttpRequestType() Type {
	cb := FuncType(nil, TypeVoid)
	ty := ObjectType([]Field{
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
	ty.IsXHR = true
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

// Generator instance struct field names (TDD-00061/ADR-00172) — a fixed
// prologue shared by every generator, followed by one field per the
// generator function's own declared parameter (named "__param0",
// "__param1", ... — same synthetic-name convention a destructured function
// parameter's own pattern fields already use, see ast.Param's doc comment).
const (
	GeneratorCtxField        = "__ctx"        // this generator's own ucontext_t (ptr, malloc'd ucontextLayout()-sized)
	GeneratorStackField      = "__stack"      // this generator's own fiber stack (ptr, malloc'd fiberStackBytes)
	GeneratorCallerCtxField  = "__callerCtx"  // where to swapcontext back to on yield/return — reset before every .next() call
	GeneratorStartedField    = "__started"    // false until the first .next() call
	GeneratorDoneField       = "__done"       // true once the body has returned or fallen off the end
	GeneratorYieldedField    = "__yielded"    // what the most recent yield/return produced
	GeneratorSentField       = "__sent"       // what the current .next(value)/.return(value) call is passing in
	GeneratorResumeModeField = "__resumeMode" // how the current resume behaves: 0 next (return __sent), 1 throw __thrown, 2 return __sent (TDD-00086)
	GeneratorThrownField     = "__thrown"     // the error object a .throw(e) injects at the suspension point (ptr, TDD-00086)
	GeneratorJmpStkField     = "__jmpStk"     // this generator's own isolated jmpbuf stack (ptr, so caller/body try-frames never interleave — TDD-00086)
	GeneratorJmpTopField     = "__jmpTop"     // the generator's jmpbuf-stack top saved across suspension (i64, TDD-00086)
	GeneratorGenErrorField   = "__genError"   // an uncaught body throw the outer catch-all captured, re-thrown on the caller side (ptr, TDD-00086)
	GeneratorThisField       = "__this"       // a generator method's receiver (ptr), bound to `this` at body entry (TDD-00063 Stage 2b)
	GeneratorEnvField        = "__env"        // a nested generator's closure environment (ptr to the captured-cell struct, null when it captures nothing) — TDD-00094

	// Async-generator step state (the spec-faithful step model: synchronous
	// body start, park-at-await, request queueing). Present on every
	// generator's layout for uniformity; sync generators never touch them.
	GeneratorPendingQField = "__pendingQ" // the in-flight step's result promise (ptr; null = no step running)
	GeneratorParkedField   = "__parked"   // i64 1 while the fiber is suspended at an await-park (vs a yield/completion)
	GeneratorReqHeadField  = "__reqHead"  // queued .next/.throw/.return requests while a step is in flight (ptr to {i64 mode, ptr sentSlot, ptr thrown, ptr q, ptr next})
	GeneratorReqTailField  = "__reqTail"  // tail of the request FIFO (ptr)
)

// GeneratorType returns a `function* f(params): T {}`'s instance value type
// — see IsGenerator's own doc comment for why this reuses ObjectType purely
// for its struct-layout machinery rather than actually being IsObject.
// paramTypes is the generator function's own declared parameter types, in
// order, stored as trailing fields so construction can populate them once
// and the generator body can read them back on its own fiber stack.
func GeneratorType(elem Type, paramTypes []Type, thisTy *Type, isAsync bool) Type {
	elemCopy := elem
	fields := []Field{
		{Name: GeneratorCtxField, Ty: TypePtr},
		{Name: GeneratorStackField, Ty: TypePtr},
		{Name: GeneratorCallerCtxField, Ty: TypePtr},
		{Name: GeneratorStartedField, Ty: TypeBool},
		{Name: GeneratorDoneField, Ty: TypeBool},
		{Name: GeneratorYieldedField, Ty: elem},
		{Name: GeneratorSentField, Ty: elem},
		{Name: GeneratorResumeModeField, Ty: TypeI64},
		{Name: GeneratorThrownField, Ty: TypePtr},
		{Name: GeneratorJmpStkField, Ty: TypePtr},
		{Name: GeneratorJmpTopField, Ty: TypeI64},
		{Name: GeneratorGenErrorField, Ty: TypePtr},
		{Name: GeneratorEnvField, Ty: TypePtr},
		{Name: GeneratorPendingQField, Ty: TypePtr},
		{Name: GeneratorParkedField, Ty: TypeI64},
		{Name: GeneratorReqHeadField, Ty: TypePtr},
		{Name: GeneratorReqTailField, Ty: TypePtr},
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
	ty.IsObject = false
	ty.IsGenerator = true
	ty.GeneratorIsAsync = isAsync
	ty.GeneratorElemType = &elemCopy
	return ty
}

// ArrayBufferType returns `new ArrayBuffer(...)`'s result type — see
// IsArrayBuffer's doc comment for the hidden-struct representation.
func ArrayBufferType() Type {
	return Type{IR: "ptr", IsArrayBuffer: true}
}

// CryptoKeyType returns a Web Crypto CryptoKey's type (TDD-00104).
func CryptoKeyType() Type {
	return Type{IR: "ptr", IsCryptoKey: true}
}

// CryptoKeyPairType returns generateKey's {publicKey, privateKey} result
// type for asymmetric algorithms (TDD-00104).
func CryptoKeyPairType() Type {
	return Type{IR: "ptr", IsCryptoKeyPair: true}
}

// BroadcastChannelType returns `new BroadcastChannel(name)`'s result type
// (TDD-00099).
func BroadcastChannelType(name string) Type {
	return Type{IR: "ptr", IsBroadcastChannel: true, BCName: name}
}

// MessagePortType returns a MessagePort<msg> type (TDD-00099).
func MessagePortType(msg Type) Type {
	m := msg
	return Type{IR: "ptr", IsMessagePort: true, ElemType: &m}
}

// MessageChannelType returns `new MessageChannel<msg>()`'s result type
// (TDD-00099).
func MessageChannelType(msg Type) Type {
	m := msg
	return Type{IR: "ptr", IsMessageChannel: true, ElemType: &m}
}

// ChannelType returns `new Channel<T>(cap)`'s result type (TDD-00143); the
// element type T lives in ElemType.
func ChannelType(elem Type) Type {
	m := elem
	return Type{IR: "ptr", IsChannel: true, ElemType: &m}
}

// SharedArrayBufferType returns `new SharedArrayBuffer(...)`'s result type —
// the ArrayBuffer representation plus the shared-across-workers flag.
// StatsType returns fs.statSync's result type (ADR-00495/ADR-00565): the full
// Stats numeric surface, in Node's own-property order (dev, mode, nlink, uid,
// gid, rdev, blksize, ino, size, blocks, atimeMs, mtimeMs, ctimeMs,
// birthtimeMs). `mode` doubles as the isFile()/isDirectory()/isSymbolicLink()
// backing (masked with S_IFMT). All fields are integer milliseconds/counts —
// the Date-valued `atime`/`mtime`/`ctime`/`birthtime` accessors are a disclosed
// gap. birthtimeMs is 0 on Linux (no birthtime in struct stat without statx).
func StatsType() Type {
	ty := ObjectType([]Field{
		{Name: "dev", Ty: TypeI64},
		{Name: "mode", Ty: TypeI64},
		{Name: "nlink", Ty: TypeI64},
		{Name: "uid", Ty: TypeI64},
		{Name: "gid", Ty: TypeI64},
		{Name: "rdev", Ty: TypeI64},
		{Name: "blksize", Ty: TypeI64},
		{Name: "ino", Ty: TypeI64},
		{Name: "size", Ty: TypeI64},
		{Name: "blocks", Ty: TypeI64},
		{Name: "atimeMs", Ty: TypeI64},
		{Name: "mtimeMs", Ty: TypeI64},
		{Name: "ctimeMs", Ty: TypeI64},
		{Name: "birthtimeMs", Ty: TypeI64},
	})
	ty.IsStats = true
	return ty
}

// statFieldOrder is the field order StatsType/__kml_fs_stat share, used to fill
// the object from the runtime's 14-i64 result.
var statFieldOrder = []string{"dev", "mode", "nlink", "uid", "gid", "rdev", "blksize", "ino", "size", "blocks", "atimeMs", "mtimeMs", "ctimeMs", "birthtimeMs"}

func SharedArrayBufferType() Type {
	return Type{IR: "ptr", IsArrayBuffer: true, IsSharedArrayBuffer: true}
}

// DataViewType returns `new DataView(...)`'s result type — see IsDataView's
// doc comment for the hidden-struct representation.
func DataViewType() Type {
	return Type{IR: "ptr", IsDataView: true}
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

// EventSourceHandleField is the name of the hidden ptr field every
// EventSource instance carries at index 0, pointing at the runtime's own
// entry struct (`{ ptr pending, ptr instance, i64 consumedOffset, i64
// state }`, runtime_eventsource.go) — never exposed via VisibleFields()/
// Object.keys/JSON, same convention RegexHandleField uses.
const EventSourceHandleField = "__kml_es_handle"

// EventSourceLastEventIdField is the name of the hidden ptr field (index 1,
// right after EventSourceHandleField) holding the SSE "last event ID"
// buffer (TDD-00038 Stage 1) — persists across dispatched records (an `id:`
// field updates it; a record with none leaves it unchanged), read into
// each dispatched MessageEvent's own lastEventId field. Hidden, not a
// visible property on EventSource itself, matching the real spec (only
// each individual event carries lastEventId — the source object doesn't
// expose it as a readable property).
const EventSourceLastEventIdField = "__kml_es_last_event_id"

// EventSourceType returns `new EventSource(url)`'s result type. url/
// readyState/onmessage are plain visible fields (readyState: 0 CONNECTING,
// 1 OPEN, 2 CLOSED), mirroring how Response's status/ok/body are plain
// field reads via the ordinary object machinery, no dispatched getter
// needed — including for `onmessage`'s assignment (`es.onmessage = (ev) =>
// ...`), which goes through the same generic object-field-assignment path
// as any other FuncType-typed field. readyState is written from two
// places: the event loop's own per-iteration scan (a CONNECTING->OPEN/
// ->CLOSED transition observed asynchronously) and emitEventSourceClose
// (synchronously, matching real EventSource's own close() setting
// readyState immediately rather than waiting for the next scan).
// `onmessage`, when non-null, is called directly from the runtime's own
// per-iteration SSE record parser (__kml_eventsource_dispatch_record,
// runtime_eventsource.go) — TDD-00038 Stage 1.
func EventSourceType() Type {
	ty := ObjectType([]Field{
		{Name: EventSourceHandleField, Ty: TypePtr},
		{Name: EventSourceLastEventIdField, Ty: TypePtr},
		{Name: "url", Ty: TypePtr},
		{Name: "readyState", Ty: TypeI64},
		{Name: "onmessage", Ty: FuncType([]Type{MessageEventType()}, TypeVoid)},
		// onopen/onerror (TDD-00038 Stage 2) — appended after onmessage
		// rather than interleaved with the hidden fields, so every existing
		// field's index stays exactly what it was in Stage 0/1 (only
		// VisibleFields()'s hidden-prefix count would need to change for an
		// interleaved insertion; a trailing append needs no such change).
		// Both fire with a MessageEventType() payload (data/lastEventId
		// left empty, type "open"/"error") for closure-call ABI uniformity
		// with onmessage, even though real EventSource passes a plain,
		// data-less Event for these two — see runtime_eventsource.go's
		// __kml_eventsource_scan for where each actually fires.
		{Name: "onopen", Ty: FuncType([]Type{MessageEventType()}, TypeVoid)},
		{Name: "onerror", Ty: FuncType([]Type{MessageEventType()}, TypeVoid)},
	})
	ty.IsEventSource = true
	return ty
}

// MessageEventType returns the fixed, concrete payload type every
// EventSource listener is called with (TDD-00038 Stage 1) — data/type/
// lastEventId, all strings, matching the real MessageEvent shape SSE
// actually needs (no generic type parameter the way EventEmitter<T> has,
// since the payload shape never varies by user code — see the TDD's own
// Design section on why this is simpler than EventEmitter<T> in exactly
// this one respect).
func MessageEventType() Type {
	return ObjectType([]Field{
		{Name: "data", Ty: TypePtr},
		{Name: "type", Ty: TypePtr},
		{Name: "lastEventId", Ty: TypePtr},
	})
}

// EventType is the WHATWG Event object (TDD-00081): a fixed-shape object with a
// type string, a defaultPrevented flag, and an internal stopImmediate flag,
// tagged IsEvent so preventDefault/stop* dispatch on it.
func EventType() Type {
	t := ObjectType([]Field{
		{Name: "type", Ty: TypePtr},
		{Name: "defaultPrevented", Ty: TypeBool},
		{Name: "stopImmediate", Ty: TypeBool},
		{Name: "cancelable", Ty: TypeBool},
	})
	t.IsEvent = true
	return t
}

// EventTargetType is a WHATWG EventTarget: a bare `Map<string, listener-list>`
// pointer, tagged IsEventTarget (TDD-00081 Stage 2).
func EventTargetType() Type {
	return Type{IR: "ptr", IsEventTarget: true}
}

// AbortSignalType is a WHATWG AbortSignal (TDD-00081 Stage 3): an object with an
// aborted flag, a reason, and a hidden listener registry so it dispatches "abort"
// like an EventTarget.
func AbortSignalType() Type {
	t := ObjectType([]Field{
		{Name: "aborted", Ty: TypeBool},
		{Name: "reason", Ty: TypePtr},
		{Name: "listeners", Ty: TypePtr},
		// deadlineNs: a monotonic-ns deadline for AbortSignal.timeout(ms) (0 =
		// none). The fetch await loop / event loop fold this in via
		// __kml_signal_aborted so a slow request is cancelled at the deadline.
		{Name: "deadlineNs", Ty: TypeI64},
	})
	t.IsAbortSignal = true
	return t
}

// AbortControllerType wraps an AbortSignal in its `signal` field.
func AbortControllerType() Type {
	t := ObjectType([]Field{
		{Name: "signal", Ty: AbortSignalType()},
	})
	t.IsAbortController = true
	return t
}

// CustomEventType is EventType plus a `detail` field of the given type.
func CustomEventType(detailTy Type) Type {
	t := ObjectType([]Field{
		{Name: "type", Ty: TypePtr},
		{Name: "detail", Ty: detailTy},
		{Name: "defaultPrevented", Ty: TypeBool},
		{Name: "stopImmediate", Ty: TypeBool},
		{Name: "cancelable", Ty: TypeBool},
	})
	t.IsEvent = true
	return t
}

// WSConnFdField is the name of the hidden i64 field every WSConnection
// carries at index 0, holding the raw accepted-socket fd — set once, right
// after the upgrade handshake, and read directly by `.send()`/`.close()`
// (emit_websocket.go). A plain fd copy rather than a pointer back into the
// connection-fiber array (`@__kml_conn_data`, runtime_http.go) deliberately:
// that array's backing storage can move (realloc) whenever another
// connection is accepted, but the fd value itself never changes for the
// life of this connection, so copying it once is simpler and avoids any
// dangling-pointer risk from a WSConnection outliving a fiber-array growth.
const WSConnFdField = "__kml_ws_conn_fd"

// WSConnectionType returns the type of the object passed to
// `http.listen(port, handler, { ws })`'s upgrade handler (TDD-00039 Stage
// 1) — see IsWSConnection's doc comment for why this is a real heap object
// built internally rather than something user code ever constructs.
// `onmessage`, like EventSource's own field of the same name, is a plain
// FuncType-typed field — assigning to it (`socket.onmessage = (ev) =>
// ...`) needs no dedicated codegen at all, since MemberExpression
// assignment already threads the field's declared type through as a hint
// (emitExprWithObjectHint, emit_exprs_assign.go), correctly resolving an
// unannotated `ev` to WSMessageEventType() the same way it already does for
// EventSource's `.onmessage`. Read directly from the runtime's own
// persistent per-connection read loop (emit_websocket.go) whenever a
// complete text/binary frame is decoded — no listener-list/EventEmitter
// machinery needed, since (like EventSource) there's exactly one callback
// slot, not an accumulating list.
func WSConnectionType() Type {
	ty := ObjectType([]Field{
		{Name: WSConnFdField, Ty: TypeI64},
		{Name: "onmessage", Ty: FuncType([]Type{WSMessageEventType()}, TypeVoid)},
	})
	ty.IsWSConnection = true
	return ty
}

// WSMessageEventType returns the fixed, concrete payload type an
// `onmessage` listener is called with (TDD-00039 Stage 1) — just `data`,
// unlike MessageEventType's SSE-specific `type`/`lastEventId` (which have
// no WebSocket-frame equivalent). Both text and binary frames are exposed
// through this same string field: a binary payload with an embedded null
// byte truncates through ordinary string operations exactly like every
// other strlen-based string value in this compiler (`req.body`, `fetch`'s
// `.text()`) — a real, documented V1 narrowing, not a bug; a binary-safe
// accessor (mirroring `req.bodyBytes()`) is a real, undesigned follow-on.
func WSMessageEventType() Type {
	return ObjectType([]Field{
		{Name: "data", Ty: TypePtr},
	})
}

// WebSocketClientHandleField is the name of the hidden ptr field every
// `new WebSocket(url)` instance carries at index 0, pointing at the
// runtime's own client entry struct (`{ i64 fd, i64 state, i64
// pendingNotify, ptr buf, i64 consumedOffset, ptr instance }`,
// runtime_websocket_client.go) — never exposed via VisibleFields()/
// Object.keys/JSON, same convention EventSourceHandleField/WSConnFdField
// use.
const WebSocketClientHandleField = "__kml_wsc_handle"

// WebSocketClientType returns `new WebSocket(url)`'s result type
// (TDD-00039 Stage 3). url/readyState are plain visible fields (readyState:
// 0 CONNECTING, 1 OPEN, 2 CLOSED — EventSource's own simplified 3-state
// model, skipping the real spec's CLOSING, same choice EventSourceType
// already made). `onopen`/`onmessage`/`onclose`/`onerror` all share
// WSMessageEventType() as their payload shape (data left "" for the three
// that carry no real message, matching EventSourceType's own onopen/onerror
// precedent — "closure-call ABI uniformity" over a payload-per-event-kind
// design) — assigning to any of them is a plain FuncType field assignment,
// needing no dedicated codegen (see WSConnectionType's own doc comment on
// why the hint-propagation this depends on already exists generically).
//
// Unlike EventSource (whose connection is asynchronous by construction —
// `new EventSource(url)` never blocks), `new WebSocket(url)` performs its
// TCP connect + HTTP upgrade handshake *synchronously*, before the
// constructor even returns (emit_websocket_client.go) — a deliberate V1
// simplification avoiding new non-blocking-connect machinery in the event
// loop's hand-rolled select() call (see TDD-00039's own Design section
// noting this exact sequencing was left as an implementation detail, not a
// design fork). This creates a real ordering problem: `.onopen` can only be
// assigned *after* `new WebSocket(url)` returns, but by then the connection
// has already succeeded or failed. Solved by never invoking
// `.onopen`/`.onerror` synchronously during construction at all — readyState
// is set immediately (reflecting the real, already-known outcome), but the
// *callback* firing is deferred to this client's first event-loop scan
// pass (the `pendingNotify` field on the runtime entry), by which point
// user code has had a chance to assign `.onopen`/`.onmessage`/`.onerror`.
// A failed connect/handshake never throws for this same reason (matching
// real WebSocket, which also never throws synchronously for a network-level
// failure) — it fires `.onerror` then `.onclose` on that same deferred
// first pass instead. Only a malformed URL/unsupported scheme (`wss://`) is
// a synchronous throw, a programmer-facing contract issue, not a network
// failure.
func WebSocketClientType() Type {
	msgTy := WSMessageEventType()
	ty := ObjectType([]Field{
		{Name: WebSocketClientHandleField, Ty: TypePtr},
		{Name: "url", Ty: TypePtr},
		{Name: "readyState", Ty: TypeI64},
		{Name: "onopen", Ty: FuncType([]Type{msgTy}, TypeVoid)},
		{Name: "onmessage", Ty: FuncType([]Type{msgTy}, TypeVoid)},
		{Name: "onclose", Ty: FuncType([]Type{msgTy}, TypeVoid)},
		{Name: "onerror", Ty: FuncType([]Type{msgTy}, TypeVoid)},
	})
	ty.IsWebSocketClient = true
	return ty
}

// WorkerType is `new Worker(path)`'s result (TDD-00098): a bare pointer to
// the runtime control block, flag-tagged so method dispatch and the string
// heuristics never mistake it for anything else. path keys the compile-time
// channel record in e.workerEntries.
func WorkerType(path string) Type {
	ty := ObjectType([]Field{{Name: "__worker_ctrl", Ty: TypePtr}})
	ty.IsWorker = true
	ty.WorkerPath = path
	return ty
}

// ChildProcessType is a spawn()/exec()/execFile() ChildProcess handle: a bare
// pointer to runtime_childprocess.go's cpStructIR, flag-tagged for method and
// property dispatch (.stdout/.stderr/.stdin/.on/.pid/.kill).
func ChildProcessType() Type {
	ty := ObjectType([]Field{{Name: "__cp", Ty: TypePtr}})
	ty.IsChildProcess = true
	return ty
}

// CPStreamType is child.stdout (which 0) / child.stderr (which 1): the same
// underlying cp pointer, tagged so .on('data'|'end', cb) stores into the
// correct listener slots.
func CPStreamType(which int) Type {
	ty := ChildProcessType()
	ty.IsChildProcess = false
	ty.IsCPStream = true
	ty.CPWhich = which
	return ty
}

// CPStdinType is child.stdin: the cp pointer, tagged for .write()/.end().
func CPStdinType() Type {
	ty := ChildProcessType()
	ty.IsChildProcess = false
	ty.IsCPStdin = true
	return ty
}

// ReadlineType is a readline.createInterface() handle: a bare pointer to
// runtime_readline.go's rlStructIR, flag-tagged for .on/.question/.close.
func ReadlineType() Type {
	ty := ObjectType([]Field{{Name: "__rl", Ty: TypePtr}})
	ty.IsReadline = true
	return ty
}

// StdinType is the process.stdin streaming handle: a bare pointer to
// runtime_stdin.go's stdinStructIR, flag-tagged for .on('data'|'end') dispatch.
func StdinType() Type {
	ty := ObjectType([]Field{{Name: "__stdin", Ty: TypePtr}})
	ty.IsStdin = true
	return ty
}

// HTTPServerType is an http.createServer() handle bound to a variable (the
// standard Node idiom, as opposed to the chained
// `http.createServer(cb).listen(port)` expression): a pointer to a single i64
// slot holding the listen fd (-1 before .listen()), flag-tagged for
// .listen/.close/.closeAllConnections/.address.
func HTTPServerType() Type {
	ty := ObjectType([]Field{{Name: "__httpsrv", Ty: TypePtr}})
	ty.IsHTTPServer = true
	return ty
}

// ClientRequestType is the http.get/request return handle (ADR-00430): a
// pointer to { ptr url, ptr userCb, i64 state } (state 0 pending · 1 fired ·
// 2 aborted). `request` fires on .end(); `get` is returned already fired.
func ClientRequestType() Type {
	ty := ObjectType([]Field{{Name: "__clientreq", Ty: TypePtr}})
	ty.IsClientRequest = true
	return ty
}

// HTTPAgentType is the inert `new http.Agent(...)` token (ADR-00432): a
// single heap byte — the client opens one connection per request, so an
// Agent carries configuration with no behavior; .destroy() is a no-op.
func HTTPAgentType() Type {
	ty := ObjectType([]Field{{Name: "__httpagent", Ty: TypePtr}})
	ty.IsHTTPAgent = true
	return ty
}

// EmbeddedAssetsType is an `embedDir(...)` handle (TDD-00142 Stage 7): a ptr to
// the packed blob linked into the binary, dispatching `.get(path)`.
func EmbeddedAssetsType() Type {
	ty := ObjectType([]Field{{Name: "__embedassets", Ty: TypePtr}})
	ty.IsEmbeddedAssets = true
	return ty
}

// WebviewType is a `new Webview(...)` handle (TDD-00142): a calloc'd
// `{ ptr webview_t, ptr boundListHead }` struct, flag-tagged for the window
// method dispatch in emit_webview.go.
func WebviewType() Type {
	ty := ObjectType([]Field{{Name: "__webview", Ty: TypePtr}})
	ty.IsWebview = true
	return ty
}

// NetServerType is a net.createServer() handle: a bare pointer to
// runtime_net.go's netServerIR, flag-tagged for .listen/.on/.close.
func NetServerType() Type {
	ty := ObjectType([]Field{{Name: "__netsrv", Ty: TypePtr}})
	ty.IsNetServer = true
	return ty
}

// NetSocketType is a TCP connection socket: a bare pointer to runtime_net.go's
// netSocketIR, flag-tagged for .on('data'|'end')/.write/.end.
func NetSocketType() Type {
	ty := ObjectType([]Field{{Name: "__netsock", Ty: TypePtr}})
	ty.IsNetSocket = true
	return ty
}

// DgramSocketType is a dgram.createSocket() handle: a bare pointer to
// runtime_dgram.go's dgramSocketIR, flag-tagged for .bind/.on/.send/.close.
func DgramSocketType() Type {
	ty := ObjectType([]Field{{Name: "__dgram", Ty: TypePtr}})
	ty.IsDgramSocket = true
	return ty
}

// ClusterWorkerType is a cluster.fork() Worker handle: a bare pointer to
// runtime_cluster.go's clusterWorkerIR { i64 id, i64 pid }, flag-tagged for
// `.id` access.
func ClusterWorkerType() Type {
	ty := ObjectType([]Field{{Name: "__worker", Ty: TypePtr}})
	ty.IsClusterWorker = true
	return ty
}

// BufferType returns a Node Buffer's type (TDD-00103): a Uint8Array
// (TypedArrayType("uint8")) with IsBuffer set — see the flag's doc comment.
func BufferType() Type {
	ty := TypedArrayType("uint8")
	ty.IsBuffer = true
	return ty
}

// BlobType returns `new Blob(...)`'s result type — see IsBlob's doc comment.
func BlobType() Type {
	return Type{IR: "ptr", IsBlob: true}
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
	ty.IsTypedArray = true
	switch elemKind {
	case "bigint64", "biguint64":
		ty.BigIntElem = true
	case "uint8clamped":
		ty.Clamped = true
	}
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

// ClassNodeStreamField is the name of the hidden ptr field a class carries
// (TDD-00132) when HasNodeStream is set — positioned right after the tag
// (and vtable pointer, if present; a stream class never also sets
// HasEventEmitter, since the Node-stream runtime carries its own listener
// surface). Holds the `nodestream` handle the options-form `new Readable(...)`
// builds. Reserved the same way ClassTagField/ClassVTableField are.
const ClassNodeStreamField = "__kml_ns_handle"

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
func ClassType(name string, inherited []Field, own []Field, hasVTable, hasEventEmitter, hasNodeStream bool) Type {
	tagged := make([]Field, 0, 4+len(inherited)+len(own))
	tagged = append(tagged, Field{Name: ClassTagField, Ty: TypeI64})
	if hasVTable {
		tagged = append(tagged, Field{Name: ClassVTableField, Ty: TypePtr})
	}
	if hasEventEmitter {
		tagged = append(tagged, Field{Name: ClassEventEmitterField, Ty: TypePtr})
	}
	if hasNodeStream {
		tagged = append(tagged, Field{Name: ClassNodeStreamField, Ty: TypePtr})
	}
	tagged = append(tagged, inherited...)
	tagged = append(tagged, own...)
	ty := ObjectType(tagged)
	ty.IsClass = true
	ty.ClassName = name
	ty.HasVTable = hasVTable
	ty.HasEventEmitter = hasEventEmitter
	ty.HasNodeStream = hasNodeStream
	return ty
}

// VisibleFields returns the fields a user should ever see: identical to
// Fields for every non-class/non-error object type, but with the hidden
// leading fields (tag, plus vtable pointer when HasVTable, plus the
// EventEmitter listener-map handle when HasEventEmitter — TDD-00023)
// stripped, and (TDD-00021) any `#`-named private field/method filtered out
// regardless of position — real JS reflection never sees a private name at
// all, unlike `private`/`protected` (a TypeScript-only, compile-time-erased
// concept real JS reflection doesn't know exists). Use this instead of
// Fields directly at any reflection/enumeration call site (Object.keys,
// Object.values, Object.entries, Object.assign, JSON.stringify, for...in,
// object-literal/spread field copying) — GEP/field-access code should keep
// using Fields (via FieldIndex) unchanged, since the hidden fields'
// presence is what makes those indices correct in the first place.
func (t Type) VisibleFields() []Field {
	fields := t.Fields
	switch {
	case t.IsClass && len(fields) > 0:
		skip := 1
		if t.HasVTable {
			skip++
		}
		if t.HasEventEmitter {
			skip++
		}
		if t.HasNodeStream {
			skip++
		}
		fields = fields[skip:]
	case t.IsError && len(fields) > 0:
		fields = fields[1:]
	case t.IsRegExp && len(fields) > 0:
		fields = fields[1:]
	case t.IsEventSource && len(fields) > 1:
		fields = fields[2:]
	case t.IsWSConnection && len(fields) > 0:
		fields = fields[1:]
	case t.IsWebSocketClient && len(fields) > 0:
		fields = fields[1:]
	case t.IsXHR && len(fields) > 3:
		fields = fields[4:]
	case t.IsStats:
		// Every Stats field (mode included) is a real Node own-enumerable
		// property, so nothing is stripped (ADR-00565).
	case t.IsRequest && len(fields) > 5:
		// HttpRequest's first five fields (method/path/query/headers/body) are
		// the user-facing surface; the trailing bodyLength + __kml_bodyctx are
		// implementation-only (backing .bodyBytes()/.stream()) and must not leak
		// through Object.keys/JSON.stringify/spread (TDD-00118 follow-up).
		fields = fields[:5]
	}
	hasPrivate := false
	for _, f := range fields {
		if strings.HasPrefix(f.Name, "#") {
			hasPrivate = true
			break
		}
	}
	if !hasPrivate {
		return fields
	}
	visible := make([]Field, 0, len(fields))
	for _, f := range fields {
		if !strings.HasPrefix(f.Name, "#") {
			visible = append(visible, f)
		}
	}
	return visible
}

// RequestType returns http.listen()'s request object type — spelled
// `HttpRequest` in source, not `Request` (TDD-00040 freed that name up for
// the real client-side Request class, FetchRequestType, below): a plain
// heap object (readable via the ordinary object field-access path, plus one
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
// ServerResponseType is Node's `http.createServer` `res` object (TDD-00131).
// Its fields are exactly what the existing http dispatcher reads off a bespoke
// handler's returned object — status/body/headers — so once `res.writeHead`/
// `end`/etc. have mutated them, the same response-writing path applies. status
// is an integer (the HTTP status code); body accumulates written chunks.
func ServerResponseType() Type {
	ty := ObjectType([]Field{
		{Name: "status", Ty: TypeI64},
		{Name: "body", Ty: TypePtr},
		{Name: "headers", Ty: MapType(TypePtr, TypePtr)},
	})
	ty.IsServerResponse = true
	return ty
}

// Http2ServerStreamType is the server-side Http2Stream (TDD-00139 Stage 2)
// handed to `server.on('stream', (stream, headers) => …)`. Its first three
// fields deliberately mirror ServerResponseType — status/body/headers are what
// the shared response-writing tail reads after the handler returns — with the
// request body appended so `stream.on('data')` can deliver it.
func Http2ServerStreamType() Type {
	ty := ObjectType([]Field{
		{Name: "status", Ty: TypeI64},
		{Name: "body", Ty: TypePtr},
		{Name: "headers", Ty: MapType(TypePtr, TypePtr)},
		{Name: "reqBody", Ty: TypePtr},
		{Name: "reqBodyLen", Ty: TypeI64},
	})
	ty.IsH2ServerStream = true
	return ty
}

// DiagChannelType is a diagnostics_channel Channel handle: a pointer to the
// runtime channel record (runtime_diagch.go's layout).
func DiagChannelType() Type {
	ty := ObjectType([]Field{{Name: "__dcchan", Ty: TypePtr}})
	ty.IsDCChannel = true
	return ty
}

// TestContextType is the node:test runner's `t` (TDD-00140): a pointer to
// the per-test record {aftersRoot, aftersN, aftersCap, skipped, todo}.
func TestContextType() Type {
	ty := ObjectType([]Field{{Name: "__testctx", Ty: TypePtr}})
	ty.IsTestContext = true
	return ty
}

// Http2ClientSessionType is a ClientHttp2Session handle from http2.connect
// (TDD-00139 Stage 3): one pointer slot holding the C driver session.
func Http2ClientSessionType() Type {
	ty := ObjectType([]Field{{Name: "__h2sess", Ty: TypePtr}})
	ty.IsH2ClientSession = true
	return ty
}

// Http2ClientStreamType is a ClientHttp2Stream from session.request: the
// 32-byte callback context the driver's response frames fire into
// (cbResponse/cbData/cbEnd/headersMap — see ensureH2ClientRuntime).
func Http2ClientStreamType() Type {
	ty := ObjectType([]Field{{Name: "__h2creq", Ty: TypePtr}})
	ty.IsH2ClientStream = true
	return ty
}

// IncomingMessageType is Node's `http.get`/`http.request` response object
// (TDD-00138) handed to the callback: `res.statusCode`, `res.on('data'|'end')`,
// with the buffered body + listener slots hidden behind the event surface.
func IncomingMessageType() Type {
	ty := ObjectType([]Field{
		{Name: "statusCode", Ty: TypeF64},
		{Name: incomingBodyField, Ty: TypePtr},
		{Name: incomingDataListenerField, Ty: TypePtr},
		{Name: incomingEndListenerField, Ty: TypePtr},
	})
	ty.IsIncomingMessage = true
	return ty
}

const (
	incomingBodyField         = "__kml_im_body"
	incomingDataListenerField = "__kml_im_data"
	incomingEndListenerField  = "__kml_im_end"
)

func RequestType() Type {
	ty := ObjectType([]Field{
		{Name: "method", Ty: TypePtr},
		{Name: "path", Ty: TypePtr},
		{Name: "query", Ty: MapType(TypePtr, TypePtr)},
		{Name: "headers", Ty: MapType(TypePtr, TypePtr)},
		{Name: "body", Ty: TypePtr},
		{Name: "bodyLength", Ty: TypeI64},
		// The streaming body context (TDD-00097 Stage 5b) — null unless the
		// program uses req.stream() (which switches the dispatcher to
		// headers-complete dispatch); .body/.bodyBytes() drain through it.
		{Name: "__kml_bodyctx", Ty: TypePtr},
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
	// A nullable non-pointer scalar field carries its presence bit alongside
	// the value (TDD-00064 Stage 3), so a null field is distinguishable from a
	// real 0 — the same { i1, T } slot a nullable-scalar local/param/return uses.
	if isNullableScalar(ty) {
		return nullableScalarStorageIR(ty)
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
	// A dynamic/union field's storage is the { i8, i64 } box — 16 bytes (the i8
	// tag padded to the i64's 8-byte alignment, plus the i64 payload), not the
	// 8 bytes ty.Align() alone would suggest (TDD-00119).
	if ty.IsDynamic {
		return 16
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
	return ty.Nullable && !ty.IsDynamic && ty.IR != "ptr" && ty.IR != "void"
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
	if t.IR == "ptr" {
		return "null"
	}
	return "0"
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
// call instructions. Arrays are returned as an aggregate {ptr, i64}; a nullable
// non-pointer scalar as its presence-flagged { i1, T } aggregate (TDD-00064
// Stage 3), so `T | null` survives a function boundary with its null-ness.
func (t Type) LLVMRetType() string {
	if t.IsArray {
		return "{ptr, i64}"
	}
	if isNullableScalar(t) {
		return nullableScalarStorageIR(t)
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
	TypeNever     = Type{IR: "i64", IsNever: true}
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
	// Optional marks a `param?: T` parameter (ADR-00164) — a call site
	// omitting it (and with no Defaults[i] expression either) gets T's own
	// zero value (Type.zeroLiteral()) rather than a "missing argument"
	// error, this compiler's established stand-in for real JS's
	// `undefined` on a concrete type with no general sentinel for it (the
	// same simplification ADR-00157's calloc fix and ADR-00158's
	// destructuring defaults both already document).
	Optional []bool
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
		// TDD-00123 Stage 1: `number` is a JS-faithful IEEE-754 double. The
		// integer types (`int8..int64`, `uint8..uint64`) remain the opt-in
		// escape hatch for real integers.
		return TypeF64
	case "string":
		return TypePtr
	case "boolean":
		return TypeBool
	case "EventTarget":
		return EventTargetType()
	case "AbortController":
		return AbortControllerType()
	case "AbortSignal":
		return AbortSignalType()
	case "Event":
		return EventType()
	case "CustomEvent":
		// A bare `CustomEvent` annotation types detail as a ptr; `CustomEvent<T>`
		// (with a resolved detail type) is handled by the generic path (TDD-00081).
		return CustomEventType(TypePtr)
	case "void":
		return TypeVoid
	case "null":
		return TypeNull
	case "undefined":
		return TypeUndefined
	case "symbol":
		return SymbolType()
	case "bigint":
		return BigIntType()
	case "any", "unknown":
		return TypeAny
	case "Date":
		return TypeDate
	case "Response":
		return ResponseType()
	case "HttpRequest", "IncomingMessage":
		return RequestType()
	case "__kml_test_ctx":
		// Internal-only: the node:test runner's TestContext parameter.
		return TestContextType()
	case "__kml_h2_stream":
		// Internal-only: synthesized for the http2 stream handler's first param.
		return Http2ServerStreamType()
	case "__kml_h2_headers":
		// Internal-only: the http2 stream handler's headers map.
		return MapType(TypePtr, TypePtr)
	case "__kml_client_response":
		// Internal-only name synthesized by contextTypeArrowParams for the
		// http client's response callback — Node calls both the server request
		// and the client response `IncomingMessage`, but this compiler models
		// them as distinct types and the public annotation name is taken by
		// the server side above. Never written by user code.
		return IncomingMessageType()
	case "ServerResponse":
		return ServerResponseType()
	case "Request":
		return FetchRequestType()
	case "Headers":
		return HeadersType()
	case "XMLHttpRequest":
		return XMLHttpRequestType()
	case "URL":
		return URLType()
	case "URLSearchParams":
		return URLSearchParamsType()
	case "URLPattern":
		return URLPatternType()
	case "RegExp":
		return RegExpType()
	case "EventSource":
		return EventSourceType()
	case "MessageEvent":
		return MessageEventType()
	case "WSConnection":
		return WSConnectionType()
	case "WSMessageEvent":
		return WSMessageEventType()
	case "WebSocket":
		return WebSocketClientType()
	case "ArrayBuffer":
		return ArrayBufferType()
	case "SharedArrayBuffer":
		return SharedArrayBufferType()
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
	case "Uint8ClampedArray":
		return TypedArrayType("uint8clamped")
	case "Buffer":
		return BufferType()
	case "BigInt64Array":
		return TypedArrayType("bigint64")
	case "BigUint64Array":
		return TypedArrayType("biguint64")
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
