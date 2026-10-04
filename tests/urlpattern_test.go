package tests

import (
	"testing"
)

// --- URLPattern: the WHATWG URL Pattern Standard, as Node's global ---
// Expected outputs are Node 24's.

func TestE2EURLPatternTestAndExecResult(t *testing.T) {
	assertOutput(t, `
const pattern = new URLPattern({ pathname: "/books/:id" })
console.log(pattern.pathname, pattern.protocol, pattern.hasRegExpGroups)
console.log(pattern.test("https://example.com/books/123"), pattern.test("https://example.com/authors/9"), pattern.test("not a url"))
const m = pattern.exec("https://example.com/books/123")!
console.log(m.pathname.groups.id, m.pathname.input, m.hostname.input, m.inputs.length, JSON.stringify(m.protocol))
console.log(pattern.exec("https://example.com/nope") === null)
`, "/books/:id * false\ntrue false false\n123 /books/123 example.com 1 {\"input\":\"https\",\"groups\":{\"0\":\"https\"}}\ntrue")
}

func TestE2EURLPatternConstructorStringBaseAndOptions(t *testing.T) {
	assertOutput(t, `
const api = new URLPattern("https://api.example.com/v1/:resource/:id(\\d+)?")
console.log(api.protocol, api.hostname, api.pathname, api.hasRegExpGroups)
const only = api.exec("https://api.example.com/v1/users")!
console.log(only.pathname.groups.resource, only.pathname.groups.id === undefined)
console.log(api.test("https://api.example.com/v1/users/x"))
const rel = new URLPattern("/files/*", "https://cdn.example.com")
console.log(rel.test("/files/a/b.png", "https://cdn.example.com"), rel.test({ pathname: "/files/z" }))
console.log(new URLPattern({ pathname: "/A/:x" }, { ignoreCase: true }).test("https://e.com/a/1"))
const p = new URLPattern({ username: "admin", password: "secret" })
console.log(p.test("http://admin:secret@x.com/"), p.test("http://admin:no@x.com/"))
`, "https api.example.com /v1/:resource/:id(\\d+)? true\nusers true\nfalse\ntrue false\ntrue\ntrue false")
}

// A result's groups are listed in the order Node's (ada's) std::unordered_map
// enumerates them — the platform C++ library's order, so the oracle is the
// local Node; an invalid pattern is a TypeError.
func TestE2EURLPatternGroupOrderAndErrors(t *testing.T) {
	assertSameAsNode(t, `
for (const n of [3, 5, 6, 7, 9, 12]) {
  const names = Array.from({ length: n }, (_, i) => "g" + String.fromCharCode(97 + i) + i);
  const p = new URLPattern({ pathname: "/" + names.map(x => ":" + x).join("/") + "/*" });
  const r = p.exec("https://x.com/" + names.map((_, i) => String(i)).join("/") + "/rest")!;
  console.log(Object.keys(r.pathname.groups).join(","));
}
try { new URLPattern({ pathname: "/x/(" }) } catch (e) { console.log((e as Error).name) }
try { new URLPattern("https://example.com:99999/") } catch (e) { console.log((e as Error).name) }
console.log(new URLPattern({ search: "" }).test("https://example.com/a?q=1"), new URLPattern().test("https://anything.example/x?q=1#f"))
`)
}
