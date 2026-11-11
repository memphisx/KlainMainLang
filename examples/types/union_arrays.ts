// Unions whose members are arrays, tuples or host objects. The value is held
// in a box; iteration, element reads, spreads and the array methods
// TypeScript resolves on the union (`map`, `filter`, `reduce`) work on
// whichever member it holds.

type Grid = number[] | number[][];

function sum(g: Grid): number {
  let s = 0;
  for (const row of g) s += Array.isArray(row) ? row.reduce((a, b) => a + b, 0) : row;
  return s;
}

console.log(sum([1, 2, 3]));          // 6
console.log(sum([[1, 2], [3], [4]])); // 10

const words: string[] | number[] = ["Thessaloniki", "Kalamaria"];
console.log(words.map((w: string | number) => String(w).length)); // [ 12, 9 ]
console.log([...words, "Panorama"].join(", "));

const pair: [string, number] | [string] = ["Ano Poli", 2];
for (const part of pair) console.log(part);

// A member every host type in the union has is read without narrowing.
function count(c: Map<string, number> | Set<string>): number {
  return c.size;
}
console.log(count(new Map([["a", 1]])), count(new Set(["x", "y"]))); // 1 2
