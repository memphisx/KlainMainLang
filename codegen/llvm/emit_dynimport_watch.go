// emit_dynimport_watch.go — the importer-side bridge to a dynamic-import
// island's module task (TDD-00225).
//
// An island is a separate runtime copy in a shared library. When its top level
// has an `await`, its main() runs the body to the first await as a module task
// and returns; from then on this program's event loop drives the island through
// five exports the island defines (emitIslandTaskGlue): `_state`, `_poll` (one
// non-blocking turn of the island's own loop), `_next_deadline_ns`, `_error`
// and `_backend` (its loop's backend descriptor, which this loop watches).
// The import() promise is a pending task promise here, settled from the
// island's module promise by the watcher below.
package llvm

import (
	_ "embed"
	"fmt"
)

//go:embed islandsrc/island.c
var islandSource string

//go:embed dynimportsrc/dynimportwatch.c
var dynImportWatchSource string

// DynImportWatchSource is the C runtime of the import() watcher.
func DynImportWatchSource() string { return dynImportWatchSource }

// UsesDynImportWatch reports whether the program links the watcher.
func (e *Emitter) UsesDynImportWatch() bool { return e.usedDynImportWatch }

// ensureDynImportWatch declares @__kml_dynimport_watch
// (dynimportsrc/dynimportwatch.c): it settles the import() promise at once
// when the island's module promise is already settled, else watches the
// island's loop as a handle on this one (its backend descriptor and its
// earliest deadline) and runs a pass of it whenever either fires.
func (e *Emitter) ensureDynImportWatch() {
	if e.usedDynImportWatch {
		return
	}
	e.usedDynImportWatch = true
	e.ensurePromiseRuntime()
	e.ensurePromiseSettle()
	e.ensureTimerRuntime() // @__kml_monotonic_ns
	e.ensureEventLoop()    // the loop the watcher's handles live on
	e.emitGlobal(`declare void @__kml_dynimport_watch(ptr, ptr, ptr, ptr, ptr, ptr, ptr, ptr)`)
}

// emitImportSettleFn emits the per-call-site function that fulfils an import()
// promise from a loaded island: it reads every annotated export through the
// island's dlsym'ed accessors into the typed result object (the shape
// importCallResultObjectType gives `await import()`) and settles the promise.
func (e *Emitter) emitImportSettleFn(hash string, objTy Type) string {
	e.importSettleCtr++
	name := fmt.Sprintf("__kml_dynimport_settle_%d", e.importSettleCtr)
	restoreBuilder := e.beginThunkEmit()

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
		e.heapStore(f.Ty.IR, v, gep, f.Ty.Align())
	}
	e.storePromiseValue("%prom", Value{Ref: obj, Ty: objTy})
	e.emitInstr("call void @__kml_promise_settle(ptr %prom, i64 1)")
	e.emitTerminator("ret void")
	e.writeFunc(fmt.Sprintf("internal void @%s(ptr %%handle, ptr %%prom)", name), e.allocas.String()+e.body.String())
	restoreBuilder()
	return name
}
