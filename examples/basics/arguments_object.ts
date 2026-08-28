// The `arguments` object is synthesized from a function's declared parameters
// when they all share one type (ADR-00387). It behaves like an array —
// `.length`, indexing, and `for...of` all work. For mixed argument types, or a
// genuinely variable count, use a `...rest` parameter instead.

// Indexed access + .length.
function sum(a: number, b: number, c: number): number {
  let total = 0
  for (let i = 0; i < arguments.length; i++) {
    total += arguments[i]
  }
  return total
}
console.log(sum(1, 2, 3)) // 6

// for...of over the arguments.
function biggest(a: number, b: number, c: number, d: number): number {
  let max = arguments[0]
  for (const x of arguments) {
    if (x > max) max = x
  }
  return max
}
console.log(biggest(3, 9, 2, 7)) // 9

// Works for string parameters too.
function shout(a: string, b: string): string {
  let out = ""
  for (let i = 0; i < arguments.length; i++) {
    out += arguments[i].toUpperCase()
  }
  return out
}
console.log(shout("hello ", "world")) // HELLO WORLD
