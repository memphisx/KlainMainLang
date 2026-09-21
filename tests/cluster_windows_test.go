package tests

import (
	"fmt"
	"io"
	"net/http"
	"runtime"
	"testing"
)

// TestE2EWindowsClusterRoundRobin pins the Windows scheduling of a
// { workers: N } cluster (ADR-01027): the primary accepts every connection and
// deals them out in turn, itself included, so request-at-a-time traffic rotates
// over all N processes. The cross-platform tests only ask for "more than one
// PID" under a concurrent burst, which is how a cluster whose workers had died
// at startup — and, later, one whose workers were alive but starved by the
// kernel's most-recent-first accept completion — both went unnoticed: each
// connection here is sequential and on its own socket, the case shared accept
// never spreads on Windows.
func TestE2EWindowsClusterRoundRobin(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows cluster scheduling: POSIX forks workers that share the listener (SCHED_NONE), so sequential requests do not rotate")
	}
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8978, (req: HttpRequest): Res => {
  return { status: 200, body: process.pid.toString() }
}, { workers: 3 })
`
	port := startHTTPClusterServer(t, src, 8978)

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	const rounds = 4
	seen := map[string]int{}
	for i := 0; i < 3*rounds; i++ {
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		seen[string(body)]++
	}
	if len(seen) != 3 {
		t.Fatalf("expected all 3 cluster processes to serve sequential requests, got %v", seen)
	}
	for pid, n := range seen {
		if n != rounds {
			t.Errorf("pid %s served %d of %d sequential requests, want %d each (round-robin): %v", pid, n, 3*rounds, rounds, seen)
		}
	}
}
