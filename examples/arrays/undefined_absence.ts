// T | undefined element-absence results: .pop()/.shift()/.find()/.findLast()/
// .at() return a real `undefined` on an empty array or a miss, exactly as
// TypeScript types them. Strict mode requires narrowing (`!== undefined`), a
// default (`??`), or a non-null assertion (`!`) before using the result where
// a bare T is expected; `-compat=js` auto-widens instead.

const empty: number[] = [];
const nums = [1, 2, 3];

console.log(empty.pop());                 // undefined
console.log(empty.shift());               // undefined
console.log(nums.find((n) => n > 10));    // undefined
console.log(nums.findLast((n) => n > 1)); // 3
console.log(nums.at(7));                  // undefined
console.log(nums.at(-1));                 // 3

// The presence bit takes part in comparisons: an absent result is never
// equal to the payload zero.
console.log(empty.pop() === undefined);   // true
console.log(empty.pop() === 0);           // false

// Consuming the result under strict mode:
const x = nums.pop();
if (x !== undefined) {
  console.log(x + 1);                     // 4
}
console.log(empty.pop() ?? 42);           // 42
console.log(nums.at(0)! * 10);            // 10

// Pointer elements too: a string array's miss is undefined, not null.
const names: string[] = [];
console.log(names.pop());                 // undefined
