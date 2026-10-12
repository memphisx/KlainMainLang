/* process.c — the process runtime: lifecycle hooks (exit, uncaughtException,
 * unhandledRejection), uptime/hrtime, cwd/chdir/umask, credentials, argv,
 * exec path, env, kill, memory numbers and the signal watchers behind
 * lib/node/internal_process.ts (in C per TDD-00240). A failing call returns
 * -errno. Compiled the same way for every program (the link drops what a
 * program does not use); ProcessCFlags passes the NaN-box constants
 * KML_NB_UNDEFINED and KML_NB_DOUBLE_OFFSET. */
#if defined(_WIN32)
/* The shim (win32fs.c, win32shim.c) defines the POSIX names below with its
 * own signatures: no CRT old-name aliases, no <time.h>/<sys/stat.h>. */
#define NO_OLDNAMES 1
#endif
#include <errno.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#if !defined(_WIN32)
#include <sys/stat.h>
#include <time.h>
#endif

typedef long long i64;

#if defined(_WIN32)
typedef struct { i64 tv_sec, tv_nsec; } kml_ts; /* the shim's own layout, not mingw's */
int clock_gettime(int clk, kml_ts *ts);
int kill(int pid, int sig);
int setenv(const char *name, const char *value, int overwrite);
int unsetenv(const char *name);
int chdir(const char *path);
char *getcwd(char *buf, size_t size);
int getpid(void);
i64 readlink(const char *path, char *buf, size_t cap);
void *signal(int sig, void *handler);
int _umask(int mode);
#define UMASK _umask
#else
#include <signal.h>
#include <unistd.h>
typedef struct timespec kml_ts;
#define UMASK umask
#endif
#if defined(__APPLE__)
#include <mach-o/dyld.h>
#define KML_MONO_CLOCK 8 /* CLOCK_UPTIME_RAW: ns resolution */
#else
#define KML_MONO_CLOCK 1
#endif

extern void *__kml_str_from_cstr(const char *s);
extern i64 __kml_current_rss_bytes(void);
extern i64 __kml_heap_total_bytes(void);
extern i64 __kml_heap_used_bytes(void);
/* The worker runtime replaces this in a program with Worker threads: exit
 * then ends the calling thread, the process only on the main one. */
__attribute__((weak, noreturn)) void __kml_thread_exit(int code) { exit(code); }
/* The program's argv, stored by main. */
extern void *__argv_ptr;
extern void *__process_argv_ptr;
extern i64 __process_argv_len;

/* ---- hooks: 0 'exit', 1 'uncaughtException', 2 'unhandledRejection' ---- */
typedef void (*hook_fn)(void *clo, i64 a, i64 b);
static _Thread_local hook_fn phook_inv[4];
static _Thread_local void *phook_clo[4];
_Thread_local i64 __kml_process_exit_code;
static _Thread_local unsigned char exit_ran;

_Bool __kml_process_hook_call(int which, i64 a, i64 b) {
    hook_fn inv = phook_inv[(unsigned)which];
    if (!inv) return 0;
    inv(phook_clo[(unsigned)which], a, b);
    return 1;
}

void __kml_native_process_hook(double which, void *inv, void *clo) {
    i64 i = (i64)which;
    phook_clo[i] = clo;
    phook_inv[i] = (hook_fn)inv;
}

void __kml_native_process_unhook(double which) { phook_inv[(i64)which] = 0; }

/* The process 'exit' event, once. */
void __kml_run_exit_handlers(i64 code) {
    if (exit_ran) return;
    exit_ran = 1;
    double d = (double)code;
    i64 bits;
    memcpy(&bits, &d, sizeof bits);
    __kml_process_hook_call(0, bits + KML_NB_DOUBLE_OFFSET, KML_NB_UNDEFINED);
}

void __kml_native_process_set_exit_code(double code) { __kml_process_exit_code = (i64)code; }
double __kml_native_process_get_exit_code(void) { return (double)__kml_process_exit_code; }

void __kml_native_process_really_exit(double code) {
    __kml_process_exit_code = (i64)code;
    __kml_run_exit_handlers(__kml_process_exit_code);
    int final = (int)__kml_process_exit_code;
    __kml_thread_exit(final);
}

/* ---- time ---- */
static i64 proc_start_ns;

static i64 mono_ns(void) {
    kml_ts ts;
    clock_gettime(KML_MONO_CLOCK, &ts);
    return (i64)ts.tv_sec * 1000000000 + (i64)ts.tv_nsec;
}

void __kml_proc_uptime_init(void) { proc_start_ns = mono_ns(); }

double __kml_process_uptime(void) { return (double)(mono_ns() - proc_start_ns) / 1000000000.0; }

double __kml_native_process_uptime(void) { return __kml_process_uptime(); }

/* hrtime fills a shared buffer (seconds, nanoseconds), read back by index. */
static i64 hrtime_buf[2];

void __kml_native_process_hrtime(void) {
    kml_ts ts;
    clock_gettime(KML_MONO_CLOCK, &ts);
    hrtime_buf[0] = (i64)ts.tv_sec;
    hrtime_buf[1] = (i64)ts.tv_nsec;
}

double __kml_native_process_hrtime_read(double which) { return (double)hrtime_buf[(int)which & 1]; }

/* ---- cwd, chdir, umask ---- */
void *__kml_process_cwd(void) {
    char *r = getcwd(NULL, 0);
    void *s = __kml_str_from_cstr(r);
    free(r);
    return s;
}

void *__kml_native_process_cwd(void) { return __kml_process_cwd(); }

double __kml_native_process_chdir(const char *path) {
    if (chdir(path) != 0) return (double)-errno;
    return 0.0;
}

double __kml_native_process_umask(double mask) {
    if (mask < 0) {
        int old = (int)UMASK(0);
        UMASK(old);
        return (double)(unsigned)old;
    }
    return (double)(unsigned)UMASK((int)mask);
}

/* ---- ids, kill, argv, exec path, version ---- */
i64 __kml_getpid(void) { return (i64)getpid(); }

double __kml_native_kill_pid(double pid, double sig) {
    if (kill((int)pid, (int)sig) != 0) return (double)-errno;
    return 0.0;
}

/* 0 pid, 1 ppid, 2 uid, 3 euid, 4 gid, 5 egid; Windows has only the pid. */
double __kml_native_process_id(double which) {
#if defined(_WIN32)
    return (int)which == 0 ? (double)__kml_getpid() : -1.0;
#else
    switch ((int)which) {
    case 1: return (double)getppid();
    case 2: return (double)getuid();
    case 3: return (double)geteuid();
    case 4: return (double)getgid();
    case 5: return (double)getegid();
    default: return (double)__kml_getpid();
    }
#endif
}

double __kml_native_process_argc(void) { return (double)__process_argv_len; }

void *__kml_native_process_argv0(void) { return *(void **)__argv_ptr; }

void *__kml_native_process_argv(double i) { return ((void **)__process_argv_ptr)[(i64)i]; }

/* The absolute, symlink-resolved path of the running executable. */
char *__kml_execpath(void) {
#if defined(__APPLE__)
    uint32_t size = 4096;
    char *buf = (char *)malloc(4096);
    _NSGetExecutablePath(buf, &size);
    char *res = realpath(buf, NULL);
    if (!res) return buf;
    free(buf);
    return res;
#else
    char *buf = (char *)malloc(4097);
    i64 n = (i64)readlink("/proc/self/exe", buf, 4096);
    buf[n < 0 ? 0 : n] = 0;
    return buf;
#endif
}

void *__kml_native_process_exec_path(void) { return __kml_str_from_cstr(__kml_execpath()); }

_Bool __kml_native_env_get(const char *key, void **out) {
    const char *raw = getenv(key);
    if (!raw) return 0;
    *out = __kml_str_from_cstr(raw);
    return 1;
}

void __kml_native_env_set(const char *key, const char *value) { setenv(key, value, 1); }

void __kml_native_env_delete(const char *key) { unsetenv(key); }

/* 0 rss, 1 heapTotal, 2 heapUsed */
double __kml_native_process_memory(double which) {
    switch ((int)which) {
    case 1: return (double)(unsigned long long)__kml_heap_total_bytes();
    case 2: return (double)(unsigned long long)__kml_heap_used_bytes();
    default: return (double)(unsigned long long)__kml_current_rss_bytes();
    }
}

/* ---- signals ----
 * On POSIX each watched signal is a uv_signal_t on the event loop
 * (loop.c), whose callback runs the watcher inside the wait, as Node's
 * does. Windows keeps signal() until its loop moves to libuv (TDD-00244
 * Stage 6): the handler only sets a flag the loop's next pass dispatches
 * (__kml_signal_dispatch). */
typedef void (*sig_watch_fn)(void *clo, double signo);
#ifdef _WIN32
static _Thread_local volatile unsigned char sig_pending[65];
#endif
static _Thread_local sig_watch_fn sig_inv[65];
static _Thread_local void *sig_clo[65];

static void *sig_install(int n, void *h) {
#if defined(_WIN32)
    return signal(n, h);
#else
    return (void *)signal(n, (void (*)(int))h);
#endif
}

void __kml_signal_note(int signum) {
#ifdef _WIN32
    if ((unsigned)signum < 65) sig_pending[signum] = 1;
#else
    if ((unsigned)signum < 65 && sig_inv[signum]) sig_inv[signum](sig_clo[signum], (double)signum);
#endif
}

#ifdef _WIN32
static void sig_handler(int signum) { __kml_signal_note(signum); }
#else
int __kml_loop_signal_start(int n);
void __kml_loop_signal_stop(int n);
#endif

#ifdef _WIN32
void __kml_signal_dispatch(void) {
    for (int i = 1; i < 65; i++) {
        if (!sig_pending[i]) continue;
        sig_pending[i] = 0;
        if (sig_inv[i]) sig_inv[i](sig_clo[i], (double)i);
    }
}
#endif

/* A client dropping mid-write unwinds via write() EPIPE, not a kill. */
void __kml_ignore_sigpipe(void) { sig_install(13, (void *)1); }

/* Starting watches the signal; -EINVAL for a number or signal the host
 * refuses. */
double __kml_native_signal_start(double signo, void *inv, void *clo) {
    int n = (int)signo;
    if (n < 1 || n > 64) return -22.0;
    sig_clo[n] = clo;
    sig_inv[n] = (sig_watch_fn)inv;
#ifdef _WIN32
    if (sig_install(n, (void *)sig_handler) == (void *)-1) return -22.0;
    return 0.0;
#else
    return (double)__kml_loop_signal_start(n);
#endif
}

/* Stopping restores the default disposition (SIGPIPE's is ignored). */
void __kml_native_signal_stop(double signo) {
    int n = (int)signo;
    if (n < 1 || n > 64) return;
    sig_inv[n] = 0;
#ifdef _WIN32
    sig_install(n, n == 13 ? (void *)1 : (void *)0);
#else
    __kml_loop_signal_stop(n);
    if (n == 13) sig_install(13, (void *)1);
#endif
}
