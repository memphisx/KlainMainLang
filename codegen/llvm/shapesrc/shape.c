// shape.c — the run-time shape of a static object (TDD-00230 phase 5).
// Every object layout starts with a header word (objheader.go); its low 32
// bits index the program's layout table, which code generation emits as
// __kml_shapes (sorted by id) with a generated get/set routine per layout.
// This file finds a layout from an object's header and dispatches to it.
//
// LAYOUT CONTRACTS (must stay in sync with objheader.go / emit_shape.go):
//   header word: bits 0..31 type id, bits 32..46 KML_HDR_MAGIC,
//                bit 47 Symbol flag, bit 48 Error flag.
//   KmlShape:    { i64 id, char *name, get, set, i64 nkeys, char **keys }
//   get(obj, key, *found) returns the boxed value; *found = 1 for an own
//   field, 2 for a member found on the class (a method or accessor), and is
//   left 0 when the key is absent.
//   set(obj, key, value) returns 1 when stored, 0 when the key is not a
//   field, -2 when the field cannot take a dynamic value.

#include <stddef.h>
#include <stdlib.h>
#include <string.h>

typedef long long i64;

typedef struct KmlShape {
    i64 id;
    const char *name;
    i64 (*get)(void *obj, const char *key, int *found);
    int (*set)(void *obj, const char *key, i64 value);
    i64 nkeys;
    const char *const *keys;
} KmlShape;

extern const KmlShape __kml_shapes[];
extern const i64 __kml_nshapes;

#define KML_HDR_MAGIC      (0x4B4DLL << 32)
#define KML_HDR_MAGIC_MASK (0x7FFFLL << 32)
#define KML_HDR_ERROR      (1LL << 48)
#define KML_ERROR_SUBCLASS_BASE 1000
#define KML_NB_UNDEFINED   10

const KmlShape *__kml_shape_of(void *obj) {
    if (!obj) return NULL;
    i64 hdr = *(i64 *)obj;
    int valid = (hdr & KML_HDR_MAGIC_MASK) == KML_HDR_MAGIC;
    // An error-subclass instance carries its TagID (>= 1000) under the
    // Error flag instead of the magic.
    if (!valid && (hdr & KML_HDR_ERROR) && (hdr & 0xFFFFFFFFLL) >= KML_ERROR_SUBCLASS_BASE)
        valid = 1;
    if (!valid) return NULL;
    i64 id = hdr & 0xFFFFFFFFLL;
    i64 lo = 0, hi = __kml_nshapes - 1;
    while (lo <= hi) {
        i64 mid = (lo + hi) / 2;
        i64 mid_id = __kml_shapes[mid].id;
        if (mid_id == id) return &__kml_shapes[mid];
        if (mid_id < id) lo = mid + 1;
        else hi = mid - 1;
    }
    return NULL;
}

// ---- own properties added at run time ----
// A static object's layout has no room for a property JavaScript adds to it
// (`anyObj.extra = 1`); the added properties live here, keyed by the
// object's address, in insertion order.

typedef struct Expando {
    void *obj;
    i64 n, cap;
    char **keys;
    i64 *vals;
} Expando;

static Expando *expandos;
static i64 expando_cap, expando_used;

static size_t expando_hash(void *obj, i64 cap) {
    size_t h = (size_t)obj;
    h ^= h >> 17;
    h *= 0x9E3779B97F4A7C15ULL;
    return (size_t)(h >> 7) & (size_t)(cap - 1);
}

static Expando *expando_find(void *obj, int create) {
    if (expando_cap == 0) {
        if (!create) return NULL;
        expando_cap = 64;
        expandos = calloc((size_t)expando_cap, sizeof(Expando));
    }
    if (create && (expando_used + 1) * 4 > expando_cap * 3) {
        Expando *old = expandos;
        i64 ocap = expando_cap;
        expando_cap *= 2;
        expandos = calloc((size_t)expando_cap, sizeof(Expando));
        for (i64 i = 0; i < ocap; i++) {
            if (!old[i].obj) continue;
            size_t j = expando_hash(old[i].obj, expando_cap);
            while (expandos[j].obj) j = (j + 1) & (size_t)(expando_cap - 1);
            expandos[j] = old[i];
        }
        free(old);
    }
    size_t j = expando_hash(obj, expando_cap);
    while (expandos[j].obj) {
        if (expandos[j].obj == obj) return &expandos[j];
        j = (j + 1) & (size_t)(expando_cap - 1);
    }
    if (!create) return NULL;
    expandos[j].obj = obj;
    expando_used++;
    return &expandos[j];
}

static i64 expando_index(const Expando *x, const char *key) {
    for (i64 i = 0; x && i < x->n; i++)
        if (strcmp(x->keys[i], key) == 0) return i;
    return -1;
}

// __kml_shape_expando_count is the number of properties added to obj.
i64 __kml_shape_expando_count(void *obj) {
    const Expando *x = expando_find(obj, 0);
    return x ? x->n : 0;
}

// __kml_shape_expando_set adds (or overwrites) property key of obj.
void __kml_shape_expando_set(void *obj, const char *key, i64 value) {
    Expando *x = expando_find(obj, 1);
    i64 i = expando_index(x, key);
    if (i >= 0) {
        x->vals[i] = value;
        return;
    }
    if (x->n == x->cap) {
        x->cap = x->cap ? x->cap * 2 : 4;
        x->keys = realloc(x->keys, (size_t)x->cap * sizeof(char *));
        x->vals = realloc(x->vals, (size_t)x->cap * sizeof(i64));
    }
    // A key is a string value: its bytes after an 8-byte length header.
    size_t len = strlen(key);
    char *base = malloc(len + 9);
    *(i64 *)base = (i64)len;
    x->keys[x->n] = base + 8;
    memcpy(x->keys[x->n], key, len + 1);
    x->vals[x->n] = value;
    x->n++;
}

// __kml_shape_expando_delete removes property key of obj; 1 when it was
// there.
int __kml_shape_expando_delete(void *obj, const char *key) {
    Expando *x = expando_find(obj, 0);
    i64 i = expando_index(x, key);
    if (i < 0) return 0;
    for (i64 k = i + 1; k < x->n; k++) {
        x->keys[k - 1] = x->keys[k];
        x->vals[k - 1] = x->vals[k];
    }
    x->n--;
    return 1;
}

// __kml_shape_get reads key off obj through its layout, then its added
// properties. *found is -1 when the object has no shape.
i64 __kml_shape_get(void *obj, const char *key, int *found) {
    const KmlShape *s = __kml_shape_of(obj);
    *found = 0;
    if (!s) {
        *found = -1;
        return KML_NB_UNDEFINED;
    }
    // An own property kept beside the layout shadows a field (a value of
    // another kind than the field holds) or a class member of that name.
    const Expando *x = expando_find(obj, 0);
    i64 i = expando_index(x, key);
    if (i >= 0) {
        *found = 1;
        return x->vals[i];
    }
    i64 v = s->get(obj, key, found);
    return *found ? v : KML_NB_UNDEFINED;
}

// __kml_shape_set writes key on obj; -1 when the object has no shape. An
// added property is overwritten (1); a key that is neither a field nor an
// added property is 0 (the caller adds it when the object is extensible).
int __kml_shape_set(void *obj, const char *key, i64 value) {
    const KmlShape *s = __kml_shape_of(obj);
    if (!s) return -1;
    int r = s->set(obj, key, value);
    if (r != 0) return r;
    Expando *x = expando_find(obj, 0);
    i64 i = expando_index(x, key);
    if (i >= 0) {
        x->vals[i] = value;
        return 1;
    }
    return 0;
}

// __kml_shape_class_name is the class name of obj's layout, or NULL for a
// plain object (or no shape).
const char *__kml_shape_class_name(void *obj) {
    const KmlShape *s = __kml_shape_of(obj);
    return s ? s->name : NULL;
}

// __kml_shape_nkeys / __kml_shape_key enumerate obj's own enumerable keys
// in declaration order, then its added properties in insertion order; -1
// keys when the object has no shape.
// An added property named like a field shadows it and is listed at the
// field's place, not again.
static int shadows_field(const KmlShape *s, const char *key) {
    for (i64 k = 0; k < s->nkeys; k++)
        if (strcmp(s->keys[k], key) == 0) return 1;
    return 0;
}

i64 __kml_shape_nkeys(void *obj) {
    const KmlShape *s = __kml_shape_of(obj);
    if (!s) return -1;
    i64 n = s->nkeys;
    const Expando *x = expando_find(obj, 0);
    for (i64 i = 0; x && i < x->n; i++)
        if (!shadows_field(s, x->keys[i])) n++;
    return n;
}

const char *__kml_shape_key(void *obj, i64 i) {
    const KmlShape *s = __kml_shape_of(obj);
    if (!s || i < 0) return NULL;
    if (i < s->nkeys) return s->keys[i];
    const Expando *x = expando_find(obj, 0);
    i -= s->nkeys;
    for (i64 k = 0; x && k < x->n; k++) {
        if (shadows_field(s, x->keys[k])) continue;
        if (i == 0) return x->keys[k];
        i--;
    }
    return NULL;
}
