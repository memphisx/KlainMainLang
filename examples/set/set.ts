// Set<T> example
const seen = new Set<string>();

seen.add("apple");
seen.add("banana");
seen.add("apple");  // duplicate — no effect

console.log(seen.size);            // 2
console.log(seen.has("apple"));    // 1
console.log(seen.has("cherry"));   // 0

seen.delete("banana");
console.log(seen.size);            // 1

// Numeric set
const ids = new Set<number>();
ids.add(10);
ids.add(20);
ids.add(10);  // duplicate

console.log(ids.size);    // 2
console.log(ids.has(20)); // 1

// ── for...of iterates a Set's elements directly ─────────────────────────────
for (const id of ids) {
  console.log(id);
}
// 10
// 20
