// emit_tuple_byval.go — value-returned small tuples (TDD-00134 Stage 3,
// -optimize-memory). A top-level function whose declared return type is a
// small tuple (≤2 plain scalar fields — register-passable on both x86-64
// SysV and arm64 AAPCS) returns the tuple's struct aggregate BY VALUE
// instead of a pointer to a heap-allocated struct. `return [a, b]` then
// builds the aggregate with insertvalue — zero allocation — and the
// idiomatic Go-style consumer `const [v, err] = f()` destructures it with
// extractvalue, also allocation-free. Any other consumer of the call result
// (binding the whole tuple, indexing, stringification) spills the aggregate
// back to a heap pointer at the call site, preserving today's semantics at
// today's allocation count.
//
// Planned per program by planTupleByValReturns: the flag lives on the
// FuncSig.RetType copy only, so the function definition, every direct call
// site, and the body's `return` lowering all read one authoritative ABI
// decision. A function referenced as a VALUE anywhere (assigned, passed as
// a callback, captured) keeps the pointer ABI — an indirect call through a
// function-type annotation knows nothing of the flag and would otherwise
// call with a mismatched signature.
package llvm

import (
	"fmt"
	"reflect"

	"KlainMainLang/ast"
)

// tupleByValEligible reports whether a return type is a small tuple whose
// aggregate can be returned in registers: 1–2 fields, each stored as its own
// plain scalar IR word (i64/double/ptr/i1/…). A field needing an aggregate
// slot (array {ptr,i64}, nullable-scalar {i1,T}) or runtime tag dispatch
// (any/unknown) disqualifies — those keep the heap-pointer ABI.
func tupleByValEligible(ty Type) bool {
	if !ty.IsTuple || len(ty.Fields) == 0 || len(ty.Fields) > 2 {
		return false
	}
	for _, f := range ty.Fields {
		ft := f.Ty
		if ft.IsDynamic || ft.IsDynamicObject || ft.Inline {
			return false
		}
		if StructFieldIR(ft) != ft.IR {
			return false // array / nullable-scalar aggregate slot
		}
	}
	return true
}

// planTupleByValReturns marks every eligible top-level function's registered
// FuncSig.RetType as TupleByVal. Must run after registerFunctions (needs
// e.funcs) and after classifyAsyncSuspension (MaySuspend excludes). The
// program-wide value-reference scan is deliberately over-broad on the
// disqualifying side — a name it cannot prove is only ever a direct callee
// keeps the pointer ABI, losing only the optimization, never soundness.
func (e *Emitter) planTupleByValReturns(prog *ast.Program) {
	if !e.optimizeMemory {
		return
	}
	candidates := map[string]bool{}
	for name, sig := range e.funcs {
		if sig.IsAsync || sig.MaySuspend {
			continue
		}
		if tupleByValEligible(sig.RetType) {
			candidates[name] = true
		}
	}
	if len(candidates) == 0 {
		return
	}
	valueRefs := collectFnValueRefs(prog, candidates)
	for name := range candidates {
		if valueRefs[name] {
			continue
		}
		sig := e.funcs[name]
		sig.RetType.TupleByVal = true
		e.funcs[name] = sig
	}
}

// collectFnValueRefs walks the whole program and reports which of the given
// function names appear anywhere OTHER than as a direct call's callee — i.e.
// are used as values. Reflection-based like collectFinRegNames/thisEscapesFn:
// the only positively-recognized safe shape is `name(...)` (the callee
// identifier is skipped, the arguments are descended into); any other
// *ast.Identifier bearing a candidate name marks it referenced.
func collectFnValueRefs(prog *ast.Program, names map[string]bool) map[string]bool {
	refs := map[string]bool{}
	var visit func(v reflect.Value)
	visit = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Ptr, reflect.Interface:
			if v.IsNil() {
				return
			}
			switch n := v.Interface().(type) {
			case *ast.CallExpression:
				if id, ok := n.Callee.(*ast.Identifier); ok && names[id.Name] {
					for _, a := range n.Args {
						visit(reflect.ValueOf(a))
					}
					return
				}
			case *ast.Identifier:
				if names[n.Name] {
					refs[n.Name] = true
				}
				return
			}
			visit(v.Elem())
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if f := v.Field(i); f.CanInterface() {
					visit(f)
				}
			}
		}
	}
	visit(reflect.ValueOf(prog))
	return refs
}

// emitTupleByValReturn lowers `return expr` in a TupleByVal function. A tuple
// literal builds the aggregate directly with insertvalue — the allocation-free
// path that is this optimization's point. Any other expression (a tuple
// variable, another call's spilled result) evaluates to a pointer and the
// aggregate is loaded from it.
func (e *Emitter) emitTupleByValReturn(r *ast.ReturnStatement, retTy Type) error {
	structIR := retTy.StructIR()
	var agg string
	if lit, ok := r.Value.(*ast.ArrayLiteral); ok && len(lit.Elements) == len(retTy.Fields) && !hasSpreadElem(lit.Elements) {
		agg = "undef"
		for i, elemExpr := range lit.Elements {
			fieldTy := retTy.Fields[i].Ty
			val, err := e.emitExprWithObjectHint(elemExpr, fieldTy)
			if err != nil {
				return err
			}
			val = e.coerce(val, fieldTy)
			next := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = insertvalue %s %s, %s %s, %d", next, structIR, agg, fieldTy.IR, val.Ref, i))
			agg = next
		}
	} else {
		val, err := e.emitExpr(r.Value)
		if err != nil {
			return err
		}
		if !val.Ty.IsTuple {
			return fmt.Errorf("%d:%d: expression is not a tuple", r.Value.GetPos().Line, r.Value.GetPos().Col)
		}
		loaded := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", loaded, structIR, val.Ref))
		agg = loaded
	}
	if err := e.emitReturnCleanups(); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("ret %s %s", structIR, agg))
	return nil
}

func hasSpreadElem(elems []ast.Expression) bool {
	for _, el := range elems {
		if _, ok := el.(*ast.SpreadElement); ok {
			return true
		}
	}
	return false
}

// spillTupleAggregate materializes a by-value tuple call result back into the
// heap-pointer shape every existing tuple consumer expects. Heap, not alloca:
// the resulting pointer may be bound and outlive the calling frame (stored,
// returned from a caller with the pointer ABI), exactly like today's
// callee-allocated tuple — so the allocation count matches the status quo on
// this path; only the direct-destructure path is allocation-free.
func (e *Emitter) spillTupleAggregate(aggReg string, tupleTy Type) Value {
	e.ensureMalloc()
	structIR := tupleTy.StructIR()
	heap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", heap, tupleTy.StructSize()))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", structIR, aggReg, heap))
	tupleTy.TupleByVal = false
	return Value{Ref: heap, Ty: tupleTy}
}

// unpackTupleAggregate binds an array-destructuring pattern positionally
// against a by-value tuple aggregate with extractvalue — no memory traffic at
// all. Eligible tuples carry only plain scalar fields, so nested sub-patterns
// cannot apply; rest stays the same clean rejection the pointer path gives.
func (e *Emitter) unpackTupleAggregate(aggReg string, tupleTy Type, elems []ast.ArrayPatternElem, pos ast.Pos) error {
	if len(elems) > len(tupleTy.Fields) {
		return fmt.Errorf("%d:%d: destructuring pattern has %d elements but the tuple has only %d", pos.Line, pos.Col, len(elems), len(tupleTy.Fields))
	}
	structIR := tupleTy.StructIR()
	for i, elem := range elems {
		if elem.Rest {
			return fmt.Errorf("%d:%d: a rest element in a tuple destructuring pattern is not yet supported", pos.Line, pos.Col)
		}
		if elem.SubArray != nil || elem.SubObject != nil {
			return fmt.Errorf("%d:%d: nested destructuring pattern does not match the tuple element's type", pos.Line, pos.Col)
		}
		if elem.Name == "" {
			continue // hole
		}
		fieldTy := tupleTy.Fields[i].Ty
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue %s %s, %d", reg, structIR, aggReg, i))
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, fieldTy.IR, fieldTy.Align()))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, reg, slot, fieldTy.Align()))
		e.define(elem.Name, Symbol{Ptr: slot, Ty: fieldTy})
	}
	return nil
}
