package tests

import "testing"

// `new Response(body?, init?)` (ADR-01190): the body normalised through a
// Blob, init's status/statusText/headers, the implied Content-Type, the
// status range and null-body-status errors; a HeadersInit as a record or
// [name, value] pairs; an untyped `await res.json()` is `any`. Node is the
// oracle.
func TestE2EResponseConstructor(t *testing.T) {
	assertSameAsNode(t, `
const a = new Response("hi")
console.log(a.status, a.ok, JSON.stringify(a.statusText), a.headers.get("content-type"), await a.text())
const b = new Response(new Uint8Array([104, 105]), { status: 404, statusText: "Nope", headers: { "X-A": "1" } })
console.log(b.status, b.ok, b.statusText, b.headers.get("x-a"), b.headers.get("content-type"), await b.text())
const c = new Response()
console.log(c.status, JSON.stringify(await c.text()))
const d = new Response('{"x":1,"y":[2,3]}', { headers: new Headers({ "content-type": "application/json" }) })
const data = await d.json()
console.log(data.x, data.y[1], d.headers.get("content-type"))
try { new Response("x", { status: 99 }) } catch (e) { console.log((e as Error).name, (e as Error).message) }
try { new Response("x", { status: 204 }) } catch (e) { console.log((e as Error).name, (e as Error).message) }
const g = new Response(new Blob(["bb"], { type: "text/x" }))
console.log(g.headers.get("content-type"), await g.text())
const buf = await new Response("abc").arrayBuffer()
console.log(buf.byteLength)
`)
}

func TestE2EHeadersInitForms(t *testing.T) {
	assertSameAsNode(t, `
const h1 = new Headers({ "Content-Type": "text/plain", "X-Two": "2" })
console.log(h1.get("content-type"), h1.get("x-two"), h1.has("X-TWO"))
const h2 = new Headers([["a", "1"], ["A", "2"], ["b", "3"]])
console.log(h2.get("a"), h2.get("b"))
const r = new Request("http://example.com/", { headers: { "X-Req": "yes" } })
console.log(r.headers.get("x-req"))
`)
}

func TestE2EResponseStatics(t *testing.T) {
	assertSameAsNode(t, `
const j = Response.json({ a: 1 }, { status: 201, headers: { "X-J": "y" } })
console.log(j.status, j.ok, j.headers.get("content-type"), j.headers.get("x-j"), await j.text())
const r = Response.redirect("http://example.com/next", 307)
console.log(r.status, r.ok, r.headers.get("location"), JSON.stringify(await r.text()))
console.log(Response.redirect("http://example.com/x").status)
try { Response.redirect("http://example.com/", 200) } catch (e) { console.log((e as Error).name, (e as Error).message) }
const er = Response.error()
console.log(er.status, er.ok, er.type, JSON.stringify(er.statusText))
const plain = new Response("x")
console.log(plain.type, JSON.stringify(plain), Object.keys(plain))
`)
}

// A fetched Response's statusText is the server's reason phrase; its type
// is "basic".
func TestE2EFetchedResponseStatusTextAndType(t *testing.T) {
	assertSameAsNodeImports(t, `
import http from 'http'
const server = http.createServer((req, res) => { res.writeHead(404, 'Custom Reason'); res.end('x') })
server.listen(0, async () => {
  const port = (server.address() as { port: number }).port
  const res = await fetch('http://127.0.0.1:' + port + '/')
  console.log(res.type, res.status, JSON.stringify(res.statusText), await res.text())
  console.log(res.bodyUsed, res.body === res.body)
  // Through .then (the promise bridge builds the Response).
  const t = await fetch('http://127.0.0.1:' + port + '/').then((r) => r.type + ' ' + r.statusText + ' ' + r.bodyUsed)
  console.log(t)
  server.close()
})
`)
}

// A ReadableStream body: .body is that stream, and text()/json()/
// arrayBuffer() read it to its end; no body makes .body null; bodyUsed and
// the "unusable" rejection of a used or locked body.
func TestE2EResponseStreamBody(t *testing.T) {
	assertSameAsNode(t, `
const enc = new TextEncoder()
function src(parts: string[]): ReadableStream<Uint8Array> {
  let i = 0
  return new ReadableStream<Uint8Array>({
    pull(c) {
      if (i < parts.length) c.enqueue(enc.encode(parts[i++]))
      else c.close()
    },
  })
}
const s1 = src(["hel", "lo ", "wörld"])
const r1 = new Response(s1)
console.log(r1.body === s1, r1.body === r1.body, r1.bodyUsed)
console.log(await r1.text(), r1.bodyUsed)
interface P { x: number; ys: string[] }
const p: P = await new Response(src(['{"x":', ' 4, "ys": ["a",', '"b"]}'])).json()
console.log(p.x, p.ys.length, p.ys[1])
const ab = await new Response(src(["ab", "cd"])).arrayBuffer()
console.log(ab.byteLength)
const j: any = await new Response(src(["[1,2,3]"])).json()
console.log(JSON.stringify(j))
console.log(Response.error().body === null, Response.redirect("http://a.example/", 301).body === null)
console.log(new Response(null).body === null, new Response().body === null, new Response("x").body !== null)
try { new Response(src(["x"]), { status: 204 }) } catch (e) { console.log("caught", (e as Error).name) }
const bad = new ReadableStream<Uint8Array>({ pull(c) { c.error(new Error("boom")) } })
try { await new Response(bad).text() } catch (e) { console.log("rejected", (e as Error).message) }
new Response(src(["t", "hen"])).text().then((t) => console.log("then", t))
`)
}

func TestE2EResponseBodyUsed(t *testing.T) {
	assertSameAsNode(t, `
const r = new Response("once")
console.log(r.bodyUsed)
console.log(await r.text(), r.bodyUsed)
try { await r.text() } catch (e) { console.log((e as Error).name, (e as Error).message) }
const r2 = new Response("locked")
const rd = r2.body!.getReader()
console.log(r2.bodyUsed)
try { await r2.arrayBuffer() } catch (e) { console.log((e as Error).name, (e as Error).message) }
const first = await rd.read()
console.log(first.done, r2.bodyUsed)
const r3 = new Response(new ReadableStream<Uint8Array>({ start(c) { c.enqueue(new Uint8Array([104, 105])); c.close() } }))
for await (const c of r3.body!) console.log(c.length)
console.log(r3.bodyUsed)
r3.json().catch((e: Error) => console.log("json", e.name))
`)
}
