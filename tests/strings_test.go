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
`, "13\nHELLO, WORLD!\nhello, world!\n1\n1\n7")
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
`, "104\n1")
}

func TestE2EStringSearch(t *testing.T) {
	assertOutput(t, `
const s: string = 'hello world'
console.log(s.search('world'))
console.log(s.search('xyz'))
console.log(s.search('world') === s.indexOf('world'))
`, "6\n-1\n1")
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
