// gcshim.c — GC-mode allocator shim. Only compiled in and linked when
// -mm=gc is set (see main.go). Defines malloc/calloc/realloc/free so that
// this executable's own already-undefined @malloc/@calloc/@realloc/@free
// references — emitted, unchanged, by every existing runtime.go/emit_*.go
// call site — resolve to the Boehm-Demers-Weiser conservative collector
// instead of libc's allocator. No changes needed anywhere else in the
// compiler: this is the only definition of these symbols in the link, so
// no special linker flags (-flat_namespace, --defsym, LD_PRELOAD) are
// needed on either Linux or macOS. See docs/adr/ADR-00071.md.
#include <gc.h>
#include <stddef.h>

__attribute__((constructor))
static void __kml_gc_ctor(void) {
	GC_INIT();
}

void *malloc(size_t size) {
	return GC_malloc(size);
}

void *calloc(size_t nmemb, size_t size) {
	// GC_malloc always zeroes its returned memory (unlike libc's malloc),
	// so a single call of the combined size already satisfies calloc's
	// contract — Boehm's public API has no separate GC_calloc.
	return GC_malloc(nmemb * size);
}

void *realloc(void *ptr, size_t size) {
	return GC_realloc(ptr, size);
}

void free(void *ptr) {
	// Documented by Boehm as always safe to call explicitly, as a latency
	// optimization (early-release a block known dead now rather than
	// waiting for the next collection cycle) — this is what makes
	// Memory.free(x) automatically become "release to the collector early"
	// in gc mode, with zero changes needed in emit_memory.go.
	GC_free(ptr);
}
