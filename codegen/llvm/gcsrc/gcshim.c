// gcshim.c — GC-mode allocator shim. Only compiled in and linked when
// -mm=gc is set (see main.go). Defines malloc/calloc/realloc/free so that
// this executable's own already-undefined @malloc/@calloc/@realloc/@free
// references — emitted, unchanged, by every existing runtime.go/emit_*.go
// call site — resolve to the Boehm-Demers-Weiser conservative collector
// instead of libc's allocator. No changes needed anywhere else in the
// compiler: this is the only definition of these symbols in the link, so
// no special linker flags (-flat_namespace, --defsym, LD_PRELOAD) are
// needed on either Linux or macOS. See docs/adr/00/ADR-00071.md.
#if defined(__linux__) && !defined(_GNU_SOURCE)
#define _GNU_SOURCE // dl_iterate_phdr's dlpi_tls_data
#endif
#include <gc.h>
#include <stddef.h>
#include <string.h>
#ifdef _WIN32
#include <windows.h> // NtCurrentTeb, for this thread's TLS block
#endif
#if defined(__linux__)
#include <link.h> // dl_iterate_phdr, for this thread's TLS blocks
#endif
#if defined(__APPLE__)
#include <mach-o/dyld.h>   // the main image's header, for its TLS block
#include <mach-o/getsect.h>
#include <pthread.h>
#include <stdint.h>
#endif
#ifndef _WIN32
#include <sys/mman.h> // mmap/munmap, for coroutine stacks
#include <unistd.h>   // getpagesize, for their guard pages
#endif
#if defined(__GLIBC__)
#include <pthread.h> // the foreign-block set's lock
#include <stdint.h>
// Declared by gc.h only under GC_THREADS; every libgc this project links is
// the threaded build, which exports it.
GC_API int GC_CALL GC_thread_is_registered(void);
#endif

// Coroutine (task) stacks live outside the collected heap. A GC_malloc'd
// stack block was zeroed at allocation (8 MiB resident for the module task,
// ADR-01050) and then scanned as one enormous heap object on every
// collection, in addition to being scanned as the running stack. Here the
// block is a bare anonymous mapping — pages exist only once touched — and on
// POSIX, where a parked task's frames really do live in it, the whole range
// is a root for the block's lifetime (the same shape klainsync.c uses for
// goroutine stacks). Registering the live [sp, high) slice per park would
// scan less; the whole-range root is already strictly cheaper than before.
// On Windows a context is a Win32 fiber on an OS stack — win32io.c registers
// the parked fiber stack itself (park_lo/park_hi) — so the block is only the
// size hint makecontext reads, never a root.
void *__kml_task_stack_alloc(size_t n) {
#ifdef _WIN32
	return VirtualAlloc(NULL, n, MEM_RESERVE | MEM_COMMIT, PAGE_READWRITE);
#else
	// A guard page under the stack, so an overflow throws (TDD-00249); it
	// sits below the range returned, as macOS makecontext faults on a stack
	// range holding one.
	size_t g = (size_t)getpagesize();
	char *p = mmap(NULL, n + g, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	if (p == MAP_FAILED) return NULL;
	mprotect(p, g, PROT_NONE);
	GC_add_roots(p + g, p + g + n);
	return p + g;
#endif
}
void __kml_task_stack_free(void *p, size_t n) {
	if (!p) return;
#ifdef _WIN32
	(void)n;
	VirtualFree(p, 0, MEM_RELEASE);
#else
	GC_remove_roots((char *)p, (char *)p + n);
	munmap((char *)p - getpagesize(), n + (size_t)getpagesize());
#endif
}

#ifndef _WIN32
#include <gc/gc_mark.h>
#include <pthread.h>
// A generator's stack (TDD-00249): an anonymous mapping with a guard page
// under it, like a task's, but not a root: its frames point back at the
// generator, so a root would keep every generator alive. Its owner is a
// collected cell of a kind of its own whose mark procedure scans the stack,
// so the frames are traced exactly while the generator is reachable, and a
// finalizer returns the stack once it is not. A parked body's owner records
// where its frames end (sp), and only [sp, top) is scanned; any other time
// (a body suspended inside another coroutine's resume) the whole stack is.
// Also owns the generator's jmpbuf stack (jmpstack.c), freed with it.
typedef struct { char *lo; size_t n; char *sp; void *jmp; void (*jmp_free)(void *); } KmlGenStack;
static int gs_kind = -1;
static void **gs_free_list;

// Spent stacks, kept mapped for the next generator: a generator is often
// short-lived, and an mmap, mprotect and munmap per instance cost more than
// the generator itself.
#define GS_POOL 64
static char *gs_pool[GS_POOL];
static size_t gs_pool_n, gs_pool_len;
static pthread_mutex_t gs_pool_mu = PTHREAD_MUTEX_INITIALIZER;
// The collector paces itself by its own heap, which the stacks are not in:
// once the live stacks double since the last collection, collect.
static size_t gs_live, gs_trigger = 64;

// Slack under the recorded frame for swapcontext's own frame.
#define GS_SLACK 1024

static struct GC_ms_entry *gs_mark(GC_word *addr, struct GC_ms_entry *top,
                                   struct GC_ms_entry *lim, GC_word env) {
	(void)env;
	KmlGenStack *s = (KmlGenStack *)addr;
	if (!s->lo) return top;
	char *from = s->lo;
	if (s->sp && s->sp - GS_SLACK > s->lo) from = s->sp - GS_SLACK;
	if (from > s->lo + s->n) from = s->lo + s->n;
	from = (char *)((uintptr_t)from & ~(uintptr_t)(sizeof(void *) - 1));
	for (void **w = (void **)from; w < (void **)(s->lo + s->n); w++)
		top = GC_MARK_AND_PUSH(*w, top, lim, w);
	return top;
}

static void gs_release(KmlGenStack *s);

static void gs_finalize(void *obj, void *cd) {
	(void)cd;
	gs_release(obj);
}

// A completed generator's stack, back to the pool at once (its body never
// runs again); the owner stays, empty, until the generator is collected.
void __kml_gen_stack_release(void *owner) { gs_release(owner); }

static void gs_release(KmlGenStack *s) {
	if (s->jmp) s->jmp_free(s->jmp);
	s->jmp = NULL;
	if (!s->lo) return;
	char *lo = s->lo;
	s->lo = NULL;
	pthread_mutex_lock(&gs_pool_mu);
	gs_live--;
	if (gs_pool_len == s->n && gs_pool_n < GS_POOL) {
		gs_pool[gs_pool_n++] = lo;
		lo = NULL;
	}
	pthread_mutex_unlock(&gs_pool_mu);
	if (lo) munmap(lo - getpagesize(), s->n + (size_t)getpagesize());
}

// Returns the owner cell; its first word is the stack's low end.
void *__kml_gen_stack_alloc(size_t n) {
	char *lo = NULL;
	pthread_mutex_lock(&gs_pool_mu);
	if (gs_kind < 0) {
		gs_free_list = GC_new_free_list();
		gs_kind = (int)GC_new_kind(gs_free_list, GC_MAKE_PROC(GC_new_proc(gs_mark), 0), 0, 1);
	}
	if (gs_pool_len == 0) gs_pool_len = n;
	int collect = ++gs_live >= gs_trigger;
	pthread_mutex_unlock(&gs_pool_mu);
	if (collect) {
		GC_gcollect();
		GC_invoke_finalizers();
		pthread_mutex_lock(&gs_pool_mu);
		gs_trigger = gs_live * 2 + 64;
		pthread_mutex_unlock(&gs_pool_mu);
	}
	pthread_mutex_lock(&gs_pool_mu);
	if (gs_pool_n > 0 && gs_pool_len == n) lo = gs_pool[--gs_pool_n];
	pthread_mutex_unlock(&gs_pool_mu);
	if (!lo) {
		size_t g = (size_t)getpagesize();
		char *p = mmap(NULL, n + g, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
		if (p == MAP_FAILED) return NULL;
		mprotect(p, g, PROT_NONE);
		lo = p + g;
	}
	KmlGenStack *s = GC_generic_malloc(sizeof *s, gs_kind);
	if (!s) {
		pthread_mutex_lock(&gs_pool_mu);
		gs_live--;
		pthread_mutex_unlock(&gs_pool_mu);
		munmap(lo - getpagesize(), n + (size_t)getpagesize());
		return NULL;
	}
	s->lo = lo;
	s->n = n;
	s->sp = lo + n + GS_SLACK; // not started: no frames yet
	s->jmp = NULL;
	// No order: the stack's frames point back at the owner's generator.
	GC_register_finalizer_no_order(s, gs_finalize, NULL, NULL, NULL);
	return s;
}

// The body is about to swap out: its frames end at the caller's frame.
__attribute__((noinline)) void __kml_gen_stack_park(void *owner) {
	((KmlGenStack *)owner)->sp = __builtin_frame_address(0);
}

void __kml_gen_stack_unpark(void *owner) { ((KmlGenStack *)owner)->sp = NULL; }

void __kml_gen_stack_own(void *owner, void *jmp, void (*jmp_free)(void *)) {
	KmlGenStack *s = owner;
	s->jmp = jmp;
	s->jmp_free = jmp_free;
}
#endif

#if defined(__linux__)
// On at least one Boehm GC build (Ubuntu's libgc-dev package, confirmed via
// a minimal reproduction outside this compiler entirely — see
// docs/adr/00/ADR-00080.md), letting GC_INIT() auto-detect the main thread's
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
// malloc() — routing to *this* shim's malloc() — so early that Boehm's /* scratch */
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
// malloc() from it — ELF runs a shared object's constructors before the /* scratch */
// executable that depends on it, regardless of any __attribute__
// ((constructor(N))) priority put on ours, since priority only orders
// constructors *within* one object. That first malloc() auto-inits Boehm /* scratch */
// GC before our constructor ever gets a chance to run, so the stackbottom
// fix has to happen lazily, on demand, from malloc() itself too — though /* scratch */
// as of ADR-00100's bump allocator above, the only malloc() calls that /* scratch */
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

#ifdef _WIN32
// win32io.c's fiber hooks, NULL until the constructor below fills them in.
extern void (*__kml_gc_set_stackbottom)(void *stack_base);
extern void (*__kml_gc_root_add)(void *lo, void *hi_plus_one);
extern void (*__kml_gc_root_remove)(void *lo, void *hi_plus_one);
// GC_stackbottom carries a deprecation attribute in newer Boehm headers; it is
// still the documented way to say "the stack now in use", and the emitted IR
// writes the same variable for its own fiber swaps (emitter.go's gcSBStore).
_Pragma("GCC diagnostic push")
_Pragma("GCC diagnostic ignored \"-Wdeprecated-declarations\"")
// The Win32 collector is the threaded build: it scans each registered thread
// from its SP to the stack base recorded for *that thread*, and never reads
// GC_stackbottom. With the SP on a fiber's stack and the recorded base still
// the thread's original one, it finds the SP out of range and scans the
// original stack instead — the running fiber's frames are not roots at all,
// and whatever only they reference is collected under them. The per-thread
// record is what has to move (under the allocation lock, as gc.h requires);
// the global is kept in step for anything that still reads it.
static void *win_set_sb_locked(void *base) {
	struct GC_stack_base sb;
	memset(&sb, 0, sizeof sb);
	sb.mem_base = base;
	GC_set_stackbottom(NULL, &sb);
	GC_stackbottom = (char *)base;
	return NULL;
}
static void win_set_stackbottom(void *base) { GC_call_with_alloc_lock(win_set_sb_locked, base); }
_Pragma("GCC diagnostic pop")
// The IR's every GC stack-bottom write on Windows (gcSBStore): its own idea of
// a context's stack is the unused malloc'd block, so it asks for "the stack
// now running" instead, which the TEB knows.
static void win_sb_cur(void) { win_set_stackbottom(((NT_TIB *)NtCurrentTeb())->StackBase); }
static void win_root_add(void *lo, void *hi) { GC_add_roots((char *)lo, (char *)hi); }
static void win_root_remove(void *lo, void *hi) { GC_remove_roots((char *)lo, (char *)hi); }

// Thread-local storage is not a root on Windows. Boehm scans the image's data
// sections and each registered thread's stack; a thread's TLS block is a
// separate loader allocation it never looks at. The runtime keeps its loop
// state in thread_local globals — the task array, the microtask queue, the
// timer list — so under -mm=gc those were collected while in use and handed
// out again, surfacing as a crash inside a later allocation. Each thread that
// runs emitted code registers its own block (the mingw CRT's TLS template
// bounds give the size; the TEB's slot array gives this thread's copy) and
// drops it before the thread — and with it the block — goes away.
extern char _tls_start, _tls_end;
extern unsigned long _tls_index;
static void kml_tls_bounds(char **lo, char **hi) {
	char **slots = (char **)((void **)NtCurrentTeb())[11]; // TEB.ThreadLocalStoragePointer (+0x58)
	*lo = slots ? slots[_tls_index] : NULL;
	*hi = *lo ? *lo + (&_tls_end - &_tls_start) : NULL;
}
void __kml_gc_tls_register(void) {
	char *lo, *hi;
	kml_tls_bounds(&lo, &hi);
	if (lo && lo < hi) GC_add_roots(lo, hi);
}
void __kml_gc_tls_unregister(void) {
	char *lo, *hi;
	kml_tls_bounds(&lo, &hi);
	if (lo && lo < hi) GC_remove_roots(lo, hi);
}
#endif

#if defined(__linux__)
// The same hole exists on Linux: neither the main thread's static TLS block nor
// a registered pthread's is part of what Boehm scans (checked directly — a
// block held only by a _Thread_local pointer is collected and handed out
// again, on the main thread and on a registered worker alike). Every loaded
// module's PT_TLS segment has a per-thread copy; dl_iterate_phdr reports the
// calling thread's.
static int kml_tls_phdr_cb(struct dl_phdr_info *info, size_t size, void *data) {
	(void)size;
	if (!info->dlpi_tls_data) return 0; // no TLS, or not yet allocated for this thread
	for (int i = 0; i < info->dlpi_phnum; i++) {
		if (info->dlpi_phdr[i].p_type != PT_TLS || info->dlpi_phdr[i].p_memsz == 0) continue;
		char *lo = (char *)info->dlpi_tls_data, *hi = lo + info->dlpi_phdr[i].p_memsz;
		if (*(int *)data) GC_add_roots(lo, hi); else GC_remove_roots(lo, hi);
	}
	return 0;
}
void __kml_gc_tls_register(void) { int add = 1; dl_iterate_phdr(kml_tls_phdr_cb, &add); }
void __kml_gc_tls_unregister(void) { int add = 0; dl_iterate_phdr(kml_tls_phdr_cb, &add); }
#elif defined(__APPLE__)
// macOS has the same hole: dyld allocates each thread's copy of the image's
// thread-local sections lazily, as one block under the image's pthread key,
// and Boehm never scans it. The key is in the image's TLV descriptors
// (__thread_vars: thunk, key, offset — the low 32 bits; the rest of the
// word is not part of it on current dyld); the block spans __thread_data
// and __thread_bss. Touching a variable of our own makes dyld allocate this
// thread's block first, and its address checks the block found.
struct kml_tlv_desc { void *thunk; unsigned long key; unsigned long offset; };
static __thread char kml_tls_anchor;
static void kml_tls_bounds(char **lo, char **hi) {
	*lo = *hi = NULL;
	volatile char *anchor = &kml_tls_anchor;
	(void)*anchor;
	const struct mach_header_64 *mh = (const struct mach_header_64 *)_dyld_get_image_header(0);
	unsigned long nv = 0, nd = 0, nb = 0;
	struct kml_tlv_desc *v = (struct kml_tlv_desc *)getsectiondata(mh, "__DATA", "__thread_vars", &nv);
	char *d = (char *)getsectiondata(mh, "__DATA", "__thread_data", &nd);
	char *b = (char *)getsectiondata(mh, "__DATA", "__thread_bss", &nb);
	if (!v || nv < sizeof *v) return;
	char *start = d && nd ? d : b, *end = b && nb ? b + nb : d + nd;
	if (!start || end <= start) return;
	char *blk = (char *)pthread_getspecific((pthread_key_t)(v->key & 0xffffffffUL));
	if (!blk || (char *)&kml_tls_anchor < blk || (char *)&kml_tls_anchor >= blk + (end - start)) return;
	*lo = blk;
	*hi = blk + (end - start);
}
void __kml_gc_tls_register(void) {
	char *lo, *hi;
	kml_tls_bounds(&lo, &hi);
	if (lo && lo < hi) GC_add_roots(lo, hi);
}
void __kml_gc_tls_unregister(void) {
	char *lo, *hi;
	kml_tls_bounds(&lo, &hi);
	if (lo && lo < hi) GC_remove_roots(lo, hi);
}
#endif

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
#ifdef _WIN32
	// Windows fibers: hand win32io.c the three collector operations its fiber
	// switch needs (see "fibers and the collector" there). Done here because
	// this file is the only one in the link that knows Boehm exists.
	__kml_gc_set_stackbottom = win_set_stackbottom;
	__kml_gc_root_add = win_root_add;
	__kml_gc_root_remove = win_root_remove;
#endif
#ifndef _WIN32
	GC_set_handle_fork(1);
#endif // no fork() on Windows: Boehm aborts on the request there (TDD-00177)
	GC_INIT();
	__kml_gc_tls_register(); // the main thread's block
	ctorDone = 1;
}

// A thread's own stack while it runs a task. The collector scans the stack
// in use (task.c points GC_stackbottom, or the thread's record, at the
// task's), so the frames below the swap — the spawner's, holding whatever it
// passed on and reads after — were no roots while the task allocated: a
// klain:http exchange that only those frames referenced was collected and
// handed out again. task.c reports leaving and coming back; these records,
// one per thread (uncollectable, never freed), are pushed at every
// collection.
typedef struct kml_ostack {
	char *sp, *base; // the stack left, from its last frame to its bottom
	struct kml_ostack *next;
} kml_ostack;
static kml_ostack *kml_ostacks;
static _Thread_local kml_ostack *kml_my_ostack;
// gc_mark.h's, declared here as GC_thread_is_registered is: its install
// path differs per distribution.
typedef void(GC_CALLBACK *kml_push_proc)(void);
GC_API void GC_CALL GC_set_push_other_roots(kml_push_proc);
GC_API kml_push_proc GC_CALL GC_get_push_other_roots(void);
GC_API void GC_CALL GC_push_all(void *, void *);
static kml_push_proc kml_prev_push;
static void GC_CALLBACK kml_push_ostacks(void) {
	if (kml_prev_push) kml_prev_push();
	for (kml_ostack *o = kml_ostacks; o; o = o->next)
		if (o->sp && o->base > o->sp) GC_push_all(o->sp, o->base);
}
static void *kml_ostack_link(void *p) {
	kml_ostack *o = (kml_ostack *)p;
	if (!kml_ostacks && !kml_prev_push) {
		kml_prev_push = GC_get_push_other_roots();
		GC_set_push_other_roots(kml_push_ostacks);
	}
	o->next = kml_ostacks;
	kml_ostacks = o;
	return NULL;
}
static void *kml_ostack_set(void *p) {
	kml_ostack *o = kml_my_ostack;
	o->sp = (char *)p;
	return NULL;
}
void __kml_gc_stack_leave(void *sp, void *base) {
	if (!kml_my_ostack) {
		kml_my_ostack = (kml_ostack *)GC_malloc_uncollectable(sizeof *kml_my_ostack);
		GC_call_with_alloc_lock(kml_ostack_link, kml_my_ostack);
	}
	kml_my_ostack->base = (char *)base;
	GC_call_with_alloc_lock(kml_ostack_set, sp);
}
void __kml_gc_stack_back(void) {
	if (kml_my_ostack) GC_call_with_alloc_lock(kml_ostack_set, NULL);
}

#if defined(__GLIBC__)
// Threads the collector does not know. The shim replaces malloc for the whole
// process, so a library's own threads allocate here too — libcurl's threaded
// DNS resolver runs getaddrinfo on one. GC_malloc from such a thread can start
// a collection, and Boehm aborts one begun on an unregistered thread
// ("Collecting from unknown thread"); even without that, a block it handed out
// would be collectable while only that thread — whose stack is no root —
// referenced it. Those threads get the C library's own allocator instead, and
// their blocks are tracked so free()/realloc() on any thread can tell them /* scratch */
// from GC objects and from other foreign pointers (see free() below). /* scratch */
extern void *__libc_malloc(size_t);
extern void *__libc_calloc(size_t, size_t);
extern void *__libc_realloc(void *, size_t);
extern void __libc_free(void *);
static __thread int kmlGCThreadKnown; // a cached "registered with the collector"
static int kml_foreign_thread(void) {
	if (kmlGCThreadKnown) return 0;
	if (GC_thread_is_registered()) {
		kmlGCThreadKnown = 1;
		return 0;
	}
	return 1; // asked again next time: the thread may register later
}
// The foreign-block set: open addressing, linear probing, tombstones.
static pthread_mutex_t kmlForeignMu = PTHREAD_MUTEX_INITIALIZER;
static void **kmlForeignTab;
static size_t kmlForeignCap, kmlForeignUsed; // used counts live + tombstones
static size_t kmlForeignLive; // read without the lock only as a "maybe any" hint
#define KML_TOMB ((void *)1)
static size_t kml_fhash(void *p, size_t cap) { return (((uintptr_t)p >> 4) * 0x9E3779B97F4A7C15ull) & (cap - 1); }
static void kml_fput(void *p) { // lock held; room guaranteed
	size_t i = kml_fhash(p, kmlForeignCap);
	while (kmlForeignTab[i] && kmlForeignTab[i] != KML_TOMB) i = (i + 1) & (kmlForeignCap - 1);
	if (!kmlForeignTab[i]) kmlForeignUsed++;
	kmlForeignTab[i] = p;
	kmlForeignLive++;
}
static void kml_foreign_add(void *p) {
	pthread_mutex_lock(&kmlForeignMu);
	if ((kmlForeignUsed + 1) * 2 > kmlForeignCap) {
		size_t oldCap = kmlForeignCap, ncap = oldCap ? oldCap : 64;
		while ((kmlForeignLive + 1) * 2 > ncap / 2) ncap *= 2;
		void **old = kmlForeignTab;
		kmlForeignTab = __libc_calloc(ncap, sizeof(void *));
		kmlForeignCap = ncap;
		kmlForeignUsed = kmlForeignLive = 0;
		for (size_t i = 0; i < oldCap; i++)
			if (old[i] && old[i] != KML_TOMB) kml_fput(old[i]);
		__libc_free(old);
	}
	kml_fput(p);
	pthread_mutex_unlock(&kmlForeignMu);
}
static int kml_foreign_remove(void *p) { // 1 when p was a foreign block
	if (!p || !__atomic_load_n(&kmlForeignLive, __ATOMIC_RELAXED)) return 0;
	int found = 0;
	pthread_mutex_lock(&kmlForeignMu);
	if (kmlForeignCap) {
		size_t i = kml_fhash(p, kmlForeignCap);
		while (kmlForeignTab[i]) {
			if (kmlForeignTab[i] == p) {
				kmlForeignTab[i] = KML_TOMB;
				kmlForeignLive--;
				found = 1;
				break;
			}
			i = (i + 1) & (kmlForeignCap - 1);
		}
	}
	pthread_mutex_unlock(&kmlForeignMu);
	return found;
}
#endif

void *malloc(size_t size) { /* scratch */
#if defined(__GLIBC__)
	if (ctorDone && kml_foreign_thread()) {
		void *p = __libc_malloc(size);
		if (p) kml_foreign_add(p);
		return p;
	}
#endif
	if (!ctorDone) {
		void *p = bumpAlloc(size);
		if (p != NULL) {
			return p;
		}
	}
	ensureGCStackbottomSet();
	return GC_malloc(size);
}

/* __kml_heap_usable for the collector's heap: a GC object's full size; a
 * bump-allocated or foreign block (libc's, a private arena's) reads as no
 * spare room, so its owner just reallocates. */
long long __kml_heap_usable(void *p) {
	if (p == NULL || isBumpPtr(p) || GC_base(p) != p) return 0;
	return (long long)GC_size(p);
}

void *calloc(size_t nmemb, size_t size) { /* scratch */
#if defined(__GLIBC__)
	if (ctorDone && kml_foreign_thread()) {
		void *p = __libc_calloc(nmemb, size);
		if (p) kml_foreign_add(p);
		return p;
	}
#endif
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

void *realloc(void *ptr, size_t size) { /* scratch */
#if defined(__GLIBC__)
	// A foreign block stays a libc block, whichever thread resizes it.
	if (kml_foreign_remove(ptr)) {
		void *q = __libc_realloc(ptr, size);
		kml_foreign_add(q ? q : ptr);
		return q;
	}
	if (ctorDone && kml_foreign_thread()) {
		void *q = __libc_malloc(size);
		if (!q) return NULL;
		if (ptr && GC_base(ptr)) { // a GC object resized off-collector: copy it out
			size_t old = GC_size(ptr);
			memcpy(q, ptr, old < size ? old : size);
		} else if (ptr) {
			__libc_free(q); // unknown provenance: keep the collector's behaviour
			return GC_realloc(ptr, size);
		}
		kml_foreign_add(q);
		return q;
	}
#endif
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

void free(void *ptr) { /* scratch */
	if (isBumpPtr(ptr)) {
		return; // no-op: bump-allocated memory is never individually freed
	}
#if defined(__GLIBC__)
	if (kml_foreign_remove(ptr)) { // handed out to a thread the collector does not know
		__libc_free(ptr);
		return;
	}
#endif
	// GC_free is only safe on a pointer Boehm actually owns. A foreign pointer
	// reaching this shim's free() — a library that allocated through a private /* scratch */
	// arena (e.g. OpenSSL's CRYPTO_secure_malloc) and frees it via libc free(), /* scratch */
	// notably in an atexit OPENSSL_cleanup handler — is NOT a GC object, and
	// GC_free would dereference GC block metadata for an out-of-heap address and
	// crash. This bit any -mm=gc program linking OpenSSL (fetch/tls, and the
	// worker runtime via libcurl) at process exit. GC_base returns the base of
	// the GC object containing ptr, or NULL if ptr isn't in the GC heap — so
	// guard on it and simply drop a foreign free (the process is collecting or
	// exiting anyway; nothing needs an explicit release).
	if (GC_base(ptr) == NULL) {
		return;
	}
	// Documented by Boehm as always safe to call explicitly, as a latency
	// optimization (early-release a block known dead now rather than
	// waiting for the next collection cycle) — this is what makes
	// Memory.free(x) automatically become "release to the collector early" /* scratch */
	// in gc mode, with zero changes needed in emit_memory.go.
	GC_free(ptr);
}

// The GC interface (TDD-00230 phase 4): codegen and every other runtime
// member reach the collector only through these kml_gc_* entry points
// (declared in kml_heap.h), never through Boehm's names, so another
// collector is another implementation of this list.

// gc.h declares the thread entry points only under GC_THREADS; every libgc
// this project links is the threaded build, which exports them.
GC_API void GC_CALL GC_allow_register_threads(void);
GC_API int GC_CALL GC_register_my_thread(const struct GC_stack_base *);
GC_API int GC_CALL GC_unregister_my_thread(void);
#ifndef _WIN32
GC_API int GC_CALL GC_get_suspend_signal(void);
GC_API int GC_CALL GC_get_thr_restart_signal(void);
#endif

// Never collected, still scanned: a block whose only reference can be
// invisible to the collector (another thread's queue, a shared buffer).
void *kml_gc_alloc_pinned(size_t n) { return GC_malloc_uncollectable(n); }
// Collected, never scanned: a block that holds no managed pointers.
void *kml_gc_alloc_atomic(size_t n) { return GC_malloc_atomic(n); }
void *kml_gc_alloc_atomic_pinned(size_t n) { return GC_malloc_atomic_uncollectable(n); }
void *kml_gc_alloc(size_t n) { return GC_malloc(n); }
void kml_gc_free(void *p) { GC_free(p); }
// The start of the managed block holding p, or NULL for foreign memory.
void *kml_gc_base(void *p) { return GC_base(p); }
// *slot is cleared when obj becomes unreachable (a weak reference).
int kml_gc_weak_link(void **slot, const void *obj) { return GC_general_register_disappearing_link(slot, obj); }
// fn(obj, data) runs once obj becomes unreachable; replaces and returns the
// data of the finalizer obj had (NULL for none).
void *kml_gc_finalizer(void *obj, void (*fn)(void *, void *), void *data) {
	GC_finalization_proc ofn;
	void *ocd = NULL;
	GC_register_finalizer(obj, fn, data, &ofn, &ocd);
	return ocd;
}
// Runs finalizers queued by earlier collections; the number run.
int kml_gc_run_finalizers(void) { return GC_invoke_finalizers(); }
void kml_gc_collect(void) { GC_gcollect(); }
void kml_gc_add_roots(void *lo, void *hi) { GC_add_roots(lo, hi); }
void kml_gc_remove_roots(void *lo, void *hi) { GC_remove_roots(lo, hi); }
size_t kml_gc_heap_size(void) { return GC_get_heap_size(); }
size_t kml_gc_free_bytes(void) { return GC_get_free_bytes(); }

// Threads: a thread allocating managed memory registers first.
// This thread's own stack base, a fiber switch's way back.
static _Thread_local void *gc_thread_base;
void kml_gc_allow_threads(void) { GC_allow_register_threads(); }
int kml_gc_thread_register(void) {
	struct GC_stack_base sb;
	if (GC_get_stack_base(&sb) != GC_SUCCESS) return -1;
	int r = GC_register_my_thread(&sb);
	// Only once registered: on macOS a thread's first thread-local access
	// allocates its TLS block through malloc, which is the collector's here.
	gc_thread_base = sb.mem_base;
	return r;
}
void kml_gc_thread_unregister(void) { GC_unregister_my_thread(); }
// Without GC_THREADS gc.h makes these no-op macros; the threaded libgc
// linked exports the real lock, which a goroutine switch must hold.
#undef GC_alloc_lock
#undef GC_alloc_unlock
GC_API void GC_CALL GC_alloc_lock(void);
GC_API void GC_CALL GC_alloc_unlock(void);
void kml_gc_lock(void) { GC_alloc_lock(); }
void kml_gc_unlock(void) { GC_alloc_unlock(); }
int kml_gc_suspend_signal(void) {
#ifdef _WIN32
	return -1;
#else
	return GC_get_suspend_signal();
#endif
}
int kml_gc_restart_signal(void) {
#ifdef _WIN32
	return -1;
#else
	return GC_get_thr_restart_signal();
#endif
}

// Fiber stacks: the stack bottom the collector scans from on this thread.
// A fiber switch repoints it at the fiber's own stack and restores it after.
// Single-threaded that is the process-wide GC_stackbottom; once the program
// starts Worker threads, this thread's own record, updated under the
// allocation lock. On Windows the stack running is the TEB's, whatever the
// caller passes (win32io.c runs contexts as Win32 fibers on OS stacks).
_Pragma("GCC diagnostic push")
_Pragma("GCC diagnostic ignored \"-Wdeprecated-declarations\"")
static int gc_threaded;
static void *gc_set_sb_locked(void *mem) {
	struct GC_stack_base sb = {0};
	sb.mem_base = mem;
	GC_set_stackbottom(NULL, &sb);
	return NULL;
}
void kml_gc_set_stack_bottom(void *mem) {
#ifdef _WIN32
	(void)mem;
	win_sb_cur();
#else
	if (gc_threaded) GC_call_with_alloc_lock(gc_set_sb_locked, mem);
	else GC_stackbottom = (char *)mem;
#endif
}
// The same with kml_gc_lock already held, for a switch that keeps the lock
// across the context swap (klainsync.c).
void kml_gc_set_stack_bottom_locked(void *mem) { gc_set_sb_locked(mem); }
void *kml_gc_stack_bottom(void) {
#ifndef _WIN32
	if (gc_threaded) {
		struct GC_stack_base sb = {0};
		GC_get_my_stackbottom(&sb);
		return sb.mem_base;
	}
#endif
	return GC_stackbottom;
}
// main()'s first step: record the main thread's stack base, and whether the
// program starts threads.
void kml_gc_main_started(int threads) {
	gc_thread_base = GC_stackbottom;
	if (threads) {
		gc_threaded = 1;
		GC_allow_register_threads();
	}
}
void *kml_gc_thread_base(void) { return gc_thread_base; }
_Pragma("GCC diagnostic pop")
