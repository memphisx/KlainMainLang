package checker

import (
	"math"
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
	// CatchVariablesAny types an unannotated catch variable any, as
	// TypeScript does without useUnknownInCatchVariables (which --strict,
	// and so this compiler, always sets). For measuring against
	// TypeScript's own non-strict cases.
	CatchVariablesAny bool
	// ImplicitAnyVariables types an unannotated variable declared without a
	// value any, as TypeScript does without noImplicitAny (--strict sets
	// it: such a variable evolves by its assignments).
	ImplicitAnyVariables bool
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
	// unanswered marks an expression whose type the checker does not model
	// yet. It behaves as any, but the shadow comparison skips it.
	unanswered *Type

	nodeTypes map[ast.Node]*Type
	symTypes  map[*binder.Symbol]*Type
	resolving map[*binder.Symbol]bool
	assigning map[ast.Expression]bool
	// storage maps each narrowable reference (a variable, a property path)
	// to its storage type: the declared type its reads narrow.
	storage map[ast.Expression]*Type
	// evolving marks the unannotated bindings declared without a value
	// (`let x;`): their type is a storage type, narrowed at every read.
	evolving map[*binder.Symbol]bool
	// tupleCtx holds the array literals whose tuple context is being found:
	// a generic call's context infers from the literal itself.
	tupleCtx map[*ast.ArrayLiteral]bool
	// building are the class and interface types whose members are being
	// filled in.
	building map[*Type]bool
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
	// inline counts the aliased conditions being expanded (at most 5, as
	// TypeScript).
	inline int
	// initializing are the parameters whose default is being typed.
	initializing map[*binder.Symbol]bool
	// parents maps each node to its parent, built on first use.
	parents map[ast.Node]ast.Node
	// declSyms maps each declaration node to its symbol, built on first use.
	declSyms map[ast.Node]*binder.Symbol
	// env names the type arguments of the generic declaration being typed.
	env []map[string]*Type
	// aliases memoises each alias per argument list; aliasing guards
	// recursion.
	aliases  map[string]*Type
	aliasing map[string]bool
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
	// depth is TypeOf's nesting; steps counts the flow steps of the
	// outermost query, and exhausted marks it past flowBudget.
	depth, steps int
	exhausted    bool
	// cutCount counts the cuts ever logged: a flow answer computed while it
	// grew is provisional and not cached.
	cutCount int
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
		nodeTypes: map[ast.Node]*Type{}, symTypes: map[*binder.Symbol]*Type{}, resolving: map[*binder.Symbol]bool{},
		assigning: map[ast.Expression]bool{}, storage: map[ast.Expression]*Type{}, evolving: map[*binder.Symbol]bool{}, tupleCtx: map[*ast.ArrayLiteral]bool{}, initializing: map[*binder.Symbol]bool{}, building: map[*Type]bool{}, failedNominal: map[*Type]bool{}, exhaustive: map[*ast.SwitchStatement]bool{}, memberDecls: map[*ast.MemberExpression]*Property{}, cannotFound: map[cannotFindKey]bool{}, modules: map[string]*Type{},
		aliases: map[string]*Type{}, aliasing: map[string]bool{}, enums: map[*binder.Symbol][]*Type{}, classing: map[*binder.Symbol]bool{}, ctorChain: map[*binder.Symbol]bool{}, nesting: map[*binder.Symbol]int{}, contextualizing: map[ast.Node]bool{}, flowCache: map[flowKey]*Type{}, provisional: map[ast.Expression]provisional{}}
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

// constAsserted reports `e as const`: its literal type is not fresh, and
// never widens.
func (c *Checker) constAsserted(e ast.Expression) bool {
	a, _ := c.assertion(e)
	return a.Const
}

// Unanswered reports whether t is the checker's "not modelled yet".
func (c *Checker) Unanswered(t *Type) bool { return t == c.unanswered }

// TypeOf returns the type of an expression, computing it once.
func (c *Checker) TypeOf(e ast.Expression) *Type {
	if e == nil {
		return c.voidT
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
		if a.Cast != nil && a.Cast.TypeNode() != nil {
			// `<T>e` is T, as `e as T` is.
			c.checkExpr(e)
			t := c.typeFromNode(a.Cast.TypeNode(), c.b.Module)
			c.nodeTypes[e] = t
			return t
		}
		if a.Const {
			c.constCtx++
			defer func() { c.constCtx-- }()
		}
	}
	c.depth++
	mark := len(c.cuts)
	t := c.checkExpr(e)
	c.depth--
	if c.exhausted {
		// Past the flow budget: nothing computed in this query is kept.
		if c.depth == 0 {
			c.exhausted = false
		}
		return c.unanswered
	}
	var deps []ast.Expression
	for _, v := range c.cuts[mark:] {
		if c.assigning[v] {
			deps = append(deps, v)
		}
	}
	if len(deps) > 0 {
		// Provisional: an enclosing computation is still open.
		c.provisional[e] = provisional{t, deps}
		return t
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
		if e.IsUndefined {
			return c.undefinedT
		}
		return c.nullT
	case *ast.Identifier:
		sym, ok := c.b.Resolve(e)
		if !ok || sym == nil {
			switch e.Name {
			case "undefined":
				return c.undefinedT
			case "NaN", "Infinity":
				return c.numT
			}
			if spec, ok := c.b.Program.BuiltinMarkers[e.Name]; ok {
				return c.moduleType(spec) // a builtin module, as its import binds it
			}
			if s := c.builtinImport(e.Name); s != nil && s.Flags&binder.Value != 0 {
				return c.typeOfSymbol(s)
			}
			c.unresolvedValue(e.Name, e, e.GetPos(), c.b.LookupScope(e))
			return c.unanswered // a global the checker has no declarations for yet
		}
		return c.narrowedType(e, sym, c.typeOfSymbol(sym))
	case *ast.SuperExpression:
		// `super.m` reads the base class's instance members.
		if m, ok := c.parentOf(e).(*ast.MemberExpression); ok && m.Object == ast.Expression(e) {
			if t := c.thisType(e); t.Flags&Object != 0 && t.Kind == Instance && t.Base != nil {
				return t.Base
			}
		}
		return c.unanswered
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
	case *ast.AwaitExpression:
		return c.awaited(c.TypeOf(e.Argument), 0)
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
		return c.join(c.TypeOf(e.Consequent), c.TypeOf(e.Alternate))
	case *ast.AssignmentExpression:
		c.TypeOf(e.Left)
		right := c.TypeOf(e.Right)
		if e.Op == "=" {
			return right
		}
		return c.unanswered
	case *ast.SequenceExpression:
		var last *Type
		for _, x := range e.Exprs {
			last = c.TypeOf(x)
		}
		return last
	case *ast.ArrayLiteral:
		if len(e.Elements) == 0 {
			return c.unanswered // an empty literal's element type comes from context
		}
		var elems []*Type
		for _, x := range e.Elements {
			t := c.TypeOf(x)
			if _, spread := x.(*ast.SpreadElement); spread || c.Unanswered(t) {
				return c.unanswered
			}
			if c.constCtx == 0 && !c.constAsserted(x) && !c.keepsLiteral(x, t) {
				t = c.widenFrom(x, t)
			}
			elems = append(elems, t)
		}
		if c.constCtx > 0 {
			return c.in.tuple(elems) // `as const`: a (readonly) tuple of literals
		}
		if tt := c.tupleContext(e, len(elems)); tt != nil {
			return c.in.tuple(elems) // `pair?: [number, number]` takes `[1, 2]`
		}
		return c.in.array(c.join(elems...))
	case *ast.ObjectLiteral:
		var props []*Property
		for _, p := range e.Properties {
			if p.KeyExpr != nil || p.AccessorKind != "" || p.Key == "" {
				return c.unanswered
			}
			t := c.TypeOf(p.Value)
			if c.Unanswered(t) {
				return c.unanswered
			}
			if c.constCtx == 0 && !c.constAsserted(p.Value) && !c.keepsLiteral(p.Value, t) {
				t = c.widenFrom(p.Value, t)
			}
			for i, q := range props {
				if q.Name == p.Key {
					// A duplicate: the last one wins.
					props = append(props[:i:i], props[i+1:]...)
					break
				}
			}
			props = append(props, &Property{Name: p.Key, Type: t})
		}
		return c.in.object(props)
	case *ast.MemberExpression:
		if m := c.namespaceMember(e); m != nil {
			return c.typeOfSymbol(m) // a namespace's member
		}
		obj := c.TypeOf(e.Object)
		switch {
		case c.Unanswered(obj):
			return c.unanswered
		case obj.Flags&Any != 0:
			return c.anyT // any's members are any, `?.` included
		case obj.Kind == Tuple && obj.Flags&Object != 0 && e.Property == "length":
			return c.in.literal(NumberLiteral, strconv.Itoa(len(obj.Elems))) // a fixed tuple's length is its size
		case obj.Kind == Array && obj.Flags&Object != 0 && e.Property == "length",
			isStringLike(obj) && e.Property == "length":
			return c.numT
		case e.Optional:
		default:
			// An object, or a union of objects that all declare the property
			// (a discriminated union's tag); a primitive or an array reads
			// its apparent type's members (String's, Array<T>'s).
			var ts []*Type
			for _, m := range members(obj) {
				recv := m
				if am := c.apparentType(m); am != nil {
					m = am
				}
				if m.Flags&Object == 0 || (m.Kind != Anonymous && m.Kind != Instance && m.Kind != Interface) {
					return c.unanswered
				}
				p := m.Prop(e.Property)
				if p == nil {
					if m.StringIndex != nil {
						// Through a string index signature (`env.PATH`).
						ts = append(ts, m.StringIndex)
						continue
					}
					return c.unanswered
				}
				ts = append(ts, c.withThis(p.Type, recv))
				if len(members(obj)) == 1 && p.Decl != nil {
					c.memberDecls[e] = p
				}
			}
			return c.narrowedMember(e, c.in.union(ts...))
		}
		return c.unanswered
	case *ast.IndexExpression:
		obj := c.TypeOf(e.Object)
		c.TypeOf(e.Index)
		if c.Unanswered(obj) {
			return c.unanswered
		}
		if obj.Flags&Any != 0 {
			return c.anyT
		}
		if e.Optional {
			return c.unanswered
		}
		key, literal := c.accessKey(e.Index)
		switch {
		case obj.Flags&Object != 0 && obj.Kind == Array:
			if literal {
				return c.narrowedMember(e, obj.Elem)
			}
			return obj.Elem
		case obj.Flags&Object != 0 && obj.Kind == Tuple && literal:
			if i, err := strconv.Atoi(key); err == nil && i >= 0 && i < len(obj.Elems) {
				return c.narrowedMember(e, obj.Elems[i])
			}
		case obj.Flags&Object != 0 && (obj.Kind == Anonymous || obj.Kind == Instance || obj.Kind == Interface):
			if literal {
				if p := obj.Prop(key); p != nil {
					return c.narrowedMember(e, p.Type)
				}
			}
			// An index signature: a numeric key reads the number index,
			// else the string one.
			kt := c.TypeOf(e.Index)
			if obj.NumberIndex != nil && kt.Flags&(Number|NumberLiteral) != 0 && kt.Flags&Union == 0 {
				return c.narrowedMember(e, obj.NumberIndex)
			}
			if obj.StringIndex != nil && (isStringLike(kt) || kt.Flags&(Number|NumberLiteral) != 0) {
				return c.narrowedMember(e, obj.StringIndex)
			}
		}
		return c.unanswered
	case *ast.CallExpression:
		// The callee first: a call's type needs its arguments only when the
		// callee is a modelled function, and typing them for nothing can
		// walk a cycle of assignments that each read the others.
		callee := c.signaturesOf(c.TypeOf(e.Callee), false)
		if c.Unanswered(callee) {
			return c.unanswered
		}
		for _, a := range e.Args {
			c.TypeOf(a)
		}
		if callee.Flags&Any != 0 {
			return c.anyT // calling an any is allowed and gives any
		}
		if callee.Flags&Object != 0 && callee.Kind == Function && !e.Optional {
			if len(callee.Overloads) > 0 {
				return c.resolveOverload(e, callee)
			}
			if len(callee.TypeParams) > 0 {
				if len(e.TypeArgs) == 0 && literalSensitive(e.Args) && c.mayHaveContext(e) {
					// tsc also infers from the call's contextual return type,
					// which can keep a literal argument's type (`const b:
					// Box<'a' | 'b'> = box('a')`); that is not modelled.
					return c.unanswered
				}
				return c.instantiate(callee.Result, c.inferArgs(e.Args, e.TypeArgs, callee, true))
			}
			return callee.Result
		}
		return c.unanswered
	case *ast.NewExpression:
		for _, a := range e.Args {
			c.TypeOf(a)
		}
		sym := c.b.NewTarget(e)
		if sym == nil {
			sym = c.builtinImport(e.ClassName)
		}
		if sym != nil && sym.Flags&binder.Class == 0 && sym.Flags&binder.Variable != 0 {
			// `new X(…)` through a value whose type has construct signatures
			// (`declare var Map: MapConstructor`).
			ctor := c.signaturesOf(c.typeOfSymbol(sym), true)
			if c.Unanswered(ctor) || ctor.Flags&Object == 0 || ctor.Kind != Function {
				return c.unanswered
			}
			fake := ast.NewCallExpression(ast.NewIdentifier(e.ClassName, e.GetPos()), e.Args, e.GetPos())
			fake.TypeArgs = e.TypeArgs
			switch {
			case len(ctor.Overloads) > 0:
				return c.resolveOverload(fake, ctor)
			case len(ctor.TypeParams) > 0:
				return c.instantiate(ctor.Result, c.inferArgs(e.Args, e.TypeArgs, ctor, true))
			}
			return ctor.Result
		}
		if sym == nil || sym.Flags&binder.Class == 0 {
			return c.unanswered
		}
		ctor := c.constructorType(sym)
		if ctor == nil {
			return c.unanswered
		}
		if len(ctor.TypeParams) == 0 {
			return ctor.Result
		}
		m := c.inferArgs(e.Args, e.TypeArgs, ctor, true)
		args := make([]*Type, len(ctor.TypeParams))
		for i, tp := range ctor.TypeParams {
			if args[i] = m[tp]; args[i] == nil || c.Unanswered(args[i]) {
				return c.unanswered
			}
		}
		if t := c.instanceOf(sym, args); t != nil {
			return t
		}
		return c.unanswered
	case *ast.ArrowFunction:
		if e.IsAsync {
			return c.unanswered
		}
		return c.functionType(e, e.Params, e.RetType, e.Block, e.Body)
	case *ast.FunctionExpression:
		if e.IsAsync || e.IsGenerator {
			return c.unanswered
		}
		return c.functionType(e, e.Params, e.RetType, e.Body, nil)
	case *ast.NonNullExpression:
		t := c.TypeOf(e.Arg)
		if c.Unanswered(t) {
			return t
		}
		return c.nonNullable(t)
	case *ast.AsExpression:
		c.TypeOf(e.Expr)
		if n := e.TypeAnnot.TypeNode(); n != nil {
			return c.typeFromNode(n, c.b.Module)
		}
		return c.unanswered
	}
	return c.unanswered
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
	l, r := c.TypeOf(e.Left), c.TypeOf(e.Right)
	switch e.Op {
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
		}
		return c.unanswered
	case "-", "*", "/", "%", "**", "&", "|", "^", "<<", ">>", ">>>":
		switch {
		case c.Unanswered(l) || c.Unanswered(r):
			return c.unanswered
		case isBigIntLike(l) && isBigIntLike(r):
			return c.bigintT
		case l.Flags&Any != 0 || r.Flags&Any != 0, isNumberLike(l) && isNumberLike(r):
			return c.numT
		}
		return c.unanswered // an operand TypeScript rejects (an object, a string)
	case ",":
		return r
	}
	return c.unanswered
}

// join is the union of ts, or unanswered when any is.
func (c *Checker) join(ts ...*Type) *Type {
	for _, t := range ts {
		if c.Unanswered(t) {
			return c.unanswered
		}
	}
	return c.in.union(ts...)
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

func (c *Checker) truthyPart(t *Type) *Type {
	var keep []*Type
	for _, m := range members(t) {
		switch {
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
	s := strings.ReplaceAll(raw, "_", "")
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return strconv.FormatFloat(v, 'g', -1, 64)
	} else if math.IsInf(v, 1) {
		return "Infinity" // `1e999` overflows, and its type is Infinity's
	}
	if v, err := strconv.ParseInt(s, 0, 64); err == nil {
		return strconv.FormatInt(v, 10)
	}
	return s
}

// functionType is the type of a function expression or arrow fn; its
// annotations resolve in its own scope.
func (c *Checker) functionType(fn ast.Node, params []ast.Param, ret *ast.TypeAnnotation, body *ast.BlockStatement, exprBody ast.Expression) *Type {
	return c.signatureType(fn, params, ret, body, exprBody, c.b.Module)
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

// tupleContext is the tuple type of n elements an array literal is
// expected to be, when its context (null and undefined aside) is one.
func (c *Checker) tupleContext(e *ast.ArrayLiteral, n int) *Type {
	if c.tupleCtx[e] {
		return nil
	}
	c.tupleCtx[e] = true
	ct := c.contextualType(e)
	delete(c.tupleCtx, e)
	if ct == nil || c.Unanswered(ct) {
		return nil
	}
	var tuple *Type
	for _, m := range members(ct) {
		switch {
		case m.Flags&(Null|Undefined) != 0 || m == c.missingT:
		case m.Flags&Object != 0 && m.Kind == Tuple && tuple == nil:
			tuple = m
		default:
			return nil
		}
	}
	if tuple == nil || len(tuple.Elems) != n {
		return nil
	}
	return tuple
}

// apparentType is the builtin interface whose members a primitive or an
// array has (TypeScript's apparent type): String for a string, Number for a
// number, Boolean for a boolean, Array<T> for an array or tuple; nil for any
// other type, or when the builtin declarations do not declare it.
func (c *Checker) apparentType(t *Type) *Type {
	if c.b.Globals == nil || t.Flags&Union != 0 {
		return nil
	}
	name := ""
	var args []*Type
	switch {
	case t.Member != "":
		return nil // an enum member: its enum's own rules
	case t.Flags&StringLike != 0:
		name = "String"
	case t.Flags&NumberLike != 0:
		name = "Number"
	case t.Flags&BooleanLike != 0:
		name = "Boolean"
	case t.Flags&ESSymbol != 0:
		name = "Symbol"
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

// MemberOverloadDecls are an overloaded method's declarations, one per
// overload in the order of the member type's Overloads, or nil.
func (c *Checker) MemberOverloadDecls(e *ast.MemberExpression) []ast.Node {
	c.TypeOf(e)
	if p := c.memberDecls[e]; p != nil && len(p.Decls) > 1 {
		return p.Decls
	}
	return nil
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
func (c *Checker) builtinImport(name string) *binder.Symbol {
	ref, ok := c.b.Program.BuiltinImportRefs[sourceName(name)]
	if !ok {
		return nil
	}
	scope := c.b.AmbientModules[ref.Module]
	if scope == nil {
		return nil
	}
	return scope.Symbols.Get(ref.Name)
}

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
	case *ast.Identifier:
		// A const keeps its initializer's fresh type.
		sym, _ := c.b.Resolve(x)
		if sym == nil || seen[sym] || len(sym.Declarations) != 1 {
			return
		}
		if v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration); ok && v.Kind == "const" && v.TypeAnnot == nil && v.Init != nil {
			seen[sym] = true
			c.freshLiterals(v.Init, out, seen)
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
	if t.Kind == Interface && t.Symbol != nil && t.Symbol.Name == "Promise" && len(t.TypeArgs) == 1 && c.inLibrary(t.Symbol.Scope) {
		return c.awaited(t.TypeArgs[0], depth+1)
	}
	// A library type is declared partially; none but Promise is a thenable.
	for o := t; o != nil; o = o.Base {
		if o.Prop("then") != nil {
			return c.unanswered
		}
	}
	return t
}
