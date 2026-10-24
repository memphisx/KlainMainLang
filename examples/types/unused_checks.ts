// Compiled with -no-unused-locals -no-unused-parameters (TypeScript's
// noUnusedLocals and noUnusedParameters): every declaration here is read,
// or is one of the forms the checks leave alone.

interface Point { x: number; y: number }

// A parameter starting with _ is exempt: the callback's shape needs it.
function onEach(xs: number[], fn: (value: number, _index: number) => void): void {
  for (const x of xs) fn(x, 0);
}

// `{ x, ...rest }` keeps x out of rest: x counts as used.
function withoutX(p: Point & { label: string }): { y: number; label: string } {
  const { x, ...rest } = p;
  return rest;
}

class Counter {
  #count = 0;
  constructor(private readonly step: number) {}
  bump(): number {
    this.#count += this.step;
    return this.#count;
  }
}

let total = 0;
onEach([1, 2, 3], (v, _i) => { total += v; });
console.log("total:", total);
console.log("rest:", JSON.stringify(withoutX({ x: 1, y: 2, label: "p" })));
const c = new Counter(5);
c.bump();
console.log("count:", c.bump());
