// shape.c — the run-time shape of a static object (TDD-00230 phase 5).
// Every object layout starts with a header word (objheader.go); its low 32
// bits key the layout table each compiled unit registers (unitreg.c), a
// row per layout with its generated get/set routines.
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

// The registered tables (unitreg.c); kind 3 holds KmlShape rows.
extern const void *__kml_unit_find(i64 kind, i64 id);
#define KML_UNIT_SHAPE 3

#define KML_HDR_MAGIC      (0x4B4DLL << 32)
#define KML_HDR_MAGIC_MASK (0x7FFFLL << 32)
#define KML_HDR_ERROR      (1LL << 48)

const KmlShape *__kml_shape_of(void *obj) {
    if (!obj) return NULL;
    i64 hdr = *(i64 *)obj;
    int valid = (hdr & KML_HDR_MAGIC_MASK) == KML_HDR_MAGIC;
    // An error-subclass instance carries its TagID (>= 1000) under the
    // Error flag instead of the magic.
    if (!valid && (hdr & KML_HDR_ERROR) && (hdr & 0xFFFFFFFFLL) >= KML_ERROR_SUBCLASS_BASE)
        valid = 1;
    if (!valid) return NULL;
    return (const KmlShape *)__kml_unit_find(KML_UNIT_SHAPE, hdr & 0xFFFFFFFFLL);
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

#ifdef KML_SHAPE_SPREAD
// { ...o } of a boxed static object: its own enumerable fields into a
// dynamic object.
extern void __kml_dynobj_set(void *bag, const char *key, i64 value);

void __kml_shape_spread(void *bag, void *obj) {
    i64 n = __kml_shape_nkeys(obj);
    for (i64 i = 0; i < n; i++) {
        const char *key = __kml_shape_key(obj, i);
        int found = 0;
        i64 v = __kml_shape_get(obj, key, &found);
        if (found == 1) __kml_dynobj_set(bag, key, v);
    }
}
#endif

#ifdef KML_OBJ_HOOKS
// The hooks dynjson.c reads a static object's layout row through. An
// Error's own enumerable fields beyond its layout (its system fields and
// extra bag) follow the layout's keys.
extern i64 __kml_error_nextra(void *o);
extern const char *__kml_error_extra_key(void *o, i64 i);
extern i64 __kml_error_extra_get(void *o, const char *key, int *found);

static int obj_is_error(void *o) { return (*(i64 *)o & KML_HDR_ERROR) != 0; }

// An Error's own name, once assigned on it: its index among the layout's
// keys (the name-own slot holds it plus one), clamped to their count; -1
// when it is not its own.
static i64 error_name_pos(void *o, i64 base) {
    if (!obj_is_error(o)) return -1;
    i64 p = *(i64 *)((char *)o + KML_ERR_NAME_OWN) - 1;
    return p > base ? base : p;
}

i64 __kml_obj_nkeys(void *o) {
    i64 n = __kml_shape_nkeys(o);
    if (!obj_is_error(o)) return n;
    i64 base = n < 0 ? 0 : n;
    i64 x = __kml_error_nextra(o) + (error_name_pos(o, base) >= 0);
    return x == 0 ? n : base + x;
}

// A string value is a pointer past its 8-byte length header.
static const struct { i64 len; char s[8]; } name_str = { 4, "name" };

const char *__kml_obj_key(void *o, i64 i0) {
    i64 n = __kml_shape_nkeys(o);
    i64 base = n < 0 ? 0 : n;
    i64 np = error_name_pos(o, base);
    int hasname = np >= 0;
    if (hasname && i0 == np) return name_str.s;
    i64 i = i0 - (hasname && i0 > np);
    if (i < base) return __kml_shape_key(o, i);
    if (!obj_is_error(o)) return NULL;
    return __kml_error_extra_key(o, i - base);
}

i64 __kml_obj_get(void *o, const char *k) {
    int f = 0;
    i64 v = __kml_shape_get(o, k, &f);
    if (f > 0 || !obj_is_error(o)) return v;
    i64 ev = __kml_error_extra_get(o, k, &f);
    return f > 0 ? ev : v;
}

int __kml_obj_has(void *o, const char *k) {
    int f = 0;
    __kml_shape_get(o, k, &f);
    if (f > 0) return 1;
    if (!obj_is_error(o)) return 0;
    __kml_error_extra_get(o, k, &f);
    return f > 0;
}

const char *__kml_obj_name(void *o) { return __kml_shape_class_name(o); }
#endif

#ifdef KML_SHAPE_KEYS_ARRAY
// A string[] of a static object's own keys: its layout row's, and an
// Error's own fields beyond it. Written to out as { ptr data, i64 len }
// (the IR wrapper keeps the aggregate-returning signature).
void __kml_shape_keys_array_c(void *o, void **out) {
    i64 n0 = __kml_obj_nkeys(o), n = n0 < 0 ? 0 : n0;
    const char **data = malloc((size_t)n * 8 + 8);
    i64 w = 0;
    for (i64 i = 0; i < n; i++) {
        const char *k = __kml_obj_key(o, i);
        if (__kml_obj_has(o, k)) data[w++] = k;
    }
    out[0] = data;
    out[1] = (void *)(size_t)w;
}
#endif

#ifdef KML_SHAPE_DESC
// Object.getOwnPropertyDescriptor(o, key) of a boxed static object: a data
// descriptor for an own field, undefined otherwise. Object.freeze and
// Object.seal record a static object's integrity level in the frozen set
// (1 frozen, 2 sealed, 3 non-extensible).
extern void *__kml_dynobj_new(void);
extern void __kml_dynobj_set(void *bag, const char *key, i64 value);
extern void *__kml_frozen_set_get(void);
extern i64 __kml_map_num_get(void *map, i64 key);

// A string value is a pointer past its 8-byte length header.
#define DESC_KEY(n, s) static const struct { i64 len; char s_[sizeof(s)]; } n = { sizeof(s) - 1, s }
DESC_KEY(desc_value, "value");
DESC_KEY(desc_writable, "writable");
DESC_KEY(desc_enumerable, "enumerable");
DESC_KEY(desc_configurable, "configurable");

i64 __kml_shape_own_desc(void *obj, const char *key) {
    int found = 0;
    i64 v = __kml_shape_get(obj, key, &found);
    if (found != 1) return KML_NB_UNDEFINED;
    i64 level = __kml_map_num_get(__kml_frozen_set_get(), (i64)obj);
    void *d = __kml_dynobj_new();
    __kml_dynobj_set(d, desc_value.s_, v);
    __kml_dynobj_set(d, desc_writable.s_, level == 1 ? KML_NB_FALSE : KML_NB_TRUE);
    __kml_dynobj_set(d, desc_enumerable.s_, KML_NB_TRUE);
    __kml_dynobj_set(d, desc_configurable.s_, level == 1 || level == 2 ? KML_NB_FALSE : KML_NB_TRUE);
    return (i64)d | 5; // a dynamic object's box
}
#endif
