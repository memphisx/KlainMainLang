package tests

import (
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// event_loop_uv_test.go — the event loop on libuv (TDD-00244). Each test
// asserts structure, never a count of timing events.

// No timer fires before its delay, to within the millisecond: as in Node, a
// timer counts from the loop's millisecond clock (ADR-01411), so against
// Date.now() — another clock, with its own millisecond boundaries — it may
// run up to 1 ms early, never more.
func TestE2ELoopTimerNeverEarly(t *testing.T) {
	assertOutput(t, `
const start = Date.now()
const delays = [1, 10, 30, 50]
let early = 0
let fired = 0
for (const d of delays) {
  setTimeout(() => {
    if (Date.now() - start < d - 1) early = early + 1
    fired = fired + 1
    if (fired === delays.length) console.log("early=" + early)
  }, d)
}
`, "early=0\n")
}

// Every timer due by the timers phase fires in that phase, in due order, with
// process.nextTick and microtasks drained after each callback (Node >= 11).
func TestE2ELoopDueTimersFireTogether(t *testing.T) {
	assertOutput(t, `
setTimeout(() => { console.log("a"); Promise.resolve().then(() => console.log("a-micro")) }, 5)
setTimeout(() => { console.log("b"); process.nextTick(() => console.log("b-tick")) }, 5)
setTimeout(() => console.log("c"), 5)
const start = Date.now()
while (Date.now() - start < 20) {}
`, "a\na-micro\nb\nb-tick\nc\n")
}

// A process waiting on a timer sleeps.
func TestE2ELoopWaitSleeps(t *testing.T) {
	bin := buildBinaryImports(t, `
setTimeout(() => console.log("done"), 500)
`)
	cmd := exec.Command(bin)
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "done\n" {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if cpu := cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime(); cpu > 150*time.Millisecond {
		t.Errorf("the process burned %v of CPU waiting 500ms on a timer: the wait spins", cpu)
	}
}

// A server and its clients above descriptor 1024: the loop has no FD_SETSIZE
// ceiling.
func TestE2ELoopDescriptorsAbove1024(t *testing.T) {
	bin := buildBinaryImports(t, `
import * as fs from "fs"
import * as os from "os"
import * as http from "http"
import * as net from "net"
const fds: number[] = []
for (let i = 0; i < 1100; i++) fds.push(fs.openSync(os.devNull, "r"))
const server = http.createServer((req, res) => { res.end("hello") })
server.listen(0, '127.0.0.1', () => {
  const port = (server.address() as any).port
  const s = net.connect(port, "127.0.0.1", () => {
    s.write("GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
  })
  let got = ""
  s.on("data", (d) => { got += d.toString() })
  s.on("end", () => { console.log("ok=" + got.endsWith("hello")); server.close() })
})
`)
	out, err := aboveFdLimit(bin).CombinedOutput()
	if err != nil || string(out) != "ok=true\n" {
		t.Fatalf("run: %v\n%s", err, out)
	}
}

// fetch above descriptor 1024: libcurl is driven through its socket API, so
// the loop polls its sockets with no FD_SETSIZE ceiling.
func TestE2ELoopFetchAbove1024(t *testing.T) {
	bin := buildBinaryImports(t, `
import * as fs from "fs"
import * as os from "os"
import * as http from "http"
const fds: number[] = []
for (let i = 0; i < 1100; i++) fds.push(fs.openSync(os.devNull, "r"))
const server = http.createServer((req, res) => { res.end("hello") })
server.listen(0, '127.0.0.1', async () => {
  const port = (server.address() as any).port
  const r = await fetch("http://127.0.0.1:" + port + "/")
  console.log("body=" + (await r.text()))
  server.close()
})
`)
	out, err := aboveFdLimit(bin).CombinedOutput()
	if err != nil || dropSanitizerNotes(string(out)) != "body=hello\n" {
		t.Fatalf("run: %v\n%s", err, out)
	}
}

// aboveFdLimit runs bin allowed 4096 descriptors: POSIX raises the soft
// limit first; Windows has none to raise (the CRT's is 8192).
func aboveFdLimit(bin string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command(bin)
	}
	return exec.Command("sh", "-c", "ulimit -n 4096 && exec "+bin)
}
