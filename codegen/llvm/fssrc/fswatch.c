/* fswatch.c — fs.watch (TDD-00181; in C per TDD-00240). On POSIX each
 * FSWatcher is a libuv uv_fs_event_t on the event loop (loop.c, TDD-00244
 * Stage 4, TDD-00252 on Windows): inotify on Linux, FSEvents or kqueue on
 * macOS, ReadDirectoryChangesW on Windows, its listener fired from libuv's
 * callback. libuv watches a tree only on macOS and Windows; on Linux a
 * recursive watcher adds a handle per directory below the root, as Node's
 * lib/internal/fs/recursive_watch does there, reporting names relative to
 * the root. */
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>

typedef long long i64;

#define HSTR(s) ({ static const struct { long long n; char b[sizeof(s)]; } \
    __attribute__((aligned(8))) h_ = { sizeof(s) - 1, s }; (void *)h_.b; })

extern void __kml_fs_throw(void *opdesc, const char *syscall, const char *path);
extern int __kml_errno_from_uv(int uv);
extern void *__kml_str_from_cstr(const char *s);
extern void __kml_native_set_last_string(const char *s);
typedef void (*kml_fs_event_cb)(void *data, const char *filename, int is_rename);
extern void *__kml_loop_fs_event_start(const char *path, int recursive, kml_fs_event_cb cb, void *data, int *err);
extern void __kml_loop_fs_event_stop(void *h);
extern void __kml_loop_handle_ref(void *h, int on);

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
#ifdef __linux__
    struct sub **subs; /* the directories below the root, recursive only */
    i64 nsubs, subs_cap;
    int recursive;
#endif
} watcher;

typedef void (*listener_fn)(void *env, void *ev, void *fname);

#ifdef __linux__
static void root_dir_added(watcher *w, const char *filename);
#endif

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
#ifdef __linux__
    w->subs = 0;
    w->nsubs = w->subs_cap = 0;
    w->recursive = 0;
#endif
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


void __kml_fswatch_on(void *wp, i64 is_rename, void *cb) {
    watcher *w = (watcher *)wp;
    if (is_rename) w->rename_cb = cb;
    else w->change_cb = cb;
}


/* The base name of a path, as libuv reports a watched file's own event. */
static const char *base_name(const char *p) {
    const char *b = strrchr(p, '/');
    return b ? b + 1 : p;
}

/* Inside the wait, as Node's FSEvent callback fires in the poll phase. */
static void on_event(void *data, const char *filename, int is_rename) {
    watcher *w = (watcher *)data;
#ifdef __linux__
    if (w->open && is_rename)
        root_dir_added(w, filename);
#endif
    void *cb = is_rename ? w->rename_cb : w->change_cb;
    if (w->open && cb)
        fire(cb, is_rename ? HSTR("rename") : HSTR("change"),
             __kml_str_from_cstr(filename ? filename : base_name(w->name)));
}

#ifdef __linux__
#include <dirent.h>
#include <stdio.h>
#include <sys/stat.h>

/* A directory below a recursive watcher's root: rel is its path relative to
 * the root ("a/b"), h its libuv handle. */
struct sub {
    watcher *w;
    char *rel;
    void *h;
};

static void watch_tree(watcher *w, const char *rel);

static char *join(const char *a, const char *b) {
    size_t la = strlen(a), lb = strlen(b);
    char *r = (char *)malloc(la + lb + 2);
    memcpy(r, a, la);
    r[la] = '/';
    memcpy(r + la + 1, b, lb + 1);
    return r;
}

static int is_dir(const char *p) {
    struct stat st;
    return lstat(p, &st) == 0 && S_ISDIR(st.st_mode);
}

static int watching(watcher *w, const char *rel) {
    for (i64 i = 0; i < w->nsubs; i++)
        if (w->subs[i] && strcmp(w->subs[i]->rel, rel) == 0)
            return 1;
    return 0;
}

/* An event in a directory below the root: the name joined to the
 * directory's relative path, and a directory that appeared is watched. */
static void on_sub_event(void *data, const char *filename, int is_rename) {
    struct sub *s = (struct sub *)data;
    watcher *w = s->w;
    if (!w->open || !filename)
        return;
    char *rel = join(s->rel, filename);
    if (is_rename) {
        char *abs = join(w->name, rel);
        if (is_dir(abs) && !watching(w, rel))
            watch_tree(w, rel);
        free(abs);
    }
    void *cb = is_rename ? w->rename_cb : w->change_cb;
    if (cb)
        fire(cb, is_rename ? HSTR("rename") : HSTR("change"), __kml_str_from_cstr(rel));
    free(rel);
}

static void sub_add(watcher *w, struct sub *s) {
    if (w->nsubs + 1 > w->subs_cap) {
        i64 nc = w->subs_cap * 2 > 8 ? w->subs_cap * 2 : 8;
        w->subs = (struct sub **)realloc(w->subs, (size_t)nc * sizeof(void *));
        w->subs_cap = nc;
    }
    w->subs[w->nsubs++] = s;
}

/* Watches the directory rel (relative to the root) and every directory
 * below it; symbolic links are not followed, as Node's walk does not. */
static void watch_tree(watcher *w, const char *rel) {
    char *abs = join(w->name, rel);
    struct sub *s = (struct sub *)malloc(sizeof *s);
    s->w = w;
    s->rel = strdup(rel);
    int err = 0;
    s->h = __kml_loop_fs_event_start(abs, 0, on_sub_event, s, &err);
    if (!s->h) {
        free(s->rel);
        free(s);
        free(abs);
        return;
    }
    if (!w->refd)
        __kml_loop_handle_ref(s->h, 0);
    sub_add(w, s);
    DIR *d = opendir(abs);
    if (d) {
        struct dirent *e;
        while ((e = readdir(d))) {
            if (!strcmp(e->d_name, ".") || !strcmp(e->d_name, ".."))
                continue;
            char *ca = join(abs, e->d_name);
            if (is_dir(ca)) {
                char *cr = join(rel, e->d_name);
                watch_tree(w, cr);
                free(cr);
            }
            free(ca);
        }
        closedir(d);
    }
    free(abs);
}

/* The root's own events: a directory that appeared directly under it is
 * watched with its subtree. */
static void root_dir_added(watcher *w, const char *filename) {
    if (!w->recursive || !filename)
        return;
    char *abs = join(w->name, filename);
    if (is_dir(abs) && !watching(w, filename))
        watch_tree(w, filename);
    free(abs);
}

static void watch_children(watcher *w) {
    DIR *d = opendir(w->name);
    if (!d)
        return;
    struct dirent *e;
    while ((e = readdir(d))) {
        if (!strcmp(e->d_name, ".") || !strcmp(e->d_name, ".."))
            continue;
        char *ca = join(w->name, e->d_name);
        if (is_dir(ca))
            watch_tree(w, e->d_name);
        free(ca);
    }
    closedir(d);
}
#endif

void *__kml_fs_watch(const char *path, int recursive) {
    watcher *w = mkwatcher(0, path);
    int err = 0;
    void *h = __kml_loop_fs_event_start(path, recursive, on_event, w, &err);
    if (!h) {
        free(w);
        /* libuv's number: -errno on POSIX, its own table on Windows (-4058) */
        errno = __kml_errno_from_uv(err);
        __kml_fs_throw(HSTR("cannot watch path"), HSTR("watch"), path);
        return 0;
    }
    w->wd = (i64)(intptr_t)h;
    reg_add(w);
#ifdef __linux__
    w->recursive = recursive;
    if (recursive)
        watch_children(w);
#endif
    return w;
}

void __kml_fswatch_close(void *wp) {
    watcher *w = (watcher *)wp;
    if (!w->open) return;
    __kml_loop_fs_event_stop((void *)(intptr_t)w->wd);
#ifdef __linux__
    for (i64 i = 0; i < w->nsubs; i++) {
        __kml_loop_fs_event_stop(w->subs[i]->h);
        free(w->subs[i]->rel);
        free(w->subs[i]);
    }
    w->nsubs = 0;
#endif
    w->open = 0;
    if (w->refd) nopen--;
}

void __kml_fswatch_set_ref(void *wp, int on) {
    watcher *w = (watcher *)wp;
    if (!w->open || w->refd == on) return;
    w->refd = on;
    nopen += on ? 1 : -1;
    __kml_loop_handle_ref((void *)(intptr_t)w->wd, on);
#ifdef __linux__
    for (i64 i = 0; i < w->nsubs; i++)
        __kml_loop_handle_ref(w->subs[i]->h, on);
#endif
}


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
