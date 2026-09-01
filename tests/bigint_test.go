package tests

import (
	"strings"
	"testing"
)

// mustCompileError asserts that source is a clean compile-time rejection whose
// message contains want.
func mustCompileError(t *testing.T, src, want string) {
	t.Helper()
	_, err := parseAndCompile(src)
	if err == nil {
		t.Fatalf("expected a compile error containing %q, got none", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected compile error containing %q, got: %v", want, err)
	}
}

// --- bigint V1 (TDD-00074): 123n literals, arbitrary-precision arithmetic, the
// full operator set, typeof, the console-vs-String print split, BigInt(), and
// the deliberate non-interoperability with number. Runs against the default
// backend (libtommath); the identical program on -bigint=gmp is verified
// manually per ADR-00216. Each test Skips (not Fails) if the backend library
// isn't installed — see appendBigIntBackend. ---

func TestE2EBigIntArithmetic(t *testing.T) {
	assertOutput(t, `
console.log(10n + 20n)
console.log(20n - 7n)
console.log(6n * 7n)
console.log(17n / 5n)
console.log(17n % 5n)
console.log(-7n / 2n)
console.log(-7n % 2n)
`, "30n\n13n\n42n\n3n\n2n\n-3n\n-1n")
}

func TestE2EBigIntArbitraryPrecision(t *testing.T) {
	// Values far beyond i64 must be exact — the whole point of bigint.
	assertOutput(t, `
console.log(2n ** 100n)
console.log(123456789012345678901234567890n + 1n)
console.log(9999999999999999999n * 9999999999999999999n)
`, "1267650600228229401496703205376n\n123456789012345678901234567891n\n99999999999999999980000000000000000001n")
}

func TestE2EBigIntTypeof(t *testing.T) {
	assertOutput(t, `
const a = 10n
console.log(typeof a)
console.log(typeof 5n)
`, "bigint\nbigint")
}

func TestE2EBigIntComparisons(t *testing.T) {
	assertOutput(t, `
console.log(10n < 20n)
console.log(20n <= 20n)
console.log(30n > 20n)
console.log(10n === 10n)
console.log(10n !== 11n)
`, "true\ntrue\ntrue\ntrue\ntrue")
}

func TestE2EBigIntBitwiseAndShift(t *testing.T) {
	assertOutput(t, `
console.log(12n & 10n)
console.log(12n | 10n)
console.log(12n ^ 10n)
console.log(~5n)
console.log(1n << 64n)
console.log(-20n >> 2n)
`, "8n\n14n\n6n\n-6n\n18446744073709551616n\n-5n")
}

func TestE2EBigIntUnaryAndTruthiness(t *testing.T) {
	assertOutput(t, `
console.log(-10n)
console.log(!0n)
console.log(!5n)
if (0n) { console.log("bad") } else { console.log("0n-falsy") }
console.log(7n > 2n ? "yes" : "no")
`, "-10n\ntrue\nfalse\n0n-falsy\nyes")
}

func TestE2EBigIntPrintSplit(t *testing.T) {
	// console.log shows the trailing `n`; String()/template interpolation do not.
	assertOutput(t, `
const a = 42n
console.log(a)
console.log(`+"`val=${a}`"+`)
`, "42n\nval=42")
}

func TestE2EBigIntConstructorAndBases(t *testing.T) {
	assertOutput(t, `
console.log(BigInt(42) + 8n)
console.log(BigInt("1000000000000000000000") + 1n)
console.log(0xffn)
console.log(0b1010n)
console.log(0o17n)
console.log(1_000_000n)
`, "50n\n1000000000000000000001n\n255n\n10n\n15n\n1000000n")
}

func TestE2EBigIntFunctionsArraysFields(t *testing.T) {
	assertOutput(t, `
function dbl(x: bigint): bigint { return x * 2n }
console.log(dbl(21n))
const arr: bigint[] = [1n, 2n, 3n]
console.log(arr[0] + arr[2])
let acc = 0n
acc += 100n
console.log(acc)
let i = 5n
i++
console.log(i)
`, "42n\n4n\n100n\n6n")
}

func TestE2EBigIntDivByZeroThrows(t *testing.T) {
	// Division/modulo by zero is a catchable Error, not a process abort.
	assertOutput(t, `
const z = 0n
try { console.log(10n / z) } catch (e) { console.log("div: " + e.message) }
try { console.log(10n % z) } catch (e) { console.log("mod: " + e.message) }
console.log("survived")
`, "div: Division by zero\nmod: Division by zero\nsurvived")
}

func TestE2EBigIntToStringRadix(t *testing.T) {
	assertOutput(t, `
console.log((255n).toString())
console.log((255n).toString(16))
console.log((10n).toString(2))
console.log((123456789012345678901234567890n).toString())
const r = 8
console.log((64n).toString(r))
`, "255\nff\n1010\n123456789012345678901234567890\n100")
}

func TestE2EBigIntCrossTypeComparison(t *testing.T) {
	// bigint vs an integer number compares exactly, both operand orders; ===/!==
	// across types stay type-distinct (a bigint is never === a number).
	assertOutput(t, `
console.log(10n < 5)
console.log(10n > 5)
console.log(10n == 10)
console.log(10n != 11)
console.log(5 < 10n)
console.log(10n === 10)
console.log(10n !== 10)
const x = 100
console.log(50n < x)
`, "false\ntrue\ntrue\ntrue\ntrue\nfalse\ntrue\ntrue")
}

func TestE2EBigIntStringConcat(t *testing.T) {
	// A bigint stringifies (bare digits) when concatenated with a string — not
	// bigint arithmetic, matching JS.
	assertOutput(t, `
console.log("x" + 10n)
console.log(10n + "y")
`, "x10\n10y")
}

func TestE2EBigIntExpressionInference(t *testing.T) {
	// A bigint arithmetic expression assigned to a const infers as bigint (not
	// i64/string), so it prints/`typeof`s correctly.
	assertOutput(t, `
const x = 2n ** 8n + 1n
console.log(x)
console.log(typeof x)
const y = (3n * 4n) - 2n
console.log(y)
`, "257n\nbigint\n10n")
}

func TestE2EBigIntFloatComparisonCompatJS(t *testing.T) {
	// Under -compat=js, bigint↔float comparison is exact (no rounding, even past
	// 2^53). big = 2^53+1 is > 2^53 and != 2^53, which an inexact conversion
	// would get wrong.
	assertOutputCompatJS(t, `
console.log(10n < 5.5)
console.log(10n > 5.5)
console.log(10n == 10.0)
console.log(10n == 10.5)
console.log(5.5 < 10n)
const big = 2n ** 53n + 1n
console.log(big == 9007199254740992.0)
console.log(big > 9007199254740992.0)
`, "false\ntrue\ntrue\nfalse\ntrue\nfalse\ntrue")
}

// --- Deliberate rejections (compile errors) ---

func TestE2EBigIntNumberComparisonIsExact(t *testing.T) {
	// `number` is a double (TDD-00123); JS permits bigint↔number comparison with
	// exact real-number semantics, including a fractional operand (10n > 5.5).
	assertOutput(t, `
console.log(10n < 5.5)
console.log(10n > 5.5)
console.log(10n == 10)
console.log(10n < 10.5)
`, "false\ntrue\ntrue\ntrue")
}

func TestE2EBigIntMixingIsError(t *testing.T) {
	mustCompileError(t, `const a = 5n
console.log(a + 1)`, "mix BigInt")
}

func TestE2EBigIntUnsignedShiftIsError(t *testing.T) {
	mustCompileError(t, `console.log(8n >>> 1n)`, ">>>")
}

func TestE2EBigIntJSONIsError(t *testing.T) {
	mustCompileError(t, `console.log(JSON.stringify(5n))`, "BigInt")
}

// ADR-00586: Number(bigint) converts to the nearest double.
func TestE2ENumberOfBigInt(t *testing.T) {
	assertOutput(t, `
console.log(Number(10n))
console.log(Number(-42n))
console.log(Number(9007199254740993n))
console.log(Number(123456789012345678901234567890n))
console.log(Number(0n))
`, "10\n-42\n9007199254740992\n1.2345678901234568e+29\n0")
}

// ADR-00584: BigInt.asIntN / asUintN clamp to the low `bits` bits.
func TestE2EBigIntAsIntN(t *testing.T) {
	assertOutput(t, `
console.log(BigInt.asIntN(8, 256n))
console.log(BigInt.asIntN(8, 255n))
console.log(BigInt.asIntN(8, 128n))
console.log(BigInt.asUintN(8, 255n))
console.log(BigInt.asUintN(8, -1n))
console.log(BigInt.asIntN(64, 12345678901234567890n))
console.log(BigInt.asUintN(4, 31n) + 1n)
`, "0n\n-1n\n-128n\n255n\n255n\n-6101065172474983726n\n16n")
}
