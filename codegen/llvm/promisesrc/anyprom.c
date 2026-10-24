/* anyprom.c — resolving a promise with a dynamic value, and the Promise<any>
 * view of a boxed typed promise (TDD-00230 phase 5; in C per TDD-00240). The
 * wrapper's header word varies with the program, so recognizing one
 * (__kml_promise_wrapper_of) and the reaction runner (__kml_pdyn_run) stay
 * generated. kml_layout.h is prepended by the compiler. */
typedef long long i64;

#define F(p, off, T) (*(T *)((char *)(p) + (off)))

extern void *malloc(__SIZE_TYPE__); /* scratch */
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
extern void __kml_promise_reject_box(void *, i64);
extern i64 __kml_obj_get(void *, const void *);
extern i64 __kml_dynobj_get(void *, const void *);

typedef unsigned long long u64;
static const struct { i64 len; char d[5]; } k_then = {KML_STR_ASCII_HDR(4), "then"};

#define NB_HDR_MAGIC (0x4B4DLL << 32)
#define NB_HDR_MAGIC_MASK (0x7FFFLL << 32)
#define NB_HOST_FLAG (1LL << 49)
#define NB_ERROR_FLAG (1LL << 48)

/* The `then` property of a dynamic value that is an object (a static
 * object's or class instance's box, or a dynamic bag), or 0. */
static i64 then_of(i64 v) {
    u64 u = (u64)v;
    if (u < 65536 || u >= (1ULL << 49)) return 0;
    void *p = (void *)(u & ~7ULL);
    switch (u & 7) {
    case 5: /* a dynamic bag */
        return __kml_dynobj_get(p, k_then.d);
    case 1: { /* a static object: its layout row, not a host or error cell */
        i64 h = *(i64 *)p;
        if ((h & NB_HDR_MAGIC_MASK) != NB_HDR_MAGIC || (h & (NB_HOST_FLAG | NB_ERROR_FLAG))) return 0;
        return __kml_obj_get(p, k_then.d);
    }
    }
    return 0;
}

void __kml_promise_resolve_any(void *q, i64 v);

/* A thenable's resolving functions share their first-call-wins state. */
typedef struct {
    void *q;
    i64 done;
} resolving;

typedef i64 (*kml_fn)(void *env, i64 recv, i64 argc, i64 *argv);

static i64 resolving_resolve(void *env, i64 recv, i64 argc, i64 *argv) {
    resolving *r = env;
    (void)recv;
    if (!r->done) {
        r->done = 1;
        __kml_promise_resolve_any(r->q, argc > 0 ? argv[0] : KML_NB_UNDEFINED);
    }
    return KML_NB_UNDEFINED;
}

static i64 resolving_reject(void *env, i64 recv, i64 argc, i64 *argv) {
    resolving *r = env;
    (void)recv;
    if (!r->done) {
        r->done = 1;
        __kml_promise_reject_box(r->q, argc > 0 ? argv[0] : KML_NB_UNDEFINED);
    }
    return KML_NB_UNDEFINED;
}

/* NewPromiseResolveThenableJob for a dynamic thenable: env is {q, v, then}.
 * then is called with v as this and q's resolving functions; a throw before
 * either is called rejects q. */
static void thenable_job(void *envp) {
    i64 *env = envp;
    resolving *r = kml_alloc(KML_HEAP_WORDS, sizeof *r);
    r->q = (void *)env[0];
    r->done = 0;
    void **res = kml_alloc(KML_HEAP_WORDS, 3 * sizeof(void *)), **rej = kml_alloc(KML_HEAP_WORDS, 3 * sizeof(void *));
    res[0] = (void *)resolving_resolve, res[1] = r, res[2] = (void *)1;
    rej[0] = (void *)resolving_reject, rej[1] = r, rej[2] = (void *)1;
    i64 argv[2] = {(i64)(u64)res | 7, (i64)(u64)rej | 7};
    void **fn = (void **)((u64)env[2] & ~7ULL);
    void *jb = __kml_push_jmpbuf();
    if (KML_SETJMP(jb) != 0) {
        if (!r->done) {
            r->done = 1;
            __kml_promise_reject_with(r->q, (i64)__kml_get_thrown_tag(), __kml_get_thrown_pay());
        }
        return;
    }
    ((kml_fn)fn[0])(fn[1], env[1], 2, argv);
    __kml_pop_jmpbuf();
}

/* Resolve q with a dynamic value: a promise it holds is adopted (a microtask
 * later), a thenable through its `then` (a job), anything else fulfills q. */
void __kml_promise_resolve_any(void *q, i64 v) {
    i64 then = then_of(v);
    if (then && ((u64)then & 7) == 7 && (u64)then >= 65536 && (u64)then < (1ULL << 49) && !__kml_promise_wrapper_of(v)) {
        i64 *env = kml_alloc(KML_HEAP_WORDS, 3 * sizeof(i64));
        void **clo = kml_alloc(KML_HEAP_WORDS, 2 * sizeof(void *));
        env[0] = (i64)(u64)q, env[1] = v, env[2] = then;
        clo[0] = (void *)thenable_job;
        clo[1] = env;
        __kml_microtask_enqueue(clo);
        return;
    }
    void *w = __kml_promise_wrapper_of(v);
    if (w) {
        void **env = kml_alloc(KML_HEAP_WORDS, 3 * sizeof(void *));
        void **clo = kml_alloc(KML_HEAP_WORDS, 2 * sizeof(void *));
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
    void **env = kml_alloc(KML_HEAP_WORDS, 3 * sizeof(void *));
    env[0] = src;
    env[1] = q;
    env[2] = boxer;
    /* The view answers for the source's rejection from here on. */
    __kml_promise_mark_handled(src);
    if (F(src, KML_PROMISE_STATE, i64) != 0) {
        pview_mirror(env);
        return q;
    }
    void **clo = kml_alloc(KML_HEAP_WORDS, 2 * sizeof(void *));
    clo[0] = (void *)pview_mirror;
    clo[1] = env;
    __kml_promise_attach_sync(src, clo);
    return q;
}
