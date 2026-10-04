/* bigintrel.c — relational comparison with a bigint on either side of a
 * dynamic `<` (TDD-00229; in C per TDD-00240). KML_BIGINT_MAGIC and
 * KML_NB_DOUBLE_OFFSET are prepended by the compiler. Linked when a
 * dynamic relational comparison used it and the program has the bigint
 * runtime. */

typedef long long i64;
typedef unsigned long long u64;

extern int __kml_bigint_cmp(void *a, void *b);
extern int __kml_bigint_cmp_double(void *a, double d);
extern double __kml_any_tonum(i64 v);

/* The bigint in a boxed word, or null when it holds none. */
void *__kml_any_bigint_of(i64 w) {
    u64 v = (u64)w;
    if (v < 65536 || v >= (u64)KML_NB_DOUBLE_OFFSET || (v & 7) != 1) return 0;
    i64 *cell = (i64 *)(v & ~7ULL);
    return cell[0] == KML_BIGINT_MAGIC ? (void *)cell[1] : 0;
}

/* JS's IsLessThan over two boxed words: 0 when neither is a bigint (compare
 * as numbers), else 1 less, 2 equal, 3 greater, 4 unordered (the other
 * side is NaN). */
int __kml_any_bigint_rel(i64 a, i64 b) {
    void *ba = __kml_any_bigint_of(a), *bb = __kml_any_bigint_of(b);
    int c;
    if (!ba && !bb) return 0;
    if (ba && bb) {
        c = __kml_bigint_cmp(ba, bb);
    } else if (ba) {
        double rd = __kml_any_tonum(b);
        if (rd != rd) return 4;
        c = __kml_bigint_cmp_double(ba, rd);
    } else {
        double ld = __kml_any_tonum(a);
        if (ld != ld) return 4;
        c = -__kml_bigint_cmp_double(bb, ld);
    }
    return c < 0 ? 1 : c > 0 ? 3 : 2;
}
