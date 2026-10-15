/* ipcc.c — the child side of child_process.fork's IPC channel (TDD-00141; in
 * C per TDD-00240): NODE_CHANNEL_FD parsed once, send, and the event-loop
 * reader, an unreferenced libuv watcher on POSIX (the select bridge's hooks
 * on Windows, until TDD-00244 Stage 6). The framing is ipc.c's. Linked only
 * when a program touches the channel. kml_layout.h is prepended by the
 * compiler. */
#include <stdlib.h>

typedef long long i64;

/* Declared as the emitted IR declares them; no system header, so the
 * prototypes cannot clash with libc's. */
#pragma clang diagnostic ignored "-Wincompatible-library-redeclaration"
extern i64 read(int, void *, i64);
extern int close(int);
extern int fcntl(int, int, ...);

#define KML_F_GETFL 3
#define KML_F_SETFL 4
#if defined(__APPLE__)
#define KML_O_NONBLOCK 0x4
#else
#define KML_O_NONBLOCK 0x800
#endif

extern void *__kml_ipc_chan_new(void);
extern void __kml_ipc_feed(void *chan, const void *src, i64 n);
extern char *__kml_ipc_take2(void *chan, i64 *isstr);
extern i64 __kml_ipc_send(i64 fd, const void *s);
extern void *__kml_loop_fd_watch(int fd, void (*cb)(void *), void *data, int refd);
extern void __kml_loop_fd_unwatch(void *w);
static void *watch;
static void drain(void *unused);

/* -2 = not yet probed, -1 = no channel, >0 = the channel fd. */
static int chan_fd = -2;
static void *chan;
/* Set once the process emitter's channel (lib/node/internal_process.ts)
 * reads the descriptor: this reader then leaves it alone, and the channel's
 * sends (cluster's announcements) still go out. */
static unsigned char claimed;

int __kml_ipcc_fd(void) {
    if (chan_fd != -2) return chan_fd;
    const char *env = getenv("NODE_CHANNEL_FD");
    if (env) {
        int fd = (int)atoll(env);
        if (fd > 0) {
            /* nonblocking + a line buffer, ready for the event loop */
            int fl = fcntl(fd, KML_F_GETFL);
            fcntl(fd, KML_F_SETFL, fl | KML_O_NONBLOCK);
            chan = __kml_ipc_chan_new();
            chan_fd = fd;
            if (!claimed)
                watch = __kml_loop_fd_watch(fd, drain, 0, 0);
            return fd;
        }
    }
    chan_fd = -1;
    return -1;
}

_Bool __kml_ipcc_send(void *s) {
    int fd = __kml_ipcc_fd();
    return fd > 0 && __kml_ipc_send(fd, s) != 0;
}

/* The channel is unreferenced here: the process emitter's channel holds the
 * loop open while it has 'message' listeners, as Node's does. Nothing here
 * listens either: a message is the process emitter's once it claims the
 * channel, so the reader only keeps the pipe drained until then. */
static void drain(void *unused) {
    char chunk[4096];
    i64 isstr;
    void *c = chan;
    (void)unused;
    if (claimed) return;
    for (;;) {
        int fd = chan_fd;
        if (fd <= 0) break;
        i64 n = read(fd, chunk, 4096);
        if (n > 0) {
            __kml_ipc_feed(c, chunk, n);
            continue;
        }
        if (n == 0) {
            __kml_loop_fd_unwatch(watch);
            watch = 0;
            close(fd);
            chan_fd = -1;
        }
        break;
    }
    if (!c) return;
    char *msg;
    while ((msg = __kml_ipc_take2(c, &isstr)) != NULL) free(msg);
}


/* lib/native.d.ts's ipcChildClaim: the process emitter's fork channel takes
 * the descriptor over from this reader (which cluster's worker side still
 * sends through), probing it first so NODE_CHANNEL_FD may then be deleted. */
void __kml_native_ipc_child_claim(void) {
    claimed = 1;
    __kml_ipcc_fd();
    __kml_loop_fd_unwatch(watch);
    watch = 0;
}
