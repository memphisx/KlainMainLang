// runtime_fetch_bodyprom.go — TDD-00186 Part B (lazy variant): a Response body
// accessor (text()/json()/arrayBuffer()) returns a pending Promise settled off
// the fetch reactor's completion, so the download overlaps intervening work and
// a JSON parse error becomes a promise rejection — rather than driving the body
// to done at the accessor call site.
//
// A body-promise request is a { ptr pending, ptr closure } entry: `pending` is
// the Response's in-flight fetch handle, `closure` a { fn, env } whose per-kind
// settle runner (emit_fetch.go) builds the value from the now-complete body and
// settles/rejects the promise. Requests park in a thread-local registry the
// curl-drain fires by `pending` on CURLMSG_DONE. The promise + Response are kept
// reachable by the awaiting task's own stack (GC-scanned), so the registry —
// like req-body's — is plain system memory.
package llvm

func (e *Emitter) ensureFetchBodyProm() {
	if e.usedFetchBodyProm {
		return
	}
	e.usedFetchBodyProm = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureFree()
	e.ensurePromiseRuntime() // the settle runners settle the body promise
	e.ensurePromiseSettle()

	e.emitGlobal("@__kml_fbp_data = internal thread_local global ptr null, align 8")
	e.emitGlobal("@__kml_fbp_len = internal thread_local global i64 0, align 8")
	e.emitGlobal("@__kml_fbp_cap = internal thread_local global i64 0, align 8")

	// __kml_fbp_invoke(closure): call the settle runner fn(env), then free the
	// env and closure (their GC contents — the promise + Response — are rooted by
	// the awaiting task, not by this freed system memory).
	e.emitGlobal(`
define void @__kml_fbp_invoke(ptr %closure) {
entry:
  %fn_p = getelementptr { ptr, ptr }, ptr %closure, i32 0, i32 0
  %fn = load ptr, ptr %fn_p, align 8
  %env_p = getelementptr { ptr, ptr }, ptr %closure, i32 0, i32 1
  %env = load ptr, ptr %env_p, align 8
  call void %fn(ptr %env)
  call void @free(ptr %env)
  call void @free(ptr %closure)
  ret void
}`)

	// __kml_fetch_bodyprom_register(pending, closure): settle now when the body
	// is already complete (pending null, or done set), else park the request.
	e.emitGlobal(`
define void @__kml_fetch_bodyprom_register(ptr %pending, ptr %closure) {
entry:
  %isnull = icmp eq ptr %pending, null
  br i1 %isnull, label %now, label %ckdone
ckdone:
  %done_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 2
  %done = load i64, ptr %done_p, align 8
  %isdone = icmp ne i64 %done, 0
  br i1 %isdone, label %now, label %park
now:
  call void @__kml_fbp_invoke(ptr %closure)
  ret void
park:
  %len = load i64, ptr @__kml_fbp_len, align 8
  %cap = load i64, ptr @__kml_fbp_cap, align 8
  %need = add i64 %len, 1
  %grow = icmp sgt i64 %need, %cap
  br i1 %grow, label %dogrow, label %app
dogrow:
  %data = load ptr, ptr @__kml_fbp_data, align 8
  %cap2 = mul i64 %cap, 2
  %ge4 = icmp sgt i64 %cap2, 4
  %nc = select i1 %ge4, i64 %cap2, i64 4
  %bytes = mul i64 %nc, 16
  %nd = call ptr @realloc(ptr %data, i64 %bytes)
  store ptr %nd, ptr @__kml_fbp_data, align 8
  store i64 %nc, ptr @__kml_fbp_cap, align 8
  br label %app
app:
  %d = load ptr, ptr @__kml_fbp_data, align 8
  %slot = getelementptr { ptr, ptr }, ptr %d, i64 %len
  %sp0 = getelementptr { ptr, ptr }, ptr %slot, i32 0, i32 0
  store ptr %pending, ptr %sp0, align 8
  %sp1 = getelementptr { ptr, ptr }, ptr %slot, i32 0, i32 1
  store ptr %closure, ptr %sp1, align 8
  %nl = add i64 %len, 1
  store i64 %nl, ptr @__kml_fbp_len, align 8
  ret void
}`)

	// __kml_fetch_bodyprom_on_done(pending): fire and remove every request keyed
	// to this completed fetch. Removal is swap-with-last (order-independent — each
	// entry settles its own promise). Called from the curl-drain on CURLMSG_DONE.
	e.emitGlobal(`
define void @__kml_fetch_bodyprom_on_done(ptr %pending) {
entry:
  %data = load ptr, ptr @__kml_fbp_data, align 8
  br label %cond
cond:
  %i = phi i64 [ 0, %entry ], [ %inext, %next ]
  %len = load i64, ptr @__kml_fbp_len, align 8
  %go = icmp slt i64 %i, %len
  br i1 %go, label %body, label %done
body:
  %slot = getelementptr { ptr, ptr }, ptr %data, i64 %i
  %ep_p = getelementptr { ptr, ptr }, ptr %slot, i32 0, i32 0
  %ep = load ptr, ptr %ep_p, align 8
  %match = icmp eq ptr %ep, %pending
  br i1 %match, label %fire, label %next
fire:
  %clo_p = getelementptr { ptr, ptr }, ptr %slot, i32 0, i32 1
  %clo = load ptr, ptr %clo_p, align 8
  ; swap-with-last, shrink len, then advance (at most one entry per pending —
  ; a second body read on one Response is a WHATWG "disturbed" violation)
  %last = sub i64 %len, 1
  %lastslot = getelementptr { ptr, ptr }, ptr %data, i64 %last
  %l0 = getelementptr { ptr, ptr }, ptr %lastslot, i32 0, i32 0
  %lp = load ptr, ptr %l0, align 8
  %l1 = getelementptr { ptr, ptr }, ptr %lastslot, i32 0, i32 1
  %lc = load ptr, ptr %l1, align 8
  store ptr %lp, ptr %ep_p, align 8
  store ptr %lc, ptr %clo_p, align 8
  store i64 %last, ptr @__kml_fbp_len, align 8
  call void @__kml_fbp_invoke(ptr %clo)
  br label %next
next:
  %inext = add i64 %i, 1
  br label %cond
done:
  ret void
}`)
}
