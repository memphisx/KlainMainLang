// --- Basic generator: yield a sequence, then return a final value ---
function* basic(): number {
    yield 1;
    yield 2;
    return 3;
}
const g1 = basic();
const g1r1 = g1.next();
console.log(g1r1.value, g1r1.done);   // 1 0
const g1r2 = g1.next();
console.log(g1r2.value, g1r2.done);   // 2 0
const g1r3 = g1.next();
console.log(g1r3.value, g1r3.done);   // 3 1

// --- Generator function parameters ---
// Calling a generator function doesn't run its body at all — only the
// first .next() call does. An infinite generator is fine as long as
// nothing tries to drain it all at once.
function* countFrom(start: number, step: number): number {
    let n = start;
    while (true) {
        yield n;
        n += step;
    }
}
const evens = countFrom(0, 2);
console.log(evens.next().value);   // 0
console.log(evens.next().value);   // 2
console.log(evens.next().value);   // 4

// --- Sending values in ---
// The value a `yield` expression evaluates to is whatever the *next*
// .next(value) call sends in — not anything related to what was yielded.
function* echo(): number {
    const a = yield 1;
    const b = yield a + 10;
    return b * 2;
}
const g2 = echo();
console.log(g2.next().value);    // 1  (first .next() just starts the body)
console.log(g2.next(5).value);   // 15 (a = 5, yields a + 10)
console.log(g2.next(7).value);   // 14 (b = 7, returns b * 2)

// --- Independent instances ---
// Each call to a generator function creates its own instance with its
// own private state — advancing one never affects another.
function* counter(): number {
    let n = 0;
    while (true) {
        n += 1;
        yield n;
    }
}
const a = counter();
const b = counter();
console.log(a.next().value);   // 1
console.log(a.next().value);   // 2
console.log(b.next().value);   // 1 (independent of a)
console.log(a.next().value);   // 3

// --- String-typed generator ---
function* names(): string {
    yield "alice";
    yield "bob";
}
const g3 = names();
let r = g3.next();
while (!r.done) {
    console.log(r.value);
    r = g3.next();
}

// --- for...of over a generator ---
// The cleanest way to drain a generator — stops automatically at `done`,
// never sees the final `return` value (only what each `yield` produced).
// The generator function is called exactly once (constructing one
// instance), not once per iteration.
function* fib(limit: number): number {
    let a = 0;
    let b = 1;
    while (a < limit) {
        yield a;
        const next = a + b;
        a = b;
        b = next;
    }
}
for (const n of fib(30)) {
    console.log(n);   // 0 1 1 2 3 5 8 13 21
}

// break/continue work normally inside a generator for...of loop.
let total = 0;
for (const n of fib(30)) {
    if (n > 10) { break; }
    if (n % 2 !== 0) { continue; }
    total += n;
}
console.log(total);   // 0 + 2 + 8 = 10

// Calling .next() again on an already-finished generator is a no-op,
// returning {value: <T's zero>, done: true} — this compiler's stand-in
// for real JS's `undefined` (no general "undefined" sentinel for a
// concrete type). For a ptr-shaped type like string, that zero value is a
// null pointer, printed as "null" — the same rendering a nullable string
// already gets elsewhere in this compiler, not a generator-specific case.
const g3r = g3.next();
console.log(g3r.value, g3r.done);   // null 1
