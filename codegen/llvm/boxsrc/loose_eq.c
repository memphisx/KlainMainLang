/* loose_eq.c — JS's Abstract Equality (`==`) over two NaN-boxed values
 * (TDD-00201 Stage 4, `-compat=js`; in C per TDD-00240). Distinct from
 * __kml_any_eq (`===`): loose equality coerces. Two heap objects compare
 * by reference; exactly one object is ToPrimitive'd; null and undefined
 * equal only each other; the same primitive type compares strictly; mixed
 * primitives compare as numbers (NaN makes it false). A bigint against a
 * bigint, a number or a string compares by value. KML_BIGINT_MAGIC is
 * prepended by the compiler. */

typedef long long i64;
typedef unsigned long long u64;

extern unsigned char __kml_any_eq(i64 a, i64 b);
extern double __kml_any_tonum(i64 v);
extern i64 __kml_toprimitive(i64 v, int hint);
extern unsigned char __kml_boxed_bigint_eq(void *ca, void *cb);
extern unsigned char __kml_boxed_bigint_eq_num(void *cell, double d);

/* mirrors __kml_nb_tag (runtime_nanbox.go) */
static int nb_tag(i64 w) {
    u64 v = (u64)w;
    static const int kindTag[8] = {2, 6, 7, 8, 9, 10, 11, 12};
    if (v >= (1ULL << 49)) return 1;
    if (v < 65536) return v == 2 ? 4 : (v == 6 || v == 7) ? 3 : 5;
    return kindTag[v & 7];
}

/* The bigint cell a box holds, or null. */
static void *bigint_cell(i64 w) {
    if (nb_tag(w) != 6) return 0;
    i64 *cell = (i64 *)((u64)w & ~7ULL);
    return cell[0] == KML_BIGINT_MAGIC ? cell : 0;
}

_Bool __kml_any_loose_eq(i64 a0, i64 b0) {
    void *biga = bigint_cell(a0), *bigb = bigint_cell(b0);
    if (biga || bigb) {
        if (biga && bigb) return __kml_boxed_bigint_eq(biga, bigb) & 1;
        void *cell = biga ? biga : bigb;
        i64 other = biga ? b0 : a0;
        int to = nb_tag(other);
        if (to == 4 || to == 5 || to >= 6) return 0;
        return __kml_boxed_bigint_eq_num(cell, __kml_any_tonum(other)) & 1;
    }
    int aobj = nb_tag(a0) >= 6, bobj = nb_tag(b0) >= 6;
    if (aobj && bobj) return __kml_any_eq(a0, b0) & 1;
    i64 a = aobj ? __kml_toprimitive(a0, 2) : a0;
    i64 b = bobj ? __kml_toprimitive(b0, 2) : b0;
    int ta = nb_tag(a), tb = nb_tag(b);
    int anull = ta == 4 || ta == 5, bnull = tb == 4 || tb == 5;
    if (anull || bnull) return anull && bnull;
    if (ta == tb) return __kml_any_eq(a, b) & 1;
    return __kml_any_tonum(a) == __kml_any_tonum(b);
}
