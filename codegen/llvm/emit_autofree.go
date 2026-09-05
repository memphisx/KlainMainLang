package llvm

// emit_autofree.go — the free-emission half of TDD-00173 (`-mm=auto`,
// `@free`, `@owned`): registration of block-exit free obligations at
// declaration sites, and their drain at every exit path. The analysis half
// is escape_check.go; the per-type free itself is emit_memory.go's
// freeSymbol (the single chokepoint shared with Memory.free).
//
// Exit-path model (mirrors pendingFinallys): each registered obligation
// carries the scope depth of its declaring block. Every exit site emits the
// frees for the scopes it leaves — block fall-through (its own depth),
// break/continue (depths inside the target loop), return (all depths) —
// always AFTER any pending finallys, since a finally body may still
// legitimately read the value. Exit-path emission never pops obligations
// (an exit inside an `if` leaves the fall-through path still owing its
// free); only the declaring block's own end pops. Each runtime path thus
// frees exactly once, the same inlining discipline finallys use.
//
// Thrown paths deliberately do NOT free (emitThrow is unhooked): a throw
// caught by a catch in the same function resumes execution with outer
// bindings still live, so freeing on the unwind would be a use-after-free.
// A thrown path leaks instead — safe, and documented in the TDD.

import (
	"fmt"

	"KlainMainLang/ast"
)

// pendingFree is one registered obligation: free `sym` when leaving the
// scope at `scopeDepth`.
type pendingFree struct {
	scopeDepth int
	name       string
	sym        Symbol
	pos        ast.Pos
	// lastUse (Stage 3, @owned): free right after this statement is emitted
	// rather than at block exit; nil = block-exit only. emitted flips once
	// the early free lands, so later exit paths (which dynamically always
	// follow the last use at the same list level) skip it.
	lastUse ast.Statement
	emitted bool
	// rebindFree: this binding is reassigned via owning-fresh RHS shapes
	// only (escape_check's rebindOwningRHS) — emitAssign frees the old
	// value right before each such store.
	rebindFree bool
}

// escFreshMethodResults: builtin container methods whose result is a freshly
// allocated value (never a view into, or the retained pointer of, anything
// pre-existing) — the call-initializer forms the implicit layer may treat as
// owning. Receiver must itself be a builtin container type (checked by the
// caller via inferExprType) so a user method that happens to share a name
// doesn't qualify.
var escFreshMethodResults = map[string]bool{
	"slice": true, "map": true, "filter": true, "concat": true,
	"split": true, "flat": true, "flatMap": true, "join": true,
	"trim": true, "trimStart": true, "trimEnd": true, "toUpperCase": true,
	"toLowerCase": true, "replace": true, "replaceAll": true,
	"substring": true, "substr": true, "repeat": true, "padStart": true,
	"padEnd": true, "toReversed": true, "toSorted": true, "toSpliced": true,
	"normalize": true, "at": false, // at() can return an element pointer copy — shared, not owned
}

// maybeRegisterAutoFree runs right after a VarDeclaration has been emitted
// (symbol defined, initializer stored). It applies the emit-time half of
// eligibility — freeable type, not a boxed capture, owning initializer —
// and registers the block-exit obligation. Explicit annotations that fail
// here are compile errors; implicit candidates are silently skipped.
func (e *Emitter) maybeRegisterAutoFree(v *ast.VarDeclaration) error {
	explicit := v.Free || v.Owned
	if !explicit && !e.isAutoMode() {
		return nil
	}
	// TDD-00134 Stage 1: a stack-allocated initializer has no heap
	// allocation to free — freeing the alloca would corrupt the heap.
	// (Explicit annotations are never stack-planned, so this only skips
	// implicit candidates.)
	if v.Init != nil && e.stackAllocatedLits[v.Init] {
		return nil
	}
	pos := v.GetPos()
	reject := func(why string) error {
		if explicit {
			tag := "@free"
			if v.Owned {
				tag = "@owned"
			}
			return fmt.Errorf("%d:%d: %s on '%s' is not supported: %s", pos.Line, pos.Col, tag, v.Name, why)
		}
		return nil
	}
	if !e.autoFreePlan[v] {
		return reject("the escape analysis could not prove it stays local to its block")
	}
	if e.hoistedCaptures != nil && e.hoistedCaptures[v.Name] {
		// A captured local's storage is a heap cell shared with the closures
		// that captured it — nothing block-exit-owned to free.
		return reject("it is captured by a closure (its storage is a shared heap cell)")
	}
	sym, ok := e.lookup(v.Name)
	if !ok {
		return nil
	}
	if sym.Boxed || sym.NullableBoxed {
		return reject("its storage is boxed")
	}
	if sym.Ty.IsClass {
		// A class instance's methods may retain `this`; receiver-position
		// calls are only escape-safe for builtin containers.
		return reject("class instances are not supported (a method could retain `this`)")
	}
	if sym.Ty.IsDynamic || sym.Ty.IsDynamicObject {
		return reject("dynamic objects have no free routine")
	}
	if !symbolTypeFreeable(sym.Ty) {
		return reject("nothing heap-allocated to free for this type")
	}
	if !e.autoFreeOwningInit(v.Init, explicit) {
		// Rebind-free upgrade: a churned string binding often starts from an
		// interned literal (`let s = ""`) that owns nothing. Replace the slot
		// value with a heap copy so the binding owns its value from the
		// start — then every rebind (and the block exit) can free
		// unconditionally. One tiny allocation, only for rebind-planned
		// string bindings.
		_, isLit := v.Init.(*ast.StringLiteral)
		if !(e.autoFreeRebind[v] && isLit && isStringTy(sym.Ty)) {
			return reject("its initializer is not a provably-owned fresh allocation (a bare alias of another value cannot be block-freed)")
		}
		e.ensureStrHeaderRuntime()
		cur := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cur, sym.Ptr))
		cp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", cp, cur))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cp, sym.Ptr))
	}
	e.pendingFrees = append(e.pendingFrees, pendingFree{
		scopeDepth: len(e.scopes),
		name:       v.Name,
		sym:        sym,
		pos:        pos,
		lastUse:    e.autoOwnedLastUse[v],
		rebindFree: e.autoFreeRebind[v],
	})
	return nil
}

// maybeFreeOnRebind runs from emitAssign right before the store of a new
// value into an identifier's slot: if that binding carries a rebind-free
// obligation (registered above), the value about to be overwritten is freed
// here — the only point it is still reachable. The RHS has already been
// evaluated (it may read the old value); matching is by name AND slot
// pointer so a shadowing inner binding never triggers the outer's free.
func (e *Emitter) maybeFreeOnRebind(name string, sym Symbol, pos ast.Pos) {
	if e.blockDone {
		return
	}
	for i := range e.pendingFrees {
		pf := &e.pendingFrees[i]
		if pf.rebindFree && !pf.emitted && pf.name == name && pf.sym.Ptr == sym.Ptr {
			_ = e.freeSymbol(pf.sym, pos)
			return
		}
	}
}

// registerOwnedParam registers a callee-side @owned parameter's free
// obligation at function entry (TDD-00173 Stage 3): freed right after its
// last-use statement, with the function-exit paths as the safety net.
func (e *Emitter) registerOwnedParam(fd *ast.FunctionDeclaration, prm ast.Param) error {
	pos := fd.GetPos()
	if e.hoistedCaptures != nil && e.hoistedCaptures[prm.Name] {
		return fmt.Errorf("%d:%d: @owned parameter '%s' of '%s' is captured by a closure — its storage is a shared heap cell", pos.Line, pos.Col, prm.Name, fd.Name)
	}
	sym, ok := e.lookup(prm.Name)
	if !ok {
		return nil
	}
	if sym.Boxed || sym.NullableBoxed || sym.Ty.IsClass || sym.Ty.IsDynamic || sym.Ty.IsDynamicObject || !symbolTypeFreeable(sym.Ty) {
		return fmt.Errorf("%d:%d: @owned parameter '%s' of '%s': this type has no compiler-insertable free", pos.Line, pos.Col, prm.Name, fd.Name)
	}
	var lastUse ast.Statement
	if m := e.autoOwnedParamLastUse[fd]; m != nil {
		lastUse = m[prm.Name]
	}
	e.pendingFrees = append(e.pendingFrees, pendingFree{
		scopeDepth: len(e.scopes),
		name:       prm.Name,
		sym:        sym,
		pos:        pos,
		lastUse:    lastUse,
	})
	return nil
}

// emitOwnedFreesAfter runs right after a statement is emitted in any
// statement-list loop: obligations whose last-use statement is exactly this
// one get their early free here and are marked emitted.
func (e *Emitter) emitOwnedFreesAfter(stmt ast.Statement) {
	for i := range e.pendingFrees {
		pf := &e.pendingFrees[i]
		if pf.emitted || pf.lastUse != stmt {
			continue
		}
		if !e.blockDone {
			_ = e.freeSymbol(pf.sym, pf.pos)
		}
		pf.emitted = true
	}
}

// autoFreeOwningInit decides whether the initializer transfers ownership of
// a fresh allocation to the binding. For an explicit annotation the user is
// asserting ownership, so only the provably-wrong shapes are rejected (a
// bare alias of another binding; an interned string literal with no heap
// allocation behind it). The implicit layer requires the initializer to be
// provably fresh.
func (e *Emitter) autoFreeOwningInit(init ast.Expression, explicit bool) bool {
	if init == nil {
		return false
	}
	if explicit {
		switch x := init.(type) {
		case *ast.Identifier:
			return false // `@free let y = x` — annotate x instead
		case *ast.StringLiteral:
			return false // interned global, freeing it would crash
		case *ast.TemplateLiteral:
			return len(x.Exprs) > 0
		}
		return true
	}
	switch x := init.(type) {
	case *ast.ArrayLiteral, *ast.ObjectLiteral, *ast.NewArrayExpression,
		*ast.NewMapExpression, *ast.NewSetExpression,
		*ast.ArrowFunction, *ast.FunctionExpression:
		return true
	case *ast.TemplateLiteral:
		return len(x.Exprs) > 0 // static templates intern like literals
	case *ast.BinaryExpression:
		// The type gate already restricted this binding to a freeable type;
		// a `+` initializer of such a type is a concatenation, which
		// allocates fresh. The value-selecting operators (`??`, `||`, `&&`)
		// yield one of their OPERANDS — possibly an alias of another binding
		// or an interned literal — so freeing their result double-frees or
		// frees non-heap memory (found by the first auto-mode differential
		// run over the examples corpus: `greet(x) ?? 'default'` aborted at
		// block exit). Comparisons and arithmetic never pass the freeable
		// type gate, so `+` is the only owning binary initializer.
		return x.Op == "+"
	case *ast.CallExpression:
		// Builtin container methods with copy-out results, on a receiver the
		// type system says is a builtin container.
		if m, ok := x.Callee.(*ast.MemberExpression); ok && escFreshMethodResults[m.Property] {
			rt := e.inferExprType(m.Object)
			if rt.IsArray || (isStringTy(rt) && !rt.IsDynamic) || rt.IsMap || rt.IsSet {
				return true
			}
			// JSON.parse always builds a fresh tree; JSON.stringify a fresh
			// string.
			if ns, ok := m.Object.(*ast.Identifier); ok && ns.Name == "JSON" && (m.Property == "parse" || m.Property == "stringify") {
				return true
			}
		}
		return false
	}
	return false
}

// emitReturnCleanups runs the pending finallys, then (TDD-00173) the frees
// owed by every scope the return exits — finallys strictly first, since a
// finally body may still legitimately read a to-be-freed value. Every
// `return` lowering path in emitReturn goes through this instead of calling
// emitPendingFinallys directly. emitThrow deliberately does not (see the
// file comment).
func (e *Emitter) emitReturnCleanups() error {
	if err := e.emitPendingFinallys(); err != nil {
		return err
	}
	e.emitFreesAbove(0)
	return nil
}

// emitFreesAbove emits, innermost-first, the frees owed by every scope
// deeper than minDepth. Used by return (minDepth 0 — the whole function)
// and break/continue (the loop-entry depth recorded on the target stacks).
// Never pops: the declaring block's own end does that.
func (e *Emitter) emitFreesAbove(minDepth int) {
	for i := len(e.pendingFrees) - 1; i >= 0; i-- {
		pf := e.pendingFrees[i]
		if pf.scopeDepth <= minDepth || pf.emitted {
			continue
		}
		// freeSymbol's only error path is an unfreeable type, which
		// registration already excluded.
		_ = e.freeSymbol(pf.sym, pf.pos)
	}
}

// emitFreesAtScopeExit emits (unless the block already terminated) and pops
// the obligations belonging to the scope at the given depth — called at a
// block's fall-through end, just before its popScope.
func (e *Emitter) emitFreesAtScopeExit(depth int) {
	keep := e.pendingFrees[:0]
	for _, pf := range e.pendingFrees {
		if pf.scopeDepth == depth {
			if !e.blockDone && !pf.emitted {
				_ = e.freeSymbol(pf.sym, pf.pos)
			}
			continue
		}
		keep = append(keep, pf)
	}
	e.pendingFrees = keep
}
