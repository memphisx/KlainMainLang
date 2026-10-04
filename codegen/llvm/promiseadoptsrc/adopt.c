/* adopt.c — resolving a promise with a promise (TDD-00223; in C per
 * TDD-00240). q settles the way h settles; the NewPromiseResolveThenableJob
 * is a microtask that attaches the settle reaction to h, itself a microtask
 * once h is settled, so the interleaving with other microtasks matches a
 * real engine. kml_layout.h is prepended by the compiler. */
#include <stdlib.h>

typedef long long i64;
typedef void (*kml_fn0)(void *env);

#define F(p, off, T) (*(T *)((char *)(p) + (off)))

extern void __kml_microtask_enqueue(void *);
extern void __kml_promise_mark_handled(void *);
extern void __kml_promise_settle(void *, i64);

typedef struct {
    void *q, *h;
} adopt_env;

static void *closure_new(kml_fn0 fn, void *env) {
    void *clo = malloc(KML_CLOSURE_SIZE);
    F(clo, KML_CLOSURE_FN, kml_fn0) = fn;
    F(clo, KML_CLOSURE_ENV, void *) = env;
    return clo;
}

/* Run clo as a microtask when p settles (now, if it already has). */
void __kml_promise_attach(void *p, void *clo) {
    __kml_promise_mark_handled(p);
    if (F(p, KML_PROMISE_STATE, i64) != 0) {
        __kml_microtask_enqueue(clo);
        return;
    }
    void *node = malloc(KML_REACTION_SIZE);
    F(node, KML_REACTION_CLOSURE, void *) = clo;
    F(node, KML_REACTION_NEXT, void *) = F(p, KML_PROMISE_REACTIONS, void *);
    F(p, KML_PROMISE_REACTIONS, void *) = node;
}

/* The reaction h.then(resolveQ, rejectQ): copy h's settlement into q. */
void __kml_promise_adopt_settle(void *envp) {
    adopt_env *env = (adopt_env *)envp;
    void *q = env->q, *h = env->h;
    F(q, KML_PROMISE_V0, i64) = F(h, KML_PROMISE_V0, i64);
    F(q, KML_PROMISE_V1, i64) = F(h, KML_PROMISE_V1, i64);
    __kml_promise_settle(q, F(h, KML_PROMISE_STATE, i64));
}

/* NewPromiseResolveThenableJob: attach the settle reaction to h. */
void __kml_promise_adopt_job(void *envp) {
    __kml_promise_attach(((adopt_env *)envp)->h, closure_new(__kml_promise_adopt_settle, envp));
}

void __kml_promise_adopt(void *q, void *h) {
    adopt_env *env = (adopt_env *)malloc(sizeof(adopt_env));
    env->q = q;
    env->h = h;
    __kml_microtask_enqueue(closure_new(__kml_promise_adopt_job, env));
}
