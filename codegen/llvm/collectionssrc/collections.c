/* collections.c — the Map/Set runtime, Object.groupBy's group map, the
 * default/custom sort comparators and Object.freeze's frozen-object set
 * (in C per TDD-00240). The hash table is collections_engine.h; this file is
 * the string- and number-keyed flavors plus the iterator list and the pieces
 * every flavor shares. kml_layout.h and the engine are prepended by the
 * compiler. */
#include <math.h>
#include <stdio.h>

/* ---- iterators and shared map operations ------------------------------- */

/* As in SpiderMonkey's OrderedHashTable, each open iterator is a node on the
 * map's list: a removal before its position shifts it back, clear() rewinds
 * it. It walks the dense entries by pos, so it sees an entry added during the
 * walk, and leaves the list when it reaches the end. */
void *__kml_map_iter_open(kml_map *m, long long hdr) {
    /* Its host header word precedes the node. */
    long long *base = (long long *)kml_alloc(KML_HEAP_WORDS, 8 + sizeof(kml_iter));
    base[0] = hdr;
    kml_iter *n = (kml_iter *)(base + 1);
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
    void *a = kml_alloc(KML_HEAP_WORDS, b);
    memcpy(a, m->keys, b);
    return a;
}

void *__kml_map_copy_vals(kml_map *m) {
    size_t b = (size_t)m->size * 8;
    void *a = kml_alloc(KML_HEAP_WORDS, b);
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

/* A string slot's null is the program's null-reference cell (its text is
 * empty): a key distinct from "", equal only to itself. Weak, as the
 * compiled units' own definitions are, so the binary has one cell. */
__attribute__((weak)) struct { i64 hdr; char text[248]; } __kml_nullref_cell;

static int is_nullref(const char *s) { return s == __kml_nullref_cell.text; }

static i64 str_hash(i64 k) {
    const char *s = (const char *)(uintptr_t)k;
    if (is_nullref(s)) return (i64)0x6e756c6cULL; /* "null" */
    return __kml_map_str_hash(s);
}

/* Null-safe: a null key equals only another null key; strcmp never sees one.
 * The null-reference cell equals only itself. */
static int str_eq(i64 a, i64 b) {
    const char *x = (const char *)(uintptr_t)a, *y = (const char *)(uintptr_t)b;
    if (!x || !y) return !x && !y;
    if (is_nullref(x) || is_nullref(y)) return x == y;
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


/* A TypedArray's default sort: numeric over the element's own width, -0
 * before +0 and NaN last. */
#define KML_CMP_TYPED_INT(name, T) \
    int __kml_cmp_typed_##name(const T *pa, const T *pb) { return (*pa > *pb) - (*pa < *pb); }
KML_CMP_TYPED_INT(i8, signed char)
KML_CMP_TYPED_INT(u8, unsigned char)
KML_CMP_TYPED_INT(i16, short)
KML_CMP_TYPED_INT(u16, unsigned short)
KML_CMP_TYPED_INT(i32, int)
KML_CMP_TYPED_INT(u32, unsigned int)
KML_CMP_TYPED_INT(i64, long long)
KML_CMP_TYPED_INT(u64, unsigned long long)
#define KML_CMP_TYPED_FLOAT(name, T) \
    int __kml_cmp_typed_##name(const T *pa, const T *pb) { \
        T a = *pa, b = *pb; \
        if (a != a) return b != b ? 0 : 1; \
        if (b != b) return -1; \
        if (a < b) return -1; \
        if (a > b) return 1; \
        if (a == 0 && b == 0) return (signbit(b) != 0) - (signbit(a) != 0); \
        return 0; \
    }
KML_CMP_TYPED_FLOAT(f32, float)
KML_CMP_TYPED_FLOAT(f64, double)

static int sign_of(double r) { return r < 0 ? -1 : r > 0 ? 1 : 0; }

static void *clos_fn(void **env) {
    char *c = (char *)__kml_sort_clos;
    *env = *(void **)(c + KML_CLOSURE_ENV);
    return *(void **)(c + KML_CLOSURE_FN);
}

/* Custom-comparator trampolines: load both elements, call the closure with
 * (env, a, b) and reduce its result to the -1/0/1 sign. The element's slot
 * (an integer or NaN box, a double, a pointer) picks the trampoline; the
 * comparator's own return type, which the program stores in __kml_sort_res
 * before the sort, picks how the result is read: a fractional double must
 * not collapse to 0, a boxed any is a number first, a boolean is 1 or 0 and
 * a void comparator's undefined is NaN, which sorts as 0. */
_Thread_local int __kml_sort_res;

enum { SORT_RES_I64, SORT_RES_F64, SORT_RES_ANY, SORT_RES_BOOL, SORT_RES_I32, SORT_RES_F32, SORT_RES_VOID, SORT_RES_I16, SORT_RES_I8 };

extern double __kml_any_tonum(i64 v);

static int sign_i64(i64 r) { return r < 0 ? -1 : r > 0 ? 1 : 0; }

/* One comparator call for each element slot, reading the result as the
 * comparator's return type says. */
static int sort_call_i64(i64 a, i64 b) {
    void *env;
    void *f = clos_fn(&env);
    switch (__kml_sort_res) {
    case SORT_RES_F64: return sign_of(((double (*)(void *, i64, i64))f)(env, a, b));
    case SORT_RES_F32: return sign_of(((float (*)(void *, i64, i64))f)(env, a, b));
    case SORT_RES_ANY: return sign_of(__kml_any_tonum(((i64 (*)(void *, i64, i64))f)(env, a, b)));
    case SORT_RES_BOOL: return ((_Bool (*)(void *, i64, i64))f)(env, a, b) ? 1 : 0;
    case SORT_RES_I32: return sign_i64(((int (*)(void *, i64, i64))f)(env, a, b));
    case SORT_RES_I16: return sign_i64(((short (*)(void *, i64, i64))f)(env, a, b));
    case SORT_RES_I8: return sign_i64(((signed char (*)(void *, i64, i64))f)(env, a, b));
    case SORT_RES_VOID: ((void (*)(void *, i64, i64))f)(env, a, b); return 0;
    default: return sign_i64(((i64 (*)(void *, i64, i64))f)(env, a, b));
    }
}

static int sort_call_f64(double a, double b) {
    void *env;
    void *f = clos_fn(&env);
    switch (__kml_sort_res) {
    case SORT_RES_F64: return sign_of(((double (*)(void *, double, double))f)(env, a, b));
    case SORT_RES_F32: return sign_of(((float (*)(void *, double, double))f)(env, a, b));
    case SORT_RES_ANY: return sign_of(__kml_any_tonum(((i64 (*)(void *, double, double))f)(env, a, b)));
    case SORT_RES_BOOL: return ((_Bool (*)(void *, double, double))f)(env, a, b) ? 1 : 0;
    case SORT_RES_I32: return sign_i64(((int (*)(void *, double, double))f)(env, a, b));
    case SORT_RES_I16: return sign_i64(((short (*)(void *, double, double))f)(env, a, b));
    case SORT_RES_I8: return sign_i64(((signed char (*)(void *, double, double))f)(env, a, b));
    case SORT_RES_VOID: ((void (*)(void *, double, double))f)(env, a, b); return 0;
    default: return sign_i64(((i64 (*)(void *, double, double))f)(env, a, b));
    }
}

/* A comparator whose parameters are float (a Float32Array element's). */
static int sort_call_f32(float a, float b) {
    void *env;
    void *f = clos_fn(&env);
    switch (__kml_sort_res) {
    case SORT_RES_F64: return sign_of(((double (*)(void *, float, float))f)(env, a, b));
    case SORT_RES_F32: return sign_of(((float (*)(void *, float, float))f)(env, a, b));
    case SORT_RES_ANY: return sign_of(__kml_any_tonum(((i64 (*)(void *, float, float))f)(env, a, b)));
    case SORT_RES_BOOL: return ((_Bool (*)(void *, float, float))f)(env, a, b) ? 1 : 0;
    case SORT_RES_I32: return sign_i64(((int (*)(void *, float, float))f)(env, a, b));
    case SORT_RES_I16: return sign_i64(((short (*)(void *, float, float))f)(env, a, b));
    case SORT_RES_I8: return sign_i64(((signed char (*)(void *, float, float))f)(env, a, b));
    case SORT_RES_VOID: ((void (*)(void *, float, float))f)(env, a, b); return 0;
    default: return sign_i64(((i64 (*)(void *, float, float))f)(env, a, b));
    }
}

static int sort_call_ptr(void *a, void *b) {
    void *env;
    void *f = clos_fn(&env);
    switch (__kml_sort_res) {
    case SORT_RES_F64: return sign_of(((double (*)(void *, void *, void *))f)(env, a, b));
    case SORT_RES_F32: return sign_of(((float (*)(void *, void *, void *))f)(env, a, b));
    case SORT_RES_ANY: return sign_of(__kml_any_tonum(((i64 (*)(void *, void *, void *))f)(env, a, b)));
    case SORT_RES_BOOL: return ((_Bool (*)(void *, void *, void *))f)(env, a, b) ? 1 : 0;
    case SORT_RES_I32: return sign_i64(((int (*)(void *, void *, void *))f)(env, a, b));
    case SORT_RES_I16: return sign_i64(((short (*)(void *, void *, void *))f)(env, a, b));
    case SORT_RES_I8: return sign_i64(((signed char (*)(void *, void *, void *))f)(env, a, b));
    case SORT_RES_VOID: ((void (*)(void *, void *, void *))f)(env, a, b); return 0;
    default: return sign_i64(((i64 (*)(void *, void *, void *))f)(env, a, b));
    }
}

/* Integer elements and NaN boxes (an any[]) are 8-byte i64 slots. */
int __kml_sort_tramp_i64(const i64 *pa, const i64 *pb) { return sort_call_i64(*pa, *pb); }

int __kml_sort_tramp_any(const i64 *pa, const i64 *pb) { return sort_call_i64(*pa, *pb); }

int __kml_sort_tramp_f64(const double *pa, const double *pb) { return sort_call_f64(*pa, *pb); }

/* A typed array's narrower elements: each read at its own width, then
 * passed as the comparator's parameters are typed (an integer, or a
 * `number` the `_d` form widens to). */
#define KML_SORT_TRAMP_INT(name, T) \
    int __kml_sort_tramp_##name(const T *pa, const T *pb) { return sort_call_i64((i64)*pa, (i64)*pb); } \
    int __kml_sort_tramp_##name##_d(const T *pa, const T *pb) { return sort_call_f64((double)*pa, (double)*pb); }
KML_SORT_TRAMP_INT(i8, signed char)
KML_SORT_TRAMP_INT(u8, unsigned char)
KML_SORT_TRAMP_INT(i16, short)
KML_SORT_TRAMP_INT(u16, unsigned short)
KML_SORT_TRAMP_INT(i32, int)
KML_SORT_TRAMP_INT(u32, unsigned int)
int __kml_sort_tramp_f32_d(const float *pa, const float *pb) { return sort_call_f64((double)*pa, (double)*pb); }
int __kml_sort_tramp_f32(const float *pa, const float *pb) { return sort_call_f32(*pa, *pb); }

/* Object/class elements (ADR-00681) and strings are 8-byte pointer slots. */
int __kml_sort_tramp_obj(void *const *pa, void *const *pb) { return sort_call_ptr(*pa, *pb); }

int __kml_sort_tramp_str(void *const *pa, void *const *pb) { return sort_call_ptr(*pa, *pb); }

/* Array elements (an array of arrays) are header pointers ({ data, length },
 * a null one an absent array); a closure takes each array parameter as the
 * header and its length. */
static i64 arr_len(void *h) { return h ? ((const i64 *)h)[1] : 0; }

static int sort_call_arr(void *a, void *b) {
    void *env;
    void *f = clos_fn(&env);
    i64 la = arr_len(a), lb = arr_len(b);
    switch (__kml_sort_res) {
    case SORT_RES_F64: return sign_of(((double (*)(void *, void *, i64, void *, i64))f)(env, a, la, b, lb));
    case SORT_RES_F32: return sign_of(((float (*)(void *, void *, i64, void *, i64))f)(env, a, la, b, lb));
    case SORT_RES_ANY: return sign_of(__kml_any_tonum(((i64 (*)(void *, void *, i64, void *, i64))f)(env, a, la, b, lb)));
    case SORT_RES_BOOL: return ((_Bool (*)(void *, void *, i64, void *, i64))f)(env, a, la, b, lb) ? 1 : 0;
    case SORT_RES_I32: return sign_i64(((int (*)(void *, void *, i64, void *, i64))f)(env, a, la, b, lb));
    case SORT_RES_I16: return sign_i64(((short (*)(void *, void *, i64, void *, i64))f)(env, a, la, b, lb));
    case SORT_RES_I8: return sign_i64(((signed char (*)(void *, void *, i64, void *, i64))f)(env, a, la, b, lb));
    case SORT_RES_VOID: ((void (*)(void *, void *, i64, void *, i64))f)(env, a, la, b, lb); return 0;
    default: return sign_i64(((i64 (*)(void *, void *, i64, void *, i64))f)(env, a, la, b, lb));
    }
}

int __kml_sort_tramp_arr(void *const *pa, void *const *pb) { return sort_call_arr(*pa, *pb); }

/* ---- Integrity levels ------------------------------------------------------ */

/* Object.freeze, seal and preventExtensions record a level in the object's
 * header word (KML_HDR_LEVEL_SHIFT): 0 none, 1 frozen, 2 sealed, 3
 * non-extensible. A level only tightens, and an immortal header (a shared
 * sentinel cell) never changes. A static object's word is its first;
 * an array's precedes its {data, len} (arrayguard.c). */
i64 __kml_obj_level(void *o) { return o ? kml_hdr_level((const i64 *)o) : 0; }

void __kml_obj_set_level(void *o, i64 lvl) {
    if (o) kml_hdr_raise_level((i64 *)o, lvl);
}

/* Whether a property may be added to the static object o. */
_Bool __kml_obj_extensible(void *o) { return __kml_obj_level(o) == 0; }

/* An object without a header word (a RegExp's host record) keeps its level
 * in a side table keyed by address: open addressing, grown at half full.
 * Under -mm=gc the table is pinned, as pattr_map is. */
static _Thread_local struct { i64 n, cap; i64 *slots; } side_lv;

static i64 *side_slot(void *o, int create) {
    if (!side_lv.cap) {
        if (!create) return 0;
        side_lv.cap = 64;
#ifdef KML_GC
        side_lv.slots = (i64 *)kml_gc_alloc_pinned(sizeof(i64) * 2 * 64);
#else
        side_lv.slots = (i64 *)kml_alloc(KML_HEAP_WORDS, sizeof(i64) * 2 * 64);
        memset(side_lv.slots, 0, sizeof(i64) * 2 * 64);
#endif
    }
    if (create && side_lv.n * 2 >= side_lv.cap) {
        i64 oc = side_lv.cap, *old = side_lv.slots;
        side_lv.cap = oc * 2;
#ifdef KML_GC
        side_lv.slots = (i64 *)kml_gc_alloc_pinned(sizeof(i64) * 2 * side_lv.cap);
#else
        side_lv.slots = (i64 *)kml_alloc(KML_HEAP_WORDS, sizeof(i64) * 2 * side_lv.cap);
        memset(side_lv.slots, 0, sizeof(i64) * 2 * side_lv.cap);
#endif
        side_lv.n = 0;
        for (i64 i = 0; i < oc; i++) {
            if (old[2 * i]) {
                *side_slot((void *)old[2 * i], 1) = old[2 * i + 1];
            }
        }
#ifndef KML_GC
        kml_free(KML_HEAP_WORDS, old);
#endif
    }
    unsigned long long h = ((unsigned long long)o >> 3) * 0x9E3779B97F4A7C15ULL;
    for (i64 i = (i64)(h & (unsigned long long)(side_lv.cap - 1));; i = (i + 1) & (side_lv.cap - 1)) {
        if (side_lv.slots[2 * i] == (i64)o) return &side_lv.slots[2 * i + 1];
        if (!side_lv.slots[2 * i]) {
            if (!create) return 0;
            side_lv.slots[2 * i] = (i64)o;
            side_lv.n++;
            return &side_lv.slots[2 * i + 1];
        }
    }
}

i64 __kml_side_level(void *o) {
    i64 *s = o ? side_slot(o, 0) : 0;
    return s ? *s : 0;
}

/* Raises o's level as kml_hdr_raise_level does: 1 frozen, 2 sealed, 3
 * non-extensible, 0 none; a level only tightens. */
void __kml_side_set_level(void *o, i64 lvl) {
    if (!o) return;
    i64 *s = side_slot(o, 1);
    if (*s == 0 || lvl < *s) *s = lvl;
}

/* The property-attribute records (propattrsrc/propattr.c), keyed by object
 * address, created on first use. Boehm does not scan thread-local storage
 * as a root, so under -mm=gc the header is allocated pinned: never
 * collected, still scanned, so it keeps its arrays reachable. */
static _Thread_local kml_map *pattr_map;

void *__kml_pattr_map_get(void) {
    if (!pattr_map) {
#ifdef KML_GC
        i64 *base = (i64 *)kml_gc_alloc_pinned(sizeof(i64) + sizeof(kml_map));
        base[0] = 0;
        kml_map *h = (kml_map *)(base + 1);
#else
        kml_map *h = kml_map_alloc(0);
#endif
        kml_map_init(h);
        pattr_map = h;
    }
    return pattr_map;
}

/* Frees a Map/Set's own allocations: its keys, values and index arrays and
 * the header word and body (from map - 8). Shallow: the entries' own
 * allocations stay, the program may still reach them. */
void __kml_map_free(kml_map *m) {
    kml_free(KML_HEAP_WORDS, m->keys);
    kml_free(KML_HEAP_WORDS, m->vals);
    kml_free(KML_HEAP_RAW, m->idx);
    kml_free(KML_HEAP_WORDS, (i64 *)m - 1);
}

/* The header word of a map: its type id (0 for a runtime-internal map). */
i64 __kml_map_header(kml_map *m) { return ((i64 *)m)[-1]; }

/* Initializes a Map/Set body inside a class instance that extends Map or Set
 * (TDD-00247): the instance's tag word is the header before it. */
void __kml_map_init_at(kml_map *m) { kml_map_init(m); }
