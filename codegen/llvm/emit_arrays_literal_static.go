package llvm

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"KlainMainLang/ast"
)

// emit_arrays_literal_static.go — a literal-only array literal lowered to
// static data (ADR-01064).
//
// emitArrayLiteralData's per-element path emits a GEP + store per element,
// and for a nested element two mallocs and four stores — a 65 K-row
// `[string, string]` table (test262's string-upper-lower-mapping.js, 3.3 MB of
// source) came out as 49 MB of IR that clang never finished. When every
// element is a compile-time constant the buffer's *contents* are known
// here, so the literal becomes one `private constant` global holding the
// image of the backing buffer and one `malloc` + `memcpy` at runtime (the
// array stays an ordinary heap buffer: push/splice realloc it, so it can
// never point at the constant itself). A two-level table of constant rows
// (`T[][]`) is one flat constant plus a row-length table, and a runtime loop
// (`__kml_arr_lit_rows`) mints the per-row buffers and boxes — O(1) IR per
// table instead of O(rows).
//
// Only what is provably identical to the per-element path is folded: string,
// number (incl. a `-` prefix), boolean literals, and rows made of those. A
// number literal into an integer element type is folded only when the value
// is an exact integer that fits — anything else (a fraction, NaN, out of
// range) takes the runtime coercion so the saturating/modular semantics stay
// in one place. Anything else (a spread, an identifier, `any[]`, a nullable
// or flat-object element, a hole) leaves the literal on the general path.

// staticArrayLiteralMinLen is the flat-literal length from which the static
// image is used; below it the handful of stores is as small as the constant
// would be, and the general path stays the only one small tests exercise.
const staticArrayLiteralMinLen = 16

// constArrayElemOperand returns the LLVM constant operand for a literal
// element in a buffer of elemTy, or ok=false when the element is not a
// foldable literal for that element type.
func constArrayElemOperand(elem ast.Expression, elemTy Type) (string, bool) {
	if elemTy.IsArray || elemTy.IsDynamic || elemTy.Inline || elemTy.Nullable || elemTy.UnionMembers != nil || isNullableScalar(elemTy) {
		return "", false
	}
	switch ex := elem.(type) {
	case *ast.StringLiteral:
		if elemTy.IR != "ptr" || elemTy.IsObject || elemTy.IsTuple || elemTy.IsFunc || elemTy.IsMap || elemTy.IsSet || elemTy.IsClass || elemTy.IsPromise {
			return "", false
		}
		return "", true // interned by the caller (needs the emitter)
	case *ast.BooleanLiteral:
		if elemTy.IR != "i1" {
			return "", false
		}
		if ex.Value {
			return "1", true
		}
		return "0", true
	case *ast.NumberLiteral:
		return constNumberOperand(ex, false, elemTy)
	case *ast.UnaryExpression:
		n, isNum := ex.Arg.(*ast.NumberLiteral)
		if !ex.Prefix || ex.Op != "-" || !isNum {
			return "", false
		}
		return constNumberOperand(n, true, elemTy)
	}
	return "", false
}

// constNumberOperand folds a number literal (optionally negated) into an
// operand of elemTy: a double/float bit pattern, or an exact integer.
func constNumberOperand(n *ast.NumberLiteral, neg bool, elemTy Type) (string, bool) {
	if n.IsBigInt {
		return "", false
	}
	v := n.Value
	var f float64
	if len(v) >= 2 && v[0] == '0' && (v[1]|32 == 'x' || v[1]|32 == 'b' || v[1]|32 == 'o') {
		i, err := strconv.ParseInt(v, 0, 64)
		if err != nil {
			return "", false
		}
		f = float64(i)
	} else {
		var err error
		if f, err = strconv.ParseFloat(v, 64); err != nil {
			return "", false
		}
	}
	if neg {
		f = -f
	}
	switch {
	case elemTy.IR == "double":
		return llvmDoubleLit(f), true
	case elemTy.IR == "float":
		// LLVM spells a float constant as the double bit pattern of the
		// (exactly representable) rounded value.
		return llvmDoubleLit(float64(float32(f))), true
	case elemTy.IsInteger() && elemTy.IR != "i1":
		if f != math.Trunc(f) || math.IsInf(f, 0) || math.IsNaN(f) {
			return "", false
		}
		bits := typeBits(elemTy.IR)
		if bits <= 0 || bits > 64 {
			return "", false
		}
		if elemTy.Signed {
			lo, hi := -math.Ldexp(1, bits-1), math.Ldexp(1, bits-1)-1
			if f < lo || f > hi {
				return "", false
			}
			return strconv.FormatInt(int64(f), 10), true
		}
		if f < 0 || f > math.Ldexp(1, bits)-1 {
			return "", false
		}
		return strconv.FormatUint(uint64(f), 10), true
	}
	return "", false
}

// constArrayElemRef is constArrayElemOperand with string literals interned.
func (e *Emitter) constArrayElemRef(elem ast.Expression, elemTy Type) (string, bool) {
	op, ok := constArrayElemOperand(elem, elemTy)
	if !ok {
		return "", false
	}
	if s, isStr := elem.(*ast.StringLiteral); isStr {
		return e.internString(s.Value), true
	}
	return op, true
}

// constArrayImage renders the elements of lit as an `[N x T]` constant
// initializer, or ok=false when any element is not a foldable literal.
func (e *Emitter) constArrayImage(elems []ast.Expression, elemTy Type) (string, bool) {
	for _, elem := range elems {
		if _, ok := constArrayElemOperand(elem, elemTy); !ok {
			return "", false
		}
	}
	var sb strings.Builder
	sb.WriteString("[")
	for i, elem := range elems {
		ref, _ := e.constArrayElemRef(elem, elemTy)
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(elemTy.IR)
		sb.WriteString(" ")
		sb.WriteString(ref)
	}
	sb.WriteString("]")
	return sb.String(), true
}

// newConstGlobal emits a private constant global of the given LLVM type and
// initializer, returning its name.
func (e *Emitter) newConstGlobal(llTy, init string) string {
	name := fmt.Sprintf("@.arrlit%d", e.arrLitIdx)
	e.arrLitIdx++
	e.emitGlobal(fmt.Sprintf("%s = private unnamed_addr constant %s %s, align 8", name, llTy, init))
	return name
}

// tryEmitStaticArrayLiteral is emitArrayLiteralData's fast path: when lit is
// a literal-only flat array of at least staticArrayLiteralMinLen elements, or
// a table whose every row is a literal-only flat array, the malloc'd backing
// buffer is built from a static image. handled=false means "not applicable —
// take the general path"; nothing has been emitted in that case.
func (e *Emitter) tryEmitStaticArrayLiteral(lit *ast.ArrayLiteral, elemTy Type) (dataReg string, handled bool) {
	n := len(lit.Elements)
	if n == 0 {
		return "", false
	}
	for _, elem := range lit.Elements {
		if _, isSpread := elem.(*ast.SpreadElement); isSpread || elem == nil {
			return "", false
		}
	}

	// Flat literal: one image, one malloc, one memcpy.
	if !elemTy.IsArray {
		if n < staticArrayLiteralMinLen {
			return "", false
		}
		image, ok := e.constArrayImage(lit.Elements, elemTy)
		if !ok {
			return "", false
		}
		llTy := fmt.Sprintf("[%d x %s]", n, elemTy.IR)
		g := e.newConstGlobal(llTy, image)
		bytes := int64(n) * int64(elemTy.Align())
		e.ensureMalloc()
		e.ensureMemcpy()
		dataReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, bytes))
		e.emitInstr(fmt.Sprintf("call void @memcpy(ptr %s, ptr %s, i64 %d)", dataReg, g, bytes))
		return dataReg, true
	}

	// Table of literal rows: every element is itself a flat literal-only array
	// of the row element type. (A deeper nesting stays on the general path.)
	rowTy := *elemTy.ElemType
	if rowTy.IsArray {
		return "", false
	}
	var flat []ast.Expression
	lens := make([]string, 0, n)
	for _, elem := range lit.Elements {
		row, ok := elem.(*ast.ArrayLiteral)
		if !ok {
			return "", false
		}
		for _, cell := range row.Elements {
			if _, isSpread := cell.(*ast.SpreadElement); isSpread || cell == nil {
				return "", false
			}
			if _, ok := constArrayElemOperand(cell, rowTy); !ok {
				return "", false
			}
		}
		flat = append(flat, row.Elements...)
		lens = append(lens, fmt.Sprintf("i64 %d", len(row.Elements)))
	}
	total := len(flat)
	flatImage := "zeroinitializer"
	if total > 0 {
		var ok bool
		if flatImage, ok = e.constArrayImage(flat, rowTy); !ok {
			return "", false
		}
	}
	flatTy := fmt.Sprintf("[%d x %s]", total, rowTy.IR)
	flatG := e.newConstGlobal(flatTy, flatImage)
	lensG := e.newConstGlobal(fmt.Sprintf("[%d x i64]", n), "["+strings.Join(lens, ", ")+"]")

	e.ensureArrLitRows()
	dataReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, int64(n)*8))
	e.emitInstr(fmt.Sprintf("call void @__kml_arr_lit_rows(ptr %s, ptr %s, ptr %s, i64 %d, i64 %d)", dataReg, flatG, lensG, int64(n), int64(rowTy.Align())))
	return dataReg, true
}

// ensureArrLitRows declares the row-table builder once:
// __kml_arr_lit_rows(outer, flat, lens, n, elemSize) fills outer[i] with a
// fresh box {data, len} whose data is a malloc'd copy of the next lens[i]
// elements of flat — exactly what the general path's boxArrayValue +
// emitArrayLiteralData produce per row, in a loop.
func (e *Emitter) ensureArrLitRows() {
	if e.usedArrLitRows {
		return
	}
	e.usedArrLitRows = true
	e.ensureMalloc()
	e.ensureMemcpy()
	e.emitGlobal(`
define void @__kml_arr_lit_rows(ptr %outer, ptr %flat, ptr %lens, i64 %n, i64 %esz) {
entry:
  br label %cond
cond:
  %i = phi i64 [ 0, %entry ], [ %inext, %body ]
  %off = phi i64 [ 0, %entry ], [ %offnext, %body ]
  %done = icmp eq i64 %i, %n
  br i1 %done, label %exit, label %body
body:
  %lenp = getelementptr i64, ptr %lens, i64 %i
  %len = load i64, ptr %lenp, align 8
  %bytes = mul i64 %len, %esz
  %data = call ptr @malloc(i64 %bytes)
  %src = getelementptr i8, ptr %flat, i64 %off
  call void @memcpy(ptr %data, ptr %src, i64 %bytes)
  %box = call ptr @malloc(i64 16)
  store ptr %data, ptr %box, align 8
  %boxlen = getelementptr i8, ptr %box, i64 8
  store i64 %len, ptr %boxlen, align 8
  %slot = getelementptr ptr, ptr %outer, i64 %i
  store ptr %box, ptr %slot, align 8
  %inext = add i64 %i, 1
  %offnext = add i64 %off, %bytes
  br label %cond
exit:
  ret void
}`)
}
