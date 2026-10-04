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
        t->slots = calloc((size_t)cap, sizeof *t->slots);
        t->cap = cap;
        t->count = 0;
        for (i64 i = 0; i < oldcap; i++)
            if (old[i]) kml_unit_put(t, old[i]);
        free(old);
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
// above the id (the header magic, the Error flag) must agree.
int __kml_class_is(i64 stored, i64 target) {
    if ((stored ^ target) & ~KML_ID_MASK) return 0;
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

// The records made, for telling one from a static reference.
static const void **kml_recs;
static i64 kml_recs_cap, kml_recs_n;

static void kml_recs_add(const void *r) {
    if ((kml_recs_n + 1) * 2 > kml_recs_cap) {
        i64 cap = kml_recs_cap ? kml_recs_cap * 2 : 64;
        const void **old = kml_recs;
        i64 oldcap = kml_recs_cap;
        kml_recs = calloc((size_t)cap, sizeof *kml_recs);
        if (!kml_recs) abort();
        kml_recs_cap = cap;
        kml_recs_n = 0;
        for (i64 i = 0; i < oldcap; i++)
            if (old[i]) kml_recs_add(old[i]);
        free(old);
    }
    i64 mask = kml_recs_cap - 1;
    for (i64 i = kml_unit_hash((i64)r) & mask;; i = (i + 1) & mask)
        if (!kml_recs[i]) { kml_recs[i] = r; kml_recs_n++; return; }
}

static int kml_is_rec(const void *p) {
    if (!kml_recs_cap) return 0;
    i64 mask = kml_recs_cap - 1;
    for (i64 i = kml_unit_hash((i64)p) & mask;; i = (i + 1) & mask) {
        if (!kml_recs[i]) return 0;
        if (kml_recs[i] == p) return 1;
    }
}

// __kml_classref_name is the name a constructor reference's payload holds:
// a static reference is its name's characters; a record names its class's.
const char *__kml_classref_name(const void *pay) {
    return kml_is_rec(pay) ? (const char *)((const void *const *)pay)[0] : (const char *)pay;
}

void *__kml_classrec_new(const void *ref, const void *base, i64 ncaps, i64 stride) {
    i64 *rec = calloc((size_t)(2 + ncaps), sizeof(i64));
    if (!rec) abort();
    rec[0] = (i64)ref;
    rec[1] = (i64)base; // the record of the local class it extends, or NULL
    kml_recs_add(rec);
    const char *row = __kml_unit_find(KML_UNIT_CTOR, (i64)ref);
    if (row && stride >= (i64)sizeof(i64)) {
        char *alias = malloc((size_t)stride);
        if (!alias) abort();
        memcpy(alias, row, (size_t)stride);
        *(i64 *)alias = (i64)rec;
        __kml_unit_register(KML_UNIT_CTOR, alias, 1, stride);
    }
    return rec;
}

// Each instance of a record's class, with the record it was built from.
static const void **kml_inst_k, **kml_inst_v;
static i64 kml_inst_cap, kml_inst_n;

static void kml_inst_put(const void *inst, const void *rec) {
    if ((kml_inst_n + 1) * 2 > kml_inst_cap) {
        i64 cap = kml_inst_cap ? kml_inst_cap * 2 : 64;
        const void **ok = kml_inst_k, **ov = kml_inst_v;
        i64 oldcap = kml_inst_cap;
        kml_inst_k = calloc((size_t)cap, sizeof *kml_inst_k);
        kml_inst_v = calloc((size_t)cap, sizeof *kml_inst_v);
        if (!kml_inst_k || !kml_inst_v) abort();
        kml_inst_cap = cap;
        kml_inst_n = 0;
        for (i64 i = 0; i < oldcap; i++)
            if (ok[i]) kml_inst_put(ok[i], ov[i]);
        free(ok);
        free(ov);
    }
    i64 mask = kml_inst_cap - 1;
    for (i64 i = kml_unit_hash((i64)inst) & mask;; i = (i + 1) & mask) {
        if (!kml_inst_k[i]) { kml_inst_k[i] = inst; kml_inst_v[i] = rec; kml_inst_n++; return; }
        if (kml_inst_k[i] == inst) { kml_inst_v[i] = rec; return; }
    }
}

static const void *kml_inst_get(const void *inst) {
    if (!kml_inst_cap) return NULL;
    i64 mask = kml_inst_cap - 1;
    for (i64 i = kml_unit_hash((i64)inst) & mask;; i = (i + 1) & mask) {
        if (!kml_inst_k[i]) return NULL;
        if (kml_inst_k[i] == inst) return kml_inst_v[i];
    }
}

// __kml_classrec_of is the record inst was built from, or NULL.
const void *__kml_classrec_of(const void *inst) { return kml_inst_get(inst); }

// __kml_classrec_bind records that inst was built from record rec.
void __kml_classrec_bind(const void *inst, const void *rec) {
    if (inst && rec) kml_inst_put(inst, rec);
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
static const void **kml_sa_k, **kml_sa_v;
static i64 kml_sa_cap, kml_sa_n;

static void kml_sa_put(const void *k, void *v) {
    if ((kml_sa_n + 1) * 2 > kml_sa_cap) {
        i64 cap = kml_sa_cap ? kml_sa_cap * 2 : 64;
        const void **ok = kml_sa_k, **ov = kml_sa_v;
        i64 oldcap = kml_sa_cap;
        kml_sa_k = calloc((size_t)cap, sizeof *kml_sa_k);
        kml_sa_v = calloc((size_t)cap, sizeof *kml_sa_v);
        if (!kml_sa_k || !kml_sa_v) abort();
        kml_sa_cap = cap;
        kml_sa_n = 0;
        for (i64 i = 0; i < oldcap; i++)
            if (ok[i]) kml_sa_put(ok[i], (void *)ov[i]);
        free(ok);
        free(ov);
    }
    i64 mask = kml_sa_cap - 1;
    for (i64 i = kml_unit_hash((i64)k) & mask;; i = (i + 1) & mask)
        if (!kml_sa_k[i]) { kml_sa_k[i] = k; kml_sa_v[i] = v; kml_sa_n++; return; }
}

static void *kml_sa_get(const void *k) {
    if (!kml_sa_cap) return NULL;
    i64 mask = kml_sa_cap - 1;
    for (i64 i = kml_unit_hash((i64)k) & mask;; i = (i + 1) & mask) {
        if (!kml_sa_k[i]) return NULL;
        if (kml_sa_k[i] == k) return (void *)kml_sa_v[i];
    }
}

// __kml_classrec_statics is the static area of record rec (of the class
// whose reference is ref), or of the class itself when rec is NULL.
void *__kml_classrec_statics(const void *rec, const void *ref, i64 bytes) {
    const void *k = rec ? rec : ref;
    void *a = kml_sa_get(k);
    if (!a) {
        a = calloc(1, (size_t)(bytes > 0 ? bytes : 1));
        if (!a) abort();
        kml_sa_put(k, a);
    }
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
