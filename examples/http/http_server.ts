// A minimal HTTP server (TDD-00004 V1), built on the select()-based event
// loop (TDD-00006 Part 1) that lets the listening socket's readiness and the
// timer queue share one wait instead of two competing loops.
//
// http.listen never returns on its own (there's no .close() in V1), so this
// example schedules a setTimeout that exits the process after a short delay
// — the same trick that lets `make examples` verify this file runs to
// completion without needing a real HTTP client to connect to it. Point a
// real client (curl, a browser) at http://localhost:8080/ while it's
// running to see it actually serve a request.

interface Res {
  status: number
  body: string
}

let requestCount = 0

setTimeout(() => {
  console.log('shutting down after ' + requestCount + ' request(s)')
  process.exit(0)
}, 300)

console.log('listening on :8080')

http.listen(8080, (req: Request): Res => {
  requestCount = requestCount + 1
  if (req.path === '/hello') {
    return { status: 200, body: 'hello, ' + req.method + ' ' + req.path }
  }
  return { status: 404, body: 'not found: ' + req.path }
})
