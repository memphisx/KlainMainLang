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
//
//	Both live in promiseadoptsrc/ (adopt.c, fetchslot.c; TDD-00240).
package llvm

import _ "embed"

//go:embed promiseadoptsrc/adopt.c
var promiseAdoptSource string

//go:embed promiseadoptsrc/fetchslot.c
var fetchSlotSource string

// PromiseAdoptSource is adopt.c, behind kml_layout.h.
func PromiseAdoptSource() string { return layoutHeader() + promiseAdoptSource }

// FetchSlotSource is fetchslot.c, behind kml_layout.h.
func FetchSlotSource() string { return layoutHeader() + fetchSlotSource }
