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
	e.emitGlobal("declare ptr @memmem(ptr noundef, i64 noundef, ptr noundef, i64 noundef)")
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
}`)
	e.emitGlobal(`
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
  %bytes = mul i64 %argc, 8
  %arr = call ptr @malloc(i64 %bytes)
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
