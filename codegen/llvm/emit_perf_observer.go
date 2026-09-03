package llvm

// emit_perf_observer.go — perf_hooks PerformanceObserver (TDD-00166). A small
// subsystem: a `new PerformanceObserver(cb)` handle, `.observe`/`.disconnect`
// methods, and a process-global observer registry that performance.mark/measure
// walk to fire matching callbacks. V1 dispatch is SYNCHRONOUS (during the
// mark/measure call), one entry per invocation — see the TDD for the async
// follow-up. Entry types: mark=1, measure=2 (a bitmask).

import (
	"fmt"

	"KlainMainLang/ast"
)

const (
	perfMaskMark    = 1
	perfMaskMeasure = 2
)

// storeObjField / loadObjField are small field-access helpers for the fixed-shape
// handle/entry/list objects this file builds.
func (e *Emitter) storeObjField(ty Type, objReg, name, ref string) {
	idx, fieldTy, _ := ty.FieldIndex(name)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, ty.StructIR(), objReg, idx))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, ref, gep, fieldTy.Align()))
}

func (e *Emitter) loadObjField(ty Type, objReg, name string) string {
	idx, fieldTy, _ := ty.FieldIndex(name)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, ty.StructIR(), objReg, idx))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", r, fieldTy.IR, gep, fieldTy.Align()))
	return r
}

// ensurePerfObserverRegistry emits the global observer registry: a singly-linked
// list of nodes `{ ptr callback, i32 mask, ptr next }`, prepended to on observe.
// A disconnected observer keeps its node but with mask 0, so the walk skips it.
func (e *Emitter) ensurePerfObserverRegistry() {
	if e.usedPerfObsRegistry {
		return
	}
	e.usedPerfObsRegistry = true
	e.ensureMalloc()
	e.emitGlobal(`
@__kml_perfobs_head = internal global ptr null, align 8

define ptr @__kml_perfobs_add(ptr %cb, i32 %mask) {
entry:
  %n = call ptr @malloc(i64 24)
  %cbp = getelementptr { ptr, i32, ptr }, ptr %n, i32 0, i32 0
  store ptr %cb, ptr %cbp, align 8
  %mp = getelementptr { ptr, i32, ptr }, ptr %n, i32 0, i32 1
  store i32 %mask, ptr %mp, align 8
  %h = load ptr, ptr @__kml_perfobs_head, align 8
  %np = getelementptr { ptr, i32, ptr }, ptr %n, i32 0, i32 2
  store ptr %h, ptr %np, align 8
  store ptr %n, ptr @__kml_perfobs_head, align 8
  ret ptr %n
}

define void @__kml_perfobs_set_mask(ptr %node, i32 %mask) {
entry:
  %mp = getelementptr { ptr, i32, ptr }, ptr %node, i32 0, i32 1
  store i32 %mask, ptr %mp, align 8
  ret void
}
`)
}

// emitNewPerformanceObserver builds a `new PerformanceObserver(cb)` handle: the
// callback closure stored in __kml_cb, __kml_node initially null (set on observe).
func (e *Emitter) emitNewPerformanceObserver(ex *ast.NewExpression) (Value, error) {
	if len(ex.Args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: new PerformanceObserver(callback) takes exactly 1 argument", ex.GetPos().Line, ex.GetPos().Col)
	}
	e.ensureMalloc()
	e.ensurePerfObserverRegistry()
	// Contextually type the callback's parameter as the entry list, so its body's
	// `list.getEntries()` resolves (an untyped arrow param would otherwise default
	// to a number). Other callable forms fall back to a plain emit.
	hint := []Type{PerfEntryListType()}
	var cb Value
	var err error
	switch fn := ex.Args[0].(type) {
	case *ast.ArrowFunction:
		cb, err = e.emitArrowFunctionWithHints(fn, hint)
	case *ast.FunctionExpression:
		cb, err = e.emitFunctionExpression(fn, hint)
	default:
		cb, err = e.emitExpr(ex.Args[0])
	}
	if err != nil {
		return Value{}, err
	}
	if cb.Ty.IR != "ptr" {
		return Value{}, fmt.Errorf("%d:%d: new PerformanceObserver expects a callback function", ex.GetPos().Line, ex.GetPos().Col)
	}
	ty := PerfObserverType()
	obj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", obj, ty.StructSize()))
	e.storeObjField(ty, obj, "__kml_cb", cb.Ref)
	e.storeObjField(ty, obj, "__kml_node", "null")
	return Value{Ref: obj, Ty: ty}, nil
}

// emitPerfObserverMethod dispatches `.observe(...)` / `.disconnect()` on a
// PerformanceObserver handle.
func (e *Emitter) emitPerfObserverMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	ty := objVal.Ty
	switch method {
	case "observe":
		mask, err := e.perfEntryTypesMask(args, pos)
		if err != nil {
			return Value{}, err
		}
		cb := e.loadObjField(ty, objVal.Ref, "__kml_cb")
		node := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_perfobs_add(ptr %s, i32 %d)", node, cb, mask))
		e.storeObjField(ty, objVal.Ref, "__kml_node", node)
		return Value{Ty: TypeVoid}, nil
	case "disconnect":
		// Clear the node's mask so the mark/measure walk skips it. A never-observed
		// observer has a null node — guard so disconnect() before observe() is a no-op.
		node := e.loadObjField(ty, objVal.Ref, "__kml_node")
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, node))
		skipL := e.freshLabel("perfobs.disc.skip")
		doL := e.freshLabel("perfobs.disc.do")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, skipL, doL))
		e.emitLabel(doL)
		e.emitInstr(fmt.Sprintf("call void @__kml_perfobs_set_mask(ptr %s, i32 0)", node))
		e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
		e.emitLabel(skipL)
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: PerformanceObserver has no method '%s' (V1 supports observe/disconnect)", pos.Line, pos.Col, method)
}

// perfEntryTypesMask computes the entry-type bitmask from a compile-time
// `{ entryTypes: ['mark', 'measure'] }` argument. A non-literal shape is a clean
// V1 rejection.
func (e *Emitter) perfEntryTypesMask(args []ast.Expression, pos ast.Pos) (int, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("%d:%d: observe({ entryTypes: [...] }) takes one options object", pos.Line, pos.Col)
	}
	obj, ok := args[0].(*ast.ObjectLiteral)
	if !ok {
		return 0, fmt.Errorf("%d:%d: observe() expects a literal `{ entryTypes: [...] }` (V1)", pos.Line, pos.Col)
	}
	var arr *ast.ArrayLiteral
	for _, p := range obj.Properties {
		if p.Key == "entryTypes" {
			if a, ok := p.Value.(*ast.ArrayLiteral); ok {
				arr = a
			}
		}
	}
	if arr == nil {
		return 0, fmt.Errorf("%d:%d: observe() requires `entryTypes: [...]` with a literal array (V1; the single-`type` form is not yet supported)", pos.Line, pos.Col)
	}
	mask := 0
	for _, el := range arr.Elements {
		lit, ok := el.(*ast.StringLiteral)
		if !ok {
			return 0, fmt.Errorf("%d:%d: entryTypes entries must be string literals (V1)", pos.Line, pos.Col)
		}
		switch lit.Value {
		case "mark":
			mask |= perfMaskMark
		case "measure":
			mask |= perfMaskMeasure
		default:
			return 0, fmt.Errorf("%d:%d: PerformanceObserver V1 supports entry types 'mark' and 'measure', got '%s'", pos.Line, pos.Col, lit.Value)
		}
	}
	return mask, nil
}

// emitPerfEntryListGetEntries returns the list's entries as the `{ptr, i64}`
// array aggregate (the general array-as-expression representation).
func (e *Emitter) emitPerfEntryListGetEntries(objVal Value) (Value, error) {
	ty := objVal.Ty
	data := e.loadObjField(ty, objVal.Ref, "__kml_data")
	length := e.loadObjField(ty, objVal.Ref, "__kml_len")
	agg0 := e.freshReg()
	agg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", agg0, data))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", agg, agg0, length))
	return Value{Ref: agg, Ty: ArrayOf(PerformanceEntryType())}, nil
}

// emitPerfDispatch is called by performance.mark/measure: it builds a
// PerformanceEntry, wraps it in a one-entry list, and walks the observer registry
// firing every observer whose mask includes typeBit. Synchronous (V1).
func (e *Emitter) emitPerfDispatch(name, entryType string, startTime, duration string, typeBit int) {
	if !e.usedPerfObsRegistry {
		return // no PerformanceObserver was ever constructed → nothing observes
	}
	entryTy := PerformanceEntryType()
	entry := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", entry, entryTy.StructSize()))
	e.storeObjField(entryTy, entry, "name", name)
	e.storeObjField(entryTy, entry, "entryType", e.internString(entryType))
	e.storeObjField(entryTy, entry, "startTime", startTime)
	e.storeObjField(entryTy, entry, "duration", duration)

	// One-element entries buffer (a single object pointer).
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8)", data))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", entry, data))

	listTy := PerfEntryListType()
	list := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", list, listTy.StructSize()))
	e.storeObjField(listTy, list, "__kml_data", data)
	e.storeObjField(listTy, list, "__kml_len", "1")

	cbTy := FuncType([]Type{listTy}, TypeVoid)

	// Walk the registry with a mutable node pointer.
	nodePtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", nodePtr))
	head := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_perfobs_head, align 8", head))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", head, nodePtr))

	condL := e.freshLabel("perfdisp.cond")
	bodyL := e.freshLabel("perfdisp.body")
	callL := e.freshLabel("perfdisp.call")
	nextL := e.freshLabel("perfdisp.next")
	endL := e.freshLabel("perfdisp.end")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	node := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", node, nodePtr))
	atEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", atEnd, node))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", atEnd, endL, bodyL))

	e.emitLabel(bodyL)
	maskGep := e.freshReg()
	mask := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, i32, ptr }, ptr %s, i32 0, i32 1", maskGep, node))
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 8", mask, maskGep))
	anded := e.freshReg()
	matched := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i32 %s, %d", anded, mask, typeBit))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", matched, anded))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", matched, callL, nextL))

	e.emitLabel(callL)
	cbGep := e.freshReg()
	cb := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, i32, ptr }, ptr %s, i32 0, i32 0", cbGep, node))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cb, cbGep))
	e.emitClosureCallValues(cb, cbTy, []Value{{Ref: list, Ty: listTy}})
	e.emitTerminator(fmt.Sprintf("br label %%%s", nextL))

	e.emitLabel(nextL)
	// Re-load the node (the callback may not touch it, but re-load to be safe),
	// then advance to next.
	curNode := e.freshReg()
	nextGep := e.freshReg()
	nextNode := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curNode, nodePtr))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, i32, ptr }, ptr %s, i32 0, i32 2", nextGep, curNode))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", nextNode, nextGep))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", nextNode, nodePtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
}
