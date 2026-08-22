// Bound generator expressions with yield-inferred element types, and
// JS-faithful mixed int/float arithmetic.

var countdown = function* (from: number) {
  for (let i = from; i > 0; i--) {
    yield i;
  }
  yield 0;
};

const parts: number[] = [];
for (const n of countdown(3)) {
  parts.push(n);
}
console.log(parts.join(" -> "));

const halves = function* halves(n: number) {
  for (let i = 1; i <= n; i++) {
    yield i * 0.5; // mixed int/float promotes to double (was truncated)
  }
};
let total = 0.0;
for (const h of halves(4)) {
  total = total + h;
}
console.log(total, 3 * 1.5, 7 / 2.0);
