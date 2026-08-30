// emit_func_adapt.go — closure adapter trampolines (ADR-00477/ADR-00478).
//
// A closure compiled against a concrete signature stored into a function-
// typed slot whose signature differs in *dynamism* (`(n: number) => any`
// holding a `(n: number) => string` implementation) used to be passed
// through as a raw header-pointer copy: the call site then dispatched by
// the slot's declared type and reinterpreted the concrete return word as a
// { i8, i64 } dynamic box (or vice versa) — silent garbage, found as
// `console.log(g(1))` printing "[object Object]" for a string return
// (ADR-00475's deferred bug).
//
// The fix: at coercion time, when the two function types disagree on
// return/parameter dynamism, wrap the closure in a generated *adapter*
// function whose LLVM signature is exactly what callers of the target type
// emit, whose env is the original closure header, and whose body converts
// each argument (unbox a dynamic argument for a concrete parameter, box a
// concrete argument for a dynamic parameter), makes the indirect call with
// the source type's signature, and converts the return the same way.
//
// Coverage (ADR-00478 completed the aggregate shapes):
//   - scalars/strings/pointers ↔ any, both directions, params and returns;
//   - arrays: forwarded through their split (ptr, len) ABI, boxed into a
//     dynamic slot via the { ptr, i64 } array header, and unboxed back out
//     of one (the header representation preserves the length);
//   - nullable scalars ({ i1, T }): boxed via emitBoxValue's existing
//     nullable path, unboxed via the null/undefined-tag → absent rule;
//   - arity narrowing: extra target arguments are dropped (the standard JS
//     ignore-extra-callback-args pattern);
//   - rest slots: forwarded when both sides carry a same-element rest tail;
//     a source-only rest receives the empty (null, 0) tail; a target-only
//     rest tail is dropped.
package llvm

import (
	"fmt"
	"strings"
)

// adapterConvertible reports whether a dynamism-mismatched pair at one
// position is a conversion the adapter implements.
func adapterConvertible(concrete Type) bool {
	if concrete.IsArray || isNullableScalar(concrete) {
		return true // header boxing / { i1, T } wrapping (ADR-00478)
	}
	switch concrete.IR {
	case "i64", "double", "i1", "ptr", "i8", "i16", "i32":
		return true
	}
	return false
}

// restOf splits a function type into its fixed parameters and (optional)
// rest slot type.
func restOf(t Type) (fixed []Type, rest *Type) {
	if t.FuncHasRest && len(t.FuncParams) > 0 {
		return t.FuncParams[:len(t.FuncParams)-1], &t.FuncParams[len(t.FuncParams)-1]
	}
	return t.FuncParams, nil
}

func adapterRetOf(t Type) Type {
	if t.FuncRetType == nil {
		return TypeVoid
	}
	return *t.FuncRetType
}

// funcAdapterPlan reports whether coercing a closure of type src into a
// slot of type tgt needs an adapter (some position differs in dynamism, or
// the rest tails differ), and whether every needed conversion is one the
// adapter supports. When needed && !supported the caller keeps the old
// passthrough behavior — a disclosed narrowing, never worse than before.
func funcAdapterPlan(src, tgt Type) (needed, supported bool) {
	if !src.IsFunc || !tgt.IsFunc {
		return false, false
	}
	sFixed, sRest := restOf(src)
	tFixed, tRest := restOf(tgt)
	if len(sFixed) > len(tFixed) {
		return false, false
	}
	supported = true
	for i := range sFixed {
		sp, tp := sFixed[i], tFixed[i]
		if sp.IsDynamic != tp.IsDynamic {
			needed = true
			concrete := sp
			if sp.IsDynamic {
				concrete = tp
			}
			if !adapterConvertible(concrete) {
				supported = false
			}
		} else if storageIR(sp) != storageIR(tp) || sp.IsArray != tp.IsArray {
			// A concrete-vs-concrete shape mismatch isn't this adapter's job
			// (and shouldn't type-check upstream anyway).
			supported = false
		}
	}
	// Rest tails: forwardable only when both sides agree on the element
	// shape; a one-sided rest is droppable/fillable (see the header comment).
	if sRest != nil && tRest != nil {
		se, te := TypeF64, TypeF64
		if sRest.ElemType != nil {
			se = *sRest.ElemType
		}
		if tRest.ElemType != nil {
			te = *tRest.ElemType
		}
		if storageIR(se) != storageIR(te) || se.IsDynamic != te.IsDynamic {
			supported = false
		}
	}
	if sRest != nil && tRest == nil {
		needed = true // source rest must be filled with the empty tail
	}
	if tRest != nil && sRest == nil {
		// Target rest tail dropped — only an adapter can do the dropping,
		// but arity alone doesn't trigger one; if something else does, the
		// drop rides along for free.
		_ = tRest
	}

	sr, tr := adapterRetOf(src), adapterRetOf(tgt)
	srVoid, trVoid := sr.IR == "void", tr.IR == "void"
	switch {
	case trVoid:
		// Result dropped or absent — no conversion either way.
	case srVoid && tr.IsDynamic:
		needed = true // void → any: return a boxed undefined
	case srVoid:
		supported = false // void → concrete has no value to produce
	case sr.IsDynamic != tr.IsDynamic:
		needed = true
		concrete := sr
		if sr.IsDynamic {
			concrete = tr
		}
		if !adapterConvertible(concrete) {
			supported = false
		}
	case storageIR(sr) != storageIR(tr) || sr.IsArray != tr.IsArray:
		supported = false
	}
	return needed, supported
}

// adapterSigParams appends the LLVM parameter declarations for ty at
// position i (arrays split into ptr+len; a rest slot is its (ptr, len)
// buffer) and returns the value-reference strings for reading them back.
func adapterSigParams(sig *[]string, ty Type, i int, isRest bool) (refs []string) {
	if ty.IsArray || isRest {
		p := fmt.Sprintf("%%a%d_p", i)
		l := fmt.Sprintf("%%a%d_l", i)
		*sig = append(*sig, "ptr "+p, "i64 "+l)
		return []string{p, l}
	}
	r := fmt.Sprintf("%%a%d", i)
	*sig = append(*sig, fmt.Sprintf("%s %s", storageIR(ty), r))
	return []string{r}
}

// emitClosureAdapter builds the adapter function and a fresh closure header
// {adapter, originalHeader}, returning the adapted Value typed as tgt.
// ok=false (nothing emitted into the caller's body) when a conversion turns
// out unsupported mid-build — the caller falls back to passthrough.
func (e *Emitter) emitClosureAdapter(orig Value, tgt Type) (Value, bool) {
	src := orig.Ty
	e.closureAdaptCtr++
	name := fmt.Sprintf("@__kml_fnadapt_%d", e.closureAdaptCtr)

	sFixed, sRest := restOf(src)
	tFixed, tRest := restOf(tgt)

	restore := e.beginThunkEmit()

	// Unpack the original closure header (our env).
	fpp, fp := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0", fpp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpp))
	epp, ep := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1", epp))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epp))

	// Adapter signature (the target type's caller ABI) + converted call
	// arguments (the source type's callee ABI).
	sigParts := []string{"ptr %env"}
	argParts := []string{"ptr " + ep}
	buildOK := true
	for i, tp := range tFixed {
		refs := adapterSigParams(&sigParts, tp, i, false)
		if i >= len(sFixed) {
			continue // extra target argument: dropped
		}
		sp := sFixed[i]
		switch {
		case sp.IsDynamic == tp.IsDynamic:
			if sp.IsArray {
				argParts = append(argParts, "ptr "+refs[0], "i64 "+refs[1])
			} else {
				argParts = append(argParts, fmt.Sprintf("%s %s", storageIR(sp), refs[0]))
			}
		case tp.IsDynamic: // dynamic argument → concrete parameter: unbox
			v := e.emitUnboxBoxToType(refs[0], sp)
			if sp.IsArray {
				// The unboxed array is a {ptr,i64} aggregate; the target's array
				// param expects a header pointer (TDD-00127).
				header, lReg := e.arrayArgFromAggregate(v)
				argParts = append(argParts, "ptr "+header, "i64 "+lReg)
			} else {
				argParts = append(argParts, fmt.Sprintf("%s %s", storageIR(sp), v.Ref))
			}
		default: // concrete argument → dynamic parameter: box
			var concrete Value
			if tp.IsArray {
				// refs[0] is the incoming header pointer; a boxed array carries
				// the {data,len} aggregate, so read data out of the header first
				// (TDD-00127).
				dataR, agg0, agg1 := e.freshReg(), e.freshReg(), e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataR, refs[0]))
				e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", agg0, dataR))
				e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", agg1, agg0, refs[1]))
				concrete = Value{Ref: agg1, Ty: tp}
			} else {
				concrete = Value{Ref: refs[0], Ty: tp}
			}
			b, err := e.emitBoxValue(concrete)
			if err != nil {
				buildOK = false
			} else {
				argParts = append(argParts, fmt.Sprintf("%s %s", storageIR(sp), b.Ref))
			}
		}
	}
	// Rest tails.
	if tRest != nil {
		refs := adapterSigParams(&sigParts, *tRest, len(tFixed), true)
		if sRest != nil {
			argParts = append(argParts, "ptr "+refs[0], "i64 "+refs[1])
		}
		// else: target rest tail dropped.
	} else if sRest != nil {
		argParts = append(argParts, "ptr "+e.emptyArrayArgHeader(), "i64 0")
	}

	// Indirect call typed by the source signature.
	srcParamTys := []string{"ptr"}
	for _, sp := range sFixed {
		if sp.IsArray {
			srcParamTys = append(srcParamTys, "ptr", "i64")
			continue
		}
		srcParamTys = append(srcParamTys, storageIR(sp))
	}
	if sRest != nil {
		srcParamTys = append(srcParamTys, "ptr", "i64")
	}
	fnTypePart := "(" + strings.Join(srcParamTys, ", ") + ")"

	sr, tr := adapterRetOf(src), adapterRetOf(tgt)
	if buildOK {
		switch {
		case tr.IR == "void":
			if sr.IR == "void" {
				e.emitInstr(fmt.Sprintf("call void %s %s(%s)", fnTypePart, fp, strings.Join(argParts, ", ")))
			} else {
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call %s %s %s(%s)", r, sr.LLVMRetType(), fnTypePart, fp, strings.Join(argParts, ", ")))
			}
			e.emitInstr("ret void")
		case sr.IR == "void" && tr.IsDynamic:
			e.emitInstr(fmt.Sprintf("call void %s %s(%s)", fnTypePart, fp, strings.Join(argParts, ", ")))
			b, err := e.emitBoxValue(Value{Ty: TypeUndefined})
			if err != nil {
				buildOK = false
			} else {
				e.emitInstr(fmt.Sprintf("ret %s %s", tr.LLVMRetType(), b.Ref))
			}
		case sr.IsDynamic == tr.IsDynamic:
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call %s %s %s(%s)", r, sr.LLVMRetType(), fnTypePart, fp, strings.Join(argParts, ", ")))
			e.emitInstr(fmt.Sprintf("ret %s %s", tr.LLVMRetType(), r))
		case tr.IsDynamic: // concrete return → dynamic slot: box
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call %s %s %s(%s)", r, sr.LLVMRetType(), fnTypePart, fp, strings.Join(argParts, ", ")))
			b, err := e.emitBoxValue(Value{Ref: r, Ty: sr})
			if err != nil {
				buildOK = false
			} else {
				e.emitInstr(fmt.Sprintf("ret %s %s", tr.LLVMRetType(), b.Ref))
			}
		default: // dynamic return → concrete slot: unbox
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call %s %s %s(%s)", r, sr.LLVMRetType(), fnTypePart, fp, strings.Join(argParts, ", ")))
			v := e.emitUnboxBoxToType(r, tr)
			e.emitInstr(fmt.Sprintf("ret %s %s", tr.LLVMRetType(), v.Ref))
		}
	}

	body := e.allocas.String() + e.body.String()
	restore()
	if !buildOK {
		return Value{}, false
	}

	e.functions.WriteString(fmt.Sprintf("\ndefine %s %s(%s) {\nentry:\n%s}\n",
		tr.LLVMRetType(), name, strings.Join(sigParts, ", "), body))

	e.ensureMalloc()
	hdr := e.buildBuiltinClosure(name, orig.Ref)
	return Value{Ref: hdr, Ty: tgt}, true
}
