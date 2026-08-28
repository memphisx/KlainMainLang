// A function *type* annotation may carry a rest parameter — both a rest-only
// list `(...xs: T[]) => R` and a leading-positional-then-rest list
// `(head: T, ...tail: U[]) => R`. The annotated slot accepts any matching
// closure, called variadically or via spread.

// Rest-only function type as a parameter type.
function apply(f: (...xs: number[]) => number, args: number[]): number {
  return f(...args)
}
console.log(apply((...xs: number[]): number => xs.length, [10, 20, 30])) // 3

// Leading positional, then rest, in the function type.
function withHead(f: (head: number, ...tail: number[]) => number): number {
  return f(1, 2, 3, 4)
}
console.log(withHead((head: number, ...tail: number[]): number => head + tail.length)) // 4

// The same shape as a named type alias, then bound to a matching arrow.
type Variadic = (...xs: number[]) => number
const count: Variadic = (...xs: number[]): number => xs.length
console.log(count(1, 2, 3, 4, 5)) // 5
