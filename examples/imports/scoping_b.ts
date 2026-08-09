// The other supporting module for scoping.ts — see scoping_a.ts's comment.
// Its own private `helper` shares a name with scoping_a.ts's, and its own
// exported `run` shares a name with scoping_a.ts's export, but neither
// collides (TDD-00041).

function helper(): string {
    return "b's own helper"
}

export function run(): string {
    return helper()
}
