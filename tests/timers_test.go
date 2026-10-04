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

// The same self-cancelling-interval bug via a plain *assignment* to a
// pre-declared `let` (`id = setInterval(...)`) rather than a `const` var-decl
// init — a distinct codegen path (emitAssign) that also stored into the
// variable's pre-promotion alloca while the callback's capture had been boxed
// to a different cell, so the closure saw a stale id and clearInterval never
// matched. Fixed by re-resolving the variable's storage after the RHS runs.
func TestE2ESetIntervalSelfCancelsViaAssignment(t *testing.T) {
	assertOutput(t, `
let count: number = 0
let id: NodeJS.Timeout | undefined = undefined
id = setInterval(() => {
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

// --- setImmediate/clearImmediate ---

func TestE2ESetImmediateFiresAfterSyncCode(t *testing.T) {
	assertOutput(t, `
console.log("sync")
setImmediate(() => {
    console.log("immediate")
})
console.log("still sync")
`, "sync\nstill sync\nimmediate")
}

func TestE2ESetImmediateFiresBeforeLaterTimeout(t *testing.T) {
	assertOutput(t, `
setTimeout(() => { console.log("timeout") }, 10)
setImmediate(() => { console.log("immediate") })
`, "immediate\ntimeout")
}

func TestE2EClearImmediateCancelsBeforeFiring(t *testing.T) {
	assertOutput(t, `
const id = setImmediate(() => {
    console.log("should not print")
})
clearImmediate(id)
console.log("cancelled")
`, "cancelled")
}

func TestE2ESetImmediateWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`setImmediate()`)
	if err == nil {
		t.Fatal("expected a compile error for setImmediate() with no arguments, got none")
	}
}

func TestE2ESetImmediateNonFunctionCallbackRejected(t *testing.T) {
	_, err := parseAndCompile(`setImmediate(5)`)
	if err == nil {
		t.Fatal("expected a compile error for setImmediate with a non-function argument, got none")
	}
}

func TestE2EClearImmediateWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`clearImmediate()`)
	if err == nil {
		t.Fatal("expected a compile error for clearImmediate() with no arguments, got none")
	}
}

// The arguments after a timer's delay (setImmediate's and nextTick's after
// the callback) are evaluated at the schedule and passed when it fires; a
// builtin method is a callback too.
func TestE2ETimerExtraArguments(t *testing.T) {
	assertOutput(t, `
let n = 1
setTimeout(console.log, 5, "t", n)
n = 2
setTimeout((a: number, b: string) => console.log(a + 1, b), 1, 41, "s")
const iv = setInterval((x: string) => { console.log("iv", x); clearInterval(iv) }, 2, "y")
process.nextTick((a: string, b: number) => console.log("tick", a, b), "q", 3)
setTimeout((a: number) => console.log("one", a), 3, 1, 2, 3)
setTimeout(() => console.log("none"), 4, 1)
function inner(x?: number): number { console.log("inner", x); return 1 }
setTimeout(inner, 6)
`, "tick q 3\n42 s\niv y\none 1\nnone\nt 1\ninner undefined")
}

// A console method referenced by value is bound, as Node's are.
func TestE2EConsoleMethodAsValue(t *testing.T) {
	assertOutput(t, `
[1, 2].forEach(console.log)
const f = console.log
f("x", 2)
Promise.resolve(3).then(console.log)
console.log(typeof console.log, console.log.name)
`, "1 0 [ 1, 2 ]\n2 1 [ 1, 2 ]\nx 2\nfunction log\n3")
}

// A timer or nextTick used as a value, through a constant, or given a
// spread passes its arguments on; a function held in `any` receives them
// through the dynamic call, an omitted optional parameter undefined.
func TestE2ETimerAsValueAndSpreadArguments(t *testing.T) {
	assertOutput(t, `
const p = process
p.nextTick(() => console.log("alias"))
const nt = process.nextTick
nt((a: string, b?: number) => console.log("value", a, b), "v")
const st = setTimeout
st((...r: any[]) => console.log("rest", r), 1, 1, 2)
const xs: any[] = [7, 8]
setTimeout((a: number, b: number) => console.log("spread", a, b), 2, ...xs)
setTimeout((...r: any[]) => console.log("empty", r), 3, ...[])
const k: any = (a: string, b?: number) => console.log("any", a, b)
setTimeout(k, 4, "z")
console.log("sync")
`, "sync\nalias\nvalue v undefined\nrest [ 1, 2 ]\nspread 7 8\nempty []\nany z undefined")
}

// A timer handle's methods, through their declarations: refresh re-arms a
// one-shot that already fired but not a cleared one, and ref/unref/
// refresh/close return the handle.
func TestE2ETimerHandleMethods(t *testing.T) {
	assertOutput(t, `
let n = 0
const t = setTimeout(() => {
    n++
    console.log("fire", n)
    if (n < 2) setTimeout(() => t.refresh(), 5)
}, 10)
console.log(t.refresh() === t, t.hasRef(), t.unref() === t, t.hasRef(), t.ref() === t)
const c = setTimeout(() => console.log("cleared fired"), 5)
clearTimeout(c)
c.refresh()
const i = setImmediate(() => console.log("imm"))
console.log(i.hasRef(), i.unref() === i, i.hasRef())
i.ref()
const d = setTimeout(() => console.log("closed fired"), 5)
console.log(d.close() === d)
`, "true true true false true\ntrue true false\ntrue\nimm\nfire 1\nfire 2")
}
