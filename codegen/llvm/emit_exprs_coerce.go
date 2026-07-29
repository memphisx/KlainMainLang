package llvm

import "fmt"

// coerce inserts a type conversion instruction if necessary.
func (e *Emitter) coerce(v Value, target Type) Value {
	// null/undefined assigned to a non-ptr type becomes the zero value.
	if v.Ty.IsNull && target.IR != "ptr" {
		return Value{Ref: zeroRef(target), Ty: target}
	}
	if v.Ty.IR == target.IR {
		return v
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

// emitToBool converts any scalar value to i1 for use in a branch.
func (e *Emitter) emitToBool(v Value) Value {
	if v.Ty.IR == "i1" {
		return v
	}
	r := e.freshReg()
	if v.Ty.IR == "ptr" {
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", r, v.Ref))
	} else if v.Ty.Float {
		e.emitInstr(fmt.Sprintf("%s = fcmp une %s %s, 0.0", r, v.Ty.IR, v.Ref))
	} else {
		e.emitInstr(fmt.Sprintf("%s = icmp ne %s %s, 0", r, v.Ty.IR, v.Ref))
	}
	return Value{Ref: r, Ty: TypeBool}
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
