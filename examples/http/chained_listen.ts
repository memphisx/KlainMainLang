// The chained-binding server idiom: listen() returns the server, the handle
// is bound before the ready callback fires, and the ephemeral port (listen 0)
// is read back via server.address(). Self-contained: the program makes one
// request against itself and closes.
import http from 'http'
import { mustCall } from 'test'

const server = http.createServer(mustCall((req, res) => {
  res.end("hello from " + req.path)
})).listen(0, mustCall(() => {
  const port = server.address().port
  console.log("bound an ephemeral port:", port > 0)
  http.get({ port: port, path: "/thessaloniki" }, mustCall((res) => {
    let data = ""
    res.on('data', (chunk: string) => { data = data + chunk })
    res.on('end', () => { console.log("response:", data) })
    server.close()
  }))
}))
