// util.inspect details console.log follows from Node: at most 100 array
// elements are shown, the rest collapse into `... n more items`; negative zero
// prints as -0 inside a container; column widths count a full-width glyph as
// two cells and a combining mark as none.

const big: number[] = [];
for (let i = 0; i < 105; i++) big.push(i);
console.log(big);                       // 100 numbers in aligned columns, then `... 5 more items`

console.log([-0, 0, 1.5, -0]);          // [ -0, 0, 1.5, -0 ]
console.log({ z: -0 });                 // { z: -0 }

const wide: string[] = [];
for (let i = 0; i < 12; i++) wide.push(i % 2 ? "日本" : "ab");
console.log(wide);                      // columns line up despite the wide glyphs
console.log(["é", "é", "日本語"]); // [ 'é', 'é', '日本語' ]
