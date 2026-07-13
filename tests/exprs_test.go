package tests

import (
	"testing"
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
`, "13\n7\n30\n3\n1")
}

func TestE2EBoolean(t *testing.T) {
	assertOutput(t, `
console.log(1 < 2)
console.log(2 > 3)
console.log(1 === 1)
console.log(1 !== 2)
`, "1\n0\n1\n1")
}

func TestE2ETernary(t *testing.T) {
	assertOutput(t, `
const x: number = 5
const abs: number = x < 0 ? -x : x
console.log(abs)
`, "5")
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

// --- Optional chaining ?. ---

func TestE2EOptionalChainingLength(t *testing.T) {
	assertOutput(t, `
const s: string = 'hello'
const n: number = s?.length ?? 0
console.log(n)
`, "5")
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

func TestE2ETypeofFunction(t *testing.T) {
	assertOutput(t, `
function add(a: number, b: number): number { return a + b }
console.log(typeof add)
`, "function")
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
`, "1\n0\n0\n1")
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
`, "1\n1\n0")
}

func TestE2ENullOptionalChain(t *testing.T) {
	assertOutput(t, `
const s: string | null = null
console.log(s?.length)
const t2: string | null = "hello"
console.log(t2?.length)
`, "0\n5")
}
