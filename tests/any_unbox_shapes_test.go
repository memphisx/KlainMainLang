package tests

import "testing"

// concat spreads an `any` argument holding an Array (IsConcatSpreadable)
// and appends any other value; `[].concat(…)` of `any` arguments is any[].
func TestE2EArrayConcatSpreadsAnyArrays(t *testing.T) {
	assertSameAsNode(t, `
const answers: any = { A: [{ type: "A" }, { type: "A2" }], B: [{ type: "B" }] }
answers.ANY = [].concat(answers.A, answers.B)
console.log(JSON.stringify(answers.ANY))
const x: any[] = []
console.log(JSON.stringify(x.concat(answers.A, answers.B, 5, "q", null)))
const nums: any = [1, 2]
const y: any[] = [0]
console.log(JSON.stringify(y.concat(nums, [[3]], { a: 1 }, new Uint8Array(2) as any)))
`)
}

// An `any` holding an array converts into a tuple, a nested array and an
// array of tuples, whether the array is static or dynamic.
func TestE2EAnyIntoTuplesAndNestedArrays(t *testing.T) {
	assertSameAsNode(t, `
const a: any = [1, "x"]
const t1: [number, string] = a
console.log(t1[1], t1[0])
const v: [number, string] = [2, "y"]
const b: any = v
const t2: [number, string] = b
console.log(t2[1], t2[0])
const j: any = JSON.parse("[[1,\"a\"],[2,\"b\"]]")
const m: [number, string][] = j
console.log(m[1][1], m.length)
const n: any = JSON.parse("[[1,2],[3]]")
const nn: number[][] = n
console.log(nn[1][0], nn.length)
const z: { [k: string]: [number, string][] } = { a: [[1, "x"]] }
for (const [k, s] of z["a"]) console.log(k, s)
const z2: { [k: string]: number[][] } = { a: [[1, 2]] }
const r = z2["a"]
console.log(r.length, r[0][1])
function f() {
  const z3: { [k: string]: [number, number][] } = { a: [[4, 5]] }
  const rr = z3["a"]
  console.log(rr[0][0], rr[0][1])
}
f()
`)
}

// Buffer.concat of a list with a spread element.
func TestE2EBufferConcatSpreadList(t *testing.T) {
	assertSameAsNode(t, `
function u16(n: number): Buffer { const b = Buffer.alloc(2); b.writeUInt16BE(n, 0); return b }
const msg = Buffer.from([1, 2, 3, 4])
const rrs: Buffer[] = [Buffer.from([9]), Buffer.from([8])]
console.log(Buffer.concat([msg.subarray(0, 2), u16(5), ...rrs]))
console.log(Buffer.concat([...rrs]).length)
`)
}

// new a.b.C() rooted at a value: an object's member, a module's export.
func TestE2ENewThroughValuePath(t *testing.T) {
	assertSameAsNodeImports(t, `
import dns from "dns"
import * as dnsNs from "dns"
const o = { k: { C: class { y = 2 } } }
console.log(new o.k.C().y)
console.log(typeof new dns.promises.Resolver().resolve4)
console.log(typeof new dnsNs.promises.Resolver().resolve4)
`)
}
