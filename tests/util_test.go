package tests

import "testing"

// --- Node `util`: util.format / util.inspect (ADR-00325) ---
//
// Pure surface over the existing value inspector (console.log's formatter) and
// JSON.stringify. util.promisify is intentionally absent (importing it fails).

func TestE2EUtilFormatSpecifiers(t *testing.T) {
	assertOutputImports(t, `
import util from 'util'
console.log(util.format("%s = %d", "count", 42))
console.log(util.format("pi is %f", 3.5))
console.log(util.format("json: %j", { a: 1, b: [2, 3] }))
`, "count = 42\npi is 3.5\njson: {\"a\":1,\"b\":[2,3]}")
}

func TestE2EUtilFormatLiteralPercentAndExtraArgs(t *testing.T) {
	assertOutputImports(t, `
import util from 'util'
console.log(util.format("100%% done", "extra", 7))
`, "100% done extra 7")
}

func TestE2EUtilInspect(t *testing.T) {
	assertOutputImports(t, `
import util from 'util'
console.log(util.inspect("a string"))
console.log(util.inspect([1, 2, 3]))
console.log(util.inspect({ name: "kml", tags: ["ts", "native"] }))
`, "'a string'\n[ 1, 2, 3 ]\n{ name: 'kml', tags: [ 'ts', 'native' ] }")
}
