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

// ── .forEach(fn): calls fn(element) for each element ────────────────────────
ids.forEach((id) => {
  console.log("id " + id);
});
// id 10
// id 20

// ── .clear(): removes every element, size drops to 0, set stays usable ──────
console.log(seen.size); // 1
seen.clear();
console.log(seen.size); // 0
console.log(seen.has("apple")); // 0
seen.add("date");
console.log(seen.size); // 1

// ── new Set(iterable): pre-populate from an array, duplicates collapse ──────
const fromLiteral = new Set([1, 2, 3, 2, 1]);
console.log(fromLiteral.size); // 3

const source: string[] = ["x", "y", "x"];
const fromVariable = new Set(source);
console.log(fromVariable.size); // 2

// An explicit <T> still works the same way it does for the empty form.
const typed = new Set<number>(source.map((s) => s.length));
console.log(typed.size); // 1 (both "x" and "y" have length 1)

// An empty array argument is just an empty set — same as the no-argument form.
const fromEmpty = new Set<number>([]);
console.log(fromEmpty.size); // 0
