// klaincares.c — the dns module's resolver: Node's cares_wrap.cc over
// c-ares (the `@link cares` natives of lib/native.d.ts). A channel is a
// ChannelWrap; a query's answer is parsed with c-ares' own parsers into the
// value cares_wrap hands its oncomplete, and reaches the callback through
// the pool's completions as JSON in nativeLastString(): {"r":answer} or
// {"r":answer,"x":ttls}. A failure is the c-ares status number.
//
// POSIX drives each channel on its loop as cares_wrap does: c-ares' sockets
// polled with uv_poll_t, its timeouts with a uv_timer_t. Windows has no
// libuv loop yet (win32io's reactor), so there a channel runs on c-ares'
// own event thread, which posts each answer to the loop.
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <pthread.h>
#include <ares.h>
#include <ares_nameser.h>
#ifndef _WIN32
#include <arpa/inet.h>
#include <netinet/in.h>
#include <uv.h>
extern uv_loop_t *__kml_loop_uv(void);
extern void *__kml_loop_raw_alloc(size_t n);
extern void __kml_loop_free_handle(uv_handle_t *h);
extern void __kml_loop_fd_forget(int fd);
#endif
extern void *__kml_pool_begin(void *inv, void *clo);
extern void __kml_pool_finish(void *item, int64_t err, char *res_str);
extern char *__kml_str_alloc(int64_t n);
extern void __kml_str_finalize(char *s);

// cares_wrap.h's pseudo-type for "a CNAME, or else A records".
#define ns_t_cname_or_a -1

// ---- JSON out ----
typedef struct { char *s; size_t n, cap; } kml_jbuf;

static void jb_put(kml_jbuf *b, const char *p, size_t n) {
    if (b->n + n + 1 > b->cap) {
        size_t cap = b->cap ? b->cap * 2 : 128;
        while (cap < b->n + n + 1) cap *= 2;
        b->s = (char *)realloc(b->s, cap);
        b->cap = cap;
    }
    memcpy(b->s + b->n, p, n);
    b->n += n;
    b->s[b->n] = 0;
}
static void jb_cstr(kml_jbuf *b, const char *p) { jb_put(b, p, strlen(p)); }
// A JSON string of the n bytes at p, each byte its own code point (V8's
// OneByteString).
static void jb_json(kml_jbuf *b, const char *p, size_t n) {
    jb_put(b, "\"", 1);
    for (size_t i = 0; i < n; i++) {
        unsigned char c = (unsigned char)p[i];
        char esc[8];
        if (c == '"' || c == '\\') {
            esc[0] = '\\';
            esc[1] = (char)c;
            jb_put(b, esc, 2);
        } else if (c < 0x20 || c >= 0x7f) {
            snprintf(esc, sizeof esc, "\\u%04x", c);
            jb_put(b, esc, 6);
        } else {
            jb_put(b, (const char *)&c, 1);
        }
    }
    jb_put(b, "\"", 1);
}
static void jb_int(kml_jbuf *b, long long v) {
    char t[32];
    snprintf(t, sizeof t, "%lld", v);
    jb_cstr(b, t);
}

#ifndef _WIN32
typedef struct kml_ares_task {
    struct kml_ares_task *next;
    ares_socket_t sock;
    uv_poll_t *poll;
} kml_ares_task;
#endif

typedef struct {
    int used;
    ares_channel channel;
#ifndef _WIN32
    uv_loop_t *loop;
    uv_timer_t *timer;
    kml_ares_task *tasks;
#endif
    int timeout, tries, max_timeout;
    int query_last_ok, is_servers_default, library_inited;
    int active_query_count;
} kml_ares_chan;

#define KML_ARES_MAXCHAN 256
static kml_ares_chan ares_chans[KML_ARES_MAXCHAN];
static pthread_mutex_t ares_library_mutex = PTHREAD_MUTEX_INITIALIZER;

#ifndef _WIN32
static void ares_close_free(uv_handle_t *h) { __kml_loop_free_handle(h); }

static void kml_ares_timeout(uv_timer_t *t) {
    kml_ares_chan *c = (kml_ares_chan *)t->data;
    ares_process_fd(c->channel, ARES_SOCKET_BAD, ARES_SOCKET_BAD);
}

static void kml_ares_start_timer(kml_ares_chan *c) {
    if (c->timer == NULL) {
        c->timer = (uv_timer_t *)__kml_loop_raw_alloc(sizeof *c->timer);
        uv_timer_init(c->loop, c->timer);
        c->timer->data = c;
    } else if (uv_is_active((uv_handle_t *)c->timer)) {
        return;
    }
    int timeout = c->timeout;
    if (timeout <= 0 || timeout > 1000) timeout = 1000;
    uv_timer_start(c->timer, kml_ares_timeout, timeout, timeout);
}

static void kml_ares_close_timer(kml_ares_chan *c) {
    if (c->timer == NULL) return;
    uv_close((uv_handle_t *)c->timer, ares_close_free);
    c->timer = NULL;
}

static void kml_ares_poll_cb(uv_poll_t *w, int status, int events) {
    kml_ares_task *task = (kml_ares_task *)w->data;
    kml_ares_chan *c = NULL;
    for (int i = 0; i < KML_ARES_MAXCHAN && !c; i++)
        for (kml_ares_task *t = ares_chans[i].tasks; t; t = t->next)
            if (t == task) { c = &ares_chans[i]; break; }
    if (!c) return;
    // Reset the idle timer.
    uv_timer_again(c->timer);
    if (status < 0) {
        // An error happened: pretend the socket is both readable and writable.
        ares_process_fd(c->channel, task->sock, task->sock);
        return;
    }
    ares_process_fd(c->channel,
                    events & UV_READABLE ? task->sock : ARES_SOCKET_BAD,
                    events & UV_WRITABLE ? task->sock : ARES_SOCKET_BAD);
}

// c-ares starts, changes or stops wanting a socket.
static void kml_ares_sockstate_cb(void *data, ares_socket_t sock, int read, int write) {
    kml_ares_chan *c = (kml_ares_chan *)data;
    kml_ares_task **pp = &c->tasks, *task = NULL;
    for (; *pp; pp = &(*pp)->next)
        if ((*pp)->sock == sock) { task = *pp; break; }
    if (read || write) {
        if (!task) {
            // A new socket.
            kml_ares_start_timer(c);
            task = (kml_ares_task *)calloc(1, sizeof *task);
            task->sock = sock;
            task->poll = (uv_poll_t *)__kml_loop_raw_alloc(sizeof *task->poll);
#ifndef _WIN32
            __kml_loop_fd_forget((int)sock);
#endif
            if (uv_poll_init_socket(c->loop, task->poll, sock) < 0) {
                // The socket won't be polled; the query will time out.
                __kml_loop_free_handle((uv_handle_t *)task->poll);
                free(task);
                return;
            }
            task->poll->data = task;
            task->next = c->tasks;
            c->tasks = task;
        }
        uv_poll_start(task->poll, (read ? UV_READABLE : 0) | (write ? UV_WRITABLE : 0), kml_ares_poll_cb);
    } else if (task) {
        *pp = task->next;
        uv_close((uv_handle_t *)task->poll, ares_close_free);
        free(task);
        if (c->tasks == NULL) kml_ares_close_timer(c);
    }
}

#endif

// ChannelWrap::Setup.
static int kml_ares_setup(kml_ares_chan *c) {
    struct ares_options options;
    memset(&options, 0, sizeof options);
    options.flags = ARES_FLAG_NOCHECKRESP;
#ifndef _WIN32
    options.sock_state_cb = kml_ares_sockstate_cb;
    options.sock_state_cb_data = c;
#else
    options.evsys = ARES_EVSYS_DEFAULT;
#endif
    options.timeout = c->timeout;
    options.tries = c->tries;
    options.qcache_max_ttl = 0;
    int r;
    if (!c->library_inited) {
        pthread_mutex_lock(&ares_library_mutex);
        // Each call increments a reference count.
        r = ares_library_init(ARES_LIB_INIT_ALL);
        pthread_mutex_unlock(&ares_library_mutex);
        if (r != ARES_SUCCESS) return r;
    }
#ifndef _WIN32
    int optmask = ARES_OPT_FLAGS | ARES_OPT_TIMEOUTMS | ARES_OPT_SOCK_STATE_CB |
                  ARES_OPT_TRIES | ARES_OPT_QUERY_CACHE;
#else
    int optmask = ARES_OPT_FLAGS | ARES_OPT_TIMEOUTMS | ARES_OPT_EVENT_THREAD |
                  ARES_OPT_TRIES | ARES_OPT_QUERY_CACHE;
#endif
    if (c->max_timeout > 0) {
        options.maxtimeout = c->max_timeout;
        optmask |= ARES_OPT_MAXTIMEOUTMS;
    }
    r = ares_init_options(&c->channel, &options, optmask);
    if (r != ARES_SUCCESS) {
        pthread_mutex_lock(&ares_library_mutex);
        ares_library_cleanup();
        pthread_mutex_unlock(&ares_library_mutex);
        return r;
    }
    c->library_inited = 1;
    return ARES_SUCCESS;
}

// ChannelWrap::EnsureServers: a channel left with c-ares' fallback server
// (the loopback address alone) after a failed query is set up again, to
// pick up a resolver configuration that has since appeared.
static void kml_ares_ensure_servers(kml_ares_chan *c) {
    if (__atomic_load_n(&c->query_last_ok, __ATOMIC_RELAXED) || !c->is_servers_default) return;
    struct ares_addr_port_node *servers = NULL;
    ares_get_servers_ports(c->channel, &servers);
    if (servers == NULL) return;
    if (servers->next != NULL) {
        ares_free_data(servers);
        c->is_servers_default = 0;
        return;
    }
    int is_loopback = 0;
    if (servers->family == AF_INET) {
        is_loopback = servers->addr.addr4.s_addr == htonl(INADDR_LOOPBACK);
    } else if (servers->family == AF_INET6) {
        static const unsigned char loop6[16] = {0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1};
        is_loopback = memcmp(&servers->addr.addr6, loop6, sizeof loop6) == 0;
    }
    ares_free_data(servers);
    if (!is_loopback) {
        c->is_servers_default = 0;
        return;
    }
    ares_destroy(c->channel);
#ifndef _WIN32
    kml_ares_close_timer(c);
#endif
    kml_ares_setup(c);
}

static kml_ares_chan *ares_chan_get(double id) {
    int i = (int)id;
    return (i >= 0 && i < KML_ARES_MAXCHAN && ares_chans[i].used) ? &ares_chans[i] : NULL;
}

// new ChannelWrap(timeout, tries, maxTimeout): the channel's id, or a
// negated c-ares status when setup fails.
double __kml_native_dns_channel_new(double timeout, double tries, double max_timeout) {
    int id = -1;
    for (int i = 0; i < KML_ARES_MAXCHAN; i++)
        if (!ares_chans[i].used) { id = i; break; }
    if (id < 0) return -ARES_ENOMEM;
    kml_ares_chan *c = &ares_chans[id];
    memset(c, 0, sizeof *c);
#ifndef _WIN32
    c->loop = __kml_loop_uv();
#endif
    c->timeout = (int)timeout;
    c->tries = (int)tries;
    c->max_timeout = (int)max_timeout;
    c->query_last_ok = 1;
    c->is_servers_default = 1;
    int r = kml_ares_setup(c);
    if (r != ARES_SUCCESS) return -r;
    c->used = 1;
    return id;
}

static char *kml_ares_str(kml_jbuf *b) {
    if (!b->s) jb_cstr(b, "");
    int64_t n = (int64_t)b->n;
    char *out = __kml_str_alloc(n + 1);
    memcpy(out, b->s, (size_t)n + 1);
    __kml_str_finalize(out);
    free(b->s);
    return out;
}

// GetServers: [[address, udpPort], …].
char *__kml_native_dns_channel_servers(double id) {
    kml_jbuf b = {0};
    jb_cstr(&b, "[");
    kml_ares_chan *c = ares_chan_get(id);
    struct ares_addr_port_node *servers = NULL;
    if (c && ares_get_servers_ports(c->channel, &servers) == ARES_SUCCESS) {
        int i = 0;
        for (struct ares_addr_port_node *cur = servers; cur; cur = cur->next, i++) {
            char ip[INET6_ADDRSTRLEN];
            ares_inet_ntop(cur->family, &cur->addr, ip, sizeof ip);
            if (i) jb_cstr(&b, ",");
            jb_cstr(&b, "[");
            jb_json(&b, ip, strlen(ip));
            jb_cstr(&b, ",");
            jb_int(&b, cur->udp_port);
            jb_cstr(&b, "]");
        }
        ares_free_data(servers);
    }
    jb_cstr(&b, "]");
    return kml_ares_str(&b);
}

// SetServers: spec holds one "family address port" line per server. The
// c-ares status, or DNS_ESETSRVPENDING (-1000) while queries are in flight.
double __kml_native_dns_channel_set_servers(double id, const char *spec) {
    kml_ares_chan *c = ares_chan_get(id);
    if (!c) return ARES_EBADSTR;
    if (__atomic_load_n(&c->active_query_count, __ATOMIC_RELAXED)) return -1000;
    const char *p = spec ? spec : "";
    int len = 0;
    for (const char *q = p; *q; q++) if (*q == '\n') len++;
    if (*p && p[strlen(p) - 1] != '\n') len++;
    if (len == 0) return ares_set_servers(c->channel, NULL);
    struct ares_addr_port_node *servers = (struct ares_addr_port_node *)calloc((size_t)len, sizeof *servers);
    struct ares_addr_port_node *last = NULL;
    int err = 0;
    for (int i = 0; i < len && *p; i++) {
        char line[160], ip[128];
        size_t n = strcspn(p, "\n");
        if (n >= sizeof line) n = sizeof line - 1;
        memcpy(line, p, n);
        line[n] = 0;
        p += strcspn(p, "\n");
        if (*p == '\n') p++;
        int fam = 0, port = 0;
        ip[0] = 0;
        sscanf(line, "%d %127s %d", &fam, ip, &port);
        struct ares_addr_port_node *cur = &servers[i];
        cur->tcp_port = cur->udp_port = port;
        if (fam == 4) {
            cur->family = AF_INET;
            err = ares_inet_pton(AF_INET, ip, &cur->addr) == 1 ? 0 : 1;
        } else {
            cur->family = AF_INET6;
            err = ares_inet_pton(AF_INET6, ip, &cur->addr) == 1 ? 0 : 1;
        }
        if (err) break;
        cur->next = NULL;
        if (last) last->next = cur;
        last = cur;
    }
    if (err == 0) err = ares_set_servers_ports(c->channel, servers);
    else err = ARES_EBADSTR;
    free(servers);
    if (err == ARES_SUCCESS) c->is_servers_default = 0;
    return err;
}

static uint32_t kml_be32(const unsigned char *p) {
    return ((uint32_t)p[0] << 24) | ((uint32_t)p[1] << 16) | ((uint32_t)p[2] << 8) | p[3];
}

// SetLocalAddress(first, second): 0, or 1 an invalid address, 2 two IPv4
// addresses, 3 two IPv6 addresses (each thrown as ERR_INVALID_ARG_VALUE).
double __kml_native_dns_channel_set_local(double id, const char *ip0, const char *ip1) {
    kml_ares_chan *c = ares_chan_get(id);
    if (!c) return 1;
    unsigned char addr0[16], addr1[16];
    int type0;
    if (ares_inet_pton(AF_INET, ip0, addr0) == 1) {
        ares_set_local_ip4(c->channel, kml_be32(addr0));
        type0 = 4;
    } else if (ares_inet_pton(AF_INET6, ip0, addr0) == 1) {
        ares_set_local_ip6(c->channel, addr0);
        type0 = 6;
    } else {
        return 1;
    }
    if (ip1 && *ip1) {
        if (ares_inet_pton(AF_INET, ip1, addr1) == 1) {
            if (type0 == 4) return 2;
            ares_set_local_ip4(c->channel, kml_be32(addr1));
        } else if (ares_inet_pton(AF_INET6, ip1, addr1) == 1) {
            if (type0 == 6) return 3;
            ares_set_local_ip6(c->channel, addr1);
        } else {
            return 1;
        }
    } else if (type0 == 4) {
        memset(addr1, 0, sizeof addr1);
        ares_set_local_ip6(c->channel, addr1);
    } else {
        ares_set_local_ip4(c->channel, 0);
    }
    return 0;
}

// StrError: cares_wrap's message for a status.
char *__kml_native_dns_strerror(double code) {
    kml_jbuf b = {0};
    jb_cstr(&b, (int)code == -1000 ? "There are pending queries." : ares_strerror((int)code));
    return kml_ares_str(&b);
}

// Cancel.
void __kml_native_dns_channel_cancel(double id) {
    kml_ares_chan *c = ares_chan_get(id);
    if (c) ares_cancel(c->channel);
}

// ---- reply parsers (cares_wrap.cc) ----

static void jb_ip(kml_jbuf *b, int family, const void *addr) {
    char ip[INET6_ADDRSTRLEN];
    ares_inet_ntop(family, addr, ip, sizeof ip);
    jb_json(b, ip, strlen(ip));
}
static void jb_str(kml_jbuf *b, const char *s) { jb_json(b, s, strlen(s)); }
static void jb_name(kml_jbuf *b, const char *key, const char *v) {
    jb_cstr(b, ",\"");
    jb_cstr(b, key);
    jb_cstr(b, "\":");
    jb_str(b, v);
}
static void jb_num(kml_jbuf *b, const char *key, long long v) {
    jb_cstr(b, ",\"");
    jb_cstr(b, key);
    jb_cstr(b, "\":");
    jb_int(b, v);
}
static void jb_sep(kml_jbuf *b, int *count) { if ((*count)++) jb_cstr(b, ","); }

static int ttl_buffer_len(const unsigned char *buf, int len) {
    if (len <= 7) return 256;
    int n = (buf[6] << 8) | buf[7];
    return n == 0 ? 1 : n;
}

// ParseGeneralReply: *type is A, AAAA, CNAME, cname-or-A, NS or PTR; the
// values are appended to b's array (count tracks its length). Returns the
// c-ares status.
static int parse_general(const unsigned char *buf, int len, int *type, kml_jbuf *b, int *count,
                         void *addrttls, int *naddrttls) {
    struct hostent *host = NULL;
    int status;
    switch (*type) {
    case ns_t_a:
    case ns_t_cname:
    case ns_t_cname_or_a:
        status = ares_parse_a_reply(buf, len, &host, (struct ares_addrttl *)addrttls, naddrttls);
        break;
    case ns_t_aaaa:
        status = ares_parse_aaaa_reply(buf, len, &host, (struct ares_addr6ttl *)addrttls, naddrttls);
        break;
    case ns_t_ns:
        status = ares_parse_ns_reply(buf, len, &host);
        break;
    default:
        status = ares_parse_ptr_reply(buf, len, NULL, 0, AF_INET, &host);
        break;
    }
    if (status != ARES_SUCCESS) return status;
    if ((*type == ns_t_cname_or_a && host->h_name && host->h_aliases[0]) || *type == ns_t_cname) {
        *type = ns_t_cname;
        jb_sep(b, count);
        jb_str(b, host->h_name);
        ares_free_hostent(host);
        return ARES_SUCCESS;
    }
    if (*type == ns_t_cname_or_a) *type = ns_t_a;
    if (*type == ns_t_ns || *type == ns_t_ptr) {
        for (int i = 0; host->h_aliases[i]; i++) {
            jb_sep(b, count);
            jb_str(b, host->h_aliases[i]);
        }
    } else {
        for (int i = 0; host->h_addr_list[i]; i++) {
            jb_sep(b, count);
            jb_ip(b, host->h_addrtype, host->h_addr_list[i]);
        }
    }
    ares_free_hostent(host);
    return ARES_SUCCESS;
}

static int parse_mx(const unsigned char *buf, int len, kml_jbuf *b, int *count) {
    struct ares_mx_reply *start;
    int status = ares_parse_mx_reply(buf, len, &start);
    if (status != ARES_SUCCESS) return status;
    for (struct ares_mx_reply *cur = start; cur; cur = cur->next) {
        jb_sep(b, count);
        jb_cstr(b, "{\"exchange\":");
        jb_str(b, cur->host);
        jb_num(b, "priority", cur->priority);
        jb_cstr(b, ",\"type\":\"MX\"}");
    }
    ares_free_data(start);
    return ARES_SUCCESS;
}

static int parse_caa(const unsigned char *buf, int len, kml_jbuf *b, int *count) {
    struct ares_caa_reply *start;
    int status = ares_parse_caa_reply(buf, len, &start);
    if (status != ARES_SUCCESS) return status;
    for (struct ares_caa_reply *cur = start; cur; cur = cur->next) {
        jb_sep(b, count);
        jb_cstr(b, "{\"critical\":");
        jb_int(b, cur->critical);
        jb_cstr(b, ",\"type\":\"CAA\",");
        jb_str(b, (const char *)cur->property);
        jb_cstr(b, ":");
        jb_str(b, (const char *)cur->value);
        jb_cstr(b, "}");
    }
    ares_free_data(start);
    return ARES_SUCCESS;
}

// The answer section's records of rr_type, each handed to fn at its RDATA:
// the walk ParseSoaReply does, shared with TLSA (whose c-ares parser is
// newer than the oldest supported c-ares).
static int walk_answers(const unsigned char *buf, int len, int rr_type,
                        int (*fn)(const unsigned char *buf, int len, const unsigned char *rd, int rdlen, void *ctx),
                        void *ctx) {
    if (len < NS_HFIXEDSZ) return ARES_EBADRESP;
    const unsigned int ancount = (buf[6] << 8) | buf[7];
    const unsigned char *ptr = buf + NS_HFIXEDSZ;
    char *name = NULL;
    long tlen;
    int status = ares_expand_name(ptr, buf, len, &name, &tlen);
    if (status != ARES_SUCCESS) return status == ARES_EBADNAME ? ARES_EBADRESP : status;
    ares_free_string(name);
    if (ptr + tlen + NS_QFIXEDSZ > buf + len) return ARES_EBADRESP;
    ptr += tlen + NS_QFIXEDSZ;
    for (unsigned int i = 0; i < ancount; i++) {
        char *rr_name = NULL;
        long rr_len_name;
        int s2 = ares_expand_name(ptr, buf, len, &rr_name, &rr_len_name);
        if (s2 != ARES_SUCCESS) return s2 == ARES_EBADNAME ? ARES_EBADRESP : s2;
        ares_free_string(rr_name);
        ptr += rr_len_name;
        if (ptr + NS_RRFIXEDSZ > buf + len) return ARES_EBADRESP;
        const int type = (ptr[0] << 8) | ptr[1];
        const int rr_len = (ptr[8] << 8) | ptr[9];
        ptr += NS_RRFIXEDSZ;
        if (type == rr_type) {
            int r = fn(buf, len, ptr, rr_len, ctx);
            if (r < 0) break;
            if (r > 0) return r;
        }
        ptr += rr_len;
    }
    return ARES_SUCCESS;
}

typedef struct { kml_jbuf *b; int *count; int with_type; int found; } soa_ctx;

// One SOA record; ParseSoaReply stops at the first (returns -1).
static int soa_record(const unsigned char *buf, int len, const unsigned char *ptr, int rdlen, void *vctx) {
    (void)rdlen;
    soa_ctx *ctx = (soa_ctx *)vctx;
    char *nsname = NULL, *hostmaster = NULL;
    long l1, l2;
    int s = ares_expand_name(ptr, buf, len, &nsname, &l1);
    if (s != ARES_SUCCESS) return s == ARES_EBADNAME ? ARES_EBADRESP : s;
    ptr += l1;
    s = ares_expand_name(ptr, buf, len, &hostmaster, &l2);
    if (s != ARES_SUCCESS) {
        ares_free_string(nsname);
        return s == ARES_EBADNAME ? ARES_EBADRESP : s;
    }
    ptr += l2;
    if (ptr + 5 * 4 > buf + len) {
        ares_free_string(nsname);
        ares_free_string(hostmaster);
        return ARES_EBADRESP;
    }
    jb_sep(ctx->b, ctx->count);
    jb_cstr(ctx->b, "{\"nsname\":");
    jb_str(ctx->b, nsname);
    jb_name(ctx->b, "hostmaster", hostmaster);
    jb_num(ctx->b, "serial", kml_be32(ptr));
    jb_num(ctx->b, "refresh", (int32_t)kml_be32(ptr + 4));
    jb_num(ctx->b, "retry", (int32_t)kml_be32(ptr + 8));
    jb_num(ctx->b, "expire", (int32_t)kml_be32(ptr + 12));
    jb_num(ctx->b, "minttl", kml_be32(ptr + 16));
    jb_cstr(ctx->b, ctx->with_type ? ",\"type\":\"SOA\"}" : "}");
    ares_free_string(nsname);
    ares_free_string(hostmaster);
    ctx->found = 1;
    return -1;
}

typedef struct { kml_jbuf *b; int *count; } tlsa_ctx;

// One TLSA record; data as hex, which the TS side turns into an ArrayBuffer.
static int tlsa_record(const unsigned char *buf, int len, const unsigned char *rd, int rdlen, void *vctx) {
    (void)buf;
    (void)len;
    tlsa_ctx *ctx = (tlsa_ctx *)vctx;
    if (rdlen <= 3) return 0; // no data: skipped
    jb_sep(ctx->b, ctx->count);
    jb_cstr(ctx->b, "{\"certUsage\":");
    jb_int(ctx->b, rd[0]);
    jb_num(ctx->b, "selector", rd[1]);
    jb_num(ctx->b, "match", rd[2]);
    jb_cstr(ctx->b, ",\"data\":{\"hex\":\"");
    static const char hx[] = "0123456789abcdef";
    for (int i = 3; i < rdlen; i++) {
        char two[2] = {hx[rd[i] >> 4], hx[rd[i] & 15]};
        jb_put(ctx->b, two, 2);
    }
    jb_cstr(ctx->b, "\"}}");
    return 0;
}

static int parse_tlsa(const unsigned char *buf, int len, kml_jbuf *b, int *count) {
    tlsa_ctx ctx = {b, count};
    return walk_answers(buf, len, 52, tlsa_record, &ctx);
}

typedef struct { kml_jbuf *b; int *count; int need_type; int found; } txt_ctx;

// One TXT record: its character-strings as one entry's chunks.
static int txt_record(const unsigned char *buf, int len, const unsigned char *rd, int rdlen, void *vctx) {
    (void)buf;
    (void)len;
    txt_ctx *ctx = (txt_ctx *)vctx;
    int chunks = 0;
    jb_sep(ctx->b, ctx->count);
    jb_cstr(ctx->b, ctx->need_type ? "{\"entries\":[" : "[");
    for (int off = 0; off < rdlen;) {
        int n = rd[off];
        if (off + 1 + n > rdlen) return ARES_EBADRESP;
        jb_sep(ctx->b, &chunks);
        jb_json(ctx->b, (const char *)rd + off + 1, (size_t)n);
        off += 1 + n;
    }
    jb_cstr(ctx->b, ctx->need_type ? "],\"type\":\"TXT\"}" : "]");
    ctx->found = 1;
    return 0;
}

// ParseTxtReply (ares_parse_txt_reply_ext's records, each string a chunk,
// as the c-ares Node bundles returns them on every c-ares this links).
static int parse_txt(const unsigned char *buf, int len, kml_jbuf *b, int *count, int need_type) {
    txt_ctx ctx = {b, count, need_type, 0};
    int status = walk_answers(buf, len, ns_t_txt, txt_record, &ctx);
    if (status != ARES_SUCCESS) return status;
    return ctx.found ? ARES_SUCCESS : ARES_ENODATA;
}

static int parse_srv(const unsigned char *buf, int len, kml_jbuf *b, int *count) {
    struct ares_srv_reply *start;
    int status = ares_parse_srv_reply(buf, len, &start);
    if (status != ARES_SUCCESS) return status;
    for (struct ares_srv_reply *cur = start; cur; cur = cur->next) {
        jb_sep(b, count);
        jb_cstr(b, "{\"name\":");
        jb_str(b, cur->host);
        jb_num(b, "port", cur->port);
        jb_num(b, "priority", cur->priority);
        jb_num(b, "weight", cur->weight);
        jb_cstr(b, ",\"type\":\"SRV\"}");
    }
    ares_free_data(start);
    return ARES_SUCCESS;
}

static int parse_naptr(const unsigned char *buf, int len, kml_jbuf *b, int *count, int need_type) {
    struct ares_naptr_reply *start;
    int status = ares_parse_naptr_reply(buf, len, &start);
    if (status != ARES_SUCCESS) return status;
    for (struct ares_naptr_reply *cur = start; cur; cur = cur->next) {
        jb_sep(b, count);
        jb_cstr(b, "{\"flags\":");
        jb_str(b, (const char *)cur->flags);
        jb_name(b, "service", (const char *)cur->service);
        jb_name(b, "regexp", (const char *)cur->regexp);
        jb_name(b, "replacement", cur->replacement);
        jb_num(b, "order", cur->order);
        jb_num(b, "preference", cur->preference);
        jb_cstr(b, need_type ? ",\"type\":\"NAPTR\"}" : "}");
    }
    ares_free_data(start);
    return ARES_SUCCESS;
}

// AnyTraits::Parse.
static int parse_any(const unsigned char *buf, int len, kml_jbuf *b) {
    int count = 0, status;
    jb_cstr(b, "[");
    // A records, or the CNAME (ParseGeneralReply with ns_t_cname_or_a).
    int n = ttl_buffer_len(buf, len);
    struct ares_addrttl *addrttls = (struct ares_addrttl *)calloc((size_t)n, sizeof *addrttls);
    int naddrttls = n;
    struct hostent *host = NULL;
    status = ares_parse_a_reply(buf, len, &host, addrttls, &naddrttls);
    if (status != ARES_SUCCESS && status != ARES_ENODATA) { free(addrttls); return status; }
    if (status == ARES_SUCCESS) {
        if (host->h_name && host->h_aliases[0]) {
            jb_sep(b, &count);
            jb_cstr(b, "{\"value\":");
            jb_str(b, host->h_name);
            jb_cstr(b, ",\"type\":\"CNAME\"}");
        } else {
            for (int i = 0; host->h_addr_list[i]; i++) {
                jb_sep(b, &count);
                jb_cstr(b, "{\"address\":");
                jb_ip(b, host->h_addrtype, host->h_addr_list[i]);
                jb_num(b, "ttl", (uint32_t)addrttls[i].ttl);
                jb_cstr(b, ",\"type\":\"A\"}");
            }
        }
        ares_free_hostent(host);
    }
    free(addrttls);
    // AAAA records.
    n = ttl_buffer_len(buf, len);
    struct ares_addr6ttl *addr6ttls = (struct ares_addr6ttl *)calloc((size_t)n, sizeof *addr6ttls);
    int naddr6ttls = n;
    host = NULL;
    status = ares_parse_aaaa_reply(buf, len, &host, addr6ttls, &naddr6ttls);
    if (status != ARES_SUCCESS && status != ARES_ENODATA) { free(addr6ttls); return status; }
    if (status == ARES_SUCCESS) {
        for (int i = 0; host->h_addr_list[i]; i++) {
            jb_sep(b, &count);
            jb_cstr(b, "{\"address\":");
            jb_ip(b, host->h_addrtype, host->h_addr_list[i]);
            jb_num(b, "ttl", (uint32_t)addr6ttls[i].ttl);
            jb_cstr(b, ",\"type\":\"AAAA\"}");
        }
        ares_free_hostent(host);
    }
    free(addr6ttls);
    // MX records.
    status = parse_mx(buf, len, b, &count);
    if (status != ARES_SUCCESS && status != ARES_ENODATA) return status;
    // NS records.
    host = NULL;
    status = ares_parse_ns_reply(buf, len, &host);
    if (status != ARES_SUCCESS && status != ARES_ENODATA) return status;
    if (status == ARES_SUCCESS) {
        for (int i = 0; host->h_aliases[i]; i++) {
            jb_sep(b, &count);
            jb_cstr(b, "{\"value\":");
            jb_str(b, host->h_aliases[i]);
            jb_cstr(b, ",\"type\":\"NS\"}");
        }
        ares_free_hostent(host);
    }
    // TXT records.
    status = parse_txt(buf, len, b, &count, 1);
    if (status != ARES_SUCCESS && status != ARES_ENODATA) return status;
    // SRV records.
    status = parse_srv(buf, len, b, &count);
    if (status != ARES_SUCCESS && status != ARES_ENODATA) return status;
    // PTR records.
    host = NULL;
    status = ares_parse_ptr_reply(buf, len, NULL, 0, AF_INET, &host);
    if (status != ARES_SUCCESS && status != ARES_ENODATA) return status;
    if (status == ARES_SUCCESS) {
        for (int i = 0; host->h_aliases[i]; i++) {
            jb_sep(b, &count);
            jb_cstr(b, "{\"value\":");
            jb_str(b, host->h_aliases[i]);
            jb_cstr(b, ",\"type\":\"PTR\"}");
        }
        ares_free_hostent(host);
    }
    // NAPTR records.
    status = parse_naptr(buf, len, b, &count, 1);
    if (status != ARES_SUCCESS && status != ARES_ENODATA) return status;
    // SOA record.
    soa_ctx sctx = {b, &count, 1, 0};
    status = walk_answers(buf, len, ns_t_soa, soa_record, &sctx);
    if (status != ARES_SUCCESS && status != ARES_ENODATA) return status;
    // TLSA records.
    status = parse_tlsa(buf, len, b, &count);
    if (status != ARES_SUCCESS && status != ARES_ENODATA) return status;
    // CAA records.
    status = parse_caa(buf, len, b, &count);
    if (status != ARES_SUCCESS && status != ARES_ENODATA) return status;
    jb_cstr(b, "]");
    return ARES_SUCCESS;
}

// One record type's Parse into b: {"r":…} or {"r":…,"x":ttls}.
static int parse_reply(int qtype, const unsigned char *buf, int len, kml_jbuf *b) {
    int count = 0, status;
    if (qtype == 255) {
        jb_cstr(b, "{\"r\":");
        status = parse_any(buf, len, b);
        jb_cstr(b, "}");
        return status;
    }
    if (qtype == ns_t_soa) {
        struct ares_soa_reply *soa;
        status = ares_parse_soa_reply(buf, len, &soa);
        if (status != ARES_SUCCESS) return status;
        jb_cstr(b, "{\"r\":{\"nsname\":");
        jb_str(b, soa->nsname);
        jb_name(b, "hostmaster", soa->hostmaster);
        jb_num(b, "serial", (uint32_t)soa->serial);
        jb_num(b, "refresh", (int32_t)soa->refresh);
        jb_num(b, "retry", (int32_t)soa->retry);
        jb_num(b, "expire", (int32_t)soa->expire);
        jb_num(b, "minttl", (uint32_t)soa->minttl);
        jb_cstr(b, "}}");
        ares_free_data(soa);
        return ARES_SUCCESS;
    }
    jb_cstr(b, "{\"r\":[");
    if (qtype == ns_t_a || qtype == ns_t_aaaa) {
        int n = ttl_buffer_len(buf, len);
        size_t sz = qtype == ns_t_a ? sizeof(struct ares_addrttl) : sizeof(struct ares_addr6ttl);
        void *ttls = calloc((size_t)n, sz);
        int nttls = n, type = qtype;
        status = parse_general(buf, len, &type, b, &count, ttls, &nttls);
        if (status == ARES_SUCCESS) {
            jb_cstr(b, "],\"x\":[");
            for (int i = 0; i < nttls; i++) {
                if (i) jb_cstr(b, ",");
                jb_int(b, (uint32_t)(qtype == ns_t_a ? ((struct ares_addrttl *)ttls)[i].ttl
                                                     : ((struct ares_addr6ttl *)ttls)[i].ttl));
            }
        }
        free(ttls);
    } else if (qtype == ns_t_cname || qtype == ns_t_ns || qtype == ns_t_ptr) {
        int type = qtype;
        status = parse_general(buf, len, &type, b, &count, NULL, NULL);
    } else if (qtype == ns_t_mx) {
        status = parse_mx(buf, len, b, &count);
    } else if (qtype == 257) {
        status = parse_caa(buf, len, b, &count);
    } else if (qtype == 52) {
        status = parse_tlsa(buf, len, b, &count);
    } else if (qtype == ns_t_txt) {
        status = parse_txt(buf, len, b, &count, 0);
    } else if (qtype == ns_t_srv) {
        status = parse_srv(buf, len, b, &count);
    } else {
        status = parse_naptr(buf, len, b, &count, 0);
    }
    jb_cstr(b, "]}");
    return status;
}

// ---- queries ----

typedef struct {
    kml_ares_chan *chan;
    int qtype; // 0: getHostByAddr
    void *item; // the pool completion that calls back
} kml_ares_query;

// QueueResponseCallback: the answer reaches the callback on a later turn of
// the loop, as cares_wrap's SetImmediate delivers it. On Windows this runs
// on c-ares' event thread.
static void kml_ares_complete(kml_ares_query *q, int status, char *json) {
    __atomic_store_n(&q->chan->query_last_ok, status != ARES_ECONNREFUSED, __ATOMIC_RELAXED);
    __atomic_fetch_sub(&q->chan->active_query_count, 1, __ATOMIC_RELAXED);
    __kml_pool_finish(q->item, status, json);
    free(q);
}

static void kml_ares_query_cb(void *arg, int status, int timeouts, unsigned char *abuf, int alen) {
    (void)timeouts;
    kml_ares_query *q = (kml_ares_query *)arg;
    char *json = NULL;
    if (status == ARES_SUCCESS) {
        kml_jbuf b = {0};
        status = parse_reply(q->qtype, abuf, alen, &b);
        if (status == ARES_SUCCESS) json = b.s;
        else free(b.s);
    }
    kml_ares_complete(q, status, json);
}

static void kml_ares_host_cb(void *arg, int status, int timeouts, struct hostent *host) {
    (void)timeouts;
    kml_ares_query *q = (kml_ares_query *)arg;
    char *json = NULL;
    if (status == ARES_SUCCESS) {
        kml_jbuf b = {0};
        int count = 0;
        jb_cstr(&b, "{\"r\":[");
        for (int i = 0; host->h_aliases[i]; i++) {
            jb_sep(&b, &count);
            jb_str(&b, host->h_aliases[i]);
        }
        jb_cstr(&b, "]}");
        json = b.s;
    }
    kml_ares_complete(q, status, json);
}

// Query: the binding's record type (0 for getHostByAddr) for name, already
// in ASCII form. Returns 0, or -1 for getHostByAddr of a non-address (the
// caller throws UV_EINVAL); the callback gets (status, 0) with the result in
// nativeLastString().
double __kml_native_dns_query(double channel, double type, const char *name, void *inv, void *clo) {
    kml_ares_chan *c = ares_chan_get(channel);
    int qtype = (int)type;
    if (qtype == 0) {
        unsigned char addr[16];
        int length, family;
        if (ares_inet_pton(AF_INET, name, addr) == 1) { length = 4; family = AF_INET; }
        else if (ares_inet_pton(AF_INET6, name, addr) == 1) { length = 16; family = AF_INET6; }
        else return -1;
        kml_ares_query *q = (kml_ares_query *)calloc(1, sizeof *q);
        q->chan = c; q->qtype = 0;
        kml_ares_ensure_servers(c);
        __atomic_fetch_add(&c->active_query_count, 1, __ATOMIC_RELAXED);
        q->item = __kml_pool_begin(inv, clo);
        ares_gethostbyaddr(c->channel, addr, length, family, kml_ares_host_cb, q);
        return 0;
    }
    kml_ares_query *q = (kml_ares_query *)calloc(1, sizeof *q);
    q->chan = c; q->qtype = qtype;
    kml_ares_ensure_servers(c);
    __atomic_fetch_add(&c->active_query_count, 1, __ATOMIC_RELAXED);
    q->item = __kml_pool_begin(inv, clo);
    ares_query(c->channel, name, ns_c_in, qtype, kml_ares_query_cb, q);
    return 0;
}
