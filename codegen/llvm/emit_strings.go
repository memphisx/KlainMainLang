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
		!ty.IsURLSearchParams && // a URLSearchParams is a pair-list handle, not a string (TDD-00203)
		!ty.IsFFIFunction // a bound native function is a function object (TDD-00229)
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
	// treats "." as the any-char pattern, not a literal dot), so a pattern is
	// compiled into a RegExp exactly as `new RegExp(String(pattern))` would —
	// special characters are interpreted (ADR-00548).
	strVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(strVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: search is only supported on strings", pos.Line, pos.Col)
	}
	strVal = e.coerce(strVal, TypePtr)
	regexVal, err := e.emitRegExpArg(args[0], "")
	if err != nil {
		return Value{}, err
	}
	return e.emitRegexSearch(strVal, regexVal), nil
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
	} else if sepTy := e.inferExprType(args[0]); sepTy.IsUndefined && !sepTy.Nullable {
		// An undefined separator leaves the string whole (it is not
		// ToString'd): the string runtime's split with a null separator.
		if _, err := e.emitExpr(args[0]); err != nil {
			return Value{}, err
		}
		e.ensureStringC()
		e.declareFn("__kml_String_split", "declare ptr @__kml_String_split(ptr noundef, ptr noundef, i1 zeroext, double noundef)")
		h := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_String_split(ptr %s, ptr null, i1 zeroext false, double 0.0)", h, e.coerce(objVal, TypePtr).Ref))
		result = e.arrayValueFromHeaderReg(h, ArrayOf(TypePtr))
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

// emitStringConcatMethod implements `str.concat(...values)`: the receiver
// followed by ToString of each argument. That is exactly what a template
// literal does with its substitutions (string hint — an object's `toString`
// wins over its `valueOf`, unlike `+`, whose default hint asks `valueOf`
// first), so the call is emitted as “ `${str}${a}${b}` “. A spread argument
// contributes ToString of each element: “ xs.map((c) => `${c}`).join("") “ —
// not a bare `join`, which renders a null/undefined element as "".
func (e *Emitter) emitStringConcatMethod(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	exprs := []ast.Expression{mem.Object}
	for _, a := range args {
		sp, ok := a.(*ast.SpreadElement)
		if !ok {
			exprs = append(exprs, a)
			continue
		}
		const elem = "__kml_concat_elem"
		each := ast.NewArrowFunction([]ast.Param{{Name: elem}}, nil,
			ast.NewTemplateLiteral([]string{"", ""}, []ast.Expression{ast.NewIdentifier(elem, pos)}, pos), nil, pos)
		mapped := ast.NewCallExpression(ast.NewMemberExpression(sp.Arg, "map", pos), []ast.Expression{each}, pos)
		exprs = append(exprs, ast.NewCallExpression(ast.NewMemberExpression(mapped, "join", pos),
			[]ast.Expression{ast.NewStringLiteral("", pos)}, pos))
	}
	return e.emitExpr(ast.NewTemplateLiteral(make([]string, len(exprs)+1), exprs, pos))
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
