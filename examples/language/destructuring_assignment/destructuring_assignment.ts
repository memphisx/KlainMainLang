// Destructuring *assignment* (`[a, b] = e`, `({ x } = e)`) — reassigning
// already-declared variables, distinct from a `const`/`let` destructuring
// declaration. Now at declaration-form parity for nesting (ADR-00595).

// ── plain targets + the swap idiom ──────────────────────────────────────────
let a = 0, b = 0;
[a, b] = [1, 2];
console.log(a, b);            // 1 2
[a, b] = [b, a];             // swap, no temp
console.log(a, b);            // 2 1

// ── a trailing ...rest collects the remainder (an independent copy) ──────────
let head = 0, second = 0;
let tail: number[] = [];
[head, second, ...tail] = [10, 20, 30, 40];
console.log(head, second, tail[0], tail[1]);  // 10 20 30 40

// ── a per-element default fires when the source is too short ─────────────────
let x = 0, y = 0;
[x = 5, y = 6] = [99];        // x in-bounds → 99, y out-of-bounds → default 6
console.log(x, y);           // 99 6

// ── nested array patterns ───────────────────────────────────────────────────
let p = 0, q = 0, r = 0, s = 0;
const grid: number[][] = [[1, 2], [3, 4]];
[[p, q], [r, s]] = grid;
console.log(p, q, r, s);     // 1 2 3 4

// ── nested object patterns, and an array pattern inside an object one ────────
let m = 0, n = 0, o = 0;
const obj = { m: 10, inner: { n: 20, o: 30 } };
({ m, inner: { n, o } } = obj);
console.log(m, n, o);        // 10 20 30

let u = 0, v = 0, w = 0;
const mixed = { pair: [7, 8] as number[], k: 9 };
({ pair: [u, v], k: w } = mixed);
console.log(u, v, w);        // 7 8 9

// ── an object-property default fires when a nullable field is null ───────────
let label = "";
const cfg: { label: string | null } = { label: null };
({ label = "untitled" } = cfg);
console.log(label);          // untitled
