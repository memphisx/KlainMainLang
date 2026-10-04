/* dynwhen.c — settling a bundled import() from its target's module promise
 * (TDD-00238 Stage 5). When the module promise mp settles, q is rejected
 * with mp's reason, or fn(q) stores the namespace object and q is
 * fulfilled. A reaction on mp, so no polling. The microtask hops match
 * Node's module loader: a target is evaluated on the fifth microtask after
 * import(), and import() settles four after its module promise's reaction.
 * kml_layout.h is prepended by the compiler. */
#include <stdlib.h>

typedef long long i64;
typedef void (*kml_fn0)(void *env);
typedef void (*kml_fulfill)(void *q);

#define F(p, off, T) (*(T *)((char *)(p) + (off)))

extern void __kml_promise_attach(void *, void *);
extern void __kml_promise_settle(void *, i64);
extern void __kml_promise_defer_settle(void *, i64, i64);
extern void __kml_microtask_enqueue(void *);

#define KML_DYN_EVAL_HOPS 5
#define KML_DYN_SETTLE_HOPS 4

typedef struct {
    void *mp, *q;
    kml_fulfill fn;
} when_env;

static void when_settled(void *envp) {
    when_env *env = (when_env *)envp;
    void *mp = env->mp, *q = env->q;
    if (F(mp, KML_PROMISE_STATE, i64) == 2) {
        F(q, KML_PROMISE_V0, i64) = F(mp, KML_PROMISE_V0, i64);
        F(q, KML_PROMISE_V1, i64) = F(mp, KML_PROMISE_V1, i64);
        __kml_promise_defer_settle(q, 2, KML_DYN_SETTLE_HOPS);
        return;
    }
    env->fn(q);
    __kml_promise_defer_settle(q, 1, KML_DYN_SETTLE_HOPS);
}

typedef void (*kml_body)(void *args);

extern void *__kml_task_alloc_promise(void);
extern void *__kml_spawn_task_ex(kml_body, void *, void *, i64, void (*)(void));
extern void __kml_task_trampoline(void);

typedef struct {
    kml_body body;
    void *p;
    i64 stack, hops;
    void *clo;
} start_env;

/* Spawn the target's body as a task (run to its first park) on the
 * microtask Node evaluates an imported module on. */
static void start_step(void *envp) {
    start_env *env = (start_env *)envp;
    if (env->hops > 1) {
        env->hops--;
        __kml_microtask_enqueue(env->clo);
        return;
    }
    __kml_spawn_task_ex(env->body, 0, env->p, env->stack, __kml_task_trampoline);
}

/* A target's module promise: the one in *slot, or a new one whose body
 * starts as a task with a stack of stack bytes. */
void *__kml_dynimport_init(void **slot, kml_body body, i64 stack) {
    if (*slot) return *slot;
    void *p = __kml_task_alloc_promise();
    *slot = p;
    start_env *env = (start_env *)malloc(sizeof(start_env));
    void *clo = malloc(KML_CLOSURE_SIZE);
    env->body = body;
    env->p = p;
    env->stack = stack;
    env->hops = KML_DYN_EVAL_HOPS;
    env->clo = clo;
    F(clo, KML_CLOSURE_FN, kml_fn0) = start_step;
    F(clo, KML_CLOSURE_ENV, void *) = env;
    __kml_microtask_enqueue(clo);
    return p;
}

void __kml_dynimport_when(void *mp, void *q, kml_fulfill fn) {
    when_env *env = (when_env *)malloc(sizeof(when_env));
    env->mp = mp;
    env->q = q;
    env->fn = fn;
    void *clo = malloc(KML_CLOSURE_SIZE);
    F(clo, KML_CLOSURE_FN, kml_fn0) = when_settled;
    F(clo, KML_CLOSURE_ENV, void *) = env;
    __kml_promise_attach(mp, clo);
}
