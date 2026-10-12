/* fswatch.c — fs.watch (TDD-00181; in C per TDD-00240). On POSIX each
 * FSWatcher is a libuv uv_fs_event_t on the event loop (loop.c, TDD-00244
 * Stage 4): inotify on Linux, FSEvents or kqueue on macOS, its listener
 * fired from libuv's callback. Windows keeps a thread per watcher in win32fswatch.c plus a loopback
 * wakeup socket in the loop's read set until its loop moves to libuv
 * (Stage 6). */
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>

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
#else
typedef void (*kml_fs_event_cb)(void *data, const char *filename, int is_rename);
extern void *__kml_loop_fs_event_start(const char *path, int recursive, kml_fs_event_cb cb, void *data, int *err);
extern void __kml_loop_fs_event_stop(void *h);
extern void __kml_loop_handle_ref(void *h, int on);
#endif

/* An FSWatcher: wd is the libuv handle or the Windows watcher context; open
 * is 1 until closed; refd is 0 after unref() (or persistent: false), and only
 * a ref'd open watcher holds the loop. The listeners are closures {fn, env} called as
 * fn(env, eventType, filename). Every watcher stays in reg, which keeps its
 * listeners reachable under -mm=gc. */
typedef struct {
    i64 wd;
    void *change_cb, *rename_cb;
    i64 open, refd;
    const char *name;
} watcher;

typedef void (*listener_fn)(void *env, void *ev, void *fname);

static i64 nopen;

static void fire(void *cb, void *ev, void *fname) {
    void **c = (void **)cb;
    ((listener_fn)c[0])(c[1], ev, fname);
}

static watcher **reg;
static i64 reg_len, reg_cap;

static watcher *mkwatcher(i64 wd, const char *path) {
    watcher *w = (watcher *)malloc(sizeof(watcher));
    w->wd = wd;
    w->change_cb = w->rename_cb = 0;
    w->open = 1;
    w->refd = 1;
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

#if defined(_WIN32)
/* Set fd in the select() read set (a byte-wise bitset) and raise *maxfd. */
static void fdset_set(void *fdset, int *maxfd, int fd) {
    ((unsigned char *)fdset)[fd / 8] |= (unsigned char)(1 << (fd % 8));
    if (fd > *maxfd) *maxfd = fd;
}
#endif

void __kml_fswatch_on(void *wp, i64 is_rename, void *cb) {
    watcher *w = (watcher *)wp;
    if (is_rename) w->rename_cb = cb;
    else w->change_cb = cb;
}

#if defined(_WIN32)

_Bool __kml_fswatch_keepalive(void) { return nopen > 0; }

void *__kml_fs_watch(const char *path, int recursive) {
    (void)recursive; /* ReadDirectoryChangesW watches one level */
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
    if (w->refd) nopen--;
}

void __kml_fswatch_set_ref(void *wp, int on) {
    watcher *w = (watcher *)wp;
    if (!w->open || w->refd == on) return;
    w->refd = on;
    nopen += on ? 1 : -1;
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

#else

/* The base name of a path, as libuv reports a watched file's own event. */
static const char *base_name(const char *p) {
    const char *b = strrchr(p, '/');
    return b ? b + 1 : p;
}

/* Inside the wait, as Node's FSEvent callback fires in the poll phase. */
static void on_event(void *data, const char *filename, int is_rename) {
    watcher *w = (watcher *)data;
    void *cb = is_rename ? w->rename_cb : w->change_cb;
    if (w->open && cb)
        fire(cb, is_rename ? HSTR("rename") : HSTR("change"),
             __kml_str_from_cstr(filename ? filename : base_name(w->name)));
}

void *__kml_fs_watch(const char *path, int recursive) {
    watcher *w = mkwatcher(0, path);
    int err = 0;
    void *h = __kml_loop_fs_event_start(path, recursive, on_event, w, &err);
    if (!h) {
        free(w);
        errno = -err;
        __kml_fs_throw(HSTR("cannot watch path"), HSTR("watch"), path);
        return 0;
    }
    w->wd = (i64)(intptr_t)h;
    reg_add(w);
    return w;
}

void __kml_fswatch_close(void *wp) {
    watcher *w = (watcher *)wp;
    if (!w->open) return;
    __kml_loop_fs_event_stop((void *)(intptr_t)w->wd);
    w->open = 0;
    if (w->refd) nopen--;
}

void __kml_fswatch_set_ref(void *wp, int on) {
    watcher *w = (watcher *)wp;
    if (!w->open || w->refd == on) return;
    w->refd = on;
    nopen += on ? 1 : -1;
    __kml_loop_handle_ref((void *)(intptr_t)w->wd, on);
}

#endif

/* The natives lib/node/fs.ts's FSWatcher runs on. __kml_native_fs_watch(path,
 * flags, inv, clo) starts a watcher (flags: 1 recursive, 2 persistent: false)
 * whose every event calls inv(clo, isRename, 0) with the file name as the
 * last string, and returns the watcher's handle. */
typedef void (*native_inv)(void *clo, double kind, double zero);

static void tramp(void *env, void *ev, void *fname) {
    void **e = (void **)env;
    __kml_native_set_last_string((const char *)fname);
    ((native_inv)e[0])(e[1], *(char *)ev == 'r' ? 1.0 : 0.0, 0.0);
}

double __kml_native_fs_watch(const char *path, double flags, void *inv, void *clo) {
    int f = (int)flags;
    void *w = __kml_fs_watch(path, f & 1);
    void **env = (void **)malloc(2 * sizeof(void *));
    env[0] = inv;
    env[1] = clo;
    void **cl = (void **)malloc(2 * sizeof(void *));
    cl[0] = (void *)tramp;
    cl[1] = env;
    __kml_fswatch_on(w, 0, cl);
    __kml_fswatch_on(w, 1, cl);
    if (f & 2) __kml_fswatch_set_ref(w, 0);
    return (double)(unsigned long long)(uintptr_t)w;
}

void __kml_native_fs_watch_close(double h) {
    __kml_fswatch_close((void *)(uintptr_t)(unsigned long long)h);
}

void __kml_native_fs_watch_ref(double h, double on) {
    __kml_fswatch_set_ref((void *)(uintptr_t)(unsigned long long)h, on != 0);
}
