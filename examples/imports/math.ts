// A supporting module — imported by imports.ts, never compiled on its own.
//
// V1 scope: files reached only via import may contain declarations (and
// their own imports) only — no top-level executable statements. Only the
// entry file (imports.ts) actually runs code; this file just contributes
// functions/types to the merged program.

export function add(a: number, b: number): number {
    return a + b
}

export function mul(a: number, b: number): number {
    return a * b
}

export interface Point { x: number; y: number }

export enum Direction { North, East, South, West }

// Not exported — usable from within this file, but not importable from
// elsewhere (imports.ts trying `import { square } from './math'` would be
// a compile error: "'./math' has no exported member 'square'").
function square(n: number): number {
    return n * n
}

export function squareOf(n: number): number {
    return square(n)
}
