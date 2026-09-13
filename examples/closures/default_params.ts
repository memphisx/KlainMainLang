// Default parameters through first-class function values (TDD-00206 Stage 1).
//
// A default parameter (`b: number = 10`) is filled when the argument is omitted
// at the call site — not only for a directly-called named function, but also
// when the function is a *value*: bound to a variable, stored in an object
// field, or an object shorthand method. Calling such a value with fewer
// arguments than parameters used to fail to compile ("not enough parameters");
// the omitted trailing parameters now take their defaults.

// --- Function expression bound to a const ---
const add = function (a: number, b: number = 10): number { return a + b }
console.log(add(5))       // 15  — b defaults to 10
console.log(add(5, 20))   // 25  — b provided

// --- Arrow bound to a const ---
const greet = (name: string, punct: string = "!"): string => name + punct
console.log(greet("hi"))         // hi!
console.log(greet("hi", "?"))    // hi?

// --- A default may reference an earlier parameter ---
const scaled = (x: number, y: number = x * 2): number => x + y
console.log(scaled(4))     // 12  — y defaults to 4 * 2
console.log(scaled(4, 1))  // 5

// --- Object shorthand method with chained defaults (y = x, z = y) ---
const box = {
    volume(x: number, y: number = x, z: number = y): number { return x * y * z },
}
console.log(box.volume(3))        // 27  — a cube
console.log(box.volume(2, 3))     // 18  — z defaults to y (3)
console.log(box.volume(2, 3, 4))  // 24

// --- Object field holding an arrow with a default ---
const formatter = {
    pad: (s: string, width: number = 5): string => {
        let out = s
        while (out.length < width) { out = out + " " }
        return out + "|"
    },
}
console.log(formatter.pad("ab"))      // "ab   |"
console.log(formatter.pad("ab", 3))   // "ab |"

// --- A default that references a variable captured from the enclosing scope ---
// Each closure returned by makeAdder captures its own `base`; an omitted second
// argument falls back to that closure's captured default, evaluated inside the
// closure body (not at the call site).
function makeAdder(base: number) {
    return (x: number, y: number = base): number => x + y
}
const add100 = makeAdder(100)
const add200 = makeAdder(200)
console.log(add100(5))     // 105 — y defaults to the captured base 100
console.log(add200(5))     // 205 — a different captured base
console.log(add100(5, 1))  // 6   — explicit argument wins

// --- A captured default on an async closure (TDD-00206 Stage 2 polish) ---
// The default is filled in the closure's async body prologue, after captures
// are bound — so each returned async closure keeps its own `base`.
function makeAsyncAdder(base: number) {
    return async (x: number, y: number = base): Promise<number> => x + y
}
const asyncAdd100 = makeAsyncAdder(100)
asyncAdd100(5).then((r) => console.log(r))     // 105
asyncAdd100(5, 1).then((r) => console.log(r))  // 6

// --- A captured default on an object or array parameter ---
// An object/array default fills the parameter's pointer / array-header slot in
// the body prologue (here the defaults are module globals).
const defaultCfg = { label: "default" }
const describe = (id: number, cfg: { label: string } = defaultCfg): string =>
    id + ":" + cfg.label
console.log(describe(1))                       // 1:default
console.log(describe(2, { label: "custom" }))  // 2:custom

const defaultItems: number[] = [1, 2, 3]
const count = (prefix: number, items: number[] = defaultItems): number =>
    prefix + items.length
console.log(count(10))          // 13
console.log(count(10, [1, 2]))  // 12

// --- .bind threads the presence mask (TDD-00206 Stage 2 polish) ---
// Binding the leading argument of a captured-default closure still lets an
// omitted trailing argument fall back to the captured default.
const bound = makeAdder(7).bind(null, 5)
console.log(bound())   // 12 — 5 + captured 7
console.log(bound(3))  // 8  — 5 + explicit 3
