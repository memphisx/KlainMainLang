/* fswatch.c — fs.watch (TDD-00181; in C per TDD-00240): a native,
 * event-driven file watcher folded into the select() event loop through the
 * hook trio __kml_fswatch_keepalive / _fdset_add / _dispatch, like worker,
 * cp and readline. Linux: inotify; its fd is readable and drops into the
 * loop's read set with no thread. macOS: kqueue + EVFILT_VNODE on an open fd
 * per watcher (the FSWatcher goes in the kevent's udata); kqueue cannot name
 * a changed directory entry, so `filename` is the watched path. Windows: a
 * thread per watcher in win32fswatch.c plus a loopback wakeup socket. Other
 * targets have no backend: watch throws, the hooks are inert. */
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#if defined(__APPLE__)
#include <fcntl.h>
#include <sys/event.h>
#include <sys/time.h>
#endif

typedef long long i64;

#define HSTR(s) ({ static const struct { long long n; char b[sizeof(s)]; } \
    __attribute__((aligned(8))) h_ = { sizeof(s) - 1, s }; (void *)h_.b; })

extern void __kml_fs_throw(void *opdesc, const char *syscall, const char *path);
extern void *__kml_str_from_cstr(const char *s);
extern void __kml_native_set_last_string(const char *s);
#if defined(_WIN32)
extern void *__kml_fswatch_win_start(const char *path);
extern int __kml_fswatch_win_wakefd(void);
extern int __kml_fswatch_win_next(void **ctx, int *is_rename, char *name);
extern void __kml_fswatch_win_stop(void *ctx);
#elif defined(__linux__)
extern int inotify_init1(int flags);
extern int inotify_add_watch(int fd, const char *path, unsigned mask);
extern int inotify_rm_watch(int fd, int wd);
#endif

/* An FSWatcher: wd is the inotify watch descriptor, the kqueue's open fd, or
 * the Windows watcher context; open is 1 until closed. The listeners are
 * closures {fn, env} called as fn(env, eventType, filename). */
typedef struct {
    i64 wd;
    void *change_cb, *rename_cb;
    i64 open;
    const char *name;
} watcher;

typedef void (*listener_fn)(void *env, void *ev, void *fname);

static i64 nopen;

#if defined(__linux__) || defined(__APPLE__) || defined(_WIN32)
static void fire(void *cb, void *ev, void *fname) {
    void **c = (void **)cb;
    ((listener_fn)c[0])(c[1], ev, fname);
}

#if defined(__linux__) || defined(_WIN32)
static watcher **reg;
static i64 reg_len, reg_cap;

static watcher *mkwatcher(i64 wd, const char *path) {
    watcher *w = (watcher *)malloc(sizeof(watcher));
    w->wd = wd;
    w->change_cb = w->rename_cb = 0;
    w->open = 1;
    w->name = path;
    return w;
}

static void reg_add(watcher *w) {
    if (reg_len + 1 > reg_cap) {
        i64 nc = reg_cap * 2 > 8 ? reg_cap * 2 : 8;
        reg = (watcher **)realloc(reg, (size_t)nc * sizeof(void *));
        reg_cap = nc;
    }
    reg[reg_len++] = w;
    nopen++;
}
#endif

/* Set fd in the select() read set (a byte-wise bitset) and raise *maxfd. */
static void fdset_set(void *fdset, int *maxfd, int fd) {
    ((unsigned char *)fdset)[fd / 8] |= (unsigned char)(1 << (fd % 8));
    if (fd > *maxfd) *maxfd = fd;
}
#endif

_Bool __kml_fswatch_keepalive(void) { return nopen > 0; }

void __kml_fswatch_on(void *wp, i64 is_rename, void *cb) {
#if defined(__linux__) || defined(__APPLE__) || defined(_WIN32)
    watcher *w = (watcher *)wp;
    if (is_rename) w->rename_cb = cb;
    else w->change_cb = cb;
#else
    (void)wp; (void)is_rename; (void)cb;
#endif
}

#if defined(__linux__)

static int ifd = -1;

void *__kml_fs_watch(const char *path) {
    int wd = -1;
    if (ifd < 0) ifd = inotify_init1(0x80800); /* IN_NONBLOCK|IN_CLOEXEC */
    /* IN_MODIFY|IN_ATTRIB|IN_CREATE|IN_DELETE|IN_MOVED_FROM|IN_MOVED_TO|
     * IN_MOVE_SELF|IN_DELETE_SELF */
    if (ifd >= 0) wd = inotify_add_watch(ifd, path, 0xFC6);
    if (wd < 0) {
        __kml_fs_throw(HSTR("cannot watch path"), HSTR("watch"), path);
        return 0;
    }
    watcher *w = mkwatcher(wd, path);
    reg_add(w);
    return w;
}

_Bool __kml_fswatch_fdset_add(void *fdset, int *maxfd) {
    if (nopen > 0 && ifd >= 0) fdset_set(fdset, maxfd, ifd);
    return 0;
}

void __kml_fswatch_close(void *wp) {
    watcher *w = (watcher *)wp;
    if (!w->open) return;
    inotify_rm_watch(ifd, (int)w->wd);
    w->open = 0;
    nopen--;
}

/* Read every pending inotify event and fire the matching watcher's
 * listener. struct inotify_event = { int wd; u32 mask, cookie, len;
 * char name[len] }. */
void __kml_fswatch_dispatch(void) {
    if (ifd < 0) return;
    for (;;) {
        _Alignas(8) char buf[4096];
        i64 n = (i64)read(ifd, buf, sizeof buf);
        if (n <= 0) return;
        for (i64 off = 0; off < n;) {
            char *ev = buf + off;
            int wd;
            unsigned mask, len;
            memcpy(&wd, ev, 4);
            memcpy(&mask, ev + 4, 4);
            memcpy(&len, ev + 12, 4);
            off += 16 + (i64)len;
            /* rename if mask & 0xFC0, else change if mask & 0x6 */
            int is_rename = (mask & 0xFC0) != 0;
            if (!is_rename && !(mask & 0x6)) continue;
            for (i64 i = 0; i < reg_len; i++) {
                watcher *w = reg[i];
                if (w->wd != wd || !w->open) continue;
                void *cb = is_rename ? w->rename_cb : w->change_cb;
                if (cb) {
                    /* the event's own name if present, else the watcher's path */
                    void *fname = len > 0 ? __kml_str_from_cstr(ev + 16) : (void *)w->name;
                    fire(cb, is_rename ? HSTR("rename") : HSTR("change"), fname);
                }
                break;
            }
        }
    }
}

#elif defined(__APPLE__)

static int kq = -1;

void *__kml_fs_watch(const char *path) {
    int fd = -1;
    if (kq < 0) kq = kqueue();
    if (kq >= 0) fd = open(path, O_EVTONLY);
    if (fd < 0) {
        __kml_fs_throw(HSTR("cannot watch path"), HSTR("watch"), path);
        return 0;
    }
    watcher *w = (watcher *)malloc(sizeof(watcher));
    w->wd = fd;
    w->change_cb = w->rename_cb = 0;
    w->open = 1;
    w->name = path;
    struct kevent kev;
    EV_SET(&kev, (uintptr_t)fd, EVFILT_VNODE, EV_ADD | EV_CLEAR,
           NOTE_DELETE | NOTE_WRITE | NOTE_EXTEND | NOTE_ATTRIB | NOTE_LINK | NOTE_RENAME | NOTE_REVOKE, 0, w);
    kevent(kq, &kev, 1, 0, 0, 0);
    nopen++;
    return w;
}

_Bool __kml_fswatch_fdset_add(void *fdset, int *maxfd) {
    if (nopen > 0 && kq >= 0) fdset_set(fdset, maxfd, kq);
    return 0;
}

void __kml_fswatch_close(void *wp) {
    watcher *w = (watcher *)wp;
    if (!w->open) return;
    close((int)w->wd); /* closing the fd removes its kevent from the kqueue */
    w->open = 0;
    nopen--;
}

void __kml_fswatch_dispatch(void) {
    if (kq < 0) return;
    struct kevent evs[16];
    struct timespec ts = {0, 0};
    int n = kevent(kq, 0, 0, evs, 16, &ts);
    for (int i = 0; i < n; i++) {
        watcher *w = (watcher *)evs[i].udata;
        unsigned ff = evs[i].fflags;
        /* rename if DELETE|RENAME|REVOKE, else change if WRITE|EXTEND|ATTRIB|LINK */
        int is_rename = (ff & (NOTE_DELETE | NOTE_RENAME | NOTE_REVOKE)) != 0;
        if (!is_rename && !(ff & (NOTE_WRITE | NOTE_EXTEND | NOTE_ATTRIB | NOTE_LINK))) continue;
        if (!w || !w->open) continue;
        void *cb = is_rename ? w->rename_cb : w->change_cb;
        if (cb) fire(cb, is_rename ? HSTR("rename") : HSTR("change"), (void *)w->name);
    }
}

#elif defined(_WIN32)

void *__kml_fs_watch(const char *path) {
    void *ctx = __kml_fswatch_win_start(path);
    if (!ctx) {
        __kml_fs_throw(HSTR("cannot watch path"), HSTR("watch"), path);
        return 0;
    }
    watcher *w = mkwatcher((i64)(intptr_t)ctx, path);
    reg_add(w);
    return w;
}

_Bool __kml_fswatch_fdset_add(void *fdset, int *maxfd) {
    if (nopen > 0) {
        int wfd = __kml_fswatch_win_wakefd();
        if (wfd >= 0) fdset_set(fdset, maxfd, wfd);
    }
    return 0;
}

void __kml_fswatch_close(void *wp) {
    watcher *w = (watcher *)wp;
    if (!w->open) return;
    __kml_fswatch_win_stop((void *)(intptr_t)w->wd);
    w->open = 0;
    nopen--;
}

void __kml_fswatch_dispatch(void) {
    void *ctx;
    int is_rename;
    char name[520];
    while (__kml_fswatch_win_next(&ctx, &is_rename, name)) {
        for (i64 i = 0; i < reg_len; i++) {
            watcher *w = reg[i];
            if (w->wd != (i64)(intptr_t)ctx || !w->open) continue;
            void *cb = is_rename ? w->rename_cb : w->change_cb;
            if (cb) fire(cb, is_rename ? HSTR("rename") : HSTR("change"), __kml_str_from_cstr(name));
            break;
        }
    }
}

#else /* no backend: watch throws, the hooks are inert */

void *__kml_fs_watch(const char *path) {
    __kml_fs_throw(HSTR("cannot watch path"), HSTR("watch"), path);
    return 0;
}
_Bool __kml_fswatch_fdset_add(void *fdset, int *maxfd) { (void)fdset; (void)maxfd; return 0; }
void __kml_fswatch_close(void *wp) { (void)wp; }
void __kml_fswatch_dispatch(void) {}

#endif

/* The natives lib/node/fs.ts's FSWatcher runs on. __kml_native_fs_watch(path,
 * inv, clo) starts a watcher whose every event calls inv(clo, isRename, 0)
 * with the file name as the last string, and returns the watcher's handle. */
typedef void (*native_inv)(void *clo, double kind, double zero);

static void tramp(void *env, void *ev, void *fname) {
    void **e = (void **)env;
    __kml_native_set_last_string((const char *)fname);
    ((native_inv)e[0])(e[1], *(char *)ev == 'r' ? 1.0 : 0.0, 0.0);
}

double __kml_native_fs_watch(const char *path, void *inv, void *clo) {
    void *w = __kml_fs_watch(path);
    void **env = (void **)malloc(2 * sizeof(void *));
    env[0] = inv;
    env[1] = clo;
    void **cl = (void **)malloc(2 * sizeof(void *));
    cl[0] = (void *)tramp;
    cl[1] = env;
    __kml_fswatch_on(w, 0, cl);
    __kml_fswatch_on(w, 1, cl);
    return (double)(unsigned long long)(uintptr_t)w;
}

void __kml_native_fs_watch_close(double h) {
    __kml_fswatch_close((void *)(uintptr_t)(unsigned long long)h);
}
