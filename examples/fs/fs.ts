// fs — synchronous file I/O (readFileSync/writeFileSync/appendFileSync/
// existsSync/unlinkSync/mkdirSync/rmdirSync/renameSync/copyFileSync/
// readdirSync). Import-gated (TDD-00049) — a virtual built-in module, not a
// real file, but real per-file scoping applies just like any other import:
// the local name below is this file's own choice, and a variable of the
// same name in some other scope would correctly shadow it instead of
// silently colliding with it.
//
// Everything here is synchronous and blocking — there's no event loop in
// this compiler, so there's no non-blocking variant to offer. readFileSync
// itself is still text-only (a file containing embedded null bytes reads
// back shorter than its real size via readFileSync's plain, strlen-based
// string) — but readFileSyncBytes, and writeFileSync/appendFileSync given
// an ArrayBuffer/TypedArray instead of a string, are binary-safe (ADR-00094,
// see the bottom of this file). The same split exists on the fetch() side —
// see examples/fetch/fetch.ts's .arrayBuffer() section.

import fs from 'fs'

const path: string = '/tmp/kml_fs_example.txt'

console.log(fs.existsSync(path))   // false — nothing there yet

fs.writeFileSync(path, 'first line')
console.log(fs.existsSync(path))   // true

const content: string = fs.readFileSync(path)
console.log(content)               // first line
console.log(content.length)        // 10

// writeFileSync truncates — a second write replaces the content entirely
fs.writeFileSync(path, 'replaced')
console.log(fs.readFileSync(path)) // replaced

// appendFileSync adds on, creating the file if it doesn't exist yet
fs.appendFileSync(path, '\nsecond line')
console.log(fs.readFileSync(path)) // replaced\nsecond line

// The canonical text-I/O idiom: an 'utf8' encoding (a bare string or an
// { encoding: 'utf8' } object) on read/write/append. This compiler's strings
// are already UTF-8, so the encoding is a faithful no-op — the point is that
// the everyday `readFileSync(path, 'utf8')` call now compiles.
fs.writeFileSync(path, 'text', 'utf8')
console.log(fs.readFileSync(path, 'utf8'))              // text
console.log(fs.readFileSync(path, { encoding: 'utf8' })) // text

// writeFileSync with { flag: 'a' } appends instead of truncating (Node's flag
// semantics); { flag: 'w' } is the default truncate.
fs.writeFileSync(path, '!', { flag: 'a' })
console.log(fs.readFileSync(path, 'utf8'))              // text!

// A failed read/write/append/delete throws a catchable Error, built from
// the OS's own reason (via strerror(errno)) — same approach as fetch's
// network-failure handling.
try {
    fs.readFileSync('/definitely/does/not/exist/kml-example.txt')
} catch (e) {
    console.log('caught: ' + e.message)
    // The Error also carries Node's fs-error surface: err.code (the errno
    // name), err.errno (the negated errno, -2 for ENOENT), err.syscall (the
    // bare syscall — 'open' here), and err.path (the offending path). A plain
    // `new Error()` leaves syscall/path null and errno 0.
    console.log(e.code + ' ' + e.errno + ' ' + e.syscall + ' ' + e.path)  // ENOENT -2 open /definitely/...
}

// existsSync itself never throws for a missing path — it's one of the few
// fs functions (matching real Node) that reports "doesn't exist" as a plain
// false rather than an error.
console.log(fs.existsSync('/definitely/does/not/exist/kml-example.txt'))  // 0

fs.unlinkSync(path)
console.log(fs.existsSync(path))   // false — cleaned up

// --- mkdirSync / readdirSync / renameSync / copyFileSync ---

const dir: string = '/tmp/kml_fs_example_dir'
fs.mkdirSync(dir)
console.log(fs.existsSync(dir))    // true

fs.writeFileSync(dir + '/a.txt', 'file a')
fs.writeFileSync(dir + '/b.txt', 'file b')

// readdirSync lists entries (excluding "." and ".."), in whatever order the
// OS's own readdir() returns them — sort for a deterministic printout.
const entries: string[] = fs.readdirSync(dir)
console.log(entries.length)   // 2
entries.sort()
for (const name of entries) {
    console.log(name)   // a.txt, then b.txt
}
// { withFileTypes: true } returns Dirent[] instead of names — each carries a
// .name and .isFile()/.isDirectory()/.isSymbolicLink(), classified from the
// directory entry's type without a per-file stat.
const dirents = fs.readdirSync(dir, { withFileTypes: true })
for (const d of dirents) {
    // Each Dirent carries .parentPath (the directory it was read from) and the
    // full set of kind predicates — isFile/isDirectory/isSymbolicLink plus the
    // device predicates isFIFO/isCharacterDevice/isBlockDevice/isSocket.
    console.log(d.name + ' isFile=' + d.isFile() + ' isFIFO=' + d.isFIFO() +
        ' parent=' + (d.parentPath === dir))   // a.txt isFile=true isFIFO=false parent=true, …
}

// { recursive: true } walks the whole tree, returning every nested entry as a
// path relative to `dir` (joined with '/'). Create a nested layout first.
fs.mkdirSync(dir + '/nested', { recursive: true })
fs.writeFileSync(dir + '/nested/c.txt', 'c')
const tree = fs.readdirSync(dir, { recursive: true })
tree.sort()
for (const rel of tree) {
    console.log(rel)   // a.txt, b.txt, nested, nested/c.txt
}
fs.unlinkSync(dir + '/nested/c.txt')
fs.rmdirSync(dir + '/nested')

// renameSync moves/renames a file in place
fs.renameSync(dir + '/a.txt', dir + '/a_renamed.txt')
console.log(fs.existsSync(dir + '/a.txt'))            // 0
console.log(fs.existsSync(dir + '/a_renamed.txt'))    // 1

// copyFileSync — reads the source fully, then writes it to dest; both files
// exist independently afterward with the same content
fs.copyFileSync(dir + '/a_renamed.txt', dir + '/a_copy.txt')
console.log(fs.readFileSync(dir + '/a_copy.txt'))   // file a
// fs.constants gives the copyFile flags and POSIX access modes by name, so
// there's no need to spell the raw numbers. COPYFILE_EXCL makes the copy fail
// if dest already exists.
try {
    fs.copyFileSync(dir + '/a_renamed.txt', dir + '/a_copy.txt', fs.constants.COPYFILE_EXCL)
} catch (e) {
    console.log('EXCL refused: ' + (e as any).code)   // EXCL refused: EEXIST
}

// accessSync checks a path against a mode (F_OK exists, R_OK/W_OK/X_OK
// readable/writable/executable) and throws if the check fails.
fs.accessSync(dir + '/a_copy.txt', fs.constants.R_OK)
console.log('a_copy is readable')                     // a_copy is readable

// mkdirSync throws if the directory already exists (no {recursive: true}
// option in this compiler — always the plain, non-recursive mkdir())
try {
    fs.mkdirSync(dir)
} catch (e) {
    console.log('caught: directory already exists')
}

// clean up
fs.unlinkSync(dir + '/a_renamed.txt')
fs.unlinkSync(dir + '/a_copy.txt')
fs.unlinkSync(dir + '/b.txt')
console.log(fs.readdirSync(dir).length)   // 0 — empty again

// rmdirSync removes an empty directory; { recursive: true } removes the whole
// tree (Node deprecated the option here in favor of rmSync but still honors it)
fs.mkdirSync(dir + "/nested/deep", { recursive: true })
fs.writeFileSync(dir + "/nested/deep/f.txt", "x")
fs.rmdirSync(dir + "/nested", { recursive: true })
console.log(fs.existsSync(dir + "/nested"))   // false — tree removed
fs.rmdirSync(dir)
console.log(fs.existsSync(dir))   // false — cleaned up

// --- readFileSyncBytes / binary-safe writeFileSync & appendFileSync (ADR-00094) ---

const binPath: string = '/tmp/kml_fs_example.bin'

// A Uint8Array with an embedded null byte — 'h','i',0,'b','y','e'. Writing
// this through plain writeFileSync(path, someString) would silently
// truncate at the null; passing the TypedArray directly writes it whole.
const bytes = new Uint8Array([104, 105, 0, 98, 121, 101])
fs.writeFileSync(binPath, bytes)

const readBack = fs.readFileSyncBytes(binPath)
console.log(readBack.length)   // 6 — not 2, unlike readFileSync's strlen-based .length would give

// appendFileSync accepts the same ArrayBuffer/TypedArray forms
fs.appendFileSync(binPath, bytes)
console.log(fs.readFileSyncBytes(binPath).length)   // 12

// An ArrayBuffer works too (writeFileSync/appendFileSync accept either an
// ArrayBuffer or any TypedArray view over one)
const buf = new ArrayBuffer(3)
const view = new Uint8Array(buf)
view[0] = 1
view[1] = 0
view[2] = 2
fs.writeFileSync(binPath, buf)
console.log(fs.readFileSyncBytes(binPath).length)   // 3

fs.unlinkSync(binPath)

// Recursive mkdir + delete of an env var.
fs.mkdirSync("/tmp/kml_fs_example/x/y", { recursive: true });
console.log(fs.existsSync("/tmp/kml_fs_example/x/y"));
fs.rmdirSync("/tmp/kml_fs_example/x/y");
fs.rmdirSync("/tmp/kml_fs_example/x");
fs.rmdirSync("/tmp/kml_fs_example");

// ── statSync ────────────────────────────────────────────────────────────────
// size/mtimeMs plus isFile()/isDirectory(); a missing path throws.
fs.writeFileSync('/tmp/kml_stat_example.txt', 'stat me')
const st = fs.statSync('/tmp/kml_stat_example.txt')
console.log(st.size)           // 7
console.log(st.isFile())       // true
console.log(st.isDirectory())  // false
fs.unlinkSync('/tmp/kml_stat_example.txt')

// ── temp dirs, symlinks, and recursive removal ──────────────────────────────
const tmpd = fs.mkdtempSync('/tmp/kml-example-')
fs.writeFileSync(tmpd + '/data.txt', 'hello')
fs.symlinkSync(tmpd + '/data.txt', tmpd + '/alias')
console.log(fs.lstatSync(tmpd + '/alias').isSymbolicLink())  // true
console.log(fs.readlinkSync(tmpd + '/alias') === tmpd + '/data.txt')  // true
// linkSync makes a hard link — a second name for the same inode (no Developer
// Mode needed on Windows, unlike symlinkSync). nlink counts the names.
fs.linkSync(tmpd + '/data.txt', tmpd + '/data.hardlink')
console.log(fs.statSync(tmpd + '/data.hardlink').nlink)  // 2
fs.truncateSync(tmpd + '/data.txt', 2)
console.log(fs.statSync(tmpd + '/data.txt').size)  // 2
// utimesSync sets the access/modify times — a number is seconds (Node's
// form), or pass a Date. statSync reads them back in milliseconds.
fs.utimesSync(tmpd + '/data.txt', 1_000_000_000, 1_500_000_000)
console.log(fs.statSync(tmpd + '/data.txt').mtimeMs)  // 1500000000000
fs.rmSync(tmpd, { recursive: true })   // removes the whole tree
console.log(fs.existsSync(tmpd))       // false

// ── fd-based I/O ────────────────────────────────────────────────────────────
// openSync/writeSync/readSync/fstatSync/closeSync over raw POSIX fds.
const fd = fs.openSync('/tmp/kml_fd_example.txt', 'w')
fs.writeSync(fd, 'raw fd write')
// fsyncSync forces buffered writes down to disk; ftruncateSync resizes the
// open file (shrinking here, from 12 bytes to 3). On Windows these map to
// FlushFileBuffers / SetEndOfFile; on POSIX to the fsync/ftruncate syscalls.
fs.fsyncSync(fd)
fs.ftruncateSync(fd, 3)
// futimesSync sets access/modify times on the open fd (utimesSync's fd twin).
fs.futimesSync(fd, 1_000_000_000, 1_500_000_000)
fs.closeSync(fd)
console.log(fs.statSync('/tmp/kml_fd_example.txt').size)  // 3
console.log(fs.statSync('/tmp/kml_fd_example.txt').mtimeMs)  // 1500000000000
const rfd = fs.openSync('/tmp/kml_fd_example.txt', 'r')
console.log(fs.fstatSync(rfd).size)   // 3 (truncated above)
const head = new Uint8Array(3)
fs.readSync(rfd, head)
console.log(head[0])                  // 114 ('r')
fs.closeSync(rfd)
fs.unlinkSync('/tmp/kml_fd_example.txt')
