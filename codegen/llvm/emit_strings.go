// emit_strings.go — string method emission (concat, comparison, slice, indexOf,
// includes, charCodeAt, trim, toUpperCase, toLowerCase, startsWith, endsWith,
// replace, split, String.fromCharCode, etc.) and the isStringTy predicate.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// isStringTy returns true for a plain string (ptr, not object/array/closure).
func isStringTy(ty Type) bool {
	return ty.IR == "ptr" && !ty.IsObject && !ty.IsArray && !ty.IsFunc && !ty.IsBigInt
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
	isNull := e.freshReg()
	safe := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", safe, isNull, e.internString("null"), v.Ref))
	return Value{Ref: safe, Ty: TypePtr}
}

// emitStringConcat concatenates two string (ptr) values and returns a new
// heap string. Found crashing (not just wrong) on a null operand — real JS
// stringifies `null` as "null" in a `+` (`"x" + null === "xnull"`), but this
// called strlen()/memcpy() on the raw pointer unconditionally, so any
// nullable-typed string that was actually null (an optional param, `T |
// null`, a missing Map/collection lookup, ...) segfaulted here instead.
func (e *Emitter) emitStringConcat(left, right Value) (Value, error) {
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	left = e.emitStringNullToLiteral(left)
	right = e.emitStringNullToLiteral(right)
	n1 := e.freshReg()
	n2 := e.freshReg()
	total := e.freshReg()
	total1 := e.freshReg()
	buf := e.freshReg()
	dst := e.freshReg()
	n2p1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", n1, left.Ref))
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", n2, right.Ref))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", total, n1, n2))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", total1, total))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", buf, total1))
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
		e.ensureStrcmp()
		cmp := e.freshReg()
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", cmp, left.Ref, right.Ref))
		iop := map[string]string{
			"==": "eq", "===": "eq", "!=": "ne", "!==": "ne",
			"<": "slt", ">": "sgt", "<=": "sle", ">=": "sge",
		}[op]
		e.emitInstr(fmt.Sprintf("%s = icmp %s i32 %s, 0", result, iop, cmp))
		return Value{Ref: result, Ty: TypeBool}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: operator '%s' is not supported for strings", pos.Line, pos.Col, op)
}

// emitStringExtract allocates a new heap string containing src[start..start+length)
// and returns a ptr value. startReg and lenReg are i64 register references.
func (e *Emitter) emitStringExtract(srcRef, startReg, lenReg string) Value {
	allocSize := e.freshReg()
	buf := e.freshReg()
	srcPtr := e.freshReg()
	nullSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", allocSize, lenReg))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", buf, allocSize))
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
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", sLen, objVal.Ref))

	startRaw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	startN := e.emitNormalizeSliceIdx(e.coerce(startRaw, TypeI64).Ref, sLen)

	var endN string
	if len(args) == 2 {
		endRaw, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		endN = e.emitNormalizeSliceIdx(e.coerce(endRaw, TypeI64).Ref, sLen)
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
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", sLen, objVal.Ref))

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
	startC := clamp(e.coerce(startRaw, TypeI64))

	var endC string
	if len(args) == 2 {
		endRaw, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		endC = clamp(e.coerce(endRaw, TypeI64))
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

// emitStringIndexOf implements s.indexOf(needle): returns the byte offset of the
// first occurrence, or -1 if not found. Uses strstr + ptrtoint arithmetic + select.
func (e *Emitter) emitStringIndexOf(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: indexOf takes exactly 1 argument", pos.Line, pos.Col)
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
	e.ensureStrstr()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @strstr(ptr %s, ptr %s)", result, objVal.Ref, needleVal.Ref))
	found := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", found, result))
	haystackInt := e.freshReg()
	resultInt := e.freshReg()
	offset := e.freshReg()
	final := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", haystackInt, objVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", resultInt, result))
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", offset, resultInt, haystackInt))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 -1", final, found, offset))
	return Value{Ref: final, Ty: TypeI64}, nil
}

// emitStringIncludes implements s.includes(needle): returns true iff needle appears in s.
func (e *Emitter) emitStringIncludes(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: includes takes exactly 1 argument", pos.Line, pos.Col)
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
	e.ensureStrstr()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @strstr(ptr %s, ptr %s)", result, objVal.Ref, needleVal.Ref))
	found := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", found, result))
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
	idxVal = e.coerce(idxVal, TypeI64)
	charPtr := e.freshReg()
	charByte := e.freshReg()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", charPtr, strVal.Ref, idxVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", charByte, charPtr))
	e.emitInstr(fmt.Sprintf("%s = zext i8 %s to i64", result, charByte))
	return Value{Ref: result, Ty: TypeI64}, nil
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
	idxVal := e.coerce(idxRaw, TypeI64)
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	sLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", sLen, objVal.Ref))
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
// is no surrogate-pair/multi-byte code point decoding here, so this is
// exactly charCodeAt's byte value under a second name. Correct for
// ASCII/Latin-1 input (where a "code point" and a "char code" are the same
// number); a documented scope narrowing for anything requiring real Unicode
// decoding, consistent with this compiler having no Unicode infrastructure
// at all yet.
func (e *Emitter) emitStringCodePointAt(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: codePointAt expects 1 argument", pos.Line, pos.Col)
	}
	return e.emitStringCharCodeAt(mem, args, pos)
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
	if e.inferExprType(args[0]).IsRegExp {
		strVal, err := e.emitExpr(mem.Object)
		if err != nil {
			return Value{}, err
		}
		if !isStringTy(strVal.Ty) {
			return Value{}, fmt.Errorf("%d:%d: search is only supported on strings", pos.Line, pos.Col)
		}
		strVal = e.coerce(strVal, TypePtr)
		regexVal, err := e.emitExpr(args[0])
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
	e.ensureStrcmp()
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", raw, objVal.Ref, otherVal.Ref))
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
	return Value{Ref: result64, Ty: TypeI64}, nil
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
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: startsWith takes exactly 1 argument", pos.Line, pos.Col)
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
	e.ensureStrlen()
	e.ensureStrncmp()
	prefixLen := e.freshReg()
	cmp := e.freshReg()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", prefixLen, prefixVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = call i32 @strncmp(ptr %s, ptr %s, i64 %s)", cmp, objVal.Ref, prefixVal.Ref, prefixLen))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", result, cmp))
	return Value{Ref: result, Ty: TypeBool}, nil
}

func (e *Emitter) emitStringEndsWith(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: endsWith takes exactly 1 argument", pos.Line, pos.Col)
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
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", sLen, objVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", sufLen, suffixVal.Ref))
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
	searchVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	repVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	e.ensureStringReplace()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_replace(ptr %s, ptr %s, ptr %s)", result, objVal.Ref, searchVal.Ref, repVal.Ref))
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
	searchVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	repVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	e.ensureStringReplaceAll()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_replace_all(ptr %s, ptr %s, ptr %s)", result, objVal.Ref, searchVal.Ref, repVal.Ref))
	return Value{Ref: result, Ty: TypePtr}, nil
}

func (e *Emitter) emitStringSplit(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: split takes exactly 1 argument", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(objVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: split is only supported on strings", pos.Line, pos.Col)
	}
	if e.inferExprType(args[0]).IsRegExp {
		objVal = e.coerce(objVal, TypePtr)
		regexVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		return e.emitRegexSplit(objVal, regexVal), nil
	}
	sepVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	e.ensureStringSplit()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_split(ptr %s, ptr %s)", result, objVal.Ref, sepVal.Ref))
	return Value{Ref: result, Ty: ArrayOf(TypePtr)}, nil
}

// emitStringCharAt extracts the character at a runtime index and returns it
// as a new heap-allocated two-byte string: { char, '\0' }.
func (e *Emitter) emitStringCharAt(strPtr string, indexExpr ast.Expression) (Value, error) {
	idxVal, err := e.emitExpr(indexExpr)
	if err != nil {
		return Value{}, err
	}
	idxVal = e.coerce(idxVal, TypeI64)

	charPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", charPtr, strPtr, idxVal.Ref))
	charVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", charVal, charPtr))

	e.ensureMalloc()
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 2)", buf))
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
	e.ensureMalloc()
	n := int64(len(args))
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", buf, n+1))
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
	cntVal = e.coerce(cntVal, TypeI64)
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()

	sLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", sLen, objVal.Ref))
	totalLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %s", totalLen, sLen, cntVal.Ref))
	bufSize := e.freshReg()
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", bufSize, totalLen))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", buf, bufSize))

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
// with negative-index support. Returns "" for out-of-range indices.
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
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", sLen, objVal.Ref))
	idxRaw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	startN := e.emitNormalizeSliceIdx(e.coerce(idxRaw, TypeI64).Ref, sLen)
	inBounds := e.freshReg()
	sliceLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", inBounds, startN, sLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 1, i64 0", sliceLen, inBounds))
	return e.emitStringExtract(objVal.Ref, startN, sliceLen), nil
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
	targetLen := e.coerce(targetLenRaw, TypeI64).Ref
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()

	sLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", sLen, objVal.Ref))

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
		e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", fLen, fillPtr))
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
	bufSize := e.freshReg()
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", bufSize, effectiveLen))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", buf, bufSize))

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
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: toFixed takes exactly 1 argument", pos.Line, pos.Col)
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
	digitsVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	digitsI64 := e.coerce(digitsVal, TypeI64).Ref
	digitsI32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", digitsI32, digitsI64))
	e.ensureSprintf()
	e.ensureMalloc()
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 64)", buf))
	fmtPtr := e.internString("%.*f")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i32 %s, double %s)", buf, fmtPtr, digitsI32, dblReg))
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

// emitNumberToExponential implements Number.prototype.toExponential(digits),
// via sprintf's own %e conversion. Known deviation from real JS, documented
// in docs/status/NUMBER-MATH.md: the exponent is always rendered with a sign and 2 digits
// (e.g. "1.23e+03"), whereas real JS always uses the minimum digit count
// ("1.23e+3") — cosmetic only, the numeric value itself is exact.
func (e *Emitter) emitNumberToExponential(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: toExponential takes exactly 1 argument", pos.Line, pos.Col)
	}
	dblReg, err := e.numberToDouble(mem)
	if err != nil {
		return Value{}, err
	}
	digitsVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	digitsI32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", digitsI32, e.coerce(digitsVal, TypeI64).Ref))
	e.ensureSprintf()
	e.ensureMalloc()
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 64)", buf))
	fmtPtr := e.internString("%.*e")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i32 %s, double %s)", buf, fmtPtr, digitsI32, dblReg))
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
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: toPrecision takes exactly 1 argument", pos.Line, pos.Col)
	}
	dblReg, err := e.numberToDouble(mem)
	if err != nil {
		return Value{}, err
	}
	digitsVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	digitsI32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", digitsI32, e.coerce(digitsVal, TypeI64).Ref))
	e.ensureSprintf()
	e.ensureMalloc()
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 64)", buf))
	fmtPtr := e.internString("%#.*g")
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i32 %s, double %s)", lenReg, buf, fmtPtr, digitsI32, dblReg))

	// Trim a bare trailing '.' left by the '#' flag when no fractional
	// digits remain (e.g. "1235." -> "1235").
	lenI64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sext i32 %s to i64", lenI64, lenReg))
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

	return Value{Ref: buf, Ty: TypePtr}, nil
}

// emitNumberToStringRadix implements Number.prototype.toString(radix?) —
// converts an integer to its digit-string representation in the given base
// (default 10, matching this compiler's ordinary number->string conversion
// for that case). Unlike the sprintf-based Number formatters above, no C
// library conversion exists for an arbitrary base, so this hand-rolls the
// classic repeated-urem/udiv digit loop, writing digits into a 70-byte
// buffer from the end backward (enough for a sign + 64 base-2 digits + a
// null terminator) so the final result can be returned as buf+startIdx with
// no memmove needed — the pre-set null terminator at the buffer's last byte
// is already in the right place regardless of how many digits end up
// written. A non-integer receiver is truncated to its integer part first —
// real JS's toString(radix) can expand fractional digits in the target
// base too, which this doesn't attempt (documented in docs/status/NUMBER-MATH.md); radix is
// trusted, not validated (must be 2-36 per spec — an out-of-range radix is
// undefined behavior here, same trust-the-caller stance other built-ins in
// this compiler already take for arguments there's no runtime check for).
func (e *Emitter) emitNumberToStringRadix(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: toString takes at most 1 argument", pos.Line, pos.Col)
	}
	numVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	nVal := e.coerce(numVal, TypeI64)

	radixRef := "10"
	if len(args) == 1 {
		radixVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		radixRef = e.coerce(radixVal, TypeI64).Ref
	}

	e.ensureMalloc()
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 70)", buf))
	nullPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 69", nullPtr, buf))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", nullPtr))

	isNeg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNeg, nVal.Ref))
	negN := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 0, %s", negN, nVal.Ref))
	uVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", uVal, isNeg, negN, nVal.Ref))

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
	return Value{Ref: result, Ty: TypePtr}, nil
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
