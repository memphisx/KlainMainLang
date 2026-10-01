package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// The legacy `url` API (`Url`, parse, format, resolve) is Node's lib/url.js
// in TypeScript, the module's companion.
func TestE2EUrlLegacyParse(t *testing.T) {
	assertSameAsNodeImports(t, `
import url from 'url';
import { parse } from 'url';
const inputs = [
  "http://user:pass@host.com:8080/p/a/t/h?query=string#hash",
  "https://EXAMPLE.com/A b?c=d e",
  "//foo/bar",
  "/just/a/path?x=1",
  "mailto:someone@example.com",
  "javascript:alert(1)",
  "file:///etc/passwd",
  "http://[::1]:3000/x",
  "HTTP://www.Example.COM\\\\a\\\\b",
  "  http://trim.me/  ",
  "http://ex.com/a#b?c",
  "xmpp:isaacschlueter@jabber.org",
];
for (const i of inputs) console.log(JSON.stringify(parse(i)));
const pq = parse("http://a.com/?x=1&y=2&x=3", true).query;
if (pq !== null && typeof pq === "object") console.log(pq.x, pq.y, Object.keys(pq));
console.log(JSON.stringify(url.parse("//h/p", false, true)));
`)
}

func TestE2EUrlLegacyFormatResolve(t *testing.T) {
	assertSameAsNodeImports(t, `
import { format, resolve } from 'url';
console.log(format({ protocol: "https", hostname: "h.test", pathname: "/x" }));
console.log(format({ protocol: "http", host: "a.com", pathname: "x y", search: "q=1" }));
console.log(format({ protocol: "mailto", pathname: "a@b.c" }));
console.log(format({ protocol: "http:", hostname: "::1", port: 80, pathname: "/" }));
console.log(format({ protocol: "https", hostname: "h", query: { a: "1", b: ["2", "3"] } }));
console.log(format("http://a.com/b?c#d"));
console.log(format(new URL("https://u:p@h.test/x?y#z"), { auth: false, fragment: false }));
const pairs = [
  ["/one/two/three", "four"], ["http://example.com/", "/one"], ["http://example.com/one", "/two"],
  ["http://a/b/c/d;p?q", "../../../g"], ["http://a/b/c/d;p?q", "g?y"], ["http://a/b/c/d;p?q", "#s"],
  ["http://a/b/c/d;p?q", "//g"], ["mailto:a@b.com", "c@d.com"], ["http://a/b", "https://c/d"],
  ["http://a/b/c", "."], ["http://a/b/c/", ".."], ["http://a/", ""],
];
for (const [f, t] of pairs) console.log(resolve(f, t));
`)
}

// url.parse warns once (DEP0169), as Node's does; domainToASCII lowercases.
func TestE2EUrlLegacyDeprecationWarning(t *testing.T) {
	bin := buildBinaryImports(t, `
import { parse, resolve, domainToASCII } from 'url';
console.log(parse("http://a.com/x").pathname, resolve("http://a/b", "c"), domainToASCII("Example.COM"));
`)
	cmd := exec.Command(bin)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "/x http://a/c example.com" {
		t.Errorf("stdout = %q", got)
	}
	if n := strings.Count(errb.String(), "[DEP0169] DeprecationWarning: `url.parse()` behavior is not standardized"); n != 1 {
		t.Errorf("want one DEP0169 warning, got %d: %q", n, errb.String())
	}
}

// String.fromCharCode / fromCodePoint encode to UTF-8 (a surrogate pair
// joined); codePointAt decodes the character at a position.
func TestE2EStringFromCodePointUTF8(t *testing.T) {
	assertSameAsNode(t, `
console.log(String.fromCodePoint(241, 128512), String.fromCharCode(0xD83D, 0xDE00), String.fromCharCode(65, 66.9, 65535 + 68));
console.log(String.fromCharCode(233) === "é", String.fromCodePoint(0x61));
for (const ch of "añ😀") console.log(ch, ch.codePointAt(0));
try { String.fromCodePoint(0x110000); } catch (e) { console.log(e instanceof RangeError, (e as Error).message); }
try { String.fromCodePoint(1.5); } catch (e) { console.log((e as Error).message); }
try { String.fromCodePoint(-1); } catch (e) { console.log((e as Error).message); }
`)
}

// process.emitWarning's positional form takes a code third.
func TestE2EProcessEmitWarningPositionalCode(t *testing.T) {
	_, stderr := compileAndRunCaptureStderr(t, `
process.emitWarning("old thing", "DeprecationWarning", "DEP0999");
`)
	if !strings.Contains(stderr, "[DEP0999] DeprecationWarning: old thing\n") {
		t.Errorf("stderr missing positional code warning: %q", stderr)
	}
}

// A union or `any` holding an object converts to a dictionary type: a
// static object as a dictionary of its keys, a dynamic one likewise, a
// dictionary as itself.
func TestE2EUnionObjectToDictionary(t *testing.T) {
	assertSameAsNode(t, `
interface D { [k: string]: string }
interface UO { hostname?: string | null; query?: string | null | D | undefined; }
function show(u: UO): void {
    const q = u.query;
    if (q !== null && q !== undefined && typeof q === 'object') console.log(Object.keys(q), q["a"]);
    else console.log("none", q);
}
function viaUnion(u: URL | UO): void {
    if (u instanceof URL) return;
    show(u);
}
show({ hostname: "h", query: { a: "1" } });
viaUnion({ hostname: "h", query: "s" });
viaUnion({ hostname: "h", query: { a: "1" } });
const d: D = { a: "2", b: "3" };
viaUnion({ hostname: "h", query: d });
const b: any = JSON.parse('{"a":"1","z":"2"}');
const r: Record<string, string> = b;
console.log(r["a"], Object.keys(r));
`)
}

// A regex literal is a RegExp to the checker: `exec`'s result indexes to
// strings, and assignment narrowing uses them.
func TestE2ECheckerRegExpLiteralTyped(t *testing.T) {
	mustCompileError(t, `
const m = /^[a-z]+:/i.exec("http:x");
if (m) { const z: number = m[0]; }
`, "type 'string' is not assignable to type 'number'")
	assertSameAsNode(t, `
const m = /^[a-z]+:/i.exec("http:x");
let proto: string | null = null;
if (m) {
    proto = m[0];
    console.log(proto.toLowerCase());
}
`)
}

// Buffer.from is binary-safe; isEncoding; the ranged ArrayBuffer form.
func TestE2EBufferBinarySafeAndRanges(t *testing.T) {
	assertSameAsNode(t, `
const b = Buffer.from("a\u0000b", "utf8");
const c = Buffer.from("x\u0000y");
console.log(b.length, c.length, b, c);
console.log(Buffer.isEncoding("UTF-8"), Buffer.isEncoding("latin1"), Buffer.isEncoding("nope"), Buffer.isEncoding(""));
const ab = new ArrayBuffer(5);
const v = new Uint8Array(ab);
for (let i = 0; i < 5; i++) v[i] = i + 1;
console.log(Buffer.from(ab, 1, 3), Buffer.from(ab, 2));
try { Buffer.from(ab, 9); } catch (e) { console.log((e as any).code, (e as Error).message); }
try { Buffer.from(ab, 1, 9); } catch (e) { console.log((e as any).code, (e as Error).message); }
const s: any = "héllo";
console.log(Buffer.byteLength(s));
`)
}
