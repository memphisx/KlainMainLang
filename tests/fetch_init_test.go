package tests

import "testing"

// fetch's init as RequestInit types it: an optional `init?: RequestInit`
// parameter (absent, a literal, an options object of another layout), and
// every BodyInit and HeadersInit form, sent with the Content-Type undici
// implies (none for bytes) — against Node.
func TestE2EFetchRequestInitParameter(t *testing.T) {
	assertSameAsNodeImports(t, `
import http from 'http'
async function api(port: number, path: string, init?: RequestInit): Promise<string> {
  const res = await fetch("http://127.0.0.1:" + port + path, init)
  return res.status + " " + await res.text()
}
const srv = http.createServer((req, res) => {
  let b = ""
  req.on("data", (c) => b += c)
  req.on("end", () => res.end(req.method + " " + (req.headers["x-a"] ?? "-") + " " + b))
})
srv.listen(0, async () => {
  const port = (srv.address() as { port: number }).port
  console.log(await api(port, "/"))
  console.log(await api(port, "/p", { method: "POST", body: "hi", headers: { "x-a": "1" } }))
  const init = { method: "PUT", headers: { "x-a": "2" }, body: JSON.stringify({ k: 1 }), extra: true }
  console.log(await api(port, "/q", init))
  srv.close()
})
`)
}

func TestE2EFetchBodyAndHeadersInitForms(t *testing.T) {
	assertSameAsNodeImports(t, `
import http from 'http'
const srv = http.createServer((req, res) => {
  const chunks: Buffer[] = []
  req.on("data", (c: Buffer) => chunks.push(c))
  req.on("end", () => {
    const b = Buffer.concat(chunks)
    res.end(req.method + " ct=" + (req.headers["content-type"] ?? "-") + " x=" + (req.headers["x-a"] ?? "-") + " len=" + b.length + " hex=" + b.toString("hex").slice(0, 16))
  })
})
async function send(port: number, init: RequestInit): Promise<void> {
  const r = await fetch("http://127.0.0.1:" + port + "/", init)
  console.log(await r.text())
}
srv.listen(0, async () => {
  const port = (srv.address() as { port: number }).port
  await send(port, { method: "POST", body: "héllo" })
  await send(port, { method: "POST", body: new Uint8Array([0, 1, 2, 255]) })
  await send(port, { method: "POST", body: new ArrayBuffer(3) })
  await send(port, { method: "POST", body: new Blob(["ab"], { type: "text/x-a" }) })
  await send(port, { method: "POST", body: new URLSearchParams({ a: "1", b: "x y" }) })
  await send(port, { method: "POST", body: "t", headers: { "Content-Type": "application/json", "X-A": "1" } })
  await send(port, { method: "PUT", body: Buffer.from("buf"), headers: [["x-a", "p1"], ["X-A", "p2"]] })
  await send(port, { method: "PATCH", headers: new Headers({ "x-a": "h" }) })
  await send(port, { method: "POST", body: new Int16Array([1, -1]) })
  srv.close()
})
`)
}

// A body on a GET or HEAD request rejects with Node's TypeError, and a
// failed fetch is a TypeError.
func TestE2EFetchGetWithBodyRejects(t *testing.T) {
	assertSameAsNode(t, `
fetch("http://127.0.0.1:1/", { body: "x" }).catch((e: Error) => console.log(e.name, e.message))
async function run() {
  try { await fetch("http://127.0.0.1:1/", { method: "head", body: "x" }) } catch (e) { console.log((e as Error).name, (e as Error).message) }
}
run()
fetch("http://127.0.0.1:1/").catch((e: Error) => console.log(e.name))
`)
}

// new Request(url, init) takes every BodyInit (its bytes copied, its implied
// Content-Type), any HeadersInit and an optional init.
func TestE2ERequestInitBodies(t *testing.T) {
	assertSameAsNode(t, `
async function show(r: Request): Promise<void> {
  const ab = await r.arrayBuffer()
  console.log(r.method, r.headers.get("content-type"), ab.byteLength, new Uint8Array(ab)[0])
}
await show(new Request("http://x.test/a", { method: "post", body: "héllo" }))
await show(new Request("http://x.test/a", { method: "PUT", body: new Uint8Array([7, 8, 9]) }))
await show(new Request("http://x.test/a", { method: "POST", body: new Blob(["zz"], { type: "text/x" }) }))
await show(new Request("http://x.test/a", { method: "POST", body: new URLSearchParams({ a: "1" }) }))
await show(new Request("http://x.test/a", { method: "POST", body: "{}", headers: { "Content-Type": "application/json" } }))
await show(new Request("http://x.test/a"))
function mk(init?: RequestInit): Request { return new Request("http://x.test/b", init) }
await show(mk())
await show(mk({ method: "DELETE" }))
try { new Request("http://x.test/a", { body: "x" }) } catch (e) { console.log((e as Error).name, (e as Error).message) }
`)
}
