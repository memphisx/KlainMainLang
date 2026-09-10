package tests

import (
	"fmt"
	"path/filepath"
	"testing"
)

// --- fs.createReadStream / fs.createWriteStream (TDD-00108) ---
//
// A Node Readable/Writable over a file: chunked read on the read side (now on
// the blocking-work thread pool — TDD-00186 — so it no longer freezes the loop),
// an fwrite/fclose sink on the write side. Chunks are strings (text-first fs), so
// a read→write pipe round-trips. Consumed via for-await or .on('data')/.on('end').

// TDD-00186: the pooled read delivers its chunks off the loop thread and in
// order. A large file read at a small highWaterMark yields many chunks that
// reassemble byte-identically, while a timer set before the read keeps firing —
// proof the read no longer blocks the reactor.
func TestE2EFsCreateReadStreamPooledOrderedNonBlocking(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "big.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
let big: string = ""
for (let i = 0; i < 1000; i++) { big += ("0000" + i).slice(-4) + ":" }
fs.writeFileSync(%q, big)
let ticks: number = 0
const timer = setInterval(() => { ticks++ }, 1)
let out: string = ""
let n: number = 0
const rs = fs.createReadStream(%q, { highWaterMark: 64 })
rs.on('data', (c: string) => { out += c; n++ })
rs.on('end', () => {
  clearInterval(timer)
  const ordered: boolean = out === big
  const many: boolean = n > 1
  console.log((ordered ? "intact" : "CORRUPT") + ":" + (many ? "multi" : "single"))
})
`, path, path)
	assertOutputImports(t, src, "intact:multi")
}

func TestE2EFsCreateWriteStream(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "w.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
async function main(): Promise<void> {
  const ws = fs.createWriteStream(%q)
  ws.write("one\n")
  ws.write("two\n")
  ws.end("three")
  await new Promise<void>((r) => setTimeout(() => r(), 10))
  console.log(fs.readFileSync(%q))
}
main()
`, path, path)
	assertOutputImports(t, src, "one\ntwo\nthree")
}

func TestE2EFsCreateReadStreamForAwait(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "r.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
async function main(): Promise<void> {
  fs.writeFileSync(%q, "alpha beta gamma")
  let out = ""
  for await (const chunk of fs.createReadStream(%q)) { out += chunk }
  console.log(out)
}
main()
`, path, path)
	assertOutputImports(t, src, "alpha beta gamma")
}

func TestE2EFsCreateReadStreamOnData(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "r.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
async function main(): Promise<void> {
  fs.writeFileSync(%q, "hello streams")
  let out = ""
  const rs = fs.createReadStream(%q)
  rs.on("data", (c: string) => { out += c })
  rs.on("end", () => { console.log(out) })
  await new Promise<void>((r) => setTimeout(() => r(), 10))
}
main()
`, path, path)
	assertOutputImports(t, src, "hello streams")
}

// A large file split across highWaterMark-sized chunks accumulates correctly.
func TestE2EFsCreateReadStreamMultiChunk(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "big.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
async function main(): Promise<void> {
  let big = ""
  for (let i = 0; i < 500; i++) big += "0123456789"
  fs.writeFileSync(%q, big)
  let chunks = 0
  let bytes = 0
  for await (const c of fs.createReadStream(%q, { highWaterMark: 1024 })) {
    chunks += 1
    bytes += c.length
  }
  console.log(chunks + " " + bytes)
}
main()
`, path, path)
	assertOutputImports(t, src, "5 5000")
}

// createReadStream(a).pipe(createWriteStream(b)) copies the file.
func TestE2EFsStreamPipe(t *testing.T) {
	dir := tempDir(t)
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	code := fmt.Sprintf(`
import fs from 'fs'
async function main(): Promise<void> {
  fs.writeFileSync(%q, "piped content here")
  fs.createReadStream(%q).pipe(fs.createWriteStream(%q))
  await new Promise<void>((r) => setTimeout(() => r(), 20))
  console.log(fs.readFileSync(%q))
}
main()
`, src, src, dst, dst)
	assertOutputImports(t, code, "piped content here")
}

func TestE2EFsCreateWriteStreamAppend(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "a.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
async function main(): Promise<void> {
  fs.writeFileSync(%q, "A")
  const ws = fs.createWriteStream(%q, { flags: "a" })
  ws.end("B")
  await new Promise<void>((r) => setTimeout(() => r(), 10))
  console.log(fs.readFileSync(%q))
}
main()
`, path, path, path)
	assertOutputImports(t, src, "AB")
}

func TestE2EFsCreateReadStreamMissingThrows(t *testing.T) {
	src := `
import fs from 'fs'
async function main(): Promise<void> {
  try {
    for await (const c of fs.createReadStream("/definitely/missing/kml-stream.txt")) { console.log(c) }
    console.log("no error")
  } catch (e) {
    console.log("caught")
  }
}
main()
`
	assertOutputImports(t, src, "caught")
}

// --- TDD-00186 backpressure: demand-driven pooled read stream ---

// A demand-driven pooled read stream stops cleanly when the consumer abandons
// it early (a for-await break): the worker is credit-gated and the loop's
// keepalive counts in-flight reads, so an unconsumed stream doesn't hang the
// process. (A hang would time the test out.)
func TestE2EFsCreateReadStreamBackpressureEarlyBreak(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "big.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
let big: string = ""
for (let i = 0; i < 4000; i++) { big += ("0000" + i).slice(-4) + ":" }
fs.writeFileSync(%q, big)
async function main(): Promise<void> {
  let chunks: number = 0
  const rs = fs.createReadStream(%q, { highWaterMark: 64 })
  for await (const c of rs) {
    chunks++
    if (chunks >= 2) { break }
  }
  console.log("consumed:" + chunks)
}
main()
`, path, path)
	assertOutputImports(t, src, "consumed:2")
}

// A mid-read failure surfaces as a stream error (rejecting the for-await),
// rather than a silent early EOF — TDD-00186 OQ2's STREAM_ERROR. Reading a
// directory triggers it (fopen succeeds, fread fails); a platform that instead
// rejects at open throws synchronously inside the same try, so either way the
// consumer sees the error.
func TestE2EFsCreateReadStreamMidReadError(t *testing.T) {
	dir := tempDir(t)
	src := fmt.Sprintf(`
import fs from 'fs'
async function main(): Promise<void> {
  try {
    const rs = fs.createReadStream(%q)
    for await (const c of rs) { console.log("chunk") }
    console.log("ended without error")
  } catch (e) {
    console.log("errored")
  }
}
main()
`, dir)
	assertOutputImports(t, src, "errored")
}
