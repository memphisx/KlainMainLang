// runtime_microtask.go — the microtask FIFO (TDD-00083 Stage 3): queueMicrotask
// and the reactions scheduled by Promise.prototype.then/.catch/.finally are run
// here, drained at the spec-reachable points (end of the top-level script, each
// scheduler step, each fired timer). Entries are closure headers {funcPtr,
// envPtr} invoked as 0-arg callbacks — a `.then` reaction is wrapped into such a
// closure at its call site (see emitPromiseThen), so this queue stays generic.
package llvm

import "fmt"

// ensureMicrotasks emits the microtask FIFO + enqueue/drain once.
func (e *Emitter) ensureMicrotasks() {
	if e.usedMicrotasks {
		return
	}
	e.usedMicrotasks = true
	e.ensureMalloc()

	e.emitGlobal("@__kml_mt_data = internal thread_local global ptr null, align 8")
	e.emitGlobal("@__kml_mt_len  = internal thread_local global i64 0, align 8")
	e.emitGlobal("@__kml_mt_cap  = internal thread_local global i64 0, align 8")
	e.emitGlobal("@__kml_mt_head = internal thread_local global i64 0, align 8")
	e.ensureRealloc()

	// @__kml_microtask_enqueue(ptr %cl): append a closure header pointer.
	e.emitGlobal(`
define void @__kml_microtask_enqueue(ptr %cl) {
entry:
  %len = load i64, ptr @__kml_mt_len, align 8
  %cap = load i64, ptr @__kml_mt_cap, align 8
  %need = add i64 %len, 1
  %grow = icmp sgt i64 %need, %cap
  br i1 %grow, label %dogrow, label %app
dogrow:
  %data = load ptr, ptr @__kml_mt_data, align 8
  %cap2 = mul i64 %cap, 2
  %ge8 = icmp sgt i64 %cap2, 8
  %nc = select i1 %ge8, i64 %cap2, i64 8
  %bytes = mul i64 %nc, 8
  %nd = call ptr @realloc(ptr %data, i64 %bytes)
  store ptr %nd, ptr @__kml_mt_data, align 8
  store i64 %nc, ptr @__kml_mt_cap, align 8
  br label %app
app:
  %d = load ptr, ptr @__kml_mt_data, align 8
  %slot = getelementptr ptr, ptr %d, i64 %len
  store ptr %cl, ptr %slot, align 8
  %nl = add i64 %len, 1
  store i64 %nl, ptr @__kml_mt_len, align 8
  ret void
}`)

	// The process.nextTick queue, apart from the promise jobs as in Node:
	// every tick runs before the next promise job (processTicksAndRejections).
	e.emitGlobal("@__kml_tq_data = internal thread_local global ptr null, align 8")
	e.emitGlobal("@__kml_tq_len  = internal thread_local global i64 0, align 8")
	e.emitGlobal("@__kml_tq_cap  = internal thread_local global i64 0, align 8")
	e.emitGlobal("@__kml_tq_head = internal thread_local global i64 0, align 8")
	e.emitGlobal(`
define void @__kml_nexttick_enqueue(ptr %cl) {
entry:
  %len = load i64, ptr @__kml_tq_len, align 8
  %cap = load i64, ptr @__kml_tq_cap, align 8
  %need = add i64 %len, 1
  %grow = icmp sgt i64 %need, %cap
  br i1 %grow, label %dogrow, label %app
dogrow:
  %data = load ptr, ptr @__kml_tq_data, align 8
  %cap2 = mul i64 %cap, 2
  %ge8 = icmp sgt i64 %cap2, 8
  %nc = select i1 %ge8, i64 %cap2, i64 8
  %bytes = mul i64 %nc, 8
  %nd = call ptr @realloc(ptr %data, i64 %bytes)
  store ptr %nd, ptr @__kml_tq_data, align 8
  store i64 %nc, ptr @__kml_tq_cap, align 8
  br label %app
app:
  %d = load ptr, ptr @__kml_tq_data, align 8
  %slot = getelementptr ptr, ptr %d, i64 %len
  store ptr %cl, ptr %slot, align 8
  %nl = add i64 %len, 1
  store i64 %nl, ptr @__kml_tq_len, align 8
  ret void
}`)
	e.emitGlobal(`
define void @__kml_drain_ticks() {
entry:
  br label %loop
loop:
  %head = load i64, ptr @__kml_tq_head, align 8
  %len = load i64, ptr @__kml_tq_len, align 8
  %more = icmp slt i64 %head, %len
  br i1 %more, label %run, label %done
run:
  %data = load ptr, ptr @__kml_tq_data, align 8
  %slot = getelementptr ptr, ptr %data, i64 %head
  %cl = load ptr, ptr %slot, align 8
  %nh = add i64 %head, 1
  store i64 %nh, ptr @__kml_tq_head, align 8
  %fp_p = getelementptr { ptr, ptr }, ptr %cl, i32 0, i32 0
  %fp = load ptr, ptr %fp_p, align 8
  %ep_p = getelementptr { ptr, ptr }, ptr %cl, i32 0, i32 1
  %ep = load ptr, ptr %ep_p, align 8
  call void (ptr) %fp(ptr %ep)
  br label %loop
done:
  store i64 0, ptr @__kml_tq_head, align 8
  store i64 0, ptr @__kml_tq_len, align 8
  ret void
}`)

	// @__kml_microtasks_pending() -> i1: the event loop's select() must not
	// block while reactions or ticks sit queued (TDD-00097 Stage 5 — a stream
	// chain advanced by a pull_settled reaction stalled behind an indefinite
	// select() before this check existed).
	e.emitGlobal(`
define i1 @__kml_microtasks_pending() {
entry:
  %head = load i64, ptr @__kml_mt_head, align 8
  %len = load i64, ptr @__kml_mt_len, align 8
  %pending = icmp slt i64 %head, %len
  %th = load i64, ptr @__kml_tq_head, align 8
  %tl = load i64, ptr @__kml_tq_len, align 8
  %tpending = icmp slt i64 %th, %tl
  %any = or i1 %pending, %tpending
  ret i1 %any
}`)

	// @__kml_drain_microtasks(): Node's processTicksAndRejections — every
	// queued tick, then every promise job (a job may queue more, drained in
	// the same pass), again while a job queued a tick.
	e.emitGlobal(`
define void @__kml_drain_microtasks() {
entry:
  br label %loop
loop:
  call void @__kml_drain_ticks()
  call void @__kml_drain_promise_jobs()
  %th = load i64, ptr @__kml_tq_head, align 8
  %tl = load i64, ptr @__kml_tq_len, align 8
  %again = icmp slt i64 %th, %tl
  br i1 %again, label %loop, label %done
done:
  ret void
}`)

	// @__kml_drain_promise_jobs(): run queued promise jobs FIFO until empty
	// — a job may enqueue more (chained .then), which are drained in the same
	// pass, matching the microtask-checkpoint semantics.
	e.emitGlobal(`
define void @__kml_drain_promise_jobs() {
entry:
  br label %loop
loop:
  %head = load i64, ptr @__kml_mt_head, align 8
  %len = load i64, ptr @__kml_mt_len, align 8
  %more = icmp slt i64 %head, %len
  br i1 %more, label %run, label %done
run:
  %data = load ptr, ptr @__kml_mt_data, align 8
  %slot = getelementptr ptr, ptr %data, i64 %head
  %cl = load ptr, ptr %slot, align 8
  %nh = add i64 %head, 1
  store i64 %nh, ptr @__kml_mt_head, align 8
  %fp_p = getelementptr { ptr, ptr }, ptr %cl, i32 0, i32 0
  %fp = load ptr, ptr %fp_p, align 8
  %ep_p = getelementptr { ptr, ptr }, ptr %cl, i32 0, i32 1
  %ep = load ptr, ptr %ep_p, align 8
  call void (ptr) %fp(ptr %ep)
  br label %loop
done:
  store i64 0, ptr @__kml_mt_head, align 8
  store i64 0, ptr @__kml_mt_len, align 8
  ret void
}`)

	// @__kml_microtask_tick(): the one tick a module top-level `await` of a plain
	// value or an already-settled promise takes. In JS the continuation is queued
	// BEHIND the jobs already waiting and AHEAD of whatever those jobs enqueue
	// (`await 1` between two `.then` links interleaves: tick 1, await 1, tick 2,
	// …). Top-level code runs on the main stack, not as a coroutine, so instead
	// of queueing itself it runs exactly the jobs queued at this moment and then
	// carries on — the same order. Both bounds are re-read every iteration: a job
	// may run a full drain of its own, which resets the indices.
	e.emitGlobal(`
define void @__kml_microtask_tick() {
entry:
  %snap = load i64, ptr @__kml_mt_len, align 8
  br label %loop
loop:
  %head = load i64, ptr @__kml_mt_head, align 8
  %len = load i64, ptr @__kml_mt_len, align 8
  %insnap = icmp slt i64 %head, %snap
  %inq = icmp slt i64 %head, %len
  %more = and i1 %insnap, %inq
  br i1 %more, label %run, label %done
run:
  %data = load ptr, ptr @__kml_mt_data, align 8
  %slot = getelementptr ptr, ptr %data, i64 %head
  %cl = load ptr, ptr %slot, align 8
  %nh = add i64 %head, 1
  store i64 %nh, ptr @__kml_mt_head, align 8
  %fp_p = getelementptr { ptr, ptr }, ptr %cl, i32 0, i32 0
  %fp = load ptr, ptr %fp_p, align 8
  %ep_p = getelementptr { ptr, ptr }, ptr %cl, i32 0, i32 1
  %ep = load ptr, ptr %ep_p, align 8
  call void (ptr) %fp(ptr %ep)
  br label %loop
done:
  %h2 = load i64, ptr @__kml_mt_head, align 8
  %l2 = load i64, ptr @__kml_mt_len, align 8
  %empty = icmp sge i64 %h2, %l2
  br i1 %empty, label %reset, label %out
reset:
  store i64 0, ptr @__kml_mt_head, align 8
  store i64 0, ptr @__kml_mt_len, align 8
  br label %out
out:
  ret void
}`)

	// @__kml_promise_drain_reactions(ptr %p): enqueue every reaction closure
	// registered on promise %p (the { ptr closure, ptr next } list at field 4)
	// onto the microtask FIFO, then clear the list. Called by a task when it
	// settles (resolve/reject), so .then/.catch/.finally callbacks fire.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_promise_drain_reactions(ptr %%p) {
entry:
  %%rx_p = getelementptr %s, ptr %%p, i32 0, i32 4
  %%head0 = load ptr, ptr %%rx_p, align 8
  br label %%rev
rev:
  ; The list is pushed at its head, so it holds the reactions newest-first;
  ; they must run in the order they were registered (p.then(A); p.then(B) runs
  ; A then B). Reverse it in place before enqueueing.
  %%rprev = phi ptr [ null, %%entry ], [ %%rcur, %%revbody ]
  %%rcur = phi ptr [ %%head0, %%entry ], [ %%rnext, %%revbody ]
  %%rdone = icmp eq ptr %%rcur, null
  br i1 %%rdone, label %%revdone, label %%revbody
revbody:
  %%rnext_p = getelementptr { ptr, ptr }, ptr %%rcur, i32 0, i32 1
  %%rnext = load ptr, ptr %%rnext_p, align 8
  store ptr %%rprev, ptr %%rnext_p, align 8
  br label %%rev
revdone:
  br label %%loop
loop:
  %%node = phi ptr [ %%rprev, %%revdone ], [ %%next, %%body ]
  %%isnull = icmp eq ptr %%node, null
  br i1 %%isnull, label %%done, label %%body
body:
  %%cl_p = getelementptr { ptr, ptr }, ptr %%node, i32 0, i32 0
  %%cl = load ptr, ptr %%cl_p, align 8
  call void @__kml_microtask_enqueue(ptr %%cl)
  %%next_p = getelementptr { ptr, ptr }, ptr %%node, i32 0, i32 1
  %%next = load ptr, ptr %%next_p, align 8
  br label %%loop
done:
  store ptr null, ptr %%rx_p, align 8
  ret void
}`, promiseStructIR))
}
