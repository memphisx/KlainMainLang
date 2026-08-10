// A supporting module — imported only by side_effects.ts, never compiled
// on its own in a meaningful way (nothing prints when run standalone
// because nothing ever calls `ready()`, but the top-level console.log
// below still runs either way).
//
// Top-level side-effecting code in imported files (TDD-00052): this file's
// own top-level statements now really run — exactly once, before anything
// that imports it, in real dependency order — not just its declarations,
// the way non-entry files used to be restricted to.

console.log("setup: initializing")

export function ready(): boolean {
    return true
}
