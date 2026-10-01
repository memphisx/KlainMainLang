package tests

import "testing"

// Language fixes the TypeScript builtin modules needed (ADR-01282,
// ADR-01283), each compared with Node's output.

func TestE2EFunctionOwnPropertyCall(t *testing.T) {
	assertOutput(t, `
function f(a: number): number { return a }
function g(s: string): string { return s + '!' }
f.g = g
f.n = 5
console.log(f.g('x'), f.n, f(1))
`, "x! 5 1")
}

func TestE2EFunctionTypeIsCallable(t *testing.T) {
	assertOutput(t, `
function call(f: Function, x: number) { return f(x) }
function nm(g: Function) { return g.name }
const fs: Function[] = [() => 1, function b() { return 2 }]
console.log(call((n: number) => n * 2, 21), JSON.stringify(nm(function named() {})), typeof fs[0], fs[1].name)
`, `42 "named" function b`)
}

func TestE2EBigIntModuleGlobal(t *testing.T) {
	assertOutput(t, `
const big = 12345678901234567890n
let c = 1n
function g() { c = c * 2n; return big + c }
console.log(g(), g(), typeof c)
`, "12345678901234567892n 12345678901234567894n bigint")
}

func TestE2ENewPromiseValueType(t *testing.T) {
	assertOutput(t, `
async function m() {
  const v = await new Promise<number>((r) => r(1))
  const q: Promise<number> = new Promise((r) => r(3))
  const w = await q
  const a = await new Promise((r) => setTimeout(() => r(7), 1))
  const c = await new Promise<string>((r) => r('s'))
  console.log(v + 1, w + 1, a, c.length)
}
m()
`, "2 4 7 1")
}

func TestE2EObjectIsAndKeysOfBoxedArrays(t *testing.T) {
	assertOutput(t, `
console.log(Object.is(-0, 0), Object.is(NaN, NaN), Object.is(1, 1), Object.is('a', 'a'), Object.is({}, {}), Object.is(null, undefined), Object.is(-0, -0))
const o = {}
const a: any = o
console.log(Object.is(a, o))
const arr: any = [7, 8]
const bytes: any = new Uint8Array([1, 2, 3])
console.log(Object.keys(arr).join('|'), Object.keys(bytes).join('|'))
`, "false true true true false false true\ntrue\n0|1 0|1|2")
}

func TestE2EHostMembersThroughAny(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>([['k', 1]])
const a: any = m
console.log(a.size, a.get('k'), a.has('z'))
a.set('z', 2)
console.log(m.get('z'), a.size)
const s = new Set<number>([1])
const b: any = s
b.add(5)
console.log(b.has(5), b.size, s.has(5))
const r: any = /x(\d)/
console.log(r.exec('ax7')[1], r.lastIndex, String(r), r instanceof RegExp, r.source)
const t = /a/g
console.log(String(t), `+"`${t}`"+`, t.toString())
`, "1 1 false\n2 2\ntrue 2 true\n7 0 /x(\\d)/ true x(\\d)\n/a/g /a/g /a/g")
}
