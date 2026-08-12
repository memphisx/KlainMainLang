package llvm

import "fmt"

// coerce inserts a type conversion instruction if necessary.
func (e *Emitter) coerce(v Value, target Type) Value {
	// null/undefined assigned to an array-typed target becomes an empty-
	// array aggregate ({ptr, i64}, not a bare `ptr` — see StructFieldIR's
	// own doc comment for why an array field's real storage type differs
	// from its own Type.IR) — checked before the plain non-ptr
	// null-to-zero-value branch below, since an IsArray target also
	// reports IR == "ptr" and would otherwise fall through untouched into
	// the unchanged-value case further down, producing an invalid `store
	// {ptr, i64} null, ...` (null is only a valid literal for a bare ptr,
	// not this aggregate shape) — a real bug found assigning a literal
	// `null` to a `T[] | null` interface field (ADR-00158).
	if v.Ty.IsNull && target.IsArray {
		return Value{Ref: "{ ptr null, i64 0 }", Ty: target}
	}
	// null/undefined assigned to a non-ptr type becomes the zero value.
	if v.Ty.IsNull && target.IR != "ptr" {
		return Value{Ref: zeroRef(target), Ty: target}
	}
	if v.Ty.IR == target.IR {
		return v
	}
	// Boxing a concrete scalar into a dynamic/union box ({ i8, i64 }): the
	// target is NOT a machine integer, but IsInteger() reports true for it
	// (any non-ptr/non-float/non-void IR, the aggregate box included), so
	// without this guard the int->int branch below would emit an invalid
	// `trunc i64 ... to { i8, i64 }`. Boxing is emitBoxValue's job, not a
	// cast; callers that must surface a boxing error do so before reaching
	// coerce (see emit_call.go / emitStaticMethodCall) — here it is a
	// best-effort backstop so no coerce call site can produce that bad IR.
	if target.IsDynamic && !v.Ty.IsDynamic {
		if boxed, err := e.emitBoxValue(v); err == nil {
			return boxed
		}
	}
	reg := e.freshReg()

	srcInt := v.Ty.IsInteger()
	dstInt := target.IsInteger()

	switch {
	// int → int (same size handled above, so either widen or narrow)
	case srcInt && dstInt:
		srcBits := typeBits(v.Ty.IR)
		dstBits := typeBits(target.IR)
		if dstBits > srcBits {
			ext := "sext"
			if !v.Ty.Signed {
				ext = "zext"
			}
			e.emitInstr(fmt.Sprintf("%s = %s %s %s to %s", reg, ext, v.Ty.IR, v.Ref, target.IR))
		} else {
			e.emitInstr(fmt.Sprintf("%s = trunc %s %s to %s", reg, v.Ty.IR, v.Ref, target.IR))
		}

	// int → float
	case srcInt && target.Float:
		op := "sitofp"
		if !v.Ty.Signed {
			op = "uitofp"
		}
		e.emitInstr(fmt.Sprintf("%s = %s %s %s to %s", reg, op, v.Ty.IR, v.Ref, target.IR))

	// float → int
	case v.Ty.Float && dstInt:
		op := "fptosi"
		if !target.Signed {
			op = "fptoui"
		}
		e.emitInstr(fmt.Sprintf("%s = %s %s %s to %s", reg, op, v.Ty.IR, v.Ref, target.IR))

	// float → float
	case v.Ty.Float && target.Float:
		srcBits := typeBits(v.Ty.IR)
		dstBits := typeBits(target.IR)
		if dstBits > srcBits {
			e.emitInstr(fmt.Sprintf("%s = fpext %s %s to %s", reg, v.Ty.IR, v.Ref, target.IR))
		} else {
			e.emitInstr(fmt.Sprintf("%s = fptrunc %s %s to %s", reg, v.Ty.IR, v.Ref, target.IR))
		}

	default:
		// No known coercion, return as-is
		return v
	}

	return Value{Ref: reg, Ty: target}
}

// emitToBool converts any scalar value to i1 for use in a branch — a thin
// alias for toBool (emit_exprs_types.go), kept as a separate name since
// call sites elsewhere already use it. Previously its own separate
// (near-duplicate) implementation that had quietly diverged from toBool's:
// this one used `fcmp une` for float truthiness, which treats NaN as
// truthy (an "unordered" comparison is true whenever either operand is
// NaN) — wrong, since real JS's Boolean(NaN) === false. toBool's `fcmp
// one` (ordered-and-not-equal, false for NaN) is correct; consolidating
// onto one implementation fixes this rather than carrying two subtly
// different bugs in two places. See ADR-00116.
func (e *Emitter) emitToBool(v Value) Value {
	return e.toBool(v)
}

// typeBits returns the bit-width of the given LLVM IR type string.
func typeBits(ir string) int {
	switch ir {
	case "i1":
		return 1
	case "i8":
		return 8
	case "i16":
		return 16
	case "i32", "float":
		return 32
	case "i64", "double":
		return 64
	}
	return 64
}
