/* bigintbox.c — the hooks the dynamic runtime calls on a boxed bigint cell
 * { i64 magic, ptr bigint } (TDD-00229; in C per TDD-00240). Linked only
 * when the program links the bigint runtime: without it no cell can exist
 * and the compiler defines these as stubs. */

#include <stdlib.h>

extern void *__kml_bigint_to_str(void *b, int base);
extern void *__kml_str_from_cstr(void *s);
extern int __kml_bigint_cmp(void *a, void *b);
extern int __kml_bigint_cmp_double(void *a, double d);
extern double __kml_bigint_to_double(void *b);
extern void *__kml_bigint_neg(void *b);

static void *cell_big(void *cell) { return ((void **)cell)[1]; }

/* Decimal digits of the cell's bigint. */
void *__kml_boxed_bigint_str(void *cell) {
    return __kml_str_from_cstr(__kml_bigint_to_str(cell_big(cell), 10));
}

/* Value equality of two cells' bigints. */
_Bool __kml_boxed_bigint_eq(void *ca, void *cb) {
    return __kml_bigint_cmp(cell_big(ca), cell_big(cb)) == 0;
}

/* A cell's bigint against a number (never equal to NaN). */
_Bool __kml_boxed_bigint_eq_num(void *cell, double d) {
    if (d != d) return 0;
    return __kml_bigint_cmp_double(cell_big(cell), d) == 0;
}

/* A cell's bigint as the nearest double (Number(x) of an `any`). */
double __kml_boxed_bigint_to_double(long long cell) {
    return __kml_bigint_to_double(cell_big((void *)cell));
}

long long __kml_typed_big_box(long long raw, int uns);
static long long box_big(void *b);

/* -x of a cell's bigint, boxed. */
long long __kml_boxed_bigint_neg(long long cell) {
    return box_big(__kml_bigint_neg(cell_big((void *)cell)));
}

/* A BigInt64Array's (BigUint64Array's, uns set) element held in an `any`
 * (dynjson.c, whose weak defaults these replace): the raw 64 bits as a
 * boxed bigint cell, and a value stored into one as the 64 bits it wraps
 * to — a bigint, or a boolean as 0n/1n (ToBigInt); anything else is V8's
 * TypeError. */
extern void *__kml_bigint_from_i64(long long v);
extern void *__kml_bigint_from_u64(unsigned long long v);
extern long long __kml_bigint_to_i64(void *b);
extern unsigned long long __kml_bigint_to_u64(void *b);
extern long long __kml_nb_pack(unsigned char tag, long long pay);
extern unsigned char __kml_nb_tag(long long w);
extern long long __kml_nb_pay(long long w);
extern char *__kml_any_inspect_at(long long v, long long depth);
extern void __kml_throw_nullderef(const char *msg);

#define KML_BOXED_BIGINT_MAGIC 0x7FF40000B1616B16LL

static long long box_big(void *b) {
    void **cell = (void **)kml_alloc(KML_HEAP_WORDS, 16);
    ((long long *)cell)[0] = KML_BOXED_BIGINT_MAGIC;
    cell[1] = b;
    return __kml_nb_pack(6, (long long)cell);
}

long long __kml_typed_big_box(long long raw, int uns) {
    return box_big(uns ? __kml_bigint_from_u64((unsigned long long)raw) : __kml_bigint_from_i64(raw));
}

long long __kml_typed_big_unbox(long long w, int uns) {
    unsigned char t = __kml_nb_tag(w);
    long long p = __kml_nb_pay(w);
    if (t == 6 && p && *(long long *)p == KML_BOXED_BIGINT_MAGIC) {
        void *b = ((void **)p)[1];
        return uns ? (long long)__kml_bigint_to_u64(b) : __kml_bigint_to_i64(b);
    }
    if (t == 3) return p != 0;
    char *shown = __kml_any_inspect_at(w, 0);
    const char *pre = "Cannot convert ", *post = " to a BigInt";
    __SIZE_TYPE__ a = __builtin_strlen(pre), n = shown ? __builtin_strlen(shown) : 0, c = __builtin_strlen(post);
    char *msg = (char *)malloc(a + n + c + 1); /* scratch */
    __builtin_memcpy(msg, pre, a);
    if (n) __builtin_memcpy(msg + a, shown, n);
    __builtin_memcpy(msg + a + n, post, c + 1);
    void *str = __kml_str_from_cstr(msg);
    free(msg);
    __kml_throw_nullderef((const char *)str);
    return 0;
}
