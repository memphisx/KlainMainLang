/* weak.c — WeakMap/WeakSet/WeakRef runtime (TDD-00112; in C per TDD-00240).
 * A weak collection is a one-word head box pointing at a singly linked list
 * of cells { next, link, val } (24 bytes); `link` points at a one-word link
 * cell holding the referent. Under -mm=gc (KML_GC, defined by the compiler)
 * the link cell is GC_malloc_atomic'd (unscanned) and registered as a
 * disappearing link, so Boehm nulls it when the referent is collected; under
 * manual it is plain malloc and never cleared. A list (not an array) keeps
 * every link cell's address stable for the by-address registration. */
#include <stdlib.h>
#include <stdint.h>

typedef long long i64;

typedef struct kml_weak_cell {
    struct kml_weak_cell *next;
    void **link;
    i64 val;
} kml_weak_cell;

#ifdef KML_GC
extern void *GC_malloc_atomic(size_t n);
extern int GC_general_register_disappearing_link(void **link, const void *obj);
static void **link_new(void *obj) {
    void **w = (void **)GC_malloc_atomic(sizeof(void *));
    *w = obj;
    GC_general_register_disappearing_link(w, obj);
    return w;
}
#else
static void **link_new(void *obj) {
    void **w = (void **)malloc(sizeof(void *));
    *w = obj;
    return w;
}
#endif

void *__kml_weak_create(void) {
    kml_weak_cell **h = (kml_weak_cell **)malloc(8);
    *h = NULL;
    return h;
}

/* First cell whose live referent == key, else NULL; a collected referent
 * reads NULL and never matches a non-null key. */
static kml_weak_cell *weak_find(kml_weak_cell **h, void *key) {
    for (kml_weak_cell *c = *h; c; c = c->next)
        if (*c->link == key) return c;
    return NULL;
}

void __kml_weak_set(void *h, void *key, i64 val) {
    kml_weak_cell *c = weak_find((kml_weak_cell **)h, key);
    if (c) {
        c->val = val;
        return;
    }
    void **w = link_new(key);
    c = (kml_weak_cell *)malloc(sizeof *c);
    c->next = *(kml_weak_cell **)h;
    c->link = w;
    c->val = val;
    *(kml_weak_cell **)h = c;
}

i64 __kml_weak_get(void *h, void *key) {
    kml_weak_cell *c = weak_find((kml_weak_cell **)h, key);
    return c ? c->val : 0;
}

unsigned char __kml_weak_has(void *h, void *key) {
    return weak_find((kml_weak_cell **)h, key) != NULL;
}

/* Unlink the cell if present. Its link word's disappearing-link registration
 * is left as-is: the word becomes unreachable (Boehm tolerates a dangling
 * registration on collected memory). */
unsigned char __kml_weak_delete(void *h, void *key) {
    kml_weak_cell **prev = (kml_weak_cell **)h;
    for (kml_weak_cell *c = *prev; c; prev = &c->next, c = c->next) {
        if (*c->link == key) {
            *prev = c->next;
            return 1;
        }
    }
    return 0;
}

void *__kml_weakref_create(void *obj) { return link_new(obj); }

void *__kml_weakref_deref(void *box) { return *(void **)box; }

#ifdef KML_WEAK_NATIVES
/* undefined as a NaN-boxed word (emit_dynamic.go nbUndefined). */
#define KML_WEAK_NB_UNDEFINED 10

/* A WeakRef's target held in `any` (lib/node/kml_weakref.ts): the box word's
 * object pointer behind a weak link, with the box's low tag bits beside it.
 * The handle is the cell's address as a number. The cell is never freed: the
 * class holds the handle as a number, which no collector traces, so under
 * -mm=gc it is uncollectable but unscanned, and its pointer a disappearing
 * link. */
typedef struct { void *obj; i64 tag; } kml_weak_any;

#ifdef KML_GC
extern void *GC_malloc_atomic_uncollectable(size_t n);
extern void *GC_base(void *p);
#define KML_WEAK_ANY_ALLOC(n) GC_malloc_atomic_uncollectable(n)
#else
#define KML_WEAK_ANY_ALLOC(n) malloc(n)
#endif

/* The kept objects (AddToKeptObjects): a WeakRef's target stays alive until
 * the end of the job that made the WeakRef or derefed it, as the spec has it.
 * A plain (scanned) array, cleared at the microtask checkpoint. */
static i64 *kml_kept;
static long kml_nkept, kml_capkept;
extern void (*__kml_clear_kept_hook)(void);

/* The slots are zeroed, not just forgotten: the array is scanned. */
static void kml_clear_kept(void) {
    for (long i = 0; i < kml_nkept; i++) kml_kept[i] = 0;
    kml_nkept = 0;
}

static void kml_keep(i64 box) {
    if (kml_nkept == kml_capkept) {
        long cap = kml_capkept ? kml_capkept * 2 : 16;
        i64 *n = (i64 *)malloc((size_t)cap * sizeof(i64));
        for (long i = 0; i < kml_nkept; i++) n[i] = kml_kept[i];
        kml_kept = n;
        kml_capkept = cap;
    }
    kml_kept[kml_nkept++] = box;
    __kml_clear_kept_hook = kml_clear_kept;
}

double __kml_weak_link_any(i64 box) {
    kml_keep(box);
    kml_weak_any *c = (kml_weak_any *)KML_WEAK_ANY_ALLOC(sizeof *c);
    c->obj = (void *)(uintptr_t)(box & ~7LL);
    c->tag = box & 7;
#ifdef KML_GC
    /* The link is registered on the object holding the target: a box's
       payload may point inside its allocation (a dynamic object's). */
    void *base = GC_base(c->obj);
    GC_general_register_disappearing_link(&c->obj, base ? base : c->obj);
#endif
    return (double)(uintptr_t)c;
}

i64 __kml_weak_deref_any(double handle) {
    kml_weak_any *c = (kml_weak_any *)(uintptr_t)handle;
    if (!c || !c->obj) return KML_WEAK_NB_UNDEFINED;
    i64 box = (i64)(uintptr_t)c->obj | c->tag;
    kml_keep(box);
    return box;
}
#endif /* KML_WEAK_NATIVES */
