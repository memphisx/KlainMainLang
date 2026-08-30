package llvm

// pure_check.go — compile-time enforcement of `/** @pure */` (TDD-00128).
//
// This is a front-end analysis pass with ZERO runtime and ZERO codegen cost:
// arguments still pass exactly as before and the emitted binary is byte-
// identical to the untagged version. The pass walks each `@pure` function body
// and rejects, with a diagnostic naming the specific violation, any of the five
// impurity classes the TDD enumerates:
//
//  1. Parameter mutation — reassigning a parameter, writing to a field/element
//     reachable from a parameter, or a mutating method on a parameter.
//  2. Free-variable / global mutation — assigning to any binding not declared
//     inside the function (a captured variable, a module-level `let`, a global).
//  3. I/O and other impure builtins — console.*, process.* effects, fs.*,
//     fetch/network, timers.
//  4. Nondeterminism — Math.random(), Date.now(), performance.now(),
//     `new Date()` (argless).
//  5. Calling a non-`@pure` function — purity is contagious; a `@pure` function
//     may only call other `@pure` functions (plus a curated pure-builtin
//     allowlist).
//
// Local mutation is fine: a `@pure` function may freely declare and mutate its
// own locals and locally-constructed objects/arrays — only *observable* effects
// are constrained.
//
// Known conservative gaps (documented in TDD-00128, not silent): parameter
// aliasing through a local (`const y = x; y.f = 1`) is not tracked; calling a
// function passed as a parameter or held in a local is allowed (higher-order
// purity is the caller's contract in V1); an unrecognized *builtin* bare call
// is allowed rather than rejected (the pure/impure builtin tables are curated,
// not exhaustive). Calls to non-`@pure` *user* functions are always rejected.

import (
	"fmt"

	"KlainMainLang/ast"
)

// Member-namespace calls that perform I/O or other observable side effects.
var pureCheckImpureNamespaces = map[string]bool{
	"console": true, "process": true, "fs": true, "fsPromises": true,
	"child_process": true, "http": true, "https": true, "net": true,
	"dgram": true, "dns": true, "os": true, "cluster": true, "readline": true,
}

// Bare function names that perform I/O or schedule effects.
var pureCheckImpureBareFns = map[string]bool{
	"fetch": true, "setTimeout": true, "setInterval": true,
	"clearTimeout": true, "clearInterval": true, "setImmediate": true,
	"queueMicrotask": true, "require": true, "structuredClone": false,
	"alert": true, "prompt": true, "confirm": true,
}

// Nondeterministic member calls: namespace -> method.
var pureCheckNondetMembers = map[string]map[string]bool{
	"Math":        {"random": true},
	"Date":        {"now": true},
	"performance": {"now": true},
}

// Bare builtin functions statically known to be pure — so a call to one does
// not trip the contagion check. Curated, not exhaustive (see the gap note).
var pureCheckPureBareBuiltins = map[string]bool{
	"parseInt": true, "parseFloat": true, "isNaN": true, "isFinite": true,
	"String": true, "Number": true, "Boolean": true, "Array": true,
	"encodeURIComponent": true, "decodeURIComponent": true,
	"encodeURI": true, "decodeURI": true,
}

// Methods that mutate their receiver in place.
var pureCheckMutatingMethods = map[string]bool{
	"push": true, "pop": true, "shift": true, "unshift": true, "splice": true,
	"sort": true, "reverse": true, "fill": true, "copyWithin": true,
	"set": true, "add": true, "delete": true, "clear": true,
}

// pureFnInfo pairs a `@pure` function's name with its body and parameters for
// the second pass.
type pureFnInfo struct {
	name   string
	params []ast.Param
	body   *ast.BlockStatement
	pos    ast.Pos
}

// checkPurity runs the `@pure` enforcement pass over the whole program. Called
// at the start of EmitProgram, before any codegen, so every compile path (CLI,
// tests, conformance) enforces it uniformly.
func (e *Emitter) checkPurity(prog *ast.Program) error {
	pureFns := map[string]bool{} // names of every @pure function in the program
	userFns := map[string]bool{} // names of every top-level user function/closure binding
	var toCheck []pureFnInfo

	collect := func(name string, pure bool, params []ast.Param, body *ast.BlockStatement, pos ast.Pos) {
		userFns[name] = true
		if pure {
			pureFns[name] = true
			if body != nil {
				toCheck = append(toCheck, pureFnInfo{name, params, body, pos})
			}
		}
	}

	for _, stmt := range prog.Body {
		switch s := stmt.(type) {
		case *ast.FunctionDeclaration:
			collect(s.Name, s.Pure, s.Params, s.Body, s.GetPos())
		case *ast.VarDeclaration:
			collectPureBinding(s, collect)
		case *ast.VarDeclarationList:
			for _, d := range s.Decls {
				collectPureBinding(d, collect)
			}
		}
	}

	c := &purityChecker{pureFns: pureFns, userFns: userFns}
	for _, fn := range toCheck {
		if err := c.checkFunction(fn); err != nil {
			return err
		}
	}
	return nil
}

func collectPureBinding(d *ast.VarDeclaration, collect func(string, bool, []ast.Param, *ast.BlockStatement, ast.Pos)) {
	switch fn := d.Init.(type) {
	case *ast.ArrowFunction:
		collect(d.Name, fn.Pure, fn.Params, fn.Block, d.GetPos())
	case *ast.FunctionExpression:
		collect(d.Name, fn.Pure, fn.Params, fn.Body, d.GetPos())
	}
}

type purityChecker struct {
	pureFns map[string]bool
	userFns map[string]bool
}

// pureScope tracks, for the function currently being checked, which names are its
// parameters (mutation = class 1) versus locally declared inside it (mutation
// OK). A name in neither is a free variable (mutation = class 2).
type pureScope struct {
	fnName string
	params map[string]bool
	locals map[string]bool
}

func (c *purityChecker) checkFunction(fn pureFnInfo) error {
	sc := &pureScope{
		fnName: fn.name,
		params: map[string]bool{},
		locals: map[string]bool{},
	}
	for _, p := range fn.params {
		sc.params[p.Name] = true
	}
	collectBlockLocals(fn.body, sc.locals)
	return c.checkStmts(fn.body.Body, sc)
}

// collectBlockLocals gathers every name declared inside a block (var/let/const,
// loop variables, nested function-declaration names) into locals, without
// descending into nested function *bodies* (those introduce their own pureScope).
func collectBlockLocals(b *ast.BlockStatement, locals map[string]bool) {
	if b == nil {
		return
	}
	for _, stmt := range b.Body {
		collectStmtLocals(stmt, locals)
	}
}

func collectStmtLocals(stmt ast.Statement, locals map[string]bool) {
	switch s := stmt.(type) {
	case *ast.VarDeclaration:
		locals[s.Name] = true
	case *ast.VarDeclarationList:
		for _, d := range s.Decls {
			locals[d.Name] = true
		}
	case *ast.FunctionDeclaration:
		locals[s.Name] = true
	case *ast.BlockStatement:
		collectBlockLocals(s, locals)
	case *ast.IfStatement:
		collectBlockLocals(s.Consequent, locals)
		if s.Alternate != nil {
			collectStmtLocals(s.Alternate, locals)
		}
	case *ast.ForStatement:
		if s.Init != nil {
			collectStmtLocals(s.Init, locals)
		}
		collectBlockLocals(s.Body, locals)
	case *ast.ForOfStatement:
		locals[s.VarName] = true
		collectBlockLocals(s.Body, locals)
	case *ast.ForInStatement:
		locals[s.VarName] = true
		collectBlockLocals(s.Body, locals)
	case *ast.WhileStatement:
		collectBlockLocals(s.Body, locals)
	case *ast.SwitchStatement:
		for _, cs := range s.Cases {
			for _, st := range cs.Body {
				collectStmtLocals(st, locals)
			}
		}
	}
}

func (c *purityChecker) checkStmts(stmts []ast.Statement, sc *pureScope) error {
	for _, stmt := range stmts {
		if err := c.checkStmt(stmt, sc); err != nil {
			return err
		}
	}
	return nil
}

func (c *purityChecker) checkStmt(stmt ast.Statement, sc *pureScope) error {
	switch s := stmt.(type) {
	case *ast.VarDeclaration:
		return c.checkExpr(s.Init, sc)
	case *ast.VarDeclarationList:
		for _, d := range s.Decls {
			if err := c.checkExpr(d.Init, sc); err != nil {
				return err
			}
		}
	case *ast.ExpressionStatement:
		return c.checkExpr(s.Expr, sc)
	case *ast.ReturnStatement:
		return c.checkExpr(s.Value, sc)
	case *ast.BlockStatement:
		return c.checkStmts(s.Body, sc)
	case *ast.IfStatement:
		if err := c.checkExpr(s.Test, sc); err != nil {
			return err
		}
		if err := c.checkStmts(s.Consequent.Body, sc); err != nil {
			return err
		}
		if s.Alternate != nil {
			return c.checkStmt(s.Alternate, sc)
		}
	case *ast.ForStatement:
		if s.Init != nil {
			if err := c.checkStmt(s.Init, sc); err != nil {
				return err
			}
		}
		if err := c.checkExpr(s.Test, sc); err != nil {
			return err
		}
		for _, u := range s.Update {
			if err := c.checkExpr(u, sc); err != nil {
				return err
			}
		}
		return c.checkStmts(s.Body.Body, sc)
	case *ast.ForOfStatement:
		if err := c.checkExpr(s.Iterable, sc); err != nil {
			return err
		}
		return c.checkStmts(s.Body.Body, sc)
	case *ast.ForInStatement:
		if err := c.checkExpr(s.Object, sc); err != nil {
			return err
		}
		return c.checkStmts(s.Body.Body, sc)
	case *ast.WhileStatement:
		if err := c.checkExpr(s.Test, sc); err != nil {
			return err
		}
		return c.checkStmts(s.Body.Body, sc)
	case *ast.SwitchStatement:
		if err := c.checkExpr(s.Discriminant, sc); err != nil {
			return err
		}
		for _, cs := range s.Cases {
			if err := c.checkExpr(cs.Test, sc); err != nil {
				return err
			}
			if err := c.checkStmts(cs.Body, sc); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *purityChecker) checkExpr(expr ast.Expression, sc *pureScope) error {
	if expr == nil {
		return nil
	}
	switch ex := expr.(type) {
	case *ast.AssignmentExpression:
		if err := c.checkAssignTarget(ex.Left, sc, ex.GetPos()); err != nil {
			return err
		}
		if err := c.checkExpr(ex.Left, sc); err != nil {
			return err
		}
		return c.checkExpr(ex.Right, sc)
	case *ast.UpdateExpression:
		if err := c.checkAssignTarget(ex.Arg, sc, ex.GetPos()); err != nil {
			return err
		}
		return c.checkExpr(ex.Arg, sc)
	case *ast.CallExpression:
		if err := c.checkCall(ex, sc); err != nil {
			return err
		}
		if err := c.checkExpr(ex.Callee, sc); err != nil {
			return err
		}
		for _, a := range ex.Args {
			if err := c.checkExpr(a, sc); err != nil {
				return err
			}
		}
	case *ast.NewDateExpression:
		if ex.Millis == nil && len(ex.Args) == 0 {
			return c.violation(ex.GetPos(), sc, "`new Date()` reads the current time (nondeterministic)")
		}
	case *ast.MemberExpression:
		return c.checkExpr(ex.Object, sc)
	case *ast.IndexExpression:
		if err := c.checkExpr(ex.Object, sc); err != nil {
			return err
		}
		return c.checkExpr(ex.Index, sc)
	case *ast.BinaryExpression:
		if err := c.checkExpr(ex.Left, sc); err != nil {
			return err
		}
		return c.checkExpr(ex.Right, sc)
	case *ast.UnaryExpression:
		return c.checkExpr(ex.Arg, sc)
	case *ast.ConditionalExpression:
		if err := c.checkExpr(ex.Test, sc); err != nil {
			return err
		}
		if err := c.checkExpr(ex.Consequent, sc); err != nil {
			return err
		}
		return c.checkExpr(ex.Alternate, sc)
	case *ast.SpreadElement:
		return c.checkExpr(ex.Arg, sc)
	case *ast.ArrayLiteral:
		for _, el := range ex.Elements {
			if err := c.checkExpr(el, sc); err != nil {
				return err
			}
		}
	case *ast.ObjectLiteral:
		for _, p := range ex.Properties {
			if p.KeyExpr != nil {
				if err := c.checkExpr(p.KeyExpr, sc); err != nil {
					return err
				}
			}
			if err := c.checkExpr(p.Value, sc); err != nil {
				return err
			}
		}
	case *ast.TemplateLiteral:
		for _, e := range ex.Exprs {
			if err := c.checkExpr(e, sc); err != nil {
				return err
			}
		}
	case *ast.NewExpression:
		for _, a := range ex.Args {
			if err := c.checkExpr(a, sc); err != nil {
				return err
			}
		}
	case *ast.ArrowFunction:
		return c.checkNested(ex.Params, ex.Block, ex.Body, sc)
	case *ast.FunctionExpression:
		return c.checkNested(ex.Params, ex.Body, nil, sc)
	}
	return nil
}

// checkNested walks a nested arrow / function expression inside a @pure body,
// extending the pureScope with its own parameters and locals so that a mutation of
// an outer parameter or free variable from within the nested closure is still
// caught.
func (c *purityChecker) checkNested(params []ast.Param, block *ast.BlockStatement, exprBody ast.Expression, sc *pureScope) error {
	inner := &pureScope{fnName: sc.fnName, params: sc.params, locals: map[string]bool{}}
	for k := range sc.locals {
		inner.locals[k] = true
	}
	for _, p := range params {
		inner.locals[p.Name] = true // the nested function's own params shadow — its mutations are local to it
	}
	if block != nil {
		collectBlockLocals(block, inner.locals)
		return c.checkStmts(block.Body, inner)
	}
	return c.checkExpr(exprBody, inner)
}

// rootIdent returns the base identifier name of an assignable target
// (`x`, `x.f`, `x[i]`, `x.a.b`), and false if the target isn't rooted in a
// plain identifier.
func rootIdent(expr ast.Expression) (string, bool) {
	switch ex := expr.(type) {
	case *ast.Identifier:
		return ex.Name, true
	case *ast.MemberExpression:
		return rootIdent(ex.Object)
	case *ast.IndexExpression:
		return rootIdent(ex.Object)
	}
	return "", false
}

// checkAssignTarget classifies a write target: to a parameter (class 1), to a
// free/global binding (class 2), or to a local (allowed).
func (c *purityChecker) checkAssignTarget(target ast.Expression, sc *pureScope, pos ast.Pos) error {
	root, ok := rootIdent(target)
	if !ok {
		return nil
	}
	_, isDirect := target.(*ast.Identifier)
	if sc.params[root] {
		if isDirect {
			return c.violation(pos, sc, fmt.Sprintf("reassigns parameter '%s'", root))
		}
		return c.violation(pos, sc, fmt.Sprintf("mutates a location reachable from parameter '%s'", root))
	}
	if sc.locals[root] {
		return nil // local binding or locally-constructed value — allowed
	}
	// Neither a parameter nor a local: a captured/module/global binding.
	if isDirect {
		return c.violation(pos, sc, fmt.Sprintf("assigns to '%s', which is not declared inside it (captured/global mutation)", root))
	}
	return c.violation(pos, sc, fmt.Sprintf("mutates '%s', which is not declared inside it (captured/global mutation)", root))
}

// checkCall enforces classes 3/4/5 and mutating-method calls on params/frees.
func (c *purityChecker) checkCall(call *ast.CallExpression, sc *pureScope) error {
	switch callee := call.Callee.(type) {
	case *ast.Identifier:
		name := callee.Name
		if pureCheckImpureBareFns[name] {
			return c.violation(call.GetPos(), sc, fmt.Sprintf("calls '%s', which performs I/O or schedules an effect", name))
		}
		if pureCheckPureBareBuiltins[name] {
			return nil
		}
		// A call to another user function is only allowed if it, too, is @pure.
		// A call to a parameter/local (a passed-in or local callback) is allowed
		// — higher-order purity is the caller's contract in V1.
		if c.userFns[name] && !c.pureFns[name] {
			return c.violation(call.GetPos(), sc, fmt.Sprintf("calls '%s', which is not @pure (purity is contagious)", name))
		}
	case *ast.MemberExpression:
		if nsID, ok := callee.Object.(*ast.Identifier); ok {
			ns := nsID.Name
			if pureCheckImpureNamespaces[ns] {
				return c.violation(call.GetPos(), sc, fmt.Sprintf("calls '%s.%s', which performs I/O", ns, callee.Property))
			}
			if m, ok := pureCheckNondetMembers[ns]; ok && m[callee.Property] {
				return c.violation(call.GetPos(), sc, fmt.Sprintf("calls '%s.%s' (nondeterministic)", ns, callee.Property))
			}
			// Object.assign(target, ...) mutates its first argument.
			if ns == "Object" && callee.Property == "assign" && len(call.Args) > 0 {
				if err := c.checkAssignTarget(call.Args[0], sc, call.GetPos()); err != nil {
					return err
				}
			}
		}
		// A mutating method on a parameter or free-variable receiver mutates
		// external state (class 1 / class 2); on a local receiver it is fine.
		if pureCheckMutatingMethods[callee.Property] {
			if root, ok := rootIdent(callee.Object); ok {
				if sc.params[root] {
					return c.violation(call.GetPos(), sc, fmt.Sprintf("calls mutating method '.%s()' on parameter '%s'", callee.Property, root))
				}
				if !sc.locals[root] {
					return c.violation(call.GetPos(), sc, fmt.Sprintf("calls mutating method '.%s()' on '%s', which is not declared inside it", callee.Property, root))
				}
			}
		}
	}
	return nil
}

func (c *purityChecker) violation(pos ast.Pos, sc *pureScope, what string) error {
	return fmt.Errorf("%d:%d: @pure function '%s' %s", pos.Line, pos.Col, pureDisplayName(sc.fnName), pureDisplayName(what))
}

// pureDisplayName strips the resolver's per-file mangling suffix
// (`name__kml_mod<N>`) so diagnostics show the source-level name. Applied to the
// whole message so an interpolated mangled identifier reads cleanly too.
func pureDisplayName(s string) string {
	for {
		i := indexOf(s, "__kml_mod")
		if i < 0 {
			return s
		}
		// drop "__kml_mod" plus the trailing digits
		j := i + len("__kml_mod")
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		s = s[:i] + s[j:]
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
