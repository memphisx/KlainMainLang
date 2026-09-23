package llvm

// UsesSpawnSync reports whether the program used a blocking child_process
// *Sync form, so the driver links the embedded C implementation.
func (e *Emitter) UsesSpawnSync() bool { return e.usedSpawnSync }

// ensureSpawnSyncRuntime declares the embedded-C entry point once.
func (e *Emitter) ensureSpawnSyncRuntime() {
	if e.usedSpawnSync {
		return
	}
	e.usedSpawnSync = true
	e.emitGlobal("declare ptr @__kml_cp_spawn_sync(ptr, ptr, i64, ptr)")
	e.ensureReexecGuardHelper() // ADR-00972: self-spawn fork-chain guard
}

// SpawnSyncSource is the embedded C implementation behind
// child_process.spawnSync/execSync/execFileSync: fork + execvp with the
// piped stdio captured to completion (poll-multiplexed, so a child filling
// stderr while stdout is being drained can't deadlock), an optional `input`
// written to its stdin, a `timeout`/`maxBuffer` kill, then a blocking
// waitpid. Result strings use the length-prefixed layout of
// runtime_strheader.go ([i64 len][bytes][NUL], value ptr = base+8).
//
// The options record (kmlss_opts) and result (kmlss_result) are 8-byte
// slotted; emit_childprocess.go writes and reads them by slot index
// (cpSyncOptsIR / cpSpawnSyncField) — keep the two in step (ADR-01080).
func SpawnSyncSource() string {
	return `#include <errno.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
/* TDD-00177 Stage 4: the fd/poll/waitpid surface comes from the Windows
   shim; the child is started by __kml_win_spawn (CreateProcessW) instead
   of fork+exec, with the same pipe ends as its stdio. */
#include "kml_posix_compat.h"
#define WIN32_LEAN_AND_MEAN /* no winsock.h: the compat header owns the POSIX socket names */
#include <windows.h>
int __kml_win_spawn(const char *file, char **argv, const char *cwd, int in_fd, int out_fd, int err_fd, int inherit_fd, int flags, char **spawn_env);
int __kml_win_pipe_pw(int *fds);
int waitpid(int pid, int *status, int options);
int __kml_win_exit_code(int pid);
int __kml_win_spawn_failed(int pid);
int kill(int pid, int sig);
#define WIFEXITED(s) (((s) & 0x7f) == 0)
#define WEXITSTATUS(s) (((s) >> 8) & 0xff)
#define WIFSIGNALED(s) (((s) & 0x7f) != 0)
#define WTERMSIG(s) ((s) & 0x7f)
/* The shim's errno numbering is Linux's (what the IR's code table decodes). */
#define KML_ETIMEDOUT 110
#define KML_ENOBUFS 105
#else
#define KML_ETIMEDOUT ETIMEDOUT
#define KML_ENOBUFS ENOBUFS
#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>
extern char **environ;
#endif

/* ADR-00972: mark a self-spawn so the re-executed child's startup guard refuses
   to run its body (see runtime_reexec_guard.go). Shared with the async spawn
   path; a no-op unless the target resolves to this executable. */
void __kml_mark_child_if_self_spawn(const char *file);

/* Length-prefixed string alloc matching __kml_str_alloc's layout. */
static char *kmlss_str(const char *buf, int64_t n) {
  char *b = (char *)malloc(n + 9);
  *(int64_t *)b = n;
  if (n > 0) memcpy(b + 8, buf, n);
  b[8 + n] = 0;
  return b + 8;
}

typedef struct {
  const char *cwd;     /* NULL: inherit */
  char **env;          /* NULL-terminated "K=V" block replacing the child's, or NULL */
  const char *input;   /* bytes written to the child's stdin; NULL: none */
  int64_t input_len;
  int64_t timeout_ms;  /* 0: none */
  int64_t kill_signal; /* the timeout/maxBuffer kill (15 = SIGTERM) */
  int64_t max_buffer;  /* bytes per stream before the kill; 0: unlimited */
  int64_t stdio;       /* 2 bits per fd (stdin 0-1, stdout 2-3, stderr 4-5): 0 pipe 1 inherit 2 ignore */
  int64_t flags;       /* bit 0 shell/verbatim (Windows), bit 1 windowsHide */
  const char *argv0;   /* the name the child sees as argv[0]; NULL: file */
} kmlss_opts;

typedef struct {
  int64_t status; /* exit code; -1 when signal-terminated or never started */
  char *out;      /* captured stdout (length-prefixed), NULL when not piped */
  char *err;      /* captured stderr, NULL when not piped */
  int64_t pid;
  int64_t signal; /* the terminating signal, 0 otherwise */
  int64_t error;  /* 0, or the errno-shaped failure: ETIMEDOUT 110, ENOBUFS 105, the exec errno */
} kmlss_result;

typedef struct { char *buf; int64_t len, cap; } kmlss_acc;

static void kmlss_push(kmlss_acc *a, const char *p, int64_t n) {
  if (a->len + n > a->cap) {
    int64_t nc = a->cap ? a->cap * 2 : 4096;
    while (nc < a->len + n) nc *= 2;
    a->buf = (char *)realloc(a->buf, nc);
    a->cap = nc;
  }
  memcpy(a->buf + a->len, p, n);
  a->len += n;
}

static int64_t kmlss_now_ms(void) {
#ifdef _WIN32
  return (int64_t)GetTickCount64();
#else
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC, &ts);
  return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
#endif
}

/* A child that never ran: Node's { status: null, signal: null, pid: 0,
   stdout/stderr/output: null } plus the error. */
static kmlss_result *kmlss_fail(kmlss_result *r, int64_t err) {
  r->status = -1;
  r->error = err;
  r->out = NULL;
  r->err = NULL;
  r->pid = 0;
  return r;
}

#ifdef _WIN32
/* The child's stdin is fed from a thread: the shim's pipe write end is an
   overlapped handle whose readiness poll() cannot report, and a blocking
   write here could deadlock against a child that fills stdout first. */
typedef struct { int fd; const char *buf; int64_t len; } kmlss_feed;
static DWORD WINAPI kmlss_feed_thread(LPVOID arg) {
  kmlss_feed *f = (kmlss_feed *)arg;
  int64_t off = 0;
  while (off < f->len) {
    int64_t n = write(f->fd, f->buf + off, f->len - off);
    if (n <= 0) { if (n < 0 && errno == EAGAIN) { Sleep(1); continue; } break; }
    off += n;
  }
  close(f->fd);
  free(f);
  return 0;
}
#endif

void *__kml_cp_spawn_sync(const char *file, char **args, int64_t argn, const kmlss_opts *o) {
  kmlss_result *r = (kmlss_result *)calloc(1, sizeof(kmlss_result));
  int m_in = (int)(o->stdio & 3), m_out = (int)((o->stdio >> 2) & 3), m_err = (int)((o->stdio >> 4) & 3);
  int inp[2] = {-1, -1}, outp[2] = {-1, -1}, errp[2] = {-1, -1};
  /* stdin is piped only to carry input; a 'pipe' stdin with no input is an
     immediate EOF (an empty pipe), as in Node. */
  if (m_in == 0) {
#ifdef _WIN32
    if (__kml_win_pipe_pw(inp) != 0) return kmlss_fail(r, errno);
#else
    if (pipe(inp) != 0) return kmlss_fail(r, errno);
#endif
  }
  if (m_out == 0 && pipe(outp) != 0) return kmlss_fail(r, errno);
  if (m_err == 0 && pipe(errp) != 0) return kmlss_fail(r, errno);

  char **argv = (char **)malloc((argn + 2) * sizeof(char *));
  argv[0] = (char *)(o->argv0 ? o->argv0 : file);
  for (int64_t i = 0; i < argn; i++) argv[i + 1] = args[i];
  argv[argn + 1] = NULL;
#ifdef _WIN32
  /* 0x10000 = guard self-spawn: spawnSync(process.execPath, ...) must not
     re-run our own body in the child (ADR-00972); mark it so the startup guard
     rejects it. Mirrors __kml_mark_child_if_self_spawn on the POSIX branch.
     -1 inherits our handle, -2 opens NUL (the shim's sentinels). */
  int wflags = (int)(o->flags & 1) | ((o->flags & 2) ? 2 : 0) | 0x10000;
  int cin = m_in == 0 ? inp[0] : (m_in == 1 ? -1 : -2);
  int cout = m_out == 0 ? outp[1] : (m_out == 1 ? -1 : -2);
  int cerr = m_err == 0 ? errp[1] : (m_err == 1 ? -1 : -2);
  int pid = __kml_win_spawn(file, argv, o->cwd, cin, cout, cerr, -1, wflags, o->env);
  free(argv);
  /* A refused spawn (ENOENT, the .bat EINVAL guard) comes back as a pseudo pid
     carrying its errno; reap it and report the errno, as the async path does. */
  int spawnerr = pid >= 0 ? __kml_win_spawn_failed(pid) : (int)errno;
  if (pid >= 0 && spawnerr) { int st0; waitpid(pid, &st0, 0); }
  if (pid < 0 || spawnerr) {
    int64_t err = spawnerr;
    if (inp[0] >= 0) { close(inp[0]); close(inp[1]); }
    if (outp[0] >= 0) { close(outp[0]); close(outp[1]); }
    if (errp[0] >= 0) { close(errp[0]); close(errp[1]); }
    return kmlss_fail(r, err);
  }
#else
  /* A CLOEXEC status pipe reports an exec failure's errno back (the child
     dies with 127 otherwise, and Node distinguishes ENOENT from a 127 exit). */
  int sp[2];
  if (pipe(sp) != 0) { free(argv); return kmlss_fail(r, errno); }
  fcntl(sp[1], F_SETFD, FD_CLOEXEC);
  pid_t pid = fork();
  if (pid < 0) { free(argv); close(sp[0]); close(sp[1]); return kmlss_fail(r, errno); }
  if (pid == 0) {
    __kml_mark_child_if_self_spawn(file);
    if (o->cwd && chdir(o->cwd) != 0) { int e = errno; write(sp[1], &e, sizeof e); _exit(127); }
    int dn = -1;
    if (m_in == 2 || m_out == 2 || m_err == 2) dn = open("/dev/null", O_RDWR);
    if (m_in == 0) dup2(inp[0], 0); else if (m_in == 2) dup2(dn, 0);
    if (m_out == 0) dup2(outp[1], 1); else if (m_out == 2) dup2(dn, 1);
    if (m_err == 0) dup2(errp[1], 2); else if (m_err == 2) dup2(dn, 2);
    if (dn >= 0) close(dn);
    if (inp[0] >= 0) { close(inp[0]); close(inp[1]); }
    if (outp[0] >= 0) { close(outp[0]); close(outp[1]); }
    if (errp[0] >= 0) { close(errp[0]); close(errp[1]); }
    close(sp[0]);
    if (o->env) environ = o->env;
    execvp(file, argv);
    int e = errno;
    write(sp[1], &e, sizeof e);
    _exit(127); /* Node's exec-failure convention */
  }
  free(argv);
  close(sp[1]);
  /* Node ignores SIGPIPE process-wide; a child that exits before reading its
     input must not take this process down with it. Non-blocking writes keep
     the poll loop draining stdout/stderr between partial feeds. */
  signal(SIGPIPE, SIG_IGN);
  if (inp[1] >= 0) fcntl(inp[1], F_SETFL, fcntl(inp[1], F_GETFL) | O_NONBLOCK);
#endif
  if (inp[0] >= 0) close(inp[0]);
  if (outp[1] >= 0) close(outp[1]);
  if (errp[1] >= 0) close(errp[1]);

  /* Feed stdin. */
  int infd = inp[1];
  int64_t inoff = 0;
  if (infd >= 0 && (!o->input || o->input_len <= 0)) { close(infd); infd = -1; }
#ifdef _WIN32
  if (infd >= 0) {
    kmlss_feed *f = (kmlss_feed *)malloc(sizeof *f);
    f->fd = infd; f->buf = o->input; f->len = o->input_len;
    HANDLE th = CreateThread(NULL, 0, kmlss_feed_thread, f, 0, NULL);
    if (th) CloseHandle(th);
    infd = -1;
  }
#endif

  kmlss_acc oa = {0, 0, 0}, ea = {0, 0, 0};
  struct pollfd fds[3];
  fds[0].fd = outp[0]; fds[0].events = POLLIN;
  fds[1].fd = errp[0]; fds[1].events = POLLIN;
  fds[2].fd = infd;    fds[2].events = POLLOUT;
  int open_ct = (outp[0] >= 0) + (errp[0] >= 0);
  int64_t deadline = o->timeout_ms > 0 ? kmlss_now_ms() + o->timeout_ms : 0;
  int killed = 0;
  char tmp[4096];
  while (open_ct > 0 || fds[2].fd >= 0) {
    int wait_ms = -1;
    if (deadline) {
      int64_t left = deadline - kmlss_now_ms();
      wait_ms = left > 0 ? (int)left : 0;
    }
    int pr = poll(fds, 3, wait_ms);
    if (pr < 0) {
      if (errno == EINTR) continue;
      break;
    }
    if (pr == 0 && deadline && kmlss_now_ms() >= deadline) {
      if (!killed) { kill((int)pid, (int)o->kill_signal); killed = 1; r->error = KML_ETIMEDOUT; }
      deadline = 0;
      continue;
    }
    for (int i = 0; i < 2; i++) {
      if (fds[i].fd < 0) continue;
      if (fds[i].revents & (POLLIN | POLLHUP | POLLERR)) {
        int64_t n = read(fds[i].fd, tmp, sizeof tmp);
        if (n > 0) {
          kmlss_acc *a = i == 0 ? &oa : &ea;
          kmlss_push(a, tmp, n);
          if (o->max_buffer > 0 && a->len > o->max_buffer && !killed) {
            kill((int)pid, (int)o->kill_signal); killed = 1; r->error = KML_ENOBUFS;
          }
        } else if (n == 0 || errno != EAGAIN) {
          close(fds[i].fd);
          fds[i].fd = -1;
          open_ct--;
        }
      }
    }
    if (fds[2].fd >= 0 && (fds[2].revents & (POLLOUT | POLLERR | POLLHUP))) {
      int64_t n = (fds[2].revents & POLLOUT) ? write(fds[2].fd, o->input + inoff, o->input_len - inoff) : -1;
      if (n > 0) inoff += n;
      int again = n < 0 && errno == EAGAIN && !(fds[2].revents & (POLLERR | POLLHUP));
      if (!again && (n <= 0 || inoff >= o->input_len)) { close(fds[2].fd); fds[2].fd = -1; }
    }
  }
  if (fds[2].fd >= 0) close(fds[2].fd);
  int st = 0;
  while (waitpid(pid, &st, 0) < 0 && errno == EINTR) {}
#ifndef _WIN32
  int execerr = 0;
  if (read(sp[0], &execerr, sizeof execerr) == (int64_t)sizeof execerr && execerr != 0) {
    /* The exec (or the cwd chdir) failed in the child: the 127 it died with
       is not a run of the program — report the errno as a never-started spawn. */
    close(sp[0]);
    free(oa.buf);
    free(ea.buf);
    return kmlss_fail(r, execerr);
  }
  close(sp[0]);
#endif
  if (WIFEXITED(st)) {
#ifdef _WIN32
    /* Windows exit codes are full 32-bit; recover the wide value the POSIX
       8-bit wait status dropped (ADR-00759), falling back for foreign pids. */
    int wc = __kml_win_exit_code((int)pid);
    r->status = wc >= 0 ? wc : WEXITSTATUS(st);
    /* A TerminateProcess'd child (our timeout/maxBuffer kill) exits 1 with
       no signal bit; report the kill as the signal, as Node does. */
    if (killed) { r->status = -1; r->signal = o->kill_signal; }
#else
    r->status = WEXITSTATUS(st);
#endif
  } else if (WIFSIGNALED(st)) {
    r->status = -1;
    r->signal = WTERMSIG(st);
  } else r->status = -1;
  r->out = outp[0] >= 0 ? kmlss_str(oa.buf ? oa.buf : "", oa.len) : NULL;
  r->err = errp[0] >= 0 ? kmlss_str(ea.buf ? ea.buf : "", ea.len) : NULL;
  free(oa.buf);
  free(ea.buf);
  r->pid = (int64_t)pid;
  return r;
}
`
}
