package binder

import (
	"KlainMainLang/ast"
	"KlainMainLang/diag"
)

// The control-flow graph (TDD-00230 P2.2). While resolving references the
// binder threads a current flow node through the program: every reference
// records the node it was read at, and walking a node's antecedents
// backwards visits every path that can lead to that read. A function's body
// is a graph of its own, starting at a Start node, so a read inside a closure
// never reaches the enclosing function's code.

// FlowFlags is a flow node's kind.
type FlowFlags uint16

const (
	// FlowStart begins a function or the module.
	FlowStart FlowFlags = 1 << iota
	// FlowScopeStart is the entry of a block scope (Scope): its lexical
	// bindings are fresh there, each time it is entered.
	FlowScopeStart
	// FlowBranchLabel joins paths (after an if, a switch, a loop's exit).
	FlowBranchLabel
	// FlowLoopLabel is a loop's head: the entry and every back edge.
	FlowLoopLabel
	// FlowAssignment initializes and/or assigns Symbol.
	FlowAssignment
	// FlowUnreachable follows a return, throw, break or continue.
	FlowUnreachable
	// FlowTrueCondition and FlowFalseCondition follow a condition (Expr)
	// known to have been truthy or falsy: an if's branches, a loop's body and
	// exit, the right operand of && and ||.
	FlowTrueCondition
	FlowFalseCondition
	// FlowSwitchClause enters a switch's case Clause (Node, the switch) by
	// its test matching the discriminant; Clause -1, or a default clause,
	// by no case test matching.
	FlowSwitchClause
	// FlowCall follows a call through a dotted name (Node, the call), in any
	// expression position: an `asserts` function narrows its argument from
	// there on.
	FlowCall
	// FlowPropertyAssignment follows a store to a property path (Node, the
	// assignment expression): `x.a = v`.
	FlowPropertyAssignment
)

// FlowNode is one node of the graph.
type FlowNode struct {
	Flags       FlowFlags
	Antecedent  *FlowNode   // the single predecessor (not a label)
	Antecedents []*FlowNode // a label's predecessors
	Scope       *Scope      // FlowStart, FlowScopeStart
	Symbol      *Symbol     // FlowAssignment
	// Node is what stores the value (FlowAssignment: the declaration, the
	// assignment expression, the loop), or the condition (FlowTrueCondition,
	// FlowFalseCondition).
	Node ast.Node
	// Clause is a FlowSwitchClause's case index.
	Clause int
	// Init marks the declaration of Symbol having run (its temporal dead
	// zone ends); Assign marks a value being stored in it.
	Init, Assign bool
	// Outer is a FlowStart's flow where its function was created, for a
	// function expression, an arrow function, or a class expression's
	// method: a closure that sees an outer binding narrowed continues its
	// walk there (TypeScript's closure narrowing, see ClosureNarrows).
	Outer *FlowNode
}

var unreachableFlow = &FlowNode{Flags: FlowUnreachable}

// jumpTarget is where a break or continue goes.
type jumpTarget struct {
	label      string    // the statement's label, "" for none
	breakTo    *FlowNode // a branch label after the statement
	continueTo *FlowNode // for a loop: where continue goes; nil otherwise
	breakOnly  bool      // a labeled non-loop statement: only `break label` targets it
}

// flowing reports whether the graph is being built (the resolving pass).
func (b *binder) flowing() bool { return !b.declaring }

func (b *binder) newLabel(flags FlowFlags, antecedents ...*FlowNode) *FlowNode {
	l := &FlowNode{Flags: flags}
	for _, a := range antecedents {
		addAntecedent(l, a)
	}
	return l
}

func addAntecedent(label, f *FlowNode) {
	if f == nil || f == unreachableFlow {
		return
	}
	for _, a := range label.Antecedents {
		if a == f {
			return
		}
	}
	label.Antecedents = append(label.Antecedents, f)
}

// finish returns the flow after label: unreachable when nothing reaches it.
func finish(label *FlowNode) *FlowNode {
	if len(label.Antecedents) == 0 {
		return unreachableFlow
	}
	return label
}

// join returns the flow where the given paths meet.
func (b *binder) join(paths ...*FlowNode) *FlowNode {
	return finish(b.newLabel(FlowBranchLabel, paths...))
}

// assignFlow records that sym is initialized and/or assigned here.
func (b *binder) assignFlow(sym *Symbol, init, assign bool) {
	if !b.flowing() || sym == nil {
		return
	}
	b.stores[sym] = append(b.stores[sym], b.storing)
	if init {
		b.declOrd[sym] = b.ord
	} else if assign {
		b.markAssignment(sym)
	}
	if b.flow == unreachableFlow {
		return
	}
	b.flow = &FlowNode{Flags: FlowAssignment, Antecedent: b.flow, Symbol: sym, Init: init, Assign: assign, Node: b.storing}
}

// switchClause returns the flow entering case clause of sw (-1: past every
// case, no default) from flow f.
func (b *binder) switchClause(f *FlowNode, sw *ast.SwitchStatement, clause int) *FlowNode {
	if !b.flowing() || f == unreachableFlow {
		return f
	}
	return &FlowNode{Flags: FlowSwitchClause, Antecedent: f, Node: sw, Clause: clause}
}

// condition returns the flow after cond has been found truthy (or falsy)
// from flow f.
func (b *binder) condition(f *FlowNode, cond ast.Expression, truthy bool) *FlowNode {
	if !b.flowing() || f == unreachableFlow || cond == nil {
		return f
	}
	flags := FlowTrueCondition
	if !truthy {
		flags = FlowFalseCondition
	}
	return &FlowNode{Flags: flags, Antecedent: f, Node: cond}
}

// scopeStartFlow marks the entry of scope s.
func (b *binder) scopeStartFlow(s *Scope) {
	if !b.flowing() || b.flow == unreachableFlow {
		return
	}
	b.flow = &FlowNode{Flags: FlowScopeStart, Antecedent: b.flow, Scope: s}
}

// containerOf is the function (or the module) a scope's code runs in.
func containerOf(s *Scope) *Scope {
	for s.Kind != FunctionScope && s.Kind != ModuleScope || s.Inline {
		s = s.Parent
	}
	return s
}

// functionOf is the function (or the module) a scope is in, an inline
// function included.
func functionOf(s *Scope) *Scope {
	for s.Kind != FunctionScope && s.Kind != ModuleScope {
		s = s.Parent
	}
	return s
}

// walkBack reports, for the paths from f backwards, whether any meets the
// fact (found) and whether any reaches the start of sym's scope without it
// (missed). fact says whether an assignment node establishes it.
func walkBack(f *FlowNode, sym *Symbol, fact func(*FlowNode) bool) (found, missed bool) {
	home := containerOf(sym.Scope)
	onPath := map[*FlowNode]bool{}
	memo := map[*FlowNode][2]bool{}
	// walk returns found, missed, and whether a loop back into the current
	// path cut the answer short (such an answer is not cached: another path
	// may reach the same label without the cut).
	var walk func(f *FlowNode) (bool, bool, bool)
	walk = func(f *FlowNode) (bool, bool, bool) {
		for {
			if f == nil || f.Flags&FlowUnreachable != 0 {
				return false, false, false
			}
			if onPath[f] {
				return false, false, true
			}
			if r, ok := memo[f]; ok {
				return r[0], r[1], false
			}
			switch {
			case f.Flags&FlowAssignment != 0:
				if f.Symbol == sym && fact(f) {
					return true, false, false
				}
				f = f.Antecedent
				continue
			case f.Flags&FlowScopeStart != 0:
				if f.Scope == sym.Scope {
					return false, true, false
				}
				f = f.Antecedent
				continue
			case f.Flags&(FlowTrueCondition|FlowFalseCondition|FlowSwitchClause|FlowCall|FlowPropertyAssignment) != 0:
				f = f.Antecedent
				continue
			case f.Flags&FlowStart != 0:
				// The start of the binding's own function or module: nothing
				// on this path set it.
				return false, f.Scope == home, false
			}
			// A label: any predecessor path.
			onPath[f] = true
			var fd, ms, cut bool
			for _, a := range f.Antecedents {
				a1, a2, a3 := walk(a)
				fd, ms, cut = fd || a1, ms || a2, cut || a3
			}
			delete(onPath, f)
			if !cut {
				memo[f] = [2]bool{fd, ms}
			}
			return fd, ms, cut
		}
	}
	found, missed, _ = walk(f)
	return found, missed
}

// ExemptFromAssignment reports whether a declared type opts a binding out of
// the definite-assignment check: `any`/`unknown` hold `undefined` anyway, and
// a nullable type has its own absent state.
func ExemptFromAssignment(ta *ast.TypeAnnotation) bool {
	return ta != nil && (ta.Name == "any" || ta.Name == "unknown" || ta.Nullable)
}

// tdzKind reports whether sym is a binding with a temporal dead zone: a
// let, const, class or enum.
func tdzKind(sym *Symbol) bool {
	for _, d := range sym.Declarations {
		if d.Kind == Lexical && d.Flags&(BlockScopedVariable|Class|Enum) != 0 {
			return true
		}
	}
	return false
}

// trackedForAssignment reports whether reads of sym must be definitely
// assigned: a let declared with a type and without an initializer, the type
// not exempt. With an initializer, a read on a path where the declaration
// has not run is a temporal-dead-zone question, not an assignment one; an
// unannotated `let x;` has an evolving type, which TypeScript does not check;
// var is hoisted and undefined-initialized, and destructured bindings are
// always assigned.
func trackedForAssignment(sym *Symbol) bool {
	if len(sym.Declarations) != 1 {
		return false
	}
	v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration)
	return ok && v.Kind != "var" && v.Init == nil && v.TypeAnnot != nil && !ExemptFromAssignment(v.TypeAnnot)
}

// alwaysTrue reports whether a loop condition never ends the loop: absent,
// or the literal true.
func alwaysTrue(test ast.Expression) bool {
	if test == nil {
		return true
	}
	lit, ok := test.(*ast.BooleanLiteral)
	return ok && lit.Value
}

// FlowDiagnostics reports the reads the flow graph proves wrong, within the
// function that declares the binding (a read from a nested function may run
// later, and is not checked):
//   - a read of a let, const, class or enum that no path reaches after its
//     declaration has run: it always throws `Cannot access before
//     initialization`;
//   - with definite, a read of a tracked let or const that some path reaches
//     before any value was stored (TypeScript's definite assignment; plain
//     JavaScript has no such rule).
func (b *Binding) FlowDiagnostics(offset func(line, col int) int, definite bool) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	add := func(m *diag.Message, id *ast.Identifier) {
		p := id.GetPos()
		start := -1
		if offset != nil {
			start = offset(p.Line, p.Col)
		}
		end := start
		if start >= 0 {
			end = start + len(id.Name)
		}
		out = append(out, diag.New(m, diag.Span{Pos: diag.Pos{Line: p.Line, Col: p.Col}, Start: start, End: end}, id.Name))
	}
	for _, id := range b.accesses {
		sym := b.refs[id]
		f := b.refFlow[id]
		if sym == nil || f == nil || b.refContainer[id] != containerOf(sym.Scope) {
			continue
		}
		// A write in the dead zone throws too; only a read needs a value.
		if b.written[id] {
			if tdzKind(sym) {
				if found, missed := walkBack(f, sym, func(n *FlowNode) bool { return n.Init }); missed && !found {
					add(diag.UsedBeforeDeclaration, id)
				}
			}
			continue
		}
		if tdzKind(sym) {
			found, missed := walkBack(f, sym, func(n *FlowNode) bool { return n.Init })
			if missed && !found {
				add(diag.UsedBeforeDeclaration, id)
				continue
			}
		}
		if definite && trackedForAssignment(sym) {
			if _, missed := walkBack(f, sym, func(n *FlowNode) bool { return n.Assign }); missed {
				add(diag.UsedBeforeAssigned, id)
			}
		}
	}
	return out
}

// FlowAt returns the flow node at a reference, and whether the reference is
// in the function or module that declares its symbol: the flow graph of that
// container is the one that stores to it, so only there does it narrow.
func (b *Binding) FlowAt(id *ast.Identifier) (f *FlowNode, local bool) {
	sym := b.refs[id]
	f = b.refFlow[id]
	return f, sym != nil && f != nil && b.refContainer[id] == containerOf(sym.Scope)
}

// StoresTo lists the nodes that store to sym, in any function, reachable or
// not: a variable declaration, a plain or compound assignment expression, or
// nil for a store through anything else (an update, a destructuring, a loop
// variable).
func (b *Binding) StoresTo(sym *Symbol) []ast.Node { return b.stores[sym] }

// EndReachable reports whether some path reaches the end of a function's
// body, where it returns undefined.
func (b *Binding) EndReachable(body *ast.BlockStatement) bool { return b.endReachable[body] }

// markAssignment records an assignment to sym at the current position
// (TypeScript's markNodeAssignments): from a nested function it may run at
// any time; otherwise it extends to the end of the outermost statement
// enclosing it that begins after sym's declaration.
func (b *binder) markAssignment(sym *Symbol) {
	if b.fnScope != functionOf(sym.Scope) {
		b.lastAssign[sym] = maxOrd
		return
	}
	decl := b.declOrd[sym]
	for i := range b.stmts {
		if b.stmts[i].start > decl {
			b.stmts[i].assigned = append(b.stmts[i].assigned, sym)
			return
		}
	}
	if b.ord > b.lastAssign[sym] {
		b.lastAssign[sym] = b.ord
	}
}

// ClosureNarrows reports whether the reference id, in a function nested in
// the one that declares sym, sees sym narrowed as at the nested function's
// creation (TypeScript 5.4): sym is a constant, or a parameter or local
// let that is not assigned after id.
func (b *Binding) ClosureNarrows(id *ast.Identifier, sym *Symbol) bool {
	if len(sym.Declarations) == 0 {
		return false
	}
	switch d := sym.Declarations[0].Node.(type) {
	case *ast.VarDeclaration:
		if d.Kind == "const" {
			return true
		}
		if d.Kind != "let" || b.exported[d] || b.Script && sym.Scope == b.Module {
			return false
		}
	case *ast.ArrayDestructuring:
		return d.Kind == "const"
	case *ast.ObjectDestructuring:
		return d.Kind == "const"
	case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction:
		if sym.Flags&FunctionScopedVariable == 0 || sym.Scope != b.fnScopes[d] {
			return false // the function's own name, not a parameter
		}
	default:
		return false
	}
	last, assigned := b.lastAssign[sym]
	return !assigned || last < b.refOrd[id]
}

// thisScope is the scope whose `this` a `this` in scope s means: the
// nearest function that is not an arrow function (a class's field
// initializers are one), or the module.
func thisScope(s *Scope) *Scope {
	for ; s != nil; s = s.Parent {
		if s.Kind == ModuleScope {
			return s
		}
		if s.Kind == FunctionScope {
			if _, arrow := s.Node.(*ast.ArrowFunction); !arrow {
				return s
			}
		}
	}
	return s
}

// ThisAt returns a `this` expression's flow node, the symbol standing for
// the `this` it means, and whether the expression is in that function's own
// flow (an arrow function's `this` is its enclosing function's, but its
// body is a flow of its own).
func (b *Binding) ThisAt(n *ast.ThisExpression) (f *FlowNode, sym *Symbol, local bool) {
	return b.thisFlow[n], b.thisOwner[n], b.thisLocal[n]
}

// SuperAt is ThisAt for a `super` expression: the same receiver.
func (b *Binding) SuperAt(n *ast.SuperExpression) (f *FlowNode, sym *Symbol, local bool) {
	return b.thisFlow[n], b.thisOwner[n], b.thisLocal[n]
}

// NamespaceScope is the scope a namespace symbol's members are declared in,
// or nil for a symbol that is not a namespace.
func (b *Binding) NamespaceScope(sym *Symbol) *Scope { return b.nsScopes[sym] }

// Exported reports whether an `export` wraps the top-level declaration n.
func (b *Binding) Exported(n ast.Node) bool { return b.exported[n] }

// EndFlow is the flow node at the end of a function's body, or nil when no
// path reaches it.
func (b *Binding) EndFlow(body *ast.BlockStatement) *FlowNode { return b.endFlow[body] }
