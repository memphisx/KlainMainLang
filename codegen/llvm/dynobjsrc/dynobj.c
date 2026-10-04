/* dynobj.c — the D1 dynamic object runtime (TDD-00155; in C per TDD-00240):
 * a per-instance property bag behind box tag 10. Layout (kept in sync with
 * dynjson.c, which walks it directly):
 *   header (48 bytes): i64 flags (low 32 bits magic "KMLD"; bit 32
 *     non-extensible, bit 33 Proxy, bit 34 NULLPROTO), ptr proto, ptr props,
 *     i64 count, i64 cap, i64 classtag.
 *   entry (32 bytes): ptr key (owned, length-prefixed copy), i64 tag, i64
 *     payload, i64 attrs (WRITABLE 1 | ENUMERABLE 2 | CONFIGURABLE 4 |
 *     ACCESSOR 8). An accessor's payload is a ptr to {getterRec, setterRec}.
 * A Proxy header reuses offsets 8/16 as target/handler. The entry array is
 * insertion-ordered (JS key order); lookup is a linear strcmp scan. All
 * memory is plain calloc/malloc/realloc for the -mm=gc shim. */
#include <stdlib.h>
#include <string.h>

typedef long long i64;

typedef struct {
    char *key;
    i64 tag, pay, attrs;
} Prop;

typedef struct {
    i64 flags;
    void *proto;
    Prop *props;
    i64 count, cap, classtag;
} Bag;

#define F_NONEXT (1LL << 32)
#define F_PROXY (1LL << 33)
#define F_NULLPROTO (1LL << 34)
#define A_WRITABLE 1
#define A_ENUM 2
#define A_CONFIG 4
#define A_ACCESSOR 8

/* Proxy view of a header: target at +8, handler at +16. */
#define PX_TARGET(o) (((Bag *)(o))->proto)
#define PX_HANDLER(o) ((void *)((Bag *)(o))->props)

/* nb_* mirror __kml_nb_pack/tag/pay (runtime_nanbox.go — keep in sync). */
static i64 nb_double(double d) {
    unsigned long long bits;
    if (d != d) bits = 0x7FF8000000000000ULL;
    else memcpy(&bits, &d, 8);
    return (i64)(bits + (1ULL << 49));
}
static i64 nb_pack(i64 tag, i64 pay) {
    switch (tag) {
    case 0: return nb_double((double)pay);
    case 1: { double d; memcpy(&d, &pay, 8); return nb_double(d); }
    case 3: return pay ? 7 : 6;
    case 4: return 2;
    case 5: return 10;
    case 2: return pay;
    case 6: return pay | 1;
    case 7: return pay | 2;
    case 8: return pay | 3;
    case 9: return pay | 4;
    case 10: return pay | 5;
    case 11: return pay | 6;
    case 12: return pay | 7;
    default: return 10;
    }
}
static i64 nb_tag(i64 word) {
    unsigned long long v = (unsigned long long)word;
    static const i64 kindTag[8] = {2, 6, 7, 8, 9, 10, 11, 12};
    if (v >= (1ULL << 49)) return 1;
    if (v < 65536) return v == 2 ? 4 : (v == 6 || v == 7) ? 3 : 5;
    return kindTag[v & 7];
}
static i64 nb_pay(i64 word) {
    unsigned long long v = (unsigned long long)word;
    if (v >= (1ULL << 49)) return (i64)(v - (1ULL << 49));
    if (v < 65536) return v == 7;
    return (v & 7) == 0 ? (i64)v : (i64)(v & ~7ULL);
}

/* A dynamic-function record {fn, env}, called as fn(env, this, argc, argv). */
typedef i64 (*dynfn)(void *env, i64 thisv, i64 argc, i64 *argv);
static i64 call_rec(void *rec, i64 thisv, i64 argc, i64 *argv) {
    return (*(dynfn *)rec)(((void **)rec)[1], thisv, argc, argv);
}

/* __kml_any_tobool is generated (it measures strings through the string
 * runtime); returns i1. */
extern unsigned char __kml_any_tobool(i64 v);
static int tobool(i64 v) { return __kml_any_tobool(v) & 1; }

#define BOX_DYNOBJ(p) ((i64)(p) | 5)

void *__kml_dynobj_new(void) {
    Bag *o = (Bag *)calloc(1, sizeof(Bag));
    o->flags = 0x444C4D4BLL;
    return o;
}

/* A bag realized from a class instance records the class's instanceof TagID
 * (ADR-00990; 0 = not from a class). */
void __kml_dynobj_set_classtag(void *o, i64 tag) { ((Bag *)o)->classtag = tag; }
i64 __kml_dynobj_classtag(void *o) { return ((Bag *)o)->classtag; }

i64 __kml_dynobj_find(void *o, const char *key) {
    Bag *b = (Bag *)o;
    for (i64 i = 0; i < b->count; i++)
        if (strcmp(b->props[i].key, key) == 0) return i;
    return -1;
}

/* v is a NaN-boxed word; entries keep the decoded (tag, payload) pair so
 * dynjson.c's walkers stay unchanged. */
void __kml_dynobj_set(void *o, const char *key, i64 v) {
    Bag *b = (Bag *)o;
    i64 tag = nb_tag(v), pay = nb_pay(v);
    i64 idx = __kml_dynobj_find(o, key);
    if (idx >= 0) {
        b->props[idx].tag = tag;
        b->props[idx].pay = pay;
        return;
    }
    if (b->count >= b->cap) {
        i64 nc = b->cap == 0 ? 8 : b->cap * 2;
        b->props = (Prop *)realloc(b->props, (size_t)nc * sizeof(Prop));
        b->cap = nc;
    }
    /* key copy is length-prefixed like every heap string (TDD-00120): i64 len
     * at base, data at base+8; consumers read the header at key-8. */
    size_t klen = strlen(key);
    char *base = (char *)malloc(klen + 9);
    *(i64 *)base = (i64)klen;
    memcpy(base + 8, key, klen + 1);
    Prop *p = &b->props[b->count];
    p->key = base + 8;
    p->tag = tag;
    p->pay = pay;
    p->attrs = A_WRITABLE | A_ENUM | A_CONFIG;
    b->count++;
}

i64 __kml_dynobj_attrs_at(void *o, i64 i) { return ((Bag *)o)->props[i].attrs; }
i64 __kml_dynobj_rawtag_at(void *o, i64 i) { return ((Bag *)o)->props[i].tag; }
i64 __kml_dynobj_rawpay_at(void *o, i64 i) { return ((Bag *)o)->props[i].pay; }
i64 __kml_dynobj_get_at(void *o, i64 i) {
    Prop *p = &((Bag *)o)->props[i];
    return nb_pack(p->tag, p->pay);
}

/* get walks the prototype chain (acyclic: set_proto refuses cycles). An
 * ACCESSOR hit calls its getter with the original receiver as this; a
 * getter-less accessor reads undefined. A Proxy runs its "get" trap as
 * trap(target, key, receiver) with this=handler, else forwards. */
i64 __kml_dynobj_get(void *o, const char *key) {
    if (((Bag *)o)->flags & F_PROXY) {
        void *target = PX_TARGET(o), *handler = PX_HANDLER(o);
        i64 trap = __kml_dynobj_get(handler, "get");
        if (nb_tag(trap) != 12) return __kml_dynobj_get(target, key);
        i64 argv[3] = {BOX_DYNOBJ(target), (i64)key, BOX_DYNOBJ(o)};
        return call_rec((void *)nb_pay(trap), BOX_DYNOBJ(handler), 3, argv);
    }
    for (void *cur = o; cur; cur = ((Bag *)cur)->proto) {
        i64 idx = __kml_dynobj_find(cur, key);
        if (idx < 0) continue;
        if (!(__kml_dynobj_attrs_at(cur, idx) & A_ACCESSOR)) return __kml_dynobj_get_at(cur, idx);
        void **pair = (void **)__kml_dynobj_rawpay_at(cur, idx);
        if (!pair[0]) return 10;
        return call_rec(pair[0], BOX_DYNOBJ(o), 0, NULL);
    }
    return 10;
}

/* setv — the checked assignment path. 0 ok, 1 read-only, 2 not extensible,
 * 3 accessor without a setter. An inherited accessor's setter runs with the
 * original receiver; an inherited read-only data property blocks the write;
 * an inherited writable one shadows as an own define, as does a miss
 * (extensibility permitting). */
i64 __kml_dynobj_setv(void *o, const char *key, i64 v) {
    Bag *ob = (Bag *)o;
    if (ob->flags & F_PROXY) {
        void *target = PX_TARGET(o), *handler = PX_HANDLER(o);
        i64 trap = __kml_dynobj_get(handler, "set");
        if (nb_tag(trap) != 12) return __kml_dynobj_setv(target, key, v);
        i64 argv[4] = {BOX_DYNOBJ(target), (i64)key, v, BOX_DYNOBJ(o)};
        return tobool(call_rec((void *)nb_pay(trap), BOX_DYNOBJ(handler), 4, argv)) ? 0 : 1;
    }
    for (void *cur = o; cur; cur = ((Bag *)cur)->proto) {
        i64 idx = __kml_dynobj_find(cur, key);
        if (idx < 0) continue;
        i64 attrs = __kml_dynobj_attrs_at(cur, idx);
        if (attrs & A_ACCESSOR) {
            void **pair = (void **)__kml_dynobj_rawpay_at(cur, idx);
            if (!pair[1]) return 3;
            i64 argv[1] = {v};
            call_rec(pair[1], BOX_DYNOBJ(o), 1, argv);
            return 0;
        }
        if (!(attrs & A_WRITABLE)) return 1;
        if (cur == o) {
            Prop *p = &ob->props[idx];
            p->tag = nb_tag(v);
            p->pay = nb_pay(v);
            return 0;
        }
        break;
    }
    /* defown: a non-extensible object may only update an existing own
     * writable property (handled above), so a miss here is a rejection. */
    if (ob->flags & F_NONEXT) return 2;
    __kml_dynobj_set(o, key, v);
    return 0;
}

/* has = own-only (Object.hasOwn / hasOwnProperty). */
_Bool __kml_dynobj_has(void *o, const char *key) { return __kml_dynobj_find(o, key) >= 0; }

/* __kml_dynobj_ordinary: whether o's prototype chain ends at the (unmodeled)
 * Object.prototype rather than an explicit null — where an own-less
 * `constructor` read finds Object. */
_Bool __kml_dynobj_ordinary(void *o) {
    void *last = o;
    for (void *cur = o; cur; cur = ((Bag *)cur)->proto) last = cur;
    return (((Bag *)last)->flags & F_NULLPROTO) == 0;
}

/* has_chain = the "in" operator: own table, then the prototype chain. A
 * Proxy consults its "has" trap, else forwards to the target. */
_Bool __kml_dynobj_has_chain(void *o, const char *key) {
    if (((Bag *)o)->flags & F_PROXY) {
        void *target = PX_TARGET(o), *handler = PX_HANDLER(o);
        i64 trap = __kml_dynobj_get(handler, "has");
        if (nb_tag(trap) != 12) return __kml_dynobj_has_chain(target, key);
        i64 argv[2] = {BOX_DYNOBJ(target), (i64)key};
        return tobool(call_rec((void *)nb_pay(trap), BOX_DYNOBJ(handler), 2, argv));
    }
    for (void *cur = o; cur; cur = ((Bag *)cur)->proto)
        if (__kml_dynobj_has(cur, key)) return 1;
    return 0;
}

/* set_proto refuses a cycle (returns false), keeping every chain acyclic so
 * the get/has walks need no visited set. proto may be null. */
_Bool __kml_dynobj_set_proto(void *o, void *proto) {
    for (void *cur = proto; cur; cur = ((Bag *)cur)->proto)
        if (cur == o) return 0;
    Bag *b = (Bag *)o;
    b->proto = proto;
    /* NULLPROTO (TDD-00229): a prototype explicitly null, as distinct from an
     * ordinary object whose Object.prototype link is not modeled (proto slot
     * also null). Cleared when a real prototype is set. */
    if (proto) b->flags &= ~F_NULLPROTO;
    else b->flags |= F_NULLPROTO;
    return 1;
}

/* Object.prototype: one shared bag whose own prototype is null (renders as
 * [Object: null prototype] {}, as in Node). Created on first use. */
static void *objproto;
void *__kml_object_prototype(void) {
    if (!objproto) {
        void *b = __kml_dynobj_new();
        __kml_dynobj_set_proto(b, NULL);
        objproto = b;
    }
    return objproto;
}

void *__kml_dynobj_get_proto(void *o) {
    Bag *b = (Bag *)o;
    if (b->proto) return b->proto;
    /* An ordinary object's [[Prototype]] is Object.prototype, whose chain is
     * not linked into every bag; only a NULLPROTO or Proxy bag answers null
     * (TDD-00229). */
    if (b->flags & (F_PROXY | F_NULLPROTO)) return NULL;
    return __kml_object_prototype();
}

_Bool __kml_dynobj_delete(void *o, const char *key) {
    Bag *b = (Bag *)o;
    if (b->flags & F_PROXY) {
        void *target = PX_TARGET(o), *handler = PX_HANDLER(o);
        i64 trap = __kml_dynobj_get(handler, "deleteProperty");
        if (nb_tag(trap) != 12) return __kml_dynobj_delete(target, key);
        i64 argv[2] = {BOX_DYNOBJ(target), (i64)key};
        return tobool(call_rec((void *)nb_pay(trap), BOX_DYNOBJ(handler), 2, argv));
    }
    i64 idx = __kml_dynobj_find(o, key);
    if (idx < 0) return 1; /* deleting a missing property is still true */
    /* a non-configurable property refuses deletion; the strict-JS caller
     * surfaces that as its TypeError. */
    if (!(b->props[idx].attrs & A_CONFIG)) return 0;
    memmove(&b->props[idx], &b->props[idx + 1], (size_t)(b->count - idx - 1) * sizeof(Prop));
    b->count--;
    return 1;
}

i64 __kml_dynobj_count(void *o) { return ((Bag *)o)->count; }
char *__kml_dynobj_key_at(void *o, i64 i) { return ((Bag *)o)->props[i].key; }

/* The canonical array-index value a string key denotes, or -1: the canonical
 * decimal of an integer in [0, 2^32-1) — digits only, no leading zero
 * (except "0"), below 4294967295. Drives own-property enumeration order. */
i64 __kml_dynobj_is_index(const char *key) {
    size_t len = strlen(key);
    if (len == 0 || len > 10) return -1;
    if (key[0] == '0' && len > 1) return -1;
    i64 acc = 0;
    for (size_t i = 0; i < len; i++) {
        if (key[i] < '0' || key[i] > '9') return -1;
        acc = acc * 10 + (key[i] - '0');
    }
    return acc < 4294967295LL ? acc : -1;
}

/* Reorder the first n key pointers into ES own-property order in place:
 * array-index keys ascending, then every other key in insertion order. */
void __kml_dynobj_es_reorder(char **arr, i64 n) {
    if (n <= 1) return;
    char **scratch = (char **)malloc((size_t)n * sizeof(char *));
    i64 *idxv = (i64 *)malloc((size_t)n * sizeof(i64));
    i64 w = 0;
    for (i64 i = 0; i < n; i++) idxv[i] = __kml_dynobj_is_index(arr[i]);
    for (;;) { /* emit the smallest not-yet-taken index key */
        i64 minpos = -1;
        for (i64 i = 0; i < n; i++)
            if (idxv[i] >= 0 && (minpos < 0 || idxv[i] < idxv[minpos])) minpos = i;
        if (minpos < 0) break;
        scratch[w++] = arr[minpos];
        idxv[minpos] = -2;
    }
    for (i64 i = 0; i < n; i++)
        if (idxv[i] == -1) scratch[w++] = arr[i];
    memcpy(arr, scratch, (size_t)n * sizeof(char *));
    free(scratch);
    free(idxv);
}

/* Whether a bag key is a symbol's: a Symbol's "\x01@@sym:%p" key or a
 * well-known symbol's "@@x" (emitSymbolPropertyKey) — string enumerations
 * skip it. */
_Bool __kml_key_is_symbol(const char *k) {
    /* Mirrors wellKnownMemberKeys (runtime_dynobj.go). */
    static const char *const wk[] = {"@@iterator", "@@asyncIterator", "@@toPrimitive", "@@toStringTag", "@@dispose", "@@asyncDispose"};
    if (k[0] == 1) return 1;
    if (k[0] != '@' || k[1] != '@') return 0;
    for (size_t i = 0; i < sizeof wk / sizeof wk[0]; i++)
        if (strcmp(k, wk[i]) == 0) return 1;
    return 0;
}

/* The key list behind getOwnPropertyNames (enumerable_only = 0: every own
 * string key) and Object.keys / for...in / JSON (1: enumerable string keys
 * only), in ES order; symbol keys are never returned. Returns the array and
 * its length in *n (the IR wrappers build the {ptr,i64}). */
static char **keys_of(void *o, i64 *n, int enumerable_only) {
    Bag *b = (Bag *)o;
    char **arr = (char **)malloc((size_t)b->count * sizeof(char *));
    i64 w = 0;
    for (i64 i = 0; i < b->count; i++) {
        if (enumerable_only && !(b->props[i].attrs & A_ENUM)) continue;
        if (__kml_key_is_symbol(b->props[i].key)) continue;
        arr[w++] = b->props[i].key;
    }
    __kml_dynobj_es_reorder(arr, w);
    *n = w;
    return arr;
}
char **__kml_dynobj_keys_c(void *o, i64 *n) { return keys_of(o, n, 0); }
char **__kml_dynobj_keys_enum_c(void *o, i64 *n) { return keys_of(o, n, 1); }

/* merge backs object spread: own ENUMERABLE properties only, read through
 * the checked get (a getter runs; its result lands as a plain data property
 * on the destination). */
void __kml_dynobj_merge(void *dst, void *src) {
    i64 count = ((Bag *)src)->count;
    for (i64 i = 0; i < count; i++) {
        if (!(__kml_dynobj_attrs_at(src, i) & A_ENUM)) continue;
        char *key = __kml_dynobj_key_at(src, i);
        __kml_dynobj_set(dst, key, __kml_dynobj_get(src, key));
    }
}

/* patch rewrites one entry's (tag, payload, attrs) in place. */
void __kml_dynobj_patch(void *o, i64 i, i64 tag, i64 pay, i64 attrs) {
    Prop *p = &((Bag *)o)->props[i];
    p->tag = tag;
    p->pay = pay;
    p->attrs = attrs;
}

/* defacc installs (or fills in) an accessor property — the object-literal
 * get/set path (ENUMERABLE|CONFIGURABLE|ACCESSOR). A null getRec/setRec
 * leaves that slot as-is. */
void __kml_dynobj_defacc(void *o, const char *key, void *getRec, void *setRec) {
    i64 idx = __kml_dynobj_find(o, key);
    void **pair;
    if (idx >= 0) {
        if (!(__kml_dynobj_attrs_at(o, idx) & A_ACCESSOR)) {
            /* a data entry converts to a fresh accessor pair */
            void **np = (void **)calloc(1, 2 * sizeof(void *));
            __kml_dynobj_patch(o, idx, 0, (i64)np, A_ENUM | A_CONFIG | A_ACCESSOR);
        }
        pair = (void **)__kml_dynobj_rawpay_at(o, idx);
        if (getRec) pair[0] = getRec;
        if (setRec) pair[1] = setRec;
        return;
    }
    pair = (void **)calloc(1, 2 * sizeof(void *));
    pair[0] = getRec;
    pair[1] = setRec;
    /* append via the raw define (undefined placeholder), then patch the
     * entry into accessor shape */
    __kml_dynobj_set(o, key, 10);
    __kml_dynobj_patch(o, __kml_dynobj_find(o, key), 0, (i64)pair, A_ENUM | A_CONFIG | A_ACCESSOR);
}

/* prevent — mode 0: preventExtensions, 1: seal (also clears CONFIGURABLE),
 * 2: freeze (also clears WRITABLE; accessors have none). */
void __kml_dynobj_prevent(void *o, i64 mode) {
    Bag *b = (Bag *)o;
    b->flags |= F_NONEXT;
    if (mode < 1) return;
    for (i64 i = 0; i < b->count; i++) {
        i64 a = b->props[i].attrs & ~(i64)A_CONFIG;
        if (mode == 2) a &= ~(i64)A_WRITABLE;
        b->props[i].attrs = a;
    }
}

/* flags_test — mode 0: isExtensible, 1: isSealed, 2: isFrozen. */
_Bool __kml_dynobj_flags_test(void *o, i64 mode) {
    Bag *b = (Bag *)o;
    int nonext = (b->flags & F_NONEXT) != 0;
    if (mode == 0) return !nonext;
    if (!nonext) return 0;
    for (i64 i = 0; i < b->count; i++) {
        i64 a = b->props[i].attrs;
        if (a & A_CONFIG) return 0;
        if (mode == 2 && !(a & A_ACCESSOR) && (a & A_WRITABLE)) return 0;
    }
    return 1;
}
