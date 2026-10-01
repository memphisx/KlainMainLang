package tests

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// --- Node `cluster` (lib/node/cluster.ts, Node's lib/internal/cluster) ---
//
// cluster.fork() forks this program with NODE_UNIQUE_ID set and the IPC
// channel open; a worker's server listen goes through the primary, which
// accepts and hands connections round (SCHED_RR) or sends the listening
// socket itself (SCHED_NONE). Each program also runs under Node and must
// print the same; only the primary prints, in an order its own events fix.

// A single process with no fork is the primary.
func TestE2EClusterSingleProcessIsPrimary(t *testing.T) {
	assertSameAsNodeImports(t, `
import cluster from 'cluster'
console.log("isPrimary:", cluster.isPrimary, cluster.isMaster)
console.log("isWorker:", cluster.isWorker)
console.log("worker:", cluster.worker)
console.log("workers:", cluster.workers)
console.log("policy:", cluster.schedulingPolicy === cluster.SCHED_RR, cluster.SCHED_NONE, cluster.SCHED_RR)
`)
}

// klain:http's http.listen inside Node cluster workers: each worker binds
// the shared port itself (SO_REUSEPORT), as http.listen({ workers }) does.
func TestE2EClusterHTTPServed(t *testing.T) {
	src := `
import cluster from 'cluster'
import http from 'klain:http'
interface Res { status: number; body: string }
if (cluster.isPrimary) {
  for (let i = 0; i < 3; i++) { cluster.fork() }
} else {
  http.listen(8793, (req: HttpRequest): Res => {
    return { status: 200, body: "served by worker " + (cluster.worker?.id ?? 0) }
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

// Worker messages both ways; 'online', 'message' and 'exit' on the Worker.
func TestE2EClusterWorkerIPCMessaging(t *testing.T) {
	assertSameAsNodeImports(t, `
import cluster from 'cluster'
if (cluster.isPrimary) {
  const worker = cluster.fork()
  worker.on('online', () => { console.log("online") })
  worker.on('message', (msg) => {
    console.log("primary got: " + msg)
    worker.send("shutdown")
  })
  worker.on('exit', (code, signal) => { console.log("worker exit: " + code + " " + signal) })
} else {
  process.send!("hi from worker " + cluster.worker!.id)
  process.on('message', (msg) => {
    if (msg === "shutdown") { process.exit(0) }
  })
}
`)
}

// Cluster-level events carry the Worker first; cluster.workers is keyed by
// id and loses a worker before its 'exit' listeners run.
func TestE2EClusterLevelEventsAndWorkers(t *testing.T) {
	assertSameAsNodeImports(t, `
import cluster from 'cluster'
if (cluster.isPrimary) {
  cluster.on('fork', (w) => { console.log("fork " + w.id) })
  cluster.on('online', (w) => { console.log("online " + w.id + " " + w.state) })
  cluster.on('setup', (s) => { console.log("setup " + s.silent + " " + s.args!.length) })
  const w1 = cluster.fork()
  console.log("workers: " + Object.keys(cluster.workers!).join(","))
  console.log("byid " + (cluster.workers![1] === w1) + " " + (cluster.workers![9] === undefined))
  cluster.on('message', (w, msg: any) => {
    console.log("msg " + w.id + " " + JSON.stringify(msg))
    w.send({ reply: msg.n + 1 })
  })
  cluster.on('exit', (w, code) => {
    console.log("exit " + w.id + " " + code + " left " + Object.keys(cluster.workers!).length + " dead " + w.isDead())
  })
} else {
  process.send!({ cmd: "add", n: 41 })
  process.on('message', (m: any) => {
    process.exit(m.reply === 42 ? 7 : 1)
  })
}
`)
}

// cluster.disconnect(cb): every worker's channel closes; each exits with
// exitedAfterDisconnect set.
func TestE2EClusterDisconnectAll(t *testing.T) {
	assertSameAsNodeImports(t, `
import cluster from 'cluster'
if (cluster.isPrimary) {
  let online = 0
  cluster.on('online', () => {
    if (++online < 2) return
    cluster.disconnect(() => { console.log("all disconnected") })
  })
  let exits = 0
  cluster.on('exit', (w, code) => {
    if (++exits === 2) console.log("exits " + code + " " + w.exitedAfterDisconnect)
  })
  cluster.fork()
  cluster.fork()
} else {
  process.on('message', () => {})
}
`)
}

// A worker's net server behind the primary, under both scheduling
// policies: round-robin (the primary accepts and hands each connection to
// a worker) and shared (each worker accepts on the socket the primary
// sent); 'listening' reports the worker's address; worker.disconnect()
// closes its server and exits it.
func TestE2EClusterNetServerPolicies(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	for _, policy := range []string{"rr", "none"} {
		t.Run(policy, func(t *testing.T) {
			t.Setenv("NODE_CLUSTER_SCHED_POLICY", policy)
			assertSameAsNodeImports(t, `
import cluster from 'cluster'
import * as net from 'net'
if (cluster.isPrimary) {
  let listening = 0
  const replies: string[] = []
  cluster.on('listening', (w, addr) => {
    console.log("listening " + addr.port + " " + addr.address + " " + addr.addressType)
    if (++listening < 2) return
    for (let i = 0; i < 4; i++) {
      const c = net.connect(8159, '127.0.0.1')
      c.setEncoding('utf8')
      let b = ''
      c.on('data', (d: string) => { b += d })
      c.on('end', () => {
        replies.push(b)
        if (replies.length < 4) return
        console.log("replies " + replies.every((r) => r === 'w1' || r === 'w2'))
        for (const id in cluster.workers) cluster.workers[id]!.send('bye')
      })
    }
  })
  let exits = 0
  cluster.on('exit', (w, code) => {
    if (++exits === 2) console.log("exited " + code + " " + w.exitedAfterDisconnect)
  })
  cluster.fork()
  cluster.fork()
} else {
  const server = net.createServer((s) => { s.end('w' + cluster.worker!.id) })
  server.listen(8159)
  process.on('message', (m) => { if (m === 'bye') cluster.worker!.disconnect() })
}
`)
		})
	}
}

// An HTTP server in each worker; setupPrimary's args and silent reach the
// worker; worker.kill() ends it with SIGTERM.
func TestE2EClusterListeningAndSetupPrimary(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	assertSameAsNodeImports(t, `
import cluster from 'cluster'
import http from 'http'
if (cluster.isPrimary) {
  cluster.setupPrimary({ args: ["--mode", "beta"], silent: true })
  console.log("settings " + cluster.settings.args!.join(" ") + " " + cluster.settings.silent)
  cluster.on('listening', (w, addr) => {
    http.get('http://127.0.0.1:8153/x', (res) => {
      let body = ''
      res.setEncoding('utf8')
      res.on('data', (c: string) => { body += c })
      res.on('end', () => {
        console.log("listening " + w.id + " " + addr.port + " " + addr.address + " body " + body)
        w.kill()
      })
    })
  })
  cluster.on('exit', (w, code, signal) => { console.log("exit " + code + " " + signal) })
  const w = cluster.fork()
  let out = ''
  w.process.stdout!.setEncoding('utf8')
  w.process.stdout!.on('data', (chunk: string) => { out += chunk })
  w.process.stdout!.on('end', () => { console.log("captured " + out.trim()) })
} else {
  console.log("argv " + process.argv[2] + " " + process.argv[3])
  const server = http.createServer((req, res) => { res.end("hi " + req.url) })
  server.listen(8153)
}
`)
}
