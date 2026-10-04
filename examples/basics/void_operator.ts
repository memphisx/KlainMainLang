// `void e` evaluates e for its side effects and yields undefined.
let calls = 0
function track(): number {
    calls++
    return calls
}

console.log(void 0, void "ignored", typeof void 1)
console.log(void track(), calls)
const nothing = void 0
console.log(nothing === undefined)

// `undefined` is not a reserved word: a function may name a binding so.
function pick(undefined: number): number {
    return undefined * 2
}
function shadow(): string {
    const undefined = "a local"
    return undefined
}
console.log(pick(21), shadow(), undefined)
