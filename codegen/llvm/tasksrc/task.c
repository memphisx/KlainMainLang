/* task.c — the async-task (coroutine) runtime (TDD-00083 Stage 2; in C per
 * TDD-00240). A task is a fiber running one async-function body, with its own
 * park state, jmpbuf stack and promise. Every task leaves through
 * swapcontext(self.ctx, self.resumerCtx); whoever swaps in writes resumerCtx
 * (the spawner, so a body that never suspends returns to its caller; else a
 * per-call slot of the scheduler or the resume runner). Under -mm=gc whoever
 * swaps into a fiber repoints the GC stack bottom at that fiber's stack and
 * restores it after the swap. kml_layout.h is prepended by the compiler. */
#include <stdlib.h>

typedef long long i64;
typedef unsigned char u8;
typedef void (*kml_body)(void *);

#define F(p, off, T) (*(T *)((char *)(p) + (off)))

/* ucontext size and field offsets per platform (win32io.c's kml_ucontext on
 * Windows). */
#if defined(_WIN32)
#define UC_SIZE 64
#define UC_SS_SP 8
#define UC_SS_SIZE 16
#define UC_LINK 24
#elif defined(__APPLE__)
#define UC_SIZE 880
#define UC_SS_SP 8
#define UC_SS_SIZE 16
#define UC_LINK 32
#elif defined(__aarch64__)
#define UC_SIZE 4560
#define UC_SS_SP 16
#define UC_SS_SIZE 32
#define UC_LINK 8
#else
#define UC_SIZE 968
#define UC_SS_SP 16
#define UC_SS_SIZE 32
#define UC_LINK 8
#endif

#define TASK_STACK_BYTES (256 * 1024)

/* Declared as the emitted IR declares them; no system header, so the
 * prototypes cannot clash with libc's. */
#pragma clang diagnostic ignored "-Wincompatible-library-redeclaration"
extern void getcontext(void *);
extern void makecontext(void *, void (*)(void), int, ...);
extern int swapcontext(void *, void *);
#if defined(_WIN32)
extern int _setjmp(void *, void *) __attribute__((returns_twice));
#define KML_SETJMP(b) _setjmp((b), 0)
extern void __kml_ctx_release(void *);
#else
extern int setjmp(void *) __attribute__((returns_twice));
#define KML_SETJMP(b) setjmp(b)
static void __kml_ctx_release(void *ctx) { (void)ctx; }
#endif
extern int usleep(unsigned);

/* The generated program's side. */
extern _Thread_local void *__kml_current_task;
extern _Thread_local void *__kml_root_async_ctx;
extern _Thread_local u8 __kml_conn_poke;
extern _Thread_local char __kml_main_ctx[];
extern _Thread_local void *__kml_conn_data;
extern _Thread_local i64 __kml_current_conn_idx;
extern _Thread_local void *__kml_curl_multi;
extern _Thread_local void *__kml_thrown;
extern _Thread_local void *__kml_cur_jmp_stk;
extern _Thread_local int __kml_jmp_top;
extern void *__kml_push_jmpbuf(void);
extern void __kml_pop_jmpbuf(void);
extern void *__kml_jmp_stack_new(void);
extern void __kml_jmp_stack_free(void *);
extern int curl_multi_perform(void *, int *);
extern void __kml_curl_drain_messages(void);
extern void __kml_reqbody_pump(void);
extern _Bool __kml_reqbody_active_on_fd(i64);
extern _Bool __kml_group_satisfied(void *);
extern void __kml_worker_abort_check(void);
extern int __kml_loop_turn(_Bool);
extern _Bool __kml_timer_fire_next(void);
extern _Bool __kml_fetch_pump(void);
extern void __kml_tla_unsettled(void) __attribute__((noreturn));
extern void __kml_top_await(void *);
extern void __kml_drain_microtasks(void);
extern void __kml_microtask_enqueue(void *);
extern void __kml_promise_drain_reactions(void *);
extern void __kml_promise_note_rejected(void *);
extern void __kml_promise_mark_handled(void *);

#ifdef KLAIN_GC
extern void *__kml_task_stack_alloc(i64);
extern void __kml_task_stack_free(void *, i64);
extern _Thread_local void *__kml_gc_orig_stackbottom;
#if defined(_WIN32)
extern void __kml_gc_sb_cur(void);
#define GC_SB_SET(p) ((void)(p), __kml_gc_sb_cur())
#elif defined(KLAIN_GC_WORKERS)
extern void __kml_gc_set_sb(void *);
#define GC_SB_SET(p) __kml_gc_set_sb(p)
#else
extern void *GC_stackbottom;
#define GC_SB_SET(p) (GC_stackbottom = (p))
#endif
#define STACK_ALLOC(n) __kml_task_stack_alloc(n)
#define STACK_FREE(s, n) __kml_task_stack_free((s), (n))
#else
#define GC_SB_SET(p) ((void)(p))
#define STACK_ALLOC(n) malloc((size_t)(n))
#define STACK_FREE(s, n) ((void)(n), free(s))
#endif

_Thread_local void *__kml_task_launching;
_Thread_local i64 __kml_task_active;
static _Thread_local void **task_data;
static _Thread_local i64 task_len, task_cap, sched_depth;

#define T_CTX(t) F(t, KML_TASK_CTX, void *)
#define T_STACK(t) F(t, KML_TASK_STACK, void *)
#define T_PROM(t) F(t, KML_TASK_PROMISE, void *)
#define T_STATE(t) F(t, KML_TASK_STATE, i64)
#define T_PFETCH(t) F(t, KML_TASK_PENDING_FETCH, void *)
#define T_PGROUP(t) F(t, KML_TASK_PENDING_GROUP, void *)
#define T_PPROM(t) F(t, KML_TASK_PENDING_PROMISE, void *)
#define T_FN(t) F(t, KML_TASK_FN, kml_body)
#define T_ARGS(t) F(t, KML_TASK_ARGS, void *)
#define T_RESUMER(t) F(t, KML_TASK_RESUMER, void *)
#define T_JMPSTK(t) F(t, KML_TASK_JMPSTK, void *)
#define T_JMPTOP(t) F(t, KML_TASK_JMPTOP, i64)
#define T_ALS(t) F(t, KML_TASK_ASYNC_CTX, void *)
#define T_STACKSZ(t) F(t, KML_TASK_STACK_SIZE, i64)
#define P_STATE(p) F(p, KML_PROMISE_STATE, i64)
#define P_WAITER(p) F(p, KML_PROMISE_WAITER, void *)
#define P_V0(p) F(p, KML_PROMISE_V0, i64)

#ifdef KLAIN_GC
/* The GC stack bottom back to the swapper's stack: the process stack on main,
 * else the current task's. */
void __kml_task_gc_restore(void) {
    void *ct = __kml_current_task;
    if (!ct) {
        GC_SB_SET(__kml_gc_orig_stackbottom);
        return;
    }
    GC_SB_SET((char *)T_STACK(ct) + T_STACKSZ(ct));
}
#define GC_RESTORE() __kml_task_gc_restore()
#define GC_INTO(t) GC_SB_SET((char *)T_STACK(t) + T_STACKSZ(t))
#else
#define GC_RESTORE() ((void)0)
#define GC_INTO(t) ((void)(t))
#endif

/* The swap out of a task that settled its promise: wake its waiter, mark it
 * done, poke the connection scan (a parked connection fiber may await exactly
 * this promise), run the promise's reactions, and leave. */
static void task_leave_settled(void *task, void *prom) {
    void *w = P_WAITER(prom);
    if (w) T_PPROM(w) = 0;
    T_STATE(task) = 2;
    __kml_task_active--;
    __kml_conn_poke = 1;
    __kml_promise_drain_reactions(prom);
    swapcontext(T_CTX(task), T_RESUMER(task));
}

/* The body stored its result in the promise's v0/v1. */
void __kml_task_finish(void *task) {
    void *prom = T_PROM(task);
    P_STATE(prom) = 1;
    task_leave_settled(task, prom);
}

/* A throw the body did not catch: the promise rejects with the error object. */
void __kml_task_reject(void *task, void *err) {
    void *prom = T_PROM(task);
    P_V0(prom) = (i64)err;
    P_STATE(prom) = 2;
    __kml_promise_note_rejected(prom);
    task_leave_settled(task, prom);
}

/* The makecontext entry: run the launched task's body under a task-level
 * catch-all. */
void __kml_task_trampoline(void) {
    void *t = __kml_task_launching;
    kml_body fn = T_FN(t);
    void *args = T_ARGS(t);
    void *jb = __kml_push_jmpbuf();
    if (KML_SETJMP(jb) != 0) {
        __kml_task_reject(t, __kml_thrown);
        return;
    }
    fn(args);
    __kml_pop_jmpbuf();
    __kml_task_finish(t);
}

void __kml_task_register(void *t) {
    if (task_len + 1 > task_cap) {
        i64 nc = task_cap * 2 > 8 ? task_cap * 2 : 8;
        task_data = (void **)realloc(task_data, (size_t)nc * sizeof(void *));
        task_cap = nc;
    }
    task_data[task_len++] = t;
    __kml_task_active++;
}

/* Called by whoever swapped into t, once its own state is restored: a done
 * task is off its stack for good, so its stack, context and jmpbuf stack go.
 * The struct goes in __kml_task_compact once it has left the task array. */
void __kml_task_reclaim(void *t) {
    if (T_STATE(t) != 2 || !T_STACK(t)) return;
    void *ctx = T_CTX(t);
    __kml_ctx_release(ctx);
    STACK_FREE(T_STACK(t), T_STACKSZ(t));
    free(ctx);
    __kml_jmp_stack_free(T_JMPSTK(t));
    T_STACK(t) = 0;
    T_CTX(t) = 0;
    T_JMPSTK(t) = 0;
}

/* Drops finished tasks from the array, in order. Only the outermost
 * scheduler step calls it. Waiter registrations are undone on resume
 * (__kml_task_unwait) and resume closures are consumed when they run, so
 * nothing names a done task's struct any more. */
void __kml_task_compact(void) {
    i64 w = 0;
    for (i64 i = 0; i < task_len; i++) {
        void *t = task_data[i];
        if (T_STATE(t) == 2)
            free(t);
        else
            task_data[w++] = t;
    }
    task_len = w;
}

void *__kml_spawn_task_ex(void *fn, void *args, void *promiseSlot, i64 stackBytes, void (*tramp)(void)) {
    void *t = malloc(KML_TASK_SIZE);
    void *ctx = malloc(UC_SIZE);
    void *stack = STACK_ALLOC(stackBytes);
    getcontext(ctx);
    F(ctx, UC_SS_SP, void *) = stack;
    F(ctx, UC_SS_SIZE, i64) = stackBytes;
    F(ctx, UC_LINK, void *) = __kml_main_ctx;
    makecontext(ctx, tramp, 0);
    T_STACKSZ(t) = stackBytes;
    T_CTX(t) = ctx;
    T_STACK(t) = stack;
    T_PROM(t) = promiseSlot;
    T_STATE(t) = 0;
    T_PFETCH(t) = 0;
    T_PGROUP(t) = 0;
    T_PPROM(t) = 0;
    T_FN(t) = (kml_body)fn;
    T_ARGS(t) = args;
    /* TDD-00168: inherit the spawner's AsyncLocalStorage frame. */
    void *prev = __kml_current_task;
    T_ALS(t) = prev ? T_ALS(prev) : __kml_root_async_ctx;
    T_JMPSTK(t) = __kml_jmp_stack_new();
    T_JMPTOP(t) = 0;
    __kml_task_register(t);
    /* The caller's resume point is a per-call slot: never the main context
     * (the spawner may be a connection fiber whose uc_link it is — TDD-00097
     * Stage 5b) and never the parent task's own context. */
    _Alignas(16) char callerctx[UC_SIZE];
    T_RESUMER(t) = callerctx;
    void *callerStk = __kml_cur_jmp_stk;
    int callerTop = __kml_jmp_top;
    __kml_cur_jmp_stk = T_JMPSTK(t);
    __kml_jmp_top = 0;
    __kml_current_task = t;
    __kml_task_launching = t;
    GC_INTO(t);
    swapcontext(callerctx, ctx);
    __kml_current_task = prev;
    GC_RESTORE();
    __kml_cur_jmp_stk = callerStk;
    __kml_jmp_top = callerTop;
    __kml_worker_abort_check();
    __kml_task_reclaim(t);
    return t;
}

void *__kml_spawn_task(void *fn, void *args, void *promiseSlot) {
    return __kml_spawn_task_ex(fn, args, promiseSlot, TASK_STACK_BYTES, __kml_task_trampoline);
}

/* Is a suspended task's park condition satisfied: a done or headers-done
 * fetch, a satisfied fetch group, a settled awaited promise, or no reason? */
static _Bool task_ready(void *t) {
    void *pf = T_PFETCH(t);
    if (pf) return F(pf, KML_FETCHREQ_DONE, i64) != 0 || F(pf, KML_FETCHREQ_HEADERS_DONE, i64) != 0;
    void *pg = T_PGROUP(t);
    if (pg) return __kml_group_satisfied(pg);
    void *pp = T_PPROM(t);
    return !pp || P_STATE(pp) != 0;
}

/* Swap into t from a per-call slot and back; the swapper's jmpbuf stack, GC
 * stack bottom and current task are restored after. */
static void task_swap_in(void *t) {
    _Alignas(16) char saveslot[UC_SIZE];
    T_PPROM(t) = 0;
    T_STATE(t) = 0;
    T_RESUMER(t) = saveslot;
    void *mstk = __kml_cur_jmp_stk;
    int mtop = __kml_jmp_top;
    __kml_cur_jmp_stk = T_JMPSTK(t);
    __kml_jmp_top = (int)T_JMPTOP(t);
    __kml_current_task = t;
    GC_INTO(t);
    swapcontext(saveslot, T_CTX(t));
    __kml_current_task = 0;
    GC_RESTORE();
    __kml_cur_jmp_stk = mstk;
    __kml_jmp_top = mtop;
    __kml_worker_abort_check();
    __kml_task_reclaim(t);
}

/* One scheduler step: pump libcurl, then resume each suspended task whose park
 * condition holds. Re-entrant: a resumed task may drive it (ADR-00985). */
void __kml_task_sched_step(void) {
    __kml_reqbody_pump();
    if (__kml_curl_multi) {
        int run;
        curl_multi_perform(__kml_curl_multi, &run);
        __kml_curl_drain_messages();
    }
    /* Only the outermost step compacts: a nested one must not move entries
     * under the scan in progress above it. */
    if (sched_depth++ == 0) __kml_task_compact();
    /* task_data/task_len reread each pass: a resumed task can spawn tasks and
     * grow the array. */
    for (i64 i = 0; i < task_len; i++) {
        void *t = task_data[i];
        if (T_STATE(t) != 1 || !task_ready(t)) continue;
        T_PGROUP(t) = 0;
        T_PFETCH(t) = 0;
        task_swap_in(t);
    }
    sched_depth--;
}

/* Folded into the loop's "never block while work is ready" test. */
_Bool __kml_task_resumable(void) {
    for (i64 i = 0; i < task_len; i++) {
        void *t = task_data[i];
        if (T_STATE(t) == 1 && task_ready(t)) return 1;
    }
    return 0;
}

/* A task holds the loop when it can run now or is parked on a fetch or a
 * fetch group (its transfer may not have reached libcurl yet). One parked on a
 * promise holds nothing, as in Node. */
_Bool __kml_task_holds_loop(void) {
    if (__kml_task_resumable()) return 1;
    for (i64 i = 0; i < task_len; i++) {
        void *t = task_data[i];
        if (T_STATE(t) == 1 && (T_PFETCH(t) || T_PGROUP(t))) return 1;
    }
    return 0;
}

/* Drops task from the waiter field of each member still naming it. */
void __kml_task_unwait(void **members, i64 count, void *task) {
    for (i64 i = 0; i < count; i++)
        if (P_WAITER(members[i]) == task) P_WAITER(members[i]) = 0;
}

/* The top-level wait of the member awaits: a turn of the real event loop
 * (TDD-00223 §1). Returns 0 to give up after a second idle verdict. */
static void top_wait(_Bool *idleseen) {
    switch (__kml_loop_turn(0)) {
    case 1:
        /* Nothing alive can wake the loop. The turn's last pass may still have
         * settled a member, so rescan once. */
        if (*idleseen) __kml_tla_unsettled();
        *idleseen = 1;
        return;
    case 2:
        /* Inside a loop callback: the subset drive, which may not re-enter the
         * loop under its own iteration. */
        __kml_drain_microtasks();
        __kml_task_sched_step();
        __kml_timer_fire_next();
        __kml_fetch_pump();
        break;
    }
    *idleseen = 0;
}

/* On a task: register as every member's waiter, set a non-null pending
 * promise (members[0], pending) so only a settling member wakes it, and park. */
static void park_on_members(void *ct, void **members, i64 count) {
    T_PPROM(ct) = members[0];
    for (i64 i = 0; i < count; i++) P_WAITER(members[i]) = ct;
    T_STATE(ct) = 1;
    T_JMPTOP(ct) = (i64)(unsigned)__kml_jmp_top;
    swapcontext(T_CTX(ct), T_RESUMER(ct));
    GC_RESTORE();
    __kml_task_unwait(members, count, ct);
}

/* Promise.race over task promises: the first settled member's index. */
i64 __kml_task_await_any_of(void **members, i64 count) {
    _Bool idleseen = 0;
    for (;;) {
        for (i64 i = 0; i < count; i++)
            if (P_STATE(members[i]) != 0) return i;
        void *ct = __kml_current_task;
        if (ct)
            park_on_members(ct, members, count);
        else
            top_wait(&idleseen);
    }
}

/* Promise.any over task promises: the first fulfilled member's index, or -1
 * once every member rejected. */
i64 __kml_task_await_first_fulfilled(void **members, i64 count) {
    _Bool idleseen = 0;
    for (;;) {
        _Bool pend = 0;
        for (i64 i = 0; i < count; i++) {
            i64 r = P_STATE(members[i]);
            if (r == 1) return i;
            pend |= r == 0;
        }
        if (!pend) return -1;
        void *ct = __kml_current_task;
        if (ct)
            park_on_members(ct, members, count);
        else
            top_wait(&idleseen);
    }
}

/* The program-exit drain: run the scheduler until no task is active. */
void __kml_task_run_all(void) {
    for (;;) {
        __kml_drain_microtasks();
        if (__kml_task_active <= 0) break;
        __kml_task_sched_step();
    }
    __kml_drain_microtasks();
}

/* Resume a parked task from a microtask drain: an await's continuation is a
 * microtask (TDD-00088). The resumer is a per-call slot, never the main
 * context: this can fire from a drain on a connection fiber. */
void __kml_task_resume(void *t) { task_swap_in(t); }

/* On a task: attach a resume reaction to the promise (or queue it now if
 * settled) and park in state 3, which only the resume microtask wakes. Off a
 * task it waits: through the loop at module top level, by yielding the
 * connection fiber on one (ADR-00986), or by a busy drive. */
void __kml_task_await_ready(void *promise) {
    __kml_promise_mark_handled(promise);
    void *ct = __kml_current_task;
    if (ct) {
        void **clo = (void **)malloc(KML_CLOSURE_SIZE);
        F(clo, KML_CLOSURE_FN, void *) = (void *)__kml_task_resume;
        F(clo, KML_CLOSURE_ENV, void *) = ct;
        if (P_STATE(promise) != 0) {
            __kml_microtask_enqueue(clo);
        } else {
            void *node = malloc(KML_REACTION_SIZE);
            F(node, KML_REACTION_CLOSURE, void *) = clo;
            F(node, KML_REACTION_NEXT, void *) = F(promise, KML_PROMISE_REACTIONS, void *);
            F(promise, KML_PROMISE_REACTIONS, void *) = node;
        }
        T_STATE(ct) = 3;
        T_JMPTOP(ct) = (i64)(unsigned)__kml_jmp_top;
        swapcontext(T_CTX(ct), T_RESUMER(ct));
        GC_RESTORE();
        return;
    }
    i64 cidx = __kml_current_conn_idx;
    if (cidx < 0) {
        /* Module top level on the main stack. */
        __kml_top_await(&P_STATE(promise));
        return;
    }
    /* On a connection fiber. One consuming its own request body must not
     * yield: the reactor would re-resume it on its own readable fd. */
    i64 fd = F((char *)__kml_conn_data + cidx * KML_CONN_SIZE, KML_CONN_FD, i64);
    if (!__kml_reqbody_active_on_fd(fd)) {
        while (P_STATE(promise) == 0) {
            /* What settles without the reactor's I/O settles here. */
            __kml_drain_microtasks();
            __kml_task_sched_step();
            __kml_timer_fire_next();
            if (P_STATE(promise) != 0) return;
            /* The connection array may have moved while suspended. */
            char *slot = (char *)__kml_conn_data + __kml_current_conn_idx * KML_CONN_SIZE;
            swapcontext(F(slot, KML_CONN_CTX, void *), __kml_main_ctx);
        }
        return;
    }
    while (P_STATE(promise) == 0) {
        __kml_drain_microtasks();
        __kml_task_sched_step();
        __kml_timer_fire_next();
        /* A pause per cycle: this can last a whole external transfer and
         * starves the peer without it. */
        usleep(200);
    }
}
