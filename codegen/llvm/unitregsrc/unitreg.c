// unitreg.c — the type-id tables compiled units register into (TDD-00238
// Stage 3). Each unit (the program, and later each library object) emits its
// rows as a constant array and hands it to __kml_unit_register from a
// constructor, so every table is complete before any module's code runs.
// A dispatcher looks a type id up here instead of switching over the whole
// program's classes.
//
// LAYOUT CONTRACT (must stay in sync with unitreg.go): a row is any struct
// whose first field is the i64 type id; `stride` is its size in bytes.
// A later row with an id already present replaces the earlier one.

#include <stdlib.h>
#include <string.h>
#include <stdint.h>

typedef long long i64;

#define KML_UNIT_KINDS 16

typedef struct {
    const char **slots; // row pointers, open addressing on the id
    i64 cap;            // power of two, 0 while empty
    i64 count;
} KmlUnitTable;

static KmlUnitTable kml_unit_tables[KML_UNIT_KINDS];

static i64 kml_unit_hash(i64 id) {
    unsigned long long x = (unsigned long long)id;
    x ^= x >> 33;
    x *= 0xff51afd7ed558ccdULL;
    x ^= x >> 33;
    return (i64)x;
}

static void kml_unit_put(KmlUnitTable *t, const char *row) {
    i64 id = *(const i64 *)row;
    i64 mask = t->cap - 1;
    for (i64 i = kml_unit_hash(id) & mask;; i = (i + 1) & mask) {
        if (!t->slots[i]) { t->slots[i] = row; t->count++; return; }
        if (*(const i64 *)t->slots[i] == id) { t->slots[i] = row; return; }
    }
}

// Registration runs from constructors (and from a dlopen'd island's), never
// concurrently with a lookup of the same kind.
void __kml_unit_register(i64 kind, const void *rows, i64 n, i64 stride) {
    if (kind < 0 || kind >= KML_UNIT_KINDS || n <= 0) return;
    KmlUnitTable *t = &kml_unit_tables[kind];
    if ((t->count + n) * 2 > t->cap) {
        i64 cap = 16;
        while (cap < (t->count + n) * 2) cap <<= 1;
        const char **old = t->slots;
        i64 oldcap = t->cap;
        t->slots = calloc((size_t)cap, sizeof *t->slots); /* scratch */
        t->cap = cap;
        t->count = 0;
        for (i64 i = 0; i < oldcap; i++)
            if (old[i]) kml_unit_put(t, old[i]);
        free(old); /* scratch */
    }
    for (i64 i = 0; i < n; i++) kml_unit_put(t, (const char *)rows + i * stride);
}

static int kml_is_rec(const void *p);
static __thread const void *kml_static_recv;

// __kml_unit_find returns the row registered under id, or NULL. A lookup of
// a record's constructor row is a dispatch through that evaluation's class
// (its static members, its construction): it notes the record as the
// receiver a static member's code reads its statics from (TDD-00242).
const void *__kml_unit_find(i64 kind, i64 id) {
    if (kind == 5 && kml_is_rec((const void *)id)) kml_static_recv = (const void *)id;
    if (kind < 0 || kind >= KML_UNIT_KINDS) return NULL;
    KmlUnitTable *t = &kml_unit_tables[kind];
    if (!t->cap) return NULL;
    i64 mask = t->cap - 1;
    for (i64 i = kml_unit_hash(id) & mask;; i = (i + 1) & mask) {
        const char *r = t->slots[i];
        if (!r) return NULL;
        if (*(const i64 *)r == id) return r;
    }
}

#define KML_UNIT_CLASS_PARENT 7
#define KML_ID_MASK 0xFFFFFFFFLL

// __kml_class_is answers `instanceof` on a stored type id: whether the class
// it names is target or extends it, walking each class's registered parent
// (a row { i64 id, i64 parent id }), as the prototype chain would. The bits
// above the id (the header magic, the Error flag) must agree; the object
// state (the integrity level, KML_HDR_STATE_MASK) is not the type.
int __kml_class_is(i64 stored, i64 target) {
    if ((stored ^ target) & ~(KML_ID_MASK | KML_HDR_STATE_MASK)) return 0;
    i64 id = stored & KML_ID_MASK, want = target & KML_ID_MASK;
    for (int depth = 0; depth < 4096; depth++) {
        if (id == want) return 1;
        const i64 *row = (const i64 *)__kml_unit_find(KML_UNIT_CLASS_PARENT, id);
        if (!row) return 0;
        id = row[1];
    }
    return 0;
}

// The dispatch hooks: find the row for o's header type id in a kind's table
// and call its function with (o, extra args), or return NULL on no match.
#define KML_UNIT_TOSTRING_TAG 0
#define KML_UNIT_INSPECT_CUSTOM 1

void *__kml_obj_tostring_tag(void *o) {
    const void *const *row = (const void *const *)__kml_unit_find(KML_UNIT_TOSTRING_TAG, *(const i64 *)o & KML_ID_MASK);
    return row ? ((void *(*)(void *))row[1])(o) : NULL;
}

void *__kml_obj_inspect_custom(void *o, i64 depth) {
    const void *const *row = (const void *const *)__kml_unit_find(KML_UNIT_INSPECT_CUSTOM, *(const i64 *)o & KML_ID_MASK);
    return row ? ((void *(*)(void *, i64))row[1])(o, depth) : NULL;
}

// Evaluation records (TDD-00242): a class declared in a function that reads
// the function's locals is a class of its own per evaluation. Its value is a
// record { const void *ref; i64 caps[n] }: ref is the class's static
// constructor reference, caps its captured bindings, boxed. The record is
// registered as an alias of ref's constructor row, so every dispatcher keyed
// by a constructor reference (`.name`, inspect, statics, construction)
// answers for it as for its class.
#define KML_UNIT_CTOR 5

static __thread void *kml_classrec_cur;

// The per-evaluation tables (records, each instance's record, each record's
// static area) are weak in their key: under -mm=gc (KML_GC) a key is held
// through a disappearing link, so a record or instance nothing else reaches
// is collected, and its entry, with the value it kept, is purged by the
// next sweep of its table. A collected record's constructor-row alias is
// removed from the constructor table with it. Under manual nothing is
// collected, and the links are never cleared.
#ifdef KML_GC
#define KW_ZALLOC(n) kml_gc_alloc(n) /* scratch */
// A key's link: cleared by the collector once the key, a heap object, dies;
// a static key (a class's own reference) is never collected.
static void **kw_link(const void *k) {
    void **w = (void **)kml_gc_alloc_atomic(sizeof *w); /* scratch */
    if (!w) abort();
    *w = (void *)k;
    if (kml_gc_base((void *)k) == k) kml_gc_weak_link(w, k);
    return w;
}
// A copied constructor row holds no managed pointer of its own, so the
// record it names stays collectable.
#define KW_ROW_ALLOC(n) kml_gc_alloc_atomic(n) /* scratch */
#else
#define KW_ZALLOC(n) calloc(1, (n)) /* scratch */
static void **kw_link(const void *k) {
    void **w = (void **)malloc(sizeof *w); /* scratch */
    if (!w) abort();
    *w = (void *)k;
    return w;
}
#define KW_ROW_ALLOC(n) malloc(n) /* scratch */
#endif

typedef struct KwCell {
    struct KwCell *next;
    void **link;     // the key, NULL once collected
    uintptr_t hidden; // ~key, which no scan reads as a pointer
    void *val;
} KwCell;

typedef struct {
    KwCell **b;
    i64 cap, n, since;
    void (*ondead)(const void *key); // a collected key's clean-up, or NULL
} KwTab;

// kw_unlink_dead drops the cells of bucket *pp whose key was collected.
static void kw_unlink_dead(KwTab *t, KwCell **pp) {
    while (*pp) {
        KwCell *c = *pp;
        if (*c->link) { pp = &c->next; continue; }
        *pp = c->next;
        t->n--;
        if (t->ondead) t->ondead((const void *)~c->hidden);
    }
}

static void kw_sweep(KwTab *t) {
    for (i64 i = 0; i < t->cap; i++) kw_unlink_dead(t, &t->b[i]);
    t->since = 0;
}

static KwCell *kw_find(KwTab *t, const void *k) {
    if (!t->cap) return NULL;
    KwCell **pp = &t->b[kml_unit_hash((i64)k) & (t->cap - 1)];
    kw_unlink_dead(t, pp);
    for (KwCell *c = *pp; c; c = c->next)
        if (*c->link == k) return c;
    return NULL;
}

static void kw_put(KwTab *t, const void *k, void *v) {
    KwCell *c = kw_find(t, k);
    if (c) { c->val = v; return; }
    if (++t->since > t->n / 2 + 32) kw_sweep(t);
    if ((t->n + 1) * 2 > t->cap) {
        i64 cap = t->cap ? t->cap * 2 : 64;
        KwCell **nb = (KwCell **)KW_ZALLOC((size_t)cap * sizeof *nb);
        if (!nb) abort();
        for (i64 i = 0; i < t->cap; i++)
            for (KwCell *x = t->b[i], *nx; x; x = nx) {
                nx = x->next;
                KwCell **h = &nb[kml_unit_hash((i64)~x->hidden) & (cap - 1)];
                x->next = *h;
                *h = x;
            }
#ifndef KML_GC
        free(t->b); /* scratch */
#endif
        t->b = nb;
        t->cap = cap;
    }
    c = (KwCell *)KW_ZALLOC(sizeof *c);
    if (!c) abort();
    c->link = kw_link(k);
    c->hidden = ~(uintptr_t)k;
    c->val = v;
    KwCell **h = &t->b[kml_unit_hash((i64)k) & (t->cap - 1)];
    c->next = *h;
    *h = c;
    t->n++;
}

// kml_unit_remove drops the row registered under id from a kind's table
// (backward-shift deletion keeps every other row's probe chain whole).
static void kml_unit_remove(i64 kind, i64 id) {
    KmlUnitTable *t = &kml_unit_tables[kind];
    if (!t->cap) return;
    i64 mask = t->cap - 1, i = kml_unit_hash(id) & mask;
    for (;; i = (i + 1) & mask) {
        if (!t->slots[i]) return;
        if (*(const i64 *)t->slots[i] == id) break;
    }
    for (i64 j = i;;) {
        j = (j + 1) & mask;
        if (!t->slots[j]) break;
        i64 k = kml_unit_hash(*(const i64 *)t->slots[j]) & mask;
        if (i <= j ? (i < k && k <= j) : (i < k || k <= j)) continue;
        t->slots[i] = t->slots[j];
        i = j;
    }
    t->slots[i] = NULL;
    t->count--;
}

static void kml_rec_dead(const void *rec) { kml_unit_remove(KML_UNIT_CTOR, (i64)rec); }

// The records made, for telling one from a static reference.
static KwTab kml_recs = {0, 0, 0, 0, kml_rec_dead};

static int kml_is_rec(const void *p) { return kw_find(&kml_recs, p) != NULL; }

// __kml_classref_name is the name a constructor reference's payload holds:
// a static reference is its name's characters; a record names its class's.
const char *__kml_classref_name(const void *pay) {
    return kml_is_rec(pay) ? (const char *)((const void *const *)pay)[0] : (const char *)pay;
}

void *__kml_classrec_new(const void *ref, const void *base, i64 ncaps, i64 stride) {
    i64 *rec = kml_calloc(KML_HEAP_WORDS, (size_t)(2 + ncaps), sizeof(i64));
    if (!rec) abort();
    rec[0] = (i64)ref;
    rec[1] = (i64)base; // the record of the local class it extends, or NULL
    kw_put(&kml_recs, rec, NULL);
    const char *row = __kml_unit_find(KML_UNIT_CTOR, (i64)ref);
    if (row && stride >= (i64)sizeof(i64)) {
        char *alias = KW_ROW_ALLOC((size_t)stride);
        if (!alias) abort();
        memcpy(alias, row, (size_t)stride);
        *(i64 *)alias = (i64)rec;
        __kml_unit_register(KML_UNIT_CTOR, alias, 1, stride);
    }
    return rec;
}

// Each instance of a record's class, with the record it was built from.
static KwTab kml_inst;

static const void *kml_inst_get(const void *inst) {
    KwCell *c = kw_find(&kml_inst, inst);
    return c ? c->val : NULL;
}

// __kml_classrec_of is the record inst was built from, or NULL.
const void *__kml_classrec_of(const void *inst) { return kml_inst_get(inst); }

// __kml_classrec_bind records that inst was built from record rec.
void __kml_classrec_bind(const void *inst, const void *rec) {
    if (inst && rec) kw_put(&kml_inst, inst, (void *)rec);
}

// __kml_classrec_is narrows `inst instanceof C` (already true for C's
// class) when C's payload is a record: inst must come from that evaluation
// or from a local class extending it.
int __kml_classrec_is(const void *inst, const void *pay) {
    if (!kml_is_rec(pay)) return 1;
    for (const i64 *r = kml_inst_get(inst); r; r = (const i64 *)r[1])
        if ((const void *)r == pay) return 1;
    return 0;
}

// __kml_classrec_enter notes the constructor a `new` through a value is about
// to build, for the construction arm to read its record's bindings.
void __kml_classrec_enter(void *pay) { kml_classrec_cur = pay; }

// __kml_classrec_current is the record the pending construction of the
// class whose static reference is ref builds from (cleared once read), or
// NULL when it builds through ref itself.
void *__kml_classrec_current(const void *ref) {
    void *r = kml_classrec_cur;
    kml_classrec_cur = NULL;
    if (!r || r == ref || ((const void **)r)[0] != ref) return NULL;
    return r;
}

// __kml_classrec_cap is binding i of record rec, undefined without one.
i64 __kml_classrec_cap(const i64 *rec, i64 i) { return rec ? rec[2 + i] : 10; }

// Statics per evaluation (TDD-00242): a record's class keeps its static
// fields in an area of its own, `bytes` long, allocated zeroed on first use.
// A class's code reached without a record (its own declaration, never used
// as a value) uses one area per class.
static KwTab kml_sa;

// __kml_classrec_statics is the static area of record rec (of the class
// whose reference is ref), or of the class itself when rec is NULL.
void *__kml_classrec_statics(const void *rec, const void *ref, i64 bytes) {
    const void *k = rec ? rec : ref;
    KwCell *c = kw_find(&kml_sa, k);
    if (c) return c->val;
    void *a = kml_calloc(KML_HEAP_WORDS, 1, (size_t)(bytes > 0 ? bytes : 1));
    if (!a) abort();
    kw_put(&kml_sa, k, a);
    return a;
}

// __kml_classrec_up is the record of the class whose reference is ref in
// rec's chain (rec itself, or the record of a local class it extends), or
// NULL.
const void *__kml_classrec_up(const i64 *rec, const void *ref) {
    for (; rec; rec = (const i64 *)rec[1])
        if ((const void *)rec[0] == ref) return rec;
    return NULL;
}

// __kml_classrec_recv sets the static receiver a direct call of a static
// member passes (the record the call names), returning the previous one.
const void *__kml_classrec_recv(const void *rec) {
    const void *old = kml_static_recv;
    kml_static_recv = rec;
    return old;
}

// __kml_classrec_static_cur is the record a static member of the class
// whose reference is ref runs against: the receiver's, up its chain.
const void *__kml_classrec_static_cur(const void *ref) {
    return __kml_classrec_up((const i64 *)kml_static_recv, ref);
}
