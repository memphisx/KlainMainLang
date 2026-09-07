package tests

import (
	"os"
	"runtime"
	"testing"
)

// ADR-00736: Windows fs errno fidelity (three rows from the TDD-00180 §4
// audit). Node on Windows reports EPERM for unlink of a directory and for
// access(W_OK) on a read-only file, and EBUSY for a sharing violation; the
// Win32 layer previously reported the Linux-shaped EISDIR/EACCES/EACCES.

// unlink of a directory: EPERM on Windows (libuv) and macOS (BSD unlink),
// EISDIR on Linux — Node differs per host the same way.
func TestE2EFsUnlinkSyncOnDirectoryCode(t *testing.T) {
	want := "EISDIR"
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		want = "EPERM"
	}
	assertOutputImports(t, `
import * as fs from 'fs'
import os from 'os'
const tmp = fs.mkdtempSync(os.tmpdir() + '/kmlunl-')
try { fs.unlinkSync(tmp) } catch (e) { console.log((e as any).code) }
fs.rmSync(tmp, { recursive: true, force: true })
console.log(fs.existsSync(tmp))
`, want+"\nfalse")
}

// access(W_OK) on a read-only file: EPERM on Windows (libuv's fs__access),
// EACCES on POSIX hosts (where root would instead succeed — skipped there).
func TestE2EFsAccessSyncReadOnlyWOKCode(t *testing.T) {
	want := "EACCES"
	if runtime.GOOS == "windows" {
		want = "EPERM"
	} else if os.Geteuid() == 0 {
		t.Skip("access(W_OK) on a read-only file succeeds for root")
	}
	assertOutputImports(t, `
import * as fs from 'fs'
import os from 'os'
const tmp = fs.mkdtempSync(os.tmpdir() + '/kmlacc-')
const p = tmp + '/ro.txt'
fs.writeFileSync(p, 'x')
fs.chmodSync(p, 256)
try { fs.accessSync(p, 2) } catch (e) { console.log((e as any).code) }
fs.chmodSync(p, 438)
fs.rmSync(tmp, { recursive: true, force: true })
console.log(fs.existsSync(tmp))
`, want+"\nfalse")
}

// unlink of a file the program itself holds open: succeeds on POSIX; on
// Windows the open CRT fd lacks FILE_SHARE_DELETE, so DeleteFileW fails with
// a sharing violation, which Node surfaces as EBUSY (not EACCES). When
// FILE_SHARE_DELETE lands (TDD-00180 §4, first row) the Windows expectation
// here becomes 'unlinked' too — update this test alongside that change.
func TestE2EFsUnlinkSyncOpenFileSharingViolation(t *testing.T) {
	want := "unlinked"
	if runtime.GOOS == "windows" {
		want = "EBUSY"
	}
	assertOutputImports(t, `
import * as fs from 'fs'
import os from 'os'
const tmp = fs.mkdtempSync(os.tmpdir() + '/kmlbusy-')
const p = tmp + '/f.txt'
fs.writeFileSync(p, 'x')
const fd = fs.openSync(p, 'r')
try { fs.unlinkSync(p); console.log('unlinked') } catch (e) { console.log((e as any).code) }
fs.closeSync(fd)
fs.rmSync(tmp, { recursive: true, force: true })
console.log(fs.existsSync(tmp))
`, want+"\nfalse")
}
