// emit_strings.go — string method emission (concat, comparison, slice, indexOf,
// includes, charCodeAt, trim, toUpperCase, toLowerCase, startsWith, endsWith,
// replace, split, String.fromCharCode, etc.) and the isStringTy predicate.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// coerceNumArgRef coerces a string/number method's numeric argument to target,
// returning a clean compile error (not invalid IR) when the argument is a
// non-numeric type such as a Symbol — the A1 invalid-IR cluster fix: a bare
// coerce silently returned the ptr register where an i64/double operand was
// expected, which reached arithmetic/compare as `'%t' type 'ptr' but expected
// 'i64'` (the same defect ADR-00882 fixed for DataView). `what` names the
// method/argument for the message. The rejection matches tsc, which itself
// refuses e.g. `"x".charAt(sym)`.
func (e *Emitter) coerceNumArgRef(v Value, target Type, pos ast.Pos, what string) (string, error) {
	cv, err := e.coerceChecked(v, target, pos, what)
	if err != nil {
		return "", err
	}
	return cv.Ref, nil
}

// isStringTy returns true for a plain string (ptr, not object/array/closure).
func isStringTy(ty Type) bool {
	return ty.IR == "ptr" && !ty.IsObject && !ty.IsArray && !ty.IsFlatArray && !ty.IsFunc && !ty.IsBigInt &&
		!ty.IsURLSearchParams // a URLSearchParams is a pair-list handle, not a string (TDD-00203)
}

// isForOfStringTy is the strict "this is really a plain string" test, for
// contexts (like for-of dispatch) where the loose isStringTy — which matches
// any bare `ptr` — would wrongly grab a Map/Set/RegExp/Promise/etc., all of
// which are also `ptr`-typed. It excludes every ptr-typed special type this
// type system carries a flag for, so only a genuine string survives.
func isForOfStringTy(ty Type) bool {
	return isStringTy(ty) && !ty.IsMap && !ty.IsSet && !ty.IsRegExp && !ty.IsPromise &&
		!ty.IsDate && !ty.IsURL && !ty.IsURLPattern && !ty.IsSymbol && !ty.IsBlob &&
		!ty.IsTypedArray && !ty.IsArrayBuffer && !ty.IsReadableStream && !ty.IsStreamReader &&
		!ty.IsNodeReadable && !ty.IsGenerator && !ty.IsDynamic && !ty.Nullable
}

// isNumberTy returns true for a plain numeric scalar (any int/float width,
// or bool) — used to gate Number.prototype method dispatch (toString(radix)
// etc.) to a receiver that's actually a number, not e.g. a string (also
// IR=="ptr"-free of the object/array/func flags, so not caught by
// isStringTy's negative check alone) or a Date (IR=="i64" like a plain
// number, but IsDate-tagged and meant to go through Date's own dispatch).
func isNumberTy(ty Type) bool {
	return ty.IR != "ptr" && !ty.IsDate
}

// emitStringNullToLiteral substitutes the interned "null" string for v when
// v is a null pointer at runtime, leaving any non-null value unchanged —
// mirrors the same select-on-icmp-eq-null pattern emitConsoleArg's ptr case
// (emit_call_console.go) already uses for console.log's own "%s" argument,
// since a null string here (an optional/nullable-typed operand whose value
// happens to be absent) is the exact same "printf/strlen(NULL) is UB"
// hazard, just reached through string concatenation instead of printing.
func (e *Emitter) emitStringNullToLiteral(v Value) Value {
	// A `T | undefined` operand (TDD-00187) whose value is absent renders as
	// "undefined", not "null" — Node stringifies `undefined` in a `+` as
	// "undefined" (`"x" + undefined === "xundefined"`). A plain `T | null`
	// operand keeps "null". The static IsUndefined flag distinguishes them.
	lit := "null"
	if v.Ty.IsUndefined {
		lit = "undefined"
	}
	isNull := e.freshReg()
	safe := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", safe, isNull, e.internString(lit), v.Ref))
	return Value{Ref: safe, Ty: TypePtr}
}

// emitStringConcat concatenates two string (ptr) values and returns a new
// heap string. Found crashing (not just wrong) on a null operand — real JS
// stringifies `null` as "null" in a `+` (`"x" + null === "xnull"`), but this
// called strlen()/memcpy() on the raw pointer unconditionally, so any
// nullable-typed string that was actually null (an optional param, `T |
// null`, a missing Map/collection lookup, ...) segfaulted here instead.
// emitStrLenHeader returns a register holding a KML string's byte length read
// from its 8-byte header (TDD-00120 Stage 2), the binary-safe replacement for a
// strlen call — an embedded NUL no longer truncates the reported length. Use it
// wherever a *KML string value's* length is needed (`.length`, concat/slice/
// indexOf operands); producer-internal strlen that establishes a header (see
// emitStringFinalizeLen) stays on strlen.
func (e *Emitter) emitStrLenHeader(ref string) string {
	e.ensureStrHeaderRuntime()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", r, ref))
	return r
}

// emitStringAlloc allocates a length-prefixed string buffer for dataLenReg
// content bytes (TDD-00120 Stage 1): malloc(8 + dataLen + 1), store dataLen as an
// i64 header at offset 0, and return the register pointing at the bytes (base+8).
// The caller fills + NUL-terminates relative to the returned pointer exactly as
// before — only the allocation and the returned pointer's origin change. The
// header lets binary-safe length reads (Stage 2+) find the true length via
// ptr-8; the retained NUL keeps every existing strlen consumer and C-interop
// boundary working unchanged in the meantime. Free such a buffer with
// emitStringFree (free base = ptr-8), never free(ptr).
func (e *Emitter) emitStringAlloc(dataLenReg string) string {
	e.ensureStrHeaderRuntime()
	dataPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_alloc(i64 %s)", dataPtr, dataLenReg))
	return dataPtr
}

// emitStringScratch allocates a length-prefixed buffer with `capBytes` of data
// capacity (TDD-00120) for producers that write via sprintf and only learn the
// length afterward (number/float/function formatting). Returns the data pointer
// (base+8); the header is uninitialized until emitStringFinalizeLen is called
// once the bytes are written. capBytes must include room for the NUL.
func (e *Emitter) emitStringScratch(capBytes int) string {
	return e.emitStringScratchReg(fmt.Sprintf("%d", capBytes))
}

// emitStringScratchReg is emitStringScratch with a register (or constant)
// capacity expression, for producers whose scratch size is only known at runtime.
func (e *Emitter) emitStringScratchReg(capExpr string) string {
	e.ensureStrHeaderRuntime()
	e.ensureMalloc()
	sz := e.freshReg()
	base := e.freshReg()
	dataPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 8", sz, capExpr))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", base, sz))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", dataPtr, base))
	return dataPtr
}

// emitStringSetLen stores a known length register into the header at dataPtr-8,
// for a scratch buffer whose exact byte count is computed during the fill (so no
// strlen is needed, and an embedded NUL in the content is preserved).
func (e *Emitter) emitStringSetLen(dataPtr, lenReg string) {
	hp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 -8", hp, dataPtr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenReg, hp))
}

// emitStringFinalizeLen stores strlen(dataPtr) into the length header at
// dataPtr-8, for a scratch buffer written via sprintf (whose content never
// contains an embedded NUL, so strlen is the true length). Pairs with
// emitStringScratch.
func (e *Emitter) emitStringFinalizeLen(dataPtr string) {
	e.ensureStrlen()
	l := e.freshReg()
	hp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", l, dataPtr))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 -8", hp, dataPtr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", l, hp))
}

// emitStringFree frees a length-prefixed heap string (TDD-00120): the malloc base
// is 8 bytes before the value pointer. Freeing the value pointer directly would
// corrupt the heap (interior free).
func (e *Emitter) emitStringFree(ptrRef string) {
	e.ensureStrHeaderRuntime()
	e.emitInstr(fmt.Sprintf("call void @__kml_str_free(ptr %s)", ptrRef))
}

func (e *Emitter) emitStringConcat(left, right Value) (Value, error) {
	e.ensureStrlen()
	e.ensureMemcpy()
	left = e.emitStringNullToLiteral(left)
	right = e.emitStringNullToLiteral(right)
	// NOTE (TDD-00120): still strlen-based — a fully binary-safe concat needs
	// every string producer to carry a length header first (the consumer switch
	// is gated on 100% producer coverage; see the TDD's Stage-2 notes).
	n1 := e.freshReg()
	n2 := e.freshReg()
	total := e.freshReg()
	dst := e.freshReg()
	n2p1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", n1, left.Ref))
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", n2, right.Ref))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", total, n1, n2))
	buf := e.emitStringAlloc(total)
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", buf, left.Ref, n1))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", dst, buf, n1))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", n2p1, n2))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", dst, right.Ref, n2p1))
	return Value{Ref: buf, Ty: TypePtr}, nil
}

// emitStringBinary handles binary operations on two string (ptr) operands.
func (e *Emitter) emitStringBinary(op string, left, right Value, pos ast.Pos) (Value, error) {
	switch op {
	case "+":
		return e.emitStringConcat(left, right)
	case "==", "===", "!=", "!==", "<", ">", "<=", ">=":
		// Binary-safe: __kml_str_cmp uses the header lengths + memcmp, so an
		// embedded NUL no longer stops the comparison early (TDD-00120 Stage 2).
		//
		// Null-aware (ADR-00724): a string-typed operand can be the null
		// pointer that stands for `undefined` (a missing process.env entry,
		// an absent optional). JS semantics: undefined equals only undefined,
		// and every ordering comparison against undefined is false (NaN).
		// Nulls are swapped for "" before the compare so it never
		// dereferences one; the null flags then decide the result.
		e.ensureStrHeaderRuntime()
		lNull := e.freshReg()
		rNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", lNull, left.Ref))
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", rNull, right.Ref))
		empty := e.internString("")
		lSafe := e.freshReg()
		rSafe := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", lSafe, lNull, empty, left.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", rSafe, rNull, empty, right.Ref))
		cmp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_str_cmp(ptr %s, ptr %s)", cmp, lSafe, rSafe))
		anyNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", anyNull, lNull, rNull))
		bothNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", bothNull, lNull, rNull))
		iop := map[string]string{
			"==": "eq", "===": "eq", "!=": "ne", "!==": "ne",
			"<": "slt", ">": "sgt", "<=": "sle", ">=": "sge",
		}[op]
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp %s i32 %s, 0", raw, iop, cmp))
		result := e.freshReg()
		switch op {
		case "==", "===":
			// both null → true; one null → false; else the compare.
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i1 %s, i1 %s", result, anyNull, bothNull, raw))
		case "!=", "!==":
			notBoth := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notBoth, bothNull))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i1 %s, i1 %s", result, anyNull, notBoth, raw))
		default:
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i1 false, i1 %s", result, anyNull, raw))
		}
		return Value{Ref: result, Ty: TypeBool}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: operator '%s' is not supported for strings", pos.Line, pos.Col, op)
}

// emitStringExtract allocates a new heap string containing src[start..start+length)
// and returns a ptr value. startReg and lenReg are i64 register references.
func (e *Emitter) emitStringExtract(srcRef, startReg, lenReg string) Value {
	srcPtr := e.freshReg()
	nullSlot := e.freshReg()
	buf := e.emitStringAlloc(lenReg)
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", srcPtr, srcRef, startReg))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", buf, srcPtr, lenReg))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", nullSlot, buf, lenReg))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", nullSlot))
	return Value{Ref: buf, Ty: TypePtr}
}

// emitNormalizeSliceIdx normalizes a slice index the JS way: negative values are
// treated as offset-from-end, then the result is clamped to [0, sLen].
// Returns the register name holding the normalized i64 value.
func (e *Emitter) emitNormalizeSliceIdx(idx, sLen string) string {
	fromEnd := e.freshReg()
	isNeg := e.freshReg()
	fromEndOk := e.freshReg()
	withNeg := e.freshReg()
	gtLen := e.freshReg()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", fromEnd, sLen, idx))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNeg, idx))
	// if fromEnd < 0, clamp that to 0
	fromEndLt0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", fromEndLt0, fromEnd))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", fromEndOk, fromEndLt0, fromEnd))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", withNeg, isNeg, fromEndOk, idx))
	// clamp to [0, sLen]
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", gtLen, withNeg, sLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", result, gtLen, sLen, withNeg))
	return result
}

// emitStringSlice implements s.slice(start[, end]).
// Negative indices count from the end; both are clamped to [0, len].
func (e *Emitter) emitStringSlice(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: slice takes 1 or 2 arguments", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: slice is only supported on strings", pos.Line, pos.Col)
	}
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()

	sLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", sLen, objVal.Ref))

	startRaw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	startRef, err := e.coerceNumArgRef(startRaw, TypeI64, args[0].GetPos(), "slice index")
	if err != nil {
		return Value{}, err
	}
	startN := e.emitNormalizeSliceIdx(startRef, sLen)

	var endN string
	if len(args) == 2 {
		endRaw, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		endRef, err := e.coerceNumArgRef(endRaw, TypeI64, args[1].GetPos(), "slice index")
		if err != nil {
			return Value{}, err
		}
		endN = e.emitNormalizeSliceIdx(endRef, sLen)
	} else {
		endN = sLen
	}

	rawLen := e.freshReg()
	isNegLen := e.freshReg()
	sliceLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", rawLen, endN, startN))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNegLen, rawLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", sliceLen, isNegLen, rawLen))

	return e.emitStringExtract(objVal.Ref, startN, sliceLen), nil
}

// emitStringSubstring implements s.substring(start[, end]).
// Negative indices are clamped to 0; if start > end they are swapped.
func (e *Emitter) emitStringSubstring(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: substring takes 1 or 2 arguments", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: substring is only supported on strings", pos.Line, pos.Col)
	}
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()

	sLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", sLen, objVal.Ref))

	// clampSubstr: negative → 0, > sLen → sLen
	clamp := func(raw Value) string {
		lt0 := e.freshReg()
		c0 := e.freshReg()
		gtL := e.freshReg()
		cL := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", lt0, raw.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", c0, lt0, raw.Ref))
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", gtL, c0, sLen))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", cL, gtL, sLen, c0))
		return cL
	}

	startRaw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	startCV, err := e.coerceChecked(startRaw, TypeI64, args[0].GetPos(), "substring index")
	if err != nil {
		return Value{}, err
	}
	startC := clamp(startCV)

	var endC string
	if len(args) == 2 {
		endRaw, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		endCV, err := e.coerceChecked(endRaw, TypeI64, args[1].GetPos(), "substring index")
		if err != nil {
			return Value{}, err
		}
		endC = clamp(endCV)
	} else {
		endC = sLen
	}

	// swap if startC > endC
	needSwap := e.freshReg()
	realStart := e.freshReg()
	realEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", needSwap, startC, endC))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", realStart, needSwap, endC, startC))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", realEnd, needSwap, startC, endC))

	sliceLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", sliceLen, realEnd, realStart))

	return e.emitStringExtract(objVal.Ref, realStart, sliceLen), nil
}

// emitStringSubstr implements the (deprecated but common) s.substr(start, length):
// a negative start counts from the end (`max(len + start, 0)`); length (default
// to the end) is clamped to `[0, len - start]`. Distinct from substring, which
// takes an end index and swaps reversed bounds.
func (e *Emitter) emitStringSubstr(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: substr takes 1 or 2 arguments (start[, length])", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: substr is only supported on strings", pos.Line, pos.Col)
	}
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()

	sLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", sLen, objVal.Ref))

	startRaw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	sr, err := e.coerceNumArgRef(startRaw, TypeI64, args[0].GetPos(), "substr start")
	if err != nil {
		return Value{}, err
	}
	// start: negative → len + start, then clamp to [0, len].
	neg := e.freshReg()
	fromEnd := e.freshReg()
	s0 := e.freshReg()
	ltz := e.freshReg()
	sClamp := e.freshReg()
	gtL := e.freshReg()
	start := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, sr))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", fromEnd, sLen, sr))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", s0, neg, fromEnd, sr))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", ltz, s0))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", sClamp, ltz, s0))
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", gtL, sClamp, sLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", start, gtL, sLen, sClamp))

	// avail = len - start (the maximum length from here).
	avail := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", avail, sLen, start))

	var sliceLen string
	if len(args) == 2 {
		lenRaw, lerr := e.emitExpr(args[1])
		if lerr != nil {
			return Value{}, lerr
		}
		lr, lerr := e.coerceNumArgRef(lenRaw, TypeI64, args[1].GetPos(), "substr length")
		if lerr != nil {
			return Value{}, lerr
		}
		// length: <0 → 0, else min(length, avail).
		lneg := e.freshReg()
		l0 := e.freshReg()
		gtAvail := e.freshReg()
		clamped := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", lneg, lr))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", l0, lneg, lr))
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", gtAvail, l0, avail))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", clamped, gtAvail, avail, l0))
		sliceLen = clamped
	} else {
		sliceLen = avail
	}

	return e.emitStringExtract(objVal.Ref, start, sliceLen), nil
}

// emitStringIndexOf implements s.indexOf(needle): returns the byte offset of the
// first occurrence, or -1 if not found. Uses strstr + ptrtoint arithmetic + select.
func (e *Emitter) emitStringIndexOf(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: indexOf takes 1 or 2 arguments (searchString[, fromIndex])", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: indexOf is only supported on strings", pos.Line, pos.Col)
	}
	needleVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// ToString(searchString) for an object argument (TDD-00201): `s.indexOf(obj)`
	// searches for obj's toString/@@toPrimitive value, not its pointer.
	if needleVal, err = e.coerceStringArg(needleVal); err != nil {
		return Value{}, err
	}
	// Binary-safe: __kml_str_indexof searches via memmem over the header lengths,
	// so an embedded NUL in either operand doesn't cut the search short. Returns
	// the byte index or -1 directly (TDD-00120 Stage 2). The optional fromIndex
	// starts the search at (clamped) that offset.
	e.ensureStrHeaderRuntime()
	final := e.freshReg()
	if len(args) == 2 {
		fromVal, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		from, err := e.coerceNumArgRef(fromVal, TypeI64, args[1].GetPos(), "indexOf fromIndex")
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_indexof_from(ptr %s, ptr %s, i64 %s)", final, objVal.Ref, needleVal.Ref, from))
	} else {
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_indexof(ptr %s, ptr %s)", final, objVal.Ref, needleVal.Ref))
	}
	return e.countToNumber(Value{Ref: final, Ty: TypeI64}), nil
}

// emitStringLastIndexOf implements s.lastIndexOf(sub): the byte index of the
// LAST occurrence of sub, or -1 — binary-safe, via __kml_str_lastindexof's
// descending memcmp scan.
func (e *Emitter) emitStringLastIndexOf(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: lastIndexOf takes 1 or 2 arguments (searchString[, fromIndex])", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: lastIndexOf is only supported on strings", pos.Line, pos.Col)
	}
	needleVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// ToString(searchString) for an object argument (TDD-00201).
	if needleVal, err = e.coerceStringArg(needleVal); err != nil {
		return Value{}, err
	}
	e.ensureStrHeaderRuntime()
	final := e.freshReg()
	// The optional fromIndex is the highest start offset considered — the scan
	// walks backward from min(fromIndex, len-needleLen).
	if len(args) == 2 {
		fromVal, ferr := e.emitExpr(args[1])
		if ferr != nil {
			return Value{}, ferr
		}
		f, ferr := e.coerceNumArgRef(fromVal, TypeI64, args[1].GetPos(), "lastIndexOf fromIndex")
		if ferr != nil {
			return Value{}, ferr
		}
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_lastindexof_from(ptr %s, ptr %s, i64 %s)", final, objVal.Ref, needleVal.Ref, f))
		return e.countToNumber(Value{Ref: final, Ty: TypeI64}), nil
	}
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_lastindexof(ptr %s, ptr %s)", final, objVal.Ref, needleVal.Ref))
	return e.countToNumber(Value{Ref: final, Ty: TypeI64}), nil
}

// emitStringIncludes implements s.includes(needle): returns true iff needle appears in s.
func (e *Emitter) emitStringIncludes(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: includes takes 1 or 2 arguments (searchString[, position])", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: includes is only supported on strings", pos.Line, pos.Col)
	}
	needleVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if needleVal, err = e.coerceStringArg(needleVal); err != nil { // ToString (TDD-00201)
		return Value{}, err
	}
	// Binary-safe includes: memmem-based index, then test for >= 0 (TDD-00120).
	// The optional position starts the search there (via __kml_str_indexof_from).
	e.ensureStrHeaderRuntime()
	idx := e.freshReg()
	found := e.freshReg()
	if len(args) == 2 {
		posVal, perr := e.emitExpr(args[1])
		if perr != nil {
			return Value{}, perr
		}
		from, perr := e.coerceNumArgRef(posVal, TypeI64, args[1].GetPos(), "includes position")
		if perr != nil {
			return Value{}, perr
		}
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_indexof_from(ptr %s, ptr %s, i64 %s)", idx, objVal.Ref, needleVal.Ref, from))
	} else {
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_indexof(ptr %s, ptr %s)", idx, objVal.Ref, needleVal.Ref))
	}
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", found, idx))
	return Value{Ref: found, Ty: TypeBool}, nil
}

func (e *Emitter) emitStringCharCodeAt(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: charCodeAt expects 1 argument", pos.Line, pos.Col)
	}
	strVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	idxVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	idxVal, err = e.coerceChecked(idxVal, TypeI64, args[0].GetPos(), "charCodeAt index")
	if err != nil {
		return Value{}, err
	}
	// Bounds check: an out-of-range index (negative or >= length) returns
	// NaN, as real JS — the pre-fix code loaded whatever byte happened to sit
	// at that address. The result is a double for exactly that reason (only a
	// double can hold NaN); an in-range code unit prints/compares identically.
	// The load itself is clamped to index 0 when out of range (still inside
	// the allocation — at worst the NUL terminator) and its value discarded.
	e.ensureStrlen()
	sLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", sLen, strVal.Ref))
	geZero := e.freshReg()
	ltLen := e.freshReg()
	inBounds := e.freshReg()
	safeIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", geZero, idxVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", ltLen, idxVal.Ref, sLen))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", inBounds, geZero, ltLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", safeIdx, inBounds, idxVal.Ref))
	charPtr := e.freshReg()
	charByte := e.freshReg()
	asI64 := e.freshReg()
	asF64 := e.freshReg()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", charPtr, strVal.Ref, safeIdx))
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", charByte, charPtr))
	e.emitInstr(fmt.Sprintf("%s = zext i8 %s to i64", asI64, charByte))
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", asF64, asI64))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, double %s, double 0x7FF8000000000000", result, inBounds, asF64))
	return Value{Ref: result, Ty: TypeF64}, nil
}

// emitStringCharAtMethod implements s.charAt(i): a 1-character string at
// index i, or "" if i is out of range. Unlike .at(), charAt does NOT support
// negative indices (charAt(-1) is always "", never wraps from the end) —
// matching real JS's own distinction between the two methods. Named
// distinctly from emitStringCharAt below (which backs s[i] bracket
// indexing, a different, pre-existing feature with the "obvious" name
// already taken).
func (e *Emitter) emitStringCharAtMethod(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: charAt takes exactly 1 argument", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: charAt is only supported on strings", pos.Line, pos.Col)
	}
	idxRaw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	idxVal, err := e.coerceChecked(idxRaw, TypeI64, args[0].GetPos(), "charAt index")
	if err != nil {
		return Value{}, err
	}
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	sLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", sLen, objVal.Ref))
	geZero := e.freshReg()
	ltLen := e.freshReg()
	inBounds := e.freshReg()
	sliceLen := e.freshReg()
	safeStart := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", geZero, idxVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", ltLen, idxVal.Ref, sLen))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", inBounds, geZero, ltLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 1, i64 0", sliceLen, inBounds))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", safeStart, inBounds, idxVal.Ref))
	return e.emitStringExtract(objVal.Ref, safeStart, sliceLen), nil
}

// emitStringCodePointAt implements s.codePointAt(i). This compiler's strings
// are plain byte sequences, not real UTF-16 (like actual JS strings) — there
// is no surrogate-pair/multi-byte code point decoding here, so an in-range
// result is exactly charCodeAt's byte value under a second name. Correct for
// ASCII/Latin-1 input (where a "code point" and a "char code" are the same
// number); a documented scope narrowing for anything requiring real Unicode
// decoding, consistent with this compiler having no Unicode infrastructure
// at all yet.
//
// Unlike charCodeAt (whose out-of-range result is NaN, hence a double),
// codePointAt returns `number | undefined` — Node yields `undefined` for an
// out-of-range index (TDD-00187). An in-range code point is a real integer,
// so the payload is an i64 riding the presence-flagged { i1, i64 } aggregate.
func (e *Emitter) emitStringCodePointAt(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: codePointAt expects 1 argument", pos.Line, pos.Col)
	}
	strVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(strVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: codePointAt is only supported on strings", pos.Line, pos.Col)
	}
	idxRaw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	idxVal, err := e.coerceChecked(idxRaw, TypeI64, args[0].GetPos(), "codePointAt index")
	if err != nil {
		return Value{}, err
	}
	// Bounds check: an out-of-range index (negative or >= length) is absent.
	// The load itself is clamped to index 0 when out of range (still inside
	// the allocation — at worst the NUL terminator) and its value discarded by
	// the presence bit.
	e.ensureStrlen()
	sLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", sLen, strVal.Ref))
	geZero := e.freshReg()
	ltLen := e.freshReg()
	inBounds := e.freshReg()
	safeIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", geZero, idxVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", ltLen, idxVal.Ref, sLen))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", inBounds, geZero, ltLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", safeIdx, inBounds, idxVal.Ref))
	charPtr := e.freshReg()
	charByte := e.freshReg()
	asI64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", charPtr, strVal.Ref, safeIdx))
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", charByte, charPtr))
	e.emitInstr(fmt.Sprintf("%s = zext i8 %s to i64", asI64, charByte))
	return e.wrapUndefinedable(Value{Ref: asI64, Ty: TypeI64}, inBounds), nil
}

// emitStringSearch implements s.search(pattern) — real JS-shaped since
// TDD-00035 Stage 5 (docs/adr/ADR-00119.md): a RegExp argument runs a real
// PCRE2 search (always from offset 0, never observably affecting the
// regex's own `lastIndex` — see emit_regexp_split.go's emitRegexSearch).
// A plain-string argument still falls back to the original literal
// behavior established before RegExp existed at all: exactly .indexOf()'s
// behavior under a second name (real JS coerces a non-RegExp argument to
// one; this compiler doesn't implement that implicit coercion, matching
// match()/matchAll()/replace()/replaceAll()'s identical narrowing).
func (e *Emitter) emitStringSearch(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: search takes exactly 1 argument", pos.Line, pos.Col)
	}
	// Real JS coerces a non-RegExp argument to a RegExp (`str.search(".")`
	// treats "." as the any-char pattern, not a literal dot), so a plain-string
	// pattern is compiled into a RegExp exactly as `new RegExp(pattern)` would
	// — special characters are interpreted (ADR-00548). Only genuinely
	// non-stringy arguments fall back to nothing.
	argTy := e.inferExprType(args[0])
	if argTy.IsRegExp || isStringTy(argTy) {
		strVal, err := e.emitExpr(mem.Object)
		if err != nil {
			return Value{}, err
		}
		if !isStringTy(strVal.Ty) {
			return Value{}, fmt.Errorf("%d:%d: search is only supported on strings", pos.Line, pos.Col)
		}
		strVal = e.coerce(strVal, TypePtr)
		var regexVal Value
		if argTy.IsRegExp {
			regexVal, err = e.emitExpr(args[0])
		} else {
			regexVal, err = e.emitNewRegExpExpression(&ast.NewRegExpExpression{Pattern: args[0]})
		}
		if err != nil {
			return Value{}, err
		}
		return e.emitRegexSearch(strVal, regexVal), nil
	}
	return e.emitStringIndexOf(mem, args, pos)
}

// emitStringLocaleCompare implements s.localeCompare(other): byte-order
// comparison via strcmp, normalized to exactly -1/0/1 (real JS's spec only
// requires negative/zero/positive, but a predictable fixed set of return
// values is more useful to print/assert on). Not real Unicode collation —
// this compiler has no locale/Intl infrastructure, the same documented scope
// narrowing already used for toLocaleDateString.
func (e *Emitter) emitStringLocaleCompare(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: localeCompare takes exactly 1 argument", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: localeCompare is only supported on strings", pos.Line, pos.Col)
	}
	otherVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	otherVal = e.coerce(otherVal, TypePtr)
	// Binary-safe byte-order compare via the header lengths (TDD-00120 Stage 2).
	e.ensureStrHeaderRuntime()
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_str_cmp(ptr %s, ptr %s)", raw, objVal.Ref, otherVal.Ref))
	isNeg := e.freshReg()
	isPos := e.freshReg()
	step1 := e.freshReg()
	result32 := e.freshReg()
	result64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i32 %s, 0", isNeg, raw))
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i32 %s, 0", isPos, raw))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i32 1, i32 0", step1, isPos))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i32 -1, i32 %s", result32, isNeg, step1))
	e.emitInstr(fmt.Sprintf("%s = sext i32 %s to i64", result64, result32))
	return e.countToNumber(Value{Ref: result64, Ty: TypeI64}), nil
}

func (e *Emitter) emitStringTrim(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: trim takes no arguments", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: trim is only supported on strings", pos.Line, pos.Col)
	}
	e.ensureStringTrim()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_trim(ptr %s)", result, objVal.Ref))
	return Value{Ref: result, Ty: TypePtr}, nil
}

func (e *Emitter) emitStringTrimStart(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: trimStart takes no arguments", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: trimStart is only supported on strings", pos.Line, pos.Col)
	}
	e.ensureStringTrimStart()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_trim_start(ptr %s)", result, objVal.Ref))
	return Value{Ref: result, Ty: TypePtr}, nil
}

func (e *Emitter) emitStringTrimEnd(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: trimEnd takes no arguments", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: trimEnd is only supported on strings", pos.Line, pos.Col)
	}
	e.ensureStringTrimEnd()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_trim_end(ptr %s)", result, objVal.Ref))
	return Value{Ref: result, Ty: TypePtr}, nil
}

func (e *Emitter) emitStringToUpper(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: toUpperCase takes no arguments", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: toUpperCase is only supported on strings", pos.Line, pos.Col)
	}
	e.ensureStringToUpper()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_toupper(ptr %s)", result, objVal.Ref))
	return Value{Ref: result, Ty: TypePtr}, nil
}

func (e *Emitter) emitStringToLower(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: toLowerCase takes no arguments", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: toLowerCase is only supported on strings", pos.Line, pos.Col)
	}
	e.ensureStringToLower()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_tolower(ptr %s)", result, objVal.Ref))
	return Value{Ref: result, Ty: TypePtr}, nil
}

func (e *Emitter) emitStringStartsWith(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: startsWith takes 1 or 2 arguments (searchString[, position])", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: startsWith is only supported on strings", pos.Line, pos.Col)
	}
	prefixVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if prefixVal, err = e.coerceStringArg(prefixVal); err != nil { // ToString (TDD-00201)
		return Value{}, err
	}
	// The optional position anchors the prefix test at that offset (binary-safe
	// bounds-checked memcmp).
	if len(args) == 2 {
		posVal, perr := e.emitExpr(args[1])
		if perr != nil {
			return Value{}, perr
		}
		p, perr := e.coerceNumArgRef(posVal, TypeI64, args[1].GetPos(), "startsWith position")
		if perr != nil {
			return Value{}, perr
		}
		e.ensureStrHeaderRuntime()
		e.ensureMemcmp()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_str_startswith_at(ptr %s, ptr %s, i64 %s)", r, objVal.Ref, prefixVal.Ref, p))
		return Value{Ref: r, Ty: TypeBool}, nil
	}
	e.ensureStrlen()
	e.ensureStrncmp()
	prefixLen := e.freshReg()
	cmp := e.freshReg()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", prefixLen, prefixVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = call i32 @strncmp(ptr %s, ptr %s, i64 %s)", cmp, objVal.Ref, prefixVal.Ref, prefixLen))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", result, cmp))
	return Value{Ref: result, Ty: TypeBool}, nil
}

func (e *Emitter) emitStringEndsWith(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: endsWith takes 1 or 2 arguments (searchString[, endPosition])", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: endsWith is only supported on strings", pos.Line, pos.Col)
	}
	suffixVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if suffixVal, err = e.coerceStringArg(suffixVal); err != nil { // ToString (TDD-00201)
		return Value{}, err
	}
	// The optional endPosition treats the string as if it ended there.
	if len(args) == 2 {
		endVal, eerr := e.emitExpr(args[1])
		if eerr != nil {
			return Value{}, eerr
		}
		ep, eperr := e.coerceNumArgRef(endVal, TypeI64, args[1].GetPos(), "endsWith endPosition")
		if eperr != nil {
			return Value{}, eperr
		}
		e.ensureStrHeaderRuntime()
		e.ensureMemcmp()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_str_endswith_at(ptr %s, ptr %s, i64 %s)", r, objVal.Ref, suffixVal.Ref, ep))
		return Value{Ref: r, Ty: TypeBool}, nil
	}
	e.ensureStrlen()
	e.ensureStrncmp()
	sLen := e.freshReg()
	sufLen := e.freshReg()
	diff := e.freshReg()
	ge := e.freshReg()
	safeDiff := e.freshReg()
	tailPtr := e.freshReg()
	cmp := e.freshReg()
	eq := e.freshReg()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", sLen, objVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", sufLen, suffixVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", diff, sLen, sufLen))
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, %s", ge, sLen, sufLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", safeDiff, ge, diff))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", tailPtr, objVal.Ref, safeDiff))
	e.emitInstr(fmt.Sprintf("%s = call i32 @strncmp(ptr %s, ptr %s, i64 %s)", cmp, tailPtr, suffixVal.Ref, sufLen))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", eq, cmp))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", result, ge, eq))
	return Value{Ref: result, Ty: TypeBool}, nil
}

func (e *Emitter) emitStringReplace(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: replace takes exactly 2 arguments", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: replace is only supported on strings", pos.Line, pos.Col)
	}
	if e.inferExprType(args[0]).IsRegExp {
		return e.emitRegexReplace(objVal, args, pos, false)
	}
	// String-literal search with a function replacer: invoke it per match with
	// (match, offset, string) — ADR-00697.
	if e.inferExprType(args[1]).IsFunc {
		return e.emitStringReplaceLiteralCallback(objVal, args[0], args[1], false, pos)
	}
	searchVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	repVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	// ToString an object search/replacement (TDD-00201); a RegExp arg was already
	// dispatched to emitRegexReplace above, so neither is a regex here.
	if searchVal, err = e.coerceStringArg(searchVal); err != nil {
		return Value{}, err
	}
	if repVal, err = e.coerceStringArg(repVal); err != nil {
		return Value{}, err
	}
	e.ensureStringReplace()
	sLen := e.emitStrLenHeader(objVal.Ref)
	searchLen := e.emitStrLenHeader(searchVal.Ref)
	repLen := e.emitStrLenHeader(repVal.Ref)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_replace(ptr %s, i64 %s, ptr %s, i64 %s, ptr %s, i64 %s)", result, objVal.Ref, sLen, searchVal.Ref, searchLen, repVal.Ref, repLen))
	return Value{Ref: result, Ty: TypePtr}, nil
}

func (e *Emitter) emitStringReplaceAll(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: replaceAll takes exactly 2 arguments", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: replaceAll is only supported on strings", pos.Line, pos.Col)
	}
	if e.inferExprType(args[0]).IsRegExp {
		return e.emitRegexReplace(objVal, args, pos, true)
	}
	// String-literal search with a function replacer: invoke it per match with
	// (match, offset, string) — ADR-00697.
	if e.inferExprType(args[1]).IsFunc {
		return e.emitStringReplaceLiteralCallback(objVal, args[0], args[1], true, pos)
	}
	searchVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	repVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	// ToString an object search/replacement (TDD-00201); a RegExp arg was already
	// dispatched to emitRegexReplace above.
	if searchVal, err = e.coerceStringArg(searchVal); err != nil {
		return Value{}, err
	}
	if repVal, err = e.coerceStringArg(repVal); err != nil {
		return Value{}, err
	}
	e.ensureStringReplaceAll()
	sLen := e.emitStrLenHeader(objVal.Ref)
	searchLen := e.emitStrLenHeader(searchVal.Ref)
	repLen := e.emitStrLenHeader(repVal.Ref)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_replace_all(ptr %s, i64 %s, ptr %s, i64 %s, ptr %s, i64 %s)", result, objVal.Ref, sLen, searchVal.Ref, searchLen, repVal.Ref, repLen))
	return Value{Ref: result, Ty: TypePtr}, nil
}

func (e *Emitter) emitStringSplit(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: split takes 1 or 2 arguments (separator[, limit])", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: split is only supported on strings", pos.Line, pos.Col)
	}
	var result Value
	if e.inferExprType(args[0]).IsRegExp {
		objVal = e.coerce(objVal, TypePtr)
		regexVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		result = e.emitRegexSplit(objVal, regexVal)
	} else {
		sepVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		// ToString an object separator (TDD-00201); the regex path is handled above.
		if sepVal, err = e.coerceStringArg(sepVal); err != nil {
			return Value{}, err
		}
		e.ensureStringSplit()
		sLen := e.emitStrLenHeader(objVal.Ref)
		sepLen := e.emitStrLenHeader(sepVal.Ref)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_split(ptr %s, i64 %s, ptr %s, i64 %s)", r, objVal.Ref, sLen, sepVal.Ref, sepLen))
		result = Value{Ref: r, Ty: ArrayOf(TypePtr)}
	}
	// Optional `limit`: cap the result to the first `limit` segments (JS
	// `split(sep, limit)`). We split fully, then clamp the reported length — the
	// observable result is identical. A negative limit acts as "no cap" (JS
	// ToUint32 makes it a huge count); the data pointer is unchanged.
	if len(args) == 2 {
		limVal, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		lim, err := e.coerceNumArgRef(limVal, TypeI64, args[1].GetPos(), "split limit")
		if err != nil {
			return Value{}, err
		}
		fullLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", fullLen, result.Ref))
		dataPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", dataPtr, result.Ref))
		// effectiveLimit = limit < 0 ? fullLen : limit
		negLim := e.freshReg()
		effLim := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", negLim, lim))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", effLim, negLim, fullLen, lim))
		// newLen = min(fullLen, effectiveLimit)
		useLim := e.freshReg()
		newLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", useLim, effLim, fullLen))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", newLen, useLim, effLim, fullLen))
		agg0 := e.freshReg()
		agg1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", agg0, dataPtr))
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", agg1, agg0, newLen))
		result = Value{Ref: agg1, Ty: ArrayOf(TypePtr)}
	}
	return result, nil
}

// emitStringCharAt extracts the character at a runtime index and returns it
// as a new heap-allocated two-byte string: { char, '\0' }.
func (e *Emitter) emitStringCharAt(strPtr string, indexExpr ast.Expression) (Value, error) {
	idxVal, err := e.emitExpr(indexExpr)
	if err != nil {
		return Value{}, err
	}
	idxVal, err = e.coerceChecked(idxVal, TypeI64, indexExpr.GetPos(), "string index")
	if err != nil {
		return Value{}, err
	}

	charPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", charPtr, strPtr, idxVal.Ref))
	charVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", charVal, charPtr))

	buf := e.emitStringAlloc("1") // TDD-00120: 1-char length-prefixed string
	e.emitInstr(fmt.Sprintf("store i8 %s, ptr %s, align 1", charVal, buf))
	nullPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 1", nullPtr, buf))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", nullPtr))

	return Value{Ref: buf, Ty: TypePtr}, nil
}

func (e *Emitter) emitStringStaticCall(property string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch property {
	case "fromCharCode", "fromCodePoint":
		return e.emitStringFromCharCode(args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: String.%s is not supported", pos.Line, pos.Col, property)
}

// emitStringFromCharCode implements String.fromCharCode(c1, c2, ...) and
// String.fromCodePoint(c1, c2, ...) for the Basic Multilingual Plane.
// Each code is truncated to a single byte (i8) and stored consecutively.
func (e *Emitter) emitStringFromCharCode(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) == 0 {
		return Value{Ref: e.internString(""), Ty: TypePtr}, nil
	}
	n := int64(len(args))
	buf := e.emitStringAlloc(fmt.Sprintf("%d", n)) // TDD-00120: n-char length-prefixed
	for i, arg := range args {
		val, err := e.emitExpr(arg)
		if err != nil {
			return Value{}, err
		}
		// The code units must be numeric. A non-number argument (e.g. a
		// string, as in `String.fromCharCode("0")`) would otherwise reach
		// the `trunc i64 <ptr> to i8` below and emit invalid IR — this
		// compiler doesn't do JS's implicit string→number coercion, so
		// reject it cleanly at compile time instead. See ADR-00195.
		if !isNumberTy(val.Ty) {
			return Value{}, fmt.Errorf("%d:%d: String.fromCharCode/fromCodePoint expects numeric arguments, got a non-number (this compiler does not implicitly coerce)", arg.GetPos().Line, arg.GetPos().Col)
		}
		coerced := e.coerce(val, TypeI64)
		ch := e.freshReg()
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i8", ch, coerced.Ref))
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", slot, buf, i))
		e.emitInstr(fmt.Sprintf("store i8 %s, ptr %s, align 1", ch, slot))
	}
	nullSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", nullSlot, buf, n))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", nullSlot))
	return Value{Ref: buf, Ty: TypePtr}, nil
}

// emitStringRepeat implements s.repeat(count): returns a new string consisting
// of count copies of s concatenated together.
func (e *Emitter) emitStringRepeat(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: repeat takes exactly 1 argument", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	cntVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	cntVal, err = e.coerceChecked(cntVal, TypeI64, args[0].GetPos(), "repeat count")
	if err != nil {
		return Value{}, err
	}
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()

	sLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", sLen, objVal.Ref))
	totalLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %s", totalLen, sLen, cntVal.Ref))
	buf := e.emitStringAlloc(totalLen) // TDD-00120: length-prefixed

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("rep.cond")
	bodyL := e.freshLabel("rep.body")
	doneL := e.freshLabel("rep.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, cntVal.Ref))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	offset := e.freshReg()
	dst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %s", offset, idxVal, sLen))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", dst, buf, offset))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", dst, objVal.Ref, sLen))
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	nullPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", nullPtr, buf, totalLen))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", nullPtr))
	return Value{Ref: buf, Ty: TypePtr}, nil
}

// emitStringAt implements s.at(index): returns the character at the given index
// with negative-index support. An out-of-range index yields a real `undefined`
// (Node types `.at()` `string | undefined`, TDD-00187), not `""` — the absent
// value is the null pointer with the static type flagged Nullable|IsUndefined.
// Must mirror inferExprType's `at` case (undefinedableElem(TypePtr)).
func (e *Emitter) emitStringAt(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: at takes exactly 1 argument", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	sLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", sLen, objVal.Ref))
	idxRaw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// `.at` index semantics (NOT slice-clamping): a negative index adds the
	// length once; anything still outside [0, len) is a miss → undefined. (Using
	// slice-normalization here wrongly clamped `"hi".at(-9)` to index 0.)
	idx, err := e.coerceNumArgRef(idxRaw, TypeI64, args[0].GetPos(), "at index")
	if err != nil {
		return Value{}, err
	}
	neg := e.freshReg()
	adjusted := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, idx))
	plusLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", plusLen, idx, sLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", adjusted, neg, plusLen, idx))
	geZero := e.freshReg()
	ltLen := e.freshReg()
	inBounds := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", geZero, adjusted))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", ltLen, adjusted, sLen))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", inBounds, geZero, ltLen))
	safeIdx := e.freshReg()
	sliceLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", safeIdx, inBounds, adjusted))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 1, i64 0", sliceLen, inBounds))
	char := e.emitStringExtract(objVal.Ref, safeIdx, sliceLen)
	// null when out of range → renders/compares as undefined
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr null", res, inBounds, char.Ref))
	return e.wrapUndefinedable(Value{Ref: res, Ty: TypePtr}, inBounds), nil
}

// emitStringPad is the shared implementation for padStart and padEnd.
// If padStart is true, the fill goes before the string; otherwise after.
func (e *Emitter) emitStringPad(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos, padStart bool) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: pad takes 1 or 2 arguments", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	targetLenRaw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	targetLen, err := e.coerceNumArgRef(targetLenRaw, TypeI64, args[0].GetPos(), "pad target length")
	if err != nil {
		return Value{}, err
	}
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()

	sLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", sLen, objVal.Ref))

	// Resolve fill string first: default is a space. Must happen before padLen
	// is finalized, since an empty fill string means "no padding" in JS (and,
	// not incidentally, avoids a srem-by-zero when indexing into it below).
	var fillPtr, fillPLen string
	if len(args) == 2 {
		fv, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		fillPtr = fv.Ref
		fLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", fLen, fillPtr))
		fillPLen = fLen
	} else {
		fillPtr = e.internString(" ")
		fillPLen = "1"
	}

	// padLen = fillPLen == 0 ? 0 : max(0, targetLen - sLen)
	rawPad := e.freshReg()
	isNeg := e.freshReg()
	nonEmptyPad := e.freshReg()
	isEmptyFill := e.freshReg()
	padLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", rawPad, targetLen, sLen))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNeg, rawPad))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", nonEmptyPad, isNeg, rawPad))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isEmptyFill, fillPLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", padLen, isEmptyFill, nonEmptyPad))

	effectiveLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", effectiveLen, padLen, sLen))
	buf := e.emitStringAlloc(effectiveLen) // TDD-00120: length-prefixed

	// Fill loop: for j = 0; j < padLen; j++ { buf[dstOff+j] = fillStr[j % fillPLen] }
	var fillDst string // where in buf to write the pad
	var strDst string  // where in buf to copy the original string
	if padStart {
		fillDst = buf
		strDst = "" // computed after loop
	} else {
		fillDst = "" // computed below
		strDst = buf
	}

	if !padStart {
		// Copy string first, then fill after.
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", strDst, objVal.Ref, sLen))
		// fillDst = buf + sLen
		tmp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", tmp, buf, sLen))
		fillDst = tmp
	}

	jAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", jAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", jAlloca))
	fillCondL := e.freshLabel("padf.cond")
	fillBodyL := e.freshLabel("padf.body")
	fillDoneL := e.freshLabel("padf.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", fillCondL))
	e.emitLabel(fillCondL)
	jVal := e.freshReg()
	fDone := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", jVal, jAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", fDone, jVal, padLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", fDone, fillDoneL, fillBodyL))
	e.emitLabel(fillBodyL)
	modIdx := e.freshReg()
	srcGep := e.freshReg()
	srcChar := e.freshReg()
	dstGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = srem i64 %s, %s", modIdx, jVal, fillPLen))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", srcGep, fillPtr, modIdx))
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", srcChar, srcGep))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", dstGep, fillDst, jVal))
	e.emitInstr(fmt.Sprintf("store i8 %s, ptr %s, align 1", srcChar, dstGep))
	jNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", jNext, jVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", jNext, jAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", fillCondL))
	e.emitLabel(fillDoneL)

	if padStart {
		// String goes after the pad.
		tmp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", tmp, buf, padLen))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", tmp, objVal.Ref, sLen))
	}

	nullGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", nullGep, buf, effectiveLen))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", nullGep))
	return Value{Ref: buf, Ty: TypePtr}, nil
}

func (e *Emitter) emitStringPadStart(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	return e.emitStringPad(mem, args, pos, true)
}

func (e *Emitter) emitStringPadEnd(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	return e.emitStringPad(mem, args, pos, false)
}

// emitNumberToFixed implements n.toFixed(digits): formats the number with
// exactly digits decimal places and returns a string.
func (e *Emitter) emitNumberToFixed(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: toFixed takes 0 or 1 arguments", pos.Line, pos.Col)
	}
	numVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	// Convert to double.
	var dblReg string
	if numVal.Ty.IR == "double" {
		dblReg = numVal.Ref
	} else {
		dblReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sitofp %s %s to double", dblReg, numVal.Ty.IR, numVal.Ref))
	}
	// The digits argument is optional and defaults to 0 (real JS: `(3.7).toFixed()`
	// is `"4"`).
	digitsI32 := "0"
	if len(args) == 1 {
		digitsVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		digitsI64, err := e.coerceNumArgRef(digitsVal, TypeI64, args[0].GetPos(), "toFixed digits")
		if err != nil {
			return Value{}, err
		}
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", reg, digitsI64))
		digitsI32 = reg
	}
	e.ensureSprintf()
	buf := e.emitStringScratch(64) // TDD-00120: length-prefixed, finalized below
	fmtPtr := e.internString("%.*f")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i32 %s, double %s)", buf, fmtPtr, digitsI32, dblReg))
	e.emitStringFinalizeLen(buf)
	return Value{Ref: buf, Ty: TypePtr}, nil
}

// numberToDouble evaluates mem.Object and widens it to a double, the shared
// first step every Number.prototype numeric-formatting method needs.
func (e *Emitter) numberToDouble(mem *ast.MemberExpression) (string, error) {
	numVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return "", err
	}
	if numVal.Ty.IR == "double" {
		return numVal.Ref, nil
	}
	dblReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp %s %s to double", dblReg, numVal.Ty.IR, numVal.Ref))
	return dblReg, nil
}

// emitNumberToExponential implements Number.prototype.toExponential(digits?).
// With a fractionDigits argument, sprintf's %e gives the fixed-precision form;
// the exponent is normalized to JS's minimum-digit form ("1.23e+03" → "1.23e+3")
// by __kml_strip_exp_zeros. With NO argument, the result uses as many mantissa
// digits as needed to round-trip the value uniquely (ECMAScript 21.1.3.3, the
// "f is undefined" path) — delegated to __kml_dtoa_exp, which reuses dtoa's
// shortest-precision loop.
// emitNumberToLocaleString implements Number.prototype.toLocaleString() with no
// argument — the en-US default (thousands grouping, max 3 fraction digits). A
// locale/options argument is an Intl feature (out of scope), rejected cleanly.
func (e *Emitter) emitNumberToLocaleString(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: toLocaleString() locale/options arguments are an Intl feature (not supported); the no-argument form gives the en-US default", pos.Line, pos.Col)
	}
	dblReg, err := e.numberToDouble(mem)
	if err != nil {
		return Value{}, err
	}
	e.ensureDtoa()
	buf := e.emitStringScratch(512)
	e.emitInstr(fmt.Sprintf("call void @__kml_num_tolocalestring(ptr %s, double %s)", buf, dblReg))
	e.emitStringFinalizeLen(buf)
	return Value{Ref: buf, Ty: TypePtr}, nil
}

func (e *Emitter) emitNumberToExponential(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: toExponential takes 0 or 1 arguments", pos.Line, pos.Col)
	}
	dblReg, err := e.numberToDouble(mem)
	if err != nil {
		return Value{}, err
	}
	if len(args) == 0 {
		// Shortest round-trip exponential form.
		e.ensureDtoa()
		buf := e.emitStringScratch(40)
		e.emitInstr(fmt.Sprintf("call void @__kml_dtoa_exp(ptr %s, double %s)", buf, dblReg))
		e.emitStringFinalizeLen(buf)
		return Value{Ref: buf, Ty: TypePtr}, nil
	}
	digitsVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	digitsRef, err := e.coerceNumArgRef(digitsVal, TypeI64, args[0].GetPos(), "toExponential digits")
	if err != nil {
		return Value{}, err
	}
	digitsI32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", digitsI32, digitsRef))
	e.ensureSprintf()
	buf := e.emitStringScratch(64) // TDD-00120: length-prefixed, finalized below
	fmtPtr := e.internString("%.*e")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i32 %s, double %s)", buf, fmtPtr, digitsI32, dblReg))
	// Normalize the exponent to JS's minimum-digit form ("1.23e+03" -> "1.23e+3").
	e.ensureStripExpZeros()
	e.emitInstr(fmt.Sprintf("call i64 @__kml_strip_exp_zeros(ptr %s)", buf))
	e.emitStringFinalizeLen(buf)
	return Value{Ref: buf, Ty: TypePtr}, nil
}

// emitNumberToPrecision implements Number.prototype.toPrecision(precision),
// via sprintf's %#g conversion (the '#' flag keeps trailing zeros, matching
// JS's "always show exactly `precision` significant digits" rule — plain
// %g strips them). One cleanup %g doesn't do on its own: when the rounded
// value has no fractional digits left, %#g still emits a bare trailing "."
// (e.g. "1235.") where JS never would ("1235") — trimmed below by checking
// the buffer's last byte after formatting. Known, documented
// (docs/status/NUMBER-MATH.md) deviation from real JS for the exponential-notation branch: same 2-digit
// zero-padded exponent as toExponential, and exact halfway-tie rounding may
// differ from JS's spec algorithm (glibc uses round-half-to-even; JS does
// not) — both cosmetic/rare-edge-case, not silent data corruption.
func (e *Emitter) emitNumberToPrecision(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: toPrecision takes 0 or 1 arguments", pos.Line, pos.Col)
	}
	// With no precision argument, `x.toPrecision()` is exactly `String(x)` (real
	// JS) — route through the number's default toString rendering.
	if len(args) == 0 {
		numVal, err := e.emitExpr(mem.Object)
		if err != nil {
			return Value{}, err
		}
		return e.emitValueToString(numVal)
	}
	dblReg, err := e.numberToDouble(mem)
	if err != nil {
		return Value{}, err
	}
	digitsVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	digitsRef, err := e.coerceNumArgRef(digitsVal, TypeI64, args[0].GetPos(), "toPrecision digits")
	if err != nil {
		return Value{}, err
	}
	digitsI32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", digitsI32, digitsRef))
	e.ensureSprintf()
	buf := e.emitStringScratch(64) // TDD-00120: length-prefixed, finalized below
	fmtPtr := e.internString("%#.*g")
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i32 %s, double %s)", lenReg, buf, fmtPtr, digitsI32, dblReg))

	// Normalize the exponent to JS's minimum-digit form ("1.2e+05" -> "1.2e+5");
	// returns the post-strip length used for the trailing-'.' trim below.
	e.ensureStripExpZeros()
	lenI64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_strip_exp_zeros(ptr %s)", lenI64, buf))

	// Trim a bare trailing '.' left by the '#' flag when no fractional
	// digits remain (e.g. "1235." -> "1235").
	lastIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", lastIdx, lenI64))
	lastPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", lastPtr, buf, lastIdx))
	lastCh := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", lastCh, lastPtr))
	isDot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, 46", isDot, lastCh)) // 46 == '.'
	trimL := e.freshLabel("toprecision.trim")
	doneL := e.freshLabel("toprecision.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isDot, trimL, doneL))
	e.emitLabel(trimL)
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", lastPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)

	e.emitStringFinalizeLen(buf) // TDD-00120: length after the optional '.' trim
	return Value{Ref: buf, Ty: TypePtr}, nil
}

// emitNumberToStringRadix implements Number.prototype.toString(radix?).
//
// The default-radix (and explicit radix 10) case is exactly `String(x)` — the
// faithful shortest-round-trip decimal, fractional part included — so it is
// delegated straight to emitValueToString (fixing the prior bug where a
// fractional receiver was truncated to its integer part, e.g. `(255.5)
// .toString()` returned `"255"`).
//
// For any other base there is no C library conversion, so the integer part is
// hand-rolled (the classic repeated-urem/udiv digit loop, see
// emitNumberRadixIntPart) and — new in ADR-00566 — the fractional part is
// expanded by the repeated-multiply-by-radix algorithm (emitNumberRadixFrac).
// For a power-of-two base the double multiply is exact, so the expansion is
// bit-exact to V8; for other bases the double arithmetic can differ from V8's
// exact-bignum result in the trailing digits and a non-terminating fraction is
// capped at 1100 digits (both disclosed in docs/status/NUMBER-MATH.md).
//
// The radix is validated to 2..36 (a RangeError otherwise — ADR-00552).
func (e *Emitter) emitNumberToStringRadix(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: toString takes at most 1 argument", pos.Line, pos.Col)
	}
	numVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	// No radix argument: identical to String(x) — the shortest decimal.
	if len(args) == 0 {
		return e.emitValueToString(numVal)
	}
	radixVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	radixRef, err := e.coerceNumArgRef(radixVal, TypeI64, args[0].GetPos(), "toString radix")
	if err != nil {
		return Value{}, err
	}
	// Real JS throws a RangeError for a radix outside 2..36 (ADR-00552).
	e.ensureExceptionHelpers()
	e.ensureStrHeaderRuntime()
	lo := e.freshReg()
	hi := e.freshReg()
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 2", lo, radixRef))
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 36", hi, radixRef))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", bad, lo, hi))
	badL := e.freshLabel("tostr.badradix")
	okL := e.freshLabel("tostr.okradix")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
	e.emitLabel(badL)
	msg := e.internString("toString() radix must be between 2 and 36")
	errObj := e.buildErrorObj(errorKindIDs["RangeError"], msg, e.internString("RangeError"))
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errObj))
	e.emitTerminator("unreachable")
	e.emitLabel(okL)

	// Radix 10 at runtime is still just String(x) (shortest decimal); any other
	// base runs the hand-rolled int+frac expansion.
	dbl := e.coerce(numVal, TypeF64).Ref
	isTen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 10", isTen, radixRef))
	resPtr, err := e.emitStrBranch(isTen,
		func() (string, error) {
			v, err := e.emitValueToString(numVal)
			return v.Ref, err
		},
		func() (string, error) {
			v, err := e.emitNumberRadixIntFrac(dbl, radixRef)
			return v.Ref, err
		})
	if err != nil {
		return Value{}, err
	}
	return Value{Ref: resPtr, Ty: TypePtr}, nil
}

// emitNumberRadixIntFrac renders `dbl` in base `radixRef` (2..36, never 10 —
// the caller delegates that) as an integer-part string plus, when the receiver
// has a fractional part, a "."-prefixed fractional expansion (ADR-00566).
func (e *Emitter) emitNumberRadixIntFrac(dbl, radixRef string) (Value, error) {
	e.ensureMathFuncs()
	// Integer part (truncated toward zero, keeping the sign) via the digit loop.
	intI := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", intI, dbl))
	intStr := e.emitNumberRadixIntPart(intI, radixRef)
	// Fractional part in [0,1): abs(dbl) - trunc(abs(dbl)).
	absD := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @fabs(double %s)", absD, dbl))
	intF := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @trunc(double %s)", intF, absD))
	frac := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fsub double %s, %s", frac, absD, intF))
	hasFrac := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp ogt double %s, 0.0", hasFrac, frac))
	fracStr := e.emitNumberRadixFrac(frac, radixRef)
	// Append the fractional string only when there is a fractional part.
	res, err := e.emitStrBranch(hasFrac,
		func() (string, error) {
			c, err := e.emitStringConcat(Value{Ref: intStr, Ty: TypePtr}, Value{Ref: fracStr, Ty: TypePtr})
			return c.Ref, err
		},
		func() (string, error) { return intStr, nil })
	if err != nil {
		return Value{}, err
	}
	return Value{Ref: res, Ty: TypePtr}, nil
}

// emitNumberRadixFrac expands a fractional value in [0,1) to a "."-prefixed
// digit string in base `radixRef` by the repeated-multiply-by-radix algorithm,
// capped at 1100 digits for a non-terminating expansion (ADR-00566).
func (e *Emitter) emitNumberRadixFrac(frac, radixRef string) string {
	radixF := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", radixF, radixRef))
	// 1 for '.', 1100 fractional digits, 1 for the NUL.
	buf := e.emitStringScratch(1102)
	e.emitInstr(fmt.Sprintf("store i8 46, ptr %s, align 1", buf)) // '.'
	fidx := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", fidx))
	e.emitInstr(fmt.Sprintf("store i64 1, ptr %s, align 8", fidx))
	fAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca double, align 8", fAlloca))
	e.emitInstr(fmt.Sprintf("store double %s, ptr %s, align 8", frac, fAlloca))

	condL := e.freshLabel("frac.cond")
	bodyL := e.freshLabel("frac.body")
	doneL := e.freshLabel("frac.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	fCur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load double, ptr %s, align 8", fCur, fAlloca))
	idxCur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxCur, fidx))
	moreFrac := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp ogt double %s, 0.0", moreFrac, fCur))
	underCap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 1101", underCap, idxCur)) // 1 ('.') + 1100 digits
	keepGoing := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", keepGoing, moreFrac, underCap))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", keepGoing, bodyL, doneL))

	e.emitLabel(bodyL)
	scaled := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fmul double %s, %s", scaled, fCur, radixF))
	digitF := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @trunc(double %s)", digitF, scaled))
	digitI := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fptoui double %s to i64", digitI, digitF))
	newFrac := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fsub double %s, %s", newFrac, scaled, digitF))
	e.emitInstr(fmt.Sprintf("store double %s, ptr %s, align 8", newFrac, fAlloca))
	isDecDigit := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %s, 10", isDecDigit, digitI))
	decChar := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 48", decChar, digitI))
	alphaChar := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 87", alphaChar, digitI))
	charVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", charVal, isDecDigit, decChar, alphaChar))
	char8 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i8", char8, charVal))
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", slot, buf, idxCur))
	e.emitInstr(fmt.Sprintf("store i8 %s, ptr %s, align 1", char8, slot))
	nextIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nextIdx, idxCur))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nextIdx, fidx))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	finalLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalLen, fidx))
	nul := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", nul, buf, finalLen))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", nul))
	e.emitStringSetLen(buf, finalLen)
	return buf
}

// (integer digit loop, extracted from the original emitNumberToStringRadix)
func (e *Emitter) emitNumberRadixIntPart(nValRef, radixRef string) string {
	e.ensureMalloc()
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 70)", buf))
	nullPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 69", nullPtr, buf))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", nullPtr))

	isNeg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNeg, nValRef))
	negN := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 0, %s", negN, nValRef))
	uVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", uVal, isNeg, negN, nValRef))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 69, ptr %s, align 8", idxAlloca))
	uAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", uAlloca))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", uVal, uAlloca))

	zeroL := e.freshLabel("tostr.zero")
	condL := e.freshLabel("tostr.cond")
	bodyL := e.freshLabel("tostr.body")
	negL := e.freshLabel("tostr.neg")
	mergeL := e.freshLabel("tostr.merge")
	doneL := e.freshLabel("tostr.done")

	// u == 0 never enters the digit loop below, so it needs its own literal
	// "0" digit written up front.
	isZero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isZero, uVal))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isZero, zeroL, condL))

	e.emitLabel(zeroL)
	e.writeDigitAndDecrement(buf, idxAlloca, "48") // '0'
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(condL)
	uC := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", uC, uAlloca))
	notZero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", notZero, uC))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", notZero, bodyL, doneL))

	e.emitLabel(bodyL)
	uB := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", uB, uAlloca))
	digit := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = urem i64 %s, %s", digit, uB, radixRef))
	quotient := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = udiv i64 %s, %s", quotient, uB, radixRef))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", quotient, uAlloca))

	isDecDigit := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %s, 10", isDecDigit, digit))
	decChar := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 48", decChar, digit)) // '0'..'9'
	alphaChar := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 87", alphaChar, digit)) // 'a'..'z' ('a'-10 == 87)
	charVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", charVal, isDecDigit, decChar, alphaChar))
	e.writeDigitAndDecrement(buf, idxAlloca, charVal)
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNeg, negL, mergeL))
	e.emitLabel(negL)
	e.writeDigitAndDecrement(buf, idxAlloca, "45") // '-'
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	finalIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalIdx, idxAlloca))
	startIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", startIdx, finalIdx))
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", result, buf, startIdx))
	// TDD-00120: the digits were filled backward into an interior slice of `buf`,
	// ending at index 69 (the first digit overwrote the preset NUL), so `result`
	// is not a header-prefixed base. Copy it out into a proper length-prefixed
	// string (data length = 70 - startIdx). This also gives it a real NUL
	// terminator, fixing the original path's 1-byte over-read past buf[69].
	e.ensureMemcpy()
	rlen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 70, %s", rlen, startIdx))
	dst := e.emitStringAlloc(rlen)
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", dst, result, rlen))
	dnull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", dnull, dst, rlen))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", dnull))
	return dst
}

// writeDigitAndDecrement stores an i64-valued byte (truncated to i8) at
// buf[*idxAlloca], then decrements *idxAlloca — the shared "write one
// character going backward" step emitNumberToStringRadix's three digit-
// producing branches (zero, a real digit, the minus sign) all need.
func (e *Emitter) writeDigitAndDecrement(buf, idxAlloca, charVal64 string) {
	idx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idx, idxAlloca))
	pos := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", pos, buf, idx))
	char8 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i8", char8, charVal64))
	e.emitInstr(fmt.Sprintf("store i8 %s, ptr %s, align 1", char8, pos))
	newIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", newIdx, idx))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newIdx, idxAlloca))
}
