// Compiler-compatible replacement for upstream test262's
// harness/doneprintHandle.js (the async-protocol reporter every host injects
// into `async`-flagged files). Upstream branches on `typeof error === 'object'
// && 'name' in error` — an untyped structural probe this compiler's type
// system can't represent. Reimplements the same observable protocol: a test
// calls `$DONE()` on success or `$DONE(err)` on failure, and the runner reads
// the `Test262:AsyncTestComplete` / `Test262:AsyncTestFailure:` marker from
// stdout after the event loop drains (TDD-00204). Every actual test file runs
// unmodified — only this shared harness file is replaced.
function $DONE(error?: any): void {
    if (error !== undefined && error !== null) {
        console.log("Test262:AsyncTestFailure:Test262Error: " + String(error));
    } else {
        console.log("Test262:AsyncTestComplete");
    }
}
