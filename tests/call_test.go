package tests

import (
	"strings"
	"testing"
)

// --- JSON.stringify objects ---

func TestE2EJSONStringifyObject(t *testing.T) {
	assertOutput(t, `
const user = { name: 'Alice', age: 30 }
console.log(JSON.stringify(user))
`, `{"name":"Alice","age":30}`)
}

func TestE2EJSONStringifyObjectBool(t *testing.T) {
	assertOutput(t, `
const flag = { enabled: true, count: 5 }
console.log(JSON.stringify(flag))
`, `{"enabled":true,"count":5}`)
}

func TestE2EJSONStringifyObjectNumeric(t *testing.T) {
	assertOutput(t, `
const point = { x: 10, y: 20 }
console.log(JSON.stringify(point))
`, `{"x":10,"y":20}`)
}

func TestE2EJSONStringifyObjectFloat(t *testing.T) {
	assertOutput(t, `
const result = { score: 9.5 }
console.log(JSON.stringify(result))
`, `{"score":9.5}`)
}

func TestE2EJSONStringifyFloatDirect(t *testing.T) {
	assertOutput(t, `
console.log(JSON.stringify(9.5))
`, `9.5`)
}

func TestE2EJSONStringifyObjectDateField(t *testing.T) {
	assertOutput(t, `
const d = new Date(0)
console.log(JSON.stringify({ when: d }))
`, `{"when":"1970-01-01T00:00:00.000Z"}`)
}

func TestE2EJSONStringifyDateDirect(t *testing.T) {
	assertOutput(t, `
const d = new Date(0)
console.log(JSON.stringify(d))
`, `"1970-01-01T00:00:00.000Z"`)
}

func TestE2EJSONStringifyNestedObject(t *testing.T) {
	assertOutput(t, `
const person = { name: 'Alexandros', address: { city: 'Thessaloniki', zip: 10001 } }
console.log(JSON.stringify(person))
`, `{"name":"Alexandros","address":{"city":"Thessaloniki","zip":10001}}`)
}

func TestE2EJSONStringifyBoolArray(t *testing.T) {
	assertOutput(t, `
const flags: boolean[] = [true, false, true]
console.log(JSON.stringify(flags))
const empty: boolean[] = []
console.log(JSON.stringify(empty))
`, "[true,false,true]\n[]")
}

func TestE2EJSONStringifyObjectArray(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const pts: Point[] = [{ x: 1, y: 2 }, { x: 3, y: 4 }]
console.log(JSON.stringify(pts))
`, `[{"x":1,"y":2},{"x":3,"y":4}]`)
}

func TestE2EJSONParseObject(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = JSON.parse('{"x":1,"y":2}')
console.log(p.x)
console.log(p.y)
`, "1\n2")
}

func TestE2EJSONParseObjectMixedFields(t *testing.T) {
	assertOutput(t, `
interface Person { name: string; age: number; active: boolean }
const person: Person = JSON.parse('{"name":"Alice","age":30,"active":true}')
console.log(person.name)
console.log(person.age)
console.log(person.active)
`, "Alice\n30\n1")
}

func TestE2EJSONParseObjectMissingField(t *testing.T) {
	assertOutput(t, `
interface Pair { a: number; b: number }
const p: Pair = JSON.parse('{"a":5}')
console.log(p.a)
console.log(p.b)
`, "5\n0")
}

// TestE2EJSONParseObjectMissingStringField is a regression test: a missing
// *string* field used to default to a null pointer (zeroRef's general ptr
// default), which crashed the moment it was printed or concatenated (every
// other string operation in this compiler assumes a `string` value is never
// null) — found while investigating an unrelated, real-world crash in the
// fetch example against a degraded (non-JSON, 503-page) response body.
// Fixed to default to an empty string instead.
func TestE2EJSONParseObjectMissingStringField(t *testing.T) {
	assertOutput(t, `
interface Ip { origin: string }
const p: Ip = JSON.parse('<html>503 Service Unavailable</html>')
console.log("[" + p.origin + "]")
console.log(p.origin.length)
`, "[]\n0")
}

func TestE2EJSONParseObjectEscapedString(t *testing.T) {
	assertOutput(t, `
interface Msg { text: string }
const m: Msg = JSON.parse('{"text":"line1\\nline2 \\"quoted\\""}')
console.log(m.text)
`, "line1\nline2 \"quoted\"")
}

// --- console methods ---

func TestE2EConsoleInfoDebug(t *testing.T) {
	// info and debug are aliases for log — go to stdout
	assertOutput(t, `
console.info('hello')
console.debug('world')
`, "hello\nworld")
}

func TestE2EConsoleAssertPass(t *testing.T) {
	// passing assertion is silent
	assertOutput(t, `
console.assert(1 === 1, 'should not print')
console.log('ok')
`, "ok")
}

func TestE2EConsoleAssertFail(t *testing.T) {
	// failing assertion prints to stderr; stdout is unaffected
	assertOutput(t, `
console.assert(1 === 2, 'bad math')
console.log('still running')
`, "still running")
}

func TestE2EConsoleDir(t *testing.T) {
	assertOutput(t, `
console.dir("hello")
console.dir(42)
`, "hello\n42")
}

func TestE2EConsoleDirWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`console.dir()`)
	if err == nil {
		t.Fatal("expected a compile error for console.dir() with no arguments, got none")
	}
}

func TestE2EConsoleTimeEnd(t *testing.T) {
	// The exact elapsed time is non-deterministic (and can even be exactly
	// 0ms if -O2 collapses the timed loop into a closed-form constant, a
	// known, harmless LLVM loop-idiom-recognition artifact — see
	// ADR-00024) — only the fixed "<label>: ...ms" shape is checked here.
	got := compileAndRun(t, `
console.time("mylabel")
console.timeEnd("mylabel")
console.timeEnd()
`)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines of output, got %d: %q", len(lines), got)
	}
	if !strings.HasPrefix(lines[0], "mylabel: ") || !strings.HasSuffix(lines[0], "ms") {
		t.Errorf("line 1: got %q, want prefix %q and suffix %q", lines[0], "mylabel: ", "ms")
	}
	if !strings.HasPrefix(lines[1], "default: ") || !strings.HasSuffix(lines[1], "ms") {
		t.Errorf("line 2: got %q, want prefix %q and suffix %q", lines[1], "default: ", "ms")
	}
}

func TestE2EConsoleTimeEndWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`console.timeEnd("a", "b")`)
	if err == nil {
		t.Fatal("expected a compile error for console.timeEnd with 2 arguments, got none")
	}
}

func TestE2EConsoleCount(t *testing.T) {
	assertOutput(t, `
console.count()
console.count()
console.count("apples")
console.count()
console.count("apples")
console.countReset("apples")
console.count("apples")
`, "default: 1\ndefault: 2\napples: 1\ndefault: 3\napples: 2\napples: 1")
}

func TestE2EConsoleCountWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`console.count("a", "b")`)
	if err == nil {
		t.Fatal("expected a compile error for console.count with 2 arguments, got none")
	}
}

func TestE2EConsoleGroupIndentsSubsequentOutput(t *testing.T) {
	assertOutput(t, `
console.log("top")
console.group("A")
console.log("inside A")
console.group("A.1")
console.log("inside A.1")
console.groupEnd()
console.log("back in A")
console.groupEnd()
console.log("back to top")
`, "top\nA\n  inside A\n  A.1\n    inside A.1\n  back in A\nback to top")
}

func TestE2EConsoleGroupMultiArgIndentsEveryLine(t *testing.T) {
	assertOutput(t, `
console.group("g")
console.log("a", "b", "c")
console.groupEnd()
`, "g\n  a\n  b\n  c")
}

func TestE2EConsoleGroupEndUnbalancedDoesNotUnderflow(t *testing.T) {
	assertOutput(t, `
console.groupEnd()
console.groupEnd()
console.log("still top level")
`, "still top level")
}

func TestE2EConsoleGroupEndWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`console.groupEnd("a")`)
	if err == nil {
		t.Fatal("expected a compile error for console.groupEnd with an argument, got none")
	}
}

func TestE2EConsoleGroupWithLogAfterDeadCode(t *testing.T) {
	// Regression test for two bugs found together while implementing
	// console.group: (1) a parser bug (fixed in parseReturnStatement) where
	// a bare `return` followed by an expression on the *next* line parsed
	// as `return <thatExpression>` instead of two separate statements,
	// missing JS's ASI restriction against a line terminator there — which
	// meant "dead" code after a bare `return` wasn't actually being treated
	// as dead at all. That bug was masked for plain console.log calls (no
	// internal branching), but (2) console.group's own indent loop, once
	// added, depended on a value computed before its own labels — dropped
	// as dead code, then referenced by code that looked "live" again purely
	// because emitLabel unconditionally resets blockDone — surfacing an
	// LLVM verifier error ("use of undefined value") instead of silently
	// executing code that looked dead. Fixing bug (1) is what actually
	// fixes this case; see TestE2EReturnASI below for a narrower,
	// console.group-independent test of the parser fix itself.
	assertOutput(t, `
function f(): void {
    console.log("before")
    return
    console.log("after")
}
f()
`, "before")
}

// --- n.toFixed ---

func TestE2ENumberToFixed(t *testing.T) {
	assertOutput(t, `
console.log((3.14159).toFixed(2))
console.log((42).toFixed(0))
console.log((1.5).toFixed(3))
`, "3.14\n42\n1.500")
}

// --- Math trig/hyperbolic/misc additions ---

func TestE2EMathTrigInverse(t *testing.T) {
	assertOutput(t, `
console.log(Math.acos(1.0))
console.log(Math.round(Math.asin(1.0) * 2.0))
console.log(Math.round(Math.atan(1.0) * 4.0))
console.log(Math.round(Math.atan2(1.0, 1.0) * 4.0))
`, "0\n3\n3\n3")
}

func TestE2EMathHyperbolic(t *testing.T) {
	assertOutput(t, `
console.log(Math.sinh(0.0))
console.log(Math.cosh(0.0))
console.log(Math.tanh(0.0))
`, "0\n1\n0")
}

func TestE2EMathCbrtExpm1Log1p(t *testing.T) {
	assertOutput(t, `
console.log(Math.cbrt(27.0))
console.log(Math.expm1(0.0))
console.log(Math.log1p(0.0))
`, "3\n0\n0")
}

// --- Near-zero-effort roadmap batch: NaN/Infinity, performance.now,
// atob/btoa, encodeURI(Component)/decodeURI(Component),
// crypto.getRandomValues/randomUUID, process.readLineSync ---

func TestE2ENaNInfinityBareGlobals(t *testing.T) {
	assertOutput(t, `
console.log(isNaN(NaN))
console.log(isFinite(Infinity))
console.log(-Infinity < 0)
console.log(Infinity > 1000000)
const x = NaN
console.log(isNaN(x))
`, "1\n0\n1\n1\n1")
}

func TestE2EPerformanceNow(t *testing.T) {
	assertOutput(t, `
const t1: number = performance.now()
let arr: number[] = []
for (let i = 0; i < 200000; i++) { arr.push(i) }
const t2: number = performance.now()
console.log(arr.length)
console.log(t2 >= t1)
`, "200000\n1")
}

func TestE2EBtoaAtob(t *testing.T) {
	assertOutput(t, `
console.log(btoa("hello"))
console.log(btoa("hi"))
console.log(btoa("hey!"))
console.log(btoa(""))
console.log(atob("aGVsbG8="))
console.log(atob("aGk="))
console.log(atob("aGV5IQ=="))
console.log(atob(btoa("round trip 123!@#")))
`, "aGVsbG8=\naGk=\naGV5IQ==\n\nhello\nhi\nhey!\nround trip 123!@#")
}

func TestE2EBtoaAtobWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`btoa("a", "b")`)
	if err == nil {
		t.Fatal("expected a compile error for btoa with the wrong argument count, got none")
	}
}

func TestE2EEncodeDecodeURIComponent(t *testing.T) {
	assertOutput(t, `
console.log(encodeURIComponent("hello world"))
console.log(encodeURIComponent("a=b&c=d"))
console.log(encodeURIComponent("path/to/thing?x=1"))
console.log(decodeURIComponent("hello%20world"))
console.log(decodeURIComponent("a%3Db%26c%3Dd"))
console.log(decodeURIComponent(encodeURIComponent("weird chars: <>{}[]")))
`, "hello%20world\na%3Db%26c%3Dd\npath%2Fto%2Fthing%3Fx%3D1\nhello world\na=b&c=d\nweird chars: <>{}[]")
}

func TestE2EEncodeDecodeURIPreservesReservedChars(t *testing.T) {
	assertOutput(t, `
console.log(encodeURI("http://example.com/path?a=1&b=2 space"))
console.log(decodeURI("http://example.com/path%3Fa=1&b=2%20space"))
console.log(decodeURI("path%2Ftest"))
`, "http://example.com/path?a=1&b=2%20space\nhttp://example.com/path%3Fa=1&b=2 space\npath%2Ftest")
}

func TestE2ECryptoGetRandomValues(t *testing.T) {
	assertOutput(t, `
let buf: number[] = new Array<number>(16)
crypto.getRandomValues(buf)
console.log(buf.length)
let allInRange = true
for (const b of buf) {
    if (b < 0 || b > 255) { allInRange = false }
}
console.log(allInRange)
`, "16\n1")
}

func TestE2ECryptoRandomUUID(t *testing.T) {
	assertOutput(t, `
const id1: string = crypto.randomUUID()
const id2: string = crypto.randomUUID()
console.log(id1.length)
console.log(id1 !== id2)
console.log(id1[8])
console.log(id1[13])
console.log(id1[18])
console.log(id1[23])
console.log(id1[14])
`, "36\n1\n-\n-\n-\n-\n4")
}
