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
`, "Alice\n30\ntrue")
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
// Fixed to default to an empty string instead. (That non-JSON body itself now
// throws a SyntaxError under P1's validation — TDD-00077/ADR-00223 — so this
// exercises the missing-field default with a valid object that lacks the key.)
func TestE2EJSONParseObjectMissingStringField(t *testing.T) {
	assertOutput(t, `
interface Ip { origin: string }
const p: Ip = JSON.parse('{"other":"x"}')
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

// ADR-00583: console.dir honors the { depth } option (Node's util.inspect
// nesting cap), rendering deeper values as [Object]. Default depth is 2.
func TestE2EConsoleDirDepth(t *testing.T) {
	assertOutput(t, `
const o = { label: "x", mid: { inner: { v: 1 } } };
console.dir(o);
console.dir(o, { depth: 0 });
console.dir(o, { depth: 1 });
console.dir(o, { depth: null });
`, "{ label: 'x', mid: { inner: { v: 1 } } }\n{ label: 'x', mid: [Object] }\n{ label: 'x', mid: { inner: [Object] } }\n{ label: 'x', mid: { inner: { v: 1 } } }")
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

// console.table renders Node's Unicode box-drawing table — byte-for-byte the
// same layout Node produces (verified against Node v26): an array of objects
// gets one column per field, an array of primitives a single "Values" column,
// and cells left-aligned with the column sized to its widest entry (ADR-00563).
func TestE2EConsoleTableObjects(t *testing.T) {
	got := compileAndRun(t, `console.table([{a: 1, b: 2}, {a: 30, b: 4}])`)
	want := "┌─────────┬────┬───┐\n" +
		"│ (index) │ a  │ b │\n" +
		"├─────────┼────┼───┤\n" +
		"│ 0       │ 1  │ 2 │\n" +
		"│ 1       │ 30 │ 4 │\n" +
		"└─────────┴────┴───┘"
	if got != want {
		t.Errorf("console.table(objects):\n got %q\nwant %q", got, want)
	}
}

func TestE2EConsoleTablePrimitivesAndStrings(t *testing.T) {
	got := compileAndRun(t, `console.table([10, 20, 300]); console.table(["x", "longvalue"])`)
	want := "┌─────────┬────────┐\n" +
		"│ (index) │ Values │\n" +
		"├─────────┼────────┤\n" +
		"│ 0       │ 10     │\n" +
		"│ 1       │ 20     │\n" +
		"│ 2       │ 300    │\n" +
		"└─────────┴────────┘\n" +
		"┌─────────┬─────────────┐\n" +
		"│ (index) │ Values      │\n" +
		"├─────────┼─────────────┤\n" +
		"│ 0       │ 'x'         │\n" +
		"│ 1       │ 'longvalue' │\n" +
		"└─────────┴─────────────┘"
	if got != want {
		t.Errorf("console.table(primitives):\n got %q\nwant %q", got, want)
	}
}

// A non-array argument falls back to console.log, matching Node.
func TestE2EConsoleTableFallback(t *testing.T) {
	assertOutput(t, `console.table(42)`, "42")
}

func TestE2EConsoleTimePerLabel(t *testing.T) {
	// Two concurrently-running labels track independent start times — the
	// per-label backing map, not a single global slot. Both must print with
	// their own label prefix and the "...ms" suffix.
	got := compileAndRun(t, `
console.time("outer")
console.time("inner")
console.timeEnd("inner")
console.timeEnd("outer")
`)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines of output, got %d: %q", len(lines), got)
	}
	if !strings.HasPrefix(lines[0], "inner: ") || !strings.HasSuffix(lines[0], "ms") {
		t.Errorf("line 1: got %q, want prefix %q and suffix %q", lines[0], "inner: ", "ms")
	}
	if !strings.HasPrefix(lines[1], "outer: ") || !strings.HasSuffix(lines[1], "ms") {
		t.Errorf("line 2: got %q, want prefix %q and suffix %q", lines[1], "outer: ", "ms")
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

func TestE2EConsoleGroupMultiArgSingleIndentedLine(t *testing.T) {
	assertOutput(t, `
console.group("g")
console.log("a", "b", "c")
console.groupEnd()
`, "g\n  a b c")
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

// TestE2ENumberToFixedDefaultDigits confirms the digits argument is optional
// and defaults to 0, rounding to the nearest integer as real JS does (ADR-00533).
func TestE2ENumberToFixedDefaultDigits(t *testing.T) {
	assertOutput(t, `
console.log((3.14159).toFixed())
console.log((3.7).toFixed())
console.log((2).toFixed())
`, "3\n4\n2")
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

// Math.cbrt must be correctly rounded and identical across platforms: the
// platform libm cbrt is not (glibc's runtime cbrt(27) is 3.0000000000000004,
// cbrt(2) is ...34 not ...32), so these use the deterministic fdlibm @__kml_cbrt
// (ADR-00241...). Values chosen because raw glibc cbrt gets several of them
// wrong in the last ULP; all match V8/Node exactly (ADR-00242).
func TestE2EMathCbrtCorrectlyRounded(t *testing.T) {
	assertOutput(t, `
console.log(Math.cbrt(2.0))
console.log(Math.cbrt(0.001))
console.log(Math.cbrt(123.456))
console.log(Math.cbrt(-0.5))
console.log(Math.cbrt(-27.0))
console.log(Math.cbrt(0.0))
`, "1.2599210498948732\n0.1\n4.979327984674048\n-0.7937005259840998\n-3\n0")
}

func TestE2EMathClz32Imul(t *testing.T) {
	assertOutput(t, `
console.log(Math.clz32(1))
console.log(Math.clz32(0))
console.log(Math.imul(3, 4))
console.log(Math.imul(-5, 12))
`, "31\n32\n12\n-60")
}

func TestE2EMathFround(t *testing.T) {
	// fround narrows to float32 precision then widens back — 5.5 is exactly
	// representable in float32, so it round-trips unchanged; toFixed(18)
	// exposes the extra digits a plain double wouldn't otherwise show.
	assertOutput(t, `
console.log(Math.fround(5.5))
console.log(Math.fround(0.1).toFixed(18))
`, "5.5\n0.100000001490116119")
}

func TestE2ENumberToPrecision(t *testing.T) {
	assertOutput(t, `
console.log((1).toPrecision(4))
console.log((0.0012345).toPrecision(2))
console.log((5).toPrecision(1))
`, "1.000\n0.0012\n5")
}

// TestE2ENumberToPrecisionNoArg confirms toPrecision() with no argument is
// exactly String(x) — the number's default toString — matching real JS
// (ADR-00534).
func TestE2ENumberToPrecisionNoArg(t *testing.T) {
	assertOutput(t, `
console.log((123.456).toPrecision())
console.log((1000).toPrecision())
console.log((0).toPrecision())
`, "123.456\n1000\n0")
}

func TestE2ENumberToExponential(t *testing.T) {
	// The exponent is rendered in JS's minimum-digit form (e+3, not e+03) —
	// ADR-00551.
	assertOutput(t, `
console.log((1234).toExponential(2))
console.log((0.0012345).toExponential(2))
console.log((1).toExponential(0))
console.log((123456).toExponential(3))
`, "1.23e+3\n1.23e-3\n1e+0\n1.235e+5")
}

// TestE2ENumberToPrecisionExponentMinDigits: the exponential-notation branch of
// toPrecision also renders its exponent in minimum-digit form (ADR-00551).
func TestE2ENumberToPrecisionExponentMinDigits(t *testing.T) {
	assertOutput(t, `
console.log((123456).toPrecision(2))
console.log((1234567).toPrecision(3))
`, "1.2e+5\n1.23e+6")
}

func TestE2ENumberToStringRadix(t *testing.T) {
	assertOutput(t, `
console.log((255).toString(16))
console.log((255).toString(2))
console.log((0).toString(16))
console.log((-255).toString(16))
console.log((42).toString())
console.log((35).toString(36))
`, "ff\n11111111\n0\n-ff\n42\nz")
}

// A fractional receiver is no longer truncated (ADR-00566): base 10 (and the
// no-arg form) delegate to the faithful shortest-decimal, and a power-of-two
// base expands the fractional digits bit-exactly to V8.
func TestE2ENumberToStringRadixFractional(t *testing.T) {
	assertOutput(t, `
console.log((255.5).toString())
console.log((255.5).toString(10))
console.log((255.5).toString(16))
console.log((0.5).toString(2))
console.log((3.75).toString(2))
console.log((10.25).toString(16))
console.log((0.1).toString(2))
`, "255.5\n255.5\nff.8\n0.1\n11.11\na.4\n0.0001100110011001100110011001100110011001100110011001101")
}

// TestE2ENumberToStringRadixRangeError: a radix outside 2..36 throws a
// RangeError, matching real JS (ADR-00552).
func TestE2ENumberToStringRadixRangeError(t *testing.T) {
	assertOutput(t, `
function tryRadix(r: number): void {
  try {
    console.log((5).toString(r))
  } catch (e) {
    console.log((e as Error).name)
  }
}
tryRadix(1)
tryRadix(37)
tryRadix(16)
`, "RangeError\nRangeError\n5")
}

func TestE2EObjectHasOwn(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 };
console.log(Object.hasOwn(p, "x"))
console.log(Object.hasOwn(p, "z"))
console.log(p.hasOwnProperty("y"))
console.log(p.hasOwnProperty("q"))
`, "true\nfalse\ntrue\nfalse")
}

func TestE2EObjectHasOwnDynamicKeyIsError(t *testing.T) {
	_, err := parseAndCompile(`
interface Point { x: number }
const p: Point = { x: 1 };
const k = "x";
console.log(p.hasOwnProperty(k))
`)
	if err == nil {
		t.Fatal("expected a compile error for a non-literal hasOwnProperty key")
	}
}

func TestE2EClassOwnToStringAndHasOwnPropertyWinOverBuiltins(t *testing.T) {
	// A class-declared toString()/hasOwnProperty() must take priority over
	// the generic Number/Object built-ins of the same name — dispatch order
	// regression guard (emitCall must check class methods before these).
	assertOutput(t, `
class Foo {
  x: number;
  constructor(x: number) {
    this.x = x;
  }
  toString(): string {
    return "custom-tostring";
  }
  hasOwnProperty(k: string): boolean {
    return false;
  }
}
const f = new Foo(5);
console.log(f.toString())
console.log(f.hasOwnProperty("x"))
`, "custom-tostring\nfalse")
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
`, "true\nfalse\ntrue\ntrue\ntrue")
}

func TestE2EPerformanceNow(t *testing.T) {
	assertOutput(t, `
const t1: number = performance.now()
let arr: number[] = []
for (let i = 0; i < 200000; i++) { arr.push(i) }
const t2: number = performance.now()
console.log(arr.length)
console.log(t2 >= t1)
// Origin is process start (ADR-00568), so the first reading is small, not the
// raw seconds-since-boot CLOCK_MONOTONIC value.
console.log(t1 >= 0 && t1 < 60000)
`, "200000\ntrue\ntrue")
}

func TestE2EPerformanceMarkMeasure(t *testing.T) {
	assertOutput(t, `
performance.mark("start")
let arr: number[] = []
for (let i = 0; i < 200000; i++) { arr.push(i) }
performance.mark("end")
const d1: number = performance.measure("work", "start", "end")
console.log(d1 >= 0)
const d2: number = performance.measure("work-to-now", "start")
console.log(d2 >= d1)
`, "true\ntrue")
}

func TestE2EPerformanceMarkOverwrite(t *testing.T) {
	// Re-marking "m" overwrites its timestamp (last-write-wins, documented
	// V1 scope) — a measure taken right after the second mark() spans ~no time,
	// while one spanning the whole loop before it spans the loop's duration, so
	// d2 must not exceed d1. Uses `<=`, not `<`: on a coarse monotonic clock
	// (some virtualized/CI environments) a fast loop can measure as 0, making
	// d1 == d2 == 0 — with `<` that flaked (~1 in 5 runs in a Docker VM). `<=`
	// still catches a real regression: without the overwrite, d2 would span the
	// whole loop *plus* the two measure calls, so d2 > d1 on any clock fine
	// enough to measure the loop at all.
	assertOutput(t, `
performance.mark("m")
let arr: number[] = []
for (let i = 0; i < 200000; i++) { arr.push(i) }
const d1: number = performance.measure("first", "m")
performance.mark("m")
const d2: number = performance.measure("second", "m")
console.log(d2 <= d1)
`, "true")
}

// The stricter companion to TestE2EPerformanceMarkOverwrite: instead of relying
// on a loop being slower than the monotonic clock's resolution (which fails on a
// coarse virtualized clock — see that test), it busy-waits on performance.now()
// until a guaranteed-measurable interval (~8ms, well above any plausible
// monotonic-clock resolution) has elapsed before the first measure. So d1 spans
// a real interval and a measure taken right after re-marking is *strictly*
// smaller — the assertion the overwrite is really about. Costs a few ms of spin
// and, unlike the pure test above, leans on performance.now() to build the span.
func TestE2EPerformanceMarkOverwriteMeasuredSpan(t *testing.T) {
	assertOutput(t, `
performance.mark("m")
const spinStart: number = performance.now()
while (performance.now() - spinStart < 8.0) { }
const d1: number = performance.measure("first", "m")
performance.mark("m")
const d2: number = performance.measure("second", "m")
console.log(d2 < d1)
`, "true")
}

func TestE2EPerformanceMeasureMissingMarkThrows(t *testing.T) {
	assertOutput(t, `
try {
  performance.measure("bad", "never-marked")
} catch (e) {
  console.log("caught")
  console.log(e.message)
}
`, "caught\nperformance.measure: no mark named 'never-marked'")
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

// The global decodeURIComponent/decodeURI throw a URIError on a malformed
// escape (a lone/truncated `%`, or one not followed by two hex digits),
// matching real JS (ADR-00556).
func TestE2EDecodeURIMalformedThrows(t *testing.T) {
	assertOutput(t, `
function tryDec(s: string): void {
  try {
    console.log(decodeURIComponent(s))
  } catch (e) {
    console.log((e as Error).name)
  }
}
tryDec("%")
tryDec("%ZZ")
tryDec("abc%2")
tryDec("ok%20here")
try { decodeURI("%G1") } catch (e) { console.log((e as Error).name) }
`, "URIError\nURIError\nURIError\nok here\nURIError")
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
`, "16\ntrue")
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
`, "36\ntrue\n-\n-\n-\n-\n4")
}

// queueMicrotask runs its callbacks after the current synchronous script, FIFO,
// including microtasks a microtask itself enqueues (TDD-00083 Stage 3).
func TestE2EQueueMicrotask(t *testing.T) {
	assertOutput(t, `
console.log("s1")
queueMicrotask(() => { console.log("m1"); queueMicrotask(() => { console.log("m3") }) })
queueMicrotask(() => { console.log("m2") })
console.log("s2")
`, "s1\ns2\nm1\nm2\nm3")
}

func TestE2EAtobInvalidCharacterThrows(t *testing.T) {
	// WHATWG atob: invalid input throws an InvalidCharacterError
	// DOMException (ADR-00458); previously it silently decoded garbage.
	assertOutput(t, `
try { atob("we go!"); console.log("no throw"); } catch (e) { console.log(e.name); }
console.log(typeof DOMException);
console.log(atob(btoa("round trip")));
`, "InvalidCharacterError\nfunction\nround trip")
}

// WHATWG forgiving-base64 (ADR-00550): ASCII whitespace is stripped, missing
// padding is tolerated (the trailing 2-/3-char group still decodes), and a
// remaining length ≡ 1 (mod 4) throws InvalidCharacterError.
func TestE2EAtobForgivingBase64(t *testing.T) {
	assertOutput(t, `
console.log(atob("SGVs bG8="))
console.log(atob("SGVsbG8"))
console.log(atob("YWI"))
console.log(atob(""))
try { atob("a"); console.log("no throw"); } catch (e) { console.log((e as Error).name); }
`, "Hello\nHello\nab\n\nInvalidCharacterError")
}

// WHATWG forgiving-base64 (ADR-00563): after up-to-two trailing '=' are
// stripped, an interior or excess '=' in the data region is a failure — real
// atob accepts '=' only as trailing padding.
func TestE2EAtobInteriorPaddingThrows(t *testing.T) {
	assertOutput(t, `
try { atob("a=b="); console.log("no throw"); } catch (e) { console.log((e as Error).name); }
try { atob("ab==="); console.log("no throw"); } catch (e) { console.log((e as Error).name); }
try { atob("=abc"); console.log("no throw"); } catch (e) { console.log((e as Error).name); }
console.log(atob("aGVsbG8="))
`, "InvalidCharacterError\nInvalidCharacterError\nInvalidCharacterError\nhello")
}
