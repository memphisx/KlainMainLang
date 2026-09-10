// runtime_strheader.go — TDD-00120 Stage 1: length-prefixed string buffers.
//
// Every heap string is allocated as [ i64 byteLength ][ bytes… ][ \0 ], and the
// string value is a `ptr` to the bytes (base+8). The retained NUL keeps strlen
// consumers and C interop working unchanged during the migration; the header
// lets binary-safe length reads (Stage 2+) recover the true length past an
// embedded \0 via `ptr-8`.
//
//	__kml_str_alloc(n) -> ptr   malloc(n+9), store n at [0], return base+8
//	__kml_str_len(ptr) -> i64   load the header at ptr-8
//	__kml_str_free(ptr)         free the base (ptr-8) — never free the value ptr
//
// Go-side string producers call these via emitStringAlloc/emitStringFree
// (emit_strings.go); hand-written runtime IR calls @__kml_str_alloc directly.
package llvm

func (e *Emitter) ensureStrHeaderRuntime() {
	if e.usedStrHeaderRuntime {
		return
	}
	e.usedStrHeaderRuntime = true
	e.ensureMalloc()
	e.ensureFree()
	e.ensureStrlen()
	e.ensureMemcpy()
	e.ensureMemcmp() // shared decl (emit_buffer.go) — avoid a duplicate memcmp
	e.ensureMemmem()
	// TDD-00120 Stage 2 binary-safe primitives. __kml_str_cmp is a strcmp-shaped
	// (<0/0/>0) lexicographic compare using the header lengths + memcmp, so an
	// embedded NUL no longer stops the comparison early. __kml_str_indexof is a
	// strstr-shaped substring search via memmem, returning the byte index or -1.
	e.emitGlobal(`
define i32 @__kml_str_cmp(ptr %a, ptr %b) {
entry:
  %la = call i64 @__kml_str_len(ptr %a)
  %lb = call i64 @__kml_str_len(ptr %b)
  %alt = icmp ult i64 %la, %lb
  %minl = select i1 %alt, i64 %la, i64 %lb
  %c = call i32 @memcmp(ptr %a, ptr %b, i64 %minl)
  %cne = icmp ne i32 %c, 0
  br i1 %cne, label %ret_c, label %ck_len
ret_c:
  ret i32 %c
ck_len:
  %ltlen = icmp ult i64 %la, %lb
  br i1 %ltlen, label %ret_neg, label %ck_gt
ret_neg:
  ret i32 -1
ck_gt:
  %gtlen = icmp ugt i64 %la, %lb
  br i1 %gtlen, label %ret_pos, label %ret_eq
ret_pos:
  ret i32 1
ret_eq:
  ret i32 0
}
define i64 @__kml_str_indexof(ptr %hay, ptr %needle) {
entry:
  %lh = call i64 @__kml_str_len(ptr %hay)
  %ln = call i64 @__kml_str_len(ptr %needle)
  %p = call ptr @memmem(ptr %hay, i64 %lh, ptr %needle, i64 %ln)
  %isnull = icmp eq ptr %p, null
  br i1 %isnull, label %notfound, label %found
found:
  %pi = ptrtoint ptr %p to i64
  %hi = ptrtoint ptr %hay to i64
  %off = sub i64 %pi, %hi
  ret i64 %off
notfound:
  ret i64 -1
}
define i64 @__kml_str_indexof_from(ptr %hay, ptr %needle, i64 %from) {
entry:
  %lh = call i64 @__kml_str_len(ptr %hay)
  %ln = call i64 @__kml_str_len(ptr %needle)
  ; clamp from to [0, lh]
  %fneg = icmp slt i64 %from, 0
  %f0 = select i1 %fneg, i64 0, i64 %from
  %ftoobig = icmp sgt i64 %f0, %lh
  %fc = select i1 %ftoobig, i64 %lh, i64 %f0
  %sublen = sub i64 %lh, %fc
  %hp = getelementptr i8, ptr %hay, i64 %fc
  %p = call ptr @memmem(ptr %hp, i64 %sublen, ptr %needle, i64 %ln)
  %isnull = icmp eq ptr %p, null
  br i1 %isnull, label %ifnotfound, label %iffound
iffound:
  %pi = ptrtoint ptr %p to i64
  %hi = ptrtoint ptr %hay to i64
  %off = sub i64 %pi, %hi
  ret i64 %off
ifnotfound:
  ret i64 -1
}
define i1 @__kml_str_startswith_at(ptr %hay, ptr %needle, i64 %pos) {
entry:
  %lh = call i64 @__kml_str_len(ptr %hay)
  %ln = call i64 @__kml_str_len(ptr %needle)
  %pneg = icmp slt i64 %pos, 0
  %p0 = select i1 %pneg, i64 0, i64 %pos
  %ptoobig = icmp sgt i64 %p0, %lh
  %pc = select i1 %ptoobig, i64 %lh, i64 %p0
  %end = add i64 %pc, %ln
  %oob = icmp sgt i64 %end, %lh
  br i1 %oob, label %swno, label %swcmp
swcmp:
  %hp = getelementptr i8, ptr %hay, i64 %pc
  %c = call i32 @memcmp(ptr %hp, ptr %needle, i64 %ln)
  %eq = icmp eq i32 %c, 0
  ret i1 %eq
swno:
  ret i1 false
}
define i1 @__kml_str_endswith_at(ptr %hay, ptr %needle, i64 %endpos) {
entry:
  %lh = call i64 @__kml_str_len(ptr %hay)
  %ln = call i64 @__kml_str_len(ptr %needle)
  %eneg = icmp slt i64 %endpos, 0
  %e0 = select i1 %eneg, i64 0, i64 %endpos
  %etoobig = icmp sgt i64 %e0, %lh
  %ec = select i1 %etoobig, i64 %lh, i64 %e0
  %start = sub i64 %ec, %ln
  %oob = icmp slt i64 %start, 0
  br i1 %oob, label %ewno, label %ewcmp
ewcmp:
  %hp = getelementptr i8, ptr %hay, i64 %start
  %c = call i32 @memcmp(ptr %hp, ptr %needle, i64 %ln)
  %eq = icmp eq i32 %c, 0
  ret i1 %eq
ewno:
  ret i1 false
}
define i64 @__kml_str_lastindexof(ptr %hay, ptr %needle) {
entry:
  %lh = call i64 @__kml_str_len(ptr %hay)
  %ln = call i64 @__kml_str_len(ptr %needle)
  %start = sub i64 %lh, %ln
  %neg = icmp slt i64 %start, 0
  br i1 %neg, label %lnotfound, label %linit
linit:
  %offp = alloca i64, align 8
  store i64 %start, ptr %offp, align 8
  br label %lcond
lcond:
  %off = load i64, ptr %offp, align 8
  %lt0 = icmp slt i64 %off, 0
  br i1 %lt0, label %lnotfound, label %lbody
lbody:
  %hp = getelementptr i8, ptr %hay, i64 %off
  %c = call i32 @memcmp(ptr %hp, ptr %needle, i64 %ln)
  %match = icmp eq i32 %c, 0
  br i1 %match, label %lfound, label %ldec
lfound:
  ret i64 %off
ldec:
  %offn = sub i64 %off, 1
  store i64 %offn, ptr %offp, align 8
  br label %lcond
lnotfound:
  ret i64 -1
}`)
	e.emitGlobal(`
define i64 @__kml_str_lastindexof_from(ptr %hay, ptr %needle, i64 %from) {
entry:
  %lh = call i64 @__kml_str_len(ptr %hay)
  %ln = call i64 @__kml_str_len(ptr %needle)
  %maxstart = sub i64 %lh, %ln
  ; start = min(from, lh-ln); a negative from falls straight through to -1
  %fbig = icmp sgt i64 %from, %maxstart
  %start = select i1 %fbig, i64 %maxstart, i64 %from
  %offp = alloca i64, align 8
  store i64 %start, ptr %offp, align 8
  br label %lfcond
lfcond:
  %off = load i64, ptr %offp, align 8
  %lt0 = icmp slt i64 %off, 0
  br i1 %lt0, label %lfnotfound, label %lfbody
lfbody:
  %hp = getelementptr i8, ptr %hay, i64 %off
  %c = call i32 @memcmp(ptr %hp, ptr %needle, i64 %ln)
  %match = icmp eq i32 %c, 0
  br i1 %match, label %lffound, label %lfdec
lffound:
  ret i64 %off
lfdec:
  %offn = sub i64 %off, 1
  store i64 %offn, ptr %offp, align 8
  br label %lfcond
lfnotfound:
  ret i64 -1
}
define ptr @__kml_str_from_cstr(ptr %c) {
entry:
  %isnull = icmp eq ptr %c, null
  br i1 %isnull, label %retnull, label %copy
copy:
  %len = call i64 @strlen(ptr %c)
  %dst = call ptr @__kml_str_alloc(i64 %len)
  %lp1 = add i64 %len, 1
  call ptr @memcpy(ptr %dst, ptr %c, i64 %lp1)
  ret ptr %dst
retnull:
  ret ptr null
}
define void @__kml_str_finalize(ptr %s) {
entry:
  %l = call i64 @strlen(ptr %s)
  %hp = getelementptr i8, ptr %s, i64 -8
  store i64 %l, ptr %hp, align 8
  ret void
}
define ptr @__kml_str_alloc(i64 %n) {
entry:
  %sz = add i64 %n, 9
  %base = call ptr @malloc(i64 %sz)
  store i64 %n, ptr %base, align 8
  %p = getelementptr i8, ptr %base, i64 8
  ret ptr %p
}
define i64 @__kml_str_len(ptr %s) {
entry:
  %hp = getelementptr i8, ptr %s, i64 -8
  %n = load i64, ptr %hp, align 8
  ret i64 %n
}
define void @__kml_str_free(ptr %s) {
entry:
  %base = getelementptr i8, ptr %s, i64 -8
  call void @free(ptr %base)
  ret void
}
define ptr @__kml_argv_headerize(i64 %argc, ptr %argv) {
entry:
  ; argc+1 slots: the array is also handed to execv(), which needs the
  ; trailing NULL (a missing terminator read past the end on every host).
  %argc1 = add i64 %argc, 1
  %bytes = mul i64 %argc1, 8
  %arr = call ptr @malloc(i64 %bytes)
  %endp = getelementptr ptr, ptr %arr, i64 %argc
  store ptr null, ptr %endp, align 8
  %ip = alloca i64, align 8
  store i64 0, ptr %ip, align 8
  br label %loop
loop:
  %i = load i64, ptr %ip, align 8
  %cont = icmp slt i64 %i, %argc
  br i1 %cont, label %body, label %done
body:
  %srcp = getelementptr ptr, ptr %argv, i64 %i
  %src = load ptr, ptr %srcp, align 8
  %copy = call ptr @__kml_str_from_cstr(ptr %src)
  %dstp = getelementptr ptr, ptr %arr, i64 %i
  store ptr %copy, ptr %dstp, align 8
  %inext = add i64 %i, 1
  store i64 %inext, ptr %ip, align 8
  br label %loop
done:
  ret ptr %arr
}`)
}

// ensureMemmem declares memmem, or on Windows defines it: memmem is a
// GNU/BSD extension absent from every Windows C runtime (UCRT included), and
// __kml_str_indexof is the one consumer. A byte-loop definition in IR keeps
// the Windows build free of an extra C shim file (TDD-00177 Stage 0).
func (e *Emitter) ensureMemmem() {
	if e.usedMemmem {
		return
	}
	e.usedMemmem = true
	if targetGOOS() != "windows" {
		e.emitGlobal("declare ptr @memmem(ptr noundef, i64 noundef, ptr noundef, i64 noundef)")
		return
	}
	e.ensureMemcmp()
	e.emitGlobal(`
define ptr @memmem(ptr %hay, i64 %hlen, ptr %needle, i64 %nlen) {
entry:
  %empty = icmp eq i64 %nlen, 0
  br i1 %empty, label %found0, label %check
found0:
  ret ptr %hay
check:
  %toolong = icmp ugt i64 %nlen, %hlen
  br i1 %toolong, label %notfound, label %loop
loop:
  %i = phi i64 [ 0, %check ], [ %inext, %next ]
  %last = sub i64 %hlen, %nlen
  %done = icmp ugt i64 %i, %last
  br i1 %done, label %notfound, label %cmp
cmp:
  %p = getelementptr i8, ptr %hay, i64 %i
  %c = call i32 @memcmp(ptr %p, ptr %needle, i64 %nlen)
  %eq = icmp eq i32 %c, 0
  br i1 %eq, label %found, label %next
next:
  %inext = add i64 %i, 1
  br label %loop
found:
  ret ptr %p
notfound:
  ret ptr null
}`)
}
