// The explicit http2 module (TDD-00139 Stage 1): http2.createServer shares
// the http server core, which serves h2c (prior-knowledge cleartext HTTP/2)
// alongside HTTP/1.1 on the same port. Try it with:
//   curl --http2-prior-knowledge http://127.0.0.1:8629/hello
import http2 from 'http2';

const server = http2.createServer((req, res) => {
  res.writeHead(200, { "Content-Type": "text/plain" });
  res.end("served over " + req.method + " " + req.path);
});

server.listen(8629, () => {
  console.log("http2 module server on", server.address().port);
  // Exit shortly after for the example runner; a real service would stay up.
  setTimeout(() => { server.close(); }, 150);
});
