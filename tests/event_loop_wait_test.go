package tests

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// event_loop_wait_test.go — there is one event loop, and every wait either
// parks a coroutine or, at module top level, takes turns of that loop
// (TDD-00223). Each test pairs a slow upstream with a 25 ms `setInterval`: in
// Node the interval keeps ticking through the wait, so a wait that starves the
// loop (the old private drives: 0 ticks) is caught by the tick count, and a
// wait that spins is caught by the process's own CPU time.

const upstreamDelay = 600 * time.Millisecond

// minTicks is deliberately loose: 600 ms / 25 ms = 24 ideal ticks, but the
// Windows timer tick rounds each 25 ms wait up to ~31 ms, and a loaded CI
// machine adds jitter. A starved loop scores 0, so anything clearly above that
// proves the point without being a timing test.
const minTicks = 8

var ticksRe = regexp.MustCompile(`ticks=(\d+)`)

func ticksOf(t *testing.T, out string) int {
	t.Helper()
	m := ticksRe.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no ticks=N in output:\n%s", out)
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// runWaitProbe compiles src (which must print "body=… ticks=N"), runs it, and
// checks the body arrived, the interval kept ticking, and the wait did not spin.
func runWaitProbe(t *testing.T, src string) {
	t.Helper()
	bin := buildBinaryImports(t, src)
	cmd := exec.Command(bin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "body=upstream /slow") {
		t.Fatalf("response body missing:\n%s", s)
	}
	if n := ticksOf(t, s); n < minTicks {
		t.Errorf("the interval fired %d times during a %v wait (want >= %d): the wait starved the event loop", n, upstreamDelay, minTicks)
	}
	if cpu := cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime(); cpu > 350*time.Millisecond {
		t.Errorf("the process burned %v of CPU across a %v wait: the wait is spinning, not sleeping in the loop", cpu, upstreamDelay)
	}
}

const waitProbeHeader = `
let ticks = 0
const iv = setInterval(() => { ticks = ticks + 1 }, 25)
function done(body: string): void {
  console.log("body=" + body + " ticks=" + ticks)
  clearInterval(iv)
}
`

func TestE2ETopLevelAwaitFetchRunsTheLoop(t *testing.T) {
	up := newDelayedUpstreamServer(t, upstreamDelay)
	runWaitProbe(t, waitProbeHeader+fmt.Sprintf(`
const r = await fetch("%s/slow")
done(await r.text())
`, up.URL))
}

func TestE2EAsyncFunctionAwaitFetchSleepsInTheLoop(t *testing.T) {
	up := newDelayedUpstreamServer(t, upstreamDelay)
	runWaitProbe(t, waitProbeHeader+fmt.Sprintf(`
async function go(): Promise<void> {
  const r = await fetch("%s/slow")
  done(await r.text())
}
go()
`, up.URL))
}

func TestE2EAsyncArrowAwaitFetchIsACoroutine(t *testing.T) {
	up := newDelayedUpstreamServer(t, upstreamDelay)
	runWaitProbe(t, waitProbeHeader+fmt.Sprintf(`
const go = async (): Promise<void> => {
  const r = await fetch("%s/slow")
  done(await r.text())
}
go()
`, up.URL))
}

func TestE2EAsyncMethodAwaitFetchIsACoroutine(t *testing.T) {
	up := newDelayedUpstreamServer(t, upstreamDelay)
	runWaitProbe(t, waitProbeHeader+fmt.Sprintf(`
class Client {
  async run(): Promise<void> {
    const r = await fetch("%s/slow")
    done(await r.text())
  }
}
new Client().run()
`, up.URL))
}

// A timer callback may be async — the timer ignores the returned promise — and
// its await parks like any other.
func TestE2EAsyncTimerCallbackIsACoroutine(t *testing.T) {
	up := newDelayedUpstreamServer(t, upstreamDelay)
	runWaitProbe(t, waitProbeHeader+fmt.Sprintf(`
setTimeout(async () => {
  const r = await fetch("%s/slow")
  done(await r.text())
}, 5)
`, up.URL))
}

// `.then` on a fetch promise: the bridge parks on the transfer, r.text() is a
// real promise, and the callback's promise is flattened into the chain. (This
// exact shape used to crash: with no flattening the second callback received
// the inner promise's pointer as its "string".)
func TestE2EFetchThenChainRunsTheLoop(t *testing.T) {
	up := newDelayedUpstreamServer(t, upstreamDelay)
	runWaitProbe(t, waitProbeHeader+fmt.Sprintf(`
fetch("%s/slow").then((r) => r.text()).then((b: string) => { done(b) })
`, up.URL))
}

// A program that fetches from its own server, from inside a callback. The old
// synchronous drive never let the loop accept the connection: a deadlock.
func TestE2EFetchOwnServerFromCallback(t *testing.T) {
	assertOutputImports(t, `
import http from 'http'
const server = http.createServer((req, res) => { res.end('pong:' + req.url) })
server.listen(0, () => {
  const addr = server.address()
  fetch('http://127.0.0.1:' + addr.port + '/ping')
    .then((r) => r.text())
    .then((body: string) => { console.log(body); server.close() })
})
`, "pong:/ping")
}

// A top-level await on a timer promise is not a pause of the world: a child's
// output arriving meanwhile is delivered during the await, as in Node.
func TestE2ETopLevelAwaitServicesOtherSources(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH")
	}
	assertOutputImports(t, `
import { spawn } from 'node:child_process'
let got = ""
const c = spawn('sh', ['-c', 'echo child-data'])
c.stdout.on('data', (d: string) => { got = got + d })
await new Promise<void>((res) => { setTimeout(() => { res() }, 700) })
console.log("during-await=[" + got.trim() + "]")
`, "during-await=[child-data]")
}

// Node ends a process whose top-level await can never settle with a warning and
// exit code 13; it does not hang and it does not fall through.
func TestE2EUnsettledTopLevelAwaitExits13(t *testing.T) {
	bin := buildBinaryImports(t, `
const p = new Promise<number>((res) => { })
console.log("before")
const v = await p
console.log("never " + v)
`)
	cmd := exec.Command(bin)
	raw, _ := cmd.CombinedOutput() // stdout + the warning on stderr
	out := string(raw)
	if code := cmd.ProcessState.ExitCode(); code != 13 {
		t.Errorf("exit code = %d, want 13", code)
	}
	if !strings.Contains(out, "before") || strings.Contains(out, "never") {
		t.Errorf("unexpected stdout:\n%s", out)
	}
	if !strings.Contains(out, "unsettled top-level await") {
		t.Errorf("missing the unsettled-await warning:\n%s", out)
	}
}

// An async arrow returns its promise at its first await — the caller continues,
// the rest of the body runs later — exactly the order a JS engine produces.
func TestE2EAsyncArrowReturnsAtFirstAwait(t *testing.T) {
	assertOutputImports(t, `
const f = async (tag: string): Promise<string> => {
  console.log(tag + " start")
  await null
  console.log(tag + " after-await")
  return tag + "!"
}
console.log("A")
const p = f("x")
console.log("B")
p.then((v: string) => { console.log("then " + v) })
console.log("C")
`, "A\nx start\nB\nC\nx after-await\nthen x!")
}

// A `.then` callback that returns a promise resolves the chain *with* it.
func TestE2EThenFlattensAReturnedPromise(t *testing.T) {
	assertOutputImports(t, `
function later(ms: number, v: number): Promise<number> {
  return new Promise<number>((res) => { setTimeout(() => { res(v) }, ms) })
}
console.log("start")
Promise.resolve(1).then((a: number) => later(60, a + 41)).then((v: number) => { console.log("flattened " + v) })
later(10, 7).then((v: number) => { console.log("first " + v) })
`, "start\nfirst 7\nflattened 42")
}

// Reactions on one promise run in the order they were registered.
func TestE2EThenReactionsRunInRegistrationOrder(t *testing.T) {
	assertOutputImports(t, `
function later(ms: number, v: number): Promise<number> {
  return new Promise<number>((res) => { setTimeout(() => { res(v) }, ms) })
}
const p = later(20, 1)
p.then((v: number) => { console.log("A " + v) })
p.then((v: number) => { console.log("B " + v) })
p.then((v: number) => { console.log("C " + v) })
`, "A 1\nB 1\nC 1")
}

// A top-level binding initialised by a builtin call is a module global like any
// other: a named function can read it (`Date.now()`), and clear it
// (`setInterval`). These were "undefined variable" compile errors.
func TestE2ETopLevelBuiltinCallBindingIsVisibleInFunctions(t *testing.T) {
	assertOutputImports(t, `
const t0 = Date.now()
const n = parseInt("41")
const s = "abc".toUpperCase()
let fired = 0
const iv = setInterval(() => { fired = fired + 1; if (fired === 2) { stop() } }, 10)
function stop(): void { clearInterval(iv); console.log("stopped after " + fired) }
function show(): void { console.log((Date.now() - t0 >= 0) + " " + (n + 1) + " " + s) }
show()
`, "true 42 ABC\nstopped after 2")
}
