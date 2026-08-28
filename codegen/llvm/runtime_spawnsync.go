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
}

// SpawnSyncSource is the embedded C implementation behind
// child_process.spawnSync/execSync/execFileSync: fork + execvp with both
// stdio pipes captured to completion (poll-multiplexed, so a child filling
// stderr while stdout is being drained can't deadlock), then a blocking
// waitpid. Result strings use the length-prefixed layout of
// runtime_strheader.go ([i64 len][bytes][NUL], value ptr = base+8).
func SpawnSyncSource() string {
	return `#include <errno.h>
#include <poll.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

/* Length-prefixed string alloc matching __kml_str_alloc's layout. */
static char *kmlss_str(const char *buf, long n) {
  char *b = (char *)malloc(n + 9);
  *(long *)b = n;
  if (n > 0) memcpy(b + 8, buf, n);
  b[8 + n] = 0;
  return b + 8;
}

typedef struct {
  long status; /* exit code; 128+signal when signal-terminated; -1 on spawn failure */
  char *out;   /* captured stdout, length-prefixed */
  char *err;   /* captured stderr, length-prefixed */
  long pid;
} kmlss_result;

typedef struct { char *buf; long len, cap; } kmlss_acc;

static void kmlss_push(kmlss_acc *a, const char *p, long n) {
  if (a->len + n > a->cap) {
    long nc = a->cap ? a->cap * 2 : 4096;
    while (nc < a->len + n) nc *= 2;
    a->buf = (char *)realloc(a->buf, nc);
    a->cap = nc;
  }
  memcpy(a->buf + a->len, p, n);
  a->len += n;
}

void *__kml_cp_spawn_sync(const char *file, char **args, long argn, const char *cwd) {
  kmlss_result *r = (kmlss_result *)calloc(1, sizeof(kmlss_result));
  int outp[2], errp[2];
  if (pipe(outp) != 0 || pipe(errp) != 0) {
    r->status = -1;
    r->out = kmlss_str("", 0);
    r->err = kmlss_str("", 0);
    return r;
  }
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
    for (long i = 0; i < argn; i++) argv[i + 1] = args[i];
    argv[argn + 1] = NULL;
    execvp(file, argv);
    _exit(127); /* Node's exec-failure convention */
  }
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
        long n = read(fds[i].fd, tmp, sizeof tmp);
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
  if (WIFEXITED(st)) r->status = WEXITSTATUS(st);
  else if (WIFSIGNALED(st)) r->status = 128 + WTERMSIG(st);
  else r->status = -1;
  r->out = kmlss_str(oa.buf ? oa.buf : "", oa.len);
  r->err = kmlss_str(ea.buf ? ea.buf : "", ea.len);
  free(oa.buf);
  free(ea.buf);
  r->pid = (long)pid;
  return r;
}
`
}
