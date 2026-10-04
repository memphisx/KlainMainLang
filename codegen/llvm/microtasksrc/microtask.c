/* microtask.c — the microtask FIFO and the process.nextTick queue
 * (TDD-00083 Stage 3; in C per TDD-00240). Entries are closure headers
 * {fn, env} run as 0-argument callbacks: a .then reaction is wrapped into
 * one at its call site, so the queues stay generic. Each isolate (thread)
 * has its own queues. kml_layout.h is prepended by the compiler. */
#include <stdlib.h>

typedef long long i64;
typedef void (*kml_fn0)(void *env);

/* The report of one unhandled rejection is generated with the program (it
 * boxes the promise for the listeners). */
extern void __kml_unhandled_report(void *p);

typedef struct {
    void **data;
    i64 len, cap, head;
} kml_queue;

static _Thread_local kml_queue mt, tq, unh;

static void q_push(kml_queue *q, void *cl) {
    if (q->len + 1 > q->cap) {
        i64 nc = q->cap * 2 > 8 ? q->cap * 2 : 8;
        q->data = (void **)realloc(q->data, (size_t)nc * sizeof(void *));
        q->cap = nc;
    }
    q->data[q->len++] = cl;
}

static void run_closure(void *cl) {
    kml_fn0 fn = *(kml_fn0 *)((char *)cl + KML_CLOSURE_FN);
    void *env = *(void **)((char *)cl + KML_CLOSURE_ENV);
    fn(env);
}

/* Run q's entries FIFO until empty; an entry may queue more, drained in the
 * same pass. */
static void q_drain(kml_queue *q) {
    while (q->head < q->len) {
        void *cl = q->data[q->head++];
        run_closure(cl);
    }
    q->head = 0;
    q->len = 0;
}

/* Unhandled rejections (Node's default, --unhandled-rejections=throw): a
 * promise rejected while nothing consumes it (no reaction, no awaiter) is
 * queued; every consumer marks it handled; after each checkpoint a queued
 * promise still unhandled is reported. */
static i64 *flags_of(void *p) { return (i64 *)((char *)p + KML_PROMISE_FLAGS); }

void __kml_promise_mark_handled(void *p) {
    if (p) *flags_of(p) |= KML_PROMISE_FLAG_HANDLED;
}

void __kml_promise_note_rejected(void *p) {
    char *pc = (char *)p;
    i64 *f = flags_of(p);
    if (*f & (KML_PROMISE_FLAG_HANDLED | KML_PROMISE_FLAG_QUEUED)) return;
    if (*(void **)(pc + KML_PROMISE_REACTIONS) || *(void **)(pc + KML_PROMISE_WAITER)) return;
    *f |= KML_PROMISE_FLAG_QUEUED;
    q_push(&unh, p);
}

void __kml_unhandled_check(void) {
    for (i64 i = 0; i < unh.len; i++) {
        void *p = unh.data[i];
        if (!(*flags_of(p) & KML_PROMISE_FLAG_HANDLED)) __kml_unhandled_report(p);
    }
    unh.len = 0;
}

void __kml_microtask_enqueue(void *cl) { q_push(&mt, cl); }
void __kml_nexttick_enqueue(void *cl) { q_push(&tq, cl); }
void __kml_drain_ticks(void) { q_drain(&tq); }
void __kml_drain_promise_jobs(void) { q_drain(&mt); }

/* The event loop must not block while jobs or ticks sit queued
 * (TDD-00097 Stage 5). */
_Bool __kml_microtasks_pending(void) { return mt.head < mt.len || tq.head < tq.len; }

/* Node's processTicksAndRejections: every queued tick, then every promise
 * job, again while a job queued a tick; then the checkpoint's report of
 * what is still unhandled (processPromiseRejections). */
/* ClearKeptObjects: the WeakRef targets kept during the job (weak.c sets
 * it), released once its microtasks have run. */
void (*__kml_clear_kept_hook)(void) = 0;

void __kml_drain_microtasks(void) {
    do {
        q_drain(&tq);
        q_drain(&mt);
    } while (tq.head < tq.len);
    __kml_unhandled_check();
    if (__kml_clear_kept_hook) __kml_clear_kept_hook();
}

/* The one tick a module top-level `await` of a plain value or a settled
 * promise takes: run exactly the jobs queued now (behind the waiting jobs,
 * ahead of what they enqueue). Both bounds are re-read every iteration: a
 * job may run a full drain of its own, which resets the indices. */
void __kml_microtask_tick(void) {
    i64 snap = mt.len;
    while (mt.head < snap && mt.head < mt.len) {
        void *cl = mt.data[mt.head++];
        run_closure(cl);
    }
    if (mt.head >= mt.len) {
        mt.head = 0;
        mt.len = 0;
    }
}

/* Reactions run at settlement itself rather than as jobs: a promise's view
 * under another static type (Promise<any>, a converted element type), which is
 * the same object in JS and so settles with it. Their runner functions. */
static kml_fn0 sync_fns[64];
static int nsync;

static int is_sync(kml_fn0 fn) {
    for (int i = 0; i < nsync; i++)
        if (sync_fns[i] == fn) return 1;
    return 0;
}

/* Register closure clo on pending promise p as a synchronous reaction: run
 * when p settles, inside the settlement. p's rejection is clo's to answer. */
void __kml_promise_attach_sync(void *p, void *clo) {
    kml_fn0 fn = *(kml_fn0 *)((char *)clo + KML_CLOSURE_FN);
    if (!is_sync(fn) && nsync < 64) sync_fns[nsync++] = fn;
    __kml_promise_mark_handled(p);
    void *node = malloc(KML_REACTION_SIZE);
    *(void **)((char *)node + KML_REACTION_CLOSURE) = clo;
    *(void **)((char *)node + KML_REACTION_NEXT) = *(void **)((char *)p + KML_PROMISE_REACTIONS);
    *(void **)((char *)p + KML_PROMISE_REACTIONS) = node;
}

/* Enqueue every reaction registered on promise p (a {closure, next} list,
 * newest first) in registration order, then clear the list. A rejection
 * nothing consumes is queued for the checkpoint's report. */
void __kml_promise_drain_reactions(void *p) {
    char *pc = (char *)p;
    void **rx = (void **)(pc + KML_PROMISE_REACTIONS);
    if (*(i64 *)(pc + KML_PROMISE_STATE) == 2)
        __kml_promise_note_rejected(p);
    void *prev = 0, *cur = *rx;
    while (cur) {
        void **nextp = (void **)((char *)cur + KML_REACTION_NEXT);
        void *next = *nextp;
        *nextp = prev;
        prev = cur;
        cur = next;
    }
    *rx = 0;
    for (void *node = prev; node; node = *(void **)((char *)node + KML_REACTION_NEXT)) {
        void *cl = *(void **)((char *)node + KML_REACTION_CLOSURE);
        if (nsync && is_sync(*(kml_fn0 *)((char *)cl + KML_CLOSURE_FN)))
            run_closure(cl);
        else
            __kml_microtask_enqueue(cl);
    }
}
