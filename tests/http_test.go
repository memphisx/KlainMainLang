package tests

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// startHTTPServer compiles src (expected to call http.listen and never
// return) and runs it as a background process, waiting for the given port
// to accept TCP connections before returning. The process is killed via
// t.Cleanup regardless of test outcome, since http.listen's own process
// never exits on its own.
func startHTTPServer(t *testing.T, src string, port int) {
	t.Helper()
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	deadline := time.Now().Add(5 * time.Second)
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

// startHTTPServerGC is startHTTPServer's -mm=gc counterpart, for exercising
// http.listen's concurrent-fiber machinery under the Boehm GC (see
// docs/adr/ADR-00071.md's GC_stackbottom fix) — skips (via buildBinaryGC)
// if libgc/bdw-gc isn't installed.
func startHTTPServerGC(t *testing.T, src string, port int) {
	t.Helper()
	binFile := buildBinaryGCImports(t, src)
	cmd := exec.Command(binFile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	deadline := time.Now().Add(5 * time.Second)
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
func startHTTPClusterServer(t *testing.T, src string, port int) {
	t.Helper()
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	pgid := cmd.Process.Pid
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	t.Cleanup(func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		_ = cmd.Wait()
		waitPortFree(addr)
	})

	deadline := time.Now().Add(5 * time.Second)
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

// startHTTPClusterServerGC is startHTTPClusterServer's -mm=gc counterpart —
// see ADR-00099 for what changed in gcshim.c to make Boehm GC safe across
// http.listen's clustering fork() (GC_set_handle_fork(1) before GC_INIT()).
// Skips (via buildBinaryGC) if libgc/bdw-gc isn't installed, same as
// startHTTPServerGC.
func startHTTPClusterServerGC(t *testing.T, src string, port int) {
	t.Helper()
	binFile := buildBinaryGCImports(t, src)
	cmd := exec.Command(binFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	pgid := cmd.Process.Pid
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	t.Cleanup(func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		_ = cmd.Wait()
		waitPortFree(addr)
	})

	deadline := time.Now().Add(5 * time.Second)
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
	startHTTPServer(t, src, 8955)
	resp, err := http.Get("http://127.0.0.1:8955/")
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
	startHTTPServer(t, src, 8973)
	resp, err := http.Get("http://127.0.0.1:8973/x")
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
	startHTTPServer(t, src, 8972)
	resp, err := http.Get("http://127.0.0.1:8972/y")
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

func TestE2EHTTPListenBasicGet(t *testing.T) {
	src := `
import http from 'http'
interface Res { status: number; body: string }
http.listen(8941, (req: HttpRequest): Res => {
  return { status: 200, body: "hello from KML" }
})
`
	startHTTPServer(t, src, 8941)
	resp, err := http.Get("http://127.0.0.1:8941/")
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8942, (req: HttpRequest): Res => {
  return { status: 200, body: req.method + " " + req.path }
})
`
	startHTTPServer(t, src, 8942)
	resp, err := http.Get("http://127.0.0.1:8942/some/path")
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
import http from 'http'
interface Res { status: number; body: string }
let count = 0
http.listen(8943, (req: HttpRequest): Res => {
  count = count + 1
  return { status: 200, body: "req " + count }
})
`
	startHTTPServer(t, src, 8943)
	for i := 1; i <= 3; i++ {
		resp, err := http.Get("http://127.0.0.1:8943/")
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8944, (req: HttpRequest): Res => {
  if (req.path === "/missing") {
    return { status: 404, body: "not found" }
  }
  return { status: 200, body: "ok" }
})
`
	startHTTPServer(t, src, 8944)
	resp, err := http.Get("http://127.0.0.1:8944/missing")
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
import http from 'http'
interface Res { status: number; body: string }
let n = 0
setInterval(() => {
  n = n + 1
}, 50)
http.listen(8945, (req: HttpRequest): Res => {
  return { status: 200, body: "n=" + n }
})
`
	startHTTPServer(t, src, 8945)
	time.Sleep(200 * time.Millisecond)
	resp, err := http.Get("http://127.0.0.1:8945/")
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
import http from 'http'
interface Res { status: number; body: string }
try {
  http.listen(8946, (req: HttpRequest): Res => {
    return { status: 200, body: "ok" }
  })
} catch (e) {
  console.log("caught: " + e.message)
}
`
	startHTTPServer(t, src, 8946)
	// A second instance on the same port must fail to bind and hit the catch.
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
	_, err := parseAndCompileImports(t, `import http from 'http'
http.listen(8947)`)
	if err == nil {
		t.Fatal("expected a compile error for http.listen with only 1 argument, got none")
	}
}

func TestE2EHTTPListenNonObjectReturnTypeRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `import http from 'http'
http.listen(8948, (req: HttpRequest): number => 200)`)
	if err == nil {
		t.Fatal("expected a compile error for a handler not returning an object type, got none")
	}
}

func TestE2EHTTPListenMissingBodyFieldRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `
import http from 'http'
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8950, (req: HttpRequest): Res => {
  return { status: 200, body: req.path }
})
`
	startHTTPServer(t, src, 8950)

	slowConn, err := net.Dial("tcp", "127.0.0.1:8950")
	if err != nil {
		t.Fatalf("slow connection dial: %v", err)
	}
	defer slowConn.Close()
	// Deliberately don't send anything on slowConn yet.

	time.Sleep(100 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.Get("http://127.0.0.1:8950/fast")
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8951, (req: HttpRequest): Res => {
  return { status: 200, body: "ok" }
})
`
	startHTTPServer(t, src, 8951)

	client := &http.Client{}
	const n = 30000
	for i := 1; i <= n; i++ {
		resp, err := client.Get("http://127.0.0.1:8951/")
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8951, async (req: HttpRequest): Promise<Res> => {
  const r: Response = await fetch("%s" + req.path)
  return { status: 200, body: r.text() }
})
`, upstream.URL)
	startHTTPServer(t, src, 8951)

	resp, err := http.Get("http://127.0.0.1:8951/hello")
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8952, async (req: HttpRequest): Promise<Res> => {
  const r: Response = await fetch("%s" + req.path)
  return { status: 200, body: r.text() }
})
`, upstream.URL)
	startHTTPServer(t, src, 8952)

	slowDone := make(chan struct{})
	go func() {
		defer close(slowDone)
		resp, err := http.Get("http://127.0.0.1:8952/slow")
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
	resp, err := http.Get("http://127.0.0.1:8952/fast")
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

// --- ADR-00072: request headers, query string, request body, response headers ---

func TestE2EHTTPListenRequestHeadersLowercasedLookup(t *testing.T) {
	src := `
import http from 'http'
interface Res { status: number; body: string }
http.listen(8953, (req: HttpRequest): Res => {
  return { status: 200, body: req.headers.get("x-test-header") + "|" + (req.headers.has("nonexistent") ? "1" : "0") }
})
`
	startHTTPServer(t, src, 8953)
	httpReq, err := http.NewRequest("GET", "http://127.0.0.1:8953/", nil)
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8954, (req: HttpRequest): Res => {
  return { status: 200, body: req.path + "|" + req.query.get("a") + "|" + req.query.get("b") + "|" + (req.query.has("flag") ? "1" : "0") + "|" + req.query.get("flag") }
})
`
	startHTTPServer(t, src, 8954)
	// "b"'s value is percent-encoded ("two words" / "&") and "flag" is a
	// bare flag with no "=" — req.path must NOT include any of this.
	resp, err := http.Get("http://127.0.0.1:8954/some/path?a=1&b=two%20words&flag")
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8955, (req: HttpRequest): Res => {
  return { status: 200, body: req.path + "|" + (req.query.has("anything") ? "1" : "0") }
})
`
	startHTTPServer(t, src, 8955)
	resp, err := http.Get("http://127.0.0.1:8955/plain")
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8956, (req: HttpRequest): Res => {
  return { status: 200, body: req.body }
})
`
	startHTTPServer(t, src, 8956)
	resp, err := http.Post("http://127.0.0.1:8956/", "application/json", strings.NewReader(`{"k":"v"}`))
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8957, (req: HttpRequest): Res => {
  return { status: 200, body: "[" + req.body + "]" }
})
`
	startHTTPServer(t, src, 8957)
	resp, err := http.Get("http://127.0.0.1:8957/")
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8958, (req: HttpRequest): Res => {
  return { status: 200, body: "len=" + req.body.length }
})
`
	startHTTPServer(t, src, 8958)
	const size = 200_000
	var b strings.Builder
	b.Grow(size)
	for i := 0; i < size; i++ {
		b.WriteByte(byte('A' + i%26))
	}
	largeBody := b.String()

	resp, err := http.Post("http://127.0.0.1:8958/", "text/plain", strings.NewReader(largeBody))
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8959, (req: HttpRequest): Res => {
  return { status: 200, body: req.body }
})
`
	startHTTPServer(t, src, 8959)
	const size = 100_000
	var b strings.Builder
	b.Grow(size)
	for i := 0; i < size; i++ {
		b.WriteByte(byte('0' + i%10))
	}
	largeBody := b.String()

	resp, err := http.Post("http://127.0.0.1:8959/", "text/plain", strings.NewReader(largeBody))
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
import http from 'http'
interface Res { status: number; body: string; headers: string }
http.listen(8962, (req: HttpRequest): Res => { return { status: 200, body: "x", headers: "not a map" } })
`)
	if err == nil {
		t.Fatal("expected a compile error for a 'headers' field that isn't Map<string, string>, got none")
	}
}

func TestE2EHTTPListenResponseHeaders(t *testing.T) {
	src := `
import http from 'http'
interface Res { status: number; body: string; headers: Map<string, string> }
http.listen(8960, (req: HttpRequest): Res => {
  let h: Map<string, string> = new Map<string, string>()
  h.set("X-Custom-Header", "custom-value")
  h.set("Content-Type", "application/json")
  return { status: 200, body: "ok", headers: h }
})
`
	startHTTPServer(t, src, 8960)
	resp, err := http.Get("http://127.0.0.1:8960/")
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8961, (req: HttpRequest): Res => {
  return { status: 200, body: "plain" }
})
`
	startHTTPServer(t, src, 8961)
	resp, err := http.Get("http://127.0.0.1:8961/")
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8971, (req: HttpRequest): Res => {
  return { status: 200, body: req.body }
})
`
	startHTTPServer(t, src, 8971)
	payload := []byte{0x41, 0x42, 0x00, 0x43, 0x44, 0x00, 0x00, 0x45}
	resp, err := http.Post("http://127.0.0.1:8971/", "application/octet-stream", bytes.NewReader(payload))
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
import http from 'http'
interface Res { status: number; body: string; bodyBytes: ArrayBuffer }
http.listen(8963, (req: HttpRequest): Res => {
  const buf: ArrayBuffer = req.bodyBytes()
  return { status: 200, body: "", bodyBytes: buf }
})
`
	startHTTPServer(t, src, 8963)
	payload := []byte{0x41, 0x42, 0x00, 0x43, 0x44, 0x00, 0x00, 0x45}
	resp, err := http.Post("http://127.0.0.1:8963/", "application/octet-stream", bytes.NewReader(payload))
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
import http from 'http'
interface Res { status: number; body: string; bodyBytes: ArrayBuffer }
http.listen(8964, (req: HttpRequest): Res => {
  const buf: ArrayBuffer = new ArrayBuffer(3)
  return { status: 200, body: "this string is much longer than 3 bytes and must be ignored", bodyBytes: buf }
})
`
	startHTTPServer(t, src, 8964)
	resp, err := http.Get("http://127.0.0.1:8964/")
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8965, (req: HttpRequest): Res => {
  const buf: ArrayBuffer = req.bodyBytes()
  return { status: 200, body: "len=" + buf.byteLength }
})
`
	startHTTPServer(t, src, 8965)
	payload := []byte{0x41, 0x00, 0x42}
	resp, err := http.Post("http://127.0.0.1:8965/", "application/octet-stream", bytes.NewReader(payload))
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
import http from 'http'
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8963, (req: HttpRequest): Res => {
  return { status: 200, body: process.pid.toString() }
}, { workers: 3 })
`
	startHTTPClusterServer(t, src, 8963)

	const n = 40
	results := make(chan string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get("http://127.0.0.1:8963/")
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

// TestE2EHTTPListenClusteringDefaultIsSingleProcess confirms the two-argument
// form (no workers option) forks nothing — cluster.isPrimary must read true
// and cluster.workerId 0, byte-identical to today's single-process behavior.
func TestE2EHTTPListenClusteringDefaultIsSingleProcess(t *testing.T) {
	src := `
import http from 'http'
import cluster from 'cluster'
interface Res { status: number; body: string }
http.listen(8964, (req: HttpRequest): Res => {
  return { status: 200, body: (cluster.isPrimary ? "primary" : "worker") + " " + cluster.workerId.toString() }
})
`
	startHTTPServer(t, src, 8964)
	resp, err := http.Get("http://127.0.0.1:8964/")
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
	src := `
import http from 'http'
interface Res { status: number; body: string }
console.log("BANNER")
http.listen(8965, (req: HttpRequest): Res => {
  return { status: 200, body: "ok" }
}, { workers: 4 })
`
	binFile := buildBinaryImports(t, src)
	cmd := exec.Command(binFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	pgid := cmd.Process.Pid
	addr := "127.0.0.1:8965"
	t.Cleanup(func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8974, (req: HttpRequest): Res => {
  if (req.path === '/shutdown') {
    http.close()
    return { status: 200, body: "shutting down" }
  }
  return { status: 200, body: process.pid.toString() }
}, { workers: 3 })
`
	startHTTPClusterServer(t, src, 8974)

	resp, err := http.Get("http://127.0.0.1:8974/shutdown")
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
	addr := "127.0.0.1:8974"
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
import http from 'http'
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
	startHTTPClusterServerGC(t, src, 8966)

	const n = 20
	// 100,000 iterations * 72 (two concatenated 36-byte segments).
	const wantTotal = "7200000"

	results := make(chan string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get("http://127.0.0.1:8966/")
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
import http from 'http'
interface Res { status: number; body: string }
http.listen(8976, (req: HttpRequest): Res => {
  return { status: 200, body: Object.keys(req).join(",") }
})
`
	startHTTPServer(t, src, 8976)
	resp, err := http.Get("http://127.0.0.1:8976/hi")
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
import http from 'http'
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
	cmd, out := startBackgroundServer(t, src, 8967)

	resp, err := http.Get("http://127.0.0.1:8967/shutdown")
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
import http from 'http'
interface Res { status: number; body: string }

http.listen(8968, (req: HttpRequest): Res => {
  http.close()
  http.close()
  return { status: 200, body: "ok" }
})
console.log("after listen returned")
`
	cmd, out := startBackgroundServer(t, src, 8968)

	resp, err := http.Get("http://127.0.0.1:8968/")
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
	src := `
import http from 'http'
interface Res { status: number; body: string }
http.listen(8975, (req: HttpRequest): Res => {
  if (req.path === '/closeall') {
    http.closeAllConnections()
    return { status: 200, body: "closed all" }
  }
  return { status: 200, body: "ok" }
})
`
	startHTTPServer(t, src, 8975)

	// A raw connection that never completes its request — the server fiber parks
	// mid-read, keeping this connection in the active registry.
	raw, err := net.DialTimeout("tcp", "127.0.0.1:8975", 2*time.Second)
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
		if resp, err := c.Get("http://127.0.0.1:8975/closeall"); err == nil {
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
import http from 'http'
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
	_, err := parseAndCompileImports(t, `import http from 'http'
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
func TestE2EHTTPListenHTTP2Cleartext(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found in PATH")
	}
	src := `
import http from 'http'
interface Res { status: number; body: string }
http.listen(8961, (req: HttpRequest): Res => {
  return { status: 200, body: "h2:" + req.method + ":" + req.path + ":" + req.body }
})
`
	startHTTPServer(t, src, 8961)

	// h2c GET
	out, err := exec.Command("curl", "-s", "--http2-prior-knowledge",
		"http://127.0.0.1:8961/hello", "-w", "|%{http_version}").CombinedOutput()
	if err != nil {
		t.Fatalf("curl h2c GET: %v\n%s", err, out)
	}
	if got := string(out); got != "h2:GET:/hello:|2" {
		t.Errorf("h2c GET: got %q, want %q", got, "h2:GET:/hello:|2")
	}

	// h2c POST with a body
	out, err = exec.Command("curl", "-s", "--http2-prior-knowledge",
		"-d", "payload", "http://127.0.0.1:8961/submit", "-w", "|%{http_version}").CombinedOutput()
	if err != nil {
		t.Fatalf("curl h2c POST: %v\n%s", err, out)
	}
	if got := string(out); got != "h2:POST:/submit:payload|2" {
		t.Errorf("h2c POST: got %q, want %q", got, "h2:POST:/submit:payload|2")
	}

	// HTTP/1.1 on the same server still works
	out, err = exec.Command("curl", "-s", "--http1.1",
		"http://127.0.0.1:8961/one", "-w", "|%{http_version}").CombinedOutput()
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
	startHTTPServer(t, src, 8983)
	out, err := exec.Command("curl", "-s", "--http2-prior-knowledge",
		"http://127.0.0.1:8983/y", "-w", "|%{http_version}").CombinedOutput()
	if err != nil {
		t.Fatalf("curl h2c: %v\n%s", err, out)
	}
	if got := string(out); got != "h2mod:/y|2" {
		t.Errorf("h2 response: got %q, want %q", got, "h2mod:/y|2")
	}
}

func TestE2EHTTP2SecureServerRejected(t *testing.T) {
	// createSecureServer must reject cleanly (no TLS server mode), never
	// silently serve cleartext under a secure-sounding name.
	_, err := parseAndCompileImports(t, `
import http2 from 'http2'
http2.createSecureServer((req, res) => { res.end("x") })
`)
	if err == nil || !strings.Contains(err.Error(), "createSecureServer is not implemented") {
		t.Fatalf("want clean createSecureServer rejection, got %v", err)
	}
}

func TestE2EHTTP2StreamsAPI(t *testing.T) {
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
	startHTTPServer(t, src, 8984)
	out, err := exec.Command("curl", "-s", "--http2-prior-knowledge",
		"http://127.0.0.1:8984/abc", "-w", "|%{http_code}|%{http_version}|%header{x-served-by}").CombinedOutput()
	if err != nil {
		t.Fatalf("curl h2c: %v\n%s", err, out)
	}
	if got := string(out); got != "p=/abc m=GET|201|2|kml" {
		t.Errorf("streams response: got %q, want %q", got, "p=/abc m=GET|201|2|kml")
	}
}

func TestE2EHTTP2StreamsRequestBody(t *testing.T) {
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
	startHTTPServer(t, src, 8985)
	out, err := exec.Command("curl", "-s", "--http2-prior-knowledge",
		"-d", "payload7", "http://127.0.0.1:8985/up", "-w", "|%{http_version}").CombinedOutput()
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
