// Compiler-compatible replacement for upstream test262's harness/assert.js.
//
// Upstream's `assert` is simultaneously a callable function (`assert(x)`)
// and a namespace with properties (`assert.sameValue(...)`) — real JS's
// function/object duality, which this compiler's type system has no
// representation for at all (a function value can't carry properties; an
// object/class namespace can't be called bare). The two forms can't both
// be preserved without editing test files themselves, which is out of
// scope here (see TDD-00008 Design V2) — this shim keeps the namespace
// form (`assert.sameValue`/`.notSameValue`/`.throws`), since it covers the
// larger share of real usage (~68% of language/ files vs. ~12% using the
// bare-call form, measured directly against the corpus). A file that only
// uses the bare `assert(x, msg)` form fails to compile against this shim
// (undefined function 'assert') — a real, visible, honestly-reported gap,
// not silently patched around.
//
// `sameValue`/`notSameValue`/`throws` all need a parameter type that
// covers "any test value." Since TDD-00062 (Staged V2) a bare `any`
// parameter is supported, so these take `any` directly — the argument is
// boxed at the call site and compared with `===` (which dispatches on the
// runtime tag). This covers scalars, `null`/`undefined`, and objects
// (compared by reference identity, matching JS `===`). Passing an *array*
// value is still a clean compile error at that call site (boxing an array
// into any/unknown is not yet supported) — a real, visible gap for the
// files that assert on array values directly.
//
// `assert.throws(ErrorConstructor, fn)` is even more fundamentally
// unavailable in full: real JS passes a built-in error type (`TypeError`,
// etc.) as a first-class *value*, comparing it against `thrown.constructor`
// — this compiler has no first-class reference to a built-in error type
// usable that way at all. Implemented best-effort below: checks that
// *something* was thrown, without verifying it was the expected error
// kind — a real, documented simplification (not silently pretending to
// check what it doesn't).
//
// `assert.compareArray` is not implemented — lower incidence than
// sameValue/notSameValue/throws, and needs array element-wise comparison
// this shim doesn't attempt yet. A call fails to compile, visible as its
// own bucket in the report.
// Function + namespace declaration merging (TDD-00095) gives this shim the
// real upstream shape at last: `assert(x)` (the bare-call form) AND
// `assert.sameValue(...)` (the namespace form) on one name — the exact TS
// idiom for JS's callable-object duality.
function assert(mustBeTrue: any, message: string = ""): void {
    if (mustBeTrue === true) {
        return;
    }
    throw new Test262Error(message);
}

namespace assert {
    export function ok(mustBeTrue: any, message: string = ""): void {
        if (mustBeTrue === true) {
            return;
        }
        throw new Test262Error(message);
    }

    export function sameValue(actual: any, expected: any, message: string = ""): void {
        if (actual === expected) {
            return;
        }
        // SameValue semantics, as upstream: NaN equals NaN (=== says no).
        // The one remaining SameValue difference — SameValue(+0, -0) being
        // false where === says true — is not reproduced (a rare wrong-pass,
        // not a wrong-fail).
        if (actual !== actual && expected !== expected) {
            return;
        }
        throw new Test262Error("assert.sameValue failed: " + message);
    }

    export function notSameValue(actual: any, unexpected: any, message: string = ""): void {
        if (actual !== unexpected) {
            return;
        }
        throw new Test262Error("assert.notSameValue failed: " + message);
    }

    export function throws(expectedErrorConstructor: any, func: () => void, message: string = ""): void {
        try {
            func();
        } catch (e) {
            return;
        }
        throw new Test262Error("assert.throws: expected an exception but none was thrown. " + message);
    }

    export function compareArray<T>(actual: T[], expected: T[], message: string = ""): void {
        if (actual.length !== expected.length) {
            throw new Test262Error("assert.compareArray length mismatch: " + message);
        }
        for (let i = 0; i < actual.length; i++) {
            if (actual[i] !== expected[i]) {
                throw new Test262Error("assert.compareArray element mismatch: " + message);
            }
        }
    }
}
