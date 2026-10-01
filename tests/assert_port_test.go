package tests

import (
	"strings"
	"testing"
)

// `assert` as Node's lib/assert.js in TypeScript (ADR-01288), and the
// language fixes it needed, each compared with Node's output.

func TestE2EAssertNodeMessages(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert'
function t(f: () => void) { try { f() } catch (e: any) { console.log(JSON.stringify([e.name, e.code, e.message, e.operator, e.generatedMessage])) } }
t(() => assert.ok((1 as number) === 2))
t(() => assert(false))
t(() => assert.strictEqual('a', 'b'))
t(() => assert.notStrictEqual(5, 5))
t(() => assert.fail())
t(() => assert.deepStrictEqual([1, 2], [1, 9]))
t(() => assert.deepStrictEqual({ a: 1, b: 'x' }, { a: 2, b: 'x' }))
t(() => assert.match('abc', /nope/))
t(() => assert.strictEqual(1, '1' as any))
t(() => assert.ok(
  1 > 2,
))
`, `["AssertionError","ERR_ASSERTION","The expression evaluated to a falsy value:\n\n  assert.ok((1          ) === 2)\n","==",true]
["AssertionError","ERR_ASSERTION","The expression evaluated to a falsy value:\n\n  assert(false)\n","==",true]
["AssertionError","ERR_ASSERTION","Expected values to be strictly equal:\n\n'a' !== 'b'\n","strictEqual",true]
["AssertionError","ERR_ASSERTION","Expected \"actual\" to be strictly unequal to: 5","notStrictEqual",true]
["AssertionError","ERR_ASSERTION","Failed","fail",true]
["AssertionError","ERR_ASSERTION","Expected values to be strictly deep-equal:\n+ actual - expected\n\n  [\n    1,\n+   2\n-   9\n  ]\n","deepStrictEqual",true]
["AssertionError","ERR_ASSERTION","Expected values to be strictly deep-equal:\n+ actual - expected\n\n  {\n+   a: 1,\n-   a: 2,\n    b: 'x'\n  }\n","deepStrictEqual",true]
["AssertionError","ERR_ASSERTION","The input did not match the regular expression /nope/. Input:\n\n'abc'\n","match",true]
["AssertionError","ERR_ASSERTION","Expected values to be strictly equal:\n\n1 !== '1'\n","strictEqual",true]
["AssertionError","ERR_ASSERTION","The expression evaluated to a falsy value:\n\n  assert.ok(\n","==",true]`)
}

func TestE2EAssertDeepEquality(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert'
class Point { x: number; y: number; constructor(x: number, y: number) { this.x = x; this.y = y } }
class MyErr extends Error {}
function t(label: string, f: () => void) { try { f(); console.log(label, 'pass') } catch (e: any) { console.log(label, JSON.stringify(e.message)) } }
t('map', () => assert.deepStrictEqual(new Map([['a', 1]]), new Map([['a', 1]])))
t('set', () => assert.deepStrictEqual(new Set([1, 2]), new Set([2, 1])))
t('date', () => assert.deepStrictEqual(new Date(0), new Date(1)))
t('regexp', () => assert.deepStrictEqual(/a/g, /a/i))
t('proto', () => assert.deepStrictEqual(new Point(1, 2), { x: 1, y: 2 }))
t('loose', () => assert.deepEqual(new Point(1, 2), { x: 1, y: 2 }))
t('typed', () => assert.deepStrictEqual(new Uint8Array([1, 2]), new Uint8Array([1, 3])))
t('errors', () => assert.deepStrictEqual(new MyErr('a'), new Error('a')))
t('zero', () => assert.deepStrictEqual(0, -0))
t('nested', () => assert.deepStrictEqual(new Map([['a', { x: 1 }]]), new Map([['a', { x: 2 }]])))
t('partial', () => assert.partialDeepStrictEqual({ a: 1, b: 2 }, { a: 1 }))
`, `map pass
set pass
date "Expected values to be strictly deep-equal:\n+ actual - expected\n\n+ 1970-01-01T00:00:00.000Z\n- 1970-01-01T00:00:00.001Z\n"
regexp "Expected values to be strictly deep-equal:\n+ actual - expected\n\n+ /a/g\n- /a/i\n"
proto "Expected values to be strictly deep-equal:\n+ actual - expected\n\n+ Point {\n- {\n    x: 1,\n    y: 2\n  }\n"
loose pass
typed "Expected values to be strictly deep-equal:\n+ actual - expected\n\n  Uint8Array(2) [\n    1,\n+   2\n-   3\n  ]\n"
errors "Expected values to be strictly deep-equal:\n+ actual - expected\n\n+ [MyErr [Error]: a]\n- [Error: a]\n"
zero "Expected values to be strictly deep-equal:\n+ actual - expected\n\n+ 0\n- -0\n"
nested "Expected values to be strictly deep-equal:\n+ actual - expected\n\n  Map(1) {\n    'a' => {\n+     x: 1\n-     x: 2\n    }\n  }\n"
partial pass`)
}

func TestE2EAssertThrowsMatchers(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert'
class MyErr extends Error { code2 = 5 }
function t(label: string, f: () => void) { try { f(); console.log(label, 'pass') } catch (e: any) { console.log(label, JSON.stringify(e.message)) } }
t('class', () => assert.throws(() => { throw new MyErr('m') }, MyErr))
t('builtin', () => assert.throws(() => { throw new TypeError('bad') }, RangeError))
t('regexp', () => assert.throws(() => { throw new Error('boom') }, /zzz/))
t('object', () => assert.throws(() => { throw new Error('boom') }, { message: 'other' }))
t('fn', () => assert.throws(() => { throw new Error('boom') }, (e: any) => false))
t('missing', () => assert.throws(() => {}, TypeError))
t('text', () => assert.throws(() => {}, 'nope'))
async function main() {
  try { await assert.rejects(Promise.reject(new Error('r')), /r/); console.log('rejects pass') } catch (e: any) { console.log('rejects', e.message) }
  try { await assert.rejects(Promise.resolve(1)); console.log('resolves pass') } catch (e: any) { console.log('resolves', JSON.stringify(e.message)) }
}
main()
`, `class pass
builtin "The error is expected to be an instance of \"RangeError\". Received \"TypeError\"\n\nError message:\n\nbad"
regexp "The input did not match the regular expression /zzz/. Input:\n\n'Error: boom'\n"
object "Expected values to be strictly deep-equal:\n+ actual - expected\n\n  Comparison {\n+   message: 'boom'\n-   message: 'other'\n  }\n"
fn "The validation function is expected to return \"true\". Received false\n\nCaught error:\n\nError: boom"
missing "Missing expected exception (TypeError)."
text "Missing expected exception: nope"
rejects pass
resolves "Missing expected rejection."`)
}

func TestE2EAssertStrictModule(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert/strict'
import { equal } from 'node:assert/strict'
import { strict } from 'assert'
function t(f: () => void) { try { f(); console.log('pass') } catch (e: any) { console.log(JSON.stringify(e.message)) } }
t(() => assert.equal(1, '1' as any))
t(() => equal(1, '1' as any))
t(() => assert(0))
t(() => strict.equal(2, 3))
`, `"Expected values to be strictly equal:\n\n1 !== '1'\n"
"Expected values to be strictly equal:\n\n1 !== '1'\n"
"The expression evaluated to a falsy value:\n\n  assert(0)\n"
"Expected values to be strictly equal:\n\n2 !== 3\n"`)
}

func TestE2EAssertOverloadArity(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"import { strictEqual } from \"assert\"\nstrictEqual(1)\n", "expected 2-3 arguments, but got 1"},
		{"import assert from \"assert\"\nassert.fail(1, 2, 'm', 'op', undefined, 7)\n", "expected 0-5 arguments, but got 6"},
	} {
		_, err := resolveAndCompile(t, c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: expected %q, got %v", c.src, c.want, err)
		}
	}
}

func TestE2EClassValueAndDynamicInstanceof(t *testing.T) {
	assertOutput(t, `
class A { a = 1 }
class B extends A {}
class Foo extends Error {}
function g(x: any, C: any) { return x instanceof C }
const C: any = Foo
console.log(typeof C, C.name, new Foo('x') instanceof C, new Error('y') instanceof C, C === Foo)
console.log(g(new B(), A), g(new A(), B), g({}, A), g(1, A), g(new RangeError('r'), Error), g(new Error('e'), RangeError))
`, `function Foo true false true
true false false false true false`)
}

func TestE2EErrorMessagesAndSubclassFields(t *testing.T) {
	assertOutput(t, `
class B extends Error { k = 5 }
class E2 extends Error { code = 'C' }
class C extends Error { constructor(m?: string) { super(m) } }
const a: any = undefined
console.log(JSON.stringify(new TypeError().message), String(new TypeError()), JSON.stringify(new Error(a).message), JSON.stringify(new C().message))
console.log(new B('y').message, new B('z').k, new B().message === '', new E2('m').code)
class M extends Error { op = 'o' }
function g(o: any, k: string) { return o[k] }
const e: any = new M('x')
console.log(Object.keys(e), 'message' in e, 'op' in e, 'name' in e, e.op, g(e, 'op'), g(e, 'message'), g(new Error('w'), 'message'))
try { throw new M('t') } catch (x: any) { console.log(x.op, x.message) }
`, `"" TypeError "" ""
y 5 true C
[ 'op' ] true true true o o x w
o t`)
}

func TestE2EObjectPrototypeCall(t *testing.T) {
	assertOutput(t, `
function h(o: any, k: any): boolean { return Object.prototype.hasOwnProperty.call(o, k) }
function en(o: any, k: any): boolean { return Object.prototype.propertyIsEnumerable.call(o, k) }
console.log(h({ a: 1 }, 'a'), h({ a: 1 }, 'b'), en({ a: 1 }, 'a'), en([1], 'length'), en([1], '0'))
const vals: any[] = [1, 'a', null, undefined, [1], { a: 1 }, new Map(), new Date(0), /x/, new Error('e'), new Uint8Array(1), () => 1, 10n]
console.log(vals.map((v) => Object.prototype.toString.call(v)).join(' '))
`, `true false true false true
[object Number] [object String] [object Null] [object Undefined] [object Array] [object Object] [object Map] [object Date] [object RegExp] [object Error] [object Uint8Array] [object Function] [object BigInt]`)
}

func TestE2ECodegenFixesForAssert(t *testing.T) {
	assertOutput(t, `
const r: [number, string][] = []
r.push([1, 'a'])
r.unshift([0, 'z'])
console.log(r, r[1][1])
const v = new Int32Array(3); v[1] = 5
const c: boolean = Math.random() < 2
const n = v[1]; const p = v[0]
const x = c ? n : p + 1
console.log(x)
function call(expected: any, a: any) { const res = expected(a); return res }
console.log(call((e: any) => false, 1))
async function ar(block: any, k: number): Promise<void> { await 1; console.log('after', typeof block, k) }
const hv: any = ar
hv('s', 2)
const b: any = /nope/.exec('abc')
console.log(b === null, [e1()])
function e1(): any { return 0 }
const t = [e1(), 1]
console.log(t)
`, `[ [ 0, 'z' ], [ 1, 'a' ] ] a
5
false
true [ 0 ]
[ 0, 1 ]
after string 2`)
}

func TestE2EAssertClass(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert'
import { Assert } from 'assert'
function t(f: () => void) { try { f(); console.log('pass') } catch (e: any) { console.log(JSON.stringify(e.message), e.diff) } }
const a = new Assert()
t(() => a.equal(1, '1' as any))
const loose = new Assert({ strict: false })
t(() => loose.equal(1, '1' as any))
const full = new assert.Assert({ diff: 'full' })
t(() => full.deepStrictEqual({ a: 1 }, { a: 2 }))
t(() => a.ok(0))
t(() => { new Assert({ diff: 'x' as any }) })
`, `"Expected values to be strictly equal:\n\n1 !== '1'\n" simple
pass
"Expected values to be strictly deep-equal:\n+ actual - expected\n\n  {\n+   a: 1\n-   a: 2\n  }\n" full
"The expression evaluated to a falsy value:\n\n  a.ok(0)\n" simple
"The property 'options.diff' must be one of: 'simple', 'full'. Received 'x'" undefined`)
}
