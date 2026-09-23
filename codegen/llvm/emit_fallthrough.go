// emit_fallthrough.go — "can this body complete without returning a value?"
// (ADR-01061). An unannotated function whose body may fall off the end, or
// hit a bare `return;`, yields `undefined` on that path in JS, and tsc infers
// `T | undefined` for it. The analysis is conservative toward "may complete",
// the direction that only ever widens a return type.
package llvm

import "KlainMainLang/ast"

// widenIfMayFallOff widens an inferred return type to `T | undefined` when
// the body can also complete without a `return <value>`. Before ADR-01061 the
// fall-off path was an `unreachable`, so such a call was UB: clang -O2 turned
// `function f(a) { a = 1; if (cond) return true }` into an infinite loop.
func widenIfMayFallOff(block *ast.BlockStatement, inferred Type) Type {
	if blockMayCompleteNormally(block) || blockHasBareReturn(block) {
		return undefinedableElem(inferred)
	}
	return inferred
}

// blockMayCompleteNormally reports whether control can reach the end of the
// block: false only when some statement in it never completes normally
// (return/throw, an if/else or try/catch whose every arm does not, a
// `while (true)`/`for (;;)` with no `break` of its own, a `switch` with a
// default whose last group ends without completing and no own `break`).
func blockMayCompleteNormally(block *ast.BlockStatement) bool {
	if block == nil {
		return true
	}
	for _, st := range block.Body {
		if !stmtMayCompleteNormally(st) {
			return false
		}
	}
	return true
}

func stmtMayCompleteNormally(stmt ast.Statement) bool {
	switch s := stmt.(type) {
	case *ast.ReturnStatement, *ast.ThrowStatement:
		return false
	case *ast.BlockStatement:
		return blockMayCompleteNormally(s)
	case *ast.LabeledStatement:
		// A labeled loop's `break label` completes the labeled statement.
		return stmtMayCompleteNormally(s.Body) || stmtHasLabeledBreak(s.Body)
	case *ast.IfStatement:
		if s.Alternate == nil {
			return true
		}
		return blockMayCompleteNormally(s.Consequent) || stmtMayCompleteNormally(s.Alternate)
	case *ast.WhileStatement:
		return !isTrueLiteral(s.Test) || blockHasOwnBreak(s.Body)
	case *ast.ForStatement:
		return s.Test != nil || blockHasOwnBreak(s.Body)
	case *ast.DoWhileStatement:
		if blockHasOwnBreak(s.Body) {
			return true
		}
		return !isTrueLiteral(s.Test) && blockMayCompleteNormally(s.Body)
	case *ast.TryStatement:
		if s.Finally != nil && !blockMayCompleteNormally(s.Finally) {
			return false
		}
		if s.Catch == nil {
			return blockMayCompleteNormally(s.Body)
		}
		return blockMayCompleteNormally(s.Body) || blockMayCompleteNormally(s.Catch.Body)
	case *ast.SwitchStatement:
		hasDefault := false
		for _, c := range s.Cases {
			if c.Test == nil {
				hasDefault = true
			}
			if stmtsHaveOwnBreak(c.Body) {
				return true
			}
		}
		if !hasDefault || len(s.Cases) == 0 {
			return true
		}
		// Groups fall through into the next; the last group decides.
		last := s.Cases[len(s.Cases)-1]
		return len(last.Body) == 0 || stmtMayCompleteNormally(last.Body[len(last.Body)-1])
	}
	return true
}

func isTrueLiteral(expr ast.Expression) bool {
	if expr == nil {
		return true
	}
	b, ok := expr.(*ast.BooleanLiteral)
	return ok && b.Value
}

// blockHasOwnBreak reports a `break` that targets this loop/switch: an
// unlabeled break not nested in an inner loop/switch, or any labeled break
// (conservatively assumed to escape this statement).
func blockHasOwnBreak(block *ast.BlockStatement) bool {
	if block == nil {
		return false
	}
	return stmtsHaveOwnBreak(block.Body)
}

func stmtsHaveOwnBreak(stmts []ast.Statement) bool {
	for _, st := range stmts {
		if stmtHasOwnBreak(st) {
			return true
		}
	}
	return false
}

func stmtHasOwnBreak(stmt ast.Statement) bool {
	switch s := stmt.(type) {
	case *ast.BreakStatement:
		return true
	case *ast.BlockStatement:
		return stmtsHaveOwnBreak(s.Body)
	case *ast.IfStatement:
		if blockHasOwnBreak(s.Consequent) {
			return true
		}
		return s.Alternate != nil && stmtHasOwnBreak(s.Alternate)
	case *ast.TryStatement:
		if blockHasOwnBreak(s.Body) {
			return true
		}
		if s.Catch != nil && blockHasOwnBreak(s.Catch.Body) {
			return true
		}
		return blockHasOwnBreak(s.Finally)
	case *ast.LabeledStatement:
		return stmtHasOwnBreak(s.Body)
	case *ast.WhileStatement, *ast.DoWhileStatement, *ast.ForStatement,
		*ast.ForOfStatement, *ast.ForInStatement, *ast.SwitchStatement:
		// An unlabeled break inside targets the inner statement; a labeled
		// one may escape — count only those.
		return stmtHasLabeledBreak(stmt)
	}
	return false
}

// walkStmtTree visits every statement nested in stmt (not into function
// bodies — those are their own control flow), stopping once fn returns true.
func walkStmtTree(stmt ast.Statement, fn func(ast.Statement) bool) bool {
	if stmt == nil {
		return false
	}
	if fn(stmt) {
		return true
	}
	walkBlock := func(b *ast.BlockStatement) bool {
		if b == nil {
			return false
		}
		for _, st := range b.Body {
			if walkStmtTree(st, fn) {
				return true
			}
		}
		return false
	}
	switch s := stmt.(type) {
	case *ast.BlockStatement:
		return walkBlock(s)
	case *ast.IfStatement:
		return walkBlock(s.Consequent) || (s.Alternate != nil && walkStmtTree(s.Alternate, fn))
	case *ast.WhileStatement:
		return walkBlock(s.Body)
	case *ast.DoWhileStatement:
		return walkBlock(s.Body)
	case *ast.ForStatement:
		return walkBlock(s.Body)
	case *ast.ForOfStatement:
		return walkBlock(s.Body)
	case *ast.ForInStatement:
		return walkBlock(s.Body)
	case *ast.LabeledStatement:
		return walkStmtTree(s.Body, fn)
	case *ast.SwitchStatement:
		for _, c := range s.Cases {
			for _, b := range c.Body {
				if walkStmtTree(b, fn) {
					return true
				}
			}
		}
	case *ast.TryStatement:
		if walkBlock(s.Body) {
			return true
		}
		if s.Catch != nil && walkBlock(s.Catch.Body) {
			return true
		}
		return walkBlock(s.Finally)
	}
	return false
}

func stmtHasLabeledBreak(stmt ast.Statement) bool {
	return walkStmtTree(stmt, func(st ast.Statement) bool {
		b, ok := st.(*ast.BreakStatement)
		return ok && b.Label != ""
	})
}

// blockHasBareReturn reports a `return;` (no value) anywhere in the body
// outside nested functions — it yields `undefined` exactly like falling off.
func blockHasBareReturn(block *ast.BlockStatement) bool {
	return walkStmtTree(block, func(st ast.Statement) bool {
		r, ok := st.(*ast.ReturnStatement)
		return ok && r.Value == nil
	})
}
