package tests

import (
	"regexp"
	"strings"
	"testing"
)

// node:test (lib/node/test.ts, ADR-01282): Node's runner and its spec
// reporter, compared with Node v24's own output. Durations vary, so they are
// normalised.

var reportDurationRE = regexp.MustCompile(`\([0-9.e-]+ms\)|duration_ms .*`)

func normalizeReport(out string) string {
	return reportDurationRE.ReplaceAllStringFunc(out, func(m string) string {
		if strings.HasPrefix(m, "duration_ms") {
			return "duration_ms X"
		}
		return "(Xms)"
	})
}

const reportSummaryPass4 = `ℹ tests 4
ℹ suites 1
ℹ pass 4
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms X`

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
	want := `# after ran
✔ adds (Xms)
▶ group
  ✔ inner (Xms)
✔ group (Xms)
▶ ctx
  ✔ sub (Xms)
✔ ctx (Xms)
` + reportSummaryPass4
	if got := normalizeReport(out); got != want {
		t.Errorf("report:\n%s\nwant:\n%s", got, want)
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
	if code != 1 {
		t.Fatalf("exit code %d, want 1; output:\n%s", code, out)
	}
	want := `✔ good (Xms)
✖ bad (Xms)
✔ after the failure still runs (Xms)
ℹ tests 3
ℹ suites 0
ℹ pass 2
ℹ fail 1
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms X

✖ failing tests:

✖ bad (Xms)
  AssertionError`
	if got := normalizeReport(out); !strings.HasPrefix(got, want) {
		t.Errorf("report:\n%s\nwant prefix:\n%s", got, want)
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
	want := `hooks saw 2
﹣ skipped (Xms) # SKIP
✔ runs (Xms)
﹣ marks itself (Xms) # SKIP
ℹ tests 3
ℹ suites 0
ℹ pass 1
ℹ fail 0
ℹ cancelled 0
ℹ skipped 2
ℹ todo 0
ℹ duration_ms X`
	if got := normalizeReport(out); got != want {
		t.Errorf("report:\n%s\nwant:\n%s", got, want)
	}
}

// A test that waits on the event loop has its line reported before the next
// test starts; synchronous tests report together, after their output.
func TestE2ENodeTestRunnerAsyncBody(t *testing.T) {
	out := compileAndRunImports(t, `
import test from 'node:test'
import assert from 'assert'
test('one', () => { console.log('in one') })
test('two', async () => {
  const v = await new Promise<number>((r) => setTimeout(() => r(41), 20))
  assert.strictEqual(v + 1, 42)
  console.log('in two')
})
test('three', () => { console.log('in three') })
test.skip('four')
test.todo('five')
`)
	want := `in one
✔ one (Xms)
in two
✔ two (Xms)
in three
✔ three (Xms)
﹣ four (Xms) # SKIP
✔ five (Xms) # TODO
ℹ tests 5
ℹ suites 0
ℹ pass 3
ℹ fail 0
ℹ cancelled 0
ℹ skipped 1
ℹ todo 1
ℹ duration_ms X`
	if got := normalizeReport(out); got != want {
		t.Errorf("report:\n%s\nwant:\n%s", got, want)
	}
}

// diagnostics_channel: Node's lib/diagnostics_channel.js in TypeScript.

func TestE2EDiagnosticsChannelPubSub(t *testing.T) {
	out := compileAndRunImports(t, `
import dc from 'diagnostics_channel'
const channel = dc.channel('app:events')
console.log("before:", channel.hasSubscribers, channel.name, channel instanceof dc.Channel)
const sub = (message: unknown, name: string | symbol) => { console.log("got", message, "on", name) }
dc.subscribe('app:events', sub)
console.log("after:", channel.hasSubscribers, dc.hasSubscribers('app:events'), dc.channel('app:events') === channel)
channel.publish({ path: "/kalimera", n: 1 })
console.log("removed:", dc.unsubscribe('app:events', sub), dc.unsubscribe('app:events', sub))
channel.publish("silent")
channel.subscribe((m: unknown) => { console.log("one-arg", m) })
channel.publish("solo")
`)
	want := "before: false app:events true\nafter: true true true\ngot { path: '/kalimera', n: 1 } on app:events\nremoved: true false\none-arg solo"
	if !strings.Contains(out, want) {
		t.Errorf("pub/sub flow mismatch:\n%s\nwant to contain:\n%s", out, want)
	}
}

// A tracing channel publishes start/end (and error) around traceSync, and
// asyncStart/asyncEnd around tracePromise's settlement and traceCallback's
// callback; a subscriber's throw goes to 'uncaughtException' without
// stopping the others.
func TestE2EDiagnosticsChannelTracing(t *testing.T) {
	out := compileAndRunImports(t, `
import { tracingChannel, subscribe, channel } from 'node:diagnostics_channel'
const tc = tracingChannel('op')
tc.subscribe({
  start: (m: any) => console.log('start', m.arg),
  end: (m: any) => console.log('end', m.result, m.error?.message),
  asyncStart: (m: any) => console.log('asyncStart', m.result),
  asyncEnd: (m: any) => console.log('asyncEnd', m.result),
  error: (m: any) => console.log('error', m.error.message),
})
console.log('sync', tc.traceSync((a: number, b: number) => a + b, { arg: 'x' }, undefined, 2, 3))
try { tc.traceSync(() => { throw new Error('bad') }, { arg: 'y' }) } catch (e: any) { console.log('caught', e.message) }
tc.tracePromise(async (v: number) => v * 2, { arg: 'p' }, undefined, 21).then((r: any) => console.log('promise', r))
tc.traceCallback((n: number, cb: (err: any, res: any) => void) => { setTimeout(() => cb(null, n + 1), 1) }, -1, { arg: 'cb' }, undefined, 41, (err: any, res: any) => console.log('callback', err, res))
subscribe('boom', () => { throw new Error('sub threw') })
subscribe('boom', (m: unknown) => console.log('second still runs', m))
process.on('uncaughtException', (e) => console.log('uncaught', e.message))
channel('boom').publish(7)
`)
	want := "start x\nend 5 undefined\nsync 5\nstart y\nerror bad\nend undefined bad\ncaught bad\nstart p\nend undefined undefined\nstart cb\nend undefined undefined\nsecond still runs 7\nasyncStart 42\nasyncEnd 42\npromise 42\nuncaught sub threw\nasyncStart 42\ncallback null 42\nasyncEnd 42"
	if out != want {
		t.Errorf("tracing output:\n%s\nwant:\n%s", out, want)
	}
}
