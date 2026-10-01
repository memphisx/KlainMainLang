package tests

import "testing"

// `util` as Node's lib/util.js in TypeScript (ADR-01289), compared with
// Node's output.

func TestE2EUtilFormatAndInspect(t *testing.T) {
	assertOutputImports(t, `
import util from 'util'
import { inspect, format } from 'util'
console.log(JSON.stringify(util.format('%s %d %i %f %j %O %% %c|', 'a', 1.5, 2.9, '3.5', { a: 1 }, { b: { c: { d: {} } } }, 'css')))
console.log(JSON.stringify([format(1, 'x', { a: 1 }), format('%s', new Map([[1, 2]])), format('%d', -0), format('%s:%s', 'a'), format('%%s %s', 'x')]))
console.log(inspect({ a: [1, 2, { b: { c: { d: 1 } } }] }, { depth: 0 }))
console.log(inspect({ a: { b: { c: { d: 1 } } } }, { depth: null }))
console.log(inspect([1, 2, 3], { compact: false }))
console.log(inspect(new Array<number>(120).fill(0), { maxArrayLength: 2 }), inspect('str'), inspect({ b: 1, a: 2 }, { sorted: true }))
console.log(util.formatWithOptions({ compact: false }, 'x %O', { a: 1 }))
`, `"a 1.5 2 3.5 {\"a\":1} { b: { c: { d: {} } } } % |"
["1 x { a: 1 }","Map(1) { 1 => 2 }","-0","a:%s","%s x"]
{ a: [Array] }
{
  a: { b: { c: { d: 1 } } }
}
[
  1,
  2,
  3
]
[ 0, 0, ... 118 more items ] 'str' { a: 2, b: 1 }
x {
  a: 1
}`)
}

func TestE2EUtilHelpers(t *testing.T) {
	assertOutputImports(t, `
import util from 'util'
import { callbackify, deprecate, debuglog, types } from 'util'
const cb = callbackify(async (a: number) => a * 2)
cb(21, (err: any, v: any) => console.log('cb', err, v))
const cbf = callbackify(async () => { throw null })
cbf((err: any) => console.log('cbf', err.code, err.message, err.reason))
const old = deprecate((x: number) => x + 1, 'old() is deprecated', 'DEP_TEST')
console.log(old(1), old(2))
const log = debuglog('foo')
log('hidden %d', 1)
console.log(log.enabled)
console.log(types.isPromise(Promise.resolve()), types.isRegExp(/a/), types.isMap(new Map()), types.isTypedArray(new Int8Array(1)), types.isDataView(new DataView(new ArrayBuffer(1))), types.isNativeError(new TypeError('x')), types.isDate(1))
console.log(util.isDeepStrictEqual({ a: [1] }, { a: [1] }), util.isDeepStrictEqual([1], ['1']), util.stripVTControlCharacters('\u001b[31mred\u001b[39m'))
console.log(util.getSystemErrorName(-2), util.getSystemErrorMessage(-2), util.styleText('red', 'plain'), util.toUSVString('ok'))
`, `2 3
false
true true true true true true false
true false red
ENOENT no such file or directory plain ok
cb null 42
cbf ERR_FALSY_VALUE_REJECTION Promise was rejected with falsy value null`)
}
