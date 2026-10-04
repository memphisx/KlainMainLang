/* childproc.c — the async child_process handle runtime (TDD-00098, TDD-00223 §5;
 * in C per TDD-00240): a process-wide registry of ChildProcess handles, the
 * event-loop hooks that drain their stdio and reap them, the exit wake, and
 * the Error objects a failed spawn / exec produces. The handle layout is
 * cpStructIR (kml_cp below, checked against kml_layout.h). kml_layout.h is
 * prepended by the compiler. The Windows process shim (win32proc.c) supplies
 * the spawn itself there; the spawn in this file is POSIX only. */
#include <errno.h>
#include <stddef.h>
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#ifdef _WIN32
#include "kml_posix_compat.h"
int waitpid(int pid, int *status, int options);
int kill(int pid, int sig);
int __kml_win_exit_code(int pid);
void __kml_win_child_watch(int pid);
#else
#include <fcntl.h>
#include <signal.h>
#include <sys/wait.h>
#include <unistd.h>
extern char **environ;
/* ADR-00972: marks a self-spawn so the re-executed child's startup guard
 * refuses to run its body (the fork-bomb guard); called in the forked child
 * right before execvp. */
void __kml_mark_child_if_self_spawn(const char *file);
#endif

typedef long long i64;

/* Generated or IR-defined runtime. */
extern char *__kml_str_alloc(i64 n);
extern void __kml_str_finalize(char *s);
extern i64 __kml_monotonic_ns(void);
extern void __kml_worker_fd_setbit(int fd, void *fdset, void *maxfd);
extern void __kml_cp_ipc_drain(void *cp);
extern const char *__kml_errno_code(int e);
extern int __kml_uv_errno(int e);
extern void *__kml_dynobj_new(void);
extern void __kml_dynobj_set(void *o, const char *key, i64 v);

/* The constructor-name symbols (one per name, compared by address; defined
 * by the unit that interns the name, which ensureChildProcRuntime forces). */
#define KML_STR_(x) #x
#define KML_STR(x) KML_STR_(x)
extern char kml_ctorname_Error[] __asm__(KML_STR(__USER_LABEL_PREFIX__) "__kml_ctorname.Error");
extern char kml_ctorname_RangeError[] __asm__(KML_STR(__USER_LABEL_PREFIX__) "__kml_ctorname.RangeError");

/* Length-prefixed string constants: {i64 len, bytes}, the value is the bytes. */
#define KSTR(id, lit) static const struct { i64 len; char s[sizeof(lit)]; } id##_o = { sizeof(lit) - 1, lit };
#define KS(id) ((char *)id##_o.s)

KSTR(k_empty, "")
KSTR(k_mb_out, "stdout maxBuffer length exceeded")
KSTR(k_mb_err, "stderr maxBuffer length exceeded")
KSTR(k_mb_code, "ERR_CHILD_PROCESS_STDIO_MAXBUFFER")

/* %kml.cp (cpStructIR). */
typedef struct { char *data; i64 len, cap; } kml_acc;
typedef struct {
  i64 pid;
  int stdin_fd, stdout_fd, stderr_fd; /* -1 once closed / not piped */
  i64 state;      /* 0 running, 1 reaped ('exit' fired), 2 closed */
  i64 exit_code;
  void *out_data, *out_end, *err_data, *err_end;
  void *close_l, *exit_l, *error_l;   /* listener lists {hdr, next} */
  i64 mode;       /* bit 0 buffered exec, bit 1 shell, bit 3 detached, bits 4-9 stdio */
  kml_acc *out_acc, *err_acc;
  void *exec_cb;
  int ipc_fd;
  void *msg_l, *ipc;
  i64 spawn_errno, kill_sig, deadline, timeout_sig, unref, wait_status, plain_code, maxbuf, maxbuf_hit;
} kml_cp;

#define CP_CHECK(f, M) _Static_assert(offsetof(kml_cp, f) == KML_CP_##M, #f)
CP_CHECK(pid, PID); CP_CHECK(stdin_fd, STDIN); CP_CHECK(stdout_fd, STDOUT); CP_CHECK(stderr_fd, STDERR);
CP_CHECK(state, STATE); CP_CHECK(exit_code, EXITCODE); CP_CHECK(out_data, OUT_DATA); CP_CHECK(err_end, ERR_END);
CP_CHECK(close_l, CLOSE_L); CP_CHECK(exit_l, EXIT_L); CP_CHECK(error_l, ERROR_L); CP_CHECK(mode, MODE);
CP_CHECK(out_acc, OUT_ACC); CP_CHECK(err_acc, ERR_ACC); CP_CHECK(exec_cb, EXEC_CB); CP_CHECK(ipc_fd, IPC_FD);
CP_CHECK(spawn_errno, SPAWN_ERRNO); CP_CHECK(kill_sig, KILL_SIG); CP_CHECK(deadline, DEADLINE);
CP_CHECK(timeout_sig, TIMEOUT_SIG); CP_CHECK(unref, UNREF); CP_CHECK(wait_status, WAIT_STATUS);
CP_CHECK(plain_code, PLAIN_CODE); CP_CHECK(maxbuf, MAXBUF); CP_CHECK(maxbuf_hit, MAXBUF_HIT);
_Static_assert(sizeof(kml_cp) == KML_CP_SIZE, "kml_cp size");

/* errorObjType. */
typedef struct {
  i64 kind;
  char *message, *name, *code;
  double errcode;
  char *errstr, *syscall, *path;
  double errno_neg;
  char *dest;
  i64 cause;
  char *address;
  double port;
  void *extra;
  i64 name_own;
} kml_err;
_Static_assert(offsetof(kml_err, message) == KML_ERR_MESSAGE && offsetof(kml_err, name) == KML_ERR_NAME &&
               offsetof(kml_err, code) == KML_ERR_CODE && offsetof(kml_err, errcode) == KML_ERR_ERRCODE &&
               offsetof(kml_err, errstr) == KML_ERR_ERRSTR && offsetof(kml_err, errno_neg) == KML_ERR_ERRNO &&
               offsetof(kml_err, cause) == KML_ERR_CAUSE && offsetof(kml_err, extra) == KML_ERR_EXTRA &&
               sizeof(kml_err) == KML_ERR_SIZE, "kml_err layout");

typedef struct { void *fn, *env; } kml_closure;
typedef struct { kml_closure *hdr; void *next; } kml_node; /* listener list node */
typedef void (*fn_env)(void *env);
typedef void (*fn_data)(void *env, char *buf, i64 n);
typedef void (*fn_err)(void *env, void *err);
typedef void (*fn_exit)(void *env, _Bool present, double code, char *sig);
typedef void (*fn_exec)(void *env, void *err, char *out, char *errs);

/* ---- registry ---- */

static kml_cp **cp_data;
static i64 cp_len, cp_cap;
static _Bool cp_kick; /* set by register: the next fdset_add reports "don't block" */

/* ---- the exit wake (TDD-00223 §5) ----
 * The loop must leave select() the moment a child ends, even when its stdio
 * is silent or held open by a grandchild. POSIX: a SIGCHLD handler writes one
 * byte to a non-blocking self-pipe whose read end sits in the loop's read set
 * (a self-pipe, not EINTR: a signal landing between the loop's last check and
 * its select() would otherwise go unnoticed). Windows: a registered wait on
 * the process handle posts to the reactor's completion port. */
#ifdef _WIN32
void __kml_cp_watch_init(void) {}
static void cp_watch_pid(int pid) { __kml_win_child_watch(pid); }
static _Bool cp_wake_fdset_add(void *fdset, void *maxfd) {
  (void)fdset; (void)maxfd;
  _Bool k = cp_kick;
  cp_kick = 0;
  return k;
}
static void cp_wake_drain(void) {}
#else
/* The pipe belongs to the process that created it: a forked cluster worker
 * inherits both ends and the handler, so every use is gated on the owner pid
 * and a worker that spawns children makes its own pipe. */
static volatile int wake_r = -1, wake_w = -1, wake_owner = 0;

static void cp_sigchld(int sig) {
  (void)sig;
  if (wake_owner == getpid()) {
    /* async-signal-safe: getpid and write only, errno preserved */
    int saved = errno;
    ssize_t n = write(wake_w, "x", 1);
    (void)n;
    errno = saved;
  }
}

void __kml_cp_watch_init(void) {
  int me = getpid();
  if (wake_owner == me) return;
  int fds[2];
  if (pipe(fds) != 0) return;
  /* both ends non-blocking (a full pipe must never stall the handler, an
   * empty one the drain) and close-on-exec */
  for (int i = 0; i < 2; i++) {
    fcntl(fds[i], F_SETFL, fcntl(fds[i], F_GETFL) | O_NONBLOCK);
    fcntl(fds[i], F_SETFD, 1);
  }
  wake_r = fds[0];
  wake_w = fds[1];
  wake_owner = me;
  signal(SIGCHLD, cp_sigchld);
}
static void cp_watch_pid(int pid) { (void)pid; }

static _Bool cp_wake_fdset_add(void *fdset, void *maxfd) {
  _Bool k = cp_kick;
  cp_kick = 0;
  if (wake_owner == getpid()) __kml_worker_fd_setbit(wake_r, fdset, maxfd);
  return k;
}

static void cp_wake_drain(void) {
  if (wake_owner != getpid()) return;
  char buf[64];
  while (read(wake_r, buf, 64) == 64) {}
}
#endif

/* ---- listener lists ---- */

/* The 'close'/'exit'/'error'/'message' slots hold a LIST of closure headers
 * ({hdr, next} nodes, appended in registration order — Node fires listeners
 * in the order they were added). */
void __kml_cp_listener_append(void **slot, void *hdr) {
  kml_node *node = (kml_node *)malloc(sizeof(kml_node));
  node->hdr = (kml_closure *)hdr;
  node->next = NULL;
  void **cursor = slot;
  while (*cursor) cursor = &((kml_node *)*cursor)->next;
  *cursor = node;
}

/* ---- stdio ---- */

/* Append n bytes, keeping a trailing NUL so the buffer doubles as a C string. */
static void cp_accum(kml_acc *a, const char *src, i64 n) {
  i64 need = a->len + n + 1;
  if (need > a->cap) {
    i64 nc = a->cap * 2 > need ? a->cap * 2 : need;
    if (nc < 64) nc = 64;
    a->data = (char *)realloc(a->data, (size_t)nc);
    a->cap = nc;
  }
  memcpy(a->data + a->len, src, (size_t)n);
  a->len += n;
  a->data[a->len] = 0;
}

/* Read the fd until EAGAIN/EOF. On data: fire dataL (streaming) or append
 * (buffered). On EOF: close, set the slot -1, fire endL (streaming). */
static void cp_drain(kml_cp *cp, int *fdslot, kml_closure *dataL, kml_closure *endL, kml_acc *accum, i64 mode) {
  char chunk[4096];
  for (;;) {
    int fd = *fdslot;
    if (fd < 0) return;
    i64 n = (i64)read(fd, chunk, 4096);
    if (n > 0) {
      if ((mode & 1) == 0) { /* streaming */
        if (!dataL) continue;
        char *buf = __kml_str_alloc(n);
        memcpy(buf, chunk, (size_t)n);
        buf[n] = 0;
        ((fn_data)dataL->fn)(dataL->env, buf, n);
        continue;
      }
      cp_accum(accum, chunk, n);
      /* maxBuffer (ADR-01080): past the limit the child is killed with the
       * killSignal and the overrun stream recorded for the callback's error. */
      if (cp->maxbuf > 0 && accum->len > cp->maxbuf && cp->maxbuf_hit == 0) {
        cp->maxbuf_hit = accum == cp->out_acc ? 1 : 2;
        kill((int)cp->pid, (int)cp->timeout_sig);
      }
      continue;
    }
    if (n != 0) return;
    close(fd);
    *fdslot = -1;
    if ((mode & 1) == 0 && endL) ((fn_env)endL->fn)(endL->env);
    return;
  }
}

void __kml_cp_stdin_write(void *cpv, const void *data, i64 n) {
  kml_cp *cp = (kml_cp *)cpv;
  if (cp->stdin_fd < 0) return;
  i64 w = (i64)write(cp->stdin_fd, data, (size_t)n);
  (void)w;
}

void __kml_cp_stdin_end(void *cpv) {
  kml_cp *cp = (kml_cp *)cpv;
  if (cp->stdin_fd < 0) return;
  close(cp->stdin_fd);
  cp->stdin_fd = -1;
}

/* ---- exit event shape and signal names (TDD-00184) ---- */

/* The streaming 'exit'/'close' (code, signal) shape: present is false for a
 * signalled child (the listener sees null), signum its signal. POSIX reads
 * the wait status (WIFSIGNALED / WTERMSIG); Windows has no signalled bit
 * (TerminateProcess sets an exit code), so it uses the signal a .kill()
 * recorded. */
static _Bool cp_event_flags(int status, i64 killsig, int *signum) {
#ifdef _WIN32
  (void)status;
  *signum = (int)killsig;
  return killsig == 0;
#else
  (void)killsig;
  int low = status & 127;
  *signum = low;
  return low == 0;
#endif
}

#define SIGNAME(id, s) KSTR(id, s)
SIGNAME(sn_hup, "SIGHUP") SIGNAME(sn_int, "SIGINT") SIGNAME(sn_quit, "SIGQUIT") SIGNAME(sn_ill, "SIGILL")
SIGNAME(sn_trap, "SIGTRAP") SIGNAME(sn_abrt, "SIGABRT") SIGNAME(sn_fpe, "SIGFPE") SIGNAME(sn_kill, "SIGKILL")
SIGNAME(sn_segv, "SIGSEGV") SIGNAME(sn_pipe, "SIGPIPE") SIGNAME(sn_alrm, "SIGALRM") SIGNAME(sn_term, "SIGTERM")
SIGNAME(sn_bus, "SIGBUS") SIGNAME(sn_usr1, "SIGUSR1") SIGNAME(sn_usr2, "SIGUSR2")

/* The Node signal name for signal number n, or NULL. The numbering is the
 * host platform's: 1-6, 8, 9, 11, 13-15 are shared by Linux and macOS;
 * SIGBUS/SIGUSR1/SIGUSR2 differ. Windows' synthetic signals reuse the
 * Linux-style numbers .kill() records. Keep in step with cpSignalTable. */
static char *cp_signal_name(i64 n) {
  switch (n) {
  case 1: return KS(sn_hup);
  case 2: return KS(sn_int);
  case 3: return KS(sn_quit);
  case 4: return KS(sn_ill);
  case 5: return KS(sn_trap);
  case 6: return KS(sn_abrt);
  case 8: return KS(sn_fpe);
  case 9: return KS(sn_kill);
  case 11: return KS(sn_segv);
  case 13: return KS(sn_pipe);
  case 14: return KS(sn_alrm);
  case 15: return KS(sn_term);
#ifdef __APPLE__
  case 10: return KS(sn_bus);
  case 30: return KS(sn_usr1);
  case 31: return KS(sn_usr2);
#else
  case 7: return KS(sn_bus);
  case 10: return KS(sn_usr1);
  case 12: return KS(sn_usr2);
#endif
  }
  return NULL;
}

/* ---- Error objects ---- */

/* "Command failed with exit code N" */
static char *cp_exec_errmsg(i64 code) {
  char *buf = __kml_str_alloc(64);
  snprintf(buf, 64, "Command failed with exit code %lld", code);
  __kml_str_finalize(buf);
  return buf;
}

/* The Error a failed *spawn* emits through 'error' (ADR-00754): message
 * "spawn <reason>" from strerror(errno). Carries Node's `err.code` (ENOENT
 * for a missing command), `err.errno` (the negative libuv-style errno, -2 on
 * POSIX) and `err.errstr`; `err.syscall`/`err.path` stay null. The
 * errno-to-code map is built per platform, so the code string is right where
 * the raw number differs Linux/macOS (EAGAIN 11 vs 35, ...). */
static void *cp_spawn_errobj(i64 err) {
  int e32 = (int)err;
  const char *reason = strerror(e32);
  char *buf = __kml_str_alloc(128);
  snprintf(buf, 128, "spawn %s", reason);
  __kml_str_finalize(buf);
  kml_err *o = (kml_err *)calloc(1, sizeof(kml_err));
  o->kind = KML_ERRKIND_ERROR;
  o->message = buf;
  o->name = kml_ctorname_Error + 8;
  o->code = (char *)__kml_errno_code(e32);
  o->errcode = (double)e32;
  o->errstr = (char *)reason;
  o->errno_neg = (double)__kml_uv_errno(e32);
  o->cause = KML_NB_UNDEFINED;
  return o;
}

/* A buffered stream as a length-prefixed string (binary-safe: .split and ===
 * read the header at ptr-8); "" if nothing was ever accumulated (TDD-00120). */
static char *cp_accum_str(kml_acc *a) {
  if (!a || !a->data) return KS(k_empty);
  char *s = __kml_str_alloc(a->len);
  memcpy(s, a->data, (size_t)a->len);
  s[a->len] = 0;
  return s;
}

/* ---- reap / close ---- */

/* waitpid(WNOHANG) a still-running child (state 0). Once reaped: record the
 * wait status / exit codes, mark it state 1 and fire 'exit' — or, for a spawn
 * that never started, 'error' (ADR-00754: 'error' then 'close', never
 * 'exit'). A child's end is two independent events, as in Node: 'exit' (the
 * process ended; fired the iteration the reap succeeds, whatever its stdio is
 * doing) and 'close' (ended AND stdio closed; __kml_cp_close). */
static void cp_reap(kml_cp *cp) {
  if (cp->state != 0) return;
  int pid = (int)cp->pid;
  int st = 0;
  int r = waitpid(pid, &st, 1 /* WNOHANG */);
  if (r != pid) return;
  int low = st & 127, code, plain;
  if (low == 0) {
    code = (st >> 8) & 255;
#ifdef _WIN32
    /* Windows exit codes are full 32-bit; recover the wide value the POSIX
     * 8-bit wait status dropped, falling back for foreign pids (ADR-00759). */
    int wc = __kml_win_exit_code(pid);
    if (wc >= 0) code = wc;
#endif
    plain = code;
  } else {
    code = low + 128; /* the folded value the buffered path / exitCode expect */
    plain = 0;
  }
  cp->exit_code = (i64)(unsigned)code;
  /* the raw wait status and the plain code, for close's own (code, signal):
   * it may run many iterations after this one */
  cp->wait_status = (i64)(unsigned)st;
  cp->plain_code = (i64)(unsigned)plain;
  int sig;
  _Bool present = cp_event_flags(st, cp->kill_sig, &sig);
  char *signame = cp_signal_name(sig);
  double evcode = present ? (double)cp->plain_code : 0.0;
  cp->state = 1;
  if (cp->mode & 1) return; /* buffered: the exec callback rides close */
  if (cp->spawn_errno != 0) { /* a failed spawn: 'error', not 'exit' */
    if (!cp->error_l) return;
    void *eo = cp_spawn_errobj(cp->spawn_errno);
    for (kml_node *n = (kml_node *)cp->error_l; n; n = (kml_node *)n->next)
      ((fn_err)n->hdr->fn)(n->hdr->env, eo);
    return;
  }
  /* The stored listener is a fixed-ABI adapter void(env, present, code,
   * signal) forwarding to the user closure with its own arity. */
  for (kml_node *n = (kml_node *)cp->exit_l; n; n = (kml_node *)n->next)
    ((fn_exit)n->hdr->fn)(n->hdr->env, present, evcode, signame);
}

/* A reaped child (state 1) whose stdio has ended: fire 'close'(code, signal),
 * or hand the buffered exec callback its whole output, and finalize (state 2).
 * The caller checks the stdio side. */
static void cp_close(kml_cp *cp) {
  if (cp->state != 1) return;
  cp->state = 2;
  i64 code64 = cp->exit_code;
  i64 ks = cp->kill_sig;
  int sig;
  _Bool present = cp_event_flags((int)cp->wait_status, ks, &sig);
  char *signame = cp_signal_name(sig);
  double evcode = present ? (double)cp->plain_code : 0.0;
  if (!(cp->mode & 1)) {
    for (kml_node *n = (kml_node *)cp->close_l; n; n = (kml_node *)n->next)
      ((fn_exit)n->hdr->fn)(n->hdr->env, present, evcode, signame);
    return;
  }
  kml_closure *cb = (kml_closure *)cp->exec_cb;
  if (!cb) return;
  char *so = cp_accum_str(cp->out_acc);
  char *se = cp_accum_str(cp->err_acc);
  /* err: null on success, else an Error object (a full errorObjType: code,
   * errno, ... are readable, ADR-01080). A maxBuffer overrun is Node's
   * RangeError ERR_CHILD_PROCESS_STDIO_MAXBUFFER, whatever the exit code. */
  i64 hit = cp->maxbuf_hit;
  kml_err *eo = NULL;
  if (code64 != 0 || hit != 0) {
    eo = (kml_err *)calloc(1, sizeof(kml_err));
    eo->kind = hit ? KML_ERRKIND_RANGE : KML_ERRKIND_ERROR;
    eo->message = hit ? (hit == 1 ? KS(k_mb_out) : KS(k_mb_err)) : cp_exec_errmsg(code64);
    eo->name = (hit ? kml_ctorname_RangeError : kml_ctorname_Error) + 8;
    eo->code = hit ? KS(k_mb_code) : NULL;
    eo->cause = KML_NB_UNDEFINED;
    /* Node's ExecException own properties: killed (this side ended it: a
     * child.kill() or the timeout) and signal (the name or null). The
     * maxBuffer RangeError is not decorated, as in Node. */
    if (!hit) {
      void *bag = __kml_dynobj_new();
      __kml_dynobj_set(bag, "killed", ks != 0 ? KML_NB_TRUE : KML_NB_FALSE);
      __kml_dynobj_set(bag, "signal", signame ? (i64)(size_t)signame : KML_NB_NULL);
      eo->extra = bag;
    }
  }
  ((fn_exec)cb->fn)(cb->env, eo, so, se);
}

/* ---- event-loop hooks ---- */

/* Drain + finalize every live child. Called by the event loop after select(). */
void __kml_cp_dispatch(void) {
  cp_wake_drain();
  /* len/data reloaded each iteration: a listener fired below may spawn a
   * child, and register grows the registry with realloc */
  for (i64 i = 0; i < cp_len; i++) {
    kml_cp *cp = cp_data[i];
    if (cp->state >= 2) continue;
    /* spawn timeout: kill the child once its deadline passes (ADR-00764). The
     * kill's signal is recorded so the reap reports the faithful ('exit',
     * null, '<signal>'). */
    if (cp->deadline != 0 && __kml_monotonic_ns() >= cp->deadline) {
      cp->deadline = 0;
      cp->kill_sig = cp->timeout_sig;
      kill((int)cp->pid, (int)cp->timeout_sig);
    }
    i64 mode = cp->mode;
    cp_drain(cp, &cp->stdout_fd, (kml_closure *)cp->out_data, (kml_closure *)cp->out_end, cp->out_acc, mode);
    cp_drain(cp, &cp->stderr_fd, (kml_closure *)cp->err_data, (kml_closure *)cp->err_end, cp->err_acc, mode);
    __kml_cp_ipc_drain(cp);
    /* 'exit' the iteration the process ends, independent of its stdio */
    cp_reap(cp);
    /* a fork child with an open IPC channel is not finalizable yet */
    if (cp->stdout_fd < 0 && cp->stderr_fd < 0 && !(cp->ipc_fd > 0))
      cp_close(cp); /* a no-op until reaped: the exit wake brings the loop back */
  }
}

/* The soonest spawn-`timeout` deadline (absolute monotonic ns) among live
 * children, or 0: folded into the loop's select() wait so a silent slow
 * child is still killed on time (ADR-00764). */
i64 __kml_cp_next_timeout_ns(void) {
  i64 best = 0;
  for (i64 i = 0; i < cp_len; i++) {
    kml_cp *cp = cp_data[i];
    if (cp->state >= 2 || cp->deadline == 0) continue;
    if (best == 0 || cp->deadline < best) best = cp->deadline;
  }
  return best;
}

/* Add every live child's read fds plus the exit wake. A child whose stdio has
 * ended but which is still running costs nothing: the loop blocks, and the
 * exit wake brings it back to reap. True only for the one non-blocking pass a
 * fresh registration asks for. */
_Bool __kml_cp_fdset_add(void *fdset, void *maxfd) {
  for (i64 i = 0; i < cp_len; i++) {
    kml_cp *cp = cp_data[i];
    if (cp->state >= 2) continue;
    if (cp->stdout_fd >= 0) __kml_worker_fd_setbit(cp->stdout_fd, fdset, maxfd);
    if (cp->stderr_fd >= 0) __kml_worker_fd_setbit(cp->stderr_fd, fdset, maxfd);
    if (cp->ipc_fd > 0) __kml_worker_fd_setbit(cp->ipc_fd, fdset, maxfd);
  }
  return cp_wake_fdset_add(fdset, maxfd);
}

/* True while any child handle is not yet finalized (an unref()'d child, field
 * UNREF, does not hold the loop open, ADR-00767). */
_Bool __kml_cp_keepalive(void) {
  for (i64 i = 0; i < cp_len; i++) {
    kml_cp *cp = cp_data[i];
    if (cp->state < 2 && cp->unref == 0) return 1;
  }
  return 0;
}

/* Append to the process-wide handle registry. */
void __kml_cp_register(void *cpv) {
  kml_cp *cp = (kml_cp *)cpv;
  __kml_cp_watch_init();
  cp_watch_pid((int)cp->pid);
  cp_kick = 1;
  if (cp_len >= cp_cap) {
    i64 nc = cp_cap * 2 > 4 ? cp_cap * 2 : 4;
    cp_data = (kml_cp **)realloc(cp_data, (size_t)nc * sizeof(kml_cp *));
    cp_cap = nc;
  }
  cp_data[cp_len++] = cp;
}

#ifndef _WIN32
/* fork+exec with three pipes; returns the ChildProcess handle. The two read
 * fds are made non-blocking; buffered mode pre-allocates the accumulators.
 * Windows spawns through __kml_win_spawn (generated, see ensureChildProcRuntime). */
void *__kml_cp_spawn(const char *file, char **argsdata, i64 argslen, i64 mode, const char *cwd, char **env,
                     i64 timeout_ms, i64 killsig, i64 maxbuf) {
  /* the exit wake must exist before the child can (TDD-00223 §5) */
  __kml_cp_watch_init();
  char **argv = (char **)malloc((size_t)(argslen + 2) * sizeof(char *));
  argv[0] = (char *)file;
  if (argslen > 0) memcpy(argv + 1, argsdata, (size_t)argslen * sizeof(char *));
  argv[argslen + 1] = NULL;

  int inp[2], outp[2], errp[2], sp[2];
  pipe(inp);
  pipe(outp);
  pipe(errp);
  /* A close-on-exec status pipe: a successful execvp closes it (the parent
   * reads EOF), a failed one leaves the child's errno in it (ADR-00754). */
  pipe(sp);
  fcntl(sp[1], F_SETFD, 1);
  fcntl(sp[0], F_SETFD, 1);
  /* Per-fd stdio (mode bits 4-9, ADR-00766): pipe (0), inherit (1), ignore (2). */
  int m_in = (int)((mode >> 4) & 3), m_out = (int)((mode >> 6) & 3), m_err = (int)((mode >> 8) & 3);
  int pid = fork();
  if (pid == 0) {
    /* detached (bit 3): a new session/group leader so the child can outlive
     * the parent and isn't in its process group (ADR-00765). */
    if (mode & 8) setsid();
    if (cwd) { int rc = chdir(cwd); (void)rc; }
    /* pipe dup2's the pipe end; inherit leaves the inherited parent fd;
     * ignore dup2's /dev/null (opened once, closed before exec). */
    int dn = (m_in == 2 || m_out == 2 || m_err == 2) ? open("/dev/null", O_RDWR) : -1;
    int si = m_in == 0 ? inp[0] : (m_in == 2 ? dn : -1);
    int so = m_out == 0 ? outp[1] : (m_out == 2 ? dn : -1);
    int se = m_err == 0 ? errp[1] : (m_err == 2 ? dn : -1);
    if (si >= 0) dup2(si, 0);
    if (so >= 0) dup2(so, 1);
    if (se >= 0) dup2(se, 2);
    if (dn >= 0) close(dn);
    close(inp[0]); close(inp[1]); close(outp[0]); close(outp[1]); close(errp[0]); close(errp[1]);
    /* A custom env fully replaces the child's: point environ at it before exec
     * so execvp inherits it (ADR-00762). No env: keep ours. */
    if (env) environ = env;
    /* ADR-00972: a self-spawn (target resolves to this executable) marks the
     * child env so its startup guard refuses to re-run this program body,
     * which would re-hit this spawn (a fork chain). */
    __kml_mark_child_if_self_spawn(file);
    execvp(file, argv);
    /* execvp only returns on failure: report errno up the status pipe */
    int e = errno;
    ssize_t w = write(sp[1], &e, sizeof e);
    (void)w;
    _exit(127);
  }
  free(argv);
  close(inp[0]);
  if (m_in != 0) close(inp[1]);
  if (m_out == 0) {
    close(outp[1]);
    fcntl(outp[0], F_SETFL, fcntl(outp[0], F_GETFL) | O_NONBLOCK);
  } else {
    close(outp[1]);
    close(outp[0]);
  }
  if (m_err == 0) {
    close(errp[1]);
    fcntl(errp[0], F_SETFL, fcntl(errp[0], F_GETFL) | O_NONBLOCK);
  } else {
    close(errp[1]);
    close(errp[0]);
  }

  kml_cp *cp = (kml_cp *)calloc(1, sizeof(kml_cp));
  cp->pid = (i64)(unsigned)pid;
  cp->stdin_fd = m_in == 0 ? inp[1] : -1;
  cp->stdout_fd = m_out == 0 ? outp[0] : -1;
  cp->stderr_fd = m_err == 0 ? errp[0] : -1;
  cp->mode = mode;
  /* spawn timeout: store the absolute deadline (now + timeout_ms) and the
   * kill signal so the dispatch can kill a slow child (ADR-00764). */
  cp->deadline = timeout_ms != 0 ? __kml_monotonic_ns() + timeout_ms * 1000000 : 0;
  cp->timeout_sig = killsig;
  cp->maxbuf = maxbuf;
  if (mode & 1) {
    cp->out_acc = (kml_acc *)calloc(1, sizeof(kml_acc));
    cp->err_acc = (kml_acc *)calloc(1, sizeof(kml_acc));
  }
  /* A short read of 4 bytes is the child's exec errno; EOF means success. */
  close(sp[1]);
  int child_err = 0;
  ssize_t got = read(sp[0], &child_err, 4);
  close(sp[0]);
  cp->spawn_errno = got > 0 ? (i64)child_err : 0;
  __kml_cp_register(cp);
  return cp;
}
#endif
