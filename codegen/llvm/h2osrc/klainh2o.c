// ---- klain:http's h2o backend (TDD-00255 Stage 2) -----------------------------
// libh2o's libuv build on the program's own loop: one listener, h2o's
// HTTP/1.1 and HTTP/2 (h2c, by prior knowledge or upgrade) on each accepted
// socket, and one handler that hands each request to TypeScript
// (lib/node/internal_khttp_h2o.ts) as an exchange id. h2o buffers a request's
// whole body before the handler runs (0.13 has no streaming request body).
//
// Lifetimes: an exchange lives until h2o disposes of its request (the
// response is out, or the connection went away); calls on a gone exchange do
// nothing. Under -mm=gc, h2o's allocations come from the collector while
// libuv's lists are raw memory, so every handle h2o's state hangs off is held
// in a table here, which the collector scans.

#include <errno.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <uv.h>
#include <h2o.h>

extern uv_loop_t *__kml_loop_uv(void);
extern char *__kml_str_alloc(int64_t n);
extern void __kml_str_finalize(char *s);
extern void __kml_drain_microtasks(void);

typedef void (*kh_inv2)(void *, double, double);

// Node's MakeCallback: a callback from the loop, then the microtasks it left.
static void kh_call(void *inv, void *clo, double a, double b) {
    ((kh_inv2)inv)(clo, a, b);
    __kml_drain_microtasks();
}

#define KH_MAX_HEADER (16 * 1024) // Node's --max-http-header-size

typedef struct kh_ex {
    h2o_req_t *req;      // NULL once h2o disposed of it
    h2o_generator_t gen; // a streamed response's
    int started;         // a streamed response started
    int writing;         // a chunk sent, its proceed not yet called
    char *wbuf;          // that chunk's bytes, until proceed
    void *winv, *wclo;   // its callback (status)
    h2o_socket_t *sock;  // its connection's, for the busy count
    double id;
} kh_ex;

// The dispose hook h2o_mem_alloc_shared links to a request's pool.
typedef struct {
    kh_ex *ex;
} kh_token;

static h2o_globalconf_t kh_config;
static h2o_context_t kh_ctx;
static h2o_accept_ctx_t kh_accept;
static int kh_inited;
static uv_tcp_t *kh_listener;
static void *kh_inv, *kh_clo;

// Exchanges by id: slot | generation << 20, so a stale id never finds the
// slot's next occupant.
#define KH_SLOT_BITS 20
static kh_ex **kh_exs;
static uint32_t *kh_gens;
static int kh_nex, kh_cex;
static int *kh_free;
static int kh_nfree;

// The accepted connections' handles, until libuv closed them, with their h2o
// sockets and how many requests each has unanswered.
static uv_tcp_t **kh_conns;
static h2o_socket_t **kh_socks;
static int *kh_busy;
static int kh_nconns, kh_cconns;

// libh2o 0.13 keeps an idle HTTP/1 keep-alive connection open through a
// shutdown, so after close() the runtime ends each one once it is idle.
static void kh_end_conn(int i) {
    uv_os_fd_t fd;
    if (uv_fileno((uv_handle_t *)kh_conns[i], &fd) == 0) shutdown(fd, SHUT_RDWR);
}
static int kh_conn_index(h2o_socket_t *sock) {
    for (int i = 0; sock && i < kh_nconns; i++)
        if (kh_socks[i] == sock) return i;
    return -1;
}

static kh_ex *kh_get(double id) {
    if (id < 0) return NULL;
    uint64_t v = (uint64_t)id;
    int slot = (int)(v & ((1u << KH_SLOT_BITS) - 1));
    if (slot >= kh_nex || !kh_exs[slot] || kh_gens[slot] != (uint32_t)(v >> KH_SLOT_BITS)) return NULL;
    return kh_exs[slot];
}

static kh_ex *kh_new(void) {
    int slot;
    if (kh_nfree > 0) {
        slot = kh_free[--kh_nfree];
    } else {
        if (kh_nex == kh_cex) {
            kh_cex = kh_cex ? kh_cex * 2 : 64;
            kh_exs = (kh_ex **)realloc(kh_exs, (size_t)kh_cex * sizeof *kh_exs); /* scratch */
            kh_gens = (uint32_t *)realloc(kh_gens, (size_t)kh_cex * sizeof *kh_gens); /* scratch */
            kh_free = (int *)realloc(kh_free, (size_t)kh_cex * sizeof *kh_free); /* scratch */
        }
        slot = kh_nex++;
        kh_gens[slot] = 0;
    }
    kh_ex *ex = (kh_ex *)calloc(1, sizeof *ex); /* scratch */
    kh_gens[slot]++;
    kh_exs[slot] = ex;
    ex->id = (double)(((uint64_t)kh_gens[slot] << KH_SLOT_BITS) | (uint64_t)slot);
    return ex;
}

static void kh_release(kh_ex *ex) {
    uint64_t v = (uint64_t)ex->id;
    int slot = (int)(v & ((1u << KH_SLOT_BITS) - 1));
    kh_exs[slot] = NULL;
    kh_free[kh_nfree++] = slot;
    free(ex->wbuf); /* scratch */
    free(ex); /* scratch */
}

// A pending write ends: its callback with status (0, or -1 when the
// response is gone).
static void kh_write_done(kh_ex *ex, int status) {
    if (!ex->writing) return;
    ex->writing = 0;
    free(ex->wbuf); /* scratch */
    ex->wbuf = NULL;
    void *inv = ex->winv, *clo = ex->wclo;
    ex->winv = ex->wclo = NULL;
    if (inv) kh_call(inv, clo, status, 0);
}

// The request's pool is cleared: the response is out or the connection gone.
static void kh_on_dispose(void *p) {
    kh_ex *ex = ((kh_token *)p)->ex;
    if (!ex) return;
    ex->req = NULL;
    int ci = kh_conn_index(ex->sock);
    if (ci >= 0 && --kh_busy[ci] == 0 && kh_ctx.shutdown_requested) kh_end_conn(ci);
    kh_write_done(ex, -1);
    kh_release(ex);
}

static void kh_on_proceed(h2o_generator_t *gen, h2o_req_t *req) {
    (void)req;
    kh_ex *ex = H2O_STRUCT_FROM_MEMBER(kh_ex, gen, gen);
    kh_write_done(ex, 0);
}

static void kh_on_stop(h2o_generator_t *gen, h2o_req_t *req) {
    (void)req;
    kh_ex *ex = H2O_STRUCT_FROM_MEMBER(kh_ex, gen, gen);
    ex->req = NULL;
    kh_write_done(ex, -1);
}

static int kh_on_req(h2o_handler_t *self, h2o_req_t *req) {
    (void)self;
    size_t hdr = req->method.len + req->path.len;
    for (size_t i = 0; i < req->headers.size; i++) hdr += req->headers.entries[i].name->len + req->headers.entries[i].value.len + 4;
    if (hdr > KH_MAX_HEADER) {
        req->http1_is_persistent = 0;
        h2o_send_error_generic(req, 431, "Request Header Fields Too Large", "", H2O_SEND_ERROR_HTTP1_CLOSE_CONNECTION);
        return 0;
    }
    kh_ex *ex = kh_new();
    ex->req = req;
    ex->sock = req->conn->callbacks->get_socket ? req->conn->callbacks->get_socket(req->conn) : NULL;
    int ci = kh_conn_index(ex->sock);
    if (ci >= 0) kh_busy[ci]++;
    kh_token *tok = (kh_token *)h2o_mem_alloc_shared(&req->pool, sizeof *tok, kh_on_dispose);
    tok->ex = ex;
    if (kh_inv) kh_call(kh_inv, kh_clo, ex->id, 0);
    return 0;
}

static void kh_conn_closed(uv_handle_t *h) {
    for (int i = 0; i < kh_nconns; i++) {
        if (kh_conns[i] == (uv_tcp_t *)h) {
            kh_nconns--;
            kh_conns[i] = kh_conns[kh_nconns];
            kh_socks[i] = kh_socks[kh_nconns];
            kh_busy[i] = kh_busy[kh_nconns];
            break;
        }
    }
    free(h); /* scratch */
}

static void kh_on_accept(uv_stream_t *l, int status) {
    if (status != 0) return;
    uv_tcp_t *conn = (uv_tcp_t *)malloc(sizeof *conn); /* scratch */
    uv_tcp_init(l->loop, conn);
    if (uv_accept(l, (uv_stream_t *)conn) != 0) {
        uv_close((uv_handle_t *)conn, (uv_close_cb)free); /* scratch */
        return;
    }
    if (kh_nconns == kh_cconns) {
        kh_cconns = kh_cconns ? kh_cconns * 2 : 64;
        kh_conns = (uv_tcp_t **)realloc(kh_conns, (size_t)kh_cconns * sizeof *kh_conns); /* scratch */
        kh_socks = (h2o_socket_t **)realloc(kh_socks, (size_t)kh_cconns * sizeof *kh_socks); /* scratch */
        kh_busy = (int *)realloc(kh_busy, (size_t)kh_cconns * sizeof *kh_busy); /* scratch */
    }
    uv_tcp_nodelay(conn, 1);
    h2o_socket_t *sock = h2o_uv_socket_create((uv_stream_t *)conn, kh_conn_closed);
    kh_conns[kh_nconns] = conn;
    kh_socks[kh_nconns] = sock;
    kh_busy[kh_nconns++] = 0;
    h2o_accept(&kh_accept, sock);
}

// h2o's own timers never hold the loop: the listener and the connections do.
static uv_handle_t **kh_before;
static int kh_nbefore, kh_cbefore;
static void kh_note(uv_handle_t *h, void *arg) {
    (void)arg;
    if (kh_nbefore == kh_cbefore) {
        kh_cbefore = kh_cbefore ? kh_cbefore * 2 : 64;
        kh_before = (uv_handle_t **)realloc(kh_before, (size_t)kh_cbefore * sizeof *kh_before); /* scratch */
    }
    kh_before[kh_nbefore++] = h;
}
static void kh_unref_new(uv_handle_t *h, void *arg) {
    (void)arg;
    for (int i = 0; i < kh_nbefore; i++)
        if (kh_before[i] == h) return;
    uv_unref(h);
}

static void kh_init(void) {
    if (kh_inited) return;
    kh_inited = 1;
    uv_loop_t *loop = __kml_loop_uv();
    h2o_config_init(&kh_config);
    kh_config.server_name = h2o_iovec_init(H2O_STRLIT(""));
    h2o_hostconf_t *host = h2o_config_register_host(&kh_config, h2o_iovec_init(H2O_STRLIT("default")), 65535);
    h2o_pathconf_t *path = h2o_config_register_path(host, "/", 0);
    h2o_handler_t *h = h2o_create_handler(path, sizeof *h);
    h->on_req = kh_on_req;
    kh_nbefore = 0;
    uv_walk(loop, kh_note, NULL);
    h2o_context_init(&kh_ctx, loop, &kh_config);
    uv_walk(loop, kh_unref_new, NULL);
    kh_accept.ctx = &kh_ctx;
    kh_accept.hosts = kh_config.hosts;
}

// Listen on host:port (every address for ""), each request going to
// inv(clo, exchange): 0, or the negative errno.
double __kml_native_khttp_h2o_serve(const char *host, double port, void *inv, void *clo) {
    kh_init();
    kh_ctx.shutdown_requested = 0; // a later listen after a close
    uv_loop_t *loop = __kml_loop_uv();
    uv_tcp_t *l = (uv_tcp_t *)malloc(sizeof *l); /* scratch */
    uv_tcp_init(loop, l);
    struct sockaddr_storage ss;
    int r;
    if (!host || !*host) {
        uv_ip6_addr("::", (int)port, (struct sockaddr_in6 *)&ss);
        r = uv_tcp_bind(l, (struct sockaddr *)&ss, 0);
        if (r == UV_EAFNOSUPPORT || r == UV_EADDRNOTAVAIL) {
            uv_ip4_addr("0.0.0.0", (int)port, (struct sockaddr_in *)&ss);
            r = uv_tcp_bind(l, (struct sockaddr *)&ss, 0);
        }
    } else if (uv_ip4_addr(host, (int)port, (struct sockaddr_in *)&ss) == 0) {
        r = uv_tcp_bind(l, (struct sockaddr *)&ss, 0);
    } else if (uv_ip6_addr(host, (int)port, (struct sockaddr_in6 *)&ss) == 0) {
        r = uv_tcp_bind(l, (struct sockaddr *)&ss, 0);
    } else {
        r = UV_EINVAL;
    }
    if (r == 0) r = uv_listen((uv_stream_t *)l, 511, kh_on_accept);
    if (r != 0) {
        uv_close((uv_handle_t *)l, (uv_close_cb)free); /* scratch */
        return r; // libuv's are -errno on POSIX
    }
    kh_listener = l;
    kh_inv = inv;
    kh_clo = clo;
    return 0;
}

static char *kh_str(const char *s, size_t n) {
    char *out = __kml_str_alloc((int64_t)n + 1);
    if (n) memcpy(out, s, n);
    out[n] = 0;
    __kml_str_finalize(out);
    return out;
}

// 2 the headers as "name\nvalue\n" pairs (lowercase names; HTTP/2's
// authority as host), 3 the query (the target after its first `?`).
char *__kml_native_khttp_h2o_req_string(double id, double which) {
    kh_ex *ex = kh_get(id);
    if (!ex || !ex->req) return kh_str("", 0);
    h2o_req_t *req = ex->req;
    switch ((int)which) {
    case 3: {
        size_t q = 0;
        while (q < req->path.len && req->path.base[q] != '?') q++;
        return q < req->path.len ? kh_str(req->path.base + q + 1, req->path.len - q - 1) : kh_str("", 0);
    }
    case 2: {
        size_t n = 0;
        int host = 0;
        for (size_t i = 0; i < req->headers.size; i++) {
            h2o_header_t *h = &req->headers.entries[i];
            n += h->name->len + h->value.len + 2;
            if (h->name == &H2O_TOKEN_HOST->buf) host = 1;
        }
        if (!host && req->authority.len) n += 6 + req->authority.len;
        char *out = __kml_str_alloc((int64_t)n + 1);
        char *p = out;
        if (!host && req->authority.len) {
            memcpy(p, "host\n", 5), p += 5;
            memcpy(p, req->authority.base, req->authority.len), p += req->authority.len;
            *p++ = '\n';
        }
        for (size_t i = 0; i < req->headers.size; i++) {
            h2o_header_t *h = &req->headers.entries[i];
            memcpy(p, h->name->base, h->name->len), p += h->name->len;
            *p++ = '\n';
            memcpy(p, h->value.base, h->value.len), p += h->value.len;
            *p++ = '\n';
        }
        *p = 0;
        __kml_str_finalize(out);
        return out;
    }
    }
    return kh_str("", 0);
}

// The request body's length, and a copy of it into buf.
double __kml_native_khttp_h2o_body_length(double id) {
    kh_ex *ex = kh_get(id);
    if (!ex || !ex->req || !ex->req->entity.base) return 0;
    return (double)ex->req->entity.len;
}

double __kml_native_khttp_h2o_body(double id, void *buf, int64_t size) {
    kh_ex *ex = kh_get(id);
    if (!ex || !ex->req || !ex->req->entity.base) return 0;
    size_t n = ex->req->entity.len < (size_t)size ? ex->req->entity.len : (size_t)size;
    memcpy(buf, ex->req->entity.base, n);
    return (double)n;
}

// Node's STATUS_CODES for the common statuses, as the native backend has.
static const char *kh_reason(int status) {
    switch (status) {
    case 100: return "Continue";
    case 101: return "Switching Protocols";
    case 200: return "OK";
    case 201: return "Created";
    case 202: return "Accepted";
    case 204: return "No Content";
    case 206: return "Partial Content";
    case 301: return "Moved Permanently";
    case 302: return "Found";
    case 303: return "See Other";
    case 304: return "Not Modified";
    case 307: return "Temporary Redirect";
    case 308: return "Permanent Redirect";
    case 400: return "Bad Request";
    case 401: return "Unauthorized";
    case 403: return "Forbidden";
    case 404: return "Not Found";
    case 405: return "Method Not Allowed";
    case 409: return "Conflict";
    case 410: return "Gone";
    case 413: return "Payload Too Large";
    case 415: return "Unsupported Media Type";
    case 418: return "I'm a Teapot";
    case 422: return "Unprocessable Entity";
    case 429: return "Too Many Requests";
    case 431: return "Request Header Fields Too Large";
    case 500: return "Internal Server Error";
    case 501: return "Not Implemented";
    case 502: return "Bad Gateway";
    case 503: return "Service Unavailable";
    case 504: return "Gateway Timeout";
    }
    return "OK";
}

// The status and the "name\nvalue\n" headers onto the response. h2o writes
// the framing fields itself.
static void kh_head(h2o_req_t *req, double status, const char *headers) {
    req->res.status = (int)status;
    req->res.reason = kh_reason((int)status);
    if (kh_ctx.shutdown_requested) req->http1_is_persistent = 0;
    const char *p = headers ? headers : "";
    while (*p) {
        const char *ne = strchr(p, '\n');
        if (!ne) break;
        const char *v = ne + 1;
        const char *ve = strchr(v, '\n');
        if (!ve) break;
        size_t nl = (size_t)(ne - p), vl = (size_t)(ve - v);
        char *name = h2o_mem_alloc_pool(&req->pool, nl);
        for (size_t i = 0; i < nl; i++) name[i] = (char)((p[i] >= 'A' && p[i] <= 'Z') ? p[i] + 32 : p[i]);
        int skip = (nl == 14 && memcmp(name, "content-length", 14) == 0) || (nl == 17 && memcmp(name, "transfer-encoding", 17) == 0) ||
                   (nl == 10 && memcmp(name, "connection", 10) == 0) || (nl == 10 && memcmp(name, "keep-alive", 10) == 0);
        if (nl == 10 && memcmp(name, "connection", 10) == 0 && vl == 5 && strncasecmp(v, "close", 5) == 0) req->http1_is_persistent = 0;
        if (!skip) {
            // The name as written goes out on HTTP/1; HTTP/2 lowercases it.
            h2o_iovec_t orig = h2o_strdup(&req->pool, p, nl);
            h2o_iovec_t val = h2o_strdup(&req->pool, v, vl);
            h2o_add_header_by_str(&req->pool, &req->res.headers, name, nl, 1, orig.base, val.base, val.len);
        }
        p = ve + 1;
    }
}

// HttpRequest's layout (`@lowerType kml_khttp_req`, kml_http_request.ts).
extern const int64_t __kml_lt_kml_khttp_req_size, __kml_lt_kml_khttp_req_tag, __kml_lt_kml_khttp_req_vtable_at;
extern void *const __kml_lt_kml_khttp_req_vtable;
extern const int64_t __kml_lt_kml_khttp_req_method, __kml_lt_kml_khttp_req_path;
extern void (*const __kml_lt_kml_khttp_req_init)(void *self);

// The exchange's HttpRequest, with its method and path (the target before
// any `?`); the backend adopts it with the query and headers.
void *__kml_native_khttp_h2o_request(double id) {
    kh_ex *ex = kh_get(id);
    char *self = (char *)calloc(1, (size_t)__kml_lt_kml_khttp_req_size);
    *(int64_t *)self = __kml_lt_kml_khttp_req_tag;
    if (__kml_lt_kml_khttp_req_vtable_at) *(void **)(self + __kml_lt_kml_khttp_req_vtable_at) = __kml_lt_kml_khttp_req_vtable;
    __kml_lt_kml_khttp_req_init(self);
    h2o_req_t *req = ex ? ex->req : NULL;
    const char *t = req ? req->path.base : "";
    size_t tl = req ? req->path.len : 0, pl = 0;
    while (pl < tl && t[pl] != '?') pl++;
    *(char **)(self + __kml_lt_kml_khttp_req_method) = req ? kh_str(req->method.base, req->method.len) : kh_str("", 0);
    *(char **)(self + __kml_lt_kml_khttp_req_path) = kh_str(t, pl);
    return self;
}

// The whole response.
void __kml_native_khttp_h2o_respond(double id, double status, const char *headers, void *body, int64_t len) {
    kh_ex *ex = kh_get(id);
    if (!ex || !ex->req || ex->started) return;
    h2o_req_t *req = ex->req;
    kh_head(req, status, headers);
    char *b = len > 0 ? h2o_mem_alloc_pool(&req->pool, (size_t)len) : NULL;
    if (len > 0) memcpy(b, body, (size_t)len);
    ex->started = 1;
    req->res.content_length = (size_t)(len > 0 ? len : 0);
    h2o_send_inline(req, b, (size_t)(len > 0 ? len : 0));
}

// A streamed response: its head, each chunk (cb(status) once h2o took it,
// the backpressure signal), its end.
void __kml_native_khttp_h2o_respond_start(double id, double status, const char *headers) {
    kh_ex *ex = kh_get(id);
    if (!ex || !ex->req || ex->started) return;
    kh_head(ex->req, status, headers);
    ex->started = 1;
    ex->gen.proceed = kh_on_proceed;
    ex->gen.stop = kh_on_stop;
    h2o_start_response(ex->req, &ex->gen);
}

void __kml_native_khttp_h2o_respond_write(double id, void *data, int64_t len, void *inv, void *clo) {
    kh_ex *ex = kh_get(id);
    if (!ex || !ex->req || !ex->started || ex->writing) {
        if (inv) kh_call(inv, clo, -1, 0);
        return;
    }
    ex->wbuf = (char *)malloc(len > 0 ? (size_t)len : 1); /* scratch */
    if (len > 0) memcpy(ex->wbuf, data, (size_t)len);
    ex->writing = 1;
    ex->winv = inv;
    ex->wclo = clo;
    h2o_iovec_t buf = h2o_iovec_init(ex->wbuf, (size_t)len);
    h2o_send(ex->req, &buf, 1, H2O_SEND_STATE_IN_PROGRESS);
}

void __kml_native_khttp_h2o_respond_end(double id) {
    kh_ex *ex = kh_get(id);
    if (!ex || !ex->req || !ex->started || ex->writing) return;
    h2o_send(ex->req, NULL, 0, H2O_SEND_STATE_FINAL);
}

// The handler failed: a streamed response is cut off, any other answered 500.
void __kml_native_khttp_h2o_abort(double id) {
    kh_ex *ex = kh_get(id);
    if (!ex || !ex->req) return;
    if (ex->started) {
        if (!ex->writing) h2o_send(ex->req, NULL, 0, H2O_SEND_STATE_ERROR);
        return;
    }
    ex->started = 1;
    h2o_send_error_generic(ex->req, 500, "Internal Server Error", "", H2O_SEND_ERROR_HTTP1_CLOSE_CONNECTION);
}

// Stop accepting; idle connections close, the ones answering finish.
void __kml_native_khttp_h2o_close(void) {
    if (kh_listener) {
        uv_close((uv_handle_t *)kh_listener, (uv_close_cb)free); /* scratch */
        kh_listener = NULL;
    }
    if (kh_inited) h2o_context_request_shutdown(&kh_ctx);
    for (int i = 0; i < kh_nconns; i++)
        if (kh_busy[i] == 0) kh_end_conn(i);
}

// Every connection now, answered or not: h2o sees each socket end.
void __kml_native_khttp_h2o_close_all(void) {
    for (int i = 0; i < kh_nconns; i++) kh_end_conn(i);
}
