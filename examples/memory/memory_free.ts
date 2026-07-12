// Memory.free(x) — Stage 1 of the staged manual-memory-management plan
// (see STATUS.md's "Memory Management" section). A raw, unsafe, opt-in
// escape hatch: this compiler still never frees anything on its own — this
// just gives a program a way to explicitly release a value it knows it's
// done with, exactly as unsafe as C's own free().
//
// Shallow free only: frees the value's own top-level heap allocation(s)
// (and, for Map/Set/closures, their own internal backing buffers), never
// anything reachable *through* it. No double-free detection, no
// use-after-free protection beyond nulling out a named variable's own
// storage after freeing it (still undefined behavior to read that storage
// again if a stale copy of the pointer survives elsewhere).
//
// IMPORTANT, C-shaped footgun: a string LITERAL (e.g. "hello") is interned
// as a compile-time global constant, not malloc'd — freeing one crashes,
// exactly like C's own free("literal"). Only free strings you know were
// dynamically built at runtime (concatenation, .slice()/.substring(),
// template literals, fs.readFileSync, JSON.stringify, fetch response
// bodies, ...) — every one of those is always a real heap allocation.

// ── string (must be a dynamically-built one, not a literal) ─────────────────
let big: string = "hello " + "world"   // concatenation result -> malloc'd
console.log(big.length)   // 11
Memory.free(big)
console.log(big === null)  // 1 (true) — the variable's own storage was nulled

// ── array ────────────────────────────────────────────────────────────────────
let nums: number[] = [1, 2, 3, 4, 5]
console.log(nums.length)   // 5
Memory.free(nums)
console.log(nums.length)   // 0 — nulled out, not a stale read

// ── object ───────────────────────────────────────────────────────────────────
interface Point { x: number; y: number }
let p: Point = { x: 1, y: 2 }
console.log(p.x)   // 1
Memory.free(p)

// ── closure ──────────────────────────────────────────────────────────────────
// Frees the closure's own header + environment struct. A captured
// variable's own heap-promoted cell (shared with the enclosing scope,
// ADR-00001) is deliberately left untouched — freeing it here could free
// memory the enclosing scope, or another closure, is still actively using.
let counter: number = 0
const inc = (): number => { counter = counter + 1; return counter }
console.log(inc())   // 1
console.log(inc())   // 2
Memory.free(inc)
console.log(counter)   // 2 — the shared cell survived the closure's own free

// ── Map / Set ────────────────────────────────────────────────────────────────
// Frees the map's own two backing buffers (keys array, values array) plus
// its header struct — not each individual key/value pair.
let scores: Map<string, number> = new Map<string, number>()
scores.set("alice", 10)
scores.set("bob", 20)
console.log(scores.size)   // 2
Memory.free(scores)

let tags: Set<string> = new Set<string>()
tags.add("greek")
console.log(tags.size)   // 1
Memory.free(tags)

// ── a directly-evaluated expression works too, not just named variables ────
function makePoint(): Point {
    return { x: 3, y: 4 }
}
Memory.free(makePoint())

// ── freeing the same named variable twice is harmless ───────────────────────
// (free(NULL) is a well-defined no-op in C — the first call already nulled
// the variable's storage.) Freeing the same allocation through two
// DIFFERENT aliases is still unsafe — this only covers the one-variable case.
let scratch: number[] = [9, 9, 9]
Memory.free(scratch)
Memory.free(scratch)

console.log("done")
