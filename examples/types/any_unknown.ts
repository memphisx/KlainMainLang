// any / unknown — Staged V1: declare/assign/reassign across types, printing,
// typeof (a genuine runtime check for these, unlike every other type), and
// ===/!== equality. Arithmetic, function parameters/returns, and array/object
// element types are not yet supported for any/unknown and give a clean
// compile-time error instead of broken output — see STATUS.md.

// --- reassign across underlying types ---
let x: any = 5
console.log(x)              // 5
x = "hello"
console.log(x)              // hello
x = true
console.log(x)              // true
x = null
console.log(x)              // null

// --- template literals pick up the current runtime value ---
let msg: any = 42
console.log(`value: ${msg}`)  // value: 42
msg = "world"
console.log(`value: ${msg}`)  // value: world

// --- typeof is a real runtime check, not a compile-time constant ---
let dyn: any = 5
console.log(typeof dyn)     // number
dyn = "hi"
console.log(typeof dyn)     // string
dyn = true
console.log(typeof dyn)     // boolean
dyn = null
console.log(typeof dyn)     // object (the well-known JS quirk)

let uninitialized: any
console.log(typeof uninitialized)  // undefined

// --- unknown behaves the same as any at runtime ---
let u: unknown = 3.14
console.log(u)               // 3.14
console.log(typeof u)        // number

// --- equality ---
let a: any = 5
let b: any = 5
console.log(a === b)         // 1
let c: any = "5"
console.log(a === c)         // 0 (different underlying types, unlike loose JS ==)
console.log(a === 5)         // 1 (comparing against a plain literal also works)

let f: any = 5.0
console.log(a === f)         // 1 (5 === 5.0 numerically, matching JS number semantics)
