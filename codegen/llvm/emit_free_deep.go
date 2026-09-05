package llvm

// emit_free_deep.go — TDD-00175 Stage 1: type-directed deep free for
// transitively-owned bindings under `-mm=auto`. Where the shallow free
// (emit_memory.go) releases only a binding's top-level allocation, this
// synthesizes a per-type recursive free routine — the same on-demand,
// type-directed walk shape the JSON projection/stringify walkers use — and
// the block-exit drain calls it instead of freeSymbol for bindings the
// analysis proved *transitively* owned.
//
// Scope (deliberately narrow): trees a typed JSON.parse / Response.json
// projection produced — objects/tuples of scalar, string, nested-object and
// inline-{ptr,i64} array fields; arrays of those (nested arrays are boxed
// element pointers). Every string in such a tree is a heap dup
// (__kml_json_string_dup; absent-field defaults heap-dup under auto too —
// see jsonDefaultRef), every struct and buffer a fresh malloc — so the graph
// is fully owned by construction. Types that can't come out of JSON
// (class/map/set/closure/dynamic) are gated out defensively.
//
// Eligibility is a downgrade lattice, never a widening: a binding that fails
// the interior-alias check (escape_check.go) keeps today's shallow free; a
// binding that fails the shallow check gets no free at all. Rejection can
// only reproduce the pre-existing leak, never a new unsoundness.

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// deepFreeEligible reports whether ty is a tree the synthesized deep free
// can walk: scalars and nullable scalars (nothing owned), heap strings,
// objects/tuples whose every field is eligible, and arrays of eligible
// elements. Anything else — class instances, dynamic values, maps/sets,
// closures, promises, flat @value arrays, typed arrays — is out.
func deepFreeEligible(ty Type) bool {
	switch {
	case ty.IsClass, ty.IsDynamic, ty.IsDynamicObject, ty.IsMap, ty.IsSet,
		ty.IsFunc, ty.IsPromise, ty.Inline, ty.IsFlatArray:
		return false
	case ty.IsArray:
		return ty.ElemType != nil && deepFreeEligible(*ty.ElemType)
	case ty.IsObject:
		if len(ty.Fields) == 0 {
			return false
		}
		for _, f := range ty.Fields {
			if !deepFreeEligible(f.Ty) {
				return false
			}
		}
		return true
	case isStringTy(ty):
		return true
	case ty.IR == "ptr":
		// Some other pointer-shaped builtin (Date is i64; Response, Blob,
		// streams, buffers are ptr) — not a JSON-tree shape.
		return false
	default:
		return true // scalar / nullable-scalar leaf: nothing owned, nothing to do
	}
}

// deepFreeOwns reports whether a value of ty owns anything *beyond* what the
// shallow free already releases — false means deep == shallow and the deep
// flag would be pure overhead.
func deepFreeOwns(ty Type) bool {
	switch {
	case ty.IsArray:
		return ty.ElemType != nil && deepFreeElemOwned(*ty.ElemType)
	case ty.IsObject:
		for _, f := range ty.Fields {
			if deepFreeElemOwned(f.Ty) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// deepFreeElemOwned: does a field/element of this type carry an owned heap
// allocation of its own (string bytes, an object struct, an array buffer)?
func deepFreeElemOwned(ty Type) bool {
	return isStringTy(ty) || ty.IsObject || ty.IsArray
}

// deepFreeKey builds the memoization key for a synthesized routine — a
// canonical structural signature, so two structurally-identical types share
// one function regardless of interface names.
func deepFreeKey(ty Type) string {
	switch {
	case ty.IsArray:
		return "a(" + deepFreeKey(*ty.ElemType) + ")"
	case ty.IsObject:
		var b strings.Builder
		b.WriteString("o(")
		for i, f := range ty.Fields {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(deepFreeKey(f.Ty))
		}
		b.WriteString(")")
		return b.String()
	case isStringTy(ty):
		return "s"
	default:
		return "z" // scalar leaf: never recursed into, one bucket suffices
	}
}

// deepFreeSym reports whether the auto layer should (and can) deep-free this
// binding: the interior-alias analysis approved it, its type is an eligible
// tree that owns more than its top level, and its initializer produced a
// fully-owned graph (a typed JSON.parse / Response.json projection).
func (e *Emitter) deepFreeSym(v *ast.VarDeclaration, ty Type) bool {
	if !e.autoFreeDeep[v] || v.Free || v.Owned {
		return false
	}
	if !deepFreeEligible(ty) || !deepFreeOwns(ty) {
		return false
	}
	return deepFreeProjectionInit(v.Init)
}

// deepFreeProjectionInit recognizes the initializer shapes whose emission is
// a typed projection producing a transitively-fresh graph: `JSON.parse(...)`
// and `res.json()`, optionally awaited. (The receiver's Response-ness isn't
// re-checked here: a non-Response `.json()` init would have failed the
// non-dynamic type gates before this is consulted.) Literal initializers are
// deliberately NOT accepted: an array/object literal can embed interned
// string literals and aliases of other bindings — per-store freshness for
// literals is a documented follow-up, not Stage 1.
func deepFreeProjectionInit(init ast.Expression) bool {
	if aw, ok := init.(*ast.AwaitExpression); ok {
		init = aw.Argument
	}
	ce, ok := init.(*ast.CallExpression)
	if !ok {
		return false
	}
	mem, ok := ce.Callee.(*ast.MemberExpression)
	if !ok {
		return false
	}
	if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "JSON" && mem.Property == "parse" {
		return true
	}
	return mem.Property == "json"
}

// freePending frees one registered obligation — the deep routine for flagged
// bindings, freeSymbol otherwise. The single dispatch point all four drain
// sites (block exit, return, break/continue, owned-last-use, rebind) share.
func (e *Emitter) freePending(pf pendingFree) error {
	if pf.deep {
		return e.freeSymbolDeep(pf.sym)
	}
	return e.freeSymbol(pf.sym, pf.pos)
}

// freeSymbolDeep is freeSymbol's deep counterpart: frees the binding's whole
// owned graph via the synthesized routine, then resets the binding's storage
// exactly as the shallow path does (empty array header / null pointer).
func (e *Emitter) freeSymbolDeep(sym Symbol) error {
	if sym.Ty.IsArray {
		fn, err := e.ensureDeepFreeArr(*sym.Ty.ElemType)
		if err != nil {
			return err
		}
		dataSlot, lenSlot := e.arrayDataLenSlots(sym)
		d := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", d, dataSlot))
		l := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", l, lenSlot))
		e.emitInstr(fmt.Sprintf("call void @%s(ptr %s, i64 %s)", fn, d, l))
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", dataSlot))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", lenSlot))
		return nil
	}
	fn, err := e.ensureDeepFree(sym.Ty)
	if err != nil {
		return err
	}
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", p, sym.Ptr))
	e.emitInstr(fmt.Sprintf("call void @%s(ptr %s)", fn, p))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", sym.Ptr))
	return nil
}

// ensureDeepFree synthesizes (once per structural type signature) the
// recursive free for an object/tuple type: `void @__kml_deep_free_N(ptr)` —
// null-check, free each owned field (strings, nested objects, inline
// {ptr,i64} array fields), then the struct itself. Same save/restore builder
// discipline as emitChanCloneThunk; memoized before body emission so the
// (structurally impossible, but cheap to guard) recursive self-reference
// terminates.
func (e *Emitter) ensureDeepFree(ty Type) (string, error) {
	key := deepFreeKey(ty)
	if name, ok := e.deepFreeFns[key]; ok {
		return name, nil
	}
	if e.deepFreeFns == nil {
		e.deepFreeFns = map[string]string{}
	}
	e.deepFreeCtr++
	name := fmt.Sprintf("__kml_deep_free_%d", e.deepFreeCtr)
	e.deepFreeFns[key] = name

	savedAllocas, savedBody, savedRegCtr, savedBlockDone := e.allocas, e.body, e.regCtr, e.blockDone
	e.allocas, e.body, e.regCtr, e.blockDone = strings.Builder{}, strings.Builder{}, 0, false

	var synthErr error
	doneL := e.freshLabel("dfree.done")
	bodyL := e.freshLabel("dfree.body")
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %%p, null", isNull))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, doneL, bodyL))
	e.emitLabel(bodyL)
	structIR := ty.StructIR()
	for i, f := range ty.Fields {
		if !deepFreeElemOwned(f.Ty) {
			continue
		}
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%p, i32 0, i32 %d", gep, structIR, i))
		if f.Ty.IsArray {
			// Inline {ptr,i64} slot (ADR-00061): pull the buffer and length
			// out, then hand off to the element walker.
			dGep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, i64}, ptr %s, i32 0, i32 0", dGep, gep))
			d := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", d, dGep))
			lGep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, i64}, ptr %s, i32 0, i32 1", lGep, gep))
			l := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", l, lGep))
			fn, err := e.ensureDeepFreeArr(*f.Ty.ElemType)
			if err != nil {
				synthErr = err
				break
			}
			e.emitInstr(fmt.Sprintf("call void @%s(ptr %s, i64 %s)", fn, d, l))
			continue
		}
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", p, gep))
		if f.Ty.IsObject {
			fn, err := e.ensureDeepFree(f.Ty)
			if err != nil {
				synthErr = err
				break
			}
			e.emitInstr(fmt.Sprintf("call void @%s(ptr %s)", fn, p))
			continue
		}
		// String field: __kml_str_free does not null-check internally, and a
		// nullable string field legitimately holds null.
		e.emitDeepFreeString(p)
	}
	if synthErr == nil {
		e.emitDeepFreeDeathSignal("%p")
		e.ensureFree()
		e.emitInstr("call void @free(ptr %p)")
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		e.emitTerminator("ret void")
		e.functions.WriteString(fmt.Sprintf("\ndefine void @%s(ptr %%p) {\nentry:\n", name))
		e.functions.WriteString(e.allocas.String())
		e.functions.WriteString(e.body.String())
		e.functions.WriteString("}\n")
	}
	e.allocas, e.body, e.regCtr, e.blockDone = savedAllocas, savedBody, savedRegCtr, savedBlockDone
	if synthErr != nil {
		delete(e.deepFreeFns, key)
		return "", synthErr
	}
	return name, nil
}

// ensureDeepFreeArr synthesizes the element-walking free for `elemTy[]`:
// `void @__kml_deep_free_arr_N(ptr data, i64 len)` — null-check the buffer,
// free each owned element (strings, object structs, boxed nested arrays —
// each slot is one 8-byte pointer), then the buffer itself.
func (e *Emitter) ensureDeepFreeArr(elemTy Type) (string, error) {
	key := "a(" + deepFreeKey(elemTy) + ")"
	if name, ok := e.deepFreeFns[key]; ok {
		return name, nil
	}
	if e.deepFreeFns == nil {
		e.deepFreeFns = map[string]string{}
	}
	e.deepFreeCtr++
	name := fmt.Sprintf("__kml_deep_free_arr_%d", e.deepFreeCtr)
	e.deepFreeFns[key] = name

	savedAllocas, savedBody, savedRegCtr, savedBlockDone := e.allocas, e.body, e.regCtr, e.blockDone
	e.allocas, e.body, e.regCtr, e.blockDone = strings.Builder{}, strings.Builder{}, 0, false

	var synthErr error
	doneL := e.freshLabel("dfarr.done")
	bodyL := e.freshLabel("dfarr.body")
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %%d, null", isNull))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, doneL, bodyL))
	e.emitLabel(bodyL)

	if deepFreeElemOwned(elemTy) {
		idxAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))
		condL := e.freshLabel("dfarr.cond")
		loopL := e.freshLabel("dfarr.loop")
		endL := e.freshLabel("dfarr.end")
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(condL)
		i0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i0, idxAlloca))
		fin := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %%n", fin, i0))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", fin, loopL, endL))
		e.emitLabel(loopL)
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %%d, i64 %s", slot, i0))
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", p, slot))
		switch {
		case elemTy.IsObject:
			fn, err := e.ensureDeepFree(elemTy)
			if err != nil {
				synthErr = err
			} else {
				e.emitInstr(fmt.Sprintf("call void @%s(ptr %s)", fn, p))
			}
		case elemTy.IsArray:
			// Boxed nested-array element (ADR-00061): the slot holds a
			// pointer to a malloc'd {ptr,i64} box; walk the inner buffer,
			// then free the box.
			fn, err := e.ensureDeepFreeArr(*elemTy.ElemType)
			if err != nil {
				synthErr = err
			} else {
				skipL := e.freshLabel("dfarr.boxskip")
				unboxL := e.freshLabel("dfarr.unbox")
				bn := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", bn, p))
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bn, skipL, unboxL))
				e.emitLabel(unboxL)
				dGep := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, i64}, ptr %s, i32 0, i32 0", dGep, p))
				id := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", id, dGep))
				lGep := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, i64}, ptr %s, i32 0, i32 1", lGep, p))
				il := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", il, lGep))
				e.emitInstr(fmt.Sprintf("call void @%s(ptr %s, i64 %s)", fn, id, il))
				e.ensureFree()
				e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", p))
				e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
				e.emitLabel(skipL)
			}
		default: // string
			e.emitDeepFreeString(p)
		}
		if synthErr == nil {
			i1 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", i1, i0))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", i1, idxAlloca))
			e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
			e.emitLabel(endL)
		}
	}
	if synthErr == nil {
		e.ensureFree()
		e.emitInstr("call void @free(ptr %d)")
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		e.emitTerminator("ret void")
		e.functions.WriteString(fmt.Sprintf("\ndefine void @%s(ptr %%d, i64 %%n) {\nentry:\n", name))
		e.functions.WriteString(e.allocas.String())
		e.functions.WriteString(e.body.String())
		e.functions.WriteString("}\n")
	}
	e.allocas, e.body, e.regCtr, e.blockDone = savedAllocas, savedBody, savedRegCtr, savedBlockDone
	if synthErr != nil {
		delete(e.deepFreeFns, key)
		return "", synthErr
	}
	return name, nil
}

// emitDeepFreeString null-checks and frees one heap string pointer, with the
// FinalizationRegistry death signal freeResolvedPointer would fire.
func (e *Emitter) emitDeepFreeString(p string) {
	skipL := e.freshLabel("dfree.sskip")
	freeL := e.freshLabel("dfree.sfree")
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, p))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, skipL, freeL))
	e.emitLabel(freeL)
	e.emitDeepFreeDeathSignal(p)
	e.emitStringFree(p)
	e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
	e.emitLabel(skipL)
}

// emitDeepFreeDeathSignal mirrors freeResolvedPointer's FinalizationRegistry
// hook (TDD-00163 Stage 2) for every pointer the deep walk frees.
func (e *Emitter) emitDeepFreeDeathSignal(ptrRef string) {
	if e.programUsesFinReg && !e.isGCMode() {
		e.ensureFinalizationHelpers()
		e.emitInstr(fmt.Sprintf("call void @__kml_finreg_onfree(ptr %s)", ptrRef))
	}
}
