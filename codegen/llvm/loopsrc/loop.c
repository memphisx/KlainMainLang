/* loop.c — the event loop (TDD-00244 Stage 1, in C per TDD-00240).
 *
 * One pass: drain microtasks around a scheduler step; decide whether
 * anything keeps the loop alive; fold the extra deadlines into the wait;
 * run libuv once, where every handle's callback runs and the HTTP
 * listeners' and connection fibers' watchers record readiness; accept on
 * the ready listeners; resume the ready connection fibers. Windows runs
 * the same loop on libuv's Windows backend, polling sockets by their
 * SOCKET (TDD-00252).
 *
 * Compile-time options (cflags from the emitter):
 *   KML_GC_SB=0|1|2|3  how a resumed fiber's GC stack bottom is set: none,
 *                      GC_stackbottom, __kml_gc_set_sb, __kml_gc_sb_cur
 *   KML_FIBER_STACK    a connection fiber's stack size
 *   KML_CLUSTER_ORPHAN a cluster worker exits when its primary is gone */

typedef long long i64;
typedef unsigned char u8;

#ifndef KML_GC_SB
#define KML_GC_SB 0
#endif
#ifndef KML_FIBER_STACK
#define KML_FIBER_STACK (1024 * 1024)
#endif

/* The readiness bitmaps' starting size: they grow to the highest watched
 * descriptor. */
#define FDSET_BYTES 128
#include <string.h>
#include <unistd.h>
#include <stdlib.h>
#include <sys/stat.h>
#include <uv.h>
int swapcontext(void *, void *);
/* ucontext_t is opaque here: its size and field offsets differ per platform
 * (and Windows uses win32io.c's own), so the emitter passes them. As in
 * task.c, the opaque prototype differs from libc's builtin one on purpose. */
#pragma clang diagnostic ignored "-Wincompatible-library-redeclaration"
int getcontext(void *);
void makecontext(void *, void (*)(void), int, ...);
#ifndef KML_UC_SIZE
#define KML_UC_SIZE 968
#define KML_UC_SP 16
#define KML_UC_SSIZE 32
#define KML_UC_LINK 8
#endif
#ifdef _WIN32
/* The shim's descriptor-level socket calls (win32io.c), under their POSIX
 * names; <uv.h> brought Winsock's SOCKET-typed ones (TDD-00252). */
int kml_accept(int, void *, void *) __asm__("accept");
int kml_setsockopt(int, int, int, const void *, int) __asm__("setsockopt");
int fcntl(int, int, ...);
#define F_GETFL 3
#define F_SETFL 4
#define O_NONBLOCK 0x800
/* libuv polls a Windows socket by its SOCKET, not the shim's descriptor. */
intptr_t __kml_win_poll_socket(int fd);
#define kml_uv_poll_init(loop, h, fd) uv_poll_init_socket((loop), (h), (uv_os_sock_t)__kml_win_poll_socket(fd))
#else
#include <fcntl.h>
#include <sys/socket.h>
#define kml_accept accept
#define kml_setsockopt setsockopt
#define kml_uv_poll_init uv_poll_init
#endif
#ifdef KML_CLUSTER_ORPHAN
void exit(int);
#endif

/* Loop state, read and written by the loop-turn and await runtimes. */
_Thread_local u8 __kml_loop_oneshot;
_Thread_local u8 __kml_loop_nowait;
_Thread_local _Bool __kml_loop_idle;

/* Timers (timers.c). */
i64 __kml_monotonic_ns(void);

/* The primary HTTP server and its connection fibers (runtime_http.go). A
 * slot is { fd, ucontext, stack, pendingFetch, pendingGroup }. */
typedef struct { i64 fd; void *ctx; void *stack; void *fetch; void *group; } kml_conn;
extern _Thread_local int __kml_listen_fd;
extern _Thread_local kml_conn *__kml_conn_data;
extern _Thread_local i64 __kml_conn_len;
extern _Thread_local i64 __kml_conn_cap;
extern _Thread_local i64 __kml_conn_active;
extern _Thread_local void (*__kml_listen_dispatch)(void);
extern _Thread_local i64 __kml_current_conn_idx;
extern _Thread_local u8 __kml_conn_poke;
extern _Thread_local u8 __kml_conn_ran;
extern _Thread_local u8 __kml_main_ctx[];
extern i64 *__kml_cluster_close_flag;
extern int __kml_cluster_primary_pid;
void __kml_http_accept_primary(int listenfd);
void __kml_http_shutdown_local_conns(void);
void __kml_http_fire_close(void);
void __kml_reactor_thread_lock(void);

#if KML_GC_SB != 0
extern _Thread_local void *__kml_gc_orig_stackbottom;
#endif
#if KML_GC_SB == 1
extern void *GC_stackbottom;
#define GC_SB_SET(p) (GC_stackbottom = (p))
#elif KML_GC_SB == 2
void __kml_gc_set_sb(void *);
#define GC_SB_SET(p) __kml_gc_set_sb(p)
#elif KML_GC_SB == 3
void __kml_gc_sb_cur(void);
#define GC_SB_SET(p) ((void)(p), __kml_gc_sb_cur())
#endif

/* Fetch: a pending fetch is { ..., done@16, signal@40, headersDone@48 }; a
 * signal's AbortSignal.timeout deadline is at 32. libcurl (fetch.c, or the
 * no-op stubs in nocurl.c). */
_Bool __kml_signal_aborted(void *sig);
_Bool __kml_group_satisfied(void *group);

/* A connection fiber's park condition: a promise it awaits (tagged by bit
 * 0, task.c) has settled, or its fetch group is satisfied. */
static _Bool conn_park_ready(void *group) {
    unsigned long long g = (unsigned long long)group;
    if (g & 1)
        return *(i64 *)((char *)(g & ~1ULL) + KML_PROMISE_STATE) != 0;
    return __kml_group_satisfied(group);
}
_Bool __kml_curl_loop_active(void);
int __kml_curl_inflight(void);
void __kml_httpc_fire_ready(void);

/* Tasks and microtasks (task.c, microtask.c, or the emitter's stubs). */
#ifdef _WIN32
/* Windows' signals are the shim's console handler (win32proc.c): it queues
 * the signal and wakes the loop, whose pass delivers it to process.c's
 * handler and runs the watchers. */
int __kml_win_sig_deliver(void);
void __kml_signal_dispatch(void);
#endif
void __kml_drain_microtasks(void);
void __kml_task_sched_step(void);
void __kml_reqbody_pump(void);
_Bool __kml_reqbody_want(void);
_Bool __kml_task_holds_loop(void);
_Bool __kml_task_resumable(void);
_Bool __kml_microtasks_pending(void);

/* Subsystem hooks: real, or the emitter's no-op stubs. */
_Bool __kml_http_xl_keepalive(void);
/* The additional listeners (httpsrv.c). */
typedef struct { i64 fd; void (*dispatch)(void); void *tlsctx, *h2vtbl; } kml_xl;
extern _Thread_local kml_xl *__kml_http_xl_data;
extern _Thread_local i64 __kml_http_xl_len;
/* The dynamic-import islands' hooks (hooks.c; dynimportwatch.c registers them). */
extern _Bool (*__kml_hook_dynimport_keepalive)(void);
extern void (*__kml_hook_dynimport_dispatch)(void);
extern i64 (*__kml_hook_dynimport_next_deadline_ns)(void);

#define BIT_SET(set, fd) ((set)[(fd) / 8] |= (u8)(1u << ((fd) % 8)))
#define BIT_ISSET(set, fd) (((set)[(fd) / 8] >> ((fd) % 8)) & 1)

/* The connection fds whose fiber is parked on a write (EAGAIN), any
 * descriptor number: the pass waits for them to be writable. */
static _Thread_local u8 *writepark;
static _Thread_local i64 writepark_cap;

void __kml_conn_park_write(int fd, int on) {
    if (fd < 0)
        return;
    if (fd >= writepark_cap * 8) {
        if (!on)
            return;
        i64 nc = writepark_cap ? writepark_cap : 128;
        while (nc * 8 <= fd)
            nc *= 2;
        u8 *n = realloc(writepark, (size_t)nc);
        memset(n + writepark_cap, 0, (size_t)(nc - writepark_cap));
        writepark = n;
        writepark_cap = nc;
    }
    if (on)
        BIT_SET(writepark, fd);
    else
        writepark[fd / 8] &= (u8) ~(1u << (fd % 8));
}

static void fold(i64 *slot, i64 deadline) {
    if (deadline != 0 && (*slot == 0 || deadline < *slot))
        *slot = deadline;
}

/* Cluster workers: a sibling's http.close() / closeAllConnections() sets a
 * word in the shared flag page; an idle worker polls it every pass. */
static void cluster_poll(void) {
    i64 *flag = __kml_cluster_close_flag;
    if (!flag)
        return;
#ifdef KML_CLUSTER_ORPHAN
    if (__kml_cluster_primary_pid != 0 && getppid() != __kml_cluster_primary_pid)
        exit(0);
#endif
    if (__atomic_load_n(&flag[1], __ATOMIC_SEQ_CST) != 0)
        __kml_http_shutdown_local_conns();
    if (__atomic_load_n(&flag[0], __ATOMIC_SEQ_CST) != 0 && __kml_listen_fd >= 0) {
        close(__kml_listen_fd);
        __kml_listen_fd = -1;
    }
}

/* Start a connection fiber for a freshly accepted fd: append a slot (the
 * table doubles, minimum 8), build a ucontext on its own stack that returns
 * to the loop through uc_link, and run it once. The entry point is the
 * owning server's dispatcher. */
void __kml_http_append_conn(int fd, void (*dispatch)(void)) {
    if (__kml_conn_len + 1 > __kml_conn_cap) {
        i64 cap = __kml_conn_cap * 2;
        if (cap < 8)
            cap = 8;
        __kml_conn_data = realloc(__kml_conn_data, (__SIZE_TYPE__)cap * sizeof(kml_conn));
        __kml_conn_cap = cap;
    }
    i64 idx = __kml_conn_len;
    kml_conn *c = &__kml_conn_data[idx];
    u8 *ctx = malloc(KML_UC_SIZE);
    u8 *stack = malloc(KML_FIBER_STACK);
    getcontext(ctx);
    *(void **)(ctx + KML_UC_SP) = stack;
    *(i64 *)(ctx + KML_UC_SSIZE) = KML_FIBER_STACK;
    *(void **)(ctx + KML_UC_LINK) = __kml_main_ctx;
    makecontext(ctx, dispatch, 0);
    c->fd = fd;
    c->ctx = ctx;
    c->stack = stack;
    c->fetch = 0;
    c->group = 0;
    __kml_conn_len = idx + 1;
    __kml_conn_active++;
    __kml_current_conn_idx = idx;
#if KML_GC_SB != 0
    GC_SB_SET(stack + KML_FIBER_STACK);
#endif
    swapcontext(__kml_main_ctx, ctx);
#if KML_GC_SB != 0
    GC_SB_SET(__kml_gc_orig_stackbottom);
#endif
    __kml_current_conn_idx = -1;
    /* The fiber may have written a response an in-process client must now
     * read: the next wait must not block. */
    __kml_conn_ran = 1;
}

/* Node's http.Server sets TCP_NODELAY on every accepted socket (noDelay
 * defaults to true); the connection is non-blocking for its fiber. */
static void accept_into(int listenfd, void (*dispatch)(void)) {
    int fd = kml_accept(listenfd, 0, 0);
    if (fd < 0)
        return;
    int one = 1;
    kml_setsockopt(fd, 6 /* IPPROTO_TCP */, 1 /* TCP_NODELAY */, &one, sizeof one);
    fcntl(fd, F_SETFL, fcntl(fd, F_GETFL) | O_NONBLOCK);
    __kml_http_append_conn(fd, dispatch);
}

void __kml_http_accept_primary(int listenfd) {
    accept_into(listenfd, __kml_listen_dispatch);
}

/* Accept on each ready additional listener into its own dispatcher. */
void __kml_http_xl_accept(void *fdset) {
    u8 *set = fdset;
    for (i64 i = 0; i < __kml_http_xl_len; i++) {
        i64 fd = __kml_http_xl_data[i].fd;
        if (fd >= 0 && BIT_ISSET(set, fd))
            accept_into((int)fd, __kml_http_xl_data[i].dispatch);
    }
}

/* Resume connection fibers: one parked on a fetch when it is done, aborted
 * or has headers; one parked on a promise group when the group is
 * satisfied; any other when its fd is ready or a task completion poked. The
 * poke is snapshotted and cleared before the scan, so a poke set by a fiber
 * resumed mid-scan survives for the next pass's no-block decision. */
static void scan_conns(u8 *rset, u8 *wset) {
    u8 poked = __kml_conn_poke;
    __kml_conn_poke = 0;
    for (i64 i = 0; i < __kml_conn_len; i++) {
        kml_conn *c = &__kml_conn_data[i];
        if (c->fd < 0)
            continue;
        int ready;
        if (c->fetch) {
            u8 *pf = (u8 *)c->fetch;
            ready = *(i64 *)(pf + 16) != 0 || __kml_signal_aborted(*(void **)(pf + 40)) ||
                    *(i64 *)(pf + 48) != 0;
        } else if (c->group) {
            ready = conn_park_ready(c->group);
        } else {
            ready = BIT_ISSET(rset, c->fd) || BIT_ISSET(wset, c->fd) || poked;
        }
        if (!ready)
            continue;
        __kml_current_conn_idx = i;
#if KML_GC_SB != 0
        GC_SB_SET((u8 *)c->stack + KML_FIBER_STACK);
#endif
        swapcontext(__kml_main_ctx, c->ctx);
#if KML_GC_SB != 0
        GC_SB_SET(__kml_gc_orig_stackbottom);
#endif
        /* Back on the loop's stack: no connection fiber is running. */
        __kml_current_conn_idx = -1;
        __kml_conn_ran = 1;
    }
}


/* The thread's libuv loop. The HTTP listeners and connection fibers each
 * own a uv_poll_t (sync_sources); their callbacks record readiness in
 * bitmaps the pass then dispatches from, as the fibers resume outside
 * libuv's callbacks. */
typedef struct {
    uv_poll_t *h;
    int fd, events;
} kml_srcwatch;

typedef struct {
    uv_loop_t loop;
    uv_timer_t timer;
    uv_async_t wake;
    u8 *rr, *rw;
    i64 rcap; /* bytes in rr and rw */
} kml_uvstate;

static _Thread_local kml_uvstate *uvs;

#if KML_GC_SB != 0 && defined(__linux__)
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

static void on_poll(uv_poll_t *h, int status, int events) {
    int fd = (int)(intptr_t)h->data;
    if (status < 0)
        events = UV_READABLE | UV_WRITABLE;
    if (events & UV_READABLE)
        BIT_SET(uvs->rr, fd);
    if (events & UV_WRITABLE)
        BIT_SET(uvs->rw, fd);
}

static void ready_bits_fit(kml_uvstate *b, int fd) {
    i64 need = fd / 8 + 1;
    if (need <= b->rcap)
        return;
    i64 ncap = b->rcap * 2;
    while (ncap < need)
        ncap *= 2;
    b->rr = RAW_REALLOC(b->rr, ncap);
    b->rw = RAW_REALLOC(b->rw, ncap);
    memset(b->rr + b->rcap, 0, ncap - b->rcap);
    memset(b->rw + b->rcap, 0, ncap - b->rcap);
    b->rcap = ncap;
}

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
    b->rcap = FDSET_BYTES;
    b->rr = RAW_MALLOC(b->rcap);
    b->rw = RAW_MALLOC(b->rcap);
    memset(b->rr, 0, b->rcap);
    memset(b->rw, 0, b->rcap);
    uvs = b;
    return b;
}

/* The thread's libuv loop and the allocator its handles live in (libc's
 * under -mm=gc on Linux: the collector does not scan libuv's lists), for
 * the runtime files that own handles of their own. */
uv_loop_t *__kml_loop_uv(void) { return &uvs_get()->loop; }
void *__kml_loop_raw_alloc(size_t n) { return RAW_MALLOC(n); }
void __kml_loop_raw_free(void *p) { RAW_FREE(p); }
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
    return uv_signal_start(h, on_signal, n);
}

void __kml_loop_signal_stop(int n) {
    if (sig_handles[n])
        uv_signal_stop(sig_handles[n]);
}

static void src_drop(kml_srcwatch *w) {
    if (w->h) {
        uv_poll_stop(w->h);
        uv_close((uv_handle_t *)w->h, free_handle);
        w->h = 0;
    }
    w->fd = -1;
    w->events = 0;
}

/* A read watcher on a descriptor some runtime file reads itself (the fork
 * child's IPC channel): cb(data) runs inside the wait when fd is readable,
 * or has failed. Holds the loop only when refd. */
typedef struct {
    uv_poll_t h;
    void (*cb)(void *);
    void *data;
} kml_fdwatch;

void __kml_loop_fd_forget(int fd);

static void on_fdwatch(uv_poll_t *h, int status, int events) {
    (void)status;
    (void)events;
    kml_fdwatch *w = (kml_fdwatch *)h;
    w->cb(w->data);
}

void *__kml_loop_fd_watch(int fd, void (*cb)(void *), void *data, int refd) {
    kml_uvstate *b = uvs_get();
    __kml_loop_fd_forget(fd);
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
    __kml_loop_fd_forget(fd);
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

/* The connection, primary-listener and additional-listener watchers. */
static _Thread_local kml_srcwatch *conn_w, *xl_w, prim_w = {0, -1, 0};
static _Thread_local i64 conn_w_cap, xl_w_cap;

static void src_grow(kml_srcwatch **arr, i64 *cap, i64 need) {
    if (need <= *cap)
        return;
    i64 ncap = *cap ? *cap * 2 : 16;
    while (ncap < need)
        ncap *= 2;
    *arr = RAW_REALLOC(*arr, sizeof(kml_srcwatch) * ncap);
    for (i64 i = *cap; i < ncap; i++)
        (*arr)[i] = (kml_srcwatch){0, -1, 0};
    *cap = ncap;
}

/* Watch fd for events under w, or stop watching (fd < 0). A descriptor
 * libuv cannot poll is reported ready, as select() would. */
static void src_want(kml_uvstate *b, kml_srcwatch *w, int fd, int events) {
    if (w->h && w->fd != fd)
        src_drop(w);
    if (fd < 0)
        return;
    ready_bits_fit(b, fd);
    if (!w->h) {
        uv_poll_t *h = RAW_MALLOC(sizeof *h);
        if (kml_uv_poll_init(&b->loop, h, fd) != 0) {
            RAW_FREE(h);
            BIT_SET(b->rr, fd);
            return;
        }
        h->data = (void *)(intptr_t)fd;
        /* the pass decides whether the server holds the loop */
        uv_unref((uv_handle_t *)h);
        w->h = h;
        w->fd = fd;
    }
    if (w->events != events) {
        if (uv_poll_start(w->h, events, on_poll) != 0) {
            src_drop(w);
            BIT_SET(b->rr, fd);
            return;
        }
        w->events = events;
    }
}

/* Bring each source's watcher in line with its descriptor. Watchers whose
 * owner let go are dropped first, so a descriptor number reused by a new
 * connection never meets its old watcher. */
static void sync_sources(kml_uvstate *b, int listenfd) {
    src_grow(&conn_w, &conn_w_cap, __kml_conn_len);
    src_grow(&xl_w, &xl_w_cap, __kml_http_xl_len);
    for (i64 i = 0; i < __kml_conn_len; i++)
        if (conn_w[i].h && conn_w[i].fd != __kml_conn_data[i].fd)
            src_drop(&conn_w[i]);
    for (i64 i = 0; i < __kml_http_xl_len; i++)
        if (xl_w[i].h && xl_w[i].fd != __kml_http_xl_data[i].fd)
            src_drop(&xl_w[i]);
    if (prim_w.h && prim_w.fd != listenfd)
        src_drop(&prim_w);
    for (i64 i = 0; i < __kml_conn_len; i++) {
        i64 fd = __kml_conn_data[i].fd;
        int ev = UV_READABLE;
        if (fd >= 0 && fd < writepark_cap * 8 && BIT_ISSET(writepark, fd))
            ev |= UV_WRITABLE;
        src_want(b, &conn_w[i], (int)fd, ev);
    }
    for (i64 i = 0; i < __kml_http_xl_len; i++)
        src_want(b, &xl_w[i], (int)__kml_http_xl_data[i].fd, UV_READABLE);
    src_want(b, &prim_w, listenfd, UV_READABLE);
}

/* A handle of its own now watches fd: the sources let go of theirs
 * (libuv allows one per descriptor). */
void __kml_loop_fd_forget(int fd) {
    if (fd < 0)
        return;
    for (i64 i = 0; i < conn_w_cap; i++)
        if (conn_w[i].fd == fd)
            src_drop(&conn_w[i]);
    for (i64 i = 0; i < xl_w_cap; i++)
        if (xl_w[i].fd == fd)
            src_drop(&xl_w[i]);
    if (prim_w.fd == fd)
        src_drop(&prim_w);
}

/* Run libuv once, for at most wait ns (-1: until an event), then hand the
 * sources' readiness back as bitmaps. */
static int wait_ready(int listenfd, u8 **r, u8 **w, i64 wait) {
    kml_uvstate *b = uvs_get();
    sync_sources(b, listenfd);
    memset(b->rr, 0, b->rcap);
    memset(b->rw, 0, b->rcap);
    /* The pass already decided the loop is alive, and the sources' own
     * watchers are unreferenced: for this run the wake is referenced, so
     * uv_run polls (and blocks, when the wait allows) even when only they
     * are waiting. */
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
    *r = b->rr;
    *w = b->rw;
    return 0;
}

/* Work runnable without waiting: queued microtasks, a request body
 * wanting a pump, a task whose wait is over, an unserviced poke. */
static _Bool runnable_now(void) {
    return __kml_microtasks_pending() | __kml_reqbody_want() | __kml_task_resumable() |
           (__kml_conn_poke != 0);
}

/* The pass's wait in ns (-1: until an event): the soonest of the JS timer
 * (Windows), a fetch's AbortSignal deadline, libcurl's (Windows) and a
 * dynamic import's, 0 when immediate. The extra deadlines bound the wait
 * only: folded into bestfire they would fire JS timers early. */
static i64 pass_wait(_Bool immediate, _Bool have_timer, i64 bestfire, _Bool has_curl) {
    (void)has_curl;
    i64 extra = 0;
    _Bool fetch_deadline = 0;
    for (i64 i = 0; i < __kml_conn_len; i++) {
        u8 *pf = (u8 *)__kml_conn_data[i].fetch;
        if (!pf)
            continue;
        u8 *sig = *(u8 **)(pf + 40);
        if (!sig)
            continue;
        i64 dl = *(i64 *)(sig + 32);
        if (dl != 0) {
            fetch_deadline = 1;
            fold(&extra, dl);
        }
    }
    fold(&extra, __kml_hook_dynimport_next_deadline_ns());
    _Bool cluster = __kml_cluster_close_flag != 0;
    _Bool bounded = have_timer | immediate | fetch_deadline | (extra != 0) | cluster;
    if (!bounded)
        return -1;
    const i64 inf = 9223372036854775807LL;
    i64 now = __kml_monotonic_ns();
    i64 wait = inf;
    if (have_timer)
        wait = bestfire - now < 0 ? 0 : bestfire - now;
    if (extra != 0) {
        i64 w = extra - now < 0 ? 0 : extra - now;
        if (w < wait)
            wait = w;
    }
    if (cluster && wait > 200000000)
        wait = 200000000;
    if (immediate)
        wait = 0;
    return wait == inf ? -1 : wait;
}

void __kml_event_loop_run(void) {
    __kml_reactor_thread_lock();
    u8 *rset = 0, *wset = 0;
    for (;;) {
        /* A single-pass run (an island's poll) takes exactly one pass. */
        if (__kml_loop_oneshot == 2)
            break;
        if (__kml_loop_oneshot == 1)
            __kml_loop_oneshot = 2;
#ifdef _WIN32
        __kml_win_sig_deliver();
        __kml_signal_dispatch();
#endif

        __kml_drain_microtasks();
        __kml_task_sched_step();
        __kml_drain_microtasks();
        __kml_reqbody_pump();
        _Bool tasks_hold = __kml_task_holds_loop();

        i64 bestfire = 0;
        /* JS timers are libuv's: they fire inside the wait. */
        _Bool have_timer = 0;

        cluster_poll();
        int listenfd = __kml_listen_fd;
        _Bool has_listener = listenfd >= 0;
        _Bool has_conns = __kml_conn_active > 0;
        __kml_http_fire_close();

        /* Evaluate every keep-alive hook: several also do work. */
        _Bool keep = has_listener | has_conns;
        /* The handles on their own libuv watchers (timers, TCP, UDP, pipes,
         * child processes, fs.watch) hold the loop as libuv counts them. */
        keep |= uv_loop_alive(&uvs_get()->loop) != 0;
        keep |= __kml_http_xl_keepalive();
        keep |= __kml_hook_dynimport_keepalive();
        keep |= tasks_hold;
        /* An in-flight transfer is an open socket. Asked of libcurl now: a
         * transfer finishing inside this call settles its promise, whose
         * continuation must still run, hence the two checks after it. */
        keep |= __kml_curl_inflight() > 0;
        keep |= __kml_microtasks_pending();
        keep |= __kml_task_resumable();
        if (!keep) {
            __kml_loop_idle = 1;
            break;
        }

        _Bool forcezero = 0;
        _Bool has_curl = __kml_curl_loop_active();

        /* Never block with work already runnable: a fiber that ran (an
         * in-process client must read what it wrote) among the rest. */
        _Bool ran = __kml_conn_ran != 0;
        __kml_conn_ran = 0;
        _Bool immediate = forcezero | ran | runnable_now() | (__kml_loop_nowait != 0);
        i64 wait = pass_wait(immediate, have_timer, bestfire, has_curl);
        u8 *rp = rset, *wp = wset;
        int rc = wait_ready(listenfd, &rp, &wp, wait);
        /* On failure (EINTR) the sets hold the watch set, not readiness. */
        if (rc < 0)
            continue;

        __kml_hook_dynimport_dispatch();
        __kml_http_xl_accept(rp);
        if (has_curl) {
            __kml_httpc_fire_ready();
        }
        if (has_listener && BIT_ISSET(rp, listenfd))
            __kml_http_accept_primary(listenfd);
        scan_conns(rp, wp);

    }
}

/* The loop inside a host's main loop (TDD-00244 Stage 7): the webview's GUI
 * thread owns the thread, so it watches libuv's backend descriptor and a
 * timer for the next deadline, and runs one non-blocking pass on either. */
void __kml_loop_run_once(void) {
    u8 oneshot = __kml_loop_oneshot, nowait = __kml_loop_nowait;
    __kml_loop_oneshot = 1;
    __kml_loop_nowait = 1;
    __kml_event_loop_run();
    __kml_loop_oneshot = oneshot;
    __kml_loop_nowait = nowait;
    __kml_loop_idle = 0;
}

int __kml_loop_backend_fd(void) { return uv_backend_fd(&uvs_get()->loop); }
#ifdef _WIN32
/* libuv's backend on Windows: its completion port, no descriptor. */
void *__kml_loop_backend_port(void) { return uvs_get()->loop.iocp; }
#endif

/* Milliseconds until the next pass is due (-1: only when the backend
 * descriptor is readable), rounded up: a timer never fires early. */
i64 __kml_loop_next_ms(void) {
    if (__kml_conn_ran || runnable_now())
        return 0;
    i64 wait = pass_wait(0, 0, 0, 0);
    i64 ms = wait < 0 ? -1 : (wait + 999999) / 1000000;
    uv_loop_t *l = &uvs_get()->loop;
    uv_update_time(l);
    int ut = uv_backend_timeout(l);
    if (ut >= 0 && (ms < 0 || ut < ms))
        ms = ut;
    return ms;
}
