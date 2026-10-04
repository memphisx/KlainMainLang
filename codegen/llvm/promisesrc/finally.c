/* finally.c — once onFinally's promise r settles, q takes r's rejection, or
 * else the source's settlement (res, v0, v1), one microtask after r's
 * reaction, as the spec's adoption of `r.then(() => value)` does (TDD-00240).
 * kml_layout.h is prepended by the compiler. */
typedef long long i64;

#define F(p, off, T) (*(T *)((char *)(p) + (off)))

extern void *malloc(unsigned long);
extern void __kml_promise_attach(void *, void *);
extern void __kml_promise_defer_settle(void *, i64, i64);

typedef struct {
    void *q;
    i64 res, v0, v1;
    void *r;
} finally_env;

static void finally_step(void *envp) {
    finally_env *env = envp;
    void *q = env->q, *r = env->r;
    if (F(r, KML_PROMISE_STATE, i64) == 2) {
        F(q, KML_PROMISE_V0, i64) = F(r, KML_PROMISE_V0, i64);
        F(q, KML_PROMISE_V1, i64) = F(r, KML_PROMISE_V1, i64);
        __kml_promise_defer_settle(q, 2, 1);
        return;
    }
    F(q, KML_PROMISE_V0, i64) = env->v0;
    F(q, KML_PROMISE_V1, i64) = env->v1;
    __kml_promise_defer_settle(q, env->res, 1);
}

void __kml_promise_finally_wait(void *r, void *q, i64 res, i64 v0, i64 v1) {
    finally_env *env = malloc(sizeof *env);
    void **clo = malloc(2 * sizeof(void *));
    env->q = q;
    env->res = res;
    env->v0 = v0;
    env->v1 = v1;
    env->r = r;
    clo[0] = (void *)finally_step;
    clo[1] = env;
    __kml_promise_attach(r, clo);
}
