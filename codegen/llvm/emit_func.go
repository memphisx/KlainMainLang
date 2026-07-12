// emit_func.go — function and closure emission: declarations, free-variable
// scanning, closure construction, and closure call paths.
package llvm

import (
	"fmt"
	"strings"
	"KlainMainLang/ast"
)

// emitFunctionDecl emits one user-defined function into e.functions.
func (e *Emitter) emitFunctionDecl(decl *ast.FunctionDeclaration) error {
	// Save current function context.
	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedScopes := e.scopes
	savedRetType := e.currentRetType
	savedIsAsync := e.isAsync
	savedCoroHdl := e.coroHdl
	savedPromiseTy := e.currentPromiseTy
	savedCoroRetLabel := e.coroRetLabel

	// Reset for this function.
	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.scopes = nil
	e.blockDone = false
	e.isAsync = decl.IsAsync
	e.coroHdl = ""
	e.currentPromiseTy = TypeVoid
	e.coroRetLabel = ""
	e.pushScope()

	// registerFunctions already resolved this (explicit annotation, or a
	// best-effort inference from the function's own first return statement
	// when unannotated — see inferUnannotatedReturnType) before any function
	// body was emitted; reuse it rather than recomputing, so this function's
	// own emitted signature always matches what every caller already
	// expects it to be.
	retType := e.funcs[decl.Name].RetType
	if retType.IsDynamic || containsDynamicElement(retType) {
		return fmt.Errorf("%d:%d: any/unknown is not yet supported as a function return type", decl.GetPos().Line, decl.GetPos().Col)
	}

	// For async functions, the IR return type is always ptr (the coro handle).
	// The logical return type (Promise<T>) is stored; T is tracked in currentPromiseTy.
	if decl.IsAsync {
		if retType.IsPromise && retType.PromiseType != nil {
			e.currentPromiseTy = *retType.PromiseType
		}
		// IR return type is ptr (the coroutine handle).
		e.currentRetType = TypePtr
		e.coroRetLabel = e.freshLabel("coro.ret")
		// Emit the coroutine prologue into e.allocas (entry block).
		e.emitAsyncPrologue()
	} else {
		e.currentRetType = retType
	}

	// Build LLVM parameter list and alloca each parameter.
	// Array parameters expand to two LLVM params: (ptr, i64 length).
	// Object and scalar parameters are each one ptr/scalar LLVM param.
	var llvmParams []string
	for _, p := range decl.Params {
		pty := TypeI64
		if p.Type != nil {
			pty = e.resolveType(p.Type)
		} else if p.Rest {
			pty = ArrayOf(TypeI64)
		}
		if pty.IsDynamic || containsDynamicElement(pty) {
			return fmt.Errorf("%d:%d: any/unknown is not yet supported as a function parameter type", decl.GetPos().Line, decl.GetPos().Col)
		}
		if pty.IsArray {
			llvmParams = append(llvmParams,
				fmt.Sprintf("ptr %%p_%s_ptr", p.Name),
				fmt.Sprintf("i64 %%p_%s_len", p.Name),
			)
			ptrAlloca := "%v_" + p.Name + "_ptr"
			lenAlloca := "%v_" + p.Name + "_len"
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrAlloca))
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenAlloca))
			e.emitInstr(fmt.Sprintf("store ptr %%p_%s_ptr, ptr %s, align 8", p.Name, ptrAlloca))
			e.emitInstr(fmt.Sprintf("store i64 %%p_%s_len, ptr %s, align 8", p.Name, lenAlloca))
			e.define(p.Name, Symbol{Ptr: ptrAlloca, LenPtr: lenAlloca, Ty: pty})
		} else {
			llvmParams = append(llvmParams, fmt.Sprintf("%s %%p_%s", pty.IR, p.Name))
			ptrName := "%v_" + p.Name
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", ptrName, pty.IR, pty.Align()))
			e.emitInstr(fmt.Sprintf("store %s %%p_%s, ptr %s, align %d", pty.IR, p.Name, ptrName, pty.Align()))
			e.define(p.Name, Symbol{Ptr: ptrName, Ty: pty})
		}
	}

	// Emit body statements.
	for _, stmt := range decl.Body.Body {
		if err := e.emitStmt(stmt); err != nil {
			return err
		}
	}

	// Add implicit terminator and assemble the function IR.
	if decl.IsAsync {
		// Async: emit coro.ret block (includes the implicit br + coro.end + ret ptr).
		e.emitAsyncEpilogue()
		e.functions.WriteString(fmt.Sprintf("\ndefine ptr @%s(%s) {\nentry:\n",
			decl.Name, strings.Join(llvmParams, ", ")))
	} else {
		// Non-async: void → ret void; non-void → unreachable fallthrough.
		if retType.IR == "void" {
			e.emitTerminator("ret void")
		} else {
			e.emitTerminator("unreachable")
		}
		e.functions.WriteString(fmt.Sprintf("\ndefine %s @%s(%s) {\nentry:\n",
			retType.LLVMRetType(), decl.Name, strings.Join(llvmParams, ", ")))
	}
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	// Restore saved context.
	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.labelCtr = savedLabelCtr
	e.scopes = savedScopes
	e.currentRetType = savedRetType
	e.isAsync = savedIsAsync
	e.coroHdl = savedCoroHdl
	e.currentPromiseTy = savedPromiseTy
	e.coroRetLabel = savedCoroRetLabel
	e.blockDone = false // main body starts unterminated

	return nil
}

// =============================================================================
// Closure / arrow-function support
// =============================================================================

// CapturedVar describes one variable captured from an enclosing scope.
type CapturedVar struct {
	Name string
	Ty   Type
	Sym  Symbol // the symbol as it exists in the enclosing scope
}

// envStructIR returns the LLVM struct type string for the closure environment.
// Every slot holds a pointer to a shared heap cell (see emitArrowFunctionWithHints),
// regardless of the captured variable's own type.
func envStructIR(caps []CapturedVar) string {
	parts := make([]string, len(caps))
	for i := range caps {
		parts[i] = "ptr"
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// envStructSize returns the byte size of the closure environment: one pointer per capture.
func envStructSize(caps []CapturedVar) int64 {
	return int64(len(caps)) * 8
}

// --- free-variable scanning ---

func scanExprFV(expr ast.Expression, bound map[string]bool, result map[string]bool) {
	if expr == nil {
		return
	}
	switch x := expr.(type) {
	case *ast.Identifier:
		if !bound[x.Name] {
			result[x.Name] = true
		}
	case *ast.BinaryExpression:
		scanExprFV(x.Left, bound, result)
		scanExprFV(x.Right, bound, result)
	case *ast.UnaryExpression:
		scanExprFV(x.Arg, bound, result)
	case *ast.UpdateExpression:
		scanExprFV(x.Arg, bound, result)
	case *ast.AssignmentExpression:
		scanExprFV(x.Left, bound, result)
		scanExprFV(x.Right, bound, result)
	case *ast.CallExpression:
		scanExprFV(x.Callee, bound, result)
		for _, a := range x.Args {
			scanExprFV(a, bound, result)
		}
	case *ast.MemberExpression:
		scanExprFV(x.Object, bound, result) // Property is a string, not a var ref
	case *ast.IndexExpression:
		scanExprFV(x.Object, bound, result)
		scanExprFV(x.Index, bound, result)
	case *ast.ArrayLiteral:
		for _, e := range x.Elements {
			scanExprFV(e, bound, result)
		}
	case *ast.ObjectLiteral:
		for _, p := range x.Properties {
			scanExprFV(p.Value, bound, result)
		}
	case *ast.SpreadElement:
		scanExprFV(x.Arg, bound, result)
	case *ast.NewArrayExpression:
		scanExprFV(x.Size, bound, result)
	case *ast.TemplateLiteral:
		for _, ex := range x.Exprs {
			scanExprFV(ex, bound, result)
		}
	case *ast.ConditionalExpression:
		scanExprFV(x.Test, bound, result)
		scanExprFV(x.Consequent, bound, result)
		scanExprFV(x.Alternate, bound, result)
	case *ast.ArrowFunction:
		// Nested arrow function: its params are bound within its own body.
		innerBound := make(map[string]bool, len(bound)+len(x.Params))
		for k, v := range bound {
			innerBound[k] = v
		}
		for _, p := range x.Params {
			innerBound[p.Name] = true
		}
		if x.Body != nil {
			scanExprFV(x.Body, innerBound, result)
		}
		if x.Block != nil {
			scanStmtsFV(x.Block.Body, innerBound, result)
		}
	// NumberLiteral, StringLiteral, BooleanLiteral: no identifiers
	}
}

func scanStmtsFV(stmts []ast.Statement, bound map[string]bool, result map[string]bool) {
	// Copy bound so local declarations don't bleed back to the caller.
	local := make(map[string]bool, len(bound))
	for k, v := range bound {
		local[k] = v
	}
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.VarDeclaration:
			if s.Init != nil {
				scanExprFV(s.Init, local, result)
			}
			local[s.Name] = true
		case *ast.ExpressionStatement:
			scanExprFV(s.Expr, local, result)
		case *ast.ReturnStatement:
			if s.Value != nil {
				scanExprFV(s.Value, local, result)
			}
		case *ast.IfStatement:
			scanExprFV(s.Test, local, result)
			if s.Consequent != nil {
				scanStmtsFV(s.Consequent.Body, local, result)
			}
			if s.Alternate != nil {
				scanStmtsFV([]ast.Statement{s.Alternate}, local, result)
			}
		case *ast.ForStatement:
			inner := make(map[string]bool, len(local))
			for k, v := range local {
				inner[k] = v
			}
			if s.Init != nil {
				if vd, ok := s.Init.(*ast.VarDeclaration); ok {
					if vd.Init != nil {
						scanExprFV(vd.Init, inner, result)
					}
					inner[vd.Name] = true
				} else if es, ok := s.Init.(*ast.ExpressionStatement); ok {
					scanExprFV(es.Expr, inner, result)
				}
			}
			if s.Test != nil {
				scanExprFV(s.Test, inner, result)
			}
			if s.Update != nil {
				scanExprFV(s.Update, inner, result)
			}
			if s.Body != nil {
				scanStmtsFV(s.Body.Body, inner, result)
			}
		case *ast.WhileStatement:
			scanExprFV(s.Test, local, result)
			if s.Body != nil {
				scanStmtsFV(s.Body.Body, local, result)
			}
		case *ast.BlockStatement:
			scanStmtsFV(s.Body, local, result)
		case *ast.ForOfStatement:
			scanExprFV(s.Iterable, local, result)
			inner := make(map[string]bool, len(local)+1)
			for k, v := range local {
				inner[k] = v
			}
			inner[s.VarName] = true
			if s.Body != nil {
				scanStmtsFV(s.Body.Body, inner, result)
			}
		case *ast.SwitchStatement:
			scanExprFV(s.Discriminant, local, result)
			for _, c := range s.Cases {
				if c.Test != nil {
					scanExprFV(c.Test, local, result)
				}
				scanStmtsFV(c.Body, local, result)
			}
		case *ast.BreakStatement:
			// no identifier references
		case *ast.ContinueStatement:
			// no identifier references
		case *ast.ArrayDestructuring:
			scanExprFV(s.Init, local, result)
			for _, name := range s.Names {
				if name != "" {
					local[name] = true
				}
			}
		case *ast.ObjectDestructuring:
			scanExprFV(s.Init, local, result)
			for _, prop := range s.Props {
				local[prop.Local] = true
			}
		}
	}
}

// gatherCaptures returns the variables from the enclosing scope that the arrow
// function's body references (sorted for deterministic output). Array variables
// cannot be captured yet (would require two env slots).
func (e *Emitter) gatherCaptures(af *ast.ArrowFunction) ([]CapturedVar, error) {
	// Build the initial bound set from the arrow function's own params.
	bound := make(map[string]bool, len(af.Params))
	for _, p := range af.Params {
		bound[p.Name] = true
	}
	// Collect all identifier names referenced in the body.
	refs := make(map[string]bool)
	if af.Body != nil {
		scanExprFV(af.Body, bound, refs)
	}
	if af.Block != nil {
		scanStmtsFV(af.Block.Body, bound, refs)
	}

	var caps []CapturedVar
	for name := range refs {
		sym, found := e.lookup(name)
		if !found {
			continue // built-in, function name, etc.
		}
		if sym.Ty.IsArray {
			return nil, fmt.Errorf("capturing array variable '%s' in a closure is not yet supported", name)
		}
		caps = append(caps, CapturedVar{Name: name, Ty: sym.Ty, Sym: sym})
	}
	// Sort for deterministic LLVM output.
	for i := 0; i < len(caps); i++ {
		for j := i + 1; j < len(caps); j++ {
			if caps[i].Name > caps[j].Name {
				caps[i], caps[j] = caps[j], caps[i]
			}
		}
	}
	return caps, nil
}

// --- closure function emission ---

// emitClosureFunc emits the named LLVM function for an arrow function into
// e.functions. The function takes ptr %env as its first parameter, followed by
// the arrow function's regular parameters. Captured variables are accessed via
// GEP into %env.
func (e *Emitter) emitClosureFunc(af *ast.ArrowFunction, caps []CapturedVar, retTy Type, paramTypes []Type, closureName string) error {
	// Save emitter state.
	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedScopes := e.scopes
	savedRetType := e.currentRetType
	savedBlockDone := e.blockDone

	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.scopes = nil
	e.blockDone = false
	e.currentRetType = retTy
	e.pushScope()

	// Build the LLVM parameter list string and alloca+store each regular param.
	paramStr := "ptr %env"
	for i, p := range af.Params {
		pty := paramTypes[i]
		paramStr += fmt.Sprintf(", %s %%p_%s", pty.IR, p.Name)
		ptrName := "%v_" + p.Name
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", ptrName, pty.IR, pty.Align()))
		e.emitInstr(fmt.Sprintf("store %s %%p_%s, ptr %s, align %d", pty.IR, p.Name, ptrName, pty.Align()))
		e.define(p.Name, Symbol{Ptr: ptrName, Ty: pty})
	}

	// Set up captured-variable access: each env slot holds a pointer to a heap
	// cell shared with the enclosing scope (and any other closure capturing the
	// same variable), so load that pointer once and use it directly as storage.
	// These go in the body builder (not allocas) but come before body statements.
	if len(caps) > 0 {
		ir := envStructIR(caps)
		for i, cap := range caps {
			slotGep := fmt.Sprintf("%%vcapslot_%s", cap.Name)
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%env, i32 0, i32 %d", slotGep, ir, i))
			cellPtr := fmt.Sprintf("%%vcap_%s", cap.Name)
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cellPtr, slotGep))
			e.define(cap.Name, Symbol{Ptr: cellPtr, Ty: cap.Ty, Boxed: true})
		}
	}

	// Emit the body.
	if af.Block != nil {
		for _, stmt := range af.Block.Body {
			if err := e.emitStmt(stmt); err != nil {
				return err
			}
		}
		if retTy.IR == "void" {
			e.emitTerminator("ret void")
		} else {
			e.emitTerminator("unreachable")
		}
	} else if af.Body != nil {
		val, err := e.emitExpr(af.Body)
		if err != nil {
			return err
		}
		if retTy.IR == "void" {
			e.emitTerminator("ret void")
		} else {
			val = e.coerce(val, retTy)
			e.emitTerminator(fmt.Sprintf("ret %s %s", val.Ty.IR, val.Ref))
		}
	}

	// Write the function into e.functions.
	e.functions.WriteString(fmt.Sprintf("\ndefine %s %s(%s) {\nentry:\n",
		retTy.LLVMRetType(), closureName, paramStr))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	// Restore state.
	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.labelCtr = savedLabelCtr
	e.scopes = savedScopes
	e.currentRetType = savedRetType
	e.blockDone = savedBlockDone
	return nil
}

// emitArrowFunction creates the closure struct {funcPtr, envPtr} on the heap
// and returns a Value pointing to it.
func (e *Emitter) emitArrowFunction(af *ast.ArrowFunction) (Value, error) {
	return e.emitArrowFunctionWithHints(af, nil)
}

// blockHasReturn reports whether a return statement is reachable anywhere in
// the block, recursing into nested control-flow bodies but not into nested
// function/arrow literals (which have their own, independently-inferred return type).
func blockHasReturn(block *ast.BlockStatement) bool {
	if block == nil {
		return false
	}
	for _, stmt := range block.Body {
		if stmtHasReturn(stmt) {
			return true
		}
	}
	return false
}

func stmtHasReturn(stmt ast.Statement) bool {
	switch s := stmt.(type) {
	case *ast.ReturnStatement:
		return true
	case *ast.BlockStatement:
		return blockHasReturn(s)
	case *ast.IfStatement:
		if blockHasReturn(s.Consequent) {
			return true
		}
		return s.Alternate != nil && stmtHasReturn(s.Alternate)
	case *ast.ForStatement:
		return blockHasReturn(s.Body)
	case *ast.ForOfStatement:
		return blockHasReturn(s.Body)
	case *ast.ForInStatement:
		return blockHasReturn(s.Body)
	case *ast.WhileStatement:
		return blockHasReturn(s.Body)
	case *ast.DoWhileStatement:
		return blockHasReturn(s.Body)
	case *ast.SwitchStatement:
		for _, c := range s.Cases {
			for _, cs := range c.Body {
				if stmtHasReturn(cs) {
					return true
				}
			}
		}
		return false
	case *ast.TryStatement:
		if blockHasReturn(s.Body) {
			return true
		}
		if s.Catch != nil && blockHasReturn(s.Catch.Body) {
			return true
		}
		return s.Finally != nil && blockHasReturn(s.Finally)
	default:
		return false
	}
}

// firstReturnExprInBlock finds the first reachable return statement's value
// expression in the block (same recursion shape as blockHasReturn/
// stmtHasReturn — nested control-flow bodies, not nested function/arrow
// literals), skipping bare `return;` statements (nothing to infer from) in
// favor of a later one that has a value. Used to give an unannotated
// function/arrow function a real return type instead of defaulting to
// void/i64 regardless of what it actually returns.
func firstReturnExprInBlock(block *ast.BlockStatement) ast.Expression {
	if block == nil {
		return nil
	}
	for _, stmt := range block.Body {
		if e := firstReturnExprInStmt(stmt); e != nil {
			return e
		}
	}
	return nil
}

func firstReturnExprInStmt(stmt ast.Statement) ast.Expression {
	switch s := stmt.(type) {
	case *ast.ReturnStatement:
		return s.Value
	case *ast.BlockStatement:
		return firstReturnExprInBlock(s)
	case *ast.IfStatement:
		if e := firstReturnExprInBlock(s.Consequent); e != nil {
			return e
		}
		if s.Alternate != nil {
			return firstReturnExprInStmt(s.Alternate)
		}
	case *ast.ForStatement:
		return firstReturnExprInBlock(s.Body)
	case *ast.ForOfStatement:
		return firstReturnExprInBlock(s.Body)
	case *ast.ForInStatement:
		return firstReturnExprInBlock(s.Body)
	case *ast.WhileStatement:
		return firstReturnExprInBlock(s.Body)
	case *ast.DoWhileStatement:
		return firstReturnExprInBlock(s.Body)
	case *ast.SwitchStatement:
		for _, c := range s.Cases {
			for _, cs := range c.Body {
				if e := firstReturnExprInStmt(cs); e != nil {
					return e
				}
			}
		}
	case *ast.TryStatement:
		if e := firstReturnExprInBlock(s.Body); e != nil {
			return e
		}
		if s.Catch != nil {
			if e := firstReturnExprInBlock(s.Catch.Body); e != nil {
				return e
			}
		}
		if s.Finally != nil {
			return firstReturnExprInBlock(s.Finally)
		}
	}
	return nil
}

// inferUnannotatedReturnType is the shared best-effort inference used by both
// registerFunctions (top-level function declarations) and
// emitArrowFunctionWithHints/inferExprType's *ast.ArrowFunction case
// (block-bodied arrow functions) when no explicit return-type annotation is
// present: push the function's own parameters into a temporary scope
// (inferExprType and its helpers never emit IR or mint registers, so this is
// safe to call before the real function body exists), then infer the first
// reachable return statement's expression type and use it as-is — including
// plain scalars, not just object/array/closure/Date. Returning ok=false (no
// reachable return has a value at all) leaves the caller's own default
// (void, or a scalar placeholder) untouched. A function with multiple
// returns of different shapes still only considers the first one; not
// attempted here — this compiler has no general union-type support beyond
// `T | null` (see CLAUDE.md), so a function that legitimately returns
// different types on different paths was never a designed-for case.
func (e *Emitter) inferUnannotatedReturnType(block *ast.BlockStatement, paramNames []string, paramTypes []Type) (Type, bool) {
	retExpr := firstReturnExprInBlock(block)
	if retExpr == nil {
		return Type{}, false
	}
	e.pushScope()
	for i, name := range paramNames {
		e.define(name, Symbol{Ty: paramTypes[i]})
	}
	inferred := e.inferExprType(retExpr)
	e.popScope()
	return inferred, true
}

// emitArrowFunctionWithHints is like emitArrowFunction but fills in types for
// parameters that have no annotation, using hints[i] when available. This lets
// HOF callers propagate the element type into untyped lambda parameters.
func (e *Emitter) emitArrowFunctionWithHints(af *ast.ArrowFunction, hints []Type) (Value, error) {
	caps, err := e.gatherCaptures(af)
	if err != nil {
		return Value{}, err
	}

	// Resolve param types: use hint when no annotation is present.
	paramTypes := make([]Type, len(af.Params))
	for i, p := range af.Params {
		if p.Type == nil && i < len(hints) {
			paramTypes[i] = hints[i]
		} else {
			paramTypes[i] = e.resolveType(p.Type)
		}
		if paramTypes[i].IsDynamic || containsDynamicElement(paramTypes[i]) {
			return Value{}, fmt.Errorf("%d:%d: any/unknown is not yet supported as a function parameter type", af.GetPos().Line, af.GetPos().Col)
		}
	}
	var retTy Type
	if af.RetType != nil {
		retTy = e.resolveType(af.RetType)
		if retTy.IsDynamic || containsDynamicElement(retTy) {
			return Value{}, fmt.Errorf("%d:%d: any/unknown is not yet supported as a function return type", af.GetPos().Line, af.GetPos().Col)
		}
	} else if af.Body != nil {
		// Temporarily push params into scope so inferExprType can resolve them.
		e.pushScope()
		for i, p := range af.Params {
			e.define(p.Name, Symbol{Ptr: fmt.Sprintf("%%__hint_%d", i), Ty: paramTypes[i]})
		}
		retTy = e.inferExprType(af.Body)
		e.popScope()
	} else if blockHasReturn(af.Block) {
		paramNames := make([]string, len(af.Params))
		for i, p := range af.Params {
			paramNames[i] = p.Name
		}
		if inferred, ok := e.inferUnannotatedReturnType(af.Block, paramNames, paramTypes); ok {
			retTy = inferred
		} else {
			retTy = TypeI64 // block body: scalar default, caller may override via annotation
		}
	} else {
		retTy = TypeVoid // block body with no reachable return (e.g. forEach callback)
	}

	// Emit the LLVM function for this closure.
	closureName := fmt.Sprintf("@__closure_%d", e.closureCtr)
	e.closureCtr++
	if err := e.emitClosureFunc(af, caps, retTy, paramTypes, closureName); err != nil {
		return Value{}, err
	}

	// Allocate the 16-byte closure header {ptr funcPtr, ptr envPtr}.
	e.ensureMalloc()
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))

	// Store function pointer into header[0].
	fpSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpSlot, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", closureName, fpSlot))

	// Allocate and populate the environment (if there are captures).
	epSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", epSlot, hdr))
	if len(caps) > 0 {
		envSize := envStructSize(caps)
		envIR := envStructIR(caps)
		env := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", env, envSize))
		for i, cap := range caps {
			cellPtr := cap.Sym.Ptr
			if !cap.Sym.Boxed {
				// First closure to capture this variable: promote it to a heap
				// cell shared by pointer with the enclosing scope and every
				// closure that captures it (instead of copying its value).
				newCell := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", newCell, cap.Ty.Align()))
				curVal := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d",
					curVal, cap.Ty.IR, cap.Sym.Ptr, cap.Ty.Align()))
				e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d",
					cap.Ty.IR, curVal, newCell, cap.Ty.Align()))
				e.updateSymbolInPlace(cap.Name, Symbol{Ptr: newCell, Ty: cap.Ty, Boxed: true})
				cellPtr = newCell
			}
			slotReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d",
				slotReg, envIR, env, i))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cellPtr, slotReg))
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", env, epSlot))
	} else {
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", epSlot))
	}

	closureTy := FuncType(paramTypes, retTy)
	return Value{Ref: hdr, Ty: closureTy}, nil
}

// --- closure call paths ---

// emitClosureCall calls a closure whose header pointer is stored in sym.Ptr.
func (e *Emitter) emitClosureCall(sym Symbol, args []ast.Expression, pos ast.Pos) (Value, error) {
	closureReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", closureReg, sym.Ptr))
	return e.emitClosureCallByPtr(closureReg, sym.Ty, args, pos)
}

// emitClosureCallByPtr calls a closure given the direct header pointer and its type.
func (e *Emitter) emitClosureCallByPtr(closurePtr string, ty Type, args []ast.Expression, pos ast.Pos) (Value, error) {
	// Load function pointer from header[0].
	fpSlot := e.freshReg()
	fpVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpSlot, closurePtr))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fpVal, fpSlot))

	// Load env pointer from header[1].
	epSlot := e.freshReg()
	epVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", epSlot, closurePtr))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", epVal, epSlot))

	// Build arg list: env first, then actual args.
	argParts := []string{"ptr " + epVal}
	for i, arg := range args {
		val, err := e.emitExpr(arg)
		if err != nil {
			return Value{}, err
		}
		if i < len(ty.FuncParams) {
			val = e.coerce(val, ty.FuncParams[i])
		}
		argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
	}

	// Build the LLVM function type string for the indirect call.
	// Format: retTy (ptr, argTy1, argTy2, ...)
	paramTyStrs := []string{"ptr"}
	for _, p := range ty.FuncParams {
		paramTyStrs = append(paramTyStrs, p.IR)
	}
	fnTypePart := "(" + strings.Join(paramTyStrs, ", ") + ")"

	retTy := ty.FuncRetType
	if retTy == nil || retTy.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void %s %s(%s)", fnTypePart, fpVal, strings.Join(argParts, ", ")))
		return Value{Ty: TypeVoid}, nil
	}

	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s %s %s(%s)", result, retTy.LLVMRetType(), fnTypePart, fpVal, strings.Join(argParts, ", ")))
	return Value{Ref: result, Ty: *retTy}, nil
}

// =============================================================================
// Callback helpers (used by HOF emission in emit_arrays.go)
// =============================================================================

// cbKind discriminates how to call a callback value.
type cbKind int

const (
	cbClosure cbKind = iota // closure header {funcPtr, envPtr} on heap
	cbNamed                 // top-level named function, called directly
)

// Callback holds everything needed to emit a callback invocation.
type Callback struct {
	kind   cbKind
	hdrPtr string  // register holding {ptr,ptr} closure header (cbClosure)
	ty     Type    // FuncType (cbClosure)
	name   string  // bare function name without @ (cbNamed)
	sig    FuncSig // (cbNamed)
}

func (cb Callback) paramTypes() []Type {
	if cb.kind == cbClosure {
		return cb.ty.FuncParams
	}
	return cb.sig.ParamTypes
}

func (cb Callback) retType() Type {
	if cb.kind == cbClosure {
		if cb.ty.FuncRetType != nil {
			return *cb.ty.FuncRetType
		}
		return TypeVoid
	}
	return cb.sig.RetType
}

func (cb Callback) arity() int { return len(cb.paramTypes()) }

// resolveCallback evaluates a callback argument (arrow function, closure var, or
// named function identifier) and returns a Callback descriptor.
func (e *Emitter) resolveCallback(arg ast.Expression) (Callback, error) {
	switch cb := arg.(type) {
	case *ast.ArrowFunction:
		v, err := e.emitArrowFunction(cb)
		if err != nil {
			return Callback{}, err
		}
		return Callback{kind: cbClosure, hdrPtr: v.Ref, ty: v.Ty}, nil
	case *ast.Identifier:
		if sig, found := e.funcs[cb.Name]; found {
			return Callback{kind: cbNamed, name: cb.Name, sig: sig}, nil
		}
		if sym, found := e.lookup(cb.Name); found && sym.Ty.IsFunc {
			hdr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hdr, sym.Ptr))
			return Callback{kind: cbClosure, hdrPtr: hdr, ty: sym.Ty}, nil
		}
		return Callback{}, fmt.Errorf("'%s' is not a callable", cb.Name)
	}
	return Callback{}, fmt.Errorf("callback must be an arrow function or function identifier")
}

// resolveCallbackWithHints is like resolveCallback but propagates element-type
// hints to untyped arrow function parameters.
func (e *Emitter) resolveCallbackWithHints(arg ast.Expression, hints []Type) (Callback, error) {
	if af, ok := arg.(*ast.ArrowFunction); ok {
		v, err := e.emitArrowFunctionWithHints(af, hints)
		if err != nil {
			return Callback{}, err
		}
		return Callback{kind: cbClosure, hdrPtr: v.Ref, ty: v.Ty}, nil
	}
	return e.resolveCallback(arg)
}

// emitCBCall invokes callback cb with the given pre-evaluated arguments.
// Values in args are coerced to the callback's declared param types.
func (e *Emitter) emitCBCall(cb Callback, args []Value) (Value, error) {
	params := cb.paramTypes()
	retTy := cb.retType()

	// Coerce args to declared param types.
	coerced := make([]Value, len(args))
	for i, a := range args {
		if i < len(params) {
			coerced[i] = e.coerce(a, params[i])
		} else {
			coerced[i] = a
		}
	}

	switch cb.kind {
	case cbClosure:
		fpSlot := e.freshReg()
		fpVal := e.freshReg()
		epSlot := e.freshReg()
		epVal := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpSlot, cb.hdrPtr))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fpVal, fpSlot))
		e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", epSlot, cb.hdrPtr))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", epVal, epSlot))

		tyParts := []string{"ptr"}
		for _, p := range params {
			tyParts = append(tyParts, p.IR)
		}
		fnType := "(" + strings.Join(tyParts, ", ") + ")"

		argParts := []string{"ptr " + epVal}
		for i, v := range coerced {
			ty := v.Ty.IR
			if i < len(params) {
				ty = params[i].IR
			}
			argParts = append(argParts, ty+" "+v.Ref)
		}
		argStr := strings.Join(argParts, ", ")

		if retTy.IR == "void" || retTy.IR == "" {
			e.emitInstr(fmt.Sprintf("call void %s %s(%s)", fnType, fpVal, argStr))
			return Value{Ty: TypeVoid}, nil
		}
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call %s %s %s(%s)", result, retTy.LLVMRetType(), fnType, fpVal, argStr))
		return Value{Ref: result, Ty: retTy}, nil

	case cbNamed:
		argParts := make([]string, len(coerced))
		for i, v := range coerced {
			ty := v.Ty.IR
			if i < len(params) {
				ty = params[i].IR
			}
			argParts[i] = ty + " " + v.Ref
		}
		argStr := strings.Join(argParts, ", ")
		if retTy.IR == "void" {
			e.emitInstr(fmt.Sprintf("call void @%s(%s)", cb.name, argStr))
			return Value{Ty: TypeVoid}, nil
		}
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call %s @%s(%s)", result, retTy.LLVMRetType(), cb.name, argStr))
		return Value{Ref: result, Ty: retTy}, nil
	}
	return Value{}, fmt.Errorf("unknown callback kind")
}
