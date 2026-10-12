// --- Computed property keys ---
// `{ [expr]: value }` — the key is computed at runtime, not known at
// compile time. The literal is an object whose keys exist at run time, as
// in JavaScript: keys can be added, read by name or by expression, and
// enumerated.

const k = 'b'
const obj = { a: 1, [k]: 2 }
console.log(obj.a)      // 1
console.log(obj[k])     // 2
console.log(obj['a'])   // 1

// Static and computed keys can be mixed freely in the same literal.
const withStatic = { x: 1, [k]: 2, y: 3 }
console.log(withStatic.x)   // 1
console.log(withStatic.b)   // 2
console.log(withStatic.y)   // 3

// --- Writing through a computed key ---
obj.a = 10
obj[k] += 5
console.log(obj.a)   // 10
console.log(obj.b)   // 7

// Assigning a brand-new key at runtime adds it — a dynamic object isn't
// limited to the keys present in the original literal.
obj['c'] = 99
console.log(obj.c)      // 99
console.log(obj['c'])   // 99

// --- Iterating a dynamic object ---
for (const key of Object.keys(obj)) {
    console.log(key)   // a, b, c
}
for (const v of Object.values(obj)) {
    console.log(v)   // 10, 7, 99
}
for (const [k, v] of Object.entries(obj)) {
    console.log(k + '=' + v)   // a=10, b=7, c=99
}

// Membership is the `in` operator.
console.log('a' in obj)   // true
console.log('z' in obj)   // false

// --- String-valued dynamic object ---
const labelKey = 'subtitle'
const titles = { title: 'hello', [labelKey]: 'world' }
console.log(titles.title)      // hello
console.log(titles.subtitle)   // world
