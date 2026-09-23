package tests

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// startHTTPServer compiles src (expected to call http.listen and never
// return) and runs it as a background process, waiting for the given port
// to accept TCP connections before returning. The process is killed via
// t.Cleanup regardless of test outcome, since http.listen's own process
// never exits on its own.
// freePort asks the OS for an unused loopback TCP port. Pure test-harness
// plumbing (Go's net package) — the compiled program still calls the real
// http.listen(<port>, cb); only the literal it binds is chosen here, a fresh one
// per server, so parallel test shards never collide on a shared hardcoded port
// (which used to make the readiness poll connect to another shard's server).
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return p
}

// subPort rewrites every occurrence of the placeholder port literal in a source
// string to the actually-allocated one, so a test keeps a readable literal in
// its program text while the harness binds a free port underneath.
func subPort(src string, from, to int) string {
	return strings.ReplaceAll(src, fmt.Sprintf("%d", from), fmt.Sprintf("%d", to))
}

// waitListening polls until the server accepts a TCP connection on port. The
// deadline is generous (15s) so CPU-saturated parallel shards don't trip it.
func waitListening(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server never started listening on %s", addr)
}

// startHTTPServer allocates a free port, substitutes it for the placeholder
// `port` literal in src, starts the server, waits for it to listen, and returns
// the actual port for the caller's client requests.
func startHTTPServer(t *testing.T, src string, port int) int {
	t.Helper()
	np := freePort(t)
	startHTTPServerFixed(t, subPort(src, port, np), np)
	return np
}

// startHTTPServerFixed runs src as a background server on exactly `port` (no
// substitution) and waits for readiness. For tests that need a specific port —
// e.g. the bind-failure test that starts a second instance on an already-bound
// port.
func startHTTPServerFixed(t *testing.T, src string, port int) {
	t.Helper()
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	// Keep what the server printed: a server that never came up (a bind error,
	// a crash at start) is otherwise undiagnosable from the readiness timeout.
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.WaitDelay = 2 * time.Second // a grandchild holding the pipe must not hang Wait
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	// Registered first, so it runs last — after the kill + Wait below, when no
	// goroutine is still writing to out.
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("server process state: %v\nserver output:\n%s", cmd.ProcessState, out.String())
		}
	})
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	waitListening(t, port)
}

// startHTTPServerGC is startHTTPServer's -mm=gc counterpart, for exercising
// http.listen's concurrent-fiber machinery under the Boehm GC (see
// docs/adr/ADR-00071.md's GC_stackbottom fix) — skips (via buildBinaryGC)
// if libgc/bdw-gc isn't installed.
func startHTTPServerGC(t *testing.T, src string, port int) int {
	t.Helper()
	np := freePort(t)
	binFile := buildBinaryGCImports(t, subPort(src, port, np))
	cmd := exec.Command(binFile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	waitListening(t, np)
	return np
}

// waitPortFree polls addr for up to 2s, returning once connections are
// actively refused (the port is genuinely free) rather than just "the one
// process exec.Command started is gone." Used by the cluster-server test
// helpers' t.Cleanup: syscall.Kill(-pgid, ...) + cmd.Wait() only confirms
// the *original* process has been reaped — a forked worker sharing that
// same process group can take a little longer to actually have its socket
// torn down at the kernel level, a real race found via a stale-server
// investigation (a bind failure race, not a language/runtime bug): without
// this wait, the next test using the same hardcoded port could start
// *before* the previous run's listener is actually gone, silently talking
// to stale, possibly already-degraded (workers missing) processes instead
// of its own freshly-compiled one.
func waitPortFree(addr string) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err != nil {
			return
		}
		conn.Close()
		time.Sleep(20 * time.Millisecond)
	}
}

// startHTTPClusterServer is startHTTPServer's http.listen({ workers: N })
// counterpart (TDD-00025): the compiled binary forks N-1 additional worker
// processes sharing one listening socket, so cleanup has to kill the whole
// process group, not just the one PID exec.Command started — plain
// cmd.Process.Kill() only reaches the original process, leaving every
// forked worker running (and, since the test binary itself doesn't reap
// them, orphaned). Setpgid at Start time gives the server (and, since
// fork() doesn't change process group by default, every worker it spawns)
// its own group, separate from the test binary's own — signaling -pgid
// reaches all of them in one call.
func startHTTPClusterServer(t *testing.T, src string, port int) int {
	t.Helper()
	np := freePort(t)
	binFile := buildBinaryImports(t, subPort(src, port, np))
	cmd := exec.Command(binFile)
	setProcGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", np)
	t.Cleanup(func() {
		killProcGroup(cmd)
		_ = cmd.Wait()
		waitPortFree(addr)
	})
	waitListening(t, np)
	return np
}

// TestE2EHTTPClusterWorkersExitWithPrimary: a { workers: N } worker does not
// outlive its primary. Node's cluster worker exits when its channel to the
// primary disconnects; workers left behind keep the shared listener — and so
// the port — open for good. Only the primary is killed here (no group kill);
// the port has to stop accepting once the workers notice.
func TestE2EHTTPClusterWorkersExitWithPrimary(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8977, (req: HttpRequest): Res => {
  return { status: 200, body: "ok" }
}, { workers: 3 })
`
	np := freePort(t)
	binFile := buildBinaryImports(t, subPort(src, 8977, np))
	cmd := exec.Command(binFile)
	setProcGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", np)
	t.Cleanup(func() {
		killProcGroup(cmd)
		_ = cmd.Wait()
		waitPortFree(addr)
	})
	waitListening(t, np)
	// Let every worker reach its event loop before the primary goes.
	time.Sleep(500 * time.Millisecond)

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill primary: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatalf("%s still accepts 10s after the primary was killed: workers outlived it", addr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// startHTTPClusterServerGC is startHTTPClusterServer's -mm=gc counterpart —
// see ADR-00099 for what changed in gcshim.c to make Boehm GC safe across
// http.listen's clustering fork() (GC_set_handle_fork(1) before GC_INIT()).
// Skips (via buildBinaryGC) if libgc/bdw-gc isn't installed, same as
// startHTTPServerGC.
func startHTTPClusterServerGC(t *testing.T, src string, port int) int {
	t.Helper()
	np := freePort(t)
	binFile := buildBinaryGCImports(t, subPort(src, port, np))
	cmd := exec.Command(binFile)
	setProcGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", np)
	t.Cleanup(func() {
		killProcGroup(cmd)
		_ = cmd.Wait()
		waitPortFree(addr)
	})
	waitListening(t, np)
	return np
}

func TestE2EHTTPCreateServerNodeShape(t *testing.T) {
	// TDD-00131: real Node http.createServer((req, res) => …).listen(port), with
	// res.writeHead(status, headers) / res.setHeader / res.write / res.end and
	// req.method access.
	src := `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.setHeader("X-Method", req.method)
  res.writeHead(201, { "Content-Type": "text/plain" })
  res.write("part1;")
  res.end("part2")
}).listen(8955)
`
	port := startHTTPServer(t, src, 8955)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Errorf("status: got %d, want 201", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain" {
		t.Errorf("Content-Type: got %q, want text/plain", ct)
	}
	if xm := resp.Header.Get("X-Method"); xm != "GET" {
		t.Errorf("X-Method: got %q, want GET", xm)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "part1;part2" {
		t.Errorf("body: got %q, want %q", string(body), "part1;part2")
	}
}

func TestE2EHTTPCreateServerNoopOptions(t *testing.T) {
	// ADR-00977: createServer accepts every option whose value states this
	// dispatcher's fixed behavior — a portable no-op. A Node app passing the
	// disabled/default values compiles and serves normally.
	src := `
import http from 'http'
http.createServer(
  { noDelay: true, keepAlive: false, requestTimeout: 0, headersTimeout: 0,
    maxRequestsPerSocket: 0, insecureHTTPParser: false, requireHostHeader: false },
  (req: IncomingMessage, res: ServerResponse) => {
    res.writeHead(200, { "Content-Type": "text/plain" })
    res.end("ok")
  }).listen(8971)
`
	port := startHTTPServer(t, src, 8971)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body: got %q, want %q", string(body), "ok")
	}
}

func TestE2EHTTPCreateServerOptionRejections(t *testing.T) {
	// ADR-00977: a value that would change behavior — or an unrecognized option —
	// is a clean rejection, never a silent ignore.
	cases := []struct{ opt, want string }{
		{"noDelay: false", "noDelay option supports only the literal true"},
		{"keepAlive: true", "keepAlive option supports only the literal false"},
		{"maxRequestsPerSocket: 5", "maxRequestsPerSocket option supports only the literal 0"},
		{`highWaterMark: "big"`, "highWaterMark option must be a compile-time integer"},
		{`requestTimeout: "x"`, "requestTimeout must be a compile-time integer"},
		{`headersTimeout: "y"`, "headersTimeout must be a compile-time integer"},
		{"maxHeaderSize: 100", "createServer option 'maxHeaderSize' is not supported"},
	}
	for _, tc := range cases {
		src := fmt.Sprintf(`
import http from 'http'
http.createServer({ %s }, (req: IncomingMessage, res: ServerResponse) => { res.end("x") }).listen(0)
`, tc.opt)
		_, err := resolveAndCompile(t, src)
		if err == nil {
			t.Errorf("{ %s }: expected a compile error, got none", tc.opt)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("{ %s }: error %q does not contain %q", tc.opt, err.Error(), tc.want)
		}
	}
}

func TestE2EHTTPCreateServerStatusCodeSetter(t *testing.T) {
	// TDD-00131 / ADR-00513: the imperative `res.statusCode = N` setter (Node's
	// alternative to writeHead(status)) drives the response status, and reading
	// `res.statusCode` back returns what was set — used here to echo it in the body.
	src := `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.statusCode = 404
  res.setHeader("Content-Type", "text/plain")
  const code = res.statusCode
  res.end("code=" + code)
}).listen(8957)
`
	port := startHTTPServer(t, src, 8957)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "code=404" {
		t.Errorf("body: got %q, want %q", string(body), "code=404")
	}
}

func TestE2EHTTPCreateServerNonBlockingListen(t *testing.T) {
	// TDD-00131 / ADR-00514: `server.listen(...)` is non-blocking — top-level
	// code after it runs before the event loop drives requests (Node's
	// listen-then-continue). Proven through the response: `phase` is mutated
	// *after* listen(); a blocking listen would never run that mutation, so the
	// handler would serve "before". Non-blocking → the handler serves "after".
	src := `
import http from 'http'
let phase = "before"
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200)
  res.end(phase)
}).listen(8959)
phase = "after"
`
	port := startHTTPServer(t, src, 8959)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "after" {
		t.Errorf("body: got %q, want %q (non-blocking listen should run the post-listen mutation)", string(body), "after")
	}
}

func TestE2EHTTPCreateServerWriteBoolAndCork(t *testing.T) {
	// TDD-00131 / ADR-00514: the buffered-sink Writable surface — res.write
	// returns Node's boolean (always true here, no backpressure), and
	// res.cork()/uncork() are accepted no-op hints.
	src := `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.cork()
  const ok = res.write("a;")
  res.uncork()
  res.end(ok ? "wrote" : "full")
}).listen(8961)
`
	port := startHTTPServer(t, src, 8961)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "a;wrote" {
		t.Errorf("body: got %q, want %q", string(body), "a;wrote")
	}
}

func TestE2EHTTPCreateServerResIncrementalStream(t *testing.T) {
	// TDD-00195 Stage 2: the incremental `res` Writable — the first res.write
	// takes the socket over for chunked transfer (each chunk framed to the fd
	// mid-handler, not buffered until return), and res.end closes it. The
	// response is Transfer-Encoding: chunked and reassembles to the joined chunks.
	src := `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200, { "Content-Type": "text/plain" })
  res.write("one;")
  res.write("two;")
  res.end("three")
}).listen(8993)
`
	port := startHTTPServer(t, src, 8993)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	// Go's client exposes chunked responses as TransferEncoding ["chunked"] and
	// strips the header from resp.Header — assert the streamed framing that way.
	if len(resp.TransferEncoding) != 1 || resp.TransferEncoding[0] != "chunked" {
		t.Errorf("TransferEncoding: got %v, want [chunked]", resp.TransferEncoding)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "one;two;three" {
		t.Errorf("body: got %q, want %q", string(body), "one;two;three")
	}
}

func TestE2EHTTPCreateServerResWriteBinary(t *testing.T) {
	// TDD-00195 Stage 2: a binary chunk (Uint8Array/Buffer) passed to res.write /
	// res.end is framed as its raw bytes, not stringified. The first chunk (via
	// res.write) streams chunked; the second (via res.end) closes it. An embedded
	// NUL must survive — a stringified path would truncate there.
	src := `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200, { "Content-Type": "application/octet-stream" })
  const a = new Uint8Array(3)
  a[0] = 5; a[1] = 0; a[2] = 200
  res.write(a)
  const b = new Uint8Array(2)
  b[0] = 1; b[1] = 255
  res.end(b)
}).listen(8994)
`
	port := startHTTPServer(t, src, 8994)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	want := []byte{5, 0, 200, 1, 255}
	if !bytes.Equal(body, want) {
		t.Errorf("body: got %v, want %v", body, want)
	}
}

func TestE2EHTTPCreateServerResEndBinaryBuffered(t *testing.T) {
	// TDD-00195 Stage 2: res.end(buf) with no prior res.write stays on the
	// buffered (Content-Length) path but still frames the raw bytes, embedded NUL
	// included, rather than stringifying the array.
	src := `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200, { "Content-Type": "application/octet-stream" })
  const b = new Uint8Array(4)
  b[0] = 7; b[1] = 0; b[2] = 9; b[3] = 250
  res.end(b)
}).listen(8998)
`
	port := startHTTPServer(t, src, 8998)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if len(resp.TransferEncoding) != 0 {
		t.Errorf("TransferEncoding: got %v, want [] (Content-Length path)", resp.TransferEncoding)
	}
	body, _ := io.ReadAll(resp.Body)
	want := []byte{7, 0, 9, 250}
	if !bytes.Equal(body, want) {
		t.Errorf("body: got %v, want %v", body, want)
	}
}

func TestE2EHTTPCreateServerResStreamBackpressure(t *testing.T) {
	// TDD-00195 Stage 2 (Layer A backpressure): a slow-reading client on a large
	// streamed response must NOT stall the whole reactor. The handler for "/big"
	// writes ~2 MB in 1 KB chunks; a raw client reads a little then stops, so the
	// server's send buffer fills and res.write hits EAGAIN. With park-on-writable
	// that fiber suspends and the reactor keeps serving — a second client's quick
	// "/" request completes promptly. A blocking write() would hang the reactor
	// here and the quick request would never return.
	kb := strings.Repeat("x", 1024)
	src := `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  if (req.url === "/big") {
    res.writeHead(200)
    let i = 0
    while (i < 2048) {
      res.write("` + kb + `")
      i = i + 1
    }
    res.end()
  } else {
    res.writeHead(200)
    res.end("quick")
  }
}).listen(8999)
`
	port := startHTTPServer(t, src, 8999)

	// Client A: request /big, read a little, then hold the connection open
	// without draining so the server parks mid-stream.
	connA, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	defer connA.Close()
	if _, err := connA.Write([]byte("GET /big HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatalf("write A: %v", err)
	}
	buf := make([]byte, 4096)
	connA.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := connA.Read(buf); err != nil {
		t.Fatalf("read A: %v", err)
	}
	// Give the server a moment to fill A's socket buffer and park.
	time.Sleep(300 * time.Millisecond)

	// Client B: a quick request must complete while A is parked.
	done := make(chan string, 1)
	go func() {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err != nil {
			done <- "ERR:" + err.Error()
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		done <- string(body)
	}()
	select {
	case got := <-done:
		if got != "quick" {
			t.Errorf("quick request while a slow stream is parked: got %q, want %q", got, "quick")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("quick request hung — the reactor was blocked by the slow client's stream (no backpressure park)")
	}
}

func TestE2EHTTPCreateServerResStreamLargeBodyIntact(t *testing.T) {
	// TDD-00195 Stage 2 (Layer A backpressure): a large streamed body read fully
	// by the client arrives byte-for-byte intact — the non-blocking write path
	// must resume and finish every chunk on EAGAIN, never drop bytes.
	src := `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200, { "Content-Type": "text/plain" })
  let i = 0
  while (i < 4096) {
    res.write("0123456789ABCDEF")
    i = i + 1
  }
  res.end()
}).listen(9001)
`
	port := startHTTPServer(t, src, 9001)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	want := strings.Repeat("0123456789ABCDEF", 4096)
	if len(body) != len(want) {
		t.Fatalf("body length: got %d, want %d", len(body), len(want))
	}
	if string(body) != want {
		t.Errorf("streamed body corrupted (first mismatch matters)")
	}
}

func TestE2EHTTPCreateServerResWriteBackpressureDrain(t *testing.T) {
	// TDD-00214 (Layer B, observable backpressure): a Node-faithful producer that
	// writes only while res.write() returns true and resumes on 'drain' must
	// deliver the whole body to a slow-reading client. res.write returns false
	// once the queued size crosses highWaterMark; the queue is drained by the
	// reactor and 'drain' fires asynchronously to resume the producer. If either
	// the false signal or the 'drain' event were broken, the producer would stall
	// and the body would arrive truncated (or the read would hang).
	const chunkBytes = 65536
	const totalChunks = 128 // 8 MiB
	src := `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200)
  const chunk = "0123456789ABCDEF".repeat(4096) // 64 KiB
  let n = 0
  const pump = () => {
    let ok = true
    while (n < 128 && ok) {
      ok = res.write(chunk)
      n = n + 1
    }
    if (n >= 128) { res.end(); return }
    res.once('drain', pump)   // resume only when the buffer drains
  }
  pump()
}).listen(8129)
`
	port := startHTTPServer(t, src, 8129)

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatalf("write req: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	br := bufio.NewReader(conn)
	// Skip the status line + headers.
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read headers: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}
	// De-chunk the body, reading deliberately slowly at the start so the server's
	// send buffer fills and it genuinely hits backpressure.
	bodyLen := 0
	reads := 0
	for {
		sizeLine, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read chunk size: %v", err)
		}
		var sz int64
		if _, err := fmt.Sscanf(strings.TrimSpace(sizeLine), "%x", &sz); err != nil {
			t.Fatalf("parse chunk size %q: %v", sizeLine, err)
		}
		if sz == 0 {
			break
		}
		buf := make([]byte, sz)
		if _, err := io.ReadFull(br, buf); err != nil {
			t.Fatalf("read chunk body: %v", err)
		}
		if _, err := br.Discard(2); err != nil { // trailing CRLF
			t.Fatalf("discard chunk CRLF: %v", err)
		}
		bodyLen += int(sz)
		reads++
		if bodyLen < 400000 {
			time.Sleep(3 * time.Millisecond) // pace the early reads → force EAGAIN
		}
	}
	want := chunkBytes * totalChunks
	if bodyLen != want {
		t.Fatalf("de-chunked body length: got %d, want %d (drain-gated producer stalled → false/'drain' broken)", bodyLen, want)
	}
}

func TestE2EHTTPCreateServerHighWaterMark(t *testing.T) {
	// ADR-00983: createServer's `{ highWaterMark: N }` option threads a per-server
	// backpressure threshold into every response (default 16384). Here a *tiny*
	// non-default threshold (256 bytes) is configured: res.write returns false as
	// soon as more than 256 unsent bytes are queued, so the drain-gated producer
	// parks almost immediately and resumes on 'drain'. The whole 8 MiB body must
	// still arrive intact — proving the configured value is honored by the shared
	// __kml_res_qwrite (a broken thread-through would either not compile, or run
	// the queue past 256 and never fire 'drain', truncating the body).
	const chunkBytes = 65536
	const totalChunks = 128 // 8 MiB
	src := `
import http from 'http'
http.createServer({ highWaterMark: 256 }, (req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200)
  const chunk = "0123456789ABCDEF".repeat(4096) // 64 KiB
  let n = 0
  const pump = () => {
    let ok = true
    while (n < 128 && ok) {
      ok = res.write(chunk)
      n = n + 1
    }
    if (n >= 128) { res.end(); return }
    res.once('drain', pump)
  }
  pump()
}).listen(8130)
`
	port := startHTTPServer(t, src, 8130)

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatalf("write req: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	br := bufio.NewReader(conn)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read headers: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}
	bodyLen := 0
	for {
		sizeLine, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read chunk size: %v", err)
		}
		var sz int64
		if _, err := fmt.Sscanf(strings.TrimSpace(sizeLine), "%x", &sz); err != nil {
			t.Fatalf("parse chunk size %q: %v", sizeLine, err)
		}
		if sz == 0 {
			break
		}
		buf := make([]byte, sz)
		if _, err := io.ReadFull(br, buf); err != nil {
			t.Fatalf("read chunk body: %v", err)
		}
		if _, err := br.Discard(2); err != nil {
			t.Fatalf("discard chunk CRLF: %v", err)
		}
		bodyLen += int(sz)
		if bodyLen < 400000 {
			time.Sleep(3 * time.Millisecond)
		}
	}
	want := chunkBytes * totalChunks
	if bodyLen != want {
		t.Fatalf("de-chunked body length: got %d, want %d (custom highWaterMark broke the drain loop)", bodyLen, want)
	}
}

func TestE2EHTTPCreateServerConnectionTimeouts(t *testing.T) {
	// TDD-00217: a positive headersTimeout/requestTimeout/keepAliveTimeout is
	// enforced by the reactor. A slow client that never completes its request
	// headers is closed at headersTimeout; a slow request body is closed at
	// requestTimeout; an idle kept-alive connection is closed at keepAliveTimeout —
	// and a normal request is unaffected, before and after each abort.
	src := `
import http from 'http'
http.createServer(
  { headersTimeout: 300, requestTimeout: 900, keepAliveTimeout: 400 },
  (req: IncomingMessage, res: ServerResponse) => {
    res.writeHead(200)
    res.end("ok")
  }).listen(8131)
`
	port := startHTTPServer(t, src, 8131)

	// A normal request works.
	if resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port)); err != nil {
		t.Fatalf("initial GET: %v", err)
	} else {
		resp.Body.Close()
	}

	// closedWithin dials, sends `req` (no completion), and asserts the server
	// closes the connection (recv → EOF) within [min,max] seconds.
	closedWithin := func(name, req string, min, max float64) {
		conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatalf("%s dial: %v", name, err)
		}
		defer conn.Close()
		if _, err := conn.Write([]byte(req)); err != nil {
			t.Fatalf("%s write: %v", name, err)
		}
		conn.SetReadDeadline(time.Now().Add(time.Duration(max+1.5) * time.Second))
		start := time.Now()
		buf := make([]byte, 256)
		// Drain any response bytes until EOF (the server closes on timeout).
		for {
			if _, err := conn.Read(buf); err != nil {
				break // EOF / reset → connection closed
			}
		}
		elapsed := time.Since(start).Seconds()
		if elapsed < min || elapsed > max {
			t.Errorf("%s: connection closed after %.2fs, want [%.2f, %.2f]", name, elapsed, min, max)
		}
	}

	// Incomplete headers → headersTimeout (~0.3s).
	closedWithin("headersTimeout", "GET /slow HTTP/1.1\r\nHost: x\r\n", 0.15, 0.7)
	// Headers complete but body never finishes → requestTimeout (~0.9s).
	closedWithin("requestTimeout",
		"POST /b HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\nAB", 0.6, 1.4)

	// Idle keep-alive → keepAliveTimeout (~0.4s after the response is read).
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("ka dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET /k HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatalf("ka write: %v", err)
	}
	br := bufio.NewReader(conn)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("ka read headers: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}
	// The 2-byte "ok" body has no Content-Length? It does (res.end sends one), so
	// read it, then time the idle close.
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	start := time.Now()
	buf := make([]byte, 256)
	for {
		if _, err := conn.Read(buf); err != nil {
			break
		}
	}
	if elapsed := time.Since(start).Seconds(); elapsed > 1.2 {
		t.Errorf("keepAliveTimeout: idle connection closed after %.2fs, want < ~1.2", elapsed)
	}

	// The server is still healthy after all the aborts.
	if resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port)); err != nil {
		t.Fatalf("final GET (server died?): %v", err)
	} else {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "ok" {
			t.Errorf("final body: got %q, want ok", string(body))
		}
	}
}

func TestE2EHTTPCreateServerResClientAbortSurvives(t *testing.T) {
	// TDD-00214 (SIGPIPE): a client that disconnects mid-stream must not kill the
	// server. SIGPIPE is ignored process-wide, so the write() to the dead socket
	// returns EPIPE and the connection unwinds; a subsequent request still works.
	src := `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200)
  let i = 0
  while (i < 4096) {
    res.write("0123456789ABCDEF")
    i = i + 1
  }
  res.end()
}).listen(8128)
`
	port := startHTTPServer(t, src, 8128)

	// Client that reads a little then abruptly closes mid-stream.
	connA, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	if _, err := connA.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatalf("write A: %v", err)
	}
	buf := make([]byte, 512)
	connA.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := connA.Read(buf); err != nil {
		t.Fatalf("read A: %v", err)
	}
	connA.Close() // abrupt disconnect → server write() gets EPIPE
	time.Sleep(200 * time.Millisecond)

	// The server must still be alive and serving.
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("follow-up GET (server died on SIGPIPE?): %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	want := strings.Repeat("0123456789ABCDEF", 4096)
	if len(body) != len(want) {
		t.Fatalf("follow-up body length: got %d, want %d", len(body), len(want))
	}
}

func TestE2EHTTPCreateServerResStreamKeepAlive(t *testing.T) {
	// TDD-00195 Stage 2: a streamed response re-arms the connection on keep-alive,
	// so two sequential requests on one reused connection both stream correctly.
	src := `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200)
  res.write("chunk-")
  res.end("end")
}).listen(8995)
`
	port := startHTTPServer(t, src, 8995)
	client := &http.Client{} // default transport reuses keep-alive connections
	for i := 0; i < 2; i++ {
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err != nil {
			t.Fatalf("GET %d: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "chunk-end" {
			t.Errorf("req %d body: got %q, want %q", i, string(body), "chunk-end")
		}
	}
}

func TestE2EHTTPCreateServerReqPipeRes(t *testing.T) {
	// TDD-00195 Stage 2: req.pipe(res) — a server req (Node Readable) piped into a
	// res (Node Writable) echoes the request body straight back to the client.
	src := `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200, { "Content-Type": "application/octet-stream" })
  req.pipe(res)
}).listen(8996)
`
	port := startHTTPServer(t, src, 8996)
	payload := "the quick brown fox jumps over the lazy dog"
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/", port), "text/plain", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != payload {
		t.Errorf("echoed body: got %q, want %q", string(body), payload)
	}
}

func TestE2EHTTPCreateServerResWritePipeInterleave(t *testing.T) {
	// TDD-00195 Stage 2: a raw res.write and req.pipe(res) on ONE response share a
	// single per-response output queue, so their bytes leave the socket in call
	// order. Here the handler writes a prefix directly, then pipes the request
	// body; the client must see the prefix before the echoed body. Before the
	// unification the direct write queued into res.__kml_outq while the pipe sink
	// wrote straight to the socket, so the two could interleave out of order.
	src := `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200, { "Content-Type": "application/octet-stream" })
  res.write("PREFIX:")
  req.pipe(res)
}).listen(8994)
`
	port := startHTTPServer(t, src, 8994)
	payload := "the quick brown fox"
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/", port), "text/plain", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	want := "PREFIX:" + payload
	if string(body) != want {
		t.Errorf("interleaved res.write + req.pipe body: got %q, want %q", string(body), want)
	}
}

func TestE2EHTTPCreateServerBoundHandle(t *testing.T) {
	// Variable-bound http.createServer handle (the standard Node idiom, as
	// opposed to the chained createServer(cb).listen(port) expression): the
	// server is bound to a const and .listen() is a later statement.
	src := `
import http from 'http'
const server = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200)
  res.end("pong:" + req.path)
})
server.listen(8973)
`
	port := startHTTPServer(t, src, 8973)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/x", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "pong:/x" {
		t.Errorf("body: got %q, want %q", string(body), "pong:/x")
	}
}

func TestE2EHTTPCreateServerOnRequestUntyped(t *testing.T) {
	// Zero-arg createServer + server.on('request', …) registration, with the
	// handler params left untyped (contextually typed IncomingMessage /
	// ServerResponse, as real Node infers them), via the named-import form.
	src := `
import { createServer } from 'http'
const server = createServer()
server.on('request', (req, res) => {
  res.writeHead(200)
  res.end("on:" + req.path)
})
server.listen(8972)
`
	port := startHTTPServer(t, src, 8972)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/y", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "on:/y" {
		t.Errorf("body: got %q, want %q", string(body), "on:/y")
	}
}

func TestE2EHTTPCreateServerEphemeralPortAndClose(t *testing.T) {
	// listen(0) binds an ephemeral port, server.address().port reports the real
	// one, and server.close() from inside the loop lets control continue past
	// the blocking listen call.
	src := `
import http from 'http'
const server = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.end("unused")
})
server.listen(0, () => {
  const port: number = server.address().port
  if (port > 0) { console.log("got ephemeral port") }
  setTimeout(() => { server.close() }, 20)
})
console.log("after loop")
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "got ephemeral port") {
		t.Errorf("address().port was not positive: %q", out)
	}
	if !strings.Contains(out, "after loop") {
		t.Errorf("control never continued past server.close(): %q", out)
	}
}

func TestE2EHTTPServerListenOptionsObject(t *testing.T) {
	// server.listen({ port, host, backlog }, cb) — Node's options-object form
	// (ADR-00801). host: "127.0.0.1" actually binds loopback, which
	// server.address().address reflects via getsockname; backlog is accepted.
	src := `
import http from 'http'
const server = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.end("unused")
})
server.listen({ port: 0, host: "127.0.0.1", backlog: 64 }, () => {
  const a = server.address()
  if (a.port > 0) { console.log("addr=" + a.address) }
  server.close()
})
console.log("after loop")
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "addr=127.0.0.1") {
		t.Errorf("host option did not bind loopback: %q", out)
	}
	if !strings.Contains(out, "after loop") {
		t.Errorf("control never continued: %q", out)
	}
}

func TestE2EHTTPServerListenOptionsDefaultHost(t *testing.T) {
	// An options literal with no host binds INADDR_ANY (0.0.0.0), same as the
	// positional listen(0) form.
	src := `
import http from 'http'
const server = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.end("unused")
})
server.listen({ port: 0 }, () => {
  console.log("addr=" + server.address().address)
  server.close()
})
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "addr=0.0.0.0") {
		t.Errorf("default host was not 0.0.0.0: %q", out)
	}
}

func TestE2EHTTPMultipleServers(t *testing.T) {
	// TDD-00191 Stage 1: two http.createServer instances listen on different
	// ports in one program, each serving its own handler concurrently.
	pa, pb := freePort(t), freePort(t)
	src := `
import http from 'http'
const a = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200); res.end("A:" + req.url)
})
const b = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200); res.end("B:" + req.url)
})
a.listen(19301, () => { b.listen(19302, () => { console.log("ready") }) })
`
	src = strings.ReplaceAll(src, "19301", fmt.Sprintf("%d", pa))
	src = strings.ReplaceAll(src, "19302", fmt.Sprintf("%d", pb))
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitListening(t, pa)
	waitListening(t, pb)

	get := func(port int, path string) string {
		c := &http.Client{Timeout: 3 * time.Second}
		resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, path))
		if err != nil {
			t.Fatalf("GET %d%s: %v", port, path, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}
	// Interleave requests across both servers — each must answer with its own
	// handler, not the other's.
	if got := get(pa, "/one"); got != "A:/one" {
		t.Errorf("server A: got %q, want %q", got, "A:/one")
	}
	if got := get(pb, "/two"); got != "B:/two" {
		t.Errorf("server B: got %q, want %q", got, "B:/two")
	}
	if got := get(pa, "/three"); got != "A:/three" {
		t.Errorf("server A (2nd request): got %q, want %q", got, "A:/three")
	}
	if got := get(pb, "/four"); got != "B:/four" {
		t.Errorf("server B (2nd request): got %q, want %q", got, "B:/four")
	}
}

func TestE2EHTTPMultipleServersCloseExits(t *testing.T) {
	// TDD-00191 Stage 1: closing every server (primary + additional) lets the
	// event loop exit — an additional server's close() must clear its
	// extra-listener-table entry, or the loop would spin forever.
	src := `
import http from 'http'
const a = http.createServer((req: IncomingMessage, res: ServerResponse) => { res.end("a") })
const b = http.createServer((req: IncomingMessage, res: ServerResponse) => { res.end("b") })
a.listen(0, () => {
  b.listen(0, () => {
    console.log("up")
    a.close()
    b.close()
  })
})
`
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	_ = out
	select {
	case <-done:
		// exited — good.
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("program did not exit after closing both servers (extra-listener close leak)")
	}
}

func TestE2EHTTPMultipleServersRelisten(t *testing.T) {
	// TDD-00191 Stage 2: an additional server can listen() again after close().
	// Primary server `a` stays up; a request to `/move` closes extra server `b`
	// and relistens it on a fresh port. The new port must then serve b's handler
	// (a fresh extra-listener-table entry appended after the -1'd one).
	pa, pb1, pb2 := freePort(t), freePort(t), freePort(t)
	src := `
import http from 'http'
const b = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200); res.end("B:" + req.url)
})
const a = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  if (req.url === "/move") { b.close(); b.listen(19402, () => {}) }
  res.writeHead(200); res.end("A:" + req.url)
})
a.listen(19400, () => { b.listen(19401, () => { console.log("ready") }) })
`
	src = strings.ReplaceAll(src, "19400", fmt.Sprintf("%d", pa))
	src = strings.ReplaceAll(src, "19401", fmt.Sprintf("%d", pb1))
	src = strings.ReplaceAll(src, "19402", fmt.Sprintf("%d", pb2))
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitListening(t, pa)
	waitListening(t, pb1)

	get := func(port int, path string) string {
		c := &http.Client{Timeout: 3 * time.Second}
		resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, path))
		if err != nil {
			t.Fatalf("GET %d%s: %v", port, path, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}
	// b serves on its first port.
	if got := get(pb1, "/x"); got != "B:/x" {
		t.Errorf("server B (first port): got %q, want %q", got, "B:/x")
	}
	// Trigger the close+relisten via a request to the primary server.
	if got := get(pa, "/move"); got != "A:/move" {
		t.Errorf("server A /move: got %q, want %q", got, "A:/move")
	}
	// b now serves on the new port.
	waitListening(t, pb2)
	if got := get(pb2, "/y"); got != "B:/y" {
		t.Errorf("server B (relisten port): got %q, want %q", got, "B:/y")
	}
}

func TestE2EHTTPMultipleServersUpgradeOnPrimary(t *testing.T) {
	// TDD-00191 Stage 3: an 'upgrade' handler on the PRIMARY server coexists
	// with a plain additional server. The upgrade divert runs on the primary
	// dispatcher; the extra server is plain HTTP/1.1.
	pa, pb := freePort(t), freePort(t)
	src := `
import http from 'http'
const a = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200); res.end('plain http')
})
a.on('upgrade', (req, socket, head) => {
  socket.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n')
  socket.on('data', (chunk) => { socket.write('echo:' + chunk.toString()) })
})
const b = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200); res.end('B:' + req.url)
})
a.listen(19501, () => { b.listen(19502, () => { console.log('ready') }) })
`
	src = strings.ReplaceAll(src, "19501", fmt.Sprintf("%d", pa))
	src = strings.ReplaceAll(src, "19502", fmt.Sprintf("%d", pb))
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitListening(t, pa)
	waitListening(t, pb)

	// The additional server serves plain HTTP.
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/x", pb))
	if err != nil {
		t.Fatalf("GET extra: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if got := string(body); got != "B:/x" {
		t.Errorf("extra server: got %q, want %q", got, "B:/x")
	}

	// The primary server upgrades and echoes.
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", pa), 2*time.Second)
	if err != nil {
		t.Fatalf("dial primary: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET /chat HTTP/1.1\r\nHost: x\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n")); err != nil {
		t.Fatalf("write upgrade req: %v", err)
	}
	r := bufio.NewReader(conn)
	readUpgradeHandshake(t, r)
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatalf("write data: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if got, want := string(buf[:n]), "echo:hello"; got != want {
		t.Errorf("primary echo: got %q, want %q", got, want)
	}
}

func TestE2EHTTPMultipleServersUpgradeOnExtra(t *testing.T) {
	// TDD-00191 Stage 3: an 'upgrade' handler on a NON-primary (additional)
	// server routes to that server's own suffixed handler global — the primary
	// is a plain server, the additional server does the upgrade echo. Proves the
	// handle→suffix association and per-server ws/upgrade dispatch.
	pa, pb := freePort(t), freePort(t)
	src := `
import http from 'http'
const a = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200); res.end('A:' + req.url)
})
const b = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200); res.end('B:' + req.url)
})
b.on('upgrade', (req, socket, head) => {
  socket.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n')
  socket.on('data', (chunk) => { socket.write('bECHO:' + chunk.toString()) })
})
a.listen(19601, () => { b.listen(19602, () => { console.log('ready') }) })
`
	src = strings.ReplaceAll(src, "19601", fmt.Sprintf("%d", pa))
	src = strings.ReplaceAll(src, "19602", fmt.Sprintf("%d", pb))
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitListening(t, pa)
	waitListening(t, pb)

	// The primary server serves plain HTTP.
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/x", pa))
	if err != nil {
		t.Fatalf("GET primary: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if got := string(body); got != "A:/x" {
		t.Errorf("primary server: got %q, want %q", got, "A:/x")
	}

	// The additional server upgrades and echoes.
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", pb), 2*time.Second)
	if err != nil {
		t.Fatalf("dial additional: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET /chat HTTP/1.1\r\nHost: x\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n")); err != nil {
		t.Fatalf("write upgrade req: %v", err)
	}
	r := bufio.NewReader(conn)
	readUpgradeHandshake(t, r)
	if _, err := conn.Write([]byte("hi")); err != nil {
		t.Fatalf("write data: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if got, want := string(buf[:n]), "bECHO:hi"; got != want {
		t.Errorf("additional-server echo: got %q, want %q", got, want)
	}
}

func TestE2EHTTPChunkedKeepAlive(t *testing.T) {
	// A chunked/streaming (ReadableStream) response is now persistent: it
	// advertises Connection: keep-alive and the writer re-arms the connection at
	// stream end, so a second request on the same socket is served (ADR-00802).
	src := `
import http from 'klain:http'
http.listen(8199, (req: HttpRequest) => {
  let n = 0
  const body = new ReadableStream<string>({
    pull: (c) => {
      n = n + 1
      if (n > 2) { c.close(); return }
      c.enqueue("chunk" + n + " ")
    }
  })
  return { status: 200, body: body }
})
`
	port := startHTTPServer(t, src, 8199)

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// First request: keep-alive (no Connection header). Second: Connection: close.
	if _, err := conn.Write([]byte("GET /a HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatalf("write req1: %v", err)
	}
	// Read the first full chunked response (ends at the "0\r\n\r\n" terminator).
	br := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	first := readChunkedResponse(t, br)
	if !strings.Contains(first, "Connection: keep-alive") {
		t.Fatalf("first streamed response did not advertise keep-alive:\n%q", first)
	}
	if !strings.Contains(first, "Transfer-Encoding: chunked") || (!strings.Contains(first, "chunk1 ") || !strings.Contains(first, "chunk2 ")) {
		t.Fatalf("first response body malformed:\n%q", first)
	}

	// Same socket, second request — only served if the connection was re-armed.
	if _, err := conn.Write([]byte("GET /b HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write req2: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	second := readChunkedResponse(t, br)
	if !strings.Contains(second, "chunk1 ") || !strings.Contains(second, "chunk2 ") {
		t.Fatalf("second request on the re-armed connection was not served:\n%q", second)
	}
	if !strings.Contains(second, "Connection: close") {
		t.Fatalf("second response should honor the client's Connection: close:\n%q", second)
	}
}

func TestE2EHTTPUnionBodyStringKeepAlive(t *testing.T) {
	// A handler whose `body` is a union `string | ReadableStream` taking the
	// *string* branch is now persistent too (TDD-00196 follow-on): it advertises
	// Connection: keep-alive and re-arms the connection, so a second request on
	// the same socket is served. Previously the union string branch always closed.
	src := `
import http from 'klain:http'
interface Res { status: number; body: string | ReadableStream<string> }
http.listen(8204, (req: HttpRequest): Res => {
  return { status: 200, body: 'path=' + req.path }
})
`
	port := startHTTPServer(t, src, 8204)

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	br := bufio.NewReader(conn)

	// First request: no Connection header ⇒ keep-alive.
	if _, err := conn.Write([]byte("GET /a HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatalf("write req1: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	first := readContentLengthResponse(t, br)
	if !strings.Contains(first, "Connection: keep-alive") {
		t.Fatalf("first union string response did not advertise keep-alive:\n%q", first)
	}
	if !strings.Contains(first, "path=/a") {
		t.Fatalf("first response body malformed:\n%q", first)
	}

	// Same socket, second request — only served if the connection was re-armed.
	if _, err := conn.Write([]byte("GET /b HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write req2: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	second := readContentLengthResponse(t, br)
	if !strings.Contains(second, "path=/b") {
		t.Fatalf("second request on the re-armed connection was not served:\n%q", second)
	}
	if !strings.Contains(second, "Connection: close") {
		t.Fatalf("second response should honor the client's Connection: close:\n%q", second)
	}
}

func TestE2EHTTPServerConnectionEvent(t *testing.T) {
	// The server 'connection' event (TDD-00198) fires once per accepted TCP
	// connection, before HTTP parsing — not once per request. Two keep-alive
	// requests on one socket see the same count; a second socket bumps it.
	src := `
import http from 'http'
let count = 0
const server = http.createServer((req, res) => { res.end('count=' + count) })
server.on('connection', (socket) => { count = count + 1 })
server.listen(8306, () => { console.log('listening') })
`
	port := startHTTPServer(t, src, 8306)

	dialReq := func(conn net.Conn, br *bufio.Reader, path string) string {
		if _, err := conn.Write([]byte("GET " + path + " HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		conn.SetReadDeadline(time.Now().Add(4 * time.Second))
		return readContentLengthResponse(t, br)
	}

	// Relative, not absolute: the readiness probe (waitListening) is itself a
	// real TCP connection that fires 'connection', so the baseline is unknown —
	// assert the increments instead.
	countOf := func(resp string) int {
		i := strings.Index(resp, "count=")
		if i < 0 {
			t.Fatalf("no count in response:\n%q", resp)
		}
		var n int
		fmt.Sscanf(resp[i+len("count="):], "%d", &n)
		return n
	}

	c1, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial c1: %v", err)
	}
	defer c1.Close()
	b1 := bufio.NewReader(c1)
	first := countOf(dialReq(c1, b1, "/a"))
	// Second request on the SAME connection — count must not change (no re-fire).
	if reuse := countOf(dialReq(c1, b1, "/b")); reuse != first {
		t.Fatalf("keep-alive reuse re-fired 'connection': first=%d reuse=%d", first, reuse)
	}

	c2, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial c2: %v", err)
	}
	defer c2.Close()
	b2 := bufio.NewReader(c2)
	if second := countOf(dialReq(c2, b2, "/c")); second != first+1 {
		t.Fatalf("second connection: want %d, got %d", first+1, second)
	}
}

func TestE2EHTTPServerCloseEvent(t *testing.T) {
	// The server 'close' event + deferred close(cb) (TDD-00197): closing from the
	// 'listening' callback with no connections in flight fires the 'close' event
	// then the close(cb) callback (registration order), and the process exits
	// cleanly. The close(cb) is no longer synchronous — it settles at drain.
	assertOutputImports(t, `
import http from 'http'
const server = http.createServer((req, res) => { res.end('ok') })
server.on('close', () => { console.log('close event') })
server.listen(8302, () => {
  console.log('listening')
  server.close(() => { console.log('close cb') })
})
`, "listening\nclose event\nclose cb")
}

func TestE2EHTTPServerCloseWaitsForInFlight(t *testing.T) {
	// close() called mid-handler must not fire 'close'/the callback until the
	// in-flight request has finished (Node's graceful drain): the response is
	// still delivered, and the ordering proves the close fires after the handler.
	src := `
import http from 'http'
const server = http.createServer((req, res) => {
  server.close(() => { console.log('closed after drain') })
  res.end('bye')
})
server.on('close', () => { console.log('close event') })
server.listen(8303, () => { console.log('listening') })
`
	port := startHTTPServer(t, src, 8303)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET /a HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write req: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	body, _ := io.ReadAll(conn)
	if !strings.Contains(string(body), "bye") {
		t.Fatalf("in-flight request was not completed after close():\n%q", string(body))
	}
	// After draining, the listener is gone — a new connection must be refused.
	if c2, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond); err == nil {
		c2.SetReadDeadline(time.Now().Add(1 * time.Second))
		if n, _ := c2.Read(make([]byte, 1)); n > 0 {
			t.Fatalf("listener still accepting after close() drained")
		}
		c2.Close()
	}
}

func TestE2EHTTPClientErrorListenerFires(t *testing.T) {
	// TDD-00215: the server 'clientError' event fires when a client truncates a
	// request mid-parse (bytes buffered, then the peer closes). The listener
	// records what it saw; a later normal request reports it back, so the test
	// needs neither server stdout nor a socket write from the listener.
	src := `
import http from 'http'
let count = 0
let lastCode = "none"
const server = http.createServer((req, res) => {
  res.end("count=" + count + " code=" + lastCode)
})
server.on('clientError', (err: Error, socket) => {
  count = count + 1
  lastCode = err.code
})
server.listen(8310)
`
	port := startHTTPServer(t, src, 8310)
	// Truncate a request: partial request, no terminator, then close.
	c1, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial c1: %v", err)
	}
	if _, err := c1.Write([]byte("GET / HTTP/1.1\r\nHost: x")); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	c1.Close() // truncate mid-request → clientError(ECONNRESET)
	time.Sleep(300 * time.Millisecond)

	// A normal request on a fresh connection reports what the listener recorded.
	c2, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial c2: %v", err)
	}
	defer c2.Close()
	b2 := bufio.NewReader(c2)
	if _, err := c2.Write([]byte("GET /r HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatalf("write req: %v", err)
	}
	c2.SetReadDeadline(time.Now().Add(4 * time.Second))
	resp := readContentLengthResponse(t, b2)
	if !strings.Contains(resp, "count=1") || !strings.Contains(resp, "code=ECONNRESET") {
		t.Fatalf("clientError did not fire as expected:\n%q", resp)
	}
}

func TestE2EHTTPClientErrorDefault431(t *testing.T) {
	// TDD-00215: with no 'clientError' listener, an oversized header block gets
	// Node's default 431 response + Connection: close (before this feature the
	// connection was closed silently).
	src := `
import http from 'http'
http.createServer((req, res) => { res.end("ok") }).listen(8311)
`
	port := startHTTPServer(t, src, 8311)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\n")); err != nil {
		t.Fatalf("write req line: %v", err)
	}
	// Overflow the 10MiB header-block cap: keep sending header bytes with no
	// terminator until the server aborts (write fails) or we pass the cap.
	pad := []byte("X-Pad: " + strings.Repeat("a", 65536) + "\r\n")
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	for sent := 0; sent < 11*1024*1024; {
		n, werr := conn.Write(pad)
		sent += n
		if werr != nil {
			break // server aborted + closed mid-send — expected
		}
	}
	conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	resp, _ := io.ReadAll(conn)
	if !strings.Contains(string(resp), "431") {
		t.Fatalf("no 431 default for header overflow:\n%q", string(resp))
	}
}

func TestE2EHTTPClientErrorSocketWriteEnd(t *testing.T) {
	// TDD-00215: the net.Socket handed to a 'clientError' listener now supports
	// socket.write()/socket.end() (previously the runtime symbols weren't linked
	// on an http-only program). The listener writes its own response on the
	// oversized-header path, replacing Node's default 431.
	src := `
import http from 'http'
const server = http.createServer((req, res) => { res.end("ok") })
server.on('clientError', (err: Error, socket) => {
  socket.write("HTTP/1.1 400 Bad Request\r\nConnection: close\r\nContent-Length: 7\r\n\r\ncustom!")
  socket.end()
})
server.listen(8312)
`
	port := startHTTPServer(t, src, 8312)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\n")); err != nil {
		t.Fatalf("write req line: %v", err)
	}
	pad := []byte("X-Pad: " + strings.Repeat("a", 65536) + "\r\n")
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	for sent := 0; sent < 11*1024*1024; {
		n, werr := conn.Write(pad)
		sent += n
		if werr != nil {
			break
		}
	}
	conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	resp, _ := io.ReadAll(conn)
	if !strings.Contains(string(resp), "400 Bad Request") || !strings.Contains(string(resp), "custom!") {
		t.Fatalf("listener's socket.write/end response not received:\n%q", string(resp))
	}
	if strings.Contains(string(resp), "431") {
		t.Fatalf("default 431 sent despite a listener owning the response:\n%q", string(resp))
	}
}

func TestE2EHTTPServerErrorEventEADDRINUSE(t *testing.T) {
	// TDD-00215 Stage 2: binding a port already held by another server fires the
	// server 'error' event with err.code === 'EADDRINUSE' instead of aborting the
	// process. The listener runs synchronously at listen() time; exiting from it
	// keeps the event loop (still holding s1's listener) from running.
	src := `
import http from 'http'
const s1 = http.createServer((req, res) => { res.end("a") })
s1.listen(8317)
const s2 = http.createServer((req, res) => { res.end("b") })
s2.on('error', (err: Error) => {
  console.log("error code=" + err.code)
  process.exit(0)
})
s2.listen(8317)
console.log("unreachable")
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "error code=EADDRINUSE") {
		t.Fatalf("expected EADDRINUSE error event, got:\n%q", out)
	}
	if strings.Contains(out, "unreachable") {
		t.Fatalf("code after a fired-and-exited error handler ran:\n%q", out)
	}
}

func TestE2EHTTPServerErrorEventNoListenerThrows(t *testing.T) {
	// TDD-00215 Stage 2: with no 'error' listener registered, a bind failure still
	// surfaces as an uncaught, process-aborting error (Node parity) — the
	// pre-feature behavior of __kml_http_bind_and_listen is preserved.
	src := `
import http from 'http'
const s1 = http.createServer((req, res) => { res.end("a") })
s1.listen(8318)
const s2 = http.createServer((req, res) => { res.end("b") })
s2.listen(8318)
console.log("unreachable")
`
	out, code := compileAndRunExpectExitImports(t, src)
	if code == 0 {
		t.Fatalf("expected nonzero exit on unhandled bind failure, got 0:\n%q", out)
	}
	if strings.Contains(out, "unreachable") {
		t.Fatalf("code after an unhandled bind failure ran:\n%q", out)
	}
}

// readContentLengthResponse reads one HTTP/1.1 response whose body length is
// given by its Content-Length header.
func readContentLengthResponse(t *testing.T, br *bufio.Reader) string {
	t.Helper()
	var sb strings.Builder
	n := 0
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		sb.WriteString(line)
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			fmt.Sscanf(strings.TrimSpace(line[len("content-length:"):]), "%d", &n)
		}
		if line == "\r\n" {
			break
		}
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(br, body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	sb.Write(body)
	return sb.String()
}

// readChunkedResponse reads one HTTP/1.1 chunked response: the header block, then
// chunk bodies until the zero-length terminator chunk.
func readChunkedResponse(t *testing.T, br *bufio.Reader) string {
	t.Helper()
	var sb strings.Builder
	// Headers up to the blank line.
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		sb.WriteString(line)
		if line == "\r\n" {
			break
		}
	}
	// Chunks: "<hexlen>\r\n<data>\r\n", ending at a 0-length chunk.
	for {
		sizeLine, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read chunk size: %v", err)
		}
		sb.WriteString(sizeLine)
		var size int
		fmt.Sscanf(strings.TrimSpace(sizeLine), "%x", &size)
		body := make([]byte, size+2) // data + trailing CRLF
		if _, err := io.ReadFull(br, body); err != nil {
			t.Fatalf("read chunk body: %v", err)
		}
		sb.Write(body)
		if size == 0 {
			break
		}
	}
	return sb.String()
}

func TestE2EHTTPExpressionBodiedHandler(t *testing.T) {
	// An expression-bodied handler arrow whose body is a void-returning res
	// method — `(req, res) => res.end(body)` — must infer a void return type and
	// emit `ret void`, not a value-less `ret i64` (invalid IR). Regression for
	// the ServerResponse-method return-type inference gap (ADR-00801).
	src := `
import http from 'http'
const server = http.createServer((req: IncomingMessage, res: ServerResponse) => res.end("hi"))
server.listen(0, () => {
  console.log("bound:" + (server.address().port > 0))
  server.close()
})
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "bound:true") {
		t.Errorf("expression-bodied res.end handler did not run: %q", out)
	}
}

func TestE2EHTTPListenBasicGet(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8941, (req: HttpRequest): Res => {
  return { status: 200, body: "hello from KML" }
})
`
	port := startHTTPServer(t, src, 8941)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello from KML" {
		t.Errorf("body: got %q, want %q", string(body), "hello from KML")
	}
}

func TestE2EHTTPListenMethodAndPathFields(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8942, (req: HttpRequest): Res => {
  return { status: 200, body: req.method + " " + req.path }
})
`
	port := startHTTPServer(t, src, 8942)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/some/path", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "GET /some/path" {
		t.Errorf("body: got %q, want %q", string(body), "GET /some/path")
	}
}

func TestE2EHTTPListenMultipleSequentialRequests(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
let count = 0
http.listen(8943, (req: HttpRequest): Res => {
  count = count + 1
  return { status: 200, body: "req " + count }
})
`
	port := startHTTPServer(t, src, 8943)
	for i := 1; i <= 3; i++ {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err != nil {
			t.Fatalf("GET #%d: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		want := fmt.Sprintf("req %d", i)
		if string(body) != want {
			t.Errorf("request #%d body: got %q, want %q", i, string(body), want)
		}
	}
}

func TestE2EHTTPListenCustomStatus(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8944, (req: HttpRequest): Res => {
  if (req.path === "/missing") {
    return { status: 404, body: "not found" }
  }
  return { status: 200, body: "ok" }
})
`
	port := startHTTPServer(t, src, 8944)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/missing", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "not found" {
		t.Errorf("body: got %q, want %q", string(body), "not found")
	}
}

func TestE2EHTTPListenCoexistsWithSetInterval(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
let n = 0
setInterval(() => {
  n = n + 1
}, 50)
http.listen(8945, (req: HttpRequest): Res => {
  return { status: 200, body: "n=" + n }
})
`
	port := startHTTPServer(t, src, 8945)
	time.Sleep(200 * time.Millisecond)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) == "n=0" {
		t.Errorf("expected setInterval to have ticked at least once while the server was running, got %q", string(body))
	}
}

func TestE2EHTTPListenBindFailureThrows(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
try {
  http.listen(8946, (req: HttpRequest): Res => {
    return { status: 200, body: "ok" }
  })
} catch (e) {
  console.log("caught: " + e.message)
}
`
	port := freePort(t)
	src = subPort(src, 8946, port)
	startHTTPServerFixed(t, src, port)
	// A second instance on the SAME port must fail to bind and hit the catch.
	got := compileAndRunImports(t, src)
	if got == "" {
		t.Fatal("expected the second instance's catch block to print something")
	}
}

func TestE2EKlainHTTPNamespaceResolves(t *testing.T) {
	// TDD-00131: the bespoke `http.listen(handler ⇒ response)` model is reachable
	// under the explicitly-non-Node `klain:http` specifier.
	_, err := parseAndCompileImports(t, `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8951, (req: HttpRequest): Res => { return { status: 200, body: "ok" } })
`)
	if err != nil {
		t.Fatalf("klain:http import should compile the bespoke server: %v", err)
	}
}

func TestE2EHTTPListenWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `import http from 'klain:http'
http.listen(8947)`)
	if err == nil {
		t.Fatal("expected a compile error for http.listen with only 1 argument, got none")
	}
}

func TestE2EHTTPListenNonObjectReturnTypeRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `import http from 'klain:http'
http.listen(8948, (req: HttpRequest): number => 200)`)
	if err == nil {
		t.Fatal("expected a compile error for a handler not returning an object type, got none")
	}
}

func TestE2EHTTPListenMissingBodyFieldRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `
import http from 'klain:http'
interface Res { status: number }
http.listen(8949, (req: HttpRequest): Res => { return { status: 200 } })
`)
	if err == nil {
		t.Fatal("expected a compile error for a handler return type missing a body field, got none")
	}
}

// TestE2EHTTPListenConcurrentConnections is the decisive test for
// ADR-00049's fiber-based scheduler (TDD-00006 Part 2): a connection that
// sits open without sending its request line for longer than this test's
// own timeout must not block a second, immediately-answered connection —
// proving the server genuinely services connections concurrently rather
// than one at a time. Before ADR-00049, this would have deadlocked (the
// slow connection's blocking read() never returns, so accept() for the
// fast connection never even runs).
func TestE2EHTTPListenConcurrentConnections(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8950, (req: HttpRequest): Res => {
  return { status: 200, body: req.path }
})
`
	port := startHTTPServer(t, src, 8950)

	slowConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("slow connection dial: %v", err)
	}
	defer slowConn.Close()
	// Deliberately don't send anything on slowConn yet.

	time.Sleep(100 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/fast", port))
		if err != nil {
			t.Errorf("fast GET: %v", err)
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "/fast" {
			t.Errorf("fast GET body: got %q, want %q", string(body), "/fast")
		}
	}()

	select {
	case <-done:
		// Good: the fast request completed without waiting for the slow
		// connection to send anything.
	case <-time.After(2 * time.Second):
		t.Fatal("fast request was blocked by the still-pending slow connection — concurrency is broken")
	}

	// Clean up the slow connection by finally sending its request.
	_, _ = slowConn.Write([]byte("GET /slow HTTP/1.1\r\n\r\n"))
}

// Regression test for a real stack-overflow crash (SIGSEGV, "connection
// reset by peer" from the client's point of view): __kml_event_loop_run's
// main select()-based dispatch loop had several `alloca`s (fd_sets, scratch
// counters) placed in loop-body blocks instead of its entry block, so every
// single select() wake — i.e. every request — leaked a fixed chunk of stack
// that was never freed until the process exited (which, for an http.listen
// server, is never). A manual repro with Apache Bench reliably crashed a
// pre-fix binary after ~20,000-21,000 requests (matching the ~16KB/iteration
// leak rate against an 8MB default stack); this test sends enough requests
// to cross that threshold and confirms the server is still alive and
// answering correctly afterward.
func TestE2EHTTPListenManyRequestsDoesNotLeakStack(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8951, (req: HttpRequest): Res => {
  return { status: 200, body: "ok" }
})
`
	port := startHTTPServer(t, src, 8951)

	client := &http.Client{}
	const n = 30000
	for i := 1; i <= n; i++ {
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err != nil {
			t.Fatalf("GET #%d (of %d): %v", i, n, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "ok" {
			t.Fatalf("GET #%d: body got %q, want %q", i, string(body), "ok")
		}
	}
}

// newDelayedUpstreamServer is an httptest server standing in for a real
// upstream API: /slow sleeps before responding, everything else responds
// immediately — used to prove ADR-00050's actual point, that two
// http.listen connections independently awaiting fetch(...) against this
// upstream run concurrently rather than one blocking the other.
func newDelayedUpstreamServer(t *testing.T, slowDelay time.Duration) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			time.Sleep(slowDelay)
		}
		fmt.Fprintf(w, "upstream %s", r.URL.Path)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestE2EHTTPListenAsyncHandlerAwaitFetch(t *testing.T) {
	upstream := newDelayedUpstreamServer(t, 0)
	src := fmt.Sprintf(`
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8951, async (req: HttpRequest): Promise<Res> => {
  const r: Response = await fetch("%s" + req.path)
  return { status: 200, body: await r.text() }
})
`, upstream.URL)
	port := startHTTPServer(t, src, 8951)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/hello", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "upstream /hello" {
		t.Errorf("body: got %q, want %q", string(body), "upstream /hello")
	}
}

// TestE2EHTTPListenConcurrentAwaitFetch is the decisive test for
// ADR-00050: two connections whose handlers each await fetch(...) against
// the same upstream, one hitting a slow path and one hitting a fast path,
// must not serialize — the fast one must complete well before the slow
// upstream's own delay elapses. Before ADR-00050, fetch() was a blocking
// libcurl call, so the slow connection's handler would have frozen the
// entire single-threaded process (every fiber, not just its own) for the
// full delay, and the fast request would have had to wait behind it.
func TestE2EHTTPListenConcurrentAwaitFetch(t *testing.T) {
	const slowDelay = 1200 * time.Millisecond
	upstream := newDelayedUpstreamServer(t, slowDelay)
	src := fmt.Sprintf(`
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8952, async (req: HttpRequest): Promise<Res> => {
  const r: Response = await fetch("%s" + req.path)
  return { status: 200, body: await r.text() }
})
`, upstream.URL)
	port := startHTTPServer(t, src, 8952)

	slowDone := make(chan struct{})
	go func() {
		defer close(slowDone)
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/slow", port))
		if err != nil {
			t.Errorf("slow GET: %v", err)
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "upstream /slow" {
			t.Errorf("slow GET body: got %q, want %q", string(body), "upstream /slow")
		}
	}()

	time.Sleep(200 * time.Millisecond) // let the slow request's fetch start first

	fastStart := time.Now()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/fast", port))
	if err != nil {
		t.Fatalf("fast GET: %v", err)
	}
	defer resp.Body.Close()
	fastElapsed := time.Since(fastStart)
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "upstream /fast" {
		t.Errorf("fast GET body: got %q, want %q", string(body), "upstream /fast")
	}
	if fastElapsed >= slowDelay/2 {
		t.Errorf("fast request took %v — expected it to complete quickly despite the slow request's %v upstream fetch still being in flight (concurrency is broken)", fastElapsed, slowDelay)
	}

	<-slowDone
}

// TestE2EHTTPCreateServerAsyncHandlerAwaitFetch is the regression for
// ADR-00985: a Node http.createServer((req,res) => …) async handler that
// awaits a *task-shaped* promise — a call to a may-suspend async fn (here
// proxy(), which itself awaits fetch) — while running on a connection fiber.
// Before the fix, __kml_task_await_ready had no fiber-aware park path: the
// fiber fell into the top-level busy-drive (toploop), whose
// __kml_task_sched_step clobbered @__kml_main_ctx (it swapcontexts
// main->task but was itself run from the fiber, not the real main context).
// The fiber then returned through its now-stale uc_link and jumped wild — the
// heap was already trashed by the time __kml_http_send_response's malloc/free
// ran (SIGABRT in the allocator lock; a smashed return address under ASan). An
// instant upstream is the trigger (the fetch completes before the reactor
// parks). Awaiting fetch *directly* in the handler never hit this — that parks
// the fiber via __kml_await_fetch_headers' maybeconn — which is why the
// res-path async handler rotted uncovered. The return-value http.listen path
// (TestE2EHTTPListenAsyncHandlerAwaitFetch) was likewise fine.
func TestE2EHTTPCreateServerAsyncHandlerAwaitFetch(t *testing.T) {
	upstream := newDelayedUpstreamServer(t, 0) // instant upstream — the crashing timing
	src := fmt.Sprintf(`
import http from 'http'
async function proxy(path: string): Promise<string> {
  const r = await fetch("%s" + path)
  return await r.text()
}
const server = http.createServer(async (req: IncomingMessage, res: ServerResponse) => {
  const v = await proxy(req.url)
  res.writeHead(200)
  res.end("front:" + v)
})
server.listen(8196)
`, upstream.URL)
	port := startHTTPServer(t, src, 8196)

	// A single request already crashed pre-fix; run several sequentially so a
	// survivor after the first proves the fiber's main_ctx was not corrupted.
	for i := 0; i < 4; i++ {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/s%d", port, i))
		if err != nil {
			t.Fatalf("seq GET %d: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		want := fmt.Sprintf("front:upstream /s%d", i)
		if string(body) != want {
			t.Errorf("seq GET %d body: got %q, want %q", i, string(body), want)
		}
	}

	// Concurrent connections, each awaiting the proxy fetch — the timing that
	// most reliably crashed pre-fix.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/c%d", port, i))
			if err != nil {
				t.Errorf("conc GET %d: %v", i, err)
				return
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			want := fmt.Sprintf("front:upstream /c%d", i)
			if string(body) != want {
				t.Errorf("conc GET %d body: got %q, want %q", i, string(body), want)
			}
		}(i)
	}
	wg.Wait()
}

// TestE2EHTTPCreateServerAsyncHandlerConcurrentTaskAwait proves ADR-00986's
// follow-up (a): a handler that awaits a task-shaped promise (here `proxy`, an
// async helper that itself awaits a slow upstream) parks by yielding its
// connection fiber to the event loop instead of busy-waiting it — so several
// such requests run concurrently. With the pre-ADR-00986 busy-wait each fiber
// held the loop to itself and the requests serialized (N × the upstream delay);
// concurrent, N requests finish in ≈ one upstream delay.
func TestE2EHTTPCreateServerAsyncHandlerConcurrentTaskAwait(t *testing.T) {
	const upstreamDelay = 300 * time.Millisecond
	upstream := newDelayedUpstreamServer(t, upstreamDelay)
	src := fmt.Sprintf(`
import http from 'http'
async function proxy(path: string): Promise<string> {
  const r = await fetch("%s" + path)
  return await r.text()
}
const server = http.createServer(async (req: IncomingMessage, res: ServerResponse) => {
  const v = await proxy("/slow")
  res.writeHead(200)
  res.end("front:" + v)
})
server.listen(8198)
`, upstream.URL)
	port := startHTTPServer(t, src, 8198)

	const n = 4
	// A bounded per-request timeout: the concurrency window is ~300ms, so 20s
	// only trips on a genuine server-side hang. Without it a deadlock in the
	// async-handler fiber-park path would block wg.Wait() forever and take the
	// entire `go test` 50m budget down with it (masking every other test) rather
	// than failing this one test fast.
	client := &http.Client{Timeout: 20 * time.Second}
	start := time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/c%d", port, i))
			if err != nil {
				errs <- err
				return
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != "front:upstream /slow" {
				errs <- fmt.Errorf("req %d body: got %q", i, string(body))
			}
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	// Serialized would be ≈ n×300ms = 1.2s; concurrent is ≈ 300ms. Assert well
	// under the serialized figure with generous headroom for scheduling/CI jitter.
	if elapsed > upstreamDelay*(n-1) {
		t.Fatalf("%d concurrent task-await requests took %v — serialized, not concurrent (upstream delay %v)", n, elapsed, upstreamDelay)
	}
}

// TestE2EHTTPCreateServerAsyncHandlerInProcessClient proves ADR-00986's
// follow-up (b): a server whose async handler awaits a task-shaped promise
// (here a setTimeout-backed delay) is driven by an in-process http.get client
// on the same event loop. Before the fix, once the handler's fiber yielded and
// then wrote its response the loop blocked in select() with no JS timer and an
// idle curl deadline, so the in-process client's completion reaction never
// fired and the process hung. The @__kml_conn_ran non-blocking-poll forces the
// following curl drive that delivers the response. The program exits 0 on
// success; a hang trips the context deadline.
func TestE2EHTTPCreateServerAsyncHandlerInProcessClient(t *testing.T) {
	bin := buildBinaryImports(t, `
import http from 'http';
async function delay(ms: number): Promise<void> {
  return await new Promise<void>((r) => setTimeout(() => r(), ms));
}
http.createServer(async (req: IncomingMessage, res: ServerResponse) => {
  await delay(20);
  res.writeHead(200, { "Content-Type": "text/plain" });
  res.end("kalimera from the server");
}).listen(18547, () => {
  http.get("http://127.0.0.1:18547/", (res) => {
    let body = "";
    res.on('data', (chunk: string) => { body = body + chunk; });
    res.on('end', () => {
      console.log("status:" + res.statusCode);
      console.log("body:" + body);
      process.exit(0);
    });
  });
});
`)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin).Output()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatal("timed out — the in-process client's completion reaction was never delivered (event loop blocked after the async handler yielded)")
	}
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := strings.TrimRight(string(out), "\n")
	want := "status:200\nbody:kalimera from the server"
	if got != want {
		t.Fatalf("output: got %q, want %q", got, want)
	}
}

// TestE2EHTTP2CreateServerAsyncHandlerAwaitFetch is the http2.createServer
// (h2c) counterpart of the crash above (ADR-00985): the same fiber-park bug
// detonated as a smashed return address under ASan on the h2 path. Several
// requests in a row prove the server survives the first async-handler await.
func TestE2EHTTP2CreateServerAsyncHandlerAwaitFetch(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found in PATH")
	}
	upstream := newDelayedUpstreamServer(t, 0) // instant upstream — the crashing timing
	src := fmt.Sprintf(`
import http2 from 'http2'
async function proxy(path: string): Promise<string> {
  const r = await fetch("%s" + path)
  return await r.text()
}
const server = http2.createServer(async (req, res) => {
  const v = await proxy(req.path)
  res.writeHead(200)
  res.end("front:" + v)
})
server.listen(8197)
`, upstream.URL)
	port := startHTTPServer(t, src, 8197)

	for i := 0; i < 3; i++ {
		out, err := exec.Command(nativeCurl(), "-s", "--http2-prior-knowledge",
			fmt.Sprintf("http://127.0.0.1:%d/h%d", port, i)).CombinedOutput()
		if err != nil {
			t.Fatalf("curl h2c #%d: %v\n%s", i, err, out)
		}
		want := fmt.Sprintf("front:upstream /h%d", i)
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("h2c #%d: got %q, want %q", i, got, want)
		}
	}
}

// --- ADR-00072: request headers, query string, request body, response headers ---

func TestE2EHTTPListenRequestHeadersLowercasedLookup(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8953, (req: HttpRequest): Res => {
  return { status: 200, body: req.headers.get("x-test-header") + "|" + (req.headers.has("nonexistent") ? "1" : "0") }
})
`
	port := startHTTPServer(t, src, 8953)
	httpReq, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	// Sent with mixed case — req.headers.get() uses a lowercased key, so a
	// lowercase lookup must still find it (case-insensitive per HTTP).
	httpReq.Header.Set("X-Test-Header", "hello")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello|0" {
		t.Errorf("body: got %q, want %q", string(body), "hello|0")
	}
}

func TestE2EHTTPListenQueryStringParsing(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8954, (req: HttpRequest): Res => {
  return { status: 200, body: req.path + "|" + req.query.get("a") + "|" + req.query.get("b") + "|" + (req.query.has("flag") ? "1" : "0") + "|" + req.query.get("flag") }
})
`
	port := startHTTPServer(t, src, 8954)
	// "b"'s value is percent-encoded ("two words" / "&") and "flag" is a
	// bare flag with no "=" — req.path must NOT include any of this.
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/some/path?a=1&b=two%%20words&flag", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	want := "/some/path|1|two words|1|"
	if string(body) != want {
		t.Errorf("body: got %q, want %q", string(body), want)
	}
}

func TestE2EHTTPListenNoQueryStringGivesEmptyMap(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8955, (req: HttpRequest): Res => {
  return { status: 200, body: req.path + "|" + (req.query.has("anything") ? "1" : "0") }
})
`
	port := startHTTPServer(t, src, 8955)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/plain", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "/plain|0" {
		t.Errorf("body: got %q, want %q", string(body), "/plain|0")
	}
}

func TestE2EHTTPListenRequestBody(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8956, (req: HttpRequest): Res => {
  return { status: 200, body: req.body }
})
`
	port := startHTTPServer(t, src, 8956)
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/", port), "application/json", strings.NewReader(`{"k":"v"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"k":"v"}` {
		t.Errorf("body: got %q, want %q", string(body), `{"k":"v"}`)
	}
}

func TestE2EHTTPListenNoBodyGivesEmptyString(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8957, (req: HttpRequest): Res => {
  return { status: 200, body: "[" + req.body + "]" }
})
`
	port := startHTTPServer(t, src, 8957)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "[]" {
		t.Errorf("body: got %q, want %q — req.body should be an empty string, not null, when no body was sent", string(body), "[]")
	}
}

// TestE2EHTTPListenLargeBodySpanningMultipleReads is the real point of
// ADR-00072's read-loop redesign: buildHTTPDispatcher's buffer must
// accumulate across as many read() calls as it takes (growing via realloc)
// until Content-Length bytes have actually arrived, rather than assuming
// one read() call returns an entire request. 200KB comfortably exceeds the
// original fixed 8KB one-shot buffer this replaced.
func TestE2EHTTPListenLargeBodySpanningMultipleReads(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8958, (req: HttpRequest): Res => {
  return { status: 200, body: "len=" + req.body.length }
})
`
	port := startHTTPServer(t, src, 8958)
	const size = 200_000
	var b strings.Builder
	b.Grow(size)
	for i := 0; i < size; i++ {
		b.WriteByte(byte('A' + i%26))
	}
	largeBody := b.String()

	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/", port), "text/plain", strings.NewReader(largeBody))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	want := fmt.Sprintf("len=%d", size)
	if string(respBody) != want {
		t.Errorf("body: got %q, want %q", string(respBody), want)
	}
}

// TestE2EHTTPListenLargeBodyContentIntegrity is the companion check to the
// size test above: not just that the length comes out right, but that the
// actual bytes survive the buffer-growth/accumulation path uncorrupted —
// echoes the full body back and compares it byte-for-byte.
func TestE2EHTTPListenLargeBodyContentIntegrity(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8959, (req: HttpRequest): Res => {
  return { status: 200, body: req.body }
})
`
	port := startHTTPServer(t, src, 8959)
	const size = 100_000
	var b strings.Builder
	b.Grow(size)
	for i := 0; i < size; i++ {
		b.WriteByte(byte('0' + i%10))
	}
	largeBody := b.String()

	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/", port), "text/plain", strings.NewReader(largeBody))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if string(respBody) != largeBody {
		t.Errorf("echoed body corrupted: got %d bytes, want %d bytes (content mismatch)", len(respBody), len(largeBody))
	}
}

func TestE2EHTTPListenWrongHeadersFieldTypeRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `
import http from 'klain:http'
interface Res { status: number; body: string; headers: string }
http.listen(8962, (req: HttpRequest): Res => { return { status: 200, body: "x", headers: "not a map" } })
`)
	if err == nil {
		t.Fatal("expected a compile error for a 'headers' field that isn't Map<string, string>, got none")
	}
}

func TestE2EHTTPListenResponseHeaders(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string; headers: Map<string, string> }
http.listen(8960, (req: HttpRequest): Res => {
  let h: Map<string, string> = new Map<string, string>()
  h.set("X-Custom-Header", "custom-value")
  h.set("Content-Type", "application/json")
  return { status: 200, body: "ok", headers: h }
})
`
	port := startHTTPServer(t, src, 8960)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Custom-Header"); got != "custom-value" {
		t.Errorf("X-Custom-Header: got %q, want %q", got, "custom-value")
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", got, "application/json")
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body: got %q, want %q", string(body), "ok")
	}
}

func TestE2EHTTPListenNoResponseHeadersUnchanged(t *testing.T) {
	// A handler with no `headers` field at all must behave byte-identically
	// to before response headers existed — no extra branches, no stray
	// blank line or header text.
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8961, (req: HttpRequest): Res => {
  return { status: 200, body: "plain" }
})
`
	port := startHTTPServer(t, src, 8961)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "plain" {
		t.Errorf("body: got %q, want %q", string(body), "plain")
	}
}

// --- Binary-safe request/response bodies (TDD-00026/ADR-00106) ---

// TestE2EHTTPListenBodyBytesRoundTripSurvivesEmbeddedNull is the real point
// of this feature: req.body/Res.body are plain null-terminated C strings, so
// a body containing an embedded null byte silently truncates through them —
// req.bodyBytes()/Res.bodyBytes carry the real byte count instead (an
// ArrayBuffer, TDD-00018), so echoing a binary payload straight through both
// accessors must come back byte-for-byte, null and all.
// TestE2EHTTPListenStringBodySurvivesEmbeddedNull is the TDD-00120 Stage 4
// payoff: the *plain string* req.body (not req.bodyBytes()) now round-trips an
// embedded null byte, because the request buffer, the string value, and the
// response writer all carry/read the header length instead of a strlen bound.
// Before the binary-safe consumer switch this truncated at the first \0, which
// is exactly why bodyBytes() existed as the escape hatch. Returned as body:
// string, so both the stored string and the string→socket write are exercised.
func TestE2EHTTPListenStringBodySurvivesEmbeddedNull(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8971, (req: HttpRequest): Res => {
  return { status: 200, body: req.body }
})
`
	port := startHTTPServer(t, src, 8971)
	payload := []byte{0x41, 0x42, 0x00, 0x43, 0x44, 0x00, 0x00, 0x45}
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/", port), "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, payload) {
		t.Errorf("body: got %v, want %v — an embedded null byte should survive the round trip through the plain string req.body", got, payload)
	}
	if cl := resp.Header.Get("Content-Length"); cl != fmt.Sprintf("%d", len(payload)) {
		t.Errorf("Content-Length: got %q, want %q", cl, fmt.Sprintf("%d", len(payload)))
	}
}

func TestE2EHTTPListenBodyBytesRoundTripSurvivesEmbeddedNull(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string; bodyBytes: ArrayBuffer }
http.listen(8963, (req: HttpRequest): Res => {
  const buf: ArrayBuffer = req.bodyBytes()
  return { status: 200, body: "", bodyBytes: buf }
})
`
	port := startHTTPServer(t, src, 8963)
	payload := []byte{0x41, 0x42, 0x00, 0x43, 0x44, 0x00, 0x00, 0x45}
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/", port), "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, payload) {
		t.Errorf("body: got %v, want %v — an embedded null byte should survive the round trip through req.bodyBytes()/Res.bodyBytes", got, payload)
	}
	if cl := resp.Header.Get("Content-Length"); cl != fmt.Sprintf("%d", len(payload)) {
		t.Errorf("Content-Length: got %q, want %q", cl, fmt.Sprintf("%d", len(payload)))
	}
}

// TestE2EHTTPListenBodyBytesWinsOverBodyField confirms the documented
// resolution to TDD-00026's "which field wins when both are set" open
// question: a non-null bodyBytes wins outright over body's own (much
// longer, in this test) string content.
func TestE2EHTTPListenBodyBytesWinsOverBodyField(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string; bodyBytes: ArrayBuffer }
http.listen(8964, (req: HttpRequest): Res => {
  const buf: ArrayBuffer = new ArrayBuffer(3)
  return { status: 200, body: "this string is much longer than 3 bytes and must be ignored", bodyBytes: buf }
})
`
	port := startHTTPServer(t, src, 8964)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	want := []byte{0, 0, 0} // ArrayBuffer is zero-initialized (real JS semantics)
	if !bytes.Equal(got, want) {
		t.Errorf("body: got %v (len %d), want %v — bodyBytes should win over the much-longer body field", got, len(got), want)
	}
}

// TestE2EHTTPListenBodyBytesByteLength confirms req.bodyBytes().byteLength
// reports the real byte count, independent of any string/strlen semantics.
func TestE2EHTTPListenBodyBytesByteLength(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8965, (req: HttpRequest): Res => {
  const buf: ArrayBuffer = req.bodyBytes()
  return { status: 200, body: "len=" + buf.byteLength }
})
`
	port := startHTTPServer(t, src, 8965)
	payload := []byte{0x41, 0x00, 0x42}
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/", port), "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	want := fmt.Sprintf("len=%d", len(payload))
	if string(got) != want {
		t.Errorf("body: got %q, want %q", string(got), want)
	}
}

func TestE2EHTTPListenWrongBodyBytesFieldTypeRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `
import http from 'klain:http'
interface Res { status: number; body: string; bodyBytes: string }
http.listen(8966, (req: HttpRequest): Res => { return { status: 200, body: "x", bodyBytes: "not an ArrayBuffer" } })
`)
	if err == nil {
		t.Fatal("expected a compile error for a 'bodyBytes' field that isn't ArrayBuffer, got none")
	}
}

// TestE2EHTTPListenClusteringMultipleWorkerPIDs (TDD-00025) is the real
// correctness check for multi-process clustering: it's not enough that the
// binary starts and answers one request — fork() + a shared listening
// socket + a non-blocking accept() all have to work together for more than
// one worker to actually end up serving traffic. Requests are fired
// concurrently (not sequentially) since a single in-flight connection at a
// time gives the kernel no real distribution pressure — the same worker
// could plausibly win every sequential accept() race.
func TestE2EHTTPListenClusteringMultipleWorkerPIDs(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8963, (req: HttpRequest): Res => {
  return { status: 200, body: process.pid.toString() }
}, { workers: 3 })
`
	port := startHTTPClusterServer(t, src, 8963)

	const n = 40
	results := make(chan string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
			if err != nil {
				results <- ""
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			results <- string(body)
		}()
	}
	wg.Wait()
	close(results)

	seen := map[string]int{}
	for r := range results {
		if r != "" {
			seen[r]++
		}
	}
	if len(seen) < 2 {
		t.Fatalf("expected %d concurrent requests to be served by more than one distinct worker PID (proves fork+shared-listener+non-blocking-accept work together, not just that the binary starts), got only %v", n, seen)
	}
}

// TestE2EHTTPClusterCombinedWithCreateServer (ADR-00989): a klain:http
// `http.listen(..., { workers: N })` cluster and an additional Node
// `http.createServer(...).listen(...)` in one program — previously a clean
// rejection. Both ports must answer, and the cluster port must be served by more
// than one worker PID (proving the fork happened after both listeners were bound
// and every worker inherited both).
func TestE2EHTTPClusterCombinedWithCreateServer(t *testing.T) {
	p1 := freePort(t) // Node createServer (primary)
	p2 := freePort(t) // klain:http cluster (additional listener)
	src := fmt.Sprintf(`
import http from 'http'
import khttp from 'klain:http'
interface Res { status: number; body: string }
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200)
  res.end("extra")
}).listen(%d)
khttp.listen(%d, (req: HttpRequest): Res => {
  return { status: 200, body: process.pid.toString() }
}, { workers: 3 })
`, p1, p2)
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	setProcGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		killProcGroup(cmd)
		_ = cmd.Wait()
		waitPortFree(fmt.Sprintf("127.0.0.1:%d", p1))
		waitPortFree(fmt.Sprintf("127.0.0.1:%d", p2))
	})
	waitListening(t, p1)
	waitListening(t, p2)

	get := func(port int) string {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err != nil {
			return ""
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}

	// The additional Node server answers.
	if b := get(p1); b != "extra" {
		t.Errorf("createServer port body: got %q, want %q", b, "extra")
	}

	// The cluster port answers and is served by more than one worker PID —
	// concurrent requests (not sequential, which a shared-accept cluster routes
	// to whichever worker wins the race, usually the same one).
	const n = 40
	results := make(chan string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- get(p2)
		}()
	}
	wg.Wait()
	close(results)
	seen := map[string]int{}
	for r := range results {
		if r != "" {
			seen[r]++
		}
	}
	if len(seen) < 2 {
		t.Fatalf("expected the cluster port to be served by more than one worker PID, got %v", seen)
	}
}

// TestE2EHTTPListenClusteringDefaultIsSingleProcess confirms the two-argument
// form (no workers option) forks nothing — cluster.isPrimary must read true
// and cluster.workerId 0, byte-identical to today's single-process behavior.
func TestE2EHTTPListenClusteringDefaultIsSingleProcess(t *testing.T) {
	src := `
import http from 'klain:http'
import cluster from 'cluster'
interface Res { status: number; body: string }
http.listen(8964, (req: HttpRequest): Res => {
  return { status: 200, body: (cluster.isPrimary ? "primary" : "worker") + " " + cluster.workerId.toString() }
})
`
	port := startHTTPServer(t, src, 8964)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "primary 0" {
		t.Errorf("body: got %q, want %q", string(body), "primary 0")
	}
}

// TestE2EHTTPListenClusteringFlushesStdoutBeforeFork guards against a real
// bug found while testing this feature: fork() duplicates libc's stdio
// buffers verbatim, so console.log output written before http.listen's
// clustering fork (and not yet flushed — the case whenever stdout isn't a
// TTY, e.g. piped, which is exactly how this test — and any real containerized
// deployment's log collector — reads it) got printed once per worker instead
// of once. __kml_http_cluster_fork now fflush(NULL)s right before each
// fork(). Deliberately pipes the child's stdout (exec.Command's default,
// not a TTY) rather than checking against a real terminal, since the bug
// only manifested in the piped/non-TTY case.
func TestE2EHTTPListenClusteringFlushesStdoutBeforeFork(t *testing.T) {
	skipForkOnlyOnWindows(t, "asserts code before http.listen prints once")
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
console.log("BANNER")
http.listen(8965, (req: HttpRequest): Res => {
  return { status: 200, body: "ok" }
}, { workers: 4 })
`
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	setProcGroup(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	addr := "127.0.0.1:8965"
	t.Cleanup(func() {
		killProcGroup(cmd)
		_ = cmd.Wait()
		waitPortFree(addr)
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Give every forked worker a moment to have run its own copy of the
	// top-level console.log (were the bug still present) before checking.
	time.Sleep(200 * time.Millisecond)

	count := strings.Count(out.String(), "BANNER")
	if count != 1 {
		t.Errorf("expected \"BANNER\" to appear exactly once (printed before the 4-worker fork), got %d times: %q", count, out.String())
	}
}

// TestE2EHTTPListenClusterCloseReachesAllWorkers is the decisive test for
// TDD-00117: a single http.close() from one worker of a { workers: N } cluster
// must shut the whole cluster down, not just the process that served the call.
// The N workers share one inherited listening socket (fork duplicates the fd),
// so the port stays accepting until *every* worker closes its own copy — a
// connection-refused on the port after a single /shutdown therefore proves all
// three workers closed, i.e. the shared close flag reached the siblings. Before
// TDD-00117 the other N-1 workers kept the socket open and this would hang until
// the deadline.
func TestE2EHTTPListenClusterCloseReachesAllWorkers(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8974, (req: HttpRequest): Res => {
  if (req.path === '/shutdown') {
    http.close()
    return { status: 200, body: "shutting down" }
  }
  return { status: 200, body: process.pid.toString() }
}, { workers: 3 })
`
	port := startHTTPClusterServer(t, src, 8974)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/shutdown", port))
	if err != nil {
		t.Fatalf("GET /shutdown: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "shutting down" {
		t.Errorf("body = %q, want %q", body, "shutting down")
	}

	// Every worker polls the shared flag on a ≤200ms cadence and then drains, so
	// the port should stop accepting well within a few seconds. If cross-worker
	// close were broken, the two workers that didn't serve /shutdown would keep
	// the inherited socket open and this loop would never see a refusal.
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			return // refused/unreachable — the whole cluster stopped accepting
		}
		conn.Close()
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("port %s still accepting 5s after a single /shutdown — the other workers never closed (cross-worker close did not reach them)", addr)
}

// TestE2EHTTPListenClusteringGCModeMultipleWorkerPIDs is the GC-mode
// counterpart of TestE2EHTTPListenClusteringMultipleWorkerPIDs and
// TestE2EHTTPListenGCModeConcurrentChurn combined: proves multi-process
// clustering and the Boehm collector coexist correctly, not just that each
// works in isolation. Every request does real allocation churn (same
// shape/scale as TestE2EHTTPListenGCModeConcurrentChurn above) specifically
// to make a collection plausible while a fork() could also be in flight —
// the exact "fork mid-collection" race ADR-00099's GC_set_handle_fork(1)
// fix targets. Correct, uncorrupted totals across every response is the
// evidence collections and forking aren't corrupting each other; more than
// one distinct worker PID answering is the evidence clustering itself
// still works under -mm=gc.
func TestE2EHTTPListenClusteringGCModeMultipleWorkerPIDs(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8966, (req: HttpRequest): Res => {
  let total = 0;
  for (let i = 0; i < 100000; i++) {
    let s: string = "abcdefghijklmnopqrstuvwxyz0123456789" + "abcdefghijklmnopqrstuvwxyz0123456789";
    total = total + s.length;
  }
  return { status: 200, body: process.pid.toString() + ":" + total.toString() };
}, { workers: 3 })
`
	port := startHTTPClusterServerGC(t, src, 8966)

	const n = 20
	// 100,000 iterations * 72 (two concatenated 36-byte segments).
	const wantTotal = "7200000"

	results := make(chan string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
			if err != nil {
				results <- ""
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			results <- string(body)
		}()
	}
	wg.Wait()
	close(results)

	seenPIDs := map[string]int{}
	for r := range results {
		if r == "" {
			t.Error("a request failed")
			continue
		}
		pid, total, ok := strings.Cut(r, ":")
		if !ok || total != wantTotal {
			t.Errorf("body: got %q, want \"<pid>:%s\"", r, wantTotal)
			continue
		}
		seenPIDs[pid]++
	}
	if len(seenPIDs) < 2 {
		t.Fatalf("expected %d concurrent requests to be served by more than one distinct worker PID under -mm=gc, got only %v", n, seenPIDs)
	}
}

// TestE2EHTTPRequestObjectKeysHidesInternals: Object.keys(req) must expose only
// the user-facing HttpRequest surface (method/path/query/headers/body), not the
// implementation-only bodyLength/__kml_bodyctx fields that back .bodyBytes() and
// .stream() (VisibleFields' IsRequest case).
func TestE2EHTTPRequestObjectKeysHidesInternals(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8976, (req: HttpRequest): Res => {
  return { status: 200, body: Object.keys(req).join(",") }
})
`
	port := startHTTPServer(t, src, 8976)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/hi", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	got := string(body)
	if got != "method,path,query,headers,body" {
		t.Errorf("Object.keys(req) = %q, want %q (internal fields must not leak)", got, "method,path,query,headers,body")
	}
	for _, internal := range []string{"bodyLength", "__kml_bodyctx"} {
		if strings.Contains(got, internal) {
			t.Errorf("Object.keys(req) leaked internal field %q: %q", internal, got)
		}
	}
}

// --- http.close() (TDD-00027) ---

// TestE2EHTTPListenCloseExitsProcess is the decisive test for TDD-00027: a
// handler calling http.close() must let http.listen()'s own call actually
// return (rather than the process just running forever, or being killed
// externally like every other http.listen test's t.Cleanup does), letting
// whatever top-level code follows it run for real. Uses
// startBackgroundServer/waitExit (signals_test.go) rather than
// startHTTPServer, since — unlike every other server test in this file —
// this process is expected to exit on its own.
func TestE2EHTTPListenCloseExitsProcess(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }

http.listen(8967, (req: HttpRequest): Res => {
  if (req.path === '/shutdown') {
    http.close()
    return { status: 200, body: "shutting down" }
  }
  return { status: 200, body: "ok" }
})
console.log("after listen returned")
`
	cmd, out, port := startBackgroundServer(t, src, 8967)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/shutdown", port))
	if err != nil {
		t.Fatalf("GET /shutdown: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "shutting down" {
		t.Errorf("body = %q, want %q", body, "shutting down")
	}

	code := waitExit(t, cmd, out, 5*time.Second)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; output:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "after listen returned") {
		t.Errorf("code after http.listen() never ran; output:\n%s", out.String())
	}
}

// TestE2EHTTPListenCloseIsIdempotent confirms calling http.close() more than
// once (here, from two different requests) doesn't crash or otherwise
// misbehave — __kml_http_close is a no-op once @__kml_listen_fd is already
// -1.
func TestE2EHTTPListenCloseIsIdempotent(t *testing.T) {
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }

http.listen(8968, (req: HttpRequest): Res => {
  http.close()
  http.close()
  return { status: 200, body: "ok" }
})
console.log("after listen returned")
`
	cmd, out, port := startBackgroundServer(t, src, 8968)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	resp.Body.Close()

	code := waitExit(t, cmd, out, 5*time.Second)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; output:\n%s", code, out.String())
	}
}

// TestE2EHTTPCloseAllConnectionsTerminatesInFlight is the decisive test for
// TDD-00118: http.closeAllConnections() must forcefully terminate an in-flight
// connection, not wait for it to finish. A raw client sends only a partial
// request (no terminating blank line), so its server-side fiber parks mid-read
// with the connection still open. A second request then calls
// http.closeAllConnections(); the partial connection must be shut down — the
// raw client's next read returns EOF promptly. Without the force-close it would
// sit parked until the read deadline.
func TestE2EHTTPCloseAllConnectionsTerminatesInFlight(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8975, (req: HttpRequest): Res => {
  if (req.path === '/closeall') {
    http.closeAllConnections()
    return { status: 200, body: "closed all" }
  }
  return { status: 200, body: "ok" }
})
`
	port := startHTTPServer(t, src, 8975)

	// A raw connection that never completes its request — the server fiber parks
	// mid-read, keeping this connection in the active registry.
	raw, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial raw: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Write([]byte("GET /wait HTTP/1.1\r\nHost: x\r\n")); err != nil {
		t.Fatalf("write partial: %v", err)
	}

	// Trigger the force-close from a second connection. The /closeall response
	// may not arrive (that connection is force-closed too, matching Node), so
	// don't assert on it — fire it and move on.
	go func() {
		c := &http.Client{Timeout: 2 * time.Second}
		if resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/closeall", port)); err == nil {
			resp.Body.Close()
		}
	}()

	// The partial connection must now hit EOF promptly.
	raw.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, err := raw.Read(buf)
	if err == nil && n > 0 {
		// A well-behaved force-close may first flush a partial/empty response,
		// but the connection must still end — one more read must reach EOF.
		raw.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, err = raw.Read(buf)
	}
	if err == nil {
		t.Fatalf("in-flight connection was not force-closed by closeAllConnections() — read succeeded instead of hitting EOF")
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("in-flight connection was not force-closed — read timed out instead of EOF (%v)", err)
	}
}

// TestE2EHTTPListenSecondCallSiteRejected: http.close() makes a second,
// textually-later http.listen() call genuinely reachable at runtime for the
// first time (the first call no longer necessarily runs forever) — without
// emitHTTPListen's httpListenCallSeen guard, this would otherwise compile
// and fail obscurely at the LLVM backend (a duplicate @__kml_http_dispatch
// definition) instead of with a clear compile-time error.
func TestE2EHTTPListenSecondCallSiteRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8969, (req: HttpRequest): Res => { http.close(); return { status: 200, body: "a" } })
http.listen(8970, (req: HttpRequest): Res => { return { status: 200, body: "b" } })
`)
	if err == nil {
		t.Fatal("expected a compile error for a second http.listen call site, got none")
	}
	if !strings.Contains(err.Error(), "http.listen may only be called once") {
		t.Errorf("error = %v, want it to mention the once-per-program limitation", err)
	}
}

// TestE2EHTTPCloseWrongArgCountRejected mirrors
// TestE2EHTTPListenWrongArgCountRejected's pattern for the new function.
func TestE2EHTTPCloseWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `import http from 'klain:http'
http.close(1)`)
	if err == nil {
		t.Fatal("expected a compile error for http.close with an argument, got none")
	}
}

// TestE2EHTTPListenHTTP2Cleartext exercises the nghttp2 h2c server (TDD-00111
// Stage 3a): an http.listen server transparently serves an HTTP/2 cleartext
// (prior-knowledge) client, dispatching through the same handler as HTTP/1.1.
// Uses curl --http2-prior-knowledge (skipped if curl is absent), the same
// posture the tls tests take toward an external client.
// nativeCurl returns a curl that does not mangle its arguments. The MSYS2
// runtime curl (msys-2.0.dll, typically C:\msys64\usr\bin\curl.exe, on PATH
// for the coreutils the child_process tests need) glob-expands its argv the
// Cygwin way, so a `-w '%{http_version}'` write-out arrives as `%http_version`
// (braces stripped) and `\n` as `n` — which made the HTTP/2, HTTPS and h2c
// version assertions fail on the CI runner while passing locally where the
// native curl was found first (ADR-00746). Prefer the native mingw curl from
// the compiler's UCRT sysroot (it also carries nghttp2 for h2/h2c), then any
// curl on PATH. POSIX is unaffected.
func nativeCurl() string {
	if runtime.GOOS != "windows" {
		return "curl"
	}
	sysroot := os.Getenv("KLAIN_SYSROOT")
	if sysroot == "" {
		sysroot = `C:\msys64\ucrt64`
	}
	if c := filepath.Join(sysroot, "bin", "curl.exe"); fileExists(c) {
		return c
	}
	if p, err := exec.LookPath("curl"); err == nil {
		return p
	}
	return "curl"
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func TestE2EHTTPListenHTTP2Cleartext(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found in PATH")
	}
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8961, (req: HttpRequest): Res => {
  return { status: 200, body: "h2:" + req.method + ":" + req.path + ":" + req.body }
})
`
	port := startHTTPServer(t, src, 8961)

	// h2c GET
	out, err := exec.Command(nativeCurl(), "-s", "--http2-prior-knowledge",
		fmt.Sprintf("http://127.0.0.1:%d/hello", port), "-w", "|%{http_version}").CombinedOutput()
	if err != nil {
		t.Fatalf("curl h2c GET: %v\n%s", err, out)
	}
	if got := string(out); got != "h2:GET:/hello:|2" {
		t.Errorf("h2c GET: got %q, want %q", got, "h2:GET:/hello:|2")
	}

	// h2c POST with a body
	out, err = exec.Command(nativeCurl(), "-s", "--http2-prior-knowledge",
		"-d", "payload", fmt.Sprintf("http://127.0.0.1:%d/submit", port), "-w", "|%{http_version}").CombinedOutput()
	if err != nil {
		t.Fatalf("curl h2c POST: %v\n%s", err, out)
	}
	if got := string(out); got != "h2:POST:/submit:payload|2" {
		t.Errorf("h2c POST: got %q, want %q", got, "h2:POST:/submit:payload|2")
	}

	// HTTP/1.1 on the same server still works
	out, err = exec.Command(nativeCurl(), "-s", "--http1.1",
		fmt.Sprintf("http://127.0.0.1:%d/one", port), "-w", "|%{http_version}").CombinedOutput()
	if err != nil {
		t.Fatalf("curl 1.1: %v\n%s", err, out)
	}
	if got := string(out); got != "h2:GET:/one:|1.1" {
		t.Errorf("1.1: got %q, want %q", got, "h2:GET:/one:|1.1")
	}
}

func TestE2EHTTPCreateServerNodeTestIdiom(t *testing.T) {
	// The full shape Node's own tests use: mustCall-wrapped untyped handler and
	// callbacks, listen(0), options-object http.get with the ephemeral port,
	// server.close() from inside the response flow — with the mustCall counts
	// verified at exit and the client response still delivered after the loop
	// winds down (post-loop reaction flush).
	src := `
import http from 'http'
import { mustCall } from 'test'
const server = http.createServer(mustCall((req, res) => {
  res.end("resp:" + req.path)
  server.close()
}))
server.listen(0, mustCall(() => {
  http.get({ port: server.address().port, path: "/req7" }, mustCall((res) => {
    let data = ""
    res.on('data', (chunk: string) => { data = data + chunk })
    res.on('end', () => { console.log("got", data) })
  }))
}))
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "got resp:/req7") {
		t.Errorf("response never delivered: %q", out)
	}
}

func TestE2EHTTP2ModuleCreateServer(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	// TDD-00139 Stage 1: the explicit http2 module's createServer — shares the
	// http server core, which speaks h2c (prior-knowledge cleartext HTTP/2) on
	// the same port. Verified with curl forcing HTTP/2.
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found in PATH")
	}
	src := `
import http2 from 'http2'
const server = http2.createServer((req, res) => {
  res.writeHead(200)
  res.end("h2mod:" + req.path)
})
server.listen(8983)
`
	port := startHTTPServer(t, src, 8983)
	out, err := exec.Command(nativeCurl(), "-s", "--http2-prior-knowledge",
		fmt.Sprintf("http://127.0.0.1:%d/y", port), "-w", "|%{http_version}").CombinedOutput()
	if err != nil {
		t.Fatalf("curl h2c: %v\n%s", err, out)
	}
	if got := string(out); got != "h2mod:/y|2" {
		t.Errorf("h2 response: got %q, want %q", got, "h2mod:/y|2")
	}
}

func TestE2EHTTP2MultiInstanceH2C(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	// TDD-00191 Stage 4: two http2.createServer instances (h2c prior-knowledge)
	// on different ports, each routing to its OWN handler via its per-server h2
	// bridge vtable (the C nghttp2 driver calls through the connection's vtable).
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found in PATH")
	}
	pa, pb := freePort(t), freePort(t)
	src := `
import http2 from 'http2'
const a = http2.createServer((req, res) => { res.writeHead(200); res.end("A:" + req.path) })
const b = http2.createServer((req, res) => { res.writeHead(200); res.end("B:" + req.path) })
a.listen(19901, () => { b.listen(19902, () => { console.log("ready") }) })
`
	src = strings.ReplaceAll(src, "19901", fmt.Sprintf("%d", pa))
	src = strings.ReplaceAll(src, "19902", fmt.Sprintf("%d", pb))
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitListening(t, pa)
	waitListening(t, pb)

	for _, tc := range []struct {
		port int
		path string
		want string
	}{{pa, "/x", "A:/x|2"}, {pb, "/y", "B:/y|2"}} {
		out, err := exec.Command(nativeCurl(), "-s", "--http2-prior-knowledge",
			fmt.Sprintf("http://127.0.0.1:%d%s", tc.port, tc.path), "-w", "|%{http_version}").CombinedOutput()
		if err != nil {
			t.Fatalf("curl h2c %d: %v\n%s", tc.port, err, out)
		}
		if got := string(out); got != tc.want {
			t.Errorf("h2c on %d: got %q, want %q", tc.port, got, tc.want)
		}
	}
}

func TestE2EHTTP2MultiInstanceSecure(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	// TDD-00191 Stage 4: a plain HTTP primary plus an additional
	// http2.createSecureServer (h2 over TLS) on a second port. The additional
	// listener carries its SSL_CTX and its h2 vtable in the extra-listener table;
	// an ALPN-"h2" accept is driven as an nghttp2 session routed to its own
	// handler, while the primary stays plain.
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found in PATH")
	}
	certLit, keyLit := genSelfSignedPEM(t)
	pa, pb := freePort(t), freePort(t)
	src := fmt.Sprintf(`
import http from 'http'
import http2 from 'http2'
const cert = "%s"
const key = "%s"
const plain = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200); res.end("plain:" + req.url)
})
const secure = http2.createSecureServer({ cert: cert, key: key }, (req, res) => {
  res.writeHead(200); res.end("h2:" + req.path)
})
plain.listen(19911, () => { secure.listen(19912, () => { console.log("ready") }) })
`, certLit, keyLit)
	src = strings.ReplaceAll(src, "19911", fmt.Sprintf("%d", pa))
	src = strings.ReplaceAll(src, "19912", fmt.Sprintf("%d", pb))
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitListening(t, pa)
	waitListening(t, pb)

	// Plain HTTP on the primary.
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/p", pa))
	if err != nil {
		t.Fatalf("GET plain: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if got := string(body); got != "plain:/p" {
		t.Errorf("plain server: got %q, want %q", got, "plain:/p")
	}

	// h2 over TLS on the additional server (curl --http2 negotiates h2 via ALPN).
	out, err := exec.Command(nativeCurl(), "-sk", "--http2",
		fmt.Sprintf("https://127.0.0.1:%d/h", pb), "-w", "|%{http_version}").CombinedOutput()
	if err != nil {
		t.Fatalf("curl h2: %v\n%s", err, out)
	}
	if got := string(out); got != "h2:/h|2" {
		t.Errorf("secure h2 server: got %q, want %q", got, "h2:/h|2")
	}
}

func TestE2EHTTP2SecureServer(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	// TDD-00111 Stage 3b: http2.createSecureServer — h2 over TLS. The accepted
	// fd is TLS-handshaken, ALPN selects h2, and the connection drives the same
	// nghttp2 session as h2c but over the SSL read/write shims. curl --http2
	// negotiates h2 via ALPN; -k accepts the self-signed fixture cert.
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found in PATH")
	}
	certLit, keyLit := genSelfSignedPEM(t)
	src := fmt.Sprintf(`
import http2 from 'http2'
const cert = "%s"
const key = "%s"
const server = http2.createSecureServer({ cert: cert, key: key }, (req, res) => {
  res.writeHead(200)
  res.end("h2tls:" + req.method + ":" + req.path)
})
server.listen(8985)
`, certLit, keyLit)
	port := startHTTPServer(t, src, 8985)

	out, err := exec.Command(nativeCurl(), "-sk", "--http2",
		fmt.Sprintf("https://127.0.0.1:%d/hello", port), "-w", "|%{http_version}").CombinedOutput()
	if err != nil {
		t.Fatalf("curl h2-tls GET: %v\n%s", err, out)
	}
	if got := string(out); got != "h2tls:GET:/hello|2" {
		t.Errorf("h2-tls GET: got %q, want %q", got, "h2tls:GET:/hello|2")
	}

	// A 1.1-only TLS client offers no h2 ALPN — createSecureServer is h2-only
	// (Node's allowHTTP1:false default), so the connection is dropped without a
	// response. curl exits non-zero; the server must survive to serve h2 again.
	_, err = exec.Command(nativeCurl(), "-sk", "--http1.1", "--max-time", "3",
		fmt.Sprintf("https://127.0.0.1:%d/one", port)).CombinedOutput()
	if err == nil {
		t.Errorf("1.1-only TLS client: want rejection, got success")
	}
	out, err = exec.Command(nativeCurl(), "-sk", "--http2",
		fmt.Sprintf("https://127.0.0.1:%d/after", port), "-w", "|%{http_version}").CombinedOutput()
	if err != nil {
		t.Fatalf("curl h2-tls after reject: %v\n%s", err, out)
	}
	if got := string(out); got != "h2tls:GET:/after|2" {
		t.Errorf("h2-tls after reject: got %q, want %q", got, "h2tls:GET:/after|2")
	}
}

func TestE2EHTTPSServer(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	// TDD-00111: https.createServer — HTTPS/1.1 over TLS. The accepted fd is
	// TLS-handshaken; the h1-only ALPN forces even an h2-capable client to 1.1,
	// and the connection is served by the ordinary fiber dispatcher whose socket
	// I/O is routed through the SSL shims. curl -k accepts the self-signed cert.
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found in PATH")
	}
	certLit, keyLit := genSelfSignedPEM(t)
	src := fmt.Sprintf(`
import https from 'https'
const cert = "%s"
const key = "%s"
const server = https.createServer({ cert: cert, key: key }, (req, res) => {
  res.writeHead(200)
  res.end("https:" + req.method + ":" + req.path + ":" + req.body)
})
server.listen(8987)
`, certLit, keyLit)
	port := startHTTPServer(t, src, 8987)

	// A GET — even an h2-capable client negotiates 1.1 (h1-only ALPN).
	out, err := exec.Command(nativeCurl(), "-sk", "--http2",
		fmt.Sprintf("https://127.0.0.1:%d/hello", port), "-w", "|%{http_version}").CombinedOutput()
	if err != nil {
		t.Fatalf("curl https GET: %v\n%s", err, out)
	}
	if got := string(out); got != "https:GET:/hello:|1.1" {
		t.Errorf("https GET: got %q, want %q", got, "https:GET:/hello:|1.1")
	}

	// A POST body — exercises the Content-Length read loop over the SSL shims.
	out, err = exec.Command(nativeCurl(), "-sk", fmt.Sprintf("https://127.0.0.1:%d/echo", port),
		"-d", "payload", "-w", "|%{http_version}").CombinedOutput()
	if err != nil {
		t.Fatalf("curl https POST: %v\n%s", err, out)
	}
	if got := string(out); got != "https:POST:/echo:payload|1.1" {
		t.Errorf("https POST: got %q, want %q", got, "https:POST:/echo:payload|1.1")
	}
}

func TestE2EHTTPSResStreamBackpressure(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	// TDD-00195 Stage 2 (Layer B over TLS): a streamed HTTPS response applies real
	// socket backpressure. The TLS chunk writes route through the non-blocking
	// __kml_tls_write_nb (SSL WANT → EAGAIN), so a slow-reading TLS client parks
	// one connection fiber on writability instead of blocking the reactor inside
	// SSL_write's poll(). A blocking TLS write would hang the whole reactor here
	// and the concurrent quick request would never return. The parked stream must
	// also resume and deliver its whole body byte-for-byte intact.
	certLit, keyLit := genSelfSignedPEM(t)
	kb := strings.Repeat("x", 1024)
	src := fmt.Sprintf(`
import https from 'https'
const cert = "%s"
const key = "%s"
https.createServer({ cert: cert, key: key }, (req, res) => {
  if (req.url === "/big") {
    res.writeHead(200)
    let i = 0
    while (i < 2048) {
      res.write("`+kb+`")
      i = i + 1
    }
    res.end()
  } else {
    res.writeHead(200)
    res.end("quick")
  }
}).listen(8993)
`, certLit, keyLit)
	port := startHTTPServer(t, src, 8993)

	// Client A: request /big over TLS, read a little, then stop draining so the
	// server's send buffer fills and the streaming fiber parks on WANT/EAGAIN.
	connA, err := tls.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("tls dial A: %v", err)
	}
	defer connA.Close()
	if _, err := connA.Write([]byte("GET /big HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatalf("write A: %v", err)
	}
	brA := bufio.NewReader(connA)
	connA.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := brA.ReadString('\n'); err != nil { // status line — proves streaming began
		t.Fatalf("read A status: %v", err)
	}
	// Let the server fill A's socket buffer and park mid-stream.
	time.Sleep(300 * time.Millisecond)

	// Client B: a quick TLS request must complete while A is parked.
	done := make(chan string, 1)
	go func() {
		out, err := exec.Command(nativeCurl(), "-sk", fmt.Sprintf("https://127.0.0.1:%d/", port)).CombinedOutput()
		if err != nil {
			done <- "ERR:" + err.Error()
			return
		}
		done <- string(out)
	}()
	select {
	case got := <-done:
		if got != "quick" {
			t.Errorf("quick TLS request while a slow stream is parked: got %q, want %q", got, "quick")
		}
	case <-time.After(6 * time.Second):
		t.Fatal("quick TLS request hung — the reactor was blocked by the slow client's TLS stream (no backpressure park)")
	}

	// Now drain A fully and de-chunk: the parked stream must resume and deliver
	// the whole 2 MiB body intact through the WANT/EAGAIN park path.
	for { // skip the rest of the headers
		line, err := brA.ReadString('\n')
		if err != nil {
			t.Fatalf("read A headers: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}
	bodyLen := 0
	connA.SetReadDeadline(time.Now().Add(15 * time.Second))
	for {
		sizeLine, err := brA.ReadString('\n')
		if err != nil {
			t.Fatalf("read A chunk size: %v", err)
		}
		var sz int64
		if _, err := fmt.Sscanf(strings.TrimSpace(sizeLine), "%x", &sz); err != nil {
			t.Fatalf("parse A chunk size %q: %v", sizeLine, err)
		}
		if sz == 0 {
			break
		}
		if _, err := io.ReadFull(brA, make([]byte, sz)); err != nil {
			t.Fatalf("read A chunk body: %v", err)
		}
		if _, err := brA.Discard(2); err != nil {
			t.Fatalf("discard A chunk CRLF: %v", err)
		}
		bodyLen += int(sz)
	}
	if want := 2048 * 1024; bodyLen != want {
		t.Fatalf("de-chunked HTTPS body length: got %d, want %d (parked TLS stream dropped bytes)", bodyLen, want)
	}
}

func TestE2EHTTPSResStreamKeepAlive(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	// TDD-00195 Stage 2 (ADR-00964): a streamed HTTPS response re-arms the
	// connection on keep-alive, so two sequential requests on ONE reused TLS
	// connection both stream correctly. The sink restores O_NONBLOCK at stream
	// end, so the TLS read loop picks up the next request exactly as on plain HTTP.
	certLit, keyLit := genSelfSignedPEM(t)
	src := fmt.Sprintf(`
import https from 'https'
const cert = "%s"
const key = "%s"
https.createServer({ cert: cert, key: key }, (req, res) => {
  res.writeHead(200)
  res.write("chunk-")
  res.end("end")
}).listen(8993)
`, certLit, keyLit)
	port := startHTTPServer(t, src, 8993)
	conn, err := tls.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	for i := 0; i < 2; i++ {
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
			t.Fatalf("write req %d: %v", i, err)
		}
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatalf("read resp %d (streamed TLS keep-alive re-arm failed?): %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "chunk-end" {
			t.Errorf("req %d body: got %q, want %q", i, string(body), "chunk-end")
		}
	}
}

func TestE2EHTTPSMultiInstancePlainPrimary(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	// TDD-00191 Stage 3: a plain HTTP primary server plus an additional HTTPS
	// server on a second port. The additional listener carries its own SSL_CTX in
	// the extra-listener table, so its accepts do their own TLS handshake while
	// the primary stays plain. Also exercises the pre-scan ordering: the plain
	// primary triggers ensureHTTPRuntime first, yet the extra-accept path is still
	// emitted TLS-aware.
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found in PATH")
	}
	certLit, keyLit := genSelfSignedPEM(t)
	pa, pb := freePort(t), freePort(t)
	src := fmt.Sprintf(`
import http from 'http'
import https from 'https'
const cert = "%s"
const key = "%s"
const plain = http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200); res.end("plain:" + req.url)
})
const secure = https.createServer({ cert: cert, key: key }, (req, res) => {
  res.writeHead(200); res.end("secure:" + req.method + ":" + req.path)
})
plain.listen(19801, () => { secure.listen(19802, () => { console.log("ready") }) })
`, certLit, keyLit)
	src = strings.ReplaceAll(src, "19801", fmt.Sprintf("%d", pa))
	src = strings.ReplaceAll(src, "19802", fmt.Sprintf("%d", pb))
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitListening(t, pa)
	waitListening(t, pb)

	// Plain HTTP on the primary.
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/a", pa))
	if err != nil {
		t.Fatalf("GET plain: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if got := string(body); got != "plain:/a" {
		t.Errorf("plain server: got %q, want %q", got, "plain:/a")
	}

	// HTTPS on the additional server (curl -k for the self-signed cert).
	out, err := exec.Command(nativeCurl(), "-sk",
		fmt.Sprintf("https://127.0.0.1:%d/b", pb)).CombinedOutput()
	if err != nil {
		t.Fatalf("curl https: %v\n%s", err, out)
	}
	if got := string(out); got != "secure:GET:/b" {
		t.Errorf("secure server: got %q, want %q", got, "secure:GET:/b")
	}
}

func TestE2EHTTPSMultiInstanceTwoSecure(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	// TDD-00191 Stage 3: two HTTPS servers in one program — the primary (via the
	// @__kml_http_tls_ctx global + reactor TLS branch) and an additional one (via
	// its own SSL_CTX in the extra-listener table). Each does its own TLS
	// handshake on its own port.
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found in PATH")
	}
	certLit, keyLit := genSelfSignedPEM(t)
	pa, pb := freePort(t), freePort(t)
	src := fmt.Sprintf(`
import https from 'https'
const cert = "%s"
const key = "%s"
const a = https.createServer({ cert: cert, key: key }, (req, res) => {
  res.writeHead(200); res.end("A:" + req.path)
})
const b = https.createServer({ cert: cert, key: key }, (req, res) => {
  res.writeHead(200); res.end("B:" + req.path)
})
a.listen(19811, () => { b.listen(19812, () => { console.log("ready") }) })
`, certLit, keyLit)
	src = strings.ReplaceAll(src, "19811", fmt.Sprintf("%d", pa))
	src = strings.ReplaceAll(src, "19812", fmt.Sprintf("%d", pb))
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitListening(t, pa)
	waitListening(t, pb)

	for _, tc := range []struct {
		port int
		want string
	}{{pa, "A:/x"}, {pb, "B:/y"}} {
		path := "/x"
		if tc.port == pb {
			path = "/y"
		}
		out, err := exec.Command(nativeCurl(), "-sk",
			fmt.Sprintf("https://127.0.0.1:%d%s", tc.port, path)).CombinedOutput()
		if err != nil {
			t.Fatalf("curl https %d: %v\n%s", tc.port, err, out)
		}
		if got := string(out); got != tc.want {
			t.Errorf("server on %d: got %q, want %q", tc.port, got, tc.want)
		}
	}
}

func TestE2EHTTP2SecureServerAllowHTTP1(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	// TDD-00111: http2.createSecureServer({ allowHTTP1: true }) serves both an
	// ALPN-negotiated h2 client (nghttp2 inline drive) and a 1.1 client (routed
	// into the fiber conn table over the SSL shims) on the same TLS port.
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found in PATH")
	}
	certLit, keyLit := genSelfSignedPEM(t)
	src := fmt.Sprintf(`
import http2 from 'http2'
const cert = "%s"
const key = "%s"
const server = http2.createSecureServer({ cert: cert, key: key, allowHTTP1: true }, (req, res) => {
  res.writeHead(200)
  res.end("both:" + req.method + ":" + req.path)
})
server.listen(8988)
`, certLit, keyLit)
	port := startHTTPServer(t, src, 8988)

	out, err := exec.Command(nativeCurl(), "-sk", "--http2",
		fmt.Sprintf("https://127.0.0.1:%d/h2", port), "-w", "|%{http_version}").CombinedOutput()
	if err != nil {
		t.Fatalf("curl h2: %v\n%s", err, out)
	}
	if got := string(out); got != "both:GET:/h2|2" {
		t.Errorf("allowHTTP1 h2: got %q, want %q", got, "both:GET:/h2|2")
	}

	out, err = exec.Command(nativeCurl(), "-sk", "--http1.1",
		fmt.Sprintf("https://127.0.0.1:%d/one", port), "-w", "|%{http_version}").CombinedOutput()
	if err != nil {
		t.Fatalf("curl 1.1 fallback: %v\n%s", err, out)
	}
	if got := string(out); got != "both:GET:/one|1.1" {
		t.Errorf("allowHTTP1 1.1: got %q, want %q", got, "both:GET:/one|1.1")
	}
}

func TestE2EHTTP2StreamsAPI(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	// TDD-00139 Stage 2: the core streams API — server.on('stream', (stream,
	// headers)), pseudo-header reads via Map bracket access, stream.respond
	// with :status + a response header, stream.end body — verified over real
	// HTTP/2 (h2c) with curl.
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found in PATH")
	}
	src := `
import http2 from 'http2'
const server = http2.createServer()
server.on('stream', (stream, headers) => {
  stream.respond({ ':status': 201, 'x-served-by': 'kml' })
  stream.end("p=" + headers[':path'] + " m=" + headers[':method'])
})
server.listen(8984)
`
	port := startHTTPServer(t, src, 8984)
	out, err := exec.Command(nativeCurl(), "-s", "--http2-prior-knowledge",
		fmt.Sprintf("http://127.0.0.1:%d/abc", port), "-w", "|%{http_code}|%{http_version}|%header{x-served-by}").CombinedOutput()
	if err != nil {
		t.Fatalf("curl h2c: %v\n%s", err, out)
	}
	if got := string(out); got != "p=/abc m=GET|201|2|kml" {
		t.Errorf("streams response: got %q, want %q", got, "p=/abc m=GET|201|2|kml")
	}
}

func TestE2EHTTP2StreamsRequestBody(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	// stream.on('data'/'end'): the request body delivered as one chunk.
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found in PATH")
	}
	src := `
import http2 from 'http2'
const server = http2.createServer()
server.on('stream', (stream, headers) => {
  let seen = ""
  stream.on('data', (chunk: string) => { seen = seen + chunk })
  stream.on('end', () => {
    stream.respond({ ':status': 200 })
    stream.end("body:" + seen)
  })
})
server.listen(8985)
`
	port := startHTTPServer(t, src, 8985)
	out, err := exec.Command(nativeCurl(), "-s", "--http2-prior-knowledge",
		"-d", "payload7", fmt.Sprintf("http://127.0.0.1:%d/up", port), "-w", "|%{http_version}").CombinedOutput()
	if err != nil {
		t.Fatalf("curl h2c POST: %v\n%s", err, out)
	}
	if got := string(out); got != "body:payload7|2" {
		t.Errorf("h2 POST: got %q, want %q", got, "body:payload7|2")
	}
}

func TestE2EHTTPServerNoHandlerListen(t *testing.T) {
	// A handler-less server (createServer() with no 'request'/'stream'
	// listener) is legitimate Node — client-behavior tests listen without
	// responding. A synthesized empty handler answers 200/empty.
	assertOutputImports(t, `
import http from 'http'
const server = http.createServer()
server.listen(0, () => {
  console.log("up", server.address().port > 0)
  setTimeout(() => { server.close() }, 10)
})
console.log("done")
`, "up true\ndone")
}

func TestE2EHTTP2ClientSession(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	// TDD-00139 Stage 3: http2.connect + session.request against the same
	// process's http2 server — the dominant corpus shape. Response headers
	// (:status + custom), body via 'data'/'end', clean close of both ends,
	// with every callback count verified at exit by mustCall.
	src := `
import http2 from 'http2'
import { mustCall } from 'test'
const server = http2.createServer()
server.on('stream', mustCall((stream, headers) => {
  stream.respond({ ':status': 200, 'x-mode': 'h2' })
  stream.end("srv:" + headers[':path'])
}))
server.listen(0, mustCall(() => {
  const client = http2.connect("http://127.0.0.1:" + server.address().port)
  const req = client.request({ ':path': '/from-client' })
  let data = ""
  req.on('response', mustCall((headers) => {
    console.log("status", headers[':status'], "xmode", headers['x-mode'])
  }))
  req.on('data', (chunk: string) => { data = data + chunk })
  req.on('end', mustCall(() => {
    console.log("got", data)
    client.close()
    server.close()
  }))
}))
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "status 200 xmode h2") {
		t.Errorf("response headers missing: %q", out)
	}
	if !strings.Contains(out, "got srv:/from-client") {
		t.Errorf("response body missing: %q", out)
	}
}

func TestE2EHTTP2ClientRequestHeaders(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	// Extra literal request headers reach the server's headers map.
	src := `
import http2 from 'http2'
const server = http2.createServer()
server.on('stream', (stream, headers) => {
  stream.respond({ ':status': 200 })
  stream.end("tok=" + headers['x-token'])
})
server.listen(0, () => {
  const client = http2.connect("http://127.0.0.1:" + server.address().port)
  const req = client.request({ ':path': '/t', 'x-token': 'abc123' })
  req.on('data', (c: string) => { console.log("body", c) })
  req.on('end', () => { client.close(); server.close() })
})
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "body tok=abc123") {
		t.Errorf("request header did not reach the server: %q", out)
	}
}

func TestE2EHTTP2ConstantsAndSettings(t *testing.T) {
	// TDD-00139 Stage 4: constants (direct and via a bound alias), default
	// settings, wire packing (6-byte big-endian entries in identifier order),
	// and a pack→unpack round trip.
	assertOutputImports(t, `
import http2 from 'http2'
console.log("hdr", http2.constants.HTTP2_HEADER_PATH)
const constants = http2.constants
console.log("code", constants.NGHTTP2_CANCEL)
const d = http2.getDefaultSettings()
console.log("defaults", d.headerTableSize, d.enablePush, d.maxFrameSize)
const packed = http2.getPackedSettings({ headerTableSize: 100, enablePush: true })
console.log("packed", packed.length, packed[1], packed[5], packed[7], packed[11])
const round = http2.getUnpackedSettings(http2.getPackedSettings(http2.getDefaultSettings()))
console.log("round", round.headerTableSize, round.maxConcurrentStreams, round.enableConnectProtocol)
`, "hdr :path\ncode 8\ndefaults 4096 true 16384\npacked 12 1 100 2 1\nround 4096 4294967295 false")
}

func TestE2EHTTPChainedListenBinding(t *testing.T) {
	// The chained-binding idiom Node tests use constantly:
	// `const server = http.createServer(cb).listen(0, readyCb)` — listen()
	// returns the server, and the handle is stored into the binding *before*
	// the ready callback runs, so `server.address().port` works inside it
	// (the var-decl split, ADR-00423).
	src := `
import http from 'http'
import { mustCall } from 'test'
const server = http.createServer(mustCall((req, res) => {
  res.end("hi:" + req.path)
})).listen(0, mustCall(() => {
  http.get({ port: server.address().port, path: "/c" }, mustCall((res) => {
    let data = ""
    res.on('data', (chunk: string) => { data = data + chunk })
    res.on('end', () => { console.log("chained", data) })
    server.close()
  }))
}))
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "chained hi:/c") {
		t.Errorf("chained-binding response never delivered: %q", out)
	}
}

func TestE2EHTTPCreateServerMustNotCall(t *testing.T) {
	// `createServer(mustNotCall())` — a server that must never see a request
	// (ADR-00424): a zero-param listener wraps into a synthesized (req, res)
	// handler that invokes it, so a hit registers the exit-verified failure.
	src := `
import http from 'http'
import { mustCall, mustNotCall } from 'test'
const server = http.createServer(mustNotCall())
server.listen(0, mustCall(() => {
  console.log("bound: " + (server.address().port > 0))
  server.close()
}))
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "bound: true") {
		t.Errorf("server never bound: %q", out)
	}
}

func TestE2EHTTPServerHandleInTopLevelFunction(t *testing.T) {
	// A top-level helper function referencing the sibling server-handle
	// binding — the corpus's `function nextRequest() { … server.address()
	// … }` idiom. Works because an http.createServer binding (plain or
	// chained) promotes to a module global (ADR-00426).
	src := `
import http from 'http'
import { mustCall } from 'test'
const server = http.createServer(mustCall((req, res) => {
  res.end("r" + req.path)
}, 2))
function nextRequest(i: number) {
  http.get({ port: server.address().port, path: "/q" + i }, mustCall((res) => {
    res.resume()
    if (i === 1) { nextRequest(2) } else { server.close() }
  }))
}
server.listen(0, mustCall(() => { nextRequest(1) }))
console.log("done")
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "done") {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestE2EHTTPClientVariableOptionsObject(t *testing.T) {
	// A variable-bound options object (`const options = {...};
	// http.get(options, cb)`) — previously the object pointer silently fell
	// through as the URL and the program hung (ADR-00429). The 'agent' key
	// is accepted (one connection per request ≙ a non-keepAlive Agent).
	src := `
import http from 'http'
import { mustCall } from 'test'
const server = http.createServer(mustCall((req, res) => { res.end("v:" + req.path) }))
server.listen(0, mustCall(() => {
  const options = { agent: null, port: server.address().port, path: "/varopt" }
  http.get(options, mustCall((res) => {
    let d = ""
    res.on('data', (c: string) => { d = d + c })
    res.on('end', () => { console.log("got " + d) })
    server.close()
  }))
}))
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "got v:/varopt") {
		t.Errorf("variable-options request failed: %q", out)
	}
}

func TestE2EHTTPClientRequestHandle(t *testing.T) {
	// The ClientRequest handle (ADR-00430): http.request(...) sends nothing
	// until .end() (the chained corpus idiom), and an aborted pre-end request
	// never fires — the server handler is mustNotCall-verified.
	src := `
import http from 'http'
import { mustCall } from 'test'
const server = http.createServer(mustCall((req, res) => {
  res.end('ok')
  server.close()
})).listen(0, mustCall(() => {
  http.request({ port: server.address().port }, mustCall((res) => {
    let data = ''
    res.on('data', (chunk: string) => { data = data + chunk })
    res.on('end', mustCall(() => { console.log("data: " + data) }))
  })).end()
}))
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "data: ok") {
		t.Errorf("request().end() roundtrip failed: %q", out)
	}
}

func TestE2EHTTPClientRequestBody(t *testing.T) {
	// A ClientRequest can carry a request body (ADR-00575): req.write(chunk)
	// stages bytes (appending across calls), req.end([body]) appends a final
	// chunk and fires. The server echoes method + body.
	src := `
import http from 'http'
import { mustCall } from 'test'
const server = http.createServer(mustCall((req, res) => {
  res.end(req.method + ":" + req.body)
  server.close()
})).listen(0, mustCall(() => {
  const req = http.request({ port: server.address().port, method: "POST" }, mustCall((res) => {
    let data = ''
    res.on('data', (chunk: string) => { data = data + chunk })
    res.on('end', mustCall(() => { console.log("got: " + data) }))
  }))
  req.write("hello ")
  req.end("world")
}))
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "got: POST:hello world") {
		t.Errorf("request body roundtrip failed: %q", out)
	}
}

func TestE2EHTTPClientRequestAbort(t *testing.T) {
	src := `
import http from 'http'
import { mustCall, mustNotCall } from 'test'
const server = http.createServer(mustNotCall())
server.listen(0, mustCall(() => {
  const req = http.request({ port: server.address().port })
  req.on('error', mustNotCall())
  req.abort()
  server.close()
}))
console.log("aborted cleanly")
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "aborted cleanly") {
		t.Errorf("abort path failed: %q", out)
	}
}

func TestE2EHTTPAgentInert(t *testing.T) {
	// `new http.Agent(options)` (ADR-00432): an inert pool-config token —
	// constructible (qualified or named import), passable as the client's
	// `agent` option, `.destroy()`-able; pooling itself never happens (one
	// connection per request, Node's non-keepAlive behavior).
	src := `
import http from 'http'
import { mustCall } from 'test'
const agent = new http.Agent({ keepAlive: true, maxSockets: 1 })
const server = http.createServer(mustCall((req, res) => { res.end('a') }))
server.listen(0, mustCall(() => {
  http.get({ port: server.address().port, agent: agent }, mustCall((res) => {
    res.resume()
    agent.destroy()
    server.close()
  }))
}))
console.log("agent ok")
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "agent ok") {
		t.Errorf("agent flow failed: %q", out)
	}
}

func TestE2EHTTPReasonPhrase(t *testing.T) {
	// ADR-00486: the status line carries the standard reason phrase
	// (previously a fixed "OK" for every status).
	src := `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(404)
  res.end("nope")
}).listen(8956)
`
	port := startHTTPServer(t, src, 8956)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.Status != "404 Not Found" {
		t.Errorf("status line: got %q, want \"404 Not Found\"", resp.Status)
	}
}

// http.request with method + headers options (ADR-00500): the transfer
// rides fetch_async's existing method/header slots; GET stays the default.
func TestE2EHTTPClientMethodAndHeaders(t *testing.T) {
	src := `
import http from 'http'
import { mustCall } from 'test'
const server = http.createServer(mustCall((req, res) => {
  res.end(req.method + "|" + req.headers["x-probe"])
}))
server.listen(0, mustCall(() => {
  const req = http.request({
    port: server.address().port,
    path: "/m",
    method: "DELETE",
    headers: { "X-Probe": "kml42" },
  }, mustCall((res) => {
    let d = ""
    res.on('data', (c: string) => { d = d + c })
    res.on('end', () => { console.log("got " + d) })
    server.close()
  }))
  req.end()
}))
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "got DELETE|kml42") {
		t.Errorf("method/headers request failed: %q", out)
	}
}

// server.on('listening') (ADR-00502): registered before listen(), fired
// right after the bind — with the port already available via address().
func TestE2EHTTPServerListeningEvent(t *testing.T) {
	src := `
import http from 'http'
import { mustCall } from 'test'
const server = http.createServer((req, res) => { res.end("ok") })
server.on('listening', mustCall(() => {
  console.log("listening on", server.address().port > 0)
  http.get({ port: server.address().port, path: "/" }, mustCall((res) => {
    res.on('data', (c: string) => {})
    res.on('end', () => { server.close(); process.exit(0) })
  }))
}))
server.listen(0)
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "listening on true") {
		t.Errorf("listening event failed: %q", out)
	}
}

// createServer({requireHostHeader: false}, cb) (ADR-00503): accepted because
// this dispatcher never enforces a Host header anyway; any other option (or
// the literal true) stays a clean rejection.
func TestE2EHTTPCreateServerRequireHostHeaderFalse(t *testing.T) {
	src := `
import http from 'http'
const server = http.createServer({ requireHostHeader: false }, (req, res) => { res.end("ok") })
server.listen(0, () => {
  http.get({ port: server.address().port }, (res) => {
    console.log("status", res.statusCode)
    res.on('data', (c: string) => {})
    res.on('end', () => { server.close(); process.exit(0) })
  })
})
`
	out := compileAndRunImports(t, src)
	if !strings.Contains(out, "status 200") {
		t.Errorf("requireHostHeader form failed: %q", out)
	}
}

// skipIfLoopbackH2PrefaceIntercepted probes whether a plain TCP connection
// over loopback can carry the HTTP/2 client preface end to end. On some
// Windows hosts a security product intercepts loopback connections that
// begin with "PRI * HTTP/2.0" and answers with its own GOAWAY; Node's own
// http2.connect fails there identically (ERR_HTTP2_ERROR), so an in-process
// h2 client/server pair cannot be exercised on such a box. The probe is pure
// Go, so it reports the host's behaviour, not the compiler's.
func skipIfLoopbackTrafficFiltered(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return
	}
	defer ln.Close()
	preface := []byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n")
	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- nil
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, len(preface))
		n, _ := io.ReadFull(c, buf)
		got <- buf[:n]
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		return
	}
	_, _ = c.Write(preface)
	seen := <-got
	c.Close()
	if string(seen) != string(preface) {
		t.Skip("loopback traffic is filtered on this host (a traffic-filtering product such as AdGuard answers HTTP/2 prefaces and breaks TLS handshakes; Node's http2.connect fails here too) — exclude localhost from the filter to run this")
	}
}

// TDD-00195 Stage 2 (flush-model): a fire-and-forget `(req,res)=>void` handler
// that ends the response from an async req.on('end') callback works — the
// connection fiber parks after the handler returns until res.end() sets the
// "ended" flag, rather than flushing an empty response on return.
func TestE2EResFireAndForgetOnData(t *testing.T) {
	src := `
import http from 'http'
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  let total = 0
  req.on('data', (c: Uint8Array) => { total = total + c.length })
  req.on('end', () => { res.end("ff=" + total) })
}).listen(8991)
`
	port := startHTTPServer(t, src, 8991)
	body := []byte("abcdefghij-1234567890")
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/", port), "text/plain", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	want := fmt.Sprintf("ff=%d", len(body))
	if string(got) != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

// http.get with no event loop is serviced by __kml_httpc_drive. Its pump loop
// allocated a stack slot on every pass and spun without waiting, so a transfer
// slow to fail — a refused connect retries for ~2 s on Windows — overflowed
// the stack (0xC00000FD) instead of reaching the uncaught-error exit. Now it
// waits on curl's sockets between pumps and exits 1 as on Linux.
func TestE2EHTTPClientRefusedConnectExitsCleanly(t *testing.T) {
	out, code := compileAndRunExpectExitImports(t, `
import http from 'http'
console.log("start")
http.get("http://127.0.0.1:1/", (res) => { console.log(res.statusCode) })
`)
	if code != 1 {
		t.Fatalf("exit code %d (want 1, the uncaught transport error); stdout %q", code, out)
	}
	if !strings.HasPrefix(out, "start") || !strings.Contains(out, "Could not connect") {
		t.Fatalf("stdout %q: want the program's own line, then the uncaught transport error", out)
	}
}
