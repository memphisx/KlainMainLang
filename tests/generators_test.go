package tests

import (
	"testing"
)

// --- Generator functions (TDD-00061/ADR-00172) ---
//
// A generator instance is its own fiber (a private ucontext_t + stack),
// reusing this compiler's existing http.listen/fetch() fiber primitive
// (TDD-00006 Part 2). V1 scope: top-level function declarations only, a
// plain (non-destructured) parameter list, a non-array element type, an
// explicit return type annotation, `yield`/bare `yield` (not `yield*`).

func TestE2EGeneratorBasicSequence(t *testing.T) {
	assertOutput(t, `
function* gen(): number {
    yield 1;
    yield 2;
    return 3;
}
const g = gen();
const r1 = g.next();
console.log(r1.value, r1.done);
const r2 = g.next();
console.log(r2.value, r2.done);
const r3 = g.next();
console.log(r3.value, r3.done);
`, "1\nfalse\n2\nfalse\n3\ntrue")
}

func TestE2EGeneratorNextAfterDoneReturnsZeroValue(t *testing.T) {
	// Calling .next() again on an already-finished generator is a no-op in
	// real JS returning {value: undefined, done: true} — this compiler's
	// own zero-value stand-in (no general "undefined" sentinel for a
	// concrete scalar type), not the last real yielded/returned value.
	assertOutput(t, `
function* gen(): number {
    yield 1;
    return 2;
}
const g = gen();
g.next();
const r2 = g.next();
console.log(r2.value, r2.done);
const r3 = g.next();
console.log(r3.value, r3.done);
`, "2\ntrue\n0\ntrue")
}

func TestE2EGeneratorSentValue(t *testing.T) {
	// The value yield "returns" is whatever the *next* .next() call sends
	// in, not anything related to what was yielded out.
	assertOutput(t, `
function* echo(): number {
    const a = yield 1;
    const b = yield a + 10;
    return b * 2;
}
const g = echo();
console.log(g.next().value);
console.log(g.next(5).value);
console.log(g.next(7).value);
`, "1\n15\n14")
}

func TestE2EGeneratorWithParameters(t *testing.T) {
	assertOutput(t, `
function* countFrom(start: number, step: number): number {
    let n = start;
    while (true) {
        yield n;
        n += step;
    }
}
const g = countFrom(10, 5);
console.log(g.next().value);
console.log(g.next().value);
console.log(g.next().value);
console.log(g.next().value);
`, "10\n15\n20\n25")
}

func TestE2EGeneratorStringElementType(t *testing.T) {
	assertOutput(t, `
function* names(): string {
    yield "alice";
    yield "bob";
}
const g = names();
let r = g.next();
while (!r.done) {
    console.log(r.value);
    r = g.next();
}
`, "alice\nbob")
}

func TestE2EGeneratorBareYield(t *testing.T) {
	assertOutput(t, `
function* ticker(): number {
    yield;
    yield 42;
}
const g = ticker();
console.log(g.next().value);
console.log(g.next().value);
`, "0\n42")
}

func TestE2EGeneratorNoYieldRunsToCompletionOnFirstNext(t *testing.T) {
	// Legal, if unusual, real JS: a generator that never yields still
	// works, just resolving directly to {value: <return>, done: true} on
	// its very first .next() call, without ever needing a second one.
	assertOutput(t, `
function* gen(): number {
    return 42;
}
const g = gen();
const r = g.next();
console.log(r.value, r.done);
`, "42\ntrue")
}

func TestE2EGeneratorClosureInsideBody(t *testing.T) {
	// A nested arrow function declared inside a generator's own body must
	// behave as an ordinary closure — its own `return` must never be
	// misrouted into the *enclosing* generator's own suspend machinery
	// (a real bug found and fixed during this feature's own development).
	assertOutput(t, `
function* gen(): number {
    let n = 0;
    const inc = (): number => { n = n + 1; return n; };
    yield inc();
    yield inc();
    yield inc();
}
const g = gen();
console.log(g.next().value);
console.log(g.next().value);
console.log(g.next().value);
`, "1\n2\n3")
}

func TestE2EMultipleIndependentGeneratorInstances(t *testing.T) {
	// Two instances of the same generator function must have fully
	// independent state (their own fiber, own struct, own local
	// variables) — advancing one must not affect the other at all.
	assertOutput(t, `
function* counter(): number {
    let n = 0;
    while (true) {
        n += 1;
        yield n;
    }
}
const a = counter();
const b = counter();
console.log(a.next().value);
console.log(a.next().value);
console.log(b.next().value);
console.log(a.next().value);
console.log(b.next().value);
`, "1\n2\n1\n3\n2")
}

func TestE2EInterleavedGeneratorsWithAllocation(t *testing.T) {
	// Stress-shaped regression guard: several generators interleaved,
	// each allocating on every iteration (array literals) — exercises the
	// fiber-stack machinery under real allocation pressure, the same
	// shape that matters most for -mm=gc (verified separately, manually,
	// under -mm=gc with heavy allocation to pressure a real Boehm
	// collection mid-suspend; not re-run here since GC mode needs
	// libgc/bdw-gc installed and this suite runs in manual mode).
	assertOutput(t, `
function* gen(id: number): number {
    let i = 0;
    while (i < 50) {
        const junk: number[] = [1, 2, 3, 4, 5];
        i = i + junk.length;
        yield id * 1000 + i;
    }
    return -1;
}
const g1 = gen(1);
const g2 = gen(2);
let total: number = 0;
let count: number = 0;
let r1 = g1.next();
let r2 = g2.next();
while (!r1.done || !r2.done) {
    if (!r1.done) { total += r1.value; count += 1; r1 = g1.next(); }
    if (!r2.done) { total += r2.value; count += 1; r2 = g2.next(); }
}
console.log(count);
console.log(total);
`, "20\n30550")
}

// --- for...of over a generator (TDD-00061/ADR-00172, second slice) ---

func TestE2EForOfGeneratorInline(t *testing.T) {
	assertOutput(t, `
function* gen(): number {
    yield 1;
    yield 2;
    yield 3;
    return 99;
}
for (const x of gen()) {
    console.log(x);
}
`, "1\n2\n3")
}

func TestE2EForOfGeneratorConstructsExactlyOnce(t *testing.T) {
	// s.Iterable (`gen()`) must be evaluated exactly once, constructing one
	// generator instance — not re-evaluated (and so re-constructed) on
	// every loop iteration.
	assertOutput(t, `
function* gen(): number {
    console.log("constructed");
    yield 1;
    yield 2;
}
for (const x of gen()) {
    console.log(x);
}
`, "constructed\n1\n2")
}

func TestE2EForOfGeneratorBreak(t *testing.T) {
	assertOutput(t, `
function* gen(): number {
    yield 1;
    yield 2;
    yield 3;
}
for (const x of gen()) {
    console.log(x);
    if (x === 2) { break; }
}
console.log("done");
`, "1\n2\ndone")
}

func TestE2EForOfGeneratorContinue(t *testing.T) {
	assertOutput(t, `
function* gen(): number {
    yield 1;
    yield 2;
    yield 3;
    yield 4;
}
let sum: number = 0;
for (const x of gen()) {
    if (x % 2 === 0) { continue; }
    sum += x;
}
console.log(sum);
`, "4")
}

func TestE2EForOfGeneratorVariable(t *testing.T) {
	assertOutput(t, `
function* gen(): number {
    yield 1;
    yield 2;
    yield 3;
}
const g = gen();
for (const x of g) {
    console.log(x);
}
`, "1\n2\n3")
}

func TestE2EForOfGeneratorStringElementType(t *testing.T) {
	assertOutput(t, `
function* names(): string {
    yield "alice";
    yield "bob";
}
for (const name of names()) {
    console.log(name);
}
`, "alice\nbob")
}
