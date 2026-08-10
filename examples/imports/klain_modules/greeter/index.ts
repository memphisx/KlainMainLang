// A hand-constructed klmpm package (TDD-00054 Stage 1) — this directory
// shape (klain_modules/<name>/klain.json + real .ts source) is exactly what
// a real klmpm-fetched dependency would look like once the tool itself
// exists; nothing about the compiler cares how the directory got here.

export function greet(name: string): string {
    return "hello " + name + " from a klain_modules package"
}
