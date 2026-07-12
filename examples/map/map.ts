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
