package llvm

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"

	"KlainMainLang/ast"
)

// holes_c.go — an array's holes (TDD-00226, holessrc/holes.c). A header's
// holes word (arrayHolesOff) is null while the array is packed; only a
// program that can make a hole (a write past the end, `new Array(n)`, an
// elided literal element) tests it.

//go:embed holessrc/holes.c
var holesSource string

func (e *Emitter) ensureHoles() {
	if e.usedHoles {
		return
	}
	e.usedHoles = true
	e.emitGlobal(`declare void @__kml_holes_mark(ptr, i64, i64)
declare i32 @__kml_holes_is(ptr, i64)
declare void @__kml_holes_fill(ptr, i64)
declare i64 @__kml_holes_count(ptr, i64)
declare i64 @__kml_holes_run(ptr, i64, i64)
declare void @__kml_holes_copy(ptr, i64, ptr, i64, i64)
declare void @__kml_holes_splice(ptr, i64, i64, i64, i64)
declare void @__kml_holes_setlen(ptr, i64, i64)
declare void @__kml_holes_clear(ptr, i64, i64)
declare void @__kml_holes_reverse(ptr, i64)
declare i64 @__kml_holes_compact(ptr, i64, i64)
declare i64 @__kml_holes_seed(ptr, i64, i32)
declare void @__kml_holes_move(ptr, i64, i64, i64)
declare i64 @__kml_holes_elem(ptr, i64)
declare i32 @__kml_holes_own(ptr, i64)
declare i32 @__kml_holes_elem_set(ptr, i64, i64)
declare ptr @__kml_holes_acc_label(ptr, i64)
declare i64 @__kml_holes_prop(ptr, i64)
declare void @__kml_holes_prop_set(ptr, i64, i64)`)
}

// holesUsed reports whether the program can make a hole: an element
// assignment (which may write past the end), `new Array(…)` / `Array(…)`, or
// an elided literal element.
func (e *Emitter) holesUsed() bool {
	if e.programIndependent() {
		return false
	}
	if e.holesScan == 0 {
		e.holesScan = 1
		if e.prog != nil {
			ast.Inspect(e.prog, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.AssignmentExpression:
					if _, ok := x.Left.(*ast.IndexExpression); ok {
						e.holesScan = 2
					}
					// `a.length = n` extends an array by holes.
					if m, ok := x.Left.(*ast.MemberExpression); ok && m.Property == "length" {
						e.holesScan = 2
					}
				case *ast.NewArrayExpression:
					e.holesScan = 2
				case *ast.NewExpression:
					if x.ClassName == "Array" && !x.Qualified {
						e.holesScan = 2
					}
				case *ast.CallExpression:
					if id, ok := x.Callee.(*ast.Identifier); ok && id.Name == "Array" {
						e.holesScan = 2
					}
					// An accessor element is a hole (Object.defineProperty).
					if m, ok := x.Callee.(*ast.MemberExpression); ok && (m.Property == "defineProperty" || m.Property == "defineProperties") {
						e.holesScan = 2
					}
				case *ast.UnaryExpression:
					if _, ok := x.Arg.(*ast.IndexExpression); ok && x.Op == "delete" {
						e.holesScan = 2
					}
				case *ast.NullLiteral:
					if x.Elision {
						e.holesScan = 2
					}
				}
				return e.holesScan != 2
			})
		}
	}
	return e.holesScan == 2
}

// emitElisionHoles marks the elided elements of the array literal lit
// (`[1, , 3]`) holes in its header hdr. Positions after a spread are known
// only at run time: emitSpreadArrayLitData records those (spreadElisions).
func (e *Emitter) emitElisionHoles(hdr string, lit *ast.ArrayLiteral) {
	for i, el := range lit.Elements {
		if _, ok := el.(*ast.SpreadElement); ok {
			return
		}
		if nl, ok := el.(*ast.NullLiteral); ok && nl.Elision {
			e.emitHolesMark(hdr, fmt.Sprint(i), fmt.Sprint(i+1))
		}
	}
}

// emitHolesWord is the holes word of the array header hdr.
func (e *Emitter) emitHolesWord(hdr string) string {
	hp, w := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", hp, hdr, arrayHolesOff))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", w, hp))
	return w
}

// emitHoleTest is an i1: element idx of the array header hdr is a hole. A
// packed array pays one load and branch.
func (e *Emitter) emitHoleTest(hdr, idx string) string {
	e.ensureHoles()
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", slot))
	e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", slot))
	holey := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", holey, e.emitHolesWord(hdr)))
	chkL, doneL := e.freshLabel("hole.chk"), e.freshLabel("hole.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", holey, chkL, doneL))
	e.emitLabel(chkL)
	r, b := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_holes_is(ptr %s, i64 %s)", r, hdr, idx))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", b, r))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", b, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", out, slot))
	return out
}

// emitHoleFill marks element idx of the array header hdr set.
func (e *Emitter) emitHoleFill(hdr, idx string) {
	e.ensureHoles()
	holey := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", holey, e.emitHolesWord(hdr)))
	fillL, doneL := e.freshLabel("hole.fill"), e.freshLabel("hole.filled")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", holey, fillL, doneL))
	e.emitLabel(fillL)
	e.emitInstr(fmt.Sprintf("call void @__kml_holes_fill(ptr %s, i64 %s)", hdr, idx))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
}

// emitHolesMark makes elements [from, to) of the array header hdr holes.
func (e *Emitter) emitHolesMark(hdr, from, to string) {
	e.ensureHoles()
	e.emitInstr(fmt.Sprintf("call void @__kml_holes_mark(ptr %s, i64 %s, i64 %s)", hdr, from, to))
}

// hofHolesHeader is the header a walk over objExpr's elements tests for
// holes: hdr, or "" when the program makes none or the array cannot hold
// one (a typed array, a flat value-type array, a view without a header).
func (e *Emitter) hofHolesHeader(objExpr ast.Expression, hdr string) string {
	if hdr == "" || !e.holesUsed() {
		return ""
	}
	if t := e.inferExprType(objExpr); t.IsTypedArray() || t.IsFlatArray() || t.ExecArray || !t.IsArray() {
		return ""
	}
	return hdr
}

// emitHoleSkip passes over a hole in a walk that skips them (forEach, map,
// filter, reduce, some, every, indexOf): when element idx of hdr is a hole,
// the walk's index (idxSlot) steps by step and the walk resumes at condL.
func (e *Emitter) emitHoleSkip(hdr, idx, idxSlot string, step int, condL string) {
	if hdr == "" {
		return
	}
	hole := e.emitHoleTest(hdr, idx)
	skipL, goL := e.freshLabel("hole.skip"), e.freshLabel("hole.visit")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hole, skipL, goL))
	e.emitLabel(skipL)
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %d", next, idx, step))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, idxSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(goL)
}

// emitHolesCopy copies the holes of elements [from, from+n) of the header
// src to elements [at, at+n) of the header dst (map, slice, concat keep
// their source's holes).
func (e *Emitter) emitHolesCopy(dst, at, src, from, n string) {
	if src == "" {
		return
	}
	e.ensureHoles()
	e.emitInstr(fmt.Sprintf("call void @__kml_holes_copy(ptr %s, i64 %s, ptr %s, i64 %s, i64 %s)", dst, at, src, from, n))
}

// emitIsUndefinedValue is an i1: v is undefined (the value a hole reads as).
func (e *Emitter) emitIsUndefinedValue(v Value) string {
	switch {
	case v.Ty.IsDynamic():
		tag, _ := e.emitUnboxTagPayload(v)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", r, tag, kmlTagUndefined))
		return r
	case v.Ty.IR == "void" || v.Ty.IsUndefined() && v.Ref == "null":
		return "true"
	case isNullableScalar(v.Ty):
		present, _ := e.nullableScalarAggParts(v)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", r, present))
		return r
	}
	return "false"
}

// emitArgIsUndefined is an i1: the argument v is undefined, including an
// optional number holding the slot's undefined (TDD-00241).
func (e *Emitter) emitArgIsUndefined(v Value) string {
	if v.Ty.IR == "double" && v.Ty.IsUndefined() && !isNullableScalar(v.Ty) {
		return e.emitF64IsUndefined(v.Ref)
	}
	return e.emitIsUndefinedValue(v)
}

// emitFillHoles stores the value a hole reads as, where a walk that reads
// holes loads the slot itself, into elements [from, to) of the backing
// buffer data: the undefined box for a dynamic element, NaN for a float,
// zero (a null reference, or the integer zero) otherwise.
func (e *Emitter) emitFillHoles(data, from, to string, elemTy Type) {
	word := ""
	switch {
	case elemTy.IsDynamic() && elemTy.IR == "i64":
		word = fmt.Sprint(nbUndefined)
	case elemTy.Float && elemTy.IR == "double":
		word = "0x7FF8000000000000"
	}
	if word == "" {
		e.ensureMemset()
		at, n, bytes := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", at, elemTy.IR, data, from))
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", n, to, from))
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytes, n, elemTy.Align()))
		e.emitInstr(fmt.Sprintf("call ptr @memset(ptr %s, i32 0, i64 %s)", at, bytes))
		return
	}
	idx := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idx))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", from, idx))
	condL, bodyL, doneL := e.freshLabel("holefill.cond"), e.freshLabel("holefill.body"), e.freshLabel("holefill.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i, more := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idx))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, i, to))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, doneL))
	e.emitLabel(bodyL)
	slot, next := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", slot, elemTy.IR, data, i))
	e.heapStore(elemTy.IR, word, slot, 8)
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, idx))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
}

// emitHolesCall calls the holes.c routine fn on the array header hdr with
// the i64 arguments args, when the program can make holes (a mutator keeping
// the bitmap in step with the elements it moves).
func (e *Emitter) emitHolesCall(fn, hdr string, args ...string) {
	if !e.holesUsed() || hdr == "" {
		return
	}
	e.ensureHoles()
	parts := []string{"ptr " + hdr}
	for _, a := range args {
		parts = append(parts, "i64 "+a)
	}
	e.emitInstr(fmt.Sprintf("call void @%s(%s)", fn, strings.Join(parts, ", ")))
}

// emitArrayElementDelete is `delete xs[i]`: an element in range becomes a
// hole (its slot holding what a hole reads as), the length unchanged; the
// result is true, as for any configurable element or absent index.
func (e *Emitter) emitArrayElementDelete(idx *ast.IndexExpression) (Value, error) {
	hdr, ptrReg, lenReg, elemTy, err := e.resolveArrayHOF(idx.Object, idx.GetPos())
	if err != nil {
		return Value{}, err
	}
	iv, err := e.emitExpr(idx.Index)
	if err != nil {
		return Value{}, err
	}
	iv, err = e.arrayIndexToI64(iv, idx.Index.GetPos())
	if err != nil {
		return Value{}, err
	}
	if hdr == "" {
		return Value{Ref: "true", Ty: TypeBool}, nil
	}
	inb := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %s, %s", inb, iv.Ref, lenReg))
	delL, doneL := e.freshLabel("arrdel.hole"), e.freshLabel("arrdel.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inb, delL, doneL))
	e.emitLabel(delL)
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, iv.Ref))
	e.emitFillHoles(ptrReg, iv.Ref, next, elemTy)
	e.emitHolesMark(hdr, iv.Ref, next)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	return Value{Ref: "true", Ty: TypeBool}, nil
}

// emitArrayHas is `k in xs` / `xs.hasOwnProperty(k)` / `Object.hasOwn(xs, k)`
// for an array: an index is the array's own exactly when it is in range and
// no hole, "length" always; with !ownOnly an Array.prototype method's name is
// there too. ok is false for a key known only at run time as a string.
func (e *Emitter) emitArrayHas(arr Value, keyExpr ast.Expression, ownOnly bool) (Value, bool, error) {
	var idx string
	if lit, isLit := keyExpr.(*ast.StringLiteral); isLit {
		n, err := strconv.ParseInt(lit.Value, 10, 64)
		switch {
		case lit.Value == "length":
			return Value{Ref: "true", Ty: TypeBool}, true, nil
		case err != nil || n < 0 || strconv.FormatInt(n, 10) != lit.Value:
			_, method := dynArrayMethods[lit.Value]
			if !ownOnly && (method || objectProtoKeys[lit.Value]) {
				return Value{Ref: "true", Ty: TypeBool}, true, nil
			}
			if arr.Ty.IsFlatArray() || arr.Ty.ExecArray || arr.Ref == "zeroinitializer" {
				return Value{Ref: "false", Ty: TypeBool}, true, nil
			}
			// A property added to the array (`a.tag = 1`), kept by its
			// header.
			e.ensureShapeRuntime()
			has, r := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_shape_expando_has(ptr %s, ptr %s)", has, e.absentSafeArrayHeader(arr.Ref), e.internString(lit.Value)))
			e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", r, has))
			return Value{Ref: r, Ty: TypeBool}, true, nil
		}
		idx = fmt.Sprint(n)
	} else {
		kt := e.inferExprType(keyExpr)
		if isStringTy(kt) || kt.IsDynamic() || !(kt.IsInteger() || kt.Float) {
			return Value{}, false, nil
		}
		kv, err := e.emitExpr(keyExpr)
		if err != nil {
			return Value{}, true, err
		}
		kv, err = e.arrayIndexToI64(kv, keyExpr.GetPos())
		if err != nil {
			return Value{}, true, err
		}
		idx = kv.Ref
	}
	inb := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %s, %s", inb, idx, e.arrayLen(arr)))
	if arr.Ty.IsTypedArray() || arr.Ty.IsFlatArray() || arr.Ty.ExecArray || !e.holesUsed() || arr.Ref == "zeroinitializer" {
		return Value{Ref: inb, Ty: TypeBool}, true, nil
	}
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", slot))
	e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", slot))
	chkL, doneL := e.freshLabel("arrhas.chk"), e.freshLabel("arrhas.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inb, chkL, doneL))
	e.emitLabel(chkL)
	// set, or an accessor element
	e.ensureHoles()
	hr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_holes_own(ptr %s, i64 %s)", hr, e.absentSafeArrayHeader(arr.Ref), idx))
	own := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", own, hr))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", own, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", r, slot))
	return Value{Ref: r, Ty: TypeBool}, true, nil
}
