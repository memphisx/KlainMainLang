// A supporting module — imported by default_export.ts and
// namespace_import.ts (TDD-00042), never compiled on its own. Declares one
// default export and one named export so both examples can exercise
// default imports and namespace-import member access against the same
// file.

export function shout(s: string): string {
    return s.toUpperCase()
}

export default function greet(name: string): string {
    return "hello " + name
}
