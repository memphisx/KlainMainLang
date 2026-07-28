// A minimal HTTP server (TDD-00004), built on the select()-based event
// loop (TDD-00006 Part 1) that lets the listening socket's readiness and the
// timer queue share one wait instead of two competing loops. req.headers/
// req.query/req.body and an optional response `headers` field (ADR-00072)
// round out request/response handling beyond just method/path/status/body.
//
// http.listen never returns on its own (there's no .close() in V1), so this
// example schedules a setTimeout that exits the process after a short delay
// — the same trick that lets `make examples` verify this file runs to
// completion without needing a real HTTP client to connect to it. Point a
// real client at it while it's running to see it actually serve a request,
// e.g.:
//   curl -H "X-Greeting: hi" "http://localhost:8080/hello?name=world"
//   curl -X POST -d '{"k":"v"}' "http://localhost:8080/echo"

interface Res {
  status: number
  body: string
  headers: Map<string, string>
}

let requestCount = 0

setTimeout(() => {
  console.log('shutting down after ' + requestCount + ' request(s)')
  process.exit(0)
}, 300)

console.log('listening on :8080')

http.listen(8080, (req: Request): Res => {
  requestCount = requestCount + 1
  let respHeaders: Map<string, string> = new Map<string, string>()
  respHeaders.set('Content-Type', 'text/plain')

  if (req.path === '/hello') {
    let name: string = req.query.has('name') ? req.query.get('name') : 'stranger'
    let greeting: string = req.headers.has('x-greeting') ? req.headers.get('x-greeting') : 'hello'
    return { status: 200, body: greeting + ', ' + name + ' (' + req.method + ' ' + req.path + ')', headers: respHeaders }
  }
  if (req.path === '/echo') {
    return { status: 200, body: 'you sent: ' + req.body, headers: respHeaders }
  }
  return { status: 404, body: 'not found: ' + req.path, headers: respHeaders }
})
