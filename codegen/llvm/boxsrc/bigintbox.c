/* bigintbox.c — the hooks the dynamic runtime calls on a boxed bigint cell
 * { i64 magic, ptr bigint } (TDD-00229; in C per TDD-00240). Linked only
 * when the program links the bigint runtime: without it no cell can exist
 * and the compiler defines these as stubs. */

extern void *__kml_bigint_to_str(void *b, int base);
extern void *__kml_str_from_cstr(void *s);
extern int __kml_bigint_cmp(void *a, void *b);
extern int __kml_bigint_cmp_double(void *a, double d);

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
