package tests

import "testing"

// Promise combinators over task promises (ADR-01193): a pending promise at
// once, settled by member reactions, chainable with .then/.catch. Node is the
// oracle, ordering included.
func TestE2EPromiseCombinatorsChain(t *testing.T) {
	assertSameAsNode(t, `
async function f(): Promise<number> { await null; throw new Error("y") }
async function v(n: number, ms: number): Promise<number> { await new Promise<void>((r) => setTimeout(r, ms)); return n }
Promise.all([v(1, 20), v(2, 5)]).then((xs) => console.log("all", xs.join(",")))
Promise.all([f(), v(1, 5)]).catch((e: Error) => console.log("all rejected", e.message))
Promise.allSettled([f(), v(3, 1)]).then((r) => console.log("settled", r.map((x) => x.status).join(",")))
Promise.race([v(4, 30), v(5, 5)]).then((x) => console.log("race", x))
Promise.any([f(), v(6, 10)]).then((x) => console.log("any", x))
Promise.any([f(), f()]).catch((e: Error) => console.log("any rejected", e.name, (e as AggregateError).errors.length))
const xs: number[] = await Promise.all([v(7, 1), v(8, 2)])
console.log("awaited", xs[0] + xs[1])
console.log("sync end")
`)
}

// `instanceof Promise` and `instanceof Array` of a value held in `any` are
// answered at run time (ADR-01194).
func TestE2EInstanceofPromiseAndArrayThroughAny(t *testing.T) {
	assertSameAsNode(t, `
const p = Promise.resolve(1)
const a: any = p
const b: any = [1, 2]
const c: any = { x: 1 }
console.log(p instanceof Promise, a instanceof Promise, b instanceof Promise, c instanceof Promise)
console.log(b instanceof Array, a instanceof Array, [1] instanceof Array)
process.on('unhandledRejection', (reason: any, promise: any) => console.log('unh', promise instanceof Promise))
Promise.reject(1)
`)
}

// `.finally`'s promise settles as Node's does: two microtasks after its
// callback (ADR-01195).
func TestE2EFinallySettlesAfterTwoTicks(t *testing.T) {
	assertSameAsNode(t, `
Promise.reject(new Error("x")).finally(() => console.log("fin")).catch(() => console.log("caught"))
Promise.resolve(5).finally(() => console.log("fin2")).then((v) => console.log("then", v))
Promise.resolve().then(() => console.log("t1")).then(() => console.log("t2")).then(() => console.log("t3")).then(() => console.log("t4")).then(() => console.log("t5"))
Promise.resolve(1).finally(() => new Promise<void>((r) => setTimeout(r, 50))).then((v) => console.log("after", v))
setTimeout(() => console.log("timer"), 10)
Promise.resolve(2).finally(async () => { throw new Error("finfail") }).catch((e: Error) => console.log("finally threw", e.message))
`)
}
