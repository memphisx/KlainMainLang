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
