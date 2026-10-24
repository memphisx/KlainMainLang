/* kml_heap.h — the managed-heap interface (TDD-00230 P3.3a). A C runtime
 * file allocates, reallocates and frees every block that becomes a program
 * value (a string, an array's data, a box, a cell) through these, and stores
 * a managed pointer into one through KML_STORE_PTR, so a collector's
 * allocation fast path and write barrier land here, as codegen's do in
 * heap_prims.go. A block's kind says what a scan finds in it: RAW holds no
 * pointers (string bytes, typed array elements), WORDS may hold any
 * (boxed values, pointers). Scratch memory the runtime keeps to itself
 * stays on libc, marked `scratch` where it is allocated. Prepended to every
 * runtime member (embedded_c.go). */
#ifndef KML_HEAP_H
#define KML_HEAP_H
#ifdef __cplusplus
extern "C" {
#endif
/* No system header: this text precedes each member's own feature macros
 * (`_GNU_SOURCE`), which a header included first would lock out. */

enum { KML_HEAP_RAW = 0, KML_HEAP_WORDS = 1 };

#ifdef __cplusplus
}
#include <stdlib.h> /* C++ defines _GNU_SOURCE itself; glibc's C++ declarations carry throw() */
extern "C" {
#else
void *malloc(__SIZE_TYPE__);
void *calloc(__SIZE_TYPE__, __SIZE_TYPE__);
void *realloc(void *, __SIZE_TYPE__);
void free(void *);
#endif

static inline void *kml_alloc(int kind, __SIZE_TYPE__ n) { (void)kind; return malloc(n); }
static inline void *kml_calloc(int kind, __SIZE_TYPE__ count, __SIZE_TYPE__ n) { (void)kind; return calloc(count, n); }
static inline void *kml_realloc(int kind, void *p, __SIZE_TYPE__ n) { (void)kind; return realloc(p, n); }
static inline void kml_free(int kind, void *p) { (void)kind; free(p); }

/* The GC interface (TDD-00230 phase 4): the collector as the runtime sees
 * it. gcshim.c implements it on Boehm under -mm=gc, and only gc-mode code
 * calls it; no runtime member names the collector itself. */
void *kml_gc_alloc(__SIZE_TYPE__ n);               /* collected, scanned */
void *kml_gc_alloc_pinned(__SIZE_TYPE__ n);        /* never collected, scanned */
void *kml_gc_alloc_atomic(__SIZE_TYPE__ n);        /* collected, not scanned */
void *kml_gc_alloc_atomic_pinned(__SIZE_TYPE__ n); /* never collected, not scanned */
void kml_gc_free(void *p);
void *kml_gc_base(void *p); /* the managed block holding p, or NULL */
int kml_gc_weak_link(void **slot, const void *obj); /* *slot cleared when obj dies */
void *kml_gc_finalizer(void *obj, void (*fn)(void *, void *), void *data); /* the previous data */
int kml_gc_run_finalizers(void);
void kml_gc_collect(void);
void kml_gc_add_roots(void *lo, void *hi);
void kml_gc_remove_roots(void *lo, void *hi);
__SIZE_TYPE__ kml_gc_heap_size(void);
__SIZE_TYPE__ kml_gc_free_bytes(void);
void kml_gc_allow_threads(void);
int kml_gc_thread_register(void); /* before a new thread's first allocation */
void kml_gc_thread_unregister(void);
void kml_gc_lock(void);
void kml_gc_unlock(void);
int kml_gc_suspend_signal(void);
int kml_gc_restart_signal(void);
void kml_gc_set_stack_bottom(void *mem); /* a fiber switch */
void kml_gc_set_stack_bottom_locked(void *mem); /* kml_gc_lock held */
void *kml_gc_stack_bottom(void);
void *kml_gc_thread_base(void); /* this thread's own stack base */

/* A string's header word, the 8 bytes before its first byte: the byte
 * length in bits 0..31, and the UTF-16 length plus one in bits 33..62 once
 * counted (0: not yet; strheader.c). Bit 32 stays clear, so the word never
 * reads as an object header's magic. A producer writes the bare byte length;
 * a reader masks. A read-only string (a C literal with a header) carries its
 * count from the start: KML_STR_ASCII_HDR for ASCII text. */
#define KML_STR_LEN_MASK 0xFFFFFFFFLL
#define KML_STR_LEN(s) (*(const long long *)((const char *)(s) - 8) & KML_STR_LEN_MASK)
#define KML_STR_ASCII_HDR(n) ((long long)(n) | (((long long)(n) + 1) << 33))
long long __kml_str_u16len_slow(const char *s);
/* The string's length in UTF-16 code units: JavaScript's `length`. */
static inline long long kml_str_u16len(const char *s) {
    unsigned long long c = (unsigned long long)*(const long long *)(s - 8) >> 33;
    return c ? (long long)c - 1 : __kml_str_u16len_slow(s);
}
/* Whether every byte of the string is ASCII, so byte offsets are its
 * UTF-16 indices. */
static inline int kml_str_ascii(const char *s) { return kml_str_u16len(s) == KML_STR_LEN(s); }

/* A managed pointer (or a box that may hold one) stored into a managed
 * block. */
#define KML_STORE_PTR(slot, v) (*(slot) = (v))

/* A bulk copy of n bytes into a managed block of the given kind (array
 * growth, slices, splices): the barrier for every slot copied, once. */
static inline void *kml_copy(int kind, void *dst, const void *src, __SIZE_TYPE__ n) { (void)kind; return __builtin_memmove(dst, src, n); }

/* The shadow call stack's top and the running fiber's base frame
 * (framesrc/frames.c, TDD-00188). A context switch keeps each side's own:
 * the switcher's are restored when it is resumed, and a fiber resumed after
 * its creator moved on is cut from the creator's frames, which may be gone. */
#ifdef __cplusplus
extern thread_local void *__kml_frame_top;
extern thread_local void *__kml_frame_base;
#else
extern _Thread_local void *__kml_frame_top;
extern _Thread_local void *__kml_frame_base;
#endif
/* The restore after a switch, out of line: a klain:sync goroutine may resume
 * on another thread, where an address of the thread-locals the optimizer
 * kept from before the switch names the previous thread's. */
#ifdef __cplusplus
extern "C"
#endif
void __kml_frame_resume(void *top, void *base);
#define KML_SWAPCONTEXT(from, to)                                   \
    do {                                                            \
        void *kml_ft_ = __kml_frame_top, *kml_fb_ = __kml_frame_base; \
        if (kml_fb_) ((void **)kml_fb_)[2] = kml_ft_;               \
        swapcontext((from), (to));                                  \
        __kml_frame_resume(kml_ft_, kml_fb_);                       \
    } while (0)
/* A fiber's entry: its base frame, a record with no descriptor whose
 * previous frame is its creator's top while the creator waits, and whose
 * last word is the fiber's top while it is switched out. */
#define KML_FIBER_ENTRY(base)                                       \
    void *base[3] = {__kml_frame_top, 0, 0};                        \
    __kml_frame_top = base;                                         \
    __kml_frame_base = base

#ifdef __cplusplus
}
#endif
#endif
