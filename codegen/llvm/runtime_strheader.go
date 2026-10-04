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
// The routines live in stringsrc/strheader.c (TDD-00240). __kml_argv_node_shape
// builds the Node-shaped process.argv: [a[0], a[0], a[1], … NULL].
//
// Go-side string producers call these via emitStringAlloc/emitStringFree
// (emit_strings.go); hand-written runtime IR calls @__kml_str_alloc directly.
package llvm

import _ "embed"

//go:embed stringsrc/strheader.c
var strheaderSource string

// StrHeaderSource is the string-header runtime's C source.
func StrHeaderSource() string { return strheaderSource }

// UsesStrHeaderC reports whether the program links the string-header runtime.
func (e *Emitter) UsesStrHeaderC() bool { return e.usedStrHeaderRuntime }

// ensureStrHeaderRuntime declares the string-header runtime (strheader.c) once.
func (e *Emitter) ensureStrHeaderRuntime() {
	if e.usedStrHeaderRuntime {
		return
	}
	e.usedStrHeaderRuntime = true
	// Other generated code leans on these decls arriving with the header runtime.
	e.ensureMalloc()
	e.ensureFree()
	e.ensureStrlen()
	e.ensureMemcpy()
	e.ensureMemcmp()
	e.ensureMemmem() // the C runtime calls memmem (defined in IR on Windows)
	e.emitGlobal(`declare i32 @__kml_str_cmp(ptr, ptr)
declare zeroext i1 @__kml_str_startswith_at(ptr, ptr, i64)
declare zeroext i1 @__kml_str_endswith_at(ptr, ptr, i64)
declare i64 @__kml_str_lastindexof(ptr, ptr)
declare i64 @__kml_str_lastindexof_from(ptr, ptr, i64)
declare ptr @__kml_str_from_cstr(ptr)
declare void @__kml_str_finalize(ptr)
declare ptr @__kml_argv_headerize(i64, ptr)
declare ptr @__kml_argv_node_shape(i64, ptr)`)
	// The leaf primitives stay IR so every call site inlines them: a call
	// into a C object cannot be inlined without LTO.
	e.emitGlobal(`define weak_odr ptr @__kml_str_alloc(i64 %n) {
entry:
  %sz = add i64 %n, 9
  %base = call ptr @malloc(i64 %sz)
  store i64 %n, ptr %base, align 8
  %p = getelementptr i8, ptr %base, i64 8
  ret ptr %p
}
define weak_odr i64 @__kml_str_len(ptr %s) {
entry:
  %hp = getelementptr i8, ptr %s, i64 -8
  %n = load i64, ptr %hp, align 8
  ret i64 %n
}
define weak_odr void @__kml_str_free(ptr %s) {
entry:
  %base = getelementptr i8, ptr %s, i64 -8
  call void @free(ptr %base)
  ret void
}`)
}

// ensureMemmem declares memmem, or on Windows defines it: memmem is a
// GNU/BSD extension absent from every Windows C runtime (UCRT included). A byte-loop definition in IR keeps
// the Windows build free of an extra C shim file (TDD-00177 Stage 0).
func (e *Emitter) ensureMemmem() {
	if e.usedMemmem {
		return
	}
	e.usedMemmem = true
	if e.opts.Target.OS() != "windows" {
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
