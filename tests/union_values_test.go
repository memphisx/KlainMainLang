package tests

import (
	"strings"
	"testing"
)

// union_values_test.go — scalar unions as *values*: a mixed-kind ternary and a
// union / nullable array element (ADR-01039).

// `c ? 1 : "a"` is `number | string`, as `??` already was for the same operand
// shape — it used to be a compile-time rejection, and `c ? 1 : true` silently
// coerced the boolean branch to a number.
func TestE2ETernaryMixedKindsIsUnion(t *testing.T) {
	assertOutput(t, `
function pick(c: boolean) { return c ? 1 : "a" }
console.log(pick(true), pick(false))
const c = process.argv.length > 99
const v = c ? 1 : "a"
console.log(v, typeof v)
const w: number | string = !c ? 2.5 : "b"
console.log(w, typeof w)
const b = c ? 1 : true
console.log(b, typeof b)
const s = c ? "yes" : false
console.log(s, typeof s, String(v) + "!")
if (typeof v === "string") { console.log(v.toUpperCase()) } else { console.log(v + 1) }
if (typeof w === "number") { console.log(w.toFixed(1)) }
const same = c ? 1 : 2
console.log(same + 1)
console.log(c ? 1 : (!c ? "mid" : true))
`, strings.Join([]string{
		"1 a", "a string", "2.5 number", "true boolean", "false boolean a!", "A", "2.5", "3", "mid",
	}, "\n"))
}

// `(number | string)[]`: one self-describing box per element — literal, push,
// index write, for-of narrowing, HOFs, search, join, JSON and console rendering.
func TestE2EUnionArrayElements(t *testing.T) {
	assertOutput(t, `
const xs: (number | string)[] = [1, "two", 3]
console.log(xs.length, xs[0], xs[1], xs.join("-"))
xs.push("four")
xs.push(5)
for (const x of xs) {
  if (typeof x === "string") console.log("s", x.toUpperCase())
  else console.log("n", x + 1)
}
console.log(xs.map(x => typeof x).join(","))
console.log(xs.filter(x => typeof x === "number").length)
console.log(xs.indexOf("two"), xs.includes(3), xs.includes("nope"))
const ys = [1, 2, 3].map(n => n % 2 === 0 ? n : "odd")
console.log(ys.join(","), ys.length)
console.log(JSON.stringify(xs))
function first(a: (number | string)[]): number | string { return a[0] }
console.log(first(xs), first(["z"]))
const bs: (boolean | number)[] = [true, 0, false]
console.log(bs.join(" "), bs)
console.log(xs)
xs[0] = "one"
console.log(xs[0], xs.slice(1, 3), xs.reverse()[0])
`, strings.Join([]string{
		"3 1 two 1-two-3",
		"n 2", "s TWO", "n 4", "s FOUR", "n 6",
		"number,string,number,string,number",
		"3",
		"1 true false",
		"odd,2,odd 3",
		`[1,"two",3,"four",5]`,
		"1 z",
		"true 0 false [ true, 0, false ]",
		"[ 1, 'two', 3, 'four', 5 ]",
		"one [ 'two', 3 ] 5",
	}, "\n"))
}

// `(number | undefined)[]` / `(number | null)[]`: an absent element is a real
// undefined/null — through reads, `??` (which unwraps to a plain number),
// narrowing, a `T | undefined` local, JSON, and a HOF whose callback returns
// `T | undefined` (`k => m.get(k)` stored the optional into a bare slot:
// invalid IR).
func TestE2ENullableArrayElements(t *testing.T) {
	assertOutput(t, `
const xs: (number | undefined)[] = [1, undefined, 3]
console.log(xs.length, xs[0], xs[1], xs)
console.log(xs[1] === undefined, xs[0] === undefined, typeof xs[1], typeof xs[0])
console.log((xs[1] ?? 7) + 1, (xs[0] ?? 7) + 1)
let total = 0
for (const x of xs) { if (x !== undefined) total += x }
console.log(total)
const ns: (number | null)[] = [null, 2]
console.log(ns, ns[0] === null, JSON.stringify(ns), JSON.stringify(xs))
ns.push(null)
ns.push(4)
console.log(ns.filter(n => n !== null).length, ns.map(n => n ?? -1).join(","))
const v: number | undefined = xs[2]
console.log(v, v === undefined ? "none" : v + 1)
const w: number | undefined = xs[1]
console.log(w, w === undefined, w ?? "gone")
const ids = [process.getuid?.(), process.pid > 0 ? 1 : undefined]
console.log(ids.length, ids[1])
const bs: (boolean | undefined)[] = [true, undefined]
console.log(bs, bs[1] ?? "dflt")
const m = new Map<string, number>([["a", 1]])
const got = ["a", "b"].map(k => m.get(k))
console.log(got, got[1] === undefined)
function opt(n?: number): any { return n }
console.log(opt(), opt(2))
`, strings.Join([]string{
		"3 1 undefined [ 1, undefined, 3 ]",
		"true false undefined number",
		"8 2",
		"4",
		"[ null, 2 ] true [null,2] [1,null,3]",
		"2 -1,2,-1,4",
		"3 4",
		"undefined true gone",
		"2 1",
		"[ true, undefined ] dflt",
		"[ 1, undefined ] true",
		"undefined 2",
	}, "\n"))
}

// `left ?? right` with a pointer left operand keeps the left's own type, so the
// result is usable in place (`(row ?? dflt).name` was "field access on
// non-object": emission returned an untyped pointer while inference returned the
// right operand's type), and stays `T | undefined` / `T | null` when the right
// operand can itself be absent.
func TestE2ENullCoalescePointerResultType(t *testing.T) {
	assertOutput(t, `
interface Row { name: string; n: number }
class P { x = 3; hi(): string { return "hi" } }
const dflt: Row = { name: "dflt", n: 0 }
function row(r?: Row) { return (r ?? dflt).name }
console.log(row(), row({ name: "given", n: 1 }))
function cls(p: P | null) { return (p ?? new P()).hi() + (p ?? new P()).x }
console.log(cls(null), cls(new P()))
function chain(a?: string, b?: string) { return a ?? b }
console.log(chain(), chain(undefined, "b"), chain("a", "b"))
const r = chain()
console.log(r === undefined, typeof r, String(r), r ?? "fallback")
function nul(a: string | null, b: string | null) { return a ?? b }
console.log(nul(null, null), nul(null, "b"), nul(null, null) === null)
function len(s?: string) { return (s ?? "four").length }
console.log(len(), len("sixsix"))
const m = new Map<string, Row>()
console.log((m.get("k") ?? dflt).n, m.get("k") ?? null)
`, strings.Join([]string{
		"dflt given", "hi3 hi3", "undefined b a", "true undefined undefined fallback",
		"null b true", "4 6", "0 null",
	}, "\n"))
}
