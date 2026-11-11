// runtime_strheader.go — TDD-00120 Stage 1: length-prefixed string buffers.
//
// Every heap string is allocated as [ i64 byteLength ][ bytes… ][ \0 ], and the
// string value is a `ptr` to the bytes (base+8). The retained NUL keeps strlen
// consumers and C interop working unchanged during the migration; the header
// lets binary-safe length reads (Stage 2+) recover the true length past an
// embedded \0 via `ptr-8`.
//
//	__kml_str_alloc(n) -> ptr   malloc(n+9), store n at [0], return base+8
//	__kml_str_len(ptr) -> i64   the byte length, from the header at ptr-8
//	__kml_str_free(ptr)         free the base (ptr-8) — never free the value ptr
//
// The routines live in stringsrc/strheader.c (TDD-00240). __kml_argv_node_shape
// builds the Node-shaped process.argv: [a[0], a[0], a[1], … NULL].
//
// Go-side string producers call these via emitStringAlloc/emitStringFree
// (emit_strings.go); hand-written runtime IR calls @__kml_str_alloc directly.
package llvm

import "fmt"

import _ "embed"

//go:embed stringsrc/strheader.c
var strheaderSource string

//go:embed fetchsrc/request.c
var requestSource string

// RequestSource is `new Request(...)`'s init checks' C source (request.c).
func RequestSource() string { return requestSource }

// StrHeaderSource is the string-header runtime's C source.
func StrHeaderSource() string { return strheaderSource }

// ensureStrHeaderRuntime declares the string-header runtime (strheader.c) once.
func (e *Emitter) ensureStrHeaderRuntime() {
	if e.usedStrHeaderRuntime {
		return
	}
	e.usedStrHeaderRuntime = true
	// Other generated code leans on these decls arriving with the header runtime.
	// The leaf primitives are built here so every call site inlines them: a
	// call into a C object cannot be inlined without LTO.
	e.buildStrLeaves()
}

// buildStrLeaves defines __kml_str_alloc(n) (n bytes after a length header
// word, one more for the NUL), __kml_str_len(s) (the header's low 32 bits)
// and __kml_str_free(s), weak_odr, so each unit inlines its own copy.
func (e *Emitter) buildStrLeaves() {
	restore := e.beginDetachedFunc()
	sz, base, p := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %%n, 9", sz))
	e.heapMallocInto(base, allocString, sz)
	e.heapStore("i64", "%n", base, 8)
	e.emitBodyAddr(p, base)
	e.emitTerminator("ret " + irPtr + " " + p)
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("weak_odr "+irPtr+" @__kml_str_alloc(i64 %n)", body)

	restore = e.beginDetachedFunc()
	hp, h, n := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitPrefixWordAddr(hp, "%s")
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", h, hp))
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, 4294967295", n, h))
	e.emitTerminator("ret i64 " + n)
	body = e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("weak_odr i64 @__kml_str_len("+irPtr+" %s)", body)

	restore = e.beginDetachedFunc()
	fb := e.freshReg()
	e.emitPrefixWordAddr(fb, "%s")
	e.heapFree(allocString, fb)
	e.emitTerminator("ret void")
	body = e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("weak_odr void @__kml_str_free("+irPtr+" %s)", body)
}
