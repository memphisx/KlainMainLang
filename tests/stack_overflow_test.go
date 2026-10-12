package tests

import (
	"strings"
	"testing"
)

// Unbounded recursion throws Node's RangeError, which the program can catch
// and recover from (TDD-00249). The self-call in tail position used to
// become a loop and never end.
func TestE2EStackOverflowCaught(t *testing.T) {
	assertOutput(t, `
let n = 0
function recurse(): void {
  n++
  recurse()
}
try { recurse() } catch (e: any) {
  console.log(e.name, e.message, e instanceof RangeError, n > 1000)
}
const isEven = (k: number): boolean => k === 0 ? true : isOdd(k - 1)
const isOdd = (k: number): boolean => k === 0 ? false : isEven(k - 1)
try { isEven(1e12) } catch (e: any) { console.log('mutual', e.message) }
class Walker { step(d: number): number { return this.step(d + 1) + 1 } }
try { new Walker().step(0) } catch (e: any) { console.log('method', e.name) }
console.log('after')
`, "RangeError Maximum call stack size exceeded true true\nmutual Maximum call stack size exceeded\nmethod RangeError\nafter")
}

// Uncaught, the overflow ends the program with exit code 1, as in Node.
func TestE2EStackOverflowUncaught(t *testing.T) {
	out, code := compileAndRunExpectExit(t, `
function f(n: number): number { return f(n + 1) + 1 }
console.log('start')
console.log(f(0))
`)
	if code != 1 || !strings.HasPrefix(out, "start") {
		t.Fatalf("exit %d, stdout %q; want exit 1 after \"start\"", code, out)
	}
}

// Node's test-fs-readdir-stack-overflow.js: each frame allocates, so the
// loop the recursion used to become allocated until memory ran out. It
// lists an empty directory: every frame's listing stays live until the
// overflow, and the native stack holds far more frames than Node's.
func TestE2EStackOverflowReaddir(t *testing.T) {
	assertOutputImports(t, `
import fs from 'fs'
import os from 'os'
import path from 'path'
import assert from 'assert'
const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'so-'))
function recurse(): void {
  fs.readdirSync(dir)
  recurse()
}
assert.throws(() => recurse(), { name: 'RangeError', message: 'Maximum call stack size exceeded' })
fs.rmdirSync(dir)
console.log('ok')
`, "ok")
}

// Inside an async function, past an await, the recursion runs on the task's
// own fiber stack; the guard page under it turns the overflow into the same
// RangeError instead of a write over the heap.
func TestE2EStackOverflowInAsyncTask(t *testing.T) {
	assertOutput(t, `
function recurse(n: number): number { return recurse(n + 1) + 1 }
async function main(): Promise<void> {
  await null
  try { recurse(0) } catch (e: any) { console.log('task', e.name, e.message) }
  await null
  try { recurse(0) } catch (e: any) { console.log('again', e.name) }
}
main().then(() => console.log('done'))
`, "task RangeError Maximum call stack size exceeded\nagain RangeError\ndone")
}
