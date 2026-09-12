package tests

import (
	"strings"
	"testing"
)

// --- URL / URLSearchParams (see docs/adr/ADR-00076.md) ---

func TestE2EURLParts(t *testing.T) {
	assertOutput(t, `
const u = new URL("https://example.com:8080/a/b?x=1&y=2#frag")
console.log(u.protocol)
console.log(u.hostname)
console.log(u.host)
console.log(u.port)
console.log(u.pathname)
console.log(u.search)
console.log(u.hash)
console.log(u.origin)
`, "https:\nexample.com\nexample.com:8080\n8080\n/a/b\n?x=1&y=2\n#frag\nhttps://example.com:8080")
}

func TestE2EURLDefaultsWhenPartsAbsent(t *testing.T) {
	assertOutput(t, `
const u = new URL("https://example.com/path")
console.log(u.port)
console.log(u.search)
console.log(u.hash)
console.log(u.pathname)
`, "\n\n\n/path")
}

func TestE2EURLHrefRoundTrip(t *testing.T) {
	assertOutput(t, `
const u = new URL("https://example.com/")
console.log(u.href)
`, "https://example.com/")
}

func TestE2EURLInvalidThrows(t *testing.T) {
	assertOutput(t, `
try {
  const u = new URL("not a url")
  console.log(u.href)
} catch (e) {
  console.log("caught: " + e.message)
}
`, "caught: Invalid URL")
}

func TestE2EURLSearchParamsFromURL(t *testing.T) {
	assertOutput(t, `
const u = new URL("https://example.com/?x=1&y=hello%20world")
console.log(u.searchParams.get("x"))
console.log(u.searchParams.get("y"))
console.log(u.searchParams.has("z"))
console.log(u.searchParams.get("z"))
`, "1\nhello world\nfalse\nnull")
}

func TestE2EURLSearchParamsConstructor(t *testing.T) {
	assertOutput(t, `
const p = new URLSearchParams("a=1&b=two%20words")
console.log(p.get("a"))
console.log(p.get("b"))
`, "1\ntwo words")
}

func TestE2EURLSearchParamsConstructorStripsLeadingQuestionMark(t *testing.T) {
	assertOutput(t, `
const u = new URL("https://example.com/?x=1")
const p = new URLSearchParams(u.search)
console.log(p.get("x"))
`, "1")
}

func TestE2EURLSearchParamsEmptyConstructor(t *testing.T) {
	assertOutput(t, `
const p = new URLSearchParams()
p.set("k", "v")
console.log(p.toString())
`, "k=v")
}

func TestE2EURLSearchParamsSetAndDelete(t *testing.T) {
	// The ordered pair-list (TDD-00203): set appends a new key, delete removes in
	// place preserving insertion order — so toString stays insertion-ordered.
	assertOutput(t, `
const p = new URLSearchParams("a=1&b=2")
p.set("c", "3")
console.log(p.get("c"))
p.delete("a")
console.log(p.has("a"))
console.log(p.toString())
`, "3\nfalse\nb=2&c=3")
}

// TestE2EURLSearchParamsDuplicateKeys covers the faithful WHATWG multi-value
// model (TDD-00203): duplicate keys are preserved, get() returns the FIRST value,
// and getAll() returns every value in order — not the old Map single-value one.
func TestE2EURLSearchParamsDuplicateKeys(t *testing.T) {
	assertOutput(t, `
const p = new URLSearchParams("a=1&a=2")
console.log(p.get("a"))
console.log(p.getAll("a").length)
console.log(p.getAll("a").join(","))
console.log(p.getAll("nope").length)
`, "1\n2\n1,2\n0")
}

func TestE2EURLSearchParamsToStringPercentEncodes(t *testing.T) {
	// WHATWG URLSearchParams serializes application/x-www-form-urlencoded: a space
	// is '+' (not %20, encodeURIComponent's rule) — TDD-00203.
	assertOutput(t, `
const p = new URLSearchParams()
p.set("q", "hello world")
console.log(p.toString())
`, "q=hello+world")
}

func TestE2EMapGetMissingKeyPrintsUndefined(t *testing.T) {
	// A real Map's missing key is `undefined`, as in Node — distinct from
	// URLSearchParams.get, which is spec'd to return `null` (ADR-00833).
	assertOutput(t, `
const m = new Map<string, string>()
m.set("a", "1")
console.log(m.get("z"))
console.log(m.get("z") === undefined)
`, "undefined\ntrue")
}

func TestE2EURLComponentSetters(t *testing.T) {
	// ADR-00572: a URL component setter re-parses the URL and re-derives every
	// field, matching real Node (marker normalization: `#`/`?` on hash/search,
	// trailing `:` on protocol are tolerated).
	assertOutput(t, `
const u = new URL("https://example.com:8080/path?a=1#frag");
u.hash = "newfrag";
console.log(u.hash);
u.hash = "#withhash";
console.log(u.hash);
u.search = "b=2&c=3";
console.log(u.search);
console.log(u.searchParams.get("b"));
u.pathname = "/other";
console.log(u.pathname);
u.port = "9090";
console.log(u.host);
u.hostname = "test.org";
console.log(u.hostname);
u.protocol = "http:";
console.log(u.protocol);
u.href = "https://new.com/x";
console.log(u.href, u.hostname);
`, "#newfrag\n#withhash\n?b=2&c=3\n2\n/other\nexample.com:9090\ntest.org\nhttp:\nhttps://new.com/x new.com")
}

func TestE2EURLBaseResolution(t *testing.T) {
	// ADR-00579: new URL(url, base) resolves a relative url against base (curl's
	// own relative resolution); an absolute url overwrites the base; an invalid
	// base throws. Matches real Node v26.
	assertOutput(t, `
console.log(new URL("/other?x=1", "http://base.com/a/b").href);
console.log(new URL("page", "http://base.com/dir/").href);
console.log(new URL("https://abs.com/z", "http://base.com").href);
const b = "http://base.com/a/b/c";
console.log(new URL("../up", b).pathname);
try { new URL("rel", "not a url"); console.log("no throw"); } catch (e) { console.log("base throws"); }
console.log(new URL("http://plain.com/").href);
`, "http://base.com/other?x=1\nhttp://base.com/dir/page\nhttps://abs.com/z\n/a/up\nbase throws\nhttp://plain.com/")
}

func TestE2EURLHostUserPassSetters(t *testing.T) {
	// ADR-00577: host (split into hostname:port), username, and password setters,
	// plus username/password reads. Matches real Node v26 (a host value with no
	// port keeps the existing port).
	assertOutput(t, `
const u = new URL("http://user:pass@example.com:8080/path?q=1");
console.log(u.username);
console.log(u.password);
console.log(u.host);
u.host = "newhost.com:9090";
console.log(u.host, u.hostname, u.port);
u.host = "bare.com";
console.log(u.host, u.port);
u.username = "alice";
u.password = "secret";
console.log(u.username, u.password);
console.log(u.href);
`, "user\npass\nexample.com:8080\nnewhost.com:9090 newhost.com 9090\nbare.com:9090 9090\nalice secret\nhttp://alice:secret@bare.com:9090/path?q=1")
}

func TestE2EURLHrefSetterInvalidThrows(t *testing.T) {
	// An invalid href assignment throws a catchable Error (Node's own behavior);
	// the other setters are lenient (a bad value is silently ignored).
	assertOutput(t, `
const u = new URL("https://example.com/");
try { u.href = "not a url"; console.log("no throw"); } catch (e) { console.log("threw"); }
console.log(u.href);
`, "threw\nhttps://example.com/")
}

func TestE2EURLComponentCompoundAssignmentRejected(t *testing.T) {
	_, err := parseAndCompile(`
const url = new URL('http://example.com/path');
url.hash += 'x';
`)
	if err == nil {
		t.Fatal("expected a compile error for compound assignment to a URL component")
	}
	if !strings.Contains(err.Error(), "compound assignment to a URL component") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// --- TDD-00203: WHATWG URL / URLSearchParams conformance overhaul ---

func TestE2EURLSearchParamsMultiValueAndOrder(t *testing.T) {
	// The ordered pair-list preserves duplicate keys and cross-key insertion
	// order verbatim, and append adds without replacing (TDD-00203).
	assertOutput(t, `
const p = new URLSearchParams("a=1&b=2&a=3")
console.log(p.toString())
console.log(p.getAll("a").join(","))
p.append("a", "4")
console.log(p.getAll("a").join(","))
console.log(p.toString())
`, "a=1&b=2&a=3\n1,3\n1,3,4\na=1&b=2&a=3&a=4")
}

func TestE2EURLSearchParamsSort(t *testing.T) {
	// Stable sort by key; equal keys keep their relative value order.
	assertOutput(t, `
const p = new URLSearchParams("c=3&a=1&b=2&a=0")
p.sort()
console.log(p.toString())
`, "a=1&a=0&b=2&c=3")
}

func TestE2EURLSearchParamsIteration(t *testing.T) {
	assertOutput(t, `
const p = new URLSearchParams("a=1&b=2&a=3")
console.log(p.keys().join(","))
console.log(p.values().join(","))
let out = ""
p.forEach((v: string, k: string): void => { out = out + k + "=" + v + ";" })
console.log(out)
for (const [k, v] of p) { console.log(k + "->" + v) }
`, "a,b,a\n1,2,3\na=1;b=2;a=3;\na->1\nb->2\na->3")
}

func TestE2EURLSearchParamsNumberValueStringified(t *testing.T) {
	// A non-string value is stringified, matching JS (params.append('n', 1) → "1").
	assertOutput(t, `
const p = new URLSearchParams()
p.append("n", 1 as any)
console.log(p.get("n"))
console.log("" + p)
`, "1\nn=1")
}

func TestE2EURLSearchParamsSize(t *testing.T) {
	assertOutput(t, `
const p = new URLSearchParams("a=1&b=2&a=3")
console.log(p.size)
p.delete("a")
console.log(p.size)
`, "3\n1")
}

func TestE2EURLSearchParamsHasDeleteTwoArg(t *testing.T) {
	// WHATWG has(name, value) / delete(name, value) narrow by value (TDD-00203).
	assertOutput(t, `
const p = new URLSearchParams("a=1&a=2&b=3")
console.log(p.has("a", "2"))
console.log(p.has("a", "9"))
p.delete("a", "1")
console.log(p.toString())
`, "true\nfalse\na=2&b=3")
}

func TestE2EURLDefaultPortStripped(t *testing.T) {
	// WHATWG normalization: a default port is stripped, the host is lowercased.
	assertOutput(t, `
console.log(new URL("http://example.com:80/x").href)
console.log(new URL("https://example.com:443/y").href)
console.log(new URL("http://example.com:8080/z").href)
console.log(new URL("HTTP://ExAmple.COM/w").hostname)
`, "http://example.com/x\nhttps://example.com/y\nhttp://example.com:8080/z\nexample.com")
}

func TestE2EURLStaticCanParseAndParse(t *testing.T) {
	// WHATWG statics URL.canParse / URL.parse — non-throwing, unlike new URL().
	assertOutput(t, `
console.log(URL.canParse("https://example.com/"))
console.log(URL.canParse("not a url"))
console.log(URL.canParse("/p", "http://h.com/"))
const p = URL.parse("https://example.com/x")
console.log(p !== null ? p.href : "null")
console.log(URL.parse("garbage") === null)
`, "true\nfalse\ntrue\nhttps://example.com/x\ntrue")
}

func TestE2EURLJSONStringifyHref(t *testing.T) {
	// JSON.stringify(url) honors URL.prototype.toJSON() === href.
	assertOutput(t, `
console.log(JSON.stringify(new URL("https://example.com/a?x=1")))
`, "\"https://example.com/a?x=1\"")
}

func TestE2EURLSearchParamsConsoleLog(t *testing.T) {
	assertOutput(t, `
console.log(new URLSearchParams("a=1&b=2"))
console.log(new URLSearchParams())
`, "URLSearchParams { 'a' => '1', 'b' => '2' }\nURLSearchParams {}")
}

func TestE2EURLSearchParamsFromArrayAndObject(t *testing.T) {
	assertOutput(t, `
console.log(new URLSearchParams([["a", "1"], ["b", "2"], ["a", "3"]]).toString())
console.log(new URLSearchParams({ x: "9", y: "8" }).toString())
`, "a=1&b=2&a=3\nx=9&y=8")
}
