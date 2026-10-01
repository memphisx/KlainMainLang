package tests

import (
	"strings"
	"testing"
)

// timers/promises (TDD-00230): the promise timers, the interval iterator,
// abort and option validation, as Node v24 prints them.

func TestE2ETimersPromisesSleepAndImmediate(t *testing.T) {
	assertOutputImports(t, `
import { setTimeout as sleep, setImmediate as immediate } from 'timers/promises'
import timers from 'timers/promises'
console.log(await sleep(5, 'value'))
console.log(await immediate('imm'))
const order: string[] = []
await Promise.all([sleep(20).then(() => { order.push('20') }), sleep(5).then(() => { order.push('5') })])
console.log(order.join(','))
console.log(typeof timers.setTimeout, typeof timers.scheduler)
await timers.scheduler.wait(5)
await timers.scheduler.yield()
console.log('done')
`, "value\nimm\n5,20\nfunction object\ndone")
}

func TestE2ETimersPromisesAbortAndOptions(t *testing.T) {
	assertOutputImports(t, `
import { setTimeout as sleep, setImmediate as immediate } from 'timers/promises'
const ac = new AbortController()
const pending = sleep(1000, 'never', { signal: ac.signal })
ac.abort()
try { await pending } catch (e: any) { console.log(e.name, e.code, e.message) }
const pre = new AbortController()
pre.abort('why')
try { await immediate('x', { signal: pre.signal }) } catch (e: any) { console.log(e.name, e.cause) }
try { await sleep(1, 'x', 5 as any) } catch (e: any) { console.log(e.code, e.message) }
try { await sleep(1, 'x', { ref: 'no' } as any) } catch (e: any) { console.log(e.code, e.message) }
sleep(100000, undefined, { ref: false }).then(() => { console.log('unreachable') })
`, `AbortError ABORT_ERR The operation was aborted
AbortError why
ERR_INVALID_ARG_TYPE The "options" argument must be of type object. Received type number (5)
ERR_INVALID_ARG_TYPE The "options.ref" property must be of type boolean. Received type string ('no')`)
}

func TestE2ETimersPromisesInterval(t *testing.T) {
	assertOutputImports(t, `
import { setInterval as every } from 'timers/promises'
let n = 0
for await (const v of every(5, 'tick')) {
  console.log(v, ++n)
  if (n === 3) break
}
const ac = new AbortController()
let m = 0
try {
  for await (const v of every(5, m, { signal: ac.signal })) {
    if (++m === 2) ac.abort()
  }
} catch (e: any) { console.log('interval', e.name, m) }
`, "tick 1\ntick 2\ntick 3\ninterval AbortError 2")
}

func TestE2ETimersPromisesWeakOptionsTypeError(t *testing.T) {
	// tsc's TS2559: a primitive shares no property with a weak type.
	_, err := resolveAndCompile(t, `
import { setTimeout } from 'timers/promises'
await setTimeout(1, 'x', 5)
`)
	want := "type '5' has no properties in common with type 'TimerOptions'"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("want an error containing %q, got %v", want, err)
	}
}

// `timers` as a module (ADR-01280): every import form, extra callback
// arguments, clear* of undefined, and timers.promises.
func TestE2ETimersModule(t *testing.T) {
	assertOutputImports(t, `
import { setTimeout as later, clearTimeout, setInterval, clearInterval } from 'timers'
import timers from 'timers'
import * as ns from 'node:timers'
later((a: string, b: number) => { console.log('later', a, b) }, 5, 'x', 2)
const never = later(() => { console.log('never') }, 5)
clearTimeout(never)
clearTimeout(undefined)
let n = 0
const iv = setInterval(() => { n++; if (n === 2) { clearInterval(iv); console.log('interval', n) } }, 1)
console.log(typeof timers.setImmediate, typeof ns.clearImmediate, typeof ns.promises.setTimeout)
console.log(await timers.promises.setTimeout(20, 'promised'))
`, "function function function\ninterval 2\nlater x 2\npromised")
}
