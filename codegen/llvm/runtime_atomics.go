// runtime_atomics.go — the Atomics.wait/notify runtime (TDD-00099): a
// portable futex substitute. No futex syscall exists on macOS, so waiting is
// an address-keyed linked list of waiter nodes guarded by one PROCESS-WIDE
// (deliberately not thread_local — cross-thread wakeup is the whole point)
// pthread mutex + condition variable. notify marks matching nodes and
// broadcasts; a spurious cond wakeup can never produce a false "ok" because
// only a matching notify sets a node's flag.
//
// Zero-initialized static storage is NOT a valid mutex/cond on Darwin
// (PTHREAD_MUTEX_INITIALIZER carries a nonzero signature; locking a zeroed
// one returns EINVAL — verified by prototype), so both are initialized
// lazily through a cmpxchg-guarded init the wait/notify entry points call.
//
// The plain atomic operations (Atomics.load/store/add/.../compareExchange)
// need nothing here at all — they lower directly to load atomic /
// store atomic / atomicrmw / cmpxchg in emit_atomics.go.
package llvm

import (
	"fmt"
	"runtime"
)

// etimedoutErrno is ETIMEDOUT's per-OS value (Darwin 60, Linux 110) — same
// per-OS-constant pattern as sigBlockFlag/httpNonblockFlag.
func etimedoutErrno() int {
	if runtime.GOOS == "darwin" {
		return 60
	}
	return 110
}

// ensurePthreadMutexDecls declares the pthread mutex trio exactly once
// (shared by the Atomics and channel runtimes).
func (e *Emitter) ensurePthreadMutexDecls() {
	if e.usedPthreadMutex {
		return
	}
	e.usedPthreadMutex = true
	e.emitGlobal("declare i32 @pthread_mutex_init(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @pthread_mutex_lock(ptr noundef)")
	e.emitGlobal("declare i32 @pthread_mutex_unlock(ptr noundef)")
}

func (e *Emitter) ensureAtomicsRuntime() {
	if e.usedAtomicsRuntime {
		return
	}
	e.usedAtomicsRuntime = true

	e.ensurePthreadMutexDecls()
	e.emitGlobal("declare i32 @pthread_cond_init(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @pthread_cond_wait(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @pthread_cond_timedwait(ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @pthread_cond_broadcast(ptr noundef)")
	e.ensureClockGettime()

	// Opaque storage: 64 bytes covers pthread_mutex_t (Darwin 64, glibc 40)
	// and pthread_cond_t (Darwin 48, glibc 48).
	e.emitGlobal("@__kml_atomics_mtx = internal global [64 x i8] zeroinitializer, align 8")
	e.emitGlobal("@__kml_atomics_cond = internal global [64 x i8] zeroinitializer, align 8")
	// Waiter list head: nodes are { i64 addr, i64 notified, ptr next },
	// stack-allocated in each waiter's own frame (touched by other threads
	// only while linked, and only under the mutex).
	e.emitGlobal("@__kml_atomics_waiters = internal global ptr null, align 8")
	// 0 = uninitialized, 1 = an initializer is running, 2 = ready.
	e.emitGlobal("@__kml_atomics_state = internal global i32 0, align 4")

	e.emitGlobal(`define void @__kml_atomics_init() {
entry:
  %pair = cmpxchg ptr @__kml_atomics_state, i32 0, i32 1 seq_cst seq_cst
  %won = extractvalue { i32, i1 } %pair, 1
  br i1 %won, label %doinit, label %waitready

doinit:
  call i32 @pthread_mutex_init(ptr @__kml_atomics_mtx, ptr null)
  call i32 @pthread_cond_init(ptr @__kml_atomics_cond, ptr null)
  store atomic i32 2, ptr @__kml_atomics_state seq_cst, align 4
  ret void

waitready:
  %st = load atomic i32, ptr @__kml_atomics_state seq_cst, align 4
  %ready = icmp eq i32 %st, 2
  br i1 %ready, label %done, label %waitready

done:
  ret void
}`)

	// __kml_atomics_unlink: remove node from the waiter list. Caller holds
	// the mutex.
	e.emitGlobal(`define void @__kml_atomics_unlink(ptr %node) {
entry:
  %head = load ptr, ptr @__kml_atomics_waiters, align 8
  %isself = icmp eq ptr %head, %node
  br i1 %isself, label %pop, label %scaninit

pop:
  %nx_p0 = getelementptr { i64, i64, ptr }, ptr %node, i32 0, i32 2
  %nx0 = load ptr, ptr %nx_p0, align 8
  store ptr %nx0, ptr @__kml_atomics_waiters, align 8
  ret void

scaninit:
  br label %scan

scan:
  %p = phi ptr [ %head, %scaninit ], [ %pn, %notfound ]
  %pnull = icmp eq ptr %p, null
  br i1 %pnull, label %out, label %body

body:
  %pn_p = getelementptr { i64, i64, ptr }, ptr %p, i32 0, i32 2
  %pn = load ptr, ptr %pn_p, align 8
  %found = icmp eq ptr %pn, %node
  br i1 %found, label %relink, label %notfound

relink:
  %nx_p1 = getelementptr { i64, i64, ptr }, ptr %node, i32 0, i32 2
  %nx1 = load ptr, ptr %nx_p1, align 8
  store ptr %nx1, ptr %pn_p, align 8
  ret void

notfound:
  br label %scan

out:
  ret void
}`)

	// __kml_atomics_wait(addr, expected, tmoms): block until notified on
	// addr. tmoms < 0 means wait forever. Returns 0 "ok", 1 "not-equal",
	// 2 "timed-out". The value check happens under the mutex, closing the
	// check-then-sleep race against a concurrent store+notify.
	e.emitGlobal(fmt.Sprintf(`define i64 @__kml_atomics_wait(ptr %%addr, i32 %%expected, double %%tmoms) {
entry:
  call void @__kml_atomics_init()
  call i32 @pthread_mutex_lock(ptr @__kml_atomics_mtx)
  %%cur = load atomic i32, ptr %%addr seq_cst, align 4
  %%eq = icmp eq i32 %%cur, %%expected
  br i1 %%eq, label %%enqueue, label %%notequal

notequal:
  call i32 @pthread_mutex_unlock(ptr @__kml_atomics_mtx)
  ret i64 1

enqueue:
  %%node = alloca { i64, i64, ptr }, align 8
  %%ts = alloca { i64, i64 }, align 8
  %%addri = ptrtoint ptr %%addr to i64
  %%na_p = getelementptr { i64, i64, ptr }, ptr %%node, i32 0, i32 0
  store i64 %%addri, ptr %%na_p, align 8
  %%nf_p = getelementptr { i64, i64, ptr }, ptr %%node, i32 0, i32 1
  store i64 0, ptr %%nf_p, align 8
  %%nx_p = getelementptr { i64, i64, ptr }, ptr %%node, i32 0, i32 2
  %%head = load ptr, ptr @__kml_atomics_waiters, align 8
  store ptr %%head, ptr %%nx_p, align 8
  store ptr %%node, ptr @__kml_atomics_waiters, align 8
  %%hastmo = fcmp oge double %%tmoms, 0.0
  br i1 %%hastmo, label %%deadline, label %%waitloop

deadline:
  ; absolute deadline = CLOCK_REALTIME now + tmoms (pthread_cond_timedwait
  ; takes an absolute realtime timespec on both platforms)
  call i32 @clock_gettime(i32 0, ptr %%ts)
  %%sec_p = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 0
  %%nsec_p = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 1
  %%sec = load i64, ptr %%sec_p, align 8
  %%nsec = load i64, ptr %%nsec_p, align 8
  %%tmons_f = fmul double %%tmoms, 1.0e6
  %%tmons = fptosi double %%tmons_f to i64
  %%addsec = sdiv i64 %%tmons, 1000000000
  %%addns = srem i64 %%tmons, 1000000000
  %%ns1 = add i64 %%nsec, %%addns
  %%sec1 = add i64 %%sec, %%addsec
  %%ovf = icmp sge i64 %%ns1, 1000000000
  %%sec1p = add i64 %%sec1, 1
  %%ns1m = sub i64 %%ns1, 1000000000
  %%sec2 = select i1 %%ovf, i64 %%sec1p, i64 %%sec1
  %%ns2 = select i1 %%ovf, i64 %%ns1m, i64 %%ns1
  store i64 %%sec2, ptr %%sec_p, align 8
  store i64 %%ns2, ptr %%nsec_p, align 8
  br label %%waitloop

waitloop:
  %%nf = load i64, ptr %%nf_p, align 8
  %%woken = icmp ne i64 %%nf, 0
  br i1 %%woken, label %%woke, label %%dowait

dowait:
  br i1 %%hastmo, label %%timed, label %%untimed

untimed:
  call i32 @pthread_cond_wait(ptr @__kml_atomics_cond, ptr @__kml_atomics_mtx)
  br label %%waitloop

timed:
  %%rc = call i32 @pthread_cond_timedwait(ptr @__kml_atomics_cond, ptr @__kml_atomics_mtx, ptr %%ts)
  %%isto = icmp eq i32 %%rc, %d
  br i1 %%isto, label %%tocheck, label %%waitloop

tocheck:
  ; a notify may have landed in the same instant the timeout fired — the
  ; node's flag, read under the mutex, is the truth
  %%nf2 = load i64, ptr %%nf_p, align 8
  %%woken2 = icmp ne i64 %%nf2, 0
  br i1 %%woken2, label %%woke, label %%timedout

woke:
  call void @__kml_atomics_unlink(ptr %%node)
  call i32 @pthread_mutex_unlock(ptr @__kml_atomics_mtx)
  ret i64 0

timedout:
  call void @__kml_atomics_unlink(ptr %%node)
  call i32 @pthread_mutex_unlock(ptr @__kml_atomics_mtx)
  ret i64 2
}`, etimedoutErrno()))

	// __kml_atomics_notify(addr, count): mark up to count waiters on addr
	// notified, broadcast, return how many were marked.
	e.emitGlobal(`define i64 @__kml_atomics_notify(ptr %addr, i64 %count) {
entry:
  call void @__kml_atomics_init()
  call i32 @pthread_mutex_lock(ptr @__kml_atomics_mtx)
  %addri = ptrtoint ptr %addr to i64
  %first = load ptr, ptr @__kml_atomics_waiters, align 8
  br label %loop

loop:
  %cur = phi ptr [ %first, %entry ], [ %next, %advance ]
  %marked = phi i64 [ 0, %entry ], [ %markednext, %advance ]
  %isnull = icmp eq ptr %cur, null
  br i1 %isnull, label %done, label %check

check:
  %na_p = getelementptr { i64, i64, ptr }, ptr %cur, i32 0, i32 0
  %na = load i64, ptr %na_p, align 8
  %amatch = icmp eq i64 %na, %addri
  %nf_p = getelementptr { i64, i64, ptr }, ptr %cur, i32 0, i32 1
  %nf = load i64, ptr %nf_p, align 8
  %unnotified = icmp eq i64 %nf, 0
  %room = icmp slt i64 %marked, %count
  %m0 = and i1 %amatch, %unnotified
  %m1 = and i1 %m0, %room
  br i1 %m1, label %mark, label %skip

mark:
  store i64 1, ptr %nf_p, align 8
  br label %advance

skip:
  br label %advance

advance:
  %minc = phi i64 [ 1, %mark ], [ 0, %skip ]
  %markednext = add i64 %marked, %minc
  %nx_p = getelementptr { i64, i64, ptr }, ptr %cur, i32 0, i32 2
  %next = load ptr, ptr %nx_p, align 8
  br label %loop

done:
  %any = icmp sgt i64 %marked, 0
  br i1 %any, label %bcast, label %out

bcast:
  call i32 @pthread_cond_broadcast(ptr @__kml_atomics_cond)
  br label %out

out:
  call i32 @pthread_mutex_unlock(ptr @__kml_atomics_mtx)
  ret i64 %marked
}`)
}
