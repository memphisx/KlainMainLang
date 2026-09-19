package tests

import (
	"fmt"
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
import http from 'klain:http'
interface Res { status: number; body: string }
if (cluster.isPrimary) {
  for (let i = 0; i < 3; i++) { cluster.fork() }
} else {
  http.listen(8793, (req: HttpRequest): Res => {
    return { status: 200, body: "served by worker " + cluster.workerId }
  })
}
`
	port := startHTTPClusterServer(t, src, 8793)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
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

func TestE2EClusterLevelEventsAndWorkers(t *testing.T) {
	// Cluster-level events (TDD-00105 remainder): cluster.on('fork'/'online'/
	// 'message'/'exit') fire with the Worker as the first argument — 'fork'
	// synchronously inside cluster.fork(), 'online' from a queued microtask,
	// 'message'/'exit' relayed off each worker's IPC/reap path (armed for
	// workers forked before OR after the registration). cluster.workers lists
	// the forked Worker handles.
	assertOutputImports(t, `
import cluster from 'cluster'
import { mustCall } from 'test'
if (cluster.isPrimary) {
  cluster.on('fork', mustCall((w) => { console.log("fork " + w.id) }))
  cluster.on('online', mustCall((w) => { console.log("online " + w.id) }))
  cluster.fork()
  console.log("workers: " + cluster.workers.length)
  for (const w of cluster.workers) { console.log("listed " + w.id) }
  cluster.on('message', mustCall((w, msg: string) => {
    console.log("msg " + w.id + " " + msg)
    w.send("shutdown")
  }))
  cluster.on('exit', mustCall((w, code) => { console.log("exit " + w.id + " " + code) }))
} else {
  process.send("hello")
  process.on('message', (msg) => {
    if (msg === "shutdown") { process.exit(7) }
  })
}
`, "fork 1\nworkers: 1\nlisted 1\nonline 1\nmsg 1 hello\nexit 1 7")
}

func TestE2EClusterDisconnectAll(t *testing.T) {
	// cluster.disconnect() closes every worker's IPC channel; the workers
	// observe the closed channel and exit their message loops.
	assertOutputImports(t, `
import cluster from 'cluster'
import { mustCall } from 'test'
if (cluster.isPrimary) {
  cluster.fork()
  cluster.fork()
  cluster.on('exit', (w, code) => { console.log("exit " + code) })
  cluster.disconnect()
  console.log("disconnected")
} else {
  process.on('message', (msg) => {})
  process.exit(0)
}
`, "disconnected\nexit 0\nexit 0")
}

func TestE2EClusterFidelitySurface(t *testing.T) {
	// The full module-level fidelity set (TDD-00105 closeout): cluster.workers
	// drops exited workers BEFORE the 'exit' listener runs (Node deletes then
	// emits), cluster.workers[id] is the ID-keyed lookup, object messages
	// cross the channel as themselves (json serialization mode, both
	// directions), several listeners per event coexist (worker-level twice +
	// the cluster-level relay), and cluster.disconnect(cb) flushes in-flight
	// messages before closing and runs its completion callback.
	assertOutputImports(t, `
import cluster from 'cluster'
import { mustCall } from 'test'
if (cluster.isPrimary) {
  cluster.on('message', mustCall((w, msg) => {
    if (typeof msg === "object") {
      console.log("obj cmd " + msg.cmd + " n " + msg.n)
      w.send({ reply: (msg.n as number) + 1 })
    } else {
      console.log("str " + msg)
    }
  }, 2))
  cluster.on('exit', mustCall((w, code) => {
    console.log("exit " + w.id + " " + code + " left " + cluster.workers.length)
  }))
  const w = cluster.fork()
  w.on('exit', mustCall((code) => { console.log("w-exit-a " + code) }))
  w.on('exit', mustCall((code) => { console.log("w-exit-b " + code) }))
  console.log("byid " + (cluster.workers[1] ? cluster.workers[1].id : -1) + " " + (cluster.workers[9] ? 1 : 0))
} else {
  process.send("hello")
  process.send({ cmd: "add", n: 41 })
  process.on('message', (m) => {
    console.log("worker reply " + m.reply)
    process.exit(0)
  })
}
`, "byid 1 0\nstr hello\nobj cmd add n 41\nworker reply 42\nexit 1 0 left 0\nw-exit-a 0\nw-exit-b 0")
}

func TestE2EClusterListeningAndSetupPrimary(t *testing.T) {
	// cluster.on('listening') fires with (worker, { address, port,
	// addressType }) when a worker's server binds; setupPrimary honors args
	// (worker argv tail) and silent (worker stdio piped to the Worker
	// handle); cluster.settings reads the stored values back. `exec` stays a
	// compile-time rejection (the re-exec-self model has no other file).
	assertOutputImports(t, `
import cluster from 'cluster'
import http from 'http'
import { mustCall } from 'test'
if (cluster.isPrimary) {
  cluster.setupPrimary({ args: ["--mode", "beta"], silent: true })
  console.log("settings " + cluster.settings.args.join(" ") + " " + cluster.settings.silent)
  cluster.on('listening', mustCall((w, addr) => {
    console.log("listening " + w.id + " " + addr.port + " " + addr.address)
    for (const ww of cluster.workers) ww.kill()
  }))
  cluster.on('exit', (w, code) => {})
  const w = cluster.fork()
  w.process.stdout.on('data', (chunk: string) => {
    console.log("captured " + chunk.trim())
  })
} else {
  console.log("argv " + process.argv[2] + " " + process.argv[3])
  const server = http.createServer((req, res) => { res.end("hi") })
  server.listen(8153)
}
`, "settings --mode beta true\ncaptured argv --mode beta\nlistening 1 8153 0.0.0.0")
}
