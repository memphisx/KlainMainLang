package tests

import "testing"

// string_decoder holds back a multi-byte character split across writes
// (UTF-8, UTF-16, base64) and reports what is left at end.
func TestE2EStringDecoder(t *testing.T) {
	assertOutputImports(t, `
import { StringDecoder } from 'string_decoder'
const d = new StringDecoder('utf8')
const euro = Buffer.from('€x')
console.log(JSON.stringify(d.write(euro.subarray(0, 1))), JSON.stringify(d.write(euro.subarray(1, 2))), JSON.stringify(d.write(euro.subarray(2))))
console.log(JSON.stringify(d.write(Buffer.from([0xE2, 0x82]))), JSON.stringify(d.end()))
const u = new StringDecoder('utf16le')
const b = Buffer.from('hé', 'utf16le')
console.log(JSON.stringify(u.write(b.subarray(0, 3))), JSON.stringify(u.write(b.subarray(3))))
const b64 = new StringDecoder('base64')
console.log(b64.write(Buffer.from('abcd')), b64.end())
try { new StringDecoder('nope') } catch (e: any) { console.log(e.code) }
`, "\"\" \"\" \"€x\"\n\"\" \"�\"\n\"h\" \"é\"\nYWJj ZA==\nERR_UNKNOWN_ENCODING")
}
