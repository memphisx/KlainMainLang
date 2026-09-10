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
// The wakeup socketpair is the general per-loop primitive the §1 audit found
// missing on POSIX (the Windows fs.watch one, ADR-00757, is the same idea); the
// Windows event-loop reactor work (TDD-00182/00183) is where this folds in on
// that platform, so the pool is POSIX-first and Windows keeps the inline
// (blocking) fs path until then.

#include <pthread.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <stdint.h>
#include <sys/select.h>
#include <sys/socket.h>
#include <errno.h>

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
extern void __kml_pool_settle(void *promise, int64_t v0, int64_t v1, int64_t state);
// TDD-00186 stream drain (loop thread): enqueue one chunk / close / error the
// readable, and re-run the WHATWG pull check to grant the next credit.
extern void __kml_pool_stream_chunk(void *rs, int64_t chunk);
extern void __kml_pool_stream_end(void *rs);
extern void __kml_pool_stream_error(void *rs, int64_t err_errno);
extern void __kml_rs_pull_if_needed(void *rs);

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

// A completion's drain action on the loop thread (TDD-00186 adds the stream
// kinds). The one-shot fs ops post one SETTLE; a createReadStream job posts many
// STREAM_CHUNKs and a terminal STREAM_END.
enum {
    KML_CMP_SETTLE = 0,      // settle a Promise (target = promise, v0/v1/state)
    KML_CMP_STREAM_CHUNK,    // enqueue a chunk into a readable (target = ctl, v0 = chunk ptr)
    KML_CMP_STREAM_END,      // close a readable (target = ctl)
    KML_CMP_STREAM_ERROR,    // error a readable (target = ctl, v0 = errno)
};

// TDD-00186 backpressure: a demand-driven read stream. The worker reads exactly
// one chunk per credit and blocks otherwise; the loop grants a credit from the
// readable's pull hook (fired by the WHATWG machinery when the consumer drains
// below the high-water mark) and re-arms it as each chunk is drained — so at most
// ~one chunk is outstanding. `inflight` is loop-only (guards against a double
// grant while a read is in flight); `credits` is the worker's condvar signal.
typedef struct kml_stream_ctl {
    pthread_mutex_t mu;
    pthread_cond_t  cv;
    int64_t credits;         // worker go-signal (mutex-guarded)
    int inflight;            // loop-only: a read is dispatched, not yet drained
    int stop;                // loop sets on cancel; worker exits its wait
    void *rs;                // the readable (loop-thread use only)
    void *fp;                // FILE* opened on the loop thread
    int64_t hwm;             // fread chunk size
    struct kml_loop_port *port;
} kml_stream_ctl;

// One struct, reused as work item (loop -> pool), stream job, and completion
// item (pool -> loop). arg0/arg1 are strdup'd copies the item owns and frees.
typedef struct kml_pool_item {
    struct kml_pool_item *next;
    int opid;               // work: which fs op (KML_OP_*)
    int kind;               // completion: drain action (KML_CMP_*)
    void *promise;          // completion target: a Promise, or (stream) the readable
    char *arg0;
    char *arg1;
    void *data;             // binary write: raw malloc'd byte buffer the item owns
    int64_t datalen;        // binary write: its byte length
    void *fp;               // stream job: the FILE* opened on the loop thread
    int64_t hwm;            // stream job: highWaterMark chunk size
    struct kml_loop_port *port;
    int64_t state;          // worker fills: 1 fulfilled / 2 rejected
    int64_t v0;             // worker fills: result word 0 (or Error ptr on reject / chunk ptr)
    int64_t v1;             // worker fills: result word 1 (readdir's length; else 0)
} kml_pool_item;

// Op ids — must match the lowering in emit_fs_async.go / emit_fs_stream.go.
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
    KML_OP_READSTREAM,      // TDD-00186: chunked file read feeding a Readable
    KML_OP_WRITEFILE_BYTES,  // TDD-00185: writeFile of a raw byte buffer
    KML_OP_APPENDFILE_BYTES, // TDD-00185: appendFile of a raw byte buffer
};

// Per-loop completion port: a Treiber stack the workers push completions onto,
// a socketpair the workers write to wake this loop's select(), and the count of
// this loop's outstanding submissions (keeps the loop alive while I/O flies).
typedef struct kml_loop_port {
    _Atomic(kml_pool_item *) comp_head;
    int wake_r, wake_w;
    atomic_long inflight;
} kml_loop_port;

// The port is per event-loop-thread. A Worker runs its own loop on its own
// thread and gets its own port; the pool itself is process-wide (below).
static __thread kml_loop_port *tls_port = NULL;

static kml_loop_port *loop_port(void) {
    if (tls_port) return tls_port;
    kml_loop_port *p = (kml_loop_port *)calloc(1, sizeof *p);
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
    if (p->wake_w >= 0) {
        char b = 1;
        ssize_t n = write(p->wake_w, &b, 1);
        (void)n;
    }
}

// Post a fresh completion (kind, target, v0) to the loop. Used by the stream
// job, which emits many completions from one work item.
static void post_completion(kml_loop_port *p, int kind, void *target, int64_t v0) {
    kml_pool_item *c = (kml_pool_item *)calloc(1, sizeof *c);
    c->kind = kind;
    c->promise = target;
    c->v0 = v0;
    c->port = p;
    push_completion(p, c);
}

static void free_stream_ctl(kml_stream_ctl *ctl) {
    pthread_mutex_destroy(&ctl->mu);
    pthread_cond_destroy(&ctl->cv);
    free(ctl);
}

// TDD-00186: read a file in hwm chunks on the worker, ONE chunk per credit
// (backpressure). Blocks on the condvar until the loop grants a credit from the
// readable's pull hook; posts each chunk as a STREAM_CHUNK, a terminal
// STREAM_END at clean EOF, or a STREAM_ERROR on a mid-read failure / a consumer
// cancel (`stop`). The chunk is a length-prefixed KML string (length at data-8),
// built in plain C — the readable leaks its chunks (as the eager path did), so
// no GC alloc happens off-thread.
static void run_read_stream(kml_stream_ctl *ctl) {
    FILE *f = (FILE *)ctl->fp;
    kml_loop_port *p = ctl->port;
    int64_t hwm = ctl->hwm > 0 ? ctl->hwm : 65536;
    int err_errno = 0;               // 0 = clean EOF (or cancel); nonzero = error
    for (;;) {
        pthread_mutex_lock(&ctl->mu);
        while (ctl->credits <= 0 && !ctl->stop) pthread_cond_wait(&ctl->cv, &ctl->mu);
        int stop = ctl->stop;
        if (!stop) ctl->credits--;
        pthread_mutex_unlock(&ctl->mu);
        if (stop) break;             // consumer cancelled — end the stream cleanly

        char *base = (char *)malloc((size_t)hwm + 9);
        if (!base) { err_errno = ENOMEM; break; }
        size_t n = f ? fread(base + 8, 1, (size_t)hwm, f) : 0;
        if (n == 0) {
            if (f && ferror(f)) err_errno = EIO;
            free(base);
            break;
        }
        *(int64_t *)base = (int64_t)n;   // length header at data-8
        base[8 + n] = 0;                 // NUL terminator
        post_completion(p, KML_CMP_STREAM_CHUNK, ctl, (int64_t)(intptr_t)(base + 8));
    }
    if (f) fclose(f);
    if (err_errno) post_completion(p, KML_CMP_STREAM_ERROR, ctl, (int64_t)err_errno);
    else post_completion(p, KML_CMP_STREAM_END, ctl, 0);
}

// Run one work item. Returns 1 if the item itself should be pushed as its
// completion (the one-shot ops), 0 if it posted its own completions and should
// be freed (the stream job).
static int run_item(kml_pool_item *it) {
    if (it->opid == KML_OP_READSTREAM) {
        run_read_stream((kml_stream_ctl *)it->promise);
        return 0;
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
    default:                r.err = (void *)1; break;
    }
    it->kind = KML_CMP_SETTLE;
    if (r.err) {                                  // threw -> reject with the Error
        it->state = 2;
        it->v0 = (int64_t)(intptr_t)r.err;
        it->v1 = 0;
    } else {                                      // fulfil with (v0, v1)
        it->state = 1;
        it->v0 = r.v0;
        it->v1 = r.v1;
    }
    return 1;
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
        if (run_item(it)) {
            push_completion(it->port, it);   // one-shot: the item is its own completion
        } else {
            free(it->arg0);
            free(it->arg1);
            free(it->data);
            free(it);                         // stream job: completions already posted
        }
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
// Enqueue a prepared work item and bump this loop's inflight count. Only the
// terminal completion (SETTLE, or a stream's STREAM_END) decrements inflight, so
// the loop stays alive across a whole multi-chunk stream read.
// bump: whether the submit itself is an in-flight read (one-shot ops), or not
// (a demand-driven read stream, where each granted credit — not the submit —
// bumps inflight, so an unconsumed stream doesn't pin the loop alive).
static void enqueue_work(kml_loop_port *p, kml_pool_item *it, int bump) {
    it->port = p;
    if (bump) atomic_fetch_add_explicit(&p->inflight, 1, memory_order_relaxed);
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
    enqueue_work(loop_port(), it, 1);
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
    enqueue_work(loop_port(), it, 1);
}

// TDD-00186: allocate a demand-driven read-stream control block on the loop
// thread. `rs` is the readable, `fp` the FILE* opened synchronously, `hwm` the
// fread chunk size. The returned handle is the env of the pull/cancel closures
// installed on the readable and the work item's target.
void *__kml_pool_stream_ctl_new(void *rs, void *fp, int64_t hwm) {
    kml_stream_ctl *ctl = (kml_stream_ctl *)calloc(1, sizeof *ctl);
    pthread_mutex_init(&ctl->mu, NULL);
    pthread_cond_init(&ctl->cv, NULL);
    ctl->rs = rs;
    ctl->fp = fp;
    ctl->hwm = hwm;
    ctl->port = loop_port();
    return ctl;
}

// TDD-00186: submit a chunked read for a control block onto the pool.
void __kml_pool_submit_readstream(void *ctl) {
    kml_pool_item *it = (kml_pool_item *)calloc(1, sizeof *it);
    it->opid = KML_OP_READSTREAM;
    it->promise = ctl;
    enqueue_work(loop_port(), it, 0);   // credits, not the submit, bump inflight
}

// TDD-00186 pull hook (loop thread): the readable's field-9 pull closure. Grant
// exactly one read credit unless one is already in flight — returns NULL (a
// synchronous pull); the chunk lands later via the completion drain, which
// clears `inflight` and re-arms the pull.
void *__kml_pool_stream_pull(void *ctlv) {
    kml_stream_ctl *ctl = (kml_stream_ctl *)ctlv;
    if (ctl->inflight) return NULL;
    ctl->inflight = 1;
    // A dispatched read keeps the loop alive; its completion (chunk/end/error)
    // decrements. Between reads a demand-starved stream sits at 0, so an
    // abandoned consumer lets the loop exit rather than hang.
    atomic_fetch_add_explicit(&ctl->port->inflight, 1, memory_order_relaxed);
    pthread_mutex_lock(&ctl->mu);
    ctl->credits++;
    pthread_cond_signal(&ctl->cv);
    pthread_mutex_unlock(&ctl->mu);
    return NULL;
}

// TDD-00186 cancel hook (loop thread): the readable's field-10 cancel closure.
// Tell the worker to stop; it wakes, ends the stream, and the drain frees ctl.
void *__kml_pool_stream_cancel(void *ctlv) {
    kml_stream_ctl *ctl = (kml_stream_ctl *)ctlv;
    pthread_mutex_lock(&ctl->mu);
    ctl->stop = 1;
    pthread_cond_signal(&ctl->cv);
    pthread_mutex_unlock(&ctl->mu);
    return NULL;
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
    if (!tls_port ||
        atomic_load_explicit(&tls_port->inflight, memory_order_relaxed) <= 0 ||
        tls_port->wake_r < 0)
        return 0;
    FD_SET(tls_port->wake_r, (fd_set *)fdset);
    if (tls_port->wake_r > *maxfd) *maxfd = tls_port->wake_r;
    return 0;
}

// Drain arrived completions on the loop thread. The comp stack is LIFO, but a
// stream's chunks must be enqueued in the order the worker read them, so reverse
// the batch to insertion order before dispatching.
void __kml_pool_dispatch(void) {
    kml_loop_port *p = tls_port;
    if (!p) return;
    if (p->wake_r >= 0) {
        char buf[64];
        while (read(p->wake_r, buf, sizeof buf) > 0) { /* drain wakeup bytes */ }
    }
    kml_pool_item *it = atomic_exchange_explicit(&p->comp_head, NULL,
                                                 memory_order_acquire);
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
        case KML_CMP_STREAM_CHUNK: {
            kml_stream_ctl *ctl = (kml_stream_ctl *)c->promise;
            atomic_fetch_sub_explicit(&p->inflight, 1, memory_order_relaxed);
            __kml_pool_stream_chunk(ctl->rs, c->v0);
            ctl->inflight = 0;                    // read complete
            __kml_rs_pull_if_needed(ctl->rs);     // grant the next credit if wanted
            break;
        }
        case KML_CMP_STREAM_END: {
            kml_stream_ctl *ctl = (kml_stream_ctl *)c->promise;
            __kml_pool_stream_end(ctl->rs);
            free_stream_ctl(ctl);
            atomic_fetch_sub_explicit(&p->inflight, 1, memory_order_relaxed);
            break;
        }
        case KML_CMP_STREAM_ERROR: {
            kml_stream_ctl *ctl = (kml_stream_ctl *)c->promise;
            __kml_pool_stream_error(ctl->rs, c->v0);
            free_stream_ctl(ctl);
            atomic_fetch_sub_explicit(&p->inflight, 1, memory_order_relaxed);
            break;
        }
        }
        free(c->arg0);
        free(c->arg1);
        free(c->data);
        free(c);
        c = nx;
    }
}
