// A Node http.createServer async handler that awaits a *task-shaped* promise
// while serving a request — a proxy front (ADR-00985). The handler awaits
// proxyGet(), a may-suspend async fn that itself awaits fetch(); awaiting its
// result on the connection fiber is a task-promise await (distinct from
// awaiting fetch directly). Before the fix, that combination corrupted the
// event loop's main context and crashed the server on the first request.
//
// Self-contained on the single event loop, like examples/http/client_server.ts:
// the front proxies to a local fixture (tools/httpbin-lite/, started by
// `make examples` — see ADR-00096), and an in-process client drives it. Runs
// unchanged under Node.js (pointed at any upstream).
import http from 'http'

// A may-suspend async fn: it awaits fetch, so a caller that awaits *it* parks
// on a task-shaped promise rather than on the fetch transfer directly.
async function proxyGet(path: string): Promise<string> {
  const r = await fetch('http://127.0.0.1:8765' + path)
  return await r.text()
}

const server = http.createServer(async (req: http.IncomingMessage, res: http.ServerResponse) => {
  const upstream = await proxyGet('/get')
  res.writeHead(200, { "Content-Type": "text/plain" })
  res.end("proxied " + upstream.length + " bytes")
  console.log("front served a proxied response of", upstream.length, "bytes")
  // The server never returns on its own; exit shortly after serving the one
  // request (this example harness only — a real server omits this).
  setTimeout(() => { process.exit(0) }, 50)
})

server.listen(18536, () => {
  // Drive the front from the same process, on the same event loop.
  http.get("http://127.0.0.1:18536/", (res) => {
    res.on('data', (_chunk: string) => {})
    res.on('end', () => {})
  })
})
