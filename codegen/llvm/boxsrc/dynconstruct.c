/* dynconstruct.c — `new c(...args)` with c held in `any` (emit_classref.go,
 * Reflect.construct; in C per TDD-00240): the constructor reference's row
 * (unit kind KML_UNIT_CTOR, emit_classref_rows.go) names its construct
 * routine; a plain function constructs through fnmeta.c; anything else
 * throws the TypeError msg through the program's fail path. Linked only
 * when the program constructs through `any`. */
typedef long long i64;

typedef struct {
    i64 payload;
    void *shown, *nstatic, *skey, *sget, *inst;
    i64 kind;
    i64 (*construct)(i64 *argv, i64 argc);
    void *get, *set;
} CtorRow;

extern const void *__kml_unit_find(i64 kind, i64 id);
extern unsigned char __kml_nb_tag(long long w);
extern long long __kml_nb_pay(long long w);
extern i64 __kml_dyn_construct_fn(i64 c, i64 *argv, i64 argc);
extern i64 __kml_dyn_construct_fail(void *msg);

extern i64 *__kml_fn_bound_env(i64 c);
extern void *malloc(__SIZE_TYPE__); /* scratch: the argument list below */
extern void free(void *); /* scratch */

i64 __kml_dyn_construct(i64 c, i64 *argv, i64 argc, void *msg) {
    i64 *env = __kml_fn_bound_env(c);
    if (env) {
        /* A bound function: its target, with the bound arguments first
         * (the bound `this` is ignored). */
        i64 n = env[2];
        i64 *all = malloc((unsigned long)(n + argc + 1) * sizeof(i64)); /* scratch: the caller still holds every value */
        for (i64 i = 0; i < n; i++) all[i] = env[3 + i];
        for (i64 i = 0; i < argc; i++) all[n + i] = argv[i];
        i64 r = __kml_dyn_construct(env[0], all, n + argc, msg);
        free(all); /* scratch */
        return r;
    }
    const CtorRow *r = 0;
    if (__kml_nb_tag(c) == KML_TAG_FUNCREF) r = (const CtorRow *)__kml_unit_find(KML_UNIT_CTOR, __kml_nb_pay(c));
    if (r && r->construct) return r->construct(argv, argc);
    i64 f = __kml_dyn_construct_fn(c, argv, argc);
    return f ? f : __kml_dyn_construct_fail(msg);
}
