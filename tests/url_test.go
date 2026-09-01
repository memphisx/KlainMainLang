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
	// delete() is a swap-with-last removal (see __kml_map_str_delete in
	// runtime_collections.go) — order after a delete is not
	// insertion-order-preserving, so toString()'s result reflects that.
	assertOutput(t, `
const p = new URLSearchParams("a=1&b=2")
p.set("c", "3")
console.log(p.get("c"))
p.delete("a")
console.log(p.has("a"))
console.log(p.toString())
`, "3\nfalse\nc=3&b=2")
}

func TestE2EURLSearchParamsDuplicateKeyLastWins(t *testing.T) {
	assertOutput(t, `
const p = new URLSearchParams("a=1&a=2")
console.log(p.get("a"))
console.log(p.getAll("a").length)
console.log(p.getAll("nope").length)
`, "2\n1\n0")
}

func TestE2EURLSearchParamsToStringPercentEncodes(t *testing.T) {
	assertOutput(t, `
const p = new URLSearchParams()
p.set("q", "hello world")
console.log(p.toString())
`, "q=hello%20world")
}

func TestE2EMapGetMissingKeyPrintsNull(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, string>()
m.set("a", "1")
console.log(m.get("z"))
`, "null")
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
