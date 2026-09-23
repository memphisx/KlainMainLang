// Arrays, TypedArrays and Symbols keep their identity inside an `any`.
// Run with -compat=js for the mixed-literal line (strict rejects it cleanly).

// A TypedArray boxed into `any` still knows what it is.
const ta: any = new Int32Array([8, 9]);
console.log(ta, ta.length, ta[1], Array.isArray(ta)); // Int32Array(2) [ 8, 9 ] 2 9 false
console.log(JSON.stringify(ta)); // {"0":8,"1":9}  (a TypedArray serializes by index keys)

// A plain array through `any`: index reads, length, JSON, String().
const nums = [1.5, 2];
const anyNums: any = nums;
console.log(anyNums[0], anyNums.length, JSON.stringify(anyNums), String(anyNums)); // 1.5 2 [1.5,2] 1.5,2

// A nested array is readable one level down.
const grid: any = [[1], [2, 3]];
console.log(grid[1], grid[1][0]); // [ 2, 3 ] 2

// TypedArray.prototype.set takes any array-like source: a boxed typed array,
// a string (characters → numbers), an array-like object; null throws.
const dst = new Int32Array(3);
dst.set(ta);
console.log(dst); // Int32Array(3) [ 8, 9, 0 ]
dst.set("12x");
console.log(dst); // Int32Array(3) [ 1, 2, 0 ]
dst.set({ length: 2, 0: 7, 1: "8" }, 1);
console.log(dst); // Int32Array(3) [ 1, 7, 8 ]
try {
  dst.set(null);
} catch (e) {
  console.log((e as Error).message); // Cannot convert undefined or null to object
}

// A Symbol inside `any` is still a symbol.
const sym = Symbol("tag");
const anySym: any = sym;
console.log(typeof anySym, anySym === sym, anySym.description, String(anySym)); // symbol true tag Symbol(tag)
try {
  Number(anySym);
} catch (e) {
  console.log((e as Error).message); // Cannot convert a Symbol value to a number
}

// Named functions see an any-typed module binding.
const config: any = { retries: 3 };
function retries(): number {
  return config.retries;
}
console.log(retries()); // 3
