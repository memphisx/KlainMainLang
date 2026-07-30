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

#if defined(__linux__)
// On at least one Boehm GC build (Ubuntu's libgc-dev package, confirmed via
// a minimal reproduction outside this compiler entirely — see
// docs/adr/ADR-00080.md), letting GC_INIT() auto-detect the main thread's
// stack bounds crashes: its glibc pthread_getattr_np() call parses
// /proc/self/maps through a growing malloc'd buffer, and since this shim
// makes malloc the *only* malloc in the whole link, that call reenters
// GC_malloc() while GC_init() is still mid-setup — corrupting enough
// internal state to either abort ("Exclusion ranges overlap") or segfault
// inside pthread_getattr_np itself. gc.h documents GC_stackbottom as safe,
// and cheaper, to set explicitly "prior to calling any GC_ routines" —
// doing so from __libc_stack_end (glibc's own record of the initial
// thread's true stack top, set by the kernel/dynamic linker before any
// user code runs) makes GC_init() skip auto-detection entirely, so the
// crashing codepath is never reached.
extern void *__libc_stack_end __attribute__((weak));
#endif

// This can't be done only from __kml_gc_ctor: a dynamically-linked
// dependency (observed with libp11-kit, pulled in transitively via
// libcurl -> GnuTLS) can run its own constructor first and call plain
// malloc() from it — ELF runs a shared object's constructors before the
// executable that depends on it, regardless of any __attribute__
// ((constructor(N))) priority put on ours, since priority only orders
// constructors *within* one object. That first malloc() auto-inits Boehm
// GC before our constructor ever gets a chance to run, so the stackbottom
// fix has to happen lazily, on demand, from malloc() itself too.
static int gcStackbottomSet = 0;

static void ensureGCStackbottomSet(void) {
	if (gcStackbottomSet) {
		return;
	}
	gcStackbottomSet = 1;
#if defined(__linux__)
	if (__libc_stack_end != NULL) {
		GC_stackbottom = (char *)__libc_stack_end;
	}
#endif
}

__attribute__((constructor))
static void __kml_gc_ctor(void) {
	ensureGCStackbottomSet();
	GC_INIT();
}

void *malloc(size_t size) {
	ensureGCStackbottomSet();
	return GC_malloc(size);
}

void *calloc(size_t nmemb, size_t size) {
	ensureGCStackbottomSet();
	// GC_malloc always zeroes its returned memory (unlike libc's malloc),
	// so a single call of the combined size already satisfies calloc's
	// contract — Boehm's public API has no separate GC_calloc.
	return GC_malloc(nmemb * size);
}

void *realloc(void *ptr, size_t size) {
	ensureGCStackbottomSet();
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
