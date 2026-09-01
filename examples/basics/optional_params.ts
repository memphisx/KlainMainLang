// Optional (`param?: T`) parameters. An omitted argument gets the
// parameter's own type's zero value (0 for numbers, "" printed as the
// string "null" for strings — this compiler's stand-in for real JS's
// `undefined`, since a concrete type like `number` or `string` has no
// general sentinel for "not provided" — the same convention default
// parameter values and destructuring defaults already use).

function greetNumber(x?: number): number {
  return x
}
console.log(greetNumber())   // 0
console.log(greetNumber(5))  // 5

// A string parameter's zero value prints as "null" (matching how
// console.log(null) already prints), so string-typed optional parameters
// read most naturally when concatenated rather than returned bare.
function greet(name?: string): string {
  return 'Hi, ' + name
}
console.log(greet())       // Hi, null
console.log(greet('Bob'))  // Hi, Bob

// Required and optional parameters can mix; optional ones must trail.
function box(w: number, h?: number, d?: number): number {
  return w * h * d
}
console.log(box(2))        // 0 (h and d both default to 0)
console.log(box(2, 3))     // 0 (d still defaults to 0)
console.log(box(2, 3, 4))  // 24

// An array-typed optional parameter's zero value is an empty array.
function total(nums?: number[]): number {
  let sum = 0
  for (let i = 0; i < nums.length; i++) { sum += nums[i] }
  return sum
}
console.log(total())            // 0
console.log(total([1, 2, 3]))   // 6

// Works identically on instance and static class methods.
class Greeter {
  greet(name?: string): string { return 'Hi, ' + name }
  static shout(name?: string): string { return 'HI, ' + name }
}
const g = new Greeter()
console.log(g.greet())            // Hi, null
console.log(g.greet('Alice'))     // Hi, Alice
console.log(Greeter.shout())      // HI, null
console.log(Greeter.shout('Al'))  // HI, Al

// A default parameter value may reference an earlier parameter (ADR-00598) —
// including chaining, where a later default builds on an earlier one.
function rect(w: number, h: number = w): number { return w * h }  // h defaults to w (a square)
console.log(rect(4))        // 16
console.log(rect(4, 2))     // 8
function label(name: string, title: string = "Mr. " + name): string { return title }
console.log(label("Smith")) // Mr. Smith
