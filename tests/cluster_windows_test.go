package tests

import (
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// TestE2EWindowsClusterWorkersExitWithPrimary: a worker does not outlive its
// primary. Node's cluster worker exits when its channel to the primary
// disconnects; here re-spawned workers used to keep serving the inherited
// listener forever once the primary was killed, holding the port open.
func TestE2EWindowsClusterWorkersExitWithPrimary(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows re-spawned cluster workers")
	}
	src := `
import http from 'klain:http'
interface Res { status: number; body: string }
http.listen(8979, (req: HttpRequest): Res => {
  return { status: 200, body: process.pid.toString() }
}, { workers: 3 })
`
	np := freePort(t)
	binFile := buildBinaryImports(t, subPort(src, 8979, np))
	cmd := exec.Command(binFile)
	setProcGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	pids := map[int]bool{}
	t.Cleanup(func() {
		killProcGroup(cmd)
		_ = cmd.Wait()
		for pid := range pids {
			_ = exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
		}
		waitPortFree(fmt.Sprintf("127.0.0.1:%d", np))
	})
	waitListening(t, np)

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	for i := 0; i < 9; i++ {
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", np))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		pid, err := strconv.Atoi(string(body))
		if err != nil {
			t.Fatalf("request %d: body %q is not a pid", i, body)
		}
		pids[pid] = true
	}
	delete(pids, cmd.Process.Pid)
	if len(pids) != 2 {
		t.Fatalf("expected 2 worker pids besides the primary, got %v", pids)
	}

	// The primary alone dies — no tree kill.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill primary: %v", err)
	}
	_ = cmd.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for pid := range pids {
		for windowsPidAlive(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("worker %d outlived its primary", pid)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// windowsPidAlive reports whether pid names a running process (tasklist prints
// an "INFO: No tasks…" line, not the pid, when it does not).
func windowsPidAlive(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH", "/FO", "CSV").Output()
	if err != nil {
		return false
	}
	return len(out) > 0 && out[0] == '"'
}

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
