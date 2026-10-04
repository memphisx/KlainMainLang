/* fetch.c — the fetch client runtime over libcurl (ADR-00050, TDD-00097,
 * TDD-00040; in C per TDD-00240): the write callback, the non-blocking
 * multi-interface transfers, the abort scan, the waits (single, group,
 * settled, headers-only), the Response header/status helpers. One C unit; the
 * sections the program uses are selected by -D flags (FetchCFlags), because
 * each one references runtime pieces only its ensure*() brings in:
 *   KML_FETCH_ASYNC     multi handle, __kml_fetch_async, drain, pump, abort, await
 *   KML_FETCH_HDRWAIT   __kml_await_fetch_headers
 *   KML_FETCH_GROUP     Promise.all/race/any group waits
 *   KML_FETCH_SETTLED   __kml_pending_finish_settled
 *   KML_FETCH_AWAITSET  __kml_await_fetch_settled
 *   KML_FETCH_HDRRAW / KML_FETCH_HDRMAP / KML_FETCH_STATUSTEXT / KML_XHR_HDRS
 *   KML_FETCH_TASKS     the task-park path (the program has may-suspend fns)
 *   KML_FETCH_GC_RESTORE  restore the GC stack bottom after a task swap
 * Struct-by-value results ({i64,ptr,i64}, ...) go out through an out-param;
 * the generated module keeps the by-value wrappers. The CURLOPT and CURLINFO
 * numbers are libcurl's frozen ABI values. kml_layout.h is prepended. */
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef long long i64;
typedef unsigned char u8;

#define F(p, off, T) (*(T *)((char *)(p) + (off)))

/* The growable buffer a transfer writes into. Header data uses the same
 * shape (the write callback reads `pend` on every call, so it is null there). */
typedef struct {
    char *data;
    i64 len, cap;
    void *pend;
    void *hbuf;
} kbuf;

/* A pending fetch. Every field is ptr/i64, so there is no padding to differ
 * from the IR struct the generated code indexes. */
typedef struct {
    void *easy;
    kbuf *buf;
    i64 done, status, result;
    void *signal;
    i64 hdrs;
    void *bstream;
    i64 paused;
    void *bridge;
} pend_t;
_Static_assert(sizeof(pend_t) == 80, "pending fetch is 80 bytes");
_Static_assert(offsetof(pend_t, done) == KML_FETCHREQ_DONE && offsetof(pend_t, hdrs) == KML_FETCHREQ_HEADERS_DONE,
               "pending fetch fields read through kml_layout.h");

/* An Error object: {kind, message, name}. */
typedef struct {
    i64 kind;
    void *msg;
    void *name;
} kerr;

/* The interned strings the generated module hands over (@__kml_fetch_strs). */
extern void *__kml_fetch_strs[];
enum { S_TYPEERR, S_ABORTNAME, S_ABORTMSG, S_TIMEOUTNAME, S_TIMEOUTMSG, S_AGGNAME, S_AGGMSG, S_EMPTY };

#define CURLINFO_RESPONSE_CODE 2097154
#define CURLINFO_PRIVATE 1048597
#define CURLOPT_WRITEDATA 10001
#define CURLOPT_URL 10002
#define CURLOPT_TIMEOUT 13
#define CURLOPT_POSTFIELDS 10015
#define CURLOPT_HTTPHEADER 10023
#define CURLOPT_CUSTOMREQUEST 10036
#define CURLOPT_HEADERDATA 10029
#define CURLOPT_FOLLOWLOCATION 52
#define CURLOPT_NOBODY 44
#define CURLOPT_NOSIGNAL 99
#define CURLOPT_HTTP_VERSION 84
#define CURLOPT_PRIVATE 10103
#define CURLOPT_WRITEFUNCTION 20011
#define CURLOPT_HEADERFUNCTION 20079
#define CURLOPT_POSTFIELDSIZE_LARGE 30120
#define CURLOPT_SSL_OPTIONS 216
#define CURLSSLOPT_NATIVE_CA 16
#define CURL_WRITEFUNC_PAUSE 0x10000001LL

#pragma clang diagnostic ignored "-Wincompatible-library-redeclaration"
extern void curl_global_init(long);
extern void *curl_easy_init(void);
extern int curl_easy_setopt(void *, int, ...);
extern int curl_easy_getinfo(void *, int, ...);
extern void curl_easy_cleanup(void *);
extern const char *curl_easy_strerror(int);
extern _Bool __kml_curl_inited;

extern void *__kml_str_from_cstr(const char *);
extern void *__kml_str_alloc(i64);
extern void __kml_throw(void *) __attribute__((noreturn));
extern i64 __kml_fetch_body_write(void *pend, const char *chunk, i64 total);
extern void __kml_fetch_body_on_done(void *pend);

/* A fetch's failure message: curl's for a transfer error, or one of the
 * request errors fetch settles with before any transfer (negative codes). */
const char *__kml_fetch_errstr(int code) {
    if (code == -2) return "Request with GET/HEAD method cannot have body.";
    return curl_easy_strerror(code);
}

/* A fetch already settled with failure `code` (no transfer): its await
 * rejects with __kml_fetch_errstr(code). */
void *__kml_fetch_failed_pending(i64 code) {
    pend_t *p = (pend_t *)calloc(1, sizeof(pend_t));
    p->done = 1;
    p->result = code;
    return p;
}

/* libcurl calls this once per chunk as the body (and, with HEADERFUNCTION,
 * the header block) streams in. A body chunk first goes to the stream hook
 * (TDD-00097 Stage 4: 1 consumed, 2 pause, 0 buffer as before); the buffer
 * grows by doubling (floor 64) and stays NUL-terminated so the body can be
 * handed around as a plain string. */
i64 __kml_curl_write_cb(const char *chunk, i64 size, i64 nmemb, kbuf *ud) {
    i64 total = size * nmemb;
    pend_t *pend = (pend_t *)ud->pend;
    if (pend) {
        pend->hdrs = 1;
        i64 hook = __kml_fetch_body_write(pend, chunk, total);
        if (hook == 1) return total;
        if (hook == 2) return CURL_WRITEFUNC_PAUSE;
    }
    i64 need = ud->len + total + 1;
    if (need > ud->cap) {
        i64 nc = ud->cap * 2;
        if (need > nc) nc = need;
        if (nc < 64) nc = 64;
        ud->data = (char *)realloc(ud->data, (size_t)nc);
        ud->cap = nc;
    }
    memcpy(ud->data + ud->len, chunk, (size_t)total);
    ud->len += total;
    ud->data[ud->len] = 0;
    return total;
}

static void *mk_error(void *msg, void *name) {
    kerr *e = (kerr *)malloc(sizeof(kerr));
    e->kind = KML_KIND_TYPEERR;
    e->msg = msg;
    e->name = name;
    return e;
}

/* An Error whose message is the fetch failure `code`'s. */
__attribute__((unused)) static void *fetch_error(i64 code) {
    return mk_error(__kml_str_from_cstr(__kml_fetch_errstr((int)code)), __kml_fetch_strs[S_TYPEERR]);
}

#ifdef KML_FETCH_ASYNC
extern int curl_multi_perform(void *, int *);
extern void *curl_multi_init(void);
extern int curl_multi_add_handle(void *, void *);
extern int curl_multi_remove_handle(void *, void *);
extern void *curl_multi_info_read(void *, int *);
extern _Thread_local void *__kml_curl_multi;
extern void __kml_curl_drain_messages(void);

extern _Thread_local void *__kml_current_task;
extern _Thread_local char __kml_main_ctx[];
extern _Thread_local void *__kml_conn_data;
extern _Thread_local i64 __kml_current_conn_idx;
extern _Thread_local int __kml_jmp_top;
extern int swapcontext(void *, void *);
extern int __kml_loop_turn(_Bool);
extern int __kml_signal_aborted(void *sig);
extern i64 __kml_signal_reason(void *sig);
extern int __kml_signal_timeout(void *sig);
extern int __kml_nb_tag(i64);
extern i64 __kml_nb_pay(i64);
extern void __kml_throw_any(u8 tag, i64 pay) __attribute__((noreturn));
extern void __kml_fetch_bodyprom_on_done(void *pend);
extern void __kml_fetch_body_abort(void *pend, void *err);
extern void __kml_drain_microtasks(void);
#ifdef KML_FETCH_GC_RESTORE
extern void __kml_task_gc_restore(void);
#define GC_RESTORE() __kml_task_gc_restore()
#else
#define GC_RESTORE() ((void)0)
#endif

#define T_CTX(t) F(t, KML_TASK_CTX, void *)
#define T_STATE(t) F(t, KML_TASK_STATE, i64)
#define T_RESUMER(t) F(t, KML_TASK_RESUMER, void *)
#define T_JMPTOP(t) F(t, KML_TASK_JMPTOP, i64)

/* Signal-carrying fetches in flight: the event loop's scan checks each
 * turn whether its signal fired (an abort lands between turns, from
 * JavaScript). */
static _Thread_local void **sf_data;
static _Thread_local i64 sf_len, sf_cap;

void __kml_sigfetch_add(void *pending) {
    if (sf_len == sf_cap) {
        i64 nc = sf_cap * 2 + 4;
        sf_data = (void **)realloc(sf_data, (size_t)nc * sizeof(void *));
        sf_cap = nc;
    }
    sf_data[sf_len++] = pending;
}

/* The error an aborted fetch's body stream reports: the signal's reason when
 * it is an Error, else the AbortError/TimeoutError DOMException. */
static void *abort_error(void *sig) {
    i64 r = __kml_signal_reason(sig);
    if ((__kml_nb_tag(r) & 0xff) == KML_TAG_OBJECT) {
        void *robj = (void *)(intptr_t)__kml_nb_pay(r);
        if (*(i64 *)robj & KML_ERR_FLAG) return robj;
    }
    _Bool timeout = __kml_signal_timeout(sig) & 1;
    kerr *e = (kerr *)malloc(sizeof(kerr));
    e->kind = KML_KIND_DOMEXC;
    e->msg = __kml_fetch_strs[timeout ? S_TIMEOUTMSG : S_ABORTMSG];
    e->name = __kml_fetch_strs[timeout ? S_TIMEOUTNAME : S_ABORTNAME];
    return e;
}

/* Tears a signal-aborted transfer down once: result -1 marks it, done lets
 * every waiter see it. */
void __kml_fetch_abort_now(void *pending) {
    pend_t *p = (pend_t *)pending;
    if (p->result == -1 || p->done) return;
    curl_multi_remove_handle(__kml_curl_multi, p->easy);
    curl_easy_cleanup(p->easy);
    p->result = -1;
    p->done = 1;
    __kml_fetch_body_abort(p, abort_error(p->signal));
}

/* Drops finished fetches and aborts those whose signal has fired. An abort
 * settles promises (queued reactions only), and a fetch added meanwhile
 * lands past `len`: it is carried over. */
void __kml_sigfetch_scan(void) {
    i64 len = sf_len, j = 0;
    if (len == 0) return;
    for (i64 i = 0; i < len; i++) {
        pend_t *p = (pend_t *)sf_data[i];
        if (p->done) continue;
        if (__kml_signal_aborted(p->signal) & 1) {
            __kml_fetch_abort_now(p);
            continue;
        }
        sf_data[j++] = p;
    }
    i64 added = sf_len - len;
    memmove(sf_data + j, sf_data + len, (size_t)added * sizeof(void *));
    sf_len = j + added;
}

/* Node rejects the fetch with signal.reason: a stored reason is thrown as-is
 * (the same (tag, payload) path a user-level throw of an any takes); none yet
 * (an AbortSignal.timeout whose deadline the check latched first) throws the
 * matching DOMException. */
void __kml_fetch_throw_abort(void *sig) {
    i64 r = __kml_signal_reason(sig);
    if (r == KML_NB_UNDEFINED) __kml_throw(abort_error(sig));
    __kml_throw_any((u8)__kml_nb_tag(r), __kml_nb_pay(r));
}

/* libcurl's count of transfers still in flight (0 with no multi handle). An
 * in-flight transfer keeps the event loop alive whether or not anything is
 * awaiting it yet; curl_multi_perform is the only thing that reports the
 * count (non-blocking; completed transfers it notices are drained). */
int __kml_curl_inflight(void) {
    __kml_sigfetch_scan();
    void *multi = __kml_curl_multi;
    if (!multi) return 0;
    int running = 0;
    curl_multi_perform(multi, &running);
    __kml_curl_drain_messages();
    return running;
}

/* Starts a transfer without blocking and returns its pending struct. method/
 * headers/body are each nullable (a null skips the setopt); headers is a
 * curl_slist*. bodylen < 0: a NUL-terminated body, else its byte length so a
 * binary body is sent whole. */
void *__kml_fetch_async_n(const char *url, const char *method, void *headers, const char *body, i64 bodylen, void *signal) {
    if (!__kml_curl_inited) {
        curl_global_init(3);
        __kml_curl_inited = 1;
    }
    if (!__kml_curl_multi) __kml_curl_multi = curl_multi_init();
    void *multi = __kml_curl_multi;

    kbuf *buf = (kbuf *)malloc(sizeof(kbuf));
    buf->data = 0;
    buf->len = buf->cap = 0;
    buf->pend = 0;
    /* The header-capture buffer (ADR-00490): same shape, fed by the same write
     * callback via HEADERFUNCTION/HEADERDATA, reachable from the body buffer
     * so the Response can parse it lazily. Its `pend` is null so a header
     * write always takes the buffer branch. */
    kbuf *hbuf = (kbuf *)malloc(sizeof(kbuf));
    hbuf->data = 0;
    hbuf->len = hbuf->cap = 0;
    hbuf->pend = 0;
    buf->hbuf = hbuf;

    void *curl = curl_easy_init();
#if defined(_WIN32)
    /* The MSYS2 libcurl/OpenSSL finds its CA bundle relative to the running
     * executable; the Windows certificate store is always there, as Node's
     * roots always are. */
    curl_easy_setopt(curl, CURLOPT_SSL_OPTIONS, (long)CURLSSLOPT_NATIVE_CA);
#endif
    curl_easy_setopt(curl, CURLOPT_URL, url);
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, __kml_curl_write_cb);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, buf);
    curl_easy_setopt(curl, CURLOPT_HEADERFUNCTION, __kml_curl_write_cb);
    curl_easy_setopt(curl, CURLOPT_HEADERDATA, hbuf);
    curl_easy_setopt(curl, CURLOPT_FOLLOWLOCATION, 1L);
    curl_easy_setopt(curl, CURLOPT_TIMEOUT, 30L);
    curl_easy_setopt(curl, CURLOPT_NOSIGNAL, 1L);
    /* HTTP/2 over TLS via ALPN, 1.1 over cleartext; a libcurl without nghttp2
     * rejects the option (ignored) and proceeds as 1.1. */
    curl_easy_setopt(curl, CURLOPT_HTTP_VERSION, 4L);

    if (method) {
        curl_easy_setopt(curl, CURLOPT_CUSTOMREQUEST, method);
        /* HEAD must also set NOBODY, or libcurl waits for a body the reply
         * never sends (a hang on Windows). */
        if (strcmp(method, "HEAD") == 0) curl_easy_setopt(curl, CURLOPT_NOBODY, 1L);
    }
    if (headers) curl_easy_setopt(curl, CURLOPT_HTTPHEADER, headers);
    if (body) {
        if (bodylen >= 0) curl_easy_setopt(curl, CURLOPT_POSTFIELDSIZE_LARGE, (long long)bodylen);
        curl_easy_setopt(curl, CURLOPT_POSTFIELDS, body);
    }

    pend_t *p = (pend_t *)malloc(sizeof(pend_t));
    p->bridge = 0;
    p->easy = curl;
    p->buf = buf;
    p->done = p->status = p->result = 0;
    p->signal = signal;
    if (signal) __kml_sigfetch_add(p);
    p->hdrs = 0;
    p->bstream = 0;
    p->paused = 0;
    buf->pend = p;

    curl_easy_setopt(curl, CURLOPT_PRIVATE, p);
    curl_multi_add_handle(multi, curl);
    int running;
    curl_multi_perform(multi, &running);
    return p;
}

void *__kml_fetch_async(const char *url, const char *method, void *headers, const char *body, void *signal) {
    return __kml_fetch_async_n(url, method, headers, body, -1, signal);
}

/* The completed-transfer queue: record each finished transfer's HTTP status
 * and CURLcode in its pending struct, drop the easy handle, set done and
 * tell the body stream / body promise. Run after every select() wake and by
 * the raw spins. */
typedef struct {
    int msg;
    void *easy;
    int result;
} curlmsg;

void __kml_curl_drain_messages(void) {
    void *multi = __kml_curl_multi;
    int left;
    curlmsg *m;
    while ((m = (curlmsg *)curl_multi_info_read(multi, &left)) != 0) {
        if (m->msg != 1) continue; /* CURLMSG_DONE */
        void *easy = m->easy;
        pend_t *p = 0;
        long status = 0; /* getinfo writes a C long (32-bit on LLP64) */
        curl_easy_getinfo(easy, CURLINFO_PRIVATE, &p);
        curl_easy_getinfo(easy, CURLINFO_RESPONSE_CODE, &status);
        p->status = status;
        p->result = m->result;
        curl_multi_remove_handle(multi, easy);
        curl_easy_cleanup(easy);
        p->done = 1;
        __kml_fetch_body_on_done(p);
        __kml_fetch_bodyprom_on_done(p);
    }
}

/* One multi_perform + drain, reporting whether transfers are still running:
 * the no-fiber await drive's hook (TDD-00097 Stage 4). */
_Bool __kml_fetch_pump(void) {
    __kml_sigfetch_scan();
    void *multi = __kml_curl_multi;
    if (!multi) return 0;
    int running = 0;
    curl_multi_perform(multi, &running);
    __kml_curl_drain_messages();
    return running > 0;
}

/* ---- finishing ---- */

typedef struct {
    i64 status;
    void *body;
    i64 len;
} fres;

/* Throws a catchable Error on a transfer-level failure, otherwise returns the
 * final status/body (a body never written is an empty string). */
void __kml_pending_finish_c(void *pending, fres *out) {
    pend_t *p = (pend_t *)pending;
    if (p->result != 0) __kml_throw(fetch_error(p->result));
    kbuf *b = p->buf;
    out->status = p->status;
    if (!b->data) {
        char *e = (char *)malloc(1);
        e[0] = 0;
        out->body = e;
        out->len = 0;
    } else {
        out->body = b->data;
        out->len = b->len;
    }
}

/* ---- waits ---- */

/* Parks the caller once while `what` is unsatisfied. On a task it records
 * what it waits for (task_off), suspends, and resumes; on a connection fiber
 * it parks in the connection's slot (conn_off) and yields to the loop.
 * Returns 0 when the caller is on neither (the main stack). */
static _Bool park(void *what, i64 task_off, i64 conn_off, _Bool use_task) {
#ifdef KML_FETCH_TASKS
    void *ct = use_task ? __kml_current_task : 0;
    if (ct) {
        F(ct, task_off, void *) = what;
        T_STATE(ct) = 1;
        T_JMPTOP(ct) = (i64)(unsigned)__kml_jmp_top;
        swapcontext(T_CTX(ct), T_RESUMER(ct));
        GC_RESTORE();
        return 1;
    }
#else
    (void)task_off;
    (void)use_task;
#endif
    i64 idx = __kml_current_conn_idx;
    if (idx < 0) return 0;
    char *slot = (char *)__kml_conn_data + idx * KML_CONN_SIZE;
    F(slot, conn_off, void *) = what;
    swapcontext(F(slot, KML_CONN_CTX, void *), __kml_main_ctx);
    /* The connection array may have moved while suspended. */
    slot = (char *)__kml_conn_data + idx * KML_CONN_SIZE;
    F(slot, conn_off, void *) = 0;
    return 1;
}

/* The main-stack wait: a turn of the real event loop (TDD-00223 §1), pinned
 * alive by this in-flight transfer, which sleeps in select() on libcurl's
 * sockets and serves every other source meanwhile. Only from inside a loop
 * callback (turn == 2) does the libcurl-only spin remain. */
static void spin(_Bool drain_microtasks) {
    if (__kml_loop_turn(1) != 2) return;
    int running;
    curl_multi_perform(__kml_curl_multi, &running);
    __kml_curl_drain_messages();
    if (drain_microtasks) __kml_drain_microtasks();
}

/* Loops until the transfer is done. Done is checked before the abort signal so
 * a re-await of an already-finished fetch (a Promise<Response> is a reusable
 * value) never re-enters the abort teardown on its already-cleaned easy
 * handle. A transfer torn down by its signal rejects. */
void __kml_await_fetch_c(void *pending, fres *out) {
    pend_t *p = (pend_t *)pending;
    void *sig = p->signal;
    for (;;) {
        if (p->done) {
            if (p->result == -1) {
                __kml_fetch_abort_now(p);
                __kml_fetch_throw_abort(sig);
            }
            break;
        }
        if (__kml_signal_aborted(sig) & 1) {
            __kml_fetch_abort_now(p);
            __kml_fetch_throw_abort(sig);
        }
        if (park(p, KML_TASK_PENDING_FETCH, KML_FETCH_CONN_PFETCH, 1)) continue;
        spin(0);
    }
    __kml_pending_finish_c(p, out);
}
#endif /* KML_FETCH_ASYNC */

#ifdef KML_FETCH_HDRWAIT
/* Drives the multi loop until the response's headers have arrived (the first
 * write-callback invocation) or the transfer is done, then returns the HTTP
 * status: the resolve-at-headers point `await fetch(...)` uses, so `.body`
 * can stream the rest. */
i64 __kml_await_fetch_headers(void *pending) {
    pend_t *p = (pend_t *)pending;
    void *sig = p->signal;
    for (;;) {
        if (p->done) {
            if (p->result == -1) {
                __kml_fetch_abort_now(p);
                __kml_fetch_throw_abort(sig);
            }
            /* A transfer-level failure throws via the shared finish path. */
            if (p->result != 0) {
                fres ignored;
                __kml_pending_finish_c(p, &ignored);
            }
            return p->status;
        }
        /* The signal is checked while the headers are awaited; once they
         * arrive the Response resolves and the event loop's scan aborts the
         * transfer and errors its body stream. */
        if (__kml_signal_aborted(sig) & 1) {
            __kml_fetch_abort_now(p);
            __kml_fetch_throw_abort(sig);
        }
        if (p->hdrs) {
            long st = 0;
            curl_easy_getinfo(p->easy, CURLINFO_RESPONSE_CODE, &st);
            return st;
        }
        if (park(p, KML_TASK_PENDING_FETCH, KML_FETCH_CONN_PFETCH, 1)) continue;
        /* Module top level: keep microtask ordering intact while waiting
         * (a queued .then drive fires here). */
        spin(1);
    }
}
#endif /* KML_FETCH_HDRWAIT */

#ifdef KML_FETCH_SETTLED
/* A non-throwing sibling of __kml_pending_finish (Promise.allSettled,
 * XMLHttpRequest.send): on failure `reason` is curl's message and the body
 * empty. */
typedef struct {
    u8 failed;
    i64 status;
    void *body;
    void *reason;
    i64 len;
} sres;

void __kml_pending_finish_settled_c(void *pending, sres *out) {
    pend_t *p = (pend_t *)pending;
    if (p->result != 0) {
        out->failed = 1;
        out->status = 0;
        out->body = 0;
        out->reason = __kml_str_from_cstr(__kml_fetch_errstr((int)p->result));
        out->len = 0;
        return;
    }
    kbuf *b = p->buf;
    out->failed = 0;
    out->status = p->status;
    out->reason = 0;
    if (!b->data) {
        char *e = (char *)malloc(1);
        e[0] = 0;
        out->body = e;
        out->len = 0;
    } else {
        out->body = b->data;
        out->len = b->len;
    }
}
#endif /* KML_FETCH_SETTLED */

#ifdef KML_FETCH_AWAITSET
/* __kml_await_fetch's loop finishing non-throwingly: XMLHttpRequest.send()'s
 * whole transfer mechanism. A blocking wait by definition, one goroutine
 * threads issue concurrently: it drives libcurl alone and never takes a turn
 * of the event loop, and has no task or abort path. */
void __kml_await_fetch_settled_c(void *pending, sres *out) {
    pend_t *p = (pend_t *)pending;
    while (!p->done) {
        if (park(p, 0, KML_FETCH_CONN_PFETCH, 0)) continue;
        int running;
        curl_multi_perform(__kml_curl_multi, &running);
        __kml_curl_drain_messages();
    }
    __kml_pending_finish_settled_c(p, out);
}
#endif /* KML_FETCH_AWAITSET */

#ifdef KML_FETCH_GROUP
/* A group is {members**, count, mode}: mode 0 waits for every member
 * (.all/.allSettled), 1 for the first (.race), 2 (.any) for the first that
 * transport-succeeded or, if none, every member done. */
typedef struct {
    pend_t **members;
    i64 count, mode;
} grp;

/* Polled by __kml_await_group_wait and by the event loop's resume-scan. */
_Bool __kml_group_satisfied(void *group) {
    grp *g = (grp *)group;
    _Bool anynotdone = 0;
    for (i64 i = 0; i < g->count; i++) {
        pend_t *m = g->members[i];
        if (g->mode == 2) {
            if (!m->done) anynotdone = 1;
            else if (m->result == 0) return 1;
        } else if (g->mode == 0) {
            if (!m->done) return 0;
        } else if (m->done) {
            return 1;
        }
    }
    if (g->mode == 0) return 1;
    return g->mode == 2 ? !anynotdone : 0;
}

/* Only after a mode-1 wait returned: at least one member is done. */
i64 __kml_first_done_index(void *group) {
    grp *g = (grp *)group;
    for (i64 i = 0; i < g->count; i++)
        if (g->members[i]->done) return i;
    return -1;
}

/* Promise.any: the first member done and transport-succeeded, else -1. */
i64 __kml_first_success_index(void *group) {
    grp *g = (grp *)group;
    for (i64 i = 0; i < g->count; i++)
        if (g->members[i]->done && g->members[i]->result == 0) return i;
    return -1;
}

/* Promise.any's all-rejected path: an AggregateError whose `.errors` are one
 * Error per member carrying that fetch's transport-failure message. */
void __kml_group_throw_aggregate(void *group) {
    grp *g = (grp *)group;
    void **errs = (void **)malloc((size_t)g->count * sizeof(void *));
    for (i64 i = 0; i < g->count; i++) errs[i] = fetch_error(g->members[i]->result);
    void **agg = (void **)malloc(KML_AGG_SIZE);
    ((i64 *)agg)[0] = KML_KIND_AGG;
    agg[1] = __kml_fetch_strs[S_AGGMSG];
    agg[2] = __kml_fetch_strs[S_AGGNAME];
    agg[3] = errs;
    ((i64 *)agg)[4] = g->count;
    __kml_throw(agg);
}

/* __kml_await_fetch's wait shape, polling the group instead of one done flag
 * and parking in the pendingGroup slot. */
void __kml_await_group_wait(void *group) {
    while (!__kml_group_satisfied(group)) {
        if (park(group, KML_TASK_PENDING_GROUP, KML_FETCH_CONN_PGROUP, 1)) continue;
        spin(0);
    }
}
#endif /* KML_FETCH_GROUP */

/* ---- Response header and status helpers ---- */

#if defined(KML_FETCH_HDRRAW) || defined(KML_FETCH_HDRMAP) || defined(KML_FETCH_STATUSTEXT)
/* The captured raw header text of a Response (every block of a redirect
 * chain), or null for a combinator-built Response / one with none. */
static const char *raw_headers(void *pending) {
    pend_t *p = (pend_t *)pending;
    if (!p || !p->buf || !p->buf->hbuf) return 0;
    return ((kbuf *)p->buf->hbuf)->data;
}
#endif

#ifdef KML_FETCH_HDRRAW
void *__kml_fetch_headers_raw(void *pending) {
    const char *raw = raw_headers(pending);
    return raw ? __kml_str_from_cstr(raw) : __kml_fetch_strs[S_EMPTY];
}
#endif

#ifdef KML_FETCH_HDRMAP
extern void *__kml_map_str_create(void);
extern void __kml_map_str_set(void *map, char *key, i64 val);

static char *str_copy(const char *s, i64 n, _Bool lower) {
    char *out = (char *)__kml_str_alloc(n);
    for (i64 i = 0; i < n; i++) out[i] = lower && s[i] >= 'A' && s[i] <= 'Z' ? (char)(s[i] + 32) : s[i];
    out[n] = 0;
    return out;
}

/* Lazily parses the raw header text into a Map<string,string> with
 * lowercased keys (the Fetch spec's Headers case rule). Status lines, blank
 * lines and lines without a name are skipped; values are left-trimmed of
 * spaces. A null pending yields an empty map. */
void *__kml_fetch_headers_map(void *pending) {
    void *map = __kml_map_str_create();
    const char *line = raw_headers(pending);
    if (!line) return map;
    while (*line) {
        const char *p = line;
        while (*p && *p != '\r' && *p != '\n' && *p != ':') p++;
        if (!*p) return map;
        const char *next = p;
        if (*p == ':' && p != line) {
            char *key = str_copy(line, p - line, 1);
            const char *vs = p + 1;
            while (*vs == ' ') vs++;
            const char *q = vs;
            while (*q && *q != '\r' && *q != '\n') q++;
            __kml_map_str_set(map, key, (i64)(intptr_t)str_copy(vs, q - vs, 0));
            next = q;
        } else if (*p == ':') {
            while (*next && *next != '\r' && *next != '\n') next++;
        }
        while (*next == '\r' || *next == '\n') next++;
        line = next;
    }
    return map;
}
#endif

#ifdef KML_FETCH_STATUSTEXT
/* A Response's statusText: the reason phrase of the last status line in its
 * captured headers ("HTTP/1.1 404 Not Found" -> "Not Found"; after a
 * redirect, the final response's), or "" when there is none (HTTP/2 sends no
 * reason phrase) or the Response was not fetched. */
static _Bool eol(char c) { return c == 0 || c == '\r' || c == '\n'; }

void *__kml_fetch_status_text(void *pending) {
    const char *raw = raw_headers(pending);
    if (!raw) return __kml_str_from_cstr("");
    i64 last = -1;
    _Bool bol = 1;
    i64 i;
    for (i = 0; raw[i]; i++) {
        if (bol && strncmp(raw + i, "HTTP/", 5) == 0) last = i;
        bol = raw[i] == '\n';
    }
    if (last < 0) return __kml_str_from_cstr("");
    i64 a = last;
    while (raw[a] != ' ') {
        if (eol(raw[a])) return __kml_str_from_cstr("");
        a++;
    }
    i64 b = a + 1;
    while (raw[b] != ' ') {
        if (eol(raw[b])) return __kml_str_from_cstr("");
        b++;
    }
    i64 r0 = b + 1, r = r0;
    while (!eol(raw[r])) r++;
    char *out = (char *)__kml_str_alloc(r - r0);
    memcpy(out, raw + r0, (size_t)(r - r0));
    out[r - r0] = 0;
    return out;
}
#endif

#ifdef KML_XHR_HDRS
/* A parsed header map's stored keys/vals (collections' map header: size at 0,
 * keys at 16, vals at 24). */
typedef struct {
    i64 size, cap;
    char **keys;
    char **vals;
} hmap;
_Static_assert(offsetof(hmap, keys) == 16 && offsetof(hmap, vals) == 24, "map header offsets");

/* getAllResponseHeaders(): "name: value\r\n" per entry in stored (arrival)
 * order; a null map (send() not yet DONE, or a network failure) is "". */
void *__kml_xhr_headers_all(hmap *m) {
    if (!m) {
        char *es = (char *)__kml_str_alloc(0);
        es[0] = 0;
        return es;
    }
    i64 tot = 0;
    for (i64 i = 0; i < m->size; i++) tot += (i64)strlen(m->keys[i]) + (i64)strlen(m->vals[i]) + 4;
    char *out = (char *)__kml_str_alloc(tot), *w = out;
    for (i64 i = 0; i < m->size; i++) {
        size_t kl = strlen(m->keys[i]), vl = strlen(m->vals[i]);
        memcpy(w, m->keys[i], kl);
        w += kl;
        *w++ = ':';
        *w++ = ' ';
        memcpy(w, m->vals[i], vl);
        w += vl;
        *w++ = '\r';
        *w++ = '\n';
    }
    *w = 0;
    return out;
}
#endif
