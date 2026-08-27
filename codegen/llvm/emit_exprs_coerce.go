package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// coerce inserts a type conversion instruction if necessary.
func (e *Emitter) coerce(v Value, target Type) Value {
	// A nullable-scalar aggregate ({ i1, T }, TDD-00064 Stage 3) demotes to its
	// bare payload whenever a plain scalar is wanted — the single point that
	// lets arithmetic, stores, returns, and argument passing consume a boundary
	// value (a T|null return/param/field) without knowing the aggregate shape.
	// Keep it intact only when the target is itself a matching nullable scalar.
	if isNullableScalar(v.Ty) && !(isNullableScalar(target) && target.IR == v.Ty.IR) {
		v = e.nullableScalarPayloadOf(v)
	}
	// null/undefined coerced *to* a nullable scalar becomes an absent { i1, T }
	// aggregate, keeping the invariant that a nullable-scalar-typed Value always
	// carries the aggregate (never a bare zero) in its register.
	if v.Ty.IsNull && isNullableScalar(target) {
		return Value{Ref: e.makeNullableScalarAgg(target, "false", zeroRef(target.withoutNullable())), Ty: target}
	}
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
	// `never` (a Promise.reject's value type — Promise<never>) assigns to any
	// target: the value is never produced (await re-throws before it's read), so a
	// zero of the target type keeps the IR well-typed and stays dead.
	if v.Ty.IsNever && !target.IsNever {
		if target.IsArray {
			return Value{Ref: "{ ptr null, i64 0 }", Ty: target}
		}
		if isNullableScalar(target) {
			return Value{Ref: e.makeNullableScalarAgg(target, "false", zeroRef(target.withoutNullable())), Ty: target}
		}
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

	// float → int. A direct fptosi/fptoui into a target narrower than 64 bits is
	// undefined for an out-of-range value (LLVM yields poison, so `300.0` into an
	// i8 came out `0`). Convert to i64 first, then truncate — this both avoids the
	// UB and gives JS's modular wraparound for narrow integer / TypedArray element
	// stores (ToUint8/ToInt32: `300 → 44`, `-1 → 255`). TDD-00123.
	case v.Ty.Float && dstInt:
		if typeBits(target.IR) < 64 {
			// Always via a signed i64 intermediate: fptoui of a negative value is
			// itself UB, and the modular trunc's 2's-complement bit pattern is the
			// same regardless of the intermediate's signedness (-1 → i64 -1 → i8
			// 0xFF = 255, matching JS ToUint8).
			wide := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fptosi %s %s to i64", wide, v.Ty.IR, v.Ref))
			e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to %s", reg, wide, target.IR))
		} else {
			op := "fptosi"
			if !target.Signed {
				op = "fptoui"
			}
			e.emitInstr(fmt.Sprintf("%s = %s %s %s to %s", reg, op, v.Ty.IR, v.Ref, target.IR))
		}

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

// coercionIsSound reports whether coerce(v, target) actually produced a value of
// the target's storage type — i.e. the conversion was real, not the default
// "return v unchanged" fallthrough for two genuinely-incompatible scalar types
// (a `number` where a string is expected, a string where a number is expected,
// etc.). Composite targets (array, object, Map/Set, dynamic/any, nullable
// scalar, function) legitimately carry a storage IR that differs from a bare
// scalar's, so they're never flagged — their own emit paths handle the value.
// This is the guard behind coerceChecked: this compiler is a typed subset, so a
// genuine cross-kind mismatch (the kind untyped JS produces) should be a clean
// compile error, never an invalid `store`/arithmetic operand.
func coercionIsSound(coerced, target Type) bool {
	if coerced.IR == target.IR {
		return true
	}
	if target.IsArray || target.IsObject || target.IsDynamic || target.IsDynamicObject ||
		target.IsMap || target.IsSet || target.IsFunc || target.UnionMembers != nil ||
		isNullableScalar(target) || target.IR == "void" {
		return true
	}
	return false
}

// coerciblePure predicts, without emitting any IR, whether coerce(src→target)
// yields a value of the target's storage type — a real conversion, not the
// silent "return unchanged" fallthrough. Used to reject a genuine cross-kind
// mismatch (a number stored into a string field, etc.) at a store/consume site
// before it emits invalid IR. Mirrors coerce's own handled cases: same IR,
// composite/dynamic/nullable/union/void target (own path), a null/undefined/
// never/dynamic/nullable source coerce specially rewrites, or numeric↔numeric.
func coerciblePure(src, target Type) bool {
	if src.IR == target.IR {
		return true
	}
	if target.IsArray || target.IsObject || target.IsDynamic || target.IsDynamicObject ||
		target.IsMap || target.IsSet || target.IsFunc || target.UnionMembers != nil ||
		isNullableScalar(target) || target.IR == "void" {
		return true
	}
	if src.IsNull || src.IsUndefined || src.IsNever || src.IsDynamic || isNullableScalar(src) {
		return true
	}
	srcNum := src.IsInteger() || src.Float
	tgtNum := target.IsInteger() || target.Float
	return srcNum && tgtNum
}

// coerceChecked is coerce plus a soundness gate: if the coercion couldn't
// convert v to target (a genuine cross-kind type mismatch), it returns a clean
// compile error instead of a value whose IR silently disagrees with target and
// would emit invalid IR at the consuming store/op. `what` names the context for
// the message (e.g. "assignment", "argument"). See coercionIsSound.
func (e *Emitter) coerceChecked(v Value, target Type, pos ast.Pos, what string) (Value, error) {
	out := e.coerce(v, target)
	if !coercionIsSound(out.Ty, target) {
		return Value{}, fmt.Errorf("%d:%d: type mismatch in %s — a value of one type cannot be used where an incompatible type is expected (this compiler is a typed subset; mixing types the way untyped JS does is not supported)", pos.Line, pos.Col, what)
	}
	return out, nil
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
