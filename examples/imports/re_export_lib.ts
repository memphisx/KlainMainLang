// Re-exports (TDD-00051) — a file that forwards another module's members
// without itself using them. `add`/`mul`/`greet` are never referenced as
// bare identifiers here; a re-export only ever binds the target's member
// into *this file's own export table*, not into its local scope — the
// forwarded names would be undefined if this file tried to use them
// directly, only re_export.ts (which actually imports them) can.

export { add } from './re_export_core'
export { mul as multiply } from './re_export_core'
export { default as greet } from './re_export_core'
