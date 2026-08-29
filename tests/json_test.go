package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// --- JSON.stringify Track S: `space` pretty-printing + generic toJSON()
// (TDD-00077). Compact output (no `space`) must stay byte-identical to before. ---

func TestE2EJSONStringifyCompactUnchanged(t *testing.T) {
	// Regression: with no space argument, output is compact exactly as before.
	assertOutput(t, `
interface Addr { city: string; zip: number; }
interface User { name: string; addr: Addr; tags: string[]; }
const u: User = { name: "bob", addr: { city: "Thessaloniki", zip: 54600 }, tags: ["a", "b"] }
console.log(JSON.stringify(u))
`, `{"name":"bob","addr":{"city":"Thessaloniki","zip":54600},"tags":["a","b"]}`)
}

func TestE2EJSONStringifyPrettyNumberSpace(t *testing.T) {
	// A numeric space indents nested objects and arrays with N spaces, and puts
	// a space after each colon — matching Node's JSON.stringify(x, null, 2).
	assertOutput(t, `
interface Addr { city: string; zip: number; }
interface User { name: string; active: boolean; addr: Addr; tags: string[]; }
const u: User = { name: "bob", active: true, addr: { city: "Thessaloniki", zip: 54600 }, tags: ["a", "b"] }
console.log(JSON.stringify(u, null, 2))
`, `{
  "name": "bob",
  "active": true,
  "addr": {
    "city": "Thessaloniki",
    "zip": 54600
  },
  "tags": [
    "a",
    "b"
  ]
}`)
}

func TestE2EJSONStringifyPrettyStringIndent(t *testing.T) {
	// A string space is used literally as the indent unit (here a tab).
	assertOutput(t, `
console.log(JSON.stringify({ x: 1, y: [1, 2] }, null, "\t"))
`, "{\n\t\"x\": 1,\n\t\"y\": [\n\t\t1,\n\t\t2\n\t]\n}")
}

func TestE2EJSONStringifyPrettyEmptiesInline(t *testing.T) {
	// An empty object/array renders inline as {}/[] even in pretty mode, exactly
	// like Node — never `{\n}` or `[\n]`.
	assertOutput(t, `
console.log(JSON.stringify({ e: {}, arr: [] }, null, 2))
`, `{
  "e": {},
  "arr": []
}`)
}

func TestE2EJSONStringifyToJSONString(t *testing.T) {
	// A user-defined toJSON() returning a string is serialized as that string
	// (re-quoted), replacing the object's own fields — as in real JS.
	assertOutput(t, `
class Money {
  amount: number = 42;
  currency: string = "EUR";
  toJSON(): string { return this.amount + this.currency; }
}
console.log(JSON.stringify(new Money()))
console.log(JSON.stringify({ price: new Money(), qty: 3 }))
`, `"42EUR"
{"price":"42EUR","qty":3}`)
}

func TestE2EJSONStringifyToJSONObject(t *testing.T) {
	// toJSON() returning a plain object shape serializes that shape (and only it,
	// so a private field the class carries but the shape omits never appears).
	assertOutput(t, `
interface Plain { id: number; label: string; }
class Widget {
  private secret: number = 99;
  id: number = 7;
  toJSON(): Plain { return { id: this.id, label: "w" }; }
}
console.log(JSON.stringify(new Widget()))
console.log(JSON.stringify(new Widget(), null, 2))
`, `{"id":7,"label":"w"}
{
  "id": 7,
  "label": "w"
}`)
}

func TestE2EJSONStringifyToJSONReturnsThis(t *testing.T) {
	// A toJSON() that returns `this` must NOT recurse forever at compile time
	// (the ADR-00221 class of bug): the guard serializes the object's own fields
	// once, matching JS's "apply toJSON once, then serialize the result".
	assertOutput(t, `
class Node2 {
  v: number = 1;
  toJSON(): Node2 { return this; }
}
console.log(JSON.stringify(new Node2()))
`, `{"v":1}`)
}

// --- JSON.parse Track P (P1): the validating parser throws a catchable
// SyntaxError on malformed input, instead of the old lenient extraction
// silently returning defaults (TDD-00077 / ADR-00223). ---

func TestE2EJSONParseMalformedThrowsCatchable(t *testing.T) {
	// A malformed document is a catchable SyntaxError (name + a position message),
	// not a silent default.
	assertOutput(t, `
interface C { x: number }
try {
  const c: C = JSON.parse("{oops}")
  console.log("NOT REACHED")
} catch (e) {
  console.log(e.name)
  console.log(e.message)
}
`, "SyntaxError\nUnexpected token in JSON at position 1")
}

func TestE2EJSONParseStrictRejections(t *testing.T) {
	// Strict JSON: single quotes, trailing commas, leading zeros, trailing junk,
	// and empty input all reject; well-formed input is accepted.
	assertOutput(t, `
interface C { x: number; y: number }
function attempt(s: string): void {
  try {
    const v: C = JSON.parse(s)
    console.log("ok")
  } catch (e) {
    console.log("reject")
  }
}
attempt("'x':1}")
attempt('{"x":1,}')
attempt('{"x":01}')
attempt('{"x":1} junk')
attempt("")
attempt('{"x":1,"y":2}')
`, "reject\nreject\nreject\nreject\nreject\nok")
}

func TestE2EJSONParseValidStillWorks(t *testing.T) {
	// Regression: valid documents (incl. escapes and a float field) still parse
	// through the existing projection after validation.
	assertOutput(t, `
interface Rec {
  name: string;
  /** @type {float64} */
  score: number;
}
const r: Rec = JSON.parse('{"name":"line1\\nok","score":9.5}')
console.log(r.name)
console.log(r.score)
`, "line1\nok\n9.5")
}

func TestE2EJSONParseDeepNestingRejected(t *testing.T) {
	// A pathologically deep document is rejected by the runtime depth guard as a
	// SyntaxError rather than overflowing the parser's stack (TDD-00077).
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		b.WriteByte('[')
	}
	for i := 0; i < 5000; i++ {
		b.WriteByte(']')
	}
	assertOutput(t, `
interface C { x: number }
try {
  const c: C = JSON.parse('`+b.String()+`')
  console.log("NOT REACHED")
} catch (e) {
  console.log(e.name)
}
`, "SyntaxError")
}

// --- JSON.parse Track P (P3): type-directed projection off the tree — nested
// objects, array/object-array fields, top-level T[], nested arrays, and the
// float-typed-variable fix, all through one path (TDD-00077 / ADR-00224). ---

func TestE2EJSONParseNestedObjectsAndArrays(t *testing.T) {
	// The original TDD-00015 goal (nested object fields) plus array-typed and
	// object-array fields, all in one document.
	assertOutput(t, `
interface Addr { city: string; zip: number }
interface User { name: string; age: number; addr: Addr; tags: string[] }
interface Team { members: User[] }
const tm: Team = JSON.parse('{"members":[{"name":"a","age":1,"addr":{"city":"X","zip":10},"tags":["p","q"]},{"name":"b","age":2,"addr":{"city":"Y","zip":20},"tags":[]}]}')
console.log(tm.members.length)
console.log(tm.members[0].name + " " + tm.members[0].addr.city + " " + tm.members[0].addr.zip)
console.log(tm.members[0].tags[1])
console.log(tm.members[1].tags.length)
`, "2\na X 10\nq\n0")
}

func TestE2EJSONParseTopLevelArrays(t *testing.T) {
	// Top-level `T[]` (primitive, object, and nested arrays) — none of which the
	// old extractor could produce.
	assertOutput(t, `
const nums: number[] = JSON.parse('[10,20,30]')
console.log(nums.length + " " + nums[0] + " " + nums[2])
const grid: number[][] = JSON.parse('[[1,2],[3,4,5]]')
console.log(grid[1].length + " " + grid[1][2])
interface P { x: number }
const ps: P[] = JSON.parse('[{"x":7},{"x":9}]')
console.log(ps[0].x + ps[1].x)
`, "3 10 30\n3 5\n16")
}

func TestE2EJSONParseFloatVariable(t *testing.T) {
	// A float-annotated top-level variable now projects via strtod — this was a
	// hard compile failure before P3's type-directed projection (ADR-00166).
	assertOutput(t, `
/** @type {float64} */
const f: number = JSON.parse('9.5')
console.log(f)
`, "9.5")
}

func TestE2EJSONParseProjectionASan(t *testing.T) {
	// Full-pipeline memory safety: a deeply-projected document (nested objects,
	// object array, string array, nested arrays) built and freed under ASan/UBSan.
	// The projection copies strings out of the tree, so freeing it can't dangle.
	bin := buildBinaryASan(t, `
interface Addr { city: string; zip: number }
interface User { name: string; addr: Addr; tags: string[] }
interface Team { members: User[] }
const tm: Team = JSON.parse('{"members":[{"name":"a","addr":{"city":"X","zip":1},"tags":["p","q"]},{"name":"b","addr":{"city":"Y","zip":2},"tags":[]}]}')
console.log(tm.members[0].addr.city)
console.log(tm.members[0].tags[1])
const nums: number[][] = JSON.parse('[[1,2],[3]]')
console.log(nums[0][1])
`)
	out, err := exec.Command(bin).CombinedOutput()
	got := string(out)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	if strings.Contains(got, "AddressSanitizer") || strings.Contains(got, "runtime error") {
		t.Fatalf("sanitizer error:\n%s", got)
	}
	if strings.TrimSpace(got) != "X\nq\n2" {
		t.Fatalf("unexpected output:\n%s", got)
	}
}

func TestE2EJSONStringifyReplacerRejected(t *testing.T) {
	// A non-null replacer (2nd arg) is a clean compile error in V1, not silently
	// ignored.
	_, err := parseAndCompile(`
const f = (k: string, v: number): number => v
console.log(JSON.stringify({ a: 1 }, f))
`)
	if err == nil {
		t.Fatal("expected a compile error for a non-null JSON.stringify replacer")
	}
	if !strings.Contains(err.Error(), "replacer argument is not supported") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EJSONStringifyRuntimeSpaceRejected(t *testing.T) {
	// A runtime (non-literal) space is rejected — pretty-print units are resolved
	// at compile time in V1.
	_, err := parseAndCompile(`
const n: number = 2
console.log(JSON.stringify({ a: 1 }, null, n))
`)
	if err == nil {
		t.Fatal("expected a compile error for a runtime JSON.stringify space argument")
	}
	if !strings.Contains(err.Error(), "space argument must be a literal") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestE2EJSONStringifyMapDict(t *testing.T) {
	// ADR-00482: map-backed dicts (index-signature objects, computed-key
	// literals, string-keyed Maps) serialize by key iteration, with
	// escaping; a number-keyed Map stays a clean rejection.
	assertOutput(t, `
interface Dict { [k: string]: number; }
const d: Dict = {};
d["a"] = 1;
d["b"] = 2.5;
console.log(JSON.stringify(d));
const m = new Map<string, string>();
m.set("x", "he\"y");
console.log(JSON.stringify(m));
`, "{\"a\":1,\"b\":2.5}\n{\"x\":\"he\\\"y\"}")
}
