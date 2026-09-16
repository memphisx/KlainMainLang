// An omitted optional `any` parameter reads as `undefined` (ADR-00956). An
// `any` slot is a NaN-boxed word, so a missing argument must be the boxed
// `undefined` sentinel — not a raw zero word, which would read back as the
// boxed integer 0 (the double 5e-324) and make `x === undefined` wrongly false.
// This is exactly the shape of the Test262 async `$DONE(error?: any)` reporter:
// `$DONE()` on success must take the "no error" branch. Runs the same on Node.

function report(error?: any): void {
  if (error !== undefined && error !== null) {
    console.log("failure:", String(error));
  } else {
    console.log("success");
  }
}

// Called with no argument: the optional `any` is `undefined` → success branch.
report();

// Called with a value: the failure branch, stringifying the argument.
report("something went wrong");

// A plain omitted-optional read compares equal to undefined and unequal to null.
function probe(x?: any): void {
  console.log("=== undefined:", x === undefined, "| === null:", x === null);
}
probe();
