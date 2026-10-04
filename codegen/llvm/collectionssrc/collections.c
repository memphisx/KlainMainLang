/* collections.c — the Map/Set runtime, Object.groupBy's group map, the
 * default/custom sort comparators and Object.freeze's frozen-object set
 * (in C per TDD-00240). The hash table is collections_engine.h; this file is
 * the string- and number-keyed flavors plus the iterator list and the pieces
 * every flavor shares. kml_layout.h and the engine are prepended by the
 * compiler. */
#include <stdio.h>

/* ---- iterators and shared map operations ------------------------------- */

/* As in SpiderMonkey's OrderedHashTable, each open iterator is a node on the
 * map's list: a removal before its position shifts it back, clear() rewinds
 * it. It walks the dense entries by pos, so it sees an entry added during the
 * walk, and leaves the list when it reaches the end. */
void *__kml_map_iter_open(kml_map *m) {
    kml_iter *n = (kml_iter *)malloc(sizeof(kml_iter));
    n->next = m->iters;
    n->pos = 0;
    n->map = m;
    m->iters = n;
    return n;
}

/* Closing twice is harmless. */
void __kml_map_iter_close(kml_iter *n) {
    kml_map *m = n->map;
    if (!m) return;
    n->map = NULL;
    for (kml_iter **link = &m->iters; *link; link = &(*link)->next) {
        if (*link == n) {
            *link = n->next;
            return;
        }
    }
}

static void iters_reset(kml_map *m) {
    for (kml_iter *n = m->iters; n; n = n->next) n->pos = 0;
}

/* Shared by Map.clear/Set.clear: resets the size and the hash index (stale
 * index entries would alias future inserts to old slots). */
void __kml_map_clear(kml_map *m) {
    iters_reset(m);
    m->size = 0;
    m->used = 0;
    memset(m->idx, 255, (size_t)m->idxcap * 8);
}

/* Removes entry idx, keeping the others in insertion order: the entries after
 * it move down one, the index slots naming them follow, and an open iterator
 * past it steps back. The caller has tombstoned its own slot. */
void __kml_map_remove_at(kml_map *m, i64 idx) {
    i64 last = m->size - 1, n = last - idx;
    memmove(m->keys + idx, m->keys + idx + 1, (size_t)n * 8);
    memmove(m->vals + idx, m->vals + idx + 1, (size_t)n * 8);
    m->size = last;
    if (n > 0)
        for (i64 s = 0; s < m->idxcap; s++)
            if (m->idx[s] > idx) m->idx[s]--;
    for (kml_iter *it = m->iters; it; it = it->next)
        if (it->pos > idx) it->pos--;
}

/* A copy of the keys / values array (size entries of 8 bytes); the generated
 * keys()/vals() wrappers pair it with the size. */
void *__kml_map_copy_keys(kml_map *m) {
    size_t b = (size_t)m->size * 8;
    void *a = malloc(b);
    memcpy(a, m->keys, b);
    return a;
}

void *__kml_map_copy_vals(kml_map *m) {
    size_t b = (size_t)m->size * 8;
    void *a = malloc(b);
    memcpy(a, m->vals, b);
    return a;
}

/* ---- string and number flavors ------------------------------------------ */

/* FNV-1a over the key's bytes. A null key (a dynamic null/undefined reaching a
 * string-keyed map, e.g. new Set().has(null)) hashes as the empty string
 * instead of reading address 0. */
i64 __kml_map_str_hash(const char *s) {
    unsigned long long h = 0xcbf29ce484222325ULL;
    if (!s) return (i64)h;
    for (; *s; s++) h = (h ^ (unsigned char)*s) * 1099511628211ULL;
    return (i64)h;
}

static i64 str_hash(i64 k) { return __kml_map_str_hash((const char *)(uintptr_t)k); }

/* Null-safe: a null key equals only another null key; strcmp never sees one. */
static int str_eq(i64 a, i64 b) {
    const char *x = (const char *)(uintptr_t)a, *y = (const char *)(uintptr_t)b;
    if (!x || !y) return !x && !y;
    return strcmp(x, y) == 0;
}

static i64 num_hash(i64 k) { return kml_mix(k); }
static int num_eq(i64 a, i64 b) { return a == b; }

#define STR_KEY(k) ((i64)(uintptr_t)(k))
#define NUM_KEY(k) (k)

KML_MAP_FLAVOR(str, const char *, str_hash, str_eq, STR_KEY, 0)
KML_MAP_FLAVOR(num, i64, num_hash, num_eq, NUM_KEY, 0)

/* ---- sort comparators ---------------------------------------------------- */

/* The closure a custom comparator sort is calling (set by the sort site). */
_Thread_local void *__kml_sort_clos;

/* The default (no-comparator) sort converts every element to a string and
 * compares lexicographically, even for numbers: [10,1,21,2] sorts to
 * [1,10,2,21]. */
int __kml_cmp_i64_lex(const i64 *pa, const i64 *pb) {
    char ba[32], bb[32];
    snprintf(ba, sizeof ba, "%lld", *pa);
    snprintf(bb, sizeof bb, "%lld", *pb);
    return strcmp(ba, bb);
}

int __kml_cmp_str(char *const *pa, char *const *pb) { return strcmp(*pa, *pb); }

static int sign_of(double r) { return r < 0 ? -1 : r > 0 ? 1 : 0; }

static void *clos_fn(void **env) {
    char *c = (char *)__kml_sort_clos;
    *env = *(void **)(c + KML_CLOSURE_ENV);
    return *(void **)(c + KML_CLOSURE_FN);
}

/* Custom-comparator trampolines: load both elements, call the closure with
 * (env, a, b) and reduce the result. An integer comparator's result is
 * truncated to i32; the others return a `number` (double) reduced to the
 * -1/0/1 sign (a fractional difference must not collapse to 0). */
int __kml_sort_tramp_i64(const i64 *pa, const i64 *pb) {
    void *env;
    i64 (*fn)(void *, i64, i64) = (i64 (*)(void *, i64, i64))clos_fn(&env);
    return (int)fn(env, *pa, *pb);
}

int __kml_sort_tramp_f64(const double *pa, const double *pb) {
    void *env;
    double (*fn)(void *, double, double) = (double (*)(void *, double, double))clos_fn(&env);
    return sign_of(fn(env, *pa, *pb));
}

/* Object/class elements (ADR-00681) and strings are 8-byte pointer slots. */
int __kml_sort_tramp_obj(void *const *pa, void *const *pb) {
    void *env;
    double (*fn)(void *, void *, void *) = (double (*)(void *, void *, void *))clos_fn(&env);
    return sign_of(fn(env, *pa, *pb));
}

int __kml_sort_tramp_str(void *const *pa, void *const *pb) {
    return __kml_sort_tramp_obj(pa, pb);
}

/* ---- Object.freeze's frozen-object set ----------------------------------- */

/* A number-keyed set (keys are ptrtoint(obj)), created on first use and read
 * by every object-field write check. Boehm does not scan thread-local
 * storage as a root, so under -mm=gc the header is allocated uncollectable:
 * never collected, still scanned, so it keeps its arrays reachable. */
#ifdef KML_GC_MODE
extern void *GC_malloc_uncollectable(size_t);
#endif

static _Thread_local kml_map *frozen_set;

void *__kml_frozen_set_get(void) {
    if (!frozen_set) {
#ifdef KML_GC_MODE
        kml_map *h = (kml_map *)GC_malloc_uncollectable(sizeof(kml_map));
#else
        kml_map *h = (kml_map *)malloc(sizeof(kml_map));
#endif
        kml_map_init(h);
        frozen_set = h;
    }
    return frozen_set;
}
