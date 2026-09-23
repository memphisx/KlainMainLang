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
	"fmt"
	"strings"
)

// dynImportWatchIR is one watcher: { handle, promise, settleFn, pollFn, errFn,
// dlFn, idle, done } — 64 bytes.
const dynImportWatchIR = "{ ptr, ptr, ptr, ptr, ptr, ptr, i64, i64 }"

// ensureDynImportWatch emits the watcher list, @__kml_dynimport_watch and the
// three event-loop hooks. A program without import() gets no-op stubs for the
// hooks (emitLoopTaskStubs).
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
	e.ensureRealloc()
	e.ensureTimerRuntime() // @__kml_monotonic_ns
	e.emitGlobal("@__kml_dynimport_watch_data = internal global ptr null, align 8")
	e.emitGlobal("@__kml_dynimport_watch_len = internal global i64 0, align 8")
	w := dynImportWatchIR
	ir := `
define internal void @__kml_dynimport_settle_from(ptr %handle, ptr %prom, ptr %settleFn, ptr %errFn, i64 %st) {
entry:
  %ful = icmp eq i64 %st, 1
  br i1 %ful, label %fulfil, label %reject
fulfil:
  call void %settleFn(ptr %handle, ptr %prom)
  ret void
reject:
  %err = call ptr %errFn()
  %errbits = ptrtoint ptr %err to i64
  %v0_p = getelementptr PROM, ptr %prom, i32 0, i32 2
  store i64 %errbits, ptr %v0_p, align 8
  call void @__kml_promise_settle(ptr %prom, i64 2)
  ret void
}
define void @__kml_dynimport_watch(ptr %handle, ptr %prom, ptr %settleFn, ptr %stateFn, ptr %pollFn, ptr %errFn, ptr %dlFn) {
entry:
  %st = call i64 %stateFn()
  %pending = icmp eq i64 %st, 0
  br i1 %pending, label %append, label %now
now:
  call void @__kml_dynimport_settle_from(ptr %handle, ptr %prom, ptr %settleFn, ptr %errFn, i64 %st)
  ret void
append:
  %len = load i64, ptr @__kml_dynimport_watch_len, align 8
  %data = load ptr, ptr @__kml_dynimport_watch_data, align 8
  %len1 = add i64 %len, 1
  %bytes = mul i64 %len1, 64
  %data1 = call ptr @realloc(ptr %data, i64 %bytes)
  store ptr %data1, ptr @__kml_dynimport_watch_data, align 8
  store i64 %len1, ptr @__kml_dynimport_watch_len, align 8
  %slot = getelementptr WSTRUCT, ptr %data1, i64 %len
  %f0 = getelementptr WSTRUCT, ptr %slot, i32 0, i32 0
  store ptr %handle, ptr %f0, align 8
  %f1 = getelementptr WSTRUCT, ptr %slot, i32 0, i32 1
  store ptr %prom, ptr %f1, align 8
  %f2 = getelementptr WSTRUCT, ptr %slot, i32 0, i32 2
  store ptr %settleFn, ptr %f2, align 8
  %f3 = getelementptr WSTRUCT, ptr %slot, i32 0, i32 3
  store ptr %pollFn, ptr %f3, align 8
  %f4 = getelementptr WSTRUCT, ptr %slot, i32 0, i32 4
  store ptr %errFn, ptr %f4, align 8
  %f5 = getelementptr WSTRUCT, ptr %slot, i32 0, i32 5
  store ptr %dlFn, ptr %f5, align 8
  %f6 = getelementptr WSTRUCT, ptr %slot, i32 0, i32 6
  store i64 0, ptr %f6, align 8
  %f7 = getelementptr WSTRUCT, ptr %slot, i32 0, i32 7
  store i64 0, ptr %f7, align 8
  ret void
}
define void @__kml_dynimport_dispatch() {
entry:
  %len = load i64, ptr @__kml_dynimport_watch_len, align 8
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %next ]
  %inb = icmp slt i64 %i, %len
  br i1 %inb, label %body, label %done
body:
  %data = load ptr, ptr @__kml_dynimport_watch_data, align 8
  %slot = getelementptr WSTRUCT, ptr %data, i64 %i
  %done_p = getelementptr WSTRUCT, ptr %slot, i32 0, i32 7
  %dn = load i64, ptr %done_p, align 8
  %isdone = icmp ne i64 %dn, 0
  br i1 %isdone, label %next, label %poll
poll:
  %pf_p = getelementptr WSTRUCT, ptr %slot, i32 0, i32 3
  %pf = load ptr, ptr %pf_p, align 8
  %r = call i64 %pf()
  %st = and i64 %r, 255
  %idle = lshr i64 %r, 8
  ; the poll ran island code; re-read the list in case it grew (realloc)
  %data2 = load ptr, ptr @__kml_dynimport_watch_data, align 8
  %slot2 = getelementptr WSTRUCT, ptr %data2, i64 %i
  %idle_p = getelementptr WSTRUCT, ptr %slot2, i32 0, i32 6
  store i64 %idle, ptr %idle_p, align 8
  %settled = icmp ne i64 %st, 0
  br i1 %settled, label %settle, label %next
settle:
  %done2_p = getelementptr WSTRUCT, ptr %slot2, i32 0, i32 7
  store i64 1, ptr %done2_p, align 8
  %h_p = getelementptr WSTRUCT, ptr %slot2, i32 0, i32 0
  %h = load ptr, ptr %h_p, align 8
  %p_p = getelementptr WSTRUCT, ptr %slot2, i32 0, i32 1
  %p = load ptr, ptr %p_p, align 8
  %sf_p = getelementptr WSTRUCT, ptr %slot2, i32 0, i32 2
  %sf = load ptr, ptr %sf_p, align 8
  %ef_p = getelementptr WSTRUCT, ptr %slot2, i32 0, i32 4
  %ef = load ptr, ptr %ef_p, align 8
  call void @__kml_dynimport_settle_from(ptr %h, ptr %p, ptr %sf, ptr %ef, i64 %st)
  br label %next
next:
  %inext = add i64 %i, 1
  br label %loop
done:
  ret void
}
define i1 @__kml_dynimport_keepalive() {
entry:
  %len = load i64, ptr @__kml_dynimport_watch_len, align 8
  %data = load ptr, ptr @__kml_dynimport_watch_data, align 8
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %next ]
  %inb = icmp slt i64 %i, %len
  br i1 %inb, label %body, label %no
body:
  %slot = getelementptr WSTRUCT, ptr %data, i64 %i
  %done_p = getelementptr WSTRUCT, ptr %slot, i32 0, i32 7
  %dn = load i64, ptr %done_p, align 8
  %idle_p = getelementptr WSTRUCT, ptr %slot, i32 0, i32 6
  %idle = load i64, ptr %idle_p, align 8
  %pending = icmp eq i64 %dn, 0
  %awake = icmp eq i64 %idle, 0
  %alive = and i1 %pending, %awake
  br i1 %alive, label %yes, label %next
next:
  %inext = add i64 %i, 1
  br label %loop
yes:
  ret i1 true
no:
  ret i1 false
}
define i64 @__kml_dynimport_next_deadline_ns() {
entry:
  %len = load i64, ptr @__kml_dynimport_watch_len, align 8
  %data = load ptr, ptr @__kml_dynimport_watch_data, align 8
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %next ]
  %best = phi i64 [ 0, %entry ], [ %best2, %next ]
  %cap = phi i1 [ false, %entry ], [ %cap2, %next ]
  %inb = icmp slt i64 %i, %len
  br i1 %inb, label %body, label %done
body:
  %slot = getelementptr WSTRUCT, ptr %data, i64 %i
  %done_p = getelementptr WSTRUCT, ptr %slot, i32 0, i32 7
  %dn = load i64, ptr %done_p, align 8
  %isdone = icmp ne i64 %dn, 0
  br i1 %isdone, label %skip, label %ask
ask:
  %df_p = getelementptr WSTRUCT, ptr %slot, i32 0, i32 5
  %df = load ptr, ptr %df_p, align 8
  %dl = call i64 %df()
  %hasdl = icmp ne i64 %dl, 0
  %none = icmp eq i64 %best, 0
  %sooner = icmp slt i64 %dl, %best
  %take0 = or i1 %none, %sooner
  %take = and i1 %hasdl, %take0
  %bestA = select i1 %take, i64 %dl, i64 %best
  %idle_p = getelementptr WSTRUCT, ptr %slot, i32 0, i32 6
  %idle = load i64, ptr %idle_p, align 8
  %awake = icmp eq i64 %idle, 0
  %nodl = xor i1 %hasdl, true
  %needcap = and i1 %awake, %nodl
  %capA = or i1 %cap, %needcap
  br label %next
skip:
  br label %next
next:
  %best2 = phi i64 [ %bestA, %ask ], [ %best, %skip ]
  %cap2 = phi i1 [ %capA, %ask ], [ %cap, %skip ]
  %inext = add i64 %i, 1
  br label %loop
done:
  br i1 %cap, label %capped, label %ret
capped:
  ; a pending island with no timer but something alive: look again in 10 ms,
  ; or sooner if some island's timer is due before that
  %now = call i64 @__kml_monotonic_ns()
  %soon = add i64 %now, 10000000
  %bnone = icmp eq i64 %best, 0
  %bsooner = icmp slt i64 %best, %soon
  %havebest = xor i1 %bnone, true
  %use = and i1 %havebest, %bsooner
  %r = select i1 %use, i64 %best, i64 %soon
  ret i64 %r
ret:
  ret i64 %best
}`
	ir = strings.ReplaceAll(ir, "PROM", promiseStructIR)
	ir = strings.ReplaceAll(ir, "WSTRUCT", w)
	e.emitGlobal(ir)
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
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", obj, objTy.StructSize()))
	structIR := objTy.StructIR()
	for _, f := range objTy.Fields {
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
