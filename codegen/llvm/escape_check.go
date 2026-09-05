package llvm

// escape_check.go — the conservative escape analysis behind `-mm=auto` and
// the `/** @free */` / `/** @owned */` annotations (TDD-00173).
//
// The question it answers, per variable: can this binding's *top-level heap
// allocation* be freed at its declaring block's exit without any live alias
// outliving the free? "Shallow free" semantics (emit_memory.go) make the
// analysis tractable: freeing x only invalidates x's own allocation (string
// bytes, array data buffer, map header+key/val arrays, closure header+env,
// object struct) — never anything merely reachable *through* x. So the only
// dangerous uses are the ones that can propagate x's own top-level pointer
// somewhere that outlives the block: `return x`, `throw x`, storing/aliasing
// it, passing it to a call that may retain it, capturing it in a closure, or
// reassigning the binding out from under the planned free. Reads that copy —
// `x.length`, `x[i]`, `x.foo`, comparisons, string coercion — are safe.
//
// The pass is strictly MORE conservative than pure_check.go's (its
// documented gaps under-report style violations; a gap here would be a
// use-after-free): any appearance of the identifier in a construct this file
// does not positively recognize as safe is treated as escaping. For an
// explicit annotation that is a compile error naming the reason; for auto
// mode's implicit layer it silently skips (the value leaks, exactly as every
// value does in manual mode — TDD-00001's definition of the mode).
//
// Run from EmitProgram (like checkPurity), producing e.autoFreePlan:
// the set of VarDeclarations whose *flow* is proven safe. The type-level
// half of eligibility (freeable type, not a captured/boxed local, owning
// initializer) is decided at emission time in maybeRegisterAutoFree
// (emit_exprs_vardecl.go), where the symbol's resolved type exists.

import (
	"fmt"

	"KlainMainLang/ast"
)

// escCallWhitelist: callees that read their arguments without retaining any
// pointer (audited against the emitted runtime, not JS semantics): the
// console printers format and copy, String() copies into a new string.
// Grown only with an explicit does-not-retain audit. Keyed "ns.method" for
// member callees, bare name otherwise.
var escCallWhitelist = map[string]bool{
	"console.log": true, "console.error": true, "console.warn": true,
	"console.info": true, "console.debug": true,
	"String": true,
}

// planAutoFrees walks the whole program and records, for every variable the
// mode/annotations make a candidate, whether its flow provably keeps the
// value block-local. Explicit @free/@owned candidates that fail return a
// compile error immediately; implicit (auto-mode) candidates that fail are
// simply not recorded.
func (e *Emitter) planAutoFrees(prog *ast.Program) error {
	p := &escPlanner{
		auto:         e.isAutoMode(),
		plan:         map[*ast.VarDeclaration]bool{},
		rebinds:      map[*ast.VarDeclaration]bool{},
		lastUse:      map[*ast.VarDeclaration]ast.Statement{},
		ownedFns:     map[string]*ast.FunctionDeclaration{},
		paramLastUse: map[*ast.FunctionDeclaration]map[string]ast.Statement{},
	}
	// TDD-00163 Stage 5: a FinalizationRegistry register-TARGET position is
	// a free-notified retention, not a disqualifying escape (the inserted
	// free runs the onfree hook, which fires the cleanup and deadens the
	// cell). Not under -mm=gc — there the death signal is the Boehm
	// finalizer, and an early free must not race it, so registration keeps
	// counting as an escape.
	if !e.isGCMode() {
		p.finregNames = collectFinRegNames(prog)
	}
	// Pre-pass: collect the functions declaring @owned parameters (Stage 3),
	// analyze each owned param's flow in its own body, and record its
	// last-use statement.
	if err := p.collectOwnedFns(prog.Body); err != nil {
		return err
	}
	if err := p.walkStmts(prog.Body, false); err != nil {
		return err
	}
	// Caller-side @owned-param contract: every call site must hand over a
	// value the caller provably no longer uses.
	if len(p.ownedFns) > 0 {
		if err := p.checkOwnedCalls(prog.Body, nil); err != nil {
			return err
		}
	}
	e.autoFreePlan = p.plan
	e.autoFreeRebind = p.rebinds
	e.autoOwnedLastUse = p.lastUse
	e.autoOwnedParamLastUse = p.paramLastUse
	return nil
}

type escPlanner struct {
	auto bool
	// stack (TDD-00134 Stage 1, -optimize-memory): this run plans stack
	// allocations, not frees — candidacy narrows to implicit let/const object
	// literals, verdicts land in `plan` all the same, and the finreg
	// register-target exemption is off (a stack value's scope exit emits no
	// free, so the registry would never be notified).
	stack       bool
	finregNames map[string]bool
	plan        map[*ast.VarDeclaration]bool
	// rebinds: plan-approved bindings that are reassigned via owning-fresh
	// RHS shapes (rebindOwningRHS) — the emitter frees the old value at each
	// such store. nil for the stack planner (never populated there).
	rebinds map[*ast.VarDeclaration]bool
	// lastUse (Stage 3): for an @owned local binding, the last statement in
	// its declaring list whose subtree mentions it — the free lands right
	// after that statement instead of at block exit. nil/absent = block-exit
	// fallback (ambiguous placement: last use inside try, or unreachable-
	// after exits).
	lastUse map[*ast.VarDeclaration]ast.Statement
	// ownedFns / paramLastUse (Stage 3): functions declaring @owned params,
	// and each owned param's last-use statement in the callee body.
	ownedFns     map[string]*ast.FunctionDeclaration
	paramLastUse map[*ast.FunctionDeclaration]map[string]ast.Statement
}

// lastUseStmt returns the free point for @owned: the last statement in list
// mentioning name — unless that statement is a try (the finally/unwind
// machinery owns cleanup there) or an abrupt exit (return/break/continue,
// whose own cleanup path already frees), in which case nil means "fall back
// to block exit".
func (c *escChecker) lastUseStmt(list []ast.Statement) ast.Statement {
	var last ast.Statement
	for _, s := range list {
		if c.mentionsStmts([]ast.Statement{s}) {
			last = s
		}
	}
	switch last.(type) {
	case *ast.TryStatement, *ast.ReturnStatement, *ast.BreakStatement, *ast.ContinueStatement, *ast.ThrowStatement:
		return nil
	}
	return last
}

// collectOwnedFns finds every function declaration carrying @owned params
// (top level and nested), escape-checks each owned param against the body,
// and records its last-use statement.
func (p *escPlanner) collectOwnedFns(stmts []ast.Statement) error {
	for _, stmt := range stmts {
		fd, ok := stmt.(*ast.FunctionDeclaration)
		if !ok || fd.Body == nil {
			continue
		}
		for _, prm := range fd.Params {
			if !prm.Owned {
				continue
			}
			c := &escChecker{name: prm.Name}
			if v := c.stmts(fd.Body.Body); v != nil {
				return fmt.Errorf("%d:%d: @owned parameter '%s' of '%s' may escape — %s at %d:%d", fd.GetPos().Line, fd.GetPos().Col, prm.Name, pureDisplayName(fd.Name), v.reason, v.pos.Line, v.pos.Col)
			}
			if p.paramLastUse[fd] == nil {
				p.paramLastUse[fd] = map[string]ast.Statement{}
			}
			p.paramLastUse[fd][prm.Name] = c.lastUseStmt(fd.Body.Body)
			p.ownedFns[fd.Name] = fd
		}
	}
	return nil
}

// checkOwnedCalls enforces the caller side of the @owned-param contract:
// each argument bound to an @owned param must be a value the caller
// provably hands over — a fresh allocation expression, or a local whose name
// never appears again in any following statement at any enclosing level
// (restStack). A reference to an @owned-param function outside direct call
// position is rejected — the contract can't follow a function value.
func (p *escPlanner) checkOwnedCalls(stmts []ast.Statement, restStack [][]ast.Statement) error {
	for i, stmt := range stmts {
		rest := make([][]ast.Statement, 0, len(restStack)+1)
		rest = append(rest, restStack...)
		rest = append(rest, stmts[i+1:])
		if err := p.checkOwnedCallsStmt(stmt, rest); err != nil {
			return err
		}
	}
	return nil
}

func (p *escPlanner) checkOwnedCallsStmt(stmt ast.Statement, rest [][]ast.Statement) error {
	switch s := stmt.(type) {
	case *ast.VarDeclaration:
		return p.checkOwnedCallsExpr(s.Init, rest)
	case *ast.VarDeclarationList:
		for _, d := range s.Decls {
			if err := p.checkOwnedCallsExpr(d.Init, rest); err != nil {
				return err
			}
		}
	case *ast.ExpressionStatement:
		return p.checkOwnedCallsExpr(s.Expr, rest)
	case *ast.ReturnStatement:
		return p.checkOwnedCallsExpr(s.Value, rest)
	case *ast.ThrowStatement:
		return p.checkOwnedCallsExpr(s.Argument, rest)
	case *ast.BlockStatement:
		return p.checkOwnedCalls(s.Body, rest)
	case *ast.IfStatement:
		if err := p.checkOwnedCallsExpr(s.Test, rest); err != nil {
			return err
		}
		if err := p.checkOwnedCalls(s.Consequent.Body, rest); err != nil {
			return err
		}
		if s.Alternate != nil {
			return p.checkOwnedCallsStmt(s.Alternate, rest)
		}
	case *ast.ForStatement:
		if s.Init != nil {
			if err := p.checkOwnedCallsStmt(s.Init, rest); err != nil {
				return err
			}
		}
		if err := p.checkOwnedCallsExpr(s.Test, rest); err != nil {
			return err
		}
		for _, u := range s.Update {
			if err := p.checkOwnedCallsExpr(u, rest); err != nil {
				return err
			}
		}
		return p.checkOwnedCalls(s.Body.Body, append(rest, s.Body.Body))
	case *ast.ForOfStatement:
		return p.checkOwnedCalls(s.Body.Body, append(rest, s.Body.Body))
	case *ast.ForInStatement:
		return p.checkOwnedCalls(s.Body.Body, append(rest, s.Body.Body))
	case *ast.WhileStatement:
		if err := p.checkOwnedCallsExpr(s.Test, rest); err != nil {
			return err
		}
		return p.checkOwnedCalls(s.Body.Body, append(rest, s.Body.Body))
	case *ast.DoWhileStatement:
		return p.checkOwnedCalls(s.Body.Body, append(rest, s.Body.Body))
	case *ast.SwitchStatement:
		for _, cs := range s.Cases {
			if err := p.checkOwnedCalls(cs.Body, rest); err != nil {
				return err
			}
		}
	case *ast.TryStatement:
		if err := p.checkOwnedCalls(s.Body.Body, rest); err != nil {
			return err
		}
		if s.Catch != nil {
			if err := p.checkOwnedCalls(s.Catch.Body.Body, rest); err != nil {
				return err
			}
		}
		if s.Finally != nil {
			return p.checkOwnedCalls(s.Finally.Body, rest)
		}
	case *ast.LabeledStatement:
		return p.checkOwnedCallsStmt(s.Body, rest)
	case *ast.FunctionDeclaration:
		if s.Body != nil {
			return p.checkOwnedCalls(s.Body.Body, nil)
		}
	}
	return nil
}

func (p *escPlanner) checkOwnedCallsExpr(expr ast.Expression, rest [][]ast.Statement) error {
	switch x := expr.(type) {
	case nil:
		return nil
	case *ast.Identifier:
		if fd, ok := p.ownedFns[x.Name]; ok {
			return fmt.Errorf("%d:%d: function '%s' declares @owned parameters and cannot be used as a value — the ownership contract can't follow a function reference", x.GetPos().Line, x.GetPos().Col, pureDisplayName(fd.Name))
		}
	case *ast.CallExpression:
		if id, ok := x.Callee.(*ast.Identifier); ok {
			if fd, exists := p.ownedFns[id.Name]; exists {
				if err := p.checkOwnedArgs(fd, x, rest); err != nil {
					return err
				}
			}
		} else if err := p.checkOwnedCallsExpr(x.Callee, rest); err != nil {
			return err
		}
		for _, a := range x.Args {
			if err := p.checkOwnedCallsExpr(a, rest); err != nil {
				return err
			}
		}
	case *ast.BinaryExpression:
		if err := p.checkOwnedCallsExpr(x.Left, rest); err != nil {
			return err
		}
		return p.checkOwnedCallsExpr(x.Right, rest)
	case *ast.UnaryExpression:
		return p.checkOwnedCallsExpr(x.Arg, rest)
	case *ast.AssignmentExpression:
		if err := p.checkOwnedCallsExpr(x.Left, rest); err != nil {
			return err
		}
		return p.checkOwnedCallsExpr(x.Right, rest)
	case *ast.ConditionalExpression:
		if err := p.checkOwnedCallsExpr(x.Test, rest); err != nil {
			return err
		}
		if err := p.checkOwnedCallsExpr(x.Consequent, rest); err != nil {
			return err
		}
		return p.checkOwnedCallsExpr(x.Alternate, rest)
	case *ast.MemberExpression:
		return p.checkOwnedCallsExpr(x.Object, rest)
	case *ast.IndexExpression:
		if err := p.checkOwnedCallsExpr(x.Object, rest); err != nil {
			return err
		}
		return p.checkOwnedCallsExpr(x.Index, rest)
	case *ast.ArrayLiteral:
		for _, el := range x.Elements {
			if err := p.checkOwnedCallsExpr(el, rest); err != nil {
				return err
			}
		}
	case *ast.ObjectLiteral:
		for _, pr := range x.Properties {
			if err := p.checkOwnedCallsExpr(pr.Value, rest); err != nil {
				return err
			}
		}
	case *ast.TemplateLiteral:
		for _, ex := range x.Exprs {
			if err := p.checkOwnedCallsExpr(ex, rest); err != nil {
				return err
			}
		}
	case *ast.ArrowFunction:
		if x.Block != nil {
			return p.checkOwnedCalls(x.Block.Body, nil)
		}
		return p.checkOwnedCallsExpr(x.Body, nil)
	case *ast.FunctionExpression:
		return p.checkOwnedCalls(x.Body.Body, nil)
	}
	return nil
}

// checkOwnedArgs verifies each argument bound to an @owned parameter is
// provably dead in the caller after this call.
func (p *escPlanner) checkOwnedArgs(fd *ast.FunctionDeclaration, call *ast.CallExpression, rest [][]ast.Statement) error {
	for i, prm := range fd.Params {
		if !prm.Owned || i >= len(call.Args) {
			continue
		}
		arg := call.Args[i]
		switch a := arg.(type) {
		case *ast.ArrayLiteral, *ast.ObjectLiteral, *ast.NewArrayExpression,
			*ast.NewMapExpression, *ast.NewSetExpression, *ast.TemplateLiteral,
			*ast.BinaryExpression, *ast.CallExpression:
			// A fresh (or at least expression-produced, never re-usable)
			// value — nothing in the caller can name it again.
			continue
		case *ast.Identifier:
			c := &escChecker{name: a.Name}
			for _, list := range rest {
				if c.mentionsStmts(list) {
					return fmt.Errorf("%d:%d: argument '%s' to @owned parameter '%s' of '%s' is used again after this call — the callee frees it; pass a value the caller no longer needs", call.GetPos().Line, call.GetPos().Col, pureDisplayName(a.Name), prm.Name, pureDisplayName(fd.Name))
				}
			}
			continue
		default:
			return fmt.Errorf("%d:%d: argument to @owned parameter '%s' of '%s' must be a fresh value or a local variable", call.GetPos().Line, call.GetPos().Col, prm.Name, pureDisplayName(fd.Name))
		}
	}
	return nil
}

// walkStmts scans one statement list: analyzes each candidate declaration
// against the statements following it in the same list, and recurses into
// nested blocks and function bodies (each function body is its own analysis
// region with its own suspension flag).
func (p *escPlanner) walkStmts(stmts []ast.Statement, suspends bool) error {
	for i, stmt := range stmts {
		if err := p.walkStmt(stmt, stmts[i+1:], suspends); err != nil {
			return err
		}
	}
	return nil
}

func (p *escPlanner) walkStmt(stmt ast.Statement, following []ast.Statement, suspends bool) error {
	switch s := stmt.(type) {
	case *ast.VarDeclaration:
		if err := p.analyzeDecl(s, nil, following, suspends); err != nil {
			return err
		}
		return p.walkExpr(s.Init, suspends)
	case *ast.VarDeclarationList:
		// The parser rejects @free/@owned on multi-declarator statements, so
		// these are only ever implicit candidates. A later sibling's
		// initializer can alias an earlier one (`let a = [1], b = a`), so
		// each declarator's analysis also sees its younger siblings' inits.
		for di, d := range s.Decls {
			var siblingInits []ast.Expression
			for _, sib := range s.Decls[di+1:] {
				if sib.Init != nil {
					siblingInits = append(siblingInits, sib.Init)
				}
			}
			if err := p.analyzeDecl(d, siblingInits, following, suspends); err != nil {
				return err
			}
			if err := p.walkExpr(d.Init, suspends); err != nil {
				return err
			}
		}
	case *ast.ExpressionStatement:
		return p.walkExpr(s.Expr, suspends)
	case *ast.ReturnStatement:
		return p.walkExpr(s.Value, suspends)
	case *ast.ThrowStatement:
		return p.walkExpr(s.Argument, suspends)
	case *ast.BlockStatement:
		return p.walkStmts(s.Body, suspends)
	case *ast.IfStatement:
		if err := p.walkExpr(s.Test, suspends); err != nil {
			return err
		}
		if err := p.walkStmts(s.Consequent.Body, suspends); err != nil {
			return err
		}
		if s.Alternate != nil {
			return p.walkStmt(s.Alternate, nil, suspends)
		}
	case *ast.ForStatement:
		if s.Init != nil {
			if err := p.walkStmt(s.Init, nil, suspends); err != nil {
				return err
			}
		}
		if err := p.walkExpr(s.Test, suspends); err != nil {
			return err
		}
		for _, u := range s.Update {
			if err := p.walkExpr(u, suspends); err != nil {
				return err
			}
		}
		return p.walkStmts(s.Body.Body, suspends)
	case *ast.ForOfStatement:
		if err := p.walkExpr(s.Iterable, suspends); err != nil {
			return err
		}
		return p.walkStmts(s.Body.Body, suspends)
	case *ast.ForInStatement:
		if err := p.walkExpr(s.Object, suspends); err != nil {
			return err
		}
		return p.walkStmts(s.Body.Body, suspends)
	case *ast.WhileStatement:
		if err := p.walkExpr(s.Test, suspends); err != nil {
			return err
		}
		return p.walkStmts(s.Body.Body, suspends)
	case *ast.DoWhileStatement:
		if err := p.walkExpr(s.Test, suspends); err != nil {
			return err
		}
		return p.walkStmts(s.Body.Body, suspends)
	case *ast.SwitchStatement:
		if err := p.walkExpr(s.Discriminant, suspends); err != nil {
			return err
		}
		for _, cs := range s.Cases {
			if err := p.walkExpr(cs.Test, suspends); err != nil {
				return err
			}
			if err := p.walkStmts(cs.Body, suspends); err != nil {
				return err
			}
		}
	case *ast.TryStatement:
		if err := p.walkStmts(s.Body.Body, suspends); err != nil {
			return err
		}
		if s.Catch != nil {
			if err := p.walkStmts(s.Catch.Body.Body, suspends); err != nil {
				return err
			}
		}
		if s.Finally != nil {
			return p.walkStmts(s.Finally.Body, suspends)
		}
	case *ast.LabeledStatement:
		return p.walkStmt(s.Body, following, suspends)
	case *ast.FunctionDeclaration:
		if s.Body != nil {
			return p.walkStmts(s.Body.Body, s.IsAsync || s.IsGenerator)
		}
	case *ast.ArrayDestructuring:
		return p.walkExpr(s.Init, suspends)
	case *ast.ObjectDestructuring:
		return p.walkExpr(s.Init, suspends)
	}
	return nil
}

// walkExpr descends into expressions only far enough to find nested function
// bodies (whose own declarations are candidates too). A closure buried in an
// expression shape not listed here is simply not scanned — its declarations
// are never candidates, which is safe (they leak like today) and the
// emit-time backstop errors on an explicit annotation that was never
// analyzed.
func (p *escPlanner) walkExpr(expr ast.Expression, suspends bool) error {
	switch x := expr.(type) {
	case nil:
		return nil
	case *ast.ArrowFunction:
		fnSuspends := x.IsAsync
		if x.Block != nil {
			return p.walkStmts(x.Block.Body, fnSuspends)
		}
		return p.walkExpr(x.Body, fnSuspends)
	case *ast.FunctionExpression:
		return p.walkStmts(x.Body.Body, x.IsAsync || x.IsGenerator)
	case *ast.CallExpression:
		if err := p.walkExpr(x.Callee, suspends); err != nil {
			return err
		}
		for _, a := range x.Args {
			if err := p.walkExpr(a, suspends); err != nil {
				return err
			}
		}
	case *ast.BinaryExpression:
		if err := p.walkExpr(x.Left, suspends); err != nil {
			return err
		}
		return p.walkExpr(x.Right, suspends)
	case *ast.UnaryExpression:
		return p.walkExpr(x.Arg, suspends)
	case *ast.AssignmentExpression:
		if err := p.walkExpr(x.Left, suspends); err != nil {
			return err
		}
		return p.walkExpr(x.Right, suspends)
	case *ast.ConditionalExpression:
		if err := p.walkExpr(x.Test, suspends); err != nil {
			return err
		}
		if err := p.walkExpr(x.Consequent, suspends); err != nil {
			return err
		}
		return p.walkExpr(x.Alternate, suspends)
	case *ast.MemberExpression:
		return p.walkExpr(x.Object, suspends)
	case *ast.IndexExpression:
		if err := p.walkExpr(x.Object, suspends); err != nil {
			return err
		}
		return p.walkExpr(x.Index, suspends)
	case *ast.ArrayLiteral:
		for _, el := range x.Elements {
			if err := p.walkExpr(el, suspends); err != nil {
				return err
			}
		}
	case *ast.ObjectLiteral:
		for _, pr := range x.Properties {
			if err := p.walkExpr(pr.Value, suspends); err != nil {
				return err
			}
		}
	case *ast.TemplateLiteral:
		for _, ex := range x.Exprs {
			if err := p.walkExpr(ex, suspends); err != nil {
				return err
			}
		}
	case *ast.AwaitExpression:
		return p.walkExpr(x.Argument, suspends)
	case *ast.SpreadElement:
		return p.walkExpr(x.Arg, suspends)
	case *ast.SequenceExpression:
		for _, ex := range x.Exprs {
			if err := p.walkExpr(ex, suspends); err != nil {
				return err
			}
		}
	}
	return nil
}

// analyzeDecl decides one candidate declaration. siblingInits are the
// initializers of younger declarators in the same multi-declarator statement
// (checked as potential aliasing sites before the following statements).
func (p *escPlanner) analyzeDecl(d *ast.VarDeclaration, siblingInits []ast.Expression, following []ast.Statement, suspends bool) error {
	explicit := d.Free || d.Owned
	if p.stack {
		// Stack-allocation candidacy (TDD-00134 Stage 1): implicit let/const
		// bindings initialized by an object literal or a closure literal
		// (arrow / function expression — their {fn,env} header and env are
		// fixed-size; the shared capture cells stay heap regardless). An
		// explicit @free/@owned asked for a heap free — honoring that keeps
		// the annotation's meaning intact, so it is excluded here. Async
		// closures are excluded: invoking one hands the header/env to a task
		// that can outlive the block.
		if explicit || d.Kind == "var" || suspends {
			return nil
		}
		switch init := d.Init.(type) {
		case *ast.ObjectLiteral:
		case *ast.ArrowFunction:
			if init.IsAsync {
				return nil
			}
		case *ast.FunctionExpression:
			if init.IsAsync || init.IsGenerator {
				return nil
			}
		case *ast.ArrayLiteral:
			// Only a tuple-annotated literal (`const t: [A, B] = [...]`) —
			// a fixed-shape struct like an object literal. Plain arrays'
			// growable data buffers are Stage 2 territory.
			if d.TypeAnnot == nil || len(d.TypeAnnot.TupleElems) == 0 {
				return nil
			}
		case *ast.NewExpression:
			// A class instance: flow candidacy here; class-level soundness
			// (constructor/methods provably never leak `this`) is decided at
			// emission time where ClassInfo exists — an ineligible class
			// silently keeps the heap path.
			if init.ClassName == "" {
				return nil
			}
		default:
			return nil
		}
	} else {
		if !explicit && !p.auto {
			return nil
		}
		if !explicit && d.Kind == "var" {
			// `var` is function-scoped/hoisted — block-exit placement doesn't
			// match its lifetime; implicit candidates are let/const only.
			return nil
		}
		if suspends {
			if explicit {
				return fmt.Errorf("%d:%d: @free/@owned on '%s' inside an async or generator function is unsupported — the value's lifetime spans suspension points", d.GetPos().Line, d.GetPos().Col, d.Name)
			}
			return nil
		}
	}
	c := &escChecker{name: d.Name, finregNames: p.finregNames,
		allowRebind: !explicit && p.auto && !p.stack}
	var v *escViolation
	for _, init := range siblingInits {
		if c.aliases(init) {
			v = &escViolation{reason: "aliased by a sibling declarator in the same statement", pos: d.GetPos()}
			break
		}
		if v = c.expr(init); v != nil {
			break
		}
	}
	if v == nil {
		v = c.stmts(following)
	}
	if v != nil {
		if explicit {
			tag := "@free"
			if d.Owned {
				tag = "@owned"
			}
			return fmt.Errorf("%d:%d: %s variable '%s' may escape its block — %s at %d:%d; remove the annotation or the escaping use", d.GetPos().Line, d.GetPos().Col, tag, pureDisplayName(d.Name), v.reason, v.pos.Line, v.pos.Col)
		}
		return nil
	}
	p.plan[d] = true
	if c.rebound {
		p.rebinds[d] = true
	}
	if d.Owned {
		// Stage 3: free right after the last statement that mentions it —
		// nil (no use, or ambiguous placement) falls back to block exit.
		if lu := c.lastUseStmt(following); lu != nil {
			p.lastUse[d] = lu
		}
	}
	return nil
}

// rebindOwningRHS reports whether an assignment's RHS provably produces a
// fresh owned allocation, so the old value can be freed at the store. `+=`
// on a freeable type is a concatenation (fresh buffer); `= a + b` likewise;
// an interpolating template stringifies into a fresh buffer. Everything
// else (a bare alias, a literal, `??`/`||` value-selection) is not owning.
func rebindOwningRHS(x *ast.AssignmentExpression) bool {
	if x.Op == "+=" {
		return true
	}
	if x.Op != "=" {
		return false
	}
	switch r := x.Right.(type) {
	case *ast.BinaryExpression:
		return r.Op == "+"
	case *ast.TemplateLiteral:
		return len(r.Exprs) > 0
	}
	return false
}

type escViolation struct {
	reason string
	pos    ast.Pos
}

// escChecker classifies every appearance of one identifier in its remaining
// scope. Bare reads in consuming positions are fine; anything that can
// propagate the top-level pointer beyond the block is a violation; any
// construct not positively recognized is a violation if it mentions the name.
type escChecker struct {
	name string
	// allowRebind: the implicit auto layer may accept owning reassignments
	// (see rebindOwningRHS) instead of disqualifying; rebound records that
	// at least one was seen, so the emitter knows to free-on-rebind. Off for
	// explicit @free/@owned (annotation semantics unchanged) and the stack
	// planner (a stack value must never be freed).
	allowRebind bool
	rebound     bool
	// finregNames (TDD-00163 Stage 5, free-check only): identifiers proven to
	// be FinalizationRegistry bindings program-wide. Passing the candidate as
	// `reg.register`'s TARGET does retain its pointer, but that retention is
	// free-notified — the inserted free runs the __kml_finreg_onfree hook,
	// which fires the cleanup callback and marks the cell dead, and a dead
	// cell's target pointer is only ever compared, never dereferenced. So it
	// is not a disqualifying escape. nil (the stack planner, gc mode, or no
	// registries) means no exemption.
	finregNames map[string]bool
}

// mentionsExpr / mentionsStmts: does the subtree reference the identifier at
// all (shadowing-aware, via the same free-variable scanners closure capture
// analysis uses)?
func (c *escChecker) mentionsExpr(expr ast.Expression) bool {
	if expr == nil {
		return false
	}
	res := map[string]bool{}
	scanExprFV(expr, map[string]bool{}, res)
	return res[c.name]
}

func (c *escChecker) mentionsStmts(stmts []ast.Statement) bool {
	res := map[string]bool{}
	scanStmtsFV(stmts, map[string]bool{}, res)
	return res[c.name]
}

// aliases reports whether evaluating expr can yield the variable's own
// top-level pointer (the value that becomes invalid at the inserted free).
// Reads through the value (member/index/method results) yield copies under
// this compiler's copy-on-read semantics and are NOT aliases.
func (c *escChecker) aliases(expr ast.Expression) bool {
	switch x := expr.(type) {
	case *ast.Identifier:
		return x.Name == c.name
	case *ast.ConditionalExpression:
		return c.aliases(x.Consequent) || c.aliases(x.Alternate)
	case *ast.AssignmentExpression:
		return c.aliases(x.Right)
	case *ast.SequenceExpression:
		if len(x.Exprs) > 0 {
			return c.aliases(x.Exprs[len(x.Exprs)-1])
		}
	}
	return false
}

func (c *escChecker) stmts(list []ast.Statement) *escViolation {
	for _, s := range list {
		if v := c.stmt(s); v != nil {
			return v
		}
	}
	return nil
}

func (c *escChecker) stmt(stmt ast.Statement) *escViolation {
	switch s := stmt.(type) {
	case nil:
		return nil
	case *ast.VarDeclaration:
		if s.Name == c.name {
			// A shadowing redeclaration in a nested block: uses beyond it
			// refer to the new binding — but distinguishing them requires
			// scope tracking this pass doesn't do. Conservative: reject.
			return &escViolation{reason: fmt.Sprintf("shadowed by a redeclaration of '%s'", c.name), pos: s.GetPos()}
		}
		if c.aliases(s.Init) {
			return &escViolation{reason: fmt.Sprintf("aliased by declaration of '%s'", s.Name), pos: s.GetPos()}
		}
		return c.expr(s.Init)
	case *ast.VarDeclarationList:
		for _, d := range s.Decls {
			if v := c.stmt(d); v != nil {
				return v
			}
		}
	case *ast.ExpressionStatement:
		return c.expr(s.Expr)
	case *ast.ReturnStatement:
		if c.aliases(s.Value) {
			return &escViolation{reason: "returned from the enclosing function", pos: s.GetPos()}
		}
		return c.expr(s.Value)
	case *ast.ThrowStatement:
		if c.aliases(s.Argument) {
			return &escViolation{reason: "thrown", pos: s.GetPos()}
		}
		return c.expr(s.Argument)
	case *ast.BlockStatement:
		return c.stmts(s.Body)
	case *ast.IfStatement:
		if v := c.expr(s.Test); v != nil {
			return v
		}
		if v := c.stmts(s.Consequent.Body); v != nil {
			return v
		}
		if s.Alternate != nil {
			return c.stmt(s.Alternate)
		}
	case *ast.ForStatement:
		if s.Init != nil {
			if v := c.stmt(s.Init); v != nil {
				return v
			}
		}
		if v := c.expr(s.Test); v != nil {
			return v
		}
		for _, u := range s.Update {
			if v := c.expr(u); v != nil {
				return v
			}
		}
		return c.stmts(s.Body.Body)
	case *ast.ForOfStatement:
		// Iterating x copies each element out of the buffer — a safe read
		// even when the iterable IS x.
		if !c.aliases(s.Iterable) {
			if v := c.expr(s.Iterable); v != nil {
				return v
			}
		}
		return c.stmts(s.Body.Body)
	case *ast.ForInStatement:
		if !c.aliases(s.Object) {
			if v := c.expr(s.Object); v != nil {
				return v
			}
		}
		return c.stmts(s.Body.Body)
	case *ast.WhileStatement:
		if v := c.expr(s.Test); v != nil {
			return v
		}
		return c.stmts(s.Body.Body)
	case *ast.DoWhileStatement:
		if v := c.expr(s.Test); v != nil {
			return v
		}
		return c.stmts(s.Body.Body)
	case *ast.SwitchStatement:
		if v := c.expr(s.Discriminant); v != nil {
			return v
		}
		for _, cs := range s.Cases {
			if v := c.expr(cs.Test); v != nil {
				return v
			}
			if v := c.stmts(cs.Body); v != nil {
				return v
			}
		}
	case *ast.TryStatement:
		if v := c.stmts(s.Body.Body); v != nil {
			return v
		}
		if s.Catch != nil {
			if v := c.stmts(s.Catch.Body.Body); v != nil {
				return v
			}
		}
		if s.Finally != nil {
			return c.stmts(s.Finally.Body)
		}
	case *ast.LabeledStatement:
		return c.stmt(s.Body)
	case *ast.BreakStatement, *ast.ContinueStatement:
		return nil
	case *ast.FunctionDeclaration:
		if s.Body != nil && c.mentionsStmts(s.Body.Body) {
			return &escViolation{reason: fmt.Sprintf("captured by nested function '%s'", s.Name), pos: s.GetPos()}
		}
	case *ast.ArrayDestructuring:
		if c.mentionsExpr(s.Init) {
			return &escViolation{reason: "destructured from", pos: s.GetPos()}
		}
	case *ast.ObjectDestructuring:
		if c.mentionsExpr(s.Init) {
			return &escViolation{reason: "destructured from", pos: s.GetPos()}
		}
	default:
		if c.mentionsStmts([]ast.Statement{stmt}) {
			return &escViolation{reason: "used in a construct the escape analysis doesn't model", pos: stmt.GetPos()}
		}
	}
	return nil
}

func (c *escChecker) expr(expr ast.Expression) *escViolation {
	switch x := expr.(type) {
	case nil:
		return nil
	case *ast.Identifier:
		// A bare read in a position the caller already classified as safe.
		return nil
	case *ast.AssignmentExpression:
		if id, ok := x.Left.(*ast.Identifier); ok && id.Name == c.name {
			// Rebind-free (implicit auto layer only): a reassignment whose
			// RHS is a provably fresh allocation (`s += ...` → concat; `s = a
			// + b`; an interpolating template) doesn't escape the old value —
			// it drops it. The emitter frees the old value right before the
			// store (emitAssign), so the binding churns without leaking; the
			// block-exit free then targets whichever value is last. Any other
			// reassignment shape still disqualifies.
			if c.allowRebind && rebindOwningRHS(x) {
				c.rebound = true
				return c.expr(x.Right)
			}
			return &escViolation{reason: "reassigned (the block-exit free would target the wrong value)", pos: x.GetPos()}
		}
		// Writes INTO the value (x.f = …, x[i] = …) don't move its own
		// pointer; the left side only needs its sub-expressions scanned.
		if v := c.expr(x.Left); v != nil {
			return v
		}
		if root, ok := rootIdent(x.Left); !ok || root != c.name {
			if c.aliases(x.Right) {
				return &escViolation{reason: "stored via assignment", pos: x.GetPos()}
			}
		}
		return c.expr(x.Right)
	case *ast.UpdateExpression:
		return c.expr(x.Arg)
	case *ast.CallExpression:
		return c.call(x)
	case *ast.MemberExpression:
		return c.expr(x.Object)
	case *ast.IndexExpression:
		if v := c.expr(x.Object); v != nil {
			return v
		}
		return c.expr(x.Index)
	case *ast.BinaryExpression:
		if v := c.expr(x.Left); v != nil {
			return v
		}
		return c.expr(x.Right)
	case *ast.UnaryExpression:
		return c.expr(x.Arg)
	case *ast.ConditionalExpression:
		if v := c.expr(x.Test); v != nil {
			return v
		}
		if v := c.expr(x.Consequent); v != nil {
			return v
		}
		return c.expr(x.Alternate)
	case *ast.TemplateLiteral:
		// Interpolation stringifies into a fresh buffer — safe even for a
		// bare alias operand.
		for _, ex := range x.Exprs {
			if !c.aliases(ex) {
				if v := c.expr(ex); v != nil {
					return v
				}
			}
		}
	case *ast.ArrayLiteral:
		for _, el := range x.Elements {
			if c.aliases(el) {
				return &escViolation{reason: "stored in an array literal", pos: x.GetPos()}
			}
			if v := c.expr(el); v != nil {
				return v
			}
		}
	case *ast.ObjectLiteral:
		for _, pr := range x.Properties {
			if c.aliases(pr.Value) {
				return &escViolation{reason: "stored in an object literal", pos: x.GetPos()}
			}
			if v := c.expr(pr.Value); v != nil {
				return v
			}
		}
	case *ast.SpreadElement:
		if c.aliases(x.Arg) {
			return &escViolation{reason: "spread", pos: x.GetPos()}
		}
		return c.expr(x.Arg)
	case *ast.SequenceExpression:
		for _, ex := range x.Exprs {
			if v := c.expr(ex); v != nil {
				return v
			}
		}
	case *ast.YieldExpression:
		if c.aliases(x.Argument) {
			return &escViolation{reason: "yielded", pos: x.GetPos()}
		}
		return c.expr(x.Argument)
	case *ast.AwaitExpression:
		return c.expr(x.Argument)
	case *ast.ArrowFunction:
		captured := false
		if x.Block != nil {
			captured = c.mentionsStmts(x.Block.Body)
		} else {
			captured = c.mentionsExpr(x.Body)
		}
		if captured {
			return &escViolation{reason: "captured by a closure", pos: x.GetPos()}
		}
	case *ast.FunctionExpression:
		if c.mentionsStmts(x.Body.Body) {
			return &escViolation{reason: "captured by a closure", pos: x.GetPos()}
		}
	default:
		if c.mentionsExpr(expr) {
			return &escViolation{reason: "used in a construct the escape analysis doesn't model", pos: expr.GetPos()}
		}
	}
	return nil
}

// call classifies a call's argument and receiver positions.
func (c *escChecker) call(x *ast.CallExpression) *escViolation {
	// FinalizationRegistry.register(target, held, token?) with the candidate
	// exactly in target position (and nowhere else in the call): safe for the
	// free check — see escChecker.finregNames. held/token positions still
	// escape (held is read by the callback after the free; a token is
	// compared by a later unregister).
	if callee, ok := x.Callee.(*ast.MemberExpression); ok && callee.Property == "register" && len(x.Args) >= 2 && len(x.Args) <= 3 {
		if recv, ok2 := callee.Object.(*ast.Identifier); ok2 && c.finregNames[recv.Name] && recv.Name != c.name {
			if id, ok3 := x.Args[0].(*ast.Identifier); ok3 && id.Name == c.name {
				rest := false
				for _, a := range x.Args[1:] {
					if c.mentionsExpr(a) {
						rest = true
					}
				}
				if !rest {
					return nil
				}
			}
		}
	}
	whitelisted := false
	switch callee := x.Callee.(type) {
	case *ast.Identifier:
		whitelisted = escCallWhitelist[callee.Name]
	case *ast.MemberExpression:
		if ns, ok := callee.Object.(*ast.Identifier); ok {
			if (ns.Name == "Memory" || ns.Name == "Memory__kml_builtin") && callee.Property == "free" {
				for _, a := range x.Args {
					if c.mentionsExpr(a) {
						return &escViolation{reason: "also freed via Memory.free — the inserted free would double-free it", pos: x.GetPos()}
					}
				}
			}
			whitelisted = escCallWhitelist[ns.Name+"."+callee.Property]
		}
		// Receiver position (`x.method(...)`): methods on this compiler's
		// builtin containers read or mutate contents and return copies —
		// they never retain the receiver's top-level pointer. The emit-time
		// type gate restricts registration to those builtin types (class
		// instances, whose methods could store `this`, are excluded there),
		// so receiver position is safe here.
		if v := c.expr(callee.Object); v != nil {
			return v
		}
	default:
		if v := c.expr(x.Callee); v != nil {
			return v
		}
	}
	for _, a := range x.Args {
		if c.aliases(a) && !whitelisted {
			return &escViolation{reason: "passed as a call argument (the callee may retain it)", pos: x.GetPos()}
		}
		if v := c.expr(a); v != nil {
			return v
		}
	}
	return nil
}
