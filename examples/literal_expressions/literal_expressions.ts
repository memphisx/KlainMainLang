// Array/Map/Set/EventEmitter literals as general expressions (TDD-00028):
// before this, `[1, 2, 3]`, `new Map<K,V>()`, `new Set<T>()`, and
// `new EventEmitter<T>()` could only ever appear directly as a const/let
// initializer — not as a call argument, a return value, an object-literal
// field value, or a plain reassignment target.

// --- Array literal as a call argument ---

function first(arr: number[]): number {
  return arr[0];
}
console.log(first([10, 20, 30]));  // 10

// The declared parameter type drives coercion, not just the literal's own
// self-inferred type — [1, 2] here is built as float64[], not int64[].
function sum(arr: float64[]): float64 {
  return arr[0] + arr[1];
}
console.log(sum([1, 2]));  // 3

// --- Array literal as a return value ---

function pair(): number[] {
  return [1, 2];
}
const p = pair();
console.log(p[0]);       // 1
console.log(p.length);   // 2

// --- Array literal as an object-literal field value ---

interface Box {
  data: number[];
}
const box: Box = { data: [5, 6, 7] };
console.log(box.data[0]);       // 5
console.log(box.data.length);   // 3

// --- Plain reassignment (no let/const — was previously rejected outright) ---

let arr: number[] = [1, 2, 3];
arr = [4, 5];
console.log(arr[0]);       // 4
console.log(arr.length);   // 2

const other: number[] = [7, 8, 9];
arr = other;
console.log(arr[0]);       // 7

// --- Spread-containing literal as a call argument ---

const base: number[] = [1, 2];
console.log(first([...base, 3, 4]));  // 1
console.log(first([0, ...base]));     // 0

// --- new Array<T>(n) as a call argument ---

function len(a: number[]): number {
  return a.length;
}
console.log(len(new Array<number>(5)));  // 5

// --- new Map/Set/EventEmitter as a call argument or return value ---

function firstValue(m: Map<string, number>): number {
  return m.get("a");
}
const built = new Map<string, number>();
built.set("a", 42);
console.log(firstValue(built));  // 42

function makeMap(): Map<string, number> {
  return new Map<string, number>();
}
const m = makeMap();
m.set("x", 7);
console.log(m.get("x"));  // 7

function makeSet(): Set<number> {
  return new Set<number>();
}
const s = makeSet();
s.add(9);
console.log(s.has(9));  // 1

function makeEmitter(): EventEmitter<string> {
  return new EventEmitter<string>();
}
const e = makeEmitter();
e.on("msg", (data: string): void => {
  console.log("got: " + data);  // got: hi
});
e.emit("msg", "hi");

// Nested array literals (array-of-arrays) are a separate, still-open gap —
// see docs/tdd/TDD-00029.md — deliberately not demonstrated here.
