// The http2 client (TDD-00139 Stage 3): http2.connect opens an h2c session,
// session.request sends a body-less request (END_STREAM at submit), and the
// response comes back through 'response'/'data'/'end' — here against the same
// process's own http2 server, the standard Node test shape.
import http2 from 'http2';

const server = http2.createServer();
server.on('stream', (stream, headers) => {
  stream.respond({ ':status': 200, 'content-type': 'text/plain' });
  stream.end("hello " + headers[':path'].slice(1));
});

server.listen(0, () => {
  const client = http2.connect("http://127.0.0.1:" + server.address().port);
  const req = client.request({ ':path': '/thessaloniki' });
  let body = "";
  req.on('response', (headers) => {
    console.log("status:", headers[':status']);
  });
  req.on('data', (chunk: string) => { body = body + chunk; });
  req.on('end', () => {
    console.log("body:", body);
    client.close();
    server.close();
  });
});
