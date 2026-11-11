/* dynobj.c — the D1 dynamic object runtime (TDD-00155; in C per TDD-00240):
 * a per-instance property bag behind box tag 10. Layout (kept in sync with
 * dynjson.c, which walks it directly):
 *   header (56 bytes): i64 header word (KML_BAG_HDR, kml_heap.h: the
 *     magic and type id 1, flags KML_BAG_NONEXT, _PROXY, _NULLPROTO,
 *     _ARRAY (Array.prototype, itself an Array), _MODULE, _FUNC),
 *     ptr proto, ptr props,
 *     i64 count, i64 cap, i64 classtag, i64 shape (reserved for hidden
 *     classes, TDD-00220; zero).
 *   entry (32 bytes): ptr key (owned, length-prefixed copy), i64 tag, i64
 *     payload, i64 attrs (WRITABLE 1 | ENUMERABLE 2 | CONFIGURABLE 4 |
 *     ACCESSOR 8). An accessor's payload is a ptr to {getterRec, setterRec}.
 * A Proxy header reuses offsets 8/16 as target/handler. The entry array is
 * insertion-ordered (JS key order); lookup is a linear strcmp scan. All
 * memory is plain calloc/malloc/realloc for the -mm=gc shim. */
#include <stdint.h>
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
    i64 count, cap, classtag, shape;
} Bag;

#define F_NONEXT KML_BAG_NONEXT
#define F_PROXY KML_BAG_PROXY
#define F_NULLPROTO KML_BAG_NULLPROTO
#define F_ARRAY KML_BAG_ARRAY
#define F_MODULE KML_BAG_MODULE
#define F_FUNC KML_BAG_FUNC
#define A_WRITABLE 1
#define A_ENUM 2
#define A_CONFIG 4
#define A_ACCESSOR 8
/* A live binding (a namespace's exported `let`): an accessor pair that
 * reads and writes the binding, shown and described as a data property. */
#define A_LIVE 16

/* Proxy view of a header: target at +8 (its bag, NULL when the target is
 * no dynamic object: an array, a static object, a function), handler at
 * +16, and the target as a boxed value in the cap word (a Proxy has no
 * entries), which the traps receive and a non-bag target is reached
 * through (the program's any get/set, emit_reflect.go). */
#define PX_TARGET(o) (((Bag *)(o))->proto)
#define PX_HANDLER(o) ((void *)((Bag *)(o))->props)
#define PX_TBOX(o) (((Bag *)(o))->cap)
extern i64 __kml_proxy_tget(i64 target, const char *key);
extern i64 __kml_proxy_tset(i64 target, const char *key, i64 v);

/* nb_* mirror __kml_nb_pack/tag/pay (runtime_nanbox.go — keep in sync). */
static i64 nb_double(double d) {
    unsigned long long bits;
    if (d != d) bits = 0x7FF8000000000000ULL;
    else memcpy(&bits, &d, 8); /* scratch */
    return (i64)(bits + (1ULL << 49));
}
static i64 nb_pack(i64 tag, i64 pay) {
    switch (tag) {
    case 0: return nb_double((double)pay);
    case 1: { double d; memcpy(&d, &pay, 8); return nb_double(d); } /* scratch */
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
    o->flags = KML_BAG_HDR;
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
 * undefined, v itself for an object, and a primitive's wrapper object, which
 * code generation builds (wrap). Every other such constructor requires `new`.
 * Constructors callable as functions (String, Number) are function values
 * instead. */
extern const void *__kml_unit_find(i64 kind, i64 id);

/* The globals Node implements as JavaScript classes (its own lib, not V8):
 * called without `new`, a class constructor's message. Sorted. */
static const char *const node_class_ctors[] = {
    "AbortController",
    "AbortSignal",
    "Blob",
    "BroadcastChannel",
    "ByteLengthQueuingStrategy",
    "CloseEvent",
    "CompressionStream",
    "CountQueuingStrategy",
    "Crypto",
    "CryptoKey",
    "CustomEvent",
    "DOMException",
    "DecompressionStream",
    "Event",
    "EventTarget",
    "File",
    "FormData",
    "Headers",
    "MessageEvent",
    "Navigator",
    "Performance",
    "PerformanceEntry",
    "PerformanceMark",
    "PerformanceMeasure",
    "PerformanceObserver",
    "PerformanceObserverEntryList",
    "PerformanceResourceTiming",
    "ReadableByteStreamController",
    "ReadableStream",
    "ReadableStreamBYOBReader",
    "ReadableStreamBYOBRequest",
    "ReadableStreamDefaultController",
    "ReadableStreamDefaultReader",
    "Request",
    "Response",
    "SubtleCrypto",
    "TextDecoder",
    "TextDecoderStream",
    "TextEncoder",
    "TextEncoderStream",
    "TransformStream",
    "TransformStreamDefaultController",
    "URL",
    "URLSearchParams",
    "WebSocket",
    "WritableStream",
    "WritableStreamDefaultController",
    "WritableStreamDefaultWriter",
};

static int cmp_name(const void *a, const void *b) { return strcmp(*(const char *const *)a, *(const char *const *)b); }

i64 __kml_classref_call(const char *name, i64 n, const i64 *argv, i64 (*wrap)(i64)) {
    if (strcmp(name, "Object") == 0) {
        i64 v = n > 0 ? argv[0] : nb_pack(5, 0);
        i64 tag = nb_tag(v);
        if (tag == 4 || tag == 5)
            return BOX_DYNOBJ(__kml_dynobj_new());
        if (tag <= 3)
            return wrap(v);
        return v;
    }
    /* A class of the program (its constructor row shows it): a class
     * constructor's own message. */
    const void *const *row = (const void *const *)__kml_unit_find(5 /* KML_UNIT_CTOR */, (i64)(uintptr_t)name);
    if (row && row[1] || bsearch(&name, node_class_ctors, sizeof node_class_ctors / sizeof *node_class_ctors, sizeof *node_class_ctors, cmp_name))
        throw_type_error3("Class constructor ", name, " cannot be invoked without 'new'");
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
    kml_copy(KML_HEAP_RAW, base + 8, key, klen + 1);
    Prop *p = &b->props[b->count];
    KML_STORE_PTR(&p->key, base + 8);
    p->tag = tag;
    KML_STORE_PTR(&p->pay, pay);
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
/* A constructor reference's extra bag and statics (boxsrc/hostbox.c); the
 * weak defaults stand in for a program without references. */
__attribute__((weak)) i64 __kml_classref_of_extra(void *bag) { (void)bag; return 0; }
__attribute__((weak)) i64 __kml_classref_get(void *p, const char *key) { (void)p; (void)key; return 10; }
/* get walks the prototype chain (acyclic: set_proto refuses cycles). An
 * ACCESSOR hit calls its getter with the original receiver as this; a
 * getter-less accessor reads undefined. A Proxy runs its "get" trap as
 * trap(target, key, receiver) with this=handler, else forwards. */
i64 __kml_dynobj_get_recv(void *o, const char *key, i64 recv);
i64 __kml_dynobj_get(void *o, const char *key) { return __kml_dynobj_get_recv(o, key, BOX_DYNOBJ(o)); }

/* get_recv is [[Get]](key, receiver): a getter found on the chain runs
 * with recv as this (Reflect.get's third argument, `super.x`). */
i64 __kml_dynobj_get_recv(void *o, const char *key, i64 recv) {
    if (recv == 10) recv = BOX_DYNOBJ(o); /* an absent receiver (a call through the function value) */
    if (((Bag *)o)->flags & F_PROXY) {
        void *target = PX_TARGET(o), *handler = PX_HANDLER(o);
        i64 trap = __kml_dynobj_get(handler, k_get.s);
        if (nb_tag(trap) != 12) return target ? __kml_dynobj_get_recv(target, key, recv) : __kml_proxy_tget(PX_TBOX(o), key);
        i64 argv[3] = {PX_TBOX(o), (i64)key, recv};
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
        return call_rec(pair[0], recv, 0, NULL);
    }
    /* A constructor reference standing in the chain (its extra bag): its
     * statics. */
    for (void *cur = o; cur; cur = ((Bag *)cur)->proto) {
        i64 ref = __kml_classref_of_extra(cur);
        if (ref) return __kml_classref_get((void *)nb_pay(ref), key);
    }
    /* A chain ending at the unmodeled Object.prototype link (not an
     * explicit null prototype) reads on in Object.prototype's bag. */
    if (last != objproto && !(((Bag *)last)->flags & (F_NULLPROTO | F_PROXY | F_FUNC))) {
        void *op = __kml_object_prototype();
        i64 idx = __kml_dynobj_find(op, key);
        if (idx >= 0) {
            if (!(__kml_dynobj_attrs_at(op, idx) & A_ACCESSOR)) return __kml_dynobj_get_at(op, idx);
            void **pair = (void **)__kml_dynobj_rawpay_at(op, idx);
            return pair[0] ? call_rec(pair[0], recv, 0, NULL) : 10;
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
/* px_set_recv is a Proxy's [[Set]](key, v, recv): its set trap, else the
 * target's [[Set]] with the same receiver. 0 ok, 1 refused. */
i64 __kml_dynobj_setv_recv(void *o, const char *key, i64 v, i64 recv);
static i64 px_set_recv(void *px, const char *key, i64 v, i64 recv) {
    void *target = PX_TARGET(px), *handler = PX_HANDLER(px);
    i64 trap = __kml_dynobj_get(handler, k_set.s);
    if (nb_tag(trap) != 12) {
        if (!target) return (__kml_proxy_tset(PX_TBOX(px), key, v), 0);
        i64 r = __kml_dynobj_setv_recv(target, key, v, recv);
        return r == 4 ? 1 : r;
    }
    i64 argv[4] = {PX_TBOX(px), (i64)key, v, recv};
    return tobool(call_rec((void *)nb_pay(trap), BOX_DYNOBJ(handler), 4, argv)) ? 0 : 1;
}

i64 __kml_dynobj_setv(void *o, const char *key, i64 v) {
    Bag *ob = (Bag *)o;
    if (ob->flags & F_PROXY) {
        void *target = PX_TARGET(o), *handler = PX_HANDLER(o);
        i64 trap = __kml_dynobj_get(handler, k_set.s);
        if (nb_tag(trap) != 12) return target ? __kml_dynobj_setv(target, key, v) : (__kml_proxy_tset(PX_TBOX(o), key, v), 0);
        i64 argv[4] = {PX_TBOX(o), (i64)key, v, BOX_DYNOBJ(o)};
        return tobool(call_rec((void *)nb_pay(trap), BOX_DYNOBJ(handler), 4, argv)) ? 0 : 1;
    }
    void *last = o;
    for (void *cur = o; cur; cur = ((Bag *)cur)->proto) {
        last = cur;
        if (cur != o && (((Bag *)cur)->flags & F_PROXY)) return px_set_recv(cur, key, v, BOX_DYNOBJ(o));
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
            KML_STORE_PTR(&p->pay, nb_pay(v));
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

/* setv_recv is [[Set]](key, v, receiver) for a receiver other than o: an
 * accessor on o's chain runs its setter with recv as this; otherwise the
 * value lands on recv (OrdinarySetWithOwnDescriptor): on its own writable
 * data property, else as a new own one. 0 ok, 1 read-only, 2 not
 * extensible, 3 accessor without a setter, 4 recv is not a dynamic object
 * (the caller then stores to recv as an ordinary assignment). */
i64 __kml_dynobj_setv_recv(void *o, const char *key, i64 v, i64 recv) {
    if (recv == 10 || (nb_tag(recv) == 10 && (void *)nb_pay(recv) == o)) return __kml_dynobj_setv(o, key, v);
    for (void *cur = o; cur; cur = ((Bag *)cur)->proto) {
        if (((Bag *)cur)->flags & F_PROXY) return px_set_recv(cur, key, v, recv);
        i64 idx = __kml_dynobj_find(cur, key);
        if (idx < 0) continue;
        i64 attrs = __kml_dynobj_attrs_at(cur, idx);
        if (attrs & A_ACCESSOR) {
            void **pair = (void **)__kml_dynobj_rawpay_at(cur, idx);
            if (!pair[1]) return 3;
            i64 argv[1] = {v};
            call_rec(pair[1], recv, 1, argv);
            return 0;
        }
        if (!(attrs & A_WRITABLE)) return 1;
        break;
    }
    if (nb_tag(recv) != 10) return 4;
    void *r = (void *)nb_pay(recv);
    i64 idx = __kml_dynobj_find(r, key);
    if (idx >= 0) {
        i64 attrs = __kml_dynobj_attrs_at(r, idx);
        if (attrs & A_ACCESSOR) return 3;
        if (!(attrs & A_WRITABLE)) return 1;
    } else if (((Bag *)r)->flags & F_NONEXT) {
        return 2;
    }
    __kml_dynobj_set(r, key, v);
    return 0;
}

/* prim_proto_set is [[Set]] on a primitive receiver (a number's, a
 * string's, a boolean's, a symbol's): a setter or a Proxy on its
 * wrapper's prototype chain runs with the primitive as receiver; anything
 * else cannot land on a primitive. 0 handled, 1 refused. */
i64 __kml_prim_proto_set(i64 recv, const char *key, i64 v) {
    i64 pb = __kml_prim_proto_of(recv);
    if (!pb) return 1;
    return __kml_dynobj_setv_recv((void *)nb_pay(pb), key, v, recv) == 0 ? 0 : 1;
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
        if (nb_tag(trap) != 12) return target ? __kml_dynobj_has_chain(target, key) : __kml_proxy_tget(PX_TBOX(o), key) != 10;
        i64 argv[2] = {PX_TBOX(o), (i64)key};
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
/* chain_nullproto: o's prototype chain ends in null without reaching
 * Object.prototype (`Object.create(null)`, or a chain of such objects), so
 * nothing on it inherits Object.prototype's methods. A proxy answers no. */
_Bool __kml_dynobj_chain_nullproto(void *o) {
    void *last = o;
    for (void *cur = o; cur; cur = ((Bag *)cur)->proto) {
        if (cur == objproto || (((Bag *)cur)->flags & F_PROXY)) return 0;
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
    /* An unset slot without NULLPROTO is Object.prototype; a function's
     * bag's is Function.prototype, which no bag argument is. */
    void *cur = b->proto ? b->proto : (b->flags & (F_NULLPROTO | F_FUNC)) ? NULL : objproto;
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
    KML_STORE_PTR(&b->proto, proto);
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
/* The class prototype objects made so far: a class has one, made once. */
static void **class_protos;
static i64 n_class_protos, cap_class_protos;

static void class_proto_add(void *b) {
    if (n_class_protos == cap_class_protos) {
        i64 cap = cap_class_protos ? cap_class_protos * 2 : 16;
        void **grown = kml_calloc(KML_HEAP_WORDS, (size_t)cap, sizeof(void *));
        for (i64 i = 0; i < n_class_protos; i++) KML_STORE_PTR(&grown[i], class_protos[i]);
        if (class_protos) kml_free(KML_HEAP_WORDS, class_protos);
        class_protos = grown;
        cap_class_protos = cap;
    }
    KML_STORE_PTR(&class_protos[n_class_protos++], b);
}

/* The class prototypes whose class a constructor function's instances can
 * be made as (a program running `C.call(this, …)`), with its allocator. */
static void **alloc_protos;
static void *(**alloc_fns)(void);
static i64 n_alloc_protos, cap_alloc_protos;

void __kml_class_proto_alloc(void *bag, void *(*alloc)(void)) {
    for (i64 i = 0; i < n_alloc_protos; i++)
        if (alloc_protos[i] == bag) return;
    if (n_alloc_protos == cap_alloc_protos) {
        i64 cap = cap_alloc_protos ? cap_alloc_protos * 2 : 8;
        void **gp = kml_calloc(KML_HEAP_WORDS, (size_t)cap, sizeof(void *));
        void *(**gf)(void) = kml_calloc(KML_HEAP_WORDS, (size_t)cap, sizeof(void *));
        for (i64 i = 0; i < n_alloc_protos; i++) {
            KML_STORE_PTR(&gp[i], alloc_protos[i]);
            gf[i] = alloc_fns[i];
        }
        alloc_protos = gp;
        alloc_fns = gf;
        cap_alloc_protos = cap;
    }
    KML_STORE_PTR(&alloc_protos[n_alloc_protos], bag);
    alloc_fns[n_alloc_protos++] = alloc;
}

extern void __kml_shape_set_proto(void *obj, void *proto);
void *__kml_dynobj_get_proto(void *o);

/* `this` for `new F()` with F.prototype proto: an instance of the nearest
 * class on proto's chain whose constructor runs on an existing object,
 * given proto, else a fresh object inheriting proto. */
i64 __kml_ctor_this(void *proto) {
    for (void *cur = proto; cur && n_alloc_protos; cur = __kml_dynobj_get_proto(cur)) {
        for (i64 i = 0; i < n_alloc_protos; i++) {
            if (alloc_protos[i] == cur) {
                void *obj = alloc_fns[i]();
                __kml_shape_set_proto(obj, proto);
                return (i64)(uintptr_t)obj | 1;
            }
        }
    }
    void *obj = __kml_dynobj_new();
    __kml_dynobj_set_proto(obj, proto);
    return BOX_DYNOBJ(obj);
}

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
    if (parent) KML_STORE_PTR(&((Bag *)b)->proto, parent);
    *slot = b;
    class_proto_add(b);
    return b;
}

/* Whether o is a class's prototype object (__kml_class_proto made it): a
 * chain reaching one resolves the class's own members. */
int __kml_is_class_proto(void *o) {
    for (i64 i = 0; i < n_class_protos; i++)
        if (class_protos[i] == o) return 1;
    return 0;
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

/* The prototype a bag was given, boxed: a function's bag as its function,
 * null, or 0 for a bag whose slot was never set. */
__attribute__((weak)) long long __kml_fn_bag_owner(void *bag) { (void)bag; return 0; }
long long __kml_dynobj_proto_box(void *o) {
    Bag *b = (Bag *)o;
    if (b->proto) {
        if (((Bag *)b->proto)->flags & F_FUNC) {
            long long fn = __kml_fn_bag_owner(b->proto);
            if (fn) return fn;
        }
        return BOX_DYNOBJ(b->proto);
    }
    return (b->flags & F_NULLPROTO) ? 2 /* null */ : 0;
}

/* A bag pointer (or NULL) boxed as a value: a function's bag is its
 * function. */
long long __kml_dynobj_box_bag(void *o) {
    if (!o) return 2; /* null */
    if (((Bag *)o)->flags & F_FUNC) {
        long long fn = __kml_fn_bag_owner(o);
        if (fn) return fn;
    }
    return BOX_DYNOBJ(o);
}

void *__kml_dynobj_get_proto(void *o) {
    Bag *b = (Bag *)o;
    if (b->flags & F_PROXY) {
        /* A Proxy's [[GetPrototypeOf]]: its handler's trap, else its
         * target's; the trap returns an object or null. */
        void *target = PX_TARGET(o), *handler = PX_HANDLER(o);
        i64 trap = __kml_dynobj_get(handler, k_getPrototypeOf.s);
        if (nb_tag(trap) != 12) return target ? __kml_dynobj_get_proto(target) : NULL;
        i64 argv[1] = {PX_TBOX(o)};
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
        if (nb_tag(trap) != 12) return target ? __kml_dynobj_delete(target, key) : 1;
        i64 argv[2] = {PX_TBOX(o), (i64)key};
        return tobool(call_rec((void *)nb_pay(trap), BOX_DYNOBJ(handler), 2, argv));
    }
    i64 idx = __kml_dynobj_find(o, key);
    if (idx < 0) return 1; /* deleting a missing property is still true */
    /* a non-configurable property refuses deletion; the strict-JS caller
     * surfaces that as its TypeError. */
    if (!(b->props[idx].attrs & A_CONFIG)) return 0;
    kml_copy(KML_HEAP_WORDS, &b->props[idx], &b->props[idx + 1], (size_t)(b->count - idx - 1) * sizeof(Prop));
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
    kml_copy(KML_HEAP_WORDS, arr, scratch, (size_t)n * sizeof(char *));
    free(scratch); /* scratch */
    free(idxv); /* scratch */
}

/* Whether a bag key is a symbol's: a Symbol's "\x01@@sym:%p" key or a
 * well-known symbol's "@@x" (emitSymbolPropertyKey) — string enumerations
 * skip it. */
_Bool __kml_key_is_symbol(const char *k) {
    /* Mirrors wellKnownMemberKeys (runtime_dynobj.go). */
    static const char *const wk[] = {"@@iterator", "@@asyncIterator", "@@toPrimitive", "@@toStringTag", "@@dispose", "@@asyncDispose", "@@species", "@@hasInstance"};
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
            KML_STORE_PTR(&arr[w++], (char *)ks[i]);
        }
        *n = w;
        return arr;
    }
    char **arr = (char **)kml_alloc(KML_HEAP_WORDS, (size_t)b->count * sizeof(char *));
    i64 w = 0;
    for (i64 i = 0; i < b->count; i++) {
        if (enumerable_only && !(b->props[i].attrs & A_ENUM)) continue;
        if (__kml_key_is_symbol(b->props[i].key)) continue;
        KML_STORE_PTR(&arr[w++], b->props[i].key);
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
            if (b->props[__kml_dynobj_find(b, k)].attrs & A_ENUM) KML_STORE_PTR(&arr[w++], level[i]);
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

/* copy_own copies each of src's own properties onto dst as it is: its
 * value or accessor pair, and its attributes. */
void __kml_dynobj_copy_own(void *dst, void *src) {
    if (!src) return;
    Bag *b = (Bag *)src;
    for (i64 i = 0; i < b->count; i++) {
        Prop *p = &b->props[i];
        __kml_dynobj_set(dst, p->key, 10);
        Prop *q = &((Bag *)dst)->props[__kml_dynobj_find(dst, p->key)];
        q->tag = p->tag;
        KML_STORE_PTR(&q->pay, p->pay);
        q->attrs = p->attrs;
    }
}

/* set_hidden sets key to v as a non-enumerable data property (a
 * prototype's `constructor`). */
void __kml_dynobj_set_hidden(void *o, const char *key, i64 v) {
    __kml_dynobj_set(o, key, v);
    ((Bag *)o)->props[__kml_dynobj_find(o, key)].attrs = A_WRITABLE | A_CONFIG;
}

/* set_attrs sets key to v as a data property with attributes attrs
 * (A_WRITABLE, A_ENUM, A_CONFIG). */
void __kml_dynobj_set_attrs(void *o, const char *key, i64 v, i64 attrs) {
    __kml_dynobj_set(o, key, v);
    ((Bag *)o)->props[__kml_dynobj_find(o, key)].attrs = (unsigned char)attrs;
}

/* patch rewrites one entry's (tag, payload, attrs) in place. */
void __kml_dynobj_patch(void *o, i64 i, i64 tag, i64 pay, i64 attrs) {
    Prop *p = &((Bag *)o)->props[i];
    p->tag = tag;
    KML_STORE_PTR(&p->pay, pay);
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
        if (getRec) KML_STORE_PTR(&pair[0], getRec);
        if (setRec) KML_STORE_PTR(&pair[1], setRec);
        return;
    }
    pair = (void **)kml_calloc(KML_HEAP_WORDS, 1, 2 * sizeof(void *));
    KML_STORE_PTR(&pair[0], getRec);
    KML_STORE_PTR(&pair[1], setRec);
    /* append via the raw define (undefined placeholder), then patch the
     * entry into accessor shape */
    __kml_dynobj_set(o, key, 10);
    __kml_dynobj_patch(o, __kml_dynobj_find(o, key), 0, (i64)pair, A_ENUM | A_CONFIG | A_ACCESSOR);
}

/* defacc_hidden is defacc for a builtin's own accessor: configurable, not
 * enumerable (Object.prototype.__proto__). */
/* V8's TypeError message for a refused redefinition of property key:
 * "Cannot redefine property: k", a symbol's as Symbol(desc). */
/* kml_key_shown is key as V8 prints it in a message: a symbol's as
 * Symbol(desc), into buf. */
static const char *kml_key_shown(const char *key, char *buf, size_t bn) {
    if (strncmp(key, "\x01@@sym:", 7) == 0) {
        void *sym = NULL;
        sscanf(key + 7, "%p", &sym);
        const char *d = sym ? *(const char **)((char *)sym + 8) : NULL;
        snprintf(buf, bn, "Symbol(%s)", d ? d : "");
        return buf;
    }
    if (key[0] == '@' && key[1] == '@') {
        snprintf(buf, bn, "Symbol(Symbol.%s)", key + 2);
        return buf;
    }
    return key;
}

/* V8's TypeError message for reading key off null (isNull) or undefined:
 * "Cannot read properties of undefined (reading 'k')". */
char *__kml_nullish_read_msg(const char *key, int isNull) {
    char desc[160];
    const char *shown = kml_key_shown(key, desc, sizeof desc);
    const char *what = isNull ? "null" : "undefined";
    size_t n = strlen(shown) + strlen(what) + 39;
    char *s = (char *)kml_alloc(KML_HEAP_RAW, n + 40);
    if (strcmp(key, "@@iterator") == 0) /* V8 words this read as iteration */
        snprintf(s + 8, n + 32, "%s is not iterable (cannot read property %s)", what, shown);
    else
        snprintf(s + 8, n + 1, "Cannot read properties of %s (reading '%s')", what, shown);
    *(i64 *)s = (i64)strlen(s + 8);
    return s + 8;
}

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
/* A call of a Proxy (`p(…)`) whose target is a function: its "apply"
 * trap as trap(target, thisArg, argumentsArray) with this=handler, else
 * the target called. 0 when v is no Proxy of a function (the caller's
 * TypeError). A Proxy of a Proxy forwards to the inner one. */
JS_KEY(k_apply, "apply");
/* Whether v is a Proxy of a function (of a Proxy of one, …): typeof
 * "function", callable. */
int __kml_proxy_is_fn(i64 v) {
    for (int depth = 0; depth < 1024; depth++) {
        if (nb_tag(v) != 10) return nb_tag(v) == 12 && depth > 0;
        void *o = (void *)nb_pay(v);
        if (!(((Bag *)o)->flags & F_PROXY)) return 0;
        v = PX_TBOX(o);
    }
    return 0;
}
extern void *__kml_dynarr_new(i64 cap0);
extern void __kml_dynarr_push(void *ap, i64 v);
int __kml_proxy_call(i64 v, i64 thisv, i64 argc, i64 *argv, i64 *out) {
    for (int depth = 0; depth < 1024; depth++) {
        if (nb_tag(v) != 10) return 0;
        void *o = (void *)nb_pay(v);
        if (!(((Bag *)o)->flags & F_PROXY)) return 0;
        i64 t = PX_TBOX(o);
        if (nb_tag(t) == 10) {
            void *to = (void *)nb_pay(t);
            if (!(((Bag *)to)->flags & F_PROXY)) return 0;
        } else if (nb_tag(t) != 12) {
            return 0;
        }
        void *handler = PX_HANDLER(o);
        i64 trap = handler ? __kml_dynobj_get(handler, k_apply.s) : 10;
        if (nb_tag(trap) == 12) {
            void *arr = __kml_dynarr_new(argc);
            for (i64 i = 0; i < argc; i++) __kml_dynarr_push(arr, argv[i]);
            i64 targv[3] = {t, thisv, (i64)arr | 6};
            *out = call_rec((void *)nb_pay(trap), BOX_DYNOBJ(handler), 3, targv);
            return 1;
        }
        if (nb_tag(t) == 12) {
            *out = call_rec((void *)nb_pay(t), thisv, argc, argv);
            return 1;
        }
        v = t;
    }
    return 0;
}

/* A Proxy's target as the boxed value its traps receive. */
i64 __kml_dynobj_proxy_tbox(void *o) { return PX_TBOX(o); }

/* A bound function's dynamic-ABI body (emit_dynfunc.go): env is { i64
 * target, i64 this, i64 n, i64 bound[n] }; it calls the target with that
 * `this` and the bound arguments before its own. */
long long __kml_dyn_bound(long long *env, long long callThis, long long argc, long long *argv) {
    (void)callThis; /* a bound function ignores its call's `this` */
    long long target = env[0], thisv = env[1], n = env[2];
    long long *nargv = (long long *)kml_alloc(KML_HEAP_WORDS, (size_t)(n + argc + 1) * 8);
    kml_copy(KML_HEAP_WORDS, nargv, env + 3, (size_t)n * 8);
    kml_copy(KML_HEAP_WORDS, nargv + n, argv, (size_t)argc * 8);
    void **rec = (void **)(uintptr_t)(target & ~7LL);
    long long (*fp)(void *, long long, long long, long long *) = (long long (*)(void *, long long, long long, long long *))rec[0];
    return fp(rec[1], thisv, n + argc, nargv);
}

/* `v instanceof C` reads v's prototype: a Proxy without a getPrototypeOf
 * trap answers with its target's, so the check sees through it to the
 * target (repeatedly, for a Proxy of a Proxy). */
static const struct { i64 n; char s[15]; } k_gpo = {KML_STR_ASCII_HDR(14), "getPrototypeOf"};
i64 __kml_dynobj_proxy_proto_src(i64 v) {
    for (int n = 0; n < 1024; n++) {
        if (nb_tag(v) != 10) return v;
        void *o = (void *)nb_pay(v);
        if (!o || !(((Bag *)o)->flags & F_PROXY)) return v;
        void *h = PX_HANDLER(o);
        if (h && __kml_dynobj_has(h, k_gpo.s)) return v;
        v = PX_TBOX(o);
    }
    return v;
}

/* A class's `super(…)` that constructed a base held in a value: what it
 * built takes the prototype of the instance being constructed (new.target's
 * prototype), when it is an object of this file. */
void __kml_dynobj_adopt_proto(i64 built, i64 proto) {
    if (nb_tag(built) != 10 || nb_tag(proto) != 10) return;
    void *o = (void *)nb_pay(built);
    if (o && !(((Bag *)o)->flags & F_PROXY)) __kml_dynobj_set_proto(o, (void *)nb_pay(proto));
}

/* A class evaluated again (declared in a function) re-reads its heritage:
 * its prototype object's [[Prototype]] becomes the newly read one (null or
 * an object of this file). */
void __kml_dynobj_relink_proto(i64 obj, i64 proto) {
    if (nb_tag(obj) != 10) return;
    void *o = (void *)nb_pay(obj);
    if (!o || (((Bag *)o)->flags & F_PROXY)) return;
    if (nb_tag(proto) == 4) __kml_dynobj_set_proto(o, 0); /* null */
    else if (nb_tag(proto) == 10) __kml_dynobj_set_proto(o, (void *)nb_pay(proto));
}
