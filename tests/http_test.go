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
	binFile := buildBinary(t, src)
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
	binFile := buildBinaryGC(t, src)
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
	binFile := buildBinary(t, src)
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
	binFile := buildBinaryGC(t, src)
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

func TestE2EHTTPListenBasicGet(t *testing.T) {
	src := `
interface Res { status: number; body: string }
http.listen(8941, (req: Request): Res => {
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
interface Res { status: number; body: string }
http.listen(8942, (req: Request): Res => {
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
interface Res { status: number; body: string }
let count = 0
http.listen(8943, (req: Request): Res => {
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
interface Res { status: number; body: string }
http.listen(8944, (req: Request): Res => {
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
interface Res { status: number; body: string }
let n = 0
setInterval(() => {
  n = n + 1
}, 50)
http.listen(8945, (req: Request): Res => {
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
interface Res { status: number; body: string }
try {
  http.listen(8946, (req: Request): Res => {
    return { status: 200, body: "ok" }
  })
} catch (e) {
  console.log("caught: " + e.message)
}
`
	startHTTPServer(t, src, 8946)
	// A second instance on the same port must fail to bind and hit the catch.
	got := compileAndRun(t, src)
	if got == "" {
		t.Fatal("expected the second instance's catch block to print something")
	}
}

func TestE2EHTTPListenWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`http.listen(8947)`)
	if err == nil {
		t.Fatal("expected a compile error for http.listen with only 1 argument, got none")
	}
}

func TestE2EHTTPListenNonObjectReturnTypeRejected(t *testing.T) {
	_, err := parseAndCompile(`http.listen(8948, (req: Request): number => 200)`)
	if err == nil {
		t.Fatal("expected a compile error for a handler not returning an object type, got none")
	}
}

func TestE2EHTTPListenMissingBodyFieldRejected(t *testing.T) {
	_, err := parseAndCompile(`
interface Res { status: number }
http.listen(8949, (req: Request): Res => { return { status: 200 } })
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
interface Res { status: number; body: string }
http.listen(8950, (req: Request): Res => {
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
interface Res { status: number; body: string }
http.listen(8951, (req: Request): Res => {
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
interface Res { status: number; body: string }
http.listen(8951, async (req: Request): Promise<Res> => {
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
interface Res { status: number; body: string }
http.listen(8952, async (req: Request): Promise<Res> => {
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
interface Res { status: number; body: string }
http.listen(8953, (req: Request): Res => {
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
interface Res { status: number; body: string }
http.listen(8954, (req: Request): Res => {
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
interface Res { status: number; body: string }
http.listen(8955, (req: Request): Res => {
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
interface Res { status: number; body: string }
http.listen(8956, (req: Request): Res => {
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
interface Res { status: number; body: string }
http.listen(8957, (req: Request): Res => {
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
interface Res { status: number; body: string }
http.listen(8958, (req: Request): Res => {
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
interface Res { status: number; body: string }
http.listen(8959, (req: Request): Res => {
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
	_, err := parseAndCompile(`
interface Res { status: number; body: string; headers: string }
http.listen(8962, (req: Request): Res => { return { status: 200, body: "x", headers: "not a map" } })
`)
	if err == nil {
		t.Fatal("expected a compile error for a 'headers' field that isn't Map<string, string>, got none")
	}
}

func TestE2EHTTPListenResponseHeaders(t *testing.T) {
	src := `
interface Res { status: number; body: string; headers: Map<string, string> }
http.listen(8960, (req: Request): Res => {
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
interface Res { status: number; body: string }
http.listen(8961, (req: Request): Res => {
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
interface Res { status: number; body: string }
http.listen(8963, (req: Request): Res => {
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
interface Res { status: number; body: string }
http.listen(8964, (req: Request): Res => {
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
interface Res { status: number; body: string }
console.log("BANNER")
http.listen(8965, (req: Request): Res => {
  return { status: 200, body: "ok" }
}, { workers: 4 })
`
	binFile := buildBinary(t, src)
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
interface Res { status: number; body: string }
http.listen(8966, (req: Request): Res => {
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
interface Res { status: number; body: string }

http.listen(8967, (req: Request): Res => {
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
interface Res { status: number; body: string }

http.listen(8968, (req: Request): Res => {
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

// TestE2EHTTPListenSecondCallSiteRejected: http.close() makes a second,
// textually-later http.listen() call genuinely reachable at runtime for the
// first time (the first call no longer necessarily runs forever) — without
// emitHTTPListen's httpListenCallSeen guard, this would otherwise compile
// and fail obscurely at the LLVM backend (a duplicate @__kml_http_dispatch
// definition) instead of with a clear compile-time error.
func TestE2EHTTPListenSecondCallSiteRejected(t *testing.T) {
	_, err := parseAndCompile(`
interface Res { status: number; body: string }
http.listen(8969, (req: Request): Res => { http.close(); return { status: 200, body: "a" } })
http.listen(8970, (req: Request): Res => { return { status: 200, body: "b" } })
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
	_, err := parseAndCompile(`http.close(1)`)
	if err == nil {
		t.Fatal("expected a compile error for http.close with an argument, got none")
	}
}
