package tests

import (
	"testing"
)

// --- Generator functions (TDD-00061/ADR-00172) ---
//
// A generator instance is its own fiber (a private ucontext_t + stack),
// reusing this compiler's existing http.listen/fetch() fiber primitive
// (TDD-00006 Part 2). V1 scope: top-level function declarations only, a
// plain (non-destructured) parameter list, an explicit return type
// annotation, `yield`/bare `yield` (not `yield*`). An array element type is
// supported (ADR-00676) — yielded/sent arrays round-trip as the inline
// {ptr,i64} aggregate through every generator slot.

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
`, "1 false\n2 false\n3 true")
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
`, "2 true\n0 true")
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
`, "42 true")
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

// --- TDD-00085: async generators (`async function*`) + `for await...of` ---

// A basic async generator consumed by for await...of.
func TestE2EAsyncGeneratorForAwait(t *testing.T) {
	assertOutput(t, `
async function* g(): number { yield 1; yield 2; yield 3 }
async function main2(): Promise<void> {
  for await (const x of g()) { console.log(x) }
  console.log("done")
}
main2()
`, "1\n2\n3\ndone")
}

// An async generator that awaits inside its body between yields.
func TestE2EAsyncGeneratorAwaitsInBody(t *testing.T) {
	assertOutput(t, `
async function double(n: number): Promise<number> { return n * 2 }
async function* g(): number {
  const xs: number[] = [1, 2, 3]
  for (const n of xs) { yield await double(n) }
}
async function main2(): Promise<void> {
  for await (const x of g()) { console.log(x) }
}
main2()
`, "2\n4\n6")
}

// Manual .next() returns a Promise<{value,done}>; a throw in the body rejects the
// outstanding .next() promise (re-thrown at the awaiting consumer).
func TestE2EAsyncGeneratorThrowRejects(t *testing.T) {
	assertOutput(t, `
async function* g(): number { yield 1; throw new Error("boom") }
async function main2(): Promise<void> {
  const it = g()
  const a = await it.next()
  console.log(a.value)
  console.log(a.done)
  try {
    await it.next()
    console.log("no throw")
  } catch (e) {
    console.log("caught " + e.message)
  }
}
main2()
`, "1\nfalse\ncaught boom")
}

// A destructuring loop variable in `for await...of`: the yielded object's fields
// (or a tuple's positions) bind in the body, reusing the same unpack core the
// sync for-of destructuring uses (ADR-00257).
func TestE2EForAwaitObjectDestructure(t *testing.T) {
	assertOutput(t, `
async function* pts(): { x: number; y: number } {
  yield { x: 1, y: 2 }
  yield { x: 3, y: 4 }
}
async function main2(): Promise<void> {
  for await (const { x, y } of pts()) { console.log(x + y) }
}
main2()
`, "3\n7")
}

func TestE2EForAwaitTupleDestructure(t *testing.T) {
	assertOutput(t, `
async function* pairs(): [number, number] {
  yield [1, 2]
  yield [3, 4]
}
async function main2(): Promise<void> {
  for await (const [a, b] of pairs()) { console.log(a * b) }
}
main2()
`, "2\n12")
}

// --- TDD-00089: Symbol.asyncIterator — user-defined async iterables ---

// A class implementing the async-iteration protocol by hand: a
// [Symbol.asyncIterator]() returning a separate iterator object whose async
// next() yields {value,done}. Consumed by for await...of.
func TestE2EAsyncIterableSeparateIterator(t *testing.T) {
	assertOutput(t, `
class RangeIter {
  private cur: number
  private end: number
  constructor(end: number) { this.cur = 0; this.end = end }
  async next(): Promise<{ value: number; done: boolean }> {
    if (this.cur >= this.end) { return { value: 0, done: true } }
    const v = this.cur
    this.cur = this.cur + 1
    return { value: v, done: false }
  }
}
class Range {
  private end: number
  constructor(end: number) { this.end = end }
  [Symbol.asyncIterator](): RangeIter { return new RangeIter(this.end) }
}
async function main2(): Promise<void> {
  for await (const x of new Range(4)) { console.log(x) }
  console.log("done")
}
main2()
`, "0\n1\n2\n3\ndone")
}

// The [Symbol.asyncIterator]() returns `this` (the object is its own iterator),
// and next() awaits between elements — the common self-iterator pattern.
func TestE2EAsyncIterableSelfWithAwait(t *testing.T) {
	assertOutput(t, `
async function delay(v: number): Promise<number> { return v }
class Ticker {
  private i: number
  private n: number
  constructor(n: number) { this.i = 0; this.n = n }
  [Symbol.asyncIterator](): Ticker { return this }
  async next(): Promise<{ value: number; done: boolean }> {
    if (this.i >= this.n) { return { value: -1, done: true } }
    const cur = await delay(this.i * 10)
    this.i = this.i + 1
    return { value: cur, done: false }
  }
}
async function main2(): Promise<void> {
  for await (const t of new Ticker(3)) { console.log(t) }
}
main2()
`, "0\n10\n20")
}

// A rejection from the iterator's next() (a throw) re-throws at the awaiting
// for-await, catchable with try/catch.
func TestE2EAsyncIterableThrowPropagates(t *testing.T) {
	assertOutput(t, `
class Boom {
  [Symbol.asyncIterator](): Boom { return this }
  async next(): Promise<{ value: number; done: boolean }> {
    throw new Error("kaboom")
  }
}
async function main2(): Promise<void> {
  try {
    for await (const b of new Boom()) { console.log(b) }
  } catch (e) {
    console.log("caught " + e.message)
  }
  console.log("done")
}
main2()
`, "caught kaboom\ndone")
}

// A destructuring loop variable over a user async iterable reuses the same
// unpack core the async-generator for-await path uses.
func TestE2EAsyncIterableDestructure(t *testing.T) {
	assertOutput(t, `
class PairIter {
  private i: number
  constructor() { this.i = 0 }
  [Symbol.asyncIterator](): PairIter { return this }
  async next(): Promise<{ value: { a: number; b: number }; done: boolean }> {
    if (this.i >= 3) { return { value: { a: 0, b: 0 }, done: true } }
    const j = this.i
    this.i = this.i + 1
    return { value: { a: j, b: j * j }, done: false }
  }
}
async function main2(): Promise<void> {
  for await (const { a, b } of new PairIter()) { console.log(a + b) }
}
main2()
`, "0\n2\n6")
}

// The same destructuring loop variable over a *sync* generator — previously the
// pattern was ignored and its bindings were undefined (fixed alongside for-await,
// ADR-00257). The tuple form also exercises the yield-with-tuple-hint fix: a
// `yield [a, b]` into a tuple slot now builds the tuple directly.
func TestE2EForOfGeneratorObjectDestructure(t *testing.T) {
	assertOutput(t, `
function* pts(): { x: number; y: number } {
  yield { x: 5, y: 6 }
  yield { x: 7, y: 8 }
}
for (const { x, y } of pts()) { console.log(x * y) }
`, "30\n56")
}

func TestE2EForOfGeneratorTupleDestructure(t *testing.T) {
	assertOutput(t, `
function* pairs(): [number, number] {
  yield [1, 2]
  yield [3, 4]
}
for (const [a, b] of pairs()) { console.log(a + b) }
`, "3\n7")
}

// for await...of over a sync Map iterates its values, awaiting each (identity
// for plain values); a Set iterates its elements — same shapes as sync for-of.
func TestE2EForAwaitOverSyncMapAndSet(t *testing.T) {
	assertOutput(t, `
async function main2(): Promise<void> {
  const m = new Map<string, number>()
  m.set("a", 5)
  m.set("b", 7)
  for await (const v of m) { console.log(v) }
  const s = new Set<string>()
  s.add("hi")
  s.add("yo")
  for await (const t of s) { console.log(t) }
}
main2()
`, "5\n7\nhi\nyo")
}

// for await...of over a sync generator (CreateAsyncFromSyncIterator): plain
// yields are identity-awaited; promise yields are awaited to their values.
func TestE2EForAwaitOverSyncGenerator(t *testing.T) {
	assertOutput(t, `
function* nums(): number {
  yield 1
  yield 2
  yield 3
}
async function work(n: number): Promise<number> { return n * 10 }
function* jobs(): Promise<number> {
  yield work(1)
  yield work(2)
}
async function main2(): Promise<void> {
  for await (const n of nums()) { console.log(n) }
  for await (const v of jobs()) { console.log(v) }
}
main2()
`, "1\n2\n3\n10\n20")
}

// A destructuring loop variable over a sync generator in for await, and a
// rejecting promise yield propagating out of the loop.
func TestE2EForAwaitOverSyncGeneratorDestructureAndReject(t *testing.T) {
	assertOutput(t, `
function* points(): { x: number; y: number } {
  yield { x: 1, y: 2 }
  yield { x: 3, y: 4 }
}
async function ok(n: number): Promise<number> { return n }
async function bad(): Promise<number> { throw new Error("gen elem fail") }
function* mixed(): Promise<number> {
  yield ok(1)
  yield bad()
  yield ok(3)
}
async function main2(): Promise<void> {
  for await (const { x, y } of points()) { console.log(x + y) }
  try {
    for await (const v of mixed()) { console.log(v) }
  } catch (e) {
    console.log("caught " + e.message)
  }
}
main2()
`, "3\n7\n1\ncaught gen elem fail")
}

// A class [Symbol.iterator]() method (desugared to @@iterator) drives sync
// for...of via the spec's {value, done} protocol — `return this` self-iterators
// and separate per-loop iterator objects both work (a second loop over the same
// iterable gets a fresh iterator).
func TestE2EForOfSymbolIterator(t *testing.T) {
	assertOutput(t, `
class Countdown {
  n: number
  constructor(start: number) { this.n = start }
  [Symbol.iterator](): Countdown { return this }
  next(): { value: number; done: boolean } {
    if (this.n <= 0) { return { value: 0, done: true } }
    const v = this.n
    this.n = this.n - 1
    return { value: v, done: false }
  }
}
class RangeIter {
  i: number
  end: number
  constructor(i: number, end: number) { this.i = i; this.end = end }
  next(): { value: number; done: boolean } {
    if (this.i >= this.end) { return { value: 0, done: true } }
    const v = this.i
    this.i = this.i + 1
    return { value: v, done: false }
  }
}
class Range {
  a: number
  b: number
  constructor(a: number, b: number) { this.a = a; this.b = b }
  [Symbol.iterator](): RangeIter { return new RangeIter(this.a, this.b) }
}
for (const x of new Countdown(3)) { console.log(x) }
const r = new Range(0, 3)
for (const x of r) { console.log(x) }
for (const x of r) { console.log(x + 10) }
`, "3\n2\n1\n0\n1\n2\n10\n11\n12")
}

// A sync [Symbol.iterator] iterable is also consumable by for await (each value
// identity-awaited).
func TestE2EForAwaitOverSymbolIteratorClass(t *testing.T) {
	assertOutput(t, `
class Countdown {
  n: number
  constructor(start: number) { this.n = start }
  [Symbol.iterator](): Countdown { return this }
  next(): { value: number; done: boolean } {
    if (this.n <= 0) { return { value: 0, done: true } }
    const v = this.n
    this.n = this.n - 1
    return { value: v, done: false }
  }
}
async function main2(): Promise<void> {
  for await (const x of new Countdown(2)) { console.log(x) }
}
main2()
`, "2\n1")
}

// An object literal with a [Symbol.asyncIterator] member (arrow-valued or
// method shorthand, desugared to a closure-typed @@asyncIterator field) is a
// for-await iterable — returning an async generator, a sync generator, or a
// class-instance iterator.
func TestE2EForAwaitObjectLiteralAsyncIterator(t *testing.T) {
	assertOutput(t, `
async function work(n: number): Promise<number> { return n * 3 }
async function* agen(): number {
  yield await work(1)
  yield await work(2)
}
function* sgen(): number {
  yield 7
  yield 8
}
class Ticks {
  n: number
  constructor() { this.n = 0 }
  async next(): Promise<{ value: number; done: boolean }> {
    this.n = this.n + 1
    if (this.n > 2) { return { value: 0, done: true } }
    return { value: this.n * 100, done: false }
  }
}
async function main2(): Promise<void> {
  const objArrow = { [Symbol.asyncIterator]: () => agen() }
  for await (const x of objArrow) { console.log(x) }
  const objMethod = { [Symbol.asyncIterator]() { return sgen() } }
  for await (const x of objMethod) { console.log(x) }
  const objClass = { [Symbol.asyncIterator]: () => new Ticks() }
  for await (const x of objClass) { console.log(x) }
}
main2()
`, "3\n6\n7\n8\n100\n200")
}

// An object literal with a [Symbol.iterator] member works in sync for...of
// (returning a sync generator or a [Symbol.iterator] class instance) and in
// for await (values identity-awaited).
func TestE2EForOfObjectLiteralSymbolIterator(t *testing.T) {
	assertOutput(t, `
function* sg(): number { yield 1; yield 2; }
class CIter {
  i: number
  constructor() { this.i = 10 }
  [Symbol.iterator](): CIter { return this }
  next(): { value: number; done: boolean } {
    if (this.i > 12) { return { value: 0, done: true } }
    const v = this.i
    this.i = this.i + 1
    return { value: v, done: false }
  }
}
function main1(): void {
  const o1 = { [Symbol.iterator]: () => sg() }
  for (const x of o1) { console.log(x) }
  const o2 = { [Symbol.iterator]() { return new CIter() } }
  for (const x of o2) { console.log(x) }
}
main1()
async function amain(): Promise<void> {
  const o3 = { [Symbol.iterator]: () => sg() }
  for await (const x of o3) { console.log(x + 100) }
}
amain()
`, "1\n2\n10\n11\n12\n101\n102")
}

// V8/spec step timing (node-diff verified, node v26): `.next()` starts the body
// SYNCHRONOUSLY up to the first await/yield; an await inside the body parks the
// step (its promise stays pending, the consumer's script continues) and resumes
// via a microtask when the awaited promise settles.
func TestE2EAsyncGeneratorSyncStartAndParkInterleave(t *testing.T) {
	assertOutput(t, `
async function* g(): number {
  console.log("g1")
  await 0
  console.log("g2")
  yield 1
}
async function main2(): Promise<void> {
  const it = g()
  const p = it.next()
  queueMicrotask(() => { console.log("m") })
  const r = await p
  console.log("value " + r.value)
}
main2()
console.log("sync")
`, "g1\nsync\ng2\nm\nvalue 1")
}

// Two .next() calls before either result is awaited: the second request queues
// (the spec's AsyncGeneratorEnqueue) and is serviced after the first step
// settles — results correspond positionally (node-diff verified).
func TestE2EAsyncGeneratorDoubleNextQueues(t *testing.T) {
	assertOutput(t, `
async function work(n: number): Promise<number> { return n }
async function* g(): number {
  yield await work(1)
  yield await work(2)
}
async function main2(): Promise<void> {
  const it = g()
  const p1 = it.next()
  const p2 = it.next()
  const r2 = await p2
  const r1 = await p1
  console.log(r1.value + "," + r2.value)
}
main2()
console.log("sync")
`, "sync\n1,2")
}

// .return() runs enclosing finallys — including a finally that itself AWAITS
// (the step parks mid-finally and still settles {42, done:true} afterwards).
func TestE2EAsyncGeneratorReturnAwaitingFinally(t *testing.T) {
	assertOutput(t, `
async function tick(): Promise<number> { return 5 }
async function* g(): number {
  try {
    yield 1
    yield 2
  } finally {
    const t = await tick()
    console.log("fin " + t)
  }
}
async function main2(): Promise<void> {
  const it = g()
  const first = await it.next()
  console.log(first.value)
  const r = await it.return(42)
  console.log(r.value + " done=" + r.done)
  const after = await it.next()
  console.log("after done=" + after.done)
}
main2()
console.log("sync")
`, "sync\n1\nfin 5\n42 done=true\nafter done=true")
}

// TDD-00085 Stage 4: an async generator *method* (`async *m()`) — the sync
// generator-method machinery (this-binding via the __this slot) fused with the
// async-generator fiber. Covers `this` access, await in the body, for-await
// consumption, and throw->reject on a later .next().
func TestE2EAsyncGeneratorMethod(t *testing.T) {
	assertOutput(t, `
async function inc(n: number): Promise<number> { return n + 1 }
class Counter {
  base: number = 10
  async *gen(): number {
    yield await inc(this.base)
    yield await inc(this.base + 1)
  }
}
async function main2(): Promise<void> {
  const c = new Counter()
  for await (const x of c.gen()) { console.log(x) }
}
main2()
`, "11\n12")
}

func TestE2EAsyncGeneratorMethodThrowRejects(t *testing.T) {
	assertOutput(t, `
class Pager {
  items: number[] = [5, 6]
  async *pages(): number {
    for (const x of this.items) { yield x }
    throw new Error("end")
  }
}
async function main2(): Promise<void> {
  const p = new Pager()
  const it = p.pages()
  console.log((await it.next()).value)
  console.log((await it.next()).value)
  try { await it.next() } catch (e) { console.log("caught " + e.message) }
}
main2()
`, "5\n6\ncaught end")
}

// --- Generator protocol completion: .throw() / .return() / yield* (TDD-00086) ---

// .throw(e) injects the error at the suspension point; a body try/catch handles
// it and the generator resumes there.
func TestE2EGeneratorThrowCaughtInBody(t *testing.T) {
	assertOutput(t, `
function* g(): number {
  try { yield 1; yield 2 }
  catch (e) { console.log("caught " + e.message); yield 99 }
}
const it = g()
console.log(it.next().value)
console.log(it.throw(new Error("boom")).value)
console.log(it.next().done)
`, "1\ncaught boom\n99\ntrue")
}

// An uncaught .throw() propagates to the .throw() caller and finishes the generator.
func TestE2EGeneratorThrowUncaughtPropagates(t *testing.T) {
	assertOutput(t, `
function* g(): number { yield 1; yield 2 }
const it = g()
console.log(it.next().value)
try { it.throw(new Error("nope")); console.log("no throw") }
catch (e) { console.log("propagated " + e.message) }
console.log(it.next().done)
`, "1\npropagated nope\ntrue")
}

// A return inside a generator's try/finally runs the finally (previously skipped).
func TestE2EGeneratorReturnStatementRunsFinally(t *testing.T) {
	assertOutput(t, `
function* g(): number {
  try { yield 1; return 2 } finally { console.log("finally ran") }
}
const it = g()
console.log(it.next().value)
console.log(it.next().value)
`, "1\nfinally ran\n2")
}

// ADR-00613: `break`ing out of a `for...of` over a generator closes the iterator
// (drives its `.return()`), so an enclosing `finally` in the generator runs.
func TestE2EForOfGeneratorBreakRunsFinally(t *testing.T) {
	assertOutput(t, `
function* gen() {
  try { yield 1; yield 2; yield 3 } finally { console.log("cleanup") }
}
for (const v of gen()) {
  console.log("got", v)
  if (v === 2) break
}
console.log("after")
`, "got 1\ngot 2\ncleanup\nafter")
}

// Normal (unbroken) consumption still runs the finally exactly once, not twice.
func TestE2EForOfGeneratorNormalCompletionFinallyOnce(t *testing.T) {
	assertOutput(t, `
function* gen() {
  try { yield 1; yield 2 } finally { console.log("cleanup") }
}
for (const v of gen()) { console.log("got", v) }
console.log("after")
`, "got 1\ngot 2\ncleanup\nafter")
}

// ADR-00614: a `return` from a `for...of` body over a generator closes the
// iterator (runs its finally), interleaved with any enclosing body finally.
func TestE2EForOfGeneratorReturnFromBodyRunsFinally(t *testing.T) {
	assertOutput(t, `
function* gen() {
  try { yield 1; yield 2; yield 3 } finally { console.log("gen cleanup") }
}
function find(): number {
  for (const v of gen()) {
    console.log("got", v)
    if (v === 2) return v * 10
  }
  return -1
}
const r = find()
console.log("result", r)
`, "got 1\ngot 2\ngen cleanup\nresult 20")
}

// A body `try/finally` around the return runs innermost-first: the body finally,
// then the generator's iterator-close finally.
func TestE2EForOfGeneratorReturnNestedFinallyOrder(t *testing.T) {
	assertOutput(t, `
function* gen() {
  try { yield 1; yield 2 } finally { console.log("gen cleanup") }
}
function f(): number {
  for (const v of gen()) {
    try {
      if (v === 1) return 99
    } finally {
      console.log("body finally", v)
    }
  }
  return 0
}
const r = f()
console.log("r", r)
`, "body finally 1\ngen cleanup\nr 99")
}

// A labeled `break` to an outer loop closes the generator; a `continue` does not.
func TestE2EForOfGeneratorLabeledBreakAndContinue(t *testing.T) {
	assertOutput(t, `
function* gen() {
  try { yield 1; yield 2; yield 3 } finally { console.log("cleanup") }
}
outer: for (let i = 0; i < 2; i++) {
  for (const v of gen()) {
    if (v === 1) continue
    console.log(i, v)
    if (v === 2) break outer
  }
}
console.log("done")
`, "0 2\ncleanup\ndone")
}

// The classic infinite generator: break triggers the finally.
func TestE2EForOfInfiniteGeneratorBreakRunsFinally(t *testing.T) {
	assertOutput(t, `
function* naturals() {
  let n = 0
  try { while (true) { yield n++ } } finally { console.log("cleanup") }
}
for (const x of naturals()) {
  console.log(x)
  if (x >= 2) break
}
console.log("after")
`, "0\n1\n2\ncleanup\nafter")
}

// .return(v) completes the generator, running enclosing finally blocks, and
// yields {value: v, done: true}.
func TestE2EGeneratorReturnMethodRunsFinally(t *testing.T) {
	assertOutput(t, `
function* g(): number {
  try { yield 1; yield 2; yield 3 } finally { console.log("cleanup") }
}
const it = g()
console.log(it.next().value)
const r = it.return(42)
console.log(r.value + " done=" + r.done)
console.log(it.next().done)
`, "1\ncleanup\n42 done=true\ntrue")
}

// .return(v) on a not-yet-started generator completes it without running the body.
func TestE2EGeneratorReturnNotStarted(t *testing.T) {
	assertOutput(t, `
function* g(): number { yield 1; yield 2 }
const it = g()
const r = it.return(99)
console.log(r.value + " done=" + r.done)
console.log(it.next().done)
`, "99 done=true\ntrue")
}

// yield* delegates to an inner generator: it re-yields each inner value and
// evaluates to the inner's return value.
func TestE2EYieldStarDelegation(t *testing.T) {
	assertOutput(t, `
function* inner(): number { yield 1; yield 2; return 3 }
function* outer(): number {
  const r = yield* inner()
  console.log("inner returned " + r)
  yield 10
}
for (const x of outer()) { console.log(x) }
`, "1\n2\ninner returned 3\n10")
}

// yield* forwards each .next(v) sent value into the inner generator.
func TestE2EYieldStarForwardsSent(t *testing.T) {
	assertOutput(t, `
function* inner(): number {
  const a = yield 1
  const b = yield a + 10
  return b
}
function* outer(): number { const r = yield* inner(); yield r }
const it = outer()
console.log(it.next().value)
console.log(it.next(100).value)
console.log(it.next(200).value)
console.log(it.next().done)
`, "1\n110\n200\ntrue")
}

// yield* forwards a .throw() into the inner generator, which can catch it.
func TestE2EYieldStarForwardsThrow(t *testing.T) {
	assertOutput(t, `
function* inner(): number {
  try { yield 1; yield 2 }
  catch (e) { console.log("inner caught " + e.message); yield 99 }
}
function* outer(): number { yield* inner(); yield 100 }
const it = outer()
console.log(it.next().value)
console.log(it.throw(new Error("boom")).value)
console.log(it.next().value)
`, "1\ninner caught boom\n99\n100")
}

// yield* forwards a .return() into the inner (running its finally) and completes
// the outer generator too.
func TestE2EYieldStarForwardsReturn(t *testing.T) {
	assertOutput(t, `
function* inner(): number {
  try { yield 1; yield 2 } finally { console.log("inner cleanup") }
}
function* outer(): number { yield* inner(); yield 100 }
const it = outer()
console.log(it.next().value)
const r = it.return(42)
console.log(r.value + " done=" + r.done)
console.log(it.next().done)
`, "1\ninner cleanup\n42 done=true\ntrue")
}

// --- Async generator .throw() / .return() (TDD-00086 async extension) ---

// .throw(e) on an async generator injects at the suspension point; a body
// try/catch (which may await) handles it and the .throw() promise fulfils.
func TestE2EAsyncGeneratorThrowCaught(t *testing.T) {
	assertOutput(t, `
async function inc(n: number): Promise<number> { return n + 1 }
async function* g(): number {
  try { yield await inc(0); yield await inc(1) }
  catch (e) { console.log("caught " + e.message); yield 99 }
}
async function main2(): Promise<void> {
  const it = g()
  console.log((await it.next()).value)
  console.log((await it.throw(new Error("boom"))).value)
  console.log((await it.next()).done)
}
main2()
`, "1\ncaught boom\n99\ntrue")
}

// An uncaught .throw() rejects the returned promise and finishes the generator.
func TestE2EAsyncGeneratorThrowUncaughtRejects(t *testing.T) {
	assertOutput(t, `
async function* g(): number { yield 1; yield 2 }
async function main2(): Promise<void> {
  const it = g()
  console.log((await it.next()).value)
  try { await it.throw(new Error("nope")); console.log("no throw") }
  catch (e) { console.log("rejected " + e.message) }
  console.log((await it.next()).done)
}
main2()
`, "1\nrejected nope\ntrue")
}

// .return(v) completes an async generator, running enclosing finally blocks, and
// fulfils with {value: v, done: true}.
func TestE2EAsyncGeneratorReturnRunsFinally(t *testing.T) {
	assertOutput(t, `
async function* g(): number {
  try { yield 1; yield 2; yield 3 } finally { console.log("cleanup") }
}
async function main2(): Promise<void> {
  const it = g()
  console.log((await it.next()).value)
  const r = await it.return(42)
  console.log(r.value + " done=" + r.done)
  console.log((await it.next()).done)
}
main2()
`, "1\ncleanup\n42 done=true\ntrue")
}

// .return(v) on a not-yet-started async generator completes it without running
// the body.
func TestE2EAsyncGeneratorReturnNotStarted(t *testing.T) {
	assertOutput(t, `
async function* g(): number { yield 1; yield 2 }
async function main2(): Promise<void> {
  const it = g()
  const r = await it.return(7)
  console.log(r.value + " done=" + r.done)
}
main2()
`, "7 done=true")
}

// --- Async yield* delegation (TDD-00086 / ADR-00260 follow-on) ---

// An async generator can yield* another async generator: each inner step's
// Promise<{value,done}> is awaited, values re-yielded, and the inner's return
// value becomes the yield* expression's value.
func TestE2EAsyncYieldStarDelegation(t *testing.T) {
	assertOutput(t, `
async function inc(n: number): Promise<number> { return n + 1 }
async function* inner(): number { yield await inc(0); yield await inc(1); return 100 }
async function* outer(): number {
  const r = yield* inner()
  console.log("inner returned " + r)
  yield 10
}
async function main2(): Promise<void> {
  for await (const x of outer()) { console.log(x) }
}
main2()
`, "1\n2\ninner returned 100\n10")
}

// Sent values and .throw() forward through an async yield* into the inner.
func TestE2EAsyncYieldStarForwardsSentAndThrow(t *testing.T) {
	assertOutput(t, `
async function* inner(): number {
  try { const a = yield 1; yield a + 10 }
  catch (e) { console.log("inner caught " + e.message); yield 99 }
}
async function* outer(): number { yield* inner(); yield 100 }
async function main2(): Promise<void> {
  const it = outer()
  console.log((await it.next()).value)
  console.log((await it.next(50)).value)
  console.log((await it.throw(new Error("boom"))).value)
  console.log((await it.next()).value)
}
main2()
`, "1\n60\ninner caught boom\n99\n100")
}

// .return() forwards through an async yield*, running the inner's finally and
// completing the outer.
func TestE2EAsyncYieldStarForwardsReturn(t *testing.T) {
	assertOutput(t, `
async function* inner(): number {
  try { yield 1; yield 2 } finally { console.log("inner cleanup") }
}
async function* outer(): number { yield* inner(); yield 100 }
async function main2(): Promise<void> {
  const it = outer()
  console.log((await it.next()).value)
  const r = await it.return(42)
  console.log(r.value + " done=" + r.done)
  console.log((await it.next()).done)
}
main2()
`, "1\ninner cleanup\n42 done=true\ntrue")
}

// yield* over a user Symbol.asyncIterator iterable inside an async generator
// (TDD-00094 stage 3): each awaited element is re-yielded, then the outer
// continues past the delegation.
func TestE2EAsyncYieldStarOverAsyncIterable(t *testing.T) {
	assertOutput(t, `
async function d(v: number): Promise<number> { return v }
class Range {
  private i: number
  private n: number
  constructor(n: number) { this.i = 0; this.n = n }
  [Symbol.asyncIterator](): Range { return this }
  async next(): Promise<{ value: number; done: boolean }> {
    if (this.i >= this.n) { return { value: -1, done: true } }
    const cur = await d(this.i)
    this.i = this.i + 1
    return { value: cur, done: false }
  }
}
async function* outer(): number { yield* new Range(3); yield 100 }
async function main2(): Promise<void> {
  for await (const x of outer()) { console.log(x) }
}
main2()
`, "0\n1\n2\n100")
}

// .return(v) on an async generator suspended at `yield* asyncIterable` completes
// the outer (running its finallys) instead of silently resuming the inner — the
// V1 own-path behavior (no delegation into the inner's optional .return).
func TestE2EAsyncYieldStarIterableReturnCompletesOuter(t *testing.T) {
	assertOutput(t, `
async function d(v: number): Promise<number> { return v }
class Range {
  private i: number
  private n: number
  constructor(n: number) { this.i = 0; this.n = n }
  [Symbol.asyncIterator](): Range { return this }
  async next(): Promise<{ value: number; done: boolean }> {
    if (this.i >= this.n) { return { value: -1, done: true } }
    const cur = await d(this.i)
    this.i = this.i + 1
    return { value: cur, done: false }
  }
}
async function* outer(): number {
  try { yield* new Range(5) } finally { console.log("outer cleanup") }
  yield 100
}
async function main2(): Promise<void> {
  const it = outer()
  console.log((await it.next()).value)
  const r = await it.return(42)
  console.log(r.value + " done=" + r.done)
  console.log((await it.next()).done)
}
main2()
`, "0\nouter cleanup\n42 done=true\ntrue")
}

// .throw(e) on an async generator suspended at `yield* asyncIterable` propagates
// the error into the outer body (catchable there) instead of being swallowed by
// another inner step — the V1 own-path behavior.
func TestE2EAsyncYieldStarIterableThrowPropagatesToOuter(t *testing.T) {
	assertOutput(t, `
async function d(v: number): Promise<number> { return v }
class Range {
  private i: number
  private n: number
  constructor(n: number) { this.i = 0; this.n = n }
  [Symbol.asyncIterator](): Range { return this }
  async next(): Promise<{ value: number; done: boolean }> {
    if (this.i >= this.n) { return { value: -1, done: true } }
    const cur = await d(this.i)
    this.i = this.i + 1
    return { value: cur, done: false }
  }
}
async function* outer(): number {
  try { yield* new Range(5) } catch (e) { console.log("outer caught " + e.message); yield 77 }
}
async function main2(): Promise<void> {
  const it = outer()
  console.log((await it.next()).value)
  console.log((await it.throw(new Error("bang"))).value)
}
main2()
`, "0\nouter caught bang\n77")
}

// When the inner async-iterable has its own .throw/.return methods, a yield*
// delegates into them: .throw(e) forwards into inner.throw (which may recover and
// keep yielding), and .return(v) forwards into inner.return (which completes).
func TestE2EAsyncYieldStarIterableDelegatesThrowReturn(t *testing.T) {
	assertOutput(t, `
async function d(v: number): Promise<number> { return v }
class Range {
  private i: number
  private n: number
  constructor(n: number) { this.i = 0; this.n = n }
  [Symbol.asyncIterator](): Range { return this }
  async next(): Promise<{ value: number; done: boolean }> {
    if (this.i >= this.n) { return { value: -1, done: true } }
    const cur = await d(this.i); this.i = this.i + 1
    return { value: cur, done: false }
  }
  async throw(e: Error): Promise<{ value: number; done: boolean }> {
    console.log("inner throw " + e.message)
    return { value: 99, done: false }
  }
  async return(v: number): Promise<{ value: number; done: boolean }> {
    console.log("inner return " + v)
    return { value: v, done: true }
  }
}
async function* outer(): number { yield* new Range(5); yield 100 }
async function main2(): Promise<void> {
  const it = outer()
  console.log((await it.next()).value)
  console.log((await it.throw(new Error("x"))).value)
  const r = await it.return(7)
  console.log(r.value + " done=" + r.done)
  console.log((await it.next()).done)
}
main2()
`, "0\ninner throw x\n99\ninner return 7\n7 done=true\ntrue")
}

// yield* over an async generator inside a *sync* generator is a clean rejection
// (awaiting each step needs an async context).
func TestE2EYieldStarAsyncInSyncRejected(t *testing.T) {
	src := `
async function* inner(): number { yield 1 }
function* outer(): number { yield* inner() }
console.log(outer())
`
	if _, err := parseAndCompile(src); err == nil {
		t.Fatal("expected a compile error for yield* over an async generator in a sync generator, got none")
	}
}

// Generator expressions bound at top level (TDD-00096/ADR-00293) — sync and
// with parameters — plus yield-based element-type inference (no annotation).
func TestE2EGeneratorExpressionBoundTopLevel(t *testing.T) {
	assertOutput(t, `
var items = function* () {
  yield 1;
  yield 2;
  yield 3;
};
for (const v of items()) { console.log(v); }
const words = function* (prefix: string) {
  yield prefix + "a";
  yield prefix + "b";
};
for (const w of words("x")) { console.log(w); }
`, "1\n2\n3\nxa\nxb")
}

func TestE2EGeneratorYieldInferenceFloatJoin(t *testing.T) {
	assertOutput(t, `
const halves = function* (n: number) {
  for (let i = 1; i <= n; i++) { yield i * 1.5; }
};
let sum = 0.0;
for (const f of halves(3)) { sum = sum + f; }
console.log(sum);
function* mixed() { yield 1; yield 2.5; }
for (const m of mixed()) { console.log(m); }
`, "9\n1\n2.5")
}

func TestE2EGeneratorExpressionAsValueRejected(t *testing.T) {
	_, err := parseAndCompile(`
function take(f: () => void): void {}
take(function* () { yield 1; });
`)
	if err == nil {
		t.Fatal("expected a compile error for a generator expression used as a value")
	}
}

// --- Array element type (ADR-00676) ---
//
// A generator whose element type is an array yields/sends arrays that
// round-trip as the inline {ptr,i64} aggregate through the __yielded/__sent
// slots, the {value,done} result object, and every for-of/next()/yield*
// binding (a boxed {data,len} header on the loop-variable side, object-
// reference model). Regression-gated alongside the scalar generators above.

func TestE2EGeneratorArrayYieldForOf(t *testing.T) {
	assertOutput(t, `
function* rows(): number[] {
    yield [1, 2];
    yield [3, 4, 5];
    yield [];
}
for (const r of rows()) {
    console.log(r.length, r.join(","));
}
`, "2 1,2\n3 3,4,5\n0 ")
}

func TestE2EGeneratorArrayYieldManualNext(t *testing.T) {
	assertOutput(t, `
function* pairs(): number[] {
    const a = [10, 20];
    yield a;
    yield [30];
}
const it = pairs();
const r1 = it.next();
console.log(r1.done, r1.value.join("-"));
const r2 = it.next();
console.log(r2.done, r2.value.join("-"));
console.log(it.next().done);
`, "false 10-20\nfalse 30\ntrue")
}

func TestE2EGeneratorStringArrayYieldStarDelegate(t *testing.T) {
	assertOutput(t, `
function* words(): string[] {
    yield ["a", "b"];
    yield ["c"];
}
function* deleg(): string[] {
    yield ["x"];
    yield* words();
    yield ["z"];
}
for (const w of deleg()) { console.log(w.join("+")); }
`, "x\na+b\nc\nz")
}

func TestE2EAsyncGeneratorArrayYieldForAwait(t *testing.T) {
	assertOutputImports(t, `
async function* agen(): number[] {
    yield [1, 2];
    yield [3, 4, 5];
}
async function main2() {
    for await (const r of agen()) {
        console.log(r.length, r.join(","));
    }
}
main2();
`, "2 1,2\n3 3,4,5")
}
