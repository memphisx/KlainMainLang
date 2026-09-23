// Optional (`param?: T`) parameters. An omitted argument is a real `undefined`,
// so inside the body the parameter reads as `T | undefined` — exactly as
// TypeScript types it (TDD-00187). Use it by narrowing (`if (x !== undefined)`),
// supplying a fallback with `??`, or asserting presence with `!`.

function greetNumber(x?: number): number {
  return x ?? 0        // supply a fallback for the omitted case
}
console.log(greetNumber())   // 0
console.log(greetNumber(5))  // 5

// An omitted argument is distinguishable from a real 0 — the parameter carries
// a presence bit, not just a zeroed value.
function seen(x?: number): boolean {
  return x !== undefined
}
console.log(seen())    // false
console.log(seen(0))   // true  (a real 0 is present, not "missing")

// String concatenation with an absent value renders "undefined" (as in Node),
// so a string-typed optional parameter reads naturally when concatenated.
function greet(name?: string): string {
  return 'Hi, ' + name
}
console.log(greet())       // Hi, undefined
console.log(greet('Bob'))  // Hi, Bob

// Required and optional parameters can mix; optional ones must trail. Narrow
// each optional operand before arithmetic.
function box(w: number, h?: number, d?: number): number {
  return w * (h ?? 1) * (d ?? 1)
}
console.log(box(2))        // 2  (h and d default to 1)
console.log(box(2, 3))     // 6  (d still defaults to 1)
console.log(box(2, 3, 4))  // 24

// An array-typed optional parameter's omitted value is `undefined`, as in
// JavaScript: reading `.length` off it throws a TypeError, so narrow (or default)
// before use.
function total(nums?: number[]): number {
  let sum = 0
  const xs = nums ?? []
  for (let i = 0; i < xs.length; i++) { sum += xs[i] }
  return sum
}
console.log(total())            // 0
console.log(total([1, 2, 3]))   // 6
function unguarded(nums?: number[]): number { return nums.length }
try { unguarded() } catch (e) { console.log((e as Error).message) } // Cannot read properties of undefined (reading 'length')

// Works identically on instance and static class methods.
class Greeter {
  greet(name?: string): string { return 'Hi, ' + name }
  static shout(name?: string): string { return 'HI, ' + name }
}
const g = new Greeter()
console.log(g.greet())            // Hi, undefined
console.log(g.greet('Alice'))     // Hi, Alice
console.log(Greeter.shout())      // HI, undefined
console.log(Greeter.shout('Al'))  // HI, Al

// A default parameter value is always present (it is not optional-absent) and
// may reference an earlier parameter (ADR-00598), including chaining.
function rect(w: number, h: number = w): number { return w * h }  // h defaults to w (a square)
console.log(rect(4))        // 16
console.log(rect(4, 2))     // 8
function label(name: string, title: string = "Mr. " + name): string { return title }
console.log(label("Smith")) // Mr. Smith
