// Destructuring extracts values from arrays or fields from objects into
// named local variables in a single declaration.
// The RHS can be a variable, a function call, or a literal.

// ─── Array destructuring from a variable ───────────────────────────────────

const coords = [10, 20, 30, 40, 50]

const [x, y, z] = coords
console.log(x)   // 10
console.log(y)   // 20
console.log(z)   // 30

// Holes skip elements at that index.
const [, second, , fourth] = coords
console.log(second)  // 20
console.log(fourth)  // 40

// Swap via a temporary array.
let a: number = 1
let b: number = 2
const tmp = [b, a]
const [newA, newB] = tmp
console.log(newA)  // 2
console.log(newB)  // 1

// ─── Array destructuring from a literal ────────────────────────────────────

const [lo, , hi] = [0, 50, 100]
console.log(lo)  // 0
console.log(hi)  // 100

// ─── Array destructuring from a function call ───────────────────────────────

function range(start: number, n: number): number[] {
    let r = new Array<number>(n)
    for (let i = 0; i < n; i++) {
        r[i] = start + i
    }
    return r
}

const [first, second2, third] = range(5, 4)
console.log(first)    // 5
console.log(second2)  // 6
console.log(third)    // 7

// ─── Object destructuring from a variable ───────────────────────────────────

let pt: { x: number; y: number } = { x: 3, y: 7 }

// Rename on extract.
const { x: px, y: py } = pt
console.log(px)  // 3
console.log(py)  // 7

// Shorthand: local name matches field name.
let size: { width: number; height: number } = { width: 800, height: 600 }
const { width, height } = size
console.log(width)   // 800
console.log(height)  // 600

// String fields.
let person: { label: string; age: number } = { label: 'Alice', age: 30 }
const { label, age } = person
console.log(label)  // Alice
console.log(age)    // 30

// ─── Object destructuring from a literal ────────────────────────────────────

const { x: ox, y: oy } = { x: 9, y: 4 }
console.log(ox)  // 9
console.log(oy)  // 4

// ─── Object destructuring from a function call ───────────────────────────────

function makeRect(w: number, h: number): { width: number; height: number } {
    let r: { width: number; height: number } = { width: w, height: h }
    return r
}

const { width: rw, height: rh } = makeRect(1920, 1080)
console.log(rw)  // 1920
console.log(rh)  // 1080

// ─── Destructuring inside functions ─────────────────────────────────────────

function sumCoords(obj: { x: number; y: number }): number {
    const { x, y } = obj
    return x + y
}
function makePoint(px: number, py: number): { x: number; y: number } {
    let p: { x: number; y: number } = { x: px, y: py }
    return p
}
console.log(sumCoords(pt))               // 10
console.log(sumCoords(makePoint(4, 6)))  // 10

function dot(p: { x: number; y: number }, q: { x: number; y: number }): number {
    const { x: ax, y: ay } = p
    const { x: bx, y: by } = q
    return ax * bx + ay * by
}

let p1: { x: number; y: number } = { x: 1, y: 2 }
let p2: { x: number; y: number } = { x: 3, y: 4 }
console.log(dot(p1, p2))  // 11

function firstPlusLast(arr: number[]): number {
    const [head] = arr
    let tail: number = arr[arr.length - 1]
    return head + tail
}
console.log(firstPlusLast(coords))           // 60  (10 + 50)
console.log(firstPlusLast(range(1, 5)))      // 1 + 5 = 6

// ─── Destructured function parameters ───────────────────────────────────────
// Unlike a plain scalar parameter, a destructured one always needs an
// explicit type annotation — there's no sensible unannotated default for a
// pattern the way `number` is for a bare name.

interface Point { x: number; y: number }

function addPoints({ x, y }: Point, other: Point): number {
    return x + y + other.x + other.y
}
console.log(addPoints({ x: 1, y: 2 }, { x: 10, y: 20 }))  // 33

// Renaming works in parameter position too.
function describe({ x: px, y: py }: Point): string {
    return px + "," + py
}
console.log(describe({ x: 5, y: 6 }))  // 5,6

function sumFirstTwo([a, b]: number[]): number {
    return a + b
}
console.log(sumFirstTwo(coords))  // 30 (10 + 20)

// Holes work in a parameter pattern the same as in a destructuring statement.
function skipFirst([, b, c]: number[]): number {
    return b + c
}
console.log(skipFirst([100, 200, 300]))  // 500

// Arrow functions support object-destructured parameters too (array-
// destructured parameters don't, yet — array-typed closure parameters
// aren't supported at all independent of destructuring).
const area = ({ x, y }: Point): number => x * y
console.log(area({ x: 5, y: 6 }))  // 30

// Class methods and constructors support destructured parameters as well.
class Vec {
    sum: number
    constructor({ x, y }: Point) {
        this.sum = x + y
    }
    static add({ x, y }: Point, other: Point): number {
        return x + y + other.x + other.y
    }
}
const v = new Vec({ x: 3, y: 4 })
console.log(v.sum)                                  // 7
console.log(Vec.add({ x: 1, y: 1 }, { x: 2, y: 2 })) // 6

// ─── Destructuring default values (`[a = expr]`, `{ a = expr }`) ───────────
// An array pattern's default fires exactly when that position is past the
// source array's actual length — a shorter array is ordinary, valid JS, not
// an error.

const [dp1 = 100, dp2 = 200, dp3 = 300] = [1, 2]
console.log(dp1)  // 1   (present, default unused)
console.log(dp2)  // 2   (present, default unused)
console.log(dp3)  // 300 (absent, default used)

// A later default may reference an earlier binding in the same pattern.
const [base = 5, offset = base] = [10]
console.log(base)    // 10 (present)
console.log(offset)  // 10 (absent, defaults to the just-bound `base`)

// An object pattern's default only works on a nullable *reference* field
// (`T | null` where T is string/array/object/class) — the one field shape
// this compiler can reliably tell "not provided" apart from a real value
// for. A nullable *scalar* field (`number | null`) represents its null as
// an in-band 0/false sentinel indistinguishable from a legitimate zero, so
// a default there — and on any non-nullable field — is a compile-time
// rejection instead of a silent wrong answer.

interface Settings { label: string | null }
const withLabel: Settings = { label: "custom" }
const withoutLabel: Settings = { label: null }

const { label: l1 = "default" } = withLabel
const { label: l2 = "default" } = withoutLabel
console.log(l1)  // custom
console.log(l2)  // default

// Destructured function parameters support defaults too, on both kinds of
// pattern, the same way.
function firstTwoOrDefaults([a = -1, b = -1]: number[]): string {
    return a + "," + b
}
console.log(firstTwoOrDefaults([7]))     // 7,-1
console.log(firstTwoOrDefaults([7, 8]))  // 7,8

// ─── Destructuring assignment (`[a, b] = expr`, `({ a, b } = expr)`) ───────
// Assigns into already-declared variables, rather than declaring new ones.
// V1 scope: every target must be a plain variable — no nested patterns, no
// rest, no per-element default (the declaration form's own richer defaults
// don't extend to assignment yet).

let assignA: number = 0
let assignB: number = 0
;[assignA, assignB] = [1, 2]
console.log(assignA)  // 1
console.log(assignB)  // 2

// The classic swap idiom, without a temporary variable this time.
;[assignA, assignB] = [assignB, assignA]
console.log(assignA)  // 2
console.log(assignB)  // 1

// Object form needs parens at statement level — `{` would otherwise start
// a block, same restriction real JS has.
interface Coord { cx: number; cy: number }
let targetX: number = 0
let targetY: number = 0
const coord: Coord = { cx: 5, cy: 6 }
;({ cx: targetX, cy: targetY } = coord)
console.log(targetX)  // 5
console.log(targetY)  // 6

// A source array shorter than the pattern reads zero for the missing
// positions — same as the declaration form's own out-of-bounds behavior,
// not a real JS `undefined`.
;[assignA, assignB] = [9]
console.log(assignA)  // 9
console.log(assignB)  // 0

// ─── Array rest destructuring (`[a, ...rest]`) ──────────────────────────────
// Collects every remaining position into a real, independent new array —
// works in a declaration, a destructured function parameter, and a
// destructuring assignment alike.

const [restFirst, ...restTail] = [1, 2, 3]
console.log(restFirst)      // 1
console.log(restTail.length) // 2
console.log(restTail[0])    // 2
console.log(restTail[1])    // 3

// Shorter than the pattern: rest is just empty, not an error.
const [rf2, rf3, ...restEmpty] = [1, 2]
console.log(rf2)               // 1
console.log(rf3)               // 2
console.log(restEmpty.length)  // 0

function firstAndRestCount([head, ...tail]: number[]): number {
    return head + tail.length
}
console.log(firstAndRestCount([10, 20, 30, 40]))  // 13

// Destructuring assignment form: the rest target must already be
// declared as an array.
let swapFirst: number = 0
let swapRest: number[] = []
;[swapFirst, ...swapRest] = [5, 6, 7]
console.log(swapFirst)       // 5
console.log(swapRest.length) // 2

// A rest array is a real copy, not a view into the source — mutating the
// source afterward doesn't affect it.
const restSource: number[] = [1, 2, 3]
const [, ...restCopy] = restSource
restSource.push(99)
console.log(restCopy.length)  // 2
