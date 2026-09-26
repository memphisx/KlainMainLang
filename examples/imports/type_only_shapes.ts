// Types and one function, imported by type_only.ts. `Side` is exported
// through a type-only export list.

export interface Box { w: number; h: number }
interface Side { label: string }
export type { Side }

export function area(b: Box): number {
    return b.w * b.h
}
