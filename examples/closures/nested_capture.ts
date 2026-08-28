// A nested function declaration may close over the enclosing function's
// parameters and locals (TDD-00129 Stage 1). It captures by reference — a
// mutation on either side is visible to the other — and keeps its captured
// environment even after the enclosing function returns.

// Capture a parameter.
function greet(name: string): string {
  function shout(): string { return name + "!"; }
  return shout();
}
console.log(greet("Thessaloniki")) // Thessaloniki!

// Capture a local by reference: the counter each call increments is the one
// enclosing `count`, and the outer body sees the updates.
function runCounter(): number {
  let count = 0;
  function tick(): void { count = count + 1; }
  tick();
  tick();
  count = count + 10;
  tick();
  return count;
}
console.log(runCounter()) // 13

// Escape as a value: the returned function still adds the captured base.
function makeAdder(base: number): (n: number) => number {
  function add(n: number): number { return base + n; }
  return add;
}
const add10 = makeAdder(10);
const add100 = makeAdder(100);
console.log(add10(5))  // 15
console.log(add100(5)) // 105

// A capturing nested function can still recurse on itself.
function sumTo(limit: number): number {
  let total = 0;
  function go(n: number): number {
    if (n > limit) return total;
    total = total + n;
    return go(n + 1);
  }
  return go(1);
}
console.log(sumTo(5)) // 15
