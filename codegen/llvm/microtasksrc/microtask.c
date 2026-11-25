/* microtask.c — the microtask FIFO and the process.nextTick queue
 * (TDD-00083 Stage 3; in C per TDD-00240). Entries are closure headers
 * {fn, env} run as 0-argument callbacks: a .then reaction is wrapped into
 * one at its call site, so the queues stay generic. Each isolate (thread)
 * has its own queues. kml_layout.h is prepended by the compiler. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <process.h>
#define kml_getpid _getpid
#else
#include <unistd.h>
#define kml_getpid getpid
#endif
#ifdef __APPLE__
#include <crt_externs.h>
#endif

typedef long long i64;
typedef void (*kml_fn0)(void *env);

/* The report of one unhandled rejection is generated with the program (it
 * boxes the promise for the listeners). */
extern void __kml_unhandled_report(void *p);

typedef struct {
    void **data;
    i64 len, cap, head;
} kml_queue;

static _Thread_local kml_queue mt, tq, unh, unh_ids;

static void q_push(kml_queue *q, void *cl) {
    if (q->len + 1 > q->cap) {
        i64 nc = q->cap * 2 > 8 ? q->cap * 2 : 8;
        q->data = (void **)realloc(q->data, (size_t)nc * sizeof(void *)); /* scratch */
        q->cap = nc;
    }
    q->data[q->len++] = cl;
}

static void run_closure(void *cl) {
    kml_fn0 fn = *(kml_fn0 *)((char *)cl + KML_CLOSURE_FN);
    void *env = *(void **)((char *)cl + KML_CLOSURE_ENV);
    /* A job runs on an empty stack, as in Node: no frame of whoever
     * drained the queue is its caller. */
    void *below = __kml_frame_top;
    __kml_frame_top = 0;
    fn(env);
    __kml_frame_top = below;
}

/* Run q's entries FIFO until empty; an entry may queue more, drained in the
 * same pass. */
extern _Thread_local volatile int *__kml_worker_term_flag;
extern void __kml_worker_terminate_now(void);

static void q_drain(kml_queue *q) {
    while (q->head < q->len) {
        /* terminate() ends a worker whose microtasks never run out, as
         * V8's interrupt ends Node's (the loop never gets the turn the
         * posted end waits for). */
        volatile int *tf = __kml_worker_term_flag;
        if (tf && *tf) __kml_worker_terminate_now();
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

#define TWIN_OF(p) (*(void **)((char *)(p) + KML_PROMISE_TWIN))

void __kml_promise_mark_handled(void *p) {
    /* p and the views it is one object with (its TWIN ring). */
    void *q = p;
    while (q) {
        *flags_of(q) |= KML_PROMISE_FLAG_HANDLED;
        q = TWIN_OF(q);
        if (q == p) break;
    }
}

/* view, src's Promise<any> view (or the reverse), becomes one object with
 * it: joined to src's TWIN ring, so a handler on either is one on both. The
 * view answers for an unhandled rejection from here on: src itself is
 * marked handled alone, the view only when src already was. */
void __kml_promise_twin(void *src, void *view) {
    if (*flags_of(src) & KML_PROMISE_FLAG_HANDLED) *flags_of(view) |= KML_PROMISE_FLAG_HANDLED;
    *flags_of(src) |= KML_PROMISE_FLAG_HANDLED;
    TWIN_OF(view) = TWIN_OF(src) ? TWIN_OF(src) : src;
    TWIN_OF(src) = view;
}

/* Each unhandled rejection's id, in the order they happen (Node's
 * lastPromiseId), and the one being reported. */
static _Thread_local i64 unh_last_id, unh_cur_id;

void __kml_promise_note_rejected(void *p) {
    char *pc = (char *)p;
    i64 *f = flags_of(p);
    if (*f & (KML_PROMISE_FLAG_HANDLED | KML_PROMISE_FLAG_QUEUED)) return;
    if (*(void **)(pc + KML_PROMISE_REACTIONS) || *(void **)(pc + KML_PROMISE_WAITER)) return;
    *f |= KML_PROMISE_FLAG_QUEUED;
    q_push(&unh, p);
    q_push(&unh_ids, (void *)(size_t)(++unh_last_id));
}

void __kml_unhandled_check(void) {
    for (i64 i = 0; i < unh.len; i++) {
        void *p = unh.data[i];
        unh_cur_id = (i64)(size_t)unh_ids.data[i];
        if (!(*flags_of(p) & KML_PROMISE_FLAG_HANDLED)) __kml_unhandled_report(p);
    }
    unh.len = 0;
    unh_ids.len = 0;
}

/* --unhandled-rejections, read from NODE_OPTIONS as Node reads its flags:
 * 0 throw (the default), 1 strict, 2 warn, 3 none, 4 warn-with-error-code.
 * --no-warnings silences the warnings. An unknown mode ends the process
 * before the program runs, as Node's option parser does. */
static int unh_mode;
static _Bool no_warnings;
static const char *const unh_modes[] = {"throw", "strict", "warn", "none", "warn-with-error-code"};

/* The program's argv[0], as the process was started. */
static const char *prog_argv0(void) {
#if defined(__APPLE__)
    char **av = *_NSGetArgv();
    if (av && av[0]) return av[0];
#elif defined(__GLIBC__)
    extern char *program_invocation_name;
    if (program_invocation_name) return program_invocation_name;
#endif
    return "node";
}

static _Bool word_is(const char *t, size_t n, const char *w) { return strlen(w) == n && strncmp(t, w, n) == 0; }

__attribute__((constructor)) static void unh_mode_init(void) {
    const char *s = getenv("NODE_OPTIONS");
    if (!s) return;
    static const char key[] = "--unhandled-rejections";
    size_t kl = sizeof key - 1;
    while (*s) {
        while (*s == ' ' || *s == '\t') s++;
        const char *t = s;
        while (*s && *s != ' ' && *s != '\t') s++;
        size_t n = (size_t)(s - t);
        if (word_is(t, n, "--no-warnings")) no_warnings = 1;
        if (n < kl || strncmp(t, key, kl) != 0 || (n > kl && t[kl] != '=')) continue;
        const char *v = t + kl + 1;
        size_t vn = n - kl - 1;
        if (n == kl) { /* `--unhandled-rejections none` */
            while (*s == ' ' || *s == '\t') s++;
            v = s;
            while (*s && *s != ' ' && *s != '\t') s++;
            vn = (size_t)(s - v);
        }
        int m = -1;
        for (int i = 0; i < 5; i++)
            if (word_is(v, vn, unh_modes[i])) m = i;
        if (m < 0) {
            fprintf(stderr, "%s: invalid value for --unhandled-rejections\n", prog_argv0());
            fflush(stderr);
            _exit(9);
        }
        unh_mode = m;
    }
}

int __kml_unhandled_mode(void) { return unh_mode; }
i64 __kml_unhandled_id(void) { return unh_cur_id; }

extern i64 __kml_str_len(const char *s);
static _Bool trace_helper_shown;

/* Node's emitUnhandledRejectionWarning: the reason (an Error-like one's
 * stack, another's noSideEffectsToString text), then the rejection's
 * notice, each a process warning on stderr. */
void __kml_unhandled_warn(const char *text) {
    if (no_warnings) return;
    int pid = (int)kml_getpid();
    fflush(stdout);
    fprintf(stderr, "(node:%d) UnhandledPromiseRejectionWarning: %.*s\n", pid, (int)__kml_str_len(text), text);
    if (!trace_helper_shown) {
        trace_helper_shown = 1;
        const char *a = prog_argv0(), *b = a;
        for (const char *c = a; *c; c++)
            if (*c == '/' || *c == '\\') b = c + 1;
        size_t bn = strlen(b);
        if (bn > 4 && strcmp(b + bn - 4, ".exe") == 0) bn -= 4;
        fprintf(stderr, "(Use `%.*s --trace-warnings ...` to show where the warning was created)\n", (int)bn, b);
    }
    fprintf(stderr,
            "(node:%d) UnhandledPromiseRejectionWarning: Unhandled promise rejection. This error originated "
            "either by throwing inside of an async function without a catch block, or by rejecting a promise "
            "which was not handled with .catch(). To terminate the node process on unhandled promise "
            "rejection, use the CLI flag `--unhandled-rejections=strict` (see "
            "https://nodejs.org/api/cli.html#cli_unhandled_rejections_mode). (rejection id: %lld)\n",
            pid, unh_cur_id);
    fflush(stderr);
}

void __kml_microtask_enqueue(void *cl) { q_push(&mt, cl); }
void __kml_nexttick_enqueue(void *cl) { q_push(&tq, cl); }

/* __kml_native.nextTick (lib/node/kml_process_tick.ts): a callback queued on
 * the tick queue, as the (invoke, closure) pair a native callback is. */
typedef void (*tick_inv)(void *clo);

static void tick_tramp(void *env) {
    void **e = (void **)env;
    ((tick_inv)e[0])(e[1]);
}

void __kml_native_next_tick(void *inv, void *clo) {
    void **env = (void **)kml_alloc(KML_HEAP_WORDS, 2 * sizeof(void *));
    env[0] = inv;
    env[1] = clo;
    void **cl = (void **)kml_closure_alloc(2 * sizeof(void *));
    cl[0] = (void *)tick_tramp;
    cl[1] = env;
    q_push(&tq, cl);
}
/* Jobs a producer could not queue where it learned of them: a collector's
 * finalizer runs inside an allocation, possibly one q_push itself made, so
 * finalization.c parks its cleanups and this moves them onto the queue
 * before anything reads it. */
void (*__kml_microtask_prefill_hook)(void) = 0;

static void prefill(void) {
    if (__kml_microtask_prefill_hook) __kml_microtask_prefill_hook();
}

void __kml_drain_ticks(void) { q_drain(&tq); }
void __kml_drain_promise_jobs(void) {
    prefill();
    q_drain(&mt);
}

/* The event loop must not block while jobs or ticks sit queued
 * (TDD-00097 Stage 5). */
_Bool __kml_microtasks_pending(void) {
    prefill();
    return mt.head < mt.len || tq.head < tq.len;
}

/* Node's processTicksAndRejections: every queued tick, then every promise
 * job, again while a job queued a tick; then the checkpoint's report of
 * what is still unhandled (processPromiseRejections). */
/* ClearKeptObjects: the WeakRef targets kept during the job (weak.c sets
 * it), released once its microtasks have run. */
void (*__kml_clear_kept_hook)(void) = 0;

void __kml_drain_microtasks(void) {
    prefill();
    do {
        q_drain(&tq);
        q_drain(&mt);
    } while (tq.head < tq.len);
    __kml_unhandled_check();
    if (__kml_clear_kept_hook) __kml_clear_kept_hook();
}

/* main()'s drain after the top-level script (hooks.c): an ES module's top
 * level is itself a promise job, so the jobs it queued run before the
 * ticks, as in Node. */
extern void (*__kml_main_drain_hook)(int module);
static void main_drain(int module) {
    if (module) __kml_drain_promise_jobs();
    __kml_drain_microtasks();
}
__attribute__((constructor)) static void main_drain_init(void) { __kml_main_drain_hook = main_drain; }

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
    void *node = kml_alloc(KML_HEAP_WORDS, KML_REACTION_SIZE);
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
