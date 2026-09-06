// Windows process and signal layer (TDD-00177 Stage 4). Compiled alongside
// the other win32*.c shims; shares the fd table with win32io.c.
//
// Node on Windows spawns with CreateProcessW; there is no fork. The IR keeps
// its pipe()-based plumbing and, on Windows, calls __kml_win_spawn where it
// would fork+exec: the pipe ends become the child's stdio, the pid comes
// back, and waitpid()/kill() work through a pid→handle table. Argument
// quoting follows libuv's make_program_args (the rules cmd.exe and the
// MSVCRT argv parser agree on). Signals: process.on('SIGINT') is a console
// control handler (Ctrl+C and Ctrl+Break both arrive as SIGINT, as in
// Node); a 'SIGTERM' listener is accepted but never fires, since nothing on
// Windows delivers SIGTERM; SIGWINCH has no console equivalent.
#define NO_OLDNAMES 1
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <errno.h>
#include <io.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

enum { L_EACCES = 13, L_ENOENT = 2, L_EINVAL = 22, L_ENOSYS = 38, L_ECHILD = 10, L_ESRCH = 3 };
enum { KFD_PLAIN = 0, KFD_SOCKET = 1 };
#define KFD_MAX 1024

// From win32io.c.
extern unsigned char kfd_kind[KFD_MAX];
extern unsigned char kfd_nonblock[KFD_MAX];
extern unsigned char kfd_inherited[KFD_MAX];
int kfd_register(HANDLE h, int kind);
int kfd_adopt_socket(HANDLE s, int fd);
HANDLE kfd_handle(int fd);
void ws_init(void);
int socket(int, int, int);
int bind(int, const void *, int);
int listen(int, int);
int accept(int, void *, int *);
int connect(int, const void *, int);
int getsockname(int, void *, int *);
int close(int);

// ---- pid table -----------------------------------------------------------------
typedef struct { DWORD pid; HANDLE h; int killsig; int failed; } kml_proc;
#define KML_PROC_MAX 256
static kml_proc kml_procs[KML_PROC_MAX];

static kml_proc *proc_slot(DWORD pid, int create) {
	kml_proc *freep = NULL;
	for (int i = 0; i < KML_PROC_MAX; i++) {
		if (kml_procs[i].pid == pid && kml_procs[i].pid) return &kml_procs[i];
		if (!kml_procs[i].pid && !freep) freep = &kml_procs[i];
	}
	if (!create || !freep) return NULL;
	memset(freep, 0, sizeof *freep);
	freep->pid = pid;
	return freep;
}

// ---- command line -------------------------------------------------------------------
static int arg_needs_quotes(const wchar_t *a) {
	if (!*a) return 1;
	// Any whitespace or control character (not just space/tab): a newline
	// inside an argument must survive the receiving program's re-parse.
	for (; *a; a++) if (*a <= L' ' || *a == L'"') return 1;
	return 0;
}

// Appends a quoted copy of arg to out (libuv's quote_cmd_arg): wrap in
// double quotes; a run of backslashes before a quote (or the end) is
// doubled, and an embedded quote is backslash-escaped.
static wchar_t *quote_arg(wchar_t *out, const wchar_t *arg) {
	if (!arg_needs_quotes(arg)) { while (*arg) *out++ = *arg++; return out; }
	*out++ = L'"';
	size_t len = wcslen(arg);
	wchar_t *start = out;
	int quote_hit = 1;
	for (size_t i = len; i > 0; i--) {
		wchar_t c = arg[i - 1];
		*out++ = c;
		if (quote_hit && c == L'\\') *out++ = L'\\';
		else if (c == L'"') { quote_hit = 1; *out++ = L'\\'; }
		else quote_hit = 0;
	}
	for (wchar_t *a = start, *b = out - 1; a < b; a++, b--) { wchar_t t = *a; *a = *b; *b = t; }
	*out++ = L'"';
	return out;
}

static wchar_t *wide_alloc(const char *s) {
	int n = MultiByteToWideChar(CP_UTF8, 0, s, -1, NULL, 0);
	if (n <= 0) return NULL;
	wchar_t *w = (wchar_t *)malloc((size_t)n * sizeof(wchar_t));
	if (w) MultiByteToWideChar(CP_UTF8, 0, s, -1, w, n);
	return w;
}

static HANDLE inheritable_dup(int fd) {
	HANDLE h = kfd_handle(fd), d;
	if (h == INVALID_HANDLE_VALUE) return INVALID_HANDLE_VALUE;
	if (!DuplicateHandle(GetCurrentProcess(), h, GetCurrentProcess(), &d, 0, TRUE, DUPLICATE_SAME_ACCESS)) return INVALID_HANDLE_VALUE;
	return d;
}

// ---- spawn ------------------------------------------------------------------------------
// __kml_win_spawn(file, argv, cwd, in_fd, out_fd, err_fd, inherit_fd):
// argv is NULL-terminated with argv[0] the program as Node passes it (a
// PATH lookup like execvp's, plus the implicit .exe/.cmd handling
// CreateProcess does); in/out/err are the pipe ends the child owns as fds
// 0/1/2 (-1 inherits ours); inherit_fd (-1 or a socket fd) reaches the
// child under the same fd number, for cluster IPC. Returns the pid, or -1
// with errno set (ENOENT when CreateProcess cannot find the program).
int __kml_win_spawn(const char *file, char **argv, const char *cwd, int in_fd, int out_fd, int err_fd, int inherit_fd) {
	(void)file;
	size_t total = 1;
	int argc = 0;
	for (; argv[argc]; argc++) total += strlen(argv[argc]) * 2 + 4;
	wchar_t *cmd = (wchar_t *)malloc(total * sizeof(wchar_t) * 2);
	if (!cmd) { errno = L_EINVAL; return -1; }
	wchar_t *p = cmd;
	for (int i = 0; i < argc; i++) {
		wchar_t *wa = wide_alloc(argv[i]);
		if (!wa) { free(cmd); errno = L_EINVAL; return -1; }
		if (i) *p++ = L' ';
		p = quote_arg(p, wa);
		free(wa);
	}
	*p = 0;
	wchar_t *wcwd = cwd ? wide_alloc(cwd) : NULL;

	STARTUPINFOW si;
	memset(&si, 0, sizeof si);
	si.cb = sizeof si;
	si.dwFlags = STARTF_USESTDHANDLES;
	HANDLE hin = in_fd >= 0 ? inheritable_dup(in_fd) : GetStdHandle(STD_INPUT_HANDLE);
	HANDLE hout = out_fd >= 0 ? inheritable_dup(out_fd) : GetStdHandle(STD_OUTPUT_HANDLE);
	HANDLE herr = err_fd >= 0 ? inheritable_dup(err_fd) : GetStdHandle(STD_ERROR_HANDLE);
	si.hStdInput = hin; si.hStdOutput = hout; si.hStdError = herr;

	// Environment: the current block plus the inherited-socket marker.
	wchar_t *env = NULL;
	HANDLE hinh = INVALID_HANDLE_VALUE;
	if (inherit_fd >= 0) {
		hinh = inheritable_dup(inherit_fd);
		wchar_t marker[96];
		wsprintfW(marker, L"KML_WIN_INHERIT_FD=%d:%I64u", inherit_fd, (unsigned long long)(uintptr_t)hinh);
		wchar_t *cur = GetEnvironmentStringsW();
		size_t curlen = 0;
		for (wchar_t *q = cur; *q; q += wcslen(q) + 1) curlen += wcslen(q) + 1;
		size_t mlen = wcslen(marker) + 1;
		env = (wchar_t *)malloc((curlen + mlen + 1) * sizeof(wchar_t));
		memcpy(env, cur, curlen * sizeof(wchar_t));
		memcpy(env + curlen, marker, mlen * sizeof(wchar_t));
		env[curlen + mlen] = 0;
		FreeEnvironmentStringsW(cur);
	}

	PROCESS_INFORMATION pi;
	memset(&pi, 0, sizeof pi);
	BOOL ok = CreateProcessW(NULL, cmd, NULL, NULL, TRUE, CREATE_UNICODE_ENVIRONMENT, env, wcwd, &si, &pi);
	DWORD err = GetLastError();
	free(cmd); free(wcwd); free(env);
	if (in_fd >= 0 && hin != INVALID_HANDLE_VALUE) CloseHandle(hin);
	if (out_fd >= 0 && hout != INVALID_HANDLE_VALUE) CloseHandle(hout);
	if (err_fd >= 0 && herr != INVALID_HANDLE_VALUE) CloseHandle(herr);
	if (hinh != INVALID_HANDLE_VALUE) CloseHandle(hinh);
	if (!ok) {
		// On POSIX a program that cannot be exec'd still yields a child, one
		// that _exit(127)s (Node's exec-failure convention), and every caller
		// reads that through waitpid. Mirror it: hand back a pseudo-pid whose
		// waitpid reports exit status 127 immediately. Pseudo-pids sit above
		// the real pid range so they never collide with a live process.
		static DWORD next_pseudo = 0x7f000000;
		errno = (err == ERROR_FILE_NOT_FOUND || err == ERROR_PATH_NOT_FOUND) ? L_ENOENT : err == ERROR_ACCESS_DENIED ? L_EACCES : L_EINVAL;
		kml_proc *pr = proc_slot(next_pseudo++, 1);
		if (!pr) return -1;
		pr->h = NULL;
		pr->failed = 1;
		return (int)pr->pid;
	}
	CloseHandle(pi.hThread);
	kml_proc *pr = proc_slot(pi.dwProcessId, 1);
	if (pr) pr->h = pi.hProcess; else CloseHandle(pi.hProcess);
	// The child now shares the socket object behind inherit_fd; the parent's
	// close() of that fd must release only its own handle (win32io.c).
	if (inherit_fd >= 0 && inherit_fd < KFD_MAX && kfd_kind[inherit_fd] == KFD_SOCKET) kfd_inherited[inherit_fd] = 1;
	return (int)pi.dwProcessId;
}

// Child side of inherit_fd: runs before main() and re-homes the inherited
// socket handle at the fd number the parent advertised.
__attribute__((constructor)) static void kml_win_adopt_inherited(void) {
	char buf[96];
	if (!GetEnvironmentVariableA("KML_WIN_INHERIT_FD", buf, sizeof buf)) return;
	int want = atoi(buf);
	char *colon = strchr(buf, ':');
	if (!colon) return;
	unsigned long long hv = strtoull(colon + 1, NULL, 10);
	ws_init();
	// The parent's socket fds come from the socket range, so the same number
	// is free here: adopt the inherited handle under it directly.
	if (kfd_adopt_socket((HANDLE)(uintptr_t)hv, want) < 0) kfd_register((HANDLE)(uintptr_t)hv, KFD_SOCKET);
	SetEnvironmentVariableA("KML_WIN_INHERIT_FD", NULL);
}

// __kml_win_self_exe: the running executable's path as a malloc'd UTF-8
// string, for re-spawning the program as a cluster worker.
char *__kml_win_self_exe(void) {
	wchar_t w[32768];
	DWORD n = GetModuleFileNameW(NULL, w, 32768);
	if (n == 0 || n >= 32768) return NULL;
	int len = WideCharToMultiByte(CP_UTF8, 0, w, -1, NULL, 0, NULL, NULL);
	char *s = (char *)malloc((size_t)len);
	if (s) WideCharToMultiByte(CP_UTF8, 0, w, -1, s, len, NULL, NULL);
	return s;
}

// ---- wait / kill -------------------------------------------------------------------------
// Status is encoded the POSIX way the IR decodes: (code << 8) for a normal
// exit, the signal number in the low bits when kill() terminated it — so
// Node's { exitCode: null, signal: 'SIGTERM' } shape survives.
int waitpid(int pid, int *status, int options) {
	kml_proc *pr = proc_slot((DWORD)pid, 0);
	if (pr && pr->failed) {
		// A spawn that never started: the "child exited 127" of exec failure.
		if (status) *status = 127 << 8;
		pr->pid = 0;
		return pid;
	}
	HANDLE h = pr ? pr->h : OpenProcess(SYNCHRONIZE | PROCESS_QUERY_LIMITED_INFORMATION, FALSE, (DWORD)pid);
	if (!h) { errno = L_ECHILD; return -1; }
	DWORD w = WaitForSingleObject(h, (options & 1) ? 0 : INFINITE);
	if (w == WAIT_TIMEOUT) return 0;
	DWORD code = 0;
	GetExitCodeProcess(h, &code);
	int st = (pr && pr->killsig) ? (pr->killsig & 0x7f) : (int)((code & 0xff) << 8);
	if (status) *status = st;
	CloseHandle(h);
	if (pr) pr->pid = 0;
	return pid;
}

int kill(int pid, int sig) {
	if (sig == 0) {
		HANDLE h = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, (DWORD)pid);
		if (!h) { errno = GetLastError() == ERROR_ACCESS_DENIED ? L_EACCES : L_ESRCH; return -1; }
		CloseHandle(h);
		return 0;
	}
	// Node on Windows: any signal terminates the target unconditionally.
	kml_proc *pr = proc_slot((DWORD)pid, 0);
	HANDLE h = pr ? pr->h : OpenProcess(PROCESS_TERMINATE, FALSE, (DWORD)pid);
	if (!h) { errno = L_ESRCH; return -1; }
	int ok = TerminateProcess(h, 1);
	if (pr) pr->killsig = sig; else CloseHandle(h);
	return ok ? 0 : (errno = L_EACCES, -1);
}

// socketpair(AF_UNIX, SOCK_STREAM): a connected loopback TCP pair. libuv
// uses a named pipe for the cluster IPC channel on Windows; a socket pair
// keeps the IR's read/write/select path unchanged.
int socketpair(int domain, int type, int protocol, int sv[2]) {
	(void)domain; (void)type; (void)protocol;
	int lfd = socket(2, 1, 0);
	if (lfd < 0) return -1;
	struct { uint16_t fam; uint16_t port; uint32_t addr; char zero[8]; } sa = { 2, 0, 0x0100007f, {0} };
	int len = sizeof sa;
	if (bind(lfd, &sa, len) || listen(lfd, 1) || getsockname(lfd, &sa, &len)) { close(lfd); return -1; }
	int cfd = socket(2, 1, 0);
	if (cfd < 0) { close(lfd); return -1; }
	if (connect(cfd, &sa, len)) { close(lfd); close(cfd); return -1; }
	int afd = accept(lfd, NULL, NULL);
	close(lfd);
	if (afd < 0) { close(cfd); return -1; }
	sv[0] = cfd; sv[1] = afd;
	return 0;
}

// The fork-model entry points are unreachable on Windows once the emitters
// take the spawn path; they stay linkable and fail loudly.
int fork(void) { errno = L_ENOSYS; return -1; }
int execv(const char *p, char **a) { (void)p; (void)a; errno = L_ENOSYS; return -1; }
int execvp(const char *p, char **a) { (void)p; (void)a; errno = L_ENOSYS; return -1; }
void *mmap(void *addr, size_t len, int prot, int flags, int fd, int64_t off) {
	(void)addr; (void)len; (void)prot; (void)flags; (void)fd; (void)off;
	errno = L_ENOSYS;
	return (void *)(intptr_t)-1;
}

// ---- signals ------------------------------------------------------------------------------
typedef void (*kml_sighandler)(int);
static kml_sighandler kml_sig_handlers[32];

// Signal wake-up (ADR-00728). A POSIX signal interrupts select()/nanosleep()
// (EINTR), which is how the reactor notices a pending SIGINT while blocked;
// a console control handler runs on its own thread and interrupts nothing.
// The handler therefore raises this flag, and the shim's select()/nanosleep()
// wait in slices while a handler is installed, returning EINTR once it is
// set — the same shape the IR already handles on POSIX.
volatile long __kml_win_sig_wake;
int __kml_win_sig_installed;
static volatile long kml_sig_queued[32];
static DWORD kml_sig_thread; // the thread that installed the handlers: the main thread

// Node's mapping (libuv): Ctrl+C → SIGINT (2), Ctrl+Break → SIGBREAK (21),
// close/logoff/shutdown → SIGHUP (1). An event with no listener returns
// FALSE so the console's default action (terminate) applies, as in Node.
//
// The IR's handler records the signal in a *thread-local* pending flag the
// reactor polls, which a POSIX kernel sets on the main thread by running the
// handler there. This callback runs on a console-owned thread, where that
// flag would land in the wrong TLS; so the event is only queued here and
// delivered — the handler actually called — on the installing thread the
// next time it waits (__kml_win_sig_deliver from select()/nanosleep()).
static BOOL WINAPI kml_ctrl_handler(DWORD ev) {
	int sig = ev == CTRL_C_EVENT ? 2 : ev == CTRL_BREAK_EVENT ? 21 : (ev == CTRL_CLOSE_EVENT || ev == CTRL_LOGOFF_EVENT || ev == CTRL_SHUTDOWN_EVENT) ? 1 : 0;
	if (!sig || !kml_sig_handlers[sig]) return FALSE;
	InterlockedExchange(&kml_sig_queued[sig], 1);
	InterlockedExchange(&__kml_win_sig_wake, 1);
	if (sig == 1) Sleep(3000); // give the program's SIGHUP listener its chance before the system ends the process
	return TRUE;
}

// __kml_win_sig_deliver runs the queued signals' handlers when called on the
// installing thread; returns the number delivered (0 on any other thread or
// with nothing queued). The waits in select()/nanosleep() call it and report
// EINTR when it delivered, as the POSIX shape the IR already handles.
int __kml_win_sig_deliver(void) {
	if (!__kml_win_sig_installed || GetCurrentThreadId() != kml_sig_thread) return 0;
	if (!InterlockedExchange(&__kml_win_sig_wake, 0)) return 0;
	int n = 0;
	for (int s = 0; s < 32; s++) {
		if (InterlockedExchange(&kml_sig_queued[s], 0) && kml_sig_handlers[s]) { kml_sig_handlers[s](s); n++; }
	}
	return n;
}

void *signal(int sig, void *handler) {
	if (sig < 0 || sig >= 32) return (void *)(intptr_t)-1;
	void *prev = (void *)kml_sig_handlers[sig];
	kml_sig_handlers[sig] = (kml_sighandler)handler;
	if (sig == 2 || sig == 1 || sig == 21) {
		static int installed;
		if (!installed) { SetConsoleCtrlHandler(kml_ctrl_handler, TRUE); installed = 1; }
		kml_sig_thread = GetCurrentThreadId();
		__kml_win_sig_installed = 1;
	}
	return prev;
}
int sigfillset(void *set) { if (set) memset(set, 0xff, 128); return 0; }
int sigemptyset(void *set) { if (set) memset(set, 0, 128); return 0; }
int sigprocmask(int how, const void *set, void *old) { (void)how; (void)set; if (old) memset(old, 0, 128); return 0; }
int pthread_sigmask(int how, const void *set, void *old) { return sigprocmask(how, set, old); }
