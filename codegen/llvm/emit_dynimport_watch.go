// emit_dynimport_watch.go — the importer-side bridge to a dynamic-import
// island's module task (TDD-00225).
//
// An island is a separate runtime copy in a shared library. When its top level
// has an `await`, its main() runs the body to the first await as a module task
// and returns; from then on this program's event loop drives the island through
// four exports the island defines (emitIslandTaskGlue): `_state`, `_poll` (one
// non-blocking turn of the island's own loop), `_next_deadline_ns` and `_error`.
// The import() promise is a pending task promise here, settled from the
// island's module promise by the watcher below.
package llvm

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed dynimportsrc/dynimportwatch.c
var dynImportWatchSource string

// DynImportWatchSource is the C runtime of the import() watcher.
func DynImportWatchSource() string { return dynImportWatchSource }

// UsesDynImportWatch reports whether the program links the watcher.
func (e *Emitter) UsesDynImportWatch() bool { return e.usedDynImportWatch }

// ensureDynImportWatch declares the watcher and the three event-loop hooks
// (dynimportsrc/dynimportwatch.c). A program without import() gets no-op
// stubs for the hooks (emitLoopTaskStubs).
//
//   - @__kml_dynimport_watch settles the import() promise at once when the
//     island's state export says its module promise is already settled (no
//     top-level await, or one that settled during init), else appends a watcher.
//   - @__kml_dynimport_dispatch polls every pending island once per loop turn
//     and settles the promise when the island's module promise has.
//   - @__kml_dynimport_keepalive holds the loop while a pending island still
//     has something that can wake it (its last poll did not report idle).
//   - @__kml_dynimport_next_deadline_ns folds the islands' earliest timer into
//     the loop's select() wait; while a pending, non-idle island reports no
//     timer, the wait is capped at 10 ms — its sockets are invisible to this
//     loop's fd sets, the V1 blind spot the TDD records.
func (e *Emitter) ensureDynImportWatch() {
	if e.usedDynImportWatch {
		return
	}
	e.usedDynImportWatch = true
	e.ensurePromiseRuntime()
	e.ensurePromiseSettle()
	e.ensureTimerRuntime() // @__kml_monotonic_ns
	e.emitGlobal(`declare void @__kml_dynimport_watch(ptr, ptr, ptr, ptr, ptr, ptr, ptr)
declare void @__kml_dynimport_dispatch()
declare zeroext i1 @__kml_dynimport_keepalive()
declare i64 @__kml_dynimport_next_deadline_ns()`)
}

// emitImportSettleFn emits the per-call-site function that fulfils an import()
// promise from a loaded island: it reads every annotated export through the
// island's dlsym'ed accessors into the typed result object (the shape
// importCallResultObjectType gives `await import()`) and settles the promise.
func (e *Emitter) emitImportSettleFn(hash string, objTy Type) string {
	e.importSettleCtr++
	name := fmt.Sprintf("__kml_dynimport_settle_%d", e.importSettleCtr)
	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedBlockDone := e.blockDone
	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.blockDone = false

	e.ensureCalloc()
	obj := e.freshReg()
	e.emitObjAllocInto(obj, objTy)
	structIR := objTy.StructIR()
	for _, f := range objTy.UserFields() {
		symName := fmt.Sprintf("__kml_dynmod_%s_%s", hash, f.Name)
		fp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynimport_sym(ptr %%handle, ptr %s)", fp, e.internString(symName)))
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call %s %s()", v, f.Ty.IR, fp))
		idx, _, _ := objTy.FieldIndex(f.Name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, obj, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", f.Ty.IR, v, gep, f.Ty.Align()))
	}
	e.storePromiseValue("%prom", Value{Ref: obj, Ty: objTy})
	e.emitInstr("call void @__kml_promise_settle(ptr %prom, i64 1)")
	e.emitTerminator("ret void")
	e.functions.WriteString(fmt.Sprintf("\ndefine internal void @%s(ptr %%handle, ptr %%prom) {\nentry:\n", name))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")
	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.blockDone = savedBlockDone
	return name
}
