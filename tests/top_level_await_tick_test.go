package tests

import "testing"

// A module top-level `await` takes one microtask tick even when there is
// nothing to wait for — a plain value, an already-settled promise — exactly as
// inside an async function: the continuation runs behind the jobs already
// queued and ahead of what those jobs enqueue. Top-level code used to carry
// straight on, so every `await` line ran before the first `.then` reaction
// (Test262 language/module-code/top-level-await/top-level-ticks.js).
func TestE2ETopLevelAwaitTakesOneTick(t *testing.T) {
	assertOutput(t, `
const actual: string[] = [];
Promise.resolve(0)
  .then(() => actual.push('tick 1'))
  .then(() => actual.push('tick 2'))
  .then(() => actual.push('tick 3'))
  .then(() => actual.push('tick 4'))
  .then(() => {
    console.log(actual.join(","));
  });
await 1; actual.push('await 1');
await 2; actual.push('await 2');
await 3; actual.push('await 3');
await 4; actual.push('await 4');
`, "tick 1,await 1,tick 2,await 2,tick 3,await 3,tick 4,await 4\n")
}

func TestE2ETopLevelAwaitSettledPromiseAndNoMicrotasks(t *testing.T) {
	assertOutput(t, `
const v = await 5;
console.log(v);
const p = Promise.resolve("x");
const log: string[] = [];
p.then(() => log.push("then-1")).then(() => log.push("then-2"));
const got = await p;
log.push("after-await-" + got);
await null;
log.push("after-null");
setTimeout(() => console.log(log.join(",")), 0);
`, "5\nthen-1,after-await-x,then-2,after-null\n")
	// No promise, no reaction anywhere: the tick has nothing to run.
	assertOutput(t, `
const n = await 41;
console.log(n + 1);
`, "42\n")
}
