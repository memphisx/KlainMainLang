// Typing an otherwise-untyped function from its JSDoc — the "typed JS"
// workflow. `@param {T} name` and `@returns {T}` fill in a parameter/return
// that has no inline `: T` annotation (TDD-00125). The type grammar is the
// same one `@type` accepts, including this compiler's integer width keywords.

/**
 * Integer division: both operands are int32, so `/` is integer division.
 * @param {int32} a
 * @param {int32} b
 * @returns {int32}
 */
function idiv(a, b) {
  return a / b
}

console.log(idiv(7, 2)) // 3  (int32 / int32 -> integer division)

/**
 * @param {string} s
 * @returns {number} the code unit count
 */
function width(s) {
  return s.length
}

console.log(width("hello")) // 5

// An inline annotation always wins over a conflicting @param.
/** @param {int32} x */
function asNumber(x: number) {
  return x / 2
}

console.log(asNumber(7)) // 3.5  (x is a number, not int32)

// A `...`/`=` decoration on the type is recognized and stripped to its base
// type; here the value flows into a declared rest parameter.
/**
 * @param {...number} nums
 */
function total(nums: number[]) {
  let t = 0
  for (const n of nums) t += n
  return t
}

console.log(total([1, 2, 3, 4])) // 10

// `@typedef {Object}` + `@property` declares a named object type; `@callback`
// declares a named function type. Both are usable wherever a type name is.
/**
 * @typedef {Object} Point
 * @property {number} x
 * @property {number} y
 */

/** @param {Point} p */
function manhattan(p) {
  return p.x + p.y
}

console.log(manhattan({ x: 3, y: 4 })) // 7

/**
 * @callback Combine
 * @param {number} a
 * @param {number} b
 * @returns {number}
 */

/** @param {Combine} fn */
function run(fn) {
  return fn(10, 5)
}

console.log(run((a, b) => a * b)) // 50

// `@template T` is the JSDoc form of a `<T>` generic — the function is
// monomorphized per concrete type, exactly like a TypeScript generic.
/**
 * @template T
 * @param {T[]} arr
 * @returns {T}
 */
function firstOf(arr) {
  return arr[0]
}

console.log(firstOf([10, 20, 30])) // 10
console.log(firstOf(["a", "b"]))   // a

// JSDoc type expressions are parsed by the real type parser: unions, inline
// object shapes, `Array.<T>`, and `function(...)` types all resolve.
/** @param {number | string} v */
function kind(v) {
  return typeof v
}

console.log(kind(1))   // number
console.log(kind("s")) // string

/** @param {{name: string, age: number}} u */
function greet(u) {
  return u.name
}

console.log(greet({ name: "Thessaloniki", age: 2300 })) // Thessaloniki

/** @param {function(number): number} f */
function twice(f) {
  return f(f(3))
}

console.log(twice((n) => n + 1)) // 5


