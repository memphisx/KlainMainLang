package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// stack_overflow_contexts_test.go — TDD-00249: unbounded recursion is
// Node's RangeError on every stack that runs program code: the main
// thread, a generator, an async task, an HTTP connection fiber, a Worker
// thread and a goroutine (ADR-01605, ADR-01648).
func TestE2EStackOverflowEveryContext(t *testing.T) {
	skipUnderSanitizers(t)
	src := `
import http from 'node:http'
import { Worker, isMainThread, parentPort } from 'node:worker_threads'
import { go, Channel } from 'klain:sync'
function rec(n: number): number { return rec(n + 1) + 1 }
function tag(where: string, e: any): string { return where + " " + (e instanceof RangeError) + " " + e.message }
if (!isMainThread) {
  try { rec(0) } catch (e: any) { parentPort!.postMessage(tag("worker", e)) }
} else {
  try { rec(0) } catch (e: any) { console.log(tag("main", e)) }
  function* gen(): Generator<number> {
    try { rec(0) } catch (e: any) { console.log(tag("generator", e)) }
    yield 1
  }
  for (const v of gen()) {}
  const ch = new Channel<string>(0)
  go(() => { try { rec(0) } catch (e: any) { ch.send(tag("goroutine", e)) } })
  console.log(ch.receive())
  await (async () => {
    await null
    try { rec(0) } catch (e: any) { console.log(tag("task", e)) }
  })()
  const s = http.createServer((req, res) => {
    try { rec(0) } catch (e: any) { res.end(tag("handler", e)) }
  })
  s.listen(0, '127.0.0.1', async () => {
    const r = await fetch("http://127.0.0.1:" + (s.address() as any).port + "/")
    console.log(await r.text())
    s.close()
    const w = new Worker(__filename)
    w.on('message', (m: string) => { console.log(m) })
  })
}
`
	out := compileAndRunImports(t, src)
	for _, where := range []string{"main", "generator", "goroutine", "task", "handler", "worker"} {
		if !strings.Contains(out, where+" true Maximum call stack size exceeded") {
			t.Errorf("%s: no RangeError; output:\n%s", where, out)
		}
	}
}

// Under -mm=gc a generator's stack is guarded too, and a spent generator's
// stack and jmpbuf stack are reclaimed (ADR-01711).
func TestE2EStackOverflowGeneratorGC(t *testing.T) {
	bin := buildBinaryGC(t, `
function rec(n: number): number { return rec(n + 1) + 1 }
function* gen(): Generator<number> {
  try { rec(0) } catch (e: any) { console.log("generator", e instanceof RangeError, e.message) }
  yield 1
}
for (const v of gen()) {}
let s = 0
for (let i = 0; i < 100000; i++) {
  const g = (function* () { try { yield i } finally { s += 0 } })()
  s += g.next().value as number
}
console.log(s)
`)
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if want := "generator true Maximum call stack size exceeded\n4999950000\n"; string(out) != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}
