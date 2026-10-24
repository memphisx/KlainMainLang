/* loop.c — the event loop (TDD-00244 Stage 1, in C per TDD-00240).
 *
 * One pass: drain microtasks around a scheduler step; decide whether
 * anything keeps the loop alive; run libuv once, where every handle's
 * callback runs. Windows runs the same loop on libuv's Windows backend,
 * polling sockets by their SOCKET (TDD-00252).
 *
 * Under -mm=gc (KML_GC) the loop's own memory comes from libc. */

typedef long long i64;
typedef unsigned char u8;

#include <string.h>
#include <unistd.h>
#include <stdlib.h>
#include <sys/stat.h>
#include <uv.h>
#ifdef _WIN32
/* libuv polls a Windows socket by its SOCKET, not the shim's descriptor. */
intptr_t __kml_win_poll_socket(int fd);
#define kml_uv_poll_init(loop, h, fd) uv_poll_init_socket((loop), (h), (uv_os_sock_t)__kml_win_poll_socket(fd))
#else
#define kml_uv_poll_init uv_poll_init
#endif

/* Loop state, read and written by the loop-turn and await runtimes. */
_Thread_local u8 __kml_loop_oneshot;
_Thread_local u8 __kml_loop_nowait;
_Thread_local _Bool __kml_loop_idle;

/* Timers (timers.c). */
i64 __kml_monotonic_ns(void);

#ifdef KML_SYNC_LOCK
/* klain:sync's: a goroutine running the loop keeps its OS thread. */
void klainsync_lock_os_thread(void);
#endif

_Bool __kml_curl_loop_active(void);
int __kml_curl_inflight(void);
void __kml_httpc_fire_ready(void);

/* Tasks and microtasks (task.c, microtask.c, or the emitter's stubs). */
#ifdef _WIN32
/* Windows' signals are the shim's console handler (win32proc.c): it queues
 * the signal and wakes the loop, whose pass delivers it to process.c's
 * handler and runs the watchers. */
int __kml_win_sig_deliver(void);
#endif
void __kml_drain_microtasks(void);
void __kml_task_sched_step(void);
_Bool __kml_task_resumable(void);
_Bool __kml_microtasks_pending(void);

/* The thread's libuv loop, its wait's timer and its wake-up. */
typedef struct {
    uv_loop_t loop;
    uv_timer_t timer;
    uv_async_t wake;
} kml_uvstate;

static _Thread_local kml_uvstate *uvs;

#if defined(KML_GC) && defined(__linux__)
/* Under -mm=gc the shim's malloc is the collector's, which does not scan
 * libuv's memory or this file's thread-locals: the loop's own memory comes
 * from libc. */
extern void *__libc_malloc(size_t);
extern void *__libc_realloc(void *, size_t);
extern void *__libc_calloc(size_t, size_t);
extern void __libc_free(void *);
#define RAW_MALLOC __libc_malloc
#define RAW_REALLOC __libc_realloc
#define RAW_FREE __libc_free
static void raw_allocator(void) {
    static int done;
    if (!done) {
        done = 1;
        uv_replace_allocator(__libc_malloc, __libc_realloc, __libc_calloc, __libc_free);
    }
}
#else
#define RAW_MALLOC malloc
#define RAW_REALLOC realloc
#define RAW_FREE free
static void raw_allocator(void) {}
#endif

static void free_handle(uv_handle_t *h) { RAW_FREE(h); }

static void on_timer(uv_timer_t *t) { (void)t; }

/* Windows: a signal (the console's control handler, the resize watcher,
 * win32proc.c) wakes the loop through its async; the pass delivers it. */
static void on_wake(uv_async_t *a) { (void)a; }
#ifdef _WIN32
void __kml_win_set_loop_wake(void (*fn)(void *), void *arg);
static void win_wake(void *arg) { uv_async_send((uv_async_t *)arg); }
#endif

static kml_uvstate *uvs_get(void) {
    if (uvs)
        return uvs;
    raw_allocator();
    kml_uvstate *b = RAW_MALLOC(sizeof *b);
    memset(b, 0, sizeof *b);
    uv_loop_init(&b->loop);
    uv_timer_init(&b->loop, &b->timer);
    uv_async_init(&b->loop, &b->wake, on_wake);
#ifdef _WIN32
    __kml_win_set_loop_wake(win_wake, &b->wake);
#endif
    /* The loop's own timer and wake never hold it: the pass decides. */
    uv_unref((uv_handle_t *)&b->timer);
    uv_unref((uv_handle_t *)&b->wake);
    uvs = b;
    return b;
}

/* The thread's libuv loop and the allocator its handles live in (libc's
 * under -mm=gc on Linux: the collector does not scan libuv's lists), for
 * the runtime files that own handles of their own. */
uv_loop_t *__kml_loop_uv(void) { return &uvs_get()->loop; }
void *__kml_loop_raw_alloc(size_t n) { return RAW_MALLOC(n); }
void __kml_loop_raw_free(void *p) { RAW_FREE(p); }
void *__kml_loop_raw_realloc(void *p, size_t n) { return RAW_REALLOC(p, n); }
void __kml_loop_free_handle(uv_handle_t *h) { RAW_FREE(h); }


/* fs.watch: a uv_fs_event_t per watcher (fswatch.c). cb(data, filename,
 * is_rename) runs inside the wait; filename may be null. The returned
 * handle, or null with *err set to libuv's negative errno. A recursive
 * watch is libuv's: macOS and Windows. */
typedef void (*kml_fs_event_cb)(void *data, const char *filename, int is_rename);
typedef struct {
    uv_fs_event_t h;
    kml_fs_event_cb cb;
    void *data;
} kml_fs_event;

static void on_fs_event(uv_fs_event_t *h, const char *filename, int events, int status) {
    kml_fs_event *f = (kml_fs_event *)h;
    if (status < 0 || !(events & (UV_RENAME | UV_CHANGE)))
        return;
    f->cb(f->data, filename, (events & UV_RENAME) != 0);
}

void *__kml_loop_fs_event_start(const char *path, int recursive, kml_fs_event_cb cb, void *data, int *err) {
    kml_uvstate *b = uvs_get();
    kml_fs_event *f = RAW_MALLOC(sizeof *f);
    uv_fs_event_init(&b->loop, &f->h);
    f->cb = cb;
    f->data = data;
    int r = uv_fs_event_start(&f->h, on_fs_event, path, recursive ? UV_FS_EVENT_RECURSIVE : 0);
    if (r < 0) {
        *err = r;
        uv_close((uv_handle_t *)&f->h, free_handle);
        return 0;
    }
    return f;
}

/* ref()/unref() of a handle this file returned. */
void __kml_loop_handle_ref(void *h, int on) {
    if (on)
        uv_ref((uv_handle_t *)h);
    else
        uv_unref((uv_handle_t *)h);
}

void __kml_loop_fs_event_stop(void *h) {
    kml_fs_event *f = (kml_fs_event *)h;
    uv_fs_event_stop(&f->h);
    uv_close((uv_handle_t *)&f->h, free_handle);
}

/* process.on(signal): a uv_signal_t per signal number, unref'd as Node's
 * signal handles are. Its callback runs the watcher inside the wait. */
void __kml_signal_note(int signum);
static _Thread_local uv_signal_t *sig_handles[65];

static void on_signal(uv_signal_t *h, int signum) {
    (void)h;
    __kml_signal_note(signum);
}

int __kml_loop_signal_start(int n) {
    kml_uvstate *b = uvs_get();
    uv_signal_t *h = sig_handles[n];
    if (!h) {
        h = RAW_MALLOC(sizeof *h);
        uv_signal_init(&b->loop, h);
        uv_unref((uv_handle_t *)h);
        sig_handles[n] = h;
    }
    int r = uv_signal_start(h, on_signal, n);
#ifdef _WIN32
    return r == 0 ? 0 : -22; /* libuv refuses only a number it has no signal for (EINVAL) */
#else
    return r; /* -errno */
#endif
}

void __kml_loop_signal_stop(int n) {
    if (sig_handles[n])
        uv_signal_stop(sig_handles[n]);
}

/* A read watcher on a descriptor some runtime file reads itself (the fork
 * child's IPC channel): cb(data) runs inside the wait when fd is readable,
 * or has failed. Holds the loop only when refd. */
typedef struct {
    uv_poll_t h;
    void (*cb)(void *);
    void *data;
} kml_fdwatch;

static void on_fdwatch(uv_poll_t *h, int status, int events) {
    (void)status;
    (void)events;
    kml_fdwatch *w = (kml_fdwatch *)h;
    w->cb(w->data);
}

void *__kml_loop_fd_watch(int fd, void (*cb)(void *), void *data, int refd) {
    kml_uvstate *b = uvs_get();
    kml_fdwatch *w = RAW_MALLOC(sizeof *w);
    if (kml_uv_poll_init(&b->loop, &w->h, fd) < 0) {
        RAW_FREE(w);
        return 0;
    }
    w->cb = cb;
    w->data = data;
    if (!refd)
        uv_unref((uv_handle_t *)&w->h);
    uv_poll_start(&w->h, UV_READABLE, on_fdwatch);
    return w;
}

/* Before the descriptor closes or changes hands. */
void __kml_loop_fd_unwatch(void *w) {
    if (!w)
        return;
    uv_poll_stop(&((kml_fdwatch *)w)->h);
    uv_close((uv_handle_t *)w, free_handle);
}

/* An I/O watcher for a descriptor another library owns (libcurl's
 * sockets): cb(data, fd, events) runs inside the wait with 1 readable,
 * 2 writable, 4 error. Unreferenced: the owner decides what holds the loop. */
typedef struct {
    uv_poll_t h;
    int fd;
    void (*cb)(void *, int, int);
    void *data;
} kml_iowatch;

static void on_iowatch(uv_poll_t *h, int status, int events) {
    kml_iowatch *w = (kml_iowatch *)h;
    int ev = status < 0 ? 7 : ((events & UV_READABLE) ? 1 : 0) | ((events & UV_WRITABLE) ? 2 : 0);
    w->cb(w->data, w->fd, ev);
}

static int uv_events(int ev) { return ((ev & 1) ? UV_READABLE : 0) | ((ev & 2) ? UV_WRITABLE : 0); }

void *__kml_loop_io_watch(int fd, int events, void (*cb)(void *, int, int), void *data) {
    kml_uvstate *b = uvs_get();
    kml_iowatch *w = RAW_MALLOC(sizeof *w);
    if (kml_uv_poll_init(&b->loop, &w->h, fd) < 0) {
        RAW_FREE(w);
        return 0;
    }
    w->fd = fd;
    w->cb = cb;
    w->data = data;
    uv_unref((uv_handle_t *)&w->h);
    if (uv_events(events))
        uv_poll_start(&w->h, uv_events(events), on_iowatch);
    return w;
}

#ifdef _WIN32
/* The same watcher for a Winsock SOCKET a library owns (libcurl's, linked
 * against Winsock itself): fd is the value handed back to cb. */
void *__kml_loop_sock_watch(intptr_t sock, int fd, int events, void (*cb)(void *, int, int), void *data) {
    kml_uvstate *b = uvs_get();
    kml_iowatch *w = RAW_MALLOC(sizeof *w);
    if (uv_poll_init_socket(&b->loop, &w->h, (uv_os_sock_t)sock) < 0) {
        RAW_FREE(w);
        return 0;
    }
    w->fd = fd;
    w->cb = cb;
    w->data = data;
    uv_unref((uv_handle_t *)&w->h);
    if (uv_events(events))
        uv_poll_start(&w->h, uv_events(events), on_iowatch);
    return w;
}
#endif

void __kml_loop_io_events(void *p, int events) {
    kml_iowatch *w = (kml_iowatch *)p;
    if (!w)
        return;
    if (uv_events(events))
        uv_poll_start(&w->h, uv_events(events), on_iowatch);
    else
        uv_poll_stop(&w->h);
}

void __kml_loop_io_unwatch(void *p) {
    if (!p)
        return;
    uv_poll_stop(&((kml_iowatch *)p)->h);
    uv_close((uv_handle_t *)p, free_handle);
}

/* A one-shot timer for another library (libcurl's): cb(data) runs inside
 * the wait. Unreferenced, as the I/O watchers are. */
typedef struct {
    uv_timer_t h;
    void (*cb)(void *);
    void *data;
} kml_libtimer;

static void on_libtimer(uv_timer_t *h) {
    kml_libtimer *t = (kml_libtimer *)h;
    t->cb(t->data);
}

void *__kml_loop_timer_new(void (*cb)(void *), void *data) {
    kml_libtimer *t = RAW_MALLOC(sizeof *t);
    uv_timer_init(&uvs_get()->loop, &t->h);
    uv_unref((uv_handle_t *)&t->h);
    t->cb = cb;
    t->data = data;
    return t;
}

void __kml_loop_timer_start(void *t, i64 ms) {
    uv_update_time(&uvs_get()->loop);
    uv_timer_start(&((kml_libtimer *)t)->h, on_libtimer, (uint64_t)(ms < 0 ? 0 : ms), 0);
}

void __kml_loop_timer_stop(void *t) { uv_timer_stop(&((kml_libtimer *)t)->h); }

/* In a forked cluster worker: the inherited loop gets its own kernel event
 * queue (the primary's is shared across fork). */
void __kml_loop_after_fork(void) {
    if (uvs)
        uv_loop_fork(&uvs->loop);
}

/* Run libuv once, for at most wait ns (-1: until an event). */
static void wait_events(i64 wait) {
    kml_uvstate *b = uvs_get();
    /* The pass already decided the loop is alive: for this run the wake is
     * referenced, so uv_run polls (and blocks, when the wait allows) even
     * when only unreferenced watchers are waiting. */
    uv_ref((uv_handle_t *)&b->wake);
    if (wait == 0) {
        uv_run(&b->loop, UV_RUN_NOWAIT);
    } else {
        if (wait > 0) {
            /* libuv counts milliseconds: round up, never wake early */
            uint64_t ms = (uint64_t)((wait + 999999) / 1000000);
            uv_timer_start(&b->timer, on_timer, ms, 0);
        }
        uv_run(&b->loop, UV_RUN_ONCE);
        uv_timer_stop(&b->timer);
    }
    uv_unref((uv_handle_t *)&b->wake);
}

/* Work runnable without waiting: queued microtasks, a task whose wait is
 * over. */
static _Bool runnable_now(void) { return __kml_microtasks_pending() | __kml_task_resumable(); }

/* How deep this thread is in __kml_event_loop_run: a callback the loop runs
 * sees 1. */
static _Thread_local int loop_depth;

void __kml_event_loop_run(void) {
#ifdef KML_SYNC_LOCK
    klainsync_lock_os_thread();
#endif
    loop_depth++;
    /* A callback the loop runs has no caller of the program's own below it,
     * as in Node: the frames under the loop are set aside until it ends. */
    void *frames_below = __kml_frame_top;
    __kml_frame_top = 0;
    for (;;) {
        /* A single-pass run (an island's poll) takes exactly one pass. */
        if (__kml_loop_oneshot == 2)
            break;
        if (__kml_loop_oneshot == 1)
            __kml_loop_oneshot = 2;
#ifdef _WIN32
        __kml_win_sig_deliver();
#endif

        __kml_drain_microtasks();
        __kml_task_sched_step();
        __kml_drain_microtasks();

        /* The handles (timers, TCP, UDP, pipes, child processes, fs.watch,
         * signals, libcurl's transfers) hold the loop as libuv counts them. */
        _Bool keep = uv_loop_alive(&uvs_get()->loop) != 0;
        /* libcurl's step: a transfer finishing inside this call settles its
         * promise, whose continuation must still run, hence the two checks
         * after it. */
        __kml_curl_inflight();
        keep |= __kml_microtasks_pending();
        keep |= __kml_task_resumable();
        if (!keep) {
            __kml_loop_idle = 1;
            break;
        }

        _Bool has_curl = __kml_curl_loop_active();
        _Bool immediate = runnable_now() | (__kml_loop_nowait != 0);
        wait_events(immediate ? 0 : -1);
        if (has_curl)
            __kml_httpc_fire_ready();
    }
    __kml_frame_top = frames_below;
    loop_depth--;
}

/* klain:http's blocking listen (TDD-00255): the loop runs until nothing holds
 * it, then the caller goes on. From top-level code that is main()'s run of
 * the loop, or the module task parking while main() runs it (even resumed
 * from inside that run, after a top-level await); from a callback the loop
 * is already running and this returns at once. */
_Thread_local int (*__kml_module_loop_hook)(void); /* module.c's park, once a module task runs */
void __kml_native_run_loop(void) {
    if (__kml_module_loop_hook && __kml_module_loop_hook())
        return;
    if (loop_depth > 0)
        return;
    __kml_event_loop_run();
}

/* The loop inside a host's main loop (TDD-00244 Stage 7): the webview's GUI
 * thread owns the thread, so it watches libuv's backend descriptor and a
 * timer for the next deadline, and runs one non-blocking pass on either. */
_Bool __kml_loop_pass(void);
void __kml_loop_run_once(void) { __kml_loop_pass(); }

/* Whether libuv holds watchers it has not yet handed the kernel: it does
 * that at the start of its next poll, so until then their descriptors
 * cannot make the backend descriptor readable. */
static _Bool watchers_pending(void) {
#ifdef _WIN32
    return 0;
#else
    void **q = (void **)&uvs_get()->loop.watcher_queue;
    return q[0] != (void *)q;
#endif
}

/* One non-blocking pass (repeated while it left watchers unregistered or
 * work runnable, so that what remains can only be woken through the
 * backend descriptor or a timer); true when it found nothing left that can
 * wake the loop (an isolated island's poll, dynimportwatch.c). */
_Bool __kml_loop_pass(void) {
    u8 oneshot = __kml_loop_oneshot, nowait = __kml_loop_nowait;
    _Bool idle = 0;
    for (int i = 0; i < 64; i++) {
        __kml_loop_oneshot = 1;
        __kml_loop_nowait = 1;
        __kml_loop_idle = 0;
        __kml_event_loop_run();
        idle = __kml_loop_idle;
        if (idle || !(watchers_pending() || runnable_now()))
            break;
    }
    __kml_loop_oneshot = oneshot;
    __kml_loop_nowait = nowait;
    __kml_loop_idle = 0;
    return idle;
}

int __kml_loop_backend_fd(void) { return uv_backend_fd(&uvs_get()->loop); }
#ifdef _WIN32
/* libuv's backend on Windows: its completion port, no descriptor. */
void *__kml_loop_backend_port(void) { return uvs_get()->loop.iocp; }
#endif

/* The backend another loop watches this one by: the descriptor, or on
 * Windows the completion port. */
i64 __kml_loop_backend_handle(void) {
#ifdef _WIN32
    return (i64)(intptr_t)__kml_loop_backend_port();
#else
    return __kml_loop_backend_fd();
#endif
}

/* A referenced wake-up another thread posts with __kml_loop_async_send:
 * cb(data) runs inside the wait. Unreferenced, it no longer holds the loop
 * but still runs cb when sent. */
typedef struct {
    uv_async_t h;
    void (*cb)(void *);
    void *data;
} kml_async;

static void on_async(uv_async_t *h) {
    kml_async *a = (kml_async *)h;
    a->cb(a->data);
}

void *__kml_loop_async_new(void (*cb)(void *), void *data) {
    kml_async *a = RAW_MALLOC(sizeof *a);
    a->cb = cb;
    a->data = data;
    uv_async_init(&uvs_get()->loop, &a->h, on_async);
    return a;
}

void __kml_loop_async_send(void *a) { uv_async_send(&((kml_async *)a)->h); }

/* Closes a timer this file made (__kml_loop_timer_new). */
void __kml_loop_timer_free(void *t) {
    uv_timer_stop(&((kml_libtimer *)t)->h);
    uv_close((uv_handle_t *)t, free_handle);
}

/* Milliseconds until the next pass is due (-1: only when the backend
 * descriptor is readable), rounded up: a timer never fires early. */
i64 __kml_loop_next_ms(void) {
    if (runnable_now())
        return 0;
    uv_loop_t *l = &uvs_get()->loop;
    uv_update_time(l);
    return uv_backend_timeout(l);
}

/* process.title: the operating system's argv[0] until a program sets it;
 * setting it also renames the process where libuv can (uv_set_process_title
 * without uv_setup_args reaches only what the platform allows). */
extern void *__argv_ptr;
extern void *__kml_str_from_cstr(const char *s);
static char *kml_process_title;

void *__kml_native_process_title(void) {
    if (kml_process_title != NULL) return __kml_str_from_cstr(kml_process_title);
    return *(void **)__argv_ptr;
}

void __kml_native_process_set_title(void *title) {
    char *copy = strdup(title != NULL ? (const char *)title : ""); /* scratch: the runtime's own copy */
    if (copy == NULL) return;
    free(kml_process_title); /* scratch */
    kml_process_title = copy;
    (void)uv_set_process_title(copy);
}
