package tests

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// --- fs (readFileSync/writeFileSync/appendFileSync/existsSync/unlinkSync) ---

func TestE2EFsWriteReadAppendUnlink(t *testing.T) {
	dir := tempDir(t)
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
	dir := tempDir(t)
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
	dir := tempDir(t)
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
fs.readFileSync("a", "utf8", "extra")`)
	if err == nil {
		t.Fatal("expected a compile error for fs.readFileSync with the wrong argument count, got none")
	}
}

// --- fs read/write encoding & flag options (ADR-00785) ---

func TestE2EFsReadWriteEncodingOption(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "enc.txt")
	// The canonical `writeFileSync(p, d, 'utf8')` / `readFileSync(p, 'utf8')`
	// idiom and its `{ encoding: 'utf8' }` object form both compile and behave
	// (strings are already UTF-8, so the encoding is a faithful no-op).
	src := fmt.Sprintf(`
import fs from 'fs'
fs.writeFileSync(%q, "hello", "utf8")
console.log(fs.readFileSync(%q, "utf8"))
fs.appendFileSync(%q, " world", { encoding: "utf8" })
console.log(fs.readFileSync(%q, { encoding: "utf8" }))
`, path, path, path, path)
	assertOutputImports(t, src, "hello\nhello world")
}

func TestE2EFsWriteFileSyncAppendFlag(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "flag.txt")
	// `{ flag: 'a' }` turns writeFileSync into an append (Node semantics);
	// `{ flag: 'w' }` truncates, the default.
	src := fmt.Sprintf(`
import fs from 'fs'
fs.writeFileSync(%q, "a")
fs.writeFileSync(%q, "B", { flag: "a" })
fs.writeFileSync(%q, "C", { flag: "a" })
console.log(fs.readFileSync(%q, "utf8"))
fs.writeFileSync(%q, "reset", { flag: "w" })
console.log(fs.readFileSync(%q, "utf8"))
`, path, path, path, path, path, path)
	assertOutputImports(t, src, "aBC\nreset")
}

func TestE2EFsReadFileSyncBadEncodingRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `import fs from 'fs'
fs.readFileSync("a", "latin1")`)
	if err == nil {
		t.Fatal("expected a compile error for a non-utf8 encoding, got none")
	}
}

func TestE2EFsWriteFileSyncModeOptionRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `import fs from 'fs'
fs.writeFileSync("a", "d", { mode: 384 })`)
	if err == nil {
		t.Fatal("expected a clean rejection for the unsupported mode option, got none")
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
	dir := tempDir(t)
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
	dir := tempDir(t)
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
	dir := tempDir(t)
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
	dir := tempDir(t)
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
	dir := tempDir(t)
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
	dir := tempDir(t)
	sub := dir + "/newdir" // not filepath.Join: it re-backslashes on Windows, and sub is spliced into a TS literal
	src := fmt.Sprintf(`
import fs from 'fs'
console.log(fs.existsSync(%q))
fs.mkdirSync(%q)
console.log(fs.existsSync(%q))
`, sub, sub, sub)
	assertOutputImports(t, src, "false\ntrue")
}

func TestE2EFsMkdirSyncAlreadyExistsThrows(t *testing.T) {
	dir := tempDir(t)
	sub := dir + "/newdir" // not filepath.Join: it re-backslashes on Windows, and sub is spliced into a TS literal
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
	dir := tempDir(t)
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
	dir := tempDir(t)
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
	dir := tempDir(t)
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
	dir := tempDir(t)
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
	dir := tempDir(t)
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
	dir := tempDir(t)
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
	dir := tempDir(t)
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

// ADR-00795: fs.constants exposes the POSIX access modes and the copyFile
// flags as compile-time numeric literals, so code can use the named constants
// (via `fs.constants.X`, a `const c = fs.constants` alias, or a
// `import { constants }` named import) with accessSync/copyFileSync instead of
// raw magic numbers. Values match Node exactly.
func TestE2EFsConstantsValues(t *testing.T) {
	assertOutputImports(t, `
import fs from 'fs'
const c = fs.constants
console.log(fs.constants.F_OK, fs.constants.R_OK, fs.constants.W_OK, fs.constants.X_OK)
console.log(c.COPYFILE_EXCL, c.COPYFILE_FICLONE, c.COPYFILE_FICLONE_FORCE)
`, "0 4 2 1\n1 2 4")
}

func TestE2EFsConstantsNamedImportAndConsumers(t *testing.T) {
	dir := tempDir(t)
	src := filepath.Join(dir, "src.txt")
	dest := filepath.Join(dir, "dest.txt")
	code := fmt.Sprintf(`
import { constants, writeFileSync, accessSync, copyFileSync, existsSync } from 'fs'
writeFileSync(%q, "hi")
accessSync(%q, constants.R_OK)
console.log("readable")
copyFileSync(%q, %q, constants.COPYFILE_EXCL)
console.log("copied", existsSync(%q))
try {
  copyFileSync(%q, %q, constants.COPYFILE_EXCL)
  console.log("overwrote")
} catch (e) {
  console.log("EXCL blocked")
}
`, src, src, src, dest, dest, src, dest)
	assertOutputImports(t, code, "readable\ncopied true\nEXCL blocked")
}

func TestE2EFsConstantsUnknownMemberRejected(t *testing.T) {
	_, err := parseAndCompile(`
import fs from 'fs'
console.log(fs.constants.O_RDWR)
`)
	if err == nil {
		t.Fatal("expected a compile error for an unsupported fs.constants member, got none")
	}
}

func TestE2EFsCopyFileSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `import fs from 'fs'
fs.copyFileSync("a")`)
	if err == nil {
		t.Fatal("expected a compile error for fs.copyFileSync with the wrong argument count, got none")
	}
}

// copyFileSync's mode argument: COPYFILE_EXCL (1) fails with EEXIST if dest
// already exists; without it (or mode 0) the copy overwrites (ADR-00788).
func TestE2EFsCopyFileSyncModeExcl(t *testing.T) {
	dir := tempDir(t)
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	prog := fmt.Sprintf(`
import fs from 'fs'
fs.copyFileSync(%q, %q)                       // create
fs.copyFileSync(%q, %q, 0)                     // overwrite ok (mode 0)
console.log(fs.readFileSync(%q, 'utf8'))
try {
  fs.copyFileSync(%q, %q, 1)                   // COPYFILE_EXCL → EEXIST
  console.log('NO THROW')
} catch (e) {
  console.log('excl:' + (e as any).code)
}
`, src, dst, src, dst, dst, src, dst)
	assertOutputImports(t, prog, "hello\nexcl:EEXIST")
}

func TestE2EFsReadlinkSyncLongTarget(t *testing.T) {
	// The old readlink path capped the target at 4096 bytes; the grow loop
	// handles any length (ADR-00788). 500 chars exercises the 256→512 grow and
	// stays under macOS's own symlink-target limit.
	dir := tempDir(t)
	link := filepath.Join(dir, "l")
	target := ""
	for i := 0; i < 500; i++ {
		target += "z"
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	src := fmt.Sprintf(`
import fs from 'fs'
const s = fs.readlinkSync(%q)
console.log(s.length)
`, link)
	assertOutputImports(t, src, "500")
}

func TestE2EFsFutimesSync(t *testing.T) {
	// futimesSync sets atime/mtime on an open fd (the fd-based utimesSync).
	dir := tempDir(t)
	path := filepath.Join(dir, "t.txt")
	src := fmt.Sprintf(`
import fs from 'fs'
fs.writeFileSync(%q, "x")
const fd = fs.openSync(%q, "r+")
fs.futimesSync(fd, 1000000, 2000000)
fs.closeSync(fd)
console.log(Math.round(fs.statSync(%q).mtimeMs / 1000))
`, path, path, path)
	assertOutputImports(t, src, "2000000")
}

func TestE2EFsReaddirSyncListsEntriesExcludingDotAndDotDot(t *testing.T) {
	dir := tempDir(t)
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
	dir := tempDir(t)
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

// fs.readdirSync(path, { withFileTypes: true }) returns Dirent[] — each with
// a `name` and isFile()/isDirectory()/isSymbolicLink(), classified from the
// dirent d_type (from FindFirstFile attributes on Windows). `mode` is a
// hidden backing field, not a JSON/enumerable property (ADR-00752).
func TestE2EFsReaddirSyncWithFileTypes(t *testing.T) {
	dir := tempDir(t)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`
import * as fs from 'fs'
const ents = fs.readdirSync("%s", { withFileTypes: true })
const rows: string[] = []
for (const e of ents) {
  rows.push(e.name + ':' + (e.isDirectory() ? 'dir' : e.isFile() ? 'file' : '?'))
}
rows.sort()
console.log(rows.join(', '))
console.log(JSON.stringify(ents[0]).indexOf('mode') === -1 ? 'no-mode-leak' : 'LEAK')
`, dir)
	assertOutputImports(t, src, "d:dir, f.txt:file\nno-mode-leak")
}

// Dirent.parentPath (the directory the entry was read from) and the device
// kind predicates isFIFO/isCharacterDevice/isBlockDevice/isSocket (ADR-00787).
func TestE2EFsReaddirSyncDirentParentPathAndDevicePredicates(t *testing.T) {
	dir := tempDir(t)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`
import * as fs from 'fs'
const ents = fs.readdirSync(%q, { withFileTypes: true })
const rows: string[] = []
for (const e of ents) {
  const kind = e.isFIFO() || e.isCharacterDevice() || e.isBlockDevice() || e.isSocket() ? 'dev'
    : e.isDirectory() ? 'dir' : e.isFile() ? 'file' : '?'
  rows.push(e.name + ':' + kind + ':' + (e.parentPath === %q ? 'parent-ok' : 'BAD'))
}
rows.sort()
console.log(rows.join(', '))
// parentPath is own-enumerable (like name); mode stays hidden.
console.log(JSON.stringify(ents[0]).indexOf('parentPath') !== -1 ? 'parentPath-enumerable' : 'MISSING')
console.log(JSON.stringify(ents[0]).indexOf('mode') === -1 ? 'no-mode-leak' : 'LEAK')
`, dir, dir)
	assertOutputImports(t, src, "d:dir:parent-ok, f.txt:file:parent-ok\nparentPath-enumerable\nno-mode-leak")
}

func TestE2EFsReaddirSyncWithFileTypesBadOptionRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `import fs from 'fs'
fs.readdirSync('.', { encoding: 'buffer' })`)
	if err == nil {
		t.Fatal("expected a compile error for an unsupported readdirSync option, got none")
	}
}

// fs.readdirSync(path, { recursive: true }) — every nested entry as a string[]
// of "/"-joined paths relative to the start dir (ADR-00786).
func TestE2EFsReaddirSyncRecursive(t *testing.T) {
	dir := tempDir(t)
	for _, d := range []string{"sub/deep", "other"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
	for _, f := range []string{"a.txt", "sub/b.txt", "sub/deep/c.txt", "other/d.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	src := fmt.Sprintf(`
import fs from 'fs'
const all = fs.readdirSync(%q, { recursive: true })
console.log(all.length)
for (const e of all.sort()) console.log(e)
`, dir)
	assertOutputImports(t, src, "7\na.txt\nother\nother/d.txt\nsub\nsub/b.txt\nsub/deep\nsub/deep/c.txt")
}

func TestE2EFsReaddirSyncRecursiveWithFileTypesRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `import fs from 'fs'
fs.readdirSync('.', { recursive: true, withFileTypes: true })`)
	if err == nil {
		t.Fatal("expected recursive + withFileTypes together to be a clean rejection, got none")
	}
}

// fs.statSync (ADR-00495): size/mtimeMs fields + isFile()/isDirectory()
// over the host's real struct stat offsets; a missing path throws the
// shared catchable fs error. Verified on Mac (Linux offsets differ and are
// encoded per-platform in statLayout).
func TestE2EFsStatSync(t *testing.T) {
	dir := tempDir(t)
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
	dir := tempDir(t)
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
	if runtime.GOOS == "windows" {
		// Creating a symlink needs Developer Mode or an elevated token on
		// Windows (Node throws EPERM otherwise, exactly as this compiler does);
		// probe from Go and skip when the box can't, rather than fail.
		probe := tempDir(t)
		if err := os.Symlink(probe, filepath.Join(probe, "probe-link")); err != nil {
			t.Skip("symlink creation not permitted on this Windows box (needs Developer Mode or admin)")
		}
	}
	assertOutputImports(t, `
import * as fs from 'fs'
import os from 'os'
const base = os.tmpdir() + '/kmlops-'
const tmp = fs.mkdtempSync(base)
console.log(tmp.indexOf(base) === 0)
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
fs.rmSync(os.tmpdir() + '/kml-definitely-absent-xyz', { force: true })
console.log("force-ok")
try { fs.rmSync(os.tmpdir() + '/kml-definitely-absent-xyz') } catch (e) { console.log("caught:", e.message.indexOf("cannot remove") > -1) }
`, "true\ntrue\ntrue\nfalse\n4\ntrue\n2\nfalse\nforce-ok\ncaught: true")
}

// ADR-00769: lstat on a symlink reports the *link's own* size — the byte length
// of its target path (POSIX semantics, matching libuv/Node) — not the target
// file's size (that's statSync, which follows) nor 0 (the Windows reparse
// point's on-disk size). Verified against the target string's length so it holds
// on every host regardless of the tmp path.
func TestE2EFsLstatSymlinkSize(t *testing.T) {
	if runtime.GOOS == "windows" {
		probe := tempDir(t)
		if err := os.Symlink(probe, filepath.Join(probe, "probe-link")); err != nil {
			t.Skip("symlink creation not permitted on this Windows box (needs Developer Mode or admin)")
		}
	}
	assertOutputImports(t, `
import * as fs from 'fs'
import os from 'os'
const tmp = fs.mkdtempSync(os.tmpdir() + '/kmllsz-')
fs.writeFileSync(tmp + '/target.txt', 'hello world')
const target = tmp + '/target.txt'
fs.symlinkSync(target, tmp + '/link')
// lstat: the link's own size == byte length of the target path string.
console.log(fs.lstatSync(tmp + '/link').size === target.length)
// stat follows the link: the target file's 11 bytes ('hello world').
console.log(fs.statSync(tmp + '/link').size)
fs.rmSync(tmp, { recursive: true, force: true })
`, "true\n11")
}

// fs.linkSync(existing, newPath) — a hard link: both names point at one
// inode, so a write through either is visible through the other and the
// content survives unlinking the original. Unlike symlinkSync, this needs no
// Developer Mode on Windows (CreateHardLinkW is unprivileged).
// fs.watch (TDD-00181, ADR-00756/ADR-00757/ADR-00758) — Linux inotify, Windows
// ReadDirectoryChangesW, and macOS kqueue/EVFILT_VNODE backends. It exercises the
// whole subsystem on every platform: an FSWatcher folded into the event loop, a
// real file change delivered as a 'change' event, then close() letting the loop
// exit. (macOS kqueue watches an fd, so the delivered `filename` is the watched
// path rather than the changed entry — ADR-00758; this test only checks `evt`.)
func TestE2EFsWatchChange(t *testing.T) {
	dir := tempDir(t)
	p := dir + "/w.txt"
	src := fmt.Sprintf(`
import * as fs from 'fs'
fs.writeFileSync("%s", "a")
const w = fs.watch("%s", (evt: string, name: string) => {
  console.log("event=" + evt)
  w.close()
})
setTimeout(() => { fs.writeFileSync("%s", "bb") }, 150)
`, p, p, p)
	assertOutputImports(t, src, "event=change")
}

func TestE2EFsLinkSync(t *testing.T) {
	dir := tempDir(t)
	a := dir + "/orig.txt"
	b := dir + "/hard.txt"
	src := fmt.Sprintf(`
import * as fs from 'fs'
fs.writeFileSync("%s", "shared")
fs.linkSync("%s", "%s")
console.log(fs.readFileSync("%s"))
console.log(fs.statSync("%s").nlink)
fs.writeFileSync("%s", "CHANGED")
console.log(fs.readFileSync("%s"))
fs.unlinkSync("%s")
console.log(fs.readFileSync("%s"))
try { fs.linkSync("%s/absent", "%s/x") } catch (e: any) { console.log("caught:", e.code) }
`, a, a, b, b, b, a, b, a, b, dir, dir)
	assertOutputImports(t, src, "shared\n2\nCHANGED\nCHANGED\ncaught: ENOENT")
}

// fd-based fs ops (ADR-00498): openSync (literal flags → host O_* bits) /
// writeSync (string data) / readSync (Uint8Array, offset/length/position) /
// fstatSync / closeSync.
func TestE2EFsFdOps(t *testing.T) {
	dir := tempDir(t)
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

// fstatSync on a non-file fd — here stdin, a pipe (the test harness feeds it
// through an OS pipe) — reports the handle kind without error. On Windows this
// exercises TDD-00182 Stage 1's kind dispatch: fd 0's handle answers
// GetFileInformationByHandle with a failure, which used to surface as an error;
// fstat now synthesizes the mode from the handle kind (FILE_TYPE_PIPE →
// S_IFIFO), matching POSIX fstat on a pipe.
func TestE2EFsFstatPipeFd(t *testing.T) {
	src := `
import * as fs from 'fs'
const s = fs.fstatSync(0)
console.log(s.isFIFO())
console.log(s.isFile())
`
	got := runImportsStdin(t, src, "ignored")
	compareLines(t, got, "true\nfalse")
}

// fs.utimesSync(path, atime, mtime) sets the access/modification times from
// a number (seconds, as Node) or a Date (its epoch). On Windows it is a
// SetFileTime shim; on POSIX, utimes(2). statSync reads them back in ms.
func TestE2EFsUtimesSync(t *testing.T) {
	dir := tempDir(t)
	p := dir + "/t.txt"
	src := fmt.Sprintf(`
import * as fs from 'fs'
fs.writeFileSync("%s", "x")
fs.utimesSync("%s", 1000000000, 1500000000)
const s = fs.statSync("%s")
console.log(s.atimeMs)
console.log(s.mtimeMs)
fs.utimesSync("%s", new Date(1600000000000), new Date(1700000000000))
const s2 = fs.statSync("%s")
console.log(s2.mtimeMs)
try { fs.utimesSync("%s/nope", 1, 1) } catch (e: any) { console.log("caught:", e.code) }
`, p, p, p, p, p, dir)
	assertOutputImports(t, src, "1000000000000\n1500000000000\n1700000000000\ncaught: ENOENT")
}

// fd-based durability + resize ops: fsyncSync (flush to disk) and
// ftruncateSync (shrink/grow/default-0). On Windows these are Win32 shims
// (FlushFileBuffers / SetEndOfFile); on POSIX they are the fsync/ftruncate
// syscalls. Both throw a coded Error on a bad fd.
func TestE2EFsFsyncFtruncate(t *testing.T) {
	dir := tempDir(t)
	p := dir + "/dur.txt"
	src := fmt.Sprintf(`
import * as fs from 'fs'
const fd = fs.openSync("%s", 'w')
fs.writeSync(fd, "hello world")
fs.fsyncSync(fd)
fs.ftruncateSync(fd, 5)
fs.fsyncSync(fd)
fs.closeSync(fd)
console.log(fs.statSync("%s").size)
console.log(fs.readFileSync("%s"))
const g = fs.openSync("%s", 'r+')
fs.ftruncateSync(g, 8)
fs.closeSync(g)
console.log(fs.statSync("%s").size)
const z = fs.openSync("%s", 'r+')
fs.ftruncateSync(z)
fs.closeSync(z)
console.log(fs.statSync("%s").size)
try { fs.fsyncSync(9999) } catch (e: any) { console.log("fsync:", e.code) }
try { fs.ftruncateSync(9999, 3) } catch (e: any) { console.log("ftrunc:", e.code) }
`, p, p, p, p, p, p, p)
	assertOutputImports(t, src, "5\nhello\n8\n0\nfsync: EBADF\nftrunc: EBADF")
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

// ADR-00768: fs errors also carry Node's `err.syscall` (the bare syscall name)
// and `err.path` (the offending path); a plain non-fs Error leaves both null.
func TestE2EFsErrorSyscallAndPath(t *testing.T) {
	assertOutputImports(t, `
import { readFileSync, statSync, unlinkSync, mkdirSync, readdirSync } from 'fs'
function probe(label: string, fn: () => void): void {
  try { fn() } catch (e: any) {
    console.log(label + ' ' + e.syscall + ' ' + e.path)
  }
}
function main2(): void {
  probe('read', () => { readFileSync('/no/such/f.txt') })
  probe('stat', () => { statSync('/no/such/f') })
  probe('unlink', () => { unlinkSync('/no/such/f') })
  probe('mkdir', () => { mkdirSync('/no/deep/f') })
  probe('scandir', () => { readdirSync('/no/such/f') })
  try { throw new Error('plain') } catch (e: any) {
    console.log('plain ' + e.syscall + ' ' + e.path)
  }
}
main2()
`, `read open /no/such/f.txt
stat stat /no/such/f
unlink unlink /no/such/f
mkdir mkdir /no/deep/f
scandir scandir /no/such/f
plain null null`)
}

// ADR-00770: fs errors carry Node's numeric `err.errno` — the negative
// libuv-style errno (ENOENT → -2). On POSIX this equals Node exactly; on Windows
// the port normalizes errno to the Linux numbers, so it is the negated Linux
// value (a documented divergence from Node's Windows UV_E* numbering). A plain
// non-fs Error reads 0 (the falsy default; Node reports undefined).
func TestE2EFsErrorErrno(t *testing.T) {
	assertOutputImports(t, `
import { readFileSync } from 'fs'
function main2(): void {
  try {
    readFileSync('/no/such/dir/nope.txt')
  } catch (e: any) {
    console.log(e.errno, e.errno === -2)
  }
  try { throw new Error('plain') } catch (e: any) { console.log(e.errno) }
}
main2()
`, "-2 true\n0")
}
