package tests

import "testing"

// ADR-01061: the conformance run-timeouts. Every one was a program that
// terminates in Node and never did here — poison from a non-finite float→int
// conversion, an `unreachable` fall-off, a raw increment of a NaN-box, an
// unchecked Atomics index. Each test pairs a Node-oracle run with an
// `assertOutput` twin for the Node-less CI lanes.

// A NaN / ±Infinity `fromIndex` is ToIntegerOrInfinity'd (NaN → 0, -∞ → 0,
// +∞ → past the end), and a fromIndex past the end returns -1 rather than
// scanning off the array.
func TestE2EIndexOfNonFiniteFromIndex(t *testing.T) {
	src := `
const a = [true, false, true]
console.log(a.indexOf(true, -Infinity), a.indexOf(true, NaN), a.indexOf(true, -NaN))
console.log(a.indexOf(true, Infinity), a.indexOf(true, 5), a.indexOf(true, -5), a.indexOf(true, 1.5))
console.log([1, 2].indexOf(2, -1), [1, 2].indexOf(1, -1))
`
	assertOutput(t, src, "0 0 0\n-1 -1 0 2\n1 -1")
	assertSameAsNode(t, src)
}

// A float→int coercion of NaN/±Infinity is defined: the saturating
// conversion, then 0 for a narrow (ToInt32-style) target.
func TestE2ENonFiniteToIntCoercion(t *testing.T) {
	src := `
const xs = [1, 2, 3, 4, 5]
console.log(xs.slice(NaN, Infinity).length, xs.slice(-Infinity, NaN).length)
const u = new Uint8Array(3)
u[0] = NaN; u[1] = Infinity; u[2] = -Infinity
console.log(u[0], u[1], u[2])
const i32 = new Int32Array(1)
i32[0] = Infinity
console.log(i32[0], NaN | 0, Infinity >>> 0)
`
	assertOutput(t, src, "5 0\n0 0 0\n0 0 0")
	assertSameAsNode(t, src)
}

// A bare `new Set()` takes its element type from its `.add`/`.has` uses (as a
// bare `new Map()` already did), or from a `Set<T>` annotation; both used to
// be silently string-keyed, so `s.has(NaN)` strcmp'd a poison pointer.
func TestE2EBareNewSetElementInference(t *testing.T) {
	src := `
var s = new Set()
console.log(s.has(NaN), s.size)
s.add(NaN); s.add(NaN); s.add(1.5)
console.log(s.has(NaN), s.has(1.5), s.has(2), s.size)
const t: Set<number> = new Set()
t.add(2.5)
console.log(t.has(2.5), t.has(2), t.size)
const m = new Set()
m.add("x")
console.log(m.has("x"), m.has("y"))
`
	assertOutput(t, src, "false 0\ntrue true false 2\ntrue false 1\ntrue false")
	assertSameAsNode(t, src)
}

// A body that can fall off the end (or `return;`) yields `undefined` on that
// path, and the unannotated return type widens to `T | undefined` — tsc's
// inference. The fall-off used to be an `unreachable`, which clang -O2
// compiled into an infinite loop.
func TestE2EFallOffEndReturnsUndefined(t *testing.T) {
	src := `
function pick(x: number) {
  if (x > 0) return true
}
function label(x: number) {
  if (x === 1) return "one"
  if (x === 2) return
  return "many"
}
function loops(x: number) {
  while (true) {
    if (x > 3) return x
    x++
  }
}
console.log(pick(1), pick(-1), pick(-1) === undefined, typeof pick(-1))
console.log(label(1), label(2), label(3))
console.log(loops(0), loops(9))
class K {
  m(flag: boolean) {
    if (flag) return 42
  }
}
console.log(new K().m(true), new K().m(false))
`
	assertOutput(t, src, "true undefined true undefined\none undefined many\n4 9\n42 undefined")
	assertSameAsNode(t, src)
}

// `++`/`--` on an `any` operand: ToNumeric, step, store a number back; a
// postfix yields the numeric old value. The raw `add i64 …, 1` on the
// NaN-boxed word never terminated an untyped `for (j = 0; …; j++)`.
func TestE2EUpdateOnAnyOperand(t *testing.T) {
	src := `
let a: any = "5"
const old = a++
console.log(old, a, typeof a)
let b: any = "abc"
b--
console.log(b)
let c: any = true
console.log(++c, c)
let n: any = 0
let s = ""
for (let k = 0; k < 3; k++) { s += n++ }
console.log(s, n)
`
	assertOutput(t, src, "5 6 number\nNaN\n2 2\n012 3")
	assertSameAsNode(t, src)
}

// Untyped `var` bindings under -compat=js: labeled `continue`/`break` in nested
// loops with a `j++` on the boxed counter (the conformance shape).
func TestE2ECompatJSUntypedVarNestedLoops(t *testing.T) {
	src := `
var __str, index, index_n
__str = ""
outer: for (index = 0; index < 4; index += 1) {
  nested: for (index_n = 0; index_n <= index; index_n++) {
    if (index * index_n == 6) continue outer
    __str += "" + index + index_n
  }
}
console.log(__str)
__str = ""
outer2: for (index = 0; index < 4; index += 1) {
  for (index_n = 0; index_n <= index; index_n++) {
    if (index * index_n >= 4) break outer2
    __str += "" + index + index_n
  }
}
console.log(__str)
`
	assertOutputCompatJS(t, src, "0010112021223031\n0010112021")
	assertSameAsNodeCompatJS(t, src)
}

// Atomics: the length is read before the index is coerced, the index goes
// through ToIndex (an object's valueOf runs), an out-of-range index is a
// RangeError (not an unchecked address that `wait` then blocks on forever),
// value/timeout coerce in spec order, and a negative timeout times out at once.
func TestE2EAtomicsIndexValidation(t *testing.T) {
	src := `
const sab = new SharedArrayBuffer(16)
const ta = new Int32Array(sab)
console.log(Atomics.wait(ta, 0, 1), Atomics.wait(ta, 0, 0, 5), Atomics.wait(ta, 0, 0, -3), Atomics.wait(ta, "1", 1, 0))
try { Atomics.wait(ta, 4, 0, 0) } catch (e) { console.log(e.name) }
try { Atomics.load(ta, -1) } catch (e) { console.log(e.name) }
try { Atomics.store(ta, NaN, 7); console.log(Atomics.load(ta, 0)) } catch (e) { console.log(e.name) }
console.log(Atomics.add(ta, 2, 5), Atomics.load(ta, 2))
`
	assertOutput(t, src, "not-equal timed-out timed-out not-equal\nRangeError\nRangeError\n7\n0 5")
	assertSameAsNode(t, src)
}

func TestE2EAtomicsWaitGrowableLengthReadFirst(t *testing.T) {
	src := `
var gsab = new SharedArrayBuffer(0, {maxByteLength: 4})
var ta = new Int32Array(gsab)
var index = { valueOf() { gsab.grow(4); return 0 } }
var value = { valueOf() { throw new Error("Unexpected value coercion") } }
var timeout = { valueOf() { throw new Error("Unexpected timeout coercion") } }
try { Atomics.wait(ta, index, value, timeout); console.log("no throw") } catch (e) { console.log("caught", e.name) }
console.log(gsab.byteLength)
`
	// No Node twin: V8 (Node 24) still reads the length after coercing the
	// index, so it reaches the value coercion and throws that Error; the
	// spec (and test262) read the length first — RangeError.
	assertOutputCompatJS(t, src, "caught RangeError\n4")
}
