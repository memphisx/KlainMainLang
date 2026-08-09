// A supporting module for scoping.ts — demonstrates true per-file module
// scope (TDD-00041). `helper` here is a completely different, unrelated
// function from scoping_b.ts's own private `helper` of the same name; each
// file gets a private internal name for its own non-exported declarations,
// so the two never collide even though both end up merged into one program.
// `run` is exported under the same name scoping_b.ts also exports — safe
// because scoping.ts imports each one under its own alias (see below).

function helper(): string {
    return "a's own helper"
}

export function run(): string {
    return helper()
}
