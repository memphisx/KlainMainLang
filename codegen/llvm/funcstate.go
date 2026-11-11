// funcstate.go — the per-function emitter state a nested function body must
// not inherit (TDD-00230 P3.5 splits it out of the Emitter).
package llvm

import (
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/shadow"
)

// funcContext is the language context of the function being emitted: its
// scopes, return and promise types, async, generator and constructor state,
// and the loop, label, cleanup and finally stacks. Emitter embeds it; a
// function emitted with none of the enclosing one's context swaps it.
type funcContext struct {
	// overrideThis is the slot holding a constructor's `this` after
	// super() to a base that may return another object (emitOverrideThis).
	overrideThis string
	// overrideThisStatic is its constructor's own instance `this`, which a
	// later super() constructs on.
	overrideThisStatic *Symbol
	// newTargetSlot holds a prototype constructor's new.target (boxed;
	// 0 when called, not constructed), read at its entry.
	newTargetSlot    string
	scopes           []scope
	currentGenerator *generatorEmitCtx
	currentRetType   Type // return type of the function being emitted
	// currentCtorClass is the class whose constructor body is being emitted
	// (TDD-00154), or "" outside any constructor — a write to a `readonly` field
	// is allowed only when this equals the field's declaring class.
	currentCtorClass string
	// memberClass is the class whose method or accessor body is being
	// emitted (static or not); "" elsewhere.
	memberClass          string
	inModuleTask         bool     // emitting @__kml_module_body
	sawAwait             bool     // set by emitAwait/for-await while a function body is emitted; saved/restored per body
	breakStack           []string // end labels for enclosing loops / switch
	continueStack        []string // continue-target labels for enclosing loops
	pendingFinallys      []pendingExit
	breakFinallyDepth    []int
	continueFinallyDepth []int
	pendingFrees         []pendingFree
	breakFreeScope       []int
	continueFreeScope    []int
	namedLabelStack      []namedLabel // labeled break/continue targets, innermost last
	isAsync              bool
	coroHdl              string // register holding the malloc'd promise slot
	asyncPromiseReg      string // non-suspending async fn: the settled task promise it returns (TDD-00084 Part A)
	asyncCatchLabel      string // non-suspending async fn: the catch-and-reject block label
	currentPromiseTy     Type   // T in Promise<T>; void if Promise<void>
	coroRetLabel         string // label for the async-return block
	asyncAdoptSlot       string
	// hoistedCaptures names the current function body's own locals/params that
	// are captured by some nested closure. Such a variable is heap-boxed eagerly
	// at its dominating declaration point (params: at function entry) rather than
	// lazily at the capturing closure's construction site — which may sit inside a
	// conditional block that doesn't dominate a later read of the box, producing
	// invalid "instruction does not dominate all uses" IR. See capturedLocalNames
	// and boxHoistedCapture.
	hoistedCaptures map[string]bool
	// forwardClosures names the current body's `const`/`let` bindings a
	// nested function reads (`const a = () => b(); const b = () => 1;`,
	// `const log = () => msg; const msg = "hi"`), and forwardBoxes the
	// entry-block cell each gets once a function built before the
	// declaration captures it: the declaration then stores into the same
	// cell. Installed with hoistedCaptures.
	forwardClosures map[string]*ast.VarDeclaration
	forwardBoxes    map[*ast.VarDeclaration]Symbol
	// inErrorOwnToString marks an Error's string conversion inside the
	// dynamic ToString emitErrorToString defers to.
	inErrorOwnToString bool
	// patternForward is each destructuring declaration's names that have
	// a forward cell: a declaration standing for the name, typed by a read
	// of it (patternForwardFill).
	patternForward map[ast.Statement]map[string]*ast.VarDeclaration
	// widenedBindings names the untyped scalar bindings in the current scope
	// that -compat=js must back with the any-box from declaration because they
	// hold two or more distinct scalar kinds over their lifetime (crossType-
	// WidenedBindings / TDD-00162).
	widenedBindings map[string]bool
	// earlyVars are the body's `var`s read before their declaration (see
	// earlyReadVars); lookup gives each its function-scope slot on that
	// first read.
	earlyVars map[string]*ast.VarDeclaration
	// thisInitSlot is a derived constructor's i1 cell recording that
	// super() has bound `this` (ctorGuardsThis); "" when not tracked.
	thisInitSlot string
	// paramScopeDone are the ParamScope vars the parameter prologue bound.
	paramScopeDone map[*ast.VarDeclaration]bool
	// emptyArrayElems maps an untyped empty-array-literal binding (`const a = []`)
	// in the current scope to the element type inferred from its later
	// push/insert/reassign usages (TDD-00205 Stage 1) — so `a.push("x")` gives it
	// `string[]` instead of the blind `number[]` default.
	emptyArrayElems map[string]Type
	// emptyMapKV maps an untyped bare-`new Map()` binding in the current scope to
	// the key/value types inferred from its later `map.set(k, v)` usages
	// (TDD-00211) — so heterogeneous keys widen to an `any`-keyed map instead of
	// riding the blind string-key/number-value default.
	emptyMapKV map[string]mapKV
	// forInitDepth is len(e.scopes) while a `for` statement's init clause is
	// emitted (0 otherwise). The init runs whenever the loop statement itself
	// is reached, so a `var` there is as definitely-initialized as one in the
	// enclosing statement list and must not be widened by hoistedVarMaySkip.
	// Keyed on the depth rather than a bool so a closure body nested in the
	// init (fresh single-frame scope stack) is unaffected.
	forInitDepth int
	// tryDepth > 0 while a try/catch/finally body is emitted; every local
	// looked up there is pinned across the setjmp (pinSlotAcrossSetjmp).
	tryDepth int
	// The emission modes a caller sets around one nested emit: a function
	// body started meanwhile begins with none of them.
	// inOwnMethodCheck: emitting the class-method arm of that check.
	inOwnMethodCheck bool
	// inHeapPrim is set while a heap primitive (heap_prims.go) emits.
	inHeapPrim              bool
	dynKeysForIn            bool // emitDynAnyKeys serves for...in: walk the prototype chain
	dynConstructing         bool // emitDynConstructDispatch: an abstract class constructs too
	dynGetInline            bool // emitting a dynGetHelper body
	dynSetInline            bool
	unboxingTriStr          bool // emitUnboxBoxToType's string null pass
	inDynValueOf            bool // emitting emitDynPrimValueOf's object arm
	inCtorArm               bool // emitting a __kml_dyn_construct arm
	brandOnly               bool // instanceof tests a private brand: the constructor's, not the prototype chain
	dunderRead              bool // emitDynProtoRead reads `x.__proto__`, not Object.getPrototypeOf
	speciesInner            bool // emitting a species method's own call on a body (arrayBodySpeciesRule)
	inArrayOverrideDispatch bool // emitArrayOverrideDispatch's default branch: Array's own method
	inDynBufferCall         bool // emitDynBufferMethodCall's non-Buffer branch
	inDynNumberCall         bool // emitDynNumberToString's non-number branch
	recordSpreadInner       bool // emitRecordSpreadLiteral is building one of its two forms
	recordTypeofOwn         bool // emitting typeof of a structural member's own-layout read
}

// funcBuilder is the state of the function being emitted: its entry-block
// allocas and its body (merged at the end), the register and label
// counters, and whether the current block has its terminator. Emitter
// embeds it, so a nested function's emission swaps one pointer.
type funcBuilder struct {
	allocas   strings.Builder
	body      strings.Builder
	regCtr    int
	labelCtr  int
	blockDone bool
	// frame is the function's shadow-call-stack record (frames.go), and
	// frameOld the top it replaced, which every return restores; "" for a
	// function without one. A helper emitted meanwhile has its own builder,
	// and so none.
	frame    string
	frameOld string
	// frameFile is the source file the frame names.
	frameFile string
	// sitePos are the enclosing calls' positions, innermost last.
	sitePos []ast.Pos
}

// programDecls are the program's declarations as code generation reads
// them: its functions, classes, interfaces, enums, generics and
// namespaces, registered before emission and read throughout it.
type programDecls struct {
	// classDeclByName indexes the program's class declarations (programClassDecl).
	classDeclByName map[string]*ast.ClassDeclaration
	// classDeclName is the name each class declaration is registered
	// under in classes: repr's class arm finds a checker type's class by
	// its declaration.
	classDeclName map[*ast.ClassDeclaration]string
	funcs         map[string]FuncSig // registered function signatures
	// callableClasses: the program's CallableClasses.
	callableClasses     map[string]bool
	interfaces          map[string]Type                // named interface, type alias, and class registry
	interfaceMethodSigs map[string]map[string]FuncSig  // interface name → method name → signature (TDD-00009 Stage 4, `implements` conformance only — not used for dispatch)
	foreignCtor         map[*ast.ClassDeclaration]bool // foreignCtorClass's answers
	classes             map[string]ClassInfo           // named class registry (fields/ctor/methods) — see emit_classes.go
	// genericFuncs/genericInterfaces/genericClasses hold the raw declaration
	// for every `<T>`-parameterized function/interface/class (TDD-00010 V1),
	// keyed by its bare source name — deliberately *not* also entered into
	// funcs/interfaces/classes, since T isn't resolvable until a real call/
	// usage/construction site supplies a concrete type. See emit_generics.go.
	genericFuncs map[string]*ast.FunctionDeclaration
	// topFuncDecls holds every non-generic top-level function declaration by name,
	// so a named-function HOF callback (`arr.reduce(callbackfn)`) whose untyped
	// parameters mismatch the element type can be re-emitted monomorphized against
	// the callback hints (ADR-00913) — the same re-emit-with-a-new-sig path that
	// instantiateGenericFunc uses, keyed here on the hint types rather than type
	// parameters.
	topFuncDecls map[string]*ast.FunctionDeclaration
	// nestedFuncSrc is each mangled nested function declaration's source
	// text, as its value's Function.prototype.toString gives it.
	nestedFuncSrc map[string]ast.SourceRange
	// classSrc is each class's source text by its mangled name, as its
	// constructor's Function.prototype.toString gives it.
	classSrc          map[string]ast.SourceRange
	genericInterfaces map[string]*ast.InterfaceDeclaration
	// genericTypeAliases holds `type Name<T> = ...` declarations (TDD-00079
	// Stage 3), instantiated on demand at each use site by substituting concrete
	// type arguments into the alias body, then resolving the result.
	genericTypeAliases map[string]*ast.TypeAliasDeclaration
	genericClasses     map[string]*ast.ClassDeclaration
	// genericFamilies is each generic class `instanceof` tests: the id its
	// instantiations record as their parent.
	genericFamilies map[string]int64
	// generators holds one entry per top-level `function*` declaration
	// (TDD-00061/ADR-00172), keyed by its bare source name — deliberately
	// separate from funcs, since constructing a generator (`gen(args)`)
	// never emits a plain `call` to the generator's own compiled body
	// function the way an ordinary named-function call does; see
	// emit_generators.go.
	generators  map[string]*GeneratorInfo
	enums       map[string]map[string]Value // enum name → member name → constant value
	enumBacking map[string]Type             // enum name → backing value type (double numeric, ptr string)
	enumObjects map[string]string           // enum name → the module constant holding it as a value
	// enumComputed: enum name → computed member → the module constant its
	// value is evaluated into where the enum is declared.
	enumComputed map[string]map[string]string
	namespaces   map[string]map[string]bool
	// nsAliases maps a fully-resolved alias name (dotted for a namespace-
	// scoped `export import`) to the dotted namespace it targets (ADR-00456).
	nsAliases        map[string]string
	globalLinks      map[string]string                // TDD-00232: a linked global's name -> its module's class
	lowerTypeClasses map[string]*ast.ClassDeclaration // @lowerType classes by mangled name (lower_type_class.go)
	// ambientGenericMiss caches names registerAmbientGeneric found nothing for.
	ambientGenericMiss map[string]bool
	// genericInstTypes memoizes genericClassInstanceType per instantiation;
	// genericInstBuilding holds the ones whose fields are being built.
	genericInstTypes    map[string]Type
	genericInstBuilding map[string]bool
	// genericInstInferring guards genericInstMethodRet's body inference
	// against a method whose return reads itself.
	genericInstInferring map[*ast.FunctionDeclaration]bool
	// genericInstSrc is each instantiation's class and type arguments, so a
	// method call through a `Box<number>` annotation instantiates it before
	// any `new Box<number>` is emitted.
	genericInstSrc map[string]genericInstSource
	// genericIfaceTypes memoizes complete generic interface instantiations
	// by their type arguments' full shapes.
	genericIfaceTypes map[string]Type
	topLevelNames     map[string]bool                                 // every name bound at module top level (registerModuleGlobals)
	staticCompanion   map[*ast.ClassDeclaration]*ast.ClassDeclaration // generic class declaration → the class holding its statics
	typeOnlyGenerics  map[string]bool                                 // classes whose type parameters codegen erased (eraseTypeOnlyGenerics)
	topLevelCallables map[string]bool                                 // top-level function and class names (initRefsCallable)
}

// moduleUnits are the program's modules as statement groups: the builtin
// library's modules and the import() targets', each run by its own init
// function. Set once per program, before emission.
type moduleUnits struct {
	// dynStmts/dynGroups/dynModules/dynInits: the bundled and lazy import()
	// targets' dynamic-only statements, by file, and the inits emitted
	// (emit_dynmodule.go).
	dynStmts   map[ast.Statement]string
	dynGroups  map[string][]ast.Statement
	dynModules map[string][]string
	dynInits   map[string]bool
	// libStmts are the builtin library's top-level statements (the
	// program's LibStatements) with their module keys.
	libStmts map[ast.Statement]string
	// libGroups are each builtin module's top-level statements by module
	// key; libInits the modules whose init function is emitted.
	libGroups  map[string][]ast.Statement
	libInits   map[string]bool
	libExports map[string]string // "<module>:<name>" → merged name of a builtin TS module declaration
}

// moduleOut is the module being written: its global declarations and its
// function definitions, the string constants it interns and the runtime
// functions it has declared. It outlives every function emitted into it.
type moduleOut struct {
	globals   strings.Builder // global declarations (string constants, printf decl, …)
	functions strings.Builder // finished function definitions (writeFunc)
	// strConsts maps a Go string value to its interned @.s<n> name, and
	// strConstRaw an interned reference back to its value.
	strConsts   map[string]string
	strConstRaw map[string]string
	fnDecls     map[string]bool // the functions declared once (declareFn)
	// The used* flags record which runtime pieces the module has
	// emitted or declared.
	usedDynImportWatch   bool // the import() watcher runtime is emitted (TDD-00225)
	usedIslandLoop       bool // an island with a top-level await: islandsrc/island.c (TDD-00225)
	usedDynWhen          bool // dynwhen.c: bundled import() settles from its module promise
	usedObjSubclassSplit bool // an inspect/JSON split reads a subclass instance at its own layout
	usedSignalAborted    bool // the AbortSignal accessors are emitted (emit_signal.go)
	usedStdoutGlobal     bool // the libc `stdout` FILE* extern global is declared (ADR-00867)
	usedAbsentArrayCell  bool
	// usedJmpStack: the program links jmpstack.c (the try frames' buffers).
	usedJmpStack bool
	// usedStackGuard: main installs the stack-overflow handler (stackguard.c).
	usedStackGuard        bool
	usedBufFill           bool // buffill.c is linked (ensureBufFill)
	usedTtyRead           bool // TDD-00031: the tty shim (klain:tty's `@link tty`)
	usedTui               bool // TDD-00150: klain:tui — Yoga layout + ANSI diff painter
	usedArrLitRows        bool // __kml_arr_lit_rows (ADR-01064)
	usedInspectReduce     bool // inspect_reduce.c (ADR-01067)
	usedCasemap           bool // casemap.c — Unicode toUpperCase/toLowerCase
	usedGCInterface       bool // the GC interface declared (runtime_worker.go)
	usedStringC           bool // string.c — lowered String.prototype methods
	usedNumberC           bool // number.c — lowered Number.prototype methods
	usedFnMeta            bool
	usedBigIntBoxHooks    bool // emit_bigint_box.go: boxed-bigint hooks the dynamic runtime calls
	usedFFIRegistry       bool
	usedUmapOrder         bool
	usedOSInfoBags        bool // the IR bag builders over osinfo.c (process.env value, networkInterfaces)
	usedDynJSONC          bool
	usedShapeRuntime      bool // the layout table + shape.c (emit_shape.go)
	usedPromiseResolveAny bool
	usedPromiseWrapperOf  bool
	usedNanBox            bool
	usedAnyToPrimitive    bool
	usedClassIs           bool // unitreg.go: __kml_class_is
	usedFetch             bool
	usedFetchAsync        bool
	usedLoopTurn          bool // emitted code waits on the event loop: main() runs it (runtime_loop_turn.go)
	usedLoopTurnDefs      bool
	// usedEventLoop: loopsrc/loop.c is linked, compiled with loopCFlags.
	usedEventLoop        bool
	usedFsWatchRuntime   bool
	usedThreadPool       bool
	usedProcessLifecycle bool
	usedWorkerRuntime    bool
	usedIPC              bool
	usedIPCChildRuntime  bool
	usedWebview          bool
	usedSync             bool
	usedEmbeddedAssets   bool
	usedSqliteNatives    bool
	usedWasmNatives      bool
	usedFFINatives       bool
	// usedFFIDl guards the one-time libdl decls (TDD-00164, runtime_ffi.go).
	usedFFIDl            bool
	usedFsThrow          bool
	usedViews            bool // views.c: typed arrays' buffer/byteOffset (TDD-00243)
	usedBoxedViews       bool // a typed array is boxed: its buffer/byteOffset read through `any`
	usedStrHeaderRuntime bool
	usedNodeInterpGuard  bool
	usedProcessUptime    bool
	usedSymbolRegistry   bool
	usedTimers           bool
	usedJSONExpandoRest  bool
	usedGeneratorRuntime bool
	usedBswap16          bool
	usedBswap32          bool
	usedBswap64          bool
	usedTaskRuntime      bool
	usedPromiseRuntime   bool // the promise struct + __kml_task_alloc_promise, without the fiber scheduler (TDD-00084 Part A)
	usedUnhandledRuntime bool // unhandled-rejection tracking (emit_unhandled.go)
	usedObjExtensible    bool // @__kml_obj_extensible is called (emitObjExtensibleFinalize)
	usedElemKindWidth    bool // @__kml_elem_kind_width (emit_fetch_body.go)
	usedObjExtra         bool // @__kml_obj_has_extra and its hooks are called (emitObjExtraFinalize)
	usedShapeKeysArray   bool
	usedProtoOf          bool
	// streamDirect marks the async wrappers streamUnderlyingDynamic builds
	// (Node's own); streamWrapDirect is set while one's stream wrapper is
	// generated, which hands the stream its promise to react to as it is.
	streamDirect      map[ast.Expression]bool
	streamWrapDirect  bool
	usedMapView       bool
	usedKeyOrder      bool // keyorder.c is linked (ensureKeyOrder)
	usedDynInstanceof bool
	// ordinaryInstanceof makes emitInstanceOf OrdinaryHasInstance: no
	// class's own @@hasInstance (the per-class instanceof rows).
	ordinaryInstanceof bool
	usedDynConstruct   bool
	usedConstructorOf  bool
	usedClassRec       bool
	usedCtorKind       bool
	// usedClassRefProto: a class reference's prototype was read
	// (@__kml_classref_proto, fnproto.go).
	usedClassRefProto bool
	fnBaseClasses     map[string]string // constructor function → its empty base (fnBases)
	// pendingConstLoop: the `const` for-in/for-of variable whose next
	// definition binds it constant (constLoopVar).
	pendingConstLoop string
	// ovrCall is the override's own body the next emitClassCall calls
	// (emitWideOverrideCall); noWideOverride, a call taking the inherited
	// signature's arguments only.
	ovrCall            *ovrCallReq
	overrideErr        error // emitOverrideThis's private definitions' failure
	noWideOverride     bool
	undeclaredBases    map[string]string              // a class extending a name nothing declares (JS lane): read at its definition
	valueBases         map[string]bool                // a variable a class extends: its value when the class is defined (fnBases)
	heritageGlobals    map[string]bool                // value bases whose prototype global is declared (emit_heritage.go)
	blockHoisted       map[*ast.ClassDeclaration]bool // a block's class moved to the top level; its block keeps a copy for its site
	protoRetargeted    map[*ast.VarDeclaration]bool   // bindings Object.setPrototypeOf retargets (protoRetargetedDecls)
	usedClassRefHas    bool                           // emitted code calls @__kml_classref_has
	emittedClassRefHas bool
	usedClassRefBag    bool // emitted code calls @__kml_classref_bag
	// defineOnClassOwn: emitDynOwnBagOf is finding a definition's target,
	// a class reference's own bag rather than its snapshot.
	defineOnClassOwn        bool
	emittedClassRefBag      bool
	usedHostBox             bool
	usedPromiseBox          bool // a promise was boxed (emitPromiseInspectHook)
	usedZeroCell            bool // @__kml_zero_cell (emit_fetch.go)
	usedPromiseSettle       bool // @__kml_promise_settle — bare-promise settle+wake+drain for new Promise(executor) (TDD-00087)
	usedPromiseAdoptRunner  bool // @__kml_promise_adopt_runner — thenable adoption for resolve(aPromise) (TDD-00091)
	usedMicrotasks          bool
	usedFinRegHelpers       bool // ensureFinalizationHelpers emit-once guard
	usedSortCmpAnyLex       bool
	usedMapStrHelpers       bool
	usedNullRef             bool
	usedFetchBodyProm       bool // lazy Response body promises settled off the reactor (TDD-00186)
	usedPromiseAddReaction  bool
	usedAnyIterCollect      bool                                        // __kml_any_iter_collect (emit_iter_collect.go)
	usedAnyIterTake         bool                                        // __kml_any_iter_take (emit_iter_collect.go)
	usedAnyObjRest          bool                                        // __kml_any_obj_rest (emit_dynobj_destr.go)
	usedObjProtoFill        bool                                        // __kml_objproto_fill (emit_builtin_proto.go)
	primSetUsed             bool                                        // a property write on a primitive (__kml_prim_proto_set)
	staticFieldInits        map[*ast.ClassDeclaration][]staticFieldInit // emit_static_override.go
	curCallee               ast.Expression                              // the callee of the call being emitted (emitCall)
	ownCtorAfterAwait       bool                                        // a for-await step reads its promise's constructor after the await
	staticWrites            map[string]bool                             // class+"\x00"+name a program writes (emit_static_override.go)
	usedZlibNatives         bool                                        // klainzlib.c compiled into the pool (-DKLAINPOOL_ZLIB)
	usedAnyArrayLikeCollect bool                                        // __kml_any_arraylike_collect (emit_iter_collect.go)
	usedExceptionHelpers    bool
	usedIntegrityLevels     bool // the header-word integrity runtime (ensureIntegrityLevels)
	usedRegexCompile        bool
	usedRegexCompileContext bool
	usedRegexMatch          bool
	// Once-written declarations and helpers, by what they serve.
	declaredBigInt bool // the __kml_bigint_* declares have been emitted once
	// recvMsgEmitted: __kml_recv_msg (emitReceiverMessage) is written.
	recvMsgEmitted       bool
	objExtraEmitted      bool
	declaredAnyArrView   bool // __kml_anyarr_view/sync declared (emitDynArrayMethodCall)
	respCollectDefined   bool // @__kml_resp_collect defined (emit_response_new.go)
	declaredDateParseStr bool
	declaredRegexGroups  bool
	// contentDefined: the content-named helpers written (defineContentNamed).
	contentDefined     map[string]bool
	dynGetHelpers      map[string]string // property name → its dynamic-get routine
	dynSetHelpers      map[string]string
	protoBagEmitted    map[string]bool
	objLitEmitted      map[string]bool
	objLitInstances    map[string]bool // synthetic classes an instance was made of
	anyBigIntOpsDone   bool
	usesDynamicImport  bool // set when an import(...) call is emitted
	usesCrypto         bool // set the first time any crypto.subtle operation is emitted (drives backend compile+link in main.go)
	usesBigInt         bool // set the first time any bigint operation is emitted (drives backend compile+link in main.go)
	workerUncaughtStub bool // @__kml_worker_uncaught is declared (declareWorkerUncaught)
	// unhandledReportDone records that @__kml_unhandled_report is defined.
	unhandledReportDone  bool
	emittedConstructorOf bool
	hasWorkers           bool // set at EmitProgram start from Program.WorkerModules
	// TDD-00099: shared memory + channels.
	// usedIPC links the fork IPC framing C (TDD-00141); usedIPCChildRuntime
	// marks the child-side channel surface (process.send/.on('message')),
	// which hooks the event loop.
	// usedWebview links the C++ system-webview binding (TDD-00142).
	// usedSync links the klain:sync goroutine runtime (TDD-00143).
	// usesSyncProgram is set from prog.UsesKlainSync before body emission: the
	// program imports klain:sync, so the safepoint-insertion pass emits
	// cooperative preempt checks at function entry and loop back-edges.
	usesSyncProgram      bool
	needMicrotaskTick    bool // emitted code calls @__kml_microtask_tick (a top-level await's tick)
	errorGetKeyDone      bool
	hostIterUsed         bool
	emittedDynConstruct  bool
	emittedClassRefProto bool // __kml_classref_proto is defined
	errorLayoutNoted     bool // errorObjType's layout has its type id
	respCollectCalled    bool // a body method calls @__kml_resp_collect
	// programUsesALS is set in EmitProgram before Pass 2: true when the
	// program has async_hooks (lib/node/async_hooks.ts), so timer callbacks
	// are wrapped to carry the async context across the schedule→fire
	// boundary (TDD-00168 Stage 3).
	programUsesALS bool
	// programUsesFinReg is a whole-program pre-scan result (set in EmitProgram
	// before Pass 2): true when the program constructs a FinalizationRegistry
	// anywhere, so Memory.free sites — possibly emitted before the construction
	// — include the finalizer-lookup hook (TDD-00163 Stage 2).
	programUsesFinReg bool
	// pendingHelpers: contentNamedHelper's helpers being emitted, true once
	// one is reached again before its body is done.
	pendingHelpers map[string]bool
	// alsBindTramps memoizes the monomorphic AsyncLocalStorage.bind trampolines
	// by closure-signature key (TDD-00168 Stage 4) — see emit_asynchooks.go.
	alsBindTramps map[string]bool
	// asyncGenStepFns memoizes the per-generator-type deferred `.next()` microtask
	// step function, keyed by the generator struct IR (TDD-00094).
	asyncGenStepFns map[string]string
	hostGetters     map[string]string // emitted host static getters (hostStaticGetter)
	// fnValueTrampolines memoizes the env-dropping trampoline emitted for each
	// named function taken by value (`const g = f`), keyed by its mangled LLVM
	// name — see emit_func_value.go.
	fnValueTrampolines map[string]bool
	// fnValueHeaders memoizes the static `{trampoline, null}` closure header
	// emitted once per named function taken by value, keyed by its mangled LLVM
	// name. Emitting it once (as a global constant) rather than malloc'ing a
	// fresh header at every reference gives a named function a *stable* value
	// identity, so a listener removal (`removeEventListener(f)`,
	// `emitter.off(f)`) matches an earlier registration — see
	// emit_func_value.go.
	fnValueHeaders     map[string]bool
	shapeMethodRecs    map[string]string // class.method → its dynamic-function record
	boxedLayouts       map[int64]bool    // type ids of layouts boxed into a dynamic value
	promiseBoxRunners  map[string]string // element representation → its Promise<any> reaction
	wkSymbols          map[string]bool   // well-known symbols emitted (emit_symbol.go)
	wkSymbolSlots      map[string]bool   // constants holding a well-known symbol (emit_symfields.go)
	promiseConvRunners map[string]string
	anyToLayoutFns     map[int64]string // layout header → its any→layout conversion (emit_shape.go)
	// protoBuilders are the __kml_proto_get_<Class> builders written.
	protoBuilders       map[string]bool
	testTrampolines     map[string]bool
	recordFns           map[string]bool // record-view read/write routines emitted (emit_record_view.go)
	mapViewFns          map[string]mapViewReq
	mapViewDone         map[string]bool
	keyOrderBlocks      map[string]string // constant order blocks by content
	hostIterFns         map[int64]string
	anyArrElemFns       map[string][2]string // KJ_BOXED routines by element type (anyArrayElemRoutines)
	fetchBodyPromRunner map[string]string    // method (text/json/arrayBuffer) → synthesized settle-runner name
	deepFreeFns         map[string]string
	unitTables          map[int]*unitTable          // unitreg.go: registered rows by kind
	islandUnitRows      map[string]map[int][]string // unitreg.go: a dynamic-only file's rows (lazy), by its hash and kind
	islandUnitFiles     map[string]bool             // unitreg.go: the dynamic-only files whose bodies register them
	ctorRows            map[string]*ctorRow         // emit_classref_rows.go, by payload
	// protoSlots are the builtin prototype objects' global slots emitted;
	// usedProtoOf marks a program that maps an instance to its builtin
	// class's prototype (emitBuiltinProtoOf).
	protoSlots map[string]bool
	classRefs  map[string]string // class → its constructor reference payload (emit_classref.go)
	// classRecords is each closure-converted class used as a value: every
	// evaluation makes a record (TDD-00242); classRecordsPending holds them
	// until the hoisted name is final. usedClassRec declares the runtime.
	classRecords map[string]bool
	// staticAreas are the record classes' static layouts (TDD-00242), and
	// classStaticCaps the locals their static members read, held in hidden
	// static fields.
	staticAreas         map[string]*staticArea
	classRecordsPending map[*ast.ClassDeclaration]bool
	classRecordSites    map[ast.Node]bool              // the in-block copy that makes the record
	classShownNames     map[string]string              // class → its display name, where the class expression's own name differs
	anonClasses         map[*ast.ClassDeclaration]bool // class expressions with no name of their own (V8 names them "anonymous" in brand errors)
	genShownNames       map[string]string              // generator → its display name, where the generator expression's own name differs
	// tupleArrayCalls memoizes tupleArrayCall's rewrites, so emission and
	// inference see one call.
	exactClassSlots  map[string]bool // a `const x = new C()` binding's slot: x is a C (foreignFieldTags)
	methodValueSlots map[string]bool // a `const f = k.greet` binding's slot (mayBeMethodValue)
	switchDiscCount  int             // synthetic `switch` discriminant bindings (bindSwitchDiscriminant)
	linkLibs         map[string]bool // external non-libc libraries the compiled program needs (e.g. "curl")
	// The runtime files EmbeddedCSources resolved, once: LinkLibs adds the
	// libraries they bring.
	cSources        []CSource
	cSourcesErr     error
	cSourcesDone    bool
	importSettleCtr int  // per-site @__kml_dynimport_settle_<n> counter
	usesPathWin32   bool // set the first time a win32-flavoured path call is emitted (drives path_win32.c compile+link — TDD-00178)
	// frameDescs are the shadow call stack's descriptors, by name and file;
	// funcFiles each function's file.
	frameDescs map[[2]string]string
	// fileIndexes are the program's files by their place (fileIndex).
	fileIndexes     map[string]int
	asyncGenStepCtr int
	// currentGenerator is non-nil exactly while emitting a generator
	// function's own body — set/cleared by emitGeneratorFunctionDecl the
	// same way currentRetType/isAsync are for an ordinary function, checked
	// by emitYieldExpression and emitReturn to route `yield`/`return`
	// through the suspend-and-swap path instead of an ordinary value/`ret`.
	// nextClassTagID continues registerClasses' own TagID sequence (see its
	// Pass 1) for generic class instantiations, assigned lazily on demand
	// (emit_generics.go) — always ≥ every real class's TagID, so a
	// Box<number> instance can never collide with an unrelated real class's
	// runtime identity tag.
	nextClassTagID int64
	// layoutIDs/layouts: the object layouts given a type id for their header
	// word (objheader.go), interned by layoutKey.
	layoutIDs map[string]int64
	layouts   []registeredLayout
	// typedLayouts: the layouts -mm=gc allocates with their pointer map, by
	// row key, in first-use order (layout_table.go).
	typedLayouts     map[int64]Type
	typedLayoutOrder []int64
	closureCtr       int // monotonically increasing counter for unique closure names
	nestedFuncCtr    int
	// objLitClassCtr names each synthetic anonymous class an object literal
	// with getters/setters is lowered to (TDD-00153) — one per literal site.
	// objLitClasses maps each such literal to its synthetic class name (so the
	// type inferred for the variable slot matches the emitted value);
	// objLitEmitted tracks which synthetic classes' method bodies were emitted.
	objLitClassCtr int
	// fnmeta.go (TDD-00229 Stage A): function-value metadata keyed by code
	// pointer, the NamedEvaluation names of anonymous literals, and whether
	// fnmeta.c + its table are needed.
	// emit_ffi_runtime.go (TDD-00229): the node:ffi registry is linked; one
	// dynamic-ABI thunk per signature identity; the last validated name length.
	fnMetas        []fnMetaEntry
	fnMetaSeen     map[string]bool
	fnWords        map[string]int64  // a registered function's prefix word (fnWord)
	staticFnOf     map[string]string // a static function value's code (emitStaticFnRecord)
	genExprCtr     int               // generator expressions compiled as values
	fromEntriesCtr int               // Object.fromEntries over non-string keys
	weakSeedCtr    int               // new WeakMap(entries)/new WeakSet(values)
	// dynFnCtr numbers @__kml_dynfn_N dynamic-ABI functions (TDD-00155
	// Stage 4); jsProtoCtor marks top-level function declarations recognized
	// as vanilla-JS prototype constructors under -compat=js.
	dynFnCtr              int
	errSubCount           int64
	ctors                 []string // functions run before main
	hookInits             []string // IR the hooks constructor runs (runtime_hooks.go)
	ctorRowOrder          []string
	loopCFlags            []string
	fetchBodyPromTypedCtr int // per-call-site typed json() runners (emit_fetch.go)
	// webviewBindings records the typed window.* bindings for --emit-window-dts.
	webviewBindings []webviewBindingSig
	// embeddedDirs maps a compile-time-literal directory path to the IR symbol
	// its packed blob is linked under (TDD-00142 Stage 7); usedEmbeddedAssets
	// gates the embed_assets.c static-server runtime.
	embeddedDirs     map[string]string
	embedSymbols     map[string]bool
	optRecvCtr       int
	generatorBodyCtr int
	recordFnList     []recordFn // their bodies, written at finalize
	// hostRecordViews: a host value reached a structural slot (convert),
	// so a view routine may meet its box.
	hostRecordViews bool
	// mapLayouts are the Map and Set instantiations created; mapViewFns the
	// view dispatchers requested (TDD-00258).
	mapLayouts      []hostLayout
	keyOrderNone    bool            // @__kml_keyorder_none is defined
	classExprSeq    int             // hoisted class expressions (emit_classexpr.go)
	hostRefs        map[string]bool // builtin constructors boxed as values (emit_classref.go)
	combinatorCtr   int             // Promise combinator runners (emit_promise_combinators.go)
	finregCount     int             // unique-name counter for per-site trampoline/report fns
	optionalCallCtr int
	deepFreeCtr     int
	libClosures     map[string]int
	// fnStaticHosts maps a builtin constructor's function-value header
	// (`Number` by value) to the constructor whose statics it reads.
	fnStaticHosts map[string]string
	// synthTypes are the types synthesized source names by annotation
	// (synthTypeAnnotation).
	synthTypes map[string]Type
	// decoratedMethodSlots (TDD-00161 Stage 2) maps class name → method name →
	// the LLVM global holding that method's current callable (a boxed tag-12
	// value). A decorated method's call sites route through this slot instead of
	// a static direct call, so a method decorator that replaces the descriptor's
	// `value` takes effect. Only ever populated for actually-decorated methods;
	// every other method keeps the static dispatch path byte-for-byte.
	decoratedMethodSlots map[string]map[string]string
	// standardFieldInitSlots (TDD-00161 Stage 5): class → field → the LLVM
	// global holding that field's standard-decorator initializer (a boxed
	// callable, or undefined for none). Consulted per-instance in the
	// constructor tail to transform the field's initial value.
	standardFieldInitSlots map[string]map[string]string
	// standardInitListGlobals: class → the LLVM global holding a dynamic array
	// of `context.addInitializer` callbacks, run per-instance in the constructor
	// tail with `this` as receiver.
	standardInitListGlobals map[string]string
	workerRegistry          string
	hostRendered            int
	hostIterated            int
	classExprCaptures       map[*ast.ClassExpression]string
}

// beginDetachedFunc starts the body of a function emitted while another is
// being emitted (a synthesized adapter, a dynamic function): a fresh,
// synchronous function body with its own scope, its return type `any` —
// none of the enclosing function's async, generator, loop or cleanup state —
// restored by the returned func.
func (e *Emitter) beginDetachedFunc() func() {
	restore := e.beginThunkEmit()
	saved := e.funcContext
	e.funcContext = &funcContext{currentRetType: TypeAny}
	// Its registers and labels are numbered from zero (a fresh builder), as
	// a declared function's are, so a helper's text does not depend on how
	// much the program emitted before it: its unit is cached by that text.
	e.pushScope()
	return func() {
		e.funcContext = saved
		restore()
	}
}

// writeFunc writes a finished function to the module: its definition
// header (`internal i64 @f(ptr %env)`), then its entry block's body, the
// allocas first. Every function the emitter builds goes out through here,
// the one place its prologue and epilogue are written.
func (e *Emitter) writeFunc(header, body string) {
	e.functions.WriteString(funcDef(header, body))
}

// funcDef is a builder-generated function's text: header (return type,
// name, parameters) over body, whose first block is entry.
func funcDef(header, body string) string {
	return "\n" + funcHead(header) + body + "}\n"
}

// funcHead opens a function's text: header over its entry block's label,
// for a body written after it.
func funcHead(header string) string {
	return "define " + header + " {\nentry:\n"
}

// frontState is the checker over the program being emitted and the
// answers codegen keeps from it: built on first use (front), read by every
// function the module emits.
type frontState struct {
	// The front end over the program being emitted (TDD-00230 P3.2): the
	// checker the lowered calls read, built on first use.
	frontChecker *checker.Checker
	// synthOrigin links each node a rewrite after binding built to the
	// expression it stands for (originate).
	synthOrigin map[ast.Expression]ast.Expression
	// loweredNodes are rewrite-built nodes with no origin (lowered).
	loweredNodes map[ast.Expression]bool
	// frontWarm is the checker's answer for each expression warmFront typed.
	frontWarm    map[ast.Expression]*checker.Type
	frontBinding *binder.Binding
	frontBuilt   bool
	// moduleExports names each CommonJS file's module.exports global
	// (emitModuleThis).
	moduleExports map[string]string
	// frontRepr answers the expression kinds typeOfExpr has switched to
	// the checker (switchedExprKind); built once, from frontChecker.
	frontRepr      *frontOracle
	frontReprBuilt bool
	// shadowOracle and shadowRec drive the old-vs-new decider comparison
	// (shadow.go); both nil outside a KML_SHADOW run.
	shadowOracle ShadowOracle
	shadowRec    *shadow.Recorder
	// shadowAsking counts the oracle queries shadowExprType has open.
	shadowAsking int
	// initFnDecls are the top-level declarations of library modules and
	// dynamic-only files, whose statements run in an init function: each is
	// a global (registerModuleGlobals).
	initFnDecls map[*ast.VarDeclaration]bool
	// shadowTopDecls is the program's top-level declarations, in a shadow
	// run (shadowLocalGlobalType).
	shadowTopDecls map[*ast.VarDeclaration]bool
	// shadowAsked counts the nodes each decider was asked about, per node
	// kind, in a shadow run (shadowSeen keeps each node once).
	shadowAsked map[string]int
	shadowSeen  map[shadowNode]bool
	// shadowKeys is shadowWrittenKeys' memo; noShadowRead is set while
	// emitShadowableRead emits the field itself.
	shadowKeys map[string]bool
	// accessorKeySet is accessorKeys' memo.
	accessorKeySet map[string]bool
	// cellsAfter is the cell declarations toCell made for destructured
	// bindings a local class captures, each to follow the statement
	// binding it once its block is rebuilt.
	cellsAfter map[ast.Statement][]ast.Statement
	// cellsBefore and cellsTail are a loop cell's write-backs: before a
	// continue of the loop body, and at the body's end.
	cellsBefore map[ast.Statement][]ast.Statement
	cellsTail   map[*ast.BlockStatement][]ast.Statement
	// dynBaseLifted names each class expression specializeDynamicBases
	// lifted to a top-level declaration, a candidate base of a class whose
	// base is a run-time value; dynBaseSeq numbers them.
	dynBaseLifted map[*ast.ClassDeclaration]string
	dynBaseSeq    int
	noShadowRead  bool
}

// programFacts is what the analysis pre-passes record about the program
// before and during emission: per-node marks and plans every function
// reads, computed once per program.
type programFacts struct {
	// spreadArgs: the program's spread arguments, iterations of their
	// value (only once arrayIterScan found the property named).
	spreadArgs map[ast.Expression]bool
	// callSpreadArgs: the arguments spread into a call or `new` (`f(...x)`),
	// whose "not iterable" TypeError V8 words as a spread's; nil until
	// first asked.
	callSpreadArgs map[ast.Expression]bool
	// jsOverrideWiden is the js lane's method families (keyed root class
	// "." name) whose overrides lower to different signatures.
	jsOverrideWiden map[string]bool
	// jsClassDeclMap caches jsClassDecls (the program's classes by name)
	// and jsGenOverridden generatorOverridden's answers: both are asked at
	// every class method call, and each walks the whole program.
	jsClassDeclMap  map[string]*ast.ClassDeclaration
	jsGenOverridden map[string]bool
	// genPatternInits: a generator's destructured parameters, as the
	// statements its start runs (desugarGeneratorPatternParams).
	genPatternInits map[*ast.FunctionDeclaration][]ast.Statement
	// liftedStaticGens maps a static generator method to the generator
	// declaration that holds its original body (liftStaticGenerators).
	liftedStaticGens map[*ast.FunctionDeclaration]*ast.FunctionDeclaration
	// promiseValue is the value type each `new Promise`'s executor was
	// emitted with, the instantiation's (newPromiseValueType): its
	// `resolve` takes it.
	promiseValue   map[*ast.NewExpression]Type
	sloppyDecls    map[*ast.FunctionDeclaration]bool
	sloppyFuncs    map[string]bool
	strictFns      map[ast.Node]bool                      // strictFunctions (callerprops.go)
	privateInOwner map[privateInKey]*ast.ClassDeclaration // each `#x in o`'s declaring class (privateInOwners)
	// boxedCapReads are a converted class's reads of a capture field
	// declared any (captureAnnotation).
	boxedCapReads map[ast.Expression]bool
	// progAssigned holds every name the program assigns (emitVarRedeclaration).
	progAssigned map[string]bool
	// promotedGlobalDecls marks the exact top-level VarDeclaration nodes promoted
	// to module globals, by pointer identity — so emitVarDecl stores the
	// initializer into the global for *those* declarations only. A same-named
	// local inside a function or a nested block (a different node) is unaffected
	// and shadows the global normally; scope depth can't distinguish them (a
	// function body also runs at scopes depth 1).
	promotedGlobalDecls map[*ast.VarDeclaration]bool
	// patternGlobals is, per top-level destructuring statement, the names it
	// binds that a function reads, each promoted to a module global
	// (registerPatternGlobals).
	patternGlobals map[ast.Statement][]string
	// jsHeldGlobals are the -compat=js globals held as `any` because no
	// rule types them (registerModuleGlobals).
	jsHeldGlobals map[*ast.VarDeclaration]bool
	funcFiles     map[ast.Node]string
	// identFiles each identifier's file, built when an undeclared name is
	// assigned (sloppyAt).
	identFiles map[*ast.Identifier]string
	voidSites  map[ast.Expression]bool // voidValueSites, computed once
	// constRegexLits maps an unambiguous `const <name> = /regex/` binding to its
	// regex literal, for resolving a replace()/replaceAll() pattern's static
	// capture count. constRegexAmbig blacklists names declared more than once
	// (any kind/scope), so a shadowed name is never misattributed;
	// constRegexScanned marks the one-time lazy scan done. See
	// staticRegexCaptureCount (emit_regexp_replace.go).
	constRegexLits    map[string]*ast.NewRegExpExpression
	constRegexAmbig   map[string]bool
	constRegexScanned bool
	// constRegexNonConst marks a recorded regex-literal binding as `let`/`var`
	// (not `const`); it is honored only if constRegexReassigned shows the name is
	// never written after declaration (TDD-00210 follow-up — the String.replace
	// A4 files bind their pattern with `var`). See constRegexLiteral.
	constRegexNonConst   map[string]bool
	constRegexReassigned map[string]bool
	// receiverPassed are the calls receiverlessThisCall built.
	receiverPassed map[*ast.CallExpression]bool
	objLitClasses  map[*ast.ObjectLiteral]string
	// anyAssigned caches anyAssignedDecls.
	anyAssigned map[*ast.VarDeclaration]bool
	// chainCopies maps a copy of an optional chain's link that emission
	// builds (the link no longer optional) to the link it copies.
	chainCopies map[ast.Expression]ast.Expression
	fnLitNames  map[ast.Node]string
	// tmplCalls memoizes each tagged template's desugared call, and
	// tmplSites each template object's cache global (emit_call.go).
	tmplCalls map[*ast.TaggedTemplateExpression]*ast.CallExpression
	tmplSites map[*ast.CallExpression]string
	// protoCtorOwnThis: the constructor functions declared with a `this: T`
	// parameter, which receives the new object (no hidden one).
	// sloppyFileSet: the program's own sloppy-mode files (sloppyFiles);
	// dynSloppy: the function expression being compiled is one of theirs.
	sloppyFileSet map[string]bool
	// sloppyWriteNodes: each assignment, update and delete in a sloppy
	// file, true where it runs in sloppy code (outside a class and a
	// "use strict" function); sloppyWrite is the one being emitted's
	// (sloppyWriteSites).
	sloppyWriteNodes map[ast.Node]bool
	// typedUse: whether each program names a TypedArray class
	// (usesTypedArrays).
	typedUse map[*ast.Program]bool
	// selfNameAssigns: assignments to a named generator expression's own
	// name, renamed to its declaration (renameGeneratorSelfName).
	selfNameAssigns map[*ast.AssignmentExpression]bool
	// genExprNames: each nested generator expression's declaration name.
	genExprNames map[*ast.FunctionExpression]string
	// refiningCall marks a call emitCall is converting to the checker's
	// function type (checkerRefinedCallType), so the inner emit is plain.
	refiningCall map[*ast.CallExpression]bool
	// stmtNS maps a namespace's top-level statement to the namespace.
	stmtNS map[ast.Statement]string
	// constArrayTypes caches the type of an array literal of constants
	// (constLiteralTree): it is the same under any scope, and a large
	// table's elements are otherwise re-typed by every pre-pass over it.
	constArrayTypes map[*ast.ArrayLiteral]Type
	// libMembers are the builtin declaration files' method signatures
	// (isLibMember), collected on first use.
	libMembers map[ast.Node]bool
	// callOrigins maps a call codegen synthesized for a builtin's lowering
	// to the program's call the checker typed.
	callOrigins     map[*ast.CallExpression]*ast.CallExpression
	literalMembers  map[*ast.IndexExpression]*ast.MemberExpression // builtinLiteralMember's nodes
	staticInitAt    map[*ast.ClassDeclaration]bool                 // program class → its static init runs at its declaration
	classReeval     map[ast.Node]*ast.ClassDeclaration             // in-function evaluation site → hoisted class whose statics it re-initializes (emit_classexpr.go)
	tupleArrayCalls map[*ast.CallExpression]*ast.CallExpression
	implicitThis    map[*ast.FunctionExpression]bool // a method shorthand's `this: any` (maybeAddThisParamExpr)
	// pendingFinallys is the stack of what leaving an enclosing try/catch (or a
	// for-of over a generator) must do, innermost last. A `return` (or
	// `break`/`continue`) that exits one runs these inline — innermost first —
	// before its own terminator: pop the try's handler (its jmpbuf), then run
	// its `finally`. See emitPendingFinallys / emitTry.
	// breakFinallyDepth / continueFinallyDepth record len(pendingFinallys) at
	// each loop/switch entry, in lockstep with breakStack / continueStack, so a
	// `break`/`continue` runs only the finallys nested inside its target
	// loop/switch (not the ones outside it, unlike `return`). See emitBreak /
	// emitContinue and pushBreakTarget / pushContinueTarget.
	// TDD-00173 (-mm=auto / @free / @owned): autoFreePlan is the escape
	// analysis' verdict per declaration (present+true = flow proven safe);
	// pendingFrees are the live block-exit free obligations of the function
	// body being emitted, each tagged with the scope depth it belongs to.
	// breakFreeScope/continueFreeScope record len(e.scopes) at loop/switch
	// entry (lockstep with breakStack/continueStack) so break/continue free
	// exactly the obligations of the scopes they exit.
	autoFreePlan   map[*ast.VarDeclaration]bool
	autoFreeRebind map[*ast.VarDeclaration]bool
	// TDD-00175 Stage 1: bindings the interior-alias analysis additionally
	// proved transitively owned (deep-free candidates), and the memo table /
	// counter for the synthesized per-type recursive frees
	// (emit_free_deep.go).
	autoFreeDeep   map[*ast.VarDeclaration]bool
	stackAllocPlan map[*ast.VarDeclaration]bool
	// classStackAudit memoizes classStackEligible per class name (may a
	// non-escaping instance of it be stack-allocated?).
	classStackAudit map[string]bool
	// Stage 3 (@owned): the last-use statement per owned binding / per owned
	// parameter — the early-free point; entries stay on pendingFrees as the
	// block-exit safety net and are marked emitted once the early free lands.
	autoOwnedLastUse      map[*ast.VarDeclaration]ast.Statement
	autoOwnedParamLastUse map[*ast.FunctionDeclaration]map[string]ast.Statement
	// arrayIterScan: 0 unscanned, 1 the program never names
	// Array.prototype[Symbol.iterator], 2 it does.
	arrayIterScan int
	// lazyStaticSig are the static methods unregisteredStaticSig is building.
	lazyStaticSig map[string]bool
	// moduleGlobals holds top-level `const`/`let` bindings promoted to LLVM
	// module globals so a named `function` declaration (emitted with a fresh,
	// separate scope) can read them — not just an arrow/closure that captures
	// them (TDD-00093). lookup falls back to this after the function scopes.
	moduleGlobals map[string]Symbol
	// anyBigIntOps are the library's `any` bigint operators
	// (emitAnyBigIntOpsFinalize).
	anyBigIntOps map[string]bool
	// bigIntScan caches mayHoldBigInt: 0 unscanned, 1 none, 2 found.
	bigIntScan int
	// strictFuncs is strictFuncIn's set, computed once.
	strictFuncs map[ast.Node]bool
	// symbolScan caches mayHoldSymbol: 0 unscanned, 1 none, 2 found.
	symbolScan int
	// emptyScopes holds, per bare `[]`/`new Map()`/`new Set()` declaration,
	// the empty-container answers of the scope that declares it, for a
	// query from inside a nested function (bareContainerType).
	emptyScopes  map[*ast.VarDeclaration]emptyScope
	emptyPending []*ast.VarDeclaration
	// filledPending/filledBoxed: -compat=js array literals with elements,
	// and those a store of another kind made any[].
	filledPending []*ast.VarDeclaration
	filledBoxed   map[*ast.VarDeclaration]bool
	// topLevelReturns maps a module-level `return` to the label after its
	// module's last statement (ast.Program.TopLevelReturns).
	topLevelReturns map[*ast.ReturnStatement]string
	newClassArgs    map[string][]ast.TypeNode
	// newByKey indexes the program's `new` expressions (originalNew).
	newByKey map[string]*ast.NewExpression
	// jsMemberWrites: property names the program assigns (jsMemberWritten).
	jsMemberWrites map[string]bool
	// memberFlags: the names memberWrittenFlag has defined a global for.
	memberFlags map[string]bool
	// objLitMethods are the object literals' function-valued properties.
	objLitMethods map[ast.Node]bool
	// forcedGlobalTypes: a top-level declaration promoted on the checker's
	// agreement with the inferred type (registerModuleGlobals).
	forcedGlobalTypes map[*ast.VarDeclaration]Type
	unassignedFields  map[string]bool // "Class.field" a read may find unassigned (markUnassignedField)
	// dynEntryWidened names the class-typed parameters a dynamic entry
	// binds as `any` while its body is emitted (emit_dynentry.go).
	dynEntryWidened map[string]bool
	// primReadKeys are the keys a dynamic read on a primitive may name
	// (emitPrimProtoOfFinalize).
	primReadKeys map[string]bool
	jsProtoCtor  map[string]bool
	// nativeIfaceMembers: the own members of each interface over a native
	// layout (nativeHeritage), by interface name.
	nativeIfaceMembers  map[string]map[string]Type
	protoCtorOwnThis    map[string]bool
	cryptoSubtleAliases map[string]bool
	// errorShadowedSlots: the system-error slots (`code`, `errno`, …) some
	// Error subclass declares as its own field (ADR-01319).
	errorShadowedSlots map[string]bool
	// forwardMethodRet is a class's methods' inferred results while it is
	// registered, for a call to a sibling declared later.
	forwardMethodRet map[string]Type
	// forwardMethods names a class's own methods (accessors aside) while it
	// is registered: a method read as a value before its signature is.
	forwardMethods    map[string]bool
	ambientMiss       map[string]bool // names with no usable builtin-declaration interface (emit_ambient.go)
	ambientBusy       map[string]bool // builtin interfaces whose members are resolving (emit_ambient.go)
	protoReachNames   map[string]bool
	libDynMemberReads map[string]bool
	// patchedProtos are the "Class.method" builtin prototype methods the
	// program replaces (patchedProtoMethod).
	patchedProtos map[string]bool
	// inNativeProto marks a builtin prototype method's own body.
	inNativeProto bool
	// moduleTDZ maps a top-level binding with a dead zone a function may
	// reach to its flag global (module_tdz.go); mainFuncCtx is main's
	// context, the top level's own code.
	moduleTDZ   map[string]string
	mainFuncCtx *funcContext
	// inTypedArrayProto marks the emission of %TypedArray%.prototype for
	// __kml_TypedArray_protoOf (a getPrototypeOf it reads is plain).
	inTypedArrayProto bool
	propAttrScan      int // 0 unscanned, 1 no defineProperty, 2 some
	wrapperScan       int // 0 unscanned, 1 no wrapper object built, 2 some
	holesScan         int // 0 unscanned, 1 no hole producer, 2 some
	// classCaptures is each closure-converted class's hidden fields
	// (emit_classclosure.go), by class name; classCapturesPending holds
	// them until the hoisted name is final, and classCapFromThis marks a
	// `new` inside such a class's body, which copies them from `this`.
	classCaptures        map[string][]classCapture
	classCapturesPending map[*ast.ClassDeclaration][]classCapture
	classCapFromThis     map[*ast.NewExpression]bool
	// classCaptureRejects is a block class naming a function local that
	// closure conversion cannot express: the local and why.
	classCaptureRejects    map[*ast.ClassDeclaration][2]string
	classStaticCaps        map[string][]classCapture
	classStaticCapsPending map[*ast.ClassDeclaration][]classCapture
	ambientOverloads       map[string]int // ambientOverloaded's counts
	// funcSeeded names a module `var` with an initializer that shares a
	// function declaration's name; funcSeedOrder lists them in order.
	funcSeeded     map[string]bool
	funcSeedOrder  []string
	arrayVariantOf map[string]string // an Array class variant's class (arrayClassVariant)
}
