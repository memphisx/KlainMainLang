/* anystr.c — String(v) of a NaN-boxed value (TDD-00230 Phase 4; was the
 * IR routine __kml_dynrender_s built in Go). ToPrimitive with the string
 * hint first (an object's own toString / @@toPrimitive), then the
 * primitive's or the remaining object's string form, as JS's ToString. */

#include <string.h>

typedef long long i64;
typedef unsigned long long u64;

#define KSTR(id, s) static const struct { i64 len; char d[sizeof(s)]; } id = { KML_STR_ASCII_HDR(sizeof(s) - 1), s }
KSTR(k_true, "true");
KSTR(k_false, "false");
KSTR(k_null, "null");
KSTR(k_undef, "undefined");
KSTR(k_object, "[object Object]");
KSTR(k_sep, ": ");
KSTR(k_symopen, "Symbol(");
KSTR(k_symclose, ")");
KSTR(k_empty, "");

extern i64 __kml_toprimitive(i64 v, int hint);
extern void __kml_dtoa(char *buf, double d);
extern char *__kml_str_alloc(i64 n);
extern char *__kml_str_concat(const char *a, const char *b);
extern i64 __kml_str_len(const char *s);
extern void *__kml_array_join(void *box);
extern void *__kml_dynarr_join(void *arr);
extern void *__kml_classref_tostring(void *name);
extern char *__kml_fn_tostring_dyn(void **rec);
extern void *__kml_boxed_bigint_str(void *cell);
extern void *__kml_host_tag(void *cell);
extern char *__kml_object_tostring(i64 word);


static char *num_str(u64 v) {
    u64 bits = v - (u64)KML_NB_DOUBLE_OFFSET;
    double d;
    memcpy(&d, &bits, sizeof d);
    char *s = __kml_str_alloc(31);
    __kml_dtoa(s, d);
    i64 n = (i64)strlen(s);
    *(i64 *)(s - 8) = KML_STR_ASCII_HDR(n);
    return s;
}

char *__kml_any_tostr(i64 w) {
    u64 v = (u64)__kml_toprimitive(w, 1);
    void *p = kml_nb_ptr(v);
    switch (kml_nb_tag(v)) {
    case 1: return num_str(v);
    case 2: return (char *)p;
    case 3: return (char *)(v == 7 ? k_true.d : k_false.d);
    case 4: return (char *)k_null.d;
    case 5: return (char *)k_undef.d;
    case 7: return (char *)__kml_array_join(p);
    case 8: return (char *)__kml_classref_tostring(p);
    case 11: return (char *)__kml_dynarr_join(p);
    case 12: return __kml_fn_tostring_dyn((void **)p);
    case 6: {
        i64 h = p ? *(i64 *)p : 0;
        if (h == KML_BIGINT_MAGIC) return (char *)__kml_boxed_bigint_str(p);
        if (p && (h & (KML_HDR_MAGIC_MASK | KML_HOST_TYPEID_FLAG)) == (KML_HDR_MAGIC | KML_HOST_TYPEID_FLAG))
            return (char *)__kml_host_tag(p);
        if (h & KML_ERROR_TYPE_FLAG) {
            const char *name = *(const char **)((char *)p + KML_ERR_NAME);
            const char *msg = *(const char **)((char *)p + KML_ERR_MESSAGE);
            if (__kml_str_len(msg) == 0) return (char *)name;
            return __kml_str_concat(__kml_str_concat(name, k_sep.d), msg);
        }
        if (h & KML_SYMBOL_TYPEID) {
            const char *desc = *(const char **)((char *)p + KML_SYMBOL_DESC);
            return __kml_str_concat(__kml_str_concat(k_symopen.d, desc ? desc : k_empty.d), k_symclose.d);
        }
        return __kml_object_tostring((i64)v);
    }
    default: return (char *)k_object.d;
    }
}

/* The default (no-comparator) sort order of an `any[]`: each slot's
 * String() form in JavaScript's string order. */
extern int __kml_str_cmp(const char *a, const char *b);
int __kml_cmp_any_lex(const i64 *a, const i64 *b) {
    return __kml_str_cmp(__kml_any_tostr(*a), __kml_any_tostr(*b));
}

/* console.log's rendering of a boxed value nested `depth` levels deep
 * (util.inspect): scalars as String() has them, containers in their
 * inspect form. maxDepth is the inspect depth option a host value's
 * routine is clamped past. */
extern void *__kml_array_inspect_at(void *box, i64 depth);
extern void *__kml_classref_shown(void *name);
extern void *__kml_classref_inspect_at(void *name, i64 depth);
extern void *__kml_fn_inspect_dyn(void *rec, i64 depth);
extern void *__kml_dynarr_inspect_at(void *arr, i64 depth);
extern void *__kml_dynobj_inspect_at(void *o, i64 depth);
extern void *__kml_obj_inspect_at(void *o, i64 depth);
extern void *__kml_host_inspect(void *cell, i64 depth);
KSTR(k_fnopen, "[Function: ");
KSTR(k_close, "]");
KSTR(k_n, "n");

KSTR(k_negzero, "-0");

char *__kml_any_show_at(i64 w, i64 depth, i64 maxDepth) {
    u64 v = (u64)w;
    void *p = kml_nb_ptr(v);
    switch (kml_nb_tag(v)) {
    case 1: /* util.inspect shows -0's sign, which String() drops */
        if (v - (u64)KML_NB_DOUBLE_OFFSET == 0x8000000000000000ULL) return (char *)k_negzero.d;
        return num_str(v);
    case 2: return (char *)p;
    case 3: return (char *)(v == 7 ? k_true.d : k_false.d);
    case 4: return (char *)k_null.d;
    case 5: return (char *)k_undef.d;
    case 7: return (char *)__kml_array_inspect_at(p, depth);
    case 8:
        if (__kml_classref_shown(p)) return (char *)__kml_classref_inspect_at(p, depth);
        return __kml_str_concat(__kml_str_concat(k_fnopen.d, (const char *)p), k_close.d);
    case 10: return (char *)__kml_dynobj_inspect_at(p, depth);
    case 11: return (char *)__kml_dynarr_inspect_at(p, depth);
    case 12: return (char *)__kml_fn_inspect_dyn(p, depth);
    case 6: {
        i64 h = p ? *(i64 *)p : 0;
        if (h == KML_BIGINT_MAGIC) return __kml_str_concat((const char *)__kml_boxed_bigint_str(p), k_n.d);
        if (p && (h & (KML_HDR_MAGIC_MASK | KML_HOST_TYPEID_FLAG)) == (KML_HDR_MAGIC | KML_HOST_TYPEID_FLAG))
            return (char *)__kml_host_inspect(p, depth > maxDepth + 1 ? maxDepth + 1 : depth);
        if (h & KML_SYMBOL_TYPEID) {
            const char *desc = *(const char **)((char *)p + KML_SYMBOL_DESC);
            return __kml_str_concat(__kml_str_concat(k_symopen.d, desc ? desc : k_empty.d), k_symclose.d);
        }
        return (char *)__kml_obj_inspect_at(p, depth);
    }
    default: return (char *)k_object.d;
    }
}

/* ---- console.* with a run-time format string (util.format) ----
 * fmt is the first argument's box; args the n boxes after it. NULL when
 * fmt is not a string or holds no `%` (the caller prints the arguments as
 * it would anyway); else the whole formatted line, the arguments left over
 * appended as console renders them (a string as is, the rest inspected). */
extern char *__kml_any_inspect_at(long long v, long long depth);
extern double __kml_any_tonum(i64 w);
extern unsigned char __kml_nb_tag(long long w);
extern long long __kml_nb_pay(long long w);
extern char *__kml_dynjson_stringify_at(i64 tag, i64 pay, const char *indent, i64 base, int *err);
extern double strtod(const char *s, char **end);
KSTR(k_space, " ");
KSTR(k_pct, "%");
KSTR(k_nan, "NaN");
KSTR(k_bign, "n");
KSTR(k_circ, "[Circular]");

static char *dbl_str(double d) {
    if (d == 0 && 1 / d < 0) return (char *)k_negzero.d;
    u64 bits;
    memcpy(&bits, &d, sizeof bits);
    if (d != d) return (char *)k_nan.d;
    return num_str(bits + (u64)KML_NB_DOUBLE_OFFSET);
}
static int is_bigint(u64 v) {
    if (kml_nb_tag(v) != 6) return 0;
    void *p = kml_nb_ptr(v);
    return p && *(i64 *)p == KML_BIGINT_MAGIC;
}
static int is_symbol(u64 v) {
    if (kml_nb_tag(v) != 6) return 0;
    void *p = kml_nb_ptr(v);
    return p && (*(i64 *)p & KML_SYMBOL_TYPEID) && !(*(i64 *)p & KML_ERROR_TYPE_FLAG) && *(i64 *)p != KML_BIGINT_MAGIC;
}
static char *fmt_token(u64 v) {
    if (kml_nb_tag(v) == 2) return (char *)kml_nb_ptr(v);
    return __kml_any_inspect_at((long long)v, 0);
}
static char *fmt_verb(char verb, u64 v) {
    int t = kml_nb_tag(v);
    switch (verb) {
    case 's':
        if (t == 1) return dbl_str(__kml_any_tonum((i64)v));
        if (is_bigint(v)) return __kml_str_concat(__kml_any_tostr((i64)v), k_bign.d);
        if (t == 2 || t == 3 || t == 4 || t == 5 || is_symbol(v)) return __kml_any_tostr((i64)v);
        return __kml_any_inspect_at((long long)v, 0);
    case 'd':
    case 'i':
    case 'f': {
        if (is_bigint(v) && verb != 'f') return __kml_str_concat(__kml_any_tostr((i64)v), k_bign.d);
        if (is_symbol(v)) return (char *)k_nan.d;
        double d;
        if (verb == 'd') {
            d = __kml_any_tonum((i64)v);
        } else {
            /* parseInt / parseFloat of String(v) */
            const char *s = __kml_any_tostr((i64)v);
            while (*s == ' ' || *s == '\t' || *s == '\n' || *s == '\r') s++;
            char *end;
            d = strtod(s, &end);
            if (end == s) return (char *)k_nan.d;
            if (verb == 'i') {
                /* digits only: parseInt stops at '.', 'e' */
                const char *q = s;
                int neg = 0;
                if (*q == '-' || *q == '+') neg = *q++ == '-';
                double r = 0;
                int any = 0;
                while (*q >= '0' && *q <= '9') { r = r * 10 + (*q++ - '0'); any = 1; }
                if (!any) return (char *)k_nan.d;
                d = neg ? -r : r;
            }
        }
        return dbl_str(d);
    }
    case 'j': {
        int err = 0;
        char *r = __kml_dynjson_stringify_at((i64)(unsigned char)__kml_nb_tag((long long)v), __kml_nb_pay((long long)v), 0, 0, &err);
        if (err == 1) return (char *)k_circ.d;
        return r ? r : (char *)k_undef.d;
    }
    default: /* o, O */
        return __kml_any_inspect_at((long long)v, 0);
    }
}

char *__kml_console_fmt(i64 fmt, const i64 *args, i64 n) {
    if (kml_nb_tag((u64)fmt) != 2) return 0;
    const char *f = (const char *)kml_nb_ptr((u64)fmt);
    i64 fl = __kml_str_len(f);
    int has = 0;
    for (i64 i = 0; i < fl; i++) if (f[i] == '%') { has = 1; break; }
    if (!has) return 0;
    char *out = (char *)k_empty.d;
    i64 a = 0, lit = 0;
    for (i64 i = 0; i < fl; i++) {
        if (f[i] != '%' || i + 1 >= fl) continue;
        char verb = f[i + 1];
        char *piece = 0;
        if (verb == '%') {
            piece = (char *)k_pct.d;
        } else if (a < n && (verb == 's' || verb == 'd' || verb == 'i' || verb == 'f' || verb == 'j' || verb == 'o' || verb == 'O')) {
            piece = fmt_verb(verb, (u64)args[a++]);
        } else if (a < n && verb == 'c') {
            a++;
            piece = (char *)k_empty.d;
        } else {
            continue;
        }
        if (i > lit) {
            char *chunk = __kml_str_alloc(i - lit);
            for (i64 k = 0; k < i - lit; k++) chunk[k] = f[lit + k];
            chunk[i - lit] = 0;
            *(i64 *)(chunk - 8) = i - lit;
            out = __kml_str_concat(out, chunk);
        }
        out = __kml_str_concat(out, piece);
        lit = i + 2;
        i++;
    }
    if (fl > lit) {
        char *chunk = __kml_str_alloc(fl - lit);
        for (i64 k = 0; k < fl - lit; k++) chunk[k] = f[lit + k];
        chunk[fl - lit] = 0;
        *(i64 *)(chunk - 8) = fl - lit;
        out = __kml_str_concat(out, chunk);
    }
    for (; a < n; a++) out = __kml_str_concat(__kml_str_concat(out, k_space.d), fmt_token((u64)args[a]));
    return out;
}

/* `console.log(...arr)`: the elements are boxes, or strings when strs (a
 * null one undefined); two or more, the first a string, format the rest. */
char *__kml_console_fmt_arr(const i64 *elems, i64 len, int strs) {
    if (len < 2) return 0;
    if (!strs) return __kml_console_fmt(elems[0], elems + 1, len - 1);
    i64 boxes[len];
    for (i64 i = 0; i < len; i++) boxes[i] = elems[i] ? elems[i] : 10;
    return __kml_console_fmt(boxes[0], boxes + 1, len - 1);
}
