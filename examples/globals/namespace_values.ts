// The builtin namespace objects, crypto and the global object are values:
// they can be stored, passed and inspected, as in Node.

function apply(ops: any, name: string, a: number, b: number): number {
  return ops[name](a, b)
}

console.log(apply(Math, 'max', 3, 9), apply(Math, 'pow', 2, 10))

const log = console.log
log('logged through an alias')

const serializer = JSON
console.log(serializer.stringify({ ok: true }))

console.log(Math, JSON, Object.keys(Math).length)
console.log(typeof globalThis, Object.prototype.toString.call(globalThis))

// The global object holds what a program puts on it.
;(globalThis as any).appVersion = '1.2.3'
const g: any = globalThis
console.log(g.appVersion)

// Crypto's methods need their receiver, as Node's do.
const fill = crypto.getRandomValues
try {
  fill(new Uint8Array(4))
} catch (e: any) {
  console.log(e.name, e.code)
}
console.log(fill.call(crypto, new Uint8Array(4)).length)
