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
static void **link_new(void *obj) {
    void **w = (void **)kml_gc_alloc_atomic(sizeof(void *));
    *w = obj;
    /* Registered on the block holding obj: Boehm clears a link only for
     * its block's start, and a key may point inside its block. */
    void *base = kml_gc_base(obj);
    kml_gc_weak_link(w, base ? base : obj);
    return w;
}

/* Ephemerons. A program making a WeakMap compiles the collector with
 * its ephemeron slots (gcshim.c, KML_EPH): every scanned block gets a word near its end. A WeakMap
 * entry's value lives in a node chained from its key's slot, so the value
 * is reachable exactly while the key is; the map holds the key only
 * through a disappearing link. A node names its map by the map's handle
 * hidden (complemented), so a key never keeps a map alive. */
typedef struct kml_eph_node {
    struct kml_eph_node *next;
    uintptr_t map; /* ~handle */
    i64 val;
} kml_eph_node;

static kml_eph_node **eph_find(void **slot, void *h) {
    kml_eph_node **pp = (kml_eph_node **)slot;
    for (; *pp; pp = &(*pp)->next)
        if ((*pp)->map == ~(uintptr_t)h) return pp;
    return NULL;
}

/* Stores val as key's value in map h; 0 when key has no slot (it is no
 * collected object: static data, or a block that cannot hold one). */
static int eph_put(void *h, void *key, i64 val) {
    void **slot = kml_gc_eph_slot(key);
    if (!slot) return 0;
    kml_eph_node **pp = eph_find(slot, h);
    if (pp) {
        (*pp)->val = val;
        return 1;
    }
    kml_eph_node *n = (kml_eph_node *)kml_gc_alloc(sizeof *n);
    n->map = ~(uintptr_t)h;
    n->val = val;
    n->next = (kml_eph_node *)*slot;
    *slot = n;
    return 1;
}

static int eph_get(void *h, void *key, i64 *val) {
    void **slot = kml_gc_eph_slot(key);
    if (!slot) return 0;
    kml_eph_node **pp = eph_find(slot, h);
    *val = pp ? (*pp)->val : 0;
    return 1;
}

static void eph_drop(void *h, void *key) {
    void **slot = key ? kml_gc_eph_slot(key) : NULL;
    kml_eph_node **pp = slot ? eph_find(slot, h) : NULL;
    if (pp) *pp = (*pp)->next;
}

/* A collected map's entries leave the keys still alive. */
static void weak_map_died(void *base, void *data) {
    (void)data;
    kml_weak_cell **h = (kml_weak_cell **)((i64 *)base + 1);
    for (kml_weak_cell *c = *h; c; c = c->next) eph_drop(h, *c->link);
}
#else
static void **link_new(void *obj) {
    void **w = (void **)kml_alloc(KML_HEAP_RAW, sizeof(void *));
    *w = obj;
    return w;
}
#endif

/* A WeakMap's or WeakSet's handle, its list head, after its host header
 * word hdr. */
void *__kml_weak_create(i64 hdr) {
    i64 *base = (i64 *)kml_alloc(KML_HEAP_WORDS, 16);
    base[0] = hdr;
    kml_weak_cell **h = (kml_weak_cell **)(base + 1);
    *h = NULL;
#ifdef KML_GC
    kml_gc_finalizer(base, weak_map_died, NULL);
#endif
    return h;
}

/* First cell whose live referent == key, else NULL; a collected referent
 * reads NULL, and a null key (a lookup of a primitive) matches nothing. */
static kml_weak_cell *weak_find(kml_weak_cell **h, void *key) {
    if (!key) return NULL; /* a key that is no object: no entry */
    for (kml_weak_cell *c = *h; c; c = c->next)
        if (*c->link == key) return c;
    return NULL;
}

void __kml_weak_set(void *h, void *key, i64 val) {
    kml_weak_cell *c = weak_find((kml_weak_cell **)h, key);
    if (!c) {
        void **w = link_new(key);
        c = (kml_weak_cell *)kml_alloc(KML_HEAP_WORDS, sizeof *c);
        c->next = *(kml_weak_cell **)h;
        c->link = w;
        c->val = 0;
        *(kml_weak_cell **)h = c;
    }
#ifdef KML_GC
    if (eph_put(h, key, val)) return;
#endif
    c->val = val;
}

i64 __kml_weak_get(void *h, void *key) {
    kml_weak_cell *c = weak_find((kml_weak_cell **)h, key);
    if (!c) return 0;
#ifdef KML_GC
    i64 v;
    if (eph_get(h, key, &v)) return v;
#endif
    return c->val;
}

unsigned char __kml_weak_has(void *h, void *key) {
    return weak_find((kml_weak_cell **)h, key) != NULL;
}

/* Unlink the cell if present. Its link word's disappearing-link registration
 * is left as-is: the word becomes unreachable (Boehm tolerates a dangling
 * registration on collected memory). */
unsigned char __kml_weak_delete(void *h, void *key) {
    kml_weak_cell **prev = (kml_weak_cell **)h;
    if (!key) return 0;
    for (kml_weak_cell *c = *prev; c; prev = &c->next, c = c->next) {
        if (*c->link == key) {
            *prev = c->next;
#ifdef KML_GC
            eph_drop(h, key);
#endif
            return 1;
        }
    }
    return 0;
}

void *__kml_weakref_create(void *obj) { return link_new(obj); }

void *__kml_weakref_deref(void *box) { return *(void **)box; }

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
#define KML_WEAK_ANY_ALLOC(n) kml_gc_alloc_atomic_pinned(n)
#else
#define KML_WEAK_ANY_ALLOC(n) kml_alloc(KML_HEAP_RAW, n)
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
        i64 *n = (i64 *)kml_alloc(KML_HEAP_WORDS, (size_t)cap * sizeof(i64));
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
    void *base = kml_gc_base(c->obj);
    kml_gc_weak_link(&c->obj, base ? base : c->obj);
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

/* A private name's side table: the private elements of the objects that
 * are not instances of the declaring class's layout (a base constructor
 * returned them for `this`), made on first use in the global *slot. */
void *__kml_ptab(void **slot) {
    if (!*slot) *slot = __kml_weak_create(0);
    return *slot;
}
