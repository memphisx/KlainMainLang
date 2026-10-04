/* cpipc.c — the parent side of child_process.fork's IPC channel (TDD-00141;
 * in C per TDD-00240): wrapping the socketpair end in a ChildProcess handle,
 * the event-loop drain of a handle's channel (firing its 'message'
 * listeners), send and disconnect. The framing is ipc.c's; the fork itself
 * (__kml_cp_fork) is emitted with the program. kml_layout.h is prepended by
 * the compiler. */
#include <stdlib.h>
#include <string.h>

typedef long long i64;

#define F(p, off, T) (*(T *)((char *)(p) + (off)))

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
extern i64 __kml_ipc_send_raw(i64 fd, const void *s);
extern void __kml_cp_register(void *cp);
extern void *__kml_str_alloc(i64 n);

typedef void (*disc_fn)(void *cp);
typedef void (*ctrl_fn)(void *cp, void *msg);
typedef void (*msg_fn)(void *env, void *msg, _Bool isstr);

/* Cluster hooks (null unless the cluster runtime arms them, at fork time):
 * disc fires once when a handle's channel goes open to closed (either end);
 * ctrl intercepts a control-prefixed line ("__kml:...", e.g. a worker's
 * listening announcement) instead of delivering it as 'message'. */
disc_fn __kml_cp_disc_hook;
ctrl_fn __kml_cp_ctrl_hook;

#define IPC_FD(cp) F(cp, KML_CP_IPC_FD, int)

/* Build and register a ChildProcess handle around an already-created IPC
 * socket (parent end): inherited stdio (no pipes), fd made non-blocking, a
 * fresh line-buffer channel. Shared by __kml_cp_fork and cluster.fork. */
void *__kml_cp_wrap_ipc(i64 pid, int pfd) {
    int fl = fcntl(pfd, KML_F_GETFL);
    fcntl(pfd, KML_F_SETFL, fl | KML_O_NONBLOCK);
    void *cp = calloc(1, KML_CP_BYTES);
    F(cp, KML_CP_PID, i64) = pid;
    F(cp, KML_CP_STDIN, int) = -1;
    F(cp, KML_CP_STDOUT, int) = -1;
    F(cp, KML_CP_STDERR, int) = -1;
    IPC_FD(cp) = pfd;
    F(cp, KML_CP_CHAN, void *) = __kml_ipc_chan_new();
    __kml_cp_register(cp);
    return cp;
}

/* Read the channel until EAGAIN/EOF, feed the C-side line buffer, then fire
 * the 'message' listeners once per decoded line. EOF closes the fd (state
 * -1) so finalize can proceed. */
void __kml_cp_ipc_drain(void *cp) {
    char chunk[4096];
    i64 isstr;
    void *chan = F(cp, KML_CP_CHAN, void *);
    for (;;) {
        int fd = IPC_FD(cp);
        if (fd <= 0) break;
        i64 n = read(fd, chunk, 4096);
        if (n > 0) {
            __kml_ipc_feed(chan, chunk, n);
            continue;
        }
        if (n == 0) {
            close(fd);
            IPC_FD(cp) = -1;
            if (__kml_cp_disc_hook) __kml_cp_disc_hook(cp);
        }
        break;
    }
    if (!chan) return;
    char *msg;
    while ((msg = __kml_ipc_take2(chan, &isstr)) != NULL) {
        /* a "__kml:"-prefixed line is runtime control traffic (worker
         * listening announcements), routed to the ctrl hook instead of the
         * 'message' listeners */
        if (strncmp(msg, "__kml:", 6) == 0) {
            if (__kml_cp_ctrl_hook) __kml_cp_ctrl_hook(cp, msg);
        } else {
            /* the message field is a listener LIST: fire each in turn */
            void *node = F(cp, KML_CP_MESSAGE, void *);
            if (node) {
                size_t len = strlen(msg);
                char *mstr = (char *)__kml_str_alloc((i64)len);
                memcpy(mstr, msg, len);
                mstr[len] = 0;
                for (; node; node = F(node, KML_REACTION_NEXT, void *)) {
                    void *hdr = F(node, KML_REACTION_CLOSURE, void *);
                    F(hdr, KML_CLOSURE_FN, msg_fn)(F(hdr, KML_CLOSURE_ENV, void *), mstr, isstr != 0);
                }
            }
        }
        free(msg);
    }
}

/* JSON-quote + newline + write; false once closed. The _json sibling frames
 * an already-serialized JSON value verbatim (the non-string send path:
 * Node's json serialization mode). */
_Bool __kml_cp_send(void *cp, void *s) {
    int fd = IPC_FD(cp);
    return fd > 0 && __kml_ipc_send(fd, s) != 0;
}

_Bool __kml_cp_send_json(void *cp, void *s) {
    int fd = IPC_FD(cp);
    return fd > 0 && __kml_ipc_send_raw(fd, s) != 0;
}

void __kml_cp_disconnect(void *cp) {
    if (IPC_FD(cp) <= 0) return;
    /* deliver any message already in flight before closing (Node flushes the
     * channel on disconnect rather than dropping queued messages) */
    __kml_cp_ipc_drain(cp);
    /* the drain's own EOF path (worker already closed) has closed the fd AND
     * fired the disc hook: don't fire it twice */
    int fd = IPC_FD(cp);
    if (fd <= 0) return;
    close(fd);
    IPC_FD(cp) = -1;
    if (__kml_cp_disc_hook) __kml_cp_disc_hook(cp);
}
