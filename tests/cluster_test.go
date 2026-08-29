package tests

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// --- Node `cluster` (TDD-00105 / ADR-00331) ---
//
// cluster.fork() re-execs the program as a worker (KML_CLUSTER_WORKER_ID env);
// cluster.isPrimary/isWorker/workerId read the seeded id. Workers each bind the
// same port via SO_REUSEPORT. The Go test drives a clustered HTTP server; the
// process-group cleanup helper (startHTTPClusterServer) reaps the forked
// workers.

// A single process with no fork is the primary: isPrimary true, isWorker false.
func TestE2EClusterSingleProcessIsPrimary(t *testing.T) {
	assertOutputImports(t, `
import cluster from 'cluster'
console.log("isPrimary:", cluster.isPrimary)
console.log("isWorker:", cluster.isWorker)
console.log("workerId:", cluster.workerId)
`, "isPrimary: true\nisWorker: false\nworkerId: 0")
}

// A clustered HTTP server: the primary forks workers, each re-execs and binds
// the shared port; a request is served by one of the workers.
func TestE2EClusterHTTPServed(t *testing.T) {
	src := `
import cluster from 'cluster'
import http from 'http'
interface Res { status: number; body: string }
if (cluster.isPrimary) {
  for (let i = 0; i < 3; i++) { cluster.fork() }
} else {
  http.listen(8793, (req: HttpRequest): Res => {
    return { status: 200, body: "served by worker " + cluster.workerId }
  })
}
`
	startHTTPClusterServer(t, src, 8793)
	resp, err := http.Get("http://127.0.0.1:8793/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.HasPrefix(string(body), "served by worker ") {
		t.Errorf("body: got %q, want prefix %q", string(body), "served by worker ")
	}
}

func TestE2EClusterWorkerIPCMessaging(t *testing.T) {
	// cluster.fork() workers now carry the TDD-00141 IPC channel
	// (ADR-00427): primary sees 'online' (microtask-deferred), receives the
	// worker's message, replies, and observes the worker's exit — all
	// mustCall-verified at exit on both sides.
	assertOutputImports(t, `
import cluster from 'cluster'
import { mustCall } from 'test'
if (cluster.isPrimary) {
  const worker = cluster.fork()
  worker.on('online', mustCall(() => { console.log("online") }))
  worker.on('message', mustCall((msg) => {
    console.log("primary got: " + msg)
    worker.send("shutdown")
  }))
  worker.on('exit', mustCall((code) => { console.log("worker exit: " + code) }))
} else {
  process.send("hi from worker " + cluster.workerId)
  process.on('message', (msg) => {
    if (msg === "shutdown") { process.exit(0) }
  })
}
`, "online\nprimary got: hi from worker 1\nworker exit: 0")
}
