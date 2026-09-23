// util.inspect's line layout (ADR-01067): console.log lays an object or array
// out exactly as Node does — entries share one line while they fit the
// 80-column `breakLength` and the value nests fewer than three levels below;
// otherwise one entry per line, indented two spaces per level. An array of
// more than six short elements is regrouped into aligned columns (numbers
// right-aligned). Beyond the default depth of 2 a non-empty container shows
// as [Object]/[Array]; an empty one still prints `{}`/`[]`.

console.log([1, 2, 3]);
// [ 1, 2, 3 ]

console.log([1, 2, 3, 4, 5, 6, 7, 8, 9, 10]);
// [
//   1, 2, 3, 4,  5,
//   6, 7, 8, 9, 10
// ]

console.log(["alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"]);
// [
//   'alpha',   'beta',
//   'gamma',   'delta',
//   'epsilon', 'zeta',
//   'eta',     'theta'
// ]

interface Row { name: string; email: string; role: string }
const rows: Row[] = [
  { name: "Ada Lovelace", email: "ada@example.com", role: "admin" },
  { name: "Grace Hopper", email: "grace@example.com", role: "editor" },
];
console.log(rows);
// [
//   { name: 'Ada Lovelace', email: 'ada@example.com', role: 'admin' },
//   { name: 'Grace Hopper', email: 'grace@example.com', role: 'editor' }
// ]

console.log({ a: { b: { c: { d: 1 } } } }, { a: { b: { c: {} } } });
// { a: { b: { c: [Object] } } } { a: { b: { c: {} } } }

// Strings are escaped and quoted the way Node does: single quotes unless the
// string contains one, control characters as escapes.
console.log({ q: "it's", tab: "a\tb", nl: "x\ny" });
// { q: "it's", tab: 'a\tb', nl: 'x\ny' }

// The dynamic (`any`) inspector uses the same layout, at the right depth.
const doc: any = JSON.parse('{"ids":[1,2,3,4,5,6,7,8,9,10,11,12],"meta":{"tags":["a","b"]}}');
console.log(doc);
// {
//   ids: [
//      1,  2, 3, 4,  5,
//      6,  7, 8, 9, 10,
//     11, 12
//   ],
//   meta: { tags: [ 'a', 'b' ] }
// }
