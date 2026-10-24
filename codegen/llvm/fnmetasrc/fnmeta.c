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
    const char *src; /* its source text (Function.prototype.toString), or NULL */
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
    FN_SLOPPY = 0x1000,     /* a sloppy (plain `function`) function: own caller/arguments */
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
 * dynjson.c has no function that can carry properties. */
static struct { long long len; char z[8]; } fn_empty_str;
/* dynjson.c replaces this when linked; without it no function carries
 * properties. */
__attribute__((weak)) char *__kml_dynobj_inspect_at(char *o, long long depth) {
    (void)o; (void)depth;
    return fn_empty_str.z;
}
static char *__kml_fn_props_inspect(void *props, long long depth) {
    if (*(long long *)((char *)props + 24) != 0) return __kml_dynobj_inspect_at((char *)props, depth);
    return fn_empty_str.z;
}

char *__kml_fn_name_dyn(void **rec);
long long __kml_fn_length_dyn(void **rec);

/* A function's new property bag starts with its own `length` and `name`, as
 * Node's function has them: not writable, not enumerable, but configurable.
 * Passed to __kml_fn_props_dyn_make by the compiler. KML_NB_DOUBLE_OFFSET and
 * KML_DYN_ATTR_CONFIGURABLE come from fnmeta.go. */
static void fn_seed(void *bag, void **rec);
void __kml_fn_bag_seed(void *bag, void **rec) { fn_seed(bag, rec); }

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
    int sloppy; /* FN_SLOPPY: its caller/arguments read, not throw */
    const char *src; /* its source text, NULL for a native or bound one */
} FnView;

static void fn_view_hdr(void **hdr, FnView *out, int depth);
static void fn_view_dyn_at(void **rec, FnView *v, int depth);

/* A boxed function value's record (a tag-12 box: the pointer under the low
 * tag bits). */
static void **fn_box_rec(long long box) { return (void **)(uintptr_t)(box & ~7LL); }

static void fn_view_code(void *fn, void *env, FnView *out, int depth) {
    const KmlFnMeta *m = depth < FN_MAX_DEPTH && fn ? fn_find(fn) : NULL;
    out->src = NULL;
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
        out->sloppy = 0;
        out->src = NULL;
        return;
    }
    size_t n = strlen(m->name);
    if (n > sizeof out->name - 1) n = sizeof out->name - 1;
    memcpy(out->name, m->name, n);
    out->name[n] = 0;
    out->length = m->length;
    out->kind = (int)(m->kind & 0xff);
    out->sloppy = (m->kind & FN_SLOPPY) != 0;
    out->src = m->src;
}

static void fn_view_hdr(void **hdr, FnView *out, int depth) {
    if (!hdr) {
        out->src = NULL;
        out->name[0] = 0;
        out->length = 0;
        out->kind = FN_PLAIN;
        return;
    }
    fn_view_code(hdr[0], hdr[1], out, depth);
}

static char *kml_str(const char *s, size_t n) {
    char *base = (char *)kml_alloc(KML_HEAP_RAW, 8 + n + 1);
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

extern void __kml_dynobj_mark_fn(void *o);
static void *fn_props_make(void *id, void *(*mk)(void), void (*seed)(void *, void **), void **rec) {
    void *bag = fn_props_lookup(id);
    if (bag) return bag;
    void *fresh = mk();
    /* Its own properties only: a miss reads on in Function.prototype, not
     * in Object.prototype's bag. */
    __kml_dynobj_mark_fn(fresh);
    seed(fresh, rec);
    fn_props_acquire();
    for (long long i = 0; i < fn_props_n; i++)
        if (fn_props_tab[i].id == id) {
            fn_props_release();
            return fn_props_tab[i].bag;
        }
    if (fn_props_n == fn_props_cap) {
        long long cap = fn_props_cap ? fn_props_cap * 2 : 16;
        fn_props_tab = (FnPropsEnt *)kml_realloc(KML_HEAP_WORDS, fn_props_tab, (size_t)cap * sizeof *fn_props_tab);
        fn_props_cap = cap;
    }
    fn_props_tab[fn_props_n].id = id;
    fn_props_tab[fn_props_n].bag = fresh;
    fn_props_n++;
    fn_props_release();
    return fresh;
}

/* A static of the builtin constructor a function record is the value of
 * (`N.isInteger` with `const N: any = Number`), by the record's identity;
 * undefined for any other function. Codegen registers one row per
 * constructor used by value (registerFnStatics). */
#define KML_UNIT_FNSTATIC 9
long long __kml_fn_caller_get(void **rec, int arguments);
long long __kml_fn_static_get(void **rec, const char *key) {
    if (key && (strcmp(key, "caller") == 0 || strcmp(key, "arguments") == 0))
        return __kml_fn_caller_get(rec, key[0] == 'a');
    const long long *row = (const long long *)__kml_unit_find(KML_UNIT_FNSTATIC, (long long)(intptr_t)__kml_fn_identity_dyn(rec));
    if (!row) return 10; /* undefined */
    return ((long long (*)(const char *))(intptr_t)row[1])(key);
}

/* The bag of a boxed function's record, or NULL when it has none. A
 * generator function always has one: it owns its `prototype`. */
static void fn_view_dyn(void **rec, FnView *v);
extern void *__kml_dynobj_new(void);
void *__kml_fn_props_dyn(void **rec) {
    if (!rec) return NULL;
    if ((long long)rec[2] & FN_EXT) return rec[4];
    void *id = __kml_fn_identity_dyn(rec);
    void *bag = fn_props_lookup(id);
    if (bag) return bag;
    FnView fv;
    fn_view_dyn(rec, &fv);
    if (fv.kind == FN_GEN || fv.kind == FN_ASYNCGEN) return fn_props_make(id, __kml_dynobj_new, __kml_fn_bag_seed, rec);
    return NULL;
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

/* Whether `key` is a member of Function.prototype (which has its own
 * `name` and `length` too). */
_Bool __kml_fn_has_proto_member(const char *key) {
    static const char *const proto[] = {"apply", "bind", "call", "toString", "constructor", "name", "length",
                                        "hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable",
                                        "toLocaleString", "valueOf", NULL};
    if (!key) return 0;
    for (int i = 0; proto[i]; i++)
        if (!strcmp(key, proto[i])) return 1;
    return 0;
}

/* Whether `key` is a function's own `name`/`length` (a function with no
 * property bag yet has both), or (not ownOnly) a member of
 * Function.prototype. */
_Bool __kml_fn_has_builtin(const char *key, _Bool ownOnly) {
    if (!key) return 0;
    if (!strcmp(key, "name") || !strcmp(key, "length")) return 1;
    return !ownOnly && __kml_fn_has_proto_member(key);
}

/* Whether `key` is an array's own index below `len`, `length` or a property
 * added to it, or (not
 * ownOnly) a member of Array.prototype / Object.prototype — `key in arr`
 * and `arr.hasOwnProperty(key)` through `any`. Kept beside the function
 * form above: both answer membership for a value with no property bag. */
/* Whether key was added to obj as its own property (shape.c); this weak
 * default stands in when the program never adds one. */
__attribute__((weak)) int __kml_shape_expando_has(void *obj, const char *key) {
    (void)obj;
    (void)key;
    return 0;
}

/* Whether element i of an array header is a hole (holes.c); none when the
 * program cannot make one. */
__attribute__((weak)) int __kml_holes_is(void *hdr, long long i) {
    (void)hdr;
    (void)i;
    return 0;
}

/* Whether element i of a dynamic array is a hole (dynarr.c). */
__attribute__((weak)) int __kml_dynarr_is_hole(void *ap, long long i) {
    (void)ap;
    (void)i;
    return 0;
}

/* word is the array's box (0 for a string): a property added to an array
 * lives in shape.c's table, keyed by a boxed static array's header or by
 * the dynamic array itself. */
_Bool __kml_array_has_key(long long len, const char *key, _Bool ownOnly, long long word) {
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
        if (!*p) {
            if (i >= len) return 0;
            /* a boxed static array's hole is no own index */
            unsigned long long hw = (unsigned long long)word;
            if (hw >= 65536 && hw < (1ULL << 49) && (hw & 7) == 2)
                return !__kml_holes_is(*(void **)(hw & ~7ULL), i);
            if (hw >= 65536 && hw < (1ULL << 49) && (hw & 7) == 6)
                return !__kml_dynarr_is_hole((void *)(hw & ~7ULL), i);
            return 1;
        }
    }
    unsigned long long w = (unsigned long long)word;
    if (w >= 65536 && w < (1ULL << 49) && ((w & 7) == 2 || (w & 7) == 6)) {
        void *arr = (w & 7) == 2 ? *(void **)(w & ~7ULL) : (void *)(w & ~7ULL);
        if (__kml_shape_expando_has(arr, key)) return 1;
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
            kml_free(KML_HEAP_RAW, props - 8);
            if (depth > 2) return kml_str("[Function]", 10);
            props = __kml_fn_props_inspect(bag, depth);
            char *head = fn_render(v);
            size_t hn = strlen(head), pn = strlen(props);
            char *buf = (char *)malloc(hn + 1 + pn + 1); /* scratch */
            memcpy(buf, head, hn);
            buf[hn] = ' ';
            memcpy(buf + hn + 1, props, pn);
            char *out = kml_str(buf, hn + 1 + pn);
            free(buf); /* scratch */
            kml_free(KML_HEAP_RAW, head - 8);
            kml_free(KML_HEAP_RAW, props - 8);
            return out;
        }
        if (props && props[0]) kml_free(KML_HEAP_RAW, props - 8);
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

/* The own-property bag of the function a closure header stands for: its
 * own, else, through an adapter (FN_THROUGH_ENV) or a dynamic function's
 * thunk (FN_THROUGH_BOX), the adapted one's. */
static void *fn_hdr_bag(void **hdr) {
    for (int depth = 0; hdr && depth < FN_MAX_DEPTH; depth++) {
        void *bag = fn_props_lookup(hdr);
        if (bag) return bag;
        const KmlFnMeta *m = hdr[0] ? fn_find(hdr[0]) : NULL;
        if (!m || !hdr[1]) return NULL;
        if (m->kind & FN_THROUGH_ENV) {
            hdr = (void **)hdr[1];
            continue;
        }
        if (m->kind & FN_THROUGH_BOX) return __kml_fn_props_dyn(fn_box_rec(((long long *)hdr[1])[1]));
        return NULL;
    }
    return NULL;
}

char *__kml_fn_inspect_hdr(void **hdr) {
    FnView v;
    fn_view_hdr(hdr, &v, 0);
    void *bag = fn_hdr_bag(hdr);
    fn_view_own_name(&v, bag);
    return fn_render_props(&v, bag, 0);
}

char *__kml_fn_name_hdr(void **hdr) {
    long long own = fn_own(fn_hdr_bag(hdr), "name", 1);
    if (own) return kml_str((const char *)(uintptr_t)own, strlen((const char *)(uintptr_t)own));
    FnView v;
    fn_view_hdr(hdr, &v, 0);
    return kml_str(v.name, strlen(v.name));
}

long long __kml_fn_length_hdr(void **hdr) {
    long long own = fn_own(fn_hdr_bag(hdr), "length", 0);
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

/* A boxed function's Object.prototype.toString tag: its kind's
 * prototype's Symbol.toStringTag, or plain "Function". */
const char *__kml_fn_tag_dyn(void **rec) {
    FnView v;
    fn_view_dyn(rec, &v);
    switch (v.kind) {
    case FN_ASYNC: return "AsyncFunction";
    case FN_GEN: return "GeneratorFunction";
    case FN_ASYNCGEN: return "AsyncGeneratorFunction";
    }
    return "Function";
}

char *__kml_fn_name_dyn(void **rec) {
    long long own = fn_own(rec ? __kml_fn_props_dyn(rec) : NULL, "name", 1);
    if (own) return kml_str((const char *)(uintptr_t)own, strlen((const char *)(uintptr_t)own));
    FnView v;
    fn_view_dyn(rec, &v);
    return kml_str(v.name, strlen(v.name));
}

/* String(f) of a boxed function: a compiled function's source text, its
 * types blanked as Node's type stripping leaves them; else the
 * native-function form with its name (`function parseInt() { [native code]
 * }`). */
char *__kml_fn_tostring_dyn(void **rec) {
    FnView sv;
    fn_view_dyn(rec, &sv);
    if (sv.src) return kml_str(sv.src, strlen(sv.src));
    char *name = __kml_fn_name_dyn(rec);
    char buf[600];
    int n = snprintf(buf, sizeof buf, "function %s() { [native code] }", name);
    if (n < 0) n = 0;
    if ((size_t)n >= sizeof buf) n = (int)sizeof buf - 1;
    return kml_str(buf, (size_t)n);
}

/* String(K) of a constructor reference (its payload, the constructor's
 * name): a class's source text, else the native-function form. */
char *__kml_classref_tostring(const char *name) {
    const KmlFnMeta *m = fn_find((void *)name);
    if (m && m->src) return kml_str(m->src, strlen(m->src));
    char buf[600];
    int n = snprintf(buf, sizeof buf, "function %s() { [native code] }", name ? name : "");
    if (n < 0) n = 0;
    if ((size_t)n >= sizeof buf) n = (int)sizeof buf - 1;
    return kml_str(buf, (size_t)n);
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
        if (m && (m->kind & FN_THROUGH_BOX)) {
            /* A thunk of a function unboxed from `any`: that function. */
            void **inner = fn_box_rec(((long long *)env)[1]);
            if (!inner) break;
            id = inner;
            fn = inner[0];
            env = (void **)inner[1];
            continue;
        }
        if (!m || !(m->kind & FN_THROUGH_ENV)) break;
        id = env;
        fn = env[0];
        env = (void **)env[1];
    }
    return id;
}

/* The identity of a closure header for `===`: an adapter's (through its
 * env, however deep) is the adapted closure header, and a thunk of a
 * function unboxed from `any` is that function's, so one function passed
 * through differently typed slots stays itself. */
void *__kml_fn_identity_hdr(void **hdr) {
    return hdr ? __kml_fn_identity_dyn(hdr) : NULL;
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

/* f.caller / f.arguments (callerprops.go). A program reading one sets
 * __kml_caller_frames, and each frame record then carries its function's
 * box (1 for the module's frame, 0 for a strict function's) and its
 * arguments object. */
int __kml_caller_frames;
extern _Thread_local void *__kml_frame_top;
extern void __kml_throw_nullderef(const char *msg) __attribute__((noreturn));
typedef struct kml_cframe {
    struct kml_cframe *prev;
    const void *desc;
    long long pos, self, args;
} kml_cframe;
static struct { long long len; char z[136]; } fn_strict_msg = {
    134, "'caller', 'callee', and 'arguments' properties may not be accessed on strict mode functions or the arguments objects for calls to them"};
/* The CommonJS module wrapper, the caller of a top-level call: a function
 * of five parameters doing nothing. */
static long long fn_cjs_wrapper(void *env, long long thisv, long long argc, long long *argv) {
    (void)env; (void)thisv; (void)argc; (void)argv;
    return KML_NB_UNDEFINED;
}
static void *fn_cjs_rec[3] = {(void *)fn_cjs_wrapper, NULL, (void *)5};

long long __kml_fn_caller_get(void **rec, int arguments) {
    FnView v;
    v.sloppy = 0;
    fn_view_dyn(rec, &v);
    if (!v.sloppy) __kml_throw_nullderef(fn_strict_msg.z);
    if (!__kml_caller_frames) return KML_NB_NULL;
    void *id = __kml_fn_identity_dyn(rec);
    for (kml_cframe *f = (kml_cframe *)__kml_frame_top; f; f = f->prev) {
        if (f->self <= 1 || __kml_fn_identity_dyn(fn_box_rec(f->self)) != id) continue;
        if (arguments) return f->args ? f->args : KML_NB_NULL;
        kml_cframe *g = f->prev;
        while (g && !g->desc) g = g->prev;
        if (!g || !g->self) return KML_NB_NULL;
        if (g->self == 1) return (long long)(uintptr_t)fn_cjs_rec | 7;
        return g->self;
    }
    return KML_NB_NULL;
}

/* Whether a function owns key as a sloppy function owns `caller` and
 * `arguments` (its read-only own properties; a strict one has none). */
_Bool __kml_fn_owns_restricted(void **rec, const char *key) {
    if (!key || (strcmp(key, "caller") != 0 && strcmp(key, "arguments") != 0)) return 0;
    FnView v;
    v.sloppy = 0;
    fn_view_dyn(rec, &v);
    return v.sloppy;
}

/* A write of f.caller / f.arguments: a strict function's (a generator, an
 * arrow, a method) reaches Function.prototype's accessor, whose setter
 * throws; a sloppy function's own properties are read-only (1); 0 for any
 * other key. */
int __kml_fn_restricted_set(void **rec, const char *key) {
    if (!key || (strcmp(key, "caller") != 0 && strcmp(key, "arguments") != 0)) return 0;
    FnView v;
    v.sloppy = 0;
    fn_view_dyn(rec, &v);
    if (!v.sloppy) __kml_throw_nullderef(fn_strict_msg.z);
    return 1;
}

/* Generator prototypes. A generator function's own `prototype` is a fresh
 * object inheriting %GeneratorPrototype% (%AsyncGeneratorPrototype%), made
 * with its property bag; an instance inherits the `prototype` its function
 * had when it was called, else the root when that was not an object. The
 * roots, and %GeneratorFunction.prototype% (the generator function's own
 * prototype, holding the root as `prototype`), are made on first use. */
extern void *__kml_dynobj_new(void);
extern _Bool __kml_dynobj_set_proto(void *o, void *proto);
extern long long __kml_dynobj_get(void *o, const char *key);
extern void *__kml_object_prototype(void);
#define GEN_BOX_BAG(p) ((long long)(uintptr_t)(p) | 5)
#define GEN_IS_BAG(w) (((w) & 7) == 5 && ((unsigned long long)(w) >> 48) == 0)
static void *gen_root[2], *genfn_proto[2];

static void gen_define(void *bag, const char *key, long long v, long long attrs) {
    __kml_dynobj_set(bag, key, v);
    long long i = __kml_dynobj_find(bag, key);
    __kml_dynobj_patch(bag, i, __kml_dynobj_rawtag_at(bag, i), __kml_dynobj_rawpay_at(bag, i), attrs);
}

void *__kml_gen_root(int async) {
    if (!gen_root[async]) {
        void *b = __kml_dynobj_new();
        __kml_dynobj_set_proto(b, __kml_object_prototype());
        static struct { long long len; char z[16]; } tags[2] = {{KML_STR_ASCII_HDR(9), "Generator"}, {KML_STR_ASCII_HDR(14), "AsyncGenerator"}};
        gen_define(b, "@@toStringTag", (long long)(uintptr_t)tags[async].z, KML_DYN_ATTR_CONFIGURABLE);
        gen_root[async] = b;
    }
    return gen_root[async];
}

/* %GeneratorFunction.prototype%: inherits Function.prototype (fnProto, a
 * bag box or 0), its `prototype` the root, the root's `constructor` it. */
long long __kml_genfn_proto(int async, long long fnProto) {
    if (!genfn_proto[async]) {
        void *b = __kml_dynobj_new();
        __kml_dynobj_set_proto(b, GEN_IS_BAG(fnProto) ? (void *)(uintptr_t)(fnProto & ~7LL) : __kml_object_prototype());
        void *root = __kml_gen_root(async);
        gen_define(b, "prototype", GEN_BOX_BAG(root), KML_DYN_ATTR_CONFIGURABLE);
        gen_define(root, "constructor", GEN_BOX_BAG(b), KML_DYN_ATTR_CONFIGURABLE);
        genfn_proto[async] = b;
    }
    return GEN_BOX_BAG(genfn_proto[async]);
}

static void gen_seed_for(void *bag, void **rec) {
    FnView fv;
    fn_view_dyn(rec, &fv);
    if (fv.kind != FN_GEN && fv.kind != FN_ASYNCGEN) return;
    void *p = __kml_dynobj_new();
    __kml_dynobj_set_proto(p, __kml_gen_root(fv.kind == FN_ASYNCGEN));
    gen_define(bag, "prototype", GEN_BOX_BAG(p), 1 /* writable */);
}

/* The property bag of the generator function a closure header is the value
 * of, made (seeded with its `prototype`) on first use. */
void *__kml_gen_fn_bag(void **hdr) {
    void *rec[3] = {hdr[0], hdr[1], 0};
    return fn_props_make(__kml_fn_identity_dyn((void **)hdr), __kml_dynobj_new, __kml_fn_bag_seed, rec);
}

/* The prototype an instance of a generator function takes: the function's
 * `prototype` when it is an object, else the root. bag may be NULL (a
 * function whose value is not tracked), which takes the root. */
long long __kml_gen_instance_proto(void *bag, int async) {
    if (bag) {
        long long i = __kml_dynobj_find(bag, "prototype");
        if (i >= 0) {
            long long v = __kml_dynobj_get(bag, "prototype");
            if (GEN_IS_BAG(v)) return v;
        }
    }
    return GEN_BOX_BAG(__kml_gen_root(async));
}

extern void *__kml_dynobj_get_proto(void *o);
extern int __kml_shape_generator_kind(void *obj);
extern long long __kml_shape_generator_proto(void *obj, int kind);

/* OrdinaryHasInstance(c, v) for c a generator function: whether c's
 * `prototype` is on v's prototype chain (a generator instance's first
 * link is the prototype it took when made). 0 for any other c. */
int __kml_gen_has_instance(long long v, long long c) {
    unsigned long long cw = (unsigned long long)c, vw = (unsigned long long)v;
    if (cw < 65536 || cw >= (1ULL << 49) || (cw & 7) != 7) return 0;
    void **rec = (void **)(uintptr_t)(cw & ~7ULL);
    FnView fv;
    fn_view_dyn(rec, &fv);
    if (fv.kind != FN_GEN && fv.kind != FN_ASYNCGEN) return 0;
    void *bag = __kml_fn_props_dyn(rec);
    long long p = bag ? __kml_dynobj_get(bag, "prototype") : 10;
    if (!GEN_IS_BAG(p) || vw < 65536 || vw >= (1ULL << 49)) return 0;
    void *target = (void *)(uintptr_t)(p & ~7LL), *o = (void *)(uintptr_t)(vw & ~7ULL), *cur = NULL;
    if ((vw & 7) == 1) {
        int k = __kml_shape_generator_kind(o);
        long long first = k ? __kml_shape_generator_proto(o, k) : 0;
        if (!GEN_IS_BAG(first)) return 0;
        cur = (void *)(uintptr_t)(first & ~7LL);
    } else if ((vw & 7) == 5)
        cur = __kml_dynobj_get_proto(o);
    for (int hops = 0; cur && hops < 100000; hops++) {
        if (cur == target) return 1;
        void *next = __kml_dynobj_get_proto(cur);
        if (next == cur) break;
        cur = next;
    }
    return 0;
}

/* A bag's prototype as getPrototypeOf reports it: Function.prototype
 * (fnProto) for a %GeneratorFunction.prototype%, else proto, its own. */
long long __kml_genfn_proto_parent(void *bag, long long fnProto, long long proto) {
    return bag && (bag == genfn_proto[0] || bag == genfn_proto[1]) ? fnProto : proto;
}

/* A function's prototype: %GeneratorFunction.prototype% for a generator
 * function, else Function.prototype (fnProto). */
long long __kml_fn_proto_of(void **rec, long long fnProto) {
    FnView fv;
    fn_view_dyn(rec, &fv);
    if (fv.kind == FN_GEN || fv.kind == FN_ASYNCGEN) return __kml_genfn_proto(fv.kind == FN_ASYNCGEN, fnProto);
    return fnProto;
}

/* The seed itself: `length` and `name` from the function's own view (its
 * bag is being made, so nothing overrides them yet), and a generator
 * function's `prototype`. */
static void fn_seed(void *bag, void **rec) {
    FnView fv;
    fn_view_dyn(rec, &fv);
    double len = (double)fv.length;
    long long lenbits;
    memcpy(&lenbits, &len, 8);
    gen_define(bag, "length", lenbits + KML_NB_DOUBLE_OFFSET, KML_DYN_ATTR_CONFIGURABLE);
    gen_define(bag, "name", (long long)(uintptr_t)kml_str(fv.name, strlen(fv.name)), KML_DYN_ATTR_CONFIGURABLE);
    if (fv.kind == FN_GEN || fv.kind == FN_ASYNCGEN) gen_seed_for(bag, rec);
}
