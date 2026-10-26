// A server request is a Node Readable (TDD-00195): consume the body with
// `for await (const chunk of req)` — or req.on('data'/'end'), or req.pipe(dest)
// — instead of buffering req.body. Self-contained: the server consumes a POSTed
// body chunk-at-a-time, and an in-process http.request drives it, then the
// process exits. Runs the same way under Node.js.
import http from 'http';

const server = http.createServer(async (req: http.IncomingMessage, res: http.ServerResponse) => {
  let total = 0;
  let chunks = 0;
  for await (const chunk of req) {
    total = total + chunk.length; // chunk is a Uint8Array
    chunks = chunks + 1;
  }
  res.end('received ' + total + ' bytes in ' + chunks + ' chunk(s)');
});
server.listen(0, () => {
  const port = (server.address() as any).port; // bound port: listen(0) picks a free one
  const creq = http.request({ port, path: '/', method: 'POST' }, (res) => {
    let body = '';
    res.on('data', (c: string) => { body = body + c; });
    res.on('end', () => {
      console.log(body);
      process.exit(0);
    });
  });
  creq.write('a streamed request body, consumed as a Node Readable');
  creq.end();
});
