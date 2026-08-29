// Map<K,V> example
const scores = new Map<string, number>();

scores.set("alice", 95);
scores.set("bob", 87);
scores.set("carol", 92);

console.log(scores.size);         // 3
console.log(scores.get("alice")); // 95
console.log(scores.has("bob"));   // 1
console.log(scores.has("dave"));  // 0

scores.delete("bob");
console.log(scores.size);         // 2

// Number-keyed map
const lookup = new Map<number, number>();
lookup.set(1, 100);
lookup.set(2, 200);
lookup.set(3, 300);

console.log(lookup.get(2));  // 200
console.log(lookup.has(4));  // 0

// ── for...of iterates a Map's values (this compiler has no [key,value] ─────
// destructuring in for-of, so use .keys() for keys) ─────────────────────────
for (const v of lookup) {
  console.log(v);
}
// 100
// 200
// 300

for (const k of scores.keys()) {
  console.log(k);
}
// alice
// carol

// ── .forEach(fn): calls fn(value, key) for each entry ───────────────────────
scores.forEach((v, k) => {
  console.log(k + " -> " + v);
});
// alice -> 95
// carol -> 92

// ── .entries(): a real [key, value] tuple — destructure it directly ─────────
for (const [key, value] of lookup.entries()) {
  console.log(key + " = " + value);
}
// 1 = 100
// 2 = 200
// 3 = 300

// ── .clear(): removes every entry, size drops to 0, map stays usable ────────
console.log(scores.size); // 2
scores.clear();
console.log(scores.size); // 0
console.log(scores.has("alice")); // 0
scores.set("dave", 100);
console.log(scores.size); // 1

// ── new Map(entries): seed a map from a [key, value][] array of 2-tuples ─────
// K/V are inferred from the entries when no <K, V> is given (here string/number)
const prices = new Map([["pen", 2], ["notebook", 5], ["eraser", 1]]);
console.log(prices.get("notebook")); // 5
console.log(prices.size);            // 3

// An explicit <K, V> wins over inference
const codes = new Map<number, string>([[404, "Not Found"], [200, "OK"]]);
console.log(codes.get(200)); // OK

// The entries source can be an already-declared [K, V][] variable
const pairs: [string, number][] = [["x", 10], ["y", 20]];
const coords = new Map(pairs);
console.log(coords.get("y")); // 20

// Entry decomposition in for-of.
const inventory = new Map<string, number>();
inventory.set("apples", 3);
inventory.set("pears", 5);
for (const [item, count] of inventory) {
    console.log(item + ": " + String(count));
}
