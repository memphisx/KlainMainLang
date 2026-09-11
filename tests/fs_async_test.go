package tests

import (
	"fmt"
	"path/filepath"
	"testing"
)

// --- Asynchronous fs: callback + Promise (fs/promises) forms (TDD-00107) ---
//
// The callback form and the non-readFile Promise ops are async-shaped over the
// blocking sync helper (the callback fires right after the op, the Promise is
// returned settled). fs.promises.readFile is genuinely non-blocking as of
// TDD-00185 Stage 2: it runs on the blocking-work thread pool and returns a
// pending Promise the loop settles on completion. A failure (the throwing sync
// helper, run under the worker's own setjmp guard) is caught and re-surfaced as
// `err` / a rejection either way.

func TestE2EFsAsyncCallbackRoundTrip(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "cb.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
fs.writeFile(%q, "hello world", (err) => {
  if (err) { console.log("write err"); return }
  fs.readFile(%q, (err2, data: string) => {
    if (err2) { console.log("read err"); return }
    console.log("read: " + data)
    fs.unlink(%q, (err3) => { console.log(err3 ? "unlink err" : "unlinked") })
  })
})
`, path, path, path)
	assertOutputImports(t, src, "read: hello world\nunlinked")
}

func TestE2EFsAsyncCallbackErrorFirst(t *testing.T) {
	src := `
import fs from 'fs'
fs.readFile("/definitely/does/not/exist/kml-fs-async.txt", (err, data: string) => {
  console.log(err ? "ENOENT caught" : "unexpected ok")
})
`
	assertOutputImports(t, src, "ENOENT caught")
}

func TestE2EFsPromisesAwaitRoundTrip(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "p.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
async function main(): Promise<void> {
  await fs.promises.writeFile(%q, "promise data")
  const s: string = await fs.promises.readFile(%q)
  console.log("read: " + s)
  await fs.promises.unlink(%q)
  console.log("done")
}
main()
`, path, path, path)
	assertOutputImports(t, src, "read: promise data\ndone")
}

func TestE2EFsPromisesRejectionCaught(t *testing.T) {
	src := `
import fs from 'fs'
async function main(): Promise<void> {
  try {
    const s: string = await fs.promises.readFile("/definitely/does/not/exist/kml-fs-async2.txt")
    console.log(s)
  } catch (e) {
    console.log("rejected caught")
  }
}
main()
`
	assertOutputImports(t, src, "rejected caught")
}

// TDD-00185 Stage 2: fs.promises.readFile runs on the thread pool. Three reads
// launched together are submitted concurrently, run on the worker threads, and
// their pending Promises are settled by the loop's completion drain — proving
// the pool queue + per-loop completion stack round-trip for multiple in-flight
// items at once (not the old settle-immediately shape, which never parks).
func TestE2EFsPromisesPooledConcurrentReads(t *testing.T) {
	dir := tempDir(t)
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	c := filepath.Join(dir, "c.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
async function main(): Promise<void> {
  await fs.promises.writeFile(%q, "A")
  await fs.promises.writeFile(%q, "B")
  await fs.promises.writeFile(%q, "C")
  const parts: string[] = await Promise.all([
    fs.promises.readFile(%q),
    fs.promises.readFile(%q),
    fs.promises.readFile(%q),
  ])
  console.log(parts[0] + parts[1] + parts[2])
}
main()
`, a, b, c, a, b, c)
	assertOutputImports(t, src, "ABC")
}

// The loop must not exit while a pooled read is in flight, and other work
// scheduled before the await must still run — i.e. the read no longer freezes
// the reactor. A timer set before the await fires (its callback runs), and the
// awaited read still resolves afterward.
func TestE2EFsPromisesPooledReadDoesNotFreezeLoop(t *testing.T) {
	skipInlineFsPoolOnWindows(t)
	dir := tempDir(t)
	p := filepath.Join(dir, "big.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
async function main(): Promise<void> {
  await fs.promises.writeFile(%q, "payload")
  let timerFired: boolean = false
  setTimeout(() => { timerFired = true }, 0)
  const s: string = await fs.promises.readFile(%q)
  await fs.promises.readFile(%q)
  console.log((timerFired ? "timer-ran" : "timer-missed") + ":" + s)
}
main()
`, p, p, p)
	assertOutputImports(t, src, "timer-ran:payload")
}

// TDD-00185 Stage 3: the whole fs.promises op table runs on the thread pool —
// void mutators (mkdir/writeFile/appendFile/copyFile/rename/unlink/rmdir), the
// string-returning readFile, and the string[]-returning readdir all round-trip
// through the pool's per-op thunks + the two-word completion settle.
func TestE2EFsPromisesPooledFullTable(t *testing.T) {
	dir := tempDir(t)
	d := filepath.Join(dir, "s3")
	src := fmt.Sprintf(`
import fs from 'fs'
async function main(): Promise<void> {
  await fs.promises.mkdir(%q)
  await fs.promises.writeFile(%q, "alpha")
  await fs.promises.appendFile(%q, "-beta")
  await fs.promises.copyFile(%q, %q)
  await fs.promises.rename(%q, %q)
  const a: string = await fs.promises.readFile(%q)
  const c: string = await fs.promises.readFile(%q)
  const entries: string[] = await fs.promises.readdir(%q)
  console.log(a + "|" + c + "|" + entries.length)
  await fs.promises.unlink(%q)
  await fs.promises.unlink(%q)
  await fs.promises.rmdir(%q)
  console.log("cleaned")
}
main()
`,
		d,
		filepath.Join(d, "a.txt"),
		filepath.Join(d, "a.txt"),
		filepath.Join(d, "a.txt"), filepath.Join(d, "b.txt"),
		filepath.Join(d, "b.txt"), filepath.Join(d, "c.txt"),
		filepath.Join(d, "a.txt"),
		filepath.Join(d, "c.txt"),
		d,
		filepath.Join(d, "a.txt"),
		filepath.Join(d, "c.txt"),
		d)
	assertOutputImports(t, src, "alpha-beta|alpha-beta|2\ncleaned")
}

// A pooled *mutator* rejects with the same Node error shape as the sync form —
// the Error is built on the worker (under its setjmp guard) and surfaced on the
// loop thread at completion.
func TestE2EFsPromisesPooledMutatorRejection(t *testing.T) {
	src := `
import fs from 'fs'
async function main(): Promise<void> {
  try {
    await fs.promises.unlink("/definitely/does/not/exist/kml-pool-unlink.txt")
    console.log("FAIL: no throw")
  } catch (e: any) {
    console.log("rejected " + e.code)
  }
}
main()
`
	assertOutputImports(t, src, "rejected ENOENT")
}

func TestE2EFsPromisesReaddir(t *testing.T) {
	dir := tempDir(t)
	a := filepath.Join(dir, "one.txt")
	b := filepath.Join(dir, "two.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
async function main(): Promise<void> {
  await fs.promises.writeFile(%q, "1")
  await fs.promises.writeFile(%q, "2")
  const files: string[] = await fs.promises.readdir(%q)
  console.log(files.length)
}
main()
`, a, b, dir)
	assertOutputImports(t, src, "2")
}

func TestE2EFsPromisesNamedImport(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "np.txt")
	src := fmt.Sprintf(`
import { readFile, writeFile, unlink } from 'fs/promises'
async function main(): Promise<void> {
  await writeFile(%q, "fs/promises works")
  const s: string = await readFile(%q)
  console.log(s)
  await unlink(%q)
}
main()
`, path, path, path)
	assertOutputImports(t, src, "fs/promises works")
}

// A missing trailing callback is a clean compile error.
func TestE2EFsAsyncMissingCallbackRejected(t *testing.T) {
	_, err := parseAndCompile(`
import fs from 'fs'
fs.readFile("x.txt")
`)
	if err == nil {
		t.Fatal("expected a compile error for fs.readFile with no callback, got none")
	}
}

// --- TDD-00185: pooled binary writes + pooled callback form ---
//
// Binary writeFile/appendFile (an ArrayBuffer/TypedArray body) and the legacy
// callback form both run on the thread pool now, off the event-loop thread —
// closing the two "still inline" leftovers of TDD-00185. Binary bodies are
// copied raw at submit (an embedded NUL survives); the callback fires from a
// GC-safe settle reaction on the loop thread.

// fs.promises.writeFile/appendFile of a Uint8Array pools onto the explicit-
// length byte thunk — an embedded NUL is written whole, not truncated.
func TestE2EFsPromisesWriteBinaryPooledEmbeddedNull(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "bin.dat")
	src := fmt.Sprintf(`
import fs from 'fs'
async function main(): Promise<void> {
  await fs.promises.writeFile(%q, new Uint8Array([65, 66, 0, 67, 68]))
  const back: string = await fs.promises.readFile(%q)
  console.log("len:" + back.length)
  await fs.promises.appendFile(%q, new Uint8Array([69, 70]))
  const back2: string = await fs.promises.readFile(%q)
  console.log("len2:" + back2.length)
  await fs.promises.unlink(%q)
}
main()
`, path, path, path, path, path)
	assertOutputImports(t, src, "len:5\nlen2:7")
}

// The callback form of a binary writeFile pools too, preserving the NUL.
func TestE2EFsAsyncCallbackWriteBinaryEmbeddedNull(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "cbbin.dat")
	src := fmt.Sprintf(`
import fs from 'fs'
fs.writeFile(%q, new Uint8Array([65, 66, 0, 67, 68]), (err) => {
  if (err) { console.log("werr"); return }
  fs.readFile(%q, (e2, d: string) => {
    if (e2) { console.log("rerr"); return }
    console.log("len:" + d.length)
    fs.unlink(%q, (e3) => {})
  })
})
`, path, path, path)
	assertOutputImports(t, src, "len:5")
}

// The pooled callback form still delivers array data (readdir) correctly.
func TestE2EFsAsyncCallbackReaddir(t *testing.T) {
	dir := tempDir(t)
	src := fmt.Sprintf(`
import fs from 'fs'
fs.writeFileSync(%q, "hi")
fs.readdir(%q, (err, entries: string[]) => {
  if (err) { console.log("err"); return }
  let found: boolean = false
  for (const e of entries) { if (e === "x.txt") { found = true } }
  console.log(found ? "found" : "missing")
})
`, filepath.Join(dir, "x.txt"), dir)
	assertOutputImports(t, src, "found")
}

// The pooled callback form does not block the reactor. A concurrently scheduled
// setTimeout(0) fires *before* the read's callback, because the read parks on a
// pool thread (a real round-trip) while the 0 ms timer is immediately due on the
// loop — an inline read would run to completion first and settle its callback
// microtask ahead of the timer.
func TestE2EFsAsyncCallbackPooledNonBlocking(t *testing.T) {
	skipInlineFsPoolOnWindows(t)
	dir := tempDir(t)
	path := filepath.Join(dir, "nb.dat")
	src := fmt.Sprintf(`
import fs from 'fs'
fs.writeFileSync(%q, "data")
let timerFirst: boolean = false
let done: boolean = false
setTimeout(() => { if (!done) { timerFirst = true } }, 0)
fs.readFile(%q, (err, data: string) => {
  done = true
  console.log(timerFirst ? "nonblocking" : "blocked")
  fs.unlink(%q, (e) => {})
})
`, path, path, path)
	assertOutputImports(t, src, "nonblocking")
}
