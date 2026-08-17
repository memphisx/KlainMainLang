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
	Ptr     string // alloca for the value (scalars) or the data pointer (arrays)
	LenPtr  string // alloca for the array length; empty for scalars
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
	globals             strings.Builder // global declarations (string constants, printf decl, …)
	functions           strings.Builder // emitted user-defined function bodies
	allocas             strings.Builder // alloca instructions for the current function
	body                strings.Builder // body instructions for the current function
	scopes              []scope
	regCtr              int
	labelCtr            int
	strConsts           map[string]string // Go string value → @.s<n> name
	strIdx              int
	linkLibs            map[string]bool // external non-libc libraries the compiled program needs (e.g. "curl")
	memMode             string          // "" (== "manual", the default) or "gc" — see SetMemMode
	regexMode           string          // "" (== the default, resolving to the highest implemented ES stage) or "pcre"/"es-ascii"/"es-unicode" — see SetRegexMode / TDD-00067
	usedPrintf          bool
	usedDprintf         bool
	usedMalloc          bool
	usedCalloc          bool
	usedRealloc         bool
	usedMemmove         bool
	funcs               map[string]FuncSig            // registered function signatures
	interfaces          map[string]Type               // named interface, type alias, and class registry
	interfaceMethodSigs map[string]map[string]FuncSig // interface name → method name → signature (TDD-00009 Stage 4, `implements` conformance only — not used for dispatch)
	classes             map[string]ClassInfo          // named class registry (fields/ctor/methods) — see emit_classes.go
	// genericFuncs/genericInterfaces/genericClasses hold the raw declaration
	// for every `<T>`-parameterized function/interface/class (TDD-00010 V1),
	// keyed by its bare source name — deliberately *not* also entered into
	// funcs/interfaces/classes, since T isn't resolvable until a real call/
	// usage/construction site supplies a concrete type. See emit_generics.go.
	genericFuncs      map[string]*ast.FunctionDeclaration
	genericInterfaces map[string]*ast.InterfaceDeclaration
	genericClasses    map[string]*ast.ClassDeclaration
	// generators holds one entry per top-level `function*` declaration
	// (TDD-00061/ADR-00172), keyed by its bare source name — deliberately
	// separate from funcs, since constructing a generator (`gen(args)`)
	// never emits a plain `call` to the generator's own compiled body
	// function the way an ordinary named-function call does; see
	// emit_generators.go.
	generators map[string]*GeneratorInfo
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
	nestedFuncScopes         []nestedFuncScope
	nestedFuncCtr            int
	usedStrlen               bool
	usedMemcpy               bool
	usedMemset               bool
	usedStrcmp               bool
	usedSprintf              bool
	usedStrstr               bool
	usedStrncmp              bool
	usedStringTrim           bool
	usedStringTrimStart      bool
	usedStringTrimEnd        bool
	usedStringToUpper        bool
	usedStringToLower        bool
	usedStringReplace        bool
	usedStringReplaceAll     bool
	usedStringSplit          bool
	usedAtoll                bool
	usedIPow                 bool
	usedJSONStringifyNum     bool
	usedJSONStringifyStr     bool
	usedJSONParseStr         bool
	usedJSONFindValue        bool
	usedJSONParseFieldStr    bool
	usedAnyEq                bool
	usedClockGettime         bool
	usedDateNow              bool
	usedPerformanceNow       bool
	usedPerformanceMarkMap   bool
	usedDateDecompose        bool
	usedSscanf               bool
	usedDaysFromCivil        bool
	usedDateParse            bool
	usedDateCompose          bool
	usedDateNameTables       bool
	usedFetch                bool
	usedFetchAsync           bool
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
	usedForkDecl             bool
	usedCloseDecl            bool
	usedReadDecl             bool
	usedFflushDecl           bool
	usedHTTPClusterFork      bool
	usedProcessCwd           bool
	usedProcessChdir         bool
	usedGetpid               bool
	usedProcessKill          bool
	usedSignalHandler        bool
	usedSignalSigint         bool
	usedSignalSigterm        bool
	usedErrnoAccessor        bool
	usedStrerror             bool
	usedFsMkdir              bool
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
	httpListenCallSeen       bool
	usedHTTPThrow            bool
	usedSplitFirst           bool
	usedHTTPParseHeaders     bool
	usedHTTPParseQuery       bool
	usedHTTPSerializeHeaders bool
	usedFiber                bool
	usedGeneratorRuntime     bool
	generatorBodyCtr         int
	usedMathFuncs            bool
	usedCtlz32               bool
	usedArc4Random           bool
	usedStrtoll              bool
	usedStrtod               bool
	usedGroupMapHelpers      bool
	usedQsort                bool
	usedSortCmpI64           bool
	usedSortCmpF64           bool
	usedSortCmpStr           bool
	usedSortTrampolineI64    bool
	usedSortTrampolineF64    bool
	usedSortTrampolineStr    bool
	usedSortClosGlobal       bool
	usedMapStrHelpers        bool
	usedMapNumHelpers        bool
	usedEventEmitterRuntime  bool
	usedOSReadProcFile       bool
	usedOSCpusLinux          bool
	usedOSCpusDarwin         bool
	usedGethostname          bool
	usedSysconf              bool
	usedSysctlbyname         bool
	usedMachVM               bool
	usedExceptionHelpers     bool
	usedFrozenSet            bool
	usedPathNormalize        bool
	usedPathDirname          bool
	usedPathBasename         bool
	usedPathExtname          bool
	usedRegexCompile         bool
	usedRegexCompileContext  bool
	usedRegexParseFlags      bool
	usedRegexMatch           bool
	usedRegexUTF16Convert    bool
	usedRegexUTF8Width       bool
	usedRegexESNormalize     bool
	breakStack               []string // end labels for enclosing loops / switch
	continueStack            []string // continue-target labels for enclosing loops
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
	isAsync          bool
	coroHdl          string // register holding the malloc'd promise slot
	currentPromiseTy Type   // T in Promise<T>; void if Promise<void>
	coroRetLabel     string // label for the async-return block
}

func NewEmitter() *Emitter {
	e := &Emitter{
		strConsts:           make(map[string]string),
		funcs:               make(map[string]FuncSig),
		interfaces:          make(map[string]Type),
		interfaceMethodSigs: make(map[string]map[string]FuncSig),
		classes:             make(map[string]ClassInfo),
		enums:               make(map[string]map[string]Value),
		genericFuncs:        make(map[string]*ast.FunctionDeclaration),
		genericInterfaces:   make(map[string]*ast.InterfaceDeclaration),
		genericClasses:      make(map[string]*ast.ClassDeclaration),
		generators:          make(map[string]*GeneratorInfo),
		fnValueTrampolines:  make(map[string]bool),
		currentRetType:      TypeI32, // main returns i32
	}
	e.pushScope()
	return e
}

// SetMemMode selects the compile-wide memory-management mode ("manual", the
// zero-value default, or "gc"). Called by main.go right after NewEmitter()
// — not a constructor argument, so every existing zero-arg NewEmitter() call
// site (mainly tests/compiler_test.go) keeps working unchanged.
func (e *Emitter) SetMemMode(mode string) { e.memMode = mode }

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

func (e *Emitter) lookup(name string) (Symbol, bool) {
	for i := len(e.scopes) - 1; i >= 0; i-- {
		if s, ok := e.scopes[i].syms[name]; ok {
			return s, true
		}
	}
	return Symbol{}, false
}

// isShadowedByLocal reports whether name is a real, already-declared local
// binding — checked first at every Tier 1 ambient-global dispatch site
// (Math/JSON/console/process/fetch/... — see resolver/reserved_names.go)
// before falling back to that name's built-in meaning, the same
// lookup-first-then-fallback pattern this file's identifier-evaluation path
// already used for NaN/Infinity before TDD-00050 generalized it. Only ever
// true under `-globals=permissive`: `-globals=strict` (the default)
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
	if name, ok := e.strConsts[s]; ok {
		return name
	}
	name := fmt.Sprintf("@.s%d", e.strIdx)
	e.strIdx++
	esc, length := escapeLLVM(s)
	e.emitGlobal(fmt.Sprintf("%s = private unnamed_addr constant [%d x i8] c\"%s\", align 1", name, length, esc))
	e.strConsts[s] = name
	return name
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

func (e *Emitter) resolveType(ta *ast.TypeAnnotation) Type {
	if ta == nil {
		return TypeI64 // default for untyped numeric variables
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
	if ta.IsFuncType {
		params := make([]Type, len(ta.FuncParams))
		for i := range ta.FuncParams {
			params[i] = e.resolveType(&ta.FuncParams[i])
		}
		ret := TypeVoid
		if ta.FuncRetType != nil {
			ret = e.resolveType(ta.FuncRetType)
		}
		return FuncType(params, ret)
	}
	// Promise<T>/Map<K,V>/Set<T> must be checked before the generic ElemType
	// fallback below (which treats any non-nil ElemType as a plain array —
	// correct for T[] and Array<T>, wrong for these three).
	if ta.Name == "Promise" {
		if ta.ElemType != nil {
			inner := e.resolveType(ta.ElemType)
			return PromiseOf(inner)
		}
		return PromiseOf(TypeVoid)
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
		subs := e.buildTypeArgSubs(genDecl.TypeParams, ta.TypeArgs)
		return e.instantiateGenericInterface(genDecl, subs)
	}
	if ta.Name == "Set" && ta.ElemType != nil {
		return SetType(e.resolveType(ta.ElemType))
	}
	if ta.Name == "EventEmitter" && ta.ElemType != nil {
		return EventEmitterType(e.resolveEventEmitterPayloadType(ta.ElemType))
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
	if len(ta.Fields) > 0 {
		fields := make([]Field, len(ta.Fields))
		for i, af := range ta.Fields {
			fields[i] = Field{Name: af.Name, Ty: e.resolveType(af.Type)}
		}
		return ObjectType(fields)
	}

	// Named type: check interface registry before falling back to built-ins.
	name := ta.Name
	// Handle T[] where T is a named interface (e.g. "User[]").
	if len(name) > 2 && name[len(name)-2:] == "[]" {
		base := name[:len(name)-2]
		if ty, ok := e.interfaces[base]; ok {
			return ArrayOf(ty)
		}
	}
	if ty, ok := e.interfaces[name]; ok {
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

func (e *Emitter) EmitProgram(prog *ast.Program) (string, error) {
	// Pass -2: rewrite each top-level `const/let/var X = class {...}` binding
	// into a nominal `class X {...}` declaration (TDD-00063 Stage 4), before
	// any registration runs — a class expression is not a runtime value here,
	// so binding it is the same as declaring the class under the LHS name.
	e.rewriteTopLevelClassExpressions(prog)

	// Pass -1: register enums so members are available as constants everywhere.
	e.registerEnums(prog)

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

	// Pass 2: emit each function declaration. A V1 (monomorphized) generic
	// function is never emitted here — it has no single concrete signature
	// to emit; each concrete instantiation is emitted on demand from its own
	// call site instead (emit_generics.go), and a generic function that's
	// never called is simply never emitted at all. A V2 (`@erased`) generic
	// function is the opposite: it has exactly one signature (already
	// registered into e.funcs by registerFunctions above), so it's emitted
	// here unconditionally, same as a plain function.
	for _, stmt := range prog.Body {
		fd, ok := stmt.(*ast.FunctionDeclaration)
		if !ok {
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
		if len(cd.StaticBlocks) > 0 {
			if err := e.emitClassStaticInit(cd); err != nil {
				return "", err
			}
			staticInitClasses = append(staticInitClasses, cd.Name)
		}
	}

	// Pass 3: emit remaining statements into main().
	// process.argv is backed by two globals set from main's own argc/argv
	// parameters, so any expression (top-level code, or any function/closure)
	// can read it later without needing to be threaded through explicitly.
	e.emitGlobal("@__argv_ptr = internal global ptr null, align 8")
	e.emitGlobal("@__argv_len = internal global i64 0, align 8")
	argc64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i32 %%argc to i64", argc64))
	e.emitInstr(fmt.Sprintf("store ptr %%argv, ptr @__argv_ptr, align 8"))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr @__argv_len, align 8", argc64))

	// gc mode: snapshot Boehm's GC_stackbottom (the process's real stack
	// base, already valid here since the gcshim's constructor-attribute
	// GC_INIT() always runs before main()'s first instruction) so the
	// swapcontext sites in runtime.go can restore it after temporarily
	// repointing it at a fiber's own stack while that fiber runs — see
	// docs/adr/ADR-00071.md for why this is needed.
	if e.isGCMode() {
		e.emitGlobal("@GC_stackbottom = external global ptr")
		e.emitGlobal("@__kml_gc_orig_stackbottom = internal global ptr null, align 8")
		origReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @GC_stackbottom, align 8", origReg))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_gc_orig_stackbottom, align 8", origReg))
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
	if e.usedEventSource || e.usedWSClient {
		e.emitInstr("call void @__kml_event_loop_run()")
	} else if e.usedTimers {
		e.emitInstr("call void @__kml_timer_drain()")
	}
	e.emitTerminator("ret i32 0")

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
	out.WriteString(e.body.String())
	out.WriteString("}\n")
	return out.String(), nil
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
			for _, m := range ed.Members {
				if sl, ok := m.Value.(*ast.StringLiteral); ok {
					ptr := e.internString(sl.Value)
					members[m.Name] = Value{Ref: ptr, Ty: TypePtr}
				}
			}
		} else {
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
	for _, stmt := range prog.Body {
		switch s := stmt.(type) {
		case *ast.InterfaceDeclaration:
			// A generic interface's field types reference an unresolvable
			// bare type-parameter name — defer to on-demand instantiation
			// at each usage site instead (TDD-00010 V1, see
			// emit_generics.go); never entered into e.interfaces itself.
			if len(s.TypeParams) > 0 {
				e.genericInterfaces[s.Name] = s
				continue
			}
			fields := make([]Field, len(s.Fields))
			for i, f := range s.Fields {
				fields[i] = Field{Name: f.Name, Ty: e.resolveType(f.Type)}
			}
			e.interfaces[s.Name] = ObjectType(fields)
			if len(s.Methods) > 0 {
				sigs := make(map[string]FuncSig, len(s.Methods))
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
			e.interfaces[s.Name] = e.resolveType(s.Type)
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
	sig := FuncSig{RetType: retType}
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
