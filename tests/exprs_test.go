package tests

import (
	"strings"
	"testing"

	"KlainMainLang/parser"
)

// --- Single quotes and optional semicolons ---

func TestE2ESingleQuotesNoSemicolons(t *testing.T) {
	assertOutput(t, `
const greeting = 'hello'
const name = 'world'
console.log(greeting + ', ' + name + '!')
`, "hello, world!")
}

func TestE2EMixedQuoteStyles(t *testing.T) {
	// double quotes remain valid when needed (e.g. string contains single quote)
	assertOutput(t, `
const a = 'single'
const b = "double"
console.log(a)
console.log(b)
`, "single\ndouble")
}

// --- Arithmetic and variables ---

func TestE2EArithmetic(t *testing.T) {
	assertOutput(t, `
const a: number = 10
const b: number = 3
console.log(a + b)
console.log(a - b)
console.log(a * b)
console.log(a / b)
console.log(a % b)
`, "13\n7\n30\n3.3333333333333335\n1")
}

func TestE2EBoolean(t *testing.T) {
	assertOutput(t, `
console.log(1 < 2)
console.log(2 > 3)
console.log(1 === 1)
console.log(1 !== 2)
`, "true\nfalse\ntrue\ntrue")
}

// TestE2ENaNComparisons pins JS's NaN comparison semantics: NaN is not equal
// to anything (including itself), so only `!=`/`!==` against NaN are true and
// every ordered comparison (`===`, `<`, `>`, `<=`, `>=`) is false. Before
// ADR-00188 `!=`/`!==` used the ordered `one` predicate, wrongly making
// `NaN !== NaN` false.
func TestE2ENaNComparisons(t *testing.T) {
	assertOutput(t, `
const x: float64 = NaN
console.log(x === x)
console.log(x !== x)
console.log(x === 1.0)
console.log(x !== 1.0)
console.log(x < 1.0)
console.log(x > 1.0)
console.log(x >= 1.0)
// a non-NaN float still compares normally
const y: float64 = 2.5
console.log(y !== 2.5)
console.log(y !== 3.5)
`, "false\ntrue\nfalse\ntrue\nfalse\nfalse\nfalse\nfalse\ntrue")
}

// TestE2EUnannotatedBooleanVarInference: an unannotated `let b = true` must
// infer boolean, not fall through to the i64 default (which printed `1`/`0`).
// Covers a bare literal, a reassignment, a `!`-valued initializer, and a
// comparison-valued initializer.
func TestE2EUnannotatedBooleanVarInference(t *testing.T) {
	assertOutput(t, `
let a = true
console.log(a)
let b = false
console.log(b)
let c = true
c = false
console.log(c)
const y = 5
let n = !(y > 3)
console.log(n)
let m = y > 3
console.log(m)
`, "true\nfalse\nfalse\nfalse\ntrue")
}

// TestE2EUnannotatedNegativeFloatLiteral: `let z = -3.5` is a UnaryExpression,
// not a NumberLiteral, so it needs the unary-inference path to keep float type
// — otherwise it fell through to i64 and printed `-3`.
func TestE2EUnannotatedNegativeFloatLiteral(t *testing.T) {
	assertOutput(t, `
let z = -3.5
console.log(z)
let w = -7
console.log(w)
`, "-3.5\n-7")
}

func TestE2ETernary(t *testing.T) {
	assertOutput(t, `
const x: number = 5
const abs: number = x < 0 ? -x : x
console.log(abs)
`, "5")
}

// --- Logical operators (short-circuit) ---

// TestE2ELogicalShortCircuit pins down that `&&`/`||` genuinely skip the right
// operand when the left already decides the result — not just "produce the
// right boolean" but never run the right-hand call. Before ADR-00186 both
// operands were always evaluated (a plain `and i1`/`or i1`).
func TestE2ELogicalShortCircuit(t *testing.T) {
	assertOutput(t, `
function sideL(): boolean { console.log('L'); return false }
function sideR(): boolean { console.log('R'); return true }
// left is false: && must skip the right side entirely
const a: boolean = sideL() && sideR()
console.log(a)
// left is true: || must skip the right side entirely
const b: boolean = sideR() || sideL()
console.log(b)
`, "L\nfalse\nR\ntrue")
}

// TestE2ELogicalShortCircuitValues checks the non-side-effecting truth table
// still holds after the control-flow rewrite, including a nested `&&` inside an
// `||` (each operand may span its own blocks).
func TestE2ELogicalShortCircuitValues(t *testing.T) {
	assertOutput(t, `
console.log(true && true)
console.log(true && false)
console.log(false && true)
console.log(true || false)
console.log(false || false)
console.log((1 < 2 && 3 < 2) || 4 < 5)
`, "true\nfalse\nfalse\ntrue\nfalse\ntrue")
}

// --- Exponentiation operator (ES2016) ---

// TestE2EExponentiation covers `**`: integer results stay exact i64, the
// operator binds tighter than `*` and is right-associative.
func TestE2EExponentiation(t *testing.T) {
	assertOutput(t, `
console.log(2 ** 10)
console.log(3 ** 3)
console.log(2 ** 0)
console.log(2 ** 3 ** 2)
console.log(5 ** 2 * 2)
console.log(10 ** 3 + 1)
`, "1024\n27\n1\n512\n50\n1001")
}

// TestE2EExponentiationNegativeExponent: `number` is a double (TDD-00123), so a
// negative exponent yields the real fractional power, as in JS.
func TestE2EExponentiationNegativeExponent(t *testing.T) {
	assertOutput(t, `
console.log(2 ** -1)
console.log(10 ** -3)
`, "0.5\n0.001")
}

// TestE2EExponentiationFloat uses float operands, which route through libm
// pow() and yield a float.
func TestE2EExponentiationFloat(t *testing.T) {
	assertOutput(t, `
const f: float64 = 2.0
console.log(f ** 10.0)
`, "1024")
}

// TestE2EExponentiationCompoundAssign covers `**=`.
func TestE2EExponentiationCompoundAssign(t *testing.T) {
	assertOutput(t, `
let n: number = 2
n **= 5
console.log(n)
n **= 2
console.log(n)
`, "32\n1024")
}

// TestE2EExponentiationParenthesizedUnaryLeft: `(-2) ** 2` is valid (4) while a
// bare `-2 ** 2` is a SyntaxError — the parentheses disambiguate.
func TestE2EExponentiationParenthesizedUnaryLeft(t *testing.T) {
	assertOutput(t, `
console.log((-2) ** 2)
console.log(-(2 ** 2))
`, "4\n-4")
}

// TestE2EExponentiationUnaryLeftRejected: an unparenthesized unary on the left
// of `**` is rejected, matching JS's early SyntaxError.
func TestE2EExponentiationUnaryLeftRejected(t *testing.T) {
	_, err := parseAndCompile(`console.log(-2 ** 2)`)
	if err == nil {
		t.Fatal("expected a parse error for an unparenthesized unary on the left of '**', got none")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected the '**' ambiguity error, got: %v", err)
	}
}

// --- Bitwise operators ---

func TestE2EBitwiseOps(t *testing.T) {
	assertOutput(t, `
const a: number = 10
const b: number = 12
console.log(a & b)
console.log(a | b)
console.log(a ^ b)
console.log(~a)
console.log(a << 1)
console.log(a >> 1)
`, "8\n14\n6\n-11\n20\n5")
}

// TestE2EArithmeticCompoundAssign covers +=/-=/*=//=/%= together — %= in
// particular had no coverage anywhere: %= wasn't even a lexer token before
// this test was added (the lexer's '%' case never checked for a following
// '=', unlike +/-/*//, so `x %= 3` was a parse error, not just an
// unimplemented codegen path).
func TestE2EArithmeticCompoundAssign(t *testing.T) {
	assertOutput(t, `
let x: number = 10
x += 5
console.log(x)
x -= 3
console.log(x)
x *= 2
console.log(x)
x /= 4
console.log(x)
x %= 4
console.log(x)
`, "15\n12\n24\n6\n2")
}

func TestE2EBitwiseAssign(t *testing.T) {
	assertOutput(t, `
let x: number = 15
x &= 6
console.log(x)
x |= 8
console.log(x)
x ^= 3
console.log(x)
`, "6\n14\n13")
}

// TestE2EBitwiseResultIsNumber: a JS bitwise/shift result is a Number (double),
// not an integer, so it participates in float division (TDD-00123 Stage 2). The
// integer-valued results still print without a trailing `.0`.
func TestE2EBitwiseResultIsNumber(t *testing.T) {
	assertOutput(t, `
console.log((7 & 6) / (7 & 5))
console.log((1 << 4) / (1 << 1))
console.log(~5 / 2)
console.log((6 | 1) / 2)
console.log((6 ^ 3) / 2)
console.log((-1 >>> 0) / 2)
`, "1.2\n8\n-3\n3.5\n2.5\n2147483647.5")
}

// --- Bitwise shift 32-bit semantics (ToInt32/ToUint32, shift count masked to 0-31) ---

func TestE2ELeftShiftInt32Overflow(t *testing.T) {
	assertOutput(t, `
console.log(1 << 31)
console.log(1 << 63)
`, "-2147483648\n-2147483648")
}

func TestE2EUnsignedRightShiftUint32(t *testing.T) {
	assertOutput(t, `
console.log(-1 >>> 0)
console.log(4294967296 >>> 0)
`, "4294967295\n0")
}

func TestE2ELeftShiftOperandToInt32Wraparound(t *testing.T) {
	// Test262 S9.5_A2.1_T1.js: ToInt32 wraparound values for the left operand.
	assertOutput(t, `
console.log(2147483648 << 0)
console.log(-4294967296 << 0)
`, "-2147483648\n0")
}

func TestE2EShiftCountMaskedTo5Bits(t *testing.T) {
	assertOutput(t, `
console.log(1 << 32)
console.log(1 << 33)
console.log(8 >> 33)
`, "1\n2\n4")
}

func TestE2ERightShiftArithmeticSignExtends(t *testing.T) {
	assertOutput(t, `
console.log(-8 >> 1)
`, "-4")
}

func TestE2EShiftCompoundAssignmentUsesInt32Semantics(t *testing.T) {
	assertOutput(t, `
let x: number = 1
x <<= 31
console.log(x)
`, "-2147483648")
}

// --- Hex / binary / octal literals ---

func TestE2EHexLiterals(t *testing.T) {
	assertOutput(t, `
const mask: number = 0xFF
console.log(mask)
const rgb: number = 0xFF0000
console.log(rgb)
const combined: number = 0xFF & 0b11110000
console.log(combined)
`, "255\n16711680\n240")
}

func TestE2EBinaryOctalLiterals(t *testing.T) {
	assertOutput(t, `
const a: number = 0b0001
const b: number = 0b0110
console.log(a | b)
console.log(a & b)
const perms: number = 0o755
console.log(perms)
`, "7\n0\n493")
}

// --- Numeric separators (1_000_000) ---

func TestE2ENumericSeparators(t *testing.T) {
	assertOutput(t, `
const million: number = 1_000_000
console.log(million)
const hex: number = 0x1_FF
console.log(hex)
const bin: number = 0b1010_0101
console.log(bin)
const oct: number = 0o7_55
console.log(oct)
let pi = 3.14_159
console.log(pi)
`, "1000000\n511\n165\n493\n3.14159")
}

// --- Logical assignment operators (&&=, ||=, ??=) ---

func TestE2ELogicalAndAssign(t *testing.T) {
	assertOutput(t, `
let a: number = 5
a &&= 10
console.log(a)
let b: number = 0
b &&= 10
console.log(b)
`, "10\n0")
}

func TestE2ELogicalOrAssign(t *testing.T) {
	assertOutput(t, `
let a: number = 0
a ||= 7
console.log(a)
let b: number = 3
b ||= 7
console.log(b)
`, "7\n3")
}

func TestE2ENullishAssign(t *testing.T) {
	assertOutput(t, `
let a: string | null = null
a ??= "default"
console.log(a)
let b: string | null = "keep"
b ??= "default"
console.log(b)
`, "default\nkeep")
}

// ADR-00600: logical assignment against a computed dynamic-object key.
func TestE2EDynamicObjectLogicalAssign(t *testing.T) {
	assertOutput(t, `
const obj: { [k: string]: string } = {}
obj["a"] = "hello"
obj["a"] ??= "default"
obj["b"] ??= "filled"
console.log(obj["a"], obj["b"])
const nums: { [k: string]: number } = {}
nums["x"] = 0
nums["x"] ||= 42
nums["y"] = 5
nums["y"] &&= 10
console.log(nums["x"], nums["y"])
`, "hello filled\n42 10")
}

func TestE2ELogicalAssignRHSNotEvaluatedWhenShortCircuited(t *testing.T) {
	// The right side must never run down the short-circuited branch — not
	// just "produce the right final value" but genuinely skip the call.
	assertOutput(t, `
function sideEffect(): number {
  console.log('called')
  return 99
}
let a: number = 5
a &&= sideEffect()
console.log(a)
let b: number = 1
b ||= sideEffect()
console.log(b)
`, "called\n99\n1")
}

func TestE2ELogicalAssignOnFieldsArraysAndStatics(t *testing.T) {
	assertOutput(t, `
interface Box { val: number }
const box: Box = { val: 0 }
box.val ||= 42
console.log(box.val)

const arr: number[] = [0, 5]
arr[0] ||= 99
arr[1] &&= 3
console.log(arr[0])
console.log(arr[1])

class Counter {
  static count: number;
  static {
    Counter.count = 0
  }
}
Counter.count ||= 100
console.log(Counter.count)
`, "42\n99\n3\n100")
}

func TestE2ENumericSeparatorMisplacedIsError(t *testing.T) {
	if _, err := parser.Parse(`const x: number = 1__000`); err == nil {
		t.Fatal("expected a compile error for a doubled numeric separator, got none")
	}
	if _, err := parser.Parse(`const y: number = 1_`); err == nil {
		t.Fatal("expected a compile error for a trailing numeric separator, got none")
	}
}

func TestE2ENumericSeparatorInLegacyLeadingZeroIsError(t *testing.T) {
	// A numeric separator is a SyntaxError inside a legacy octal / non-octal
	// decimal literal (leading 0 then a digit or '_'), even in non-strict
	// mode — ADR-00197. `1_000` and `0.5_5` stay valid.
	for _, bad := range []string{`0_0`, `08_0`, `0_9`, `00_0`} {
		if _, err := parser.Parse("const x = " + bad); err == nil {
			t.Fatalf("expected a compile error for %q, got none", bad)
		}
	}
	for _, ok := range []string{`1_000`, `0.5_5`, `0`, `0x1_F`} {
		if _, err := parser.Parse("const x = " + ok); err != nil {
			t.Fatalf("expected %q to parse, got %v", ok, err)
		}
	}
}

// --- Null coalescing ?? ---

func TestE2ENullCoalescing(t *testing.T) {
	assertOutput(t, `
const s: string = 'hello'
const result: string = s ?? 'default'
console.log(result)
`, "hello")
}

func TestE2ENullCoalescingNumber(t *testing.T) {
	assertOutput(t, `
const n: number = 42
const r: number = n ?? 99
console.log(r)
`, "42")
}

func TestE2ENullCoalescingChained(t *testing.T) {
	assertOutput(t, `
const a: string = 'first'
const b: string = 'second'
const r: string = a ?? b ?? 'fallback'
console.log(r)
`, "first")
}

// --- Nullable non-pointer scalars (TDD-00064 Stage 1) ---

// A `number | null` local now carries a real presence bit rather than
// collapsing null onto the value 0, so `x ?? d` and `x === null` distinguish a
// genuine null from a legitimately-present 0 (the exact bugs #1 and its
// `=== null` sibling this stage removes).
func TestE2ENullableScalarCoalesce(t *testing.T) {
	assertOutput(t, `
let x: number | null = null
console.log(x ?? 42)
let y: number | null = 7
console.log(y ?? 42)
let z: number | null = 0
console.log(z ?? 42)
`, "42\n7\n0")
}

func TestE2ENullableScalarNullEquality(t *testing.T) {
	assertOutput(t, `
let z: number | null = 0
console.log(z === null)
let x: number | null = null
console.log(x === null)
console.log(x !== null)
let y: number | null = 5
console.log(y !== null)
`, "false\ntrue\nfalse\ntrue")
}

func TestE2ENullableScalarBoolean(t *testing.T) {
	assertOutput(t, `
let a: boolean | null = null
console.log(a ?? true)
let b: boolean | null = false
console.log(b ?? true)
console.log(b === null)
`, "true\nfalse\nfalse")
}

// Copying one nullable scalar to another preserves null-ness rather than
// reading it back as a present 0.
func TestE2ENullableScalarCopyPreservesNull(t *testing.T) {
	assertOutput(t, `
let a: number | null = null
let b: number | null = a
console.log(b ?? 99)
a = 5
let c: number | null = a
console.log(c ?? 99)
`, "99\n5")
}

func TestE2ENullableScalarReassignAndNullishAssign(t *testing.T) {
	assertOutput(t, `
let a: number | null = 5
a = null
console.log(a ?? 1)
a = 8
console.log(a ?? 1)
let f: number | null = null
f ??= 100
console.log(f)
let g: number | null = 3
g ??= 100
console.log(g)
`, "1\n8\n100\n3")
}

// A logical compound assignment (??=/&&=/||=) on a nullable-scalar *field* must
// route through the presence-flagged { i1, T } path, not the generic
// emitLogicalCompoundAssign that reads the aggregate as a non-ptr value and
// no-op'd `??=` (a null field could never compare equal to null). Covers all
// three operators, the present-value-preserved case for each, and a class
// field — the read side already worked; this is the write side.
func TestE2ENullableScalarFieldLogicalAssign(t *testing.T) {
	assertOutput(t, `
interface Box { v: number | null; n: number | null }
const b: Box = { v: null, n: 42 }
b.v ??= 5
b.n ??= 99
console.log(b.v)
console.log(b.n)
let b2: Box = { v: 3, n: 0 }
b2.v &&= 7
b2.n &&= 7
console.log(b2.v)
console.log(b2.n)
let b3: Box = { v: 0, n: 8 }
b3.v ||= 11
b3.n ||= 11
console.log(b3.v)
console.log(b3.n)
class C { x: number | null = null }
const c = new C()
c.x ??= 20
console.log(c.x)
`, "5\n42\n7\n0\n11\n8\n20")
}

// A nullable scalar prints its real JS rendering — `null` when absent, its
// value when present (a present 0/false is not "null") — rather than the
// payload 0 the bare representation used to surface (TDD-00064 Stage 2).
func TestE2ENullableScalarPrint(t *testing.T) {
	assertOutput(t, `
let x: number | null = null
console.log(x)
let y: number | null = 5
console.log(y)
let z: number | null = 0
console.log(z)
let b: boolean | null = null
console.log(b)
let c: boolean | null = false
console.log(c)
`, "null\n5\n0\nnull\nfalse")
}

// Flow narrowing: inside `if (x !== null)` the local is known present, so
// `x === null` folds to false and it prints as its value.
func TestE2ENullableScalarNarrowingGuard(t *testing.T) {
	assertOutput(t, `
let m: number | null = 7
if (m !== null) {
  console.log(m)
  console.log(m === null)
}
let n: number | null = null
if (n === null) {
  console.log("absent")
} else {
  console.log(n)
}
`, "7\nfalse\nabsent")
}

// Early-exit narrowing: after `if (w === null) return`, the boxed local is
// proven present for the rest of the enclosing scope; a null assigned into it
// mid-function is tracked and short-circuits at the guard.
func TestE2ENullableScalarEarlyExitNarrowing(t *testing.T) {
	assertOutput(t, `
function f(n: number): number {
  let w: number | null = 8
  if (n < 0) { w = null }
  if (w === null) { return -1 }
  return w + 100
}
console.log(f(1))
console.log(f(-1))
`, "108\n-1")
}

// --- Optional chaining ?. ---

func TestE2EOptionalChainingLength(t *testing.T) {
	assertOutput(t, `
const s: string = 'hello'
const n: number = s?.length ?? 0
console.log(n)
`, "5")
}

// TestE2EOptionalChainingArray confirms `arr?.length` and `arr?.method()` on an
// array receiver work — an array is a {ptr, i64} aggregate, not a bare pointer,
// so the previous unconditional `icmp eq ptr` null check produced invalid IR;
// it now falls back to plain access (ADR-00539).
func TestE2EOptionalChainingArray(t *testing.T) {
	assertOutput(t, `
const a: number[] = [1, 2, 3]
console.log(a?.length)
const empty: number[] = []
console.log(empty?.length)
console.log(a?.map((x: number) => x * 2).join(","))
`, "3\n0\n2,4,6")
}

func TestE2EOptionalChainingCombined(t *testing.T) {
	assertOutput(t, `
const greeting: string = 'world'
const msg: string = greeting ?? 'stranger'
console.log(msg)
const len: number = greeting?.length ?? 0
console.log(len)
`, "world\n5")
}

// --- typeof ---

func TestE2ETypeofPrimitives(t *testing.T) {
	assertOutput(t, `
const n: number = 42
const s: string = 'hi'
const b: boolean = true
console.log(typeof n)
console.log(typeof s)
console.log(typeof b)
`, "number\nstring\nboolean")
}

func TestE2ETypeofGuard(t *testing.T) {
	assertOutput(t, `
const x: number = 7
if (typeof x === 'number') { console.log('yes') } else { console.log('no') }
`, "yes")
}

// ADR-00607: `typeof value.method` is "function" — a class method or a known
// built-in string/array method — while a field, `length`, or an accessor still
// answers through normal inference.
func TestE2ETypeofValueMethodRef(t *testing.T) {
	assertOutput(t, `
class C { m(): number { return 1; } field: number = 5; get val(): string { return "x"; } }
const c = new C()
console.log(typeof c.m)
console.log(typeof c.field)
console.log(typeof c.val)
console.log(typeof "hi".slice)
console.log(typeof [1].push)
console.log(typeof "hi".length)
console.log(typeof [1].length)
`, "function\nnumber\nstring\nfunction\nfunction\nnumber\nnumber")
}

func TestE2ETypeofFunction(t *testing.T) {
	assertOutput(t, `
function add(a: number, b: number): number { return a + b }
console.log(typeof add)
`, "function")
}

// Every heap-backed built-in value is typeof "object" (a conformance-sweep
// find: they all fell through to "string"/"number"); null is "object" per JS.
func TestE2ETypeofBuiltinValues(t *testing.T) {
	assertOutput(t, `
async function af(): Promise<number> { return 1 }
function* g(): number { yield 1 }
class K { x: number; constructor() { this.x = 1 } }
async function main2(): Promise<void> {
  const p = af()
  console.log(typeof p)
  console.log(typeof new K())
  console.log(typeof new Map<string, number>())
  console.log(typeof new Set<number>())
  const m = new Map<string, number>()
  console.log(typeof m)
  const gi = g()
  console.log(typeof gi)
  console.log(typeof null)
  console.log(typeof new Date())
  console.log(typeof new Error("x"))
}
main2()
`, "object\nobject\nobject\nobject\nobject\nobject\nobject\nobject\nobject")
}

// typeof on namespace/constructor references and unresolved identifiers gives
// the JS answer statically: constructors and implemented statics are
// "function", Math/JSON are "object", an unimplemented static or undeclared
// name is "undefined" — never a silent "number".
func TestE2ETypeofNamespacesAndUndeclared(t *testing.T) {
	assertOutput(t, `
class K { }
console.log(typeof Promise)
console.log(typeof Promise.all)
console.log(typeof Promise.race)
console.log(typeof Promise.try)
console.log(typeof Math)
console.log(typeof Math.floor)
console.log(typeof Math.PI)
console.log(typeof JSON.parse)
console.log(typeof K)
console.log(typeof fetch)
console.log(typeof totallyUndeclared)
`, "function\nfunction\nfunction\nundefined\nobject\nfunction\nnumber\nfunction\nfunction\nfunction\nundefined")
}

// ADR-00596: typeof on a static method of a common namespace answers "function"
// (was falling through to the "number" inference default).
func TestE2ETypeofNamespaceStaticMethods(t *testing.T) {
	assertOutput(t, `
console.log(typeof Object.keys)
console.log(typeof console.log)
console.log(typeof Number.isInteger)
console.log(typeof Date.now)
console.log(typeof Array.isArray)
console.log(typeof String.fromCharCode)
console.log(typeof Number.MAX_VALUE)
`, "function\nfunction\nfunction\nfunction\nfunction\nfunction\nnumber")
}

// --- const reassignment rejection ---

func TestE2EConstScalarReassignmentRejected(t *testing.T) {
	_, err := parseAndCompile(`
const x = 5
x = 10
console.log(x)
`)
	if err == nil {
		t.Fatal("expected a compile error for reassigning a const-declared scalar, got none")
	}
}

func TestE2EConstCompoundAssignmentRejected(t *testing.T) {
	_, err := parseAndCompile(`
const x = 5
x += 1
console.log(x)
`)
	if err == nil {
		t.Fatal("expected a compile error for compound-assigning a const-declared scalar, got none")
	}
}

func TestE2EConstObjectRebindingRejected(t *testing.T) {
	_, err := parseAndCompile(`
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
p = { x: 3, y: 4 }
`)
	if err == nil {
		t.Fatal("expected a compile error for rebinding a const-declared object, got none")
	}
}

func TestE2EConstCapturedByClosureReassignmentRejected(t *testing.T) {
	_, err := parseAndCompile(`
const x = 5
const f = () => { x = 10 }
console.log(x)
`)
	if err == nil {
		t.Fatal("expected a compile error for reassigning a const captured by a closure, got none")
	}
}

func TestE2ELetReassignmentStillWorks(t *testing.T) {
	assertOutput(t, `
let y = 5
y = 10
console.log(y)
`, "10")
}

func TestE2EConstArrayElementMutationStillWorks(t *testing.T) {
	assertOutput(t, `
const arr: number[] = [1, 2, 3]
arr[0] = 99
console.log(arr[0])
`, "99")
}

func TestE2EConstObjectFieldMutationStillWorks(t *testing.T) {
	assertOutput(t, `
interface Point { x: number; y: number }
const p: Point = { x: 1, y: 2 }
p.x = 99
console.log(p.x)
`, "99")
}

// --- null / undefined ---

func TestE2ENullLiteral(t *testing.T) {
	assertOutput(t, `
let x: string | null = null
console.log(x === null)
console.log(x !== null)
x = "hello"
console.log(x === null)
console.log(x !== null)
`, "true\nfalse\nfalse\ntrue")
}

func TestE2ENullableArrayField(t *testing.T) {
	// Real bug found investigating destructuring defaults (ADR-00158):
	// coerce() left a `null` literal assigned to a `T[] | null` field as a
	// bare `ptr null`, but an array field's real struct storage type is
	// the {ptr, i64} aggregate (StructFieldIR), not a plain ptr — produced
	// invalid IR (`store {ptr, i64} null, ...`) that failed at the clang
	// step, not the codegen step.
	assertOutput(t, `
interface Box { items: number[] | null }
let empty: Box = { items: null }
console.log(empty.items === null)
let full: Box = { items: [1, 2, 3] }
console.log(full.items === null)
`, "true\nfalse")
}

func TestE2ENullInTemplate(t *testing.T) {
	assertOutput(t, `
const n: string | null = null
console.log(`+"`"+`value is ${n}`+"`"+`)
`, "value is null")
}

func TestE2EUndefinedInTemplate(t *testing.T) {
	assertOutput(t, `
const u = undefined
console.log(`+"`"+`u is ${u}`+"`"+`)
`, "u is undefined")
}

func TestE2ENullNullishCoalesce(t *testing.T) {
	assertOutput(t, `
const a = null ?? "fallback"
const b: string | null = "real"
const c = b ?? "fallback"
console.log(a)
console.log(c)
`, "fallback\nreal")
}

func TestE2ENullEquality(t *testing.T) {
	assertOutput(t, `
console.log(null === null)
console.log(null === undefined)
console.log(null !== null)
`, "true\ntrue\nfalse")
}

func TestE2ENullOptionalChain(t *testing.T) {
	assertOutput(t, `
const s: string | null = null
console.log(s?.length)
const t2: string | null = "hello"
console.log(t2?.length)
`, "0\n5")
}

// --- Comma / sequence operator (ADR-00179) ---

// A sequence yields its last operand's value; earlier operands run for side
// effects only.
func TestE2ESequenceYieldsLast(t *testing.T) {
	assertOutput(t, `
const x = (1, 2, 3);
console.log(x);
`, "3")
}

func TestE2ESequenceSideEffects(t *testing.T) {
	assertOutput(t, `
let r = (console.log("a"), console.log("b"), 5);
console.log(r);
`, "a\nb\n5")
}

// An operand may reference a local variable and mutate it (assignment is an
// expression), and the sequence's value takes the last operand's type.
func TestE2ESequenceLocalAndAssignment(t *testing.T) {
	assertOutput(t, `
let a = 0;
let b = (a = 7, a + 1);
console.log(a, b);
`, "7 8")
}

// The last operand's type drives the result even when it differs from earlier
// operands (a regression guard: the value's storage slot must match).
func TestE2ESequenceLastTypeWins(t *testing.T) {
	assertOutput(t, `
const s = (1, "hello");
console.log(s);
console.log(typeof s);
`, "hello\nstring")
}

// A sequence works in a condition position.
func TestE2ESequenceInCondition(t *testing.T) {
	assertOutput(t, `
let a = 0;
if ((a = 1, a === 1)) { console.log("yes"); } else { console.log("no"); }
`, "yes")
}

// Math on NaN/±Infinity input (ADR-00286): the float paths preserve
// non-finite values instead of hitting fptosi poison, min/max propagate NaN
// via llvm.minimum/maximum, and round uses JS tie-toward-+Infinity semantics
// including the signed zero.
func TestE2EMathNaNInfinity(t *testing.T) {
	assertOutput(t, `
console.log(Math.floor(NaN), Math.ceil(NaN), Math.round(NaN), Math.trunc(NaN), Math.sign(NaN));
console.log(Math.floor(Infinity), Math.ceil(-Infinity), Math.trunc(Infinity));
console.log(Math.min(1, NaN), Math.max(NaN, 2));
console.log(Math.min(1, Infinity), Math.max(1, -Infinity));
console.log(Math.sign(Infinity), Math.sign(-Infinity));
console.log(Math.round(2.5), Math.round(-2.5), Math.round(-0.5));
`, "NaN NaN NaN NaN NaN\nInfinity -Infinity Infinity\nNaN NaN\n1 1\n1 -1\n3 -2 -0")
}

// parseInt/parseFloat return NaN on a no-digits input, and
// charCodeAt/codePointAt are bounds-checked (NaN out of range) instead of
// reading past the string (ADR-00287).
func TestE2EParseAndCharCodeNaN(t *testing.T) {
	assertOutput(t, `
console.log(parseInt("abc"), parseFloat("abc"), parseInt(""), parseFloat(""));
console.log(parseInt("12px"), parseFloat("3.5kg"), parseInt("  42"), parseFloat("  .5"));
console.log(Number.isNaN(parseInt("abc")), Number.isNaN(parseFloat("xyz")));
console.log(parseInt("42") + 1);
const s = "abc";
console.log(s.charCodeAt(0), s.charCodeAt(2), s.charCodeAt(3), s.charCodeAt(-1), s.charCodeAt(100));
console.log(String.fromCharCode("A".charCodeAt(0) + 1));
`, "NaN NaN NaN NaN\n12 3.5 42 0.5\ntrue true\n43\n97 99 NaN NaN NaN\nB")
}

// TestE2ETernaryNullableScalar confirms a `cond ? scalar : null` ternary is a
// nullable scalar — the null survives into ??, string concat, === null, and an
// annotated or unannotated binding, whether returned from a function or written
// inline (ADR-00538). A string-valued ternary infers correctly too.
func TestE2ETernaryNullableScalar(t *testing.T) {
	assertOutput(t, `
function get(b: boolean): number | null { return b ? 3 : null }
console.log(get(false) ?? -1)
console.log(get(true) ?? -1)
console.log("c" + get(false))
console.log("c" + get(true))
console.log(get(false) === null)
const a = get(false)
console.log(a ?? -1)
const cond: boolean = get(true) === 3
const y = cond ? 5 : null
const y2 = cond ? null : 9
console.log(y ?? -1, y2 ?? -1)
const s = cond ? "hi" : "bye"
console.log(s)
`, "-1\n3\ncnull\nc3\ntrue\n-1\n5 -1\nhi")
}

// TestE2EExponentNumberLiterals confirms ES exponent notation in number
// literals (e/E, optional sign, digits, numeric separators) lexes and evaluates
// to the right doubles, cross-checked against real Node (ADR-00532).
func TestE2EExponentNumberLiterals(t *testing.T) {
	assertOutput(t, `
console.log(1e3, 1.5e3, 1E3, 2e-2, 6.022e23);
console.log(1e21, 1_000e1, 5e2 + 1);
const x: number = 5e2;
console.log(x * 2);
`, "1000 1500 1000 0.02 6.022e+23\n1e+21 10000 501\n1000")
}

// TestE2ENumberIsIntegerNonFinite confirms Number.isInteger is false for any
// non-finite value (±Infinity, NaN), matching real JS — floor(Infinity) equals
// Infinity, so the whole-number test alone wrongly answered true (ADR-00531).
func TestE2ENumberIsIntegerNonFinite(t *testing.T) {
	assertOutput(t, `
console.log(Number.isInteger(Infinity), Number.isInteger(-Infinity), Number.isInteger(NaN));
console.log(Number.isInteger(5), Number.isInteger(5.2), Number.isInteger(0), Number.isInteger(-3));
`, "false false false\ntrue false true true")
}

// TestE2EParseIntHexAutoDetect confirms parseInt with no radix argument
// auto-detects base 16 for a "0x"/"0X" prefix (past whitespace and an optional
// sign) and base 10 otherwise — no octal auto-detect ("077" is 77) — and that
// a "0x" with no trailing hex digit is NaN, all matching real Node (ADR-00530).
func TestE2EParseIntHexAutoDetect(t *testing.T) {
	assertOutput(t, `
console.log(parseInt("0xFF"), parseInt("0x1F"), parseInt("-0x10"), parseInt("  0X1A"), parseInt("+0xA"));
console.log(parseInt("077"), parseInt("08"), parseInt("10"), parseInt("5x"), parseInt("0x0"));
console.log(parseInt("0x"), parseInt("0X"), parseInt("0xG"), parseInt("0xff", 16), parseInt("12", 8));
`, "255 31 -16 26 10\n77 8 10 5 0\nNaN NaN NaN 255 10")
}

// TestE2ENumberParseFloatInfinitySpelling confirms JS's rule that the only
// accepted string infinity spelling is the exact word "Infinity" (optionally
// signed) — C strtod's extra spellings ("inf"/"infinity"/case variants) are
// NaN, while "Infinity" itself and a numeric overflow like "1e999" are
// Infinity (ADR-00529). Number() requires the whole string to be numeric, so
// "Infinityx" is NaN; parseFloat() takes the numeric prefix, so "Infinityx"
// is Infinity — both cross-checked against real Node.
func TestE2ENumberParseFloatInfinitySpelling(t *testing.T) {
	assertOutput(t, `
console.log(Number("inf"), Number("Infinity"), Number("-Infinity"), Number("+Infinity"));
console.log(Number("infinity"), Number("INFINITY"), Number("1e999"), Number("  Infinity  "));
console.log(Number("Infinityx"));
console.log(parseFloat("Infinity"), parseFloat("inf"), parseFloat("infinityXYZ"));
console.log(parseFloat("Infinityx"), parseFloat("-Infinity"));
`, "NaN Infinity -Infinity Infinity\nNaN NaN Infinity Infinity\nNaN\nInfinity NaN NaN\nInfinity -Infinity")
}

// parseFloat does NOT accept a "0x"/"0X" hex prefix (unlike Number/ToNumber):
// it reads the leading "0" and stops at the "x", so parseFloat("0x10") is 0,
// while Number("0x10") stays 16 (ADR-00545).
func TestE2EParseFloatHexPrefix(t *testing.T) {
	assertOutput(t, `
console.log(parseFloat("0x10"), parseFloat("0X1F"), parseFloat("0.5x"));
console.log(parseFloat("-0x10"), Number("0x10"), Number.parseFloat("0xFF"));
`, "0 0 0.5\n-0 16 0")
}

// Multi-argument console.log joins with single spaces on one line, a no-arg
// call prints a bare newline, and -0 displays as "-0" (ADR-00285).
func TestE2EConsoleLogSpacingAndNegZero(t *testing.T) {
	assertOutput(t, `
console.log(1, "two", true, [3, 4]);
console.log();
console.log(-0.0);
console.log("a", 1.5, "b");
`, "1 two true [ 3, 4 ]\n\n-0\na 1.5 b")
}

// Built-in error constructors as first-class values (ADR-00289): boxed
// funcrefs — identity-comparable, typeof "function", passable to any params.
func TestE2EErrorConstructorAsValue(t *testing.T) {
	assertOutput(t, `
function take(x: any): void {
  console.log(typeof x);
  console.log(x === TypeError, x === RangeError);
}
take(TypeError);
console.log(TypeError === TypeError);
`, "function\ntrue false\ntrue")
}

// String(x)/Number(x)/Boolean(x) conversion calls (ADR-00291).
func TestE2EGlobalConversionFunctions(t *testing.T) {
	assertOutput(t, `
console.log(String(42), String(3.5), String(true), String("x"));
console.log(Number("3.5"), Number(""), Number("12px"), Number("0x10"), Number(true), Number(null));
console.log(Boolean(1), Boolean(0), Boolean(""), Boolean("x"), Boolean(NaN));
console.log(Number.isNaN(Number("abc")));
const pn = parseInt("abc");
console.log(pn, Number.isNaN(pn));
`, "42 3.5 true x\n3.5 0 NaN 16 1 0\ntrue false false true false\ntrue\nNaN true")
}

// Mixed int/float arithmetic promotes to double (ADR-00292) — the old
// left-biased unification computed `i * 1.5` as `i * 1`.
func TestE2EMixedNumericPromotion(t *testing.T) {
	assertOutput(t, `
let i = 3;
console.log(i * 1.5, i + 0.5, i - 0.25, i / 2.0, i % 2.5);
console.log(1.5 * i, 0.5 + i);
console.log(3 === 3.5, 3 < 3.5, 4.5 > i);
const x = i * 1.5;
console.log(x, typeof x);
`, "4.5 3.5 2.75 1.5 0.5\n4.5 3.5\nfalse true true\n4.5 number")
}

// Near-miss alignment batch (ADR-00295): Unicode whitespace in trims, the JS
// pow deviation, and oversized integer literals becoming doubles.
func TestE2ENearMissAlignment(t *testing.T) {
	assertOutput(t, `
console.log("[" + "  \u3000abc \uFEFF ".trim() + "]");
console.log("[" + "  x".trimStart() + "]");
console.log("[" + "x  ".trimEnd() + "]");
console.log("  ".trim().length);
const e2: float64 = Infinity;
console.log(1 ** e2, (-1) ** -e2, Math.pow(1, e2), 2 ** 0.5);
console.log(92233720368620160000, typeof 92233720368620160000);
console.log(92233720368620160000 === 92233720368620160000);
`, "[abc]\n[x]\n[x]\n0\nNaN NaN NaN 1.4142135623730951\n92233720368620160000 number\ntrue")
}

// --- ADR-00376: ++/-- on a member or index target (previously identifier-only).
// Desugars to the equivalent compound assignment; postfix returns the old value. ---

func TestE2EUpdateInstanceField(t *testing.T) {
	assertOutput(t, `
class C { x = 0; bump(): void { this.x++; } }
const c = new C();
c.bump();
c.bump();
console.log(c.x);
`, "2")
}

func TestE2EUpdateStaticField(t *testing.T) {
	assertOutput(t, `
class C { static n = 0; static inc(): void { C.n++; } }
C.inc();
C.inc();
C.inc();
console.log(C.n);
`, "3")
}

func TestE2EUpdateObjectField(t *testing.T) {
	assertOutput(t, `
const o = { k: 5 };
o.k++;
o.k--;
o.k--;
console.log(o.k);
`, "4")
}

// ADR-00606: a consumed postfix update on a side-effecting member receiver
// evaluates that receiver exactly once (hoisted into a temp).
func TestE2EPostfixSideEffectingReceiverEvaluatedOnce(t *testing.T) {
	assertOutput(t, `
let calls = 0;
class Box { v: number = 0; }
const b = new Box();
function makeBox(): Box { calls++; return b; }
const before = makeBox().v++;
console.log(before);
console.log(b.v);
console.log(calls);
`, "0\n1\n1")
}

// ADR-00606: a consumed postfix update hoists a side-effecting index expression
// so it runs exactly once.
func TestE2EPostfixSideEffectingIndexEvaluatedOnce(t *testing.T) {
	assertOutput(t, `
let calls = 0;
const a = [10, 20, 30];
function idx(): number { calls++; return 1; }
const before = a[idx()]++;
console.log(before);
console.log(a[1]);
console.log(calls);
`, "20\n21\n1")
}

// ADR-00606: a consumed postfix update on a side-effecting array-producing
// object evaluates that object exactly once.
func TestE2EPostfixSideEffectingArrayObjectEvaluatedOnce(t *testing.T) {
	assertOutput(t, `
let calls = 0;
const a = [10, 20, 30];
function makeArr(): number[] { calls++; return a; }
const before = makeArr()[1]++;
console.log(before);
console.log(a[1]);
console.log(calls);
`, "20\n21\n1")
}

func TestE2EUpdateIndexTarget(t *testing.T) {
	assertOutput(t, `
const a = [10, 20];
a[0]++;
console.log(a[0]);
console.log(a[1]);
`, "11\n20")
}

func TestE2EUpdatePostfixReturnsOldValue(t *testing.T) {
	assertOutput(t, `
const o = { k: 5 };
const r = o.k++;
console.log(r);
console.log(o.k);
`, "5\n6")
}

func TestE2EUpdatePrefixReturnsNewValue(t *testing.T) {
	assertOutput(t, `
const o = { k: 5 };
const r = ++o.k;
console.log(r);
console.log(o.k);
`, "6\n6")
}

func TestE2EUpdateIndexPostfixSideEffect(t *testing.T) {
	assertOutput(t, `
const a = [0, 0, 0];
let i = 0;
a[i++] = 9;
console.log(a[0]);
console.log(i);
`, "9\n1")
}

func TestE2EUpdateFloatField(t *testing.T) {
	assertOutput(t, `
class C { /** @type {float64} */ x = 1.5; }
const c = new C();
c.x++;
console.log(c.x);
`, "2.5")
}

func TestE2EUpdateBigIntField(t *testing.T) {
	assertOutput(t, `
class C { count: bigint = 10n; }
const c = new C();
c.count++;
c.count++;
console.log(c.count);
`, "12n")
}

// --- TDD-00123 Stage 1: `number` is an IEEE-754 double (JS-faithful) ---

func TestE2ENumberIsDouble(t *testing.T) {
	assertOutput(t, `
console.log(0.1 + 0.2)
console.log(10 / 3)
console.log(5 / 2)
console.log(2 ** -1)
console.log(7 % 3)
const x: number = 3.75
console.log(x * 2)
`, "0.30000000000000004\n3.3333333333333335\n2.5\n0.5\n1\n7.5")
}

func TestE2ENumberIntegerEscapeHatch(t *testing.T) {
	// The explicit integer types keep integer semantics; a bare literal is a
	// `number` (double), so mixing promotes to float.
	assertOutput(t, `
let a: int32 = 7
let b: int32 = 2
console.log(a / b)
console.log(a / 2)
`, "3\n3.5")
}

// TestE2EIntegerEscapeHatchExactLiterals: an int64/uint64 binding initialized
// from a literal above 2^53 stays exact — the literal is parsed straight to a
// 64-bit integer rather than rounding through the default float64 literal model
// (TDD-00123). A plain `number` at the same magnitude still rounds, matching JS.
func TestE2EIntegerEscapeHatchExactLiterals(t *testing.T) {
	assertOutput(t, `
let a: int64 = 9007199254740993
console.log(a)
let b: number = 9007199254740993
console.log(b)
let u: uint64 = 18446744073709551615
console.log(u)
function f(x: int64): int64 { return x }
console.log(f(9007199254740993))
`, "9007199254740993\n9007199254740992\n18446744073709551615\n9007199254740993")
}

// TestE2EJSDocParamReturnTyping: an untyped function is typed from its
// `@param`/`@returns` JSDoc — the "typed JS" workflow (TDD-00125). An `int32`
// @param gives integer division; a `string` @param enables `.length`; an
// inline annotation still wins over a conflicting @param.
func TestE2EJSDocParamReturnTyping(t *testing.T) {
	assertOutput(t, `
/**
 * @param {int32} x
 * @param {int32} y
 * @returns {int32}
 */
function divi(x, y) { return x / y }
console.log(divi(7, 2))

/** @param {string} s */
function len(s) { return s.length }
console.log(len("hello"))

/** @param {int32} x */
function half(x: number) { return x / 2 }
console.log(half(7))
`, "3\n5\n3.5")
}

// TestE2EJSDocTypedefCallback: `@typedef {Object}`+`@property` declares a
// named object type and `@callback` a named function type, both usable wherever
// a type name is — via `@param`/`@type` (TDD-00125 Stage 2).
func TestE2EJSDocTypedefCallback(t *testing.T) {
	assertOutput(t, `
/**
 * @typedef {Object} Point
 * @property {number} x
 * @property {number} y
 */
/** @param {Point} p */
function sum(p) { return p.x + p.y }
console.log(sum({x: 3, y: 4}))

/**
 * @callback Combine
 * @param {number} a
 * @param {number} b
 * @returns {number}
 */
/** @param {Combine} fn */
function run(fn) { return fn(10, 5) }
console.log(run((a, b) => a * b))
`, "7\n50")
}

// TestE2EJSDocTemplateGeneric: `@template T` declares a generic function — the
// JSDoc form of `<T>` — monomorphized per concrete type like a TS generic
// (TDD-00125 Stage 3).
func TestE2EJSDocTemplateGeneric(t *testing.T) {
	assertOutput(t, `
/**
 * @template T
 * @param {T} x
 * @returns {T}
 */
function identity(x) { return x }
console.log(identity(42))
console.log(identity("hi"))

/**
 * @template T
 * @param {T[]} arr
 * @returns {T}
 */
function head(arr) { return arr[0] }
console.log(head([10, 20, 30]))
`, "42\nhi\n10")
}

// TestE2EJSDocTypeExpressions: JSDoc type strings are parsed by the real type
// parser (TDD-00125 Stage 4) — union, nullable `?T`, non-null `!T`, `*`,
// inline object shapes, `Array.<T>`, and `function(...)` types all resolve.
func TestE2EJSDocTypeExpressions(t *testing.T) {
	assertOutput(t, `
/** @param {number | string} v */
function kind(v) { return typeof v }
console.log(kind(3))
console.log(kind("x"))

/** @param {{x: number, y: number}} p */
function sum(p) { return p.x + p.y }
console.log(sum({x: 3, y: 4}))

/** @param {Array.<number>} a */
function firstOf(a) { return a[0] }
console.log(firstOf([9, 8]))

/** @param {function(number, number): number} fn */
function run(fn) { return fn(6, 7) }
console.log(run((a, b) => a * b))

/** @type {Object.<string, number>} */
const scores = { alice: 90 }
scores["bob"] = 85
console.log(scores["alice"] + scores["bob"])
`, "number\nstring\n7\n9\n42\n175")
}

// TestE2EJSDocClassAndDocTags: the class/visibility tags (`@implements`,
// `@private`, `@readonly`, `@override`) and the documentation-only tags
// (`@deprecated`, `@see`) are accepted and erased — matching how TypeScript
// erases them at runtime and this compiler erases the equivalent modifiers —
// and never interfere with the typing tags on the same declaration
// (TDD-00125 Stage 5).
func TestE2EJSDocClassAndDocTags(t *testing.T) {
	assertOutput(t, `
interface Named { name: string }
/** @implements {Named} */
class Person implements Named {
  /** @readonly */
  name: string = "Thessaloniki"
  /** @private @type {int32} */
  age: number = 40
  /** @returns {int32} */
  getAge() { return this.age }
}
const p = new Person()
console.log(p.name)
console.log(p.getAge())

/**
 * @deprecated use half2 instead
 * @see https://example.com
 * @param {int32} x
 */
function half(x) { return x }
console.log(half(9))
`, "Thessaloniki\n40\n9")
}

func TestE2ENumberLiteralForms(t *testing.T) {
	assertOutput(t, `
console.log(0xff)
console.log(0b101)
console.log(0o17)
console.log(100)
console.log(3.14)
`, "255\n5\n15\n100\n3.14")
}
