package tests

import (
	"runtime"
	"strings"
	"testing"
)

// stack_overflow_contexts_windows_test.go — TDD-00249 on Windows: unbounded
// recursion is Node's RangeError on every stack that runs program code: the
// main thread, a generator, an async task, an HTTP connection fiber, a
// Worker thread and a goroutine (ADR-01605). POSIX's fiber and thread
// stacks are BACKLOG §0 (TDD-00249 Stage 2), so this runs on Windows.
func TestE2EStackOverflowEveryContextWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("POSIX thread and fiber stacks: TDD-00249 Stage 2")
	}
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
