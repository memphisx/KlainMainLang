/* worker.c — the thread side of Worker threads (TDD-00098; in C per
 * TDD-00240): the thread main a Worker runs (klainpool.c's workerSpawn starts
 * it), the thread-aware exit that process.exit and an uncaught error take on
 * a worker, and the loop hooks that end a terminated worker. Under -mm=gc the
 * thread registers with Boehm before its first allocation, its thread-local
 * block a root, and its own stack bottom in the TLS orig slot so fiber-swap
 * restores never point at another thread's stack. kml_layout.h is prepended
 * by the compiler. */
#include <stdlib.h>

typedef long long i64;
typedef unsigned char u8;

#define F(p, off, T) (*(T *)((char *)(p) + (off)))

/* Declared as the emitted IR declares them; no system header, so the
 * prototypes cannot clash with libc's. */
#pragma clang diagnostic ignored "-Wincompatible-library-redeclaration"
extern int pthread_sigmask(int, const void *, void *);
extern int sigfillset(void *);
extern void pthread_exit(void *) __attribute__((noreturn));
extern int swapcontext(void *, void *);

#if defined(__APPLE__)
#define KML_SIG_BLOCK 1 /* glibc defines SIG_BLOCK as 0, Darwin as 1 */
#else
#define KML_SIG_BLOCK 0
#endif

/* klainpool.c's worker natives. */
extern void *__kml_worker_enter(void *ctx);
extern void __kml_worker_leave(i64 code);
extern _Bool __kml_worker_is_thread(void);
extern _Bool __kml_worker_terminating(void);

/* The generated program's side. */
extern i64 __kml_worker_run_loop(void);
extern _Thread_local void *__kml_current_task;

#ifdef KLAIN_GC
extern int GC_get_stack_base(void *);
extern int GC_register_my_thread(const void *);
extern int GC_unregister_my_thread(void);
extern void __kml_gc_tls_register(void);
extern void __kml_gc_tls_unregister(void);
extern _Thread_local void *__kml_gc_orig_stackbottom;
#define GC_UNREGISTER() (__kml_gc_tls_unregister(), GC_unregister_my_thread())
#else
#define GC_UNREGISTER() ((void)0)
#endif

static _Thread_local u8 worker_abort;

/* The thread a Worker runs. Signals are the main thread's; the worker's
 * modules evaluate (its entry), then its loop runs until nothing holds it
 * open, and the parent hears the exit code. */
void *__kml_worker_thread(void *ctx) {
#ifdef KLAIN_GC
    void *gcsb[2];
    GC_get_stack_base(gcsb);
    GC_register_my_thread(gcsb);
    __kml_gc_tls_register();
    __kml_gc_orig_stackbottom = gcsb[0];
#endif
    char sigset[128] __attribute__((aligned(8)));
    sigfillset(sigset);
    pthread_sigmask(KML_SIG_BLOCK, sigset, NULL);
    void (*entry)(void) = (void (*)(void))__kml_worker_enter(ctx);
    entry();
    i64 code = __kml_worker_run_loop();
    __kml_worker_leave(code);
    GC_UNREGISTER();
    return NULL;
}

/* The end of a worker thread from anywhere on its own stack (process.exit,
 * an uncaught error, terminate()). */
__attribute__((noreturn)) void __kml_worker_end_thread(void) {
    GC_UNREGISTER();
    pthread_exit(NULL);
}

/* process.exit's end on a worker: the thread ends with the code; elsewhere
 * the process does. On a coroutine stack (a worker module task's top level,
 * or any task) the thread may not end from there: winpthreads' pthread_exit
 * longjmps to the thread's start frame, and a longjmp off a fiber stack
 * fast-fails the process. It parks for good and whoever resumed the task,
 * always on the thread's own stack, ends the thread (abort_check). */
__attribute__((noreturn)) void __kml_thread_exit(int code) {
    if (!__kml_worker_is_thread()) exit(code);
    __kml_worker_leave((i64)code);
    void *ct = __kml_current_task;
    if (!ct) __kml_worker_end_thread();
    worker_abort = 1;
    swapcontext(F(ct, KML_TASK_CTX, void *), F(ct, KML_TASK_RESUMER, void *));
    __builtin_unreachable();
}

/* Runs after every task swap returns (spawn, the scheduler, the resume
 * runner) and ends the thread once control is back on its own stack. */
void __kml_worker_abort_check(void) {
    if (worker_abort && !__kml_current_task) __kml_worker_end_thread();
}

/* The loop's worker hook: a terminated worker ends at its next turn, with
 * exit code 1 (klainpool.c's leave records it). */
void __kml_worker_dispatch(void) {
    if (__kml_worker_terminating()) __kml_thread_exit(1);
}
_Bool __kml_worker_keepalive(void) { return 0; }
_Bool __kml_worker_fdset_add(void *fdset, void *maxfd) {
    (void)fdset;
    (void)maxfd;
    return 0;
}
