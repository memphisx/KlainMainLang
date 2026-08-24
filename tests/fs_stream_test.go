package tests

import (
	"fmt"
	"path/filepath"
	"testing"
)

// --- fs.createReadStream / fs.createWriteStream (TDD-00108) ---
//
// A Node Readable/Writable over a file: eager chunked fread on the read side, an
// fwrite/fclose sink on the write side. Chunks are strings (text-first fs), so a
// read→write pipe round-trips. Consumed via for-await or .on('data')/.on('end').

func TestE2EFsCreateWriteStream(t *testing.T) {
	dir := t.TempDir()
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
	dir := t.TempDir()
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
	dir := t.TempDir()
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
	dir := t.TempDir()
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
	dir := t.TempDir()
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
	dir := t.TempDir()
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
