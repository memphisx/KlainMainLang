// A closure whose block body can reach the end without a `return` returns
// `undefined` on that path — its type is `T | undefined`, exactly as
// TypeScript infers it (ADR-01065). This holds for a bound arrow, a function
// expression, every array callback, a then-callback and an async body.

const double = (x: number) => { if (x > 1) return x * 2; };
console.log(double(1), double(2)); // undefined 4

const nums = [1, 2, 3];
console.log(nums.map((n) => { if (n > 1) return n * 10; }));    // [ undefined, 20, 30 ]
console.log(nums.filter((n) => { if (n > 1) return true; }));   // [ 2, 3 ]  (undefined is falsy)
console.log(nums.find((n) => { if (n > 2) return true; }));     // 3
console.log(nums.map((n) => { if (n > 1) return { n }; }));     // [ undefined, { n: 2 }, { n: 3 } ]

// Consuming a `number | undefined` under strict typing needs a narrowing.
const r = double(1);
if (r !== undefined) console.log(r + 1); else console.log("no value"); // no value
console.log(double(5) ?? 0);                                              // 10

// Through a promise the absence is preserved.
async function maybe(x: number) { if (x > 1) return x * 2; }
console.log(await maybe(1), await maybe(3));                      // undefined 6
Promise.resolve(2)
  .then((x) => { if (x > 1) return x * 3; })
  .then((v) => console.log("then", v));                           // then 6

// Binding such a closure where a `(x: number) => number` is declared is the
// same compile-time error tsc reports ("number | undefined is not assignable
// to number") — declare the slot `number | undefined`, or return on every path.
const total: (x: number) => number | undefined = (x) => { if (x > 0) return x; };
console.log(total(0), total(4));                                  // undefined 4
