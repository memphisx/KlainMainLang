package tests

import "testing"

// A readable piped into an HTTP/2 compat response writes and ends it, by
// pipeline() and by pipe() (ADR-02074).
func TestE2EHttp2PipeIntoCompatResponse(t *testing.T) {
	assertSameAsNodeImports(t, `import http2 from 'http2';
import { pipeline } from 'stream';
import type { AddressInfo } from 'net';
let mode = 0;
const server = http2.createServer((req, res) => {
  if (mode === 0) pipeline(req, res, (err) => { console.log('server pipeline', err ? err.code : 'ok'); });
  else req.pipe(res);
});
function echo(done: () => void): void {
  const client = http2.connect('http://127.0.0.1:' + (server.address() as AddressInfo).port);
  const req = client.request({ ':method': 'POST' });
  let got = '';
  req.on('response', (h) => console.log('response', h[':status']));
  req.on('data', (d: Buffer) => { got += d.toString(); });
  req.on('end', () => { console.log('echo', JSON.stringify(got)); client.close(done); });
  req.write('hello ');
  setTimeout(() => { req.write('world'); req.end(); }, 20);
}
server.listen(0, '127.0.0.1', () => {
  echo(() => { mode = 1; echo(() => server.close()); });
});
`)
}

// A client resetting its request mid-upload: the server's request aborts
// and its pipeline fails with a premature close once every stream in it
// has closed, in Node's order of events (ADR-02074).
func TestE2EHttp2PipelineResetEvents(t *testing.T) {
	assertSameAsNodeImports(t, `import http2 from 'http2';
import { Readable, pipeline } from 'stream';
import type { AddressInfo } from 'net';
const ev: string[] = [];
const server = http2.createServer((req, res) => {
  const st: any = (req as any).stream;
  for (const e of ['aborted', 'end', 'close', 'error']) st.on(e, (x: any) => ev.push('stream.' + e + (x && x.code ? ':' + x.code : '')));
  for (const e of ['aborted', 'end', 'close', 'error']) req.on(e, () => ev.push('req.' + e));
  for (const e of ['finish', 'close', 'error']) res.on(e, () => ev.push('res.' + e));
  pipeline(req, res, (err) => { ev.push('server pipeline ' + (err ? err.code : 'ok')); console.log(ev.join(' | ')); });
});
server.listen(0, '127.0.0.1', () => {
  const client = http2.connect('http://127.0.0.1:' + (server.address() as AddressInfo).port);
  const req = client.request({ ':method': 'POST' });
  let reads = 0;
  const rs = new Readable({ read() { reads++; rs.push('hello'); } });
  pipeline(rs, req, (err) => {
    console.log('client', err ? err.code : 'ok', 'rstCode', req.rstCode, 'bounded', reads < 100000);
    setTimeout(() => { server.close(); client.close(); }, 100);
  });
  let cnt = 10;
  req.on('data', () => { cnt--; if (cnt === 0) rs.destroy(); });
});
`)
}
