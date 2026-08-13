// Static-string eval (TDD-00046 static subset).
//
// When eval's argument is a compile-time-constant string that parses as a
// single expression, this compiler evaluates it by compiling that expression
// through its own pipeline — no embedded JS engine involved. The expression
// sees the enclosing scope (direct-eval semantics).

console.log(eval("2 ** 10"))          // 1024
console.log(eval("(1 + 2) * 3"))      // 9
console.log(eval("'a' + 'b' + 'c'"))  // abc
console.log(eval("10 % 3 === 1"))     // true

const answer: number = eval("6 * 7")
console.log(answer)                    // 42

// Anything outside the static-expression subset — a dynamic argument, a
// statement/declaration, or invalid syntax — is a clean compile error, not a
// silent or wrong result. (Those lines are omitted here so the example
// compiles; see docs/tdd/TDD-00046.md.)
