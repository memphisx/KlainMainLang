/* netsock.c — the two byte-IO helpers of a net.Socket handle (in C per
 * TDD-00240): write n bytes to its connection and close it. Split from
 * net.c so a path that hands out a net.Socket without a net server (the HTTP
 * 'clientError'/'connection' events) links just these. kml_layout.h is
 * prepended by the compiler. */
typedef long long i64;

#define F(p, off, T) (*(T *)((char *)(p) + (off)))

/* Declared as the emitted IR declares them; no system header, so the
 * prototypes cannot clash with libc's. */
#pragma clang diagnostic ignored "-Wincompatible-library-redeclaration"
extern i64 write(int, const void *, i64);
extern int close(int);


typedef void (*close_fn)(void *env);

/* Write n bytes to the connection fd (no-op once closed). */
void __kml_net_sock_write(void *sock, void *data, i64 n) {
    i64 fd = F(sock, KML_NETSOCK_FD, i64);
    if (fd < 0) return;
    write((int)fd, data, n);
}

/* Close the connection (a no-op once closed) and fire its 'close' listener
 * once (ADR-00501). */
void __kml_net_sock_close(void *sock) {
    i64 fd = F(sock, KML_NETSOCK_FD, i64);
    if (fd < 0) return;
    close((int)fd);
    F(sock, KML_NETSOCK_FD, i64) = -1;
    F(sock, KML_NETSOCK_STATE, i64) = 1;
    void *cl = F(sock, KML_NETSOCK_CLOSE, void *);
    if (cl) {
        F(sock, KML_NETSOCK_CLOSE, void *) = 0;
        F(cl, KML_CLOSURE_FN, close_fn)(F(cl, KML_CLOSURE_ENV, void *));
    }
}
