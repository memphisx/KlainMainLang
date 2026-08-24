package tests

import (
	"fmt"
	"path/filepath"
	"testing"
)

// --- Asynchronous fs: callback + Promise (fs/promises) forms (TDD-00107) ---
//
// Blocking I/O under the hood (no thread pool), delivered async-shaped: the
// callback fires right after the op, the Promise is returned settled. A failure
// (the throwing sync helper) is caught and re-surfaced as `err` / a rejection.

func TestE2EFsAsyncCallbackRoundTrip(t *testing.T) {
	dir := t.TempDir()
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
	dir := t.TempDir()
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

func TestE2EFsPromisesReaddir(t *testing.T) {
	dir := t.TempDir()
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
	dir := t.TempDir()
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
