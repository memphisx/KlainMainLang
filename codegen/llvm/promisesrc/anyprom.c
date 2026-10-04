/* anyprom.c — resolving a promise with a dynamic value, and the Promise<any>
 * view of a boxed typed promise (TDD-00230 phase 5; in C per TDD-00240). The
 * wrapper's header word varies with the program, so recognizing one
 * (__kml_promise_wrapper_of) and the reaction runner (__kml_pdyn_run) stay
 * generated. kml_layout.h is prepended by the compiler. */
typedef long long i64;

#define F(p, off, T) (*(T *)((char *)(p) + (off)))

extern void *malloc(unsigned long);
extern void __kml_microtask_enqueue(void *);
extern void __kml_promise_settle(void *, i64);
extern void *__kml_task_alloc_promise(void);
extern void *__kml_promise_wrapper_of(i64);
extern void __kml_pdyn_attach(void *src, i64 onF, i64 onR, i64 onFin, void *q, void *boxer, i64 kind);

typedef i64 (*boxfn)(void *);

/* NewPromiseResolveThenableJob: register the pass-through reaction later;
 * env is {q, src, boxer}. */
static void pdyn_adopt_job(void *env) {
    void **a = env;
    __kml_pdyn_attach(a[1], KML_NB_UNDEFINED, KML_NB_UNDEFINED, KML_NB_UNDEFINED, a[0], a[2], KML_PDYN_THEN);
}

/* Resolve q with a dynamic value: a promise it holds is adopted (a microtask
 * later), anything else fulfills q. */
void __kml_promise_resolve_any(void *q, i64 v) {
    void *w = __kml_promise_wrapper_of(v);
    if (w) {
        void **env = malloc(3 * sizeof(void *));
        void **clo = malloc(2 * sizeof(void *));
        env[0] = q;
        env[1] = F(w, KML_PBOX_PROMISE, void *);
        env[2] = F(w, KML_PBOX_BOXFN, void *);
        clo[0] = (void *)pdyn_adopt_job;
        clo[1] = env;
        __kml_microtask_enqueue(clo);
        return;
    }
    F(q, KML_PROMISE_V0, i64) = v;
    __kml_promise_settle(q, 1);
}

extern void __kml_promise_mark_handled(void *);
extern void __kml_promise_attach_sync(void *p, void *clo);

/* The mirror reaction: env is {src, view, boxer}. The view takes src's
 * settlement, its value boxed. */
static void pview_mirror(void *envp) {
    void **env = envp;
    void *src = env[0], *q = env[1];
    i64 state = F(src, KML_PROMISE_STATE, i64);
    if (F(q, KML_PROMISE_STATE, i64) != 0) return;
    if (state == 1) {
        F(q, KML_PROMISE_V0, i64) = ((boxfn)env[2])(src);
    } else {
        F(q, KML_PROMISE_V0, i64) = F(src, KML_PROMISE_V0, i64);
        F(q, KML_PROMISE_V1, i64) = F(src, KML_PROMISE_V1, i64);
    }
    __kml_promise_settle(q, state);
}

/* The Promise<any> view of a boxed typed promise (its wrapper w). JS has one
 * object, so the view settles with the source, in the same step: a settled
 * source at once, a pending one through a mirror reaction its settlement runs
 * directly rather than as a job. */
void *__kml_promise_view_any(void *w) {
    void *src = F(w, KML_PBOX_PROMISE, void *);
    void *boxer = F(w, KML_PBOX_BOXFN, void *);
    void *q = __kml_task_alloc_promise();
    void **env = malloc(3 * sizeof(void *));
    env[0] = src;
    env[1] = q;
    env[2] = boxer;
    /* The view answers for the source's rejection from here on. */
    __kml_promise_mark_handled(src);
    if (F(src, KML_PROMISE_STATE, i64) != 0) {
        pview_mirror(env);
        return q;
    }
    void **clo = malloc(2 * sizeof(void *));
    clo[0] = (void *)pview_mirror;
    clo[1] = env;
    __kml_promise_attach_sync(src, clo);
    return q;
}
