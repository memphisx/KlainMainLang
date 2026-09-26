// infer_memo.go — inferExprType memoises member/call nodes for the duration of
// one outermost inference.
//
// inferExprType decides a member call's type by asking "what is the receiver?"
// once per builtin family it knows (dozens of `e.inferExprType(mem.Object)`
// probes per call node). Each probe re-infers the whole receiver, so a chain
// costs probes^depth: a five-link `.then` chain took ~52 s to compile, one link
// more would not finish. Inference emits no code and reads only the scope
// stack, so within one outermost call a node's type is a pure function of the
// scopes in force — which is exactly what the memo is keyed on:
//
//   - it lives only while an inference is running (cleared when the outermost
//     call returns), so nothing emission does in between — declarations,
//     flow narrowing, a function body's fresh scope stack — can be observed
//     stale;
//   - inference itself pushes a scope to type a callback body with its
//     parameters bound (callbackReturnType, the arrow case): pushScope opens a
//     fresh layer, popScope drops it, define clears the current one;
//   - a layer remembers the scope stack it was filled under (depth + identity
//     of the function frame) and is ignored under any other, which covers the
//     helpers that swap e.scopes wholesale.
package llvm

import (
	"reflect"

	"KlainMainLang/ast"
)

type inferMemoLayer struct {
	depth int     // len(e.scopes) the layer was opened under
	base  uintptr // identity of e.scopes[0].syms (0 when there is no scope)
	types map[ast.Expression]Type
}

func (e *Emitter) inferScopeIdentity() (int, uintptr) {
	if len(e.scopes) == 0 {
		return 0, 0
	}
	return len(e.scopes), reflect.ValueOf(e.scopes[0].syms).Pointer()
}

func (e *Emitter) inferMemoOpenLayer() {
	d, b := e.inferScopeIdentity()
	e.inferMemo = append(e.inferMemo, inferMemoLayer{depth: d, base: b, types: map[ast.Expression]Type{}})
}

// inferMemoTop returns the current layer when it was filled under the scope
// stack now in force, nil otherwise.
func (e *Emitter) inferMemoTop() map[ast.Expression]Type {
	if e.inferDepth == 0 || len(e.inferMemo) == 0 {
		return nil
	}
	top := e.inferMemo[len(e.inferMemo)-1]
	if d, b := e.inferScopeIdentity(); d != top.depth || b != top.base {
		return nil
	}
	return top.types
}

// inferMemoScopePushed / Popped / Defined are called by pushScope, popScope and
// define; they do nothing outside a running inference.
func (e *Emitter) inferMemoScopePushed() {
	if e.inferDepth > 0 {
		e.inferMemoOpenLayer()
	}
}

func (e *Emitter) inferMemoScopePopped() {
	if e.inferDepth > 0 && len(e.inferMemo) > 1 {
		e.inferMemo = e.inferMemo[:len(e.inferMemo)-1]
	}
}

func (e *Emitter) inferMemoDefined() {
	if e.inferDepth > 0 && len(e.inferMemo) > 0 {
		e.inferMemo[len(e.inferMemo)-1].types = map[ast.Expression]Type{}
	}
}

// inferExprType: compile-time type of expr, no codegen.
func (e *Emitter) inferExprType(expr ast.Expression) Type {
	ty := e.inferExprTypeMemo(expr)
	// A union-typed property read the checker narrows (`typeof o.v ===
	// "object" ? o.v.slice() : …`) reads as that member (mirrors emitMember).
	if m, ok := expr.(*ast.MemberExpression); ok && ty.IsDynamic && len(ty.UnionMembers) > 0 {
		if nt, ok := e.checkerNarrowedUnion(m, ty); ok {
			ty = nt
		}
	}
	if e.shadowOracle != nil {
		e.shadowExprType(expr, ty)
	}
	return ty
}

func (e *Emitter) inferExprTypeMemo(expr ast.Expression) Type {
	// Only the nodes that carry the receiver recursion are memoised.
	switch expr.(type) {
	case *ast.CallExpression, *ast.MemberExpression:
	default:
		return e.inferExprTypeUncached(expr)
	}
	if e.inferDepth == 0 {
		e.inferMemo = e.inferMemo[:0]
		e.inferDepth++
		e.inferMemoOpenLayer()
		ty := e.inferExprTypeUncached(expr)
		e.inferDepth--
		e.inferMemo = e.inferMemo[:0]
		return ty
	}
	if m := e.inferMemoTop(); m != nil {
		if ty, ok := m[expr]; ok {
			return ty
		}
	}
	e.inferDepth++
	ty := e.inferExprTypeUncached(expr)
	e.inferDepth--
	if m := e.inferMemoTop(); m != nil {
		m[expr] = ty
	}
	return ty
}
