// runtime_worker.go — the Worker (worker_threads) runtime (TDD-00098):
// pthread spawn/join, the per-worker control block, and the pipe-based
// message channel both sides' event-loop instances poll.
//
// Channel design: each direction is a plain pipe carrying 8-byte pointers to
// malloc'd message envelopes ({ i64 kind, i64 w0, i64 w1 }). POSIX
// guarantees a write() of <= PIPE_BUF bytes is atomic, so an 8-byte pointer
// write needs no mutex at all — the kernel is the queue. The pipe read fd is
// non-blocking and folded into each thread's select() fdset by
// @__kml_worker_fdset_add (see the TDD-00098 hooks in runtime_http.go's
// @__kml_event_loop_run); @__kml_worker_dispatch drains and dispatches after
// each select() wakeup. Envelope kinds: 0 = message, 1 = worker exited
// (w0 = exit code), 2 = terminate request (parent → worker).
//
// Every mutable global here is thread_local: the parent thread's
// @__kml_workers_* array holds its spawned children's control blocks, and a
// worker thread sees its own control block via @__kml_worker_self (null on
// the main thread — that null/non-null split is how the shared hook
// functions pick the parent-side vs worker-side behavior).
package llvm

import (
	"fmt"
	"runtime"
)

// workerCtrlIR is the control block's LLVM struct type. Field indices:
//
//	0 tid (pthread_t — i64-sized on both supported platforms)
//	1 p2w_r  2 p2w_w   parent → worker pipe (read/write fds)
//	3 w2p_r  4 w2p_w   worker → parent pipe
//	5 terminate flag   6 exited flag   7 exit code
//	8 workerData w0    9 workerData w1 (encoded payload words)
//	10 msg_cb  11 exit_cb  12 err_cb   parent-side adapter closures
//	13 wmsg_cb                          worker-side (parentPort) adapter
//	14 entry_fn                         the worker module's entry function
//	15 alive_hold                       worker keepalive (message listener registered)
const workerCtrlIR = "{ i64, i32, i32, i32, i32, i64, i64, i64, i64, i64, ptr, ptr, ptr, ptr, ptr, i64 }"

const workerCtrlBytes = 112

// gcSBStore returns the IR statement that repoints the CURRENT thread's GC
// stack bottom at val: single-threaded, the direct @GC_stackbottom store the
// fiber machinery has always used; under Worker threads, the lock-guarded
// per-thread @__kml_gc_set_sb call (TDD-00098 stage 4) — a raw store to the
// process-wide global would corrupt the other threads' scanning.
func (e *Emitter) gcSBStore(val string) string {
	if e.hasWorkers {
		return fmt.Sprintf("call void @__kml_gc_set_sb(ptr %s)", val)
	}
	return fmt.Sprintf("store ptr %s, ptr @GC_stackbottom, align 8", val)
}

// sigBlockFlag returns SIG_BLOCK's numeric value — glibc defines it as 0,
// Darwin as 1. Same per-OS-constant pattern as httpNonblockFlag.
func sigBlockFlag() int {
	if runtime.GOOS == "darwin" {
		return 1
	}
	return 0
}

func (e *Emitter) ensureWorkerRuntime() {
	if e.usedWorkerRuntime {
		return
	}
	e.usedWorkerRuntime = true
	e.ensureMalloc()
	e.ensureCalloc()
	e.ensureFree()
	e.ensureMemcpy()
	e.ensureReadDecl()
	e.ensurePrintf()
	e.ensureExit()
	// The parent's message/exit delivery and the worker's own drive both run
	// on the unified event loop (its state is all thread_local, so each
	// thread runs an independent instance) — see the TDD-00098 hooks there.
	e.ensureHTTPRuntime()

	e.emitGlobal("declare i32 @pthread_create(ptr noundef, ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @pthread_join(i64 noundef, ptr noundef)")
	e.emitGlobal("declare i32 @pthread_sigmask(i32 noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @sigfillset(ptr noundef)")
	e.emitGlobal("declare i32 @pipe(ptr noundef)")
	e.emitGlobal("declare void @pthread_exit(ptr noundef)")
	// read/write/close/fcntl are declared by ensureHTTPRuntime already.

	e.emitGlobal("@__kml_workers_data = internal thread_local global ptr null, align 8")
	e.emitGlobal("@__kml_workers_len = internal thread_local global i64 0, align 8")
	e.emitGlobal("@__kml_workers_cap = internal thread_local global i64 0, align 8")
	e.emitGlobal("@__kml_worker_self = internal thread_local global ptr null, align 8")

	// -mm=gc soundness (TDD-00098 stage 4): while a message envelope is in
	// flight, its ONLY reference lives inside the kernel's pipe buffer —
	// invisible to Boehm's scan — and the same goes for control blocks
	// referenced only from thread-local globals. So under gc mode, control
	// blocks, the per-thread worker registry array, and envelopes are
	// allocated with GC_malloc_uncollectable: never collected, but still
	// *scanned*, which conservatively keeps every payload clone reachable
	// through the envelope's encoded words. `free` is already shimmed to
	// GC_free, so the receiver-side envelope free works in both modes.
	rawAlloc := "@malloc"
	ctrlAlloc := fmt.Sprintf("call ptr @calloc(i64 1, i64 %d)", workerCtrlBytes)
	if e.isGCMode() {
		e.emitGlobal("declare ptr @GC_malloc_uncollectable(i64 noundef)")
		rawAlloc = "@GC_malloc_uncollectable"
		// GC_malloc_uncollectable returns zeroed memory, matching calloc.
		ctrlAlloc = fmt.Sprintf("call ptr @GC_malloc_uncollectable(i64 %d)", workerCtrlBytes)
	}

	// __kml_worker_spawn: allocate + wire a control block, register it in
	// the calling thread's worker list, and start the pthread.
	e.emitGlobal(fmt.Sprintf(`define ptr @__kml_worker_spawn(ptr %%entryfn0, i64 %%wd0, i64 %%wd1) {
entry:
  %%pipes1 = alloca [2 x i32], align 4
  %%pipes2 = alloca [2 x i32], align 4
  %%ctrl = %s
  call i32 @pipe(ptr %%pipes1)
  call i32 @pipe(ptr %%pipes2)
  %%p2wr_p = getelementptr [2 x i32], ptr %%pipes1, i32 0, i32 0
  %%p2ww_p = getelementptr [2 x i32], ptr %%pipes1, i32 0, i32 1
  %%w2pr_p = getelementptr [2 x i32], ptr %%pipes2, i32 0, i32 0
  %%w2pw_p = getelementptr [2 x i32], ptr %%pipes2, i32 0, i32 1
  %%p2wr = load i32, ptr %%p2wr_p, align 4
  %%p2ww = load i32, ptr %%p2ww_p, align 4
  %%w2pr = load i32, ptr %%w2pr_p, align 4
  %%w2pw = load i32, ptr %%w2pw_p, align 4
  ; both read ends non-blocking: dispatch drains until EAGAIN
  %%fl1 = call i32 (i32, i32, ...) @fcntl(i32 %%p2wr, i32 3)
  %%fl1n = or i32 %%fl1, %d
  call i32 (i32, i32, ...) @fcntl(i32 %%p2wr, i32 4, i32 %%fl1n)
  %%fl2 = call i32 (i32, i32, ...) @fcntl(i32 %%w2pr, i32 3)
  %%fl2n = or i32 %%fl2, %d
  call i32 (i32, i32, ...) @fcntl(i32 %%w2pr, i32 4, i32 %%fl2n)
  %%f1 = getelementptr %s, ptr %%ctrl, i32 0, i32 1
  store i32 %%p2wr, ptr %%f1, align 4
  %%f2 = getelementptr %s, ptr %%ctrl, i32 0, i32 2
  store i32 %%p2ww, ptr %%f2, align 4
  %%f3 = getelementptr %s, ptr %%ctrl, i32 0, i32 3
  store i32 %%w2pr, ptr %%f3, align 4
  %%f4 = getelementptr %s, ptr %%ctrl, i32 0, i32 4
  store i32 %%w2pw, ptr %%f4, align 4
  %%f8 = getelementptr %s, ptr %%ctrl, i32 0, i32 8
  store i64 %%wd0, ptr %%f8, align 8
  %%f9 = getelementptr %s, ptr %%ctrl, i32 0, i32 9
  store i64 %%wd1, ptr %%f9, align 8
  %%f14 = getelementptr %s, ptr %%ctrl, i32 0, i32 14
  store ptr %%entryfn0, ptr %%f14, align 8
  call void @__kml_workers_register(ptr %%ctrl)
  call void @__kml_worker_curl_preinit()
  %%tid_p = getelementptr %s, ptr %%ctrl, i32 0, i32 0
  call i32 @pthread_create(ptr %%tid_p, ptr null, ptr @__kml_worker_main, ptr %%ctrl)
  ret ptr %%ctrl
}`, ctrlAlloc, httpNonblockFlag(), httpNonblockFlag(),
		workerCtrlIR, workerCtrlIR, workerCtrlIR, workerCtrlIR, workerCtrlIR, workerCtrlIR, workerCtrlIR, workerCtrlIR))

	// __kml_workers_register: append ctrl to the calling thread's growable
	// worker array (same doubling growth every other runtime array uses).
	e.emitGlobal(fmt.Sprintf(`define void @__kml_workers_register(ptr %%ctrl) {
entry:
  %%len = load i64, ptr @__kml_workers_len, align 8
  %%cap = load i64, ptr @__kml_workers_cap, align 8
  %%needgrow = icmp sge i64 %%len, %%cap
  br i1 %%needgrow, label %%grow, label %%putslot

grow:
  %%cap2 = mul i64 %%cap, 2
  %%atleast4 = icmp sge i64 %%cap2, 4
  %%newcap = select i1 %%atleast4, i64 %%cap2, i64 4
  %%newbytes = mul i64 %%newcap, 8
  %%newdata = call ptr %s(i64 %%newbytes)
  %%olddata = load ptr, ptr @__kml_workers_data, align 8
  %%oldbytes = mul i64 %%len, 8
  %%hasold = icmp ne ptr %%olddata, null
  br i1 %%hasold, label %%copyold, label %%aftercopy

copyold:
  call ptr @memcpy(ptr %%newdata, ptr %%olddata, i64 %%oldbytes)
  call void @free(ptr %%olddata)
  br label %%aftercopy

aftercopy:
  store ptr %%newdata, ptr @__kml_workers_data, align 8
  store i64 %%newcap, ptr @__kml_workers_cap, align 8
  br label %%putslot

putslot:
  %%data = load ptr, ptr @__kml_workers_data, align 8
  %%slot = getelementptr ptr, ptr %%data, i64 %%len
  store ptr %%ctrl, ptr %%slot, align 8
  %%newlen = add i64 %%len, 1
  store i64 %%newlen, ptr @__kml_workers_len, align 8
  ret void
}`, rawAlloc))

	// __kml_worker_main: the pthread trampoline. Blocks all signals (they
	// are the main thread's to handle), publishes the control block into
	// TLS, runs the worker module's top level, drives this thread's own
	// event-loop instance until nothing keeps it alive, then runs the exit
	// protocol (exited flag + exit envelope to the parent).
	// -mm=gc (TDD-00098 stage 4): register this thread with Boehm before its
	// first allocation, and remember its own stack bottom in the TLS orig
	// slot so fiber-swap restores on this thread never point at another
	// thread's stack. Unregister on the way out.
	gcRegister, gcUnregister := "", ""
	if e.isGCMode() {
		e.emitGlobal("declare i32 @GC_get_stack_base(ptr noundef)")
		e.emitGlobal("declare i32 @GC_register_my_thread(ptr noundef)")
		e.emitGlobal("declare i32 @GC_unregister_my_thread()")
		gcRegister = `
  %gcsb = alloca [2 x ptr], align 8
  call i32 @GC_get_stack_base(ptr %gcsb)
  call i32 @GC_register_my_thread(ptr %gcsb)
  %gcmem_p = getelementptr [2 x ptr], ptr %gcsb, i32 0, i32 0
  %gcmem = load ptr, ptr %gcmem_p, align 8
  store ptr %gcmem, ptr @__kml_gc_orig_stackbottom, align 8`
		gcUnregister = `
  call i32 @GC_unregister_my_thread()`
	}
	e.emitGlobal(fmt.Sprintf(`define ptr @__kml_worker_main(ptr %%ctrl) {
entry:%s
  %%sigset = alloca [128 x i8], align 8
  call i32 @sigfillset(ptr %%sigset)
  call i32 @pthread_sigmask(i32 %d, ptr %%sigset, ptr null)
  store ptr %%ctrl, ptr @__kml_worker_self, align 8
  %%entry_p = getelementptr %s, ptr %%ctrl, i32 0, i32 14
  %%entryfn = load ptr, ptr %%entry_p, align 8
  call void %%entryfn()
  call void @__kml_event_loop_run()
  ; The exited flag is deliberately NOT set here: the parent sets it when it
  ; processes the exit envelope. Setting it from this thread would race the
  ; parent's keepalive check — the parent's loop could exit (and the process
  ; end) before the envelope is ever dispatched, silently dropping the
  ; 'exit' event. Found as a real bug in the first end-to-end run.
  %%code_p = getelementptr %s, ptr %%ctrl, i32 0, i32 7
  %%code = load i64, ptr %%code_p, align 8
  %%w2pw_p = getelementptr %s, ptr %%ctrl, i32 0, i32 4
  %%w2pw = load i32, ptr %%w2pw_p, align 4
  call void @__kml_worker_send_env(i32 %%w2pw, i64 1, i64 %%code, i64 0)%s
  ret ptr null
}`, gcRegister, sigBlockFlag(), workerCtrlIR, workerCtrlIR, workerCtrlIR, gcUnregister))

	// __kml_worker_send_env: malloc an envelope and write its pointer into
	// the pipe (atomic for 8 bytes; blocks only if 64K of envelopes are
	// already queued — natural backpressure).
	e.emitGlobal(fmt.Sprintf(`define void @__kml_worker_send_env(i32 %%fd, i64 %%kind, i64 %%w0, i64 %%w1) {
entry:
  %%env = call ptr %s(i64 24)
  %%k_p = getelementptr { i64, i64, i64 }, ptr %%env, i32 0, i32 0
  store i64 %%kind, ptr %%k_p, align 8
  %%w0_p = getelementptr { i64, i64, i64 }, ptr %%env, i32 0, i32 1
  store i64 %%w0, ptr %%w0_p, align 8
  %%w1_p = getelementptr { i64, i64, i64 }, ptr %%env, i32 0, i32 2
  store i64 %%w1, ptr %%w1_p, align 8
  %%slot = alloca ptr, align 8
  store ptr %%env, ptr %%slot, align 8
  call i64 @write(i32 %%fd, ptr %%slot, i64 8)
  ret void
}`, rawAlloc))

	// __kml_worker_post: the typed-codegen entry point for postMessage /
	// terminate. Direction is picked by which thread is calling: the worker
	// thread (self != null) sends to its parent, the parent sends to the
	// given worker.
	e.emitGlobal(fmt.Sprintf(`define void @__kml_worker_post(ptr %%ctrl, i64 %%kind, i64 %%w0, i64 %%w1) {
entry:
  %%self = load ptr, ptr @__kml_worker_self, align 8
  %%isworker = icmp ne ptr %%self, null
  br i1 %%isworker, label %%toparent, label %%tochild

toparent:
  %%w2pw_p = getelementptr %s, ptr %%ctrl, i32 0, i32 4
  %%w2pw = load i32, ptr %%w2pw_p, align 4
  call void @__kml_worker_send_env(i32 %%w2pw, i64 %%kind, i64 %%w0, i64 %%w1)
  ret void

tochild:
  %%p2ww_p = getelementptr %s, ptr %%ctrl, i32 0, i32 2
  %%p2ww = load i32, ptr %%p2ww_p, align 4
  call void @__kml_worker_send_env(i32 %%p2ww, i64 %%kind, i64 %%w0, i64 %%w1)
  ret void
}`, workerCtrlIR, workerCtrlIR))

	// __kml_worker_set_cb: store an adapter closure into a control-block
	// slot (10 msg / 11 exit / 12 err / 13 worker-side msg). Registering a
	// worker-side message listener also sets the keepalive hold.
	e.emitGlobal(fmt.Sprintf(`define void @__kml_worker_set_cb(ptr %%ctrl, i64 %%slotidx, ptr %%cb) {
entry:
  %%slot = getelementptr %s, ptr %%ctrl, i32 0, i32 10
  %%isw = icmp eq i64 %%slotidx, 13
  %%off = sub i64 %%slotidx, 10
  %%slotp = getelementptr ptr, ptr %%slot, i64 %%off
  store ptr %%cb, ptr %%slotp, align 8
  br i1 %%isw, label %%hold, label %%done

hold:
  %%hold_p = getelementptr %s, ptr %%ctrl, i32 0, i32 15
  store i64 1, ptr %%hold_p, align 8
  br label %%done

done:
  ret void
}`, workerCtrlIR, workerCtrlIR))

	// __kml_worker_keepalive: event-loop hook. Worker thread: stay alive
	// while a message listener is registered and no terminate request has
	// been seen. Parent thread: stay alive while any child hasn't exited.
	e.emitGlobal(fmt.Sprintf(`define i1 @__kml_worker_keepalive() {
entry:
  %%self = load ptr, ptr @__kml_worker_self, align 8
  %%isworker = icmp ne ptr %%self, null
  br i1 %%isworker, label %%workerside, label %%parentside

workerside:
  %%hold_p = getelementptr %s, ptr %%self, i32 0, i32 15
  %%hold = load i64, ptr %%hold_p, align 8
  %%hashold = icmp ne i64 %%hold, 0
  %%term_p = getelementptr %s, ptr %%self, i32 0, i32 5
  %%term = load i64, ptr %%term_p, align 8
  %%noterm = icmp eq i64 %%term, 0
  %%walive = and i1 %%hashold, %%noterm
  ret i1 %%walive

parentside:
  %%len = load i64, ptr @__kml_workers_len, align 8
  %%data = load ptr, ptr @__kml_workers_data, align 8
  br label %%loop

loop:
  %%i = phi i64 [ 0, %%parentside ], [ %%inext, %%next ]
  %%inb = icmp slt i64 %%i, %%len
  br i1 %%inb, label %%check, label %%none

check:
  %%slot = getelementptr ptr, ptr %%data, i64 %%i
  %%ctrl = load ptr, ptr %%slot, align 8
  %%ex_p = getelementptr %s, ptr %%ctrl, i32 0, i32 6
  %%ex = load i64, ptr %%ex_p, align 8
  %%live = icmp eq i64 %%ex, 0
  br i1 %%live, label %%alive, label %%next

next:
  %%inext = add i64 %%i, 1
  br label %%loop

alive:
  ret i1 1

none:
  ret i1 0
}`, workerCtrlIR, workerCtrlIR, workerCtrlIR))

	// __kml_worker_fdset_add: event-loop hook — add this thread's message
	// pipe read fds into the read fd_set, bumping *maxfd. Returns false
	// (pipe readability alone wakes select(); no forced-zero case).
	e.emitGlobal(fmt.Sprintf(`define i1 @__kml_worker_fdset_add(ptr %%fdset, ptr %%maxfd) {
entry:
  %%self = load ptr, ptr @__kml_worker_self, align 8
  %%isworker = icmp ne ptr %%self, null
  br i1 %%isworker, label %%workerside, label %%parentside

workerside:
  %%p2wr_p = getelementptr %s, ptr %%self, i32 0, i32 1
  %%p2wr = load i32, ptr %%p2wr_p, align 4
  call void @__kml_worker_fd_setbit(i32 %%p2wr, ptr %%fdset, ptr %%maxfd)
  ret i1 0

parentside:
  %%len = load i64, ptr @__kml_workers_len, align 8
  %%data = load ptr, ptr @__kml_workers_data, align 8
  br label %%loop

loop:
  %%i = phi i64 [ 0, %%parentside ], [ %%inext, %%next ]
  %%inb = icmp slt i64 %%i, %%len
  br i1 %%inb, label %%body, label %%done

body:
  %%slot = getelementptr ptr, ptr %%data, i64 %%i
  %%ctrl = load ptr, ptr %%slot, align 8
  %%ex_p = getelementptr %s, ptr %%ctrl, i32 0, i32 6
  %%ex = load i64, ptr %%ex_p, align 8
  %%live = icmp eq i64 %%ex, 0
  br i1 %%live, label %%addfd, label %%next

addfd:
  %%w2pr_p = getelementptr %s, ptr %%ctrl, i32 0, i32 3
  %%w2pr = load i32, ptr %%w2pr_p, align 4
  call void @__kml_worker_fd_setbit(i32 %%w2pr, ptr %%fdset, ptr %%maxfd)
  br label %%next

next:
  %%inext = add i64 %%i, 1
  br label %%loop

done:
  ret i1 0
}`, workerCtrlIR, workerCtrlIR, workerCtrlIR))

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

	// __kml_worker_drain_fd: read 8-byte envelope pointers from fd until
	// EAGAIN, dispatching each against ctrl. side: 0 = parent draining a
	// child's worker→parent pipe, 1 = worker draining its parent→worker
	// pipe. Envelope kinds: 0 message, 1 exit (parent side only), 2
	// terminate (worker side only). Adapter closures are uniform
	// void(ptr env, i64 w0, i64 w1) — codegen synthesizes the decode shim.
	e.emitGlobal(fmt.Sprintf(`define void @__kml_worker_drain_fd(i32 %%fd, ptr %%ctrl, i64 %%side) {
entry:
  %%slot = alloca ptr, align 8
  br label %%loop

loop:
  %%n = call i64 @read(i32 %%fd, ptr %%slot, i64 8)
  %%got = icmp eq i64 %%n, 8
  br i1 %%got, label %%gotenv, label %%done

gotenv:
  %%env = load ptr, ptr %%slot, align 8
  %%k_p = getelementptr { i64, i64, i64 }, ptr %%env, i32 0, i32 0
  %%kind = load i64, ptr %%k_p, align 8
  %%w0_p = getelementptr { i64, i64, i64 }, ptr %%env, i32 0, i32 1
  %%w0 = load i64, ptr %%w0_p, align 8
  %%w1_p = getelementptr { i64, i64, i64 }, ptr %%env, i32 0, i32 2
  %%w1 = load i64, ptr %%w1_p, align 8
  call void @free(ptr %%env)
  %%ismsg = icmp eq i64 %%kind, 0
  br i1 %%ismsg, label %%msg, label %%notmsg

msg:
  %%isw = icmp eq i64 %%side, 1
  %%cbslot_p = getelementptr %s, ptr %%ctrl, i32 0, i32 10
  %%wcbslot_p = getelementptr %s, ptr %%ctrl, i32 0, i32 13
  %%cb_pp = select i1 %%isw, ptr %%wcbslot_p, ptr %%cbslot_p
  %%cb = load ptr, ptr %%cb_pp, align 8
  %%hascb = icmp ne ptr %%cb, null
  br i1 %%hascb, label %%callcb, label %%loop

callcb:
  %%fp_p = getelementptr { ptr, ptr }, ptr %%cb, i32 0, i32 0
  %%ep_p = getelementptr { ptr, ptr }, ptr %%cb, i32 0, i32 1
  %%fp = load ptr, ptr %%fp_p, align 8
  %%ep = load ptr, ptr %%ep_p, align 8
  call void %%fp(ptr %%ep, i64 %%w0, i64 %%w1)
  br label %%loop

notmsg:
  %%isexit = icmp eq i64 %%kind, 1
  br i1 %%isexit, label %%exitenv, label %%notexit

exitenv:
  %%ex_p = getelementptr %s, ptr %%ctrl, i32 0, i32 6
  store i64 1, ptr %%ex_p, align 8
  %%code_p = getelementptr %s, ptr %%ctrl, i32 0, i32 7
  store i64 %%w0, ptr %%code_p, align 8
  %%tid_p = getelementptr %s, ptr %%ctrl, i32 0, i32 0
  %%tid = load i64, ptr %%tid_p, align 8
  call i32 @pthread_join(i64 %%tid, ptr null)
  %%ecb_p = getelementptr %s, ptr %%ctrl, i32 0, i32 11
  %%ecb = load ptr, ptr %%ecb_p, align 8
  %%hasecb = icmp ne ptr %%ecb, null
  br i1 %%hasecb, label %%callecb, label %%loop

callecb:
  %%efp_p = getelementptr { ptr, ptr }, ptr %%ecb, i32 0, i32 0
  %%eep_p = getelementptr { ptr, ptr }, ptr %%ecb, i32 0, i32 1
  %%efp = load ptr, ptr %%efp_p, align 8
  %%eep = load ptr, ptr %%eep_p, align 8
  call void %%efp(ptr %%eep, i64 %%w0, i64 %%w1)
  br label %%loop

notexit:
  %%isterm = icmp eq i64 %%kind, 2
  br i1 %%isterm, label %%termenv, label %%noterm

termenv:
  %%term_p = getelementptr %s, ptr %%ctrl, i32 0, i32 5
  store i64 1, ptr %%term_p, align 8
  ; a terminated worker reports exit code 1, matching Node's terminate()
  %%tcode_p = getelementptr %s, ptr %%ctrl, i32 0, i32 7
  store i64 1, ptr %%tcode_p, align 8
  br label %%loop

noterm:
  ; kind 3: uncaught exception on the worker thread (TDD-00098 stage 5) —
  ; w0 is the (heap) error-message string. With an 'error' listener, dispatch
  ; it; without one, print and kill the process, Node's own default.
  %%iserr = icmp eq i64 %%kind, 3
  br i1 %%iserr, label %%errenv, label %%loop

errenv:
  %%rcb_p = getelementptr %s, ptr %%ctrl, i32 0, i32 12
  %%rcb = load ptr, ptr %%rcb_p, align 8
  %%hasrcb = icmp ne ptr %%rcb, null
  br i1 %%hasrcb, label %%callrcb, label %%errfatal

callrcb:
  %%rfp_p = getelementptr { ptr, ptr }, ptr %%rcb, i32 0, i32 0
  %%rep_p = getelementptr { ptr, ptr }, ptr %%rcb, i32 0, i32 1
  %%rfp = load ptr, ptr %%rfp_p, align 8
  %%rep = load ptr, ptr %%rep_p, align 8
  call void %%rfp(ptr %%rep, i64 %%w0, i64 %%w1)
  br label %%loop

errfatal:
  %%emsg = inttoptr i64 %%w0 to ptr
  call i32 (ptr, ...) @printf(ptr %s, ptr %%emsg)
  call void @exit(i32 1)
  unreachable

done:
  ret void
}`, workerCtrlIR, workerCtrlIR, workerCtrlIR, workerCtrlIR, workerCtrlIR, workerCtrlIR, workerCtrlIR, workerCtrlIR, workerCtrlIR,
		e.internString("Uncaught (in worker): %s\n")))

	// __kml_worker_dispatch: event-loop hook — drain whichever pipes belong
	// to this thread's role.
	e.emitGlobal(fmt.Sprintf(`define void @__kml_worker_dispatch() {
entry:
  %%self = load ptr, ptr @__kml_worker_self, align 8
  %%isworker = icmp ne ptr %%self, null
  br i1 %%isworker, label %%workerside, label %%parentside

workerside:
  %%p2wr_p = getelementptr %s, ptr %%self, i32 0, i32 1
  %%p2wr = load i32, ptr %%p2wr_p, align 4
  call void @__kml_worker_drain_fd(i32 %%p2wr, ptr %%self, i64 1)
  ret void

parentside:
  %%len = load i64, ptr @__kml_workers_len, align 8
  %%data = load ptr, ptr @__kml_workers_data, align 8
  br label %%loop

loop:
  %%i = phi i64 [ 0, %%parentside ], [ %%inext, %%next ]
  %%inb = icmp slt i64 %%i, %%len
  br i1 %%inb, label %%body, label %%done

body:
  %%slot = getelementptr ptr, ptr %%data, i64 %%i
  %%ctrl = load ptr, ptr %%slot, align 8
  %%ex_p = getelementptr %s, ptr %%ctrl, i32 0, i32 6
  %%ex = load i64, ptr %%ex_p, align 8
  %%live = icmp eq i64 %%ex, 0
  br i1 %%live, label %%drain, label %%next

drain:
  %%w2pr_p = getelementptr %s, ptr %%ctrl, i32 0, i32 3
  %%w2pr = load i32, ptr %%w2pr_p, align 4
  call void @__kml_worker_drain_fd(i32 %%w2pr, ptr %%ctrl, i64 0)
  br label %%next

next:
  %%inext = add i64 %%i, 1
  br label %%loop

done:
  ret void
}`, workerCtrlIR, workerCtrlIR, workerCtrlIR))
	// __kml_worker_uncaught: called from @__kml_throw's uncaught path with
	// the error's message string (TDD-00098 stage 5). On the main thread it
	// returns (the caller prints and exits, today's behavior). On a worker
	// thread it does NOT return: it reports the error + an exit(1) envelope
	// to the parent and ends just this thread.
	e.emitGlobal(fmt.Sprintf(`define void @__kml_worker_uncaught(ptr %%msg) {
entry:
  %%self = load ptr, ptr @__kml_worker_self, align 8
  %%isworker = icmp ne ptr %%self, null
  br i1 %%isworker, label %%workerside, label %%mainside

mainside:
  ret void

workerside:
  %%w2pw_p = getelementptr %s, ptr %%self, i32 0, i32 4
  %%w2pw = load i32, ptr %%w2pw_p, align 4
  %%msgw = ptrtoint ptr %%msg to i64
  call void @__kml_worker_send_env(i32 %%w2pw, i64 3, i64 %%msgw, i64 0)
  call void @__kml_worker_send_env(i32 %%w2pw, i64 1, i64 1, i64 0)
  call void @pthread_exit(ptr null)
  unreachable
}`, workerCtrlIR))
}

