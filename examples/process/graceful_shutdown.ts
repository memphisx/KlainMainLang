// process.on('SIGINT'/'SIGTERM', handler) — TDD-00019/ADR-00079. Lets a
// long-running server run cleanup code and call process.exit() itself
// instead of dying instantly, finally closing out http.listen's one
// remaining gap (graceful shutdown — see docs/status/HTTP-SERVER.md).
//
// Like examples/http/http_server.ts, http.listen never returns on its own,
// so this example needs a way to end deterministically under `make
// examples` without a real external signal. Rather than the setTimeout+
// process.exit() trick that example uses, this one sends itself a real
// SIGINT via process.kill(process.pid, 2) — proving the full round trip
// (registration -> OS signal delivery -> handler invocation -> graceful
// exit), not just that process.exit() works. Try it interactively too: run
// this and press Ctrl-C instead of waiting for the scheduled self-signal.

import http from 'klain:http'  // bespoke handler⇒response server (Node's faithful shape is http.createServer)

let requestCount = 0

process.on('SIGINT', () => {
  console.log('graceful shutdown after ' + requestCount + ' request(s)')
  process.exit(0)
})

// Simulates an external Ctrl-C / `kill -INT`/orchestrator SIGTERM arriving
// shortly after startup, so this example terminates deterministically.
setTimeout(() => {
  process.kill(process.pid, 2) // 2 = SIGINT
}, 300)

console.log('listening on :8090')

http.listen(8090, (req: HttpRequest): { status: number; body: string } => {
  requestCount = requestCount + 1
  return { status: 200, body: 'hello' }
})
