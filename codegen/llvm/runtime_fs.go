package llvm

import (
	"fmt"
	"runtime"
)

// ensureFsThrow declares __kml_fs_throw: builds "<opDesc> '<path>': <reason>"
// from the current errno via strerror() and throws it as a KML Error via the
// existing @__kml_throw mechanism (emit_exceptions.go) — the same "let a
// real OS-level failure surface as a catchable Error" approach ADR-00021
// already established for fetch's network failures.
func (e *Emitter) ensureFsThrow() {
	if e.usedFsThrow {
		return
	}
	e.usedFsThrow = true
	e.ensureMalloc()
	e.ensureStrlen()
	e.ensureSprintf()
	e.ensureStrHeaderRuntime() // error .message must be headered for concat/=== (TDD-00120)
	e.ensureExceptionHelpers()
	accessor := errnoAccessor()
	e.ensureErrnoAccessor()
	e.ensureStrerror()
	fmtPtr := e.internString("%s '%s': %s")
	errNamePtr := e.internString("Error")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_throw(ptr %%opdesc, ptr %%path) {
entry:
  %%errno_ptr = call ptr @%s()
  %%errno_val = load i32, ptr %%errno_ptr, align 4
  %%errmsg = call ptr @strerror(i32 %%errno_val)
  %%len_op = call i64 @strlen(ptr %%opdesc)
  %%len_path = call i64 @strlen(ptr %%path)
  %%len_err = call i64 @strlen(ptr %%errmsg)
  %%sum1 = add i64 %%len_op, %%len_path
  %%sum2 = add i64 %%sum1, %%len_err
  %%bufsize = add i64 %%sum2, 32
  %%buf = call ptr @__kml_str_alloc(i64 %%bufsize)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%buf, ptr %s, ptr %%opdesc, ptr %%path, ptr %%errmsg)
  call void @__kml_str_finalize(ptr %%buf)
  %%errobj = call ptr @malloc(i64 24)
  %%errobj.kind = getelementptr { i64, ptr, ptr }, ptr %%errobj, i32 0, i32 0
  store i64 0, ptr %%errobj.kind, align 8
  %%errobj.msg = getelementptr { i64, ptr, ptr }, ptr %%errobj, i32 0, i32 1
  store ptr %%buf, ptr %%errobj.msg, align 8
  %%errobj.name = getelementptr { i64, ptr, ptr }, ptr %%errobj, i32 0, i32 2
  store ptr %s, ptr %%errobj.name, align 8
  call void @__kml_throw(ptr %%errobj)
  ret void
}`, accessor, fmtPtr, errNamePtr))
}

func (e *Emitter) ensureFopen() {
	if e.usedFopen {
		return
	}
	e.usedFopen = true
	e.emitGlobal("declare ptr @fopen(ptr noundef, ptr noundef)")
}

func (e *Emitter) ensureFclose() {
	if e.usedFclose {
		return
	}
	e.usedFclose = true
	e.emitGlobal("declare i32 @fclose(ptr noundef)")
}

func (e *Emitter) ensureFread() {
	if e.usedFread {
		return
	}
	e.usedFread = true
	e.emitGlobal("declare i64 @fread(ptr noundef, i64 noundef, i64 noundef, ptr noundef)")
}

func (e *Emitter) ensureFwrite() {
	if e.usedFwrite {
		return
	}
	e.usedFwrite = true
	e.emitGlobal("declare i64 @fwrite(ptr noundef, i64 noundef, i64 noundef, ptr noundef)")
}

// ensureFsReadFile declares __kml_fs_read_file: reads an entire file into a
// malloc'd, null-terminated string. Throws (via __kml_fs_throw) if the file
// can't be opened. A thin wrapper around __kml_fs_read_file_raw
// (ADR-00094) that discards the real byte count — kept as its own symbol,
// behavior-unchanged, so readFileSync's existing text-only contract (a file
// containing embedded null bytes reads back shorter than its real size)
// stays exactly as it was; fs.readFileSyncBytes (emit_fs.go) is the
// null-byte-safe alternative, going through __kml_fs_read_file_raw directly.
func (e *Emitter) ensureFsReadFile() {
	if e.usedFsReadFile {
		return
	}
	e.usedFsReadFile = true
	e.ensureFsReadFileRaw()
	e.ensureStrHeaderRuntime()
	e.ensureMemcpy()
	// TDD-00120: the raw buffer is dual-use (also the readFileSyncBytes path), so
	// copy it into a length-prefixed string sized by the real byte count (the
	// header carries the true length for a future binary-safe read), then free
	// the plain raw buffer.
	e.emitGlobal(`
define ptr @__kml_fs_read_file(ptr %path) {
entry:
  %raw = call { ptr, i64 } @__kml_fs_read_file_raw(ptr %path)
  %buf = extractvalue { ptr, i64 } %raw, 0
  %size = extractvalue { ptr, i64 } %raw, 1
  %dst = call ptr @__kml_str_alloc(i64 %size)
  call ptr @memcpy(ptr %dst, ptr %buf, i64 %size)
  %np = getelementptr i8, ptr %dst, i64 %size
  store i8 0, ptr %np, align 1
  call void @free(ptr %buf)
  ret ptr %dst
}`)
}

// ensureFsReadFileRaw declares __kml_fs_read_file_raw(path) -> {ptr, i64}:
// the actual fopen/fseek/ftell/fread implementation, returning both the
// malloc'd (null-terminated, for the string wrapper's benefit) buffer and
// its real byte count — ftell already computes the exact size before
// __kml_fs_read_file used to discard it in favor of a bare ptr (ADR-00094).
// Shared by __kml_fs_read_file (readFileSync, discards the length) and
// emit_fs.go's emitFsReadFileSyncBytes (readFileSyncBytes, keeps it — the
// {ptr, i64} return is already the exact SSA aggregate shape a TypedArray
// value uses, so that caller needs no repacking at all).
func (e *Emitter) ensureFsReadFileRaw() {
	if e.usedFsReadFileRaw {
		return
	}
	e.usedFsReadFileRaw = true
	e.ensureFsThrow()
	e.ensureMalloc()
	e.ensureFopen()
	e.ensureFclose()
	e.emitGlobal("declare i32 @fseek(ptr noundef, i64 noundef, i32 noundef)")
	e.emitGlobal("declare i64 @ftell(ptr noundef)")
	e.ensureFread()
	modePtr := e.internString("rb")
	opDescPtr := e.internString("cannot open file for reading")
	e.emitGlobal(fmt.Sprintf(`
define { ptr, i64 } @__kml_fs_read_file_raw(ptr %%path) {
entry:
  %%f = call ptr @fopen(ptr %%path, ptr %s)
  %%isnull = icmp eq ptr %%f, null
  br i1 %%isnull, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable

ok:
  %%seekend = call i32 @fseek(ptr %%f, i64 0, i32 2)
  %%size = call i64 @ftell(ptr %%f)
  %%seekset = call i32 @fseek(ptr %%f, i64 0, i32 0)
  %%sizep1 = add i64 %%size, 1
  %%buf = call ptr @malloc(i64 %%sizep1)
  %%nread = call i64 @fread(ptr %%buf, i64 1, i64 %%size, ptr %%f)
  %%termptr = getelementptr i8, ptr %%buf, i64 %%size
  store i8 0, ptr %%termptr, align 1
  call i32 @fclose(ptr %%f)
  %%r0 = insertvalue { ptr, i64 } undef, ptr %%buf, 0
  %%r1 = insertvalue { ptr, i64 } %%r0, i64 %%size, 1
  ret { ptr, i64 } %%r1
}`, modePtr, opDescPtr))
}

// ensureFsWriteFile declares __kml_fs_write_file: writes (creating or
// truncating) a file with the given string content. Throws if the file
// can't be opened for writing.
func (e *Emitter) ensureFsWriteFile() {
	e.ensureFsWriteLike(&e.usedFsWriteFile, "__kml_fs_write_file", "wb", "cannot open file for writing")
}

// ensureFsAppendFile declares __kml_fs_append_file: like ensureFsWriteFile,
// but appends (creating the file if it doesn't exist yet) instead of
// truncating.
func (e *Emitter) ensureFsAppendFile() {
	e.ensureFsWriteLike(&e.usedFsAppendFile, "__kml_fs_append_file", "ab", "cannot open file for appending")
}

// ensureFsWriteFileBytes/ensureFsAppendFileBytes declare the ArrayBuffer/
// TypedArray-aware siblings of ensureFsWriteFile/ensureFsAppendFile
// (ADR-00094) — __kml_fs_write_file_bytes/__kml_fs_append_file_bytes take
// an explicit length instead of relying on strlen, so a buffer with an
// embedded null byte writes out whole. emit_fs.go's emitFsWriteLikeCall
// routes to these when the data argument is an ArrayBuffer/TypedArray, and
// to the existing strlen-based functions above (untouched) for a plain
// string — both sets of runtime functions coexist independently.
func (e *Emitter) ensureFsWriteFileBytes() {
	e.ensureFsWriteLikeBytes(&e.usedFsWriteFileBytes, "__kml_fs_write_file_bytes", "wb", "cannot open file for writing")
}

func (e *Emitter) ensureFsAppendFileBytes() {
	e.ensureFsWriteLikeBytes(&e.usedFsAppendFileBytes, "__kml_fs_append_file_bytes", "ab", "cannot open file for appending")
}

// ensureFsWriteLike is the shared implementation behind ensureFsWriteFile
// and ensureFsAppendFile — identical shape, differing only in fopen mode,
// the generated function's name, and the error message.
func (e *Emitter) ensureFsWriteLike(used *bool, fnName, mode, opDesc string) {
	if *used {
		return
	}
	*used = true
	e.ensureFsThrow()
	e.ensureStrlen()
	e.ensureFopen()
	e.ensureFclose()
	e.ensureFwrite()
	modePtr := e.internString(mode)
	opDescPtr := e.internString(opDesc)
	e.emitGlobal(fmt.Sprintf(`
define void @%s(ptr %%path, ptr %%data) {
entry:
  %%f = call ptr @fopen(ptr %%path, ptr %s)
  %%isnull = icmp eq ptr %%f, null
  br i1 %%isnull, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable

ok:
  %%len = call i64 @strlen(ptr %%data)
  %%nwritten = call i64 @fwrite(ptr %%data, i64 1, i64 %%len, ptr %%f)
  call i32 @fclose(ptr %%f)
  ret void
}`, fnName, modePtr, opDescPtr))
}

// ensureFsWriteLikeBytes is ensureFsWriteLike's explicit-length sibling
// (ADR-00094): identical shape, except the caller passes the real byte
// count directly instead of it being derived via strlen — so a buffer with
// an embedded null byte writes out whole, not truncated at the first one.
func (e *Emitter) ensureFsWriteLikeBytes(used *bool, fnName, mode, opDesc string) {
	if *used {
		return
	}
	*used = true
	e.ensureFsThrow()
	e.ensureFopen()
	e.ensureFclose()
	e.ensureFwrite()
	modePtr := e.internString(mode)
	opDescPtr := e.internString(opDesc)
	e.emitGlobal(fmt.Sprintf(`
define void @%s(ptr %%path, ptr %%data, i64 %%len) {
entry:
  %%f = call ptr @fopen(ptr %%path, ptr %s)
  %%isnull = icmp eq ptr %%f, null
  br i1 %%isnull, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable

ok:
  %%nwritten = call i64 @fwrite(ptr %%data, i64 1, i64 %%len, ptr %%f)
  call i32 @fclose(ptr %%f)
  ret void
}`, fnName, modePtr, opDescPtr))
}

// ensureFsExists declares __kml_fs_exists: a plain existence check via
// POSIX access() — deliberately does NOT throw (matching real Node's
// fs.existsSync, one of the few fs functions that reports "doesn't exist"
// as a plain false rather than an error).
func (e *Emitter) ensureFsExists() {
	if e.usedFsExists {
		return
	}
	e.usedFsExists = true
	e.emitGlobal("declare i32 @access(ptr noundef, i32 noundef)")
	e.emitGlobal(`
define i1 @__kml_fs_exists(ptr %path) {
entry:
  %r = call i32 @access(ptr %path, i32 0)
  %ok = icmp eq i32 %r, 0
  ret i1 %ok
}`)
}

// ensureFsUnlink declares __kml_fs_unlink: deletes a file via the portable
// ANSI C remove() (simpler than POSIX unlink() for this purpose, and
// available identically on every target this compiler supports). Throws on
// failure.
func (e *Emitter) ensureFsUnlink() {
	if e.usedFsUnlink {
		return
	}
	e.usedFsUnlink = true
	e.ensureFsThrow()
	e.emitGlobal("declare i32 @remove(ptr noundef)")
	opDescPtr := e.internString("cannot delete file")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_unlink(ptr %%path) {
entry:
  %%r = call i32 @remove(ptr %%path)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable

ok:
  ret void
}`, opDescPtr))
}

// ensureFsRmdir declares __kml_fs_rmdir: removes an empty directory via
// POSIX rmdir() — deliberately not remove()/unlink() (which would also
// silently accept a plain file, unlike real Node's fs.rmdirSync, which is
// specifically directory-only and fails with ENOTDIR/ENOTEMPTY otherwise).
// No recursive-delete option (matching mkdirSync's lack of {recursive:
// true}) — only ever removes a directory that's already empty.
func (e *Emitter) ensureFsRmdir() {
	if e.usedFsRmdir {
		return
	}
	e.usedFsRmdir = true
	e.ensureFsThrow()
	e.emitGlobal("declare i32 @rmdir(ptr noundef)")
	opDescPtr := e.internString("cannot remove directory")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_rmdir(ptr %%path) {
entry:
  %%r = call i32 @rmdir(ptr %%path)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable

ok:
  ret void
}`, opDescPtr))
}

// ensureFsMkdir declares __kml_fs_mkdir: creates a directory via POSIX
// mkdir(), mode 0777 (reduced by the process umask as usual — the same
// default real Node's fs.mkdirSync uses without an explicit mode option).
// Throws on failure (e.g. EEXIST if the path already exists, ENOENT if the
// parent doesn't) — matches unlinkSync's exact shape, one path argument.
func (e *Emitter) ensureFsMkdir() {
	if e.usedFsMkdir {
		return
	}
	e.usedFsMkdir = true
	e.ensureFsThrow()
	e.emitGlobal("declare i32 @mkdir(ptr noundef, i32 noundef)")
	opDescPtr := e.internString("cannot create directory")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_mkdir(ptr %%path) {
entry:
  %%r = call i32 @mkdir(ptr %%path, i32 511)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable

ok:
  ret void
}`, opDescPtr))
}

// ensureFsMkdirP declares __kml_fs_mkdir_p: `mkdirSync(path, {recursive:
// true})` (ADR-00487) — creates each missing path prefix, ignoring
// already-exists at every step; verifies the final directory exists via
// access(2) and throws only when it genuinely couldn't be created.
func (e *Emitter) ensureFsMkdirP() {
	if e.usedFsMkdirP {
		return
	}
	e.usedFsMkdirP = true
	e.ensureFsMkdir()
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureFsExists() // for the access(2) decl + existence probe
	opDescPtr := e.internString("mkdirSync (recursive)")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_mkdir_p(ptr %%path) {
entry:
  %%len = call i64 @strlen(ptr %%path)
  %%len1 = add i64 %%len, 1
  %%buf = call ptr @malloc(i64 %%len1)
  %%ign0 = call ptr @memcpy(ptr %%buf, ptr %%path, i64 %%len1)
  br label %%loop
loop:
  %%i = phi i64 [ 1, %%entry ], [ %%inext, %%cont ]
  %%atEnd = icmp sge i64 %%i, %%len
  br i1 %%atEnd, label %%final, label %%chk
chk:
  %%p = getelementptr i8, ptr %%buf, i64 %%i
  %%c = load i8, ptr %%p, align 1
  %%isSlash = icmp eq i8 %%c, 47
  br i1 %%isSlash, label %%mk, label %%cont
mk:
  store i8 0, ptr %%p, align 1
  %%r1 = call i32 @mkdir(ptr %%buf, i32 511)
  store i8 47, ptr %%p, align 1
  br label %%cont
cont:
  %%inext = add i64 %%i, 1
  br label %%loop
final:
  %%r2 = call i32 @mkdir(ptr %%buf, i32 511)
  %%acc = call i32 @access(ptr %%buf, i32 0)
  %%missing = icmp ne i32 %%acc, 0
  br i1 %%missing, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable
ok:
  ret void
}`, opDescPtr))
}

// ensureFsRename declares __kml_fs_rename: renames/moves a file via POSIX
// rename(). Throws on failure, using the same "<opDesc> '<path>': <reason>"
// shape as every other fs.* failure — with the *old* path in the message,
// since that's the argument the caller will recognize.
func (e *Emitter) ensureFsRename() {
	if e.usedFsRename {
		return
	}
	e.usedFsRename = true
	e.ensureFsThrow()
	e.emitGlobal("declare i32 @rename(ptr noundef, ptr noundef)")
	opDescPtr := e.internString("cannot rename")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_rename(ptr %%oldpath, ptr %%newpath) {
entry:
  %%r = call i32 @rename(ptr %%oldpath, ptr %%newpath)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%oldpath)
  unreachable

ok:
  ret void
}`, opDescPtr))
}

// direntNameOffset returns struct dirent's d_name field offset (in bytes)
// on the host this compiler itself is running on (and will therefore also
// clang on) — struct dirent has no portable/stable layout across libc
// implementations, only the "d_name is a null-terminated char array
// somewhere in there" guarantee POSIX actually promises.
//
// Verified, not guessed: the Darwin offset (21) was confirmed directly by
// compiling and running a real C program on this project's own dev machine
// (offsetof(struct dirent, d_name) against Xcode's actual <dirent.h>). The
// Linux offset (19) originally came from reading glibc's own source
// (sysdeps/unix/sysv/linux/bits/dirent.h: __ino64_t d_ino (8) + __off64_t
// d_off (8) + unsigned short d_reclen (2) + unsigned char d_type (1), no
// padding before d_name since it's 1-byte-aligned char data), and was later
// independently confirmed by actually compiling and running the same
// offsetof probe inside a real x86-64 Linux container (`docker run
// --platform linux/amd64 ubuntu:24.04`) while investigating ADR-00051's
// ucontext_t bug — this number was correct all along, unlike that one.
// Both numbers assume a 64-bit build, which is this project's only target
// per its own stated scope.
func direntNameOffset() int {
	if runtime.GOOS == "darwin" {
		return 21
	}
	return 19
}

// ensureFsReaddir declares __kml_fs_readdir: lists a directory's entries
// (excluding "." and "..", matching real Node's fs.readdirSync) via POSIX
// opendir/readdir/closedir, returning a {ptr, i64} string[] aggregate grown
// with the same realloc-doubling shape __kml_fetch/__kml_exec_file_sync
// already use for their own growable buffers — just growing an array of
// ptr-sized name slots here instead of raw bytes. Each returned name is a
// malloc'd strdup() copy, independent of the OS's own dirent buffer (which
// readdir() is free to reuse/overwrite on the next call).
func (e *Emitter) ensureFsReaddir() {
	if e.usedFsReaddir {
		return
	}
	e.usedFsReaddir = true
	e.ensureFsThrow()
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureStrcmp()
	e.ensureStrHeaderRuntime() // TDD-00120: entry names are header-copied strings
	e.emitGlobal("declare ptr @opendir(ptr noundef)")
	e.emitGlobal("declare ptr @readdir(ptr noundef)")
	e.emitGlobal("declare i32 @closedir(ptr noundef)")
	e.emitGlobal("declare ptr @strdup(ptr noundef)")
	opDescPtr := e.internString("cannot open directory")
	dotPtr := e.internString(".")
	dotdotPtr := e.internString("..")
	e.emitGlobal(fmt.Sprintf(`
define {ptr, i64} @__kml_fs_readdir(ptr %%path) {
entry:
  %%dir = call ptr @opendir(ptr %%path)
  %%dirisnull = icmp eq ptr %%dir, null
  br i1 %%dirisnull, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable

ok:
  %%bufslot = call ptr @malloc(i64 24)
  %%data_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 0
  %%len_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 1
  %%cap_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 2
  store ptr null, ptr %%data_p, align 8
  store i64 0, ptr %%len_p, align 8
  store i64 0, ptr %%cap_p, align 8
  br label %%readloop

readloop:
  %%ent = call ptr @readdir(ptr %%dir)
  %%entisnull = icmp eq ptr %%ent, null
  br i1 %%entisnull, label %%done, label %%gotent

gotent:
  %%nameptr = getelementptr i8, ptr %%ent, i64 %d
  %%isdot = call i32 @strcmp(ptr %%nameptr, ptr %s)
  %%isdotdot = call i32 @strcmp(ptr %%nameptr, ptr %s)
  %%isdotb = icmp eq i32 %%isdot, 0
  %%isdotdotb = icmp eq i32 %%isdotdot, 0
  %%skip = or i1 %%isdotb, %%isdotdotb
  br i1 %%skip, label %%readloop, label %%append

append:
  %%curdata = load ptr, ptr %%data_p, align 8
  %%curlen = load i64, ptr %%len_p, align 8
  %%curcap = load i64, ptr %%cap_p, align 8
  %%neededp1 = add i64 %%curlen, 1
  %%needgrow = icmp sgt i64 %%neededp1, %%curcap
  br i1 %%needgrow, label %%grow, label %%storeit

grow:
  %%cap2 = mul i64 %%curcap, 2
  %%atleast8 = icmp sgt i64 %%cap2, 8
  %%newcap = select i1 %%atleast8, i64 %%cap2, i64 8
  %%newcapbytes = mul i64 %%newcap, 8
  %%newdata = call ptr @realloc(ptr %%curdata, i64 %%newcapbytes)
  store ptr %%newdata, ptr %%data_p, align 8
  store i64 %%newcap, ptr %%cap_p, align 8
  br label %%storeit

storeit:
  %%dataNow = load ptr, ptr %%data_p, align 8
  %%namecopy = call ptr @__kml_str_from_cstr(ptr %%nameptr)
  %%slot = getelementptr ptr, ptr %%dataNow, i64 %%curlen
  store ptr %%namecopy, ptr %%slot, align 8
  %%newlen = add i64 %%curlen, 1
  store i64 %%newlen, ptr %%len_p, align 8
  br label %%readloop

done:
  call i32 @closedir(ptr %%dir)
  %%finaldata = load ptr, ptr %%data_p, align 8
  %%finallen = load i64, ptr %%len_p, align 8
  %%r0 = insertvalue {ptr, i64} undef, ptr %%finaldata, 0
  %%r1 = insertvalue {ptr, i64} %%r0, i64 %%finallen, 1
  ret {ptr, i64} %%r1
}`, opDescPtr, direntNameOffset(), dotPtr, dotdotPtr))
}


// statLayout returns the host libc's struct stat field offsets (mode/size/
// st_mtim seconds+nanoseconds) plus the mode field's load width in bits —
// struct stat has no portable layout, so these are per-OS/arch constants,
// the same approach direntNameOffset takes. Darwin (64-bit-inode default):
// st_mode u16 @4, mtimespec @48, st_size @96. glibc x86-64: st_mode u32
// @24, st_size @48, st_mtim @88. glibc aarch64: st_mode u32 @16, st_size
// @48, st_mtim @88.
func statLayout() (modeOff, modeBits, sizeOff, mtimeSecOff, mtimeNsecOff int) {
	if runtime.GOOS == "darwin" {
		return 4, 16, 96, 48, 56
	}
	if runtime.GOARCH == "arm64" {
		return 16, 32, 48, 88, 96
	}
	return 24, 32, 48, 88, 96
}

// ensureFsStat declares __kml_fs_stat (ADR-00495): stat(2) into a 256-byte
// scratch buffer, extracting {mode, size, mtimeMs} at statLayout()'s host
// offsets. Throws the shared fs error on failure (ENOENT and friends).
func (e *Emitter) ensureFsStat() {
	if e.usedFsStat {
		return
	}
	e.usedFsStat = true
	e.ensureFsThrow()
	e.ensureMalloc()
	e.emitGlobal("declare i32 @stat(ptr noundef, ptr noundef)")
	modeOff, modeBits, sizeOff, secOff, nsecOff := statLayout()
	opDescPtr := e.internString("cannot stat path")
	e.emitGlobal(fmt.Sprintf(`
define { i64, i64, i64 } @__kml_fs_stat(ptr %%path) {
entry:
  %%buf = alloca [256 x i8], align 8
  %%r = call i32 @stat(ptr %%path, ptr %%buf)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable

ok:
  %%modep = getelementptr i8, ptr %%buf, i64 %d
  %%modew = load i%d, ptr %%modep, align 2
  %%mode = zext i%d %%modew to i64
  %%sizep = getelementptr i8, ptr %%buf, i64 %d
  %%size = load i64, ptr %%sizep, align 8
  %%secp = getelementptr i8, ptr %%buf, i64 %d
  %%sec = load i64, ptr %%secp, align 8
  %%nsecp = getelementptr i8, ptr %%buf, i64 %d
  %%nsec = load i64, ptr %%nsecp, align 8
  %%ms1 = mul i64 %%sec, 1000
  %%ms2 = sdiv i64 %%nsec, 1000000
  %%ms = add i64 %%ms1, %%ms2
  %%r0 = insertvalue { i64, i64, i64 } undef, i64 %%mode, 0
  %%r1 = insertvalue { i64, i64, i64 } %%r0, i64 %%size, 1
  %%r2 = insertvalue { i64, i64, i64 } %%r1, i64 %%ms, 2
  ret { i64, i64, i64 } %%r2
}`, opDescPtr, modeOff, modeBits, modeBits, sizeOff, secOff, nsecOff))
}


// ensureFsLstat declares __kml_fs_lstat — statSync's twin over lstat(2)
// (does not follow symlinks), sharing statLayout()'s offsets (ADR-00497).
func (e *Emitter) ensureFsLstat() {
	if e.usedFsLstat {
		return
	}
	e.usedFsLstat = true
	e.ensureFsThrow()
	e.emitGlobal("declare i32 @lstat(ptr noundef, ptr noundef)")
	modeOff, modeBits, sizeOff, secOff, nsecOff := statLayout()
	opDescPtr := e.internString("cannot lstat path")
	e.emitGlobal(fmt.Sprintf(`
define { i64, i64, i64 } @__kml_fs_lstat(ptr %%path) {
entry:
  %%buf = alloca [256 x i8], align 8
  %%r = call i32 @lstat(ptr %%path, ptr %%buf)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable

ok:
  %%modep = getelementptr i8, ptr %%buf, i64 %d
  %%modew = load i%d, ptr %%modep, align 2
  %%mode = zext i%d %%modew to i64
  %%sizep = getelementptr i8, ptr %%buf, i64 %d
  %%size = load i64, ptr %%sizep, align 8
  %%secp = getelementptr i8, ptr %%buf, i64 %d
  %%sec = load i64, ptr %%secp, align 8
  %%nsecp = getelementptr i8, ptr %%buf, i64 %d
  %%nsec = load i64, ptr %%nsecp, align 8
  %%ms1 = mul i64 %%sec, 1000
  %%ms2 = sdiv i64 %%nsec, 1000000
  %%ms = add i64 %%ms1, %%ms2
  %%r0 = insertvalue { i64, i64, i64 } undef, i64 %%mode, 0
  %%r1 = insertvalue { i64, i64, i64 } %%r0, i64 %%size, 1
  %%r2 = insertvalue { i64, i64, i64 } %%r1, i64 %%ms, 2
  ret { i64, i64, i64 } %%r2
}`, opDescPtr, modeOff, modeBits, modeBits, sizeOff, secOff, nsecOff))
}

// ensureFsPathOps declares the one-shot path-based helpers (ADR-00497):
// realpath, mkdtemp, symlink, readlink, chmod, truncate, access — each a
// thin libc wrapper throwing the shared fs error on failure.
func (e *Emitter) ensureFsPathOps() {
	if e.usedFsPathOps {
		return
	}
	e.usedFsPathOps = true
	e.ensureFsThrow()
	e.ensureMalloc()
	e.ensureStrlen()
	e.ensureMemcpy()
	e.ensureFsExists() // owns the `access` decl
	e.emitGlobal("declare ptr @realpath(ptr noundef, ptr noundef)")
	e.emitGlobal("declare ptr @mkdtemp(ptr noundef)")
	e.emitGlobal("declare i32 @symlink(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i64 @readlink(ptr noundef, ptr noundef, i64 noundef)")
	e.emitGlobal("declare i32 @chmod(ptr noundef, i32 noundef)")
	e.emitGlobal("declare i32 @truncate(ptr noundef, i64 noundef)")
	realpathDesc := e.internString("cannot resolve path")
	mkdtempDesc := e.internString("cannot create temp directory")
	symlinkDesc := e.internString("cannot create symlink")
	readlinkDesc := e.internString("cannot read symlink")
	chmodDesc := e.internString("cannot chmod path")
	truncateDesc := e.internString("cannot truncate path")
	accessDesc := e.internString("cannot access path")
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_fs_realpath(ptr %%path) {
entry:
  %%r = call ptr @realpath(ptr %%path, ptr null)
  %%failed = icmp eq ptr %%r, null
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable
ok:
  ret ptr %%r
}

define ptr @__kml_fs_mkdtemp(ptr %%prefix) {
entry:
  %%plen = call i64 @strlen(ptr %%prefix)
  %%tlen = add i64 %%plen, 7
  %%tmpl = call ptr @malloc(i64 %%tlen)
  %%ign = call ptr @memcpy(ptr %%tmpl, ptr %%prefix, i64 %%plen)
  %%xs = getelementptr i8, ptr %%tmpl, i64 %%plen
  store i8 88, ptr %%xs, align 1
  %%x1 = getelementptr i8, ptr %%xs, i64 1
  store i8 88, ptr %%x1, align 1
  %%x2 = getelementptr i8, ptr %%xs, i64 2
  store i8 88, ptr %%x2, align 1
  %%x3 = getelementptr i8, ptr %%xs, i64 3
  store i8 88, ptr %%x3, align 1
  %%x4 = getelementptr i8, ptr %%xs, i64 4
  store i8 88, ptr %%x4, align 1
  %%x5 = getelementptr i8, ptr %%xs, i64 5
  store i8 88, ptr %%x5, align 1
  %%nul = getelementptr i8, ptr %%xs, i64 6
  store i8 0, ptr %%nul, align 1
  %%r = call ptr @mkdtemp(ptr %%tmpl)
  %%failed = icmp eq ptr %%r, null
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %%prefix)
  unreachable
ok:
  ret ptr %%r
}

define void @__kml_fs_symlink(ptr %%target, ptr %%path) {
entry:
  %%r = call i32 @symlink(ptr %%target, ptr %%path)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable
ok:
  ret void
}

define ptr @__kml_fs_readlink(ptr %%path) {
entry:
  %%buf = call ptr @malloc(i64 4097)
  %%n = call i64 @readlink(ptr %%path, ptr %%buf, i64 4096)
  %%failed = icmp slt i64 %%n, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable
ok:
  %%end = getelementptr i8, ptr %%buf, i64 %%n
  store i8 0, ptr %%end, align 1
  ret ptr %%buf
}

define void @__kml_fs_chmod(ptr %%path, i64 %%mode) {
entry:
  %%m32 = trunc i64 %%mode to i32
  %%r = call i32 @chmod(ptr %%path, i32 %%m32)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable
ok:
  ret void
}

define void @__kml_fs_truncate(ptr %%path, i64 %%len) {
entry:
  %%r = call i32 @truncate(ptr %%path, i64 %%len)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable
ok:
  ret void
}

define void @__kml_fs_access(ptr %%path, i64 %%mode) {
entry:
  %%m32 = trunc i64 %%mode to i32
  %%r = call i32 @access(ptr %%path, i32 %%m32)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable
ok:
  ret void
}`, realpathDesc, mkdtempDesc, symlinkDesc, readlinkDesc, chmodDesc, truncateDesc, accessDesc))
}

// ensureFsRm declares __kml_fs_rm (ADR-00497): fs.rmSync. remove(3) first
// (covers files and empty directories); on failure with recursive set,
// walks the directory via opendir/readdir (lstat is deliberately not
// needed: children are recursed blindly and remove() handles non-dirs) and
// rmdir()s the emptied directory. force swallows a missing path.
func (e *Emitter) ensureFsRm() {
	if e.usedFsRm {
		return
	}
	e.usedFsRm = true
	e.ensureFsThrow()
	e.ensureFsUnlink()
	e.ensureFsRmdir() // owns the `rmdir` decl
	e.ensureFsReaddir()
	e.ensureFsExists()
	e.ensureMalloc()
	e.ensureStrlen()
	e.ensureMemcpy()
	rmDesc := e.internString("cannot remove path")
	nameOff := direntNameOffset()
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_rm(ptr %%path, i1 %%recursive, i1 %%force) {
entry:
  %%r = call i32 @remove(ptr %%path)
  %%ok0 = icmp eq i32 %%r, 0
  br i1 %%ok0, label %%done, label %%notplain
notplain:
  br i1 %%force, label %%chkexists, label %%chkrec
chkexists:
  %%ex = call i1 @__kml_fs_exists(ptr %%path)
  br i1 %%ex, label %%chkrec, label %%done
chkrec:
  br i1 %%recursive, label %%walk, label %%fail
walk:
  %%d = call ptr @opendir(ptr %%path)
  %%dnull = icmp eq ptr %%d, null
  br i1 %%dnull, label %%fail, label %%loop
loop:
  %%ent = call ptr @readdir(ptr %%d)
  %%enull = icmp eq ptr %%ent, null
  br i1 %%enull, label %%endwalk, label %%checkname
checkname:
  %%name = getelementptr i8, ptr %%ent, i64 %d
  %%c0 = load i8, ptr %%name, align 1
  %%isdot = icmp eq i8 %%c0, 46
  br i1 %%isdot, label %%maybeskip, label %%recurse
maybeskip:
  %%c1p = getelementptr i8, ptr %%name, i64 1
  %%c1 = load i8, ptr %%c1p, align 1
  %%isend = icmp eq i8 %%c1, 0
  br i1 %%isend, label %%loop, label %%maybedotdot
maybedotdot:
  %%isdot2 = icmp eq i8 %%c1, 46
  br i1 %%isdot2, label %%maybeskip2, label %%recurse
maybeskip2:
  %%c2p = getelementptr i8, ptr %%name, i64 2
  %%c2 = load i8, ptr %%c2p, align 1
  %%isend2 = icmp eq i8 %%c2, 0
  br i1 %%isend2, label %%loop, label %%recurse
recurse:
  %%plen = call i64 @strlen(ptr %%path)
  %%nlen = call i64 @strlen(ptr %%name)
  %%clen0 = add i64 %%plen, %%nlen
  %%clen = add i64 %%clen0, 2
  %%child = call ptr @malloc(i64 %%clen)
  %%ign1 = call ptr @memcpy(ptr %%child, ptr %%path, i64 %%plen)
  %%slashp = getelementptr i8, ptr %%child, i64 %%plen
  store i8 47, ptr %%slashp, align 1
  %%dstn = getelementptr i8, ptr %%slashp, i64 1
  %%ign2 = call ptr @memcpy(ptr %%dstn, ptr %%name, i64 %%nlen)
  %%endp = getelementptr i8, ptr %%dstn, i64 %%nlen
  store i8 0, ptr %%endp, align 1
  call void @__kml_fs_rm(ptr %%child, i1 true, i1 true)
  br label %%loop
endwalk:
  %%ignc = call i32 @closedir(ptr %%d)
  %%rr = call i32 @rmdir(ptr %%path)
  %%okr = icmp eq i32 %%rr, 0
  br i1 %%okr, label %%done, label %%fail
fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable
done:
  ret void
}`, nameOff, rmDesc))
}


// openFlagBits maps a Node open-flags string to the host's O_* bit mask —
// per-OS constants (Darwin and glibc disagree on everything past
// O_RDONLY/O_WRONLY/O_RDWR), resolved at compile time from the literal
// (ADR-00498).
func openFlagBits(flags string) (int, bool) {
	creat, trunc, appnd, excl := 0x200, 0x400, 0x8, 0x800
	if runtime.GOOS != "darwin" {
		creat, trunc, appnd, excl = 0x40, 0x200, 0x400, 0x80
	}
	m := map[string]int{
		"r": 0, "r+": 2,
		"w": 1 | creat | trunc, "w+": 2 | creat | trunc,
		"a": 1 | creat | appnd, "a+": 2 | creat | appnd,
		"wx": 1 | creat | trunc | excl, "ax": 1 | creat | appnd | excl,
	}
	v, ok := m[flags]
	return v, ok
}

// ensureFsFdOps declares the fd-based helpers (ADR-00498): open/close/
// read/write/fstat over raw POSIX fds. open throws the shared fs error on
// failure; the others throw on a negative return.
func (e *Emitter) ensureFsFdOps() {
	if e.usedFsFdOps {
		return
	}
	e.usedFsFdOps = true
	e.ensureFsThrow()
	e.emitGlobal("declare i32 @open(ptr noundef, i32 noundef, ...)")
	e.emitGlobal("declare i32 @close(i32 noundef)")
	e.emitGlobal("declare i64 @read(i32 noundef, ptr noundef, i64 noundef)")
	e.emitGlobal("declare i64 @write(i32 noundef, ptr noundef, i64 noundef)")
	e.emitGlobal("declare i64 @lseek(i32 noundef, i64 noundef, i32 noundef)")
	e.emitGlobal("declare i32 @fstat(i32 noundef, ptr noundef)")
	openDesc := e.internString("cannot open path")
	fdDesc := e.internString("fd operation failed")
	modeOff, modeBits, sizeOff, secOff, nsecOff := statLayout()
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_fs_open(ptr %%path, i64 %%flags, i64 %%mode) {
entry:
  %%f32 = trunc i64 %%flags to i32
  %%m32 = trunc i64 %%mode to i32
  %%fd = call i32 (ptr, i32, ...) @open(ptr %%path, i32 %%f32, i32 %%m32)
  %%failed = icmp slt i32 %%fd, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable
ok:
  %%r = sext i32 %%fd to i64
  ret i64 %%r
}

define i64 @__kml_fs_fdrw(i64 %%fd, ptr %%buf, i64 %%len, i64 %%position, i1 %%isWrite) {
entry:
  %%f32 = trunc i64 %%fd to i32
  %%seek = icmp sge i64 %%position, 0
  br i1 %%seek, label %%doseek, label %%doio
doseek:
  %%s = call i64 @lseek(i32 %%f32, i64 %%position, i32 0)
  br label %%doio
doio:
  br i1 %%isWrite, label %%dw, label %%dr
dw:
  %%wn = call i64 @write(i32 %%f32, ptr %%buf, i64 %%len)
  br label %%chk
dr:
  %%rn = call i64 @read(i32 %%f32, ptr %%buf, i64 %%len)
  br label %%chk
chk:
  %%n = phi i64 [ %%wn, %%dw ], [ %%rn, %%dr ]
  %%failed = icmp slt i64 %%n, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr null)
  unreachable
ok:
  ret i64 %%n
}

define { i64, i64, i64 } @__kml_fs_fstat(i64 %%fd) {
entry:
  %%f32 = trunc i64 %%fd to i32
  %%buf = alloca [256 x i8], align 8
  %%r = call i32 @fstat(i32 %%f32, ptr %%buf)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr null)
  unreachable
ok:
  %%modep = getelementptr i8, ptr %%buf, i64 %d
  %%modew = load i%d, ptr %%modep, align 2
  %%mode = zext i%d %%modew to i64
  %%sizep = getelementptr i8, ptr %%buf, i64 %d
  %%size = load i64, ptr %%sizep, align 8
  %%secp = getelementptr i8, ptr %%buf, i64 %d
  %%sec = load i64, ptr %%secp, align 8
  %%nsecp = getelementptr i8, ptr %%buf, i64 %d
  %%nsec = load i64, ptr %%nsecp, align 8
  %%ms1 = mul i64 %%sec, 1000
  %%ms2 = sdiv i64 %%nsec, 1000000
  %%ms = add i64 %%ms1, %%ms2
  %%r0 = insertvalue { i64, i64, i64 } undef, i64 %%mode, 0
  %%r1 = insertvalue { i64, i64, i64 } %%r0, i64 %%size, 1
  %%r2 = insertvalue { i64, i64, i64 } %%r1, i64 %%ms, 2
  ret { i64, i64, i64 } %%r2
}`, openDesc, fdDesc, fdDesc, modeOff, modeBits, modeBits, sizeOff, secOff, nsecOff))
}
