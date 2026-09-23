package tests

import "testing"

// ADR-01069: a `string | null` field holding null serialises as JSON null (it
// was "" — ADR-00683's crash placeholder), and a string is quoted by its header
// length, so an embedded NUL becomes \u0000 instead of ending the string.
func TestE2EJSONStringifyNullStringField(t *testing.T) {
	const src = `
interface P { s: string | null; n: number | null; q?: string }
const a: P = { s: null, n: null };
const b: P = { s: "x", n: 1, q: "y" };
console.log(JSON.stringify(a));
console.log(JSON.stringify(b));
console.log(JSON.stringify([a, b]));
`
	assertOutput(t, src, `{"s":null,"n":null}
{"s":"x","n":1,"q":"y"}
[{"s":null,"n":null},{"s":"x","n":1,"q":"y"}]`)
	assertSameAsNode(t, src)
}

// Control characters take the full QuoteJSONString escape set (`\b`, `\f`, and
// `\u00XX` for the rest). An embedded NUL still ends the string (strlen-bounded
// — BACKLOG §0, header-less sidecar strings).
func TestE2EJSONStringifyControlCharEscapes(t *testing.T) {
	const src = `
const a = "a\u0001b\u001f\b\f";
console.log(JSON.stringify(a));
console.log(JSON.stringify(a).length);
console.log(JSON.stringify({ k: a, arr: [a] }));
const d: any = { k: a };
console.log(JSON.stringify(d));
`
	assertOutput(t, src, `"a\u0001b\u001f\b\f"
20
{"k":"a\u0001b\u001f\b\f","arr":["a\u0001b\u001f\b\f"]}
{"k":"a\u0001b\u001f\b\f"}`)
	assertSameAsNode(t, src)
}
