package tests

import (
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
	assertOutput(t, src, "0\n1\nhello\nhello world\n0")
}

func TestE2EFsWriteFileSyncOverwritesExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	src := fmt.Sprintf(`
fs.writeFileSync(%q, "first")
fs.writeFileSync(%q, "second")
console.log(fs.readFileSync(%q))
`, path, path, path)
	assertOutput(t, src, "second")
}

func TestE2EFsReadFileSyncUntypedInference(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	src := fmt.Sprintf(`
fs.writeFileSync(%q, "abc")
const content = fs.readFileSync(%q)
console.log(content.length)
`, path, path)
	assertOutput(t, src, "3")
}

func TestE2EFsReadFileSyncNonexistentThrows(t *testing.T) {
	src := `
try {
    const content: string = fs.readFileSync("/definitely/does/not/exist/kml-test-file.txt")
    console.log(content)
} catch (e) {
    console.log("caught")
}
`
	assertOutput(t, src, "caught")
}

func TestE2EFsReadFileSyncNonexistentUncaughtExitsNonZero(t *testing.T) {
	_, exitCode := compileAndRunExpectExit(t, `
const content: string = fs.readFileSync("/definitely/does/not/exist/kml-test-file.txt")
console.log(content)
`)
	if exitCode == 0 {
		t.Fatal("expected a non-zero exit code for an uncaught fs.readFileSync failure, got 0")
	}
}

func TestE2EFsUnlinkSyncNonexistentThrows(t *testing.T) {
	src := `
try {
    fs.unlinkSync("/definitely/does/not/exist/kml-test-file.txt")
} catch (e) {
    console.log("caught")
}
`
	assertOutput(t, src, "caught")
}

func TestE2EFsWriteFileSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fs.writeFileSync("a")`)
	if err == nil {
		t.Fatal("expected a compile error for fs.writeFileSync with the wrong argument count, got none")
	}
}

func TestE2EFsReadFileSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fs.readFileSync("a", "b")`)
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
const arr = fs.readFileSyncBytes(%q)
console.log(arr.length)
for (let i = 0; i < arr.length; i++) {
    console.log(arr[i])
}
`, path)
	assertOutput(t, src, "6\n104\n105\n0\n98\n121\n101")
}

func TestE2EFsReadFileSyncBytesNonexistentThrows(t *testing.T) {
	src := `
try {
    const arr = fs.readFileSyncBytes("/definitely/does/not/exist/kml-test-file.bin")
    console.log(arr.length)
} catch (e) {
    console.log("caught")
}
`
	assertOutput(t, src, "caught")
}

func TestE2EFsReadFileSyncBytesWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fs.readFileSyncBytes("a", "b")`)
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
const arr = new Uint8Array([104, 105, 0, 98, 121, 101])
fs.writeFileSync(%q, arr)
`, path)
	assertOutput(t, src, "")

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
const arr = new Uint8Array([1, 0, 2])
fs.writeFileSync(%q, arr)
fs.appendFileSync(%q, arr)
`, path, path)
	assertOutput(t, src, "")

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
const buf = new ArrayBuffer(3)
const view = new Uint8Array(buf)
view[0] = 5
view[1] = 0
view[2] = 6
fs.writeFileSync(%q, buf)
`, path)
	assertOutput(t, src, "")

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
const path: string = %q
fs.writeFileSync(path, "hello")
console.log(fs.readFileSync(path))
fs.appendFileSync(path, " world")
console.log(fs.readFileSync(path))
`, path)
	assertOutput(t, src, "hello\nhello world")
}

// --- fs.mkdirSync / renameSync / copyFileSync / readdirSync ---

func TestE2EFsMkdirSyncCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "newdir")
	src := fmt.Sprintf(`
console.log(fs.existsSync(%q))
fs.mkdirSync(%q)
console.log(fs.existsSync(%q))
`, sub, sub, sub)
	assertOutput(t, src, "0\n1")
}

func TestE2EFsMkdirSyncAlreadyExistsThrows(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "newdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("os.Mkdir(%q): %v", sub, err)
	}
	src := fmt.Sprintf(`
try {
    fs.mkdirSync(%q)
    console.log("should not print")
} catch (e) {
    console.log(e.message.startsWith("cannot create directory '%s': "))
}
`, sub, sub)
	assertOutput(t, src, "1")
}

func TestE2EFsMkdirSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fs.mkdirSync()`)
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
console.log(fs.existsSync(%q))
fs.rmdirSync(%q)
console.log(fs.existsSync(%q))
`, sub, sub, sub)
	assertOutput(t, src, "1\n0")
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
try {
    fs.rmdirSync(%q)
    console.log("should not print")
} catch (e) {
    console.log("caught")
}
`, sub)
	assertOutput(t, src, "caught")
}

func TestE2EFsRmdirSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fs.rmdirSync()`)
	if err == nil {
		t.Fatal("expected a compile error for fs.rmdirSync() with no arguments, got none")
	}
}

func TestE2EFsRenameSyncMovesFile(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(dir, "new.txt")
	src := fmt.Sprintf(`
fs.writeFileSync(%q, "content")
fs.renameSync(%q, %q)
console.log(fs.existsSync(%q))
console.log(fs.existsSync(%q))
console.log(fs.readFileSync(%q))
`, oldPath, oldPath, newPath, oldPath, newPath, newPath)
	assertOutput(t, src, "0\n1\ncontent")
}

func TestE2EFsRenameSyncNonexistentThrows(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "does-not-exist.txt")
	newPath := filepath.Join(dir, "new.txt")
	src := fmt.Sprintf(`
try {
    fs.renameSync(%q, %q)
    console.log("should not print")
} catch (e) {
    console.log("caught")
}
`, oldPath, newPath)
	assertOutput(t, src, "caught")
}

func TestE2EFsRenameSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fs.renameSync("a")`)
	if err == nil {
		t.Fatal("expected a compile error for fs.renameSync with the wrong argument count, got none")
	}
}

func TestE2EFsCopyFileSyncCopiesContent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dest := filepath.Join(dir, "dest.txt")
	code := fmt.Sprintf(`
fs.writeFileSync(%q, "copy me")
fs.copyFileSync(%q, %q)
console.log(fs.existsSync(%q))
console.log(fs.readFileSync(%q))
console.log(fs.readFileSync(%q))
`, src, src, dest, src, src, dest)
	assertOutput(t, code, "1\ncopy me\ncopy me")
}

func TestE2EFsCopyFileSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fs.copyFileSync("a")`)
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
const entries: string[] = fs.readdirSync(%q)
console.log(entries.length)
entries.sort()
for (const e of entries) {
    console.log(e)
}
`, dir)
	assertOutput(t, src, "3\na.txt\nb.txt\nc.txt")
}

func TestE2EFsReaddirSyncEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	src := fmt.Sprintf(`
const entries: string[] = fs.readdirSync(%q)
console.log(entries.length)
`, dir)
	assertOutput(t, src, "0")
}

func TestE2EFsReaddirSyncNonexistentThrows(t *testing.T) {
	assertOutput(t, `
try {
    fs.readdirSync("/definitely/does/not/exist/kml-test-dir")
    console.log("should not print")
} catch (e) {
    console.log("caught")
}
`, "caught")
}

func TestE2EFsReaddirSyncWrongArgCountRejected(t *testing.T) {
	_, err := parseAndCompile(`fs.readdirSync()`)
	if err == nil {
		t.Fatal("expected a compile error for fs.readdirSync() with no arguments, got none")
	}
}
