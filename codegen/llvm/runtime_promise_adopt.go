// runtime_promise_adopt.go — promise resolution with a promise (TDD-00223).
//
//	__kml_promise_adopt(ptr q, ptr h)
//	    Resolve q with promise h: q settles the way h settles, with h's value or
//	    reason. This is what `.then(cb)` does when cb returns a promise
//	    (flattening) — without it q was fulfilled with the inner promise's
//	    *pointer* as its value. Timing follows the spec: a
//	    NewPromiseResolveThenableJob microtask calls h.then(resolveQ, rejectQ),
//	    whose reaction is itself a microtask once h is settled — two ticks
//	    after the callback returned when h is already settled, so interleaving
//	    with other microtasks matches a real engine.
//
//	__kml_promise_attach(ptr p, ptr closure)
//	    The runtime form of emitAttachPromiseReaction: run `closure` as a
//	    microtask when p settles (now, if it already has).
//
//	__kml_fetch_slot_to_promise(ptr slot) -> ptr
//	    Bridge a raw fetch handle to a pending task promise. The bridge
//	    (@__kml_fetch_drive_run) runs as a *coroutine*: its wait for the
//	    response headers parks on the fetch and the event loop keeps running,
//	    where it used to be a microtask that drove the transfer synchronously
//	    and blocked every other source for the whole wait.
package llvm

import "fmt"

func (e *Emitter) ensurePromiseAdopt() {
	if e.usedPromiseAdopt {
		return
	}
	e.usedPromiseAdopt = true
	e.ensurePromiseRuntime()
	e.ensureMicrotasks()
	e.ensureMalloc()
	e.ensurePromiseSettle()
	p := promiseStructIR
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_promise_attach(ptr %%p, ptr %%clo) {
entry:
  %%res_p = getelementptr %[1]s, ptr %%p, i32 0, i32 0
  %%res = load i64, ptr %%res_p, align 8
  %%settled = icmp ne i64 %%res, 0
  br i1 %%settled, label %%now, label %%later
now:
  call void @__kml_microtask_enqueue(ptr %%clo)
  ret void
later:
  %%node = call ptr @malloc(i64 16)
  %%rx_p = getelementptr %[1]s, ptr %%p, i32 0, i32 4
  %%old = load ptr, ptr %%rx_p, align 8
  %%n_clo = getelementptr { ptr, ptr }, ptr %%node, i32 0, i32 0
  store ptr %%clo, ptr %%n_clo, align 8
  %%n_next = getelementptr { ptr, ptr }, ptr %%node, i32 0, i32 1
  store ptr %%old, ptr %%n_next, align 8
  store ptr %%node, ptr %%rx_p, align 8
  ret void
}

; the reaction h.then(resolveQ, rejectQ): copy h's settlement into q
define void @__kml_promise_adopt_settle(ptr %%env) {
entry:
  %%q_p = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0
  %%q = load ptr, ptr %%q_p, align 8
  %%h_p = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1
  %%h = load ptr, ptr %%h_p, align 8
  %%hv0_p = getelementptr %[1]s, ptr %%h, i32 0, i32 2
  %%hv0 = load i64, ptr %%hv0_p, align 8
  %%hv1_p = getelementptr %[1]s, ptr %%h, i32 0, i32 3
  %%hv1 = load i64, ptr %%hv1_p, align 8
  %%qv0_p = getelementptr %[1]s, ptr %%q, i32 0, i32 2
  store i64 %%hv0, ptr %%qv0_p, align 8
  %%qv1_p = getelementptr %[1]s, ptr %%q, i32 0, i32 3
  store i64 %%hv1, ptr %%qv1_p, align 8
  %%hs_p = getelementptr %[1]s, ptr %%h, i32 0, i32 0
  %%hs = load i64, ptr %%hs_p, align 8
  call void @__kml_promise_settle(ptr %%q, i64 %%hs)
  ret void
}

; NewPromiseResolveThenableJob: attach the settle reaction to h
define void @__kml_promise_adopt_job(ptr %%env) {
entry:
  %%h_p = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1
  %%h = load ptr, ptr %%h_p, align 8
  %%clo = call ptr @malloc(i64 16)
  %%c_fp = getelementptr { ptr, ptr }, ptr %%clo, i32 0, i32 0
  store ptr @__kml_promise_adopt_settle, ptr %%c_fp, align 8
  %%c_ep = getelementptr { ptr, ptr }, ptr %%clo, i32 0, i32 1
  store ptr %%env, ptr %%c_ep, align 8
  call void @__kml_promise_attach(ptr %%h, ptr %%clo)
  ret void
}

define void @__kml_promise_adopt(ptr %%q, ptr %%h) {
entry:
  %%env = call ptr @malloc(i64 16)
  %%e_q = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0
  store ptr %%q, ptr %%e_q, align 8
  %%e_h = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1
  store ptr %%h, ptr %%e_h, align 8
  %%clo = call ptr @malloc(i64 16)
  %%c_fp = getelementptr { ptr, ptr }, ptr %%clo, i32 0, i32 0
  store ptr @__kml_promise_adopt_job, ptr %%c_fp, align 8
  %%c_ep = getelementptr { ptr, ptr }, ptr %%clo, i32 0, i32 1
  store ptr %%env, ptr %%c_ep, align 8
  call void @__kml_microtask_enqueue(ptr %%clo)
  ret void
}`, p))
}

// ensureFetchSlotToPromise emits @__kml_fetch_slot_to_promise (see the file
// comment). Requires the coroutine runtime: the bridge parks on the fetch.
func (e *Emitter) ensureFetchSlotToPromise() {
	if e.usedFetchSlotToPromise {
		return
	}
	e.usedFetchSlotToPromise = true
	e.ensureFetchDriveRunner()
	e.ensureTaskRuntime()
	e.ensureMalloc()
	e.emitGlobal(`
define ptr @__kml_fetch_slot_to_promise(ptr %slot) {
entry:
  ; One bridge promise per fetch: p.then(f) and "await p" are reactions on
  ; the *same* promise, so they run in the order they were registered and see
  ; the same Response object — as they do in JS, where p is one promise.
  %pending = load ptr, ptr %slot, align 8
  %br_p = getelementptr { ptr, ptr, i64, i64, i64, ptr, i64, ptr, i64, ptr }, ptr %pending, i32 0, i32 9
  %have = load ptr, ptr %br_p, align 8
  %cached = icmp ne ptr %have, null
  br i1 %cached, label %reuse, label %make
reuse:
  ret ptr %have
make:
  %prom = call ptr @__kml_task_alloc_promise()
  store ptr %prom, ptr %br_p, align 8
  %env = call ptr @malloc(i64 16)
  %e_s = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 0
  store ptr %slot, ptr %e_s, align 8
  %e_p = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 1
  store ptr %prom, ptr %e_p, align 8
  ; the coroutine's own task promise is bookkeeping only: the bridge settles
  ; %prom itself (fulfilled with the Response, or rejected on a transport error)
  %taskprom = call ptr @__kml_task_alloc_promise()
  %t = call ptr @__kml_spawn_task(ptr @__kml_fetch_drive_run, ptr %env, ptr %taskprom)
  ret ptr %prom
}`)
}
