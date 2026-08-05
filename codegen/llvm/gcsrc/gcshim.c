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
#include <string.h>

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

// preCtorBump: a tiny, fixed-size, no-real-free bump allocator serving
// every malloc/calloc/realloc/free call that happens before __kml_gc_ctor
// has actually run — found necessary (not just theoretical) via ADR-00100:
// building this project's own test suite under AddressSanitizer showed
// even a trivial "console.log('hello')" -mm=gc program segfaulting before
// any user code runs. gdb traced it to ASan's own startup
// (__asan_init -> InitializeAsanInterceptors -> dlsym(), hunting for
// __isoc99_printf) hitting its own internal error path, which calls
// malloc() — routing to *this* shim's malloc() — so early that Boehm's
// GC_malloc_kind() (which needs thread-local storage via __tls_get_addr())
// crashes calling into ASan's own not-yet-installed __tls_get_addr
// interceptor: a genuine circular bootstrap dependency, reentering the
// very function that was about to finish setting up the interceptor table.
//
// This is a strictly earlier trigger than the __libc_stack_end-focused fix
// below was ever designed for (ASan's dlsym probing runs during the
// dynamic linker's own _dl_init, before __libc_start_main has even set
// __libc_stack_end) — no amount of stackbottom-setting logic avoids it,
// since the crash is inside GC_malloc's own TLS access, not anything this
// shim's own code touches directly. The only robust fix is to not call
// into Boehm *at all* until __kml_gc_ctor has run in a known-safe context.
//
// ADR-00080 already considered and rejected a bump allocator for the
// *previous* earliest-known trigger (libp11-kit's constructor calling
// malloc before this shim's own constructor) specifically because
// pthread_getattr_np's /proc/self/maps read needs a *growing* buffer via
// realloc, which a naive bump allocator can't serve without real per-block
// size tracking. That rejection doesn't apply here: every pre-ctor
// allocation observed (both the libp11-kit case and this new ASan case)
// is small and one-off, never freed or grown within this narrow window,
// which is exactly what a fixed bump buffer is good for. 64KB is a
// generous multiple of the largest single pre-ctor allocation actually
// observed (dlsym's ~144-byte error-message buffer); genuinely running
// out here falls through to ordinary GC_malloc (see bumpAlloc below) —
// still not fully safe pre-ctor, but no worse than before this fix existed.
static int ctorDone = 0;
static char preCtorBump[65536];
static size_t preCtorBumpUsed = 0;

static void *bumpAlloc(size_t size) {
	size_t aligned = (size + 15) & ~(size_t)15;
	if (preCtorBumpUsed + aligned > sizeof(preCtorBump)) {
		return NULL;
	}
	void *p = preCtorBump + preCtorBumpUsed;
	preCtorBumpUsed += aligned;
	return p;
}

static int isBumpPtr(void *ptr) {
	return ptr != NULL && (char *)ptr >= preCtorBump && (char *)ptr < preCtorBump + sizeof(preCtorBump);
}

// This can't be done only from __kml_gc_ctor: a dynamically-linked
// dependency (observed with libp11-kit, pulled in transitively via
// libcurl -> GnuTLS) can run its own constructor first and call plain
// malloc() from it — ELF runs a shared object's constructors before the
// executable that depends on it, regardless of any __attribute__
// ((constructor(N))) priority put on ours, since priority only orders
// constructors *within* one object. That first malloc() auto-inits Boehm
// GC before our constructor ever gets a chance to run, so the stackbottom
// fix has to happen lazily, on demand, from malloc() itself too — though
// as of ADR-00100's bump allocator above, the only malloc() calls that
// still reach this function at all are ones happening *after*
// __kml_gc_ctor, since every earlier call is served by bumpAlloc instead.
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
	// TDD-00025/http.listen({workers: N}) forks worker processes that keep
	// allocating normally after fork() (unlike execFileSync's child, which
	// only ever execvp()s/_exit()s and never touches the collector) — a
	// forked child can otherwise inherit a GC lock stuck mid-acquisition
	// (held by whatever internal Boehm state existed at the instant of
	// fork()) and deadlock or corrupt memory on its first allocation.
	// GC_set_handle_fork(1), called before GC_INIT() per gc.h's own
	// documented contract, makes GC_init() register Boehm's own internal
	// pthread_atfork handlers itself — real pthread_atfork() handlers fire
	// around every fork() in the process automatically, so this needs no
	// per-call-site instrumentation at the __kml_http_cluster_fork call
	// site itself. (gc.h separately documents a manual alternative —
	// GC_set_handle_fork(-1) plus explicit GC_atfork_prepare/parent/child
	// calls around each fork() — for platforms where automatic pthread_atfork
	// registration might be unavailable; not needed here, since both of this
	// project's target platforms, Linux glibc and Darwin/BSD libc, ship
	// standard POSIX pthread_atfork.)
	GC_set_handle_fork(1);
	GC_INIT();
	ctorDone = 1;
}

void *malloc(size_t size) {
	if (!ctorDone) {
		void *p = bumpAlloc(size);
		if (p != NULL) {
			return p;
		}
	}
	ensureGCStackbottomSet();
	return GC_malloc(size);
}

void *calloc(size_t nmemb, size_t size) {
	if (!ctorDone) {
		void *p = bumpAlloc(nmemb * size);
		if (p != NULL) {
			return p;
		}
	}
	ensureGCStackbottomSet();
	// GC_malloc always zeroes its returned memory (unlike libc's malloc),
	// so a single call of the combined size already satisfies calloc's
	// contract — Boehm's public API has no separate GC_calloc.
	return GC_malloc(nmemb * size);
}

void *realloc(void *ptr, size_t size) {
	if (!ctorDone && (ptr == NULL || isBumpPtr(ptr))) {
		void *p = bumpAlloc(size);
		if (p != NULL) {
			if (ptr != NULL) {
				memcpy(p, ptr, size);
			}
			return p;
		}
	}
	ensureGCStackbottomSet();
	return GC_realloc(ptr, size);
}

void free(void *ptr) {
	if (isBumpPtr(ptr)) {
		return; // no-op: bump-allocated memory is never individually freed
	}
	// Documented by Boehm as always safe to call explicitly, as a latency
	// optimization (early-release a block known dead now rather than
	// waiting for the next collection cycle) — this is what makes
	// Memory.free(x) automatically become "release to the collector early"
	// in gc mode, with zero changes needed in emit_memory.go.
	GC_free(ptr);
}
