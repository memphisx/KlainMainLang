/* errno.c — errno to Node error code name, libuv description and libuv
 * errno (TDD-00240; was generated per target as IR switches). One table
 * serves all three. POSIX targets switch on the platform's own errno
 * constants; Windows switches on the shim's Linux numbering (TDD-00177),
 * where libuv's own errno numbers are fixed, not the negated OS value.
 * Results are headered KML strings (length word before the bytes). */
#include <errno.h>

#define HSTR(s) ({ static const struct { long long n; char b[sizeof(s)]; } \
    __attribute__((aligned(8))) h_ = { sizeof(s) - 1, s }; (void *)h_.b; })

/* X(name, shim errno, libuv errno on Windows, libuv description or "") */
#define KML_ERRNOS(X) \
    X(EPERM, 1, -4048, "operation not permitted") \
    X(ENOENT, 2, -4058, "no such file or directory") \
    X(EIO, 5, -4070, "i/o error") \
    X(EBADF, 9, -4083, "bad file descriptor") \
    X(EACCES, 13, -4092, "permission denied") \
    X(EEXIST, 17, -4075, "file already exists") \
    X(ENOTDIR, 20, -4052, "not a directory") \
    X(EISDIR, 21, -4068, "illegal operation on a directory") \
    X(EINVAL, 22, -4071, "invalid argument") \
    X(EMFILE, 24, -4066, "too many open files") \
    X(ENFILE, 23, -4061, "file table overflow") \
    X(ENOSPC, 28, -4055, "no space left on device") \
    X(EROFS, 30, -4043, "read-only file system") \
    X(EBUSY, 16, -4082, "resource busy or locked") \
    X(ENOTEMPTY, 39, -4051, "directory not empty") \
    X(ELOOP, 40, -4067, "too many symbolic links encountered") \
    X(ENAMETOOLONG, 36, -4064, "name too long") \
    X(EXDEV, 18, -4037, "cross-device link not permitted") \
    X(EAGAIN, 11, -4088, "resource temporarily unavailable") \
    X(EPIPE, 32, -4047, "broken pipe") \
    X(EFBIG, 27, -4036, "file too large") \
    X(ENODEV, 19, -4059, "no such device") \
    X(ESPIPE, 29, -4041, "invalid seek") \
    X(EMLINK, 31, -4032, "too many links") \
    X(ESRCH, 3, -4040, "no such process") \
    X(ECHILD, 10, -4094, "") \
    X(EADDRINUSE, 98, -4091, "address already in use") \
    X(EADDRNOTAVAIL, 99, -4090, "address not available") \
    X(ECONNRESET, 104, -4077, "connection reset by peer") \
    X(ECONNREFUSED, 111, -4078, "connection refused") \
    X(ETIMEDOUT, 110, -4039, "connection timed out") \
    X(EHOSTUNREACH, 113, -4073, "host is unreachable") \
    X(ENETUNREACH, 101, -4062, "network is unreachable") \
    X(ECONNABORTED, 103, -4079, "software caused connection abort") \
    X(ENOBUFS, 105, -4060, "no buffer space available")
#ifdef _WIN32
#define KML_ERRNOS_WIN(X) \
    X(ENOTSUP, 4049, -4049, "") \
    X(UNKNOWN, 4094, -4094, "unknown error") \
    X(ENOMEM, 12, -4057, "") \
    X(EFAULT, 14, -4074, "") \
    X(ENOSYS, 38, -4054, "") \
    X(EINPROGRESS, 115, -4094, "") \
    X(ENETDOWN, 100, -4063, "") \
    X(ENETRESET, 102, -4094, "") \
    X(EISCONN, 106, -4069, "") \
    X(ENOTCONN, 107, -4053, "") \
    X(ESHUTDOWN, 108, -4042, "") \
    X(EHOSTDOWN, 112, -4031, "") \
    X(EALREADY, 114, -4084, "") \
    X(ENOTSOCK, 88, -4050, "") \
    X(EDESTADDRREQ, 89, -4076, "") \
    X(EMSGSIZE, 90, -4065, "") \
    X(EPROTOTYPE, 91, -4044, "") \
    X(ENOPROTOOPT, 92, -4035, "") \
    X(EPROTONOSUPPORT, 93, -4045, "") \
    X(EOPNOTSUPP, 95, -4049, "") \
    X(EAFNOSUPPORT, 97, -4089, "")
#else
#define KML_ERRNOS_WIN(X)
#endif

#ifdef _WIN32
#define EV(n, w) (w)
#else
#define EV(n, w) (n)
#endif

void *__kml_errno_code(int e) {
    switch (e) {
#define X(n, w, u, d) case EV(n, w): return HSTR(#n);
    KML_ERRNOS(X)
    KML_ERRNOS_WIN(X)
#undef X
    }
    return 0;
}

/* libuv's description, or null when it has none (the caller falls back to
 * strerror). */
void *__kml_errno_desc(int e) {
    switch (e) {
#define X(n, w, u, d) case EV(n, w): return sizeof(d) > 1 ? HSTR(d) : 0;
    KML_ERRNOS(X)
    KML_ERRNOS_WIN(X)
#undef X
    }
    return 0;
}

/* Node's err.errno: POSIX libuv numbers are the negated OS errno. */
int __kml_uv_errno(int e) {
#ifdef _WIN32
    if (e == 0) return 0;
    switch (e) {
#define X(n, w, u, d) case w: return u;
    KML_ERRNOS(X)
    KML_ERRNOS_WIN(X)
#undef X
    }
    return -4094;
#else
    return -e;
#endif
}
