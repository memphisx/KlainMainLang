// var vs let/const scoping.
//
// let/const are block-scoped: a binding declared inside { ... } (or a loop or
// if body) is not visible after that block. var is function-scoped: it "leaks"
// out of the block it was declared in and stays visible for the rest of the
// enclosing function. See docs/adr/ADR-00210.md.

// --- var leaks out of a plain block ---
function leaks(): void {
    { var inner = 5 }
    console.log(inner)   // 5  (var is visible after the block)
}
leaks()

// --- a for-loop's var counter leaks too ---
function counter(): void {
    for (var i = 0; i < 3; i = i + 1) {}
    console.log(i)       // 3  (i survives the loop)
}
counter()

// --- var may be re-declared with the same name; the later one just wins ---
var total = 1
var total = 2
console.log(total)       // 2

// --- a var read on a control-flow path where it was never assigned ---
// An any-typed var reads back as undefined (like JS); a typed var reads a
// deterministic zero default rather than uninitialized memory.
function maybe(flag: boolean): void {
    if (flag) { var got: any = 42 }
    console.log(got)     // undefined  (flag is false, so the assignment never ran)
}
maybe(false)

// --- let/const stay block-scoped (the counterpart to var above) ---
function blockScoped(): void {
    let a = 10
    {
        let a = 20       // a different binding, shadowing the outer a
        console.log(a)   // 20
    }
    console.log(a)       // 10  (outer a is unchanged)
}
blockScoped()
