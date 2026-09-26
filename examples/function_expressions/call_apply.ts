// Function.prototype.call / .apply / .bind (TDD-00137): invoke a first-class
// function value with an explicit argument list, and — for a function with a
// `this: T` parameter — an explicit receiver. Runs the same under Node.js.

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

// .bind returns a partially-applied function.
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

// A `this: T` parameter types the receiver, which .call/.apply/.bind supply
// and a method call passes.
interface Counter { count: number }
function bump(this: Counter, by: number): number {
  this.count += by;
  return this.count;
}
const counter: Counter = { count: 0 };
console.log("this via call:", bump.call(counter, 5), bump.apply(counter, [2]));
const bumpCounter = bump.bind(counter);
console.log("this via bind:", bumpCounter(3), counter.count);

// A callback typed with `this` gets the receiver the caller passes, as a
// Node options object's `read()` gets its stream.
class Source {
  items: string[] = [];
  private read?: (this: Source, n: number) => void;
  constructor(opts: { read?: (this: Source, n: number) => void }) { this.read = opts.read; }
  pull(n: number): string[] {
    if (this.read) this.read.call(this, n);
    return this.items;
  }
}
const src = new Source({ read(n) { for (let i = 0; i < n; i++) this.items.push("item" + i); } });
console.log("callback this:", src.pull(3).join(","));
