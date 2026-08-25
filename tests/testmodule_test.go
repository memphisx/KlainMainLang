package tests

import "testing"

// The native `test` builtin (TDD-00122): mustCall/mustNotCall/mustCallAtLeast/
// skip + env probes. mustCall wraps a callback, counts invocations, and verifies
// the count at process exit — a satisfied expectation exits 0, an unmet or
// exceeded one exits non-zero. See ADR-00370.

func expectExit(t *testing.T, src string, wantExit int) {
	t.Helper()
	_, code := compileAndRunExpectExitImports(t, src)
	if code != wantExit {
		t.Fatalf("exit code = %d, want %d\nsource:\n%s", code, wantExit, src)
	}
}

func TestE2ETestMustCallSatisfied(t *testing.T) {
	expectExit(t, `
import { mustCall } from 'test'
const cb = mustCall((n: number): number => n + 1)
console.log(cb(4))
`, 0)
}

func TestE2ETestMustCallNeverCalled(t *testing.T) {
	expectExit(t, `
import { mustCall } from 'test'
const cb = mustCall((): void => { console.log('x') })
console.log('not calling it')
`, 1)
}

func TestE2ETestMustCallCalledTooManyTimes(t *testing.T) {
	expectExit(t, `
import { mustCall } from 'test'
const cb = mustCall((n: number): number => n)
cb(1)
cb(2)
`, 1)
}

func TestE2ETestMustCallExplicitCount(t *testing.T) {
	expectExit(t, `
import { mustCall } from 'test'
const cb = mustCall((n: number): number => n, 2)
cb(1)
cb(2)
console.log('ok')
`, 0)
}

func TestE2ETestMustCallAtLeast(t *testing.T) {
	expectExit(t, `
import { mustCallAtLeast } from 'test'
const cb = mustCallAtLeast((n: number): number => n, 1)
cb(1)
cb(2)
cb(3)
console.log('ok')
`, 0)
}

func TestE2ETestMustNotCallNotInvoked(t *testing.T) {
	expectExit(t, `
import { mustNotCall } from 'test'
const f = mustNotCall()
console.log('never call f')
`, 0)
}

func TestE2ETestMustNotCallInvoked(t *testing.T) {
	expectExit(t, `
import { mustNotCall } from 'test'
const f = mustNotCall()
f()
`, 1)
}

func TestE2ETestSkipExitsZero(t *testing.T) {
	expectExit(t, `
import { skip } from 'test'
skip('unsupported on this platform')
console.log('should not run')
`, 0)
}

func TestE2ETestProbesOutput(t *testing.T) {
	assertOutputImports(t, `
import { isWindows, isMacOS, hasCrypto } from 'test'
console.log(isWindows)
console.log(hasCrypto)
`, "false\ntrue")
}

// mustCall composes with real async: a satisfied timer callback exits 0.
func TestE2ETestMustCallWithTimer(t *testing.T) {
	expectExit(t, `
import { mustCall } from 'test'
setTimeout(mustCall((): void => { console.log('fired') }), 0)
`, 0)
}
