// Map, Set and Array iterators: live, lazy, and in insertion order.

const stock = new Map<string, number>([["apples", 3], ["pears", 0], ["plums", 7]]);

// A Map iterates its [key, value] entries.
for (const [fruit, count] of stock) {
  console.log(`${fruit}: ${count}`);
}

// Iteration is live: a deleted entry is skipped, an added one is visited.
for (const [fruit, count] of stock) {
  if (count === 0) stock.delete(fruit);
  if (fruit === "plums") stock.set("figs", 2);
}
console.log([...stock.keys()].join(", "));

// keys()/values()/entries() are iterators: step them by hand.
const names = stock.keys();
console.log(names.next().value);
console.log(names);
console.log(names.toArray());
console.log(names.next().done);

// A Set or Map can be built from any iterable.
const seen = new Set(stock.values());
console.log(seen);
const copy = new Map(stock.entries());
console.log(copy.size);

// An array's iterator sees elements pushed after it was made.
const queue = ["a", "b"];
const it = queue.values();
it.next();
queue.push("c");
console.log([...it]);
