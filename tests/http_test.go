package tests

import (
	"fmt"
	"io"
	"net"
	"net/http"
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
