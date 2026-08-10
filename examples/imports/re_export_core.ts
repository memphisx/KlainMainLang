// A supporting module — imported only via re_export_lib.ts's re-exports
// (TDD-00051), never directly by re_export.ts and never compiled on its
// own.

export function add(a: number, b: number): number {
    return a + b
}

export function mul(a: number, b: number): number {
    return a * b
}

export default function greet(name: string): string {
    return "hello " + name
}
