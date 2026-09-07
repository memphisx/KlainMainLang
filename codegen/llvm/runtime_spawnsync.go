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
	e.emitGlobal("declare ptr @__kml_cp_spawn_sync(ptr, ptr, i64, ptr, i64)")
}

// SpawnSyncSource is the embedded C implementation behind
// child_process.spawnSync/execSync/execFileSync: fork + execvp with both
// stdio pipes captured to completion (poll-multiplexed, so a child filling
// stderr while stdout is being drained can't deadlock), then a blocking
// waitpid. Result strings use the length-prefixed layout of
// runtime_strheader.go ([i64 len][bytes][NUL], value ptr = base+8).
func SpawnSyncSource() string {
	return `#include <errno.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
/* TDD-00177 Stage 4: the fd/poll/waitpid surface comes from the Windows
   shim; the child is started by __kml_win_spawn (CreateProcessW) instead
   of fork+exec, with the same pipe ends as its stdout/stderr. */
#include "kml_posix_compat.h"
int __kml_win_spawn(const char *file, char **argv, const char *cwd, int in_fd, int out_fd, int err_fd, int inherit_fd, int flags, char **spawn_env);
int waitpid(int pid, int *status, int options);
int __kml_win_exit_code(int pid);
#define WIFEXITED(s) (((s) & 0x7f) == 0)
#define WEXITSTATUS(s) (((s) >> 8) & 0xff)
#define WIFSIGNALED(s) (((s) & 0x7f) != 0)
#define WTERMSIG(s) ((s) & 0x7f)
#else
#include <poll.h>
#include <sys/wait.h>
#include <unistd.h>
#endif

/* Length-prefixed string alloc matching __kml_str_alloc's layout. */
static char *kmlss_str(const char *buf, int64_t n) {
  char *b = (char *)malloc(n + 9);
  *(int64_t *)b = n;
  if (n > 0) memcpy(b + 8, buf, n);
  b[8 + n] = 0;
  return b + 8;
}

typedef struct {
  int64_t status; /* exit code; 128+signal when signal-terminated; -1 on spawn failure */
  char *out;   /* captured stdout, length-prefixed */
  char *err;   /* captured stderr, length-prefixed */
  int64_t pid;
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

/* flags bit 0: shell/verbatim (execSync) — passed to __kml_win_spawn's
   windowsVerbatimArguments on Windows, ignored on POSIX (ADR-00740). */
void *__kml_cp_spawn_sync(const char *file, char **args, int64_t argn, const char *cwd, int64_t flags) {
  (void)flags;
  kmlss_result *r = (kmlss_result *)calloc(1, sizeof(kmlss_result));
  int outp[2], errp[2];
  if (pipe(outp) != 0 || pipe(errp) != 0) {
    r->status = -1;
    r->out = kmlss_str("", 0);
    r->err = kmlss_str("", 0);
    return r;
  }
#ifdef _WIN32
  char **argv = (char **)malloc((argn + 2) * sizeof(char *));
  argv[0] = (char *)file;
  for (int64_t i = 0; i < argn; i++) argv[i + 1] = args[i];
  argv[argn + 1] = NULL;
  int pid = __kml_win_spawn(file, argv, cwd, -1, outp[1], errp[1], -1, (int)(flags & 1), NULL);
  free(argv);
  if (pid < 0) {
    close(outp[0]); close(outp[1]); close(errp[0]); close(errp[1]);
    r->status = -1;
    r->out = kmlss_str("", 0);
    r->err = kmlss_str("", 0);
    return r;
  }
#else
  pid_t pid = fork();
  if (pid < 0) {
    r->status = -1;
    r->out = kmlss_str("", 0);
    r->err = kmlss_str("", 0);
    return r;
  }
  if (pid == 0) {
    if (cwd && chdir(cwd) != 0) _exit(127);
    dup2(outp[1], 1);
    dup2(errp[1], 2);
    close(outp[0]); close(outp[1]);
    close(errp[0]); close(errp[1]);
    char **argv = (char **)malloc((argn + 2) * sizeof(char *));
    argv[0] = (char *)file;
    for (int64_t i = 0; i < argn; i++) argv[i + 1] = args[i];
    argv[argn + 1] = NULL;
    execvp(file, argv);
    _exit(127); /* Node's exec-failure convention */
  }
#endif
  close(outp[1]);
  close(errp[1]);
  kmlss_acc oa = {0, 0, 0}, ea = {0, 0, 0};
  struct pollfd fds[2];
  fds[0].fd = outp[0]; fds[0].events = POLLIN;
  fds[1].fd = errp[0]; fds[1].events = POLLIN;
  int open_ct = 2;
  char tmp[4096];
  while (open_ct > 0) {
    if (poll(fds, 2, -1) < 0) {
      if (errno == EINTR) continue;
      break;
    }
    for (int i = 0; i < 2; i++) {
      if (fds[i].fd < 0) continue;
      if (fds[i].revents & (POLLIN | POLLHUP)) {
        int64_t n = read(fds[i].fd, tmp, sizeof tmp);
        if (n > 0) {
          kmlss_push(i == 0 ? &oa : &ea, tmp, n);
        } else {
          close(fds[i].fd);
          fds[i].fd = -1;
          open_ct--;
        }
      }
    }
  }
  int st = 0;
  while (waitpid(pid, &st, 0) < 0 && errno == EINTR) {}
  if (WIFEXITED(st)) {
#ifdef _WIN32
    /* Windows exit codes are full 32-bit; recover the wide value the POSIX
       8-bit wait status dropped (ADR-00759), falling back for foreign pids. */
    int wc = __kml_win_exit_code((int)pid);
    r->status = wc >= 0 ? wc : WEXITSTATUS(st);
#else
    r->status = WEXITSTATUS(st);
#endif
  }
  else if (WIFSIGNALED(st)) r->status = 128 + WTERMSIG(st);
  else r->status = -1;
  r->out = kmlss_str(oa.buf ? oa.buf : "", oa.len);
  r->err = kmlss_str(ea.buf ? ea.buf : "", ea.len);
  free(oa.buf);
  free(ea.buf);
  r->pid = (int64_t)pid;
  return r;
}
`
}
