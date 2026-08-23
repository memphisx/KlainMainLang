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

	// @__kml_microtasks_pending() -> i1: the event loop's select() must not
	// block while reactions sit queued (TDD-00097 Stage 5 — a stream chain
	// advanced by a pull_settled reaction stalled behind an indefinite
	// select() before this check existed).
	e.emitGlobal(`
define i1 @__kml_microtasks_pending() {
entry:
  %head = load i64, ptr @__kml_mt_head, align 8
  %len = load i64, ptr @__kml_mt_len, align 8
  %pending = icmp slt i64 %head, %len
  ret i1 %pending
}`)

	// @__kml_drain_microtasks(): run queued callbacks FIFO until empty — a
	// callback may enqueue more (chained .then), which are drained in the same
	// pass, matching the microtask-checkpoint semantics.
	e.emitGlobal(`
define void @__kml_drain_microtasks() {
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

	// @__kml_promise_drain_reactions(ptr %p): enqueue every reaction closure
	// registered on promise %p (the { ptr closure, ptr next } list at field 4)
	// onto the microtask FIFO, then clear the list. Called by a task when it
	// settles (resolve/reject), so .then/.catch/.finally callbacks fire.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_promise_drain_reactions(ptr %%p) {
entry:
  %%rx_p = getelementptr %s, ptr %%p, i32 0, i32 4
  %%head = load ptr, ptr %%rx_p, align 8
  br label %%loop
loop:
  %%node = phi ptr [ %%head, %%entry ], [ %%next, %%body ]
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
