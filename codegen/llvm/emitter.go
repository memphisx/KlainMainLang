package llvm

import (
	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/lib"
	"KlainMainLang/options"
	"KlainMainLang/resolver"
	"KlainMainLang/sema"
	"KlainMainLang/shadow"
	"fmt"
	"math"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
)

// Value is an LLVM value reference: either a register (%t0) or an inline constant (42).
// An array value is the pointer to its shared {data, len} header (TDD-00230
// P3.3): arrayParts reads its current data and length, mkArray builds one.
type Value struct {
	Ref string
	Ty  Type
}

// Symbol represents a local variable in the symbol table.
type Symbol struct {
	// TDZ marks a parameter still in its dead zone while an earlier
	// parameter's default is evaluated (emitParamDefaultPrologue): reading
	// it throws JavaScript's ReferenceError.
	TDZ bool
	// NotDefined marks a name a parameter default reads that only the
	// function body declares: outside the body's scope, so reading it
	// throws ReferenceError "<name> is not defined".
	NotDefined bool
	// Ptr is the value's storage slot. For a scalar it is the alloca holding the
	// value. For an array (object-reference model, TDD-00127) it is a stable
	// slot — an `alloca ptr`, or a module global — holding a pointer to a shared
	// heap {data, len} header; arrayDataLenSlots derives the field addresses.
	Ptr     string
	Ty      Type
	Boxed   bool // true once Ptr points to a heap cell shared with closures that capture it
	IsConst bool // true for a `const`-declared binding; checked by emitAssign to reject plain `=` reassignment
	// SelfName marks a named function expression's own name inside it: an
	// immutable binding (assignment is ignored, or throws in strict code).
	SelfName bool
	// IsCapture marks a closure body's binding for a cell captured from an
	// enclosing function — visible in the closure's own frame but not one of
	// its `var`s, so a `var` of the same name there is a fresh local, not a
	// re-declaration (varRedeclarationTarget).
	IsCapture bool
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
	// ForwardName is set on the cell of a closure an earlier closure
	// captured before its declaration ran (forwardClosureSym): a call
	// through it checks the cell, as JavaScript's temporal dead zone does.
	ForwardName string
}

// isNullableScalarLocal reports whether this binding is a nullable non-pointer
// scalar: its storage is always the presence-flagged { i1, T } aggregate.
func (s Symbol) isNullableScalarLocal() bool {
	return isNullableScalar(s.Ty) && s.Ptr != ""
}

type scope struct {
	syms map[string]Symbol
	// top marks the program's own frame (created once in NewEmitter): the
	// one function scope whose `var` bindings live in moduleGlobals rather
	// than in syms, which the `var` re-declaration check must consult.
	top bool
}

// Emitter walks an AST and produces LLVM IR text.
type Emitter struct {
	// iterTakeNow (n+1, 0 unset) bounds the next emitAnyArrayElems
	// iteration to n values (emitAnyArrayElemsTake); iterTake carries a
	// destructuring pattern's count into resolveArrayHOF.
	iterTakeNow, iterTake int
	// iterUse marks the next resolveArrayHOF as an iteration (array
	// destructuring), which a replaced Array.prototype[Symbol.iterator]
	// reroutes (array_iter_patch.go).
	iterUse bool
	// arrayIterScan: 0 unscanned, 1 the program never names
	// Array.prototype[Symbol.iterator], 2 it does.
	arrayIterScan int
	// spreadArgs: the program's spread arguments, iterations of their
	// value (only once arrayIterScan found the property named).
	spreadArgs map[ast.Expression]bool
	// forceArrayProtocol: the next emitForOfAnyValue walks an array through
	// the protocol (the collect/take helpers, reached for an array only
	// once Array.prototype[Symbol.iterator] changed).
	forceArrayProtocol bool
	// genPatternInits: a generator's destructured parameters, as the
	// statements its start runs (desugarGeneratorPatternParams).
	genPatternInits map[*ast.FunctionDeclaration][]ast.Statement
	// liftedStaticGens maps a static generator method to the generator
	// declaration that holds its original body (liftStaticGenerators).
	liftedStaticGens map[*ast.FunctionDeclaration]*ast.FunctionDeclaration
	// lazyStaticSig are the static methods unregisteredStaticSig is building.
	lazyStaticSig map[string]bool
	// classDeclByName indexes the program's class declarations (programClassDecl).
	classDeclByName map[string]*ast.ClassDeclaration
	// promiseValue is the value type each `new Promise`'s executor was
	// emitted with, the instantiation's (newPromiseValueType): its
	// `resolve` takes it.
	promiseValue map[*ast.NewExpression]Type
	// callerProps: the program reads `caller`/`arguments` of a function
	// (callerprops.go); sloppyDecls/sloppyFuncs its sloppy functions.
	callerProps bool
	// genProtos: the program can observe a generator's prototype
	// (prepareGenProtos), so each instance records the one it inherits.
	genProtos bool
	// genArgc is the count (plus one) the next generator construction
	// stores in its __argc field ("" for not known).
	genArgc string
	// argcOverride, when set, is the register holding the count noteArgc
	// stores (a dynamic adapter's argv count) in place of the call's own.
	argcOverride    string
	sloppyDecls     map[*ast.FunctionDeclaration]bool
	sloppyFuncs     map[string]bool
	strictFns       map[ast.Node]bool                      // strictFunctions (callerprops.go)
	privateInOwner  map[privateInKey]*ast.ClassDeclaration // each `#x in o`'s declaring class (privateInOwners)
	*funcBuilder                                           // the function being emitted (funcstate.go)
	*funcContext                                           // its language context (funcstate.go)
	switchDiscCount int                                    // synthetic `switch` discriminant bindings (bindSwitchDiscriminant)
	// shadowOracle and shadowRec drive the old-vs-new decider comparison
	// (shadow.go); both nil outside a KML_SHADOW run.
	shadowOracle ShadowOracle
	// boxedCapReads are a converted class's reads of a capture field
	// declared any (captureAnnotation).
	boxedCapReads map[ast.Expression]bool
	// progAssigned holds every name the program assigns (emitVarRedeclaration).
	progAssigned map[string]bool
	shadowRec    *shadow.Recorder
	globals      strings.Builder // global declarations (string constants, printf decl, …)
	functions    strings.Builder // emitted user-defined function bodies
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
	// classDeclName is the name each class declaration is registered
	// under in classes: repr's class arm finds a checker type's class by
	// its declaration.
	classDeclName map[*ast.ClassDeclaration]string
	sharedCode    int // content-named helper bodies being built (sharedBody)
	// shadowAsked counts the nodes each decider was asked about, per node
	// kind, in a shadow run (shadowSeen keeps each node once).
	shadowAsked   map[string]int
	shadowSeen    map[shadowNode]bool
	analysisDepth int // analysis pre-passes running (analysisPass)
	// anyBigIntOps are the library's `any` bigint operators
	// (emitAnyBigIntOpsFinalize).
	anyBigIntOps     map[string]bool
	anyBigIntOpsDone bool
	// bigIntScan caches mayHoldBigInt: 0 unscanned, 1 none, 2 found.
	bigIntScan int
	// varsBeingInitialized names the variable(s) whose initializer is currently
	// being emitted. A closure created inside a variable's own initializer that
	// captures that same variable (`const s = f(() => use(s))`) must NOT seed its
	// capture cell by loading the still-uninitialized slot — see
	// promoteCaptureToCell.
	varsBeingInitialized map[string]bool
	// hoistedCaptures names the current function body's own locals/params that
	// are captured by some nested closure. Such a variable is heap-boxed eagerly
	// at its dominating declaration point (params: at function entry) rather than
	// lazily at the capturing closure's construction site — which may sit inside a
	// conditional block that doesn't dominate a later read of the box, producing
	// invalid "instruction does not dominate all uses" IR. Installed (saved/
	// restored) per function body, exactly like e.allocas. See capturedLocalNames
	// and boxHoistedCapture.
	hoistedCaptures map[string]bool
	// forwardClosures names the current body's `const`/`let` closures that a
	// closure written before the declaration calls (`const a = () => b();
	// const b = () => 1;`), and forwardBoxes the entry-block cell each gets
	// once that earlier closure captures it: the declaration then stores into
	// the same cell. Installed with hoistedCaptures.
	forwardClosures map[string]*ast.VarDeclaration
	forwardBoxes    map[*ast.VarDeclaration]Symbol
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
	// widenedBindings names the untyped scalar bindings in the current scope
	// that -compat=js must back with the any-box from declaration because they
	// hold two or more distinct scalar kinds over their lifetime (crossType-
	// WidenedBindings / TDD-00162). Saved/restored per body like hoistedCaptures.
	widenedBindings map[string]bool
	// emptyArrayElems maps an untyped empty-array-literal binding (`const a = []`)
	// in the current scope to the element type inferred from its later
	// push/insert/reassign usages (TDD-00205 Stage 1) — so `a.push("x")` gives it
	// `string[]` instead of the blind `number[]` default. Saved/restored per body
	// like widenedBindings.
	emptyArrayElems map[string]Type
	// emptyMapKV maps an untyped bare-`new Map()` binding in the current scope to
	// the key/value types inferred from its later `map.set(k, v)` usages
	// (TDD-00211) — so heterogeneous keys widen to an `any`-keyed map instead of
	// riding the blind string-key/number-value default. Saved/restored per body
	// like emptyArrayElems.
	emptyMapKV map[string]mapKV
	// emptyScopes holds, per bare `[]`/`new Map()`/`new Set()` declaration,
	// the empty-container answers of the scope that declares it, for a
	// query from inside a nested function (bareContainerType).
	emptyScopes  map[*ast.VarDeclaration]emptyScope
	emptyPending []*ast.VarDeclaration
	// filledPending/filledBoxed: -compat=js array literals with elements,
	// and those a store of another kind made any[].
	filledPending []*ast.VarDeclaration
	filledBoxed   map[*ast.VarDeclaration]bool
	// newCollectionHint carries a redeclared `var`'s already-decided Map/Set
	// type into the bare `new Map()`/`new Set()` its initializer emits
	// (emitVarRedeclaration → emitExpr, ADR-01061). Consumed on first use.
	newCollectionHint *Type
	strConsts         map[string]string // Go string value → @.s<n> name
	strConstRaw       map[string]string // interned string reference → its Go string value
	// convertGaps lists the representation pairs coerce has no conversion
	// for (TDD-00230 P3.3: convert is total); any is a compiler bug.
	convertGaps []string
	linkLibs    map[string]bool // external non-libc libraries the compiled program needs (e.g. "curl")
	opts        options.Options // the compile modes, set once (SetOptions)
	// The front end over the program being emitted (TDD-00230 P3.2): the
	// checker the lowered calls read, built on first use; fnDecls are the
	// functions declared once.
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
	// frontRepr answers the expression kinds inferExprType has switched to
	// the checker (switchedExprKind); built once, from frontChecker.
	frontRepr         *frontOracle
	frontReprBuilt    bool
	fnDecls           map[string]bool
	usesDynamicImport bool // set when an import(...) call is emitted
	// topLevelReturns maps a module-level `return` to the label after its
	// module's last statement (ast.Program.TopLevelReturns).
	topLevelReturns       map[*ast.ReturnStatement]string
	islandHash            string // non-empty when compiling a shared-library island (TDD-00056): its stable hash
	dynImportShimDeclared bool   // guards the one-time @__kml_dynimport_run declaration
	usedDynImportWatch    bool   // the import() watcher runtime is emitted (TDD-00225)
	usedIslandLoop        bool   // an island with a top-level await: islandsrc/island.c (TDD-00225)
	usedDynWhen           bool   // dynwhen.c: bundled import() settles from its module promise
	usedObjSubclassSplit  bool   // an inspect/JSON split reads a subclass instance at its own layout
	// dynStmts/dynGroups/dynModules/dynInits: the bundled and lazy import()
	// targets' dynamic-only statements, by file, and the inits emitted
	// (emit_dynmodule.go).
	dynStmts              map[ast.Statement]string
	dynGroups             map[string][]ast.Statement
	dynModules            map[string][]string
	dynInits              map[string]bool
	importSettleCtr       int   // per-site @__kml_dynimport_settle_<n> counter
	noAnyErr              error // first --no-any violation seen during type resolution; surfaced by EmitProgram
	usesCrypto            bool  // set the first time any crypto.subtle operation is emitted (drives backend compile+link in main.go)
	usesBigInt            bool  // set the first time any bigint operation is emitted (drives backend compile+link in main.go)
	declaredBigInt        bool  // the __kml_bigint_* declares have been emitted once
	usesJSONParse         bool  // set the first time JSON.parse/Response.json() is emitted (drives json_parse.c compile+link in main.go — TDD-00077 Track P)
	declaredJSONParseTree bool  // the __kml_json_* parse-tree declares have been emitted once
	usesPathWin32         bool  // set the first time a win32-flavoured path call is emitted (drives path_win32.c compile+link — TDD-00178)
	usesFloatFmt          bool  // set the first time a float is printed (drives dtoa.c compile+link in main.go — TDD-00080)
	declaredDtoa          bool  // the __kml_dtoa declare has been emitted once
	usedSignalAborted     bool  // the AbortSignal accessors are emitted (emit_signal.go)
	usedPrintf            bool
	usedDprintf           bool
	usedStdoutGlobal      bool // the libc `stdout` FILE* extern global is declared (ADR-00867)
	usedMalloc            bool
	usedArgc              bool
	// methodsReadArgs: some class member reads `arguments`, so every method
	// and constructor call stores its argument count (an override is called
	// through its base's signature, which cannot carry a hidden rest).
	methodsReadArgs bool
	// newClassArgs: a generic class construction's type arguments as the
	// checker inferred them, by the `new`'s class and position.
	storesPresentBusy map[*ast.Identifier]bool // storesPresent's recursion guard
	newClassArgs      map[string][]*ast.TypeAnnotation
	// jsMemberWrites: property names the program assigns (jsMemberWritten).
	jsMemberWrites map[string]bool
	// memberFlags: the names memberWrittenFlag has defined a global for.
	memberFlags map[string]bool
	// inOwnMethodCheck: emitting the class-method arm of that check.
	inOwnMethodCheck    bool
	usedCalloc          bool
	usedNullDerefThrow  bool
	usedRangeErrorThrow bool
	usedAbsentArrayCell bool
	// pendingDeref: base nodes whose value emitExpr must null-guard (emit_nullderef.go).
	pendingDeref     map[ast.Expression]derefGuard
	usedRealloc      bool
	usedMemmove      bool
	usedTableHelpers bool
	funcs            map[string]FuncSig // registered function signatures
	// libStmts are the builtin library's top-level statements (the
	// program's LibStatements) with their module keys.
	libStmts map[ast.Statement]string
	// libGroups are each builtin module's top-level statements by module
	// key; libInits the modules whose init function is emitted.
	libGroups map[string][]ast.Statement
	libInits  map[string]bool
	// usedJmpStack: the program links jmpstack.c (the try frames' buffers).
	usedJmpStack bool
	usedFrames   bool
	// callAs is the receiver naming of the method call being emitted.
	callAs callAs
	// prePassSelf is the module binding whose initializer the globals
	// pre-pass is reading.
	prePassSelf string
	// objLitMethods are the object literals' function-valued properties.
	objLitMethods map[ast.Node]bool
	// memberFrame names the next class member emitClassMember emits, as
	// Node names its frame, and its class (frames.go).
	memberFrame struct {
		node ast.Node
		name string
	}
	// frameDescs are the shadow call stack's descriptors, by name and file;
	// funcFiles each function's file.
	frameDescs map[[2]string]string
	funcFiles  map[ast.Node]string
	// identFiles each identifier's file, built when an undeclared name is
	// assigned (sloppyAt).
	identFiles map[*ast.Identifier]string
	// usedStackGuard: main installs the stack-overflow handler (stackguard.c).
	usedStackGuard bool
	// contentDefined: the content-named helpers written (defineContentNamed).
	contentDefined map[string]bool
	// pendingHelpers: contentNamedHelper's helpers being emitted, true once
	// one is reached again before its body is done.
	pendingHelpers map[string]bool
	// inLib is the key of the builtin module whose code is being registered
	// or emitted, "" for the program's own (compatJS, closure names).
	inLib string
	// libClosures counts the closures named after each library position;
	// genericInst is the generic instantiation being emitted (literalSymbol).
	genericInst string
	libClosures map[string]int
	// callableClasses: the program's CallableClasses.
	callableClasses map[string]bool
	// forcedGlobalTypes: a top-level declaration promoted on the checker's
	// agreement with the inferred type (registerModuleGlobals).
	forcedGlobalTypes   map[*ast.VarDeclaration]Type
	interfaces          map[string]Type                  // named interface, type alias, and class registry
	interfaceMethodSigs map[string]map[string]FuncSig    // interface name → method name → signature (TDD-00009 Stage 4, `implements` conformance only — not used for dispatch)
	classes             map[string]ClassInfo             // named class registry (fields/ctor/methods) — see emit_classes.go
	workerUncaughtStub  bool                             // the no-op @__kml_worker_uncaught is defined (stubWorkerUncaught)
	unassignedFields    map[string]bool                  // "Class.field" a read may find unassigned (markUnassignedField)
	lowerTypeClasses    map[string]*ast.ClassDeclaration // @lowerType classes by mangled name (lower_type_class.go)
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
	// ambientGenericMiss caches names registerAmbientGeneric found nothing for.
	ambientGenericMiss map[string]bool
	genericClasses     map[string]*ast.ClassDeclaration
	// generators holds one entry per top-level `function*` declaration
	// (TDD-00061/ADR-00172), keyed by its bare source name — deliberately
	// separate from funcs, since constructing a generator (`gen(args)`)
	// never emits a plain `call` to the generator's own compiled body
	// function the way an ordinary named-function call does; see
	// emit_generators.go.
	generators map[string]*GeneratorInfo
	// alsBindTramps memoizes the monomorphic AsyncLocalStorage.bind trampolines
	// by closure-signature key (TDD-00168 Stage 4) — see emit_asynchooks.go.
	alsBindTramps map[string]bool
	// asyncGenStepFns memoizes the per-generator-type deferred `.next()` microtask
	// step function, keyed by the generator struct IR (TDD-00094).
	asyncGenStepFns map[string]string
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
	// stableIDKeys is the key each stable-range type id was hashed from
	// (stableTypeID), to catch two keys colliding.
	stableIDKeys map[int64]string
	// genericInstTypes memoizes genericClassInstanceType per instantiation;
	// genericInstBuilding holds the ones whose fields are being built.
	genericInstTypes    map[string]Type
	genericInstBuilding map[string]bool
	// genericInstSrc is each instantiation's class and type arguments, so a
	// method call through a `Box<number>` annotation instantiates it before
	// any `new Box<number>` is emitted.
	genericInstSrc map[string]genericInstSource
	// genericIfaceTypes memoizes complete generic interface instantiations
	// by their type arguments' full shapes.
	genericIfaceTypes map[string]Type
	// layoutIDs/layouts: the object layouts given a type id for their header
	// word (objheader.go), interned by layoutKey.
	layoutIDs   map[string]int64
	layouts     []registeredLayout
	enums       map[string]map[string]Value // enum name → member name → constant value
	enumBacking map[string]Type             // enum name → backing value type (double numeric, ptr string)
	enumObjects map[string]string           // enum name → the module constant holding it as a value
	// enumComputed: enum name → computed member → the module constant its
	// value is evaluated into where the enum is declared.
	enumComputed map[string]map[string]string
	// inHeapPrim is set while a heap primitive (heap_prims.go) emits.
	inHeapPrim bool
	// heapCheck is KML_HEAPCHECK as this emitter was made (heap_check.go).
	heapCheck string
	// recvMsgEmitted: __kml_recv_msg (emitReceiverMessage) is written.
	recvMsgEmitted bool
	// inHostRow names the builtin constructor whose static get row is being
	// emitted (emitHostRefGetRows): boxing it there would read the row itself.
	inHostRow string
	// fnStaticHosts maps a builtin constructor's function-value header
	// (`Number` by value) to the constructor whose statics it reads.
	fnStaticHosts map[string]string
	hostGetters   map[string]string // emitted host static getters (hostStaticGetter)
	usedBufFill   bool              // buffill.c is linked (ensureBufFill)
	closureCtr    int               // monotonically increasing counter for unique closure names
	// fnValueTrampolines memoizes the env-dropping trampoline emitted for each
	// named function taken by value (`const g = f`), keyed by its mangled LLVM
	// name — see emit_func_value.go.
	fnValueTrampolines map[string]bool
	intrinsicSigs      map[string]FuncSig // an intrinsic used as a value (emit_intrinsics.go)
	usedHypot          bool
	// fnValueHeaders memoizes the static `{trampoline, null}` closure header
	// emitted once per named function taken by value, keyed by its mangled LLVM
	// name. Emitting it once (as a global constant) rather than malloc'ing a
	// fresh header at every reference gives a named function a *stable* value
	// identity, so a listener removal (`removeEventListener(f)`,
	// `emitter.off(f)`) matches an earlier registration — see
	// emit_func_value.go.
	fnValueHeaders map[string]bool
	// nestedFuncScopes/nestedFuncCtr — TDD-00057. One nestedFuncScope frame
	// per enclosing function/closure body currently being emitted, pushed
	// by pushNestedFuncScope and popped once that body finishes; searched
	// innermost-first by resolveFuncRef so a nested function's name is
	// visible throughout its own enclosing body (hoisted, self-recursive,
	// visible to its own further-nested descendants) without ever being
	// entered into the flat, whole-program e.funcs map.
	nestedFuncScopes []nestedFuncScope
	nestedFuncCtr    int
	// sigInferInProgress holds the declarations whose unannotated return
	// type buildFunctionSig is currently inferring. Re-entry for the same
	// declaration (a recursive nested function whose first reachable return
	// is the recursive call itself, reached back through the letrec
	// identifier inference or inferBlockReturnExpr) keeps the void default
	// instead of recursing without bound.
	sigInferInProgress map[*ast.FunctionDeclaration]bool
	// sigInferCycleHit is set when that guard trips, telling
	// inferUnannotatedReturnType the candidate return it just inferred was
	// poisoned by the cycle so it should try the next return statement
	// (`if (n > 0) return f(n - 1); return p + "done";` must infer from the
	// base case, not void).
	sigInferCycleHit bool
	// prog is the whole program AST, retained for `typeof value` resolution
	// (finding a referenced value's top-level declaration before value scope is
	// populated).
	prog *ast.Program
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
	// typeofResolving guards against infinite recursion while resolving a
	// self- or mutually-referential `typeof value` query (ADR-00389).
	typeofResolving map[string]bool
	// resolveTypeDepth bounds resolveType recursion so a self-referential type
	// bails to a fallback instead of overflowing the stack.
	resolveTypeDepth int
	// typeParamScope maps a generic declaration's type-parameter names to the
	// concrete types of the instantiation being built (its signature, body,
	// and return-type inference). resolveType consults it first, so a type
	// parameter is substituted at *any* nesting depth — `Promise<T>`,
	// `Map<string, T>`, `T[][]`, `(x: T) => T` — not just the bare-T / T[]
	// parameter positions substituteGenericType handles on its own. Pushed
	// and popped by withTypeParamScope; nil outside any instantiation.
	typeParamScope map[string]Type
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
	// objLitClassCtr names each synthetic anonymous class an object literal
	// with getters/setters is lowered to (TDD-00153) — one per literal site.
	// objLitClasses maps each such literal to its synthetic class name (so the
	// type inferred for the variable slot matches the emitted value);
	// objLitEmitted tracks which synthetic classes' method bodies were emitted.
	objLitClassCtr int
	// recvTempCtr names receiverTemp's locals.
	recvTempCtr int
	// receiverPassed are the calls receiverlessThisCall built.
	receiverPassed map[*ast.CallExpression]bool
	objLitClasses  map[*ast.ObjectLiteral]string
	objLitEmitted  map[string]bool
	// currentCtorClass is the class whose constructor body is being emitted
	// (TDD-00154), or "" outside any constructor — a write to a `readonly` field
	// is allowed only when this equals the field's declaring class.
	usedStrlen bool
	usedStrchr bool
	// inspectDepthCap overrides the inspector's default recursion cap for the
	// duration of a single console.dir call; inspectDepthSet gates it (so a valid
	// depth of 0 is distinguishable from "no override").
	inspectDepthCap      int
	inspectDepthSet      bool
	usedTtyRead          bool // TDD-00031: the tty shim (klain:tty's `@link tty`)
	usedTui              bool // TDD-00150: klain:tui — Yoga layout + ANSI diff painter
	usedMemcpy           bool
	usedMemset           bool
	usedStrcmp           bool
	usedSprintf          bool
	usedStrncmp          bool
	usedStringTrim       bool
	usedStringTrimStart  bool
	usedStringTrimEnd    bool
	usedStringToLower    bool
	usedStringReplace    bool
	usedStringReplaceAll bool
	usedStringSplit      bool
	usedAtoll            bool
	usedIPow             bool
	usedByteBlockAlloc   bool
	usedHeapUsable       bool
	usedReceivedInt      bool
	usedJSONStringifyNum bool
	usedJSONStringifyStr bool
	// jsonToJSONActive guards JSON.stringify's toJSON() dispatch against a
	// class whose toJSON() returns its own (or a mutually-referencing) type,
	// which would recurse forever at compile time (cf. ADR-00221). A class
	// name present here is mid-serialization; re-entry serializes it as a
	// plain object instead of re-dispatching toJSON.
	jsonToJSONActive  map[string]bool
	usedAnyEq         bool
	usedDynObj        bool
	usedDynArr        bool
	usedArrLitRows    bool // __kml_arr_lit_rows (ADR-01064)
	usedInspectReduce bool // inspect_reduce.c (ADR-01067)
	usedCasemap       bool // casemap.c — Unicode toUpperCase/toLowerCase
	usedGCInterface   bool // the GC interface declared (runtime_worker.go)
	usedCollate       bool // collate.c — localeCompare's Unicode collation
	usedStringC       bool // string.c — lowered String.prototype methods
	usedNumberC       bool // number.c — lowered Number.prototype methods
	// fnmeta.go (TDD-00229 Stage A): function-value metadata keyed by code
	// pointer, the NamedEvaluation names of anonymous literals, and whether
	// fnmeta.c + its table are needed.
	usedFnMeta         bool
	usedBigIntBoxHooks bool // emit_bigint_box.go: boxed-bigint hooks the dynamic runtime calls
	// emit_ffi_runtime.go (TDD-00229): the node:ffi registry is linked; one
	// dynamic-ABI thunk per signature identity; the last validated name length.
	usedFFIRegistry bool
	usedUmapOrder   bool
	fnMetas         []fnMetaEntry
	// dynEntryWidened names the class-typed parameters a dynamic entry
	// binds as `any` while its body is emitted (emit_dynentry.go).
	dynEntryWidened map[string]bool
	// anyArgTyped is set while a call's typed entry is emitted for
	// arguments the any-argument dispatch already checked (emit_anyarg.go).
	anyArgTyped bool
	// anyArgBoxRet asks the next emitAnyArgDispatch for a boxed result
	// (anyArgReturnsBox; identifierCallRule sets it).
	anyArgBoxRet bool
	// shadowKeys is shadowWrittenKeys' memo; noShadowRead is set while
	// emitShadowableRead emits the field itself.
	shadowKeys   map[string]bool
	noShadowRead bool
	// anyHeldMemo caches anyHeldDecl.
	anyHeldMemo map[*ast.VarDeclaration]bool
	// anyAssigned caches anyAssignedDecls.
	anyAssigned map[*ast.VarDeclaration]bool
	// chainCopies maps a copy of an optional chain's link that emission
	// builds (the link no longer optional) to the link it copies.
	chainCopies       map[ast.Expression]ast.Expression
	fnMetaSeen        map[string]bool
	fnLitNames        map[ast.Node]string
	usedOSInfo        bool         // osinfo.c — os.type/release/…/networkInterfaces, process.env enumeration
	usedOSInfoBags    bool         // the IR bag builders over osinfo.c (process.env value, networkInterfaces)
	usedDynObjEntries bool         // @__kml_dynobj_entries — Object.entries/values on a dynamic object
	closureBoxOnEntry map[int]bool // the closure being emitted: params annotated `any` whose ABI is the hint type (boxed on entry, ADR-01080)
	// tmplCalls memoizes each tagged template's desugared call, and
	// tmplSites each template object's cache global (emit_call.go).
	tmplCalls             map[*ast.TaggedTemplateExpression]*ast.CallExpression
	tmplSites             map[*ast.CallExpression]string
	usedDynJSONFromNode   bool
	usedDynJSONC          bool
	usedShapeRuntime      bool // the layout table + shape.c (emit_shape.go)
	usedShapeSpread       bool
	libExports            map[string]string // "<module>:<name>" → merged name of a builtin TS module declaration
	shapeMethodRecs       map[string]string // class.method → its dynamic-function record
	boxedLayouts          map[int64]bool    // type ids of layouts boxed into a dynamic value
	usedFdlibm            bool              // fdlibm.c: Math's transcendental functions
	promiseBoxRunners     map[string]string // element representation → its Promise<any> reaction
	wkSymbols             map[string]bool   // well-known symbols emitted (emit_symbol.go)
	usedPromiseResolveAny bool
	// unhandledReportDone records that @__kml_unhandled_report is defined.
	unhandledReportDone bool
	// synthTypes are the types synthesized source names by annotation
	// (synthTypeAnnotation).
	synthTypes           map[string]Type
	usedPromiseWrapperOf bool
	genExprCtr           int                      // generator expressions compiled as values
	fromEntriesCtr       int                      // Object.fromEntries over non-string keys
	weakSeedCtr          int                      // new WeakMap(entries)/new WeakSet(values)
	dynKeysForIn         bool                     // emitDynAnyKeys serves for...in: walk the prototype chain
	newTargetFn          string                   // the prototype constructor being emitted (new.target)
	dynConstructing      bool                     // emitDynConstructDispatch: an abstract class constructs too
	newTargetDecl        *ast.FunctionDeclaration // newTargetFn's declaration
	promiseConvRunners   map[string]string
	anyToLayoutFns       map[int64]string // layout header → its any→layout conversion (emit_shape.go)
	usedNanBox           bool
	usedAnyOps           bool
	usedAnyToPrimitive   bool
	usedAnyLooseEq       bool
	// dynFnCtr numbers @__kml_dynfn_N dynamic-ABI functions (TDD-00155
	// Stage 4); jsProtoCtor marks top-level function declarations recognized
	// as vanilla-JS prototype constructors under -compat=js.
	dynFnCtr int
	// sharedLitUnit names the shared unit (shared_<name>) a generated
	// helper's literals belong to while it is built: named by position, the
	// same in every program, as a library module's are.
	sharedLitUnit string
	// protoBuilders are the __kml_proto_get_<Class> builders written.
	protoBuilders map[string]bool
	// primReadKeys are the keys a dynamic read on a primitive may name
	// (emitPrimProtoOfFinalize).
	primReadKeys map[string]bool
	jsProtoCtor  map[string]bool
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
	sloppyWrite      bool
	// nativeIfaceMembers: the own members of each interface over a native
	// layout (nativeHeritage), by interface name.
	nativeIfaceMembers map[string]map[string]Type
	// typedUse: whether each program names a TypedArray class
	// (usesTypedArrays).
	typedUse  map[*ast.Program]bool
	dynSloppy bool
	// selfNameAssigns: assignments to a named generator expression's own
	// name, renamed to its declaration (renameGeneratorSelfName).
	selfNameAssigns map[*ast.AssignmentExpression]bool
	// genExprNames: each nested generator expression's declaration name.
	genExprNames     map[*ast.FunctionExpression]string
	protoCtorOwnThis map[string]bool
	// foreignThisBody: a class method's body is being compiled for a
	// foreign receiver (emitForeignReceiverCall), its `this` any.
	foreignThisBody bool
	foreignClass    *ClassInfo // the class whose method body foreignThisBody compiles
	protoBagEmitted map[string]bool
	errSubCount     int64
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
	usedDateNow             bool
	usedPerformanceNow      bool
	usedUnitReg             bool                        // unitreg.go
	unitTables              map[int]*unitTable          // unitreg.go: registered rows by kind
	islandUnitRows          map[string]map[int][]string // unitreg.go: a dynamic-only file's rows (lazy), by its hash and kind
	islandUnitFiles         map[string]bool             // unitreg.go: the dynamic-only files whose bodies register them
	curDynFile              string                      // the hash of the dynamic-only file whose body is being emitted (lazy)
	ctors                   []string                    // functions run before main
	hookInits               []string                    // IR the hooks constructor runs (runtime_hooks.go)
	ctorRows                map[string]*ctorRow         // emit_classref_rows.go, by payload
	ctorRowOrder            []string
	usedClassIs             bool // unitreg.go: __kml_class_is
	emittedConstructorOf    bool
	usedDateDecompose       bool
	usedDaysFromCivil       bool
	usedDateParse           bool
	usedDateCompose         bool
	usedDateFields          bool
	usedDateNameTables      bool
	usedFetch               bool
	usedFetchAsync          bool
	usedLoopTurn            bool // emitted code waits on the event loop: main() runs it (runtime_loop_turn.go)
	usedLoopTurnDefs        bool
	// usedEventLoop: loopsrc/loop.c is linked, compiled with loopCFlags.
	usedEventLoop          bool
	loopCFlags             []string
	moduleTask             bool // the entry program has a top-level await: its module body is a coroutine task (TDD-00224)
	usedModuleTaskRuntime  bool
	usedPromiseAdopt       bool            // runtime_promise_adopt.go
	fetchBodyPromTypedCtr  int             // per-call-site typed json() runners (emit_fetch.go)
	topLevelNames          map[string]bool // every name bound at module top level (registerModuleGlobals)
	usedFetchSlotToPromise bool
	usedFsWatchRuntime     bool
	usedThreadPool         bool
	usedProcessUptime      bool
	// refiningCall marks a call emitCall is converting to the checker's
	// function type (checkerRefinedCallType), so the inner emit is plain.
	refiningCall         map[*ast.CallExpression]bool
	usedCurrentRSS       bool
	usedProcessLifecycle bool
	testTrampolines      map[string]bool
	usedWorkerRuntime    bool
	hasWorkers           bool // set at EmitProgram start from Program.WorkerModules
	// currentWorkerMod is non-empty while a worker module's entry function
	// is being emitted; workerRegistry is main's start-up registration of
	// the worker modules (emit_worker.go).
	currentWorkerMod string
	workerRegistry   string
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
	// stmtNS maps a namespace's top-level statement to the namespace.
	stmtNS map[ast.Statement]string
	// TDD-00099: shared memory + channels.
	usedWeakHelpers bool
	// usedIPC links the fork IPC framing C (TDD-00141); usedIPCChildRuntime
	// marks the child-side channel surface (process.send/.on('message')),
	// which hooks the event loop.
	usedIPC             bool
	usedIPCChildRuntime bool
	// usedWebview links the C++ system-webview binding (TDD-00142).
	usedWebview bool
	// usedSync links the klain:sync goroutine runtime (TDD-00143).
	usedSync bool
	// usesSyncProgram is set from prog.UsesKlainSync before body emission: the
	// program imports klain:sync, so the safepoint-insertion pass emits
	// cooperative preempt checks at function entry and loop back-edges.
	usesSyncProgram bool
	// webviewBindings records the typed window.* bindings for --emit-window-dts.
	webviewBindings []webviewBindingSig
	// embeddedDirs maps a compile-time-literal directory path to the IR symbol
	// its packed blob is linked under (TDD-00142 Stage 7); usedEmbeddedAssets
	// gates the embed_assets.c static-server runtime.
	embeddedDirs             map[string]string
	usedEmbeddedAssets       bool
	usedEmbedRuntime         bool
	embedSymbols             map[string]bool
	usedAtomicsRuntime       bool
	usedSqliteNatives        bool
	usedCaresNatives         bool // the resolver on c-ares (caressrc/klaincares.c) compiled in
	usedH2ONatives           bool // klain:http's h2o backend (h2osrc/klainh2o.c) compiled in
	usedFFINatives           bool
	usedAnyBigIntRel         bool
	usedPendingFinishSettled bool
	usedFetchAwaitSettled    bool
	usedCurlSlist            bool
	// usedFFIDl guards the one-time libdl decls (TDD-00164, runtime_ffi.go).
	usedFFIDl                    bool
	usedFsThrow                  bool
	usedErrnoCode                bool
	usedErrnoDesc                bool
	optRecvCtr                   int
	usedBase64Encode             bool
	usedBase64Decode             bool
	usedUtf8LabelCheck           bool
	usedBase64EncodeBytes        bool
	usedEncodeURIComponent       bool
	usedEncodeURI                bool
	usedEncodeFileURLPath        bool
	usedDecodeURIComponent       bool
	usedDecodeURIComponentStrict bool
	usedDecodeURIStrict          bool
	usedCryptoRandomBytes        bool
	usedWeakNatives              bool // weak.c's WeakRef natives (kml_weakref.ts)
	usedViews                    bool // views.c: typed arrays' buffer/byteOffset (TDD-00243)
	usedBoxedViews               bool // a typed array is boxed: its buffer/byteOffset read through `any`
	usedCryptoFillAny            bool
	usedDOMExcCode               bool
	usedCryptoRandomUUID         bool
	cryptoSubtleAliases          map[string]bool
	usedCloseDecl                bool
	usedStrHeaderRuntime         bool
	usedMemmem                   bool
	usedHeapStats                bool
	usedReadDecl                 bool
	usedWriteDecl                bool
	usedFcntlDecl                bool
	usedProcessCwd               bool
	usedGetpid                   bool
	needMicrotaskTick            bool // emitted code calls @__kml_microtask_tick (a top-level await's tick)
	inferDepth                   int  // nesting of memoised inferExprType calls (infer_memo.go)
	inferMemo                    []inferMemoLayer
	// condTypeDepth is how many conditional types resolveConditionalType is
	// inside; a recursive one is cut off at maxCondTypeDepth.
	condTypeDepth int
	// condTypeWork counts the conditional types resolved under the outermost
	// one; past maxCondTypeWork the rest resolve as `any` (a type whose
	// branches each instantiate it again grows exponentially, not deep).
	condTypeWork int
	// condTypeTotal counts every conditional type resolved in the program;
	// past maxCondTypeTotal the rest resolve as `any`, bounding the repeats
	// of one expansion from each place that names it.
	condTypeTotal int
	// genericAtDepth holds the instantiations built under the current
	// top-level generic interface instantiation, by depth and arguments.
	genericAtDepth map[string]Type
	// constArrayTypes caches the type of an array literal of constants
	// (constLiteralTree): it is the same under any scope, and a large
	// table's elements are otherwise re-typed by every pre-pass over it.
	constArrayTypes         map[*ast.ArrayLiteral]Type
	usedExecPath            bool
	usedNodeInterpGuard     bool
	usedHTTPClientReactions bool
	usedSignalHandler       bool
	usedSigpipeIgnored      bool // TDD-00214: SIGPIPE ignored once at reactor start
	usedStrerror            bool
	usedSymbolRegistry      bool
	usedFetchHeadersMap     bool
	usedFetchHeadersRaw     bool
	// errorShadowedSlots: the system-error slots (`code`, `errno`, …) some
	// Error subclass declares as its own field (ADR-01319).
	errorShadowedSlots    map[string]bool
	usedXHRHeadersAll     bool
	usedConsoleGroupDepth bool
	usedConsoleTimer      bool
	usedConsoleCountMap   bool
	usedMapFree           bool
	usedClosureFree       bool
	usedTimers            bool
	// libMembers are the builtin declaration files' method signatures
	// (isLibMember), collected on first use.
	libMembers map[ast.Node]bool
	// callOrigins maps a call codegen synthesized for a builtin's lowering
	// to the program's call the checker typed.
	callOrigins map[*ast.CallExpression]*ast.CallExpression
	// jsonKnownTabs numbers the key tables emitJSONProjectObject emits.
	jsonKnownTabs       int
	usedJSONExpandoRest bool
	// usedTLSHandles: the TypeScript tls module's primitives (tlshandle.c).
	usedTLSHandles       bool
	usedFiber            bool
	usedGeneratorRuntime bool
	generatorBodyCtr     int
	usedMathFuncs        bool
	usedFptosiSat        bool
	usedFloatMinMax      bool
	usedToNumber         bool
	usedBswap16          bool
	usedBswap32          bool
	usedBswap64          bool
	usedRoundEven        bool
	usesBufferCodecs     bool
	usedMemcmp           bool
	declaredBufferCodecs bool
	usedWsSpan           bool
	usedJsPow            bool
	namespaces           map[string]map[string]bool
	// nsAliases maps a fully-resolved alias name (dotted for a namespace-
	// scoped `export import`) to the dotted namespace it targets (ADR-00456).
	nsAliases map[string]string

	usedTaskRuntime      bool
	usedPromiseRuntime   bool // the promise struct + __kml_task_alloc_promise, without the fiber scheduler (TDD-00084 Part A)
	usedUnhandledRuntime bool // unhandled-rejection tracking (emit_unhandled.go)
	// forwardMethodRet is a class's methods' inferred results while it is
	// registered, for a call to a sibling declared later.
	forwardMethodRet map[string]Type
	// forwardMethods names a class's own methods (accessors aside) while it
	// is registered: a method read as a value before its signature is.
	forwardMethods     map[string]bool
	globalLinks        map[string]string // TDD-00232: a linked global's name -> its module's class
	recordFns          map[string]bool   // record-view read/write routines emitted (emit_record_view.go)
	recordFnList       []recordFn        // their bodies, written at finalize
	usedObjExtensible  bool              // @__kml_obj_extensible is called (emitObjExtensibleFinalize)
	usedStrFromCodes   bool
	usedElemKindWidth  bool            // @__kml_elem_kind_width (emit_fetch_body.go)
	ambientMiss        map[string]bool // names with no usable builtin-declaration interface (emit_ambient.go)
	usedObjExtra       bool            // @__kml_obj_has_extra and its hooks are called (emitObjExtraFinalize)
	objExtraEmitted    bool
	errorGetKeyDone    bool
	dynGetInline       bool              // emitting a dynGetHelper body
	dynGetHelpers      map[string]string // property name → its dynamic-get routine
	dynSetInline       bool
	dynSetHelpers      map[string]string
	extraTypes         []Type // static types whose added properties a whole-object operation reads
	usedShapeKeysArray bool
	usedShapeDesc      bool
	usedOwnSymbols     bool // Object.getOwnPropertySymbols (shape.c's KML_OWN_SYMBOLS)
	usedArrayGuard     bool
	// Host handles boxed into `any` (emit_hostbox.go).
	hostLayouts []hostLayout
	// protoSlots are the builtin prototype objects' global slots emitted;
	// usedProtoOf marks a program that maps an instance to its builtin
	// class's prototype (emitBuiltinProtoOf).
	protoSlots map[string]bool
	// protoReach* are the builtin prototype methods the program can reach
	// (proto_reach.go).
	protoReachBuilt, protoReachAll bool
	protoReachNames                map[string]bool
	usedProtoOf                    bool
	// mapLayouts are the Map and Set instantiations created; mapViewFns the
	// view dispatchers requested (TDD-00258).
	mapLayouts              []hostLayout
	mapViewFns              map[string]mapViewReq
	usedMapView             bool
	mapViewDone             map[string]bool
	unboxingTriStr          bool              // emitUnboxBoxToType's string null pass
	usedKeyOrder            bool              // keyorder.c is linked (ensureKeyOrder)
	usedPropAttr            bool              // propattr.c linked
	propAttrScan            int               // 0 unscanned, 1 no defineProperty, 2 some
	wrapperScan             int               // 0 unscanned, 1 no wrapper object built, 2 some
	usedWrapper             bool              // wrapper.c linked
	usedHoles               bool              // holes.c linked
	holesScan               int               // 0 unscanned, 1 no hole producer, 2 some
	idxHeader               string            // emitIndexBase's array header, "" for none
	joinHoles               string            // the header emitArrayJoinCore tests for holes
	spreadElisions          []string          // an elision's index after a spread, as emitted
	inDynValueOf            bool              // emitting emitDynPrimValueOf's object arm
	keyOrderBlocks          map[string]string // constant order blocks by content
	keyOrderNone            bool              // @__kml_keyorder_none is defined
	hostRendered            int
	hostIterated            int
	hostIterUsed            bool
	hostIterFns             map[int64]string
	classRefs               map[string]string // class → its constructor reference payload (emit_classref.go)
	declaredClassRefInspect bool              // __kml_classref_inspect_at declared
	usedDynInstanceof       bool
	usedDynConstruct        bool
	emittedDynConstruct     bool
	usedConstructorOf       bool
	classExprSeq            int // hoisted class expressions (emit_classexpr.go)
	classExprCaptures       map[*ast.ClassExpression]string
	// classCaptures is each closure-converted class's hidden fields
	// (emit_classclosure.go), by class name; classCapturesPending holds
	// them until the hoisted name is final, and classCapFromThis marks a
	// `new` inside such a class's body, which copies them from `this`.
	classCaptures        map[string][]classCapture
	classCapturesPending map[*ast.ClassDeclaration][]classCapture
	classCapFromThis     map[*ast.NewExpression]bool
	// classCaptureRejects is a block class naming a function local that
	// closure conversion cannot express: the local and why.
	classCaptureRejects map[*ast.ClassDeclaration][2]string
	// classRecords is each closure-converted class used as a value: every
	// evaluation makes a record (TDD-00242); classRecordsPending holds them
	// until the hoisted name is final. usedClassRec declares the runtime.
	classRecords map[string]bool
	// staticAreas are the record classes' static layouts (TDD-00242), and
	// classStaticCaps the locals their static members read, held in hidden
	// static fields.
	staticAreas            map[string]*staticArea
	classStaticCaps        map[string][]classCapture
	classStaticCapsPending map[*ast.ClassDeclaration][]classCapture
	classRecordsPending    map[*ast.ClassDeclaration]bool
	classRecordSites       map[ast.Node]bool // the in-block copy that makes the record
	usedClassRec           bool
	inCtorArm              bool           // emitting a __kml_dyn_construct arm
	ambientOverloads       map[string]int // ambientOverloaded's counts
	// funcSeeded names a module `var` with an initializer that shares a
	// function declaration's name; funcSeedOrder lists them in order.
	funcSeeded      map[string]bool
	funcSeedOrder   []string
	classShownNames map[string]string                               // class → its display name, where the class expression's own name differs
	genShownNames   map[string]string                               // generator → its display name, where the generator expression's own name differs
	literalMembers  map[*ast.IndexExpression]*ast.MemberExpression  // builtinLiteralMember's nodes
	staticInitAt    map[*ast.ClassDeclaration]bool                  // program class → its static init runs at its declaration
	brandOnly       bool                                            // instanceof tests a private brand: the constructor's, not the prototype chain
	dunderRead      bool                                            // emitDynProtoRead reads `x.__proto__`, not Object.getPrototypeOf
	staticCompanion map[*ast.ClassDeclaration]*ast.ClassDeclaration // generic class declaration → the class holding its statics
	classReeval     map[ast.Node]*ast.ClassDeclaration              // in-function evaluation site → hoisted class whose statics it re-initializes (emit_classexpr.go)
	usedClassRefGet bool
	hostRefs        map[string]bool // builtin constructors boxed as values (emit_classref.go)
	usedCtorKind    bool
	// usedClassRefProto: a class reference's prototype was read
	// (@__kml_classref_proto, fnproto.go).
	usedClassRefProto    bool
	emittedClassRefProto bool // __kml_classref_proto is defined
	// tupleArrayCalls memoizes tupleArrayCall's rewrites, so emission and
	// inference see one call.
	exactClassSlots         map[string]bool   // a `const x = new C()` binding's slot: x is a C (foreignFieldTags)
	arrayVariantOf          map[string]string // an Array class variant's class (arrayClassVariant)
	speciesInner            bool              // emitting a species method's own call on a body (arrayBodySpeciesRule)
	curCallee               ast.Expression    // the callee of the call being emitted (notFunctionMsg)
	methodValueSlots        map[string]bool   // a `const f = k.greet` binding's slot (mayBeMethodValue)
	tupleArrayCalls         map[*ast.CallExpression]*ast.CallExpression
	usedHostBox             bool
	usedPromiseBox          bool                 // a promise was boxed (emitPromiseInspectHook)
	inArrayOverrideDispatch bool                 // emitArrayOverrideDispatch's default branch: Array's own method
	declaredAnyArrView      bool                 // __kml_anyarr_view/sync declared (emitDynArrayMethodCall)
	anyArrElemFns           map[string][2]string // KJ_BOXED routines by element type (anyArrayElemRoutines)
	typeOnlyGenerics        map[string]bool      // classes whose type parameters codegen erased (eraseTypeOnlyGenerics)
	respCollectDefined      bool                 // @__kml_resp_collect defined (emit_response_new.go)
	respCollectCalled       bool                 // a body method calls @__kml_resp_collect
	usedZeroCell            bool                 // @__kml_zero_cell (emit_fetch.go)
	usedStrcasecmp          bool
	combinatorCtr           int  // Promise combinator runners (emit_promise_combinators.go)
	usedPromiseSettle       bool // @__kml_promise_settle — bare-promise settle+wake+drain for new Promise(executor) (TDD-00087)
	usedFetchDriveRunner    bool // @__kml_fetch_drive_run — deferred raw-fetch drive microtask for .then on a fetch
	usedPromiseAdoptRunner  bool // @__kml_promise_adopt_runner — thenable adoption for resolve(aPromise) (TDD-00091)
	usedAwaitTimerDrive     bool // a lightweight await references @__kml_timer_fire_next (TDD-00087)
	usedMicrotasks          bool
	usedCoreC               bool
	usedCollections         bool
	usedCollectionsAny      bool
	usedCurrentTaskGlobal   bool
	usedAsyncCtxAccessors   bool
	// programUsesALS is set in EmitProgram before Pass 2: true when the
	// program has async_hooks (lib/node/async_hooks.ts), so timer callbacks
	// are wrapped to carry the async context across the schedule→fire
	// boundary (TDD-00168 Stage 3).
	programUsesALS bool
	// programUsesFinReg is a whole-program pre-scan result (set in EmitProgram
	// before Pass 2): true when the program constructs a FinalizationRegistry
	// anywhere, so Memory.free sites — possibly emitted before the construction
	// — include the finalizer-lookup hook (TDD-00163 Stage 2).
	programUsesFinReg     bool
	usedFinRegHelpers     bool // ensureFinalizationHelpers emit-once guard
	finregCount           int  // unique-name counter for per-site trampoline/report fns
	hasMaySuspend         bool // any async fn classified may-suspend (TDD-00083 Stage 2)
	usedCbrt              bool
	usedCtlz32            bool
	usedArc4Random        bool
	usedStrtoll           bool
	usedParseIntBase      bool
	usedStrtod            bool
	usedStrtodJS          bool
	usedStrtodParseFloat  bool
	usedQsort             bool
	usedSortTrampolineArr bool
	usedSortCmpI64Lex     bool
	usedSortCmpF64Lex     bool
	usedSortCmpStr        bool
	usedSortTrampolineI64 bool
	usedSortTrampolineF64 bool
	usedSortTrampolineStr bool
	usedSortTrampolineObj bool
	usedSortTrampolineAny bool
	usedSortCmpAnyLex     bool
	usedAnyToCStr         bool
	optionalCallCtr       int
	usedSortClosGlobal    bool
	usedMapStrHelpers     bool
	usedMapNumHelpers     bool
	usedMapAnyHelpers     bool
	usedMapClear          bool
	usedMapIters          bool
	usedNullRef           bool
	// notNullRef holds fromThreeState's registers, plain strings that are
	// never nullRef (asTriStr).
	notNullRef             map[string]bool
	usedDateLocal          bool
	declaredDateParseStr   bool
	declaredRegexGroups    bool
	usedJSONConcat2        bool
	usedStreamRuntime      bool
	usedWStreamRuntime     bool
	usedStreamPipeRuntime  bool
	usedAwaitFetchHeaders  bool
	usedFetchBodyStream    bool
	usedFetchBodyProm      bool              // lazy Response body promises settled off the reactor (TDD-00186)
	fetchBodyPromRunner    map[string]string // method (text/json/arrayBuffer) → synthesized settle-runner name
	usedZlibStreamRuntime  bool
	usedPromiseAddReaction bool
	usedAnyIterCollect     bool // __kml_any_iter_collect (emit_iter_collect.go)
	usedAnyIterTake        bool // __kml_any_iter_take (emit_iter_collect.go)
	usedAnyObjRest         bool // __kml_any_obj_rest (emit_dynobj_destr.go)
	usedObjProtoFill       bool // __kml_objproto_fill (emit_builtin_proto.go)
	// destrSource: a declaration's destructuring source, for the next
	// dynamic object pattern's coercibility message (emitRequireDestructurable).
	destrSource             ast.Expression
	usedZlibNatives         bool            // klainzlib.c compiled into the pool (-DKLAINPOOL_ZLIB)
	usedFsNatives           bool            // the program uses the fs natives
	usedH2Node              bool            // http2src/h2node.c (lib/node/http2.ts's natives)
	inDynBufferCall         bool            // emitDynBufferMethodCall's non-Buffer branch
	inDynNumberCall         bool            // emitDynNumberToString's non-number branch
	topLevelCallables       map[string]bool // top-level function and class names (initRefsCallable)
	usedAnyArrayLikeCollect bool            // __kml_any_arraylike_collect (emit_iter_collect.go)
	usedExceptionHelpers    bool
	usedIntegrityLevels     bool // the header-word integrity runtime (ensureIntegrityLevels)
	usedPathNormalize       bool
	usedRegexCompile        bool
	usedRegexCompileContext bool
	usedRegexParseFlags     bool
	usedRegexValidateFlags  bool
	usedRegexMatch          bool
	usedRegexUTF16Convert   bool
	usedRegexUTF8Width      bool
	usedRegexESNormalize    bool
	usedRegexESClasses      bool
	usedRegexExpand         bool
	synthDisplay            map[string]string                // a synthesized binding's name in messages (displayName)
	recordSpreadInner       bool                             // emitRecordSpreadLiteral is building one of its two forms
	recordTypeofOwn         bool                             // emitting typeof of a structural member's own-layout read
	implicitThis            map[*ast.FunctionExpression]bool // a method shorthand's `this: any` (maybeAddThisParamExpr)
	usedRegexFlagsCanon     bool
	usedRegexSourceNorm     bool
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
	autoFreeDeep map[*ast.VarDeclaration]bool
	deepFreeFns  map[string]string
	deepFreeCtr  int
	// TDD-00134 Stage 1 (-optimize-memory): declarations whose object-literal
	// initializer is emitted into an entry-block alloca instead of calloc.
	// pendingStackAllocLit marks the exact literal node while its declaration
	// is being emitted; stackAllocatedLits records the literals actually
	// stack-emitted (so maybeRegisterAutoFree never frees an alloca).
	// wantTupleAggregate is the one-shot handshake between the tuple
	// destructuring fast path and emitCallToFuncSig (TDD-00134 Stage 3):
	// set right before emitting a direct call to a TupleByVal function,
	// grab-and-cleared at the call's entry so nested argument calls never
	// see it; the call then returns the raw aggregate instead of spilling.
	wantTupleAggregate   bool
	stackAllocPlan       map[*ast.VarDeclaration]bool
	pendingStackAllocLit ast.Expression
	stackAllocatedLits   map[ast.Expression]bool
	// classStackAudit memoizes classStackEligible per class name (may a
	// non-escaping instance of it be stack-allocated?).
	classStackAudit map[string]bool
	// Stage 3 (@owned): the last-use statement per owned binding / per owned
	// parameter — the early-free point; entries stay on pendingFrees as the
	// block-exit safety net and are marked emitted once the early free lands.
	autoOwnedLastUse      map[*ast.VarDeclaration]ast.Statement
	autoOwnedParamLastUse map[*ast.FunctionDeclaration]map[string]ast.Statement
	// pendingLabel is set by a LabeledStatement just before emitting its body;
	// the next loop to start consumes it via pushPendingLabel. Non-loop bodies
	// leave it unconsumed, so the label is simply never registered.
	pendingLabel string
	usedFree     bool
	usedExit     bool
	usedGetenv   bool
}

func NewEmitter() *Emitter {
	e := &Emitter{
		funcBuilder:             &funcBuilder{},
		heapCheck:               os.Getenv("KML_HEAPCHECK"),
		fnDecls:                 make(map[string]bool),
		refiningCall:            make(map[*ast.CallExpression]bool),
		strConsts:               make(map[string]string),
		strConstRaw:             make(map[string]string),
		moduleGlobals:           make(map[string]Symbol),
		promotedGlobalDecls:     make(map[*ast.VarDeclaration]bool),
		classDeclName:           make(map[*ast.ClassDeclaration]string),
		forcedGlobalTypes:       make(map[*ast.VarDeclaration]Type),
		varsBeingInitialized:    make(map[string]bool),
		funcs:                   make(map[string]FuncSig),
		interfaces:              make(map[string]Type),
		interfaceMethodSigs:     make(map[string]map[string]FuncSig),
		classes:                 make(map[string]ClassInfo),
		decoratedMethodSlots:    make(map[string]map[string]string),
		standardFieldInitSlots:  make(map[string]map[string]string),
		standardInitListGlobals: make(map[string]string),
		enums:                   make(map[string]map[string]Value),
		enumBacking:             make(map[string]Type),
		enumObjects:             make(map[string]string),
		jsonToJSONActive:        make(map[string]bool),
		genericFuncs:            make(map[string]*ast.FunctionDeclaration),
		topFuncDecls:            make(map[string]*ast.FunctionDeclaration),
		nestedFuncSrc:           make(map[string]ast.SourceRange),
		classSrc:                make(map[string]ast.SourceRange),
		genericInterfaces:       make(map[string]*ast.InterfaceDeclaration),
		genericTypeAliases:      make(map[string]*ast.TypeAliasDeclaration),
		genericClasses:          make(map[string]*ast.ClassDeclaration),
		staticCompanion:         make(map[*ast.ClassDeclaration]*ast.ClassDeclaration),
		generators:              make(map[string]*GeneratorInfo),
		alsBindTramps:           make(map[string]bool),
		asyncGenStepFns:         make(map[string]string),
		fnValueTrampolines:      make(map[string]bool),
		intrinsicSigs:           make(map[string]FuncSig),
		fnValueHeaders:          make(map[string]bool),
		testTrampolines:         make(map[string]bool),
		funcContext:             &funcContext{currentRetType: TypeI32}, // main returns i32
	}
	e.pushScope()
	e.scopes[0].top = true
	return e
}

// varRedeclarationTarget reports the binding a `var name` re-declaration
// refers to, when there is one: JS `var` is function-scoped, so a second
// `var x` in the same function (a `for (var i …)` after an earlier `var i`,
// or two such loops in a row — the Test262 `S12.14_A11` shape) names the
// existing variable and its initializer is an assignment, not a new
// binding that shadows the first (ADR-00728). Only the current function's
// own frame counts: a closure's captured cells (IsCapture) and an outer
// function's variables are not re-declared by an inner `var`.
func (e *Emitter) varRedeclarationTarget(name string) (Symbol, bool) {
	if len(e.scopes) == 0 {
		return Symbol{}, false
	}
	if sym, ok := e.scopes[0].syms[name]; ok && !sym.IsCapture {
		return sym, true
	}
	if e.scopes[0].top {
		if sym, ok := e.moduleGlobals[name]; ok {
			return sym, true
		}
	}
	return Symbol{}, false
}

// SetOptions sets every compile mode at once (TDD-00230 P2.6): the CLI
// builds one options.Options and hands the same value to the program's
// emitter and to each dynamic-import island's. The Set* methods below each
// change one mode, for tests.
func (e *Emitter) SetOptions(o options.Options) { e.opts = o }

// Options returns the compile modes.
func (e *Emitter) Options() options.Options { return e.opts }

// Toolchain is the clang invocation for this program's compile target.
func (e *Emitter) Toolchain() Toolchain { return Toolchain{Target: e.opts.Target} }

// SetMemMode selects the compile-wide memory-management mode ("manual", the
// zero-value default, or "gc"). Called by main.go right after NewEmitter()
// — not a constructor argument, so every existing zero-arg NewEmitter() call
// site (mainly tests/compiler_test.go) keeps working unchanged.
func (e *Emitter) SetMemMode(mode string) { e.opts.MemMode = mode }

// SetIslandHash marks this compile as a shared-library island (TDD-00056) with
// the given stable hash; EmitProgram then appends the island's run-once init
// and per-export accessor functions (`__kml_dynmod_<hash>_*`) and the output is
// linked with `clang -shared` instead of as an executable.
func (e *Emitter) SetIslandHash(hash string) { e.islandHash = hash }

func (e *Emitter) isGCMode() bool { return e.opts.MemMode == "gc" }

// isAutoMode reports the -mm=auto mode (TDD-00173): plain malloc/free like
// manual, but the compiler inserts frees at proven-safe points and
// Memory.free is a compile error (the compiler owns every free).
func (e *Emitter) isAutoMode() bool { return e.opts.MemMode == "auto" }

// SetRegexMode selects the compile-wide RegExp dialect mode (TDD-00067's
// `-regex=` flag): "pcre" (raw PCRE2, today's behavior), "es-ascii" (Option
// A compile-option alignment), "es-unicode" (Option B, + PCRE2_UTF and
// NEWLINE_ANY), or "es-utf16" (es-unicode matching + UTF-16 code-unit offset
// reporting, Stage 3). The zero value "" means "unset" and resolves to the
// highest implemented ES stage — see resolveRegexMode. Called by main.go
// right after NewEmitter(), like SetMemMode, so zero-arg NewEmitter() call
// sites (tests) keep the default without threading a mode through.
func (e *Emitter) SetRegexMode(mode string) { e.opts.Regex = mode }

// BigIntBackend returns the resolved backend name ("" → the libtommath default).
func (e *Emitter) BigIntBackend() string {
	if e.opts.BigInt == "" {
		return "libtommath"
	}
	return e.opts.BigInt
}

// SetCryptoBackend selects the compile-wide crypto backend (TDD-00104),
// called by main.go from the -crypto flag. "" resolves to the default,
// openssl (libcrypto, all platforms); "commoncrypto" opts into Apple
// CommonCrypto + Security.framework (macOS only).
func (e *Emitter) SetCryptoBackend(mode string) { e.opts.Crypto = mode }

// CryptoBackend returns the resolved backend name ("" → the openssl default).
func (e *Emitter) CryptoBackend() string {
	if e.opts.Crypto == "" {
		return "openssl"
	}
	return e.opts.Crypto
}

// WebviewBackend returns the resolved backend name ("" → the system default).
func (e *Emitter) WebviewBackend() string {
	if e.opts.Webview == "" {
		return "system"
	}
	return e.opts.Webview
}

// UsesCrypto reports whether the emitted program actually used crypto.subtle,
// so main.go only compiles+links a crypto backend for programs that need one.
func (e *Emitter) UsesCrypto() bool { return e.usesCrypto }

// UsesBigInt reports whether the emitted program actually used bigint, so
// main.go only compiles+links a backend for programs that need one.
func (e *Emitter) UsesBigInt() bool { return e.usesBigInt }

// UsesWorkers reports whether the program spawns Worker threads (TDD-00098)
// — main.go adds -pthread to the clang invocation when it does.
func (e *Emitter) UsesWorkers() bool { return e.usedWorkerRuntime }

// exitCall is the call that ends the program with code (an i32 operand): on
// a Worker's thread it ends only that worker (runtime_worker.go).
func (e *Emitter) exitCall(code string) string {
	if e.hasWorkers {
		return "call void @__kml_thread_exit(i32 " + code + ")"
	}
	return "call void @exit(i32 " + code + ")"
}

// isolateTLS qualifies a global holding program state: a program with
// Workers gives each thread its own copy, as each Node Worker is its own
// isolate that evaluates its modules afresh.
func (e *Emitter) isolateTLS() string {
	if e.hasWorkers {
		return "thread_local "
	}
	return ""
}

// UsesFFIDl reports whether the program uses node:ffi (dlopen/dlsym, TDD-00164).
// main.go adds the FFI link flags (see FFILinkFlags) when it does.
func (e *Emitter) UsesFFIDl() bool { return e.usedFFIDl }

// SetCompatMode selects the whole-program compatibility axis (TDD-00075):
// "" / "strict" (default — the compiler's opinionated, safer-than-JS
// semantics) or "js" (best-effort JS-faithful). Governs behaviors with a
// genuine strict-vs-JS tradeoff; global-shadowing (the old -globals flag) is
// handled resolver-side, so this drives the emitter-side inhabitants.
func (e *Emitter) SetCompatMode(mode string) { e.opts.Compat = mode }

// SetNoAny enables --no-any (TDD-00209): the any/unknown escape hatch is banned.
// The CLI only sets it under the strict lane (ignored with a warning under
// -compat=js), so noAnyMode need not re-check the mode.
func (e *Emitter) SetNoAny(on bool) { e.opts.NoAny = on }

// SetOptimizeMemory toggles TDD-00134's allocation optimizations (Stage 1:
// escape analysis → stack allocation of non-escaping object literals).
func (e *Emitter) SetOptimizeMemory(on bool) { e.opts.OptimizeMemory = on }

// SetFinalizersMode selects the FinalizationRegistry exit diagnostics
// (TDD-00163 Stage 4): "off" (default) or "report" (one leak line per
// registration still live at exit under -mm=manual).
func (e *Emitter) SetFinalizersMode(mode string) { e.opts.Finalizers = mode }

// SetEmitDecoratorMetadata toggles TypeScript's emitDecoratorMetadata behavior
// (TDD-00161 Stage 3): design:type/paramtypes/returntype metadata for decorated
// members.
func (e *Emitter) SetEmitDecoratorMetadata(on bool) { e.opts.EmitDecoratorMetadata = on }

// SetDecoratorDialect selects the decorator dialect (TDD-00161 Stage 5):
// "experimental" (default) or "standard" (TC39). Empty means experimental.
func (e *Emitter) SetDecoratorDialect(d string) { e.opts.Decorators = d }

// standardDecorators reports whether the TC39 `(value, context)` dialect is
// selected.
func (e *Emitter) standardDecorators() bool { return e.opts.Decorators == "standard" }

// compatJS reports whether JS-faithful compatibility mode is active.
func (e *Emitter) compatJS() bool { return e.opts.CompatJS() && e.inLib == "" }

// libScope marks the code emitted from here on as the builtin library's (a
// statement of a module written in TypeScript), which compiles in the strict
// lane whatever lane the program uses; the returned func restores.
func (e *Emitter) libScope(st ast.Statement) func() {
	saved := e.inLib
	e.inLib = e.libStmts[st]
	return func() { e.inLib = saved }
}

// userStatements is prog's body without the builtin library's statements:
// what the js lane's whole-program inference passes read.
func (e *Emitter) userStatements(prog *ast.Program) *ast.Program {
	if len(e.libStmts) == 0 {
		return prog
	}
	cp := *prog
	cp.Body = nil
	for _, st := range prog.Body {
		if e.libStmts[st] == "" {
			cp.Body = append(cp.Body, st)
		}
	}
	return &cp
}

// resolveRegexMode maps the raw flag (including "" for unset) to the
// concrete mode actually used for codegen. The default resolves to the
// highest ES stage implemented so far — "ecmascript" as of Option C
// (TDD-00067): es-unicode matching plus the source-normalization pass, byte-
// indexed so it stays consistent with the string layer. es-utf16 is NOT the
// default (its UTF-16 index space intentionally diverges from .charCodeAt/
// .slice — ADR-00208); a program wanting spec-exact offsets selects it
// explicitly. This is the single place the default is defined.
func (e *Emitter) resolveRegexMode() string {
	if e.opts.Regex == "" {
		return "ecmascript"
	}
	return e.opts.Regex
}

// --- Scope ---

func (e *Emitter) pushScope() {
	e.scopes = append(e.scopes, scope{syms: make(map[string]Symbol)})
	e.inferMemoScopePushed()
}
func (e *Emitter) popScope() {
	e.scopes = e.scopes[:len(e.scopes)-1]
	e.inferMemoScopePopped()
}

func (e *Emitter) define(name string, sym Symbol) {
	// A class type captured before the class's fields were registered (a
	// signature built during registration) binds as the live class.
	if sym.Ty.IsClass() && !sym.Ty.IsArray() {
		sym.Ty = e.canonicalizeClassTy(sym.Ty)
	}
	e.scopes[len(e.scopes)-1].syms[name] = sym
	e.inferMemoDefined()
	if e.tryDepth > 0 {
		e.pinSlotAcrossSetjmp(sym.Ptr)
	}
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
// pinSlotAcrossSetjmp makes a local's alloca visible to LLVM as escaped
// memory, so its value survives a `try` (ADR-01057). `try` is setjmp/longjmp
// (emitTry): the catch block is a CFG successor of the setjmp call, not of the
// try body, so for a non-escaping alloca SSA promotion forwards the value the
// slot held *at the setjmp* into the catch — `let x = 0; try { x = 1; throw …
// } catch { x }` read 0. Once the address has been handed to an opaque inline
// asm (the classic `asm volatile("" :: "r"(p) : "memory")` escape) every call
// — the setjmp, the throw — may read or write it, so the try body's store is
// materialized and the catch reloads it. The pin goes into the entry block,
// which dominates everything, next to the alloca; only real allocas of the
// current function qualify (a heap-boxed capture cell is a malloc'd pointer,
// already opaque). Called from lookup for every local touched inside a
// try/catch/finally body; cost is confined to those slots.
func (e *Emitter) pinSlotAcrossSetjmp(ptr string) {
	if ptr == "" || !strings.HasPrefix(ptr, "%") {
		return
	}
	allocas := e.allocas.String()
	if !strings.Contains(allocas, "  "+ptr+" = alloca ") {
		return
	}
	pin := fmt.Sprintf("call void asm sideeffect \"\", \"r,~{memory}\"(ptr %s)", ptr)
	if strings.Contains(allocas, pin) {
		return
	}
	e.emitAlloca(pin)
}

// hoistedVarMaySkip reports whether the `var` declaration being emitted can be
// skipped on some path to a later read of its (function-scoped) binding: it
// sits in a nested block — an `if`/loop/`switch`/`try` body — rather than
// the function's own statement list, or it is dead code after a terminator
// (`break; var b = "x"`). Such a binding reads `undefined` on that path in
// JS, so emitVarDecl widens it to `T | undefined` (ADR-01057). A `for` init
// clause is exempt (see forInitDepth). The hand-off is deliberately
// syntactic: a nested-block `var` read only inside its block pays a
// presence bit it never needs, which is a cost, not a divergence.
func (e *Emitter) hoistedVarMaySkip() bool {
	if e.blockDone {
		return true
	}
	return len(e.scopes) > 1 && len(e.scopes) != e.forInitDepth
}

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
			if e.tryDepth > 0 {
				e.pinSlotAcrossSetjmp(s.Ptr)
			}
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
// binding — checked first at every ambient-global dispatch site
// (Math/JSON/console/process/fetch/...) before falling back to that name's
// built-in meaning: a program's declaration of a builtin's name is its own
// binding, in every lane (ADR-01333).
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
	r := fmt.Sprintf("%%t%d", n)
	if len(e.notNullRef) > 0 {
		delete(e.notNullRef, r) // a name reused in another function
	}
	return r
}

func (e *Emitter) freshLabel(prefix string) string {
	n := e.labelCtr
	e.labelCtr++
	return fmt.Sprintf("%s.%d", prefix, n)
}

// --- Emission helpers ---

func (e *Emitter) emitGlobal(line string) { e.globals.WriteString(withSite(line) + "\n") }
func (e *Emitter) emitAlloca(line string) { e.allocas.WriteString("  " + withSite(line) + "\n") }

// emitGuardPanic is the sentinel a codegen invariant guard panics with; it is
// recovered at the EmitProgram boundary and turned into a clean compile error
// rather than a Go stack trace. Used by the malformed-store rail below.
type emitGuardPanic struct{ msg string }

// malformedStoreValueOperand reports whether a `store` instruction line carries
// an EMPTY value operand — the signature of an unimplemented expression that
// returned a zero `Value{}` (empty Ref) whose Ref was then formatted straight
// into `store <ty> <val>, ptr …`, yielding `store <ty> , ptr …` (invalid IR
// clang later rejects with an opaque scratch-file error). Valid stores always
// have a non-empty value token between the type and the first comma.
func malformedStoreValueOperand(line string) bool {
	rest := strings.TrimPrefix(line, "store ")
	// Skip the type token.
	rest = strings.TrimLeft(rest, " ")
	sp := strings.IndexByte(rest, ' ')
	if sp < 0 {
		return false // no space after type — not the shape we guard
	}
	rest = strings.TrimLeft(rest[sp:], " ")
	// After the type + whitespace, a well-formed store has a value token; the
	// bug leaves a bare comma here (empty operand).
	return rest == "" || rest[0] == ','
}

// scalarConstIntoAggregateStore reports whether a store line writes a bare
// scalar constant into an aggregate-typed ("{ … }") slot — e.g.
// `store {ptr, i64} 0, ptr %p` — which is invalid IR (an aggregate has no scalar
// zero; its constant is `zeroinitializer` or an aggregate literal). A store of a
// register (`%r`), `zeroinitializer`, or an aggregate literal (`{ … }`) into the
// same slot is well-formed and passes. Only a `{`-prefixed store type is
// considered, so scalar stores are untouched.
func scalarConstIntoAggregateStore(line string) bool {
	rest := strings.TrimLeft(strings.TrimPrefix(line, "store "), " ")
	if len(rest) == 0 || rest[0] != '{' {
		return false // not an aggregate-typed store
	}
	close := strings.IndexByte(rest, '}')
	if close < 0 {
		return false
	}
	val := strings.TrimLeft(rest[close+1:], " ")
	if val == "" {
		return false
	}
	// A bare numeric constant operand (`0`, `-1`, `1.5`, `0x…`) is the invalid
	// shape; a register (`%…`), `zeroinitializer`, `null`, `getelementptr(…)`,
	// or a nested aggregate literal (`{…`) are all well-formed.
	c := val[0]
	return c == '-' || (c >= '0' && c <= '9')
}

func (e *Emitter) emitInstr(line string) {
	if e.blockDone {
		return // skip dead code after a terminator
	}
	if e.heapCheck != "" && !e.inHeapPrim {
		e.heapCheckInstr(line)
	}
	if strings.HasPrefix(line, "store ") && malformedStoreValueOperand(line) {
		// Turn silent invalid IR into a loud, clean rejection at the emitter — a
		// permanent rail so no unimplemented expression can ship a malformed store
		// past codegen into clang. See "Invalid-IR backlog" cluster B.
		panic(emitGuardPanic{msg: "unsupported operation: an expression produced no value and cannot be stored — this construct is not yet implemented"})
	}
	if strings.HasPrefix(line, "store ") && scalarConstIntoAggregateStore(line) {
		// A scalar constant stored into an aggregate slot (`store {ptr, i64} 0`)
		// is invalid IR — it arises when a value is coerced to an aggregate-typed
		// target (an array/tuple/nullable slot) but the source register (a bare
		// scalar zero/constant) is kept, e.g. a mismatched generator `.next(x)`
		// value whose element type is a union the coerce mis-represents. A clean
		// rejection at the emitter rather than shipping the bad store to clang.
		if os.Getenv("KML_GUARD_STACK") != "" {
			fmt.Fprintln(os.Stderr, "guard:", line)
		}
		panic(emitGuardPanic{msg: "unsupported operation: a non-matching value was coerced to an aggregate-typed slot (array/tuple/union) — this construct is not yet supported"})
	}
	e.body.WriteString("  " + withSite(line) + "\n")
}

// emitTerminator emits a terminator instruction and marks the block as done.
func (e *Emitter) emitTerminator(line string) {
	if e.blockDone {
		return
	}
	if e.frame != "" && strings.HasPrefix(line, "ret ") {
		e.leaveFrame()
	}
	e.body.WriteString("  " + withSite(line) + "\n")
	e.blockDone = true
}

// emitLabel starts a new basic block, resetting the terminator flag.
func (e *Emitter) emitLabel(label string) {
	e.body.WriteString(label + ":\n")
	e.blockDone = false
}

// --- String constants ---

// strHeaderWord is the header word of a read-only string holding s: its byte
// length with its UTF-16 length already counted (kml_heap.h's layout), since
// a constant cannot take the count later.
func strHeaderWord(s string) int64 {
	var u int64
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b&0xC0 != 0x80 {
			u++
		}
		if b >= 0xF0 {
			u++
		}
	}
	return int64(len(s)) | (u+1)<<33
}

func (e *Emitter) internString(s string) string {
	if ref, ok := e.strConsts[s]; ok {
		return ref
	}
	esc, length := escapeLLVM(s)
	if isErrorKindName(s) || sema.IsBuiltinConstructor(s) || s == "Promise" {
		// A constructor's name is also its reference's payload (`TypeError`
		// as a value, an error's `constructor`), compared by address: one
		// symbol per name, which every unit's copy links to (TDD-00238).
		name := "@__kml_ctorname." + s
		e.emitGlobal(fmt.Sprintf("%s = linkonce_odr constant { i64, [%d x i8] } { i64 %d, [%d x i8] c\"%s\" }, align 8", name, length, strHeaderWord(s), length, esc))
		ref := fmt.Sprintf("getelementptr inbounds ({ i64, [%d x i8] }, ptr %s, i32 0, i32 1)", length, name)
		e.strConsts[s] = ref
		e.strConstRaw[ref] = s
		return ref
	}
	// Named by content, so a string is spelled the same in every unit and
	// a builtin module's code is the same whatever program it is in
	// (TDD-00238 Stage 4).
	name := contentSymbol("@.s.", s)
	// TDD-00120 Stage 2: a string literal carries the same 8-byte length header
	// as a heap string — `{ i64 len, [N x i8] bytes }` — and the string *value*
	// is a pointer to the bytes (field 1), so `__kml_str_len(ptr)` reads the true
	// length from ptr-8 uniformly for literals and heap strings alike. The bytes
	// stay NUL-terminated, so every strlen consumer keeps working meanwhile.
	// One symbol per content, merged across units: a property key is
	// compared by address, so a separately compiled unit must name the same
	// copy (TDD-00238 Stage 4).
	e.emitGlobal(fmt.Sprintf("%s = linkonce_odr hidden constant { i64, [%d x i8] } { i64 %d, [%d x i8] c\"%s\" }, align 8", name, length, strHeaderWord(s), length, esc))
	ref := fmt.Sprintf("getelementptr inbounds ({ i64, [%d x i8] }, ptr %s, i32 0, i32 1)", length, name)
	e.strConsts[s] = ref
	e.strConstRaw[ref] = s
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
	// A local in scope (a function's, or a block's) shadows the top level.
	for i := len(e.scopes) - 1; i >= 0; i-- {
		if s, ok := e.scopes[i].syms[name]; ok {
			if !e.scopes[i].top {
				return s.Ty, true
			}
			break
		}
	}
	// A top-level declaration is checked first, from the AST, so `typeof value`
	// resolves to the *same* type whether it runs at eager registration (before
	// value scope exists) or at emit time — a promoted global's stored handle
	// type can differ from its structural shape, which would desync the two
	// passes.
	if e.prog != nil {
		match := func(vd *ast.VarDeclaration) (Type, bool) {
			// Top-level names carry the resolver's per-file `__kml_mod<N>`
			// suffix; a query may keep the source name.
			declName := vd.Name
			if i := strings.LastIndex(declName, "__kml_mod"); i > 0 {
				declName = declName[:i]
			}
			if declName != name && vd.Name != name {
				return Type{}, false
			}
			if vd.TypeAnnot != nil {
				return e.resolveType(vd.TypeAnnot), true
			}
			// -compat=js: an untyped object-literal binding is a dynamic
			// object held in an `any`, as emitVarDecl stores it.
			if _, isLit := vd.Init.(*ast.ObjectLiteral); isLit && e.compatJS() {
				return TypeAny, true
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
					return funcValueType(sig), true
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
		return funcValueType(sig), true
	}
	return Type{}, false
}

// resolveTypeofType resolves a `typeof value` type query (ADR-00389) to the
// referenced value's type, walking any trailing member path. An unresolvable
// query falls back to the number default rather than failing (resolveType has
// no error channel).
func (e *Emitter) resolveTypeofType(ta *ast.TypeAnnotation) Type {
	// `typeof C` of a class is its constructor reference, boxed, whether
	// or not the class is registered yet (an interface naming a class
	// declared after it).
	if len(ta.TypeofPath) == 0 {
		if _, isClass := e.classes[ta.TypeofName]; isClass {
			return TypeAny
		}
		if sym := e.lookupBuiltinType(ta.TypeofName); sym != nil && sym.Flags&binder.Class != 0 {
			return TypeAny
		}
	}
	base, ok := e.resolveValueType(ta.TypeofName)
	if !ok {
		return e.checkerTypeofType(ta)
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
			return e.checkerTypeofType(ta)
		}
		base = next
	}
	return base
}

// checkerTypeofType is a `typeof` query's type, or a declaration file's
// annotation, as the checker answers it (a function declaration's
// signature, which codegen holds as no value), else the untyped default.
func (e *Emitter) checkerTypeofType(ta *ast.TypeAnnotation) Type {
	if ta.TypeNode() != nil {
		if o := e.reprOracle(); o != nil {
			if t, ok := o.AnnotationType(ta); ok {
				return t
			}
		}
	}
	return TypeI64
}

// indexSignatureType builds the map-backed dynamic-object type for a string
// index signature `{ [k: string]: V }` (TDD-00130), matching
// inferDynamicObjectType's shape so the shared read/write/iterate machinery
// (TDD-00012) applies unchanged.
// mergeByteArrayMembers keeps one byte array of a union's members: a Buffer
// is a Uint8Array, one representation (`string | Buffer | Uint8Array`).
func mergeByteArrayMembers(members []Type) []Type {
	isBytes := func(t Type) bool {
		return t.IsTypedArray() && t.ElemType != nil && t.ElemType.IR == "i8" && !t.ElemType.Signed || t.IsBuffer()
	}
	keep := -1
	for i, m := range members {
		if isBytes(m) && (keep < 0 || m.IsBuffer()) {
			keep = i
		}
	}
	if keep < 0 {
		return members
	}
	var out []Type
	for i, m := range members {
		if isBytes(m) && i != keep {
			continue
		}
		out = append(out, m)
	}
	return out
}

// withDictFields attaches an index-signature dictionary's named properties.
func (e *Emitter) withDictFields(t Type, fields []ast.AnnotField) Type {
	for _, f := range fields {
		ft := e.resolveType(f.Type)
		if f.Optional {
			ft = undefinedableElem(ft)
		}
		t.DictFields = append(t.DictFields, Field{Name: f.Name, Ty: ft})
	}
	return t
}

func (e *Emitter) indexSignatureType(valAnnot *ast.TypeAnnotation) Type {
	valTy := e.resolveType(valAnnot)

	return dictViewType(valTy)
}

// isDictType reports a string-keyed dictionary type, in either
// representation (a map, or the object itself with a declared property
// type).
func isDictType(t Type) bool {
	return (t.IsDynamicObject() && t.IsMap()) || (isUnconstrainedDynamic(t) && t.DynPropTy != nil)
}

// dictValType is a dictionary type's property type.
func dictValType(t Type) (Type, bool) {
	switch {
	case t.IsDynamicObject() && t.IsMap() && t.MapVal != nil:
		return *t.MapVal, true
	case isUnconstrainedDynamic(t) && t.DynPropTy != nil:
		return *t.DynPropTy, true
	}
	return Type{}, false
}

// dictViewType is a string-keyed dictionary held as the object itself (an
// `any` box) whose every property has type valTy.
func dictViewType(valTy Type) Type {
	t := TypeAny
	t.DynPropTy = &valTy
	return t
}

func (e *Emitter) resolveType(ta *ast.TypeAnnotation) Type {
	if ta != nil {
		if t, ok := e.synthTypes[ta.Name]; ok {
			return t // synthesized source's exact type (synthTypeAnnotation)
		}
	}
	if ta != nil && ta.IsCtorType {
		return TypeAny // a constructor: its boxed reference (emit_classref.go)
	}
	t := annotationNullish(ta, e.decideAnnotationType(ta))
	if e.shadowOracle != nil && ta != nil {
		e.shadowAnnotationType(ta, t)
	}
	return t
}

// annotationNullish applies the null and undefined an annotation's parse
// recorded (`T | null | undefined`, an optional parameter's `undefined`).
func annotationNullish(ta *ast.TypeAnnotation, t Type) Type {
	if ta != nil && ta.Nullable && ta.Undefined && ta.HasNull && threeStateEligible(t) {
		t = withNullAndUndef(t)
	}
	// `Map<K, V> | undefined`, `Set<T> | null`, `T | undefined` for an object
	// T: a possibly-absent collection or object is its null pointer (the
	// Map/Set arms and a type parameter's substitution build the type alone).
	if ta != nil && ta.Nullable && (t.IsMap() || t.IsSet() || t.IsObject() || t.IsClass()) && t.IR == "ptr" && !t.IsArray() && !t.Nullable() {
		t.setNullable(true)
		t.setUndefined(ta.Undefined)
	}
	return t
}

// decideAnnotationType is resolveType's decider: the representation of an
// annotation.
func (e *Emitter) decideAnnotationType(ta *ast.TypeAnnotation) Type {
	if ta == nil {
		return TypeI64 // default for untyped numeric variables
	}
	if e.opts.NoAny {
		e.checkNoAnyAnnotation(ta)
	}
	// A declaration file's annotation is only its node: the checker's
	// type for it.
	if ta.NodeOnly() {
		return e.checkerTypeofType(ta)
	}
	// A global a global module implements (TDD-00232) names the module's
	// class; its declaration's type arguments are erased (`CustomEvent<T>`).
	if base := strings.TrimRight(ta.Name, "[]"); base != "" {
		if m, ok := e.globalLinks[base]; ok && !e.userTypeName(base) {
			linked := *ta
			linked.Name = m + ta.Name[len(base):]
			linked.TypeArgs = nil
			if base == ta.Name {
				linked.ElemType = nil // a lone type argument (`WeakRef<Box>`), erased
			}
			return e.decideAnnotationType(&linked)
		}
	}
	// An iteration-protocol interface outside a generator's own signature
	// (`it: AsyncIterable<T>`, `(): Generator<T>`) is any object with the
	// protocol's members — a generator of any shape, a class, a user object:
	// held as a dynamic value and driven by the run-time protocol (TDD-00230
	// phase 5). A generator declaration's own return annotation is read by
	// generatorElemAnnotation instead.
	if generatorWrapperNames[ta.Name] && len(ta.UnionMembers) == 0 && len(ta.TupleElems) == 0 {
		if _, own := e.genericInterfaces[ta.Name]; !own {
			if _, own := e.interfaces[ta.Name]; !own {
				return TypeAny
			}
		}
	}
	// TypeScript's `Function` (the interface of every callable): any
	// function, held as a dynamic value, so it calls through the dynamic path
	// and keeps its identity, `name` and own properties. TypeScript's
	// `Object` (the interface every non-nullish value fits, an array or a
	// function included) is held the same way.
	if base := strings.TrimRight(ta.Name, "[]"); (base == "Function" || base == "Object") && len(ta.UnionMembers) == 0 && len(ta.TypeArgs) == 0 && ta.ElemType == nil && !e.userTypeName(base) {
		t := TypeAny
		for i := 0; i < (len(ta.Name)-len(base))/2; i++ {
			t = ArrayOf(t)
		}
		return t
	}
	// @types/node's `NodeJS.Dict<T>` / `NodeJS.ReadOnlyDict<T>` (`{ [key:
	// string]: T | undefined }`, the qualifier dropped): a string-keyed
	// dictionary of T, unless the program declares its own.
	if (ta.Name == "Dict" || ta.Name == "ReadOnlyDict") && len(ta.TypeArgs) == 1 {
		if _, own := e.genericInterfaces[ta.Name]; !own {
			if _, alias := e.genericTypeAliases[ta.Name]; !alias {
				return e.indexSignatureType(ta.TypeArgs[0])
			}
		}
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
	// A type parameter of the generic instantiation being built (see
	// typeParamScope): `T` anywhere inside `Promise<T>` / `T[][]` / a closure
	// type substitutes its concrete type. Only a bare name matches — a
	// user-generic `T<…>` or `T[]` (ElemType set) still resolves structurally
	// below, recursing back here for the inner `T`.
	if e.typeParamScope != nil && ta.ElemType == nil && len(ta.TypeArgs) == 0 && ta.UnionMembers == nil &&
		ta.IntersectionMembers == nil && !ta.IsFuncType && !ta.IsTypeof {
		if concrete, ok := e.typeParamScope[ta.Name]; ok {
			if ta.Nullable {
				concrete.setNullable(true)
				concrete.setUndefined(ta.Undefined)
			}
			return concrete
		}
		// `T[]` / `T[][]` spelled as one suffixed name (the parser's array
		// shorthand keeps the brackets in Name): peel one level and recurse.
		if base, isArr := strings.CutSuffix(ta.Name, "[]"); isArr {
			root := base
			for strings.HasSuffix(root, "[]") {
				root = strings.TrimSuffix(root, "[]")
			}
			if _, ok := e.typeParamScope[root]; ok {
				inner := *ta
				inner.Name = base
				inner.Nullable = false
				inner.Undefined = false
				inner.PeelArrayNode()
				t := ArrayOf(e.resolveType(&inner))
				if ta.Nullable {
					t.setNullable(true)
					t.setUndefined(ta.Undefined)
				}
				return t
			}
		}
	}
	// `typeof value` type query (ADR-00389): resolve the referenced value's type,
	// then walk any trailing member path (`typeof a.b`).
	if ta.IsTypeof {
		return e.resolveTypeofType(ta)
	}
	// `InstanceType<typeof C>` of a class (or a class expression's binding):
	// the class's instances.
	if ta.Name == "InstanceType" && len(ta.TypeArgs) == 1 && !e.userTypeName("InstanceType") {
		if q := ta.TypeArgs[0]; q != nil && q.IsTypeof && len(q.TypeofPath) == 0 {
			if _, isClass := e.classes[q.TypeofName]; isClass {
				return e.decideAnnotationType(&ast.TypeAnnotation{Name: q.TypeofName})
			}
		}
	}
	// String-literal type (`"north"`, TDD-00079): as a value type it is just a
	// string; the literal value is only consumed at the AST level by
	// Pick/Omit/Record key collection, not here.
	// Numeric-literal type (`1`, ADR-00459): as a value type it is just a
	// number, mirroring the string-literal stance below.
	// A union's head carries its first member's flags: a union is read by
	// the union branch below, whatever its first member.
	if ta.IsNumberLiteral && ta.UnionMembers == nil {
		return TypeF64
	}
	if ta.IsStringLiteral && ta.UnionMembers == nil {
		// String-shaped, but carry the literal value so a discriminated-union
		// tag field can be matched at compile time (TDD-00116). Behaves as
		// string everywhere else (isStringTy checks IR "ptr").
		return Type{IR: "ptr", Kind: kindStrLiteral, LitValue: ta.LiteralValue, Absent: absenceOf(ta.Nullable, ta.Nullable && ta.Undefined, false)}
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
	// One member beside null/undefined (`{ [k: string]: V } | null`) is that
	// member, nullable, as `T | null` on a named T is.
	if len(ta.UnionMembers) == 1 && ta.UnionMembers[0] != nil && ta.UnionMembers[0].UnionMembers == nil {
		one := *ta.UnionMembers[0]
		one.Nullable = one.Nullable || ta.Nullable
		one.Undefined = one.Undefined || ta.Undefined
		return e.resolveType(&one)
	}
	if ta.UnionMembers != nil {
		members := make([]Type, len(ta.UnionMembers))
		for i, m := range ta.UnionMembers {
			members[i] = e.resolveType(m)
		}
		members = mergeByteArrayMembers(members)
		// A member that is itself unconstrained (`object`, `any`, `unknown`)
		// makes the union the plain NaN box: any value may be in it.
		for _, m := range members {
			if isUnconstrainedDynamic(m) && !m.IsDynamicObject() && !m.IsNull() && !m.IsUndefined() {
				any := TypeAny
				any.setNullable(ta.Nullable)
				return any
			}
		}
		// Members of one primitive kind (`'dir' | 'file'`, `1 | 2`) are that
		// primitive: a union of one representation collapses to it.
		if kind := primitiveAnnotKind(ta.UnionMembers[0]); kind != "" && len(members) == len(ta.UnionMembers) {
			same := true
			for _, m := range ta.UnionMembers[1:] {
				if primitiveAnnotKind(m) != kind {
					same = false
					break
				}
			}
			if same {
				one := members[0]
				if one.IsStrLiteral() {
					one = TypePtr
				}
				if ta.Nullable {
					one = undefinedableElem(one)
					one.setUndefined(ta.Undefined)
				}
				return one
			}
		}
		// Members that all resolve to a string (a literal union alias beside
		// another literal: `BufferEncoding | 'buffer'`) are a string, as the
		// literal-only case above.
		allStr := len(members) > 0
		for _, m := range members {
			// Exactly a string's representation: a stream or other host
			// handle is a pointer too, and isStringTy does not tell them apart.
			allStr = allStr && (m.IsStrLiteral() || reprKey(m) == reprKey(TypePtr))
		}
		if allStr {
			members = []Type{TypePtr}
		}
		if len(members) == 1 {
			one := members[0]
			if ta.Nullable {
				one = undefinedableElem(one)
				one.setUndefined(ta.Undefined)
			}
			return one
		}
		return Type{IR: TypeAny.IR, Kind: kindDynamic, UnionMembers: members, Absent: absenceOf(ta.Nullable, false, false)}
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
		// A value of a host or class member (`Error & { port?: number }`) is
		// that object, carrying the other members' properties as its own: no
		// struct of the merged fields holds it, so it is read dynamically.
		for _, m := range members {
			if m.IsError() || m.IsClass() || (m.Host != 0 && !m.IsObject()) {
				return Type{IR: TypeAny.IR, Kind: kindDynamic, Absent: absenceOf(ta.Nullable, false, false)}
			}
		}
		// A branded primitive (`string & { __brand: "id" }`): its values are
		// that primitive's, the object members a compile-time tag.
		if prim, ok := brandedPrimitive(members); ok {
			prim.setNullable(ta.Nullable)
			return prim
		}
		fields, _ := mergeIntersectionFields(members)
		obj := UserObjectType(fields)
		obj.IntersectionMembers = members
		obj.setNullable(ta.Nullable)
		return obj
	}
	if ta.IsFuncType {
		params := make([]Type, len(ta.FuncParams))
		for i := range ta.FuncParams {
			params[i] = e.resolveType(&ta.FuncParams[i])
			if params[i].IR == "void" {
				// A `void` parameter (`value: T` with T = void) holds
				// whatever is passed: no value has the void IR type.
				params[i] = TypeAny
			}
		}
		ret := TypeVoid
		if ta.FuncRetType != nil {
			ret = e.resolveType(ta.FuncRetType)
		}
		ft := FuncType(params, ret)
		ft.FuncHasRest = ta.FuncHasRest
		ft.FuncThis = ta.FuncThis
		if len(ta.FuncParamOptional) > 0 {
			opt := make([]bool, len(params))
			for i := range opt {
				if i < len(ta.FuncParamOptional) {
					opt[i] = ta.FuncParamOptional[i]
				}
			}
			// A rest slot is always omittable.
			if ta.FuncHasRest && len(opt) > 0 {
				opt[len(opt)-1] = true
			}
			ft.FuncParamOptional = opt
		}
		// `(() => void) | undefined`: a function that may be absent (a null
		// closure pointer).
		if ta.Nullable {
			ft.setNullable(true)
			ft.setUndefined(ta.Undefined)
		}
		return ft
	}
	// A builtin generic whose declaration names its layout (`Promise<T>`,
	// `Map<K, V>`, the WHATWG streams), before the generic ElemType fallback
	// below, which would read any of them as a plain array.
	if t, ok := e.genericLayout(ta); ok {
		return withAnnotNull(t, ta)
	}
	// An instance of a generic class, `Box<number>`: the type its
	// `new Box<number>(…)` has.
	if genDecl, ok := e.genericClasses[ta.Name]; ok {
		explicit := ta.TypeArgs
		if len(explicit) == 0 && ta.ElemType != nil {
			explicit = []*ast.TypeAnnotation{ta.ElemType}
		}
		if targs, ok := classTypeArgs(genDecl, explicit); ok {
			if ty, err := e.genericClassInstanceType(genDecl, e.buildTypeArgSubs(genDecl.TypeParams, targs)); err == nil {
				return withAnnotNull(ty, ta)
			}
		}
	}
	// A registered generic interface (TDD-00010 V1 / TDD-00037), e.g.
	// Box<number> or Box<number, string> — must also be checked before the
	// generic ElemType fallback below, as the builtin generics are above.
	if genDecl, ok := e.genericInterfaces[ta.Name]; ok && len(ta.TypeArgs) < len(genDecl.TypeParams) {
		// Type arguments left out take the parameters' defaults (`A` is
		// `A<any>` for `interface A<T = any>`); one without a default is any.
		args := append([]*ast.TypeAnnotation{}, ta.TypeArgs...)
		for i := len(args); i < len(genDecl.TypeParams); i++ {
			var def *ast.TypeAnnotation
			if i < len(genDecl.TypeParameters) && genDecl.TypeParameters[i].Default != nil {
				if conv, err := ast.TypeAnnotationOf(genDecl.TypeParameters[i].Default, ""); err == nil {
					def = conv
				}
			}
			if def == nil {
				def = &ast.TypeAnnotation{Name: "any"}
			}
			args = append(args, def)
		}
		filled := *ta
		filled.TypeArgs = args
		ta = &filled
	}
	if len(ta.TypeArgs) > 0 {
		e.registerAmbientGeneric(ta.Name)
	}
	if aliasDecl, ok := e.genericTypeAliases[ta.Name]; ok && len(ta.TypeArgs) < len(aliasDecl.TypeParams) {
		// The same for a generic alias (`type A<T = string> = T[]`).
		args := append([]*ast.TypeAnnotation{}, ta.TypeArgs...)
		for i := len(args); i < len(aliasDecl.TypeParams); i++ {
			var def *ast.TypeAnnotation
			if i < len(aliasDecl.TypeParameters) && aliasDecl.TypeParameters[i].Default != nil {
				if conv, err := ast.TypeAnnotationOf(aliasDecl.TypeParameters[i].Default, ""); err == nil {
					def = conv
				}
			}
			if def == nil {
				def = &ast.TypeAnnotation{Name: "any"}
			}
			args = append(args, def)
		}
		filled := *ta
		filled.TypeArgs = args
		ta = &filled
	}
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
		subs := e.buildTypeArgSubs(genDecl.TypeParams, ta.TypeArgs)
		// Memoized per type arguments' full shapes: a member mentioning
		// another instantiation (`then<U>(...): Promise<U>`) rebuilt the
		// whole tree, fanning out per member down to the depth cap, at
		// every mention of the outer type. Only an instantiation built from
		// the top is complete to the cap; one built inside another is
		// truncated, so it is not kept.
		var key strings.Builder
		key.WriteString(ta.Name)
		for _, tp := range genDecl.TypeParams {
			key.WriteString("<" + reprKey(subs[tp]) + ">")
		}
		k := key.String()
		if t, ok := e.genericIfaceTypes[k]; ok {
			return t
		}
		top := e.genericDepth == 0
		// Inside one top-level instantiation, an instantiation is the same
		// at the same depth (the cap truncates it identically), so it is
		// built once per depth: the cap bounds depth, not breadth, and an
		// interface nested in itself through several members (`owner:
		// List<List<T>>`) otherwise multiplies at every level.
		if top || e.genericAtDepth == nil {
			// (A generic alias or class raises the depth too, so the first
			// interface under one may not be the top.)
			e.genericAtDepth = map[string]Type{}
		}
		dk := fmt.Sprintf("%d|%s", e.genericDepth, k)
		if t, ok := e.genericAtDepth[dk]; ok {
			return t
		}
		e.genericDepth++
		t := e.instantiateGenericInterface(genDecl, subs)
		e.genericDepth--
		e.genericAtDepth[dk] = t
		if top {
			if e.genericIfaceTypes == nil {
				e.genericIfaceTypes = map[string]Type{}
			}
			e.genericIfaceTypes[k] = t
		}
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
		bound := make(map[string]Type, len(aliasDecl.TypeParams))
		for i, param := range aliasDecl.TypeParams {
			if i < len(ta.TypeArgs) {
				subs[param] = ta.TypeArgs[i]
				bound[param] = e.resolveType(ta.TypeArgs[i])
			}
		}
		// The substituted body keeps the declaration's type nodes: its
		// parameters are bound in the scope too, as the checker's reading
		// of those nodes names them.
		var t Type
		e.genericDepth++
		e.withTypeParamScope(bound, func() { t = e.resolveType(substituteAnnotation(aliasDecl.Type, subs)) })
		e.genericDepth--
		return t
	}
	// Built-in utility types (TDD-00079) — after the user-generic registry so a
	// user-defined type of the same name still wins, before the generic ElemType
	// fallback below (which would otherwise misread `Partial<User>` as `User[]`).
	if len(ta.TypeArgs) > 0 && utilityTypeNames[ta.Name] {
		if ty, ok := e.resolveUtilityType(ta, ta.Name, ta.TypeArgs); ok {
			return ty
		}
	}
	// Tuple type `[T0, T1, ...]` (TDD-00066) — checked before the generic
	// ElemType/array fallback below.
	if ta.EmptyTuple {
		return TupleType(nil) // `[]`: an event with no arguments, a zero-length tuple
	}
	if len(ta.TupleElems) > 0 {
		elems := make([]Type, len(ta.TupleElems))
		for i, et := range ta.TupleElems {
			elems[i] = e.resolveType(et)
		}
		ty := TupleType(elems)
		if ta.Nullable { // `[T, U] | null` / `| undefined` (ADR-01063)
			ty.setNullable(true)
			ty.setUndefined(ta.Undefined)
		}
		return ty
	}
	if ta.ElemType != nil {
		arr := ArrayOf(e.resolveType(ta.ElemType))
		if ta.Nullable { // `(() => void)[] | null`, `Array<T> | undefined`
			arr.setNullable(true)
			arr.setUndefined(ta.Undefined)
		}
		return arr
	}
	// A string index signature `{ [k: string]: V }` (TDD-00130) resolves to the
	// existing map-backed dynamic-object representation (TDD-00012), giving it
	// `d[key]` read/write sugar and map iteration for free.
	if ta.IndexSig != nil {
		return e.withDictFields(e.indexSignatureType(ta.IndexSig), ta.Fields)
	}
	if len(ta.Fields) > 0 {
		fields := make([]Field, len(ta.Fields))
		for i, af := range ta.Fields {
			fty := e.resolveType(af.Type)
			// A `name?: T` optional field is `T | undefined` (TDD-00187
			// Stage 2): a scalar widens to the { i1, T } presence slot, a
			// pointer keeps null-as-absent — either way the calloc-zeroed
			// slot of an omitted field reads back as a real absent value.
			if af.Optional {
				fty = optionalFieldType(fty)
			}
			fields[i] = Field{Name: af.Name, Ty: fty, Optional: af.Optional}
		}
		ty := UserObjectType(fields)
		if ta.Nullable { // `{ … } | null` / `| undefined` (ADR-01063)
			ty.setNullable(true)
			ty.setUndefined(ta.Undefined)
		}
		return ty
	}

	// A timer handle: Node's Timeout and Immediate classes,
	// lib/node/internal_timers.ts's (TDD-00245).
	if len(ta.Qualifier) == 1 && ta.Qualifier[0] == "NodeJS" && (ta.Name == "Timeout" || ta.Name == "Immediate") {
		ty := TypeAny
		if m, ok := e.libExports["internal_timers:"+ta.Name]; ok {
			if _, isClass := e.classes[m]; isClass {
				ty = e.resolveType(&ast.TypeAnnotation{Name: m})
			}
		}
		if ta.Nullable {
			ty.setNullable(true)
			ty.setUndefined(ta.Undefined)
		}
		return ty
	}
	// A `NodeJS.*` type, before the registries below: its last segment alone
	// (`Platform`) may name a user type.
	if ty, ok := nodeJSNamespaceType(ta); ok {
		if ta.Nullable {
			ty.setNullable(true)
			ty.setUndefined(ta.Undefined)
		}
		return ty
	}
	// Named type: check interface registry before falling back to built-ins.
	name := ta.Name
	// Handle T[] where T is a named interface (e.g. "User[]") or enum
	// ("Color[]") — the parser hands a named-type array as a single "[]"-suffix
	// name, so ElemType is nil and the enum-backing lookup below wouldn't fire.
	if len(name) > 2 && name[len(name)-2:] == "[]" {
		base := name[:len(name)-2]
		// `User[] | null` is a possibly-absent array, as `number[] | null`.
		nullableArr := func(arr Type) Type {
			if ta.Nullable {
				arr.setNullable(true)
				arr.setUndefined(ta.Undefined)
			}
			return arr
		}
		if ty, ok := e.interfaces[base]; ok {
			return nullableArr(ArrayOf(ty))
		}
		if backing, ok := e.enumBacking[base]; ok {
			return nullableArr(ArrayOf(backing))
		}
		// A nested named array (`P[][]`) arrives as a single suffix-name whose base
		// is itself suffixed (`P[]`) — strip one level and resolve the rest
		// recursively, so every depth keeps the named element's real object type
		// rather than falling through to the i64 unknown-name default (which made
		// `const a: P[][] = [[{...}]]` reject its object elements as heterogeneous).
		if strings.HasSuffix(base, "[]") {
			return nullableArr(ArrayOf(e.resolveType(&ast.TypeAnnotation{Name: base})))
		}
	}
	if ty, ok := e.interfaces[name]; ok {
		if ta.Nullable {
			ty.setNullable(true)
			ty.setUndefined(ta.Undefined)
		}
		return ty
	}
	// A named enum used as a type annotation resolves to its backing value type
	// — i64 for a numeric enum, ptr for a string enum. Without this a string
	// enum annotation fell through to the i64 unknown-name default, so storing
	// a member's string pointer into the i64 slot was a clang type error.
	if backing, ok := e.enumBacking[name]; ok {
		if ta.Nullable {
			backing.setNullable(true)
			backing.setUndefined(ta.Undefined)
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
			ty.setNullable(true)
			ty.setUndefined(ta.Undefined)
		}
		return ty
	}
	ty := e.resolveTypeName(ta.Name)
	// A name no builtin table knows may be an interface the builtin
	// declarations declare (`RequestInit`): its object type.
	if name != "int64" && len(ta.Qualifier) == 0 && reflect.DeepEqual(ty, TypeI64) {
		if at, ok := e.ambientInterfaceType(name); ok {
			ty = at
		}
	}
	// A qualified builtin name no table knows (`NodeJS.ExitListener`): a box,
	// which holds whatever it names, rather than the numeric default.
	if len(ta.Qualifier) > 0 && reflect.DeepEqual(ty, TypeI64) {
		ty = TypeAny
	}
	if ta.Nullable {
		ty.setNullable(true)
		ty.setUndefined(ta.Undefined)
	}
	return ty
}

// nodeJSNamespaceType is the representation of a `NodeJS.*` type from
// lib/node.d.ts: the same one the matching `process` member infers to.
// `NodeJS.Process` is absent, since `process` is not a first-class value.
func nodeJSNamespaceType(ta *ast.TypeAnnotation) (Type, bool) {
	if len(ta.Qualifier) != 1 || ta.Qualifier[0] != "NodeJS" {
		return Type{}, false
	}
	switch ta.Name {
	case "Platform", "Architecture":
		return TypePtr, true
	case "ProcessEnv":
		return TypeAny, true
	case "MemoryUsage":
		return memoryUsageType(), true
	case "ErrnoException":
		return errorObjType, true // an Error with its code, errno, syscall and path
	case "ArrayBufferView", "TypedArray":
		// Any TypedArray or a DataView: several representations, boxed.
		return TypeAny, true
	}
	return Type{}, false
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
	// A name declared more than once (`var C = class {}; var C = class {}`)
	// holds a different class each time: its classes stay values.
	declared := map[string]int{}
	for _, stmt := range prog.Body {
		switch d := stmt.(type) {
		case *ast.VarDeclaration:
			declared[d.Name]++
		case *ast.ClassDeclaration:
			declared[d.Name]++
		case *ast.FunctionDeclaration:
			declared[d.Name]++
		}
	}
	for i, stmt := range prog.Body {
		vd, ok := stmt.(*ast.VarDeclaration)
		if !ok {
			continue
		}
		ce, ok := vd.Init.(*ast.ClassExpression)
		if !ok || declared[vd.Name] > 1 {
			continue
		}
		if !anonymousClass(ce) {
			// `const Box = class Impl {}`: declared as Box, its references
			// to Impl are to it, and it shows as Impl.
			resolver.RenameInClass(ce.Decl, ce.Decl.Name, vd.Name)
			if e.classShownNames == nil {
				e.classShownNames = map[string]string{}
			}
			e.classShownNames[vd.Name] = ce.Decl.Name
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
		// `var G = function* g() {}`: declared as G, its references to g
		// are to it, and it shows as g.
		if fe.Name != "" && fe.Name != vd.Name {
			ownParam := false
			for _, p := range fe.Params {
				ownParam = ownParam || p.Name == fe.Name
			}
			if !ownParam && fe.Body != nil {
				e.renameGeneratorSelfName(fe, vd.Name)
			}
			if e.genShownNames == nil {
				e.genShownNames = map[string]string{}
			}
			e.genShownNames[vd.Name] = fe.Name
		}
		fd := &ast.FunctionDeclaration{
			Name:        vd.Name,
			Params:      fe.Params,
			ReturnType:  fe.RetType,
			Body:        fe.Body,
			IsAsync:     fe.IsAsync,
			IsGenerator: true,
			Src:         fe.Src,
		}
		fd.SetPos(vd.GetPos())
		prog.Body[i] = fd
	}
}

func (e *Emitter) EmitProgram(prog *ast.Program) (ir string, err error) {
	defer func() {
		if err == nil && len(e.convertGaps) > 0 {
			err = fmt.Errorf("internal compiler error: no conversion %s", strings.Join(e.convertGaps, "; "))
		}
	}()
	// Recover a codegen invariant-guard panic (e.g. the malformed-store rail in
	// emitInstr) into a clean compile error instead of a Go stack trace.
	defer func() {
		if r := recover(); r != nil {
			if g, ok := r.(emitGuardPanic); ok {
				if os.Getenv("KML_GUARD_STACK") != "" {
					debug.PrintStack()
				}
				ir, err = "", fmt.Errorf("%s", g.msg)
				return
			}
			panic(r)
		}
	}()
	// The semantic passes between parsing and codegen (TDD-00230): today,
	// deciding which `new X(…)` constructs a builtin. Every entry into codegen
	// comes through here, so every compile path runs them exactly once.
	if err := sema.Prepare(prog); err != nil {
		return "", err
	}
	e.libStmts = prog.LibStatements
	e.groupLibStatements(prog)
	e.groupDynStatements(prog)
	e.callableClasses = prog.CallableClasses
	e.globalLinks = prog.GlobalLinks
	if e.compatJS() {
		jsThisGeneratorsAsFunctions(prog, e.libStmts)
		jsThisFunctionsAsBindings(prog, e.libStmts)
	}
	// An interface member shape this code generation cannot represent (a
	// call signature beside properties, an index signature keyed by a type
	// alias, …) is valid TypeScript the checker reads; it is rejected here.
	var legacyErr error
	ast.Inspect(prog, func(n ast.Node) bool {
		if d, ok := n.(*ast.InterfaceDeclaration); ok && d.LegacyErr != nil && legacyErr == nil {
			legacyErr = d.LegacyErr
		}
		return legacyErr == nil
	})
	if legacyErr != nil {
		return "", legacyErr
	}
	// TDD-00230 P0.2: with KML_SHADOW set and a new pass registered, every
	// old decider's answer is compared with the new pass's.
	stopShadow, err := e.startShadow(prog)
	if err != nil {
		return "", err
	}
	defer stopShadow()
	// TDD-00128: enforce `/** @pure */` before any codegen. A front-end-only
	// check with zero effect on the emitted IR — a violation is a compile error.
	if err := e.checkPurity(prog); err != nil {
		return "", err
	}
	// TDD-00173: escape-analyze every @free/@owned annotation (all modes) and,
	// under -mm=auto, every implicit candidate. Front-end-only; a failed
	// explicit annotation is a compile error, a failed implicit candidate is
	// silently skipped.
	if err := e.planAutoFrees(prog); err != nil {
		return "", err
	}
	// TDD-00134 Stage 1: under -optimize-memory, plan the non-escaping object
	// literals whose declarations become entry-block allocas.
	if err := e.planStackAllocs(prog); err != nil {
		return "", err
	}
	// Retained so a `typeof value` type query can resolve the referenced value's
	// type from its top-level declaration even at eager type-alias registration
	// (Pass 0), before module globals are bound.
	e.prog = prog
	if hasLocalClass(prog) {
		// The class passes below rewrite, rename and move classes the
		// checker reads by name: every expression is typed off the code as
		// written first.
		e.warmFront(prog)
	}
	e.prepareCallerProps()
	e.prepareGenProtos()
	// Class fields keyed by a declared symbol, and their accesses (TDD-00246).
	if err := e.lowerSymbolFields(prog); err != nil {
		return "", err
	}
	// TS namespaces (TDD-00095): the parser desugared members into flat
	// mangled declarations; the member table is what use sites resolve
	// through (emitCall/emitMember/inferExprType).
	e.namespaces = prog.Namespaces
	// A namespace's own statements see its members by their bare names.
	e.stmtNS = map[ast.Statement]string{}
	for _, g := range prog.NamespaceGroups {
		for _, st := range g.Members {
			if len(g.Name) > len(e.stmtNS[st]) {
				e.stmtNS[st] = g.Name
			}
		}
	}
	e.libExports = prog.LibExports
	e.registerNSAliases(prog.NSAliases)

	// TDD-00098: known before any emission (the resolver recorded worker
	// modules), so every gc-fiber emission site can pick the thread-aware
	// stackbottom mechanism up front.
	e.hasWorkers = len(prog.WorkerModules) > 0

	// TDD-00224: known before any emission — a blocking event-loop call emitted in
	// a function body (Pass 2) already needs to know the module body is a task.
	// An island too (TDD-00225): its main() spawns the task and returns; the
	// importer's loop drives it through the island's poll export.
	e.moduleTask = programHasTopLevelAwait(prog)

	// TDD-00168 Stage 3: does the program have async_hooks? Decided before
	// Pass 2 so a timer callback scheduled inside an `als.run(...)` — possibly
	// from a function body emitted before the store's construction — is
	// wrapped to carry the async context to its fire.
	_, hasALS := e.libExports["async_hooks:AsyncLocalStorage"]
	e.programUsesALS = hasALS

	// TDD-00163 Stage 2: does the program construct a FinalizationRegistry
	// anywhere? A pre-scan for the same emission-order reason as ALS above —
	// a Memory.free inside a function body can be emitted before the top-level
	// `new FinalizationRegistry`, and it needs the finalizer-lookup hook.
	e.programUsesFinReg = programConstructsClass(prog, "FinalizationRegistry")

	// TDD-00158 Stage 2: klain:ws (the WSConnection frame-codec convenience).
	// The resolver records the import on the merged program; when set, the
	// dispatcher emits the WebSocket handshake + frame loop (TLS-aware), guarded
	// at runtime by the connection-handler global a `new WebSocketServer(
	// {server}).on('connection', …)` populates.

	// TDD-00164: node:ffi. Gates DynamicLibrary constructor recognition.

	// TDD-00143 Stage 2: if the program imports klain:sync, the safepoint pass
	// emits cooperative preempt checks (function entry + loop back-edges). Force
	// the runtime's extern decls + link now so those `call @klainsync_safepoint`
	// sites resolve regardless of where the first go/channel is emitted.
	e.usesSyncProgram = prog.UsesKlainSync
	if e.usesSyncProgram {
		e.ensureSyncRuntime()
	}

	// Pass -2: rewrite each top-level `const/let/var X = class {...}` binding
	// into a nominal `class X {...}` declaration (TDD-00063 Stage 4), before
	// any registration runs — a class expression is not a runtime value here,
	// so binding it is the same as declaring the class under the LHS name.
	e.rewriteTopLevelClassExpressions(prog)
	e.outerPrivateReads(prog)   // before hoisting: a nested class still sits in its outer one
	e.staticPrivatesNamed(prog) // likewise
	e.privateInOwners(prog)     // likewise
	e.inferFunctionNames(prog)  // the names hoisted anonymous classes show
	e.hoistClassExpressions(prog)
	e.staticThisToClass(prog)                       // after hoisting: every class has its final name
	e.privateInChecks(prog)                         // likewise
	e.liftedStaticGens = liftStaticGenerators(prog) // after staticThisToClass: its body names the class
	e.rewriteTopLevelGeneratorExpressions(prog)

	// Pass -1: register enums so members are available as constants everywhere.
	if err := e.registerEnums(prog); err != nil {
		return "", err
	}
	ast.AddNamespaceObjects(prog) // a program the resolver did not build

	// Pass -0.5: register a name-only placeholder type for every non-generic
	// class BEFORE interfaces resolve their fields, so an interface/type-alias
	// field typed as a class (`interface W { p: Point }`) resolves to the class's
	// ptr type rather than the i64 unknown-name default (which silently
	// mis-stored the instance pointer). The full class type is filled in by
	// registerClasses below; field access canonicalizes the placeholder to it.
	e.eraseTypeOnlyGenerics(prog)
	e.eraseInheritingGenerics(prog)
	e.registerClassNamePlaceholders(prog)

	// Pass 0: register interfaces and type aliases so they're available to function signatures.
	e.registerLibraryStringAliases(prog)
	hoistLocalTypes(prog)
	e.registerInterfaces(prog)
	e.normalizeRestParams(prog)

	// Pass 0.5: register classes (fields/layout + constructor/method
	// signatures) — after interfaces (a class field/param/return may
	// reference one) and before functions (a function signature may
	// reference a class by name).
	// A class method may return a generator function's call: register the
	// generators first, then again once the classes exist (registerFunctions).
	e.preRegisterGenerators(prog)
	if err := e.registerClasses(prog); err != nil {
		return "", err
	}
	// TDD-00161 Stage 2: register a routing slot for every decorated method,
	// before any method body or call site is emitted, so call sites can route
	// through the slot. Populates e.decoratedMethodSlots and declares the
	// globals; validation/rejection of unsupported shapes happens at emit time.
	if err := e.registerDecoratedMethodSlots(prog); err != nil {
		return "", err
	}

	// Pass 1: register all top-level function signatures so calls work regardless of order.
	// Signature inference may meet a top-level binding that is not yet a module
	// global (Pass 1.7); it needs the name set to type it from its declaration.
	e.collectTopLevelNames(prog)
	if err := e.registerFunctions(prog); err != nil {
		return "", err
	}
	// `-compat=js` (TDD-00155 Stage 4): recognize vanilla-JS prototype
	// constructors — after function sigs exist, before any body is emitted.
	e.jsCollectProtoCtors(e.userStatements(prog))
	// Mark async functions that can actually suspend (TDD-00083 Stage 2), so the
	// emitter compiles them as coroutine tasks; the rest keep the synchronous
	// malloc-slot fast path. Must run after registerFunctions (needs e.funcs).
	e.classifyAsyncSuspension(prog)

	// TDD-00134 Stage 3 (-optimize-memory): mark eligible small-tuple returns
	// as by-value. After classifyAsyncSuspension (MaySuspend excludes), before
	// any body or call is emitted (the flag is the ABI both sides read).
	e.planTupleByValReturns(prog)

	// -compat=js cross-type widening (TDD-00162): compute which untyped top-level
	// bindings hold multiple scalar kinds *before* registerModuleGlobals, so
	// reliableGlobalType can promote a widened binding as an any-box global
	// (matching emitVarDecl's own widened typing). Recomputed for each function
	// body in emit_func.go; this sets the module-scope set.
	e.widenedBindings = e.crossTypeWidenedBindings(e.userStatements(prog).Body)
	e.inferEmptyContainers(prog.Body)

	// Pass 1.7: promote top-level simple `const`/`let`/`var` to module globals
	// (TDD-00093) so the function bodies emitted next can read them (a named
	// function has its own fresh scope and can't see a `main()` local). Must run
	// before Pass 2 emits any function body.
	e.registerModuleGlobals(prog)

	// Pass 1.75: NamedEvaluation names for anonymous function literals
	// (TDD-00229), read when each literal's code is emitted.
	e.inferFunctionNames(prog)

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
	// Every layout is final now: no registry keeps a class snapshot.
	e.refreshClassTypes()
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
		restore := e.libScope(stmt)
		if fd.IsGenerator {
			err := e.emitGeneratorFunctionDecl(fd, e.generators[fd.Name])
			restore()
			if err != nil {
				return "", err
			}
			continue
		}
		if len(fd.TypeParams) == 0 || fd.Erased {
			if err := e.emitFunctionDecl(fd); err != nil {
				restore()
				return "", err
			}
		}
		restore()
	}

	// Pass 2b: emit each class's constructor and methods. A generic class
	// (TDD-00010 V1) is never emitted here — like a generic function, it has
	// no single concrete shape to emit; each concrete instantiation is
	// emitted on demand from its own `new ClassName<T>(...)` site instead
	// (emit_generics.go).
	for _, stmt := range prog.Body {
		if cd, ok := stmt.(*ast.ClassDeclaration); ok && len(cd.TypeParams) == 0 {
			restore := e.libScope(stmt)
			err := e.emitClassDecl(cd)
			restore()
			if err != nil {
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
	e.staticInitAt = map[*ast.ClassDeclaration]bool{}
	for _, stmt := range prog.Body {
		cd, ok := stmt.(*ast.ClassDeclaration)
		if !ok || len(cd.TypeParams) > 0 {
			continue
		}
		restore := e.libScope(stmt)
		e.emitClassVTable(cd.Name)
		e.emitClassStaticFieldGlobals(cd.Name)
		if len(cd.StaticBlocks) > 0 || classHasStaticFieldInit(cd) {
			if err := e.emitClassStaticInit(cd); err != nil {
				restore()
				return "", err
			}
			if e.reevaluatedClass(cd) {
				// (Re-run at each evaluation: emitClassReeval.)
			} else if e.libStmts[stmt] == "" {
				// A program class initializes its statics where its
				// declaration runs, in source order.
				e.staticInitAt[cd] = true
			} else {
				staticInitClasses = append(staticInitClasses, cd.Name)
			}
		}
		restore()
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
	e.emitGlobal("@__argv_ptr = global ptr null, align 8")
	e.emitGlobal("@__argv_len = internal global i64 0, align 8")
	e.emitGlobal("@__process_argv_ptr = global ptr null, align 8")
	e.emitGlobal("@__process_argv_len = global i64 0, align 8")
	argc64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i32 %%argc to i64", argc64))
	argvSrc := "%argv"
	if e.opts.Target.OS() == "windows" {
		// The CRT's argv is ANSI-code-page decoded, so a non-ASCII argument
		// arrives as '?'. Reconstruct it as UTF-8 from GetCommandLineW at
		// startup, as Node does (ADR-00745). Falls back to the CRT argv/argc
		// if the shim returns null.
		e.emitGlobal("declare ptr @__kml_win_argv(ptr)")
		acAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", acAlloca))
		wargv := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_win_argv(ptr %s)", wargv, acAlloca))
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, wargv))
		argvSel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %%argv, ptr %s", argvSel, isNull, wargv))
		wac := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", wac, acAlloca))
		wac64 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i32 %s to i64", wac64, wac))
		argcSel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", argcSel, isNull, argc64, wac64))
		argc64, argvSrc = argcSel, argvSel
	}
	// TDD-00120: the OS argv[] entries are foreign char* with no length header;
	// copy them into length-prefixed strings so process.argv[i] behaves like every
	// other string (its .length reads the header, not strlen).
	e.ensureStrHeaderRuntime()
	argvHdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_argv_headerize(i64 %s, ptr %s)", argvHdr, argc64, argvSrc))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__argv_ptr, align 8", argvHdr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr @__argv_len, align 8", argc64))
	// process.argv is Node-shaped ([execPath, execPath, ...userArgs]) — the
	// executable path appears at index 0 AND 1, so process.argv.slice(2) is the
	// user arguments, matching Node / a Node single-executable app. The raw
	// @__argv_ptr above keeps its OS shape for execv/fork/IPC; only process.*
	// reads use @__process_argv_ptr.
	procArgv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_argv_node_shape(i64 %s, ptr %s)", procArgv, argc64, argvHdr))
	procArgc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", procArgc, argc64))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__process_argv_ptr, align 8", procArgv))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr @__process_argv_len, align 8", procArgc))

	// gc mode: record the main thread's stack base, the bottom a fiber
	// switch restores (gcSBStore), and whether Worker threads will run, which
	// makes every later switch update this thread's own record under the
	// collector's lock (gcshim.c's GC interface).
	if e.isGCMode() {
		e.ensureGCInterface()
		threads := 0
		if e.hasWorkers {
			threads = 1
		}
		e.emitInstr(fmt.Sprintf("call void @kml_gc_main_started(i32 %d)", threads))
	}

	// A builtin module's classes initialize their statics before any
	// program code runs.
	for _, name := range staticInitClasses {
		e.emitInstr(fmt.Sprintf("call void @%s_staticinit()", name))
	}
	if err := e.seedFuncGlobals(); err != nil {
		return "", err
	}

	// Eager-boxing capture set for the top-level (main) body: its own locals
	// captured by some nested closure are boxed at declaration (see
	// hoistedCaptures). A top-level simple scalar promoted to a module global is
	// excluded at the declaration site (emit_exprs_vardecl.go) — a global already
	// dominates everything and is resolved directly, never captured.
	e.hoistedCaptures = capturedLocalNames(prog.Body, nil)
	e.setForwardClosures(prog.Body)
	// TDD-00224: with a top-level await the statements below go into the module
	// task's body instead of main(). Islands keep the main-stack wait (Stage 2).
	var modSplit *moduleBodySplit
	if e.moduleTask {
		modSplit = e.beginModuleBody()
	}
	// e.widenedBindings was already computed for prog.Body before
	// registerModuleGlobals (above) and hasn't been overwritten since (function
	// bodies save/restore it), so it still holds the module-scope set here.
	// A module-level `return` (CommonJS) ends its module's evaluation: it
	// branches past the module's last statement.
	moduleEnds := map[ast.Statement]string{}
	e.topLevelReturns = map[*ast.ReturnStatement]string{}
	for r, last := range prog.TopLevelReturns {
		if e.libStmts[last] != "" {
			continue // a builtin module's statements run from its init
		}
		if moduleEnds[last] == "" {
			moduleEnds[last] = e.freshLabel("module.end")
		}
		e.topLevelReturns[r] = moduleEnds[last]
	}
	// The module's frame, which Node prints as its location alone; each
	// top-level statement names its own file.
	mainFile := ""
	for _, st := range prog.Body {
		if f := prog.StmtFiles[st]; f != "" && !strings.HasPrefix(f, lib.ModuleRoot) {
			mainFile = f
		}
	}
	e.enterFrameIn(mainFile, e.moduleFrameName(mainFile), ast.Pos{Line: 1, Col: 1})
	e.markModuleFrame()
	if err := e.seedProtoCtorConstructors(); err != nil {
		return "", err
	}
	curFile := mainFile
	if err := e.emitLibInitCalls(prog.Body, func(stmt ast.Statement) error {
		if f := prog.StmtFiles[stmt]; f != "" && f != curFile && e.frame != "" && !strings.HasPrefix(f, lib.ModuleRoot) {
			e.setFrameFile(f)
			curFile = f
		}
		if _, ok := stmt.(*ast.FunctionDeclaration); !ok {
			e.curNamespace = e.stmtNS[stmt]
			err := e.emitStmt(stmt)
			e.curNamespace = ""
			if err != nil {
				return err
			}
			e.emitOwnedFreesAfter(stmt)
		}
		if end := moduleEnds[stmt]; end != "" {
			e.emitTerminator("br label %" + end)
			e.emitLabel(end)
		}
		return nil
	}); err != nil {
		return "", err
	}
	// TDD-00173: frees owed by top-level bindings at the end of main().
	e.emitFreesAbove(0)
	modProm := ""
	if modSplit != nil {
		modProm = e.endModuleBody(modSplit)
	}
	islandTask := modProm != "" && e.islandHash != ""
	if islandTask {
		// TDD-00225: an island's main() ends right after the spawn — the body ran
		// to its first await; no loop of its own, no unsettled exit. The importer
		// polls the island's loop (emitIslandGlue) and settles its import() from
		// @__kml_module_promise.
		e.emitTerminator("ret i32 0")
		modProm = ""
		e.ensureEventLoop() // the island's own loop, turned by its poll export
		e.usedIslandLoop = true
	}
	// If the program ever called setTimeout/setInterval/
	// clearTimeout/clearInterval, drain any still-pending timers after the
	// top-level script finishes — the same place real Node keeps the
	// process alive for. Skipped entirely (via emitInstr's own dead-code
	// check) if the last top-level statement already terminated the block,
	// e.g. process.exit() — matching real Node, which also never drains
	// pending timers after an explicit exit.
	// `new` through a value: its dispatcher, before the loop epilogue reads
	// the runtime flags its constructions set (a Promise's microtasks).
	e.emitDynConstructOnce()
	// Microtasks run after the top-level synchronous script, before any timer or
	// task macrotask (TDD-00083 Stage 3).
	if e.usedMicrotasks {
		if e.entryIsModule(prog) {
			// An ES module's top level is itself a promise job: the jobs it
			// queued run before the ticks, as in Node.
			e.emitInstr("call void @__kml_drain_promise_jobs()")
		}
		e.emitInstr("call void @__kml_drain_microtasks()")
	}
	// The structural view's routines (TDD-00233) may use any runtime
	// (exceptions, the dynamic object model): written before the finalize
	// steps that decide which runtimes are linked.
	e.emitRecordFnsFinalize()
	e.emitObjExtraFinalize()

	// One event loop drives every source (TDD-00244): timers, tasks, workers,
	// child processes, fs.watch, the pool, sockets and servers.
	useFullLoop := e.usedTimers || e.usedTaskRuntime || e.usedWorkerRuntime || e.usedFsWatchRuntime || e.usedThreadPool || e.usedIPCChildRuntime
	useFullLoop = useFullLoop || e.usedLoopTurn || e.usedFetchAsync
	if useFullLoop {
		e.ensureEventLoop() // emit event_loop_run + every symbol it references
		modLoopL := ""
		if modProm != "" {
			modLoopL = e.freshLabel("mod.loop")
			e.emitTerminator(fmt.Sprintf("br label %%%s", modLoopL))
			e.emitLabel(modLoopL)
		}
		e.emitInstr("call void @__kml_event_loop_run()")
		if modProm != "" {
			e.emitModuleLoopResume(modLoopL)
		}
	}
	if modProm != "" {
		e.emitModuleUnsettledCheck(modProm)
	}
	// TDD-00084 Part B: if the event loop was emitted but the task/microtask
	// runtimes were not, define no-op stubs for the symbols it references.
	e.emitLoopTaskStubs()
	// The crypto backend's pooled natives reference the pool's job entry; a
	// program using only crypto.subtle never calls them.
	if e.usesCrypto && !e.usedThreadPool {
		e.emitGlobal("define void @__kml_pool_job(ptr %work, ptr %job, ptr %inv, ptr %clo) {\nentry:\n  ret void\n}")
	}
	// TDD-00098 stage 5: @__kml_throw's uncaught path references
	// @__kml_worker_uncaught unconditionally, and the task runtime's swap sites
	// reference @__kml_worker_abort_check; no-op stubs without workers.
	if e.usedExceptionHelpers {
		e.stubWorkerUncaught()
	}
	if e.usedTaskRuntime && !e.usedWorkerRuntime {
		e.emitGlobal("define void @__kml_worker_abort_check() {\nentry:\n  ret void\n}")
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
	// A response-body stream's read pumps in-flight fetches (TDD-00097
	// Stage 4); without fetch the pump is a no-op stub.
	if e.usedAwaitTimerDrive {
		if !e.usedFetchAsync {
			e.emitGlobal("define i1 @__kml_fetch_pump() {\n  ret i1 0\n}")
		}
	}
	// The process-lifecycle runtime (ADR-00334) is emitted whenever exceptions
	// (whose uncaught path calls the hooks) or any process.on('exit')/
	// 'uncaughtException'/exitCode surface is used.
	// The library's `any` bigint helpers, before the runtimes their bodies
	// may need (the exception helpers) are emitted.
	e.emitAnyBigIntOpsFinalize()
	if e.usedExceptionHelpers || e.usedProcessLifecycle {
		e.emitProcessLifecycleRuntime()
	}
	// Normal program end: when process.exitCode / process.on('exit') is in play,
	// run the 'exit' listener and return process.exitCode; otherwise a plain 0.
	if e.usedProcessLifecycle {
		ec := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_process_exit_code, align 8", ec))
		e.emitInstr(fmt.Sprintf("call void @__kml_run_exit_handlers(i64 %s)", ec))
		// An 'exit' listener may set process.exitCode.
		ecAfter := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_process_exit_code, align 8", ecAfter))
		ec32 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", ec32, ecAfter))
		e.emitTerminator(fmt.Sprintf("ret i32 %s", ec32))
	} else {
		e.emitTerminator("ret i32 0")
	}

	// The unhandled-rejection report (or the checkpoint's no-op).

	e.ensureRuntimeDeps()

	// The layout table (TDD-00230 phase 5) spans every class and object layout
	// the program built; its method adapters register function metadata.
	e.emitRecordFnsFinalize() // any a finalize step requested since
	// Before the shape table: boxing a class's static object registers its
	// layout.
	e.emitClassRefFinalize()
	if e.usedDynJSONC {
		e.ensureShapeRuntime() // dynjson.c reads static objects' rows (emitObjHooksFinalize)
	}
	e.emitMapViewFinalize()
	e.emitHostInspectRoutines()
	e.emitValueProtoGetFinalize()
	e.emitPrimProtoOfFinalize()
	e.emitShapeFinalize()
	e.emitRecordFnsFinalize() // the host rows' typed reads may request view routines
	e.emitHostBoxFinalize()
	e.emitHostToPrimFinalize()
	e.emitObjExtraFinalize() // one a finalize routine above first needed
	e.emitMapViewFinalize()  // one a routine above first requested
	// The unhandled-rejection report (or the checkpoint's no-op), after
	// every routine that can reach a promise has been generated.
	e.emitUnhandledFinalize()

	// Function-value metadata (TDD-00229): the code-pointer table spans every
	// function the program emitted, so it is defined last.
	e.emitFnMetaFinalize()
	e.emitObjHooksFinalize()
	e.emitUnhandledFinalize()       // one a hook above first reached the promise runtime
	e.emitResponseCollectFinalize() // a host row above may call a body method
	e.emitObjExtensibleFinalize()
	e.emitMapViewFinalize()
	if e.usedClassRefProto {
		e.emitClassRefProto() // a routine generated above read a prototype
	}
	e.emitObjExtraFinalize() // one a hook above first needed
	e.emitUnitRegFinalize()  // every finalize step above may add rows
	// Last: whether the bigint runtime links (its hooks real, or stubs) is
	// known only once every routine that can reach a bigint is generated.
	e.emitBoxedBigIntHooksFinalize()
	// A finalize step above may have linked the exception runtime first;
	// exceptions.c calls the lifecycle hooks (TDD-00251).
	if e.usedExceptionHelpers {
		e.emitProcessLifecycleRuntime()
	}
	// And a runtime file a finalize step first linked brings its own.
	e.ensureRuntimeDeps()

	// Line-buffer stdout at startup (ADR-00867). C stdio full-buffers a stream
	// that is not a TTY, so `console.log`/`printf` to a pipe or file withholds
	// all output until exit — a program piped to `tee`/`grep` or a Docker log
	// collector shows nothing while it runs, unlike Node which delivers stdout
	// incrementally. _IOLBF (1) flushes on each newline, matching both a TTY's
	// own default and Node's line-at-a-time delivery, without full-buffering's
	// withhold-until-exit. The `stdout` FILE* is a libc extern global whose
	// symbol differs by platform (glibc `stdout`, macOS `__stdoutp`); Windows is
	// left to its own stdio path (UCRT treats _IOLBF as full buffering anyway).
	if e.opts.Target.OS() != "windows" {
		e.emitGlobal("declare i32 @setvbuf(ptr, ptr, i32, i64)")
		e.ensureStdoutGlobal()
		// Node ignores SIGPIPE at startup: a write to a closed pipe fails with
		// EPIPE on the stream instead of killing the process.
		e.emitGlobal("declare ptr @signal(i32, ptr)")
	}
	// A stack overflow throws Node's RangeError (TDD-00249): main installs
	// the handler first. Without a throw path the handler reports it itself.
	// An island's main runs on the importer's fiber, inside a process whose
	// handler is already installed, so an island never installs its own. Its
	// task fibers still take their stacks from the same runtime file. On
	// Windows the handler is a vectored exception handler (stackguard.c).
	stackGuardArgs := ""
	e.usedStackGuard = true
	if e.islandHash == "" {
		e.emitGlobal("declare void @__kml_stack_guard_init(ptr, ptr)")
		throwFn := "null"
		if e.usedExceptionHelpers {
			e.ensureRangeErrorThrow()
			throwFn = "@__kml_throw_range_error"
		}
		stackGuardArgs = fmt.Sprintf("ptr %s, ptr %s", throwFn, e.internString("Maximum call stack size exceeded"))
	}

	// The runtime's hooks into this program, then every constructor.
	e.emitHookRegistration()
	e.emitCtorList()

	var out strings.Builder
	out.WriteString("; Generated by KlainMainLang\n\n")
	if ct := e.opts.Target.Triple; ct != "" {
		// A cross-compilation target (TDD-00146 Stage 1) stamps its own triple
		// so the IR and the clang --target agree; LLVM derives the data layout
		// from the triple, matching the Windows precedent below (triple only,
		// no explicit datalayout line). Explicit --target wins over the
		// Windows-host default.
		out.WriteString("target triple = \"" + ct + "\"\n\n")
	} else if runtime.GOOS == "windows" {
		// The mingw-w64 target (TDD-00177 Stage 0) — without it clang assumes
		// its default MSVC triple and warns on every compile. Linux/macOS keep
		// relying on clang's host default, so their output is unchanged.
		out.WriteString("target triple = \"x86_64-w64-windows-gnu\"\n\n")
	}
	if e.opts.Target.OS() == "windows" {
		out.WriteString("declare i32 @_setmode(i32, i32)\n\n")
	}
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
	out.WriteString(e.workerRegistry)
	if stackGuardArgs != "" {
		out.WriteString("  call void @__kml_stack_guard_init(" + stackGuardArgs + ")\n")
	}
	// A binary that reads process.execPath can be spawned as `execPath -p <expr>`
	// (a Node interpreter re-exec). It cannot honor that; reject such flags up
	// front so a self-spawning program fails cleanly instead of fork-bombing.
	if e.usedNodeInterpGuard {
		out.WriteString("  call void @__kml_reject_node_interp_flags(i32 %argc, ptr %argv)\n")
	}
	if e.opts.Target.OS() != "windows" {
		// Line-buffer stdout before any output (see the decl above).
		fmt.Fprintf(&out, "  %%__kml_stdout_fp = load ptr, ptr @%s, align 8\n", e.stdoutGlobalSymbol())
		out.WriteString("  call i32 @setvbuf(ptr %__kml_stdout_fp, ptr null, i32 1, i64 0)\n")
		// SIGPIPE is 13 on Linux and Darwin; inttoptr 1 is SIG_IGN.
		out.WriteString("  call ptr @signal(i32 13, ptr inttoptr (i64 1 to ptr))\n")
	}
	if e.opts.Target.OS() == "windows" {
		// The UCRT opens stdin/stdout/stderr in text mode, translating every
		// "\n" to "\r\n" on write (and back on read). Node on Windows does no
		// such translation: console.log("a") writes the bytes "a\n". Switch
		// all three to binary mode before any I/O (TDD-00177 Stage 0).
		// 0x8000 is _O_BINARY.
		for fd := 0; fd < 3; fd++ {
			fmt.Fprintf(&out, "  call i32 @_setmode(i32 %d, i32 32768)\n", fd)
		}
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
	// --no-any (TDD-00209): surface the first any/unknown annotation seen during
	// type resolution as a clean compile error, superseding the produced IR.
	if e.noAnyErr != nil {
		return "", e.noAnyErr
	}
	return coffWeakComdats(markNoTail(renameHooks(dropDefinedDeclares(out.String())))), nil
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
	e.emitIslandTaskGlue(out)

	publics := make([]string, 0, len(prog.EntryExportMangled))
	for p := range prog.EntryExportMangled {
		publics = append(publics, p)
	}
	sort.Strings(publics)
	for _, public := range publics {
		mangled := prog.EntryExportMangled[public]
		sym, ok := e.moduleGlobals[mangled]
		if !ok || sym.Ptr == "" || sym.Ty.IsArray() {
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

// registerEnums pre-scans all top-level enum declarations and resolves each
// member to a compile-time constant, as TypeScript evaluates a constant enum
// expression: a number (a double, as every number is) or a string. A member
// without an initializer is the previous numeric member plus one (0 first).
func (e *Emitter) registerEnums(prog *ast.Program) error {
	var body []ast.Statement
	defer func() { prog.Body = body }()
	for _, stmt := range prog.Body {
		body = append(body, stmt)
		ed, ok := stmt.(*ast.EnumDeclaration)
		if !ok {
			continue
		}
		members := make(map[string]Value, len(ed.Members))
		consts := map[string]ast.EnumConst{}
		next, nextOK := 0.0, true
		strs, nums := 0, 0
		pos := ed.GetPos()
		computed := map[string]string{}
		for _, m := range ed.Members {
			var c ast.EnumConst
			if m.Value == nil {
				if !nextOK {
					return fmt.Errorf("%d:%d: enum member '%s' must have an initializer", ed.GetPos().Line, ed.GetPos().Col, m.Name)
				}
				c = ast.EnumConst{Num: next}
			} else {
				var ok bool
				if c, ok = ast.FoldEnumConst(m.Value, ed.Name, consts); !ok {
					// A computed member (`A = f()`): a number, evaluated
					// where the enum is declared, into a module constant;
					// the member after it needs an initializer (TS1061).
					slot := "__kml_enumval_" + ed.Name + "_" + m.Name
					computed[m.Name] = slot
					// Typed as written, before its member reads are
					// rewritten in place.
					e.warmFront(m.Value)
					init := e.enumMemberRefs(m.Value, ed.Name, members)
					body = append(body[:len(body)-1], ast.NewVarDeclaration("const", slot, &ast.TypeAnnotation{Name: "number"}, init, pos), ed)
					members[m.Name] = Value{Ty: TypeF64}
					nums++
					nextOK = false
					continue
				}
			}
			consts[m.Name] = c
			if c.IsStr {
				strs++
				members[m.Name] = Value{Ref: e.internString(c.Str), Ty: TypePtr}
				nextOK = false
			} else {
				nums++
				members[m.Name] = Value{Ref: llvmDoubleLit(c.Num), Ty: TypeF64}
				next, nextOK = c.Num+1, true
			}
		}
		switch {
		case strs > 0 && nums > 0:
			e.enumBacking[ed.Name] = TypeAny // a heterogeneous enum: its values are boxed
		case strs > 0:
			e.enumBacking[ed.Name] = TypePtr
		default:
			e.enumBacking[ed.Name] = TypeF64
		}
		e.enums[ed.Name] = members
		if len(computed) > 0 {
			if e.enumComputed == nil {
				e.enumComputed = map[string]map[string]string{}
			}
			e.enumComputed[ed.Name] = computed
		}
		// The enum as a value (`Object.keys(E)`, `console.log(E)`): its
		// object, each name to its value and, for a number, the value back
		// to the name, held in a module constant built where the enum is.
		var props []ast.ObjectProperty
		var reverse []ast.Statement
		hidden := "__kml_enumobj_" + ed.Name
		for _, m := range ed.Members {
			if slot, isComputed := computed[m.Name]; isComputed {
				// Its reverse entry is keyed by the value, known at run time.
				props = append(props, ast.ObjectProperty{Key: m.Name, Value: ast.NewIdentifier(slot, pos)})
				reverse = append(reverse, ast.NewExpressionStatement(ast.NewAssignmentExpression("=",
					ast.NewIndexExpression(ast.NewIdentifier(hidden, pos), ast.NewIdentifier(slot, pos), pos),
					ast.NewStringLiteral(m.Name, pos), pos), pos))
				continue
			}
			c := consts[m.Name]
			if c.IsStr {
				props = append(props, ast.ObjectProperty{Key: m.Name, Value: ast.NewStringLiteral(c.Str, pos)})
				continue
			}
			var num ast.Expression = ast.NewNumberLiteral(ast.JSNumberString(math.Abs(c.Num)), pos)
			if math.Signbit(c.Num) {
				num = ast.NewUnaryExpression("-", true, num, pos)
			}
			props = append(props, ast.ObjectProperty{Key: m.Name, Value: num}, ast.ObjectProperty{Key: ast.JSNumberString(c.Num), Value: ast.NewStringLiteral(m.Name, pos)})
		}
		e.enumObjects[ed.Name] = hidden
		body = append(body, ast.NewVarDeclaration("const", hidden, ast.AnyAnnotation(""), ast.NewObjectLiteral(props, pos), pos))
		body = append(body, reverse...)
	}
	return nil
}

// enumMemberRefs is a computed enum initializer with each bare name of one
// of the enum's earlier members read as that member (`B = A + 1` is
// `E.A + 1`), as TypeScript resolves a name inside an enum.
func (e *Emitter) enumMemberRefs(x ast.Expression, enum string, members map[string]Value) ast.Expression {
	var rw func(n ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		if id, ok := n.(*ast.Identifier); ok {
			if _, member := members[id.Name]; member {
				m := ast.NewMemberExpression(ast.NewIdentifier(enum, id.GetPos()), id.Name, id.GetPos())
				e.originate(m, id)
				return m
			}
			return n
		}
		switch n.(type) {
		case *ast.ArrowFunction, *ast.FunctionExpression:
			return n // its own scope
		}
		ast.RewriteChildren(n, rw)
		return n
	}
	if out, ok := rw(x).(ast.Expression); ok {
		return out
	}
	return x
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
	// Pass 0: seed every plain interface / object-shape type alias name with a
	// provisional named placeholder (an empty ObjectType carrying RefName)
	// BEFORE any field is resolved, so a self- or mutually-referential field
	// (`interface N { next: N | null; children: N[] }`) resolves to a real
	// ptr-shaped named type instead of the number default — mirroring
	// registerClasses' Pass 0. The placeholder's empty Fields is re-resolved on
	// demand at each drilling field access via canonicalizeClassTy (keyed by
	// RefName), the same way a class's stale self-ref snapshot is. Index-signature
	// and call-signature interfaces are non-structural and excluded.
	for _, stmt := range prog.Body {
		switch s := stmt.(type) {
		case *ast.InterfaceDeclaration:
			if classNames[s.Name] || len(s.TypeParams) > 0 || s.IndexSig != nil || s.CallSig != nil {
				continue
			}
			ph := ObjectType(nil)
			ph.RefName = s.Name
			e.interfaces[s.Name] = ph
		case *ast.TypeAliasDeclaration:
			// An object-shape alias (`type N = { next: N | null }`) is a named
			// structural type too and can self-reference; seed it the same way.
			// A non-object alias (union, primitive, function) can't meaningfully
			// self-reference through a field, so it's left for eager resolution.
			if len(s.TypeParams) > 0 || s.Type == nil || len(s.Type.Fields) == 0 {
				continue
			}
			ph := ObjectType(nil)
			ph.RefName = s.Name
			e.interfaces[s.Name] = ph
		}
	}
	// A builtin library module's declaration resolves as that module's
	// (libScope), in the strict lane whatever the program's lane is.
	savedInLib := e.inLib
	defer func() { e.inLib = savedInLib }()
	for _, stmt := range prog.Body {
		e.inLib = e.libStmts[stmt]
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
				e.interfaces[s.Name] = e.withDictFields(e.indexSignatureType(s.IndexSig), s.Fields)
				continue
			}
			// So does one extending a dictionary (`interface ParsedUrlQuery
			// extends NodeJS.Dict<string | string[]> {}`).
			if base, ok := e.dictHeritage(s); ok {
				e.interfaces[s.Name] = e.withDictFields(base, s.Fields)
				continue
			}
			// A callable interface (lone call signature — ADR-00455)
			// registers as the equivalent function type.
			if s.CallSig != nil {
				e.interfaces[s.Name] = e.resolveType(s.CallSig)
				continue
			}
			// One extending a builtin with a native layout (`interface
			// CodedError extends TypeError { code: string }`) is a value of
			// that layout: its own members are the instance's properties.
			if base, ok := e.nativeHeritage(s); ok {
				base.RefName = s.Name
				e.interfaces[s.Name] = base
				own := map[string]Type{}
				for _, f := range s.Fields {
					fty := e.resolveType(f.Type)
					if f.Optional {
						fty = optionalFieldType(fty)
					}
					own[f.Name] = fty
				}
				if e.nativeIfaceMembers == nil {
					e.nativeIfaceMembers = map[string]map[string]Type{}
				}
				e.nativeIfaceMembers[s.Name] = own
				continue
			}
			fields := make([]Field, len(s.Fields))
			for i, f := range s.Fields {
				fty := e.resolveType(f.Type)
				// `name?: T` widens to `T | undefined` (TDD-00187 Stage 2) —
				// same rule as resolveType's inline object-shape path.
				if f.Optional {
					fty = optionalFieldType(fty)
				}
				fields[i] = Field{Name: f.Name, Ty: fty, Optional: f.Optional}
			}
			fields = e.withMethodFields(fields, s.Methods, e.resolveType)
			// interface+interface declaration merging (ADR-00479): a second
			// same-name interface unions its members into the first (first
			// declaration wins on a member-name collision, mirroring TS's
			// no-conflicting-members rule without checking it).
			if prev, ok := e.interfaces[s.Name]; ok && prev.IsObject() {
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
			finalTy := UserObjectType(fields)
			finalTy.RefName = s.Name
			e.interfaces[s.Name] = finalTy
			if len(s.Methods) > 0 {
				sigs := e.interfaceMethodSigs[s.Name]
				if sigs == nil {
					sigs = make(map[string]FuncSig, len(s.Methods))
				}
				for _, m := range s.Methods {
					sig := e.buildParamSig(m.Params, nil)
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
				aliasTy := e.resolveType(s.Type)
				// Tag a named object-shape alias for on-demand re-resolution of
				// its self-referential field snapshots (canonicalizeClassTy),
				// exactly as a plain interface is tagged.
				if aliasTy.IsObject() && aliasTy.RefName == "" {
					aliasTy.RefName = s.Name
				}
				e.interfaces[s.Name] = aliasTy
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
		s, ok := stmt.(*ast.InterfaceDeclaration)
		if !ok || len(s.TypeParams) > 0 || s.IndexSig != nil {
			continue
		}
		// The bases as the resolver left them: a namespace-qualified base
		// (`net.ServerOpts`) is renamed to its declaration, unqualified.
		bases := append([]string(nil), s.Extends...)
		for _, h := range s.Heritage {
			if ref, ok := h.(*ast.TypeReference); ok && len(ref.Qualifier) == 0 && len(ref.TypeArgs) == 0 {
				dup := false
				for _, b := range bases {
					if b == ref.Name {
						dup = true
					}
				}
				if !dup {
					bases = append(bases, ref.Name)
				}
			}
		}
		if len(bases) > 0 {
			pending = append(pending, ifaceExt{s.Name, bases})
		}
	}
	// An interface extending Error (or NodeJS.ErrnoException) whose own
	// members are fields every Error has (`address?: string; port?:
	// number`) is the Error representation, as its base is.
	for _, stmt := range prog.Body {
		s, ok := stmt.(*ast.InterfaceDeclaration)
		if !ok || len(s.Heritage) != 1 || len(s.TypeParams) > 0 {
			continue
		}
		ref, ok := s.Heritage[0].(*ast.TypeReference)
		if !ok || len(ref.TypeArgs) > 0 {
			continue
		}
		q := strings.Join(ref.Qualifier, ".")
		if !(len(ref.Qualifier) == 0 && ref.Name == "Error") && !(q == "NodeJS" && ref.Name == "ErrnoException") {
			continue
		}
		ie := ifaceExt{name: s.Name}
		own, ok := e.interfaces[ie.name]
		if !ok || !own.IsObject() {
			continue
		}
		fits := len(e.interfaceMethodSigs[ie.name]) == 0
		for _, f := range own.UserFields() {
			if _, _, fixed := errorObjType.FieldIndex(f.Name); !fixed || f.Name == ClassTagField || f.Name == errorExtraField {
				fits = false
			}
		}
		if fits {
			e.interfaces[ie.name] = errorObjType
		}
	}
	for pass := 0; pass < len(pending)+1; pass++ {
		changed := false
		for _, ie := range pending {
			own, ok := e.interfaces[ie.name]
			if !ok || !own.IsObject() {
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
				if !ok || !bt.IsObject() {
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
				mergedTy := UserObjectType(merged)
				mergedTy.RefName = ie.name
				e.interfaces[ie.name] = mergedTy
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

// preRegisterGenerators records each top-level generator function's
// signature before the classes register, so a method returning its call
// infers a generator; one naming a class not yet registered waits for
// registerFunctions.
func (e *Emitter) preRegisterGenerators(prog *ast.Program) {
	savedInLib := e.inLib
	defer func() { e.inLib = savedInLib }()
	// Provisional: registerFunctions rebuilds each signature once every
	// generator is known (a later one a `yield*` delegates to).
	defer e.analysisPass()()
	for _, stmt := range prog.Body {
		if fd, ok := stmt.(*ast.FunctionDeclaration); ok && fd.IsGenerator && len(fd.TypeParams) == 0 {
			e.inLib = e.libStmts[stmt]
			if info, err := e.buildGeneratorSig(fd); err == nil {
				e.generators[fd.Name] = info
			}
		}
	}
}

// registerFunctions pre-scans all top-level function declarations and records
// their signatures so calls can be resolved before the function body is emitted.
func (e *Emitter) registerFunctions(prog *ast.Program) error {
	var unannotated []*ast.FunctionDeclaration
	savedInLib := e.inLib
	defer func() { e.inLib = savedInLib }()
	for _, stmt := range prog.Body {
		fd, ok := stmt.(*ast.FunctionDeclaration)
		if !ok {
			continue
		}
		e.inLib = e.libStmts[stmt]
		// `-compat=js`: call sites disagreeing on an unannotated parameter's
		// kind used to be a hard error; with implicit-`any` (TDD-00076 A1)
		// a genuinely polymorphic parameter simply becomes a boxed `any` —
		// both callers are valid JS — so buildFunctionSig's conflict-aware
		// fallback handles it and no validation is needed here.
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
		e.topFuncDecls[fd.Name] = fd
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
	} else if inferred, ok := e.inferUnannotatedReturnType(fd.Body, sig.ParamNames, e.bodyParamTypes(fd.Params, sig.ParamTypes)); ok {
		sig.RetType = inferred
	} else {
		sig.RetType = TypeVoid
	}
	// An async `@erased` generic returns a Promise just like a plain async fn
	// (TDD-00207) — wrap an unannotated/bare inner type so call-site .then/await
	// take the task path.
	if fd.IsAsync && !sig.RetType.IsPromise() {
		sig.RetType = PromiseOf(e.awaitedRet(sig.RetType))
	}
	if fd.IsAsync && sig.RetType.IsPromise() {
		// Either epilogue returns a task promise, as a call is typed and
		// as asyncClosureRetType types an async closure's.
		sig.RetType.PromiseTask = true
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
	// A function that reads `arguments` gains an implicit trailing `any[]` rest
	// parameter here, at the one authoritative signature computation, so the
	// definition site and every call site agree on the variadic arity
	// (TDD-00210 Stage 1). Idempotent — re-inference sweeps re-enter this.
	maybeAddArgumentsRestParam(fd)
	thisTaking := e.maybeAddThisParam(fd)
	retType := TypeVoid
	if fd.ReturnType != nil {
		retType = e.resolveType(fd.ReturnType)
	}
	sig := FuncSig{RetType: retType, IsAsync: fd.IsAsync, This: thisTaking}
	for i, p := range fd.Params {
		var pty Type
		if p.Type != nil {
			pty = e.resolveType(p.Type)
		} else if p.Rest {
			if p.Name == argumentsRestParam {
				pty = ArrayOf(TypeAny) // implicit `arguments` rest: boxed elements (TDD-00210)
			} else {
				pty = untypedRestType()
			}
		} else if ct, ok := e.jsCheckerParam(fd, i); ok {
			// -compat=js: the checker's call-site type (checkJs's any where
			// the sites disagree).
			pty = ct
		} else if dt, ok := e.paramDefaultType(p); ok {
			// Typed by its literal default, as TS does (`p = '!'` is a string).
			pty = dt
		} else if pt, ok := e.patternParamType(fd, i); ok {
			// A destructured parameter: the type its pattern gives it.
			pty = pt
		} else if e.compatJS() {
			// TDD-00076 A1 (implicit-`any`): under `-compat=js`, an
			// unannotated parameter no call site can type — or whose call
			// sites genuinely disagree (a polymorphic JS function) — is a
			// boxed `any`, dispatched at runtime. Precedence: annotation →
			// call-site-inferred concrete type → boxed any.
			pty = TypeAny
		} else {
			pty = TypeI64
			pty.Inferred = true // no annotation given — see docs/adr/00/ADR-00042.md
		}
		sig.ParamTypes = append(sig.ParamTypes, optionalParamType(p, pty))
		sig.ParamNames = append(sig.ParamNames, p.Name)
		sig.Defaults = append(sig.Defaults, callSiteDefault(p)) // a destructured parameter's
		sig.HasDefault = append(sig.HasDefault, p.Default != nil)
		sig.Optional = append(sig.Optional, p.Optional || bodyDefaulted(p))
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
	if fd.ReturnType == nil && e.sigInferInProgress[fd] {
		// Recursion guard: this declaration's return type is already being
		// inferred higher up the stack (a recursive nested function reached
		// back through the letrec identifier inference or
		// inferBlockReturnExpr). Keep the void default here and tell the
		// in-flight inference its current candidate is poisoned.
		e.sigInferCycleHit = true
	} else if fd.ReturnType == nil {
		if e.sigInferInProgress == nil {
			e.sigInferInProgress = map[*ast.FunctionDeclaration]bool{}
		}
		e.sigInferInProgress[fd] = true
		defer delete(e.sigInferInProgress, fd)
		paramNames := make([]string, len(fd.Params))
		for i, p := range fd.Params {
			paramNames[i] = p.Name
		}
		if inferred, ok := e.inferUnannotatedReturnType(fd.Body, paramNames, e.bodyParamTypes(fd.Params, sig.ParamTypes)); ok {
			sig.RetType = inferred
		} else if blockAlwaysDiverges(fd.Body) {
			// No reachable return AND every path throws ⇒ `never` (TS's own
			// inference). Keeps the result usable in an operand position —
			// coerce turns a never value into a dead zero (emit_convert.go)
			// — instead of the void default, whose empty IR operand is invalid
			// (ADR-00869). The body still emits an `unreachable` fallthrough.
			sig.RetType = TypeNever
		}
	}
	// An async function always returns a Promise: wrap an unannotated/bare
	// inferred inner type (number, void, or `never` for a body that only throws)
	// so `f().then`/`.catch`/`await` see a promise at the call site and the
	// emitter's currentPromiseTy is the real element type (TDD-00207). An already
	// `Promise<T>`-annotated return is left alone.
	if fd.IsAsync && !sig.RetType.IsPromise() {
		sig.RetType = PromiseOf(e.awaitedRet(sig.RetType))
	}
	if fd.IsAsync && sig.RetType.IsPromise() {
		// Either epilogue returns a task promise, as a call is typed and
		// as asyncClosureRetType types an async closure's.
		sig.RetType.PromiseTask = true
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

// entryIsModule reports whether the program's entry file is an ES module
// (it imports or exports; the resolver records it before stripping imports).
func (e *Emitter) entryIsModule(prog *ast.Program) bool {
	if prog.EntryIsModule {
		return true
	}
	for _, st := range prog.Body {
		switch st.(type) {
		case *ast.ImportDeclaration, *ast.ExportDeclaration, *ast.ExportFromDeclaration:
			return true
		}
	}
	return false
}

// dictHeritage is the dictionary type an interface extends (a generic base
// such as `NodeJS.Dict<T>` whose instance is a string-keyed dictionary).
func (e *Emitter) dictHeritage(s *ast.InterfaceDeclaration) (Type, bool) {
	for _, h := range s.Heritage {
		ref, ok := h.(*ast.TypeReference)
		if !ok || len(ref.TypeArgs) == 0 {
			continue
		}
		ta, err := ast.TypeAnnotationOf(h, "ts")
		if err != nil || ta == nil {
			continue
		}
		if t := e.resolveType(ta); isDictType(t) {
			return t, true
		}
	}
	return Type{}, false
}

// primitiveAnnotKind is the primitive a union member's annotation names —
// the keyword or a literal of it (`string`, `'dir'`, `1`, `true`) — or "".
func primitiveAnnotKind(ta *ast.TypeAnnotation) string {
	if ta == nil || ta.UnionMembers != nil || ta.ElemType != nil || len(ta.TypeArgs) > 0 {
		return ""
	}
	switch {
	case ta.IsStringLiteral || ta.Name == "string":
		return "string"
	case ta.IsNumberLiteral || ta.Name == "number":
		return "number"
	case ta.Name == "boolean" || ta.Name == "true" || ta.Name == "false":
		return "boolean"
	}
	return ""
}

// userTypeName reports whether the program itself declares a class,
// interface or type alias named name.
func (e *Emitter) userTypeName(name string) bool {
	if _, ok := e.classes[name]; ok {
		return true
	}
	if _, ok := e.interfaces[name]; ok {
		return true
	}
	if _, ok := e.genericClasses[name]; ok {
		return true
	}
	return false
}

// withAnnotNull is t with the null or undefined an annotation's union adds
// (`Promise<T> | null`): absent as a pointer type's null.
func withAnnotNull(t Type, ta *ast.TypeAnnotation) Type {
	if ta.Nullable && !t.IsDynamic() {
		t.setNullable(true)
		t.setUndefined(ta.Undefined)
	}
	return t
}

// stubWorkerUncaught defines a no-op @__kml_worker_uncaught, once, for a
// program without the Worker runtime: @__kml_throw's uncaught path and the
// unhandled-rejection report call it unconditionally.
func (e *Emitter) stubWorkerUncaught() {
	if e.usedWorkerRuntime || e.workerUncaughtStub {
		return
	}
	e.workerUncaughtStub = true
	e.emitGlobal("define void @__kml_worker_uncaught(i8 %tag, i64 %pay) {\nentry:\n  ret void\n}")
}
