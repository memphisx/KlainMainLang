// Function.prototype.call / .apply (TDD-00137): invoke a first-class function
// value with an explicit argument list. `thisArg` is accepted for JS
// compatibility but has no effect here — closures carry no rebindable `this` —
// so this is the argument-plumbing use (`fn.call(null, …)` / `fn.apply(null,
// […])`), which is how most real code uses these. Runs the same under Node.js.

const add = (a: number, b: number): number => a + b;

// .call forwards its trailing arguments one by one.
console.log("call:", add.call(null, 3, 4));

// .apply forwards the elements of a (literal) array.
console.log("apply:", add.apply(null, [5, 6]));

// A named function works as a first-class value too.
function greet(name: string): string {
  return "kalimera, " + name;
}
console.log(greet.call(null, "kosme"));

// .apply also accepts a runtime array, spread into a rest parameter.
const total = (...ns: number[]): number => {
  let t = 0;
  for (const n of ns) t = t + n;
  return t;
};
const nums = [10, 20, 30];
console.log("apply(rest):", total.apply(null, nums));

// .bind returns a partially-applied function (thisArg ignored).
const multiply = (a: number, b: number): number => a * b;
const double = multiply.bind(null, 2);
console.log("bind:", double(21));

// Useful for a callback that a helper invokes indirectly.
function runTwice(fn: (n: number) => void): void {
  fn(1);
  fn(2);
}
const show = (n: number): void => { console.log("n =", n); };
runTwice((n: number) => show.call(null, n));
