// Compiler-compatible replacement for upstream test262's harness/sta.js.
// Upstream defines Test262Error as a prototype-based pseudo-class
// (`function Test262Error(...) {}` + `Test262Error.prototype.toString =
// function(){}` + `Test262Error.thrower = function(){}`, a property
// assigned onto a function object) and `throw`s a bare string from
// $DONOTEVALUATE — none of which this compiler's type system can
// represent (no prototype-based classes, no dynamic property assignment
// onto a function, no throwing a non-Error/non-object value in a way that
// stays distinguishable — see TDD-00022's own vanilla-JS-compatibility
// findings). Reimplements the same *observable* behavior real Test262
// files actually depend on (an object with a `.message` field, throwable,
// catchable) using this compiler's real `class`. Every actual test file
// is used completely unmodified — only this shared harness file is
// replaced. See TDD-00008 Design V2 and ADR-00151's own conformance ADR.
class Test262Error {
    message: string;
    constructor(message: string) {
        this.message = message;
    }
}

function $DONOTEVALUATE(): void {
    throw new Test262Error("Test262: This statement should not be evaluated.");
}
