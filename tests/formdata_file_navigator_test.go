package tests

import "testing"

func TestE2EFileGlobal(t *testing.T) {
	// File: a named Blob, as Node's, and buffer's File export.
	assertSameAsNode(t, `
import { File as BF } from "buffer";
const fl = new File(["abc"], "n.txt", { type: "Text/Plain", lastModified: 42 });
console.log(fl.name, fl.size, fl.type, fl.lastModified, fl instanceof Blob, String(fl), fl);
try { new (File as any)(["a"]); } catch (e: any) { console.log(e.name, e.message, e.code); }
console.log(typeof File, File.length, BF === File, typeof new File([], "x").lastModified);
fl.text().then((t) => console.log("text", t));
`)
}

func TestE2EFormDataEntries(t *testing.T) {
	// FormData's entry list: a Blob entry becomes a File, set replaces,
	// iteration keeps insertion order, and webidl's errors.
	assertSameAsNode(t, `
const f = new FormData();
console.log(f, String(f));
f.append("a", "1"); f.append("a", String(2)); f.append("c", new Blob(["q"]), "q.txt");
console.log(f.get("a"), f.getAll("a"), f.has("c"), f.get("zz"), f.getAll("zz"));
const c = f.get("c") as File; console.log(c instanceof File, c.name, c.size);
f.set("a", "z"); console.log([...f.keys()], [...f].map(([k, v]) => [k, typeof v === "string" ? v : v.name]));
f.delete("c"); for (const [k, v] of f) console.log(k, v);
f.forEach((v, k, p) => console.log("fe", k, v, p === f));
try { (f as any).append("x"); } catch (e: any) { console.log(e.name, e.message); }
try { (f as any).append("x", "1", "fn"); } catch (e: any) { console.log(e.name, e.message); }
try { new (FormData as any)(1); } catch (e: any) { console.log(e.name, e.message); }
console.log(typeof FormData, FormData.length);
`)
}

func TestE2EFormDataBodies(t *testing.T) {
	// A FormData body is undici's multipart encoding; formData() reads
	// multipart and urlencoded bodies back.
	assertSameAsNode(t, `
const f = new FormData(); f.append("a", "1\nx"); f.append("up", new Blob(["hé"], { type: "text/plain" }), "h.txt"); f.append('q"n', new Blob(["z"]));
const r = new Response(f);
console.log(r.headers.get("content-type")!.replace(/\d{11}$/, "N"));
const t = await r.text(); console.log(JSON.stringify(t.replace(/----formdata-undici-0\d{11}/g, "B")));
const back = await new Request("http://localhost/x", { method: "POST", body: f }).formData();
console.log(back.get("a"), (back.get("up") as File).name, await (back.get("up") as File).text());
const u = await new Response("a=1&b=%20x", { headers: { "content-type": "application/x-www-form-urlencoded" } }).formData();
console.log([...u.entries()]);
try { await new Response("x", { headers: { "content-type": "text/plain" } }).formData(); } catch (e: any) { console.log(e.name, e.message); }
`)
}

func TestE2EFormDataFetchRoundTrip(t *testing.T) {
	// fetch sends a FormData over the wire; the echoed body parses back.
	assertSameAsNodeImports(t, `
import http from 'http';
const srv = http.createServer((req, res) => {
  const chunks: Buffer[] = [];
  req.on('data', (c: Buffer) => chunks.push(c));
  req.on('end', () => {
    res.writeHead(200, { 'content-type': req.headers['content-type'] ?? '' });
    res.end(Buffer.concat(chunks));
  });
});
srv.listen(0, async () => {
  const port = (srv.address() as any).port;
  const f = new FormData();
  f.append('user', 'thessaloniki');
  f.append('doc', new File(['{"a":1}'], 'a.json', { type: 'application/json' }));
  const res = await fetch('http://127.0.0.1:' + port + '/up', { method: 'POST', body: f });
  const g = await res.formData();
  const doc = g.get('doc') as File;
  console.log(g.get('user'), doc.name, doc.type, await doc.text());
  srv.close();
});
`)
}

func TestE2ENavigatorGlobal(t *testing.T) {
	// navigator as Node reports it for the same host and environment.
	assertSameAsNode(t, `
console.log(navigator, navigator.hardwareConcurrency, navigator.language, navigator.languages, navigator.platform);
console.log(String(navigator), typeof Navigator, globalThis.navigator === navigator, navigator.userAgent.startsWith("Node.js/"));
try { new (Navigator as any)(); } catch (e: any) { console.log(e.name, e.message, e.code); }
console.log(navigator.languages === navigator.languages, Object.isFrozen(navigator.languages));
`)
}

func TestE2ESpreadDeclaredTupleIterator(t *testing.T) {
	// A class whose [Symbol.iterator]() declares a tuple-yielding result
	// spreads into those tuples.
	assertSameAsNode(t, `
class C { *e(): IterableIterator<[string, any]> { yield ["a", 1]; } [Symbol.iterator](): IterableIterator<[string, any]> { return this.e(); } }
console.log([...new C()]);
class D { *[Symbol.iterator](): IterableIterator<[string, number]> { yield ["b", 2]; } }
console.log([...new D()]);
`)
}

func TestE2EBufferDeprecatedConstructor(t *testing.T) {
	// Buffer(…) and new Buffer(…): Buffer.alloc for a number, Buffer.from
	// otherwise, with DEP0005 warned once.
	assertSameAsNode(t, `
process.on("warning", (w) => console.log("warning", w.name, (w as any).code));
const f = () => new Buffer(2);
console.log(f(), new Buffer("hi"), new Buffer([1, 2]), new Buffer("aGk=", "base64").toString());
console.log((Buffer as any)(3).length);
try { new (Buffer as any)(1, "utf8"); } catch (e: any) { console.log(e.name, e.code); }
`)
}
