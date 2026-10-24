package tests

import "testing"

// Promise.withResolvers (ES2024): a promise and the functions settling it,
// with the type argument explicit or taken from the context.
func TestE2EPromiseWithResolversSameAsNode(t *testing.T) {
	assertSameAsNode(t, `
const r = Promise.withResolvers<string>()
r.promise.catch((e: any) => console.log("err", e.message))
r.reject(new Error("boom"))
const c: PromiseWithResolvers<boolean> = Promise.withResolvers()
c.promise.then((v) => console.log("ctx", v))
c.resolve(true)
console.log(typeof Promise.withResolvers, typeof r.resolve)
async function main(): Promise<void> {
  const { promise, resolve } = Promise.withResolvers<number>()
  setTimeout(() => resolve(42), 5)
  console.log("got", await promise)
  const w = Promise.withResolvers<string>()
  setTimeout(() => w.resolve("later"), 5)
  console.log(await w.promise)
}
main()
`)
}
