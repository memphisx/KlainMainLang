/* fnmeta.c — the object side of function values (TDD-00229 Stage A).
 *
 * A function value is a closure header {fnptr, env} (static ABI) or a boxed
 * dynamic record {fnptr, env, arity} (tag 12, dyn ABI). Its name, length and
 * kind live in rows each compiled unit registers (unitreg.c), keyed by the
 * code pointer (fnmeta.go). An unregistered code pointer is an anonymous
 * plain function — every runtime-internal callable (a promise resolver, a
 * stream callback) lands there, which is how Node renders those too. */

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    void *fn; /* the row's key, as unitreg.c's i64 */
    const char *name;
    long long length;
    long long kind;
} KmlFnMeta;

extern const void *__kml_unit_find(long long kind, long long id);
#define KML_UNIT_FNMETA 4

enum {
    FN_PLAIN = 0,
    FN_ASYNC = 1,
    FN_GEN = 2,
    FN_ASYNCGEN = 3,
    FN_CLASS = 4,
    FN_BOUND = 0x100,
    FN_THROUGH_ENV = 0x200,
    FN_THROUGH_BOX = 0x400, /* env = { tag, box }: a dynamic function's closure thunk */
    FN_BOUND_BOX = 0x800,   /* env = { target box, this, n, bound… }: a dynamic bound function */
};

/* An extended record (a node:ffi bound function, TDD-00229) flags its arity
 * word and carries its own name and own-property bag:
 * { fnptr, env, arity | FN_EXT, char *name, void *props }. */
#define FN_EXT (1LL << 62)

/* Dynamic-object runtime (dynobjsrc/dynobj.c), always linked with this file. */
extern long long __kml_dynobj_find(void *o, const char *key);
extern void __kml_dynobj_set(void *o, const char *key, long long v);
extern long long __kml_dynobj_rawtag_at(void *o, long long i);
extern long long __kml_dynobj_rawpay_at(void *o, long long i);
extern void __kml_dynobj_patch(void *o, long long i, long long tag, long long pay, long long attrs);

/* Renders an own-property bag at `depth` ("" when empty). A program without
 * dynjson.c (KML_FN_DYNJSON unset) has no function that can carry properties. */
#ifdef KML_FN_DYNJSON
extern char *__kml_dynobj_inspect_at(char *o, long long depth);
#endif
static struct { long long len; char z[8]; } fn_empty_str;
static char *__kml_fn_props_inspect(void *props, long long depth) {
#ifdef KML_FN_DYNJSON
    if (*(long long *)((char *)props + 24) != 0) return __kml_dynobj_inspect_at((char *)props, depth);
#else
    (void)props; (void)depth;
#endif
    return fn_empty_str.z;
}

char *__kml_fn_name_dyn(void **rec);
long long __kml_fn_length_dyn(void **rec);

/* A function's new property bag starts with its own `length` and `name`, as
 * Node's function has them: not writable, not enumerable, but configurable.
 * Passed to __kml_fn_props_dyn_make by the compiler. KML_NB_DOUBLE_OFFSET and
 * KML_DYN_ATTR_CONFIGURABLE come from fnmeta.go. */
void __kml_fn_bag_seed(void *bag, void **rec) {
    double len = (double)__kml_fn_length_dyn(rec);
    long long lenbits;
    memcpy(&lenbits, &len, 8);
    __kml_dynobj_set(bag, "length", lenbits + KML_NB_DOUBLE_OFFSET);
    long long li = __kml_dynobj_find(bag, "length");
    __kml_dynobj_patch(bag, li, __kml_dynobj_rawtag_at(bag, li), __kml_dynobj_rawpay_at(bag, li), KML_DYN_ATTR_CONFIGURABLE);
    __kml_dynobj_set(bag, "name", (long long)(uintptr_t)__kml_fn_name_dyn(rec));
    long long ni = __kml_dynobj_find(bag, "name");
    __kml_dynobj_patch(bag, ni, __kml_dynobj_rawtag_at(bag, ni), __kml_dynobj_rawpay_at(bag, ni), KML_DYN_ATTR_CONFIGURABLE);
}

/* Nested bind/adapter chains are short; the bound bounds a malformed cycle. */
#define FN_MAX_DEPTH 64

static const KmlFnMeta *fn_find(void *fn) {
    return fn ? (const KmlFnMeta *)__kml_unit_find(KML_UNIT_FNMETA, (long long)(intptr_t)fn) : NULL;
}

/* Resolved view of one function value. */
typedef struct {
    char name[512];
    long long length;
    int kind; /* FN_PLAIN..FN_CLASS */
} FnView;

static void fn_view_hdr(void **hdr, FnView *out, int depth);
static void fn_view_dyn_at(void **rec, FnView *v, int depth);

/* A boxed function value's record (a tag-12 box: the pointer under the low
 * tag bits). */
static void **fn_box_rec(long long box) { return (void **)(uintptr_t)(box & ~7LL); }

static void fn_view_code(void *fn, void *env, FnView *out, int depth) {
    const KmlFnMeta *m = depth < FN_MAX_DEPTH && fn ? fn_find(fn) : NULL;
    if (!m) {
        out->name[0] = 0;
        out->length = 0;
        out->kind = FN_PLAIN;
        return;
    }
    if ((m->kind & FN_THROUGH_ENV) && env) {
        fn_view_hdr((void **)env, out, depth + 1);
        return;
    }
    if ((m->kind & FN_THROUGH_BOX) && env) {
        fn_view_dyn_at(fn_box_rec(((long long *)env)[1]), out, depth + 1);
        return;
    }
    if ((m->kind & (FN_BOUND | FN_BOUND_BOX)) && env) {
        FnView target;
        long long bound = m->length;
        if (m->kind & FN_BOUND_BOX) {
            fn_view_dyn_at(fn_box_rec(((long long *)env)[0]), &target, depth + 1);
            bound = ((long long *)env)[2];
        } else
            fn_view_hdr(*(void ***)env, &target, depth + 1);
        size_t n = strlen(target.name);
        if (n > sizeof out->name - 7) n = sizeof out->name - 7;
        memcpy(out->name, "bound ", 6);
        memcpy(out->name + 6, target.name, n);
        out->name[6 + n] = 0;
        /* m->length holds a trampoline's bound-argument count. */
        out->length = target.length > bound ? target.length - bound : 0;
        out->kind = target.kind == FN_CLASS ? FN_PLAIN : target.kind;
        return;
    }
    size_t n = strlen(m->name);
    if (n > sizeof out->name - 1) n = sizeof out->name - 1;
    memcpy(out->name, m->name, n);
    out->name[n] = 0;
    out->length = m->length;
    out->kind = (int)(m->kind & 0xff);
}

static void fn_view_hdr(void **hdr, FnView *out, int depth) {
    if (!hdr) {
        out->name[0] = 0;
        out->length = 0;
        out->kind = FN_PLAIN;
        return;
    }
    fn_view_code(hdr[0], hdr[1], out, depth);
}

static char *kml_str(const char *s, size_t n) {
    char *base = (char *)malloc(8 + n + 1);
    *(long long *)base = (long long)n;
    memcpy(base + 8, s, n);
    base[8 + n] = 0;
    return base + 8;
}

/* util.inspect's rendering of a function: `[Function: f]`,
 * `[AsyncFunction: f]`, `[GeneratorFunction: f]`,
 * `[AsyncGeneratorFunction: f]`, `[class C]`; an empty name reads
 * `(anonymous)` in place of `: name`. */
static char *fn_render(const FnView *v) {
    char buf[600];
    const char *tag = "Function";
    switch (v->kind) {
    case FN_ASYNC: tag = "AsyncFunction"; break;
    case FN_GEN: tag = "GeneratorFunction"; break;
    case FN_ASYNCGEN: tag = "AsyncGeneratorFunction"; break;
    }
    int n;
    if (v->kind == FN_CLASS)
        n = v->name[0] ? snprintf(buf, sizeof buf, "[class %s]", v->name)
                       : snprintf(buf, sizeof buf, "[class (anonymous)]");
    else
        n = v->name[0] ? snprintf(buf, sizeof buf, "[%s: %s]", tag, v->name)
                       : snprintf(buf, sizeof buf, "[%s (anonymous)]", tag);
    if (n < 0) n = 0;
    if ((size_t)n >= sizeof buf) n = (int)sizeof buf - 1;
    return kml_str(buf, (size_t)n);
}

void *__kml_fn_identity_dyn(void **rec);

/* Own properties of a function value (`f.x = 1`, `Object.defineProperty(f,
 * …)`): one dynamic-object bag per function identity — the closure header,
 * which every boxing of one function leads back to (__kml_fn_identity_dyn) —
 * in a table the program's lifetime keeps, as a function with properties is
 * rare. An extended record carries its own bag. The table lives in static
 * data (a -mm=gc root) and is locked for workers. */
typedef struct {
    void *id;
    void *bag;
} FnPropsEnt;
static FnPropsEnt *fn_props_tab;
static long long fn_props_n, fn_props_cap;
static volatile int fn_props_lock;

static void fn_props_acquire(void) {
    while (__atomic_exchange_n(&fn_props_lock, 1, __ATOMIC_ACQUIRE)) {
    }
}
static void fn_props_release(void) { __atomic_store_n(&fn_props_lock, 0, __ATOMIC_RELEASE); }

static void *fn_props_lookup(void *id) {
    void *bag = NULL;
    fn_props_acquire();
    for (long long i = 0; i < fn_props_n; i++)
        if (fn_props_tab[i].id == id) {
            bag = fn_props_tab[i].bag;
            break;
        }
    fn_props_release();
    return bag;
}

static void *fn_props_make(void *id, void *(*mk)(void), void (*seed)(void *, void **), void **rec) {
    void *bag = fn_props_lookup(id);
    if (bag) return bag;
    void *fresh = mk();
    seed(fresh, rec);
    fn_props_acquire();
    for (long long i = 0; i < fn_props_n; i++)
        if (fn_props_tab[i].id == id) {
            fn_props_release();
            return fn_props_tab[i].bag;
        }
    if (fn_props_n == fn_props_cap) {
        long long cap = fn_props_cap ? fn_props_cap * 2 : 16;
        fn_props_tab = (FnPropsEnt *)realloc(fn_props_tab, (size_t)cap * sizeof *fn_props_tab);
        fn_props_cap = cap;
    }
    fn_props_tab[fn_props_n].id = id;
    fn_props_tab[fn_props_n].bag = fresh;
    fn_props_n++;
    fn_props_release();
    return fresh;
}

/* The bag of a boxed function's record, or NULL when it has none. */
void *__kml_fn_props_dyn(void **rec) {
    if (!rec) return NULL;
    if ((long long)rec[2] & FN_EXT) return rec[4];
    return fn_props_lookup(__kml_fn_identity_dyn(rec));
}

/* The dynamic-object property read, recorded with the first bag (a program
 * with a bag has the dynamic-object runtime): `name`/`length` an own
 * property overrides are read through it. */
static long long (*fn_bag_get)(void *, const char *);

/* The bag of a boxed function's record, made with `mk` (the dynamic-object
 * constructor) on first use. */
void *__kml_fn_props_dyn_make(void **rec, void *(*mk)(void), long long (*get)(void *, const char *),
                              void (*seed)(void *, void **)) {
    fn_bag_get = get;
    if ((long long)rec[2] & FN_EXT) {
        if (!rec[4]) {
            void *fresh = mk();
            seed(fresh, rec);
            rec[4] = fresh;
        }
        return rec[4];
    }
    return fn_props_make(__kml_fn_identity_dyn(rec), mk, seed, rec);
}

/* Whether `key` is a function's own `name`/`length`, or (not ownOnly) a
 * member of Function.prototype. */
_Bool __kml_fn_has_builtin(const char *key, _Bool ownOnly) {
    static const char *const proto[] = {"apply", "bind", "call", "toString", "constructor",
                                        "hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable",
                                        "toLocaleString", "valueOf", NULL};
    if (!key) return 0;
    if (!strcmp(key, "name") || !strcmp(key, "length")) return 1;
    if (ownOnly) return 0;
    for (int i = 0; proto[i]; i++)
        if (!strcmp(key, proto[i])) return 1;
    return 0;
}

/* Whether `key` is an array's own index below `len` or `length`, or (not
 * ownOnly) a member of Array.prototype / Object.prototype — `key in arr`
 * and `arr.hasOwnProperty(key)` through `any`. Kept beside the function
 * form above: both answer membership for a value with no property bag. */
_Bool __kml_array_has_key(long long len, const char *key, _Bool ownOnly) {
    static const char *const proto[] = {
        "at", "concat", "copyWithin", "entries", "every", "fill", "filter", "find", "findIndex",
        "findLast", "findLastIndex", "flat", "flatMap", "forEach", "includes", "indexOf", "join",
        "keys", "lastIndexOf", "map", "pop", "push", "reduce", "reduceRight", "reverse", "shift",
        "slice", "some", "sort", "splice", "toLocaleString", "toReversed", "toSorted",
        "toSpliced", "toString", "unshift", "values", "with", "constructor", "hasOwnProperty",
        "isPrototypeOf", "propertyIsEnumerable", "valueOf", NULL};
    if (!key) return 0;
    if (!strcmp(key, "length")) return 1;
    if (key[0] >= '0' && key[0] <= '9' && (key[0] != '0' || key[1] == 0)) {
        long long i = 0;
        const char *p = key;
        for (; *p >= '0' && *p <= '9' && i < (1LL << 53); p++) i = i * 10 + (*p - '0');
        if (!*p) return i < len;
    }
    if (ownOnly) return 0;
    for (int i = 0; proto[i]; i++)
        if (!strcmp(key, proto[i])) return 1;
    return 0;
}

/* `[Function: f]` followed by ` { own: props }` when the bag has enumerable
 * keys — which past the depth limit collapses to the name-less
 * `[Function]`, as Node does for a function with keys. */
static char *fn_render_props(const FnView *v, void *bag, long long depth) {
    if (bag) {
        char *props = __kml_fn_props_inspect(bag, depth);
        if (props && props[0] && strcmp(props, "{}") != 0) {
            free(props - 8);
            if (depth > 2) return kml_str("[Function]", 10);
            props = __kml_fn_props_inspect(bag, depth);
            char *head = fn_render(v);
            size_t hn = strlen(head), pn = strlen(props);
            char *buf = (char *)malloc(hn + 1 + pn + 1);
            memcpy(buf, head, hn);
            buf[hn] = ' ';
            memcpy(buf + hn + 1, props, pn);
            char *out = kml_str(buf, hn + 1 + pn);
            free(buf);
            free(head - 8);
            free(props - 8);
            return out;
        }
        if (props && props[0]) free(props - 8);
    }
    return fn_render(v);
}

/* An own `name` (a string) or `length` (a number) defined on the function,
 * as a NaN-boxed word, or 0 when it has none. */
static long long fn_own(void *bag, const char *key, int wantString) {
    if (!bag || !fn_bag_get) return 0;
    long long w = fn_bag_get(bag, key);
    if (wantString) return (w > 16 && w < (1LL << 49) && (w & 7) == 0) ? w : 0;
    return w >= (1LL << 49) ? w : 0;
}

static long long fn_own_length(long long w) {
    union { long long i; double d; } u;
    u.i = w - (1LL << 49);
    return (long long)u.d;
}

/* The view's name replaced by an own `name` the function was given. */
static void fn_view_own_name(FnView *v, void *bag) {
    long long own = fn_own(bag, "name", 1);
    if (!own) return;
    const char *n = (const char *)(uintptr_t)own;
    size_t len = strlen(n);
    if (len > sizeof v->name - 1) len = sizeof v->name - 1;
    memcpy(v->name, n, len);
    v->name[len] = 0;
}

char *__kml_fn_inspect_hdr(void **hdr) {
    FnView v;
    fn_view_hdr(hdr, &v, 0);
    void *bag = hdr ? fn_props_lookup(hdr) : NULL;
    fn_view_own_name(&v, bag);
    return fn_render_props(&v, bag, 0);
}

char *__kml_fn_name_hdr(void **hdr) {
    long long own = fn_own(hdr ? fn_props_lookup(hdr) : NULL, "name", 1);
    if (own) return kml_str((const char *)(uintptr_t)own, strlen((const char *)(uintptr_t)own));
    FnView v;
    fn_view_hdr(hdr, &v, 0);
    return kml_str(v.name, strlen(v.name));
}

long long __kml_fn_length_hdr(void **hdr) {
    long long own = fn_own(hdr ? fn_props_lookup(hdr) : NULL, "length", 0);
    if (own) return fn_own_length(own);
    FnView v;
    fn_view_hdr(hdr, &v, 0);
    return v.length;
}

/* A tag-12 record {fnptr, env, arity}: its code pointer is either a natively
 * dynamic function (registered under its own name) or an adapter whose env is
 * the adapted closure header (registered FN_THROUGH_ENV); an extended record
 * names itself. */
static void fn_view_dyn_at(void **rec, FnView *v, int depth) {
    if (!rec) {
        fn_view_hdr(NULL, v, 0);
        return;
    }
    long long arity = (long long)rec[2];
    if (arity & FN_EXT) {
        const char *name = (const char *)rec[3];
        size_t n = name ? strlen(name) : 0;
        if (n > sizeof v->name - 1) n = sizeof v->name - 1;
        memcpy(v->name, name ? name : "", n);
        v->name[n] = 0;
        v->length = arity & ~FN_EXT;
        v->kind = FN_PLAIN;
        return;
    }
    fn_view_code(rec[0], rec[1], v, depth);
}

static void fn_view_dyn(void **rec, FnView *v) { fn_view_dyn_at(rec, v, 0); }

/* util.inspect of a boxed function at nesting `depth`. */
char *__kml_fn_inspect_dyn(void **rec, long long depth) {
    FnView v;
    fn_view_dyn(rec, &v);
    void *bag = __kml_fn_props_dyn(rec);
    fn_view_own_name(&v, bag);
    return fn_render_props(&v, bag, depth);
}

char *__kml_fn_name_dyn(void **rec) {
    long long own = fn_own(rec ? __kml_fn_props_dyn(rec) : NULL, "name", 1);
    if (own) return kml_str((const char *)(uintptr_t)own, strlen((const char *)(uintptr_t)own));
    FnView v;
    fn_view_dyn(rec, &v);
    return kml_str(v.name, strlen(v.name));
}

long long __kml_fn_length_dyn(void **rec) {
    long long own = fn_own(rec ? __kml_fn_props_dyn(rec) : NULL, "length", 0);
    if (own) return fn_own_length(own);
    FnView v;
    fn_view_dyn(rec, &v);
    return v.length;
}

/* The identity of a dynamic function record for listener removal: an
 * adapter's (through its env, however deep) is the adapted closure header,
 * so boxing one closure twice (through a typed adapter too) gives records
 * that compare equal. */
void *__kml_fn_identity_dyn(void **rec) {
    if (!rec) return NULL;
    void *fn = rec[0];
    void **env = (void **)rec[1];
    void *id = rec;
    for (int depth = 0; depth < FN_MAX_DEPTH && fn && env; depth++) {
        const KmlFnMeta *m = fn_find(fn);
        if (!m || !(m->kind & FN_THROUGH_ENV)) break;
        id = env;
        fn = env[0];
        env = (void **)env[1];
    }
    return id;
}

/* The identity of a closure header for `===`: an adapter's (through its
 * env, however deep) is the adapted closure header, so one function passed
 * through two differently typed slots stays itself. */
void *__kml_fn_identity_hdr(void **hdr) {
    for (int depth = 0; hdr && depth < FN_MAX_DEPTH; depth++) {
        const KmlFnMeta *m = hdr[0] ? fn_find(hdr[0]) : NULL;
        if (!m || !(m->kind & FN_THROUGH_ENV) || !hdr[1]) break;
        hdr = (void **)hdr[1];
    }
    return hdr;
}

/* The box of the dynamic function a closure header stands for, through any
 * adapters (FN_THROUGH_ENV, their env the adapted header) down to a
 * dynamic function's thunk (env { d2sTag, box }): 0 when there is none. */
long long __kml_fn_thunk_box(void **hdr, const void *d2sTag) {
    for (int depth = 0; hdr && depth < FN_MAX_DEPTH; depth++) {
        void **env = (void **)hdr[1];
        if (env && env[0] == d2sTag) return ((long long *)env)[1];
        const KmlFnMeta *m = hdr[0] ? fn_find(hdr[0]) : NULL;
        if (!m || !(m->kind & FN_THROUGH_ENV)) return 0;
        hdr = env;
    }
    return 0;
}
