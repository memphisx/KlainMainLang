/* net.c — the net socket the HTTP server hands out on 'upgrade',
 * 'connection' and 'clientError' (in C per TDD-00240; Node's `net` module
 * itself is lib/node/net.ts): socket.on('data'|'end'|'close').
 *
 * Every open socket's fd is non-blocking and folded into the central
 * select() event loop like the child_process read pipes: fdset_add adds each
 * one's fd (read-interest), dispatch drains the readable ones after
 * select(), and keepalive holds the loop open while one is open. The sockets
 * live in one process-wide registry; each socket's listeners are raw closure
 * headers the dispatch invokes directly, the same posture as child_process.
 * kml_layout.h is prepended by the compiler. */
#include <stdlib.h>
#include <string.h>

typedef long long i64;

#define F(p, off, T) (*(T *)((char *)(p) + (off)))

/* Declared as the emitted IR declares them; no system header, so the
 * prototypes cannot clash with libc's. */
#pragma clang diagnostic ignored "-Wincompatible-library-redeclaration"
extern i64 read(int, void *, i64);
extern int close(int);
extern int getsockname(int, void *, void *);
extern const char *inet_ntop(int, const void *, char *, int);

extern i64 __kml_tls_read(void *ssl, void *buf, i64 n);
extern void __kml_tls_free(void *ssl);
extern void *__kml_str_alloc(i64 n);
extern void *__kml_str_from_cstr(const char *c);
extern void __kml_worker_fd_setbit(int fd, void *fdset, void *maxfd);

typedef void (*fire_fn)(void *env);
typedef void (*data_fn)(void *env, void *buf, i64 n);

static void **conn_data;
static i64 conn_len, conn_cap;

/* Append a connection to the process-wide registry (realloc-doubling, same
 * shape as __kml_cp_register). */
void __kml_net_conn_register(void *c) {
    if (conn_len >= conn_cap) {
        i64 nc = conn_cap * 2 > 4 ? conn_cap * 2 : 4;
        conn_data = (void **)realloc(conn_data, (size_t)nc * sizeof(void *));
        conn_cap = nc;
    }
    conn_data[conn_len++] = c;
}

/* True while any connection is still open. */
_Bool __kml_net_keepalive(void) {
    for (i64 i = 0; i < conn_len; i++)
        if (F(conn_data[i], KML_NETSOCK_STATE, i64) == 0) return 1;
    return 0;
}

/* Add every open connection fd to the read set. Never forces a zero
 * timeout. */
_Bool __kml_net_fdset_add(void *fdset, void *maxfd) {
    for (i64 i = 0; i < conn_len; i++) {
        i64 fd = F(conn_data[i], KML_NETSOCK_FD, i64);
        if (fd >= 0) __kml_worker_fd_setbit((int)fd, fdset, maxfd);
    }
    return 0;
}

/* Fire a listener closure header {fn, env} taking no argument. */
static void fire(void *cl) { F(cl, KML_CLOSURE_FN, fire_fn)(F(cl, KML_CLOSURE_ENV, void *)); }

/* Drain every open connection socket: each socket's 'data' listener gets a
 * fresh Buffer, 'end' then 'close' fire on EOF. */
void __kml_net_dispatch(void) {
    char chunk[4096];
    i64 len = conn_len; /* a listener may register more: they wait a turn */
    for (i64 i = 0; i < len; i++) {
        void *sk = conn_data[i]; /* re-read: a listener's register may move the array */
        for (;;) {
            i64 fd = F(sk, KML_NETSOCK_FD, i64);
            if (fd < 0) break;
            void *ssl = F(sk, KML_NETSOCK_SSL, void *);
            i64 n = ssl ? __kml_tls_read(ssl, chunk, 4096) : read((int)fd, chunk, 4096);
            if (n > 0) {
                void *dl = F(sk, KML_NETSOCK_DATA, void *);
                if (dl) {
                    /* TDD-00120: length-prefixed chunk. A (chunk: string)
                     * listener binds this pointer directly, so it carries
                     * the 8-byte length header (ptr-8) like every other
                     * string, and stays NUL-terminated for strlen consumers.
                     * The listener is still called with length n, so Buffer
                     * consumers are unaffected. */
                    char *buf = (char *)__kml_str_alloc(n);
                    memcpy(buf, chunk, (size_t)n);
                    buf[n] = 0;
                    F(dl, KML_CLOSURE_FN, data_fn)(F(dl, KML_CLOSURE_ENV, void *), buf, n);
                }
                continue;
            }
            if (n == 0) {
                __kml_tls_free(F(sk, KML_NETSOCK_SSL, void *));
                close((int)fd);
                F(sk, KML_NETSOCK_FD, i64) = -1;
                F(sk, KML_NETSOCK_STATE, i64) = 1;
                void *el = F(sk, KML_NETSOCK_END, void *);
                if (el) fire(el);
                /* 'close' fires after 'end' (Node ordering), listener or
                 * not (ADR-00501). */
                void *cl = F(sk, KML_NETSOCK_CLOSE, void *);
                if (cl) {
                    F(sk, KML_NETSOCK_CLOSE, void *) = 0;
                    fire(cl);
                }
            }
            break;
        }
    }
}

#if __BYTE_ORDER__ != __ORDER_LITTLE_ENDIAN__
#error "net.c assumes a little-endian host"
#endif

/* getsockname(fd): the host-order local port, or -1 on error. Backs
 * server.address()/socket.address() and, crucially, the `listen(0)`
 * ephemeral-port idiom (bind picks the port; this reads it back). */
int __kml_net_sockname_port(int fd) {
    unsigned char addr[16];
    int alen = 16;
    if (getsockname(fd, addr, &alen) != 0) return -1;
    unsigned short pn;
    memcpy(&pn, addr + 2, 2);
    return __builtin_bswap16(pn); /* ntohs */
}

/* getsockname(fd): the local IPv4 address as a length-prefixed string
 * ("0.0.0.0" for a wildcard-bound server, the real peer-local IP like
 * "127.0.0.1" for a connected socket), or "" on error. sockaddr_in's
 * sin_addr is at byte offset 4 on both Linux and BSD/macOS. */
void *__kml_net_sockname_addr(int fd) {
    unsigned char addr[16];
    char tmp[46];
    int alen = 16;
    if (getsockname(fd, addr, &alen) == 0 && inet_ntop(2, addr + 4, tmp, 46) != NULL) return __kml_str_from_cstr(tmp);
    return __kml_str_from_cstr("");
}
