// klainfs.c — the fs module's natives, in the pool's translation unit: one
// blocking operation per libuv uv_fs_* request, run synchronously on the
// calling thread (fs.*Sync, as Node's sync binding calls do) or on a pool
// worker with the callback on the loop thread (the callback and promise
// forms, as libuv's uv_fs_* with a callback do).
//
// Every operation takes the same arguments: an op number, two paths, four
// numbers and a byte region. A result is a number (a descriptor, a byte
// count), a string read with nativeLastString() (readlink, realpath,
// mkdtemp, a recursive mkdir's first directory, readdir's entries), or bytes
// written into the region (stat's 18 doubles in Node's kFsStatsFieldsNumber
// order, statfs's 8). A failure is the negated errno (sync) or the errno
// argument of the callback.
//
// Compiled in only for a program that uses fs (-DKLAINPOOL_FS).

#ifdef KLAINPOOL_FS
#ifdef _WIN32
// win32fs.c's POSIX surface: Linux errno values, glibc x86-64 struct stat
// (with the birth time in the reserved tail) and a dirent carrying d_type.
typedef struct {
    uint64_t st_dev, st_ino, st_nlink;
    uint32_t st_mode, st_uid, st_gid, pad0;
    uint64_t st_rdev;
    int64_t st_size, st_blksize, st_blocks;
    int64_t atime_sec, atime_nsec, mtime_sec, mtime_nsec;
    int64_t ctime_sec, ctime_nsec, birth_sec, birth_nsec;
    int64_t reserved;
} kfs_stat;
typedef struct { uint32_t d_ino; uint16_t d_reclen; uint16_t d_namlen; uint8_t d_type; char d_name[1024]; } kfs_dirent;
int stat(const char *path, kfs_stat *st);
int lstat(const char *path, kfs_stat *st);
int fstat(int fd, kfs_stat *st);
void *opendir(const char *path);
kfs_dirent *readdir(void *dp);
int closedir(void *dp);
int mkdir(const char *path, int mode);
int rmdir(const char *path);
int rename(const char *from, const char *to);
int access(const char *path, int mode);
int chmod(const char *path, int mode);
int fchmod(int fd, int mode);
int truncate(const char *path, int64_t len);
int ftruncate(int fd, int64_t len);
char *realpath(const char *path, char *resolved);
int64_t readlink(const char *path, char *buf, size_t cap);
int symlink(const char *target, const char *path);
int link(const char *existing, const char *path);
char *mkdtemp(char *tmpl);
int utimes(const char *path, const void *times);
int futimes(int fd, const void *times);
int fsync(int fd);
int __kml_win_symlink_type(const char *type);
int __kml_win_copyfile(const char *src, const char *dst);
struct kfs_timeval { int64_t tv_sec; int64_t tv_usec; };
#define KFS_S_IFMT 0170000
#define KFS_S_IFDIR 0040000
#define KFS_S_IFLNK 0120000
#define kfs_errno_EEXIST 17
#define kfs_errno_ENOENT 2
#define kfs_errno_ENOTSUP 95
#define kfs_errno_ENOSYS 38
#define kfs_errno_EINVAL 22
#define kfs_errno_ENOMEM 12
#else
#include <sys/stat.h>
#include <sys/time.h>
#include <dirent.h>
#include <limits.h>
#ifdef __linux__
#include <sys/sysmacros.h>
#endif
typedef struct stat kfs_stat;
typedef struct dirent kfs_dirent;
#define KFS_S_IFMT S_IFMT
#define KFS_S_IFDIR S_IFDIR
#define KFS_S_IFLNK S_IFLNK
#define kfs_errno_EEXIST EEXIST
#define kfs_errno_ENOENT ENOENT
#define kfs_errno_ENOTSUP ENOTSUP
#define kfs_errno_ENOSYS ENOSYS
#define kfs_errno_EINVAL EINVAL
#define kfs_errno_ENOMEM ENOMEM
#endif

extern int __kml_fs_statfs(const char *path, int64_t *out);

// The op numbers lib/node/fs.ts passes (its FsOp).
enum {
    KFS_OPEN = 0, KFS_CLOSE, KFS_FSYNC, KFS_FDATASYNC, KFS_READ, KFS_WRITE,
    KFS_STAT, KFS_LSTAT, KFS_FSTAT, KFS_STATFS, KFS_MKDIR, KFS_MKDIRP,
    KFS_RMDIR, KFS_UNLINK, KFS_RENAME, KFS_LINK, KFS_SYMLINK, KFS_READLINK,
    KFS_REALPATH, KFS_MKDTEMP, KFS_CHMOD, KFS_FCHMOD, KFS_CHOWN, KFS_FCHOWN,
    KFS_LCHOWN, KFS_TRUNCATE, KFS_FTRUNCATE, KFS_ACCESS, KFS_UTIMES,
    KFS_FUTIMES, KFS_LUTIMES, KFS_COPYFILE, KFS_READDIR,
};

// libuv's uv_dirent_type_t, what Node's getDirents reads.
enum { KFS_DT_UNKNOWN = 0, KFS_DT_FILE, KFS_DT_DIR, KFS_DT_LINK, KFS_DT_FIFO, KFS_DT_SOCKET, KFS_DT_CHAR, KFS_DT_BLOCK };

static int64_t kfs_stat_sec(const kfs_stat *s, int which, int nsec) {
#if defined(_WIN32)
    switch (which) {
    case 0: return nsec ? s->atime_nsec : s->atime_sec;
    case 1: return nsec ? s->mtime_nsec : s->mtime_sec;
    case 2: return nsec ? s->ctime_nsec : s->ctime_sec;
    default: return nsec ? s->birth_nsec : s->birth_sec;
    }
#elif defined(__APPLE__)
    const struct timespec *t = which == 0 ? &s->st_atimespec : which == 1 ? &s->st_mtimespec
                             : which == 2 ? &s->st_ctimespec : &s->st_birthtimespec;
    return nsec ? (int64_t)t->tv_nsec : (int64_t)t->tv_sec;
#else
    // Linux's stat has no birth time; libuv reports statx's, or the ctime
    // when the file system has none.
    const struct timespec *t = which == 0 ? &s->st_atim : which == 1 ? &s->st_mtim : &s->st_ctim;
    return nsec ? (int64_t)t->tv_nsec : (int64_t)t->tv_sec;
#endif
}

// Node's stat array: dev mode nlink uid gid rdev blksize ino size blocks,
// then (sec, nsec) of atime mtime ctime birthtime.
static void kfs_fill_stat(const kfs_stat *s, void *out, int64_t size) {
    if (size < 18 * 8) return;
    double v[18];
    v[0] = (double)s->st_dev;
    v[1] = (double)s->st_mode;
    v[2] = (double)s->st_nlink;
    v[3] = (double)s->st_uid;
    v[4] = (double)s->st_gid;
    v[5] = (double)s->st_rdev;
    v[6] = (double)s->st_blksize;
    v[7] = (double)s->st_ino;
    v[8] = (double)s->st_size;
    v[9] = (double)s->st_blocks;
    for (int i = 0; i < 4; i++) {
        v[10 + 2 * i] = (double)kfs_stat_sec(s, i, 0);
        v[11 + 2 * i] = (double)kfs_stat_sec(s, i, 1);
    }
    memcpy(out, v, sizeof v);
}

// A string result the item owns.
static void kfs_result_str(kml_pool_item *it, const char *s) {
    free(it->res_str);
    it->res_str = strdup(s ? s : "");
}

#ifndef _WIN32
static int kfs_set_times(kml_pool_item *it, struct timeval tv[2]) {
    for (int i = 0; i < 2; i++) {
        double t = i == 0 ? it->d[0] : it->d[1];
        double sec = (double)(int64_t)t;
        if (sec > t) sec -= 1;
        tv[i].tv_sec = (time_t)sec;
        tv[i].tv_usec = (suseconds_t)((t - sec) * 1e6);
    }
    return 0;
}
#else
static void kfs_set_times(kml_pool_item *it, struct kfs_timeval tv[2]) {
    for (int i = 0; i < 2; i++) {
        double t = i == 0 ? it->d[0] : it->d[1];
        double sec = (double)(int64_t)t;
        if (sec > t) sec -= 1;
        tv[i].tv_sec = (int64_t)sec;
        tv[i].tv_usec = (int64_t)((t - sec) * 1e6);
    }
}
#endif

// A recursive mkdir (Node's MKDirpSync): the first directory it creates, or
// none when the path already is one.
static int kfs_mkdirp(const char *path, int mode, char **first) {
    if (mkdir(path, mode) == 0) {
        if (!*first) *first = strdup(path);
        return 0;
    }
    int e = errno;
    if (e == kfs_errno_EEXIST) {
        kfs_stat st;
        if (stat(path, &st) == 0 && (st.st_mode & KFS_S_IFMT) == KFS_S_IFDIR) return 0;
        return kfs_errno_EEXIST;
    }
    if (e != kfs_errno_ENOENT) return e;
    // The parent: the path without its last component.
    size_t n = strlen(path);
    while (n > 1 && (path[n - 1] == '/' || path[n - 1] == '\\')) n--;
    while (n > 0 && path[n - 1] != '/' && path[n - 1] != '\\') n--;
    while (n > 1 && (path[n - 1] == '/' || path[n - 1] == '\\')) n--;
    if (n == 0 || (n == 1 && (path[0] == '/' || path[0] == '\\'))) return e;
#ifdef _WIN32
    if (n == 2 && path[1] == ':') return e;
#endif
    char *parent = (char *)malloc(n + 1);
    memcpy(parent, path, n);
    parent[n] = 0;
    int r = kfs_mkdirp(parent, mode, first);
    free(parent);
    if (r != 0) return r;
    if (mkdir(path, mode) == 0) {
        if (!*first) *first = strdup(path);
        return 0;
    }
    e = errno;
    if (e == kfs_errno_EEXIST) {
        kfs_stat st;
        if (stat(path, &st) == 0 && (st.st_mode & KFS_S_IFMT) == KFS_S_IFDIR) return 0;
    }
    return e;
}

static int kfs_copyfile(const char *src, const char *dst, int mode) {
    // COPYFILE_FICLONE_FORCE: no copy-on-write clone here.
    if (mode & 4) return kfs_errno_ENOTSUP;
#ifdef _WIN32
    if (mode & 1) {
        kfs_stat st;
        if (stat(dst, &st) == 0) return kfs_errno_EEXIST;
    }
    return __kml_win_copyfile(src, dst) == 0 ? 0 : errno;
#else
    int in = open(src, O_RDONLY | O_CLOEXEC);
    if (in < 0) return errno;
    struct stat st;
    if (fstat(in, &st) != 0) {
        int e = errno;
        close(in);
        return e;
    }
    int out = open(dst, O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC | ((mode & 1) ? O_EXCL : 0), st.st_mode & 07777);
    if (out < 0) {
        int e = errno;
        close(in);
        return e;
    }
    // libuv makes the destination's mode the source's.
    int e = 0;
    if (fchmod(out, st.st_mode & 07777) != 0) e = errno;
    char buf[64 * 1024];
    while (e == 0) {
        ssize_t n = read(in, buf, sizeof buf);
        if (n < 0) {
            if (errno == EINTR) continue;
            e = errno;
            break;
        }
        if (n == 0) break;
        for (ssize_t off = 0; off < n;) {
            ssize_t w = write(out, buf + off, (size_t)(n - off));
            if (w < 0) {
                if (errno == EINTR) continue;
                e = errno;
                break;
            }
            off += w;
        }
    }
    close(in);
    if (close(out) != 0 && e == 0) e = errno;
    if (e != 0) unlink(dst);
    return e;
#endif
}

typedef struct { char *name; int type; } kfs_entry;

static int kfs_entry_cmp(const void *a, const void *b) {
    return strcmp(((const kfs_entry *)a)->name, ((const kfs_entry *)b)->name);
}

// readdir's result: each entry as its type digit and its name, joined by
// '/', which no file name contains. libuv's scandir sorts them (strcmp).
static int kfs_readdir(kml_pool_item *it) {
    void *d = opendir(it->arg0);
    if (!d) return errno;
    size_t n = 0, cap = 16;
    kfs_entry *ents = (kfs_entry *)malloc(cap * sizeof *ents);
    for (;;) {
        errno = 0;
        kfs_dirent *de = readdir(d);
        if (!de) break;
        const char *nm = de->d_name;
        if (strcmp(nm, ".") == 0 || strcmp(nm, "..") == 0) continue;
        int t = KFS_DT_UNKNOWN;
        switch (de->d_type) {
        case 8: t = KFS_DT_FILE; break;
        case 4: t = KFS_DT_DIR; break;
        case 10: t = KFS_DT_LINK; break;
        case 1: t = KFS_DT_FIFO; break;
        case 12: t = KFS_DT_SOCKET; break;
        case 2: t = KFS_DT_CHAR; break;
        case 6: t = KFS_DT_BLOCK; break;
        }
        if (n == cap) {
            cap *= 2;
            ents = (kfs_entry *)realloc(ents, cap * sizeof *ents);
        }
        ents[n].name = strdup(nm);
        ents[n].type = t;
        n++;
    }
    closedir(d);
#ifndef _WIN32
    qsort(ents, n, sizeof *ents, kfs_entry_cmp);
#endif
    size_t len = 1;
    for (size_t i = 0; i < n; i++) len += strlen(ents[i].name) + 2;
    char *out = (char *)malloc(len);
    size_t o = 0;
    for (size_t i = 0; i < n; i++) {
        if (i > 0) out[o++] = '/';
        out[o++] = (char)('0' + ents[i].type);
        size_t l = strlen(ents[i].name);
        memcpy(out + o, ents[i].name, l);
        o += l;
        free(ents[i].name);
    }
    out[o] = 0;
    free(ents);
    free(it->res_str);
    it->res_str = out;
    return 0;
}

// The operation: d[0..3] are its numbers, arg0/arg1 its paths, buf/buflen
// its region. Sets err (an errno) or res/res_str.
static void work_fs(kml_pool_item *it) {
    int fd = (int)it->d[0];
    int e = 0;
    switch ((int)it->a[3]) {
    case KFS_OPEN: {
#ifdef O_CLOEXEC
        int r = open(it->arg0, (int)it->d[0] | O_CLOEXEC, (int)it->d[1]);
#else
        int r = open(it->arg0, (int)it->d[0], (int)it->d[1]);
#endif
        if (r < 0) e = errno; else it->res = r;
        break;
    }
    case KFS_CLOSE:
        if (close(fd) != 0) e = errno;
        break;
    case KFS_FSYNC:
        if (kml_fsync(fd) != 0) e = errno;
        break;
    case KFS_FDATASYNC:
#if defined(__linux__)
        if (fdatasync(fd) != 0) e = errno;
#else
        if (kml_fsync(fd) != 0) e = errno;
#endif
        break;
    case KFS_READ:
    case KFS_WRITE: {
        // d[1] offset, d[2] length, d[3] position (< 0: the current one).
        int64_t off = (int64_t)it->d[1], len = (int64_t)it->d[2];
        if (off < 0) off = 0;
        if (off > it->buflen) off = it->buflen;
        if (len < 0) len = 0;
        if (len > it->buflen - off) len = it->buflen - off;
        char *p = (char *)it->buf + off;
        int64_t pos = it->d[3] >= 0 ? (int64_t)it->d[3] : -1;
        int64_t n;
        do {
            if ((int)it->a[3] == KFS_READ)
                n = pos < 0 ? read(fd, p, (size_t)len) : kml_pread(fd, p, (size_t)len, pos);
            else
                n = pos < 0 ? write(fd, p, (size_t)len) : kml_pwrite(fd, p, (size_t)len, pos);
        } while (n < 0 && errno == EINTR);
        if (n < 0) e = errno; else it->res = (double)n;
        break;
    }
    case KFS_STAT:
    case KFS_LSTAT:
    case KFS_FSTAT: {
#if defined(__linux__) && defined(STATX_BTIME)
        // libuv's uv__fs_statx: the birth time too, where the file system
        // records one (else the ctime, as libuv falls back).
        {
            struct statx sx;
            int op = (int)it->a[3];
            int r = op == KFS_FSTAT ? statx(fd, "", AT_EMPTY_PATH, STATX_BASIC_STATS | STATX_BTIME, &sx)
                  : statx(AT_FDCWD, it->arg0, op == KFS_LSTAT ? AT_SYMLINK_NOFOLLOW : 0, STATX_BASIC_STATS | STATX_BTIME, &sx);
            if (r == 0) {
                if (it->buflen >= 18 * 8) {
                    double v[18];
                    v[0] = (double)makedev(sx.stx_dev_major, sx.stx_dev_minor);
                    v[1] = (double)sx.stx_mode;
                    v[2] = (double)sx.stx_nlink;
                    v[3] = (double)sx.stx_uid;
                    v[4] = (double)sx.stx_gid;
                    v[5] = (double)makedev(sx.stx_rdev_major, sx.stx_rdev_minor);
                    v[6] = (double)sx.stx_blksize;
                    v[7] = (double)sx.stx_ino;
                    v[8] = (double)sx.stx_size;
                    v[9] = (double)sx.stx_blocks;
                    const struct statx_timestamp *b = (sx.stx_mask & STATX_BTIME) ? &sx.stx_btime : &sx.stx_ctime;
                    const struct statx_timestamp *ts[4] = { &sx.stx_atime, &sx.stx_mtime, &sx.stx_ctime, b };
                    for (int i = 0; i < 4; i++) {
                        v[10 + 2 * i] = (double)ts[i]->tv_sec;
                        v[11 + 2 * i] = (double)ts[i]->tv_nsec;
                    }
                    memcpy(it->buf, v, sizeof v);
                }
                break;
            }
            if (errno != ENOSYS && errno != EPERM) {
                e = errno;
                break;
            }
        }
#endif
        kfs_stat st;
        int r = (int)it->a[3] == KFS_STAT ? stat(it->arg0, &st)
              : (int)it->a[3] == KFS_LSTAT ? lstat(it->arg0, &st) : fstat(fd, &st);
        if (r != 0) e = errno; else kfs_fill_stat(&st, it->buf, it->buflen);
        break;
    }
    case KFS_STATFS: {
        int64_t v[8];
        if (__kml_fs_statfs(it->arg0, v) != 0) {
            e = errno;
        } else if (it->buflen >= 8 * 8) {
            // Node's StatFs: type bsize frsize blocks bfree bavail files ffree.
            double o[8];
            for (int i = 0; i < 8; i++) o[i] = (double)v[i];
            memcpy(it->buf, o, sizeof o);
        }
        break;
    }
    case KFS_MKDIR:
        if (mkdir(it->arg0, (int)it->d[0]) != 0) e = errno;
        break;
    case KFS_MKDIRP: {
        char *first = NULL;
        e = kfs_mkdirp(it->arg0, (int)it->d[0], &first);
        // res 1: a directory was created (its path is the string).
        if (e == 0 && first) {
            it->res = 1;
            free(it->res_str);
            it->res_str = first;
        } else {
            free(first);
        }
        break;
    }
    case KFS_RMDIR:
        if (rmdir(it->arg0) != 0) e = errno;
        break;
    case KFS_UNLINK:
        if (unlink(it->arg0) != 0) e = errno;
        break;
    case KFS_RENAME:
        if (rename(it->arg0, it->arg1) != 0) e = errno;
        break;
    case KFS_LINK:
        if (link(it->arg0, it->arg1) != 0) e = errno;
        break;
    case KFS_SYMLINK:
#ifdef _WIN32
        // d[0]: 0 auto, 1 file, 2 dir, 3 junction.
        __kml_win_symlink_type(it->d[0] == 1 ? "file" : it->d[0] == 2 ? "dir" : it->d[0] == 3 ? "junction" : NULL);
#endif
        if (symlink(it->arg0, it->arg1) != 0) e = errno;
        break;
    case KFS_READLINK: {
        size_t cap = 256;
        for (;;) {
            char *b = (char *)malloc(cap);
            int64_t n = readlink(it->arg0, b, cap);
            if (n < 0) {
                e = errno;
                free(b);
                break;
            }
            if ((size_t)n < cap) {
                b[n] = 0;
                free(it->res_str);
                it->res_str = b;
                break;
            }
            free(b);
            cap *= 2;
        }
        break;
    }
    case KFS_REALPATH: {
        char *r = realpath(it->arg0, NULL);
        if (!r) {
            e = errno;
        } else {
            free(it->res_str);
            it->res_str = r;
        }
        break;
    }
    case KFS_MKDTEMP: {
        // arg0 is the template, ending in XXXXXX.
        char *t = strdup(it->arg0);
        if (!mkdtemp(t)) {
            e = errno;
            free(t);
        } else {
            free(it->res_str);
            it->res_str = t;
        }
        break;
    }
    case KFS_CHMOD:
        if (chmod(it->arg0, (int)it->d[0]) != 0) e = errno;
        break;
    case KFS_FCHMOD:
        if (fchmod(fd, (int)it->d[1]) != 0) e = errno;
        break;
    case KFS_CHOWN:
    case KFS_FCHOWN:
    case KFS_LCHOWN:
#ifndef _WIN32
        {
            int r = (int)it->a[3] == KFS_CHOWN ? chown(it->arg0, (uid_t)it->d[0], (gid_t)it->d[1])
                  : (int)it->a[3] == KFS_LCHOWN ? lchown(it->arg0, (uid_t)it->d[0], (gid_t)it->d[1])
                  : fchown(fd, (uid_t)it->d[1], (gid_t)it->d[2]);
            if (r != 0) e = errno;
        }
#endif
        // libuv's chown family does nothing on Windows.
        break;
    case KFS_TRUNCATE:
        if (truncate(it->arg0, (int64_t)it->d[0]) != 0) e = errno;
        break;
    case KFS_FTRUNCATE:
        if (ftruncate(fd, (int64_t)it->d[1]) != 0) e = errno;
        break;
    case KFS_ACCESS:
        if (access(it->arg0, (int)it->d[0]) != 0) e = errno;
        break;
    case KFS_UTIMES:
    case KFS_LUTIMES: {
#ifdef _WIN32
        struct kfs_timeval tv[2];
        kfs_set_times(it, tv);
        if ((int)it->a[3] == KFS_LUTIMES) {
            kfs_stat st;
            if (lstat(it->arg0, &st) == 0 && (st.st_mode & KFS_S_IFMT) == KFS_S_IFLNK) { e = kfs_errno_ENOSYS; break; }
        }
        if (utimes(it->arg0, tv) != 0) e = errno;
#else
        struct timeval tv[2];
        kfs_set_times(it, tv);
        if (((int)it->a[3] == KFS_LUTIMES ? lutimes(it->arg0, tv) : utimes(it->arg0, tv)) != 0) e = errno;
#endif
        break;
    }
    case KFS_FUTIMES: {
        // d[0] fd; the times follow.
        double at = it->d[1], mt = it->d[2];
        it->d[0] = at;
        it->d[1] = mt;
#ifdef _WIN32
        struct kfs_timeval tv[2];
#else
        struct timeval tv[2];
#endif
        kfs_set_times(it, tv);
        if (futimes(fd, tv) != 0) e = errno;
        break;
    }
    case KFS_COPYFILE:
        e = kfs_copyfile(it->arg0, it->arg1, (int)it->d[0]);
        break;
    case KFS_READDIR:
        e = kfs_readdir(it);
        break;
    default:
        e = kfs_errno_EINVAL;
    }
    it->err = e;
}

static void kfs_setup(kml_pool_item *it, double op, const char *p1, const char *p2,
                      double a, double b, double c, double d, void *data, int64_t size) {
    it->a[3] = (int64_t)op;
    it->arg0 = strdup(p1 ? p1 : "");
    it->arg1 = strdup(p2 ? p2 : "");
    it->d[0] = a;
    it->d[1] = b;
    it->d[2] = c;
    it->d[3] = d;
    it->buf = data;
    it->buflen = size;
}

// The synchronous form: the result, or the negated errno. A string result
// is the last string.
double __kml_native_fs_call(double op, const char *p1, const char *p2, double a, double b, double c, double d,
                            void *data, int64_t size) {
    kml_pool_item it;
    memset(&it, 0, sizeof it);
    kfs_setup(&it, op, p1, p2, a, b, c, d, data, size);
    work_fs(&it);
    native_set_last_string(it.res_str);
    free(it.arg0);
    free(it.arg1);
    free(it.res_str);
    return it.err ? -(double)it.err : it.res;
}

// The pooled form: callback(errno or 0, result) on the loop thread, the
// string result as the last string. The region stays reachable through the
// callback's closure.
void __kml_native_fs_call_async(double op, const char *p1, const char *p2, double a, double b, double c, double d,
                                void *data, int64_t size, void *inv, void *clo) {
    kml_pool_item *it = native_item(work_fs, inv, clo);
    kfs_setup(it, op, p1, p2, a, b, c, d, data, size);
    native_submit(it);
}
#endif
