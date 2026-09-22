// Scalar unions as values: a ternary whose branches differ in kind, and arrays
// whose elements are a union or may be absent.

function label(n: number) { return n % 2 === 0 ? n : "odd" }
console.log(label(2), label(3), typeof label(3))

const mixed: (number | string)[] = [1, "two", 3]
mixed.push("four")
for (const x of mixed) {
  if (typeof x === "string") console.log(x.toUpperCase())
  else console.log(x * 10)
}
console.log(mixed, JSON.stringify(mixed))

// Absent elements are real `undefined`s; `??` unwraps to a plain number.
const readings: (number | undefined)[] = [20.5, undefined, 22]
let sum = 0
for (const r of readings) sum += r ?? 0
console.log(sum, readings[1] === undefined, readings)

// A callback that may miss produces a `(number | undefined)[]`.
const stock = new Map<string, number>([["apple", 3]])
const counts = ["apple", "pear"].map(k => stock.get(k))
console.log(counts, counts.map(c => c ?? 0).join(","))
