package tests

import "testing"

// A paused HTTP/2 stream stops giving the sender window: the server's write
// completes only after the client reads again, and the server's timeout
// waits while the write is still making progress (ADR-02079).
func TestE2EHttp2PausedStreamHoldsSender(t *testing.T) {
	assertSameAsNode(t, `import http2 from 'http2';
const events: string[] = [];
let resumed = false;
const server = http2.createServer();
server.setTimeout(400);
server.on('timeout', () => events.push('server timeout'));
server.on('stream', (stream) => {
  stream.respond({ ':status': 200 });
  stream.write(Buffer.alloc(1024 * 1024, 0x41), () => events.push('write done after resume: ' + resumed));
  stream.end();
});
server.listen(0, () => {
  const port = (server.address() as any).port;
  const client = http2.connect('http://localhost:' + port);
  const req = client.request({ ':path': '/' });
  req.end();
  let got = 0;
  let paused = false;
  req.on('data', (chunk: Buffer) => {
    got += chunk.length;
    if (!paused) {
      paused = true;
      req.pause();
      setTimeout(() => { resumed = true; req.resume(); }, 250);
    }
  });
  req.on('end', () => {
    events.push('received ' + got);
    console.log(events.join('\n'));
    client.close();
    server.close();
  });
});
`)
}
