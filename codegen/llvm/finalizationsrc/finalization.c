/* finalization.c — FinalizationRegistry runtime (TDD-00163; in C per
 * TDD-00240). One pending-callback path, several death signals, chosen by
 * -mm (KML_GC, and KML_FINREPORT for -finalizers=report under manual, are
 * defined by the compiler):
 *  - manual: __kml_finreg_onfree (the compiled free chokepoint) enqueues each
 *    live registration targeting the freed pointer; survivors are flushed by
 *    an atexit hook (with KML_FINREPORT it first prints one leak line per
 *    survivor).
 *  - gc: each register() also registers a Boehm finalizer whose callback only
 *    enqueues, never calling user code from collector context.
 * Death signals enqueue on the microtask FIFO, never execute. Cells are never
 * unlinked (`dead` marks fired/unregistered). Under gc `target` is left NULL
 * (a scanned reference would keep it alive) and `gc_next` chains earlier
 * registrations on the same target, since GC_register_finalizer replaces and
 * hands back the previous client data. The program's trampoline and report
 * functions arrive as pointers in the registry record. */
#include <stdio.h>
#include <stdlib.h>

typedef long long i64;
typedef void (*kml_tramp)(void *cl, i64 held);
typedef void (*kml_report)(i64 held, i64 line, i64 col);

extern void __kml_microtask_enqueue(void *closure);
extern void __kml_drain_microtasks(void);

typedef struct { /* __kml_finreg_create's 24-byte record */
    void *cl;
    kml_tramp tramp;
    kml_report report;
} kml_registry;

typedef struct kml_fincell { /* 72 bytes */
    struct kml_fincell *next;
    kml_registry *reg;
    void *target;
    i64 held;
    void *token;
    i64 line, col;
    i64 dead;
    struct kml_fincell *gc_next;
} kml_fincell;

/* The list head is thread-local under manual but a plain global under gc:
 * Boehm does not scan TLS blocks as roots, so a TLS-only-reachable cell
 * would be collected out from under its pending finalizer. */
#ifdef KML_GC
static kml_fincell *all;
extern void *GC_malloc_uncollectable(size_t n);
extern void GC_register_finalizer(void *obj, void (*fn)(void *, void *), void *cd,
                                  void (**ofn)(void *, void *), void **ocd);
extern int GC_invoke_finalizers(void);
#define ENV_ALLOC GC_malloc_uncollectable
#else
static _Thread_local kml_fincell *all;
#define ENV_ALLOC malloc
#endif
static char atexit_done;

static void finreg_atexit(void);

void *__kml_finreg_create(void *cl, void *tramp, void *rep) {
    kml_registry *r = (kml_registry *)malloc(sizeof *r);
    r->cl = cl;
    r->tramp = (kml_tramp)tramp;
    r->report = (kml_report)rep;
    if (!atexit_done) {
        atexit_done = 1;
        atexit(finreg_atexit);
    }
    return r;
}

/* Under gc a death callback fires from Boehm with the newest cell for the
 * collected target; defined below. */
#ifdef KML_GC
static void gc_cb(void *obj, void *cd);
#endif

void __kml_finreg_register(void *reg, void *target, i64 held, void *token, i64 line, i64 col) {
    kml_fincell *c = (kml_fincell *)malloc(sizeof *c);
    c->next = all;
    c->reg = (kml_registry *)reg;
#ifdef KML_GC
    c->target = NULL;
#else
    c->target = target;
#endif
    c->held = held;
    c->token = token;
    c->line = line;
    c->col = col;
    c->dead = 0;
    c->gc_next = NULL;
    all = c;
#ifdef KML_GC
    void (*ofn)(void *, void *);
    void *ocd = NULL;
    GC_register_finalizer(target, gc_cb, c, &ofn, &ocd);
    c->gc_next = (kml_fincell *)ocd;
#else
    (void)target;
#endif
}

/* Mark every live cell of this registry made with this token dead. */
unsigned char __kml_finreg_unregister(void *reg, void *token) {
    unsigned char found = 0;
    if (!token) return 0;
    for (kml_fincell *c = all; c; c = c->next) {
        if (c->reg == reg && c->token == token && c->dead == 0) {
            c->dead = 1;
            found = 1;
        }
    }
    return found;
}

typedef struct { /* thunk env: {trampoline, closure, held} */
    kml_tramp tramp;
    void *cl;
    i64 held;
} kml_finenv;

typedef struct { /* closure header {fn, env} */
    void (*fn)(void *env);
    void *env;
} kml_hdr;

static void fin_thunk(void *envp) {
    kml_finenv *env = (kml_finenv *)envp;
    env->tramp(env->cl, env->held);
}

/* Mark dead and push the cleanup callback onto the microtask FIFO: the one
 * path every death signal funnels into. Under gc the env/header are
 * uncollectable: between enqueue and drain they are reachable only through
 * the FIFO's thread-local buffer, which Boehm does not scan. */
static void enqueue_cell(kml_fincell *c) {
    c->dead = 1;
    kml_registry *reg = c->reg;
    kml_finenv *env = (kml_finenv *)ENV_ALLOC(sizeof *env);
    env->tramp = reg->tramp;
    env->cl = reg->cl;
    env->held = c->held;
    kml_hdr *h = (kml_hdr *)ENV_ALLOC(sizeof *h);
    h->fn = fin_thunk;
    h->env = env;
    __kml_microtask_enqueue(h);
}

#ifdef KML_GC
static void gc_cb(void *obj, void *cd) {
    (void)obj;
    for (kml_fincell *c = (kml_fincell *)cd; c; c = c->gc_next)
        if (c->dead == 0) enqueue_cell(c);
}
#else
/* Memory.free's chokepoint: every live registration targeting p fires. */
void __kml_finreg_onfree(void *p) {
    if (!p) return;
    for (kml_fincell *c = all; c; c = c->next)
        if (c->target == p && c->dead == 0) enqueue_cell(c);
}
#endif

/* Exit flush. Manual (report only): print one line per live registration,
 * then enqueue every survivor. Both modes finish by draining the microtask
 * FIFO so everything pending runs. */
static void finreg_atexit(void) {
#ifdef KML_GC
    GC_invoke_finalizers();
#else
#ifdef KML_FINREPORT
    i64 total = 0;
    for (kml_fincell *c = all; c; c = c->next)
        if (c->dead == 0) total++;
    if (total > 0) {
        printf("[finalizers] leak: %lld registration(s) never freed\n", total);
        for (kml_fincell *c = all; c; c = c->next)
            if (c->dead == 0) c->reg->report(c->held, c->line, c->col);
    }
#endif
    for (kml_fincell *c = all; c; c = c->next)
        if (c->dead == 0) enqueue_cell(c);
#endif
    __kml_drain_microtasks();
}
