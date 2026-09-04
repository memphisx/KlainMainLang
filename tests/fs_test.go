package tests

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// --- fs (readFileSync/writeFileSync/appendFileSync/existsSync/unlinkSync) ---

func TestE2EFsWriteReadAppendUnlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
const path: string = %q
console.log(fs.existsSync(path))
fs.writeFileSync(path, "hello")
console.log(fs.existsSync(path))
const content: string = fs.readFileSync(path)
console.log(content)
fs.appendFileSync(path, " world")
console.log(fs.readFileSync(path))
fs.unlinkSync(path)
console.log(fs.existsSync(path))
`, path)
	assertOutputImports(t, src, "false\ntrue\nhello\nhello world\nfalse")
}

func TestE2EFsWriteFileSyncOverwritesExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
fs.writeFileSync(%q, "first")
fs.writeFileSync(%q, "second")
console.log(fs.readFileSync(%q))
`, path, path, path)
	assertOutputImports(t, src, "second")
}

func TestE2EFsReadFileSyncUntypedInference(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
fs.writeFileSync(%q, "abc")
const content = fs.readFileSync(%q)
console.log(content.length)
`, path, path)
	assertOutputImports(t, src, "3")
}

func TestE2EFsReadFileSyncNonexistentThrows(t *testing.T) {
	src := `
import fs from 'fs'
try {
    const content: string = fs.readFileSync("/definitely/does/not/exist/kml-test-file.txt")
    console.log(content)
} catch (e) {
    console.log("caught")
}
`
	assertOutputImports(t, src, "caught")
}

// TDD-00120: an error's .message is built raw (strerror + sprintf); after the
// binary-safe consumer switch, concatenating or searching it (the common
// `'prefix: ' + e.message` idiom) reads a length header at message-8. If the
// producer isn't headered that's garbage → truncation or an intermittent
// SIGBUS (caught by examples/fs/fs.ts before this test existed). Header the
// message and the idiom works: includes() finds the path, and concat keeps the
// full length.
func TestE2EFsErrorMessageConcatBinarySafe(t *testing.T) {
	src := `
import fs from 'fs'
try {
    fs.readFileSync("/definitely/does/not/exist/kml-test-file.txt")
} catch (e) {
    const m: string = e.message
    console.log(m.includes("kml-test-file.txt"))
    console.log(("caught: " + m).length === 8 + m.length)
}
`
	assertOutputImports(t, src, "true\ntrue")
}

func TestE2EFsReadFileSyncNonexistentUncaughtExitsNonZero(t *testing.T) {
	_, exitCode := compileAndRunExpectExitImports(t, `
import fs from 'fs'
const content: string = fs.readFileSync("/definitely/does/not/exist/kml-test-file.txt")
console.log(content)
`)
	if exitCode == 0 {
		t.Fatal("expected a non-zero exit code for an uncaught fs.readFileSync failure, got 0")
	}
}

func TestE2EFsUnlinkSyncNonexistentThrows(t *testing.T) {
	src := `
import fs from 'fs'
try {
    fs.unlinkSync("/definitely/does/not/exist/kml-test-file.txt")
} catch (e) {
    console.log("caught")
}
`
	assertOutputImports(t, src, "caught")
}

func TestE2EFsWriteFileSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `import fs from 'fs'
fs.writeFileSync("a")`)
	if err == nil {
		t.Fatal("expected a compile error for fs.writeFileSync with the wrong argument count, got none")
	}
}

func TestE2EFsReadFileSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `import fs from 'fs'
fs.readFileSync("a", "b")`)
	if err == nil {
		t.Fatal("expected a compile error for fs.readFileSync with the wrong argument count, got none")
	}
}

// --- fs.readFileSyncBytes / binary-aware writeFileSync/appendFileSync (ADR-00094) ---

// TestE2EFsReadFileSyncBytesPreservesEmbeddedNullByte writes the fixture
// directly via os.WriteFile (real Go, not KML) rather than fs.writeFileSync,
// since a null byte can't survive today's strlen-based string write path —
// this test is exercising the read side only. The null is NOT at the end
// (byte 2 of 6), so an off-by-one in the length threading can't hide behind
// a null-at-the-end body.
func TestE2EFsReadFileSyncBytesPreservesEmbeddedNullByte(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.bin")
	if err := os.WriteFile(path, []byte{'h', 'i', 0, 'b', 'y', 'e'}, 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	src := fmt.Sprintf(`
import fs from 'fs'
const arr = fs.readFileSyncBytes(%q)
console.log(arr.length)
for (let i = 0; i < arr.length; i++) {
    console.log(arr[i])
}
`, path)
	assertOutputImports(t, src, "6\n104\n105\n0\n98\n121\n101")
}

func TestE2EFsReadFileSyncBytesNonexistentThrows(t *testing.T) {
	src := `
import fs from 'fs'
try {
    const arr = fs.readFileSyncBytes("/definitely/does/not/exist/kml-test-file.bin")
    console.log(arr.length)
} catch (e) {
    console.log("caught")
}
`
	assertOutputImports(t, src, "caught")
}

func TestE2EFsReadFileSyncBytesWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `import fs from 'fs'
fs.readFileSyncBytes("a", "b")`)
	if err == nil {
		t.Fatal("expected a compile error for fs.readFileSyncBytes with the wrong argument count, got none")
	}
}

// TestE2EFsWriteFileSyncUint8ArrayRoundTripsEmbeddedNullByte exercises the
// new write side end to end: a KML-constructed Uint8Array containing an
// embedded null, written via fs.writeFileSync, read back via os.ReadFile
// (real Go) to confirm the bytes on disk are exactly right, not just what
// this compiler's own readFileSyncBytes reports.
func TestE2EFsWriteFileSyncUint8ArrayRoundTripsEmbeddedNullByte(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.bin")
	src := fmt.Sprintf(`
import fs from 'fs'
const arr = new Uint8Array([104, 105, 0, 98, 121, 101])
fs.writeFileSync(%q, arr)
`, path)
	assertOutputImports(t, src, "")

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile: %v", err)
	}
	want := []byte{'h', 'i', 0, 'b', 'y', 'e'}
	if string(got) != string(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestE2EFsAppendFileSyncUint8ArrayRoundTripsEmbeddedNullByte(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.bin")
	src := fmt.Sprintf(`
import fs from 'fs'
const arr = new Uint8Array([1, 0, 2])
fs.writeFileSync(%q, arr)
fs.appendFileSync(%q, arr)
`, path, path)
	assertOutputImports(t, src, "")

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile: %v", err)
	}
	want := []byte{1, 0, 2, 1, 0, 2}
	if string(got) != string(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestE2EFsWriteFileSyncArrayBufferRoundTripsEmbeddedNullByte covers the
// ArrayBuffer branch specifically (as opposed to the TypedArray-view branch
// the tests above already cover) — data written through a Uint8Array view
// but passed to writeFileSync as the underlying ArrayBuffer itself.
func TestE2EFsWriteFileSyncArrayBufferRoundTripsEmbeddedNullByte(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.bin")
	src := fmt.Sprintf(`
import fs from 'fs'
const buf = new ArrayBuffer(3)
const view = new Uint8Array(buf)
view[0] = 5
view[1] = 0
view[2] = 6
fs.writeFileSync(%q, buf)
`, path)
	assertOutputImports(t, src, "")

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile: %v", err)
	}
	want := []byte{5, 0, 6}
	if string(got) != string(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestE2EFsWriteFileSyncStringPathUnchanged pins down that the existing
// strlen-based string path (fs.writeFileSync/appendFileSync with a plain
// string argument) is completely unaffected by the new ArrayBuffer/
// TypedArray branch added alongside it.
func TestE2EFsWriteFileSyncStringPathUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
const path: string = %q
fs.writeFileSync(path, "hello")
console.log(fs.readFileSync(path))
fs.appendFileSync(path, " world")
console.log(fs.readFileSync(path))
`, path)
	assertOutputImports(t, src, "hello\nhello world")
}

// --- fs.mkdirSync / renameSync / copyFileSync / readdirSync ---

func TestE2EFsMkdirSyncCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "newdir")
	src := fmt.Sprintf(`
import fs from 'fs'
console.log(fs.existsSync(%q))
fs.mkdirSync(%q)
console.log(fs.existsSync(%q))
`, sub, sub, sub)
	assertOutputImports(t, src, "false\ntrue")
}

func TestE2EFsMkdirSyncAlreadyExistsThrows(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "newdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("os.Mkdir(%q): %v", sub, err)
	}
	src := fmt.Sprintf(`
import fs from 'fs'
try {
    fs.mkdirSync(%q)
    console.log("should not print")
} catch (e) {
    console.log(e.message.startsWith("cannot create directory '%s': "))
}
`, sub, sub)
	assertOutputImports(t, src, "true")
}

func TestE2EFsMkdirSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `import fs from 'fs'
fs.mkdirSync()`)
	if err == nil {
		t.Fatal("expected a compile error for fs.mkdirSync() with no arguments, got none")
	}
}

func TestE2EFsRmdirSyncRemovesEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "toremove")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("os.Mkdir(%q): %v", sub, err)
	}
	src := fmt.Sprintf(`
import fs from 'fs'
console.log(fs.existsSync(%q))
fs.rmdirSync(%q)
console.log(fs.existsSync(%q))
`, sub, sub, sub)
	assertOutputImports(t, src, "true\nfalse")
}

func TestE2EFsRmdirSyncRecursive(t *testing.T) {
	// ADR-00578: fs.rmdirSync(path, { recursive: true }) removes the whole tree.
	dir := t.TempDir()
	root := filepath.Join(dir, "tree")
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "b", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	src := fmt.Sprintf(`
import fs from 'fs'
console.log(fs.existsSync(%q))
fs.rmdirSync(%q, { recursive: true })
console.log(fs.existsSync(%q))
`, root, root, root)
	assertOutputImports(t, src, "true\nfalse")
}

func TestE2EFsRmdirSyncNonEmptyThrows(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "nonempty")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("os.Mkdir(%q): %v", sub, err)
	}
	if err := os.WriteFile(filepath.Join(sub, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	src := fmt.Sprintf(`
import fs from 'fs'
try {
    fs.rmdirSync(%q)
    console.log("should not print")
} catch (e) {
    console.log("caught")
}
`, sub)
	assertOutputImports(t, src, "caught")
}

func TestE2EFsRmdirSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `import fs from 'fs'
fs.rmdirSync()`)
	if err == nil {
		t.Fatal("expected a compile error for fs.rmdirSync() with no arguments, got none")
	}
}

func TestE2EFsRenameSyncMovesFile(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(dir, "new.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
fs.writeFileSync(%q, "content")
fs.renameSync(%q, %q)
console.log(fs.existsSync(%q))
console.log(fs.existsSync(%q))
console.log(fs.readFileSync(%q))
`, oldPath, oldPath, newPath, oldPath, newPath, newPath)
	assertOutputImports(t, src, "false\ntrue\ncontent")
}

func TestE2EFsRenameSyncNonexistentThrows(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "does-not-exist.txt")
	newPath := filepath.Join(dir, "new.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
try {
    fs.renameSync(%q, %q)
    console.log("should not print")
} catch (e) {
    console.log("caught")
}
`, oldPath, newPath)
	assertOutputImports(t, src, "caught")
}

func TestE2EFsRenameSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `import fs from 'fs'
fs.renameSync("a")`)
	if err == nil {
		t.Fatal("expected a compile error for fs.renameSync with the wrong argument count, got none")
	}
}

func TestE2EFsCopyFileSyncCopiesContent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dest := filepath.Join(dir, "dest.txt")
	code := fmt.Sprintf(`
import fs from 'fs'
fs.writeFileSync(%q, "copy me")
fs.copyFileSync(%q, %q)
console.log(fs.existsSync(%q))
console.log(fs.readFileSync(%q))
console.log(fs.readFileSync(%q))
`, src, src, dest, src, src, dest)
	assertOutputImports(t, code, "true\ncopy me\ncopy me")
}

// TestE2EFsCopyFileSyncPreservesEmbeddedNullByte confirms copyFileSync now
// routes through the binary-safe read_raw/write_bytes pair (ADR-00094), so a
// source file with an embedded null byte copies whole rather than truncated
// at the first null. The fixture is written via real Go os.WriteFile (a null
// can't survive the strlen-based string write path), and the destination is
// verified via real Go os.ReadFile, not this compiler's own readers.
func TestE2EFsCopyFileSyncPreservesEmbeddedNullByte(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dest := filepath.Join(dir, "dest.bin")
	want := []byte{'h', 'i', 0, 'b', 'y', 'e'}
	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	code := fmt.Sprintf(`
import fs from 'fs'
fs.copyFileSync(%q, %q)
`, src, dest)
	assertOutputImports(t, code, "")

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("os.ReadFile: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("copied bytes = %v, want %v", got, want)
	}
}

func TestE2EFsCopyFileSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `import fs from 'fs'
fs.copyFileSync("a")`)
	if err == nil {
		t.Fatal("expected a compile error for fs.copyFileSync with the wrong argument count, got none")
	}
}

func TestE2EFsReaddirSyncListsEntriesExcludingDotAndDotDot(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("os.WriteFile: %v", err)
		}
	}
	src := fmt.Sprintf(`
import fs from 'fs'
const entries: string[] = fs.readdirSync(%q)
console.log(entries.length)
entries.sort()
for (const e of entries) {
    console.log(e)
}
`, dir)
	assertOutputImports(t, src, "3\na.txt\nb.txt\nc.txt")
}

func TestE2EFsReaddirSyncEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	src := fmt.Sprintf(`
import fs from 'fs'
const entries: string[] = fs.readdirSync(%q)
console.log(entries.length)
`, dir)
	assertOutputImports(t, src, "0")
}

func TestE2EFsReaddirSyncNonexistentThrows(t *testing.T) {
	assertOutputImports(t, `
import fs from 'fs'
try {
    fs.readdirSync("/definitely/does/not/exist/kml-test-dir")
    console.log("should not print")
} catch (e) {
    console.log("caught")
}
`, "caught")
}

func TestE2EFsReaddirSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `import fs from 'fs'
fs.readdirSync()`)
	if err == nil {
		t.Fatal("expected a compile error for fs.readdirSync() with no arguments, got none")
	}
}

// fs.statSync (ADR-00495): size/mtimeMs fields + isFile()/isDirectory()
// over the host's real struct stat offsets; a missing path throws the
// shared catchable fs error. Verified on Mac (Linux offsets differ and are
// encoded per-platform in statLayout).
func TestE2EFsStatSync(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/probe.txt"
	src := fmt.Sprintf(`
import * as fs from 'fs'
fs.writeFileSync("%s", "hello!")
const st = fs.statSync("%s")
console.log(st.size)
console.log(st.isFile())
console.log(st.isDirectory())
console.log(st.mtimeMs > 1500000000000)
console.log(fs.statSync("%s").isDirectory())
try { fs.statSync("%s/absent") } catch (e) { console.log("caught:", e.message.indexOf("cannot stat") > -1) }
`, file, file, dir, dir)
	assertOutputImports(t, src, "6\ntrue\nfalse\ntrue\ntrue\ncaught: true")
}

// fs.statSync full Stats surface (ADR-00565): mode/uid/gid/nlink/ino/blksize/
// dev + atimeMs/ctimeMs beyond the original size/mtimeMs. Verified on Mac
// (Linux offsets differ and are encoded per-platform in statLayout).
func TestE2EFsStatFullSurface(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/probe.txt"
	src := fmt.Sprintf(`
import * as fs from 'fs'
fs.writeFileSync("%s", "abcdef")
const st = fs.statSync("%s")
console.log((st.mode & 0o777) > 0)
console.log(st.uid >= 0)
console.log(st.gid >= 0)
console.log(st.nlink >= 1)
console.log(st.ino > 0)
console.log(st.blksize > 0)
console.log(st.atimeMs >= st.mtimeMs - 1000000000)
console.log(st.ctimeMs > 1500000000000)
console.log(st.size)
`, file, file)
	assertOutputImports(t, src, "true\ntrue\ntrue\ntrue\ntrue\ntrue\ntrue\ntrue\n6")
}

// Path-based fs sync ops (ADR-00497): mkdtempSync/symlinkSync/readlinkSync/
// lstatSync (+isSymbolicLink)/realpathSync/chmodSync/truncateSync/accessSync/
// rmSync (recursive + force). Mac-verified; Linux shares the libc calls but
// the stat offsets carry ADR-00495's Linux-unverified caveat.
func TestE2EFsPathOps(t *testing.T) {
	assertOutputImports(t, `
import * as fs from 'fs'
const tmp = fs.mkdtempSync('/tmp/kmlops-')
console.log(tmp.indexOf('/tmp/kmlops-') === 0)
fs.writeFileSync(tmp + '/a.txt', 'data')
fs.symlinkSync(tmp + '/a.txt', tmp + '/link')
console.log(fs.readlinkSync(tmp + '/link') === tmp + '/a.txt')
console.log(fs.lstatSync(tmp + '/link').isSymbolicLink())
console.log(fs.lstatSync(tmp + '/link').isFile())
console.log(fs.statSync(tmp + '/link').size)
console.log(fs.realpathSync(tmp + '/link').indexOf('a.txt') > -1)
fs.chmodSync(tmp + '/a.txt', 420)
fs.truncateSync(tmp + '/a.txt', 2)
console.log(fs.statSync(tmp + '/a.txt').size)
fs.accessSync(tmp + '/a.txt')
fs.mkdirSync(tmp + '/sub/deep', { recursive: true })
fs.writeFileSync(tmp + '/sub/deep/f.txt', 'x')
fs.rmSync(tmp, { recursive: true, force: true })
console.log(fs.existsSync(tmp))
fs.rmSync('/tmp/kml-definitely-absent-xyz', { force: true })
console.log("force-ok")
try { fs.rmSync('/tmp/kml-definitely-absent-xyz') } catch (e) { console.log("caught:", e.message.indexOf("cannot remove") > -1) }
`, "true\ntrue\ntrue\nfalse\n4\ntrue\n2\nfalse\nforce-ok\ncaught: true")
}

// fd-based fs ops (ADR-00498): openSync (literal flags → host O_* bits) /
// writeSync (string data) / readSync (Uint8Array, offset/length/position) /
// fstatSync / closeSync.
func TestE2EFsFdOps(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/fd.txt"
	src := fmt.Sprintf(`
import * as fs from 'fs'
const wfd = fs.openSync("%s", 'w')
console.log(fs.writeSync(wfd, "hello world"))
fs.closeSync(wfd)
const rfd = fs.openSync("%s", 'r')
console.log(fs.fstatSync(rfd).size)
console.log(fs.fstatSync(rfd).isFile())
const buf = new Uint8Array(5)
console.log(fs.readSync(rfd, buf))
console.log(buf[0])
console.log(fs.readSync(rfd, buf, 0, 5, 6))
console.log(buf[0])
fs.closeSync(rfd)
try { fs.openSync("%s/absent/f", 'r') } catch (e) { console.log("caught:", e.message.indexOf("cannot open") > -1) }
`, p, p, dir)
	assertOutputImports(t, src, "11\n11\ntrue\n5\n104\n5\n119\ncaught: true")
}

// ADR-00684: fs errors carry the Node error code, so `e.code === 'ENOENT'`
// (the canonical fs error idiom) matches.
func TestE2EFsErrorCode(t *testing.T) {
	assertOutputImports(t, `
import { readFileSync } from 'fs'
function main2(): void {
  try {
    readFileSync('/no/such/dir/nope.txt')
  } catch (e: any) {
    console.log(e.code, e.code === 'ENOENT')
  }
}
main2()
`, "ENOENT true")
}
