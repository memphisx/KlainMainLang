// An array stored in an object or class field is a reference, just like a local
// array: reading the field and binding it elsewhere shares the SAME array, so a
// mutation through the field is visible through the alias, and vice-versa
// (TDD-00213 Stage 2). Previously a field read snapshotted its contents.

class Basket {
  items: number[] = []
}

const b = new Basket()
b.items.push(1)
b.items.push(2)

const alias = b.items // same array as b.items
b.items.push(3)
console.log(alias) // [ 1, 2, 3 ] — the push through the field is visible
console.log(alias === b.items) // true

alias.push(4)
console.log(b.items) // [ 1, 2, 3, 4 ] — and a push through the alias is visible

// Object-literal fields behave the same.
const o = { xs: [10, 20] }
const xs = o.xs
o.xs.push(30)
console.log(xs) // [ 10, 20, 30 ]

// structuredClone makes a genuinely independent copy (a fresh array).
const snapshot = structuredClone(o)
o.xs.push(40)
console.log(snapshot.xs) // [ 10, 20, 30 ] — the clone is unaffected
