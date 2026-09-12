package llvm

import _ "embed"

// emit_usp_runtime.go — the embedded C runtime + ABI declarations for the
// ordered-pair-list URLSearchParams backing (TDD-00203, urlsearchparamssrc/
// urlsearchparams.c). Replaces the former Map<string,string> backing so
// duplicate keys and cross-key insertion order round-trip faithfully. The ABI
// is declared once (ensureURLSearchParams) and the C file is compiled+linked in
// when UsesURLSearchParams() is set (embedded_c.go), the same shape as the
// URLPattern runtime.

//go:embed urlsearchparamssrc/urlsearchparams.c
var urlSearchParamsSource string

// URLSearchParamsSource returns the C source implementing the __kml_usp_* ABI.
func URLSearchParamsSource() string { return urlSearchParamsSource }

// UsesURLSearchParams reports whether any URLSearchParams (or a URL's
// .searchParams) reached codegen, so the C runtime is compiled in.
func (e *Emitter) UsesURLSearchParams() bool { return e.usesURLSearchParams }

// ensureURLSearchParams declares the __kml_usp_* ABI once and marks the program
// as needing urlsearchparams.c compiled. No external library — plain C.
func (e *Emitter) ensureURLSearchParams() {
	e.usesURLSearchParams = true
	if e.declaredURLSearchP {
		return
	}
	e.declaredURLSearchP = true
	e.emitGlobal(`declare ptr @__kml_usp_create()`)
	e.emitGlobal(`declare void @__kml_usp_append(ptr, ptr, ptr)`)
	e.emitGlobal(`declare void @__kml_usp_set(ptr, ptr, ptr)`)
	e.emitGlobal(`declare ptr @__kml_usp_get(ptr, ptr)`)
	e.emitGlobal(`declare i32 @__kml_usp_has(ptr, ptr)`)
	e.emitGlobal(`declare i32 @__kml_usp_has2(ptr, ptr, ptr)`)
	e.emitGlobal(`declare void @__kml_usp_delete(ptr, ptr)`)
	e.emitGlobal(`declare void @__kml_usp_delete2(ptr, ptr, ptr)`)
	e.emitGlobal(`declare i64 @__kml_usp_size(ptr)`)
	e.emitGlobal(`declare ptr @__kml_usp_key_at(ptr, i64)`)
	e.emitGlobal(`declare ptr @__kml_usp_val_at(ptr, i64)`)
	e.emitGlobal(`declare {ptr, i64} @__kml_usp_get_all(ptr, ptr)`)
	e.emitGlobal(`declare {ptr, i64} @__kml_usp_keys(ptr)`)
	e.emitGlobal(`declare {ptr, i64} @__kml_usp_values(ptr)`)
	e.emitGlobal(`declare {ptr, i64} @__kml_usp_entries(ptr)`)
	e.emitGlobal(`declare void @__kml_usp_sort(ptr)`)
	e.emitGlobal(`declare ptr @__kml_usp_to_string(ptr)`)
	e.emitGlobal(`declare ptr @__kml_usp_inspect(ptr)`)
	e.emitGlobal(`declare void @__kml_usp_parse(ptr, ptr)`)
}
