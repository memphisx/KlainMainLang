/* worker.c — the thread side of Worker threads (TDD-00098; in C per
 * TDD-00240): the thread main a Worker runs (klainpool.c's workerSpawn starts
 * it), and the thread-aware exit that process.exit, an uncaught error and
 * terminate() take on a worker. Under -mm=gc the
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
extern int sigdelset(void *, int);
extern void pthread_exit(void *) __attribute__((noreturn));
extern int swapcontext(void *, void *);

#if defined(__APPLE__)
#define KML_SIG_BLOCK 1 /* glibc defines SIG_BLOCK as 0, Darwin as 1 */
#define KML_SIGBUS 10
#else
#define KML_SIG_BLOCK 0
#define KML_SIGBUS 7
#endif
#define KML_SIGSEGV 11

__attribute__((noreturn)) void __kml_thread_exit(int code);

/* klainpool.c's worker natives. */
extern void *__kml_worker_enter(void *ctx);
extern void __kml_worker_leave(i64 code);
extern _Bool __kml_worker_is_thread(void);
extern void __kml_worker_set_end(void (*end)(int));

/* The generated program's side. */
extern i64 __kml_worker_run_loop(void);
extern _Thread_local void *__kml_current_task;

#ifdef KML_GC
extern void __kml_gc_tls_register(void);
extern void __kml_gc_tls_unregister(void);
#define GC_UNREGISTER() (__kml_gc_tls_unregister(), kml_gc_thread_unregister())
#else
#define GC_UNREGISTER() ((void)0)
#endif

static _Thread_local u8 worker_abort;
#ifdef _WIN32
/* __kml_worker_thread's start frame, where __kml_worker_end_thread returns. */
static _Thread_local void *worker_end_jb[5];
/* The shim's wake and completion forwarder for this thread's loop
 * (TDD-00252), released as the thread ends. */
void __kml_win_clear_loop_wake(void);
#define LOOP_RELEASE() __kml_win_clear_loop_wake()
#else
#define LOOP_RELEASE() ((void)0)
#endif
/* The stack the overflow handler runs on, kept per thread (stackguard.c). */
void __kml_stack_guard_thread(void);

/* The thread a Worker runs. Signals are the main thread's; the worker's
 * modules evaluate (its entry), then its loop runs until nothing holds it
 * open, and the parent hears the exit code. */
void *__kml_worker_thread(void *ctx) {
#ifdef KML_GC
    kml_gc_thread_register();
    __kml_gc_tls_register();
#endif
    char sigset[128] __attribute__((aligned(8)));
    sigfillset(sigset);
#ifndef _WIN32
    /* A fault is this thread's own: a stack overflow's is its RangeError. */
    sigdelset(sigset, KML_SIGSEGV);
    sigdelset(sigset, KML_SIGBUS);
#if defined(KML_GC) && !defined(__APPLE__)
    /* The collector stops a registered thread by signal (Mach calls on
     * Darwin): blocked, a collection while the worker runs never completes. */
    sigdelset(sigset, kml_gc_suspend_signal());
    sigdelset(sigset, kml_gc_restart_signal());
#endif
#endif
    pthread_sigmask(KML_SIG_BLOCK, sigset, NULL);
    __kml_worker_set_end(__kml_thread_exit);
    __kml_stack_guard_thread();
#ifdef _WIN32
    if (__builtin_setjmp(worker_end_jb)) {
        LOOP_RELEASE();
        GC_UNREGISTER();
        return NULL;
    }
#endif
    void (*entry)(void) = (void (*)(void))__kml_worker_enter(ctx);
    entry();
    i64 code = __kml_worker_run_loop();
    __kml_worker_leave(code);
    LOOP_RELEASE();
    GC_UNREGISTER();
    return NULL;
}

/* The end of a worker thread from anywhere on its own stack (process.exit,
 * an uncaught error, terminate()). On Windows winpthreads' pthread_exit
 * unwinds with RtlUnwindEx, which fails the process (0xC00000FF) at a frame
 * without unwind data; the thread instead jumps back to its start function,
 * restoring registers only, and returns from there. */
__attribute__((noreturn)) void __kml_worker_end_thread(void) {
#ifdef _WIN32
    __builtin_longjmp(worker_end_jb, 1);
#else
    GC_UNREGISTER();
    pthread_exit(NULL);
#endif
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
    KML_SWAPCONTEXT(F(ct, KML_TASK_CTX, void *), F(ct, KML_TASK_RESUMER, void *));
    __builtin_unreachable();
}

/* Runs after every task swap returns (spawn, the scheduler, the resume
 * runner) and ends the thread once control is back on its own stack. */
void __kml_worker_abort_check(void) {
    if (worker_abort && !__kml_current_task) __kml_worker_end_thread();
}
