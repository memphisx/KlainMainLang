/* fetch.c — the fetch client runtime over libcurl (ADR-00050, TDD-00097,
 * TDD-00040; in C per TDD-00240): the write callback, the non-blocking
 * multi-interface transfers, the abort scan, the waits (single, group,
 * settled, headers-only), the Response header/status helpers. One C unit,
 * compiled the same way for every program: the link drops the functions a
 * program does not reach, with the runtime pieces they reference.
 * Struct-by-value results ({i64,ptr,i64}, ...) go out through an out-param;
 * the generated module keeps the by-value wrappers. The CURLOPT and CURLINFO
 * numbers are libcurl's frozen ABI values. kml_layout.h is prepended. */
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#ifdef _WIN32
#include <ws2tcpip.h>
#else
#include <arpa/inet.h>
#include <netdb.h>
#include <netinet/in.h>
#include <sys/socket.h>
#endif
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
    void *cause; /* a failed transfer's reason, the TypeError's cause */
} pend_t;
_Static_assert(sizeof(pend_t) == 88, "pending fetch is 88 bytes");
_Static_assert(offsetof(pend_t, done) == KML_FETCHREQ_DONE && offsetof(pend_t, hdrs) == KML_FETCHREQ_HEADERS_DONE,
               "pending fetch fields read through kml_layout.h");

/* An Error object's leading fields: {kind, message, name}. It is allocated
 * at the full Error layout, which code reading its other fields expects. */
typedef struct {
    i64 kind;
    void *msg;
    void *name;
} kerr;
_Static_assert(KML_ERR_MESSAGE == 8 && KML_ERR_NAME == 16, "kerr is the Error layout's head");

/* The interned strings the generated module hands over (@__kml_fetch_strs). */
extern void *__kml_fetch_strs[];
enum { S_TYPEERR, S_ABORTNAME, S_ABORTMSG, S_TIMEOUTNAME, S_TIMEOUTMSG, S_AGGNAME, S_AGGMSG, S_EMPTY, S_ERRNAME, S_FAILED };

#define CURLINFO_RESPONSE_CODE 2097154
#define CURLINFO_PRIVATE 1048597
#define CURLINFO_EFFECTIVE_URL 1048577
#define CURLINFO_PRIMARY_IP 1048608
#define CURLINFO_OS_ERRNO 2097177
#define CURLINFO_PRIMARY_PORT 2097192
#define CURLE_COULDNT_RESOLVE_HOST 6
#define CURLE_COULDNT_CONNECT 7
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

#if defined(__APPLE__)
/* macOS's libcurl waits with select() and refuses a socket at or above
 * FD_SETSIZE. When the descriptor limit lets a program go past it, a few
 * low descriptors are reserved at startup: a transfer's socket opened
 * above the limit moves onto one (dup2), and goes back to the reserve when
 * libcurl closes it. */
#include <fcntl.h>
#include <sys/resource.h>
#include <sys/select.h>
#include <sys/socket.h>
#include <unistd.h>
#define CURLOPT_OPENSOCKETFUNCTION 20163
#define CURLOPT_CLOSESOCKETFUNCTION 20208
#define LOWFD_RESERVE 32
struct kml_curl_sockaddr {
    int family, socktype, protocol;
    unsigned int addrlen;
};
static int lowfd_free[LOWFD_RESERVE], lowfd_n;
static unsigned char lowfd_ours[FD_SETSIZE / 8];

__attribute__((constructor)) static void lowfd_reserve(void) {
    struct rlimit rl;
    if (getrlimit(RLIMIT_NOFILE, &rl) != 0 || rl.rlim_cur <= FD_SETSIZE) return;
    while (lowfd_n < LOWFD_RESERVE) {
        int fd = open("/dev/null", O_RDONLY | O_CLOEXEC);
        if (fd < 0) break;
        if (fd >= FD_SETSIZE) {
            close(fd);
            break;
        }
        lowfd_ours[fd / 8] |= (unsigned char)(1u << (fd % 8));
        lowfd_free[lowfd_n++] = fd;
    }
}

static int lowfd_open(void *clientp, int purpose, struct kml_curl_sockaddr *a) {
    (void)clientp;
    (void)purpose;
    int s = socket(a->family, a->socktype, a->protocol);
    if (s < FD_SETSIZE || lowfd_n == 0) return s;
    int r = lowfd_free[--lowfd_n];
    dup2(s, r);
    close(s);
    fcntl(r, F_SETFD, FD_CLOEXEC);
    return r;
}

static int lowfd_close(void *clientp, int s) {
    (void)clientp;
    if (s >= 0 && s < FD_SETSIZE && (lowfd_ours[s / 8] >> (s % 8) & 1)) {
        int dn = open("/dev/null", O_RDONLY | O_CLOEXEC);
        if (dn >= 0) {
            dup2(dn, s);
            close(dn);
            fcntl(s, F_SETFD, FD_CLOEXEC);
            lowfd_free[lowfd_n++] = s;
            return 0;
        }
    }
    return close(s);
}
#endif

extern void *__kml_str_from_cstr(const char *);
extern void *__kml_str_alloc(i64);
extern void __kml_throw(void *) __attribute__((noreturn));
extern i64 __kml_fetch_body_write(void *pend, const char *chunk, i64 total);
extern void __kml_fetch_body_on_done(void *pend);

/* A fetch's failure message: curl's for a transfer error, or one of the
 * request errors fetch settles with before any transfer (negative codes). */
const char *__kml_fetch_errstr(int code) {
    if (code == -2) return "Request with GET/HEAD method cannot have body.";
    if (code == -3) return "bad port";
    return curl_easy_strerror(code);
}

/* A fetch already settled with failure `code` (no transfer): its await
 * rejects with __kml_fetch_errstr(code). */
void *__kml_fetch_failed_pending(i64 code) {
    pend_t *p = (pend_t *)kml_calloc(KML_HEAP_WORDS, 1, sizeof(pend_t));
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
        ud->data = (char *)kml_realloc(KML_HEAP_RAW, ud->data, (size_t)nc);
        ud->cap = nc;
    }
    memcpy(ud->data + ud->len, chunk, (size_t)total);
    ud->len += total;
    ud->data[ud->len] = 0;
    return total;
}

static void *mk_error(i64 kind, void *msg, void *name) {
    kerr *e = (kerr *)kml_calloc(KML_HEAP_WORDS, 1, KML_ERR_SIZE);
    e->kind = kind;
    e->msg = msg;
    e->name = name;
    return e;
}

extern i64 __kml_nb_pack(u8 tag, i64 pay);

/* An AggregateError: the Error layout, then its errors array (KML_AGG_ERRORS,
 * any[]: each error boxed in place) and their count. */
static void *mk_aggregate(void *msg, void **errs, i64 n) {
    for (i64 i = 0; i < n; i++)
        ((i64 *)errs)[i] = __kml_nb_pack(KML_TAG_ERROR, (i64)(intptr_t)errs[i]);
    char *agg = (char *)kml_calloc(KML_HEAP_WORDS, 1, KML_AGG_SIZE);
    F(agg, KML_ERROR_KIND, i64) = KML_ERRKIND_AGG;
    F(agg, KML_ERR_MESSAGE, void *) = msg;
    F(agg, KML_ERR_NAME, void *) = __kml_fetch_strs[S_AGGNAME];
    F(agg, KML_ERR_CAUSE, i64) = KML_NB_UNDEFINED;
    F(agg, KML_AGG_ERRORS, void **) = errs;
    F(agg, KML_AGG_ERRORS + 8, i64) = n;
    return agg;
}

extern void *__kml_errno_code(int e);
extern int __kml_uv_errno(int e);
extern void *__kml_dynobj_new(void);
extern void __kml_dynobj_set(void *o, const char *key, i64 v);
extern void __kml_error_own_cause(void *op);

static const struct { i64 len; char d[9]; } k_hostname = { KML_STR_ASCII_HDR(8), "hostname" };

/* A string from printf-style parts. */
static void *fmt_str(const char *fmt, const char *a, const char *b, long n) {
    char buf[600];
    snprintf(buf, sizeof buf, fmt, a, b, n);
    return __kml_str_from_cstr(buf);
}

/* The host of an http(s) URL (brackets of an IPv6 literal dropped). */
static void url_host(const char *url, char *out, size_t cap) {
    out[0] = 0;
    const char *h = url ? strstr(url, "://") : 0;
    if (!h) return;
    h += 3;
    const char *at = h;
    while (*at && *at != '/' && *at != '?' && *at != '#') at++;
    for (const char *c = h; c < at; c++)
        if (*c == '@') h = c + 1;
    const char *end = h;
    if (*h == '[') {
        h++;
        end = h;
        while (*end && *end != ']') end++;
    } else {
        while (end < at && *end != ':') end++;
    }
    size_t n = (size_t)(end - h);
    if (n >= cap) n = cap - 1;
    memcpy(out, h, n);
    out[n] = 0;
}

/* The port of an http(s) URL, its scheme's default when absent. */
static long url_port(const char *url) {
    const char *h = url ? strstr(url, "://") : 0;
    if (!h) return 0;
    long dflt = strncmp(url, "https", 5) == 0 ? 443 : 80;
    h += 3;
    const char *at = h;
    while (*at && *at != '/' && *at != '?' && *at != '#') at++;
    for (const char *c = h; c < at; c++)
        if (*c == '@') h = c + 1;
    if (*h == '[')
        while (h < at && *h != ']') h++;
    while (h < at && *h != ':') h++;
    return h < at ? strtol(h + 1, 0, 10) : dflt;
}

/* Node's error for a refused (or otherwise failed) connect to ip:port. */
static void *connect_error(const char *ecode, long oserr, const char *ip, long port) {
    void *o = mk_error(KML_ERRKIND_ERROR, port > 0 ? fmt_str("connect %s %s:%ld", ecode, ip, port) : fmt_str("connect %s %s", ecode, ip, 0),
                       __kml_fetch_strs[S_ERRNAME]);
    F(o, KML_ERR_ERRNO, double) = (double)__kml_uv_errno((int)oserr);
    F(o, KML_ERR_CODE, const void *) = ecode;
    F(o, KML_ERROR_SYSCALL, const void *) = __kml_str_from_cstr("connect");
    F(o, KML_ERR_ADDRESS, const void *) = __kml_str_from_cstr(ip);
    if (port > 0) F(o, KML_ERR_PORT, double) = (double)port;
    return o;
}

/* The reason a transfer failed, as Node's fetch (undici) reports it in the
 * TypeError's `cause`: the system error of the connect or the lookup, or
 * an Error with libcurl's message for any other failure. Read off the easy
 * handle before it is freed. */
static void *failure_cause(void *easy, int code) {
    void *errname = __kml_fetch_strs[S_ERRNAME];
    if (code == CURLE_COULDNT_RESOLVE_HOST) {
        char *url = 0;
        char host[256];
        curl_easy_getinfo(easy, CURLINFO_EFFECTIVE_URL, &url);
        url_host(url, host, sizeof host);
        void *o = mk_error(KML_ERRKIND_ERROR, fmt_str("getaddrinfo ENOTFOUND %s", host, 0, 0), errname);
        F(o, KML_ERR_ERRNO, double) = -3008;
        F(o, KML_ERR_CODE, const void *) = __kml_str_from_cstr("ENOTFOUND");
        F(o, KML_ERROR_SYSCALL, const void *) = __kml_str_from_cstr("getaddrinfo");
        void *bag = __kml_dynobj_new();
        __kml_dynobj_set(bag, k_hostname.d, (i64)(intptr_t)__kml_str_from_cstr(host)); /* a string boxes as its pointer */
        F(o, KML_ERR_EXTRA, void *) = bag;
        return o;
    }
    long oserr = 0, port = 0;
    char *ip = 0;
    curl_easy_getinfo(easy, CURLINFO_OS_ERRNO, &oserr);
#ifdef _WIN32
    /* libcurl reports the Winsock code (WSAECONNREFUSED, 10061). */
    {
        extern int __kml_win_wsa_errno(int wsa);
        if (oserr >= 10000) oserr = __kml_win_wsa_errno((int)oserr);
    }
#endif
    curl_easy_getinfo(easy, CURLINFO_PRIMARY_IP, &ip);
    curl_easy_getinfo(easy, CURLINFO_PRIMARY_PORT, &port);
    const char *ecode = oserr ? (const char *)__kml_errno_code((int)oserr) : 0;
    if (code == CURLE_COULDNT_CONNECT && ecode) {
        char *url = 0;
        curl_easy_getinfo(easy, CURLINFO_EFFECTIVE_URL, &url);
        if (port <= 0) port = url_port(url);
        if (ip && *ip) return connect_error(ecode, oserr, ip, port);
        /* libcurl leaves the address unset after a failed connect (macOS):
         * the URL's host's addresses, which libcurl tried in turn. A name
         * with several is Node's AggregateError of one connect error each
         * (its autoSelectFamily attempts), carrying the code. */
        char host[256];
        url_host(url, host, sizeof host);
        struct addrinfo hints = {0}, *res = 0;
        hints.ai_socktype = SOCK_STREAM;
        void **errs = 0;
        i64 n = 0;
        char seen[8][INET6_ADDRSTRLEN + 1];
        if (host[0] && getaddrinfo(host, 0, &hints, &res) == 0) {
            for (struct addrinfo *ai = res; ai && n < 8; ai = ai->ai_next) {
                const void *a = ai->ai_family == AF_INET6 ? (const void *)&((struct sockaddr_in6 *)ai->ai_addr)->sin6_addr
                                                         : (const void *)&((struct sockaddr_in *)ai->ai_addr)->sin_addr;
                char addr[INET6_ADDRSTRLEN + 1];
                if (!inet_ntop(ai->ai_family, a, addr, sizeof addr)) continue;
                int dup = 0;
                for (i64 i = 0; i < n; i++) dup |= strcmp(seen[i], addr) == 0;
                if (dup) continue;
                strcpy(seen[n], addr);
                errs = (void **)kml_realloc(KML_HEAP_WORDS, errs, (size_t)(n + 1) * sizeof(void *));
                errs[n++] = connect_error(ecode, oserr, addr, port);
            }
            freeaddrinfo(res);
        }
        if (n == 1) return errs[0];
        if (n > 1) {
            void *agg = mk_aggregate(__kml_fetch_strs[S_EMPTY], errs, n);
            F(agg, KML_ERR_CODE, const void *) = ecode;
            return agg;
        }
    }
    return mk_error(KML_ERRKIND_ERROR, __kml_str_from_cstr(curl_easy_strerror(code)), errname);
}

/* The rejection of a failed fetch: a request error (negative code) is a
 * TypeError with its own message; a transfer failure is Node's
 * `TypeError: fetch failed`, its reason the cause. */
__attribute__((unused)) static void *fetch_error(pend_t *p) {
    if (p->result < 0 && p->result != -3)
        return mk_error(KML_KIND_TYPEERR, __kml_str_from_cstr(__kml_fetch_errstr((int)p->result)), __kml_fetch_strs[S_TYPEERR]);
    void *e = mk_error(KML_KIND_TYPEERR, __kml_fetch_strs[S_FAILED], __kml_fetch_strs[S_TYPEERR]);
    void *cause = p->cause ? p->cause : mk_error(KML_ERRKIND_ERROR, __kml_str_from_cstr(__kml_fetch_errstr((int)p->result)), __kml_fetch_strs[S_ERRNAME]);
    F(e, KML_ERR_CAUSE, i64) = __kml_nb_pack(KML_TAG_OBJECT, (i64)(intptr_t)cause);
    return e;
}

extern void *curl_multi_init(void);
extern int curl_multi_add_handle(void *, void *);
extern int curl_multi_remove_handle(void *, void *);
extern void *curl_multi_info_read(void *, int *);
extern int curl_multi_timeout(void *, long *);
extern i64 __kml_monotonic_ns(void);
extern _Thread_local void *__kml_curl_multi;
extern void __kml_curl_drain_messages(void);

extern _Thread_local int __kml_jmp_top;
extern int swapcontext(void *, void *);
/* A wait with nothing to park: no coroutine, no async generator step, no
 * connection fiber. Every async callable that waits is a coroutine
 * (TDD-00223 section 2) and the module body with a top-level await is a
 * task (TDD-00224), so reaching this is a compiler bug. Each runtime file
 * that waits has its own copy, so none names another (TDD-00251). */
static void __kml_wait_outside_coroutine(void) {
    fputs("internal error: a wait outside a coroutine inside an event-loop callback\n", stderr);
    abort();
}
extern int __kml_signal_aborted(void *sig);
extern i64 __kml_signal_reason(void *sig);
extern int __kml_signal_timeout(void *sig);
extern int __kml_nb_tag(i64);
extern i64 __kml_nb_pay(i64);
extern void __kml_throw_any(u8 tag, i64 pay) __attribute__((noreturn));
extern void __kml_fetch_body_abort(void *pend, void *err);
extern void __kml_drain_microtasks(void);
/* The current task (hooks.c's), and two routines other runtime files
 * register in hooks.c when they are linked: the task runtime's GC restore
 * under -mm=gc, and the Response-body-promise drain (TDD-00251). Without
 * them no task runs and no body promise waits. */
extern _Thread_local void *__kml_current_task;
extern void (*__kml_hook_task_gc_restore)(void);
extern void (*__kml_hook_fetch_bodyprom_on_done)(void *);
#define GC_RESTORE() __kml_hook_task_gc_restore()

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
        sf_data = (void **)realloc(sf_data, (size_t)nc * sizeof(void *)); /* scratch */
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
    return mk_error(KML_KIND_DOMEXC, __kml_fetch_strs[timeout ? S_TIMEOUTMSG : S_ABORTMSG],
                    __kml_fetch_strs[timeout ? S_TIMEOUTNAME : S_ABORTNAME]);
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

/* ---- driving libcurl ----
 * libcurl is driven through its socket API (TDD-00244 Stage 3):
 * it reports the sockets it waits on and its next deadline through
 * CURLMOPT_SOCKETFUNCTION / CURLMOPT_TIMERFUNCTION, and every drive is a
 * curl_multi_socket_action. Each reported socket is a libuv watcher and the
 * deadline a libuv timer on the event loop (loop.c), whose callbacks drive
 * libcurl inside the wait (no FD_SETSIZE ceiling); a wait outside the loop
 * waits in libcurl itself (curl_multi_poll). On Windows (TDD-00252) a socket libcurl reports is a
 * Winsock SOCKET in both link modes (libcurl calls Winsock, not the shim),
 * which the watch and the outside wait take as it is. */
#ifdef _WIN32
extern void *__kml_loop_sock_watch(intptr_t sock, int fd, int events, void (*cb)(void *, int, int), void *data);
static intptr_t curl_os_sock(int s) { return (intptr_t)s; }
#endif
#define CURLMOPT_SOCKETFUNCTION 20001
#define CURLMOPT_TIMERFUNCTION 20004
#define CURL_SOCKET_TIMEOUT (-1)
extern int curl_multi_setopt(void *, int, ...);
extern int curl_multi_socket_all(void *, int *);
extern int curl_multi_poll(void *, void *, unsigned, int, int *);
/* curl_socket_t: a SOCKET (64-bit) on Windows, where CURL_SOCKET_TIMEOUT is
 * ~0; an int passed there would reach libcurl as 0xFFFFFFFF, an unknown
 * socket, and the timeout (a finished resolve among it) never runs. */
#ifdef _WIN32
extern int kml_curl_multi_socket_action(void *, intptr_t, int, int *) __asm__("curl_multi_socket_action");
#define curl_multi_socket_action(m, s, ev, run) kml_curl_multi_socket_action((m), (intptr_t)(s), (ev), (run))
#else
extern int curl_multi_socket_action(void *, int, int, int *);
#endif

typedef struct { int fd, what; void *uvw; } curl_watch;
extern void *__kml_loop_io_watch(int fd, int events, void (*cb)(void *, int, int), void *data);
extern void __kml_loop_io_events(void *w, int events);
extern void __kml_loop_io_unwatch(void *w);
extern void *__kml_loop_timer_new(void (*cb)(void *), void *data);
extern void __kml_loop_timer_start(void *t, i64 ms);
extern void __kml_loop_timer_stop(void *t);
extern void *__kml_loop_async_new(void (*cb)(void *), void *data);
extern void __kml_loop_handle_ref(void *h, int on);
static _Thread_local void *curl_timer;
static void on_curl_io(void *data, int fd, int ev);
static _Thread_local curl_watch *watches;
static _Thread_local int nwatches, capwatches;
/* 0: none; else the monotonic ns libcurl wants to be driven at */
static _Thread_local i64 curl_deadline;
static _Thread_local int curl_running;

static int on_curl_socket(void *easy, int fd, int what, void *userp, void *sockp) {
    (void)easy; (void)userp; (void)sockp;
    for (int i = 0; i < nwatches; i++) {
        if (watches[i].fd != fd) continue;
        if (what == 4) { /* CURL_POLL_REMOVE: before libcurl closes it */
            __kml_loop_io_unwatch(watches[i].uvw);
            watches[i] = watches[--nwatches];
        } else {
            watches[i].what = what;
            __kml_loop_io_events(watches[i].uvw, what);
        }
        return 0;
    }
    if (what == 4) return 0;
    if (nwatches == capwatches) {
        capwatches = capwatches ? capwatches * 2 : 16;
        watches = realloc(watches, sizeof(curl_watch) * (size_t)capwatches); /* scratch */
    }
    watches[nwatches].fd = fd;
    watches[nwatches].what = what;
#ifdef _WIN32
    watches[nwatches].uvw = __kml_loop_sock_watch(curl_os_sock(fd), fd, what, on_curl_io, 0);
#else
    watches[nwatches].uvw = __kml_loop_io_watch(fd, what, on_curl_io, 0);
#endif
    nwatches++;
    return 0;
}

static void on_curl_due(void *unused);

static int on_curl_timer(void *multi, long ms, void *userp) {
    (void)multi; (void)userp;
    if (!curl_timer) curl_timer = __kml_loop_timer_new(on_curl_due, 0);
    if (ms < 0) {
        curl_deadline = 0;
        __kml_loop_timer_stop(curl_timer);
    } else {
        i64 dl = __kml_monotonic_ns() + (i64)ms * 1000000;
        curl_deadline = dl == 0 ? 1 : dl;
        __kml_loop_timer_start(curl_timer, ms);
    }
    return 0;
}

static void *multi_new(void) {
    void *m = curl_multi_init();
    curl_multi_setopt(m, CURLMOPT_SOCKETFUNCTION, on_curl_socket);
    curl_multi_setopt(m, CURLMOPT_TIMERFUNCTION, on_curl_timer);
    return m;
}

/* Holds the event loop while libcurl has a transfer running, awaited yet
 * or not, as an open socket would: a referenced handle that never fires. */
static _Thread_local void *curl_hold;
static _Thread_local _Bool curl_held;
static void on_curl_hold(void *unused) { (void)unused; }

static void hold_sync(void) {
    _Bool want = curl_running > 0;
    if (want == curl_held) return;
    if (!curl_hold) curl_hold = __kml_loop_async_new(on_curl_hold, 0);
    curl_held = want;
    __kml_loop_handle_ref(curl_hold, want);
}

static void act(int fd, int ev) {
    curl_multi_socket_action(__kml_curl_multi, fd, ev, &curl_running);
    hold_sync();
}

/* Inside the wait: a socket's readiness or the deadline drives libcurl,
 * and finished transfers settle (the loop's pass then fires their
 * reactions and resumes the fibers waiting on them). */
static void on_curl_io(void *data, int fd, int ev) {
    (void)data;
    act(fd, ev);
    __kml_curl_drain_messages();
}

static void on_curl_due(void *unused) {
    (void)unused;
    /* libcurl 8.20's resolver thread pool hands its results back only when
     * every handle is processed; a timeout action alone leaves a finished
     * lookup waiting until the transfer times out (TDD-00252). */
    curl_multi_socket_all(__kml_curl_multi, &curl_running);
    hold_sync();
    curl_deadline = 0;
    act(CURL_SOCKET_TIMEOUT, 0);
    __kml_curl_drain_messages();
}

/* A timeout action: libcurl serves whatever deadline is due (a transfer
 * just added is due at once, so this also starts it) and reports how many
 * transfers are still running, which a removed or finished transfer changes
 * without any socket event. The stored deadline is cleared only when due:
 * the timer callback reports changes only. */
static void kick(void) {
    if (curl_deadline != 0 && curl_deadline <= __kml_monotonic_ns()) curl_deadline = 0;
    act(CURL_SOCKET_TIMEOUT, 0);
}

/* A wait outside the event loop: libcurl waits on its own sockets for up
 * to max_ms (bounded by its deadline), then every socket is served. */
static void drive_once(int max_ms) {
    kick();
    if (max_ms > 0 && curl_running > 0) curl_multi_poll(__kml_curl_multi, 0, 0, max_ms, 0);
    curl_multi_socket_all(__kml_curl_multi, &curl_running);
    hold_sync();
    kick();
    __kml_curl_drain_messages();
}

/* One drive and drain with no wait: the scheduler's and the loop's
 * keep-alive check's hook. It serves libcurl's sockets without blocking: a scheduler step can run in a wait outside the
 * loop (a connection fiber consuming its own request body), and a transfer
 * whose readiness nothing reports would stall there. */
void __kml_curl_kick(void) {
    if (!__kml_curl_multi) return;
    drive_once(0);
}

/* One drive outside the event loop, waiting up to max_ms on libcurl's
 * sockets; returns the transfers still running. */
int __kml_curl_drive_once(int max_ms) {
    if (!__kml_curl_multi) return 0;
    drive_once(max_ms);
    return curl_running;
}

/* libcurl's count of transfers still in flight (0 with no multi handle),
 * after aborting those whose signal fired and draining those that just
 * finished; each pass's libcurl step. What holds the loop is curl_hold. */
int __kml_curl_inflight(void) {
    __kml_sigfetch_scan();
    if (!__kml_curl_multi) return 0;
    __kml_curl_kick();
    return curl_running;
}

/* The event loop's view of libcurl (loop.c): whether the multi handle
 * exists; on Windows also its sockets merged into the loop's sets, its own
 * deadline (0 for none), and one drive after the wait. */
_Bool __kml_curl_loop_active(void) { return __kml_curl_multi != 0; }


/* Starts a transfer without blocking and returns its pending struct. method/
 * headers/body are each nullable (a null skips the setopt); headers is a
 * curl_slist*. bodylen < 0: a NUL-terminated body, else its byte length so a
 * binary body is sent whole. */
/* The fetch standard's bad ports: a request to one fails without a
 * connection being attempted. */
static const unsigned short bad_ports[] = {
    1, 7, 9, 11, 13, 15, 17, 19, 20, 21, 22, 23, 25, 37, 42, 43, 53, 69, 77, 79, 87, 95, 101, 102,
    103, 104, 109, 110, 111, 113, 115, 117, 119, 123, 135, 137, 139, 143, 161, 179, 389, 427, 465,
    512, 513, 514, 515, 526, 530, 531, 532, 540, 548, 554, 556, 563, 587, 601, 636, 989, 990, 993,
    995, 1719, 1720, 1723, 2049, 3659, 4045, 4190, 5060, 5061, 6000, 6566, 6665, 6666, 6667, 6668,
    6669, 6679, 6697, 10080,
};

static _Bool url_bad_port(const char *url) {
    if (!url || (strncmp(url, "http://", 7) != 0 && strncmp(url, "https://", 8) != 0)) return 0;
    long port = url_port(url);
    for (size_t i = 0; i < sizeof bad_ports / sizeof bad_ports[0]; i++)
        if (bad_ports[i] == port) return 1;
    return 0;
}

void *__kml_fetch_async_n(const char *url, const char *method, void *headers, const char *body, i64 bodylen, void *signal) {
    if (url_bad_port(url)) return __kml_fetch_failed_pending(-3);
    if (!__kml_curl_inited) {
        curl_global_init(3);
        __kml_curl_inited = 1;
    }
    if (!__kml_curl_multi) __kml_curl_multi = multi_new();
    void *multi = __kml_curl_multi;

    kbuf *buf = (kbuf *)kml_alloc(KML_HEAP_WORDS, sizeof(kbuf));
    buf->data = 0;
    buf->len = buf->cap = 0;
    buf->pend = 0;
    /* The header-capture buffer (ADR-00490): same shape, fed by the same write
     * callback via HEADERFUNCTION/HEADERDATA, reachable from the body buffer
     * so the Response can parse it lazily. Its `pend` is null so a header
     * write always takes the buffer branch. */
    kbuf *hbuf = (kbuf *)kml_alloc(KML_HEAP_WORDS, sizeof(kbuf));
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
#if defined(__APPLE__)
    curl_easy_setopt(curl, CURLOPT_OPENSOCKETFUNCTION, lowfd_open);
    curl_easy_setopt(curl, CURLOPT_CLOSESOCKETFUNCTION, lowfd_close);
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

    pend_t *p = (pend_t *)kml_alloc(KML_HEAP_WORDS, sizeof(pend_t));
    p->bridge = 0;
    p->cause = 0;
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
    __kml_curl_kick();
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
        if (m->result != 0) p->cause = failure_cause(easy, m->result);
        curl_multi_remove_handle(multi, easy);
        curl_easy_cleanup(easy);
        p->done = 1;
        __kml_fetch_body_on_done(p);
        __kml_hook_fetch_bodyprom_on_done(p);
    }
}

/* A drive without waiting after a paused body resumes: a timeout action
 * starts what unpausing made due; returns whether transfers still run. */
_Bool __kml_fetch_pump(void) {
    __kml_sigfetch_scan();
    if (!__kml_curl_multi) return 0;
    kick();
    __kml_curl_drain_messages();
    return curl_running > 0;
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
    if (p->result != 0) __kml_throw(fetch_error(p));
    kbuf *b = p->buf;
    out->status = p->status;
    if (!b->data) {
        char *e = (char *)kml_alloc(KML_HEAP_RAW, 1);
        e[0] = 0;
        out->body = e;
        out->len = 0;
    } else {
        out->body = b->data;
        out->len = b->len;
    }
}

/* ---- waits ---- */

/* Parks the caller once while `what` is unsatisfied: on a task it records
 * what it waits for (task_off), suspends, and resumes. Returns 0 when the
 * caller is on the main stack. */
static _Bool park(void *what, i64 task_off, _Bool use_task) {
    void *ct = use_task ? __kml_current_task : 0;
    if (ct) {
        F(ct, task_off, void *) = what;
        T_STATE(ct) = 1;
        T_JMPTOP(ct) = (i64)(unsigned)__kml_jmp_top;
        KML_SWAPCONTEXT(T_CTX(ct), T_RESUMER(ct));
        GC_RESTORE();
        return 1;
    }
    return 0;
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
        if (park(p, KML_TASK_PENDING_FETCH, 1)) continue;
        __kml_wait_outside_coroutine();
    }
    __kml_pending_finish_c(p, out);
}

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
        if (park(p, KML_TASK_PENDING_FETCH, 1)) continue;
        /* Module top level: a turn of the loop, which drains microtasks */
        __kml_wait_outside_coroutine();
    }
}

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
        char *e = (char *)kml_alloc(KML_HEAP_RAW, 1);
        e[0] = 0;
        out->body = e;
        out->len = 0;
    } else {
        out->body = b->data;
        out->len = b->len;
    }
}

/* __kml_await_fetch's loop finishing non-throwingly: XMLHttpRequest.send()'s
 * whole transfer mechanism. A blocking wait by definition, one goroutine
 * threads issue concurrently: it drives libcurl alone and never takes a turn
 * of the event loop, and has no task or abort path. */
void __kml_await_fetch_settled_c(void *pending, sres *out) {
    pend_t *p = (pend_t *)pending;
    while (!p->done) {
        if (park(p, 0, 0)) continue;
        __kml_curl_drive_once(100);
    }
    __kml_pending_finish_settled_c(p, out);
}

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
    void **errs = (void **)kml_alloc(KML_HEAP_WORDS, (size_t)g->count * sizeof(void *));
    for (i64 i = 0; i < g->count; i++) errs[i] = fetch_error(g->members[i]);
    void *agg = mk_aggregate(__kml_fetch_strs[S_AGGMSG], errs, g->count);
    __kml_throw(agg);
}

/* __kml_await_fetch's wait shape, polling the group instead of one done flag
 * and parking in the pendingGroup slot. */
void __kml_await_group_wait(void *group) {
    while (!__kml_group_satisfied(group)) {
        if (park(group, KML_TASK_PENDING_GROUP, 1)) continue;
        __kml_wait_outside_coroutine();
    }
}

/* ---- Response header and status helpers ---- */

/* The captured raw header text of a Response (every block of a redirect
 * chain), or null for a combinator-built Response / one with none. */
static const char *raw_headers(void *pending) {
    pend_t *p = (pend_t *)pending;
    if (!p || !p->buf || !p->buf->hbuf) return 0;
    return ((kbuf *)p->buf->hbuf)->data;
}

void *__kml_fetch_headers_raw(void *pending) {
    const char *raw = raw_headers(pending);
    return raw ? __kml_str_from_cstr(raw) : __kml_fetch_strs[S_EMPTY];
}

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
