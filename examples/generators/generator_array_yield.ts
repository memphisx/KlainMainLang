// --- Generator with an array element type (ADR-00676) ---
// A generator whose declared return type is an array yields/sends arrays that
// round-trip through every generator slot as the inline {ptr,i64} aggregate.

// yield array literals of varying length, consumed by for...of
function* rows(): number[] {
    yield [1, 2];
    yield [3, 4, 5];
    yield [];
}
for (const r of rows()) {
    console.log(r.length, r.join(","));   // 2 1,2 / 3 3,4,5 / 0
}

// yield an array held in a local, then read .value back manually
function* pairs(): number[] {
    const a = [10, 20];
    yield a;
    yield [30];
}
const it = pairs();
const r1 = it.next();
console.log(r1.done, r1.value.join("-"));  // false 10-20
const r2 = it.next();
console.log(r2.done, r2.value.join("-"));  // false 30
console.log(it.next().done);               // true

// yield* delegation forwards each inner array through the outer generator
function* words(): string[] {
    yield ["a", "b"];
    yield ["c"];
}
function* deleg(): string[] {
    yield ["x"];
    yield* words();
    yield ["z"];
}
for (const w of deleg()) {
    console.log(w.join("+"));              // x / a+b / c / z
}

// async generator of arrays, drained with for await
async function* agen(): number[] {
    yield [1, 2];
    yield [3, 4, 5];
}
async function main2() {
    for await (const r of agen()) {
        console.log(r.length, r.join(","));  // 2 1,2 / 3 3,4,5
    }
}
main2();
