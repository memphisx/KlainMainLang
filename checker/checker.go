package checker

import (
	"strconv"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/diag"
	"KlainMainLang/lib"
	"KlainMainLang/options"
)

// Checker answers TypeOf for a bound program.
type Checker struct {
	// argsWritten: argumentsReassigned's set, computed once.
	argsWritten map[*binder.Symbol]bool
	// importObjects are the module objects of dynamic imports, by call.
	importObjects map[*ast.ImportCallExpression]*Type
	// deferredRets are the functions whose deferred result is being
	// computed (functionType).
	deferredRets map[ast.Node]bool
	// relMemo, relActive and relAssumed are relate's memo (relate.go).
	relMemo   map[relKey]ternary
	relActive map[relKey]bool
	// relIdent counts the recursion identities (recursionIdentity) being
	// related.
	relIdent   map[string]int
	relAssumed int
	// iterDepth bounds iteratedType through `[Symbol.iterator]` members.
	iterDepth int
	// returnCtx holds the calls inferring from their contextual type
	// (inferFromReturnContext), so typing that context does not re-enter.
	// instantiations counts generic interface instantiations (interfaceOf).
	instantiations int
	returnCtx      map[ast.Expression]bool
	// ctxStandIn maps a call synthesized to type a front-end `new` (a
	// builtin's construct signatures) to that expression, whose context
	// it shares.
	ctxStandIn map[ast.Node]ast.Node
	// refitting marks a call whose picked overload is being re-checked
	// against its callbacks' results (refitOverload).
	refitting map[*ast.CallExpression]bool
	// paramCtx holds the constructions whose argument contexts are being
	// inferred (constructParamContext).
	paramCtx map[*ast.NewExpression]bool
	// newNoCtor is each `new` whose target has call signatures but no
	// construct signature: any, TS7009 under noImplicitAny.
	newNoCtor map[*ast.NewExpression]bool
	// newUntyped is each `new` of a value typed Function itself: any, and
	// tsc's TS2351 (it has no construct signature, and is no union).
	newUntyped map[*ast.NewExpression]bool
	// foreignCtor caches ctorReturnsForeign per class symbol.
	foreignCtor map[*binder.Symbol]bool
	// CatchVariablesAny types an unannotated catch variable any, as
	// TypeScript does without useUnknownInCatchVariables (which --strict,
	// and so this compiler, always sets). For measuring against
	// TypeScript's own non-strict cases.
	CatchVariablesAny bool
	// ImplicitAnyVariables types an unannotated variable declared without a
	// value any, as TypeScript does without noImplicitAny (--strict sets
	// it: such a variable evolves by its assignments).
	ImplicitAnyVariables bool
	// ImplicitThis is TypeScript's noImplicitThis off: a `this` with no type
	// is not TS2683 (the strict lane never sets it).
	ImplicitThis bool
	// LegacyClassFields checks class fields as TypeScript does for a target
	// before ES2022 (or without useDefineForClassFields): an instance
	// field's initializer runs in the constructor, and may not read the
	// constructor's own names (TS2301).
	LegacyClassFields bool
	// LibGlobal, when set, is the library's global values a spelling
	// suggestion may offer (the lib option's files); otherwise every one
	// TypeScript's library and Node's declarations declare.
	LibGlobal func(name string) bool

	b *binder.Binding
	// opts are the compile modes; -compat=js makes an unannotated
	// parameter with no contextual type take the type its call sites pass,
	// and a class without declared fields have the ones its constructor
	// assigns.
	opts options.Options
	// js holds the -compat=js call-site tables (jsmode.go).
	js *jsTables
	in interner

	anyT, unknownT, objectT, emptyObjT, thisT, strT, numT, boolT, bigintT, symT, voidT, undefinedT, nullT, neverT, trueT, falseT *Type
	// missingT is an omitted optional property: undefined to every rule
	// (it carries the Undefined flag), but a distinct type, so an explicit
	// undefined, an explicit null and an absent property stay three facts.
	missingT *Type
	// globalThisT is `typeof globalThis`: the global object, whose members
	// the global declarations are (namespaceMember).
	globalThisT *Type
	// unanswered marks an expression whose type the checker does not model
	// yet. It behaves as any, but the shadow comparison skips it.
	unanswered *Type

	nodeTypes map[ast.Node]*Type
	// standsFor maps a node a rewrite after binding built to the node it
	// replaced (StandsFor).
	standsFor map[ast.Expression]ast.Expression
	// literalSnaps holds the object literals being typed that have methods,
	// each with the snapshot `this` reads meanwhile (nil: none), and
	// literalThisFix the `this` nodes that read one.
	literalSnaps   map[*ast.ObjectLiteral]*Type
	literalThisFix map[*ast.ObjectLiteral][]ast.Expression
	// nextReported is each iterable already reported TS2490.
	nextReported map[memberKey]bool
	// memberBusy guards buildingMember against a member typing itself.
	memberBusy map[memberKey]bool
	// memberDecl is the declaration of each member memberBusy marks whose
	// result is inferred; cycleReported the members TS7023 was reported for.
	memberDecl    map[memberKey]*ast.FunctionDeclaration
	cycleReported map[*ast.FunctionDeclaration]bool
	// literalCtxBusy guards literalOfContext's walk of a conditional's
	// branches.
	literalCtxBusy map[*Type]bool
	// intersectionOf are the members of an intersection of function types
	// the overload list standing for it was made from (functionIntersection):
	// inference infers to each, as tsc's inferToMultipleTypes.
	intersectionOf map[*Type][]*Type
	// forInKeyNodes are the synthesized `Extract<keyof X, string>` node of
	// each generic `for…in` (forInKeyType), one per loop.
	forInKeyNodes map[*ast.ForInStatement]ast.TypeNode
	// literalCycles are the object literal methods TS7023 was reported for.
	literalCycles map[*ast.FunctionExpression]bool
	memberStack   []memberKey
	// aliasGuard counts the array and tuple element types being typed;
	// aliasGuardAt is its value when each alias being built began.
	aliasGuard int
	// sensBefore is the call argument whose context the first inference
	// pass is computing: the context-sensitive arguments before it infer.
	sensBefore   ast.Expression
	aliasGuardAt map[string]int
	// declLeaves is each declaration's symbols (WarmDecl), built on first use.
	declLeaves map[ast.Node][]*binder.Symbol
	// snapshotOf is the object literal each literalSnapshot type stands for.
	snapshotOf map[*Type]*ast.ObjectLiteral

	// chainRecv holds an optional chain link's receiver while the link is
	// typed: the receiver's type with what the chain short-circuits on
	// removed. chainNonOpt is a link's type without the undefined its
	// chain adds (tsc's non-optional type of the link).
	chainRecv   map[ast.Expression]*Type
	chainNonOpt map[ast.Expression]*Type

	symTypes  map[*binder.Symbol]*Type
	resolving map[*binder.Symbol]bool
	// symCut is the open-computation key of a symbol whose type is being
	// resolved: an answer reached through its cycle is provisional, as one
	// through an open assignment is (cuts).
	symCut map[*binder.Symbol]ast.Expression
	// depLeaves caches dependentLeaf: nil info for a binding that is none.
	depLeaves map[*binder.Symbol]depEntry
	// feCycled marks a named function expression whose own name was read
	// while its type was inferred.
	feCycled map[*ast.FunctionExpression]bool
	// bodyRets memoizes BodyReturnType: a body's result, read as written
	// (a later lowering may rewrite its statements).
	bodyRets map[*ast.BlockStatement]*Type
	// fdCycled marks a function declaration whose return inference
	// re-entered it through a call: its result is any (TS7023).
	fdCycled map[*ast.FunctionDeclaration]bool
	// resolveStack is resolving in order: a cycle's members are the
	// symbols from its re-entered one up.
	resolveStack []*binder.Symbol
	// selfRef holds the functions whose self-reference type is being built
	// (typeOfSymbol).
	selfRef   map[*binder.Symbol]bool
	assigning map[ast.Expression]bool
	// storage maps each narrowable reference (a variable, a property path)
	// to its storage type: the declared type its reads narrow.
	storage map[ast.Expression]*Type
	// evolving marks the unannotated bindings declared without a value
	// (`let x;`): their type is a storage type, narrowed at every read.
	evolving map[*binder.Symbol]bool
	// evolvingArray marks the bindings declared `[]` with no type: tsc's
	// evolving array, whose element type grows along the flow by
	// push/unshift and element stores. arrayMuts lists each one's
	// mutations (built once, on first use).
	evolvingArray map[*binder.Symbol]bool
	// fitting marks the calls whose overloads are being fitted to an
	// argument (firstFittingOverloadParam).
	fitting map[*ast.CallExpression]bool
	// bivariantArgs counts the argument checks against an inferred
	// instantiation open: function parameters compare both ways there.
	bivariantArgs int
	arrayMuts     map[*binder.Symbol][]ast.Node
	// implicitAnyReported holds the reads and declarations a captured
	// evolving binding's TS7005/TS7034 was reported at.
	implicitAnyReported map[ast.Node]bool
	// inferringResult counts, per function, the inferences of its result
	// in progress (returnType).
	inferringResult map[ast.Node]int
	// literalMembers counts type literals being built; lazyQueries the
	// `typeof` queries resolved in one's members (tsc defers those).
	literalMembers, lazyQueries int
	// lazyShaping are the functions whose `typeof` signature is being
	// built (typeQueryType).
	lazyShaping map[*binder.Symbol]bool
	// inst is the outermost instantiation in progress (instantiate).
	inst *instState
	// tupleCtx holds the array literals whose tuple context is being found:
	// a generic call's context infers from the literal itself.
	tupleCtx map[*ast.ArrayLiteral]bool
	// building are the class and interface types whose members are being
	// filled in.
	building map[*Type]bool
	// methodAssigning are the js-lane members jsMethodAssignedMember is
	// typing (a cycle reads any).
	methodAssigning map[string]bool
	// suggestionCount is tsc's: the unresolved names reported so far; past
	// maximumSuggestionCount (10) none gets a spelling suggestion.
	suggestionCount int
	// failedNominal are the interface types whose construction came out
	// unanswered: a later lookup of the interned type is unanswered too.
	failedNominal map[*Type]bool
	// memberDecls are the declared members member accesses resolved to.
	memberDecls map[*ast.MemberExpression]*Property
	// cannotFound are the TS2304 reports made, one per name and position.
	cannotFound map[cannotFindKey]bool
	// implicitThis holds the `this` expressions TS2683 was reported for.
	implicitThis map[*ast.ThisExpression]bool
	thisReported map[*ast.ThisExpression]bool // TS2332 already reported
	// mappedFrom is the source of each `{ [P in keyof T]: T[P] }` over a
	// class or library instance (Readonly<Date>): the value it maps.
	mappedFrom map[*Type]*Type
	// accessNodes are the type nodes an element access over a generic key
	// is deferred as (`o[k]` for `k: K` is `typeof o[K]`), one per access.
	accessNodes map[*ast.IndexExpression]*ast.IndexedAccessType
	// spreadAs is the member of a union spread a literal is being typed
	// with (unionSpread).
	// nsObjects caches each namespace's `typeof N` object type;
	// nsBuilding marks the ones being built.
	nsObjects  map[*binder.Symbol]*Type
	nsBuilding map[*binder.Symbol]bool
	spreadAs   map[ast.Expression]*Type
	// keepSigParams is set while a call infers from a generic function
	// argument keeping its type parameters (higherOrderResult).
	keepSigParams int
	// ixNode is the indexed access node indexedAccess types through.
	ixNode *ast.IndexedAccessType
	// spreadNodes are the intersection nodes generic spreads are typed by,
	// one per literal (spreadIntersection).
	spreadNodes map[*ast.ObjectLiteral]*ast.IntersectionType
	// knownTypeNames are the names checkTypeNames takes as declared (built
	// on first use).
	knownTypeNames map[string]bool
	// modules memoises each builtin module's type (moduleType).
	modules map[string]*Type
	// exhaustive memoises exhaustiveSwitch.
	exhaustive map[*ast.SwitchStatement]bool
	// unifyDepth counts the structural inferences in progress (unify
	// through the members of another declaration).
	unifyDepth int
	// unifying holds the (target, source) pairs unify is inside, so a
	// type that reaches itself (a function returning its own type) ends
	// the walk, as tsc's inference visited set does.
	unifying map[[2]*Type]bool
	// pickingGuard holds the predicate calls whose overload is being
	// picked (narrowByPredicate).
	pickingGuard map[*ast.CallExpression]bool
	// evalNodes holds every node of code spliced in from a constant eval
	// string, which tsc's syntactic checks never see.
	evalNodes map[ast.Node]bool
	// evalDepth is how deep the check walk is in eval code (inEvalCode),
	// whose diagnostics are dropped.
	evalDepth int
	// inline counts the aliased conditions being expanded (at most 5, as
	// TypeScript).
	inline int
	// initializing are the parameters whose default is being typed.
	initializing map[*binder.Symbol]bool
	// parents maps each node to its parent, built on first use.
	parents map[ast.Node]ast.Node
	// expandoCache: a function's expando assignments by property (expando.go).
	expandoCache map[*binder.Symbol]map[string][]ast.Expression
	// declSyms maps each declaration node to its symbol, built on first use.
	declSyms map[ast.Node]*binder.Symbol
	// env names the type arguments of the generic declaration being typed.
	env []map[string]*Type
	// substs bind function type parameters while a deferred type is
	// evaluated (typeops.go).
	substs []map[*Type]*Type
	// inferPlaceholders are the `infer` placeholders of the extends clauses
	// being typed: type parameters that do not make a type deferred.
	inferPlaceholders map[*Type]bool
	// aliases memoises each alias per argument list; aliasing guards
	// recursion.
	aliases  map[string]*Type
	aliasing map[string]bool
	// aliasPending is the stand-in a function type alias reached again
	// while built hands out, keyed as aliases is.
	aliasPending map[string]*Type
	// classing marks the classes whose base instance is being typed, and
	// ctorChain those whose inherited constructor is: a circular `extends`
	// has no answer.
	classing, ctorChain map[*binder.Symbol]bool
	// constCtx counts the `as const` assertions being typed: literals keep
	// their types, array literals are tuples.
	constCtx int
	// contextualizing marks the functions whose contextual type is being
	// computed (contextualReturn).
	contextualizing map[ast.Node]bool
	// keepLiterals makes inferArgs keep an inferred literal type (what a
	// call's contextual return type can make tsc do).
	keepLiterals bool
	// literalKept marks the type parameters whose inferred literals the
	// current inference keeps, as tsc does where a union parameter decides
	// it (unify).
	literalKept map[*Type]bool
	// inferring holds the calls whose type arguments are being inferred for
	// an argument's contextual type (contextualType).
	inferring map[ast.Node]bool
	// inferSoFar is, for each call whose type arguments are being inferred,
	// what the arguments before the one being typed inferred: that one's
	// context instantiates with it (tsc's non-fixing mapper).
	inferSoFar map[ast.Node]map[*Type]*Type
	// returnCtxBusy marks the calls whose contextual type
	// returnContextCands is reading.
	returnCtxBusy map[ast.Node]bool
	// substituting marks the reads substitutedConstraint is asking the
	// contextual type of.
	substituting map[ast.Expression]bool
	// sigParams are the parameters of the function types whose results
	// are being typed, innermost last, for a `typeof` of one.
	sigParams []map[string]*Type
	// mappedCand marks the inference candidates a homomorphic mapped
	// parameter gave (a reverse mapped type), of a lower priority.
	mappedCand map[*Type]bool
	// elabWiden is set while an object literal's properties are reported
	// one by one (elaborate).
	elabWiden int
	// nesting counts the instantiations of each generic declaration in
	// progress (maxNesting).
	nesting map[*binder.Symbol]int
	// enums memoises each enum's member types.
	enums map[*binder.Symbol][]*Type
	// cuts logs each assignment value a recursive query met while it was
	// being computed; an answer that depended on one still in assigning is
	// provisional and not cached.
	cuts []ast.Expression
	// provisional memoises the answers that depended on assignment values
	// still being computed (deps): reused while every one of them is still
	// open, so a cycle of assignments is walked once, not once per path.
	provisional map[ast.Expression]provisional
	// comparing marks the other sides of comparisons being typed to narrow
	// a reference (comparedType).
	comparing map[ast.Expression]bool
	// depth is TypeOf's nesting; steps counts the flow steps of the
	// outermost query, and exhausted marks it past flowBudget.
	depth, steps int
	exhausted    bool
	// cutCount counts the cuts ever logged: a flow answer computed while it
	// grew is provisional and not cached.
	cutCount int
	// classSnapshot maps a class value typed while its statics still are
	// (classObject's partial snapshot) to its class.
	classSnapshot map[*Type]*ast.ClassDeclaration
	// reentries counts the queries that met one still being computed (a
	// symbol resolving, an overload pick in progress): an unanswered
	// result computed across one is not cached, since asked again after
	// the cycle it may be answered.
	reentries int
	// skipReturn makes signatureType give a function no inferred result
	// (any), for the shape of a deferred signature (functionType).
	skipReturn ast.Node
	// boundTP is each copy of a signature's own type parameter, by the
	// original and its constraint and default (boundCopy); tpBound marks
	// the type parameters already given theirs; boundOrigin is each copy's
	// original.
	boundTP     map[[3]*Type]*Type
	boundOrigin map[*Type]*Type
	// returnInfer counts the return-context inferences under way
	// (inferFromReturnContext).
	returnInfer int
	tpBound     map[*Type]bool
	// envBounds is each declared type parameter whose bounds may name an
	// enclosing declaration's type parameters, with their names: read again
	// under an instantiation's bindings of them.
	envBounds map[*Type]map[string]bool
	// flowCache memoises complete flow answers at branch and loop labels.
	flowCache map[flowKey]*Type
	// diags are Check's type errors; checked marks them computed.
	diags   []*diag.Diagnostic
	checked bool
	file    string // the file the statement being checked came from
}

// New returns a checker over b.
func New(b *binder.Binding) *Checker { return NewWith(b, options.Options{}) }

// NewWith returns a checker over b in the mode opts gives.
func NewWith(b *binder.Binding, opts options.Options) *Checker {
	c := &Checker{b: b, opts: opts, in: interner{byKey: map[string]*Type{}},
		nodeTypes: map[ast.Node]*Type{}, chainRecv: map[ast.Expression]*Type{}, chainNonOpt: map[ast.Expression]*Type{}, symTypes: map[*binder.Symbol]*Type{}, resolving: map[*binder.Symbol]bool{}, selfRef: map[*binder.Symbol]bool{},
		assigning: map[ast.Expression]bool{}, storage: map[ast.Expression]*Type{}, evolving: map[*binder.Symbol]bool{}, evolvingArray: map[*binder.Symbol]bool{}, fitting: map[*ast.CallExpression]bool{}, implicitAnyReported: map[ast.Node]bool{}, tupleCtx: map[*ast.ArrayLiteral]bool{}, forInKeyNodes: map[*ast.ForInStatement]ast.TypeNode{}, intersectionOf: map[*Type][]*Type{}, initializing: map[*binder.Symbol]bool{}, building: map[*Type]bool{}, failedNominal: map[*Type]bool{}, exhaustive: map[*ast.SwitchStatement]bool{}, memberDecls: map[*ast.MemberExpression]*Property{}, cannotFound: map[cannotFindKey]bool{}, implicitThis: map[*ast.ThisExpression]bool{}, thisReported: map[*ast.ThisExpression]bool{}, mappedFrom: map[*Type]*Type{}, accessNodes: map[*ast.IndexExpression]*ast.IndexedAccessType{}, spreadAs: map[ast.Expression]*Type{}, modules: map[string]*Type{},
		aliases: map[string]*Type{}, aliasing: map[string]bool{}, enums: map[*binder.Symbol][]*Type{}, classing: map[*binder.Symbol]bool{}, ctorChain: map[*binder.Symbol]bool{}, nesting: map[*binder.Symbol]int{}, contextualizing: map[ast.Node]bool{}, inferring: map[ast.Node]bool{}, flowCache: map[flowKey]*Type{}, provisional: map[ast.Expression]provisional{}, literalSnaps: map[*ast.ObjectLiteral]*Type{}, literalThisFix: map[*ast.ObjectLiteral][]ast.Expression{}, snapshotOf: map[*Type]*ast.ObjectLiteral{}, memberBusy: map[memberKey]bool{}, memberDecl: map[memberKey]*ast.FunctionDeclaration{}, cycleReported: map[*ast.FunctionDeclaration]bool{}, literalCycles: map[*ast.FunctionExpression]bool{}, nextReported: map[memberKey]bool{}}
	c.anyT = c.in.intrinsic(Any)
	c.unknownT = c.in.intrinsic(Unknown)
	c.objectT = c.in.intrinsic(NonPrimitive)
	c.emptyObjT = c.in.object(nil)
	c.thisT = c.in.intern("this", func() *Type { return &Type{Flags: TypeParam, Value: "this"} })
	c.strT = c.in.intrinsic(String)
	c.numT = c.in.intrinsic(Number)
	c.boolT = c.in.intrinsic(Boolean)
	c.bigintT = c.in.intrinsic(BigInt)
	c.symT = c.in.intrinsic(ESSymbol)
	c.voidT = c.in.intrinsic(Void)
	c.undefinedT = c.in.intrinsic(Undefined)
	c.nullT = c.in.intrinsic(Null)
	c.missingT = c.in.intern("missing", func() *Type { return &Type{Flags: Undefined, Value: "missing"} })
	c.globalThisT = c.in.intern("globalThis", func() *Type { return &Type{Flags: Object, Kind: Anonymous, Value: "globalThis"} })
	c.neverT = c.in.intrinsic(Never)
	c.trueT = c.in.literal(BooleanLiteral, "true")
	c.falseT = c.in.literal(BooleanLiteral, "false")
	c.unanswered = &Type{Flags: Any, Value: "unanswered", ID: -1}
	return c
}

type provisional struct {
	t    *Type
	deps []ast.Expression
}

func (c *Checker) allAssigning(vs []ast.Expression) bool {
	for _, v := range vs {
		if !c.assigning[v] {
			return false
		}
	}
	return true
}

// assertion returns the erased `as const` / `satisfies` assertion on e.
func (c *Checker) assertion(e ast.Expression) (ast.Assertion, bool) {
	if c.b.Module == nil {
		return ast.Assertion{}, false
	}
	prog, ok := c.b.Module.Node.(*ast.Program)
	if !ok || prog.Assertions == nil {
		return ast.Assertion{}, false
	}
	a, ok := prog.Assertions[e]
	return a, ok
}

// annotScope is the scope type node n is written in, the module's when not
// recorded.
func (c *Checker) annotScope(n ast.TypeNode) *binder.Scope {
	if s := c.b.AnnotationScope(n); s != nil {
		return s
	}
	return c.b.Module
}

// constAsserted reports `e as const`: its literal type is not fresh, and
// never widens.
func (c *Checker) constAsserted(e ast.Expression) bool {
	a, _ := c.assertion(e)
	return a.Const
}

// Unanswered reports whether t is the checker's "not modelled yet".
func (c *Checker) Unanswered(t *Type) bool { return t == c.unanswered }

// AnyType is the any type.
func (c *Checker) AnyType() *Type { return c.anyT }

// ThisType is the polymorphic `this` of a class or interface member, before
// an access binds it to the receiver.
func (c *Checker) ThisType() *Type { return c.thisT }

// WarmDecl types every symbol declaration n declares (a function, its
// parameters and their pattern leaves) now, before a rewrite of n's
// parameters after binding would hide what they were written as.
func (c *Checker) WarmDecl(n ast.Node) {
	if c.declLeaves == nil {
		c.declLeaves = map[ast.Node][]*binder.Symbol{}
		for _, s := range c.b.Symbols() {
			for _, d := range s.Declarations {
				c.declLeaves[d.Node] = append(c.declLeaves[d.Node], s)
			}
		}
	}
	for _, s := range c.declLeaves[n] {
		c.typeOfSymbol(s)
	}
	// A method's type is its class's member: build the class's instance
	// and static sides now too.
	if cls, ok := c.parentOf(n).(*ast.ClassDeclaration); ok {
		c.classInstance(cls)
		c.classValueOf(cls)
	}
}

// TypeOf returns the type of an expression, computing it once.
// StandsFor records that syn, a node a rewrite after binding put in
// orig's place (a hoisted class expression's reference), has orig's type:
// the binder never saw syn, and a type computed later reads the rewritten
// tree.
func (c *Checker) StandsFor(syn, orig ast.Expression) {
	if c.standsFor == nil {
		c.standsFor = map[ast.Expression]ast.Expression{}
	}
	c.standsFor[syn] = orig
}

func (c *Checker) TypeOf(e ast.Expression) *Type {
	if e == nil {
		return c.voidT
	}
	if o, ok := c.standsFor[e]; ok {
		return c.TypeOf(o)
	}
	if t, ok := c.chainRecv[e]; ok {
		return t
	}
	if t, ok := c.nodeTypes[e]; ok {
		return t
	}
	if p, ok := c.provisional[e]; ok && c.allAssigning(p.deps) {
		// Reused under the same open assignments; those who read it are
		// provisional too.
		c.cuts = append(c.cuts, p.deps...)
		c.cutCount++
		return p.t
	}
	if c.depth == 0 {
		c.steps = 0
	}
	if a, ok := c.assertion(e); ok {
		if a.Cast != nil {
			// `<T>e` is T, as `e as T` is.
			c.checkExpr(e)
			t := c.typeFromNode(a.Cast, c.annotScope(a.Cast))
			c.nodeTypes[e] = t
			return t
		}
		if a.Const {
			c.constCtx++
			defer func() { c.constCtx-- }()
		}
	}
	c.depth++
	mark, reBefore := len(c.cuts), c.reentries
	t := c.checkExpr(e)
	c.depth--
	if c.exhausted {
		// Past the flow budget: nothing computed in this query is kept.
		if c.depth == 0 {
			c.exhausted = false
			// The top-level query itself stays unanswered: asking again
			// would spend the whole budget again.
			c.nodeTypes[e] = c.unanswered
		}
		return c.unanswered
	}
	// The resolver's namespace object (`import * as ns`, `import()`'s
	// result): typed as its frozen literal, and marked as the namespace.
	if ce, ok := e.(*ast.CallExpression); ok && ce.Intrinsic == ast.ModuleNamespaceIntrinsic && t.Flags&Object != 0 && !t.ModuleNamespace {
		ns := *t
		ns.ModuleNamespace = true
		t = &ns
	}
	// Each distinct open assignment once: a reused provisional answer
	// appends its deps again, and duplicates compound across mutually
	// dependent bindings (`a = f(b); b = f(a)` in a loop).
	var deps []ast.Expression
	seen := map[ast.Expression]bool{}
	if mark > len(c.cuts) {
		mark = len(c.cuts) // the log was cleared when the last assignment closed
	}
	for _, v := range c.cuts[mark:] {
		if c.assigning[v] && !seen[v] {
			seen[v] = true
			deps = append(deps, v)
		}
	}
	// What an enclosing query sees of this one: its open assignments, once
	// (an assignment closed here stays closed for every enclosing query).
	c.cuts = append(c.cuts[:mark], deps...)
	if len(deps) > 0 {
		// Provisional: an enclosing computation is still open.
		c.provisional[e] = provisional{t, deps}
		return t
	}
	if c.reentries > reBefore && c.Unanswered(t) {
		// Unanswered because a cycle was met on the way (an argument
		// typed before the overloaded call around it): asked again once
		// the cycle is closed, it may be answered.
		return t
	}
	if c.classSnapshot[t] != nil {
		return t // a class's statics so far (classObject): read again once typed
	}
	c.nodeTypes[e] = t
	return t
}

// StorageTypeOf is the type of the storage e reads: for a variable or a
// property path, its declared type (an evolving let's storage type) before
// the read narrows it; otherwise TypeOf(e). reprOf(StorageTypeOf(e)) is the
// representation the value is held in, TypeOf(e) what this read may be.
func (c *Checker) StorageTypeOf(e ast.Expression) *Type {
	t := c.TypeOf(e)
	if s, ok := c.storage[e]; ok {
		return s
	}
	return t
}

func (c *Checker) checkExpr(e ast.Expression) *Type {
	if t, ok := c.chainLink(e); ok {
		return t
	}
	switch e := e.(type) {
	case *ast.NumberLiteral:
		if e.IsBigInt {
			return c.in.literal(BigIntLiteral, e.Value)
		}
		return c.in.literal(NumberLiteral, canonicalNumber(e.Value))
	case *ast.StringLiteral:
		return c.in.literal(StringLiteral, e.Value)
	case *ast.TemplateLiteral:
		return c.templateType(e)
	case *ast.BooleanLiteral:
		if e.Value {
			return c.trueT
		}
		return c.falseT
	case *ast.NullLiteral:
		if sym := c.b.UndefinedRef(e); sym != nil {
			return c.typeOfSymbol(sym)
		}
		if e.IsUndefined {
			return c.undefinedT
		}
		return c.nullT
	case *ast.Identifier:
		sym, ok := c.b.Resolve(e)
		if !ok || sym == nil {
			if prog, isProg := c.b.Module.Node.(*ast.Program); isProg && prog.StaticThis[e] != nil {
				return c.classValueOf(prog.StaticThis[e])
			}
			switch e.Name {
			case "undefined":
				return c.undefinedT
			case "globalThis":
				return c.globalThisT
			case "NaN", "Infinity":
				return c.numT
			}
			if t := c.builtinValueType(e.Name); t != nil {
				return t
			}
			c.unresolvedValue(e.Name, e, e.GetPos(), c.b.LookupScope(e))
			if c.jsUndeclaredName(e.Name, c.b.LookupScope(e)) {
				return c.anyT // JavaScript's undeclared global: tsc's error type
			}
			if !c.opts.CompatJS() && lib.GlobalValue(e.Name) && c.b.LookupScope(e) != nil && c.libraryLoaded() {
				// A global tsc's library declares that this compiler's does
				// not (`ResizeObserver`): its read throws a ReferenceError
				// here, so no value of a narrower type is ever produced.
				return c.anyT
			}
			return c.unanswered // a global the checker has no declarations for yet
		}
		if t := c.paramDefaultReference(e, sym); t != nil {
			return t
		}
		if c.ownInitializerRead(e, sym) {
			c.reportOwnInit(sym)
			return c.anyT // TS7022: tsc's error type
		}
		t := c.narrowedType(e, sym, c.typeOfSymbol(sym))
		if c.hoistedVarRead(e, sym) && !c.Unanswered(t) && t.Flags&(Any|Unknown) == 0 {
			t = c.in.union(t, c.undefinedT)
		}
		return t
	case *ast.SuperExpression:
		// `super.m` reads the base class's instance members; in a static
		// member, the base class's own (its value, `typeof B`).
		receiver := false
		switch p := c.parentOf(e).(type) {
		case *ast.MemberExpression:
			receiver = p.Object == ast.Expression(e)
		case *ast.IndexExpression:
			receiver = p.Object == ast.Expression(e) // `super["m"]`
		}
		if receiver {
			t := c.thisType(e)
			if t.Flags&Object != 0 && t.Kind == Instance && t.Base != nil {
				return t.Base
			}
			if t.Flags&Object != 0 && t.ClassOf != nil {
				if bt := c.baseClassValue(t.ClassOf); bt != nil {
					return bt
				}
			}
		}
		return c.unanswered
	case *ast.NewTargetExpression:
		// tsc types it as the constructor or function; no `new` reaches a
		// method, accessor, field initializer or static block.
		if e.Ctx == ast.NewTargetUndefined {
			return c.undefinedT
		}
		// In JavaScript (no tsc diagnostic rides on it), one no `new` can
		// reach (an async or generator function's) is undefined, as it runs.
		if c.opts.CompatJS() && e.Ctx != ast.NewTargetConstructor && c.unconstructibleFn(e) {
			return c.undefinedT
		}
		return c.anyT
	case *ast.ThisExpression:
		// `this` narrows too (`if (this.isLeader()) this.lead()`).
		t := c.thisType(e)
		if c.Unanswered(t) || !narrowable(t) {
			return t
		}
		f, local := c.flowAtRoot(e)
		if !local && !c.closureFlow(e, f) {
			return t
		}
		_, sym, _ := c.b.ThisAt(e)
		nt, _ := c.flowType(f, ref{sym: sym}, t, newFlowWalk())
		return nt
	case *ast.BinaryExpression:
		return c.checkBinary(e)
	case *ast.ImportCallExpression:
		// `import(x)` of a specifier that is not a string literal: a
		// promise of any module (tsc's Promise<any>).
		if _, lit := e.Specifier.(*ast.StringLiteral); !lit {
			c.TypeOf(e.Specifier)
			if p := c.promiseOf(c.anyT); p != nil {
				return p
			}
			return c.unanswered
		}
		if p := c.promiseOf(c.importedModule(e)); p != nil {
			return p
		}
		return c.unanswered
	case *ast.AwaitExpression:
		// An awaited `[]` has no array context: never[], as tsc types it.
		if al, ok := e.Argument.(*ast.ArrayLiteral); ok && len(al.Elements) == 0 && !c.opts.CompatJS() {
			if at := c.TypeOf(al); c.Unanswered(at) {
				return c.in.array(c.neverT)
			}
		}
		return c.awaited(c.TypeOf(e.Argument), 0)
	case *ast.ClassExpression:
		// `class { … }` as a value: its constructor and statics (`typeof C`).
		if e.Decl != nil {
			if sym := c.symbolOf(e.Decl); sym != nil {
				return c.typeOfSymbol(sym)
			}
		}
		return c.unanswered
	case *ast.YieldExpression:
		// What `.next(v)` sends: any without a declared generator type;
		// `yield* g` is g's return value.
		if e.Argument == nil {
			return c.anyT
		}
		at := c.TypeOf(e.Argument)
		if e.Delegate && !c.Unanswered(at) && at.Flags&Object != 0 && at.Kind == Interface && at.Symbol != nil &&
			(at.Symbol.Name == "Generator" || at.Symbol.Name == "AsyncGenerator") && len(at.TypeArgs) > 1 && c.inLibrary(at.Symbol.Scope) {
			return at.TypeArgs[1]
		}
		return c.anyT
	case *ast.UnaryExpression:
		arg := c.TypeOf(e.Arg)
		switch e.Op {
		case "!":
			return c.boolT
		case "typeof":
			// The eight results typeof can give (tsc's typeofType).
			var names []*Type
			for _, n := range []string{"bigint", "boolean", "function", "number", "object", "string", "symbol", "undefined"} {
				names = append(names, c.in.literal(StringLiteral, n))
			}
			return c.in.union(names...)
		case "void":
			return c.undefinedT
		case "-", "~":
			if lit, ok := e.Arg.(*ast.NumberLiteral); ok && e.Op == "-" {
				// `-1` is the literal type -1 (`-1n` too).
				v := c.TypeOf(lit).Value
				switch {
				case v == "0":
				case strings.HasPrefix(v, "-"):
					v = v[1:]
				default:
					v = "-" + v
				}
				if lit.IsBigInt {
					return c.in.literal(BigIntLiteral, v)
				}
				return c.in.literal(NumberLiteral, v)
			}
			if isBigIntLike(arg) {
				return c.bigintT
			}
			return c.numT
		case "+":
			if _, ok := e.Arg.(*ast.NumberLiteral); ok && arg.Flags&NumberLiteral != 0 {
				return arg // `+1` is the literal type 1
			}
			return c.numT
		case "delete":
			return c.boolT
		}
		return c.unanswered
	case *ast.UpdateExpression:
		if isBigIntLike(c.TypeOf(e.Arg)) {
			return c.bigintT
		}
		return c.numT
	case *ast.ConditionalExpression:
		c.TypeOf(e.Test)
		branches := []*Type{c.TypeOf(e.Consequent), c.TypeOf(e.Alternate)}
		for i, x := range []ast.Expression{e.Consequent, e.Alternate} {
			if c.Unanswered(branches[i]) && isEmptyArrayLiteral(x) && c.constCtx == 0 {
				// An empty branch: never[], with or without a contextual
				// type, which subtype reduction drops beside the other
				// branch's array.
				branches[i] = c.in.array(c.neverT)
			}
		}
		if kept := dropNeverArrays(branches); len(kept) == 1 {
			return kept[0]
		}
		if !c.Unanswered(branches[0]) && !c.Unanswered(branches[1]) {
			// tsc's subtype reduction of an object literal branch the other
			// branch's type covers (`o ? o : {}` is o's type) ...
			if t := c.literalBranchCovered(e.Consequent, branches[0], branches[1]); t != nil {
				return t
			}
			if t := c.literalBranchCovered(e.Alternate, branches[1], branches[0]); t != nil {
				return t
			}
			// ... and its normalization of object literals in a union: each
			// gets the others' missing properties as optional undefined
			// (`c ? { a: 1 } : {}` is `{ a: number } | { a?: undefined }`).
			if isObjectLiteralNode(e.Consequent) && isObjectLiteralNode(e.Alternate) {
				if n0, n1, ok := c.normalizeLiteralPair(branches[0], branches[1]); ok {
					return c.join(n0, n1)
				}
			}
			// ... and of a derived class's instance beside its base's
			// (`c ? new B() : new A()` is A).
			if t := c.derivedBranchReduced(branches[0], branches[1]); t != nil {
				return t
			}
		}
		return c.join(branches[0], branches[1])
	case *ast.AssignmentExpression:
		left := c.TypeOf(e.Left)
		right := c.TypeOf(e.Right)
		if e.Op == "=" {
			return right
		}
		if id, ok := e.Left.(*ast.Identifier); ok {
			// A compound assignment to an evolving `let x = null` / `let s`
			// is any: its target reads the declared implicit any (tsc).
			if sym, ok := c.b.Resolve(id); ok && sym != nil && c.evolving[sym] && !c.evolvingArray[sym] {
				return c.anyT
			}
		}
		if e.Op == "??=" || e.Op == "||=" || e.Op == "&&=" {
			// A logical assignment reads its target as declared, not as
			// narrowed (`let n: 1 | 2 | null = null; n ??= 1` is 1 | 2).
			left = c.StorageTypeOf(e.Left)
		}
		// `x op= y` is typed as `x op y` (tsc's checkBinaryLikeExpression).
		return c.binaryResult(strings.TrimSuffix(e.Op, "="), left, right)
	case *ast.SequenceExpression:
		var last *Type
		for _, x := range e.Exprs {
			last = c.TypeOf(x)
		}
		return last
	case *ast.ArrayLiteral:
		if len(e.Elements) == 0 {
			if c.opts.CompatJS() && c.constCtx == 0 && !c.joinedBranch(e) {
				// JavaScript's `[]` with no array context is any[] (tsc
				// widens it without noImplicitAny): `xs.reduce(f, [])`.
				// A conditional's or `||`/`??`'s branch joins the other one
				// (never[], dropped beside an array).
				ct := c.contextualType(e)
				switch {
				case ct == nil || c.Unanswered(ct) || ct.Flags&Object == 0 || ct.Kind != Array && ct.Kind != Tuple:
					return c.in.array(c.anyT)
				case ct.Kind == Array:
					return ct // an array context's own type
				}
			}
			if !c.opts.CompatJS() && c.constCtx == 0 && !c.joinedBranch(e) {
				if _, recv := c.parentOf(e).(*ast.MemberExpression); recv {
					// A receiver (`[].concat(x)`) has no context: never[]
					// under strictNullChecks, as tsc gives.
					return c.in.array(c.neverT)
				}
			}
			if c.constCtx == 0 {
				// Against a rest tuple (`[string, ...number[]]`): the empty
				// tuple `[]`, short of its leading elements.
				if ct := c.contextualType(e); ct != nil && !c.Unanswered(ct) && ct.Flags&Object != 0 && ct.Flags&Union == 0 && ct.Kind == Array && ct.Head != nil {
					return c.in.tuple(nil)
				}
			}
			return c.unanswered // an empty literal's element type comes from context
		}
		var elems []*Type
		spreads := false
		for _, x := range e.Elements {
			if sp, ok := x.(*ast.SpreadElement); ok {
				// `[...xs, 1]`: the spread's iterated elements join the
				// element type (a tuple's shape from a spread is not modelled).
				at := c.TypeOf(sp.Arg)
				it := c.iteratedType(at)
				if c.Unanswered(it) && isEmptyArrayLiteral(sp.Arg) {
					it = c.neverT // `[...[]]`: nothing
				}
				if c.Unanswered(it) && !c.Unanswered(at) && at.Flags&(NumberLike|BooleanLike|BigIntLike|Undefined|Null) != 0 && c.opts.CompatJS() {
					// A primitive with no iterator (`[...5]`): the spread
					// throws at run time; nothing joins the element type.
					it = c.anyT
				}
				if c.Unanswered(it) || c.constCtx > 0 {
					return c.unanswered
				}
				elems = append(elems, it)
				spreads = true
				continue
			}
			t := c.TypeOf(x)
			if c.Unanswered(t) && isEmptyArrayLiteral(x) && c.constCtx == 0 && c.contextualType(x) == nil {
				// An empty element with no context: never[] (strictNullChecks),
				// which the element type's subtype reduction drops beside
				// another array.
				t = c.in.array(c.neverT)
			}
			if c.Unanswered(t) {
				return c.unanswered
			}
			if c.constCtx == 0 && !c.constAsserted(x) && !c.keepsLiteral(x, t) {
				t = c.widenFrom(x, t)
			}
			elems = append(elems, t)
		}
		elems = dropNeverArrays(elems)
		if c.constCtx > 0 {
			return c.in.tuple(elems) // `as const`: a (readonly) tuple of literals
		}
		if spreads {
			st := c.spreadTuple(e)
			if st != nil && c.tupleContext(e, len(st.Elems)+len(st.Head)) != nil {
				return st
			}
			if c.tupleContext(e, len(elems)) != nil {
				if st != nil {
					return st // a context of numeric members takes the tuple
				}
				return c.unanswered
			}
			return c.in.array(c.join(elems...))
		}
		if tt := c.tupleContext(e, len(elems)); tt != nil {
			return c.in.tuple(elems) // `pair?: [number, number]` takes `[1, 2]`
		}
		return c.in.array(c.join(elems...))
	case *ast.ObjectLiteral:
		// A spread of a union of object types: the literal per member,
		// joined (tsc's getSpreadType maps over the union).
		if arg, u := c.unionSpread(e); arg != nil {
			out := make([]*Type, 0, len(u.Types))
			for _, m := range u.Types {
				c.spreadAs[arg] = m
				t := c.checkExpr(e)
				delete(c.spreadAs, arg)
				if c.Unanswered(t) {
					return t
				}
				out = append(out, t)
			}
			return c.in.union(out...)
		}
		// A spread of a generic value (`{ ...from, ...p }`): the
		// intersection of the spread types and the literal's other
		// properties (tsc's getSpreadType over a generic object type).
		if gs := c.genericSpreads(e); len(gs) > 0 {
			empty := c.in.object(nil)
			var parts []*Type
			for _, a := range gs {
				parts = append(parts, c.TypeOf(a))
				c.spreadAs[a] = empty
			}
			rest := c.checkExpr(e)
			for _, a := range gs {
				delete(c.spreadAs, a)
			}
			if c.Unanswered(rest) {
				return rest
			}
			if rest.Flags&Object == 0 || len(rest.Props) > 0 || rest.StringIndex != nil || rest.NumberIndex != nil {
				parts = append(parts, rest)
			}
			return c.spreadIntersection(e, parts)
		}
		// A method's result may read `this`: while the literal is typed,
		// `this` is a snapshot of its other properties (literalThis).
		if _, busy := c.literalSnaps[e]; !busy && hasMethodValue(e) && c.contextualType(e) == nil {
			c.literalSnaps[e] = nil
			c.literalSnaps[e] = c.literalSnapshot(e)
			defer func() {
				delete(c.literalSnaps, e)
				// A `this` that read the snapshot is asked again: then it
				// is the finished type.
				for _, x := range c.literalThisFix[e] {
					delete(c.nodeTypes, x)
					delete(c.provisional, x)
				}
				delete(c.literalThisFix, e)
			}()
		}
		var props []*Property
		// A property naming an earlier one replaces it in place: the object
		// built keeps each key where it was first added (ES own-property
		// order), the layout this type stands for.
		put := func(np *Property) {
			for i, r := range props {
				if r.Name == np.Name {
					props[i] = np
					return
				}
			}
			props = append(props, np)
		}
		spreadAny := false
		// A computed key of a non-literal string or number type adds tsc's
		// index signature (getObjectLiteralIndexInfo), of the union of the
		// properties it applies to.
		var strIdx, numIdx bool
		var computedStr, computedNum []*Type
		// tsc's getSpreadType keeps an index signature only where every
		// part has one (getUnionIndexInfos): the spreads' signatures, and
		// none once a property of the literal's own sits beside them.
		var spreadStr, spreadNum []*Type
		spreads, spreadIdxAll, ownProps := 0, true, false
		for _, p := range e.Properties {
			if sp, isSpread := p.Value.(*ast.SpreadElement); isSpread && p.KeyExpr == nil && p.Key == "" {
				// tsc's getSpreadType: spreading an any makes the literal any.
				st := c.TypeOf(sp.Arg)
				if m := c.spreadAs[sp.Arg]; m != nil {
					st = m // one member of a union spread (unionSpread)
				}
				if c.Unanswered(st) {
					return c.unanswered
				}
				if st.Flags&Any != 0 {
					spreadAny = true
					continue
				}
				if c.invalidSpread(st) {
					// tsc's TS2698: the literal is its error type, any.
					spreadAny = true
					continue
				}
				// A falsy member spreads nothing: what is left, its
				// properties optional (tsc's getSpreadType over
				// removeDefinitelyFalsyTypes, `{ ...(c && o) }`).
				maybe := false
				if rest := c.WithoutFalsy(st); rest != nil && rest != st {
					st, maybe = rest, true
				}
				if !c.spreadable(st) {
					return c.unanswered
				}
				spreads++
				if st.StringIndex != nil {
					spreadStr = append(spreadStr, st.StringIndex)
				} else {
					spreadIdxAll = false
				}
				if st.NumberIndex != nil {
					spreadNum = append(spreadNum, st.NumberIndex)
				}
				// tsc's getSpreadType: the spread's properties override
				// earlier ones, writable, ahead of the earlier ones left.
				right := make([]*Property, 0, len(st.Props))
				for _, q := range st.Props {
					if st.Kind == Instance && !instanceSpreads(q) {
						continue
					}
					np := &Property{Name: q.Name, Type: q.Type, Optional: q.Optional || maybe}
					if maybe && !q.Optional {
						np = &Property{Name: q.Name, Type: c.in.union(q.Type, c.undefinedT), Optional: true}
					}
					right = append(right, np)
				}
				for _, np := range right {
					if np.Optional {
						for _, r := range props {
							if r.Name == np.Name {
								// An optional property keeps an earlier one,
								// as the union of both.
								np = &Property{Name: np.Name, Type: c.in.union(r.Type, c.NonUndefined(np.Type)), Optional: r.Optional}
								break
							}
						}
					}
					put(np)
				}
				continue
			}
			key := p.Key
			if p.KeyExpr != nil && p.AccessorKind == "" {
				kt := c.TypeOf(p.KeyExpr)
				switch {
				case c.Unanswered(kt):
					return c.unanswered
				case kt.Flags&(StringLiteral|NumberLiteral) != 0 && kt.Flags&Union == 0:
					key = kt.Value // `[k]` with `const k = 'a'`: the property a
				case kt.Flags&(String|Number|Any) != 0 && kt.Flags&Union == 0:
					vt := c.TypeOf(p.Value)
					if c.Unanswered(vt) {
						return c.unanswered
					}
					if c.constCtx == 0 && !c.constAsserted(p.Value) {
						vt = c.widenFrom(p.Value, vt)
					}
					if kt.Flags&Number != 0 {
						numIdx = true
						computedNum = append(computedNum, vt)
					} else {
						strIdx = true
					}
					computedStr = append(computedStr, vt)
					continue
				default:
					return c.unanswered
				}
			} else if p.KeyExpr != nil {
				return c.unanswered
			}
			// A spread was handled above: an empty Key here is the
			// property "" (`{ "": 0 }`).
			if p.AccessorKind != "" {
				// A getter's property is what it returns, a setter-only
				// one its parameter's type; a getter alone is readonly.
				ft := c.TypeOf(p.Value)
				if c.Unanswered(ft) || ft.Flags&Object == 0 || ft.Kind != Function {
					return c.unanswered
				}
				var at *Type
				if p.AccessorKind == "get" {
					at = ft.Ret()
				} else if len(ft.Params) > 0 {
					at = ft.Params[0]
				}
				if at == nil || c.Unanswered(at) {
					return c.unanswered
				}
				var prev *Property
				for _, q := range props {
					if q.Name == p.Key {
						prev = q
						break
					}
				}
				np := &Property{Name: p.Key, Type: at, Readonly: p.AccessorKind == "get"}
				if prev != nil && prev.Readonly != np.Readonly {
					// The pair: the getter's type, writable.
					get, set := np, prev
					if p.AccessorKind == "set" {
						get, set = prev, np
					}
					np = &Property{Name: p.Key, Type: get.Type}
					if set.Type != get.Type {
						np.Write = set.Type
					}
				}
				put(np)
				continue
			}
			t := c.TypeOf(p.Value)
			if c.Unanswered(t) && isEmptyArrayLiteral(p.Value) && c.constCtx == 0 && c.contextualType(p.Value) == nil && !c.opts.CompatJS() {
				// An empty property value with no context: never[]
				// (strictNullChecks), as an empty array element is.
				t = c.in.array(c.neverT)
			}
			if c.Unanswered(t) {
				return c.unanswered
			}
			if c.constCtx == 0 && !c.constAsserted(p.Value) && !c.keepsLiteral(p.Value, t) {
				t = c.widenFrom(p.Value, t)
			}
			put(&Property{Name: key, Type: t}) // a duplicate: the last one wins
			ownProps = true
		}
		if spreadAny {
			return c.anyT
		}
		if len(spreadStr) > 0 || len(spreadNum) > 0 {
			if strIdx || numIdx {
				return c.unanswered // a computed key's signature beside them
			}
			if !spreadIdxAll || ownProps {
				// A part without the signature: tsc drops it.
				return c.in.object(props)
			}
			var num *Type
			if len(spreadNum) == spreads {
				num = c.in.union(spreadNum...)
			}
			return c.in.indexed(props, c.in.union(spreadStr...), num)
		}
		if strIdx || numIdx {
			var str, num *Type
			if strIdx {
				ts := append([]*Type(nil), computedStr...)
				for _, q := range props {
					ts = append(ts, q.Type)
				}
				str = c.in.union(ts...)
			}
			if numIdx {
				ts := append([]*Type(nil), computedNum...)
				for _, q := range props {
					if numericName(q.Name) {
						ts = append(ts, q.Type)
					}
				}
				num = c.in.union(ts...)
			}
			return c.in.indexed(props, str, num)
		}
		return c.in.object(props)
	case *ast.MemberExpression:
		if m := c.namespaceMember(e); m != nil {
			if n := len(m.Declarations); n > 0 && c.builtinNamespace(e.Object) {
				// A namespace's member (or a builtin reached through
				// globalThis) keeps its declaration; an overloaded
				// function, each overload's.
				p := &Property{Name: e.Property, Decl: m.Declarations[n-1].Node}
				if n > 1 {
					for _, d := range m.Declarations {
						p.Decls = append(p.Decls, d.Node)
					}
				}
				c.memberDecls[e] = p
			}
			return c.typeOfSymbol(m) // a namespace's member
		}
		if th, ok := e.Object.(*ast.ThisExpression); ok {
			if t, ok := c.thisMemberAhead(th, e); ok {
				return t
			}
		}
		obj := c.TypeOf(e.Object)
		for i := 0; !c.Unanswered(obj) && obj.Flags&TypeParam != 0 && obj.Flags&Union == 0 && obj.Constraint != nil && i < 8; i++ {
			// A value of a type parameter reads its constraint's members
			// (tsc's apparent type of a type parameter).
			obj = obj.Constraint
		}
		if obj == c.globalThisT {
			// A global through the global object held in a binding (`const
			// g = globalThis; g.setTimeout`), else no global of that name:
			// tsc's error type (TS7017), any.
			if c.b.Globals != nil {
				if sym := c.b.Globals.Symbols.Get(e.Property); sym != nil {
					return c.typeOfSymbol(sym)
				}
			}
			if t := c.builtinValueType(e.Property); t != nil {
				return t
			}
			return c.anyT
		}
		if c.building[obj] && obj.Prop(e.Property) == nil {
			c.buildingMember(obj, e.Property)
		}
		if cd := c.classSnapshot[obj]; cd != nil && obj.Prop(e.Property) == nil && declaresStatic(cd, e.Property) {
			// A static the class declares, not yet typed (`K.s` read while
			// `s`'s own result builds K's instance): a cycle, asked again
			// once the class is typed.
			c.reentries++
			return c.unanswered
		}
		if lit := c.snapshotOf[obj]; lit != nil && obj.Prop(e.Property) == nil && !literalNames(lit, e.Property) && !objectProto[e.Property] {
			// `this.b` in a literal's method naming no property of it: tsc's
			// error type, any (reported TS2339 against the finished type).
			return c.anyT
		}
		switch {
		case c.Unanswered(obj):
			return c.unanswered
		case obj.Flags&Any != 0:
			return c.anyT // any's members are any, `?.` included
		case obj.Kind == Tuple && obj.Flags&Object != 0 && e.Property == "length":
			// A fixed tuple's length is its size; with optional elements,
			// each size it may have (`0 | 1`).
			var ls []*Type
			for n := obj.minLen(); n <= len(obj.Elems); n++ {
				ls = append(ls, c.in.literal(NumberLiteral, strconv.Itoa(n)))
			}
			return c.in.union(ls...)
		case obj.Kind == Array && obj.Flags&Object != 0 && e.Property == "length",
			isStringLike(obj) && e.Property == "length":
			return c.numT
		default:
			// An object, or a union of objects that all declare the property
			// (a discriminated union's tag); a primitive or an array reads
			// its apparent type's members (String's, Array<T>'s).
			var ts []*Type
			// A receiver that may be null or undefined reads its other
			// members' property: tsc reports the access (TS18047-18049,
			// nonnull.go) and types it from the non-nullable type.
			ms := members(obj)
			if nn := c.NonNullable(obj); nn != obj && nn.Flags&Never == 0 && !c.Unanswered(nn) {
				ms = members(nn)
			}
			if at := c.unionArrayReceiver(ms, e.Property); at != nil {
				// A method of a union of arrays reads off an array of
				// their elements (tsc 5.2): `(string[] | never[]).map`.
				ms = []*Type{at}
			}
			for _, m := range ms {
				recv := m
				if am := c.apparentType(m); am != nil {
					m = am
				}
				if m.Flags&Object != 0 && m.Kind == Function && len(ms) == 1 {
					// A function's expando property, else Function's member.
					if t := c.expandoType(e.Object, e.Property); t != nil {
						return t
					}
					if t := c.functionMember(e.Property); t != nil {
						return t
					}
					// Object.prototype's (`f.constructor`).
					if p := c.objectMember(e.Property); p != nil {
						return p.Type
					}
					return c.unanswered
				}
				if m.Flags&Object == 0 || (m.Kind != Anonymous && m.Kind != Instance && m.Kind != Interface) {
					return c.unanswered
				}
				p := m.Prop(e.Property)
				if p == nil && m.StringIndex == nil && (m.ClassOf != nil || len(m.Calls) > 0 || len(m.Constructs) > 0) && !c.declaresStatic(m.ClassOf, e.Property) {
					// A class value or another callable reads Function's
					// members (`C.name`, `this.name` in a static block).
					if ft := c.functionMember(e.Property); ft != nil {
						ts = append(ts, ft)
						continue
					}
				}
				if p == nil {
					// Object.prototype's (`o.hasOwnProperty`, `o.toString`),
					// ahead of a string index signature as in tsc.
					p = c.objectMember(e.Property)
				}
				if p == nil {
					if m.StringIndex != nil {
						// Through a string index signature (`env.PATH`).
						ts = append(ts, m.StringIndex)
						continue
					}
					if c.jsUndeclaredMember(m, e.Property) {
						ts = append(ts, c.anyT) // tsc's error type, unreported in JavaScript
						continue
					}
					if t := c.jsMethodAssignedMember(m, e.Property); t != nil {
						ts = append(ts, t)
						continue
					}
					if c.opts.CompatJS() && recv != m && obj.Flags&Union == 0 && recv.Flags&Object != 0 && (recv.Kind == Array || recv.Kind == Tuple) && c.libLacks(m, e.Property) {
						// An array's named property (`arr.city = …`): tsc
						// reads it as any in JavaScript.
						ts = append(ts, c.anyT)
						continue
					}
					if c.opts.CompatJS() && obj.Flags&Union == 0 && (m.Kind == Instance || m.Kind == Interface) && c.libLacks(m, e.Property) {
						// A member a library type lacks (`buf.transferToImmutable`
						// on a runtime without it): any in JavaScript.
						ts = append(ts, c.anyT)
						continue
					}
					if recv != m && obj.Flags&Union == 0 && recv.Flags&(StringLike|NumberLike|BooleanLike|BigIntLike) != 0 && c.libLacks(m, e.Property) {
						// A member a primitive's wrapper lacks (`(5).x`):
						// tsc's error type (TS2339, checkProperty).
						ts = append(ts, c.anyT)
						continue
					}
					if strings.HasPrefix(e.Property, "#") && (m.Kind == Instance || m.Kind == Anonymous && m.ClassOf != nil) {
						// A private name the instance lacks (a static one read
						// off `this`), or the class lacks (an instance one read
						// off a static method's `this`): tsc's error type
						// (TS2339, checkProperty).
						ts = append(ts, c.anyT)
						continue
					}
					if c.opts.CompatJS() && obj.Flags&Union == 0 && m.Kind == Anonymous && m.ClassOf != nil && !strings.HasPrefix(e.Property, "#") {
						// A static the class does not declare (one given at
						// run time, or deleted): any in JavaScript.
						ts = append(ts, c.anyT)
						continue
					}
					if c.inEvalCode(e) {
						// Code spliced in from a string, which tsc never
						// checks: JavaScript's read, any.
						ts = append(ts, c.anyT)
						continue
					}
					return c.unanswered
				}
				ts = append(ts, c.withThis(p.Type, recv))
				if len(ms) == 1 && p.Decl != nil {
					c.memberDecls[e] = p
				}
			}
			return c.narrowedMember(e, c.in.union(ts...))
		}
	case *ast.IndexExpression:
		obj := c.TypeOf(e.Object)
		if c.invalidIndexKey(c.TypeOf(e.Index)) {
			return c.anyT // tsc's error type (TS2538), whatever the receiver
		}
		if c.Unanswered(obj) {
			return c.unanswered
		}
		if obj.Flags&Any != 0 {
			return c.anyT
		}
		key, literal := c.accessKey(e.Index)
		if kt := c.TypeOf(e.Index); !literal && kt.Flags&(String|Number) != 0 && kt.Flags&Union == 0 &&
			obj.Kind == Anonymous && obj.StringIndex == nil && obj.NumberIndex == nil && len(obj.Props) > 0 && c.freshLiteralRead(e) {
			// An object literal itself read by a string or number key: its
			// properties' types, or undefined (tsc's
			// getPropertyTypeForIndexType).
			ts := []*Type{c.undefinedT}
			for _, p := range obj.Props {
				ts = append(ts, p.Type)
			}
			return c.in.union(ts...)
		}
		if obj.Flags&TypeParam != 0 && obj.Flags&Union == 0 && obj.Constraint != nil && !c.Unanswered(obj.Constraint) &&
			obj.Constraint.Flags&Object != 0 && obj.Constraint.Flags&Union == 0 && (obj.Constraint.Kind == Array || obj.Constraint.Kind == Tuple) {
			// An array-constrained type parameter read by a number (`o[0]`,
			// `O extends Opt[]`): its constraint's element.
			if _, err := strconv.Atoi(key); literal && err == nil {
				obj = obj.Constraint
			} else if kt := c.TypeOf(e.Index); !literal && !c.Unanswered(kt) && kt.Flags&Union == 0 && kt.Flags&(Number|NumberLiteral) != 0 {
				obj = obj.Constraint
			}
		}
		if obj.Flags&Deferred != 0 && isTupleNode(obj.deferNode) {
			if t := c.variadicElement(e, obj, key, literal); t != nil {
				return t
			}
		}
		// A well-known symbol member of an array (`xs[Symbol.iterator]`):
		// Array<T>'s, never an element.
		// A primitive's (`"ab"[Symbol.iterator]`) is its apparent
		// interface's (String's).
		if literal && strings.HasPrefix(key, "@@") && (obj.Flags&Object != 0 && (obj.Kind == Array || obj.Kind == Tuple) || obj.Flags&Union == 0 && isStringLike(obj)) {
			if am := c.apparentType(obj); am != nil {
				if p := am.Prop(key); p != nil {
					return p.Type
				}
			}
			return c.unanswered
		}
		// A primitive's named member (`x['charAt']`, `true['toString']`):
		// its apparent interface's, as `x.charAt` reads.
		if literal && obj.Flags&Union == 0 && obj.Flags&(String|StringLiteral|Number|NumberLiteral|Boolean|BooleanLiteral|BigInt|BigIntLiteral) != 0 && !numericName(key) {
			if am := c.apparentType(obj); am != nil {
				if p := am.Prop(key); p != nil {
					return p.Type
				}
			}
			if p := c.objectMember(key); p != nil {
				return p.Type // Object.prototype's (`x["hasOwnProperty"]`)
			}
		}
		// A union of arrays, tuples and strings (`number[] | string[][]`):
		// each member's element, joined (tsc's indexed access distributes
		// over the union).
		if obj.Flags&Union != 0 {
			if t, ok := c.unionElementAccess(e, obj, key, literal); ok {
				return t
			}
		}
		// A generic receiver read by a generic key (`s[k]`, `s: T`, `k:
		// keyof T`): the indexed access `T[keyof T]`, evaluated once bound.
		if !literal && (obj.Flags&(TypeParam|Deferred) != 0 || obj.Flags&Union == 0 && c.generic(obj)) {
			if kt := c.TypeOf(e.Index); !c.Unanswered(kt) && kt.Flags&Union == 0 && kt.Flags&(TypeParam|Deferred) != 0 {
				return c.genericAccess(e, obj, kt)
			}
		}
		// An expando member a constant key declared (`foo[k] = "ok"`).
		if literal {
			if t := c.expandoType(e.Object, key); t != nil {
				return t
			}
		}
		// `object` read by a computed string or number key: an implicit
		// any (TS7053).
		if obj.Flags&NonPrimitive != 0 && obj.Flags&Union == 0 && !literal {
			if kt := c.TypeOf(e.Index); !c.Unanswered(kt) && kt.Flags&Union == 0 && (isStringLike(kt) || kt.Flags&(Number|NumberLiteral|Any) != 0) {
				return c.anyT
			}
		}
		// A union of object types read by a literal key every member has
		// (`val["kind"]`): each member's property, joined, as `val.kind`.
		if literal && obj.Flags&Union != 0 && !strings.HasPrefix(key, "@@") {
			var ts []*Type
			for _, m := range obj.Types {
				if m.Flags&Object == 0 || m.Flags&Union != 0 || (m.Kind != Anonymous && m.Kind != Instance && m.Kind != Interface) {
					ts = nil
					break
				}
				p := m.Prop(key)
				if p == nil {
					ts = nil
					break
				}
				pt := c.withThis(p.Type, m)
				if p.Optional {
					pt = c.in.union(pt, c.undefinedT)
				}
				ts = append(ts, pt)
			}
			if len(ts) > 0 {
				return c.narrowedMember(e, c.in.union(ts...))
			}
		}
		switch {
		case isStringLike(obj) && obj.Flags&Union == 0:
			// A string's number index (`"lol"[1]`): a string, as the String
			// interface's `[index: number]: string` reads.
			if kt := c.TypeOf(e.Index); !c.Unanswered(kt) && kt.Flags&Union == 0 && kt.Flags&(Number|NumberLiteral) != 0 {
				return c.strT
			}
		case obj.Flags&Object != 0 && obj.Kind == Array:
			if literal && obj.Head != nil {
				if i, err := strconv.Atoi(key); err == nil && i >= 0 {
					return c.narrowedMember(e, obj.elemAt(i))
				}
			}
			if literal {
				return c.narrowedMember(e, obj.Elem)
			}
			return obj.Elem
		case obj.Flags&Object != 0 && obj.Kind == Tuple && literal:
			if i, err := strconv.Atoi(key); err == nil && i >= 0 && i < len(obj.Elems) {
				return c.narrowedMember(e, obj.Elems[i])
			}
		case obj.Flags&Object != 0 && obj.Kind == Tuple && len(obj.Elems) > 0:
			// A number key reads any element: their union (tsc's number
			// index of a tuple).
			if kt := c.TypeOf(e.Index); !c.Unanswered(kt) && kt.Flags&Union == 0 && kt.Flags&(Number|NumberLiteral) != 0 {
				if obj.Opt > 0 {
					return c.in.union(append(append([]*Type(nil), obj.Elems...), c.undefinedT)...)
				}
				return c.in.union(obj.Elems...)
			}
		case obj.Flags&Object != 0 && (obj.Kind == Anonymous || obj.Kind == Instance || obj.Kind == Interface):
			if literal {
				if p := obj.Prop(key); p != nil {
					return c.narrowedMember(e, c.withThis(p.Type, obj)) // `this` is the receiver, as for `o.k`
				}
			}
			kt := c.TypeOf(e.Index)
			// A key of a type parameter (`k: K`, `K extends keyof S`): the
			// indexed access `S[K]`, evaluated once K is bound.
			if !c.Unanswered(kt) && kt.Flags&TypeParam != 0 && kt.Flags&Union == 0 {
				return c.genericAccess(e, obj, kt)
			}
			// A union of literal keys (`k: keyof S`) reads each member's
			// property, joined (tsc's indexed access over a union).
			if ts, ok := c.literalKeyUnionProps(obj, kt); ok {
				return c.in.union(ts...)
			}
			// An index signature: a numeric key reads the number index,
			// else the string one.
			if obj.NumberIndex != nil && kt.Flags&(Number|NumberLiteral) != 0 && kt.Flags&Union == 0 {
				return c.narrowedMember(e, obj.NumberIndex)
			}
			if obj.StringIndex != nil && (isStringLike(kt) || kt.Flags&(Number|NumberLiteral) != 0) {
				return c.narrowedMember(e, obj.StringIndex)
			}
			// An any key reads the string index, else the number one.
			if kt.Flags&Any != 0 && !c.Unanswered(kt) {
				if obj.StringIndex != nil {
					return c.narrowedMember(e, obj.StringIndex)
				}
				if obj.NumberIndex != nil {
					return c.narrowedMember(e, obj.NumberIndex)
				}
			}
			// No signature and no such property: an implicit any (TS7053
			// under --strict, plain any in a JS file), an `any` key's too.
			if obj.StringIndex == nil && obj.NumberIndex == nil && !c.Unanswered(kt) && kt.Flags&Union == 0 && (isStringLike(kt) || kt.Flags&(Number|NumberLiteral|ESSymbol|Any) != 0 || symbolKey(kt)) {
				return c.anyT
			}
			// A string key on a number index only (a Buffer's
			// `buf[`read${k}`]`): no index it takes, an implicit any.
			if obj.StringIndex == nil && obj.NumberIndex != nil && !c.Unanswered(kt) && kt.Flags&Union == 0 && isStringLike(kt) {
				return c.anyT
			}
			// A symbol key on index signatures only (`Record<string, T>`):
			// no property key they take (TS2538).
			if (obj.StringIndex != nil || obj.NumberIndex != nil) && symbolKey(kt) {
				return c.anyT
			}
		}
		if obj.Flags&Union == 0 && obj.Flags&(Number|NumberLiteral|Boolean|BooleanLiteral|BigInt|BigIntLiteral|ESSymbol) != 0 {
			// A key a number, boolean, BigInt or symbol does not name
			// (`(5)[0]`): its wrapper's, an implicit any (TS7053 under
			// --strict), as for an object without the property.
			if kt := c.TypeOf(e.Index); !c.Unanswered(kt) {
				return c.anyT
			}
		}
		if c.opts.CompatJS() && obj.Flags&Union != 0 {
			// A JavaScript value of several shapes read by a key not every
			// shape declares (`a[0]`, a: number | number[]): any, as a
			// JavaScript file's implicit any reads.
			if kt := c.TypeOf(e.Index); !c.Unanswered(kt) {
				return c.anyT
			}
		}
		return c.unanswered
	case *ast.CallExpression:
		if c.namespaceObjectRef(e.Callee) {
			// `Math()`: no call signature; tsc's error type.
			for _, a := range e.Args {
				c.TypeOf(a)
			}
			return c.anyT
		}
		// `JSON.parse(s) as Rec[]`: the parser keeps the assertion on the
		// call (AssertedType); the expression is the asserted type.
		if e.AssertedType != nil {
			if n := e.AssertedType; n != nil {
				return c.typeFromNode(n, c.b.Module)
			}
		}
		// The callee first: a call's type needs its arguments only when the
		// callee is a modelled function, and typing them for nothing can
		// walk a cycle of assignments that each read the others.
		if _, super := e.Callee.(*ast.SuperExpression); super {
			for _, a := range e.Args {
				c.TypeOf(a)
			}
			if c.opts.CompatJS() {
				// As JavaScript runs it, the `this` it initialized.
				return c.thisType(e)
			}
			return c.voidT // a super call constructs `this`; its value is void
		}
		if m, ok := e.Callee.(*ast.MemberExpression); ok && c.opts.CompatJS() {
			if ot := c.TypeOf(m.Object); !c.Unanswered(ot) && ot.Flags&^(Null|Undefined) == 0 && ot.Flags != 0 {
				// A method of null or undefined: the read throws its
				// TypeError at run time.
				for _, a := range e.Args {
					c.TypeOf(a)
				}
				return c.anyT
			}
		}
		calleeT := c.TypeOf(e.Callee)
		if t := c.unionArrayMethod(e, calleeT); t != nil {
			calleeT = t
		}
		if c.untypedCallee(calleeT) || c.untypedUnion(calleeT) {
			for _, a := range e.Args {
				c.TypeOf(a)
			}
			return c.anyT // tsc's resolveUntypedCall
		}
		if c.opts.CompatJS() && !c.Unanswered(calleeT) && calleeT.Flags&Union != 0 {
			// A hoisted `var` read before its assignment may be undefined;
			// calling it is checked at run time, so the call is the
			// function's.
			if nu := c.NonUndefined(calleeT); !c.Unanswered(nu) && nu.Flags&Union == 0 {
				calleeT = nu
			}
		}
		callee := c.signaturesOf(calleeT, false)
		if c.opts.CompatJS() && !c.Unanswered(calleeT) && calleeT.Flags&Object != 0 && calleeT.Flags&Union == 0 && calleeT.Kind != Function && len(calleeT.Calls) == 0 {
			// As JavaScript: an object without a call signature, called,
			// is checked at run time (a TypeError when not a function).
			for _, a := range e.Args {
				c.TypeOf(a)
			}
			return c.anyT
		}
		if c.Unanswered(callee) {
			return c.unanswered
		}
		for _, a := range e.Args {
			c.TypeOf(a)
		}
		if callee.Flags&Any != 0 {
			return c.anyT // calling an any is allowed and gives any
		}
		// `f.call(…)` / `f.apply(…)` under strictBindCallApply
		// (CallableFunction): f's own result.
		if r := c.boundFunction(e); r != nil {
			return r
		}
		if r := c.boundCallResult(e); r != nil {
			return r
		}
		if callee.Flags&Object != 0 && callee.Kind == Function {
			if len(callee.Overloads) > 0 {
				return c.resolveOverload(e, callee)
			}
			if len(callee.TypeParams) > 0 {
				if r := c.higherOrderResult(e, callee); r != nil {
					return r
				}
				if len(e.TypeArgs) == 0 && literalSensitive(e.Args) && c.mayHaveContext(e) {
					// tsc also infers from the call's contextual return type.
					// For a callback argument that types its parameters,
					// which is not modelled. Otherwise all it can change is
					// whether a literal argument keeps its type (`const b:
					// Box<'a' | 'b'> = box('a')`): the widened instantiation,
					// else the literal one, whichever the context takes; if
					// neither, the widened one, and the mismatch is reported.
					if hasFunctionArg(e.Args) {
						// Plain inference, where the context cannot change it:
						// none, `any`, or one its result already satisfies.
						ct := c.contextualType(e)
						r := c.instantiate(callee.Ret(), c.inferArgsRecv(callRecv(e), e.Args, e.TypeArgs, callee, true))
						if !c.Unanswered(r) && (ct == nil || ct.Flags&(Any|Unknown) != 0 || !c.Unanswered(ct) && c.assignable(r, ct)) {
							return r
						}
						// An awaited operand's context is `T | PromiseLike<T>`
						// (tsc's getContextualTypeForAwaitOperand).
						if _, awaited := c.parentOf(e).(*ast.AwaitExpression); awaited && !c.Unanswered(r) && !c.Unanswered(ct) {
							if pt := c.promiseOf(ct); pt != nil && c.assignable(r, pt) {
								return r
							}
						}
						// No callback leaves a parameter to the context: the
						// context changes nothing the arguments infer, and the
						// mismatch is reported.
						// A callback returning an object or array literal is
						// typed in the call's context, which keeps its literals
						// (`map(() => ({ k: 'a' }))` against `{ k: 'a' }[]`): a
						// mismatch only in literals is not decided here, any
						// other is reported.
						composite := returnsCompositeLiteral(e.Args)
						if composite && !c.Unanswered(r) && !c.Unanswered(ct) && c.assignable(r, c.literalErased(ct, 0)) {
							return c.unanswered
						}
						if !c.Unanswered(r) && !anyContextSensitive(e.Args) && !c.Unanswered(ct) {
							m := c.inferArgsRecv(callRecv(e), e.Args, e.TypeArgs, callee, true)
							c.inferFromReturnContext(e, callee, m)
							if r = c.instantiate(callee.Ret(), m); c.Unanswered(r) || c.assignable(r, ct) {
								return r
							}
							c.keepLiterals = true
							ml := c.inferArgsRecv(callRecv(e), e.Args, e.TypeArgs, callee, true)
							c.inferFromReturnContext(e, callee, ml)
							rl := c.instantiate(callee.Ret(), ml)
							c.keepLiterals = false
							if c.Unanswered(rl) || c.assignable(rl, ct) {
								return rl
							}
							return r
						}
						if !c.Unanswered(r) && !c.Unanswered(ct) && !composite {
							// A callback's own result infers before the
							// context, which fills only what nothing else
							// infers (tsc's return-type inference has the
							// lowest priority): a result still off the
							// context, other than in its literals, is the
							// call's, and the mismatch is reported.
							m := c.inferArgsRecv(callRecv(e), e.Args, e.TypeArgs, callee, true)
							c.inferFromReturnContext(e, callee, m)
							rc := c.instantiate(callee.Ret(), m)
							if c.Unanswered(rc) || c.assignable(rc, ct) {
								return rc
							}
							c.keepLiterals = true
							ml := c.inferArgsRecv(callRecv(e), e.Args, e.TypeArgs, callee, true)
							c.inferFromReturnContext(e, callee, ml)
							rl := c.instantiate(callee.Ret(), ml)
							c.keepLiterals = false
							if c.Unanswered(rl) || c.assignable(rl, ct) {
								return rl
							}
							if !c.generic(rc) && !c.assignable(rc, c.literalErased(ct, 0)) {
								return rc
							}
						}
						return c.unanswered
					}
					r := c.instantiate(callee.Ret(), c.inferArgsRecv(callRecv(e), e.Args, e.TypeArgs, callee, true))
					ct := c.contextualType(e)
					if ct == nil || c.Unanswered(ct) || c.Unanswered(r) || c.assignable(r, ct) {
						return r
					}
					c.keepLiterals = true
					rl := c.instantiate(callee.Ret(), c.inferArgsRecv(callRecv(e), e.Args, e.TypeArgs, callee, true))
					c.keepLiterals = false
					if !c.Unanswered(rl) && c.assignable(rl, ct) {
						return rl
					}
					return r
				}
				m := c.inferArgsRecv(callRecv(e), e.Args, e.TypeArgs, callee, true)
				if len(e.TypeArgs) == 0 {
					// A type parameter no argument infers takes its
					// candidate from the call's contextual type (`const a:
					// number[] = mk()`), as tsc's return-type inference.
					c.inferFromReturnContext(e, callee, m)
				}
				return c.instantiate(callee.Ret(), m)
			}
			return callee.Ret()
		}
		return c.unanswered
	case *ast.NewDateExpression, *ast.NewMapExpression, *ast.NewSetExpression, *ast.NewWeakMapExpression, *ast.NewWeakSetExpression,
		*ast.NewXMLHttpRequestExpression,
		*ast.NewTypedArrayExpression, *ast.NewRequestExpression,
		*ast.NewReadableStreamExpression, *ast.NewWritableStreamExpression, *ast.NewTransformStreamExpression,
		*ast.NewErrorExpression, *ast.NewCompressionStreamExpression, *ast.NewArrayExpression,
		*ast.NewArrayBufferExpression:
		// The front end's own `new Date(…)`, `new Map<K, V>(…)`, …: as a
		// `new` through the global's construct signatures.
		name, args, targs, ok := builtinNewParts(e)
		if !ok {
			return c.unanswered
		}
		return c.newBuiltinType(e, name, args, targs, e.GetPos())
	case *ast.TaggedTemplateExpression:
		// A call of the tag: its one non-generic signature's result.
		for _, x := range e.Exprs {
			c.TypeOf(x)
		}
		tag := c.TypeOf(e.Tag)
		if tag.Flags&Any != 0 {
			return c.anyT
		}
		fn := c.signaturesOf(tag, false)
		if c.Unanswered(fn) || fn.Flags&Object == 0 || fn.Kind != Function || fn.Ret() == nil {
			return c.unanswered
		}
		if len(fn.Overloads) == 0 && len(fn.TypeParams) == 0 {
			return fn.Ret()
		}
		return c.taggedTemplateResult(e, fn)
	case *ast.NewRegExpExpression:
		// `/re/flags` and `new RegExp(…)`: the RegExp interface.
		c.TypeOf(e.Pattern)
		if e.Flags != nil {
			c.TypeOf(e.Flags)
		}
		if c.b.Globals != nil {
			if sym := c.b.Globals.Symbols.Get("RegExp"); sym != nil && sym.Flags&binder.Interface != 0 {
				return c.interfaceOf(sym, nil)
			}
		}
		return c.unanswered
	case *ast.NewExpression:
		for _, a := range e.Args {
			c.TypeOf(a)
		}
		if c.namespaceObjectNew(e) {
			return c.anyT // `new Math()`: no construct signature; tsc's error type
		}
		if fe, ok := e.Callee.(*ast.FunctionExpression); ok && c.opts.CompatJS() {
			c.TypeOf(fe)
			return c.anyT // a JavaScript constructor function's instance
		}
		var sym *binder.Symbol
		qualified := c.b.QualifiedCallee(e) // `new o.C()` through a variable or namespace o
		if e.Callee == nil && qualified == nil {
			sym = c.b.NewTarget(e)
			if sym == nil {
				sym = c.builtinImport(e.ClassName)
			}
		}
		if e.Callee != nil || qualified != nil || sym != nil && sym.Flags&binder.Class == 0 && sym.Flags&binder.Variable != 0 {
			// `new X(…)` through a value whose type has construct signatures
			// (`declare var Map: MapConstructor`, `new ctors[0]()`).
			var calleeT *Type
			var callee ast.Expression
			if e.Callee != nil {
				calleeT, callee = c.TypeOf(e.Callee), e.Callee
			} else if qualified != nil {
				calleeT, callee = c.TypeOf(qualified), qualified
			} else if id := c.b.NewCallee(e); id != nil {
				calleeT, callee = c.TypeOf(id), id // the read flow narrows
			} else {
				calleeT, callee = c.typeOfSymbol(sym), ast.NewIdentifier(e.ClassName, e.GetPos())
			}
			if c.Unanswered(calleeT) {
				return calleeT
			}
			if calleeT.Flags&Any != 0 {
				return c.anyT
			}
			if c.opts.CompatJS() && calleeT.Flags&(Undefined|Null|Union) != 0 {
				// `var D; … new D()`: an undefined (or null) callee is a
				// TypeError at run time, so the construction is the rest's.
				if nn := c.nonNullable(calleeT); nn.Flags&(Undefined|Null|Never) != 0 || nn == calleeT && calleeT.Flags&Union == 0 {
					return c.anyT
				} else {
					calleeT = nn
				}
			}
			switch {
			case c.untypedCallee(calleeT):
				if c.newUntyped == nil {
					c.newUntyped = map[*ast.NewExpression]bool{}
				}
				c.newUntyped[e] = true
				return c.anyT
			case c.untypedUnion(calleeT):
				// A union with a Function member: an untyped construction,
				// TS7009 under noImplicitAny.
				if c.newNoCtor == nil {
					c.newNoCtor = map[*ast.NewExpression]bool{}
				}
				c.newNoCtor[e] = true
				return c.anyT
			}
			construct := func(ctor *Type) *Type {
				// An instance of a JavaScript class whose constructor may
				// give another object: that object, untyped.
				if r := ctor.Ret(); r != nil && r.Kind == Instance && r.Symbol != nil && r.Symbol.Flags&binder.Class != 0 && c.ctorReturnsForeign(r.Symbol) {
					return c.anyT
				}
				fake := ast.NewCallExpression(callee, e.Args, e.GetPos())
				fake.TypeArgs = e.TypeArgs
				switch {
				case len(ctor.Overloads) > 0:
					if c.ctxStandIn == nil {
						c.ctxStandIn = map[ast.Node]ast.Node{}
					}
					c.ctxStandIn[fake] = e
					defer delete(c.ctxStandIn, fake)
					return c.resolveOverload(fake, ctor)
				case len(ctor.TypeParams) > 0:
					m := c.inferArgsRecv(callRecv(e), e.Args, e.TypeArgs, ctor, true)
					if len(e.TypeArgs) == 0 {
						c.inferFromReturnContext(e, ctor, m)
					}
					return c.instantiate(ctor.Ret(), m)
				}
				return ctor.Ret()
			}
			ctor := c.signaturesOf(calleeT, true)
			if c.Unanswered(ctor) || ctor.Flags&Object == 0 || ctor.Kind != Function {
				// A union of constructors (`new ctor()`, ctor one of
				// `[Uint8Array, Int16Array]`): each one's instance, joined,
				// as tsc resolves a union's construct signatures.
				if calleeT.Flags&Union != 0 && len(calleeT.Types) > 1 {
					var ts []*Type
					for _, m := range calleeT.Types {
						mc := c.signaturesOf(m, true)
						if c.Unanswered(mc) || mc.Flags&Object == 0 || mc.Kind != Function {
							ts = nil
							break
						}
						t := construct(mc)
						if c.Unanswered(t) {
							ts = nil
							break
						}
						ts = append(ts, t)
					}
					if ts != nil {
						return c.in.union(ts...)
					}
					if c.opts.CompatJS() {
						// JavaScript: whichever of them it builds, at run
						// time (or its error).
						return c.anyT
					}
				}
				if t := c.newWithoutConstruct(e, calleeT, sym); t != nil {
					return t
				}
				return c.unanswered
			}
			return construct(ctor)
		}
		if sym != nil && sym.Flags&binder.Class == 0 && sym.Flags&binder.Function != 0 {
			// `new f()` of a declared function.
			if t := c.newWithoutConstruct(e, c.typeOfSymbol(sym), sym); t != nil {
				return t
			}
			if c.opts.CompatJS() {
				// A plain function constructs an object of no declared
				// shape in JavaScript.
				return c.anyT
			}
		}
		if sym == nil && c.opts.CompatJS() && e.Callee == nil && qualified == nil {
			return c.anyT // an undeclared constructor: a ReferenceError at run time, as an undeclared call's
		}
		if sym == nil && !c.opts.CompatJS() && e.Callee == nil && qualified == nil && c.libraryLoaded() && lib.GlobalValue(e.ClassName) {
			// A global @types/node gives a value (`new FormData()`) that
			// this compiler's declarations give only as an interface: tsc
			// constructs that interface.
			g := c.b.Globals.Symbols.Get(e.ClassName)
			if g != nil && g.Flags&binder.Value == 0 && g.Flags&binder.Interface != 0 {
				return c.interfaceOf(g, nil)
			}
			if g == nil {
				return c.anyT // undeclared here: the read throws before anything is built
			}
		}
		if sym == nil || sym.Flags&binder.Class == 0 {
			return c.unanswered
		}
		if c.ctorReturnsForeign(sym) {
			// A JavaScript constructor returning another object: the
			// expression's value is that object.
			return c.anyT
		}
		ctor := c.constructorType(sym)
		if ctor == nil {
			return c.unanswered
		}
		if len(ctor.TypeParams) == 0 {
			return ctor.Ret()
		}
		m := c.inferArgsRecv(callRecv(e), e.Args, e.TypeArgs, ctor, true)
		// A parameter no argument names: the contextual type's argument
		// (`const s: Stack<number> = new Stack()`), else its default, else
		// unknown, as tsc infers.
		var ctxArgs []*Type
		if len(e.TypeArgs) == 0 {
			if ct := c.contextualType(e); ct != nil && !c.Unanswered(ct) && ct.Kind == Instance && ct.Symbol == sym && len(ct.TypeArgs) == len(ctor.TypeParams) {
				ctxArgs = ct.TypeArgs
			}
		}
		args := make([]*Type, len(ctor.TypeParams))
		for i, tp := range ctor.TypeParams {
			if args[i] = m[tp]; (args[i] == nil || args[i].Flags&Unknown != 0 && ctxArgs != nil) && len(e.TypeArgs) == 0 {
				switch {
				case ctxArgs != nil:
					args[i] = ctxArgs[i]
				case tp.Default != nil:
					args[i] = c.instantiate(tp.Default, m)
				default:
					args[i] = c.unknownT
				}
			}
			if args[i] == nil || c.Unanswered(args[i]) {
				return c.unanswered
			}
		}
		if t := c.instanceOf(sym, args); t != nil {
			return t
		}
		return c.unanswered
	case *ast.ArrowFunction:
		return c.functionType(e, e.Params, e.RetType, e.Block, e.Body)
	case *ast.FunctionExpression:
		t := c.functionType(e, e.Params, e.RetType, e.Body, nil)
		// A named function expression its own name reads while its result
		// is inferred (`function B() { return B }`): tsc's result is any,
		// not one level of the function unrolled.
		if c.feCycled[e] && e.RetType == nil && !e.IsGenerator && !c.Unanswered(t) && t.Flags&Object != 0 && t.Kind == Function {
			res := c.anyT
			if e.IsAsync {
				res = c.promiseOf(c.anyT)
			}
			t = c.in.withResult(t, res)
		}
		return t
	case *ast.NonNullExpression:
		t := c.TypeOf(e.Arg)
		if c.Unanswered(t) {
			return t
		}
		nn := c.nonNullable(t)
		if nn.Flags&Never != 0 && t.Flags&Never == 0 {
			// A reference flow narrows to only null/undefined (`a = null;
			// a!.push(1)`) asserts its declared type (tsc's
			// getFlowTypeOfReference, under a non-null assertion).
			if id, ok := e.Arg.(*ast.Identifier); ok {
				if sym, ok := c.b.Resolve(id); ok && sym != nil {
					if d := c.typeOfSymbol(sym); !c.Unanswered(d) {
						return c.nonNullable(d)
					}
				}
			}
		}
		return c.nonNullParams(nn)
	case *ast.AsExpression:
		if n := e.TypeAnnot; n != nil {
			// The asserted type, the operand checked apart (tsc's
			// checkAssertionDeferred): inferring a result through an
			// assertion never enters its operand.
			return c.typeFromNode(n, c.annotScope(n))
		}
		c.TypeOf(e.Expr)
		return c.unanswered
	}
	return c.unanswered
}

// joinedBranch reports e a conditional's branch or a `||`/`??` operand,
// whose type joins the other side's.
func (c *Checker) joinedBranch(e ast.Expression) bool {
	switch p := c.parentOf(e).(type) {
	case *ast.ConditionalExpression:
		return p.Consequent == e || p.Alternate == e
	case *ast.BinaryExpression:
		return p.Op == "||" || p.Op == "??"
	}
	return false
}

// templateType is a template literal's type: without substitutions a
// string literal; with them, a string literal again when every span is a
// literal and the template is in a const or literal context (tsc's
// checkTemplateExpression, whose template literal type then has one value);
// otherwise string.
func (c *Checker) templateType(e *ast.TemplateLiteral) *Type {
	spans := make([]*Type, len(e.Exprs))
	for i, x := range e.Exprs {
		spans[i] = c.TypeOf(x)
	}
	if len(e.Exprs) == 0 {
		return c.in.literal(StringLiteral, strings.Join(e.Quasis, ""))
	}
	if c.constCtx == 0 && !c.constAsserted(e) && !c.literalContext(e) {
		return c.strT
	}
	var b strings.Builder
	for i, q := range e.Quasis {
		b.WriteString(q)
		if i >= len(spans) {
			continue
		}
		t := spans[i]
		switch {
		case t.Member != "" && t.Flags&NumberLiteral != 0:
			return c.strT // an enum member: `${E.A}` is not modelled
		case t.Flags&(StringLiteral|NumberLiteral|BigIntLiteral|BooleanLiteral) != 0:
			b.WriteString(t.Value)
		default:
			return c.strT
		}
	}
	return c.in.literal(StringLiteral, b.String())
}

// literalContext reports whether e's contextual type has a string literal
// member: a template in it keeps its literal type.
func (c *Checker) literalContext(e ast.Expression) bool {
	ct := c.contextualType(e)
	if ct == nil || c.Unanswered(ct) {
		return false
	}
	for _, m := range members(ct) {
		if m.Flags&StringLiteral != 0 {
			return true
		}
	}
	return false
}

// checkBinary types a binary expression by TypeScript's rules.
func (c *Checker) checkBinary(e *ast.BinaryExpression) *Type {
	if isPrivateInLeft(e) {
		c.TypeOf(e.Right) // `#x in o`: the name is the class's, not a value
		return c.boolT
	}
	l, r := c.TypeOf(e.Left), c.TypeOf(e.Right)
	if (e.Op == "??" || e.Op == "||") && !c.Unanswered(l) && c.Unanswered(r) && isEmptyArrayLiteral(e.Right) {
		// `xs ?? []`: the empty literal's never[] reduces away beside an
		// array (an empty literal against a tuple is its own case).
		rest := c.nonNullable(l)
		if e.Op == "||" {
			rest = c.truthyPart(l)
		}
		if rest.Flags&Object != 0 && rest.Flags&Union == 0 && rest.Kind == Array {
			return rest
		}
		if !c.opts.CompatJS() && rest.Flags&Never == 0 && !c.Unanswered(rest) {
			// Beside anything else the literal is its own type: `[]` where
			// the left is tuple-like (RegExpMatchArray), else never[].
			lit := c.in.array(c.neverT)
			if c.tupleContext(e.Right.(*ast.ArrayLiteral), 0) != nil {
				lit = c.in.tuple(nil)
			}
			return c.in.union(rest, lit)
		}
	}
	if (e.Op == "??" || e.Op == "||") && !c.Unanswered(l) && !c.Unanswered(r) {
		// tsc reduces the result by subtype: `opts ?? {}` is the options
		// type when the empty literal is a subtype of it.
		rest := c.nonNullable(l)
		if e.Op == "||" {
			rest = c.truthyPart(l)
		}
		if rest.Flags&Never == 0 && !c.Unanswered(rest) {
			if t := c.literalBranchCovered(e.Right, r, rest); t != nil {
				return t
			}
		}
	}
	return c.binaryResult(e.Op, l, r)
}

// binaryResult is the type of `l op r`, a binary expression's or, for
// `x op= y`, a compound assignment's.
func (c *Checker) binaryResult(op string, l, r *Type) *Type {
	switch op {
	case "==", "!=", "===", "!==", "<", ">", "<=", ">=", "instanceof", "in":
		return c.boolT
	case "&&":
		if c.Unanswered(l) || c.Unanswered(r) {
			return c.unanswered
		}
		if !canBe(l, truthyMember) {
			return l // never truthy: the right side is never the value
		}
		return c.join(c.falsyPart(l), r)
	case "||":
		if c.Unanswered(l) || c.Unanswered(r) {
			return c.unanswered
		}
		if !canBe(l, falsyMember) {
			return l // never falsy: the right side is never the value
		}
		return c.join(c.truthyPart(l), r)
	case "??":
		if c.Unanswered(l) || c.Unanswered(r) {
			return c.unanswered
		}
		if !canBe(l, func(m *Type) bool { return m.Flags&(Nullish|Void|Any|Unknown) != 0 }) {
			return l // never nullish
		}
		return c.join(c.nonNullable(l), r)
	case "+":
		switch {
		case c.Unanswered(l) || c.Unanswered(r):
			return c.unanswered
		case isStringLike(l) || isStringLike(r):
			return c.strT
		case isNumberLike(l) && isNumberLike(r):
			return c.numT
		case isBigIntLike(l) && isBigIntLike(r):
			return c.bigintT
		case l.Flags&Any != 0 || r.Flags&Any != 0:
			return c.anyT // neither side is a string: any
		}
		return c.anyT // an operand TypeScript rejects (TS2365): its error type
	case "-", "*", "/", "%", "**", "&", "|", "^", "<<", ">>", ">>>":
		switch {
		case c.Unanswered(l) || c.Unanswered(r):
			return c.unanswered
		case isBigIntLike(l) && isBigIntLike(r):
			return c.bigintT
		case l.Flags&Any != 0 && r.Flags&Any != 0:
			return c.in.anyNumber() // a bigint when both hold one
		case l.Flags&Any != 0 || r.Flags&Any != 0, isNumberLike(l) && isNumberLike(r):
			return c.numT
		}
		// An operand TypeScript rejects (an object, a string, TS2362/2363):
		// number, unless a bigint may meet a number (TS2365), whose result
		// is the error type (tsc's checkBinaryLikeExpression).
		if canBe(l, isBigIntLike) || canBe(r, isBigIntLike) {
			return c.anyT
		}
		return c.numT
	case ",":
		return r
	}
	return c.unanswered
}

// join is the union of ts, or unanswered when any is.
// dropNeverArrays is an array literal's element types without never[] when
// another array is among them: never[] is a subtype of every array, which
// tsc's subtype reduction of the element type removes (`[[], [1]]` is
// number[][]).
func dropNeverArrays(ts []*Type) []*Type {
	isNever := func(t *Type) bool {
		return t.Flags&Object != 0 && t.Kind == Array && t.Elem.Flags&Never != 0
	}
	other := false
	for _, t := range ts {
		if t.Flags&Object != 0 && (t.Kind == Array || t.Kind == Tuple) && !isNever(t) {
			other = true
		}
	}
	if !other {
		return ts
	}
	out := ts[:0:0]
	for _, t := range ts {
		if !isNever(t) {
			out = append(out, t)
		}
	}
	return out
}

func (c *Checker) join(ts ...*Type) *Type {
	for _, t := range ts {
		if c.Unanswered(t) {
			return c.unanswered
		}
	}
	return c.in.union(ts...)
}

// literalErased is t with its literal types widened, inside object and
// array types too, and a tuple as an array of its elements: what an
// argument typed without t as context could infer.
func (c *Checker) literalErased(t *Type, depth int) *Type {
	if depth > 8 || c.Unanswered(t) {
		return t
	}
	switch {
	case t.Flags&Union != 0:
		ms := make([]*Type, len(t.Types))
		for i, m := range t.Types {
			ms[i] = c.literalErased(m, depth+1)
		}
		return c.in.union(ms...)
	case t.Flags&Literal != 0:
		return widen(c, t)
	case t.Flags&Object != 0 && t.Kind == Array && t.Head != nil:
		head := make([]*Type, len(t.Head))
		for i, h := range t.Head {
			head[i] = c.literalErased(h, depth+1)
		}
		return c.in.tupleRest(head, c.literalErased(t.RestElem, depth+1))
	case t.Flags&Object != 0 && t.Kind == Array:
		return c.in.array(c.literalErased(t.Elem, depth+1))
	case t.Flags&Object != 0 && t.Kind == Tuple:
		es := make([]*Type, len(t.Elems))
		for i, el := range t.Elems {
			es[i] = c.literalErased(el, depth+1)
		}
		return c.in.array(c.in.union(es...))
	case t.Flags == Object && t.Kind == Anonymous && len(t.Calls) == 0 && len(t.Constructs) == 0:
		ps := make([]*Property, len(t.Props))
		for i, p := range t.Props {
			np := *p
			np.Type = c.literalErased(p.Type, depth+1)
			ps[i] = &np
		}
		return c.in.indexedOn(c.in.object(ps), t.StringIndex, t.NumberIndex)
	}
	return t
}

// widen gives a mutable binding's type for a literal initializer: a literal
// widens to its base type.
func widen(c *Checker, t *Type) *Type {
	switch {
	case t.Flags&Literal != 0 && t.Member != "":
		return c.enumType(t.Symbol) // an enum member widens to its enum
	case t.Flags&StringLiteral != 0:
		return c.strT
	case t.Flags&NumberLiteral != 0:
		return c.numT
	case t.Flags&BooleanLiteral != 0:
		return c.boolT
	case t.Flags&BigIntLiteral != 0:
		return c.bigintT
	case t.Flags&Union != 0:
		ms := make([]*Type, len(t.Types))
		for i, m := range t.Types {
			ms[i] = widen(c, m)
		}
		return c.in.union(ms...)
	}
	return t
}

func (c *Checker) nonNullable(t *Type) *Type {
	if t.Flags&Union == 0 {
		if t.Flags&Nullish != 0 {
			return c.neverT
		}
		return t
	}
	var keep []*Type
	for _, m := range t.Types {
		if m.Flags&Nullish == 0 {
			keep = append(keep, m)
		}
	}
	return c.in.union(keep...)
}

// canBe reports whether some member of t satisfies can.
func canBe(t *Type, can func(*Type) bool) bool {
	for _, m := range members(t) {
		if can(m) {
			return true
		}
	}
	return false
}

// falsyMember reports whether a value of type m may be falsy.
func falsyMember(m *Type) bool {
	switch {
	case m.Flags&(Nullish|Void|Any|Unknown|Boolean|String|Number|BigInt|Int|Float32) != 0:
		return true
	case m.Flags&BooleanLiteral != 0:
		return m.Value == "false"
	case m.Flags&StringLiteral != 0:
		return m.Value == ""
	case m.Flags&NumberLiteral != 0:
		return m.Value == "0" || m.Value == "NaN"
	case m.Flags&BigIntLiteral != 0:
		return m.Value == "0"
	case m.Flags&TypeParam != 0:
		return true // its argument may be anything
	case m.Flags&Object != 0 && m.Kind == Anonymous && len(m.Props) == 0 && len(m.Calls) == 0 && len(m.Constructs) == 0:
		return true // `{}` holds "" and 0 too
	}
	return false // an object is always truthy
}

// truthyMember reports whether a value of type m may be truthy.
func truthyMember(m *Type) bool {
	switch {
	case m.Flags&(Nullish|Void) != 0:
		return false
	case m.Flags&BooleanLiteral != 0:
		return m.Value == "true"
	case m.Flags&StringLiteral != 0:
		return m.Value != ""
	case m.Flags&NumberLiteral != 0:
		return m.Value != "0" && m.Value != "NaN"
	case m.Flags&BigIntLiteral != 0:
		return m.Value != "0"
	}
	return true
}

// falsyPart and truthyPart approximate the halves of a union an operand of
// && and || can pass on.
func (c *Checker) falsyPart(t *Type) *Type {
	var keep []*Type
	for _, m := range members(t) {
		switch {
		case m.Flags&(Nullish|Boolean|String|Number|BigInt|Any|Unknown) != 0:
			keep = append(keep, c.falsyOf(m))
		case m == c.falseT, m.Flags&StringLiteral != 0 && m.Value == "", m.Flags&NumberLiteral != 0 && m.Value == "0":
			keep = append(keep, m)
		}
	}
	return c.in.union(keep...)
}

func (c *Checker) falsyOf(t *Type) *Type {
	switch {
	case t.Flags&Boolean != 0:
		return c.falseT
	case t.Flags&String != 0:
		return c.in.literal(StringLiteral, "")
	case t.Flags&Number != 0:
		return c.in.literal(NumberLiteral, "0")
	case t.Flags&BigInt != 0:
		return c.in.literal(BigIntLiteral, "0")
	}
	return t
}

// nonNullParams is t with each type parameter member whose constraint
// admits null or undefined (`T extends string | undefined`) as `T & K`, K
// its constraint without them: tsc's NonNullable<T>, `T & {}`.
func (c *Checker) nonNullParams(t *Type) *Type {
	changed := false
	var out []*Type
	for _, m := range members(t) {
		if m.Flags&TypeParam != 0 && m.Flags&Union == 0 && m != c.thisT && m.Symbol != nil {
			if b := c.baseConstraint(m, 0); b != nil && !c.Unanswered(b) && b.Flags&Union != 0 && someMember(b, Nullish) {
				if k := c.nonNullable(b); k.Flags&Never == 0 {
					out = append(out, c.in.narrowedParam(m, k))
					changed = true
					continue
				}
			}
		}
		out = append(out, m)
	}
	if !changed {
		return t
	}
	return c.in.union(out...)
}

func (c *Checker) truthyPart(t *Type) *Type {
	var keep []*Type
	for _, m := range members(t) {
		switch {
		case m.narrowOf != nil && m.Constraint != nil:
			// `T & (object | null)`: of its constraint, the truthy part.
			if k := c.truthyPart(m.Constraint); k.Flags&Never == 0 {
				keep = append(keep, c.in.narrowedParam(m, k))
			}
		case m.Flags&Nullish != 0, m == c.falseT:
		case m.Flags&StringLiteral != 0 && m.Value == "", m.Flags&NumberLiteral != 0 && m.Value == "0":
		case m.Flags&Boolean != 0:
			keep = append(keep, c.trueT)
		default:
			keep = append(keep, m)
		}
	}
	return c.in.union(keep...)
}

func members(t *Type) []*Type {
	if t.Flags&Union != 0 {
		return t.Types
	}
	return []*Type{t}
}

func isStringLike(t *Type) bool { return allMembers(t, StringLike) }
func isNumberLike(t *Type) bool { return allMembers(t, NumberLike) }
func isBigIntLike(t *Type) bool { return allMembers(t, BigIntLike) }

func allMembers(t *Type, f TypeFlags) bool {
	for _, m := range members(t) {
		if m.Flags&f == 0 {
			return false
		}
	}
	return true
}

// canonicalNumber renders a numeric literal's value as TypeScript prints its
// literal type (`0x10` is 16, `1.50` is 1.5).
func canonicalNumber(raw string) string {
	return ast.NumberKey(raw)
}

// functionType is the type of a function expression or arrow fn; its
// annotations resolve in its own scope.
//
// A function expression's return type that meets a binding still being
// typed (`const o = { m() {…}, n: function () { return o.m(); } }`) is
// deferred: TypeScript resolves a signature's return type when it is asked
// for, after the binding is typed.
func (c *Checker) functionType(fn ast.Node, params []ast.Param, ret ast.TypeNode, body *ast.BlockStatement, exprBody ast.Expression) *Type {
	mark := c.reentries
	t := c.signatureType(fn, params, ret, body, exprBody, c.b.Module)
	if !c.Unanswered(t) || c.reentries == mark || ret != nil || c.skipReturn != nil {
		return t
	}
	switch f := fn.(type) {
	case *ast.FunctionExpression:
		if f.IsAsync || f.IsGenerator {
			return t
		}
	case *ast.ArrowFunction:
		if f.IsAsync {
			return t
		}
	default:
		return t
	}
	c.skipReturn = fn
	shape := c.signatureType(fn, params, ret, body, exprBody, c.b.Module)
	c.skipReturn = nil
	if c.Unanswered(shape) || shape.Flags&Object == 0 || shape.Kind != Function {
		return t
	}
	return c.in.deferred(shape, func() (*Type, bool) {
		if c.deferredRets[fn] {
			// Asked again while this function's result is computed, through
			// another instance of its type: the cycle's any.
			c.reportArrowReturnCycle(fn)
			return c.anyT, true
		}
		if c.deferredRets == nil {
			c.deferredRets = map[ast.Node]bool{}
		}
		c.deferredRets[fn] = true
		defer delete(c.deferredRets, fn)
		full := c.signatureType(fn, params, ret, body, exprBody, c.b.Module)
		if c.Unanswered(full) {
			return c.unanswered, false
		}
		return full.Ret(), true
	}, func() *Type {
		c.reportArrowReturnCycle(fn)
		return c.anyT
	})
}

// reportArrowReturnCycle reports TS7023 for a function expression or arrow
// bound to a const whose result is asked for inside its own return
// (`const g = () => g()`).
func (c *Checker) reportArrowReturnCycle(fn ast.Node) {
	if c.opts.CompatJS() || c.ImplicitAnyVariables || c.implicitAnyReported[fn] {
		return
	}
	v, ok := c.parentOf(fn).(*ast.VarDeclaration)
	if !ok || v.Init != ast.Expression(fn.(ast.Expression)) {
		return
	}
	c.implicitAnyReported[fn] = true
	pos := v.GetPos()
	if v.NamePos.Line > 0 {
		pos = v.NamePos
	}
	c.report(diag.ReturnImplicitAnyCycle, pos, sourceName(v.Name))
}

// withThis is a member's type t read through a receiver of type recv: the
// polymorphic `this` in it is the receiver (TypeScript instantiates `this`
// per access).
func (c *Checker) withThis(t, recv *Type) *Type {
	if !occurs(c.thisT, t) {
		return t
	}
	return c.instantiate(t, map[*Type]*Type{c.thisT: recv})
}

// unionArrayReceiver is the array type a method call on a union of array
// types ms reads name from (`Array<A | B>` for `A[] | B[]`), or nil when ms
// is not two or more arrays or name is no method of Array.
func (c *Checker) unionArrayReceiver(ms []*Type, name string) *Type {
	if len(ms) < 2 {
		return nil
	}
	var elems []*Type
	for _, m := range ms {
		switch {
		case m.Flags&Object == 0 || m.Flags&Union != 0:
			return nil
		case m.Kind == Array:
			elems = append(elems, m.Elem)
		case m.Kind == Tuple:
			if len(m.Elems) == 0 {
				elems = append(elems, c.neverT)
			} else {
				elems = append(elems, m.Elems...)
			}
		case m.Kind == Interface && m.Prop("map") != nil && m.Prop("length") != nil:
			// An interface extending Array (`RegExpMatchArray`).
			it := c.iteratedType(m)
			if c.Unanswered(it) || it == c.anyT {
				return nil
			}
			elems = append(elems, it)
		default:
			return nil
		}
	}
	at := c.in.array(c.in.union(elems...))
	ap := c.apparentType(at)
	if ap == nil {
		return nil
	}
	if p := ap.Prop(name); p == nil || p.Type.Flags&Object == 0 || p.Type.Kind != Function {
		return nil
	}
	return at
}

// tupleContext is the tuple type of n elements an array literal is
// expected to be, when its context (null and undefined aside) is one.
// tupleCtxMembers is t's members as a tuple context reads them: a
// conditional type not yet decided as its branches (`V<T>` = `T extends M
// ? … : { [K in keyof T]: … }`), a bare type parameter among them left out
// (it decides neither way).
func (c *Checker) tupleCtxMembers(t *Type, depth int) []*Type {
	var out []*Type
	for _, m := range members(t) {
		if m.Flags&Deferred == 0 || depth > 3 {
			out = append(out, m)
			continue
		}
		br := c.deferredBranches(m)
		if br == nil {
			out = append(out, m)
			continue
		}
		for _, b := range br {
			for _, bm := range c.tupleCtxMembers(b, depth+1) {
				if bm.Flags&TypeParam == 0 {
					out = append(out, bm)
				}
			}
		}
	}
	return out
}

func (c *Checker) tupleContext(e *ast.ArrayLiteral, n int) *Type {
	if c.tupleCtx[e] {
		return nil
	}
	c.tupleCtx[e] = true
	ct := c.contextualType(e)
	// An argument for a homomorphic mapped parameter (`x: { [K in keyof
	// A]: … }`) is a tuple whatever A is inferred to be: tsc decides by
	// the parameter, A not yet fixed.
	if raw := c.rawContext(e); raw != nil && !c.Unanswered(raw) {
		for _, m := range c.tupleCtxMembers(raw, 0) {
			if m.Flags&Deferred != 0 && (c.homomorphicMapped(m) || isTupleNode(m.deferNode)) {
				ct = raw
				break
			}
		}
	}
	delete(c.tupleCtx, e)
	if ct == nil || c.Unanswered(ct) {
		return nil
	}
	if ct.Flags&TypeParam != 0 && ct.Constraint != nil {
		// A type parameter constrained by a tuple type (`T extends
		// readonly unknown[] | []`) infers a tuple, as TypeScript does.
		for _, m := range members(ct.Constraint) {
			if m.Flags&Object != 0 && m.Kind == Tuple {
				es := make([]*Type, n)
				for i := range es {
					es[i] = c.unknownT
				}
				return c.in.tuple(es)
			}
		}
		return nil
	}
	var tuple *Type
	for _, m := range c.tupleCtxMembers(ct, 0) {
		switch {
		case m.Flags&(Null|Undefined) != 0 || m == c.missingT:
		case m.Flags&(String|Number|Boolean|BigInt|StringLiteral|NumberLiteral|BooleanLiteral|BigIntLiteral|ESSymbol) != 0:
			// A primitive member no array literal can be.
		case m.Flags&Object != 0 && m.Kind == Tuple && tuple == nil:
			tuple = m
		case m.Flags&Object != 0 && m.Kind == Array && m.Head != nil && tuple == nil && n >= len(m.Head):
			// A rest tuple: the literal is a tuple of its own elements,
			// each expected as the position it fills.
			es := make([]*Type, n)
			for i := range es {
				es[i] = m.elemAt(i)
			}
			tuple = c.in.tuple(es)
		case m.Flags&Deferred != 0 && tuple == nil && isTupleNode(m.deferNode):
			// A variadic tuple type (`[...A]`): the literal is a tuple of
			// its own elements (tsc's isTupleLikeType).
			es := make([]*Type, n)
			for i := range es {
				es[i] = c.unknownT
			}
			tuple = c.in.tuple(es)
		case m.Flags&Deferred != 0 && tuple == nil && c.homomorphicMapped(m):
			// A mapped type over a type parameter's keys (tsc's
			// inTupleContext): a tuple, each element expected as the
			// template at its index.
			es := make([]*Type, n)
			for i := range es {
				if es[i] = c.deferredMappedTemplate(m, strconv.Itoa(i)); es[i] == nil {
					es[i] = c.unknownT
				}
			}
			tuple = c.in.tuple(es)
		case m.Flags&Object != 0 && m.Kind == Interface && m.Prop("0") != nil && tuple == nil:
			// A tuple-like interface (`RegExpMatchArray`'s `0: string`):
			// the literal is a tuple of its own elements (isTupleLikeType).
			es := make([]*Type, n)
			for i := range es {
				es[i] = c.unknownT
			}
			tuple = c.in.tuple(es)
		default:
			return nil
		}
	}
	if tuple == nil || n < tuple.minLen() || n > len(tuple.Elems) {
		return nil
	}
	return tuple
}

// apparentType is the builtin interface whose members a primitive or an
// array has (TypeScript's apparent type): String for a string, Number for a
// number, Boolean for a boolean, Object for `object`, Array<T> for an array
// or tuple; nil for any
// other type, or when the builtin declarations do not declare it.
func (c *Checker) apparentType(t *Type) *Type {
	if c.b.Globals == nil || t.Flags&Union != 0 {
		return nil
	}
	name := ""
	var args []*Type
	switch {
	case t.Flags&StringLike != 0:
		name = "String"
	case t.Flags&NumberLike != 0:
		name = "Number"
	case t.Flags&BooleanLike != 0:
		name = "Boolean"
	case t.Flags&ESSymbol != 0:
		name = "Symbol"
	case t.Flags&BigIntLike != 0:
		name = "BigInt"
	case t.Flags&NonPrimitive != 0:
		name = "Object" // `object`: Object's members (`constructor`, `toString`)
	case t.Flags&Object != 0 && t.Kind == Array:
		name, args = "Array", []*Type{t.Elem}
	case t.Flags&Object != 0 && t.Kind == Tuple && len(t.Elems) > 0:
		name, args = "Array", []*Type{c.in.union(t.Elems...)}
	default:
		return nil
	}
	sym := c.b.Globals.Symbols.Get(name)
	if sym == nil || sym.Flags&binder.Interface == 0 {
		return nil
	}
	at := c.interfaceOf(sym, args)
	if c.Unanswered(at) {
		return nil
	}
	return at
}

// signaturesOf is the function type a callee of type t is called through:
// t itself for a function type, or an object type's call signatures
// (construct signatures, for `new`) as one function or an overload list;
// unanswered when it has none.
func (c *Checker) signaturesOf(t *Type, construct bool) *Type {
	if t != nil && t.Flags&Deferred != 0 {
		// A deferred conditional (`K extends "a" ? () => number : never`)
		// is called through its constraint, the union of its branches
		// (tsc's getConstraintOfConditionalType).
		if bs := c.deferredBranches(t); len(bs) > 0 {
			return c.signaturesOf(c.in.union(bs...), construct)
		}
	}
	if !c.Unanswered(t) && t.Flags&Union != 0 {
		if u := c.unionSigOf(t, construct); u != nil {
			return u
		}
		if construct {
			return c.unanswered
		}
		return t
	}
	if c.Unanswered(t) || t.Flags&Object == 0 || t.Flags&Union != 0 {
		return t
	}
	if t.Kind == Function && !construct {
		return t
	}
	sigs := reorderCandidates(t.Calls, t.callOrigins)
	if construct {
		sigs = reorderCandidates(t.Constructs, t.constructOrigins)
	}
	switch len(sigs) {
	case 0:
		if construct {
			return c.unanswered
		}
		return t
	case 1:
		return sigs[0]
	}
	return c.in.overloaded(sigs)
}

// unionSignature is the signature a union of function types is called
// through, as TypeScript resolves one (getUnionSignatures over members with
// one plain signature each): identical parameter lists take those
// parameters; others combine as combineUnionParameters does, the longer
// list kept, each position's types intersected, a position required when
// either side requires it, and a rest element when either has one. The
// result is the union of the results. Nil for any other union.
func (c *Checker) unionSignature(t *Type) *Type { return c.unionSigOf(t, false) }

// unionSigOf is unionSignature over call or construct signatures.
func (c *Checker) unionSigOf(t *Type, construct bool) *Type {
	var acc *flatSig
	var results []*Type
	// Generic members with as many type parameters each read the first's
	// (tsc's combineSignaturesOfUnionMembers maps the others' onto them).
	var tps []*Type
	for i, m := range t.Types {
		if m.Flags&Object == 0 {
			return nil
		}
		s := c.signaturesOf(m, construct)
		if c.Unanswered(s) || s.Kind != Function || len(s.Overloads) > 0 || s.Predicate != nil || s.Ret() == nil || s.ThisType != nil {
			return nil
		}
		switch {
		case i == 0:
			tps = s.TypeParams
		case len(s.TypeParams) == 0:
			// A plain member beside generic ones: the generic's
			// parameters stand (tsc keeps whichever side has them).
		case len(tps) == 0:
			tps = s.TypeParams
		case len(s.TypeParams) != len(tps):
			return nil
		case len(tps) > 0:
			m := make(map[*Type]*Type, len(tps))
			for j, tp := range s.TypeParams {
				m[tp] = tps[j]
			}
			s = c.instantiate(s, m)
		}
		f, ok := flatten(s)
		if !ok {
			return nil
		}
		if acc == nil {
			acc = f
		} else if acc, ok = c.combineSigs(acc, f); !ok {
			return nil
		}
		results = append(results, s.Ret())
	}
	if acc == nil {
		return nil
	}
	params := append([]*Type(nil), acc.params...)
	opts := make([]bool, len(params))
	for i := range opts {
		opts[i] = i >= acc.min
	}
	rest := acc.rest != nil
	if rest {
		params = append(params, c.in.array(acc.rest))
		opts = append(opts, false)
	}
	return c.in.function(params, opts, rest, c.in.union(results...), nil, tps)
}

// flatSig is a signature's parameters as positions: the fixed ones, how
// many are required, and the rest element (nil without a rest).
type flatSig struct {
	params []*Type
	min    int
	rest   *Type
}

// flatten is s's parameter positions, a tuple rest spread into them.
func flatten(s *Type) (*flatSig, bool) {
	f := &flatSig{}
	n := len(s.Params)
	fixed := n
	if s.restParam {
		fixed = n - 1
	}
	f.min = fixed
	for i := 0; i < fixed; i++ {
		f.params = append(f.params, s.Params[i])
		if i < len(s.optionals) && s.optionals[i] && f.min == fixed {
			f.min = i
		}
	}
	if s.restParam {
		r := s.Params[n-1]
		switch {
		case r.Flags&Object != 0 && r.Kind == Array:
			f.rest = r.Elem
		case r.Flags&Object != 0 && r.Kind == Tuple && len(r.Labels) >= 0:
			if f.min == fixed {
				f.min = fixed + r.minLen()
			}
			f.params = append(f.params, r.Elems...)
		default:
			return nil, false
		}
	}
	return f, true
}

// combineSigs is combineUnionParameters over two flattened signatures.
func (c *Checker) combineSigs(a, b *flatSig) (*flatSig, bool) {
	at := func(f *flatSig, i int) *Type {
		if i < len(f.params) {
			return f.params[i]
		}
		return f.rest
	}
	n := len(a.params)
	if len(b.params) > n {
		n = len(b.params)
	}
	out := &flatSig{min: a.min}
	if b.min > out.min {
		out.min = b.min
	}
	for i := 0; i < n; i++ {
		x, ok := c.intersectParam(at(a, i), at(b, i))
		if !ok {
			return nil, false
		}
		out.params = append(out.params, x)
	}
	// A rest stays when the side of more parameters (a rest counting as
	// one) has it, on a tie when both do (tsc's combineUnionParameters,
	// as its arity errors show).
	count := func(f *flatSig) int {
		if f.rest != nil {
			return len(f.params) + 1
		}
		return len(f.params)
	}
	ca, cb := count(a), count(b)
	if ca > cb && a.rest != nil || cb > ca && b.rest != nil || ca == cb && a.rest != nil && b.rest != nil {
		x, ok := c.intersectParam(a.rest, b.rest)
		if !ok {
			return nil, false
		}
		out.rest = x
	}
	return out, true
}

// intersectParam is the intersection of two parameter types at one
// position, nil standing for a side without one (unknown): identical types,
// or primitives and their unions, whose members each other takes. Any other
// pair is not modelled.
func (c *Checker) intersectParam(x, y *Type) (*Type, bool) {
	switch {
	case x == nil:
		return y, true
	case y == nil || x == y:
		return x, true
	}
	prim := func(t *Type) bool {
		for _, m := range members(t) {
			if m.Flags&Object != 0 || m.Flags&(TypeParam|Deferred|Any|Unknown) != 0 || c.Unanswered(m) {
				return false
			}
		}
		return true
	}
	if !prim(x) || !prim(y) {
		// Two function types: their overload list.
		if r := c.functionIntersection([]*Type{x, y}); r != nil {
			return r, true
		}
		// Two object types: their intersection (`{ x: string } & { y:
		// string }`), when it reduces.
		if x.Flags&Object != 0 && y.Flags&Object != 0 && x.Flags&Union == 0 && y.Flags&Union == 0 && !c.generic(x) && !c.generic(y) {
			if r := c.intersectTypes([]*Type{x, y}); !c.Unanswered(r) {
				return r, true
			}
		}
		return nil, false
	}
	var keep []*Type
	for _, m := range members(x) {
		if c.assignableTo(m, y) == yes {
			keep = append(keep, m)
		}
	}
	for _, m := range members(y) {
		if c.assignableTo(m, x) == yes {
			keep = append(keep, m)
		}
	}
	if len(keep) == 0 {
		return c.neverT, true
	}
	return c.in.union(keep...), true
}

// builtinNamespace reports whether x names a builtin global namespace
// (`Reflect`, or globalThis itself), not a program's own.
// IsGlobalThis reports the global object's type.
func (c *Checker) IsGlobalThis(t *Type) bool { return t == c.globalThisT }

func (c *Checker) builtinNamespace(x ast.Expression) bool {
	id, ok := x.(*ast.Identifier)
	if !ok {
		return false
	}
	if id.Name == "globalThis" {
		return true
	}
	sym, _ := c.b.Resolve(id)
	return sym != nil && c.b.Globals != nil && sym.Scope == c.b.Globals
}

// MemberDecl is the declaration a property access resolves to (a method
// signature of a builtin declaration, …), or nil when the checker does not
// know one.
func (c *Checker) MemberDecl(e *ast.MemberExpression) ast.Node {
	c.TypeOf(e)
	if p := c.memberDecls[e]; p != nil {
		return p.Decl
	}
	return nil
}

// MemberInLibrary reports a property access resolving to a member the
// builtin library declares.
func (c *Checker) MemberInLibrary(e *ast.MemberExpression) bool {
	c.TypeOf(e)
	p := c.memberDecls[e]
	if p == nil {
		return false
	}
	if p.Owner != nil {
		return c.inLibrary(p.Owner.Scope)
	}
	// An interface's member: the interface the receiver is; an array's or
	// tuple's, the library's Array.
	r := c.nonNullable(c.TypeOf(e.Object))
	if r.Flags&Object != 0 && (r.Kind == Array || r.Kind == Tuple) {
		return true
	}
	return r.Symbol != nil && c.inLibrary(r.Symbol.Scope)
}

// MemberOverloadDecls are an overloaded method's declarations, one per
// overload in the order of the member type's Overloads, or nil.
func (c *Checker) MemberOverloadDecls(e *ast.MemberExpression) []ast.Node {
	c.TypeOf(e)
	if p := c.memberDecls[e]; p != nil && len(p.Decls) > 1 {
		return p.Decls
	}
	return nil
}

// OverloadResult is the result type every overload of fn that a call may
// resolve to agrees on, taking an argument the checker cannot type as
// fitting any parameter; nil when they differ or none fits.
// AnyArgOverloadCall reports whether e calls an overloaded function with an
// argument typed any or unknown where the overloads that fit disagree on the
// result: the overload tsc picks is then the first that fits, not one the
// run-time value is known to take.
func (c *Checker) AnyArgOverloadCall(e *ast.CallExpression) bool {
	fn := c.TypeOf(e.Callee)
	if c.Unanswered(fn) || fn.Kind != Function || len(fn.Overloads) == 0 {
		return false
	}
	for _, a := range e.Args {
		if _, spread := a.(*ast.SpreadElement); spread {
			continue
		}
		if t := c.TypeOf(a); !c.Unanswered(t) && t.Flags&(Any|Unknown) != 0 {
			return c.OverloadResult(e, fn) == nil
		}
	}
	return false
}

// literalKeyUnionProps is the property types a union of string or number
// literal keys reads from obj, each a declared property; ok is false when
// one is not.
func (c *Checker) literalKeyUnionProps(obj, kt *Type) ([]*Type, bool) {
	if c.Unanswered(kt) || kt.Flags&Union == 0 {
		return nil, false
	}
	var ts []*Type
	for _, m := range kt.Types {
		if m.Flags&(StringLiteral|NumberLiteral) == 0 || m.Flags&Union != 0 {
			return nil, false
		}
		p := obj.Prop(m.Value)
		if p == nil || p.Accessor && p.Type == nil {
			return nil, false
		}
		ts = append(ts, p.Type)
	}
	return ts, len(ts) > 0
}

// taggedTemplateResult is the result of tag call e through an overloaded
// or generic tag fn: the first signature its strings array and
// substitutions fit (tsc's resolveCall over the template's arguments), a
// generic one instantiated by e's type arguments or by what the
// substitutions infer.
func (c *Checker) taggedTemplateResult(e *ast.TaggedTemplateExpression, fn *Type) *Type {
	var strs *Type
	if c.b.Globals != nil {
		if sym := c.b.Globals.Symbols.Get("TemplateStringsArray"); sym != nil && sym.Flags&binder.Interface != 0 {
			strs = c.interfaceOf(sym, nil)
		}
	}
	if strs == nil || c.Unanswered(strs) {
		return c.unanswered
	}
	args := []*Type{strs}
	for _, x := range e.Exprs {
		t := c.TypeOf(x)
		if c.Unanswered(t) {
			return c.unanswered
		}
		args = append(args, t)
	}
	sigs := fn.Overloads
	if len(sigs) == 0 {
		sigs = []*Type{fn}
	}
	for _, sig := range sigs {
		if !arityFits(sig, len(args)) {
			continue
		}
		if len(sig.TypeParams) > 0 {
			m := map[*Type]*Type{}
			if len(e.TypeArgs) > 0 {
				if len(e.TypeArgs) != len(sig.TypeParams) {
					continue
				}
				for i, tp := range sig.TypeParams {
					scope := c.b.TypeArgScope(e.TypeArgs[i])
					if scope == nil {
						scope = c.b.Module
					}
					ta := c.typeFromNode(e.TypeArgs[i], scope)
					if c.Unanswered(ta) {
						return c.unanswered
					}
					m[tp] = ta
				}
			} else {
				cands := map[*Type][]*Type{}
				for i, a := range args {
					if p := paramAt(sig, i); p != nil {
						c.unify(p, a, sig, cands)
					}
				}
				for _, tp := range sig.TypeParams {
					if cs := cands[tp]; len(cs) > 0 {
						m[tp] = c.in.union(cs...)
						if !c.keepsLiterals(tp, sig) {
							m[tp] = widen(c, m[tp])
						}
					} else {
						m[tp] = c.unknownT
					}
				}
			}
			inst := c.instantiate(sig, m)
			if c.Unanswered(inst) || inst.Flags&Object == 0 || inst.Kind != Function {
				return c.unanswered
			}
			sig = inst
		} else if len(e.TypeArgs) > 0 {
			continue // type arguments select a generic signature only
		}
		fits := true
		for i, a := range args {
			p := paramAt(sig, i)
			if p == nil {
				fits = false
				break
			}
			if r := c.assignableTo(a, p); r == no {
				fits = false
				break
			} else if r != yes {
				return c.unanswered
			}
		}
		if fits {
			return sig.Ret()
		}
	}
	return c.unanswered
}

func (c *Checker) OverloadResult(e *ast.CallExpression, fn *Type) *Type {
	var result *Type
	for _, sig := range fn.Overloads {
		if !arityFits(sig, len(e.Args)) || len(sig.TypeParams) > 0 {
			continue
		}
		fits := true
		for j, a := range e.Args {
			if contextSensitive(a) {
				continue
			}
			t := c.TypeOf(a)
			if c.Unanswered(t) || t.Flags&(Any|Unknown) != 0 {
				continue
			}
			if p := paramAt(sig, j); p == nil || !c.assignable(t, p) {
				fits = false
				break
			}
		}
		if !fits {
			continue
		}
		if result != nil && result != sig.Ret() {
			return nil
		}
		result = sig.Ret()
	}
	return result
}

// OverloadIndex is the overload of fn a call resolves to (the first its
// arguments fit, as TypeScript picks it), or -1.
func (c *Checker) OverloadIndex(e *ast.CallExpression, fn *Type) int {
	for _, a := range e.Args {
		if contextSensitive(a) {
			continue
		}
		// An `any` fits every overload: which one runs is decided by the
		// value, not the type (`s.split(x)` with x a RegExp at run time).
		if t := c.TypeOf(a); c.Unanswered(t) || t.Flags&(Any|Unknown) != 0 {
			return -1
		}
	}
	for i, sig := range fn.Overloads {
		if !arityFits(sig, len(e.Args)) {
			continue
		}
		if len(sig.TypeParams) > 0 {
			return -1 // inference would decide: not answered here
		}
		fits := true
		for j, a := range e.Args {
			if contextSensitive(a) {
				continue
			}
			if p := paramAt(sig, j); p == nil || !c.assignable(c.TypeOf(a), p) {
				fits = false
				break
			}
		}
		if fits {
			return i
		}
	}
	return -1
}

// Binding is the binding the checker reads.
func (c *Checker) Binding() *binder.Binding { return c.b }

// NonUndefined is t without undefined: an optional parameter's declared type.
func (c *Checker) NonUndefined(t *Type) *Type {
	return c.filter(t, func(m *Type) bool { return m.Flags&Undefined == 0 })
}

// MaybeUndefined reports whether a value of type t may be undefined.
func (c *Checker) MaybeUndefined(t *Type) bool {
	for _, m := range members(t) {
		if m.Flags&(Undefined|Void|Any|Unknown) != 0 {
			return true
		}
	}
	return false
}

// PrimitiveMember is the member of a string's (str) or number's apparent
// type (String, Number) named name, with its declarations: a builtin method
// read off a value known to be one where the checker cannot type the value
// itself.
func (c *Checker) PrimitiveMember(str bool, name string) (*Type, ast.Node, []ast.Node) {
	prim := c.numT
	if str {
		prim = c.strT
	}
	at := c.apparentType(prim)
	if at == nil {
		return nil, nil, nil
	}
	p := at.Prop(name)
	if p == nil {
		return nil, nil, nil
	}
	var decls []ast.Node
	if len(p.Decls) > 1 {
		decls = p.Decls
	}
	return c.withThis(p.Type, prim), p.Decl, decls
}

// NonNullable is t without null and undefined.
func (c *Checker) NonNullable(t *Type) *Type { return c.nonNullable(t) }

// cannotFind reports TS2304 for a name nothing declares: not the program,
// not the builtin declarations, and not TypeScript's library or Node's
// declarations either — a name tsc cannot resolve whichever libraries a
// program is checked with. The -compat=js lane checks JavaScript, where an
// unbound name is a run-time ReferenceError, not a type error.
func (c *Checker) cannotFind(name string, pos ast.Pos) {
	name, ok := c.unresolvable(name)
	if !ok {
		return
	}
	key := cannotFindKey{name, pos}
	if c.cannotFound[key] {
		return
	}
	c.cannotFound[key] = true
	c.report(diag.CannotFindName, pos, name)
}

// unresolvable is the source spelling of a name cannotFind reports, or
// false when the name is bound where the merged program no longer shows it.
func (c *Checker) unresolvable(name string) (string, bool) {
	if c.opts.CompatJS() || strings.HasSuffix(name, "_kml_builtin") || strings.HasPrefix(name, "__kml") {
		return "", false // a builtin module's marker, or compiler-internal
	}
	name = sourceName(name) // a file's own top-level name, mangled by the resolver
	if intrinsicNames[name] || lib.KnownGlobal(name) || c.b.Program.BuiltinImports[name] || c.nsAlias(name) || c.ambientName(name) {
		return "", false // bound where the merged program no longer shows it
	}
	return name, true
}

// intrinsicNames are the global names TypeScript knows without declaring
// them in any library file.
var intrinsicNames = map[string]bool{"globalThis": true, "arguments": true, "undefined": true}

type cannotFindKey struct {
	name string
	pos  ast.Pos
}

// builtinImport resolves a name an import from a builtin module binds
// (`import { EventEmitter } from 'events'`, whose statement the merged
// program drops) to its export's declaration in that module's `declare
// module` block; nil when the module or the export is not declared.
// builtinValueType is the type of a value a builtin module's import binds
// under name: the module itself, or one of its members. Nil for neither.
func (c *Checker) builtinValueType(name string) *Type {
	if spec, ok := c.b.Program.BuiltinMarkers[name]; ok {
		return c.moduleType(spec)
	}
	if s := c.builtinImport(name); s != nil && s.Flags&binder.Value != 0 {
		return c.typeOfSymbol(s)
	}
	return nil
}

func (c *Checker) builtinImport(name string) *binder.Symbol {
	ref, ok := c.b.Program.BuiltinImportRefs[sourceName(name)]
	if !ok {
		return nil
	}
	scope := c.b.AmbientModules[ref.Module]
	if scope == nil {
		return nil
	}
	if eq := c.exportEquals(ref.Module); eq != nil {
		// `export = X`: the module's members are X's namespace's.
		if ref.Name == "default" {
			return eq
		}
		if ns := c.b.NamespaceScope(eq); ns != nil {
			return ns.Symbols.Get(ref.Name)
		}
		return nil
	}
	return scope.Symbols.Get(ref.Name)
}

// exportEquals is the symbol a builtin module's `export = X;` makes the
// module, or nil.
func (c *Checker) exportEquals(spec string) *binder.Symbol {
	name := c.b.AmbientExportEquals[spec]
	scope := c.b.AmbientModules[spec]
	if name == "" || scope == nil {
		return nil
	}
	return scope.Symbols.Get(name)
}

// valueWithNamespace is the type of the value sym names when a namespace
// merges into it (`function assert(…)` + `namespace assert { … }`): its
// call signatures, and the namespace's exported values as properties.
func (c *Checker) valueWithNamespace(sym *binder.Symbol) *Type {
	base := c.typeOfSymbol(sym)
	ns := c.b.NamespaceScope(sym)
	if ns == nil {
		return base
	}
	t := &Type{Flags: Object, Kind: Anonymous, partial: true, ID: -2}
	if !c.Unanswered(base) && base.Flags&Object != 0 && base.Kind == Function {
		if len(base.Overloads) > 0 {
			t.Calls = append(t.Calls, base.Overloads...)
		} else {
			t.Calls = append(t.Calls, base)
		}
	}
	ns.Symbols.Each(func(m *binder.Symbol) {
		if m.Flags&binder.Value == 0 {
			return
		}
		var decls []ast.Node
		for _, d := range m.Declarations {
			decls = append(decls, d.Node)
		}
		p := &Property{Name: m.Name, Type: c.typeOfSymbol(m), Decls: decls}
		if len(decls) > 0 {
			p.Decl = decls[len(decls)-1]
		}
		t.setProp(p)
	})
	return t
}

// AmbientName reports whether name is an erased ambient declaration's.
func (c *Checker) AmbientName(name string) bool { return c.ambientName(name) }

// ambientName reports whether name is an erased ambient declaration's.
func (c *Checker) ambientName(name string) bool {
	for _, n := range c.b.Program.AmbientNames {
		if n == name {
			return true
		}
	}
	return false
}

// nsAlias reports whether name is an import-equals alias (`import X = Y.Z`),
// which the binder does not bind.
func (c *Checker) nsAlias(name string) bool {
	for _, a := range c.b.Program.NSAliases {
		if a.Name == name {
			return true
		}
	}
	return false
}

// inLibrary reports whether scope belongs to the builtin declarations: the
// global scope they are bound into, or a scope inside it (a module's).
func (c *Checker) inLibrary(scope *binder.Scope) bool {
	for s := scope; s != nil && c.b.Globals != nil; s = s.Parent {
		switch s {
		case c.b.Module:
			return false // the program's own (its module scope sits in the globals)
		case c.b.Globals:
			return true
		}
	}
	return false
}

// importedModule is what `import("./mod")` resolves to: an object of the
// target's exports, each typed as its merged-program binding (or its
// annotation, for a target compiled separately).
func (c *Checker) importedModule(e *ast.ImportCallExpression) *Type {
	if sym := c.b.Module.Symbols.Get(e.Namespace); e.Namespace != "" && sym != nil {
		return c.typeOfSymbol(sym) // its namespace object
	}
	props := make([]*Property, 0, len(e.Exports))
	for _, x := range e.Exports {
		pt := c.anyT
		if sym := c.b.Module.Symbols.Get(x.Mangled); x.Mangled != "" && sym != nil {
			pt = c.typeOfSymbol(sym)
		} else if x.TypeAnnot != nil {
			pt = c.typeFromNode(x.TypeAnnot, c.b.Module)
		}
		props = append(props, &Property{Name: x.Name, Type: pt})
	}
	if t := c.importObjects[e]; t != nil {
		return t
	}
	t := *c.in.object(props)
	t.ImportCall = e
	if c.importObjects == nil {
		c.importObjects = map[*ast.ImportCallExpression]*Type{}
	}
	c.importObjects[e] = &t
	return &t
}

// moduleType is a builtin module's exports (`declare module "path" { … }`)
// as an object: what `import path from 'path'` binds, and what a named
// import's `path.join` reads. Partial — the declarations list what this
// compiler implements.
func (c *Checker) moduleType(spec string) *Type {
	if t, ok := c.modules[spec]; ok {
		return t
	}
	scope := c.b.AmbientModules[spec]
	if scope == nil {
		c.modules[spec] = c.unanswered
		return c.unanswered
	}
	if eq := c.exportEquals(spec); eq != nil {
		t := c.valueWithNamespace(eq)
		c.modules[spec] = t
		return t
	}
	t := &Type{Flags: Object, Kind: Anonymous, partial: true, ID: -2}
	c.modules[spec] = t
	scope.Symbols.Each(func(sym *binder.Symbol) {
		if sym.Flags&binder.Value == 0 {
			return
		}
		var decls []ast.Node
		for _, d := range sym.Declarations {
			decls = append(decls, d.Node)
		}
		p := &Property{Name: sym.Name, Type: c.typeOfSymbol(sym), Decls: decls}
		if len(decls) > 0 {
			p.Decl = decls[len(decls)-1]
		}
		t.setProp(p)
	})
	return t
}

// widenFrom is t, the type of e, with the literal types e makes fresh
// widened: tsc widens a mutable binding's literal type only when it is
// fresh, the type of a literal expression (and of what passes one on: a
// conditional, a logical operator, a const initialized with one). A literal
// type from a declaration or a type argument stays (`let h = f<"a">()` is
// "a"). An enum member always widens to its enum.
func (c *Checker) widenFrom(e ast.Expression, t *Type) *Type {
	fresh := map[*Type]bool{}
	c.freshLiterals(e, fresh, map[*binder.Symbol]bool{})
	var widenFresh func(t *Type) *Type
	widenFresh = func(t *Type) *Type {
		switch {
		case t.Flags&Union != 0:
			ms := make([]*Type, len(t.Types))
			for i, m := range t.Types {
				ms[i] = widenFresh(m)
			}
			return c.in.union(ms...)
		case t.Flags&Literal != 0 && t.Member != "", fresh[t]:
			return widen(c, t)
		}
		return t
	}
	return widenFresh(t)
}

// keepsLiteral reports whether every literal member of t, the type of e, is
// one its context keeps (literalOfContext).
func (c *Checker) keepsLiteral(e ast.Expression, t *Type) bool {
	if t.Flags&Literal == 0 && !(t.Flags&Union != 0 && someMember(t, Literal)) {
		return false
	}
	ctx := c.rawContext(e)
	if ctx == nil {
		return false
	}
	for _, m := range members(t) {
		if m.Flags&Literal != 0 && !c.literalOfContext(m, ctx) {
			return false
		}
	}
	return true
}

// freshBranches adds to out the literal types fresh in the union of the
// branches' values: a literal a branch has regular is regular in the union
// (tsc's removeRedundantLiteralTypes drops the fresh twin).
func (c *Checker) freshBranches(out map[*Type]bool, seen map[*binder.Symbol]bool, branches ...ast.Expression) {
	fresh, regular := map[*Type]bool{}, map[*Type]bool{}
	for _, b := range branches {
		bf := map[*Type]bool{}
		c.freshLiterals(b, bf, seen)
		for _, m := range members(c.TypeOf(b)) {
			if m.Flags&Literal != 0 && !bf[m] {
				regular[m] = true
			}
		}
		for m := range bf {
			fresh[m] = true
		}
	}
	for m := range fresh {
		if !regular[m] {
			out[m] = true
		}
	}
}

// freshLiterals adds to out the literal types e's value has fresh.
func (c *Checker) freshLiterals(e ast.Expression, out map[*Type]bool, seen map[*binder.Symbol]bool) {
	if e == nil || c.constAsserted(e) {
		return
	}
	addAll := func(t *Type) {
		for _, m := range members(t) {
			if m.Flags&Literal != 0 {
				out[m] = true
			}
		}
	}
	switch x := e.(type) {
	case *ast.NumberLiteral, *ast.StringLiteral, *ast.BooleanLiteral, *ast.TemplateLiteral:
		addAll(c.TypeOf(e))
	case *ast.UnaryExpression:
		if _, ok := x.Arg.(*ast.NumberLiteral); ok && (x.Op == "-" || x.Op == "+") {
			addAll(c.TypeOf(e))
		}
		if x.Op == "!" {
			addAll(c.TypeOf(e)) // a boolean result is fresh
		}
	case *ast.ConditionalExpression:
		c.freshBranches(out, seen, x.Consequent, x.Alternate)
	case *ast.BinaryExpression:
		switch x.Op {
		case "&&", "||", "??":
			c.freshBranches(out, seen, x.Left, x.Right)
		case ",":
			c.freshLiterals(x.Right, out, seen)
		case "==", "!=", "===", "!==", "<", ">", "<=", ">=", "instanceof", "in":
			addAll(c.TypeOf(e))
		}
	case *ast.SequenceExpression:
		if len(x.Exprs) > 0 {
			c.freshLiterals(x.Exprs[len(x.Exprs)-1], out, seen)
		}
	case *ast.AssignmentExpression:
		if x.Op == "=" {
			c.freshLiterals(x.Right, out, seen)
		}
	case *ast.NonNullExpression:
		c.freshLiterals(x.Arg, out, seen)
	case *ast.CallExpression:
		// A generic call's inferences keep the arguments' freshness: `f("a")`
		// with `f<T>(x: T): T` is a fresh "a". Explicit type arguments are not
		// fresh.
		if len(x.TypeArgs) > 0 {
			return
		}
		fn := c.signaturesOf(c.TypeOf(x.Callee), false)
		if c.Unanswered(fn) || fn.Flags&Object == 0 || fn.Kind != Function {
			return
		}
		generic := len(fn.TypeParams) > 0
		for _, o := range fn.Overloads {
			generic = generic || len(o.TypeParams) > 0
		}
		if generic {
			for _, a := range x.Args {
				c.freshLiterals(a, out, seen)
			}
		}
	case *ast.TaggedTemplateExpression:
		// A generic tag's inferences keep its substitutions' freshness,
		// as a call's.
		if len(x.TypeArgs) > 0 {
			return
		}
		fn := c.signaturesOf(c.TypeOf(x.Tag), false)
		if c.Unanswered(fn) || fn.Flags&Object == 0 || fn.Kind != Function {
			return
		}
		generic := len(fn.TypeParams) > 0
		for _, o := range fn.Overloads {
			generic = generic || len(o.TypeParams) > 0
		}
		if generic {
			for _, a := range x.Exprs {
				c.freshLiterals(a, out, seen)
			}
		}
	case *ast.Identifier:
		// A const keeps its initializer's fresh type.
		sym, _ := c.b.Resolve(x)
		if sym == nil || seen[sym] || len(sym.Declarations) != 1 {
			return
		}
		if v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration); ok && v.Kind == "const" && v.TypeAnnot == nil && v.Init != nil {
			seen[sym] = true
			c.freshLiterals(v.Init, out, seen)
			return
		}
		// A `boolean` binding narrowed by assignment keeps the assigned
		// literal's freshness (tsc's getAssignmentReducedType maps it
		// fresh): `var n = s` after `var s: boolean = false` is boolean.
		if dt := c.typeOfSymbol(sym); !c.Unanswered(dt) && someMember(dt, Boolean) {
			for _, m := range members(dt) {
				if m.Flags&Boolean != 0 && m.Flags&BooleanLiteral == 0 {
					for _, n := range members(c.TypeOf(x)) {
						if n.Flags&BooleanLiteral != 0 {
							out[n] = true
						}
					}
				}
			}
		}
	}
}

// awaited is the type an `await` of a value of type t produces (tsc's
// getAwaitedType): a Promise's value type, through nested promises and each
// member of a union; any other object with a `then` (a thenable) is
// unanswered, since its callback's type decides; a non-thenable is itself.
func (c *Checker) awaited(t *Type, depth int) *Type {
	if depth > 8 || c.Unanswered(t) || t.Flags&(Any|Unknown) != 0 {
		return t
	}
	if t.Flags&Union != 0 {
		ms := make([]*Type, 0, len(t.Types))
		for _, m := range t.Types {
			a := c.awaited(m, depth+1)
			if c.Unanswered(a) {
				return a
			}
			ms = append(ms, a)
		}
		return c.in.union(ms...)
	}
	if t.Flags&Object == 0 {
		return t
	}
	if t.Kind == Interface && t.Symbol != nil && (t.Symbol.Name == "Promise" || t.Symbol.Name == "PromiseLike") && len(t.TypeArgs) == 1 && c.inLibrary(t.Symbol.Scope) {
		return c.awaited(t.TypeArgs[0], depth+1)
	}
	// A program's thenable (getPromisedTypeOfPromise): what its `then`'s
	// onfulfilled callback takes, awaited; any when that callback is any
	// (TS1320). A library type is declared partially; none but Promise is
	// a thenable.
	if pt, ok := c.promisedType(t); ok {
		return c.awaited(pt, depth+1)
	}
	for o := t; o != nil; o = o.Base {
		if p := o.Prop("then"); p != nil {
			// A `then` with no call signatures makes no thenable (tsc's
			// isThenableType): the object is its own awaited value.
			if pt := p.Type; pt != nil && !c.Unanswered(pt) && pt.Flags&(Any|Unknown|Union|TypeParam) == 0 &&
				!(pt.Flags&Object != 0 && (pt.Kind == Function || len(pt.Calls) > 0)) {
				return t
			}
			return c.unanswered
		}
	}
	return t
}

// promisedType is what program object type t's `then` method resolves
// with (tsc's getPromisedTypeOfPromise): the union, over every `then`
// signature's onfulfilled callback and each of that callback's signatures,
// of its first parameter, or any when a callback's type is any; false when
// t is no such thenable (no `then`, a library one, or a callback the
// checker cannot read).
func (c *Checker) promisedType(t *Type) (*Type, bool) {
	if t.Flags&Object == 0 || t.Flags&Union != 0 || t.Kind != Anonymous && t.Kind != Instance && t.Kind != Interface || t.partial {
		return nil, false
	}
	if t.Symbol != nil && c.inLibrary(t.Symbol.Scope) {
		return nil, false
	}
	p := t.Prop("then")
	if p == nil || p.Type == nil || c.Unanswered(p.Type) || p.Type.Flags&Object == 0 || p.Type.Kind != Function {
		return nil, false
	}
	var out []*Type
	for _, then := range callSignatures(p.Type) {
		if len(then.Params) == 0 {
			return nil, false
		}
		cb := then.Params[0]
		if cb.Flags&Any != 0 {
			return c.anyT, true
		}
		cb = c.NonUndefined(cb)
		cbs := []*Type{cb}
		if cb.Flags&Union != 0 {
			cbs = cb.Types
		}
		for _, m := range cbs {
			if m.Flags&Object == 0 || m.Kind != Function {
				return nil, false
			}
			for _, sig := range callSignatures(m) {
				if len(sig.Params) == 0 {
					out = append(out, c.neverT) // getTypeOfFirstParameterOfSignatureWithFallback
				} else {
					out = append(out, sig.Params[0])
				}
			}
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return c.in.union(out...), true
}

// callSignatures is a function type's call signatures: its overloads, else
// itself.
func callSignatures(fn *Type) []*Type {
	if len(fn.Overloads) > 0 {
		return fn.Overloads
	}
	return []*Type{fn}
}

// anyThenable reports a program thenable whose onfulfilled callback is
// any: tsc awaits it as any and reports TS1320.
func (c *Checker) anyThenable(t *Type) bool {
	if t == nil || c.Unanswered(t) || t.Flags&Object == 0 {
		return false
	}
	p := t.Prop("then")
	pt, ok := c.promisedType(t)
	return ok && pt.Flags&Any != 0 && p != nil && p.Type.Params[0].Flags&Any != 0
}

// GlobalInterface is the type of the global interface name declares (the
// builtin declarations' `interface RegExp`), its type parameters left as
// they are, or nil when there is none.
func (c *Checker) GlobalInterface(name string) *Type {
	if c.b.Globals == nil {
		return nil
	}
	sym := c.b.Globals.Symbols.Get(name)
	if sym == nil || sym.Flags&binder.Interface == 0 {
		return nil
	}
	// A generic interface (`Map<K, V>`): its members over its own type
	// parameters, `any` for one the checker cannot name.
	var args []*Type
	for _, d := range sym.Declarations {
		if id, ok := d.Node.(*ast.InterfaceDeclaration); ok {
			scope := c.b.ScopeOf(id)
			for _, name := range id.TypeParams {
				arg := c.anyT
				if scope != nil {
					if tps := scope.Symbols.Get(name); tps != nil && tps.Flags&binder.TypeParameter != 0 {
						arg = c.typeParamType(tps)
					}
				}
				args = append(args, arg)
			}
			break
		}
	}
	t := c.interfaceOf(sym, args)
	if t == nil || c.Unanswered(t) {
		return nil
	}
	return t
}

// newBuiltinType types `new Name(args)` for a builtin the parser gives its
// own node (`new Date`, `new Map`, `new Set`, `new WeakMap`, `new WeakSet`):
// through the global's construct signatures, as a NewExpression is.
// builtinCtor is the construct signatures of the global name (`Map`), nil
// when it has none.
func (c *Checker) builtinCtor(name string) *Type {
	sym := c.builtinCtorSym(name)
	if sym == nil {
		return nil
	}
	ctor := c.signaturesOf(c.typeOfSymbol(sym), true)
	if c.Unanswered(ctor) || ctor.Flags&Object == 0 || ctor.Kind != Function {
		return nil
	}
	return ctor
}

// builtinCtorSym is the global variable name declares (`Map`), nil when
// the builtin declarations declare none.
func (c *Checker) builtinCtorSym(name string) *binder.Symbol {
	if c.b.Globals == nil {
		return nil
	}
	sym := c.b.Globals.Symbols.Get(name)
	if sym == nil || sym.Flags&binder.Variable == 0 {
		return nil
	}
	return sym
}

// builtinNewParts is a builtin `new` node's global, arguments and explicit
// type arguments.
func builtinNewParts(e ast.Node) (string, []ast.Expression, []ast.TypeNode, bool) {
	switch e := e.(type) {
	case *ast.NewDateExpression:
		if e.Millis != nil {
			return "Date", []ast.Expression{e.Millis}, nil, true
		}
		return "Date", e.Args, nil, true
	case *ast.NewMapExpression:
		var args []ast.Expression
		if e.Init != nil {
			args = []ast.Expression{e.Init}
		}
		var targs []ast.TypeNode
		if e.KeyType != nil && e.ValType != nil {
			targs = []ast.TypeNode{e.KeyType, e.ValType}
		}
		return "Map", args, targs, true
	case *ast.NewSetExpression:
		var args []ast.Expression
		if e.Init != nil {
			args = []ast.Expression{e.Init}
		}
		var targs []ast.TypeNode
		if e.ElemType != nil {
			targs = []ast.TypeNode{e.ElemType}
		}
		return "Set", args, targs, true
	case *ast.NewWeakMapExpression:
		var args []ast.Expression
		if e.Init != nil {
			args = []ast.Expression{e.Init}
		}
		var targs []ast.TypeNode
		if e.KeyType != nil && e.ValType != nil {
			targs = []ast.TypeNode{e.KeyType, e.ValType}
		}
		return "WeakMap", args, targs, true
	case *ast.NewWeakSetExpression:
		var args []ast.Expression
		if e.Init != nil {
			args = []ast.Expression{e.Init}
		}
		var targs []ast.TypeNode
		if e.ElemType != nil {
			targs = []ast.TypeNode{e.ElemType}
		}
		return "WeakSet", args, targs, true
	case *ast.NewXMLHttpRequestExpression:
		return "XMLHttpRequest", nil, nil, true
	case *ast.NewTypedArrayExpression:
		for name, kind := range typedArrayKinds {
			if kind == e.ElemKind {
				return name, present(e.Arg, e.ByteOffset, e.Length), nil, true
			}
		}
	case *ast.NewRequestExpression:
		return "Request", present(e.URL, e.Init), nil, true
	case *ast.NewReadableStreamExpression:
		return "ReadableStream", present(e.Source, e.Strategy), annots(e.ChunkType), true
	case *ast.NewWritableStreamExpression:
		return "WritableStream", present(e.Sink, e.Strategy), annots(e.ChunkType), true
	case *ast.NewTransformStreamExpression:
		var targs []ast.TypeNode
		if e.InType != nil && e.OutType != nil {
			targs = []ast.TypeNode{e.InType, e.OutType}
		}
		return "TransformStream", present(e.Transformer, e.WritableStrategy, e.ReadableStrategy), targs, true
	case *ast.NewErrorExpression:
		if e.Kind == "AggregateError" {
			return e.Kind, present(e.Errors, e.Message), nil, true
		}
		return e.Kind, present(e.Message, e.Name), nil, true
	case *ast.NewCompressionStreamExpression:
		if e.Decompress {
			return "DecompressionStream", present(e.Format), nil, true
		}
		return "CompressionStream", present(e.Format), nil, true
	case *ast.NewArrayExpression:
		return "Array", present(e.Size), annots(e.ElemType), true
	case *ast.NewArrayBufferExpression:
		if e.Shared {
			return "SharedArrayBuffer", present(e.ByteLength), nil, true
		}
		return "ArrayBuffer", present(e.ByteLength), nil, true
	}
	return "", nil, nil, false
}

// typedArrayKinds maps each TypedArray global to the element kind its
// `new` node carries.
var typedArrayKinds = map[string]string{
	"Int8Array": "int8", "Uint8Array": "uint8", "Uint8ClampedArray": "uint8clamped",
	"Int16Array": "int16", "Uint16Array": "uint16", "Int32Array": "int32",
	"Uint32Array": "uint32", "Float32Array": "float32", "Float64Array": "float64",
	"BigInt64Array": "bigint64", "BigUint64Array": "biguint64",
}

// present is the leading arguments that are there: a `new` node keeps an
// omitted one as nil, and only trailing ones may be omitted.
func present(args ...ast.Expression) []ast.Expression {
	var out []ast.Expression
	for _, a := range args {
		if a == nil {
			break
		}
		out = append(out, a)
	}
	return out
}

// annots is a `new` node's lone explicit type argument, if it has one.
func annots(t ast.TypeNode) []ast.TypeNode {
	if t == nil {
		return nil
	}
	return []ast.TypeNode{t}
}

func (c *Checker) newBuiltinType(orig ast.Expression, name string, args []ast.Expression, typeArgs []ast.TypeNode, pos ast.Pos) *Type {
	for _, a := range args {
		c.TypeOf(a)
	}
	ctor := c.builtinCtor(name)
	if ctor == nil {
		return c.unanswered
	}
	fake := ast.NewCallExpression(ast.NewIdentifier(name, pos), args, pos)
	fake.TypeArgs = typeArgs
	// The call's type arguments may come from where the `new` stands (tsc's
	// inference from the return type).
	if c.ctxStandIn == nil {
		c.ctxStandIn = map[ast.Node]ast.Node{}
	}
	c.ctxStandIn[fake] = orig
	defer delete(c.ctxStandIn, fake)
	switch {
	case len(ctor.Overloads) > 0:
		r := c.resolveOverload(fake, ctor)
		if c.Unanswered(r) {
			// No overload takes these arguments (an error reported where
			// the call is checked): tsc types the call from its candidates,
			// here the one interface they all construct.
			if t := sameResultInterface(ctor.Overloads); t != nil {
				return t
			}
		}
		return r
	case len(ctor.TypeParams) > 0:
		m := c.inferArgs(args, typeArgs, ctor, true)
		if len(typeArgs) == 0 {
			c.inferFromReturnContext(fake, ctor, m)
		}
		return c.instantiate(ctor.Ret(), m)
	}
	return ctor.Ret()
}

// sameResultInterface is the result of the first non-generic overload when
// every overload constructs the same interface, else nil.
func sameResultInterface(ovs []*Type) *Type {
	var first *Type
	for _, ov := range ovs {
		if ov.Ret() == nil || ov.Ret().Flags&Object == 0 || ov.Ret().Symbol == nil {
			return nil
		}
		if first != nil && ov.Ret().Symbol != first.Symbol {
			return nil
		}
		if first == nil && len(ov.TypeParams) == 0 {
			first = ov.Ret()
		}
	}
	return first
}

// boundCallResult is the result of `f.call(…)` / `f.apply(…)` where f is a
// function of one non-generic signature: tsc's CallableFunction under
// strictBindCallApply. The js lane keeps the Function interface's any.
func (c *Checker) boundCallResult(e *ast.CallExpression) *Type {
	m, ok := e.Callee.(*ast.MemberExpression)
	if !ok || c.opts.CompatJS() || (m.Property != "call" && m.Property != "apply") || e.Optional || m.Optional {
		return nil
	}
	ft := c.TypeOf(m.Object)
	if c.Unanswered(ft) || ft.Flags&Object == 0 || ft.Kind != Function || len(ft.Overloads) > 0 || len(ft.TypeParams) > 0 || ft.Ret() == nil {
		return nil
	}
	if ft.Ret().Flags&Void != 0 {
		return c.voidT
	}
	return ft.Ret()
}

// boundFunction is the type of `f.bind(thisArg, …bound)` where f has one
// non-generic signature: f without its `this` and the bound leading
// parameters (CallableFunction.bind under strictBindCallApply).
func (c *Checker) boundFunction(e *ast.CallExpression) *Type {
	m, ok := e.Callee.(*ast.MemberExpression)
	if !ok || c.opts.CompatJS() || m.Property != "bind" || e.Optional || m.Optional || len(e.Args) == 0 {
		return nil
	}
	for _, a := range e.Args {
		if _, spread := a.(*ast.SpreadElement); spread {
			return nil
		}
	}
	ft := c.TypeOf(m.Object)
	if c.Unanswered(ft) || ft.Flags&Object == 0 || ft.Kind != Function || len(ft.Overloads) > 0 || len(ft.TypeParams) > 0 || ft.Ret() == nil {
		return nil
	}
	k := len(e.Args) - 1
	if k > len(ft.Params) || ft.restParam && k >= len(ft.Params) {
		return nil
	}
	var optionals []bool
	if len(ft.optionals) > k {
		optionals = ft.optionals[k:]
	}
	bound := c.in.function(ft.Params[k:], optionals, ft.restParam, ft.Ret(), nil, nil)
	if len(ft.ParamNames) == len(ft.Params) {
		bound = c.in.withParamNames(bound, ft.ParamNames[k:])
	}
	return bound
}

// LibraryMethod reports a member read that names a method the builtin
// declarations declare (`m.get`, `re.test`, `process.send`).
func (c *Checker) LibraryMethod(e *ast.MemberExpression) bool {
	c.TypeOf(e)
	p := c.memberDecls[e]
	if p == nil || p.Decl == nil {
		return false
	}
	if _, ok := p.Decl.(*ast.MethodSignature); !ok {
		return false
	}
	t := c.NonNullable(c.TypeOf(e.Object))
	return t.Flags&Object != 0 && t.Symbol != nil && (t.Kind == Interface || t.Kind == Instance) && c.inLibrary(t.Symbol.Scope)
}

// chainLink types e when it is a link of an optional chain, as tsc's
// checkPropertyAccessChain / checkElementAccessChain / checkCallChain do: a
// `?.` link reads its receiver with null and undefined removed, a link
// after one reads the receiver's non-optional type, and the link's type
// gains undefined when its chain can short-circuit there.
func (c *Checker) chainLink(e ast.Expression) (*Type, bool) {
	obj, optional, ok := chainObject(e)
	if !ok {
		return nil, false
	}
	if _, busy := c.chainRecv[obj]; busy {
		return nil, false // the link itself, over the receiver set below
	}
	var recv *Type
	propagate := false
	switch {
	case optional:
		ot := c.TypeOf(obj)
		if c.Unanswered(ot) || ot.Flags&Any != 0 {
			return nil, false
		}
		if c.optionalOnNullish(ot) {
			// Always short-circuits. An element of never is never, so the
			// chain is undefined; a member or a call of never is tsc's
			// error type.
			if _, index := e.(*ast.IndexExpression); index {
				return c.undefinedT, true
			}
			return c.anyT, true
		}
		recv = c.nonNullable(ot)
		propagate = recv != ot
	case !chainEnd(obj) && inOptionalChain(obj):
		c.TypeOf(obj)
		nt, ok := c.chainNonOpt[obj]
		if !ok {
			return nil, false
		}
		recv, propagate = nt, true
	default:
		return nil, false
	}
	c.chainRecv[obj] = recv
	t := c.checkExpr(e)
	delete(c.chainRecv, obj)
	if propagate && !c.Unanswered(t) && t.Flags&Any == 0 {
		c.chainNonOpt[e] = t
		t = c.in.union(t, c.undefinedT)
	}
	return t, true
}

// ChainNonOptional is the type of e, a link of an optional chain, without
// the undefined its chain adds: the type the link has where its receivers
// are known present.
func (c *Checker) ChainNonOptional(e ast.Expression) *Type {
	t := c.TypeOf(e)
	if nt, ok := c.chainNonOpt[e]; ok {
		return nt
	}
	return t
}

// InOptionalChain reports e as a link of an optional chain.
func InOptionalChain(e ast.Expression) bool { return inOptionalChain(e) }

// chainReceiver is the type a link that continues an optional chain reads
// its receiver as: the receiver's non-optional type.
func (c *Checker) chainReceiver(obj ast.Expression) (*Type, bool) {
	if chainEnd(obj) || !inOptionalChain(obj) {
		return nil, false
	}
	c.TypeOf(obj)
	nt, ok := c.chainNonOpt[obj]
	return nt, ok
}

// isPrivateInLeft reports `#x in o`, whose left operand is a private name.
func isPrivateInLeft(e *ast.BinaryExpression) bool {
	id, ok := e.Left.(*ast.Identifier)
	return ok && e.Op == "in" && strings.HasPrefix(id.Name, "#")
}

func isObjectLiteralNode(x ast.Expression) bool {
	_, ok := x.(*ast.ObjectLiteral)
	return ok
}

// plainObject reports an anonymous object type without index or call
// signatures: an object literal's.
func plainObject(t *Type) bool {
	return t.Flags&Object != 0 && t.Flags&Union == 0 && t.Kind == Anonymous && t.StringIndex == nil && t.NumberIndex == nil && len(t.Calls) == 0
}

// literalBranchCovered is other when branch x, an object literal of type
// lt, is a subtype of it: each of lt's properties is other's, of an
// assignable type, and every property of other lt lacks is optional.
func (c *Checker) literalBranchCovered(x ast.Expression, lt, other *Type) *Type {
	if !isObjectLiteralNode(x) || !plainObject(lt) || other.Flags&Object == 0 || other.Flags&Union != 0 {
		return nil
	}
	switch other.Kind {
	case Anonymous, Interface:
	default:
		return nil
	}
	for _, p := range lt.Props {
		op := other.Prop(p.Name)
		if op == nil || !c.assignable(p.Type, op.Type) {
			return nil
		}
	}
	for _, op := range other.Props {
		if lt.Prop(op.Name) == nil && !op.Optional {
			return nil
		}
	}
	return other
}

// normalizeLiteralPair gives each of two object literal types the other's
// missing properties as optional undefined (tsc's normalized object
// literal union).
func (c *Checker) normalizeLiteralPair(a, b *Type) (*Type, *Type, bool) {
	if !plainObject(a) || !plainObject(b) {
		return nil, nil, false
	}
	add := func(t, from *Type) *Type {
		props := append([]*Property(nil), t.Props...)
		for _, p := range from.Props {
			if t.Prop(p.Name) == nil {
				props = append(props, &Property{Name: p.Name, Type: c.undefinedT, Optional: true})
			}
		}
		if len(props) == len(t.Props) {
			return t
		}
		return c.in.object(props)
	}
	return add(a, b), add(b, a), true
}

// spreadable reports whether getSpreadType copies t's properties as they
// are: an object literal's or type literal's type, or an interface, with no
// index or call signatures and every member modeled.
// invalidSpread reports a type tsc's isValidSpreadType rejects in an
// object literal's spread (TS2698): what is left once the definitely
// falsy members are removed must be any, an object or a type parameter,
// and something must be left.
func (c *Checker) invalidSpread(t *Type) bool {
	members := []*Type{t}
	if t.Flags&Union != 0 {
		members = t.Types
	}
	left := 0
	for _, m := range members {
		if definitelyFalsy(m) {
			continue
		}
		left++
		if m.Flags&(Any|Object|NonPrimitive|TypeParam|Deferred) == 0 {
			return true
		}
	}
	return left == 0
}

// WithoutFalsy is t without its definitely falsy members (tsc's
// removeDefinitelyFalsyTypes), which is what an object spread of it copies;
// nil when nothing is left.
func (c *Checker) WithoutFalsy(t *Type) *Type {
	var keep []*Type
	for _, m := range members(t) {
		if !definitelyFalsy(m) {
			keep = append(keep, m)
		}
	}
	if len(keep) == 0 {
		return nil
	}
	return c.in.union(keep...)
}

// definitelyFalsy reports a type whose every value is falsy: null,
// undefined, void, or the literal false, 0, "" or 0n (tsc's
// removeDefinitelyFalsyTypes).
func definitelyFalsy(t *Type) bool {
	switch {
	case t.Flags&Nullish != 0:
		return true
	case t.Flags&BooleanLiteral != 0:
		return t.Value == "false"
	case t.Flags&NumberLiteral != 0:
		return t.Value == "0" || t.Value == "-0"
	case t.Flags&StringLiteral != 0:
		return t.Value == ""
	case t.Flags&BigIntLiteral != 0:
		return t.Value == "0" || t.Value == "0n"
	}
	return false
}

// spreadable reports an object type whose spread the checker types: every
// member known. A library type lists only what this compiler implements, so
// it is spread only when it is a dictionary alone (`NodeJS.Dict<T>`,
// `ProcessEnv`), its index signature covering every read.
// instanceSpreads reports a class instance's member a spread copies, as
// tsc's isSpreadableProperty: a public data property, not a method, an
// accessor, a private or protected member or a `#` name.
func instanceSpreads(p *Property) bool {
	return !p.Accessor && p.Visibility == "" && !strings.HasPrefix(p.Name, "#") && (p.Field || p.Owner == nil)
}

func (c *Checker) spreadable(t *Type) bool {
	return t.Flags&Object != 0 && t.Flags&Union == 0 && (t.Kind == Anonymous && t.ClassOf == nil || t.Kind == Interface || t.Kind == Instance) &&
		(!t.partial || t.StringIndex != nil && len(t.Props) == 0) && (t.NumberIndex == nil || t.StringIndex != nil) && len(t.Calls) == 0 && len(t.Constructs) == 0
}

// declaresStatic reports whether class cls declares a static member name
// (a class value's snapshot, mid-build, may not hold it yet).
func (c *Checker) declaresStatic(cls *binder.Symbol, name string) bool {
	return classDeclares(cls, name, true)
}

// classDeclares reports whether class cls declares a field or method name,
// static or not as static says.
func classDeclares(cls *binder.Symbol, name string, static bool) bool {
	if cls == nil {
		return false
	}
	for _, d := range cls.Declarations {
		cd, ok := d.Node.(*ast.ClassDeclaration)
		if ce, isExpr := d.Node.(*ast.ClassExpression); isExpr {
			cd, ok = ce.Decl, ce.Decl != nil
		}
		if !ok {
			continue
		}
		for _, f := range cd.Fields {
			if f.Static == static && f.Name == name {
				return true
			}
		}
		for _, m := range cd.Methods {
			if m.IsStatic == static && m.Name == name {
				return true
			}
		}
	}
	return false
}

// hasMethodValue reports whether object literal lit has a method or a
// function expression value (whose `this` is the literal).
func hasMethodValue(lit *ast.ObjectLiteral) bool {
	for _, p := range lit.Properties {
		if _, ok := p.Value.(*ast.FunctionExpression); ok {
			return true
		}
	}
	return false
}

// derivedBranchReduced is tsc's subtype reduction of two class instance
// types one of which derives from, and is assignable to, the other: the
// base. Nil otherwise, and for interfaces, whose strict subtype relation
// tsc checks member by member (a Buffer beside a Uint8Array stays a union).
func (c *Checker) derivedBranchReduced(a, b *Type) *Type {
	inst := func(t *Type) bool {
		return t.Flags == Object && t.Kind == Instance && t.Symbol != nil
	}
	if a == b || !inst(a) || !inst(b) {
		return nil
	}
	if c.derivesFrom(a, b) && c.assignableTo(a, b) == yes {
		return b
	}
	if c.derivesFrom(b, a) && c.assignableTo(b, a) == yes {
		return a
	}
	return nil
}

// newWithoutConstruct is the type of `new` of a target with call signatures
// but no construct signature: tsc's any (TS7009 under noImplicitAny). As
// JavaScript, a plain function is a constructor function, which this
// checker does not type; a generator, async function or arrow never
// constructs (the `new` throws), any.
func (c *Checker) newWithoutConstruct(e *ast.NewExpression, calleeT *Type, sym *binder.Symbol) *Type {
	if c.Unanswered(calleeT) {
		return nil
	}
	call := c.signaturesOf(calleeT, false)
	if c.Unanswered(call) || call.Flags&Object == 0 || call.Kind != Function {
		return nil
	}
	if d := c.newTargetDecl(e, sym); c.opts.CompatJS() && !neverConstructs(d) {
		if _, fe := d.(*ast.FunctionExpression); fe {
			for _, a := range e.Args {
				c.TypeOf(a) // what they assign takes part (`new x(x = 1)`)
			}
			return c.anyT // a JavaScript constructor function a variable holds: its instance
		}
		return nil
	}
	if c.newNoCtor == nil {
		c.newNoCtor = map[*ast.NewExpression]bool{}
	}
	c.newNoCtor[e] = true
	return c.anyT
}

// newTargetDecl is the function a `new` names: a declared function, or the
// function a variable is initialized with.
func (c *Checker) newTargetDecl(e *ast.NewExpression, sym *binder.Symbol) ast.Node {
	if sym == nil {
		if id, ok := e.Callee.(*ast.Identifier); ok {
			sym, _ = c.b.Resolve(id)
		}
	}
	if sym == nil || len(sym.Declarations) != 1 {
		return nil
	}
	switch d := sym.Declarations[0].Node.(type) {
	case *ast.FunctionDeclaration:
		return d
	case *ast.VarDeclaration:
		return d.Init
	}
	return nil
}

// neverConstructs reports a function that is not a constructor: a
// generator, an async function or an arrow.
func neverConstructs(n ast.Node) bool {
	switch f := n.(type) {
	case *ast.ArrowFunction:
		return true
	case *ast.FunctionExpression:
		return f.IsGenerator || f.IsAsync
	case *ast.FunctionDeclaration:
		return f.IsGenerator || f.IsAsync
	}
	return false
}

// ownInitializerRead reports a read of an unannotated variable inside its
// own initializer, not deferred into a nested function or class (`var acc
// = (acc ?? 0) + j`): tsc types the variable with its error type (TS7022).
func (c *Checker) ownInitializerRead(e *ast.Identifier, sym *binder.Symbol) bool {
	if len(sym.Declarations) != 1 {
		return false
	}
	v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration)
	if !ok || v.TypeAnnot != nil || v.Init == nil {
		return false
	}
	for p := c.parentOf(e); p != nil; p = c.parentOf(p) {
		switch p.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		}
		if p == ast.Node(v) {
			return true
		}
	}
	return false
}

// reportOwnInit reports TS7022 at the name of a variable read in its own
// initializer, once.
func (c *Checker) reportOwnInit(sym *binder.Symbol) {
	v := sym.Declarations[0].Node.(*ast.VarDeclaration)
	if c.opts.CompatJS() || c.ImplicitAnyVariables || c.implicitAnyReported[v] {
		return
	}
	c.implicitAnyReported[v] = true
	pos := v.GetPos()
	if v.NamePos.Line > 0 {
		pos = v.NamePos
	}
	c.report(diag.ImplicitAnyOwnInit, pos, sourceName(v.Name))
}

// paramDefaultReference reports a parameter default's read of its own
// parameter (TS2372, and TS7022 when unannotated: the read is any) or of a
// later one (TS2373), outside any function nested in the default; nil
// otherwise.
func (c *Checker) paramDefaultReference(e *ast.Identifier, sym *binder.Symbol) *Type {
	var owner ast.Node
	for _, d := range sym.Declarations {
		switch d.Node.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction:
			owner = d.Node
		}
	}
	if owner == nil {
		return nil
	}
	var fn ast.Node
	for p := c.parentOf(e); p != nil; p = c.parentOf(p) {
		switch p.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction:
			fn = p
		}
		if fn != nil {
			break
		}
	}
	if fn != owner {
		return nil
	}
	var params []ast.Param
	switch f := fn.(type) {
	case *ast.FunctionDeclaration:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	case *ast.ArrowFunction:
		params = f.Params
	}
	declared := -1
	for i, p := range params {
		if p.Name == e.Name {
			declared = i
		}
	}
	if declared < 0 {
		return nil
	}
	for j, p := range params {
		if p.Default == nil || !containsNode(p.Default, e) {
			continue
		}
		switch {
		case j == declared:
			if p.Type == nil {
				c.report(diag.ImplicitAnyOwnInit, p.Pos, sourceName(p.Name))
			}
			c.report(diag.ParamSelfReference, e.GetPos(), sourceName(p.Name))
			if p.Type == nil {
				return c.anyT
			}
		case declared > j:
			c.report(diag.ParamForwardReference, e.GetPos(), sourceName(p.Name), sourceName(e.Name))
		}
		return nil
	}
	return nil
}

// containsNode reports n within root.
func containsNode(root, n ast.Node) bool {
	found := false
	ast.Inspect(root, func(x ast.Node) bool {
		found = found || x == n
		return !found
	})
	return found
}

// baseClassValue is the value of the class sym extends (`typeof B`, or a
// builtin base's constructor value), nil when it has none the checker
// models.
func (c *Checker) baseClassValue(sym *binder.Symbol) *Type {
	for _, d := range sym.Declarations {
		cd, ok := d.Node.(*ast.ClassDeclaration)
		if !ok {
			if ce, isCE := d.Node.(*ast.ClassExpression); isCE {
				cd, ok = ce.Decl, ce.Decl != nil
			}
		}
		if !ok || cd.BaseClass == "" {
			continue
		}
		bsym := c.baseClassSymbol(cd, sym.Scope)
		if (bsym == nil || bsym.Flags&binder.Class == 0) && !cd.BaseQualified {
			bsym = lookupName(cd.BaseClass, sym.Scope)
			if bsym == nil || bsym.Flags&binder.Variable == 0 {
				bsym = c.builtinImport(cd.BaseClass)
			}
		}
		if bsym == nil {
			return nil
		}
		if bt := c.typeOfSymbol(bsym); !c.Unanswered(bt) && bt.Flags&Object != 0 {
			return bt
		}
		return nil
	}
	return nil
}

// unconstructibleFn reports a new.target whose function (through arrows)
// is async or a generator, which `new` cannot call.
func (c *Checker) unconstructibleFn(e ast.Node) bool {
	for n := c.parentOf(e); n != nil; n = c.parentOf(n) {
		switch f := n.(type) {
		case *ast.ArrowFunction:
			continue
		case *ast.FunctionDeclaration:
			return f.IsAsync || f.IsGenerator
		case *ast.FunctionExpression:
			return f.IsAsync || f.IsGenerator
		case *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		}
	}
	return false
}

// declaresStatic reports whether cd declares a static member named name.
func declaresStatic(cd *ast.ClassDeclaration, name string) bool {
	for _, f := range cd.Fields {
		if f.Static && f.Name == name {
			return true
		}
	}
	for _, m := range cd.Methods {
		if m.IsStatic && m.Name == name {
			return true
		}
	}
	return false
}

// unionElementAccess is an element read off a union whose every member is
// an array, a tuple or a string: the union of each member's element.
func (c *Checker) unionElementAccess(e *ast.IndexExpression, obj *Type, key string, literal bool) (*Type, bool) {
	kt := c.TypeOf(e.Index)
	if c.Unanswered(kt) || kt.Flags&Union != 0 || kt.Flags&(Number|NumberLiteral) == 0 {
		return nil, false
	}
	var ts []*Type
	for _, m := range obj.Types {
		switch {
		case isStringLike(m) && m.Flags&Union == 0:
			ts = append(ts, c.strT)
		case m.Flags&Object != 0 && m.Kind == Array:
			if i, err := strconv.Atoi(key); literal && err == nil && i >= 0 && m.Head != nil {
				ts = append(ts, m.elemAt(i))
			} else {
				ts = append(ts, m.Elem)
			}
		case m.Flags&Object != 0 && m.Kind == Tuple && literal:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(m.Elems) {
				return nil, false // past a member's end: tsc's TS2339
			}
			ts = append(ts, m.Elems[i])
		case m.Flags&Object != 0 && m.Kind == Tuple && len(m.Elems) > 0:
			ts = append(ts, m.Elems...)
			if m.Opt > 0 {
				ts = append(ts, c.undefinedT)
			}
		default:
			return nil, false
		}
	}
	return c.in.union(ts...), true
}

// unionArrayMethod is the method a call on a union of arrays goes through
// when its members' methods combine into no one signature (`(string[] |
// number[]).map(…)`): the method of an array of the members' elements, as
// TypeScript (5.2 on) resolves it. Nil otherwise.
func (c *Checker) unionArrayMethod(e *ast.CallExpression, calleeT *Type) *Type {
	m, ok := e.Callee.(*ast.MemberExpression)
	if !ok || c.Unanswered(calleeT) || calleeT.Flags&Union == 0 || c.unionSignature(calleeT) != nil {
		return nil
	}
	recv := c.TypeOf(m.Object)
	if c.Unanswered(recv) || recv.Flags&Union == 0 {
		return nil
	}
	var elems []*Type
	for _, t := range recv.Types {
		if t.Flags&Object == 0 || t.Kind != Array || t.Elem == nil {
			return nil
		}
		elems = append(elems, t.Elem)
	}
	arr := c.in.array(c.in.union(elems...))
	am := c.apparentType(arr)
	if am == nil {
		return nil
	}
	p := am.Prop(m.Property)
	if p == nil || p.Type == nil {
		return nil
	}
	return c.withThis(p.Type, arr)
}

// returnsCompositeLiteral reports an arrow argument whose result is an object
// or array literal: the call's contextual return type reaches into it and
// keeps its literals (`map(() => ({ k: 'a' }))` against `{ k: 'a' }[]`).
func returnsCompositeLiteral(args []ast.Expression) bool {
	for _, a := range args {
		f, ok := a.(*ast.ArrowFunction)
		if !ok {
			continue
		}
		var results []ast.Expression
		if f.Body != nil {
			results = append(results, f.Body)
		} else if f.Block != nil {
			for _, st := range f.Block.Body {
				if r, ok := st.(*ast.ReturnStatement); ok && r.Value != nil {
					results = append(results, r.Value)
				}
			}
		}
		for _, r := range results {
			switch r.(type) {
			case *ast.ObjectLiteral, *ast.ArrayLiteral:
				return true
			}
		}
	}
	return false
}

// MappedFrom is the instance type t, a homomorphic mapping that keeps each
// member's type (`Readonly<Map<K, V>>`), maps, or nil: a value of t is a
// value of it.
func (c *Checker) MappedFrom(t *Type) *Type {
	return c.mappedFrom[t]
}

// genericAccess is `o[k]` for a key of type parameter type k: the deferred
// indexed access `O[K]` (tsc's getIndexedAccessType of a generic index),
// typed from a type node naming both, so instantiation evaluates it as a
// written `T[K]`.
func (c *Checker) genericAccess(e *ast.IndexExpression, obj, k *Type) *Type {
	n := c.accessNodes[e]
	if n == nil {
		n = &ast.IndexedAccessType{ObjectType: &ast.TypeReference{Name: "__kml_access_obj"}, IndexType: &ast.TypeReference{Name: "__kml_access_key"}}
		c.accessNodes[e] = n
	}
	pop := c.pushEnv([]string{"__kml_access_obj", "__kml_access_key"}, []*Type{obj, k})
	defer pop()
	return c.typeFromNode(n, c.b.Module)
}

// unionSpread is the first spread of literal e whose operand is a union of
// object types (`{ ...item }` for `item: S | F`), not already being typed
// per member, and that union; nil when there is none.
// higherOrderResult is a generic call's result when a generic function
// argument meets a non-generic function parameter and the callee returns a
// non-generic function (`compose(<J>(x: J) => [x])`): the argument's own
// type parameters are inferred from as they are and become the result's
// (tsc's higher-order function type inference). Nil otherwise.
func (c *Checker) higherOrderResult(e *ast.CallExpression, callee *Type) *Type {
	ret := callee.Ret()
	if len(e.TypeArgs) > 0 || ret == nil || ret.Flags&Object == 0 || ret.Flags&Union != 0 || ret.Kind != Function || len(ret.TypeParams) > 0 || len(ret.Overloads) > 0 {
		return nil
	}
	var tps []*Type
	for i, a := range e.Args {
		if _, spread := a.(*ast.SpreadElement); spread || i >= len(callee.Params) {
			return nil
		}
		pt := callee.Params[i]
		if pt.Flags&Object == 0 || pt.Flags&Union != 0 || pt.Kind != Function || len(pt.TypeParams) > 0 {
			continue
		}
		at := c.TypeOf(a)
		if c.Unanswered(at) || at.Flags&Object == 0 || at.Kind != Function || len(at.TypeParams) == 0 || len(at.Overloads) > 0 {
			continue
		}
		tps = append(tps, at.TypeParams...)
	}
	if len(tps) == 0 {
		return nil
	}
	c.keepSigParams++
	m := c.inferArgsRecv(callRecv(e), e.Args, e.TypeArgs, callee, true)
	c.keepSigParams--
	r := c.instantiate(ret, m)
	if c.Unanswered(r) || r.Flags&Object == 0 || r.Kind != Function {
		return nil
	}
	var own []*Type
	for _, tp := range tps {
		if occurs(tp, r) {
			own = append(own, tp)
		}
	}
	if len(own) == 0 {
		return nil
	}
	return c.in.function(r.Params, r.optionals, r.restParam, r.Ret(), r.Predicate, own)
}

// genericSpreads are the arguments of e's spreads whose types are generic.
func (c *Checker) genericSpreads(e *ast.ObjectLiteral) []ast.Expression {
	var out []ast.Expression
	for _, p := range e.Properties {
		sp, ok := p.Value.(*ast.SpreadElement)
		if !ok || p.KeyExpr != nil || p.Key != "" {
			continue
		}
		if _, busy := c.spreadAs[sp.Arg]; busy {
			continue
		}
		if st := c.TypeOf(sp.Arg); !c.Unanswered(st) && st.Flags&Any == 0 && c.generic(st) {
			out = append(out, sp.Arg)
		}
	}
	return out
}

// spreadIntersection is the intersection of parts, as an intersection
// type node over them evaluates: deferred while one is generic.
func (c *Checker) spreadIntersection(e *ast.ObjectLiteral, parts []*Type) *Type {
	if len(parts) == 1 {
		return parts[0]
	}
	if c.spreadNodes == nil {
		c.spreadNodes = map[*ast.ObjectLiteral]*ast.IntersectionType{}
	}
	n := c.spreadNodes[e]
	names := make([]string, len(parts))
	for i := range parts {
		names[i] = "__kml_spread" + strconv.Itoa(i)
	}
	if n == nil || len(n.Types) != len(parts) {
		n = &ast.IntersectionType{}
		for _, name := range names {
			n.Types = append(n.Types, &ast.TypeReference{Name: name})
		}
		c.spreadNodes[e] = n
	}
	defer c.pushEnv(names, parts)()
	return c.typeFromNode(n, c.b.Module)
}

func (c *Checker) unionSpread(e *ast.ObjectLiteral) (ast.Expression, *Type) {
	for _, p := range e.Properties {
		sp, ok := p.Value.(*ast.SpreadElement)
		if !ok || p.KeyExpr != nil || p.Key != "" {
			continue
		}
		if _, busy := c.spreadAs[sp.Arg]; busy {
			continue
		}
		st := c.TypeOf(sp.Arg)
		if c.Unanswered(st) || st.Flags&Union == 0 || len(st.Types) > 16 {
			continue
		}
		objects := true
		for _, m := range st.Types {
			if m.Flags&Object == 0 || m.Flags&Union != 0 || !c.spreadable(m) {
				objects = false
				break
			}
		}
		if objects {
			return sp.Arg, st
		}
	}
	return nil, nil
}

// homomorphicMapped reports a deferred mapped type over `keyof` a type (a
// homomorphic mapped type), or an intersection with one.
func (c *Checker) homomorphicMapped(t *Type) bool {
	if parts := c.deferredIntersectionParts(t); parts != nil {
		for _, m := range parts {
			if m.Flags&Deferred != 0 && c.homomorphicMapped(m) {
				return true
			}
		}
		return false
	}
	n, ok := t.deferNode.(*ast.MappedType)
	if !ok || n.Constraint == nil {
		return false
	}
	op, ok := n.Constraint.(*ast.TypeOperator)
	return ok && op.Operator == "keyof"
}

func isTupleNode(n ast.TypeNode) bool {
	_, ok := n.(*ast.TupleType)
	return ok
}

// variadicElement is element access e on variadic tuple obj (`[X, ...A]`):
// a fixed element before the spread by its index, `length` a number, any
// other numeric key the indexed access `obj[k]`, evaluated once A is bound;
// nil for any other member, read off the tuple's array interface.
func (c *Checker) variadicElement(e *ast.IndexExpression, obj *Type, key string, literal bool) *Type {
	n := obj.deferNode.(*ast.TupleType)
	kt := c.TypeOf(e.Index)
	if c.Unanswered(kt) {
		return nil
	}
	if literal && !numericName(key) {
		return nil
	}
	if !literal && kt.Flags&(Number|NumberLiteral) == 0 {
		return nil
	}
	if literal {
		if i, err := strconv.Atoi(key); err == nil && i >= 0 {
			for j, el := range n.Elements {
				if j == i {
					switch m := el.(type) {
					case *ast.RestType, *ast.OptionalType:
					case *ast.NamedTupleMember:
						if !m.Rest && !m.Optional {
							return c.deferredPart(obj, m.Type)
						}
					default:
						return c.deferredPart(obj, el)
					}
					break
				}
				if _, isRest := el.(*ast.RestType); isRest {
					break
				}
				if m, ok := el.(*ast.NamedTupleMember); ok && m.Rest {
					break
				}
			}
		}
	}
	return c.genericAccess(e, obj, kt)
}

// spreadTuple is the tuple an array literal with spreads is in a tuple
// context: each spread tuple's elements in place, a spread array or rest
// tuple's rest the tuple's rest, the elements after it joining that rest
// (tsc's createArrayLiteralType over a tuple context); nil for a spread
// the checker cannot shape.
func (c *Checker) spreadTuple(e *ast.ArrayLiteral) *Type {
	var head []*Type
	var rest *Type
	add := func(t *Type) {
		if rest != nil {
			rest = c.in.union(rest, t)
		} else {
			head = append(head, t)
		}
	}
	for _, x := range e.Elements {
		if sp, ok := x.(*ast.SpreadElement); ok {
			st := c.TypeOf(sp.Arg)
			if c.Unanswered(st) || st.Flags&Object == 0 || st.Flags&Union != 0 {
				return nil
			}
			switch {
			case st.Kind == Tuple && st.Opt == 0:
				for _, el := range st.Elems {
					add(el)
				}
			case st.Kind == Array:
				for _, h := range st.Head {
					add(h)
				}
				r := st.Elem
				if st.RestElem != nil {
					r = st.RestElem
				}
				if rest != nil {
					rest = c.in.union(rest, r)
				} else {
					rest = r
				}
			default:
				return nil
			}
			continue
		}
		t := c.TypeOf(x)
		if c.Unanswered(t) || isOmitted(x) {
			return nil
		}
		if !c.constAsserted(x) && !c.keepsLiteral(x, t) {
			t = c.widenFrom(x, t)
		}
		add(t)
	}
	if rest == nil {
		return c.in.tuple(head)
	}
	return c.in.tupleRest(head, rest)
}

// ctorReturnsForeign reports, in JavaScript, a class whose constructor may
// return an object other than an instance of the class (`return { … }`):
// `new` then yields that object (the return override).
func (c *Checker) ctorReturnsForeign(sym *binder.Symbol) bool {
	if !c.opts.CompatJS() {
		return false
	}
	if r, ok := c.foreignCtor[sym]; ok {
		return r
	}
	if c.foreignCtor == nil {
		c.foreignCtor = map[*binder.Symbol]bool{}
	}
	c.foreignCtor[sym] = false // (a `new` of the class inside: not foreign)
	found := false
	for _, d := range sym.Declarations {
		cd, ok := d.Node.(*ast.ClassDeclaration)
		if !ok || cd.Constructor == nil || cd.Constructor.Body == nil {
			continue
		}
		ast.Inspect(cd.Constructor.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ArrowFunction, *ast.ClassExpression, *ast.ClassDeclaration:
				return false
			case *ast.ReturnStatement:
				if x.Value == nil || found {
					return false
				}
				if _, isThis := x.Value.(*ast.ThisExpression); isThis {
					return false
				}
				if ne, ok := x.Value.(*ast.NewExpression); ok {
					if ne.ClassName == "Proxy" && ne.Callee == nil {
						found = true // a Proxy is typed as its target, not one
						return false
					}
				}
				t := c.TypeOf(x.Value)
				if c.Unanswered(t) {
					return false
				}
				if t.Kind == Instance && t.Symbol == sym {
					return false
				}
				if t.Flags&(Any|Object|NonPrimitive|Union|TypeParam) != 0 {
					found = true
				}
				return false
			}
			return !found
		})
	}
	if !found {
		// A derived class's `this` is what its base's construction
		// returned: another object when the base's constructor returns one.
		for _, d := range sym.Declarations {
			if cd, ok := d.Node.(*ast.ClassDeclaration); ok && cd.BaseClass != "" {
				b := c.baseClassSymbol(cd, sym.Scope)
				if b != nil && b != sym && b.Flags&binder.Class != 0 && c.ctorReturnsForeign(b) {
					found = true
				}
				// A base held in a binding (`extends Constructor`, a
				// parameter): what it constructs is `this`.
				if b == nil && !cd.BaseQualified {
					// (Not a builtin global, a var with an interface.)
					if v := lookupName(cd.BaseClass, sym.Scope); v != nil && v.Flags&binder.Variable != 0 && v.Flags&(binder.Class|binder.Function|binder.Interface) == 0 {
						found = true
					}
				}
			}
		}
	}
	c.foreignCtor[sym] = found
	return found
}

// libraryLoaded reports whether the library's declarations are bound (a
// checker run on a bare program has none): only then does a global tsc
// declares and they lack mean one this compiler does not provide.
func (c *Checker) libraryLoaded() bool {
	return c.b.Globals != nil && c.b.Globals.Symbols.Get("Array") != nil
}
