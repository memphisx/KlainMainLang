//go:build windows

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The rows of the Windows faithfulness audit (TDD-00180 §4/§6) whose fix is
// only observable on a real Windows filesystem.

// An unlinked name is gone at once even while a descriptor still holds the file
// open (POSIX-semantics delete), so it can be recreated immediately — with a
// classic DeleteFileW the name lingers delete-pending and the write fails.
func TestE2EWinUnlinkOpenFileThenRecreate(t *testing.T) {
	dir := tempDir(t)
	p := filepath.ToSlash(filepath.Join(dir, "held.txt"))
	assertOutputImports(t, `
import { openSync, closeSync, unlinkSync, writeFileSync, readFileSync, existsSync } from 'fs'
const p = "`+p+`"
writeFileSync(p, 'old')
const fd = openSync(p, 'r')
unlinkSync(p)
console.log(existsSync(p))
writeFileSync(p, 'new')
console.log(readFileSync(p, 'utf8'))
closeSync(fd)
console.log(readFileSync(p, 'utf8'))
`, "false\nnew\nnew")
}

// rename replaces a target that another descriptor holds open.
func TestE2EWinRenameOverOpenTarget(t *testing.T) {
	dir := tempDir(t)
	a := filepath.ToSlash(filepath.Join(dir, "a.txt"))
	b := filepath.ToSlash(filepath.Join(dir, "b.txt"))
	assertOutputImports(t, `
import { openSync, closeSync, renameSync, writeFileSync, readFileSync, existsSync } from 'fs'
writeFileSync("`+a+`", 'A')
writeFileSync("`+b+`", 'B')
const fd = openSync("`+b+`", 'r')
renameSync("`+a+`", "`+b+`")
console.log(readFileSync("`+b+`", 'utf8'), existsSync("`+a+`"))
closeSync(fd)
`, "A false")
}

// stat.blocks is what the file occupies on disk (allocation size / 512), so it
// covers the whole cluster-rounded length, not just ceil(size / 512).
func TestE2EWinStatBlocksIsAllocationSize(t *testing.T) {
	dir := tempDir(t)
	p := filepath.ToSlash(filepath.Join(dir, "blocks.bin"))
	assertOutputImports(t, `
import { writeFileSync, statSync } from 'fs'
writeFileSync("`+p+`", 'x'.repeat(100001))
const st = statSync("`+p+`")
console.log(st.blksize, st.blocks * 512 >= st.size, st.blocks % 8 === 0)
`, "4096 true true")
}

// process.chdir maintains the hidden per-drive "=X:" variable, and process.env
// is the live Win32 block, so a drive-relative path resolves against the
// directory this process left that drive in.
func TestE2EWinChdirWritesDriveVariable(t *testing.T) {
	dir, err := filepath.EvalSymlinks(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	drive := strings.ToUpper(filepath.VolumeName(dir))
	if len(drive) != 2 {
		t.Skipf("temp dir is not on a drive letter: %q", dir)
	}
	assertOutputImports(t, `
import path from 'path'
process.chdir("`+filepath.ToSlash(dir)+`")
console.log(process.env["=`+drive+`"] === process.cwd())
console.log(path.resolve("`+drive+`sub") === path.join(process.cwd(), "sub"))
`, "true\ntrue")
}

// process.env round-trips a non-ASCII value as UTF-8 (the wide environment
// API), for a variable inherited from the parent and for one written here.
func TestE2EWinProcessEnvUnicodeAndCase(t *testing.T) {
	os.Setenv("KML_UNI_IN", "καλημέρα-日本")
	defer os.Unsetenv("KML_UNI_IN")
	assertOutputImports(t, `
console.log(process.env.KML_UNI_IN)
process.env.KML_UNI_OUT = "naïve-Ω"
console.log(process.env.KML_UNI_OUT, process.env.kml_uni_out)
delete process.env.KML_UNI_OUT
console.log(process.env.KML_UNI_OUT)
`, "καλημέρα-日本\nnaïve-Ω naïve-Ω\nundefined")
}

// symlinkSync(target, path, 'junction') makes an NTFS junction — a directory
// link that needs no privilege (so this runs without Developer Mode, unlike a
// symlink). It reads back as a symbolic link to the absolute target, resolves
// through, and unlinks without touching the target. A relative target is
// resolved against the link's own directory. readlink gives the target
// without the separator a junction is stored with, and a junction whose
// target is gone does not exist (existsSync follows it, as Node does).
func TestE2EWinSymlinkJunction(t *testing.T) {
	dir, err := filepath.EvalSymlinks(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	d := filepath.ToSlash(dir)
	assertOutputImports(t, `
import { mkdirSync, writeFileSync, readFileSync, symlinkSync, lstatSync, readlinkSync, unlinkSync, existsSync } from 'fs'
import path from 'path'
const root = "`+d+`"
mkdirSync(root + "/real")
writeFileSync(root + "/real/f.txt", "through")
symlinkSync(root + "/real", root + "/abs-j", 'junction')
symlinkSync("real", root + "/rel-j", 'junction')
console.log(lstatSync(root + "/abs-j").isSymbolicLink(), lstatSync(root + "/abs-j").isDirectory())
console.log(readFileSync(root + "/abs-j/f.txt", 'utf8'), readFileSync(root + "/rel-j/f.txt", 'utf8'))
const want = path.resolve(root, "real")
console.log(readlinkSync(root + "/abs-j") === want, readlinkSync(root + "/rel-j") === want)
unlinkSync(root + "/abs-j")
console.log(existsSync(root + "/abs-j"), existsSync(root + "/real/f.txt"))
symlinkSync(root + "/gone", root + "/dangling-j", 'junction')
console.log(lstatSync(root + "/dangling-j").isSymbolicLink(), existsSync(root + "/dangling-j"))
`, "true false\nthrough through\ntrue true\nfalse true\ntrue false")
}

// copyFileSync is the OS copy on Windows, so the destination carries the
// source's modification time and attributes (a read-only source yields a
// read-only copy, as in Node) — a read-then-write stamps "now" and drops both.
func TestE2EWinCopyFileCarriesMetadata(t *testing.T) {
	dir := tempDir(t)
	src := filepath.ToSlash(filepath.Join(dir, "src.bin"))
	dst := filepath.ToSlash(filepath.Join(dir, "dst.bin"))
	assertOutputImports(t, `
import { writeFileSync, copyFileSync, statSync, utimesSync, chmodSync, readFileSync, constants } from 'fs'
writeFileSync("`+src+`", "abc-payload")
utimesSync("`+src+`", 1000000000, 1000000000)
chmodSync("`+src+`", 0o444)
copyFileSync("`+src+`", "`+dst+`")
const s = statSync("`+dst+`")
console.log(s.size, s.mtimeMs === 1000000000000, (s.mode & 0o222) === 0)
try { copyFileSync("`+src+`", "`+dst+`", constants.COPYFILE_EXCL) } catch (e: any) { console.log(e.code, e.syscall) }
chmodSync("`+dst+`", 0o666)
chmodSync("`+src+`", 0o666)
`, "11 true true\nEEXIST copyfile")
}

// A Win32 error libuv has no errno for surfaces as Node's UNKNOWN, and access
// denied is EPERM (libuv's mapping), not the POSIX-flavoured EACCES.
func TestE2EWinAccessDeniedIsEPERM(t *testing.T) {
	dir := tempDir(t)
	sub := filepath.ToSlash(filepath.Join(dir, "adir"))
	assertOutputImports(t, `
import { mkdirSync, readFileSync, writeFileSync } from 'fs'
mkdirSync("`+sub+`")
try { writeFileSync("`+sub+`", 'x') } catch (e: any) { console.log(e.code, e.errno) }
`, "EISDIR -4068")
}

// fs.realpathSync resolves the way Node's readlink-based walk does, not only
// the way the kernel's final-path query does (ADR-01058): a relative link
// target stored with forward slashes (which the kernel will not follow — Node
// can't `open` it either, yet its realpath resolves it), a directory link with
// a trailing component, a link chain, `..`/`.` segments, a link loop (ELOOP),
// a dangling link and a missing path (ENOENT). The same walk is what serves a
// volume without a DOS device name (a RAM disk), where the final-path query
// fails outright — run with KML_SCRATCH on such a drive to exercise that leg.
func TestE2EWinRealpathWalksLinks(t *testing.T) {
	probe := tempDir(t)
	if err := os.Symlink(probe, filepath.Join(probe, "probe-link")); err != nil {
		t.Skip("symlink creation not permitted on this Windows box (needs Developer Mode or admin)")
	}
	// realpathSync canonicalizes 8.3 short components to their long form (as Node
	// does — the runner's TMP arrives as C:\Users\RUNNER~1\...), so the expected
	// path must be canonicalized the same way. EvalSymlinks resolves the short
	// name; the sibling tests above do the same.
	real, err := filepath.EvalSymlinks(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.ToSlash(real)
	want := filepath.FromSlash(dir) + `\w\dir\sub\f.txt`
	assertOutputImports(t, `
import * as fs from 'fs'
const tmp = "`+dir+`";
fs.mkdirSync(tmp + "/w/dir/sub", { recursive: true });
fs.writeFileSync(tmp + "/w/dir/sub/f.txt", "x");
fs.symlinkSync("dir/sub/f.txt", tmp + "/w/rel");
fs.symlinkSync(tmp + "/w/dir", tmp + "/w/dlink", "dir");
fs.symlinkSync("rel", tmp + "/w/chain");
fs.symlinkSync("loopb", tmp + "/w/loopa");
fs.symlinkSync("loopa", tmp + "/w/loopb");
fs.symlinkSync("nowhere", tmp + "/w/dangling");
console.log(fs.realpathSync(tmp + "/w/rel"));
console.log(fs.realpathSync(tmp + "/w/dlink/sub/f.txt"));
console.log(fs.realpathSync(tmp + "/w/chain"));
console.log(fs.realpathSync(tmp + "/w/dir/../dir/sub/./f.txt"));
for (const p of ["/w/loopa", "/w/dangling", "/w/missing"]) {
  try { fs.realpathSync(tmp + p); console.log("no throw"); } catch (e) { console.log((e as any).code); }
}
`, strings.Repeat(want+"\n", 4)+"ELOOP\nENOENT\nENOENT")
}

// copyFile onto a read-only destination is refused with EPERM and leaves the
// destination as it was, as in Node — sync, callback and promise forms.
func TestE2EWinCopyFileReadOnlyDestinationEPERM(t *testing.T) {
	dir := tempDir(t)
	d := filepath.ToSlash(dir)
	assertOutputImports(t, `
import fs from 'fs'
const src = "`+d+`/src", dst = "`+d+`/dst"
fs.writeFileSync(src, "source")
fs.writeFileSync(dst, "dest")
fs.chmodSync(dst, 0o444)
try { fs.copyFileSync(src, dst); console.log("copied") } catch (e: any) { console.log(e.code, fs.readFileSync(dst, 'utf8')) }
fs.copyFile(src, dst, (e) => {
  console.log(e ? e.code : "copied")
  fs.promises.copyFile(src, dst).then(() => console.log("copied"), (e: any) => console.log(e.code))
})
`, "EPERM dest\nEPERM\nEPERM")
}

// realpathSync walks a UNC path from its `\\server\share` root (Node's
// splitRootRe); the root was taken as a lone separator, so the server name
// was looked up on the current drive.
func TestE2EWinRealpathUNC(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	share := `\\` + host + `\c$\Windows`
	if _, err := os.Stat(share); err != nil {
		t.Skipf("administrative share not reachable here: %v", err)
	}
	// The program's paths are built from String.fromCharCode(92) so no
	// escaping layer can disturb them.
	assertOutputImports(t, `
import fs from 'fs'
const b = String.fromCharCode(92)
const h = "`+host+`"
const root = b + b + h + b + "c$" + b
const cases: string[][] = [
  ["//" + h + "/c$/Windows", root + "Windows"],
  [root, root],
  ["//" + h + "/c$/", root],
]
for (const c of cases) console.log(fs.realpathSync(c[0]).toLowerCase() === c[1].toLowerCase())
`, "true\ntrue\ntrue")
}
