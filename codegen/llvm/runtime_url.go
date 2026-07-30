package llvm

// ensureCurlURL declares libcurl's URL API (urlapi.h) — used by `new
// URL(...)` (emit_url.go) to parse an absolute URL string into its parts
// (scheme, host, port, path, query, fragment) without hand-rolling a URL
// parser: `curl_url()` creates a handle, `curl_url_set` parses a string
// into it (or fails with a non-zero CURLUcode on a malformed URL),
// `curl_url_get` extracts one part at a time (also fails with a non-zero
// code when that part is simply absent, e.g. no port/query/fragment — not
// necessarily malformed), and `curl_url_cleanup` frees the handle itself.
// Reuses the same "long-lived C library, no new build dependency" approach
// fetch()'s own libcurl linkage already established.
//
// Deliberately does NOT declare `curl_free()` — every string
// `curl_url_get` returns is intentionally leaked rather than freed here
// (see emit_url.go's construction code), the same "a short-lived CLI
// program's bounded per-call leak is a non-issue in the default manual
// memory-management mode" tradeoff docs/status/MEMORY-MANAGEMENT.md already documents elsewhere,
// and moot entirely under `-mm=gc`.
func (e *Emitter) ensureCurlURL() {
	if e.usedCurlURL {
		return
	}
	e.usedCurlURL = true
	e.requireLink("curl")
	e.emitGlobal("declare ptr @curl_url()")
	e.emitGlobal("declare void @curl_url_cleanup(ptr noundef)")
	e.emitGlobal("declare i32 @curl_url_get(ptr noundef, i32 noundef, ptr noundef, i32 noundef)")
	e.emitGlobal("declare i32 @curl_url_set(ptr noundef, i32 noundef, ptr noundef, i32 noundef)")
}
