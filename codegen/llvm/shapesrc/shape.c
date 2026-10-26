// shape.c — the run-time shape of a static object (TDD-00230 phase 5).
// Every object layout starts with a header word (objheader.go); its low 32
// bits key the layout table each compiled unit registers (unitreg.c), a
// row per layout with its generated get/set routines.
// This file finds a layout from an object's header and dispatches to it.
//
// LAYOUT CONTRACTS (must stay in sync with objheader.go / emit_shape.go):
//   header word: bits 0..31 type id, bits 32..46 KML_HDR_MAGIC,
//                bit 47 Symbol flag, bit 48 Error flag.
//   KmlShape:    { i64 id, char *name, get, set, i64 nkeys, char **keys,
//                  void ***syms }: syms[i] is the global holding the symbol
//                  of a symbol-keyed field's key, NULL for a string key.
//   get(obj, key, *found) returns the boxed value; *found = 1 for an own
//   field, 2 for a member found on the class (a method or accessor), and is
//   left 0 when the key is absent.
//   set(obj, key, value) returns 1 when stored, 0 when the key is not a
//   field, -2 when the field cannot take a dynamic value.

#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
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
    void **const *syms;
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

// Whether a property key is symbol sym's own key ("\x01@@sym:%p"): a field
// keyed by a declared symbol answers it (TDD-00246).
_Bool __kml_key_is_sym(const char *key, void *sym) {
    if (!key || !sym || strncmp(key, "\x01@@sym:", 7) != 0) return 0;
    char want[40];
    snprintf(want, sizeof want, "\x01@@sym:%p", sym);
    return strcmp(key, want) == 0;
}

// The symbol a symbol-keyed field's layout key names (TDD-00246), or NULL.
static void *layout_key_symbol(const KmlShape *s, const char *key) {
    if (!s || !s->syms || !key || key[0] != 1) return NULL;
    for (i64 i = 0; i < s->nkeys; i++)
        if (s->syms[i] && strcmp(s->keys[i], key) == 0) return *s->syms[i];
    return NULL;
}

// A symbol's own run-time property key, "\x01@@sym:%p".
static char *symbol_key(void *sym) {
    char *k = malloc(40); /* scratch */
    snprintf(k, 40, "\x01@@sym:%p", sym);
    return k;
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
    // attrs[i] is entry i's attributes (XA_*), NULL while every entry is a
    // writable, enumerable, configurable data property. An accessor's value
    // is a {getter, setter, owner} triple: two function records (either
    // may be null) and the boxed owner, the receiver when none is given.
    unsigned char *attrs;
} Expando;

#define XA_W 1
#define XA_E 2
#define XA_C 4
#define XA_ACC 8
#define XA_DEFAULT (XA_W | XA_E | XA_C)

static int xattrs(const Expando *x, i64 i) { return x->attrs ? x->attrs[i] : XA_DEFAULT; }

// A function record {fn, env}, called as fn(env, this, argc, argv).
typedef i64 (*xdynfn)(void *env, i64 thisv, i64 argc, i64 *argv);
static i64 xcall(void *rec, i64 thisv, i64 argc, i64 *argv) {
    return (*(xdynfn *)rec)(((void **)rec)[1], thisv, argc, argv);
}

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
        expandos = kml_calloc(KML_HEAP_WORDS, (size_t)expando_cap, sizeof(Expando));
    }
    if (create && (expando_used + 1) * 4 > expando_cap * 3) {
        Expando *old = expandos;
        i64 ocap = expando_cap;
        expando_cap *= 2;
        expandos = kml_calloc(KML_HEAP_WORDS, (size_t)expando_cap, sizeof(Expando));
        for (i64 i = 0; i < ocap; i++) {
            if (!old[i].obj) continue;
            size_t j = expando_hash(old[i].obj, expando_cap);
            while (expandos[j].obj) j = (j + 1) & (size_t)(expando_cap - 1);
            expandos[j] = old[i];
        }
        kml_free(KML_HEAP_WORDS, old);
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

// __kml_shape_expando_has: whether key was added to obj as its own
// property (an instance's own method shadowing its class's).
int __kml_shape_expando_has(void *obj, const char *key) {
    return expando_index(expando_find(obj, 0), key) >= 0;
}

// __kml_shape_expando_key is the i-th property added to obj (a symbol's as
// its "\x01@@sym:%p" key), NULL past the last.
const char *__kml_shape_expando_key(void *obj, i64 i) {
    const Expando *x = expando_find(obj, 0);
    return x && i >= 0 && i < x->n ? x->keys[i] : NULL;
}

// __kml_shape_expando_get reads property key added to obj into *out; 0 when
// it was never added.
int __kml_shape_expando_get(void *obj, const char *key, i64 *out) {
    const Expando *x = expando_find(obj, 0);
    i64 i = expando_index(x, key);
    if (i < 0) return 0;
    *out = xattrs(x, i) & XA_ACC ? 10 : x->vals[i]; /* an accessor: _get_recv */
    return 1;
}

// __kml_shape_expando_get_recv is __kml_shape_expando_get running an
// accessor's getter with this = recv.
int __kml_shape_expando_get_recv(void *obj, const char *key, i64 recv, i64 *out) {
    const Expando *x = expando_find(obj, 0);
    i64 i = expando_index(x, key);
    if (i < 0) return 0;
    if (!(xattrs(x, i) & XA_ACC)) {
        *out = x->vals[i];
        return 1;
    }
    void **pair = (void **)x->vals[i];
    *out = pair[0] ? xcall(pair[0], recv ? recv : (i64)pair[2], 0, NULL) : 10;
    return 1;
}

// __kml_shape_expando_attrs is property key's attributes (XA_*), -1 when
// it was never added.
int __kml_shape_expando_attrs(void *obj, const char *key) {
    const Expando *x = expando_find(obj, 0);
    i64 i = expando_index(x, key);
    return i < 0 ? -1 : xattrs(x, i);
}

// __kml_shape_expando_raw is property key's stored word: a data value, or
// an accessor's {getter, setter} pair.
i64 __kml_shape_expando_raw(void *obj, const char *key) {
    const Expando *x = expando_find(obj, 0);
    i64 i = expando_index(x, key);
    return i < 0 ? 10 : x->vals[i];
}

// __kml_shape_expando_put assigns property key of obj, as JS's [[Set]] on
// an own property: -1 when there is none, 0 stored (or a setter run with
// this = recv), 1 read-only, 3 an accessor without a setter.
int __kml_shape_expando_put(void *obj, const char *key, i64 v, i64 recv) {
    Expando *x = expando_find(obj, 0);
    i64 i = expando_index(x, key);
    if (i < 0) return -1;
    int a = xattrs(x, i);
    if (a & XA_ACC) {
        void **pair = (void **)x->vals[i];
        if (!pair[1]) return 3;
        i64 argv[1] = {v};
        xcall(pair[1], recv ? recv : (i64)pair[2], 1, argv);
        return 0;
    }
    if (!(a & XA_W)) return 1;
    x->vals[i] = v;
    return 0;
}

static int shadows_field(const KmlShape *s, const char *key);
int __kml_shape_expando_delete(void *obj, const char *key);

// unshadow drops obj's shadow of field key, a static write having stored
// the field.
static void unshadow(void *obj, const char *key) { __kml_shape_expando_delete(obj, key); }

// Set when the first shadow is made: a static field write calls it to drop
// a shadow of the field it stores (null until then, so a program with no
// shadow pays one load).
void (*__kml_unshadow_hook)(void *, const char *) = 0;

// Set with __kml_unshadow_hook: a static read of a field some program
// writes through `any` reads its shadow, when obj has one.
int (*__kml_shadow_get_hook)(void *, const char *, i64 *) = 0;

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
        x->keys = kml_realloc(KML_HEAP_WORDS, x->keys, (size_t)x->cap * sizeof(char *));
        x->vals = kml_realloc(KML_HEAP_WORDS, x->vals, (size_t)x->cap * sizeof(i64));
        if (x->attrs) x->attrs = kml_realloc(KML_HEAP_RAW, x->attrs, (size_t)x->cap);
    }
    if (x->attrs) x->attrs[x->n] = XA_DEFAULT;
    // A key is a string value: its bytes after an 8-byte length header.
    size_t len = strlen(key);
    char *base = kml_alloc(KML_HEAP_RAW, len + 9);
    *(i64 *)base = (i64)len;
    x->keys[x->n] = base + 8;
    memcpy(x->keys[x->n], key, len + 1);
    x->vals[x->n] = value;
    x->n++;
    const KmlShape *s = __kml_shape_of(obj);
    if (s && shadows_field(s, key)) {
        __kml_unshadow_hook = unshadow;
        __kml_shadow_get_hook = __kml_shape_expando_get;
    }
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
        if (x->attrs) x->attrs[k - 1] = x->attrs[k];
    }
    x->n--;
    return 1;
}

// __kml_shape_expando_define adds (or replaces) property key of obj with
// the stored word v and attributes a (XA_*; an accessor's v is its pair).
void __kml_shape_expando_define(void *obj, const char *key, i64 v, int a) {
    __kml_shape_expando_set(obj, key, v);
    Expando *x = expando_find(obj, 0);
    i64 i = expando_index(x, key);
    if (!x->attrs) {
        if (a == XA_DEFAULT) return;
        x->attrs = kml_alloc(KML_HEAP_RAW, (size_t)x->cap);
        memset(x->attrs, XA_DEFAULT, (size_t)x->cap);
    }
    x->attrs[i] = (unsigned char)a;
}

// A dynamic bag where a static object was expected: a structural type's
// value converted from any is the bag itself (emitAnyToLayout), read by
// name. Weak: a program may link this file without dynamic objects.
#define KML_BAG_MAGIC 0x444C4D4BLL
static int is_bag(void *o) { return o && (*(i64 *)o & 0xFFFFFFFFLL) == KML_BAG_MAGIC; }
extern char **__kml_dynobj_keys_enum_c(void *o, i64 *n) __attribute__((weak)) __attribute__((weak));
extern i64 __kml_dynobj_get(void *o, const char *key) __attribute__((weak)) __attribute__((weak));
extern i64 __kml_dynobj_setv(void *o, const char *key, i64 v) __attribute__((weak)) __attribute__((weak));
extern i64 __kml_dynobj_find(void *o, const char *key) __attribute__((weak)) __attribute__((weak));

// __kml_shape_get reads key off obj through its layout, then its added
// properties. *found is -1 when the object has no shape.
extern i64 __kml_error_extra_get(void *o, const char *key, int *found) __attribute__((weak));

// Whether obj is a generator instance, by its layout's toStringTag row: 1
// for a generator, 2 for an async one, else 0.
const char *__kml_generator_tag(void *o);
const char *__kml_async_generator_tag(void *o);
int __kml_shape_generator_kind(void *obj) {
    const KmlShape *s = __kml_shape_of(obj);
    if (!s) return 0;
    const i64 *row = (const i64 *)__kml_unit_find(0, s->id);
    if (!row) return 0;
    if (row[1] == (i64)(intptr_t)__kml_generator_tag) return 1;
    if (row[1] == (i64)(intptr_t)__kml_async_generator_tag) return 2;
    return 0;
}

// A generator instance's prototype: its recorded one (at the offset the
// compiler emits when a program can observe it), else the root. 0 when the
// program records none.
// Defaults the compiler's offset and fnmeta.c's root override.
__attribute__((weak)) const i64 __kml_gen_proto_off = -1;
__attribute__((weak)) void *__kml_gen_root(int async) {
    (void)async;
    return NULL;
}
i64 __kml_shape_generator_proto(void *obj, int kind) {
    if (__kml_gen_proto_off < 0) return 0;
    i64 p = *(i64 *)((char *)obj + __kml_gen_proto_off);
    if (p) return p;
    void *root = __kml_gen_root(kind == 2);
    return root ? ((i64)(intptr_t)root | 5) : 0;
}

i64 __kml_shape_get(void *obj, const char *key, int *found) {
    if (is_bag(obj)) {
        i64 v = __kml_dynobj_get(obj, key);
        *found = __kml_dynobj_find(obj, key) >= 0 || v != KML_NB_UNDEFINED;
        return v;
    }
    /* An error's `stack` is formatted on first read, not its raw slot. */
    if ((*(i64 *)obj & KML_HDR_ERROR) && !strcmp(key, "stack")) return __kml_error_extra_get(obj, key, found);
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
        i64 v = x->vals[i];
        if (xattrs(x, i) & XA_ACC) __kml_shape_expando_get_recv(obj, key, 0, &v); /* its getter */
        return v;
    }
    i64 v = s->get(obj, key, found);
    if (*found) return v;
    // A generator's member its layout lacks is its prototype chain's.
    int gk = __kml_shape_generator_kind(obj);
    i64 proto = gk ? __kml_shape_generator_proto(obj, gk) : 0;
    if (proto && (proto & 7) == 5) {
        void *bag = (void *)(intptr_t)(proto & ~7LL);
        v = __kml_dynobj_get(bag, key);
        *found = v != KML_NB_UNDEFINED;
        return v;
    }
    return KML_NB_UNDEFINED;
}

// __kml_shape_set writes key on obj; -1 when the object has no shape. An
// added property is overwritten (1); a key that is neither a field nor an
// added property is 0 (the caller adds it when the object is extensible).
int __kml_shape_set(void *obj, const char *key, i64 value) {
    if (is_bag(obj)) return __kml_dynobj_setv(obj, key, value) == 0 ? 1 : 0;
    const KmlShape *s = __kml_shape_of(obj);
    if (!s) return -1;
    int r = s->set(obj, key, value);
    if (r == 1) {
        // Stored in the field: a shadow of it (an earlier value of another
        // kind) is gone.
        if (expando_cap) __kml_shape_expando_delete(obj, key);
        return 1;
    }
    if (r != 0) return r;
    // An added property: a setter runs, a read-only one keeps its value.
    if (__kml_shape_expando_put(obj, key, value, 0) >= 0) return 1;
    return 0;
}

// __kml_shape_class_name is the class name of obj's layout, or NULL for a
// plain object (or no shape).
const char *__kml_shape_class_name(void *obj) {
    const KmlShape *s = __kml_shape_of(obj);
    return s ? s->name : NULL;
}

// __kml_proto_call is a builtin prototype's method (`Map.prototype.get`)
// called on thisv: the method of thisv's own host layout, which its shape
// getter answers under the hidden key (`__kml_lm_Map.get`), called with
// thisv and the arguments. *ok is 0, and nothing is called, when thisv is no
// instance of that class. An object word's low bits are 1, a
// dynamic-function record's 7; the rest is the pointer.
typedef i64 (*kml_dynfn)(void *env, i64 thisv, i64 argc, i64 *argv);
i64 __kml_proto_call(i64 thisv, const char *hidden, i64 argc, i64 *argv, int *ok) {
    unsigned long long w = (unsigned long long)thisv;
    *ok = 0;
    if (w < 65536 || w >= (1ULL << 49) || (w & 7) != 1 || !(w & ~7ULL)) return KML_NB_UNDEFINED;
    int found = 0;
    unsigned long long m = (unsigned long long)__kml_shape_get((void *)(w & ~7ULL), hidden, &found);
    if (found <= 0 || m < 65536 || m >= (1ULL << 49) || (m & 7) != 7) return KML_NB_UNDEFINED;
    void **rec = (void **)(m & ~7ULL);
    *ok = 1;
    return (*(kml_dynfn *)rec)(rec[1], thisv, argc, argv);
}

// __kml_proto_get is a builtin prototype's getter (`Map.prototype.size`)
// called on thisv: thisv's own property key, when thisv is an instance of
// the class: its layout answers the class's hidden method key probe, or its
// class name is cls. *ok is 0, and nothing is read, otherwise.
i64 __kml_proto_get(i64 thisv, const char *probe, const char *cls, const char *key, int *ok) {
    unsigned long long w = (unsigned long long)thisv;
    *ok = 0;
    if (w < 65536 || w >= (1ULL << 49) || (w & 7) != 1 || !(w & ~7ULL)) return KML_NB_UNDEFINED;
    void *o = (void *)(w & ~7ULL);
    int found = 0;
    if (probe) (void)__kml_shape_get(o, probe, &found);
    if (found <= 0) {
        const char *name = __kml_shape_class_name(o);
        if (!name || strcmp(name, cls) != 0) return KML_NB_UNDEFINED;
    }
    *ok = 1;
    found = 0;
    return __kml_shape_get(o, key, &found);
}

// __kml_receiver_desc is how Node names an object receiver in a TypeError
// (`#<Set>`): its layout's class, `Object` for a plain or dynamic object;
// NULL for a primitive, which the caller renders as its string.
__attribute__((weak)) const char *__kml_anyarr_class_name(void *box) { (void)box; return "Array"; }
const char *__kml_receiver_desc(i64 v) {
    unsigned long long w = (unsigned long long)v;
    unsigned tag = (unsigned)(w & 7);
    if (w < 65536 || w >= (1ULL << 49) || (tag != 1 && tag != 2 && tag != 5 && tag != 6) || !(w & ~7ULL)) return NULL;
    /* An exotic builtin (an array, a TypedArray, a Date) is its tag, as
       V8 names it; any other object its constructor. */
    const char *tagName = tag == 2 ? __kml_anyarr_class_name((void *)(w & ~7ULL)) : tag == 6 ? "Array" : NULL;
    const char *name = tag == 1 ? __kml_shape_class_name((void *)(w & ~7ULL)) : NULL;
    if (name && strcmp(name, "Date") == 0) tagName = name;
    if (!name) name = "Object";
    const char *fmt = tagName ? "[object %s]" : "#<%s>";
    if (tagName) name = tagName;
    size_t len = strlen(name) + (tagName ? 9 : 3);
    char *base = kml_alloc(KML_HEAP_RAW, len + 9);
    *(i64 *)base = (i64)len;
    snprintf(base + 8, len + 1, fmt, name);
    return base + 8;
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

// __kml_shape_has_field: whether key is a field of obj's layout.
int __kml_shape_has_field(void *obj, const char *key) {
    const KmlShape *s = is_bag(obj) ? NULL : __kml_shape_of(obj);
    return s && shadows_field(s, key);
}

// Whether added property i of x is listed: an enumerable one that does not
// stand in for a field.
static int expando_listed(const KmlShape *s, const Expando *x, i64 i) {
    return !shadows_field(s, x->keys[i]) && (xattrs(x, i) & XA_E);
}

i64 __kml_shape_nkeys(void *obj) {
    if (is_bag(obj)) {
        i64 n = 0;
        kml_free(KML_HEAP_WORDS, __kml_dynobj_keys_enum_c(obj, &n));
        return n;
    }
    const KmlShape *s = __kml_shape_of(obj);
    if (!s) return -1;
    i64 n = s->nkeys;
    const Expando *x = expando_find(obj, 0);
    for (i64 i = 0; x && i < x->n; i++)
        if (expando_listed(s, x, i)) n++;
    return n;
}

const char *__kml_shape_key(void *obj, i64 i) {
    if (is_bag(obj)) {
        i64 n = 0;
        char **keys = __kml_dynobj_keys_enum_c(obj, &n);
        const char *k = i >= 0 && i < n ? keys[i] : NULL; /* the bag's own key */
        kml_free(KML_HEAP_WORDS, keys);
        return k;
    }
    const KmlShape *s = __kml_shape_of(obj);
    if (!s || i < 0) return NULL;
    if (i < s->nkeys) return s->keys[i];
    const Expando *x = expando_find(obj, 0);
    i -= s->nkeys;
    for (i64 k = 0; x && k < x->n; k++) {
        if (!expando_listed(s, x, k)) continue;
        if (i == 0) return x->keys[k];
        i--;
    }
    return NULL;
}

// { ...o } of a boxed static object: its own enumerable fields into a
// dynamic object.
extern void __kml_dynobj_set(void *bag, const char *key, i64 value) __attribute__((weak));

void __kml_shape_spread(void *bag, void *obj) {
    const KmlShape *s = __kml_shape_of(obj);
    i64 n = __kml_shape_nkeys(obj);
    for (i64 i = 0; i < n; i++) {
        const char *key = __kml_shape_key(obj, i);
        int found = 0;
        i64 v = __kml_shape_get(obj, key, &found);
        if (found != 1) continue;
        // A symbol-keyed field is copied as its symbol's property.
        void *sym = layout_key_symbol(s, key);
        __kml_dynobj_set(bag, sym ? symbol_key(sym) : key, v);
    }
}

// The hooks dynjson.c reads a static object's layout row through. An
// Error's own enumerable fields beyond its layout (its system fields and
// extra bag) follow the layout's keys.
extern i64 __kml_error_nextra(void *o) __attribute__((weak));
extern const char *__kml_error_extra_key(void *o, i64 i) __attribute__((weak));
extern i64 __kml_error_extra_get(void *o, const char *key, int *found) __attribute__((weak));

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
static const struct { i64 len; char s[8]; } name_str = { KML_STR_ASCII_HDR(4), "name" };

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

// A generator object's Symbol.toStringTag, the tag row each generator
// layout registers.
const char *__kml_generator_tag(void *o) { (void)o; return "Generator"; }
const char *__kml_async_generator_tag(void *o) { (void)o; return "AsyncGenerator"; }

// A string[] of a static object's own keys: its layout row's, and an
// Error's own fields beyond it. Written to out as { ptr data, i64 len }
// (the IR wrapper keeps the aggregate-returning signature).
void __kml_shape_keys_array_c(void *o, void **out) {
    i64 n0 = __kml_obj_nkeys(o), n = n0 < 0 ? 0 : n0;
    const char **data = kml_alloc(KML_HEAP_WORDS, (size_t)n * 8 + 8);
    i64 w = 0;
    for (i64 i = 0; i < n; i++) {
        const char *k = __kml_obj_key(o, i);
        /* A symbol's key ("\x01…": a symbol-keyed field or property) is
           no string key. */
        if (k && k[0] != 1 && __kml_obj_has(o, k)) data[w++] = k;
    }
    out[0] = data;
    out[1] = (void *)(size_t)w;
}

// { ...src } into the static object dst: src's enumerable properties added
// beside its layout, an accessor's by its getter, as data properties of dst
// (a key dst's layout has is the literal's own). The table is looked up
// again after each getter, which may grow it.
void __kml_shape_expando_spread(void *dst, void *src) {
    const KmlShape *ss = __kml_shape_of(src), *ds = __kml_shape_of(dst);
    for (i64 i = 0;; i++) {
        const Expando *x = expando_find(src, 0);
        if (!x || i >= x->n) return;
        const char *key = x->keys[i];
        if (ss && shadows_field(ss, key)) continue;
        if (!(xattrs(x, i) & XA_E) || ds && shadows_field(ds, key)) continue;
        i64 v = x->vals[i];
        if (xattrs(x, i) & XA_ACC) __kml_shape_expando_get_recv(src, key, 0, &v);
        __kml_shape_expando_set(dst, key, v);
    }
}

// Object.getOwnPropertyNames of a static object: its layout's keys, then
// every property added beside it, in the order they were added.
void __kml_shape_names_c(void *o, void **out) {
    __kml_shape_keys_array_c(o, out);
    const KmlShape *s = __kml_shape_of(o);
    const Expando *x = expando_find(o, 0);
    if (!s || !x) return;
    const char **data = (const char **)out[0];
    i64 n = (i64)(size_t)out[1], w = 0;
    data = kml_realloc(KML_HEAP_WORDS, data, (size_t)(n + x->n) * 8 + 8);
    for (i64 i = 0; i < n; i++) {
        i64 xi = expando_index(x, data[i]);
        if (xi < 0 || shadows_field(s, data[i])) data[w++] = data[i];
    }
    for (i64 i = 0; i < x->n; i++)
        if (x->keys[i][0] != 1 && !shadows_field(s, x->keys[i])) data[w++] = x->keys[i];
    out[0] = data;
    out[1] = (void *)(size_t)w;
}

// Object.getOwnPropertyDescriptor(o, key) of a boxed static object: a data
// descriptor for an own field, undefined otherwise. Object.freeze and
// Object.seal record a static object's integrity level in the frozen set
// (1 frozen, 2 sealed, 3 non-extensible).
extern void *__kml_dynobj_new(void) __attribute__((weak));
extern void __kml_dynobj_set(void *bag, const char *key, i64 value) __attribute__((weak));
extern i64 __kml_obj_level(void *o) __attribute__((weak));
extern i64 __kml_map_num_get(void *map, i64 key);

// A string value is a pointer past its 8-byte length header.
#define DESC_KEY(n, s) static const struct { i64 len; char s_[sizeof(s)]; } n = { KML_STR_ASCII_HDR(sizeof(s) - 1), s }
DESC_KEY(desc_value, "value");
DESC_KEY(desc_writable, "writable");
DESC_KEY(desc_enumerable, "enumerable");
DESC_KEY(desc_configurable, "configurable");
DESC_KEY(desc_get, "get");
DESC_KEY(desc_set, "set");

/* An object literal's own accessor function of kind ("get"/"set") for key,
 * through its layout's reserved "\x01@@get:k" / "\x01@@set:k" key; found
 * is 0 when it has none. */
static i64 own_accessor(void *obj, const char *kind, const char *key, int *found) {
    size_t n = strlen(key);
    char stack[96];
    char *probe = n + 10 <= sizeof stack ? stack : malloc(n + 10); /* scratch */
    if (!probe) { *found = 0; return KML_NB_UNDEFINED; }
    snprintf(probe, n + 10, "\x01@@%s:%s", kind, key);
    *found = 0;
    i64 v = __kml_shape_get(obj, probe, found);
    if (probe != stack) free(probe); /* scratch */
    return *found == 1 ? v : KML_NB_UNDEFINED;
}

/* propattr.c's attributes when the program defines a property; this weak
 * default (no attributes) stands in otherwise. */
__attribute__((weak)) i64 __kml_pattr_attrs(void *obj, const char *name) {
    (void)obj;
    (void)name;
    return 0;
}

/* Whether key is an object literal's own accessor property of obj: bit 1
 * a getter, bit 2 a setter, 0 for a data property. */
int __kml_obj_accessor(void *obj, const char *key) {
    if (!__kml_shape_of(obj)) return 0;
    const Expando *x = expando_find(obj, 0);
    i64 xi = expando_index(x, key);
    if (xi >= 0) {
        /* an accessor Object.defineProperty added */
        if (!(xattrs(x, xi) & XA_ACC)) return 0;
        void **pair = (void **)x->vals[xi];
        return (pair[0] != NULL) | (pair[1] != NULL) << 1;
    }
    int g = 0, s = 0;
    own_accessor(obj, "get", key, &g);
    own_accessor(obj, "set", key, &s);
    return (g == 1) | (s == 1) << 1;
}

i64 __kml_shape_own_desc(void *obj, const char *key) {
    const Expando *x = expando_find(obj, 0);
    i64 xi = expando_index(x, key);
    if (xi >= 0) {
        /* A property added beside the layout, with its own attributes. */
        int a = xattrs(x, xi);
        void *d = __kml_dynobj_new();
        if (a & XA_ACC) {
            void **pair = (void **)x->vals[xi];
            __kml_dynobj_set(d, desc_get.s_, pair[0] ? (i64)pair[0] | 7 : KML_NB_UNDEFINED);
            __kml_dynobj_set(d, desc_set.s_, pair[1] ? (i64)pair[1] | 7 : KML_NB_UNDEFINED);
        } else {
            __kml_dynobj_set(d, desc_value.s_, x->vals[xi]);
            __kml_dynobj_set(d, desc_writable.s_, a & XA_W ? KML_NB_TRUE : KML_NB_FALSE);
        }
        __kml_dynobj_set(d, desc_enumerable.s_, a & XA_E ? KML_NB_TRUE : KML_NB_FALSE);
        __kml_dynobj_set(d, desc_configurable.s_, a & XA_C ? KML_NB_TRUE : KML_NB_FALSE);
        return (i64)d | 5;
    }
    int found = 0;
    i64 v = __kml_shape_get(obj, key, &found);
    if (found != 1) return KML_NB_UNDEFINED;
    i64 level = __kml_obj_level(obj);
    /* Attributes Object.defineProperty gave the property (propattr.c,
     * linked only when the program defines one). */
    i64 at = __kml_pattr_attrs(obj, key);
    void *d = __kml_dynobj_new();
    int has_get = 0, has_set = 0;
    i64 g = own_accessor(obj, "get", key, &has_get);
    i64 st = own_accessor(obj, "set", key, &has_set);
    if (has_get || has_set) {
        /* An object literal's own accessor property. */
        __kml_dynobj_set(d, desc_get.s_, g);
        __kml_dynobj_set(d, desc_set.s_, st);
        __kml_dynobj_set(d, desc_enumerable.s_, (at & 2) ? KML_NB_FALSE : KML_NB_TRUE);
        __kml_dynobj_set(d, desc_configurable.s_, level == 1 || level == 2 || (at & 4) ? KML_NB_FALSE : KML_NB_TRUE);
        return (i64)d | 5;
    }
    __kml_dynobj_set(d, desc_value.s_, v);
    __kml_dynobj_set(d, desc_writable.s_, level == 1 || (at & 1) ? KML_NB_FALSE : KML_NB_TRUE);
    __kml_dynobj_set(d, desc_enumerable.s_, (at & 2) ? KML_NB_FALSE : KML_NB_TRUE);
    __kml_dynobj_set(d, desc_configurable.s_, level == 1 || level == 2 || (at & 4) ? KML_NB_FALSE : KML_NB_TRUE);
    return (i64)d | 5; // a dynamic object's box
}

// Object.getOwnPropertySymbols of a boxed value: a static object's
// symbol-keyed fields, then the symbol keys added to it; a dynamic object's
// symbol keys. out receives the { data, length } of the symbols.
extern i64 __kml_dynobj_count(void *bag) __attribute__((weak));
extern void *__kml_dynobj_key_at(void *bag, i64 i) __attribute__((weak));

static void *key_symbol(const char *k) {
    void *p = NULL;
    if (k && strncmp(k, "\x01@@sym:", 7) == 0) sscanf(k + 7, "%p", &p);
    return p;
}

extern _Bool __kml_dynobj_proxy_parts(void *o, void **target, void **handler) __attribute__((weak));
extern i64 __kml_dynobj_call_trap(void *rec, i64 thisv, i64 argc, i64 *argv) __attribute__((weak));
extern _Bool __kml_dynobj_flags_test(void *o, i64 mode) __attribute__((weak));
extern i64 __kml_dynobj_attrs_at(void *o, i64 i) __attribute__((weak));
extern i64 __kml_dynobj_get(void *o, const char *key) __attribute__((weak));
extern long long __kml_anyarr_get_by_key(void *box, const char *key) __attribute__((weak));
extern i64 __kml_dynarr_len(void *a);
extern i64 __kml_dynarr_at(void *a, i64 i);

#define OWNKEYS_ERR(n, s) static const struct { i64 len; char s_[sizeof(s)]; } n = { KML_STR_ASCII_HDR(sizeof(s) - 1), s }
OWNKEYS_ERR(err_nullish, "Cannot convert undefined or null to object");
OWNKEYS_ERR(err_nonobj, "CreateListFromArrayLike called on non-object");
OWNKEYS_ERR(err_badkey, "'ownKeys' on proxy: trap result elements must be strings or symbols");
OWNKEYS_ERR(err_dup, "'ownKeys' on proxy: trap returned duplicate entries");
OWNKEYS_ERR(err_missing, "'ownKeys' on proxy: trap result did not include a non-configurable key of the target");
OWNKEYS_ERR(err_nonext, "'ownKeys' on proxy: trap result must name exactly the keys of a non-extensible target");
OWNKEYS_ERR(key_length, "length");
OWNKEYS_ERR(key_ownKeys, "ownKeys");
OWNKEYS_ERR(key_gopd, "getOwnPropertyDescriptor");
OWNKEYS_ERR(key_get, "get");
OWNKEYS_ERR(key_set, "set");
OWNKEYS_ERR(key_value, "value");
OWNKEYS_ERR(key_writable, "writable");
OWNKEYS_ERR(key_enumerable, "enumerable");
OWNKEYS_ERR(key_configurable, "configurable");

void __kml_own_symbols_c(i64 boxed, void **out);

// The property key a boxed ownKeys trap element names: a string as itself, a
// symbol as its "\x01@@sym:%p" key; NULL for anything else.
static const char *trap_key(i64 w) {
    unsigned long long v = (unsigned long long)w;
    if (v < 65536 || v >= (1ULL << 49)) return NULL;
    if ((v & 7) == 0) return (const char *)v;
    if ((v & 7) == 1 && (*(i64 *)(v & ~7ULL) & (1LL << 47))) return symbol_key((void *)(v & ~7ULL));
    return NULL;
}

static int has_key(const char **ks, i64 n, const char *k) {
    for (i64 i = 0; i < n; i++)
        if (strcmp(ks[i], k) == 0) return 1;
    return 0;
}

extern char **__kml_dynobj_keys_c(void *o, i64 *n) __attribute__((weak));
extern _Bool __kml_key_is_symbol(const char *k) __attribute__((weak));
extern i64 __kml_dynobj_own_desc(void *o, const char *key) __attribute__((weak));
extern unsigned char __kml_any_tobool(i64 v);
extern _Bool __kml_dynobj_has_chain(void *o, const char *key) __attribute__((weak));
extern void __kml_throw_nullderef(const char *msg) __attribute__((noreturn));

static int is_proxy(void *bag, void **target, void **handler) {
    return __kml_dynobj_proxy_parts(bag, target, handler);
}

// A boxed array (a static one's box or a dynamic one): its length, its
// element i.
static int is_array_like(unsigned long long r) {
    return r >= 65536 && r < (1ULL << 49) && ((r & 7) == 2 || (r & 7) == 6);
}
static i64 array_like_len(unsigned long long r) {
    void *arr = (void *)(r & ~7ULL);
    if ((r & 7) == 6) return __kml_dynarr_len(arr);
    unsigned long long lw = (unsigned long long)__kml_anyarr_get_by_key(arr, key_length.s_);
    double d;
    lw -= 1ULL << 49;
    memcpy(&d, &lw, 8);
    return (i64)d;
}
static i64 array_like_at(unsigned long long r, i64 i) {
    void *arr = (void *)(r & ~7ULL);
    if ((r & 7) == 6) return __kml_dynarr_at(arr, i);
    char idx[24];
    snprintf(idx, sizeof idx, "%lld", i);
    return __kml_anyarr_get_by_key(arr, idx);
}

// A Proxy's [[OwnPropertyKeys]]: its ownKeys trap's result, which must be an
// array-like of distinct strings and symbols that keeps the target's
// invariants (every non-configurable key; exactly the keys of a
// non-extensible target), else its target's own keys (string keys in ES
// order, then symbols). Keys come back as bag keys (a symbol as its
// "\x01@@sym:%p"). Returns the TypeError message, or NULL with *ks_out and
// *n_out set.
const char *__kml_proxy_own_keys(void *px, const char ***ks_out, i64 *n_out) {
    void *target, *handler;
    if (!is_proxy(px, &target, &handler)) {
        i64 m;
        char **names = __kml_dynobj_keys_c(px, &m);
        i64 c = __kml_dynobj_count(px);
        const char **ks = kml_alloc(KML_HEAP_WORDS, (size_t)(m + c + 1) * sizeof *ks);
        i64 n = 0;
        for (i64 i = 0; i < m; i++) ks[n++] = names[i];
        for (i64 i = 0; i < c; i++) {
            const char *k = (const char *)__kml_dynobj_key_at(px, i);
            if (__kml_key_is_symbol(k)) ks[n++] = k;
        }
        *ks_out = ks;
        *n_out = n;
        return NULL;
    }
    i64 trap = __kml_dynobj_get(handler, key_ownKeys.s_);
    unsigned long long tw = (unsigned long long)trap;
    if (tw < 65536 || tw >= (1ULL << 49) || (tw & 7) != 7) return __kml_proxy_own_keys(target, ks_out, n_out);
    i64 argv[1] = {(i64)target | 5};
    unsigned long long r = (unsigned long long)__kml_dynobj_call_trap((void *)(tw & ~7ULL), (i64)handler | 5, 1, argv);
    if (!is_array_like(r)) return err_nonobj.s_;
    i64 n = array_like_len(r);
    const char **ks = kml_alloc(KML_HEAP_WORDS, (size_t)(n ? n : 1) * sizeof *ks);
    for (i64 i = 0; i < n; i++) {
        const char *k = trap_key(array_like_at(r, i));
        if (!k) return err_badkey.s_;
        if (has_key(ks, i, k)) return err_dup.s_;
        ks[i] = k;
    }
    _Bool extensible = __kml_dynobj_flags_test(target, 0);
    i64 tc = __kml_dynobj_count(target);
    for (i64 i = 0; i < tc; i++) {
        const char *k = (const char *)__kml_dynobj_key_at(target, i);
        int configurable = (__kml_dynobj_attrs_at(target, i) & 4) != 0;
        if ((!configurable || !extensible) && !has_key(ks, n, k))
            return configurable ? err_nonext.s_ : err_missing.s_;
    }
    if (!extensible)
        for (i64 i = 0; i < n; i++) {
            int found = 0;
            for (i64 j = 0; j < tc && !found; j++)
                found = strcmp(ks[i], (const char *)__kml_dynobj_key_at(target, j)) == 0;
            if (!found) return err_nonext.s_;
        }
    *ks_out = ks;
    *n_out = n;
    return NULL;
}

// A property of a boxed object a trap returned: a dynamic object's through
// its own get, a static object's through its layout; undefined otherwise.
static i64 trap_result_get(i64 r, const char *key, int *has) {
    unsigned long long v = (unsigned long long)r;
    *has = 0;
    if (v < 65536 || v >= (1ULL << 49)) return KML_NB_UNDEFINED;
    void *p = (void *)(v & ~7ULL);
    if ((v & 7) == 5) {
        *has = __kml_dynobj_has_chain(p, key);
        return __kml_dynobj_get(p, key);
    }
    if ((v & 7) == 1) {
        int found = 0;
        i64 w = __kml_shape_get(p, key, &found);
        *has = found;
        return found ? w : KML_NB_UNDEFINED;
    }
    return KML_NB_UNDEFINED;
}

OWNKEYS_ERR(err_gopd_ret, "'getOwnPropertyDescriptor' on proxy: trap returned neither object nor undefined for property '");

OWNKEYS_ERR(err_then_nofn, "then is not a function");

// Promise.prototype.catch/finally on a receiver that is not a promise:
// Invoke(receiver, "then", a, b), with thenv the receiver's `then`.
i64 __kml_invoke_then(i64 thisv, i64 thenv, i64 a, i64 b) {
    unsigned long long tw = (unsigned long long)thenv;
    if (tw < 65536 || tw >= (1ULL << 49) || (tw & 7) != 7) __kml_throw_nullderef(err_then_nofn.s_);
    i64 argv[2] = {a, b};
    return __kml_dynobj_call_trap((void *)(tw & ~7ULL), thisv, 2, argv);
}

OWNKEYS_ERR(key_defprop, "defineProperty");
OWNKEYS_ERR(err_defprop_false, "'defineProperty' on proxy: trap returned falsish for property '");

// A Proxy's [[DefineOwnProperty]]: its defineProperty trap called with
// (target, key, desc), a falsish result a TypeError; without a trap, on to
// its target. Returns the bag the define applies to, or NULL when a trap
// handled it.
void *__kml_proxy_define(void *bag, const char *key, void *desc) {
    void *target, *handler;
    while (bag && is_proxy(bag, &target, &handler)) {
        i64 trap = __kml_dynobj_get(handler, key_defprop.s_);
        unsigned long long tw = (unsigned long long)trap;
        if (tw < 65536 || tw >= (1ULL << 49) || (tw & 7) != 7) {
            bag = target;
            continue;
        }
        void *sym = key_symbol(key);
        i64 argv[3] = {(i64)target | 5, sym ? (i64)sym | 1 : (i64)key, (i64)desc | 5};
        i64 r = __kml_dynobj_call_trap((void *)(tw & ~7ULL), (i64)handler | 5, 3, argv);
        if (!(__kml_any_tobool(r) & 1)) {
            const char *k = sym ? "Symbol()" : key;
            size_t a = strlen(err_defprop_false.s_), b = strlen(k);
            char *base = kml_alloc(KML_HEAP_RAW, a + b + 10);
            *(i64 *)base = (i64)(a + b + 1);
            snprintf(base + 8, a + b + 2, "%s%s'", err_defprop_false.s_, k);
            __kml_throw_nullderef(base + 8);
        }
        return NULL;
    }
    return bag;
}

// A Proxy's [[GetOwnProperty]] as a descriptor object: its
// getOwnPropertyDescriptor trap's result, completed
// (ToPropertyDescriptor, CompletePropertyDescriptor) into a new object, or
// its target's. Undefined for no such property.
i64 __kml_proxy_own_desc(void *px, const char *key) {
    void *target, *handler;
    if (!is_proxy(px, &target, &handler)) return __kml_dynobj_own_desc(px, key);
    i64 trap = __kml_dynobj_get(handler, key_gopd.s_);
    unsigned long long tw = (unsigned long long)trap;
    if (tw < 65536 || tw >= (1ULL << 49) || (tw & 7) != 7) return __kml_proxy_own_desc(target, key);
    void *sym = key_symbol(key);
    i64 argv[2] = {(i64)target | 5, sym ? (i64)sym | 1 : (i64)key};
    i64 r = __kml_dynobj_call_trap((void *)(tw & ~7ULL), (i64)handler | 5, 2, argv);
    unsigned long long rv = (unsigned long long)r;
    if (rv == KML_NB_UNDEFINED) return KML_NB_UNDEFINED;
    if (rv < 65536 || rv >= (1ULL << 49) || ((rv & 7) != 5 && (rv & 7) != 1)) {
        size_t a = strlen(err_gopd_ret.s_), b = strlen(sym ? "Symbol()" : key);
        char *base = kml_alloc(KML_HEAP_RAW, a + b + 10);
        *(i64 *)base = (i64)(a + b + 1);
        snprintf(base + 8, a + b + 2, "%s%s'", err_gopd_ret.s_, sym ? "Symbol()" : key);
        __kml_throw_nullderef(base + 8);
    }
    void *desc = __kml_dynobj_new();
    int has, accessor = 0;
    i64 g = trap_result_get(r, key_get.s_, &has);
    if (has) { __kml_dynobj_set(desc, key_get.s_, g); accessor = 1; }
    i64 st = trap_result_get(r, key_set.s_, &has);
    if (has) { __kml_dynobj_set(desc, key_set.s_, st); accessor = 1; }
    if (accessor) {
        int hg, hs;
        trap_result_get(r, key_get.s_, &hg);
        trap_result_get(r, key_set.s_, &hs);
        if (!hg) __kml_dynobj_set(desc, key_get.s_, KML_NB_UNDEFINED);
        if (!hs) __kml_dynobj_set(desc, key_set.s_, KML_NB_UNDEFINED);
    } else {
        i64 val = trap_result_get(r, key_value.s_, &has);
        __kml_dynobj_set(desc, key_value.s_, has ? val : KML_NB_UNDEFINED);
        i64 w = trap_result_get(r, key_writable.s_, &has);
        __kml_dynobj_set(desc, key_writable.s_, (has && (__kml_any_tobool(w) & 1)) ? 7 : 6);
    }
    i64 en = trap_result_get(r, key_enumerable.s_, &has);
    __kml_dynobj_set(desc, key_enumerable.s_, (has && (__kml_any_tobool(en) & 1)) ? 7 : 6);
    i64 cf = trap_result_get(r, key_configurable.s_, &has);
    __kml_dynobj_set(desc, key_configurable.s_, (has && (__kml_any_tobool(cf) & 1)) ? 7 : 6);
    return (i64)desc | 5;
}

// Whether a Proxy's own property key is enumerable ([[GetOwnProperty]]):
// 1 enumerable, 0 not, -1 absent.
int __kml_proxy_own_enum(void *px, const char *key) {
    i64 d = __kml_proxy_own_desc(px, key);
    if ((unsigned long long)d == KML_NB_UNDEFINED) return -1;
    return __kml_any_tobool(__kml_dynobj_get((void *)((unsigned long long)d & ~7ULL), key_enumerable.s_)) & 1;
}

// Object.getOwnPropertySymbols with the spec's checks: a null or undefined
// argument throws, and a Proxy runs its [[OwnPropertyKeys]]. Returns the
// TypeError message, or NULL with out filled as __kml_own_symbols_c fills it.
const char *__kml_own_symbols_checked(i64 boxed, void **out) {
    unsigned long long v = (unsigned long long)boxed;
    if (v == 2 || v == KML_NB_UNDEFINED) return err_nullish.s_;
    void *target, *handler;
    if (v < 65536 || v >= (1ULL << 49) || (v & 7) != 5 ||
        !is_proxy((void *)(v & ~7ULL), &target, &handler)) {
        __kml_own_symbols_c(boxed, out);
        return NULL;
    }
    const char **ks;
    i64 n;
    const char *err = __kml_proxy_own_keys((void *)(v & ~7ULL), &ks, &n);
    if (err) return err;
    void **data = kml_alloc(KML_HEAP_WORDS, (size_t)(n ? n : 1) * sizeof *data);
    i64 m = 0;
    for (i64 i = 0; i < n; i++) {
        void *sym = key_symbol(ks[i]);
        if (sym) data[m++] = sym;
    }
    out[0] = data;
    out[1] = (void *)(size_t)m;
    return NULL;
}

// Object rest (`{ a, ...r } = src`) of a Proxy source: CopyDataProperties,
// its [[OwnPropertyKeys]] once, then for each key outside excl (a boxed
// array of keys) its [[GetOwnProperty]] and, when enumerable, its [[Get]].
// Returns 0 for a source that is not a Proxy, else the boxed new object;
// a broken ownKeys trap throws.
i64 __kml_proxy_obj_rest(i64 src, i64 excl) {
    unsigned long long v = (unsigned long long)src;
    void *target, *handler;
    if (v < 65536 || v >= (1ULL << 49) || (v & 7) != 5) return 0;
    void *px = (void *)(v & ~7ULL);
    if (!is_proxy(px, &target, &handler)) return 0;
    const char **ks;
    i64 n;
    const char *err = __kml_proxy_own_keys(px, &ks, &n);
    if (err) {
        size_t len = strlen(err);
        char *base = kml_alloc(KML_HEAP_RAW, len + 9);
        *(i64 *)base = (i64)len;
        memcpy(base + 8, err, len + 1);
        __kml_throw_nullderef(base + 8);
    }
    i64 ne = is_array_like((unsigned long long)excl) ? array_like_len((unsigned long long)excl) : 0;
    void *out = __kml_dynobj_new();
    for (i64 i = 0; i < n; i++) {
        int skip = 0;
        for (i64 j = 0; j < ne && !skip; j++) {
            const char *x = trap_key(array_like_at((unsigned long long)excl, j));
            skip = x && strcmp(x, ks[i]) == 0;
        }
        if (skip || __kml_proxy_own_enum(px, ks[i]) != 1) continue;
        __kml_dynobj_set(out, ks[i], __kml_dynobj_get(px, ks[i]));
    }
    return (i64)out | 5;
}

// Reflect.ownKeys of a Proxy: its [[OwnPropertyKeys]] once, each key boxed
// (a string as its pointer, a symbol with kind 1). Returns 0 for a value
// that is not a Proxy (out untouched), else 1 with out filled or *err set.
int __kml_proxy_reflect_own_keys(i64 boxed, void **out, const char **err) {
    unsigned long long v = (unsigned long long)boxed;
    void *target, *handler;
    *err = NULL;
    if (v < 65536 || v >= (1ULL << 49) || (v & 7) != 5 || !is_proxy((void *)(v & ~7ULL), &target, &handler)) return 0;
    const char **ks;
    i64 n;
    if ((*err = __kml_proxy_own_keys((void *)(v & ~7ULL), &ks, &n))) return 1;
    i64 *data = kml_alloc(KML_HEAP_WORDS, (size_t)(n ? n : 1) * sizeof(i64));
    for (i64 i = 0; i < n; i++) {
        void *sym = key_symbol(ks[i]);
        data[i] = sym ? (i64)((size_t)sym | 1) : (i64)(size_t)ks[i];
    }
    out[0] = data;
    out[1] = (void *)(size_t)n;
    return 1;
}

static void own_symbols(i64 boxed, void **out, int enum_only) {
    unsigned long long v = (unsigned long long)boxed;
    void **data = NULL;
    i64 n = 0, cap = 0;
#define PUSH(p)                                                     \
    do {                                                            \
        if (n == cap) {                                             \
            cap = cap ? cap * 2 : 4;                                \
            data = kml_realloc(KML_HEAP_WORDS, data, (size_t)cap * sizeof(void *));     \
        }                                                           \
        data[n++] = (p);                                            \
    } while (0)
    if (v >= 65536 && v < (1ULL << 49)) {
        void *p = (void *)(v & ~7ULL);
        if ((v & 7) == 1) {
            const KmlShape *s = __kml_shape_of(p);
            if (s && s->syms)
                for (i64 i = 0; i < s->nkeys; i++)
                    if (s->syms[i]) PUSH(*s->syms[i]);
            const Expando *x = expando_find(p, 0);
            for (i64 i = 0; x && i < x->n; i++) {
                void *sym = key_symbol(x->keys[i]);
                if (sym) PUSH(sym);
            }
        } else if ((v & 7) == 2 || (v & 7) == 6) {
            /* an array's added properties, keyed by its header (a boxed
             * static array's first word) or by the dynamic array itself */
            void *arr = (v & 7) == 2 ? *(void **)p : p;
            const Expando *x = expando_find(arr, 0);
            for (i64 i = 0; x && i < x->n; i++) {
                void *sym = key_symbol(x->keys[i]);
                if (sym) PUSH(sym);
            }
        } else if ((v & 7) == 5) {
            i64 c = __kml_dynobj_count(p);
            for (i64 i = 0; i < c; i++) {
                void *sym = key_symbol((const char *)__kml_dynobj_key_at(p, i));
                /* attribute bit 2 marks an enumerable property */
                if (sym && (!enum_only || (__kml_dynobj_attrs_at(p, i) & 2))) PUSH(sym);
            }
        }
    }
#undef PUSH
    out[0] = data;
    out[1] = (void *)(size_t)n;
}

void __kml_own_symbols_c(i64 boxed, void **out) { own_symbols(boxed, out, 0); }

// The symbols of a boxed value's own enumerable symbol-keyed properties, the
// ones Object.assign copies and Object.defineProperties reads.
void __kml_own_enum_symbols_c(i64 boxed, void **out) { own_symbols(boxed, out, 1); }

// Reflect.ownKeys: the string keys, then the symbols, each boxed (a string
// boxes as its pointer, a symbol as an object: its pointer with kind 1).
// The IR passes each array as a { ptr, i64 } value, which LLVM flattens into
// its two fields on every target; a C struct parameter would match only the
// System V ABI (Windows x64 passes a 16-byte struct by reference).
void __kml_own_keys_c(void **names, i64 names_len, void **syms, i64 syms_len, void **out) {
    i64 n = names_len + syms_len;
    i64 *data = kml_alloc(KML_HEAP_WORDS, (size_t)(n ? n : 1) * sizeof(i64));
    for (i64 i = 0; i < names_len; i++) data[i] = (i64)(size_t)names[i];
    for (i64 i = 0; i < syms_len; i++) data[names_len + i] = (i64)((size_t)syms[i] | 1);
    out[0] = data;
    out[1] = (void *)(size_t)n;
}
