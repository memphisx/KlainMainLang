// runtime_streams_pipe.go — TDD-00097 Stage 3: the pipeTo state machine,
// TransformStream coupling, and tee(), all driven purely by promise reactions
// (no fiber). Struct IR names are substituted with a string replacer (not
// fmt %s ordering) — the earlier files' positional-argument slips motivated
// the switch.
//
// %kml.pipe (80 B): 0 src rstream · 1 dst wstream · 2 prom (pipe promise) ·
// 3 decodeFn ({i64,i64,i64}(ptr rec) — compiler-emitted per chunk type) ·
// 4 flags (1 preventClose · 2 preventAbort · 4 preventCancel) · 5 sigA (ptr
// to the AbortSignal's aborted flag, or null) · 6 sigR (ptr to its reason
// slot, or null) · 7/8 pending chunk words · 9 curProm (the promise the
// current reaction fires on).
//
// %kml.tee (72 B): 0 src · 1 b1 · 2 b2 · 3 decodeFn · 4 reading ·
// 5 canceled1 · 6 canceled2 · 7 reason1 · 8 reason2.
//
// %kml.ts (72 B): 0 readable · 1 writable · 2 transClo · 3 flushClo ·
// 4 parked flag · 5/6 parked chunk words · 7 parkedProm (the in-flight
// writable write promise while a chunk waits for readable capacity) ·
// 8 closeProm (settled once flush + readable close complete).
package llvm

import "strings"

func streamIRExpand(src string) string {
	return strings.NewReplacer(
		"RSTREAM", rstreamStructIR,
		"WSTREAM", wstreamStructIR,
		"PROMISE", promiseStructIR,
		"PIPECTX", "{ ptr, ptr, ptr, ptr, i64, ptr, ptr, i64, i64, ptr }",
		"TEECTX", "{ ptr, ptr, ptr, ptr, i64, i64, i64, i64, i64 }",
		"TSCTX", "{ ptr, ptr, ptr, ptr, i64, i64, i64, ptr, ptr }",
	).Replace(src)
}

// rejectPromiseIR emits "store err into %prom v0, settle rejected" — used
// inline below via the shared helper.
func (e *Emitter) ensureStreamPipeRuntime() {
	if e.usedStreamPipeRuntime {
		return
	}
	e.usedStreamPipeRuntime = true
	e.ensureStreamRuntime()
	e.ensureWStreamRuntime()

	// Small shared helpers: reject a promise with err bits; make a
	// {fn, env} closure header.
	e.emitGlobal(streamIRExpand(`
define void @__kml_prom_reject(ptr %prom, i64 %err) {
entry:
  %v0_p = getelementptr PROMISE, ptr %prom, i32 0, i32 2
  store i64 %err, ptr %v0_p, align 8
  call void @__kml_promise_settle(ptr %prom, i64 2)
  ret void
}

define ptr @__kml_mkclo(ptr %fn, ptr %env) {
entry:
  %clo = call ptr @malloc(i64 16)
  %c0 = getelementptr { ptr, ptr }, ptr %clo, i32 0, i32 0
  store ptr %fn, ptr %c0, align 8
  %c1 = getelementptr { ptr, ptr }, ptr %clo, i32 0, i32 1
  store ptr %env, ptr %c1, align 8
  ret ptr %clo
}`))

	// __kml_pipe_to: allocate the pipe context and start the loop.
	e.emitGlobal(streamIRExpand(`
define ptr @__kml_pipe_to(ptr %src, ptr %dst, ptr %decode, i64 %flags, ptr %sigA, ptr %sigR) {
entry:
  %ctx = call ptr @malloc(i64 80)
  %f0 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 0
  store ptr %src, ptr %f0, align 8
  %f1 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 1
  store ptr %dst, ptr %f1, align 8
  %prom = call ptr @__kml_task_alloc_promise()
  %f2 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 2
  store ptr %prom, ptr %f2, align 8
  %f3 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 3
  store ptr %decode, ptr %f3, align 8
  %f4 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 4
  store i64 %flags, ptr %f4, align 8
  %f5 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 5
  store ptr %sigA, ptr %f5, align 8
  %f6 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 6
  store ptr %sigR, ptr %f6, align 8
  call void @__kml_pipe_step(ptr %ctx)
  ret ptr %prom
}

define void @__kml_pipe_step(ptr %ctx) {
entry:
  %f5 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 5
  %sigA = load ptr, ptr %f5, align 8
  %nosig = icmp eq ptr %sigA, null
  br i1 %nosig, label %read, label %cksig
cksig:
  %ab = load i8, ptr %sigA, align 1
  %aborted = icmp ne i8 %ab, 0
  br i1 %aborted, label %sigabort, label %read
sigabort:
  call void @__kml_pipe_signal_abort(ptr %ctx)
  ret void
read:
  %f0 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 0
  %src = load ptr, ptr %f0, align 8
  %p = call ptr @__kml_rs_read(ptr %src)
  %f9 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 9
  store ptr %p, ptr %f9, align 8
  %clo = call ptr @__kml_mkclo(ptr @__kml_pipe_on_read, ptr %ctx)
  call void @__kml_promise_add_reaction(ptr %p, ptr %clo)
  ret void
}

define void @__kml_pipe_signal_abort(ptr %ctx) {
entry:
  %f6 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 6
  %sigR = load ptr, ptr %f6, align 8
  %noreason = icmp eq ptr %sigR, null
  br i1 %noreason, label %zero, label %loadr
loadr:
  %r0 = load i64, ptr %sigR, align 8
  br label %go
zero:
  br label %go
go:
  %reason = phi i64 [ %r0, %loadr ], [ 0, %zero ]
  %f4 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 4
  %flags = load i64, ptr %f4, align 8
  %pcBit = and i64 %flags, 4
  %pc = icmp ne i64 %pcBit, 0
  br i1 %pc, label %skipCancel, label %doCancel
doCancel:
  %f0 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 0
  %src = load ptr, ptr %f0, align 8
  %ignored = call ptr @__kml_rs_cancel(ptr %src, i64 %reason)
  br label %skipCancel
skipCancel:
  %paBit = and i64 %flags, 2
  %pa = icmp ne i64 %paBit, 0
  br i1 %pa, label %skipAbort, label %doAbort
doAbort:
  %f1 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 1
  %dst = load ptr, ptr %f1, align 8
  %ignored2 = call ptr @__kml_ws_abort(ptr %dst, i64 %reason)
  br label %skipAbort
skipAbort:
  %f2 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 2
  %prom = load ptr, ptr %f2, align 8
  call void @__kml_prom_reject(ptr %prom, i64 %reason)
  ret void
}

define void @__kml_pipe_on_read(ptr %ctx) {
entry:
  %f9 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 9
  %p = load ptr, ptr %f9, align 8
  %pst_p = getelementptr PROMISE, ptr %p, i32 0, i32 0
  %pst = load i64, ptr %pst_p, align 8
  %rejected = icmp eq i64 %pst, 2
  br i1 %rejected, label %srcerr, label %ok
srcerr:
  %pv0_p = getelementptr PROMISE, ptr %p, i32 0, i32 2
  %err = load i64, ptr %pv0_p, align 8
  %f4 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 4
  %flags = load i64, ptr %f4, align 8
  %paBit = and i64 %flags, 2
  %pa = icmp ne i64 %paBit, 0
  br i1 %pa, label %rej, label %doAbort
doAbort:
  %f1 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 1
  %dst = load ptr, ptr %f1, align 8
  %ignored = call ptr @__kml_ws_abort(ptr %dst, i64 %err)
  br label %rej
rej:
  %f2 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 2
  %prom = load ptr, ptr %f2, align 8
  call void @__kml_prom_reject(ptr %prom, i64 %err)
  ret void
ok:
  %rv0_p = getelementptr PROMISE, ptr %p, i32 0, i32 2
  %recBits = load i64, ptr %rv0_p, align 8
  %rec = inttoptr i64 %recBits to ptr
  %f3 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 3
  %decode = load ptr, ptr %f3, align 8
  %dv = call { i64, i64, i64 } %decode(ptr %rec)
  %v0 = extractvalue { i64, i64, i64 } %dv, 0
  %v1 = extractvalue { i64, i64, i64 } %dv, 1
  %done = extractvalue { i64, i64, i64 } %dv, 2
  %isDone = icmp ne i64 %done, 0
  br i1 %isDone, label %closing, label %haveChunk
closing:
  %f4b = getelementptr PIPECTX, ptr %ctx, i32 0, i32 4
  %flagsb = load i64, ptr %f4b, align 8
  %pcBit = and i64 %flagsb, 1
  %pc = icmp ne i64 %pcBit, 0
  %f2b = getelementptr PIPECTX, ptr %ctx, i32 0, i32 2
  %promb = load ptr, ptr %f2b, align 8
  br i1 %pc, label %finish, label %doClose
doClose:
  %f1b = getelementptr PIPECTX, ptr %ctx, i32 0, i32 1
  %dstb = load ptr, ptr %f1b, align 8
  %cp = call ptr @__kml_ws_close(ptr %dstb)
  %nocp = icmp eq ptr %cp, null
  br i1 %nocp, label %finish, label %waitClose
waitClose:
  store ptr %cp, ptr %f9, align 8
  %clo = call ptr @__kml_mkclo(ptr @__kml_pipe_on_closed, ptr %ctx)
  call void @__kml_promise_add_reaction(ptr %cp, ptr %clo)
  ret void
finish:
  call void @__kml_promise_settle(ptr %promb, i64 1)
  ret void
haveChunk:
  %f7 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 7
  store i64 %v0, ptr %f7, align 8
  %f8 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 8
  store i64 %v1, ptr %f8, align 8
  %f1c = getelementptr PIPECTX, ptr %ctx, i32 0, i32 1
  %dstc = load ptr, ptr %f1c, align 8
  %rdy_p = getelementptr WSTREAM, ptr %dstc, i32 0, i32 13
  %rdy = load ptr, ptr %rdy_p, align 8
  %rst_p = getelementptr PROMISE, ptr %rdy, i32 0, i32 0
  %rst = load i64, ptr %rst_p, align 8
  %fulfilled = icmp eq i64 %rst, 1
  br i1 %fulfilled, label %writeNow, label %waitReady
writeNow:
  call void @__kml_pipe_on_ready(ptr %ctx)
  ret void
waitReady:
  %clo2 = call ptr @__kml_mkclo(ptr @__kml_pipe_on_ready, ptr %ctx)
  call void @__kml_promise_add_reaction(ptr %rdy, ptr %clo2)
  ret void
}

define void @__kml_pipe_on_ready(ptr %ctx) {
entry:
  %f1 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 1
  %dst = load ptr, ptr %f1, align 8
  %dst_st_p = getelementptr WSTREAM, ptr %dst, i32 0, i32 0
  %dst_st = load i64, ptr %dst_st_p, align 8
  %errored = icmp eq i64 %dst_st, 2
  br i1 %errored, label %desterr, label %write
desterr:
  %derr_p = getelementptr WSTREAM, ptr %dst, i32 0, i32 1
  %derr = load i64, ptr %derr_p, align 8
  call void @__kml_pipe_dest_error(ptr %ctx, i64 %derr)
  ret void
write:
  %f7 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 7
  %v0 = load i64, ptr %f7, align 8
  %f8 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 8
  %v1 = load i64, ptr %f8, align 8
  %wp = call ptr @__kml_ws_write(ptr %dst, i64 %v0, i64 %v1)
  %nowp = icmp eq ptr %wp, null
  br i1 %nowp, label %closedDest, label %waitWrite
closedDest:
  %f2 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 2
  %prom = load ptr, ptr %f2, align 8
  call void @__kml_promise_settle(ptr %prom, i64 1)
  ret void
waitWrite:
  %f9 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 9
  store ptr %wp, ptr %f9, align 8
  %clo = call ptr @__kml_mkclo(ptr @__kml_pipe_on_written, ptr %ctx)
  call void @__kml_promise_add_reaction(ptr %wp, ptr %clo)
  ret void
}

define void @__kml_pipe_dest_error(ptr %ctx, i64 %err) {
entry:
  %f4 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 4
  %flags = load i64, ptr %f4, align 8
  %pcBit = and i64 %flags, 4
  %pc = icmp ne i64 %pcBit, 0
  br i1 %pc, label %rej, label %doCancel
doCancel:
  %f0 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 0
  %src = load ptr, ptr %f0, align 8
  %ignored = call ptr @__kml_rs_cancel(ptr %src, i64 %err)
  br label %rej
rej:
  %f2 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 2
  %prom = load ptr, ptr %f2, align 8
  call void @__kml_prom_reject(ptr %prom, i64 %err)
  ret void
}

define void @__kml_pipe_on_written(ptr %ctx) {
entry:
  %f9 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 9
  %wp = load ptr, ptr %f9, align 8
  %pst_p = getelementptr PROMISE, ptr %wp, i32 0, i32 0
  %pst = load i64, ptr %pst_p, align 8
  %rejected = icmp eq i64 %pst, 2
  br i1 %rejected, label %err, label %next
err:
  %pv0_p = getelementptr PROMISE, ptr %wp, i32 0, i32 2
  %werr = load i64, ptr %pv0_p, align 8
  call void @__kml_pipe_dest_error(ptr %ctx, i64 %werr)
  ret void
next:
  call void @__kml_pipe_step(ptr %ctx)
  ret void
}

define void @__kml_pipe_on_closed(ptr %ctx) {
entry:
  %f9 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 9
  %cp = load ptr, ptr %f9, align 8
  %pst_p = getelementptr PROMISE, ptr %cp, i32 0, i32 0
  %pst = load i64, ptr %pst_p, align 8
  %rejected = icmp eq i64 %pst, 2
  %f2 = getelementptr PIPECTX, ptr %ctx, i32 0, i32 2
  %prom = load ptr, ptr %f2, align 8
  br i1 %rejected, label %rej, label %ok
rej:
  %pv0_p = getelementptr PROMISE, ptr %cp, i32 0, i32 2
  %cerr = load i64, ptr %pv0_p, align 8
  call void @__kml_prom_reject(ptr %prom, i64 %cerr)
  ret void
ok:
  call void @__kml_promise_settle(ptr %prom, i64 1)
  ret void
}`))

	// tee(): two branches share one source read at a time.
	e.emitGlobal(streamIRExpand(`
define void @__kml_tee_pullhook(ptr %ctx) {
entry:
  %f4 = getelementptr TEECTX, ptr %ctx, i32 0, i32 4
  %reading = load i64, ptr %f4, align 8
  %busy = icmp ne i64 %reading, 0
  br i1 %busy, label %ret, label %go
go:
  store i64 1, ptr %f4, align 8
  %f0 = getelementptr TEECTX, ptr %ctx, i32 0, i32 0
  %src = load ptr, ptr %f0, align 8
  %p = call ptr @__kml_rs_read(ptr %src)
  %env = call ptr @malloc(i64 16)
  %e0 = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 0
  store ptr %p, ptr %e0, align 8
  %e1 = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 1
  store ptr %ctx, ptr %e1, align 8
  %clo = call ptr @__kml_mkclo(ptr @__kml_tee_on_read, ptr %env)
  call void @__kml_promise_add_reaction(ptr %p, ptr %clo)
  ret void
ret:
  ret void
}

define ptr @__kml_tee_pull(ptr %ctx) {
entry:
  call void @__kml_tee_pullhook(ptr %ctx)
  ret ptr null
}

define void @__kml_tee_on_read(ptr %env) {
entry:
  %e0 = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 0
  %p = load ptr, ptr %e0, align 8
  %e1 = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 1
  %ctx = load ptr, ptr %e1, align 8
  %f4 = getelementptr TEECTX, ptr %ctx, i32 0, i32 4
  store i64 0, ptr %f4, align 8
  %f1 = getelementptr TEECTX, ptr %ctx, i32 0, i32 1
  %b1 = load ptr, ptr %f1, align 8
  %f2 = getelementptr TEECTX, ptr %ctx, i32 0, i32 2
  %b2 = load ptr, ptr %f2, align 8
  %pst_p = getelementptr PROMISE, ptr %p, i32 0, i32 0
  %pst = load i64, ptr %pst_p, align 8
  %rejected = icmp eq i64 %pst, 2
  br i1 %rejected, label %err, label %ok
err:
  %pv0_p = getelementptr PROMISE, ptr %p, i32 0, i32 2
  %errv = load i64, ptr %pv0_p, align 8
  call void @__kml_rs_error(ptr %b1, i64 %errv)
  call void @__kml_rs_error(ptr %b2, i64 %errv)
  ret void
ok:
  %rv0_p = getelementptr PROMISE, ptr %p, i32 0, i32 2
  %recBits = load i64, ptr %rv0_p, align 8
  %rec = inttoptr i64 %recBits to ptr
  %f3 = getelementptr TEECTX, ptr %ctx, i32 0, i32 3
  %decode = load ptr, ptr %f3, align 8
  %dv = call { i64, i64, i64 } %decode(ptr %rec)
  %v0 = extractvalue { i64, i64, i64 } %dv, 0
  %v1 = extractvalue { i64, i64, i64 } %dv, 1
  %done = extractvalue { i64, i64, i64 } %dv, 2
  %isDone = icmp ne i64 %done, 0
  %f5 = getelementptr TEECTX, ptr %ctx, i32 0, i32 5
  %c1 = load i64, ptr %f5, align 8
  %alive1 = icmp eq i64 %c1, 0
  %f6 = getelementptr TEECTX, ptr %ctx, i32 0, i32 6
  %c2 = load i64, ptr %f6, align 8
  %alive2 = icmp eq i64 %c2, 0
  br i1 %isDone, label %closeBranches, label %forward
closeBranches:
  br i1 %alive1, label %close1, label %skip1
close1:
  %ign1 = call i64 @__kml_rs_close(ptr %b1)
  br label %skip1
skip1:
  br i1 %alive2, label %close2, label %skip2
close2:
  %ign2 = call i64 @__kml_rs_close(ptr %b2)
  br label %skip2
skip2:
  ret void
forward:
  br i1 %alive1, label %enq1, label %fskip1
enq1:
  %ign3 = call i64 @__kml_rs_enqueue(ptr %b1, i64 %v0, i64 %v1)
  br label %fskip1
fskip1:
  br i1 %alive2, label %enq2, label %fskip2
enq2:
  %ign4 = call i64 @__kml_rs_enqueue(ptr %b2, i64 %v0, i64 %v1)
  br label %fskip2
fskip2:
  ret void
}

define ptr @__kml_tee_cancel(ptr %env, i64 %reason) {
entry:
  %e0 = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 0
  %ctx = load ptr, ptr %e0, align 8
  %e1 = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 1
  %whichP = load ptr, ptr %e1, align 8
  %which = ptrtoint ptr %whichP to i64
  %isFirst = icmp eq i64 %which, 1
  br i1 %isFirst, label %mark1, label %mark2
mark1:
  %f5 = getelementptr TEECTX, ptr %ctx, i32 0, i32 5
  store i64 1, ptr %f5, align 8
  %f7 = getelementptr TEECTX, ptr %ctx, i32 0, i32 7
  store i64 %reason, ptr %f7, align 8
  br label %ck
mark2:
  %f6 = getelementptr TEECTX, ptr %ctx, i32 0, i32 6
  store i64 1, ptr %f6, align 8
  %f8 = getelementptr TEECTX, ptr %ctx, i32 0, i32 8
  store i64 %reason, ptr %f8, align 8
  br label %ck
ck:
  %f5b = getelementptr TEECTX, ptr %ctx, i32 0, i32 5
  %c1 = load i64, ptr %f5b, align 8
  %f6b = getelementptr TEECTX, ptr %ctx, i32 0, i32 6
  %c2 = load i64, ptr %f6b, align 8
  %both = and i64 %c1, %c2
  %doCancel = icmp ne i64 %both, 0
  br i1 %doCancel, label %cancelSrc, label %ret
cancelSrc:
  %f0 = getelementptr TEECTX, ptr %ctx, i32 0, i32 0
  %src = load ptr, ptr %f0, align 8
  %ign = call ptr @__kml_rs_cancel(ptr %src, i64 %reason)
  ret ptr null
ret:
  ret ptr null
}`))

	// TransformStream: the writable sink write runs the transform when the
	// readable side has capacity, else parks the chunk; the readable side's
	// pull resumes a parked chunk.
	e.emitGlobal(streamIRExpand(`
define ptr @__kml_ts_run_transform(ptr %ctx, i64 %v0, i64 %v1) {
entry:
  %f2 = getelementptr TSCTX, ptr %ctx, i32 0, i32 2
  %tc = load ptr, ptr %f2, align 8
  %identity = icmp eq ptr %tc, null
  br i1 %identity, label %ident, label %invoke
ident:
  %f0 = getelementptr TSCTX, ptr %ctx, i32 0, i32 0
  %rs = load ptr, ptr %f0, align 8
  %ign = call i64 @__kml_rs_enqueue(ptr %rs, i64 %v0, i64 %v1)
  ret ptr null
invoke:
  %tfp_p = getelementptr { ptr, ptr }, ptr %tc, i32 0, i32 0
  %tfp = load ptr, ptr %tfp_p, align 8
  %tep_p = getelementptr { ptr, ptr }, ptr %tc, i32 0, i32 1
  %tep = load ptr, ptr %tep_p, align 8
  %p = call ptr %tfp(ptr %tep, i64 %v0, i64 %v1)
  ret ptr %p
}

define ptr @__kml_ts_sink_write(ptr %ctx, i64 %v0, i64 %v1) {
entry:
  %f0 = getelementptr TSCTX, ptr %ctx, i32 0, i32 0
  %rs = load ptr, ptr %f0, align 8
  %d = call double @__kml_rs_desired(ptr %rs)
  %hasRoom = fcmp ogt double %d, 0.0
  br i1 %hasRoom, label %now, label %park
now:
  %p = call ptr @__kml_ts_run_transform(ptr %ctx, i64 %v0, i64 %v1)
  ret ptr %p
park:
  %f4 = getelementptr TSCTX, ptr %ctx, i32 0, i32 4
  store i64 1, ptr %f4, align 8
  %f5 = getelementptr TSCTX, ptr %ctx, i32 0, i32 5
  store i64 %v0, ptr %f5, align 8
  %f6 = getelementptr TSCTX, ptr %ctx, i32 0, i32 6
  store i64 %v1, ptr %f6, align 8
  %prom = call ptr @__kml_task_alloc_promise()
  %f7 = getelementptr TSCTX, ptr %ctx, i32 0, i32 7
  store ptr %prom, ptr %f7, align 8
  ; A read may already be parked on the readable (its pull ran before this
  ; chunk arrived) — re-kick pull so the parked chunk is picked up now.
  call void @__kml_rs_pull_if_needed(ptr %rs)
  ret ptr %prom
}

define ptr @__kml_ts_pull(ptr %ctx) {
entry:
  %f4 = getelementptr TSCTX, ptr %ctx, i32 0, i32 4
  %parked = load i64, ptr %f4, align 8
  %have = icmp ne i64 %parked, 0
  br i1 %have, label %resume, label %ret
resume:
  store i64 0, ptr %f4, align 8
  %f5 = getelementptr TSCTX, ptr %ctx, i32 0, i32 5
  %v0 = load i64, ptr %f5, align 8
  %f6 = getelementptr TSCTX, ptr %ctx, i32 0, i32 6
  %v1 = load i64, ptr %f6, align 8
  %f7 = getelementptr TSCTX, ptr %ctx, i32 0, i32 7
  %wprom = load ptr, ptr %f7, align 8
  %p = call ptr @__kml_ts_run_transform(ptr %ctx, i64 %v0, i64 %v1)
  %sync = icmp eq ptr %p, null
  br i1 %sync, label %settleNow, label %chain
settleNow:
  call void @__kml_promise_settle(ptr %wprom, i64 1)
  ret ptr null
chain:
  %env = call ptr @malloc(i64 16)
  %e0 = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 0
  store ptr %p, ptr %e0, align 8
  %e1 = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 1
  store ptr %wprom, ptr %e1, align 8
  %clo = call ptr @__kml_mkclo(ptr @__kml_ts_mirror_settle, ptr %env)
  call void @__kml_promise_add_reaction(ptr %p, ptr %clo)
  ret ptr null
ret:
  ret ptr null
}

define void @__kml_ts_mirror_settle(ptr %env) {
entry:
  %e0 = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 0
  %p = load ptr, ptr %e0, align 8
  %e1 = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 1
  %tgt = load ptr, ptr %e1, align 8
  %pst_p = getelementptr PROMISE, ptr %p, i32 0, i32 0
  %pst = load i64, ptr %pst_p, align 8
  %rejected = icmp eq i64 %pst, 2
  br i1 %rejected, label %rej, label %ok
rej:
  %pv0_p = getelementptr PROMISE, ptr %p, i32 0, i32 2
  %err = load i64, ptr %pv0_p, align 8
  call void @__kml_prom_reject(ptr %tgt, i64 %err)
  ret void
ok:
  call void @__kml_promise_settle(ptr %tgt, i64 1)
  ret void
}

define ptr @__kml_ts_sink_close(ptr %ctx) {
entry:
  %f3 = getelementptr TSCTX, ptr %ctx, i32 0, i32 3
  %fc = load ptr, ptr %f3, align 8
  %noflush = icmp eq ptr %fc, null
  br i1 %noflush, label %closeNow, label %flush
closeNow:
  %f0 = getelementptr TSCTX, ptr %ctx, i32 0, i32 0
  %rs = load ptr, ptr %f0, align 8
  %ign = call i64 @__kml_rs_close(ptr %rs)
  ret ptr null
flush:
  %ffp_p = getelementptr { ptr, ptr }, ptr %fc, i32 0, i32 0
  %ffp = load ptr, ptr %ffp_p, align 8
  %fep_p = getelementptr { ptr, ptr }, ptr %fc, i32 0, i32 1
  %fep = load ptr, ptr %fep_p, align 8
  %p = call ptr %ffp(ptr %fep)
  %sync = icmp eq ptr %p, null
  br i1 %sync, label %closeNow, label %async
async:
  %cprom = call ptr @__kml_task_alloc_promise()
  %f8 = getelementptr TSCTX, ptr %ctx, i32 0, i32 8
  store ptr %cprom, ptr %f8, align 8
  %env = call ptr @malloc(i64 16)
  %e0 = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 0
  store ptr %p, ptr %e0, align 8
  %e1 = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 1
  store ptr %ctx, ptr %e1, align 8
  %clo = call ptr @__kml_mkclo(ptr @__kml_ts_flush_done, ptr %env)
  call void @__kml_promise_add_reaction(ptr %p, ptr %clo)
  ret ptr %cprom
}

define void @__kml_ts_flush_done(ptr %env) {
entry:
  %e0 = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 0
  %p = load ptr, ptr %e0, align 8
  %e1 = getelementptr { ptr, ptr }, ptr %env, i32 0, i32 1
  %ctx = load ptr, ptr %e1, align 8
  %f0 = getelementptr TSCTX, ptr %ctx, i32 0, i32 0
  %rs = load ptr, ptr %f0, align 8
  %f8 = getelementptr TSCTX, ptr %ctx, i32 0, i32 8
  %cprom = load ptr, ptr %f8, align 8
  %pst_p = getelementptr PROMISE, ptr %p, i32 0, i32 0
  %pst = load i64, ptr %pst_p, align 8
  %rejected = icmp eq i64 %pst, 2
  br i1 %rejected, label %rej, label %ok
rej:
  %pv0_p = getelementptr PROMISE, ptr %p, i32 0, i32 2
  %err = load i64, ptr %pv0_p, align 8
  call void @__kml_rs_error(ptr %rs, i64 %err)
  call void @__kml_prom_reject(ptr %cprom, i64 %err)
  ret void
ok:
  %ign = call i64 @__kml_rs_close(ptr %rs)
  call void @__kml_promise_settle(ptr %cprom, i64 1)
  ret void
}

define ptr @__kml_ts_sink_abort(ptr %ctx, i64 %reason) {
entry:
  %f0 = getelementptr TSCTX, ptr %ctx, i32 0, i32 0
  %rs = load ptr, ptr %f0, align 8
  call void @__kml_rs_error(ptr %rs, i64 %reason)
  ret ptr null
}`))
}
