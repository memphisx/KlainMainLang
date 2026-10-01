// runtime_worker.go — the thread side of Worker threads: the thread main a
// Worker runs (klainpool.c's workerSpawn starts it), the thread-aware exit
// that process.exit and an uncaught error take on a worker, and the loop
// hook that ends a terminated worker. The Worker, MessagePort and
// BroadcastChannel surface is lib/node/worker_threads.ts.
package llvm

import (
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

// sigBlockFlag returns SIG_BLOCK's numeric value — glibc defines it as 0,
// Darwin as 1. Same per-OS-constant pattern as httpNonblockFlag.
func (e *Emitter) sigBlockFlag() int {
	if e.opts.Target.OS() == "darwin" {
		return 1
	}
	return 0
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

func (e *Emitter) ensureWorkerRuntime() {
	if e.usedWorkerRuntime {
		return
	}
	e.usedWorkerRuntime = true
	e.ensureExit()
	e.ensureWorkerFdSetbit()
	e.emitGlobal("declare i32 @pthread_sigmask(i32 noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @sigfillset(ptr noundef)")
	e.emitGlobal("declare void @pthread_exit(ptr noundef)")
	// klainpool.c's worker natives.
	e.emitGlobal("declare void @__kml_worker_register(ptr, ptr, ptr)")
	e.emitGlobal("declare ptr @__kml_worker_enter(ptr)")
	e.emitGlobal("declare void @__kml_worker_leave(i64)")
	e.emitGlobal("declare zeroext i1 @__kml_worker_is_thread()")
	e.emitGlobal("declare zeroext i1 @__kml_worker_terminating()")

	// A worker thread's GC registration: with Boehm before its first
	// allocation, its thread-local block a root, and its own stack bottom in
	// the TLS orig slot so fiber-swap restores never point at another
	// thread's stack.
	gcRegister, gcUnregister := "", ""
	if e.isGCMode() {
		e.emitGlobal("declare i32 @GC_get_stack_base(ptr noundef)")
		e.emitGlobal("declare i32 @GC_register_my_thread(ptr noundef)")
		e.emitGlobal("declare i32 @GC_unregister_my_thread()")
		e.emitGlobal("declare void @__kml_gc_tls_register()")
		e.emitGlobal("declare void @__kml_gc_tls_unregister()")
		gcRegister = `
  %gcsb = alloca [2 x ptr], align 8
  call i32 @GC_get_stack_base(ptr %gcsb)
  call i32 @GC_register_my_thread(ptr %gcsb)
  call void @__kml_gc_tls_register()
  %gcmem_p = getelementptr [2 x ptr], ptr %gcsb, i32 0, i32 0
  %gcmem = load ptr, ptr %gcmem_p, align 8
  store ptr %gcmem, ptr @__kml_gc_orig_stackbottom, align 8`
		gcUnregister = `
  call void @__kml_gc_tls_unregister()
  call i32 @GC_unregister_my_thread()`
	}

	// @__kml_worker_thread(ctx): the thread a Worker runs. Signals are the
	// main thread's; the worker's modules evaluate (its entry), then its loop
	// runs until nothing holds it open, and the parent hears the exit code.
	e.emitGlobal(fmt.Sprintf(`define ptr @__kml_worker_thread(ptr %%ctx) {
entry:%s
  %%sigset = alloca [128 x i8], align 8
  call i32 @sigfillset(ptr %%sigset)
  call i32 @pthread_sigmask(i32 %d, ptr %%sigset, ptr null)
  %%entryfn = call ptr @__kml_worker_enter(ptr %%ctx)
  call void %%entryfn()
  %%code = call i64 @__kml_worker_run_loop()
  call void @__kml_worker_leave(i64 %%code)%s
  ret ptr null
}`, gcRegister, e.sigBlockFlag(), gcUnregister))

	// @__kml_worker_end_thread: the end of a worker thread from anywhere on
	// its own stack (process.exit, an uncaught error, terminate()).
	e.emitGlobal(fmt.Sprintf(`define void @__kml_worker_end_thread() {
entry:%s
  call void @pthread_exit(ptr null)
  unreachable
}`, gcUnregister))

	// @__kml_thread_exit(code): process.exit's end on a worker — the thread
	// ends with the code; elsewhere the process does. On a coroutine stack (a
	// worker module task's top level, or any task) the thread may not end
	// from there: winpthreads' pthread_exit longjmps to the thread's start
	// frame, and a longjmp off a fiber stack fast-fails the process. It
	// parks for good and whoever resumed the task — always on the thread's
	// own stack — ends the thread (@__kml_worker_abort_check).
	e.ensureTaskRuntime()
	e.emitGlobal("@__kml_worker_abort = internal thread_local global i1 false, align 1")
	e.emitGlobal(fmt.Sprintf(`define void @__kml_thread_exit(i32 %%code) {
entry:
  %%w = call zeroext i1 @__kml_worker_is_thread()
  br i1 %%w, label %%worker, label %%proc
proc:
  call void @exit(i32 %%code)
  unreachable
worker:
  %%c64 = sext i32 %%code to i64
  call void @__kml_worker_leave(i64 %%c64)
  %%ct = load ptr, ptr @__kml_current_task, align 8
  %%ontask = icmp ne ptr %%ct, null
  br i1 %%ontask, label %%parkforever, label %%endthread
endthread:
  call void @__kml_worker_end_thread()
  unreachable
parkforever:
  store i1 true, ptr @__kml_worker_abort, align 1
  %%rc_p = getelementptr %s, ptr %%ct, i32 0, i32 %d
  %%rc = load ptr, ptr %%rc_p, align 8
  %%ctx_p = getelementptr %s, ptr %%ct, i32 0, i32 %d
  %%ctx = load ptr, ptr %%ctx_p, align 8
  %%sw = call i32 @swapcontext(ptr %%ctx, ptr %%rc)
  unreachable
}`, taskStructIR, taskResumerCtx, taskStructIR, taskCtx))

	// @__kml_worker_abort_check runs after every task swap returns (spawn,
	// the scheduler, @__kml_task_resume) and ends the thread once control is
	// back on the thread's own stack.
	e.emitGlobal(`define void @__kml_worker_abort_check() {
entry:
  %abort = load i1, ptr @__kml_worker_abort, align 1
  br i1 %abort, label %chk, label %done
chk:
  %ct = load ptr, ptr @__kml_current_task, align 8
  %onstack = icmp eq ptr %ct, null
  br i1 %onstack, label %endthread, label %done
endthread:
  call void @__kml_worker_end_thread()
  unreachable
done:
  ret void
}`)

	// The loop's worker hook: a terminated worker ends at its next turn,
	// with exit code 1 (klainpool.c's leave records it).
	e.emitGlobal(`define void @__kml_worker_dispatch() {
entry:
  %t = call zeroext i1 @__kml_worker_terminating()
  br i1 %t, label %stop, label %done
stop:
  call void @__kml_thread_exit(i32 1)
  ret void
done:
  ret void
}
define i1 @__kml_worker_keepalive() {
entry:
  ret i1 0
}
define i1 @__kml_worker_fdset_add(ptr %fdset, ptr %maxfd) {
entry:
  ret i1 0
}`)

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
