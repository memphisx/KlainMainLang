// klainpool.c — libuv-style blocking-work thread pool for real async fs I/O.
// TDD-00185, Stages 1-2.
//
// The pool runs blocking syscalls off the single event-loop thread; the Promise
// is settled back ON the loop thread. That invariant is the whole point: the
// loop, fiber, microtask and Promise state are all thread-local and the reactor
// is thread-pinned, so a worker must never touch a Promise or the JS/GC heap
// directly — it runs the syscall and hands a plain result back, and the loop
// thread does everything JS-visible at drain time.
//
// Division of labour:
//   * Threading (pthreads, condvar, atomics, the socketpair wakeup) lives here.
//   * The Promise/exception *layout* lives in the emitted IR, exported as two
//     thunks this file calls: __kml_pool_thunk_readfile (runs the existing
//     throwing sync helper under a per-worker setjmp guard, returns
//     {result, err}) and __kml_pool_settle (stores the value word and calls
//     __kml_promise_settle) — so this file needs no knowledge of either struct.
//
// The wakeup is per loop. On POSIX it is a socketpair whose read end sits in the
// loop's select() set. On Windows the reactor's one wait is the loop thread's
// completion port (TDD-00183), so a worker wakes it with an addressed packet to
// that port — no descriptor, nothing in the fd sets.

#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <stdint.h>
#include <errno.h>
#ifdef _WIN32
// win32io.c: the calling thread's reactor port, and a wake addressed to a port.
extern void *__kml_win_reactor_port(void);
extern void __kml_win_port_wake(void *port);
#else
#include <sys/select.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <arpa/inet.h>
#include <netdb.h>
#include <sys/un.h>
#include <poll.h>
#endif

// ---- IR-exported thunks (Promise/exception layout lives in the emitted IR) --
// Each thunk runs one fs op under a per-worker setjmp guard and returns two
// result words + a caught-Error pointer: {v0, v1, err}. err != NULL means the
// op threw (reject with it); otherwise (v0, v1) are the fulfil value — a single
// pointer for readFile (v0), a {ptr,len} pair for readdir (v0, v1), or ignored
// for the void ops. Struct layout matches the IR {i64, i64, ptr}.
// Written through an out-pointer, not returned by value (see buildPoolThunk).
struct kml_triple { int64_t v0; int64_t v1; void *err; };
extern void __kml_pool_thunk_readfile(const char *path, struct kml_triple *out);
extern void __kml_pool_thunk_writefile(const char *path, const char *data, struct kml_triple *out);
extern void __kml_pool_thunk_appendfile(const char *path, const char *data, struct kml_triple *out);
extern void __kml_pool_thunk_unlink(const char *path, struct kml_triple *out);
extern void __kml_pool_thunk_mkdir(const char *path, struct kml_triple *out);
extern void __kml_pool_thunk_rmdir(const char *path, struct kml_triple *out);
extern void __kml_pool_thunk_rename(const char *oldp, const char *newp, struct kml_triple *out);
extern void __kml_pool_thunk_copyfile(const char *src, const char *dst, struct kml_triple *out);
extern void __kml_pool_thunk_readdir(const char *path, struct kml_triple *out);
// TDD-00185 binary writes: explicit byte buffer + length (an ArrayBuffer/
// TypedArray body, copied raw at submit so no GC pointer crosses the thread).
extern void __kml_pool_thunk_writefile_bytes(const char *path, const void *data, int64_t len, struct kml_triple *out);
extern void __kml_pool_thunk_appendfile_bytes(const char *path, const void *data, int64_t len, struct kml_triple *out);
extern void __kml_pool_thunk_readfile_bytes(const char *path, struct kml_triple *out);
extern void __kml_pool_settle(void *promise, int64_t v0, int64_t v1, int64_t state);

#ifdef KLAINPOOL_GC
// Under -mm=gc a worker allocates GC memory (the read buffer / result string),
// so it must register with Boehm before its first allocation — mirroring the
// Worker/klain:sync threads (TDD-00098).
extern int GC_get_stack_base(void *);
extern int GC_register_my_thread(void *);
#ifdef KLAINPOOL_GC_ENABLE
// GC_allow_register_threads is call-once and shared across concurrency
// subsystems; the build defines this macro only when the pool is the sole owner
// of the enable (no Worker modules, no klain:sync — see embedded_c.go).
extern void GC_allow_register_threads(void);
#endif
#endif

// A completion's drain action on the loop thread: a one-shot fs op settles its
// Promise; a native operation calls its callback.
enum {
    KML_CMP_SETTLE = 0,      // settle a Promise (target = promise, v0/v1/state)
    KML_CMP_CALL,            // a native operation's callback (inv/clo, err/result)
};

// One struct, reused as work item (loop -> pool) and completion item (pool ->
// loop). arg0/arg1 are strdup'd copies the item owns and frees.
typedef struct kml_pool_item {
    struct kml_pool_item *next;
    int opid;               // work: which fs op (KML_OP_*)
    int kind;               // completion: drain action (KML_CMP_*)
    void *promise;          // completion target: a Promise
    char *arg0;
    char *arg1;
    void *data;             // binary write: raw malloc'd byte buffer the item owns
    int64_t datalen;        // binary write: its byte length
    struct kml_loop_port *port;
    int64_t state;          // worker fills: 1 fulfilled / 2 rejected
    int64_t v0;             // worker fills: result word 0 (or Error ptr on reject)
    int64_t v1;             // worker fills: result word 1 (readdir's length; else 0)
    // A native operation (KML_OP_NATIVE): its work, run on a worker, fills
    // err/res; the loop then calls inv(clo, err, res). a[] and buf/buflen are
    // its arguments. live links it into native_live while it is pending.
    void (*work)(struct kml_pool_item *);
    void *inv;
    void *clo;
    int64_t a[4];
    void *buf;
    int64_t buflen;
    int64_t err;            // a positive errno, or 0
    double res;
    char *res_str;          // a string result, read by nativeLastString()
    struct kml_pool_item *live_prev, *live_next;
} kml_pool_item;

// Op ids — must match the lowering in emit_fs_async.go.
enum {
    KML_OP_READFILE = 0,
    KML_OP_WRITEFILE,
    KML_OP_APPENDFILE,
    KML_OP_UNLINK,
    KML_OP_MKDIR,
    KML_OP_RMDIR,
    KML_OP_RENAME,
    KML_OP_COPYFILE,
    KML_OP_READDIR,
    KML_OP_WRITEFILE_BYTES,  // TDD-00185: writeFile of a raw byte buffer
    KML_OP_APPENDFILE_BYTES, // TDD-00185: appendFile of a raw byte buffer
    KML_OP_READFILE_BYTES,   // readFile with no encoding: the file's bytes
    KML_OP_NATIVE,           // a native operation: the item's own work function
};

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
    if (it->opid == KML_OP_NATIVE) {
        it->work(it);
        it->kind = KML_CMP_CALL;
        return;
    }
    struct kml_triple r = { 0, 0, NULL };
    switch (it->opid) {
    case KML_OP_READFILE:   __kml_pool_thunk_readfile(it->arg0, &r); break;
    case KML_OP_WRITEFILE:  __kml_pool_thunk_writefile(it->arg0, it->arg1, &r); break;
    case KML_OP_APPENDFILE: __kml_pool_thunk_appendfile(it->arg0, it->arg1, &r); break;
    case KML_OP_UNLINK:     __kml_pool_thunk_unlink(it->arg0, &r); break;
    case KML_OP_MKDIR:      __kml_pool_thunk_mkdir(it->arg0, &r); break;
    case KML_OP_RMDIR:      __kml_pool_thunk_rmdir(it->arg0, &r); break;
    case KML_OP_RENAME:     __kml_pool_thunk_rename(it->arg0, it->arg1, &r); break;
    case KML_OP_COPYFILE:   __kml_pool_thunk_copyfile(it->arg0, it->arg1, &r); break;
    case KML_OP_READDIR:    __kml_pool_thunk_readdir(it->arg0, &r); break;
    case KML_OP_WRITEFILE_BYTES:  __kml_pool_thunk_writefile_bytes(it->arg0, it->data, it->datalen, &r); break;
    case KML_OP_APPENDFILE_BYTES: __kml_pool_thunk_appendfile_bytes(it->arg0, it->data, it->datalen, &r); break;
    case KML_OP_READFILE_BYTES:   __kml_pool_thunk_readfile_bytes(it->arg0, &r); break;
    default:                r.err = (void *)1; break;
    }
    it->kind = KML_CMP_SETTLE;
    if (r.err) {                                  // threw -> reject with the Error
        it->state = 2;
        it->v0 = (int64_t)(intptr_t)r.err;
        it->v1 = 13;                              // caught-value tag kmlTagError:
                                                  // v0/v1 double as the rejection
                                                  // reason's (payload, tag) so a
                                                  // .catch/await sees a real Error
                                                  // (TDD-00207)
    } else {                                      // fulfil with (v0, v1)
        it->state = 1;
        it->v0 = r.v0;
        it->v1 = r.v1;
    }
}

static void *worker_main(void *arg) {
    (void)arg;
#ifdef KLAINPOOL_GC
    void *sb[2];
    GC_get_stack_base(sb);
    GC_register_my_thread(sb);
#endif
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

// Started lazily on the first submit, under q_mu.
static void ensure_pool_locked(void) {
    if (pool_started) return;
    pool_started = 1;
#if defined(KLAINPOOL_GC) && defined(KLAINPOOL_GC_ENABLE)
    // Permit the not-GC-created worker threads to register themselves with
    // Boehm before any of them starts allocating.
    GC_allow_register_threads();
#endif
    const char *env = getenv("UV_THREADPOOL_SIZE");
    if (env && *env) {
        int v = atoi(env);
        if (v > 0) pool_size = v > 1024 ? 1024 : v;
    }
    for (int i = 0; i < pool_size; i++) {
        pthread_t t;
        if (pthread_create(&t, NULL, worker_main, NULL) == 0)
            pthread_detach(t);
    }
}

// ---- submit (called on the loop thread) ------------------------------------
// Enqueue a prepared work item and bump this loop's inflight count, which its
// completion decrements: the loop stays alive while it is in flight.
static void enqueue_work(kml_loop_port *p, kml_pool_item *it) {
    it->port = p;
    atomic_fetch_add_explicit(&p->inflight, 1, memory_order_relaxed);
    pthread_mutex_lock(&q_mu);
    ensure_pool_locked();
    if (q_tail) q_tail->next = it; else q_head = it;
    q_tail = it;
    pthread_cond_signal(&q_cv);
    pthread_mutex_unlock(&q_mu);
}

// arg0/arg1 are the op's string arguments (arg1 NULL for a 1-arg op); both are
// strdup'd so the item owns copies that outlive the caller's JS values.
void __kml_pool_submit(int opid, void *promise, const char *arg0, const char *arg1) {
    kml_pool_item *it = (kml_pool_item *)calloc(1, sizeof *it);
    it->opid = opid;
    it->promise = promise;
    it->arg0 = arg0 ? strdup(arg0) : NULL;
    it->arg1 = arg1 ? strdup(arg1) : NULL;
    enqueue_work(loop_port(), it);
}

// TDD-00185: submit a binary writeFile/appendFile. The byte buffer is copied
// into a raw malloc'd block the item owns (never a GC pointer, so the worker is
// GC-safe), mirroring the strdup of arg0; freed alongside arg0/arg1 at drain.
void __kml_pool_submit_write_bytes(int opid, void *promise, const char *path, const void *data, int64_t len) {
    kml_pool_item *it = (kml_pool_item *)calloc(1, sizeof *it);
    it->opid = opid;
    it->promise = promise;
    it->arg0 = path ? strdup(path) : NULL;
    it->datalen = len;
    if (len > 0 && data) {
        it->data = malloc((size_t)len);
        if (it->data) memcpy(it->data, data, (size_t)len);
    }
    enqueue_work(loop_port(), it);
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
    it->opid = KML_OP_NATIVE;
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
#include <io.h>
#define kml_fsync _commit
// No pread/pwrite in the C runtime: seek, then transfer. A positioned
// transfer on a descriptor shared across workers is not atomic here.
static int64_t kml_pread(int fd, void *b, size_t n, int64_t pos) {
    if (_lseeki64(fd, pos, SEEK_SET) < 0) return -1;
    return _read(fd, b, (unsigned)n);
}
static int64_t kml_pwrite(int fd, const void *b, size_t n, int64_t pos) {
    if (_lseeki64(fd, pos, SEEK_SET) < 0) return -1;
    return _write(fd, b, (unsigned)n);
}
#else
#define kml_fsync fsync
static int64_t kml_pread(int fd, void *b, size_t n, int64_t pos) { return pread(fd, b, n, (off_t)pos); }
static int64_t kml_pwrite(int fd, const void *b, size_t n, int64_t pos) { return pwrite(fd, b, n, (off_t)pos); }
#endif

static void work_open(kml_pool_item *it) {
#ifdef O_CLOEXEC
    int fd = open(it->arg0, (int)it->a[0] | O_CLOEXEC, (int)it->a[1]);
#else
    int fd = open(it->arg0, (int)it->a[0], (int)it->a[1]);
#endif
    if (fd < 0) it->err = errno; else it->res = fd;
}

static void work_close(kml_pool_item *it) {
    if (close((int)it->a[0]) != 0) it->err = errno;
}

static void work_fsync(kml_pool_item *it) {
    if (kml_fsync((int)it->a[0]) != 0) it->err = errno;
}

// a[0] fd, a[1] length, a[2] position (-1: the file's current position); buf
// is the region to fill or send.
static void work_read(kml_pool_item *it) {
    int64_t n;
    do {
        n = it->a[2] < 0 ? read((int)it->a[0], it->buf, (size_t)it->a[1])
                         : kml_pread((int)it->a[0], it->buf, (size_t)it->a[1], it->a[2]);
    } while (n < 0 && errno == EINTR);
    if (n < 0) it->err = errno; else it->res = (double)n;
}

static void work_write(kml_pool_item *it) {
    int64_t n;
    do {
        n = it->a[2] < 0 ? write((int)it->a[0], it->buf, (size_t)it->a[1])
                         : kml_pwrite((int)it->a[0], it->buf, (size_t)it->a[1], it->a[2]);
    } while (n < 0 && errno == EINTR);
    if (n < 0) it->err = errno; else it->res = (double)n;
}

void __kml_native_fs_open(const char *path, double flags, double mode, void *inv, void *clo) {
    kml_pool_item *it = native_item(work_open, inv, clo);
    it->arg0 = strdup(path ? path : "");
    it->a[0] = (int64_t)flags;
    it->a[1] = (int64_t)mode;
    native_submit(it);
}

void __kml_native_fs_close(double fd, void *inv, void *clo) {
    kml_pool_item *it = native_item(work_close, inv, clo);
    it->a[0] = (int64_t)fd;
    native_submit(it);
}

void __kml_native_fs_fsync(double fd, void *inv, void *clo) {
    kml_pool_item *it = native_item(work_fsync, inv, clo);
    it->a[0] = (int64_t)fd;
    native_submit(it);
}

// The region [offset, offset+length) of the buffer (data, size); the caller
// has validated it, and a region past the end is clamped, never overrun.
static void native_region(kml_pool_item *it, void *data, int64_t size, double offset, double length) {
    int64_t off = (int64_t)offset, len = (int64_t)length;
    if (off < 0) off = 0;
    if (off > size) off = size;
    if (len < 0) len = 0;
    if (len > size - off) len = size - off;
    it->buf = (char *)data + off;
    it->a[1] = len;
}

void __kml_native_fs_read(double fd, void *data, int64_t size, double offset, double length, double position, void *inv, void *clo) {
    kml_pool_item *it = native_item(work_read, inv, clo);
    it->a[0] = (int64_t)fd;
    native_region(it, data, size, offset, length);
    it->a[2] = position >= 0 ? (int64_t)position : -1;
    native_submit(it);
}

void __kml_native_fs_write(double fd, void *data, int64_t size, double offset, double length, double position, void *inv, void *clo) {
    kml_pool_item *it = native_item(work_write, inv, clo);
    it->a[0] = (int64_t)fd;
    native_region(it, data, size, offset, length);
    it->a[2] = position >= 0 ? (int64_t)position : -1;
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

#ifndef _WIN32 // the TCP handles and dns.lookup: POSIX sockets (net keeps its codegen form on Windows)
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

static kml_tcp **tcp_tab = NULL;
static int tcp_cap = 0, tcp_n = 0;

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

static int64_t tcp_recv(kml_tcp *h, char *p, int64_t n) {
    if (h->tls && tcp_tls) return tcp_tls->read(h->tls, p, n);
    int64_t r = read(h->fd, p, (size_t)n);
    if (r < 0) return (errno == EWOULDBLOCK || errno == EINTR) ? -EAGAIN : -errno;
    return r;
}

static void set_nonblock(int fd) {
    int fl = fcntl(fd, F_GETFL, 0);
    fcntl(fd, F_SETFL, fl | O_NONBLOCK);
}

typedef void (*kml_inv2)(void *, double, double);
#define TCP_CALL(inv, clo, a, b) ((kml_inv2)(inv))((clo), (double)(a), (double)(b))

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
            int off = 0;
            setsockopt(fd, IPPROTO_IPV6, IPV6_V6ONLY, &off, sizeof off);
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

static int tcp_flush(kml_tcp *h) {
    while (h->wq_head) {
        kml_wreq *w = h->wq_head;
        while (w->off < w->len) {
            int64_t n = tcp_send(h, w->data + w->off, w->len - w->off);
            if (n < 0) {
                if (n == -EAGAIN) return 0;
                return (int)-n;
            }
            w->off += n;
        }
        h->wq_head = w->next;
        if (!h->wq_head) h->wq_tail = NULL;
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
double __kml_native_tcp_write(double id, void *buf, int64_t size, double offset, double length, void *inv, void *clo) {
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
            int64_t n = tcp_send(h, src + done, len - done);
            if (n < 0) {
                if (n == -EAGAIN) break;
                return n;
            }
            done += n;
        }
        if (done == len) return 1;
    }
    kml_wreq *w = (kml_wreq *)calloc(1, sizeof *w);
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

static int tcp_active(kml_tcp *h) {
    return !h->closed_notified && (h->fd >= 0 || h->closing);
}

_Bool __kml_tcp_keepalive(void) {
    for (int i = 0; i < tcp_n; i++) {
        kml_tcp *h = tcp_tab[i];
        if (tcp_active(h) && (h->refd || h->closing || h->wq_head)) return 1;
    }
    return 0;
}

// Add the handles' fds to the reactor's sets: read interest for a
// listening server or a reading stream, write interest for a connect or
// queued writes. Returns true when work is ready without waiting.
_Bool __kml_tcp_fdset_add(void *fdset, void *wfdset, int *maxfd) {
    _Bool now = 0;
    for (int i = 0; i < tcp_n; i++) {
        kml_tcp *h = tcp_tab[i];
        if (h->closing && !h->closed_notified) { now = 1; continue; }
        if (h->fd < 0) continue;
        if (h->connecting > 1) { now = 1; continue; }
        if (h->listening || (h->reading && !h->read_eof) || h->tls_state == 1) {
            FD_SET(h->fd, (fd_set *)fdset);
            if (h->fd > *maxfd) *maxfd = h->fd;
        }
        int tls_wants_write = h->tls && tcp_tls && tcp_tls->want_write(h->tls);
        if (h->connecting || (h->wq_head && h->tls_state != 1) || tls_wants_write) {
            FD_SET(h->fd, (fd_set *)wfdset);
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
    _Bool ran = 0;
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
        struct pollfd pfd = { h->fd, POLLIN | POLLOUT, 0 };
        if (poll(&pfd, 1, 0) < 0) continue;
        int readable = (pfd.revents & (POLLIN | POLLHUP | POLLERR)) != 0;
        int writable = (pfd.revents & (POLLOUT | POLLERR | POLLHUP)) != 0;
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
            int err = tcp_flush(h);
            ran = 1;
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
#else
_Bool __kml_tcp_keepalive(void) { return 0; }
_Bool __kml_tcp_fdset_add(void *fdset, void *wfdset, int *maxfd) { (void)fdset; (void)wfdset; (void)maxfd; return 0; }
_Bool __kml_tcp_dispatch(void) { return 0; }
#endif

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
    if (!tls_port ||
        atomic_load_explicit(&tls_port->inflight, memory_order_relaxed) <= 0 ||
        tls_port->wake_r < 0)
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
        switch (c->kind) {
        case KML_CMP_SETTLE:
            __kml_pool_settle(c->promise, c->v0, c->v1, c->state);
            atomic_fetch_sub_explicit(&p->inflight, 1, memory_order_relaxed);
            break;
        case KML_CMP_CALL:
            native_unlink(c);
            atomic_fetch_sub_explicit(&p->inflight, 1, memory_order_relaxed);
            // The tick queue and the promise jobs run after the dispatch, at
            // the loop's own drain (its step also resumes what they wake).
            native_set_last_string(c->res_str);
            ((void (*)(void *, double, double))c->inv)(c->clo, (double)c->err, c->res);
            break;
        }
        free(c->arg0);
        free(c->arg1);
        free(c->data);
        free(c->res_str);
        free(c);
        c = nx;
    }
    return ran;
}
