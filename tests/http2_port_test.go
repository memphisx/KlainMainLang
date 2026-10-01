package tests

import (
	"strings"
	"testing"
)

// http2 is Node's lib/internal/http2 in TypeScript over an nghttp2 session
// per Http2Session (http2src/h2node.c). Each program also runs under Node
// and must print the same; none prints the relative order of events on the
// two ends of one connection, which the event loop decides.

func TestE2EHttp2PortSync(t *testing.T) {
	assertSameAsNodeImports(t, "import http2 from 'http2';\nconst t = (n: string, f: () => any) => { try { console.log(n, 'ok', f()); } catch (e: any) { console.log(n, e.name, e.code, e.message); } };\nt('defaults', () => http2.getDefaultSettings());\nt('packed-empty', () => http2.getPackedSettings({}).toString('hex'));\nt('packed-all', () => http2.getPackedSettings({ headerTableSize: 1, enablePush: false, initialWindowSize: 2, maxFrameSize: 16385, maxConcurrentStreams: 3, maxHeaderListSize: 4, enableConnectProtocol: true }).toString('hex'));\nt('packed-custom', () => http2.getPackedSettings({ customSettings: { 100: 5 } } as any).toString('hex'));\nt('bad-frame', () => http2.getPackedSettings({ maxFrameSize: 1 }));\nt('bad-push', () => http2.getPackedSettings({ enablePush: 1 as any }));\nt('bad-len', () => http2.getUnpackedSettings(Buffer.alloc(5)));\nt('unpack', () => http2.getUnpackedSettings(http2.getPackedSettings({ maxHeaderListSize: 7, enablePush: true })));\nt('consts', () => [http2.constants.HTTP2_HEADER_PATH, http2.constants.NGHTTP2_CANCEL, http2.constants.HTTP_STATUS_TEAPOT, Object.keys(http2.constants).length]);\nt('sensitive', () => typeof http2.sensitiveHeaders);\nt('connect-proto', () => http2.connect('ftp://x'));\n")
}

func TestE2EHttp2PortCompatAndClient(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	assertSameAsNodeImports(t, "import http2 from 'http2';\nimport type { AddressInfo } from 'net';\nconst log = (...a: any[]) => console.log(...a);\nconst server = http2.createServer((req, res) => {\n  let body = '';\n  req.setEncoding('utf8');\n  req.on('data', (c: string) => { body += c; });\n  req.on('end', () => {\n    log('server req', req.method, req.url, req.httpVersion, req.headers['x-test'], JSON.stringify(body));\n    res.setHeader('x-reply', 'yes');\n    res.writeHead(201, { 'content-type': 'text/plain' });\n    res.addTrailers({ 'x-trailer': 'done' });\n    res.end('got ' + body.length);\n  });\n});\nserver.listen(0, () => {\n  const port = (server.address() as AddressInfo).port;\n  const client = http2.connect('http://127.0.0.1:' + port);\n  client.on('connect', () => log('connect', client.alpnProtocol, client.encrypted));\n  const req = client.request({ ':method': 'POST', ':path': '/upload?q=1', 'x-test': 'abc' });\n  req.on('response', (h) => log('response', h[':status'], h['content-type'], h['x-reply']));\n  req.on('trailers', (t) => log('trailers', t['x-trailer']));\n  let data = '';\n  req.setEncoding('utf8');\n  req.on('data', (c: string) => { data += c; });\n  req.on('end', () => {\n    log('client body', data);\n    client.ping(Buffer.from('12345678'), (err, dur, payload) => {\n      log('ping', err, typeof dur, payload.toString());\n      try { client.request({ connection: 'keep-alive' }); } catch (e: any) { log(e.name, e.code, e.message); }\n      try { client.request({ ':nope': 'x' }); } catch (e: any) { log(e.name, e.code, e.message); }\n      client.close(() => { log('client closed'); server.close(() => log('server closed')); });\n    });\n  });\n  req.end('hello world');\n});\n")
}

func TestE2EHttp2PortStreamsAPI(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	assertSameAsNodeImports(t, "import http2 from 'http2';\nimport type { AddressInfo } from 'net';\nconst server = http2.createServer();\nserver.on('stream', (stream, headers) => {\n  let b = '';\n  stream.on('data', (c: Buffer) => { b += c.toString(); });\n  stream.on('end', () => { stream.respond({ ':status': 200 }); stream.end('len ' + b.length); });\n});\nserver.listen(0, () => {\n  const client = http2.connect('http://127.0.0.1:' + (server.address() as AddressInfo).port);\n  const req = client.request({ ':method': 'POST', ':path': '/' });\n  req.on('response', (h) => console.log('status', h[':status']));\n  req.on('data', (d: Buffer) => console.log('data', d.toString()));\n  req.on('end', () => { client.close(); server.close(); });\n  try { req.write('abc'); } catch (e) { console.log('write threw', typeof e, e); }\n  req.end();\n});\n")
}

func TestE2EHttp2PortPushAndReset(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	assertSameAsNodeImports(t, "import http2 from 'http2';\nimport type { AddressInfo } from 'net';\nconst server = http2.createServer();\nserver.on('stream', (stream, headers) => {\n  if (headers[':path'] === '/rst') { stream.close(http2.constants.NGHTTP2_CANCEL); return; }\n  console.log('pushAllowed', stream.pushAllowed);\n  stream.pushStream({ ':path': '/style.css' }, (err, push, h) => {\n    console.log('push cb', err, h[':path']);\n    push.respond({ ':status': 200, 'content-type': 'text/css' });\n    push.end('body{}');\n    stream.respond({ ':status': 200 });\n    stream.end('page');\n  });\n});\nserver.listen(0, () => {\n  const client = http2.connect('http://127.0.0.1:' + (server.address() as AddressInfo).port);\n  client.on('stream', (pushed, headers) => {\n    console.log('client got push', headers[':path']);\n    let b = '';\n    pushed.on('data', (c: Buffer) => { b += c.toString(); });\n    pushed.on('end', () => console.log('push body', b));\n  });\n  const req = client.request({ ':path': '/' });\n  let b = '';\n  req.on('data', (c: Buffer) => { b += c.toString(); });\n  req.on('end', () => {\n    console.log('page', b);\n    const r2 = client.request({ ':path': '/rst' });\n    r2.on('close', () => {\n      console.log('rst close', r2.rstCode, r2.aborted);\n      client.close(() => { console.log('client close'); server.close(); });\n    });\n    r2.on('error', (e) => console.log('r2 error', e.message));\n    r2.resume();\n  });\n});\n")
}

func TestE2EHttp2PortSettings(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	assertSameAsNodeImports(t, "import http2 from 'http2';\nimport type { AddressInfo } from 'net';\nconst server = http2.createServer({ settings: { maxConcurrentStreams: 7 } });\nserver.listen(0, () => {\n  const client = http2.connect('http://127.0.0.1:' + (server.address() as AddressInfo).port, { settings: { initialWindowSize: 70000 } });\n  client.on('localSettings', (s) => {\n    console.log('local', s);\n    client.settings({ headerTableSize: 1024 }, (err, st) => { console.log('settings cb', err, st.headerTableSize); client.close(); server.close(); });\n  });\n  client.on('remoteSettings', (s) => console.log('remote maxConcurrentStreams', s.maxConcurrentStreams));\n});\n")
}

// stream.setTimeout fires 'timeout' on an idle stream; the session's timer
// (unref'd) does not keep the process alive.
func TestE2EHttp2PortStreamTimeout(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	assertSameAsNodeImports(t, "import * as http2 from 'http2';\nimport type { AddressInfo } from 'net';\nconst server = http2.createServer();\nserver.on('stream', (stream) => {\n    stream.setTimeout(50, () => {\n        console.log('stream timeout');\n        stream.respond({ ':status': 200 });\n        stream.end('late');\n    });\n});\nserver.listen(0, () => {\n    const port = (server.address() as AddressInfo).port;\n    const client = http2.connect(`http://127.0.0.1:${port}`);\n    client.setTimeout(1000);\n    const req = client.request({ ':path': '/' });\n    let body = '';\n    req.setEncoding('utf8');\n    req.on('data', (c: string) => { body += c; });\n    req.on('end', () => { console.log('body', body); client.close(); server.close(); });\n    req.end();\n});\n")
}

// h2 over TLS (ALPN), and allowHTTP1 serving an HTTP/1.1 client from the
// same server.
func TestE2EHttp2PortSecure(t *testing.T) {
	skipIfLoopbackTrafficFiltered(t)
	certLit, keyLit := genSelfSignedPEM(t)
	src := strings.NewReplacer("\"KEY\"", "\""+keyLit+"\"", "\"CERT\"", "\""+certLit+"\"").Replace("import http2 from 'http2';\nimport https from 'https';\nimport type { AddressInfo } from 'net';\nconst key = \"KEY\", cert = \"CERT\";\nconst server = http2.createSecureServer({ key, cert, allowHTTP1: true }, (req, res) => {\n  res.end('v' + req.httpVersion + ' ' + req.url);\n});\nserver.listen(0, () => {\n  const port = (server.address() as AddressInfo).port;\n  const client = http2.connect('https://localhost:' + port, { ca: cert });\n  client.on('connect', () => console.log('alpn', client.alpnProtocol, client.encrypted));\n  const r = client.request({ ':path': '/h2' });\n  let b = '';\n  r.on('data', (c: Buffer) => { b += c.toString(); });\n  r.on('end', () => {\n    console.log('h2 body', b);\n    client.close();\n    https.get({ host: 'localhost', port, path: '/h1', ca: cert }, (res) => {\n      let d = '';\n      res.on('data', (c: Buffer) => { d += c.toString(); });\n      res.on('end', () => { console.log('h1 body', d, res.httpVersion); server.close(); });\n    });\n  });\n});\n")
	assertSameAsNodeImports(t, src)
}
