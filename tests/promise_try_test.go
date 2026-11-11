package tests

import "testing"

// Promise.try (ES2025): the callback runs now, with the extra arguments;
// its value, an adopted thenable, or its throw settles the promise.
func TestE2EPromiseTrySameAsNode(t *testing.T) {
	assertSameAsNode(t, `
const order: string[] = []
const p = Promise.try(() => { order.push("cb"); return 1 })
order.push("after")
async function main(): Promise<void> {
  console.log(await p, order.join(","))
  console.log(await Promise.try((a: number, b: number) => a + b, 2, 3))
  console.log(await Promise.try(() => Promise.resolve(41)) + 1)
  try { await Promise.try(() => { throw new TypeError("sync") }) } catch (e: any) { console.log(e.name, e.message) }
  try { await Promise.try(async () => { throw new RangeError("async") }) } catch (e: any) { console.log(e.name, e.message) }
  console.log(typeof Promise.try, Promise.try.length, Promise.try.name)
}
main()
`)
}

// Awaited<T> in a generic body is T's settled value for every
// instantiation, not an array.
func TestE2EAwaitedInGenericBody(t *testing.T) {
	assertSameAsNode(t, `
function r<T>(v: T): Awaited<T> { return v as any }
console.log(r(4), r("s"), r(true), r({ a: 1 }).a, r([1, 2]).length, r(10n))
async function u<T>(v: T): Promise<Awaited<T>> { return v as any }
async function main(): Promise<void> {
  console.log(await u(Promise.resolve(3)), await u("x"))
}
main()
`)
}

// A value typed `T | PromiseLike<T>` holds either: a number, or a promise
// that awaits to one.
func TestE2EValueOrPromiseLike(t *testing.T) {
	assertSameAsNode(t, `
const g: () => number | PromiseLike<number> = () => Promise.resolve(41);
const v = g();
console.log(typeof v, await v);
let w: number | PromiseLike<number> = 3;
console.log(typeof w, await w);
w = Promise.resolve(8);
console.log(typeof w, await w);
const r = await Promise.try(() => Promise.resolve(5) as number | PromiseLike<number>);
console.log(r);
`)
}
