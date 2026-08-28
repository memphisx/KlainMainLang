// Real Node.js `http.createServer` (TDD-00131) — this file runs unchanged under
// Node.js as well as KlainMainLang. The handler gets `(req, res)`: read request
// fields off `req`, build the response by calling methods on `res`.

import http from 'http'

// The server never returns on its own, so (only for this example harness)
// schedule a timer that exits shortly after startup — the same trick the other
// server examples use. A real server omits this.
setTimeout(() => {
  process.exit(0)
}, 300)

http.createServer((req: IncomingMessage, res: ServerResponse) => {
  res.setHeader("X-Powered-By", "KlainMainLang")
  if (req.url === "/health") {
    res.writeHead(200, { "Content-Type": "text/plain" })
    res.end("ok")
  } else {
    res.writeHead(200, { "Content-Type": "text/plain" })
    res.write("Hello, ")
    res.end("world")
  }
}).listen(8080, () => {
  console.log("server listening on http://localhost:8080")
})
