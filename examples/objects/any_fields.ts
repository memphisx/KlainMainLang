// `any`/`unknown`-typed object and class fields (TDD-00208). Such a field is a
// boxed slot (box-on-write / unbox-on-read) — the field-shaped counterpart of an
// `any[]` array — so it can hold a value of any kind over its lifetime.

// A class field typed `any` holds each kind in turn.
class Box {
  v: any = null
}
const b = new Box()
b.v = 42
console.log(b.v) // 42
b.v = 'hello'
console.log(b.v) // hello
b.v = true
console.log(typeof b.v) // boolean

// An object-literal `any` field, including through JSON.
const o: { x: any } = { x: 5 }
o.x = 'str'
console.log(o.x) // str
console.log(JSON.stringify(o)) // {"x":"str"}

// Evolving-any: an unannotated `= null` field reassigned in a method is widened
// to a boxed `any` field, so `??=` works.
class Counter {
  #n = null
  bump() {
    return (this.#n ??= 1)
  }
}
const c = new Counter()
console.log(c.bump()) // 1

// Destructuring and spread over an `any` field.
const { v } = b
console.log(v) // true
const o2 = { ...o }
console.log(o2.x) // str
