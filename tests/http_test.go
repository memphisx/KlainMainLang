package tests

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
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
