/* toprim.c — runtime ToPrimitive over a NaN-boxed dynamic value
 * (TDD-00201 Stage 4, `-compat=js`; in C per TDD-00240). Under compat=js a
 * plain object literal is a dynamic bag (tag 10), so its methods are looked
 * up and invoked at run time: Symbol.toPrimitive first, then valueOf /
 * toString in hint order (valueOf then toString for number/default,
 * toString then valueOf for string); the first callable whose result is a
 * primitive (tag < 6) wins, else the object renders as itself. A
 * non-object box passes through unchanged. */

#include <stdlib.h>
#include <string.h>

typedef long long i64;
typedef unsigned long long u64;

extern i64 __kml_obj_get(void *o, const void *key);
extern i64 __kml_dynobj_get(void *o, const void *key);
extern void *__kml_array_join(void *arr);
extern void *__kml_dynarr_join(void *arr);
extern void *__kml_fn_props_dyn(void *rec);
/* A host box's primitive (a Date's), or the box itself; defined with the
 * program's host rows. */
extern i64 __kml_host_toprim(i64 v, int hint);

/* Length-prefixed string constants, laid out as the compiler's own. */
#define KSTR(id, s) static const struct { i64 len; char d[sizeof(s)]; } id = { sizeof(s) - 1, s }
KSTR(k_vo, "valueOf");
KSTR(k_ts, "toString");
KSTR(k_tp, "@@toPrimitive");
KSTR(k_num, "number");
KSTR(k_str, "string");
KSTR(k_def, "default");
KSTR(k_obj, "[object Object]");
KSTR(k_tag, "@@toStringTag");
KSTR(k_noprim, "Cannot convert object to primitive value");

extern void __kml_throw_nullderef(const char *msg) __attribute__((noreturn));

/* mirrors __kml_nb_tag / __kml_nb_pay (runtime_nanbox.go) */
static int nb_tag(i64 w) {
    u64 v = (u64)w;
    static const int kindTag[8] = {2, 6, 7, 8, 9, 10, 11, 12};
    if (v >= (1ULL << 49)) return 1;
    if (v < 65536) return v == 2 ? 4 : (v == 6 || v == 7) ? 3 : 5;
    return kindTag[v & 7];
}
static void *nb_ptr(i64 w) {
    u64 v = (u64)w;
    return (void *)((v & 7) == 0 ? v : v & ~7ULL);
}

/* A function box is a record { fn, env } called (env, this, argc, argv). */
typedef i64 (*kml_fn)(void *env, i64 recv, i64 argc, i64 *argv);

static i64 invoke(i64 fnbox, i64 recv, i64 argc, i64 *argv) {
    void **rec = (void **)nb_ptr(fnbox);
    return ((kml_fn)rec[0])(rec[1], recv, argc, argv);
}

/* A key of the object being converted: a dynamic bag's or function's own
 * property, or a static object's through its layout row. */
static i64 tp_get(void *bag, int isstatic, const void *key) {
    return isstatic ? __kml_obj_get(bag, key) : __kml_dynobj_get(bag, key);
}

i64 __kml_toprimitive(i64 v, int hint) {
    hint &= 0xff;
    int tag = nb_tag(v), isobj = tag == 10, isstatic = 0;
    void *bag = nb_ptr(v);
    if (isobj) {
        /* a dynamic bag */
    } else if (tag == 6) {
        i64 hr = __kml_host_toprim(v, hint);
        if (hr != v) return hr;
        if (!bag) return v;
        isstatic = 1; /* a static object's own methods, by its layout */
    } else if (tag == 7) {
        return (i64)__kml_array_join(bag); /* Array.prototype.join */
    } else if (tag == 11) {
        return (i64)__kml_dynarr_join(bag);
    } else if (tag == 12) {
        /* a function's own properties take part as an object's do */
        bag = __kml_fn_props_dyn(bag);
        if (!bag) return v;
    } else {
        return v;
    }
    i64 tp = tp_get(bag, isstatic, k_tp.d);
    if (nb_tag(tp) == 12) {
        i64 hb = hint == 2 ? (i64)(u64)k_def.d : hint == 1 ? (i64)(u64)k_str.d : (i64)(u64)k_num.d;
        i64 argv[1] = {hb};
        return invoke(tp, v, 1, argv);
    }
    const void *first = hint == 1 ? k_ts.d : k_vo.d, *second = hint == 1 ? k_vo.d : k_ts.d;
    const void *order[2] = {first, second};
    int ownToStringObject = 0;
    for (int i = 0; i < 2; i++) {
        i64 f = tp_get(bag, isstatic, order[i]);
        if (nb_tag(f) != 12) {
            /* A dynamic object without its own toString has
             * Object.prototype's, which always gives a primitive: the ladder
             * ends there (`String({ valueOf() { return 1 } })` is
             * "[object Object]"). */
            if (isobj && order[i] == k_ts.d) break;
            continue;
        }
        i64 argv[1];
        i64 r = invoke(f, v, 0, argv);
        if (nb_tag(r) < 6) return r;
        if (order[i] == k_ts.d) ownToStringObject = 1;
    }
    /* OrdinaryToPrimitive: the object's own toString returned an object and
     * no method gave a primitive (the inherited valueOf returns the object
     * itself), so there is no primitive value. */
    if (ownToStringObject) __kml_throw_nullderef(k_noprim.d);
    /* A static object without such a method renders as itself (an Error's
     * "name: message", a Symbol's description); a dynamic one is
     * Object.prototype.toString's `[object Tag]`, its Symbol.toStringTag
     * when a string. */
    if (!isobj) return v;
    i64 t = tp_get(bag, 0, k_tag.d);
    if (nb_tag(t) != 2) return (i64)(u64)k_obj.d;
    const char *name = (const char *)nb_ptr(t);
    i64 n = *(const i64 *)(name - 8);
    char *s = (char *)malloc((size_t)n + 18);
    *(i64 *)s = n + 9;
    memcpy(s + 8, "[object ", 8);
    memcpy(s + 16, name, (size_t)n);
    memcpy(s + 16 + n, "]", 2);
    return (i64)(u64)(s + 8);
}

/* ToNumber of a heap value that is not a string (@__kml_any_tonum's object
 * arm): ToPrimitive with the number hint, then ToNumber of the primitive. A
 * value with no primitive conversion is NaN. */
extern double __kml_any_tonum(i64 v);

double __kml_obj_tonum(i64 v) {
    i64 p = __kml_toprimitive(v, 0);
    if (p == v || nb_tag(p) >= 6) return __builtin_nan("");
    return __kml_any_tonum(p);
}
