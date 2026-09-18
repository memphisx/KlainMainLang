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

// ADR-01000: fs sync-error `.message` is now byte-exact to Node's
// `<CODE>: <libuv-desc>, <syscall>[ '<path>'[ -> '<dest>']]` form (built by
// __kml_fs_errmsg from __kml_errno_desc's libuv description table), not the old
// "<opdesc> '<path>': <strerror>" shape. These assert the exact string against a
// fixed nonexistent path so the leading code token, the libuv description, the
// syscall name, and the conditional path/dest clauses all match.

// ENOENT with a path: the single-path clause is present, in quotes.
func TestE2EFsErrorMessageENOENTWithPath(t *testing.T) {
	assertOutputImports(t, `
import * as fs from 'fs'
try { fs.readFileSync('/no_such_kml_dir/f.txt') }
catch (e) { const x = e as any; console.log(x.message + '|' + x.code + '|' + x.syscall + '|' + x.path) }
`, "ENOENT: no such file or directory, open '/no_such_kml_dir/f.txt'|ENOENT|open|/no_such_kml_dir/f.txt")
}

// EISDIR reading a directory: Node emits NO path clause and `err.path` is
// undefined (the `read` syscall carries no path in its message).
func TestE2EFsErrorMessageEISDIRNoPath(t *testing.T) {
	assertOutputImports(t, `
import * as fs from 'fs'
import os from 'os'
try { fs.readFileSync(os.tmpdir()) }
catch (e) { const x = e as any; console.log(x.message + '|' + x.code + '|' + x.syscall + '|' + (x.path == null)) }
`, "EISDIR: illegal operation on a directory, read|EISDIR|read|true")
}

// EBADF on an fd op: no path clause, `err.path` undefined, syscall is the op.
func TestE2EFsErrorMessageEBADFNoPath(t *testing.T) {
	assertOutputImports(t, `
import * as fs from 'fs'
try { fs.fsyncSync(99999) }
catch (e) { const x = e as any; console.log(x.message + '|' + x.code + '|' + x.syscall + '|' + (x.path == null)) }
`, "EBADF: bad file descriptor, fsync|EBADF|fsync|true")
}

// A two-path op (rename): the message carries `'<src>' -> '<dest>'`, and both
// `err.path` (source) and `err.dest` (destination) are set, as in Node.
func TestE2EFsErrorMessageRenameTwoPath(t *testing.T) {
	assertOutputImports(t, `
import * as fs from 'fs'
try { fs.renameSync('/no_such_kml_dir/a', '/no_such_kml_dir/b') }
catch (e) { const x = e as any; console.log(x.message + '|' + x.syscall + '|' + x.path + '|' + x.dest) }
`, "ENOENT: no such file or directory, rename '/no_such_kml_dir/a' -> '/no_such_kml_dir/b'|rename|/no_such_kml_dir/a|/no_such_kml_dir/b")
}

// copyFileSync surfaces the two-path `copyfile` error (not the underlying
// `open` on one path): a missing source reports syscall `copyfile` with both
// paths and `err.dest` set.
func TestE2EFsErrorMessageCopyFileTwoPath(t *testing.T) {
	assertOutputImports(t, `
import * as fs from 'fs'
try { fs.copyFileSync('/no_such_kml_dir/a', '/no_such_kml_dir2/b') }
catch (e) { const x = e as any; console.log(x.message + '|' + x.code + '|' + x.syscall + '|' + x.path + '|' + x.dest) }
`, "ENOENT: no such file or directory, copyfile '/no_such_kml_dir/a' -> '/no_such_kml_dir2/b'|ENOENT|copyfile|/no_such_kml_dir/a|/no_such_kml_dir2/b")
}

// unlink of a file the program itself holds open succeeds on every host: on
// Windows too, now that open()/fopen() include FILE_SHARE_DELETE in the
// share mode (ADR-00743) — matching POSIX and Node, where an open file can
// be unlinked and the name disappears while the fd stays valid.
func TestE2EFsUnlinkSyncOpenFileSharingViolation(t *testing.T) {
	want := "unlinked"
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
