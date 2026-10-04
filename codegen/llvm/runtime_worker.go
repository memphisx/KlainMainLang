// runtime_worker.go — the thread side of Worker threads: the thread main a
// Worker runs (klainpool.c's workerSpawn starts it), the thread-aware exit
// that process.exit and an uncaught error take on a worker, and the loop
// hook that ends a terminated worker. The Worker, MessagePort and
// BroadcastChannel surface is lib/node/worker_threads.ts.
package llvm

import (
	_ "embed"
	"fmt"
)

// gcSBStore returns the IR statement that repoints the CURRENT thread's GC
// stack bottom at val: single-threaded, the direct @GC_stackbottom store the
// fiber machinery has always used; under Worker threads, the lock-guarded
// per-thread @__kml_gc_set_sb call (TDD-00098 stage 4) — a raw store to the
// process-wide global would corrupt the other threads' scanning.
//
// Windows: a context is a Win32 fiber running on a stack the OS allocated, not
// on the block the IR malloc'd for it (win32io.c), so val — computed from that
// block — is never the stack in use. The shim re-points the collector on every
// switch; an IR store after the switch would undo that with a heap address and
// the next collection would scan from the live SP into unrelated memory. There
// the statement records the stack actually running, read from the TEB.
func (e *Emitter) gcSBStore(val string) string {
	if e.opts.Target.OS() == "windows" {
		e.ensureGCStackBottomCurrent()
		return "call void @__kml_gc_sb_cur()"
	}
	if e.hasWorkers {
		return fmt.Sprintf("call void @__kml_gc_set_sb(ptr %s)", val)
	}
	return fmt.Sprintf("store ptr %s, ptr @GC_stackbottom, align 8", val)
}

// gcSBLoad returns the IR statement that reads the CURRENT thread's GC stack
// bottom into reg — the value a later gcSBStore(reg) puts back. Single-threaded
// that is @GC_stackbottom, the variable gcSBStore writes; under Worker threads
// the per-thread record @__kml_gc_set_sb writes, read back through
// GC_get_my_stackbottom. On Windows gcSBStore ignores its operand (it reads the
// TEB), so any defined value serves.
func (e *Emitter) gcSBLoad(reg string) string {
	if e.opts.Target.OS() == "windows" {
		return fmt.Sprintf("%s = load ptr, ptr @__kml_gc_orig_stackbottom, align 8", reg)
	}
	if e.hasWorkers {
		if !e.usedGCSBGet {
			e.usedGCSBGet = true
			e.emitGlobal("declare ptr @GC_get_my_stackbottom(ptr noundef)")
			e.emitGlobal(`define ptr @__kml_gc_get_sb() {
entry:
  %sb = alloca [2 x ptr], align 8
  call ptr @GC_get_my_stackbottom(ptr %sb)
  %slot = getelementptr [2 x ptr], ptr %sb, i32 0, i32 0
  %mem = load ptr, ptr %slot, align 8
  ret ptr %mem
}`)
		}
		return fmt.Sprintf("%s = call ptr @__kml_gc_get_sb()", reg)
	}
	return fmt.Sprintf("%s = load ptr, ptr @GC_stackbottom, align 8", reg)
}

// ensureGCStackBottomCurrent declares @__kml_gc_sb_cur (gcshim.c, Windows only —
// see gcSBStore): record the stack this code is running on, as the TEB reports
// it, as the current thread's GC stack base.
func (e *Emitter) ensureGCStackBottomCurrent() {
	if e.usedGCSBCur {
		return
	}
	e.usedGCSBCur = true
	e.emitGlobal("declare void @__kml_gc_sb_cur()")
}

// ensureGCUncollectable declares GC_malloc_uncollectable exactly once —
// zeroed, never collected, but still scanned. Used for any allocation whose
// only live reference can be invisible to Boehm (pipe-buffered envelopes,
// cross-thread shared blocks; TDD-00098/TDD-00099).
func (e *Emitter) ensureGCUncollectable() {
	if e.usedGCUncollectable {
		return
	}
	e.usedGCUncollectable = true
	e.emitGlobal("declare ptr @GC_malloc_uncollectable(i64 noundef)")
}

//go:embed workersrc/worker.c
var workerSource string

// WorkerSource is the Worker thread runtime's C source, behind kml_layout.h.
func WorkerSource() string { return layoutHeader() + workerSource }

// WorkerCFlags is worker.c's mode define: under -mm=gc the thread registers
// with the collector.
func (e *Emitter) WorkerCFlags() []string {
	if e.isGCMode() {
		return []string{"-DKLAIN_GC=1"}
	}
	return nil
}

// ensureWorkerRuntime declares the Worker thread runtime (workersrc/worker.c)
// once and emits the generated @__kml_worker_uncaught.
func (e *Emitter) ensureWorkerRuntime() {
	if e.usedWorkerRuntime {
		return
	}
	e.usedWorkerRuntime = true
	e.ensureExit()
	e.ensureWorkerFdSetbit()
	e.ensureTaskRuntime()
	// klainpool.c's worker natives, and worker.c's own entry points.
	e.emitGlobal(`declare void @__kml_worker_register(ptr, ptr, ptr)
declare ptr @__kml_worker_enter(ptr)
declare void @__kml_worker_leave(i64)
declare zeroext i1 @__kml_worker_is_thread()
declare zeroext i1 @__kml_worker_terminating()
declare ptr @__kml_worker_thread(ptr)
declare void @__kml_worker_end_thread()
declare void @__kml_thread_exit(i32)
declare void @__kml_worker_abort_check()
declare void @__kml_worker_dispatch()
declare i1 @__kml_worker_keepalive()
declare i1 @__kml_worker_fdset_add(ptr, ptr)`)

	// @__kml_worker_uncaught(tag, payload): an uncaught error on a worker
	// thread goes to the parent's 'error' listener (through the hook
	// worker_threads.ts registers, which posts it on the internal port) and
	// ends the thread with code 1. On the main thread it returns.
	restore := e.beginDetachedFunc()
	box := e.emitCaughtToAny(Value{Ref: "%rec", Ty: TypeCaught})
	e.emitInstr(fmt.Sprintf("call i1 @__kml_process_hook_call(i32 3, i64 %s, i64 %d)", box.Ref, nbUndefined))
	e.emitInstr("call void @__kml_thread_exit(i32 1)")
	e.emitTerminator("unreachable")
	body := e.allocas.String() + e.body.String()
	restore()
	e.ensureProcessHooks()
	e.functions.WriteString(fmt.Sprintf(`
define void @__kml_worker_uncaught(i8 %%tag, i64 %%pay) {
entry:
  %%w = call zeroext i1 @__kml_worker_is_thread()
  br i1 %%w, label %%worker, label %%main
main:
  ret void
worker:
  %%rec0 = insertvalue { i8, i64 } undef, i8 %%tag, 0
  %%rec = insertvalue { i8, i64 } %%rec0, i64 %%pay, 1
%s}
`, body))
}

// ensureWorkerFdSetbit emits the fd_set bit-set helper exactly once — used
// by both the worker and channel event-loop hooks.
func (e *Emitter) ensureWorkerFdSetbit() {
	if e.usedWorkerFdSetbit {
		return
	}
	e.usedWorkerFdSetbit = true
	e.emitGlobal(`define void @__kml_worker_fd_setbit(i32 %fd, ptr %fdset, ptr %maxfd) {
entry:
  %fddiv8 = sdiv i32 %fd, 8
  %fdmod8 = srem i32 %fd, 8
  %fddiv8_64 = sext i32 %fddiv8 to i64
  %byteptr = getelementptr i8, ptr %fdset, i64 %fddiv8_64
  %bitpos8 = trunc i32 %fdmod8 to i8
  %bitmask = shl i8 1, %bitpos8
  %oldbyte = load i8, ptr %byteptr, align 1
  %newbyte = or i8 %oldbyte, %bitmask
  store i8 %newbyte, ptr %byteptr, align 1
  %curmax = load i32, ptr %maxfd, align 4
  %bigger = icmp sgt i32 %fd, %curmax
  br i1 %bigger, label %update, label %done

update:
  store i32 %fd, ptr %maxfd, align 4
  br label %done

done:
  ret void
}`)
}
