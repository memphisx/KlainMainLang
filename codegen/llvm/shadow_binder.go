// shadow_binder.go — the new front end's side of the shadow comparison
// (TDD-00230 P2.1–P2.3): the binder answers name resolution, the checker the
// type questions, its types mapped to codegen's representation by reprOf.
package llvm

import (
	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/lib"
	"KlainMainLang/options"
	"KlainMainLang/resolver"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type frontOracle struct {
	b *binder.Binding
	c frontView
	r reprCtx
	// q memoizes ExprType within one outermost query, over which codegen's
	// state does not change: the rules that read a receiver's
	// representation would otherwise retype a call chain's receivers once
	// per rule and link.
	q *oracleQuery
	// storesBusy is the references reprStoresPresent is answering.
	storesBusy map[*ast.Identifier]bool
	// litBusy holds the object literals being typed, for one whose property
	// reads the literal again.
	litBusy map[*ast.ObjectLiteral]bool
	// storageBusy and returnsBusy guard literalStorage and
	// literalMethodReturns against a method reading its own literal's
	// binding.
	storageBusy map[*ast.ObjectLiteral]bool
	returnsBusy map[*ast.ObjectLiteral]bool
	// dynInit caches dynamicInit per declaration.
	dynInit map[*ast.VarDeclaration]bool
	// fnReads caches readInFunctions (its "" key marks it filled).
	fnReads map[string]bool
	// forOfBusy holds the loops whose iterable is being typed, for one that
	// reads its own variable (`for (var v of v)`).
	forOfBusy map[*ast.ForOfStatement]bool
	// executors are the functions passed to `new Promise(…)`; onRejected
	// maps a `.catch(f)` / `.then(_, f)` handler literal to its receiver.
	executors  map[ast.Node]*ast.NewExpression
	onRejected map[ast.Node]ast.Expression
	// written memoizes writtenIn per scope node, when set: the AST and its
	// binding do not change while codegen asks.
	written map[ast.Node]map[*binder.Symbol]bool
	// anyBound are the function literals initializing an `any` binding:
	// emitted with the boxed convention, a rest parameter one box.
	anyBound map[ast.Node]bool
	// inBody holds the functions whose body result inferredBodyReturn is
	// computing.
	inBody map[ast.Node]bool
	// widenedReduceOf maps a js-lane reduce callback that can fall off the
	// end to its call (jsWidenedReduceCallback).
	widenedReduceOf map[ast.Node]*ast.CallExpression
}

func init() {
	boxed := func(frontOracle, *ast.CallExpression) (Type, bool) { return TypeAny, true }
	// The Object statics that build or return a dynamic object (a defined
	// accessor, a prototype link, a descriptor) are held boxed, whatever
	// the lib's result type.
	for _, k := range []string{"defineProperty", "getOwnPropertyDescriptor", "create", "getPrototypeOf", "setPrototypeOf", "groupBy"} {
		in := intrinsics["Object."+k]
		in.held = boxed
		intrinsics["Object."+k] = in
	}
	// Of a dynamic object, values/entries are a dynamic array
	// (emitDynAnyEntries).
	for _, k := range []string{"values", "entries"} {
		in := intrinsics["Object."+k]
		in.held = func(o frontOracle, call *ast.CallExpression) (Type, bool) {
			if len(call.Args) == 1 {
				if at, ok := o.ExprType(call.Args[0]); ok && isUnconstrainedDynamic(at) {
					return TypeAny, true
				}
			}
			return Type{}, false
		}
		intrinsics["Object."+k] = in
	}
	// JSON.stringify of an `any` (or of arguments only known at run time)
	// may be undefined: held boxed, though the lib's result is string.
	in := intrinsics["JSON.stringify"]
	in.held = func(o frontOracle, call *ast.CallExpression) (Type, bool) {
		if resolver.StringifyArgsAtRunTime(call.Args) {
			return TypeAny, true
		}
		if len(call.Args) >= 1 {
			if at, ok := o.ExprType(call.Args[0]); ok && isUnconstrainedDynamic(at) {
				return TypeAny, true
			}
		}
		return Type{}, false
	}
	intrinsics["JSON.stringify"] = in
}

func init() {
	NewShadowOracle = func(prog *ast.Program, opts options.Options, r reprCtx) ShadowOracle {
		// The lane's own rules: Annex B binding and the checker's js mode.
		libProgs, _ := lib.Programs() // a parse error in them fails the strict lane's check first
		b := binder.BindWith(prog, binder.Options{AnnexB: opts.CompatJS(), Lib: libProgs})
		r.nodeJS = func(sym *binder.Symbol) bool {
			if b.Globals == nil {
				return false
			}
			ns := b.Globals.Symbols.Get("NodeJS")
			return ns != nil && b.NamespaceScope(ns) == sym.Scope
		}
		// Checked up front, as the front end's checker is, so it answers
		// from the program as written, before lowering rewrites it.
		c := checker.NewWith(b, opts)
		c.Check()
		return frontOracle{b: b, c: c, r: r, q: &oracleQuery{}, storesBusy: map[*ast.Identifier]bool{}, litBusy: map[*ast.ObjectLiteral]bool{}, storageBusy: map[*ast.ObjectLiteral]bool{}, returnsBusy: map[*ast.ObjectLiteral]bool{}, dynInit: map[*ast.VarDeclaration]bool{}, fnReads: map[string]bool{}, forOfBusy: map[*ast.ForOfStatement]bool{}}.withPromiseCallbacks(prog)
	}
}

// ExprType answers typeOfExpr, which gives a variable or property read
// the representation of its storage: the checker's storage type, not the
// type the read narrows it to.
func (o frontOracle) ExprType(e ast.Expression) (Type, bool) {
	// A binding an accessor is defined on is held boxed (accessorLiteral).
	if id, isID := e.(*ast.Identifier); isID {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && accessorLiteral(o.b, v) {
				return TypeAny, true
			}
		}
	}
	if o.q == nil {
		t, ok := o.exprTypeQuery(e)
		return o.byValReturn(e, o.growableBuffer(e, t), ok), ok
	}
	key := oracleKey{e: e}
	if o.r.scopeKey != nil {
		key.depth, key.base = o.r.scopeKey()
	}
	if o.q.depth == 0 {
		o.q.memo = map[oracleKey]oracleAnswer{}
	} else if a, ok := o.q.memo[key]; ok {
		return a.t, a.ok
	}
	// An expression whose typing reaches itself again (a loop variable
	// read in its own iterable, a literal read in its own property) may
	// re-ask a few times on its way to an answer; one nested deeper than
	// that is a cycle, cut with no answer. An answer computed past a cut is
	// provisional, and not kept.
	if o.q.inflight[key] >= oracleReentry {
		o.q.cuts++
		return Type{}, false
	}
	if o.q.inflight == nil {
		o.q.inflight = map[oracleKey]int{}
	}
	o.q.inflight[key]++
	cuts := o.q.cuts
	o.q.depth++
	t, ok := o.exprTypeQuery(e)
	t = o.growableBuffer(e, t)
	t = o.byValReturn(e, t, ok)
	o.q.depth--
	o.q.inflight[key]--
	if o.q.cuts == cuts {
		o.q.memo[key] = oracleAnswer{t: t, ok: ok}
	}
	if o.q.depth == 0 {
		o.q.memo, o.q.inflight = nil, nil
	}
	return t, ok
}

type oracleQuery struct {
	depth int
	memo  map[oracleKey]oracleAnswer
	cuts  int // provisional answers handed out (a cycle cut)
	// inflight counts each expression's asks being computed.
	inflight map[oracleKey]int
}

// oracleReentry is how deeply one expression's typing may re-ask itself
// before the ask is a cycle.
const oracleReentry = 4

// oracleKey is a node under the scope stack codegen asked under: a name's
// answer depends on its binding.
type oracleKey struct {
	e     ast.Expression
	depth int
	base  uintptr
}

type oracleAnswer struct {
	t  Type
	ok bool
}

// exprTypeQuery is ExprType, unmemoized.
func (o frontOracle) exprTypeQuery(e ast.Expression) (Type, bool) {
	// A structuredClone the run time performs: a box (a class instance's
	// clone is a plain object, as in Node).
	if call, isCall := e.(*ast.CallExpression); isCall && o.r.runtimeClone != nil && o.r.runtimeClone(call) {
		return TypeAny, true
	}
	// A private member of a static method's `this` taken as a parameter
	// (staticReceivers, which rewrites the body): brand-checked, then read
	// from the class's slot, typed as the member the rewrite started from.
	if m, isM := e.(*ast.MemberExpression); isM && strings.HasPrefix(m.Property, "#") && o.r.origin != nil && o.c.TypeOf(m.Object).Flags&checker.Any != 0 {
		if orig, ok := o.r.origin(m); ok && orig != ast.Expression(m) {
			if ot := o.c.TypeOf(orig); !o.c.Unanswered(ot) && ot.Flags&checker.Any == 0 {
				if rt, ok := o.r.of(ot); ok {
					return rt, true
				}
			}
		}
	}
	// A property the checker cannot type of a value held boxed (a js-lane
	// binding) is read by the dynamic dispatch: a box.
	if m, isM := e.(*ast.MemberExpression); isM && o.c.Unanswered(o.c.TypeOf(m)) {
		if rt, ok := o.ExprType(m.Object); ok && isUnconstrainedDynamic(rt) && rt.DynPropTy == nil {
			return TypeAny, true
		}
	}
	// A member the checker cannot type of a class instance codegen lays out
	// (a js-lane field the constructor assigns, in the program's own code):
	// that field's slot.
	if m, isM := e.(*ast.MemberExpression); isM && o.r.js != nil && o.r.js() && o.r.inLib != nil && !o.r.inLib() && !strings.HasPrefix(m.Property, "__kml_") && o.c.Unanswered(o.c.TypeOf(m)) {
		if rt, ok := o.ExprType(m.Object); ok && rt.ClassName != "" {
			if _, ft, fok := rt.FieldIndex(m.Property); fok {
				return ft, true
			}
		}
	}
	// A call or read the any-argument dispatch runs (a builtin function
	// passed as a callback, `xs.filter(Boolean)`) is emitted through that
	// dispatch: its box.
	if o.r.boxOverride != nil {
		if bt, ok := o.r.boxOverride(e); ok {
			return bt, true
		}
	}
	// An array's or a string's method read as a value is its prototype's
	// record, a box, whatever signature the checker gives it.
	if m, isM := e.(*ast.MemberExpression); isM && !m.Optional && o.r.methodRecord != nil && o.r.methodRecord(m) {
		return TypeAny, true
	}
	// A JavaScript literal property's function returning its own `this`
	// returns that box: its `this` is set by the call. Asked while the
	// literal itself is typed, its parameters are what it binds them as.
	if fe, isFE := e.(*ast.FunctionExpression); isFE && fe.PropValue && fe.RetType == nil && !fe.IsAsync && !fe.IsGenerator && o.r.js != nil && o.r.js() && o.r.literalParam != nil && returnsOwnThis(fe.Body) {
		params := make([]Type, len(fe.Params))
		for i, p := range fe.Params {
			pt, pok := o.r.literalParam(fe, i)
			if !pok || p.Rest || p.ArrayPattern != nil || p.ObjectPattern != nil {
				return Type{}, false
			}
			params[i] = pt
		}
		return FuncType(params, TypeAny), true
	}
	// A method read off a receiver held as a box (a class a function
	// returns) is read dynamically: the box's member.
	if m, isM := e.(*ast.MemberExpression); isM && !m.Optional {
		if mt := o.c.TypeOf(m); !o.c.Unanswered(mt) && mt.Flags&checker.Object != 0 && mt.Kind == checker.Function {
			if ot := o.c.TypeOf(m.Object); !o.c.Unanswered(ot) && ot.Flags&checker.Any == 0 && o.callInitialized(m.Object) {
				if rt, ok := o.ExprType(m.Object); ok && isUnconstrainedDynamic(rt) {
					return TypeAny, true
				}
			}
		}
	}
	// A call of a receiver's method whose result is the receiver's own type
	// (a literal method returning `this`), with no layout of its own,
	// returns what the receiver holds.
	if call, isCall := e.(*ast.CallExpression); isCall {
		var recv ast.Expression
		if ix, isIx := call.Callee.(*ast.IndexExpression); isIx {
			recv = ix.Object // (a named method's call has its own rules)
		}
		if recv != nil {
			if t := o.c.TypeOf(call); !o.c.Unanswered(t) && t == o.c.TypeOf(recv) && t.Flags&checker.Object != 0 && t.Kind == checker.Anonymous {
				if _, mapped := o.r.of(t); !mapped {
					return o.ExprType(recv)
				}
			}
		}
	}
	// A literal spreading a class instance with a TypeScript-private or
	// protected field copies it at run time (an own property), though tsc's
	// type leaves it out: the layout codegen builds holds it.
	// A named member read off a plain box the checker types a primitive is
	// that primitive (emitMember's dynamic arm unboxes it): `this.k` in an
	// object literal's method, whose `this` is a box.
	// … but in a static generator lifted out of its class (liftStaticGenerators)
	// that takes its receiver as a leading `this: any`, the read stays the
	// box the generator yields.
	if m, ok := e.(*ast.MemberExpression); ok && o.liftedGenReceiver(m) {
		return TypeAny, true
	}
	if m, ok := e.(*ast.MemberExpression); ok && !m.Optional && o.r.checkerPrim != nil {
		if _, isThis := m.Object.(*ast.ThisExpression); isThis {
			if ot, ok := o.ExprType(m.Object); ok && isUnconstrainedDynamic(ot) && ot.DynPropTy == nil {
				if pt, ok := o.r.checkerPrim(m); ok {
					return pt, true
				}
			}
		}
	}
	if lit, ok := e.(*ast.ObjectLiteral); ok && o.spreadsHiddenFields(lit) {
		return o.hiddenFieldsLiteral(lit)
	}
	if id, ok := e.(*ast.Identifier); ok {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if v, isV := sym.Declarations[0].Node.(*ast.VarDeclaration); isV && v.TypeAnnot == nil {
				if lit, isLit := v.Init.(*ast.ObjectLiteral); isLit && o.spreadsHiddenFields(lit) {
					return o.hiddenFieldsLiteral(lit) // a binding of one
				}
			}
		}
	}
	// A js-lane prototype constructor returns a box (what `new` of it or a
	// plain call's `return new F(v)` yields; jsCollectProtoCtors).
	if id, ok := e.(*ast.Identifier); ok && o.r.protoCtor != nil && o.r.protoCtor(id.Name) {
		if sym, _ := o.b.Resolve(id); sym != nil && sym.Flags&binder.Function != 0 && sym.Scope != nil && sym.Scope.Kind == binder.ModuleScope {
			if t, ok := o.exprTypeQueryInner(e); ok && t.IsFunc() && t.FuncRetType != nil && t.FuncRetType.IR != "void" && !t.FuncRetType.IsDynamic() {
				any := TypeAny
				t.FuncRetType = &any
				return t, true
			}
		}
	}
	// `new F(…)` of a prototype constructor function is a dynamic object,
	// answered before any rule that reads F (inside F's own body, whose
	// return is still being inferred).
	if nx, ok := e.(*ast.NewExpression); ok && o.r.protoCtor != nil && (o.r.protoCtor(nx.ClassName) || o.newOfFunctionDecl(nx)) {
		return TypeAny, true
	}
	// `new Map([[k, v], …])` in the JavaScript lane, whose pairs of
	// differing value types no typed overload takes (the lane's lenient
	// arity then picks `new (): Map<any, any>`): the types codegen reads
	// the entries with.
	if nm, ok := e.(*ast.NewMapExpression); ok && o.r.mapEntries != nil && o.r.js != nil && o.r.js() && nm.KeyType == nil && nm.ValType == nil {
		if _, lit := nm.Init.(*ast.ArrayLiteral); lit && o.mapAnyAny(nm) {
			return o.r.mapEntries(nm), true
		}
	}
	// A JavaScript `new Set([[…], […]])` whose rows differ in layout holds
	// them as the seed does: boxed rows.
	set := e
	if id, isID := e.(*ast.Identifier); isID && o.r.js != nil && o.r.js() {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.Kind == "const" && v.TypeAnnot == nil {
				if _, isNS := v.Init.(*ast.NewSetExpression); isNS {
					set = v.Init // a const binding of one holds it as built
				}
			}
		}
	}
	if ns, ok := set.(*ast.NewSetExpression); ok && o.r.js != nil && o.r.js() && ns.ElemType == nil {
		if seed, lit := ns.Init.(*ast.ArrayLiteral); lit && len(seed.Elements) > 1 {
			if st, sok := o.ExprType(seed); sok && st.IsArray() && st.ElemType != nil && reprKey(*st.ElemType) == reprKey(ArrayOf(TypeAny)) {
				return SetType(*st.ElemType), true
			}
		}
	}
	// A const binding of a builtin method or a function value's property,
	// read as a value through a box (`const of = Array.of`, `const f =
	// n.isNaN`), holds that box.
	if id, ok := e.(*ast.Identifier); ok {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.Kind == "const" && v.TypeAnnot == nil {
				if m, isM := v.Init.(*ast.MemberExpression); isM && !m.Optional && (o.boxedReceiver(m) && o.functionTyped(m) || o.r.methodRecord != nil && o.r.methodRecord(m)) {
					if it, ok := o.ExprType(m); ok && isUnconstrainedDynamic(it) && it.DynPropTy == nil {
						return it, true
					}
				}
			}
		}
	}
	// A const binding of one holds it as built.
	if id, ok := e.(*ast.Identifier); ok && o.r.mapEntries != nil && o.r.js != nil && o.r.js() {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.Kind == "const" && v.TypeAnnot == nil {
				if nm, isNM := v.Init.(*ast.NewMapExpression); isNM && nm.KeyType == nil && nm.ValType == nil {
					if _, lit := nm.Init.(*ast.ArrayLiteral); lit && o.mapAnyAny(nm) {
						return o.r.mapEntries(nm), true
					}
				}
			}
		}
	}
	return o.exprTypeQueryInner(e)
}

// newOfFunctionDecl reports `new F(…)` of a plain function declaration
// (top-level, or local to a block or function) under -compat=js: a prototype constructor, which
// jsCollectProtoCtors records only after the bodies it may sit in (`return
// new P(v)` inside P) are first inferred.
func (o frontOracle) newOfFunctionDecl(nx *ast.NewExpression) bool {
	if o.r.js == nil || !o.r.js() || nx.Qualified || nx.ClassName == "" {
		return false
	}
	sym := o.b.NewTarget(nx)
	if sym == nil || len(sym.Declarations) != 1 || sym.Scope == nil {
		return false
	}
	// A block's function the resolver binds as a constant function
	// expression is one too.
	if v, isV := sym.Declarations[0].Node.(*ast.VarDeclaration); isV {
		if fe, isFE := v.Init.(*ast.FunctionExpression); isFE {
			return !fe.IsGenerator && !fe.IsAsync && len(fe.TypeParams) == 0
		}
		// Annex B's `var F` a block's function declaration assigns.
		if nl, isNull := v.Init.(*ast.NullLiteral); v.Kind == "var" && (v.Init == nil || isNull && nl.IsUndefined) {
			return blockFunctionNames(o.b)[v.Name]
		}
		return false
	}
	fd, ok := sym.Declarations[0].Node.(*ast.FunctionDeclaration)
	return ok && !fd.IsGenerator && !fd.IsAsync && len(fd.TypeParams) == 0
}

// boxedReceiver reports a member read of a program binding held as a box
// or a function value (`const n = Number; n.isNaN`), which the dynamic
// dispatch reads (typeOfExprFull's boxed- and function-receiver rules).
func (o frontOracle) boxedReceiver(m *ast.MemberExpression) bool {
	id, isID := m.Object.(*ast.Identifier)
	if !isID {
		return false
	}
	if sym, _ := o.b.Resolve(id); sym == nil || sym.Flags&binder.Variable == 0 || o.r.libGlobal(sym) {
		return false // (a library global, `JSON`: its intrinsics)
	}
	rt, ok := o.ExprType(m.Object)
	if ok && rt.IsFunc() && o.r.funcProp != nil {
		// A function value's property: off the boxed function too.
		_, fok := o.r.funcProp(m)
		return fok
	}
	return ok && isUnconstrainedDynamic(rt) && rt.DynPropTy == nil
}

// mapAnyAny reports a `new Map(…)` the checker leaves unanswered or types
// Map<any, any>.
func (o frontOracle) mapAnyAny(nm *ast.NewMapExpression) bool {
	t := o.c.TypeOf(nm)
	if o.c.Unanswered(t) {
		return true
	}
	return len(t.TypeArgs) == 2 && t.TypeArgs[0].Flags&checker.Any != 0 && t.TypeArgs[1].Flags&checker.Any != 0
}

// literalGeneratorRead reports `o.g` of a const bound to an object
// literal whose g is a generator function: held boxed, as codegen's
// object literal holds a generator method.
func (o frontOracle) literalGeneratorRead(m *ast.MemberExpression) bool {
	return o.literalGeneratorReadAt(m, 0)
}

func (o frontOracle) literalGeneratorReadAt(m *ast.MemberExpression, depth int) bool {
	id, ok := m.Object.(*ast.Identifier)
	if !ok || depth > 8 {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil {
		return false
	}
	v, isV := singleVarDecl(sym)
	if !isV || v.Kind != "const" || v.TypeAnnot != nil {
		return false
	}
	lit, ok := v.Init.(*ast.ObjectLiteral)
	if !ok {
		return false
	}
	found := false
	for _, p := range lit.Properties {
		if p.KeyExpr == nil && p.Key == m.Property {
			switch val := p.Value.(type) {
			case *ast.FunctionExpression:
				found = val.IsGenerator
			case *ast.MemberExpression:
				found = o.literalGeneratorReadAt(val, depth+1) // `{ g: o.g }`
			case *ast.Identifier:
				found = o.generatorDeclRef(val) // `{ g }` of a `function* g`
			default:
				found = false
			}
		}
	}
	return found
}

func (o frontOracle) exprTypeQueryInner(e ast.Expression) (Type, bool) {
	if o.r.boxOverride != nil {
		if t, ok := o.r.boxOverride(e); ok {
			return t, true
		}
	}
	if m, ok := e.(*ast.MemberExpression); ok && (o.dynamicMethodRead(m) || o.literalGeneratorRead(m)) {
		return TypeAny, true
	}
	// A property read off a binding held as a plain box (an iterator step
	// over a parameter's iterator) is the box's dynamic read: a box, the
	// precise member type the checker gives it notwithstanding.
	if m, ok := e.(*ast.MemberExpression); ok && !m.Optional && !exprReceiverOrCallee(o.c.ParentOf(m), m) && o.boxedReceiver(m) {
		if rt, rok := o.ExprType(m.Object); rok && !rt.IsFunc() && len(rt.UnionMembers) == 0 {
			// (A primitive member, `.length`, reads typed.)
			if ct, mt := o.c.TypeOf(m.Object), o.c.TypeOf(m); !o.c.Unanswered(ct) && ct.Flags&checker.Object != 0 && !o.c.Unanswered(mt) && mt.Flags&checker.Object != 0 && mt.Flags&checker.Union == 0 {
				return TypeAny, true
			}
		}
	}
	// A const alias of a builtin lowered to a library function (`const st =
	// setTimeout`): that function's value (emitVarDecl's loweredGlobalFn).
	if id, isID := e.(*ast.Identifier); isID && o.r.loweredValue != nil {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 && !o.b.StoredElsewhere(sym) {
			if v, isV := sym.Declarations[0].Node.(*ast.VarDeclaration); isV && v.Kind == "const" && v.TypeAnnot == nil {
				if init, isI := v.Init.(*ast.Identifier); isI {
					if lt, lok := o.r.loweredValue(init); lok {
						return lt, true
					}
				}
			}
		}
	}
	// A parameter annotated with one string literal and null or undefined
	// (`y: 'p' | null`) keeps the literal, as annotationType reads it.
	if id, isID := e.(*ast.Identifier); isID {
		if lit, ok := o.literalParamAnnotation(id); ok {
			if ty, ok := o.exprType(e); ok && ty.IR == irPtr && !ty.IsDynamic() && ty.Kind == kindNone {
				ty.Kind, ty.LitValue = kindStrLiteral, lit
				return ty, true
			}
		}
	}
	// A `for (var q of …)` binding read outside its loop's body (after the
	// loop, or in a closure): the loop may not have run, so possibly
	// undefined, as codegen holds it.
	if id, isID := e.(*ast.Identifier); isID {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			var body ast.Node
			switch d := sym.Declarations[0].Node.(type) {
			case *ast.ForOfStatement:
				if d.Kind == "var" && d.VarName == id.Name && (o.b.ReadElsewhere(id) || !nonEmptyLiteral(d.Iterable)) {
					body = d.Body // (over a non-empty literal, the loop runs)
				}
			case *ast.ForInStatement:
				if d.Kind == "var" && d.VarName == id.Name {
					body = d.Body
				}
			}
			if body != nil && (o.b.ReadElsewhere(id) || !o.within(id, body)) {
				if ty, ok := o.exprType(e); ok && !ty.IsDynamic() && !ty.Nullable() && ty.IR == irPtr {
					return indexReadType(ty), true
				}
			}
		}
	}
	// `...{ length }`: the rest array's length, a number (tsc's implicit
	// any notwithstanding), as codegen binds it.
	if id, isID := e.(*ast.Identifier); isID && o.restPatternLength(id) {
		return TypeF64, true
	}
	// A property of `globalThis.X` (X a library global) reads as X's
	// (`globalThis.crypto.subtle`, as codegen's unwrapGlobalThis reads it).
	if m, ok := e.(*ast.MemberExpression); ok && !m.Optional && o.globalThisGlobal(m.Object) {
		if mt := o.c.TypeOf(m); !o.c.Unanswered(mt) && mt.Flags&checker.Object != 0 && mt.Flags&checker.Union == 0 && mt.Kind != checker.Function {
			if t, tok := o.r.of(mt); tok && !t.IsDynamic() {
				return t, true
			}
		}
	}
	// … and off any other expression held as a plain box (a generic
	// method's boxed result, `nested.get().get`).
	if m, ok := e.(*ast.MemberExpression); ok && !m.Optional {
		if _, isID := m.Object.(*ast.Identifier); !isID && !o.globalThisGlobal(m.Object) {
			if rt, rok := o.ExprType(m.Object); rok && isUnconstrainedDynamic(rt) && rt.DynPropTy == nil && len(rt.UnionMembers) == 0 {
				if mt := o.c.TypeOf(m); !o.c.Unanswered(mt) && mt.Flags&checker.Object != 0 && mt.Flags&checker.Union == 0 {
					return TypeAny, true
				}
			}
		}
	}
	// A generator step's `value`/`done` is codegen's {value, done} field,
	// not the IteratorResult member flow narrows it to.
	if m, ok := e.(*ast.MemberExpression); ok && !m.Optional && (m.Property == "value" || m.Property == "done") {
		if rt, rok := o.ExprType(m.Object); rok && isGenStepLayout(rt) {
			if _, ft, found := rt.FieldIndex(m.Property); found {
				return ft, true
			}
		}
	}
	// A generator function a literal's layout holds boxed, read off any
	// receiver of that layout (`make().seq`).
	if m, ok := e.(*ast.MemberExpression); ok && !m.Optional && isGeneratorFunc(o.c.TypeOf(m)) {
		if rt, rok := o.ExprType(m.Object); rok && rt.IsObject() && !rt.IsDynamic() && !rt.IsClass() {
			for _, f := range rt.Fields {
				if f.Name == m.Property && isUnconstrainedDynamic(f.Ty) {
					return TypeAny, true
				}
			}
		}
	}
	// A node a rewrite after binding built is the checker's type of the
	// expression it stands for, as the code was written (warmFront); the
	// rewrite's own reading (a static method's `this: any` receiver) is not
	// the original's.
	if o.r.origin != nil {
		if orig, isSyn := o.r.origin(e); isSyn {
			// A sequence is its last expression, as emission evaluates it.
			if s, isSeq := e.(*ast.SequenceExpression); isSeq && len(s.Exprs) > 0 {
				if st, sok := o.superSequence(s); sok {
					return st, true
				}
				return o.ExprType(s.Exprs[len(s.Exprs)-1])
			}
			t, kept := o.r.warm(orig)
			if !kept {
				t = o.c.TypeOf(orig)
			}
			if !o.c.Unanswered(t) {
				if ty, ok := o.r.of(t); ok {
					return ty, true
				}
			}
			// `C.#m` on the class a rewrite named: as codegen registered
			// the static member (a generator method's value).
			if m, isM := e.(*ast.MemberExpression); isM && o.r.staticMethod != nil {
				if id, isID := m.Object.(*ast.Identifier); isID {
					if _, synID := o.r.origin(id); synID {
						return o.r.staticMethod(id.Name, m.Property)
					}
				}
			}
			return Type{}, false
		}
	}
	if o.r.chainCopy != nil {
		if orig, isCopy := o.r.chainCopy(e); isCopy {
			return o.chainCopyType(e, orig)
		}
	}
	// new.target outside a constructor and any function declaration (a
	// function expression's, an arrow's in one, an async function's) is
	// undefined (emitNewTarget).
	if nt, isNT := e.(*ast.NewTargetExpression); isNT && nt.Ctx != ast.NewTargetConstructor && nt.FnID == 0 {
		return TypeUndefined, true
	}
	// … and a function declaration's is the function only where it is
	// compiled to be constructed.
	if nt, isNT := e.(*ast.NewTargetExpression); isNT && nt.Ctx == ast.NewTargetFunction && o.r.newTargetHeld != nil {
		if o.r.newTargetHeld(nt) {
			return TypeAny, true
		}
		return TypeUndefined, true
	}
	// A literal setting its prototype (`{ __proto__: null }`) builds as a
	// dynamic object (hasProtoKey), held as a box; under -compat=js every
	// bare literal does (emitExpr's routing), accessors included.
	if lit, isOL := e.(*ast.ObjectLiteral); isOL && (hasProtoKey(lit) || lit.HasAccessors() && objLitHasSpread(lit) || o.r.js != nil && o.r.js()) {
		return TypeAny, true
	}
	// A generator expression is a boxed constructor closure (mirrors
	// emitGeneratorExpressionValue).
	if fe, isFE := e.(*ast.FunctionExpression); isFE && fe.IsGenerator {
		return TypeAny, true
	}
	// Under -compat=js a sloppy function expression reading its own `this`
	// and `arguments` without taking `this` stays dynamic (mirrors
	// emitFunctionExpression).
	if fe, isFE := e.(*ast.FunctionExpression); isFE && o.r.js != nil && o.r.js() && !fe.IsAsync && fe.Body != nil && usesOwnThis(fe.Body.Body) && hasArgumentsRestParamSlice(maybeAddArgumentsRestParams(fe.Params, fe.Body.Body)) {
		if o.r.funcThis == nil {
			return TypeAny, true
		}
		if _, has := o.r.funcThis(fe); !has {
			return TypeAny, true
		}
	}
	// `[...s.matchAll(r)]`: the match arrays the iterator yields.
	if lit, isAL := e.(*ast.ArrayLiteral); isAL && len(lit.Elements) == 1 {
		if sp, isSp := lit.Elements[0].(*ast.SpreadElement); isSp {
			if it, ok := o.ExprType(sp.Arg); ok && it.IterRegExp && it.IterSrc != nil {
				return *it.IterSrc, true
			}
		}
	}
	if ne, isNew := e.(*ast.NewExpression); isNew && o.r.genericNew != nil {
		if gt, ok := o.r.genericNew(ne); ok {
			return gt, true
		}
	}
	// … and so does an unannotated binding of one.
	if id, isID := e.(*ast.Identifier); isID && o.r.genericNew != nil {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil {
				if ne, isNew := v.Init.(*ast.NewExpression); isNew {
					if gt, ok := o.r.genericNew(ne); ok && !o.hoistSkippable(v) {
						return gt, true
					}
				}
			}
		}
	}
	ty, ok := o.exprTypeBase(e)
	// A function literal whose result reprOf cannot name (a returned
	// generator object held as a box) returns what its body's returns
	// read as (ReturnType), its parameters mapped as usual.
	if !ok {
		if ft, fok := o.literalFromReturns(e); fok {
			ty, ok = ft, true
		}
	}
	// … and calling it in place returns that.
	if call, isCall := e.(*ast.CallExpression); isCall && !ok {
		if _, isFE := call.Callee.(*ast.FunctionExpression); isFE {
			if ft, fok := o.ExprType(call.Callee); fok && ft.IsFunc() && ft.FuncRetType != nil {
				ty, ok = *ft.FuncRetType, true
			}
		}
	}
	// Under -compat=js a binding of an unannotated function literal is the
	// function as emission compiles it, its parameters bound from their
	// call sites (literalParamType), not the checker's default-typed ones.
	if id, isID := e.(*ast.Identifier); isID && ok && ty.IsFunc() && o.r.js != nil && o.r.js() {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil {
				switch v.Init.(type) {
				case *ast.ArrowFunction, *ast.FunctionExpression:
					if ft, fok := o.ExprType(v.Init); fok && ft.IsFunc() {
						if ty.UncheckedIndex {
							ft = indexReadType(ft) // a skippable declaration's (exprTypeBase)
						}
						return ft, true
					}
				}
			}
		}
	}
	// A conditional the checker answers with a union no representation
	// holds (`string | Buffer`) joins its branches as emission does
	// (emitConditional): a nullable scalar, a box for a dynamic branch, a
	// scalar union, a nullable pointer, else a box for mixed kinds.
	if ce, isCE := e.(*ast.ConditionalExpression); isCE && !ok {
		ct, cok := o.ExprType(ce.Consequent)
		at, aok := o.ExprType(ce.Alternate)
		if cok && aok {
			if reprKey(ct) == reprKey(at) && ct.IR != "" {
				return ct, true // branches of one layout (objects of a tuple field)
			}
			if nty, nok := ternaryNullableScalarType(ct, at); nok {
				return nty, true
			}
			if ct.IsDynamic() || at.IsDynamic() {
				return TypeAny, true
			}
			if uTy, uok := ternaryUnion(ct, at); uok {
				return uTy, true
			}
			if nty, nok := ternaryNullablePointerType(ct, at); nok {
				return nty, true
			}
			if ternaryMixed(ct, at) {
				return TypeAny, true
			}
		}
	}
	// A function declaration the mapping does not represent (generic,
	// compiled once with its type parameters erased; overloaded, compiled
	// as its implementation) is that compiled function's value.
	if id, isID := e.(*ast.Identifier); isID && !ok && o.r.funcRef != nil {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) > 0 && sameFunctionDecls(sym.Declarations) {
			// (A generic one called directly is typed per call, never as a
			// value: erasing it there would ask this again, without end.)
			if fd, isFD := functionDeclOf(sym.Declarations); isFD && fd.Name == id.Name && !fd.IsGenerator && (len(fd.TypeParams) == 0 || !exprCallee(o.c.ParentOf(id), id)) {
				if ft, fok := o.r.funcRef(fd.Name); fok {
					return ft, true
				}
			}
		}
	}
	// A match result's own member the checker types by an index signature
	// (`m.groups`): the layout codegen reads it as (execMembers).
	if m, isM := e.(*ast.MemberExpression); isM && !ok {
		if rt, rok := o.ExprType(m.Object); rok && rt.ExecArray {
			if mt, _, has := execArrayMemberType(m.Property); has {
				return mt, true
			}
		}
	}
	// A member of a value that is only undefined or null (`v()` of a
	// function returning nothing) throws when read: a dynamic read.
	if m, isM := e.(*ast.MemberExpression); isM && !ok && !m.Optional {
		if rt, rok := o.ExprType(m.Object); rok && (rt.IR == "void" || rt.Kind == kindUndefined || rt.IsNull()) {
			return TypeAny, true
		}
	}
	// A rest parameter annotated with a tuple of several element kinds
	// (`...p: [string, number]`) collects its arguments boxed.
	if id, isID := e.(*ast.Identifier); isID && !ok {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if pm := declaredParam(sym, id.Name); pm != nil && pm.Rest && pm.Type != nil {
				if ct := o.c.StorageTypeOf(id); !o.c.Unanswered(ct) && ct.Flags&checker.Object != 0 && ct.Kind == checker.Tuple {
					return ArrayOf(TypeAny), true
				}
			}
		}
	}
	// A function declaration whose every return is its own name returns
	// itself boxed (returnsOwnName).
	if id, isID := e.(*ast.Identifier); isID && ok && ty.IsFunc() {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if fd, isFD := sym.Declarations[0].Node.(*ast.FunctionDeclaration); isFD && fd.ReturnType == nil && returnsOwnName(fd) {
				boxed := TypeAny
				ty.FuncRetType = &boxed
				return ty, true
			}
		}
	}
	// `super(…)` is the constructed instance, its class's `this`.
	if call, isCall := e.(*ast.CallExpression); isCall && o.r.class != nil {
		if _, isSuper := call.Callee.(*ast.SuperExpression); isSuper {
			for n := o.c.ParentOf(call); n != nil; n = o.c.ParentOf(n) {
				if cd, isCD := n.(*ast.ClassDeclaration); isCD {
					if sym, sok := o.b.DeclSymbol(cd); sok {
						if ct, cok := o.r.class(sym); cok && ct.IsClass() {
							return ct, true
						}
					}
					break
				}
			}
		}
	}
	// `super` in a subclass of Array is the receiver as an array of its
	// element (`super.filter(f)`).
	if sx, isSX := e.(*ast.SuperExpression); isSX && !ok {
		if t := o.c.TypeOf(sx); !o.c.Unanswered(t) && t.Flags&checker.Object != 0 && t.Symbol != nil && t.Symbol.Name == "Array" && o.r.libGlobal(t.Symbol) && len(t.TypeArgs) == 1 {
			if et, eok := o.r.of(t.TypeArgs[0]); eok {
				return ArrayOf(et), true
			}
		}
	}
	// An arrow returning `super(…)` (`const go = () => super(9)`) returns
	// the instance.
	if af, isAF := e.(*ast.ArrowFunction); isAF && !ok && af.Block == nil && len(af.Params) == 0 {
		body := af.Body
		if sq, isSeq := body.(*ast.SequenceExpression); isSeq {
			if st, sok := o.superSequence(sq); sok {
				return FuncType(nil, st), true
			}
		}
		if call, isCall := body.(*ast.CallExpression); isCall {
			if _, isSuper := call.Callee.(*ast.SuperExpression); isSuper {
				if st, sok := o.ExprType(call); sok {
					return FuncType(nil, st), true
				}
			}
		}
	}
	// An element callback on an array the checker leaves unanswered (an
	// evolving JavaScript array): it takes and returns boxes, map and
	// flatMap make an `any[]`, filter the receiver's kind.
	if !ok {
		if call, isCall := e.(*ast.CallExpression); isCall && boxedArrayElemCall(o, call) {
			m := call.Callee.(*ast.MemberExpression)
			if r, has := evolvingElemResult[m.Property]; has {
				if r {
					return o.ExprType(m.Object)
				}
				return ArrayOf(TypeAny), true
			}
		}
		var params []ast.Param
		switch f := e.(type) {
		case *ast.FunctionExpression:
			params = f.Params
		case *ast.ArrowFunction:
			params = f.Params
		default:
			params = nil
		}
		if call, isCall := o.c.ParentOf(e).(*ast.CallExpression); isCall && params != nil && len(call.Args) > 0 && call.Args[0] == e && boxedArrayElemCall(o, call) {
			pts := make([]Type, len(params))
			for i := range pts {
				pts[i] = TypeAny
			}
			return FuncType(pts, TypeAny), true
		}
	}
	// A function member of a library global held as a box, no intrinsic
	// implementing it (`Promise.try` read as a value): a read off that box.
	if m, isM := e.(*ast.MemberExpression); isM && !ok && !m.Optional && !exprCallee(o.c.ParentOf(m), m) && o.libGlobalRef(m.Object) {
		if bt, bok := o.ExprType(m.Object); bok && isUnconstrainedDynamic(bt) {
			if ft := o.c.TypeOf(m); !o.c.Unanswered(ft) && ft.Flags&checker.Object != 0 && ft.Kind == checker.Function {
				if o.r.builtinRef == nil {
					return TypeAny, true
				}
				if _, isRef := o.r.builtinRef(m); !isRef {
					return TypeAny, true
				}
			}
		}
	}
	// A binding the checker leaves unanswered (`var label = opts && opts.label
	// || String(name)` over an untyped read) reads what its storage holds,
	// as emission reads a bound name.
	if id, isID := e.(*ast.Identifier); isID && !ok && o.r.heldIdent != nil {
		if ht, hok := o.r.heldIdent(id); hok {
			return ht, true
		}
	}
	// … and an unannotated binding stored once, by its initializer, holds
	// what that initializer is held as: a read the checker narrows past an
	// answer (`ret instanceof Error ? ret : …` over `boolean | Error`; a
	// branch where the binding narrows to never) reads that storage.
	if id, isID := e.(*ast.Identifier); isID && !ok {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil && v.Init != nil && v.Name == id.Name && len(o.b.StoresTo(sym)) == 1 {
				if it, iok := o.ExprType(v.Init); iok && it.IR != "" && it.IR != "void" {
					return it, true
				}
			}
			// A `var` declared again, each time initialized and stored
			// nowhere else: the first declaration's global, which every
			// initializer stores alike.
			if it, iok := o.redeclaredVarHeld(sym, id); iok {
				return it, true
			}
		}
	}
	// Under -compat=js a member read the checker leaves unanswered (`opts.label`
	// on a JSDoc `{object}`, tsc's error type) on a receiver held as a box
	// reads a box (the dynamic property read).
	if recv := dynamicReadReceiver(e); recv != nil && !ok && o.r.js != nil && o.r.js() {
		if rt, rok := o.heldOperand(recv); rok && isUnconstrainedDynamic(rt) {
			return TypeAny, true // a member or element read (`this[0]` in a function)
		}
	}
	// A call the checker leaves unanswered through a binding held as a
	// function returns what that function's held signature returns, as
	// emission calls it (`let f = () => o` with o's literal untyped).
	if call, isCall := e.(*ast.CallExpression); isCall && !ok && !call.Optional && o.r.heldIdent != nil {
		if id, isID := call.Callee.(*ast.Identifier); isID {
			if ht, hok := o.r.heldIdent(id); hok && ht.IsFunc() && !ht.IsDynamic() && ht.FuncRetType != nil && ht.FuncRetType.IR != "void" {
				return *ht.FuncRetType, true
			}
		}
	}
	// A call of a declared function whose annotated result the checker
	// answers with a union no inferred representation holds (`boolean |
	// Error`) returns the annotation's representation, as emission
	// registers the function's result (annotationType).
	if call, isCall := e.(*ast.CallExpression); isCall && !ok && !call.Optional && o.r.annotation != nil {
		if id, isID := call.Callee.(*ast.Identifier); isID {
			if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
				if fd, isFD := sym.Declarations[0].Node.(*ast.FunctionDeclaration); isFD && fd.ReturnType != nil && len(fd.TypeParams) == 0 && len(fd.Overloads) == 0 && !fd.IsAsync && !fd.IsGenerator {
					if ct := o.c.TypeOf(call); !o.c.Unanswered(ct) && ct.Flags&checker.Union != 0 {
						if rt := o.r.annotation(fd.ReturnType); rt.IR != "" && rt.IR != "void" {
							return rt, true
						}
					}
				}
			}
		}
	}
	// An undeclared name's read throws its ReferenceError (or reads the
	// global object's property); the value is a box (emitNotDefined,
	// emitGlobalPropertyRead).
	if id, isID := e.(*ast.Identifier); isID && !ok && o.r.unresolved != nil && o.r.unresolved(id) {
		return TypeAny, true
	}
	// An empty literal the checker types `never[]` is codegen's default
	// `number[]` (inferArrayTypeOf); an annotated binding's slot takes its
	// declared type (declaredArrayType).
	empty := e
	if aw, isAw := e.(*ast.AwaitExpression); isAw {
		empty = aw.Argument // `await []`: the array itself
	}
	if lit, isAL := empty.(*ast.ArrayLiteral); isAL && !ok && len(lit.Elements) == 0 {
		ty, ok = ArrayOf(TypeF64), true
	}
	// A field holding tuples (`{ h: [["k", "v"]] as [string, string][] }`)
	// reads as the layout its object holds, the tuples headerless structs.
	if m, isM := e.(*ast.MemberExpression); isM && ok && !m.Optional && hasTuple(o.c.TypeOf(e)) {
		ot, ook := o.ExprType(m.Object)
		if id, isID := m.Object.(*ast.Identifier); isID && o.r.heldIdent != nil {
			if ht, hok := o.r.heldIdent(id); hok {
				ot, ook = ht, true // the layout its binding holds
			}
		}
		if ook && ot.IsObject() && !ot.IsDynamic() {
			for _, f := range ot.Fields {
				if f.Name == m.Property && (reprHasTuple(f.Ty) || ot.IsClass() && !f.Ty.IsTuple()) {
					// (A class instance's field, whatever the checker's
					// instance spells: an erased one's holds no tuple.)
					ty = f.Ty
					break
				}
			}
		}
	}
	// An assertion naming a tuple (`[["k", "v"]] as [string, string][]`)
	// is the headerless struct its annotation declares (annotationType), not
	// the array an inferred tuple of one element kind is.
	if as, isAs := e.(*ast.AsExpression); isAs && ok && as.TypeAnnot != nil && typeNodeHasTuple(as.TypeAnnot) {
		if at, aok := o.AnnotationType(as.TypeAnnot); aok {
			ty = at
		}
	}
	// An assertion the checker rejects (`input as DataView` of a string)
	// is held as the asserted type (emitAsExpression).
	if as, isAs := e.(*ast.AsExpression); isAs && !ok && as.TypeAnnot != nil {
		if ty, ok = o.AnnotationType(as.TypeAnnot); !ok && o.r.annotation != nil {
			ty, ok = o.r.annotation(as.TypeAnnot), true
		}
	}
	// An unannotated binding of a call only codegen can type (a
	// generator's instance, `var it = gen()`, and its step,
	// `var r = it.next()`) holds codegen's layout.
	if id, isID := e.(*ast.Identifier); isID && !ok && o.r.codegenCall != nil {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil {
				if call, isCall := v.Init.(*ast.CallExpression); isCall {
					if ct, cok := o.r.codegenCall(call); cok {
						ty, ok = ct, true
					}
				}
			}
		}
	}
	// Otherwise an unannotated binding no other kind is assigned into holds
	// its initializer's representation (`var gen = C.gen`).
	if id, isID := e.(*ast.Identifier); isID && !ok {
		if sym, _ := o.b.Resolve(id); sym != nil && (o.r.widened == nil || !o.r.widened(id.Name)) {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil && v.Init != nil {
				if it, iok := o.ExprType(v.Init); iok && (it.IsFunc() || it.IsGenerator()) {
					ty, ok = it, true
				}
			}
		}
	}
	// A field of a layout only codegen names (a generator step's
	// `{value, done}`, an object literal's with a boxed generator method or
	// a method the checker types by a tuple): the field's type.
	if m, isM := e.(*ast.MemberExpression); isM && !ok && !m.Optional {
		if rt, rok := o.ExprType(m.Object); rok && rt.IsObject() && !rt.IsClass() && !rt.IsDynamic() && !rt.Nullable() {
			if _, ft, has := rt.FieldIndex(m.Property); has {
				ty, ok = ft, true
			}
		}
	}
	// A class's static member where the checker has no type for it (a
	// static generator method read as a value, an untyped static field):
	// as codegen registered it. The class is named, bound to a class
	// expression, or a static member's `this` (staticThisToClass).
	if m, isM := e.(*ast.MemberExpression); isM && !ok && !m.Optional && o.r.staticMethod != nil {
		if id, isID := m.Object.(*ast.Identifier); isID && o.namesClass(id) {
			if t, sok := o.r.staticMethod(id.Name, m.Property); sok {
				ty, ok = t, true
			}
		}
	}
	// A call of a binding holding a generator's value (`var gen = C.gen;
	// gen()`): its instance.
	if c, isC := e.(*ast.CallExpression); isC && !ok && !c.Optional {
		if id, isID := c.Callee.(*ast.Identifier); isID {
			if ft, fok := o.ExprType(id); fok && isGeneratorCtor(ft) {
				ty, ok = *ft.FuncRetType, true
			}
		}
	}
	// A call of a generator's value held in such a field: its instance.
	if c, isC := e.(*ast.CallExpression); isC && !ok && !c.Optional {
		if m, isM := c.Callee.(*ast.MemberExpression); isM && !m.Optional {
			if ft, fok := o.ExprType(m); fok && isGeneratorCtor(ft) {
				ty, ok = *ft.FuncRetType, true
			}
		}
	}
	// An unannotated binding of a value codegen holds boxed (dynamicInit)
	// reads as that box.
	if id, isID := e.(*ast.Identifier); isID && ok && !ty.IsDynamic() {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil && v.Init != nil && o.dynamicInit(v) {
				if it, iok := o.ExprType(v.Init); iok && isUnconstrainedDynamic(it) {
					ty, _ = o.boxedInitRead(id, v)
				}
			}
		}
	}
	// An arithmetic operator with an operand codegen holds as a box is the
	// runtime dispatch's box (typeOfExpr's dynamic-operand rule).
	if b, isB := e.(*ast.BinaryExpression); isB && ok && !ty.IsDynamic() {
		if dt, dok := o.dynamicBinary(b); dok {
			ty = dt
		}
	}
	// Under -compat=js an arithmetic operand held null or undefined goes
	// through the runtime dispatch (null→0, undefined→NaN): a box, unless
	// a real string side concatenates (emitBinary, ADR-00901).
	if b, isB := e.(*ast.BinaryExpression); isB && ok && arithOps[b.Op] && !ty.IsDynamic() && o.r.js != nil && o.r.js() {
		lt, lok := o.heldOperand(b.Left)
		rt, rok := o.heldOperand(b.Right)
		if lok && rok {
			lN := (lt.IsNull() || lt.IsUndefined()) && !lt.UncheckedIndex
			rN := (rt.IsNull() || rt.IsUndefined()) && !rt.UncheckedIndex
			if (lN || rN) && !(isStringTy(lt) && !lN) && !(isStringTy(rt) && !rN) && !lt.IsObject() && !rt.IsObject() && !lt.IsArray() && !rt.IsArray() && !lt.IsBigInt() && !rt.IsBigInt() {
				ty = TypeAny
			}
		}
	}
	// `+` with a string literal operand the checker leaves unanswered
	// (`label + " getter"` over an untyped binding) is a string: the other
	// operand is converted and concatenated (emitBinary's string `+`).
	if b, isB := e.(*ast.BinaryExpression); isB && !ok && b.Op == "+" && (isStringLitNode(b.Left) || isStringLitNode(b.Right)) {
		return TypePtr, true
	}
	// `+` the checker types `any` over operands codegen holds concrete (a
	// replace callback's capture groups, held as strings) concatenates them
	// when either is a string.
	if b, isB := e.(*ast.BinaryExpression); isB && ok && b.Op == "+" && isUnconstrainedDynamic(ty) {
		lt, lok := o.heldOperand(b.Left)
		rt, rok := o.heldOperand(b.Right)
		if lok && rok && !lt.IsDynamic() && !rt.IsDynamic() && (isStringTy(lt) || isStringTy(rt)) {
			ty = TypePtr
		}
	}
	// A literal of mixed elements is held as emission joins them: a union
	// of their kinds, or boxed elements when they do not form one
	// (mixedArrayElem, mixedKindsBoxed).
	// A literal the checker leaves unanswered that initializes an annotated
	// binding is built as the declared type (declaredArrayType):
	// `const CI_ENVS: [string, number][] = [['APPVEYOR', COLORS_256], …]`.
	if lit, isAL := e.(*ast.ArrayLiteral); isAL && !ok {
		if v, isV := o.c.ParentOf(lit).(*ast.VarDeclaration); isV && v.Init == ast.Expression(lit) && v.TypeAnnot != nil {
			if dt, dok := o.AnnotationType(v.TypeAnnot); dok && dt.IsArray() {
				ty, ok = dt, true
			}
		}
	}
	// One of spreads only (`[...pairs]`, over declared tuples whose
	// inferred mix has no layout of its own) holds what the spread arrays
	// hold, when they agree.
	if lit, isAL := e.(*ast.ArrayLiteral); isAL && !ok && len(lit.Elements) > 0 {
		var held *Type
		agree := true
		for _, el := range lit.Elements {
			sp, isSp := el.(*ast.SpreadElement)
			if !isSp {
				agree = false
				break
			}
			at, aok := o.ExprType(sp.Arg)
			var et Type
			switch {
			case !aok:
			case at.IsArray() && at.ElemType != nil:
				et = *at.ElemType
			case mapIterable(at) && at.IsSet() && at.MapKey != nil:
				et = *at.MapKey
			case at.IsCollIter():
				et = collIterElemType(at)
			}
			if !et.IsTuple() || et.IsDynamic() || held != nil && !sameFieldLayout(*held, et) {
				agree = false
				break
			}
			held = &et
		}
		if agree && held != nil {
			ty, ok = ArrayOf(*held), true
		}
	}
	// One the checker leaves unanswered (`[0, 0n, true, "s", {}, Symbol()]`)
	// is joined the same way from its elements' answers.
	if lit, isAL := e.(*ast.ArrayLiteral); isAL && (ok && ty.IsArray() && ty.ElemType != nil && ty.ElemType.IsDynamic() && len(ty.ElemType.UnionMembers) > 0 || !ok && len(lit.Elements) > 0) {
		answered := true
		// A promise that only rejects (`Promise.reject(e)`, `Promise<never>`)
		// takes the layout of a sibling promise, as the literal holds it.
		var sibling *Type
		for _, el := range lit.Elements {
			if t, tok := o.ExprType(el); tok && t.IsPromise() && !t.IsDynamic() && t.PromiseType != nil && !t.PromiseType.IsNever() {
				sibling = &t
				break
			}
		}
		typeOf := func(x ast.Expression) Type {
			t, tok := o.ExprType(x)
			answered = answered && tok
			if sibling != nil && t.IsPromise() && t.PromiseType != nil && t.PromiseType.IsNever() {
				return *sibling
			}
			return t
		}
		// Generators spread (`[...h()]`) whose yields the checker cannot type
		// and codegen holds boxed: the array of those boxes.
		if !ok {
			gens := true
			for _, el := range lit.Elements {
				sp, isSp := el.(*ast.SpreadElement)
				if !isSp {
					gens = false
					break
				}
				if at, aok := o.ExprType(sp.Arg); !aok || !at.IsGenerator() || at.GeneratorElemType == nil || !at.GeneratorElemType.IsDynamic() {
					gens = false
					break
				}
			}
			if gens {
				return ArrayOf(TypeAny), true
			}
		}
		u, mixed := mixedArrayElem(lit, typeOf)
		boxed := !mixed && mixedKindsBoxed(lit, typeOf)
		switch {
		case !answered:
		case !ok && !mixed && !boxed:
			// Elements of one representation whose type the checker gives no
			// layout for (generator objects, settled promises): the array of
			// what emission builds of them.
			var et Type
			same := true
			for i, el := range lit.Elements {
				if _, isSp := el.(*ast.SpreadElement); isSp {
					same = false
					break
				}
				t := typeOf(el)
				if t.IsDynamic() || i > 0 && reprKey(t) != reprKey(et) {
					same = false
					break
				}
				et = t
			}
			if same {
				ty, ok = ArrayOf(et), true
			}
		case mixed:
			ty, ok = ArrayOf(u), true
		case boxed:
			ty, ok = ArrayOf(TypeAny), true
		}
	}
	// An array of object literals with methods holds them as their
	// literals are built: each method takes its `this` (FuncThis).
	if lit, isAL := e.(*ast.ArrayLiteral); isAL && ok && len(lit.Elements) > 0 && ty.ElemType != nil && ty.ElemType.IsObject() {
		var et Type
		same := true
		for i, el := range lit.Elements {
			ol, isOL := el.(*ast.ObjectLiteral)
			if !isOL || !literalHasMethod(ol) {
				same = false
				break
			}
			t, tok := o.ExprType(ol)
			if !tok || i > 0 && reprKey(t) != reprKey(et) {
				same = false
				break
			}
			et = t
		}
		if same {
			ty = ArrayOf(et)
		}
	}
	// A literal asserted to an array of tuples (`[[1, "x"]] as [number,
	// string][]`) is built in that shape, its element literals as the
	// tuples (emitAsExpression).
	if lit, isAL := e.(*ast.ArrayLiteral); isAL && ok {
		var as *ast.AsExpression
		var inner bool
		switch p := o.c.ParentOf(lit).(type) {
		case *ast.AsExpression:
			as = p
		case *ast.ArrayLiteral:
			if pa, isAs := o.c.ParentOf(p).(*ast.AsExpression); isAs {
				as, inner = pa, true
			}
		}
		if as != nil && as.TypeAnnot != nil {
			if at, aok := o.AnnotationType(as.TypeAnnot); aok && at.IsArray() && at.ElemType != nil && at.ElemType.IsTuple() {
				if inner {
					ty = *at.ElemType
				} else {
					ty = at
				}
			}
		}
	}
	// A spread Map contributes its entries as `[key, value]` tuples, as
	// emission builds them; the checker's tuple type says so.
	if lit, isAL := e.(*ast.ArrayLiteral); isAL && ok && ty.IsArray() {
		for _, el := range lit.Elements {
			if sp, isSp := el.(*ast.SpreadElement); isSp {
				if st, sok := o.ExprType(sp.Arg); sok && st.IsMap() && !st.IsDynamic() {
					r := o.r
					r.tuples = true
					if tt, tok := r.of(o.c.TypeOf(lit)); tok && tt.IsArray() && tt.ElemType != nil && tt.ElemType.IsTuple() {
						ty = tt
					}
					break
				}
			}
		}
	}
	// A one-element literal is an array of that element as held
	// (inferArrayTypeOf), whatever the checker narrows it to.
	if lit, isAL := e.(*ast.ArrayLiteral); isAL && ok && len(lit.Elements) == 1 {
		el := lit.Elements[0]
		_, isSp := el.(*ast.SpreadElement)
		_, isLit := el.(*ast.ArrayLiteral)
		if et, eok := o.ExprType(el); eok && !isSp && !isLit && !et.IsDynamic() && !et.IsFunc() && !et.IsArray() && !nullishLiteralType(et) && et.IR != "" && et.IR != "void" && (et.IsClass() || et.IsObject()) {
			ty = ArrayOf(et)
		}
	}
	// A read of a variable whose only store is a builtin function
	// (`var dp = Object.defineProperty; dp(o, k, d)`): that closure.
	if id, isID := e.(*ast.Identifier); isID && o.r.builtinRef != nil {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, single := singleVarDecl(sym); single && v.TypeAnnot == nil && v.Init != nil && len(o.b.StoresTo(sym)) <= 1 {
				init := v.Init
				if ix, isIx := init.(*ast.IndexExpression); isIx && !ix.Optional && o.libGlobalRef(ix.Object) {
					if key, isStr := ix.Index.(*ast.StringLiteral); isStr {
						init = ast.NewMemberExpression(ix.Object, key.Value, ix.GetPos()) // `Date["UTC"]`
					}
				}
				if bt, bok := o.r.builtinRef(init); bok {
					ty, ok = bt, true
				}
			}
		}
	}
	// A loop variable over a boxed-element array (`for (var r of [0, 0n,
	// true])`, whose union of element kinds has no representation) is a
	// box, as the loop holds each element.
	if id, isID := e.(*ast.Identifier); isID && !ok {
		if it, iok := o.forOfIterableType(id); iok {
			if it.IsArray() && it.ElemType != nil && it.ElemType.IsDynamic() {
				ty, ok = TypeAny, true
			}
		}
	}
	// A leaf of an unannotated array-pattern parameter whose default types it
	// as a tuple of optional elements (`[[...x] = v] = []`) is unpacked from
	// the boxes that parameter holds (an array of any). The js lane's
	// parameter is a box itself, already answered.
	if id, isID := e.(*ast.Identifier); isID && (o.r.js == nil || !o.r.js()) {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if p, _ := patternParamLeaf(sym, id.Name); p != nil && p.Type == nil && o.anyBound[sym.Declarations[0].Node] {
				ty, ok = TypeAny, true // a function literal bound to any takes boxes
			} else if p, el := patternParamLeaf(sym, id.Name); p != nil && p.Type == nil {
				pt := o.c.ImpliedPatternType(*p) // no default: the pattern's own type
				if p.Default != nil {
					pt = o.c.PatternDefaultType(*p)
				}
				// A leaf with its own default (`[p = 7] = []`) takes that
				// default's type once unpacked, as the checker types it.
				if pr, pok := o.r.of(pt); pok && pr.IsArray() && pr.ElemType != nil && pr.ElemType.IsDynamic() && el.Default == nil && (p.Default != nil || el.Rest && nestedInDefaultedElem(p.ArrayPattern, el)) {
					if el.Rest {
						ty, ok = ArrayOf(TypeAny), true
					} else {
						ty, ok = TypeAny, true
					}
				}
			}
		}
	}
	// A loop variable the checker types as a union over a static array
	// (`PromiseSettledResult` of an allSettled result) is held as the
	// array's element, as the loop stores it.
	if id, isID := e.(*ast.Identifier); isID && ok && ty.IsDynamic() && len(ty.UnionMembers) > 0 {
		if it, iok := o.forOfIterableType(id); iok && it.IsArray() && !it.IsDynamic() && it.ElemType != nil && !it.ElemType.IsDynamic() && !it.BigIntElem {
			ty = *it.ElemType
		}
	}
	// A Map's entries are its [key, value] tuples, of the kinds the loop's
	// map holds (a js-lane `new Map()` its sets decide).
	if id, isID := e.(*ast.Identifier); isID {
		if it, iok := o.forOfIterableType(id); iok {
			if it.Host == hostMap && !it.Weak && it.MapKey != nil && it.MapVal != nil {
				ty, ok = TupleType([]Type{*it.MapKey, *it.MapVal}), true
			}
		}
		// `const [e] = m`: an array pattern's direct element over a Map is
		// one of its entries, the same tuple.
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if d, isAD := sym.Declarations[0].Node.(*ast.ArrayDestructuring); isAD {
				if el := findArrayPatternElem(d.Elems, id.Name); el != nil && !el.Rest && directArrayPatternElem(d.Elems, el) {
					if it, iok := o.ExprType(d.Init); iok && it.Host == hostMap && !it.Weak && it.MapKey != nil && it.MapVal != nil {
						ty, ok = TupleType([]Type{*it.MapKey, *it.MapVal}), true
					}
				}
			}
		}
	}
	// An object literal's method returning its own `this` (an iterator's
	// `[Symbol.iterator]() { return this; }`): tsc's recursive literal
	// type the checker does not build; the result is a box, as emission
	// holds that `this`.
	if fe, isFE := e.(*ast.FunctionExpression); isFE && !ok && returnsOnlyThis(fe) {
		if _, inLit := o.c.ParentOf(fe).(*ast.ObjectLiteral); inLit {
			ty, ok = FuncType(nil, TypeAny), true
		}
	}
	// `new K()` of a `const K = class {…}` binding (inside its own static
	// method too, where the checker's class is still open): the class's
	// instance, as codegen declares it.
	if ne, isNE := e.(*ast.NewExpression); isNE && !ok && o.r.class != nil && ne.Callee == nil && !ne.Qualified {
		{
			if sym := o.b.NewTarget(ne); sym != nil && len(sym.Declarations) == 1 && !o.b.StoredElsewhere(sym) {
				if v, isV := sym.Declarations[0].Node.(*ast.VarDeclaration); isV && v.Kind == "const" {
					// Hoisting leaves the binding a reference to the class.
					if ref, isRef := v.Init.(*ast.Identifier); isRef && strings.Contains(ref.Name, "__kml_cx") {
						if o.r.annotation != nil {
							if ct := o.r.annotation(nameAnnotation(ref.Name)); ct.IsClass() && ct.ClassName == ref.Name {
								ty, ok = ct, true
							}
						}
					}
				}
			}
		}
	}
	// A property of a `this` codegen binds as a plain box (a static method
	// of a class expression a factory makes per call, whose statics live on
	// each class object): the dynamic read.
	if m, isM := e.(*ast.MemberExpression); isM && !m.Optional {
		if th, isThis := m.Object.(*ast.ThisExpression); isThis && o.r.inferred != nil && o.staticOfClassExpr(th) {
			if tt, tok := o.r.inferred(th); tok && isUnconstrainedDynamic(tt) && tt.DynPropTy == nil && len(tt.UnionMembers) == 0 {
				return TypeAny, true
			}
		}
	}
	// A spread literal whose type has no representation (optional members
	// of a Map, a Date or a tuple, nullable): the dynamic object codegen
	// builds.
	if lit, isLit := e.(*ast.ObjectLiteral); isLit && !ok && objLitHasSpread(lit) {
		if lt := o.c.TypeOf(lit); !o.c.Unanswered(lt) {
			if _, mok := o.r.of(lt); !mok {
				ty, ok = TypeAny, true
			}
		}
	}
	// A property only one member of the receiver's union has (`server.
	// address().port` off `string | AddressInfo | null`, a js-lane read):
	// that member's property, as codegen reads it.
	if m, isM := e.(*ast.MemberExpression); isM && !ok && !m.Optional && o.c.Unanswered(o.c.TypeOf(m)) {
		if rt := o.c.TypeOf(m.Object); !o.c.Unanswered(rt) && rt.Flags&checker.Union != 0 {
			var only *checker.Property
			n := 0
			for _, u := range rt.Types {
				if u.Flags&checker.Object != 0 {
					if p := u.Prop(m.Property); p != nil {
						only = p
						n++
					}
				}
			}
			if n == 1 && only.Type != nil && !o.c.Unanswered(only.Type) {
				if pt, pok := o.r.of(only.Type); pok && !pt.IsDynamic() && pt.IR != irPtr {
					ty, ok = pt, true
				}
			}
		}
	}
	// `new` of a class value the checker cannot follow (a class a call
	// returns, `new c.B()` of a class-valued field): constructed through the
	// dynamic path, a box.
	if ne, isNE := e.(*ast.NewExpression); isNE && !ok && o.c.Unanswered(o.c.TypeOf(ne)) && len(ne.TypeArgs) == 0 {
		if m, isM := ne.Callee.(*ast.MemberExpression); isM && !m.Optional {
			if ft, fok := o.ExprType(m); fok && ft.IsDynamic() {
				ty, ok = TypeAny, true
			}
		} else if qc, isQ := o.b.QualifiedCallee(ne).(*ast.MemberExpression); isQ && ne.Callee == nil {
			// `new c.B()`: a class held in a field of an instance.
			if id, isID := qc.Object.(*ast.Identifier); isID {
				if sym, _ := o.b.Resolve(id); sym != nil && sym.Flags&binder.Variable != 0 && len(sym.Declarations) == 1 {
					if v, isV := sym.Declarations[0].Node.(*ast.VarDeclaration); isV {
						if _, isNew := v.Init.(*ast.NewExpression); isNew {
							ty, ok = TypeAny, true
						}
					}
				}
			}
		} else if ne.Callee == nil && !ne.Qualified {
			if o.r.js != nil && o.r.js() && o.b.NewTarget(ne) == nil && o.b.LookupScope(ne) != nil {
				// A name no scope declares, constructed in JavaScript
				// through the global property: a box.
				ty, ok = TypeAny, true
			}
			if sym := o.b.NewTarget(ne); sym != nil && len(sym.Declarations) == 1 {
				if v, isV := sym.Declarations[0].Node.(*ast.VarDeclaration); isV && v.TypeAnnot == nil {
					switch v.Init.(type) {
					case *ast.CallExpression, *ast.MemberExpression: // `const E = events.EventEmitter`
						ty, ok = TypeAny, true
					}
				}
			}
		}
	}
	// A call of a literal's `return this` method (`o.self()`, and a call
	// on its result), which the checker leaves unanswered: the box the
	// method returns.
	if call, isC := e.(*ast.CallExpression); isC && !ok && !call.Optional {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && !m.Optional && o.selfReturningCall(m) {
			ty, ok = TypeAny, true
		}
	}
	// Any literal whose checker type has no representation (a property of
	// an unmappable union) is its fields' representations, as
	// inferObjectType builds it.
	if lit, isLit := e.(*ast.ObjectLiteral); isLit && !ok && (o.r.js == nil || !o.r.js()) && !lit.HasAccessors() && !objLitHasSpread(lit) && !hasProtoKey(lit) {
		ty, ok = o.fieldsLiteral(lit)
	}
	// A function expression taking a `this` (declared, or a method
	// shorthand reading its own) takes it as its first parameter
	// (maybeAddThisParamExpr), which the checker keeps apart.
	if fe, isFE := e.(*ast.FunctionExpression); isFE && ok && ty.IsFunc() && o.r.funcThis != nil {
		if tt, has := o.r.funcThis(fe); has {
			user := 0
			for _, p := range fe.Params {
				if p.Name != "this" {
					user++
				}
			}
			if len(ty.FuncParams) == user {
				ty.FuncParams = append([]Type{tt}, ty.FuncParams...)
				if len(ty.FuncParamOptional) == user {
					ty.FuncParamOptional = append([]bool{false}, ty.FuncParamOptional...)
				}
				ty.FuncParamNames = nil
			}
			ty.FuncThis = true
		}
	}
	// An unannotated function returning a literal with an accessor returns
	// that literal's synthetic class, as its return is inferred from the
	// value (funcBodyReturnType over inferObjectType).
	if fe, isFE := e.(*ast.FunctionExpression); isFE && ok && ty.IsFunc() && fe.RetType == nil && !fe.IsAsync && !fe.IsGenerator && fe.Body != nil {
		if lit, isLit := firstReturnExpr(fe.Body).(*ast.ObjectLiteral); isLit && lit.HasAccessors() {
			if rt, rok := o.ExprType(lit); rok && rt.IsClass() {
				ty.FuncRetType = &rt
			}
		}
	}
	// Under -compat=js a function literal's unannotated parameter is bound
	// as emission binds it (its default's type, its call sites', else a
	// box), not by the checker's contextual type.
	if ok && ty.IsFunc() && o.r.js != nil && o.r.js() && o.r.literalParam != nil {
		var params []ast.Param
		switch fn := e.(type) {
		case *ast.FunctionExpression:
			if !fn.IsGenerator {
				params = fn.Params
			}
		case *ast.ArrowFunction:
			params = fn.Params
		}
		off := 0
		if ty.FuncThis && (len(params) == 0 || params[0].Name != "this") {
			off = 1 // the `this` the oracle prepended
		}
		var ps []Type
		for i := range params {
			k := i + off
			if k >= len(ty.FuncParams) {
				break
			}
			if pt, pok := o.r.literalParam(e, i); pok && reprKey(pt) != reprKey(ty.FuncParams[k]) {
				if ps == nil {
					ps = append([]Type(nil), ty.FuncParams...)
				}
				ps[k] = pt
			}
		}
		if ps != nil {
			ty.FuncParams = ps
		}
	}
	// A function literal reading `arguments` carries the synthetic rest
	// parameter emission appends for it (maybeAddArgumentsRestParams),
	// which the checker's signature leaves out; a call stores its count.
	// So does a declared function a name reads, once emission has given it
	// that parameter (a sloppy function where a program reads `.arguments`
	// or `.caller`, callerprops.go).
	var declParams []ast.Param
	if id, isID := e.(*ast.Identifier); isID && ok && ty.IsFunc() {
		if sym, _ := o.b.Resolve(id); sym != nil {
			// One function declaration (with any namespace merged into it).
			var only *ast.FunctionDeclaration
			n := 0
			for _, d := range sym.Declarations {
				if fd, isFD := d.Node.(*ast.FunctionDeclaration); isFD {
					only, n = fd, n+1
				}
			}
			if n == 1 && only.Name == id.Name && hasArgumentsRestParam(only) {
				declParams = only.Params
			}
			// A `var` bound to a function literal reading `arguments`
			// that is never reassigned holds that literal.
			if v, isV := singleVarDecl(sym); n == 0 && isV && v.TypeAnnot == nil && !o.reassigned(sym) {
				if fe, isFE := v.Init.(*ast.FunctionExpression); isFE && fe.Body != nil {
					if ps := maybeAddArgumentsRestParams(fe.Params, fe.Body.Body); hasArgumentsRestParamSlice(ps) {
						declParams = ps
					}
				}
			}
		}
	}
	if fn, isFE := e.(*ast.FunctionExpression); declParams != nil || isFE && ok && ty.IsFunc() && fn.Body != nil {
		params := declParams
		if params == nil {
			params = maybeAddArgumentsRestParams(fn.Params, fn.Body.Body)
		}
		off := 0
		if ty.FuncThis {
			off = 1
		}
		if hasArgumentsRestParamSlice(params) && len(ty.FuncParams) == len(params)-1+off {
			ty.FuncParams = append(append([]Type(nil), ty.FuncParams...), ArrayOf(TypeAny))
			ty.FuncHasRest = true
			if ty.FuncParamNames != nil {
				ty.FuncParamNames = append(append([]string(nil), ty.FuncParamNames...), argumentsRestParam)
			}
			if ty.FuncParamOptional != nil {
				ty.FuncParamOptional = append(append([]bool(nil), ty.FuncParamOptional...), false)
			}
			ty.FuncCountsArgs = true
		}
	}
	// An immediately invoked function's unannotated parameter is typed by
	// its argument (tsc's contextual IIFE parameter): a box where the
	// argument is one, as an object literal spreading a box is.
	if fn, isFE := e.(*ast.FunctionExpression); isFE && ok && ty.IsFunc() && !ty.FuncThis {
		if call, isCall := o.c.ParentOf(fn).(*ast.CallExpression); isCall && call.Callee == ast.Expression(fn) {
			var ps []Type
			for i, p := range fn.Params {
				if i >= len(call.Args) || i >= len(ty.FuncParams) || p.Type != nil || p.Default != nil || p.Rest || p.ArrayPattern != nil || p.ObjectPattern != nil {
					continue
				}
				if _, spread := call.Args[i].(*ast.SpreadElement); spread {
					break
				}
				if at, aok := o.ExprType(call.Args[i]); aok && (isUnconstrainedDynamic(at)) && !ty.FuncParams[i].IsDynamic() {
					if ps == nil {
						ps = append([]Type(nil), ty.FuncParams...)
					}
					ps[i] = TypeAny
				}
			}
			if ps != nil {
				ty.FuncParams = ps
			}
		}
	}
	// An annotated return is the annotation as emission resolves it: a
	// declared tuple is a positional struct (TupleType), not the array the
	// checker's tuple maps to (typeOfExpr's annotationType).
	if ok && ty.IsFunc() && o.r.annotation != nil {
		var rt ast.TypeNode
		async := false
		switch fn := e.(type) {
		case *ast.ArrowFunction:
			rt, async = fn.RetType, fn.IsAsync
		case *ast.FunctionExpression:
			if !fn.IsGenerator {
				rt, async = fn.RetType, fn.IsAsync
			}
		}
		if rt != nil {
			cur := TypeVoid
			if ty.FuncRetType != nil {
				cur = *ty.FuncRetType
			}
			if at := o.r.annotation(rt); at.IR != "" {
				if want := asyncClosureRetType(async, at); reprKey(want) != reprKey(cur) {
					ty.FuncRetType = &want
				}
			}
		}
	}
	// A destructured parameter's default is filled by the caller
	// (callSiteDefault): the function never takes it absent.
	if ok && ty.IsFunc() {
		if params := o.declParams(e); params != nil {
			ty = o.callerDefaultParams(ty, params)
		}
	}
	// A parameter default the caller evaluates marks the function's
	// default mask, as emission sets it (setFuncParamDefaults).
	if ok && ty.IsFunc() {
		lit := e
		if id, isID := e.(*ast.Identifier); isID {
			// A binding of a literal holds the literal's value.
			if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
				if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil && (v.Kind == "const" || !o.reassigned(sym)) {
					lit = v.Init
				}
			}
		}
		switch fn := lit.(type) {
		case *ast.FunctionExpression:
			if !fn.IsGenerator {
				setFuncParamDefaults(&ty, fn.Params)
			}
		case *ast.ArrowFunction:
			setFuncParamDefaults(&ty, fn.Params)
		}
	}
	// An empty array literal's `never` element is codegen's default number
	// (inferArrayType), nested or not; awaited, it is the same array.
	lit := e
	if id, isID := lit.(*ast.Identifier); isID && ok && ty.IsArray() {
		// A binding of one holds that array.
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil && v.Init != nil {
				lit = v.Init
			}
		}
	}
	if aw, isAw := lit.(*ast.AwaitExpression); isAw {
		lit = aw.Argument
	}
	if _, isAL := lit.(*ast.ArrayLiteral); isAL && ok && ty.IsArray() {
		if nt, changed := neverLeafToNumber(ty); changed {
			ty = nt
		}
	}
	// A find-kin callback over a live array takes its element as `T |
	// undefined` (findElemHint): the callback may shrink the array.
	if fn, isFn := e.(*ast.ArrowFunction); isFn && ok && ty.IsFunc() && len(ty.FuncParams) > 0 {
		if ft, fok := o.findElemParam(fn); fok {
			ps := append([]Type(nil), ty.FuncParams...)
			ps[0] = ft
			ty.FuncParams = ps
		}
	}
	if id, isID := e.(*ast.Identifier); isID && ok {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if pm := declaredParam(sym, id.Name); pm != nil {
				if fn, isFn := sym.Declarations[0].Node.(*ast.ArrowFunction); isFn && len(fn.Params) > 0 && &fn.Params[0] == pm && reprKey(ty.withoutNullable()) == reprKey(ty) {
					if ft, fok := o.findElemParam(fn); fok && reprKey(ft.withoutNullable()) == reprKey(ty) {
						ty = ft
					}
				}
			}
		}
	}
	// An unannotated callback parameter the checker leaves `any` is what
	// codegen bound it as from the callee's hints (a replace callback's
	// capture groups are strings).
	if id, isID := e.(*ast.Identifier); isID && ok && isUnconstrainedDynamic(ty) && o.r.heldIdent != nil {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if pm := declaredParam(sym, id.Name); pm != nil && pm.Type == nil && !pm.Rest && pm.Default == nil {
				if ht, hok := o.r.heldIdent(id); hok && !ht.IsDynamic() && ht.IR != "" {
					ty = ht
				}
			}
		}
	}
	// `this` in an object literal's method shorthand reading it is the
	// method's own `this` parameter (funcExprThisType: a box).
	if th, isThis := e.(*ast.ThisExpression); isThis && o.r.funcThis != nil {
	walk:
		for n := o.c.ParentOf(th); n != nil; n = o.c.ParentOf(n) {
			switch f := n.(type) {
			case *ast.ArrowFunction:
				continue
			case *ast.FunctionExpression:
				// A literal with a getter or setter is its synthetic
				// class's instance (ensureObjLitClass), its methods'
				// `this` included.
				if ol, lit := o.c.ParentOf(f).(*ast.ObjectLiteral); lit && ol.HasAccessors() && !ol.HasComputedKey() && !hasProtoKey(ol) && !objLitHasSpread(ol) && o.r.objLit != nil && !o.declaresThis(f) && (o.r.js == nil || !o.r.js()) {
					if ct, cok := o.r.objLit(ol); cok {
						ty, ok = ct, true
						break walk
					}
				}
				if _, lit := o.c.ParentOf(f).(*ast.ObjectLiteral); lit && (f.Method || f.PropValue) {
					if tt, has := o.r.funcThis(f); has {
						// A boxed `this` the checker types from the method's
						// contextual `this` parameter reads as that type.
						// A method whose `this` a `this` parameter types
						// (its own `read(this: Readable)`, or the
						// contextual signature's) reads it as that type;
						// the literal's own type is its box.
						if !(tt.IsDynamic() && ok && !ty.IsDynamic() && ty.IR != "" && (o.declaresThis(f) || ty.IsClass() || declaredByInterface(o.c.TypeOf(th)))) {
							ty, ok = tt, true
						}
					} else if f.PropValue && o.r.js != nil && o.r.js() {
						ty, ok = TypeAny, true // a JavaScript function's own call-time `this`
					}
				}
				break walk
			case *ast.FunctionDeclaration, *ast.ClassDeclaration:
				break walk
			}
		}
	}
	// A const binding of an object literal with a method holds what its
	// declaration answers (GlobalType: the literal's layout, a method
	// taking its `this`).
	if id, isID := e.(*ast.Identifier); isID && (o.r.js == nil || !o.r.js()) {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if v, isV := sym.Declarations[0].Node.(*ast.VarDeclaration); isV && v.Kind == "const" && v.TypeAnnot == nil {
				if lit, isLit := v.Init.(*ast.ObjectLiteral); isLit && literalHasMethod(lit) {
					if lt, lok := o.ExprType(lit); lok {
						ty, ok = lt, true
					}
				}
			}
		}
	}
	// A static object's method that takes its `this` (an object literal's
	// method reading it) is read as the field holds it.
	if m, isM := e.(*ast.MemberExpression); isM && ok && ty.IsFunc() && !ty.FuncThis {
		if ot, ook := o.ExprType(m.Object); ook && ot.IsObject() && !ot.IsDynamic() && !ot.IsClass() {
			if _, ft, has := ot.FieldIndex(m.Property); has && ft.IsFunc() && ft.FuncThis {
				ft.Absent = ty.Absent // an optional read keeps its absence
				ty = ft
			}
		}
	}
	// A `then` callback's value parameter takes a boxed promise value as
	// its box.
	if fn, isFn := e.(*ast.ArrowFunction); isFn && ok && ty.IsFunc() && len(ty.FuncParams) > 0 && !ty.FuncParams[0].IsDynamic() {
		if bt, bok := o.thenValueBox(fn); bok {
			ps := append([]Type(nil), ty.FuncParams...)
			ps[0] = bt
			ty.FuncParams = ps
		}
	}
	// An object literal a property type of which does not map (an
	// overloaded function's value, held by its implementation signature)
	// is its properties' values' answers, in order.
	if lit, isLit := e.(*ast.ObjectLiteral); isLit && (!ok || genFuncProp(o.c.TypeOf(lit))) && (o.r.js == nil || !o.r.js()) {
		if lt, lok := o.literalFields(lit); lok {
			ty, ok = lt, true
		}
	}
	// A literal asserted to a shape is built in that shape
	// (emitAsExpression): an object literal whose keys are the type's
	// fields, an array literal of `any` elements or none.
	if as, isAs := e.(*ast.AsExpression); isAs && as.TypeAnnot != nil {
		if at, aok := o.r.of(o.c.TypeOf(as)); aok {
			switch lit := as.Expr.(type) {
			case *ast.ObjectLiteral:
				if at.IsObject() && !at.IsDynamic() && !at.IsClass() && len(at.Fields) > 0 && literalKeysAreFields(lit, at) {
					ty, ok = at, true
				}
			case *ast.ArrayLiteral:
				if at.IsArray() && at.ElemType != nil && (at.ElemType.IsDynamic() || len(lit.Elements) == 0) {
					ty, ok = at, true
				}
			}
		}
	}
	// An assertion that keeps the box (keepsBox): the operand's box.
	if as, isAs := e.(*ast.AsExpression); isAs && as.TypeAnnot != nil && o.r.keepsBox != nil && o.r.keepsBox(as) {
		if it, iok := o.ExprType(as.Expr); iok && it.IsDynamic() {
			ty, ok = it, true
		}
	}
	// `expr!` holds the operand without its null and undefined (TDD-00187).
	if _, isNN := e.(*ast.NonNullExpression); isNN && ok && !ty.IsDynamic() && ty.Nullable() {
		ty.setNullable(false)
		ty.setUndefined(false)
	}
	// A concrete operand asserted to a union stays erased: the operand's
	// own value (emitAsExpression boxes only into `any`).
	if as, isAs := e.(*ast.AsExpression); isAs && ok && ty.IsDynamic() && !isUnconstrainedDynamic(ty) && len(ty.UnionMembers) > 0 {
		if it, iok := o.ExprType(as.Expr); iok && !it.IsDynamic() && it.IR != "void" && !it.IsCaught() {
			ty = it
		}
	}
	// An erased assertion (`t as K`) is its operand as emitted, an index
	// read's unchecked bound included.
	if as, isAs := e.(*ast.AsExpression); isAs && ok && !ty.UncheckedIndex {
		if it, iok := o.ExprType(as.Expr); iok && it.UncheckedIndex {
			if u := it; func() bool { u.UncheckedIndex = false; return reprKey(u) == reprKey(ty) }() {
				ty = it
			}
		}
	}
	// A link of an optional chain that can short-circuit there is what
	// emitOptionalChain makes of the read: the read's value or undefined.
	if ok && checker.InOptionalChain(e) && !isUnconstrainedDynamic(ty) && o.c.ChainNonOptional(e) != o.c.TypeOf(e) {
		ty = optionalCallResultType(optCalleeValue, ty)
	}
	// A class field nothing may have assigned yet (`o!: T`) is typed T by
	// tsc but reads undefined until assigned: an unchecked read, as an
	// out-of-range element's.
	if m, isM := e.(*ast.MemberExpression); isM && ok && !ty.Nullable() && !ty.IsDynamic() && o.r.unassigned != nil && o.r.unassigned(m) {
		ty = indexReadType(ty)
	}
	return ty, ok
}

// exprTypeBase is ExprType before an optional chain's undefined.
func (o frontOracle) exprTypeBase(e ast.Expression) (Type, bool) {
	ty, ok := o.exprTypeOuter(e)
	// js lane: a reduce whose callback can fall off the end widens its
	// accumulator, and so its result, to `T | undefined` (emitReduce).
	if o.r.js != nil && o.r.js() && ok {
		if call, isC := e.(*ast.CallExpression); isC {
			if cb := jsWidenedReduceCallback(call); cb != nil && reduceAccWidenable(ty) {
				return undefinedableElem(ty), true
			}
		}
		if id, isID := e.(*ast.Identifier); isID {
			if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
				if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil {
					if call, isC := v.Init.(*ast.CallExpression); isC && jsWidenedReduceCallback(call) != nil && reduceAccWidenable(ty) {
						return undefinedableElem(ty), true
					}
				}
			}
		}
	}
	// A binding of a builtin's type narrowed to a program class extending
	// it (`m instanceof C`, `class C extends Map`) reads as that class: the
	// instance is the class's object (ADR-01469).
	if id, isID := e.(*ast.Identifier); isID && ok && !ty.IsClass() && !ty.IsDynamic() && flowTrusted(o.b, id) && o.r.class != nil {
		if nt := o.c.TypeOf(id); !o.c.Unanswered(nt) && nt.Flags&checker.Object != 0 && nt.Kind == checker.Instance && nt.Symbol != nil && nt.Base != nil {
			if ct, cok := o.r.class(nt.Symbol); cok && ct.IsClass() {
				return ct, true
			}
		}
	}
	// A function reading `arguments` gains its implicit rest at emission
	// (maybeAddArgumentsRestParams), whatever the checker saw: its value
	// carries that rest, so a call through the value packs what it passes.
	if ok && ty.IsFunc() && !ty.FuncHasRest {
		var decl ast.Node
		switch x := e.(type) {
		case *ast.Identifier:
			if sym, _ := o.b.Resolve(x); sym != nil && len(sym.Declarations) == 1 {
				decl = sym.Declarations[0].Node
			}
		case *ast.MemberExpression:
			decl = o.c.MemberDecl(x)
		}
		if fd, isFD := decl.(*ast.FunctionDeclaration); isFD && fd.Body != nil {
			off := 0
			if ty.FuncThis {
				off = 1 // the `this` the function takes
			}
			if ps := maybeAddArgumentsRestParams(fd.Params, fd.Body.Body); hasArgumentsRestParamSlice(ps) && len(ty.FuncParams) == len(ps)-1+off {
				ft := ty
				ft.FuncParams = append(append([]Type(nil), ty.FuncParams...), ArrayOf(TypeAny))
				ft.FuncHasRest = true
				ft.FuncParamNames = nil
				if off == 0 {
					for _, p := range ps {
						ft.FuncParamNames = append(ft.FuncParamNames, p.Name)
					}
				}
				if ft.FuncParamOptional != nil {
					ft.FuncParamOptional = append(append([]bool(nil), ty.FuncParamOptional...), false)
				}
				_, isID := e.(*ast.Identifier)
				ft.FuncCountsArgs = isID
				return ft, true
			}
		}
	}
	// An unannotated function returns what its body's returns read as
	// (ReturnType): its value's and its calls' result follow.
	if ok && (ty.IsFunc() || !ty.IsDynamic()) {
		var fn ast.Node
		target := e
		if call, isCall := e.(*ast.CallExpression); isCall {
			target = call.Callee
		}
		switch x := target.(type) {
		case *ast.ArrowFunction, *ast.FunctionExpression:
			fn = x
		case *ast.Identifier:
			if sym, _ := o.b.Resolve(x); sym != nil && len(sym.Declarations) == 1 {
				switch d := sym.Declarations[0].Node.(type) {
				case *ast.FunctionDeclaration:
					fn = d
				case *ast.VarDeclaration:
					if d.TypeAnnot == nil {
						switch d.Init.(type) {
						case *ast.ArrowFunction, *ast.FunctionExpression:
							fn = d.Init
						}
					}
				}
			}
		}
		// An async body returning a box resolves its promise with that box.
		if _, isCall := e.(*ast.CallExpression); isCall && ty.IsPromise() && ty.PromiseType != nil && !ty.PromiseType.IsDynamic() {
			if rt, rok := o.asyncBodyReturn(fn); rok && (isUnconstrainedDynamic(rt) || rt.IsFunc() && rt.FuncCountsArgs && ty.PromiseType.IsFunc() && !ty.PromiseType.FuncCountsArgs) {
				ty.PromiseType = &rt
				return ty, true
			}
		} else if !isCall && ty.IsFunc() && ty.FuncRetType != nil && ty.FuncRetType.IsPromise() && ty.FuncRetType.PromiseType != nil && ty.FuncRetType.PromiseType.IsFunc() {
			// … and one resolving with a function literal reading its own
			// `arguments`, with that function as emission builds it (its
			// synthetic rest).
			if rt, rok := o.asyncBodyReturn(fn); rok && rt.IsFunc() && rt.FuncCountsArgs && !ty.FuncRetType.PromiseType.FuncCountsArgs {
				ft, pt := ty, *ty.FuncRetType
				pt.PromiseType = &rt
				ft.FuncRetType = &pt
				return ft, true
			}
		} else if !isCall && ty.IsFunc() && ty.FuncRetType != nil && ty.FuncRetType.IsPromise() && ty.FuncRetType.PromiseType != nil {
			// … and one resolving with undefined where the checker has a box
			// (`async () => new.target`), with that undefined.
			if rt, rok := o.asyncBodyReturn(fn); rok && (isUnconstrainedDynamic(rt) && !ty.FuncRetType.PromiseType.IsDynamic() || rt.IsUndefined() && !rt.Nullable() && isUnconstrainedDynamic(*ty.FuncRetType.PromiseType)) {
				ft, pt := ty, *ty.FuncRetType
				pt.PromiseType = &rt
				ft.FuncRetType = &pt
				return ft, true
			}
		}
		if rt, rok := o.inferredBodyReturn(fn); rok {
			if _, isCall := e.(*ast.CallExpression); isCall {
				if !ty.IsFunc() && rt.Nullable() && !ty.Nullable() && rt.IR == ty.IR {
					return rt, true
				}
			} else if ty.IsFunc() && ty.FuncRetType != nil && (rt.Nullable() && !ty.FuncRetType.Nullable() && rt.IR == ty.FuncRetType.IR || (ty.FuncRetType.IR == "void" || !ty.FuncRetType.IsDynamic()) && isUnconstrainedDynamic(rt)) {
				ft := ty
				ft.FuncRetType = &rt
				return ft, true
			}
		}
	}
	// A method returning an accessor literal returns its synthetic class's
	// instance (ReturnType).
	if lit, isLit := e.(*ast.ObjectLiteral); isLit && ok && len(ty.Fields) > 0 {
		if nt := o.literalMethodDefaults(lit, ty); reprKey(nt) != reprKey(ty) {
			return nt, true
		}
		// A method taking its `this` holds it as its first parameter, as
		// the method's own expression answers.
		var tf []Field
		for _, p := range lit.Properties {
			fe, isFE := p.Value.(*ast.FunctionExpression)
			if p.KeyExpr != nil || p.AccessorKind != "" || !isFE {
				continue
			}
			mt, mok := o.ExprType(fe)
			if !mok || !mt.IsFunc() || !mt.FuncThis {
				continue
			}
			for i, f := range ty.Fields {
				if f.Name == p.Key && f.Ty.IsFunc() && !f.Ty.FuncThis {
					if tf == nil {
						tf = append([]Field(nil), ty.Fields...)
					}
					tf[i].Ty = mt
				}
			}
		}
		if tf != nil {
			ty.Fields = tf
			return ty, true
		}
		if nt := o.literalMethodReturns(lit, ty); reprKey(nt) != reprKey(ty) {
			return nt, true
		}
	}
	// An array of object literals of one layout holds that layout, its
	// methods taking their `this` as each literal's own answer does.
	if al, isAL := e.(*ast.ArrayLiteral); isAL && ok && ty.IsArray() && ty.ElemType != nil && len(al.Elements) > 0 {
		var et Type
		same := true
		for i, el := range al.Elements {
			ol, isOL := el.(*ast.ObjectLiteral)
			if !isOL {
				same = false
				break
			}
			lt, lok := o.ExprType(ol)
			if !lok || i > 0 && reprKey(lt) != reprKey(et) {
				same = false
				break
			}
			et = lt
		}
		if same && reprKey(et) != reprKey(*ty.ElemType) && reprKey(withoutFuncThis(et)) == reprKey(withoutFuncThis(*ty.ElemType)) {
			return ArrayOf(et), true
		}
	}
	// A class a static field holds is held boxed: a member read off it
	// is read dynamically (emitMember), whatever class the checker knows.
	if m, isM := e.(*ast.MemberExpression); isM && ok && !m.Optional && !ty.IsDynamic() && o.staticClassField(m.Object) {
		return TypeAny, true
	}
	// A method read straight off a literal is that literal's field, and a
	// binding of one, never reassigned, holds it.
	if m, isM := e.(*ast.MemberExpression); isM && ok && !m.Optional {
		if lit, isLit := m.Object.(*ast.ObjectLiteral); isLit {
			if lt, lok := o.ExprType(lit); lok {
				for _, f := range lt.Fields {
					if f.Name == m.Property && f.Ty.IsFunc() && f.Ty.FuncThis {
						return f.Ty, true
					}
				}
			}
		}
	}
	if id, isID := e.(*ast.Identifier); isID && ok && ty.IsFunc() && !ty.FuncThis {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil && (v.Kind == "const" || !o.reassigned(sym)) {
				if m, isM := v.Init.(*ast.MemberExpression); isM && !m.Optional {
					_, isLit := m.Object.(*ast.ObjectLiteral)
					_, isRef := m.Object.(*ast.Identifier) // `const f = counter.describe`
					if isLit || isRef {
						if mt, mok := o.ExprType(m); mok && mt.IsFunc() && mt.FuncThis {
							return mt, true
						}
					}
				}
			}
		}
	}
	// A binding of an array of object literals whose methods take their
	// `this` holds that array; a callback given its elements, one of them.
	if id, isID := e.(*ast.Identifier); isID && ok && (ty.IsArray() || ty.IsObject()) && !ty.IsDynamic() {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil && (v.Kind == "const" || !o.reassigned(sym)) {
				if al, isAL := v.Init.(*ast.ArrayLiteral); isAL && ty.IsArray() {
					if at, aok := o.ExprType(al); aok && onlyThisDiffers(at, ty) {
						return at, true
					}
				}
			}
			if pm := declaredParam(sym, id.Name); pm != nil && pm.Type == nil && ty.IsObject() {
				if fn, isFn := sym.Declarations[0].Node.(*ast.ArrowFunction); isFn && len(fn.Params) > 0 && &fn.Params[0] == pm {
					if call, isCall := o.c.ParentOf(fn).(*ast.CallExpression); isCall && len(call.Args) > 0 && call.Args[0] == ast.Expression(fn) {
						if m, isM := call.Callee.(*ast.MemberExpression); isM && !m.Optional && elementCallback[m.Property] {
							if rt, rok := o.ExprType(m.Object); rok && rt.IsArray() && rt.ElemType != nil && onlyThisDiffers(*rt.ElemType, ty) {
								return *rt.ElemType, true
							}
						}
					}
				}
			}
		}
	}
	// `const q = a` of a const bound to an object literal: a's layout (its
	// methods taking their `this` included).
	if id, isID := e.(*ast.Identifier); isID && ok && ty.IsObject() && !ty.IsDynamic() {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.Kind == "const" && v.TypeAnnot == nil {
				if src, isSrc := v.Init.(*ast.Identifier); isSrc {
					if ss, _ := o.b.Resolve(src); ss != nil {
						if sv, sok := singleVarDecl(ss); sok && sv.Kind == "const" && sv.TypeAnnot == nil {
							if _, isLit := sv.Init.(*ast.ObjectLiteral); isLit {
								if at, aok := o.ExprType(src); aok && at.IsObject() && !at.IsDynamic() {
									return at, true
								}
							}
						}
					}
				}
			}
		}
	}
	// A collection seed of rows of differing kinds the checker leaves
	// unmapped is the array of its first row (inferArrayTypeOf).
	if seed, isLit := e.(*ast.ArrayLiteral); isLit && !ok && len(seed.Elements) > 0 {
		if row, isRow := seed.Elements[0].(*ast.ArrayLiteral); isRow && o.collectionSeedRow(row) {
			if rt, rok := o.ExprType(row); rok {
				if o.r.js != nil && o.r.js() && !sameSeedRows(o, seed, rt) {
					// JavaScript's rows of different layouts: boxed rows.
					return ArrayOf(ArrayOf(TypeAny)), true
				}
				return ArrayOf(rt), true
			}
		}
	}
	// A collection seed's rows are arrays of their elements, as emission
	// builds them, whatever tuples the constructor's context types.
	if row, isLit := e.(*ast.ArrayLiteral); isLit && o.collectionSeedRow(row) {
		if tt := o.c.TypeOf(row); !o.c.Unanswered(tt) && tt.Kind == checker.Tuple {
			if rt, rok := o.r.of(o.c.TupleAsArray(tt)); rok {
				return rt, true
			}
		}
	}
	// An unannotated binding initialized `null` and never given a value
	// (its evolving type's storage is `null | undefined`, which maps to a
	// box) holds the null.
	if id, isID := e.(*ast.Identifier); isID && ok && (ty.IsNull() && ty.IsUndefined() || isUnconstrainedDynamic(ty) && nullAndUndefinedOnly(o.c.StorageTypeOf(id))) {
		// (A JavaScript module binding a function reads is the global's
		// box.)
		if sym, _ := o.b.Resolve(id); sym != nil && !(o.r.js != nil && o.r.js() && sym.Scope != nil && sym.Scope.Kind == binder.ModuleScope && inFunction(o.c, id)) {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil {
				if nl, isNull := v.Init.(*ast.NullLiteral); isNull && !nl.IsUndefined {
					if !ty.IsNull() {
						return TypeNull, true
					}
					ty.setUndefined(false)
					return ty, true
				}
			}
		}
	}
	// An unannotated binding of an object literal with a getter or setter
	// holds the literal's synthetic class (TDD-00153).
	// (The js lane holds any untyped object-literal binding in a box.)
	if id, isID := e.(*ast.Identifier); isID && ok && !ty.IsClass() && o.r.objLit != nil && (o.r.js == nil || !o.r.js()) {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil {
				if lit, isLit := v.Init.(*ast.ObjectLiteral); isLit && hasAccessor(lit) {
					if ct, cok := o.r.objLit(lit); cok {
						return ct, true
					}
				}
			}
		}
	}
	// A `var` whose declaration can be skipped on the way to a read holds
	// `T | undefined` (hoistedVarMaySkip, ADR-01057). A module-level one
	// read in its own block reads the block's slot, which widens only an
	// array or object (the module global behind it is widened).
	if id, isID := e.(*ast.Identifier); isID && ok && !ty.IsDynamic() && ty.IR != "void" {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && sym.Scope != nil && o.hoistSkippable(v) && !o.promotedGlobal(sym, v) && !o.synthesizedArguments(id) {
				// A module-level one reads the slot emission bound it to (a
				// block's widened local, or the global behind it): widened
				// exactly when that storage is.
				if sym.Scope.Kind == binder.ModuleScope && o.r.heldIdent != nil {
					if ht, hok := o.r.heldIdent(id); hok && ht.Absent == 0 && !isNullableScalar(ht) {
						return ty, true
					}
				}
				return indexReadType(ty), true
			}
		}
	}
	// A class field codegen widened to a box (a `null`-initialized field
	// later given a value: classFieldNullEvolves) reads as that box.
	if m, isM := e.(*ast.MemberExpression); isM && ok && !ty.IsDynamic() {
		if rt, rok := o.ExprType(m.Object); rok && rt.IsClass() {
			for _, name := range []string{m.Property, llvmSafeSymbol(m.Property)} {
				if _, ft, found := rt.FieldIndex(name); found {
					if ft.IsDynamic() && (ty.IsNull() || ty.IsUndefined()) {
						return TypeAny, true
					}
					break
				}
			}
		}
	}
	// A method read as a value is the bound method closure, whose call
	// passes the arguments past its parameters in the rest without a count
	// (the method's own entry counts them): no FuncCountsArgs.
	if _, isM := e.(*ast.MemberExpression); isM && ok && ty.IsFunc() && ty.FuncCountsArgs {
		ty.FuncCountsArgs = false
	}
	// … and so is a binding of one.
	if id, isID := e.(*ast.Identifier); isID && ok && ty.IsFunc() && ty.FuncCountsArgs && o.boundMethodValue(id) {
		ty.FuncCountsArgs = false
	}
	// A `/** @value */` array binding holds the flat value-type array.
	if id, isID := e.(*ast.Identifier); isID && ok && ty.IsArray() && ty.ElemType != nil && flatArrayElemOK(*ty.ElemType) {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.ValueArr {
				return FlatArrayType(*ty.ElemType), true
			}
		}
	}
	// A binding of such a call holds the closure it returns.
	if id, isID := e.(*ast.Identifier); isID {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil {
				if call, isCall := v.Init.(*ast.CallExpression); isCall {
					if ft, ok := o.overloadedClosureCall(call); ok {
						return ft, true
					}
				}
			}
		}
	}
	// An untyped binding codegen widened to a box holds a box.
	if id, isID := e.(*ast.Identifier); isID && o.r.widened != nil && o.r.widened(id.Name) {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil && v.Kind != "const" {
				return TypeAny, true
			}
		}
	}
	// An annotated binding initialized from an `any` holds the box.
	if id, isID := e.(*ast.Identifier); isID && o.r.anyHeld != nil {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && o.r.anyHeld(v) {
				return TypeAny, true
			}
		}
	}
	// A function's own local the checker types as a union its declaration
	// stores as one member (a ternary of Buffer and Uint8Array) is held as
	// that member (localDeclType, emitVarDeclBody).
	if id, isID := e.(*ast.Identifier); isID && ok && ty.IsDynamic() && len(ty.UnionMembers) > 0 && o.r.localDecl != nil {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV {
				if lt, has := o.r.localDecl(v); has && !lt.IsDynamic() {
					return lt, true
				}
			}
		}
	}
	// An unannotated binding is held as an initializer codegen holds boxed.
	if id, isID := e.(*ast.Identifier); isID {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && o.dynamicInit(v) {
				return o.boxedInitRead(id, v)
			}
		}
	}
	// A binding holding a void call's result holds undefined.
	if _, isID := e.(*ast.Identifier); isID && ok && ty.IR == "void" {
		return TypeUndefined, true
	}
	// A builtin call a library signature declares to return tuples
	// (`Object.entries(o)`, its `.filter(...)`), and an array of spreads of
	// them (`[...m.entries()]`), hold those tuples.
	if !ok && o.libTupleSource(e) {
		if nt := o.c.TypeOf(e); !o.c.Unanswered(nt) && hasTuple(nt) {
			r := o.r
			r.tuples = true
			ty, ok = r.of(nt)
		}
	}
	// A function literal annotating its result: a tuple it declares is the
	// positional struct codegen builds (annotatedFuncResult).
	if !ok && annotatedFuncResult(e) {
		if nt := o.c.TypeOf(e); !o.c.Unanswered(nt) && hasTuple(nt) {
			r := o.r
			r.tuples = true
			ty, ok = r.of(nt)
		}
	}
	// A read of a property an annotation declares a tuple holds that tuple.
	if m, isM := e.(*ast.MemberExpression); isM {
		ps, isPS := o.c.MemberDecl(m).(*ast.PropertySignature)
		rt := o.c.NonNullable(o.c.TypeOf(m.Object))
		declared := isPS && ps.Type != nil || !o.c.Unanswered(rt) && rt.Flags&checker.Object != 0 && (rt.Kind == checker.Interface || rt.Kind == checker.Instance)
		if declared {
			// A class instance's field holds what its instantiation lays
			// out: an erased one's (`Opt<any>`, a generic method's result)
			// is no tuple, whatever the checker's instance spells.
			if ot, ook := o.ExprType(m.Object); ook && ot.IsClass() && !ot.IsDynamic() {
				for _, f := range ot.Fields {
					if f.Name == m.Property && !f.Ty.IsTuple() && hasTuple(o.c.TypeOf(m)) {
						declared = false
						ty, ok = f.Ty, true
					}
				}
			}
		}
		if declared {
			if nt := o.c.TypeOf(m); !o.c.Unanswered(nt) && hasTuple(nt) {
				r := o.r
				r.tuples = true
				if tt, tok := r.of(nt); tok {
					ty, ok = tt, true
				}
			}
		}
	}
	// A call (or its await) of a function declared to return a tuple (or a
	// promise of one): the tuple, as the declaration spells it.
	if !ok {
		inner := e
		if aw, isAw := e.(*ast.AwaitExpression); isAw {
			inner = aw.Argument
		}
		if call, isC := inner.(*ast.CallExpression); isC && !call.Optional {
			if cid, isID := call.Callee.(*ast.Identifier); isID {
				if sym, _ := o.b.Resolve(cid); sym != nil && len(sym.Declarations) == 1 {
					if fd, isFD := sym.Declarations[0].Node.(*ast.FunctionDeclaration); isFD && fd.ReturnType != nil && len(fd.TypeParams) == 0 {
						if nt := o.c.TypeOf(e); !o.c.Unanswered(nt) && hasTuple(nt) {
							r := o.r
							r.tuples = true
							if tt, tok := r.of(nt); tok {
								ty, ok = tt, true
							}
						}
					}
				}
			}
			if m, isM := call.Callee.(*ast.MemberExpression); isM {
				// A class method declared to return one (`m(): [T, string]`).
				if fd, isFD := o.c.MemberDecl(m).(*ast.FunctionDeclaration); isFD && fd.ReturnType != nil {
					if nt := o.c.TypeOf(e); !o.c.Unanswered(nt) && hasTuple(nt) {
						r := o.r
						r.tuples = true
						if tt, tok := r.of(nt); tok {
							ty, ok = tt, true
						}
					}
				}
			}
		}
	}
	// A call of a function value no declaration spells tuples for (an
	// unannotated parameter or arrow): what the callee, as held, returns.
	if !ok {
		if call, isC := e.(*ast.CallExpression); isC && !call.Optional {
			if nt := o.c.TypeOf(e); !o.c.Unanswered(nt) && hasTuple(nt) {
				if ft, fok := o.ExprType(call.Callee); fok {
					switch {
					case ft.IsDynamic():
						ty, ok = TypeAny, true
					case ft.IsFunc() && ft.FuncRetType != nil:
						ty, ok = *ft.FuncRetType, true
					}
				}
			}
		}
	}
	if id, isID := e.(*ast.Identifier); isID {
		// A binding an annotation declares a tuple holds that tuple; an
		// unannotated one, what its initializer is held as; a function
		// declared to return one returns it.
		if sym, _ := o.b.Resolve(id); sym != nil {
			if len(sym.Declarations) == 1 {
				if fd, isFD := sym.Declarations[0].Node.(*ast.FunctionDeclaration); isFD && fd.ReturnType != nil {
					if nt := o.c.TypeOf(id); !o.c.Unanswered(nt) && nt.Flags&checker.Object != 0 && nt.Kind == checker.Function && hasTuple(nt.Ret()) {
						r := o.r
						r.tuples = true
						if ft, fok := r.of(nt); fok {
							ty, ok = ft, true
						}
					}
				}
			}
			if pm := declaredParam(sym, id.Name); pm != nil && pm.Type != nil && !pm.Rest {
				// Its storage, not the type a read narrows it to.
				if nt := o.c.StorageTypeOf(id); !o.c.Unanswered(nt) && hasTuple(nt) {
					r := o.r
					r.tuples = true
					if pt, pok := r.of(nt); pok {
						ty, ok = pt, true
						// A read flow narrows to one of its members is
						// that member, unboxed (emitIdent).
						if rt := o.c.TypeOf(id); pt.IsDynamic() && len(pt.UnionMembers) > 0 && flowTrusted(o.b, id) && !o.c.Unanswered(rt) && rt.Flags&checker.Union == 0 {
							if mt, mok := r.of(rt); mok {
								for _, m := range pt.UnionMembers {
									if reprKey(m) == reprKey(mt) {
										ty = mt
										break
									}
								}
							}
						}
					}
				}
			}
			if v, isV := singleVarDecl(sym); isV {
				if nt := o.c.TypeOf(id); !o.c.Unanswered(nt) && hasTuple(nt) {
					if v.TypeAnnot != nil || explicitTupleTypeArgs(v.Init) {
						// Or a `new Map<K, [A, B]>()`: its type arguments
						// are annotations too.
						r := o.r
						r.tuples = true
						ty, ok = r.of(nt)
					} else if v.Init != nil {
						ty, ok = o.ExprType(v.Init)
					}
				}
			}
		}
	}
	if id, isID := e.(*ast.Identifier); isID && ok && ty.IsDynamic() && o.emptyObjectRest(id) && !o.boxedRestSource(id) {
		return UserObjectType(nil), true // bindObjectRest's residual: no fields left
	}
	if id, isID := e.(*ast.Identifier); isID && ok && !ty.Nullable() && (ty.IR == "i1" || ty.IR == "double" && ty.Float || ty.IsObject() && !ty.IsDynamic() || isStringTy(ty) || ty.IsArray()) {
		// A number, boolean, string or object an array pattern binds without a default from
		// a source that is not a literal may be past its end: an
		// undefined-capable slot (unpackArrayPatternInto).
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			var el *ast.ArrayPatternElem
			switch d := sym.Declarations[0].Node.(type) {
			case *ast.ArrayDestructuring:
				if src, lit := d.Init.(*ast.ArrayLiteral); lit && !hasSpreadElem(src.Elements) {
					// A literal source binds its elements; a name past its
					// end (JavaScript, `let [x, y] = [[1]]`) reads undefined.
					for i := range d.Elems {
						if d.Elems[i].Name == id.Name && i >= len(src.Elements) {
							el = &d.Elems[i]
						}
					}
				} else if !lit {
					el = findArrayPatternElem(d.Elems, id.Name)
					// A tuple source binds positionally, its elements as
					// they are (unpackTuplePatternInto).
					if st, sok := o.ExprType(d.Init); sok && st.IsTuple() && !st.IsDynamic() && directArrayPatternElem(d.Elems, el) {
						el = nil
					}
				}
			case *ast.ObjectDestructuring:
				if _, lit := d.Init.(*ast.ObjectLiteral); !lit {
					el = findArrayPatternElemIn(d.Props, id.Name)
				}
			case *ast.ForOfStatement:
				// `for (const [a, b] of rows)` over arrays (not tuples): the
				// same, each row possibly shorter.
				if it, iok := o.ExprType(d.Iterable); d.ArrayPattern != nil && !d.Await && iok && it.IsArray() && !it.IsDynamic() && it.ElemType != nil {
					if row := *it.ElemType; row.IsArray() && !row.IsTuple() && !row.IsDynamic() {
						el = findArrayPatternElem(d.ArrayPattern, id.Name)
						if !directArrayPatternElem(d.ArrayPattern, el) {
							el = nil
						}
					}
				}
			}
			if el != nil && el.Default == nil && !el.Rest {
				if ty.IsArray() {
					return indexReadType(ty), true // an element read's absent array
				}
				ty.setNullable(true)
				ty.setUndefined(true)
				return ty, true
			}
		}
	}
	if id, isID := e.(*ast.Identifier); isID && ok {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil && ty.IsDynamic() {
				if call, isCall := v.Init.(*ast.CallExpression); isCall {
					if tt, ok := o.assignResult(call); ok && !tt.IsArray() {
						return tt, true // the binding holds the target
					}
				}
			}
			if v, isV := singleVarDecl(sym); isV {
				if o.dynamicInit(v) {
					return o.boxedInitRead(id, v)
				}
				// A JavaScript alias of an array binding (`let u2 = u`) holds
				// what its source holds, element boxes and all.
				if src, isSrc := v.Init.(*ast.Identifier); isSrc && v.TypeAnnot == nil && ty.IsArray() && o.r.js != nil && o.r.js() {
					if st, sok := o.ExprType(src); sok && st.IsArray() && !st.IsTuple() {
						return st, true
					}
				}
				if sym.Scope != nil && sym.Scope.Kind == binder.ModuleScope && o.evolvingBoxed(v) {
					return ArrayOf(TypeAny), true
				}
				// An unannotated string binding of a read that may be
				// undefined (a dictionary's) holds it so: present only where
				// its stores prove it (assignedString).
				if v.TypeAnnot == nil && v.Init != nil && isStringTy(ty) && !ty.Nullable() {
					if it, ok := o.ExprType(v.Init); ok && isStringTy(it) && it.Nullable() && it.IsUndefined() && !it.UncheckedIndex {
						if !(flowTrusted(o.b, id) && (o.b.DefinitelyAssigned(id) || o.reprStoresPresent(id) || o.b.GuardedSinceStore(id))) {
							return it, true
						}
					}
				}
				// A const object literal with a member returning a generator
				// is the literal's answer (genFuncProp).
				if lit, isLit := v.Init.(*ast.ObjectLiteral); isLit && v.TypeAnnot == nil && v.Kind == "const" && (genFuncProp(o.c.TypeOf(lit)) || o.boxedIdentValue(lit)) && (o.r.js == nil || !o.r.js()) {
					if lt, ok := o.literalFields(lit); ok {
						return lt, true
					}
				}
				// An unannotated binding of a builtin iterator call the checker
				// types by a lib interface (`const it = arr[Symbol.iterator]()`)
				// holds the iterator codegen lays out.
				if call, isCall := v.Init.(*ast.CallExpression); isCall && v.TypeAnnot == nil && isUnconstrainedDynamic(ty) && o.r.codegenCall != nil {
					if ct, cok := o.r.codegenCall(call); cok && ct.IsCollIter() {
						return ct, true
					}
				}
				st := o.undefinedSlot(v, sym, ty)
				if st.Nullable() && !ty.Nullable() && o.presentRead(id, st, v.Init != nil) {
					return ty, true
				}
				// A nullable reference flow proves present (emitIdent's
				// checkerPresentRef), whatever initialized it.
				if presentRefCandidate(id, st) && !o.arrayPatternBinding(id) && !o.storesIndexRead(id) && flowTrusted(o.b, id) {
					if nt := o.c.TypeOf(id); !o.c.Unanswered(nt) && presentType(nt) {
						return st.withoutNullable(), true
					}
				}
				return st, true
			}
		}
	}
	return ty, ok
}

// dynamicInit reports an unannotated binding whose initializer codegen
// holds boxed though its static type is narrower (`Object.defineProperty(
// {}, …)`, typed `{}`): the binding is held boxed too.
func (o frontOracle) dynamicInit(v *ast.VarDeclaration) bool {
	if v.TypeAnnot != nil || v.Init == nil || v.Ambient {
		return false
	}
	// Each read of the binding asks again; typing a large initializer
	// (a 20K-row table) once per read is quadratic.
	if d, seen := o.dynInit[v]; seen {
		return d
	}
	d := o.dynamicInitUncached(v)
	o.dynInit[v] = d
	return d
}

func (o frontOracle) dynamicInitUncached(v *ast.VarDeclaration) bool {
	if ol, isOL := v.Init.(*ast.ObjectLiteral); isOL && ol.HasComputedKey() && (o.r.js == nil || !o.r.js()) {
		return true
	}
	// An object literal an accessor is defined on (`Object.defineProperty(o,
	// "a", { get() {…} })`): a dynamic object, whose reads call it.
	if accessorLiteral(o.b, v) {
		return true
	}
	switch v.Init.(type) {
	case *ast.CallExpression, *ast.ArrayLiteral, *ast.MemberExpression, *ast.IndexExpression, *ast.NonNullExpression:
	default:
		return false
	}
	it, ok := o.ExprType(v.Init)
	if !ok {
		return false
	}
	t := o.c.TypeOf(v.Init)
	if o.c.Unanswered(t) {
		return false
	}
	if lit, isLit := v.Init.(*ast.ArrayLiteral); isLit && len(lit.Elements) == 0 {
		return false // evolvingBoxed's
	}
	if lit, isLit := v.Init.(*ast.ArrayLiteral); isLit && o.r.js != nil && o.r.js() && len(lit.Elements) > 0 {
		if _, nested := lit.Elements[0].(*ast.ArrayLiteral); nested {
			mt, ok := o.r.of(t)
			return !ok || reprKey(mt) != reprKey(it) // the js lane's nested boxes
		}
	}
	if it.IsArray() && it.ElemType != nil && isUnconstrainedDynamic(*it.ElemType) {
		// An array of boxes: held so unless the checker's is one too (or
		// one or undefined, which flow narrows).
		nt := o.c.NonNullable(t)
		return nt.Flags&checker.Object == 0 || nt.Kind != checker.Array || nt.Elem.Flags&(checker.Any|checker.Unknown) == 0
	}
	if t.Flags&checker.Any != 0 && it.IsArray() {
		return true // a call the checker leaves `any` that codegen types (Object.assign)
	}
	return isUnconstrainedDynamic(it) && t.Flags&(checker.Any|checker.Unknown) == 0
}

// undefinedSlot is the storage a binding annotated with a plain number,
// boolean or string has when it may hold undefined, which every read of it
// is typed as (emitVarDecl, globalUndefinedStorage): declared without a
// value, or given one that may be undefined (`let v: number = xs[i]`, a
// read past the end), as Node keeps it.
func (o frontOracle) undefinedSlot(v *ast.VarDeclaration, sym *binder.Symbol, ty Type) Type {
	if v.TypeAnnot == nil || v.Ambient || ty.Nullable() || ty.IsDynamic() || len(ty.UnionMembers) > 0 {
		return ty
	}
	num, boolean, str := ty.Float && ty.IR == "double", ty.IR == "i1", isStringTy(ty)
	global := sym.Scope != nil && sym.Scope.Kind == binder.ModuleScope
	if !num && !str && !(boolean && (global || v.Init == nil)) {
		return ty
	}
	if v.Init != nil {
		it, ok := o.ExprType(v.Init)
		if !ok || !it.Nullable() || !it.IsUndefined() || it.NullAndUndef() {
			return ty
		}
		if str != isStringTy(it.withoutNullable()) || num && !it.withoutNullable().Float {
			return ty
		}
	}
	if str {
		ty.setNullable(true)
		ty.setUndefined(true)
		return ty
	}
	nt := undefinedableElem(ty)
	nt.setUndefined(true)
	return nt
}

// presentRead mirrors emitIdent's checkerPresent/assignedString for a
// binding undefinedSlot holds: a read every path to which has stored a
// present value, or that flow proves present, is the payload.
// An initializer that may be absent marks the slot as an element read's
// (unchecked): only the stores themselves prove it present then.
func (o frontOracle) presentRead(id *ast.Identifier, ty Type, absentInit bool) bool {
	if !flowTrusted(o.b, id) {
		return false
	}
	if o.reprStoresPresent(id) {
		return true
	}
	if ty.UncheckedIndex || absentInit {
		return false
	}
	if o.b.DefinitelyAssigned(id) {
		return true
	}
	nt, st := o.c.TypeOf(id), o.c.StorageTypeOf(id)
	return !o.c.Unanswered(st) && !o.c.Unanswered(nt) && !presentType(st) && presentType(nt)
}

// boxedValueQuery reports whether q (`typeof E`, `typeof N.Sub`) names an
// enum or a namespace itself, whose object a value holds boxed
// (exprTypeOuter).
func (o frontOracle) boxedValueQuery(q *ast.TypeQuery) bool {
	var sym *binder.Symbol
	for s := o.b.LookupScope(q); s != nil && sym == nil; s = s.Parent {
		if v := s.Symbols.Get(q.Name); v != nil && v.Flags&binder.Value != 0 {
			sym = v
		}
	}
	for _, seg := range q.Path {
		if sym == nil {
			return false
		}
		sc := o.b.NamespaceScope(sym)
		if sc == nil {
			return false
		}
		sym = sc.Symbols.Get(seg)
	}
	return sym != nil && sym.Flags&binder.Variable == 0 && sym.Flags&(binder.Enum|binder.NamespaceModule) != 0 && sym.Flags&(binder.Class|binder.Function) == 0
}

func (o frontOracle) exprTypeOuter(e ast.Expression) (Type, bool) {
	// A read through a proxy is its get trap's result, whatever the
	// target declares.
	if obj := dynamicReadReceiver(e); obj != nil && o.proxyValue(obj) {
		return TypeAny, true
	}
	// An enum's name as a value is its object, held boxed (emitIdent), and
	// so is a namespace's enum (`X.Kind`).
	if id, isID := e.(*ast.Identifier); isID {
		if sym, _ := o.b.Resolve(id); sym != nil && sym.Flags&binder.Enum != 0 && sym.Flags&binder.Variable == 0 {
			return TypeAny, true
		}
	}
	if m, isM := e.(*ast.MemberExpression); isM {
		if sym := o.c.NamespaceMember(m); sym != nil && sym.Flags&binder.Enum != 0 && sym.Flags&binder.Variable == 0 {
			return TypeAny, true
		}
		// A nested namespace itself is an object, held boxed.
		if sym := o.c.NamespaceMember(m); sym != nil && sym.Flags&binder.NamespaceModule != 0 && sym.Flags&binder.Variable == 0 {
			return TypeAny, true
		}
	}
	if id, isID := e.(*ast.Identifier); isID {
		if sym, _ := o.b.Resolve(id); sym != nil && sym.Flags&binder.NamespaceModule != 0 && sym.Flags&(binder.Variable|binder.Function|binder.Class|binder.Enum) == 0 {
			return TypeAny, true
		}
	}
	// An unannotated parameter of an arrow passed to an erased generic
	// method (`this.call<T>(s, (fd, done) => …)`): the erased signature's
	// hint codegen binds it with, its type parameters boxes.
	if id, isID := e.(*ast.Identifier); isID && o.r.erasedParam != nil {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if af, isAF := sym.Declarations[0].Node.(*ast.ArrowFunction); isAF {
				for i := range af.Params {
					if p := af.Params[i]; p.Name == id.Name && p.Type == nil && !p.Rest && p.ArrayPattern == nil && p.ObjectPattern == nil {
						if pt, ok := o.r.erasedParam(af, i); ok {
							return pt, true
						}
					}
				}
			}
		}
	}
	// `x!` over an operand held as a tuple struct: that struct, present.
	if nn, isNN := e.(*ast.NonNullExpression); isNN {
		if it, ok := o.ExprType(nn.Arg); ok && it.IsTuple() && !it.IsDynamic() {
			return it.withoutNullable(), true
		}
	}
	// `m.get(k)` off a Map holding tuples (`Map<number, [string,
	// string]>`): the headerless struct the map holds, undefined on a miss
	// (emitMapMethodGet), where the checker's tuple maps to an array.
	if call, isCall := e.(*ast.CallExpression); isCall && len(call.Args) == 1 && !call.Optional {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && m.Property == "get" && !m.Optional {
			if rt, ok := o.ExprType(m.Object); ok && rt.IsMap() && rt.MapVal != nil && rt.MapVal.IsTuple() && mapGetUndefinedablePtr(*rt.MapVal) {
				v := *rt.MapVal
				v.setNullable(true)
				v.setUndefined(true)
				return v, true
			}
		}
	}
	// `a && b` / `a || b` is value-preserving in either lane: its slot is
	// what its operands' representations give (emitShortCircuit), not the
	// checker's narrowed result (`undefined && true` is `undefined` to tsc).
	if b, isB := e.(*ast.BinaryExpression); isB && (b.Op == "&&" || b.Op == "||") {
		lt, lok := o.heldOperand(b.Left)
		rt, rok := o.heldOperand(b.Right)
		// An operand held as a box makes the slot a box, the other
		// operand boxed into it (emitShortCircuit's dynamic arm).
		if lok && isUnconstrainedDynamic(lt) || rok && isUnconstrainedDynamic(rt) {
			return TypeAny, true
		}
		if lok && rok {
			if ty, ok := andAbsentScalar(b.Op, lt, rt); ok {
				return ty, true
			}
			if ty, ok := shortCircuitValueType(b.Op, lt, rt); ok {
				return ty, true
			}
			return TypeBool, true
		}
	}
	// `await p` of a value held as a promise is the promise's payload as
	// held (emitAwait): `Promise.all` over a thenable instance fulfills
	// with boxes, whatever tuple the checker gives.
	if aw, isAw := e.(*ast.AwaitExpression); isAw {
		if pt, ok := o.ExprType(aw.Argument); ok && pt.IsPromise() && pt.PromiseType != nil {
			return *pt.PromiseType, true
		}
		// A generator object has no `then`: awaiting it is itself.
		if pt, ok := o.ExprType(aw.Argument); ok && pt.IsGenerator() {
			return pt, true
		}
	}
	// An index read off an array held with boxed elements (a literal of
	// mixed kinds, `Promise.all` over a thenable) reads the element's box,
	// whatever element type the checker gives (emitIndex).
	if ix, isIx := e.(*ast.IndexExpression); isIx && !ix.Optional {
		if at, ok := o.ExprType(ix.Object); ok && at.IsArray() && !at.IsTuple() && !at.IsTypedArray() && at.DynPropTy == nil && at.ElemType != nil && at.ElemType.IsDynamic() {
			if kt, kok := o.ExprType(ix.Index); kok && nonPtrScalar(kt) && !kt.IsDynamic() {
				return indexReadType(*at.ElemType), true
			}
		}
	}
	// `new (expr)(…)` constructs through the value expr holds
	// (emitDynNew): a program class's instance the checker resolves, else
	// a box (checkerInstanceType).
	if nx, isNew := e.(*ast.NewExpression); isNew && nx.Callee != nil {
		if t := o.c.TypeOf(nx); !o.c.Unanswered(t) && t.Symbol != nil && o.r.class != nil {
			if ct, ok := o.r.class(t.Symbol); ok && ct.IsObject() && ct.IsClass() {
				return ct, true
			}
		}
		return TypeAny, true
	}
	// A proxy is held boxed, whatever its target is (emitNewProxy).
	if nx, isNew := e.(*ast.NewExpression); isNew && nx.Callee == nil && nx.ClassName == "Proxy" && !nx.Qualified && o.r.class != nil {
		if t := o.c.TypeOf(nx); o.c.Unanswered(t) || t.Symbol == nil || !o.c.InProgram(t.Symbol.Declarations[0].Node) {
			return TypeAny, true
		}
	}
	// An array literal spreading a box, or holding one beside other
	// elements, is `any[]` (inferArrayTypeOf), whatever the elements'
	// other shapes.
	if lit, isLit := e.(*ast.ArrayLiteral); isLit && len(lit.Elements) > 0 {
		for _, el := range lit.Elements {
			if sp, isSp := el.(*ast.SpreadElement); isSp {
				if st, ok := o.ExprType(sp.Arg); ok && isUnconstrainedDynamic(st) {
					return ArrayOf(TypeAny), true
				}
				continue
			}
			if len(lit.Elements) > 1 && !plainLiteral(el) {
				if et, ok := o.ExprType(el); ok && isUnconstrainedDynamic(et) {
					return ArrayOf(TypeAny), true
				}
			}
		}
	}
	// An object literal with a getter or setter is an instance of the
	// synthetic class emission registers for it (inferObjectType,
	// ensureObjLitClass).
	if lit, isLit := e.(*ast.ObjectLiteral); isLit && lit.HasAccessors() && !lit.HasComputedKey() && !hasProtoKey(lit) && !objLitHasSpread(lit) && o.r.objLit != nil {
		if ct, ok := o.r.objLit(lit); ok {
			return ct, true
		}
	}
	// `a ?? b` likewise (nullCoalesceType).
	if b, isB := e.(*ast.BinaryExpression); isB && b.Op == "??" {
		lt, lok := o.heldOperand(b.Left)
		rt, rok := o.heldOperand(b.Right)
		if lok && rok {
			return nullCoalesceType(lt, rt), true
		}
	}
	// A name bound to a box, or to a scalar slot that holds undefined, is
	// read as its storage (identStorageType), whatever the checker knows
	// of the value; so is a leaf of a generator's destructured parameter,
	// which codegen destructures from the hidden boxed parameter
	// (desugarGeneratorPatternParams).
	if id, isID := e.(*ast.Identifier); isID && o.r.heldIdent != nil {
		if ht, ok := o.r.heldIdent(id); ok && (ht.IsDynamic() || isNullableScalar(ht) || o.genPatternLeaf(id)) {
			return ht, true
		}
	}
	// A dynamic entry's widened parameter is a box, and so is a member
	// read or a method call on it.
	// A member of an any-held binding the checker types a primitive reads
	// as that primitive (infer_structural's checkerPrimitive arm), unless
	// it is a dynamic entry's widened parameter.
	if m, isM := e.(*ast.MemberExpression); isM && o.r.checkerPrim != nil && o.r.widenedParam != nil && o.widenedUse(e) {
		if ot, ok := o.ExprType(m.Object); ok && isUnconstrainedDynamic(ot) {
			if pt, ok := o.r.checkerPrim(m); ok {
				return pt, true
			}
		}
	}
	if o.r.widenedParam != nil && o.widenedUse(e) && !o.narrowedBoxLocal(e) {
		// A widened rest parameter is bound as the array of boxes; a
		// parameter whose box knows its members (`number | undefined`), as
		// that box.
		if id, isID := e.(*ast.Identifier); isID && o.r.heldIdent != nil {
			if ht, ok := o.r.heldIdent(id); ok && (ht.IsArray() && ht.ElemType != nil && ht.ElemType.IsDynamic() || ht.IsDynamic() && len(ht.UnionMembers) > 0) {
				return ht, true
			}
		}
		return TypeAny, true
	}
	if call, isCall := e.(*ast.CallExpression); isCall && o.r.codegenCall != nil {
		if t, ok := o.r.codegenCall(call); ok {
			return t, true
		}
	}
	// A call of an erased generic method on a generic class's instance
	// (`als.run(store, cb)`'s `R`) is codegen's box (genericMethodResult).
	if call, isCall := e.(*ast.CallExpression); isCall && o.r.erasedCall != nil {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && !m.Optional && !call.Optional && o.mayBeGenericInstance(m.Object) {
			rt, rok := o.ExprType(m.Object)
			if !rok && o.r.inferred != nil {
				rt, rok = o.r.inferred(m.Object) // a generic class's instance
			}
			if rok && rt.IsClass() {
				if t, ok := o.r.erasedCall(call, rt); ok && !(isUnconstrainedDynamic(t) && o.c.TypeOf(call).Flags&checker.NumberLike != 0) {
					return t, true // a number result stays a number
				}
			}
		}
	}
	if ty, ok := o.tupleExprType(e); ok {
		return ty, true
	}
	// An array method that copies its receiver's elements (`filter`,
	// `slice`, `concat`) copies a boxed-element array's boxes, whatever
	// narrower type the checker gives the result.
	if call, isCall := e.(*ast.CallExpression); isCall {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && !m.Optional && elementCopyingMethod[m.Property] {
			if rt, ok := o.ExprType(m.Object); ok && rt.IsArray() && !rt.IsTypedArray() && rt.ElemType != nil && reprKey(*rt.ElemType) == reprKey(TypeAny) {
				if ct := o.c.TypeOf(call); !o.c.Unanswered(ct) && ct.Flags&checker.Object != 0 && ct.Kind == checker.Array && ct.Elem.Flags&checker.Any == 0 {
					return ArrayOf(TypeAny), true
				}
			}
		}
	}
	// A callback passed where an overloaded function's implementation takes
	// `any` (`dns.resolve6(h, opts, (e, a) => …)`) is boxed: its parameters
	// are boxes whichever overload the checker picked.
	if id, isID := e.(*ast.Identifier); isID {
		if o.anyImplCallbackParam(id) {
			return TypeAny, true
		}
	}
	// An unannotated callback parameter given a boxed-element array's
	// elements (`items.filter((x) => …)`) holds those boxes.
	if id, isID := e.(*ast.Identifier); isID {
		if el, ok := o.boxedElementParam(id); ok {
			return el, true
		}
	}
	// A non-null assertion of a box (a dictionary's view included) is that
	// box.
	if nn, isNN := e.(*ast.NonNullExpression); isNN {
		if it, ok := o.ExprType(nn.Arg); ok && (isUnconstrainedDynamic(it) || it.IsDynamic() && it.DynPropTy != nil && len(it.UnionMembers) == 0) {
			return it, true
		}
	}
	// … written with a string key (`Date["UTC"]`): the same closure.
	if ix, isIx := e.(*ast.IndexExpression); isIx && !ix.Optional && o.r.builtinRef != nil && !exprReceiverOrCallee(o.c.ParentOf(ix), ix) {
		if key, isStr := ix.Index.(*ast.StringLiteral); isStr && o.libGlobalRef(ix.Object) {
			if t, ok := o.r.builtinRef(ast.NewMemberExpression(ix.Object, key.Value, ix.GetPos())); ok {
				return t, true
			}
		}
	}
	// A builtin function an intrinsic implements, read as a value (`var
	// dp = Object.defineProperty`): the closure codegen builds for it.
	if m, isM := e.(*ast.MemberExpression); isM && !m.Optional && o.r.builtinRef != nil && (!exprReceiverOrCallee(o.c.ParentOf(m), m) || funcMetaReceiver(o.c.ParentOf(m), m)) && !o.boxedReceiver(m) && !(o.r.methodRecord != nil && o.r.methodRecord(m)) {
		if t, ok := o.r.builtinRef(m); ok {
			return t, true
		}
	}
	// A builtin type's method read as a value (`m.get`, `/x/.test`,
	// `process.send`) is a host function codegen boxes; a global
	// namespace's static (`Math.max`) is its intrinsic.
	if m, isM := e.(*ast.MemberExpression); isM && !m.Optional && o.c.LibraryMethod(m) && !o.loweredStatic(m) && (o.intrinsicKey(m) == "" || strings.HasPrefix(o.intrinsicKey(m), "crypto.")) {
		return TypeAny, true // `crypto` is an object: its methods are its own
	}
	// `yield*` over a generator is its return value, which the result
	// object carries in the element's slot: the element type (emitYieldStar).
	if y, isY := e.(*ast.YieldExpression); isY && y.Delegate && y.Argument != nil {
		if it, ok := o.ExprType(y.Argument); ok && it.IsGenerator() && it.GeneratorElemType != nil {
			return *it.GeneratorElemType, true
		}
	}
	// process.stdin/stdout/stderr: the stdio module's stream, boxed.
	if m, isM := e.(*ast.MemberExpression); isM && o.r.stdio != nil && (o.r.stdio(m) || o.r.stdio(m.Object)) {
		// … and its members, read off the box (checkerPrimitive): a pipe's
		// stream has no `isTTY`.
		return TypeAny, true
	}
	// A program class's method read as a value (`return this.#m`, `this.m`,
	// `A.prototype[1]`) is its function record, boxed (emitMember).
	if m, isM := e.(*ast.MemberExpression); isM && !m.Optional && o.r.methodValue != nil {
		if ot, ok := o.ExprType(m.Object); ok && o.r.methodValue(ot, m.Property) {
			return TypeAny, true
		}
	}
	if ix, isIx := e.(*ast.IndexExpression); isIx && !ix.Optional && o.r.methodValue != nil {
		if key, lit := literalKey(ix.Index); lit {
			if ot, ok := o.ExprType(ix.Object); ok && o.r.methodValue(ot, key) {
				return TypeAny, true
			}
		}
	}
	// An unannotated getter's read is what its body returns, as the
	// getter's emitted signature is.
	if m, isM := e.(*ast.MemberExpression); isM && !m.Optional && o.r.getterDecl != nil {
		if ot, ok := o.ExprType(m.Object); ok {
			if g := o.r.getterDecl(ot, m.Property); g != nil && g.ReturnType == nil && g.Body != nil {
				if rt, ok, hasValue := o.ReturnType(g.Body); ok && hasValue {
					return rt, true
				}
			}
		}
	}

	// A method an Array subclass inherits that returns `this` (`sort`,
	// `reverse`, `fill`, `copyWithin`) returns the receiver as the array
	// it is (the instance's array header, ADR-01469).
	if call, isCall := e.(*ast.CallExpression); isCall {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && !m.Optional && thisReturningArrayMethod[m.Property] {
			if rt := o.c.NonNullable(o.c.TypeOf(m.Object)); !o.c.Unanswered(rt) && rt.Flags&checker.Object != 0 && rt.Kind == checker.Instance {
				if el := o.c.IteratedType(rt); el != nil && !o.c.Unanswered(el) {
					if er, ok := o.r.of(el); ok {
						return ArrayOf(er), true
					}
				}
			}
		}
	}
	if call, isCall := e.(*ast.CallExpression); isCall {
		if tt, ok := o.assignResult(call); ok {
			return tt, true
		}
	}
	if call, isCall := e.(*ast.CallExpression); isCall {
		if ft, ok := o.overloadedClosureCall(call); ok {
			return ft, true
		}
	}
	// A call of an overloaded function runs its implementation: one
	// declared to return `any` returns a box, whichever overload the
	// checker picked (`execSync(cmd, { encoding })`).
	if call, isCall := e.(*ast.CallExpression); isCall {
		if id, isID := call.Callee.(*ast.Identifier); isID {
			if sym, _ := o.b.Resolve(id); sym != nil {
				for _, d := range sym.Declarations {
					if fd, isFD := d.Node.(*ast.FunctionDeclaration); isFD && len(fd.Overloads) > 0 && fd.ReturnType != nil && !fd.IsAsync && !fd.IsGenerator && o.voidOverValue(call, fd) {
						return TypeAny, true
					}
					if fd, isFD := d.Node.(*ast.FunctionDeclaration); isFD && len(fd.Overloads) > 0 && annotIs(fd.ReturnType, "any") && !fd.IsAsync && !fd.IsGenerator {
						if ct := o.c.TypeOf(call); o.c.Unanswered(ct) || ct.Flags&checker.Union != 0 && o.c.NonNullable(ct).Flags&checker.Union != 0 || o.objectResult(call) {
							return TypeAny, true // an overload of several kinds: the implementation's box
						}
					}

				}
			}
		}
	}
	// `Promise.all([...])` of a literal holding a thenable class instance
	// (unwrapped by its `then` at run time) resolves the literal's own
	// elements, boxed, not the checker's awaited ones.
	if call, isCall := e.(*ast.CallExpression); isCall && len(call.Args) == 1 && o.r.inferred != nil {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && o.intrinsicKey(m) == "Promise.all" {
			if lit, isLit := call.Args[0].(*ast.ArrayLiteral); isLit && o.holdsThenableInstance(lit) {
				if lt, lok := o.r.inferred(lit); lok && lt.IsArray() && lt.ElemType != nil && lt.ElemType.IsDynamic() {
					if ty, ok := o.r.of(o.c.TypeOf(call)); ok && ty.IsPromise() {
						at := lt
						ty.PromiseType = &at
						return ty, true
					}
				}
			}
		}
	}
	// A Promise static called on a subclass constructs that class
	// (promiseSubclassStatic); so does a binding of one.
	if call, isCall := e.(*ast.CallExpression); isCall {
		if it, ok := o.promiseSubclassStatic(call); ok {
			return it, true
		}
	}
	if id, isID := e.(*ast.Identifier); isID {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil && v.Kind == "const" {
				if call, isCall := v.Init.(*ast.CallExpression); isCall {
					if it, ok := o.promiseSubclassStatic(call); ok {
						return it, true
					}
				}
			}
		}
	}
	// `slice` on an instance of a class extending ArrayBuffer makes
	// another of that class (species).
	if call, isCall := e.(*ast.CallExpression); isCall && o.r.bufferClass != nil {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && m.Property == "slice" {
			if rt, rok := o.ExprType(m.Object); rok && rt.IsClass() && !rt.IsDynamic() && !rt.Nullable() && o.r.bufferClass(rt.ClassName) {
				return rt, true
			}
		}
	}
	// `then`/`catch`/`finally` on a Promise subclass's instance makes another
	// of that class (species), settling with the checker's value.
	if call, isCall := e.(*ast.CallExpression); isCall {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && (m.Property == "then" || m.Property == "catch" || m.Property == "finally") {
			if rt, rok := o.ExprType(m.Object); rok && rt.IsClass() && (rt.PromiseType != nil || o.r.promiseClass != nil && o.r.promiseClass(rt.ClassName) || o.extendsPromise(o.c.TypeOf(m.Object))) {
				if ty, ok := o.r.of(o.c.TypeOf(call)); ok && ty.IsPromise() {
					out := rt
					if o.r.species != nil {
						if ct, plain, sok := o.r.species(rt.ClassName); sok {
							if plain {
								return ty, true
							}
							out = ct
						}
					}
					if o.r.promiseErased != nil && o.r.promiseErased(out.ClassName) {
						out.PromiseType = &TypeAny
						return out, true
					}
					// A promise settling to nothing carries no value type
					// (promiseSubclassOf).
					out.PromiseType = nil
					if ty.PromiseType != nil && !ty.PromiseType.IsNever() {
						pt := *ty.PromiseType
						out.PromiseType = &pt
					}
					return out, true
				}
			}
		}
	}
	// `await p` of a promise codegen holds resolving a box is that box; so
	// is an unannotated binding of it.
	if aw := o.awaitedOperand(e); aw != nil {
		if pt, pok := o.ExprType(aw); pok && pt.IsPromise() && pt.PromiseType != nil && pt.PromiseType.IsDynamic() {
			return *pt.PromiseType, true
		}
	}
	// `f.bind(…)` is the value emitFunctionBind builds from f's.
	if call, isCall := e.(*ast.CallExpression); isCall && o.r.boundFunc != nil {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && m.Property == "bind" && !m.Optional && !call.Optional {
			if ft, fok := o.ExprType(m.Object); fok && ft.IsFunc() {
				if bt, bok := o.r.boundFunc(ft, len(call.Args)); bok {
					return bt, true
				}
			}
		}
	}
	if call, isCall := e.(*ast.CallExpression); isCall && o.boxedArrayCall(call) {
		return TypeAny, true
	}
	// `str.match(re)` builds exec's match array (its header carrying index
	// and input); a RegExp that may be global may give the plain array of
	// every match instead (emitStringMatch).
	// … as do its `!` and an unannotated binding of either, until
	// narrowed or reassigned.
	if t, ok := o.stringMatch(e); ok {
		return t, true
	}
	// A method with overloads runs its implementation: one declared to
	// return `any` or `Promise<any>` (SubtleCrypto's), where an overload
	// returns a promise, resolves a box.
	if call, isCall := e.(*ast.CallExpression); isCall {
		if m, isM := call.Callee.(*ast.MemberExpression); isM {
			fd, isFD := o.c.MemberDecl(m).(*ast.FunctionDeclaration)
			if !isFD && o.r.classMethod != nil {
				// Declared by an interface, run by the class codegen lays the
				// receiver out as.
				rt, rok := o.ExprType(m.Object)
				if !rok && o.r.inferred != nil {
					rt, rok = o.r.inferred(m.Object) // a receiver the checker types by a library interface
				}
				if rok && rt.IsClass() {
					fd = o.r.classMethod(rt.ClassName, m.Property)
					isFD = fd != nil
				}
			}
			if isFD && len(fd.Overloads) > 0 && fd.ReturnType != nil && !fd.IsAsync && !fd.IsGenerator {
				if annotIs(fd.ReturnType, "any") && (!o.overloadsAgree(call) && o.anyArgument(call) || o.objectResult(call)) {
					return TypeAny, true // the implementation's box
				}
				if o.voidOverValue(call, fd) {
					return TypeAny, true
				}
				if o.anyOrPromiseOfAny(fd) {
					if ty, ok := o.r.of(o.c.TypeOf(call)); ok && ty.IsPromise() {
						ty.PromiseType = &TypeAny
						return ty, true
					}
				}
			}
		}
	}
	// An object literal with a computed key is a dynamic object: a
	// dictionary of its values' one representation, else a box
	// (inferDynamicObjectType).
	if ol, isOL := e.(*ast.ObjectLiteral); isOL && ol.HasComputedKey() && (o.r.js == nil || !o.r.js()) {
		return o.computedKeyObject(ol)
	}
	// An array literal with an element codegen holds boxed though its
	// static type is narrower is an array of boxes.
	if al, isAL := e.(*ast.ArrayLiteral); isAL {
		for _, el := range al.Elements {
			if el == nil {
				continue
			}
			if _, spread := el.(*ast.SpreadElement); spread || plainLiteral(el) {
				continue
			}
			if et, ok := o.ExprType(el); ok && isUnconstrainedDynamic(et) {
				if t := o.c.TypeOf(el); !o.c.Unanswered(t) && t.Flags&(checker.Any|checker.Unknown) == 0 {
					return ArrayOf(TypeAny), true
				}
			}
		}
	}
	// A conditional with one branch an undefined-capable scalar slot (a
	// binding undefinedSlot holds) and the other that scalar is held as the
	// slot: its undefined survives as the conditional's value.
	if c, isC := e.(*ast.ConditionalExpression); isC {
		ct, cok := o.ExprType(c.Consequent)
		at, aok := o.ExprType(c.Alternate)
		if cok && aok {
			for _, p := range [][2]Type{{ct, at}, {at, ct}} {
				u, other := p[0], p[1]
				// A string's slot is its pointer either way: it keeps its
				// undefined only where the conditional may be undefined.
				if u.Nullable() && u.IsUndefined() && !u.NullAndUndef() && !u.IsDynamic() && !other.Nullable() &&
					reprKey(u.withoutNullable()) == reprKey(other) && (isNullableScalar(u) || isStringTy(u) && o.mayBeUndefined(c)) {
					return u, true
				}
			}
			// A branch held as a box makes the conditional one
			// (emitConditionalAny).
			if isUnconstrainedDynamic(ct) || isUnconstrainedDynamic(at) {
				return TypeAny, true
			}
			// Byte views of different kinds (a Buffer and a Uint8Array)
			// share a layout: the conditional is held as its consequent
			// (emitConditional).
			if (ct.IsBuffer() || ct.IsTypedArray()) && (at.IsBuffer() || at.IsTypedArray()) && ct.IsBuffer() != at.IsBuffer() && !ct.Nullable() && !at.Nullable() && byteElems(ct) && byteElems(at) {
				return ct, true
			}
			// An array-valued conditional's slot holds the branch it
			// picked, as its storage is (a narrowed field read keeps its
			// absent-capable storage; emitConditionalArray).
			if cb, ab := ct, at; ct.IsArray() && at.IsArray() && ct.Absent != at.Absent && func() bool { cb.Absent, ab.Absent = 0, 0; return reprKey(cb) == reprKey(ab) }() {
				if lit, ok := c.Consequent.(*ast.ArrayLiteral); ok && len(lit.Elements) == 0 {
					ct, at = at, ct
				}
				if aty, ok := ternaryArrayType(ct, at); ok {
					return aty, true
				}
			}
		}
	}
	// An assertion of a caught value keeps the caught pair (`err as Error`
	// is erased, as emitAsExpression erases it), and so does an untyped
	// binding it initializes.
	if as, isAs := e.(*ast.AsExpression); isAs {
		if inner, ok := o.ExprType(as.Expr); ok && inner.IsCaught() {
			return inner, true
		}
	}
	if id, isID := e.(*ast.Identifier); isID {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, ok := singleVarDecl(sym); ok && v.TypeAnnot == nil {
				if as, isAs := v.Init.(*ast.AsExpression); isAs {
					if inner, ok := o.ExprType(as.Expr); ok && inner.IsCaught() {
						return inner, true
					}
				}
			}
		}
	}
	// A -compat=js module var declared with values of different kinds is
	// one any box (crossTypeWidenedBindings).
	if id, isID := e.(*ast.Identifier); isID {
		if sym, _ := o.b.Resolve(id); sym != nil && o.jsWidenedVar(sym) {
			return TypeAny, true
		}
	}
	// A typed array's view properties, as emitTypedArrayViewProp reads them
	// (its buffer may be shared: a box).
	if m, isM := e.(*ast.MemberExpression); isM && o.typedArrayView(m) {
		if m.Property == "buffer" {
			return TypeAny, true
		}
		return TypeI64, true
	}
	// `X["k"]` on a builtin global reads as `X.k` (builtinLiteralMember).
	if ix, isIx := e.(*ast.IndexExpression); isIx && !ix.Optional {
		if id, isID := ix.Object.(*ast.Identifier); isID && knownGlobalNamespace[id.Name] {
			if lit, isLit := ix.Index.(*ast.StringLiteral); isLit && isIdentifierName(lit.Value) {
				if sym, _ := o.b.Resolve(id); sym != nil && o.r.libGlobal(sym) {
					if lit.Value == "prototype" {
						return TypeAny, true
					}
					t, ok := o.exprTypeRaw(e)
					if m := ast.NewMemberExpression(id, lit.Value, ix.GetPos()); ok && t.IsFunc() && o.intrinsicMethodValue(m) {
						t = o.intrinsicValueType(o.intrinsicKey(m), o.c.TypeOf(e), t)
					}
					return t, ok
				}
			}
		}
	}
	t, ok := o.exprTypeRaw(e)
	// … and an unannotated binding of one holds it so.
	if id, isID := e.(*ast.Identifier); ok && isID {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if v, isV := sym.Declarations[0].Node.(*ast.VarDeclaration); isV && v.Kind == "const" && v.TypeAnnot == nil {
				if m, isM := v.Init.(*ast.MemberExpression); isM && (o.intrinsicMethodValue(m) || o.typedArrayView(m)) {
					return o.ExprType(m)
				}
			}
		}
	}
	// A builtin object's intrinsic method as a value takes every argument
	// boxed, converting it as the builtin does (ensureBuiltinFunc).
	if m, isM := e.(*ast.MemberExpression); ok && isM && t.IsFunc() && o.intrinsicMethodValue(m) {
		t = o.intrinsicValueType(o.intrinsicKey(m), o.c.TypeOf(m), t)
	}
	// An unannotated function declaration whose body only throws returns
	// `void` to TypeScript; codegen gives it `never`, so its call is a
	// value in an operand position (ADR-00869).
	if id, isID := e.(*ast.Identifier); ok && isID && t.IsFunc() && t.FuncRetType != nil && t.FuncRetType.IR == "void" {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if fd, isFD := sym.Declarations[0].Node.(*ast.FunctionDeclaration); isFD && fd.ReturnType == nil && !fd.IsAsync && !fd.IsGenerator && blockAlwaysDiverges(fd.Body) {
				nt := TypeNever
				t.FuncRetType = &nt
			}
		}
	}
	// … and so does a direct call of one.
	if call, isCall := e.(*ast.CallExpression); ok && isCall && t.IsPromise() && t.PromiseType == nil {
		if id, isID := call.Callee.(*ast.Identifier); isID {
			if ft, fok := o.ExprType(id); fok && ft.IsFunc() && ft.FuncRetType != nil && ft.FuncRetType.IsPromise() && ft.FuncRetType.PromiseType != nil && ft.FuncRetType.PromiseType.IsNever() {
				t = *ft.FuncRetType
			}
		}
	}
	// … and an async one's promise settles with never.
	if id, isID := e.(*ast.Identifier); ok && isID && t.IsFunc() && t.FuncRetType != nil && t.FuncRetType.IsPromise() && t.FuncRetType.PromiseType == nil {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if fd, isFD := sym.Declarations[0].Node.(*ast.FunctionDeclaration); isFD && fd.ReturnType == nil && fd.IsAsync && !fd.IsGenerator && blockAlwaysDiverges(fd.Body) {
				rt := *t.FuncRetType
				nt := TypeNever
				rt.PromiseType = &nt
				t.FuncRetType = &rt
			}
		}
	}
	// Under -compat=js a mixed array literal, and an unannotated binding of
	// one, holds its elements in plain boxes (inferArrayType's any[]);
	// strict keeps the union.
	if ok && o.r.js != nil && o.r.js() && t.IsArray() && t.ElemType != nil && t.ElemType.IsDynamic() && len(t.ElemType.UnionMembers) > 0 && o.literalArray(e) {
		t = ArrayOf(TypeAny)
	}
	if ix, isIx := e.(*ast.IndexExpression); ok && isIx && t.IsDynamic() && len(t.UnionMembers) > 0 && o.literalArray(ix.Object) {
		if at, aok := o.ExprType(ix.Object); aok && at.IsArray() && at.ElemType != nil && reprKey(*at.ElemType) == reprKey(TypeAny) {
			t = TypeAny // … whose element reads are those boxes
		}
	}
	// … and a nested one's rows are its boxed rows (an unchecked read keeps
	// its possible undefined).
	if ix, isIx := e.(*ast.IndexExpression); ok && isIx && t.IsArray() && o.r.js != nil && o.r.js() && o.literalArray(ix.Object) {
		if at, aok := o.ExprType(ix.Object); aok && at.IsArray() && at.ElemType != nil && at.ElemType.IsArray() {
			row := *at.ElemType
			row.setNullable(t.Nullable())
			row.setUndefined(t.IsUndefined())
			row.UncheckedIndex = t.UncheckedIndex
			t = row
		}
	}
	// … and an unannotated binding of such a row read holds the read (a
	// `var` declared again is its first declaration's global).
	if id, isID := e.(*ast.Identifier); ok && isID && t.IsArray() && !t.Nullable() {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) > 0 {
			if v, isV := sym.Declarations[0].Node.(*ast.VarDeclaration); isV && v.TypeAnnot == nil {
				if ix, isIx := v.Init.(*ast.IndexExpression); isIx && (o.literalArray(ix.Object) || o.r.js == nil || !o.r.js()) {
					if it, iok := o.ExprType(ix); iok && it.IsArray() && it.Nullable() && it.UncheckedIndex {
						bare := it.withoutNullable()
						bare.UncheckedIndex = false
						if reprKey(bare) == reprKey(t) {
							return it, true
						}
					}
				}
			}
		}
	}
	return t, ok
}

// literalArray reports whether e is an array literal, or reads an
// unannotated binding initialized with one.
func (o frontOracle) literalArray(e ast.Expression) bool {
	switch x := e.(type) {
	case *ast.ArrayLiteral:
		return true
	case *ast.Identifier:
		if sym, _ := o.b.Resolve(x); sym != nil && len(sym.Declarations) == 1 {
			if v, isV := sym.Declarations[0].Node.(*ast.VarDeclaration); isV && v.TypeAnnot == nil {
				_, lit := v.Init.(*ast.ArrayLiteral)
				return lit
			}
		}
	}
	return false
}

// intrinsicMethodValue reports whether m names a builtin global object's
// intrinsic static method (`Math.max`, `Date.parse`).
func (o frontOracle) intrinsicMethodValue(m *ast.MemberExpression) bool {
	return o.intrinsicKey(m) != ""
}

// intrinsicKey is the intrinsic a builtin global object's static method m
// names, or "".
func (o frontOracle) intrinsicKey(m *ast.MemberExpression) string {
	id, isID := m.Object.(*ast.Identifier)
	if !isID {
		return ""
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil || !o.r.libGlobal(sym) {
		return ""
	}
	ms, isMS := o.c.MemberDecl(m).(*ast.MethodSignature)
	if !isMS || strings.Contains(ms.Intrinsic, ".prototype.") {
		return ""
	}
	return ms.Intrinsic
}

// loweredStatic reports a builtin global object's static method declared
// `@lower` (`Math.sqrt`): read as a value, the one shared function codegen
// makes for it (TDD-00230 P3.2), not a boxed host method.
func (o frontOracle) loweredStatic(m *ast.MemberExpression) bool {
	obj := m.Object
	if g, isG := obj.(*ast.MemberExpression); isG && !g.Optional {
		// `globalThis.Math.sqrt` reads the global `Math`'s.
		if id, isID := g.Object.(*ast.Identifier); isID && id.Name == "globalThis" {
			if sym, _ := o.b.Resolve(id); sym == nil || o.r.libGlobal(sym) {
				if gs := o.b.Globals.Symbols.Get(g.Property); gs == nil || !o.r.libGlobal(gs) {
					return false
				}
				ms, isMS := o.c.MemberDecl(m).(*ast.MethodSignature)
				return isMS && ms.Lower != ""
			}
		}
	}
	id, isID := obj.(*ast.Identifier)
	if !isID {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil || !o.r.libGlobal(sym) {
		return false
	}
	ms, isMS := o.c.MemberDecl(m).(*ast.MethodSignature)
	return isMS && ms.Lower != ""
}

func (o frontOracle) exprTypeRaw(e ast.Expression) (Type, bool) {
	// A generator method read off an object held boxed is a box, and so
	// is what calling a boxed generator function returns: a generator's
	// instance has no representation outside codegen's own generators.
	if m, ok := e.(*ast.MemberExpression); ok && isGeneratorFunc(o.c.TypeOf(m)) {
		if rt, ok := o.ExprType(m.Object); ok && isUnconstrainedDynamic(rt) {
			return TypeAny, true
		}
	}
	// A `const` holding a generic or overloaded function value reprOf cannot
	// name (`const sc = structuredClone`) holds its initializer's value.
	if id, ok := e.(*ast.Identifier); ok && isGenericFuncValue(o.c.TypeOf(id)) {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if v, isVar := sym.Declarations[0].Node.(*ast.VarDeclaration); isVar && v.Kind == "const" && v.TypeAnnot == nil && v.Init != nil {
				if t, ok := o.ExprType(v.Init); ok {
					return t, true
				}
			}
		}
	}
	// A `const` holding a collection iterator (`const names = m.keys()`)
	// holds its initializer's: the iterator keeps the collection it walks,
	// which its type (`MapIterator<string>`) no longer names. So does one
	// holding a generator's step (`const r = it.next()`): codegen's own
	// {value, done}, not the IteratorResult union its type names.
	if id, ok := e.(*ast.Identifier); ok {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if v, isVar := sym.Declarations[0].Node.(*ast.VarDeclaration); isVar && v.TypeAnnot == nil {
				init := v.Init
				if aw, isAwait := init.(*ast.AwaitExpression); isAwait {
					init = aw.Argument // an async generator's step, awaited
					// … or a `const` holding it (`const p = it.next(); await p`)
					if pid, isID := init.(*ast.Identifier); isID {
						if ps, _ := o.b.Resolve(pid); ps != nil && len(ps.Declarations) == 1 {
							if pv, isVar := ps.Declarations[0].Node.(*ast.VarDeclaration); isVar && pv.Kind == "const" && pv.TypeAnnot == nil && pv.Init != nil {
								if pc, isCall := pv.Init.(*ast.CallExpression); isCall && o.generatorStep(pc) {
									init = pc
								}
							}
						}
					}
				}
				if call, isCall := init.(*ast.CallExpression); isCall {
					step := o.generatorStep(call)
					if step && v.Kind != "const" && !o.stepsOnly(sym) {
						step = false
					}
					if step || v.Kind == "const" && collIterCall(call) {
						if t, ok := o.ExprType(v.Init); ok && (step || t.IsCollIter()) {
							return t, true
						}
					}
				}
			}
		}
	}
	// An async generator's step reaching a `.then` callback's parameter
	// (`g.next().then(r => r.value)`) is the same {value, done}: the
	// promise's own.
	if id, ok := e.(*ast.Identifier); ok {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if t, ok := o.thenStepParam(sym); ok {
				return t, true
			}
		}
	}
	// A call through a function value whose result is a box (a builtin's
	// closure, `const sc = structuredClone; sc(x)`) returns that box.
	if call, ok := e.(*ast.CallExpression); ok && !call.Optional {
		if id, isID := call.Callee.(*ast.Identifier); isID {
			if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
				if _, isVar := sym.Declarations[0].Node.(*ast.VarDeclaration); isVar {
					if ft, fok := o.ExprType(id); fok && ft.IsFunc() && ft.FuncRetType != nil && isUnconstrainedDynamic(*ft.FuncRetType) {
						if ct := o.c.TypeOf(call); o.c.Unanswered(ct) || !o.reprs(ct) {
							return TypeAny, true
						}
					}
				}
			}
		}
	}
	// A caught value's property other than its typed error fields is read
	// as a box (inferMemberType's caught record).
	if m, ok := e.(*ast.MemberExpression); ok && m.Property != "errors" {
		if ot, ook := o.ExprType(m.Object); ook && ot.IsCaught() {
			if _, _, typed := errorObjType.FieldIndex(m.Property); !typed || !caughtTypedField(m.Property) {
				return TypeAny, true
			}
		}
	}
	// A function value's property other than its own is read off the boxed
	// function.
	if m, ok := e.(*ast.MemberExpression); ok && !m.Optional && o.r.funcProp != nil && !o.classValue(m.Object) && !o.localClassValue(m.Object) {
		// An overloaded or generic function (`promisify`) is a function to
		// the checker whatever value codegen holds it as.
		if ot, ook := o.ExprType(m.Object); ook && ot.IsFunc() || o.checkerFunction(m.Object) {
			if st, sok := o.r.funcProp(m); sok {
				return st, true
			}
		}
	}
	// A js parameter the function's own type holds as a box (a default
	// codegen can't type it by) reads as that box.
	if id, ok := e.(*ast.Identifier); ok && o.r.js != nil && o.r.js() {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if pm := declaredParam(sym, id.Name); pm != nil && pm.Type == nil && pm.Default != nil {
				fn, _ := sym.Declarations[0].Node.(ast.Expression)
				if fn != nil {
					if ft, fok := o.ExprType(fn); fok && ft.IsFunc() {
						fs := sym.Declarations[0].Node
						var params []ast.Param
						switch f := fs.(type) {
						case *ast.ArrowFunction:
							params = f.Params
						case *ast.FunctionExpression:
							params = f.Params
						}
						for i := range params {
							if &params[i] == pm && i < len(ft.FuncParams) && isUnconstrainedDynamic(ft.FuncParams[i]) {
								return ft.FuncParams[i], true
							}
						}
					}
				}
			}
		}
	}
	// A binding of a bare `new Map()`/`new Set()` holds what its calls make
	// of it.
	if id, ok := e.(*ast.Identifier); ok && o.r.bareCollection != nil {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV {
				if t, tok := o.r.bareCollection(v); tok {
					return t, true
				}
			}
		}
	}
	// An immediately invoked generator function expression's instance is
	// held as a box.
	if iifeGenerator(e) {
		return TypeAny, true
	}
	// A property of a builtin constructor's or a class's prototype object
	// (`Array.prototype.map`, `D.prototype.hasOwnProperty`), which codegen
	// holds boxed, is read off the box.
	if m, ok := e.(*ast.MemberExpression); ok && !m.Optional {
		if pm, isPM := m.Object.(*ast.MemberExpression); isPM && pm.Property == "prototype" && !(m.Property == "length" && o.c.TypeOf(m).Flags&checker.Number != 0) {
			if ot, ook := o.ExprType(m.Object); ook && isUnconstrainedDynamic(ot) || o.userClassName(pm.Object) {
				return TypeAny, true
			}
		}
	}
	// A method called on an object held as a box is a dynamic call: its
	// result is a box, whatever the checker knows of the method.
	if call, ok := e.(*ast.CallExpression); ok && o.r.js != nil && o.r.js() {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && !o.localClassValue(m.Object) && !o.classValue(m.Object) {
			if rt, rok := o.ExprType(m.Object); rok && isUnconstrainedDynamic(rt) {
				return TypeAny, true
			}
		}
	}
	// A call the checker cannot type through a name codegen holds (a `var`
	// assigned a function later): a boxed name's call is dynamic, a
	// function value's returns what the function does.
	if call, ok := e.(*ast.CallExpression); ok && !call.Optional && o.c.Unanswered(o.c.TypeOf(call)) {
		if id, isID := call.Callee.(*ast.Identifier); isID {
			if ct, ok := o.ExprType(id); ok {
				if isUnconstrainedDynamic(ct) {
					return TypeAny, true
				}
				if ct.IsFunc() && ct.FuncRetType != nil {
					return *ct.FuncRetType, true
				}
			}
		}
	}
	if call, ok := e.(*ast.CallExpression); ok && isGeneratorInstance(o.c.TypeOf(call)) {
		if ct, ok := o.ExprType(call.Callee); ok && isUnconstrainedDynamic(ct) {
			return TypeAny, true
		}
		// A plain function declared to return a generator (one that
		// delegates to an inner generator, as timers/promises' setInterval
		// does): codegen holds the object it returns as a box.
		if id, isID := call.Callee.(*ast.Identifier); isID {
			if sym, found := o.b.Resolve(id); found && sym != nil && len(sym.Declarations) > 0 {
				if fd, isFD := sym.Declarations[0].Node.(*ast.FunctionDeclaration); isFD && !fd.IsGenerator && fd.ReturnType != nil {
					return TypeAny, true
				}
			}
		}
	}
	// A function's `prototype` (no lib member: codegen reads it off the
	// value at run time) is an `any`, as a class's prototype object is.
	if m, ok := e.(*ast.MemberExpression); ok && m.Property == "prototype" && o.userClassName(m.Object) {
		return TypeAny, true
	}
	// … and the class read for it, a value codegen boxes.
	if id, ok := e.(*ast.Identifier); ok && o.userClassName(id) {
		if m, isM := o.c.ParentOf(id).(*ast.MemberExpression); isM && m.Object == ast.Expression(id) && m.Property == "prototype" {
			return TypeAny, true
		}
	}
	if m, ok := e.(*ast.MemberExpression); ok && m.Property == "prototype" {
		if rt, ok := o.ExprType(m.Object); ok && (rt.IsFunc() || isUnconstrainedDynamic(rt)) && o.c.Unanswered(o.c.TypeOf(m)) {
			return TypeAny, true
		}
		// … and a builtin constructor's, which is no value yet.
		if id, ok := m.Object.(*ast.Identifier); ok {
			if sym, _ := o.b.Resolve(id); sym != nil && o.r.libGlobal(sym) {
				return TypeAny, true
			}
		}
	}
	// A well-known symbol (`Symbol.iterator`) is held boxed, and so is a
	// `const` initialized from one.
	if o.wellKnownSymbol(e) {
		return TypeAny, true
	}
	// A library global object read as a value (`Number`, `Object`, `Math`,
	// `Buffer`); a declared function is its function type's.
	if id, ok := e.(*ast.Identifier); ok && o.r.builtinValue != nil {
		if sym, _ := o.b.Resolve(id); sym != nil && o.r.libGlobal(sym) && (isLibObjectValue(o.c.TypeOf(id)) || isGenericFuncValue(o.c.TypeOf(id))) {
			if t, ok := o.r.builtinValue(id); ok {
				return t, true
			}
		}
	}
	// A library global as a receiver or a callee: the value codegen holds
	// (`Reflect`, a namespace object), else none (`process.pid`,
	// `setTimeout(…)`): codegen dispatches the member or the call by name,
	// its representation the default number.
	if id, ok := e.(*ast.Identifier); ok && o.r.builtinValue != nil {
		if sym, _ := o.b.Resolve(id); sym != nil && o.r.libGlobal(sym) && receiverOrCallee(o.c.ParentOf(id), id) {
			if t, held := o.r.builtinValue(id); held {
				return t, true
			}
			return TypeI64, true
		}
	}
	// A namespace and nothing else as a receiver (`ns.f`): codegen reads
	// the member's flat declaration, never the namespace itself.
	if id, ok := e.(*ast.Identifier); ok {
		if m, isM := o.c.ParentOf(id).(*ast.MemberExpression); isM && m.Object == ast.Expression(id) {
			if sym, _ := o.b.Resolve(id); sym != nil && sym.Flags&binder.NamespaceModule != 0 && sym.Flags&(binder.Function|binder.Class|binder.Enum|binder.Variable) == 0 {
				return TypeI64, true
			}
		}
	}
	// A lifted static generator method's function (liftStaticGenerators),
	// which no source declares.
	if id, ok := e.(*ast.Identifier); ok && o.r.generatorValue != nil && strings.Contains(id.Name, "__kml_sgen_") {
		if t, ok := o.r.generatorValue(id); ok {
			return t, true
		}
	}
	// A generator function read as a value: its constructor closure, boxed
	// (generatorValueType), whether or not codegen has registered it yet.
	if id, ok := e.(*ast.Identifier); ok && o.r.generatorValue != nil {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			// A parameter's declaration is its function too: not the
			// generator itself.
			param := sym.Flags&binder.FunctionScopedVariable != 0
			gen := false
			switch d := sym.Declarations[0].Node.(type) {
			case *ast.FunctionDeclaration:
				gen = d.IsGenerator && !param
				// A nested one that captures and reads its own value is
				// held in a cell, as a function (emitSelfReferencingGenerator).
				if gen && readsOwnValue(d) && sym.Scope != nil && sym.Scope.Kind != binder.ModuleScope && o.r.heldIdent != nil {
					if ht, hok := o.r.heldIdent(id); hok && ht.IsFunc() {
						return ht, true
					}
				}
			case *ast.VarDeclaration:
				// A top-level `var g = function* () {}` is a declaration
				// (rewriteTopLevelGeneratorExpressions), so registered
				// under its name.
				fe, isFE := d.Init.(*ast.FunctionExpression)
				gen = isFE && fe.IsGenerator && d.TypeAnnot == nil && sym.Scope != nil && sym.Scope.Kind == binder.ModuleScope
			case *ast.FunctionExpression:
				// Its own name in its body, renamed to the binding that
				// declaration took (renameGeneratorSelfName).
				gen = d.IsGenerator && !param
				if gen && o.r.heldIdent != nil {
					// Lifted to a nested declaration that captures: held
					// in a cell, as a function.
					if ht, hok := o.r.heldIdent(id); hok && ht.IsFunc() {
						return ht, true
					}
				}
			}
			if gen {
				return TypeAny, true
			}
		}
	}
	if id, ok := e.(*ast.Identifier); ok {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration); ok && v.Kind == "const" && v.TypeAnnot == nil && o.wellKnownSymbol(v.Init) {
				return TypeAny, true
			}
		}
	}
	// A function literal bound to `any` takes its arguments as one box.
	if id, ok := e.(*ast.Identifier); ok && id.Name == "arguments" {
		if sym, _ := o.b.Resolve(id); sym != nil && sym.Implicit && sym.Scope != nil && o.anyBound[sym.Scope.Node] {
			return TypeAny, true
		}
		// In the js lane, an object literal's method takes them as one box
		// too: such a method is a boxed object's dynamic function.
		if sym, _ := o.b.Resolve(id); sym != nil && sym.Implicit && sym.Scope != nil && o.r.js != nil && o.r.js() {
			if fe, isFE := sym.Scope.Node.(*ast.FunctionExpression); isFE {
				// A generator or an async method keeps its own convention (an
				// any[]).
				if _, inLit := o.c.ParentOf(fe).(*ast.ObjectLiteral); (fe.Method || inLit) && !fe.IsGenerator && !fe.IsAsync {
					return TypeAny, true
				}
			}
		}
		// A body `var arguments` leaves the arguments object bound (codegen
		// synthesizes it, the var's store reusing its slot).
		if o.synthesizedArguments(id) {
			return ArrayOf(TypeAny), true
		}
	}
	// The rest parameter of a function literal bound to `any` is one box.
	if id, ok := e.(*ast.Identifier); ok {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 && o.anyBound[sym.Declarations[0].Node] {
			var params []ast.Param
			switch f := sym.Declarations[0].Node.(type) {
			case *ast.ArrowFunction:
				params = f.Params
			case *ast.FunctionExpression:
				params = f.Params
			}
			if len(params) > 0 && params[len(params)-1].Rest && params[len(params)-1].Name == id.Name {
				return TypeAny, true
			}
		}
	}
	// A builtin whose result the lane holds otherwise than its lib type.
	if call, ok := e.(*ast.CallExpression); ok {
		if m, ok := call.Callee.(*ast.MemberExpression); ok {
			if in := intrinsics[o.intrinsicKey(m)]; in.held != nil {
				if t, ok := in.held(o, call); ok {
					return t, true
				}
			}
		}
	}
	// `f.call(…)` / `f.apply(…)` returns f's result, whatever the lane's
	// bind/call/apply checking.
	if call, ok := e.(*ast.CallExpression); ok {
		if m, ok := call.Callee.(*ast.MemberExpression); ok && (m.Property == "call" || m.Property == "apply") {
			if ft, ok := o.ExprType(m.Object); ok && ft.IsFunc() && ft.FuncRetType != nil && ft.FuncRetType.IR != "" {
				if ft.FuncRetType.IR == "void" {
					return TypeVoid, true
				}
				return *ft.FuncRetType, true
			}
		}
	}
	// A binding of such a group read holds that absence too.
	if id, ok := e.(*ast.Identifier); ok {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil {
				if ix, isIx := v.Init.(*ast.IndexExpression); isIx && !ix.Optional && o.matchArray(ix.Object) {
					if ty, ok := o.exprType(e); ok && !ty.IsDynamic() {
						return indexReadType(ty), true
					}
				}
			}
		}
	}
	// A nullish comparison's operand keeps the binding's declared
	// possibly-absent form: a narrowing by assignment from a read that may
	// miss (`b = map[k]`) leaves the run-time check to decide.
	if id, ok := e.(*ast.Identifier); ok && o.nullishCompared(id) && !(o.storesPresent(id) && o.reprStoresPresent(id)) {
		if dt, dok := o.declaredNullable(id); dok {
			if ty, ok := o.exprType(e); ok && !ty.Nullable() && !ty.IsDynamic() && ty.IR == dt.IR {
				return dt, true
			}
		}
	}
	// A regular-expression match's group may not have participated: an
	// element read, absent on a miss (as codegen reads any array element).
	if ix, ok := e.(*ast.IndexExpression); ok && o.matchArray(ix.Object) {
		if ty, ok := o.exprType(e); ok && !ty.IsDynamic() {
			return indexReadType(ty), true
		}
	}
	// An optional method a class leaves unimplemented (`m?(): T;`) reads as
	// its possibly-undefined slot, not a typed closure.
	if m, ok := e.(*ast.MemberExpression); ok {
		if fd, ok := o.c.MemberDecl(m).(*ast.FunctionDeclaration); ok && fd.IsOptional && fd.Body == nil {
			return Type{IR: irPtr, Absent: absenceOf(true, true, false)}, true
		}
	}
	// `a && b` of one pointer type is held in the left operand's slot: a
	// three-state left keeps its null/undefined marker, as `&&` can yield
	// that falsy side; `||` yields the left side only present
	// (shortCircuitValueType).
	if b, ok := e.(*ast.BinaryExpression); ok && b.Op == "&&" {
		lt, lok := o.ExprType(b.Left)
		ty, tok := o.exprType(e)
		if lok && tok && lt.NullAndUndef() && lt.IR == irPtr && ty.IR == irPtr && ty.Nullable() && !ty.NullAndUndef() && reprKey(lt.withoutNullable()) == reprKey(ty.withoutNullable()) {
			ty.setNullAndUndef(true)
			return ty, true
		}
	}
	// A property read through a caught value asserted to a type
	// (`(err as Error).message`) reads the caught value's own property,
	// which a thrown non-object lacks.
	if m, ok := e.(*ast.MemberExpression); ok {
		if as, ok := m.Object.(*ast.AsExpression); ok && o.caughtBinding(as) {
			if ty, ok := o.exprType(e); ok && ty.IR == irPtr && !ty.IsDynamic() && !ty.Nullable() {
				ty.setNullable(true)
				ty.setUndefined(true)
				return ty, true
			}
		}
	}
	// A comma expression is its last operand; `(super(…), …, this)`, an
	// inlined field initialization's, is the instance the call returns.
	if sq, ok := e.(*ast.SequenceExpression); ok && len(sq.Exprs) > 0 {
		if st, sok := o.superSequence(sq); sok {
			return st, true
		}
		return o.ExprType(sq.Exprs[len(sq.Exprs)-1])
	}
	if lit, ok := e.(*ast.ArrayLiteral); ok && o.r.js != nil && o.r.js() && o.jsBoxedArrayLiteral(lit) {
		return ArrayOf(TypeAny), true
	}
	// A literal of nested array literals that all hold one representation
	// (a js-lane boxed `[null, 0]` included) is an array of it.
	if lit, ok := e.(*ast.ArrayLiteral); ok && o.r.js != nil && o.r.js() && len(lit.Elements) > 0 {
		var et Type
		same := true
		for i, el := range lit.Elements {
			inner, isLit := el.(*ast.ArrayLiteral)
			if !isLit || len(inner.Elements) == 0 {
				same = false
				break
			}
			t, ok := o.ExprType(inner)
			if !ok || i > 0 && reprKey(t) != reprKey(et) {
				same = false
				break
			}
			et = t
		}
		if same {
			return ArrayOf(et), true
		}
	}
	if o.r.js != nil && o.r.js() {
		target := e
		if as, ok := e.(*ast.AssignmentExpression); ok && as.Op == "=" {
			target = as.Left
		}
		if as, ok := e.(*ast.AssignmentExpression); ok && target != e && o.jsUntypedVar(as.Left) {
			return TypeAny, true // an assignment's value is its target's slot
		}
	}
	ty, ok := o.exprType(e)
	if ok && ty.IsFunc() && o.r.js != nil && o.r.js() {
		ty = o.jsDefaultedParams(e, ty)
	}
	if call, isCall := e.(*ast.CallExpression); ok && isCall && !ty.IsDynamic() && o.r.js != nil && o.r.js() {
		// A call of a function literal binding returning its boxed parameter
		// is that box (jsDefaultedParams' result type).
		if id, isID := call.Callee.(*ast.Identifier); isID {
			if ft, fok := o.exprTypeRaw(id); fok && ft.IsFunc() && ft.FuncRetType != nil && isUnconstrainedDynamic(*ft.FuncRetType) {
				if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
					if v, vok := sym.Declarations[0].Node.(*ast.VarDeclaration); vok && v.Init != nil && (literalReturnsOnlyBoxedParam(v.Init, ft.FuncParams) || o.jsReturnsReboxed(v.Init)) {
						return TypeAny, true
					}
				}
			}
		}
	}
	// An element whose type the checker gives no layout for (a
	// `PromiseSettledResult` union) is the element the array holds.
	if ix, isIx := e.(*ast.IndexExpression); isIx && !ok && !ix.Optional {
		if at, aok := o.ExprType(ix.Object); aok && at.IsArray() && !at.IsDynamic() && !at.IsTuple() && at.ElemType != nil && at.DynPropTy == nil {
			return indexReadType(*at.ElemType), true
		}
	}
	return ty, ok
}

// jsUntypedVar reports a js-lane `var` every declaration of which has no
// initializer or annotation: held as `any`, whatever is stored to it.
func (o frontOracle) jsUntypedVar(e ast.Expression) bool {
	id, ok := e.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) == 0 {
		return false
	}
	for _, d := range sym.Declarations {
		v, ok := d.Node.(*ast.VarDeclaration)
		if !ok || v.Kind != "var" || v.Init != nil || v.TypeAnnot != nil {
			return false
		}
	}
	return true
}

// jsDefaultedParams: in the js lane a caller may pass anything, so an
// unannotated parameter is held as `any` unless its call sites agree on a
// primitive (jsSiteParam) or its default is a literal, array, object or
// module binding codegen types it by (paramDefaultType).
func (o frontOracle) jsDefaultedParams(e ast.Expression, ty Type) Type {
	var params []ast.Param
	switch f := e.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	case *ast.Identifier:
		// A binding of a function literal reads as the literal's value
		// (GlobalType's own answer for it).
		if sym, _ := o.b.Resolve(f); sym != nil && len(sym.Declarations) == 1 {
			if v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration); ok && v.TypeAnnot == nil && v.Init != nil {
				switch v.Init.(type) {
				case *ast.ArrowFunction, *ast.FunctionExpression:
					return o.jsDefaultedParams(v.Init, ty)
				}
			}
			// A declaration holds each defaulted parameter boxed, whatever
			// its default (jsCheckerParam leaves them to the box).
			if fd, ok := sym.Declarations[0].Node.(*ast.FunctionDeclaration); ok && fd.Name == f.Name && len(fd.Params) <= len(ty.FuncParams) {
				var out []Type
				for i, p := range fd.Params {
					if p.Type != nil || p.Default == nil || p.Rest || p.ArrayPattern != nil || p.ObjectPattern != nil {
						continue
					}
					if out == nil {
						out = append([]Type(nil), ty.FuncParams...)
					}
					out[i] = TypeAny
				}
				if out != nil {
					ty.FuncParams = out
					ty.FuncParamOptional = nil
					// Its result read off a boxed parameter alone (`return
					// a`) is that box.
					if returnsOnlyBoxedParam(fd, out) {
						rt := TypeAny
						ty.FuncRetType = &rt
					}
				}
			}
		}
		return ty
	default:
		return ty
	}
	if len(params) != len(ty.FuncParams) {
		return ty
	}
	var out []Type
	for i, p := range params {
		if p.Type != nil || p.Rest || p.ArrayPattern != nil || p.ObjectPattern != nil {
			continue
		}
		if p.Default == nil {
			// One its call sites type (jsArrowParam keeps only their one
			// primitive) is held boxed.
			t := o.c.JSCallSiteParam(e, i)
			if t == nil || t.Flags&checker.Union == 0 && t.Flags&(checker.String|checker.Number|checker.Boolean) != 0 || i >= len(ty.FuncParams) || ty.FuncParams[i].IsDynamic() {
				continue
			}
			if out == nil {
				out = append([]Type(nil), ty.FuncParams...)
			}
			out[i] = TypeAny
			continue
		}
		switch d := p.Default.(type) {
		case *ast.StringLiteral, *ast.TemplateLiteral, *ast.NumberLiteral, *ast.BooleanLiteral,
			*ast.ArrayLiteral, *ast.ObjectLiteral:
			continue
		case *ast.Identifier:
			// A module-level binding's type (moduleGlobals); not another
			// parameter's.
			if sym, _ := o.b.Resolve(d); sym != nil && sym.Scope == o.b.Module {
				continue
			}
		}
		if t := o.c.JSCallSiteParam(e, i); t != nil && t.Flags&checker.Union == 0 && t.Flags&(checker.String|checker.Number|checker.Boolean) != 0 {
			continue // jsSiteParam's primitive
		}
		if out == nil {
			out = append([]Type(nil), ty.FuncParams...)
		}
		out[i] = TypeAny
	}
	reboxed := map[string]bool{}
	for i, p := range params {
		if out != nil && i < len(out) && i < len(ty.FuncParams) && out[i].IsDynamic() && !ty.FuncParams[i].IsDynamic() {
			reboxed[p.Name] = true
		}
	}
	if out != nil {
		ty.FuncParams = out
	}
	// Its result read off a boxed parameter (`(p) => p`, `(p) => p.then(f)`)
	// is a box, whatever the checker's call sites make the parameter.
	if ty.FuncRetType != nil && !ty.FuncRetType.IsDynamic() && ty.FuncRetType.IR != "void" && (literalReturnsOnlyBoxedParam(e, ty.FuncParams) || returnsRead(e, reboxed)) {
		rt := TypeAny
		ty.FuncRetType = &rt
	}
	return ty
}

// jsReturnsReboxed reports a function literal whose result reads a
// parameter jsDefaultedParams holds boxed against its call sites' type.
func (o frontOracle) jsReturnsReboxed(e ast.Expression) bool {
	var params []ast.Param
	switch f := e.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	default:
		return false
	}
	names := map[string]bool{}
	for i, p := range params {
		if p.Type != nil || p.Default != nil || p.Rest || p.ArrayPattern != nil || p.ObjectPattern != nil {
			continue
		}
		if t := o.c.JSCallSiteParam(e, i); t != nil && !(t.Flags&checker.Union == 0 && t.Flags&(checker.String|checker.Number|checker.Boolean) != 0) && t.Flags&checker.Any == 0 {
			names[p.Name] = true
		}
	}
	return returnsRead(e, names)
}

func (o frontOracle) exprType(e ast.Expression) (Type, bool) {
	// A function declared more than once is its last declaration, as it
	// runs (tsc reads JavaScript's repeats as overloads), and so is a call
	// of it.
	if id, ok := e.(*ast.Identifier); ok {
		if fd := o.lastRedeclaredFunc(id); fd != nil {
			if st := o.c.FunctionSignature(fd); st != nil {
				if t, tok := o.r.of(st); tok {
					return t, true
				}
			}
		}
	}
	if call, ok := e.(*ast.CallExpression); ok && !call.Optional {
		if id, isID := call.Callee.(*ast.Identifier); isID {
			if fd := o.lastRedeclaredFunc(id); fd != nil {
				if ft, fok := o.ExprType(id); fok && ft.IsFunc() && ft.FuncRetType != nil {
					return *ft.FuncRetType, true
				}
			}
		}
	}
	// A parameter a body function declaration replaces holds that function
	// (tsc's TS2300 duplicate is any).
	if id, ok := e.(*ast.Identifier); ok {
		if fd := o.paramReplacingFunc(id); fd != nil {
			if st := o.c.FunctionSignature(fd); st != nil {
				if t, tok := o.r.of(st); tok {
					return t, true
				}
			}
		}
	}
	// `import(x)`: a task promise of the module object codegen lays out.
	if ic, ok := e.(*ast.ImportCallExpression); ok && o.r.importResult != nil {
		if t := o.c.TypeOf(ic); !o.c.Unanswered(t) {
			pt := PromiseOf(o.r.importResult(ic))
			pt.PromiseTask = true
			return pt, true
		}
	}
	// `process.env` is read through its own rewrite (processEnvIndexRewrite)
	// and held as a plain `any`.
	if m, ok := e.(*ast.MemberExpression); ok && m.Property == "env" {
		if id, ok := m.Object.(*ast.Identifier); ok && id.Name == "process" {
			return TypeAny, true
		}
	}
	// … and a variable of it is a string or undefined, by name or by a
	// computed key (processEnvIndexRewrite).
	if m, ok := e.(*ast.MemberExpression); ok {
		if env, ok := m.Object.(*ast.MemberExpression); ok && env.Property == "env" {
			if id, ok := env.Object.(*ast.Identifier); ok && id.Name == "process" {
				return undefinedableElem(TypePtr), true
			}
		}
	}
	if ix, ok := e.(*ast.IndexExpression); ok && !ix.Optional {
		if env, ok := ix.Object.(*ast.MemberExpression); ok && env.Property == "env" && !env.Optional {
			if id, ok := env.Object.(*ast.Identifier); ok && id.Name == "process" {
				return undefinedableElem(TypePtr), true
			}
		}
	}
	t := o.c.StorageTypeOf(e)
	if genericLiteral(e) {
		t = o.c.ErasedSignature(t) // compiled once, its type parameters any
	}
	if o.c.Unanswered(t) {
		return Type{}, false
	}
	ty, ok := o.r.of(t)
	// A `const` holding `a || b` is held in that expression's slot.
	if id, isID := e.(*ast.Identifier); isID && ok && ty.IR == irPtr && ty.Nullable() && !ty.NullAndUndef() {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if v, isVar := sym.Declarations[0].Node.(*ast.VarDeclaration); isVar && v.Kind == "const" && v.TypeAnnot == nil {
				if bin, isBin := v.Init.(*ast.BinaryExpression); isBin && (bin.Op == "||" || bin.Op == "&&") {
					if it, iok := o.ExprType(bin); iok && it.NullAndUndef() && reprKey(it.withoutNullable()) == reprKey(ty.withoutNullable()) {
						ty.setNullAndUndef(true)
					}
				}
			}
		}
	}
	if o.jsUntypedVar(e) {
		ty, ok = TypeAny, true // narrowed below like any other `any`
	}
	// A static read off a class value held as a box (one a function
	// returns, `const [A, AS] = make(1)`) is the box's dynamic read.
	if m, isM := e.(*ast.MemberExpression); isM && ok && !m.Optional {
		if _, isThis := m.Object.(*ast.ThisExpression); !isThis && o.classValue(m.Object) && !o.localClassValue(m.Object) {
			if rt, rok := o.ExprType(m.Object); rok && isUnconstrainedDynamic(rt) && rt.DynPropTy == nil {
				return TypeAny, true
			}
		}
	}
	// A property of a `this` held as a box (an object literal's method typed
	// by a contextual `this:` parameter) is the box's dynamic read.
	// A static member's `this` is its class, whose statics emission reads
	// directly.
	if m, isM := e.(*ast.MemberExpression); isM && ok {
		if _, isThis := m.Object.(*ast.ThisExpression); isThis && !o.classValue(m.Object) {
			if rt, rok := o.ExprType(m.Object); rok && isUnconstrainedDynamic(rt) && rt.DynPropTy == nil {
				return TypeAny, true
			}
		}
	}
	// A logical assignment whose value's type does not map (`b &&= f()`
	// with f void) is held in its target's slot, as emission stores it.
	if as, isAs := e.(*ast.AssignmentExpression); isAs && !ok && (as.Op == "&&=" || as.Op == "||=" || as.Op == "??=") {
		if lt, lok := o.ExprType(as.Left); lok {
			return lt, true
		}
	}
	// An array literal of builtin global functions (`[String, Number]`),
	// whose element type does not map: any[], each element boxed, as
	// codegen builds it.
	if lit, isLit := e.(*ast.ArrayLiteral); isLit && !ok && len(lit.Elements) > 0 {
		all := true
		for _, el := range lit.Elements {
			if id, isID := el.(*ast.Identifier); !isID || !o.isGlobalFunc(id) {
				all = false
				break
			}
		}
		if all {
			return ArrayOf(TypeAny), true
		}
	}
	if !ok {
		// A read off a box whose declared type does not map is the box's
		// dynamic read.
		if obj := dynamicReadReceiver(e); obj != nil {
			if rt, rok := o.ExprType(obj); rok && isUnconstrainedDynamic(rt) && rt.DynPropTy == nil && !o.localClassValue(obj) && !o.classValue(obj) && !o.libGlobalRef(obj) {
				return TypeAny, true
			}
		}
		return ty, ok
	}
	// A builtin global function as a value (`parseInt`) is called through
	// the boxed convention: every parameter `any`, its declared result.
	if id, isID := e.(*ast.Identifier); isID && ty.IsFunc() && o.isGlobalFunc(id) {
		params := make([]Type, len(ty.FuncParams))
		for i := range params {
			params[i] = TypeAny
		}
		ret := TypeVoid
		if ty.FuncRetType != nil {
			ret = *ty.FuncRetType
		}
		return FuncType(params, ret), true
	}
	// An executor's `reject` takes the rejection as a caught value, the
	// record a rejected promise holds (emitNewPromise).
	if id, isID := e.(*ast.Identifier); isID && ty.IsFunc() {
		if ne, i := o.executorParam(id); ne != nil && i == 1 {
			return FuncType([]Type{TypeCaught}, TypeVoid), true
		} else if ne != nil && i == 0 {
			// `resolve` takes the promise's value; a Promise<void>'s none.
			// A generic subclass's value is erased: any, as codegen
			// builds it.
			if ne.ClassName != "Promise" && o.genericClass(ne.ClassName) {
				rt := FuncType([]Type{TypeAny}, TypeVoid)
				rt.setKind(kindPromiseResolver, true)
				return rt, true
			}
			// An explicit type argument is the value its executor was
			// emitted with: a type parameter's is that instantiation's.
			if len(ne.TypeArgs) == 1 && o.r.promiseValue != nil {
				if vt, ok := o.r.promiseValue(ne); ok {
					if vt.IR == "void" || vt.IR == "" {
						return FuncType(nil, TypeVoid), true
					}
					rt := FuncType([]Type{vt}, TypeVoid)
					rt.setKind(kindPromiseResolver, true)
					return rt, true
				}
			}
			if pt := o.c.TypeOf(ne); !o.c.Unanswered(pt) {
				// A Promise subclass's instance: the Promise<T> it extends.
				for b := pt; b != nil && b.Flags&checker.Object != 0 && b.Kind == checker.Instance && b.Base != nil; b = b.Base {
					pt = b.Base
				}
				if r, ok := o.r.of(pt); ok && r.IsPromise() {
					if r.PromiseType == nil || r.PromiseType.IR == "void" || r.PromiseType.IR == "" {
						return FuncType(nil, TypeVoid), true
					}
					rt := FuncType([]Type{*r.PromiseType}, TypeVoid)
					rt.setKind(kindPromiseResolver, true)
					return rt, true
				}
			}
		}
	}
	// A callback parameter typed by an erased generic class's constructor
	// (`new Q<number>((r) => …)`): codegen reads the erased annotation.
	if id, isID := e.(*ast.Identifier); isID {
		if t, ok := o.erasedCtorCallbackParam(id); ok {
			return t, true
		}
	}
	// A read of a `then` callback's value parameter (thenValueBox).
	if id, isID := e.(*ast.Identifier); isID {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if pm := declaredParam(sym, id.Name); pm != nil {
				if fn, isFn := sym.Declarations[0].Node.(*ast.ArrowFunction); isFn && len(fn.Params) > 0 && &fn.Params[0] == pm {
					if bt, bok := o.thenValueBox(fn); bok {
						return bt, true
					}
				}
			}
		}
	}
	// A binding of such a `then`/`catch`/`finally` holds that instance.
	if id, isID := e.(*ast.Identifier); isID {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil {
				if call, isCall := v.Init.(*ast.CallExpression); isCall {
					if m, isM := call.Callee.(*ast.MemberExpression); isM && (m.Property == "then" || m.Property == "catch" || m.Property == "finally") {
						if it, iok := o.ExprType(call); iok && it.IsClass() {
							return it, true
						}
					}
				}
			}
		}
	}
	// The executor a Promise subclass passes to `super(…)` takes the same
	// resolve and reject (emitNewPromise).
	if id, isID := e.(*ast.Identifier); isID {
		if i := o.superExecutorParam(id); i == 1 {
			return FuncType([]Type{TypeCaught}, TypeVoid), true
		} else if i == 0 {
			if ft := o.c.TypeOf(id); !o.c.Unanswered(ft) && ft.Flags&checker.Object != 0 && ft.Kind == checker.Function && len(ft.Params) == 1 {
				for _, m := range checkerMembers(ft.Params[0]) {
					if m.Flags&(checker.Object|checker.Undefined|checker.Void) != 0 {
						continue
					}
					if vr, ok := o.r.of(m); ok {
						rt := FuncType([]Type{vr}, TypeVoid)
						rt.setKind(kindPromiseResolver, true)
						return rt, true
					}
				}
			}
		}
	}
	if id, isID := e.(*ast.Identifier); isID && o.isRejectionParam(id) {
		return TypeCaught, true
	}
	// An overload picked for an `any` argument is the first that fits any
	// value, not the one the run-time value takes: codegen holds the result
	// as `any` (checkerPrimitive).
	if call, ok := e.(*ast.CallExpression); ok && o.c.AnyArgOverloadCall(call) && ty.IR != "void" && !o.overloadsAgree(call) && (scalarTypeKind(ty) != "" || isStringTy(ty)) {
		return TypeAny, true // a void result is no value either way
	}
	// `a && b` / `a || b` / `a ?? b` with an operand held as `any`: the
	// value is either operand, so it is held as one (shortCircuitValueType).
	if b, ok := e.(*ast.BinaryExpression); ok && (b.Op == "&&" || b.Op == "||" || b.Op == "??") {
		lt, lok := o.ExprType(b.Left)
		rt, rok := o.ExprType(b.Right)
		if lok && rok && (isUnconstrainedDynamic(lt) || isUnconstrainedDynamic(rt)) {
			return TypeAny, true
		}
	}
	// A read through a receiver held as `any` is an `any` (the dynamic
	// member read), whatever the checker knows of the receiver's type.
	// … and through an un-narrowed union of scalars (a guard another
	// function may undo): its box answers.
	if m, ok := e.(*ast.MemberExpression); ok {
		if rt, ok := o.ExprType(m.Object); ok && rt.IsDynamic() && len(rt.UnionMembers) > 0 && isSelfDescribingBox(rt) && !unionAllHaveLength(rt) {
			if m.Property == "length" {
				return TypeF64, true
			}
			return TypeAny, true
		}
	}
	// A property of a union whose one object member declares it is read off
	// that member when the box holds it: a box (emitUnionSoleObjectMemberRead).
	// A property of a union of objects codegen holds in a shape the
	// mapping has none for is read as that shape reads it: no answer here.
	if m, ok := e.(*ast.MemberExpression); ok {
		if _, rok := o.ExprType(m.Object); !rok {
			if rt := o.c.NonNullable(o.c.TypeOf(m.Object)); !o.c.Unanswered(rt) && rt.Flags&checker.Union != 0 {
				return Type{}, false
			}
		}
	}
	if m, ok := e.(*ast.MemberExpression); ok && !m.Optional {
		if rt, ok := o.ExprType(m.Object); ok && rt.IsDynamic() && len(rt.UnionMembers) > 0 && !isSelfDescribingBox(rt) {
			if name, _, disc := unionDiscriminantField(rt); !disc || name != m.Property {
				var obj Type
				n := 0
				for _, um := range rt.UnionMembers {
					if unionMemberTag(um) == "object" {
						obj, n = um, n+1
					}
				}
				if n == 1 {
					if _, _, has := obj.FieldIndex(m.Property); has || obj.IsClass() {
						return TypeAny, true
					}
				}
			}
		}
	}
	// A property of a function value (`assert.throws`, set by `ok.throws =
	// throws`) lives in its boxed property table: read and called as a box.
	if call, ok := e.(*ast.CallExpression); ok {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && o.functionValueProp(m) {
			return TypeAny, true
		}
	}
	// A method call through a receiver held boxed is a dynamic call: a box.
	if call, ok := e.(*ast.CallExpression); ok {
		if m, isM := call.Callee.(*ast.MemberExpression); isM && !m.Optional {
			if rt, ok := o.ExprType(m.Object); ok && isUnconstrainedDynamic(rt) && rt.DynPropTy == nil && !o.localClassValue(m.Object) {
				if mt := o.c.TypeOf(m.Object); !o.c.Unanswered(mt) && mt.Flags&checker.Object != 0 && mt.ClassOf != nil {
					return TypeAny, true
				}
			}
		}
	}
	if obj := dynamicReadReceiver(e); obj != nil {
		if rt, ok := o.ExprType(obj); ok && isUnconstrainedDynamic(rt) && !(plainMemberRead(e) && o.narrowedToInstance(obj)) && !o.localClassValue(obj) && (!o.classValue(obj) || o.reassignableModuleBinding(obj)) && !o.libGlobalRef(obj) {
			// An index-signature view: every property has its value type,
			// absent on a miss (dynPropReadType).
			if rt.DynPropTy != nil {
				return dynPropReadType(*rt.DynPropTy), true
			}
			// A property the checker types as a number or boolean reads as
			// it; a string stays boxed, as it may be absent (checkerPrimitive).
			if m, isM := e.(*ast.MemberExpression); isM && !o.widenedUse(e) && (o.r.js == nil || !o.r.js()) && (o.r.stdio == nil || !o.r.stdio(m.Object)) {
				if nt := o.c.TypeOf(e); !o.c.Unanswered(nt) && nt.Flags&(checker.Union|checker.Any|checker.Unknown) == 0 {
					switch {
					case nt.Flags&(checker.Number|checker.NumberLiteral) != 0 && !nt.AnyArith:
						return TypeF64, true
					case nt.Flags&(checker.Boolean|checker.BooleanLiteral) != 0:
						return TypeBool, true
					}
				}
			}
			return TypeAny, true
		}
	}
	// A property read through an erased `x as C` reads the operand's
	// object: a property it lacks, or has optional, may be absent.
	if m, ok := e.(*ast.MemberExpression); ok && !ty.Nullable() && ty.IR == irPtr && !ty.IsDynamic() {
		if as, ok := m.Object.(*ast.AsExpression); ok && as.TypeAnnot != nil {
			if ot := o.c.TypeOf(as.Expr); !o.c.Unanswered(ot) {
				ot = o.c.NonNullable(ot)
				if ot.Flags&checker.Object != 0 && ot.Flags&checker.Union == 0 && (ot.Kind == checker.Instance || ot.Kind == checker.Interface) && ot.StringIndex == nil {
					if p := ot.Prop(m.Property); p == nil || p.Optional {
						ty.setNullable(true)
						ty.setUndefined(true)
					}
				}
			}
		}
	}
	// … and a `const` initialized from that read holds the same.
	if id, ok := e.(*ast.Identifier); ok && !ty.Nullable() && ty.IR == irPtr && !ty.IsDynamic() {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration); ok && v.Kind == "const" && v.TypeAnnot == nil {
				if m, ok := v.Init.(*ast.MemberExpression); ok {
					if _, ok := m.Object.(*ast.AsExpression); ok {
						if it, ok := o.ExprType(m); ok && it.Nullable() && reprKey(it.withoutNullable()) == reprKey(ty) {
							return it, true
						}
					}
				}
			}
		}
	}
	// A `catch (err)` binding holds the caught-value record (emitCatchBody);
	// `err as T` is erased and keeps it.
	if o.caughtBinding(e) {
		return TypeCaught, true
	}
	// `x as T` is erased (ADR-00371): a possibly-null pointer asserted to
	// the same shape without null stays possibly null at run time, where a
	// call or member read through it throws as in Node.
	if src := o.assertedOperand(e); src != nil && !ty.Nullable() && ty.IR == irPtr && !ty.IsDynamic() {
		if st, ok := o.ExprType(src); ok && st.Nullable() && st.IR == irPtr && !st.IsDynamic() &&
			st.IsFunc() == ty.IsFunc() && st.IsArray() == ty.IsArray() && st.IsObject() == ty.IsObject() {
			ty.setNullable(true)
			ty.setUndefined(st.IsUndefined())
		}
	}
	// The same for a scalar: `n as number` with `n: number | undefined`
	// still holds the absent value.
	if src := o.assertedOperand(e); src != nil && !ty.Nullable() && nonPtrScalar(ty) && !ty.IsDynamic() {
		if st, ok := o.ExprType(src); ok && isNullableScalar(st) && reprKey(st.withoutNullable()) == reprKey(ty) {
			return st, true
		}
	}
	// A nullable read every path to which stores a value held present is
	// its payload (storesPresent; an erased `x as T` of an absent value is
	// not one), or every path to which passes a guard excluding undefined
	// since the store, for a string (assignedString).
	if id, isID := e.(*ast.Identifier); isID && ty.Nullable() && !ty.IsDynamic() && (o.storesPresent(id) && o.reprStoresPresent(id) || isStringTy(ty) && ty.IsUndefined() && flowTrusted(o.b, id) && o.b.GuardedSinceStore(id)) {
		return ty.withoutNullable(), true
	}
	// A nullable scalar read where flow proves it present is its payload
	// (emitIdent).
	if id, isID := e.(*ast.Identifier); isID && isNullableScalar(ty) && !ty.UncheckedIndex && flowTrusted(o.b, id) {
		if nt, st := o.c.TypeOf(id), o.c.StorageTypeOf(id); !o.c.Unanswered(nt) && !o.c.Unanswered(st) && !presentType(st) && presentType(nt) {
			return ty.withoutNullable(), true
		}
	}
	// A nullable instance, function or promise reference flow proves
	// present reads as present (emitIdent's checkerPresentRef); an array
	// pattern's binding may be absent past the source's end.
	if presentRefCandidate(e, ty) && !o.arrayPatternBinding(e) && (!o.storesIndexRead(e) || ty.IsArray() && o.declaresAbsent(e)) && flowTrusted(o.b, e) {
		if nt := o.c.TypeOf(e); !o.c.Unanswered(nt) && presentType(nt) {
			return ty.withoutNullable(), true
		}
	}
	// A union variable flow narrows to one of its primitive members reads
	// as that member, unboxed (a symbol, its symbol object; a class
	// instance, `x instanceof A`, that instance; its array member, that
	// array).
	if id, isID := e.(*ast.Identifier); isID && ty.IsDynamic() && len(ty.UnionMembers) > 0 && flowTrusted(o.b, id) {
		if nt := o.c.TypeOf(id); !o.c.Unanswered(nt) && nt.Flags&checker.Union == 0 &&
			(nt.Flags&(checker.Boolean|checker.BooleanLiteral|checker.Number|checker.NumberLiteral|checker.String|checker.StringLiteral|checker.ESSymbol) != 0 ||
				nt.Flags&checker.Object != 0 && (nt.Kind == checker.Instance || nt.Kind == checker.Array || nt.Kind == checker.Tuple || nt.Kind == checker.Interface)) {
			if r, ok := o.r.of(nt); ok {
				for _, m := range ty.UnionMembers {
					if reprKey(m) == reprKey(r) {
						return r, true
					}
				}
			}
			// `Array.isArray` narrows to `any[]` (a `ReadonlyArray` member
			// too): the box's one plain array member.
			if nt.Flags&checker.Object != 0 && nt.Kind == checker.Array {
				var arr Type
				n := 0
				for _, m := range ty.UnionMembers {
					if m.IsArray() && !m.IsTypedArray() && !m.IsBuffer() {
						arr = m
						n++
					}
				}
				if n == 1 {
					return arr, true
				}
			}
		}
	}
	// A union-typed property read the checker narrows (`typeof o.v ===
	// "object" ? o.v.slice() : …`) reads as that member (inferRefined).
	if m, isM := e.(*ast.MemberExpression); isM && ty.IsDynamic() && len(ty.UnionMembers) > 0 && o.r.narrowedUnion != nil {
		if nt, ok := o.r.narrowedUnion(m, ty); ok {
			return nt, true
		}
	}
	// … to its promise member, that promise; to several of its members (a
	// typeof test ruling one out), the union of those, in declared order
	// (unionComplement).
	if id, isID := e.(*ast.Identifier); isID && ty.IsDynamic() && len(ty.UnionMembers) > 1 && flowTrusted(o.b, id) {
		if nt := o.c.TypeOf(id); !o.c.Unanswered(nt) && nt.Flags&checker.Nullish == 0 {
			if nt.Flags&checker.Union == 0 {
				if r, ok := o.r.of(nt); ok && r.IsPromise() {
					for _, m := range ty.UnionMembers {
						if reprKey(m) == reprKey(r) {
							return m, true
						}
					}
				}
				// … to its one function member (`typeof o === 'function'`),
				// that function.
				if nt.Flags&checker.Object != 0 && nt.Kind == checker.Function {
					var fn []Type
					for _, m := range ty.UnionMembers {
						if m.IsFunc() {
							fn = append(fn, m)
						}
					}
					if len(fn) == 1 {
						return fn[0], true
					}
				}
			} else if sub, ok := o.unionSubset(ty, nt); ok {
				return sub, true
			}
		}
	}
	// An `any` variable a typeof test narrows to a boolean, number, string
	// or symbol reads unboxed (a bigint stays boxed).
	if id, isID := e.(*ast.Identifier); isID && ty.IsDynamic() && len(ty.UnionMembers) == 0 && flowTrusted(o.b, id) {
		if nt := o.c.TypeOf(id); !o.c.Unanswered(nt) && nt.Flags&(checker.Boolean|checker.BooleanLiteral|checker.Number|checker.NumberLiteral|checker.String|checker.StringLiteral|checker.ESSymbol) != 0 && nt.Flags&checker.Union == 0 {
			if r, ok := o.r.of(nt); ok {
				return r, true
			}
		}
		// … or to a class instance (`x instanceof C`): the instance.
		if nt := o.c.TypeOf(id); !o.c.Unanswered(nt) && nt.Flags&checker.Object != 0 && nt.Flags&checker.Union == 0 && nt.Kind == checker.Instance &&
			nt.Symbol != nil && len(nt.TypeArgs) == 0 && o.r.narrowClass != nil {
			if r, ok := o.r.narrowClass(nt.Symbol); ok {
				return r, true
			}
		}
	}
	if as, isAs := e.(*ast.AsExpression); isAs && ty.IsObject() {
		// An assertion between two static object types keeps the operand's
		// representation; only one out of a dynamic value converts it.
		if ot, ok := o.ExprType(as.Expr); ok && ot.IsObject() && !ot.IsDynamic() && !ot.IsDynamicObject() {
			// The same layout under an interface's name (`new RangeError()
			// as Coded`) takes the name: its own members are read through
			// it (nativeIfaceMember).
			if ty.RefName != "" && ot.RefName == "" && ty.StructIR() == ot.StructIR() {
				return ty, true
			}
			return ot, true
		}
	}
	if lit, isLit := e.(*ast.ObjectLiteral); isLit && ty.IsObject() {
		ty = o.literalGenFields(lit, o.literalStorage(lit, ty))
	}
	// A binding of such a literal holds its layout.
	if id, isID := e.(*ast.Identifier); isID && ty.IsObject() {
		if sym, _ := o.b.Resolve(id); sym != nil {
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil {
				if lit, isLit := v.Init.(*ast.ObjectLiteral); isLit {
					ty = o.literalGenFields(lit, o.literalStorage(lit, ty))
				}
			}
		}
	}
	if o.recordArrayRead(e) {
		ty.setNullable(true)
		ty.setUndefined(true) // a missing key reads undefined
	}
	switch arr, typed := o.elementRead(e); {
	case typed && arr.ElemType != nil:
		if _, isIndex := e.(*ast.IndexExpression); isIndex {
			return indexReadType(*arr.ElemType), true // the native element
		}
		if id, isID := e.(*ast.Identifier); isID && o.storesPresent(id) && o.reprStoresPresent(id) {
			break // a store since that holds a present value (`n = n * 256`)
		}
		ty = indexReadType(ty)
		ty.UncheckedIndex = false // a local holds the number it converted to
	case arr.IsArray():
		if arr.DynPropTy != nil && ty.IR != irPtr {
			break // a scalar record value holds its own absence (dynPropReadType)
		}
		if _, isIndex := e.(*ast.IndexExpression); isIndex && arr.DynPropTy == nil && arr.ElemType != nil && arr.ElemType.IsDynamic() && !ty.IsDynamic() {
			// Elements held boxed (`(number | undefined)[]`, a literal with
			// holes): the read is the element's box, its own absence
			// included.
			ty = *arr.ElemType
			break
		}
		ty = indexReadType(ty)
		if arr.DynPropTy != nil {
			ty.UncheckedIndex = false // dynPropReadType's absence
		}
		if _, isID := e.(*ast.Identifier); isID && ty.IsClass() {
			ty.UncheckedIndex = false // a local holds the instance pointer
		}
		// A string binding read past a guard excluding undefined since its
		// store is the payload (assignedString).
		if id, isID := e.(*ast.Identifier); isID && isStringTy(ty) && ty.Nullable() && ty.IsUndefined() && flowTrusted(o.b, id) && o.b.GuardedSinceStore(id) {
			ty = ty.withoutNullable()
			ty.UncheckedIndex = false
		}
	}
	return ty, true
}

// libGlobalRef reports a reference to a library global (`Math`, `Buffer`):
// its members are its declaration's, however the global itself is held.
func (o frontOracle) libGlobalRef(e ast.Expression) bool {
	id, ok := e.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := o.b.Resolve(id)
	return sym != nil && o.r.libGlobal(sym)
}

// dynamicReadReceiver is the receiver of a member or element read.
func dynamicReadReceiver(e ast.Expression) ast.Expression {
	switch x := e.(type) {
	case *ast.MemberExpression:
		return x.Object
	case *ast.IndexExpression:
		return x.Object
	}
	return nil
}

// caughtBinding reports a catch clause's own binding, read directly or
// through an erased `err as T`.
func (o frontOracle) caughtBinding(e ast.Expression) bool {
	for as, ok := e.(*ast.AsExpression); ok && as.TypeAnnot != nil; as, ok = e.(*ast.AsExpression) {
		if kw, isKw := as.TypeAnnot.(*ast.KeywordType); isKw && (kw.Keyword == "any" || kw.Keyword == "unknown") {
			return false // boxed into the box the assertion names
		}
		e = as.Expr
	}
	id, ok := e.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil || sym.Scope == nil || sym.Scope.Kind != binder.CatchScope {
		return false
	}
	// The parameter itself, not a variable the catch body declares.
	for _, d := range sym.Declarations {
		if _, isVar := d.Node.(*ast.VarDeclaration); isVar {
			return false
		}
	}
	return true
}

// presentRefCandidate reports a nullable instance, function, promise,
// object or array reference, read as a variable or a property: one flow may prove
// present (checkerPresentRef).
func presentRefCandidate(e ast.Expression, ty Type) bool {
	switch e.(type) {
	case *ast.Identifier, *ast.MemberExpression:
	default:
		return false
	}
	return ty.IR == irPtr && ty.Nullable() && !ty.IsDynamic() && !ty.UncheckedIndex &&
		(ty.IsClass() || ty.IsFunc() || ty.IsPromise() || ty.IsObject() || ty.IsArray())
}

// arrayPatternBinding reports a name an array pattern declares.
func (o frontOracle) arrayPatternBinding(e ast.Expression) bool {
	id, ok := e.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil {
		return false
	}
	for _, d := range sym.Declarations {
		if _, ok := d.Node.(*ast.ArrayDestructuring); ok {
			return true
		}
	}
	return false
}

// recordArrayRead reports an array value read from a string-keyed record
// (`m[k]` with `m: Record<string, T[]>`), directly or as the initializer of
// an unannotated variable e names: a missing key is a null array.
func (o frontOracle) recordArrayRead(e ast.Expression) bool {
	switch x := e.(type) {
	case *ast.IndexExpression:
		ot := o.c.TypeOf(x.Object)
		if o.c.Unanswered(ot) {
			return false
		}
		rt, ok := o.r.of(ot)
		vt, isDict := dictValType(rt)
		return ok && isDict && vt.IsArray()
	case *ast.Identifier:
		sym, _ := o.b.Resolve(x)
		if sym == nil || len(sym.Declarations) != 1 {
			return false
		}
		v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration)
		if !ok || v.TypeAnnot != nil || v.Init == nil {
			return false
		}
		if ix, ok := v.Init.(*ast.IndexExpression); ok {
			return o.recordArrayRead(ix)
		}
	}
	return false
}

// elementRead is the array e reads an element of (`a[i]`), directly or as
// the initializer of an unannotated variable e names, and whether it is a
// typed array or Buffer: the value may be undefined (an index past the
// end) though tsc types it as the element type.
func (o frontOracle) elementRead(e ast.Expression) (Type, bool) {
	switch x := e.(type) {
	case *ast.IndexExpression:
		ot := o.c.TypeOf(x.Object)
		if o.c.Unanswered(ot) {
			return Type{}, false
		}
		// `a?.[i]` is the element read of the array itself; a short circuit
		// yields undefined, the slot an out-of-range read already holds
		// (emitOptionalIndex).
		if x.Optional {
			ot = o.c.NonNullable(ot)
		}
		rt, ok := o.r.of(ot)
		if vt, isDict := dictValType(rt); ok && isDict {
			// A string-keyed record's missing key reads undefined too; an
			// array value's absence is its null header (recordRead).
			if vt.IsArray() {
				return Type{}, false
			}
			rec := ArrayOf(vt)
			rec.DynPropTy = &vt // marks a record read: absent, not unchecked
			return rec, false
		}
		if !ok || !rt.IsArray() || rt.IsTuple() {
			return Type{}, false
		}
		return rt, rt.IsTypedArray() || rt.IsBuffer()
	case *ast.Identifier:
		sym, _ := o.b.Resolve(x)
		if sym == nil || len(sym.Declarations) != 1 {
			return Type{}, false
		}
		v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration)
		if !ok || v.TypeAnnot != nil || v.Init == nil {
			return Type{}, false
		}
		switch v.Init.(type) {
		case *ast.IndexExpression, *ast.ConditionalExpression:
			return o.elementRead(v.Init)
		}
	case *ast.ConditionalExpression:
		// `c ? a[i] : -1`: the read's absence survives a number on the
		// other side, as the conditional's value.
		for _, p := range [][2]ast.Expression{{x.Consequent, x.Alternate}, {x.Alternate, x.Consequent}} {
			arr, typed := o.elementRead(p[0])
			if !arr.IsArray() {
				continue
			}
			ot := o.c.StorageTypeOf(p[1])
			if o.c.Unanswered(ot) {
				continue
			}
			if r, ok := o.r.of(ot); ok && (nonPtrScalar(r) || arr.ElemType != nil && !r.IsDynamic() && reprKey(*arr.ElemType) == reprKey(r)) {
				return arr, typed
			}
		}
	}
	return Type{}, false
}

func (o frontOracle) AnnotationType(ta ast.TypeNode) (Type, bool) {
	n := ta
	if n == nil {
		return Type{}, false
	}
	// An iteration-protocol interface (`AsyncIterableIterator<T>`) or
	// `PromiseLike<T>` outside a generator's own signature is any object
	// with the protocol's members: a box, driven by the run-time protocol.
	if r, ok := ast.NonNullish(n).(*ast.TypeReference); ok && len(r.Qualifier) == 0 && (generatorWrapperNames[r.Name] || r.Name == "PromiseLike") && o.r.userType != nil && !o.r.userType(r.Name) {
		if ms, _, _ := ast.NonNullishMembers(n); len(ms) == 1 {
			return TypeAny, true
		}
	}
	// A rewrite's `any` (a reference node naming the keyword): the box.
	if r, ok := n.(*ast.TypeReference); ok && r.Name == "any" && len(r.Qualifier) == 0 && len(r.TypeArgs) == 0 {
		return TypeAny, true
	}
	// An intersection with a module's type (`import("tty").ReadStream & {
	// fd: 0 }`), or an alias of one, is held boxed: read by its run-time
	// kind.
	if importIntersection(n) || o.importAlias(n) {
		return TypeAny, true
	}
	if it, isI := n.(*ast.IntersectionType); isI {
		for _, m := range it.Types {
			if o.importAlias(m) {
				return TypeAny, true
			}
		}
	}
	// A tuple with a rest element (`[number, ...string[]]`) has no fixed
	// shape: an array of boxed elements, as the annotation converts. One
	// whose every spread is a type parameter the instantiation binds to a
	// tuple (`[...A, L]`) is that tuple's elements in place.
	if tt, isT := n.(*ast.TupleType); isT {
		if t, ok := o.splicedTuple(tt); ok {
			return t, true
		}
		for _, el := range tt.Elements {
			_, rest := el.(*ast.RestType)
			if nm, named := el.(*ast.NamedTupleMember); named && nm.Rest {
				rest = true
			}
			if rest {
				return ArrayOf(TypeAny), true
			}
		}
	}
	// `T[K]` in an instantiation binding T to an object and K to a string
	// key (`pluck<T, K extends keyof T>`): the field type every key shares,
	// else a box, as annotationType reads it.
	if ia, isIA := n.(*ast.IndexedAccessType); isIA && o.r.typeParam != nil {
		on, ook := bareTypeRefName(ia.ObjectType)
		kn, kok := bareTypeRefName(ia.IndexType)
		if ook && kok {
			if _, arr := ia.ObjectType.(*ast.ArrayType); !arr {
				ot, okT := o.r.typeParam(on)
				kt, okK := o.r.typeParam(kn)
				if okT && okK && ot.IsObject() && !ot.IsDynamic() && kt.IR == irPtr && isStringTy(kt) && !kt.IsStrLiteral() {
					var common *Type
					same := true
					for i := range ot.Fields {
						f := ot.Fields[i]
						if strings.HasPrefix(f.Name, "__kml_") {
							continue
						}
						if common == nil {
							common = &ot.Fields[i].Ty
						} else if reprKey(*common) != reprKey(f.Ty) {
							same = false
						}
					}
					if common != nil && same {
						return *common, true
					}
					return TypeAny, true
				}
			}
		}
	}
	// An assertion signature (`asserts value is T`) returns nothing; a
	// type predicate (`x is A`), a boolean.
	if p, isP := n.(*ast.TypePredicate); isP {
		if p.Asserts {
			return TypeVoid, true
		}
		return TypeBool, true
	}
	// The polymorphic `this` of a class's method is the class's instance
	// (the annotation names the class).
	if ast.IsKeywordNode(n, "this") && o.r.class != nil && o.b.Module != nil {
		if sym := o.b.Module.Symbols.Get(o.thisClassName(n)); sym != nil {
			if ct, ok := o.r.class(sym); ok {
				return ct, true
			}
		}
	}
	// The empty object type `{}` (alone, or a union member: `AddressInfo |
	// {}`) admits any non-nullish value: held as the box annotationType gives
	// it.
	if emptyTypeLiteral(n) {
		return TypeAny, true
	}
	if u, isU := n.(*ast.UnionType); isU {
		for _, m := range u.Types {
			if emptyTypeLiteral(m) {
				return TypeAny, true
			}
		}
	}
	var t *checker.Type
	var names []string
	var binds []*checker.Type
	scope := o.b.Module
	if params := o.genericAliasParams(n); params != nil {
		// A generic alias's body, instantiated: its parameters are the
		// instantiation's (annotationType binds them).
		t = o.c.TypeFromNodeWithParams(n, scope, params)
	} else if names = o.boundTypeParams(); len(names) > 0 {
		// An annotation in a generic body names the instantiation's type
		// parameters (annotationType reads them from its scope); one bound to
		// a primitive is that type, so a type computed from it evaluates
		// (`Awaited<T>`, `T extends string ? … : …`).
		binds = o.primitiveBindings(names, hasConditional(n))
		t = o.c.TypeFromNodeWithBindings(n, scope, names, binds)
		if binds == nil {
			binds = make([]*checker.Type, len(names))
		}
	} else {
		t = o.c.TypeFromNode(n, scope)
		if o.c.Unanswered(t) && mentionsThisType(n) {
			// An interface member's own `this` (`self(): this`), read
			// outside the interface: its polymorphic `this`, unbound.
			t = o.c.TypeFromNodeWithBindings(n, scope, []string{"this"}, []*checker.Type{o.c.ThisType()})
		}
	}
	// A type parameter no instantiation binds (an erased class's, a
	// generic arrow's or method's in a body compiled once): erased, the
	// box annotationType holds it as, wherever the annotation names it
	// (`call<T>(run: (done: (v: T) => void) => void): Promise<T>`).
	if o.c.Unanswered(t) && o.r.erasedTPNames != nil {
		bound := map[string]bool{}
		for _, nm := range names {
			bound[nm] = true
		}
		all, allB := append([]string(nil), names...), append([]*checker.Type(nil), binds...)
		for _, nm := range o.r.erasedTPNames() {
			if !bound[nm] {
				all, allB = append(all, nm), append(allB, o.c.AnyType())
			}
		}
		if len(all) > len(names) && !bound["this"] && mentionsThisType(n) {
			// (With an interface member's own `this`: `extend<T>(p: T):
			// this & T`.)
			all, allB = append(all, "this"), append(allB, o.c.ThisType())
		}
		if len(all) > len(names) {
			t = o.c.TypeFromNodeWithBindings(n, scope, all, allB)
		}
		// An access indexed by one (`AbortSignalEventMap[K]`): every key's
		// member, as annotationType reads it.
		if o.c.Unanswered(t) && len(all) > len(names) {
			erased := map[string]bool{}
			for _, nm := range all[len(names):] {
				erased[nm] = true
			}
			if en := erasedIndexAccess(n, erased); en != n {
				t = o.c.TypeFromNodeWithBindings(en, scope, all, allB)
			}
		}
	}
	// Read where it is written: a function's own class (`const b: A`
	// naming a class declared in the body) is not the module's, and its
	// name as written, before a rewrite renamed it.
	if o.c.Unanswered(t) {
		if sc := o.b.AnnotationScope(n); sc != nil && sc != scope {
			t = o.c.TypeFromNode(n, sc)
		}
	}
	if o.c.Unanswered(t) && o.r.warmAnn != nil {
		if wt, ok := o.r.warmAnn(n); ok {
			t = wt
		}
	}
	if o.c.Unanswered(t) {
		return Type{}, false
	}
	if t.Flags&checker.Object != 0 && t.Kind == checker.Tuple && len(t.Elems) == 0 {
		return TupleType(nil), true // `[]`: an empty struct
	}
	// `keyof T` of a type codegen cannot list the keys of (an erased type
	// parameter's): a string key.
	if op, isOp := n.(*ast.TypeOperator); isOp && op.Operator == "keyof" && t.Flags&checker.Union != 0 {
		keys := true
		for _, m := range t.Types {
			keys = keys && m.Flags&(checker.String|checker.Number|checker.ESSymbol) != 0
		}
		if keys {
			return TypePtr, true
		}
	}
	if q, isQ := n.(*ast.TypeQuery); isQ && o.boxedValueQuery(q) {
		return TypeAny, true // `typeof E`, `typeof N`: its object, held boxed
	}
	if t.Flags&checker.StringLiteral != 0 {
		// An annotation keeps its string literal (a discriminant's tag).
		return Type{IR: irPtr, Kind: kindStrLiteral, LitValue: t.Value}, true
	}
	r := o.r
	r.literals, r.tuples = true, true
	if at := o.enclosingRecursiveAlias(n, t); at != nil {
		// Inside a recursive alias's own declaration annotationType still
		// holds the alias's placeholder: its self-reference has no fields.
		r.mapping = map[*checker.Type]bool{at: true}
	}
	ty, ok := r.of(t)
	if op, isOp := n.(*ast.TypeOperator); !ok && isOp && op.Operator == "keyof" {
		return TypePtr, true // a key of a type left open: a string
	}
	if !ok {
		return ty, ok
	}
	nullable, undef, _ := annotAbsence(ta)
	if nullable && ty.Kind == kindNone && ty.IR != "void" && !ty.Nullable() {
		ty.setNullable(true)
		ty.setUndefined(undef)
	}
	if nullable && ty.IsDynamic() && len(ty.UnionMembers) > 0 && !ty.Nullable() {
		// An optional parameter's union (`options?: AddEventListenerOptions
		// | boolean`): the box annotationType marks absent.
		ty.setNullable(true)
	}
	return annotationNullish(ta, ty), true
}

// byteElems reports whether a view's elements are bytes.
func byteElems(t Type) bool { return t.ElemType != nil && t.ElemType.IR == "i8" }

// genericAliasParams is the type parameter names of the module-level generic
// alias whose body holds the type node n, else nil.
// boundTypeParams are the type parameters emission has bound.
func (o frontOracle) boundTypeParams() []string {
	if o.r.typeParamNames == nil {
		return nil
	}
	return o.r.typeParamNames()
}

// primitiveBindings is each bound type parameter's checker type where its
// instantiation is a plain primitive, else nil.
func (o frontOracle) primitiveBindings(names []string, conditional bool) []*checker.Type {
	if o.r.typeParam == nil {
		return nil
	}
	out := make([]*checker.Type, len(names))
	for i, name := range names {
		tt, ok := o.r.typeParam(name)
		if !ok || tt.Nullable() {
			continue
		}
		if tt.IsDynamic() && len(tt.UnionMembers) == 0 && tt.Kind == kindDynamic {
			// Bound to the box: any, as an erased parameter reads.
			out[i] = o.c.AnyType()
			continue
		}
		if kw, bare := typeNodeOf(tt).(*ast.KeywordType); bare && kw.Keyword == "string" && conditional {
			// A string binding may be a widened literal (`K` of `w("a")`),
			// which a conditional tests: left erased, as any (both
			// branches).
			out[i] = o.c.AnyType()
			continue
		}
		if nd := typeNodeOf(tt); nd != nil && !tt.IsStrLiteral() {
			// A primitive, promise or array of them (`Awaited<T>` for T =
			// Promise<number> is number).
			if bt := o.c.TypeFromNode(nd, o.b.Module); !o.c.Unanswered(bt) {
				out[i] = bt
			}
			continue
		}
		if tt.Host != 0 {
			continue
		}
		// A string literal (`K` of `pluck(p, "z")`), a declared object or
		// class (`T` of it): the checker's own, so `T[K]` evaluates.
		var ref ast.TypeNode
		switch {
		case tt.IsStrLiteral():
			ref = &ast.LiteralType{Kind: "string", Value: tt.LitValue}
		case tt.Kind == kindClass && tt.ClassName != "":
			ref = &ast.TypeReference{Name: tt.ClassName}
		case tt.IsObject() && !tt.IsDynamic() && tt.RefName != "":
			ref = &ast.TypeReference{Name: tt.RefName}
		}
		if ref != nil {
			if bt := o.c.TypeFromNode(ref, o.b.Module); !o.c.Unanswered(bt) {
				out[i] = bt
			}
			continue
		}
		if tt.Kind != kindNone {
			continue
		}
		switch {
		case tt.IR == "double" && tt.Float:
			out[i] = o.c.PrimitiveType("number")
		case tt.IR == "i1":
			out[i] = o.c.PrimitiveType("boolean")
		case tt.IR == "void":
			out[i] = o.c.PrimitiveType("void")
		}
	}
	return out
}

func (o frontOracle) genericAliasParams(n ast.Node) []string {
	prog, ok := o.b.Module.Node.(*ast.Program)
	if !ok {
		return nil
	}
	for _, st := range prog.Body {
		ad, ok := st.(*ast.TypeAliasDeclaration)
		if !ok || len(ad.TypeParams) == 0 || ad.Type == nil {
			continue
		}
		inside := false
		ast.Inspect(ad.Type, func(m ast.Node) bool {
			if m == n {
				inside = true
			}
			return !inside
		})
		if inside {
			return ad.TypeParams
		}
	}
	return nil
}

// enclosingRecursiveAlias is the recursive alias (checker.RecursiveAlias)
// whose own declaration holds the type node n, when t mentions it.
func (o frontOracle) enclosingRecursiveAlias(n ast.Node, t *checker.Type) *checker.Type {
	var cands []*checker.Type
	note := func(x *checker.Type) {
		for _, m := range append([]*checker.Type{x}, x.Types...) {
			if m.RecursiveAlias() {
				cands = append(cands, m)
			}
		}
	}
	note(t)
	for _, p := range t.Props {
		note(p.Type)
	}
	for _, at := range cands {
		ad, ok := at.Symbol.Declarations[0].Node.(*ast.TypeAliasDeclaration)
		if !ok || ad.Type == nil {
			continue
		}
		inside := false
		ast.Inspect(ad.Type, func(m ast.Node) bool {
			if m == n {
				inside = true
			}
			return !inside
		})
		if inside {
			return at
		}
	}
	return nil
}

// growableBuffer marks a buffer constructed with `{maxByteLength}` growable,
// at the construction and at reads of an unannotated binding of it: a fact
// about the value codegen keeps on its type.
func (o frontOracle) growableBuffer(e ast.Expression, t Type) Type {
	if !t.IsArrayBuffer() {
		return t
	}
	var growable func(x ast.Expression, depth int) bool
	growable = func(x ast.Expression, depth int) bool {
		if nb, isBuf := x.(*ast.NewArrayBufferExpression); isBuf {
			return nb.MaxByteLength != nil
		}
		// A call of a program function each of whose returns is one
		// (`function make(n, m) { return new ArrayBuffer(n, {maxByteLength: m}); }`).
		call, isCall := x.(*ast.CallExpression)
		if !isCall || depth > 3 {
			return false
		}
		id, isID := call.Callee.(*ast.Identifier)
		if !isID {
			return false
		}
		sym, _ := o.b.Resolve(id)
		if sym == nil || len(sym.Declarations) != 1 {
			return false
		}
		fd, isFD := sym.Declarations[0].Node.(*ast.FunctionDeclaration)
		if !isFD || fd.Body == nil || fd.ReturnType != nil || fd.IsGenerator || fd.IsAsync {
			return false
		}
		all, any := true, false
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch r := n.(type) {
			case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration:
				return false
			case *ast.ReturnStatement:
				any = true
				all = all && r.Value != nil && growable(r.Value, depth+1)
			}
			return all
		})
		return all && any
	}
	if id, isID := e.(*ast.Identifier); isID {
		// A binding every declaration of initializes with a growable buffer
		// (`var b = new ArrayBuffer(1, {maxByteLength})` declared again).
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) > 0 {
			all := true
			for _, d := range sym.Declarations {
				v, isV := d.Node.(*ast.VarDeclaration)
				if !isV || v.TypeAnnot != nil || !growable(v.Init, 0) {
					all = false
					break
				}
			}
			if all {
				t.BufferGrowable = true
				return t
			}
		}
	}
	switch e.(type) {
	case *ast.NewArrayBufferExpression, *ast.CallExpression:
		t.BufferGrowable = growable(e, 0)
	}
	return t
}

// GlobalType answers the module-global pre-pass: a module variable is promotable
// when its declared type has a representation.
func (o frontOracle) GlobalType(v *ast.VarDeclaration) (Type, bool, bool) {
	// An alias of an unannotated object literal's binding (`const q = a`)
	// holds that binding's object, in its layout.
	if id, isID := v.Init.(*ast.Identifier); isID && v.TypeAnnot == nil && o.b != nil {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if src, isVar := sym.Declarations[0].Node.(*ast.VarDeclaration); isVar && src != v && src.TypeAnnot == nil {
				if _, lit := src.Init.(*ast.ObjectLiteral); lit {
					// Its layout, promoted when this binding is read by a
					// function (`const p = fs.promises`, which the resolver
					// names as the library's own binding), whether or not the
					// literal's own binding is.
					st, _, sok := o.GlobalType(src)
					if !sok {
						return st, false, false
					}
					return st, o.readInFunctions(v.Name) || o.r.anyHeld != nil && o.r.anyHeld(v), true
				}
			}
		}
	}
	// An initialized `var` sharing its name with a top-level function is one
	// binding: a global any, holding the function until the `var` runs.
	if v.Kind == "var" && v.Init != nil && v.TypeAnnot == nil && o.topFunction(v.Name) {
		return TypeAny, true, true
	}
	ty, ok, decided := o.globalType(v)
	// A binding no function reads stays a main() local: as a global it would
	// behave the same, and its own declaration lays it out.
	// (A binding JavaScript holds as any, assigned another kind anywhere, is
	// a global whether or not a function reads it.)
	if ok && decided && !o.readInFunctions(v.Name) && !(o.r.anyHeld != nil && o.r.anyHeld(v)) {
		return ty, false, true
	}
	if _, isM := v.Init.(*ast.MemberExpression); isM && ok && v.TypeAnnot == nil && ty.IsFunc() && ty.FuncCountsArgs {
		ty.FuncCountsArgs = false // a bound method value (exprTypeBase)
	}
	if ok && v.TypeAnnot == nil {
		switch init := v.Init.(type) {
		case *ast.ObjectLiteral:
			ty = o.literalMethodDefaults(init, ty)
			ty = o.literalMethodReturns(init, ty)
		case *ast.ArrayLiteral:
			// An array of object literals (`[{ n: 1, d() { … } }, …]`):
			// its element, as a literal's binding holds it.
			if ty.IsArray() && ty.ElemType != nil && ty.ElemType.IsObject() && len(init.Elements) > 0 {
				if first, lit := init.Elements[0].(*ast.ObjectLiteral); lit && literalHasMethod(first) {
					if el, eok := o.ExprType(first); eok && el.IsObject() && sameFieldNames(el, *ty.ElemType) {
						ty = ArrayOf(el)
					}
				}
			}
		case *ast.Identifier, *ast.FunctionExpression:
			if ty.IsFunc() {
				if params := o.declParams(init); params != nil {
					ty = o.callerDefaultParams(ty, params)
				}
			}
		}
	}
	if ok && v.TypeAnnot == nil {
		ty = o.growableBuffer(v.Init, ty)
	}
	if ok && ty.IR == "void" {
		ty = TypeUndefined // a void call's result, stored as undefined
	}
	return ty, ok, decided
}

func (o frontOracle) globalType(v *ast.VarDeclaration) (Type, bool, bool) {
	if accessorLiteral(o.b, v) {
		return TypeAny, true, true // a dynamic object, whose reads call its accessors
	}
	if c, isCall := v.Init.(*ast.CallExpression); isCall && (c.Intrinsic == ast.ModuleNamespaceIntrinsic || c.Intrinsic == ast.LiveBindingsIntrinsic) {
		return TypeAny, true, true // a module namespace object, one box (typeOfExpr)
	}
	if v.TypeAnnot == nil && o.r.widened != nil && o.r.widened(v.Name) || o.r.anyHeld != nil && o.r.anyHeld(v) {
		return TypeAny, true, true
	}
	// Under -compat=js a copy of a boxed binding (`var e = d` where d is
	// later assigned null) holds the same box …
	if id, isID := v.Init.(*ast.Identifier); isID && v.TypeAnnot == nil && o.r.js != nil && o.r.js() {
		if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) == 1 {
			if src, isVar := sym.Declarations[0].Node.(*ast.VarDeclaration); isVar && src.TypeAnnot == nil && src != v {
				if o.r.widened != nil && o.r.widened(src.Name) || o.r.anyHeld != nil && o.r.anyHeld(src) {
					return TypeAny, true, true
				}
				// … and a copy of one whose initializer is held dynamically
				// (the js lane's nested-array boxes), that binding's layout.
				if _, isID := src.Init.(*ast.Identifier); !isID && o.dynamicInit(src) {
					if st, _, ok := o.globalType(src); ok {
						return st, true, true
					}
				}
			}
		}
	}
	switch v.Init.(type) {
	case *ast.NewWeakSetExpression, *ast.NewWeakMapExpression:
		if o.r.bareCollection != nil {
			if t, ok := o.r.bareCollection(v); ok {
				return t, true, true // seeded from null (bareCollectionType)
			}
		}
	}
	if iifeGenerator(v.Init) && v.TypeAnnot == nil {
		return TypeAny, true, true
	}
	// A program generic class's `new`: the monomorph emission builds.
	if ne, isNew := v.Init.(*ast.NewExpression); isNew && v.TypeAnnot == nil && o.r.genericNew != nil {
		if gt, ok := o.r.genericNew(ne); ok {
			return gt, true, true
		}
	}
	// A redeclared function, or its call: its last declaration's.
	if v.TypeAnnot == nil {
		callee := v.Init
		if call, isCall := callee.(*ast.CallExpression); isCall && !call.Optional {
			callee = call.Callee
		}
		if id, isID := callee.(*ast.Identifier); isID && o.lastRedeclaredFunc(id) != nil {
			if it, ok := o.ExprType(v.Init); ok {
				return it, true, true
			}
		}
	}
	// A collection's or matchAll's lazy iterator, or a generator's step,
	// as codegen names it.
	if call, isCall := v.Init.(*ast.CallExpression); isCall && v.TypeAnnot == nil {
		if it, ok := o.ExprType(call); ok && (it.IsCollIter() || o.generatorStep(call) && (v.Kind == "const" || o.stepsOnlyDecl(v))) {
			return it, true, true
		}
		// Such an iterator's `next()`: its {value, done} step.
		if m, isM := call.Callee.(*ast.MemberExpression); isM && m.Property == "next" && len(call.Args) == 0 {
			if it, ok := o.ExprType(m.Object); ok && it.IsCollIter() {
				return collIterResultType(it), true, true
			}
		}
	}
	if call, isCall := v.Init.(*ast.CallExpression); isCall && v.TypeAnnot == nil {
		if ft, ok := o.overloadedClosureCall(call); ok {
			return ft, true, true
		}
		if tt, ok := o.assignResult(call); ok && !tt.IsArray() {
			return tt, true, true
		}
	}
	// A builtin function held in a variable (`var dp =
	// Object.defineProperty`): the closure emission builds for it.
	if v.TypeAnnot == nil && v.Init != nil && o.r.builtinRef != nil {
		if bt, ok := o.r.builtinRef(v.Init); ok {
			return bt, true, true
		}
	}
	// Under -compat=js an unannotated function literal is the function as
	// emission compiles it (its literal's answer, literalParamType).
	if v.TypeAnnot == nil && o.r.js != nil && o.r.js() {
		switch v.Init.(type) {
		case *ast.ArrowFunction, *ast.FunctionExpression:
			if fe, isFE := v.Init.(*ast.FunctionExpression); !isFE || !fe.IsGenerator {
				if ft, ok := o.ExprType(v.Init); ok && ft.IsFunc() {
					return ft, true, true
				}
			}
		}
	}
	// A generic function literal is its erased signature (genericLiteral).
	if v.TypeAnnot == nil && genericLiteral(v.Init) && isGenericFuncValue(o.c.TypeOf(v.Init)) {
		if ft, ok := o.ExprType(v.Init); ok && ft.IsFunc() {
			return ft, true, true
		}
	}
	// In either lane, a function literal reading `arguments` is the
	// function as emission compiles it, its synthetic rest included.
	if fe, isFE := v.Init.(*ast.FunctionExpression); isFE && v.TypeAnnot == nil && !fe.IsGenerator && fe.Body != nil && hasArgumentsRestParamSlice(maybeAddArgumentsRestParams(fe.Params, fe.Body.Body)) {
		if ft, ok := o.ExprType(fe); ok && ft.IsFunc() {
			return ft, true, true
		}
	}
	// A const object literal whose type does not map (an overloaded
	// function's value) is the literal's answer (literalFields), and so is
	// one with a method, which may take its `this` (FuncThis).
	// A literal with a computed or spread key depends on its scope: it
	// stays a local (promotableInitInPrePass).
	if lit, isLit := v.Init.(*ast.ObjectLiteral); isLit && v.TypeAnnot == nil && scopedLiteralKey(lit) {
		return Type{}, false, true
	}
	// So does an array literal with a spread (promotableInitInPrePass).
	if lit, isLit := v.Init.(*ast.ArrayLiteral); isLit && v.TypeAnnot == nil {
		for _, el := range lit.Elements {
			if _, sp := el.(*ast.SpreadElement); sp {
				return Type{}, false, true
			}
		}
	}
	if lit, isLit := v.Init.(*ast.ObjectLiteral); isLit && v.TypeAnnot == nil && v.Kind != "const" && (o.r.js == nil || !o.r.js()) {
		// A var or let one with a method is the literal's answer (a method
		// taking its `this`); one whose type does not map, its fields'.
		if literalHasMethod(lit) {
			if lt, ok := o.ExprType(lit); ok {
				return lt, true, true
			}
		}
		if _, mapped := o.r.of(o.c.TypeOf(lit)); !mapped {
			if lt, ok := o.literalFields(lit); ok {
				return lt, true, true
			}
		}
	}
	if lit, isLit := v.Init.(*ast.ObjectLiteral); isLit && v.TypeAnnot == nil && v.Kind == "const" && (o.r.js == nil || !o.r.js()) {
		if literalHasMethod(lit) {
			if lt, ok := o.ExprType(lit); ok {
				return lt, true, true
			}
		}
		if _, mapped := o.r.of(o.c.TypeOf(lit)); !mapped || genFuncProp(o.c.TypeOf(lit)) {
			if lt, ok := o.literalFields(lit); ok {
				return lt, true, true
			}
		}
	}
	// A function literal with a destructured parameter its caller fills
	// (callSiteDefault) is its literal's answer, which drops that absence.
	if v.TypeAnnot == nil {
		switch v.Init.(type) {
		case *ast.ArrowFunction, *ast.FunctionExpression:
			for _, p := range o.declParams(v.Init) {
				if callSiteDefault(p) != nil {
					if ft, ok := o.ExprType(v.Init); ok && ft.IsFunc() {
						return ft, true, true
					}
					break
				}
			}
		}
	}
	if o.r.bareCollection != nil {
		if t, ok := o.r.bareCollection(v); ok {
			return t, true, true
		}
	}
	// A generator function expression is held boxed (a generator method
	// read off its object), whatever the checker makes of its body.
	if fe, ok := v.Init.(*ast.FunctionExpression); ok && fe.IsGenerator && v.TypeAnnot == nil {
		// A top-level one is a generator declaration, registered under its
		// name (rewriteTopLevelGeneratorExpressions).
		if o.r.generatorValue != nil {
			if t, ok := o.r.generatorValue(ast.NewIdentifier(v.Name, v.GetPos())); ok {
				return t, true, true
			}
		}
		return TypeAny, true, true
	}
	if o.dynamicInit(v) {
		if accessorLiteral(o.b, v) {
			return TypeAny, true, true
		}
		t, ok := o.ExprType(v.Init)
		return t, ok, true
	}
	if o.evolvingBoxed(v) {
		return ArrayOf(TypeAny), true, true
	}
	if _, isLit := v.Init.(*ast.ObjectLiteral); isLit && v.TypeAnnot == nil && o.r.js != nil && o.r.js() {
		return TypeAny, true, true // the js lane's dynamic object
	}
	if lit, isLit := v.Init.(*ast.ObjectLiteral); isLit && v.TypeAnnot == nil && hasAccessor(lit) && o.r.objLit != nil && (o.r.js == nil || !o.r.js()) {
		if ct, cok := o.r.objLit(lit); cok {
			return ct, true, true // its synthetic class (TDD-00153)
		}
	}
	// An unannotated binding of a plain function literal holds that
	// function's value (its result as its body returns it).
	if v.TypeAnnot == nil {
		switch fl := v.Init.(type) {
		case *ast.ArrowFunction, *ast.FunctionExpression:
			rt, rok := o.inferredBodyReturn(fl)
			if !rok {
				rt, rok = o.asyncBodyReturn(fl)
			}
			if rok && (rt.Nullable() || isUnconstrainedDynamic(rt)) {
				if ft, fok := o.ExprType(v.Init); fok && ft.IsFunc() {
					return ft, true, true
				}
			}
		}
	}
	// An unannotated binding with no initializer is held `any` (its
	// narrower types are flow's, per read), in either lane.
	if v.Init == nil && v.TypeAnnot == nil && !v.Ambient {
		if o.b != nil {
			if sym, found := o.b.DeclSymbol(v); found && laterVarDecl(sym, v) {
				return Type{}, false, true // a later `var` is a store to the first
			}
		}
		return TypeAny, true, true
	}
	// An unannotated binding of a stdio stream, or of its member, holds the
	// box.
	if m, ok := v.Init.(*ast.MemberExpression); ok && v.TypeAnnot == nil && o.r.stdio != nil && (o.r.stdio(m) || o.r.stdio(m.Object)) {
		return TypeAny, true, true
	}
	// An awaited array literal is the array (its `never` leaf codegen's
	// number).
	if aw, isAw := v.Init.(*ast.AwaitExpression); isAw && v.TypeAnnot == nil {
		if _, isAL := aw.Argument.(*ast.ArrayLiteral); isAL {
			if at, ok := o.ExprType(aw); ok {
				return at, true, true
			}
		}
	}
	t := o.c.VarType(v)
	_, initRef := v.Init.(*ast.Identifier)
	if !o.c.Unanswered(t) && t.Flags&checker.Object != 0 && (t.Kind == checker.Function && len(t.Overloads) > 0 || initRef && len(t.Calls) > 0) && v.TypeAnnot == nil && v.Init != nil {
		// An overloaded function's value, or one with properties (`export
		// default strict`, `const f = g`): what its initializer is held as,
		// the implementation.
		if it, ok := o.ExprType(v.Init); ok {
			return it, true, true
		}
		if id, ok := v.Init.(*ast.Identifier); ok && o.b != nil {
			if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) > 0 {
				for _, d := range sym.Declarations {
					if fd, isFD := d.Node.(*ast.FunctionDeclaration); isFD {
						if it := o.c.ImplementationSignature(fd); it != nil {
							if rt, rok := o.r.of(it); rok {
								return rt, true, true
							}
						}
					}
				}
			}
		}
	}
	if o.c.Unanswered(t) && v.TypeAnnot != nil && o.synthesizedDecl(v) {
		// A declaration codegen synthesizes (an enum's or a namespace's
		// object, an enum's computed member) holds its annotation.
		if at, ok := o.AnnotationType(v.TypeAnnot); ok {
			return at, true, true
		}
	}
	if o.c.Unanswered(t) {
		// A binding no function reads stays a local whatever its type.
		if !o.readInFunctions(v.Name) {
			return Type{}, false, true
		}
		return Type{}, false, false
	}
	// A `var` declared again is the first declaration's global; the later
	// one is a store to it.
	if o.b != nil {
		if sym, found := o.b.DeclSymbol(v); found && laterVarDecl(sym, v) {
			return Type{}, false, true
		} else if sym != nil && o.jsWidenedVar(sym) {
			return TypeAny, true, true
		}
	}
	if o.jsBoxedInit(v) {
		return ArrayOf(TypeAny), true, true
	}
	// A generator's instance (`const g = gen()`): codegen's layout.
	if call, isCall := v.Init.(*ast.CallExpression); isCall && v.TypeAnnot == nil && o.r.codegenCall != nil {
		if ct, cok := o.r.codegenCall(call); cok && ct.IsGenerator() {
			return ct, true, true
		}
	}
	if _, named := v.Init.(*ast.Identifier); isGeneratorFunc(t) && !named {
		return TypeAny, true, true // a generator method read off its object
	}
	if v.TypeAnnot != nil && t.Flags&checker.Object != 0 && t.Kind == checker.Tuple && len(t.Elems) == 0 {
		return TupleType(nil), true, true // an annotation's `[]`, an empty struct
	}
	if v.TypeAnnot != nil && t.Flags&checker.StringLiteral != 0 && t.Flags&checker.Union == 0 {
		// An annotation keeps its string literal (`Named<User>` naming
		// `"named"`), as annotationType resolves it.
		return Type{IR: irPtr, Kind: kindStrLiteral, LitValue: t.Value}, true, true
	}
	r := o.r
	// A `new Map<K, [A, B]>()`'s type arguments are annotations too.
	r.tuples = v.TypeAnnot != nil || explicitTupleTypeArgs(v.Init) || annotatedFuncResult(v.Init)
	r.unwritten = v.TypeAnnot == nil
	ty, ok := r.of(t)
	if ok && ty.IsFunc() && o.r.js != nil && o.r.js() && v.Init != nil {
		ty = o.jsDefaultedParams(v.Init, ty)
	}
	if lit, isLit := v.Init.(*ast.ObjectLiteral); ok && isLit && v.TypeAnnot == nil {
		// Each field is stored as the literal builds it (an asserted array
		// of tuples in that shape, literalStorage).
		ty = o.literalGenFields(lit, o.literalStorage(lit, ty))
	}
	if !r.tuples && v.Init != nil && hasTuple(t) {
		// An unannotated binding of a tuple (`const t = f()`, `let w = u`)
		// holds what its initializer is held as: a tuple an annotation
		// declared, else the array codegen built.
		ty, ok = o.ExprType(v.Init)
	}
	if _, isNew := v.Init.(*ast.NewExpression); !ok && isNew && v.TypeAnnot == nil {
		// A generic class's instantiation: the class codegen built for it.
		ty, ok = o.ExprType(v.Init)
	}
	if ok && o.b != nil {
		if sym, found := o.b.DeclSymbol(v); found {
			ty = o.undefinedSlot(v, sym, ty)
		}
	}
	return ty, ok, true
}

// tupleExprType answers an expression whose type involves a tuple where
// codegen holds one: a call of a function whose return annotation declares
// it, and an array literal of tuples held alike (`[u, t]`).
func (o frontOracle) tupleExprType(e ast.Expression) (Type, bool) {
	nt := o.c.TypeOf(e)
	if o.c.Unanswered(nt) || !hasTuple(nt) {
		return Type{}, false
	}
	switch x := e.(type) {
	case *ast.CallExpression:
		id, ok := x.Callee.(*ast.Identifier)
		if !ok {
			return Type{}, false
		}
		// Through a binding an annotation types as a function returning a
		// tuple: that tuple.
		if ft, fok := o.ExprType(id); fok && ft.IsFunc() && ft.FuncRetType != nil && ft.FuncRetType.IsTuple() {
			return *ft.FuncRetType, true
		}
		sym, _ := o.b.Resolve(id)
		if sym == nil || len(sym.Declarations) != 1 {
			return Type{}, false
		}
		if fd, ok := sym.Declarations[0].Node.(*ast.FunctionDeclaration); ok && fd.ReturnType != nil && !fd.IsAsync && !fd.IsGenerator {
			r := o.r
			r.tuples = true
			return r.of(nt)
		}
	case *ast.IndexExpression:
		// A read of a held tuple's position is that field; of an array of
		// tuples, its element (possibly absent).
		if x.Optional {
			return Type{}, false
		}
		ot, ok := o.ExprType(x.Object)
		if !ok {
			return Type{}, false
		}
		if ot.IsTuple() {
			if n, isNum := x.Index.(*ast.NumberLiteral); isNum {
				if k, err := strconv.Atoi(n.Value); err == nil {
					if f := ot.UserFields(); k >= 0 && k < len(f) {
						return f[k].Ty, true
					}
				}
			}
			return Type{}, false
		}
		if ot.IsArray() && ot.ElemType != nil && ot.ElemType.IsTuple() {
			return indexReadType(*ot.ElemType), true
		}
		// A record's value an annotation declares to hold tuples is held
		// as declared; a missing key reads undefined (recordRead).
		if vt, isDict := dictValType(ot); isDict && reprHasTuple(vt) {
			return undefinedableElem(vt), true
		}
	case *ast.ArrayLiteral:
		if len(x.Elements) == 0 {
			return Type{}, false
		}
		var elem Type
		for i, el := range x.Elements {
			if _, spread := el.(*ast.SpreadElement); spread {
				return Type{}, false
			}
			et, ok := o.ExprType(el)
			if !ok || !et.IsTuple() || i > 0 && reprKey(et) != reprKey(elem) {
				return Type{}, false
			}
			elem = et
		}
		return ArrayOf(elem), true
	}
	return Type{}, false
}

// reprHasTuple reports a tuple in a representation or its array element.
func reprHasTuple(t Type) bool {
	return t.IsTuple() || t.IsArray() && t.ElemType != nil && reprHasTuple(*t.ElemType)
}

// selfContaining reports a function type that contains itself (`const f =
// () => f`, tsc's `() => ...`): no closure signature spells it, so its
// values are held boxed and called dynamically.
// selfReturningDecl reports the type of a function declaration whose every
// return returns its own name (returnsOwnName).
func selfReturningDecl(t *checker.Type) bool {
	decl := func(x *checker.Type) bool {
		if x == nil || x.Symbol == nil || len(x.Symbol.Declarations) != 1 {
			return false
		}
		fd, ok := x.Symbol.Declarations[0].Node.(*ast.FunctionDeclaration)
		return ok && fd.ReturnType == nil && returnsOwnName(fd)
	}
	// A read of it is typed as a signature returning the declaration's own
	// type: the same function.
	if r := t.Ret(); r != nil && r != t && t.Symbol == nil && r.Flags&checker.Object != 0 && r.Kind == checker.Function && decl(r) {
		return true
	}
	return decl(t)
}

func selfContaining(t *checker.Type) bool {
	seen := map[*checker.Type]bool{}
	var walk func(x *checker.Type) bool
	walk = func(x *checker.Type) bool {
		if x == nil || x.Flags&checker.Object == 0 || x.Kind != checker.Function {
			return false
		}
		if x == t && len(seen) > 0 {
			return true
		}
		if seen[x] {
			return false
		}
		seen[x] = true
		if walk(x.Ret()) {
			return true
		}
		for _, p := range x.Params {
			if walk(p) {
				return true
			}
		}
		return false
	}
	return walk(t)
}

// hasTuple reports a tuple type in t, its array element or union members.
func hasTuple(t *checker.Type) bool { return hasTupleIn(t, map[*checker.Type]bool{}) }

// hasTupleIn is hasTuple, seen the types being searched around t: a type
// that contains itself (`const f = () => f`) holds no tuple there.
func hasTupleIn(t *checker.Type, seen map[*checker.Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	switch {
	case t == nil:
		return false
	case t.Flags&checker.Union != 0:
		for _, m := range t.Types {
			if hasTupleIn(m, seen) {
				return true
			}
		}
	case t.Flags&checker.Object != 0 && t.Kind == checker.Tuple:
		return true
	case t.Flags&checker.Object != 0 && t.Kind == checker.Array:
		return hasTupleIn(t.Elem, seen)
	case t.Flags&checker.Object != 0 && (t.Kind == checker.Instance || t.Kind == checker.Interface) && len(t.TypeArgs) > 0:
		// A collection's type arguments (`Map<number, [string, string]>`).
		for _, a := range t.TypeArgs {
			if hasTupleIn(a, seen) {
				return true
			}
		}
	case t.Flags&checker.Object != 0 && (t.StringIndex != nil || t.NumberIndex != nil) && len(t.Props) == 0:
		// A record whose values an annotation declares as tuples.
		return hasTupleIn(t.StringIndex, seen) || hasTupleIn(t.NumberIndex, seen)
	case t.Flags&checker.Object != 0 && t.Kind == checker.Function && len(t.Overloads) == 0:
		// A function type an annotation spells returns or takes its tuples
		// as declared.
		if hasTupleIn(t.Ret(), seen) {
			return true
		}
		for _, p := range t.Params {
			if hasTupleIn(p, seen) {
				return true
			}
		}
	}
	return false
}

// ReturnType answers funcBodyReturnType: hasValue is false for a
// body that returns no value (void).
func (o frontOracle) ReturnType(body *ast.BlockStatement) (Type, bool, bool) {
	// An object literal method's `return this`: the literal's synthetic class
	// where it has an accessor (ensureObjLitClass), in either lane.
	fe, isFE := o.c.ParentOf(body).(*ast.FunctionExpression)
	thisOnly := isFE && returnsOnlyThis(fe)
	if thisOnly {
		if lit, inLit := o.c.ParentOf(fe).(*ast.ObjectLiteral); inLit && hasAccessor(lit) && o.r.objLit != nil {
			if ct, cok := o.r.objLit(lit); cok {
				return ct, true, true
			}
		}
	}
	t := o.c.BodyReturnType(body)
	if o.c.Unanswered(t) {
		// Any other literal's: a box (exprTypeQuery).
		if thisOnly {
			if _, inLit := o.c.ParentOf(fe).(*ast.ObjectLiteral); inLit {
				return TypeAny, true, true
			}
		}
		return o.returnsAnswered(body)
	}
	if t.Flags&checker.Void != 0 {
		// A void value returned through a box (`return o.f()` on a boxed
		// o) is that box.
		if rt, _, rok := o.returnsAnswered(body); rok && isUnconstrainedDynamic(rt) {
			return rt, true, true
		}
		return Type{}, false, true
	}
	ty, ok := o.r.of(t)
	if !ok {
		// A type reprOf cannot name: what its returns read as; in
		// JavaScript a union of kinds it cannot name is a box.
		rt, hv, rok := o.returnsAnswered(body)
		if !rok && t.Flags&checker.Union != 0 && o.r.js != nil && o.r.js() {
			return TypeAny, true, true
		}
		return rt, hv, rok
	}
	if ty.IsFunc() {
		// A returned function is what its returns read as when that is a
		// function or a box (funcBodyReturnType keeps the old
		// inference: the closure carries its parameters' defaults, which
		// the type has not).
		if rt, hv, rok := o.returnsAnswered(body); rok && (rt.IsFunc() || rt.IsDynamic()) {
			return rt, hv, true
		}
		ty.FuncCountsArgs = false // a returned method value: see exprType
	}
	if ok && !ty.IsDynamic() {
		// What its returns read as: an evolving field read is a box, a
		// skippable `var` (hoistSkippable) may be undefined.
		// A return of a value codegen holds as a box (a js parameter with
		// a default) is that box.
		if rt, _, rok := o.returnsAnswered(body); rok && (rt.IsDynamic() && (ty.IsNull() || ty.IsUndefined() || isUnconstrainedDynamic(rt)) || rt.Nullable() && !ty.Nullable() && rt.IR == ty.IR) {
			return rt, true, true
		}
		// A returned accessor literal is its synthetic class's instance, and
		// a returned literal whose methods return one has them so.
		if rt, _, rok := o.returnsAnswered(body); rok && (rt.IsClass() && !ty.IsClass() || len(rt.Fields) > 0 && len(rt.Fields) == len(ty.Fields) && reprKey(rt) != reprKey(ty)) {
			// The checker's absence stays: a body that may fall off its end
			// (`{ n } | undefined`) returns undefined there.
			if ty.Nullable() && !rt.Nullable() {
				rt.setNullable(true)
				rt.setUndefined(ty.IsUndefined())
			}
			return rt, true, true
		}
	}
	return ty, ok, ok
}

// Reference answers emitIdent: a name the builtin declarations declare
// (`Infinity`, `console`) is a global, as codegen counts it.
func (o frontOracle) Reference(id *ast.Identifier) (bool, bool) {
	sym, ok := o.b.Resolve(id)
	if sym != nil && sym.Implicit {
		// A function's own `arguments`: codegen materializes it per function
		// as its lowering needs, so neither answer is the binding's.
		return false, false
	}
	if sym != nil && o.b.Globals != nil {
		for s := sym.Scope; s != nil; s = s.Parent {
			if s == o.b.Module {
				break
			}
			if s == o.b.Globals {
				return false, ok
			}
		}
	}
	return sym != nil, ok
}

// reprOf is the representation codegen gives a checker type — the first
// cut of TDD-00230 P3.3's closed set: the primitives, the native widths,
// arrays of them and the builtins a `@lowerType` declaration lays out. A type
// outside it has no answer yet.
func reprOf(t *checker.Type) (Type, bool) { return reprCtx{}.of(t) }

// ambientClassSymbol reports a symbol a `declare class` declares.
func ambientClassSymbol(s *binder.Symbol) bool {
	if s == nil {
		return false
	}
	for _, d := range s.Declarations {
		if cd, ok := d.Node.(*ast.ClassDeclaration); ok && cd.Ambient {
			return true
		}
	}
	return false
}

// reprCtx is what a representation depends on beyond the type: the classes
// codegen registered, and the lane (the js lane holds an object literal's
// type as a dynamic object). The zero value answers neither.
type reprCtx struct {
	// param marks a signature's own parameter: a `T | PromiseLike<T>`
	// there is T, codegen's resolvers adopting a thenable at run time.
	param bool
	// fnBusy are the function and dictionary types being represented: one
	// that contains itself (`const f = () => f`, tsc's `() => ...`) holds
	// the inner occurrence as a box.
	fnBusy map[*checker.Type]bool
	// erasedTP are the type parameters of the generic signatures being
	// represented: codegen compiles a value of such a signature with them
	// erased to any.
	erasedTP map[*checker.Type]bool
	// unwritten is set for a type no annotation wrote (an unannotated
	// binding's): a union of several array types is the box codegen's
	// literal inference builds, where an annotation of one is rejected.
	unwritten bool
	// erasedGeneric reports a nested generic function compiled with its
	// type parameters erased to any (lowerNestedGenericFuncs).
	erasedGeneric func(ast.Node) bool
	// typeParam is the concrete type of the instantiation being emitted
	// for a type parameter's name (typeParamScope), when one is.
	typeParam func(name string) (Type, bool)
	// typeParamNames are the type parameters bound where emission is (a
	// generic body's instantiation), sorted.
	typeParamNames func() []string
	// promiseValue is the value type a `new Promise`'s executor was
	// emitted with (emitNewPromise).
	promiseValue func(*ast.NewExpression) (Type, bool)
	class        func(*binder.Symbol) (Type, bool)
	// genericClass is a generic class's instantiation at args, the class
	// codegen monomorphizes it to (genericClassInstanceType).
	genericClass func(*binder.Symbol, []Type) (Type, bool)
	// iface answers a program's own interface by the layout codegen
	// registered for it.
	iface func(*binder.Symbol) (Type, bool)
	// narrowClass answers a class an `any` narrowed to reads as.
	narrowClass func(*binder.Symbol) (Type, bool)
	// narrowedUnion is the member of a boxed union a reference's checker
	// type narrows it to (checkerNarrowedUnion).
	narrowedUnion func(ast.Expression, Type) (Type, bool)
	// genDesugared reports a generator whose destructured parameters
	// codegen rewrote (desugarGeneratorPatternParams).
	genDesugared func(*ast.BlockStatement) bool
	// genPatternInits is a generator's destructured parameters, moved off
	// its signature (desugarGeneratorPatternParams): their defaults read
	// names as the function does.
	genPatternInits func() map[*ast.FunctionDeclaration][]ast.Statement
	// liftedGen is the generator holding a lifted static generator
	// method's body (liftStaticGenerators).
	liftedGen func(*ast.FunctionDeclaration) *ast.FunctionDeclaration
	// stdio reports `process.stdin`/`stdout`/`stderr`, read through the
	// stdio module's getter as a box (emitMemberRaw), or a binding of one
	// (stdioStream).
	stdio func(ast.Expression) bool
	// objects answers an object type (the zero value leaves it to the
	// old inference, as the codegen deciders still need).
	objects bool
	// mapping holds the anonymous object types being mapped, to cut a
	// self-reference.
	mapping map[*checker.Type]bool
	js      func() bool
	// widened reports an untyped binding of the scope being emitted that
	// codegen backs with a box (crossTypeWidenedBindings).
	widened func(string) bool
	// filledBoxed: a js-lane array literal codegen holds as boxes.
	filledBoxed func(*ast.VarDeclaration) bool
	// literals keeps a union's string-literal members as literals, as an
	// annotation's union does (`'get' | 'set' | boolean`).
	literals bool
	// tuples answers a tuple type: one an annotation declares, which codegen
	// stores as a tuple (an inferred tuple, `Promise.all([…])`'s, is an
	// array to it).
	tuples bool
	// objLit answers an accessor-bearing object literal by the synthetic
	// class codegen lowers it to (ensureObjLitClass).
	objLit func(*ast.ObjectLiteral) (Type, bool)
	// bareCollection is the storage codegen gives an unannotated binding of
	// a bare `new Map()`/`new Set()`: its key and value types from the
	// binding's later calls (inferEmptyMapKVTypes).
	bareCollection func(*ast.VarDeclaration) (Type, bool)
	// mapEntries is codegen's key and value types of a `new Map(entries)`
	// (mapKVTypes).
	mapEntries func(*ast.NewMapExpression) Type
	// methodRecord reports a builtin method read as a value that codegen
	// holds as its prototype's record (typeOfExprFull).
	methodRecord func(*ast.MemberExpression) bool
	// funcProp is a function value's property codegen reads off the boxed
	// function (`g.toString`): ok is false for one it reads as declared.
	funcProp func(*ast.MemberExpression) (Type, bool)
	// nodeJS reports a symbol the builtin `NodeJS` namespace declares.
	nodeJS func(*binder.Symbol) bool
	// extends reports whether class name derives from base (classExtends).
	extends func(name, base string) bool
	// libClass is the implementation class codegen gives a lib-declared
	// interface (`Event`, `NodeJS.Timeout`), as annotationType does.
	libClass func(sym *binder.Symbol, nodeJS bool, args []Type) (Type, bool)
	// protoCtor reports a js-lane prototype constructor function: `new` of
	// one is a dynamic object.
	protoCtor func(string) bool
	// classMethod is the method declaration a registered class runs for
	// name, or nil.
	classMethod func(className, name string) *ast.FunctionDeclaration
	// species is the class type a registered class's [Symbol.species]
	// names (plain is true for Promise itself).
	species func(className string) (ty Type, plain, ok bool)
	// bufferClass reports a class extending ArrayBuffer (SharedArrayBuffer).
	bufferClass func(className string) bool
	// promiseErased reports a class extending Promise whose extends clause
	// erases its value type, so its instances hold every value boxed.
	promiseErased func(className string) bool
	// promiseClass reports a class extending Promise.
	promiseClass func(className string) bool
	// inferred is codegen's own type for an expression the checker cannot
	// answer.
	inferred func(ast.Expression) (Type, bool)
	// byValRet reports a function -optimize-memory returns its tuple from
	// by value (planTupleByValReturns).
	byValRet func(name string) bool
	// widened reports a parameter a dynamic entry binds as `any`
	// (emit_dynentry.go).
	widenedParam func(*ast.Identifier) bool
	checkerPrim  func(ast.Expression) (Type, bool)
	// anyHeld answers an annotated binding initialized from an `any`,
	// which holds the box (emit_anyarg.go).
	anyHeld func(*ast.VarDeclaration) bool
	// runtimeClone reports a structuredClone the run-time clone performs
	// (its result a box, as the intrinsic types it).
	runtimeClone func(*ast.CallExpression) bool
	// localDecl answers a function's own unannotated local as its
	// declaration stores it (localDeclType).
	localDecl func(*ast.VarDeclaration) (Type, bool)
	// anyArgBox answers a call whose any-argument check boxes its result
	// (anyArgReturnsBox).
	boxOverride func(ast.Expression) (Type, bool)
	// erasedParam is erasedCallbackParam.
	erasedParam func(*ast.ArrowFunction, int) (Type, bool)
	// narrowed is checkerNarrowed: a boxed local's read the checker narrows.
	narrowed func(*ast.Identifier, Type) (Type, bool)
	// chainCopy is the program link a copy emission builds of an optional
	// chain's link stands for (noteChainCopy).
	chainCopy func(ast.Expression) (ast.Expression, bool)
	// origin is the expression of the code as written that a node a
	// rewrite after binding built stands for (originate).
	origin func(ast.Expression) (ast.Expression, bool)
	// builtinRef is the closure codegen builds for a builtin function read
	// as a value (`Object.defineProperty` held in a variable).
	builtinRef func(ast.Expression) (Type, bool)
	// warm is the checker's answer warmFront kept for an expression.
	warm func(ast.Expression) (*checker.Type, bool)
	// staticMethod is a class's static member, as codegen registered it:
	// a field's type, a method read as a value (a lifted static
	// generator's forwarding method).
	staticMethod func(class, method string) (Type, bool)
	// unassigned reports a read of a class field that may still be
	// unassigned (unassignedField).
	unassigned func(*ast.MemberExpression) bool
	// methodValue reports a class instance's method read as a value, which
	// codegen holds as a boxed function record (classMethodValue).
	methodValue func(Type, string) bool
	// newTargetHeld reports a function's new.target that emission holds as
	// the function itself (newTargetType), else undefined.
	newTargetHeld func(*ast.NewTargetExpression) bool
	// keepsBox reports an assertion that keeps its operand's box (an
	// array asserted from any, a typed array asserted to another kind).
	keepsBox     func(*ast.AsExpression) bool
	funcThis     func(*ast.FunctionExpression) (Type, bool)
	literalParam func(ast.Expression, int) (Type, bool)
	annotation   func(ast.TypeNode) Type
	unresolved   func(*ast.Identifier) bool
	// heldIdent is the type the binding a name reads is held as in the scope
	// being emitted (a callback parameter codegen hinted, a replace
	// callback's capture group).
	heldIdent func(*ast.Identifier) (Type, bool)
	// getterDecl is the getter a class instance's property read runs
	// (classGetterDecl), or nil.
	getterDecl func(Type, string) *ast.FunctionDeclaration
	// genericNew is the instance a program generic class's `new` builds
	// (newTypeArgs): the monomorph emission instantiates.
	genericNew func(*ast.NewExpression) (Type, bool)
	// codegenCall is a call's representation that only codegen can name
	// (codegenCallRepr): a generator's instance, its results, a collection
	// iterator's own methods.
	codegenCall func(*ast.CallExpression) (Type, bool)
	// scopeKey identifies codegen's scope stack (inferScopeIdentity).
	scopeKey func() (int, uintptr)
	// erasedCall is codegen's result for a call of an erased generic
	// method (`run<R>(…): R`, genericMethodResult).
	erasedCall func(*ast.CallExpression, Type) (Type, bool)
	// boundFunc is the value `f.bind(…)` with n arguments makes of a
	// function value of the given type (emitFunctionBind).
	boundFunc func(Type, int) (Type, bool)
	// builtinValue is a builtin global read as a value: the function,
	// namespace object or boxed constructor codegen makes of it.
	builtinValue func(*ast.Identifier) (Type, bool)
	// loweredValue is a builtin function lowered to a library function,
	// read as a value (`const st = setTimeout`): that function's value.
	loweredValue func(*ast.Identifier) (Type, bool)
	// funcRef is the value of the function codegen declared under a name
	// (resolveFuncRef): a generic one's erased signature.
	funcRef func(string) (Type, bool)
	// generatorValue is a generator function read as a value
	// (generatorValueType); its instance layout is codegen's.
	generatorValue func(*ast.Identifier) (Type, bool)
	// importResult is a dynamic import's module object: the layout of the
	// exports codegen builds (importCallResultObjectType).
	importResult func(*ast.ImportCallExpression) Type
	// erasedTPNames are the type-parameter names a body compiled once
	// erases (erasedTypeParamNames).
	erasedTPNames func() []string
	// warmAnn is an annotation's type as written (frontAnnWarm).
	warmAnn func(ast.TypeNode) (*checker.Type, bool)
	// inLib reports emission inside a builtin library module.
	inLib func() bool
	// globalClass is the class of the builtin module a global interface's
	// name links to (TDD-00232).
	globalClass func(string) (Type, bool)
	// userType reports a generic or plain interface the program declares
	// under name.
	userType func(string) bool
	// mappedFrom is the instance a member-preserving mapped type maps
	// (checker.MappedFrom): its values are that instance's.
	mappedFrom func(*checker.Type) *checker.Type
}

// repr is the representation context of the program being emitted.
func (e *Emitter) repr() reprCtx {
	return reprCtx{mappedFrom: func(t *checker.Type) *checker.Type {
		if e.frontRepr != nil {
			return e.frontRepr.c.MappedFrom(t)
		}
		return nil
	}, erasedGeneric: func(n ast.Node) bool { fd, ok := n.(*ast.FunctionDeclaration); return ok && e.erasedGenerics[fd] }, typeParam: func(n string) (Type, bool) { t, ok := e.typeParamScope[n]; return t, ok }, typeParamNames: func() []string {
		names := make([]string, 0, len(e.typeParamScope))
		for n := range e.typeParamScope {
			names = append(names, n)
		}
		sort.Strings(names)
		return names
	}, promiseValue: func(ne *ast.NewExpression) (Type, bool) { t, ok := e.promiseValue[ne]; return t, ok }, class: e.classRepr, genericClass: e.genericClassRepr, iface: e.interfaceRepr, narrowClass: e.narrowableClass, narrowedUnion: e.checkerNarrowedUnion, genDesugared: e.genDesugaredBody, genPatternInits: func() map[*ast.FunctionDeclaration][]ast.Statement { return e.genPatternInits }, liftedGen: func(m *ast.FunctionDeclaration) *ast.FunctionDeclaration { return e.liftedStaticGens[m] }, stdio: e.stdioStream, objects: true, js: e.compatJS, widened: func(n string) bool { return e.widenedBindings[n] }, filledBoxed: func(v *ast.VarDeclaration) bool { return e.filledBoxed[v] }, objLit: e.objLitClassRepr, protoCtor: func(n string) bool { return e.jsProtoCtor[n] }, funcProp: e.funcValueProp, libClass: e.libInterfaceClass, bareCollection: e.bareContainerType, methodRecord: e.methodRecordRead, mapEntries: func(x *ast.NewMapExpression) Type { return MapType(e.mapKVTypes(x.KeyType, x.ValType, x.Init)) }, classMethod: e.classMethodDecl, species: e.speciesClass, bufferClass: func(n string) bool {
		info, ok := e.classes[n]
		return ok && info.HostBase != nil && info.HostBase.IsArrayBuffer()
	}, promiseErased: e.promiseHostErased, promiseClass: e.isPromiseClass, boundFunc: e.boundFuncValueType, erasedCall: e.erasedMethodCall, widenedParam: e.dynEntryWidenedIdent, checkerPrim: e.checkerPrimitive, anyHeld: e.anyHeldDecl, localDecl: e.localDeclType, runtimeClone: e.runtimeCloneCall, extends: e.classExtends, boxOverride: e.boxOverride, narrowed: e.checkerNarrowed, erasedParam: e.erasedCallbackParam, chainCopy: e.chainCopyOf, origin: func(x ast.Expression) (ast.Expression, bool) { o, ok := e.synthOrigin[x]; return o, ok }, warm: func(x ast.Expression) (*checker.Type, bool) { t, ok := e.frontWarm[x]; return t, ok }, builtinRef: e.builtinRefValueType, codegenCall: e.codegenCallRepr, genericNew: e.genericNewRepr, methodValue: e.classMethodValue, newTargetHeld: e.newTargetHeld, keepsBox: e.keepsBox, funcThis: e.funcExprThisType, literalParam: e.literalParamType, annotation: e.annotationType, unresolved: e.unresolvedReference, heldIdent: e.heldIdent, getterDecl: e.classGetterDecl, staticMethod: e.staticMethodValue, scopeKey: e.inferScopeIdentity, builtinValue: e.unboundBuiltinValueType, loweredValue: e.loweredGlobalValue, funcRef: e.funcRefValue, generatorValue: e.generatorValueRepr, importResult: e.importCallResultObjectType, erasedTPNames: e.erasedTypeParamNames, warmAnn: func(n ast.TypeNode) (*checker.Type, bool) { t, ok := e.frontAnnWarm[n]; return t, ok }, inLib: func() bool { return e.inLib != "" }, globalClass: e.globalLinkedClass, userType: func(n string) bool { _, g := e.genericInterfaces[n]; _, i := e.interfaces[n]; return g || i }, unassigned: e.unassignedField, inferred: func(x ast.Expression) (Type, bool) { return e.typeOfExpr(x), true }, byValRet: func(n string) bool { sig, ok := e.funcs[n]; return ok && sig.RetType.TupleByVal }}
}

// narrowableClass is the instance type of the class sym declares when an
// `any` narrowed to it reads unboxed (checkerNarrowed): a plain class, not
// one over a host body or an Error subclass.
func (e *Emitter) narrowableClass(sym *binder.Symbol) (Type, bool) {
	ct, ok := e.classRepr(sym)
	if !ok || !ct.IsClass() {
		return Type{}, false
	}
	if info, ok := e.classes[ct.ClassName]; ok && info.HostBase == nil && !info.IsErrorSubclass {
		return ct, true
	}
	return Type{}, false
}

// classRepr is the instance type of the class sym declares, once codegen
// registered it.
func (e *Emitter) genericClassRepr(sym *binder.Symbol, args []Type) (Type, bool) {
	for _, d := range sym.Declarations {
		cd, ok := d.Node.(*ast.ClassDeclaration)
		if !ok || len(cd.TypeParams) != len(args) || len(args) == 0 {
			continue
		}
		subs := make(map[string]Type, len(args))
		for i, n := range cd.TypeParams {
			subs[n] = args[i]
		}
		if t, err := e.genericClassInstanceType(cd, subs); err == nil {
			return t, true
		}
	}
	return Type{}, false
}

func (e *Emitter) classRepr(sym *binder.Symbol) (Type, bool) {
	for _, d := range sym.Declarations {
		cd, ok := d.Node.(*ast.ClassDeclaration)
		if ce, isExpr := d.Node.(*ast.ClassExpression); isExpr {
			cd, ok = ce.Decl, true // a class expression: its hoisted declaration
		}
		if v, isVar := d.Node.(*ast.VarDeclaration); isVar {
			if ce, isExpr := v.Init.(*ast.ClassExpression); isExpr && ce.Decl != nil {
				cd, ok = ce.Decl, true // `const C = class {}`: the same
			}
		}
		if ok && e.foreignCtorClass(cd) {
			// Its constructor may return an object that is not its instance:
			// what `new` yields is held as any, the object it is.
			return TypeAny, true
		}
		if ok {
			if name, ok := e.classDeclName[cd]; ok {
				// Published before its layout is built (ADR-00906): no
				// answer until it is.
				if info, ok := e.classes[name]; ok && info.Ty.IR != "" {
					return info.Ty, true
				}
				// While it registers, the instance is the type `this` is
				// bound to there: the provisional layout, or the
				// name-placeholder a field initializer reads.
				if this, ok := e.lookup("this"); ok && this.Ty.IsClass() && this.Ty.ClassName == name {
					return this.Ty, true
				}
				if pt, ok := e.interfaces[name]; ok && pt.IsClass() && pt.ClassName == name && pt.IR != "" {
					return pt, true
				}
			} else if pt, ok := e.interfaces[cd.Name]; ok && pt.IsClass() && pt.ClassName == cd.Name && pt.IR != "" {
				// Not registered yet: the name placeholder an alias or
				// interface naming it resolves to
				// (registerClassNamePlaceholders).
				return pt, true
			}
		}
	}
	return Type{}, false
}

// interfaceRepr is the layout codegen registered for the interface sym
// declares (a structural object type named by its RefName).
func (e *Emitter) interfaceRepr(sym *binder.Symbol) (Type, bool) {
	if ty, ok := e.interfaceClassBase(sym); ok {
		return ty, true
	}
	for _, d := range sym.Declarations {
		// A non-generic object alias is registered as an interface is.
		if ad, ok := d.Node.(*ast.TypeAliasDeclaration); ok && len(ad.TypeParams) == 0 {
			if ty, ok := e.interfaces[ad.Name]; ok && ty.RefName == ad.Name && ty.IsObject() && len(ty.Fields) > 0 {
				return ty, true
			}
		}
		if id, ok := d.Node.(*ast.InterfaceDeclaration); ok && id.LowerType == "" && len(id.TypeParams) == 0 {
			if ty, ok := e.interfaces[id.Name]; ok && ty.RefName == id.Name && (ty.IsObject() && len(ty.Fields) > 0 || e.nativeIfaceMembers[id.Name] != nil) {
				// An object type's members, or a native layout (`extends
				// Date`) whose own members the interface adds.
				return ty, true
			}
			if ty, ok := e.interfaces[id.Name]; ok && reprKey(ty) == reprKey(errorObjType) {
				// An Error kin adding only fields every Error has
				// (`extends NodeJS.ErrnoException { port?: number }`): the
				// Error representation.
				return ty, true
			}
			if ty, ok := e.interfaces[id.Name]; ok && ty.RefName == id.Name && ty.IsObject() && ty.Fields == nil && e.ambientBuilding(id.Name) {
				// A self-reference while the interface's own members
				// resolve (a listener's `this: AbortSignal`): the
				// placeholder ambientInterfaceType breaks the cycle with.
				return ty, true
			}
			// A builtin declaration's interface not yet read: registered
			// as annotationType registers it on first use.
			if _, seen := e.interfaces[id.Name]; !seen && libDeclared(sym) {
				if ty, ok := e.ambientInterfaceType(id.Name); ok && ty.IsObject() && len(ty.Fields) > 0 {
					return ty, true
				}
			}
		}
	}
	return Type{}, false
}

// interfaceClassBase is the class an interface extends, as codegen holds
// it: an interface extending one class (`interface
// ChildProcessWithoutNullStreams extends ChildProcess`) only narrows its
// members' types, and the class's instance is what it holds.
func (e *Emitter) interfaceClassBase(sym *binder.Symbol) (Type, bool) {
	var base *binder.Symbol
	for _, d := range sym.Declarations {
		id, ok := d.Node.(*ast.InterfaceDeclaration)
		if !ok || len(id.TypeParams) > 0 {
			return Type{}, false
		}
		// Names in a module carry its suffix, as the interface's own does.
		suffix := strings.TrimPrefix(sym.Name, ast.Unmangle(sym.Name))
		for _, name := range id.Extends {
			for sc := sym.Scope; sc != nil; sc = sc.Parent {
				b := sc.Symbols.Get(name + suffix)
				if b == nil {
					b = sc.Symbols.Get(name)
				}
				if b != nil {
					if _, isClass := e.classRepr(b); isClass {
						if base != nil && base != b {
							return Type{}, false
						}
						base = b
					}
					break
				}
			}
		}
	}
	if base != nil {
		return e.classRepr(base)
	}
	return Type{}, false
}

func (r reprCtx) of(t *checker.Type) (Type, bool) {
	if t.SelfReferential() {
		// A union holding itself (`T | Box<R<T>>`) has no finite layout:
		// a box, read by its run-time kind.
		return TypeAny, true
	}
	if t.Flags&checker.Object != 0 && t.Flags&checker.Union == 0 && len(t.Calls)+len(t.Constructs) > 0 && len(t.Props) > 0 && (t.Symbol == nil || !libDeclared(t.Symbol)) && t.Kind != checker.Instance {
		// A program's callable with properties (`F & { foo: string }`): a
		// function object, held boxed as one carrying expandos is. A
		// library one (`process.memoryUsage`) keeps its host function.
		return TypeAny, true
	}
	if t.Flags&checker.Union != 0 {
		return r.union(t)
	}
	// `Readonly<Map<K, V>>` with no representation of its own (an
	// overloaded member): the Map it maps (readonly is the checker's alone).
	if r.mappedFrom != nil {
		if src := r.mappedFrom(t); src != nil {
			own := r
			own.mappedFrom = nil
			if rt, ok := own.of(t); ok {
				return rt, true
			}
			return r.of(src)
		}
	}
	// An ambient class's instance or constructor (`declare class C`): an
	// object only the environment provides, held boxed.
	if t.Flags&checker.Object != 0 && ambientClassSymbol(t.Symbol) {
		return TypeAny, true
	}
	// A global interface a builtin module implements (`Event`,
	// `AbortSignal`, `URL`) is that module's class.
	if t.Flags&checker.Object != 0 && t.Kind == checker.Interface && t.Symbol != nil && len(t.TypeArgs) == 0 && r.globalClass != nil && r.libGlobal(t.Symbol) {
		if ct, ok := r.globalClass(t.Symbol.Name); ok {
			return ct, true
		}
	}
	// An object type that is one call signature and nothing else (`{ (n:
	// number): string }`, `interface Renderer { (n: number): string }`) is
	// that function; an instance of a generic one (`Make<T>`), of its
	// instantiated signature.
	if t.Flags&checker.Object != 0 && (t.Kind == checker.Interface || t.Kind == checker.Anonymous) && len(t.Calls) == 1 && len(t.Constructs) == 0 && len(t.Props) == 0 && t.StringIndex == nil && t.NumberIndex == nil {
		if r.fnBusy[t.Calls[0]] {
			return TypeAny, true // reached again from its own result (`interface A { (): A }`)
		}
		if ft, ok := r.of(t.Calls[0]); ok && ft.IsFunc() {
			return ft, true
		}
	}
	switch {
	case t.Flags&checker.Deferred != 0 && r.erasedDeferred(t):
		// A type computed from a type parameter codegen erased (`T[K]` in
		// a generic arrow): a box, read by its run-time kind.
		return TypeAny, true
	case t.Flags&checker.Deferred != 0 && r.param:
		// A parameter a builtin declaration types by what the checker
		// does not evaluate (`ev: AbortSignalEventMap[K]`): a box, read by
		// its run-time kind.
		return TypeAny, true
	case t.Flags&checker.TypeParam != 0 && r.erasedTP[t]:
		return TypeAny, true
	case t.Flags&checker.TypeParam != 0:
		// A generic body is emitted once per instantiation: its type
		// parameter is that instantiation's type.
		name := t.Value
		if name == "" && t.Symbol != nil {
			name = t.Symbol.Name // a declared type parameter's symbol
		}
		if t.Symbol != nil && r.erasedGeneric != nil {
			for _, d := range t.Symbol.Declarations {
				if r.erasedGeneric(d.Node) {
					return TypeAny, true
				}
			}
		}
		if r.typeParam != nil && name != "" && name != "this" {
			if tt, ok := r.typeParam(name); ok {
				return tt, true
			}
			if r.typeParamNames != nil && len(r.typeParamNames()) == 0 {
				// A body compiled once, outside any instantiation (a generic
				// subclass's inherited member, a lowered library function's
				// `__kml_Promise_withResolvers<T>`): its type parameter is
				// erased.
				return TypeAny, true
			}
		}
		if name == "this" && t.Symbol == nil && t.Constraint == nil {
			// The polymorphic `this` of an interface's own member, not yet
			// bound to a receiver: the object itself, held boxed.
			return TypeAny, true
		}
		if name != "this" && r.typeParam != nil && t.Constraint != nil && t.Constraint.Flags&checker.Object != 0 && t.Constraint.Flags&checker.Union == 0 && t.Constraint.Kind == checker.Array {
			// A type parameter this instantiation leaves unbound (a generic
			// method's own, `O extends Opt<any>[]`, its rest arguments
			// collected by every call alike): its array constraint, in the
			// signature and the body both.
			own := r
			own.typeParam = nil
			if ct, ok := own.of(t.Constraint); ok {
				return ct, true
			}
		}
		return Type{}, false
	case t.Kind == checker.Anonymous && t.Value == "globalThis":
		// The global object is a box (emitIdent's globalThis).
		return TypeAny, true
	case t.ModuleNamespace:
		// A module namespace object is held boxed (decideGlobalType,
		// importCallResultObjectType).
		return TypeAny, true
	case len(t.Intersects) > 0 && r.hostIntersection(t):
		// A host or class member's value carrying the others' properties,
		// read dynamically (annotationType's intersection).
		return TypeAny, true
	case t.ImportCall != nil && r.importResult != nil:
		// The module object codegen lays out (importCallResultObjectType).
		return r.importResult(t.ImportCall), true
	case t.Flags&(checker.Any|checker.Unknown|checker.NonPrimitive) != 0, t.AnyArith:
		// `object` too: annotationType holds any non-primitive boxed.
		return TypeAny, true
	case t.Flags&(checker.Number|checker.NumberLiteral) != 0:
		return TypeF64, true
	case t.Flags&checker.Float32 != 0:
		return TypeF32, true
	case t.Flags&checker.Int != 0:
		return Type{IR: "i" + itoa(t.Bits), Signed: t.Signed}, true
	case t.Flags&(checker.String|checker.StringLiteral) != 0:
		return TypePtr, true
	case t.Flags&(checker.Boolean|checker.BooleanLiteral) != 0:
		return TypeBool, true
	case t.Flags&(checker.BigInt|checker.BigIntLiteral) != 0:
		return BigIntType(), true
	case t.Flags&checker.ESSymbol != 0:
		return SymbolType(), true
	case t.Flags&checker.Void != 0:
		return TypeVoid, true
	case t.Flags&checker.Never != 0:
		return TypeNever, true
	case t.Flags&checker.Null != 0:
		return TypeNull, true
	case t.Flags&checker.Undefined != 0:
		return TypeUndefined, true
	case t.Flags&checker.Object != 0 && t.Kind != checker.Array && len(t.TypeArgs) == 1 && declaredLowerType(t.Symbol) == "readonlyarray":
		// The library's `ReadonlyArray<T>` is an array to annotationType, as
		// `readonly T[]` is.
		if e, ok := r.of(t.TypeArgs[0]); ok {
			return ArrayOf(e), true
		}
	case t.Flags&checker.Object != 0 && t.Kind == checker.Array:
		if e, ok := r.of(t.Elem); ok {
			// JavaScript stores anything into an array of only null or
			// undefined (`var a = [,]`): -compat=js holds its elements boxed.
			if r.js != nil && r.js() && t.Elem.Flags&^checker.Nullish == 0 {
				e = TypeAny
			}
			// A void element (`[1].map(() => {})`) is undefined, held as a box.
			if e.IR == "void" {
				e = TypeAny
			}
			return ArrayOf(e), true
		}
		// Functions whose signature has no closure representation (a
		// generator's `[function* () {}]`): emission boxes each element.
		if el := t.Elem; el.Flags&checker.Object != 0 && el.Kind == checker.Function {
			return ArrayOf(TypeAny), true
		}
	case t.Flags&checker.Object != 0 && t.Kind == checker.Tuple && len(t.Elems) > 0:
		// A tuple an annotation declares is a headerless positional struct
		// (TupleType); one the checker inferred (`await Promise.all([a,
		// b])`) is the array codegen builds, of its one element kind.
		// A tuple with an optional element has no fixed shape: an array of
		// boxed elements, as the annotation converts (variableTupleElement).
		if t.Opt > 0 {
			return ArrayOf(TypeAny), true
		}
		elems := make([]Type, len(t.Elems))
		for i, el := range t.Elems {
			e, ok := r.of(el)
			if !ok {
				return Type{}, false
			}
			elems[i] = e
		}
		if r.tuples {
			return TupleType(elems), true
		}
		if j, ok := joinedElems(elems); ok {
			return ArrayOf(j), true
		}
		// Functions of different signatures: emission boxes each element.
		// Any other mix is the tuple itself, an array of its elements
		// (ADR-01977).
		for _, el := range elems {
			if !el.IsFunc() {
				return TupleType(elems), true
			}
		}
		return ArrayOf(TypeAny), true
	case t.Flags&checker.Object != 0 && t.Kind == checker.Function && len(t.Overloads) == 0 && len(t.TypeParams) == 0:
		return r.function(t)
	case t.Flags&checker.Object != 0 && t.Kind == checker.Function && len(t.Overloads) == 0 && t.Symbol == nil:
		// A generic signature written as a type (`<T>(x: T) => T`), not a
		// declaration's: its own type parameters erased.
		er := make(map[*checker.Type]bool, len(r.erasedTP)+len(t.TypeParams))
		for k := range r.erasedTP {
			er[k] = true
		}
		for _, tp := range t.TypeParams {
			er[tp] = true
		}
		r.erasedTP = er
		return r.function(t)
	}
	// A `NodeJS.*` interface is the layout its declaration's tag names.
	if t.Flags&checker.Object != 0 && t.Kind == checker.Interface && t.Symbol != nil && len(t.TypeArgs) == 0 && t.Symbol.Scope != nil && t.Symbol.Scope.Kind == binder.NamespaceScope && r.nodeJS != nil && r.nodeJS(t.Symbol) {
		if nt, ok := taggedLayout(t.Symbol); ok {
			return nt, true
		}
		// A callable one (`process.hrtime`, NodeJS.HRTime) is the
		// function its call signature declares, as the process
		// module implements it.
		if len(t.Calls) == 1 && len(t.Constructs) == 0 {
			if ft, ok := r.of(t.Calls[0]); ok && ft.IsFunc() {
				return ft, true
			}
		}
	}
	// A JavaScript generator holds what it yields boxed, whatever the
	// checker infers from the yields' literals.
	if t.Flags&checker.Object != 0 && t.Kind == checker.Interface && t.Symbol != nil && r.js != nil && r.js() && isGeneratorInstance(t) {
		return GeneratorType(TypeAny, declaredLowerType(t.Symbol) == "asyncgenerator"), true
	}
	// A lib-declared interface a lib module implements as a class:
	// that class, whatever its type arguments (`DataView<ArrayBufferLike>`,
	// `WeakRef<T>`: one class serves every instantiation). An iteration
	// interface's instantiation (`Generator<number>`) is held boxed instead,
	// as annotationType holds it.
	if t.Flags&checker.Object != 0 && t.Kind == checker.Interface && t.Symbol != nil && r.libClass != nil && (len(t.TypeArgs) == 0 || !generatorWrapperNames[t.Symbol.Name]) {
		nodeJS := t.Symbol.Scope != nil && t.Symbol.Scope.Kind == binder.NamespaceScope && r.nodeJS != nil && r.nodeJS(t.Symbol)
		if nodeJS || r.libGlobal(t.Symbol) {
			var args []Type
			for _, a := range t.TypeArgs {
				at, aok := r.of(a)
				if !aok {
					args = nil
					break
				}
				args = append(args, at)
			}
			if ct, ok := r.libClass(t.Symbol, nodeJS, args); ok {
				return ct, true
			}
			// (One class whatever the arguments: `DataView<ArrayBuffer |
			// SharedArrayBuffer>` is the DataView.)
			if args != nil {
				if ct, ok := r.libClass(t.Symbol, nodeJS, nil); ok {
					return ct, true
				}
			}
		}
	}
	// An iteration-protocol interface's instantiation other than a
	// generator's (`Iterator<number>`, a function's result): any object
	// with the protocol's members, held boxed as annotationType holds it.
	if t.Flags&checker.Object != 0 && t.Kind == checker.Interface && t.Symbol != nil && len(t.TypeArgs) > 0 &&
		generatorWrapperNames[t.Symbol.Name] && !isGeneratorInstance(t) && r.libGlobal(t.Symbol) {
		return TypeAny, true
	}
	// A user class's value (`typeof C`) is its constructor reference,
	// boxed (resolveTypeofType).
	if t.Flags&checker.Object != 0 && t.ClassOf != nil && r.class != nil {
		if _, ok := r.class(t.ClassOf); ok || genericClassDecl(t.ClassOf) {
			return TypeAny, true
		}
	}
	// A value of only signatures, several of them (overloads, or call and
	// construct signatures both), is a dynamic function value: a call
	// passes its arguments as written, whichever signature they select.
	if t.Flags&checker.Object != 0 && (t.Kind == checker.Anonymous || t.Kind == checker.Interface || t.Kind == checker.Instance || t.Kind == checker.Function) && t.ClassOf == nil &&
		len(t.Calls)+len(t.Constructs) > 1 && len(t.Calls) > 0 && len(t.Props) == 0 && t.StringIndex == nil && t.NumberIndex == nil {
		return TypeAny, true
	}
	// So is an overload list a type wrote (`((e: 0) => A) & ((e: 1) => B)`).
	if t.Flags&checker.Object != 0 && t.Flags&checker.Union == 0 && t.Kind == checker.Function && len(t.Overloads) > 0 && t.Symbol == nil {
		return TypeAny, true
	}
	// A constructor type (`new (n: string) => A`) holds such a reference
	// too, whichever class it is given.
	if t.Flags&checker.Object != 0 && (t.Kind == checker.Anonymous || t.Kind == checker.Interface) && t.ClassOf == nil && len(t.Constructs) > 0 && len(t.Calls) == 0 && len(t.Props) == 0 && t.StringIndex == nil && t.NumberIndex == nil {
		return TypeAny, true
	}
	// A generic class codegen erased (eraseTypeOnlyGenerics) is one class
	// whatever its type arguments (`EventEmitter<[never]>`).
	if t.Flags&checker.Object != 0 && t.Kind == checker.Instance && t.Symbol != nil && (len(t.TypeArgs) == 0 || codegenErasedClass(t.Symbol)) && r.class != nil {
		if ty, ok := r.class(t.Symbol); ok {
			return ty, true
		}
	}
	if t.Flags&checker.Object != 0 && t.Kind == checker.Instance && t.Symbol != nil && len(t.TypeArgs) > 0 && r.genericClass != nil {
		args := make([]Type, len(t.TypeArgs))
		mapped := true
		for i, a := range t.TypeArgs {
			if args[i], mapped = r.of(a); !mapped {
				break
			}
		}
		if mapped {
			if ty, ok := r.genericClass(t.Symbol, args); ok {
				return ty, true
			}
		}
	}
	if r.objects && t.Flags&checker.Object != 0 && (t.Kind == checker.Anonymous || t.Kind == checker.Interface && dictInterface(t.Symbol)) && t.StringIndex == nil && t.NumberIndex != nil && len(t.Props) == 0 && len(t.Calls) == 0 && len(t.Constructs) == 0 {
		// A number index signature alone (`{ [n: number]: T }`, or an
		// interface of one): the same map, its keys the numbers' strings.
		if v, ok := r.of(t.NumberIndex); ok {
			return dictViewType(v), true
		}
	}
	// An interface with required members beside an index signature (a
	// library's `ProcessVersions extends Dict<string>`, a program's `{ m():
	// void; [k: string]: unknown }`): an open object, read through a box.
	if r.objects && t.Flags&checker.Object != 0 && t.Kind == checker.Interface && len(t.Props) > 0 && !allOptional(t.Props) && t.Symbol != nil && len(t.Calls) == 0 && len(t.Constructs) == 0 &&
		(t.StringIndex != nil && libDeclared(t.Symbol) || (t.StringIndex != nil || t.NumberIndex != nil) && !libDeclared(t.Symbol)) {
		return TypeAny, true
	}
	// A string index signature and nothing else (`{ [k: string]: T }`): a
	// string-keyed map held as a dynamic object.
	// An interface of one (`NodeJS.Dict<T>`) is the same, its value
	// `T | undefined` held as T (codegen's Dict), and so is one naming
	// optional members beside it (`IncomingHttpHeaders`' `accept?`).
	if r.objects && t.Flags&checker.Object != 0 && (t.Kind == checker.Anonymous || t.Kind == checker.Interface) && t.StringIndex != nil && (len(t.Props) == 0 || t.Kind == checker.Interface && allOptional(t.Props) || t.Kind == checker.Anonymous) && len(t.Calls) == 0 && len(t.Constructs) == 0 && (t.Kind != checker.Interface || dictInterface(t.Symbol)) {
		if r.fnBusy[t] {
			return TypeAny, true // a dictionary of itself: the inner one boxed
		}
		busy := map[*checker.Type]bool{t: true}
		for k := range r.fnBusy {
			busy[k] = true
		}
		r.fnBusy = busy
		if v, ok := r.of(t.StringIndex); ok {
			if t.Kind == checker.Interface && v.Nullable() && v.IsUndefined() && (!v.IsDynamic() || len(t.Props) == 0 && !ownIndexSig(t.Symbol)) {
				// A union's box too when the index is `NodeJS.Dict`'s
				// (`ParsedUrlQuery`), not when the interface writes its own
				// (`OutgoingHttpHeaders`) or names members beside it
				// (`IncomingHttpHeaders`); a box keeps its null.
				box := v.IsDynamic()
				v = v.withoutNullable()
				if box && hasNullMember(t.StringIndex) {
					v.setNullable(true)
				}
			}
			dt := dictViewType(v)
			// Its named members, as withDictFields attaches them.
			for _, p := range t.Props {
				ft, ok := r.of(p.Type)
				if !ok {
					return Type{}, false
				}
				if ft.Nullable() && ft.IsUndefined() && !ft.IsDynamic() {
					ft = ft.withoutNullable()
				}
				dt.DictFields = append(dt.DictFields, Field{Name: p.Name, Ty: undefinedableElem(ft)})
			}
			return dt, true
		}
	}
	if r.objects && t.Flags&checker.Object != 0 && t.Kind == checker.Anonymous && t.ClassOf == nil && len(t.Calls) == 0 && len(t.Constructs) == 0 && t.StringIndex == nil && t.NumberIndex == nil {
		// An interface's members, instantiated: the interface's own
		// representation when codegen gives it one (one extending a class).
		if t.Symbol != nil && r.iface != nil {
			if ty, ok := r.iface(t.Symbol); ok {
				return ty, true
			}
		}
		return r.object(t)
	}
	// A builtin interface whose declaration names its native layout; a
	// program's interface over one (`extends TypeError`) is the type codegen
	// registered for it, which carries its own members.
	argOf := r.of
	if lt := declaredLowerType(t.Symbol); lt == "set" || lt == "map" {
		// A collection's tuple elements are the declared tuple's struct, as
		// an annotation's (`new Set(pairs as [string, number][])`).
		rt := r
		rt.tuples = true
		argOf = rt.of
	}
	if ty, ok := lowerTypeReprWith(t, argOf); ok {
		if t.Kind == checker.Interface && t.Symbol != nil && len(t.TypeArgs) == 0 && r.iface != nil {
			if it, ok := r.iface(t.Symbol); ok && it.RefName != "" && it.StructIR() == ty.StructIR() {
				return it, true
			}
		}
		return ty, true
	}
	if t.Flags&checker.Object != 0 && t.Kind == checker.Interface && t.Symbol != nil && len(t.TypeArgs) == 0 && r.iface != nil {
		if ty, ok := r.iface(t.Symbol); ok {
			return ty, true
		}
		if ty, ok := r.arrayHeritage(t.Symbol); ok {
			return ty, true
		}
		// An interface declared in a function body (codegen registers only
		// the module's): its members, as an object type's.
		if r.objects && localInterface(t.Symbol) && len(t.Calls) == 0 && len(t.Constructs) == 0 && t.StringIndex == nil && t.NumberIndex == nil {
			return r.object(t)
		}
		return Type{}, false
	}
	// The iteration protocol (`Iterable<T>`, `AsyncIterable<T>`): a string,
	// an array, a collection or a generator satisfies it, each in its own
	// layout, so a value of it is held boxed; so is a `PromiseLike<T>`, a
	// native promise or any thenable.
	if t.Flags&checker.Object != 0 && t.Kind == checker.Interface && t.Symbol != nil && (t.Symbol.Name == "Iterable" || t.Symbol.Name == "AsyncIterable" || t.Symbol.Name == "PromiseLike") && r.libGlobal(t.Symbol) {
		return TypeAny, true
	}
	// A program's generic interface, instantiated (`Box<number>`): its
	// instantiated members, as an object type's (codegen's per-argument
	// structural type).
	if r.objects && t.Flags&checker.Object != 0 && t.Kind == checker.Interface && t.Symbol != nil && len(t.TypeArgs) > 0 &&
		(!r.libGlobal(t.Symbol) || ambientDataShape(t.Symbol.Name)) && len(t.Calls) == 0 && len(t.Constructs) == 0 && t.StringIndex == nil && t.NumberIndex == nil {
		return r.object(t)
	}
	return Type{}, false
}

// localInterface reports whether sym is an interface declared inside a
// function or block, not at a module's top level.
func localInterface(sym *binder.Symbol) bool {
	if sym.Scope == nil || sym.Scope.Kind == binder.ModuleScope {
		return false
	}
	for _, d := range sym.Declarations {
		if _, ok := d.Node.(*ast.InterfaceDeclaration); ok {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	switch n {
	case 8:
		return "8"
	case 16:
		return "16"
	case 32:
		return "32"
	}
	return "64"
}

// reprOfUnion is a union's representation: `T | null`/`T | undefined` is T's
// nullable form; two or more primitive kinds (`number | bigint`,
// `string | number`) are a NaN-boxed union naming its members.
func (r reprCtx) union(t *checker.Type) (Type, bool) {
	// `ReadableStreamReadResult<T>` (its value and done members) is the
	// read-result layout its `@lowerType` alias names.
	if rt, ok := r.streamReadResult(t); ok {
		return rt, true
	}
	if st, ok := r.settledResult(t); ok {
		return st, true
	}
	// Overloaded functions of each member (`y.filter` on `string[] |
	// number[]`): a boxed function, as functions of different signatures
	// are (below).
	overloads := len(t.Types) > 1
	for _, m := range t.Types {
		overloads = overloads && m.Flags&checker.Object != 0 && m.Kind == checker.Function && len(m.Overloads) > 0
	}
	if overloads {
		return TypeAny, true
	}
	// `ArrayBufferView` (every typed array and DataView): the plain box
	// annotationType gives it, whether or not the DataView class is linked.
	// Typed arrays of different element kinds (`Uint8Array | Int16Array`,
	// `new ctor()` over several constructors) share no layout either.
	// Nor does a typed array and an instance of a class (one extending it:
	// `new K()` over `Uint8Array` and `class MyU extends Uint8Array`).
	views := map[string]bool{}
	own := false
	for _, m := range t.Types {
		if m.Flags&checker.Object != 0 && m.Symbol != nil && r.libGlobal(m.Symbol) {
			switch {
			case m.Symbol.Name == "DataView":
				views["DataView"] = true
			case strings.HasSuffix(m.Symbol.Name, "Array") && m.Symbol.Name != "Array" && m.Symbol.Name != "ReadonlyArray":
				views[m.Symbol.Name] = true
			}
		} else if m.Flags&checker.Object != 0 && m.Symbol != nil && m.Kind == checker.Instance {
			own = true
		}
	}
	if len(views) > 1 || len(views) == 1 && own {
		return TypeAny, true
	}
	var parts []Type
	seen := map[string]bool{}
	null, undef := false, false
	thenable := false // a PromiseLike member
	named := true     // every object member a declared interface
	for _, m := range t.Types {
		if m.Flags&checker.Object != 0 && m.Kind != checker.Interface && !writtenLiteral(m) {
			named = false
		}
		var pt Type
		switch {
		case m.Flags&checker.Object != 0 && m.Kind == checker.Interface && m.Symbol != nil && m.Symbol.Name == "PromiseLike" && r.libGlobal(m.Symbol) && len(t.Types) > 1:
			// `T | PromiseLike<T>`: a parameter's is T (codegen's resolvers
			// adopt a thenable at run time); any other is a value or any
			// thenable, boxed, as codegen's annotation decider holds it.
			thenable = true
			if r.param {
				continue
			}
			pt = TypeAny
		case m.Flags&(checker.Undefined|checker.Void) != 0:
			// `void` beside other members is the undefined a function
			// that returns nothing yields (`string | void`).
			undef = true
			continue
		case m.Flags&checker.Null != 0:
			null = true
			continue
		case m.Flags&(checker.Boolean|checker.BooleanLiteral) != 0:
			pt = TypeBool
		case r.literals && m.Flags&checker.StringLiteral != 0:
			pt = Type{IR: irPtr, Kind: kindStrLiteral, LitValue: m.Value}
		default:
			var ok bool
			mr := r
			if m.Flags&checker.Object != 0 && m.Kind == checker.Anonymous {
				// An object member keeps its literal tags (`k: "a"`), the
				// discriminants codegen narrows the union by.
				mr.literals = true
			}
			pt, ok = mr.of(m)
			if !ok && callableInterface(m) {
				// A callable interface (`EventListener`) beside other
				// members: a box, read by its run-time kind.
				return TypeAny, true
			}
			if ok && isUnconstrainedDynamic(pt) {
				// A dictionary view with only null/undefined beside it
				// stays the view: the box holds the absent value too.
				if pt.DynPropTy != nil && onlyAbsentBesides(t, m) {
					return pt, true
				}
				return TypeAny, true // a member held as `any` holds the rest
			}
			if ok && pt.IsDynamic() && pt.DynPropTy == nil && len(t.Types) > 1 && m.Flags&checker.TypeParam != 0 {
				// A type parameter bound to a boxed union, beside other
				// members: the box holds the rest.
				return TypeAny, true
			}
			if !ok || pt.IsDynamic() || pt.IsNull() || pt.IsUndefined() || pt.IR == "void" {
				return Type{}, false
			}
		}
		// reprKey leaves out a literal's value (metadata), so `'a' | 'b'`
		// would dedupe to `'a'`: two literals are two members here.
		k := reprKey(pt)
		if pt.IsStrLiteral() {
			k += "\x00" + pt.LitValue
		}
		if !seen[k] {
			seen[k] = true
			parts = append(parts, pt)
		}
	}
	// `ArrayBufferLike` (an ArrayBuffer or a SharedArrayBuffer) is held as
	// an ArrayBuffer handle, as codegen resolves the alias.
	if len(parts) > 1 {
		allAB := true
		for _, p := range parts {
			allAB = allAB && p.IsArrayBuffer()
		}
		if allAB {
			parts = []Type{ArrayBufferType()}
		}
	}
	// A promise that never fulfils (`Promise<never>`, a Promise.reject)
	// adds nothing beside another promise: `[Promise.reject(e),
	// Promise.resolve(2)]` holds promises of number.
	if len(parts) > 1 {
		var kept []Type
		for _, p := range parts {
			if p.IsPromise() && p.PromiseType != nil && p.PromiseType.IsNever() {
				continue
			}
			kept = append(kept, p)
		}
		anyPromise := false
		for _, p := range kept {
			anyPromise = anyPromise || p.IsPromise()
		}
		if anyPromise && len(kept) > 0 && len(kept) < len(parts) {
			parts = kept
		}
	}
	// Literals of one string kind (`'a' | 'b'`) are a string: only a lone
	// literal keeps its value (a discriminant's tag).
	if len(parts) > 1 {
		allStr := true
		for _, p := range parts {
			allStr = allStr && p.IR == irPtr && isStringTy(p) && !hasHandleFlag(p)
		}
		if allStr {
			parts = []Type{TypePtr}
		}
	}
	if thenable && len(parts) > 1 {
		return TypeAny, true // a value of several kinds or a thenable: boxed
	}
	// A class beside its subclasses (`ClientHttp2Stream | Http2Stream`, a
	// flow union of `this`) is one pointer: the base class's.
	if len(parts) > 1 && r.extends != nil {
		for _, base := range parts {
			if base.ClassName == "" {
				continue
			}
			all := true
			for _, p := range parts {
				all = all && p.ClassName != "" && (p.ClassName == base.ClassName || r.extends(p.ClassName, base.ClassName))
			}
			if all {
				parts = []Type{base}
				break
			}
		}
	}
	switch {
	case len(parts) == 0 && undef && null:
		// `null | undefined`: a box keeps which one it holds.
		return TypeAny, true
	case len(parts) == 0 && undef:
		return TypeUndefined, true
	case len(parts) == 0:
		return TypeNull, true
	case len(parts) == 1:
		p := parts[0]
		if !null && !undef {
			return p, true
		}
		if null && undef && threeStateEligible(p) {
			return withNullAndUndef(p), true
		}
		if p.IR == irPtr || p.IsArray() {
			p.setNullable(true)
			p.setUndefined(undef)
			return p, true
		}
		if undef {
			return undefinedableElem(p), true
		}
		p.setNullable(true) // `T | null`
		return p, true
	}
	// Task promises of one payload differing only in which absences it
	// holds (`Promise<number | null> | Promise<number | null | undefined>`):
	// the one promise whose payload holds them all, as a task settles it.
	if merged, ok := mergeTaskPromises(parts); ok && !null && !undef {
		return merged, true
	}
	allFuncs := true
	for _, p := range parts {
		allFuncs = allFuncs && p.IsFunc()
	}
	if allFuncs {
		// Functions of different signatures (`String | Number`): a boxed
		// function, called through the dynamic convention, holds either.
		return TypeAny, true
	}
	// A function beside scalars (`boolean | ((a: string, b: string) =>
	// number)`): a box of each, as annotationType gives it.
	funcs, scalars := 0, 0
	for _, p := range parts {
		switch {
		case p.IsFunc():
			funcs++
		case !p.IsDynamic() && !p.IsObject() && !p.IsArray() && !p.IsClass() && (p.IR == "i1" || p.Float || p.IsInteger() || p.IR == irPtr && isStringTy(p) && !hasHandleFlag(p)):
			scalars++
		}
	}
	if funcs > 0 && funcs+scalars == len(parts) {
		return Type{IR: TypeAny.IR, Kind: kindDynamic, UnionMembers: parts, Absent: absenceOf(null || undef, undef, false)}, true
	}
	objs, arrs, byteArrs := 0, 0, 0
	for _, p := range parts {
		// A class instance names its own layout (`Readable | Writable`,
		// as annotationType boxes it), and an array its header, whatever the
		// other arrays beside it; a function has none a box could name.
		if p.IsArray() && (p.IsTypedArray() || p.IsBuffer()) {
			byteArrs++
		} else if p.IsArray() {
			arrs++
		}
		if p.IsObject() && !p.IsSymbol() && !p.IsClass() {
			objs++
		}
	}
	// Typed-array views beside a class (`ArrayBufferView`: every typed
	// array and DataView): the plain box annotationType gives the union.
	classes := 0
	for _, p := range parts {
		if p.IsClass() {
			classes++
		}
	}
	if byteArrs > 1 && classes > 0 {
		return TypeAny, true
	}
	if objs > 0 && objs < len(parts) {
		// Strings beside one plain object (`BufferEncoding |
		// ObjectEncodingOptions`): a box of the string and the object, as
		// annotationType gives it; scalars beside one (`number | Error`), a
		// box of each; several objects, or a function beside them, a box of
		// every member.
		var strs, rest []Type
		for _, p := range parts {
			if p.IR == irPtr && isStringTy(p) && !hasHandleFlag(p) {
				strs = append(strs, p)
			} else {
				rest = append(rest, p)
			}
		}
		if objs == 1 && len(strs) > 0 && len(rest) == 1 {
			return Type{IR: TypeAny.IR, Kind: kindDynamic, UnionMembers: []Type{TypePtr, rest[0]}, Absent: absenceOf(null || undef, undef, false)}, true
		}
		return Type{IR: TypeAny.IR, Kind: kindDynamic, UnionMembers: parts, Absent: absenceOf(null || undef, undef, false)}, true
	}
	if objs > 0 && !named {
		// Plain objects of different shapes (tsc's normalized `{ a: number }
		// | { a?: undefined }`): the box a `U` annotation of them gives,
		// read by key. Declared interfaces keep their layouts, as
		// annotationType's union does.
		return Type{IR: TypeAny.IR, Kind: kindDynamic, Absent: absenceOf(null || undef, undef, false)}, true
	}
	return Type{IR: TypeAny.IR, Kind: kindDynamic, UnionMembers: parts, Absent: absenceOf(null || undef, undef, false)}, true
}

// declaredByInterface reports an object type an interface declaration
// declares (not a module's namespace object or a literal's type).
func declaredByInterface(t *checker.Type) bool {
	if t.Symbol == nil || len(t.Symbol.Declarations) == 0 {
		return false
	}
	_, ok := t.Symbol.Declarations[0].Node.(*ast.InterfaceDeclaration)
	return ok
}

// genericOverloads reports an overloaded function type every signature of
// which has its own type parameters.
func genericOverloads(t *checker.Type) bool {
	if len(t.Overloads) == 0 {
		return false
	}
	for _, o := range t.Overloads {
		if len(o.TypeParams) == 0 {
			return false
		}
	}
	return true
}

// firstOverload is an object's overloaded method member as codegen lays
// it out: its first overload, that overload's own type parameters erased
// (an interface's `then<U>(…)` overloads).
func (r reprCtx) firstOverload(t *checker.Type) (Type, bool) {
	first := t.Overloads[0]
	er := make(map[*checker.Type]bool, len(r.erasedTP)+len(first.TypeParams))
	for k := range r.erasedTP {
		er[k] = true
	}
	for _, tp := range first.TypeParams {
		er[tp] = true
	}
	r.erasedTP = er
	return r.function(first)
}

// writtenLiteral reports a type literal the source wrote (`{ kind: 'a';
// size: number }`): every member a signature, none a normalized object
// literal's synthesized optional undefined. It keeps its layout in a union,
// as a declared interface does.
func writtenLiteral(t *checker.Type) bool {
	if t.Kind != checker.Anonymous || len(t.Props) == 0 {
		return false
	}
	for _, p := range t.Props {
		if _, method := p.Decl.(*ast.MethodSignature); !p.Written && !method {
			return false
		}
	}
	return true
}

// ownIndexSig reports an interface whose declaration writes its string
// index signature itself, not as a generic dictionary (`Dict<T>`'s `T |
// undefined`, held as T).
// dictInterface reports an interface codegen registers as a dictionary:
// one writing its own index signature, or extending a generic one
// (`extends NodeJS.Dict<T>`) — not one inheriting a plain interface's
// (dictHeritage).
func dictInterface(sym *binder.Symbol) bool {
	if sym == nil || ownIndexSig(sym) {
		return true
	}
	for _, d := range sym.Declarations {
		if id, ok := d.Node.(*ast.InterfaceDeclaration); ok {
			if id.IndexSig != nil && len(id.TypeParams) > 0 && len(id.Fields) == 0 {
				return true // a generic dictionary itself (`NodeJS.Dict<T>`)
			}
			for _, h := range id.Heritage {
				if ref, ok := h.(*ast.TypeReference); ok && len(ref.TypeArgs) > 0 {
					return true
				}
			}
		}
	}
	return false
}

func ownIndexSig(sym *binder.Symbol) bool {
	if sym == nil {
		return false
	}
	for _, d := range sym.Declarations {
		if id, ok := d.Node.(*ast.InterfaceDeclaration); ok && id.IndexSig != nil && len(id.TypeParams) == 0 {
			return true
		}
	}
	return false
}

// overloadedClosureCall answers a call of an overloaded function whose
// implementation declares a function return (`promisify(f)`): the call runs
// the implementation, so it returns that declared closure, whichever
// overload's signature the checker picked.
func (o frontOracle) overloadedClosureCall(call *ast.CallExpression) (Type, bool) {
	id, isID := call.Callee.(*ast.Identifier)
	if !isID {
		return Type{}, false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil {
		return Type{}, false
	}
	for _, d := range sym.Declarations {
		fd, isFD := d.Node.(*ast.FunctionDeclaration)
		if !isFD || len(fd.Overloads) == 0 || !annotIsFunctionType(fd.ReturnType) || fd.IsAsync || fd.IsGenerator {
			continue
		}
		if ft, ok := o.AnnotationType(fd.ReturnType); ok && ft.IsFunc() {
			return ft, true
		}
	}
	return Type{}, false
}

// hasNullMember reports whether t is null or a union with a null member.
func hasNullMember(t *checker.Type) bool {
	for _, m := range checkerMembers(t) {
		if m.Flags&checker.Null != 0 {
			return true
		}
	}
	return false
}

// reprOfFunction is a function type's representation: a closure pointer
// carrying its parameters' and result's representations.
func (r reprCtx) function(t *checker.Type) (Type, bool) {
	r.param = false // a nested signature's result is no parameter
	if r.fnBusy[t] || len(r.fnBusy) > 0 && selfContaining(t) {
		return TypeAny, true // the inner occurrence (or a copy of it)
	}
	busy := map[*checker.Type]bool{t: true}
	for k := range r.fnBusy {
		busy[k] = true
	}
	r.fnBusy = busy
	var params []Type
	if t.ThisType != nil {
		this, ok := r.of(t.ThisType)
		if !ok {
			return Type{}, false
		}
		params = append(params, this)
	}
	// Every parameter's omittability is explicit, as an annotation's is: a
	// first-class call site rejects a missing required argument only where
	// it is recorded.
	var optional []bool
	if len(t.Params) > 0 {
		optional = make([]bool, len(t.Params))
	}
	// A parameter's type is its annotation's (or a contextual
	// annotation's): string literals stay literals, as annotationType keeps
	// them in a signature (a discriminated union parameter's tags).
	pctx := r
	pctx.literals, pctx.param = true, true
	pctx.tuples = true // a tuple parameter is its annotation's tuple
	for i, p := range t.Params {
		ctx := pctx
		if i == len(t.Params)-1 && t.HasRestParam() {
			// A rest tuple is the array its call collects, not a tuple value.
			ctx.tuples = r.tuples
		}
		pr, ok := ctx.of(p)
		if !ok && i == len(t.Params)-1 && t.HasRestParam() && p.Flags&checker.Object != 0 && p.Kind == checker.Tuple {
			// A tuple rest parameter (`...xs: [boolean, number]`) of mixed
			// elements: the boxed array the call collects its arguments into.
			pr, ok = ArrayOf(TypeAny), true
		}
		if !ok {
			return Type{}, false
		}
		if pr.IR == "void" {
			pr = TypeAny // a `void` parameter (`T` = void) holds whatever is passed
		}
		if i == len(t.Params)-1 && t.HasRestParam() && !pr.IsArray() && pr.IsDynamic() {
			// A rest parameter typed by an erased type parameter (`...o: O`,
			// `O extends Opt<any>[]`): the boxed array its arguments fill.
			pr = ArrayOf(TypeAny)
		}
		params = append(params, pr)
		if t.OptionalParam(i) {
			optional[i] = true
		}
	}
	ret := TypeVoid
	if selfReturningDecl(t) {
		ret = TypeAny // itself, boxed (returnsOwnName)
	} else if t.Ret() != nil && t.Ret().Flags&checker.Never != 0 {
		ret = TypeNever // a function that never returns (ADR-00869)
	} else if t.Ret() != nil {
		rr, ok := r.of(t.Ret())
		if !ok {
			return Type{}, false
		}
		ret = rr
	}
	ty := FuncType(params, ret)
	ty.FuncHasRest = t.HasRestParam()
	ty.FuncThis = t.ThisType != nil
	ty.FuncParamOptional = optional
	if len(t.ParamNames) == len(t.Params) && t.ThisType == nil {
		ty.FuncParamNames = t.ParamNames
	}
	// A function reading `arguments` (its implicit rest) has its caller
	// count what it passes (funcTypeFromSig).
	ty.FuncCountsArgs = countsArgs(ty.FuncHasRest, ty.FuncParamNames)
	return ty, true
}

// object is an object type's representation: a dynamic object in the js
// lane, otherwise the fixed-layout object of its properties in order.
func (r reprCtx) object(t *checker.Type) (Type, bool) {
	if r.js != nil && r.js() {
		return TypeAny, true
	}
	// `{}` takes every non-nullish value, any object among them: boxed, as
	// an annotation `{}` resolves.
	if len(t.Props) == 0 && t.Symbol == nil {
		return TypeAny, true
	}
	// A self-reference inside the literal is the stub annotationType cuts
	// the cycle at: an object with no fields of its own.
	if r.mapping[t] {
		if t.RecursiveAlias() && len(t.TypeArgs) > 0 {
			// A generic one's instance names no registered alias: held
			// boxed, its object read by its header.
			return TypeAny, true
		}
		stub := UserObjectType(nil)
		if t.RecursiveAlias() {
			// Named, so a field read re-resolves it once the alias is
			// registered (canonicalizeClassTy).
			if ad, ok := t.Symbol.Declarations[0].Node.(*ast.TypeAliasDeclaration); ok {
				stub.RefName = ad.Name
			}
		}
		return stub, true
	}
	if r.mapping == nil {
		r.mapping = map[*checker.Type]bool{}
	}
	r.mapping[t] = true
	defer delete(r.mapping, t)
	fields := make([]Field, 0, len(t.Props))
	for _, p := range t.Props {
		if p.Accessor && (t.Kind != checker.Anonymous || t.ClassOf != nil) {
			return Type{}, false
		}
		// A type literal's accessor (`{ get done(): boolean }`) is a
		// property to its readers: the object's own getter answers it.
		ty, ok := r.of(p.Type)
		if p.Type.Flags&checker.Object != 0 && p.Type.Kind == checker.Function && len(p.Type.Overloads) > 0 {
			// An overloaded method: a dynamic function value, called with
			// its arguments as written (withMethodFields).
			ty, ok = TypeAny, true
		}
		if p.Write != nil && p.Write != p.Type {
			// A setter taking other values than its getter gives (`get
			// done(): false; set done(v: T | null)`): the slot holds either.
			ty, ok = TypeAny, true
		}
		if !ok && p.Type.Flags&checker.Object != 0 && p.Type.Kind == checker.Function && genericOverloads(p.Type) && declaredByInterface(t) {
			ty, ok = r.firstOverload(p.Type)
		}
		if r.literals && p.Type.Flags&checker.StringLiteral != 0 && p.Type.Flags&checker.Union == 0 {
			// An annotation's literal-typed member keeps its literal, as
			// annotationType resolves the member's annotation.
			ty, ok = Type{IR: irPtr, Kind: kindStrLiteral, LitValue: p.Type.Value}, true
		}
		if isGeneratorFunc(p.Type) {
			// An object's generator method is held as `any` (a generator
			// declaration's value: literalGenFields).
			ty, ok = TypeAny, true
		}
		if !ok {
			return Type{}, false
		}
		if p.Optional {
			ty = optionalFieldType(ty) // `key?: T` holds absence, as annotationType widens it
		}
		fields = append(fields, Field{Name: p.Name, Ty: ty, Optional: p.Optional})
	}
	return UserObjectType(fields), true
}

// isGlobalFunc reports whether id names a declaration of the builtin
// globals, directly or through an unannotated variable it initializes
// (`const f = parseInt`).
func (o frontOracle) isGlobalFunc(id *ast.Identifier) bool {
	sym, _ := o.b.Resolve(id)
	if sym == nil || o.b.Globals == nil {
		return false
	}
	if len(sym.Declarations) == 1 {
		if v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration); ok && v.TypeAnnot == nil {
			if init, ok := v.Init.(*ast.Identifier); ok && init != id {
				return o.isGlobalFunc(init)
			}
		}
	}
	for sc := sym.Scope; sc != nil; sc = sc.Parent {
		if sc == o.b.Module {
			return false
		}
		if sc == o.b.Globals {
			return true
		}
	}
	return false
}

// withPromiseCallbacks collects the function literals passed as the
// executor of `new Promise(…)` and as a rejection handler.
func (o frontOracle) withPromiseCallbacks(prog *ast.Program) frontOracle {
	o.written = map[ast.Node]map[*binder.Symbol]bool{}
	o.executors, o.onRejected = map[ast.Node]*ast.NewExpression{}, map[ast.Node]ast.Expression{}
	o.anyBound = map[ast.Node]bool{}
	o.inBody = map[ast.Node]bool{}
	o.widenedReduceOf = map[ast.Node]*ast.CallExpression{}
	isFn := func(e ast.Expression) bool {
		switch e.(type) {
		case *ast.ArrowFunction, *ast.FunctionExpression:
			return true
		}
		return false
	}
	// A worker module's top level, kept out of Body, holds executors too.
	stmts := prog.Body
	for _, wm := range prog.WorkerModules {
		stmts = append(stmts[:len(stmts):len(stmts)], wm.Body...)
	}
	for _, st := range stmts {
		ast.Inspect(st, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.VarDeclaration:
				if x.TypeAnnot != nil && isFn(x.Init) {
					if kw, ok := x.TypeAnnot.(*ast.KeywordType); ok && kw.Keyword == "any" {
						o.anyBound[x.Init] = true
					}
				}
			case *ast.AssignmentExpression:
				// A function stored into an untyped module binding declared
				// without a value (`var ref; ref = function () {…}`), which
				// codegen holds as `any`, is boxed too.
				if fe, gen := x.Right.(*ast.FunctionExpression); gen && fe.IsGenerator {
					break // a generator keeps its own convention
				}
				if id, ok := x.Left.(*ast.Identifier); ok && x.Op == "=" && isFn(x.Right) {
					if sym, _ := o.b.Resolve(id); sym != nil {
						if v, ok := singleVarDecl(sym); ok && v.TypeAnnot == nil && v.Init == nil && sym.Scope != nil && sym.Scope.Kind == binder.ModuleScope {
							o.anyBound[x.Right] = true
						}
					}
				}
			case *ast.NewExpression:
				if len(x.Args) > 0 && !x.Qualified && isFn(x.Args[0]) && (x.ClassName == "Promise" || o.promiseSubclassImplicitCtor(x.ClassName)) {
					o.executors[x.Args[0]] = x
				}
			case *ast.CallExpression:
				if cb := jsWidenedReduceCallback(x); cb != nil {
					o.widenedReduceOf[cb] = x
				}
				if m, ok := x.Callee.(*ast.MemberExpression); ok {
					if m.Property == "catch" && len(x.Args) > 0 && isFn(x.Args[0]) {
						o.onRejected[x.Args[0]] = m.Object
					} else if m.Property == "then" && len(x.Args) > 1 && isFn(x.Args[1]) {
						o.onRejected[x.Args[1]] = m.Object
					}
				}
			}
			return true
		})
	}
	return o
}

// isRejectionParam reports a name bound to the first parameter of a native
// promise's rejection handler, unannotated or annotated any/unknown: the
// handler is called with the caught value (emitRejectCallback).
func (o frontOracle) isRejectionParam(id *ast.Identifier) bool {
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return false
	}
	fn := sym.Declarations[0].Node
	recv, ok := o.onRejected[fn]
	if !ok {
		return false
	}
	var params []ast.Param
	switch f := fn.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	}
	if len(params) == 0 || params[0].Name != id.Name {
		return false
	}
	if params[0].Type != nil {
		at, ok := o.AnnotationType(params[0].Type)
		if len(params) != 1 || !ok || !at.IsDynamic() {
			return false
		}
	}
	rt := o.c.TypeOf(recv)
	if o.c.Unanswered(rt) {
		return false
	}
	r, ok := o.r.of(rt)
	return ok && r.IsPromise()
}

// executorParam is the `new Promise(…)` whose executor declares the name
// id is bound to, and that parameter's index (resolve 0, reject 1).
func (o frontOracle) executorParam(id *ast.Identifier) (*ast.NewExpression, int) {
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return nil, -1
	}
	ne := o.executors[sym.Declarations[0].Node]
	if ne == nil {
		return nil, -1
	}
	var params []ast.Param
	switch fn := sym.Declarations[0].Node.(type) {
	case *ast.ArrowFunction:
		params = fn.Params
	case *ast.FunctionExpression:
		params = fn.Params
	}
	for i, p := range params {
		if i < 2 && p.Name == id.Name {
			return ne, i
		}
	}
	return nil, -1
}

// assertedOperand is the operand of the `x as T` e is, directly or as the
// initializer of an unannotated variable e names; nil otherwise.
func (o frontOracle) assertedOperand(e ast.Expression) ast.Expression {
	switch x := e.(type) {
	case *ast.AsExpression:
		if x.TypeAnnot != nil {
			return x.Expr
		}
	case *ast.Identifier:
		sym, _ := o.b.Resolve(x)
		if sym == nil || len(sym.Declarations) != 1 {
			return nil
		}
		if v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration); ok && v.TypeAnnot == nil && v.Kind == "const" {
			if as, ok := v.Init.(*ast.AsExpression); ok && as.TypeAnnot != nil {
				return as.Expr
			}
		}
	}
	return nil
}

// presentType reports a type with no null, undefined or void in it.
func presentType(t *checker.Type) bool {
	if t.Flags&(checker.Any|checker.Unknown|checker.Never|checker.Null|checker.Undefined|checker.Void) != 0 {
		return false
	}
	for _, m := range t.Types {
		if m.Flags&(checker.Null|checker.Undefined|checker.Void|checker.Any|checker.Unknown) != 0 {
			return false
		}
	}
	return true
}

// literalStorage is an object literal's layout with each plain property's
// field the representation its value is stored in: a value a check narrows
// (`{ v: first }` after `typeof first === 'boolean'`) keeps its own storage,
// as a narrowed read does not change how a value is held.
func (o frontOracle) literalStorage(lit *ast.ObjectLiteral, ty Type) Type {
	// A method reading the literal's own binding (`n: function () { return
	// o.m(); }`) asks for the literal again: the checker's layout there.
	if o.storageBusy[lit] {
		return ty
	}
	o.storageBusy[lit] = true
	defer delete(o.storageBusy, lit)
	fields := append([]Field(nil), ty.Fields...)
	for _, p := range lit.Properties {
		if p.KeyExpr != nil || p.AccessorKind != "" || p.Value == nil {
			continue
		}
		// A name's field holds its binding; a method's, the function
		// emission builds (its `this` parameter included).
		var vt Type
		var ok bool
		switch v := p.Value.(type) {
		case *ast.Identifier:
			if vt, ok = o.ExprType(v); !ok {
				continue
			}
		case *ast.FunctionExpression:
			if vt, ok = o.ExprType(v); !ok || !vt.IsFunc() {
				continue
			}
		case *ast.AsExpression:
			// A literal asserted to an array of tuples is built in that
			// shape (emitAsExpression).
			if _, isLit := v.Expr.(*ast.ArrayLiteral); !isLit || v.TypeAnnot == nil {
				continue
			}
			if vt, ok = o.AnnotationType(v.TypeAnnot); !ok || !vt.IsArray() || vt.ElemType == nil || !vt.ElemType.IsTuple() {
				continue
			}
		default:
			continue
		}
		for i := range fields {
			if fields[i].Name == p.Key {
				fields[i].Ty = vt
			}
		}
	}
	ty.Fields = fields
	return ty
}

// narrowedToInstance reports a receiver held as `any` that a check narrows
// to a class instance (`x instanceof C`): codegen reads its members through
// the class's layout.
func (o frontOracle) narrowedToInstance(obj ast.Expression) bool {
	if !flowTrusted(o.b, obj) {
		return false
	}
	t := o.c.TypeOf(obj)
	return !o.c.Unanswered(t) && t.Flags&checker.Object != 0 && t.Kind == checker.Instance && t.Flags&checker.Union == 0
}

// plainMemberRead reports `obj.name`: a named property, not a computed or
// symbol key, which codegen reads dynamically whatever the receiver.
func plainMemberRead(e ast.Expression) bool {
	m, ok := e.(*ast.MemberExpression)
	return ok && !strings.HasPrefix(m.Property, "@@") && !strings.HasPrefix(m.Property, "__kml")
}

// onlyAbsentBesides reports that every member of union u other than m is
// null or undefined.
func onlyAbsentBesides(u, m *checker.Type) bool {
	for _, o := range u.Types {
		if o != m && o.Flags&(checker.Null|checker.Undefined) == 0 {
			return false
		}
	}
	return true
}

// boxedIdentValue reports an object literal with a property whose value is
// a binding held boxed while its type maps to a scalar: the type alone
// does not give the layout codegen builds.
func (o frontOracle) boxedIdentValue(lit *ast.ObjectLiteral) bool {
	for _, p := range lit.Properties {
		if _, isID := p.Value.(*ast.Identifier); !isID {
			continue
		}
		vt, ok := o.ExprType(p.Value)
		if !ok || !isUnconstrainedDynamic(vt) {
			continue
		}
		if ct, cok := o.r.of(o.c.TypeOf(p.Value)); cok && !ct.IsDynamic() {
			return true
		}
	}
	return false
}

// genFuncProp reports an object type with a property of a function type
// returning a generator: the type alone does not tell a generator method
// (held as `any`) from a function returning one (held as itself).
func genFuncProp(t *checker.Type) bool {
	if t == nil || t.Flags&checker.Object == 0 {
		return false
	}
	for _, p := range t.Props {
		if isGeneratorFunc(p.Type) {
			return true
		}
	}
	return false
}

// isGeneratorInstance reports a Generator or an AsyncGenerator.
func isGeneratorInstance(t *checker.Type) bool {
	if t == nil || t.Flags&checker.Object == 0 || t.Symbol == nil || (t.Kind != checker.Interface && t.Kind != checker.Instance) {
		return false
	}
	l := declaredLowerType(t.Symbol)
	return l == "generator" || l == "asyncgenerator"
}

// isGeneratorFunc reports a function type returning a Generator or an
// AsyncGenerator.
func isGeneratorFunc(t *checker.Type) bool {
	if t.Flags&checker.Object == 0 || t.Kind != checker.Function {
		return false
	}
	return isGeneratorInstance(t.Ret())
}

// storesPresent mirrors the emitter's: every reaching store is a
// declaration or plain assignment of a value the checker types present.
func (o frontOracle) storesPresent(id *ast.Identifier) bool {
	stores, ok := o.b.ReachingStores(id)
	if !ok {
		return false
	}
	for _, s := range stores {
		var v ast.Expression
		switch x := s.(type) {
		case *ast.VarDeclaration:
			v = x.Init
		case *ast.AssignmentExpression:
			v = x.Right
		}
		if v == nil {
			return false
		}
		t := o.c.TypeOf(v)
		if o.c.Unanswered(t) || !presentType(t) || t.Flags&(checker.Any|checker.Unknown) != 0 {
			return false
		}
	}
	return true
}

// reprStoresPresent is the emitter's storesPresent: every reaching store's
// value is held present (an element read, which may be absent, is not).
func (o frontOracle) reprStoresPresent(id *ast.Identifier) bool {
	stores, ok := o.b.ReachingStores(id)
	if !ok {
		return false
	}
	// A store reading the binding itself (`n = n + 1`) asks again: not
	// present on that path, as the emitter's busy set answers.
	if o.storesBusy != nil {
		if o.storesBusy[id] {
			return false
		}
		o.storesBusy[id] = true
		defer delete(o.storesBusy, id)
	}
	for _, s := range stores {
		var v ast.Expression
		switch x := s.(type) {
		case *ast.VarDeclaration:
			v = x.Init
		case *ast.AssignmentExpression:
			v = x.Right
		}
		if v == nil {
			return false
		}
		t, ok := o.ExprType(v)
		if !ok || t.IR == "" || t.IR == "void" || t.Nullable() || t.IsUndefined() || t.IsNull() || t.IsDynamic() {
			return false
		}
	}
	return true
}

// declaresAbsent reports a reference whose declared storage type itself
// holds null or undefined (`string[] | undefined` from an index signature
// that says so): a guard's narrowing of it is flow's, not an unchecked
// index read typed present. Codegen narrows such an array binding (its
// absence is the null header) and keeps an object one nullable.
func (o frontOracle) declaresAbsent(e ast.Expression) bool {
	t := o.c.StorageTypeOf(e)
	if o.c.Unanswered(t) {
		return false
	}
	for _, m := range checkerMembers(t) {
		if m.Flags&(checker.Undefined|checker.Null) != 0 {
			return true
		}
	}
	return false
}

// storesIndexRead mirrors the emitter's for a variable reference.
func (o frontOracle) storesIndexRead(e ast.Expression) bool {
	id, ok := e.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil {
		return false
	}
	for _, st := range o.b.StoresTo(sym) {
		var v ast.Expression
		switch x := st.(type) {
		case *ast.VarDeclaration:
			v = x.Init
		case *ast.AssignmentExpression:
			v = x.Right
		}
		for {
			as, ok := v.(*ast.AsExpression)
			if !ok {
				break
			}
			v = as.Expr
		}
		if _, ok := v.(*ast.IndexExpression); ok {
			return true
		}
	}
	return false
}

// wellKnownSymbol reports a read of a builtin `Symbol.X` symbol.
func (o frontOracle) wellKnownSymbol(e ast.Expression) bool {
	m, ok := e.(*ast.MemberExpression)
	if !ok {
		return false
	}
	id, ok := m.Object.(*ast.Identifier)
	if !ok || id.Name != "Symbol" {
		return false
	}
	if sym, _ := o.b.Resolve(id); sym != nil && (o.b.Globals == nil || sym.Scope != o.b.Globals) {
		return false
	}
	t := o.c.TypeOf(m)
	return !o.c.Unanswered(t) && t.Flags&checker.ESSymbol != 0
}

// jsBoxedArrayLiteral mirrors the js lane's inferArrayType: a literal of
// only holes, `undefined` and `null`, or of two or more element kinds, is
// boxed (`any[]`).
func (o frontOracle) jsBoxedArrayLiteral(lit *ast.ArrayLiteral) bool {
	if len(lit.Elements) == 0 {
		return false
	}
	nullish := true
	for _, el := range lit.Elements {
		if _, ok := el.(*ast.NullLiteral); !ok {
			nullish = false
			break
		}
	}
	if nullish {
		return true
	}
	kinds := map[string]bool{}
	for _, el := range lit.Elements {
		if _, ok := el.(*ast.SpreadElement); ok {
			return false
		}
		t, ok := o.ExprType(el)
		if !ok {
			return false
		}
		kinds[elemKindKeyBroad(t)] = true
	}
	return len(kinds) >= 2
}

// jsBoxedInit reports an unannotated js-lane declaration whose initializer
// is a boxed array literal (jsBoxedArrayLiteral).
func (o frontOracle) jsBoxedInit(v *ast.VarDeclaration) bool {
	lit, ok := v.Init.(*ast.ArrayLiteral)
	return ok && v.TypeAnnot == nil && o.r.js != nil && o.r.js() && o.jsBoxedArrayLiteral(lit)
}

// matchArray reports a value of the lib's RegExpMatchArray/RegExpExecArray
// (or one of them or null, narrowed).
func (o frontOracle) matchArray(e ast.Expression) bool {
	t := o.c.TypeOf(e)
	if o.c.Unanswered(t) {
		return false
	}
	t = o.c.NonNullable(t)
	if t.Flags&checker.Object == 0 || t.Symbol == nil {
		return false
	}
	l := declaredLowerType(t.Symbol)
	return l == "regexpmatcharray" || l == "regexpexecarray"
}

// libGlobal reports a symbol the builtin declarations declare globally (a
// scope with no parent), not one of the program's.
func (r reprCtx) libGlobal(sym *binder.Symbol) bool {
	return sym.Scope != nil && sym.Scope.Parent == nil && sym.Scope.Kind == binder.ModuleScope
}

// libDeclared reports a symbol the builtin declarations declare, globally
// or in one of their namespaces (`NodeJS.ProcessVersions`): its scopes
// reach the root without passing a program module.
func libDeclared(sym *binder.Symbol) bool {
	for s := sym.Scope; s != nil; s = s.Parent {
		if s.Kind == binder.ModuleScope {
			return s.Parent == nil
		}
	}
	return false
}

// returnsAnswered is a body's return type where the checker has none but
// every return's value has one representation (a member read the lib does
// not declare, answered as codegen reads it).
func (o frontOracle) returnsAnswered(body *ast.BlockStatement) (Type, bool, bool) {
	var ty Type
	n := 0
	ok := true
	ast.Inspect(body, func(x ast.Node) bool {
		if !ok {
			return false
		}
		switch r := x.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		case *ast.ReturnStatement:
			if r.Value == nil {
				ok = false
				return false
			}
			t, has := o.ExprType(r.Value)
			if !has || n > 0 && reprKey(t) != reprKey(ty) {
				ok = false
				return false
			}
			ty, n = t, n+1
		}
		return true
	})
	if !ok || n == 0 {
		return Type{}, false, false
	}
	return ty, true, true
}

// unionSubset is the union ty narrowed to nt's members, when each of them
// is a distinct member of ty and some are ruled out: a box of those
// members, in ty's order, or the one left.
func (o frontOracle) unionSubset(ty Type, nt *checker.Type) (Type, bool) {
	keep := map[string]bool{}
	for _, m := range nt.Types {
		r, ok := o.r.of(m)
		if !ok {
			return Type{}, false
		}
		if r.IsDynamic() && len(r.UnionMembers) > 0 {
			for _, rm := range r.UnionMembers {
				keep[reprKey(rm)] = true
			}
			continue
		}
		keep[reprKey(r)] = true
	}
	var sub []Type
	for _, m := range ty.UnionMembers {
		if keep[reprKey(m)] {
			sub = append(sub, m)
			delete(keep, reprKey(m))
		}
	}
	if len(keep) > 0 || len(sub) == 0 || len(sub) == len(ty.UnionMembers) {
		return Type{}, false
	}
	if len(sub) == 1 {
		return sub[0], true
	}
	out := ty
	out.UnionMembers = sub
	return out, true
}

// boxedParams is a function's parameters taken boxed: each an `any`, a
// rest one an `any[]`.
func boxedParams(t Type) []Type {
	params := make([]Type, len(t.FuncParams))
	for i := range params {
		params[i] = TypeAny
	}
	if t.FuncHasRest && len(params) > 0 {
		params[len(params)-1] = ArrayOf(TypeAny)
	}
	return params
}

// typedArrayView reports whether m reads a typed array's `buffer` or
// `byteOffset`.
func (o frontOracle) typedArrayView(m *ast.MemberExpression) bool {
	if m.Property != "buffer" && m.Property != "byteOffset" {
		return false
	}
	if rt, ok := o.ExprType(m.Object); ok && rt.IsTypedArray() {
		return true
	}
	// A union binding narrowed to a view reads as it (emitIdent).
	if nt := o.c.TypeOf(m.Object); !o.c.Unanswered(nt) {
		if tt, ok := o.r.of(nt); ok && tt.IsTypedArray() {
			return true
		}
	}
	return false
}

// intrinsicValueType is an intrinsic's type as a value (ensureBuiltinFunc):
// its widest overload (a rest parameter, else the most parameters), every
// parameter boxed.
func (o frontOracle) intrinsicValueType(key string, fn *checker.Type, t Type) Type {
	if fn != nil && len(fn.Overloads) > 0 {
		w := fn.Overloads[len(fn.Overloads)-1]
		for _, ov := range fn.Overloads {
			if ov.HasRestParam() && !w.HasRestParam() || ov.HasRestParam() == w.HasRestParam() && len(ov.Params) > len(w.Params) {
				w = ov
			}
		}
		if wt, ok := o.r.of(w); ok && wt.IsFunc() {
			t = wt
		}
	}
	t.FuncParams = boxedParams(t)
	if key == "Date.UTC" {
		// It counts its arguments: one rest (ensureBuiltinFunc).
		t.FuncParams, t.FuncHasRest = []Type{ArrayOf(TypeAny)}, true
	}
	return t
}

// singleVarDecl is sym's one variable declaration.
// redeclaredVarHeld is what a `var` declared more than once holds when
// every declaration is unannotated with an initializer, those are its only
// stores, and every initializer is held as the same representation.
func (o frontOracle) redeclaredVarHeld(sym *binder.Symbol, id *ast.Identifier) (Type, bool) {
	if len(sym.Declarations) < 2 || len(o.b.StoresTo(sym)) != len(sym.Declarations) || o.jsWidenedVar(sym) {
		return Type{}, false
	}
	var held Type
	for i, d := range sym.Declarations {
		v, isV := d.Node.(*ast.VarDeclaration)
		if !isV || v.Kind != "var" || v.TypeAnnot != nil || v.Init == nil || v.Name != id.Name {
			return Type{}, false
		}
		it, ok := o.ExprType(v.Init)
		if !ok || it.IR == "" || it.IR == "void" || i > 0 && reprKey(it) != reprKey(held) {
			return Type{}, false
		}
		held = it
	}
	return held, true
}

func singleVarDecl(sym *binder.Symbol) (*ast.VarDeclaration, bool) {
	var v *ast.VarDeclaration
	for _, d := range sym.Declarations {
		switch n := d.Node.(type) {
		case *ast.TypeAliasDeclaration, *ast.InterfaceDeclaration:
			continue // a type merged with the value (`type P = …; const P = …`)
		case *ast.VarDeclaration:
			if v != nil {
				return nil, false
			}
			v = n
		default:
			return nil, false
		}
	}
	return v, v != nil
}

// jsWidenedVar reports a -compat=js module var declared more than once,
// untyped, with initializers of different kinds: codegen holds it as one
// any box (crossTypeWidenedBindings).
func (o frontOracle) jsWidenedVar(sym *binder.Symbol) bool {
	if o.r.js == nil || !o.r.js() || len(sym.Declarations) < 2 || sym.Scope == nil || sym.Scope.Kind != binder.ModuleScope {
		return false
	}
	kind := ""
	for _, d := range sym.Declarations {
		v, ok := d.Node.(*ast.VarDeclaration)
		if !ok || v.TypeAnnot != nil {
			return false
		}
		if v.Init == nil {
			continue
		}
		t := o.c.TypeOf(v.Init)
		if o.c.Unanswered(t) {
			return false
		}
		r, ok := o.r.of(t)
		if !ok {
			return false
		}
		k := scalarTypeKind(r)
		if k == "" {
			k = reprKey(r)
		}
		switch {
		case kind == "":
			kind = k
		case kind != k:
			return true
		}
	}
	return false
}

// findArrayPatternElem is the leaf of an array pattern (nested array
// patterns included) that binds name, or nil.
// directArrayPatternElem reports whether el is one of elems itself, not
// nested in a sub-pattern.
func directArrayPatternElem(elems []ast.ArrayPatternElem, el *ast.ArrayPatternElem) bool {
	for i := range elems {
		if &elems[i] == el {
			return true
		}
	}
	return false
}

func findArrayPatternElem(elems []ast.ArrayPatternElem, name string) *ast.ArrayPatternElem {
	for i := range elems {
		if elems[i].Name == name {
			return &elems[i]
		}
		if el := findArrayPatternElem(elems[i].SubArray, name); el != nil {
			return el
		}
		if el := findArrayPatternElemIn(elems[i].SubObject, name); el != nil {
			return el
		}
	}
	return nil
}

// findArrayPatternElemIn is the array-pattern leaf binding name nested in
// an object pattern's properties, or nil.
func findArrayPatternElemIn(props []ast.DestructProp, name string) *ast.ArrayPatternElem {
	for i := range props {
		if el := findArrayPatternElem(props[i].SubArray, name); el != nil {
			return el
		}
		if el := findArrayPatternElemIn(props[i].SubObject, name); el != nil {
			return el
		}
	}
	return nil
}

// localClassValue reports a parameter or local holding a user class's
// value (`c: typeof C`): codegen reads its statics typed, through the
// class it names.
func (o frontOracle) localClassValue(obj ast.Expression) bool {
	id, isID := obj.(*ast.Identifier)
	if !isID {
		return false
	}
	// A static member's `this`, rewritten to its class's name
	// (staticThisToClass): the class itself.
	if o.b.Module != nil {
		if prog, isProg := o.b.Module.Node.(*ast.Program); isProg && prog.StaticThis[id] != nil {
			return true
		}
	}
	sym, _ := o.b.Resolve(id)
	if sym != nil && len(sym.Declarations) > 0 {
		if _, isClass := sym.Declarations[0].Node.(*ast.ClassDeclaration); isClass {
			return true // the class's own name: its statics, typed
		}
	}
	if sym == nil || sym.Scope == nil || sym.Scope.Kind == binder.ModuleScope {
		return false
	}
	t := o.c.TypeOf(id)
	return !o.c.Unanswered(t) && t.Flags&checker.Object != 0 && t.ClassOf != nil
}

// evolvingBoxed reports an unannotated binding initialized with `[]` whose
// later stores mix element kinds: codegen holds it as a boxed-element array
// (inferEmptyArrayElemTypes), the checker as an array of their union.
func (o frontOracle) evolvingBoxed(v *ast.VarDeclaration) bool {
	if o.r.filledBoxed != nil && o.r.filledBoxed(v) {
		return true // a js-lane literal another element kind is stored into
	}
	lit, ok := v.Init.(*ast.ArrayLiteral)
	if !ok || len(lit.Elements) != 0 || v.TypeAnnot != nil {
		return false
	}
	t := o.c.ModuleVarType(v)
	if t == nil || o.c.Unanswered(t) || t.Flags&checker.Object == 0 || t.Kind != checker.Array || t.Elem.Flags&checker.Union == 0 {
		return false
	}
	et, ok := o.r.of(t.Elem)
	return ok && et.IsDynamic() && len(et.UnionMembers) > 1
}

// elementCopyingMethod are the array methods whose result holds the
// receiver's own elements.
var elementCopyingMethod = map[string]bool{
	"filter": true, "slice": true, "concat": true, "reverse": true, "sort": true,
	"toSorted": true, "toReversed": true, "toSpliced": true, "splice": true, "with": true,
}

// boxedElementParam is the type of an unannotated first parameter of a
// callback passed to an array method on a boxed-element array: its boxes;
// on an allSettled result, its settlement records.
func (o frontOracle) boxedElementParam(id *ast.Identifier) (Type, bool) {
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return Type{}, false
	}
	fn := sym.Declarations[0].Node
	var params []ast.Param
	switch f := fn.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	default:
		return Type{}, false
	}
	if len(params) == 0 || params[0].Name != id.Name || params[0].Type != nil {
		return Type{}, false
	}
	call, ok := o.c.ParentOf(fn).(*ast.CallExpression)
	if !ok || len(call.Args) == 0 || call.Args[0] != fn {
		return Type{}, false
	}
	m, ok := call.Callee.(*ast.MemberExpression)
	if !ok {
		return Type{}, false
	}
	rt, ok := o.ExprType(m.Object)
	if ok && o.boxedArrayCall(call) {
		return TypeAny, true // the box's method passes its elements boxed
	}
	// An allSettled result's element: the checker gives its
	// `PromiseSettledResult` union no layout.
	if ok && rt.IsArray() && rt.ElemType != nil && isSettlementType(*rt.ElemType) && elementCallback[m.Property] {
		return *rt.ElemType, true
	}
	if !ok || !rt.IsArray() || rt.IsTypedArray() || rt.ElemType == nil || reprKey(*rt.ElemType) != reprKey(TypeAny) {
		return Type{}, false
	}
	if ct := o.c.TypeOf(m.Object); o.c.Unanswered(ct) || ct.Flags&checker.Object == 0 || ct.Kind != checker.Array || ct.Elem.Flags&checker.Any != 0 {
		return Type{}, false
	}
	return TypeAny, true
}

// boxedArrayCall reports an element-callback method (`map`, `filter`, …)
// called on an array codegen holds boxed (an `any`-returning
// implementation's result) whose elements are typed arrays: a dynamic
// call on the box, which passes the callback boxed elements and returns a
// box.
func (o frontOracle) boxedArrayCall(call *ast.CallExpression) bool {
	m, ok := call.Callee.(*ast.MemberExpression)
	if !ok || m.Optional || !elementCallback[m.Property] {
		return false
	}
	rt, ok := o.ExprType(m.Object)
	if !ok || !isUnconstrainedDynamic(rt) {
		return false
	}
	ct := o.c.TypeOf(m.Object)
	if o.c.Unanswered(ct) || ct.Flags&checker.Object == 0 || ct.Kind != checker.Array || ct.Elem.Flags&checker.Any != 0 {
		return false
	}
	et, ok := o.r.of(ct.Elem)
	return ok && et.IsTypedArray() // a typed-array element is held boxed
}

// elementCallback names the array methods whose callback's first parameter
// is an element.
// evolvingElemResult is what an element callback method makes of an
// evolving JavaScript array: true for the receiver's own kind (filter),
// false for an `any[]` (map, flatMap).
var evolvingElemResult = map[string]bool{"map": false, "flatMap": false, "filter": true}

var elementCallback = map[string]bool{
	"map": true, "filter": true, "forEach": true, "some": true, "every": true, "find": true,
	"findIndex": true, "findLast": true, "findLastIndex": true, "flatMap": true,
}

// computedKeyObject mirrors inferDynamicObjectType.
func (o frontOracle) computedKeyObject(ol *ast.ObjectLiteral) (Type, bool) {
	var valTy *Type
	for _, p := range ol.Properties {
		if _, spread := p.Value.(*ast.SpreadElement); spread || p.AccessorKind != "" {
			return TypeAny, true
		}
		t, ok := o.ExprType(p.Value)
		if !ok {
			return Type{}, false
		}
		if t.IsDynamic() || t.IsFunc() || t.IR == "" {
			return TypeAny, true
		}
		if valTy == nil {
			valTy = &t
		} else if reprKey(*valTy) != reprKey(t) {
			return TypeAny, true
		}
	}
	if valTy == nil {
		return TypeAny, true
	}
	return dictViewType(*valTy), true
}

// jsWidenedReduceCallback is the callback of a `reduce`/`reduceRight` call
// with an initial value whose block body can fall off the end.
func jsWidenedReduceCallback(call *ast.CallExpression) ast.Node {
	m, ok := call.Callee.(*ast.MemberExpression)
	if !ok || (m.Property != "reduce" && m.Property != "reduceRight") || len(call.Args) != 2 {
		return nil
	}
	var body *ast.BlockStatement
	switch f := call.Args[0].(type) {
	case *ast.ArrowFunction:
		body = f.Block
	case *ast.FunctionExpression:
		body = f.Body
	}
	if body == nil || !(blockMayCompleteNormally(body) || blockHasBareReturn(body)) {
		return nil
	}
	return call.Args[0]
}

// thisReturningArrayMethod names the Array methods that return their
// receiver.
var thisReturningArrayMethod = map[string]bool{"sort": true, "reverse": true, "fill": true, "copyWithin": true}

// overloadsAgree reports a call whose callee's overloads all return one
// representation: whichever the run-time value picks, the result is held
// so (`zlib.gunzipSync(buf)`'s Buffer).
func (o frontOracle) overloadsAgree(call *ast.CallExpression) bool {
	ft := o.c.TypeOf(call.Callee)
	if o.c.Unanswered(ft) || ft.Flags&checker.Object == 0 {
		return false
	}
	sigs := ft.Overloads
	if len(sigs) == 0 {
		sigs = ft.Calls
	}
	if len(sigs) < 2 {
		return false
	}
	key := ""
	for _, sig := range sigs {
		if sig.Ret() == nil || len(sig.TypeParams) > 0 {
			return false
		}
		r, ok := o.r.of(sig.Ret())
		if !ok {
			return false
		}
		if key == "" {
			key = reprKey(r)
		} else if reprKey(r) != key {
			return false
		}
	}
	return true
}

// declaredParam is the parameter named name a function declares sym as,
// or nil.
func declaredParam(sym *binder.Symbol, name string) *ast.Param {
	if len(sym.Declarations) != 1 {
		return nil
	}
	var params []ast.Param
	switch f := sym.Declarations[0].Node.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	case *ast.FunctionDeclaration:
		params = f.Params
	default:
		return nil
	}
	for i := range params {
		if params[i].Name == name {
			return &params[i]
		}
	}
	return nil
}

// globalThisGlobal reports `globalThis.X` of a library global X, which
// codegen reads as the bare X (unwrapGlobalThis).
func (o frontOracle) globalThisGlobal(e ast.Expression) bool {
	g, ok := e.(*ast.MemberExpression)
	if !ok || g.Optional {
		return false
	}
	id, isID := g.Object.(*ast.Identifier)
	if !isID || id.Name != "globalThis" {
		return false
	}
	if sym, _ := o.b.Resolve(id); sym != nil && !o.r.libGlobal(sym) {
		return false
	}
	gs := o.b.Globals.Symbols.Get(g.Property)
	return gs != nil && o.r.libGlobal(gs)
}

// literalParamAnnotation is the annotation of the parameter id reads when
// it names one string literal, possibly nullable.
func (o frontOracle) literalParamAnnotation(id *ast.Identifier) (string, bool) {
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 || o.b.StoredElsewhere(sym) {
		return "", false
	}
	var params []ast.Param
	switch fn := sym.Declarations[0].Node.(type) {
	case *ast.FunctionDeclaration:
		params = fn.Params
	case *ast.ArrowFunction:
		params = fn.Params
	case *ast.FunctionExpression:
		params = fn.Params
	default:
		return "", false
	}
	for _, p := range params {
		if p.Name != id.Name || p.Type == nil {
			continue
		}
		ms, hasNull, hasUndef := ast.NonNullishMembers(p.Type)
		if lt, ok := ms0(ms).(*ast.LiteralType); ok && len(ms) == 1 && lt.Kind == "string" && (hasNull || hasUndef || p.Optional) {
			return lt.Value, true
		}
	}
	return "", false
}

// thisClassName is the name of the class whose polymorphic `this` the
// type node n (a member's annotation) spells, "" when none encloses it.
func (o frontOracle) thisClassName(n ast.TypeNode) string {
	for sc := o.b.AnnotationScope(n); sc != nil; sc = sc.Parent {
		if sc.Kind != binder.FunctionScope || sc.Node == nil {
			continue
		}
		for p := o.c.ParentOf(sc.Node); p != nil; p = o.c.ParentOf(p) {
			switch x := p.(type) {
			case *ast.ClassDeclaration:
				return x.Name
			case *ast.ClassExpression:
				return x.Decl.Name
			}
		}
		return ""
	}
	return ""
}

// isAnyArrayNode reports `any[]` (or `Array<any>`).
func isAnyArrayNode(n ast.TypeNode) bool {
	el := ast.ArrayElemNode(n)
	return el != nil && ast.IsKeywordNode(el, "any")
}

// ms0 is ms's first node, nil when there is none.
func ms0(ms []ast.TypeNode) ast.TypeNode {
	if len(ms) == 0 {
		return nil
	}
	return ast.Unparen(ms[0])
}

// staticOfClassExpr reports a `this` read in a static method of a class
// expression.
func (o frontOracle) staticOfClassExpr(th *ast.ThisExpression) bool {
	for n := o.c.ParentOf(th); n != nil; n = o.c.ParentOf(n) {
		switch x := n.(type) {
		case *ast.ArrowFunction:
			continue
		case *ast.FunctionDeclaration:
			if !x.IsStatic {
				return false
			}
			cd, isCD := o.c.ParentOf(x).(*ast.ClassDeclaration)
			if !isCD {
				return false
			}
			_, isCE := o.c.ParentOf(cd).(*ast.ClassExpression)
			return isCE || strings.Contains(cd.Name, "__kml_cx")
		case *ast.FunctionExpression, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		}
	}
	return false
}

// nonEmptyLiteral reports a string or array literal with an element.
func nonEmptyLiteral(e ast.Expression) bool {
	switch x := e.(type) {
	case *ast.StringLiteral:
		return x.Value != ""
	case *ast.ArrayLiteral:
		return len(x.Elements) > 0
	}
	return false
}

// within reports n inside node anc.
func (o frontOracle) within(n, anc ast.Node) bool {
	for p := n; p != nil; p = o.c.ParentOf(p) {
		if p == anc {
			return true
		}
	}
	return false
}

// restPatternLength reports id bound by a rest parameter's object
// pattern to the rest array's own `length`.
func (o frontOracle) restPatternLength(id *ast.Identifier) bool {
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return false
	}
	var params []ast.Param
	switch f := sym.Declarations[0].Node.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	case *ast.FunctionDeclaration:
		params = f.Params
	}
	if len(params) == 0 {
		return false
	}
	p := params[len(params)-1]
	if !p.Rest || p.Type != nil {
		return false
	}
	for _, d := range p.ObjectPattern {
		if d.Key == "length" && d.Local == id.Name && d.KeyExpr == nil && d.Default == nil && d.SubArray == nil && d.SubObject == nil {
			return true
		}
	}
	return false
}

// functionValueProp reports a read of a property a program's function
// value carries by assignment (`fn.p = v`), not one of Function's.
func (o frontOracle) functionValueProp(m *ast.MemberExpression) bool {
	rt := o.c.NonNullable(o.c.TypeOf(m.Object))
	if o.c.Unanswered(rt) || rt.Flags&checker.Object == 0 || o.c.Unanswered(o.c.TypeOf(m)) {
		return false
	}
	switch {
	case rt.Kind == checker.Function:
	case rt.Kind == checker.Anonymous && len(rt.Calls) > 0:
	default:
		return false
	}
	// A member Function (or Object, under it) declares is not an expando.
	for _, own := range [...]string{"Function", "Object"} {
		if t := o.c.GlobalInterface(own); t != nil && t.Prop(m.Property) != nil {
			return false
		}
	}
	return true
}

// hoistSkippable mirrors hoistedVarMaySkip: a `var` in a nested block (an
// `if`/loop/`switch`/`try` body), not a `for` init, or after a terminator in
// its function's own statement list.
func (o frontOracle) hoistSkippable(v *ast.VarDeclaration) bool {
	if v.Kind != "var" {
		return false
	}
	var node ast.Node = v
	parent := o.c.ParentOf(v)
	if l, isList := parent.(*ast.VarDeclarationList); isList {
		node, parent = l, o.c.ParentOf(l)
	}
	for {
		switch p := parent.(type) {
		case *ast.Program:
			return false
		case *ast.ForStatement:
			return p.Init != node
		case *ast.BlockStatement:
			outer := o.c.ParentOf(p)
			switch outer.(type) {
			case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.DoWhileStatement:
				// A body that runs at least once reaches the declaration
				// unless a jump before it leaves.
				for _, st := range p.Body {
					if st == node {
						break
					}
					switch st.(type) {
					case *ast.ReturnStatement, *ast.BreakStatement, *ast.ContinueStatement, *ast.ThrowStatement:
						return true
					}
				}
				if _, isDo := outer.(*ast.DoWhileStatement); isDo {
					// … as far as the `do` statement itself is reached.
					node, parent = outer, o.c.ParentOf(outer)
					if l, isL := parent.(*ast.LabeledStatement); isL {
						node, parent = l, o.c.ParentOf(l)
					}
					continue
				}
				return false
			}
			return true
		case nil:
			return false
		}
		return true
	}
}

// inferredBodyReturn is what an unannotated, plain (not async, not
// generator) function's block body returns, as ReturnType answers it.
func (o frontOracle) inferredBodyReturn(fn ast.Node) (Type, bool) {
	// A recursive function's own call inside its body is the body's
	// result again: no answer from that side.
	if o.inBody == nil || o.inBody[fn] {
		return Type{}, false
	}
	o.inBody[fn] = true
	defer delete(o.inBody, fn)
	var body *ast.BlockStatement
	switch f := fn.(type) {
	case *ast.ArrowFunction:
		if f.RetType != nil || f.IsAsync {
			return Type{}, false
		}
		if f.Block == nil && f.Body != nil {
			return o.ExprType(f.Body)
		}
		body = f.Block
	case *ast.FunctionExpression:
		if f.RetType != nil || f.IsAsync || f.IsGenerator {
			return Type{}, false
		}
		body = f.Body
	case *ast.FunctionDeclaration:
		if f.ReturnType != nil || f.IsAsync || f.IsGenerator {
			return Type{}, false
		}
		body = f.Body
	}
	if body == nil {
		return Type{}, false
	}
	rt, ok, hasValue := o.ReturnType(body)
	return rt, ok && hasValue
}

// neverLeafToNumber replaces an array type's innermost `never` element
// with the number codegen gives an empty array literal.
func neverLeafToNumber(t Type) (Type, bool) {
	if !t.IsArray() || t.ElemType == nil {
		return t, false
	}
	el := *t.ElemType
	if el.IsNever() {
		el = TypeF64
	} else if ne, changed := neverLeafToNumber(el); changed {
		el = ne
	} else {
		return t, false
	}
	t.ElemType = &el
	return t, true
}

// arithOps are the arithmetic binary operators.
var arithOps = map[string]bool{"+": true, "-": true, "*": true, "/": true, "%": true, "**": true}

// dynamicBinary is typeOfExpr's answer for an arithmetic operator with
// an operand held as a box.
func (o frontOracle) dynamicBinary(b *ast.BinaryExpression) (Type, bool) {
	if !arithOps[b.Op] {
		return Type{}, false
	}
	lt, lok := o.ExprType(b.Left)
	rt, rok := o.ExprType(b.Right)
	if !lok || !rok || !isUnconstrainedDynamic(lt) && !isUnconstrainedDynamic(rt) {
		return Type{}, false
	}
	if b.Op == "+" {
		if isStringTy(lt) || isStringTy(rt) {
			return Type{}, false
		}
		return TypeAny, true
	}
	if anyArithIsNumber(b.Op, lt, rt) {
		return Type{}, false
	}
	return TypeAny, true
}

// findKin are the array methods whose callback reads an element the
// callback shrank the array below as undefined.
var findKin = map[string]bool{"find": true, "findLast": true, "findIndex": true, "findLastIndex": true}

// findElemParam is the `T | undefined` a find-kin callback's unannotated
// first parameter takes over an array codegen walks through its live header
// (hofLiveHeader).
func (o frontOracle) findElemParam(fn *ast.ArrowFunction) (Type, bool) {
	if len(fn.Params) == 0 || fn.Params[0].Type != nil || fn.Params[0].Rest || fn.Params[0].ArrayPattern != nil || fn.Params[0].ObjectPattern != nil {
		return Type{}, false
	}
	call, ok := o.c.ParentOf(fn).(*ast.CallExpression)
	if !ok || len(call.Args) == 0 || call.Args[0] != ast.Expression(fn) {
		return Type{}, false
	}
	m, ok := call.Callee.(*ast.MemberExpression)
	if !ok || m.Optional || !findKin[m.Property] {
		return Type{}, false
	}
	rt, ok := o.ExprType(m.Object)
	if !ok || !rt.IsArray() || rt.ElemType == nil || rt.IsTypedArray() || rt.IsFlatArray() || rt.ExecArray || rt.IsDynamic() || rt.Nullable() {
		return Type{}, false
	}
	if t := undefinedableElem(*rt.ElemType); isNullableScalar(t) {
		return t, true
	}
	return Type{}, false
}

// thenValueBox is the box a `p.then(fn)` callback's unannotated value
// parameter holds when codegen holds p's value as a box, or the function
// it holds when that is a literal counting its `arguments` (the synthetic
// rest the checker's type lacks).
func (o frontOracle) thenValueBox(fn *ast.ArrowFunction) (Type, bool) {
	if len(fn.Params) == 0 || fn.Params[0].Type != nil || fn.Params[0].Rest || fn.Params[0].ArrayPattern != nil || fn.Params[0].ObjectPattern != nil {
		return Type{}, false
	}
	call, ok := o.c.ParentOf(fn).(*ast.CallExpression)
	if !ok || len(call.Args) == 0 || call.Args[0] != ast.Expression(fn) {
		return Type{}, false
	}
	m, ok := call.Callee.(*ast.MemberExpression)
	if !ok || m.Property != "then" {
		return Type{}, false
	}
	rt, ok := o.ExprType(m.Object)
	if !ok || !rt.IsPromise() || rt.PromiseType == nil {
		return Type{}, false
	}
	pt := *rt.PromiseType
	// An allSettled array: the checker gives its `PromiseSettledResult`
	// union no layout.
	settled := pt.IsArray() && pt.ElemType != nil && isSettlementType(*pt.ElemType)
	if !isUnconstrainedDynamic(pt) && !(pt.IsFunc() && pt.FuncCountsArgs) && !settled {
		return Type{}, false
	}
	return pt, true
}

// asyncBodyReturn is what an unannotated async (not generator) function's
// block body returns, as ReturnType answers it.
func (o frontOracle) asyncBodyReturn(fn ast.Node) (Type, bool) {
	if o.inBody == nil || o.inBody[fn] {
		return Type{}, false
	}
	var body *ast.BlockStatement
	switch f := fn.(type) {
	case *ast.ArrowFunction:
		if f.RetType == nil && f.IsAsync && f.Block == nil && f.Body != nil {
			o.inBody[fn] = true
			defer delete(o.inBody, fn)
			return o.ExprType(f.Body)
		}
		if f.RetType == nil && f.IsAsync {
			body = f.Block
		}
	case *ast.FunctionExpression:
		if f.RetType == nil && f.IsAsync && !f.IsGenerator {
			body = f.Body
		}
	case *ast.FunctionDeclaration:
		if f.ReturnType == nil && f.IsAsync && !f.IsGenerator {
			body = f.Body
		}
	}
	if body == nil {
		return Type{}, false
	}
	o.inBody[fn] = true
	defer delete(o.inBody, fn)
	rt, ok, hasValue := o.ReturnType(body)
	return rt, ok && hasValue
}

// hasAccessor reports an object literal declaring a getter or setter, made
// as its synthetic class: a computed key makes it a dynamic object instead
// (inferObjectType).
func hasAccessor(lit *ast.ObjectLiteral) bool {
	if lit.HasComputedKey() || objLitHasSpread(lit) {
		return false
	}
	for _, p := range lit.Properties {
		if p.AccessorKind != "" {
			return true
		}
	}
	return false
}

// libInterfaceClass is the class annotationType gives a lib-declared
// interface: a global module's class (globalLinks), or the class its
// declaration's tag names (`NodeJS.Timeout`).
func (e *Emitter) libInterfaceClass(sym *binder.Symbol, nodeJS bool, args []Type) (Type, bool) {
	var m string
	if nodeJS {
		m = e.taggedLibClass(sym)
	} else if m = e.globalLinks[sym.Name]; m == "" {
		m = e.taggedLibClass(sym)
	}
	// A generic one (StreamAsyncIterator<R>) is its instantiation.
	if cd := e.genericClasses[m]; cd != nil && len(cd.TypeParams) == len(args) {
		subs := make(map[string]Type, len(args))
		for i, n := range cd.TypeParams {
			subs[n] = args[i]
		}
		if t, err := e.genericClassInstanceType(cd, subs); err == nil {
			return t, true
		}
		return Type{}, false
	}
	info, isClass := e.classes[m]
	if m == "" || !isClass {
		return Type{}, false
	}
	if info.Ty.IR != "" {
		return info.Ty, true // the class's instance
	}
	return e.namedType(m), true
}

// bareContainerType is the type codegen's pre-pass gives an unannotated
// binding of a bare `[]` (typed by its pushes, or codegen's default) or of
// a bare collection (bareCollectionType).
func (e *Emitter) bareContainerType(v *ast.VarDeclaration) (Type, bool) {
	// Answered by the declaring scope's pre-pass, even from inside a
	// nested function.
	if sc, ok := e.emptyScopes[v]; ok {
		arr, kv := e.emptyArrayElems, e.emptyMapKV
		e.emptyArrayElems, e.emptyMapKV = sc.arr, sc.kv
		defer func() { e.emptyArrayElems, e.emptyMapKV = arr, kv }()
	}
	if lit, ok := v.Init.(*ast.ArrayLiteral); ok && v.TypeAnnot == nil && len(lit.Elements) == 0 {
		if ns := e.stmtNS[v]; ns != "" && e.curNamespace == "" {
			e.curNamespace = ns
			defer func() { e.curNamespace = "" }()
		}
		if ty := e.declaredArrayType(v.Name, lit); ty.IsArray() && ty.ElemType != nil {
			return ty, true
		}
		return Type{}, false
	}
	return e.bareCollectionType(v)
}

// bareCollectionType is the type codegen's pre-pass gives an unannotated
// binding of a bare `new Map()`/`new Set()` whose calls the pre-pass saw.
func (e *Emitter) bareCollectionType(v *ast.VarDeclaration) (Type, bool) {
	if v.TypeAnnot != nil {
		return Type{}, false
	}
	switch init := v.Init.(type) {
	case *ast.NewMapExpression:
		if init.KeyType != nil || init.ValType != nil || init.Init != nil {
			return Type{}, false
		}
	case *ast.NewSetExpression:
		if init.ElemType != nil || init.Init != nil {
			return Type{}, false
		}
	case *ast.NewWeakSetExpression:
		// `new WeakSet()`, `new WeakSet(null)`: seeded from nothing, in the
		// key type codegen infers for the construction.
		if _, isNull := init.Init.(*ast.NullLiteral); (isNull || init.Init == nil) && init.ElemType == nil {
			return WeakSetType(e.weakSetElem(init)), true
		}
		return Type{}, false
	case *ast.NewWeakMapExpression:
		if _, isNull := init.Init.(*ast.NullLiteral); (isNull || init.Init == nil) && init.KeyType == nil && init.ValType == nil {
			return WeakMapType(e.weakMapKV(init, nil)), true
		}
		return Type{}, false
	default:
		return Type{}, false
	}
	// With no call seen, emitMapVarDecl/emitSetVarDecl's defaults.
	kv := e.emptyMapKV[v.Name]
	// Its widened key and value types from the pre-pass, as
	// emitMapVarDecl/emitSetVarDecl create its runtime family (TDD-00211,
	// ADR-01061).
	if _, isSet := v.Init.(*ast.NewSetExpression); isSet {
		elemTy := TypePtr
		if kv.keyKnown {
			elemTy = kv.key
		}
		return SetType(elemTy), true
	}
	keyTy, valTy := TypePtr, TypeI64
	if kv.keyKnown {
		keyTy = kv.key
	}
	if kv.valKnown {
		valTy = kv.val
	}
	return MapType(keyTy, forceAnyMapVal(keyTy, valTy)), true
}

// funcValueProp is the box a function value's property other than its own
// reads as (typeOfExpr's function-value rule).
func (e *Emitter) funcValueProp(m *ast.MemberExpression) (Type, bool) {
	if st := e.builtMemberType(m); st.IsDynamic() {
		return st, true
	}
	return Type{}, false
}

// objLitClassRepr is the synthetic class an accessor-bearing object literal
// is lowered to.
func (e *Emitter) objLitClassRepr(lit *ast.ObjectLiteral) (Type, bool) {
	if info, ok := e.classes[e.ensureObjLitClass(lit)]; ok {
		return info.Ty, true
	}
	return Type{}, false
}

// checkerFunction reports an expression the checker types as a function.
func (o frontOracle) checkerFunction(e ast.Expression) bool {
	t := o.c.TypeOf(e)
	return t != nil && t.Flags&checker.Object != 0 && t.Kind == checker.Function
}

// anyImplCallbackParam reports an unannotated parameter of a function
// literal passed to an overloaded function whose implementation declares
// that argument's parameter `any` (the literal is boxed, its parameters
// boxes; a class instance parameter keeps its class), or takes it as a
// listener of boxes (`(...args: any[]) => void`).
func (o frontOracle) anyImplCallbackParam(id *ast.Identifier) bool {
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return false
	}
	fn := sym.Declarations[0].Node
	var params []ast.Param
	switch f := fn.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	default:
		return false
	}
	pidx := -1
	for i, p := range params {
		// A rest parameter collects the boxes into an array, the
		// checker's `any[]`.
		if p.Name == id.Name && p.Type == nil && !p.Rest {
			pidx = i
		}
	}
	if pidx < 0 {
		return false
	}
	call, ok := o.c.ParentOf(fn).(*ast.CallExpression)
	if !ok {
		return false
	}
	at := -1
	for i, a := range call.Args {
		if _, spread := a.(*ast.SpreadElement); spread {
			return false
		}
		if ast.Node(a) == fn {
			at = i
		}
	}
	if at < 0 {
		return false
	}
	var decl ast.Node
	switch c := call.Callee.(type) {
	case *ast.Identifier:
		if cs, _ := o.b.Resolve(c); cs != nil && len(cs.Declarations) > 0 {
			decl = cs.Declarations[len(cs.Declarations)-1].Node
		}
	case *ast.MemberExpression:
		decl = o.c.MemberDecl(c)
	}
	impl, ok := decl.(*ast.FunctionDeclaration)
	if !ok || len(impl.Overloads) == 0 {
		return false
	}
	if at >= len(impl.Params) {
		last := len(impl.Params) - 1
		if last < 0 || !impl.Params[last].Rest {
			return false
		}
		at = last
	}
	pt := impl.Params[at].Type
	var ptn ast.TypeNode
	if pt != nil {
		ptn = pt
	}
	if ft, isFn := ast.NonNullish(ptn).(*ast.FunctionType); ptn != nil && isFn && !impl.Params[at].Rest {
		// A listener the implementation takes as `(...args: any[]) => void`
		// is called with boxes (`socket.on('data', (d) => …)`).
		if n := len(ft.Parameters); n > 0 {
			k := pidx
			if last := ft.Parameters[n-1]; last.Rest && k >= n-1 {
				return isAnyArrayNode(last.Type)
			}
			return k < n && (ft.Parameters[k].Type == nil || ast.IsKeywordNode(ft.Parameters[k].Type, "any"))
		}
		return false
	}
	if ptn == nil || !(ast.IsKeywordNode(ptn, "any") || impl.Params[at].Rest && isAnyArrayNode(ptn)) {
		return false
	}
	// The implementation builds plain records (`dns`'s NaptrRecord): an
	// interface- or literal-typed parameter receives a box.
	if recordShaped(o.c.NonNullable(o.c.TypeOf(id))) {
		return true
	}
	if pt := o.c.NonNullable(o.c.TypeOf(id)); !o.c.Unanswered(pt) && pt.Flags&checker.Object != 0 && pt.Kind == checker.Instance {
		return false
	}
	return true
}

// classMethodDecl is the method declaration class className runs for name.
func (e *Emitter) classMethodDecl(className, name string) *ast.FunctionDeclaration {
	if info, ok := e.classes[className]; ok {
		return info.Methods[name]
	}
	return nil
}

// heldOperand is what an operand is held as: its answer, or for a name the
// checker leaves unanswered, its binding in the scope being emitted.
func (o frontOracle) heldOperand(e ast.Expression) (Type, bool) {
	if t, ok := o.ExprType(e); ok {
		return t, true
	}
	if id, isID := e.(*ast.Identifier); isID && o.r.heldIdent != nil {
		return o.r.heldIdent(id)
	}
	return Type{}, false
}

// typeNodeHasTuple reports a tuple type anywhere in n.
func typeNodeHasTuple(n ast.TypeNode) bool {
	found := false
	if n == nil {
		return false
	}
	ast.Inspect(n, func(x ast.Node) bool {
		if _, ok := x.(*ast.TupleType); ok {
			found = true
		}
		return !found
	})
	return found
}

// literalKey is the property name a literal index spells (`o["k"]`,
// `o[1]`).
func literalKey(x ast.Expression) (string, bool) {
	switch k := x.(type) {
	case *ast.StringLiteral:
		return k.Value, true
	case *ast.NumberLiteral:
		// A plain decimal integer spells its own key; any other form
		// (`0x1f`, `1e3`, `1.50`) is left to the checker.
		if !k.IsBigInt && k.Value != "" && strings.Trim(k.Value, "0123456789") == "" && (k.Value == "0" || k.Value[0] != '0') {
			return k.Value, true
		}
	}
	return "", false
}

// mayBeUndefined reports an expression the checker types as possibly
// undefined (or cannot type).
func (o frontOracle) mayBeUndefined(e ast.Expression) bool {
	t := o.c.TypeOf(e)
	if o.c.Unanswered(t) {
		return true
	}
	if t.Flags&checker.Union != 0 {
		for _, m := range t.Types {
			if m.Flags&(checker.Undefined|checker.Void|checker.Any|checker.Unknown) != 0 {
				return true
			}
		}
		return false
	}
	return t.Flags&(checker.Undefined|checker.Void|checker.Any|checker.Unknown) != 0
}

// anyArgument reports a call passing an argument typed `any`.
func (o frontOracle) anyArgument(call *ast.CallExpression) bool {
	for _, a := range call.Args {
		if t := o.c.TypeOf(a); !o.c.Unanswered(t) && t.Flags&checker.Any != 0 {
			return true
		}
	}
	return false
}

// objectResult reports a call the checker types as an array or object (an
// overload's): codegen holds an `any` implementation's result boxed, and
// only a primitive one is narrowed back (checkerPrimitive).
func (o frontOracle) objectResult(call *ast.CallExpression) bool {
	t := o.c.TypeOf(call)
	if o.c.Unanswered(t) || t.Flags&checker.Object == 0 {
		return false
	}
	// A byte buffer the runtime hands back as one (`cipher.update`'s
	// Buffer) keeps its layout.
	r, ok := o.r.of(t)
	return !ok || !(r.IsTypedArray() || r.IsBuffer() || r.IsArrayBuffer())
}

// recordShaped reports an interface or object-literal type, or an
// array of one: what a library implementation hands a callback as a box.
func recordShaped(t *checker.Type) bool {
	if t == nil || t.Flags&checker.Object == 0 {
		return false
	}
	switch t.Kind {
	case checker.Array:
		return recordShaped(t.Elem)
	case checker.Interface:
		return true
	case checker.Anonymous:
		return len(t.Calls) == 0 && len(t.Constructs) == 0
	}
	return false
}

// holdsThenableInstance reports an array literal with an element that is an
// instance of a program class declaring `then`.
func (o frontOracle) holdsThenableInstance(lit *ast.ArrayLiteral) bool {
	for _, el := range lit.Elements {
		t := o.c.TypeOf(el)
		if o.c.Unanswered(t) || t.Flags&checker.Object == 0 || t.Kind != checker.Instance || t.Symbol == nil {
			continue
		}
		for _, d := range t.Symbol.Declarations {
			if cd, ok := d.Node.(*ast.ClassDeclaration); ok {
				for _, m := range cd.Methods {
					if m.Name == "then" {
						return true
					}
				}
			}
		}
	}
	return false
}

// superExecutorParam is the index (0 resolve, 1 reject) of the executor
// parameter id names, where the executor is the argument of `super(…)` in a
// class extending Promise; -1 otherwise.
func (o frontOracle) superExecutorParam(id *ast.Identifier) int {
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return -1
	}
	fn := sym.Declarations[0].Node
	var params []ast.Param
	switch f := fn.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	default:
		return -1
	}
	call, ok := o.c.ParentOf(fn).(*ast.CallExpression)
	if !ok || len(call.Args) == 0 || ast.Node(call.Args[0]) != fn {
		return -1
	}
	if _, isSuper := call.Callee.(*ast.SuperExpression); !isSuper {
		return -1
	}
	found := false
	for n := ast.Node(call); n != nil; n = o.c.ParentOf(n) {
		if cd, isCD := n.(*ast.ClassDeclaration); isCD {
			if cd.BaseClass != "Promise" {
				return -1
			}
			found = true
			break
		}
	}
	if !found {
		return -1
	}
	for i, p := range params {
		if i < 2 && p.Name == id.Name {
			return i
		}
	}
	return -1
}

// checkerMembers is a union's members, or the type itself.
func checkerMembers(t *checker.Type) []*checker.Type {
	if t != nil && t.Flags&checker.Union != 0 {
		return t.Types
	}
	return []*checker.Type{t}
}

// extendsPromise reports an instance of a program class extending Promise.
func (o frontOracle) extendsPromise(t *checker.Type) bool {
	if t == nil || o.c.Unanswered(t) || t.Flags&checker.Object == 0 || t.Kind != checker.Instance || t.Symbol == nil {
		return false
	}
	for _, d := range t.Symbol.Declarations {
		if cd, ok := d.Node.(*ast.ClassDeclaration); ok {
			return cd.BaseClass == "Promise" || o.extendsPromise(t.Base)
		}
	}
	return false
}

// voidOverValue reports a call an overload types `void` whose
// implementation returns a value (`dns.lookup`'s request object): codegen
// holds what the implementation returns, boxed.
func (o frontOracle) voidOverValue(call *ast.CallExpression, impl *ast.FunctionDeclaration) bool {
	if impl.ReturnType == nil {
		return false
	}
	switch n := ast.Unparen(impl.ReturnType).(type) {
	case *ast.KeywordType:
		if n.Keyword == "void" || n.Keyword == "undefined" || n.Keyword == "never" {
			return false
		}
	case *ast.TypeReference, *ast.ArrayType:
	default:
		return false // a type spelled other than by name
	}
	t := o.c.TypeOf(call)
	return !o.c.Unanswered(t) && t.Flags&checker.Void != 0
}

// nullishCompared reports an identifier compared (`===`, `!==`, `==`, `!=`)
// against `null` or `undefined`.
func (o frontOracle) nullishCompared(id *ast.Identifier) bool {
	be, ok := o.c.ParentOf(id).(*ast.BinaryExpression)
	if !ok {
		return false
	}
	switch be.Op {
	case "===", "!==", "==", "!=":
	default:
		return false
	}
	other := be.Right
	if ast.Node(be.Right) == ast.Node(id) {
		other = be.Left
	}
	switch x := other.(type) {
	case *ast.NullLiteral:
		return true
	case *ast.Identifier:
		return x.Name == "undefined"
	}
	return false
}

// declaredNullable is the representation of the annotated type of the
// local id names, when that type admits null or undefined.
func (o frontOracle) declaredNullable(id *ast.Identifier) (Type, bool) {
	sym, _ := o.b.Resolve(id)
	if sym == nil {
		return Type{}, false
	}
	v, ok := singleVarDecl(sym)
	if !ok || v.TypeAnnot == nil {
		return Type{}, false
	}
	t, ok := o.AnnotationType(v.TypeAnnot)
	if !ok || !t.Nullable() {
		return Type{}, false
	}
	return t, true
}

// awaitedOperand is the operand of the `await` e is, directly or as the
// initializer of an unannotated variable e names; nil otherwise.
func (o frontOracle) awaitedOperand(e ast.Expression) ast.Expression {
	switch x := e.(type) {
	case *ast.AwaitExpression:
		return x.Argument
	case *ast.Identifier:
		sym, _ := o.b.Resolve(x)
		if sym == nil {
			return nil
		}
		if v, ok := singleVarDecl(sym); ok && v.TypeAnnot == nil {
			if aw, isAw := v.Init.(*ast.AwaitExpression); isAw {
				return aw.Argument
			}
		}
	}
	return nil
}

// stringMatch is the type of `str.match(re)` (emitStringMatch), of its
// non-null assertion, and of a const binding of either.
func (o frontOracle) stringMatch(e ast.Expression) (Type, bool) {
	switch x := e.(type) {
	case *ast.NonNullExpression:
		if t, ok := o.stringMatch(x.Arg); ok {
			return t.withoutNullable(), true
		}
	case *ast.Identifier:
		sym, _ := o.b.Resolve(x)
		if sym == nil {
			return Type{}, false
		}
		v, isV := singleVarDecl(sym)
		if !isV || v.Kind != "const" || v.TypeAnnot != nil || v.Init == nil {
			return Type{}, false
		}
		t, ok := o.stringMatch(v.Init)
		if ok && t.Nullable() {
			if ct := o.c.TypeOf(x); !o.c.Unanswered(ct) && ct.Flags&checker.Union == 0 {
				t = t.withoutNullable() // narrowed present
			}
		}
		return t, ok
	case *ast.CallExpression:
		if len(x.Args) != 1 {
			return Type{}, false
		}
		m, isM := x.Callee.(*ast.MemberExpression)
		if !isM || m.Property != "match" || m.Optional || x.Optional {
			return Type{}, false
		}
		if rt := o.c.NonNullable(o.c.TypeOf(m.Object)); !o.c.Unanswered(rt) && rt.Flags&checker.StringLike != 0 && rt.Flags&checker.Union == 0 {
			t := execArrayType()
			t.ExecMaybePlain = !matchIsExec(x.Args[0])
			return t, true
		}
	}
	return Type{}, false
}

// narrowedBoxLocal reports a dynamic entry's boxed local (not a parameter)
// the checker narrows to a string at e: codegen reads the box's payload.
func (o frontOracle) narrowedBoxLocal(e ast.Expression) bool {
	id, ok := e.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if _, isV := singleVarDecl(sym); sym == nil || !isV {
		return false
	}
	nt := o.c.TypeOf(id)
	return !o.c.Unanswered(nt) && nt.Flags&checker.Union == 0 && nt.Flags&(checker.String|checker.StringLiteral) != 0
}

// assignResult answers `Object.assign(target, …)`, which returns its
// target: a `[]` target takes the first array source's elements, and a
// static target its sources add nothing to stays that layout
// (emitObjectAssign, dynamicAssign).
func (o frontOracle) assignResult(call *ast.CallExpression) (Type, bool) {
	m, isM := call.Callee.(*ast.MemberExpression)
	if len(call.Args) < 2 || !isM || o.intrinsicKey(m) != "Object.assign" {
		return Type{}, false
	}
	if lit, isLit := call.Args[0].(*ast.ArrayLiteral); isLit && len(lit.Elements) == 0 {
		if st, ok := o.ExprType(call.Args[1]); ok && st.IsArray() {
			return st, true
		}
		return Type{}, false
	}
	tt, ok := o.ExprType(call.Args[0])
	if ok && tt.IsArray() {
		return tt, true
	}
	if _, isOL := call.Args[0].(*ast.ObjectLiteral); ok && !isOL && tt.IsObject() && !tt.IsDynamic() && o.sourcesFit(tt, call.Args[1:]) {
		return tt, true
	}
	return Type{}, false
}

// sourcesFit reports Object.assign sources that are static objects whose
// every property the target's layout has.
func (o frontOracle) sourcesFit(tt Type, srcs []ast.Expression) bool {
	for _, s := range srcs {
		if _, spread := s.(*ast.SpreadElement); spread {
			return false
		}
		st, ok := o.ExprType(s)
		if !ok || st.IsDynamic() || !st.IsObject() && !st.IsArray() {
			return false
		}
		if !st.IsObject() {
			continue
		}
		for _, f := range st.VisibleFields() {
			if _, _, ok := tt.FieldIndex(f.Name); !ok {
				return false
			}
		}
	}
	return true
}

// widenedUse reports e as a dynamic entry's widened parameter, or a
// member read, index or call rooted at one.
func (o frontOracle) widenedUse(e ast.Expression) bool {
	return widenedRooted(e, o.r.widenedParam)
}

// chainCopyType answers c, emission's copy of the optional chain link orig:
// orig's own answer while c still sits in an optional chain, else orig's
// non-optional type (the undefined its chain adds gone).
func (o frontOracle) chainCopyType(c, orig ast.Expression) (Type, bool) {
	// A link of the copy read off a receiver the copy holds as a box (its
	// now plain `tmp.f` a shadowable read, boxOverride): the dynamic
	// dispatch's, as codegen reads it.
	if m, isM := c.(*ast.MemberExpression); isM && !m.Optional && o.r.boxOverride != nil && o.r.funcProp != nil {
		if rt, ok := o.r.boxOverride(m.Object); ok && isUnconstrainedDynamic(rt) {
			if st, ok := o.r.funcProp(m); ok {
				return st, true
			}
		}
	}
	if copyInOptionalChain(c) {
		return o.ExprType(orig)
	}
	return o.exprTypeBase(orig)
}

// copyInOptionalChain reports a link with a `?.` below it.
func copyInOptionalChain(e ast.Expression) bool {
	for {
		obj, optional, ok := chainChild(e)
		if !ok {
			return false
		}
		if optional {
			return true
		}
		e = obj
	}
}

// mayBeGenericInstance reports a receiver the checker types as a generic
// class's instance, or leaves unanswered: only such a receiver's method can
// be erased.
func (o frontOracle) mayBeGenericInstance(e ast.Expression) bool {
	t := o.c.TypeOf(e)
	return o.c.Unanswered(t) || t.Flags&checker.Object != 0 && t.Kind == checker.Instance && len(t.TypeArgs) > 0
}

// isLibObjectValue reports a library global's object type that is not a
// plain function type: a constructor, a namespace object, a host value.
func isLibObjectValue(t *checker.Type) bool {
	return t != nil && t.Flags&checker.Object != 0 && t.Kind != checker.Function
}

// reassignableModuleBinding reports a module-level `let`/`var` read: one
// that may hold another class (a subclass overriding a static), so a read
// through it dispatches on what it holds.
func (o frontOracle) reassignableModuleBinding(x ast.Expression) bool {
	id, isID := x.(*ast.Identifier)
	if !isID {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil || sym.Scope == nil || sym.Scope.Kind != binder.ModuleScope {
		return false
	}
	v, isV := singleVarDecl(sym)
	return isV && v.Kind != "const"
}

// classValue reports an expression the checker types as a class's
// constructor (`typeof C`): codegen reads its statics as declared.
func (o frontOracle) classValue(x ast.Expression) bool {
	t := o.c.TypeOf(x)
	return !o.c.Unanswered(t) && t.Flags&checker.Object != 0 && (t.ClassOf != nil || len(t.Constructs) > 0)
}

// iifeGenerator reports a call of a generator function expression in
// place (`function* () {}()`).
func iifeGenerator(e ast.Expression) bool {
	call, ok := e.(*ast.CallExpression)
	if !ok {
		return false
	}
	fe, ok := call.Callee.(*ast.FunctionExpression)
	return ok && fe.IsGenerator
}

// reprs reports whether reprOf names t.
func (o frontOracle) reprs(t *checker.Type) bool {
	_, ok := o.r.of(t)
	return ok
}

// isGenericFuncValue reports a function type reprOf cannot name (generic or
// overloaded): a builtin one read as a value is the closure codegen builds.
func isGenericFuncValue(t *checker.Type) bool {
	return t != nil && t.Flags&checker.Object != 0 && t.Kind == checker.Function && (len(t.TypeParams) > 0 || len(t.Overloads) > 0)
}

// isConstructorValue reports a builtin constructor's type: an object with
// call or construct signatures that is not a plain function type.
func isConstructorValue(t *checker.Type) bool {
	return t.Flags&checker.Object != 0 && t.Kind != checker.Function && (len(t.Calls) > 0 || len(t.Constructs) > 0)
}

// receiverOrCallee reports id as parent's member receiver or call callee.
func receiverOrCallee(parent ast.Node, id *ast.Identifier) bool {
	return exprReceiverOrCallee(parent, id)
}

// exprReceiverOrCallee is receiverOrCallee for any expression.
func exprReceiverOrCallee(parent ast.Node, id ast.Expression) bool {
	switch p := parent.(type) {
	case *ast.MemberExpression:
		return p.Object == ast.Expression(id)
	case *ast.CallExpression:
		return p.Callee == ast.Expression(id)
	}
	return false
}

// withoutFuncThis is object type t with its methods' `this` parameters
// dropped: the layout the checker's own type gives it.
func withoutFuncThis(t Type) Type {
	fs := make([]Field, len(t.Fields))
	copy(fs, t.Fields)
	for i, f := range fs {
		if f.Ty.IsFunc() && f.Ty.FuncThis {
			ft := f.Ty
			ft.FuncThis = false
			if len(ft.FuncParams) > 0 {
				ft.FuncParams = ft.FuncParams[1:]
			}
			fs[i].Ty = ft
		}
	}
	t.Fields = fs
	return t
}

// onlyThisDiffers reports a and b as one layout but for a's methods taking
// their `this` (withoutFuncThis).
func onlyThisDiffers(a, b Type) bool {
	if reprKey(a) == reprKey(b) {
		return false
	}
	strip := func(t Type) Type {
		if t.IsArray() && t.ElemType != nil {
			el := withoutFuncThis(*t.ElemType)
			return ArrayOf(el)
		}
		return withoutFuncThis(t)
	}
	return reprKey(strip(a)) == reprKey(strip(b))
}

// boxedArrayElemCall reports an element-callback method call on an array
// the checker leaves unanswered (an evolving JavaScript array): codegen
// types its callback's parameters and result as boxes.
func boxedArrayElemCall(o frontOracle, call *ast.CallExpression) bool {
	m, ok := call.Callee.(*ast.MemberExpression)
	if !ok || m.Optional || call.Optional || !elementCallback[m.Property] || !o.c.Unanswered(o.c.TypeOf(call)) {
		return false
	}
	rt, rok := o.ExprType(m.Object)
	return rok && rt.IsArray() && rt.ElemType != nil && !rt.IsTypedArray() && o.r.js != nil && o.r.js()
}

// superSequence is the instance `(super(…), …, this)` evaluates to
// (inlineFieldInitsAtSuper): the super call's.
func (o frontOracle) superSequence(sq *ast.SequenceExpression) (Type, bool) {
	if _, isThis := sq.Exprs[len(sq.Exprs)-1].(*ast.ThisExpression); !isThis || len(sq.Exprs) < 2 {
		return Type{}, false
	}
	call, isCall := sq.Exprs[0].(*ast.CallExpression)
	if !isCall {
		return Type{}, false
	}
	if _, isSuper := call.Callee.(*ast.SuperExpression); !isSuper {
		return Type{}, false
	}
	return o.ExprType(call)
}

// exprCallee reports x as parent's call callee.
func exprCallee(parent ast.Node, x ast.Expression) bool {
	c, ok := parent.(*ast.CallExpression)
	return ok && c.Callee == x
}

// isGenStepLayout reports codegen's generator step result, `{value, done}`.
func isGenStepLayout(t Type) bool {
	if !t.IsObject() || t.IsClass() {
		return false
	}
	_, _, v := t.FieldIndex("value")
	_, _, d := t.FieldIndex("done")
	return v && d
}

// literalGenFields is ty, object literal lit's layout, with each property
// whose value names a generator function declaration (`{ seq }`) holding
// that generator's value, as codegen stores it, not a method's box.
func (o frontOracle) literalGenFields(lit *ast.ObjectLiteral, ty Type) Type {
	if !ty.IsObject() || ty.IsDynamic() {
		return ty
	}
	ty = literalArgumentsRest(lit, ty)
	if o.r.generatorValue == nil {
		return ty
	}
	var fields []Field
	for _, p := range lit.Properties {
		id, isID := p.Value.(*ast.Identifier)
		if !isID || p.KeyExpr != nil {
			continue
		}
		sym, _ := o.b.Resolve(id)
		if sym == nil || len(sym.Declarations) != 1 {
			continue
		}
		if fd, isFD := sym.Declarations[0].Node.(*ast.FunctionDeclaration); !isFD || !fd.IsGenerator {
			continue
		}
		gt, ok := o.r.generatorValue(id)
		if !ok {
			continue
		}
		if fields == nil {
			fields = append([]Field(nil), ty.Fields...)
		}
		for i := range fields {
			if fields[i].Name == p.Key {
				fields[i].Ty = gt
			}
		}
	}
	if fields != nil {
		ty.Fields = fields
	}
	return ty
}

// literalArgumentsRest gives each method of object literal lit that reads
// `arguments` the implicit rest parameter emission appends for it
// (maybeAddArgumentsRestParams), in ty's field for it.
func literalArgumentsRest(lit *ast.ObjectLiteral, ty Type) Type {
	var fields []Field
	for _, p := range lit.Properties {
		fe, isFE := p.Value.(*ast.FunctionExpression)
		if !isFE || p.KeyExpr != nil || p.AccessorKind != "" || fe.Body == nil || fe.IsGenerator {
			continue
		}
		// Emission may have appended it to the declaration already.
		declared := fe.Params
		if hasArgumentsRestParamSlice(declared) {
			declared = declared[:len(declared)-1]
		} else if !hasArgumentsRestParamSlice(maybeAddArgumentsRestParams(fe.Params, fe.Body.Body)) {
			continue
		}
		if fields == nil {
			fields = append([]Field(nil), ty.Fields...)
		}
		for i := range fields {
			ft := fields[i].Ty
			if fields[i].Name != p.Key || !ft.IsFunc() || ft.FuncHasRest || len(ft.FuncParams) != len(declared) {
				continue
			}
			ft.FuncParams = append(append([]Type(nil), ft.FuncParams...), ArrayOf(TypeAny))
			ft.FuncHasRest = true
			if ft.FuncParamNames != nil {
				ft.FuncParamNames = append(append([]string(nil), ft.FuncParamNames...), argumentsRestParam)
			}
			if ft.FuncParamOptional != nil {
				ft.FuncParamOptional = append(append([]bool(nil), ft.FuncParamOptional...), false)
			}
			ft.FuncCountsArgs = true
			fields[i].Ty = ft
		}
	}
	if fields != nil {
		ty.Fields = fields
	}
	return ty
}

// isGeneratorCtor reports codegen's value of a generator function: a
// closure returning its instance.
func isGeneratorCtor(t Type) bool {
	return t.IsFunc() && t.FuncRetType != nil && t.FuncRetType.IsGenerator()
}

// staticMethodValue is class's static member: a field's type, or a method
// read as a value (emitStaticFieldRead).
func (e *Emitter) staticMethodValue(class, method string) (Type, bool) {
	info, ok := e.classes[class]
	if !ok {
		return Type{}, false
	}
	if ft, ok := info.StaticFieldTypes[method]; ok {
		return ft, true
	}
	sig, ok := info.StaticMethodSigs[method]
	if !ok {
		return Type{}, false
	}
	return funcValueType(sig), true
}

// classExprBinding reports a module binding of a class expression, which
// codegen declares as a class of that name (rewriteTopLevelClassExpressions).
func classExprBinding(sym *binder.Symbol) bool {
	v, ok := singleVarDecl(sym)
	if !ok || sym.Scope == nil || sym.Scope.Kind != binder.ModuleScope {
		return false
	}
	_, isCE := v.Init.(*ast.ClassExpression)
	return isCE
}

// namesClass reports id naming a class: its declaration, a module binding
// of a class expression, or a static member's `this` rewritten to it.
func (o frontOracle) namesClass(id *ast.Identifier) bool {
	if o.b.Module != nil {
		if prog, isProg := o.b.Module.Node.(*ast.Program); isProg && prog.StaticThis[id] != nil {
			return true
		}
	}
	sym, _ := o.b.Resolve(id)
	return sym != nil && (sym.Flags&binder.Class != 0 || classExprBinding(sym))
}

// functionTyped reports an expression the checker types as a function (a
// method read as a value), not a data property a narrowing may refine.
func (o frontOracle) functionTyped(e ast.Expression) bool {
	t := o.c.TypeOf(e)
	return !o.c.Unanswered(t) && t.Flags&checker.Object != 0 && t.Kind == checker.Function
}

// liftedGenReceiver reports a member read off `this` in a static generator
// method lifted to a generator function that takes its receiver as a
// leading `this` parameter.
func (o frontOracle) liftedGenReceiver(m *ast.MemberExpression) bool {
	if _, isThis := m.Object.(*ast.ThisExpression); !isThis || o.r.liftedGen == nil {
		return false
	}
	for n := o.c.ParentOf(m); n != nil; n = o.c.ParentOf(n) {
		switch f := n.(type) {
		case *ast.ArrowFunction:
			continue
		case *ast.FunctionDeclaration:
			g := o.r.liftedGen(f)
			return g != nil && len(g.Params) > 0 && g.Params[0].Name == "this"
		case *ast.FunctionExpression, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		}
	}
	return false
}

// spreadsHiddenFields reports an object literal spreading a class instance
// with a private or protected field (not a `#` name).
func (o frontOracle) spreadsHiddenFields(lit *ast.ObjectLiteral) bool {
	for _, p := range lit.Properties {
		sp, ok := p.Value.(*ast.SpreadElement)
		if !ok || p.Key != "" || p.KeyExpr != nil {
			continue
		}
		t := o.c.TypeOf(sp.Arg)
		if o.c.Unanswered(t) || t.Flags&checker.Object == 0 || t.Kind != checker.Instance {
			continue
		}
		for _, q := range t.Props {
			if q.Field && q.Visibility != "" && !strings.HasPrefix(q.Name, "#") {
				return true
			}
		}
	}
	return false
}

// hiddenFieldsLiteral is such a literal's layout, as inferObjectType builds
// it: each spread's fields, then each property, a later one replacing an
// earlier one of its name.
func (o frontOracle) hiddenFieldsLiteral(lit *ast.ObjectLiteral) (Type, bool) {
	if lit.HasAccessors() || hasProtoKey(lit) {
		return Type{}, false
	}
	var fields []Field
	upsert := func(f Field) {
		for i, existing := range fields {
			if existing.Name == f.Name {
				fields[i] = f
				return
			}
		}
		fields = append(fields, f)
	}
	for _, p := range lit.Properties {
		if sp, ok := p.Value.(*ast.SpreadElement); ok && p.Key == "" && p.KeyExpr == nil {
			st, ok := o.ExprType(sp.Arg)
			if !ok || st.IsDynamic() || !st.IsObject() {
				return Type{}, false
			}
			for _, f := range st.SpreadFields() {
				upsert(f)
			}
			continue
		}
		if p.KeyExpr != nil || p.AccessorKind != "" {
			return Type{}, false
		}
		ft, ok := o.ExprType(p.Value)
		if !ok {
			return Type{}, false
		}
		if fe, isFE := p.Value.(*ast.FunctionExpression); isFE && fe.IsGenerator {
			ft = TypeAny
		}
		upsert(Field{Name: p.Key, Ty: ft})
	}
	return UserObjectType(fields), true
}

// objLitHasSpread reports an object literal with a `...spread` property.
func objLitHasSpread(lit *ast.ObjectLiteral) bool {
	for _, prop := range lit.Properties {
		if _, ok := prop.Value.(*ast.SpreadElement); ok && prop.Key == "" && prop.KeyExpr == nil {
			return true
		}
	}
	return false
}

// codegenErasedClass reports whether sym is a generic class code generation
// compiles as an ordinary one (eraseTypeOnlyGenerics, eraseInheritingGenerics).
func codegenErasedClass(sym *binder.Symbol) bool {
	for _, d := range sym.Declarations {
		if cd, ok := d.Node.(*ast.ClassDeclaration); ok && cd.Declared != nil {
			return true
		}
	}
	return false
}

// streamReadResult is the layout of `ReadableStreamReadValueResult<T> |
// ReadableStreamReadDoneResult<T>`, the expansion of the lib's
// `ReadableStreamReadResult<T>` alias (streamReadResultType).
func (r reprCtx) streamReadResult(t *checker.Type) (Type, bool) {
	var value, done *checker.Type
	for _, m := range t.Types {
		if m.Flags&checker.Object == 0 || m.Kind != checker.Interface || m.Symbol == nil || !r.libGlobal(m.Symbol) || len(m.TypeArgs) != 1 {
			return Type{}, false
		}
		if n := m.Symbol.Name; n == "ReadableStreamReadValueResult" {
			value = m
		} else if n == "ReadableStreamReadDoneResult" {
			done = m
		} else {
			return Type{}, false
		}
	}
	if value == nil || done == nil || len(t.Types) != 2 {
		return Type{}, false
	}
	elem, ok := r.of(value.TypeArgs[0])
	if !ok {
		return Type{}, false
	}
	return streamReadResultType(elem), true
}

// joinedElems is the one element kind of an inferred tuple's elements:
// all the same, or nullable scalars (or settlements of them) differing only
// in their absent states, which codegen's literal joins (joinedPromiseElems).
func joinedElems(elems []Type) (Type, bool) {
	out := elems[0]
	for _, e := range elems[1:] {
		if reprKey(e) == reprKey(out) {
			continue
		}
		a, b := out, e
		settled := isSettlementType(a) && isSettlementType(b)
		if settled {
			a, b = a.VisibleFields()[1].Ty, b.VisibleFields()[1].Ty
		}
		if _, ok := sameButAbsence([]Type{a}, b); !ok {
			return Type{}, false
		}
		a.Absent = joinAbsence(a.Absent, b.Absent)
		if settled {
			out = SettlementType(a)
		} else {
			out = a
		}
	}
	return out, true
}

// funcMetaReceiver reports x as the receiver of a function's own `length`
// or `name` read (`Promise.all.length`).
func funcMetaReceiver(parent ast.Node, x ast.Expression) bool {
	pm, ok := parent.(*ast.MemberExpression)
	return ok && pm.Object == x && !pm.Optional && (pm.Property == "length" || pm.Property == "name")
}

// settledResult is the settlement record (SettlementType) a library
// `PromiseSettledResult<T>` union is: Promise.allSettled's elements.
var settledNames = [2]string{"PromiseFulfilledResult", "PromiseRejectedResult"}

func (r reprCtx) settledResult(t *checker.Type) (Type, bool) {
	var value *checker.Type
	rejected := false
	for _, m := range t.Types {
		if m.Flags&checker.Object == 0 || m.Kind != checker.Interface || m.Symbol == nil || !r.libGlobal(m.Symbol) {
			return Type{}, false
		}
		// The fulfilled record carries `value: T`, the rejected `reason`.
		switch {
		case m.Prop("value") != nil && len(m.TypeArgs) == 1 && m.Symbol.Name == settledNames[0]:
			value = m.TypeArgs[0]
		case m.Prop("reason") != nil && m.Symbol.Name == settledNames[1]:
			rejected = true
		default:
			return Type{}, false
		}
	}
	if value == nil || !rejected || len(t.Types) != 2 {
		return Type{}, false
	}
	vt, ok := r.of(value)
	if !ok {
		return Type{}, false
	}
	if isVoidTy(vt) {
		vt = TypeAny // a Promise<void> fulfils with undefined
	}
	return SettlementType(vt), true
}

// emptyTypeLiteral reports the type literal `{}`.
func emptyTypeLiteral(n ast.TypeNode) bool {
	if p, isP := n.(*ast.ParenthesizedType); isP {
		n = p.Type
	}
	tl, ok := n.(*ast.TypeLiteral)
	return ok && len(tl.Members) == 0
}

// isStringLitNode reports a string or template literal.
func isStringLitNode(x ast.Expression) bool {
	switch x.(type) {
	case *ast.StringLiteral, *ast.TemplateLiteral:
		return true
	}
	return false
}

// declParams is the parameter list of the function e is: a function
// literal's own, or a function declaration's an identifier names.
func (o frontOracle) declParams(e ast.Expression) []ast.Param {
	switch fn := e.(type) {
	case *ast.FunctionExpression:
		return fn.Params
	case *ast.ArrowFunction:
		return fn.Params
	case *ast.Identifier:
		if sym, _ := o.b.Resolve(fn); sym != nil && len(sym.Declarations) == 1 {
			if fd, isFD := sym.Declarations[0].Node.(*ast.FunctionDeclaration); isFD {
				return fd.Params
			}
			if v, isV := singleVarDecl(sym); isV && v.TypeAnnot == nil && (v.Kind == "const" || !o.reassigned(sym)) {
				switch v.Init.(type) {
				case *ast.ArrowFunction, *ast.FunctionExpression:
					return o.declParams(v.Init)
				}
			}
		}
	}
	return nil
}

// literalFromReturns is a synchronous, non-generator function literal's
// type when its checker type fails to map: an unannotated parameter
// without a default whose type has no representation is a box, as
// emission types it (checkerContextualParam takes only primitives and any,
// the rest fall to TypeAny), the
// other parameters map as usual, and a result reprOf cannot name (a
// returned generator object held as a box) is what its returns read as.
func (o frontOracle) literalFromReturns(e ast.Expression) (Type, bool) {
	var params []ast.Param
	var body *ast.BlockStatement
	var exprBody ast.Expression // `=> expr`
	var retAnn ast.TypeNode
	switch fn := e.(type) {
	case *ast.FunctionExpression:
		if fn.IsGenerator || fn.IsAsync {
			return Type{}, false
		}
		params, body, retAnn = fn.Params, fn.Body, fn.RetType
	case *ast.ArrowFunction:
		if fn.IsAsync {
			return Type{}, false
		}
		params, body, exprBody, retAnn = fn.Params, fn.Block, fn.Body, fn.RetType
	default:
		return Type{}, false
	}
	if retAnn != nil {
		return Type{}, false
	}
	ct := o.c.TypeOf(e)
	if o.c.Unanswered(ct) || ct.Flags&checker.Object == 0 || ct.Kind != checker.Function || ct.ThisType != nil || len(ct.Overloads) > 0 || len(ct.TypeParams) > 0 || ct.Ret() == nil || len(ct.Params) != len(params) {
		return Type{}, false
	}
	stub := *ct.WithoutResult()
	stub.Params = append([]*checker.Type(nil), ct.Params...)
	boxed := map[int]bool{}
	for i, p := range params {
		// (A defaulted pattern, `([[,] = g()] = []) =>`, is as boxed as a
		// bare name when its type has no representation: fnmeta's
		// patternDefaultType falls back to any there.)
		if p.Type == nil && (p.Default == nil || p.ArrayPattern != nil || p.ObjectPattern != nil) && !p.Rest {
			if _, pok := o.r.of(ct.Params[i]); !pok {
				boxed[i] = true
				stub.Params[i] = o.c.AnyType()
			}
		}
	}
	ret, rok := o.r.of(ct.Ret())
	if !rok {
		switch {
		case body != nil:
			var bok bool
			if ret, _, bok = o.ReturnType(body); !bok {
				return Type{}, false
			}
		case exprBody != nil:
			// An expression body's own representation (`() => o`).
			var eok bool
			if ret, eok = o.ExprType(exprBody); !eok {
				return Type{}, false
			}
		default:
			return Type{}, false
		}
	} else if len(boxed) == 0 {
		return Type{}, false // nothing here defeated reprOf
	}
	fty, fok := o.r.function(&stub)
	if !fok {
		return Type{}, false
	}
	fty.FuncRetType = &ret
	return fty, true
}

// returnsOnlyThis reports a sync, non-generator, unannotated function
// expression without parameters (but the `this` emission may add) whose
// every return is a bare `this`.
func returnsOnlyThis(fe *ast.FunctionExpression) bool {
	if fe.IsAsync || fe.IsGenerator || fe.RetType != nil || fe.Body == nil {
		return false
	}
	for _, p := range fe.Params {
		if p.Name != "this" {
			return false
		}
	}
	rs := returnExprs(fe.Body, nil)
	for _, r := range rs {
		if _, isThis := r.(*ast.ThisExpression); !isThis {
			return false
		}
	}
	return len(rs) > 0
}

// hasSelfReturningMethod reports a literal with a returnsOnlyThis method.
func (o frontOracle) selfReturningCall(m *ast.MemberExpression) bool {
	switch obj := m.Object.(type) {
	case *ast.CallExpression:
		if om, isM := obj.Callee.(*ast.MemberExpression); isM && !om.Optional && !obj.Optional {
			return o.selfReturningCall(om)
		}
	case *ast.Identifier:
		sym, _ := o.b.Resolve(obj)
		if sym == nil || len(sym.Declarations) != 1 || o.b.StoredElsewhere(sym) {
			return false
		}
		vd, isVD := sym.Declarations[0].Node.(*ast.VarDeclaration)
		if !isVD || vd.Kind != "const" || vd.TypeAnnot != nil {
			return false
		}
		lit, isLit := vd.Init.(*ast.ObjectLiteral)
		if !isLit || hasAccessor(lit) {
			return false
		}
		for _, p := range lit.Properties {
			if p.KeyExpr == nil && p.Key == m.Property {
				fe, isFE := p.Value.(*ast.FunctionExpression)
				return isFE && returnsOnlyThis(fe)
			}
		}
	}
	return false
}

func hasSelfReturningMethod(lit *ast.ObjectLiteral) bool {
	for _, p := range lit.Properties {
		if fe, isFE := p.Value.(*ast.FunctionExpression); isFE && returnsOnlyThis(fe) {
			return true
		}
	}
	return false
}

// fieldsLiteral is an object literal's fixed layout from its properties'
// own representations, the last of a duplicated key winning.
func (o frontOracle) fieldsLiteral(lit *ast.ObjectLiteral) (Type, bool) {
	if o.litBusy[lit] {
		// A property reading the literal's own binding (`var a = { f: a }`).
		return Type{}, false
	}
	o.litBusy[lit] = true
	defer delete(o.litBusy, lit)
	var fields []Field
	for _, p := range lit.Properties {
		if p.Key == "" {
			return Type{}, false
		}
		pt, pok := o.ExprType(p.Value)
		if !pok {
			return Type{}, false
		}
		for i, f := range fields {
			if f.Name == p.Key {
				fields = append(fields[:i:i], fields[i+1:]...)
				break
			}
		}
		fields = append(fields, Field{Name: p.Key, Ty: pt})
	}
	return UserObjectType(fields), true
}

// forOfBinding is the sync for-of loop whose bare loop variable id names,
// when that loop is the name's only declaration.
// forOfIterableType is the type of the iterable of the loop binding id,
// once per loop at a time: a loop reading its own variable (`for (var v
// of v)`) has none while its iterable is being typed.
func (o frontOracle) forOfIterableType(id *ast.Identifier) (Type, bool) {
	f := o.forOfBinding(id)
	if f == nil || o.forOfBusy[f] {
		return Type{}, false
	}
	o.forOfBusy[f] = true
	defer delete(o.forOfBusy, f)
	return o.ExprType(f.Iterable)
}

func (o frontOracle) forOfBinding(id *ast.Identifier) *ast.ForOfStatement {
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return nil
	}
	if f, ok := sym.Declarations[0].Node.(*ast.ForOfStatement); ok && !f.Await && f.VarName == id.Name && f.ArrayPattern == nil && f.ObjectPattern == nil {
		return f
	}
	return nil
}

// builtinRefValueType is the closure type of the builtin function expr
// names (emitBuiltinValue), when it names one.
func (e *Emitter) builtinRefValueType(expr ast.Expression) (Type, bool) {
	b, ok := e.builtinRef(expr)
	if !ok {
		return Type{}, false
	}
	_, sig, err := e.ensureBuiltinFunc(b)
	if err != nil {
		return Type{}, false
	}
	return funcTypeFromSig(sig), true
}

// literalFields is object literal lit's layout from the checker's property
// types, each property whose type does not map taking its value's answer;
// false for a literal with a spread, accessor, method or computed key.
func (o frontOracle) literalFields(lit *ast.ObjectLiteral) (Type, bool) {
	if o.litBusy[lit] {
		// A property whose function returns the literal's own binding.
		return Type{}, false
	}
	o.litBusy[lit] = true
	defer delete(o.litBusy, lit)
	ct := o.c.TypeOf(lit)
	if o.c.Unanswered(ct) || ct.Flags&checker.Object == 0 || ct.Kind != checker.Anonymous || len(ct.Props) != len(lit.Properties) {
		return Type{}, false
	}
	fields := make([]Field, 0, len(ct.Props))
	for i, p := range ct.Props {
		lp := lit.Properties[i]
		key := lp.Key
		if lp.KeyExpr != nil {
			// A well-known symbol's member (`[Symbol.asyncIterator]: …`).
			if m, ok := lp.KeyExpr.(*ast.MemberExpression); ok {
				if id, ok := m.Object.(*ast.Identifier); ok && id.Name == "Symbol" {
					key = "@@" + m.Property
				}
			}
		}
		if p.Accessor || lp.AccessorKind != "" || key != p.Name || lp.Value == nil || p.Optional {
			return Type{}, false
		}
		if _, isSpread := lp.Value.(*ast.SpreadElement); isSpread {
			return Type{}, false
		}
		ty, ok := o.r.of(p.Type)
		if _, isID := lp.Value.(*ast.Identifier); isID && ok && !ty.IsDynamic() {
			// A binding held boxed (an any-argument call's result) is stored
			// as that box.
			if vt, vok := o.ExprType(lp.Value); vok && isUnconstrainedDynamic(vt) {
				ty = vt
			}
		}
		if fe, isFE := lp.Value.(*ast.FunctionExpression); isFE && fe.IsGenerator {
			ty, ok = TypeAny, true // a generator method is held as `any`
		} else if o.generatorDeclRef(lp.Value) {
			ty, ok = TypeAny, true // `{ seq }` of a `function*`: the same
		} else if isGeneratorFunc(p.Type) {
			ty, ok = o.ExprType(lp.Value) // a function returning one
		}
		if !ok {
			ty, ok = o.ExprType(lp.Value)
		}
		if id, isID := lp.Value.(*ast.Identifier); !ok && isID {
			// An overloaded function's value: its implementation signature.
			if sym, _ := o.b.Resolve(id); sym != nil && len(sym.Declarations) > 0 {
				if fd, isFD := sym.Declarations[len(sym.Declarations)-1].Node.(*ast.FunctionDeclaration); isFD {
					if it := o.c.ImplementationSignature(fd); it != nil {
						ty, ok = o.r.of(it)
					}
				}
			}
		}
		if !ok {
			return Type{}, false
		}
		fields = append(fields, Field{Name: p.Name, Ty: ty})
	}
	return UserObjectType(fields), true
}

// generatorDeclRef reports an identifier naming a generator function
// declaration.
func (o frontOracle) generatorDeclRef(e ast.Expression) bool {
	id, ok := e.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return false
	}
	fd, isFD := sym.Declarations[0].Node.(*ast.FunctionDeclaration)
	return isFD && fd.IsGenerator
}

// literalHasMethod reports whether object literal lit has a function
// expression value (a method shorthand among them).
func literalHasMethod(lit *ast.ObjectLiteral) bool {
	for _, p := range lit.Properties {
		if _, ok := p.Value.(*ast.FunctionExpression); ok {
			return true
		}
	}
	return false
}

// genPatternLeaf reports a name a generator's destructured parameter
// binds, once codegen has rewritten that parameter.
func (o frontOracle) genPatternLeaf(id *ast.Identifier) bool {
	if o.r.genDesugared == nil {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return false
	}
	// A function expression is lifted to a declaration sharing its body.
	switch fn := sym.Declarations[0].Node.(type) {
	case *ast.FunctionDeclaration:
		if o.r.liftedGen != nil {
			if g := o.r.liftedGen(fn); g != nil {
				fn = g // a static generator method's body moved to its generator
			}
		}
		return fn.IsGenerator && id.Name != fn.Name && fn.Body != nil && o.r.genDesugared(fn.Body)
	case *ast.FunctionExpression:
		return fn.IsGenerator && fn.Name != id.Name && fn.Body != nil && o.r.genDesugared(fn.Body)
	}
	return false
}

// allOptional reports whether every property of props is optional.
func allOptional(props []*checker.Property) bool {
	for _, p := range props {
		if !p.Optional {
			return false
		}
	}
	return true
}

// boxedInitRead is a read of a binding held as its initializer is, boxed
// (dynamicInit): unboxed where the checker narrows the read
// (checkerNarrowed), as emitIdent reads it.
func (o frontOracle) boxedInitRead(id *ast.Identifier, v *ast.VarDeclaration) (Type, bool) {
	if accessorLiteral(o.b, v) {
		return TypeAny, true
	}
	it, ok := o.ExprType(v.Init)
	if ok && o.r.narrowed != nil && !it.IsArray() {
		if nt, nok := o.r.narrowed(id, it); nok {
			return nt, true
		}
	}
	return it, ok
}

// explicitTupleTypeArgs reports a `new Map<K, V>()` whose written type
// arguments name a tuple.
func explicitTupleTypeArgs(x ast.Expression) bool {
	nm, ok := x.(*ast.NewMapExpression)
	if !ok {
		return false
	}
	has := func(a ast.TypeNode) bool {
		found := false
		if a != nil {
			ast.Inspect(a, func(n ast.Node) bool {
				if _, ok := n.(*ast.TupleType); ok {
					found = true
				}
				return !found
			})
		}
		return found
	}
	return has(nm.KeyType) || has(nm.ValType)
}

// Warm types every expression under n with the oracle's checker, before a
// rewrite after binding changes the code it reads (warmFront).
// StandsFor records that syn, built after binding, has orig's type.
func (o frontOracle) StandsFor(syn, orig ast.Expression) { o.c.StandsFor(syn, orig) }

func (o frontOracle) Warm(n ast.Node) {
	// Declarations after every expression: a binding resolved first holds
	// its initializer's computation open across the members it reaches
	// (`var self = this` then a nested class's method returning `self.#x`).
	var decls []*ast.VarDeclaration
	ast.Inspect(n, func(x ast.Node) bool {
		if ex, ok := x.(ast.Expression); ok {
			o.c.TypeOf(ex)
		}
		if v, ok := x.(*ast.VarDeclaration); ok {
			decls = append(decls, v)
		}
		return true
	})
	for _, v := range decls {
		o.c.WarmDecl(v)
	}
}

// reassigned reports a write to sym after its declaration (`x = …`,
// `x++`) anywhere in its scope.
func (o frontOracle) reassigned(sym *binder.Symbol) bool {
	if sym.Scope == nil || sym.Scope.Node == nil {
		return true
	}
	if o.written != nil {
		set, ok := o.written[sym.Scope.Node]
		if !ok {
			set = o.writtenIn(sym.Scope.Node)
			o.written[sym.Scope.Node] = set
		}
		return set[sym]
	}
	return o.writtenIn(sym.Scope.Node)[sym]
}

// writtenIn is the set of symbols an assignment or update in scope node n
// writes (by name): what reassigned asks of every symbol of a scope, walked
// once.
func (o frontOracle) writtenIn(n ast.Node) map[*binder.Symbol]bool {
	set := map[*binder.Symbol]bool{}
	ast.Inspect(n, func(n ast.Node) bool {
		var target ast.Expression
		switch x := n.(type) {
		case *ast.AssignmentExpression:
			target = x.Left
		case *ast.UpdateExpression:
			target = x.Arg
		}
		if id, isID := target.(*ast.Identifier); isID {
			if s, _ := o.b.Resolve(id); s != nil {
				set[s] = true
			}
		}
		return true
	})
	return set
}

// libTupleSource reports a call of a builtin library method or function,
// or an array literal of only spreads of such calls: the tuples its result
// holds are the library signature's annotations.
func (o frontOracle) libTupleSource(e ast.Expression) bool {
	switch x := e.(type) {
	case *ast.CallExpression:
		// A Map's `m[Symbol.iterator]()`: its entries.
		if ix, isIx := x.Callee.(*ast.IndexExpression); isIx && o.libMapValue(ix.Object) {
			if mem, isMem := ix.Index.(*ast.MemberExpression); isMem && mem.Property == "iterator" {
				if id, isID := mem.Object.(*ast.Identifier); isID && id.Name == "Symbol" {
					return true
				}
			}
		}
		m, isM := x.Callee.(*ast.MemberExpression)
		if !isM {
			return false
		}
		// A method of an array held with boxed elements (`Array.from` of
		// a class's iterator, then `.find(…)`): its result is held boxed
		// too, whatever tuples the signature spells.
		if ot, ok := o.ExprType(m.Object); ok && ot.IsArray() && ot.ElemType != nil && ot.ElemType.IsDynamic() {
			return false
		}
		return o.c.MemberInLibrary(m)
	case *ast.ArrayLiteral:
		if len(x.Elements) == 0 {
			return false
		}
		for _, el := range x.Elements {
			sp, isSp := el.(*ast.SpreadElement)
			if !isSp || !o.libTupleSource(sp.Arg) && !o.libMapValue(sp.Arg) && !o.declaredIteratorValue(sp.Arg) {
				return false
			}
		}
		return true
	}
	return false
}

// declaredIteratorValue reports e a class instance whose
// [Symbol.iterator]() declares its result (`IterableIterator<[string,
// any]>`, FormData's): spread, the tuples that declaration spells.
func (o frontOracle) declaredIteratorValue(e ast.Expression) bool {
	t := o.c.NonNullable(o.c.TypeOf(e))
	if o.c.Unanswered(t) || t.Flags&checker.Object == 0 || t.Kind != checker.Instance {
		return false
	}
	p := t.Prop("@@iterator")
	if p == nil {
		return false
	}
	fd, isFD := p.Decl.(*ast.FunctionDeclaration)
	return isFD && fd.ReturnType != nil
}

// libMapValue reports e a library Map's value: spread, its entry tuples.
func (o frontOracle) libMapValue(e ast.Expression) bool {
	t := o.c.NonNullable(o.c.TypeOf(e))
	if o.c.Unanswered(t) || t.Flags&checker.Object == 0 || t.Kind != checker.Interface || t.Symbol == nil {
		return false
	}
	return (t.Symbol.Name == "Map" || t.Symbol.Name == "ReadonlyMap") && t.Symbol.Scope == o.b.Globals
}

// patternParamLeaf is the array-pattern parameter of sym's function binding
// name, and that leaf, or nil.
func patternParamLeaf(sym *binder.Symbol, name string) (*ast.Param, *ast.ArrayPatternElem) {
	if len(sym.Declarations) != 1 {
		return nil, nil
	}
	var params []ast.Param
	switch f := sym.Declarations[0].Node.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	case *ast.FunctionDeclaration:
		params = f.Params
	default:
		return nil, nil
	}
	for i := range params {
		if params[i].ArrayPattern == nil {
			continue
		}
		if el := findArrayPatternElem(params[i].ArrayPattern, name); el != nil {
			return &params[i], el
		}
	}
	return nil, nil
}

// userClassName reports x naming a class the program declares (codegen's
// isClassName).
func (o frontOracle) userClassName(x ast.Expression) bool {
	id, ok := x.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil || o.r.libGlobal(sym) || len(sym.Declarations) != 1 {
		return false
	}
	_, isClass := sym.Declarations[0].Node.(*ast.ClassDeclaration)
	return isClass
}

// scopedLiteralKey reports a computed or spread key in lit or a literal it
// nests.
func scopedLiteralKey(lit *ast.ObjectLiteral) bool {
	for _, p := range lit.Properties {
		if p.KeyExpr != nil || p.Key == "" {
			return true
		}
		if sub, ok := p.Value.(*ast.ObjectLiteral); ok && scopedLiteralKey(sub) {
			return true
		}
	}
	return false
}

// paramReplacingFunc is the function declaration replacing the parameter id
// reads, or nil.
func (o frontOracle) paramReplacingFunc(id *ast.Identifier) *ast.FunctionDeclaration {
	sym, _ := o.b.Resolve(id)
	if sym == nil {
		return nil
	}
	// A parameter's declaration is the function that owns it.
	var replacing *ast.FunctionDeclaration
	param := false
	for _, d := range sym.Declarations {
		switch n := d.Node.(type) {
		case *ast.FunctionDeclaration:
			if n.Name == id.Name && n.Body != nil {
				replacing = n
			} else {
				param = true
			}
		case *ast.FunctionExpression, *ast.ArrowFunction:
			param = true
		}
	}
	if !param {
		return nil
	}
	return replacing
}

// promotedGlobal reports a module `var` codegen promotes to a module global
// (the module-global pre-pass), held as its registered type: no hoisting widening.
func (o frontOracle) promotedGlobal(sym *binder.Symbol, v *ast.VarDeclaration) bool {
	if sym.Scope.Kind != binder.ModuleScope {
		return false
	}
	// One nested in a top-level block is promoted as what a skipped
	// declaration leaves (registerModuleGlobals): widened all the same.
	parent := o.c.ParentOf(v)
	if l, isList := parent.(*ast.VarDeclarationList); isList {
		parent = o.c.ParentOf(l)
	}
	if _, top := parent.(*ast.Program); !top {
		return false
	}
	_, ok, _ := o.GlobalType(v)
	return ok
}

// lastRedeclaredFunc is the last of two or more function declarations id
// names, or nil.
func (o frontOracle) lastRedeclaredFunc(id *ast.Identifier) *ast.FunctionDeclaration {
	sym, _ := o.b.Resolve(id)
	if sym == nil {
		return nil
	}
	var last *ast.FunctionDeclaration
	n := 0
	for _, d := range sym.Declarations {
		if fd, ok := d.Node.(*ast.FunctionDeclaration); ok && fd.Body != nil && fd.Name == id.Name && len(fd.Overloads) == 0 {
			last = fd
			n++
		}
	}
	if n < 2 {
		return nil
	}
	return last
}

// synthesizedArguments reports an `arguments` read resolving to a body
// `var arguments` of a function codegen gives its arguments object.
func (o frontOracle) synthesizedArguments(id *ast.Identifier) bool {
	if id.Name != "arguments" {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil || sym.Implicit || sym.Scope == nil {
		return false
	}
	if _, isVar := singleVarDecl(sym); !isVar {
		return false
	}
	switch fn := sym.Scope.Node.(type) {
	case *ast.FunctionDeclaration:
		return fn.Body != nil && paramsUseArguments(fn.Params, fn.Body.Body)
	case *ast.FunctionExpression:
		return fn.Body != nil && paramsUseArguments(fn.Params, fn.Body.Body)
	}
	return false
}

// boundMethodValue reports an unannotated, never-reassigned binding of a
// member read (a method value).
func (o frontOracle) boundMethodValue(id *ast.Identifier) bool {
	sym, _ := o.b.Resolve(id)
	if sym == nil {
		return false
	}
	v, isV := singleVarDecl(sym)
	if !isV || v.TypeAnnot != nil || (v.Kind != "const" && o.reassigned(sym)) {
		return false
	}
	_, isM := v.Init.(*ast.MemberExpression)
	return isM
}

// callerDefaultParams is ty with the parameters of params whose default
// the caller fills present (exprTypeBase).
func (o frontOracle) callerDefaultParams(ty Type, params []ast.Param) Type {
	off := 0
	if ty.FuncThis && (len(params) == 0 || params[0].Name != "this") {
		off = 1
	}
	var ps []Type
	for i, p := range params {
		k := i + off
		// An unannotated parameter whose default types it never (a
		// throwing default) is emission's untyped default.
		if k < len(ty.FuncParams) && p.Type == nil && p.Default != nil && p.ArrayPattern == nil && p.ObjectPattern == nil && !p.Rest &&
			(o.r.js == nil || !o.r.js()) && (ty.FuncParams[k].IsNever() || ty.FuncParams[k].isKind(kindUndefined)) {
			if ps == nil {
				ps = append([]Type(nil), ty.FuncParams...)
			}
			it := TypeI64
			it.Inferred = true
			ps[k] = optionalParamType(p, it)
			continue
		}
		if k >= len(ty.FuncParams) || callSiteDefault(p) == nil {
			continue
		}
		if a := ty.FuncParams[k].Absent; a != absUndef && a != absBoth {
			continue
		}
		if ps == nil {
			ps = append([]Type(nil), ty.FuncParams...)
		}
		if ps[k].Absent == absBoth {
			ps[k].Absent = absNull
		} else {
			ps[k].Absent = absNone
		}
	}
	if ps != nil {
		ty.FuncParams = ps
	}

	return ty
}

// literalMethodDefaults applies callerDefaultParams to the method fields of
// an object literal's layout.
func (o frontOracle) literalMethodDefaults(lit *ast.ObjectLiteral, ty Type) Type {
	var fields []Field
	for _, p := range lit.Properties {
		fe, isFE := p.Value.(*ast.FunctionExpression)
		if p.KeyExpr != nil || p.AccessorKind != "" || !isFE {
			continue
		}
		for i, f := range ty.Fields {
			if f.Name != p.Key || !f.Ty.IsFunc() {
				continue
			}
			if nt := o.callerDefaultParams(f.Ty, fe.Params); reprKey(nt) != reprKey(f.Ty) {
				if fields == nil {
					fields = append([]Field(nil), ty.Fields...)
				}
				fields[i].Ty = nt
			}
		}
	}
	if fields != nil {
		ty.Fields = fields
	}
	return ty
}

// collectionSeedRow reports an element of the array literal seeding a
// `new Map`/`Set`/`WeakMap`/`WeakSet`.
func (o frontOracle) collectionSeedRow(row *ast.ArrayLiteral) bool {
	seed, isLit := o.c.ParentOf(row).(*ast.ArrayLiteral)
	if !isLit {
		return false
	}
	switch o.c.ParentOf(seed).(type) {
	case *ast.NewMapExpression, *ast.NewSetExpression, *ast.NewWeakMapExpression, *ast.NewWeakSetExpression:
		return true
	}
	return false
}

// nestedInDefaultedElem reports leaf inside a nested array pattern that
// has a default (`[[...x] = v]`).
func nestedInDefaultedElem(elems []ast.ArrayPatternElem, leaf *ast.ArrayPatternElem) bool {
	for i := range elems {
		if elems[i].SubArray != nil && elems[i].Default != nil && findArrayPatternElem(elems[i].SubArray, leaf.Name) == leaf {
			return true
		}
	}
	return false
}

// staticClassField reports a read `C.f` of a static field of program class
// C initialized with a class expression (hoisted or not).
func (o frontOracle) staticClassField(x ast.Expression) bool {
	m, ok := x.(*ast.MemberExpression)
	if !ok || m.Optional {
		return false
	}
	id, ok := m.Object.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return false
	}
	cd, ok := sym.Declarations[0].Node.(*ast.ClassDeclaration)
	if v, isV := sym.Declarations[0].Node.(*ast.VarDeclaration); isV {
		if ce, isCE := v.Init.(*ast.ClassExpression); isCE {
			cd, ok = ce.Decl, true
		}
	}
	if !ok || cd == nil {
		return false
	}
	for _, f := range cd.Fields {
		if f.Static && f.Name == m.Property {
			// A class expression, or the reference hoisting left for it.
			if id, isID := f.Initializer.(*ast.Identifier); isID {
				return strings.Contains(id.Name, "__kml_cx")
			}
			_, isCE := f.Initializer.(*ast.ClassExpression)
			return isCE
		}
	}
	return false
}

// literalMethodReturns is ty with the method fields of lit that return an
// accessor literal (ReturnType) returning its synthetic class's instance.
func (o frontOracle) literalMethodReturns(lit *ast.ObjectLiteral, ty Type) Type {
	// A method reading the literal's own binding asks for it again.
	if o.returnsBusy[lit] {
		return ty
	}
	o.returnsBusy[lit] = true
	defer delete(o.returnsBusy, lit)
	var fields []Field
	for _, p := range lit.Properties {
		fe, isFE := p.Value.(*ast.FunctionExpression)
		if p.KeyExpr != nil || p.AccessorKind != "" || !isFE || fe.Body == nil {
			continue
		}
		rt, _, rok := o.ReturnType(fe.Body)
		if !rok || !rt.IsClass() {
			continue
		}
		if fields == nil {
			fields = append([]Field(nil), ty.Fields...)
		}
		for i := range fields {
			if ft := fields[i].Ty; fields[i].Name == p.Key && ft.IsFunc() && ft.FuncRetType != nil && !ft.FuncRetType.IsClass() {
				r := rt
				ft.FuncRetType = &r
				fields[i].Ty = ft
			}
		}
	}
	if fields != nil {
		ty.Fields = fields
	}
	return ty
}

// genericLiteral reports a function literal, which the checker types as
// generic when it declares type parameters (`<T>(x: T) => x`).
func genericLiteral(e ast.Expression) bool {
	switch e.(type) {
	case *ast.ArrowFunction, *ast.FunctionExpression:
		return true
	}
	return false
}

// promiseStatics are the Promise statics that construct their receiver.
var promiseStatics = map[string]bool{"resolve": true, "reject": true, "all": true, "allSettled": true, "any": true, "race": true}

// promiseSubclassStatic is a Promise static called on a subclass
// (`Counted.resolve(1)`): an instance of that class (its `this`,
// NewPromiseCapability(C)), settling with the checker's value.
func (o frontOracle) promiseSubclassStatic(call *ast.CallExpression) (Type, bool) {
	if call.Optional {
		return Type{}, false
	}
	m, isM := call.Callee.(*ast.MemberExpression)
	if !isM || m.Optional || !promiseStatics[m.Property] {
		return Type{}, false
	}
	ct := o.c.TypeOf(m.Object)
	if o.c.Unanswered(ct) || ct.Flags&checker.Object == 0 || ct.ClassOf == nil {
		return Type{}, false
	}
	proto := ct.Prop("prototype")
	if proto == nil || !o.extendsPromise(proto.Type) {
		return Type{}, false
	}
	it, iok := o.r.of(proto.Type)
	if m.Property == "reject" && iok && it.IsClass() {
		return it, true // settles with no value of its own
	}
	ty, ok := o.r.of(o.c.TypeOf(call))
	if !iok || !it.IsClass() || !ok || !ty.IsPromise() || ty.PromiseType == nil {
		return Type{}, false
	}
	pt := *ty.PromiseType
	// `all` of a literal holding thenables settles with the array emission
	// builds (as Promise.all's).
	if m.Property == "all" && len(call.Args) == 1 && o.r.inferred != nil {
		if lit, isLit := call.Args[0].(*ast.ArrayLiteral); isLit && o.holdsThenableInstance(lit) {
			if lt, lok := o.r.inferred(lit); lok && lt.IsArray() && lt.ElemType != nil && lt.ElemType.IsDynamic() {
				pt = lt
			}
		}
	}
	it.PromiseType = &pt
	return it, true
}

// genericNewRepr is the instance `new C(…)` of a program generic class C
// builds: the monomorph newTypeArgs instantiates.
func (e *Emitter) genericNewRepr(ne *ast.NewExpression) (Type, bool) {
	genDecl, ok := e.genericClasses[ne.ClassName]
	if !ok {
		return Type{}, false
	}
	targs, ok := e.newTypeArgs(genDecl, ne)
	if !ok {
		return Type{}, false
	}
	ty, err := e.genericClassInstanceType(genDecl, e.buildTypeArgSubs(genDecl.TypeParams, targs))
	return ty, err == nil
}

// anyOrPromiseOfAny reports whether implementation fd is declared to return
// `any` or a `Promise<any>`.
func (o frontOracle) anyOrPromiseOfAny(fd *ast.FunctionDeclaration) bool {
	sig := o.c.ImplementationSignature(fd)
	if sig == nil || sig.Ret() == nil {
		return false
	}
	r := sig.Ret()
	if r.Flags&checker.Any != 0 {
		return true
	}
	return r.Flags&checker.Object != 0 && r.Symbol != nil && declaredLowerType(r.Symbol) == "promise" &&
		len(r.TypeArgs) == 1 && r.TypeArgs[0].Flags&checker.Any != 0
}

// dynamicMethodRead reports a read of a method a dynamically represented
// builtin declares (`o.hasOwnProperty`, Object.prototype's) on a static
// object or class instance, whose layout has no such member: a function
// value taking its receiver as `this`, held boxed.
func (o frontOracle) dynamicMethodRead(m *ast.MemberExpression) bool {
	rt, ok := o.ExprType(m.Object)
	return ok && isDynamicMethodRead(o.c, m, rt)
}

func isDynamicMethodRead(c frontView, m *ast.MemberExpression, recv Type) bool {
	if c == nil || !recv.IsObject() || recv.IsDynamic() || recv.IsArray() {
		return false
	}
	ms, ok := c.MemberDecl(m).(*ast.MethodSignature)
	return ok && dynamicLibMethods()[ms]
}

var (
	dynamicLibMethodsOnce sync.Once
	dynamicLibMethodsSet  map[*ast.MethodSignature]bool
)

// dynamicLibMethods are the methods of the builtin interfaces tagged
// `@lowerType dynamic` (Object's).
func dynamicLibMethods() map[*ast.MethodSignature]bool {
	dynamicLibMethodsOnce.Do(func() {
		dynamicLibMethodsSet = map[*ast.MethodSignature]bool{}
		progs, err := lib.Programs()
		if err != nil {
			return
		}
		for _, p := range progs {
			ast.Inspect(p, func(n ast.Node) bool {
				if id, ok := n.(*ast.InterfaceDeclaration); ok && id.LowerType == "dynamic" {
					for _, mem := range id.Members {
						if ms, ok := mem.(*ast.MethodSignature); ok {
							dynamicLibMethodsSet[ms] = true
						}
					}
				}
				return true
			})
		}
	})
	return dynamicLibMethodsSet
}

// generatorStep reports a generator's next(), return() or throw() call.
func (o frontOracle) generatorStep(e ast.Expression) bool {
	call, ok := e.(*ast.CallExpression)
	if !ok || call.Optional {
		return false
	}
	m, ok := call.Callee.(*ast.MemberExpression)
	if !ok || m.Optional || (m.Property != "next" && m.Property != "return" && m.Property != "throw") {
		return false
	}
	rt, ok := o.ExprType(m.Object)
	if ok && rt.IsGenerator() {
		return true
	}
	// Any other iterator's step codegen builds as {value, done} (a
	// collection's or a matchAll iterator's).
	st, sok := o.ExprType(call)
	return ok && sok && isGenStepLayout(st)
}

// genericClassDecl reports a symbol declaring a generic class: codegen
// instantiates it per use, and its value is the boxed constructor
// reference like any class's.
func genericClassDecl(sym *binder.Symbol) bool {
	for _, d := range sym.Declarations {
		if cd, ok := d.Node.(*ast.ClassDeclaration); ok && len(cd.DeclaredTypeParams().Names) > 0 {
			return true
		}
	}
	return false
}

// hostIntersection reports an intersection with a host or class member
// (`Error & { port?: number }`): no struct of the merged fields holds it.
func (r *reprCtx) hostIntersection(t *checker.Type) bool {
	for _, m := range t.Intersects {
		// An array carrying properties (`string[] & { tag: string }`) has
		// no fixed shape either.
		if mt, ok := r.of(m); ok && (mt.IsError() || mt.IsClass() || mt.Host != 0 && !mt.IsObject() || mt.IsArray()) {
			return true
		}
	}
	return false
}

// thenStepParam is the type of sym when it is the unannotated first
// parameter of a callback passed straight to `.then` on a generator step's
// promise: the step codegen resolves it with.
func (o frontOracle) thenStepParam(sym *binder.Symbol) (Type, bool) {
	if sym.Scope == nil {
		return Type{}, false
	}
	var fn ast.Node
	var params []ast.Param
	switch f := sym.Scope.Node.(type) {
	case *ast.ArrowFunction:
		fn, params = f, f.Params
	case *ast.FunctionExpression:
		fn, params = f, f.Params
	default:
		return Type{}, false
	}
	if len(params) == 0 || params[0].Name != sym.Name || params[0].Type != nil || params[0].ArrayPattern != nil || params[0].ObjectPattern != nil {
		return Type{}, false
	}
	call, ok := o.c.ParentOf(fn).(*ast.CallExpression)
	if !ok || len(call.Args) == 0 || call.Args[0] != fn {
		return Type{}, false
	}
	m, ok := call.Callee.(*ast.MemberExpression)
	if !ok || m.Property != "then" || !o.generatorStep(m.Object) {
		return Type{}, false
	}
	if pt, ok := o.ExprType(m.Object); ok && pt.PromiseType != nil {
		return *pt.PromiseType, true
	}
	return Type{}, false
}

// stepsOnly reports a binding every write to which stores a generator step
// (`r = it.next()`): it keeps the step's {value, done} layout.
func (o frontOracle) stepsOnly(sym *binder.Symbol) bool {
	if sym.Scope == nil || sym.Scope.Node == nil {
		return false
	}
	only := true
	ast.Inspect(sym.Scope.Node, func(n ast.Node) bool {
		var target ast.Expression
		var value ast.Expression
		switch x := n.(type) {
		case *ast.AssignmentExpression:
			target, value = x.Left, x.Right
			if x.Op != "=" {
				value = nil
			}
		case *ast.UpdateExpression:
			target = x.Arg
		}
		if id, isID := target.(*ast.Identifier); isID {
			if s, _ := o.b.Resolve(id); s == sym && (value == nil || !o.generatorStep(value)) {
				only = false
			}
		}
		return only
	})
	return only
}

// stepsOnlyDecl is stepsOnly for the binding v declares.
func (o frontOracle) stepsOnlyDecl(v *ast.VarDeclaration) bool {
	sym, ok := o.b.DeclSymbol(v)
	return ok && sym != nil && o.stepsOnly(sym)
}

// collIterCall reports a call that may build a collection iterator:
// keys(), values() or entries() of a collection, or a string's matchAll().
func collIterCall(call *ast.CallExpression) bool {
	m, ok := call.Callee.(*ast.MemberExpression)
	if !ok {
		return false
	}
	_, iter := collIterMethod(m.Property)
	return iter || m.Property == "matchAll"
}

// nullAndUndefinedOnly reports the type `null | undefined`.
func nullAndUndefinedOnly(t *checker.Type) bool {
	if t == nil || t.Flags&checker.Union == 0 || len(t.Types) != 2 {
		return false
	}
	n, u := false, false
	for _, m := range t.Types {
		n = n || m.Flags&checker.Null != 0
		u = u || m.Flags&checker.Undefined != 0
	}
	return n && u
}

// synthesizedDecl reports a declaration codegen made, which the binder
// never bound.
func (o frontOracle) synthesizedDecl(v *ast.VarDeclaration) bool {
	if o.b == nil {
		return false
	}
	_, found := o.b.DeclSymbol(v)
	return !found
}

// arrayHeritage is the array a program interface with no members of its
// own is when it extends `Array<T>` or `ReadonlyArray<T>`
// (`interface Ticks extends ReadonlyArray<number> {}`): the base's layout,
// as an annotation naming it resolves.
func (r reprCtx) arrayHeritage(sym *binder.Symbol) (Type, bool) {
	if r.annotation == nil || len(sym.Declarations) != 1 {
		return Type{}, false
	}
	id, ok := sym.Declarations[0].Node.(*ast.InterfaceDeclaration)
	if !ok || len(id.Members) != 0 || len(id.TypeParameters) != 0 || len(id.Heritage) != 1 {
		return Type{}, false
	}
	ref, ok := id.Heritage[0].(*ast.TypeReference)
	if !ok || len(ref.Qualifier) != 0 || (ref.Name != "Array" && ref.Name != "ReadonlyArray") || len(ref.TypeArgs) != 1 {
		return Type{}, false
	}
	ta, err := ast.TypeAnnotationOf(ref, "")
	if err != nil {
		return Type{}, false
	}
	if ty := r.annotation(ta); ty.IsArray() && ty.ElemType != nil {
		return ty, true
	}
	return Type{}, false
}

// annotatedFuncResult reports a function expression or arrow with a return
// annotation: a tuple it declares is the positional struct codegen builds.
func annotatedFuncResult(x ast.Expression) bool {
	switch f := x.(type) {
	case *ast.ArrowFunction:
		return f.RetType != nil
	case *ast.FunctionExpression:
		return f.RetType != nil
	}
	return false
}

// laterVarDecl reports v is a variable declaration of sym after its first
// one (a `var` declared again); a type of the same name is not one.
func laterVarDecl(sym *binder.Symbol, v *ast.VarDeclaration) bool {
	// A program's global belongs to its first top-level declaration, as
	// codegen lays it out; one nested in a block is a store to it.
	if sym.Scope != nil {
		if prog, ok := sym.Scope.Node.(*ast.Program); ok {
			top := map[ast.Node]bool{}
			var add func(st ast.Statement)
			add = func(st ast.Statement) {
				switch x := st.(type) {
				case *ast.VarDeclaration:
					top[x] = true
				case *ast.VarDeclarationList:
					for _, d := range x.Decls {
						top[d] = true
					}
				case *ast.ExportDeclaration:
					add(x.Decl)
				}
			}
			for _, st := range prog.Body {
				add(st)
			}
			for _, d := range sym.Declarations {
				if _, isVar := d.Node.(*ast.VarDeclaration); isVar && top[d.Node] {
					return d.Node != ast.Node(v)
				}
			}
		}
	}
	for _, d := range sym.Declarations {
		if _, isVar := d.Node.(*ast.VarDeclaration); isVar {
			return d.Node != ast.Node(v)
		}
	}
	return false
}

// sameFieldNames reports two object layouts with the same fields in order.
func sameFieldNames(a, b Type) bool {
	if len(a.Fields) != len(b.Fields) {
		return false
	}
	for i := range a.Fields {
		if a.Fields[i].Name != b.Fields[i].Name {
			return false
		}
	}
	return true
}

// topFunction reports a top-level non-generic function declaration with a
// body named name.
// They share readInFunctions' table under a "\x00f" key prefix, filled once.
func (o frontOracle) topFunction(name string) bool {
	if !o.fnReads["\x00f"] {
		o.fnReads["\x00f"] = true // filled
		if o.b != nil && o.b.Module != nil {
			if prog, isProg := o.b.Module.Node.(*ast.Program); isProg {
				for _, st := range prog.Body {
					if fd, ok := st.(*ast.FunctionDeclaration); ok && fd.Body != nil && len(fd.TypeParams) == 0 {
						o.fnReads["\x00f"+fd.Name] = true
					}
				}
			}
		}
	}
	return o.fnReads["\x00f"+name]
}

// readInFunctions reports a top-level name some function body reads
// (namesReadInFunctions, computed once per program), a namespace member, or
// a binding the compiler synthesizes.
func (o frontOracle) readInFunctions(name string) bool {
	// A namespace member is read through its namespace's qualified names
	// (`Config.port`) and by its bare name inside it, and a binding the
	// compiler synthesizes (an enum's value, a dynamic module's export) by
	// the code it generates: each counted as read.
	if strings.Contains(name, ast.NamespaceMangle("", "")) || strings.HasPrefix(name, "__kml_") {
		return true
	}
	if !o.fnReads[""] {
		if o.b != nil && o.b.Module != nil {
			if prog, isProg := o.b.Module.Node.(*ast.Program); isProg {
				for n := range namesReadInFunctions(prog) {
					o.fnReads[n] = true
				}
				if o.r.genPatternInits != nil {
					for _, sts := range o.r.genPatternInits() {
						for _, st := range sts {
							ast.Inspect(st, func(n ast.Node) bool {
								if id, ok := n.(*ast.Identifier); ok {
									o.fnReads[id.Name] = true
								}
								return true
							})
						}
					}
				}
				// The entry's exports, which an importer reads (a
				// dynamic-import island's accessors).
				for _, m := range prog.EntryExportMangled {
					o.fnReads[m] = true
				}
			}
		}
		o.fnReads[""] = true // filled
	}
	return o.fnReads[name]
}

// promiseSubclassNoCtor reports a program class named name that extends
// Promise with no constructor and no instance members: `new` passes its
// executor straight to Promise's.
// promiseSubclassImplicitCtor reports a program class extending Promise
// directly with no constructor and no fields of its own (methods allowed):
// `new C(executor)` hands the executor to Promise's, which passes it the
// resolve and reject functions.
func (o frontOracle) promiseSubclassImplicitCtor(name string) bool {
	prog, ok := o.b.Module.Node.(*ast.Program)
	if !ok {
		return false
	}
	for _, st := range prog.Body {
		if cd, isCD := st.(*ast.ClassDeclaration); isCD && cd.Name == name {
			return cd.BaseClass == "Promise" && !cd.BaseQualified && cd.Constructor == nil && len(cd.Fields) == 0
		}
	}
	return false
}

// erasedCtorCallbackParam is the type of id when it names a parameter of a
// function passed to `new C(…)` for a class whose type parameters codegen
// erased: the matching entry of the constructor parameter's (erased)
// function-type annotation.
func (o frontOracle) erasedCtorCallbackParam(id *ast.Identifier) (Type, bool) {
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return Type{}, false
	}
	fn := sym.Declarations[0].Node
	var params []ast.Param
	switch f := fn.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	default:
		return Type{}, false
	}
	pi := -1
	for i, p := range params {
		if p.Name == id.Name && p.Type == nil {
			pi = i
		}
	}
	ne, ok := o.c.ParentOf(fn).(*ast.NewExpression)
	if pi < 0 || !ok || ne.Qualified {
		return Type{}, false
	}
	prog, isProg := o.b.Module.Node.(*ast.Program)
	if !isProg {
		return Type{}, false
	}
	for _, st := range prog.Body {
		cd, isCD := st.(*ast.ClassDeclaration)
		if !isCD || cd.Name != ne.ClassName {
			continue
		}
		if cd.Declared == nil || cd.Constructor == nil {
			return Type{}, false
		}
		for j, a := range ne.Args {
			if ast.Node(a) != fn || j >= len(cd.Constructor.Params) {
				continue
			}
			ta := cd.Constructor.Params[j].Type
			if ta == nil {
				return Type{}, false
			}
			ft, isFn := ast.NonNullish(ta).(*ast.FunctionType)
			if !isFn || pi >= len(ft.Parameters) {
				return Type{}, false
			}
			fp := ft.Parameters[pi].Type
			fpf, fpFn := ast.NonNullish(fp).(*ast.FunctionType)
			if fp == nil || !fpFn {
				if fp == nil || ast.IsKeywordNode(fp, "any") {
					return TypeAny, true
				}
				return Type{}, false
			}
			// A function-typed parameter (`res: (v: T) => void`): the
			// checker's answer with its erased parameters any.
			bt, ok := o.r.of(o.c.TypeOf(id))
			if !ok || !bt.IsFunc() || len(bt.FuncParams) != len(fpf.Parameters) {
				return Type{}, false
			}
			bt.FuncParams = append([]Type(nil), bt.FuncParams...)
			for k, q := range fpf.Parameters {
				if q.Type == nil || ast.IsKeywordNode(q.Type, "any") {
					bt.FuncParams[k] = TypeAny
				}
			}
			return bt, true
		}
		return Type{}, false
	}
	return Type{}, false
}

// returnsOnlyBoxedParam reports every return of fd's own body a bare read
// of a parameter boxed in params.
func returnsOnlyBoxedParam(fd *ast.FunctionDeclaration, params []Type) bool {
	if fd.Body == nil || fd.IsAsync || fd.IsGenerator {
		return false
	}
	return bodyReturnsOnlyBoxedParam(fd.Body, fd.Params, params)
}

// literalReturnsOnlyBoxedParam is returnsOnlyBoxedParam for an arrow or a
// function expression (an arrow's concise body a parameter alone).
func literalReturnsOnlyBoxedParam(e ast.Expression, params []Type) bool {
	switch f := e.(type) {
	case *ast.ArrowFunction:
		if f.IsAsync {
			return false
		}
		if f.Body != nil {
			id, isID := f.Body.(*ast.Identifier)
			if !isID {
				return false
			}
			for i, p := range f.Params {
				if p.Name == id.Name && i < len(params) && params[i].IsDynamic() {
					return true
				}
			}
			return false
		}
		return f.Block != nil && bodyReturnsOnlyBoxedParam(f.Block, f.Params, params)
	case *ast.FunctionExpression:
		return !f.IsAsync && !f.IsGenerator && f.Body != nil && bodyReturnsOnlyBoxedParam(f.Body, f.Params, params)
	}
	return false
}

// returnsRead reports a concise arrow body, or a return of a function
// literal e, whose expression is a member access or call chain rooted at one
// of names.
func returnsRead(e ast.Expression, names map[string]bool) bool {
	if len(names) == 0 {
		return false
	}
	rooted := func(x ast.Expression) bool {
		for {
			switch n := x.(type) {
			case *ast.Identifier:
				return names[n.Name]
			case *ast.MemberExpression:
				x = n.Object
			case *ast.IndexExpression:
				x = n.Object
			case *ast.CallExpression:
				x = n.Callee
			default:
				return false
			}
		}
	}
	var body ast.Node
	switch f := e.(type) {
	case *ast.ArrowFunction:
		if f.IsAsync {
			return false
		}
		if f.Body != nil {
			return rooted(f.Body)
		}
		body = f.Block
	case *ast.FunctionExpression:
		if f.IsAsync || f.IsGenerator {
			return false
		}
		body = f.Body
	}
	if body == nil {
		return false
	}
	found := false
	ast.Inspect(body, func(x ast.Node) bool {
		switch r := x.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration:
			return false
		case *ast.ReturnStatement:
			found = found || r.Value != nil && rooted(r.Value)
		}
		return true
	})
	return found
}

func bodyReturnsOnlyBoxedParam(body ast.Node, fparams []ast.Param, params []Type) bool {
	n := 0
	ok := true
	ast.Inspect(body, func(x ast.Node) bool {
		switch r := x.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration:
			return false
		case *ast.ReturnStatement:
			n++
			id, isID := r.Value.(*ast.Identifier)
			if !isID {
				ok = false
				return false
			}
			found := false
			for i, p := range fparams {
				if p.Name == id.Name && i < len(params) && params[i].IsDynamic() {
					found = true
				}
			}
			ok = ok && found
		}
		return true
	})
	return ok && n > 0
}

// readsOwnBinding reports a read of v's own binding inside a function in
// its initializer lit.
func (o frontOracle) readsOwnBinding(v *ast.VarDeclaration, lit *ast.ObjectLiteral) bool {
	found := false
	var walk func(n ast.Node, inFn bool) bool
	walk = func(n ast.Node, inFn bool) bool {
		ast.Inspect(n, func(x ast.Node) bool {
			if found {
				return false
			}
			switch f := x.(type) {
			case *ast.ArrowFunction, *ast.FunctionExpression:
				if !inFn && x != n {
					walk(f, true)
					return false
				}
			case *ast.Identifier:
				if inFn && f.Name == v.Name {
					if sym, _ := o.b.Resolve(f); sym != nil && len(sym.Declarations) > 0 && sym.Declarations[0].Node == ast.Node(v) {
						found = true
					}
				}
			}
			return true
		})
		return found
	}
	return walk(lit, false)
}

// literalBindingType is the checker's type of v's binding (the widened
// literal its reads have), represented.
func (o frontOracle) literalBindingType(v *ast.VarDeclaration) (Type, bool) {
	ct := o.c.VarType(v)
	if ct == nil || o.c.Unanswered(ct) {
		return Type{}, false
	}
	return o.r.of(ct)
}

// genericClass reports a program class named name with type parameters.
func (o frontOracle) genericClass(name string) bool {
	prog, ok := o.b.Module.Node.(*ast.Program)
	if !ok {
		return false
	}
	for _, st := range prog.Body {
		if cd, isCD := st.(*ast.ClassDeclaration); isCD && cd.Name == name {
			return len(cd.TypeParams) > 0 || cd.Declared != nil // (erased: Declared keeps them)
		}
	}
	return false
}

func (o frontOracle) promiseSubclassNoCtor(name string) bool {
	prog, ok := o.b.Module.Node.(*ast.Program)
	if !ok {
		return false
	}
	for _, st := range prog.Body {
		if cd, isCD := st.(*ast.ClassDeclaration); isCD && cd.Name == name {
			if cd.BaseClass != "Promise" || cd.BaseQualified || cd.Constructor != nil || len(cd.Fields) > 0 {
				return false
			}
			for _, m := range cd.Methods {
				if !m.IsStatic {
					return false // an instance method: a class of its own
				}
			}
			return true
		}
	}
	return false
}

// emptyObjectRest reports an object pattern's rest binding (`{ a, ...rest }`)
// over a source whose every property the pattern names: the checker's `{}`.
func (o frontOracle) emptyObjectRest(id *ast.Identifier) bool {
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return false
	}
	d, ok := sym.Declarations[0].Node.(*ast.ObjectDestructuring)
	if !ok {
		return false
	}
	for _, p := range d.Props {
		if p.Rest && p.Local == id.Name {
			t := o.c.TypeOf(id)
			return !o.c.Unanswered(t) && t.Flags&checker.Object != 0 && t.Kind == checker.Anonymous && len(t.Props) == 0 && len(t.Intersects) == 0 && t.StringIndex == nil && t.NumberIndex == nil
		}
	}
	return false
}

// byValReturn is a read of a function declaration -optimize-memory returns
// its tuple from by value: that ABI on its result.
func (o frontOracle) byValReturn(e ast.Expression, t Type, ok bool) Type {
	id, isID := e.(*ast.Identifier)
	if !isID || !ok || o.r.byValRet == nil || !t.IsFunc() || t.FuncRetType == nil || !t.FuncRetType.IsTuple() || t.FuncRetType.TupleByVal {
		return t
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 || sym.Scope == nil || sym.Scope.Kind != binder.ModuleScope {
		return t
	}
	if _, fd := sym.Declarations[0].Node.(*ast.FunctionDeclaration); !fd || !o.r.byValRet(id.Name) {
		return t
	}
	rt := *t.FuncRetType
	rt.TupleByVal = true
	t.FuncRetType = &rt
	return t
}

// returnsOwnThis reports a body whose every return returns `this`.
func returnsOwnThis(body *ast.BlockStatement) bool {
	if body == nil {
		return false
	}
	n, own := 0, true
	ast.Inspect(body, func(x ast.Node) bool {
		switch r := x.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		case *ast.ReturnStatement:
			n++
			if _, ok := r.Value.(*ast.ThisExpression); !ok {
				own = false
			}
		}
		return true
	})
	return n > 0 && own
}

// inFunction reports a node inside a function or class body.
func inFunction(c frontView, n ast.Node) bool {
	for p := c.ParentOf(n); p != nil; p = c.ParentOf(p) {
		switch p.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration, *ast.ClassExpression:
			return true
		}
	}
	return false
}

// sameSeedRows reports a collection seed whose rows all hold rt.
func sameSeedRows(o frontOracle, seed *ast.ArrayLiteral, rt Type) bool {
	for _, el := range seed.Elements[1:] {
		row, isRow := el.(*ast.ArrayLiteral)
		if !isRow {
			return false
		}
		t, ok := o.ExprType(row)
		if !ok || reprKey(t) != reprKey(rt) {
			return false
		}
	}
	return true
}

// boxedRestSource reports a rest binding whose pattern's source is held as a
// box (a Proxy): its rest is read off the box, a box too.
func (o frontOracle) boxedRestSource(id *ast.Identifier) bool {
	sym, _ := o.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return false
	}
	d, ok := sym.Declarations[0].Node.(*ast.ObjectDestructuring)
	if !ok || d.Init == nil {
		return false
	}
	st, sok := o.ExprType(d.Init)
	return sok && isUnconstrainedDynamic(st)
}

// proxyValue reports a `new Proxy(...)` expression, or a const binding of
// one: held boxed (emitNewProxy).
func (o frontOracle) proxyValue(x ast.Expression) bool {
	if id, isID := x.(*ast.Identifier); isID {
		sym, _ := o.b.Resolve(id)
		if sym == nil {
			return false
		}
		v, isV := singleVarDecl(sym)
		if !isV || v.Kind != "const" || v.Init == nil {
			return false
		}
		x = v.Init
	}
	nx, isNew := x.(*ast.NewExpression)
	if !isNew || nx.Callee != nil || nx.ClassName != "Proxy" || nx.Qualified {
		return false
	}
	t, ok := o.ExprType(nx)
	return ok && isUnconstrainedDynamic(t)
}

// callInitialized reports a variable whose one declaration is initialized
// by a call (`const C = make()`).
func (o frontOracle) callInitialized(x ast.Expression) bool {
	id, isID := x.(*ast.Identifier)
	if !isID {
		return false
	}
	sym, _ := o.b.Resolve(id)
	if sym == nil {
		return false
	}
	v, isV := singleVarDecl(sym)
	if !isV {
		return false
	}
	_, isCall := v.Init.(*ast.CallExpression)
	return isCall
}

// LocalType is a function local's declared type: an unannotated binding of
// an expression holds that expression's value as it is emitted (an element
// read widened to a number), and every other declaration its declared type
// (GlobalType).
func (o frontOracle) LocalType(v *ast.VarDeclaration) (Type, bool) {
	if accessorLiteral(o.b, v) {
		return TypeAny, true // a dynamic object, whose reads call its accessors
	}
	// A binding widened across kinds, or of a call the any-argument check
	// boxes, holds the box.
	if v.TypeAnnot == nil && o.r.widened != nil && o.r.widened(v.Name) || o.r.anyHeld != nil && o.r.anyHeld(v) {
		return TypeAny, true
	}
	if o.varSharesFunction(v) {
		// One binding with a function declaration: the function until the
		// `var` runs, then its value.
		return TypeAny, true
	}
	switch v.Init.(type) {
	case *ast.NewWeakMapExpression, *ast.NewWeakSetExpression:
		// A bare weak collection: the key type codegen seeds from its
		// first use, as a global's (bareCollectionType).
		if o.r.bareCollection != nil {
			if t, ok := o.r.bareCollection(v); ok {
				return t, true
			}
		}
	}
	if v.TypeAnnot == nil && v.Init != nil {
		switch init := v.Init.(type) {
		case *ast.NullLiteral:
			// A binding only ever null (or undefined) holds that value.
			if init.IsUndefined {
				return TypeUndefined, true
			}
			return TypeNull, true
		case *ast.IndexExpression:
			if it, ok := o.ExprType(init); ok {
				return elementBindingType(it), true
			}
		case *ast.BooleanLiteral, *ast.StringLiteral, *ast.TemplateLiteral,
			*ast.NumberLiteral, *ast.ArrayLiteral, *ast.ObjectLiteral, *ast.NewArrayExpression,
			*ast.NewMapExpression, *ast.NewSetExpression:
		default:
			// A call the any-argument dispatch runs holds its box.
			if o.r.boxOverride != nil {
				if bt, ok := o.r.boxOverride(init); ok {
					return bt, true
				}
			}
			if it, ok := o.ExprType(init); ok {
				// A call answered `any` (an implementation signature behind
				// overloads) whose overload resolves to a primitive is held
				// as that primitive.
				if call, isCall := init.(*ast.CallExpression); isCall && isUnconstrainedDynamic(it) && it.DynPropTy == nil && o.r.checkerPrim != nil {
					if pt, ok := o.r.checkerPrim(call); ok {
						return pt, true
					}
				}
				// A void result (`await tick()`) holds undefined.
				if it.IR == "void" {
					return TypeUndefined, true
				}
				return it, true
			}
		}
	}
	ty, _, ok := o.GlobalType(v)
	return ty, ok && ty.IR != ""
}

// mergeTaskPromises is one task promise for parts, every one a task
// promise of the same payload but for the absences it holds.
func mergeTaskPromises(parts []Type) (Type, bool) {
	var out Type
	for i, p := range parts {
		if !p.IsPromise() || !p.PromiseTask || p.PromiseType == nil {
			return Type{}, false
		}
		in := *p.PromiseType
		if i == 0 {
			out = p
			continue
		}
		cur := *out.PromiseType
		a, b := cur, in
		a.Absent, b.Absent = 0, 0
		if reprKey(a) != reprKey(b) {
			return Type{}, false
		}
		switch {
		case cur.NullAndUndef() || in.NullAndUndef(),
			(cur.Nullable() || in.Nullable()) && cur.IsUndefined() != in.IsUndefined():
			if !threeStateEligible(a) {
				return Type{}, false
			}
			m := withNullAndUndef(a)
			out.PromiseType = &m
		case in.Nullable() && !cur.Nullable():
			m := in
			out.PromiseType = &m
		}
	}
	return out, true
}

// plainLiteral reports a primitive literal or an array literal of them,
// never typed as a box (a large constant table's rows).
func plainLiteral(e ast.Expression) bool {
	switch x := e.(type) {
	case *ast.StringLiteral, *ast.NumberLiteral, *ast.BooleanLiteral:
		return true
	case *ast.ArrayLiteral:
		for _, el := range x.Elements {
			if !plainLiteral(el) {
				return false
			}
		}
		return true
	}
	return false
}

// declaresThis reports a function declaring a `this: T` parameter.
func (o frontOracle) declaresThis(f ast.Node) bool {
	prog, ok := o.b.Module.Node.(*ast.Program)
	return ok && prog.ThisParams[f] != nil
}

// sameFunctionDecls reports ds as one function's declarations, all of one
// name, with any namespace merged into it (`exec.__promisify__`).
func sameFunctionDecls(ds []binder.Declaration) bool {
	name := ""
	for _, d := range ds {
		if d.Kind == binder.NamespaceDecl {
			continue
		}
		fd, ok := d.Node.(*ast.FunctionDeclaration)
		if !ok || (name != "" && fd.Name != name) {
			return false
		}
		name = fd.Name
	}
	return true
}

// functionDeclOf is the function declaration among ds.
func functionDeclOf(ds []binder.Declaration) (*ast.FunctionDeclaration, bool) {
	for i := len(ds) - 1; i >= 0; i-- {
		if fd, ok := ds[i].Node.(*ast.FunctionDeclaration); ok {
			return fd, true
		}
	}
	return nil, false
}

// bareTypeRefName is the name of an unqualified type reference without type
// arguments, or of an array of one (`T`, `T[]`).
func bareTypeRefName(n ast.TypeNode) (string, bool) {
	if a, ok := n.(*ast.ArrayType); ok {
		n = a.ElementType
	}
	if r, ok := n.(*ast.TypeReference); ok && len(r.Qualifier) == 0 && len(r.TypeArgs) == 0 {
		return r.Name, true
	}
	return "", false
}

// erasedTypeParamNames are the names a generic class, function, method or
// arrow of the program declares as type parameters: erased in a body
// compiled outside any instantiation.
func (e *Emitter) erasedTypeParamNames() []string {
	if e.classTPNames == nil && e.prog != nil {
		seen := map[string]bool{}
		ast.Inspect(e.prog, func(n ast.Node) bool {
			var names []string
			switch d := n.(type) {
			case *ast.ClassDeclaration:
				// (An erased class's are its declared ones.)
				names = d.DeclaredTypeParams().Names
			case *ast.FunctionDeclaration:
				// A generic function's or method's, outside an
				// instantiation (which binds them instead).
				names = d.TypeParams
			case *ast.ArrowFunction:
				names = d.TypeParams // erased to any
			case *ast.FunctionExpression:
				names = d.TypeParams
			case *ast.InterfaceDeclaration:
				// A generic interface's, and its methods' own, in a
				// member read outside an instantiation (`emit<E extends
				// keyof M>(…args: Args<M[E]>)`).
				names = append(names, d.TypeParams...)
				for _, m := range d.Members {
					if ms, ok := m.(*ast.MethodSignature); ok {
						for _, tp := range ms.TypeParameters {
							names = append(names, tp.Name)
						}
					}
				}
			}
			for _, tp := range names {
				if !seen[tp] {
					seen[tp] = true
					e.classTPNames = append(e.classTPNames, tp)
				}
			}
			return true
		})
		// A builtin interface method's own (`addEventListener<K extends
		// keyof AbortSignalEventMap>`), its members laid out once.
		if libProgs, err := lib.Programs(); err == nil {
			for _, lp := range libProgs {
				for _, st := range lp.Body {
					id, ok := st.(*ast.InterfaceDeclaration)
					if !ok {
						continue
					}
					for _, m := range id.Members {
						ms, ok := m.(*ast.MethodSignature)
						if !ok {
							continue
						}
						for _, tp := range ms.TypeParameters {
							if !seen[tp.Name] {
								seen[tp.Name] = true
								e.classTPNames = append(e.classTPNames, tp.Name)
							}
						}
					}
				}
			}
		}
		if e.classTPNames == nil {
			e.classTPNames = []string{}
		}
	}
	return e.classTPNames
}

// globalLinkedClass is the instance type of the module class a global
// interface named name links to.
func (e *Emitter) globalLinkedClass(name string) (Type, bool) {
	m, ok := e.globalLinks[name]
	if !ok || e.userTypeName(name) || e.front() == nil || e.frontBinding == nil || e.frontBinding.Module == nil {
		return Type{}, false
	}
	sym := e.frontBinding.Module.Symbols.Get(m)
	if sym == nil {
		return Type{}, false
	}
	return e.classRepr(sym)
}

// erasedIndexAccess is n with each access indexed by an erased type
// parameter (`M[K]`) read over every key of its object (`M[keyof M]`), or n
// itself when it has none. The nodes it rewrites are copies.
func erasedIndexAccess(n ast.TypeNode, erased map[string]bool) ast.TypeNode {
	switch x := n.(type) {
	case *ast.IndexedAccessType:
		if name, ok := bareTypeRefName(x.IndexType); ok && erased[name] {
			if _, isArr := x.IndexType.(*ast.ArrayType); !isArr {
				return &ast.IndexedAccessType{ObjectType: x.ObjectType, IndexType: &ast.TypeOperator{Operator: "keyof", Type: x.ObjectType, Range: x.Range}, Range: x.Range}
			}
		}
	case *ast.FunctionType:
		var ps []*ast.SignatureParameter
		for i, p := range x.Parameters {
			if np := erasedIndexAccess(p.Type, erased); np != p.Type {
				if ps == nil {
					ps = append([]*ast.SignatureParameter(nil), x.Parameters...)
				}
				cp := *p
				cp.Type = np
				ps[i] = &cp
			}
		}
		if ps != nil {
			cp := *x
			cp.Parameters = ps
			return &cp
		}
	case *ast.UnionType:
		var ms []ast.TypeNode
		for i, m := range x.Types {
			if nm := erasedIndexAccess(m, erased); nm != m {
				if ms == nil {
					ms = append([]ast.TypeNode(nil), x.Types...)
				}
				ms[i] = nm
			}
		}
		if ms != nil {
			cp := *x
			cp.Types = ms
			return &cp
		}
	}
	return n
}

// callableInterface reports an interface type declared with a call
// signature (`interface EventListener { (evt: Event): void }`).
func callableInterface(t *checker.Type) bool {
	if t.Flags&checker.Object == 0 || t.Kind != checker.Interface || t.Symbol == nil {
		return false
	}
	for _, d := range t.Symbol.Declarations {
		if id, ok := d.Node.(*ast.InterfaceDeclaration); ok && id.CallSig != nil {
			return true
		}
	}
	return false
}

// typeNodeOf is a type node naming codegen type t, for the primitive,
// promise, array and plain object shapes a binding needs (`T` = `Promise<number>` of
// `u(Promise.resolve(3))`), else nil.
func typeNodeOf(t Type) ast.TypeNode {
	if t.Nullable() {
		return nil
	}
	switch {
	case t.IsPromise() && t.PromiseType != nil:
		if in := typeNodeOf(*t.PromiseType); in != nil {
			return &ast.TypeReference{Name: "Promise", TypeArgs: []ast.TypeNode{in}}
		}
	case t.IsArray() && t.ElemType != nil && !t.IsTypedArray() && !t.IsBuffer() && !t.IsTuple():
		if in := typeNodeOf(*t.ElemType); in != nil {
			return &ast.ArrayType{ElementType: in}
		}
	case t.Kind == kindClass && t.ClassName != "":
		return &ast.TypeReference{Name: t.ClassName} // a class's instance
	case t.IsTuple():
		// A tuple's elements (`A` of `same([1, "a"])`).
		es := make([]ast.TypeNode, len(t.Fields))
		for i, f := range t.Fields {
			if es[i] = typeNodeOf(f.Ty); es[i] == nil {
				return nil
			}
		}
		return &ast.TupleType{Elements: es}
	case t.IsObject() && !t.IsDynamic() && !t.IsClass() && t.RefName == "" && t.Host == 0 && len(t.Fields) > 0:
		// An object literal's layout (`T` of `r({ a: 1 })`).
		var ms []ast.TypeMember
		for _, f := range t.Fields {
			if strings.HasPrefix(f.Name, "__kml_") {
				continue
			}
			ft := f.Ty
			if f.Optional {
				ft = ft.withoutNullable()
			}
			in := typeNodeOf(ft)
			if in == nil {
				return nil
			}
			ms = append(ms, &ast.PropertySignature{Name: f.Name, Optional: f.Optional, Type: in})
		}
		return &ast.TypeLiteral{Members: ms}
	case t.IsBigInt():
		return &ast.KeywordType{Keyword: "bigint"}
	case t.Kind != kindNone || t.Host != 0:
	case t.IR == "double" && t.Float:
		return &ast.KeywordType{Keyword: "number"}
	case t.IR == "i1":
		return &ast.KeywordType{Keyword: "boolean"}
	case t.IR == irPtr && isStringTy(t) && !hasHandleFlag(t):
		return &ast.KeywordType{Keyword: "string"}
	}
	return nil
}

// importIntersection reports an intersection one member of which is an
// `import("m").T` type.
func importIntersection(n ast.TypeNode) bool {
	it, ok := n.(*ast.IntersectionType)
	if !ok {
		return false
	}
	for _, m := range it.Types {
		if _, imp := m.(*ast.ImportType); imp {
			return true
		}
	}
	return false
}

// importAlias reports a reference to an alias of an importIntersection
// (`WriteStream`, of `import("tty").WriteStream & { … }`).
func (o frontOracle) importAlias(n ast.TypeNode) bool {
	r, ok := n.(*ast.TypeReference)
	if !ok || len(r.TypeArgs) > 0 || len(r.Qualifier) > 0 {
		return false
	}
	sc := o.b.AnnotationScope(n)
	if sc == nil {
		sc = o.b.Module
	}
	for s := sc; s != nil; s = s.Parent {
		sym := s.Symbols.Get(r.Name)
		if sym == nil {
			continue
		}
		for _, d := range sym.Declarations {
			if ad, ok := d.Node.(*ast.TypeAliasDeclaration); ok && ad.Type != nil && importIntersection(ad.Type) {
				return true
			}
		}
		return false
	}
	return false
}

// hasConditional reports a conditional type anywhere in n.
func hasConditional(n ast.TypeNode) bool {
	found := false
	ast.Inspect(n, func(x ast.Node) bool {
		if _, ok := x.(*ast.ConditionalType); ok {
			found = true
		}
		return !found
	})
	return found
}

// mentionsThisType reports a type node naming the `this` type.
func mentionsThisType(n ast.TypeNode) bool {
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		if tn, ok := m.(ast.TypeNode); ok && ast.IsKeywordNode(tn, "this") {
			found = true
		}
		return !found
	})
	return found
}

// erasedDeferred reports a deferred type waiting on a type parameter the
// emission has an answer for: one codegen erased to any, or one the
// instantiation being emitted binds. The checker evaluates it with
// neither, so its value is held boxed.
func (r reprCtx) erasedDeferred(t *checker.Type) bool {
	for _, tp := range t.DeferredTypeParams() {
		if _, ok := r.of(tp); ok {
			return true
		}
	}
	return false
}

// splicedTuple is tuple annotation tt with each spread of a type parameter
// bound to a tuple replaced by its elements; false when some spread is
// anything else or an element is not laid out.
func (o frontOracle) splicedTuple(tt *ast.TupleType) (Type, bool) {
	if o.r.typeParam == nil {
		return Type{}, false
	}
	var elems []Type
	spliced := false
	for _, el := range tt.Elements {
		var spread ast.TypeNode
		switch m := el.(type) {
		case *ast.RestType:
			spread = m.Type
		case *ast.NamedTupleMember:
			if m.Optional {
				return Type{}, false
			}
			if m.Rest {
				spread = m.Type
			} else {
				el = m.Type
			}
		case *ast.OptionalType:
			return Type{}, false
		}
		if spread != nil {
			name, ok := bareTypeRefName(spread)
			if !ok {
				return Type{}, false
			}
			bound, ok := o.r.typeParam(name)
			if !ok || !bound.IsTuple() {
				return Type{}, false
			}
			for _, f := range bound.Fields {
				elems = append(elems, f.Ty)
			}
			spliced = true
			continue
		}
		t, ok := o.AnnotationType(el)
		if !ok {
			return Type{}, false
		}
		elems = append(elems, t)
	}
	if !spliced {
		return Type{}, false
	}
	return TupleType(elems), true
}

// varSharesFunction reports whether `var` v is one binding with a function
// declaration of its scope (`var d = 1; function d() {}`).
func (o frontOracle) varSharesFunction(v *ast.VarDeclaration) bool {
	if v.Kind != "var" || v.TypeAnnot != nil || o.b == nil {
		return false
	}
	sym, ok := o.b.DeclSymbol(v)
	if !ok || sym == nil {
		return false
	}
	for _, d := range sym.Declarations {
		if _, fn := d.Node.(*ast.FunctionDeclaration); fn {
			return true
		}
	}
	return false
}

// accessorTargets is the declarations of the bindings the program defines
// an accessor property on by name: the first argument of
// Object.defineProperty / Reflect.defineProperty with a descriptor literal
// naming get or set, or of Object.defineProperties with such a descriptor.
func accessorTargets(b *binder.Binding) map[*ast.VarDeclaration]bool {
	if b == nil || b.Program == nil {
		return nil
	}
	if m, ok := b.Memo.Load("accessorTargets"); ok {
		return m.(map[*ast.VarDeclaration]bool)
	}
	out := map[*ast.VarDeclaration]bool{}
	accessorDefinitions(b.Program, func(c *ast.CallExpression, _ string) {
		if id, ok := c.Args[0].(*ast.Identifier); ok {
			if sym, _ := b.Resolve(id); sym != nil {
				for _, d := range sym.Declarations {
					if v, ok := d.Node.(*ast.VarDeclaration); ok {
						out[v] = true
					}
				}
			}
		}
	})
	b.Memo.Store("accessorTargets", out)
	return out
}

// accessorLiteral reports v, an unannotated binding of an object literal
// or a class instance, as a target accessorTargets lists.
func accessorLiteral(b *binder.Binding, v *ast.VarDeclaration) bool {
	switch v.Init.(type) {
	case *ast.ObjectLiteral, *ast.NewExpression:
		return v.TypeAnnot == nil && accessorTargets(b)[v]
	}
	return false
}

// blockFunctionNames is the names of prog's plain (not generator or async)
// function declarations nested in a block, which Annex B also binds as a
// var of the enclosing function, and of the vars assigned such a function.
func blockFunctionNames(b *binder.Binding) map[string]bool {
	if b == nil || b.Program == nil {
		return nil
	}
	prog := b.Program
	if m, ok := b.Memo.Load("blockFunctionNames"); ok {
		return m.(map[string]bool)
	}
	out := map[string]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BlockStatement:
			for _, st := range x.Body {
				if fd, ok := st.(*ast.FunctionDeclaration); ok && !fd.IsGenerator && !fd.IsAsync {
					out[fd.Name] = true
				}
			}
		case *ast.AssignmentExpression:
			// (The resolver's form of one: the var assigned the function.)
			if id, ok := x.Left.(*ast.Identifier); ok && x.Op == "=" {
				if fe, ok := x.Right.(*ast.FunctionExpression); ok && !fe.IsGenerator && !fe.IsAsync {
					out[id.Name] = true
				}
			}
		}
		return true
	})
	b.Memo.Store("blockFunctionNames", out)
	return out
}
