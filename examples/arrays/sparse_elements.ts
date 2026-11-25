// Sparse arrays: a write far past the end keeps the elements out of line,
// as V8's dictionary elements do, instead of allocating every slot up to it.

const ids: string[] = [];
ids[4294967294] = "last index"; // 2^32 - 2, the largest array index
ids[7] = "seven";
console.log(ids.length, ids[4294967294], ids[7], ids[8]);
console.log(4294967294 in ids, 100 in ids);

// Truncating drops the far element and leaves a plain short array.
ids.length = 8;
console.log(ids.length, ids[4294967294], ids[7]);

// Walking a sparse array sees only the set elements, as in Node.
const scores: number[] = [10];
scores[50000] = 30;
let total = 0;
scores.forEach((s) => { total += s; });
console.log(scores.length, total, scores.indexOf(30));
