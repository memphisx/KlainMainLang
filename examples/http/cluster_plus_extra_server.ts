// A klain:http `http.listen(..., { workers: N })` cluster and an additional
// Node `http.createServer(...).listen(...)` in ONE program (ADR-00989). Both
// were previously mutually exclusive; now the cluster joins the shared
// non-blocking reactor as an additional listener, the fork happens after every
// listener is bound, and each worker serves both ports.
//
// The additional createServer is declared first (it holds the primary reactor
// slot); the klain:http cluster follows and forks the workers. Like the other
// server examples, each process schedules its own exit so `make examples`
// terminates without a live client — point curl at :8091 (spread across
// workers) and :8092 while it runs.
import http from 'http'          // Node's faithful createServer shape
import khttp from 'klain:http'   // bespoke handler⇒response server (+ { workers })
import os from 'os'

interface Res { status: number; body: string }

setTimeout(() => { process.exit(0) }, 300)

// Additional Node server — the primary reactor slot.
http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.writeHead(200, { 'Content-Type': 'text/plain' })
  res.end('metrics from the side server\n')
}).listen(8092)

// Clustered klain:http service across every core — an additional listener.
khttp.listen(8091, (req: HttpRequest): Res => {
  return { status: 200, body: 'served by pid ' + process.pid.toString() + '\n' }
}, { workers: os.cpus().length })
