package tests

import (
	"testing"
)

// --- TDD-00084 Part A: a non-suspending async fn returns a real (settled) task
// promise, so .then/.catch/.finally and Promise combinators work on it, and a
// throwing async fn rejects instead of throwing synchronously — all without a
// fetch/fiber runtime (these programs never touch the network). ---

func TestE2EPartANonSuspendingThenChain(t *testing.T) {
	assertOutput(t, `
async function v(): Promise<number> { return 21 }
v().then((n: number) => n * 2).then((m: number) => { console.log(m) })
console.log("sync")
`, "sync\n42")
}

func TestE2EPartANonSuspendingThrowRejects(t *testing.T) {
	// A throwing non-suspending async fn rejects its promise; .catch recovers,
	// and (separately) await re-throws into a surrounding try/catch.
	assertOutput(t, `
async function bad(): Promise<number> { throw new Error("boom") }
bad().catch((e) => { console.log("caught") })
console.log("sync")
`, "sync\ncaught")
	assertOutput(t, `
async function bad(): Promise<number> { throw new Error("nope") }
async function run(): Promise<void> {
  try { const x = await bad(); console.log("got " + x) }
  catch (e) { console.log("caught " + e.message) }
}
run()
`, "caught nope")
}

func TestE2EPartANonSuspendingCombinators(t *testing.T) {
	// Promise.any skip-rejected and Promise.all over non-suspending async fns —
	// the previously-broken case (their results are now settled task promises).
	assertOutput(t, `
async function bad(n: number): Promise<number> { throw new Error("f" + n) }
async function good(): Promise<number> { return 42 }
async function run(): Promise<void> {
  const r: number = await Promise.any([bad(1), good(), bad(2)])
  console.log(r)
  const xs: number[] = await Promise.all([good(), good()])
  console.log(xs[0] + xs[1])
}
run()
`, "42\n84")
}

// --- async/await ---

func TestE2EAsyncAwaitNumber(t *testing.T) {
	assertOutput(t, `
async function add(a: number, b: number): Promise<number> {
    return a + b
}
const result = await add(3, 4)
console.log(result)
`, "7")
}
func TestE2EAsyncAwaitString(t *testing.T) {
	assertOutput(t, `
async function greet(name: string): Promise<string> {
    return "Hello, " + name + "!"
}
const msg = await greet("world")
console.log(msg)
`, "Hello, world!")
}
func TestE2EAsyncAwaitVoid(t *testing.T) {
	assertOutput(t, `
async function doNothing(): Promise<void> {
    console.log("doing nothing")
}
await doNothing()
`, "doing nothing")
}
func TestE2EAsyncChained(t *testing.T) {
	assertOutput(t, `
async function double(n: number): Promise<number> {
    return n * 2
}
async function addOne(n: number): Promise<number> {
    return n + 1
}
const a = await double(5)
const b = await addOne(a)
console.log(b)
`, "11")
}

// TestE2EAsyncArrowFunctionBlockBody covers a bug found while wiring
// http.listen's own async-handler support (ADR-00050): async *arrow*
// functions never got the Promise-wrapping treatment named async function
// declarations already had — emitClosureFunc never set up the async
// prologue/epilogue, so `return X` inside one returned X directly instead
// of wrapping it in the malloc'd Promise slot every caller expects. This
// was invisible before since named top-level functions can't be passed by
// reference (a separate, already-tracked limitation), so an async arrow
// function used to be the only way to get an async *callback* at all, and
// nothing had ever exercised one.
func TestE2EAsyncArrowFunctionBlockBody(t *testing.T) {
	assertOutput(t, `
const addAsync = async (a: number, b: number): Promise<number> => {
    return a + b
}
const result = await addAsync(3, 4)
console.log(result)
`, "7")
}

func TestE2EAsyncArrowFunctionExpressionBody(t *testing.T) {
	assertOutput(t, `
const doubleAsync = async (n: number): Promise<number> => n * 2
console.log(await doubleAsync(5))
`, "10")
}

// TestE2EAsyncPromiseArrayReturnLengthAndContents is a regression test for
// a bug found while implementing Promise.all (ADR-00073): emitAsyncPrologue
// and emitAwait's generic branch used to size/load a Promise<T> slot via
// promiseTy.Align()/.IR directly, which for an array-typed T is the bare
// 8-byte ptr — silently dropping the length half of the {ptr, i64} array
// value arrays are stored as everywhere else in this codebase. Fixed by
// using StructFieldSize/StructFieldIR instead — this test would have
// printed a garbage or zero length before that fix.
func TestE2EAsyncPromiseArrayReturnLengthAndContents(t *testing.T) {
	assertOutput(t, `
async function makeArr(): Promise<number[]> {
    const arr: number[] = [1, 2, 3, 4, 5]
    return arr
}
const arr = await makeArr()
console.log(arr.length)
for (const x of arr) {
    console.log(x)
}
`, "5\n1\n2\n3\n4\n5")
}

// --- Promise.all / .race / .allSettled over ordinary (non-fetch) promises ---
//
// Every ordinary async function's Promise is already fulfilled by the time
// its call returns (this compiler has no real suspension outside
// fetch()'s Promise<Response> — see emit_promise.go's own header comment
// and TDD-00016) — so there's nothing to parallelize here. These tests
// cover the honest, documented behavior for that case: .all collects in
// order, .race takes the first element, .allSettled always reports every
// entry fulfilled. The real-concurrency case (arrays of fetch()) is
// covered in tests/fetch_test.go instead.

func TestE2EPromiseAllOrdinaryPromises(t *testing.T) {
	assertOutput(t, `
async function double(n: number): Promise<number> {
    return n * 2
}
const arr: Array<Promise<number>> = []
arr.push(double(1))
arr.push(double(2))
arr.push(double(3))
const results = await Promise.all(arr)
console.log(results.length)
for (const x of results) {
    console.log(x)
}
`, "3\n2\n4\n6")
}

func TestE2EPromiseRaceOrdinaryPromises(t *testing.T) {
	assertOutput(t, `
async function double(n: number): Promise<number> {
    return n * 2
}
const arr: Array<Promise<number>> = []
arr.push(double(10))
arr.push(double(20))
const winner = await Promise.race(arr)
console.log(winner)
`, "20")
}

func TestE2EPromiseAllSettledOrdinaryPromises(t *testing.T) {
	assertOutput(t, `
async function double(n: number): Promise<number> {
    return n * 2
}
const arr: Array<Promise<number>> = []
arr.push(double(1))
arr.push(double(2))
const settled = await Promise.allSettled(arr)
for (const s of settled) {
    console.log(s.status)
    console.log(s.value)
}
`, "fulfilled\n2\nfulfilled\n4")
}

// A .catch/onRejected callback's error parameter is the error object — .message/
// .name (and AggregateError's .errors) work without annotating it, and an
// Error-family annotation (`e: Error`) resolves to the same shape. Fixes the
// former "annotate to access .message" caveat, which itself did not work.
func TestE2EPromiseCatchErrorParam(t *testing.T) {
	assertOutput(t, `
async function bad(m: string): Promise<number> { throw new Error(m) }
bad("one").catch((e) => { console.log("a:" + e.message) })
bad("two").catch((e: Error) => { console.log("b:" + e.name + ":" + e.message) })
console.log("sync")
`, "sync\na:one\nb:Error:two")
}

// --- Promise.resolve / Promise.reject + awaiting a .then/.catch chain (ADR-00262) ---

// Promise.resolve(v) is a settled task promise: awaitable, and .then/.catch work.
func TestE2EPromiseResolve(t *testing.T) {
	assertOutput(t, `
async function main2(): Promise<void> {
  console.log(await Promise.resolve(42))
  Promise.resolve(10).then((n: number) => { console.log("then " + n) })
}
main2()
`, "42\nthen 10")
}

// Promise.reject(e) is a settled rejected task promise: await re-throws, .catch recovers.
func TestE2EPromiseReject(t *testing.T) {
	assertOutput(t, `
async function main2(): Promise<void> {
  try { await Promise.reject(new Error("bad")) }
  catch (e) { console.log("caught " + e.message) }
  Promise.reject(new Error("later")).catch((e) => { console.log("catch " + e.message) })
}
main2()
`, "caught bad\ncatch later")
}

// Awaiting the result of a .then/.catch chain waits for the chain's microtask to
// run before reading the value — previously it read a pending promise (garbage
// value) and freed a slot the end-of-script drain then touched, a use-after-free.
func TestE2EAwaitThenChainResult(t *testing.T) {
	assertOutput(t, `
async function main2(): Promise<void> {
  const a = await Promise.resolve(5).then((n: number) => n * 2)
  console.log("a=" + a)
  const b = await Promise.resolve(1).then((n: number) => n + 1).then((m: number) => m * 10)
  console.log("b=" + b)
}
main2()
`, "a=10\nb=20")
}

// Awaiting a rejecting-source .catch chain runs the catch (recovering the value)
// before the await reads it.
func TestE2EAwaitCatchChainResult(t *testing.T) {
	assertOutput(t, `
async function bad(): Promise<number> { throw new Error("boom") }
async function main2(): Promise<void> {
  const x = await bad().catch((e) => { console.log("caught " + e.message); return 7 })
  console.log("x=" + x)
}
main2()
`, "caught boom\nx=7")
}

// --- new Promise((resolve, reject) => …) executor constructor (TDD-00087) ---

// Synchronous resolve/reject: the executor settles the promise before it returns.
func TestE2ENewPromiseSyncResolve(t *testing.T) {
	assertOutput(t, `
async function main2(): Promise<void> {
  const p = new Promise<number>((resolve, reject) => { resolve(42) })
  console.log(await p)
  const q = new Promise<number>((resolve, reject) => { reject(new Error("nope")) })
  try { await q } catch (e) { console.log("caught " + e.message) }
}
main2()
`, "42\ncaught nope")
}

// .then/.catch chain on a new Promise.
func TestE2ENewPromiseThenChain(t *testing.T) {
	assertOutput(t, `
async function main2(): Promise<void> {
  new Promise<number>((resolve) => resolve(7)).then((n: number) => { console.log("then " + n) })
  console.log("sync")
}
main2()
`, "sync\nthen 7")
}

// First settle wins: a later resolve/reject (and the value it would store) is ignored.
func TestE2ENewPromiseFirstSettleWins(t *testing.T) {
	assertOutput(t, `
async function main2(): Promise<void> {
  const p = new Promise<number>((resolve, reject) => { resolve(1); resolve(2); reject(new Error("x")) })
  console.log(await p)
}
main2()
`, "1")
}

// A deferred resolve from a setTimeout callback: awaiting drives the timer.
func TestE2ENewPromiseDeferredResolve(t *testing.T) {
	assertOutput(t, `
async function main2(): Promise<void> {
  const p = new Promise<number>((resolve) => { setTimeout(() => { resolve(42) }, 5) })
  console.log("waiting")
  console.log(await p)
}
main2()
`, "waiting\n42")
}

// Promise.reject is Promise<never> — its (never-produced) value assigns to any
// binding type, so `const s: string = await Promise.reject(...)` type-checks
// (the value is dead — await re-throws). Closes the former reject-value-typing
// caveat (ADR-00264).
func TestE2EPromiseRejectNeverTyping(t *testing.T) {
	assertOutput(t, `
async function main2(): Promise<void> {
  try { const s: string = await Promise.reject(new Error("boom")); console.log(s) }
  catch (e) { console.log("caught " + e.message) }
  const r: string = await Promise.reject(new Error("x")).catch((e) => "recovered")
  console.log(r)
}
main2()
`, "caught boom\nrecovered")
}

// --- Promise representation unification (TDD-00087 follow-up / ADR-00265) ---
// Async methods now emit the same task-struct promise async functions do, so a
// value typed `Promise<T>` has one representation and `await` reads the right slot.

// An explicitly `: Promise<T>`-annotated variable awaits correctly (previously it
// read the state field — the annotation discarded the task-shape tag).
func TestE2EAwaitAnnotatedPromiseVar(t *testing.T) {
	assertOutput(t, `
async function f(): Promise<number> { return 5 }
async function main2(): Promise<void> {
  const p: Promise<number> = f()
  console.log(await p)
  const q: Promise<number> = Promise.resolve(9)
  console.log(await q)
}
main2()
`, "5\n9")
}

// A plain (non-async) function returning new Promise, awaited by the caller.
func TestE2ESyncFnReturnsNewPromise(t *testing.T) {
	assertOutput(t, `
function make(ok: boolean): Promise<string> {
  return new Promise<string>((resolve, reject) => {
    if (ok) { resolve("yes") } else { reject(new Error("no")) }
  })
}
async function main2(): Promise<void> {
  console.log(await make(true))
  try { await make(false) } catch (e) { console.log("err " + e.message) }
}
main2()
`, "yes\nerr no")
}

// An async method's Promise<T> result awaits/chains like a function's — and stored
// through a Promise<T>-typed field/binding reads correctly.
func TestE2EAsyncMethodResultThroughAnnotation(t *testing.T) {
	assertOutput(t, `
class Svc {
  async load(): Promise<number> { return 21 }
}
async function main2(): Promise<void> {
  const s = new Svc()
  const p: Promise<number> = s.load()
  console.log(await p)
  s.load().then((n: number) => { console.log("then " + n) })
}
main2()
`, "21\nthen 21")
}

// Async-return flattening (ADR-00265): `return <promise>` from an async fn adopts
// that promise's state, rather than double-wrapping it.
func TestE2EAsyncReturnFlattening(t *testing.T) {
	assertOutput(t, `
async function make(ok: boolean): Promise<string> {
  return new Promise<string>((resolve, reject) => {
    if (ok) { resolve("yes") } else { reject(new Error("no")) }
  })
}
async function viaResolve(): Promise<number> { return Promise.resolve(5) }
async function main2(): Promise<void> {
  console.log(await make(true))
  try { await make(false) } catch (e) { console.log("err " + e.message) }
  console.log(await viaResolve())
}
main2()
`, "yes\nerr no\n5")
}

// The canonical delay() helper: an async fn returning a deferred new Promise.
func TestE2EAsyncDelayHelper(t *testing.T) {
	assertOutput(t, `
async function delay(ms: number): Promise<string> {
  return new Promise<string>((resolve) => { setTimeout(() => { resolve("waited " + ms) }, ms) })
}
async function main2(): Promise<void> { console.log(await delay(5)) }
main2()
`, "waited 5")
}

// new Promise<void> — resolve takes no argument (the executor's resolve is
// `() => void`, not `(v: void) => void`, which was invalid IR). ADR-00266.
func TestE2ENewPromiseVoid(t *testing.T) {
	assertOutput(t, `
async function delayMs(ms: number): Promise<void> {
  await new Promise<void>((resolve) => { setTimeout(() => { resolve() }, ms) })
}
async function main2(): Promise<void> {
  console.log("before")
  await delayMs(5)
  console.log("after")
}
main2()
`, "before\nafter")
}

// new Promise's executor may be a function expression or a closure-typed variable,
// not only an arrow literal (ADR-00267).
func TestE2ENewPromiseExecutorForms(t *testing.T) {
	assertOutput(t, `
async function main2(): Promise<void> {
  const a = new Promise<number>(function(resolve, reject) { resolve(5) })
  console.log(await a)
  const exec = (resolve: (n: number) => void, reject: (e: Error) => void) => { resolve(8) }
  const b = new Promise<number>(exec)
  console.log(await b)
}
main2()
`, "5\n8")
}

// --- Microtask-accurate await ordering (TDD-00088 / ADR-00268) ---
// Every await yields a microtask tick, even of an already-settled promise, and
// the resume is a microtask ordered in FIFO with .then/queueMicrotask.

func TestE2EAwaitYieldsFireAndForget(t *testing.T) {
	assertOutput(t, `
async function f(): Promise<void> { console.log("a"); await Promise.resolve(1); console.log("b") }
f()
console.log("c")
`, "a\nc\nb")
}

func TestE2EAwaitYieldsVsThen(t *testing.T) {
	assertOutput(t, `
async function main2(): Promise<void> {
  console.log("1")
  Promise.resolve().then(() => console.log("3"))
  console.log("2")
  await Promise.resolve()
  console.log("4")
}
main2()
`, "1\n2\n3\n4")
}

func TestE2EAwaitFIFOInterleave(t *testing.T) {
	assertOutput(t, `
async function a(): Promise<void> { console.log("a1"); await Promise.resolve(); console.log("a2"); await Promise.resolve(); console.log("a3") }
async function b(): Promise<void> { console.log("b1"); await Promise.resolve(); console.log("b2"); await Promise.resolve(); console.log("b3") }
a(); b()
console.log("sync")
queueMicrotask(() => console.log("qm"))
`, "a1\nb1\nsync\na2\nb2\nqm\na3\nb3")
}

// --- TDD-00090: a Promise is a reusable value — `await` and the combinators no
// longer free (consume) the promise slot, so the same promise can be awaited
// again or read after a combinator. Previously any of these was a use-after-free
// (SIGTRAP), including the double-await case that made the `await` row's
// zero-caveat "strict" claim untrue. ---

func TestE2EDoubleAwaitSamePromise(t *testing.T) {
	assertOutput(t, `
async function f(n: number): Promise<number> { return n }
async function main2(): Promise<void> {
  const p = f(42)
  console.log(await p)
  console.log(await p)
}
main2()
`, "42\n42")
}

func TestE2EAwaitMemberAfterCombinator(t *testing.T) {
	assertOutput(t, `
async function f(n: number): Promise<number> { return n }
async function main2(): Promise<void> {
  const p = f(5)
  const q = f(9)
  const arr = await Promise.all([p, q])
  console.log(arr[0])
  console.log(arr[1])
  console.log(await p)
  console.log(await q)
}
main2()
`, "5\n9\n5\n9")
}

func TestE2ESamePromiseTwiceInCombinator(t *testing.T) {
	assertOutput(t, `
async function f(n: number): Promise<number> { return n }
async function main2(): Promise<void> {
  const p = f(3)
  const arr = await Promise.all([p, p])
  console.log(arr[0])
  console.log(arr[1])
  console.log(await p)
}
main2()
`, "3\n3\n3")
}

func TestE2EAllSettledMemberReuse(t *testing.T) {
	assertOutput(t, `
async function f(n: number): Promise<number> { return n }
async function main2(): Promise<void> {
  const p = f(8)
  const s = await Promise.allSettled([p])
  console.log(s[0].status)
  console.log(await p)
}
main2()
`, "fulfilled\n8")
}

// --- TDD-00091: thenable adoption — resolve(aPromise) settles the outer promise
// when the inner one settles (value forwarded on fulfill, error on reject),
// instead of coercing a promise to the value type. Reaction-based, so it's a
// microtask tick like .then (not synchronous). ---

func TestE2EThenableAdoptFulfilled(t *testing.T) {
	assertOutput(t, `
async function inner(): Promise<number> { return 7 }
async function main2(): Promise<void> {
  const p = new Promise<number>((resolve) => { resolve(inner()) })
  console.log(await p)
}
main2()
`, "7")
}

func TestE2EThenableAdoptRejected(t *testing.T) {
	assertOutput(t, `
async function bad(): Promise<number> { throw new Error("inner fail") }
async function main2(): Promise<void> {
  try {
    await new Promise<number>((resolve) => { resolve(bad()) })
  } catch (e) {
    console.log("caught " + e.message)
  }
}
main2()
`, "caught inner fail")
}

func TestE2EThenableAdoptDeferred(t *testing.T) {
	assertOutput(t, `
function delayP(v: number, ms: number): Promise<number> {
  return new Promise<number>((res) => setTimeout(() => res(v), ms))
}
async function main2(): Promise<void> {
  console.log(await new Promise<number>((resolve) => { resolve(delayP(99, 10)) }))
}
main2()
`, "99")
}

func TestE2EThenableAdoptIsMicrotask(t *testing.T) {
	assertOutput(t, `
async function inner(): Promise<number> { return 11 }
async function main2(): Promise<void> {
  console.log("1")
  const outer = new Promise<number>((resolve) => { resolve(inner()) })
  console.log("2")
  outer.then((v: number) => { console.log("then " + v) })
  console.log("3")
  await outer
}
main2()
`, "1\n2\n3\nthen 11")
}

// A plain-value resolve still works (adoption only fires for a Promise argument).
func TestE2EResolvePlainValueStillWorks(t *testing.T) {
	assertOutput(t, `
async function main2(): Promise<void> {
  console.log(await new Promise<number>((resolve) => { resolve(5) }))
}
main2()
`, "5")
}

// A bare top-level-function reference is a valid executor (caveat B was stale).
func TestE2ENewPromiseTopLevelFnExecutor(t *testing.T) {
	assertOutput(t, `
function execOk(resolve: (v: number) => void, reject: (e: Error) => void): void { resolve(42) }
async function main2(): Promise<void> {
  console.log(await new Promise<number>(execOk))
}
main2()
`, "42")
}

// --- TDD-00092: `for await...of` over a sync array — JS awaits each element, so
// an array of promises is consumed sequentially and an array of plain values
// awaits each as a harmless identity. ---

func TestE2EForAwaitOverArrayOfPromises(t *testing.T) {
	assertOutput(t, `
async function f(n: number): Promise<number> { return n * 2 }
async function main2(): Promise<void> {
  const ps = [f(1), f(2), f(3)]
  for await (const x of ps) { console.log(x) }
}
main2()
`, "2\n4\n6")
}

func TestE2EForAwaitOverArrayOfValues(t *testing.T) {
	assertOutput(t, `
async function main2(): Promise<void> {
  for await (const v of [10, 20, 30]) { console.log(v) }
}
main2()
`, "10\n20\n30")
}

func TestE2EForAwaitOverArrayRejectionPropagates(t *testing.T) {
	assertOutput(t, `
async function f(n: number): Promise<number> { return n }
async function bad(): Promise<number> { throw new Error("elem fail") }
async function main2(): Promise<void> {
  try {
    for await (const x of [f(1), bad(), f(3)]) { console.log(x) }
  } catch (e) {
    console.log("caught " + e.message)
  }
}
main2()
`, "1\ncaught elem fail")
}

func TestE2EForAwaitOverArrayDestructure(t *testing.T) {
	assertOutput(t, `
async function pt(a: number, b: number): Promise<{ x: number; y: number }> { return { x: a, y: b } }
async function main2(): Promise<void> {
  for await (const { x, y } of [pt(1, 2), pt(3, 4)]) { console.log(x + y) }
}
main2()
`, "3\n7")
}

// --- Generic-type array suffix (`Promise<number>[]`, `Map<K,V>[]`) — a parser
// bug where the Promise/Array/Set/Map branches returned before consuming a
// trailing `[]`, so the annotation swallowed the initializer. ---

func TestE2EGenericArraySuffixAnnotation(t *testing.T) {
	assertOutput(t, `
async function f(n: number): Promise<number> { return n * 10 }
async function main2(): Promise<void> {
  const ps: Promise<number>[] = [f(1), f(2)]
  for await (const x of ps) { console.log(x) }
}
main2()
`, "10\n20")
}

func TestE2EMapArraySuffixAnnotation(t *testing.T) {
	assertOutput(t, `
const m: Map<string, number>[] = []
const one = new Map<string, number>()
one.set("a", 1)
m.push(one)
console.log(m.length)
console.log(m[0].get("a"))
`, "1\n1")
}
