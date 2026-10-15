// klainpool.c — libuv-style blocking-work thread pool (TDD-00185) and the
// natives of the builtin modules written in TypeScript.
//
// The pool runs blocking syscalls off the single event-loop thread; the
// callback runs back ON the loop thread. That invariant is the whole point: the
// loop, fiber, microtask and Promise state are all thread-local and the reactor
// is thread-pinned, so a worker must never touch a Promise or the JS/GC heap
// directly — it runs the syscall and hands a plain result back, and the loop
// thread does everything JS-visible at drain time.
//
// The wakeup is per loop. On POSIX it is the loop's uv_async_t (loop.c). On
// Windows the reactor's one wait is the loop thread's
// completion port (TDD-00183), so a worker wakes it with an addressed packet to
// that port — no descriptor, nothing in the fd sets.

// statx (klainfs.c) is a GNU extension of glibc's <sys/stat.h>.
#if defined(__linux__) && !defined(_GNU_SOURCE)
#define _GNU_SOURCE
#endif
#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <errno.h>
#ifdef _WIN32
// The descriptor surface of win32io.c/win32fs.c (read/write/close/fcntl/poll
// and the socket calls), which takes the Linux constants the POSIX code below
// is written against.
// libuv with the shim's descriptor-level POSIX surface (TDD-00252).
#include "kml_uv_win.h"
// libuv polls a Windows socket by its SOCKET; a pipe or console descriptor
// is not one, and is woken by the shim instead (tcp_arm).
extern intptr_t __kml_win_poll_socket(int fd);
#define kml_uv_poll_init(loop, w, fd) uv_poll_init_socket((loop), (w), (uv_os_sock_t)__kml_win_poll_socket(fd))
int open(const char *path, int flags, ...);
int unlink(const char *path);
int _commit(int fd);
#define O_RDONLY 0
#define O_WRONLY 1
#define O_RDWR 2
#define O_CREAT 0x40
#define O_EXCL 0x80
#define O_TRUNC 0x200
#define O_APPEND 0x400
#undef EAFNOSUPPORT
#undef ENAMETOOLONG
#define EAFNOSUPPORT 97
#define ENAMETOOLONG 36
#define SO_ERROR 4
// Linux's numbers, which the Windows layer's setsockopt translates.
#define SO_BROADCAST 6
#define SO_SNDBUF 7
#define SO_RCVBUF 8
#define TCP_KEEPIDLE 4
#define SHUT_WR 1
#define AF_UNIX 1
// Winsock's AF_INET6 (a sockaddr carries the family as Winsock reads it;
// socket() and inet_pton/ntop accept it too).
#undef AF_INET6
#define AF_INET6 23
// getaddrinfo's failures are Winsock codes.
#define EAI_NONAME 11001
#define EAI_AGAIN 11002
#define EAI_NODATA 11004
// Winsock's in6_addr, sockaddr_in6 and sockaddr_storage (kml_uv_win.h).
struct sockaddr_un { sa_family_t sun_family; char sun_path[108]; };
#else
#include <unistd.h>
#include <fcntl.h>
#include <sys/select.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <arpa/inet.h>
#include <netdb.h>
#include <sys/un.h>
#include <poll.h>
#include <uv.h>
#define kml_uv_poll_init uv_poll_init
#endif
// The reactor's descriptor sets are plain bitmaps (no FD_SETSIZE ceiling).
#define KML_FD_SET(fd, set) (((unsigned char *)(set))[(fd) >> 3] |= (unsigned char)(1u << ((fd) & 7)))

#ifdef KLAINPOOL_GC
// Under -mm=gc a worker allocates GC memory (the read buffer / result string),
// so it must register with Boehm before its first allocation — mirroring the
// Worker/klain:sync threads (TDD-00098).
extern int GC_get_stack_base(void *);
extern int GC_register_my_thread(void *);
#ifdef KLAINPOOL_GC_ENABLE
// GC_allow_register_threads is call-once and shared across concurrency
// subsystems; the build defines this macro only when the pool is the sole owner
// of the enable (no Worker modules, no klain:sync — see ThreadPoolCFlags).
extern void GC_allow_register_threads(void);
#endif
#endif

// One struct, reused as work item (loop -> pool) and completion item (pool ->
// loop). arg0/arg1 are strdup'd copies the item owns and frees.
typedef struct kml_pool_item {
    struct kml_pool_item *next;
    char *arg0;
    char *arg1;
    struct kml_loop_port *port;
    // Its work, run on a worker, fills err/res; the loop then calls
    // inv(clo, err, res). a[]/d[] and buf/buflen are its arguments. live
    // links it into native_live while it is pending.
    void (*work)(struct kml_pool_item *);
    void *inv;
    void *clo;
    int64_t a[4];
    double d[4];            // an fs operation's numbers (klainfs.c)
    void *buf;
    int64_t buflen;
    int64_t err;            // a positive errno, or 0
    double res;
    char *res_str;          // a string result, read by nativeLastString()
    struct kml_pool_item *live_prev, *live_next;
} kml_pool_item;

// Per-loop completion port: a Treiber stack the workers push completions onto,
// the wakeup that ends this loop's wait (its uv_async_t on POSIX, the loop
// thread's reactor port on Windows), and the count of this loop's outstanding
// submissions (keeps the loop alive while I/O flies).
typedef struct kml_loop_port {
    _Atomic(kml_pool_item *) comp_head;
    // The port's uv_async_t: woken by a completion or a hold change from
    // another thread, and referenced exactly while inflight > 0.
    uv_async_t *async;
    int refd;
    atomic_long inflight;
} kml_loop_port;

// The port is per event-loop-thread. A Worker runs its own loop on its own
// thread and gets its own port; the pool itself is process-wide (below).
static __thread kml_loop_port *tls_port = NULL;

extern uv_loop_t *__kml_loop_uv(void);
extern void *__kml_loop_raw_alloc(size_t n);
extern _Thread_local unsigned char __kml_conn_poke;
_Bool __kml_pool_dispatch(void);

// On the port's own thread (uv_ref is the loop thread's alone): match the
// async's reference to the hold count.
static void port_sync(kml_loop_port *p) {
    int want = atomic_load_explicit(&p->inflight, memory_order_relaxed) > 0;
    if (want == p->refd) return;
    p->refd = want;
    if (want) uv_ref((uv_handle_t *)p->async);
    else uv_unref((uv_handle_t *)p->async);
}

// Inside the wait: deliver the completions, then re-match the reference.
// A completion may resume a connection fiber's await.
static void on_port_async(uv_async_t *a) {
    if (__kml_pool_dispatch()) __kml_conn_poke = 1;
    port_sync((kml_loop_port *)a->data);
}

static kml_loop_port *loop_port(void) {
    if (tls_port) return tls_port;
    kml_loop_port *p = (kml_loop_port *)calloc(1, sizeof *p);
    p->async = (uv_async_t *)__kml_loop_raw_alloc(sizeof *p->async);
    uv_async_init(__kml_loop_uv(), p->async, on_port_async);
    p->async->data = p;
    uv_unref((uv_handle_t *)p->async);
    tls_port = p;
    return p;
}

// A change to p's hold count, from any thread: the port's own thread
// re-matches its reference now; another thread wakes it to.
static void port_hold(kml_loop_port *p, long d) {
    atomic_fetch_add_explicit(&p->inflight, d, memory_order_relaxed);
    if (p == tls_port) port_sync(p);
    else uv_async_send(p->async);
}

// ---- process-wide work queue (FIFO, mutex + condvar) -----------------------
static pthread_mutex_t q_mu = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t  q_cv = PTHREAD_COND_INITIALIZER;
static kml_pool_item  *q_head = NULL, *q_tail = NULL;
static int pool_started = 0;
static int pool_size = 4;                             // libuv's default

// Push one completion item onto the origin loop's Treiber stack and wake it.
static void push_completion(kml_loop_port *p, kml_pool_item *it) {
    kml_pool_item *h;
    do {
        h = atomic_load_explicit(&p->comp_head, memory_order_relaxed);
        it->next = h;
    } while (!atomic_compare_exchange_weak_explicit(
                 &p->comp_head, &h, it, memory_order_release, memory_order_relaxed));
    uv_async_send(p->async);
}

// Run one work item, which becomes its own completion.
static void run_item(kml_pool_item *it) {
    it->work(it);
}

// The pool's start-up barrier: every thread is running before the first
// job is queued, as libuv's init_threads waits for them — so jobs submitted
// in order start, and usually finish, in order.
static pthread_mutex_t start_mu = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t start_cv = PTHREAD_COND_INITIALIZER;
static int started_threads = 0;

static void *worker_main(void *arg) {
    (void)arg;
#ifdef KLAINPOOL_GC
    void *sb[2];
    GC_get_stack_base(sb);
    GC_register_my_thread(sb);
#endif
    pthread_mutex_lock(&start_mu);
    started_threads++;
    pthread_cond_signal(&start_cv);
    pthread_mutex_unlock(&start_mu);
    for (;;) {
        pthread_mutex_lock(&q_mu);
        while (!q_head) pthread_cond_wait(&q_cv, &q_mu);
        kml_pool_item *it = q_head;
        q_head = it->next;
        if (!q_head) q_tail = NULL;
        pthread_mutex_unlock(&q_mu);

        it->next = NULL;
        run_item(it);
        push_completion(it->port, it);       // the item is its own completion
    }
    return NULL;
}

// Permit threads the collector did not create (the pool's, a Worker's) to
// register themselves with Boehm before any of them allocates. Call-once: a
// second call aborts in Boehm.
#if defined(KLAINPOOL_GC) && defined(KLAINPOOL_GC_ENABLE)
static pthread_once_t gc_allow_once = PTHREAD_ONCE_INIT;
static void gc_allow_now(void) { GC_allow_register_threads(); }
static void kml_gc_allow(void) { pthread_once(&gc_allow_once, gc_allow_now); }
#else
static void kml_gc_allow(void) {}
#endif

// Started once, on the first submit, before any job is queued.
static pthread_once_t pool_once = PTHREAD_ONCE_INIT;

static void start_pool(void) {
    pool_started = 1;
    kml_gc_allow();
    const char *env = getenv("UV_THREADPOOL_SIZE");
    if (env && *env) {
        int v = atoi(env);
        if (v > 0) pool_size = v > 1024 ? 1024 : v;
    }
    int made = 0;
    for (int i = 0; i < pool_size; i++) {
        pthread_t t;
        if (pthread_create(&t, NULL, worker_main, NULL) == 0) {
            pthread_detach(t);
            made++;
        }
    }
    pthread_mutex_lock(&start_mu);
    while (started_threads < made) pthread_cond_wait(&start_cv, &start_mu);
    pthread_mutex_unlock(&start_mu);
}

// ---- submit (called on the loop thread) ------------------------------------
// Enqueue a prepared work item and bump this loop's inflight count, which its
// completion decrements: the loop stays alive while it is in flight.
static void enqueue_work(kml_loop_port *p, kml_pool_item *it) {
    it->port = p;
    port_hold(p, 1);
    pthread_once(&pool_once, start_pool);
    pthread_mutex_lock(&q_mu);
    if (q_tail) q_tail->next = it; else q_head = it;
    q_tail = it;
    pthread_cond_signal(&q_cv);
    pthread_mutex_unlock(&q_mu);
}

// ---- native operations (TDD-00231) -----------------------------------------
// The primitives the builtin modules written in TypeScript call (lib/native.d.ts):
// each runs one blocking syscall on a worker and calls its callback on the loop
// thread with (errno or 0, result). A pending operation is linked into
// native_live, a process-wide root: the collector scans globals but not the
// loop thread's TLS port, and the callback's closure (and the buffer it keeps)
// must stay reachable while the syscall runs.

static pthread_mutex_t live_mu = PTHREAD_MUTEX_INITIALIZER;
static kml_pool_item *native_live = NULL;

static void native_link(kml_pool_item *it) {
    pthread_mutex_lock(&live_mu);
    it->live_prev = NULL;
    it->live_next = native_live;
    if (native_live) native_live->live_prev = it;
    native_live = it;
    pthread_mutex_unlock(&live_mu);
}

static void native_unlink(kml_pool_item *it) {
    pthread_mutex_lock(&live_mu);
    if (it->live_prev) it->live_prev->live_next = it->live_next;
    else native_live = it->live_next;
    if (it->live_next) it->live_next->live_prev = it->live_prev;
    pthread_mutex_unlock(&live_mu);
}

static kml_pool_item *native_item(void (*work)(kml_pool_item *), void *inv, void *clo) {
    kml_pool_item *it = (kml_pool_item *)calloc(1, sizeof *it);
    it->work = work;
    it->inv = inv;
    it->clo = clo;
    return it;
}

static void native_submit(kml_pool_item *it) {
    native_link(it);
    enqueue_work(loop_port(), it);
}

// A native that completes later off the pool (the resolver's queries on
// c-ares): __kml_pool_begin, on the loop thread, holds the loop open and
// keeps inv/clo live; __kml_pool_finish, from any thread, delivers
// inv(clo, err, 0) on that loop with res_str (malloc'd; taken) as
// nativeLastString().
void *__kml_pool_begin(void *inv, void *clo) {
    kml_pool_item *it = native_item(NULL, inv, clo);
    it->port = loop_port();
    native_link(it);
    port_hold(it->port, 1);
    return it;
}

void __kml_pool_finish(void *item, int64_t err, char *res_str) {
    kml_pool_item *it = (kml_pool_item *)item;
    it->err = err;
    it->res_str = res_str;
    push_completion(it->port, it);
}

#ifdef _WIN32
#define kml_fsync _commit
// The Windows layer's pread/pwrite (win32fs.c): one transfer at the offset.
int64_t pread(int fd, void *b, size_t n, int64_t pos);
int64_t pwrite(int fd, const void *b, size_t n, int64_t pos);
static int64_t kml_pread(int fd, void *b, size_t n, int64_t pos) { return pread(fd, b, n, pos); }
static int64_t kml_pwrite(int fd, const void *b, size_t n, int64_t pos) { return pwrite(fd, b, n, pos); }
#else
#define kml_fsync fsync
static int64_t kml_pread(int fd, void *b, size_t n, int64_t pos) { return pread(fd, b, n, (off_t)pos); }
static int64_t kml_pwrite(int fd, const void *b, size_t n, int64_t pos) { return pwrite(fd, b, n, (off_t)pos); }
#endif

// A job of another runtime unit (crypto): work(job, &err, &res) runs on a
// worker, then the loop calls inv(clo, err, res). The job is the unit's own
// memory; its work frees what it no longer needs.
typedef void (*kml_job_work)(void *job, int64_t *err, double *res);

static void work_job(kml_pool_item *it) {
    kml_job_work w = (kml_job_work)(intptr_t)it->a[0];
    int64_t err = 0;
    double res = 0;
    w(it->buf, &err, &res);
    it->err = err;
    it->res = res;
}

void __kml_pool_job(kml_job_work work, void *job, void *inv, void *clo) {
    kml_pool_item *it = native_item(work_job, inv, clo);
    it->a[0] = (int64_t)(intptr_t)work;
    it->buf = job;
    native_submit(it);
}

// Node's stringToFlags: an open-flags string as the platform's O_* bits, or
// -1 for one Node rejects.
double __kml_native_fs_flags(const char *s) {
    if (!s) return -1;
#ifndef O_SYNC
#define O_SYNC 0
#endif
    static const struct { const char *name; int flags; } table[] = {
        {"r", O_RDONLY}, {"rs", O_RDONLY | O_SYNC}, {"sr", O_RDONLY | O_SYNC},
        {"r+", O_RDWR}, {"rs+", O_RDWR | O_SYNC}, {"sr+", O_RDWR | O_SYNC},
        {"w", O_TRUNC | O_CREAT | O_WRONLY}, {"wx", O_TRUNC | O_CREAT | O_WRONLY | O_EXCL},
        {"xw", O_TRUNC | O_CREAT | O_WRONLY | O_EXCL},
        {"w+", O_TRUNC | O_CREAT | O_RDWR}, {"wx+", O_TRUNC | O_CREAT | O_RDWR | O_EXCL},
        {"xw+", O_TRUNC | O_CREAT | O_RDWR | O_EXCL},
        {"a", O_APPEND | O_CREAT | O_WRONLY}, {"ax", O_APPEND | O_CREAT | O_WRONLY | O_EXCL},
        {"xa", O_APPEND | O_CREAT | O_WRONLY | O_EXCL},
        {"as", O_APPEND | O_CREAT | O_WRONLY | O_SYNC}, {"sa", O_APPEND | O_CREAT | O_WRONLY | O_SYNC},
        {"a+", O_APPEND | O_CREAT | O_RDWR}, {"ax+", O_APPEND | O_CREAT | O_RDWR | O_EXCL},
        {"xa+", O_APPEND | O_CREAT | O_RDWR | O_EXCL},
        {"as+", O_APPEND | O_CREAT | O_RDWR | O_SYNC}, {"sa+", O_APPEND | O_CREAT | O_RDWR | O_SYNC},
    };
    for (size_t i = 0; i < sizeof table / sizeof table[0]; i++)
        if (strcmp(s, table[i].name) == 0) return table[i].flags;
    return -1;
}

// ---- a native callback's string result -------------------------------------
// A callback's arguments are numbers; a string result (a resolved address)
// is read with nativeLastString() inside the callback.
static __thread char *native_last_str = NULL;

static void native_set_last_string(const char *s) {
    free(native_last_str);
    native_last_str = s ? strdup(s) : NULL;
}

// For the other runtime units (tls.c).
void __kml_native_set_last_string(const char *s) { native_set_last_string(s); }

// A headered KML string copy of the last string result ("" when none).
extern char *__kml_str_alloc(int64_t n);
extern void __kml_str_finalize(char *s);
char *__kml_native_last_string(void) {
    const char *src = native_last_str ? native_last_str : "";
    int64_t n = (int64_t)strlen(src);
    char *out = __kml_str_alloc(n + 1);
    memcpy(out, src, (size_t)n + 1);
    __kml_str_finalize(out);
    return out;
}

// ---- dns.lookup (getaddrinfo on the pool) --------------------------------------
// The callback gets (0, family) with the address as the last string, or
// (10000 + EAI code, 0) when the lookup fails.
static void work_lookup(kml_pool_item *it) {
    struct addrinfo hints, *res = NULL;
    memset(&hints, 0, sizeof hints);
    hints.ai_family = it->a[0] == 4 ? AF_INET : it->a[0] == 6 ? AF_INET6 : 0;
    hints.ai_socktype = SOCK_STREAM;
    int rc = getaddrinfo(it->arg0, NULL, &hints, &res);
    if (rc != 0 || !res) {
        it->err = 10000 + (rc < 0 ? -rc : rc);
        return;
    }
    char buf[64] = {0};
    if (res->ai_family == AF_INET6) {
        inet_ntop(AF_INET6, &((struct sockaddr_in6 *)res->ai_addr)->sin6_addr, buf, sizeof buf);
        it->res = 6;
    } else {
        inet_ntop(AF_INET, &((struct sockaddr_in *)res->ai_addr)->sin_addr, buf, sizeof buf);
        it->res = 4;
    }
    it->res_str = strdup(buf);
    freeaddrinfo(res);
}

void __kml_native_dns_lookup(const char *host, double family, void *inv, void *clo) {
    kml_pool_item *it = native_item(work_lookup, inv, clo);
    it->arg0 = strdup(host ? host : "");
    it->a[0] = (int64_t)family;
    native_submit(it);
}

// The EAI code's name, as Node's err.code (ENOTFOUND for a name that does
// not resolve).
char *__kml_native_eai_code(double code) {
    const char *name = "EAI_FAIL";
    int c = (int)code;
#ifdef EAI_NONAME
    if (c == (EAI_NONAME < 0 ? -EAI_NONAME : EAI_NONAME)) name = "ENOTFOUND";
#endif
#ifdef EAI_NODATA
    if (c == (EAI_NODATA < 0 ? -EAI_NODATA : EAI_NODATA)) name = "ENOTFOUND";
#endif
#ifdef EAI_AGAIN
    if (c == (EAI_AGAIN < 0 ? -EAI_AGAIN : EAI_AGAIN)) name = "EAI_AGAIN";
#endif
    int64_t n = (int64_t)strlen(name);
    char *out = __kml_str_alloc(n + 1);
    memcpy(out, name, (size_t)n + 1);
    __kml_str_finalize(out);
    return out;
}

// ---- TCP handles (Node's tcp_wrap, libuv-shaped) ------------------------------
// A handle is an integer id into a process-wide table, a collector root for
// the closures it holds. The dispatch runs what became ready on each: an
// accept, a connect's completion, a read, a queued write's progress, a
// shutdown, a close. POSIX: each handle's descriptor is a uv_poll_t on the
// thread's loop (TDD-00244 Stage 4), armed for what the handle waits on and
// referenced as libuv's own stream handles are; the dispatch is the loop's
// check callback. Windows: the reactor's fdset and dispatch hooks.
typedef struct kml_wreq {
    struct kml_wreq *next;
    char *data;
    int64_t len, off;
    void *inv, *clo;
    int sendfd; // an IPC write's descriptor, sent with its first byte; -1 none
} kml_wreq;

typedef struct kml_tcp {
    int fd;
    int server, listening, connecting, reading, refd;
    int closing, closed_notified, shut_pending, shut_done, read_eof;
    void *conn_inv, *conn_clo;       // server: onConnection
    void *connect_inv, *connect_clo; // client: onConnect
    void *read_inv, *read_clo;       // onRead
    void *shut_inv, *shut_clo;
    void *close_inv, *close_clo;
    kml_wreq *wq_head, *wq_tail;
    char *rbuf;
    int64_t rlen;
    char *pipe_path; // a listening pipe's path, unlinked at its close
    // TLS over the handle (tls.c's, Node's tls_wrap): the session, its state
    // (0 none, 1 handshaking, 2 open, 3 failed) and onSecure(status, 0).
    void *tls;
    int tls_state;
    void *sec_inv, *sec_clo;
    // The session's ciphertext not yet written, and the socket's end (with
    // its error) once a read for the session met it.
    char *cout;
    int64_t cout_len, cout_cap;
    int raw_eof, raw_err;
    int tls_notified; // close_notify sent
    // A datagram socket (Node's udp_wrap): each read is one datagram, from
    // the sender recorded here.
    int udp;
    struct sockaddr_storage from;
    // An IPC pipe (Node's uv_pipe_init(ipc=1)): descriptors that arrived
    // with the bytes read, oldest first, for uv_accept of a pending handle.
    int ipc;
    int *ipc_fds;
    int ipc_nfds, ipc_cap;
#ifdef _WIN32
    // A socket frame read so far (ipc_recv): its bytes, and how many.
    unsigned char *ipc_frame;
    int ipc_frame_len;
    // A pipe or console: its libuv stream (us_*), NULL for a socket.
    struct kml_ustream *us;
#endif
    uv_poll_t *poll;  // the descriptor's watcher, NULL until armed
    int pev;          // the events it is started for
    int rev;          // the events it reported since the last dispatch
    int unpollable;   // libuv cannot watch the descriptor: always ready
} kml_tcp;

// The TLS session operations tlshandle.c installs: I/O returns bytes, 0 at
// the end of the stream (read), -EAGAIN when the session waits on the
// socket, or another -errno for an error. The session is on memory BIOs:
// feed hands it the ciphertext read here (n == 0 at the socket's end), and
// drain takes the ciphertext to write.
typedef struct kml_tls_ops {
    int (*handshake)(void *tls);  // 1 done, 0 waiting, <0 error
    int64_t (*read)(void *tls, char *buf, int64_t n);
    int64_t (*write)(void *tls, const char *buf, int64_t n);
    int (*pending)(void *tls);
    void (*shutdown)(void *tls);
    void (*free)(void *tls);
    void (*feed)(void *tls, const char *buf, int64_t n);
    int64_t (*drain)(void *tls, char *buf, int64_t n);
} kml_tls_ops;
static const kml_tls_ops *tcp_tls = NULL;

void __kml_tcp_set_tls_ops(const void *ops) { tcp_tls = (const kml_tls_ops *)ops; }

static __thread kml_tcp **tcp_tab = NULL;
static __thread int tcp_cap = 0, tcp_n = 0;

static int tcp_new(int fd) {
    if (tcp_n == tcp_cap) {
        int nc = tcp_cap ? tcp_cap * 2 : 16;
        kml_tcp **nt = (kml_tcp **)calloc((size_t)nc, sizeof *nt);
        if (tcp_tab) memcpy(nt, tcp_tab, (size_t)tcp_cap * sizeof *nt);
        tcp_tab = nt;
        tcp_cap = nc;
    }
    kml_tcp *h = (kml_tcp *)calloc(1, sizeof *h);
    h->fd = fd;
    h->refd = 1;
    tcp_tab[tcp_n] = h;
    return tcp_n++;
}

static kml_tcp *tcp_get(double id) {
    int i = (int)id;
    return (i >= 0 && i < tcp_n) ? tcp_tab[i] : NULL;
}

// loop.c: the thread's libuv loop, the allocator its handles live in, and
// the bridge's release of a descriptor a handle here now watches.
extern uv_loop_t *__kml_loop_uv(void);
extern void *__kml_loop_raw_alloc(size_t n);
extern void __kml_loop_free_handle(uv_handle_t *h);
extern void __kml_loop_fd_forget(int fd);
extern _Thread_local unsigned char __kml_conn_poke;

static _Bool tcp_dispatch(void);
static int proc_kick_wanted(void);

#ifdef _WIN32
// ---- pipes and consoles as libuv streams (TDD-00252 Stage 3) ----------------
// libuv polls a Windows socket, never a pipe or a console: those are a
// uv_pipe_t or uv_tty_t, as Node's pipe_wrap and tty_wrap make them. The
// handle reads into a buffer the table's reads take from, and writes one
// request at a time; a read, the end, or a finished write is the handle
// becoming readable or writable to the dispatch above.
extern int __kml_errno_from_uv(int uv);
// A libuv error as an errno, never 0 (one with no errno of its own is EIO).
static int us_errno(int r) {
    int e = __kml_errno_from_uv(r);
    return e ? e : EIO;
}
// What a libuv-stream handle has for a descriptor: no shim descriptor, a
// number every descriptor call refuses (EBADF).
#define US_FD (1 << 30)
#define US_HIGH_WATER (4 << 20) // bytes buffered before reading pauses

typedef struct kml_ustream {
    uv_stream_t *s;
    kml_tcp *h;
    char *buf;
    int64_t off, len, cap;
    int eof, rerr;    // the end, or a read error (errno)
    int reading;      // uv_read_start'ed
    int wbusy;        // a write request in flight, of wpend bytes
    int64_t wpend;
    int64_t wdone;    // bytes its completion wrote, for the next send
    int werr;         // its error (errno), for the next send
    int collect;      // spawnSync's: read everything, never pause
    // A pipe server's accepted connections, oldest first.
    struct kml_ustream **conns;
    int nconns, cconns;
} kml_ustream;

static void us_alloc(uv_handle_t *x, size_t want, uv_buf_t *b) {
    kml_ustream *u = (kml_ustream *)x->data;
    (void)want;
    if (u->off > 0 && u->off == u->len) u->off = u->len = 0;
    if (u->cap - u->len < 65536) {
        if (u->off > 0) {
            memmove(u->buf, u->buf + u->off, (size_t)(u->len - u->off));
            u->len -= u->off;
            u->off = 0;
        }
        if (u->cap - u->len < 65536) {
            u->cap = u->cap ? u->cap * 2 : 131072;
            u->buf = (char *)realloc(u->buf, (size_t)u->cap);
        }
    }
    *b = uv_buf_init(u->buf + u->len, 65536);
}

static void us_on_read(uv_stream_t *s, ssize_t n, const uv_buf_t *b) {
    kml_ustream *u = (kml_ustream *)s->data;
    // Ctrl+Z at the start of a console line ends the stream, as the CRT's
    // console reads have it.
    if (n > 0 && s->type == UV_TTY && b->base[0] == 0x1a) n = UV_EOF;
    if (n > 0) u->len += n;
    else if (n == UV_EOF) u->eof = 1;
    else if (n < 0) u->rerr = us_errno((int)n);
    if (n == 0) return;
    if (u->eof || u->rerr || (!u->collect && u->len - u->off >= US_HIGH_WATER)) {
        uv_read_stop(s);
        u->reading = 0;
    }
    if (u->h) u->h->rev |= UV_READABLE;
}

static void us_on_write(uv_write_t *req, int status) {
    kml_ustream *u = (kml_ustream *)req->data;
    if (u) {
        u->wbusy = 0;
        if (status < 0) u->werr = us_errno(status);
        else u->wdone = u->wpend;
        u->wpend = 0;
        if (u->h) u->h->rev |= UV_WRITABLE;
    }
    free(req);
}

static void us_on_shutdown(uv_shutdown_t *req, int status) {
    (void)status;
    free(req);
}

static void us_on_close(uv_handle_t *x) {
    kml_ustream *u = (kml_ustream *)x->data;
    if (u) {
        free(u->buf);
        free(u);
    }
    __kml_loop_free_handle(x);
}

// A CRT descriptor (libuv's own currency on Windows) as a stream on loop:
// a console is a uv_tty_t, anything else a uv_pipe_t. libuv owns the
// descriptor from here. NULL when libuv refuses it (errno set).
static kml_ustream *us_open(uv_loop_t *loop, int crt, int readable) {
    kml_ustream *u = (kml_ustream *)calloc(1, sizeof *u);
    int r;
    if (uv_guess_handle(crt) == UV_TTY) {
        uv_tty_t *t = (uv_tty_t *)__kml_loop_raw_alloc(sizeof *t);
        r = uv_tty_init(loop, t, crt, readable);
        u->s = (uv_stream_t *)t;
    } else {
        uv_pipe_t *p = (uv_pipe_t *)__kml_loop_raw_alloc(sizeof *p);
        r = uv_pipe_init(loop, p, 0);
        if (r == 0) r = uv_pipe_open(p, crt);
        u->s = (uv_stream_t *)p;
    }
    u->s->data = u;
    if (r != 0) {
        errno = us_errno(r);
        uv_close((uv_handle_t *)u->s, us_on_close);
        return NULL;
    }
    return u;
}

// The handle's stream: buffered bytes, the end (0), -EAGAIN, or -errno.
static int64_t us_recv(kml_ustream *u, char *p, int64_t n) {
    if (u->len > u->off) {
        int64_t k = u->len - u->off < n ? u->len - u->off : n;
        memcpy(p, u->buf + u->off, (size_t)k);
        u->off += k;
        return k;
    }
    if (u->rerr) return -u->rerr;
    if (u->eof) return 0;
    return -EAGAIN;
}

// One write request at a time: the first send of a buffer starts it and
// answers -EAGAIN, the next send of the same buffer (the queue's head, once
// the completion made the handle writable) takes what it wrote.
static int64_t us_send(kml_ustream *u, const char *p, int64_t n) {
    if (u->werr) {
        int e = u->werr;
        u->werr = 0;
        return -e;
    }
    if (u->wdone > 0) {
        int64_t d = u->wdone < n ? u->wdone : n;
        u->wdone = 0;
        return d;
    }
    if (u->wbusy) return -EAGAIN;
    uv_buf_t b = uv_buf_init((char *)p, (unsigned int)n);
    int r = uv_try_write(u->s, &b, 1);
    if (r > 0) return r;
    if (r != UV_EAGAIN && r != UV_ENOSYS) return -us_errno(r);
    uv_write_t *req = (uv_write_t *)malloc(sizeof *req + (size_t)n);
    memcpy(req + 1, p, (size_t)n);
    req->data = u;
    b = uv_buf_init((char *)(req + 1), (unsigned int)n);
    r = uv_write(req, u->s, &b, 1, us_on_write);
    if (r != 0) {
        free(req);
        return -us_errno(r);
    }
    u->wbusy = 1;
    u->wpend = n;
    return -EAGAIN;
}

// What the stream holds for the dispatch: buffered bytes or their end, a
// finished write.
static int us_ready(kml_ustream *u) {
    int ev = 0;
    if (u->nconns > 0) ev |= UV_READABLE;
    if (u->len > u->off || u->eof || u->rerr) ev |= UV_READABLE;
    if (!u->wbusy && (u->wdone || u->werr)) ev |= UV_WRITABLE;
    return ev;
}

// Reading, and holding the loop, for what the handle now waits on.
static void us_arm(kml_tcp *h) {
    kml_ustream *u = h->us;
    int want = !h->closing && h->reading && !h->read_eof && !u->eof && !u->rerr &&
               u->len - u->off < US_HIGH_WATER;
    if (want && !u->reading) {
        if (uv_read_start(u->s, us_alloc, us_on_read) == 0) u->reading = 1;
        else u->eof = 1, h->rev |= UV_READABLE;
    } else if (!want && u->reading) {
        uv_read_stop(u->s);
        u->reading = 0;
    }
    if (h->refd || h->wq_head || u->wbusy) uv_ref((uv_handle_t *)u->s);
    else uv_unref((uv_handle_t *)u->s);
}

static void us_close_detached(kml_ustream *u);
static int tcp_new_us(kml_ustream *u);
static void tcp_touch(kml_tcp *h);
static void us_close(kml_tcp *h) {
    kml_ustream *u = h->us;
    h->us = NULL;
    u->h = NULL;
    for (int i = 0; i < u->nconns; i++) us_close_detached(u->conns[i]);
    free(u->conns);
    u->conns = NULL;
    u->nconns = 0;
    if (u->reading) uv_read_stop(u->s);
    uv_close((uv_handle_t *)u->s, us_on_close);
}

static void us_close_detached(kml_ustream *u) {
    uv_close((uv_handle_t *)u->s, us_on_close);
}

// The next connection a pipe server accepted, or NULL.
static kml_ustream *us_take_conn(kml_ustream *u) {
    if (u->nconns == 0) return NULL;
    kml_ustream *c = u->conns[0];
    memmove(u->conns, u->conns + 1, (size_t)(u->nconns - 1) * sizeof *u->conns);
    u->nconns--;
    return c;
}

// A pipe server's connection: accepted into a new pipe at once, as libuv
// offers the next instance only after uv_accept.
static void us_on_connection(uv_stream_t *server, int status) {
    kml_ustream *u = (kml_ustream *)server->data;
    if (status < 0) return;
    kml_ustream *c = (kml_ustream *)calloc(1, sizeof *c);
    uv_pipe_t *p = (uv_pipe_t *)__kml_loop_raw_alloc(sizeof *p);
    uv_pipe_init(server->loop, p, 0);
    c->s = (uv_stream_t *)p;
    p->data = c;
    if (uv_accept(server, c->s) != 0) {
        uv_close((uv_handle_t *)p, us_on_close);
        return;
    }
    if (u->nconns == u->cconns) {
        u->cconns = u->cconns ? u->cconns * 2 : 4;
        u->conns = (kml_ustream **)realloc(u->conns, (size_t)u->cconns * sizeof *u->conns);
    }
    u->conns[u->nconns++] = c;
    if (u->h) u->h->rev |= UV_READABLE;
}

static void us_on_connect(uv_connect_t *req, int status) {
    kml_ustream *u = (kml_ustream *)req->data;
    free(req);
    if (!u || !u->h) return;
    u->h->connecting = 2 + (status < 0 ? us_errno(status) : 0);
    u->h->rev |= UV_WRITABLE;
}

// Whether path is in the pipe namespace (\\.\pipe\name, \\?\pipe\name): a
// named pipe, Node's local socket on Windows. Any other path stays an AF_UNIX
// socket.
static int us_pipe_name(const char *p) {
    return p && p[0] == '\\' && p[1] == '\\' && (p[2] == '.' || p[2] == '?') && p[3] == '\\' &&
           _strnicmp(p + 4, "pipe\\", 5) == 0 && p[9] != 0;
}

// A named-pipe server (libuv's uv_pipe_bind + uv_listen): its handle, or -errno.
static double us_pipe_listen(const char *path, int backlog, void *inv, void *clo) {
    kml_ustream *u = (kml_ustream *)calloc(1, sizeof *u);
    uv_pipe_t *p = (uv_pipe_t *)__kml_loop_raw_alloc(sizeof *p);
    uv_pipe_init(__kml_loop_uv(), p, 0);
    u->s = (uv_stream_t *)p;
    p->data = u;
    int r = uv_pipe_bind(p, path);
    if (r == 0) r = uv_listen(u->s, backlog, us_on_connection);
    if (r != 0) {
        uv_close((uv_handle_t *)p, us_on_close);
        return -us_errno(r);
    }
    int id = tcp_new_us(u);
    kml_tcp *h = tcp_tab[id];
    h->server = h->listening = 1;
    h->conn_inv = inv;
    h->conn_clo = clo;
    tcp_touch(h);
    return id;
}

// A named-pipe client (uv_pipe_connect): its handle; the connect's outcome
// reaches onConnect as a socket's does.
static double us_pipe_connect(const char *path, void *inv, void *clo) {
    kml_ustream *u = (kml_ustream *)calloc(1, sizeof *u);
    uv_pipe_t *p = (uv_pipe_t *)__kml_loop_raw_alloc(sizeof *p);
    uv_pipe_init(__kml_loop_uv(), p, 0);
    u->s = (uv_stream_t *)p;
    p->data = u;
    int id = tcp_new_us(u);
    kml_tcp *h = tcp_tab[id];
    h->connecting = 1;
    h->connect_inv = inv;
    h->connect_clo = clo;
    uv_connect_t *req = (uv_connect_t *)malloc(sizeof *req);
    req->data = u;
    uv_pipe_connect(req, p, path, us_on_connect);
    tcp_touch(h);
    return id;
}

// A table handle over a stream.
static int tcp_new_us(kml_ustream *u) {
    int id = tcp_new(US_FD);
    tcp_tab[id]->us = u;
    u->h = tcp_tab[id];
    return id;
}
#endif

// The loop's check callback runs the dispatch after each poll. Work ready
// without the descriptor (a close to report, a failed connect, decrypted
// bytes, a shutdown after the last write, a child's held exit) starts an
// idle handle, so that poll does not block: a referenced one when the work
// holds the loop open, an unreferenced one when it does not.
static __thread uv_check_t *tcp_check;
static __thread uv_idle_t *tcp_kick, *tcp_kick_weak;

static void tcp_on_poll(uv_poll_t *w, int status, int events) {
    kml_tcp *h = (kml_tcp *)w->data;
    if (status < 0) events = UV_READABLE | UV_WRITABLE; // the operation answers
    h->rev |= events;
}

static void tcp_on_idle(uv_idle_t *w) { (void)w; }
static void tcp_rearm_all(void);

static void tcp_on_check(uv_check_t *w) {
    (void)w;
    if (tcp_dispatch()) __kml_conn_poke = 1;
    tcp_rearm_all();
}

static void tcp_loop_init(void) {
    if (tcp_check) return;
    uv_loop_t *loop = __kml_loop_uv();
    tcp_check = (uv_check_t *)__kml_loop_raw_alloc(sizeof *tcp_check);
    uv_check_init(loop, tcp_check);
    uv_check_start(tcp_check, tcp_on_check);
    uv_unref((uv_handle_t *)tcp_check);
    tcp_kick = (uv_idle_t *)__kml_loop_raw_alloc(sizeof *tcp_kick);
    uv_idle_init(loop, tcp_kick);
    tcp_kick_weak = (uv_idle_t *)__kml_loop_raw_alloc(sizeof *tcp_kick_weak);
    uv_idle_init(loop, tcp_kick_weak);
    uv_unref((uv_handle_t *)tcp_kick_weak);
}

// Whether the handle has work the descriptor will not announce.
static int tcp_immediate(kml_tcp *h) {
    if (h->closing) return !h->closed_notified;
    if (h->fd < 0) return 0;
    if (h->connecting > 1) return 1;
    if (h->tls_state == 2 && h->reading && !h->read_eof && tcp_tls && tcp_tls->pending(h->tls) > 0) return 1;
    if (h->shut_pending && !h->wq_head && h->cout_len == 0) return 1;
#ifdef _WIN32
    if (h->us) {
        int ev = us_ready(h->us);
        if ((ev & UV_READABLE) && h->reading && !h->read_eof) return 1;
        if ((ev & UV_WRITABLE) && h->wq_head) return 1;
    }
#endif
    return 0;
}

// Start, restart or stop the handle's watcher for what it now waits on, as
// tcp_fdset_add chose the sets; it holds the loop open while referenced, or
// while writes are queued (libuv's write requests count regardless).
static void tcp_arm(kml_tcp *h) {
    tcp_loop_init();
    int want = 0;
    if (!h->closing && h->fd >= 0) {
        if (h->listening || (h->reading && !h->read_eof) || h->tls_state == 1) want |= UV_READABLE;
        if (h->connecting == 1 || (h->wq_head && h->tls_state != 1) || h->cout_len > 0) want |= UV_WRITABLE;
    }
#ifdef _WIN32
    if (h->us) {
        us_arm(h);
        return;
    }
#endif
    if (want && !h->poll && !h->unpollable) {
        uv_poll_t *w = (uv_poll_t *)__kml_loop_raw_alloc(sizeof *w);
        __kml_loop_fd_forget(h->fd);
        if (kml_uv_poll_init(__kml_loop_uv(), w, h->fd) != 0) {
            __kml_loop_free_handle((uv_handle_t *)w);
            h->unpollable = 1;
        } else {
            w->data = h;
            h->poll = w;
        }
    }
    if (h->poll) {
        if (want != h->pev) {
            if (want) uv_poll_start(h->poll, want, tcp_on_poll);
            else uv_poll_stop(h->poll);
            h->pev = want;
        }
        if (h->refd || h->wq_head) uv_ref((uv_handle_t *)h->poll);
        else uv_unref((uv_handle_t *)h->poll);
    }
}

// The kick: started while any handle (or held child exit) has work ready.
static void tcp_rearm_all(void) {
    tcp_loop_init();
    int strong = 0, weak = 0;
    for (int i = 0; i < tcp_n; i++) {
        kml_tcp *h = tcp_tab[i];
        tcp_arm(h);
        int imm = tcp_immediate(h) || (h->unpollable && h->fd >= 0 && !h->closing &&
                                       (h->listening || h->reading || h->wq_head || h->connecting));
        if (!imm) continue;
        // A close, a queued write and a referenced handle's work hold the loop.
        if (h->closing || h->wq_head || h->refd) strong = 1;
        else weak = 1;
    }
    int pk = proc_kick_wanted();
    if (pk == 2) strong = 1;
    else if (pk == 1) weak = 1;
    if (strong) uv_idle_start(tcp_kick, tcp_on_idle);
    else uv_idle_stop(tcp_kick);
    if (weak) uv_idle_start(tcp_kick_weak, tcp_on_idle);
    else uv_idle_stop(tcp_kick_weak);
}

// A handle's watcher closes with its descriptor (before it, so the number
// can be reused).
static void tcp_unwatch(kml_tcp *h) {
    if (!h->poll) return;
    uv_poll_stop(h->poll);
    uv_close((uv_handle_t *)h->poll, __kml_loop_free_handle);
    h->poll = NULL;
    h->pev = 0;
}
// After a change to what the handle waits on: rearm it, and kick the loop
// when the work is ready now (the check callback stops the kick).
static void tcp_touch(kml_tcp *h) {
    if (!h) return;
    tcp_arm(h);
    int imm = tcp_immediate(h) || (h->unpollable && h->fd >= 0 && !h->closing);
    if (!imm) return;
    if (h->closing || h->wq_head || h->refd) uv_idle_start(tcp_kick, tcp_on_idle);
    else uv_idle_start(tcp_kick_weak, tcp_on_idle);
}

int __kml_tcp_fd(double id) {
    kml_tcp *h = tcp_get(id);
    return h ? h->fd : -1;
}

// Start TLS on the handle: the handshake runs in the dispatch, then
// onSecure(0, 0), or onSecure(error, 0).
int __kml_tcp_attach_tls(double id, void *tls, void *inv, void *clo) {
    kml_tcp *h = tcp_get(id);
    if (!h || h->fd < 0) return -EBADF;
    h->tls = tls;
    h->tls_state = 1;
    h->sec_inv = inv;
    h->sec_clo = clo;
    tcp_touch(h);
    return 0;
}

void *__kml_tcp_tls(double id) {
    kml_tcp *h = tcp_get(id);
    return h ? h->tls : NULL;
}

// The socket's own I/O: bytes, -EAGAIN, or another -errno.
static int64_t raw_send(kml_tcp *h, const char *p, int64_t n) {
#ifdef _WIN32
    if (h->us) return us_send(h->us, p, n);
#endif
#ifdef MSG_NOSIGNAL
    int64_t r = send(h->fd, p, (size_t)n, MSG_NOSIGNAL);
#else
    int64_t r = write(h->fd, p, (size_t)n);
#endif
    if (r < 0) return (errno == EWOULDBLOCK || errno == EINTR) ? -EAGAIN : -errno;
    return r;
}

static int64_t raw_recv(kml_tcp *h, char *p, int64_t n) {
#ifdef _WIN32
    if (h->us) return us_recv(h->us, p, n);
#endif
    int64_t r = read(h->fd, p, (size_t)n);
    if (r < 0) return (errno == EWOULDBLOCK || errno == EINTR) ? -EAGAIN : -errno;
    return r;
}

// The session's ciphertext out: drained into cout, then written as far as
// the socket takes it. 0, or -errno.
static int tls_push(kml_tcp *h) {
    for (;;) {
        if (h->cout_cap - h->cout_len < 16384) {
            h->cout_cap = h->cout_cap ? h->cout_cap * 2 : 65536;
            h->cout = (char *)realloc(h->cout, (size_t)h->cout_cap);
        }
        int64_t n = tcp_tls->drain(h->tls, h->cout + h->cout_len, h->cout_cap - h->cout_len);
        if (n <= 0) break;
        h->cout_len += n;
    }
    int64_t off = 0;
    while (off < h->cout_len) {
        int64_t n = raw_send(h, h->cout + off, h->cout_len - off);
        if (n == -EAGAIN) break;
        if (n < 0) {
            h->cout_len = 0;
            return (int)n;
        }
        off += n;
    }
    memmove(h->cout, h->cout + off, (size_t)(h->cout_len - off));
    h->cout_len -= off;
    return 0;
}

// The socket's ciphertext in, to the end of what it has now.
static void tls_pull(kml_tcp *h) {
    char buf[65536];
    while (!h->raw_eof) {
        int64_t n = raw_recv(h, buf, sizeof buf);
        if (n == -EAGAIN) break;
        if (n <= 0) {
            h->raw_eof = 1;
            h->raw_err = n < 0 ? (int)-n : 0;
            tcp_tls->feed(h->tls, NULL, 0);
            break;
        }
        tcp_tls->feed(h->tls, buf, n);
    }
}

// Plain or TLS I/O on the handle: bytes, -EAGAIN, or another -errno. A TLS
// write waits while earlier ciphertext is still unwritten.
static int64_t tcp_send(kml_tcp *h, const char *p, int64_t n) {
    if (!(h->tls && tcp_tls)) return raw_send(h, p, n);
    int e = tls_push(h);
    if (e < 0) return e;
    if (h->cout_len > 0) return -EAGAIN;
    int64_t r = tcp_tls->write(h->tls, p, n);
    e = tls_push(h);
    return e < 0 ? e : r;
}

#ifndef _WIN32
// An IPC pipe's read or write carrying descriptors (SCM_RIGHTS), as libuv's
// uv__read / uv__write do for a pipe opened with ipc set.
static int64_t ipc_recv(kml_tcp *h, char *p, int64_t n) {
    struct iovec iov = { p, (size_t)n };
    union { struct cmsghdr c; char b[CMSG_SPACE(sizeof(int) * 16)]; } ctl;
    struct msghdr m;
    memset(&m, 0, sizeof m);
    m.msg_iov = &iov;
    m.msg_iovlen = 1;
    m.msg_control = ctl.b;
    m.msg_controllen = sizeof ctl.b;
    int64_t r = recvmsg(h->fd, &m, 0);
    if (r < 0) return (errno == EWOULDBLOCK || errno == EINTR) ? -EAGAIN : -errno;
    for (struct cmsghdr *c = CMSG_FIRSTHDR(&m); c; c = CMSG_NXTHDR(&m, c)) {
        if (c->cmsg_level != SOL_SOCKET || c->cmsg_type != SCM_RIGHTS) continue;
        int k = (int)((c->cmsg_len - CMSG_LEN(0)) / sizeof(int));
        int *fds = (int *)CMSG_DATA(c);
        for (int i = 0; i < k; i++) {
            fcntl(fds[i], F_SETFD, FD_CLOEXEC);
            if (h->ipc_nfds == h->ipc_cap) {
                h->ipc_cap = h->ipc_cap ? h->ipc_cap * 2 : 4;
                h->ipc_fds = (int *)realloc(h->ipc_fds, (size_t)h->ipc_cap * sizeof(int));
            }
            h->ipc_fds[h->ipc_nfds++] = fds[i];
        }
    }
    return r;
}

static int64_t ipc_send_fd(kml_tcp *h, const char *p, int64_t n, int fd) {
    struct iovec iov = { (void *)p, (size_t)n };
    union { struct cmsghdr c; char b[CMSG_SPACE(sizeof(int))]; } ctl;
    struct msghdr m;
    memset(&m, 0, sizeof m);
    memset(&ctl, 0, sizeof ctl);
    m.msg_iov = &iov;
    m.msg_iovlen = 1;
    m.msg_control = ctl.b;
    m.msg_controllen = sizeof ctl.b;
    struct cmsghdr *c = CMSG_FIRSTHDR(&m);
    c->cmsg_level = SOL_SOCKET;
    c->cmsg_type = SCM_RIGHTS;
    c->cmsg_len = CMSG_LEN(sizeof(int));
    memcpy(CMSG_DATA(c), &fd, sizeof(int));
#ifdef MSG_NOSIGNAL
    int64_t r = sendmsg(h->fd, &m, MSG_NOSIGNAL);
#else
    int64_t r = sendmsg(h->fd, &m, 0);
#endif
    if (r < 0) return (errno == EWOULDBLOCK || errno == EINTR) ? -EAGAIN : -errno;
    return r;
}
#endif

#ifdef _WIN32
// An IPC pipe's socket on Windows travels in the byte stream, as libuv's do:
// a 0 byte (which Node's serialized messages never hold), "KMLH", then the
// WSAPROTOCOL_INFOW the sender duplicated for this process, ahead of the
// message it goes with. ipc_recv lifts each frame out of what it reads and
// keeps the socket for ipc_take_fd.
extern void __kml_win_ipc_set_peer(int fd, int pid);
extern int __kml_win_ipc_peer(int fd);
extern int __kml_win_ipc_info_size(void);
extern int __kml_win_ipc_dup(int fd, int pid, void *info);
extern int __kml_win_ipc_adopt(const void *info);
#define IPC_FRAME_HEAD 5

static double tcp_write_fd(double id, void *buf, int64_t size, double offset, double length, int sendfd, void *inv, void *clo);

static void ipc_keep_fd(kml_tcp *h, int fd) {
    if (h->ipc_nfds == h->ipc_cap) {
        h->ipc_cap = h->ipc_cap ? h->ipc_cap * 2 : 4;
        h->ipc_fds = (int *)realloc(h->ipc_fds, (size_t)h->ipc_cap * sizeof(int));
    }
    h->ipc_fds[h->ipc_nfds++] = fd;
}

static int64_t ipc_recv(kml_tcp *h, char *p, int64_t n) {
    int frame = IPC_FRAME_HEAD + __kml_win_ipc_info_size();
    for (;;) {
        int64_t r = raw_recv(h, p, n);
        if (r <= 0) {
            // EOF inside a frame drops the partial frame.
            if (r == 0) h->ipc_frame_len = 0;
            return r;
        }
        int64_t o = 0;
        for (int64_t i = 0; i < r; i++) {
            if (h->ipc_frame_len == 0 && p[i] != 0) {
                p[o++] = p[i];
                continue;
            }
            if (!h->ipc_frame) h->ipc_frame = (unsigned char *)malloc((size_t)frame);
            h->ipc_frame[h->ipc_frame_len++] = (unsigned char)p[i];
            if (h->ipc_frame_len == frame) {
                h->ipc_frame_len = 0;
                if (memcmp(h->ipc_frame + 1, "KMLH", 4) == 0) {
                    int fd = __kml_win_ipc_adopt(h->ipc_frame + IPC_FRAME_HEAD);
                    if (fd >= 0) ipc_keep_fd(h, fd);
                }
            }
        }
        // A read of nothing but frame bytes is not the end of the stream:
        // the message they precede follows.
        if (o > 0) return o;
    }
}

// The write of buf with the socket behind fd duplicated for the peer.
static double ipc_write_frame(double id, void *buf, int64_t size, double offset, double length, int fd, void *inv, void *clo) {
    kml_tcp *h = tcp_get(id);
    if (!h || h->fd < 0) return -EBADF;
    int pid = __kml_win_ipc_peer(h->fd);
    if (pid <= 0) return -ENOTSUP;
    int info = __kml_win_ipc_info_size();
    int64_t off = (int64_t)offset, len = (int64_t)length;
    if (off < 0) off = 0;
    if (off > size) off = size;
    if (len > size - off) len = size - off;
    int64_t total = IPC_FRAME_HEAD + info + len;
    char *out = (char *)malloc((size_t)total);
    out[0] = 0;
    memcpy(out + 1, "KMLH", 4);
    if (__kml_win_ipc_dup(fd, pid, out + IPC_FRAME_HEAD) != 0) {
        int e = errno;
        free(out);
        return -e;
    }
    memcpy(out + IPC_FRAME_HEAD + info, (const char *)buf + off, (size_t)len);
    double r = tcp_write_fd(id, out, total, 0, (double)total, -1, inv, clo);
    free(out);
    return r;
}
#endif

// A queued write's next bytes, its descriptor with the first of them.
static int64_t tcp_send_req(kml_tcp *h, kml_wreq *w) {
#ifndef _WIN32
    if (w->sendfd >= 0) {
        int64_t n = ipc_send_fd(h, w->data + w->off, w->len - w->off, w->sendfd);
        if (n > 0) w->sendfd = -1;
        return n;
    }
#endif
    return tcp_send(h, w->data + w->off, w->len - w->off);
}

static int64_t tcp_recv(kml_tcp *h, char *p, int64_t n) {
    if (h->tls && tcp_tls) {
        tls_pull(h);
        int64_t r = tcp_tls->read(h->tls, p, n);
        tls_push(h); // a read may answer (a key update, an alert)
        if (r == -EAGAIN && h->raw_eof) return h->raw_err ? -h->raw_err : 0;
        return r;
    }
    if (h->ipc) return ipc_recv(h, p, n);
    return raw_recv(h, p, n);
}

static void set_nonblock(int fd) {
    int fl = fcntl(fd, F_GETFL, 0);
    fcntl(fd, F_SETFL, fl | O_NONBLOCK);
}

typedef void (*kml_inv2)(void *, double, double);
// Node's MakeCallback: a callback from the loop runs, then the tick queue and
// the promise jobs it left, before the loop's next callback.
extern void __kml_drain_microtasks(void);
static void kml_make_callback(void *inv, void *clo, double a, double b) {
    ((kml_inv2)inv)(clo, a, b);
    __kml_drain_microtasks();
}
#define TCP_CALL(inv, clo, a, b) kml_make_callback((inv), (clo), (double)(a), (double)(b))

// Parse an IP string into a sockaddr; 0 on success.
static int tcp_addr(const char *host, int port, struct sockaddr_storage *ss, socklen_t *len) {
    memset(ss, 0, sizeof *ss);
    struct sockaddr_in6 *a6 = (struct sockaddr_in6 *)ss;
    struct sockaddr_in *a4 = (struct sockaddr_in *)ss;
    if (host && strchr(host, ':')) {
        a6->sin6_family = AF_INET6;
        a6->sin6_port = htons((unsigned short)port);
        if (inet_pton(AF_INET6, host, &a6->sin6_addr) != 1) return EINVAL;
        *len = sizeof *a6;
        return 0;
    }
    a4->sin_family = AF_INET;
    a4->sin_port = htons((unsigned short)port);
    if (inet_pton(AF_INET, host && *host ? host : "0.0.0.0", &a4->sin_addr) != 1) return EINVAL;
    *len = sizeof *a4;
    return 0;
}

// A cluster worker tells the primary it is listening (the cluster runtime's
// strong definition replaces this one when the program uses cluster).
#ifndef _WIN32
__attribute__((weak)) void __kml_cluster_announce_listening_at(int32_t port, const char *host) { (void)port; (void)host; }
#endif

// Listen on host:port ("" is every address, IPv6 dual-stack when possible).
// Returns the handle id, or -errno.
double __kml_native_tcp_listen(const char *host, double port, double backlog, void *inv, void *clo) {
    struct sockaddr_storage ss;
    socklen_t len;
    int any = !host || !*host;
    int fd = -1;
#ifdef _WIN32
    // A re-spawned cluster worker (Windows has no fork): the primary bound
    // every listener before starting it and deals their connections to it
    // round-robin, as Node's cluster does there. Binding again is EADDRINUSE;
    // the inherited listener is this server's.
    {
        extern int __kml_win_inherited_listener(int port);
        int inherited = port > 0 ? __kml_win_inherited_listener((int)port) : -1;
        if (inherited >= 0) {
            fd = inherited;
            goto listening;
        }
    }
#endif
    if (any) {
        fd = socket(AF_INET6, SOCK_STREAM, 0);
        if (fd >= 0) {
#ifndef _WIN32 // the Windows layer's socket() already clears it
            int off = 0;
            setsockopt(fd, IPPROTO_IPV6, IPV6_V6ONLY, &off, sizeof off);
#endif
            tcp_addr("::", (int)port, &ss, &len);
        }
    }
    if (fd < 0) {
        if (tcp_addr(any ? "0.0.0.0" : host, (int)port, &ss, &len) != 0) return -EINVAL;
        fd = socket(ss.ss_family, SOCK_STREAM, 0);
        if (fd < 0) return -errno;
    }
    int one = 1;
    setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
#if defined(SO_REUSEPORT) && !defined(_WIN32)
    // Cluster workers share the port; the kernel spreads the accepts.
    int worker = getenv("KML_CLUSTER_WORKER_ID") != NULL;
    if (worker) setsockopt(fd, SOL_SOCKET, SO_REUSEPORT, &one, sizeof one);
#endif
    if (bind(fd, (struct sockaddr *)&ss, len) != 0 || listen(fd, backlog > 0 ? (int)backlog : 511) != 0) {
        int e = errno;
        close(fd);
        return -e;
    }
#ifdef FD_CLOEXEC
    fcntl(fd, F_SETFD, FD_CLOEXEC);
#endif
#ifdef _WIN32
listening:;
#endif
    set_nonblock(fd);
    int id = tcp_new(fd);
    kml_tcp *h = tcp_tab[id];
    h->server = h->listening = 1;
    h->conn_inv = inv;
    h->conn_clo = clo;
#if defined(SO_REUSEPORT) && !defined(_WIN32)
    if (worker) {
        struct sockaddr_storage bound;
        socklen_t blen = sizeof bound;
        int bport = (int)port;
        if (getsockname(fd, (struct sockaddr *)&bound, &blen) == 0)
            bport = ntohs(bound.ss_family == AF_INET6 ? ((struct sockaddr_in6 *)&bound)->sin6_port
                                                      : ((struct sockaddr_in *)&bound)->sin_port);
        __kml_cluster_announce_listening_at(bport, any ? NULL : host);
    }
#endif
    tcp_touch(h);
    return id;
}

// A bound, listening socket nothing accepts on here (net's
// _createServerHandle for a cluster primary's shared handle: the workers
// accept on the descriptor they are sent). Returns the handle id, or -errno.
double __kml_native_tcp_bind(const char *host, double port, double backlog) {
    double id = __kml_native_tcp_listen(host, port, backlog, NULL, NULL);
    if (id >= 0) {
        tcp_tab[(int)id]->listening = 0;
        tcp_touch(tcp_tab[(int)id]);
    }
    return id;
}

// A listening socket's existing descriptor (a handle received over IPC, or
// `listen({ fd })`) as a server handle; onConnection as tcp_listen's.
// Returns the handle id, or -errno.
double __kml_native_tcp_listen_open(double fdd, void *inv, void *clo) {
    int fd = (int)fdd;
    if (fd < 0) return -EBADF;
#ifdef FD_CLOEXEC
    fcntl(fd, F_SETFD, FD_CLOEXEC);
#endif
    set_nonblock(fd);
    int id = tcp_new(fd);
    kml_tcp *h = tcp_tab[id];
    h->server = h->listening = 1;
    h->conn_inv = inv;
    h->conn_clo = clo;
    tcp_touch(h);
    return id;
}

// Connect to ip:port; onConnect(errno, 0) runs once it completes. Returns
// the handle id, or -errno.
double __kml_native_tcp_connect(const char *ip, double port, void *inv, void *clo) {
    struct sockaddr_storage ss;
    socklen_t len;
    if (tcp_addr(ip, (int)port, &ss, &len) != 0) return -EINVAL;
    int fd = socket(ss.ss_family, SOCK_STREAM, 0);
    if (fd < 0) return -errno;
    set_nonblock(fd);
    int id = tcp_new(fd);
    kml_tcp *h = tcp_tab[id];
    h->connecting = 1;
    h->connect_inv = inv;
    h->connect_clo = clo;
    if (connect(fd, (struct sockaddr *)&ss, len) != 0 && errno != EINPROGRESS && errno != EINTR) {
        h->connecting = 2 + errno; // fails on the next dispatch, as libuv reports it
    }
    tcp_touch(h);
    return id;
}

// A Unix-domain socket address for path; 0 on success.
static int pipe_addr(const char *path, struct sockaddr_un *su) {
    memset(su, 0, sizeof *su);
    su->sun_family = AF_UNIX;
    if (strlen(path) >= sizeof su->sun_path) return ENAMETOOLONG;
    strcpy(su->sun_path, path);
    return 0;
}

// Listen on the Unix-domain socket path (libuv's uv_pipe_bind + listen).
// Returns the handle id, or -errno.
double __kml_native_pipe_listen(const char *path, double backlog, void *inv, void *clo) {
#ifdef _WIN32
    if (us_pipe_name(path)) return us_pipe_listen(path, backlog > 0 ? (int)backlog : 511, inv, clo);
#endif
    struct sockaddr_un su;
    int rc = pipe_addr(path, &su);
    if (rc != 0) return -rc;
    int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0) return -errno;
    if (bind(fd, (struct sockaddr *)&su, sizeof su) != 0 || listen(fd, backlog > 0 ? (int)backlog : 511) != 0) {
        int e = errno;
        close(fd);
        return -e;
    }
#ifdef FD_CLOEXEC
    fcntl(fd, F_SETFD, FD_CLOEXEC);
#endif
    set_nonblock(fd);
    int id = tcp_new(fd);
    kml_tcp *h = tcp_tab[id];
    h->server = h->listening = 1;
    h->conn_inv = inv;
    h->conn_clo = clo;
    h->pipe_path = strdup(path);
    tcp_touch(h);
    return id;
}

// Connect to the Unix-domain socket path; as tcp_connect.
double __kml_native_pipe_connect(const char *path, void *inv, void *clo) {
#ifdef _WIN32
    if (us_pipe_name(path)) return us_pipe_connect(path, inv, clo);
#endif
    struct sockaddr_un su;
    int rc = pipe_addr(path, &su);
    if (rc != 0) return -rc;
    int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0) return -errno;
    set_nonblock(fd);
    int id = tcp_new(fd);
    kml_tcp *h = tcp_tab[id];
    h->connecting = 1;
    h->connect_inv = inv;
    h->connect_clo = clo;
    if (connect(fd, (struct sockaddr *)&su, sizeof su) != 0 && errno != EINPROGRESS && errno != EINTR && errno != EAGAIN) {
        h->connecting = 2 + errno;
    }
    tcp_touch(h);
    return id;
}

// Start delivering reads: onRead(0, n) with n bytes to take, (0, -1) at the
// end of the stream, (errno, 0) on an error.
void __kml_native_tcp_read_start(double id, void *inv, void *clo) {
    kml_tcp *h = tcp_get(id);
    if (!h) return;
    h->reading = 1;
    h->read_inv = inv;
    h->read_clo = clo;
    tcp_touch(h);
}

void __kml_native_tcp_read_stop(double id) {
    kml_tcp *h = tcp_get(id);
    if (h) h->reading = 0;
    tcp_touch(h);
}

// Copy the bytes a read delivered into buf (inside onRead).
double __kml_native_tcp_take(double id, void *buf, int64_t size) {
    kml_tcp *h = tcp_get(id);
    if (!h || !h->rbuf) return 0;
    int64_t n = h->rlen < size ? h->rlen : size;
    memcpy(buf, h->rbuf, (size_t)n);
    return (double)n;
}

// Send the queued writes; *progressed says whether any byte went out.
static int tcp_flush(kml_tcp *h, int *progressed) {
    while (h->wq_head) {
        kml_wreq *w = h->wq_head;
        while (w->off < w->len) {
            int64_t n = tcp_send_req(h, w);
            if (n < 0) {
                if (n == -EAGAIN) return 0;
                return (int)-n;
            }
            w->off += n;
            *progressed = 1;
        }
        h->wq_head = w->next;
        if (!h->wq_head) h->wq_tail = NULL;
        *progressed = 1;
        if (w->inv) TCP_CALL(w->inv, w->clo, 0, 0);
        free(w->data);
        free(w);
    }
    return 0;
}

// Write len bytes of buf from offset. Returns 1 when they were all written
// now (the caller runs its callback itself, as Node's write does when the
// request completes synchronously), 0 when queued (onWritten(errno, 0)
// runs later), or -errno.
static double tcp_write_fd(double id, void *buf, int64_t size, double offset, double length, int sendfd, void *inv, void *clo);

double __kml_native_tcp_write(double id, void *buf, int64_t size, double offset, double length, void *inv, void *clo) {
    return tcp_write_fd(id, buf, size, offset, length, -1, inv, clo);
}

double __kml_native_tcp_fileno(double id) {
    kml_tcp *h = tcp_get(id);
    return h && h->fd >= 0 ? h->fd : -1;
}

// An IPC pipe (Node's uv_pipe_init with ipc set): reads collect the
// descriptors sent with the bytes.
void __kml_native_ipc_open(double id) {
    kml_tcp *h = tcp_get(id);
    if (h) h->ipc = 1;
}

// The oldest descriptor received on the IPC pipe, or -1 (uv_accept of the
// pending handle).
double __kml_native_ipc_take_fd(double id) {
    kml_tcp *h = tcp_get(id);
    if (!h || h->ipc_nfds == 0) return -1;
    int fd = h->ipc_fds[0];
    memmove(h->ipc_fds, h->ipc_fds + 1, (size_t)(h->ipc_nfds - 1) * sizeof(int));
    h->ipc_nfds--;
    return fd;
}

// A write carrying the stream handle's descriptor (uv_write2): as
// tcp_write, the descriptor sent with the first byte; on Windows the socket
// duplicated for the peer, framed ahead of the bytes (ipc_write_frame).
double __kml_native_ipc_write_handle(double id, void *buf, int64_t size, double offset, double length, double handle, void *inv, void *clo) {
    kml_tcp *src = tcp_get(handle);
    if (!src || src->fd < 0) return -EBADF;
#ifdef _WIN32
    return ipc_write_frame(id, buf, size, offset, length, src->fd, inv, clo);
#else
    return tcp_write_fd(id, buf, size, offset, length, src->fd, inv, clo);
#endif
}

static double tcp_write_fd(double id, void *buf, int64_t size, double offset, double length, int sendfd, void *inv, void *clo) {
    kml_tcp *h = tcp_get(id);
    if (!h || h->fd < 0) return -EBADF;
    int64_t off = (int64_t)offset, len = (int64_t)length;
    if (off < 0) off = 0;
    if (off > size) off = size;
    if (len > size - off) len = size - off;
    const char *src = (const char *)buf + off;
    int64_t done = 0;
    if (!h->wq_head && !h->connecting && h->tls_state != 1) {
        while (done < len) {
#ifndef _WIN32
            int64_t n = sendfd >= 0 ? ipc_send_fd(h, src + done, len - done, sendfd) : tcp_send(h, src + done, len - done);
#else
            int64_t n = tcp_send(h, src + done, len - done);
#endif
            if (n < 0) {
                if (n == -EAGAIN) break;
                return n;
            }
            if (n > 0) sendfd = -1;
            done += n;
        }
        if (done == len) {
            if (h->cout_len > 0) tcp_touch(h);
            return 1;
        }
    }
    kml_wreq *w = (kml_wreq *)calloc(1, sizeof *w);
    w->sendfd = sendfd;
    w->len = len - done;
    w->data = (char *)malloc((size_t)w->len + 1);
    memcpy(w->data, src + done, (size_t)w->len);
    w->inv = inv;
    w->clo = clo;
    if (h->wq_tail) h->wq_tail->next = w; else h->wq_head = w;
    h->wq_tail = w;
    tcp_touch(h);
    return 0;
}

// Half-close once the queued writes are out; onShutdown(errno, 0).
void __kml_native_tcp_shutdown(double id, void *inv, void *clo) {
    kml_tcp *h = tcp_get(id);
    if (!h) return;
    h->shut_pending = 1;
    h->shut_inv = inv;
    h->shut_clo = clo;
    tcp_touch(h);
}

// Close the handle; onClose(0, 0) runs on the next dispatch.
void __kml_native_tcp_close(double id, void *inv, void *clo) {
    kml_tcp *h = tcp_get(id);
    if (!h || h->closing) return;
    h->closing = 1;
    h->reading = 0;
    h->close_inv = inv;
    h->close_clo = clo;
    if (h->tls && tcp_tls) tcp_tls->free(h->tls);
    h->tls = NULL;
    free(h->cout);
    h->cout = NULL;
    h->cout_len = h->cout_cap = 0;
    tcp_unwatch(h);
#ifdef _WIN32
    if (h->us) us_close(h);
    else
#endif
    if (h->fd >= 0) close(h->fd);
    h->fd = -1;
    for (int i = 0; i < h->ipc_nfds; i++) close(h->ipc_fds[i]);
    free(h->ipc_fds);
#ifdef _WIN32
    free(h->ipc_frame);
    h->ipc_frame = NULL;
    h->ipc_frame_len = 0;
#endif
    h->ipc_fds = NULL;
    h->ipc_nfds = h->ipc_cap = 0;
    if (h->pipe_path) {
        unlink(h->pipe_path);
        free(h->pipe_path);
        h->pipe_path = NULL;
    }
    tcp_touch(h);
}

void __kml_native_tcp_set_no_delay(double id, _Bool on) {
    kml_tcp *h = tcp_get(id);
    int v = on ? 1 : 0;
    if (h && h->fd >= 0) setsockopt(h->fd, IPPROTO_TCP, TCP_NODELAY, &v, sizeof v);
}

void __kml_native_tcp_set_keep_alive(double id, _Bool on, double delaySecs) {
    kml_tcp *h = tcp_get(id);
    if (!h || h->fd < 0) return;
    int v = on ? 1 : 0;
    setsockopt(h->fd, SOL_SOCKET, SO_KEEPALIVE, &v, sizeof v);
#if defined(TCP_KEEPIDLE)
    if (on && delaySecs > 0) { int d = (int)delaySecs; setsockopt(h->fd, IPPROTO_TCP, TCP_KEEPIDLE, &d, sizeof d); }
#elif defined(TCP_KEEPALIVE)
    if (on && delaySecs > 0) { int d = (int)delaySecs; setsockopt(h->fd, IPPROTO_TCP, TCP_KEEPALIVE, &d, sizeof d); }
#endif
}

void __kml_native_tcp_ref(double id, _Bool on) {
    kml_tcp *h = tcp_get(id);
    if (h) h->refd = on ? 1 : 0;
    tcp_touch(h);
}

// The local (peer false) or remote address: port + 65536 * family (4 or 6),
// the address as the last string; -errno on failure.
double __kml_native_tcp_address(double id, _Bool peer) {
    kml_tcp *h = tcp_get(id);
    if (!h || h->fd < 0) return -EBADF;
    struct sockaddr_storage ss;
    socklen_t len = sizeof ss;
    int rc = peer ? getpeername(h->fd, (struct sockaddr *)&ss, &len) : getsockname(h->fd, (struct sockaddr *)&ss, &len);
    if (rc != 0) return -errno;
    if (ss.ss_family != AF_INET && ss.ss_family != AF_INET6) return -EAFNOSUPPORT; // a pipe
    char buf[64] = {0};
    int port, fam;
    if (ss.ss_family == AF_INET6) {
        struct sockaddr_in6 *a = (struct sockaddr_in6 *)&ss;
        inet_ntop(AF_INET6, &a->sin6_addr, buf, sizeof buf);
        port = ntohs(a->sin6_port);
        fam = 6;
    } else {
        struct sockaddr_in *a = (struct sockaddr_in *)&ss;
        inet_ntop(AF_INET, &a->sin_addr, buf, sizeof buf);
        port = ntohs(a->sin_port);
        fam = 4;
    }
    native_set_last_string(buf);
    return port + 65536.0 * fam;
}

// ---- UDP handles (Node's udp_wrap) --------------------------------------------
// A datagram socket is a handle in the same table: read with tcpReadStart
// (each onRead is one datagram, taken with tcpTake, its sender read with
// udpSender), closed with tcpClose, referenced with tcpRef.

// A datagram socket of family 4 or 6: the handle id, or -errno.
double __kml_native_udp_socket(double family) {
    int fd = socket(family == 6 ? AF_INET6 : AF_INET, SOCK_DGRAM, 0);
    if (fd < 0) return -errno;
#ifdef FD_CLOEXEC
    fcntl(fd, F_SETFD, FD_CLOEXEC);
#endif
    set_nonblock(fd);
    int id = tcp_new(fd);
    tcp_tab[id]->udp = 1;
    return id;
}

// Bind to ip:port. flags: bit 0 SO_REUSEADDR, bit 1 IPV6_V6ONLY. 0, or -errno.
double __kml_native_udp_bind(double id, const char *ip, double port, double flags) {
    kml_tcp *h = tcp_get(id);
    if (!h || h->fd < 0) return -EBADF;
    struct sockaddr_storage ss;
    socklen_t len;
    if (tcp_addr(ip, (int)port, &ss, &len) != 0) return -EINVAL;
    int f = (int)flags, one = 1;
    if (f & 1) setsockopt(h->fd, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
#if defined(SO_REUSEPORT) && !defined(_WIN32)
    if (f & 1) setsockopt(h->fd, SOL_SOCKET, SO_REUSEPORT, &one, sizeof one);
#endif
#ifndef _WIN32
    if (ss.ss_family == AF_INET6) {
        int v6 = (f & 2) ? 1 : 0;
        setsockopt(h->fd, IPPROTO_IPV6, IPV6_V6ONLY, &v6, sizeof v6);
    }
#endif
    if (bind(h->fd, (struct sockaddr *)&ss, len) != 0) return -errno;
    return 0;
}

// Connect (a default destination) to ip:port, or dissolve it (ip ""). 0, or
// -errno.
double __kml_native_udp_connect(double id, const char *ip, double port) {
    kml_tcp *h = tcp_get(id);
    if (!h || h->fd < 0) return -EBADF;
    struct sockaddr_storage ss;
    socklen_t len;
    if (!ip || !*ip) {
        memset(&ss, 0, sizeof ss);
#ifdef AF_UNSPEC
        ss.ss_family = AF_UNSPEC;
#endif
        len = sizeof(struct sockaddr_in);
        if (connect(h->fd, (struct sockaddr *)&ss, len) != 0 && errno != EAFNOSUPPORT) return -errno;
        return 0;
    }
    if (tcp_addr(ip, (int)port, &ss, &len) != 0) return -EINVAL;
    if (connect(h->fd, (struct sockaddr *)&ss, len) != 0) return -errno;
    return 0;
}

// Send bytes [offset, offset + length) to ip:port (ip "" when connected):
// the count sent, or -errno. A datagram goes out whole or not at all; a full
// send buffer is waited out, as libuv's queued send would be.
double __kml_native_udp_send(double id, void *buf, int64_t size, double offset, double length, const char *ip, double port) {
    kml_tcp *h = tcp_get(id);
    if (!h || h->fd < 0) return -EBADF;
    int64_t off = (int64_t)offset, n = (int64_t)length;
    if (off < 0 || n < 0 || off + n > size) return -EINVAL;
    struct sockaddr_storage ss;
    socklen_t len = 0;
    int to = ip && *ip;
    if (to && tcp_addr(ip, (int)port, &ss, &len) != 0) return -EINVAL;
    for (;;) {
        int64_t w = to ? sendto(h->fd, (const char *)buf + off, (size_t)n, 0, (struct sockaddr *)&ss, len)
                       : sendto(h->fd, (const char *)buf + off, (size_t)n, 0, NULL, 0);
        if (w >= 0) return (double)w;
        if (errno == EINTR) continue;
        if (errno == EAGAIN || errno == EWOULDBLOCK) {
            struct pollfd pfd = {h->fd, POLLOUT, 0};
            poll(&pfd, 1, -1);
            continue;
        }
        return -errno;
    }
}

// The sender of the datagram being read: its port + 65536 * family (4 or 6),
// its address left for lastString.
double __kml_native_udp_sender(double id) {
    kml_tcp *h = tcp_get(id);
    if (!h) return -EBADF;
    char abuf[64] = {0};
    int port, fam;
    if (h->from.ss_family == AF_INET6) {
        struct sockaddr_in6 *a = (struct sockaddr_in6 *)&h->from;
        inet_ntop(AF_INET6, &a->sin6_addr, abuf, sizeof abuf);
        port = ntohs(a->sin6_port);
        fam = 6;
    } else {
        struct sockaddr_in *a = (struct sockaddr_in *)&h->from;
        inet_ntop(AF_INET, &a->sin_addr, abuf, sizeof abuf);
        port = ntohs(a->sin_port);
        fam = 4;
    }
    native_set_last_string(abuf);
    return port + 65536.0 * fam;
}

#ifndef _WIN32
#include <net/if.h>
#endif

// A socket option: 0 SO_BROADCAST, 1 unicast TTL, 2 multicast TTL, 3
// multicast loopback, 4 SO_RCVBUF, 5 SO_SNDBUF (value < 0 reads it). The
// value read, 0 when set, or -errno.
double __kml_native_udp_option(double id, double which, double value) {
    kml_tcp *h = tcp_get(id);
    if (!h || h->fd < 0) return -EBADF;
    struct sockaddr_storage ss;
    socklen_t sl = sizeof ss;
    int v6 = getsockname(h->fd, (struct sockaddr *)&ss, &sl) == 0 && ss.ss_family == AF_INET6;
    int level = SOL_SOCKET, opt;
    switch ((int)which) {
    case 0: opt = SO_BROADCAST; break;
#ifndef _WIN32
    case 1: level = v6 ? IPPROTO_IPV6 : IPPROTO_IP; opt = v6 ? IPV6_UNICAST_HOPS : IP_TTL; break;
    case 2: level = v6 ? IPPROTO_IPV6 : IPPROTO_IP; opt = v6 ? IPV6_MULTICAST_HOPS : IP_MULTICAST_TTL; break;
    case 3: level = v6 ? IPPROTO_IPV6 : IPPROTO_IP; opt = v6 ? IPV6_MULTICAST_LOOP : IP_MULTICAST_LOOP; break;
#else
    // Winsock's numbers: IPPROTO_IP 0 (IP_TTL 4, IP_MULTICAST_TTL 10,
    // IP_MULTICAST_LOOP 11), IPPROTO_IPV6 41 (IPV6_UNICAST_HOPS 4,
    // IPV6_MULTICAST_HOPS 10, IPV6_MULTICAST_LOOP 11).
    case 1: level = v6 ? 41 : 0; opt = 4; break;
    case 2: level = v6 ? 41 : 0; opt = 10; break;
    case 3: level = v6 ? 41 : 0; opt = 11; break;
#endif
    case 4: opt = SO_RCVBUF; break;
    case 5: opt = SO_SNDBUF; break;
    default: return -EINVAL;
    }
    if (value < 0) {
        int v = 0;
        socklen_t vl = sizeof v;
        if (getsockopt(h->fd, level, opt, &v, &vl) != 0) return -errno;
        return v;
    }
#ifndef _WIN32
    if (!v6 && level == IPPROTO_IP && (opt == IP_MULTICAST_TTL || opt == IP_MULTICAST_LOOP)) {
        unsigned char c = (unsigned char)(int)value;
        if (setsockopt(h->fd, level, opt, &c, sizeof c) != 0) return -errno;
        return 0;
    }
#endif
    int v = (int)value;
    if (setsockopt(h->fd, level, opt, &v, sizeof v) != 0) return -errno;
    return 0;
}

// Join (add) or leave a multicast group on an interface address ("" any).
// 0, or -errno.
double __kml_native_udp_membership(double id, const char *group, const char *iface, _Bool add) {
    kml_tcp *h = tcp_get(id);
    if (!h || h->fd < 0) return -EBADF;
#ifndef _WIN32
    if (strchr(group, ':')) {
        struct ipv6_mreq m;
        memset(&m, 0, sizeof m);
        if (inet_pton(AF_INET6, group, &m.ipv6mr_multiaddr) != 1) return -EINVAL;
        if (iface && *iface) m.ipv6mr_interface = if_nametoindex(iface);
        if (setsockopt(h->fd, IPPROTO_IPV6, add ? IPV6_JOIN_GROUP : IPV6_LEAVE_GROUP, &m, sizeof m) != 0) return -errno;
        return 0;
    }
    struct ip_mreq m;
    memset(&m, 0, sizeof m);
    if (inet_pton(AF_INET, group, &m.imr_multiaddr) != 1) return -EINVAL;
    if (iface && *iface && inet_pton(AF_INET, iface, &m.imr_interface) != 1) return -EINVAL;
    if (setsockopt(h->fd, IPPROTO_IP, add ? IP_ADD_MEMBERSHIP : IP_DROP_MEMBERSHIP, &m, sizeof m) != 0) return -errno;
    return 0;
#else
    // libuv on Windows refuses a group of the other family (EINVAL).
    struct sockaddr_storage ss;
    socklen_t sl = sizeof ss;
    int v6 = getsockname(h->fd, (struct sockaddr *)&ss, &sl) == 0 && ss.ss_family == AF_INET6;
    if (v6 != (strchr(group, ':') != 0)) return -EINVAL;
    if (v6) {
        // Winsock's ipv6_mreq; the interface is the scope of an address
        // ("::%3"), as libuv's uv_ip6_addr reads it on Windows.
        struct { struct in6_addr multi; unsigned long iface; } m6;
        memset(&m6, 0, sizeof m6);
        if (inet_pton(AF_INET6, group, &m6.multi) != 1) return -EINVAL;
        const char *pct = iface ? strchr(iface, '%') : 0;
        if (pct) m6.iface = strtoul(pct + 1, 0, 10);
        if (setsockopt(h->fd, 41, add ? 12 : 13, &m6, sizeof m6) != 0) return -errno; // IPV6_ADD/DROP_MEMBERSHIP
        return 0;
    }
    struct { struct in_addr multi, iface; } m;
    memset(&m, 0, sizeof m);
    if (inet_pton(AF_INET, group, &m.multi) != 1) return -EINVAL;
    if (iface && *iface && inet_pton(AF_INET, iface, &m.iface) != 1) return -EINVAL;
    if (setsockopt(h->fd, 0, add ? 12 : 13, &m, sizeof m) != 0) return -errno; // IP_ADD/DROP_MEMBERSHIP
    return 0;
#endif
}

// The outgoing multicast interface (an address; IPv6: an interface name or
// scope). 0, or -errno.
double __kml_native_udp_multicast_interface(double id, const char *iface) {
    kml_tcp *h = tcp_get(id);
    if (!h || h->fd < 0) return -EBADF;
#ifndef _WIN32
    struct sockaddr_storage ss;
    socklen_t sl = sizeof ss;
    if (getsockname(h->fd, (struct sockaddr *)&ss, &sl) == 0 && ss.ss_family == AF_INET6) {
        const char *pct = strchr(iface, '%');
        unsigned int idx = if_nametoindex(pct ? pct + 1 : iface);
        if (setsockopt(h->fd, IPPROTO_IPV6, IPV6_MULTICAST_IF, &idx, sizeof idx) != 0) return -errno;
        return 0;
    }
    struct in_addr a;
    if (inet_pton(AF_INET, iface, &a) != 1) return -EINVAL;
    if (setsockopt(h->fd, IPPROTO_IP, IP_MULTICAST_IF, &a, sizeof a) != 0) return -errno;
    return 0;
#else
    struct sockaddr_storage ss;
    socklen_t sl = sizeof ss;
    if (getsockname(h->fd, (struct sockaddr *)&ss, &sl) == 0 && ss.ss_family == AF_INET6) {
        // The interface is the address's scope ("::%3"), as on libuv.
        const char *pct = strchr(iface, '%');
        unsigned long idx = pct ? strtoul(pct + 1, 0, 10) : 0;
        if (setsockopt(h->fd, 41, 9, &idx, sizeof idx) != 0) return -errno; // IPV6_MULTICAST_IF
        return 0;
    }
    struct in_addr a;
    if (inet_pton(AF_INET, iface, &a) != 1) return -EINVAL;
    if (setsockopt(h->fd, 0, 9, &a, sizeof a) != 0) return -errno; // IP_MULTICAST_IF
    return 0;
#endif
}

static int tcp_active(kml_tcp *h) {
    return !h->closed_notified && (h->fd >= 0 || h->closing);
}

static _Bool proc_dispatch(void);
static _Bool proc_dispatch_unrefd(void);


// Run what became ready on every handle. Returns whether anything ran.
static _Bool tcp_dispatch(void) {
    // An unref'd child's exit waits for a pass in which the loop is still
    // running before its streams close; when they are what held the loop,
    // the loop ends first and the exit goes unreported, as libuv's does.
    _Bool ran = proc_dispatch_unrefd();
    for (int i = 0; i < tcp_n; i++) {
        kml_tcp *h = tcp_tab[i];
        if (h->closing) {
            if (!h->closed_notified) {
                h->closed_notified = 1;
                ran = 1;
                if (h->close_inv) TCP_CALL(h->close_inv, h->close_clo, 0, 0);
            }
            continue;
        }
        if (h->fd < 0) continue;
        // What the watcher reported since the last dispatch; a descriptor
        // libuv cannot watch is always ready, and the operation answers.
        int rev = h->unpollable ? (UV_READABLE | UV_WRITABLE) : h->rev;
#ifdef _WIN32
        if (h->us) rev |= us_ready(h->us);
#endif
        h->rev = 0;
        int readable = (rev & UV_READABLE) != 0;
        int writable = (rev & UV_WRITABLE) != 0;
        if (h->listening) {
            if (!readable) continue;
            for (;;) {
#ifdef _WIN32
                if (h->us) {
                    kml_ustream *c = us_take_conn(h->us);
                    if (!c) break;
                    int cid = tcp_new_us(c);
                    h = tcp_tab[i];
                    ran = 1;
                    TCP_CALL(h->conn_inv, h->conn_clo, 0, cid);
                    h = tcp_tab[i];
                    if (h->closing || h->fd < 0) break;
                    continue;
                }
#endif
                int c = accept(h->fd, NULL, NULL);
                if (c < 0) break;
                set_nonblock(c);
                int cid = tcp_new(c);
                h = tcp_tab[i];
                ran = 1;
                TCP_CALL(h->conn_inv, h->conn_clo, 0, cid);
                h = tcp_tab[i];
                if (h->closing || h->fd < 0) break;
            }
            continue;
        }
        if (h->connecting) {
            int err = 0;
            if (h->connecting > 1) err = h->connecting - 2;
            else if (writable) {
                socklen_t el = sizeof err;
                getsockopt(h->fd, SOL_SOCKET, SO_ERROR, &err, &el);
            } else continue;
            h->connecting = 0;
            ran = 1;
            TCP_CALL(h->connect_inv, h->connect_clo, err, 0);
            h = tcp_tab[i];
            if (h->fd < 0) continue;
        }
        if (h->cout_len > 0 && writable && tls_push(h) < 0) ran = 1;
        if (h->tls_state == 1 && tcp_tls) {
            tls_pull(h);
            int rc = tcp_tls->handshake(h->tls);
            if (tls_push(h) < 0 && rc == 0) rc = -1;
            if (rc == 0 && h->raw_eof) rc = -1;
            if (rc == 0) continue;
            h->tls_state = rc > 0 ? 2 : 3;
            ran = 1;
            if (h->sec_inv) TCP_CALL(h->sec_inv, h->sec_clo, rc > 0 ? 0 : -rc, 0);
            h = tcp_tab[i];
            if (h->fd < 0 || h->tls_state != 2) continue;
            writable = 1;
            readable = 1;
        }
        if (h->tls_state == 3) continue;
        if (h->wq_head && writable) {
            int progressed = 0;
            int err = tcp_flush(h, &progressed);
            if (err || progressed) ran = 1;
            if (err) {
                kml_wreq *w = h->wq_head;
                h->wq_head = h->wq_tail = NULL;
                while (w) {
                    kml_wreq *nx = w->next;
                    if (w->inv) TCP_CALL(w->inv, w->clo, err, 0);
                    free(w->data);
                    free(w);
                    w = nx;
                }
            }
            h = tcp_tab[i];
            if (h->fd < 0) continue;
        }
        // A TLS half-close sends close_notify first, and waits for it to be
        // written.
        if (h->shut_pending && !h->wq_head && h->cout_len == 0 && h->tls && tcp_tls && !h->tls_notified) {
            h->tls_notified = 1;
            tcp_tls->shutdown(h->tls);
            tls_push(h);
        }
        if (h->shut_pending && !h->wq_head && h->cout_len == 0) {
            h->shut_pending = 0;
            int err;
#ifdef _WIN32
            if (h->us) {
                // libuv's: after the writes, the write end of the pipe is done
                uv_shutdown_t *sr = (uv_shutdown_t *)malloc(sizeof *sr);
                int r = uv_shutdown(sr, h->us->s, us_on_shutdown);
                if (r != 0) free(sr);
                err = r == 0 || r == UV_ENOTCONN ? 0 : us_errno(r);
            } else
#endif
            err = shutdown(h->fd, SHUT_WR) == 0 ? 0 : errno;
            ran = 1;
            if (h->shut_inv) TCP_CALL(h->shut_inv, h->shut_clo, err, 0);
            h = tcp_tab[i];
            if (h->fd < 0) continue;
        }
        if (h->tls && tcp_tls && tcp_tls->pending(h->tls) > 0) readable = 1;
        while (h->udp && h->reading && readable && h->fd >= 0) {
            char buf[65536];
            socklen_t fl = sizeof h->from;
            int64_t n = recvfrom(h->fd, buf, sizeof buf, 0, (struct sockaddr *)&h->from, &fl);
            if (n < 0) {
                if (errno == EWOULDBLOCK || errno == EAGAIN || errno == EINTR) break;
                int e = errno;
                ran = 1;
                TCP_CALL(h->read_inv, h->read_clo, e, 0);
                h = tcp_tab[i];
                break;
            }
            ran = 1;
            h->rbuf = buf;
            h->rlen = n;
            TCP_CALL(h->read_inv, h->read_clo, 0, (double)n);
            h = tcp_tab[i];
            h->rbuf = NULL;
            h->rlen = 0;
        }
        if (h->udp) continue;
        while (h->reading && !h->read_eof && readable && h->fd >= 0) {
            char buf[65536];
            int64_t n = tcp_recv(h, buf, sizeof buf);
            if (n < 0) {
                if (n == -EAGAIN) break;
                int e = (int)-n;
                ran = 1;
                h->read_eof = 1;
                TCP_CALL(h->read_inv, h->read_clo, e, 0);
                break;
            }
            ran = 1;
            if (n == 0) {
                h->read_eof = 1;
                TCP_CALL(h->read_inv, h->read_clo, 0, -1);
                break;
            }
            h->rbuf = buf;
            h->rlen = n;
            TCP_CALL(h->read_inv, h->read_clo, 0, (double)n);
            h = tcp_tab[i];
            h->rbuf = NULL;
            h->rlen = 0;
            // A TLS session may hold more records than one read returned.
            if (n < (int64_t)sizeof buf && !h->tls) break;
        }
    }
    // A child's exit after its streams, as libuv's readiness order has it:
    // a process's descriptors close before it is reaped, so the end of its
    // pipes (an IPC channel's disconnect) comes before its 'exit'.
    if (proc_dispatch()) ran = 1;
    return ran;
}

// ---- Process handles (Node's process_wrap, libuv's uv_spawn) ----------------
// A child process is an id into a process-wide table. Its stdio pipes are
// stream handles (the TCP table above: POSIX socketpairs, Windows libuv's
// UV_CREATE_PIPE named pipes), read back with processStdio after the spawn.
// The child is a libuv uv_process_t on the thread's loop (TDD-00244 Stage 4,
// TDD-00252 Stage 3), whose exit callback records the exit; the dispatch
// runs onExit(exitCode, signal).
#ifndef _WIN32
#include <signal.h>
#include <sys/wait.h>
#include <uv.h>
extern char **environ;
// loop.c: the thread's libuv loop and the allocator libuv handles live in.
extern uv_loop_t *__kml_loop_uv(void);
extern void *__kml_loop_raw_alloc(size_t n);
extern void __kml_loop_free_handle(uv_handle_t *h);
#else
extern int kill(int pid, int sig);
extern int socketpair(int domain, int type, int protocol, int sv[2]);
#endif

#define KML_MAX_STDIO_H 32
typedef struct kml_proch {
    int pid;
    int alive, refd;
    void *exit_inv, *exit_clo;
    // An unref'd child's exit waits a pass (libuv's unref'd child watcher):
    // when nothing else holds the loop open it ends first, and the exit is
    // never reported.
    int pending;
    double pending_code, pending_sig;
    int stdio[KML_MAX_STDIO_H]; // its stdio stream handles (-1 none)
    uv_process_t *uvp;          // until the exit callback closes it
    int exited;                 // the exit callback ran: ex_code, ex_sig
    double ex_code, ex_sig;
} kml_proch;

static __thread kml_proch **proc_tab = NULL;
static __thread int proc_cap = 0, proc_n = 0;
#define KML_MAX_STDIO 32
static int proc_last_stdio[KML_MAX_STDIO];

// libuv reaped the child: record its exit for the dispatch, and close the
// handle. On Windows status is the full 32-bit exit code (0xC0000005 for an
// access violation), as Node reports it.
static void proc_on_exit(uv_process_t *h, int64_t status, int term_signal) {
    kml_proch *p = (kml_proch *)h->data;
    if (p) {
        p->exited = 1;
#ifdef _WIN32
        p->ex_code = (double)(uint32_t)status;
#else
        p->ex_code = (double)status;
#endif
        p->ex_sig = (double)term_signal;
        p->uvp = NULL;
    }
    uv_close((uv_handle_t *)h, __kml_loop_free_handle);
}

static kml_proch *proc_get(double id) {
    int i = (int)id;
    return (i >= 0 && i < proc_n) ? proc_tab[i] : NULL;
}

static int proc_new(int pid, void *inv, void *clo) {
    if (proc_n == proc_cap) {
        int nc = proc_cap ? proc_cap * 2 : 8;
        kml_proch **nt = (kml_proch **)calloc((size_t)nc, sizeof *nt);
        if (proc_tab) memcpy(nt, proc_tab, (size_t)proc_cap * sizeof *nt);
        proc_tab = nt;
        proc_cap = nc;
    }
    kml_proch *p = (kml_proch *)calloc(1, sizeof *p);
    p->pid = pid;
    p->alive = 1;
    p->refd = 1;
    p->exit_inv = inv;
    p->exit_clo = clo;
    proc_tab[proc_n] = p;
    tcp_loop_init(); // the dispatch reports the exit
    return proc_n++;
}

// The NUL-separated strings of buf, as a NULL-terminated vector.
static char **proc_strings(const char *buf, int64_t size) {
    int n = 0;
    for (int64_t i = 0; i < size; i++)
        if (buf[i] == 0) n++;
    char **v = (char **)calloc((size_t)n + 1, sizeof *v);
    int64_t start = 0;
    int k = 0;
    for (int64_t i = 0; i < size; i++) {
        if (buf[i] == 0) {
            v[k++] = strdup(buf + start);
            start = i + 1;
        }
    }
    return v;
}

static void proc_free_strings(char **v) {
    if (!v) return;
    for (char **q = v; *q; q++) free(*q);
    free(v);
}

// The stdio spec, comma-separated per descriptor: "p" a pipe, "c" the IPC
// channel (a pipe too), "i" ignored (/dev/null), or a decimal fd the child
// inherits at that position. Returns the count, or -1.
static int proc_stdio_parse(const char *spec, char *kind, int *fd) {
    int n = 0;
    const char *q = spec ? spec : "";
    while (*q && n < KML_MAX_STDIO) {
        if (*q == 'p' || *q == 'c' || *q == 'i') {
            kind[n] = *q;
            fd[n] = -1;
            q++;
        } else if (*q >= '0' && *q <= '9') {
            int v = 0;
            while (*q >= '0' && *q <= '9') v = v * 10 + (*q++ - '0');
            kind[n] = 'f';
            fd[n] = v;
        } else {
            return -1;
        }
        n++;
        if (*q == ',') q++;
    }
    return n;
}

#ifndef _WIN32
#if defined(__APPLE__)
#include <mach-o/dyld.h>
#endif
#include <limits.h>
// Whether file (as execvp would find it) is this running executable: a
// compiled program has no interpreter mode, so a child spawned from
// process.execPath would re-run the program body and spawn again. Such a
// child is marked (KML_KLAIN_REEXEC) and its startup guard refuses to run
// (ADR-00972). fork, a deliberate self-run, is not marked.
static int proc_is_self(const char *file) {
    char self[4096], rself[PATH_MAX], rfile[PATH_MAX];
#if defined(__APPLE__)
    uint32_t sz = (uint32_t)sizeof self;
    if (_NSGetExecutablePath(self, &sz) != 0) return 0;
#else
    ssize_t n = readlink("/proc/self/exe", self, sizeof self - 1);
    if (n < 0) return 0;
    self[n] = 0;
#endif
    const char *sr = realpath(self, rself) ? rself : self;
    const char *fr = file;
    char found[PATH_MAX];
    if (!strchr(file, '/')) {
        // A bare name: its first match on PATH.
        const char *path = getenv("PATH");
        fr = NULL;
        while (path && *path) {
            const char *end = strchr(path, ':');
            size_t dl = end ? (size_t)(end - path) : strlen(path);
            if (dl + strlen(file) + 2 < sizeof found) {
                memcpy(found, path, dl);
                found[dl] = '/';
                strcpy(found + dl + 1, file);
                if (access(found, X_OK) == 0) {
                    fr = found;
                    break;
                }
            }
            path = end ? end + 1 : NULL;
        }
        if (!fr) return 0;
    }
    const char *fres = realpath(fr, rfile) ? rfile : fr;
    return strcmp(sr, fres) == 0;
}

// The value of name in env (the child's given environment), or in ours.
static const char *proc_env_value(char **env, const char *name, size_t nl) {
    if (!env) {
        char buf[256];
        if (nl >= sizeof buf) return NULL;
        memcpy(buf, name, nl);
        buf[nl] = 0;
        return getenv(buf);
    }
    for (int i = 0; env[i]; i++)
        if (strncmp(env[i], name, nl) == 0 && env[i][nl] == '=') return env[i] + nl + 1;
    return NULL;
}

// Whether a shell spawn (`sh -c <command>`, as exec and shell: true run)
// starts this executable: a command word that names it, directly or through
// a variable ("$NODE" with NODE set to process.execPath). The shell itself is
// then marked, and passes the mark on to the copy it starts.
static int proc_shell_runs_self(char **argv, char **env) {
    if (!argv || !argv[0] || !argv[1] || strcmp(argv[1], "-c") != 0 || !argv[2]) return 0;
    const char *p = argv[2];
    int cmdpos = 1;
    while (*p) {
        if (strchr(" \t\n", *p)) { p++; continue; }
        if (strchr("|;&()", *p)) { cmdpos = 1; p++; continue; }
        char word[PATH_MAX];
        size_t wl = 0;
        char quote = 0;
        while (*p && (quote || !strchr(" \t\n|;&()<>", *p))) {
            if (quote ? *p == quote : (*p == '"' || *p == '\'')) {
                quote = quote ? 0 : *p;
            } else if (wl + 1 < sizeof word) {
                word[wl++] = *p;
            }
            p++;
        }
        word[wl] = 0;
        if (*p == '<' || *p == '>') p++;
        if (!cmdpos || wl == 0) continue;
        cmdpos = 0;
        const char *target = word;
        if (word[0] == '$') {
            const char *name = word + 1;
            size_t nl = strlen(name);
            if (*name == '{' && nl > 1 && name[nl - 1] == '}') { name++; nl -= 2; }
            target = proc_env_value(env, name, nl);
        }
        if (target && *target && proc_is_self(target)) return 1;
    }
    return 0;
}
#endif

// The spawn, on a given libuv loop: the thread's, or spawnSync's own.
typedef uv_loop_t proc_loop;
#ifdef _WIN32
// win32proc.c: Node's refusal of a batch file without a shell, the
// self-spawn test, and a shim descriptor as a CRT one for uv_spawn.
extern int __kml_win_is_batch_file(const char *file);
extern int __kml_win_spawn_is_self(const char *file, char **argv, char **env);
extern int __kml_win_fd_crt(int fd);
extern int _close(int fd);
#define proc_uv_errno(r) us_errno(r)
#else
#define proc_uv_errno(r) (-(r))
#endif

// The environment the child gets with the self-spawn marker added: the
// given one, or a copy of ours (on Windows in UTF-8, as libuv's is).
static char **proc_env_marked(char **envp) {
    char **base = envp;
    int n = 0;
#ifdef _WIN32
    uv_env_item_t *items = NULL;
    int count = 0;
    if (!base) {
        if (uv_os_environ(&items, &count) != 0) count = 0;
        base = (char **)calloc((size_t)count + 1, sizeof *base);
        for (int i = 0; i < count; i++) {
            size_t kl = strlen(items[i].name), vl = strlen(items[i].value);
            base[i] = (char *)malloc(kl + vl + 2);
            memcpy(base[i], items[i].name, kl);
            base[i][kl] = '=';
            memcpy(base[i] + kl + 1, items[i].value, vl + 1);
        }
        uv_os_free_environ(items, count);
    }
#else
    if (!base) base = environ;
#endif
    while (base[n]) n++;
    char **marked = (char **)calloc((size_t)n + 2, sizeof *marked);
    for (int i = 0; i < n; i++) marked[i] = strdup(base[i]);
    marked[n] = strdup("KML_KLAIN_REEXEC=1");
#ifdef _WIN32
    if (!envp) proc_free_strings(base);
#endif
    return marked;
}

static double proc_spawn_on(proc_loop *loop, const char *file, void *argv_buf, int64_t argv_size, void *env_buf, int64_t env_size, const char *cwd, double flags, const char *stdio, double uid, double gid, void *inv, void *clo) {
    char kind[KML_MAX_STDIO];
    int want[KML_MAX_STDIO], parent_end[KML_MAX_STDIO], child_end[KML_MAX_STDIO];
    int nst = proc_stdio_parse(stdio, kind, want);
    if (nst < 0) return -EINVAL;
    for (int i = 0; i < KML_MAX_STDIO; i++) proc_last_stdio[i] = -1;
    for (int i = 0; i < nst; i++) parent_end[i] = child_end[i] = -1;
#ifdef _WIN32
    // A pipe is libuv's (UV_CREATE_PIPE, a named pipe the child owns one end
    // of), as Node's stdio pipes are; the IPC channel is a socket pair whose
    // far end the child inherits as a descriptor (UV_INHERIT_FD, through the
    // CRT's descriptor table), as is an inherited descriptor.
    uv_pipe_t *pipes[KML_MAX_STDIO];
    int crt_end[KML_MAX_STDIO];
    for (int i = 0; i < nst; i++) pipes[i] = NULL, crt_end[i] = -1;
#endif
    int err = 0;
    for (int i = 0; i < nst && !err; i++) {
        if (kind[i] == 'p' || kind[i] == 'c') {
#ifdef _WIN32
            if (kind[i] == 'p') {
                pipes[i] = (uv_pipe_t *)__kml_loop_raw_alloc(sizeof *pipes[i]);
                uv_pipe_init(loop, pipes[i], 0);
                continue;
            }
#endif
            int sv[2];
            if (socketpair(AF_UNIX, SOCK_STREAM, 0, sv) != 0) { err = errno; break; }
            parent_end[i] = sv[0];
            child_end[i] = sv[1];
#ifdef _WIN32
            crt_end[i] = __kml_win_fd_crt(sv[1]);
            if (crt_end[i] < 0) { err = errno; break; }
#else
            fcntl(sv[0], F_SETFD, FD_CLOEXEC);
#endif
        } else if (kind[i] == 'f') {
            child_end[i] = want[i];
#ifdef _WIN32
            // Our own 0-2 are the CRT's; libuv answers a missing one (a GUI
            // parent's) with NUL, as Node's child gets.
            if (want[i] == i && i <= 2) crt_end[i] = i;
            else if ((crt_end[i] = __kml_win_fd_crt(want[i])) < 0) { err = errno; break; }
#endif
        }
        // 'i': libuv's UV_IGNORE (/dev/null, NUL)
    }
    char **argv = proc_strings((const char *)argv_buf, argv_size);
    char **envp = env_size > 0 ? proc_strings((const char *)env_buf, env_size) : NULL;
    int pid = -1;
    uv_process_t *uvp = NULL;
#ifdef _WIN32
    // Node refuses a batch file without a shell: cmd.exe would parse its
    // arguments (CVE-2024-27980).
    if (!err && !((int)flags & 2) && __kml_win_is_batch_file(file)) err = EINVAL;
#endif
    if (!err) {
        // A self-spawn is marked for the child's startup guard (ADR-00972),
        // unless this is a deliberate self-run (fork).
#ifdef _WIN32
        int reexec = !((int)flags & 4) && __kml_win_spawn_is_self(file, argv, envp);
#else
        int reexec = !((int)flags & 4) && (proc_is_self(file) || proc_shell_runs_self(argv, envp));
#endif
        char **marked = reexec ? proc_env_marked(envp) : NULL;
        uv_stdio_container_t cont[KML_MAX_STDIO];
        for (int i = 0; i < nst; i++) {
            if (kind[i] == 'i') {
                cont[i].flags = UV_IGNORE;
#ifdef _WIN32
            } else if (pipes[i]) {
                cont[i].flags = (uv_stdio_flags)(UV_CREATE_PIPE | UV_READABLE_PIPE | UV_WRITABLE_PIPE);
                cont[i].data.stream = (uv_stream_t *)pipes[i];
            } else {
                cont[i].flags = UV_INHERIT_FD;
                cont[i].data.fd = crt_end[i];
#else
            } else {
                cont[i].flags = UV_INHERIT_FD;
                cont[i].data.fd = child_end[i];
#endif
            }
        }
        uv_process_options_t o;
        memset(&o, 0, sizeof o);
        o.exit_cb = proc_on_exit;
        o.file = file;
        o.args = argv;
        o.env = marked ? marked : envp;
        o.cwd = cwd && *cwd ? cwd : NULL;
        o.stdio_count = nst;
        o.stdio = cont;
        if ((int)flags & 1) o.flags |= UV_PROCESS_DETACHED;
        if ((int)flags & 2) o.flags |= UV_PROCESS_WINDOWS_VERBATIM_ARGUMENTS;
        if (uid >= 0) {
            o.flags |= UV_PROCESS_SETUID;
            o.uid = (uv_uid_t)uid;
        }
        if (gid >= 0) {
            o.flags |= UV_PROCESS_SETGID;
            o.gid = (uv_gid_t)gid;
        }
        uvp = (uv_process_t *)__kml_loop_raw_alloc(sizeof *uvp);
        int r = uv_spawn(loop, uvp, &o);
        proc_free_strings(marked);
        if (r < 0) {
            // A handle uv_spawn refused is still closed.
            err = proc_uv_errno(r);
            uvp->data = NULL;
            uv_close((uv_handle_t *)uvp, __kml_loop_free_handle);
            uvp = NULL;
        } else {
            pid = uvp->pid;
#ifdef _WIN32
            // The IPC channel's peer, for a socket sent over it (ipc_write_handle).
            for (int i = 0; i < nst; i++)
                if (kind[i] == 'c') __kml_win_ipc_set_peer(parent_end[i], pid);
#endif
        }
    }
    // Close the child's ends, keep ours.
    for (int i = 0; i < nst; i++) {
#ifdef _WIN32
        if (crt_end[i] >= 0 && !(kind[i] == 'f' && crt_end[i] == i)) _close(crt_end[i]);
#endif
        if ((kind[i] == 'p' || kind[i] == 'c') && child_end[i] >= 0) close(child_end[i]);
    }
    proc_free_strings(argv);
    proc_free_strings(envp);
    if (err) {
        for (int i = 0; i < nst; i++) {
            if (parent_end[i] >= 0) close(parent_end[i]);
#ifdef _WIN32
            if (pipes[i]) uv_close((uv_handle_t *)pipes[i], __kml_loop_free_handle);
#endif
        }
        return -(double)err;
    }
    for (int i = 0; i < nst; i++) {
#ifdef _WIN32
        if (pipes[i]) {
            kml_ustream *u = (kml_ustream *)calloc(1, sizeof *u);
            u->s = (uv_stream_t *)pipes[i];
            u->s->data = u;
            proc_last_stdio[i] = tcp_new_us(u);
            continue;
        }
#endif
        if (parent_end[i] < 0) continue;
        set_nonblock(parent_end[i]);
        proc_last_stdio[i] = tcp_new(parent_end[i]);
    }
    int id = proc_new(pid, inv, clo);
    for (int k = 0; k < KML_MAX_STDIO_H; k++) proc_tab[id]->stdio[k] = k < KML_MAX_STDIO ? proc_last_stdio[k] : -1;
    proc_tab[id]->uvp = uvp;
    uvp->data = proc_tab[id];
    return id;
}

// Spawn file with argv (argv[0] the name the child sees) and env (empty:
// ours), in cwd ("" is ours). flags: bit 0 detached, bit 1
// windowsVerbatimArguments, bit 2 a deliberate self-run (fork). uid/gid < 0
// keep ours. Returns the process handle, or -errno when the program could not
// be started. onExit(exitCode, signal) runs on the loop once the child has
// exited.
double __kml_native_process_spawn(const char *file, void *argv_buf, int64_t argv_size, void *env_buf, int64_t env_size, const char *cwd, double flags, const char *stdio, double uid, double gid, void *inv, void *clo) {
    proc_loop *loop = __kml_loop_uv();
    return proc_spawn_on(loop, file, argv_buf, argv_size, env_buf, env_size, cwd, flags, stdio, uid, gid, inv, clo);
}

// The stream handle of the last spawn's descriptor i, or -1.
double __kml_native_process_stdio(double i) {
    int k = (int)i;
    return (k >= 0 && k < KML_MAX_STDIO) ? proc_last_stdio[k] : -1;
}

double __kml_native_process_pid(double id) {
    kml_proch *p = proc_get(id);
    return p ? p->pid : 0;
}

// Send signal to the child: 0, or -errno (ESRCH once it has been reaped).
double __kml_native_process_kill(double id, double signal) {
    kml_proch *p = proc_get(id);
    if (!p) return -EBADF;
    if (!p->alive) return -ESRCH;
    if (p->exited) return -ESRCH; // reaped: the pid may be another process's
#ifdef _WIN32
    // libuv's: SIGTERM, SIGKILL, SIGINT and SIGQUIT end the process (the
    // exit reports the signal), 0 tests it, the rest are ENOSYS.
    if (p->uvp) {
        int r = uv_process_kill(p->uvp, (int)signal);
        return r == 0 ? 0 : -us_errno(r);
    }
#endif
    if (kill(p->pid, (int)signal) != 0) return -errno;
    return 0;
}

// A referenced child holds the loop open until it exits.
void __kml_native_process_ref(double id, _Bool on) {
    kml_proch *p = proc_get(id);
    if (!p) return;
    p->refd = on;
    if (p->uvp) {
        if (on) uv_ref((uv_handle_t *)p->uvp);
        else uv_unref((uv_handle_t *)p->uvp);
    }
}

// The handle is done with: its exit is no longer reported.
void __kml_native_process_close(double id) {
    kml_proch *p = proc_get(id);
    if (p) {
        p->exit_inv = NULL;
        p->refd = 0;
        if (p->uvp) uv_unref((uv_handle_t *)p->uvp);
    }
}

// A child exit held for its streams (2, holds the loop) or an unref'd
// child's exit waiting on them (1, does not), for the loop's kick.
static int proc_kick_wanted(void) {
    int k = 0;
    for (int i = 0; i < proc_n; i++) {
        if (proc_tab[i]->pending == 2) return 2;
        if (proc_tab[i]->pending == 1) k = 1;
    }
    return k;
}


// The exits of unref'd children reaped on an earlier pass (pending 1).
static _Bool proc_dispatch_unrefd(void) {
    _Bool ran = 0;
    for (int i = 0; i < proc_n; i++) {
        kml_proch *p = proc_tab[i];
        if (p->pending == 1) {
            // Not while its own stdio is still active (reading before the end
            // of the stream, or closing): when nothing else holds the loop
            // open it ends first, and the exit goes unreported.
            int open = 0;
            for (int k = 0; k < KML_MAX_STDIO_H; k++) {
                int h = p->stdio[k];
                if (h < 0 || h >= tcp_n) continue;
                kml_tcp *t = tcp_tab[h];
                if (!t->closed_notified && ((t->reading && !t->read_eof) || t->closing)) open = 1;
            }
            if (open) continue;
            p->pending = 0;
            ran = 1;
            if (p->exit_inv) TCP_CALL(p->exit_inv, p->exit_clo, p->pending_code, p->pending_sig);
            continue;
        }
    }
    return ran;
}

static _Bool proc_dispatch(void) {
    _Bool ran = 0;
    for (int i = 0; i < proc_n; i++) {
        kml_proch *p = proc_tab[i];
        if (p->pending == 2) {
            // Held one pass for its streams, which this pass has now read.
            p->pending = 0;
            ran = 1;
            if (p->exit_inv) TCP_CALL(p->exit_inv, p->exit_clo, p->pending_code, p->pending_sig);
            continue;
        }
        if (p->pending) continue; // proc_dispatch_unrefd's
        if (!p->alive) continue;
        if (!p->exited) continue;
        p->alive = 0;
        double code = p->ex_code, sig = p->ex_sig;
        if (!p->refd) {
            p->pending = 1;
            p->pending_code = code;
            p->pending_sig = sig;
            continue;
        }
        // Reaped after this pass read its streams: what they still hold
        // (an IPC channel's last message, its end) was written before the
        // process ended, so it is read first, next pass.
        int unread = 0;
        for (int k = 0; k < KML_MAX_STDIO_H; k++) {
            int h = p->stdio[k];
            if (h < 0 || h >= tcp_n) continue;
            kml_tcp *t = tcp_tab[h];
            if (t->fd < 0 || t->closing || !t->reading || t->read_eof) continue;
#ifdef _WIN32
            if (t->us) {
                if (us_ready(t->us) & UV_READABLE) unread = 1;
                continue;
            }
#endif
            struct pollfd pfd = { t->fd, POLLIN, 0 };
            if (poll(&pfd, 1, 0) > 0 && (pfd.revents & (POLLIN | POLLHUP)) != 0) unread = 1;
        }
        if (unread) {
            p->pending = 2;
            p->pending_code = code;
            p->pending_sig = sig;
            continue;
        }
        ran = 1;
        if (p->exit_inv) TCP_CALL(p->exit_inv, p->exit_clo, code, sig);
    }
    return ran;
}

// ---- spawnSync (Node's spawn_sync.cc) ------------------------------------------
// Blocks: spawns as processSpawn does, feeds input to a piped stdin, collects
// the piped output, enforces timeout (ms, 0 none) and maxBuffer (bytes per
// stream, 0 none) with killSignal, and waits for the child. Returns 0, or
// -errno when the program could not be started; the rest is read back with
// spawnSyncResult and spawnSyncTake.
static int64_t sync_status, sync_signal, sync_error, sync_pid;
static char *sync_out[KML_MAX_STDIO];
static int64_t sync_len[KML_MAX_STDIO];

static int64_t proc_now_ms(void) {
#ifdef _WIN32
    extern unsigned long long GetTickCount64(void);
    return (int64_t)GetTickCount64();
#else
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
#endif
}

#ifdef _WIN32
// spawnSync's collection on its own loop: the input written to a piped
// stdin, which then closes; every other pipe read to its end; the timeout
// and maxBuffer kills; the exit.
typedef struct { kml_proch *p; int sig; int killed; } sync_win_kill;

static void sync_win_kill_now(sync_win_kill *k, int64_t why) {
    if (k->killed) return;
    k->killed = 1;
    sync_error = why;
    if (k->p->uvp) uv_process_kill(k->p->uvp, k->sig);
}

static void sync_win_on_timeout(uv_timer_t *t) {
    sync_win_kill_now((sync_win_kill *)t->data, ETIMEDOUT);
}

static void sync_win_on_written(uv_write_t *req, int status) {
    (void)status; // EPIPE: the child stopped reading
    uv_close((uv_handle_t *)req->handle, us_on_close);
    free(req);
}

static double spawn_sync_win(uv_loop_t *loop, kml_proch *p, void *input, int64_t input_size, double timeout, double maxBuffer, double killSignal) {
    kml_ustream *us[KML_MAX_STDIO] = {0};
    for (int i = 0; i < KML_MAX_STDIO; i++) {
        int h = proc_last_stdio[i];
        if (h < 0) continue;
        kml_tcp *t = tcp_tab[h];
        // the stream is ours here; the handle is retired
        us[i] = t->us;
        t->us = NULL;
        t->fd = -1;
        t->closing = t->closed_notified = 1;
        if (us[i]) us[i]->h = NULL;
    }
    if (us[0]) {
        if (input_size > 0) {
            uv_write_t *req = (uv_write_t *)malloc(sizeof *req + (size_t)input_size);
            memcpy(req + 1, input, (size_t)input_size);
            uv_buf_t b = uv_buf_init((char *)(req + 1), (unsigned int)input_size);
            if (uv_write(req, us[0]->s, &b, 1, sync_win_on_written) != 0) {
                free(req);
                uv_close((uv_handle_t *)us[0]->s, us_on_close);
            }
        } else {
            uv_close((uv_handle_t *)us[0]->s, us_on_close);
        }
        us[0] = NULL;
    }
    for (int i = 1; i < KML_MAX_STDIO; i++) {
        if (!us[i]) continue;
        us[i]->collect = 1;
        if (uv_read_start(us[i]->s, us_alloc, us_on_read) != 0) us[i]->eof = 1;
    }
    sync_win_kill k = { p, (int)killSignal, 0 };
    uv_timer_t timer;
    uv_timer_init(loop, &timer);
    timer.data = &k;
    if (timeout > 0) uv_timer_start(&timer, sync_win_on_timeout, (uint64_t)timeout, 0);
    for (;;) {
        int open = 0;
        for (int i = 1; i < KML_MAX_STDIO; i++) {
            if (!us[i]) continue;
            if (maxBuffer > 0 && us[i]->len - us[i]->off > (int64_t)maxBuffer) sync_win_kill_now(&k, ENOBUFS);
            if (!us[i]->eof && !us[i]->rerr) open = 1;
        }
        if (p->exited && !open) break;
        uv_run(loop, UV_RUN_ONCE);
    }
    uv_close((uv_handle_t *)&timer, NULL);
    for (int i = 1; i < KML_MAX_STDIO; i++) {
        if (!us[i]) continue;
        int64_t n = us[i]->len - us[i]->off;
        sync_out[i] = (char *)malloc((size_t)n + 1);
        memcpy(sync_out[i], us[i]->buf + us[i]->off, (size_t)n);
        sync_len[i] = n;
        uv_close((uv_handle_t *)us[i]->s, us_on_close);
    }
    uv_run(loop, UV_RUN_DEFAULT);
    uv_loop_close(loop);
    p->alive = 0;
    p->exit_inv = NULL;
    sync_status = (int64_t)p->ex_code;
    sync_signal = (int64_t)p->ex_sig;
    return 0;
}
#endif

double __kml_native_spawn_sync(const char *file, void *argv_buf, int64_t argv_size, void *env_buf, int64_t env_size, const char *cwd, double flags, const char *stdio, double uid, double gid, void *input, int64_t input_size, double timeout, double maxBuffer, double killSignal) {
    for (int i = 0; i < KML_MAX_STDIO; i++) {
        free(sync_out[i]);
        sync_out[i] = NULL;
        sync_len[i] = 0;
    }
    sync_status = sync_signal = sync_error = sync_pid = 0;
    // Its own libuv loop, as Node's spawn_sync has: the one nested loop, which
    // shares no handle with the thread's (TDD-00244).
    (void)__kml_loop_uv(); // the loop's allocator, under -mm=gc
    uv_loop_t priv;
    uv_loop_init(&priv);
    double id = proc_spawn_on(&priv, file, argv_buf, argv_size, env_buf, env_size, cwd, flags, stdio, uid, gid, NULL, NULL);
    if (id < 0) {
        uv_run(&priv, UV_RUN_DEFAULT);
        uv_loop_close(&priv);
        return id;
    }
    kml_proch *p = proc_get(id);
    sync_pid = p->pid;
#ifdef _WIN32
    return spawn_sync_win(&priv, p, input, input_size, timeout, maxBuffer, killSignal);
#else
    int fds[KML_MAX_STDIO];
    int caps[KML_MAX_STDIO];
    for (int i = 0; i < KML_MAX_STDIO; i++) {
        fds[i] = -1;
        caps[i] = 0;
        int h = proc_last_stdio[i];
        if (h < 0) continue;
        kml_tcp *t = tcp_tab[h];
        fds[i] = t->fd;
        t->fd = -1; // the descriptor is ours here; the handle is retired
        t->closing = t->closed_notified = 1;
    }
    // Feed stdin, then close it.
    if (fds[0] >= 0) {
        int64_t off = 0;
        int64_t start = proc_now_ms();
        while (off < input_size) {
            struct pollfd pf = { fds[0], POLLOUT, 0 };
            int wait = 100;
            if (timeout > 0) {
                int64_t left = (int64_t)timeout - (proc_now_ms() - start);
                if (left <= 0) break;
                if (left < wait) wait = (int)left;
            }
            if (poll(&pf, 1, wait) <= 0) continue;
            ssize_t n = write(fds[0], (const char *)input + off, (size_t)(input_size - off));
            if (n < 0) {
                if (errno == EAGAIN || errno == EINTR) continue;
                break; // EPIPE: the child stopped reading
            }
            off += n;
        }
        close(fds[0]);
        fds[0] = -1;
    }
    int64_t start = proc_now_ms();
    int killed = 0;
    for (;;) {
        struct pollfd pfs[KML_MAX_STDIO];
        int map[KML_MAX_STDIO];
        int n = 0;
        for (int i = 1; i < KML_MAX_STDIO; i++) {
            if (fds[i] < 0) continue;
            pfs[n].fd = fds[i];
            pfs[n].events = POLLIN;
            pfs[n].revents = 0;
            map[n++] = i;
        }
        if (n == 0) break;
        int wait = -1;
        if (timeout > 0 && !killed) {
            int64_t left = (int64_t)timeout - (proc_now_ms() - start);
            if (left <= 0) {
                kill(p->pid, (int)killSignal);
                killed = 1;
                sync_error = ETIMEDOUT;
                continue;
            }
            wait = (int)left;
        }
        int r = poll(pfs, (unsigned)n, wait);
        if (r < 0 && errno != EINTR) break;
        for (int k = 0; k < n && r > 0; k++) {
            if (!pfs[k].revents) continue;
            int i = map[k];
            if (sync_len[i] + 65536 > caps[i]) {
                int nc = caps[i] ? caps[i] * 2 : 65536;
                while (nc < sync_len[i] + 65536) nc *= 2;
                sync_out[i] = (char *)realloc(sync_out[i], (size_t)nc);
                caps[i] = nc;
            }
            ssize_t got = read(fds[i], sync_out[i] + sync_len[i], 65536);
            if (got > 0) {
                sync_len[i] += got;
                if (maxBuffer > 0 && sync_len[i] > (int64_t)maxBuffer && !killed) {
                    kill(p->pid, (int)killSignal);
                    killed = 1;
                    sync_error = ENOBUFS;
                }
            } else if (got == 0 || (errno != EAGAIN && errno != EINTR)) {
                close(fds[i]);
                fds[i] = -1;
            }
        }
    }
    for (int i = 0; i < KML_MAX_STDIO; i++)
        if (fds[i] >= 0) close(fds[i]);
    // The private loop runs until the exit callback, then closes.
    while (!p->exited) uv_run(&priv, UV_RUN_ONCE);
    uv_run(&priv, UV_RUN_DEFAULT);
    uv_loop_close(&priv);
    p->alive = 0;
    p->exit_inv = NULL;
    sync_status = (int64_t)p->ex_code;
    sync_signal = (int64_t)p->ex_sig;
    return 0;
#endif
}

// What the last spawnSync gave: 0 pid, 1 exit status, 2 terminating signal
// (0 none), 3 error (ETIMEDOUT, ENOBUFS, or 0), 4 + i the bytes collected
// from descriptor i.
double __kml_native_spawn_sync_result(double what) {
    int w = (int)what;
    switch (w) {
    case 0: return (double)sync_pid;
    case 1: return (double)sync_status;
    case 2: return (double)sync_signal;
    case 3: return (double)sync_error;
    }
    int i = w - 4;
    return (i >= 0 && i < KML_MAX_STDIO) ? (double)sync_len[i] : 0;
}

// Copy descriptor i's collected bytes into buf.
double __kml_native_spawn_sync_take(double fd, void *buf, int64_t size) {
    int i = (int)fd;
    if (i < 0 || i >= KML_MAX_STDIO || !sync_out[i]) return 0;
    int64_t n = sync_len[i] < size ? sync_len[i] : size;
    memcpy(buf, sync_out[i], (size_t)n);
    return (double)n;
}

// A signal's number by name ("SIGTERM"), or 0.
double __kml_native_signal_number(const char *name) {
    static const struct { const char *n; int v; } sigs[] = {
#ifndef _WIN32
        {"SIGHUP", SIGHUP},
        {"SIGINT", 2}, {"SIGQUIT", 3}, {"SIGILL", 4}, {"SIGTRAP", 5}, {"SIGABRT", 6},
        {"SIGFPE", 8}, {"SIGKILL", 9}, {"SIGSEGV", 11}, {"SIGPIPE", 13}, {"SIGALRM", 14}, {"SIGTERM", 15},
        {"SIGBUS", SIGBUS}, {"SIGUSR1", SIGUSR1}, {"SIGUSR2", SIGUSR2}, {"SIGCHLD", SIGCHLD},
        {"SIGCONT", SIGCONT}, {"SIGSTOP", SIGSTOP}, {"SIGTSTP", SIGTSTP}, {"SIGTTIN", SIGTTIN},
        {"SIGTTOU", SIGTTOU}, {"SIGURG", SIGURG}, {"SIGXCPU", SIGXCPU}, {"SIGXFSZ", SIGXFSZ},
        {"SIGVTALRM", SIGVTALRM}, {"SIGPROF", SIGPROF}, {"SIGWINCH", SIGWINCH}, {"SIGIO", SIGIO},
        {"SIGSYS", SIGSYS},
#else
        // Node's os.constants.signals on Windows: the CRT's and libuv's
        // (uv/win.h); kill answers ENOSYS for those it cannot deliver.
        {"SIGHUP", 1}, {"SIGINT", 2}, {"SIGQUIT", 3}, {"SIGILL", 4}, {"SIGABRT", 22},
        {"SIGFPE", 8}, {"SIGKILL", 9}, {"SIGSEGV", 11}, {"SIGTERM", 15}, {"SIGBREAK", 21},
        {"SIGWINCH", 28},
#endif
    };
    for (size_t i = 0; i < sizeof sigs / sizeof sigs[0]; i++)
        if (strcmp(sigs[i].n, name) == 0) return sigs[i].v;
    return 0;
}

// A signal's name by number, or "".
char *__kml_native_signal_name(double num) {
    static const char *names[] = {"SIGHUP", "SIGINT", "SIGQUIT", "SIGILL", "SIGTRAP", "SIGABRT", "SIGBUS", "SIGFPE",
                                  "SIGKILL", "SIGUSR1", "SIGSEGV", "SIGUSR2", "SIGPIPE", "SIGALRM", "SIGTERM",
                                  "SIGCHLD", "SIGCONT", "SIGSTOP", "SIGTSTP", "SIGTTIN", "SIGTTOU", "SIGURG",
                                  "SIGXCPU", "SIGXFSZ", "SIGVTALRM", "SIGPROF", "SIGWINCH", "SIGIO", "SIGSYS", "SIGBREAK"};
    const char *out = "";
    for (size_t i = 0; i < sizeof names / sizeof names[0]; i++) {
        if (__kml_native_signal_number(names[i]) == num) {
            out = names[i];
            break;
        }
    }
    int64_t n = (int64_t)strlen(out);
    char *s = __kml_str_alloc(n + 1);
    memcpy(s, out, (size_t)n + 1);
    __kml_str_finalize(s);
    return s;
}

// ---- stdio and tty handles (Node's tty_wrap / pipe_wrap over a descriptor) --
// process.stdin/stdout/stderr and `tty` (lib/node/tty.ts) over the stream
// handle table above. libuv's uv_guess_handle, uv_tty_init, uv_pipe_open,
// uv_tty_set_mode and uv_tty_get_winsize.
#ifdef _WIN32
extern int __kml_win_fd_kind(int fd);                     // 0 unknown, 1 file, 2 pipe, 3 tty, 4 tcp
extern int __kml_win_tty_set_raw(int fd, int raw);        // 0, or -errno
extern int __kml_win_tty_size(int fd, int *cols, int *rows); // 0, or -1 when not a console
#else
#include <termios.h>
#include <sys/ioctl.h>
#include <sys/stat.h>
#endif

static char *kml_cstr(const char *out) {
    int64_t n = (int64_t)strlen(out);
    char *s = __kml_str_alloc(n + 1);
    memcpy(s, out, (size_t)n + 1);
    __kml_str_finalize(s);
    return s;
}

// What descriptor fd is: "TTY", "FILE", "PIPE", "TCP", "UDP" or "UNKNOWN".
char *__kml_native_guess_handle_type(double fdd) {
    int fd = (int)fdd;
    const char *t = "UNKNOWN";
#ifdef _WIN32
    static const char *kinds[] = {"UNKNOWN", "FILE", "PIPE", "TTY", "TCP"};
    int k = fd >= 0 ? __kml_win_fd_kind(fd) : 0;
    t = kinds[k >= 0 && k <= 4 ? k : 0];
#else
    struct stat st;
    if (fd >= 0 && fstat(fd, &st) == 0) {
        if (isatty(fd)) {
            t = "TTY";
        } else if (S_ISREG(st.st_mode) || S_ISCHR(st.st_mode)) {
            t = "FILE";
        } else if (S_ISFIFO(st.st_mode)) {
            t = "PIPE";
        } else if (S_ISSOCK(st.st_mode)) {
            int type = 0;
            socklen_t tl = sizeof type;
            struct sockaddr_storage ss;
            socklen_t sl = sizeof ss;
            if (getsockopt(fd, SOL_SOCKET, SO_TYPE, &type, &tl) == 0 &&
                getsockname(fd, (struct sockaddr *)&ss, &sl) == 0) {
                if (ss.ss_family == AF_UNIX) t = "PIPE";
                else if (type == SOCK_STREAM && (ss.ss_family == AF_INET || ss.ss_family == AF_INET6)) t = "TCP";
                else if (type == SOCK_DGRAM && (ss.ss_family == AF_INET || ss.ss_family == AF_INET6)) t = "UDP";
            }
        }
    }
#endif
    return kml_cstr(t);
}

// An existing descriptor as a stream handle: its id, or -errno. A readable
// terminal is reopened by its path, as uv_tty_init does, so that making it
// non-blocking does not change the terminal the parent shell reads; a pipe
// or socket is made non-blocking.
double __kml_native_stream_open(double fdd, _Bool readable) {
    int fd = (int)fdd;
#ifdef _WIN32
    if (fd < 0) return -EBADF;
    // A pipe or console is libuv's stream over the descriptor's handle, as
    // Node's pipe_wrap and tty_wrap are; a socket is the table's own.
    int k = __kml_win_fd_kind(fd);
    if (k == 2 || k == 3) {
        int crt = __kml_win_fd_crt(fd);
        if (crt < 0) return -errno;
        kml_ustream *u = us_open(__kml_loop_uv(), crt, readable);
        if (!u) return -errno;
        return tcp_new_us(u);
    }
    if (readable) set_nonblock(fd);
    return tcp_new(fd);
#else
    struct stat st;
    if (fd < 0 || fstat(fd, &st) != 0) return -EBADF;
    if (isatty(fd)) {
        if (readable) {
            const char *path = ttyname(fd);
            int nfd = path ? open(path, O_RDWR | O_NOCTTY) : -1;
            if (nfd >= 0) {
                fcntl(nfd, F_SETFD, FD_CLOEXEC);
                set_nonblock(nfd);
                fd = nfd;
            }
        }
    } else if (!S_ISREG(st.st_mode)) {
        set_nonblock(fd);
    }
    return tcp_new(fd);
#endif
}

// A blocking write of all of buf to fd — Node's stdout/stderr on a file,
// terminal or POSIX pipe: bytes written, or -errno.
double __kml_native_write_sync(double fdd, void *buf, int64_t size, double offset, double length) {
    int fd = (int)fdd;
    const char *p = (const char *)buf + (int64_t)offset;
    int64_t left = (int64_t)length, done = 0;
    if ((int64_t)offset < 0 || (int64_t)offset + left > size) return -EINVAL;
    while (left > 0) {
        int64_t w = write(fd, p + done, (size_t)left);
        if (w < 0) {
            if (errno == EINTR) continue;
#ifndef _WIN32
            if (errno == EAGAIN || errno == EWOULDBLOCK) {
                struct pollfd pfd = {fd, POLLOUT, 0};
                poll(&pfd, 1, -1);
                continue;
            }
#endif
            return -errno;
        }
        done += w;
        left -= w;
    }
    return (double)done;
}

#ifndef _WIN32
static struct termios tty_orig;
static int tty_orig_fd = -1;
static void tty_reset_mode(void) {
    if (tty_orig_fd >= 0) tcsetattr(tty_orig_fd, TCSANOW, &tty_orig);
}
#endif

// A terminal handle's mode: 1 raw (libuv's UV_TTY_MODE_RAW), 0 normal.
// 0, or -errno. The original mode comes back at exit (uv_tty_reset_mode).
double __kml_native_tty_set_raw_mode(double id, _Bool raw) {
    kml_tcp *h = tcp_get(id);
    if (!h || h->fd < 0) return -EBADF;
#ifdef _WIN32
    if (h->us) {
        if (h->us->s->type != UV_TTY) return -ENOTTY;
        // Raw with VT input: an arrow key arrives as the ESC [ A a POSIX
        // terminal sends, to this stream and to klain:tty's console reads.
        int r = uv_tty_set_mode((uv_tty_t *)h->us->s, raw ? UV_TTY_MODE_RAW_VT : UV_TTY_MODE_NORMAL);
        return r == 0 ? 0 : -us_errno(r);
    }
    return __kml_win_tty_set_raw(h->fd, raw ? 1 : 0);
#else
    struct termios cur;
    if (tcgetattr(h->fd, &cur) != 0) return -errno;
    if (tty_orig_fd < 0) {
        tty_orig = cur;
        tty_orig_fd = h->fd;
        atexit(tty_reset_mode);
    }
    struct termios t = tty_orig;
    if (raw) {
        t.c_iflag &= ~(BRKINT | ICRNL | INPCK | ISTRIP | IXON);
        t.c_oflag |= ONLCR;
        t.c_cflag |= CS8;
        t.c_lflag &= ~(ECHO | ICANON | IEXTEN | ISIG);
        t.c_cc[VMIN] = 1;
        t.c_cc[VTIME] = 0;
    }
    if (tcsetattr(h->fd, TCSADRAIN, &t) != 0) return -errno;
    return 0;
#endif
}

// A terminal's size as cols * 65536 + rows, or -errno.
double __kml_native_tty_window_size(double fdd) {
    int fd = (int)fdd;
#ifdef _WIN32
    int cols = 0, rows = 0;
    if (__kml_win_tty_size(fd, &cols, &rows) != 0) return -EINVAL;
    return (double)cols * 65536 + rows;
#else
    struct winsize ws;
    if (ioctl(fd, TIOCGWINSZ, &ws) != 0) return -errno;
    return (double)ws.ws_col * 65536 + ws.ws_row;
#endif
}

// ---- event-loop hooks ------------------------------------------------------
// An outstanding submission keeps this loop alive so it doesn't exit while a
// read is in flight and never settles the awaiting Promise: on POSIX through
// the port's referenced async, on Windows through this hook.

// Drain arrived completions on the loop thread (the port's async callback
// on POSIX, the loop's pass on Windows). The comp stack is LIFO: reverse
// the batch to completion order before dispatching.
// Returns whether any completion ran.
_Bool __kml_pool_dispatch(void) {
    kml_loop_port *p = tls_port;
    if (!p) return 0;
    kml_pool_item *it = atomic_exchange_explicit(&p->comp_head, NULL,
                                                 memory_order_acquire);
    _Bool ran = it != NULL;
    // Reverse LIFO -> FIFO (insertion order).
    kml_pool_item *ordered = NULL;
    while (it) {
        kml_pool_item *nx = it->next;
        it->next = ordered;
        ordered = it;
        it = nx;
    }
    for (kml_pool_item *c = ordered; c;) {
        kml_pool_item *nx = c->next;
        native_unlink(c);
        port_hold(p, -1);
        native_set_last_string(c->res_str);
        kml_make_callback(c->inv, c->clo, (double)c->err, c->res);
        free(c->arg0);
        free(c->arg1);
        free(c->res_str);
        free(c);
        c = nx;
    }
    return ran;
}

// ---- MessagePort and Worker threads (Node's worker_threads) ----------------
// A port is one end of an entangled pair, in a process-wide table: a message
// posted on one end is queued on the other, and the loop that owns that end
// (the thread that started it) is woken with a completion that calls its
// callback with (0 messages | 1 closed, 0). Its callback takes the queue with
// mportHas/mportTake. A message is a value the sender cloned: it belongs to
// the receiver from then on. A referenced port holds its loop open while it
// has a callback, as a Node port with a 'message' listener does.

typedef struct kml_msg {
    struct kml_msg *next;
    int64_t v;
} kml_msg;

typedef struct kml_mport {
    int peer;              // the entangled end, or -1
    kml_loop_port *loop;   // the owning loop, or NULL (not started, or in transit)
    void *inv, *clo;
    int refd, held, notified, closed, close_sent;
    kml_msg *head, *tail;
} kml_mport;

static pthread_mutex_t mp_mu = PTHREAD_MUTEX_INITIALIZER;
static kml_mport **mp_tab = NULL;
static int mp_n = 0, mp_cap = 0;

static kml_mport *mp_get(double id) {
    int i = (int)id;
    return (i >= 0 && i < mp_n) ? mp_tab[i] : NULL;
}

static int mp_new_locked(void) {
    if (mp_n == mp_cap) {
        int nc = mp_cap ? mp_cap * 2 : 16;
        kml_mport **nt = (kml_mport **)calloc((size_t)nc, sizeof *nt);
        if (mp_tab) memcpy(nt, mp_tab, (size_t)mp_cap * sizeof *nt);
        mp_tab = nt;
        mp_cap = nc;
    }
    kml_mport *p = (kml_mport *)calloc(1, sizeof *p);
    p->peer = -1;
    p->refd = 1;
    mp_tab[mp_n] = p;
    return mp_n++;
}

// Post a completion calling inv(clo, a, b) on another (or this) thread's loop.
static void kml_post_to_loop(kml_loop_port *loop, void *inv, void *clo, int64_t a, double b) {
    kml_pool_item *it = native_item(NULL, inv, clo);
    it->err = a;
    it->res = b;
    it->port = loop;
    native_link(it);
    port_hold(loop, 1);
    push_completion(loop, it);
}

// Under mp_mu: wake the port's loop if it has something to deliver.
static void mp_notify_locked(kml_mport *p) {
    if (!p->loop || !p->inv || p->notified) return;
    if (p->head) {
        p->notified = 1;
        kml_post_to_loop(p->loop, p->inv, p->clo, 0, 0);
    } else if (p->closed && !p->close_sent) {
        p->notified = 1;
        p->close_sent = 1;
        kml_post_to_loop(p->loop, p->inv, p->clo, 1, 0);
    }
}

// Under mp_mu: the port holds its loop open while it is referenced, started
// and open.
static void mp_hold_locked(kml_mport *p) {
    int want = p->refd && p->loop && p->inv && !p->closed;
    if (want && !p->held) {
        p->held = 1;
        port_hold(p->loop, 1);
    } else if (!want && p->held) {
        p->held = 0;
        port_hold(p->loop, -1);
    }
}

// A new entangled pair: the first port's id; the second's is the next.
double __kml_native_mport_pair(void) {
    pthread_mutex_lock(&mp_mu);
    int a = mp_new_locked();
    int b = mp_new_locked();
    mp_tab[a]->peer = b;
    mp_tab[b]->peer = a;
    pthread_mutex_unlock(&mp_mu);
    return a;
}

// Deliver on this thread's loop: onEvent(0 messages | 1 closed, 0).
void __kml_native_mport_start(double id, void *inv, void *clo) {
    pthread_mutex_lock(&mp_mu);
    kml_mport *p = mp_get(id);
    if (p) {
        if (p->held) { p->held = 0; port_hold(p->loop, -1); }
        p->loop = loop_port();
        p->inv = inv;
        p->clo = clo;
        p->notified = 0;
        mp_hold_locked(p);
        mp_notify_locked(p);
    }
    pthread_mutex_unlock(&mp_mu);
}

// Stop delivering (the port is transferred, or has no listener): messages
// queue until it starts again.
void __kml_native_mport_stop(double id) {
    pthread_mutex_lock(&mp_mu);
    kml_mport *p = mp_get(id);
    if (p) {
        if (p->held) { p->held = 0; port_hold(p->loop, -1); }
        p->loop = NULL;
        p->inv = NULL;
        p->clo = NULL;
        p->notified = 0;
    }
    pthread_mutex_unlock(&mp_mu);
}

void __kml_native_mport_ref(double id, _Bool on) {
    pthread_mutex_lock(&mp_mu);
    kml_mport *p = mp_get(id);
    if (p) {
        p->refd = on ? 1 : 0;
        mp_hold_locked(p);
    }
    pthread_mutex_unlock(&mp_mu);
}

// Queue a value on the other end: 0, or -1 when the pair is closed (Node
// drops the message).
double __kml_native_mport_post(double id, int64_t v) {
    pthread_mutex_lock(&mp_mu);
    kml_mport *p = mp_get(id);
    kml_mport *q = p && p->peer >= 0 ? mp_tab[p->peer] : NULL;
    if (!p || !q || p->closed || q->closed) {
        pthread_mutex_unlock(&mp_mu);
        return -1;
    }
    kml_msg *m = (kml_msg *)calloc(1, sizeof *m);
    m->v = v;
    if (q->tail) q->tail->next = m; else q->head = m;
    q->tail = m;
    mp_notify_locked(q);
    pthread_mutex_unlock(&mp_mu);
    return 0;
}

// Whether a message waits; when none does, the next arrival notifies again.
_Bool __kml_native_mport_has(double id) {
    pthread_mutex_lock(&mp_mu);
    kml_mport *p = mp_get(id);
    _Bool has = p && p->head;
    if (p && !has) {
        p->notified = 0;
        if (p->closed && !p->close_sent) mp_notify_locked(p);
    }
    pthread_mutex_unlock(&mp_mu);
    return has;
}

// The oldest waiting message (mportHas said there is one).
int64_t __kml_native_mport_take(double id) {
    pthread_mutex_lock(&mp_mu);
    kml_mport *p = mp_get(id);
    int64_t v = 0;
    if (p && p->head) {
        kml_msg *m = p->head;
        p->head = m->next;
        if (!p->head) p->tail = NULL;
        v = m->v;
        free(m);
    }
    pthread_mutex_unlock(&mp_mu);
    return v;
}

// Close the pair: each end delivers what is queued, then 'close'.
void __kml_native_mport_close(double id) {
    pthread_mutex_lock(&mp_mu);
    kml_mport *p = mp_get(id);
    if (p && !p->closed) {
        kml_mport *q = p->peer >= 0 ? mp_tab[p->peer] : NULL;
        p->closed = 1;
        mp_hold_locked(p);
        mp_notify_locked(p);
        if (q && !q->closed) {
            q->closed = 1;
            mp_hold_locked(q);
            mp_notify_locked(q);
        }
    }
    pthread_mutex_unlock(&mp_mu);
}

// ---- Worker threads --------------------------------------------------------
// Each worker module's entry is registered at start-up by path; a Worker is a
// thread that runs the emitted thread main (thread) with its context. The
// parent's loop hears onEvent(0 online | 1 exit, exit code); a referenced
// worker holds the parent's loop open until its exit is heard.

typedef struct kml_wentry {
    char *path;
    void *entry;
    void *thread;
} kml_wentry;

static kml_wentry *went = NULL;
static int went_n = 0, went_cap = 0;

void __kml_worker_register(const char *path, void *entry, void *thread) {
    if (went_n == went_cap) {
        int nc = went_cap ? went_cap * 2 : 4;
        kml_wentry *nt = (kml_wentry *)calloc((size_t)nc, sizeof *nt);
        if (went) memcpy(nt, went, (size_t)went_cap * sizeof *nt);
        went = nt;
        went_cap = nc;
    }
    went[went_n].path = strdup(path);
    went[went_n].entry = entry;
    went[went_n].thread = thread;
    went_n++;
}

typedef struct kml_wctx {
    int id;
    double thread_id;
    double port, iport;     // the worker's ends of the public and internal pairs
    int64_t data;           // workerData, cloned by the parent
    char *filename;
    void *entry;
    kml_loop_port *parent;  // the spawning thread's loop
    kml_loop_port *loop;    // the worker's own loop, once it runs
    void *inv, *clo;        // the parent's onEvent
    int refd, held, exited;
    atomic_int terminate;
    int code;
    pthread_t tid;
} kml_wctx;

static pthread_mutex_t w_mu = PTHREAD_MUTEX_INITIALIZER;
static kml_wctx **w_tab = NULL;
static int w_n = 0, w_cap = 0;
static atomic_long w_next_thread_id = 1;
static __thread kml_wctx *w_self = NULL;

static kml_wctx *w_get(double id) {
    int i = (int)id;
    kml_wctx *w = NULL;
    pthread_mutex_lock(&w_mu);
    if (i >= 0 && i < w_n) w = w_tab[i];
    pthread_mutex_unlock(&w_mu);
    return w;
}

// The same module path: on Windows '/' and '\' are one separator and letters
// compare without case, as the file system resolves them.
static int worker_path_eq(const char *a, const char *b) {
#ifdef _WIN32
    for (;; a++, b++) {
        char x = *a == '/' ? '\\' : *a, y = *b == '/' ? '\\' : *b;
        if (x >= 'A' && x <= 'Z') x = (char)(x - 'A' + 'a');
        if (y >= 'A' && y <= 'Z') y = (char)(y - 'A' + 'a');
        if (x != y) return 0;
        if (!x) return 1;
    }
#else
    return strcmp(a, b) == 0;
#endif
}

// Start a worker of the registered module at path: its id, or -1 when no
// worker module has that path, -errno when the thread fails to start.
double __kml_native_worker_spawn(const char *path, double port, double iport, int64_t data, void *inv, void *clo) {
    kml_wentry *e = NULL;
    for (int i = 0; i < went_n; i++)
        if (path && worker_path_eq(went[i].path, path)) e = &went[i];
    if (!e) return -1;
    kml_wctx *w = (kml_wctx *)calloc(1, sizeof *w);
    w->thread_id = (double)atomic_fetch_add(&w_next_thread_id, 1);
    w->port = port;
    w->iport = iport;
    w->data = data;
    w->filename = strdup(path);
    w->entry = e->entry;
    w->parent = loop_port();
    w->inv = inv;
    w->clo = clo;
    w->refd = 1;
    pthread_mutex_lock(&w_mu);
    if (w_n == w_cap) {
        int nc = w_cap ? w_cap * 2 : 8;
        kml_wctx **nt = (kml_wctx **)calloc((size_t)nc, sizeof *nt);
        if (w_tab) memcpy(nt, w_tab, (size_t)w_cap * sizeof *nt);
        w_tab = nt;
        w_cap = nc;
    }
    w->id = w_n;
    w_tab[w_n++] = w;
    w->held = 1;
    port_hold(w->parent, 1);
    pthread_mutex_unlock(&w_mu);
    kml_gc_allow();
    pthread_attr_t attr;
    pthread_attr_init(&attr);
    // Node gives a Worker a 4 MB stack.
    pthread_attr_setstacksize(&attr, 4 << 20);
    int rc = pthread_create(&w->tid, &attr, (void *(*)(void *))e->thread, w);
    pthread_attr_destroy(&attr);
    if (rc != 0) {
        pthread_mutex_lock(&w_mu);
        w->held = 0;
        w->exited = 1;
        port_hold(w->parent, -1);
        pthread_mutex_unlock(&w_mu);
        return -rc;
    }
    pthread_detach(w->tid);
    return w->id;
}

// The thread id a worker got (Node's worker.threadId), 0 for the main thread.
double __kml_native_worker_thread_id(double id) {
    if (id < 0) return w_self ? w_self->thread_id : 0;
    kml_wctx *w = w_get(id);
    return w ? w->thread_id : -1;
}

void __kml_native_worker_ref(double id, _Bool on) {
    kml_wctx *w = w_get(id);
    if (!w) return;
    pthread_mutex_lock(&w_mu);
    w->refd = on ? 1 : 0;
    int want = w->refd && !w->exited;
    if (want && !w->held) {
        w->held = 1;
        port_hold(w->parent, 1);
    } else if (!want && w->held) {
        w->held = 0;
        port_hold(w->parent, -1);
    }
    pthread_mutex_unlock(&w_mu);
}

// worker.c's thread-aware exit, set by the worker thread before it enters.
static void (*w_end)(int);
void __kml_worker_set_end(void (*end)(int)) { w_end = end; }

// Delivered on the terminated worker's own loop: the thread ends with exit
// code 1 (leave records it).
static void kml_terminate_cb(void *clo, double a, double b) {
    (void)clo; (void)a; (void)b;
    if (w_end) w_end(1);
}

// Stop the worker: it ends at its loop's next turn, with exit code 1.
void __kml_native_worker_terminate(double id) {
    kml_wctx *w = w_get(id);
    if (!w) return;
    atomic_store(&w->terminate, 1);
    pthread_mutex_lock(&w_mu);
    if (w->loop && !w->exited) kml_post_to_loop(w->loop, (void *)kml_terminate_cb, NULL, 0, 0);
    pthread_mutex_unlock(&w_mu);
}

// This thread's worker: 0 whether it is one, 1 its public port, 2 its
// internal port.
double __kml_native_worker_self(double which) {
    if (!w_self) return which == 0 ? 0 : -1;
    switch ((int)which) {
    case 0: return 1;
    case 1: return w_self->port;
    case 2: return w_self->iport;
    }
    return -1;
}

int64_t __kml_native_worker_self_data(void) {
    return w_self ? w_self->data : 0;
}

// The worker's file (its module's path).
char *__kml_native_worker_self_filename(void) {
    const char *src = w_self ? w_self->filename : "";
    int64_t n = (int64_t)strlen(src);
    char *out = __kml_str_alloc(n + 1);
    memcpy(out, src, (size_t)n + 1);
    __kml_str_finalize(out);
    return out;
}

// Called by the thread main: this thread is w's; the parent hears 'online'.
void *__kml_worker_enter(kml_wctx *w) {
    w_self = w;
    pthread_mutex_lock(&w_mu);
    w->loop = loop_port();
    if (atomic_load(&w->terminate)) kml_post_to_loop(w->loop, (void *)kml_terminate_cb, NULL, 0, 0);
    pthread_mutex_unlock(&w_mu);
    kml_post_to_loop(w->parent, w->inv, w->clo, 0, 0);
    return w->entry;
}

_Bool __kml_worker_is_thread(void) { return w_self != NULL; }

// The thread's end: the parent hears 'exit' with the code (1 when it was
// terminated).
void __kml_worker_leave(int64_t code) {
    kml_wctx *w = w_self;
    if (!w) return;
    pthread_mutex_lock(&w_mu);
    if (atomic_load(&w->terminate)) code = 1;
    w->code = (int)code;
    pthread_mutex_unlock(&w_mu);
    kml_post_to_loop(w->parent, w->inv, w->clo, 1, (double)code);
}

// The parent heard the exit: the worker no longer holds its loop.
void __kml_native_worker_exited(double id) {
    kml_wctx *w = w_get(id);
    if (!w) return;
    pthread_mutex_lock(&w_mu);
    w->exited = 1;
    if (w->held) {
        w->held = 0;
        port_hold(w->parent, -1);
    }
    pthread_mutex_unlock(&w_mu);
}

// ---- BroadcastChannel registry ---------------------------------------------
// Every open BroadcastChannel, by name, as the port id others post into.

typedef struct kml_bc {
    char *name;
    double port;
} kml_bc;

static pthread_mutex_t bc_mu = PTHREAD_MUTEX_INITIALIZER;
static kml_bc *bc_tab = NULL;
static int bc_n = 0, bc_cap = 0;

void __kml_native_bc_join(const char *name, double port) {
    pthread_mutex_lock(&bc_mu);
    if (bc_n == bc_cap) {
        int nc = bc_cap ? bc_cap * 2 : 8;
        kml_bc *nt = (kml_bc *)calloc((size_t)nc, sizeof *nt);
        if (bc_tab) memcpy(nt, bc_tab, (size_t)bc_cap * sizeof *nt);
        bc_tab = nt;
        bc_cap = nc;
    }
    bc_tab[bc_n].name = strdup(name ? name : "");
    bc_tab[bc_n].port = port;
    bc_n++;
    pthread_mutex_unlock(&bc_mu);
}

void __kml_native_bc_leave(const char *name, double port) {
    pthread_mutex_lock(&bc_mu);
    for (int i = 0; i < bc_n; i++) {
        if (bc_tab[i].port == port && strcmp(bc_tab[i].name, name ? name : "") == 0) {
            free(bc_tab[i].name);
            bc_tab[i] = bc_tab[bc_n - 1];
            bc_n--;
            break;
        }
    }
    pthread_mutex_unlock(&bc_mu);
}

// How many channels have the name, and the i-th one's port (-1 past the end).
double __kml_native_bc_count(const char *name) {
    int n = 0;
    pthread_mutex_lock(&bc_mu);
    for (int i = 0; i < bc_n; i++)
        if (strcmp(bc_tab[i].name, name ? name : "") == 0) n++;
    pthread_mutex_unlock(&bc_mu);
    return n;
}

double __kml_native_bc_member(const char *name, double idx) {
    int k = 0;
    double port = -1;
    pthread_mutex_lock(&bc_mu);
    for (int i = 0; i < bc_n; i++) {
        if (strcmp(bc_tab[i].name, name ? name : "") != 0) continue;
        if (k++ == (int)idx) {
            port = bc_tab[i].port;
            break;
        }
    }
    pthread_mutex_unlock(&bc_mu);
    return port;
}
