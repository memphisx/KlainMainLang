package tests

import "testing"

// http.request's headers as a flat [name, value, ...] array go out as given,
// with no Host header of the request's own (Node's _http_client.js).
func TestE2EHttpRequestArrayHeaders(t *testing.T) {
	assertSameAsNode(t, `
import * as http from 'http'
const server = http.createServer((req, res) => {
  console.log(req.rawHeaders.filter((_, i) => i % 2 === 0).join(','))
  console.log(req.headers['x-a'], req.headers.host === undefined)
  res.end('ok')
})
server.listen(0, () => {
  const port = (server.address() as { port: number }).port
  http.get({ port, headers: ['X-A', '1', 'X-B', '2', 'Connection', 'close'] }, (res) => {
    res.resume()
    res.on('end', () => {
      console.log('status', res.statusCode)
      server.close()
    })
  })
})
`)
}
