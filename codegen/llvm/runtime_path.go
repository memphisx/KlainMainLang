// runtime_path.go — C-runtime helpers backing Node's `path` module
// (emit_path.go). POSIX-only (this compiler doesn't cross-compile — see
// PATH.md), all pure string manipulation on top of libc, no new dependency.
package llvm

// ensurePathNormalize declares the segment-normalization machinery shared by
// path.join and path.resolve: __kml_path_normalize(ptr raw, i1 is_absolute)
// splits raw on '/', drops "." segments, resolves ".." against a growable
// stack of the segments seen so far (popping the top unless it's itself
// ".." — in which case a leading ".." is kept for a relative result, or
// dropped entirely for an absolute one, matching real Node's inability to
// go above root), then rejoins the surviving stack with '/' — prefixed with
// '/' when is_absolute, defaulting to "." (relative) or "/" (absolute) when
// the stack ends up empty. The three helper functions are emitted together
// under one used-flag since nothing else calls them independently.
func (e *Emitter) ensurePathNormalize() {
	if e.usedPathNormalize {
		return
	}
	e.usedPathNormalize = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemcpy()
	e.ensureStrlen()
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`
define void @__kml_path_stack_append(ptr %stackslot, ptr %item) {
entry:
  %data_p = getelementptr { ptr, i64, i64 }, ptr %stackslot, i32 0, i32 0
  %len_p = getelementptr { ptr, i64, i64 }, ptr %stackslot, i32 0, i32 1
  %cap_p = getelementptr { ptr, i64, i64 }, ptr %stackslot, i32 0, i32 2
  %curdata = load ptr, ptr %data_p, align 8
  %curlen = load i64, ptr %len_p, align 8
  %curcap = load i64, ptr %cap_p, align 8
  %neededp1 = add i64 %curlen, 1
  %needgrow = icmp sgt i64 %neededp1, %curcap
  br i1 %needgrow, label %grow, label %storeit

grow:
  %cap2 = mul i64 %curcap, 2
  %atleast8 = icmp sgt i64 %cap2, 8
  %newcap = select i1 %atleast8, i64 %cap2, i64 8
  %newcapbytes = mul i64 %newcap, 8
  %newdata = call ptr @realloc(ptr %curdata, i64 %newcapbytes)
  store ptr %newdata, ptr %data_p, align 8
  store i64 %newcap, ptr %cap_p, align 8
  br label %storeit

storeit:
  %dataNow = load ptr, ptr %data_p, align 8
  %slot = getelementptr ptr, ptr %dataNow, i64 %curlen
  store ptr %item, ptr %slot, align 8
  %newlen = add i64 %curlen, 1
  store i64 %newlen, ptr %len_p, align 8
  ret void
}

define ptr @__kml_path_stack_join(ptr %stackslot, i1 %is_absolute) {
entry:
  %data_p = getelementptr { ptr, i64, i64 }, ptr %stackslot, i32 0, i32 0
  %len_p = getelementptr { ptr, i64, i64 }, ptr %stackslot, i32 0, i32 1
  %data = load ptr, ptr %data_p, align 8
  %slen = load i64, ptr %len_p, align 8
  %isempty = icmp eq i64 %slen, 0
  br i1 %isempty, label %emptycase, label %buildloop

emptycase:
  br i1 %is_absolute, label %rootcase, label %dotcase

rootcase:
  %rootbuf = call ptr @__kml_str_alloc(i64 1)
  store i8 47, ptr %rootbuf, align 1
  %rootnull = getelementptr i8, ptr %rootbuf, i64 1
  store i8 0, ptr %rootnull, align 1
  ret ptr %rootbuf

dotcase:
  %dotbuf = call ptr @__kml_str_alloc(i64 1)
  store i8 46, ptr %dotbuf, align 1
  %dotnull = getelementptr i8, ptr %dotbuf, i64 1
  store i8 0, ptr %dotnull, align 1
  ret ptr %dotbuf

buildloop:
  %acc_a = alloca ptr, align 8
  %idx_a = alloca i64, align 8
  store i64 0, ptr %idx_a, align 8
  store ptr null, ptr %acc_a, align 8
  br label %appendloop

appendloop:
  %idx = load i64, ptr %idx_a, align 8
  %done = icmp uge i64 %idx, %slen
  br i1 %done, label %finish, label %appendone

appendone:
  %seg_p = getelementptr ptr, ptr %data, i64 %idx
  %seg = load ptr, ptr %seg_p, align 8
  %isfirst = icmp eq i64 %idx, 0
  br i1 %isfirst, label %appendfirst, label %appendrest

appendfirst:
  br i1 %is_absolute, label %appendfirstabs, label %appendfirstrel

appendfirstabs:
  %seglen_f = call i64 @strlen(ptr %seg)
  %buffdata = add i64 %seglen_f, 1
  %buff = call ptr @__kml_str_alloc(i64 %buffdata)
  store i8 47, ptr %buff, align 1
  %destf = getelementptr i8, ptr %buff, i64 1
  %seglen_f1 = add i64 %seglen_f, 1
  call ptr @memcpy(ptr %destf, ptr %seg, i64 %seglen_f1)
  store ptr %buff, ptr %acc_a, align 8
  br label %appendnext

appendfirstrel:
  store ptr %seg, ptr %acc_a, align 8
  br label %appendnext

appendrest:
  %accr = load ptr, ptr %acc_a, align 8
  %alen = call i64 @strlen(ptr %accr)
  %slen2 = call i64 @strlen(ptr %seg)
  %sumlen = add i64 %alen, %slen2
  %bufrdata = add i64 %sumlen, 1
  %bufr = call ptr @__kml_str_alloc(i64 %bufrdata)
  call ptr @memcpy(ptr %bufr, ptr %accr, i64 %alen)
  %sepp = getelementptr i8, ptr %bufr, i64 %alen
  store i8 47, ptr %sepp, align 1
  %alenp1 = add i64 %alen, 1
  %destr = getelementptr i8, ptr %bufr, i64 %alenp1
  %slen2p1 = add i64 %slen2, 1
  call ptr @memcpy(ptr %destr, ptr %seg, i64 %slen2p1)
  store ptr %bufr, ptr %acc_a, align 8
  br label %appendnext

appendnext:
  %idxnext = add i64 %idx, 1
  store i64 %idxnext, ptr %idx_a, align 8
  br label %appendloop

finish:
  %finalacc = load ptr, ptr %acc_a, align 8
  ret ptr %finalacc
}

define void @__kml_path_normalize_onesegment(ptr %stackslot, ptr %raw, i64 %start, i64 %endpos, i1 %is_absolute) {
entry:
  %seglen = sub i64 %endpos, %start
  %isempty = icmp eq i64 %seglen, 0
  br i1 %isempty, label %done, label %checklen1

checklen1:
  %is1 = icmp eq i64 %seglen, 1
  br i1 %is1, label %checkdot1, label %checklen2

checkdot1:
  %c0p = getelementptr i8, ptr %raw, i64 %start
  %c0 = load i8, ptr %c0p, align 1
  %c0isdot = icmp eq i8 %c0, 46
  br i1 %c0isdot, label %done, label %pushplain

checklen2:
  %is2 = icmp eq i64 %seglen, 2
  br i1 %is2, label %checkdot2, label %pushplain

checkdot2:
  %d0p = getelementptr i8, ptr %raw, i64 %start
  %d0 = load i8, ptr %d0p, align 1
  %start1 = add i64 %start, 1
  %d1p = getelementptr i8, ptr %raw, i64 %start1
  %d1 = load i8, ptr %d1p, align 1
  %d0dot = icmp eq i8 %d0, 46
  %d1dot = icmp eq i8 %d1, 46
  %bothdot = and i1 %d0dot, %d1dot
  br i1 %bothdot, label %handledotdot, label %pushplain

handledotdot:
  %slen_p = getelementptr { ptr, i64, i64 }, ptr %stackslot, i32 0, i32 1
  %curslen = load i64, ptr %slen_p, align 8
  %stackempty = icmp eq i64 %curslen, 0
  br i1 %stackempty, label %dd_push_or_drop, label %dd_check_top

dd_check_top:
  %sdata_p = getelementptr { ptr, i64, i64 }, ptr %stackslot, i32 0, i32 0
  %curdata = load ptr, ptr %sdata_p, align 8
  %topidx = sub i64 %curslen, 1
  %topslot = getelementptr ptr, ptr %curdata, i64 %topidx
  %topptr = load ptr, ptr %topslot, align 8
  %t0 = load i8, ptr %topptr, align 1
  %t1p = getelementptr i8, ptr %topptr, i64 1
  %t1 = load i8, ptr %t1p, align 1
  %t2p = getelementptr i8, ptr %topptr, i64 2
  %t2 = load i8, ptr %t2p, align 1
  %t0dot = icmp eq i8 %t0, 46
  %t1dot = icmp eq i8 %t1, 46
  %t2null = icmp eq i8 %t2, 0
  %ta = and i1 %t0dot, %t1dot
  %topisdotdot = and i1 %ta, %t2null
  br i1 %topisdotdot, label %dd_push_or_drop, label %dd_pop

dd_pop:
  %newslen = sub i64 %curslen, 1
  store i64 %newslen, ptr %slen_p, align 8
  br label %done

dd_push_or_drop:
  br i1 %is_absolute, label %done, label %dd_push_dotdot

dd_push_dotdot:
  %ddbuf = call ptr @__kml_str_alloc(i64 2)
  store i8 46, ptr %ddbuf, align 1
  %ddp1 = getelementptr i8, ptr %ddbuf, i64 1
  store i8 46, ptr %ddp1, align 1
  %ddp2 = getelementptr i8, ptr %ddbuf, i64 2
  store i8 0, ptr %ddp2, align 1
  call void @__kml_path_stack_append(ptr %stackslot, ptr %ddbuf)
  br label %done

pushplain:
  %pbuf = call ptr @__kml_str_alloc(i64 %seglen)
  %psrc = getelementptr i8, ptr %raw, i64 %start
  call ptr @memcpy(ptr %pbuf, ptr %psrc, i64 %seglen)
  %pnullp = getelementptr i8, ptr %pbuf, i64 %seglen
  store i8 0, ptr %pnullp, align 1
  call void @__kml_path_stack_append(ptr %stackslot, ptr %pbuf)
  br label %done

done:
  ret void
}

define ptr @__kml_path_normalize(ptr %raw, i1 %is_absolute) {
entry:
  %len = call i64 @strlen(ptr %raw)
  %stackslot = call ptr @malloc(i64 24)
  %sdata_p = getelementptr { ptr, i64, i64 }, ptr %stackslot, i32 0, i32 0
  %slen_p = getelementptr { ptr, i64, i64 }, ptr %stackslot, i32 0, i32 1
  %scap_p = getelementptr { ptr, i64, i64 }, ptr %stackslot, i32 0, i32 2
  store ptr null, ptr %sdata_p, align 8
  store i64 0, ptr %slen_p, align 8
  store i64 0, ptr %scap_p, align 8
  %i_a = alloca i64, align 8
  %segstart_a = alloca i64, align 8
  store i64 0, ptr %i_a, align 8
  store i64 0, ptr %segstart_a, align 8
  br label %scanloop

scanloop:
  %i = load i64, ptr %i_a, align 8
  %atend = icmp uge i64 %i, %len
  br i1 %atend, label %flushfinal, label %scancheck

scancheck:
  %cp = getelementptr i8, ptr %raw, i64 %i
  %c = load i8, ptr %cp, align 1
  %isslash = icmp eq i8 %c, 47
  br i1 %isslash, label %flushseg, label %advance

advance:
  %inext2 = add i64 %i, 1
  store i64 %inext2, ptr %i_a, align 8
  br label %scanloop

flushseg:
  %segstart_v = load i64, ptr %segstart_a, align 8
  call void @__kml_path_normalize_onesegment(ptr %stackslot, ptr %raw, i64 %segstart_v, i64 %i, i1 %is_absolute)
  %inext1 = add i64 %i, 1
  store i64 %inext1, ptr %i_a, align 8
  store i64 %inext1, ptr %segstart_a, align 8
  br label %scanloop

flushfinal:
  %segstart_f = load i64, ptr %segstart_a, align 8
  call void @__kml_path_normalize_onesegment(ptr %stackslot, ptr %raw, i64 %segstart_f, i64 %len, i1 %is_absolute)
  %result = call ptr @__kml_path_stack_join(ptr %stackslot, i1 %is_absolute)
  ret ptr %result
}`)
}

// ensurePathDirname declares __kml_path_dirname(ptr path) -> ptr: the
// directory portion of a path (trailing slashes trimmed first, then the
// portion before the last remaining '/'). Returns "." when path has no '/'
// at all, "/" when path is (after trimming) at the filesystem root.
func (e *Emitter) ensurePathDirname() {
	if e.usedPathDirname {
		return
	}
	e.usedPathDirname = true
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureStrlen()
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`
define ptr @__kml_path_dirname(ptr %path) {
entry:
  %len = call i64 @strlen(ptr %path)
  %lenzero = icmp eq i64 %len, 0
  br i1 %lenzero, label %ret_dot, label %trimtrail_init

ret_dot:
  %dotbuf = call ptr @__kml_str_alloc(i64 1)
  store i8 46, ptr %dotbuf, align 1
  %dotnull = getelementptr i8, ptr %dotbuf, i64 1
  store i8 0, ptr %dotnull, align 1
  ret ptr %dotbuf

trimtrail_init:
  %end_a = alloca i64, align 8
  store i64 %len, ptr %end_a, align 8
  br label %trimtrail_check

trimtrail_check:
  %end1 = load i64, ptr %end_a, align 8
  %endgt0 = icmp sgt i64 %end1, 0
  br i1 %endgt0, label %trimtrail_test, label %trimtrail_done

trimtrail_test:
  %tprev = sub i64 %end1, 1
  %tpp = getelementptr i8, ptr %path, i64 %tprev
  %tc = load i8, ptr %tpp, align 1
  %tisslash = icmp eq i8 %tc, 47
  br i1 %tisslash, label %trimtrail_step, label %trimtrail_done

trimtrail_step:
  store i64 %tprev, ptr %end_a, align 8
  br label %trimtrail_check

trimtrail_done:
  %endfinal = load i64, ptr %end_a, align 8
  %allslash = icmp eq i64 %endfinal, 0
  br i1 %allslash, label %ret_root, label %findslash_init

ret_root:
  %rootbuf = call ptr @__kml_str_alloc(i64 1)
  store i8 47, ptr %rootbuf, align 1
  %rootnull = getelementptr i8, ptr %rootbuf, i64 1
  store i8 0, ptr %rootnull, align 1
  ret ptr %rootbuf

findslash_init:
  %scan_a = alloca i64, align 8
  %init_scan = sub i64 %endfinal, 1
  store i64 %init_scan, ptr %scan_a, align 8
  br label %findslash_check

findslash_check:
  %scan1 = load i64, ptr %scan_a, align 8
  %scanlt0 = icmp slt i64 %scan1, 0
  br i1 %scanlt0, label %ret_dot2, label %findslash_test

ret_dot2:
  %dotbuf2 = call ptr @__kml_str_alloc(i64 1)
  store i8 46, ptr %dotbuf2, align 1
  %dotnull2 = getelementptr i8, ptr %dotbuf2, i64 1
  store i8 0, ptr %dotnull2, align 1
  ret ptr %dotbuf2

findslash_test:
  %sp = getelementptr i8, ptr %path, i64 %scan1
  %sc = load i8, ptr %sp, align 1
  %sisslash = icmp eq i8 %sc, 47
  br i1 %sisslash, label %findslash_done, label %findslash_step

findslash_step:
  %scanprev = sub i64 %scan1, 1
  store i64 %scanprev, ptr %scan_a, align 8
  br label %findslash_check

findslash_done:
  %slashpos = load i64, ptr %scan_a, align 8
  %isroot = icmp eq i64 %slashpos, 0
  br i1 %isroot, label %ret_root2, label %ret_substr

ret_root2:
  %rootbuf2 = call ptr @__kml_str_alloc(i64 1)
  store i8 47, ptr %rootbuf2, align 1
  %rootnull2 = getelementptr i8, ptr %rootbuf2, i64 1
  store i8 0, ptr %rootnull2, align 1
  ret ptr %rootbuf2

ret_substr:
  %sublen1 = add i64 %slashpos, 1
  %subbuf = call ptr @__kml_str_alloc(i64 %slashpos)
  call ptr @memcpy(ptr %subbuf, ptr %path, i64 %slashpos)
  %subnull = getelementptr i8, ptr %subbuf, i64 %slashpos
  store i8 0, ptr %subnull, align 1
  ret ptr %subbuf
}`)
}

// ensurePathBasename declares __kml_path_basename(ptr path, ptr ext) -> ptr:
// the final path segment (trailing slashes trimmed first), with ext (a
// possibly-null ptr — null meaning "no ext argument given", the same
// nullable-ptr convention emitGetenvCall's doc comment already documents)
// stripped from the end if the segment ends with it exactly.
func (e *Emitter) ensurePathBasename() {
	if e.usedPathBasename {
		return
	}
	e.usedPathBasename = true
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureStrlen()
	e.ensureStrcmp()
	e.emitGlobal(`
define ptr @__kml_path_basename(ptr %path, ptr %ext) {
entry:
  %extisnull0 = icmp eq ptr %ext, null
  br i1 %extisnull0, label %trimtrail_entry, label %checkwholepath

checkwholepath:
  %wholecmp = call i32 @strcmp(ptr %path, ptr %ext)
  %wholematch = icmp eq i32 %wholecmp, 0
  br i1 %wholematch, label %ret_empty0, label %trimtrail_entry

trimtrail_entry:
  %len = call i64 @strlen(ptr %path)
  %lenzero = icmp eq i64 %len, 0
  br i1 %lenzero, label %ret_empty0, label %trimtrail_init

ret_empty0:
  %ebuf0 = call ptr @__kml_str_alloc(i64 0)
  store i8 0, ptr %ebuf0, align 1
  ret ptr %ebuf0

trimtrail_init:
  %end_a = alloca i64, align 8
  store i64 %len, ptr %end_a, align 8
  br label %trimtrail_check

trimtrail_check:
  %end1 = load i64, ptr %end_a, align 8
  %endgt0 = icmp sgt i64 %end1, 0
  br i1 %endgt0, label %trimtrail_test, label %trimtrail_done

trimtrail_test:
  %tprev = sub i64 %end1, 1
  %tpp = getelementptr i8, ptr %path, i64 %tprev
  %tc = load i8, ptr %tpp, align 1
  %tisslash = icmp eq i8 %tc, 47
  br i1 %tisslash, label %trimtrail_step, label %trimtrail_done

trimtrail_step:
  store i64 %tprev, ptr %end_a, align 8
  br label %trimtrail_check

trimtrail_done:
  %endfinal = load i64, ptr %end_a, align 8
  %allslash = icmp eq i64 %endfinal, 0
  br i1 %allslash, label %ret_empty1, label %findslash_init

ret_empty1:
  %ebuf1 = call ptr @__kml_str_alloc(i64 0)
  store i8 0, ptr %ebuf1, align 1
  ret ptr %ebuf1

findslash_init:
  %scan_a = alloca i64, align 8
  %initscan = sub i64 %endfinal, 1
  store i64 %initscan, ptr %scan_a, align 8
  br label %findslash_check

findslash_check:
  %scan1 = load i64, ptr %scan_a, align 8
  %scanlt0 = icmp slt i64 %scan1, 0
  br i1 %scanlt0, label %findslash_done, label %findslash_test

findslash_test:
  %sp = getelementptr i8, ptr %path, i64 %scan1
  %sc = load i8, ptr %sp, align 1
  %sisslash = icmp eq i8 %sc, 47
  br i1 %sisslash, label %findslash_done, label %findslash_step

findslash_step:
  %scanprev = sub i64 %scan1, 1
  store i64 %scanprev, ptr %scan_a, align 8
  br label %findslash_check

findslash_done:
  %slashpos = load i64, ptr %scan_a, align 8
  %start = add i64 %slashpos, 1
  %baselen = sub i64 %endfinal, %start
  %baselen1 = add i64 %baselen, 1
  %basebuf = call ptr @__kml_str_alloc(i64 %baselen)
  %basesrc = getelementptr i8, ptr %path, i64 %start
  call ptr @memcpy(ptr %basebuf, ptr %basesrc, i64 %baselen)
  %basenull = getelementptr i8, ptr %basebuf, i64 %baselen
  store i8 0, ptr %basenull, align 1
  %extisnull = icmp eq ptr %ext, null
  br i1 %extisnull, label %done, label %checkext

checkext:
  %extlen = call i64 @strlen(ptr %ext)
  %extlenzero = icmp eq i64 %extlen, 0
  %extlonger = icmp uge i64 %extlen, %baselen
  %skipext = or i1 %extlenzero, %extlonger
  br i1 %skipext, label %done, label %sufcheck

sufcheck:
  %sufoffset = sub i64 %baselen, %extlen
  %sufp = getelementptr i8, ptr %basebuf, i64 %sufoffset
  %cmp = call i32 @strcmp(ptr %sufp, ptr %ext)
  %matches = icmp eq i32 %cmp, 0
  br i1 %matches, label %truncate, label %done

truncate:
  ; The suffix matched: return a base of length %sufoffset. A bare NUL store
  ; would not work — length-carrying strings (TDD-00120) read their i64 header,
  ; not a terminator, so .length/=== would still see the full untruncated length.
  ; Allocate a correctly-headered buffer instead.
  %tbuf = call ptr @__kml_str_alloc(i64 %sufoffset)
  call ptr @memcpy(ptr %tbuf, ptr %basebuf, i64 %sufoffset)
  %tnull = getelementptr i8, ptr %tbuf, i64 %sufoffset
  store i8 0, ptr %tnull, align 1
  ret ptr %tbuf

done:
  ret ptr %basebuf
}`)
}

// ensurePathExtname declares __kml_path_extname(ptr path) -> ptr: the
// extension (including the leading '.') of path's basename, or "" if the
// basename has no '.' or its only '.' is the leading character (a dotfile
// like ".gitignore" has no extension — matches real Node).
func (e *Emitter) ensurePathExtname() {
	if e.usedPathExtname {
		return
	}
	e.usedPathExtname = true
	e.ensurePathBasename()
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureStrlen()
	e.emitGlobal(`
define ptr @__kml_path_extname(ptr %path) {
entry:
  %base = call ptr @__kml_path_basename(ptr %path, ptr null)
  %blen = call i64 @strlen(ptr %base)
  %scan_a = alloca i64, align 8
  %initscan = sub i64 %blen, 1
  store i64 %initscan, ptr %scan_a, align 8
  br label %scancheck

scancheck:
  %scan1 = load i64, ptr %scan_a, align 8
  %scanlt0 = icmp slt i64 %scan1, 0
  br i1 %scanlt0, label %ret_empty, label %scantest

ret_empty:
  %ebuf = call ptr @__kml_str_alloc(i64 0)
  store i8 0, ptr %ebuf, align 1
  ret ptr %ebuf

scantest:
  %cp = getelementptr i8, ptr %base, i64 %scan1
  %c = load i8, ptr %cp, align 1
  %isdot = icmp eq i8 %c, 46
  br i1 %isdot, label %gotdot, label %scanstep

scanstep:
  %scanprev = sub i64 %scan1, 1
  store i64 %scanprev, ptr %scan_a, align 8
  br label %scancheck

gotdot:
  %dotpos = load i64, ptr %scan_a, align 8
  %atstart = icmp eq i64 %dotpos, 0
  br i1 %atstart, label %ret_empty2, label %ret_ext

ret_empty2:
  %ebuf2 = call ptr @__kml_str_alloc(i64 0)
  store i8 0, ptr %ebuf2, align 1
  ret ptr %ebuf2

ret_ext:
  %extlen = sub i64 %blen, %dotpos
  %extlen1 = add i64 %extlen, 1
  %extbuf = call ptr @__kml_str_alloc(i64 %extlen)
  %extsrc = getelementptr i8, ptr %base, i64 %dotpos
  call ptr @memcpy(ptr %extbuf, ptr %extsrc, i64 %extlen1)
  ret ptr %extbuf
}`)
}
