// --- Computed property keys ---
// `{ [expr]: value }` — the key is computed at runtime, not known at
// compile time. Internally this is backed by the same Map<string,V> runtime
// as `new Map<K,V>()` (see docs/tdd/TDD-00012.md), just with `.field` /
// `[expr]` sugar instead of `.get()`/`.set()`.

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

// A dynamic object is also a real Map<string,V> under the hood, so its own
// .get()/.set()/.has() API works directly, no dot/bracket sugar needed.
console.log(obj.has('a'))   // 1
console.log(obj.has('z'))   // 0
console.log(obj.get('b'))   // 7

// --- String-valued dynamic object ---
const labelKey = 'subtitle'
const titles = { title: 'hello', [labelKey]: 'world' }
console.log(titles.title)      // hello
console.log(titles.subtitle)   // world
