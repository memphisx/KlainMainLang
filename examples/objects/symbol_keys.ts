// --- Symbol-keyed properties ---
// A Symbol is a valid, unique property key. `obj[sym]` uses ToPropertyKey
// (the symbol stays a symbol — it is NOT stringified, which would throw), so
// set / get / has / delete round-trip on the symbol's own identity. Symbol
// keys are deliberately invisible to the string enumeration (Object.keys,
// Object.getOwnPropertyNames, for...in) — they are a separate namespace.

const obj: any = {}
const s1 = Symbol('one')
const s2 = Symbol('two')

// Set and read back through the symbol key.
obj[s1] = 42
obj[s2] = 'hello'
console.log(obj[s1])   // 42
console.log(obj[s2])   // hello

// Two distinct symbols never collide, even with the same description.
const sameDesc = Symbol('one')
console.log(obj[sameDesc] === undefined)   // true

// Object.hasOwn sees the symbol key.
console.log(Object.hasOwn(obj, s1))          // true
console.log(Object.hasOwn(obj, s2))          // true

// String keys and symbol keys coexist on the same object.
obj.a = 1
obj.b = 2

// Symbol keys never leak into the string enumeration.
console.log(Object.keys(obj).join(','))                  // a,b
console.log(Object.getOwnPropertyNames(obj).join(','))   // a,b
const forIn: string[] = []
for (const k in obj) forIn.push(k)
console.log(forIn.join(','))                             // a,b

// Object.freeze applies to a symbol-keyed object too.
Object.freeze(obj)
console.log(Object.isFrozen(obj))   // true

// A well-known symbol set at run time: Symbol.toPrimitive drives conversion.
const money: any = { cents: 1250 }
money[Symbol.toPrimitive] = (hint: string) => hint === 'number' ? money.cents / 100 : '$' + (money.cents / 100).toFixed(2)
console.log(`${money}`, +money)          // $12.50 12.5
console.log(Object.keys(money).join(','))  // cents

// A function's own Symbol.toPrimitive, too.
function version() { return '1.2.3' }
(version as any)[Symbol.toPrimitive] = () => version()
console.log(`v${version}`)                 // v1.2.3

// Computed-key methods: a generator iterator, and a computed name.
const range = { *[Symbol.iterator]() { for (let i = 1; i <= 3; i++) yield i } }
for (const n of range) console.log(n)     // 1 2 3
const verb = 'greet'
const greeter: any = { [verb](name: string) { return 'hi ' + name } }
console.log(greeter.greet('Thessaloniki')) // hi Thessaloniki
