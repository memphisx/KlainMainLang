package checker

import (
	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"strconv"
	"strings"
)

// narrowedType is the type of a reference to sym at id: its declared type,
// narrowed along the flow paths that reach id by the assignments to sym and
// the conditions tested on it (TypeScript's control-flow narrowing). A union,
// unknown, an evolving let or a class instance (to a subclass) narrows, and
// only in the container that declares sym.
func (c *Checker) narrowedType(id *ast.Identifier, sym *binder.Symbol, declared *Type) *Type {
	c.storage[id] = declared
	if !narrowable(declared) && declared.Flags&Any == 0 && !c.evolving[sym] {
		return declared
	}
	f, local := c.b.FlowAt(id)
	r := ref{sym: sym}
	if !local {
		// A closure sees the binding as narrowed where it was created when
		// nothing can change it afterwards; otherwise as declared at its
		// start, and narrowed along its own flow from there. A builtin
		// declaration (`Math`) reads as declared: it is no program binding,
		// and walking the program's flow for every read of one exhausts the
		// flow budget.
		if f == nil || c.evolving[sym] || c.inLibrary(sym.Scope) {
			return declared
		}
		r.outer = c.b.ClosureNarrows(id, sym)
	}
	t, _ := c.flowType(f, r, declared, newFlowWalk())
	return t
}

// flowKey is a flow cache entry: the type reference r, declared declared,
// has at a branch or loop label (TypeScript's flow type cache, which keeps
// narrowing linear where each read would otherwise walk every path back).
type flowKey struct {
	f        *binder.FlowNode
	sym      *binder.Symbol
	path     string
	declared *Type
	outer    bool
}

func joinPath(p []string) string {
	s := ""
	for _, x := range p {
		s += "\x00" + x
	}
	return s
}

// narrowable reports whether a reference of declared type t can narrow: a
// union, unknown, boolean (true | false), a primitive or a literal (to
// never), or an object type (to a subtype, by instanceof or a type
// predicate).
func narrowable(t *Type) bool {
	return t.Flags&(Union|Unknown|Boolean|Literal|String|Number|BigInt|ESSymbol|Int|Float32) != 0 ||
		t.Flags&Object != 0 && (t.Kind == Instance || t.Kind == Interface || t.Kind == Anonymous)
}

// ref is a narrowable reference: a symbol, or a property path on it
// (`x.a.b` is sym x, path [a b]).
type ref struct {
	sym  *binder.Symbol
	path []string
	// outer continues the walk out of the closures a read is in, up to the
	// binding's own function (binder.ClosureNarrows).
	outer bool
}

// refOf returns the reference e denotes: an identifier, or a chain of
// property accesses on one (`x.a` and `x["a"]` are the same reference; an
// element access needs a literal key).
func (c *Checker) refOf(e ast.Expression) (ref, ast.Expression, bool) {
	return c.refOfExpr(e, false)
}

// refOfExpr is refOf, looking through a comma (`(f(), x).a` is x.a) and a
// non-null assertion; with assigned, through an assignment to its target
// too, as a condition's reference is matched (tsc's isMatchingReference:
// `(o = f()).done` tests o.done).
func (c *Checker) refOfExpr(e ast.Expression, assigned bool) (ref, ast.Expression, bool) {
	var path []string
	for {
		switch x := e.(type) {
		case *ast.SequenceExpression:
			if len(x.Exprs) == 0 {
				return ref{}, nil, false
			}
			e = x.Exprs[len(x.Exprs)-1]
			continue
		case *ast.BinaryExpression:
			if x.Op != "," {
				return ref{}, nil, false
			}
			e = x.Right
			continue
		case *ast.NonNullExpression:
			e = x.Arg
			continue
		case *ast.AssignmentExpression:
			if !assigned {
				return ref{}, nil, false
			}
			e = x.Left
			continue
		case *ast.MemberExpression:
			path = append(path, x.Property)
			e = x.Object
			continue
		case *ast.IndexExpression:
			key, ok := c.accessKey(x.Index)
			if !ok {
				return ref{}, nil, false
			}
			path = append(path, key)
			e = x.Object
			continue
		case *ast.Identifier:
			sym, ok := c.b.Resolve(x)
			if !ok || sym == nil {
				return ref{}, nil, false
			}
			for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
				path[i], path[j] = path[j], path[i]
			}
			return ref{sym: sym, path: path}, x, true
		case *ast.ThisExpression:
			_, sym, _ := c.b.ThisAt(x)
			if sym == nil {
				return ref{}, nil, false
			}
			for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
				path[i], path[j] = path[j], path[i]
			}
			return ref{sym: sym, path: path}, x, true
		case *ast.SuperExpression:
			// `super.m`: the base's member on this receiver, apart from `this.m`.
			_, sym, _ := c.b.SuperAt(x)
			if sym == nil || len(path) == 0 {
				return ref{}, nil, false
			}
			for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
				path[i], path[j] = path[j], path[i]
			}
			return ref{sym: sym, path: append([]string{"\x00super"}, path...)}, x, true
		}
		return ref{}, nil, false
	}
}

// refersTo reports whether e denotes r.
func (c *Checker) refersTo(e ast.Expression, r ref) bool {
	// A condition on an assignment tests its target, `(m = re.exec(s)) !=
	// null` (TypeScript's getReferenceCandidate).
	er, _, ok := c.refOfExpr(e, true)
	return ok && er.sym == r.sym && samePath(er.path, r.path)
}

// prefixOf reports whether e denotes a proper prefix of r's path: storing to
// it replaces what r reads.
func (c *Checker) prefixOf(e ast.Expression, r ref) bool {
	er, _, ok := c.refOf(e)
	return ok && er.sym == r.sym && len(er.path) < len(r.path) && samePath(er.path, r.path[:len(er.path)])
}

func samePath(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// literalKey is the property key a literal index denotes.
func literalKey(e ast.Expression) (string, bool) {
	switch k := e.(type) {
	case *ast.StringLiteral:
		return k.Value, true
	case *ast.NumberLiteral:
		if !k.IsBigInt {
			return canonicalNumber(k.Value), true
		}
	case *ast.TemplateLiteral:
		if len(k.Exprs) == 0 {
			return strings.Join(k.Quasis, ""), true
		}
	}
	return "", false
}

// narrowedMember is the type of a property path read e whose declared
// type is declared, narrowed like a variable: by conditions on the same path
// and stores to it, reset by a store to its root or a shorter prefix.
func (c *Checker) narrowedMember(e ast.Expression, declared *Type) *Type {
	c.storage[e] = declared
	if !narrowable(declared) {
		return declared
	}
	r, root, ok := c.refOf(e)
	if !ok {
		return declared
	}
	f, local := c.flowAtRoot(root)
	if !local && !c.closureFlow(root, f) {
		return declared
	}
	t, _ := c.flowType(f, r, declared, newFlowWalk())
	return t
}

// closureFlow reports whether a reference whose root is not its function's
// own (a captured binding, an arrow function's `this`) narrows along f, the
// closure's own flow, from its declared type at the closure's start — as a
// captured name does (narrowedType). A builtin declaration's binding reads
// as declared.
func (c *Checker) closureFlow(root ast.Expression, f *binder.FlowNode) bool {
	if f == nil {
		return false
	}
	if id, ok := root.(*ast.Identifier); ok {
		sym, ok := c.b.Resolve(id)
		return ok && sym != nil && !c.evolving[sym] && !c.inLibrary(sym.Scope)
	}
	return true
}

// flowBudget bounds the flow steps one outermost TypeOf query takes, as
// TypeScript bounds its control-flow analysis (TS2563): past it the query
// is unanswered, never guessed.
const flowBudget = 500000

// flowWalk is one backward walk's state: the labels on the current path,
// and the answers the walk already has at labels, each with the labels on
// the path it was cut at.
type flowWalk struct {
	onPath map[*binder.FlowNode]bool
	memo   map[*binder.FlowNode]flowMemo
}

type flowMemo struct {
	t    *Type
	deps []*binder.FlowNode
}

func newFlowWalk() *flowWalk {
	return &flowWalk{onPath: map[*binder.FlowNode]bool{}, memo: map[*binder.FlowNode]flowMemo{}}
}

// flowType walks back from f. A path through a loop back into the current
// walk contributes nothing (the least fixed point: a loop adds only what it
// assigns); such an answer is cut at that loop's label, and depends on it
// (deps). An answer with no deps is complete.
func (c *Checker) flowType(f *binder.FlowNode, r ref, declared *Type, w *flowWalk) (*Type, []*binder.FlowNode) {
	for {
		if c.steps++; c.steps > flowBudget || c.exhausted {
			c.exhausted = true
			return c.unanswered, nil
		}
		switch {
		case f == nil:
			return declared, nil
		case f.Flags&binder.FlowUnreachable != 0:
			return c.neverT, nil
		case f.Flags&binder.FlowAssignment != 0:
			if f.Symbol != r.sym {
				// `for (const k in ref)` acts as a non-null assertion on ref
				// (tsc's getTypeAtFlowAssignment).
				if fi, ok := f.Node.(*ast.ForInStatement); ok && (c.refersTo(fi.Object, r) || c.optionalChainContains(fi.Object, r)) {
					t, deps := c.flowType(f.Antecedent, r, declared, w)
					return c.nonNullable(t), deps
				}
				f = f.Antecedent
				continue
			}
			if len(r.path) > 0 {
				return declared, nil // the root was replaced
			}
			at := c.assignedType(f, declared)
			if _, pattern := f.Node.(*ast.Identifier); pattern && c.Unanswered(at) {
				return at, nil // a destructured value the checker cannot type
			}
			if a, ok := f.Node.(*ast.AssignmentExpression); ok && compoundLike(a) {
				declared = widen(c, declared) // `x = x + 1`: x's base type
			}
			return c.assignmentReduced(declared, at), nil
		case f.Flags&binder.FlowPropertyAssignment != 0:
			a := f.Node.(*ast.AssignmentExpression)
			switch {
			case len(r.path) > 0 && c.refersTo(a.Left, r):
				return c.assignmentReduced(declared, c.assignedType(f, declared)), nil
			case len(r.path) > 0 && c.prefixOf(a.Left, r):
				return declared, nil
			}
			f = f.Antecedent
			continue
		case f.Flags&binder.FlowCall != 0:
			call := f.Node.(*ast.CallExpression)
			if c.neverReturning(call) {
				return c.neverT, nil // no path continues past it
			}
			t, deps := c.flowType(f.Antecedent, r, declared, w)
			return c.narrowByAssertion(t, r, call), deps
		case f.Flags&binder.FlowSwitchClause != 0:
			t, deps := c.flowType(f.Antecedent, r, declared, w)
			return c.narrowBySwitch(t, r, f.Node.(*ast.SwitchStatement), f.Clause), deps
		case f.Flags&(binder.FlowTrueCondition|binder.FlowFalseCondition) != 0:
			t, deps := c.flowType(f.Antecedent, r, declared, w)
			return c.narrowBy(t, r, f.Node.(ast.Expression), f.Flags&binder.FlowTrueCondition != 0), deps
		case f.Flags&binder.FlowScopeStart != 0:
			if f.Scope == r.sym.Scope {
				return declared, nil
			}
			f = f.Antecedent
			continue
		case f.Flags&binder.FlowStart != 0:
			if r.outer && f.Outer != nil && f.Scope != r.sym.Scope && !encloses(f.Scope, r.sym.Scope) {
				f = f.Outer
				continue
			}
			if len(r.path) == 0 && f.Scope == r.sym.Scope {
				return c.initialType(r.sym, declared), nil
			}
			return declared, nil
		}
		if w.onPath[f] {
			return c.neverT, []*binder.FlowNode{f}
		}
		k := flowKey{f, r.sym, joinPath(r.path), declared, r.outer}
		if t, ok := c.flowCache[k]; ok {
			return t, nil
		}
		if m, ok := w.memo[f]; ok && w.allOnPath(m.deps) {
			return m.t, m.deps
		}
		w.onPath[f] = true
		cuts := c.cutCount
		var ts []*Type
		var deps []*binder.FlowNode
		for _, a := range f.Antecedents {
			if a.Flags&binder.FlowSwitchClause != 0 && a.Clause == -1 && c.exhaustiveSwitch(a.Node.(*ast.SwitchStatement)) {
				continue // no case matched: an exhaustive switch never gets here
			}
			t, d := c.flowType(a, r, declared, w)
			ts = append(ts, t)
			deps = mergeDeps(deps, d, f)
		}
		delete(w.onPath, f)
		t := c.unknownJoin(c.in.union(ts...))
		w.memo[f] = flowMemo{t, deps}
		if len(deps) == 0 && c.cutCount == cuts {
			// A complete answer: no loop outside f cut it, and no assignment
			// it read was a provisional recursion cut.
			c.flowCache[k] = t
		}
		return t, deps
	}
}

// unknownJoin is tsc's rule for unknown's narrowed parts: where branches
// that narrowed unknown to `{}`, null and undefined meet again, the
// reference is unknown.
func (c *Checker) unknownJoin(t *Type) *Type {
	if t.Flags&Union == 0 || len(t.Types) != 3 {
		return t
	}
	var have TypeFlags
	for _, m := range t.Types {
		switch {
		case m == c.emptyObjT:
			have |= Object
		case m.Flags&Null != 0, m.Flags&Undefined != 0 && !m.IsMissing():
			have |= m.Flags & (Null | Undefined)
		}
	}
	if have == Object|Null|Undefined {
		return c.unknownT
	}
	return t
}

func (w *flowWalk) allOnPath(deps []*binder.FlowNode) bool {
	for _, d := range deps {
		if !w.onPath[d] {
			return false
		}
	}
	return true
}

// mergeDeps adds d's labels to deps, leaving out self: a loop's own label
// is where its cut resolves.
func mergeDeps(deps, d []*binder.FlowNode, self *binder.FlowNode) []*binder.FlowNode {
next:
	for _, x := range d {
		if x == self {
			continue
		}
		for _, y := range deps {
			if x == y {
				continue next
			}
		}
		deps = append(deps, x)
	}
	return deps
}

// assignedType is the type an assignment node stores: an initializer, the
// right side of `=`; anything
// else (a compound assignment, an update, a destructuring or loop binding)
// stores a value of the declared type.
func (c *Checker) assignedType(f *binder.FlowNode, declared *Type) *Type {
	sym := f.Symbol
	var value ast.Expression
	switch n := f.Node.(type) {
	case *ast.VarDeclaration:
		if n.Init == nil {
			if c.evolving[sym] {
				return c.undefinedT
			}
			return declared // `let x: T;` keeps T (only definite assignment sees undefined)
		}
		value = n.Init
	case *ast.AssignmentExpression:
		switch n.Op {
		case "=", "&&=", "||=", "??=":
			// A logical assignment stores its right side on the path where
			// it stores at all (the binder joins the other).
		default:
			return declared
		}
		value = n.Right
	case *ast.Identifier:
		// A destructuring assignment's target (`[a, b] = pair`).
		return c.destructuredType(n)
	default:
		return declared
	}
	// The value may read sym itself (`x = x + 1` in a loop). A recursive
	// query contributes nothing (the least fixed point, as a loop's back
	// edge does); the answers that depended on it are provisional.
	if c.assigning[value] {
		c.cuts = append(c.cuts, value)
		c.cutCount++
		return c.neverT
	}
	if a, ok := value.(*ast.ArrayLiteral); ok && len(a.Elements) == 0 && (sym == nil || !c.evolving[sym]) {
		return c.in.array(c.neverT) // an assigned `[]` is never[] under strictNullChecks
	}
	c.assigning[value] = true
	t := c.TypeOf(value)
	delete(c.assigning, value)
	if len(c.assigning) == 0 {
		c.cuts = c.cuts[:0]
	}
	if c.evolving[sym] {
		t = c.widenFrom(value, t)
	}
	return t
}

// compoundLike reports tsc's isCompoundLikeAssignment: `x = a op b` with op a
// shift, additive or multiplicative operator. Its target is typed by the
// base type of its declared literal type (`let s: "" …; s = s + "x"`).
func compoundLike(a *ast.AssignmentExpression) bool {
	if a.Op != "=" {
		return false
	}
	b, ok := a.Right.(*ast.BinaryExpression)
	if !ok {
		return false
	}
	switch b.Op {
	case "<<", ">>", ">>>", "+", "-", "*", "/", "%", "**":
		return true
	}
	return false
}

// assignmentReduced keeps the members of declared that the assigned type can
// be (TypeScript's assignment reduction): `let x: string | number = 1` is a
// number until the next assignment.
func (c *Checker) assignmentReduced(declared, assigned *Type) *Type {
	if assigned.Flags&Never != 0 {
		return assigned // a recursion cut: this path adds nothing
	}
	if declared.Flags&Union == 0 || c.Unanswered(assigned) || assigned.Flags&(Any|Unknown) != 0 {
		return declared
	}
	var keep []*Type
	for _, m := range declared.Types {
		for _, a := range members(assigned) {
			if assignableMember(a, m) || (a.Flags&Object != 0 && m.Flags&Object != 0 && c.assignableTo(a, m) != no) {
				keep = append(keep, m)
				break
			}
		}
	}
	if len(keep) == 0 {
		return declared
	}
	return c.in.union(keep...)
}

// assignableMember approximates "a is assignable to m" for single members.
func assignableMember(a, m *Type) bool {
	switch {
	case a == m, m.Flags&(Any|Unknown) != 0:
		return true
	case a.Flags&Undefined != 0 && m.Flags&Undefined != 0:
		return true // undefined and missing: an optional property takes either
	case a.Flags&Literal != 0:
		return m.Flags&literalBase(a.Flags) != 0
	case a.Flags&Object != 0 && m.Flags&Object != 0:
		if a.Kind == Instance && m.Kind == Instance {
			return a.Derives(m.Symbol)
		}
		return a.Kind == m.Kind
	case a.Flags&Void != 0:
		return m.Flags&Undefined != 0
	}
	return false
}

func literalBase(f TypeFlags) TypeFlags {
	switch {
	case f&StringLiteral != 0:
		return String
	case f&NumberLiteral != 0:
		return Number | Int | Float32
	case f&BooleanLiteral != 0:
		return Boolean
	}
	return BigInt
}

// narrowBy narrows t, the type of r, by cond having been truthy or falsy.
func (c *Checker) narrowBy(t *Type, r ref, cond ast.Expression, truthy bool) *Type {
	if t.Flags&Any != 0 {
		return c.narrowAny(t, r, cond, truthy)
	}
	if a, ok := cond.(*ast.AssignmentExpression); ok && a.Op == "=" {
		// `if (x = e)`: e narrows, then x's truthiness (tsc's
		// narrowTypeByBinaryExpression for `=`).
		t = c.narrowBy(t, r, a.Right, truthy)
		if !c.refersTo(a.Left, r) {
			return t
		}
		if truthy {
			if t.Flags&Unknown != 0 {
				return c.emptyObjT
			}
			return c.truthyPart(t)
		}
		return c.removeTruthy(t)
	}
	if c.refersTo(cond, r) {
		if truthy {
			if t.Flags&Unknown != 0 {
				return c.emptyObjT // a truthy unknown is some non-nullish value
			}
			return c.truthyPart(t)
		}
		return c.removeTruthy(t)
	}
	if truthy && c.optionalChainContains(cond, r) {
		// `if (x?.p)`: x is not nullish when the chain is truthy (tsc's
		// narrowTypeByTruthiness).
		return c.nonNullable(t)
	}
	if id, ok := cond.(*ast.Identifier); ok && c.inline < 5 {
		// An aliased condition (TypeScript 4.4): `const isStr = typeof x
		// === "string"; if (isStr) …` narrows x as its initializer would,
		// when x cannot have changed since.
		if init := c.aliasedCondition(id); init != nil && c.constantRef(r) {
			c.inline++
			defer func() { c.inline-- }()
			return c.narrowBy(t, r, init, truthy)
		}
	}
	if m, ok := c.accessOf(skipToAccess(cond)); ok && c.refersTo(m.Object, r) && isDiscriminant(t, m.Property) {
		// `if (x.done)`: the members whose discriminant can be truthy (or
		// falsy) stay (tsc's narrowTypeByTruthiness by discriminant).
		return c.filter(t, func(mt *Type) bool {
			if mt.Flags&Object == 0 {
				return !truthy || mt.Flags&Nullish == 0 || m.Optional && !truthy
			}
			p := mt.Prop(m.Property)
			if p == nil {
				return true
			}
			if truthy {
				return canBe(p.Type, truthyMember)
			}
			return canBe(p.Type, falsyMember)
		})
	}
	switch e := cond.(type) {
	case *ast.UnaryExpression:
		if e.Op == "!" {
			return c.narrowBy(t, r, e.Arg, !truthy)
		}
	case *ast.CallExpression:
		return c.narrowByPredicate(t, r, e, truthy)
	case *ast.BinaryExpression:
		switch e.Op {
		case "instanceof":
			return c.narrowByInstanceof(t, r, e, truthy)
		case "in":
			return c.narrowByIn(t, r, e, truthy)
		case "&&", "||":
			// && is truthy when both are; || is falsy when both are.
			both := (e.Op == "&&") == truthy
			if both {
				return c.narrowBy(c.narrowBy(t, r, e.Left, truthy), r, e.Right, truthy)
			}
			return c.in.union(c.narrowBy(t, r, e.Left, truthy),
				c.narrowBy(c.narrowBy(t, r, e.Left, !truthy), r, e.Right, truthy))
		case "===", "!==", "==", "!=":
			equal := (e.Op == "===" || e.Op == "==") == truthy
			loose := e.Op == "==" || e.Op == "!="
			// `cond === true`, `false !== cond`: narrow by cond itself
			// (narrowTypeByBooleanComparison) — unless cond is the reference,
			// which narrows by equality, as tsc checks that first.
			if b, ok := e.Right.(*ast.BooleanLiteral); ok && !isAccess(e.Left) && !c.refersTo(e.Left, r) {
				return c.narrowBy(t, r, e.Left, equal == b.Value)
			}
			if b, ok := e.Left.(*ast.BooleanLiteral); ok && !isAccess(e.Right) && !c.refersTo(e.Right, r) {
				return c.narrowBy(t, r, e.Right, equal == b.Value)
			}
			if r := c.narrowByEquality(t, r, e.Left, e.Right, equal, loose); r != nil {
				return r
			}
			if r := c.narrowByEquality(t, r, e.Right, e.Left, equal, loose); r != nil {
				return r
			}
			if r := c.narrowByDiscriminant(t, r, e.Left, e.Right, equal, loose); r != nil {
				return r
			}
			if r := c.narrowByDiscriminant(t, r, e.Right, e.Left, equal, loose); r != nil {
				return r
			}
			if c.optionalChainContains(e.Left, r) {
				return c.chainContainment(t, e.Right, equal, loose)
			}
			if c.optionalChainContains(e.Right, r) {
				return c.chainContainment(t, e.Left, equal, loose)
			}
		}
	}
	return t
}

// narrowAny narrows an `any` reference as TypeScript does: only `typeof`
// (to the primitive it names, narrowTypeByTypeName) and `instanceof` (to the
// class's instance) change it; truthiness, equality and assignments keep it
// `any`.
func (c *Checker) narrowAny(t *Type, r ref, cond ast.Expression, truthy bool) *Type {
	switch e := cond.(type) {
	case *ast.UnaryExpression:
		if e.Op == "!" {
			return c.narrowAny(t, r, e.Arg, !truthy)
		}
	case *ast.BinaryExpression:
		switch e.Op {
		case "&&", "||":
			both := (e.Op == "&&") == truthy
			if both {
				return c.narrowAny(c.narrowAny(t, r, e.Left, truthy), r, e.Right, truthy)
			}
			return c.in.union(c.narrowAny(t, r, e.Left, truthy),
				c.narrowAny(c.narrowAny(t, r, e.Left, !truthy), r, e.Right, truthy))
		case "instanceof":
			if !truthy || !c.refersTo(e.Left, r) {
				return t
			}
			if cls, ok := e.Right.(*ast.Identifier); ok {
				if csym, ok := c.b.Resolve(cls); ok && csym != nil && csym.Flags&binder.Class != 0 {
					if inst := c.instanceType(csym); inst != nil {
						return inst
					}
				}
			}
			return t
		case "===", "!==", "==", "!=":
			equal := (e.Op == "===" || e.Op == "==") == truthy
			for _, pair := range [][2]ast.Expression{{e.Left, e.Right}, {e.Right, e.Left}} {
				u, ok := pair[0].(*ast.UnaryExpression)
				if !ok || u.Op != "typeof" || !c.refersTo(u.Arg, r) {
					continue
				}
				lit, ok := stringConst(pair[1])
				if !ok || !equal {
					return t
				}
				switch lit {
				case "string":
					return c.strT
				case "number":
					return c.numT
				case "bigint":
					return c.bigintT
				case "symbol":
					return c.symT
				case "boolean":
					return c.boolT
				case "undefined":
					return c.undefinedT
				case "object", "function":
					return t
				}
				return c.objectT // a host object's typeof
			}
		}
	}
	return t
}

// narrowByEquality narrows t by `ref ==/=== other` (equal) or its negation,
// when ref is sym or `typeof sym`; nil when neither.
func (c *Checker) narrowByEquality(t *Type, r ref, ref, other ast.Expression, equal, loose bool) *Type {
	if u, ok := ref.(*ast.UnaryExpression); ok && u.Op == "typeof" {
		lit, isLit := stringConst(other)
		if !isLit {
			return nil
		}
		if !c.refersTo(u.Arg, r) {
			// `typeof x?.p === "string"`: x is not nullish when p's type is
			// not undefined.
			if equal == (lit != "undefined") && c.optionalChainContains(u.Arg, r) {
				t = c.nonNullable(t)
			}
			// `typeof x.p === "undefined"` with p a discriminant: the members
			// whose p can have that typeof (tsc's narrowTypeByDiscriminant).
			if m, ok := c.accessOf(u.Arg); ok && c.refersTo(m.Object, r) && isDiscriminant(t, m.Property) {
				return c.filter(t, func(mt *Type) bool {
					if mt.Flags&Object == 0 {
						return true
					}
					p := mt.Prop(m.Property)
					return p == nil || c.narrowByTypeof(p.Type, lit, equal).Flags&Never == 0
				})
			}
			if equal == (lit != "undefined") && c.optionalChainContains(u.Arg, r) {
				return t
			}
			return nil
		}
		return c.narrowByTypeof(t, lit, equal)
	}
	if !c.refersTo(ref, r) {
		return nil
	}
	ot := c.TypeOf(other)
	if ot.Flags&(Null|Undefined) != 0 && ot.Flags&Union == 0 && t.Flags&Unknown != 0 {
		// unknown is {} | null | undefined here.
		switch {
		case equal && loose:
			return c.in.union(c.nullT, c.undefinedT)
		case equal:
			return ot
		case loose:
			return c.emptyObjT
		case ot.Flags&Null != 0:
			return c.in.union(c.emptyObjT, c.undefinedT)
		}
		return c.in.union(c.emptyObjT, c.nullT)
	}
	if ot.Flags&(Null|Undefined) != 0 && ot.Flags&Union == 0 {
		match := ot.Flags & (Null | Undefined)
		if loose {
			match = Null | Undefined | Void
		} else if match&Undefined != 0 {
			match |= Void
		}
		return c.filter(t, func(m *Type) bool { return (m.Flags&match != 0) == equal })
	}
	if c.Unanswered(ot) || ot.Flags&(Any|Unknown|TypeParam) != 0 {
		return t
	}
	if t.Flags&Unknown != 0 {
		// tsc: `u === v` narrows unknown to v's type when v is a primitive
		// or `object`, to `object` when v is an object; nothing else does.
		switch {
		case !equal || loose:
			return t
		case ot.Flags&(StringLike|NumberLike|BooleanLike|BigIntLike|ESSymbol|NonPrimitive) != 0 && ot.Flags&Object == 0:
			return ot
		case ot.Flags&Object != 0 && ot.Flags&Union == 0:
			return c.objectT
		}
		return t
	}
	// TypeScript's narrowTypeByEquality: the true branch keeps what is
	// comparable with the value (or coerces to it under ==), with a
	// primitive replaced by the value's literals; the false branch drops the
	// unit members equal to a unit value.
	if equal {
		kept := c.filter(t, func(m *Type) bool {
			return c.comparable(m, ot, 0) != no || c.comparable(ot, m, 0) != no || loose && coercible(m, ot)
		})
		return c.replacePrimitives(kept, ot)
	}
	if !isUnit(ot) {
		return t
	}
	return c.filter(t, func(m *Type) bool {
		return !(isUnit(m) && (c.comparable(m, ot, 0) == yes || c.comparable(ot, m, 0) == yes))
	})
}

// isUnit reports a type with one value: a literal, an enum member, null or
// undefined.
func isUnit(t *Type) bool {
	return t.Flags&Union == 0 && t.Flags&(Literal|Null|Undefined|Void) != 0
}

// coercible is TypeScript's isCoercibleUnderDoubleEquals: a number, string
// or boolean literal may == a number, string or bigint value (`x == n`); a
// literal value is compared by comparability only.
func coercible(s, t *Type) bool {
	return s.Flags&(Number|Int|Float32|String|BooleanLiteral) != 0 && t.Flags&(Number|Int|Float32|String|BigInt) != 0
}

// replacePrimitives is TypeScript's replacePrimitivesWithLiterals: each
// primitive member of t that lits has literals of becomes those literals.
func (c *Checker) replacePrimitives(t, lits *Type) *Type {
	var have TypeFlags
	for _, l := range members(lits) {
		if l.Member == "" {
			have |= l.Flags & (StringLiteral | NumberLiteral | BigIntLiteral)
		}
	}
	if have == 0 {
		return t
	}
	var out []*Type
	changed := false
	for _, m := range members(t) {
		var f TypeFlags
		switch {
		case m.Flags&String != 0 && have&StringLiteral != 0:
			f = StringLiteral
		case m.Flags&Number != 0 && have&NumberLiteral != 0:
			f = NumberLiteral
		case m.Flags&BigInt != 0 && have&BigIntLiteral != 0:
			f = BigIntLiteral
		default:
			out = append(out, m)
			continue
		}
		changed = true
		for _, l := range members(lits) {
			if l.Flags&f != 0 && l.Member == "" {
				out = append(out, l)
			}
		}
	}
	if !changed {
		return t
	}
	return c.in.union(out...)
}

var typeofFlags = map[string]TypeFlags{
	"string":    StringLike,
	"number":    NumberLike,
	"bigint":    BigIntLike,
	"boolean":   BooleanLike,
	"undefined": Undefined | Void,
	"symbol":    ESSymbol,
}

// narrowByTypeof narrows t by `typeof sym === name` (equal) or its negation.
func (c *Checker) narrowByTypeof(t *Type, name string, equal bool) *Type {
	var is func(m *Type) bool
	if f, ok := typeofFlags[name]; ok {
		is = func(m *Type) bool { return m.Flags&f != 0 }
	} else if name == "function" {
		is = func(m *Type) bool { return m.Flags&Object != 0 && m.Kind == Function }
	} else if name == "object" {
		is = func(m *Type) bool {
			return m.Flags&(Null|NonPrimitive) != 0 || m.Flags&Object != 0 && m.Kind != Function
		}
	} else {
		// Any other name is a host object's typeof (tsc's TypeofEQHostObject):
		// only an object or a function can have it.
		is = func(m *Type) bool { return m.Flags&(NonPrimitive|Object) != 0 }
		if t.Flags&Unknown != 0 {
			if equal {
				return c.objectT
			}
			return t
		}
	}
	if t.Flags&Unknown != 0 {
		if !equal {
			return t
		}
		switch name {
		case "string":
			return c.strT
		case "number":
			return c.numT
		case "bigint":
			return c.bigintT
		case "symbol":
			return c.symT
		case "boolean":
			return c.boolT
		case "undefined":
			return c.undefinedT
		case "object":
			return c.in.union(c.objectT, c.nullT)
		}
		return c.unanswered // a function: the checker has no Function type yet
	}
	var out []*Type
	for _, m := range members(t) {
		if m.Flags&Object != 0 && m.Kind == Anonymous && len(m.Props) == 0 && equal {
			// `{}` holds every non-nullish value: typeof picks one kind.
			switch name {
			case "string":
				out = append(out, c.strT)
			case "number":
				out = append(out, c.numT)
			case "bigint":
				out = append(out, c.bigintT)
			case "symbol":
				out = append(out, c.symT)
			case "boolean":
				out = append(out, c.boolT)
			case "object":
				out = append(out, c.objectT)
			case "undefined":
			default:
				return c.unanswered
			}
			continue
		}
		out = append(out, m)
	}
	return c.filter(c.in.union(out...), func(m *Type) bool { return is(m) == equal })
}

// removeTruthy is the falsy branch: TypeScript drops only the members that
// are never falsy (objects, true, non-empty literals).
func (c *Checker) removeTruthy(t *Type) *Type {
	return c.filter(t, func(m *Type) bool {
		switch {
		case m.Flags&(Object|NonPrimitive) != 0, m == c.trueT:
			return false
		case m.Flags&StringLiteral != 0:
			return m.Value == ""
		case m.Flags&NumberLiteral != 0:
			return m.Value == "0" || m.Value == "NaN"
		case m.Flags&BigIntLiteral != 0:
			return m.Value == "0"
		}
		return true
	})
}

func (c *Checker) filter(t *Type, keep func(*Type) bool) *Type {
	if t.Flags&(Any|Unknown) != 0 {
		return t
	}
	var out []*Type
	for _, m := range members(t) {
		if m == c.boolT {
			// boolean narrows as true | false.
			for _, b := range []*Type{c.trueT, c.falseT} {
				if keep(b) {
					out = append(out, b)
				}
			}
			continue
		}
		if keep(m) {
			out = append(out, m)
		}
	}
	return c.in.union(out...)
}

// narrowByDiscriminant narrows a union of objects by `sym.p === lit` (equal)
// or its negation: the members whose p can (or cannot only) be lit. An
// optional chain `sym?.p === lit` also proves sym non-nullish.
func (c *Checker) narrowByDiscriminant(t *Type, r ref, ref, other ast.Expression, equal, loose bool) *Type {
	m, ok := c.accessOf(ref)
	if !ok {
		return nil
	}
	if !c.refersTo(m.Object, r) {
		return nil
	}
	ot := c.TypeOf(other)
	if ot.Flags&(Literal|Null|Undefined) == 0 || ot.Flags&Union != 0 {
		return nil
	}
	if m.Optional {
		t = c.chainContainment(t, other, equal, loose)
	}
	if !isDiscriminant(t, m.Property) {
		return t
	}
	return c.filter(t, func(mt *Type) bool {
		if mt.Flags&Object == 0 {
			return mt.Flags&Nullish != 0 && !equal // `x?.p !== lit` keeps a nullish x
		}
		p := mt.Prop(m.Property)
		if p == nil {
			return true
		}
		can, only := false, true
		for _, pt := range members(p.Type) {
			same := pt == ot || pt.Flags&ot.Flags&Undefined != 0 || loose && ot.Flags&(Null|Undefined) != 0 && pt.Flags&Nullish != 0
			if same || pt.Flags&literalBase(ot.Flags) != 0 && ot.Flags&Literal != 0 || pt.Flags&(Any|Unknown) != 0 {
				can = true
			}
			if !same {
				only = false
			}
		}
		if equal {
			return can
		}
		return !only
	})
}

// access is a property read by name: `x.p`, or `x["p"]` with a literal key.
type access struct {
	Object   ast.Expression
	Property string
	Optional bool
}

// accessOf is e as a property read by name, when it is one.
func (c *Checker) accessOf(e ast.Expression) (access, bool) {
	switch x := e.(type) {
	case *ast.MemberExpression:
		return access{x.Object, x.Property, x.Optional}, true
	case *ast.IndexExpression:
		if k, ok := c.accessKey(x.Index); ok {
			return access{x.Object, k, x.Optional}, true
		}
	}
	return access{}, false
}

// skipToAccess is the property access a condition tests through a comma
// (`(f(), x.p)`): tsc's getReferenceCandidate.
func skipToAccess(e ast.Expression) ast.Expression {
	for {
		switch x := e.(type) {
		case *ast.SequenceExpression:
			if len(x.Exprs) == 0 {
				return e
			}
			e = x.Exprs[len(x.Exprs)-1]
		case *ast.BinaryExpression:
			if x.Op != "," {
				return e
			}
			e = x.Right
		default:
			return e
		}
	}
}

// isDiscriminant reports whether property name discriminates the union t
// (TypeScript's isDiscriminantProperty): its type differs between the
// members that declare it, and one of them gives it a literal type (a unit
// type, a union of them, or boolean). `v.x === undefined` narrows nothing
// when every x is `number | undefined` or `string`.
func isDiscriminant(t *Type, name string) bool {
	var first *Type
	uniform, literal := true, false
	count := 0
	for _, m := range members(t) {
		if m.Flags&Object == 0 {
			continue
		}
		p := m.Prop(name)
		if p == nil {
			continue
		}
		count++
		if first == nil {
			first = p.Type
		} else if p.Type != first {
			uniform = false
		}
		literal = literal || isLiteralType(p.Type)
	}
	// A union already narrowed to one member keeps discriminating by a
	// literal property (TypeScript tests the declared union).
	return literal && (!uniform || count == 1)
}

// isLiteralType is TypeScript's isLiteralType: boolean, a unit type, or a
// union of units.
func isLiteralType(t *Type) bool {
	if t.Flags&Boolean != 0 {
		return true
	}
	for _, m := range members(t) {
		if m.Flags&(Literal|Boolean|Null|Undefined) == 0 {
			return false
		}
	}
	return true
}

// narrowByInstanceof narrows t by `sym instanceof C`.
func (c *Checker) narrowByInstanceof(t *Type, r ref, e *ast.BinaryExpression, truthy bool) *Type {
	cls, ok := e.Right.(*ast.Identifier)
	if !ok || !c.refersTo(e.Left, r) {
		return t
	}
	csym, ok := c.b.Resolve(cls)
	if ok && csym != nil && csym.Flags&binder.Class == 0 {
		// A constructor value (`declare var Error: ErrorConstructor`): the
		// instance is what its construct signature makes.
		if inst := c.constructedType(c.TypeOf(cls)); inst != nil {
			if truthy {
				return c.narrowToType(t, inst)
			}
			return c.filter(t, func(m *Type) bool {
				return !(m.Flags&Object != 0 && (m == inst || (m.Symbol != nil && m.Symbol == inst.Symbol) || c.assignableTo(m, inst) == yes))
			})
		}
	}
	if !ok || csym == nil || csym.Flags&binder.Class == 0 {
		if truthy && t.Flags&Unknown != 0 {
			return c.unanswered // a library class the checker has no declaration for yet
		}
		return t
	}
	inst := c.instanceType(csym)
	if inst == nil {
		if truthy && t.Flags&Unknown != 0 {
			return c.unanswered // an instance the checker does not model
		}
		return t
	}
	is := func(m *Type) bool { return m.Flags&Object != 0 && m.Kind == Instance && m.Derives(csym) }
	if truthy {
		if t.Flags&Unknown != 0 {
			return inst
		}
		// A subclass instance stays; a base class's becomes C's.
		var out []*Type
		for _, m := range members(t) {
			switch {
			case is(m):
				out = append(out, m)
			case m.Flags&Object != 0 && m.Kind == Instance && inst.Derives(m.Symbol):
				out = append(out, inst)
			}
		}
		return c.in.union(out...)
	}
	return c.filter(t, func(m *Type) bool { return !is(m) })
}

// constructedType is the instance type `x instanceof ctor` narrows to, as
// tsc has it: the type of ctor's `prototype` property unless that is any,
// else what its first construct signature makes; nil when neither is an
// object type.
func (c *Checker) constructedType(ctor *Type) *Type {
	if ctor == nil || c.Unanswered(ctor) || ctor.Flags&Object == 0 {
		return nil
	}
	if p := ctor.Prop("prototype"); p != nil && p.Type != nil && !c.Unanswered(p.Type) && p.Type.Flags&Any == 0 {
		if p.Type.Flags&Object != 0 {
			return p.Type
		}
		return nil
	}
	if len(ctor.Constructs) == 0 {
		return nil
	}
	r := ctor.Constructs[0].Result
	if r == nil || c.Unanswered(r) || r.Flags&Object == 0 {
		return nil
	}
	return r
}

// narrowByIn narrows a union of objects by `"p" in sym`.
func (c *Checker) narrowByIn(t *Type, r ref, e *ast.BinaryExpression, truthy bool) *Type {
	key, ok := e.Left.(*ast.StringLiteral)
	if !ok || !c.refersTo(e.Right, r) {
		return t
	}
	return c.filter(t, func(m *Type) bool {
		if m.Flags&Object == 0 {
			return false // `in` throws on a primitive
		}
		p := m.Prop(key.Value)
		if p == nil {
			return !truthy
		}
		return truthy || p.Optional
	})
}

// narrowByPredicate narrows t by a call to a function with a `p is T`
// result whose argument p is sym.
func (c *Checker) narrowByPredicate(t *Type, r ref, e *ast.CallExpression, truthy bool) *Type {
	ft := c.dottedType(e.Callee)
	if c.unknownGuard(ft, e, r) {
		return c.unanswered
	}
	if ft.Flags&Object == 0 || ft.Kind != Function || ft.Predicate == nil || ft.Predicate.Asserts || e.Optional {
		return t
	}
	p := ft.Predicate
	arg := predicateSubject(e, p)
	if arg == nil {
		return t
	}
	if !c.refersTo(arg, r) {
		return c.predicateContainment(t, r, arg, p.Type, truthy)
	}
	pt := p.Type
	if len(ft.TypeParams) > 0 {
		// A generic guard: its predicate type is the call's instantiation.
		pt = c.instantiate(pt, c.inferArgs(e.Args, e.TypeArgs, ft, true))
		if hasTypeParam(pt) || c.Unanswered(pt) {
			return c.unanswered
		}
	}
	if truthy {
		return c.narrowToType(t, pt)
	}
	if t.Flags&Unknown != 0 {
		return t
	}
	return c.filter(t, func(m *Type) bool { return !c.within(m, pt) })
}

// dottedType is the type of a call's callee as a type guard or assertion
// sees it (TypeScript's getTypeOfDottedName): through declared types, with
// no flow narrowing. Narrowing it would walk back to this very call.
func (c *Checker) dottedType(e ast.Expression) *Type {
	switch x := e.(type) {
	case *ast.Identifier:
		sym, ok := c.b.Resolve(x)
		if !ok || sym == nil {
			if spec, ok := c.b.Program.BuiltinMarkers[x.Name]; ok {
				return c.moduleType(spec) // a builtin module, as TypeOf types it
			}
			if s := c.builtinImport(x.Name); s != nil && s.Flags&binder.Value != 0 {
				return c.typeOfSymbol(s)
			}
			return c.unanswered
		}
		return c.typeOfSymbol(sym)
	case *ast.ThisExpression:
		return c.thisType(x)
	case *ast.SuperExpression:
		// `super.m(…)`: the base class's instance.
		if t := c.thisType(x); !c.Unanswered(t) && t.Flags&Object != 0 && t.Kind == Instance && t.Base != nil {
			return t.Base
		}
		return c.unanswered
	case *ast.MemberExpression:
		if x.Optional {
			return c.unanswered
		}
		if m := c.namespaceMember(x); m != nil {
			return c.typeOfSymbol(m)
		}
		obj := c.dottedType(x.Object)
		if c.Unanswered(obj) || obj.Flags&Object == 0 || obj.Flags&Union != 0 {
			return c.unanswered
		}
		if p := obj.Prop(x.Property); p != nil {
			return p.Type
		}
	}
	return c.unanswered
}

// unknownGuard reports a call the checker cannot type whose arguments
// mention r: it may be a type guard or an `asserts` function (Node's
// `assert(x)`), so what r is after it is not known. `console`'s methods
// never are.
func (c *Checker) unknownGuard(ft *Type, e *ast.CallExpression, r ref) bool {
	if !c.Unanswered(ft) {
		return false
	}
	if m, ok := e.Callee.(*ast.MemberExpression); ok {
		if id, ok := m.Object.(*ast.Identifier); ok && id.Name == "console" {
			return false
		}
	}
	for _, a := range e.Args {
		if c.mentions(a, r) {
			return true
		}
	}
	return false
}

// mentions reports whether e reads r or a path under it.
func (c *Checker) mentions(e ast.Expression, r ref) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if x, ok := n.(ast.Expression); ok && !found {
			if er, _, ok := c.refOf(x); ok && er.sym == r.sym {
				found = true
			}
		}
		return !found
	})
	return found
}

// narrowToType narrows t to the members that are candidates, or to
// candidate itself when none is (unknown included).
//
// As TypeScript's getNarrowedType, each member of t becomes the candidate
// when the candidate is one of its kind (`Type` guarded to `TypeExt`), stays
// when it is already within the candidate, and drops otherwise.
func (c *Checker) narrowToType(t, candidate *Type) *Type {
	if t.Flags&Unknown != 0 {
		return candidate
	}
	if t.Flags&Any != 0 {
		return t
	}
	// tsc's getNarrowedType: a member that is a subtype of the candidate
	// stays itself (`string | string[]` by `any[]` is `string[]`); else the
	// candidate, when it is a subtype of the member.
	var out []*Type
	for _, m := range members(t) {
		switch {
		case c.within(m, candidate):
			out = append(out, m)
		case m.Flags&Object != 0 && candidate.Flags&Object != 0 && candidate.Flags&Union == 0 &&
			m != candidate && c.assignableTo(candidate, m) == yes:
			out = append(out, candidate)
		}
	}
	if r := c.in.union(out...); r != c.neverT {
		return r
	}
	// Two unrelated object types narrow to their intersection (`Legged`
	// by `x is Winged` is `Legged & Winged`), which is not modelled.
	if t.Flags&Object != 0 && t.Flags&Union == 0 && candidate.Flags&Object != 0 && candidate.Flags&Union == 0 {
		return c.unanswered
	}
	return candidate
}

func (c *Checker) within(m, candidate *Type) bool {
	for _, pm := range members(candidate) {
		if !assignableMember(m, pm) {
			continue
		}
		// Two named object types of one kind: m must really be a pm (a
		// Uint8Array is not a Buffer; a Buffer is a Uint8Array; a
		// Crate<{}> is not a Crate<Sundries>).
		if m.Flags&Object != 0 && pm.Flags&Object != 0 && m != pm && m.Symbol != nil && pm.Symbol != nil &&
			(m.Kind == Interface || m.Kind == Instance) && c.assignableTo(m, pm) != yes {
			continue
		}
		return true
	}
	return false
}

// storageType is the type of an unannotated binding declared without a value
// (`let x;`, TDD-00162): undefined, joined with the widened type of every
// value stored to it anywhere. Each read narrows it along the flow graph, as
// TypeScript's evolving let does; the storage type is what codegen gives the
// variable a representation from.
func (c *Checker) storageType(sym *binder.Symbol) *Type {
	c.evolving[sym] = true
	ts := []*Type{c.undefinedT}
	for _, n := range c.b.StoresTo(sym) {
		var value ast.Expression
		switch n := n.(type) {
		case *ast.VarDeclaration:
			value = n.Init
		case *ast.AssignmentExpression:
			if n.Op != "=" {
				return c.unanswered
			}
			value = n.Right
		case *ast.Identifier:
			t := c.destructuredType(n)
			if c.Unanswered(t) {
				return t
			}
			ts = append(ts, widen(c, t))
			continue
		default:
			return c.unanswered
		}
		if value == nil {
			continue
		}
		t := c.TypeOf(value)
		if c.Unanswered(t) {
			return t
		}
		ts = append(ts, c.widenFrom(value, t))
	}
	return c.in.union(ts...)
}

// narrowBySwitch narrows t by entering a switch's clause: its test equals
// the discriminant; for a default clause (or -1, past every case) no test
// does.
//
// Entering a clause from the dispatch means its test matched and no earlier
// one did (the first matching case wins), so a repeated `case 'number':` is
// never entered. `switch (true)` narrows by each case expression as a
// condition.
func (c *Checker) narrowBySwitch(t *Type, r ref, sw *ast.SwitchStatement, clause int) *Type {
	last := len(sw.Cases)
	if clause >= 0 && sw.Cases[clause].Test != nil {
		last = clause
	}
	if t.Flags&Unknown != 0 && last < len(sw.Cases) && !hasDefault(sw) && c.refersTo(sw.Discriminant, r) {
		// tsc: without a default, an unknown discriminant narrows to the
		// clause's case type alone.
		return c.narrowByCase(t, r, sw.Discriminant, sw.Cases[last].Test, true)
	}
	for _, cs := range sw.Cases[:last] {
		if cs.Test != nil {
			t = c.narrowByCase(t, r, sw.Discriminant, cs.Test, false)
		}
	}
	if last < len(sw.Cases) {
		t = c.narrowByCase(t, r, sw.Discriminant, sw.Cases[last].Test, true)
	}
	return t
}

func hasDefault(sw *ast.SwitchStatement) bool {
	for _, cs := range sw.Cases {
		if cs.Test == nil {
			return true
		}
	}
	return false
}

func (c *Checker) narrowByCase(t *Type, r ref, disc, test ast.Expression, equal bool) *Type {
	if b, ok := disc.(*ast.BooleanLiteral); ok && b.Value {
		return c.narrowBy(t, r, test, equal) // switch (true)
	}
	if r := c.narrowByEquality(t, r, disc, test, equal, false); r != nil {
		return r
	}
	if r := c.narrowByDiscriminant(t, r, disc, test, equal, false); r != nil {
		return r
	}
	return t
}

// narrowByAssertion narrows t after a call statement to an `asserts`
// function whose asserted argument is sym: to the asserted type, or to its
// truthy part for `asserts p`.
func (c *Checker) narrowByAssertion(t *Type, r ref, e *ast.CallExpression) *Type {
	if e.Optional {
		return t
	}
	ft := c.dottedType(e.Callee)
	if c.unknownGuard(ft, e, r) {
		return c.unanswered
	}
	if ft.Flags&Object == 0 || ft.Kind != Function || ft.Predicate == nil || !ft.Predicate.Asserts {
		return t
	}
	p := ft.Predicate
	arg := predicateSubject(e, p)
	if arg == nil {
		return t
	}
	if p.Type == nil {
		return c.narrowBy(t, r, arg, true)
	}
	pt := p.Type
	if len(ft.TypeParams) > 0 {
		pt = c.instantiate(pt, c.inferArgs(e.Args, e.TypeArgs, ft, true))
		if hasTypeParam(pt) || c.Unanswered(pt) {
			if c.refersTo(arg, r) {
				return c.unanswered
			}
			return t
		}
	}
	if !c.refersTo(arg, r) {
		return c.predicateContainment(t, r, arg, pt, true)
	}
	return c.narrowToType(t, pt)
}

// encloses reports whether scope s is outer or one of its ancestors: the
// start of a function that s's code runs in.
func encloses(outer, s *binder.Scope) bool {
	for ; s != nil; s = s.Parent {
		if s == outer {
			return true
		}
	}
	return false
}

// initialType is the type sym has where its function starts: a parameter
// whose default cannot be undefined does not hold undefined
// (removeOptionalityFromDeclaredType).
func (c *Checker) initialType(sym *binder.Symbol, declared *Type) *Type {
	def := c.paramDefault(sym)
	if def == nil || nullFactsOf(declared)&isUndefined == 0 || c.initializing[sym] {
		return declared // a default that reads the parameter itself
	}
	c.initializing[sym] = true
	defer delete(c.initializing, sym)
	if dt := c.TypeOf(def); c.Unanswered(dt) || nullFactsOf(dt)&isUndefined != 0 || dt.Flags&(Any|Unknown) != 0 {
		return declared
	}
	return c.filter(declared, func(m *Type) bool { return m.Flags&(Undefined|Void) == 0 })
}

// paramDefault is the default value of the parameter sym, or nil.
func (c *Checker) paramDefault(sym *binder.Symbol) ast.Expression {
	if len(sym.Declarations) == 0 || sym.Flags&binder.FunctionScopedVariable == 0 {
		return nil
	}
	var params []ast.Param
	switch fn := sym.Declarations[0].Node.(type) {
	case *ast.FunctionDeclaration:
		params = fn.Params
	case *ast.FunctionExpression:
		params = fn.Params
	case *ast.ArrowFunction:
		params = fn.Params
	default:
		return nil
	}
	if c.b.ScopeOf(sym.Declarations[0].Node) != sym.Scope {
		return nil
	}
	for _, p := range params {
		if p.Name == sym.Name {
			return p.Default
		}
	}
	return nil
}

// stringConst is a string literal's value, or a template literal's that has
// no substitutions.
func stringConst(e ast.Expression) (string, bool) {
	switch x := e.(type) {
	case *ast.StringLiteral:
		return x.Value, true
	case *ast.TemplateLiteral:
		if len(x.Exprs) == 0 && len(x.Quasis) == 1 {
			return x.Quasis[0], true
		}
	}
	return "", false
}

func isAccess(e ast.Expression) bool {
	switch e.(type) {
	case *ast.MemberExpression, *ast.IndexExpression:
		return true
	}
	return false
}

// chainObject is the object a member, element or call link applies to,
// and whether e is such a link.
func chainObject(e ast.Expression) (obj ast.Expression, optional, ok bool) {
	switch x := e.(type) {
	case *ast.MemberExpression:
		return x.Object, x.Optional, true
	case *ast.IndexExpression:
		return x.Object, x.Optional, true
	case *ast.CallExpression:
		return x.Callee, x.Optional, true
	}
	return nil, false, false
}

// chainEnd reports a link written in parentheses: an optional chain ends
// there.
func chainEnd(e ast.Expression) bool {
	switch x := e.(type) {
	case *ast.MemberExpression:
		return x.ChainEnd
	case *ast.IndexExpression:
		return x.ChainEnd
	case *ast.CallExpression:
		return x.ChainEnd
	}
	return false
}

// inOptionalChain reports whether e is a link of an optional chain: it or a
// link below it (before the chain's end) is `?.`.
func inOptionalChain(e ast.Expression) bool {
	for {
		obj, optional, ok := chainObject(e)
		if !ok {
			return false
		}
		if optional {
			return true
		}
		if chainEnd(obj) {
			return false
		}
		e = obj
	}
}

// optionalChainContains is TypeScript's optionalChainContainsReference: r
// is the object of a link of the optional chain e.
func (c *Checker) optionalChainContains(e ast.Expression, r ref) bool {
	for inOptionalChain(e) {
		obj, _, _ := chainObject(e)
		if c.refersTo(obj, r) {
			return true
		}
		e = obj
	}
	return false
}

// accessKey is the property key an element access's index denotes: a
// literal, or a constant whose type is a string or number literal.
func (c *Checker) accessKey(e ast.Expression) (string, bool) {
	if k, ok := literalKey(e); ok {
		return k, true
	}
	id, ok := e.(*ast.Identifier)
	if !ok {
		return "", false
	}
	sym, _ := c.b.Resolve(id)
	if sym == nil || len(sym.Declarations) == 0 {
		return "", false
	}
	if v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration); !ok || v.Kind != "const" {
		return "", false
	}
	switch t := c.typeOfSymbol(sym); {
	case t.Flags&(StringLiteral|NumberLiteral) != 0 && t.Flags&Union == 0 && t.Member == "":
		return t.Value, true
	}
	return "", false
}

// chainContainment narrows the object of an optional chain compared with
// value (TypeScript's narrowTypeByOptionalChainContainment): when the
// branch proves the chain reached its end, the object is not nullish.
func (c *Checker) chainContainment(t *Type, value ast.Expression, equal, loose bool) *Type {
	vt := c.TypeOf(value)
	if c.Unanswered(vt) {
		return t
	}
	nullable := Undefined | Void
	if loose {
		nullable |= Null
	}
	every := func(f func(*Type) bool) bool {
		for _, m := range members(vt) {
			if !f(m) {
				return false
			}
		}
		return true
	}
	if !equal && every(func(m *Type) bool { return m.Flags&nullable != 0 }) ||
		equal && every(func(m *Type) bool { return m.Flags&(Any|Unknown|nullable) == 0 }) {
		return c.nonNullable(t)
	}
	return t
}

// predicateContainment narrows the object of an optional chain passed to a
// type guard or assertion: a true guard whose type excludes undefined, or a
// false one whose type is only nullish, proves the chain reached its end.
func (c *Checker) predicateContainment(t *Type, r ref, arg ast.Expression, pt *Type, truthy bool) *Type {
	if pt == nil || c.Unanswered(pt) || !c.optionalChainContains(arg, r) {
		return t
	}
	if truthy && nullFactsOf(pt)&isUndefined == 0 && pt.Flags&(Any|Unknown|TypeParam) == 0 ||
		!truthy && allMembers(pt, Null|Undefined|Void) {
		return c.nonNullable(t)
	}
	return t
}

// flowAtRoot is the flow node at a reference's root (a name or `this`), and
// whether the root is read in the function whose flow stores to it.
func (c *Checker) flowAtRoot(root ast.Expression) (*binder.FlowNode, bool) {
	switch x := root.(type) {
	case *ast.Identifier:
		return c.b.FlowAt(x)
	case *ast.ThisExpression:
		f, _, local := c.b.ThisAt(x)
		return f, local && f != nil
	case *ast.SuperExpression:
		f, _, local := c.b.SuperAt(x)
		return f, local && f != nil
	}
	return nil, false
}

// neverReturning reports a call statement to a function declared to return
// never (TypeScript's getEffectsSignature): the code after it is not
// reached. The callee is a dotted name typed through declared types, and a
// function declaration it names must annotate its return type.
func (c *Checker) neverReturning(call *ast.CallExpression) bool {
	if _, stmt := c.parentOf(call).(*ast.ExpressionStatement); !stmt || call.Optional {
		return false
	}
	ft := c.dottedType(call.Callee)
	if c.Unanswered(ft) || ft.Flags&Object == 0 || ft.Kind != Function || len(ft.Overloads) > 0 || ft.Result == nil || ft.Result.Flags&Never == 0 {
		return false
	}
	sym := c.calleeSymbol(call.Callee)
	if sym != nil && sym.Flags&binder.Function != 0 && len(sym.Declarations) > 0 {
		if fd, ok := sym.Declarations[0].Node.(*ast.FunctionDeclaration); ok && fd.ReturnType == nil {
			return false // an inferred never: not an effect
		}
	}
	return true
}

// namespaceMember is the member symbol `ns.name` denotes, when ns names a
// namespace (possibly dotted), or nil.
func (c *Checker) namespaceMember(e *ast.MemberExpression) *binder.Symbol {
	var ns *binder.Symbol
	switch x := e.Object.(type) {
	case *ast.Identifier:
		ns, _ = c.b.Resolve(x)
	case *ast.MemberExpression:
		ns = c.namespaceMember(x)
	}
	if ns == nil {
		return nil
	}
	scope := c.b.NamespaceScope(ns)
	if scope == nil {
		return nil
	}
	return scope.Symbols.Get(e.Property)
}

// calleeSymbol is the symbol a dotted callee names, when it names one: an
// identifier or a namespace member.
func (c *Checker) calleeSymbol(e ast.Expression) *binder.Symbol {
	switch x := e.(type) {
	case *ast.Identifier:
		sym, _ := c.b.Resolve(x)
		return sym
	case *ast.MemberExpression:
		return c.namespaceMember(x)
	}
	return nil
}

// predicateSubject is what a call's type predicate is about: the argument
// at its index, or for `this is T` the receiver of a method call; nil when
// the call has no such operand.
func predicateSubject(e *ast.CallExpression, p *Predicate) ast.Expression {
	if p.Index == -1 {
		if m, ok := e.Callee.(*ast.MemberExpression); ok {
			return m.Object
		}
		return nil
	}
	if p.Index < 0 || p.Index >= len(e.Args) {
		return nil
	}
	return e.Args[p.Index]
}

// aliasedCondition is the initializer of the unannotated constant id names,
// the condition an alias stands for; nil otherwise.
func (c *Checker) aliasedCondition(id *ast.Identifier) ast.Expression {
	sym, _ := c.b.Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return nil
	}
	v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration)
	if !ok || v.Kind != "const" || v.TypeAnnot != nil || v.Init == nil {
		return nil
	}
	return v.Init
}

// constantRef is TypeScript's isConstantReference for a narrowed name: a
// constant, or a parameter or local let that nothing assigns after its
// declaration. (A property path would need its properties readonly, which
// the checker does not record.)
func (c *Checker) constantRef(r ref) bool {
	if len(r.path) > 0 {
		// A property path is constant when its root is, and each property
		// on it is readonly.
		var t *Type
		if r.sym.Name == "this" && len(r.sym.Declarations) == 0 {
			if r.sym.Scope == nil || r.sym.Scope.Node == nil {
				return false
			}
			t = c.thisType(r.sym.Scope.Node)
		} else {
			if !c.constantRef(ref{sym: r.sym}) {
				return false
			}
			t = c.typeOfSymbol(r.sym)
		}
		for _, key := range r.path {
			if c.Unanswered(t) || t.Flags&Object == 0 || t.Flags&Union != 0 {
				return false
			}
			p := t.Prop(key)
			if p == nil || !p.Readonly {
				return false
			}
			t = p.Type
		}
		return true
	}
	if len(r.sym.Declarations) == 0 {
		return r.sym.Name == "this" // `this` never changes
	}
	switch d := r.sym.Declarations[0].Node.(type) {
	case *ast.VarDeclaration:
		if d.Kind == "const" {
			return true
		}
		if d.Kind != "let" {
			return false
		}
	case *ast.ArrayDestructuring:
		return d.Kind == "const"
	case *ast.ObjectDestructuring:
		return d.Kind == "const"
	case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction:
		if r.sym.Flags&binder.FunctionScopedVariable == 0 {
			return false
		}
	default:
		return false
	}
	for _, n := range c.b.StoresTo(r.sym) {
		if _, decl := n.(*ast.VarDeclaration); !decl {
			return false // assigned somewhere
		}
	}
	return true
}

// exhaustiveSwitch is TypeScript's isExhaustiveSwitchStatement for a switch
// without a default clause: its case values cover every value of its
// discriminant's type, so the path past every case is never taken.
func (c *Checker) exhaustiveSwitch(sw *ast.SwitchStatement) bool {
	if v, ok := c.exhaustive[sw]; ok {
		return v
	}
	c.exhaustive[sw] = false // a cycle through this switch answers no
	t := c.TypeOf(sw.Discriminant)
	if c.Unanswered(t) || t.Flags&(Any|Unknown) != 0 {
		return false
	}
	for _, cs := range sw.Cases {
		if cs.Test == nil {
			return false
		}
		ct := c.TypeOf(cs.Test)
		if c.Unanswered(ct) || !isUnit(ct) {
			continue
		}
		t = c.filter(t, func(m *Type) bool { return !(isUnit(m) && c.comparable(m, ct, 0) == yes) })
	}
	v := t.Flags&Never != 0
	c.exhaustive[sw] = v
	return v
}

// endReachable reports whether some path reaches the end of a function
// body (TypeScript's functionHasImplicitReturn): the binder's graph, where
// an exhaustive switch's bypass and a call that never returns end a path.
func (c *Checker) endReachable(body *ast.BlockStatement) bool {
	f := c.b.EndFlow(body)
	if f == nil {
		return false
	}
	return c.reachable(f, map[*binder.FlowNode]bool{})
}

func (c *Checker) reachable(f *binder.FlowNode, seen map[*binder.FlowNode]bool) bool {
	for {
		switch {
		case f == nil:
			return true
		case f.Flags&binder.FlowUnreachable != 0:
			return false
		case f.Flags&binder.FlowStart != 0:
			return true
		case f.Flags&binder.FlowCall != 0:
			if c.neverReturning(f.Node.(*ast.CallExpression)) {
				return false
			}
			f = f.Antecedent
			continue
		case f.Flags&(binder.FlowBranchLabel|binder.FlowLoopLabel) != 0:
			if seen[f] {
				return false
			}
			seen[f] = true
			for _, a := range f.Antecedents {
				if a.Flags&binder.FlowSwitchClause != 0 && a.Clause == -1 && c.exhaustiveSwitch(a.Node.(*ast.SwitchStatement)) {
					continue
				}
				if c.reachable(a, seen) {
					return true
				}
			}
			return false
		}
		f = f.Antecedent
	}
}

// destructuredType is the value a destructuring assignment stores in the
// target n, a name or a nested pattern within it (tsc's getAssignedType): the
// element or property of the value its enclosing pattern receives, a default
// standing in for undefined; unanswered where the checker cannot tell.
func (c *Checker) destructuredType(n ast.Expression) *Type {
	switch p := c.parentOf(n).(type) {
	case *ast.AssignmentExpression:
		if p.Left != n || p.Op != "=" {
			return c.unanswered
		}
		if c.patternContainer(p) {
			// A default, `[x = d] = …`: the element without undefined, or d.
			elem := c.destructuredType(p)
			if c.Unanswered(elem) {
				return elem
			}
			d := c.TypeOf(p.Right)
			if c.Unanswered(d) {
				return d
			}
			return c.in.union(c.filter(elem, func(m *Type) bool { return m.Flags&Undefined == 0 }), d)
		}
		if lit, ok := p.Right.(*ast.ArrayLiteral); ok {
			if _, arrayPattern := p.Left.(*ast.ArrayLiteral); arrayPattern {
				// An array literal assigned to an array pattern is a tuple
				// (its contextual type is the pattern's).
				elems := make([]*Type, len(lit.Elements))
				for i, e := range lit.Elements {
					if _, spread := e.(*ast.SpreadElement); spread {
						return c.unanswered
					}
					t := c.TypeOf(e)
					if c.Unanswered(t) {
						return t
					}
					elems[i] = c.widenFrom(e, t)
				}
				return c.in.tuple(elems)
			}
		}
		return c.TypeOf(p.Right) // the whole pattern's value
	case *ast.ArrayLiteral:
		idx := -1
		for i, e := range p.Elements {
			if e == n {
				idx = i
			}
			if _, spread := e.(*ast.SpreadElement); spread && i <= idx {
				return c.unanswered
			}
		}
		arr := c.destructuredType(p)
		switch {
		case idx < 0, c.Unanswered(arr), arr.Flags&Union != 0:
			return c.unanswered
		case arr.Flags&Any != 0:
			return c.anyT
		case arr.Flags&Object != 0 && arr.Kind == Tuple:
			if idx < len(arr.Elems) {
				return arr.Elems[idx]
			}
			return c.undefinedT
		case arr.Flags&Object != 0 && arr.Kind == Array:
			return arr.Elem
		}
		return c.unanswered
	case *ast.ObjectLiteral:
		for _, prop := range p.Properties {
			if prop.Value != n || prop.AccessorKind != "" {
				continue
			}
			key := prop.Key
			if prop.KeyExpr != nil {
				kt := c.TypeOf(prop.KeyExpr)
				if kt.Flags&(StringLiteral|NumberLiteral) == 0 || kt.Member != "" {
					return c.unanswered
				}
				key = kt.Value
			}
			obj := c.destructuredType(p)
			switch {
			case c.Unanswered(obj), obj.Flags&Union != 0:
				return c.unanswered
			case obj.Flags&Any != 0:
				return c.anyT
			case obj.Flags&Object != 0 && obj.Kind == Tuple:
				if i, err := strconv.Atoi(key); err == nil && i >= 0 && i < len(obj.Elems) {
					return obj.Elems[i]
				}
			case obj.Flags&Object != 0 && (obj.Kind == Anonymous || obj.Kind == Instance || obj.Kind == Interface):
				if pr := obj.Prop(key); pr != nil {
					return pr.Type
				}
			}
			return c.unanswered
		}
	}
	return c.unanswered
}

// patternContainer reports whether n is an element or property value of an
// assignment pattern (an array or object literal being assigned to).
func (c *Checker) patternContainer(n ast.Expression) bool {
	switch p := c.parentOf(n).(type) {
	case *ast.ArrayLiteral:
		return c.inTarget(p)
	case *ast.ObjectLiteral:
		return c.inTarget(p)
	}
	return false
}

// inTarget reports whether the literal lit is (inside) an assignment's
// target.
func (c *Checker) inTarget(lit ast.Expression) bool {
	for n := lit; n != nil; {
		switch p := c.parentOf(n).(type) {
		case *ast.AssignmentExpression:
			if p.Left == n {
				return true
			}
			return false
		case *ast.ArrayLiteral, *ast.ObjectLiteral:
			n = p.(ast.Expression)
		default:
			return false
		}
	}
	return false
}
