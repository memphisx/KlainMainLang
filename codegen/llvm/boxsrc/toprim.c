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
extern i64 __kml_dynobj_find(void *o, const char *key);
extern _Bool __kml_dynobj_chain_nullproto(void *o);
extern void *__kml_array_join(void *arr);
extern void *__kml_dynarr_join(void *arr);
extern void *__kml_fn_props_dyn(void *rec);
extern char *__kml_fn_tostring_dyn(void **rec);
/* A host box's primitive (a Date's), or the box itself; defined with the
 * program's host rows. */
extern i64 __kml_host_toprim(i64 v, int hint);

/* Length-prefixed string constants, laid out as the compiler's own. */
#define KSTR(id, s) static const struct { i64 len; char d[sizeof(s)]; } id = { KML_STR_ASCII_HDR(sizeof(s) - 1), s }
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
extern void __kml_require_callable(long long word, const char *type) __attribute__((weak));
extern char *__kml_object_tostring(long long word) __attribute__((weak));

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

/* Whether v boxes a bigint (its cell's magic, emit_bigint_box.go). */
int __kml_any_is_bigint(i64 v) {
    void *p = nb_ptr(v);
    return nb_tag(v) == 6 && p && *(const u64 *)p == 0x7FF40000B1616B16ULL;
}

/* Whether v boxes a symbol: an object-kind box whose header has the symbol
 * bit (1 << 47). */
static int is_symbol_box(i64 v) {
    u64 w = (u64)v;
    return w >= 65536 && w < (1ULL << 49) && (w & 7) == 1 && (*(const i64 *)(w & ~7ULL) & (1LL << 47));
}

KSTR(k_symnum, "Cannot convert a Symbol value to a number");
KSTR(k_symstr, "Cannot convert a Symbol value to a string");

/* The TypeError converting a symbol primitive is, when v is one: to a
 * string (tostr) or to a number. */
void __kml_any_symbol_guard(i64 v, int tostr) {
    if (is_symbol_box(v)) __kml_throw_nullderef(tostr ? k_symstr.d : k_symnum.d);
}

i64 __kml_toprimitive(i64 v, int hint) {
    hint &= 0xff;
    if (is_symbol_box(v)) return v; /* a symbol is a primitive */
    int tag = nb_tag(v), isobj = tag == 10, isstatic = 0;
    void *bag = nb_ptr(v);
    /* A function: Function.prototype.toString ends its ladder (valueOf
     * returns the function itself), its source text. */
    void **fnrec = 0;
    if (isobj) {
        /* a dynamic bag */
    } else if (tag == 6) {
        /* A boxed bigint's cell (emit_bigint_box.go's magic): a primitive. */
        if (__kml_any_is_bigint(v)) return v;
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
        fnrec = (void **)bag;
        bag = __kml_fn_props_dyn(bag);
        if (!bag) return (i64)(u64)__kml_fn_tostring_dyn(fnrec);
    } else {
        return v;
    }
    i64 tp = tp_get(bag, isstatic, k_tp.d);
    int tpt = nb_tag(tp);
    if (tpt != 12 && tpt != 4 && tpt != 5 && !(isstatic && tp == 0)) {
        /* GetMethod: a Symbol.toPrimitive that is neither nullish nor
         * callable is a TypeError (`number 1 is not a function`). */
        const char *ty = tpt == 1 ? "number" : tpt == 2 ? "string" : tpt == 3 ? "boolean"
                       : __kml_any_is_bigint(tp) ? "bigint" : is_symbol_box(tp) ? "symbol" : "object";
        if (__kml_require_callable) __kml_require_callable(tp, ty);
        __kml_throw_nullderef(k_noprim.d);
    }
    if (tpt == 12) {
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
            /* An own toString that is not callable (`toString: undefined`)
             * is skipped as GetMethod skips it, on to valueOf. */
            if (isobj && order[i] == k_ts.d) {
                /* (A null-prototype chain has no Object.prototype.toString:
                 * on to valueOf, then TypeError.) */
                if (__kml_dynobj_find(bag, k_ts.d) < 0 && !__kml_dynobj_chain_nullproto(bag)) break;
                ownToStringObject = 1; /* no Object.prototype.toString to end on */
            }
            /* A class instance without a toString of its own has
             * Object.prototype's too: `[object Tag]` ends the ladder before
             * valueOf (`String(x)` of one with only valueOf). An Error's
             * is its own rendering, below. */
            if (isstatic && order[i] == k_ts.d && __kml_object_tostring && !(*(i64 *)bag & (1LL << 48)))
                return (i64)(u64)__kml_object_tostring(v);
            continue;
        }
        i64 argv[1];
        i64 r = invoke(f, v, 0, argv);
        if (nb_tag(r) < 6 || __kml_any_is_bigint(r) || is_symbol_box(r)) return r;
        if (order[i] == k_ts.d) ownToStringObject = 1;
    }
    /* OrdinaryToPrimitive: the object's own toString returned an object and
     * no method gave a primitive (the inherited valueOf returns the object
     * itself), so there is no primitive value. */
    if (ownToStringObject) __kml_throw_nullderef(k_noprim.d);
    if (fnrec) return (i64)(u64)__kml_fn_tostring_dyn(fnrec);
    /* A static object without such a method renders as itself (an Error's
     * "name: message", a Symbol's description); a dynamic one is
     * Object.prototype.toString's `[object Tag]`, its Symbol.toStringTag
     * when a string. */
    if (!isobj) {
        /* A plain static object (no Error flag) is Object.prototype's
         * toString, `[object Tag]`. */
        if (isstatic && __kml_object_tostring && !(*(i64 *)bag & (1LL << 48)))
            return (i64)(u64)__kml_object_tostring(v);
        return v;
    }
    i64 t = tp_get(bag, 0, k_tag.d);
    if (nb_tag(t) != 2) return (i64)(u64)k_obj.d;
    const char *name = (const char *)nb_ptr(t);
    i64 n = KML_STR_LEN(name);
    char *s = (char *)kml_alloc(KML_HEAP_RAW, (size_t)n + 18);
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

/* JS ToNumber and ToBoolean over a NaN-boxed word. Numbers decode;
 * true/false are 1/0; null is 0; undefined NaN. A string goes through
 * Number(s)'s StringToNumber (__kml_to_number, ADR-01057). A boxed Symbol
 * (an object whose hidden field 0 carries the Symbol type-id flag,
 * ADR-01059) or BigInt throws the spec's TypeError; any other heap reference goes
 * through ToPrimitive with the number hint (__kml_obj_tonum). */
extern double __kml_to_number(const char *s);
extern double __kml_obj_tonum(i64 v);
extern i64 __kml_str_len(const char *s);
KSTR(k_bignum, "Cannot convert a BigInt value to a number");

#define NB_DOUBLE_OFFSET 562949953421312ULL /* 1 << 49 */

static double nb_double(u64 v) {
    u64 bits = v - NB_DOUBLE_OFFSET;
    double d;
    memcpy(&d, &bits, sizeof d);
    return d;
}

double __kml_any_tonum(i64 w) {
    u64 v = (u64)w;
    if (v >= NB_DOUBLE_OFFSET) return nb_double(v);
    if (v < 65536) return v == 2 || v == 6 ? 0.0 : v == 7 ? 1.0 : __builtin_nan("");
    if ((v & 7) == 0) return __kml_to_number((const char *)v);
    if ((v & 7) == 1 && (*(const i64 *)(v & ~7ULL) & KML_SYMBOL_TYPEID))
        __kml_throw_nullderef(k_symnum.d);
    if (__kml_any_is_bigint(w)) __kml_throw_nullderef(k_bignum.d);
    return __kml_obj_tonum(w);
}

unsigned char __kml_any_tobool(i64 w) {
    u64 v = (u64)w;
    if (v >= NB_DOUBLE_OFFSET) {
        double d = nb_double(v);
        return d != 0.0 && d == d;
    }
    if (v < 65536) return v == 7;
    if ((v & 7) == 0) return __kml_str_len((const char *)v) != 0;
    return 1;
}
