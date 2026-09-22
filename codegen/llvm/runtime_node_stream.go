// runtime_node_stream.go — TDD-00097 Stage 8: Node's `stream` module
// (`Readable`/`Writable`/`Transform`, `'data'`/`'end'`/`'error'`/`'finish'`
// events, `.pipe()`), built as wrappers over the WHATWG internals: the
// readable side is an %kml.rstream, the writable side an %kml.wstream,
// events ride the EventEmitter listener-list machinery (`__kml_map_str_*` +
// `__kml_ee_list_*`), and `.pipe()`/`pipeline` ride Stage 3's pipe machine.
//
// %kml.nodestream (80 B):
//
//	0 rstream ptr    (null for a Writable)
//	1 wstream ptr    (null for a Readable)
//	2 emap ptr       event name → listener list (the EventEmitter registry)
//	3 flowing i64    0 not started · 1 flowing · 2 paused
//	4 invokeData ptr compiler thunk: void (ptr listenerClo, i64 v0, i64 v1)
//	5 decodeFn ptr   {i64,i64,i64} (ptr rec) — Stage 3's record decoder
//	6 endEmitted i64
//	7 drainArmed i64
//	8 wRegistered i64 (writable completion reaction armed)
//	9 finishedProm ptr  finished(readable)'s promise — settled once 'end'/'close' were emitted (lazily created)
package llvm

import "fmt"

const (
	nodeStreamStructIR   = "{ ptr, ptr, ptr, i64, ptr, ptr, i64, i64, i64, i64 }"
	nodeStreamStructSize = 80
)

func (e *Emitter) ensureNodeStreamRuntime() {
	if e.usedNodeStreamRuntime {
		return
	}
	e.usedNodeStreamRuntime = true
	e.ensureStreamRuntime()
	e.ensureWStreamRuntime()
	e.ensureStreamPipeRuntime() // __kml_mkclo / __kml_prom_reject / pipe machine
	e.ensureMapStrHelpers()
	e.ensureEventEmitterRuntime() // __kml_ee_list_*
	e.ensureMalloc()
	e.ensureMemcpy()

	ns := nodeStreamStructIR
	p := promiseStructIR

	// __kml_ns_alloc(rs, ws, invokeData, decode) → wrapper.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_ns_alloc(ptr %%rs, ptr %%ws, ptr %%inv, ptr %%dec) {
entry:
  %%n = call ptr @malloc(i64 %d)
  %%f0 = getelementptr %s, ptr %%n, i32 0, i32 0
  store ptr %%rs, ptr %%f0, align 8
  %%f1 = getelementptr %s, ptr %%n, i32 0, i32 1
  store ptr %%ws, ptr %%f1, align 8
  %%emap = call ptr @__kml_map_str_create()
  %%f2 = getelementptr %s, ptr %%n, i32 0, i32 2
  store ptr %%emap, ptr %%f2, align 8
  %%f3 = getelementptr %s, ptr %%n, i32 0, i32 3
  store i64 0, ptr %%f3, align 8
  %%f4 = getelementptr %s, ptr %%n, i32 0, i32 4
  store ptr %%inv, ptr %%f4, align 8
  %%f5 = getelementptr %s, ptr %%n, i32 0, i32 5
  store ptr %%dec, ptr %%f5, align 8
  %%f6 = getelementptr %s, ptr %%n, i32 0, i32 6
  store i64 0, ptr %%f6, align 8
  %%f7 = getelementptr %s, ptr %%n, i32 0, i32 7
  store i64 0, ptr %%f7, align 8
  %%f8 = getelementptr %s, ptr %%n, i32 0, i32 8
  store i64 0, ptr %%f8, align 8
  %%f9 = getelementptr %s, ptr %%n, i32 0, i32 9
  store i64 0, ptr %%f9, align 8
  ret ptr %%n
}`, nodeStreamStructSize, ns, ns, ns, ns, ns, ns, ns, ns, ns, ns))

	// __kml_ns_listeners(ns, name) → the listener list (or null).
	// __kml_ns_add_listener(ns, name, clo, once).
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_ns_listeners(ptr %%n, ptr %%name) {
entry:
  %%f2 = getelementptr %s, ptr %%n, i32 0, i32 2
  %%emap = load ptr, ptr %%f2, align 8
  %%raw = call i64 @__kml_map_str_get(ptr %%emap, ptr %%name)
  %%lst = inttoptr i64 %%raw to ptr
  ret ptr %%lst
}

define void @__kml_ns_add_listener(ptr %%n, ptr %%name, ptr %%clo, i64 %%once) {
entry:
  %%f2 = getelementptr %s, ptr %%n, i32 0, i32 2
  %%emap = load ptr, ptr %%f2, align 8
  %%raw = call i64 @__kml_map_str_get(ptr %%emap, ptr %%name)
  %%lst0 = inttoptr i64 %%raw to ptr
  %%isnull = icmp eq ptr %%lst0, null
  br i1 %%isnull, label %%mk, label %%have
mk:
  %%newlst = call ptr @__kml_ee_list_create()
  %%bits = ptrtoint ptr %%newlst to i64
  call void @__kml_map_str_set(ptr %%emap, ptr %%name, i64 %%bits)
  br label %%have
have:
  %%lst = phi ptr [ %%newlst, %%mk ], [ %%lst0, %%entry ]
  call void @__kml_ee_list_push(ptr %%lst, ptr %%clo, i64 %%once)
  ret void
}`, ns, ns))

	// __kml_ns_emit0(ns, name): invoke every zero-arg listener for name
	// (registration order, honoring once-removal).
	// __kml_ns_emit1p(ns, name, arg): same for one-pointer-arg listeners
	// (the 'error' event).
	// __kml_ns_emit_data(ns, v0, v1): 'data' listeners through the
	// compiler's typed invoke thunk.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_ns_emit_common(ptr %%n, ptr %%name, i64 %%mode, i64 %%v0, i64 %%v1) {
entry:
  %%lst = call ptr @__kml_ns_listeners(ptr %%n, ptr %%name)
  %%isnull = icmp eq ptr %%lst, null
  br i1 %%isnull, label %%done, label %%go
go:
  %%len = load i64, ptr %%lst, align 8
  %%dgep = getelementptr i8, ptr %%lst, i64 16
  %%data0 = load ptr, ptr %%dgep, align 8
  %%bytes = mul i64 %%len, 16
  %%snap = call ptr @malloc(i64 %%bytes)
  %%ign = call ptr @memcpy(ptr %%snap, ptr %%data0, i64 %%bytes)
  br label %%cond
cond:
  %%i = phi i64 [ 0, %%go ], [ %%inext, %%next ]
  %%more = icmp slt i64 %%i, %%len
  br i1 %%more, label %%body, label %%done
body:
  %%ep = getelementptr { ptr, i64 }, ptr %%snap, i64 %%i, i32 0
  %%clo = load ptr, ptr %%ep, align 8
  %%op = getelementptr { ptr, i64 }, ptr %%snap, i64 %%i, i32 1
  %%once = load i64, ptr %%op, align 8
  %%fp_p = getelementptr { ptr, ptr }, ptr %%clo, i32 0, i32 0
  %%fp = load ptr, ptr %%fp_p, align 8
  %%envp = getelementptr { ptr, ptr }, ptr %%clo, i32 0, i32 1
  %%env = load ptr, ptr %%envp, align 8
  %%m0 = icmp eq i64 %%mode, 0
  br i1 %%m0, label %%call0, label %%ck1
call0:
  call void %%fp(ptr %%env)
  br label %%after
ck1:
  %%m1 = icmp eq i64 %%mode, 1
  br i1 %%m1, label %%call1, label %%calldata
call1:
  %%argp = inttoptr i64 %%v0 to ptr
  call void %%fp(ptr %%env, ptr %%argp)
  br label %%after
calldata:
  %%f4 = getelementptr %s, ptr %%n, i32 0, i32 4
  %%inv = load ptr, ptr %%f4, align 8
  call void %%inv(ptr %%clo, i64 %%v0, i64 %%v1)
  br label %%after
after:
  %%isonce = icmp ne i64 %%once, 0
  br i1 %%isonce, label %%rm, label %%next
rm:
  %%lst2 = call ptr @__kml_ns_listeners(ptr %%n, ptr %%name)
  call void @__kml_ee_list_remove(ptr %%lst2, ptr %%clo)
  br label %%next
next:
  %%inext = add i64 %%i, 1
  br label %%cond
done:
  ret void
}`, ns))

	// __kml_ns_destroy_close(n): destroy()'s 'close' emission for a
	// readable that is NOT flowing (ADR-00483) — a flowing stream's own
	// flow loop already emits 'end'/'close' when the closed source drains,
	// so emitting here too would double-fire. Enqueued as a microtask by
	// the destroy() call site, keeping Node's asynchronous 'close' order.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_ns_destroy_close(ptr %%n) {
entry:
  %%f3 = getelementptr %s, ptr %%n, i32 0, i32 3
  %%fl = load i64, ptr %%f3, align 8
  %%isflow = icmp eq i64 %%fl, 1
  br i1 %%isflow, label %%ret, label %%emit
emit:
  call void @__kml_ns_emit_common(ptr %%n, ptr %s, i64 0, i64 0, i64 0)
  ret void
ret:
  ret void
}`, nodeStreamStructIR, e.internString("close")))

	// Flow loop: reaction-driven read → emit('data') → read …; done emits
	// 'end' then 'close'; a rejected read emits 'error'.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_ns_flow_step(ptr %%n) {
entry:
  %%f3 = getelementptr %s, ptr %%n, i32 0, i32 3
  %%fl = load i64, ptr %%f3, align 8
  %%isflow = icmp eq i64 %%fl, 1
  br i1 %%isflow, label %%go, label %%ret
go:
  %%f0 = getelementptr %s, ptr %%n, i32 0, i32 0
  %%rs = load ptr, ptr %%f0, align 8
  %%nors = icmp eq ptr %%rs, null
  br i1 %%nors, label %%ret, label %%read
read:
  %%prm = call ptr @__kml_rs_read(ptr %%rs)
  %%env = call ptr @malloc(i64 16)
  %%e0 = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0
  store ptr %%prm, ptr %%e0, align 8
  %%e1 = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1
  store ptr %%n, ptr %%e1, align 8
  %%clo = call ptr @__kml_mkclo(ptr @__kml_ns_flow_onread, ptr %%env)
  call void @__kml_promise_add_reaction(ptr %%prm, ptr %%clo)
  ret void
ret:
  ret void
}

define void @__kml_ns_flow_onread(ptr %%env) {
entry:
  %%e0 = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0
  %%prm = load ptr, ptr %%e0, align 8
  %%e1 = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1
  %%n = load ptr, ptr %%e1, align 8
  %%pst_p = getelementptr %s, ptr %%prm, i32 0, i32 0
  %%pst = load i64, ptr %%pst_p, align 8
  %%rej = icmp eq i64 %%pst, 2
  %%pv0_p = getelementptr %s, ptr %%prm, i32 0, i32 2
  %%pv0 = load i64, ptr %%pv0_p, align 8
  br i1 %%rej, label %%err, label %%ok
err:
  %%f3e = getelementptr %s, ptr %%n, i32 0, i32 3
  store i64 0, ptr %%f3e, align 8
  call void @__kml_ns_emit_common(ptr %%n, ptr %s, i64 1, i64 %%pv0, i64 0)
  ret void
ok:
  %%rec = inttoptr i64 %%pv0 to ptr
  %%f5 = getelementptr %s, ptr %%n, i32 0, i32 5
  %%dec = load ptr, ptr %%f5, align 8
  %%dv = call { i64, i64, i64 } %%dec(ptr %%rec)
  %%v0 = extractvalue { i64, i64, i64 } %%dv, 0
  %%v1 = extractvalue { i64, i64, i64 } %%dv, 1
  %%done = extractvalue { i64, i64, i64 } %%dv, 2
  %%isdone = icmp ne i64 %%done, 0
  br i1 %%isdone, label %%ended, label %%chunk
ended:
  %%f3d = getelementptr %s, ptr %%n, i32 0, i32 3
  store i64 0, ptr %%f3d, align 8
  %%f6 = getelementptr %s, ptr %%n, i32 0, i32 6
  store i64 1, ptr %%f6, align 8
  call void @__kml_ns_emit_common(ptr %%n, ptr %s, i64 0, i64 0, i64 0)
  call void @__kml_ns_emit_common(ptr %%n, ptr %s, i64 0, i64 0, i64 0)
  %%f9d = getelementptr %s, ptr %%n, i32 0, i32 9
  %%finp = load ptr, ptr %%f9d, align 8
  %%hasfin = icmp ne ptr %%finp, null
  br i1 %%hasfin, label %%finsettle, label %%finret
finsettle:
  call void @__kml_promise_settle(ptr %%finp, i64 1)
  ret void
finret:
  ret void
chunk:
  call void @__kml_ns_emit_common(ptr %%n, ptr %s, i64 2, i64 %%v0, i64 %%v1)
  call void @__kml_ns_flow_step(ptr %%n)
  ret void
}

define void @__kml_ns_flow_kick(ptr %%n) {
entry:
  call void @__kml_ns_flow_step(ptr %%n)
  ret void
}

define void @__kml_ns_start_flow(ptr %%n) {
entry:
  %%f3s = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, i64, i64, i64, i64 }, ptr %%n, i32 0, i32 3
  %%fl = load i64, ptr %%f3s, align 8
  %%fresh = icmp eq i64 %%fl, 0
  br i1 %%fresh, label %%go, label %%ret
go:
  store i64 1, ptr %%f3s, align 8
  %%clo = call ptr @__kml_mkclo(ptr @__kml_ns_flow_kick, ptr %%n)
  call void @__kml_microtask_enqueue(ptr %%clo)
  ret void
ret:
  ret void
}

define void @__kml_ns_resume(ptr %%n) {
entry:
  %%f3r = getelementptr { ptr, ptr, ptr, i64, ptr, ptr, i64, i64, i64, i64 }, ptr %%n, i32 0, i32 3
  %%fl = load i64, ptr %%f3r, align 8
  %%already = icmp eq i64 %%fl, 1
  br i1 %%already, label %%ret, label %%go
go:
  store i64 1, ptr %%f3r, align 8
  %%clo = call ptr @__kml_mkclo(ptr @__kml_ns_flow_kick, ptr %%n)
  call void @__kml_microtask_enqueue(ptr %%clo)
  ret void
ret:
  ret void
}`, ns, ns, p, p, ns, e.internString("error"), ns, ns, ns, e.internString("end"), e.internString("close"), ns, e.internString("data")))

	// __kml_ns_finished_prom(n) → finished(readable)'s promise. Node's finished()
	// resolves on the stream's 'end'/'close', i.e. after every 'data' and every
	// earlier-registered 'end' listener ran — not when the source closes, which
	// for a flowing stream is a read earlier. A flowing stream's promise is
	// settled by the flow loop after it emits 'end' and 'close'; one that is not
	// flowing (consumed by a pipe or a reader) settles with the source's closed
	// promise. A rejected closed promise rejects it either way.
	e.ensurePromiseSettle()
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_ns_finished_prom(ptr %%n, ptr %%closed) {
entry:
  %%f9 = getelementptr %[1]s, ptr %%n, i32 0, i32 9
  %%cur = load ptr, ptr %%f9, align 8
  %%have = icmp ne ptr %%cur, null
  br i1 %%have, label %%ret, label %%mk
ret:
  ret ptr %%cur
mk:
  %%fp = call ptr @__kml_task_alloc_promise()
  store ptr %%fp, ptr %%f9, align 8
  %%f6 = getelementptr %[1]s, ptr %%n, i32 0, i32 6
  %%ended = load i64, ptr %%f6, align 8
  %%isended = icmp ne i64 %%ended, 0
  br i1 %%isended, label %%now, label %%react
now:
  call void @__kml_promise_settle(ptr %%fp, i64 1)
  ret ptr %%fp
react:
  %%env = call ptr @malloc(i64 16)
  %%e0 = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0
  store ptr %%closed, ptr %%e0, align 8
  %%e1 = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1
  store ptr %%n, ptr %%e1, align 8
  %%clo = call ptr @__kml_mkclo(ptr @__kml_ns_fin_onclosed, ptr %%env)
  call void @__kml_promise_add_reaction(ptr %%closed, ptr %%clo)
  ret ptr %%fp
}

define void @__kml_ns_fin_onclosed(ptr %%env) {
entry:
  %%e0 = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0
  %%closed = load ptr, ptr %%e0, align 8
  %%e1 = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1
  %%n = load ptr, ptr %%e1, align 8
  %%f9 = getelementptr %[1]s, ptr %%n, i32 0, i32 9
  %%fp = load ptr, ptr %%f9, align 8
  %%st_p = getelementptr %[2]s, ptr %%closed, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%rej = icmp eq i64 %%st, 2
  br i1 %%rej, label %%reject, label %%ok
reject:
  %%cv0_p = getelementptr %[2]s, ptr %%closed, i32 0, i32 2
  %%cv0 = load i64, ptr %%cv0_p, align 8
  %%cv1_p = getelementptr %[2]s, ptr %%closed, i32 0, i32 3
  %%cv1 = load i64, ptr %%cv1_p, align 8
  %%fv0_p = getelementptr %[2]s, ptr %%fp, i32 0, i32 2
  store i64 %%cv0, ptr %%fv0_p, align 8
  %%fv1_p = getelementptr %[2]s, ptr %%fp, i32 0, i32 3
  store i64 %%cv1, ptr %%fv1_p, align 8
  call void @__kml_promise_settle(ptr %%fp, i64 2)
  ret void
ok:
  ; a flowing stream still has its 'end' to emit: the flow loop settles it
  %%f3 = getelementptr %[1]s, ptr %%n, i32 0, i32 3
  %%fl = load i64, ptr %%f3, align 8
  %%flowing = icmp ne i64 %%fl, 0
  %%f6 = getelementptr %[1]s, ptr %%n, i32 0, i32 6
  %%ended = load i64, ptr %%f6, align 8
  %%notended = icmp eq i64 %%ended, 0
  %%wait = and i1 %%flowing, %%notended
  br i1 %%wait, label %%leave, label %%settle
settle:
  call void @__kml_promise_settle(ptr %%fp, i64 1)
  ret void
leave:
  ret void
}`, ns, p))

	// __kml_ns_finished_both(a, b) → a promise fulfilled once both are, rejected
	// with the first rejection: finished() on a Duplex/Transform waits for the
	// readable side's 'end' and the writable side's 'finish'.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_ns_finished_both(ptr %%a, ptr %%b) {
entry:
  %%out = call ptr @__kml_task_alloc_promise()
  %%env = call ptr @malloc(i64 24)
  %%e0 = getelementptr { ptr, ptr, ptr }, ptr %%env, i32 0, i32 0
  store ptr %%out, ptr %%e0, align 8
  %%e1 = getelementptr { ptr, ptr, ptr }, ptr %%env, i32 0, i32 1
  store ptr %%a, ptr %%e1, align 8
  %%e2 = getelementptr { ptr, ptr, ptr }, ptr %%env, i32 0, i32 2
  store ptr %%b, ptr %%e2, align 8
  %%clo = call ptr @__kml_mkclo(ptr @__kml_ns_fin_both_step, ptr %%env)
  call void @__kml_promise_add_reaction(ptr %%a, ptr %%clo)
  call void @__kml_promise_add_reaction(ptr %%b, ptr %%clo)
  ret ptr %%out
}

define void @__kml_ns_fin_both_step(ptr %%env) {
entry:
  %%e0 = getelementptr { ptr, ptr, ptr }, ptr %%env, i32 0, i32 0
  %%out = load ptr, ptr %%e0, align 8
  %%e1 = getelementptr { ptr, ptr, ptr }, ptr %%env, i32 0, i32 1
  %%a = load ptr, ptr %%e1, align 8
  %%e2 = getelementptr { ptr, ptr, ptr }, ptr %%env, i32 0, i32 2
  %%b = load ptr, ptr %%e2, align 8
  %%as_p = getelementptr %[1]s, ptr %%a, i32 0, i32 0
  %%as = load i64, ptr %%as_p, align 8
  %%bs_p = getelementptr %[1]s, ptr %%b, i32 0, i32 0
  %%bs = load i64, ptr %%bs_p, align 8
  %%arej = icmp eq i64 %%as, 2
  br i1 %%arej, label %%reja, label %%ckb
ckb:
  %%brej = icmp eq i64 %%bs, 2
  br i1 %%brej, label %%rejb, label %%ckboth
reja:
  br label %%reject
rejb:
  br label %%reject
reject:
  %%src = phi ptr [ %%a, %%reja ], [ %%b, %%rejb ]
  %%sv0_p = getelementptr %[1]s, ptr %%src, i32 0, i32 2
  %%sv0 = load i64, ptr %%sv0_p, align 8
  %%sv1_p = getelementptr %[1]s, ptr %%src, i32 0, i32 3
  %%sv1 = load i64, ptr %%sv1_p, align 8
  %%os_p = getelementptr %[1]s, ptr %%out, i32 0, i32 0
  %%os = load i64, ptr %%os_p, align 8
  %%osettled = icmp ne i64 %%os, 0
  br i1 %%osettled, label %%ret, label %%doreject
doreject:
  %%ov0_p = getelementptr %[1]s, ptr %%out, i32 0, i32 2
  store i64 %%sv0, ptr %%ov0_p, align 8
  %%ov1_p = getelementptr %[1]s, ptr %%out, i32 0, i32 3
  store i64 %%sv1, ptr %%ov1_p, align 8
  call void @__kml_promise_settle(ptr %%out, i64 2)
  ret void
ckboth:
  %%aok = icmp eq i64 %%as, 1
  %%bok = icmp eq i64 %%bs, 1
  %%both = and i1 %%aok, %%bok
  br i1 %%both, label %%fulfil, label %%ret
fulfil:
  call void @__kml_promise_settle(ptr %%out, i64 1)
  ret void
ret:
  ret void
}`, p))

	// Writable completion: one reaction on the inner wstream's closed
	// promise — fulfilled emits 'finish' then 'close', rejected emits
	// 'error'. Armed once per wrapper.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_ns_arm_writable(ptr %%n) {
entry:
  %%f8 = getelementptr %s, ptr %%n, i32 0, i32 8
  %%armed = load i64, ptr %%f8, align 8
  %%is = icmp ne i64 %%armed, 0
  br i1 %%is, label %%ret, label %%arm
arm:
  store i64 1, ptr %%f8, align 8
  %%f1 = getelementptr %s, ptr %%n, i32 0, i32 1
  %%ws = load ptr, ptr %%f1, align 8
  %%cp_p = getelementptr %s, ptr %%ws, i32 0, i32 14
  %%cp = load ptr, ptr %%cp_p, align 8
  %%clo = call ptr @__kml_mkclo(ptr @__kml_ns_on_wclosed, ptr %%n)
  call void @__kml_promise_add_reaction(ptr %%cp, ptr %%clo)
  ret void
ret:
  ret void
}

define void @__kml_ns_on_wclosed(ptr %%n) {
entry:
  %%f1 = getelementptr %s, ptr %%n, i32 0, i32 1
  %%ws = load ptr, ptr %%f1, align 8
  %%cp_p = getelementptr %s, ptr %%ws, i32 0, i32 14
  %%cp = load ptr, ptr %%cp_p, align 8
  %%pst_p = getelementptr %s, ptr %%cp, i32 0, i32 0
  %%pst = load i64, ptr %%pst_p, align 8
  %%rej = icmp eq i64 %%pst, 2
  br i1 %%rej, label %%err, label %%fin
err:
  %%pv0_p = getelementptr %s, ptr %%cp, i32 0, i32 2
  %%pv0 = load i64, ptr %%pv0_p, align 8
  call void @__kml_ns_emit_common(ptr %%n, ptr %s, i64 1, i64 %%pv0, i64 0)
  ret void
fin:
  call void @__kml_ns_emit_common(ptr %%n, ptr %s, i64 0, i64 0, i64 0)
  call void @__kml_ns_emit_common(ptr %%n, ptr %s, i64 0, i64 0, i64 0)
  ret void
}

define void @__kml_ns_on_drain_ready(ptr %%n) {
entry:
  %%f7 = getelementptr %s, ptr %%n, i32 0, i32 7
  store i64 0, ptr %%f7, align 8
  call void @__kml_ns_emit_common(ptr %%n, ptr %s, i64 0, i64 0, i64 0)
  ret void
}

define i64 @__kml_ns_write_done(ptr %%n, ptr %%ws) {
entry:
  ; After a write: report !backpressure, arming one 'drain' emission per
  ; backpressure episode.
  %%d = call double @__kml_ws_desired(ptr %%ws)
  %%ok = fcmp ogt double %%d, 0.0
  br i1 %%ok, label %%fine, label %%bp
fine:
  ret i64 1
bp:
  %%f7 = getelementptr %s, ptr %%n, i32 0, i32 7
  %%armed = load i64, ptr %%f7, align 8
  %%is = icmp ne i64 %%armed, 0
  br i1 %%is, label %%no, label %%arm
arm:
  store i64 1, ptr %%f7, align 8
  %%rdy_p = getelementptr %s, ptr %%ws, i32 0, i32 13
  %%rdy = load ptr, ptr %%rdy_p, align 8
  %%clo = call ptr @__kml_mkclo(ptr @__kml_ns_on_drain_ready, ptr %%n)
  call void @__kml_promise_add_reaction(ptr %%rdy, ptr %%clo)
  ret i64 0
no:
  ret i64 0
}`, ns, ns, wstreamStructIR, ns, wstreamStructIR, p, p, e.internString("error"), e.internString("finish"), e.internString("close"), ns, e.internString("drain"), ns, wstreamStructIR))
}
