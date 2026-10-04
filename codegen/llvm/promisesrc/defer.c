/* defer.c — settle a promise after N more microtasks (the ticks a spec
 * adoption takes; TDD-00240). kml_layout.h is prepended by the compiler. */
typedef long long i64;

extern void *malloc(unsigned long);
extern void __kml_microtask_enqueue(void *);
extern void __kml_promise_settle(void *, i64);

typedef struct {
    void *q;
    i64 state, hops;
    void *clo; /* the closure that re-enqueues this step */
} defer_env;

void __kml_promise_defer_step(void *envp) {
    defer_env *env = envp;
    if (env->hops > 1) {
        env->hops--;
        __kml_microtask_enqueue(env->clo);
        return;
    }
    __kml_promise_settle(env->q, env->state);
}

/* Settle q with state after hops more microtasks. */
void __kml_promise_defer_settle(void *q, i64 state, i64 hops) {
    defer_env *env = malloc(sizeof *env);
    void **clo = malloc(2 * sizeof(void *));
    env->q = q;
    env->state = state;
    env->hops = hops;
    env->clo = clo;
    clo[0] = (void *)__kml_promise_defer_step;
    clo[1] = env;
    __kml_microtask_enqueue(clo);
}

#if defined(_WIN32)
extern int _setjmp(void *, void *) __attribute__((returns_twice));
#define KML_SETJMP(b) _setjmp((b), 0)
#else
extern int setjmp(void *) __attribute__((returns_twice));
#define KML_SETJMP(b) setjmp(b)
#endif

extern void *__kml_push_jmpbuf(void);
extern void __kml_pop_jmpbuf(void);
extern i64 __kml_get_thrown_pay(void);
extern unsigned char __kml_get_thrown_tag(void);
extern void __kml_promise_reject_with(void *, i64, i64);

/* A then/catch/finally reaction: env is the runner's {p, onF, onR, onFin, q}
 * followed by the runner itself. A handler that throws rejects q with the
 * thrown value, as the spec's NewPromiseReactionJob does with an abrupt
 * handler completion. */
void __kml_then_guard(void *envp) {
    void **env = envp;
    void (*run)(void *) = (void (*)(void *))env[5];
    void *jb = __kml_push_jmpbuf();
    if (KML_SETJMP(jb) != 0) {
        i64 pay = __kml_get_thrown_pay();
        i64 tag = (i64)__kml_get_thrown_tag();
        __kml_promise_reject_with(env[4], tag, pay);
        return;
    }
    run(envp);
    __kml_pop_jmpbuf();
}
