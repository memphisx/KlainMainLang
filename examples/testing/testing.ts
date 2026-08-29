// The native `test` builtin (TDD-00122 / ADR-00370): real async assertions for
// this project's own examples and tests, instead of eyeballing console.log.
//
// `mustCall(fn, n?)` wraps a callback so that if it is NOT invoked exactly n
// times (default 1) by the time the program exits, the process fails with a
// diagnostic and a non-zero exit code. This catches the classic async bug a
// bare console.log can't: a handler that silently never fires.

import { mustCall, mustNotCall, skip, isWindows } from 'test'

// A timer callback that MUST fire exactly once — verified at exit, no manual
// bookkeeping.
setTimeout(mustCall((): void => {
  console.log('timer fired')
}), 0)

// A handler that must fire for each of three items.
const each = mustCall((n: number): void => {
  console.log('item ' + n)
}, 3)
each(1)
each(2)
each(3)

// mustNotCall marks a path that must never run — e.g. an error callback on a
// call we expect to succeed. If it were ever invoked, the process would fail.
const onError = mustNotCall()

// Environment probes gate platform-specific expectations.
if (isWindows) {
  skip('this example targets POSIX')
}

console.log('all expectations registered; they are checked at exit')

// mustCall wraps any fixed-signature callback — arrays, nullable scalars,
// and wide arities included; only rest parameters stay unsupported.
const sum5 = mustCall((a: number, b: number, c: number, d: number, e: number) => {
    console.log(a + b + c + d + e)  // 15
})
sum5(1, 2, 3, 4, 5)
const takeList = mustCall((xs: string[]) => {
    console.log(xs.join("+"))       // alpha+beta
})
takeList(["alpha", "beta"])
