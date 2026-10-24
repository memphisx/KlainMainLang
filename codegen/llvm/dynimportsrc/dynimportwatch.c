/* dynimportwatch.c — the importer-side bridge to a dynamic-import island's
 * module task (TDD-00225, in C per TDD-00240). An island with a top-level
 * await has its own libuv loop, which this program's loop watches as a
 * handle: a read watcher on the island loop's backend descriptor (on Windows,
 * a thread waiting on its completion port) and a timer at the island's
 * earliest deadline. Either runs one non-blocking pass of the island (its
 * poll export: state, with 0x100 set when it reported idle). The watcher holds
 * this loop while the island is pending and not idle. The import() promise is
 * a pending task promise settled from the island's module promise. */
#include <stdlib.h>
#if defined(_WIN32)
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#endif

typedef long long i64;
typedef i64 (*kml_poll_fn)(void);
typedef void *(*kml_err_fn)(void);
typedef void (*kml_settle_fn)(void *handle, void *prom);

/* Defined by the compiler and the loop (loop.c). */
extern void __kml_promise_reject_err(void *p, i64 err);
extern i64 __kml_monotonic_ns(void);
extern void *__kml_loop_timer_new(void (*cb)(void *), void *data);
extern void __kml_loop_timer_start(void *t, i64 ms);
extern void __kml_loop_timer_stop(void *t);
extern void __kml_loop_timer_free(void *t);
extern void __kml_loop_handle_ref(void *h, int on);
#if defined(_WIN32)
extern void *__kml_loop_async_new(void (*cb)(void *), void *data);
extern void __kml_loop_async_send(void *a);
#else
extern void *__kml_loop_fd_watch(int fd, void (*cb)(void *), void *data, int refd);
extern void __kml_loop_fd_unwatch(void *w);
#endif

typedef struct {
    void *handle, *prom;
    kml_settle_fn settle;
    kml_poll_fn poll;
    kml_err_fn err;
    kml_poll_fn deadline;
    void *timer; /* the island's earliest deadline; a zero-delay first pass */
    void *wake;  /* the backend watcher (POSIX) or the port thread's async (Windows) */
    _Bool done, held;
#if defined(_WIN32)
    HANDLE port, resume;
#endif
} Watch;

static void settle_from(void *handle, void *prom, kml_settle_fn settle, kml_err_fn err, i64 st) {
    if (st == 1) settle(handle, prom);
    else __kml_promise_reject_err(prom, (i64)(size_t)err());
}

static void pass(void *p);

/* After a pass: hold this loop while the island can still wake, with a timer
 * at its deadline; release everything once its promise settled. */
static void rearm(Watch *w, i64 st, _Bool idle) {
    if (st != 0) {
        w->done = 1;
        __kml_loop_timer_free(w->timer);
        __kml_loop_handle_ref(w->wake, 0);
#if !defined(_WIN32)
        __kml_loop_fd_unwatch(w->wake);
#endif
        settle_from(w->handle, w->prom, w->settle, w->err, st);
        return;
    }
    if (idle != !w->held) {
        w->held = !idle;
        __kml_loop_handle_ref(w->wake, w->held);
    }
    i64 dl = w->deadline();
    if (dl == 0) {
        __kml_loop_timer_stop(w->timer);
        return;
    }
    i64 ms = (dl - __kml_monotonic_ns() + 999999) / 1000000;
    __kml_loop_timer_start(w->timer, ms);
}

static void pass(void *p) {
    Watch *w = (Watch *)p;
    if (w->done) return;
    i64 r = w->poll();
    rearm(w, r & 255, (r >> 8) != 0);
#if defined(_WIN32)
    if (!w->done) SetEvent(w->resume);
#endif
}

#if defined(_WIN32)
/* The island loop's completion port has no descriptor: a thread waits on it,
 * puts back what it took and wakes this loop, which runs a pass and lets the
 * thread wait again (the webview embedding does the same, loop_embed.cc). */
static DWORD WINAPI port_wait(LPVOID p) {
    Watch *w = (Watch *)p;
    for (;;) {
        WaitForSingleObject(w->resume, INFINITE);
        if (w->done) return 0;
        DWORD bytes = 0;
        ULONG_PTR key = 0;
        OVERLAPPED *ov = NULL;
        BOOL got = GetQueuedCompletionStatus(w->port, &bytes, &key, &ov, INFINITE);
        if (got || ov) PostQueuedCompletionStatus(w->port, bytes, key, ov);
        __kml_loop_async_send(w->wake);
    }
}
#endif

/* Settles the import() promise at once when the island's module promise is
 * already settled (no top-level await, or one that settled during init),
 * else watches the island's loop. */
void __kml_dynimport_watch(void *handle, void *prom, kml_settle_fn settle, kml_poll_fn state,
                           kml_poll_fn poll, kml_err_fn err, kml_poll_fn deadline, kml_poll_fn backend) {
    i64 st = state();
    if (st != 0) { settle_from(handle, prom, settle, err, st); return; }
    Watch *w = (Watch *)calloc(1, sizeof *w); /* scratch: one per pending island, kept: a handle may still fire */
    *w = (Watch){handle, prom, settle, poll, err, deadline, 0, 0, 0, 1};
    w->timer = __kml_loop_timer_new(pass, w);
#if defined(_WIN32)
    w->port = (HANDLE)(intptr_t)backend();
    w->resume = CreateEventW(NULL, FALSE, FALSE, NULL);
    w->wake = __kml_loop_async_new(pass, w);
    HANDLE t = CreateThread(NULL, 64 * 1024, port_wait, w, 0, NULL);
    if (t) CloseHandle(t);
#else
    w->wake = __kml_loop_fd_watch((int)backend(), pass, w, 1);
#endif
    /* The island ran to its first await: its first pass now. */
    __kml_loop_timer_start(w->timer, 0);
}
