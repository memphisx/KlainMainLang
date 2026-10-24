/* dynobj.c — the D1 dynamic object runtime (TDD-00155; in C per TDD-00240):
 * a per-instance property bag behind box tag 10. Layout (kept in sync with
 * dynjson.c, which walks it directly):
 *   header (48 bytes): i64 flags (low 32 bits magic "KMLD"; bit 32
 *     non-extensible, bit 33 Proxy, bit 34 NULLPROTO, bit 35 array exotic:
 *     Array.prototype, itself an Array; bit 36 a module namespace object),
 *     ptr proto, ptr props,
 *     i64 count, i64 cap, i64 classtag.
 *   entry (32 bytes): ptr key (owned, length-prefixed copy), i64 tag, i64
 *     payload, i64 attrs (WRITABLE 1 | ENUMERABLE 2 | CONFIGURABLE 4 |
 *     ACCESSOR 8). An accessor's payload is a ptr to {getterRec, setterRec}.
 * A Proxy header reuses offsets 8/16 as target/handler. The entry array is
 * insertion-ordered (JS key order); lookup is a linear strcmp scan. All
 * memory is plain calloc/malloc/realloc for the -mm=gc shim. */
#include <stdio.h>
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
#define F_ARRAY (1LL << 35)
#define F_MODULE (1LL << 36)
#define F_FUNC (1LL << 37) /* a function's own-property bag */
#define A_WRITABLE 1
#define A_ENUM 2
#define A_CONFIG 4
#define A_ACCESSOR 8
/* A live binding (a namespace's exported `let`): an accessor pair that
 * reads and writes the binding, shown and described as a data property. */
#define A_LIVE 16

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

/* Keys C code reads or makes: JS strings, length-prefixed, since a Proxy's
 * trap receives them and a bag made here shows them to the program. */
#define JS_KEY(n, str) static const struct { i64 len; char s[sizeof(str)]; } n = {KML_STR_ASCII_HDR(sizeof(str) - 1), str}
JS_KEY(k_get, "get");
JS_KEY(k_set, "set");
JS_KEY(k_has, "has");
JS_KEY(k_deleteProperty, "deleteProperty");
JS_KEY(k_getPrototypeOf, "getPrototypeOf");
JS_KEY(k_value, "value");
JS_KEY(k_writable, "writable");
JS_KEY(k_enumerable, "enumerable");
JS_KEY(k_configurable, "configurable");
JS_KEY(k_iterator, "@@iterator");

/* Marks a function's own-property bag: a miss there is Function.prototype's
 * to answer (the caller's), not Object.prototype's bag. */
void __kml_dynobj_mark_fn(void *o) { ((Bag *)o)->flags |= F_FUNC; }

void *__kml_dynobj_new(void) {
    Bag *o = (Bag *)kml_calloc(KML_HEAP_WORDS, 1, sizeof(Bag));
    o->flags = 0x444C4D4BLL;
    return o;
}

extern void __kml_throw_nullderef(const char *msg) __attribute__((noreturn));

/* Throws a TypeError whose message is "<pre><name><post>", as a heap string,
 * length-prefixed (TDD-00120): the thrown error keeps it. */
static void throw_type_error3(const char *pre, const char *name, const char *post) __attribute__((noreturn));
static void throw_type_error3(const char *pre, const char *name, const char *post) {
    size_t len = strlen(pre) + strlen(name) + strlen(post);
    char *base = (char *)kml_alloc(KML_HEAP_RAW, len + 9);
    *(i64 *)base = (i64)len;
    snprintf(base + 8, len + 1, "%s%s%s", pre, name, post);
    __kml_throw_nullderef(base + 8);
}

/* A builtin constructor held as a value (a class reference, its name) called
 * as a function. Object(v): a new empty object for no argument, null or
 * undefined, v itself for an object; a primitive's wrapper object does not
 * exist yet. Every other such constructor requires `new`. Constructors
 * callable as functions (String, Number) are function values instead. */
i64 __kml_classref_call(const char *name, i64 n, const i64 *argv) {
    if (strcmp(name, "Object") == 0) {
        i64 v = n > 0 ? argv[0] : nb_pack(5, 0);
        i64 tag = nb_tag(v);
        if (tag == 4 || tag == 5)
            return BOX_DYNOBJ(__kml_dynobj_new());
        if (tag <= 3)
            throw_type_error3("", "Object", "() of a primitive is not supported: primitive wrapper objects do not exist yet");
        return v;
    }
    throw_type_error3("Constructor ", name, " requires 'new'");
}

/* A bag realized from a class instance records the class's instanceof TagID
 * (ADR-00990; 0 = not from a class). */
void __kml_dynobj_set_classtag(void *o, i64 tag) { ((Bag *)o)->classtag = tag; }
i64 __kml_dynobj_classtag(void *o) { return ((Bag *)o)->classtag; }

/* Whether target (a class's prototype object, null before its first read)
   is on o's prototype chain: `o instanceof C` of a plain object. */
i64 __kml_dynobj_chain_has(void *o, void *target) {
    if (!target) return 0;
    for (Bag *b = ((Bag *)o)->proto; b; b = b->proto)
        if ((void *)b == target) return 1;
    return 0;
}

i64 __kml_dynobj_find(void *o, const char *key) {
    Bag *b = (Bag *)o;
    for (i64 i = 0; i < b->count; i++)
        if (strcmp(b->props[i].key, key) == 0) return i;
    return -1;
}

/* Object.prototype.propertyIsEnumerable on v: 1 or 0 for a dynamic
 * object's own property key (a string, or a symbol's synthetic key); -1
 * when v is not a dynamic object. */
int __kml_dynobj_prop_enumerable(i64 v, const char *key) {
    if (nb_tag(v) != 10) return -1;
    Bag *b = (Bag *)nb_pay(v);
    i64 i = __kml_dynobj_find(b, key);
    return i >= 0 && (b->props[i].attrs & A_ENUM) ? 1 : 0;
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
        b->props = (Prop *)kml_realloc(KML_HEAP_WORDS, b->props, (size_t)nc * sizeof(Prop));
        b->cap = nc;
    }
    /* key copy is length-prefixed like every heap string (TDD-00120): i64 len
     * at base, data at base+8; consumers read the header at key-8. */
    size_t klen = strlen(key);
    char *base = (char *)kml_alloc(KML_HEAP_RAW, klen + 9);
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

static void *objproto; /* Object.prototype's bag, below */
void *__kml_object_prototype(void);
/* get walks the prototype chain (acyclic: set_proto refuses cycles). An
 * ACCESSOR hit calls its getter with the original receiver as this; a
 * getter-less accessor reads undefined. A Proxy runs its "get" trap as
 * trap(target, key, receiver) with this=handler, else forwards. */
i64 __kml_dynobj_get(void *o, const char *key) {
    if (((Bag *)o)->flags & F_PROXY) {
        void *target = PX_TARGET(o), *handler = PX_HANDLER(o);
        i64 trap = __kml_dynobj_get(handler, k_get.s);
        if (nb_tag(trap) != 12) return __kml_dynobj_get(target, key);
        i64 argv[3] = {BOX_DYNOBJ(target), (i64)key, BOX_DYNOBJ(o)};
        return call_rec((void *)nb_pay(trap), BOX_DYNOBJ(handler), 3, argv);
    }
    void *last = o;
    for (void *cur = o; cur; cur = ((Bag *)cur)->proto) {
        last = cur;
        i64 idx = __kml_dynobj_find(cur, key);
        if (idx < 0) continue;
        if (!(__kml_dynobj_attrs_at(cur, idx) & A_ACCESSOR)) return __kml_dynobj_get_at(cur, idx);
        void **pair = (void **)__kml_dynobj_rawpay_at(cur, idx);
        if (!pair[0]) return 10;
        return call_rec(pair[0], BOX_DYNOBJ(o), 0, NULL);
    }
    /* A chain ending at the unmodeled Object.prototype link (not an
     * explicit null prototype) reads on in Object.prototype's bag. */
    if (last != objproto && !(((Bag *)last)->flags & (F_NULLPROTO | F_PROXY | F_FUNC))) {
        void *op = __kml_object_prototype();
        i64 idx = __kml_dynobj_find(op, key);
        if (idx >= 0) {
            if (!(__kml_dynobj_attrs_at(op, idx) & A_ACCESSOR)) return __kml_dynobj_get_at(op, idx);
            void **pair = (void **)__kml_dynobj_rawpay_at(op, idx);
            return pair[0] ? call_rec(pair[0], BOX_DYNOBJ(o), 0, NULL) : 10;
        }
    }
    return 10;
}

/* prim_get — a primitive's (a number's, a string's, a boolean's) read of
 * a key its wrapper's prototype does not model: Object.prototype's
 * property, a getter there running with the primitive itself as its
 * receiver (strict code's `this`). */
extern i64 __kml_prim_proto_of(i64 v);
i64 __kml_prim_proto_get(i64 recv, const char *key) {
    /* Its class's prototype first (Number.prototype.toFixed). */
    i64 pb = __kml_prim_proto_of(recv);
    if (pb) {
        void *p = (void *)nb_pay(pb);
        i64 idx = __kml_dynobj_find(p, key);
        if (idx >= 0) {
            if (!(__kml_dynobj_attrs_at(p, idx) & A_ACCESSOR)) return __kml_dynobj_get_at(p, idx);
            void **pair = (void **)__kml_dynobj_rawpay_at(p, idx);
            return pair[0] ? call_rec(pair[0], recv, 0, NULL) : 10;
        }
    }
    void *op = __kml_object_prototype();
    i64 idx = __kml_dynobj_find(op, key);
    if (idx < 0) return 10;
    if (!(__kml_dynobj_attrs_at(op, idx) & A_ACCESSOR)) return __kml_dynobj_get_at(op, idx);
    void **pair = (void **)__kml_dynobj_rawpay_at(op, idx);
    return pair[0] ? call_rec(pair[0], recv, 0, NULL) : 10;
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
        i64 trap = __kml_dynobj_get(handler, k_set.s);
        if (nb_tag(trap) != 12) return __kml_dynobj_setv(target, key, v);
        i64 argv[4] = {BOX_DYNOBJ(target), (i64)key, v, BOX_DYNOBJ(o)};
        return tobool(call_rec((void *)nb_pay(trap), BOX_DYNOBJ(handler), 4, argv)) ? 0 : 1;
    }
    void *last = o;
    for (void *cur = o; cur; cur = ((Bag *)cur)->proto) {
        last = cur;
        i64 idx = __kml_dynobj_find(cur, key);
        if (idx < 0) continue;
        i64 attrs = __kml_dynobj_attrs_at(cur, idx);
        if (attrs & A_ACCESSOR) {
            void **pair = (void **)__kml_dynobj_rawpay_at(cur, idx);
            if (!pair[1]) return (attrs & A_LIVE) ? 1 : 3;
            i64 argv[1] = {v};
            call_rec(pair[1], BOX_DYNOBJ(o), 1, argv);
            return 0;
        }
        if (!(attrs & A_WRITABLE) || (((Bag *)cur)->flags & F_MODULE)) return 1;
        if (cur == o) {
            Prop *p = &ob->props[idx];
            p->tag = nb_tag(v);
            p->pay = nb_pay(v);
            return 0;
        }
        last = 0; /* an inherited writable property: shadowed below */
        break;
    }
    /* A miss on a chain ending at the unmodeled Object.prototype link runs
     * an accessor of Object.prototype's (`__proto__`). */
    if (last && last != objproto && !(((Bag *)last)->flags & (F_NULLPROTO | F_PROXY | F_FUNC))) {
        void *op = __kml_object_prototype();
        i64 idx = __kml_dynobj_find(op, key);
        if (idx >= 0 && (__kml_dynobj_attrs_at(op, idx) & A_ACCESSOR)) {
            void **pair = (void **)__kml_dynobj_rawpay_at(op, idx);
            if (!pair[1]) return 3;
            i64 argv[1] = {v};
            call_rec(pair[1], BOX_DYNOBJ(o), 1, argv);
            return 0;
        }
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
static void *objproto; /* Object.prototype's bag, below */

_Bool __kml_dynobj_has_chain(void *o, const char *key) {
    if (((Bag *)o)->flags & F_PROXY) {
        void *target = PX_TARGET(o), *handler = PX_HANDLER(o);
        i64 trap = __kml_dynobj_get(handler, k_has.s);
        if (nb_tag(trap) != 12) return __kml_dynobj_has_chain(target, key);
        i64 argv[2] = {BOX_DYNOBJ(target), (i64)key};
        return tobool(call_rec((void *)nb_pay(trap), BOX_DYNOBJ(handler), 2, argv));
    }
    void *last = o;
    for (void *cur = o; cur; cur = ((Bag *)cur)->proto) {
        if (__kml_dynobj_has(cur, key)) return 1;
        last = cur;
    }
    /* A chain ending at an unmodeled Object.prototype link (not an explicit
     * null prototype), or at Object.prototype's own bag, reaches
     * Object.prototype's members. */
    if (!(((Bag *)last)->flags & F_NULLPROTO) || (objproto && last == objproto)) {
        static const char *const objproto[] = {
            "constructor", "hasOwnProperty", "isPrototypeOf",
            "propertyIsEnumerable", "toLocaleString", "toString", "valueOf",
            "__proto__", "__defineGetter__", "__defineSetter__",
            "__lookupGetter__", "__lookupSetter__"};
        for (unsigned i = 0; i < sizeof objproto / sizeof *objproto; i++)
            if (strcmp(key, objproto[i]) == 0) return 1;
    }
    return 0;
}

/* proto_prop: whether reading o.__proto__ reaches something other than
 * Object.prototype's accessor, and then (in *out) what: an own or
 * inherited "__proto__" property, or undefined when the chain ends at an
 * explicit null without passing Object.prototype. */
static const struct { i64 n; char s[10]; } k_proto = {KML_STR_ASCII_HDR(9), "__proto__"};
/* proto_plain: o.__proto__ is a plain property of o's (proto_prop). */
static _Bool proto_plain(void *o, _Bool *found) {
    *found = 0;
    if (((Bag *)o)->flags & F_PROXY) return 0;
    void *last = o;
    for (void *cur = o; cur; cur = ((Bag *)cur)->proto) {
        if (cur == objproto) return 0;
        if (__kml_dynobj_has(cur, k_proto.s)) return *found = 1;
        last = cur;
    }
    return (((Bag *)last)->flags & F_NULLPROTO) != 0;
}
_Bool __kml_dynobj_proto_prop(void *o, i64 *out) {
    _Bool found;
    if (!proto_plain(o, &found)) return 0;
    *out = found ? __kml_dynobj_get(o, k_proto.s) : 10; /* undefined */
    return 1;
}
/* proto_setplain: an assignment to o.__proto__ that is a plain property's
 * (proto_plain), done; else 0, the setter's to run. */
_Bool __kml_dynobj_proto_setplain(void *o, i64 v) {
    _Bool found;
    if (!proto_plain(o, &found)) return 0;
    __kml_dynobj_setv(o, k_proto.s, v);
    return 1;
}

/* set_proto is OrdinarySetPrototypeOf: the same prototype succeeds; a
 * non-extensible object refuses another (returns false), as does a cycle,
 * keeping every chain acyclic so the get/has walks need no visited set.
 * proto may be null. */
static void *objproto;
_Bool __kml_dynobj_set_proto(void *o, void *proto) {
    Bag *b = (Bag *)o;
    /* An unset slot without NULLPROTO is Object.prototype. */
    void *cur = b->proto ? b->proto : (b->flags & F_NULLPROTO) ? NULL : objproto;
    int same = proto ? cur == proto : (b->flags & F_NULLPROTO) != 0;
    if (same) return 1;
    /* Object.prototype is an immutable prototype exotic object. */
    if (o == objproto && objproto) return 0;
    if (b->flags & F_NONEXT) return 0;
    /* The chain proto starts, an unset slot standing for Object.prototype. */
    for (void *cur = proto; cur;) {
        if (cur == o) return 0;
        Bag *cb = (Bag *)cur;
        if (cb->flags & F_PROXY) break;
        cur = cb->proto ? cb->proto : (cb->flags & F_NULLPROTO || cur == objproto) ? NULL : objproto;
    }
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
extern void __kml_objproto_fill(void *b);
void *__kml_object_prototype(void) {
    if (!objproto) {
        void *b = __kml_dynobj_new();
        __kml_dynobj_set_proto(b, NULL);
        objproto = b;
        __kml_objproto_fill(b);
    }
    return objproto;
}

/* A builtin class's prototype object (`Map.prototype`): one bag per class,
 * made on first read into *slot, holding keys[i] → vals[i] (its methods and
 * `constructor`), none enumerable, as Node's are. */
void *__kml_builtin_proto(void **slot, i64 n, const char *const *keys, const i64 *vals) {
    if (*slot) return *slot;
    void *b = __kml_dynobj_new();
    for (i64 i = 0; i < n; i++) {
        __kml_dynobj_set(b, keys[i], vals[i]);
        ((Bag *)b)->props[((Bag *)b)->count - 1].attrs = A_WRITABLE | A_CONFIG;
    }
    *slot = b;
    return b;
}

void __kml_dynobj_defacc(void *o, const char *key, void *getRec, void *setRec);
/* A class's prototype object: keys[i] a method (vals[i] its box) when
   sets[i] is -1, else an accessor whose getter and setter records are
   vals[i] and sets[i] (either 0 when absent). None is enumerable. Its own
   [[Prototype]] is parent (its base class's prototype), or Object.prototype
   when parent is null. */
void *__kml_class_proto(void **slot, i64 n, const char *const *keys, const i64 *vals, const i64 *sets, void *parent) {
    if (*slot) return *slot;
    void *b = __kml_dynobj_new();
    for (i64 i = 0; i < n; i++) {
        if (sets[i] == -1) {
            __kml_dynobj_set(b, keys[i], vals[i]);
            ((Bag *)b)->props[__kml_dynobj_find(b, keys[i])].attrs = A_WRITABLE | A_CONFIG;
        } else {
            __kml_dynobj_defacc(b, keys[i], (void *)vals[i], (void *)sets[i]);
            ((Bag *)b)->props[__kml_dynobj_find(b, keys[i])].attrs = A_CONFIG | A_ACCESSOR;
        }
    }
    if (parent) ((Bag *)b)->proto = parent;
    *slot = b;
    return b;
}

/* Array.prototype is itself an Array (an empty one): its own `length` is 0,
   writable but neither enumerable nor configurable. */
/* Array.prototype's bag, and its [Symbol.iterator] as made: an array's
 * iteration takes its fast path while the property is still that one. */
static void *array_proto_bag;
static i64 array_iter_orig = 10;

void __kml_dynobj_array_exotic(void *o) {
    Bag *b = (Bag *)o;
    if (b->flags & F_ARRAY) return;
    b->flags |= F_ARRAY;
    __kml_dynobj_set(o, "length", nb_pack(0, 0));
    b->props[__kml_dynobj_find(o, "length")].attrs = A_WRITABLE;
    array_proto_bag = o;
    array_iter_orig = __kml_dynobj_get(o, k_iterator.s);
}

/* An array's key read through Array.prototype when the program replaced or
 * deleted its [Symbol.iterator]: 1 with the value it has now (undefined
 * once deleted); 0 for any other key, or the property as made. */
int __kml_array_iter_override(const char *key, i64 *out) {
    if (!array_proto_bag || strcmp(key, k_iterator.s) != 0) return 0;
    i64 cur = __kml_dynobj_get(array_proto_bag, k_iterator.s);
    if (cur == array_iter_orig) return 0;
    *out = cur;
    return 1;
}

/* The [Symbol.iterator] method an iteration of v calls, given m as read:
 * an array's is Array.prototype's as it is now. */
i64 __kml_iter_method_of(i64 v, i64 m) {
    i64 t = (i64)nb_tag(v), cur;
    if ((t == 7 || t == 11) && __kml_array_iter_override(k_iterator.s, &cur)) return cur;
    return m;
}

/* Whether arrays iterate through the protocol: Array.prototype's
 * [Symbol.iterator] is no longer the one it was made with. */
_Bool __kml_array_iter_patched(void) {
    i64 v;
    return __kml_array_iter_override(k_iterator.s, &v) != 0;
}

_Bool __kml_dynobj_is_array_exotic(void *o) { return (((Bag *)o)->flags & F_ARRAY) != 0; }

void __kml_dynobj_prevent(void *o, i64 mode);

/* A module namespace object: null prototype, sealed (its properties stay
   writable, but a write is rejected), tagged "Module". */
void __kml_dynobj_module_ns(void *o) {
    __kml_dynobj_prevent(o, 1);
    ((Bag *)o)->flags |= F_MODULE | F_NULLPROTO;
}

void *__kml_dynobj_get_proto(void *o);

/* `v instanceof Object`: an object or a function whose prototype chain
   reaches Object.prototype; a null-prototype bag's does not, nor does
   Object.prototype's own. */
_Bool __kml_instanceof_object(i64 v) {
    i64 t = nb_tag(v);
    if (t == 10) {
        void *op = __kml_object_prototype();
        void *o = __kml_dynobj_get_proto((void *)nb_pay(v));
        for (int hops = 0; o && hops < 100000; hops++) {
            if (o == op) return 1;
            o = __kml_dynobj_get_proto(o);
        }
        return 0;
    }
    return t == 6 || t == 7 || t == 8 || t == 9 || t == 11 || t == 12;
}

void *__kml_dynobj_get_proto(void *o) {
    Bag *b = (Bag *)o;
    if (b->flags & F_PROXY) {
        /* A Proxy's [[GetPrototypeOf]]: its handler's trap, else its
         * target's; the trap returns an object or null. */
        void *target = PX_TARGET(o), *handler = PX_HANDLER(o);
        i64 trap = __kml_dynobj_get(handler, k_getPrototypeOf.s);
        if (nb_tag(trap) != 12) return target ? __kml_dynobj_get_proto(target) : NULL;
        i64 argv[1] = {BOX_DYNOBJ(target)};
        i64 r = call_rec((void *)nb_pay(trap), BOX_DYNOBJ(handler), 1, argv);
        if (nb_tag(r) == 4) return NULL;
        if (nb_tag(r) != 10) throw_type_error3("'getPrototypeOf' on proxy: trap returned neither object nor null", "", "");
        return (void *)nb_pay(r);
    }
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
        i64 trap = __kml_dynobj_get(handler, k_deleteProperty.s);
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
    char **scratch = (char **)malloc((size_t)n * sizeof(char *)); /* scratch */
    i64 *idxv = (i64 *)malloc((size_t)n * sizeof(i64)); /* scratch */
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
    free(scratch); /* scratch */
    free(idxv); /* scratch */
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

/* A Proxy's [[OwnPropertyKeys]] and [[GetOwnProperty]] (shape.c, linked
 * with every Proxy; these defaults keep a program without one linking). */
__attribute__((weak)) const char *__kml_proxy_own_keys(void *px, const char ***ks, i64 *n) {
    (void)px;
    *ks = NULL;
    *n = 0;
    return NULL;
}
__attribute__((weak)) int __kml_proxy_own_enum(void *px, const char *key) {
    (void)px;
    (void)key;
    return -1;
}
__attribute__((weak)) i64 __kml_proxy_own_desc(void *px, const char *key) {
    (void)px;
    (void)key;
    return 10;
}
__attribute__((weak)) i64 __kml_proxy_obj_rest(i64 src, i64 excl) {
    (void)src;
    (void)excl;
    return 0;
}

/* Marks every accessor of bag o a live binding (A_LIVE). */
void __kml_dynobj_mark_live(void *o) {
    Bag *b = (Bag *)o;
    for (i64 i = 0; i < b->count; i++)
        if (b->props[i].attrs & A_ACCESSOR) b->props[i].attrs |= A_LIVE;
}

/* [[GetOwnProperty]] of a bag as a descriptor object, undefined (10) for no
 * such own property; a Proxy runs its trap (__kml_proxy_own_desc). */
i64 __kml_dynobj_own_desc(void *o, const char *key) {
    Bag *b = (Bag *)o;
    if (b->flags & F_PROXY) return __kml_proxy_own_desc(o, key);
    i64 idx = __kml_dynobj_find(o, key);
    if (idx < 0) return 10;
    i64 attrs = b->props[idx].attrs;
    void *d = __kml_dynobj_new();
    if (attrs & A_LIVE) {
        void **pair = (void **)b->props[idx].pay;
        __kml_dynobj_set(d, k_value.s, call_rec(pair[0], BOX_DYNOBJ(o), 0, 0));
        /* A module namespace's binding is writable (by its module). */
        __kml_dynobj_set(d, k_writable.s, pair[1] || (b->flags & F_MODULE) ? 7 : 6);
    } else if (attrs & A_ACCESSOR) {
        void **pair = (void **)b->props[idx].pay;
        __kml_dynobj_set(d, k_get.s, pair[0] ? (i64)pair[0] | 7 : 10);
        __kml_dynobj_set(d, k_set.s, pair[1] ? (i64)pair[1] | 7 : 10);
    } else {
        __kml_dynobj_set(d, k_value.s, __kml_dynobj_get_at(o, idx));
        __kml_dynobj_set(d, k_writable.s, (attrs & A_WRITABLE) ? 7 : 6);
    }
    __kml_dynobj_set(d, k_enumerable.s, (attrs & A_ENUM) ? 7 : 6);
    __kml_dynobj_set(d, k_configurable.s, (attrs & A_CONFIG) ? 7 : 6);
    return BOX_DYNOBJ(d);
}

/* The key list behind getOwnPropertyNames (enumerable_only = 0: every own
 * string key) and Object.keys / for...in / JSON (1: enumerable string keys
 * only), in ES order; symbol keys are never returned. Returns the array and
 * its length in *n (the IR wrappers build the {ptr,i64}). */
static char **keys_of(void *o, i64 *n, int enumerable_only) {
    Bag *b = (Bag *)o;
    if (b->flags & F_PROXY) {
        /* [[OwnPropertyKeys]] in the trap's order, then for Object.keys
         * each string key's [[GetOwnProperty]]. */
        const char **ks;
        i64 m;
        const char *err = __kml_proxy_own_keys(o, &ks, &m);
        if (err) throw_type_error3("", err, "");
        char **arr = (char **)kml_alloc(KML_HEAP_WORDS, (size_t)(m ? m : 1) * sizeof(char *));
        i64 w = 0;
        for (i64 i = 0; i < m; i++) {
            if (__kml_key_is_symbol(ks[i])) continue;
            if (enumerable_only && __kml_proxy_own_enum(o, ks[i]) != 1) continue;
            arr[w++] = (char *)ks[i];
        }
        *n = w;
        return arr;
    }
    char **arr = (char **)kml_alloc(KML_HEAP_WORDS, (size_t)b->count * sizeof(char *));
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

/* forin_keys is for...in's EnumerateObjectProperties: the object's own
 * enumerable string keys in ES order, then each prototype's, a key already
 * met at a nearer level (enumerable or not) skipped. The walk stops at a
 * Proxy, whose traps the for...in path does not run. */
char **__kml_dynobj_forin_keys_c(void *o, i64 *n) {
    i64 total = 0;
    for (Bag *b = (Bag *)o; b && !(b->flags & F_PROXY); b = (Bag *)b->proto) total += b->count;
    char **seen = (char **)malloc((size_t)(total ? total : 1) * sizeof(char *)); /* scratch */
    char **arr = (char **)kml_alloc(KML_HEAP_WORDS, (size_t)(total ? total : 1) * sizeof(char *));
    i64 ns = 0, w = 0;
    for (Bag *b = (Bag *)o; b && !(b->flags & F_PROXY); b = (Bag *)b->proto) {
        i64 m;
        char **level = keys_of(b, &m, 0);
        for (i64 i = 0; i < m; i++) {
            const char *k = level[i];
            int dup = 0;
            for (i64 j = 0; j < ns && !dup; j++) dup = strcmp(seen[j], k) == 0;
            if (dup) continue;
            seen[ns++] = level[i];
            if (b->props[__kml_dynobj_find(b, k)].attrs & A_ENUM) arr[w++] = level[i];
        }
        kml_free(KML_HEAP_WORDS, level);
    }
    free(seen); /* scratch */
    *n = w;
    return arr;
}

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

/* set_hidden sets key to v as a non-enumerable data property (a
 * prototype's `constructor`). */
void __kml_dynobj_set_hidden(void *o, const char *key, i64 v) {
    __kml_dynobj_set(o, key, v);
    ((Bag *)o)->props[__kml_dynobj_find(o, key)].attrs = A_WRITABLE | A_CONFIG;
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
            void **np = (void **)kml_calloc(KML_HEAP_WORDS, 1, 2 * sizeof(void *));
            __kml_dynobj_patch(o, idx, 0, (i64)np, A_ENUM | A_CONFIG | A_ACCESSOR);
        }
        pair = (void **)__kml_dynobj_rawpay_at(o, idx);
        if (getRec) pair[0] = getRec;
        if (setRec) pair[1] = setRec;
        return;
    }
    pair = (void **)kml_calloc(KML_HEAP_WORDS, 1, 2 * sizeof(void *));
    pair[0] = getRec;
    pair[1] = setRec;
    /* append via the raw define (undefined placeholder), then patch the
     * entry into accessor shape */
    __kml_dynobj_set(o, key, 10);
    __kml_dynobj_patch(o, __kml_dynobj_find(o, key), 0, (i64)pair, A_ENUM | A_CONFIG | A_ACCESSOR);
}

/* defacc_hidden is defacc for a builtin's own accessor: configurable, not
 * enumerable (Object.prototype.__proto__). */
/* V8's TypeError message for a refused redefinition of property key:
 * "Cannot redefine property: k", a symbol's as Symbol(desc). */
char *__kml_redefine_msg(const char *key) {
    char desc[160];
    const char *shown = key;
    if (strncmp(key, "\x01@@sym:", 7) == 0) {
        void *sym = NULL;
        sscanf(key + 7, "%p", &sym);
        const char *d = sym ? *(const char **)((char *)sym + 8) : NULL;
        snprintf(desc, sizeof desc, "Symbol(%s)", d ? d : "");
        shown = desc;
    } else if (key[0] == '@' && key[1] == '@') {
        snprintf(desc, sizeof desc, "Symbol(Symbol.%s)", key + 2);
        shown = desc;
    }
    size_t n = strlen(shown) + 26;
    char *s = (char *)kml_alloc(KML_HEAP_RAW, n + 9);
    *(i64 *)s = (i64)n;
    snprintf(s + 8, n + 1, "Cannot redefine property: %s", shown);
    return s + 8;
}

void __kml_dynobj_defacc_hidden(void *o, const char *key, void *getRec, void *setRec) {
    __kml_dynobj_defacc(o, key, getRec, setRec);
    i64 idx = __kml_dynobj_find(o, key);
    __kml_dynobj_patch(o, idx, 0, __kml_dynobj_rawpay_at(o, idx), A_CONFIG | A_ACCESSOR);
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

/* Proxy parts for callers outside this file (shape.c's ownKeys trap): whether
 * o is a Proxy, its target and handler; calling a trap record; whether a bag
 * is extensible. */
_Bool __kml_dynobj_proxy_parts(void *o, void **target, void **handler) {
    if (!(((Bag *)o)->flags & F_PROXY)) return 0;
    *target = PX_TARGET(o);
    *handler = PX_HANDLER(o);
    return 1;
}
i64 __kml_dynobj_call_trap(void *rec, i64 thisv, i64 argc, i64 *argv) { return call_rec(rec, thisv, argc, argv); }
