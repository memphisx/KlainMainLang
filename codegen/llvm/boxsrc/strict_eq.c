/* strict_eq.c — JS's Strict Equality (`===`) and SameValue (`Object.is`)
 * over two NaN-boxed values (TDD-00156; in C per TDD-00240). The
 * KML_* constants are prepended by the compiler. */

typedef long long i64;
typedef unsigned long long u64;

extern int strcmp(const char *a, const char *b);
extern void *__kml_fn_identity_dyn(void *rec);
extern unsigned char __kml_boxed_bigint_eq(void *ca, void *cb);

/* Two numbers compare as doubles (NaN !== NaN, -0 === 0). Bitwise-equal
 * non-numbers are one immediate or one pointer. Otherwise two strings
 * compare by content; two any-array boxes by their shared live header (one
 * array's aliases stay equal across a reallocating push); two dynamic
 * function records by the function they wrap; two boxed bigints by value;
 * two host boxes of one handle are one value. */
_Bool __kml_any_eq(i64 a, i64 b) {
    u64 ua = (u64)a, ub = (u64)b;
    int anum = ua >= (u64)KML_NB_DOUBLE_OFFSET, bnum = ub >= (u64)KML_NB_DOUBLE_OFFSET;
    if (anum && bnum) {
        union { u64 u; double d; } x = {ua - (u64)KML_NB_DOUBLE_OFFSET}, y = {ub - (u64)KML_NB_DOUBLE_OFFSET};
        return x.d == y.d;
    }
    if (anum || bnum) return 0;
    if (a == b) return 1;
    if (ua < 65536 || ub < 65536) return 0;
    int ak = (int)(ua & 7), bk = (int)(ub & 7);
    if (ak != bk) return 0;
    void *pa = (void *)(ua & ~7ULL), *pb = (void *)(ub & ~7ULL);
    switch (ak) {
    case 0:
        return strcmp((const char *)a, (const char *)b) == 0;
    case 2:
        return *(void **)pa == *(void **)pb;
    case 7:
        return __kml_fn_identity_dyn(pa) == __kml_fn_identity_dyn(pb);
    case 1: {
        i64 fa = *(i64 *)pa, fb = *(i64 *)pb;
        if (fa == KML_BIGINT_MAGIC && fb == KML_BIGINT_MAGIC) return __kml_boxed_bigint_eq(pa, pb) & 1;
        i64 host = KML_HDR_MAGIC | KML_HOST_TYPEID_FLAG, mask = KML_HDR_MAGIC_MASK | KML_HOST_TYPEID_FLAG;
        if ((fa & mask) != host || (fb & mask) != host) return 0;
        /* a headered host (a Map, a Set) is its own box: distinct
         * pointers are distinct values */
        if (fa & KML_HOST_INLINE_FLAG) return 0;
        return ((void **)pa)[1] == ((void **)pb)[1];
    }
    }
    return 0;
}

/* SameValue: a number is itself (NaN is NaN, +0 is not -0); anything else
 * is strict equality. */
_Bool __kml_Object_is(i64 a, i64 b) {
    u64 ua = (u64)a, ub = (u64)b;
    if (ua >= (u64)KML_NB_DOUBLE_OFFSET && ub >= (u64)KML_NB_DOUBLE_OFFSET) {
        if (a == b) return 1;
        union { u64 u; double d; } x = {ua - (u64)KML_NB_DOUBLE_OFFSET}, y = {ub - (u64)KML_NB_DOUBLE_OFFSET};
        return x.d != x.d && y.d != y.d;
    }
    return __kml_any_eq(a, b);
}
