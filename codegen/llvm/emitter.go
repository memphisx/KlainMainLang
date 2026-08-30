package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Value is an LLVM value reference: either a register (%t0) or an inline constant (42).
type Value struct {
	Ref string
	Ty  Type
}

// Symbol represents a local variable in the symbol table.
type Symbol struct {
	// Ptr is the value's storage slot. For a scalar it is the alloca holding the
	// value. For an array (object-reference model, TDD-00127) it is a stable
	// slot — an `alloca ptr`, or a module global — holding a pointer to a shared
	// heap {data, len} header; arrayDataLenSlots derives the field addresses.
	Ptr     string
	Ty      Type
	Boxed   bool // true once Ptr points to a heap cell shared with closures that capture it
	IsConst bool // true for a `const`-declared binding; checked by emitAssign to reject plain `=` reassignment
	// NullableBoxed marks a binding whose storage is the presence-flagged
	// { i1, T } nullable-scalar aggregate (TDD-00064 Option A), as opposed to a
	// bare scalar slot. Only local variable declarations set it so far; a
	// nullable-scalar function parameter or object field still uses bare
	// storage (with the Nullable flag set on Ty but no presence word) until
	// Stage 3 converts those boundaries, so every null-aware site must gate on
	// this rather than on isNullableScalar(Ty) alone — otherwise it would GEP a
	// presence/payload field out of a plain scalar slot. See
	// Symbol.isNullableScalarLocal.
	NullableBoxed bool
	// NarrowedNonNull marks a nullable-scalar binding (TDD-00064 Stage 2) that
	// flow analysis has proven non-null in the current region — a shadow copy
	// defined into the guarded block's own scope, sharing the outer binding's
	// storage Ptr but known present. It lets null-aware sites (console.log's
	// null-vs-value branch, `x ?? d`, `x === null`) skip the runtime presence
	// test and treat the value as definitely a T. popScope discards the shadow,
	// restoring the nullable view.
	NarrowedNonNull bool
	// NarrowedTo marks a union-typed binding (TDD-00114) that flow analysis has
	// narrowed to a specific member type in the current region — a shadow copy
	// defined into the guarded block's own scope, sharing the outer binding's
	// storage Ptr but read as the concrete narrowed type (the union box is
	// unboxed to it at every use site). nil = not narrowed. popScope discards
	// the shadow, restoring the union view. Generalizes NarrowedNonNull from a
	// bool to a target type.
	NarrowedTo *Type
}

// isNullableScalarLocal reports whether this binding is a nullable non-pointer
// scalar stored as the presence-flagged { i1, T } aggregate — the only shape
// the Stage 1/2 null-aware code paths may GEP/load a presence bit from. A
// nullable-scalar parameter/field (bare storage, Stage 3) returns false here
// and keeps its pre-existing bare-scalar behavior.
func (s Symbol) isNullableScalarLocal() bool {
	return s.NullableBoxed && isNullableScalar(s.Ty)
}

type scope struct {
	syms map[string]Symbol
}

// Emitter walks an AST and produces LLVM IR text.
type Emitter struct {
	globals   strings.Builder // global declarations (string constants, printf decl, …)
	functions strings.Builder // emitted user-defined function bodies
	allocas   strings.Builder // alloca instructions for the current function
	body      strings.Builder // body instructions for the current function
	scopes    []scope
	// moduleGlobals holds top-level `const`/`let` bindings promoted to LLVM
	// module globals so a named `function` declaration (emitted with a fresh,
	// separate scope) can read them — not just an arrow/closure that captures
	// them (TDD-00093). lookup falls back to this after the function scopes.
	moduleGlobals map[string]Symbol
	// promotedGlobalDecls marks the exact top-level VarDeclaration nodes promoted
	// to module globals, by pointer identity — so emitVarDecl stores the
	// initializer into the global for *those* declarations only. A same-named
	// local inside a function or a nested block (a different node) is unaffected
	// and shadows the global normally; scope depth can't distinguish them (a
	// function body also runs at scopes depth 1).
	promotedGlobalDecls map[*ast.VarDeclaration]bool
	// varsBeingInitialized names the variable(s) whose initializer is currently
	// being emitted. A closure created inside a variable's own initializer that
	// captures that same variable (`const s = f(() => use(s))`) must NOT seed its
	// capture cell by loading the still-uninitialized slot — see
	// promoteCaptureToCell.
	varsBeingInitialized  map[string]bool
	regCtr                int
	labelCtr              int
	strConsts             map[string]string // Go string value → @.s<n> name
	strIdx                int
	linkLibs              map[string]bool // external non-libc libraries the compiled program needs (e.g. "curl")
	memMode               string          // "" (== "manual", the default) or "gc" — see SetMemMode
	dynamicImportMode     string          // "eager" (default) or "lazy" — see SetDynamicImportMode (TDD-00055/TDD-00056)
	usesDynamicImport     bool            // set when an import(...) call is emitted
	islandHash            string          // non-empty when compiling a shared-library island (TDD-00056): its stable hash
	dynImportShimDeclared bool            // guards the one-time @__kml_dynimport_run declaration
	regexMode             string          // "" (== the default, resolving to the highest implemented ES stage) or "pcre"/"es-ascii"/"es-unicode" — see SetRegexMode / TDD-00067
	bigintBackend         string          // "" (== "libtommath", the default) or "gmp" — the __kml_bigint_* ABI implementation to link. See SetBigIntBackend / TDD-00074
	compatMode            string          // "" (== "strict", the default) or "js" — the whole-program compatibility axis. See SetCompatMode / TDD-00075
	cryptoBackend         string          // "" (== "openssl", the default) or "commoncrypto" — the __kml_crypto_* ABI implementation to compile+link. See SetCryptoBackend / TDD-00104
	usesCrypto            bool            // set the first time any crypto.subtle operation is emitted (drives backend compile+link in main.go)
	usesBigInt            bool            // set the first time any bigint operation is emitted (drives backend compile+link in main.go)
	declaredBigInt        bool            // the __kml_bigint_* declares have been emitted once
	usesJSONParse         bool            // set the first time JSON.parse/Response.json() is emitted (drives json_parse.c compile+link in main.go — TDD-00077 Track P)
	declaredJSONParseTree bool            // the __kml_json_* parse-tree declares have been emitted once
	usesURLPattern        bool            // set the first time a URLPattern is constructed (drives urlpattern.c compile+link in main.go — TDD-00100)
	declaredURLPattern    bool            // the __kml_urlpattern_* declares have been emitted once
	usesFloatFmt          bool            // set the first time a float is printed (drives dtoa.c compile+link in main.go — TDD-00080)
	declaredDtoa          bool            // the __kml_dtoa declare has been emitted once
	usedSignalAborted     bool            // the __kml_signal_aborted helper has been emitted (TDD-00081 Stage 3c)
	usedPrintf            bool
	usedDprintf           bool
	usedMalloc            bool
	usedCalloc            bool
	usedRealloc           bool
	usedMemmove           bool
	funcs                 map[string]FuncSig            // registered function signatures
	interfaces            map[string]Type               // named interface, type alias, and class registry
	interfaceMethodSigs   map[string]map[string]FuncSig // interface name → method name → signature (TDD-00009 Stage 4, `implements` conformance only — not used for dispatch)
	classes               map[string]ClassInfo          // named class registry (fields/ctor/methods) — see emit_classes.go
	// genericFuncs/genericInterfaces/genericClasses hold the raw declaration
	// for every `<T>`-parameterized function/interface/class (TDD-00010 V1),
	// keyed by its bare source name — deliberately *not* also entered into
	// funcs/interfaces/classes, since T isn't resolvable until a real call/
	// usage/construction site supplies a concrete type. See emit_generics.go.
	genericFuncs      map[string]*ast.FunctionDeclaration
	genericInterfaces map[string]*ast.InterfaceDeclaration
	// genericTypeAliases holds `type Name<T> = ...` declarations (TDD-00079
	// Stage 3), instantiated on demand at each use site by substituting concrete
	// type arguments into the alias body, then resolving the result.
	genericTypeAliases map[string]*ast.TypeAliasDeclaration
	genericClasses     map[string]*ast.ClassDeclaration
	// generators holds one entry per top-level `function*` declaration
	// (TDD-00061/ADR-00172), keyed by its bare source name — deliberately
	// separate from funcs, since constructing a generator (`gen(args)`)
	// never emits a plain `call` to the generator's own compiled body
	// function the way an ordinary named-function call does; see
	// emit_generators.go.
	generators map[string]*GeneratorInfo
	// asyncGenStepFns memoizes the per-generator-type deferred `.next()` microtask
	// step function, keyed by the generator struct IR (TDD-00094).
	asyncGenStepFns map[string]string
	asyncGenStepCtr int
	// currentGenerator is non-nil exactly while emitting a generator
	// function's own body — set/cleared by emitGeneratorFunctionDecl the
	// same way currentRetType/isAsync are for an ordinary function, checked
	// by emitYieldExpression and emitReturn to route `yield`/`return`
	// through the suspend-and-swap path instead of an ordinary value/`ret`.
	currentGenerator *generatorEmitCtx
	// nextClassTagID continues registerClasses' own TagID sequence (see its
	// Pass 1) for generic class instantiations, assigned lazily on demand
	// (emit_generics.go) — always ≥ every real class's TagID, so a
	// Box<number> instance can never collide with an unrelated real class's
	// runtime identity tag.
	nextClassTagID int64
	enums          map[string]map[string]Value // enum name → member name → constant value
	enumBacking    map[string]Type             // enum name → backing value type (i64 numeric, ptr string)
	currentRetType Type                        // return type of the function being emitted
	blockDone      bool                        // true after a terminator (ret/br) in the current block
	closureCtr     int                         // monotonically increasing counter for unique closure names
	// fnValueTrampolines memoizes the env-dropping trampoline emitted for each
	// named function taken by value (`const g = f`), keyed by its mangled LLVM
	// name — see emit_func_value.go.
	fnValueTrampolines map[string]bool
	// nestedFuncScopes/nestedFuncCtr — TDD-00057. One nestedFuncScope frame
	// per enclosing function/closure body currently being emitted, pushed
	// by pushNestedFuncScope and popped once that body finishes; searched
	// innermost-first by resolveFuncRef so a nested function's name is
	// visible throughout its own enclosing body (hoisted, self-recursive,
	// visible to its own further-nested descendants) without ever being
	// entered into the flat, whole-program e.funcs map.
	nestedFuncScopes []nestedFuncScope
	nestedFuncCtr    int
	// prog is the whole program AST, retained for `typeof value` resolution
	// (finding a referenced value's top-level declaration before value scope is
	// populated).
	prog *ast.Program
	// typeofResolving guards against infinite recursion while resolving a
	// self- or mutually-referential `typeof value` query (ADR-00389).
	typeofResolving map[string]bool
	// resolveTypeDepth bounds resolveType recursion so a self-referential type
	// bails to a fallback instead of overflowing the stack.
	resolveTypeDepth int
	// enclosingCapturables (TDD-00129 Stage 1) is a stack parallel to
	// nestedFuncScopes: one frame per enclosing function/closure body, holding
	// the names of that body's *capturable* bindings — its parameters plus its
	// var/let/const (and destructured) locals, but NOT its function/class
	// declarations (those are resolved by name, never closed over). A nested
	// function declaration counts as "capturing" when a free variable of its
	// body is present in any frame here, driving the classification in
	// pushNestedFuncScope. Purely syntactic, since pre-scan runs before the
	// enclosing scope's params/locals are define()d.
	enclosingCapturables []map[string]bool
	usedStrlen           bool
	usedIsatty           bool
	usedTermiosRaw       bool // TDD-00031: process.stdin.setRawMode termios machinery
	usedWinSize          bool // TDD-00031: process.stdout.columns/.rows ioctl(TIOCGWINSZ)
	usedTtyRead          bool // TDD-00031: klain:tty readByte/readKey blocking fd-0 reads
	usedSignalSigwinch   bool // TDD-00031: process.on('SIGWINCH') registration
	usedMemcpy           bool
	usedMemset           bool
	usedStrcmp           bool
	usedSprintf          bool
	usedStrstr           bool
	usedStrncmp          bool
	usedStringTrim       bool
	usedStringTrimStart  bool
	usedStringTrimEnd    bool
	usedStringToUpper    bool
	usedStringToLower    bool
	usedStringReplace    bool
	usedStringReplaceAll bool
	usedStringSplit      bool
	usedAtoll            bool
	usedIPow             bool
	usedJSONStringifyNum bool
	usedJSONStringifyStr bool
	// jsonToJSONActive guards JSON.stringify's toJSON() dispatch against a
	// class whose toJSON() returns its own (or a mutually-referencing) type,
	// which would recurse forever at compile time (cf. ADR-00221). A class
	// name present here is mid-serialization; re-entry serializes it as a
	// plain object instead of re-dispatching toJSON.
	jsonToJSONActive       map[string]bool
	usedAnyEq              bool
	usedClockGettime       bool
	usedDateNow            bool
	usedPerformanceNow     bool
	usedPerformanceMarkMap bool
	usedDateDecompose      bool
	usedSscanf             bool
	usedDaysFromCivil      bool
	usedDateParse          bool
	usedDateCompose        bool
	usedDateNameTables     bool
	usedFetch              bool
	usedFetchAsync         bool
	// TDD-00098: Worker (worker_threads) state. workerEntries maps a worker
	// module's canonical path to its entry symbol + statically-declared
	// channel types; currentWorkerMod is non-empty while a worker module's
	// entry function is being emitted (gates parentPort/workerData).
	usedConnPokeGlobal     bool
	usedChildProcRuntime   bool
	usedReadlineRuntime    bool
	usedStdinRuntime       bool
	usedNetRuntime         bool
	usedDgramRuntime       bool
	usedClusterRuntime     bool
	usedProcessUptime      bool
	usedProcessHrtime      bool
	usedGetrusage          bool
	usedProcessLifecycle   bool
	usedTestRuntime        bool // TDD-00122: the `test` module's mustCall registry + exit verifier
	testTrampolines        map[string]bool
	testSkipFmtEmitted     bool
	testMustNotCallEmitted bool
	dnsDeclared            bool
	getaddrinfoDeclared    bool
	usedCPKill             bool
	usedWorkerRuntime      bool
	hasWorkers             bool // set at EmitProgram start from Program.WorkerModules
	workerEntries          map[string]*workerEntryInfo
	currentWorkerMod       string
	workerAdaptCtr         int
	// genericDepth counts nested generic interface/alias instantiations in
	// resolveType — the cap that turns an infinitely expanding generic
	// (`interface Foo<T> { x: Foo<Foo<T>> }`) into an opaque tail instead of
	// an unbounded recursion (ADR-00452).
	genericDepth int
	// curNamespace is the namespace whose member function is currently being
	// emitted (TDD-00148 Stage 4) — derived from the `__kmlns_` infix of the
	// function's mangled name in emitFunctionDeclAs; "" outside namespace
	// members. Drives the bare-sibling-reference fallback (nsSibling).
	curNamespace string
	// closureAdaptCtr numbers the generated closure adapter trampolines
	// (`@__kml_fnadapt_N`, ADR-00477).
	closureAdaptCtr int
	// TDD-00099: shared memory + channels.
	usedGCUncollectable bool
	usedWeakHelpers     bool
	usedGCGcollect      bool
	usedHTTP2           bool
	// usedSpawnSync links the embedded C behind child_process's *Sync forms.
	usedSpawnSync bool
	// usedIPC links the fork IPC framing C (TDD-00141); usedIPCChildRuntime
	// marks the child-side channel surface (process.send/.on('message')),
	// which hooks the event loop.
	usedIPC             bool
	usedIPCChildRuntime bool
	usedCPForkRuntime   bool
	// usedWebview links the C++ system-webview binding (TDD-00142);
	// webviewConstructed enforces one-window-per-process; webviewThunkCtr
	// numbers the per-bind trampolines.
	usedWebview                bool
	webviewConstructed         bool
	webviewThunkCtr            int
	webviewPumpEmitted         bool
	webviewReturnRunnerEmitted bool
	// webviewTypedRunners caches per-inner-type async-typed-bind settlement
	// runners (TDD-00142 Stage 6), keyed by the inner type's shape.
	webviewTypedRunners map[string]string
	// webviewBindings records the typed window.* bindings for --emit-window-dts.
	webviewBindings []webviewBindingSig
	// embeddedDirs maps a compile-time-literal directory path to the IR symbol
	// its packed blob is linked under (TDD-00142 Stage 7); usedEmbeddedAssets
	// gates the embed_assets.c static-server runtime.
	embeddedDirs       map[string]string
	usedEmbeddedAssets bool
	usedEmbedRuntime   bool
	embedSymbols       map[string]bool
	// usedH2Client marks the http2 client-session runtime (TDD-00139 Stage 3).
	usedH2Client       bool
	usedH2ClientBridge bool
	// usedH2TLSServer marks http2.createSecureServer (TDD-00111 Stage 3b): the
	// event loop gains a TLS-wrap + ALPN + h2-drive branch in its accept path.
	usedH2TLSServer bool
	// usedHTTPS1Server marks the HTTPS/1.1 server path (https.createServer, or
	// http2.createSecureServer's allowHTTP1 fallback): an ALPN-negotiated
	// http/1.1 (or no-ALPN) TLS connection is registered in the fd→SSL table and
	// served by the normal connection fiber, whose I/O is routed through the
	// SSL-aware conn_recv/conn_send/conn_close shims.
	usedHTTPS1Server bool
	// emittedHTTPSConnShims guards the one-time emission of the fd→SSL registry
	// and the conn_recv/conn_send/conn_close I/O shims (emitHTTPSConnShims).
	emittedHTTPSConnShims bool
	// usedNodeTestRuntime: the node:test runner bookkeeping (TDD-00140).
	usedNodeTestRuntime bool
	// usedDiagChRuntime: diagnostics_channel pub/sub core.
	usedDiagChRuntime bool
	// nodeTestPrefix is the compile-time describe/suite name stack.
	nodeTestPrefix           []string // the generic __kml_h2c_on_* IR callbacks http2.c references
	usedAtomicsRuntime       bool
	usedChanRuntime          bool
	usedPipeDecl             bool
	usedPthreadMutex         bool
	usedWorkerFdSetbit       bool
	bcChannels               map[string]*bcChannelInfo
	usedPromiseCombinators   bool
	usedPendingFinishSettled bool
	usedFetchAwaitSettled    bool
	usedCurlSlist            bool
	usedCurlURL              bool
	usedFopen                bool
	usedFclose               bool
	usedFwrite               bool
	usedFsThrow              bool
	usedFsReadFile           bool
	usedFsReadFileRaw        bool
	usedFsReadStream         bool
	usedFread                bool
	usedFsWriteStream        bool
	usedTLS                  bool
	usedTLSRuntime           bool
	usedFsWriteFile          bool
	usedFsAppendFile         bool
	usedFsWriteFileBytes     bool
	usedFsAppendFileBytes    bool
	usedFsExists             bool
	usedFsUnlink             bool
	usedBase64Encode         bool
	usedBase64Decode         bool
	usedBase64Alphabet       bool
	usedBase64EncodeBytes    bool
	usedHexDigits            bool
	usedHexDecodeTable       bool
	usedEncodeURIComponent   bool
	usedEncodeURI            bool
	usedDecodeURIComponent   bool
	usedDecodeURI            bool
	usedCryptoRandomBytes    bool
	usedCryptoFillNumArray   bool
	usedCryptoRandomUUID     bool
	usedReadLineSync         bool
	usedExecFileSync         bool
	usedProcessGetID         bool
	usedChdirDecl            bool
	usedPemFromDer           bool
	cryptoSubtleAliases      map[string]bool
	usedHTTPCThunk           bool
	usedHTTPCBegin           bool
	usedExecvDecl            bool
	usedExecvpDecl           bool
	usedExitRawDecl          bool
	usedForkDecl             bool
	usedCloseDecl            bool
	usedWaitpidDecl          bool
	usedMmapDecl             bool
	usedShutdownDecl         bool
	usedHTTPCloseAllConns    bool
	usedStrHeaderRuntime     bool
	usedSetenvDecl           bool
	usedReadDecl             bool
	usedWriteDecl            bool
	usedFcntlDecl            bool
	usedFflushDecl           bool
	usedHTTPClusterFork      bool
	usedProcessCwd           bool
	usedProcessChdir         bool
	usedGetpid               bool
	usedExecPath             bool
	usedProcessWarning       bool
	usedHTTPClientReactions  bool
	usedHTTPCFlushHook       bool // post-event-loop client-reaction flush hook global
	usedNtohs                bool // shared ntohs libc declaration (net + dgram)
	usedProcessKill          bool
	usedSignalHandler        bool
	usedSignalSigint         bool
	usedSignalSigterm        bool
	usedErrnoAccessor        bool
	usedCryptoCheck          bool
	usedCryptoDigest         bool
	usedCryptoHmac           bool
	usedCryptoMemeq          bool
	usedCryptoAesGcm         bool
	usedCryptoAesCbc         bool
	usedCryptoB64url         bool
	usedCryptoRsa            bool
	usedCryptoEcdsa          bool
	usedCryptoEcRaw          bool
	usedCryptoJwkRsa         bool
	usedCryptoJwkEc          bool
	usedCryptoJwkMapSet      bool
	usedCryptoDerive         bool
	usedStrerror             bool
	usedFsMkdir              bool
	usedFsMkdirP             bool
	usedUnsetenv             bool
	usedSymbolRegistry       bool
	usedFetchHeadersMap      bool
	usedXHRHeadersAll        bool
	usedFsStat               bool
	usedFsLstat              bool
	usedFsPathOps            bool
	usedFsRm                 bool
	usedFsFdOps              bool
	usedFsRmdir              bool
	usedFsRename             bool
	usedFsReaddir            bool
	usedConsoleGroupDepth    bool
	usedConsoleTimer         bool
	usedConsoleCountMap      bool
	usedMapFree              bool
	usedClosureFree          bool
	usedTimers               bool
	usedHTTP                 bool
	usedHTTPClose            bool
	// usedHTTPListen marks the Node `http.createServer(...).listen(...)`
	// bound-handle path (TDD-00131): unlike the bespoke `http.listen`, it does
	// not run the event loop inline — `listen()` registers the listener and
	// returns (non-blocking, Node's listen-then-continue flow), and the Pass 3
	// tail drives the event loop after the top-level script, like timers/SSE.
	// (The bespoke `http.listen` stays blocking: its `{workers}` cluster fork
	// relies on children never running post-listen top-level code.)
	usedHTTPListen bool
	// usedEventSource marks whether the *program* actually constructs an
	// EventSource — distinct from ensureEventSourceRuntime's own internal
	// idempotency flag (usedEventSourceRuntime, runtime_eventsource.go),
	// which is set unconditionally by ensureHTTPRuntime regardless of
	// whether EventSource is ever used (see that function's own doc
	// comment on why every symbol __kml_event_loop_run's IR references must
	// always be defined). This one only decides Pass 3's own tail: prefer
	// the full __kml_event_loop_run() over the narrower __kml_timer_drain()
	// so a plain top-level `new EventSource(...)` with no http.listen still
	// gets its transfer driven and the process stays alive for it.
	usedEventSource bool
	// usedEventSourceRuntime is ensureEventSourceRuntime's own internal
	// idempotency guard (runtime_eventsource.go) — separate from
	// usedEventSource above, since this one is also set unconditionally by
	// ensureHTTPRuntime regardless of whether the program ever constructs
	// an EventSource.
	usedEventSourceRuntime bool
	// WebSocket runtime helpers (TDD-00039 Stage 0) — no AST/emit_* hook
	// exists yet, these are only ever invoked directly by
	// codegen/llvm's own internal tests until Stage 1/3 wire them up to
	// http.listen({ws})/new WebSocket(url). usedWSFshl32 guards the shared
	// llvm.fshl.i32 intrinsic declaration (SHA-1's rotates); the others
	// guard the SHA-1 digest, the shared mask/unmask XOR helper (used by
	// both frame directions, per the TDD's "one loop, not two copies"
	// decision), and the frame encode/decode pair.
	usedWSFshl32      bool
	usedWSSHA1        bool
	usedWSMaskApply   bool
	usedWSFrameEncode bool
	usedWSFrameDecode bool
	// usedWSClient marks whether the *program* actually constructs a
	// `new WebSocket(url)` (TDD-00039 Stage 3) — distinct from
	// usedWSClientRuntime's own internal idempotency flag, mirroring
	// usedEventSource/usedEventSourceRuntime's own split exactly (see that
	// pair's doc comment above): this one only decides EmitProgram's Pass 3
	// tail (prefer the full event-loop drain over the narrower timer-only
	// one so a plain top-level `new WebSocket(...)` stays alive for its
	// onmessage/onclose callbacks).
	usedWSClient bool
	// usedWSClientRuntime is ensureWSClientRuntime's own internal
	// idempotency guard (runtime_websocket_client.go) — set unconditionally
	// by ensureHTTPRuntime regardless of whether the program ever
	// constructs a WebSocket client, same reasoning usedEventSourceRuntime
	// documents.
	usedWSClientRuntime bool
	// httpListenCallSeen is not a "runtime machinery emitted" flag like the
	// usedX fields around it — it tracks whether an http.listen(...) call
	// site has already been compiled, so a second one (only reachable at
	// runtime now that http.close() lets the first one's call return, see
	// TDD-00027) is rejected at compile time instead of producing a
	// duplicate @__kml_http_dispatch definition the LLVM backend would
	// reject with a confusing symbol-collision error.
	httpListenCallSeen bool
	// httpServerHandlerPending marks a zero-arg http.createServer() handle
	// still waiting for its request handler via server.on('request', cb);
	// server.listen rejects until one is registered.
	httpServerHandlerPending  bool
	usedHTTPThrow             bool
	usedSplitFirst            bool
	usedHTTPParseHeaders      bool
	usedHTTPParseQuery        bool
	usedHTTPSerializeHeaders  bool
	usedFiber                 bool
	usedGeneratorRuntime      bool
	chunkHdrAdapterEmitted    bool
	dgramMsgHdrAdapterEmitted bool
	generatorBodyCtr          int
	usedMathFuncs             bool
	usedFloatMinMax           bool
	usedToNumber              bool
	usedBswap16               bool
	usedBswap32               bool
	usedBswap64               bool
	usedRoundEven             bool
	usesBufferCodecs          bool
	usedMemcmp                bool
	declaredBufferCodecs      bool
	usedWsSpan                bool
	usedJsPow                 bool
	namespaces                map[string]map[string]bool
	// nsAliases maps a fully-resolved alias name (dotted for a namespace-
	// scoped `export import`) to the dotted namespace it targets (ADR-00456).
	nsAliases               map[string]string
	usedTaskRuntime         bool
	usedPromiseRuntime      bool // the promise struct + __kml_task_alloc_promise, without the fiber scheduler (TDD-00084 Part A)
	usedPromiseSettle       bool // @__kml_promise_settle — bare-promise settle+wake+drain for new Promise(executor) (TDD-00087)
	usedFetchDriveRunner    bool // @__kml_fetch_drive_run — deferred raw-fetch drive microtask for .then on a fetch
	usedPromiseAdoptRunner  bool // @__kml_promise_adopt_runner — thenable adoption for resolve(aPromise) (TDD-00091)
	usedAwaitTimerDrive     bool // a lightweight await references @__kml_timer_fire_next (TDD-00087)
	usedMicrotasks          bool
	thenCtr                 int // unique-name counter for .then/.catch/.finally reaction runners
	newPromiseCtr           int // unique-name counter for new Promise(executor) resolve/reject thunks (TDD-00087)
	usedCurrentTaskGlobal   bool
	hasMaySuspend           bool // any async fn classified may-suspend (TDD-00083 Stage 2)
	usedCbrt                bool
	usedCtlz32              bool
	usedArc4Random          bool
	usedStrtoll             bool
	usedStrtod              bool
	usedGroupMapHelpers     bool
	usedQsort               bool
	usedSortCmpI64          bool
	usedSortCmpF64          bool
	usedSortCmpStr          bool
	usedSortTrampolineI64   bool
	usedSortTrampolineF64   bool
	usedSortTrampolineStr   bool
	usedSortClosGlobal      bool
	usedMapStrHelpers       bool
	usedMapNumHelpers       bool
	usedEventEmitterRuntime bool
	usedStreamRuntime       bool
	usedWStreamRuntime      bool
	usedStreamPipeRuntime   bool
	usedAwaitFetchHeaders   bool
	usedFetchBodyStream     bool
	usedHTTPStreamRuntime   bool
	usedReqBodyRuntime      bool
	usedReqBodyStream       bool
	usedReqBodyDrain        bool
	usedZlibStreamRuntime   bool
	usedZlibOneshot         bool
	usedZlibExterns         bool
	usedNodeStreamRuntime   bool
	usedPromiseAddReaction  bool
	streamSiteCtr           int
	noopCallbackEmitted     bool
	usedOSReadProcFile      bool
	usedOSCpusLinux         bool
	usedOSCpusDarwin        bool
	usedGethostname         bool
	usedSysconf             bool
	usedSysctlbyname        bool
	usedMachVM              bool
	usedExceptionHelpers    bool
	usedFrozenSet           bool
	usedPathNormalize       bool
	usedPathDirname         bool
	usedPathBasename        bool
	usedPathExtname         bool
	usedRegexCompile        bool
	usedRegexCompileContext bool
	usedRegexParseFlags     bool
	usedRegexMatch          bool
	usedRegexUTF16Convert   bool
	usedRegexUTF8Width      bool
	usedRegexESNormalize    bool
	breakStack              []string // end labels for enclosing loops / switch
	continueStack           []string // continue-target labels for enclosing loops
	// pendingFinallys is the stack of enclosing `finally` block bodies, innermost
	// last. A `return` (or `break`/`continue`) that exits a try/catch runs these
	// inline — innermost first — before its own terminator, so `finally` still
	// executes on an early exit. See emitPendingFinallys / emitTry.
	pendingFinallys [][]ast.Statement
	// breakFinallyDepth / continueFinallyDepth record len(pendingFinallys) at
	// each loop/switch entry, in lockstep with breakStack / continueStack, so a
	// `break`/`continue` runs only the finallys nested inside its target
	// loop/switch (not the ones outside it, unlike `return`). See emitBreak /
	// emitContinue and pushBreakTarget / pushContinueTarget.
	breakFinallyDepth    []int
	continueFinallyDepth []int
	// pendingLabel is set by a LabeledStatement just before emitting its body;
	// the next loop to start consumes it via pushPendingLabel. Non-loop bodies
	// leave it unconsumed, so the label is simply never registered.
	pendingLabel    string
	namedLabelStack []namedLabel // labeled break/continue targets, innermost last
	usedFree        bool
	usedExit        bool
	usedGetenv      bool
	// Async function state (reset per function, like currentRetType).
	isAsync             bool
	coroHdl             string // register holding the malloc'd promise slot
	asyncPromiseReg     string // non-suspending async fn: the settled task promise it returns (TDD-00084 Part A)
	asyncCatchLabel     string // non-suspending async fn: the catch-and-reject block label
	emittingHTTPHandler bool   // an http.listen handler arrow is being emitted — keep the old bare-slot async model (connection-fiber-driven, not task-promise), TDD-00084 Part A
	// httpResMode marks the dispatcher as Node's http.createServer `(req, res)`
	// shape (TDD-00131): the handler takes a second `res` arg and returns void;
	// the dispatcher creates `res`, calls handler(req, res), and reads the
	// accumulated response off `res` (a ServerResponseType object) — otherwise
	// identical to the bespoke return-object path.
	httpResMode bool
	// httpStreamMode marks the dispatcher as the http2 core-streams shape:
	// handler(stream, headers) with the response read off the stream object
	// (TDD-00139 Stage 2).
	httpStreamMode bool
	// httpStreamHandlerArity: 2 or 3 — whether the stream listener declares
	// Node's optional third `flags` parameter.
	httpStreamHandlerArity int
	// httpHandlerNode pins WHICH arrow/function-expression is the handler:
	// the bare-slot model applies to it alone — an async callback nested
	// inside the handler (e.g. a streaming body's pull, TDD-00097 Stage 5)
	// must get a real settled promise, or its returned slot is an 8-byte
	// never-settled sentinel that loses every reaction attached to it.
	httpHandlerNode  ast.Node
	currentPromiseTy Type   // T in Promise<T>; void if Promise<void>
	coroRetLabel     string // label for the async-return block
}

func NewEmitter() *Emitter {
	e := &Emitter{
		strConsts:            make(map[string]string),
		moduleGlobals:        make(map[string]Symbol),
		promotedGlobalDecls:  make(map[*ast.VarDeclaration]bool),
		varsBeingInitialized: make(map[string]bool),
		funcs:                make(map[string]FuncSig),
		interfaces:           make(map[string]Type),
		interfaceMethodSigs:  make(map[string]map[string]FuncSig),
		classes:              make(map[string]ClassInfo),
		enums:                make(map[string]map[string]Value),
		enumBacking:          make(map[string]Type),
		jsonToJSONActive:     make(map[string]bool),
		genericFuncs:         make(map[string]*ast.FunctionDeclaration),
		genericInterfaces:    make(map[string]*ast.InterfaceDeclaration),
		genericTypeAliases:   make(map[string]*ast.TypeAliasDeclaration),
		genericClasses:       make(map[string]*ast.ClassDeclaration),
		generators:           make(map[string]*GeneratorInfo),
		asyncGenStepFns:      make(map[string]string),
		fnValueTrampolines:   make(map[string]bool),
		testTrampolines:      make(map[string]bool),
		currentRetType:       TypeI32, // main returns i32
	}
	e.pushScope()
	return e
}

// SetMemMode selects the compile-wide memory-management mode ("manual", the
// zero-value default, or "gc"). Called by main.go right after NewEmitter()
// — not a constructor argument, so every existing zero-arg NewEmitter() call
// site (mainly tests/compiler_test.go) keeps working unchanged.
func (e *Emitter) SetMemMode(mode string) { e.memMode = mode }

// SetDynamicImportMode selects the dynamic import() backend: "eager" (default,
// TDD-00055) or "lazy" (shared-library islands, TDD-00056).
func (e *Emitter) SetDynamicImportMode(mode string) { e.dynamicImportMode = mode }

// UsesDynamicImport reports whether the emitted program contained a dynamic
// import() — for main.go's post-emit build steps.
func (e *Emitter) UsesDynamicImport() bool { return e.usesDynamicImport }

// SetIslandHash marks this compile as a shared-library island (TDD-00056) with
// the given stable hash; EmitProgram then appends the island's run-once init
// and per-export accessor functions (`__kml_dynmod_<hash>_*`) and the output is
// linked with `clang -shared` instead of as an executable.
func (e *Emitter) SetIslandHash(hash string) { e.islandHash = hash }

func (e *Emitter) isGCMode() bool { return e.memMode == "gc" }

// SetRegexMode selects the compile-wide RegExp dialect mode (TDD-00067's
// `-regex=` flag): "pcre" (raw PCRE2, today's behavior), "es-ascii" (Option
// A compile-option alignment), "es-unicode" (Option B, + PCRE2_UTF and
// NEWLINE_ANY), or "es-utf16" (es-unicode matching + UTF-16 code-unit offset
// reporting, Stage 3). The zero value "" means "unset" and resolves to the
// highest implemented ES stage — see resolveRegexMode. Called by main.go
// right after NewEmitter(), like SetMemMode, so zero-arg NewEmitter() call
// sites (tests) keep the default without threading a mode through.
func (e *Emitter) SetRegexMode(mode string) { e.regexMode = mode }

// SetBigIntBackend selects the compile-wide bigint backend library (TDD-00074),
// called by main.go from the -bigint flag. "" resolves to the default,
// libtommath (public domain); "gmp" opts into GMP.
func (e *Emitter) SetBigIntBackend(mode string) { e.bigintBackend = mode }

// BigIntBackend returns the resolved backend name ("" → the libtommath default).
func (e *Emitter) BigIntBackend() string {
	if e.bigintBackend == "" {
		return "libtommath"
	}
	return e.bigintBackend
}

// SetCryptoBackend selects the compile-wide crypto backend (TDD-00104),
// called by main.go from the -crypto flag. "" resolves to the default,
// openssl (libcrypto, all platforms); "commoncrypto" opts into Apple
// CommonCrypto + Security.framework (macOS only).
func (e *Emitter) SetCryptoBackend(mode string) { e.cryptoBackend = mode }

// CryptoBackend returns the resolved backend name ("" → the openssl default).
func (e *Emitter) CryptoBackend() string {
	if e.cryptoBackend == "" {
		return "openssl"
	}
	return e.cryptoBackend
}

// UsesCrypto reports whether the emitted program actually used crypto.subtle,
// so main.go only compiles+links a crypto backend for programs that need one.
func (e *Emitter) UsesCrypto() bool { return e.usesCrypto }

// UsesTLS reports whether the program used the `tls` module, so main.go only
// compiles tlssrc/tls.c and links libssl for programs that need it (TDD-00109).
func (e *Emitter) UsesTLS() bool { return e.usedTLS }

// UsesBigInt reports whether the emitted program actually used bigint, so
// main.go only compiles+links a backend for programs that need one.
func (e *Emitter) UsesBigInt() bool { return e.usesBigInt }

// UsesWorkers reports whether the program spawns Worker threads (TDD-00098)
// — main.go adds -pthread to the clang invocation when it does.
func (e *Emitter) UsesWorkers() bool { return e.usedWorkerRuntime }

// SetCompatMode selects the whole-program compatibility axis (TDD-00075):
// "" / "strict" (default — the compiler's opinionated, safer-than-JS
// semantics) or "js" (best-effort JS-faithful). Governs behaviors with a
// genuine strict-vs-JS tradeoff; global-shadowing (the old -globals flag) is
// handled resolver-side, so this drives the emitter-side inhabitants.
func (e *Emitter) SetCompatMode(mode string) { e.compatMode = mode }

// compatJS reports whether JS-faithful compatibility mode is active.
func (e *Emitter) compatJS() bool { return e.compatMode == "js" }

// resolveRegexMode maps the raw flag (including "" for unset) to the
// concrete mode actually used for codegen. The default resolves to the
// highest ES stage implemented so far — "ecmascript" as of Option C
// (TDD-00067): es-unicode matching plus the source-normalization pass, byte-
// indexed so it stays consistent with the string layer. es-utf16 is NOT the
// default (its UTF-16 index space intentionally diverges from .charCodeAt/
// .slice — ADR-00208); a program wanting spec-exact offsets selects it
// explicitly. This is the single place the default is defined.
func (e *Emitter) resolveRegexMode() string {
	if e.regexMode == "" {
		return "ecmascript"
	}
	return e.regexMode
}

// --- Scope ---

func (e *Emitter) pushScope() { e.scopes = append(e.scopes, scope{syms: make(map[string]Symbol)}) }
func (e *Emitter) popScope()  { e.scopes = e.scopes[:len(e.scopes)-1] }

func (e *Emitter) define(name string, sym Symbol) {
	e.scopes[len(e.scopes)-1].syms[name] = sym
}

// promoteVarToFuncScope moves a just-defined `var` binding from the innermost
// (block) scope up to the enclosing function's own scope (scopes[0]) — the
// function-scoping half of JS `var` semantics: a `var` declared inside a
// block, loop, or `if` body stays visible after that block exits, unlike a
// block-scoped `let`/`const`. Every function-like body resets e.scopes to a
// single fresh frame (emit_func.go), so scopes[0] is always the current
// function boundary and popScope only ever tears down block scopes; moving the
// binding down to scopes[0] therefore survives every intervening popScope.
// The binding is moved (not copied) so it has a single home, keeping
// updateSymbolInPlace (closure-capture boxing) correct. A `var` declared
// directly at function scope (len(scopes)==1) needs no move.
//
// This is deliberately promotion-at-declaration, not full hoisting: the
// binding becomes visible from its declaration onward, not before it. A read
// strictly before the declaration still fails as an undefined variable rather
// than yielding `undefined` (typed `var`) — matching the TypeScript
// definite-assignment view rather than sloppy-JS hoist-to-undefined. See
// ADR-00210 / TDD-00070.
func (e *Emitter) promoteVarToFuncScope(name string) {
	if len(e.scopes) <= 1 {
		return
	}
	inner := len(e.scopes) - 1
	if sym, ok := e.scopes[inner].syms[name]; ok {
		delete(e.scopes[inner].syms, name)
		e.scopes[0].syms[name] = sym
	}
}

func (e *Emitter) lookup(name string) (Symbol, bool) {
	for i := len(e.scopes) - 1; i >= 0; i-- {
		if s, ok := e.scopes[i].syms[name]; ok {
			return s, true
		}
	}
	// A top-level `const`/`let` promoted to a module global (TDD-00093) is
	// visible from any function's fresh scope — checked last, so a same-named
	// local always shadows it.
	if s, ok := e.moduleGlobals[name]; ok {
		return s, true
	}
	return Symbol{}, false
}

// isShadowedByLocal reports whether name is a real, already-declared local
// binding — checked first at every Tier 1 ambient-global dispatch site
// (Math/JSON/console/process/fetch/... — see resolver/reserved_names.go)
// before falling back to that name's built-in meaning, the same
// lookup-first-then-fallback pattern this file's identifier-evaluation path
// already used for NaN/Infinity before TDD-00050 generalized it. Only ever
// true under `-compat=js`: `-compat=strict` (the default)
// structurally guarantees no such binding can exist, since the resolver
// rejects the declaration outright — so this check is a provably-safe
// no-op in strict-mode builds, not a runtime mode switch of its own.
func (e *Emitter) isShadowedByLocal(name string) bool {
	_, ok := e.lookup(name)
	return ok
}

// updateSymbolInPlace overwrites name's entry in whichever scope currently
// holds it (rather than shadowing it in the innermost scope), so the update
// stays visible after that scope's block exits.
func (e *Emitter) updateSymbolInPlace(name string, sym Symbol) bool {
	for i := len(e.scopes) - 1; i >= 0; i-- {
		if _, ok := e.scopes[i].syms[name]; ok {
			e.scopes[i].syms[name] = sym
			return true
		}
	}
	return false
}

// --- Name generation ---

func (e *Emitter) freshReg() string {
	n := e.regCtr
	e.regCtr++
	return fmt.Sprintf("%%t%d", n)
}

func (e *Emitter) freshLabel(prefix string) string {
	n := e.labelCtr
	e.labelCtr++
	return fmt.Sprintf("%s.%d", prefix, n)
}

// --- Emission helpers ---

func (e *Emitter) emitGlobal(line string) { e.globals.WriteString(line + "\n") }
func (e *Emitter) emitAlloca(line string) { e.allocas.WriteString("  " + line + "\n") }
func (e *Emitter) emitInstr(line string) {
	if e.blockDone {
		return // skip dead code after a terminator
	}
	e.body.WriteString("  " + line + "\n")
}

// emitTerminator emits a terminator instruction and marks the block as done.
func (e *Emitter) emitTerminator(line string) {
	if e.blockDone {
		return
	}
	e.body.WriteString("  " + line + "\n")
	e.blockDone = true
}

// emitLabel starts a new basic block, resetting the terminator flag.
func (e *Emitter) emitLabel(label string) {
	e.body.WriteString(label + ":\n")
	e.blockDone = false
}

// --- String constants ---

func (e *Emitter) internString(s string) string {
	if ref, ok := e.strConsts[s]; ok {
		return ref
	}
	name := fmt.Sprintf("@.s%d", e.strIdx)
	e.strIdx++
	esc, length := escapeLLVM(s)
	dataLen := length - 1 // escapeLLVM's length includes the trailing NUL
	// TDD-00120 Stage 2: a string literal carries the same 8-byte length header
	// as a heap string — `{ i64 len, [N x i8] bytes }` — and the string *value*
	// is a pointer to the bytes (field 1), so `__kml_str_len(ptr)` reads the true
	// length from ptr-8 uniformly for literals and heap strings alike. The bytes
	// stay NUL-terminated, so every strlen consumer keeps working meanwhile.
	e.emitGlobal(fmt.Sprintf("%s = private unnamed_addr constant { i64, [%d x i8] } { i64 %d, [%d x i8] c\"%s\" }, align 8", name, length, dataLen, length, esc))
	ref := fmt.Sprintf("getelementptr inbounds ({ i64, [%d x i8] }, ptr %s, i32 0, i32 1)", length, name)
	e.strConsts[s] = ref
	return ref
}

// --- Link flags ---

// requireLink marks that the compiled program needs an external, non-libc
// library at link time (e.g. "curl" for -lcurl). Every C dependency before
// fetch (malloc, sscanf, gmtime, …) was plain libc, implicitly linked by
// clang's default driver behavior with no extra flag needed — fetch is the
// first feature that needs anything beyond that, so this is a new,
// deliberately general mechanism (not a one-off special case for curl):
// the next native-library-backed feature (WebSocket, crypto.subtle, …) just
// calls this too. main.go reads LinkLibs() after EmitProgram and only adds
// -l<lib> flags for libraries a given program actually ended up using.
func (e *Emitter) requireLink(lib string) {
	if e.linkLibs == nil {
		e.linkLibs = map[string]bool{}
	}
	e.linkLibs[lib] = true
}

// LinkLibs returns the external libraries this program's compiled code
// needs, sorted for a reproducible build command.
func (e *Emitter) LinkLibs() []string {
	if len(e.linkLibs) == 0 {
		return nil
	}
	libs := make([]string, 0, len(e.linkLibs))
	for lib := range e.linkLibs {
		libs = append(libs, lib)
	}
	sort.Strings(libs)
	return libs
}

func escapeLLVM(s string) (string, int) {
	var b strings.Builder
	n := 0
	for _, c := range []byte(s) {
		switch c {
		case '\n':
			b.WriteString("\\0A")
		case '\r':
			b.WriteString("\\0D")
		case '\t':
			b.WriteString("\\09")
		case '"':
			b.WriteString("\\22")
		case '\\':
			b.WriteString("\\5C")
		default:
			if c < 32 || c > 126 {
				b.WriteString(fmt.Sprintf("\\%02X", c))
			} else {
				b.WriteByte(c)
			}
		}
		n++
	}
	b.WriteString("\\00")
	return b.String(), n + 1 // +1 for null terminator
}

// --- Type resolution ---

// resolveValueType finds the type of a value binding by name — an in-scope
// local, a module global, a top-level function (its function type), or, as a
// fallback usable before value scope is populated (eager type-alias
// registration), the value's own top-level `const`/`let`/`var` declaration in
// the program AST (its annotation, else its inferred initializer type).
func (e *Emitter) resolveValueType(name string) (Type, bool) {
	// Cycle guard: a self-referential `typeof` (e.g. `function f(): typeof f`, or
	// a mutually-referential pair) would otherwise recurse forever through
	// buildFunctionSig → resolveType → resolveValueType. Bail if already
	// resolving this name.
	if e.typeofResolving[name] {
		return Type{}, false
	}
	if e.typeofResolving == nil {
		e.typeofResolving = map[string]bool{}
	}
	e.typeofResolving[name] = true
	defer delete(e.typeofResolving, name)
	// A top-level declaration is checked first, from the AST, so `typeof value`
	// resolves to the *same* type whether it runs at eager registration (before
	// value scope exists) or at emit time — a promoted global's stored handle
	// type can differ from its structural shape, which would desync the two
	// passes. (A function-local of the same name shadowing a top-level binding is
	// a rare case not distinguished here.)
	if e.prog != nil {
		match := func(vd *ast.VarDeclaration) (Type, bool) {
			// Top-level names carry the resolver's per-file `__kml_mod<N>`
			// mangling suffix, while a `typeof value` query keeps the source
			// name — compare against the unmangled declaration name.
			declName := vd.Name
			if i := strings.LastIndex(declName, "__kml_mod"); i > 0 {
				declName = declName[:i]
			}
			if declName != name {
				return Type{}, false
			}
			if vd.TypeAnnot != nil {
				return e.resolveType(vd.TypeAnnot), true
			}
			if vd.Init != nil {
				return e.inferExprType(vd.Init), true
			}
			return Type{}, false
		}
		for _, st := range e.prog.Body {
			switch d := st.(type) {
			case *ast.VarDeclaration:
				if ty, ok := match(d); ok {
					return ty, true
				}
			case *ast.VarDeclarationList:
				for _, vd := range d.Decls {
					if ty, ok := match(vd); ok {
						return ty, true
					}
				}
			case *ast.FunctionDeclaration:
				declName := d.Name
				if i := strings.LastIndex(declName, "__kml_mod"); i > 0 {
					declName = declName[:i]
				}
				if declName == name && len(d.TypeParams) == 0 {
					sig := e.buildFunctionSig(d)
					return FuncType(sig.ParamTypes, sig.RetType), true
				}
			}
		}
	}
	if sym, ok := e.lookup(name); ok {
		return sym.Ty, true
	}
	if sym, ok := e.moduleGlobals[name]; ok {
		return sym.Ty, true
	}
	if _, sig, ok := e.resolveFuncRef(name); ok {
		return FuncType(sig.ParamTypes, sig.RetType), true
	}
	return Type{}, false
}

// resolveTypeofType resolves a `typeof value` type query (ADR-00389) to the
// referenced value's type, walking any trailing member path. An unresolvable
// query falls back to the number default rather than failing (resolveType has
// no error channel).
func (e *Emitter) resolveTypeofType(ta *ast.TypeAnnotation) Type {
	base, ok := e.resolveValueType(ta.TypeofName)
	if !ok {
		return TypeI64
	}
	for _, seg := range ta.TypeofPath {
		next, found := Type{}, false
		for _, f := range base.Fields {
			if f.Name == seg {
				next, found = f.Ty, true
				break
			}
		}
		if !found {
			return TypeI64
		}
		base = next
	}
	return base
}

// indexSignatureType builds the map-backed dynamic-object type for a string
// index signature `{ [k: string]: V }` (TDD-00130), matching
// inferDynamicObjectType's shape so the shared read/write/iterate machinery
// (TDD-00012) applies unchanged.
func (e *Emitter) indexSignatureType(valAnnot *ast.TypeAnnotation) Type {
	keyTy := TypePtr
	valTy := e.resolveType(valAnnot)
	return Type{IR: "ptr", IsMap: true, IsDynamicObject: true, MapKey: &keyTy, MapVal: &valTy}
}

func (e *Emitter) resolveType(ta *ast.TypeAnnotation) Type {
	if ta == nil {
		return TypeI64 // default for untyped numeric variables
	}
	// Depth guard against a self-referential type (`type T = { next: T }`, a
	// recursive generic alias, a recursive index signature) — a fixed-shape
	// struct can't represent an infinitely-recursive type anyway, so bail to a
	// generic pointer rather than overflowing the stack. The limit is far beyond
	// any real type nesting.
	e.resolveTypeDepth++
	defer func() { e.resolveTypeDepth-- }()
	if e.resolveTypeDepth > 300 {
		return TypePtr
	}
	// `typeof value` type query (ADR-00389): resolve the referenced value's type,
	// then walk any trailing member path (`typeof a.b`).
	if ta.IsTypeof {
		return e.resolveTypeofType(ta)
	}
	// String-literal type (`"north"`, TDD-00079): as a value type it is just a
	// string; the literal value is only consumed at the AST level by
	// Pick/Omit/Record key collection, not here.
	// Numeric-literal type (`1`, ADR-00459): as a value type it is just a
	// number, mirroring the string-literal stance below.
	if ta.IsNumberLiteral {
		return TypeF64
	}
	if ta.IsStringLiteral {
		// String-shaped, but carry the literal value so a discriminated-union
		// tag field can be matched at compile time (TDD-00116). Behaves as
		// string everywhere else (isStringTy checks IR "ptr").
		return Type{IR: "ptr", IsStrLiteral: true, LitValue: ta.LiteralValue}
	}
	// keyof / indexed access / mapped types (TDD-00079 Stage 2) — each evaluates
	// to a concrete type off a concrete operand.
	if ta.IsKeyof {
		return e.resolveKeyof(ta)
	}
	if ta.IsIndexedAccess {
		return e.resolveIndexedAccess(ta)
	}
	if ta.IsMapped {
		return e.resolveMappedType(ta)
	}
	if ta.IsConditional {
		return e.resolveConditionalType(ta)
	}
	// General union (T | U | ..., TDD-00043): reuses any/unknown's runtime
	// { i8, i64 } box (TypeAny's IR) but additionally carries the resolved
	// member set, so it stays distinguishable from — and, unlike — bare
	// any/unknown at every checkpoint that currently rejects IsDynamic
	// outright (see isUnconstrainedDynamic, emit_dynamic.go). Checked before
	// every other branch below since a union member can itself be any of the
	// shapes those branches handle (though V1 only actually permits
	// number/string/boolean members — see validateUnionMembers, called by
	// this Type's actual usage sites, not here, since resolveType has no
	// error return).
	if ta.UnionMembers != nil {
		members := make([]Type, len(ta.UnionMembers))
		for i, m := range ta.UnionMembers {
			members[i] = e.resolveType(m)
		}
		return Type{IR: TypeAny.IR, IsDynamic: true, UnionMembers: members, Nullable: ta.Nullable}
	}
	// Intersection (A & B & ..., TDD-00078): collapses to one ObjectType whose
	// fields are the merged fields of every member — no runtime tag, unlike a
	// union. Checked here (before the Fields/Name branches below) because the
	// head-copy an intersection carries still has the first member's Name and
	// possibly its Fields set. mergeIntersectionFields can fail (a non-object
	// member, or a conflicting field type); resolveType has no error return, so
	// the failure is re-detected at each use site by validateIntersectionMembers
	// — here the merge just yields whatever fields it produced (nil on error),
	// keeping the result a well-formed object type that never crashes codegen.
	if ta.IntersectionMembers != nil {
		members := make([]Type, len(ta.IntersectionMembers))
		for i, m := range ta.IntersectionMembers {
			members[i] = e.resolveType(m)
		}
		fields, _ := mergeIntersectionFields(members)
		obj := ObjectType(fields)
		obj.IntersectionMembers = members
		obj.Nullable = ta.Nullable
		return obj
	}
	if ta.IsFuncType {
		params := make([]Type, len(ta.FuncParams))
		for i := range ta.FuncParams {
			params[i] = e.resolveType(&ta.FuncParams[i])
		}
		ret := TypeVoid
		if ta.FuncRetType != nil {
			ret = e.resolveType(ta.FuncRetType)
		}
		ft := FuncType(params, ret)
		ft.FuncHasRest = ta.FuncHasRest
		return ft
	}
	// Promise<T>/Map<K,V>/Set<T> must be checked before the generic ElemType
	// fallback below (which treats any non-nil ElemType as a plain array —
	// correct for T[] and Array<T>, wrong for these three).
	if ta.Name == "Promise" {
		inner := TypeVoid
		if ta.ElemType != nil {
			inner = e.resolveType(ta.ElemType)
		}
		pt := PromiseOf(inner)
		// Every promise this compiler produces is now task-shaped — async
		// functions and methods alike (TDD-00087 follow-up unified async methods
		// onto the task-struct model) — except a raw fetch's `Promise<Response>`.
		// So a declared `Promise<T>` annotation is task-shaped, and `const p:
		// Promise<T> = f(); await p` reads the right slot.
		if !inner.IsResponse {
			pt.PromiseTask = true
		}
		return pt
	}
	if ta.Name == "Map" && ta.ElemType != nil {
		keyTy := TypePtr
		if ta.KeyType != nil {
			keyTy = e.resolveType(ta.KeyType)
		}
		valTy := e.resolveType(ta.ElemType)
		return MapType(keyTy, valTy)
	}
	// A registered generic interface (TDD-00010 V1 / TDD-00037), e.g.
	// Box<number> or Box<number, string> — must also be checked before the
	// generic ElemType fallback below, same reasoning as
	// Promise/Map/Set/EventEmitter above.
	if genDecl, ok := e.genericInterfaces[ta.Name]; ok && len(ta.TypeArgs) > 0 {
		// Depth cap for infinitely expanding generics (`interface Foo<T> {
		// x: Foo<Foo<T>> }`) — TS handles these lazily, this eager
		// instantiation would recurse forever (observed as a real oracle
		// hang). Past the cap the remaining tail becomes an opaque empty
		// object type; a program that never touches fields that deep is
		// unaffected. The cap is 8, not deeper: expansion is eager, so a
		// doubly-recursive type costs O(2^depth) type nodes.
		if e.genericDepth > 8 {
			return ObjectType(nil)
		}
		e.genericDepth++
		t := e.instantiateGenericInterface(genDecl, e.buildTypeArgSubs(genDecl.TypeParams, ta.TypeArgs))
		e.genericDepth--
		return t
	}
	// Generic type alias `type Name<T> = ...` (TDD-00079 Stage 3): substitute the
	// concrete type arguments into the alias body annotation, then resolve it.
	if aliasDecl, ok := e.genericTypeAliases[ta.Name]; ok && len(ta.TypeArgs) > 0 {
		// Same infinite-expansion depth cap as the generic-interface branch.
		if e.genericDepth > 8 {
			return ObjectType(nil)
		}
		subs := make(map[string]*ast.TypeAnnotation, len(aliasDecl.TypeParams))
		for i, param := range aliasDecl.TypeParams {
			if i < len(ta.TypeArgs) {
				subs[param] = ta.TypeArgs[i]
			}
		}
		e.genericDepth++
		t := e.resolveType(substituteAnnotation(aliasDecl.Type, subs))
		e.genericDepth--
		return t
	}
	// Built-in utility types (TDD-00079) — after the user-generic registry so a
	// user-defined type of the same name still wins, before the generic ElemType
	// fallback below (which would otherwise misread `Partial<User>` as `User[]`).
	if len(ta.TypeArgs) > 0 && utilityTypeNames[ta.Name] {
		if ty, ok := e.resolveUtilityType(ta.Name, ta.TypeArgs); ok {
			return ty
		}
	}
	if ta.Name == "Set" && ta.ElemType != nil {
		return SetType(e.resolveType(ta.ElemType))
	}
	// WeakMap<K,V> / WeakSet<T> / WeakRef<T> annotations (TDD-00112). The type
	// args arrive via TypeArgs (the generic path) since these names aren't
	// special-cased in the type-annotation parser the way Map/Set are; a bare
	// form (no args) falls back to object-key / number-value defaults.
	if ta.Name == "WeakMap" {
		keyTy, valTy := TypePtr, TypeI64
		if len(ta.TypeArgs) >= 1 {
			keyTy = e.resolveType(ta.TypeArgs[0])
		}
		if len(ta.TypeArgs) >= 2 {
			valTy = e.resolveType(ta.TypeArgs[1])
		}
		return WeakMapType(keyTy, valTy)
	}
	if ta.Name == "WeakSet" {
		elemTy := TypePtr
		if len(ta.TypeArgs) >= 1 {
			elemTy = e.resolveType(ta.TypeArgs[0])
		} else if ta.ElemType != nil {
			elemTy = e.resolveType(ta.ElemType)
		}
		return WeakSetType(elemTy)
	}
	if ta.Name == "WeakRef" {
		referentTy := TypePtr
		if len(ta.TypeArgs) >= 1 {
			referentTy = e.resolveType(ta.TypeArgs[0])
		} else if ta.ElemType != nil {
			referentTy = e.resolveType(ta.ElemType)
		}
		return WeakRefType(referentTy)
	}
	// MessagePort<T> (TDD-00099) — the worker-side annotation for a port
	// received through workerData/postMessage.
	if ta.Name == "MessagePort" {
		if ta.ElemType != nil {
			return MessagePortType(e.resolveType(ta.ElemType))
		}
		if len(ta.TypeArgs) > 0 {
			return MessagePortType(e.resolveType(ta.TypeArgs[0]))
		}
		return MessagePortType(TypeI64)
	}
	if ta.Name == "EventEmitter" && ta.ElemType != nil {
		return EventEmitterType(e.resolveEventEmitterPayloadType(ta.ElemType))
	}
	// ReadableStream<T> and its reader/controller (TDD-00097 Stage 1). A bare
	// `ReadableStream` annotation (no type arg) defaults its chunk to number,
	// same as `new ReadableStream(...)` without a type argument.
	if ta.Name == "ReadableStream" {
		if ta.ElemType != nil {
			return ReadableStreamType(e.resolveType(ta.ElemType))
		}
		return ReadableStreamType(TypeI64)
	}
	if ta.Name == "ReadableStreamDefaultReader" && ta.ElemType != nil {
		return StreamReaderType(e.resolveType(ta.ElemType))
	}
	if ta.Name == "ReadableStreamDefaultController" && ta.ElemType != nil {
		return RSControllerType(e.resolveType(ta.ElemType))
	}
	if ta.Name == "WritableStream" {
		if ta.ElemType != nil {
			return WritableStreamType(e.resolveType(ta.ElemType))
		}
		return WritableStreamType(TypeI64)
	}
	if ta.Name == "WritableStreamDefaultWriter" && ta.ElemType != nil {
		return WSWriterType(e.resolveType(ta.ElemType))
	}
	if ta.Name == "WritableStreamDefaultController" && ta.ElemType != nil {
		return WSControllerType(e.resolveType(ta.ElemType))
	}
	if ta.Name == "TransformStream" {
		inTy, outTy := TypeI64, TypeI64
		if ta.KeyType != nil {
			inTy = e.resolveType(ta.KeyType)
		}
		if ta.ElemType != nil {
			outTy = e.resolveType(ta.ElemType)
		}
		i, o := inTy, outTy
		return Type{IR: "ptr", IsTransformStream: true, StreamChunk: &i, StreamOut: &o}
	}
	// Tuple type `[T0, T1, ...]` (TDD-00066) — checked before the generic
	// ElemType/array fallback below.
	if len(ta.TupleElems) > 0 {
		elems := make([]Type, len(ta.TupleElems))
		for i, et := range ta.TupleElems {
			elems[i] = e.resolveType(et)
		}
		return TupleType(elems)
	}
	if ta.ElemType != nil {
		return ArrayOf(e.resolveType(ta.ElemType))
	}
	// A string index signature `{ [k: string]: V }` (TDD-00130) resolves to the
	// existing map-backed dynamic-object representation (TDD-00012), giving it
	// `d[key]` read/write sugar and map iteration for free.
	if ta.IndexSig != nil {
		return e.indexSignatureType(ta.IndexSig)
	}
	if len(ta.Fields) > 0 {
		fields := make([]Field, len(ta.Fields))
		for i, af := range ta.Fields {
			fields[i] = Field{Name: af.Name, Ty: e.resolveType(af.Type)}
		}
		return ObjectType(fields)
	}

	// Named type: check interface registry before falling back to built-ins.
	name := ta.Name
	// Handle T[] where T is a named interface (e.g. "User[]") or enum
	// ("Color[]") — the parser hands a named-type array as a single "[]"-suffix
	// name, so ElemType is nil and the enum-backing lookup below wouldn't fire.
	if len(name) > 2 && name[len(name)-2:] == "[]" {
		base := name[:len(name)-2]
		if ty, ok := e.interfaces[base]; ok {
			return ArrayOf(ty)
		}
		if backing, ok := e.enumBacking[base]; ok {
			return ArrayOf(backing)
		}
	}
	if ty, ok := e.interfaces[name]; ok {
		if ta.Nullable {
			ty.Nullable = true
		}
		return ty
	}
	// A named enum used as a type annotation resolves to its backing value type
	// — i64 for a numeric enum, ptr for a string enum. Without this a string
	// enum annotation fell through to the i64 unknown-name default, so storing
	// a member's string pointer into the i64 slot was a clang type error.
	if backing, ok := e.enumBacking[name]; ok {
		if ta.Nullable {
			backing.Nullable = true
		}
		return backing
	}
	// An error kind used as a type annotation (`e: Error`, `err: TypeError`)
	// resolves to the shared error object shape ({kind, message, name}), so
	// `.message`/`.name` (and `AggregateError`'s `.errors`) work — most useful on a
	// `.catch`/reject callback's parameter, which receives the thrown error object.
	if _, ok := errorKindIDs[name]; ok {
		ty := errorObjType
		if ta.Nullable {
			ty.Nullable = true
		}
		return ty
	}
	ty := ResolveTypeName(ta.Name)
	if ta.Nullable {
		ty.Nullable = true
	}
	return ty
}

// --- Top-level entry ---

// EmitProgram generates LLVM IR for an entire program (script-style: top-level → main).
// rewriteTopLevelClassExpressions turns each top-level `const/let/var X =
// class {...}` into a nominal `class X {...}` declaration in place (TDD-00063
// Stage 4), so all the existing class-registration/emit machinery applies
// unchanged. Only a single-declarator VarDeclaration is rewritten; a class
// expression anywhere else (a multi-declarator list, a nested/non-top-level
// binding, an argument or return value, an `export`-wrapped binding) is left
// as a ClassExpression node and cleanly rejected when it reaches emitExpr.
// The LHS name always wins: a named class expression's own name (`class D
// {...}`) is dropped, so a body self-reference to it is a clean "unknown
// class" error — the self-reference subset is a deferred V1 cut.
func (e *Emitter) rewriteTopLevelClassExpressions(prog *ast.Program) {
	for i, stmt := range prog.Body {
		vd, ok := stmt.(*ast.VarDeclaration)
		if !ok {
			continue
		}
		ce, ok := vd.Init.(*ast.ClassExpression)
		if !ok {
			continue
		}
		ce.Decl.Name = vd.Name
		prog.Body[i] = ce.Decl
	}
}

// rewriteTopLevelGeneratorExpressions rewrites each top-level `const/let/var
// G = function* (...) {...}` (sync or async) into a named generator
// FunctionDeclaration (TDD-00096 Part 1) — the exact precedent
// rewriteTopLevelClassExpressions set: a bound generator expression is
// observationally a declaration under another name. Any *other* use of a
// generator expression (argument, nested binding, IIFE) is rejected at its
// emission site instead.
func (e *Emitter) rewriteTopLevelGeneratorExpressions(prog *ast.Program) {
	for i, stmt := range prog.Body {
		vd, ok := stmt.(*ast.VarDeclaration)
		if !ok {
			continue
		}
		fe, ok := vd.Init.(*ast.FunctionExpression)
		if !ok || !fe.IsGenerator {
			continue
		}
		fd := &ast.FunctionDeclaration{
			Name:        vd.Name,
			Params:      fe.Params,
			ReturnType:  fe.RetType,
			Body:        fe.Body,
			IsAsync:     fe.IsAsync,
			IsGenerator: true,
		}
		fd.SetPos(vd.GetPos())
		prog.Body[i] = fd
	}
}

func (e *Emitter) EmitProgram(prog *ast.Program) (string, error) {
	// TDD-00128: enforce `/** @pure */` before any codegen. A front-end-only
	// check with zero effect on the emitted IR — a violation is a compile error.
	if err := e.checkPurity(prog); err != nil {
		return "", err
	}
	// Retained so a `typeof value` type query can resolve the referenced value's
	// type from its top-level declaration even at eager type-alias registration
	// (Pass 0), before module globals are bound.
	e.prog = prog
	// TS namespaces (TDD-00095): the parser desugared members into flat
	// mangled declarations; the member table is what use sites resolve
	// through (emitCall/emitMember/inferExprType).
	e.namespaces = prog.Namespaces
	e.registerNSAliases(prog.NSAliases)

	// TDD-00098: known before any emission (the resolver recorded worker
	// modules), so every gc-fiber emission site can pick the thread-aware
	// stackbottom mechanism up front.
	e.hasWorkers = len(prog.WorkerModules) > 0

	// Pass -2: rewrite each top-level `const/let/var X = class {...}` binding
	// into a nominal `class X {...}` declaration (TDD-00063 Stage 4), before
	// any registration runs — a class expression is not a runtime value here,
	// so binding it is the same as declaring the class under the LHS name.
	e.rewriteTopLevelClassExpressions(prog)
	e.rewriteTopLevelGeneratorExpressions(prog)

	// Pass -1: register enums so members are available as constants everywhere.
	e.registerEnums(prog)

	// Pass -0.5: register a name-only placeholder type for every non-generic
	// class BEFORE interfaces resolve their fields, so an interface/type-alias
	// field typed as a class (`interface W { p: Point }`) resolves to the class's
	// ptr type rather than the i64 unknown-name default (which silently
	// mis-stored the instance pointer). The full class type is filled in by
	// registerClasses below; field access canonicalizes the placeholder to it.
	e.registerClassNamePlaceholders(prog)

	// Pass 0: register interfaces and type aliases so they're available to function signatures.
	e.registerInterfaces(prog)

	// Pass 0.5: register classes (fields/layout + constructor/method
	// signatures) — after interfaces (a class field/param/return may
	// reference one) and before functions (a function signature may
	// reference a class by name).
	if err := e.registerClasses(prog); err != nil {
		return "", err
	}

	// Pass 1: register all top-level function signatures so calls work regardless of order.
	if err := e.registerFunctions(prog); err != nil {
		return "", err
	}
	// Mark async functions that can actually suspend (TDD-00083 Stage 2), so the
	// emitter compiles them as coroutine tasks; the rest keep the synchronous
	// malloc-slot fast path. Must run after registerFunctions (needs e.funcs).
	e.classifyAsyncSuspension(prog)

	// Pass 1.7: promote top-level simple `const`/`let`/`var` to module globals
	// (TDD-00093) so the function bodies emitted next can read them (a named
	// function has its own fresh scope and can't see a `main()` local). Must run
	// before Pass 2 emits any function body.
	e.registerModuleGlobals(prog)

	// Pass 1.8: pre-register `const { subtle } = globalThis.crypto` aliases
	// (ADR-00434) so function bodies emitted in Pass 2 — before the top-level
	// destructuring statement itself is reached in Pass 3 — dispatch
	// `subtle.digest(...)` correctly.
	e.registerCryptoSubtleAliases(prog)

	// Pass 2: emit each function declaration. A V1 (monomorphized) generic
	// function is never emitted here — it has no single concrete signature
	// to emit; each concrete instantiation is emitted on demand from its own
	// call site instead (emit_generics.go), and a generic function that's
	// never called is simply never emitted at all. A V2 (`@erased`) generic
	// function is the opposite: it has exactly one signature (already
	// registered into e.funcs by registerFunctions above), so it's emitted
	// here unconditionally, same as a plain function.
	// A redeclared function name (legal JS re-declaration — including a
	// run of `declare function f(…);` ambient signatures, ADR-00471) emits
	// only its *last* declaration, matching JS's last-wins hoisting;
	// emitting each would be an LLVM redefinition error.
	lastDeclFor := map[string]ast.Statement{}
	for _, stmt := range prog.Body {
		if fd, ok := stmt.(*ast.FunctionDeclaration); ok {
			lastDeclFor[fd.Name] = stmt
		}
	}
	for _, stmt := range prog.Body {
		fd, ok := stmt.(*ast.FunctionDeclaration)
		if !ok {
			continue
		}
		if lastDeclFor[fd.Name] != stmt {
			continue
		}
		if fd.IsGenerator {
			if err := e.emitGeneratorFunctionDecl(fd, e.generators[fd.Name]); err != nil {
				return "", err
			}
			continue
		}
		if len(fd.TypeParams) == 0 || fd.Erased {
			if err := e.emitFunctionDecl(fd); err != nil {
				return "", err
			}
		}
	}

	// Pass 2b: emit each class's constructor and methods. A generic class
	// (TDD-00010 V1) is never emitted here — like a generic function, it has
	// no single concrete shape to emit; each concrete instantiation is
	// emitted on demand from its own `new ClassName<T>(...)` site instead
	// (emit_generics.go).
	for _, stmt := range prog.Body {
		if cd, ok := stmt.(*ast.ClassDeclaration); ok && len(cd.TypeParams) == 0 {
			if err := e.emitClassDecl(cd); err != nil {
				return "", err
			}
		}
	}

	// Pass 2c: emit vtable globals for classes needing dynamic dispatch
	// (TDD-00009 Stage 3) — a no-op per class outside a HasVTable tree.
	// Also emit each class's own static field globals (Stage 4) and, for a
	// class with static {} block(s), its @ClassName_staticinit function —
	// called once, in declaration order, at the very start of Pass 3 below.
	var staticInitClasses []string
	for _, stmt := range prog.Body {
		cd, ok := stmt.(*ast.ClassDeclaration)
		if !ok || len(cd.TypeParams) > 0 {
			continue
		}
		e.emitClassVTable(cd.Name)
		e.emitClassStaticFieldGlobals(cd.Name)
		if len(cd.StaticBlocks) > 0 || classHasStaticFieldInit(cd) {
			if err := e.emitClassStaticInit(cd); err != nil {
				return "", err
			}
			staticInitClasses = append(staticInitClasses, cd.Name)
		}
	}

	// Pass 2d (TDD-00098): emit every worker module's entry function before
	// main — the channel types recorded during worker emission (parentPort
	// handler annotations, workerData annotation, postMessage payload
	// types) gate every parent-side Worker use emitted in Pass 3.
	if err := e.emitWorkerModules(prog); err != nil {
		return "", err
	}

	// Pass 3: emit remaining statements into main().
	// process.argv is backed by two globals set from main's own argc/argv
	// parameters, so any expression (top-level code, or any function/closure)
	// can read it later without needing to be threaded through explicitly.
	e.emitGlobal("@__argv_ptr = internal global ptr null, align 8")
	e.emitGlobal("@__argv_len = internal global i64 0, align 8")
	argc64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i32 %%argc to i64", argc64))
	// TDD-00120: the OS argv[] entries are foreign char* with no length header;
	// copy them into length-prefixed strings so process.argv[i] behaves like every
	// other string (its .length reads the header, not strlen).
	e.ensureStrHeaderRuntime()
	argvHdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_argv_headerize(i64 %s, ptr %%argv)", argvHdr, argc64))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__argv_ptr, align 8", argvHdr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr @__argv_len, align 8", argc64))

	// gc mode: snapshot Boehm's GC_stackbottom (the process's real stack
	// base, already valid here since the gcshim's constructor-attribute
	// GC_INIT() always runs before main()'s first instruction) so the
	// swapcontext sites in runtime.go can restore it after temporarily
	// repointing it at a fiber's own stack while that fiber runs — see
	// docs/adr/ADR-00071.md for why this is needed.
	if e.isGCMode() {
		e.emitGlobal("@GC_stackbottom = external global ptr")
		// thread_local: under Worker threads each thread restores to ITS OWN
		// stack bottom, never the main thread's (TDD-00098 stage 4). The
		// worker trampoline fills in each worker thread's value; main's is
		// snapshotted here. Harmless TLS for the single-threaded case too.
		e.emitGlobal("@__kml_gc_orig_stackbottom = internal thread_local global ptr null, align 8")
		origReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @GC_stackbottom, align 8", origReg))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_gc_orig_stackbottom, align 8", origReg))
		if e.hasWorkers {
			// Thread-aware stackbottom mechanism (validated by direct .ll
			// prototype on darwin/arm64): instead of storing through the
			// process-wide @GC_stackbottom, every fiber swap site calls
			// @__kml_gc_set_sb, which updates the CURRENT thread's recorded
			// stack bottom under the GC allocation lock.
			e.emitGlobal("declare void @GC_set_stackbottom(ptr noundef, ptr noundef)")
			e.emitGlobal("declare ptr @GC_call_with_alloc_lock(ptr noundef, ptr noundef)")
			e.emitGlobal("declare void @GC_allow_register_threads()")
			e.emitGlobal(`define ptr @__kml_gc_set_sb_locked(ptr %mem) {
entry:
  %sb = alloca [2 x ptr], align 8
  %slot = getelementptr [2 x ptr], ptr %sb, i32 0, i32 0
  store ptr %mem, ptr %slot, align 8
  call void @GC_set_stackbottom(ptr null, ptr %sb)
  ret ptr null
}`)
			e.emitGlobal(`define void @__kml_gc_set_sb(ptr %mem) {
entry:
  call ptr @GC_call_with_alloc_lock(ptr @__kml_gc_set_sb_locked, ptr %mem)
  ret void
}`)
			e.emitInstr("call void @GC_allow_register_threads()")
		}
	}

	// TDD-00009 Stage 4: run every class's static {} block(s) once, in
	// declaration order, before any of the entry file's own top-level
	// statements — so top-level code reading a static field always sees
	// its initialized value.
	for _, name := range staticInitClasses {
		e.emitInstr(fmt.Sprintf("call void @%s_staticinit()", name))
	}

	for _, stmt := range prog.Body {
		if _, ok := stmt.(*ast.FunctionDeclaration); ok {
			continue
		}
		if err := e.emitStmt(stmt); err != nil {
			return "", err
		}
	}
	// If the program ever constructed an EventSource, prefer the full
	// __kml_event_loop_run() over the narrower __kml_timer_drain() below —
	// it already generalizes plain timer draining (see its own doc comment
	// in runtime_http.go) while also driving libcurl's multi-interface and
	// the EventSource scan every iteration, keeping the process alive for
	// as long as any EventSource is still open (TDD-00038 Stage 0). If the
	// program also called http.listen(...), that call's own inline
	// __kml_event_loop_run() invocation already terminates this block
	// (never returns), making this one dead code — same "skipped via
	// emitInstr's own dead-code check" reasoning the usedTimers branch below
	// already relies on.
	//
	// Otherwise, if the program ever called setTimeout/setInterval/
	// clearTimeout/clearInterval, drain any still-pending timers after the
	// top-level script finishes — the same place real Node keeps the
	// process alive for. Skipped entirely (via emitInstr's own dead-code
	// check) if the last top-level statement already terminated the block,
	// e.g. process.exit() — matching real Node, which also never drains
	// pending timers after an explicit exit.
	// Microtasks run after the top-level synchronous script, before any timer or
	// task macrotask (TDD-00083 Stage 3).
	if e.usedMicrotasks {
		e.emitInstr("call void @__kml_drain_microtasks()")
	}
	// TDD-00084 Part B: a program mixing coroutine tasks with timers or
	// EventSource/WebSocket drives all of them under the single task-aware event
	// loop (__kml_event_loop_run). A pure-task program (fetch only) keeps the
	// lighter task_run_all drive; a pure-timer program keeps timer_drain.
	// TDD-00098: a program that spawned workers must keep driving the full
	// loop — it is what delivers worker messages and joins exited workers.
	useFullLoop := e.usedEventSource || e.usedWSClient || (e.usedTaskRuntime && e.usedTimers) || e.usedWorkerRuntime || e.usedChanRuntime || e.usedChildProcRuntime || e.usedReadlineRuntime || e.usedStdinRuntime || e.usedNetRuntime || e.usedDgramRuntime || e.usedIPCChildRuntime || e.usedHTTPListen
	if useFullLoop {
		e.ensureHTTPRuntime() // emit event_loop_run + every symbol it references
		e.emitInstr("call void @__kml_event_loop_run()")
		if e.usedHTTPListen {
			// Non-blocking Node listen defers its event loop to here — flush the
			// http-client reaction hook after it, exactly as an inline listen
			// loop did (emit_http.go's emitPostLoopFlush).
			e.emitPostLoopFlush()
		}
	} else if e.usedTaskRuntime {
		// A may-suspend async program with no timers/SSE/WS: drain the coroutine
		// scheduler until all spawned tasks complete.
		e.emitInstr("call void @__kml_task_run_all()")
	} else if e.usedTimers {
		e.emitInstr("call void @__kml_timer_drain()")
	}
	// TDD-00084 Part B: if the event loop was emitted but the task/microtask
	// runtimes were not, define no-op stubs for the symbols it references.
	e.emitLoopTaskStubs()
	e.emitCPRuntimeStubs()
	// TDD-00109: the net runtime's read/write/close branch on a socket's SSL*
	// and call the __kml_tls_* ABI. Declare it (libssl provides it via tlssrc/
	// tls.c) when `tls` is used, else define no-op stubs so a plain-net program
	// links — its sockets always have a null SSL*, so the stubs are never run.
	e.emitTLSNetSymbols()
	// TDD-00098 stage 5: @__kml_throw's uncaught path references
	// @__kml_worker_uncaught unconditionally; no-op stub without workers.
	if e.usedExceptionHelpers && !e.usedWorkerRuntime {
		e.emitGlobal("define void @__kml_worker_uncaught(ptr %msg) {\nentry:\n  ret void\n}")
	}
	// TDD-00098: __kml_worker_spawn calls this before the first
	// pthread_create — curl_global_init is not thread-safe and must run
	// exactly once, before any thread exists. Real when the program can
	// fetch at all; a no-op stub otherwise.
	if e.usedWorkerRuntime {
		if e.usedFetch || e.usedFetchAsync {
			e.emitGlobal(`define void @__kml_worker_curl_preinit() {
entry:
  %inited = load i1, ptr @__kml_curl_inited, align 1
  br i1 %inited, label %done, label %doinit
doinit:
  call void @curl_global_init(i64 3)
  store i1 1, ptr @__kml_curl_inited, align 1
  br label %done
done:
  ret void
}`)
		} else {
			e.emitGlobal("define void @__kml_worker_curl_preinit() {\nentry:\n  ret void\n}")
		}
	}
	// TDD-00087: a lightweight await drives timers via @__kml_timer_fire_next.
	// Emit the real one when the program uses timers, else a no-op stub so the
	// reference resolves (a program with awaits but no timers).
	if e.usedAwaitTimerDrive {
		if e.usedTimers {
			e.emitTimerFireNext()
		} else {
			e.emitGlobal("define i1 @__kml_timer_fire_next() {\n  ret i1 0\n}")
		}
		// The same drive loop pumps in-flight fetches (TDD-00097 Stage 4);
		// without fetch the pump is a no-op stub.
		if !e.usedFetchAsync {
			e.emitGlobal("define i1 @__kml_fetch_pump() {\n  ret i1 0\n}")
		}
	}
	// The shared curl write callback references the body-stream hooks
	// (TDD-00097 Stage 4); when no Response.body stream was ever created,
	// they are no-op stubs and the buffered path is byte-for-byte unchanged.
	if e.usedReqBodyDrain && !e.usedReqBodyRuntime {
		e.emitGlobal("define void @__kml_reqbody_drain(ptr %c) {\n  ret void\n}")
	}
	if (e.usedFetch || e.usedFetchAsync) && !e.usedFetchBodyStream {
		e.emitGlobal("define i64 @__kml_fetch_body_write(ptr %p, ptr %c, i64 %t) {\n  ret i64 0\n}")
		e.emitGlobal("define void @__kml_fetch_body_on_done(ptr %p) {\n  ret void\n}")
	}
	// cluster (TDD-00105): the primary blocks until every forked worker exits,
	// keeping it alive while workers serve (a worker's own table is empty, so
	// this is a no-op there — and a worker is normally still inside its event
	// loop and never reaches here anyway).
	if e.usedClusterRuntime {
		e.emitInstr("call void @__kml_cluster_wait_all()")
	}
	// The process-lifecycle runtime (ADR-00334) is emitted whenever exceptions
	// (whose uncaught path calls the hooks) or any process.on('exit')/
	// 'uncaughtException'/exitCode surface is used.
	if e.usedExceptionHelpers || e.usedProcessLifecycle {
		e.emitProcessLifecycleRuntime()
	}
	// Normal program end: when process.exitCode / process.on('exit') is in play,
	// run the 'exit' listener and return process.exitCode; otherwise a plain 0.
	if e.usedProcessLifecycle {
		ec := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_process_exit_code, align 8", ec))
		e.emitInstr(fmt.Sprintf("call void @__kml_run_exit_handlers(i64 %s)", ec))
		ec32 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", ec32, ec))
		e.emitTerminator(fmt.Sprintf("ret i32 %s", ec32))
	} else {
		e.emitTerminator("ret i32 0")
	}

	var out strings.Builder
	out.WriteString("; Generated by KlainMainLang\n\n")
	out.WriteString(e.globals.String())
	if e.globals.Len() > 0 {
		out.WriteString("\n")
	}
	out.WriteString(e.functions.String())
	if e.functions.Len() > 0 {
		out.WriteString("\n")
	}
	out.WriteString("define i32 @main(i32 %argc, ptr %argv) {\nentry:\n")
	out.WriteString(e.allocas.String())
	// cluster (TDD-00105): a re-exec'd worker carries its id in the environment;
	// seed @__kml_cluster_worker_id from it before any cluster.isPrimary read.
	if e.usedClusterRuntime {
		out.WriteString("  call void @__kml_cluster_seed_id()\n")
	}
	// process.uptime() measures from process start — capture it once here.
	if e.usedProcessUptime {
		out.WriteString("  call void @__kml_proc_uptime_init()\n")
	}
	out.WriteString(e.body.String())
	out.WriteString("}\n")
	if e.islandHash != "" {
		e.emitIslandGlue(prog, &out)
	}
	return out.String(), nil
}

// emitIslandGlue appends the shared-library island surface (TDD-00056): a
// run-once `__kml_dynmod_<hash>_init` that drives the island's top-level
// (main()) exactly once, and one `__kml_dynmod_<hash>_<name>` accessor per
// module-global-promoted entry export, returning that binding's value. The
// call site dlopen()s the island, calls init once, and dlsym()s the accessors
// to build the dynamic-import result object. Exports whose type wasn't promoted
// to a module global (classes, closures, non-simple types) are omitted in V1,
// exactly as TDD-00055's own result-object synthesis omits them.
func (e *Emitter) emitIslandGlue(prog *ast.Program, out *strings.Builder) {
	h := e.islandHash
	fmt.Fprintf(out, "\n@__kml_dynmod_%s_inited = internal global i1 0, align 1\n", h)
	fmt.Fprintf(out, "define void @__kml_dynmod_%s_init() {\nentry:\n", h)
	fmt.Fprintf(out, "  %%done = load i1, ptr @__kml_dynmod_%s_inited, align 1\n", h)
	out.WriteString("  br i1 %done, label %skip, label %run\n")
	out.WriteString("run:\n")
	fmt.Fprintf(out, "  store i1 1, ptr @__kml_dynmod_%s_inited, align 1\n", h)
	out.WriteString("  %ig = call i32 @main(i32 0, ptr null)\n")
	out.WriteString("  br label %skip\n")
	out.WriteString("skip:\n  ret void\n}\n")

	publics := make([]string, 0, len(prog.EntryExportMangled))
	for p := range prog.EntryExportMangled {
		publics = append(publics, p)
	}
	sort.Strings(publics)
	for _, public := range publics {
		mangled := prog.EntryExportMangled[public]
		sym, ok := e.moduleGlobals[mangled]
		if !ok || sym.Ptr == "" || sym.Ty.IsArray {
			// Not a scalar/string module global (an array global is a header
			// slot, not a directly-loadable scalar; classes/closures/functions
			// aren't promoted at all) — omitted in V1.
			continue
		}
		ir := sym.Ty.IR
		fmt.Fprintf(out, "define %s @__kml_dynmod_%s_%s() {\nentry:\n", ir, h, public)
		fmt.Fprintf(out, "  %%v = load %s, ptr %s, align %d\n", ir, sym.Ptr, sym.Ty.Align())
		fmt.Fprintf(out, "  ret %s %%v\n}\n", ir)
	}
}

// registerEnums pre-scans all top-level enum declarations and resolves each member
// to a compile-time constant Value. Numeric members auto-increment from 0 (or from
// the last explicit value); string members require an explicit string literal.
func (e *Emitter) registerEnums(prog *ast.Program) {
	for _, stmt := range prog.Body {
		ed, ok := stmt.(*ast.EnumDeclaration)
		if !ok {
			continue
		}
		members := make(map[string]Value, len(ed.Members))

		// Detect string enum: any member has an explicit string value.
		isString := false
		for _, m := range ed.Members {
			if _, ok := m.Value.(*ast.StringLiteral); ok {
				isString = true
				break
			}
		}

		if isString {
			e.enumBacking[ed.Name] = TypePtr
			for _, m := range ed.Members {
				if sl, ok := m.Value.(*ast.StringLiteral); ok {
					ptr := e.internString(sl.Value)
					members[m.Name] = Value{Ref: ptr, Ty: TypePtr}
				}
			}
		} else {
			e.enumBacking[ed.Name] = TypeI64
			var counter int64
			for _, m := range ed.Members {
				if m.Value != nil {
					if nl, ok := m.Value.(*ast.NumberLiteral); ok {
						n, _ := strconv.ParseInt(nl.Value, 0, 64)
						counter = n
					}
				}
				members[m.Name] = Value{Ref: fmt.Sprintf("%d", counter), Ty: TypeI64}
				counter++
			}
		}
		e.enums[ed.Name] = members
	}
}

// registerInterfaces pre-scans all top-level interface and type alias declarations
// and records them in e.interfaces so resolveType can resolve named object types.
func (e *Emitter) registerInterfaces(prog *ast.Program) {
	// TS declaration merging (ADR-00466): an interface sharing a class's
	// name yields to the class — registering it would make annotations of
	// that name resolve to a plain object shape instead of the class
	// instance. The interface's extra members are ignored (disclosed
	// narrowing of real merge semantics).
	classNames := map[string]bool{}
	for _, stmt := range prog.Body {
		if cd, ok := stmt.(*ast.ClassDeclaration); ok {
			classNames[cd.Name] = true
		}
	}
	for _, stmt := range prog.Body {
		switch s := stmt.(type) {
		case *ast.InterfaceDeclaration:
			if classNames[s.Name] {
				continue
			}
			// A generic interface's field types reference an unresolvable
			// bare type-parameter name — defer to on-demand instantiation
			// at each usage site instead (TDD-00010 V1, see
			// emit_generics.go); never entered into e.interfaces itself.
			if len(s.TypeParams) > 0 {
				e.genericInterfaces[s.Name] = s
				continue
			}
			// A string index signature interface (TDD-00130) registers as the
			// map-backed dynamic-object type.
			if s.IndexSig != nil {
				e.interfaces[s.Name] = e.indexSignatureType(s.IndexSig)
				continue
			}
			// A callable interface (lone call signature — ADR-00455)
			// registers as the equivalent function type.
			if s.CallSig != nil {
				e.interfaces[s.Name] = e.resolveType(s.CallSig)
				continue
			}
			fields := make([]Field, len(s.Fields))
			for i, f := range s.Fields {
				fields[i] = Field{Name: f.Name, Ty: e.resolveType(f.Type)}
			}
			// interface+interface declaration merging (ADR-00479): a second
			// same-name interface unions its members into the first (first
			// declaration wins on a member-name collision, mirroring TS's
			// no-conflicting-members rule without checking it).
			if prev, ok := e.interfaces[s.Name]; ok && prev.IsObject {
				seen := map[string]bool{}
				for _, f := range prev.Fields {
					seen[f.Name] = true
				}
				merged := prev.Fields
				for _, f := range fields {
					if !seen[f.Name] {
						merged = append(merged, f)
					}
				}
				fields = merged
			}
			e.interfaces[s.Name] = ObjectType(fields)
			if len(s.Methods) > 0 {
				sigs := e.interfaceMethodSigs[s.Name]
				if sigs == nil {
					sigs = make(map[string]FuncSig, len(s.Methods))
				}
				for _, m := range s.Methods {
					sig := e.buildParamSig(m.Params)
					if m.ReturnType != nil {
						sig.RetType = e.resolveType(m.ReturnType)
					} else {
						sig.RetType = TypeVoid
					}
					sigs[m.Name] = sig
				}
				e.interfaceMethodSigs[s.Name] = sigs
			}
		case *ast.TypeAliasDeclaration:
			// A generic alias can't be resolved eagerly (its type parameters are
			// unbound); defer to on-demand instantiation at each use site.
			if len(s.TypeParams) > 0 {
				e.genericTypeAliases[s.Name] = s
			} else {
				e.interfaces[s.Name] = e.resolveType(s.Type)
			}
		}
	}

	// Merge `interface C extends A, B` base members (ADR-00451 batch):
	// base fields precede own fields; own declarations win on a name
	// collision; method signatures merge the same way. Iterated to a
	// fixpoint so multi-level chains resolve regardless of declaration
	// order; a cycle simply stops changing and stays as declared.
	type ifaceExt struct {
		name    string
		extends []string
	}
	var pending []ifaceExt
	for _, stmt := range prog.Body {
		if s, ok := stmt.(*ast.InterfaceDeclaration); ok && len(s.Extends) > 0 && len(s.TypeParams) == 0 && s.IndexSig == nil {
			pending = append(pending, ifaceExt{s.Name, s.Extends})
		}
	}
	for pass := 0; pass < len(pending)+1; pass++ {
		changed := false
		for _, ie := range pending {
			own, ok := e.interfaces[ie.name]
			if !ok || !own.IsObject {
				continue
			}
			// The resolver suffixes per-module names (`C__kml_mod0`); the
			// Extends list holds source-written bare names, so retry each
			// base under the owner's own module suffix.
			suffix := ""
			if i := strings.LastIndex(ie.name, "__kml_mod"); i > 0 {
				suffix = ie.name[i:]
			}
			var merged []Field
			seen := map[string]bool{}
			for _, base := range ie.extends {
				bt, ok := e.interfaces[base]
				if !ok && suffix != "" {
					base = base + suffix
					bt, ok = e.interfaces[base]
				}
				if !ok || !bt.IsObject {
					continue
				}
				for _, f := range bt.Fields {
					if !seen[f.Name] {
						seen[f.Name] = true
						merged = append(merged, f)
					}
				}
				for mn, ms := range e.interfaceMethodSigs[base] {
					if _, exists := e.interfaceMethodSigs[ie.name][mn]; !exists {
						if e.interfaceMethodSigs[ie.name] == nil {
							e.interfaceMethodSigs[ie.name] = map[string]FuncSig{}
						}
						e.interfaceMethodSigs[ie.name][mn] = ms
						changed = true
					}
				}
			}
			for _, f := range own.Fields {
				if seen[f.Name] {
					// Own re-declaration wins: replace the base's entry.
					for i := range merged {
						if merged[i].Name == f.Name {
							merged[i] = f
						}
					}
					continue
				}
				merged = append(merged, f)
			}
			if len(merged) != len(own.Fields) {
				e.interfaces[ie.name] = ObjectType(merged)
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

// registerFunctions pre-scans all top-level function declarations and records
// their signatures so calls can be resolved before the function body is emitted.
func (e *Emitter) registerFunctions(prog *ast.Program) error {
	var unannotated []*ast.FunctionDeclaration
	for _, stmt := range prog.Body {
		fd, ok := stmt.(*ast.FunctionDeclaration)
		if !ok {
			continue
		}
		// TDD-00061/ADR-00172: a generator function is never registered into
		// e.funcs at all — constructing one (`gen(args)`) doesn't emit an
		// ordinary `call`, it builds a fiber-backed instance struct instead
		// (see emit_generators.go), a fundamentally different dispatch than
		// every other entry in e.funcs gets.
		if fd.IsGenerator {
			info, err := e.buildGeneratorSig(fd)
			if err != nil {
				return err
			}
			e.generators[fd.Name] = info
			continue
		}
		// A generic function's param/return types reference an unresolvable
		// bare type-parameter name (e.g. "T") — resolving them now the same
		// way a normal function's are would silently default to i64
		// (ResolveTypeName's fallback). Defer entirely to on-demand
		// instantiation at each call site instead (TDD-00010 V1, see
		// emit_generics.go); the generic declaration itself is never
		// entered into e.funcs.
		if len(fd.TypeParams) > 0 {
			// TDD-00010 V2: an `@erased` generic function is compiled exactly
			// once, under its own source name, with every bare-T parameter/
			// return position substituted to TypeAny instead of a concrete
			// type — the opposite of the on-demand-instantiation path below.
			// Registered directly into e.funcs (not e.genericFuncs) so every
			// existing e.funcs-based mechanism (call dispatch, inferExprType,
			// Pass 2 emission) treats it exactly like a plain function whose
			// signature happens to use TypeAny, with no new dispatch code
			// needed anywhere else.
			if fd.Erased {
				e.funcs[fd.Name] = e.buildErasedFunctionSig(fd)
				if fd.ReturnType == nil {
					// TDD-00058: an @erased generic function's own return-
					// type inference has the identical same-file forward-
					// reference boundary a plain unannotated function does
					// (both ultimately call inferUnannotatedReturnType) —
					// folded into the same fixed-point sweep below rather
					// than left as a separate, narrower gap. Confirmed with
					// a real repro (an @erased function calling a plain,
					// later-declared unannotated function returning an
					// object) before fixing, not assumed.
					unannotated = append(unannotated, fd)
				}
				continue
			}
			e.genericFuncs[fd.Name] = fd
			continue
		}
		e.funcs[fd.Name] = e.buildFunctionSig(fd)
		if fd.ReturnType == nil {
			unannotated = append(unannotated, fd)
		}
	}
	e.reinferUntilFixedPoint(unannotated, func(fd *ast.FunctionDeclaration) FuncSig {
		if fd.Erased {
			return e.buildErasedFunctionSig(fd)
		}
		return e.buildFunctionSig(fd)
	}, func(fd *ast.FunctionDeclaration, sig FuncSig) {
		e.funcs[fd.Name] = sig
	}, func(fd *ast.FunctionDeclaration) Type {
		return e.funcs[fd.Name].RetType
	})
	return nil
}

// reinferUntilFixedPoint closes ADR-00041's forward-reference boundary
// (TDD-00058): a same-file/same-scope unannotated function calling another
// unannotated function declared later only saw an incomplete e.funcs/nested
// scope on registerFunctions'/pushNestedFuncScope's own first, single,
// source-order pass, so a non-scalar callee return type (object/array/
// closure/Date) under-inferred. Re-runs build for every entry in decls,
// writing each result back immediately (not batched) so a later entry in
// the same sweep can already see an earlier one's freshly corrected result,
// repeating until a full sweep changes nothing or the sweep cap
// (len(decls), enough for any acyclic reference chain to fully propagate)
// is reached — a genuinely circular unannotated pair can't converge by
// construction and keeps whatever the last sweep computed, same graceful
// fallback ADR-00041 already established, not a new failure mode.
func (e *Emitter) reinferUntilFixedPoint(decls []*ast.FunctionDeclaration, build func(*ast.FunctionDeclaration) FuncSig, store func(*ast.FunctionDeclaration, FuncSig), current func(*ast.FunctionDeclaration) Type) {
	for i := 0; i < len(decls); i++ {
		changed := false
		for _, fd := range decls {
			prev := current(fd)
			sig := build(fd)
			if !reflect.DeepEqual(prev, sig.RetType) {
				changed = true
			}
			store(fd, sig)
		}
		if !changed {
			return
		}
	}
}

// buildErasedFunctionSig computes the FuncSig for one `@erased` generic
// function declaration (TDD-00010 V2): every bare type-parameter position
// substitutes to TypeAny, and an unannotated return type gets the same
// best-effort inference (inferUnannotatedReturnType) a plain function's
// does — factored out of registerFunctions' inline block so it can also be
// re-run by reinferUntilFixedPoint (TDD-00058) for the identical same-file
// forward-reference boundary a plain unannotated function has.
func (e *Emitter) buildErasedFunctionSig(fd *ast.FunctionDeclaration) FuncSig {
	subs := make(map[string]Type, len(fd.TypeParams))
	for _, tp := range fd.TypeParams {
		subs[tp] = TypeAny
	}
	sig := e.buildGenericParamSig(fd.Params, subs)
	if fd.ReturnType != nil {
		sig.RetType = e.substituteGenericType(fd.ReturnType, subs)
	} else if inferred, ok := e.inferUnannotatedReturnType(fd.Body, sig.ParamNames, sig.ParamTypes); ok {
		sig.RetType = inferred
	} else {
		sig.RetType = TypeVoid
	}
	return sig
}

// buildFunctionSig computes the FuncSig for one non-generic function
// declaration: resolved/defaulted parameter types, rest-param detection, and
// an explicit or best-effort-inferred (see inferUnannotatedReturnType)
// return type. Shared by registerFunctions (top-level, TDD-00041's original
// home for this logic) and pushNestedFuncScope (TDD-00057) — one
// authoritative computation for both, rather than two copies that could
// silently drift apart the way emitFunctionDecl's own once did (see the
// comment on the return-type fallback below).
func (e *Emitter) buildFunctionSig(fd *ast.FunctionDeclaration) FuncSig {
	retType := TypeVoid
	if fd.ReturnType != nil {
		retType = e.resolveType(fd.ReturnType)
	}
	sig := FuncSig{RetType: retType, IsAsync: fd.IsAsync}
	for _, p := range fd.Params {
		var pty Type
		if p.Type != nil {
			pty = e.resolveType(p.Type)
		} else if p.Rest {
			pty = ArrayOf(TypeI64) // default rest element type: number
		} else {
			pty = TypeI64
			pty.Inferred = true // no annotation given — see docs/adr/ADR-00042.md
		}
		sig.ParamTypes = append(sig.ParamTypes, pty)
		sig.ParamNames = append(sig.ParamNames, p.Name)
		sig.Defaults = append(sig.Defaults, p.Default) // nil when no default
		sig.Optional = append(sig.Optional, p.Optional)
	}
	if len(fd.Params) > 0 && fd.Params[len(fd.Params)-1].Rest {
		sig.HasRest = true
	}
	// An unannotated function defaulted to TypeVoid above regardless of
	// what it actually returns — every caller trusted this registered
	// signature, so this broke both field access/calls on an
	// object/array/closure result AND (found while verifying this fix)
	// even a plain scalar return: emitFunctionDecl used to compute its
	// own, separately-defaulted-to-void return type independently of
	// this signature, so `function addOne(n) { return n + 1 }` emitted
	// a function whose LLVM signature said void while its body still
	// tried to `ret i64` the real value — a hard clang-stage type
	// mismatch, not just a silently-wrong result. Best-effort inference
	// from the function's own first return statement (see
	// inferUnannotatedReturnType) fixes both; a function with no
	// reachable return value at all keeps the void default.
	if fd.ReturnType == nil {
		paramNames := make([]string, len(fd.Params))
		for i, p := range fd.Params {
			paramNames[i] = p.Name
		}
		if inferred, ok := e.inferUnannotatedReturnType(fd.Body, paramNames, sig.ParamTypes); ok {
			sig.RetType = inferred
		}
	}
	return sig
}

// emitFunctionDecl emits one user-defined function into e.functions.

// registerNSAliases resolves `import X = Y.Z` alias declarations
// (ADR-00456) against the now-complete namespace table. Each target is
// resolved scope-relatively (innermost enclosing namespace outward, then
// absolute), following already-resolved aliases; two passes handle an
// alias whose target alias is declared later. An unresolvable alias is
// simply not registered — its use sites keep their ordinary undefined
// diagnostics.
func (e *Emitter) registerNSAliases(decls []ast.NSAliasDecl) {
	e.nsAliases = map[string]string{}
	resolveTarget := func(scope, target string) (string, bool) {
		try := func(cand string) (string, bool) {
			if _, ok := e.namespaces[cand]; ok {
				return cand, true
			}
			if t, ok := e.nsAliases[cand]; ok {
				return t, true
			}
			// A dotted target whose first segment is itself an alias
			// (`import r = M.X` where X aliases M.N).
			if i := strings.Index(cand, "."); i > 0 {
				if t, ok := e.nsAliases[cand[:i]]; ok {
					whole := t + cand[i:]
					if _, ok := e.namespaces[whole]; ok {
						return whole, true
					}
				}
			}
			return "", false
		}
		if scope != "" {
			parts := strings.Split(scope, ".")
			for k := len(parts); k >= 1; k-- {
				if t, ok := try(strings.Join(parts[:k], ".") + "." + target); ok {
					return t, true
				}
				// A dotted alias name declared in an enclosing scope.
				if t, ok := e.nsAliases[strings.Join(parts[:k], ".")+"."+target]; ok {
					return t, true
				}
			}
		}
		return try(target)
	}
	for pass := 0; pass < 2; pass++ {
		for _, d := range decls {
			full := d.Name
			if d.Scope != "" {
				full = d.Scope + "." + d.Name
			}
			if _, done := e.nsAliases[full]; done {
				continue
			}
			if t, ok := resolveTarget(d.Scope, d.Target); ok {
				e.nsAliases[full] = t
			}
		}
	}
}
