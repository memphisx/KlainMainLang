package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// Optional chains short-circuit as a whole: in `a?.b.c`, `a?.b.m(x)`,
// `a?.[k].d` a nullish `a` makes the ENTIRE chain `undefined` — the later
// `.c` / `.m(x)` / `.d` links are skipped too, not run against the absent
// value. The single-link emitters (emitOptionalMember / emitOptionalCall /
// emitOptionalIndex / emitOptionalCalleeCall) only cover a chain whose `?.` is
// its last link; a chain that continues past a `?.` is handled here, by
// guarding once at the leftmost `?.` and emitting everything to its right —
// the remaining chain, with that link made plain and its base bound to a
// temporary — inside the guarded branch. A further `?.` to the right is then
// the leftmost optional link of that remainder, so the scheme composes.

// chainChild returns the next node down an access chain (the receiver of a
// member/element access, the callee of a call, the operand of a `!`
// assertion), and whether n is itself an optional link.
func chainChild(n ast.Expression) (child ast.Expression, optional, ok bool) {
	switch x := n.(type) {
	case *ast.MemberExpression:
		return x.Object, x.Optional, true
	case *ast.IndexExpression:
		return x.Object, x.Optional, true
	case *ast.CallExpression:
		return x.Callee, x.Optional, true
	case *ast.NonNullExpression:
		return x.Arg, false, true
	}
	return nil, false, false
}

// chainEnded reports whether n was written in parentheses, which ends the
// optional chain it belongs to: `(a?.b).c` is not short-circuited.
func chainEnded(n ast.Expression) bool {
	switch x := n.(type) {
	case *ast.MemberExpression:
		return x.ChainEnd
	case *ast.IndexExpression:
		return x.ChainEnd
	case *ast.CallExpression:
		return x.ChainEnd
	}
	return false
}

// leftmostOptionalDepth returns how many links below top the chain's leftmost
// (innermost) `?.` sits — 0 when top itself is that link — or -1 when the chain
// has none.
func leftmostOptionalDepth(top ast.Expression) int {
	depth := -1
	cur := top
	for d := 0; ; d++ {
		child, optional, ok := chainChild(cur)
		if !ok {
			break
		}
		if optional {
			depth = d
		}
		if chainEnded(child) {
			break
		}
		cur = child
	}
	return depth
}

// optionalChainDepth is leftmostOptionalDepth restricted to the chains this
// file owns: -1 for a chain the single-link emitters already handle (the `?.`
// is the top link, or the top is an ordinary call on an optional member —
// `a?.m(...)`, emitOptionalCall).
func optionalChainDepth(top ast.Expression) int {
	depth := leftmostOptionalDepth(top)
	if depth <= 0 {
		return -1
	}
	if depth == 1 {
		if c, ok := top.(*ast.CallExpression); ok && !c.Optional {
			if m, ok := c.Callee.(*ast.MemberExpression); ok && m.Optional {
				return -1
			}
		}
	}
	return depth
}

// chainLinkAt returns the node depth links below top.
func chainLinkAt(top ast.Expression, depth int) ast.Expression {
	cur := top
	for ; depth > 0; depth-- {
		cur, _, _ = chainChild(cur)
	}
	return cur
}

// rewriteChain returns a copy of the chain's spine in which the link at depth
// is no longer optional and, when newBase is non-nil, reads from newBase. Only
// the spine is copied — the AST is shared with inferExprType and must not be
// mutated.
func rewriteChain(n ast.Expression, depth int, newBase ast.Expression) ast.Expression {
	switch x := n.(type) {
	case *ast.MemberExpression:
		c := *x
		if depth == 0 {
			c.Optional = false
			if newBase != nil {
				c.Object = newBase
			}
		} else {
			c.Object = rewriteChain(x.Object, depth-1, newBase)
		}
		return &c
	case *ast.IndexExpression:
		c := *x
		if depth == 0 {
			c.Optional = false
			if newBase != nil {
				c.Object = newBase
			}
		} else {
			c.Object = rewriteChain(x.Object, depth-1, newBase)
		}
		return &c
	case *ast.CallExpression:
		c := *x
		if depth == 0 {
			c.Optional = false
			if newBase != nil {
				c.Callee = newBase
			}
		} else {
			c.Callee = rewriteChain(x.Callee, depth-1, newBase)
		}
		return &c
	case *ast.NonNullExpression:
		return ast.NewNonNullExpression(rewriteChain(x.Arg, depth-1, newBase), x.GetPos())
	}
	return n
}

// optionalChainGuard says how the base of a chain's leftmost `?.` can be absent.
type optionalChainGuard int

const (
	chainGuardNone   optionalChainGuard = iota // never nullish: the chain is plain
	chainGuardPtr                              // a pointer: null check
	chainGuardScalar                           // a `{ i1, T }` nullable scalar: presence bit
	chainGuardAbsent                           // statically undefined on this host
)

// classifyChainGuard decides the guard for the optional link `link` from the
// static type of its base — shared by emit and inference. hostDependent is set
// for a built-in whose presence depends on the target (process.getuid?.()), whose
// chain is `T | undefined` on every host.
func (e *Emitter) classifyChainGuard(link ast.Expression) (guard optionalChainGuard, hostDependent bool) {
	base, _, _ := chainChild(link)
	if call, ok := link.(*ast.CallExpression); ok {
		switch e.classifyOptionalCallee(call.Callee) {
		case optCalleePresent:
			return chainGuardNone, false
		case optCalleeHostDependent:
			if e.opts.Target.OS() == "windows" {
				return chainGuardAbsent, true
			}
			return chainGuardNone, true
		}
	}
	bt := e.inferExprType(base)
	switch {
	case isNullableScalar(bt):
		return chainGuardScalar, false
	case bt.IR == "ptr" && !bt.IsArray:
		return chainGuardPtr, false
	}
	return chainGuardNone, false
}

// inferOptionalChain is emitOptionalChain's type mirror.
func (e *Emitter) inferOptionalChain(top ast.Expression) (Type, bool) {
	depth := optionalChainDepth(top)
	if depth < 0 {
		return Type{}, false
	}
	guard, hostDependent := e.classifyChainGuard(chainLinkAt(top, depth))
	inner := e.inferExprType(rewriteChain(top, depth, nil))
	if guard == chainGuardNone && !hostDependent {
		return inner, true
	}
	return optionalCallResultType(optCalleeValue, inner), true
}

// undefinedValueOf is the `undefined` of a short-circuited chain whose
// non-skipped type is inner.
func (e *Emitter) undefinedValueOf(inner Type) Value {
	resTy := optionalCallResultType(optCalleeValue, inner)
	switch {
	case inner.IR == "void" || inner.IR == "":
		return Value{Ty: TypeVoid}
	case inner.IsArray:
		return Value{Ref: "{ ptr null, i64 0 }", Ty: inner}
	case isNullableScalar(resTy):
		return Value{Ref: e.makeNullableScalarAgg(resTy, "false", zeroRef(inner)), Ty: resTy}
	}
	return Value{Ref: zeroRef(inner), Ty: resTy}
}

// emitOptionalChain emits a chain that continues past a `?.`; handled is false
// for every other expression.
func (e *Emitter) emitOptionalChain(top ast.Expression) (v Value, handled bool, err error) {
	depth := optionalChainDepth(top)
	if depth < 0 {
		return Value{}, false, nil
	}
	link := chainLinkAt(top, depth)
	guard, hostDependent := e.classifyChainGuard(link)
	pos := top.GetPos()

	switch guard {
	case chainGuardAbsent:
		return e.undefinedValueOf(e.inferExprType(rewriteChain(top, depth, nil))), true, nil
	case chainGuardNone:
		plain := rewriteChain(top, depth, nil)
		pv, perr := e.emitExpr(plain)
		if perr != nil || !hostDependent {
			return pv, true, perr
		}
		// Present on this host, but typed `T | undefined` on every host.
		if resTy := optionalCallResultType(optCalleeValue, e.inferExprType(plain)); isNullableScalar(resTy) && !isNullableScalar(pv.Ty) {
			return e.coerce(pv, resTy), true, nil
		}
		return pv, true, nil
	}

	base, _, _ := chainChild(link)
	bv, err := e.emitExpr(base)
	if err != nil {
		return Value{}, true, err
	}
	e.optionalCallCtr++
	tmpName := fmt.Sprintf("__optc_chain_%d", e.optionalCallCtr)
	tmpTy := bv.Ty.withoutNullable()
	var isNull string
	switch {
	case isNullableScalar(bv.Ty):
		present, payload := e.nullableScalarAggParts(bv)
		isNull = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", isNull, present))
		tmpTy = payload.Ty
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, payload.Ty.IR, payload.Ty.Align()))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", payload.Ty.IR, payload.Ref, slot, payload.Ty.Align()))
		e.define(tmpName, Symbol{Ptr: slot, Ty: tmpTy})
	case bv.Ty.IR == "ptr" && !bv.Ty.IsArray:
		isNull = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, bv.Ref))
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", bv.Ref, slot))
		e.define(tmpName, Symbol{Ptr: slot, Ty: tmpTy})
	default:
		// Inference promised a nullable base and emission produced a value that
		// cannot be absent: the base is already evaluated and cannot be bound
		// generically (an array is two allocas), so refuse rather than evaluate
		// it a second time.
		return Value{}, true, fmt.Errorf("%d:%d: optional chain on a value of type %s is not supported", pos.Line, pos.Col, bv.Ty.IR)
	}
	through := rewriteChain(top, depth, ast.NewIdentifier(tmpName, base.GetPos()))
	v, err = e.emitNullGuardedExpr(isNull, through)
	return v, true, err
}
