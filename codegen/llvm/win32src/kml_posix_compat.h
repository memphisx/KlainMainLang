// Windows replacement for the POSIX headers the embedded C helpers include
// (unistd.h, poll.h, sys/socket.h, netdb.h, netinet/in.h, arpa/inet.h) —
// TDD-00177 Stage 2. It declares the fd-based I/O surface that win32io.c
// implements, with the *Linux* constant values those helpers were written
// against, so a helper's `#ifdef _WIN32` branch is just this include.
//
// Written into the shim cache directory next to the compiled shim objects
// and made visible through -I (see hostclang.go); never installed anywhere.
#ifndef KML_POSIX_COMPAT_H
#define KML_POSIX_COMPAT_H

#include <stddef.h>
#include <stdint.h>
#include <errno.h>
#include <time.h> // struct timespec, for the nanosleep adapter below

#ifdef __cplusplus
extern "C" {
#endif

// ---- errno (Linux numbering, as win32io.c sets it) --------------------------
#undef EAGAIN
#undef EWOULDBLOCK
#undef EINPROGRESS
#undef EINTR
#undef ECONNRESET
#undef ECONNREFUSED
#undef EADDRINUSE
#undef ENOTCONN
#undef EPIPE
#undef ETIMEDOUT
#define EAGAIN 11
#define EWOULDBLOCK 11
#define EINTR 4
#define EPIPE 32
#define EADDRINUSE 98
#define ECONNRESET 104
#define ENOTCONN 107
#define ETIMEDOUT 110
#define ECONNREFUSED 111
#define EINPROGRESS 115

// ---- fcntl -----------------------------------------------------------------------
#define F_GETFL 3
#define F_SETFL 4
#define O_NONBLOCK 0x800
int fcntl(int fd, int cmd, ...);

// ---- unistd -----------------------------------------------------------------------
int64_t read(int fd, void *buf, size_t n);
int64_t write(int fd, const void *buf, size_t n);
int close(int fd);
int pipe(int fds[2]);
int usleep(unsigned usec);
int64_t sysconf(int name);
#define _SC_NPROCESSORS_ONLN 84
#define _SC_PAGESIZE 30

// ---- time -------------------------------------------------------------------------
// The shim's nanosleep takes the IR's { i64 sec, i64 nsec } layout; mingw's
// struct timespec has a 32-bit tv_nsec, so a helper's call is converted.
struct kml_timespec64 { int64_t sec, nsec; };
// Bound under a private name: mingw's time.h already declares nanosleep
// with its own struct timespec, and the two prototypes must not meet.
int kml_nanosleep64(const struct kml_timespec64 *req, struct kml_timespec64 *rem) __asm__("nanosleep");
static inline int kml_nanosleep_ts(const struct timespec *req) {
	struct kml_timespec64 r = { (int64_t)req->tv_sec, (int64_t)req->tv_nsec };
	return kml_nanosleep64(&r, NULL);
}
#define nanosleep(req, rem) kml_nanosleep_ts(req)

// ---- ucontext (Win32 Fibers in the shim) ---------------------------------------
// Field offsets match win32io.c's kml_ucontext: fiber 0, ss_sp 8, ss_size
// 16, uc_link 24, fn 32, argc 40.
typedef struct kml_ucontext_t {
	void *fiber;
	struct { void *ss_sp; size_t ss_size; } uc_stack;
	struct kml_ucontext_t *uc_link;
	void (*fn)(void);
	int64_t argc;
} ucontext_t;
int getcontext(ucontext_t *ctx);
void makecontext(ucontext_t *ctx, void (*fn)(void), int argc, ...);
int swapcontext(ucontext_t *from, ucontext_t *to);

// ---- poll -------------------------------------------------------------------------
// A helper that also pulls in winsock2.h (tls.c via OpenSSL) already has
// Winsock's struct pollfd/POLL* — with a SOCKET-sized fd and different
// bit values. This layer's poll() takes int fds with Linux bits, so the
// name is redirected to this project's own struct unconditionally.
#undef POLLIN
#undef POLLOUT
#undef POLLERR
#undef POLLHUP
#define POLLIN 0x0001
#define POLLOUT 0x0004
#define POLLERR 0x0008
#define POLLHUP 0x0010
struct kml_pollfd { int fd; short events; short revents; };
#define pollfd kml_pollfd
typedef unsigned long nfds_t;
int poll(struct kml_pollfd *fds, nfds_t n, int timeout);

// ---- sockets ----------------------------------------------------------------------
#define AF_INET 2
#define AF_INET6 10
#define SOCK_STREAM 1
#define SOCK_DGRAM 2
#define IPPROTO_TCP 6
#define TCP_NODELAY 1
#define SOL_SOCKET 1
#define SO_REUSEADDR 2
#define SO_KEEPALIVE 9
#define SHUT_RDWR 2
#define INADDR_ANY 0u
#define INADDR_LOOPBACK 0x7f000001u
#ifndef _WINSOCK2API_ // winsock2.h (via OpenSSL) already defines these, same layout
typedef int socklen_t;
typedef uint16_t sa_family_t;
typedef uint16_t in_port_t;
typedef uint32_t in_addr_t;
struct in_addr { in_addr_t s_addr; };
struct sockaddr { sa_family_t sa_family; char sa_data[14]; };
struct sockaddr_in {
	sa_family_t sin_family;
	in_port_t sin_port;
	struct in_addr sin_addr;
	char sin_zero[8];
};
#endif
// Windows layout: ai_canonname precedes ai_addr (unlike glibc).
struct addrinfo {
	int ai_flags, ai_family, ai_socktype, ai_protocol;
	size_t ai_addrlen;
	char *ai_canonname;
	struct sockaddr *ai_addr;
	struct addrinfo *ai_next;
};
#ifndef _WINSOCK2API_
int socket(int domain, int type, int protocol);
int bind(int fd, const void *addr, int len);
int listen(int fd, int backlog);
int accept(int fd, void *addr, int *len);
int connect(int fd, const void *addr, int len);
int shutdown(int fd, int how);
int setsockopt(int fd, int level, int opt, const void *val, int len);
int getsockopt(int fd, int level, int opt, void *val, int *len);
int getsockname(int fd, void *addr, int *len);
int getpeername(int fd, void *addr, int *len);
int64_t recvfrom(int fd, void *buf, size_t n, int flags, void *addr, int *alen);
int64_t sendto(int fd, const void *buf, size_t n, int flags, const void *addr, int alen);
static inline int64_t recv(int fd, void *buf, size_t n, int flags) { (void)flags; return read(fd, buf, n); }
static inline int64_t send(int fd, const void *buf, size_t n, int flags) { (void)flags; return write(fd, buf, n); }
unsigned short htons(unsigned short v);
unsigned short ntohs(unsigned short v);
static inline uint32_t htonl(uint32_t v) { return ((v & 0xffu) << 24) | ((v & 0xff00u) << 8) | ((v >> 8) & 0xff00u) | (v >> 24); }
static inline uint32_t ntohl(uint32_t v) { return htonl(v); }
int inet_pton(int af, const char *src, void *dst);
const char *inet_ntop(int af, const void *src, char *dst, size_t size);
int getaddrinfo(const char *node, const char *svc, const void *hints, struct addrinfo **res);
void freeaddrinfo(struct addrinfo *ai);
#endif

#ifdef __cplusplus
}
#endif
#endif
