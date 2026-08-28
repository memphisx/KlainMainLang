// The http2 core streams API (TDD-00139 Stage 2): server.on('stream') hands
// each request to a (stream, headers) handler — pseudo-headers read straight
// off the headers map, stream.respond sets :status + response headers,
// stream.end sends the body. Try it with:
//   curl --http2-prior-knowledge http://127.0.0.1:8631/kalimera
import http2 from 'http2';

const server = http2.createServer();

server.on('stream', (stream, headers) => {
  stream.respond({ ':status': 200, 'content-type': 'text/plain', 'x-engine': 'klainmain' });
  stream.end("you asked for " + headers[':path'] + " via " + headers[':method']);
});

server.listen(8631, () => {
  console.log("h2 streams server on", server.address().port);
  setTimeout(() => { server.close(); }, 150);
});
