package tests

import "testing"

// Spread, array destructuring and Array.from run the iteration protocol on
// a value that is not an array: held in `any` (a generator, an object with
// a [Symbol.iterator]() method, a string), or a static object or class
// instance with a [Symbol.iterator] member. Array.from reads a
// non-iterable as an array-like.
func TestE2EIterableSpreadDestructureFrom(t *testing.T) {
	assertSameAsNode(t, `
const o: any = { *[Symbol.iterator]() { yield 1; yield 2 } }
const [a, b] = o
console.log(a, b, Array.from(o), [...o])
const s = { *[Symbol.iterator]() { yield 1; yield 2 } }
console.log([...s], Array.from(s))
class C { *[Symbol.iterator]() { yield 3; yield 4 } }
console.log([...new C()], Array.from(new C()))
const c = new C()
const [x, y] = c
console.log(x, y)
const al: any = { length: 2, 0: "p", 1: "q" }
console.log(Array.from(al), Array.from({ a: 1 } as any))
const str: any = "añb"
console.log(Array.from(str), [...str])
function* g() { yield 7 }
const gi: any = g()
console.log(Array.from(gi))
`)
	assertSameAsNode(t, `
const s = { *[Symbol.iterator]() { yield 4; yield 9 } }
console.log(Math.max(...s), Array.from(s, (x) => x * 2))
const o: any = { *[Symbol.iterator]() { yield "k"; yield "v" } }
function two(a: any, b: any) { return a + "=" + b }
console.log(Array.from(o, (x: any) => x.toUpperCase()))
const pairs: any = { *[Symbol.iterator]() { yield [1, "a"]; yield [2, "b"] } }
for (const [n, l] of pairs) console.log(n, l)
const [[p1, p2]] = pairs
console.log(p1, p2)
const arr: any[] = [[1, 2]]
const [[a, b]] = arr
console.log(a, b)
const v: any = [[3, 4]]
const [[c, d]] = v
console.log(c, d)
`)
	assertSameAsNode(t, `
const u: any = 5, o: any = { a: 1 }, n: any = null
try { console.log([...u]) } catch (e: any) { console.log(e.message) }
try { console.log([...o]) } catch (e: any) { console.log(e.message) }
try { console.log([...n]) } catch (e: any) { console.log(e.message) }
`)
	assertSameAsNode(t, `
function* g() { yield 1; yield 2 }
console.log(Array.from(g()), Array.from(g(), (x) => x * 10))
const al = { length: 3, 0: "a", 1: "b" }
console.log(Array.from(al), Array.from({ length: 2 }, (_, i) => i))
`)
}
