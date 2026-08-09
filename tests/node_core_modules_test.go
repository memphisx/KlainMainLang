package tests

import (
	"testing"
)

// --- querystring (see docs/adr/ADR-00139.md) ---

func TestE2EQuerystringParse(t *testing.T) {
	assertOutput(t, `
const m = querystring.parse("a=1&b=hello%20world")
console.log(m.get("a"))
console.log(m.get("b"))
`, "1\nhello world")
}

func TestE2EQuerystringParseBareFlagIsEmptyString(t *testing.T) {
	assertOutput(t, `
const m = querystring.parse("debug&x=1")
console.log(m.get("debug"))
console.log(m.get("x"))
`, "\n1")
}

func TestE2EQuerystringParseDoesNotStripLeadingQuestionMark(t *testing.T) {
	// Unlike `new URLSearchParams(str)`, querystring.parse treats a leading
	// '?' as plain text at the start of the first key — matching real Node.
	assertOutput(t, `
const m = querystring.parse("?a=1")
console.log(m.get("?a"))
console.log(m.get("a"))
`, "1\nnull")
}

func TestE2EQuerystringStringify(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, string>()
m.set("q", "hello world")
m.set("page", "2")
console.log(querystring.stringify(m))
`, "q=hello%20world&page=2")
}

func TestE2EQuerystringRoundTrip(t *testing.T) {
	assertOutput(t, `
const parsed = querystring.parse("a=1&b=2")
console.log(querystring.stringify(parsed))
`, "a=1&b=2")
}

// --- assert (see docs/adr/ADR-00140.md) ---

func TestE2EAssertOkPasses(t *testing.T) {
	assertOutput(t, `
assert.ok(1 === 1)
assert(true)
console.log("reached")
`, "reached")
}

func TestE2EAssertOkThrowsWithDefaultMessage(t *testing.T) {
	assertOutput(t, `
try {
  assert.ok(1 === 2)
} catch (e) {
  console.log(e.name)
  console.log(e.message)
}
`, "AssertionError\nthe expression evaluated to a falsy value")
}

func TestE2EAssertOkThrowsWithCustomMessage(t *testing.T) {
	assertOutput(t, `
try {
  assert(false, "custom failure")
} catch (e) {
  console.log(e.message)
}
`, "custom failure")
}

func TestE2EAssertEqualPasses(t *testing.T) {
	assertOutput(t, `
assert.equal(1, 1)
assert.strictEqual("a", "a")
console.log("ok")
`, "ok")
}

func TestE2EAssertEqualThrows(t *testing.T) {
	assertOutput(t, `
try {
  assert.equal(1, 2)
} catch (e) {
  console.log(e.message)
}
`, "values are not equal")
}

func TestE2EAssertNotEqualPassesAndThrows(t *testing.T) {
	assertOutput(t, `
assert.notEqual(1, 2)
try {
  assert.notStrictEqual(5, 5)
} catch (e) {
  console.log(e.message)
}
`, "values are equal")
}

func TestE2EAssertFail(t *testing.T) {
	assertOutput(t, `
try {
  assert.fail("boom")
} catch (e) {
  console.log(e.name + ": " + e.message)
}
try {
  assert.fail()
} catch (e) {
  console.log(e.message)
}
`, "AssertionError: boom\nfailed")
}

func TestE2EAssertThrowsPassesWhenFunctionThrows(t *testing.T) {
	assertOutput(t, `
assert.throws(() => { throw new Error("boom") })
console.log("ok")
`, "ok")
}

func TestE2EAssertThrowsFailsWhenFunctionDoesNotThrow(t *testing.T) {
	assertOutput(t, `
try {
  assert.throws(() => { const x = 1 })
} catch (e) {
  console.log(e.message)
}
`, "missing expected exception")
}

func TestE2EAssertThrowsCustomMessageOnMissingException(t *testing.T) {
	assertOutput(t, `
try {
  assert.throws(() => { const x = 1 }, "expected a throw")
} catch (e) {
  console.log(e.message)
}
`, "expected a throw")
}
