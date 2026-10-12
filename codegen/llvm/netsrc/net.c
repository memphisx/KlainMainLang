/* net.c — the socket-name readers behind server.address() and
 * socket.address() (in C per TDD-00240; Node's `net` module itself is
 * lib/node/net.ts, its sockets the handle table's in klainpool.c).
 * kml_layout.h is prepended by the compiler. */
#include <string.h>

typedef long long i64;

/* Declared as the emitted IR declares them; no system header, so the
 * prototypes cannot clash with libc's. */
#pragma clang diagnostic ignored "-Wincompatible-library-redeclaration"
extern int getsockname(int, void *, void *);
extern const char *inet_ntop(int, const void *, char *, int);

extern void *__kml_str_from_cstr(const char *c);

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
