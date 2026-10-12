/* httpsrv.c — the primary HTTP server's fixed runtime (TDD-00244 Stage 5,
 * in C per TDD-00240): the additional listeners' table, close() and
 * closeAllConnections(). The accept paths and the connection fibers' entry,
 * whose IR still depends on the program (its TLS and HTTP/2 use), are
 * runtime_http.go's until they move here too. Linked with loop.c. */

typedef long long i64;
typedef unsigned char u8;

/* Declared as the emitted IR declares them; no system header, so the
 * prototypes cannot clash with libc's (or with the Windows shims'). */
#pragma clang diagnostic ignored "-Wincompatible-library-redeclaration"
extern int close(int);
extern int shutdown(int, int);
extern void *realloc(void *, unsigned long);

/* The connection table (runtime_http.go): a slot is { fd, ucontext, stack,
 * pendingFetch, pendingGroup }, fd -1 when free. */
typedef struct { i64 fd; void *ctx, *stack, *fetch, *group; } kml_conn;
extern _Thread_local kml_conn *__kml_conn_data;
extern _Thread_local i64 __kml_conn_len;
extern _Thread_local int __kml_listen_fd;
extern _Thread_local u8 __kml_conn_poke;
/* A {workers:N} cluster's shared page: word 0 set by close(), word 1 by
 * closeAllConnections(); null in a single process. */
extern i64 *__kml_cluster_close_flag;

/* ---- additional listeners (TDD-00191) ----
 * A server beyond the primary: { fd (-1 once closed), dispatch, SSL_CTX*
 * (null for plain), HTTP/2 vtable }. runtime_http.go's accept reads the
 * table. */
typedef struct { i64 fd; void *dispatch, *tlsctx, *h2vtbl; } kml_xl;
_Thread_local kml_xl *__kml_http_xl_data;
_Thread_local i64 __kml_http_xl_len;
static _Thread_local i64 xl_cap;

/* A closed listener's slot is reused, so a close()/listen() cycle keeps the
 * table bounded. */
void __kml_http_register_extra_listener(int fd, void *dispatch, void *tlsctx, void *h2vtbl) {
    kml_xl *s = 0;
    for (i64 i = 0; i < __kml_http_xl_len; i++)
        if (__kml_http_xl_data[i].fd < 0) {
            s = &__kml_http_xl_data[i];
            break;
        }
    if (!s) {
        if (__kml_http_xl_len + 1 > xl_cap) {
            i64 nc = xl_cap * 2 > 4 ? xl_cap * 2 : 4;
            __kml_http_xl_data = (kml_xl *)realloc(__kml_http_xl_data, (unsigned long)nc * sizeof(kml_xl));
            xl_cap = nc;
        }
        s = &__kml_http_xl_data[__kml_http_xl_len++];
    }
    s->fd = fd;
    s->dispatch = dispatch;
    s->tlsctx = tlsctx;
    s->h2vtbl = h2vtbl;
}

/* While any additional listener is open, the loop stays alive. */
_Bool __kml_http_xl_keepalive(void) {
    for (i64 i = 0; i < __kml_http_xl_len; i++)
        if (__kml_http_xl_data[i].fd >= 0) return 1;
    return 0;
}

#ifdef _WIN32
/* The open listeners' descriptors into the loop's read set. On POSIX each
 * has its own watcher (loop.c). */
_Bool __kml_http_xl_fdset_add(u8 *set, int *maxfd) {
    for (i64 i = 0; i < __kml_http_xl_len; i++) {
        i64 fd = __kml_http_xl_data[i].fd;
        if (fd < 0) continue;
        set[fd / 8] |= (u8)(1u << (fd % 8));
        if (fd > *maxfd) *maxfd = (int)fd;
    }
    return 0;
}
#endif

/* server.close() on an additional server: it stops accepting, and the
 * connections it accepted finish on their own. A no-op for an unknown or
 * closed descriptor. */
void __kml_http_close_extra_listener(int fd) {
    for (i64 i = 0; i < __kml_http_xl_len; i++)
        if (__kml_http_xl_data[i].fd == fd) {
            close(fd);
            __kml_http_xl_data[i].fd = -1;
            return;
        }
}

/* ---- close() and closeAllConnections() (TDD-00027, TDD-00117/00118) ---- */

/* http.close(): the listener closes now, and every parked fiber is woken
 * once so a connection idle between keep-alive requests sees it and retires
 * (Node >= 19 closes idle keep-alive connections), letting the active count
 * reach zero. In a cluster, the shared word tells every worker to close its
 * own listener. Redundant calls are no-ops. */
void __kml_http_close(void) {
    if (__kml_cluster_close_flag)
        __atomic_store_n(&__kml_cluster_close_flag[0], 1, __ATOMIC_SEQ_CST);
    int fd = __kml_listen_fd;
    if (fd < 0) return;
    close(fd);
    __kml_listen_fd = -1;
    __kml_conn_poke = 1;
}

/* Every local connection is shut down (SHUT_RDWR, 2 on every host): its
 * fiber reads the end of the stream and unwinds through its own finish
 * path, which owns the close. Nothing is torn down from outside. */
void __kml_http_shutdown_local_conns(void) {
    for (i64 i = 0; i < __kml_conn_len; i++) {
        i64 fd = __kml_conn_data[i].fd;
        if (fd >= 0) shutdown((int)fd, 2);
    }
}

/* http.closeAllConnections(): close every connection now, in every worker
 * of a cluster. */
void __kml_http_close_all_conns(void) {
    if (__kml_cluster_close_flag)
        __atomic_store_n(&__kml_cluster_close_flag[1], 1, __ATOMIC_SEQ_CST);
    __kml_http_shutdown_local_conns();
}
