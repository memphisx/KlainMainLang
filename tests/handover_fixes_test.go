package tests

import "testing"

// An `any` holding null, read as a `T | null | undefined` pointer, stays
// null: directly, as a parameter, and through a callback adapted from an
// `(e?: any)` type.
func TestE2EAnyNullIntoThreeStatePointer(t *testing.T) {
	assertSameAsNodeImports(t, `
function f(e?: Error | null) { console.log('f', e) }
const x: any = null
f(x)
let y: Error | null | undefined = x
console.log('y', y)
let s: string | null | undefined = x
console.log('s', s)
const g: (e?: Error | null) => void = (e) => console.log('g', e)
const h: (e?: any) => void = g
h(null)
h(undefined)
`)
}

// A socket write's callback gets null on success, as in Node, also through
// a subclass's write override.
func TestE2EWriteCallbackGetsNull(t *testing.T) {
	assertSameAsNodeImports(t, `
import http from 'http'
import { Writable } from 'stream'
class W extends Writable {
  _write(c: any, e: BufferEncoding, cb: (error?: Error | null) => void) { cb(null) }
  write(chunk: any, encoding?: BufferEncoding | ((error?: Error | null) => void), cb?: (error?: Error | null) => void): boolean {
    return super.write(chunk, encoding as any, cb)
  }
}
new W().write('a', (e: any) => console.log('sub cb', e))
const server = http.createServer((req, res) => {
  res.write('a', (e: any) => console.log('res.write cb', e))
  res.end('b')
})
server.listen(0, () => {
  const port = (server.address() as any).port
  const req = http.request({ port, method: 'POST' }, (res) => { res.resume(); res.on('end', () => server.close()) })
  req.write('x', (e: any) => console.log('req.write cb', e))
  req.end()
})
`)
}

// An async callable whose only wait is a `for await` runs as a coroutine:
// the stream operators built on one, called outside any coroutine.
func TestE2EForAwaitOnlyWaitIsCoroutine(t *testing.T) {
	assertSameAsNodeImports(t, `
import { Readable } from 'stream'
setTimeout(() => {
  Readable.from([1, 2, 3]).some(async (x) => x > 2).then((v) => console.log('timer some', v))
}, 1)
process.nextTick(() => { Readable.from([1]).every((x) => x > 0).then((v) => console.log('tick every', v)) })
Readable.from([1, 2, 3]).some((x: number) => x > 5).then((v) => console.log('sync some', v))
Readable.from([1, 2, 3]).find(async (x: number) => x === 2).then((v) => console.log('find', v))
Readable.from([1, 2]).forEach((x: number) => console.log('each', x)).then(() => console.log('each done'))
`)
}

// An error's system field assigned undefined is still its own property
// (keys, inspect, `in`), and `in`/hasOwn see the error's other own
// properties too.
func TestE2EErrorOwnUndefinedField(t *testing.T) {
	assertSameAsNodeImports(t, `
class D extends Error {
  code: string
  constructor(errno: any) { super('m'); (this as any).errno = errno; (this as any).foo = undefined; this.code = 'X' }
}
const a = new D(undefined)
console.log(Object.keys(a), 'errno' in a, (a as any).errno)
console.log(Object.keys(new D(-2)))
console.log(JSON.stringify(a))
const e: any = new Error('z'); e.errno = undefined; e.bar = 1; e.baz = undefined
console.log(Object.keys(e))
console.log('errno' in e, 'bar' in e, 'baz' in e, 'nope' in e, Object.hasOwn(e, 'errno'), e.hasOwnProperty('baz'))
`)
}
