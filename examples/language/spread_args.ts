// Spread a runtime-length array into a function's rest parameter.
function sum(...nums: number[]): number {
  let total = 0;
  for (const n of nums) total += n;
  return total;
}

const values = [4, 8, 15, 16, 23, 42];
console.log("sum of all:", sum(...values));
console.log("sum of some:", sum(1, 2, 3));

// A spread may follow fixed arguments.
function tagged(tag: string, ...nums: number[]): string {
  return tag + " (" + nums.length + " values, sum " + sum(...nums) + ")";
}
console.log(tagged("data", ...values));

// Arrow functions with a rest parameter work the same way.
const product = (...nums: number[]): number => {
  let p = 1;
  for (const n of nums) p *= n;
  return p;
};
console.log("product:", product(...[2, 3, 4]));

// Multiple spreads and positional arguments freely mix, concatenating into the
// rest parameter at runtime in left-to-right order.
const lows = [1, 2, 3];
const highs = [100, 200];
console.log("merged sum:", sum(...lows, 50, ...highs));

// Spread into a class method's rest parameter, instance or static.
class Stats {
  totalFrom(base: number, ...nums: number[]): number {
    let t = base;
    for (const n of nums) t += n;
    return t;
  }
  static sum(...nums: number[]): number {
    let t = 0;
    for (const n of nums) t += n;
    return t;
  }
}
const stats = new Stats();
console.log("total from base:", stats.totalFrom(1000, ...lows, ...highs));
console.log("static sum:", Stats.sum(...lows, ...highs));

// Spread also works into the common variadic builtins, folded at runtime.
console.log("largest:", Math.max(...lows, ...highs));
console.log("smallest:", Math.min(0, ...lows));
console.log("all values:", ...lows, ...highs);
