// Nullable non-pointer scalars: `number | null`, `boolean | null`, etc.
//
// A scalar like `number` has no spare bit pattern to mean "absent", so a
// nullable scalar is stored with a presence flag. That makes a genuine null
// distinguishable from a legitimately-present 0 everywhere — `??`, `=== null`,
// printing, JSON — across locals, parameters, return values, fields, and Maps.

// --- Locals: 0 is not null ---
let present: number | null = 0
let absent: number | null = null
console.log(present ?? 42)      // 0   (a real 0, not the fallback)
console.log(absent ?? 42)      // 42
console.log(present === null)  // false
console.log(absent === null)   // true
console.log(present)           // 0
console.log(absent)            // null  (prints the real JS value)

// --- Flow narrowing: inside the guard, the value is known present ---
function describe(v: number | null): string {
    if (v === null) {
        return 'absent'
    }
    return 'present: ' + v          // v is a plain number here
}
console.log(describe(0))       // present: 0
console.log(describe(null))    // absent

// --- Return values distinguish null from 0 ---
function firstNonNegative(a: number, b: number): number | null {
    if (a >= 0) return a
    if (b >= 0) return b
    return null
}
console.log(firstNonNegative(0, 5) ?? -1)    // 0
console.log(firstNonNegative(-1, -2) ?? -1)  // -1

// --- Object/interface fields ---
interface Reading { label: string; value: number | null }
const r1: Reading = { label: 'temp', value: 0 }
const r2: Reading = { label: 'humidity', value: null }
console.log(r1.value ?? -1)              // 0
console.log(r2.value ?? -1)              // -1
console.log(JSON.stringify(r2))          // {"label":"humidity","value":null}

// --- Map.get: a missing key is null, a stored 0 is 0 ---
const scores = new Map<string, number>()
scores.set('alice', 0)
console.log(scores.get('alice') ?? -1)   // 0
console.log(scores.get('bob') ?? -1)     // -1
console.log(scores.get('bob') === null)  // true

// --- Class iterator: yielding 0 no longer ends iteration early ---
class CountTo3 {
    private i: number = 0
    next(): number | null {
        if (this.i > 2) return null
        const v = this.i
        this.i = this.i + 1
        return v
    }
}
for (const n of new CountTo3()) {
    console.log(n)             // 0, then 1, then 2
}
