package llvm

import (
	"fmt"
	"strings"
	"syscall"
)

// ensureErrnoCode declares __kml_errno_code(i32 errno) -> ptr: maps an errno to
// the Node error-code NAME string (`ENOENT`, `EISDIR`, …) that `err.code`
// exposes, or null for an unmapped value. Building the switch from Go's
// per-platform `syscall` constants keeps the numeric values correct on both
// Linux and macOS (they differ for e.g. ENAMETOOLONG/ELOOP/ENOTEMPTY).
// errnoCodePair is one (numeric errno, Node code name) mapping. The numeric
// value is the target platform's, so both ensureErrnoCode (errno→name) and
// ensureErrnoDesc (errno→libuv description) switch on the same labels.
type errnoCodePair struct {
	v    int
	name string
}

// errnoCodePairs is the single (errno, code-name) table both ensureErrnoCode and
// ensureErrnoDesc build their switches from, so the two can never drift on which
// numeric values map to which code. Windows uses the shim's Linux errno numbers.
func (e *Emitter) errnoCodePairs() []errnoCodePair {
	if e.opts.Target.OS() == "windows" {
		pairs := make([]errnoCodePair, 0, len(linuxErrnoPairs))
		for _, p := range linuxErrnoPairs {
			pairs = append(pairs, errnoCodePair{p[0].(int), p[1].(string)})
		}
		return pairs
	}
	return []errnoCodePair{
		{int(syscall.EPERM), "EPERM"}, {int(syscall.ENOENT), "ENOENT"},
		{int(syscall.EIO), "EIO"}, {int(syscall.EBADF), "EBADF"},
		{int(syscall.EACCES), "EACCES"}, {int(syscall.EEXIST), "EEXIST"},
		{int(syscall.ENOTDIR), "ENOTDIR"}, {int(syscall.EISDIR), "EISDIR"},
		{int(syscall.EINVAL), "EINVAL"}, {int(syscall.EMFILE), "EMFILE"},
		{int(syscall.ENFILE), "ENFILE"}, {int(syscall.ENOSPC), "ENOSPC"},
		{int(syscall.EROFS), "EROFS"}, {int(syscall.EBUSY), "EBUSY"},
		{int(syscall.ENOTEMPTY), "ENOTEMPTY"}, {int(syscall.ELOOP), "ELOOP"},
		{int(syscall.ENAMETOOLONG), "ENAMETOOLONG"}, {int(syscall.EXDEV), "EXDEV"},
		{int(syscall.EAGAIN), "EAGAIN"}, {int(syscall.EPIPE), "EPIPE"},
		{int(syscall.EFBIG), "EFBIG"}, {int(syscall.ENODEV), "ENODEV"},
		{int(syscall.ESPIPE), "ESPIPE"}, {int(syscall.EMLINK), "EMLINK"},
		{int(syscall.ESRCH), "ESRCH"}, {int(syscall.ECHILD), "ECHILD"},
		// Socket/bind errnos (TDD-00215 Stage 2: server 'error' event .code).
		{int(syscall.EADDRINUSE), "EADDRINUSE"}, {int(syscall.EADDRNOTAVAIL), "EADDRNOTAVAIL"},
		{int(syscall.ECONNRESET), "ECONNRESET"}, {int(syscall.ECONNREFUSED), "ECONNREFUSED"},
		// Async net.connect failure errnos (ADR-01021: socket 'error' event .code).
		{int(syscall.ETIMEDOUT), "ETIMEDOUT"}, {int(syscall.EHOSTUNREACH), "EHOSTUNREACH"},
		{int(syscall.ENETUNREACH), "ENETUNREACH"}, {int(syscall.ECONNABORTED), "ECONNABORTED"},
		// spawnSync's maxBuffer overrun (ADR-01080).
		{int(syscall.ENOBUFS), "ENOBUFS"},
	}
}

// libuvErrnoDesc is the errno-code → libuv canonical description string that
// Node's fs error `.message` embeds (`<CODE>: <desc>, <syscall> …`). Verbatim
// from libuv's `uv-common.c` `uv_strerror` table so the byte-exact message
// matches Node (ADR-01000). Any code absent here falls back to `strerror`.
var libuvErrnoDesc = map[string]string{
	"EPERM":         "operation not permitted",
	"ENOENT":        "no such file or directory",
	"EIO":           "i/o error",
	"UNKNOWN":       "unknown error",
	"EBADF":         "bad file descriptor",
	"EACCES":        "permission denied",
	"EEXIST":        "file already exists",
	"ENOTDIR":       "not a directory",
	"EISDIR":        "illegal operation on a directory",
	"EINVAL":        "invalid argument",
	"EMFILE":        "too many open files",
	"ENFILE":        "file table overflow",
	"ENOSPC":        "no space left on device",
	"EROFS":         "read-only file system",
	"EBUSY":         "resource busy or locked",
	"ENOTEMPTY":     "directory not empty",
	"ELOOP":         "too many symbolic links encountered",
	"ENAMETOOLONG":  "name too long",
	"EXDEV":         "cross-device link not permitted",
	"EAGAIN":        "resource temporarily unavailable",
	"EPIPE":         "broken pipe",
	"EFBIG":         "file too large",
	"ENODEV":        "no such device",
	"ESPIPE":        "invalid seek",
	"EMLINK":        "too many links",
	"EADDRINUSE":    "address already in use",
	"EADDRNOTAVAIL": "address not available",
	"ECONNRESET":    "connection reset by peer",
	"ECONNREFUSED":  "connection refused",
}

func (e *Emitter) ensureErrnoCode() {
	if e.usedErrnoCode {
		return
	}
	e.usedErrnoCode = true
	pairs := e.errnoCodePairs()
	seen := map[int]bool{}
	var cases, blocks strings.Builder
	for _, p := range pairs {
		if seen[p.v] {
			continue // some names alias one value (EAGAIN/EWOULDBLOCK) — one label per value
		}
		seen[p.v] = true
		s := e.internString(p.name)
		lbl := "ec_" + p.name
		cases.WriteString(fmt.Sprintf("    i32 %d, label %%%s\n", p.v, lbl))
		blocks.WriteString(fmt.Sprintf("%s:\n  ret ptr %s\n", lbl, s))
	}
	e.emitGlobal(fmt.Sprintf(`define ptr @__kml_errno_code(i32 %%e) {
entry:
  switch i32 %%e, label %%unknown [
%s  ]
%sunknown:
  ret ptr null
}`, cases.String(), blocks.String()))
	e.emitUVErrno()
}

// uvWinErrno is libuv's fixed Windows errno numbering (uv/errno.h: the UV__E*
// fallbacks, which Windows always takes). Node's `err.errno` is this value
// there — `ENOENT` is -4058, not -2 — so the error-object boundary translates
// the shim's Linux-numbered errno through it. Anything libuv has no name for is
// UV_UNKNOWN.
var uvWinErrno = map[string]int{
	"E2BIG": -4093, "EACCES": -4092, "EADDRINUSE": -4091, "EADDRNOTAVAIL": -4090,
	"EAFNOSUPPORT": -4089, "EAGAIN": -4088, "EALREADY": -4084, "EBADF": -4083,
	"EBUSY": -4082, "ECANCELED": -4081, "ECONNABORTED": -4079, "ECONNREFUSED": -4078,
	"ECONNRESET": -4077, "EDESTADDRREQ": -4076, "EEXIST": -4075, "EFAULT": -4074,
	"EHOSTUNREACH": -4073, "EINTR": -4072, "EINVAL": -4071, "EIO": -4070,
	"EISCONN": -4069, "EISDIR": -4068, "ELOOP": -4067, "EMFILE": -4066,
	"EMSGSIZE": -4065, "ENAMETOOLONG": -4064, "ENETDOWN": -4063, "ENETUNREACH": -4062,
	"ENFILE": -4061, "ENOBUFS": -4060, "ENODEV": -4059, "ENOENT": -4058,
	"ENOMEM": -4057, "ENONET": -4056, "ENOSPC": -4055, "ENOSYS": -4054,
	"ENOTCONN": -4053, "ENOTDIR": -4052, "ENOTEMPTY": -4051, "ENOTSOCK": -4050,
	"ENOTSUP": -4049, "EOPNOTSUPP": -4049, "EPERM": -4048, "EPIPE": -4047,
	"EPROTO": -4046, "EPROTONOSUPPORT": -4045, "EPROTOTYPE": -4044, "EROFS": -4043,
	"ESHUTDOWN": -4042, "ESPIPE": -4041, "ESRCH": -4040, "ETIMEDOUT": -4039,
	"ETXTBSY": -4038, "EXDEV": -4037, "EFBIG": -4036, "ENOPROTOOPT": -4035,
	"ERANGE": -4034, "ENXIO": -4033, "EMLINK": -4032, "EHOSTDOWN": -4031,
	"ENOTTY": -4029, "EILSEQ": -4027, "EOVERFLOW": -4026,
}

const uvWinUnknown = -4094

// emitUVErrno defines __kml_uv_errno(i32 errno) -> i32: the value Node reports
// as `err.errno`. On POSIX libuv's numbers are the negated OS errno; on Windows
// they are libuv's own table (uvWinErrno).
func (e *Emitter) emitUVErrno() {
	if e.opts.Target.OS() != "windows" {
		e.emitGlobal(`define i32 @__kml_uv_errno(i32 %e) {
entry:
  %n = sub i32 0, %e
  ret i32 %n
}`)
		return
	}
	seen := map[int]bool{}
	var cases, blocks strings.Builder
	for _, p := range e.errnoCodePairs() {
		uv, ok := uvWinErrno[p.name]
		if !ok || seen[p.v] {
			continue
		}
		seen[p.v] = true
		cases.WriteString(fmt.Sprintf("    i32 %d, label %%uv_%s\n", p.v, p.name))
		blocks.WriteString(fmt.Sprintf("uv_%s:\n  ret i32 %d\n", p.name, uv))
	}
	e.emitGlobal(fmt.Sprintf(`define i32 @__kml_uv_errno(i32 %%e) {
entry:
  %%zero = icmp eq i32 %%e, 0
  br i1 %%zero, label %%none, label %%look
none:
  ret i32 0
look:
  switch i32 %%e, label %%unknown [
%s  ]
%sunknown:
  ret i32 %d
}`, cases.String(), blocks.String(), uvWinUnknown))
}

// ensureErrnoDesc declares __kml_errno_desc(i32 errno) -> ptr: the libuv
// canonical description string Node's fs `.message` embeds ("no such file or
// directory", …), or null when the code isn't in the table (the caller then
// falls back to strerror). Built from the same (errno, code) pairs as
// ensureErrnoCode so the numeric values stay correct on every target
// (ADR-01000).
func (e *Emitter) ensureErrnoDesc() {
	if e.usedErrnoDesc {
		return
	}
	e.usedErrnoDesc = true
	seen := map[int]bool{}
	var cases, blocks strings.Builder
	for _, p := range e.errnoCodePairs() {
		if seen[p.v] {
			continue
		}
		seen[p.v] = true
		desc, ok := libuvErrnoDesc[p.name]
		if !ok {
			continue // unmapped code → default (strerror) branch
		}
		s := e.internString(desc)
		lbl := "ed_" + p.name
		cases.WriteString(fmt.Sprintf("    i32 %d, label %%%s\n", p.v, lbl))
		blocks.WriteString(fmt.Sprintf("%s:\n  ret ptr %s\n", lbl, s))
	}
	e.emitGlobal(fmt.Sprintf(`define ptr @__kml_errno_desc(i32 %%e) {
entry:
  switch i32 %%e, label %%unknown [
%s  ]
%sunknown:
  ret ptr null
}`, cases.String(), blocks.String()))
}

// ensureFsErrmsg declares __kml_fs_errmsg(code, desc, syscall, path, dest) ->
// ptr: assembles Node's exact fs-error `.message` string
//
//	<code>: <desc>, <syscall>[ '<path>'[ -> '<dest>']]
//
// The path clause appears only when `path` is non-null and non-empty, and the
// ` -> '<dest>'` arrow only for a two-path op (rename/copyFile). All three
// message shapes are handled by selecting the sprintf format string, so the
// builder stays a single branch-free block (ADR-01000). Returned buffer is a
// headered KML string (concat/=== ready).
func (e *Emitter) ensureFsErrmsg() {
	if e.usedFsErrmsg {
		return
	}
	e.usedFsErrmsg = true
	e.ensureStrHeaderRuntime()
	e.ensureStrlen()
	e.ensureSprintf()
	empty := e.internString("")
	fmtBase := e.internString("%s: %s, %s")
	fmtPath := e.internString("%s: %s, %s '%s'")
	fmtBoth := e.internString("%s: %s, %s '%s' -> '%s'")
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_fs_errmsg(ptr %%code, ptr %%desc, ptr %%sc, ptr %%path, ptr %%dest) {
entry:
  %%p_null = icmp eq ptr %%path, null
  %%path2 = select i1 %%p_null, ptr %s, ptr %%path
  %%d_null = icmp eq ptr %%dest, null
  %%dest2 = select i1 %%d_null, ptr %s, ptr %%dest
  %%lc = call i64 @strlen(ptr %%code)
  %%ld = call i64 @strlen(ptr %%desc)
  %%ls = call i64 @strlen(ptr %%sc)
  %%lp = call i64 @strlen(ptr %%path2)
  %%ldst = call i64 @strlen(ptr %%dest2)
  %%s1 = add i64 %%lc, %%ld
  %%s2 = add i64 %%s1, %%ls
  %%s3 = add i64 %%s2, %%lp
  %%s4 = add i64 %%s3, %%ldst
  %%bufsize = add i64 %%s4, 32
  %%buf = call ptr @__kml_str_alloc(i64 %%bufsize)
  %%has_p = icmp ne i64 %%lp, 0
  %%has_d = icmp ne i64 %%ldst, 0
  %%f1 = select i1 %%has_d, ptr %s, ptr %s
  %%fmt = select i1 %%has_p, ptr %%f1, ptr %s
  call i32 (ptr, ptr, ...) @sprintf(ptr %%buf, ptr %%fmt, ptr %%code, ptr %%desc, ptr %%sc, ptr %%path2, ptr %%dest2)
  call void @__kml_str_finalize(ptr %%buf)
  ret ptr %%buf
}`, empty, empty, fmtBoth, fmtPath, fmtBase))
}

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
	e.ensureCalloc() // errobj is calloc'd so trailing fields (cause/address/port) default clean
	e.ensureStrlen()
	e.ensureSprintf()
	e.ensureStrHeaderRuntime() // error .message must be headered for concat/=== (TDD-00120)
	e.ensureExceptionHelpers()
	accessor := e.errnoAccessor()
	e.ensureErrnoAccessor()
	e.ensureStrerror()
	e.ensureErrnoCode()
	e.ensureErrnoDesc()
	e.ensureFsErrmsg()
	empty := e.internString("")
	errNamePtr := e.internString("Error")
	// The full 6-field errorObjType: kind/message/name PLUS the Node error-code
	// trio code/errcode/errstr, so `err.code === 'ENOENT'`/'EISDIR' (the
	// canonical fs idiom) matches, `.errno` carries the raw errno, and `.errstr`
	// the strerror text. Previously this built a truncated 3-field object (and
	// under-allocated 24 bytes for the 6-field type), leaving `.code` unset.
	//
	// The message is now assembled by __kml_fs_errmsg to Node's byte-exact
	// `<CODE>: <libuv-desc>, <syscall>[ '<path>'[ -> '<dest>']]` form (ADR-01000).
	// `opdesc` is retained in the ABI (every call site still passes its verb) but
	// no longer appears in the message. __kml_fs_throw2 carries the two-path
	// (rename/copyFile) `<src> -> <dest>` form; __kml_fs_throw forwards dest=null.
	eIR := errorObjType.StructIR()
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_throw2(ptr %%opdesc, ptr %%syscall, ptr %%path, ptr %%dest) {
entry:
  %%errno_ptr = call ptr @%s()
  %%errno_val = load i32, ptr %%errno_ptr, align 4
  %%errobj = call ptr @__kml_fs_error_new(i32 %%errno_val, ptr %%syscall, ptr %%path, ptr %%dest)
  call void @__kml_throw(ptr %%errobj)
  ret void
}
define ptr @__kml_fs_error_new(i32 %%errno_val, ptr %%syscall, ptr %%path, ptr %%dest) {
entry:
  %%errmsg = call ptr @strerror(i32 %%errno_val)
  %%code_raw = call ptr @__kml_errno_code(i32 %%errno_val)
  %%code_null = icmp eq ptr %%code_raw, null
  %%code = select i1 %%code_null, ptr %s, ptr %%code_raw
  %%desc_raw = call ptr @__kml_errno_desc(i32 %%errno_val)
  %%desc_null = icmp eq ptr %%desc_raw, null
  %%desc = select i1 %%desc_null, ptr %%errmsg, ptr %%desc_raw
  %%buf = call ptr @__kml_fs_errmsg(ptr %%code, ptr %%desc, ptr %%syscall, ptr %%path, ptr %%dest)
  %%errno_d = sitofp i32 %%errno_val to double
  %%errobj = call ptr @calloc(i64 1, i64 %d)
  %%errobj.kind = getelementptr %s, ptr %%errobj, i32 0, i32 0
  store i64 281474976710656, ptr %%errobj.kind, align 8
  %%errobj.msg = getelementptr %s, ptr %%errobj, i32 0, i32 1
  store ptr %%buf, ptr %%errobj.msg, align 8
  %%errobj.name = getelementptr %s, ptr %%errobj, i32 0, i32 2
  store ptr %s, ptr %%errobj.name, align 8
  %%errobj.code = getelementptr %s, ptr %%errobj, i32 0, i32 3
  store ptr %%code_raw, ptr %%errobj.code, align 8
  %%errobj.errcode = getelementptr %s, ptr %%errobj, i32 0, i32 4
  store double %%errno_d, ptr %%errobj.errcode, align 8
  %%errobj.errstr = getelementptr %s, ptr %%errobj, i32 0, i32 5
  store ptr %%errmsg, ptr %%errobj.errstr, align 8
  %%errobj.syscall = getelementptr %s, ptr %%errobj, i32 0, i32 6
  store ptr %%syscall, ptr %%errobj.syscall, align 8
  %%errobj.path = getelementptr %s, ptr %%errobj, i32 0, i32 7
  store ptr %%path, ptr %%errobj.path, align 8
  %%errno_neg = call i32 @__kml_uv_errno(i32 %%errno_val)
  %%errno_negd = sitofp i32 %%errno_neg to double
  %%errobj.errno = getelementptr %s, ptr %%errobj, i32 0, i32 8
  store double %%errno_negd, ptr %%errobj.errno, align 8
  %%errobj.dest = getelementptr %s, ptr %%errobj, i32 0, i32 9
  store ptr %%dest, ptr %%errobj.dest, align 8
  ret ptr %%errobj
}
define void @__kml_fs_throw(ptr %%opdesc, ptr %%syscall, ptr %%path) {
entry:
  call void @__kml_fs_throw2(ptr %%opdesc, ptr %%syscall, ptr %%path, ptr null)
  ret void
}`, accessor, empty, errorObjType.StructSize(), eIR, eIR, eIR, errNamePtr, eIR, eIR, eIR, eIR, eIR, eIR, eIR))
}

// ensureStatDecl emits the `declare i32 @stat` exactly once. Shared by
// ensureFsStat (statSync) and ensureFsReadFileRaw's EISDIR guard so the two
// callers don't emit competing declarations.
func (e *Emitter) ensureStatDecl() {
	if e.usedStatDecl {
		return
	}
	e.usedStatDecl = true
	e.emitFSDecl("stat", "i32", []string{"ptr", "ptr"})
}

// emitFSDecl declares a libc file-metadata entry point. On Intel macOS the
// plain `stat`/`lstat`/`fstat`/`opendir`/`readdir` symbols are the legacy
// 32-bit-inode variants with a different struct layout; the 64-bit-inode
// layout this compiler's stat/dirent code was written against (verified on
// Apple Silicon, where it is the only one) is exported there under
// `<name>$INODE64`. To keep every call site as it is, an *internal* function
// of the plain name wraps the `$INODE64` export on darwin/amd64; everywhere
// else this is the plain declaration (ADR-00733). Unverified on Intel
// hardware as of writing — the macOS x64 CI lane is its test.
func (e *Emitter) emitFSDecl(name, ret string, params []string) {
	sig := strings.Join(params, " noundef, ") + " noundef"
	if !(e.opts.Target.OS() == "darwin" && e.opts.Target.Arch() == "amd64") {
		e.emitGlobal(fmt.Sprintf("declare %s @%s(%s)", ret, name, sig))
		return
	}
	var args, fparams []string
	for i, p := range params {
		fparams = append(fparams, fmt.Sprintf("%s %%a%d", p, i))
		args = append(args, fmt.Sprintf("%s %%a%d", p, i))
	}
	e.emitGlobal(fmt.Sprintf("declare %s @\"%s$INODE64\"(%s)", ret, name, sig))
	e.emitGlobal(fmt.Sprintf(`
define internal %s @%s(%s) {
entry:
  %%r = call %s @"%s$INODE64"(%s)
  ret %s %%r
}`, ret, name, strings.Join(fparams, ", "), ret, name, strings.Join(args, ", "), ret))
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

// ensureFsReadFd declares __kml_fs_read_fd_all(fd) -> {ptr, i64}: every
// byte read(2) returns from an open descriptor until end of file (stdin is
// fd 0), in a buffer that doubles as it fills, NUL-terminated past its
// length. A read error throws Node's `<CODE>: <desc>, read` (no path), and
// an interrupted read retries.
func (e *Emitter) ensureFsReadFd() {
	if e.usedFsReadFd {
		return
	}
	e.usedFsReadFd = true
	e.ensureFsThrow()
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureReadDecl()
	e.ensureErrnoAccessor()
	accessor := e.errnoAccessor()
	e.emitGlobal(fmt.Sprintf(`
define { ptr, i64 } @__kml_fs_read_fd_all(i32 %%fd) {
entry:
  %%buf0 = call ptr @malloc(i64 65537)
  br label %%loop
loop:
  %%buf = phi ptr [ %%buf0, %%entry ], [ %%buf, %%retry ], [ %%buf, %%more ], [ %%nbuf, %%grow ]
  %%cap = phi i64 [ 65536, %%entry ], [ %%cap, %%retry ], [ %%cap, %%more ], [ %%ncap, %%grow ]
  %%len = phi i64 [ 0, %%entry ], [ %%len, %%retry ], [ %%len2, %%more ], [ %%len2, %%grow ]
  %%dst = getelementptr i8, ptr %%buf, i64 %%len
  %%room = sub i64 %%cap, %%len
  %%n = call i64 @read(i32 %%fd, ptr %%dst, i64 %%room)
  %%neg = icmp slt i64 %%n, 0
  br i1 %%neg, label %%err, label %%got
err:
  %%ep = call ptr @%s()
  %%ev = load i32, ptr %%ep, align 4
  %%intr = icmp eq i32 %%ev, 4
  br i1 %%intr, label %%retry, label %%fail
retry:
  br label %%loop
fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr null)
  unreachable
got:
  %%eof = icmp eq i64 %%n, 0
  br i1 %%eof, label %%done, label %%more0
more0:
  %%len2 = add i64 %%len, %%n
  %%full = icmp eq i64 %%len2, %%cap
  br i1 %%full, label %%grow, label %%more
more:
  br label %%loop
grow:
  %%ncap = mul i64 %%cap, 2
  %%nsz = add i64 %%ncap, 1
  %%nbuf = call ptr @realloc(ptr %%buf, i64 %%nsz)
  br label %%loop
done:
  %%endp = getelementptr i8, ptr %%buf, i64 %%len
  store i8 0, ptr %%endp, align 1
  %%r0 = insertvalue { ptr, i64 } undef, ptr %%buf, 0
  %%r1 = insertvalue { ptr, i64 } %%r0, i64 %%len, 1
  ret { ptr, i64 } %%r1
}`, accessor, e.internString("read"), e.internString("read")))
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
	e.ensureStatDecl()
	e.ensureErrnoAccessor()
	e.emitGlobal("declare i32 @fseek(ptr noundef, i64 noundef, i32 noundef)")
	e.emitGlobal("declare i64 @ftell(ptr noundef)")
	e.ensureFread()
	e.ensureRealloc()
	modePtr := e.internString("rb")
	opDescPtr := e.internString("cannot open file for reading")
	// EISDIR guard (ADR-00692): fopen(2) happily opens a directory on both Linux
	// and macOS, after which fread returns garbage/zero bytes. Node instead
	// throws `EISDIR: illegal operation on a directory, read`. Detect the
	// directory up front with stat(2) + the host struct-stat mode field, set
	// errno to EISDIR, and route through the shared __kml_fs_throw so `.code`,
	// `.errno`, and the Node-shaped message all come from the one code path.
	L := e.statLayout()
	modeLoadTy := fmt.Sprintf("i%d", L.modeBits)
	modeReg := "%mode_raw"
	if L.modeBits < 32 {
		modeReg = "%mode_ext"
	}
	modeExtLL := ""
	if L.modeBits < 32 {
		modeExtLL = fmt.Sprintf("  %%mode_ext = zext %s %%mode_raw to i32\n", modeLoadTy)
	}
	eisdirOpDescPtr := e.internString("read")
	scRead := e.internString("read")
	scOpen := e.internString("open")
	e.emitGlobal(fmt.Sprintf(`
define { ptr, i64 } @__kml_fs_read_file_raw(ptr %%path) {
entry:
  %%stbuf = alloca [256 x i8], align 8
  %%statr = call i32 @stat(ptr %%path, ptr %%stbuf)
  %%statok = icmp eq i32 %%statr, 0
  br i1 %%statok, label %%checkdir, label %%doopen

checkdir:
  %%modep = getelementptr i8, ptr %%stbuf, i64 %d
  %%mode_raw = load %s, ptr %%modep, align 1
%s  %%fmt = and i32 %s, 61440
  %%isdir = icmp eq i32 %%fmt, 16384
  br i1 %%isdir, label %%eisdir, label %%doopen

eisdir:
  %%eptr = call ptr @%s()
  store i32 %d, ptr %%eptr, align 4
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr null)
  unreachable

doopen:
  %%f = call ptr @fopen(ptr %%path, ptr %s)
  %%isnull = icmp eq ptr %%f, null
  br i1 %%isnull, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable

ok:
  %%seekend = call i32 @fseek(ptr %%f, i64 0, i32 2)
  %%size = call i64 @ftell(ptr %%f)
  %%seekset = call i32 @fseek(ptr %%f, i64 0, i32 0)
  ; A reported size of 0 (or a seek/ftell failure, size < 0) means the size is
  ; not known up front — the case for /proc and /sys pseudo-files, which report
  ; st_size 0 yet read real bytes. fread-by-known-size would return "". Fall
  ; back to a grow-until-EOF loop (which also handles a genuinely empty file:
  ; the first fread returns 0 and we terminate at length 0). ADR-00811.
  %%nonpos = icmp sle i64 %%size, 0
  br i1 %%nonpos, label %%grow, label %%sized

sized:
  %%sizep1 = add i64 %%size, 1
  %%buf = call ptr @malloc(i64 %%sizep1)
  %%nread = call i64 @fread(ptr %%buf, i64 1, i64 %%size, ptr %%f)
  %%termptr = getelementptr i8, ptr %%buf, i64 %%size
  store i8 0, ptr %%termptr, align 1
  call i32 @fclose(ptr %%f)
  %%r0 = insertvalue { ptr, i64 } undef, ptr %%buf, 0
  %%r1 = insertvalue { ptr, i64 } %%r0, i64 %%size, 1
  ret { ptr, i64 } %%r1

grow:
  %%capslot = alloca i64, align 8
  %%bufslot = alloca ptr, align 8
  %%totslot = alloca i64, align 8
  store i64 8192, ptr %%capslot, align 8
  %%gb0 = call ptr @malloc(i64 8192)
  store ptr %%gb0, ptr %%bufslot, align 8
  store i64 0, ptr %%totslot, align 8
  br label %%growloop

growloop:
  %%gcap = load i64, ptr %%capslot, align 8
  %%gtot = load i64, ptr %%totslot, align 8
  %%gneed = add i64 %%gtot, 4097
  %%gtight = icmp ugt i64 %%gneed, %%gcap
  br i1 %%gtight, label %%growbuf, label %%doread

growbuf:
  %%gcap1 = load i64, ptr %%capslot, align 8
  %%gbuf1 = load ptr, ptr %%bufslot, align 8
  %%ncap = shl i64 %%gcap1, 1
  %%nbuf = call ptr @realloc(ptr %%gbuf1, i64 %%ncap)
  store i64 %%ncap, ptr %%capslot, align 8
  store ptr %%nbuf, ptr %%bufslot, align 8
  br label %%doread

doread:
  %%dbuf = load ptr, ptr %%bufslot, align 8
  %%dtot = load i64, ptr %%totslot, align 8
  %%dst = getelementptr i8, ptr %%dbuf, i64 %%dtot
  %%dread = call i64 @fread(ptr %%dst, i64 1, i64 4096, ptr %%f)
  %%dtot2 = add i64 %%dtot, %%dread
  store i64 %%dtot2, ptr %%totslot, align 8
  %%deof = icmp ult i64 %%dread, 4096
  br i1 %%deof, label %%growdone, label %%growloop

growdone:
  %%fbuf = load ptr, ptr %%bufslot, align 8
  %%ftot = load i64, ptr %%totslot, align 8
  %%fterm = getelementptr i8, ptr %%fbuf, i64 %%ftot
  store i8 0, ptr %%fterm, align 1
  call i32 @fclose(ptr %%f)
  %%fr0 = insertvalue { ptr, i64 } undef, ptr %%fbuf, 0
  %%fr1 = insertvalue { ptr, i64 } %%fr0, i64 %%ftot, 1
  ret { ptr, i64 } %%fr1
}`, L.modeOff, modeLoadTy, modeExtLL, modeReg, e.errnoAccessor(), e.errnoEISDIR(), eisdirOpDescPtr, scRead, modePtr, opDescPtr, scOpen))
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
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable

ok:
  %%len = call i64 @strlen(ptr %%data)
  %%nwritten = call i64 @fwrite(ptr %%data, i64 1, i64 %%len, ptr %%f)
  call i32 @fclose(ptr %%f)
  ret void
}`, fnName, modePtr, opDescPtr, e.internString("open")))
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
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable

ok:
  %%nwritten = call i64 @fwrite(ptr %%data, i64 1, i64 %%len, ptr %%f)
  call i32 @fclose(ptr %%f)
  ret void
}`, fnName, modePtr, opDescPtr, e.internString("open")))
}

// ensureChmodDecl declares C `chmod` exactly once — shared by ensureFsPathOps
// (chmodSync) and ensureFsChmodCreated (writeFileSync's `{ mode }`). On Windows
// it is the CRT `_chmod` shim (win32fs.c), which honours only the write bit;
// callers use it best-effort. emitGlobal does not dedup, so a shared guard
// keeps the declaration from being emitted twice.
func (e *Emitter) ensureChmodDecl() {
	if e.usedChmodDecl {
		return
	}
	e.usedChmodDecl = true
	e.emitGlobal("declare i32 @chmod(ptr noundef, i32 noundef)")
}

// ensureFsChmodCreated declares __kml_fs_chmod_created(path, mode, existed):
// applies `mode` via chmod only when `existed` is false — i.e. only to a file
// this write just created, matching Node's writeFileSync/appendFileSync `{ mode
// }` (open(2) applies its mode arg to new files and ignores it for existing
// ones). Best-effort: a chmod failure on a file we just created as its owner is
// not surfaced (the write itself already succeeded), so no throw path (ADR-00988).
func (e *Emitter) ensureFsChmodCreated() {
	if e.usedFsChmodCreated {
		return
	}
	e.usedFsChmodCreated = true
	e.ensureChmodDecl()
	// umask(2) has no portable read-only form: umask(0) sets it to 0 and returns
	// the prior value, which is immediately restored. This runs only on the
	// synchronous, main-thread writeFileSync/appendFileSync path, so the brief
	// window carries no cross-thread race. `mode & ~umask` reproduces open(2)'s
	// own creation masking, which Node's `{ mode }` rides on.
	e.emitGlobal("declare i32 @umask(i32 noundef)")
	e.emitGlobal(`
define void @__kml_fs_chmod_created(ptr %path, i64 %mode, i1 %existed) {
entry:
  br i1 %existed, label %done, label %doit
doit:
  %m32 = trunc i64 %mode to i32
  %old = call i32 @umask(i32 0)
  %restore = call i32 @umask(i32 %old)
  %keep = xor i32 %old, -1
  %eff = and i32 %m32, %keep
  %r = call i32 @chmod(ptr %path, i32 %eff)
  br label %done
done:
  ret void
}`)
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

// ensureFsUnlink declares __kml_fs_unlink: deletes a file via POSIX
// unlink() (libc on POSIX hosts, the Win32 layer's on Windows). Not ANSI C
// remove(): remove() also deletes an empty directory, where Node's
// fs.unlinkSync throws (EISDIR on Linux, EPERM on macOS/Windows) —
// ADR-00736. Throws on failure.
func (e *Emitter) ensureFsUnlink() {
	if e.usedFsUnlink {
		return
	}
	e.usedFsUnlink = true
	e.ensureFsThrow()
	e.emitGlobal("declare i32 @unlink(ptr noundef)")
	opDescPtr := e.internString("cannot delete file")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_unlink(ptr %%path) {
entry:
  %%r = call i32 @unlink(ptr %%path)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable

ok:
  ret void
}`, opDescPtr, e.internString("unlink")))
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
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable

ok:
  ret void
}`, opDescPtr, e.internString("rmdir")))
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
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable

ok:
  ret void
}`, opDescPtr, e.internString("mkdir")))
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
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable
ok:
  ret void
}`, opDescPtr, e.internString("mkdir")))
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
  call void @__kml_fs_throw2(ptr %s, ptr %s, ptr %%oldpath, ptr %%newpath)
  unreachable

ok:
  ret void
}`, opDescPtr, e.internString("rename")))
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
func (e *Emitter) direntNameOffset() int {
	if e.opts.Target.OS() == "windows" {
		return 9 // kml_dirent: d_ino u32, d_reclen u16, d_namlen u16, d_type u8, d_name
	}
	if e.opts.Target.OS() == "darwin" {
		return 21
	}
	return 19
}

// direntTypeOffset is the byte offset of `d_type` in the host's `struct
// dirent` — the file-type byte fs.readdirSync(withFileTypes) reads (ADR-00752).
// glibc: d_ino(8)+d_off(8)+d_reclen(2) → 18. Darwin: d_ino(8)+d_seekoff(8)+
// d_reclen(2)+d_namlen(2) → 20. Windows: the shim's kml_dirent places d_type
// right after d_namlen, at offset 8 (win32fs.c), populated from the Win32
// FindFirstFile attributes since mingw's own dirent has no d_type.
func (e *Emitter) direntTypeOffset() int {
	if e.opts.Target.OS() == "windows" {
		return 8
	}
	if e.opts.Target.OS() == "darwin" {
		return 20
	}
	return 18
}

// ensureFsReaddir declares __kml_fs_readdir: lists a directory's entries
// (excluding "." and "..", matching real Node's fs.readdirSync) via POSIX
// opendir/readdir/closedir, returning a {ptr, i64} string[] aggregate grown
// with the same realloc-doubling shape __kml_fetch
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
	e.emitFSDecl("opendir", "ptr", []string{"ptr"})
	e.emitFSDecl("readdir", "ptr", []string{"ptr"})
	e.emitGlobal("declare i32 @closedir(ptr noundef)")
	e.emitGlobal("declare ptr @strdup(ptr noundef)")
	e.ensureQsort()
	opDescPtr := e.internString("cannot open directory")
	dotPtr := e.internString(".")
	dotdotPtr := e.internString("..")
	// Node's entries come sorted by name: libuv's uv_fs_scandir sorts them
	// with strcmp. A name slot holds the string; a Dirent's first field does.
	e.emitGlobal(`
define i32 @__kml_fs_cmp_name(ptr %a, ptr %b) {
entry:
  %sa = load ptr, ptr %a, align 8
  %sb = load ptr, ptr %b, align 8
  %r = call i32 @strcmp(ptr %sa, ptr %sb)
  ret i32 %r
}

define i32 @__kml_fs_cmp_dirent(ptr %a, ptr %b) {
entry:
  %da = load ptr, ptr %a, align 8
  %db = load ptr, ptr %b, align 8
  %sa = load ptr, ptr %da, align 8
  %sb = load ptr, ptr %db, align 8
  %r = call i32 @strcmp(ptr %sa, ptr %sb)
  ret i32 %r
}`)
	e.emitGlobal(fmt.Sprintf(`
define {ptr, i64} @__kml_fs_readdir(ptr %%path, i1 %%withTypes) {
entry:
  %%dir = call ptr @opendir(ptr %%path)
  %%dirisnull = icmp eq ptr %%dir, null
  br i1 %%dirisnull, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
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
  br i1 %%withTypes, label %%mkdirent, label %%storename

storename:
  store ptr %%namecopy, ptr %%slot, align 8
  br label %%advance

mkdirent:
  %%dtypep = getelementptr i8, ptr %%ent, i64 %d
  %%dtype = load i8, ptr %%dtypep, align 1
  %%isfifot = icmp eq i8 %%dtype, 1
  %%ischrt = icmp eq i8 %%dtype, 2
  %%isdirt = icmp eq i8 %%dtype, 4
  %%isblkt = icmp eq i8 %%dtype, 6
  %%isregt = icmp eq i8 %%dtype, 8
  %%islnkt = icmp eq i8 %%dtype, 10
  %%issockt = icmp eq i8 %%dtype, 12
  %%m1 = select i1 %%isfifot, i64 4096, i64 0
  %%m2 = select i1 %%ischrt, i64 8192, i64 %%m1
  %%m3 = select i1 %%isdirt, i64 16384, i64 %%m2
  %%m4 = select i1 %%isblkt, i64 24576, i64 %%m3
  %%m5 = select i1 %%isregt, i64 32768, i64 %%m4
  %%m6 = select i1 %%islnkt, i64 40960, i64 %%m5
  %%mode = select i1 %%issockt, i64 49152, i64 %%m6
  %%dirent = call ptr @malloc(i64 24)
  %%dname_p = getelementptr { ptr, ptr, i64 }, ptr %%dirent, i32 0, i32 0
  store ptr %%namecopy, ptr %%dname_p, align 8
  %%dpp_p = getelementptr { ptr, ptr, i64 }, ptr %%dirent, i32 0, i32 1
  store ptr %%path, ptr %%dpp_p, align 8
  %%dmode_p = getelementptr { ptr, ptr, i64 }, ptr %%dirent, i32 0, i32 2
  store i64 %%mode, ptr %%dmode_p, align 8
  store ptr %%dirent, ptr %%slot, align 8
  br label %%advance

advance:
  %%newlen = add i64 %%curlen, 1
  store i64 %%newlen, ptr %%len_p, align 8
  br label %%readloop

done:
  call i32 @closedir(ptr %%dir)
  %%finaldata = load ptr, ptr %%data_p, align 8
  %%finallen = load i64, ptr %%len_p, align 8
  %%cmp = select i1 %%withTypes, ptr @__kml_fs_cmp_dirent, ptr @__kml_fs_cmp_name
  call void @qsort(ptr %%finaldata, i64 %%finallen, i64 8, ptr %%cmp)
  %%r0 = insertvalue {ptr, i64} undef, ptr %%finaldata, 0
  %%r1 = insertvalue {ptr, i64} %%r0, i64 %%finallen, 1
  ret {ptr, i64} %%r1
}`, opDescPtr, e.internString("scandir"), e.direntNameOffset(), dotPtr, dotdotPtr, e.direntTypeOffset()))
}

// ensureFsReaddirRecursive declares __kml_fs_readdir_recursive, backing
// fs.readdirSync(path, { recursive: true }): every entry in the tree, as a
// string[] of paths relative to the starting directory ("sub", "sub/f.txt",
// …), joined with "/". It is a genuine recursive descent — __kml_fs_readdir_rec
// calls itself per subdirectory, threading the shared {data,len,cap}
// accumulator (%bufslot). A subdirectory is recognised by d_type == DT_DIR (4),
// the fast path every normal filesystem here populates (APFS/ext4/the Windows
// shim); a filesystem that reports DT_UNKNOWN is not descended (a documented
// caveat, no stat fallback). Only the top-level open throws (via the wrapper),
// matching Node; an unreadable subdirectory is skipped. Returns {ptr, i64}, the
// same aggregate the non-recursive form uses, so the string[] path in the
// emitter is unchanged.
func (e *Emitter) ensureFsReaddirRecursive() {
	if e.usedFsReaddirRecursive {
		return
	}
	e.usedFsReaddirRecursive = true
	e.ensureFsReaddir() // shares opendir/readdir/closedir/strdup decls + the throw
	e.ensureFree()
	e.ensureStrlen()
	e.ensureSprintf()
	opDescPtr := e.internString("cannot open directory")
	joinFmt := e.internString("%s/%s")
	emptyPtr := e.internString("")
	dotPtr := e.internString(".")
	dotdotPtr := e.internString("..")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_readdir_rec(ptr %%full, ptr %%rel, ptr %%bufslot) {
entry:
  %%dir = call ptr @opendir(ptr %%full)
  %%dnull = icmp eq ptr %%dir, null
  br i1 %%dnull, label %%ret, label %%rl

rl:
  %%ent = call ptr @readdir(ptr %%dir)
  %%enull = icmp eq ptr %%ent, null
  br i1 %%enull, label %%close, label %%got

got:
  %%nameptr = getelementptr i8, ptr %%ent, i64 %d
  %%isdot = call i32 @strcmp(ptr %%nameptr, ptr %s)
  %%isdd = call i32 @strcmp(ptr %%nameptr, ptr %s)
  %%d0 = icmp eq i32 %%isdot, 0
  %%d1 = icmp eq i32 %%isdd, 0
  %%skip = or i1 %%d0, %%d1
  br i1 %%skip, label %%rl, label %%build

build:
  %%rellen = call i64 @strlen(ptr %%rel)
  %%relempty = icmp eq i64 %%rellen, 0
  br i1 %%relempty, label %%relroot, label %%reljoin

relroot:
  %%cr0 = call ptr @strdup(ptr %%nameptr)
  br label %%haverel

reljoin:
  %%namelen = call i64 @strlen(ptr %%nameptr)
  %%rsz1 = add i64 %%rellen, %%namelen
  %%rsz2 = add i64 %%rsz1, 2
  %%cr1 = call ptr @malloc(i64 %%rsz2)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%cr1, ptr %s, ptr %%rel, ptr %%nameptr)
  br label %%haverel

haverel:
  %%childRel = phi ptr [ %%cr0, %%relroot ], [ %%cr1, %%reljoin ]
  %%km = call ptr @__kml_str_from_cstr(ptr %%childRel)
  %%data_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 0
  %%len_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 1
  %%cap_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 2
  %%curlen = load i64, ptr %%len_p, align 8
  %%curcap = load i64, ptr %%cap_p, align 8
  %%np1 = add i64 %%curlen, 1
  %%needgrow = icmp sgt i64 %%np1, %%curcap
  br i1 %%needgrow, label %%grow, label %%store

grow:
  %%curdata = load ptr, ptr %%data_p, align 8
  %%cap2 = mul i64 %%curcap, 2
  %%atleast8 = icmp sgt i64 %%cap2, 8
  %%newcap = select i1 %%atleast8, i64 %%cap2, i64 8
  %%newcapbytes = mul i64 %%newcap, 8
  %%newdata = call ptr @realloc(ptr %%curdata, i64 %%newcapbytes)
  store ptr %%newdata, ptr %%data_p, align 8
  store i64 %%newcap, ptr %%cap_p, align 8
  br label %%store

store:
  %%dnow = load ptr, ptr %%data_p, align 8
  %%slot = getelementptr ptr, ptr %%dnow, i64 %%curlen
  store ptr %%km, ptr %%slot, align 8
  %%newlen = add i64 %%curlen, 1
  store i64 %%newlen, ptr %%len_p, align 8
  %%dtp = getelementptr i8, ptr %%ent, i64 %d
  %%dt = load i8, ptr %%dtp, align 1
  %%isdir = icmp eq i8 %%dt, 4
  br i1 %%isdir, label %%recurse, label %%freerel

recurse:
  %%fulllen = call i64 @strlen(ptr %%full)
  %%namelen2 = call i64 @strlen(ptr %%nameptr)
  %%fsz1 = add i64 %%fulllen, %%namelen2
  %%fsz2 = add i64 %%fsz1, 2
  %%cf = call ptr @malloc(i64 %%fsz2)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%cf, ptr %s, ptr %%full, ptr %%nameptr)
  call void @__kml_fs_readdir_rec(ptr %%cf, ptr %%childRel, ptr %%bufslot)
  call void @free(ptr %%cf)
  br label %%freerel

freerel:
  call void @free(ptr %%childRel)
  br label %%rl

close:
  call i32 @closedir(ptr %%dir)
  br label %%ret

ret:
  ret void
}

define {ptr, i64} @__kml_fs_readdir_recursive(ptr %%path) {
entry:
  %%probe = call ptr @opendir(ptr %%path)
  %%pnull = icmp eq ptr %%probe, null
  br i1 %%pnull, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable

ok:
  call i32 @closedir(ptr %%probe)
  %%bufslot = call ptr @malloc(i64 24)
  %%data_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 0
  %%len_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 1
  %%cap_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 2
  store ptr null, ptr %%data_p, align 8
  store i64 0, ptr %%len_p, align 8
  store i64 0, ptr %%cap_p, align 8
  call void @__kml_fs_readdir_rec(ptr %%path, ptr %s, ptr %%bufslot)
  %%finaldata = load ptr, ptr %%data_p, align 8
  %%finallen = load i64, ptr %%len_p, align 8
  %%r0 = insertvalue {ptr, i64} undef, ptr %%finaldata, 0
  %%r1 = insertvalue {ptr, i64} %%r0, i64 %%finallen, 1
  ret {ptr, i64} %%r1
}`, e.direntNameOffset(), dotPtr, dotdotPtr, joinFmt, e.direntTypeOffset(), joinFmt,
		opDescPtr, e.internString("scandir"), emptyPtr))
}

// ensureFsReaddirRecursiveTypes declares __kml_fs_readdir_recursive_types,
// backing fs.readdirSync(path, { recursive: true, withFileTypes: true })
// (ADR-00980): every entry in the tree as a Dirent[], each Dirent carrying its
// basename `name`, the full path of its containing directory as `parentPath`,
// and the hidden S_IFMT `mode` word (from d_type) the kind predicates read —
// exactly the shape the non-recursive withFileTypes form produces, just walked
// recursively. Structurally a merge of __kml_fs_readdir_rec (the recursive
// descent) and the `mkdirent` block of __kml_fs_readdir (the Dirent builder):
// each entry is materialised as a `{ptr name, ptr parentPath, i64 mode}` heap
// struct, and a subdirectory (d_type == DT_DIR) is descended with its full
// path. parentPath is a header-copied string of %full (its own copy, since the
// caller frees the malloc'd descent path after the recursive call returns).
func (e *Emitter) ensureFsReaddirRecursiveTypes() {
	if e.usedFsReaddirRecursiveTypes {
		return
	}
	e.usedFsReaddirRecursiveTypes = true
	e.ensureFsReaddirRecursive() // shares every decl (opendir/readdir/… + throw + str_from_cstr)
	opDescPtr := e.internString("cannot open directory")
	joinFmt := e.internString("%s/%s")
	dotPtr := e.internString(".")
	dotdotPtr := e.internString("..")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_readdir_rec_t(ptr %%full, ptr %%bufslot) {
entry:
  %%dir = call ptr @opendir(ptr %%full)
  %%dnull = icmp eq ptr %%dir, null
  br i1 %%dnull, label %%ret, label %%rl

rl:
  %%ent = call ptr @readdir(ptr %%dir)
  %%enull = icmp eq ptr %%ent, null
  br i1 %%enull, label %%close, label %%got

got:
  %%nameptr = getelementptr i8, ptr %%ent, i64 %d
  %%isdot = call i32 @strcmp(ptr %%nameptr, ptr %s)
  %%isdd = call i32 @strcmp(ptr %%nameptr, ptr %s)
  %%d0 = icmp eq i32 %%isdot, 0
  %%d1 = icmp eq i32 %%isdd, 0
  %%skip = or i1 %%d0, %%d1
  br i1 %%skip, label %%rl, label %%build

build:
  %%dtp = getelementptr i8, ptr %%ent, i64 %d
  %%dt = load i8, ptr %%dtp, align 1
  %%isfifot = icmp eq i8 %%dt, 1
  %%ischrt = icmp eq i8 %%dt, 2
  %%isdirt = icmp eq i8 %%dt, 4
  %%isblkt = icmp eq i8 %%dt, 6
  %%isregt = icmp eq i8 %%dt, 8
  %%islnkt = icmp eq i8 %%dt, 10
  %%issockt = icmp eq i8 %%dt, 12
  %%m1 = select i1 %%isfifot, i64 4096, i64 0
  %%m2 = select i1 %%ischrt, i64 8192, i64 %%m1
  %%m3 = select i1 %%isdirt, i64 16384, i64 %%m2
  %%m4 = select i1 %%isblkt, i64 24576, i64 %%m3
  %%m5 = select i1 %%isregt, i64 32768, i64 %%m4
  %%m6 = select i1 %%islnkt, i64 40960, i64 %%m5
  %%mode = select i1 %%issockt, i64 49152, i64 %%m6
  %%namecopy = call ptr @__kml_str_from_cstr(ptr %%nameptr)
  %%parentcopy = call ptr @__kml_str_from_cstr(ptr %%full)
  %%dirent = call ptr @malloc(i64 24)
  %%dname_p = getelementptr { ptr, ptr, i64 }, ptr %%dirent, i32 0, i32 0
  store ptr %%namecopy, ptr %%dname_p, align 8
  %%dpp_p = getelementptr { ptr, ptr, i64 }, ptr %%dirent, i32 0, i32 1
  store ptr %%parentcopy, ptr %%dpp_p, align 8
  %%dmode_p = getelementptr { ptr, ptr, i64 }, ptr %%dirent, i32 0, i32 2
  store i64 %%mode, ptr %%dmode_p, align 8
  %%data_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 0
  %%len_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 1
  %%cap_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 2
  %%curlen = load i64, ptr %%len_p, align 8
  %%curcap = load i64, ptr %%cap_p, align 8
  %%np1 = add i64 %%curlen, 1
  %%needgrow = icmp sgt i64 %%np1, %%curcap
  br i1 %%needgrow, label %%grow, label %%store

grow:
  %%curdata = load ptr, ptr %%data_p, align 8
  %%cap2 = mul i64 %%curcap, 2
  %%atleast8 = icmp sgt i64 %%cap2, 8
  %%newcap = select i1 %%atleast8, i64 %%cap2, i64 8
  %%newcapbytes = mul i64 %%newcap, 8
  %%newdata = call ptr @realloc(ptr %%curdata, i64 %%newcapbytes)
  store ptr %%newdata, ptr %%data_p, align 8
  store i64 %%newcap, ptr %%cap_p, align 8
  br label %%store

store:
  %%dnow = load ptr, ptr %%data_p, align 8
  %%slot = getelementptr ptr, ptr %%dnow, i64 %%curlen
  store ptr %%dirent, ptr %%slot, align 8
  %%newlen = add i64 %%curlen, 1
  store i64 %%newlen, ptr %%len_p, align 8
  br i1 %%isdirt, label %%recurse, label %%rl

recurse:
  %%fulllen = call i64 @strlen(ptr %%full)
  %%namelen2 = call i64 @strlen(ptr %%nameptr)
  %%fsz1 = add i64 %%fulllen, %%namelen2
  %%fsz2 = add i64 %%fsz1, 2
  %%cf = call ptr @malloc(i64 %%fsz2)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%cf, ptr %s, ptr %%full, ptr %%nameptr)
  call void @__kml_fs_readdir_rec_t(ptr %%cf, ptr %%bufslot)
  call void @free(ptr %%cf)
  br label %%rl

close:
  call i32 @closedir(ptr %%dir)
  br label %%ret

ret:
  ret void
}

define {ptr, i64} @__kml_fs_readdir_recursive_types(ptr %%path) {
entry:
  %%probe = call ptr @opendir(ptr %%path)
  %%pnull = icmp eq ptr %%probe, null
  br i1 %%pnull, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable

ok:
  call i32 @closedir(ptr %%probe)
  %%bufslot = call ptr @malloc(i64 24)
  %%data_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 0
  %%len_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 1
  %%cap_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 2
  store ptr null, ptr %%data_p, align 8
  store i64 0, ptr %%len_p, align 8
  store i64 0, ptr %%cap_p, align 8
  call void @__kml_fs_readdir_rec_t(ptr %%path, ptr %%bufslot)
  %%finaldata = load ptr, ptr %%data_p, align 8
  %%finallen = load i64, ptr %%len_p, align 8
  %%r0 = insertvalue {ptr, i64} undef, ptr %%finaldata, 0
  %%r1 = insertvalue {ptr, i64} %%r0, i64 %%finallen, 1
  ret {ptr, i64} %%r1
}`, e.direntNameOffset(), dotPtr, dotdotPtr, e.direntTypeOffset(), joinFmt,
		opDescPtr, e.internString("scandir")))
}

// statLayout returns the host libc's struct stat field offsets and load widths
// for the full Stats surface (ADR-00565). struct stat has no portable layout,
// so these are per-OS/arch constants — the same approach direntNameOffset
// takes. The three supported hosts:
//
//	Darwin (64-bit-inode default): st_dev i32 @0, st_mode u16 @4, st_nlink u16
//	  @6, st_ino u64 @8, st_uid u32 @16, st_gid u32 @20, st_rdev i32 @24,
//	  atimespec @32, mtimespec @48, ctimespec @64, birthtimespec @80, st_size
//	  i64 @96, st_blocks i64 @104, st_blksize i32 @112.
//	glibc x86-64: st_dev u64 @0, st_ino u64 @8, st_nlink u64 @16, st_mode u32
//	  @24, st_uid u32 @28, st_gid u32 @32, st_rdev u64 @40, st_size i64 @48,
//	  st_blksize i64 @56, st_blocks i64 @64, st_atim @72, st_mtim @88, st_ctim
//	  @104. No birthtime in struct stat (needs statx) → reported 0.
//	glibc aarch64: st_dev u64 @0, st_ino u64 @8, st_mode u32 @16, st_nlink u32
//	  @20, st_uid u32 @24, st_gid u32 @28, st_rdev u64 @32, st_size i64 @48,
//	  st_blksize i32 @56, st_blocks i64 @64, st_atim @72, st_mtim @88, st_ctim
//	  @104. No birthtime → 0.
//
// A time field is (secOff, nsecOff); a birthtime secOff of -1 means "not in
// this struct, report 0". A scalar field is (off, bits).
type statFieldLayout struct {
	devOff, devBits         int
	modeOff, modeBits       int
	nlinkOff, nlinkBits     int
	inoOff, inoBits         int
	uidOff, gidOff          int // both u32
	rdevOff, rdevBits       int
	sizeOff                 int // i64
	blocksOff               int // i64
	blksizeOff, blksizeBits int
	atimeSec, atimeNsec     int
	mtimeSec, mtimeNsec     int
	ctimeSec, ctimeNsec     int
	birthSec, birthNsec     int // birthSec < 0 → report 0
}

func (e *Emitter) statLayout() statFieldLayout {
	if e.opts.Target.OS() == "windows" {
		// win32fs.c writes the glibc x86-64 layout and puts birthtime (real on
		// Windows, as in Node) in the struct's reserved tail.
		return statFieldLayout{
			devOff: 0, devBits: 64,
			inoOff: 8, inoBits: 64,
			nlinkOff: 16, nlinkBits: 64,
			modeOff: 24, modeBits: 32,
			uidOff: 28, gidOff: 32,
			rdevOff: 40, rdevBits: 64,
			sizeOff: 48, blocksOff: 64,
			blksizeOff: 56, blksizeBits: 64,
			atimeSec: 72, atimeNsec: 80,
			mtimeSec: 88, mtimeNsec: 96,
			ctimeSec: 104, ctimeNsec: 112,
			birthSec: 120, birthNsec: 128,
		}
	}
	if e.opts.Target.OS() == "darwin" {
		return statFieldLayout{
			devOff: 0, devBits: 32,
			modeOff: 4, modeBits: 16,
			nlinkOff: 6, nlinkBits: 16,
			inoOff: 8, inoBits: 64,
			uidOff: 16, gidOff: 20,
			rdevOff: 24, rdevBits: 32,
			sizeOff: 96, blocksOff: 104,
			blksizeOff: 112, blksizeBits: 32,
			atimeSec: 32, atimeNsec: 40,
			mtimeSec: 48, mtimeNsec: 56,
			ctimeSec: 64, ctimeNsec: 72,
			birthSec: 80, birthNsec: 88,
		}
	}
	if e.opts.Target.Arch() == "arm64" {
		return statFieldLayout{
			devOff: 0, devBits: 64,
			inoOff: 8, inoBits: 64,
			modeOff: 16, modeBits: 32,
			nlinkOff: 20, nlinkBits: 32,
			uidOff: 24, gidOff: 28,
			rdevOff: 32, rdevBits: 64,
			sizeOff: 48, blocksOff: 64,
			blksizeOff: 56, blksizeBits: 32,
			atimeSec: 72, atimeNsec: 80,
			mtimeSec: 88, mtimeNsec: 96,
			ctimeSec: 104, ctimeNsec: 112,
			birthSec: -1, birthNsec: -1,
		}
	}
	return statFieldLayout{
		devOff: 0, devBits: 64,
		inoOff: 8, inoBits: 64,
		nlinkOff: 16, nlinkBits: 64,
		modeOff: 24, modeBits: 32,
		uidOff: 28, gidOff: 32,
		rdevOff: 40, rdevBits: 64,
		sizeOff: 48, blocksOff: 64,
		blksizeOff: 56, blksizeBits: 64,
		atimeSec: 72, atimeNsec: 80,
		mtimeSec: 88, mtimeNsec: 96,
		ctimeSec: 104, ctimeNsec: 112,
		birthSec: -1, birthNsec: -1,
	}
}

// statResultIR is the LLVM return type of __kml_fs_stat/__kml_fs_lstat: 14
// i64s in the order buildStatsObject/StatsType expect — dev, mode, nlink, uid,
// gid, rdev, blksize, ino, size, blocks, atimeMs, mtimeMs, ctimeMs,
// birthtimeMs (mirroring Node's Stats own-property order).
const statResultIR = "{ i64, i64, i64, i64, i64, i64, i64, i64, i64, i64, i64, i64, i64, i64 }"

// statBodyLL renders the shared ok-path of __kml_fs_stat/__kml_fs_lstat: read
// every field at its host offset, convert timespecs to integer milliseconds,
// and pack the 14-i64 result.
func statBodyLL(L statFieldLayout) string {
	var b strings.Builder
	scalar := func(name string, off, bits int) {
		fmt.Fprintf(&b, "  %%%sp = getelementptr i8, ptr %%buf, i64 %d\n", name, off)
		if bits == 64 {
			fmt.Fprintf(&b, "  %%%s = load i64, ptr %%%sp, align 1\n", name, name)
		} else {
			fmt.Fprintf(&b, "  %%%sw = load i%d, ptr %%%sp, align 1\n", name, bits, name)
			fmt.Fprintf(&b, "  %%%s = zext i%d %%%sw to i64\n", name, bits, name)
		}
	}
	timeMs := func(name string, secOff, nsecOff int) {
		if secOff < 0 {
			fmt.Fprintf(&b, "  %%%s = add i64 0, 0\n", name)
			return
		}
		fmt.Fprintf(&b, "  %%%s_sp = getelementptr i8, ptr %%buf, i64 %d\n", name, secOff)
		fmt.Fprintf(&b, "  %%%s_sec = load i64, ptr %%%s_sp, align 1\n", name, name)
		fmt.Fprintf(&b, "  %%%s_np = getelementptr i8, ptr %%buf, i64 %d\n", name, nsecOff)
		fmt.Fprintf(&b, "  %%%s_ns = load i64, ptr %%%s_np, align 1\n", name, name)
		fmt.Fprintf(&b, "  %%%s_a = mul i64 %%%s_sec, 1000\n", name, name)
		fmt.Fprintf(&b, "  %%%s_b = sdiv i64 %%%s_ns, 1000000\n", name, name)
		fmt.Fprintf(&b, "  %%%s = add i64 %%%s_a, %%%s_b\n", name, name, name)
	}
	scalar("dev", L.devOff, L.devBits)
	scalar("mode", L.modeOff, L.modeBits)
	scalar("nlink", L.nlinkOff, L.nlinkBits)
	scalar("uid", L.uidOff, 32)
	scalar("gid", L.gidOff, 32)
	scalar("rdev", L.rdevOff, L.rdevBits)
	scalar("blksize", L.blksizeOff, L.blksizeBits)
	scalar("ino", L.inoOff, L.inoBits)
	scalar("size", L.sizeOff, 64)
	scalar("blocks", L.blocksOff, 64)
	timeMs("atimeMs", L.atimeSec, L.atimeNsec)
	timeMs("mtimeMs", L.mtimeSec, L.mtimeNsec)
	timeMs("ctimeMs", L.ctimeSec, L.ctimeNsec)
	timeMs("birthMs", L.birthSec, L.birthNsec)
	order := []string{"dev", "mode", "nlink", "uid", "gid", "rdev", "blksize", "ino", "size", "blocks", "atimeMs", "mtimeMs", "ctimeMs", "birthMs"}
	prev := "undef"
	for i, name := range order {
		reg := fmt.Sprintf("%%pack%d", i)
		fmt.Fprintf(&b, "  %s = insertvalue %s %s, i64 %%%s, %d\n", reg, statResultIR, prev, name, i)
		prev = reg
	}
	fmt.Fprintf(&b, "  ret %s %s\n", statResultIR, prev)
	return b.String()
}

// ensureFsStat declares __kml_fs_stat (ADR-00495/ADR-00565): stat(2) into a
// 256-byte scratch buffer, extracting the full Stats surface at statLayout()'s
// host offsets. Throws the shared fs error on failure (ENOENT and friends).
func (e *Emitter) ensureFsStat() {
	if e.usedFsStat {
		return
	}
	e.usedFsStat = true
	e.ensureFsThrow()
	e.ensureMalloc()
	e.ensureStatDecl()
	opDescPtr := e.internString("cannot stat path")
	e.emitGlobal(fmt.Sprintf(`
define %s @__kml_fs_stat(ptr %%path) {
entry:
  %%buf = alloca [256 x i8], align 8
  %%r = call i32 @stat(ptr %%path, ptr %%buf)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable

ok:
%s}`, statResultIR, opDescPtr, e.internString("stat"), statBodyLL(e.statLayout())))
}

// ensureFsLstat declares __kml_fs_lstat — statSync's twin over lstat(2)
// (does not follow symlinks), sharing statLayout()'s offsets (ADR-00497).
func (e *Emitter) ensureFsLstat() {
	if e.usedFsLstat {
		return
	}
	e.usedFsLstat = true
	e.ensureFsThrow()
	e.emitFSDecl("lstat", "i32", []string{"ptr", "ptr"})
	opDescPtr := e.internString("cannot lstat path")
	e.emitGlobal(fmt.Sprintf(`
define %s @__kml_fs_lstat(ptr %%path) {
entry:
  %%buf = alloca [256 x i8], align 8
  %%r = call i32 @lstat(ptr %%path, ptr %%buf)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable

ok:
%s}`, statResultIR, opDescPtr, e.internString("lstat"), statBodyLL(e.statLayout())))
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
	e.ensureFree() // readlink's grow loop frees each undersized attempt
	e.ensureStrlen()
	e.ensureMemcpy()
	e.ensureFsExists() // owns the `access` decl
	e.emitGlobal("declare ptr @realpath(ptr noundef, ptr noundef)")
	e.emitGlobal("declare ptr @mkdtemp(ptr noundef)")
	e.emitGlobal("declare i32 @symlink(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @link(ptr noundef, ptr noundef)")
	e.ensureReadlinkDecl()
	e.ensureChmodDecl()
	e.emitGlobal("declare i32 @truncate(ptr noundef, i64 noundef)")
	realpathDesc := e.internString("cannot resolve path")
	mkdtempDesc := e.internString("cannot create temp directory")
	symlinkDesc := e.internString("cannot create symlink")
	linkDesc := e.internString("cannot create link")
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
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
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
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%prefix)
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
  call void @__kml_fs_throw2(ptr %s, ptr %s, ptr %%target, ptr %%path)
  unreachable
ok:
  ret void
}

define void @__kml_fs_link(ptr %%existing, ptr %%path) {
entry:
  %%r = call i32 @link(ptr %%existing, ptr %%path)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw2(ptr %s, ptr %s, ptr %%existing, ptr %%path)
  unreachable
ok:
  ret void
}

define ptr @__kml_fs_readlink(ptr %%path) {
entry:
  br label %%try
try:
  %%cap = phi i64 [ 256, %%entry ], [ %%cap2, %%grow ]
  %%prev = phi ptr [ null, %%entry ], [ %%buf, %%grow ]
  call void @free(ptr %%prev)
  %%allocsz = add i64 %%cap, 1
  %%buf = call ptr @malloc(i64 %%allocsz)
  %%n = call i64 @readlink(ptr %%path, ptr %%buf, i64 %%cap)
  %%failed = icmp slt i64 %%n, 0
  br i1 %%failed, label %%fail, label %%chkfit
chkfit:
  ; readlink returns min(len, cap); n == cap means the target may be longer,
  ; so grow and retry until it fits strictly inside the buffer (no truncation).
  %%fit = icmp slt i64 %%n, %%cap
  br i1 %%fit, label %%ok, label %%grow
grow:
  %%cap2 = mul i64 %%cap, 2
  br label %%try
fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
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
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
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
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
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
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable
ok:
  ret void
}`, realpathDesc, e.internString("lstat"), mkdtempDesc, e.internString("mkdtemp"),
		symlinkDesc, e.internString("symlink"), linkDesc, e.internString("link"),
		readlinkDesc, e.internString("readlink"), chmodDesc, e.internString("chmod"),
		truncateDesc, e.internString("open"), accessDesc, e.internString("access")))
}

// ensureFsCopyFileOS declares __kml_fs_copy_file_os(src, dest): the Windows
// transfer half of fs.copyFileSync, the shim's CopyFileW, throwing Node's
// two-path `copyfile` error on failure.
func (e *Emitter) ensureFsCopyFileOS() {
	if e.usedFsCopyFileOS {
		return
	}
	e.usedFsCopyFileOS = true
	e.ensureFsThrow()
	e.emitGlobal("declare i32 @__kml_win_copyfile(ptr, ptr)")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_copy_file_os(ptr %%src, ptr %%dest) {
entry:
  %%r = call i32 @__kml_win_copyfile(ptr %%src, ptr %%dest)
  %%bad = icmp ne i32 %%r, 0
  br i1 %%bad, label %%fail, label %%ok
ok:
  ret void
fail:
  call void @__kml_fs_throw2(ptr %s, ptr %s, ptr %%src, ptr %%dest)
  unreachable
}`, e.internString("cannot copy file"), e.internString("copyfile")))
}

// ensureFsCopyFileGuard declares __kml_fs_copy_file_guard(src, dest, excl): the
// pre-flight validation for fs.copyFileSync, run before the binary-safe
// read+write composition fills the copy. It probes both ends so any failure
// surfaces as Node's exact two-path `copyfile` error (`ENOENT: …, copyfile
// '<src>' -> '<dest>'`, `err.syscall === 'copyfile'`, `err.path`/`err.dest`
// set — ADR-01000) rather than the underlying `open` on one path that the raw
// read/write helpers would otherwise report:
//   - opens src O_RDONLY: catches a missing/unreadable source (ENOENT/EACCES/…).
//   - opens dest O_WRONLY|O_CREAT|O_TRUNC (+O_EXCL when `excl`, the COPYFILE_EXCL
//     mode bit — ADR-00788): catches a missing dest parent, an unwritable dest,
//     a directory dest, and the EEXIST of an existing dest under COPYFILE_EXCL.
//
// Both descriptors are closed immediately; the subsequent
// __kml_fs_read_file_raw/__kml_fs_write_file_bytes then do the real transfer
// (and, under excl, fill the empty file this guard atomically created). Uses
// the shared fd-op decls (open/close).
func (e *Emitter) ensureFsCopyFileGuard() {
	if e.usedFsCopyExclGuard {
		return
	}
	e.usedFsCopyExclGuard = true
	e.ensureFsThrow()
	e.ensureFsFdOps() // shares the open/close declarations
	rd, _ := e.openFlagBits("r")
	w, _ := e.openFlagBits("w")
	wx, _ := e.openFlagBits("wx")
	desc := e.internString("cannot copy file")
	sc := e.internString("copyfile")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_copy_file_guard(ptr %%src, ptr %%dest, i1 %%excl) {
entry:
  %%sfd = call i32 (ptr, i32, ...) @open(ptr %%src, i32 %d, i32 0)
  %%sbad = icmp slt i32 %%sfd, 0
  br i1 %%sbad, label %%fail, label %%srcok
srcok:
  %%sc0 = call i32 @close(i32 %%sfd)
  %%dflags = select i1 %%excl, i32 %d, i32 %d
  %%dfd = call i32 (ptr, i32, ...) @open(ptr %%dest, i32 %%dflags, i32 420)
  %%dbad = icmp slt i32 %%dfd, 0
  br i1 %%dbad, label %%fail, label %%destok
destok:
  %%dc0 = call i32 @close(i32 %%dfd)
  ret void
fail:
  call void @__kml_fs_throw2(ptr %s, ptr %s, ptr %%src, ptr %%dest)
  unreachable
}`, rd, wx, w, desc, sc))
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
	// rm's first attempt stays ANSI C remove() (file or empty directory in
	// one call); unlinkSync no longer declares it (ADR-00736).
	e.emitGlobal("declare i32 @remove(ptr noundef)")
	e.ensureFsReaddir()
	e.ensureFsExists()
	e.ensureMalloc()
	e.ensureStrlen()
	e.ensureMemcpy()
	rmDesc := e.internString("cannot remove path")
	nameOff := e.direntNameOffset()
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
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable
done:
  ret void
}`, nameOff, rmDesc, e.internString("unlink")))
}

// openFlagBits maps a Node open-flags string to the host's O_* bit mask —
// per-OS constants (Darwin and glibc disagree on everything past
// O_RDONLY/O_WRONLY/O_RDWR), resolved at compile time from the literal
// (ADR-00498).
func (e *Emitter) openFlagBits(flags string) (int, bool) {
	creat, trunc, appnd, excl := 0x200, 0x400, 0x8, 0x800
	if e.opts.Target.OS() != "darwin" {
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
// ensureFsUtimes declares __kml_fs_utimes(path, timeval[2]*): utimes(2) on
// POSIX, a SetFileTime shim on Windows (win32fs.c), throwing the shared fs
// error on failure. The `times` buffer is two {i64 sec, i64 usec} pairs
// (ADR-00755).
func (e *Emitter) ensureFsUtimes() {
	if e.usedFsUtimes {
		return
	}
	e.usedFsUtimes = true
	e.ensureFsThrow()
	e.emitGlobal("declare i32 @utimes(ptr noundef, ptr noundef)")
	utimesDesc := e.internString("cannot set file times")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_utimes(ptr %%path, ptr %%times) {
entry:
  %%r = call i32 @utimes(ptr %%path, ptr %%times)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable
ok:
  ret void
}`, utimesDesc, e.internString("utime")))
}

// ensureFsFutimes declares __kml_fs_futimes(fd, timeval[2]*): the fd-based twin
// of __kml_fs_utimes (ADR-00788) — futimes(2) on POSIX, a win32 SetFileTime
// shim keyed on _get_osfhandle(fd) (win32fs.c) on Windows. Same two
// {i64 sec, i64 usec} `times` buffer as utimesSync.
func (e *Emitter) ensureFsFutimes() {
	if e.usedFsFutimes {
		return
	}
	e.usedFsFutimes = true
	e.ensureFsThrow()
	e.emitGlobal("declare i32 @futimes(i32 noundef, ptr noundef)")
	desc := e.internString("cannot set file times")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_futimes(i64 %%fd, ptr %%times) {
entry:
  %%f32 = trunc i64 %%fd to i32
  %%r = call i32 @futimes(i32 %%f32, ptr %%times)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %s)
  unreachable
ok:
  ret void
}`, desc, e.internString("futime"), e.internString("")))
}

// ensureOpenDecl declares C `open` exactly once (shared by the fd ops and the
// macOS fs.watch backend, which would otherwise redefine it).
func (e *Emitter) ensureOpenDecl() {
	if e.usedOpenDecl {
		return
	}
	e.usedOpenDecl = true
	e.emitGlobal("declare i32 @open(ptr noundef, i32 noundef, ...)")
}

func (e *Emitter) ensureFsFdOps() {
	if e.usedFsFdOps {
		return
	}
	e.usedFsFdOps = true
	e.ensureFsThrow()
	e.ensureOpenDecl()
	// close/read/write are also declared by other subsystems (stdin, http,
	// process, worker pool). Route through the shared ensure*Decl guards so a
	// program using both fs fd-ops and one of those paths emits each declare
	// exactly once — a duplicate `declare` is lenient on macOS/Linux clang but
	// a hard "invalid redefinition" error on the Windows UCRT64 clang.
	e.ensureCloseDecl()
	e.ensureReadDecl()
	e.ensureWriteDecl()
	e.emitGlobal("declare i64 @lseek(i32 noundef, i64 noundef, i32 noundef)")
	e.emitGlobal("declare i32 @fsync(i32 noundef)")
	e.emitGlobal("declare i32 @ftruncate(i32 noundef, i64 noundef)")
	e.emitFSDecl("fstat", "i32", []string{"i32", "ptr"})
	openDesc := e.internString("cannot open path")
	fdDesc := e.internString("fd operation failed")
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_fs_open(ptr %%path, i64 %%flags, i64 %%mode) {
entry:
  %%f32 = trunc i64 %%flags to i32
  %%m32 = trunc i64 %%mode to i32
  %%fd = call i32 (ptr, i32, ...) @open(ptr %%path, i32 %%f32, i32 %%m32)
  %%failed = icmp slt i32 %%fd, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
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
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr null)
  unreachable
ok:
  ret i64 %%n
}

define %s @__kml_fs_fstat(i64 %%fd) {
entry:
  %%f32 = trunc i64 %%fd to i32
  %%buf = alloca [256 x i8], align 8
  %%r = call i32 @fstat(i32 %%f32, ptr %%buf)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr null)
  unreachable
ok:
%s}`, openDesc, e.internString("open"), fdDesc, e.internString("read"),
		statResultIR, fdDesc, e.internString("fstat"), statBodyLL(e.statLayout())))
	// fd-based ops carry no path in Node's message (and `err.path` is undefined),
	// so pass a null path — __kml_fs_errmsg emits `<CODE>: <desc>, <syscall>` with
	// no path clause, and the null flows through to `err.path` (ADR-01000). The
	// message builder tolerates a null path, so the former `ptr null` latent
	// crash on the fdrw path (above) is a crash no longer.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_fsync(i64 %%fd) {
entry:
  %%f32 = trunc i64 %%fd to i32
  %%r = call i32 @fsync(i32 %%f32)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr null)
  unreachable
ok:
  ret void
}
define void @__kml_fs_ftruncate(i64 %%fd, i64 %%len) {
entry:
  %%f32 = trunc i64 %%fd to i32
  %%r = call i32 @ftruncate(i32 %%f32, i64 %%len)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr null)
  unreachable
ok:
  ret void
}`, fdDesc, e.internString("fsync"), fdDesc, e.internString("ftruncate")))
}

// ensureFsFchmod declares __kml_fs_fchmod(fd, mode): fchmod(2) (the Windows
// shim's FileBasicInfo read-only-bit form), throwing the fd-shaped fs error.
func (e *Emitter) ensureFsFchmod() {
	if e.usedFsFchmod {
		return
	}
	e.usedFsFchmod = true
	e.ensureFsThrow()
	e.emitGlobal("declare i32 @fchmod(i32 noundef, i32 noundef)")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_fchmod(i64 %%fd, i64 %%mode) {
entry:
  %%f32 = trunc i64 %%fd to i32
  %%m32 = trunc i64 %%mode to i32
  %%r = call i32 @fchmod(i32 %%f32, i32 %%m32)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr null)
  unreachable
ok:
  ret void
}`, e.internString("fd operation failed"), e.internString("fchmod")))
}

// ensureFsStatfs declares __kml_fs_statfs_checked(path, out): the osinfo
// sidecar's statfs (statfs(2) / GetDiskFreeSpaceW) with the path-shaped fs
// error on failure (Node: `ENOENT: no such file or directory, statfs '<p>'`).
func (e *Emitter) ensureFsStatfs() {
	if e.usedFsStatfs {
		return
	}
	e.usedFsStatfs = true
	e.ensureFsThrow()
	e.ensureOSInfo()
	e.emitGlobal("declare i32 @__kml_fs_statfs(ptr, ptr)")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_statfs_checked(ptr %%path, ptr %%out) {
entry:
  %%r = call i32 @__kml_fs_statfs(ptr %%path, ptr %%out)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok
fail:
  call void @__kml_fs_throw(ptr %s, ptr %s, ptr %%path)
  unreachable
ok:
  ret void
}`, e.internString("cannot statfs path"), e.internString("statfs")))
}

// linuxErrnoPairs is the errno→code table for Windows, where the shim sets
// Linux errno values (TDD-00177): Go's syscall.E* constants on windows are
// synthetic and would never match.
var linuxErrnoPairs = [][2]interface{}{
	{1, "EPERM"}, {2, "ENOENT"}, {5, "EIO"}, {9, "EBADF"}, {13, "EACCES"},
	{17, "EEXIST"}, {20, "ENOTDIR"}, {21, "EISDIR"}, {22, "EINVAL"}, {24, "EMFILE"},
	{23, "ENFILE"}, {28, "ENOSPC"}, {30, "EROFS"}, {16, "EBUSY"}, {39, "ENOTEMPTY"},
	{40, "ELOOP"}, {36, "ENAMETOOLONG"}, {18, "EXDEV"}, {11, "EAGAIN"}, {32, "EPIPE"},
	{27, "EFBIG"}, {19, "ENODEV"}, {29, "ESPIPE"}, {31, "EMLINK"},
	{4049, "ENOTSUP"}, {4094, "UNKNOWN"}, // the shim's L_UNKNOWN: a Win32 error libuv has no name for
	{3, "ESRCH"}, {10, "ECHILD"}, {12, "ENOMEM"}, {14, "EFAULT"}, {38, "ENOSYS"}, {115, "EINPROGRESS"},
	// Socket codes the expanded WSA→errno table can now produce (ADR-00742),
	// so err.code on a network error matches Node on Windows.
	{98, "EADDRINUSE"}, {99, "EADDRNOTAVAIL"}, {100, "ENETDOWN"},
	{101, "ENETUNREACH"}, {102, "ENETRESET"}, {103, "ECONNABORTED"},
	{104, "ECONNRESET"}, {105, "ENOBUFS"}, {106, "EISCONN"}, {107, "ENOTCONN"},
	{108, "ESHUTDOWN"}, {110, "ETIMEDOUT"}, {111, "ECONNREFUSED"},
	{112, "EHOSTDOWN"}, {113, "EHOSTUNREACH"}, {114, "EALREADY"},
	{88, "ENOTSOCK"}, {89, "EDESTADDRREQ"}, {90, "EMSGSIZE"}, {91, "EPROTOTYPE"},
	{92, "ENOPROTOOPT"}, {93, "EPROTONOSUPPORT"}, {95, "EOPNOTSUPP"},
	{97, "EAFNOSUPPORT"},
}

// errnoEISDIR is the EISDIR value the emitted IR stores before throwing on
// a directory read: the host's on Linux/macOS, and the Linux number on
// Windows (Go's syscall.EISDIR there is synthetic; the shim speaks Linux
// errno — TDD-00177).
func (e *Emitter) errnoEISDIR() int {
	if e.opts.Target.OS() == "windows" {
		return 21
	}
	return int(syscall.EISDIR)
}
