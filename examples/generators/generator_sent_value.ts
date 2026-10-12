// A `yield` expression evaluates to the value passed to `.next(v)` — its type
// is the generator's TNext, so a resume value can flow into a differently
// typed target. See ADR-00954.

const box = { note: "initial" };

function* recorder(): Generator<string | undefined, void, any> {
  // The resume value (TNext any) is stored into a string field: the store coerces
  // through the dynamic path rather than emitting a raw element-word store.
  box.note = yield;
  yield "done";
}

const it = recorder();
it.next();              // run up to the first `yield`
it.next("updated");     // resume: sends "updated" as the yield value
console.log(box.note);

// A resume value consumed as a numeric local, too.
function* adder(): Generator<undefined, number, number> {
  const a: number = yield;
  const b: number = yield;
  return a + b;
}
const g = adder();
g.next();
g.next(10);
console.log(g.next(32).value);
