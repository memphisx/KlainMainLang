// Package freevars finds the free variables of AST subtrees: the names
// an expression or statement list reads that it does not itself bind.
package freevars

import "KlainMainLang/ast"

// AddParamBoundNames adds the names params actually bind in their own body
// scope. A destructured param's real bound names are its pattern's field/
// element names, not the param's own Name (a synthetic internal name, e.g.
// "__param0", never referenced by the body) — see gatherCaptures' own
// comment for the wrong-answer bug this avoids: a pattern field sharing a
// name with an outer-scope variable must shadow it, not be free-variable-
// scanned as a capture of the outer binding.
func AddParamBoundNames(bound map[string]bool, params []ast.Param) {
	for _, p := range params {
		bound[p.Name] = true
		// Recurse into nested sub-patterns (TDD-00065 Stage 2) so a
		// nested-bound name shadows an outer variable rather than being
		// scanned as a capture of it.
		CollectArrayPatternNames(p.ArrayPattern, bound)
		CollectObjectPatternNames(p.ObjectPattern, bound)
	}
}

// ScanExprFV adds to result the names expr reads that neither bound nor
// expr itself binds.
func ScanExprFV(expr ast.Expression, bound map[string]bool, result map[string]bool) {
	if expr == nil {
		return
	}
	switch x := expr.(type) {
	case *ast.Identifier:
		if !bound[x.Name] {
			result[x.Name] = true
		}
	case *ast.BinaryExpression:
		ScanExprFV(x.Left, bound, result)
		ScanExprFV(x.Right, bound, result)
	case *ast.UnaryExpression:
		ScanExprFV(x.Arg, bound, result)
	case *ast.YieldExpression:
		// `yield e` / `yield* e` — the operand is a free-variable position (a bare
		// `yield` has a nil argument). Needed so a nested generator's captures are
		// detected (TDD-00094).
		ScanExprFV(x.Argument, bound, result)
	case *ast.AwaitExpression:
		ScanExprFV(x.Argument, bound, result)
	case *ast.UpdateExpression:
		ScanExprFV(x.Arg, bound, result)
	case *ast.AssignmentExpression:
		ScanExprFV(x.Left, bound, result)
		ScanExprFV(x.Right, bound, result)
	case *ast.CallExpression:
		ScanExprFV(x.Callee, bound, result)
		for _, a := range x.Args {
			ScanExprFV(a, bound, result)
		}
	case *ast.MemberExpression:
		ScanExprFV(x.Object, bound, result) // Property is a string, not a var ref
	case *ast.IndexExpression:
		ScanExprFV(x.Object, bound, result)
		ScanExprFV(x.Index, bound, result)
	case *ast.ArrayLiteral:
		for _, e := range x.Elements {
			ScanExprFV(e, bound, result)
		}
	case *ast.ObjectLiteral:
		for _, p := range x.Properties {
			ScanExprFV(p.KeyExpr, bound, result)
			ScanExprFV(p.Value, bound, result)
		}
	case *ast.SpreadElement:
		ScanExprFV(x.Arg, bound, result)
	case *ast.NewArrayExpression:
		ScanExprFV(x.Size, bound, result)
	case *ast.NewTargetExpression:
		ScanExprFV(x.This, bound, result)
	case *ast.NewSetExpression:
		ScanExprFV(x.Init, bound, result)
	case *ast.NewWeakSetExpression:
		ScanExprFV(x.Init, bound, result)
	case *ast.NewWeakMapExpression:
		ScanExprFV(x.Init, bound, result)
	case *ast.NewMapExpression:
		ScanExprFV(x.Init, bound, result)
	case *ast.TemplateLiteral:
		for _, ex := range x.Exprs {
			ScanExprFV(ex, bound, result)
		}
	case *ast.TaggedTemplateExpression:
		ScanExprFV(x.Tag, bound, result)
		for _, ex := range x.Exprs {
			ScanExprFV(ex, bound, result)
		}
	case *ast.ConditionalExpression:
		ScanExprFV(x.Test, bound, result)
		ScanExprFV(x.Consequent, bound, result)
		ScanExprFV(x.Alternate, bound, result)
	case *ast.SequenceExpression:
		for _, sub := range x.Exprs {
			ScanExprFV(sub, bound, result)
		}
	case *ast.NonNullExpression:
		ScanExprFV(x.Arg, bound, result)
	case *ast.AsExpression:
		ScanExprFV(x.Expr, bound, result)
	case *ast.NewTypedArrayExpression:
		// `new Int32Array(buf)` inside a closure body: buf is a real free
		// variable (was silently unscanned, later failing as "undefined
		// variable" at emission — found via a SharedArrayBuffer view in a
		// worker handler, TDD-00099).
		ScanExprFV(x.Arg, bound, result)
		if x.ByteOffset != nil {
			ScanExprFV(x.ByteOffset, bound, result)
		}
		if x.Length != nil {
			ScanExprFV(x.Length, bound, result)
		}
	case *ast.NewArrayBufferExpression:
		ScanExprFV(x.ByteLength, bound, result)
	case *ast.NewCompressionStreamExpression:
		if x.Format != nil {
			ScanExprFV(x.Format, bound, result)
		}
	case *ast.NewTransformStreamExpression:
		if x.Transformer != nil {
			ScanExprFV(x.Transformer, bound, result)
		}
		if x.WritableStrategy != nil {
			ScanExprFV(x.WritableStrategy, bound, result)
		}
		if x.ReadableStrategy != nil {
			ScanExprFV(x.ReadableStrategy, bound, result)
		}
	case *ast.NewWritableStreamExpression:
		if x.Sink != nil {
			ScanExprFV(x.Sink, bound, result)
		}
		if x.Strategy != nil {
			ScanExprFV(x.Strategy, bound, result)
		}
	case *ast.NewReadableStreamExpression:
		// The underlying source / strategy object literals hold callbacks
		// whose free variables are real captures (TDD-00097 Stage 1).
		if x.Source != nil {
			ScanExprFV(x.Source, bound, result)
		}
		if x.Strategy != nil {
			ScanExprFV(x.Strategy, bound, result)
		}
	case *ast.ArrowFunction:
		// Nested arrow function: its params are bound within its own body.
		innerBound := make(map[string]bool, len(bound)+len(x.Params))
		for k, v := range bound {
			innerBound[k] = v
		}
		AddParamBoundNames(innerBound, x.Params)
		ScanParamDefaultsFV(x.Params, innerBound, result)
		if x.Body != nil {
			ScanExprFV(x.Body, innerBound, result)
		}
		if x.Block != nil {
			ScanStmtsFV(x.Block.Body, innerBound, result)
		}
	case *ast.FunctionExpression:
		// Function expression: its own params are bound within its body;
		// no outer captures leak automatically but the body's free vars
		// against the outer bound set ARE actual captures.
		innerBound := make(map[string]bool, len(bound)+len(x.Params))
		for k, v := range bound {
			innerBound[k] = v
		}
		AddParamBoundNames(innerBound, x.Params)
		innerBound["arguments"] = true // its own, not the enclosing function's
		ScanParamDefaultsFV(x.Params, innerBound, result)
		ScanStmtsFV(x.Body.Body, innerBound, result)
	case *ast.NewExpression:
		// `new K(…)` reads K as a binding when one is in scope (gatherCaptures
		// keeps only names it can look up, so a class name drops out).
		if x.Callee == nil && !x.Qualified && x.ClassName != "" && !bound[x.ClassName] {
			result[x.ClassName] = true
		}
		// `new obj.method()`: its qualifier's head is a read.
		if h := NewQualifierHead(x); h != "" && !bound[h] {
			result[h] = true
		}
		ast.ForEachChild(expr, func(ch ast.Node) bool {
			if ce, ok := ch.(ast.Expression); ok {
				ScanExprFV(ce, bound, result)
			}
			return true
		})
	default:
		// Any other expression (`typeof x`, …): its
		// sub-expressions, through the generated traversal, so a kind the
		// cases above do not name is still scanned. Literals have none.
		ast.ForEachChild(expr, func(ch ast.Node) bool {
			if ce, ok := ch.(ast.Expression); ok {
				ScanExprFV(ce, bound, result)
			}
			return true
		})
	}
}

// CollectArrayPatternNames records every leaf binding name introduced by an
// array destructuring pattern, recursing into nested sub-patterns
// (TDD-00065 Stage 2) so the free-variable scan doesn't misclassify a
// nested-bound name as an outer capture.
func CollectArrayPatternNames(elems []ast.ArrayPatternElem, out map[string]bool) {
	for _, el := range elems {
		switch {
		case el.SubArray != nil:
			CollectArrayPatternNames(el.SubArray, out)
		case el.SubObject != nil:
			CollectObjectPatternNames(el.SubObject, out)
		case el.Name != "":
			out[el.Name] = true
		}
	}
}

// CollectObjectPatternNames is CollectArrayPatternNames' object-pattern
// counterpart.
func CollectObjectPatternNames(props []ast.DestructProp, out map[string]bool) {
	for _, pr := range props {
		switch {
		case pr.SubArray != nil:
			CollectArrayPatternNames(pr.SubArray, out)
		case pr.SubObject != nil:
			CollectObjectPatternNames(pr.SubObject, out)
		default:
			out[pr.Local] = true
		}
	}
}

// ScanStmtsFV is ScanExprFV over a statement list, its declarations
// binding names for the statements after them.
func ScanStmtsFV(stmts []ast.Statement, bound map[string]bool, result map[string]bool) {
	// Copy bound so local declarations don't bleed back to the caller.
	local := make(map[string]bool, len(bound))
	for k, v := range bound {
		local[k] = v
	}
	for _, stmt := range stmts {
		if fd, ok := stmt.(*ast.FunctionDeclaration); ok && fd.Name != "" {
			local[fd.Name] = true // hoisted: bound throughout the block
		}
	}
	// A `var` (a loop head's too) is the function's, bound throughout it.
	CollectVarNames(stmts, local)
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.FunctionDeclaration:
			// A nested declaration's free names are this scope's too.
			inner := make(map[string]bool, len(local)+len(s.Params)+1)
			for k, v := range local {
				inner[k] = v
			}
			inner["arguments"] = true
			AddParamBoundNames(inner, s.Params)
			ScanParamDefaultsFV(s.Params, inner, result)
			if s.Body != nil {
				ScanStmtsFV(s.Body.Body, inner, result)
			}
		case *ast.VarDeclaration:
			if s.Init != nil {
				ScanExprFV(s.Init, local, result)
			}
			local[s.Name] = true
		case *ast.VarDeclarationList:
			for _, d := range s.Decls {
				if d.Init != nil {
					ScanExprFV(d.Init, local, result)
				}
				local[d.Name] = true
			}
		case *ast.ExpressionStatement:
			ScanExprFV(s.Expr, local, result)
		case *ast.ReturnStatement:
			if s.Value != nil {
				ScanExprFV(s.Value, local, result)
			}
		case *ast.IfStatement:
			ScanExprFV(s.Test, local, result)
			if s.Consequent != nil {
				ScanStmtsFV(s.Consequent.Body, local, result)
			}
			if s.Alternate != nil {
				ScanStmtsFV([]ast.Statement{s.Alternate}, local, result)
			}
		case *ast.ForStatement:
			inner := make(map[string]bool, len(local))
			for k, v := range local {
				inner[k] = v
			}
			if s.Init != nil {
				if vd, ok := s.Init.(*ast.VarDeclaration); ok {
					if vd.Init != nil {
						ScanExprFV(vd.Init, inner, result)
					}
					inner[vd.Name] = true
				} else if vdl, ok := s.Init.(*ast.VarDeclarationList); ok {
					for _, d := range vdl.Decls {
						if d.Init != nil {
							ScanExprFV(d.Init, inner, result)
						}
						inner[d.Name] = true
					}
				} else if es, ok := s.Init.(*ast.ExpressionStatement); ok {
					ScanExprFV(es.Expr, inner, result)
				}
			}
			if s.Test != nil {
				ScanExprFV(s.Test, inner, result)
			}
			for _, upd := range s.Update {
				ScanExprFV(upd, inner, result)
			}
			if s.Body != nil {
				ScanStmtsFV(s.Body.Body, inner, result)
			}
		case *ast.WhileStatement:
			ScanExprFV(s.Test, local, result)
			if s.Body != nil {
				ScanStmtsFV(s.Body.Body, local, result)
			}
		case *ast.BlockStatement:
			ScanStmtsFV(s.Body, local, result)
		case *ast.ForOfStatement:
			ScanExprFV(s.Iterable, local, result)
			inner := make(map[string]bool, len(local)+1)
			for k, v := range local {
				inner[k] = v
			}
			// A destructuring loop variable (TDD-00065) binds each of its
			// pattern's (possibly nested) leaf names, not a single VarName.
			if s.ArrayPattern != nil {
				CollectArrayPatternNames(s.ArrayPattern, inner)
			} else if s.ObjectPattern != nil {
				CollectObjectPatternNames(s.ObjectPattern, inner)
			} else {
				inner[s.VarName] = true
			}
			// Its defaults and computed keys run each iteration.
			ForEachPatternExpr(s.ArrayPattern, s.ObjectPattern, func(x ast.Expression) { ScanExprFV(x, inner, result) })
			if s.Body != nil {
				ScanStmtsFV(s.Body.Body, inner, result)
			}
		case *ast.ForInStatement:
			ScanExprFV(s.Object, local, result)
			inner := make(map[string]bool, len(local)+1)
			for k, v := range local {
				inner[k] = v
			}
			inner[s.VarName] = true
			if s.Body != nil {
				ScanStmtsFV(s.Body.Body, inner, result)
			}
		case *ast.DoWhileStatement:
			if s.Body != nil {
				ScanStmtsFV(s.Body.Body, local, result)
			}
			ScanExprFV(s.Test, local, result)
		case *ast.LabeledStatement:
			if s.Body != nil {
				ScanStmtsFV([]ast.Statement{s.Body}, local, result)
			}
		case *ast.ThrowStatement:
			ScanExprFV(s.Argument, local, result)
		case *ast.TryStatement:
			if s.Body != nil {
				ScanStmtsFV(s.Body.Body, local, result)
			}
			if s.Catch != nil {
				inner := make(map[string]bool, len(local)+1)
				for k, v := range local {
					inner[k] = v
				}
				if s.Catch.Param != "" {
					inner[s.Catch.Param] = true
				}
				CollectObjectPatternNames(s.Catch.ObjectPattern, inner)
				ForEachPatternExpr(nil, s.Catch.ObjectPattern, func(x ast.Expression) { ScanExprFV(x, inner, result) })
				if s.Catch.Body != nil {
					ScanStmtsFV(s.Catch.Body.Body, inner, result)
				}
			}
			if s.Finally != nil {
				ScanStmtsFV(s.Finally.Body, local, result)
			}
		case *ast.SwitchStatement:
			ScanExprFV(s.Discriminant, local, result)
			for _, c := range s.Cases {
				if c.Test != nil {
					ScanExprFV(c.Test, local, result)
				}
				ScanStmtsFV(c.Body, local, result)
			}
		case *ast.BreakStatement:
			// no identifier references
		case *ast.ContinueStatement:
			// no identifier references
		case *ast.ArrayDestructuring:
			ScanExprFV(s.Init, local, result)
			for _, elem := range s.Elems {
				if elem.Default != nil {
					ScanExprFV(elem.Default, local, result)
				}
				if elem.Name != "" {
					local[elem.Name] = true
				}
			}
		case *ast.ObjectDestructuring:
			ScanExprFV(s.Init, local, result)
			for _, prop := range s.Props {
				if prop.KeyExpr != nil {
					ScanExprFV(prop.KeyExpr, local, result)
				}
				if prop.Default != nil {
					ScanExprFV(prop.Default, local, result)
				}
				local[prop.Local] = true
			}
		}
	}
}

// ScanParamDefaultsFV adds the free names of params' defaults to refs: each
// parameter's own default and every default inside a destructuring pattern,
// all evaluated in the function's prologue.
func ScanParamDefaultsFV(params []ast.Param, bound, refs map[string]bool) {
	var arr func([]ast.ArrayPatternElem)
	var obj func([]ast.DestructProp)
	arr = func(elems []ast.ArrayPatternElem) {
		for _, el := range elems {
			if el.Default != nil {
				ScanExprFV(el.Default, bound, refs)
			}
			arr(el.SubArray)
			obj(el.SubObject)
		}
	}
	obj = func(props []ast.DestructProp) {
		for _, pr := range props {
			if pr.KeyExpr != nil {
				ScanExprFV(pr.KeyExpr, bound, refs)
			}
			if pr.Default != nil {
				ScanExprFV(pr.Default, bound, refs)
			}
			arr(pr.SubArray)
			obj(pr.SubObject)
		}
	}
	for _, p := range params {
		if p.Default != nil {
			ScanExprFV(p.Default, bound, refs)
		}
		arr(p.ArrayPattern)
		obj(p.ObjectPattern)
	}
}

// ForEachPatternExpr calls f on each default and computed key of a
// destructuring pattern, nested ones included.
func ForEachPatternExpr(ap []ast.ArrayPatternElem, op []ast.DestructProp, f func(ast.Expression)) {
	for _, el := range ap {
		if el.Default != nil {
			f(el.Default)
		}
		ForEachPatternExpr(el.SubArray, el.SubObject, f)
	}
	for _, pr := range op {
		if pr.KeyExpr != nil {
			f(pr.KeyExpr)
		}
		if pr.Default != nil {
			f(pr.Default)
		}
		ForEachPatternExpr(pr.SubArray, pr.SubObject, f)
	}
}

// NewQualifierHead is the binding a qualified `new a.b.C()` reads first
// (a), or "".
func NewQualifierHead(x *ast.NewExpression) string {
	if x.Callee != nil || !x.Qualified {
		return ""
	}
	if len(x.QualifierPath) > 0 {
		return x.QualifierPath[0]
	}
	return x.Qualifier
}

// CollectVarNames adds the names stmts declare with `var` at any depth of
// their blocks and loop heads, not in a nested function or class.
func CollectVarNames(stmts []ast.Statement, out map[string]bool) {
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		ast.ForEachChild(n, func(ch ast.Node) bool {
			switch c := ch.(type) {
			case *ast.ArrowFunction, *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
				return true
			case *ast.VarDeclaration:
				if c.Kind == "var" && c.Name != "" {
					out[c.Name] = true
				}
			case *ast.ArrayDestructuring:
				if c.Kind == "var" {
					CollectArrayPatternNames(c.Elems, out)
				}
			case *ast.ObjectDestructuring:
				if c.Kind == "var" {
					CollectObjectPatternNames(c.Props, out)
				}
			case *ast.ForInStatement:
				if c.Kind == "var" && c.VarName != "" {
					out[c.VarName] = true
				}
			case *ast.ForOfStatement:
				if c.Kind == "var" {
					switch {
					case c.ArrayPattern != nil:
						CollectArrayPatternNames(c.ArrayPattern, out)
					case c.ObjectPattern != nil:
						CollectObjectPatternNames(c.ObjectPattern, out)
					case c.VarName != "":
						out[c.VarName] = true
					}
				}
			}
			walk(ch)
			return true
		})
	}
	walk(&ast.BlockStatement{Body: stmts})
}
