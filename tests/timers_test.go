package tests

import (
	"testing"
)

// --- setTimeout/clearTimeout/setInterval/clearInterval ---
//
// Real wall-clock delays, kept small (a handful of ms) so the suite stays
// fast. Assertions are on order/behavior, never on exact timing (matching
// the same convention console.timeEnd's own tests already use).

func TestE2ESetTimeoutFires(t *testing.T) {
	assertOutput(t, `
console.log("sync")
setTimeout(() => {
    console.log("fired")
}, 5)
`, "sync\nfired")
}

func TestE2ESetTimeoutOrdersByDelayNotRegistration(t *testing.T) {
	assertOutput(t, `
setTimeout(() => { console.log("C") }, 30)
setTimeout(() => { console.log("A") }, 5)
setTimeout(() => { console.log("B") }, 15)
console.log("sync")
`, "sync\nA\nB\nC")
}

func TestE2EClearTimeoutCancelsBeforeFiring(t *testing.T) {
	assertOutput(t, `
const id = setTimeout(() => {
    console.log("should not print")
}, 20)
clearTimeout(id)
console.log("cancelled")
`, "cancelled")
}

func TestE2ESetIntervalRepeatsAndSelfCancels(t *testing.T) {
	// Regression test for a real bug found while writing this test: the
	// idiomatic self-cancelling-interval pattern (the interval's own
	// callback reads the `id` its own declaration is in the middle of
	// producing) silently never cancelled anything, because emitVarDecl
	// stored the real setInterval() return value into the variable's
	// pre-promotion alloca — but the callback's capture had already been
	// boxed to a *different*, freshly-malloc'd cell (ADR-00001) by the time
	// that store happened, so the closure only ever saw the cell's stale,
	// pre-initialization value. Fixed by re-resolving the variable's
	// current storage location (via a fresh lookup) right before the final
	// store, instead of trusting the pointer captured before the
	// initializer ran.
	assertOutput(t, `
let count: number = 0
const id = setInterval(() => {
    count = count + 1
    console.log("tick " + count)
    if (count >= 3) {
        clearInterval(id)
    }
}, 5)
`, "tick 1\ntick 2\ntick 3")
}

func TestE2EProcessExitSkipsPendingTimers(t *testing.T) {
	assertOutput(t, `
setTimeout(() => {
    console.log("should not print")
}, 10)
console.log("before exit")
process.exit(0)
`, "before exit")
}

func TestE2ESetTimeoutUncaughtThrowPropagates(t *testing.T) {
	_, code := compileAndRunExpectExit(t, `
setTimeout(() => {
    throw new Error("boom from timer")
}, 5)
console.log("sync done")
`)
	if code == 0 {
		t.Fatal("expected a non-zero exit code for an uncaught throw from a timer callback, got 0")
	}
}

func TestE2ESetTimeoutWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`setTimeout()`)
	if err == nil {
		t.Fatal("expected a compile error for setTimeout() with no arguments, got none")
	}
}

func TestE2ESetTimeoutNonFunctionCallbackRejected(t *testing.T) {
	_, err := parseAndCompile(`setTimeout(5, 10)`)
	if err == nil {
		t.Fatal("expected a compile error for setTimeout with a non-function first argument, got none")
	}
}

func TestE2EClearIntervalWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`clearInterval()`)
	if err == nil {
		t.Fatal("expected a compile error for clearInterval() with no arguments, got none")
	}
}
