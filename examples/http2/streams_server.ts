// The http2 core API: server.on('stream') hands each request to a
// (stream, headers) handler, with the pseudo-headers in the headers object;
// stream.respond sends :status and the response headers, stream.end the body.
// Try it with:
//   curl --http2-prior-knowledge http://127.0.0.1:8631/kalimera
import http2 from 'http2';
import type { AddressInfo } from 'net';

const server = http2.createServer();

server.on('stream', (stream, headers) => {
  stream.respond({ ':status': 200, 'content-type': 'text/plain', 'x-engine': 'klainmain' });
  stream.end("you asked for " + headers[':path'] + " via " + headers[':method']);
});

server.listen(8631, () => {
  console.log("h2 streams server on", (server.address() as AddressInfo).port);
  setTimeout(() => { server.close(); }, 150);
});
