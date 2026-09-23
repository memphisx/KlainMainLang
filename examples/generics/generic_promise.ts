// A generic function's type parameter is in scope for its whole body, so `T`
// works at any nesting depth — `Promise<T>`, `T[]`, `T[][]`, `Map<string, T>`,
// `T | null`, a callback typed with T — not only as a bare parameter type.

function later<T>(ms: number, v: T): Promise<T> {
  return new Promise<T>((resolve) => setTimeout(() => resolve(v), ms));
}

function pair<T>(a: T, b: T): T[] {
  const xs: T[] = [a, b];
  return xs;
}

function grid<T>(a: T): T[][] {
  const m: T[][] = [[a], [a, a]];
  return m;
}

function index<T>(key: string, v: T): Map<string, T> {
  const m = new Map<string, T>();
  m.set(key, v);
  return m;
}

function firstOrNull<T>(xs: T[]): T | null {
  if (xs.length > 0) return xs[0]!;
  return null;
}

function apply<T>(v: T, f: (x: T) => T): T {
  return f(v);
}

async function main(): Promise<void> {
  console.log(await later(1, "x"), await later(1, 2));   // x 2
  console.log(pair("a", "b"), pair(1, 2));              // [ 'a', 'b' ] [ 1, 2 ]
  console.log(grid("s")[1].length);                     // 2
  console.log(index("k", 42).get("k"));                 // 42
  console.log(firstOrNull<string>([]), firstOrNull([7])); // null 7
  console.log(apply(2, (x) => x * 3), apply("q", (x) => x + "!")); // 6 q!
}
main();
