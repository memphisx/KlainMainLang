package resolver

import (
	"KlainMainLang/ast"
	"fmt"
)

// checkTDZ enforces the temporal-dead-zone early error (TDD-00071 Stage 1): a
// read of a `let`/`const`/`class` binding before its declaration has run is
// `Cannot access 'x' before initialization`, not the generic `undefined
// variable`. Because a block's lexical names are hoisted to the block top in a
// TDZ state, this also fixes the shadowing correctness bug where
// `let x = 1; { console.log(x); let x = 2; }` used to read the *outer* x
// instead of erroring.
//
// The analysis is deliberately sound rather than complete (TDD-00071's
// no-false-positives rule): it only flags a read that reaches a still-TDZ
// binding *within the same function* by straight textual order. A read inside a
// nested function/closure is exempt — real TDZ is a runtime check, and the
// closure may legally run after the declaration — so cross-function reads never
// produce a false positive. Missed cases (a TDZ read that only happens through
// a closure, or through data flow) are a documented V1 gap, not an error.
func checkTDZ(prog *ast.Program) error {
	st := &tdzState{}
	// The module/script top level is its own function-like boundary, so a
	// top-level `console.log(y); let y = 1;` is caught, while a top-level
	// function reading an outer top-level `let` is exempt.
	return st.walkScope(prog.Body, true, nil, nil)
}

type tdzBindState int

const (
	tdzUninit tdzBindState = iota // lexical binding hoisted but not yet initialized
	tdzInit                       // lexical binding whose declaration has run
)

type tdzFrame struct {
	lexical      map[string]tdzBindState // let/const/class/enum in this scope
	shadow       map[string]bool         // var/function/param/catch — never in TDZ
	funcBoundary bool                    // true for a function/arrow/method/script body
}

type tdzState struct {
	frames []*tdzFrame
}

// walkScope pushes a fresh frame for a block or function body, hoists its direct
// lexical names into TDZ and its var/function names as shadows, binds any params
// (function/arrow bodies) and extraShadows (a catch parameter), then walks the
// body in order so each lexical declaration flips its own name to initialized.
func (st *tdzState) walkScope(body []ast.Statement, funcBoundary bool, params []ast.Param, extraShadows []string) error {
	fr := &tdzFrame{lexical: map[string]tdzBindState{}, shadow: map[string]bool{}, funcBoundary: funcBoundary}
	st.frames = append(st.frames, fr)
	defer func() { st.frames = st.frames[:len(st.frames)-1] }()

	for _, p := range params {
		for _, n := range paramNames(p) {
			fr.shadow[n] = true
		}
	}
	for _, n := range extraShadows {
		fr.shadow[n] = true
	}
	for _, stmt := range body {
		for _, d := range declaredNamesOf(stmt) {
			if isLexicalKind(d.kind) {
				if _, ok := fr.lexical[d.name]; !ok {
					fr.lexical[d.name] = tdzUninit
				}
			} else {
				fr.shadow[d.name] = true
			}
		}
	}
	for _, stmt := range body {
		if err := st.walkStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

// markInitialized flips a lexical name in the current (innermost) frame to
// initialized — called once its declaration statement has been walked.
func (st *tdzState) markInitialized(name string) {
	fr := st.frames[len(st.frames)-1]
	if _, ok := fr.lexical[name]; ok {
		fr.lexical[name] = tdzInit
	}
}

func (st *tdzState) walkStmt(stmt ast.Statement) error {
	switch s := stmt.(type) {
	case *ast.VarDeclaration:
		return st.walkVarDecl(s)
	case *ast.VarDeclarationList:
		for _, d := range s.Decls {
			if err := st.walkVarDecl(d); err != nil {
				return err
			}
		}
	case *ast.ArrayDestructuring:
		if s.Init != nil {
			if err := st.walkExpr(s.Init); err != nil {
				return err
			}
		}
		if isLexicalKind(s.Kind) {
			var ns []string
			for _, e := range s.Elems {
				collectArrayPatternNames(e, &ns)
			}
			for _, n := range ns {
				st.markInitialized(n)
			}
		}
	case *ast.ObjectDestructuring:
		if s.Init != nil {
			if err := st.walkExpr(s.Init); err != nil {
				return err
			}
		}
		if isLexicalKind(s.Kind) {
			var ns []string
			for _, p := range s.Props {
				collectObjectPatternNames(p, &ns)
			}
			for _, n := range ns {
				st.markInitialized(n)
			}
		}
	case *ast.ExpressionStatement:
		return st.walkExpr(s.Expr)
	case *ast.ReturnStatement:
		if s.Value != nil {
			return st.walkExpr(s.Value)
		}
	case *ast.IfStatement:
		if err := st.walkExpr(s.Test); err != nil {
			return err
		}
		if s.Consequent != nil {
			if err := st.walkScope(s.Consequent.Body, false, nil, nil); err != nil {
				return err
			}
		}
		if s.Alternate != nil {
			return st.walkStmt(s.Alternate)
		}
	case *ast.BlockStatement:
		return st.walkScope(s.Body, false, nil, nil)
	case *ast.ForStatement:
		return st.walkFor(s)
	case *ast.ForOfStatement:
		return st.walkForInOf(s.Iterable, s.Kind, forLoopVarNames(s.VarName, s.ArrayPattern, s.ObjectPattern), s.Body)
	case *ast.ForInStatement:
		return st.walkForInOf(s.Object, s.Kind, []string{s.VarName}, s.Body)
	case *ast.WhileStatement:
		if err := st.walkExpr(s.Test); err != nil {
			return err
		}
		if s.Body != nil {
			return st.walkScope(s.Body.Body, false, nil, nil)
		}
	case *ast.DoWhileStatement:
		if s.Body != nil {
			if err := st.walkScope(s.Body.Body, false, nil, nil); err != nil {
				return err
			}
		}
		return st.walkExpr(s.Test)
	case *ast.SwitchStatement:
		return st.walkSwitch(s)
	case *ast.TryStatement:
		return st.walkTry(s)
	case *ast.LabeledStatement:
		return st.walkStmt(s.Body)
	case *ast.FunctionDeclaration:
		if s.Body != nil {
			return st.walkScope(s.Body.Body, true, s.Params, nil)
		}
	case *ast.ClassDeclaration:
		if err := st.walkClass(s); err != nil {
			return err
		}
		st.markInitialized(s.Name)
	case *ast.EnumDeclaration:
		// An enum name is a lexical binding (hoisted TDZ), initialized once its
		// declaration is reached; its members are literal, so there are no reads
		// to walk. Without this its name would stay TDZ and a later `E.A` read
		// would be a false positive.
		st.markInitialized(s.Name)
	}
	return nil
}

func (st *tdzState) walkVarDecl(v *ast.VarDeclaration) error {
	if v.Init != nil {
		if err := st.walkExpr(v.Init); err != nil {
			return err
		}
	}
	if isLexicalKind(v.Kind) {
		st.markInitialized(v.Name)
	}
	return nil
}

func (st *tdzState) walkFor(s *ast.ForStatement) error {
	// The for-head (`for (let i = 0; …)`) is its own block scope enclosing the
	// body, so a fresh frame carries the init binding through test/update/body.
	fr := &tdzFrame{lexical: map[string]tdzBindState{}, shadow: map[string]bool{}}
	st.frames = append(st.frames, fr)
	defer func() { st.frames = st.frames[:len(st.frames)-1] }()

	if s.Init != nil {
		for _, d := range declaredNamesOf(s.Init) {
			if isLexicalKind(d.kind) {
				fr.lexical[d.name] = tdzUninit
			} else {
				fr.shadow[d.name] = true
			}
		}
		if err := st.walkStmt(s.Init); err != nil {
			return err
		}
	}
	if s.Test != nil {
		if err := st.walkExpr(s.Test); err != nil {
			return err
		}
	}
	for _, u := range s.Update {
		if err := st.walkExpr(u); err != nil {
			return err
		}
	}
	if s.Body != nil {
		return st.walkScope(s.Body.Body, false, nil, nil)
	}
	return nil
}

func (st *tdzState) walkForInOf(iter ast.Expression, kind string, varNames []string, body *ast.BlockStatement) error {
	// The iterable is evaluated before the loop variable is bound, so a read of
	// the loop variable in the iterable (`for (const x of x)`) is still a TDZ.
	if err := st.walkExpr(iter); err != nil {
		return err
	}
	fr := &tdzFrame{lexical: map[string]tdzBindState{}, shadow: map[string]bool{}}
	st.frames = append(st.frames, fr)
	defer func() { st.frames = st.frames[:len(st.frames)-1] }()
	// The loop variable(s) are bound (initialized) per iteration. Recording
	// every name — including a destructuring pattern's leaves — keeps a loop
	// variable from being mistaken for an outer TDZ binding of the same name.
	for _, n := range varNames {
		if n == "" {
			continue
		}
		if isLexicalKind(kind) {
			fr.lexical[n] = tdzInit
		} else {
			fr.shadow[n] = true
		}
	}
	if body != nil {
		return st.walkScope(body.Body, false, nil, nil)
	}
	return nil
}

// forLoopVarNames returns every name a for-of loop variable binds: a bare
// VarName, or the leaf names of a destructuring pattern.
func forLoopVarNames(varName string, arr []ast.ArrayPatternElem, obj []ast.DestructProp) []string {
	if arr == nil && obj == nil {
		return []string{varName}
	}
	var ns []string
	for _, e := range arr {
		collectArrayPatternNames(e, &ns)
	}
	for _, p := range obj {
		collectObjectPatternNames(p, &ns)
	}
	return ns
}

func (st *tdzState) walkSwitch(s *ast.SwitchStatement) error {
	if err := st.walkExpr(s.Discriminant); err != nil {
		return err
	}
	// A switch body is a single lexical scope shared across all cases.
	fr := &tdzFrame{lexical: map[string]tdzBindState{}, shadow: map[string]bool{}}
	st.frames = append(st.frames, fr)
	defer func() { st.frames = st.frames[:len(st.frames)-1] }()
	for _, c := range s.Cases {
		for _, stmt := range c.Body {
			for _, d := range declaredNamesOf(stmt) {
				if isLexicalKind(d.kind) {
					if _, ok := fr.lexical[d.name]; !ok {
						fr.lexical[d.name] = tdzUninit
					}
				} else {
					fr.shadow[d.name] = true
				}
			}
		}
	}
	for _, c := range s.Cases {
		if c.Test != nil {
			if err := st.walkExpr(c.Test); err != nil {
				return err
			}
		}
		for _, stmt := range c.Body {
			if err := st.walkStmt(stmt); err != nil {
				return err
			}
		}
	}
	return nil
}

func (st *tdzState) walkTry(s *ast.TryStatement) error {
	if s.Body != nil {
		if err := st.walkScope(s.Body.Body, false, nil, nil); err != nil {
			return err
		}
	}
	if s.Catch != nil && s.Catch.Body != nil {
		var shadows []string
		if s.Catch.Param != "" {
			shadows = append(shadows, s.Catch.Param)
		}
		for _, p := range s.Catch.ObjectPattern {
			if p.Local != "" {
				shadows = append(shadows, p.Local)
			}
		}
		if err := st.walkScope(s.Catch.Body.Body, false, nil, shadows); err != nil {
			return err
		}
	}
	if s.Finally != nil {
		return st.walkScope(s.Finally.Body, false, nil, nil)
	}
	return nil
}

func (st *tdzState) walkClass(c *ast.ClassDeclaration) error {
	// Method/constructor bodies are function boundaries — walk them so their own
	// locals are TDZ-checked, but a reference to an outer binding from inside a
	// method is exempt (cross-boundary), same as any closure. Field initializers
	// run in constructor context and are left to a later stage (V1 gap).
	walkMethod := func(fn *ast.FunctionDeclaration) error {
		if fn != nil && fn.Body != nil {
			return st.walkScope(fn.Body.Body, true, fn.Params, nil)
		}
		return nil
	}
	if err := walkMethod(c.Constructor); err != nil {
		return err
	}
	for _, m := range c.Methods {
		if err := walkMethod(m); err != nil {
			return err
		}
	}
	return nil
}

// walkExpr walks an expression, checking every variable read against the TDZ
// state. It mirrors rename.go's rewriteExpr in which sub-nodes are real
// variable references (a member's Object but not its Property, an object
// literal's computed key and values but not shorthand keys), and treats an
// arrow/function expression as a nested function boundary rather than descending
// into it inline.
func (st *tdzState) walkExpr(expr ast.Expression) error {
	switch e := expr.(type) {
	case *ast.Identifier:
		return st.checkRead(e.Name, e.GetPos())
	case *ast.AwaitExpression:
		return st.walkExpr(e.Argument)
	case *ast.YieldExpression:
		if e.Argument != nil {
			return st.walkExpr(e.Argument)
		}
	case *ast.BinaryExpression:
		if err := st.walkExpr(e.Left); err != nil {
			return err
		}
		return st.walkExpr(e.Right)
	case *ast.ConditionalExpression:
		if err := st.walkExpr(e.Test); err != nil {
			return err
		}
		if err := st.walkExpr(e.Consequent); err != nil {
			return err
		}
		return st.walkExpr(e.Alternate)
	case *ast.SequenceExpression:
		for _, x := range e.Exprs {
			if err := st.walkExpr(x); err != nil {
				return err
			}
		}
	case *ast.SpreadElement:
		return st.walkExpr(e.Arg)
	case *ast.UnaryExpression:
		return st.walkExpr(e.Arg)
	case *ast.UpdateExpression:
		return st.walkExpr(e.Arg)
	case *ast.AssignmentExpression:
		if err := st.walkExpr(e.Left); err != nil {
			return err
		}
		return st.walkExpr(e.Right)
	case *ast.CallExpression:
		if err := st.walkExpr(e.Callee); err != nil {
			return err
		}
		for _, a := range e.Args {
			if err := st.walkExpr(a); err != nil {
				return err
			}
		}
	case *ast.MemberExpression:
		return st.walkExpr(e.Object)
	case *ast.IndexExpression:
		if err := st.walkExpr(e.Object); err != nil {
			return err
		}
		return st.walkExpr(e.Index)
	case *ast.ArrayLiteral:
		for _, x := range e.Elements {
			if err := st.walkExpr(x); err != nil {
				return err
			}
		}
	case *ast.NewArrayExpression:
		if e.Size != nil {
			return st.walkExpr(e.Size)
		}
	case *ast.ObjectLiteral:
		for i := range e.Properties {
			if e.Properties[i].KeyExpr != nil {
				if err := st.walkExpr(e.Properties[i].KeyExpr); err != nil {
					return err
				}
			}
			if e.Properties[i].Value != nil {
				if err := st.walkExpr(e.Properties[i].Value); err != nil {
					return err
				}
			}
		}
	case *ast.TemplateLiteral:
		for _, x := range e.Exprs {
			if err := st.walkExpr(x); err != nil {
				return err
			}
		}
	case *ast.ArrowFunction:
		return st.walkClosure(e.Params, e.Body, e.Block)
	case *ast.FunctionExpression:
		return st.walkClosure(e.Params, nil, e.Body)
	}
	return nil
}

// walkClosure walks an arrow/function-expression body as a nested function
// boundary: its own locals are TDZ-checked, but reads of an outer binding are
// exempt (the closure may run after that binding initializes).
func (st *tdzState) walkClosure(params []ast.Param, exprBody ast.Expression, block *ast.BlockStatement) error {
	if block != nil {
		return st.walkScope(block.Body, true, params, nil)
	}
	if exprBody != nil {
		// Expression-bodied arrow: push a boundary frame for its params, then
		// walk the single expression.
		fr := &tdzFrame{lexical: map[string]tdzBindState{}, shadow: map[string]bool{}, funcBoundary: true}
		for _, p := range params {
			for _, n := range paramNames(p) {
				fr.shadow[n] = true
			}
		}
		st.frames = append(st.frames, fr)
		defer func() { st.frames = st.frames[:len(st.frames)-1] }()
		return st.walkExpr(exprBody)
	}
	return nil
}

// checkRead resolves a variable reference against the frame stack and reports a
// TDZ error only when it binds to a still-uninitialized lexical name *within the
// same function* — a reference that crosses a function boundary to reach the
// binding is exempt (the closure may run later), keeping the analysis free of
// false positives.
func (st *tdzState) checkRead(name string, pos ast.Pos) error {
	crossedBoundary := false
	for i := len(st.frames) - 1; i >= 0; i-- {
		fr := st.frames[i]
		if state, ok := fr.lexical[name]; ok {
			if state == tdzUninit && !crossedBoundary {
				return fmt.Errorf("%d:%d: cannot access '%s' before initialization", pos.Line, pos.Col, name)
			}
			return nil // resolved to a lexical binding (initialized, or exempt)
		}
		if fr.shadow[name] {
			return nil // resolved to a var/param/function — never a TDZ
		}
		if fr.funcBoundary {
			crossedBoundary = true
		}
	}
	return nil // not a tracked local (global/import/undefined) — codegen's concern
}

// ---------------------------------------------------------------------------
// Definite-assignment analysis (TDD-00071 Stage 2, caveat #1)
//
// A read of a *tracked* binding (a typed `let`/`var` — not annotated
// `any`/`unknown`/nullable) that is not definitely assigned on every path to
// the read is `variable 'x' is used before being assigned`. This is a
// must-analysis: `assigned` holds only bindings assigned on *all* paths, so a
// binding missing from it is "maybe unassigned" and a read of it errors.
//
// Soundness (TDD-00071's no-false-positives rule) drives every conservative
// choice: a branch that *might* not complete normally (contains any
// return/throw/break/continue) is excluded from the if/else merge, so the merge
// never claims a binding assigned when it isn't; a read that crosses a function
// boundary is exempt (the closure may run after assignment); an assignment form
// the walk doesn't recognize is treated as possibly assigning (never as a
// definite non-assignment). Missed errors are a documented gap; a rejected
// valid program is not.
func checkDefiniteAssignment(prog *ast.Program) error {
	da := &daState{assigned: map[int]bool{}, tracked: map[int]bool{}}
	return da.walkFunc(prog.Body, nil)
}

type daFrame struct {
	ids          map[string]int
	funcBoundary bool
}

type daState struct {
	frames   []*daFrame
	assigned map[int]bool // binding id -> definitely assigned on every path to here
	tracked  map[int]bool // binding id -> subject to the definite-assignment check
	nextID   int
}

// walkFunc analyzes one function body (or the script top level) as an
// independent unit: its params are pre-bound as assigned, its function-scoped
// vars are pre-declared (unassigned), and a fresh assigned-set is threaded
// through its statements. Nested functions are analyzed on their own when the
// walk reaches them, so a read of an outer binding is never checked here.
func (da *daState) walkFunc(body []ast.Statement, params []ast.Param) error {
	fr := &daFrame{ids: map[string]int{}, funcBoundary: true}
	da.frames = append(da.frames, fr)
	defer func() { da.frames = da.frames[:len(da.frames)-1] }()

	for _, p := range params {
		for _, n := range paramNames(p) {
			id := da.declare(fr, n, false) // params are never in the dead zone
			da.assigned[id] = true
		}
	}
	// Function-scoped `var`s are visible (hoisted) throughout the body; declare
	// them up front so an assignment anywhere is attributed to the one binding.
	da.hoistVars(body, fr)

	_, err := da.walkStmts(body)
	return err
}

// declare assigns a fresh id to name in frame fr and records whether it is
// tracked. A binding is tracked unless it is exempt (any/unknown/nullable).
func (da *daState) declare(fr *daFrame, name string, tracked bool) int {
	id := da.nextID
	da.nextID++
	fr.ids[name] = id
	if tracked {
		da.tracked[id] = true
	}
	return id
}

// hoistVars pre-declares every function-scoped `var` in body (not crossing a
// nested function) into the function frame, tracked according to its
// annotation, so the analysis models var's function scoping.
func (da *daState) hoistVars(body []ast.Statement, fr *daFrame) {
	vars := map[string]bool{}
	gatherFuncVars(body, vars)
	for name, tracked := range vars {
		if _, exists := fr.ids[name]; !exists {
			da.declare(fr, name, tracked)
		}
	}
}

// walkStmts threads assigned through a statement list, stopping once a
// statement definitely diverges (the rest is dead code). Returns whether the
// list definitely completes abnormally.
func (da *daState) walkStmts(stmts []ast.Statement) (bool, error) {
	for _, stmt := range stmts {
		diverges, err := da.walkStmt(stmt)
		if err != nil {
			return false, err
		}
		if diverges {
			return true, nil
		}
	}
	return false, nil
}

func (da *daState) walkStmt(stmt ast.Statement) (bool, error) {
	switch s := stmt.(type) {
	case *ast.VarDeclaration:
		return false, da.walkDADecl(s)
	case *ast.VarDeclarationList:
		for _, d := range s.Decls {
			if err := da.walkDADecl(d); err != nil {
				return false, err
			}
		}
	case *ast.ArrayDestructuring, *ast.ObjectDestructuring:
		// A destructuring declaration binds its leaves as assigned (each gets a
		// value from the initializer). Walk the initializer's reads first.
		return false, da.walkDestructuringDecl(stmt)
	case *ast.ExpressionStatement:
		return false, da.walkExprDA(s.Expr)
	case *ast.ReturnStatement:
		if s.Value != nil {
			if err := da.walkExprDA(s.Value); err != nil {
				return false, err
			}
		}
		return true, nil
	case *ast.ThrowStatement:
		if err := da.walkExprDA(s.Argument); err != nil {
			return false, err
		}
		return true, nil
	case *ast.BreakStatement, *ast.ContinueStatement:
		return true, nil
	case *ast.IfStatement:
		return da.walkDAIf(s)
	case *ast.BlockStatement:
		return da.walkDABlock(s.Body)
	case *ast.ForStatement:
		var seedStmts []ast.Statement
		if s.Init != nil {
			seedStmts = append(seedStmts, s.Init)
		}
		if s.Body != nil {
			seedStmts = append(seedStmts, s.Body.Body...)
		}
		seedExprs := append([]ast.Expression{}, s.Update...)
		if s.Test != nil {
			seedExprs = append(seedExprs, s.Test)
		}
		return false, da.walkDALoop(seedStmts, seedExprs, func() error {
			if s.Init != nil {
				if _, err := da.walkStmt(s.Init); err != nil {
					return err
				}
			}
			if s.Test != nil {
				if err := da.walkExprDA(s.Test); err != nil {
					return err
				}
			}
			for _, u := range s.Update {
				if err := da.walkExprDA(u); err != nil {
					return err
				}
			}
			if s.Body != nil {
				_, err := da.walkDABlock(s.Body.Body)
				return err
			}
			return nil
		})
	case *ast.ForOfStatement:
		return false, da.walkDAForInOf(s.Iterable, forLoopVarNames(s.VarName, s.ArrayPattern, s.ObjectPattern), s.Body)
	case *ast.ForInStatement:
		return false, da.walkDAForInOf(s.Object, []string{s.VarName}, s.Body)
	case *ast.WhileStatement:
		var seedStmts []ast.Statement
		if s.Body != nil {
			seedStmts = s.Body.Body
		}
		return false, da.walkDALoop(seedStmts, []ast.Expression{s.Test}, func() error {
			if err := da.walkExprDA(s.Test); err != nil {
				return err
			}
			if s.Body != nil {
				_, err := da.walkDABlock(s.Body.Body)
				return err
			}
			return nil
		})
	case *ast.DoWhileStatement:
		// A do/while body runs at least once, so unlike other loops its
		// definite assignments ARE guaranteed afterward. Over-seed for
		// loop-carried read safety, walk for reads, then restore and credit the
		// precise set the body definitely assigns (computed by a must-analysis,
		// so `do { if (c) x=1 } while(…)` is still caught while
		// `do { x=1 } while(…)` and `do { if(c) x=1; else x=2 } while(…)` are not).
		before := copyIntSet(da.assigned)
		seed := map[string]bool{}
		if s.Body != nil {
			for _, st := range s.Body.Body {
				gatherAssignedNamesStmt(st, seed)
			}
		}
		gatherAssignedNamesExpr(s.Test, seed)
		for n := range seed {
			da.markAssigned(n)
		}
		if s.Body != nil {
			if _, err := da.walkDABlock(s.Body.Body); err != nil {
				return false, err
			}
		}
		if err := da.walkExprDA(s.Test); err != nil {
			return false, err
		}
		da.assigned = before
		if s.Body != nil {
			for n := range definiteAssignsOf(s.Body.Body) {
				da.markAssigned(n)
			}
		}
		return false, nil
	case *ast.SwitchStatement:
		return false, da.walkDASwitch(s)
	case *ast.TryStatement:
		return false, da.walkDATry(s)
	case *ast.LabeledStatement:
		return da.walkStmt(s.Body)
	case *ast.FunctionDeclaration:
		if s.Body != nil {
			return false, da.walkFunc(s.Body.Body, s.Params)
		}
	case *ast.ClassDeclaration:
		return false, da.walkDAClass(s)
	}
	return false, nil
}

func (da *daState) walkDADecl(v *ast.VarDeclaration) error {
	if v.Init != nil {
		if err := da.walkExprDA(v.Init); err != nil {
			return err
		}
	}
	fr := da.frames[len(da.frames)-1]
	tracked := !isExemptAnnotation(v.TypeAnnot)
	var id int
	if v.Kind == "var" {
		// Already hoisted into the function frame; find it there.
		id = da.resolveID(v.Name)
	} else {
		id = da.declare(fr, v.Name, tracked)
	}
	if v.Init != nil && id >= 0 {
		da.assigned[id] = true
	}
	return nil
}

func (da *daState) walkDestructuringDecl(stmt ast.Statement) error {
	var init ast.Expression
	var names []string
	switch s := stmt.(type) {
	case *ast.ArrayDestructuring:
		init = s.Init
		for _, e := range s.Elems {
			collectArrayPatternNames(e, &names)
		}
	case *ast.ObjectDestructuring:
		init = s.Init
		for _, p := range s.Props {
			collectObjectPatternNames(p, &names)
		}
	}
	if init != nil {
		if err := da.walkExprDA(init); err != nil {
			return err
		}
	}
	// Destructuring leaves are exempt from the check (their concrete type isn't
	// tracked here) but recorded as assigned so a later read never false-flags.
	fr := da.frames[len(da.frames)-1]
	for _, n := range names {
		id := da.declare(fr, n, false)
		da.assigned[id] = true
	}
	return nil
}

func (da *daState) walkDABlock(body []ast.Statement) (bool, error) {
	fr := &daFrame{ids: map[string]int{}}
	da.frames = append(da.frames, fr)
	defer func() { da.frames = da.frames[:len(da.frames)-1] }()
	return da.walkStmts(body)
}

func (da *daState) walkDAIf(s *ast.IfStatement) (bool, error) {
	if err := da.walkExprDA(s.Test); err != nil {
		return false, err
	}
	before := copyIntSet(da.assigned)

	// Consequent from the pre-if state.
	if s.Consequent != nil {
		if _, err := da.walkDABlock(s.Consequent.Body); err != nil {
			return false, err
		}
	}
	consAssigned := copyIntSet(da.assigned)
	da.assigned = copyIntSet(before)

	altAssigned := copyIntSet(before)
	if s.Alternate != nil {
		if _, err := da.walkStmt(s.Alternate); err != nil {
			return false, err
		}
		altAssigned = copyIntSet(da.assigned)
	}

	// Merge: keep only branches that definitely complete normally, so a
	// diverging branch never drags the intersection below the truth.
	consKept := s.Consequent != nil && !blockMayExit(s.Consequent.Body)
	altKept := s.Alternate == nil || !stmtMayExit(s.Alternate)

	switch {
	case s.Alternate == nil:
		// The consequent may be skipped entirely — nothing new is definite.
		da.assigned = before
	case consKept && altKept:
		da.assigned = intersectIntSet(consAssigned, altAssigned)
	case consKept:
		da.assigned = consAssigned
	case altKept:
		da.assigned = altAssigned
	default:
		// Both branches diverge — code after the if is dead; union is harmless.
		da.assigned = unionIntSet(consAssigned, altAssigned)
	}
	// The if as a whole diverges (making later code dead) only if both branches
	// exit on every path — an under-approximation, which is safe since this
	// merely prunes dead code from further analysis.
	diverges := s.Alternate != nil && allPathsExit(s.Consequent.Body) && allPathsExit2(s.Alternate)
	return diverges, nil
}

// walkDALoop implements the sound over-approximation for a `for`/`while`/
// `for-of`/`for-in` loop: every name the loop assigns anywhere is marked
// assigned *before* the loop is walked. This never rejects valid code — a
// loop-carried assignment (`for (…) { if (i===0) x=1; else x=x+1 }`) and a
// for-init that runs unconditionally (`for (var i=0; …) {} use(i)`) both stay
// accepted — at the cost of missing the genuinely-unsafe form (a loop that runs
// zero times, so a body-only assignment isn't definite). Reads inside the loop
// are still checked against the pre-seeded state. `do/while` and `switch` do
// NOT use this — they get precise credit via definiteAssignsOf (ADR-00214).
func (da *daState) walkDALoop(seedFrom []ast.Statement, seedExprs []ast.Expression, body func() error) error {
	names := map[string]bool{}
	for _, s := range seedFrom {
		gatherAssignedNamesStmt(s, names)
	}
	for _, e := range seedExprs {
		gatherAssignedNamesExpr(e, names)
	}
	for n := range names {
		da.markAssigned(n)
	}
	return body()
}

func (da *daState) walkDAForInOf(iter ast.Expression, varNames []string, body *ast.BlockStatement) error {
	if err := da.walkExprDA(iter); err != nil {
		return err
	}
	var seedStmts []ast.Statement
	if body != nil {
		seedStmts = body.Body
	}
	return da.walkDALoop(seedStmts, nil, func() error {
		fr := &daFrame{ids: map[string]int{}}
		da.frames = append(da.frames, fr)
		defer func() { da.frames = da.frames[:len(da.frames)-1] }()
		for _, n := range varNames {
			if n == "" {
				continue
			}
			id := da.declare(fr, n, false) // loop var is bound each iteration
			da.assigned[id] = true
		}
		if body != nil {
			_, err := da.walkStmts(body.Body)
			return err
		}
		return nil
	})
}

func (da *daState) walkDASwitch(s *ast.SwitchStatement) error {
	if err := da.walkExprDA(s.Discriminant); err != nil {
		return err
	}
	// Over-seed every case's assignments for read-checking (so a read of a
	// binding a later case assigns doesn't false-flag), walk the cases, then
	// restore and credit only what the switch *definitely* assigns: the
	// intersection over every case entry of what running from it assigns, and
	// only when a `default` covers the unmatched path. So a switch that assigns
	// a binding in every case with a default is accepted, while one that leaves
	// it unassigned on some case (or has no default) is still caught.
	before := copyIntSet(da.assigned)
	fr := &daFrame{ids: map[string]int{}}
	da.frames = append(da.frames, fr)
	for _, c := range s.Cases {
		for _, stmt := range c.Body {
			names := map[string]bool{}
			gatherAssignedNamesStmt(stmt, names)
			for n := range names {
				da.markAssigned(n)
			}
		}
	}
	for _, c := range s.Cases {
		if c.Test != nil {
			if err := da.walkExprDA(c.Test); err != nil {
				da.frames = da.frames[:len(da.frames)-1]
				return err
			}
		}
		if _, err := da.walkStmts(c.Body); err != nil {
			da.frames = da.frames[:len(da.frames)-1]
			return err
		}
	}
	da.frames = da.frames[:len(da.frames)-1]
	da.assigned = before
	for n := range switchDefiniteAssigns(s.Cases) {
		da.markAssigned(n)
	}
	return nil
}

func (da *daState) walkDATry(s *ast.TryStatement) error {
	// Over-approximate: pre-seed every name assigned anywhere in try/catch/
	// finally as assigned, so a try/catch that assigns a binding on both the
	// normal and error paths is never wrongly flagged. Sound (never rejects
	// valid code); misses a binding left unassigned on some path.
	names := map[string]bool{}
	if s.Body != nil {
		for _, stmt := range s.Body.Body {
			gatherAssignedNamesStmt(stmt, names)
		}
	}
	if s.Catch != nil && s.Catch.Body != nil {
		for _, stmt := range s.Catch.Body.Body {
			gatherAssignedNamesStmt(stmt, names)
		}
	}
	if s.Finally != nil {
		for _, stmt := range s.Finally.Body {
			gatherAssignedNamesStmt(stmt, names)
		}
	}
	for n := range names {
		da.markAssigned(n)
	}

	if s.Body != nil {
		if _, err := da.walkDABlock(s.Body.Body); err != nil {
			return err
		}
	}
	if s.Catch != nil && s.Catch.Body != nil {
		fr := &daFrame{ids: map[string]int{}}
		da.frames = append(da.frames, fr)
		if s.Catch.Param != "" {
			id := da.declare(fr, s.Catch.Param, false)
			da.assigned[id] = true
		}
		for _, p := range s.Catch.ObjectPattern {
			if p.Local != "" {
				id := da.declare(fr, p.Local, false)
				da.assigned[id] = true
			}
		}
		_, err := da.walkStmts(s.Catch.Body.Body)
		da.frames = da.frames[:len(da.frames)-1]
		if err != nil {
			return err
		}
	}
	if s.Finally != nil {
		if _, err := da.walkDABlock(s.Finally.Body); err != nil {
			return err
		}
	}
	return nil
}

func (da *daState) walkDAClass(c *ast.ClassDeclaration) error {
	walkMethod := func(fn *ast.FunctionDeclaration) error {
		if fn != nil && fn.Body != nil {
			return da.walkFunc(fn.Body.Body, fn.Params)
		}
		return nil
	}
	if err := walkMethod(c.Constructor); err != nil {
		return err
	}
	for _, m := range c.Methods {
		if err := walkMethod(m); err != nil {
			return err
		}
	}
	return nil
}

// walkExprDA walks an expression, checking reads and recording assignments.
func (da *daState) walkExprDA(expr ast.Expression) error {
	switch e := expr.(type) {
	case *ast.Identifier:
		return da.checkAssigned(e.Name, e.GetPos())
	case *ast.AssignmentExpression:
		if err := da.walkExprDA(e.Right); err != nil {
			return err
		}
		if id, ok := e.Left.(*ast.Identifier); ok {
			if e.Op != "=" {
				// Compound assignment reads the target before writing it.
				if err := da.checkAssigned(id.Name, id.GetPos()); err != nil {
					return err
				}
			}
			da.markAssigned(id.Name)
			return nil
		}
		return da.walkExprDA(e.Left)
	case *ast.UpdateExpression:
		if id, ok := e.Arg.(*ast.Identifier); ok {
			if err := da.checkAssigned(id.Name, id.GetPos()); err != nil {
				return err
			}
			da.markAssigned(id.Name)
			return nil
		}
		return da.walkExprDA(e.Arg)
	case *ast.AwaitExpression:
		return da.walkExprDA(e.Argument)
	case *ast.YieldExpression:
		if e.Argument != nil {
			return da.walkExprDA(e.Argument)
		}
	case *ast.BinaryExpression:
		if err := da.walkExprDA(e.Left); err != nil {
			return err
		}
		return da.walkExprDA(e.Right)
	case *ast.ConditionalExpression:
		if err := da.walkExprDA(e.Test); err != nil {
			return err
		}
		if err := da.walkExprDA(e.Consequent); err != nil {
			return err
		}
		return da.walkExprDA(e.Alternate)
	case *ast.SequenceExpression:
		for _, x := range e.Exprs {
			if err := da.walkExprDA(x); err != nil {
				return err
			}
		}
	case *ast.SpreadElement:
		return da.walkExprDA(e.Arg)
	case *ast.UnaryExpression:
		return da.walkExprDA(e.Arg)
	case *ast.CallExpression:
		if err := da.walkExprDA(e.Callee); err != nil {
			return err
		}
		for _, a := range e.Args {
			if err := da.walkExprDA(a); err != nil {
				return err
			}
		}
	case *ast.MemberExpression:
		return da.walkExprDA(e.Object)
	case *ast.IndexExpression:
		if err := da.walkExprDA(e.Object); err != nil {
			return err
		}
		return da.walkExprDA(e.Index)
	case *ast.ArrayLiteral:
		for _, x := range e.Elements {
			if err := da.walkExprDA(x); err != nil {
				return err
			}
		}
	case *ast.NewArrayExpression:
		if e.Size != nil {
			return da.walkExprDA(e.Size)
		}
	case *ast.ObjectLiteral:
		for i := range e.Properties {
			if e.Properties[i].KeyExpr != nil {
				if err := da.walkExprDA(e.Properties[i].KeyExpr); err != nil {
					return err
				}
			}
			if e.Properties[i].Value != nil {
				if err := da.walkExprDA(e.Properties[i].Value); err != nil {
					return err
				}
			}
		}
	case *ast.TemplateLiteral:
		for _, x := range e.Exprs {
			if err := da.walkExprDA(x); err != nil {
				return err
			}
		}
	case *ast.ArrowFunction:
		if e.Block != nil {
			return da.walkFunc(e.Block.Body, e.Params)
		}
		if e.Body != nil {
			// Expression-bodied arrow: a fresh function unit whose only outer
			// references are cross-boundary (exempt).
			da.frames = append(da.frames, &daFrame{ids: map[string]int{}, funcBoundary: true})
			for _, p := range e.Params {
				for _, n := range paramNames(p) {
					id := da.declare(da.frames[len(da.frames)-1], n, false)
					da.assigned[id] = true
				}
			}
			err := da.walkExprDA(e.Body)
			da.frames = da.frames[:len(da.frames)-1]
			return err
		}
	case *ast.FunctionExpression:
		return da.walkFunc(e.Body.Body, e.Params)
	}
	return nil
}

// checkAssigned errors if name resolves to a tracked binding, within the same
// function, that isn't definitely assigned. A cross-function reference is
// exempt (the closure may run after the assignment).
func (da *daState) checkAssigned(name string, pos ast.Pos) error {
	crossed := false
	for i := len(da.frames) - 1; i >= 0; i-- {
		fr := da.frames[i]
		if id, ok := fr.ids[name]; ok {
			if !crossed && da.tracked[id] && !da.assigned[id] {
				return fmt.Errorf("%d:%d: variable '%s' is used before being assigned", pos.Line, pos.Col, name)
			}
			return nil
		}
		if fr.funcBoundary {
			crossed = true
		}
	}
	return nil
}

// markAssigned records an assignment to name against its resolved binding.
func (da *daState) markAssigned(name string) {
	if id := da.resolveID(name); id >= 0 {
		da.assigned[id] = true
	}
}

// resolveID returns the binding id name currently resolves to, or -1.
func (da *daState) resolveID(name string) int {
	for i := len(da.frames) - 1; i >= 0; i-- {
		if id, ok := da.frames[i].ids[name]; ok {
			return id
		}
	}
	return -1
}

// isExemptAnnotation reports whether a declared type opts a binding out of the
// definite-assignment check: `any`/`unknown` (legitimately hold `undefined`) or
// a nullable `T | null`/`T | undefined` (its own presence representation).
func isExemptAnnotation(ta *ast.TypeAnnotation) bool {
	if ta == nil {
		return false // unannotated: still concrete-typed here, so tracked
	}
	return ta.Name == "any" || ta.Name == "unknown" || ta.Nullable
}

// gatherFuncVars collects the function-scoped `var` names in body (not crossing
// a nested function), mapping each to whether it is tracked (non-exempt type).
func gatherFuncVars(body []ast.Statement, out map[string]bool) {
	for _, stmt := range body {
		gatherFuncVarsStmt(stmt, out)
	}
}

func gatherFuncVarsStmt(stmt ast.Statement, out map[string]bool) {
	track := func(name string, ta *ast.TypeAnnotation) {
		if name == "" {
			return
		}
		// `var` is hoisted and undefined-initialized in JS — it has no TDZ,
		// so a read before assignment is legal (yielding this compiler's
		// deterministic zero/undefined default, ADR-00215). Definite
		// assignment therefore never tracks `var` bindings — only `let`/
		// `const` (real TDZ) are enforced (ADR-00454; previously `var` was
		// lumped in with `let`).
		_ = ta
		out[name] = false
	}
	switch s := stmt.(type) {
	case *ast.VarDeclaration:
		if s.Kind == "var" {
			track(s.Name, s.TypeAnnot)
		}
	case *ast.VarDeclarationList:
		for _, d := range s.Decls {
			if d.Kind == "var" {
				track(d.Name, d.TypeAnnot)
			}
		}
	case *ast.BlockStatement:
		gatherFuncVars(s.Body, out)
	case *ast.IfStatement:
		if s.Consequent != nil {
			gatherFuncVars(s.Consequent.Body, out)
		}
		if s.Alternate != nil {
			gatherFuncVarsStmt(s.Alternate, out)
		}
	case *ast.ForStatement:
		if s.Init != nil {
			gatherFuncVarsStmt(s.Init, out)
		}
		if s.Body != nil {
			gatherFuncVars(s.Body.Body, out)
		}
	case *ast.ForOfStatement:
		if s.Body != nil {
			gatherFuncVars(s.Body.Body, out)
		}
	case *ast.ForInStatement:
		if s.Body != nil {
			gatherFuncVars(s.Body.Body, out)
		}
	case *ast.WhileStatement:
		if s.Body != nil {
			gatherFuncVars(s.Body.Body, out)
		}
	case *ast.DoWhileStatement:
		if s.Body != nil {
			gatherFuncVars(s.Body.Body, out)
		}
	case *ast.SwitchStatement:
		for _, c := range s.Cases {
			gatherFuncVars(c.Body, out)
		}
	case *ast.TryStatement:
		if s.Body != nil {
			gatherFuncVars(s.Body.Body, out)
		}
		if s.Catch != nil && s.Catch.Body != nil {
			gatherFuncVars(s.Catch.Body.Body, out)
		}
		if s.Finally != nil {
			gatherFuncVars(s.Finally.Body, out)
		}
	case *ast.LabeledStatement:
		gatherFuncVarsStmt(s.Body, out)
	}
}

// gatherAssignedNamesStmt / gatherAssignedNamesExpr collect every name that a
// statement (or expression) assigns to — an `=`/compound assignment target, an
// `++`/`--` target, or a declaration initializer — recursively, without
// crossing a nested function boundary. Used to over-seed loop/switch/try
// analysis so their assignments never cause a false positive.
func gatherAssignedNamesStmt(stmt ast.Statement, out map[string]bool) {
	switch s := stmt.(type) {
	case *ast.VarDeclaration:
		if s.Init != nil {
			out[s.Name] = true
			gatherAssignedNamesExpr(s.Init, out)
		}
	case *ast.VarDeclarationList:
		for _, d := range s.Decls {
			if d.Init != nil {
				out[d.Name] = true
				gatherAssignedNamesExpr(d.Init, out)
			}
		}
	case *ast.ExpressionStatement:
		gatherAssignedNamesExpr(s.Expr, out)
	case *ast.ReturnStatement:
		if s.Value != nil {
			gatherAssignedNamesExpr(s.Value, out)
		}
	case *ast.ThrowStatement:
		gatherAssignedNamesExpr(s.Argument, out)
	case *ast.BlockStatement:
		for _, c := range s.Body {
			gatherAssignedNamesStmt(c, out)
		}
	case *ast.IfStatement:
		gatherAssignedNamesExpr(s.Test, out)
		if s.Consequent != nil {
			for _, c := range s.Consequent.Body {
				gatherAssignedNamesStmt(c, out)
			}
		}
		if s.Alternate != nil {
			gatherAssignedNamesStmt(s.Alternate, out)
		}
	case *ast.ForStatement:
		if s.Init != nil {
			gatherAssignedNamesStmt(s.Init, out)
		}
		if s.Body != nil {
			for _, c := range s.Body.Body {
				gatherAssignedNamesStmt(c, out)
			}
		}
	case *ast.ForOfStatement:
		if s.Body != nil {
			for _, c := range s.Body.Body {
				gatherAssignedNamesStmt(c, out)
			}
		}
	case *ast.ForInStatement:
		if s.Body != nil {
			for _, c := range s.Body.Body {
				gatherAssignedNamesStmt(c, out)
			}
		}
	case *ast.WhileStatement:
		if s.Body != nil {
			for _, c := range s.Body.Body {
				gatherAssignedNamesStmt(c, out)
			}
		}
	case *ast.DoWhileStatement:
		if s.Body != nil {
			for _, c := range s.Body.Body {
				gatherAssignedNamesStmt(c, out)
			}
		}
	case *ast.SwitchStatement:
		for _, c := range s.Cases {
			for _, stmt := range c.Body {
				gatherAssignedNamesStmt(stmt, out)
			}
		}
	case *ast.TryStatement:
		if s.Body != nil {
			for _, c := range s.Body.Body {
				gatherAssignedNamesStmt(c, out)
			}
		}
		if s.Catch != nil && s.Catch.Body != nil {
			for _, c := range s.Catch.Body.Body {
				gatherAssignedNamesStmt(c, out)
			}
		}
		if s.Finally != nil {
			for _, c := range s.Finally.Body {
				gatherAssignedNamesStmt(c, out)
			}
		}
	case *ast.LabeledStatement:
		gatherAssignedNamesStmt(s.Body, out)
	}
}

func gatherAssignedNamesExpr(expr ast.Expression, out map[string]bool) {
	switch e := expr.(type) {
	case *ast.AssignmentExpression:
		if id, ok := e.Left.(*ast.Identifier); ok {
			out[id.Name] = true
		} else {
			gatherAssignedNamesExpr(e.Left, out)
		}
		gatherAssignedNamesExpr(e.Right, out)
	case *ast.UpdateExpression:
		if id, ok := e.Arg.(*ast.Identifier); ok {
			out[id.Name] = true
		} else {
			gatherAssignedNamesExpr(e.Arg, out)
		}
	case *ast.BinaryExpression:
		gatherAssignedNamesExpr(e.Left, out)
		gatherAssignedNamesExpr(e.Right, out)
	case *ast.ConditionalExpression:
		gatherAssignedNamesExpr(e.Test, out)
		gatherAssignedNamesExpr(e.Consequent, out)
		gatherAssignedNamesExpr(e.Alternate, out)
	case *ast.SequenceExpression:
		for _, x := range e.Exprs {
			gatherAssignedNamesExpr(x, out)
		}
	case *ast.UnaryExpression:
		gatherAssignedNamesExpr(e.Arg, out)
	case *ast.SpreadElement:
		gatherAssignedNamesExpr(e.Arg, out)
	case *ast.AwaitExpression:
		gatherAssignedNamesExpr(e.Argument, out)
	case *ast.CallExpression:
		gatherAssignedNamesExpr(e.Callee, out)
		for _, a := range e.Args {
			gatherAssignedNamesExpr(a, out)
		}
	case *ast.IndexExpression:
		gatherAssignedNamesExpr(e.Object, out)
		gatherAssignedNamesExpr(e.Index, out)
	case *ast.ArrayLiteral:
		for _, x := range e.Elements {
			gatherAssignedNamesExpr(x, out)
		}
	case *ast.TemplateLiteral:
		for _, x := range e.Exprs {
			gatherAssignedNamesExpr(x, out)
		}
		// Arrow/function expressions are function boundaries — their internal
		// assignments belong to their own scope, so they're not gathered here.
	}
}

// definiteAssignsOf computes the set of names a statement list *definitely*
// assigns when run once — a name-based must-analysis used to credit the
// precise set after a do/while body (which runs at least once) and per switch
// case. It is a sound over-approximation of the true definite set: straight-line
// assignments and `if`/`else` (intersection of both branches) are exact, while
// nested loops/`try`/unrecognized statements fall back to gathering every name
// they *might* assign. Over-approximating the credit is what keeps the caller
// free of false positives — crediting too little would reject valid code.
func definiteAssignsOf(body []ast.Statement) map[string]bool {
	out := map[string]bool{}
	for _, s := range body {
		d, diverges := definiteAssignsOfStmt(s)
		for n := range d {
			out[n] = true
		}
		if diverges {
			break
		}
	}
	return out
}

func definiteAssignsOfStmt(stmt ast.Statement) (map[string]bool, bool) {
	switch s := stmt.(type) {
	case *ast.ExpressionStatement:
		out := map[string]bool{}
		if a, ok := s.Expr.(*ast.AssignmentExpression); ok {
			if id, ok := a.Left.(*ast.Identifier); ok {
				out[id.Name] = true
			}
		}
		if u, ok := s.Expr.(*ast.UpdateExpression); ok {
			if id, ok := u.Arg.(*ast.Identifier); ok {
				out[id.Name] = true
			}
		}
		return out, false
	case *ast.VarDeclaration:
		out := map[string]bool{}
		if s.Init != nil {
			out[s.Name] = true
		}
		return out, false
	case *ast.VarDeclarationList:
		out := map[string]bool{}
		for _, d := range s.Decls {
			if d.Init != nil {
				out[d.Name] = true
			}
		}
		return out, false
	case *ast.BlockStatement:
		return definiteAssignsOf(s.Body), allPathsExit(s.Body)
	case *ast.IfStatement:
		if s.Alternate == nil {
			return map[string]bool{}, false // may be skipped — nothing definite
		}
		var consD map[string]bool
		if s.Consequent != nil {
			consD = definiteAssignsOf(s.Consequent.Body)
		} else {
			consD = map[string]bool{}
		}
		altD, altDiv := definiteAssignsOfStmt(s.Alternate)
		consDiv := s.Consequent != nil && allPathsExit(s.Consequent.Body)
		switch {
		case consDiv && altDiv:
			return unionStrSet(consD, altD), true
		case consDiv:
			return altD, false
		case altDiv:
			return consD, false
		default:
			return intersectStrSet(consD, altD), false
		}
	case *ast.SwitchStatement:
		return switchDefiniteAssigns(s.Cases), false
	case *ast.LabeledStatement:
		return definiteAssignsOfStmt(s.Body)
	case *ast.ReturnStatement, *ast.ThrowStatement, *ast.BreakStatement, *ast.ContinueStatement:
		return map[string]bool{}, true // diverges — assigns nothing on the way out
	case *ast.TryStatement:
		// try/catch may run partially; only finally is guaranteed.
		if s.Finally != nil {
			return definiteAssignsOf(s.Finally.Body), false
		}
		return map[string]bool{}, false
	case *ast.ForStatement, *ast.ForOfStatement, *ast.ForInStatement,
		*ast.WhileStatement, *ast.DoWhileStatement:
		// A nested loop can't be proven to run (except do/while, kept simple):
		// over-approximate by gathering everything it might assign, so a nested
		// definite assignment is never lost (which would be a false positive).
		names := map[string]bool{}
		gatherAssignedNamesStmt(stmt, names)
		return names, false
	}
	// Unrecognized: over-approximate to stay sound.
	names := map[string]bool{}
	gatherAssignedNamesStmt(stmt, names)
	return names, false
}

// switchDefiniteAssigns returns the names a switch definitely assigns on every
// path — the intersection, over each case entry, of what running from that case
// through fall-through (until a break/return) assigns. A switch with no default
// has an unmatched path that assigns nothing, so it credits nothing.
func switchDefiniteAssigns(cases []ast.SwitchCase) map[string]bool {
	hasDefault := false
	for _, c := range cases {
		if c.Test == nil {
			hasDefault = true
		}
	}
	if !hasDefault {
		return map[string]bool{}
	}
	var result map[string]bool
	for i := range cases {
		var flat []ast.Statement
		for j := i; j < len(cases); j++ {
			flat = append(flat, cases[j].Body...)
		}
		entry := definiteAssignsOf(flat)
		if result == nil {
			result = entry
		} else {
			result = intersectStrSet(result, entry)
		}
	}
	if result == nil {
		return map[string]bool{}
	}
	return result
}

func intersectStrSet(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		if b[k] {
			out[k] = true
		}
	}
	return out
}

func unionStrSet(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

// --- control-flow helpers ---

func copyIntSet(m map[int]bool) map[int]bool {
	out := make(map[int]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func intersectIntSet(a, b map[int]bool) map[int]bool {
	out := map[int]bool{}
	for k := range a {
		if b[k] {
			out[k] = true
		}
	}
	return out
}

func unionIntSet(a, b map[int]bool) map[int]bool {
	out := copyIntSet(a)
	for k := range b {
		out[k] = true
	}
	return out
}

// blockMayExit / stmtMayExit over-approximate "this branch might not complete
// normally": true if it contains any return/throw/break/continue, without
// crossing a nested function boundary. Over-approximating exits is sound — it
// only ever excludes a branch from the merge, never wrongly includes one.
func blockMayExit(body []ast.Statement) bool {
	for _, s := range body {
		if stmtMayExit(s) {
			return true
		}
	}
	return false
}

func stmtMayExit(stmt ast.Statement) bool {
	switch s := stmt.(type) {
	case *ast.ReturnStatement, *ast.ThrowStatement, *ast.BreakStatement, *ast.ContinueStatement:
		return true
	case *ast.BlockStatement:
		return blockMayExit(s.Body)
	case *ast.IfStatement:
		if s.Consequent != nil && blockMayExit(s.Consequent.Body) {
			return true
		}
		return s.Alternate != nil && stmtMayExit(s.Alternate)
	case *ast.ForStatement:
		return s.Body != nil && blockMayExit(s.Body.Body)
	case *ast.ForOfStatement:
		return s.Body != nil && blockMayExit(s.Body.Body)
	case *ast.ForInStatement:
		return s.Body != nil && blockMayExit(s.Body.Body)
	case *ast.WhileStatement:
		return s.Body != nil && blockMayExit(s.Body.Body)
	case *ast.DoWhileStatement:
		return s.Body != nil && blockMayExit(s.Body.Body)
	case *ast.SwitchStatement:
		for _, c := range s.Cases {
			if blockMayExit(c.Body) {
				return true
			}
		}
	case *ast.TryStatement:
		if s.Body != nil && blockMayExit(s.Body.Body) {
			return true
		}
		if s.Catch != nil && s.Catch.Body != nil && blockMayExit(s.Catch.Body.Body) {
			return true
		}
		return s.Finally != nil && blockMayExit(s.Finally.Body)
	case *ast.LabeledStatement:
		return stmtMayExit(s.Body)
	}
	return false
}

// allPathsExit / allPathsExit2 conservatively report whether a branch exits on
// *every* path (used only to decide whether the whole if diverges, which just
// prunes dead code — an under-approximation is safe here).
func allPathsExit(body []ast.Statement) bool {
	for _, s := range body {
		switch s.(type) {
		case *ast.ReturnStatement, *ast.ThrowStatement, *ast.BreakStatement, *ast.ContinueStatement:
			return true
		}
	}
	return false
}

func allPathsExit2(stmt ast.Statement) bool {
	switch s := stmt.(type) {
	case *ast.ReturnStatement, *ast.ThrowStatement, *ast.BreakStatement, *ast.ContinueStatement:
		return true
	case *ast.BlockStatement:
		return allPathsExit(s.Body)
	}
	return false
}

// paramNames returns every name a parameter binds, flattening a destructuring
// pattern to its leaf names.
func paramNames(p ast.Param) []string {
	switch {
	case p.ArrayPattern != nil:
		var ns []string
		for _, e := range p.ArrayPattern {
			collectArrayPatternNames(e, &ns)
		}
		return ns
	case p.ObjectPattern != nil:
		var ns []string
		for _, dp := range p.ObjectPattern {
			collectObjectPatternNames(dp, &ns)
		}
		return ns
	default:
		return []string{p.Name}
	}
}
