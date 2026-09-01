package tests

import (
	"testing"
)

// --- TextEncoder / TextDecoder (see docs/status/ENCODING-TEXT.md) ---

func TestE2ETextEncoderEncode(t *testing.T) {
	assertOutput(t, `
const enc = new TextEncoder()
const bytes = enc.encode("Hi!")
console.log(bytes.length)
console.log(bytes[0])
console.log(bytes[1])
console.log(bytes[2])
`, "3\n72\n105\n33")
}

func TestE2ETextEncoderEmptyString(t *testing.T) {
	assertOutput(t, `
const enc = new TextEncoder()
const bytes = enc.encode("")
console.log(bytes.length)
`, "0")
}

func TestE2ETextDecoderDecodeUint8Array(t *testing.T) {
	assertOutput(t, `
const enc = new TextEncoder()
const dec = new TextDecoder()
const bytes = enc.encode("hello world")
console.log(dec.decode(bytes))
`, "hello world")
}

func TestE2ETextDecoderDecodeArrayBuffer(t *testing.T) {
	assertOutput(t, `
const dec = new TextDecoder()
const buf = new ArrayBuffer(3)
const view: Uint8Array = new Uint8Array(buf)
view[0] = 72
view[1] = 105
view[2] = 33
console.log(dec.decode(buf))
`, "Hi!")
}

func TestE2ETextDecoderLabelValidation(t *testing.T) {
	// The label is WHATWG-normalized (trim + lowercase) and validated against
	// the UTF-8 aliases (ADR-00567): a UTF-8 alias decodes; anything else — a
	// recognized non-UTF-8 encoding (latin1) or a bogus label — throws a
	// catchable RangeError. See docs/status/ENCODING-TEXT.md.
	assertOutput(t, `
const enc = new TextEncoder()
console.log(new TextDecoder("utf-8").decode(enc.encode("ok")))
console.log(new TextDecoder("UTF-8").decode(enc.encode("A")))
console.log(new TextDecoder("  utf8 ").decode(enc.encode("B")))
console.log(new TextDecoder("unicode-1-1-utf-8").decode(enc.encode("C")))
try { new TextDecoder("latin1"); console.log("no throw") } catch (e) { console.log((e as Error).name) }
try { new TextDecoder("bogus"); console.log("no throw") } catch (e) { console.log((e as Error).name) }
`, "ok\nA\nB\nC\nRangeError\nRangeError")
}

func TestE2ETextDecoderDecodeUnsupportedArgIsError(t *testing.T) {
	_, err := parseAndCompile(`
const dec = new TextDecoder()
console.log(dec.decode(42))
`)
	if err == nil {
		t.Fatal("expected a compile error for TextDecoder.decode() called with a non-byte-source argument")
	}
}
