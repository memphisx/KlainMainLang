// --- The comma / sequence operator (ADR-00179) ---
// `(a, b, c)` evaluates each operand left to right and yields the last one.
// Earlier operands run purely for their side effects.

// The value is the last operand.
const x = (1, 2, 3);
console.log(x);  // 3

// Earlier operands still execute (here, two console.log calls run first).
const r = (console.log("first"), console.log("second"), 42);
console.log(r);  // first / second / 42

// Assignment is an expression, so a sequence can update state and then yield
// a value computed from it.
let count = 0;
const next = (count = count + 1, count * 10);
console.log(count, next);  // 1 / 10

// The last operand's type is the sequence's type — here a string. (The first
// operand is written `count + 0` rather than a bare `count`: a sequence whose
// first operand is a lone identifier, `(count, "ready")`, is instead parsed as
// an arrow-function parameter list — a known limitation, see ADR-00179.)
const label = (count + 0, "ready");
console.log(label);  // ready

// Useful in a condition: perform a side effect, then test.
let a = 0;
if ((a = 5, a > 3)) {
    console.log("a was set and is greater than 3");
}
