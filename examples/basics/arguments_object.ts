// The `arguments` object reflects the values a function was *actually* called
// with — the declared parameters followed by any extra arguments (TDD-00210).
// It behaves like an array: `.length`, indexing, and `for...of` all work. Each
// element is `any` (arguments are heterogeneous), so a strict-typed use casts
// with `as T`; `-compat=js` allows operating on the boxed value directly.

// Indexed access + .length, including arguments beyond the declared arity.
function sum(first: number): number {
  let total = 0
  for (let i = 0; i < arguments.length; i++) {
    total += arguments[i] as number
  }
  return total
}
console.log(sum(1, 2, 3)) // 6
console.log(sum(1, 2, 3, 4, 5)) // 15 — the two extra arguments are reflected

// for...of over the arguments.
function biggest(a: number, b: number, c: number, d: number): number {
  let max: number = arguments[0] as number
  for (const x of arguments) {
    const n: number = x as number
    if (n > max) max = n
  }
  return max
}
console.log(biggest(3, 9, 2, 7)) // 9

// Heterogeneous arguments: each slot keeps its own value.
function label(name: string, count: number): string {
  return (arguments[0] as string) + "=" + (arguments[1] as number)
}
console.log(label("items", 42)) // items=42
