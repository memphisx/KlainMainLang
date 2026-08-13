package tests

import (
	"testing"
)

// --- Strings ---

func TestE2EStringConcat(t *testing.T) {
	assertOutput(t, `
const a: string = 'hello'
const b: string = 'world'
console.log(a + ', ' + b + '!')
`, "hello, world!")
}

func TestE2EStringPlusNumberConcat(t *testing.T) {
	// Regression test: "+" with exactly one string operand must stringify
	// the other side (matching real JS), not blindly reinterpret it as
	// already being a string pointer — found broken (crashed at the clang
	// verification step) while writing a Timers example that printed an
	// interval tick count.
	assertOutput(t, `
let count: number = 3
console.log("tick " + count)
console.log(count + " tick")
`, "tick 3\n3 tick")
}

// Regression test: concatenating a null string used to segfault
// (emitStringConcat called strlen() on the raw null pointer unconditionally)
// instead of stringifying it as "null", matching real JS's `"x" + null ===
// "xnull"` and this compiler's own console.log(null) behavior.
func TestE2EStringConcatWithNull(t *testing.T) {
	assertOutput(t, `
let s: string | null = null
console.log('Hi, ' + s)
console.log(s + ', world')
`, "Hi, null\nnull, world")
}

func TestE2EStringPlusBooleanConcat(t *testing.T) {
	assertOutput(t, `
let flag: boolean = true
console.log("flag is " + flag)
console.log(flag + " is the flag")
`, "flag is true\ntrue is the flag")
}

func TestE2EStringMethods(t *testing.T) {
	assertOutput(t, `
const s: string = 'Hello, World!'
console.log(s.length)
console.log(s.toUpperCase())
console.log(s.toLowerCase())
console.log(s.includes('World'))
console.log(s.startsWith('Hello'))
console.log(s.indexOf('World'))
`, "13\nHELLO, WORLD!\nhello, world!\ntrue\ntrue\n7")
}

func TestE2EStringSlice(t *testing.T) {
	assertOutput(t, `
const s: string = 'hello'
console.log(s.slice(1, 3))
console.log(s.slice(-2))
console.log(s.substring(1, 3))
`, "el\nlo\nel")
}

func TestE2EStringReplaceAll(t *testing.T) {
	assertOutput(t, `
console.log("aaa".replaceAll("a", "bb"))
console.log("hello world hello".replaceAll("hello", "hi"))
console.log("no match here".replaceAll("xyz", "abc"))
console.log("aaa".replaceAll("a", "a"))
console.log("banana".replaceAll("ana", "ANA"))
`, "bbbbbb\nhi world hi\nno match here\naaa\nbANAna")
}

func TestE2ETemplateLiteral(t *testing.T) {
	assertOutput(t, `
const x: number = 42
const msg: string = `+"`"+`value is ${x}`+"`"+`
console.log(msg)
`, "value is 42")
}

func TestE2EStringPlusAssign(t *testing.T) {
	// A pre-existing bug (found while building TDD-00059's own tagged-
	// template example/tests, which accumulate a result via `+=`): `+=`
	// with a string-typed left-hand side reached emitArith's generic
	// numeric-`add` fallback unconditionally, a hard clang-stage "invalid
	// operand type" for even the plainest `s += "b"` — not just a mixed
	// string/number case (that one already worked via `s = s + "b"`, since
	// plain `+`/emitBinary had its own string handling all along).
	assertOutput(t, `
let result: string = "a"
result += "b"
result += "c"
console.log(result)
`, "abc")
}

func TestE2EStringPlusAssignWithNumber(t *testing.T) {
	assertOutput(t, `
let result: string = "n="
result += 5
console.log(result)
`, "n=5")
}

// --- Tagged template literals (TDD-00059) ---

func TestE2ETaggedTemplateBasic(t *testing.T) {
	assertOutput(t, `
function tag(strings: string[], ...values: number[]): string {
    let result = strings[0];
    for (let i = 0; i < values.length; i++) {
        result += values[i];
        result += strings[i + 1];
    }
    return result;
}
console.log(tag`+"`"+`a${1}b${2}c`+"`"+`)
`, "a1b2c")
}

func TestE2ETaggedTemplateNoSubstitution(t *testing.T) {
	assertOutput(t, `
function tag(strings: string[]): string {
    return strings[0];
}
console.log(tag`+"`"+`hello world`+"`"+`)
`, "hello world")
}

func TestE2ETaggedTemplateValuesAreRealTypedValues(t *testing.T) {
	// Interpolated values reach the tag function untouched (real numbers,
	// not stringified) — unlike a plain, un-tagged template literal's own
	// interpolation.
	assertOutput(t, `
function sumTag(strings: string[], ...values: number[]): number {
    let s = 0;
    for (const v of values) { s += v; }
    return s;
}
console.log(sumTag`+"`"+`x${10}y${20}z${12}`+"`"+`)
`, "42")
}

func TestE2ETaggedTemplateArrowFunctionTag(t *testing.T) {
	// An arrow function's own first parameter (the strings array) is
	// unavoidably array-typed — this only became usable as a tag once
	// array-typed closure parameters were fixed (ADR-00151/TDD-00059,
	// closing the ADR-00105 gap this file originally just documented
	// around).
	assertOutput(t, `
const tag = (strings: string[], a: number, b: number): number => a + b + strings.length;
console.log(tag`+"`"+`x${1}y${2}z`+"`"+`)
`, "6")
}

func TestE2ETaggedTemplateArrowFunctionTagWithRest(t *testing.T) {
	assertOutput(t, `
const tag = (strings: string[], ...values: number[]): number => {
    let s = 0;
    for (const v of values) { s += v; }
    return s;
};
console.log(tag`+"`"+`x${10}y${20}z${12}`+"`"+`)
`, "42")
}

func TestE2ETaggedTemplateClosureCapture(t *testing.T) {
	assertOutput(t, `
function tag(strings: string[], ...values: number[]): number {
    let s = 0;
    for (const v of values) { s += v; }
    return s;
}
function make(): () => number {
    let base = 100;
    return (): number => tag`+"`"+`x${base}y`+"`"+`;
}
const closure = make();
console.log(closure())
`, "100")
}

func TestE2ETaggedTemplateUnannotatedConst(t *testing.T) {
	assertOutput(t, `
function tag(strings: string[], ...values: number[]): string {
    return strings[0] + values[0];
}
const r = tag`+"`"+`v=${99}`+"`"+`;
console.log(r)
`, "v=99")
}

func TestE2ETaggedTemplateClassMethodTag(t *testing.T) {
	// A class method usable as a tag when it takes only the strings array
	// (no additional params) — see TDD-00059's own notes for a separate,
	// pre-existing class-method-call bug found (not fixed) when an array-
	// literal argument is combined with further trailing arguments.
	assertOutput(t, `
class Fmt {
    build(strings: string[]): string {
        return strings[0];
    }
}
const f = new Fmt();
console.log(f.build`+"`"+`hi`+"`"+`)
`, "hi")
}

func TestE2ETaggedTemplateRawPropertyRejected(t *testing.T) {
	// Deliberate V1 scope cut (TDD-00059): no `.raw` property on the
	// strings array.
	_, err := parseAndCompile("function tag(strings: string[]): string { return strings.raw[0]; } console.log(tag`hi`)")
	if err == nil {
		t.Fatal("expected a compile error for 'strings.raw' on a tagged template's strings array, got none")
	}
}

// --- str.repeat ---

func TestE2EStringRepeat(t *testing.T) {
	assertOutput(t, `
console.log('ab'.repeat(3))
console.log('x'.repeat(0))
`, "ababab\n")
}

// --- str.at ---

func TestE2EStringAt(t *testing.T) {
	assertOutput(t, `
const s: string = 'hello'
console.log(s.at(0))
console.log(s.at(-1))
console.log(s.at(1))
`, "h\no\ne")
}

func TestE2EStringCharAt(t *testing.T) {
	assertOutput(t, `
const s: string = 'hello'
console.log(s.charAt(0))
console.log(s.charAt(4))
console.log("[" + s.charAt(10) + "]")
console.log("[" + s.charAt(-1) + "]")
`, "h\no\n[]\n[]")
}

func TestE2EStringCharAtWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`"a".charAt()`)
	if err == nil {
		t.Fatal("expected a compile error for .charAt() with no arguments, got none")
	}
}

func TestE2EStringCodePointAt(t *testing.T) {
	assertOutput(t, `
const s: string = 'hello'
console.log(s.codePointAt(0))
console.log(s.codePointAt(0) === s.charCodeAt(0))
`, "104\ntrue")
}

func TestE2EStringSearch(t *testing.T) {
	assertOutput(t, `
const s: string = 'hello world'
console.log(s.search('world'))
console.log(s.search('xyz'))
console.log(s.search('world') === s.indexOf('world'))
`, "6\n-1\ntrue")
}

func TestE2EStringLocaleCompare(t *testing.T) {
	assertOutput(t, `
console.log('apple'.localeCompare('banana'))
console.log('banana'.localeCompare('apple'))
console.log('apple'.localeCompare('apple'))
`, "-1\n1\n0")
}

func TestE2EStringLocaleCompareWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`"a".localeCompare()`)
	if err == nil {
		t.Fatal("expected a compile error for .localeCompare() with no arguments, got none")
	}
}

// --- str.padStart / str.padEnd ---

func TestE2EStringPadStart(t *testing.T) {
	assertOutput(t, `
console.log('5'.padStart(3, '0'))
console.log('hello'.padStart(3))
console.log('hi'.padStart(5, 'ab'))
`, "005\nhello\nabahi")
}

func TestE2EStringPadEnd(t *testing.T) {
	assertOutput(t, `
console.log('5'.padEnd(4, '0'))
console.log('hi'.padEnd(6, '!-'))
`, "5000\nhi!-!-")
}

func TestE2EStringTrimStartEnd(t *testing.T) {
	assertOutput(t, `
console.log("[" + "  hello  ".trimStart() + "]")
console.log("[" + "  hello  ".trimEnd() + "]")
console.log("[" + "hello".trimStart() + "]")
console.log("[" + "   ".trimStart() + "]")
console.log("[" + "   ".trimEnd() + "]")
console.log("[" + "".trimStart() + "]")
console.log("[" + "".trimEnd() + "]")
`, "[hello  ]\n[  hello]\n[hello]\n[]\n[]\n[]\n[]")
}

func TestE2EStringPadEmptyFill(t *testing.T) {
	assertOutput(t, `
console.log('ab'.padStart(5, ''))
console.log('ab'.padEnd(5, ''))
`, "ab\nab")
}

func TestE2EStringSplitEmptySeparator(t *testing.T) {
	assertOutput(t, `
const chars: string[] = "abc".split("")
console.log(chars.length)
console.log(chars[0])
console.log(chars[2])
const empty: string[] = "".split("")
console.log(empty.length)
`, "3\na\nc\n0")
}

// --- String-literal escape sequences (ADR-00194) ---

func TestE2EStringHexEscape(t *testing.T) {
	assertOutput(t, `
console.log("\x41\x42\x5A")
`, "ABZ")
}

func TestE2EStringUnicodeEscape(t *testing.T) {
	assertOutput(t, `
console.log("AB")
console.log("é")
`, "AB\né")
}

func TestE2EStringUnicodeCodePointEscape(t *testing.T) {
	assertOutput(t, `
console.log("\u{48}\u{49}")
`, "HI")
}

func TestE2EStringNonEscapeAndNonOctalDecimal(t *testing.T) {
	// \A is a NonEscapeCharacter (→ "A"); \8 / \9 are NonOctalDecimalEscape (→ the digit).
	assertOutput(t, `
console.log("\A\B\%\8\9")
`, "AB%89")
}

func TestE2EStringLegacyOctalEscape(t *testing.T) {
	assertOutput(t, `
console.log("\1\2\7" === "\x01\x02\x07")
console.log("\08" === "\x008")
`, "true\ntrue")
}

func TestE2EStringLineContinuation(t *testing.T) {
	assertOutput(t, "console.log(\"hello \\\nworld\")", "hello world")
}

func TestE2ETemplateHexUnicodeEscape(t *testing.T) {
	assertOutput(t, "console.log(`x=\\x41 u=\\u0042 d=\\$`)", "x=A u=B d=$")
}

func TestE2EStringFromCharCodeNonNumberRejected(t *testing.T) {
	// A string argument must be a clean compile error, not invalid IR
	// (ADR-00196) — this compiler doesn't do implicit string→number coercion.
	_, err := parseAndCompile(`console.log(String.fromCharCode("0"))`)
	if err == nil {
		t.Fatal("expected a compile error for String.fromCharCode with a string argument, got none")
	}
}
