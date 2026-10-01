package tests

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A host value (Map, Headers, Blob, ArrayBuffer, …) stored into `any` stays
// an object: typeof, console.log, instanceof, ===, Set membership and its
// string tag match Node (ADR-01204).
func TestE2EHostValuesThroughAny(t *testing.T) {
	assertSameAsNode(t, `
const m = new Map<string, number>(); m.set("k", 1)
const a: any = m
console.log(typeof a)
console.log(a)
const h: any = new Headers({ x: "1" })
console.log(typeof h, h)
const b: any = new Blob(["ab"])
console.log(b)
console.log(b instanceof Blob, a instanceof Blob, a instanceof Map, h instanceof Map, h instanceof Headers)
const back = a as Map<string, number>
console.log(back.get("k"))
const bb: Blob = b
console.log(bb.size)
const a2: any = m
console.log(a === a2, a === h)
const s = new Set<any>(); s.add(a); s.add(a2)
console.log(s.size)
const xs: any[] = [new Blob(["a"]), new Headers(), new TextEncoder(), m, new URLSearchParams("a=1"), new ArrayBuffer(2)]
for (const x of xs) console.log(typeof x)
console.log(String(a), `+"`${b}`"+`)
const n: any = null
const nb: Blob = n
console.log(nb === null)
`)
}

// console.log of the host classes that are not plain object layouts.
func TestE2EHostClassInspect(t *testing.T) {
	assertSameAsNode(t, `
console.log(new Blob(["ab"]))
console.log(new Blob(["a"], { type: "text/plain" }))
console.log(new Headers({ "content-type": "a", x: "1" }))
console.log(new Headers())
console.log(new Headers({ "content-type": "text/plain", "x-custom": "1", "set-cookie": "a=1" }))
console.log(new ArrayBuffer(2))
console.log(new ArrayBuffer(101))
console.log(new SharedArrayBuffer(2))
console.log(new TextEncoder())
console.log(new TextDecoder())
console.log({ b: new Blob(["ab"]), h: new Headers({ a: "1" }) })
console.log([new Blob([])])
`)
}

// Buffer.from and a TypedArray constructor dispatch on an `any` argument at
// run time, as Node does.
func TestE2EBufferAndTypedArrayFromAny(t *testing.T) {
	assertSameAsNode(t, `
const ab = new ArrayBuffer(3)
new Uint8Array(ab)[1] = 7
const vals: any[] = ["hi", ab, [1, 2, 300], new Uint8Array([9, 8])]
for (const v of vals) console.log(Buffer.from(v))
for (const v of vals.slice(1)) console.log(new Uint8Array(v))
const n: any = 3
console.log(new Uint8Array(n))
const view = new Uint8Array(ab as any)
view[0] = 5
console.log(new Uint8Array(ab))
`)
}

// ws:/wss: are WHATWG special schemes; a malformed URL throws Node's coded
// TypeError.
func TestE2EURLWebSocketSchemesAndError(t *testing.T) {
	assertSameAsNode(t, `
console.log(new URL("ws://127.0.0.1:8981/").href)
console.log(new URL("wss://h:1/x?y").href)
for (const u of ["ws://h:80/", "wss://h:443/x", "ws://h:81/"]) {
  const x = new URL(u)
  console.log(x.href, JSON.stringify(x.port), x.host, x.origin)
}
try { new URL("not a url") } catch (e: any) { console.log(e instanceof TypeError, e.code, e.message) }
`)
}

func TestE2ERandomBytesIsBuffer(t *testing.T) {
	assertSameAsNodeImports(t, `
import { randomBytes } from 'crypto'
const b = randomBytes(4)
console.log(Buffer.isBuffer(b), b.length, b.toString('hex').length, b.toString('base64').length)
`)
}

// The WebSocket constructor's errors and a refused connection, as Node's
// (undici's) WebSocket reports them.
func TestE2EWebSocketConstructorErrorsAndRefusal(t *testing.T) {
	assertSameAsNode(t, `
const ws = new WebSocket("ws://127.0.0.1:1/")
console.log(ws.readyState, ws.url, ws.binaryType, ws.protocol === "", ws.bufferedAmount)
ws.binaryType = "nope" as any
console.log(ws.binaryType)
ws.onopen = () => { console.log("onopen (should not fire)") }
ws.onerror = (ev) => { console.log("onerror " + ev.type) }
ws.onclose = (ev) => {
  console.log("onclose " + ev.code + " " + ev.wasClean + " readyState: " + ws.readyState)
}
for (const u of ["http://x/#frag", "ftp://x/", "not a url"]) {
  try { new WebSocket(u) } catch (e: any) { console.log(e.name + ": " + e.message) }
}
try { new WebSocket("ws://x/", ["a", "a"]) } catch (e: any) { console.log(e.name + ": " + e.message) }
try { ws.send("x") } catch (e: any) { console.log(e.name + ": " + e.message) }
try { ws.close(1001) } catch (e: any) { console.log(e.name + ": " + e.message) }
try { ws.close(1000, "x".repeat(124)) } catch (e: any) { console.log(e.name + ": " + e.message) }
`)
}

// A Blob sent between strings goes out in order: its bytes are read first
// (undici's send queue).
func TestE2EWebSocketSendBlobInOrder(t *testing.T) {
	serverSrc := `
import http from 'http'
import { WebSocketServer, WSConnection } from 'klain:ws'
const server = http.createServer((req: http.IncomingMessage, res: http.ServerResponse) => {
  res.writeHead(200); res.end("x")
})
const wss = new WebSocketServer({ server })
wss.on('connection', (socket: WSConnection) => {
  socket.onmessage = (ev) => {
    socket.send((ev.isBinary ? "bin:" : "txt:") + ev.data)
  }
})
server.listen(8984)
`
	port := startHTTPServer(t, serverSrc, 8984)
	clientSrc := `
const ws = new WebSocket("ws://127.0.0.1:8984/")
ws.onopen = () => {
  ws.send("one")
  ws.send(new Blob(["two"]))
  ws.send("three")
}
let n = 0
ws.onmessage = (ev) => {
  console.log(ev.data)
  if (++n === 3) ws.close()
}
const safety = setTimeout(() => { console.log("SAFETY TIMEOUT"); process.exit(1) }, 4000)
ws.onclose = (ev) => { console.log("closed " + ev.wasClean); clearTimeout(safety) }
`
	out := runClientWithTimeout(t, subPort(clientSrc, 8984, port), 8*time.Second)
	want := "txt:one\nbin:two\ntxt:three\nclosed true"
	if out != want {
		t.Errorf("client output:\ngot:\n%s\nwant:\n%s", out, want)
	}
}

// A signal-carrying fetch resolves at the headers; aborting mid-body
// rejects the pending read with the reason, as Node's fetch does
// (ADR-01205).
func TestE2EFetchAbortMidStream(t *testing.T) {
	srv := newEventSourceTestServer(t)
	defer srv.Close()
	// /hang never sends its headers.
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer hang.Close()
	src := strings.ReplaceAll(strings.ReplaceAll(`
async function main() {
  const c = new AbortController()
  const res = await fetch("URL/stream", { signal: c.signal })
  console.log("status", res.status)
  const reader = res.body!.getReader()
  const first = await reader.read()
  console.log("first", first.done, first.value!.length)
  setTimeout(() => c.abort(), 50)
  try {
    await reader.read()
    console.log("no error")
  } catch (e: any) {
    console.log("read rejected", e.name, e.message)
  }
  try {
    await fetch("HANG/", { signal: AbortSignal.timeout(20) })
    console.log("resolved?")
  } catch (e: any) { console.log("timeout rejected", e.name) }
  const c2 = new AbortController(); c2.abort()
  try { await fetch("URL/stream", { signal: c2.signal }) } catch (e: any) { console.log("pre-aborted", e.name) }
}
main()
`, "URL", srv.URL), "HANG", hang.URL)
	out := compileAndRun(t, src)
	want := "status 200\nfirst false 13\nread rejected AbortError This operation was aborted\ntimeout rejected TimeoutError\npre-aborted AbortError"
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

// A text frame that is not UTF-8 fails the connection: error, then close
// with 1006, and the client's close frame carries 1007 (RFC 6455 §8.1).
func TestE2EWebSocketInvalidUTF8FailsConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	closeCode := make(chan int, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		req, err := http.ReadRequest(r)
		if err != nil {
			return
		}
		h := sha1.Sum([]byte(req.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		c.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " +
			base64.StdEncoding.EncodeToString(h[:]) + "\r\n\r\n"))
		c.Write([]byte{0x81, 0x02, 0xff, 0xfe})
		// The client's close frame: masked, code in the first two bytes.
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(r, hdr); err != nil || hdr[0]&0x0f != 8 {
			closeCode <- -1
			return
		}
		body := make([]byte, 4+int(hdr[1]&0x7f))
		if _, err := io.ReadFull(r, body); err != nil || len(body) < 6 {
			closeCode <- -1
			return
		}
		closeCode <- int(body[4]^body[0])<<8 | int(body[5]^body[1])
	}()
	src := strings.ReplaceAll(`
const ws = new WebSocket("ws://ADDR/")
ws.onmessage = () => { console.log("message (should not fire)") }
ws.onerror = (ev) => { console.log("error") }
ws.onclose = (ev) => { console.log("close " + ev.code + " " + ev.wasClean) }
`, "ADDR", ln.Addr().String())
	out := runClientWithTimeout(t, src, 8*time.Second)
	if out != "error\nclose 1006 false" {
		t.Errorf("got:\n%s", out)
	}
	select {
	case code := <-closeCode:
		if code != 1007 {
			t.Errorf("close frame code %d, want 1007", code)
		}
	case <-time.After(3 * time.Second):
		t.Error("no close frame")
	}
}
