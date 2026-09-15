// Array reference semantics across function returns and closure captures
// (TDD-00213 Stage 3). An array is one object: returning it or capturing it in a
// closure shares the same object, so mutations propagate and identity holds —
// exactly as in Node.

// --- Returning an array keeps its identity ---
const store = { items: [1, 2, 3] };
function getItems(): number[] {
  return store.items; // returns the SAME array, not a copy
}
const items = getItems();
items.push(4);
console.log(store.items.join(",")); // 1,2,3,4 — the caller's push is visible
console.log(items === store.items); // true    — same object

// Two calls returning the same source array are identity-equal…
console.log(getItems() === getItems()); // true
// …but a returned fresh literal is a new object each time.
function freshPair(): number[] {
  return [0, 0];
}
console.log(freshPair() === freshPair()); // false

// --- Capturing an array in a closure shares it both ways ---
function makeList() {
  const xs: number[] = [];
  const add = (v: number) => {
    xs.push(v);
  }; // captures xs by reference
  const snapshot = () => xs; // returns the shared array
  return { add, snapshot };
}
const list = makeList();
list.add(10);
list.add(20);
console.log(list.snapshot().join(",")); // 10,20

// An outer mutation is visible through a captured reader.
const nums = [1];
const readNums = () => nums;
nums.push(2);
console.log(readNums().join(",")); // 1,2
