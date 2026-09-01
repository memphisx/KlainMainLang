/* klainsync — Go-fidelity goroutine runtime (TDD-00143, Stage 1).
 *
 * A GMP work-stealing scheduler with cooperative goroutines, fixed
 * guard-paged stacks over ucontext, and CSP channels whose blocking send/
 * receive park the *G* (the M keeps running other Gs), not the OS thread.
 *
 * This is the `klain:sync` embedded runtime: an explicitly-non-Node opt-in.
 * A program that never imports `klain:sync` links none of this and pays
 * nothing. Nothing here touches async/await, Promises, or Worker.
 *
 * Scope of THIS file (Stage 1): the scheduler, `go`, buffered/unbuffered
 * channels, and a function-entry cooperative safepoint hook. Loop-back-edge
 * safepoints, sysmon preempt-flagging, `select`, blocking-syscall P-handoff,
 * signal-based async preemption, and growable stacks are later stages.
 *
 * Channel elements are a fixed 8-byte slot (i64/f64/ptr all fit) — the
 * compiler bitcasts every Channel<T> element through an i64. That covers
 * number/string/object/boolean channels without per-type marshalling.
 *
 * GC interaction: under -mm=gc the compiler defines KLAINSYNC_GC=1; each M
 * thread registers with Boehm, and each goroutine stack is added as a root
 * region for its lifetime (conservative but correct — a live object reachable
 * only from a parked goroutine's stack must not be collected). Under manual
 * mode a goroutine's stack is freed on exit; a leaked blocked goroutine leaks,
 * documented like other manual-mode handles.
 */

#define _XOPEN_SOURCE 700 /* ucontext (getcontext/makecontext/swapcontext) */
#define _GNU_SOURCE        /* Linux: MAP_ANONYMOUS, sysconf constants */
#ifdef __APPLE__
#define _DARWIN_C_SOURCE   /* macOS: MAP_ANON, _SC_NPROCESSORS_ONLN under _XOPEN_SOURCE */
#endif
#include <errno.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <time.h>
#include <ucontext.h>
#include <unistd.h>

/* ------------------------------------------------------------------ *
 *  GC glue — weakly bound so manual-mode builds (which never link      *
 *  -lgc) resolve these to NULL and skip the calls entirely.            *
 * ------------------------------------------------------------------ */
#ifdef KLAINSYNC_GC
/* Declared by the Boehm headers we link against; we forward-declare the
 * few we need so this file needs no gc.h on the include path. */
extern int  GC_get_stack_base(void *);
extern int  GC_register_my_thread(void *);
extern int  GC_unregister_my_thread(void);
extern void GC_allow_register_threads(void);
extern void GC_add_roots(void *low, void *high_plus_one);
extern void GC_remove_roots(void *low, void *high_plus_one);
extern void *GC_malloc_uncollectable(size_t);
extern void GC_free(void *);
/* Control blocks live across threads and are reachable from queues the
 * collector cannot see into reliably; keep them uncollectable + scanned. */
#define KS_CTLALLOC(sz) GC_malloc_uncollectable(sz)
#define KS_CTLFREE(p) GC_free(p)
#else
#define KS_CTLALLOC(sz) calloc(1, (sz))
#define KS_CTLFREE(p) free(p)
#endif

/* ------------------------------------------------------------------ *
 *  Tunables                                                            *
 * ------------------------------------------------------------------ */
#define KS_STACK_BYTES (256 * 1024) /* per-goroutine fixed stack */
#define KS_RUNQ_CAP 256 /* per-P local ring capacity */
#define KS_SEL_MAX 64   /* max cases (and thus distinct channel locks) per select */

/* ------------------------------------------------------------------ *
 *  Goroutine (G)                                                       *
 * ------------------------------------------------------------------ */
enum ks_gstatus { KS_RUNNABLE, KS_RUNNING, KS_WAITING, KS_DEAD };

typedef struct ks_g {
    ucontext_t ctx;      /* saved execution context */
    void *stack;         /* mmap'd stack base (low address) */
    size_t stacksize;    /* usable size (excludes guard page) */
    void *stack_alloc;   /* full mmap base incl. guard page, for munmap */
    size_t alloc_size;
    void (*fn)(void *);  /* goroutine body: fn(env) */
    void *env;           /* closure environment */
    int status;
    int started;
    _Atomic int preempt; /* set by sysmon to request a yield at the next
                            cooperative safepoint; read+cleared by the G */
    struct ks_g *qnext;  /* intrusive link for run queues */
    /* LockOSThread (Go's runtime.LockOSThread): when non-NULL, this G is bound
     * to exactly one M and never migrates — it always resumes on locked_m,
     * even across preemption. Used by the Node event-loop/reactor goroutine,
     * whose ucontext connection fibers are thread-bound and cannot legally
     * swapcontext across OS threads. The G still preempts at safepoints; it
     * just re-runs on the same M. */
    struct ks_m *locked_m;
} ks_g;

/* ------------------------------------------------------------------ *
 *  Logical processor (P) — a local run queue                           *
 * ------------------------------------------------------------------ */
typedef struct ks_p {
    pthread_mutex_t mu;
    ks_g *ring[KS_RUNQ_CAP];
    int head, tail, count;
} ks_p;

/* ------------------------------------------------------------------ *
 *  Machine (M) — an OS thread bound to one P                           *
 * ------------------------------------------------------------------ */
typedef struct ks_m {
    pthread_t thread;
    ucontext_t sched_ctx;   /* the scheduler loop's own context */
    ks_p *p;                /* the P this M runs */
    /* The G currently running on this M and when it started (monotonic ns).
     * Written by this M in ks_execute, read by the sysmon thread — atomic so
     * that cross-thread read is well-defined; a stale read only costs one
     * spurious preempt flag, which the safepoint tolerates. */
    _Atomic(ks_g *) cur;
    _Atomic long long cur_start_ns;
    unsigned schedtick; /* schedule counter for the periodic global-queue poll */
    /* deferred park-unlock: a G parking on a channel hands its mutex here so
     * the scheduler releases it AFTER the context switch completes (so no
     * other M can ready+run this G while it is still switching out). */
    pthread_mutex_t *park_unlock;
    /* deferred multi-unlock for select: a G parking on several channels hands
     * all their mutexes here, released together after the switch-out. */
    pthread_mutex_t *park_unlock_list[KS_SEL_MAX];
    int park_unlock_n;
    /* deferred self-ready: a G yielding (gosched) hands itself here so the
     * scheduler re-enqueues it AFTER the switch-out, closing the same
     * double-run hazard the park path avoids. */
    ks_g *ready_after;
    /* LockOSThread: the G exclusively bound to this M (NULL when unlocked). A
     * locked M runs ONLY this G — it never pops its P, steals, or runs the
     * global queue — and parks on lcv when the locked G is not runnable. */
    _Atomic(ks_g *) locked_g;
    pthread_mutex_t lmu;
    pthread_cond_t lcv;
} ks_m;

/* ------------------------------------------------------------------ *
 *  Global scheduler state                                              *
 * ------------------------------------------------------------------ */
static int ks_nprocs;
static ks_p *ks_procs;
static ks_m *ks_machines;

/* global run queue (backstop + where the main thread's spawns land before
 * any M has stolen them) */
static pthread_mutex_t ks_glock = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t ks_gcond = PTHREAD_COND_INITIALIZER;
static ks_g *ks_ghead, *ks_gtail;
static int ks_idle;          /* # of Ms parked waiting for work */
static _Atomic long ks_live; /* # of live (non-dead) goroutines */

static pthread_once_t ks_once = PTHREAD_ONCE_INIT;
static _Thread_local ks_m *ks_curm; /* NULL on the main/non-M thread */
static _Thread_local ks_g *ks_curg; /* NULL when not running a G */

/* ------------------------------------------------------------------ *
 *  Run-queue plumbing                                                  *
 * ------------------------------------------------------------------ */
static void ks_global_push(ks_g *g) {
    pthread_mutex_lock(&ks_glock);
    g->qnext = NULL;
    if (ks_gtail)
        ks_gtail->qnext = g;
    else
        ks_ghead = g;
    ks_gtail = g;
    if (ks_idle > 0)
        pthread_cond_signal(&ks_gcond);
    pthread_mutex_unlock(&ks_glock);
}

static ks_g *ks_global_pop(void) {
    ks_g *g = ks_ghead;
    if (g) {
        ks_ghead = g->qnext;
        if (!ks_ghead)
            ks_gtail = NULL;
        g->qnext = NULL;
    }
    return g;
}

static int ks_p_push(ks_p *p, ks_g *g) {
    pthread_mutex_lock(&p->mu);
    if (p->count == KS_RUNQ_CAP) {
        pthread_mutex_unlock(&p->mu);
        return 0; /* full — caller falls back to the global queue */
    }
    p->ring[p->tail] = g;
    p->tail = (p->tail + 1) % KS_RUNQ_CAP;
    p->count++;
    pthread_mutex_unlock(&p->mu);
    return 1;
}

static ks_g *ks_p_pop(ks_p *p) {
    pthread_mutex_lock(&p->mu);
    ks_g *g = NULL;
    if (p->count > 0) {
        g = p->ring[p->head];
        p->head = (p->head + 1) % KS_RUNQ_CAP;
        p->count--;
    }
    pthread_mutex_unlock(&p->mu);
    return g;
}

/* Steal roughly half of victim's queue into thief; return one G to run now. */
static ks_g *ks_p_steal(ks_p *thief, ks_p *victim) {
    if (victim == thief)
        return NULL;
    pthread_mutex_lock(&victim->mu);
    int n = victim->count / 2;
    if (victim->count > 0 && n == 0)
        n = 1;
    ks_g *first = NULL;
    for (int i = 0; i < n; i++) {
        ks_g *g = victim->ring[victim->head];
        victim->head = (victim->head + 1) % KS_RUNQ_CAP;
        victim->count--;
        if (!first)
            first = g; /* run the first stolen G immediately */
        else if (thief)
            ks_p_push(thief, g);
        else
            ks_global_push(g); /* P-less rescue M: park extras globally */
    }
    pthread_mutex_unlock(&victim->mu);
    return first;
}

/* Make g runnable and find it a home. Called from any context. A P-less
 * rescue M (m->p == NULL) has no local queue, so it readies onto the global
 * queue. */
static void ks_ready(ks_g *g) {
    /* A LockOSThread'd G never enters a shared queue: wake its owning M, which
     * is the only M that will ever run it. Set status + signal together under
     * the M's lock so its findrunnable park cannot miss the wakeup. */
    ks_m *lm = g->locked_m;
    if (lm) {
        pthread_mutex_lock(&lm->lmu);
        g->status = KS_RUNNABLE;
        pthread_cond_signal(&lm->lcv);
        pthread_mutex_unlock(&lm->lmu);
        return;
    }
    g->status = KS_RUNNABLE;
    ks_m *m = ks_curm;
    if (m && m->p && ks_p_push(m->p, g))
        return;
    ks_global_push(g);
}

/* ------------------------------------------------------------------ *
 *  Scheduler loop (runs on each M's own thread/stack)                  *
 * ------------------------------------------------------------------ */
static ks_g *ks_findrunnable(ks_m *m) {
    /* A locked M runs ONLY its bound G: it never touches the shared queues, so
     * it can neither steal nor be stolen from. Wait until that G is runnable
     * (it parks here while the G is blocked on a channel/fetch), then run it. */
    ks_g *lg = atomic_load_explicit(&m->locked_g, memory_order_relaxed);
    if (lg) {
        pthread_mutex_lock(&m->lmu);
        while (lg->status != KS_RUNNABLE)
            pthread_cond_wait(&m->lcv, &m->lmu);
        pthread_mutex_unlock(&m->lmu);
        return lg;
    }
    for (;;) {
        /* Periodically poll the global queue first (Go's every-61 heuristic) so
         * a P with a steady stream of local work can't starve the global run
         * queue indefinitely. */
        if (++m->schedtick % 61 == 0) {
            pthread_mutex_lock(&ks_glock);
            ks_g *gg = ks_global_pop();
            pthread_mutex_unlock(&ks_glock);
            if (gg)
                return gg;
        }
        ks_g *g = m->p ? ks_p_pop(m->p) : NULL; /* rescue Ms have no local queue */
        if (g)
            return g;
        /* try the global queue */
        pthread_mutex_lock(&ks_glock);
        g = ks_global_pop();
        if (g) {
            pthread_mutex_unlock(&ks_glock);
            return g;
        }
        pthread_mutex_unlock(&ks_glock);
        /* try to steal */
        for (int i = 0; i < ks_nprocs; i++) {
            g = ks_p_steal(m->p, &ks_procs[i]);
            if (g)
                return g;
        }
        /* nothing to do — park until someone pushes work. An M never
         * self-terminates: it stays available for the next `go`. Go's
         * "main returns kills all" is realized by process exit() reaping
         * these parked M threads; there is no scheduler-level shutdown. */
        pthread_mutex_lock(&ks_glock);
        if (ks_ghead) {
            g = ks_global_pop();
            pthread_mutex_unlock(&ks_glock);
            return g;
        }
        ks_idle++;
        pthread_cond_wait(&ks_gcond, &ks_glock);
        ks_idle--;
        pthread_mutex_unlock(&ks_glock);
    }
}

static void ks_g_prime(ks_g *g); /* fwd */
static void ks_g_free(ks_g *g);  /* fwd */

static long long ks_now_ns(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (long long)ts.tv_sec * 1000000000LL + ts.tv_nsec;
}

static void ks_execute(ks_m *m, ks_g *g) {
    g->preempt = 0; /* clear any stale flag before this run window */
    atomic_store_explicit(&m->cur_start_ns, ks_now_ns(), memory_order_relaxed);
    atomic_store_explicit(&m->cur, g, memory_order_relaxed);
    g->status = KS_RUNNING;
    ks_curg = g;
    if (!g->started)
        ks_g_prime(g); /* build context lazily, on this M's stack */
    swapcontext(&m->sched_ctx, &g->ctx);
    /* back on the scheduler stack */
    ks_curg = NULL;
    atomic_store_explicit(&m->cur, NULL, memory_order_relaxed);
    /* Read the terminal status BEFORE any unlock/ready: at this instant g is
     * still unreachable to other Ms (a parked G's waker needs park_unlock,
     * still held; a completed/yielded G is in no queue), so this read cannot
     * race with a concurrent ks_ready. */
    int dead = (g->status == KS_DEAD);
    /* deferred park-unlock: release the channel lock now that g is fully
     * switched out and cannot be double-run. */
    if (m->park_unlock) {
        pthread_mutex_t *mu = m->park_unlock;
        m->park_unlock = NULL;
        pthread_mutex_unlock(mu);
    }
    /* select's multi-lock deferred unlock: release every channel lock the
     * selector held while enqueuing its cases, now that it is switched out. */
    for (int i = 0; i < m->park_unlock_n; i++)
        pthread_mutex_unlock(m->park_unlock_list[i]);
    m->park_unlock_n = 0;
    /* deferred self-ready (gosched): only now, switched out, is it safe to
     * make g runnable again. A yielding G goes to the *global* queue (its back),
     * not this P's local queue — otherwise findrunnable, which drains the local
     * queue first, would immediately re-pick it and starve everything in the
     * global queue (fatal with GOMAXPROCS=1: the preempted spinner would just
     * resume). This gives fair round-robin. */
    if (m->ready_after) {
        /* A locked G yielding stays on its own M — it must not enter the global
         * queue (no other M may run it). This M's next findrunnable returns it. */
        if (m->ready_after->locked_m == m) {
            m->ready_after->status = KS_RUNNABLE;
        } else {
            m->ready_after->status = KS_RUNNABLE;
            ks_global_push(m->ready_after);
        }
        m->ready_after = NULL;
    }
    /* reclaim a finished goroutine's stack (manual mode) / roots (gc mode). */
    if (dead)
        ks_g_free(g);
}

static void ks_sched_loop(ks_m *m) {
    for (;;)
        ks_execute(m, ks_findrunnable(m)); /* findrunnable blocks; never NULL */
}

/* ------------------------------------------------------------------ *
 *  Goroutine stack + entry                                             *
 * ------------------------------------------------------------------ */
static void ks_g_trampoline(void) {
    ks_g *g = ks_curg;
    g->fn(g->env);
    /* body returned — the goroutine is done. */
    g->status = KS_DEAD;
    atomic_fetch_sub(&ks_live, 1);
    /* Return to the CURRENT M's scheduler, not a fixed uc_link: a goroutine
     * that parked and resumed on a different M must hand control back to
     * whichever M is running it now (ks_curm), or it would fall through into
     * a stale M's context. This never returns (the G is dead). */
    swapcontext(&g->ctx, &ks_curm->sched_ctx);
}

/* Per-goroutine stack size, resolved once from KLAINSYNC_STACK_KB (default
 * KS_STACK_BYTES). Lower it to pack far more goroutines (at the cost of overflow
 * headroom); raise it for deep recursion. This is the tunable half of Stage 5 —
 * true growable/moving stacks (an 8 KiB start that copies-and-grows) need the
 * pointers into the stack to be relocatable, i.e. precise stack maps
 * (llvm.experimental.gc.statepoint), the same infrastructure TDD-00135's moving
 * collector needs; that is the documented architectural gate, not implemented
 * here. */
static size_t ks_stack_bytes = KS_STACK_BYTES;

static ks_g *ks_g_new(void (*fn)(void *), void *env) {
    ks_g *g = (ks_g *)KS_CTLALLOC(sizeof(ks_g));
    g->fn = fn;
    g->env = env;
    g->status = KS_RUNNABLE;

    /* Fixed goroutine stack, no overflow guard page: macOS makecontext faults
     * if the ucontext's stack region contains any PROT_NONE page, and the
     * existing HTTP/generator fibers likewise run on plain unguarded stacks.
     * Very deep recursion in a goroutine is undefined behaviour, documented
     * like the other fibers. */
    size_t total = ks_stack_bytes;
    void *base = mmap(NULL, total, PROT_READ | PROT_WRITE,
                      MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    if (base == MAP_FAILED) {
        fprintf(stderr, "klain:sync: goroutine stack mmap failed\n");
        abort();
    }
    g->stack_alloc = base;
    g->alloc_size = total;
    g->stack = base;
    g->stacksize = total;

#ifdef KLAINSYNC_GC
    /* Register the usable stack span as a conservative root region so a live
     * object reachable only from this (possibly parked) goroutine survives. */
    GC_add_roots(g->stack, (char *)g->stack + g->stacksize);
#endif
    return g;
}

static void ks_g_free(ks_g *g) {
#ifdef KLAINSYNC_GC
    GC_remove_roots(g->stack, (char *)g->stack + g->stacksize);
#endif
    munmap(g->stack_alloc, g->alloc_size);
    KS_CTLFREE(g);
}

/* First time a G is scheduled, build its context lazily so a never-run G
 * costs only its stack. Called on the scheduler stack right before switch. */
static void ks_g_prime(ks_g *g) {
    getcontext(&g->ctx);
    g->ctx.uc_stack.ss_sp = g->stack;
    g->ctx.uc_stack.ss_size = g->stacksize;
    g->ctx.uc_link = NULL; /* trampoline swaps back to ks_curm explicitly */
    makecontext(&g->ctx, ks_g_trampoline, 0);
    g->started = 1;
}

/* ------------------------------------------------------------------ *
 *  park / unpark for channel operations                                *
 * ------------------------------------------------------------------ */
/* Park the current G, deferring the release of `mu` to the scheduler.
 * Precondition: called while running a G, with `mu` held. */
static void ks_park(pthread_mutex_t *mu) {
    ks_g *g = ks_curg;
    ks_m *m = ks_curm;
    g->status = KS_WAITING;
    m->park_unlock = mu;
    swapcontext(&g->ctx, &m->sched_ctx);
    /* resumed: we are running again on some M (possibly a different one). */
}

/* Like ks_park, but defers the release of several mutexes (a select parking on
 * multiple channel locks). All are released after the switch-out completes. */
static void ks_park_all(pthread_mutex_t **mus, int n) {
    ks_g *g = ks_curg;
    ks_m *m = ks_curm;
    g->status = KS_WAITING;
    for (int i = 0; i < n; i++)
        m->park_unlock_list[i] = mus[i];
    m->park_unlock_n = n;
    swapcontext(&g->ctx, &m->sched_ctx);
}

/* ------------------------------------------------------------------ *
 *  Channels (hchan)                                                    *
 * ------------------------------------------------------------------ *
 * A blocked send/receive is represented by a `sudog` wait node. Two waiter
 * kinds coexist on the same queues so a goroutine and the main (non-M) thread
 * can rendezvous with each other:
 *   - a goroutine waiter parks its G (the M runs other Gs), woken via ks_ready;
 *   - a non-M-thread waiter (the program's main thread has no G to park)
 *     blocks on its own condvar, woken via a signal.
 * The sudog is stack-allocated in the blocking caller's frame, which stays
 * alive for the whole park (goroutine stack persists while parked; the thread
 * frame persists across cond_wait). */
enum ks_sgkind { KS_SG_G, KS_SG_THREAD };

/* A select groups one sudog per case behind a single atomic winner: the first
 * channel to make a case ready claims `won` via CAS; every other case's sudog
 * becomes stale and is discarded when a waker pops it. */
typedef struct ks_selgroup {
    _Atomic int won;      /* -1 until a case claims it, then the case index */
    int is_thread;        /* main/non-M-thread select: wait on mu/cv below */
    pthread_mutex_t mu;
    pthread_cond_t cv;
} ks_selgroup;

typedef struct ks_sudog {
    int kind;
    ks_g *g;               /* KS_SG_G: the parked goroutine */
    pthread_cond_t cv;     /* KS_SG_THREAD: wakeup condvar */
    int done;              /* KS_SG_THREAD: wakeup predicate */
    int64_t *elem;         /* send: value to hand off; recv: slot to fill */
    int closed;            /* set by the waker when close() woke this node */
    struct ks_sudog *next;
    /* select support (NULL/-1 for a plain send/receive) */
    ks_selgroup *group;    /* non-NULL when this sudog is one select case */
    int caseidx;           /* this case's index within the select */
    int32_t *select_ok;    /* select recv: where to write the ok flag */
    int select_is_send;    /* select case direction, for close handling */
} ks_sudog;

typedef struct ks_waitq {
    ks_sudog *head, *tail;
} ks_waitq;

typedef struct ks_chan {
    pthread_mutex_t mu;
    int64_t *buf;    /* ring of `cap` 8-byte slots (NULL if unbuffered) */
    int cap;
    int count;       /* # elements in buf */
    int sendx, recvx;
    int closed;
    ks_waitq recvq;  /* waiters blocked in receive */
    ks_waitq sendq;  /* waiters blocked in send */
} ks_chan;

static void ks_wq_push(ks_waitq *q, ks_sudog *sg) {
    sg->next = NULL;
    if (q->tail)
        q->tail->next = sg;
    else
        q->head = sg;
    q->tail = sg;
}

static ks_sudog *ks_wq_pop(ks_waitq *q) {
    ks_sudog *sg = q->head;
    if (sg) {
        q->head = sg->next;
        if (!q->head)
            q->tail = NULL;
        sg->next = NULL;
    }
    return sg;
}

/* Commit to a popped waiter. A plain sudog always commits; a select sudog
 * commits only if it wins its group's `won` CAS — otherwise it is stale (some
 * other case of the same select already fired) and the caller discards it. */
static int ks_sudog_acquire(ks_sudog *sg) {
    if (!sg->group)
        return 1;
    int expected = -1;
    return atomic_compare_exchange_strong(&sg->group->won, &expected, sg->caseidx);
}

/* Pop the next committable waiter, discarding stale select sudogs (already
 * removed from the queue, which is exactly what their owner wants). */
static ks_sudog *ks_wq_pop_acquire(ks_waitq *q) {
    for (;;) {
        ks_sudog *sg = ks_wq_pop(q);
        if (!sg)
            return NULL;
        if (ks_sudog_acquire(sg))
            return sg;
        /* stale: dropped. continue to the next waiter. */
    }
}

/* Remove a specific sudog from a queue if still present (a select waking up
 * pulls its remaining, un-fired sudogs out of the other channels). */
static void ks_wq_remove(ks_waitq *q, ks_sudog *target) {
    ks_sudog *prev = NULL, *cur = q->head;
    while (cur) {
        if (cur == target) {
            if (prev)
                prev->next = cur->next;
            else
                q->head = cur->next;
            if (q->tail == cur)
                q->tail = prev;
            cur->next = NULL;
            return;
        }
        prev = cur;
        cur = cur->next;
    }
}

/* Wake a dequeued waiter. Caller still holds the channel mutex; for a G that
 * is safe (ks_ready just enqueues); for a thread it signals its condvar. A
 * select waiter (its group already claimed) wakes the same way — the parked
 * selector/thread is on sg->g / sg->cv like any other waiter. */
static void ks_wq_wake(ks_sudog *sg) {
    if (sg->group && sg->group->is_thread) {
        /* a select run by the main/non-M thread: wake it via the group cond
         * (the winning case index is already committed in group->won). */
        pthread_mutex_lock(&sg->group->mu);
        pthread_cond_signal(&sg->group->cv);
        pthread_mutex_unlock(&sg->group->mu);
    } else if (sg->kind == KS_SG_G) {
        ks_ready(sg->g);
    } else {
        sg->done = 1;
        pthread_cond_signal(&sg->cv);
    }
}

/* Block the current caller on `q` with wait node `sg`, releasing `mu` while
 * parked and re-acquiring it before returning. Returns with `mu` held. */
static void ks_wq_block(ks_chan *c, ks_waitq *q, ks_sudog *sg) {
    if (ks_curg) {
        sg->kind = KS_SG_G;
        sg->g = ks_curg;
        ks_wq_push(q, sg);
        ks_park(&c->mu); /* deferred-unlocks c->mu after the switch-out */
        pthread_mutex_lock(&c->mu);
    } else {
        sg->kind = KS_SG_THREAD;
        sg->done = 0;
        pthread_cond_init(&sg->cv, NULL);
        ks_wq_push(q, sg);
        while (!sg->done)
            pthread_cond_wait(&sg->cv, &c->mu);
        pthread_cond_destroy(&sg->cv);
    }
}

void *klainsync_chan_new(int64_t capacity) {
    ks_chan *c = (ks_chan *)KS_CTLALLOC(sizeof(ks_chan));
    pthread_mutex_init(&c->mu, NULL);
    c->cap = (int)capacity;
    if (c->cap > 0)
        c->buf = (int64_t *)KS_CTLALLOC(sizeof(int64_t) * c->cap);
    return c;
}

/* Send v on channel c. Blocks (parks the G, or the thread if on main) until
 * the value is delivered or buffered. Panics on send-after-close. */
void klainsync_chan_send(void *ch, int64_t v) {
    ks_chan *c = (ks_chan *)ch;
    pthread_mutex_lock(&c->mu);
    for (;;) {
        if (c->closed) {
            pthread_mutex_unlock(&c->mu);
            fprintf(stderr, "klain:sync: send on closed channel\n");
            abort();
        }
        /* fast path: a receiver is already waiting — hand off directly. */
        ks_sudog *r = ks_wq_pop_acquire(&c->recvq);
        if (r) {
            *r->elem = v;
            if (r->select_ok)
                *r->select_ok = 1;
            ks_wq_wake(r);
            pthread_mutex_unlock(&c->mu);
            return;
        }
        /* buffered slot available? */
        if (c->cap > 0 && c->count < c->cap) {
            c->buf[c->sendx] = v;
            c->sendx = (c->sendx + 1) % c->cap;
            c->count++;
            pthread_mutex_unlock(&c->mu);
            return;
        }
        /* must block until a receiver takes v (or the channel is closed). */
        ks_sudog sg;
        int64_t slot = v;
        sg.elem = &slot;
        sg.closed = 0;
        sg.group = NULL;
        sg.select_ok = NULL;
        ks_wq_block(c, &c->sendq, &sg);
        if (sg.closed) {
            pthread_mutex_unlock(&c->mu);
            fprintf(stderr, "klain:sync: send on closed channel\n");
            abort();
        }
        pthread_mutex_unlock(&c->mu);
        return;
    }
}

/* Receive from c. Returns the element; *ok=1 on a real value, *ok=0 when the
 * channel is closed and drained (element is the zero value). Blocks otherwise. */
int64_t klainsync_chan_recv(void *ch, int32_t *ok) {
    ks_chan *c = (ks_chan *)ch;
    pthread_mutex_lock(&c->mu);
    for (;;) {
        /* buffered element available? */
        if (c->count > 0) {
            int64_t v = c->buf[c->recvx];
            c->recvx = (c->recvx + 1) % c->cap;
            c->count--;
            /* a blocked sender can now deposit into the freed slot */
            ks_sudog *s = ks_wq_pop_acquire(&c->sendq);
            if (s) {
                c->buf[c->sendx] = *s->elem;
                c->sendx = (c->sendx + 1) % c->cap;
                c->count++;
                ks_wq_wake(s);
            }
            pthread_mutex_unlock(&c->mu);
            if (ok)
                *ok = 1;
            return v;
        }
        /* unbuffered / no buffered element: a waiting sender? (rendezvous) */
        ks_sudog *s = ks_wq_pop_acquire(&c->sendq);
        if (s) {
            int64_t v = *s->elem;
            ks_wq_wake(s);
            pthread_mutex_unlock(&c->mu);
            if (ok)
                *ok = 1;
            return v;
        }
        if (c->closed) {
            pthread_mutex_unlock(&c->mu);
            if (ok)
                *ok = 0;
            return 0;
        }
        /* must block until a sender arrives (or the channel is closed). */
        ks_sudog sg;
        int64_t slot = 0;
        sg.elem = &slot;
        sg.closed = 0;
        sg.group = NULL;
        sg.select_ok = NULL;
        ks_wq_block(c, &c->recvq, &sg);
        if (sg.closed) {
            pthread_mutex_unlock(&c->mu);
            if (ok)
                *ok = 0;
            return 0;
        }
        /* a sender handed its value directly into our slot. */
        pthread_mutex_unlock(&c->mu);
        if (ok)
            *ok = 1;
        return slot;
    }
}

void klainsync_chan_close(void *ch) {
    ks_chan *c = (ks_chan *)ch;
    pthread_mutex_lock(&c->mu);
    if (c->closed) {
        pthread_mutex_unlock(&c->mu);
        fprintf(stderr, "klain:sync: close of closed channel\n");
        abort();
    }
    c->closed = 1;
    /* wake every blocked receiver (zero value + closed) and every blocked
     * sender (which then panics). Select waiters are acquired (claimed) first;
     * a stale one is discarded. */
    ks_sudog *sg;
    while ((sg = ks_wq_pop_acquire(&c->recvq))) {
        sg->closed = 1;
        if (sg->select_ok)
            *sg->select_ok = 0; /* select recv on a closed channel: ok=0 */
        ks_wq_wake(sg);
    }
    while ((sg = ks_wq_pop_acquire(&c->sendq))) {
        sg->closed = 1;
        ks_wq_wake(sg);
    }
    pthread_mutex_unlock(&c->mu);
}

/* ------------------------------------------------------------------ *
 *  select                                                              *
 * ------------------------------------------------------------------ *
 * Go's select: evaluate all cases, run one ready case; if several are ready
 * pick pseudo-randomly; with a `default` case never block; otherwise park the
 * goroutine on every case until one fires. A case is one channel operation
 * plus (in the emitted code) a handler; this runtime returns the index of the
 * case that fired (or -1 for default) and, for a recv case, fills recvval and
 * recv_ok — the emitted code then dispatches to that case's handler.
 *
 * The layout of ks_selcase must match what codegen builds (emit_sync.go). */
typedef struct {
    void *ch;        /* the channel */
    int64_t sendval; /* send case: value to send (in) */
    int64_t recvval; /* recv case: received value (out) */
    int32_t dir;     /* 0 = receive, 1 = send */
    int32_t recv_ok; /* recv case: 1 = value, 0 = channel closed (out) */
} ks_selcase;

/* A small per-thread xorshift RNG for select's fair tie-break. */
static _Thread_local uint64_t ks_rng;
static unsigned ks_rand(void) {
    uint64_t x = ks_rng;
    if (x == 0)
        x = (uint64_t)ks_now_ns() ^ (uint64_t)(uintptr_t)&x ^ 0x9e3779b97f4a7c15ULL;
    x ^= x << 13;
    x ^= x >> 7;
    x ^= x << 17;
    ks_rng = x;
    return (unsigned)(x >> 33);
}

int klainsync_select(void *cases_v, int n, int has_default) {
    ks_selcase *cases = (ks_selcase *)cases_v;
    if (n <= 0) {
        if (has_default)
            return -1;
        /* select {} with no default blocks forever (Go semantics). */
        if (ks_curg) {
            ks_curg->status = KS_WAITING;
            swapcontext(&ks_curg->ctx, &ks_curm->sched_ctx); /* never readied */
        } else {
            for (;;) {
                struct timespec req = {3600, 0};
                nanosleep(&req, NULL);
            }
        }
        return -1;
    }

    /* Distinct channel locks, sorted by address (canonical lock order). */
    ks_chan *locks[KS_SEL_MAX];
    int nl = 0;
    for (int i = 0; i < n; i++) {
        ks_chan *c = (ks_chan *)cases[i].ch;
        int found = 0;
        for (int j = 0; j < nl; j++)
            if (locks[j] == c) {
                found = 1;
                break;
            }
        if (!found)
            locks[nl++] = c;
    }
    for (int i = 1; i < nl; i++) {
        ks_chan *k = locks[i];
        int j = i - 1;
        while (j >= 0 && locks[j] > k) {
            locks[j + 1] = locks[j];
            j--;
        }
        locks[j + 1] = k;
    }
#define KS_SEL_UNLOCK_ALL()                       \
    do {                                          \
        for (int _i = 0; _i < nl; _i++)           \
            pthread_mutex_unlock(&locks[_i]->mu); \
    } while (0)
    for (int i = 0; i < nl; i++)
        pthread_mutex_lock(&locks[i]->mu);

    /* Pass 1: poll cases in a random rotation; run the first ready one. */
    unsigned start = n > 1 ? ks_rand() % (unsigned)n : 0;
    for (int k = 0; k < n; k++) {
        int i = (int)((start + (unsigned)k) % (unsigned)n);
        ks_chan *c = (ks_chan *)cases[i].ch;
        if (cases[i].dir == 0) { /* receive */
            if (c->count > 0) {
                int64_t v = c->buf[c->recvx];
                c->recvx = (c->recvx + 1) % c->cap;
                c->count--;
                ks_sudog *s = ks_wq_pop_acquire(&c->sendq);
                if (s) {
                    c->buf[c->sendx] = *s->elem;
                    c->sendx = (c->sendx + 1) % c->cap;
                    c->count++;
                    ks_wq_wake(s);
                }
                cases[i].recvval = v;
                cases[i].recv_ok = 1;
                KS_SEL_UNLOCK_ALL();
                return i;
            }
            ks_sudog *s = ks_wq_pop_acquire(&c->sendq);
            if (s) {
                cases[i].recvval = *s->elem;
                cases[i].recv_ok = 1;
                ks_wq_wake(s);
                KS_SEL_UNLOCK_ALL();
                return i;
            }
            if (c->closed) {
                cases[i].recvval = 0;
                cases[i].recv_ok = 0;
                KS_SEL_UNLOCK_ALL();
                return i;
            }
        } else { /* send */
            if (c->closed) {
                KS_SEL_UNLOCK_ALL();
                fprintf(stderr, "klain:sync: send on closed channel\n");
                abort();
            }
            ks_sudog *r = ks_wq_pop_acquire(&c->recvq);
            if (r) {
                *r->elem = cases[i].sendval;
                if (r->select_ok)
                    *r->select_ok = 1;
                ks_wq_wake(r);
                KS_SEL_UNLOCK_ALL();
                return i;
            }
            if (c->cap > 0 && c->count < c->cap) {
                c->buf[c->sendx] = cases[i].sendval;
                c->sendx = (c->sendx + 1) % c->cap;
                c->count++;
                KS_SEL_UNLOCK_ALL();
                return i;
            }
        }
    }

    /* Pass 2: no case ready. */
    if (has_default) {
        KS_SEL_UNLOCK_ALL();
        return -1;
    }

    /* Pass 3: block. Enqueue one sudog per case behind a shared group; the
     * first channel to make its case ready claims the group and wakes us. */
    ks_selgroup group;
    atomic_store(&group.won, -1);
    group.is_thread = (ks_curg == NULL);
    if (group.is_thread) {
        pthread_mutex_init(&group.mu, NULL);
        pthread_cond_init(&group.cv, NULL);
    }
    ks_sudog sgs[KS_SEL_MAX];
    for (int i = 0; i < n; i++) {
        ks_chan *c = (ks_chan *)cases[i].ch;
        sgs[i].group = &group;
        sgs[i].caseidx = i;
        sgs[i].closed = 0;
        sgs[i].kind = ks_curg ? KS_SG_G : KS_SG_THREAD;
        sgs[i].g = ks_curg;
        if (cases[i].dir == 0) {
            sgs[i].elem = &cases[i].recvval;
            sgs[i].select_ok = &cases[i].recv_ok;
            sgs[i].select_is_send = 0;
            ks_wq_push(&c->recvq, &sgs[i]);
        } else {
            sgs[i].elem = &cases[i].sendval;
            sgs[i].select_ok = NULL;
            sgs[i].select_is_send = 1;
            ks_wq_push(&c->sendq, &sgs[i]);
        }
    }

    if (ks_curg) {
        pthread_mutex_t *mus[KS_SEL_MAX];
        for (int i = 0; i < nl; i++)
            mus[i] = &locks[i]->mu;
        ks_park_all(mus, nl); /* releases all channel locks after switch-out */
        for (int i = 0; i < nl; i++)
            pthread_mutex_lock(&locks[i]->mu);
    } else {
        KS_SEL_UNLOCK_ALL(); /* channels unlocked; wait on the group */
        pthread_mutex_lock(&group.mu);
        while (atomic_load(&group.won) == -1)
            pthread_cond_wait(&group.cv, &group.mu);
        pthread_mutex_unlock(&group.mu);
        for (int i = 0; i < nl; i++)
            pthread_mutex_lock(&locks[i]->mu);
    }

    int winner = atomic_load(&group.won);
    /* Pull our remaining (un-fired) sudogs out of every channel. */
    for (int i = 0; i < n; i++) {
        ks_chan *c = (ks_chan *)cases[i].ch;
        if (cases[i].dir == 0)
            ks_wq_remove(&c->recvq, &sgs[i]);
        else
            ks_wq_remove(&c->sendq, &sgs[i]);
    }
    KS_SEL_UNLOCK_ALL();
    if (group.is_thread) {
        pthread_mutex_destroy(&group.mu);
        pthread_cond_destroy(&group.cv);
    }
    /* A send case woken by close() panics, like a direct send on a closed chan. */
    if (winner >= 0 && sgs[winner].select_is_send && sgs[winner].closed) {
        fprintf(stderr, "klain:sync: send on closed channel\n");
        abort();
    }
    return winner;
#undef KS_SEL_UNLOCK_ALL
}

/* ------------------------------------------------------------------ *
 *  M thread main                                                       *
 * ------------------------------------------------------------------ */
static void *ks_m_main(void *arg) {
    ks_m *m = (ks_m *)arg;
    ks_curm = m;
#ifdef KLAINSYNC_GC
    void *sb[2];
    GC_get_stack_base(sb);
    GC_register_my_thread(sb);
#endif
    ks_sched_loop(m);
#ifdef KLAINSYNC_GC
    GC_unregister_my_thread();
#endif
    return NULL;
}

/* ------------------------------------------------------------------ *
 *  sysmon — cooperative-preemption flagging                            *
 * ------------------------------------------------------------------ *
 * A background thread that flags a goroutine which has monopolised its M for
 * longer than KS_PREEMPT_NS. The flag is honoured at the next cooperative
 * safepoint the compiler inserted (function entry + loop back-edges), so a
 * tight, channel-free loop can no longer starve other goroutines sharing its
 * P. This is Go's tier-1 (cooperative) preemption; tier-2 signal-based async
 * preemption for safepoint-free code is a later stage. */
#define KS_PREEMPT_NS (10 * 1000 * 1000LL) /* 10ms run budget */
#define KS_SYSMON_SLEEP_NS (2 * 1000 * 1000LL)
#define KS_SYSCALL_NS (20 * 1000 * 1000LL) /* stuck-in-syscall threshold */

static _Atomic int ks_rescue_count;
static int ks_rescue_cap;

/* A P-less "rescue" M: spawned by sysmon when every P's M is stuck in a
 * blocking call and runnable goroutines are waiting. It owns no P (m->p ==
 * NULL) and drains work purely from the global queue and by stealing from the
 * stuck Ps, so a program whose goroutines all block in fs/fetch at once does
 * not deadlock. It parks when idle (bounded count), like any other M. This is
 * the V1 approximation of Go's entersyscall/exitsyscall P-handoff. */
static void *ks_rescue_main(void *arg) {
    ks_m *m = (ks_m *)arg;
    ks_curm = m;
#ifdef KLAINSYNC_GC
    void *sb[2];
    GC_get_stack_base(sb);
    GC_register_my_thread(sb);
#endif
    ks_sched_loop(m); /* never returns; parks when idle */
    return NULL;
}

/* Is there runnable work no running M is going to reach soon? */
static int ks_work_pending(void) {
    pthread_mutex_lock(&ks_glock);
    int g = (ks_ghead != NULL);
    pthread_mutex_unlock(&ks_glock);
    if (g)
        return 1;
    for (int i = 0; i < ks_nprocs; i++) {
        pthread_mutex_lock(&ks_procs[i].mu);
        int c = ks_procs[i].count;
        pthread_mutex_unlock(&ks_procs[i].mu);
        if (c > 0)
            return 1;
    }
    return 0;
}

static void *ks_sysmon_main(void *arg) {
    (void)arg;
    for (;;) {
        struct timespec req = {0, KS_SYSMON_SLEEP_NS};
        nanosleep(&req, NULL);
        long long now = ks_now_ns();
        int stuck = 0;
        for (int i = 0; i < ks_nprocs; i++) {
            ks_g *g = atomic_load_explicit(&ks_machines[i].cur, memory_order_relaxed);
            if (!g)
                continue;
            /* A LockOSThread'd G owns its M exclusively — preempting it buys no
             * fairness (its M runs nothing else) and its long select()/blocking
             * calls are legitimate, not a starving spinner. Leave it alone, and
             * never treat it as "stuck" work needing a rescue M. */
            if (g->locked_m)
                continue;
            long long start = atomic_load_explicit(&ks_machines[i].cur_start_ns, memory_order_relaxed);
            long long elapsed = now - start;
            if (elapsed > KS_PREEMPT_NS)
                atomic_store_explicit(&g->preempt, 1, memory_order_relaxed);
            /* Still the same G past the syscall threshold with its preempt flag
             * unheeded: it never reached a cooperative safepoint, so it is stuck
             * in a blocking C call (or a safepoint-free region — rescuing is
             * harmless either way). */
            if (elapsed > KS_SYSCALL_NS &&
                atomic_load_explicit(&g->preempt, memory_order_relaxed))
                stuck = 1;
        }
        /* If every P's M might be stuck, no M is idle to steal the stranded
         * work, and there IS runnable work, spawn one bounded rescue M. */
        if (stuck && ks_idle == 0 && ks_work_pending() &&
            atomic_load(&ks_rescue_count) < ks_rescue_cap) {
            ks_m *rm = (ks_m *)calloc(1, sizeof(ks_m));
            rm->p = NULL;
            pthread_mutex_init(&rm->lmu, NULL);
            pthread_cond_init(&rm->lcv, NULL);
            pthread_t t;
            if (pthread_create(&t, NULL, ks_rescue_main, rm) == 0) {
                pthread_detach(t);
                atomic_fetch_add(&ks_rescue_count, 1);
            } else {
                free(rm);
            }
        }
    }
    return NULL;
}

/* ------------------------------------------------------------------ *
 *  Lazy scheduler bootstrap                                            *
 * ------------------------------------------------------------------ */
static pthread_t ks_sysmon_thread;

static void ks_init(void) {
    long n = sysconf(_SC_NPROCESSORS_ONLN);
    const char *env = getenv("GOMAXPROCS");
    if (env && *env) {
        long v = strtol(env, NULL, 10);
        if (v > 0)
            n = v;
    }
    if (n < 1)
        n = 1;
    ks_nprocs = (int)n;
    /* Bound rescue-M growth under blocking-call pressure. */
    ks_rescue_cap = ks_nprocs * 4 + 4;
    /* Per-goroutine stack size (KiB) — clamped to a sane floor so makecontext
     * always has room for its initial frame. */
    const char *skb = getenv("KLAINSYNC_STACK_KB");
    if (skb && *skb) {
        long kb = strtol(skb, NULL, 10);
        if (kb >= 8)
            ks_stack_bytes = (size_t)kb * 1024;
    }
    ks_procs = (ks_p *)calloc(ks_nprocs, sizeof(ks_p));
    ks_machines = (ks_m *)calloc(ks_nprocs, sizeof(ks_m));
#ifdef KLAINSYNC_GC
    GC_allow_register_threads();
#endif
    for (int i = 0; i < ks_nprocs; i++)
        pthread_mutex_init(&ks_procs[i].mu, NULL);
    for (int i = 0; i < ks_nprocs; i++) {
        ks_machines[i].p = &ks_procs[i];
        pthread_mutex_init(&ks_machines[i].lmu, NULL);
        pthread_cond_init(&ks_machines[i].lcv, NULL);
        pthread_create(&ks_machines[i].thread, NULL, ks_m_main, &ks_machines[i]);
    }
    /* sysmon needs no P and never runs managed code, so it is not an M and is
     * not GC-registered; it only reads scheduler state and sets preempt flags. */
    pthread_create(&ks_sysmon_thread, NULL, ks_sysmon_main, NULL);
}

/* ------------------------------------------------------------------ *
 *  Public ABI called from emitted IR                                   *
 * ------------------------------------------------------------------ */
/* Spawn a goroutine running fn(env). Context is primed lazily on the M that
 * first executes it (ks_execute), so a never-run G costs only its stack. */
void klainsync_go(void *fn, void *env) {
    pthread_once(&ks_once, ks_init);
    atomic_fetch_add(&ks_live, 1);
    ks_g *g = ks_g_new((void (*)(void *))fn, env);
    ks_global_push(g);
}

/* Go's runtime.LockOSThread: bind the current goroutine to its current M so it
 * never migrates. Used by the Node event-loop/reactor goroutine, whose
 * ucontext connection fibers are thread-bound (a fiber created on thread A
 * cannot be swapcontext'd on thread B) and whose reactor state lives in
 * thread-local storage — both of which a migrating goroutine would corrupt.
 * The locked M then runs ONLY this G. Any other Gs already sitting in this M's
 * local run queue are flushed to the global queue so a peer M can run them
 * (this M no longer will). A no-op off a goroutine (main/non-M thread already
 * never migrates). Idempotent. */
void klainsync_lock_os_thread(void) {
    ks_g *g = ks_curg;
    ks_m *m = ks_curm;
    if (!g || !m)
        return;
    g->locked_m = m;
    atomic_store_explicit(&m->locked_g, g, memory_order_release);
    /* Drain this M's local queue to the global queue: the locked M's
     * findrunnable will never pop its P again, so anything left there would be
     * stranded. */
    if (m->p) {
        for (;;) {
            ks_g *other = ks_p_pop(m->p);
            if (!other)
                break;
            ks_global_push(other);
        }
    }
}

/* Undo klainsync_lock_os_thread. Not called by the reactor (it owns its M for
 * the process lifetime) but provided for completeness / future callers. */
void klainsync_unlock_os_thread(void) {
    ks_g *g = ks_curg;
    ks_m *m = ks_curm;
    if (!g || !m || g->locked_m != m)
        return;
    g->locked_m = NULL;
    atomic_store_explicit(&m->locked_g, NULL, memory_order_release);
}

/* Yield the current goroutine, returning it to the run queue so its P can run
 * another G. A no-op on the main/non-M thread. This is what a triggered
 * safepoint calls to hand off. */
void klainsync_gosched(void) {
    ks_g *g = ks_curg;
    ks_m *m = ks_curm;
    if (!g || !m)
        return;
    g->status = KS_RUNNABLE;
    m->ready_after = g; /* scheduler re-enqueues us after the switch-out */
    swapcontext(&g->ctx, &m->sched_ctx);
}

/* Cooperative preempt safepoint. Emitted by the compiler at function entry
 * (Stage 1) and, later, at loop back-edges (Stage 2). A single predictable
 * load + branch in the common case; yields only when sysmon has flagged this
 * goroutine. Safe to call from the main/non-M thread (ks_curg is NULL). */
void klainsync_safepoint(void) {
    ks_g *g = ks_curg;
    if (g && g->preempt) {
        g->preempt = 0;
        klainsync_gosched();
    }
}
