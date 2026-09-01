// emit_console_table.go — console.table(rows) (ADR-00560). Renders an array of
// objects (columns = the shared field names) or an array of primitives (a
// single "Values" column) as Node's Unicode box-drawing table. A non-array (or
// otherwise unsupported) argument falls back to console.log, matching Node's own
// behavior for a non-tabular value. Cell width is measured in bytes (this
// compiler's strings are byte sequences), so the layout is exact for ASCII
// content and follows the same wide-character narrowing the rest of the string
// surface already documents.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// ensureTableHelpers declares the two runtime string helpers console.table
// needs: __kml_table_repeat (repeat a unit string n times) and
// __kml_table_padend (right-pad a string with spaces to a byte width). Both
// return length-prefixed strings.
func (e *Emitter) ensureTableHelpers() {
	if e.usedTableHelpers {
		return
	}
	e.usedTableHelpers = true
	e.ensureStrlen()
	e.ensureMemcpy()
	e.ensureStrHeaderRuntime() // __kml_str_alloc
	e.emitGlobal(`
define ptr @__kml_table_repeat(ptr %unit, i64 %n) {
entry:
  %ulen = call i64 @strlen(ptr %unit)
  %total = mul i64 %ulen, %n
  %out = call ptr @__kml_str_alloc(i64 %total)
  br label %cond
cond:
  %k = phi i64 [ 0, %entry ], [ %kn, %body ]
  %done = icmp sge i64 %k, %n
  br i1 %done, label %fin, label %body
body:
  %off = mul i64 %k, %ulen
  %dst = getelementptr i8, ptr %out, i64 %off
  call ptr @memcpy(ptr %dst, ptr %unit, i64 %ulen)
  %kn = add i64 %k, 1
  br label %cond
fin:
  ret ptr %out
}

define ptr @__kml_table_padend(ptr %s, i64 %width) {
entry:
  %slen = call i64 @strlen(ptr %s)
  %needpad = icmp slt i64 %slen, %width
  %total = select i1 %needpad, i64 %width, i64 %slen
  %out = call ptr @__kml_str_alloc(i64 %total)
  call ptr @memcpy(ptr %out, ptr %s, i64 %slen)
  br label %cond
cond:
  %i = phi i64 [ %slen, %entry ], [ %in, %body ]
  %done = icmp sge i64 %i, %total
  br i1 %done, label %fin, label %body
body:
  %p = getelementptr i8, ptr %out, i64 %i
  store i8 32, ptr %p, align 1
  %in = add i64 %i, 1
  br label %cond
fin:
  ret ptr %out
}`)
}

// tableColumn is one rendered column: a compile-time header and a closure that
// emits the cell string for a given row (row index register + loaded element).
type tableColumn struct {
	header string
	cell   func(idxReg string, elem Value) (Value, error)
}

// emitConsoleTable implements console.table(data). Only the single-argument
// array form is tabulated; anything else falls back to console.log.
func (e *Emitter) emitConsoleTable(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return e.emitConsolePrint(args, 1, "")
	}
	argTy := e.inferExprType(args[0])
	if !argTy.IsArray || argTy.ElemType == nil {
		return e.emitConsolePrint(args, 1, "")
	}
	elemTy := *argTy.ElemType

	// Build the column list.
	cols := []tableColumn{{
		header: "(index)",
		cell: func(idxReg string, _ Value) (Value, error) {
			return e.emitValueToString(Value{Ref: idxReg, Ty: TypeI64})
		},
	}}
	if isInspectableObject(elemTy) {
		fields := elemTy.VisibleFields()
		if len(fields) == 0 {
			return e.emitConsolePrint(args, 1, "")
		}
		for _, f := range fields {
			f := f
			cols = append(cols, tableColumn{
				header: f.Name,
				cell: func(_ string, elem Value) (Value, error) {
					idx, _, _ := elem.Ty.FieldIndex(f.Name)
					gep := e.freshReg()
					load := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, elem.Ty.StructIR(), elem.Ref, idx))
					e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", load, StructFieldIR(f.Ty), gep, f.Ty.Align()))
					return e.emitInspectField(Value{Ref: load, Ty: f.Ty}, 0)
				},
			})
		}
	} else {
		cols = append(cols, tableColumn{
			header: "Values",
			cell: func(_ string, elem Value) (Value, error) {
				return e.emitInspectField(elem, 0)
			},
		})
	}

	e.ensureTableHelpers()
	e.ensureStrHeaderRuntime()

	ptrReg, lenReg, _, err := e.resolveArrayForHOF(args[0], pos)
	if err != nil {
		return Value{}, err
	}

	// Per-column max-width allocas, initialised to the header byte length.
	widthAllocas := make([]string, len(cols))
	for i, c := range cols {
		w := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", w))
		e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", len(c.header), w))
		widthAllocas[i] = w
	}

	// Pass A: measure every cell to grow the per-column widths.
	if err := e.emitTableRowLoop(ptrReg, lenReg, elemTy, cols, func(idxReg string, elem Value) error {
		for i, c := range cols {
			cell, err := c.cell(idxReg, elem)
			if err != nil {
				return err
			}
			clen := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", clen, cell.Ref))
			cur := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, widthAllocas[i]))
			gt := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", gt, clen, cur))
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", sel, gt, clen, cur))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sel, widthAllocas[i]))
		}
		return nil
	}); err != nil {
		return Value{}, err
	}

	// Load the finalised widths.
	widths := make([]string, len(cols))
	for i := range cols {
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", w, widthAllocas[i]))
		widths[i] = w
	}

	dash := e.internString("─")
	space := e.internString(" ")
	nl := e.internString("\n")
	vbar := e.internString("│")

	concat := func(a, b Value) Value {
		r, err := e.emitStringConcat(a, b)
		if err != nil {
			panic(err) // concat of two headered strings never fails
		}
		return r
	}
	str := func(s string) Value { return Value{Ref: e.internString(s), Ty: TypePtr} }

	// A border line: left + repeat(─, w+2) per column joined by mid + right.
	border := func(left, mid, right string) Value {
		acc := str(left)
		for i := range cols {
			wp2 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 2", wp2, widths[i]))
			seg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_table_repeat(ptr %s, i64 %s)", seg, dash, wp2))
			acc = concat(acc, Value{Ref: seg, Ty: TypePtr})
			if i < len(cols)-1 {
				acc = concat(acc, str(mid))
			}
		}
		acc = concat(acc, str(right))
		return concat(acc, Value{Ref: nl, Ty: TypePtr})
	}

	// A content row: │ + " " + cell.padEnd(w) + " " + │ per column.
	cellsRow := func(cellVals []Value) Value {
		acc := Value{Ref: vbar, Ty: TypePtr}
		for i := range cols {
			padded := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_table_padend(ptr %s, i64 %s)", padded, cellVals[i].Ref, widths[i]))
			acc = concat(acc, Value{Ref: space, Ty: TypePtr})
			acc = concat(acc, Value{Ref: padded, Ty: TypePtr})
			acc = concat(acc, Value{Ref: space, Ty: TypePtr})
			acc = concat(acc, Value{Ref: vbar, Ty: TypePtr})
		}
		return concat(acc, Value{Ref: nl, Ty: TypePtr})
	}

	// Header row from the compile-time header strings.
	headerVals := make([]Value, len(cols))
	for i, c := range cols {
		headerVals[i] = str(c.header)
	}

	// Assemble: top, header, mid; the data rows are appended in a runtime loop.
	outAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", outAlloca))
	head := concat(concat(border("┌", "┬", "┐"), cellsRow(headerVals)), border("├", "┼", "┤"))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", head.Ref, outAlloca))

	if err := e.emitTableRowLoop(ptrReg, lenReg, elemTy, cols, func(idxReg string, elem Value) error {
		cellVals := make([]Value, len(cols))
		for i, c := range cols {
			cell, err := c.cell(idxReg, elem)
			if err != nil {
				return err
			}
			cellVals[i] = cell
		}
		rowStr := cellsRow(cellVals)
		cur := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cur, outAlloca))
		merged := concat(Value{Ref: cur, Ty: TypePtr}, rowStr)
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", merged.Ref, outAlloca))
		return nil
	}); err != nil {
		return Value{}, err
	}

	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cur, outAlloca))
	final := concat(Value{Ref: cur, Ty: TypePtr}, border("└", "┴", "┘"))
	e.ensurePrintf()
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ...) @printf(ptr %s, ptr %s)", e.internString("%s"), final.Ref))
	return Value{Ty: TypeVoid}, nil
}

// emitTableRowLoop runs `body` once per array element, giving it the row index
// register and the loaded element Value.
func (e *Emitter) emitTableRowLoop(ptrReg, lenReg string, elemTy Type, cols []tableColumn, body func(idxReg string, elem Value) error) error {
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))
	condL := e.freshLabel("table.cond")
	bodyL := e.freshLabel("table.body")
	doneL := e.freshLabel("table.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idx, idxAlloca))
	atEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, %s", atEnd, idx, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", atEnd, doneL, bodyL))
	e.emitLabel(bodyL)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gep, elemTy.IR, ptrReg, idx))
	elem := e.loadArrayElem(gep, elemTy)
	if err := body(idx, elem); err != nil {
		return err
	}
	next := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, idx))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
	return nil
}
