// runtime_loop_turn.go — waiting from the main stack runs the real event loop
// (TDD-00223 §1).
//
// Code that must wait and is not a coroutine — module top-level `await` — used
// to spin a private drive that serviced a subset of sources (microtasks +
// timers, or libcurl alone) and starved the rest: sockets, servers, child
// stdio, workers, fs. It now takes turns of the one real loop:
//
//	__kml_loop_turn(i1 pin) -> i32
//	    Run @__kml_event_loop_run for exactly one iteration — every source, one
//	    select() with its proper timeout — and return:
//	      0  a turn was taken; re-check what you are waiting for
//	      1  the loop is idle: nothing is left alive that could ever wake it
//	      2  cannot run from here: the caller is itself inside a loop callback
//	         (re-entering the loop under its own iteration is not sound)
//	    pin=true holds the loop alive for the turn — a wait on an in-flight
//	    fetch, whose libcurl socket is what Node's loop would be holding open.
//
//	__kml_top_await(ptr word)
//	    Wait until *word (a promise's state) turns non-zero. If the loop goes
//	    idle first the await can never settle, and the process ends the way Node
//	    ends it: a warning on stderr and exit code 13.
//
// Only module top-level code reaches these with result 0/1: a callback that
// awaits is compiled as a coroutine and parks instead (§2). Result 2 keeps the
// old subset drive for the remaining main-stack waits issued from inside a loop
// callback, so they are no worse than before while they still exist.
package llvm

import "strconv"

// noteLoopTurn records that emitted code references the loop-turn runtime, so
// the end of main() emits the full event loop (and with it these definitions).
func (e *Emitter) noteLoopTurn() {
	e.usedLoopTurn = true
	e.usedAwaitTimerDrive = true // @__kml_timer_fire_next / @__kml_fetch_pump resolve
}

// ensureLoopTurn emits the loop-turn globals and functions. Called by
// ensureHTTPRuntime just before it defines @__kml_event_loop_run, which reads
// the globals.
func (e *Emitter) ensureLoopTurn() {
	if e.usedLoopTurnDefs {
		return
	}
	e.usedLoopTurnDefs = true
	e.usedAwaitTimerDrive = true
	e.ensureExit()
	e.ensureWriteDecl()
	e.ensureUsleepDecl()
	e.ensureCurrentTaskGlobal()
	// A goroutine never takes a loop turn (it has no reactor of its own). Without
	// klain:sync linked there are no goroutines to ask about.
	if e.usesSyncProgram {
		e.emitGlobal("declare i32 @klainsync_on_goroutine()")
		e.emitGlobal("define internal i1 @__kml_loop_on_goroutine() {\n  %g = call i32 @klainsync_on_goroutine()\n  %r = icmp ne i32 %g, 0\n  ret i1 %r\n}")
	} else {
		e.emitGlobal("define internal i1 @__kml_loop_on_goroutine() {\n  ret i1 false\n}")
	}
	e.emitGlobal("@__kml_loop_oneshot = internal thread_local global i8 0, align 1")
	e.emitGlobal("@__kml_loop_pin = internal thread_local global i64 0, align 8")
	e.emitGlobal("@__kml_loop_depth = internal thread_local global i64 0, align 8")
	e.emitGlobal("@__kml_loop_idle = internal thread_local global i1 false, align 1")
	msg := "Warning: Detected unsettled top-level await\n"
	e.emitGlobal(`@.kml_tla_unsettled = private unnamed_addr constant [` + strconv.Itoa(len(msg)) + ` x i8] c"Warning: Detected unsettled top-level await\0A"`)
	e.emitGlobal(`
define i32 @__kml_loop_turn(i1 %pin) {
entry:
  ; Only the main stack at module top level may run the loop: not a loop
  ; callback (depth > 0), not a coroutine or a connection fiber — those park
  ; instead, and running the loop on their stack would have it switch contexts
  ; out from under itself — and not a goroutine, which has no reactor.
  %depth = load i64, ptr @__kml_loop_depth, align 8
  %inside = icmp sgt i64 %depth, 0
  %curtask = load ptr, ptr @__kml_current_task, align 8
  %ontask = icmp ne ptr %curtask, null
  %connidx = load i64, ptr @__kml_current_conn_idx, align 8
  %onfiber = icmp sge i64 %connidx, 0
  %ongo = call i1 @__kml_loop_on_goroutine()
  %no0 = or i1 %inside, %ontask
  %no1 = or i1 %no0, %onfiber
  %no = or i1 %no1, %ongo
  br i1 %no, label %cannot, label %run
cannot:
  ret i32 2
run:
  %pins = load i64, ptr @__kml_loop_pin, align 8
  %pinadd = zext i1 %pin to i64
  %pins1 = add i64 %pins, %pinadd
  store i64 %pins1, ptr @__kml_loop_pin, align 8
  store i1 false, ptr @__kml_loop_idle, align 1
  store i8 1, ptr @__kml_loop_oneshot, align 1
  call void @__kml_event_loop_run()
  store i8 0, ptr @__kml_loop_oneshot, align 1
  store i64 %pins, ptr @__kml_loop_pin, align 8
  %idle = load i1, ptr @__kml_loop_idle, align 1
  %r = zext i1 %idle to i32
  ret i32 %r
}

define void @__kml_top_await(ptr %word) {
entry:
  br label %check
check:
  %v = load i64, ptr %word, align 8
  %settled = icmp ne i64 %v, 0
  br i1 %settled, label %ret, label %turn
turn:
  %r = call i32 @__kml_loop_turn(i1 false)
  switch i32 %r, label %check [
    i32 1, label %idle
    i32 2, label %inner
  ]
idle:
  ; the loop's own final microtask/scheduler pass may have settled it
  %v2 = load i64, ptr %word, align 8
  %settled2 = icmp ne i64 %v2, 0
  br i1 %settled2, label %ret, label %unsettled
unsettled:
  %w = call i64 @write(i32 2, ptr @.kml_tla_unsettled, i64 ` + strconv.Itoa(len(msg)) + `)
  call void @exit(i32 13)
  unreachable
inner:
  ; inside a loop callback: the subset drive, unchanged — microtasks, the
  ; scheduler, due timers, in-flight fetches — falling through when none of
  ; them can make progress any more
  call void @__kml_drain_microtasks()
  call void @__kml_task_sched_step()
  %v3 = load i64, ptr %word, align 8
  %settled3 = icmp ne i64 %v3, 0
  br i1 %settled3, label %ret, label %innerdrive
innerdrive:
  %fired = call i1 @__kml_timer_fire_next()
  %pumped = call i1 @__kml_fetch_pump()
  %tact = load i64, ptr @__kml_task_active, align 8
  %hastasks = icmp sgt i64 %tact, 0
  %prog0 = or i1 %fired, %pumped
  %prog = or i1 %prog0, %hastasks
  br i1 %prog, label %innernap, label %ret
innernap:
  %ig = call i32 @usleep(i32 200)
  br label %check
ret:
  ret void
}`)
}

// ensureUsleepDecl declares usleep once — the task runtime and the loop-turn
// runtime both nap between drive cycles.
func (e *Emitter) ensureUsleepDecl() {
	if e.usedUsleepDecl {
		return
	}
	e.usedUsleepDecl = true
	e.emitGlobal("declare i32 @usleep(i32 noundef)")
}
