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
	"ESRCH":         "no such process",
	"ETIMEDOUT":     "connection timed out",
	"EHOSTUNREACH":  "host is unreachable",
	"ENETUNREACH":   "network is unreachable",
	"ECONNABORTED":  "software caused connection abort",
	"ENOBUFS":       "no buffer space available",
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

// ensureOpenDecl declares C `open` exactly once (shared by the fd ops and the
// macOS fs.watch backend, which would otherwise redefine it).
func (e *Emitter) ensureOpenDecl() {
	if e.usedOpenDecl {
		return
	}
	e.usedOpenDecl = true
	e.emitGlobal("declare i32 @open(ptr noundef, i32 noundef, ...)")
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
