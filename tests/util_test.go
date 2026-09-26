package tests

import "testing"

// --- Node `util`: util.format / util.inspect (ADR-00325) ---
//
// Pure surface over the existing value inspector (console.log's formatter) and
// JSON.stringify; util.promisify is lib/node/internal_util.ts.

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

// util.promisify: the callback's error rejects, its value resolves; typed
// through the @types overloads; a non-function is ERR_INVALID_ARG_TYPE.
func TestE2EUtilPromisify(t *testing.T) {
	assertOutputImports(t, `
import { promisify } from 'util'
import util from 'node:util'
function addLater(a: number, b: number, cb: (err: Error | null, v: number) => void): void {
  setTimeout(() => cb(null, a + b), 1)
}
function failLater(cb: (err: Error | null) => void): void {
  setTimeout(() => cb(new Error("nope")), 1)
}
const add = promisify(addLater)
add(2, 3).then((v) => console.log("sum", v + 1))
util.promisify(failLater)().catch((e: Error) => console.log("rejected", e.message))
for (const v of [5, "s", null] as any[]) {
  try { promisify(v) } catch (e) { const err = e as NodeJS.ErrnoException; console.log(err.name, err.code, err.message) }
}
`, `TypeError ERR_INVALID_ARG_TYPE The "original" argument must be of type function. Received type number (5)
TypeError ERR_INVALID_ARG_TYPE The "original" argument must be of type function. Received type string ('s')
TypeError ERR_INVALID_ARG_TYPE The "original" argument must be of type function. Received null
sum 6
rejected nope`)
}
