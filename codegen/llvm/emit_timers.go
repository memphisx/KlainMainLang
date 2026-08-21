// emit_timers.go — setTimeout/clearTimeout/setInterval/clearInterval.
// Bare global functions (like fetch/btoa/parseInt), not a namespace.
//
// Needs no general-purpose (I/O-multiplexing) event loop — just a
// sleep-until-next-due queue, drained once by EmitProgram after the
// program's own top-level code finishes (see ensureTimerRuntime below
// for the full design). An active setInterval with
// nothing ever calling clearInterval on it means that drain loop never
// finishes, matching real Node's behavior: the process only exits once
// every timer has fired-and-not-repeated or been cleared.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// timerCallbackPtr evaluates and validates arg as a Memory.free-free zero-
// argument, void-returning closure — the only callback shape this V1
// supports, matching the fixed `call void (ptr) %fp(ptr %ep)` trampoline
// shape __kml_timer_drain uses to call it back later.
func (e *Emitter) timerCallbackPtr(arg ast.Expression, fnName string, pos ast.Pos) (string, error) {
	val, err := e.emitExpr(arg)
	if err != nil {
		return "", err
	}
	if !val.Ty.IsFunc {
		return "", fmt.Errorf("%d:%d: %s's first argument must be a function", pos.Line, pos.Col, fnName)
	}
	if len(val.Ty.FuncParams) != 0 || (val.Ty.FuncRetType != nil && val.Ty.FuncRetType.IR != "void") {
		return "", fmt.Errorf("%d:%d: %s's callback must take no arguments and return nothing (() => void)", pos.Line, pos.Col, fnName)
	}
	return val.Ref, nil
}

// timerDelayArg resolves the optional delayMs argument (0 if omitted).
func (e *Emitter) timerDelayArg(args []ast.Expression, idx int) (string, error) {
	if idx >= len(args) {
		return "0", nil
	}
	val, err := e.emitExpr(args[idx])
	if err != nil {
		return "", err
	}
	val = e.coerce(val, TypeI64)
	return val.Ref, nil
}

func (e *Emitter) emitSetTimeout(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: setTimeout takes 1 or 2 arguments (callback, delayMs?)", pos.Line, pos.Col)
	}
	closurePtr, err := e.timerCallbackPtr(args[0], "setTimeout", pos)
	if err != nil {
		return Value{}, err
	}
	delayRef, err := e.timerDelayArg(args, 1)
	if err != nil {
		return Value{}, err
	}
	e.ensureTimerRuntime()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_timer_schedule(ptr %s, i64 %s, i64 0)", r, closurePtr, delayRef))
	return Value{Ref: r, Ty: TypeI64}, nil
}

func (e *Emitter) emitSetInterval(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: setInterval takes 1 or 2 arguments (callback, delayMs?)", pos.Line, pos.Col)
	}
	closurePtr, err := e.timerCallbackPtr(args[0], "setInterval", pos)
	if err != nil {
		return Value{}, err
	}
	delayRef, err := e.timerDelayArg(args, 1)
	if err != nil {
		return Value{}, err
	}
	e.ensureTimerRuntime()
	r := e.freshReg()
	// intervalMs == delayMs: the same cadence used for the first fire is
	// reused for every subsequent one, matching real JS's setInterval.
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_timer_schedule(ptr %s, i64 %s, i64 %s)", r, closurePtr, delayRef, delayRef))
	return Value{Ref: r, Ty: TypeI64}, nil
}

// emitSetImmediate implements setImmediate(callback): schedules callback to
// fire via the same timer queue setTimeout/setInterval already use, with
// delayMs hardcoded to 0 (no delay argument — real Node's setImmediate
// doesn't take one either). Known scope narrowing, not a bug: real Node
// guarantees a setImmediate callback fires before a same-tick
// setTimeout(fn, 0) when both are scheduled from inside an I/O callback,
// because its event loop has distinct phases (check vs. timers) — this
// compiler's __kml_timer_drain is a single flat fire-time-ordered queue
// with no phase concept, so setImmediate(fn) and setTimeout(fn, 0) are
// genuinely indistinguishable here (both fire at "now"). Documented in
// docs/status/TIMERS.md rather than silently assumed equivalent.
func (e *Emitter) emitSetImmediate(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: setImmediate takes exactly 1 argument (callback)", pos.Line, pos.Col)
	}
	closurePtr, err := e.timerCallbackPtr(args[0], "setImmediate", pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureTimerRuntime()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_timer_schedule(ptr %s, i64 0, i64 0)", r, closurePtr))
	return Value{Ref: r, Ty: TypeI64}, nil
}

func (e *Emitter) emitClearTimer(args []ast.Expression, fnName string, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: %s takes exactly 1 argument (id)", pos.Line, pos.Col, fnName)
	}
	idVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	idVal = e.coerce(idVal, TypeI64)
	e.ensureTimerRuntime()
	e.emitInstr(fmt.Sprintf("call void @__kml_timer_clear(i64 %s)", idVal.Ref))
	return Value{Ty: TypeVoid}, nil
}

// ensureTimerRuntime declares everything setTimeout/clearTimeout/
// setInterval/clearInterval need: the global timer queue (three globals —
// data pointer, length, capacity — the same "separate globals" shape
// process.argv already uses for its own ptr+len pair, rather than one
// malloc'd header struct, since there's only ever one timer queue per
// program), and four functions:
//
//	__kml_timer_schedule(ptr closure, i64 delayMs, i64 intervalMs) -> i64
//	  Appends a new entry (growing the queue via the same realloc-doubling
//	  shape __kml_fetch/__kml_exec_file_sync/__kml_fs_readdir all already
//	  use, just holding fixed-size 32-byte structs this time instead of
//	  bytes or ptrs) and returns its id. intervalMs is 0 for a one-shot
//	  setTimeout, or the repeat cadence for setInterval.
//	__kml_timer_clear(i64 id)
//	  Linear scan by id; sets that entry's intervalMs to -1 (the sentinel
//	  for "cancelled / already fired and done, never consider again" —
//	  chosen over physically removing the entry so the queue never needs
//	  compaction, and over a separate cancelled flag so every field stays
//	  a plain i64/ptr with no padding ambiguity to reason about).
//	__kml_timer_drain()
//	  Runs after the program's own top-level code finishes (see
//	  EmitProgram). Repeatedly: linear-scan for the pending (intervalMs !=
//	  -1) entry with the smallest fire time; if none, return (queue
//	  exhausted, main() can finally end); otherwise sleep via nanosleep()
//	  until it's due, call its closure, then — since the callback may
//	  itself have scheduled/cleared timers and grown/reallocated the queue
//	  — reload the queue pointer and this entry fresh before deciding
//	  whether to reschedule (intervalMs > 0, matching JS's own repeat
//	  behavior) or mark it done (intervalMs = -1).
//
// Entry layout ({ i64 id, i64 fireAtNs, i64 intervalMs, ptr closureHdr },
// 32 bytes, no padding): every field is i64 or ptr, both naturally 8-byte
// aligned, so the struct's total size and field order are unambiguous
// without needing LLVM's sizeof-via-GEP idiom.
// emitTimerFireNext emits @__kml_timer_fire_next() -> i1: fire the single
// earliest-due pending timer (sleeping until it is due), returning 1, or 0 when
// the timer queue is empty/all-done. Unlike __kml_timer_drain (a run-to-empty
// loop used at program exit), this is one step, so an `await` on a promise a
// timer will settle can drive timers incrementally and re-check the promise after
// each fire (TDD-00087). Signal handling is left to __kml_timer_drain's own loop.
func (e *Emitter) emitTimerFireNext() {
	e.emitGlobal(`
define i1 @__kml_timer_fire_next() {
entry:
  %besti = alloca i64, align 8
  %bestfire = alloca i64, align 8
  %scani = alloca i64, align 8
  %ts = alloca { i64, i64 }, align 8
  %len = load i64, ptr @__kml_timer_len, align 8
  %data = load ptr, ptr @__kml_timer_data, align 8
  store i64 -1, ptr %besti, align 8
  store i64 0, ptr %bestfire, align 8
  store i64 0, ptr %scani, align 8
  br label %scanloop
scanloop:
  %si = load i64, ptr %scani, align 8
  %ib = icmp slt i64 %si, %len
  br i1 %ib, label %scanbody, label %scandone
scanbody:
  %slot = getelementptr { i64, i64, i64, ptr }, ptr %data, i64 %si
  %int_p = getelementptr { i64, i64, i64, ptr }, ptr %slot, i32 0, i32 2
  %int = load i64, ptr %int_p, align 8
  %isdone = icmp eq i64 %int, -1
  br i1 %isdone, label %scannext, label %consider
consider:
  %fire_p = getelementptr { i64, i64, i64, ptr }, ptr %slot, i32 0, i32 1
  %fire = load i64, ptr %fire_p, align 8
  %bi = load i64, ptr %besti, align 8
  %noneyet = icmp eq i64 %bi, -1
  br i1 %noneyet, label %takebest, label %compare
compare:
  %bf = load i64, ptr %bestfire, align 8
  %better = icmp slt i64 %fire, %bf
  br i1 %better, label %takebest, label %scannext
takebest:
  store i64 %si, ptr %besti, align 8
  store i64 %fire, ptr %bestfire, align 8
  br label %scannext
scannext:
  %sn = add i64 %si, 1
  store i64 %sn, ptr %scani, align 8
  br label %scanloop
scandone:
  %fb = load i64, ptr %besti, align 8
  %none = icmp eq i64 %fb, -1
  br i1 %none, label %retfalse, label %havebest
havebest:
  %tf = load i64, ptr %bestfire, align 8
  %now = call i64 @__kml_monotonic_ns()
  %need = icmp sgt i64 %tf, %now
  br i1 %need, label %dosleep, label %dofire
dosleep:
  %wait = sub i64 %tf, %now
  %sec = sdiv i64 %wait, 1000000000
  %nsr = srem i64 %wait, 1000000000
  %ts_s = getelementptr { i64, i64 }, ptr %ts, i32 0, i32 0
  %ts_n = getelementptr { i64, i64 }, ptr %ts, i32 0, i32 1
  store i64 %sec, ptr %ts_s, align 8
  store i64 %nsr, ptr %ts_n, align 8
  %src = call i32 @nanosleep(ptr %ts, ptr null)
  br label %dofire
dofire:
  %data2 = load ptr, ptr @__kml_timer_data, align 8
  %fi = load i64, ptr %besti, align 8
  %fslot = getelementptr { i64, i64, i64, ptr }, ptr %data2, i64 %fi
  %fc_p = getelementptr { i64, i64, i64, ptr }, ptr %fslot, i32 0, i32 3
  %fc = load ptr, ptr %fc_p, align 8
  %fp_p = getelementptr { ptr, ptr }, ptr %fc, i32 0, i32 0
  %fp = load ptr, ptr %fp_p, align 8
  %ep_p = getelementptr { ptr, ptr }, ptr %fc, i32 0, i32 1
  %ep = load ptr, ptr %ep_p, align 8
  call void (ptr) %fp(ptr %ep)
  %fint_p = getelementptr { i64, i64, i64, ptr }, ptr %fslot, i32 0, i32 2
  %fint = load i64, ptr %fint_p, align 8
  %rep = icmp sgt i64 %fint, 0
  br i1 %rep, label %resched, label %markdone
resched:
  %now2 = call i64 @__kml_monotonic_ns()
  %intns = mul i64 %fint, 1000000
  %nf = add i64 %now2, %intns
  %ff_p = getelementptr { i64, i64, i64, ptr }, ptr %fslot, i32 0, i32 1
  store i64 %nf, ptr %ff_p, align 8
  br label %rettrue
markdone:
  store i64 -1, ptr %fint_p, align 8
  br label %rettrue
rettrue:
  ret i1 1
retfalse:
  ret i1 0
}`)
}

func (e *Emitter) ensureTimerRuntime() {
	if e.usedTimers {
		return
	}
	e.usedTimers = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureClockGettime()
	// __kml_timer_drain below unconditionally checks the pending-signal
	// flags (TDD-00019) at the top of its loop, whether or not this
	// program ever calls process.on — same "every symbol the loop's IR
	// mentions must be declared, regardless of whether the specific
	// feature is used" reasoning ensureHTTPRuntime already documents for
	// ensureFetchAsync/ensurePromiseCombinators.
	e.ensureSignalHandlerRuntime()
	clockID := monotonicClockID()
	e.emitGlobal("declare i32 @nanosleep(ptr noundef, ptr noundef)")
	e.emitGlobal("@__kml_timer_data = internal global ptr null, align 8")
	e.emitGlobal("@__kml_timer_len = internal global i64 0, align 8")
	e.emitGlobal("@__kml_timer_cap = internal global i64 0, align 8")
	e.emitGlobal("@__kml_timer_next_id = internal global i64 1, align 8")

	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_monotonic_ns() {
entry:
  %%ts = alloca { i64, i64 }, align 8
  %%r = call i32 @clock_gettime(i32 %s, ptr %%ts)
  %%sec_p = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 0
  %%nsec_p = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 1
  %%sec = load i64, ptr %%sec_p, align 8
  %%nsec = load i64, ptr %%nsec_p, align 8
  %%sec_ns = mul i64 %%sec, 1000000000
  %%total = add i64 %%sec_ns, %%nsec
  ret i64 %%total
}`, clockID))

	e.emitGlobal(`
define i64 @__kml_timer_schedule(ptr %closure, i64 %delayms, i64 %intervalms) {
entry:
  %len = load i64, ptr @__kml_timer_len, align 8
  %cap = load i64, ptr @__kml_timer_cap, align 8
  %data = load ptr, ptr @__kml_timer_data, align 8
  %neededp1 = add i64 %len, 1
  %needgrow = icmp sgt i64 %neededp1, %cap
  br i1 %needgrow, label %grow, label %doappend

grow:
  %cap2 = mul i64 %cap, 2
  %atleast8 = icmp sgt i64 %cap2, 8
  %newcap = select i1 %atleast8, i64 %cap2, i64 8
  %newcapbytes = mul i64 %newcap, 32
  %newdata = call ptr @realloc(ptr %data, i64 %newcapbytes)
  store ptr %newdata, ptr @__kml_timer_data, align 8
  store i64 %newcap, ptr @__kml_timer_cap, align 8
  br label %doappend

doappend:
  %dataNow = load ptr, ptr @__kml_timer_data, align 8
  %slot = getelementptr { i64, i64, i64, ptr }, ptr %dataNow, i64 %len

  %id = load i64, ptr @__kml_timer_next_id, align 8
  %nextid = add i64 %id, 1
  store i64 %nextid, ptr @__kml_timer_next_id, align 8
  %id_p = getelementptr { i64, i64, i64, ptr }, ptr %slot, i32 0, i32 0
  store i64 %id, ptr %id_p, align 8

  %now = call i64 @__kml_monotonic_ns()
  %delayns = mul i64 %delayms, 1000000
  %fireat = add i64 %now, %delayns
  %fireat_p = getelementptr { i64, i64, i64, ptr }, ptr %slot, i32 0, i32 1
  store i64 %fireat, ptr %fireat_p, align 8

  %interval_p = getelementptr { i64, i64, i64, ptr }, ptr %slot, i32 0, i32 2
  store i64 %intervalms, ptr %interval_p, align 8

  %closure_p = getelementptr { i64, i64, i64, ptr }, ptr %slot, i32 0, i32 3
  store ptr %closure, ptr %closure_p, align 8

  %newlen = add i64 %len, 1
  store i64 %newlen, ptr @__kml_timer_len, align 8

  ret i64 %id
}`)

	e.emitGlobal(`
define void @__kml_timer_clear(i64 %id) {
entry:
  %len = load i64, ptr @__kml_timer_len, align 8
  %data = load ptr, ptr @__kml_timer_data, align 8
  %ip = alloca i64, align 8
  store i64 0, ptr %ip, align 8
  br label %loop

loop:
  %i = load i64, ptr %ip, align 8
  %inbounds = icmp slt i64 %i, %len
  br i1 %inbounds, label %body, label %done

body:
  %slot = getelementptr { i64, i64, i64, ptr }, ptr %data, i64 %i
  %id_p = getelementptr { i64, i64, i64, ptr }, ptr %slot, i32 0, i32 0
  %eid = load i64, ptr %id_p, align 8
  %match = icmp eq i64 %eid, %id
  br i1 %match, label %cancelit, label %next

cancelit:
  %interval_p = getelementptr { i64, i64, i64, ptr }, ptr %slot, i32 0, i32 2
  store i64 -1, ptr %interval_p, align 8
  br label %done

next:
  %inext = add i64 %i, 1
  store i64 %inext, ptr %ip, align 8
  br label %loop

done:
  ret void
}`)

	e.emitGlobal(`
define void @__kml_timer_drain() {
entry:
  %besti = alloca i64, align 8
  %bestfire = alloca i64, align 8
  %scani = alloca i64, align 8
  %ts = alloca { i64, i64 }, align 8
  br label %outerloop

outerloop:
  ; TDD-00019: identical signal-check block to __kml_event_loop_run's own
  ; (runtime_http.go) — a signal interrupting nanosleep() below just
  ; returns early with no fd_set-style staleness concern, so no return-
  ; value check is needed here the way select() needed one.
  %sigintp = load volatile i8, ptr @__kml_sigint_pending, align 1
  %sigintset = icmp ne i8 %sigintp, 0
  br i1 %sigintset, label %sigintfire, label %checksigterm

sigintfire:
  store volatile i8 0, ptr @__kml_sigint_pending, align 1
  %sigintclos = load ptr, ptr @__kml_sigint_closure, align 8
  %hassigint = icmp ne ptr %sigintclos, null
  br i1 %hassigint, label %sigintcall, label %checksigterm

sigintcall:
  %sigintfp_p = getelementptr { ptr, ptr }, ptr %sigintclos, i32 0, i32 0
  %sigintep_p = getelementptr { ptr, ptr }, ptr %sigintclos, i32 0, i32 1
  %sigintfp = load ptr, ptr %sigintfp_p, align 8
  %sigintep = load ptr, ptr %sigintep_p, align 8
  call void %sigintfp(ptr %sigintep)
  br label %checksigterm

checksigterm:
  %sigtermp = load volatile i8, ptr @__kml_sigterm_pending, align 1
  %sigtermset = icmp ne i8 %sigtermp, 0
  br i1 %sigtermset, label %sigtermfire, label %timerscan

sigtermfire:
  store volatile i8 0, ptr @__kml_sigterm_pending, align 1
  %sigtermclos = load ptr, ptr @__kml_sigterm_closure, align 8
  %hassigterm = icmp ne ptr %sigtermclos, null
  br i1 %hassigterm, label %sigtermcall, label %timerscan

sigtermcall:
  %sigtermfp_p = getelementptr { ptr, ptr }, ptr %sigtermclos, i32 0, i32 0
  %sigtermep_p = getelementptr { ptr, ptr }, ptr %sigtermclos, i32 0, i32 1
  %sigtermfp = load ptr, ptr %sigtermfp_p, align 8
  %sigtermep = load ptr, ptr %sigtermep_p, align 8
  call void %sigtermfp(ptr %sigtermep)
  br label %timerscan

timerscan:
  %len = load i64, ptr @__kml_timer_len, align 8
  %data = load ptr, ptr @__kml_timer_data, align 8
  store i64 -1, ptr %besti, align 8
  store i64 0, ptr %bestfire, align 8
  store i64 0, ptr %scani, align 8
  br label %scanloop

scanloop:
  %si = load i64, ptr %scani, align 8
  %sinbounds = icmp slt i64 %si, %len
  br i1 %sinbounds, label %scanbody, label %scandone

scanbody:
  %sslot = getelementptr { i64, i64, i64, ptr }, ptr %data, i64 %si
  %sinterval_p = getelementptr { i64, i64, i64, ptr }, ptr %sslot, i32 0, i32 2
  %sinterval = load i64, ptr %sinterval_p, align 8
  %sdone = icmp eq i64 %sinterval, -1
  br i1 %sdone, label %scannext, label %scanconsider

scanconsider:
  %sfire_p = getelementptr { i64, i64, i64, ptr }, ptr %sslot, i32 0, i32 1
  %sfire = load i64, ptr %sfire_p, align 8
  %curbesti = load i64, ptr %besti, align 8
  %noneyet = icmp eq i64 %curbesti, -1
  br i1 %noneyet, label %scantakebest, label %scancompare

scancompare:
  %curbestfire = load i64, ptr %bestfire, align 8
  %better = icmp slt i64 %sfire, %curbestfire
  br i1 %better, label %scantakebest, label %scannext

scantakebest:
  store i64 %si, ptr %besti, align 8
  store i64 %sfire, ptr %bestfire, align 8
  br label %scannext

scannext:
  %sinext = add i64 %si, 1
  store i64 %sinext, ptr %scani, align 8
  br label %scanloop

scandone:
  %foundbest = load i64, ptr %besti, align 8
  %nomore = icmp eq i64 %foundbest, -1
  br i1 %nomore, label %alldone, label %havebest

havebest:
  %targetfire = load i64, ptr %bestfire, align 8
  %now1 = call i64 @__kml_monotonic_ns()
  %needwait = icmp sgt i64 %targetfire, %now1
  br i1 %needwait, label %dosleep, label %dofire

dosleep:
  %waitns = sub i64 %targetfire, %now1
  %waitsec = sdiv i64 %waitns, 1000000000
  %waitnsrem = srem i64 %waitns, 1000000000
  %ts_sec = getelementptr { i64, i64 }, ptr %ts, i32 0, i32 0
  %ts_nsec = getelementptr { i64, i64 }, ptr %ts, i32 0, i32 1
  store i64 %waitsec, ptr %ts_sec, align 8
  store i64 %waitnsrem, ptr %ts_nsec, align 8
  %sleeprc = call i32 @nanosleep(ptr %ts, ptr null)
  ; TDD-00019: a signal (e.g. a process.on-registered SIGINT/SIGTERM)
  ; interrupts nanosleep() early, well before the timer is actually due —
  ; firing it now would be genuinely premature, not just early-by-a-bit.
  ; Loop back to outerloop instead, which checks the pending-signal flags
  ; first and, if this wasn't actually a signal we care about, simply
  ; recomputes the (now-shorter) remaining wait and sleeps again.
  %sleepinterrupted = icmp ne i32 %sleeprc, 0
  br i1 %sleepinterrupted, label %outerloop, label %dofire

dofire:
  %data2 = load ptr, ptr @__kml_timer_data, align 8
  %fireidx = load i64, ptr %besti, align 8
  %fslot = getelementptr { i64, i64, i64, ptr }, ptr %data2, i64 %fireidx
  %fclosure_p = getelementptr { i64, i64, i64, ptr }, ptr %fslot, i32 0, i32 3
  %fclosure = load ptr, ptr %fclosure_p, align 8
  %fp_p = getelementptr { ptr, ptr }, ptr %fclosure, i32 0, i32 0
  %fp = load ptr, ptr %fp_p, align 8
  %ep_p = getelementptr { ptr, ptr }, ptr %fclosure, i32 0, i32 1
  %ep = load ptr, ptr %ep_p, align 8
  call void (ptr) %fp(ptr %ep)

  %data3 = load ptr, ptr @__kml_timer_data, align 8
  %fslot2 = getelementptr { i64, i64, i64, ptr }, ptr %data3, i64 %fireidx
  %finterval_p = getelementptr { i64, i64, i64, ptr }, ptr %fslot2, i32 0, i32 2
  %finterval = load i64, ptr %finterval_p, align 8
  %stillrepeating = icmp sgt i64 %finterval, 0
  br i1 %stillrepeating, label %reschedule, label %maybemarkdone

reschedule:
  %now2 = call i64 @__kml_monotonic_ns()
  %intervalns = mul i64 %finterval, 1000000
  %newfire = add i64 %now2, %intervalns
  %ffire_p = getelementptr { i64, i64, i64, ptr }, ptr %fslot2, i32 0, i32 1
  store i64 %newfire, ptr %ffire_p, align 8
  br label %outerloop

maybemarkdone:
  %alreadycancelled = icmp eq i64 %finterval, -1
  br i1 %alreadycancelled, label %outerloop, label %markdone

markdone:
  store i64 -1, ptr %finterval_p, align 8
  br label %outerloop

alldone:
  ret void
}`)
}
