// A function may read a `let`/`const` declared after it in the same body.
// It shares that binding: called once the declaration has run, it sees the
// value and every later write; called before, it throws JavaScript's
// "Cannot access … before initialization" ReferenceError.

function server(): void {
  const describe = (): string => `${city} has ${visits} visits`;
  const visit = (): void => { visits++; };
  const city = "Thessaloniki";
  let visits = 0;
  visit(); visit();
  console.log(describe()); // Thessaloniki has 2 visits
}
server();

// A hoisted function declaration reads a binding written later in the body.
function hoisted(): void {
  function label(): string { return prefix + ":" + count; }
  const prefix = "count";
  let count = 1;
  count += 1;
  console.log(label()); // count:2
}
hoisted();

// Called before the binding's declaration ran: the dead zone.
function early(): void {
  const twice = (): number => late * 2;
  try {
    twice();
  } catch (e) {
    console.log((e as Error).name + ": " + (e as Error).message); // ReferenceError: Cannot access 'late' before initialization
  }
  const late = 21;
  console.log(twice()); // 42
}
early();
