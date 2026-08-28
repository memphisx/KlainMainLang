package tests

import (
	"strings"
	"testing"
)

// TDD-00140: the node:test runner — test/it/describe/suite, TestContext,
// hooks, TAP-shaped reporting, nonzero exit on failure.

func TestE2ENodeTestRunnerPassing(t *testing.T) {
	out := compileAndRunImports(t, `
import { test, describe, it } from 'node:test'
import assert from 'assert'
test('adds', () => { assert.strictEqual(1 + 1, 2) })
describe('group', () => {
  it('inner', () => { assert.ok(true) })
})
test('ctx', (t) => {
  t.after(() => { console.log("# after ran") })
  t.test('sub', () => { assert.strictEqual("ab".length, 2) })
})
`)
	for _, want := range []string{"ok - adds", "ok - group > inner", "ok - sub", "# after ran", "ok - ctx", "tests 4, pass 4, fail 0, skip 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestE2ENodeTestRunnerFailureExitsNonzero(t *testing.T) {
	out, code := compileAndRunExpectExitImports(t, `
import { test } from 'node:test'
import assert from 'assert'
test('good', () => { assert.ok(true) })
test('bad', () => { assert.strictEqual(1, 2, "nope") })
test('after the failure still runs', () => { assert.ok(true) })
`)
	if code == 0 {
		t.Fatalf("expected nonzero exit with a failing test; output:\n%s", out)
	}
	for _, want := range []string{"ok - good", "not ok - bad: nope", "ok - after the failure still runs", "tests 3, pass 2, fail 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestE2ENodeTestRunnerSkipAndHooks(t *testing.T) {
	out := compileAndRunImports(t, `
import { test, after, beforeEach } from 'node:test'
import assert from 'assert'
let n = 0
beforeEach(() => { n = n + 1 })
after(() => { console.log("hooks saw", n) })
test('skipped', { skip: true }, () => { assert.ok(false) })
test('runs', () => { assert.ok(true) })
test('marks itself', (t) => { t.skip() })
`)
	for _, want := range []string{"ok - skipped # SKIP", "ok - runs", "ok - marks itself # SKIP", "hooks saw 2", "pass 1, fail 0, skip 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestE2ENodeTestRunnerAsyncBody(t *testing.T) {
	out := compileAndRunImports(t, `
import { test } from 'node:test'
import assert from 'assert'
test('async', async () => {
  const v = await Promise.resolve(41)
  assert.strictEqual(v + 1, 42)
})
`)
	if !strings.Contains(out, "ok - async") || !strings.Contains(out, "pass 1, fail 0") {
		t.Errorf("async test did not pass: %s", out)
	}
}

// diagnostics_channel (ADR-00420): pub/sub with string messages.

func TestE2EDiagnosticsChannelPubSub(t *testing.T) {
	out := compileAndRunImports(t, `
import dc from 'diagnostics_channel'
const channel = dc.channel('app:events')
console.log("before:", channel.hasSubscribers, channel.name)
const sub = (message: string, name: string) => { console.log("got", message, "on", name) }
dc.subscribe('app:events', sub)
console.log("after:", channel.hasSubscribers)
channel.publish("first")
console.log("removed:", dc.unsubscribe('app:events', sub))
channel.publish("silent")
channel.subscribe((m: string) => { console.log("one-arg", m) })
channel.publish("solo")
`)
	want := "before: false app:events\nafter: true\ngot first on app:events\nremoved: true\none-arg solo"
	if !strings.Contains(out, want) {
		t.Errorf("pub/sub flow mismatch:\n%s\nwant to contain:\n%s", out, want)
	}
}

func TestE2EDiagnosticsChannelUntypedSubscriberRejected(t *testing.T) {
	// A pre-wrapped subscriber whose params defaulted to number would
	// silently reinterpret published string pointers — must reject cleanly.
	_, err := parseAndCompileImports(t, `
import dc from 'diagnostics_channel'
import { mustCall } from 'node:test'
const sub = mustCall((message, name) => { console.log(message) }, 1)
dc.subscribe('x', sub)
dc.channel('x').publish("boom")
`)
	if err == nil || !strings.Contains(err.Error(), "parameters must be strings") {
		t.Fatalf("want clean untyped-subscriber rejection, got %v", err)
	}
}
