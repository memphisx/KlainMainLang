package tests

import "testing"

// --- Spread in call arguments (TDD-00106 / ADR-00335 V1, ADR-00336 V2) ---
//
// V1: a single spread fills a callee's rest parameter, optionally after fixed
// args: f(...arr), f(a, ...arr).
// V2: any number of spreads freely mixed with positional args, still only
// within the rest region: f(...a, ...b), f(...a, x, ...b), obj.m(...a); plus
// spread into the variadic builtins console.log / Math.max / Math.min.
// Still rejected: a spread into a fixed-arity (rest-less) user function and a
// spread filling a fixed parameter slot (both need a static-arity split).

func TestE2ESpreadIntoRest(t *testing.T) {
	assertOutput(t, `
function sum(...nums: number[]): number {
  let t = 0
  for (const n of nums) t += n
  return t
}
const arr = [1, 2, 3, 4, 5]
console.log(sum(...arr))
console.log(sum(10, 20))
`, "15\n30")
}

func TestE2ESpreadAfterFixedArgs(t *testing.T) {
	assertOutput(t, `
function label(prefix: string, ...items: number[]): number {
  return prefix.length + items.length
}
const a = [1, 2, 3]
console.log(label("hi", ...a))
`, "5")
}

func TestE2ESpreadStringArray(t *testing.T) {
	assertOutput(t, `
function join(...parts: string[]): string {
  let s = ""
  for (const p of parts) s += p
  return s
}
console.log(join(...["a", "b", "c"]))
`, "abc")
}

func TestE2ESpreadFixedArityRejected(t *testing.T) {
	_, err := parseAndCompile(`
function f(a: number, b: number): number { return a + b }
const arr = [1, 2]
f(...arr)
`)
	if err == nil {
		t.Fatal("expected a compile error spreading into a fixed-arity function, got none")
	}
}

// V2: multiple spreads concatenate into one rest buffer at runtime.
func TestE2ESpreadMultiple(t *testing.T) {
	assertOutput(t, `
function sum(...nums: number[]): number {
  let t = 0
  for (const n of nums) t += n
  return t
}
const a = [1, 2, 3]
const b = [4, 5]
console.log(sum(...a, ...b))
console.log(sum(...a, ...a, ...b))
`, "15\n21")
}

// V2: a spread freely mixed with positional args, in any rest-region position,
// preserving left-to-right order.
func TestE2ESpreadMixedPositional(t *testing.T) {
	assertOutput(t, `
function first(...nums: number[]): number { return nums.length > 0 ? nums[0] : -1 }
function sum(...nums: number[]): number {
  let t = 0
  for (const n of nums) t += n
  return t
}
const a = [10, 20]
const b = [30]
console.log(sum(...a, 100, ...b))
console.log(sum(1, ...a, ...b))
console.log(first(99, ...a))
`, "160\n61\n99")
}

// V2: multiple/mixed spreads through a closure's rest slot.
func TestE2ESpreadMultipleClosure(t *testing.T) {
	assertOutput(t, `
const sum = (...nums: number[]): number => {
  let t = 0
  for (const n of nums) t += n
  return t
}
const a = [1, 2]
const b = [3, 4]
console.log(sum(...a, ...b))
console.log(sum(...a, 10, ...b))
`, "10\n20")
}

// V2: spread into instance- and static-method rest parameters.
func TestE2ESpreadIntoMethods(t *testing.T) {
	assertOutput(t, `
class Acc {
  total(base: number, ...nums: number[]): number {
    let t = base
    for (const n of nums) t += n
    return t
  }
  static sum(...nums: number[]): number {
    let t = 0
    for (const n of nums) t += n
    return t
  }
}
const acc = new Acc()
const a = [1, 2]
const b = [3, 4]
console.log(acc.total(100, ...a, ...b))
console.log(Acc.sum(...a, 5, ...b))
`, "110\n15")
}

// A spread filling a fixed parameter slot (not the rest slot) stays rejected —
// it would need a runtime split against static arity.
func TestE2ESpreadIntoFixedSlotRejected(t *testing.T) {
	_, err := parseAndCompile(`
function f(a: number, b: number, ...rest: number[]): number { return a + b }
const arr = [1, 2, 3]
f(...arr)
`)
	if err == nil {
		t.Fatal("expected a compile error for a spread filling a fixed parameter slot, got none")
	}
}

// V2: spread into the variadic builtins Math.max / Math.min folds the array at
// runtime, with any argument count (including a lone spread) and free mixing
// with positional args.
func TestE2ESpreadMathMinMax(t *testing.T) {
	assertOutput(t, `
const a = [3, 7, 1, 9, 4]
console.log(Math.max(...a))
console.log(Math.min(...a))
console.log(Math.max(10, ...a, 20))
console.log(Math.min(0, ...a))
const f = [1.5, 2.5, 0.5]
console.log(Math.max(...f))
`, "9\n1\n20\n0\n2.5")
}

// V2: spread into console.log (and stderr variants) expands to space-separated
// tokens with a single trailing newline, for any positional/spread mix.
func TestE2ESpreadConsoleLog(t *testing.T) {
	assertOutput(t, `
const a = [1, 2, 3]
console.log(...a)
console.log("nums:", ...a, "end")
const s = ["foo", "bar"]
console.log(...s)
const empty: number[] = []
console.log("only", ...empty)
`, "1 2 3\nnums: 1 2 3 end\nfoo bar\nonly")
}

// Arrow functions can now have a rest parameter and receive a spread (the
// detection + closure-call spread forwarding fixes).
func TestE2EArrowRestParamAndSpread(t *testing.T) {
	assertOutput(t, `
const sum = (...nums: number[]): number => {
  let t = 0
  for (const n of nums) t += n
  return t
}
console.log(sum(1, 2, 3))
const arr = [4, 5, 6]
console.log(sum(...arr))
const withFixed = (label: string, ...nums: number[]): number => label.length + nums.length
console.log(withFixed("hi", ...arr))
`, "6\n15\n5")
}

func TestE2ESpreadArrayExpression(t *testing.T) {
	// A spread accepts any array-valued expression, not just a variable
	// (ADR-00845): a Map iterator, a slice/map result, and array literals.
	assertOutput(t, `
const m = new Map<string, number>()
m.set("a", 1); m.set("b", 2)
console.log([...m.keys()].join(","))
console.log([...m.values()].join(","))
console.log([...[1, 2], ...[3, 4]].join(","))
const arr = [1, 2, 3]
console.log([0, ...arr.slice(1), 9].join(","))
console.log([...arr.map(x => x * 2)].join(","))
`, "a,b\n1,2\n1,2,3,4\n0,2,3,9\n2,4,6")
}

func TestE2ESpreadStringIntoArray(t *testing.T) {
	// Spreading a string yields its characters as single-character strings
	// (ADR-00849), usable alongside static elements and other spreads.
	assertOutput(t, `
console.log([..."abc"].join("-"))
const s = "hi"
console.log([...s].join("."))
console.log([..."ab", ..."cd"].join(""))
console.log(["x", ..."ab", "y"].join(","))
console.log([..."abc"].map(c => c.toUpperCase()).join(""))
console.log([..."abc"].length)
`, "a-b-c\nh.i\nabcd\nx,a,b,y\nABC\n3")
}

// Spreading an object copies only its own properties: an absent optional
// field leaves the value an earlier property gave.
func TestE2EObjectSpreadSkipsAbsentOptional(t *testing.T) {
	assertSameAsNodeImports(t, `
interface S { exec?: string; args?: string[]; silent?: boolean; n?: number }
let base: S = { silent: true }
function setup(options?: S): S {
  return { exec: 'main', args: ['a'], silent: false, n: 1, ...base, ...options }
}
const r = setup()
console.log(r.exec, r.args, r.silent, r.n)
const q = setup({ n: 2 })
console.log(q.exec, q.silent, q.n)
`)
}

// Spreading a primitive into an object literal is tsc's TS2698 (its type
// is any); as JavaScript, a string spreads its indices and every other
// primitive nothing.
func TestE2EObjectSpreadPrimitive(t *testing.T) {
	mustCompileError(t, `
declare var n: number;
export const a = { ...n };
`, "spread types may only be created from object types")
	mustCompileError(t, `
export const c = { ...null, k: 1 };
`, "spread types may only be created from object types")
	assertSameAsNode(t, `
const o: { k: number } | undefined = Math.random() < 2 ? { k: 1 } : undefined;
const f = (b: false | { j: number }) => ({ ...o, ...b });
console.log(JSON.stringify(f({ j: 2 })), JSON.stringify(f(false)));
const g = (b: 0 | "" | { j: number; k?: string }) => { const r = { z: true, ...b }; return r; };
console.log(JSON.stringify(g({ j: 3, k: "x" })), JSON.stringify(g(0)), JSON.stringify(g("").j));
const h = (b: { j: number } | undefined) => ({ x: 1, ...b });
console.log(JSON.stringify(h(undefined)), "j" in h(undefined), Object.keys(h({ j: 1 })));
`)
	assertSameAsNodeCompatJS(t, `
var n = 1, s = "ab";
var a = { ...n, x: 1 };
var b = { ...s, y: 2 };
var c = { ...null, ...undefined, k: 3 };
console.log(JSON.stringify(a), JSON.stringify(b), JSON.stringify(c), JSON.stringify({ ...true }));
var f = function (arg) { var yield = arg; return { ...yield }; };
console.log(JSON.stringify(f({ q: 1 })), JSON.stringify(f(5)));
var gen = async function* g() { yield 1; };
var iter = gen();
var iter = gen();
var item = iter.next();
item.then(r => console.log(r.value, r.done));
`)
}
