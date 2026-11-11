package tests

import "testing"

// fetch of a data: URL: its MIME type and percent- or base64-decoded bytes
// as a finished 200 response, a malformed one rejecting — against Node.
func TestE2EFetchDataURL(t *testing.T) {
	assertSameAsNodeImports(t, `
for (const u of ["data:text/plain,hey%20you", "data:,plain", "data:;base64,aGVsbG8=", "data:application/json;base64, eyJhIjoxfQ", "data:text/html;charset=utf-8,<b>x</b>#frag"]) {
  const r = await fetch(u);
  console.log(r.status, r.statusText, r.ok, r.headers.get("content-type"), JSON.stringify(await r.text()));
}
const b = await (await fetch("data:application/octet-stream;base64,AAEC")).arrayBuffer();
console.log(new Uint8Array(b));
try { await fetch("data:;base64,a"); } catch (e: any) { console.log(e.name, e.message); }
const r = await fetch("data:text/plain,abc");
console.log(r.url, r.redirected, r.type);
let n = 0;
for await (const ch of r.body!) n += ch.length;
console.log(n);
`)
}

// Response.url and .redirected after a redirect, and a constructed
// Response's; console.log of a Response — against Node.
func TestE2EFetchResponseURLRedirectedInspect(t *testing.T) {
	assertSameAsNodeImports(t, `
import http from "node:http";
const srv = http.createServer((req, res) => {
  if (req.url === "/a") { res.writeHead(302, { Location: "/b" }); res.end(); return; }
  res.end("at " + req.url);
});
srv.listen(0, async () => {
  const port = (srv.address() as any).port;
  const r = await fetch(`+"`"+`http://127.0.0.1:${port}/a`+"`"+`);
  console.log(new URL(r.url).pathname, r.redirected, await r.text());
  const c = new Response("x");
  console.log(JSON.stringify(c.url), c.redirected);
  srv.close();
});
`)
	assertSameAsNodeImports(t, `
console.log(new Response(null, { status: 204, statusText: "None" }));
`)
}

// A Request's init members (cache, credentials, mode, redirect, referrer,
// referrerPolicy, integrity, keepalive) with their defaults and enum
// checks, its signal following the init's, fetch(request) sending its bytes
// and honoring its signal, and console.log of a Request — against Node.
func TestE2EFetchRequestInitMembersAndSignal(t *testing.T) {
	assertSameAsNodeImports(t, `
const q = new Request("http://e.com/x", { method: "POST", body: "b" });
console.log(q.url, q.method, q.cache, q.credentials, JSON.stringify(q.destination), JSON.stringify(q.integrity), q.keepalive, q.mode, q.redirect, q.referrer, JSON.stringify(q.referrerPolicy));
const o = new Request("http://e.com/", { cache: "no-store", credentials: "omit", integrity: "sha256-x", keepalive: true, mode: "no-cors", redirect: "manual", referrer: "", referrerPolicy: "no-referrer" });
console.log(o.cache, o.credentials, o.integrity, o.keepalive, o.mode, o.redirect, JSON.stringify(o.referrer), o.referrerPolicy);
console.log(new Request("http://e.com/", { referrer: "http://e.com" }).referrer);
const bad: any[] = [{ mode: "navigate" }, { cache: "bogus" }, { redirect: "x" }, { mode: "no-cors", cache: "only-if-cached" }, { referrer: "/rel" }];
for (const b of bad) { try { new Request("http://e.com/", b); } catch (e: any) { console.log(e.name, e.message); } }
const dyn: any = { mode: "same-origin", keepalive: 1 };
const d = new Request("http://e.com/", dyn);
console.log(d.mode, d.keepalive, d.cache);
`)
	assertSameAsNodeImports(t, `
import http from "node:http";
const srv = http.createServer((req, res) => {
  const parts: Buffer[] = [];
  req.on("data", (c: Buffer) => parts.push(c));
  req.on("end", () => { const b = Buffer.concat(parts); res.end(`+"`"+`${req.method} ${b.length} ${b[1]}`+"`"+`); });
});
srv.listen(0, async () => {
  const port = (srv.address() as any).port;
  const q = new Request(`+"`"+`http://127.0.0.1:${port}/`+"`"+`, { method: "POST", body: new Uint8Array([1, 0, 2]) });
  console.log(q.signal.aborted);
  console.log(await (await fetch(q)).text());
  const ac = new AbortController();
  const q2 = new Request(`+"`"+`http://127.0.0.1:${port}/`+"`"+`, { signal: ac.signal });
  console.log(q2.signal === ac.signal, q2.signal.aborted);
  ac.abort();
  console.log(q2.signal.aborted);
  try { await fetch(q2); } catch (e: any) { console.log(e.name); }
  console.log(new Request("http://e.com/"));
  srv.close();
});
`)
}

// The Body readers bytes() and blob() (typed by the Content-Type), and a
// fetched Response's headers and status text read after its body —
// against Node.
func TestE2EFetchBodyBytesBlobAndHeadersAfterBody(t *testing.T) {
	assertSameAsNodeImports(t, `
const r = new Response("xy");
console.log(await r.bytes());
const f = await fetch("data:;base64,AAEC");
const b = await f.bytes();
console.log(b, b.byteLength, b.buffer.byteLength);
const q = new Request("http://e.com/", { method: "POST", body: "hi" });
console.log(await q.bytes());
`)
	assertSameAsNodeImports(t, `
const r = new Response("xy", { headers: { "content-type": "text/x" } });
const b = await r.blob();
console.log(b, await b.text());
const f = await (await fetch("data:image/png;base64,AAEC")).blob();
console.log(f.size, f.type);
console.log((await new Response(new Uint8Array([1])).blob()).type === "");
`)
	assertSameAsNodeImports(t, `
import http from "node:http";
const srv = http.createServer((req, res) => { res.setHeader("x-a", "1"); res.end("ok"); });
srv.listen(0, async () => {
  const port = (srv.address() as any).port;
  const r = await fetch(`+"`"+`http://127.0.0.1:${port}/`+"`"+`);
  await r.text();
  console.log(r.headers.get("x-a"), r.statusText);
  srv.close();
});
`)
}
