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
// The wakeup is per loop. On POSIX it is a socketpair whose read end sits in the
// loop's select() set. On Windows the reactor's one wait is the loop thread's
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
// win32io.c: the calling thread's reactor port, a wake addressed to a port,
// and whether a socket's connect is still in flight.
extern void *__kml_win_reactor_port(void);
extern void __kml_win_port_wake(void *port);
extern int __kml_win_connect_pending(int fd);
// The descriptor surface of win32io.c/win32fs.c (read/write/close/fcntl/poll
// and the socket calls), which takes the Linux constants the POSIX code below
// is written against.
#include "kml_posix_compat.h"
int open(const char *path, int flags, ...);
int unlink(const char *path);
long long _lseeki64(int fd, long long offset, int whence);
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
struct in6_addr { unsigned char s6_addr[16]; };
struct sockaddr_in6 {
    sa_family_t sin6_family;
    in_port_t sin6_port;
    uint32_t sin6_flowinfo;
    struct in6_addr sin6_addr;
    uint32_t sin6_scope_id;
};
struct sockaddr_storage { sa_family_t ss_family; char ss_pad[126]; } __attribute__((aligned(8)));
struct sockaddr_un { sa_family_t sun_family; char sun_path[108]; };
// The reactor's descriptor sets are the IR's 1024-bit bitmaps.
#define KML_FD_SET(fd, set) (((unsigned char *)(set))[(fd) >> 3] |= (unsigned char)(1u << ((fd) & 7)))
#else
#include <unistd.h>
#include <fcntl.h>
#define KML_FD_SET(fd, set) FD_SET((fd), (fd_set *)(set))
#include <sys/select.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <arpa/inet.h>
#include <netdb.h>
#include <sys/un.h>
#include <poll.h>
#endif

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
// the wakeup that ends this loop's select() (a socketpair on POSIX, the loop
// thread's reactor port on Windows), and the count of this loop's outstanding
// submissions (keeps the loop alive while I/O flies).
typedef struct kml_loop_port {
    _Atomic(kml_pool_item *) comp_head;
#ifdef _WIN32
    void *win_port;
#else
    int wake_r, wake_w;
#endif
    atomic_long inflight;
} kml_loop_port;

// The port is per event-loop-thread. A Worker runs its own loop on its own
// thread and gets its own port; the pool itself is process-wide (below).
static __thread kml_loop_port *tls_port = NULL;

static kml_loop_port *loop_port(void) {
    if (tls_port) return tls_port;
    kml_loop_port *p = (kml_loop_port *)calloc(1, sizeof *p);
#ifdef _WIN32
    p->win_port = __kml_win_reactor_port();   // loop_port() runs on the loop thread
#else
    int sv[2];
    if (socketpair(AF_UNIX, SOCK_STREAM, 0, sv) != 0) {
        // Fall back to a pipe: read end sv[0], write end sv[1].
        if (pipe(sv) != 0) { sv[0] = sv[1] = -1; }
    }
    p->wake_r = sv[0];
    p->wake_w = sv[1];
    if (p->wake_r >= 0) {
        int fl = fcntl(p->wake_r, F_GETFL, 0);
        fcntl(p->wake_r, F_SETFL, fl | O_NONBLOCK);   // drain never blocks
    }
#endif
    tls_port = p;
    return p;
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
#ifdef _WIN32
    __kml_win_port_wake(p->win_port);
#else
    if (p->wake_w >= 0) {
        char b = 1;
        ssize_t n = write(p->wake_w, &b, 1);
        (void)n;
    }
#endif
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
    atomic_fetch_add_explicit(&p->inflight, 1, memory_order_relaxed);
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

#ifdef _WIN32
#define kml_fsync _commit
// No pread/pwrite in the C runtime: seek, then transfer. A positioned
// transfer on a descriptor shared across workers is not atomic here.
static int64_t kml_pread(int fd, void *b, size_t n, int64_t pos) {
    if (_lseeki64(fd, pos, SEEK_SET) < 0) return -1;
    return read(fd, b, n);
}
static int64_t kml_pwrite(int fd, const void *b, size_t n, int64_t pos) {
    if (_lseeki64(fd, pos, SEEK_SET) < 0) return -1;
    return write(fd, b, n);
}
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
// the closures it holds. The reactor wakes on the handles' fds; dispatch
// polls each one and runs what became ready: an accept, a connect's
// completion, a read, a queued write's progress, a shutdown, a close.
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
    // A datagram socket (Node's udp_wrap): each read is one datagram, from
    // the sender recorded here.
    int udp;
    struct sockaddr_storage from;
    // An IPC pipe (Node's uv_pipe_init(ipc=1)): descriptors that arrived
    // with the bytes read, oldest first, for uv_accept of a pending handle.
    int ipc;
    int *ipc_fds;
    int ipc_nfds, ipc_cap;
} kml_tcp;

// The TLS session operations tls.c installs: I/O returns bytes, 0 at the
// end of the stream (read), -EAGAIN when the session waits on the socket,
// or another -errno / -(10000 + reason) for an error.
typedef struct kml_tls_ops {
    int (*handshake)(void *tls);  // 1 done, 0 waiting, <0 error
    int64_t (*read)(void *tls, char *buf, int64_t n);
    int64_t (*write)(void *tls, const char *buf, int64_t n);
    int (*want_write)(void *tls);
    int (*pending)(void *tls);
    void (*shutdown)(void *tls);
    void (*free)(void *tls);
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
    return 0;
}

void *__kml_tcp_tls(double id) {
    kml_tcp *h = tcp_get(id);
    return h ? h->tls : NULL;
}

// Plain or TLS I/O on the handle: bytes, -EAGAIN, or another -errno.
static int64_t tcp_send(kml_tcp *h, const char *p, int64_t n) {
    if (h->tls && tcp_tls) return tcp_tls->write(h->tls, p, n);
#ifdef MSG_NOSIGNAL
    int64_t r = send(h->fd, p, (size_t)n, MSG_NOSIGNAL);
#else
    int64_t r = write(h->fd, p, (size_t)n);
#endif
    if (r < 0) return (errno == EWOULDBLOCK || errno == EINTR) ? -EAGAIN : -errno;
    return r;
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
    if (h->tls && tcp_tls) return tcp_tls->read(h->tls, p, n);
#ifndef _WIN32
    if (h->ipc) return ipc_recv(h, p, n);
#endif
    int64_t r = read(h->fd, p, (size_t)n);
    if (r < 0) return (errno == EWOULDBLOCK || errno == EINTR) ? -EAGAIN : -errno;
    return r;
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
    return id;
}

// A bound, listening socket nothing accepts on here (net's
// _createServerHandle for a cluster primary's shared handle: the workers
// accept on the descriptor they are sent). Returns the handle id, or -errno.
double __kml_native_tcp_bind(const char *host, double port, double backlog) {
    double id = __kml_native_tcp_listen(host, port, backlog, NULL, NULL);
    if (id >= 0) tcp_tab[(int)id]->listening = 0;
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
    return id;
}

// Connect to the Unix-domain socket path; as tcp_connect.
double __kml_native_pipe_connect(const char *path, void *inv, void *clo) {
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
}

void __kml_native_tcp_read_stop(double id) {
    kml_tcp *h = tcp_get(id);
    if (h) h->reading = 0;
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
// tcp_write, the descriptor sent with the first byte. -ENOTSUP on Windows.
double __kml_native_ipc_write_handle(double id, void *buf, int64_t size, double offset, double length, double handle, void *inv, void *clo) {
#ifdef _WIN32
    (void)id; (void)buf; (void)size; (void)offset; (void)length; (void)handle; (void)inv; (void)clo;
    return -ENOTSUP;
#else
    kml_tcp *src = tcp_get(handle);
    if (!src || src->fd < 0) return -EBADF;
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
        if (done == len) return 1;
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
    return 0;
}

// Half-close once the queued writes are out; onShutdown(errno, 0).
void __kml_native_tcp_shutdown(double id, void *inv, void *clo) {
    kml_tcp *h = tcp_get(id);
    if (!h) return;
    h->shut_pending = 1;
    h->shut_inv = inv;
    h->shut_clo = clo;
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
    if (h->fd >= 0) close(h->fd);
    h->fd = -1;
    for (int i = 0; i < h->ipc_nfds; i++) close(h->ipc_fds[i]);
    free(h->ipc_fds);
    h->ipc_fds = NULL;
    h->ipc_nfds = h->ipc_cap = 0;
    if (h->pipe_path) {
        unlink(h->pipe_path);
        free(h->pipe_path);
        h->pipe_path = NULL;
    }
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
    case 1: level = 0; opt = 4; break;   // Winsock IPPROTO_IP / IP_TTL
    case 2: level = 0; opt = 10; break;  // IP_MULTICAST_TTL
    case 3: level = 0; opt = 11; break;  // IP_MULTICAST_LOOP
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
    struct in_addr a;
    if (inet_pton(AF_INET, iface, &a) != 1) return -EINVAL;
    if (setsockopt(h->fd, 0, 9, &a, sizeof a) != 0) return -errno; // IP_MULTICAST_IF
    return 0;
#endif
}

static int tcp_active(kml_tcp *h) {
    return !h->closed_notified && (h->fd >= 0 || h->closing);
}

static _Bool proc_keepalive(void);
static _Bool proc_fdset_add(void *fdset, int *maxfd);
static _Bool proc_dispatch(void);

_Bool __kml_tcp_keepalive(void) {
    if (proc_keepalive()) return 1;
    for (int i = 0; i < tcp_n; i++) {
        kml_tcp *h = tcp_tab[i];
        if (!tcp_active(h)) continue;
        if (h->closing || h->wq_head) return 1;
        // A referenced handle holds the loop while it has something to do,
        // as libuv's active handles do: a listen, a connect, a read that
        // has not reached the end, a pending shutdown or handshake.
        if (h->refd && (h->listening || h->connecting || (h->reading && !h->read_eof) ||
                        h->shut_pending || h->tls_state == 1)) return 1;
    }
    return 0;
}

// Add the handles' fds to the reactor's sets: read interest for a
// listening server or a reading stream, write interest for a connect or
// queued writes. Returns true when work is ready without waiting.
_Bool __kml_tcp_fdset_add(void *fdset, void *wfdset, int *maxfd) {
    _Bool now = proc_fdset_add(fdset, maxfd);
    for (int i = 0; i < tcp_n; i++) {
        kml_tcp *h = tcp_tab[i];
        if (h->closing && !h->closed_notified) { now = 1; continue; }
        if (h->fd < 0) continue;
        if (h->connecting > 1) { now = 1; continue; }
        if (h->listening || (h->reading && !h->read_eof) || h->tls_state == 1) {
            KML_FD_SET(h->fd, fdset);
            if (h->fd > *maxfd) *maxfd = h->fd;
        }
        int tls_wants_write = h->tls && tcp_tls && tcp_tls->want_write(h->tls);
        if (h->connecting || (h->wq_head && h->tls_state != 1) || tls_wants_write) {
            KML_FD_SET(h->fd, wfdset);
            if (h->fd > *maxfd) *maxfd = h->fd;
        }
        // Decrypted bytes buffered in the session: ready without the socket.
        if (h->tls_state == 2 && h->reading && !h->read_eof && tcp_tls && tcp_tls->pending(h->tls) > 0) now = 1;
        if (h->shut_pending && !h->wq_head) now = 1;
    }
    return now;
}

// Run what became ready on every handle. Returns whether anything ran.
_Bool __kml_tcp_dispatch(void) {
    _Bool ran = proc_dispatch();
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
#ifdef _WIN32
        // The Windows layer consumes a readiness it reports (the reactor's
        // select() already took it), so each operation is simply tried: an
        // accept, a read or a write that cannot proceed answers EAGAIN. Only
        // a connect's end is asked for.
        int readable = 1, writable = h->connecting != 1 || !__kml_win_connect_pending(h->fd);
#else
        struct pollfd pfd = { h->fd, POLLIN | POLLOUT, 0 };
        if (poll(&pfd, 1, 0) < 0) continue;
        // POLLNVAL: a device poll() does not support (macOS /dev/null, which
        // select() reports ready) — the operation itself answers.
        int readable = (pfd.revents & (POLLIN | POLLHUP | POLLERR | POLLNVAL)) != 0;
        int writable = (pfd.revents & (POLLOUT | POLLERR | POLLHUP | POLLNVAL)) != 0;
#endif
        if (h->listening) {
            if (!readable) continue;
            for (;;) {
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
        if (h->tls_state == 1 && tcp_tls) {
            int rc = tcp_tls->handshake(h->tls);
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
        if (h->shut_pending && !h->wq_head) {
            h->shut_pending = 0;
            if (h->tls && tcp_tls) tcp_tls->shutdown(h->tls);
            int err = shutdown(h->fd, SHUT_WR) == 0 ? 0 : errno;
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
            if (n < (int64_t)sizeof buf) break;
        }
    }
    return ran;
}

// ---- Process handles (Node's process_wrap, libuv's uv_spawn) ----------------
// A child process is an id into a process-wide table. Its stdio pipes are
// stream handles (the TCP table above: POSIX socketpairs, as libuv's
// UV_CREATE_PIPE makes them), read back with processStdio after the spawn.
// The exit is noticed through a SIGCHLD self-pipe in the reactor's read set
// (POSIX) or the child-exit wake on the reactor's port (Windows); the
// dispatch reaps with waitpid(WNOHANG) and runs onExit(exitCode, signal).
#ifndef _WIN32
#include <signal.h>
#include <sys/wait.h>
extern char **environ;
#else
extern int __kml_win_spawn(const char *file, char **argv, const char *cwd, int in_fd, int out_fd, int err_fd, int inherit_fd, int flags, char **spawn_env);
extern int __kml_win_spawn_failed(int pid);
extern void __kml_win_child_watch(int pid);
extern int __kml_win_exit_code(int pid);
extern int waitpid(int pid, int *status, int options);
extern int kill(int pid, int sig);
extern int pipe(int fds[2]);
extern int socketpair(int domain, int type, int protocol, int sv[2]);
#ifndef WNOHANG
#define WNOHANG 1
#endif
#ifndef WIFEXITED
#define WIFEXITED(s) (((s) & 0x7f) == 0)
#define WEXITSTATUS(s) (((s) >> 8) & 0xff)
#define WIFSIGNALED(s) (((s) & 0x7f) != 0)
#define WTERMSIG(s) ((s) & 0x7f)
#endif
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
} kml_proch;

static __thread kml_proch **proc_tab = NULL;
static __thread int proc_cap = 0, proc_n = 0;
// A pass that doesn't block after a spawn: a child that exited before the
// watch saw it is still reaped at once.
static int proc_kick = 0;
#define KML_MAX_STDIO 32
static int proc_last_stdio[KML_MAX_STDIO];

#ifndef _WIN32
static int sigchld_pipe[2] = {-1, -1};
static struct sigaction sigchld_prev;

static void kml_sigchld(int sig) {
    int saved = errno;
    if (sigchld_pipe[1] >= 0) {
        char b = 1;
        ssize_t w = write(sigchld_pipe[1], &b, 1);
        (void)w;
    }
    // A handler installed before ours still runs.
    if (!(sigchld_prev.sa_flags & SA_SIGINFO) && sigchld_prev.sa_handler != SIG_DFL && sigchld_prev.sa_handler != SIG_IGN)
        sigchld_prev.sa_handler(sig);
    errno = saved;
}

static void proc_watch_init(void) {
    if (sigchld_pipe[0] >= 0) return;
    if (pipe(sigchld_pipe) != 0) return;
    for (int i = 0; i < 2; i++) {
        set_nonblock(sigchld_pipe[i]);
        fcntl(sigchld_pipe[i], F_SETFD, FD_CLOEXEC);
    }
    struct sigaction sa;
    memset(&sa, 0, sizeof sa);
    sa.sa_handler = kml_sigchld;
    sigemptyset(&sa.sa_mask);
    sa.sa_flags = SA_RESTART | SA_NOCLDSTOP;
    sigaction(SIGCHLD, &sa, &sigchld_prev);
}
#endif

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
    proc_kick = 1;
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
#endif

// Spawn file with argv (argv[0] the name the child sees) and env (empty:
// ours), in cwd ("" is ours). flags: bit 0 detached, bit 1
// windowsVerbatimArguments, bit 2 a deliberate self-run (fork). uid/gid < 0
// keep ours. Returns the process
// handle, or -errno when the program could not be started. onExit(exitCode,
// signal) runs on the loop once the child has been reaped.
double __kml_native_process_spawn(const char *file, void *argv_buf, int64_t argv_size, void *env_buf, int64_t env_size, const char *cwd, double flags, const char *stdio, double uid, double gid, void *inv, void *clo) {
    char kind[KML_MAX_STDIO];
    int want[KML_MAX_STDIO], parent_end[KML_MAX_STDIO], child_end[KML_MAX_STDIO];
    int nst = proc_stdio_parse(stdio, kind, want);
    if (nst < 0) return -EINVAL;
    for (int i = 0; i < KML_MAX_STDIO; i++) proc_last_stdio[i] = -1;
    for (int i = 0; i < nst; i++) parent_end[i] = child_end[i] = -1;
    int err = 0;
    for (int i = 0; i < nst && !err; i++) {
        if (kind[i] == 'p' || kind[i] == 'c') {
            int sv[2];
#ifdef _WIN32
            // stdin/stdout/stderr are pipes the child owns an end of; the IPC
            // channel is the one descriptor it inherits by number.
            int rc = kind[i] == 'c' ? socketpair(AF_UNIX, SOCK_STREAM, 0, sv) : pipe(sv);
            if (rc != 0) { err = errno; break; }
            if (kind[i] == 'c' || i != 0) {
                parent_end[i] = sv[0];
                child_end[i] = sv[1];
            } else {
                parent_end[i] = sv[1];
                child_end[i] = sv[0];
            }
#else
            if (socketpair(AF_UNIX, SOCK_STREAM, 0, sv) != 0) { err = errno; break; }
            parent_end[i] = sv[0];
            child_end[i] = sv[1];
            fcntl(sv[0], F_SETFD, FD_CLOEXEC);
#endif
        } else if (kind[i] == 'i') {
#ifdef _WIN32
            child_end[i] = -2;
#else
            child_end[i] = open("/dev/null", i == 0 ? O_RDONLY : O_RDWR);
            if (child_end[i] < 0) err = errno;
#endif
        } else {
            child_end[i] = want[i];
        }
    }
    char **argv = proc_strings((const char *)argv_buf, argv_size);
    char **envp = env_size > 0 ? proc_strings((const char *)env_buf, env_size) : NULL;
    int pid = -1;
    if (!err) {
#ifdef _WIN32
        int in = nst > 0 ? (kind[0] == 'f' && want[0] == 0 ? -1 : child_end[0]) : -1;
        int out = nst > 1 ? (kind[1] == 'f' && want[1] == 1 ? -1 : child_end[1]) : -1;
        int errfd = nst > 2 ? (kind[2] == 'f' && want[2] == 2 ? -1 : child_end[2]) : -1;
        int ipc = -1;
        for (int i = 3; i < nst; i++)
            if (kind[i] == 'c') ipc = child_end[i];
        // 0x10000 marks a self-spawn for the child's startup guard (ADR-00972),
        // unless this is a deliberate self-run (fork).
        pid = __kml_win_spawn(file, argv, cwd && *cwd ? cwd : NULL, in, out, errfd, ipc, (((int)flags & 2) ? 1 : 0) | (((int)flags & 4) ? 0 : 0x10000), envp);
        if (pid < 0) err = errno ? errno : ENOENT;
        else if (__kml_win_spawn_failed(pid)) {
            err = __kml_win_spawn_failed(pid);
            waitpid(pid, NULL, 0);
            pid = -1;
        } else {
            __kml_win_child_watch(pid);
        }
        (void)uid; (void)gid;
#else
        proc_watch_init();
        int reexec = !((int)flags & 4) && proc_is_self(file);
        if (reexec && envp) {
            // The marker joins the given environment.
            int n = 0;
            while (envp[n]) n++;
            char **grown = (char **)realloc(envp, ((size_t)n + 2) * sizeof *envp);
            if (grown) {
                envp = grown;
                envp[n] = strdup("KML_KLAIN_REEXEC=1");
                envp[n + 1] = NULL;
            }
        }
        int errpipe[2];
        if (pipe(errpipe) != 0) {
            err = errno;
        } else {
            fcntl(errpipe[0], F_SETFD, FD_CLOEXEC);
            fcntl(errpipe[1], F_SETFD, FD_CLOEXEC);
            pid = fork();
            if (pid == 0) {
                // The child: default signal dispositions and an empty mask,
                // as libuv's uv__process_child_init gives it.
                sigset_t none;
                sigemptyset(&none);
                sigprocmask(SIG_SETMASK, &none, NULL);
                for (int sg = 1; sg < 32; sg++) {
                    if (sg == SIGKILL || sg == SIGSTOP) continue;
                    signal(sg, SIG_DFL);
                }
                if ((int)flags & 1) setsid();
                // Move every end out of the way of the descriptors it lands on.
                for (int i = 0; i < nst; i++) {
                    if (child_end[i] >= 0 && child_end[i] < nst && child_end[i] != i) {
                        int moved = fcntl(child_end[i], F_DUPFD, nst);
                        if (moved >= 0) child_end[i] = moved;
                    }
                }
                for (int i = 0; i < nst; i++) {
                    if (child_end[i] < 0) continue;
                    if (child_end[i] == i) {
                        fcntl(i, F_SETFD, 0);
                    } else if (dup2(child_end[i], i) < 0) {
                        int e = errno;
                        ssize_t w = write(errpipe[1], &e, sizeof e);
                        (void)w;
                        _exit(127);
                    }
                }
                if (cwd && *cwd && chdir(cwd) != 0) {
                    int e = errno;
                    ssize_t w = write(errpipe[1], &e, sizeof e);
                    (void)w;
                    _exit(127);
                }
                if (gid >= 0 && setgid((gid_t)gid) != 0) {
                    int e = errno;
                    ssize_t w = write(errpipe[1], &e, sizeof e);
                    (void)w;
                    _exit(127);
                }
                if (uid >= 0 && setuid((uid_t)uid) != 0) {
                    int e = errno;
                    ssize_t w = write(errpipe[1], &e, sizeof e);
                    (void)w;
                    _exit(127);
                }
                if (envp) environ = envp;
                else if (reexec) setenv("KML_KLAIN_REEXEC", "1", 1);
                execvp(file, argv);
                int e = errno;
                ssize_t w = write(errpipe[1], &e, sizeof e);
                (void)w;
                _exit(127);
            }
            if (pid < 0) err = errno;
            close(errpipe[1]);
            if (pid > 0) {
                int e = 0;
                ssize_t r;
                do {
                    r = read(errpipe[0], &e, sizeof e);
                } while (r < 0 && errno == EINTR);
                if (r == (ssize_t)sizeof e) {
                    // exec failed: reap the child, report its errno.
                    waitpid(pid, NULL, 0);
                    err = e;
                    pid = -1;
                }
            }
            close(errpipe[0]);
        }
#endif
    }
    // Close the child's ends (the ignored ones we opened too), keep ours.
    for (int i = 0; i < nst; i++) {
        if ((kind[i] == 'p' || kind[i] == 'c' || kind[i] == 'i') && child_end[i] >= 0) close(child_end[i]);
    }
    proc_free_strings(argv);
    proc_free_strings(envp);
    if (err) {
        for (int i = 0; i < nst; i++)
            if (parent_end[i] >= 0) close(parent_end[i]);
        return -(double)err;
    }
    for (int i = 0; i < nst; i++) {
        if (parent_end[i] < 0) continue;
        set_nonblock(parent_end[i]);
        proc_last_stdio[i] = tcp_new(parent_end[i]);
    }
    int id = proc_new(pid, inv, clo);
    for (int k = 0; k < KML_MAX_STDIO_H; k++) proc_tab[id]->stdio[k] = k < KML_MAX_STDIO ? proc_last_stdio[k] : -1;
    return id;
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
    if (kill(p->pid, (int)signal) != 0) return -errno;
    return 0;
}

// A referenced child holds the loop open until it exits.
void __kml_native_process_ref(double id, _Bool on) {
    kml_proch *p = proc_get(id);
    if (p) p->refd = on;
}

// The handle is done with: its exit is no longer reported.
void __kml_native_process_close(double id) {
    kml_proch *p = proc_get(id);
    if (p) {
        p->exit_inv = NULL;
        p->refd = 0;
    }
}

static _Bool proc_keepalive(void) {
    for (int i = 0; i < proc_n; i++)
        if (proc_tab[i]->alive && proc_tab[i]->refd && proc_tab[i]->exit_inv) return 1;
    return 0;
}

static _Bool proc_fdset_add(void *fdset, int *maxfd) {
    int any = 0;
    for (int i = 0; i < proc_n; i++)
        if (proc_tab[i]->alive) any = 1;
    if (!any) return 0;
#ifndef _WIN32
    if (sigchld_pipe[0] >= 0) {
        KML_FD_SET(sigchld_pipe[0], fdset);
        if (sigchld_pipe[0] > *maxfd) *maxfd = sigchld_pipe[0];
    }
#else
    (void)fdset;
    (void)maxfd;
#endif
    _Bool kick = proc_kick != 0;
    proc_kick = 0;
    return kick;
}

static _Bool proc_dispatch(void) {
#ifndef _WIN32
    if (sigchld_pipe[0] >= 0) {
        char buf[64];
        while (read(sigchld_pipe[0], buf, sizeof buf) > 0) { /* drain */ }
    }
#endif
    _Bool ran = 0;
    for (int i = 0; i < proc_n; i++) {
        kml_proch *p = proc_tab[i];
        if (p->pending) {
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
        if (!p->alive) continue;
        int st = 0;
        int r = waitpid(p->pid, &st, WNOHANG);
        if (r == 0 || (r < 0 && errno == EINTR)) continue;
        p->alive = 0;
        double code = 0, sig = 0;
        if (r > 0) {
            if (WIFEXITED(st)) code = WEXITSTATUS(st);
            else if (WIFSIGNALED(st)) sig = WTERMSIG(st);
#ifdef _WIN32
            int wide = __kml_win_exit_code(p->pid);
            if (sig == 0 && wide != -1) code = (double)(unsigned int)wide;
#endif
        }
        if (!p->refd) {
            p->pending = 1;
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

double __kml_native_spawn_sync(const char *file, void *argv_buf, int64_t argv_size, void *env_buf, int64_t env_size, const char *cwd, double flags, const char *stdio, double uid, double gid, void *input, int64_t input_size, double timeout, double maxBuffer, double killSignal) {
    for (int i = 0; i < KML_MAX_STDIO; i++) {
        free(sync_out[i]);
        sync_out[i] = NULL;
        sync_len[i] = 0;
    }
    sync_status = sync_signal = sync_error = sync_pid = 0;
    double id = __kml_native_process_spawn(file, argv_buf, argv_size, env_buf, env_size, cwd, flags, stdio, uid, gid, NULL, NULL);
    if (id < 0) return id;
    kml_proch *p = proc_get(id);
    sync_pid = p->pid;
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
    int st = 0;
    int r;
    do {
        r = waitpid(p->pid, &st, 0);
    } while (r < 0 && errno == EINTR);
    p->alive = 0;
    p->exit_inv = NULL;
    if (r > 0) {
        if (WIFEXITED(st)) sync_status = WEXITSTATUS(st);
        else if (WIFSIGNALED(st)) sync_signal = WTERMSIG(st);
#ifdef _WIN32
        int wide = __kml_win_exit_code(p->pid);
        if (sync_signal == 0 && wide != -1) sync_status = (int64_t)(unsigned int)wide;
#endif
    }
    return 0;
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
#ifdef SIGHUP
        {"SIGHUP", SIGHUP},
#endif
        {"SIGINT", 2}, {"SIGQUIT", 3}, {"SIGILL", 4}, {"SIGTRAP", 5}, {"SIGABRT", 6},
        {"SIGFPE", 8}, {"SIGKILL", 9}, {"SIGSEGV", 11}, {"SIGPIPE", 13}, {"SIGALRM", 14}, {"SIGTERM", 15},
#ifndef _WIN32
        {"SIGBUS", SIGBUS}, {"SIGUSR1", SIGUSR1}, {"SIGUSR2", SIGUSR2}, {"SIGCHLD", SIGCHLD},
        {"SIGCONT", SIGCONT}, {"SIGSTOP", SIGSTOP}, {"SIGTSTP", SIGTSTP}, {"SIGTTIN", SIGTTIN},
        {"SIGTTOU", SIGTTOU}, {"SIGURG", SIGURG}, {"SIGXCPU", SIGXCPU}, {"SIGXFSZ", SIGXFSZ},
        {"SIGVTALRM", SIGVTALRM}, {"SIGPROF", SIGPROF}, {"SIGWINCH", SIGWINCH}, {"SIGIO", SIGIO},
        {"SIGSYS", SIGSYS},
#else
        {"SIGBREAK", 21}, {"SIGWINCH", 28},
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
                                  "SIGXCPU", "SIGXFSZ", "SIGVTALRM", "SIGPROF", "SIGWINCH", "SIGIO", "SIGSYS"};
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
    (void)readable;
    return fd >= 0 ? tcp_new(fd) : -EBADF;
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

// ---- event-loop hooks (called from the emitted reactor) --------------------
// An outstanding submission keeps this loop alive so it doesn't exit while a
// read is in flight and never settles the awaiting Promise.
_Bool __kml_pool_keepalive(void) {
    return tls_port && atomic_load_explicit(&tls_port->inflight,
                                            memory_order_relaxed) > 0;
}

// Add the wakeup read fd to the loop's read set while work is in flight. Returns
// 0 (no forced-zero timeout): we *want* select() to block on the wakefd so the
// loop sleeps until a completion lands rather than spinning.
_Bool __kml_pool_fdset_add(void *fdset, int *maxfd) {
#ifdef _WIN32
    // The wake is a packet on the reactor's port, which select() always waits on.
    (void)fdset; (void)maxfd;
    return 0;
#else
    // Watched whenever the loop has a port at all: another thread (a Worker,
    // a MessagePort's other end) may wake it with nothing in flight here.
    if (!tls_port || tls_port->wake_r < 0)
        return 0;
    FD_SET(tls_port->wake_r, (fd_set *)fdset);
    if (tls_port->wake_r > *maxfd) *maxfd = tls_port->wake_r;
    return 0;
#endif
}

// Drain arrived completions on the loop thread. The comp stack is LIFO: reverse
// the batch to completion order before dispatching.
// Returns whether any completion ran.
_Bool __kml_pool_dispatch(void) {
    kml_loop_port *p = tls_port;
    if (!p) return 0;
#ifndef _WIN32
    if (p->wake_r >= 0) {
        char buf[64];
        while (read(p->wake_r, buf, sizeof buf) > 0) { /* drain wakeup bytes */ }
    }
#endif
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
        atomic_fetch_sub_explicit(&p->inflight, 1, memory_order_relaxed);
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
    atomic_fetch_add_explicit(&loop->inflight, 1, memory_order_relaxed);
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
        atomic_fetch_add_explicit(&p->loop->inflight, 1, memory_order_relaxed);
    } else if (!want && p->held) {
        p->held = 0;
        atomic_fetch_sub_explicit(&p->loop->inflight, 1, memory_order_relaxed);
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
        if (p->held) { p->held = 0; atomic_fetch_sub_explicit(&p->loop->inflight, 1, memory_order_relaxed); }
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
        if (p->held) { p->held = 0; atomic_fetch_sub_explicit(&p->loop->inflight, 1, memory_order_relaxed); }
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

// Start a worker of the registered module at path: its id, or -1 when no
// worker module has that path, -errno when the thread fails to start.
double __kml_native_worker_spawn(const char *path, double port, double iport, int64_t data, void *inv, void *clo) {
    kml_wentry *e = NULL;
    for (int i = 0; i < went_n; i++)
        if (path && strcmp(went[i].path, path) == 0) e = &went[i];
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
    atomic_fetch_add_explicit(&w->parent->inflight, 1, memory_order_relaxed);
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
        atomic_fetch_sub_explicit(&w->parent->inflight, 1, memory_order_relaxed);
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
        atomic_fetch_add_explicit(&w->parent->inflight, 1, memory_order_relaxed);
    } else if (!want && w->held) {
        w->held = 0;
        atomic_fetch_sub_explicit(&w->parent->inflight, 1, memory_order_relaxed);
    }
    pthread_mutex_unlock(&w_mu);
}

static void kml_noop_cb(void *clo, double a, double b) { (void)clo; (void)a; (void)b; }

// Stop the worker: its loop ends at its next turn, with exit code 1.
void __kml_native_worker_terminate(double id) {
    kml_wctx *w = w_get(id);
    if (!w) return;
    atomic_store(&w->terminate, 1);
    pthread_mutex_lock(&w_mu);
    if (w->loop && !w->exited) kml_post_to_loop(w->loop, (void *)kml_noop_cb, NULL, 0, 0);
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
    if (atomic_load(&w->terminate)) kml_post_to_loop(w->loop, (void *)kml_noop_cb, NULL, 0, 0);
    pthread_mutex_unlock(&w_mu);
    kml_post_to_loop(w->parent, w->inv, w->clo, 0, 0);
    return w->entry;
}

_Bool __kml_worker_is_thread(void) { return w_self != NULL; }

_Bool __kml_worker_terminating(void) {
    return w_self && atomic_load(&w_self->terminate);
}

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
        atomic_fetch_sub_explicit(&w->parent->inflight, 1, memory_order_relaxed);
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
