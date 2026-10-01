// http2.createServer with a request handler: Node's compatibility API
// (Http2ServerRequest/Http2ServerResponse) over each HTTP/2 stream. It speaks
// h2c, cleartext HTTP/2 with prior knowledge. Try it with:
//   curl --http2-prior-knowledge http://127.0.0.1:8629/hello
import http2 from 'http2';
import type { AddressInfo } from 'net';

const server = http2.createServer((req, res) => {
  res.writeHead(200, { "Content-Type": "text/plain" });
  res.end("served over " + req.method + " " + req.url);
});

server.listen(8629, () => {
  console.log("http2 module server on", (server.address() as AddressInfo).port);
  // Exit shortly after for the example runner; a real service would stay up.
  setTimeout(() => { server.close(); }, 150);
});
