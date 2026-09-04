package tests

import (
	"fmt"
	"testing"
)

// ADR-00692: fs.readFileSync on a directory path must throw EISDIR, matching
// Node ("EISDIR: illegal operation on a directory, read"). fopen(2) happily
// opens a directory on both Linux and macOS and then returns garbage/zero
// bytes; the fix guards the read path with stat(2) up front, sets errno to
// EISDIR, and routes through the shared fs error path so `.code === 'EISDIR'`.
func TestE2EFsReadFileSyncOnDirectoryThrowsEISDIR(t *testing.T) {
	dir := t.TempDir()
	src := fmt.Sprintf(`
import fs from 'fs'
try {
    const content: string = fs.readFileSync(%q)
    console.log("NO THROW len=" + content.length)
} catch (e) {
    console.log((e as any).code)
}
`, dir)
	assertOutputImports(t, src, "EISDIR")
}

// The thrown value is a catchable Error carrying the EISDIR code AND a
// non-empty message — the full 6-field error object, not a bare code.
func TestE2EFsReadFileSyncOnDirectoryErrorShape(t *testing.T) {
	dir := t.TempDir()
	src := fmt.Sprintf(`
import fs from 'fs'
try {
    fs.readFileSync(%q)
} catch (e) {
    console.log((e as any).code === "EISDIR")
    console.log((e as any).message.length > 0)
}
`, dir)
	assertOutputImports(t, src, "true\ntrue")
}
