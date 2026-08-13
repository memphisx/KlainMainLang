package tests

import (
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
