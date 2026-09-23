package llvm

import (
	_ "embed"
	"fmt"
)

// inspect_c.go — the embedded util.inspect line-layout runtime (ADR-01067):
// Node's reduceToSingleString/groupArrayElements over a list of already
// rendered entries. Compiled alongside the program only when a structured
// value (object, array, tuple, Map, Set) is inspected.

//go:embed inspectsrc/inspect_reduce.c
var inspectReduceSource string

// InspectReduceSource returns the C source implementing __kml_inspect_begin/
// push/end. main.go compiles it when UsesInspectReduce() is set (libc + libm).
func InspectReduceSource() string { return inspectReduceSource }

// UsesInspectReduce reports whether a structured inspection reached codegen,
// so the build knows to compile+link the inspect C file.
func (e *Emitter) UsesInspectReduce() bool { return e.usedInspectReduce }

// ensureInspectReduce declares the entry-list ABI exactly once and marks the
// program as needing the C file compiled in.
func (e *Emitter) ensureInspectReduce() {
	if e.usedInspectReduce {
		return
	}
	e.usedInspectReduce = true
	e.requireLink("m") // sqrt/floor in the column grouping
	e.emitGlobal(`declare ptr @__kml_inspect_begin(i64, i64)`)
	e.emitGlobal(`declare void @__kml_inspect_push(ptr, ptr)`)
	e.emitGlobal(`declare void @__kml_inspect_push_more(ptr, i64)`)
	e.emitGlobal(`declare ptr @__kml_inspect_end(ptr, ptr, ptr, i64, i64, i64, i64)`)
	e.emitGlobal(`declare ptr @__kml_inspect_quote(ptr)`)
}

// inspectBegin opens an entry list for a container at nesting depth `depth`;
// nonempty is an i64 operand (or constant) saying whether it has entries —
// Node records the depth only for a non-empty container.
func (e *Emitter) inspectBegin(depth int, nonempty string) string {
	e.ensureInspectReduce()
	l := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_inspect_begin(i64 %d, i64 %s)", l, depth, nonempty))
	return l
}

// inspectPush appends one rendered entry string.
func (e *Emitter) inspectPush(list string, entry Value) {
	e.emitInstr(fmt.Sprintf("call void @__kml_inspect_push(ptr %s, ptr %s)", list, entry.Ref))
}

// inspectEnd lays the entries out between open and close (both IR string
// operands) and returns the rendered string. isArray enables Node's column
// grouping; numeric right-aligns the columns (a number/bigint array).
func (e *Emitter) inspectEnd(list, open, close string, depth int, isArray, numeric bool) Value {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_inspect_end(ptr %s, ptr %s, ptr %s, i64 %d, i64 %d, i64 %s, i64 %s)",
		r, list, open, close, 2*depth, depth, boolI64(isArray), boolI64(numeric)))
	return Value{Ref: r, Ty: TypePtr}
}

func boolI64(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// icmpNe returns an i1 register for `a != b` (i64 operands).
func (e *Emitter) icmpNe(a, b string) string {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %s", r, a, b))
	return r
}
