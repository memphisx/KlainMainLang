// The http2 client: http2.connect opens a session, session.request sends a
// GET (which ends its stream at once), and the response arrives through
// 'response'/'data'/'end', here from the same process's own http2 server.
import http2 from 'http2';
import type { AddressInfo } from 'net';

const server = http2.createServer();
server.on('stream', (stream, headers) => {
  stream.respond({ ':status': 200, 'content-type': 'text/plain' });
  stream.end("hello " + headers[':path'].slice(1));
});

server.listen(0, () => {
  const client = http2.connect("http://127.0.0.1:" + (server.address() as AddressInfo).port);
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
