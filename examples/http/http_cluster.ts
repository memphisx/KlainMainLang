// Multi-process clustering for http.listen() (TDD-00025): a third, optional
// { workers: N } argument forks N-1 additional processes right after
// bind()+listen() succeeds, all of them (the original process included)
// sharing the one listening socket and served round-robin by the kernel —
// letting a single http.listen() service use every core on the machine
// instead of just one. os.cpus().length is the natural value to pass, not
// an arbitrary constant: it closes the loop between this feature and the
// os module (ADR-00090).
//
// cluster.isPrimary/cluster.workerId (0 for the original process, 1..N-1
// for each fork) exist purely for cases like the startup banner below —
// logging it from every one of N processes would be noisy, so real
// programs typically want exactly one of them to do it.
//
// Like the plain single-process example (http_server.ts), http.listen never
// returns on its own, so each process (the original and every fork alike —
// there's no dedicated non-serving primary in this V1) independently
// schedules its own setTimeout that exits after a short delay, so this
// example runs to completion under `make examples` without needing a real
// client to connect. Point curl at it while it's running to see requests
// actually load-balanced across workers, e.g. run several times in a row:
//   curl "http://localhost:8081/"

import http from 'http'
import os from 'os'
import cluster from 'cluster'

interface Res {
  status: number
  body: string
}

setTimeout(() => {
  process.exit(0)
}, 300)

if (cluster.isPrimary) {
  console.log('primary starting ' + os.cpus().length + ' worker(s) on :8081')
}

http.listen(8081, (req: HttpRequest): Res => {
  return { status: 200, body: 'served by worker ' + cluster.workerId.toString() + ' (pid ' + process.pid.toString() + ')' }
}, { workers: os.cpus().length })
