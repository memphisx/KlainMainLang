// Windows process and signal layer (TDD-00177 Stage 4). Compiled alongside
// the other win32*.c shims; shares the fd table with win32io.c.
//
// A child process is libuv's uv_spawn (klainpool.c); this layer answers what
// that path asks (the batch-file refusal, the self-spawn test, CRT
// descriptors) and re-spawns this executable as a cluster worker, with
// argument quoting after libuv's make_program_args (the rules cmd.exe and the
// MSVCRT argv parser agree on). kill() is libuv's uv_kill. Signals: process.on('SIGINT') is a console
// control handler (Ctrl+C is SIGINT, Ctrl+Break SIGBREAK, as in Node); a
// 'SIGTERM' listener is accepted but never fires, since nothing on Windows
// delivers SIGTERM; SIGWINCH comes from a console-size watcher thread, the
// console having no resize notification of its own.
#define NO_OLDNAMES 1
#define WIN32_LEAN_AND_MEAN
#ifndef _WIN32_WINNT
#define _WIN32_WINNT 0x0601 // PROC_THREAD_ATTRIBUTE_HANDLE_LIST needs Vista+
#endif
#include <windows.h>
#include <tlhelp32.h>
#include <stdio.h>
#include <errno.h>
#include <io.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <wchar.h>

enum { L_EACCES = 13, L_ENOENT = 2, L_EINVAL = 22, L_ENOSYS = 38, L_ESRCH = 3, L_EBADF = 9, L_EMFILE = 24 };
enum { KFD_PLAIN = 0, KFD_SOCKET = 1 };
#define KFD_MAX 16384

// From win32io.c. The fd descriptor table (kind/inherited/…) is owned there
// and reached through accessors, so this object never binds to its layout
// (TDD-00182 Stage 1).
int kfd_kind_of(int fd);
void kfd_set_inherited(int fd);
int kfd_register(HANDLE h, int kind);
int kfd_adopt_socket(HANDLE s, int fd);
int kfd_listeners(int *out, int max);
void kfd_rr_add_worker(int lfd, unsigned long pid, int chan);
void kfd_rr_adopt(int lfd, int chan, int nonblock);
void *__kml_win_cluster_flag(void); // defined below; the spawn hands its section over
int kfd_nonblock_of(int fd);
int socketpair(int, int, int, int[2]);
HANDLE kfd_handle(int fd);
void ws_init(void);
int socket(int, int, int);
int bind(int, const void *, int);
int listen(int, int);
int accept(int, void *, int *);
int connect(int, const void *, int);
int getsockname(int, void *, int *);
int getpeername(int, void *, int *);
int close(int);

// The cluster this process belongs to, named by its primary's pid: this
// process's own unless it was spawned as a cluster worker.
static DWORD kml_cluster_primary_pid;
static DWORD kml_cluster_primary(void) {
	return kml_cluster_primary_pid ? kml_cluster_primary_pid : GetCurrentProcessId();
}

// The cluster's shared close-flag section (see __kml_win_cluster_flag below):
// created by the primary, its handle inherited by every worker at spawn.
static HANDLE kml_flag_section;   // this process's handle (primary: created; worker: inherited)
static void *kml_flag_view;       // mapped once, reused
static HANDLE kml_flag_inherited; // handed over at spawn, before main runs

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
	wchar_t *w = (wchar_t *)malloc((size_t)n * sizeof(wchar_t)); /* scratch */
	if (w) MultiByteToWideChar(CP_UTF8, 0, s, -1, w, n);
	return w;
}

static HANDLE inheritable_dup(int fd) {
	HANDLE h = kfd_handle(fd), d;
	if (h == INVALID_HANDLE_VALUE) return INVALID_HANDLE_VALUE;
	if (!DuplicateHandle(GetCurrentProcess(), h, GetCurrentProcess(), &d, 0, TRUE, DUPLICATE_SAME_ACCESS)) return INVALID_HANDLE_VALUE;
	return d;
}

// The std handle the child gets for a slot the caller wants inherited: an
// inheritable duplicate of ours, or NUL when ours is missing/invalid — the
// replacement libuv makes, so a GUI-subsystem parent (no console handles)
// still hands its children usable fds 0/1/2 (ADR-00737).
static HANDLE inheritable_std(DWORD std_which, int for_input) {
	HANDLE d;
	HANDLE h = GetStdHandle(std_which);
	if (h && h != INVALID_HANDLE_VALUE &&
	    DuplicateHandle(GetCurrentProcess(), h, GetCurrentProcess(), &d, 0, TRUE, DUPLICATE_SAME_ACCESS))
		return d;
	SECURITY_ATTRIBUTES sa = { sizeof sa, NULL, TRUE };
	return CreateFileW(L"NUL", for_input ? GENERIC_READ : GENERIC_WRITE,
	                   FILE_SHARE_READ | FILE_SHARE_WRITE, &sa, OPEN_EXISTING, 0, NULL);
}

// Case-insensitive "ends with .bat or .cmd" — the extensions CreateProcessW
// silently hands to cmd.exe when they appear on a bare command line, which
// turns quoted arguments into command injection (CVE-2024-27980). Node
// answers EINVAL for these unless the caller asked for a shell.
static int is_batch_file(const char *s) {
	size_t n = s ? strlen(s) : 0;
	if (n < 4) return 0;
	const char *ext = s + n - 4;
	return (ext[0] == '.') &&
	       ((ext[1] | 32) == 'b' ? ((ext[2] | 32) == 'a' && (ext[3] | 32) == 't')
	                             : ((ext[1] | 32) == 'c' && (ext[2] | 32) == 'm' && (ext[3] | 32) == 'd'));
}

// ADR-00972: does this spawn target resolve to the running executable? A
// compiled binary handed process.execPath re-runs main in the child (it has no
// interpreter mode), re-hitting the same spawn — an unbounded self-fork chain.
// When the target is ourselves the child's env is marked so its startup guard
// (@__kml_reject_node_interp_flags) refuses to re-run the program body. POSIX
// sets the marker in the forked child before execvp; Windows has no fork, so the
// self-spawn is detected here and the marker injected into the child's
// environment block. Best-effort path comparison: both sides go through
// GetFullPathNameW (normalizing ./.. and mixed slashes) and compare
// case-insensitively, the case-fold Win32 filesystems use.
static int is_self_spawn(const char *file, char **argv) {
	const char *cand = (file && *file) ? file : (argv && argv[0] ? argv[0] : NULL);
	if (!cand) return 0;
	wchar_t *wc = wide_alloc(cand);
	if (!wc) return 0;
	static wchar_t selfw[32768], fullself[32768], fullcand[32768];
	DWORD n = GetModuleFileNameW(NULL, selfw, 32768);
	int match = 0;
	if (n > 0 && n < 32768 &&
	    GetFullPathNameW(selfw, 32768, fullself, NULL) &&
	    GetFullPathNameW(wc, 32768, fullcand, NULL))
		match = (_wcsicmp(fullself, fullcand) == 0);
	free(wc); /* scratch */
	return match;
}

// is_self_word is is_self_spawn for one command word of a shell command: a
// path, or a bare name searched on PATH the way the shell would (.exe
// assumed).
static int is_self_word(const char *word) {
	if (strpbrk(word, "/\:")) return is_self_spawn(word, NULL);
	wchar_t *w = wide_alloc(word);
	if (!w) return 0;
	static wchar_t found[32768];
	DWORD n = SearchPathW(NULL, w, L".exe", 32768, found, NULL);
	free(w); /* scratch */
	if (n == 0 || n >= 32768) return 0;
	int len = WideCharToMultiByte(CP_UTF8, 0, found, -1, NULL, 0, NULL, NULL);
	char *u = len > 0 ? (char *)malloc((size_t)len) : NULL; /* scratch */
	if (!u) return 0;
	WideCharToMultiByte(CP_UTF8, 0, found, -1, u, len, NULL, NULL);
	int r = is_self_spawn(u, NULL);
	free(u); /* scratch */
	return r;
}

// shell_env_value is name's value in the child's given environment, or in
// ours (Windows names are case-insensitive).
static const char *shell_env_value(char **env, const char *name, size_t nl) {
	static char buf[32768];
	if (!env) {
		char key[256];
		if (nl >= sizeof key) return NULL;
		memcpy(key, name, nl);
		key[nl] = 0;
		DWORD n = GetEnvironmentVariableA(key, buf, sizeof buf);
		return n > 0 && n < sizeof buf ? buf : NULL;
	}
	for (int i = 0; env[i]; i++)
		if (_strnicmp(env[i], name, nl) == 0 && env[i][nl] == '=') return env[i] + nl + 1;
	return NULL;
}

// shell_runs_self reports whether a shell spawn starts this executable
// (ADR-01581 on Windows): cmd.exe's `/c <command>` (exec and shell: true) or
// a POSIX shell's `-c <command>`. A word in command position (the start, or
// after | & ; or a parenthesis) is compared after quotes are stripped; a
// variable (%NAME%, $NAME, ${NAME}) is looked up first. The shell is then
// marked and passes the mark to the copy it starts, whose guard exits 9.
static int shell_runs_self(char **argv, char **env) {
	if (!argv || !argv[0]) return 0;
	const char *cmd = NULL;
	int is_cmd = 0;
	for (int i = 1; argv[i] && argv[i + 1]; i++) {
		if (_stricmp(argv[i], "/c") == 0) { cmd = argv[i + 1]; is_cmd = 1; break; }
		if (strcmp(argv[i], "-c") == 0) { cmd = argv[i + 1]; break; }
	}
	if (!cmd) return 0;
	const char *p = cmd, *end = cmd + strlen(cmd);
	// cmd /s (exec's form, `""<file>" args"`) drops the command's outer
	// quotes; cmd quotes only with ", a POSIX shell with ' as well.
	if (is_cmd && end - p >= 2 && *p == '"' && end[-1] == '"') { p++; end--; }
	int cmdpos = 1;
	while (p < end) {
		if (strchr(" \t\r\n", *p)) { p++; continue; }
		if (strchr("|;&()", *p)) { cmdpos = 1; p++; continue; }
		char word[4096];
		size_t wl = 0;
		char quote = 0;
		while (p < end && (quote || !strchr(" \t\r\n|;&()<>", *p))) {
			if (quote ? *p == quote : (*p == '"' || (!is_cmd && *p == '\''))) quote = quote ? 0 : *p;
			else if (wl + 1 < sizeof word) word[wl++] = *p;
			p++;
		}
		word[wl] = 0;
		if (p < end && (*p == '<' || *p == '>')) p++;
		if (!cmdpos || wl == 0) continue;
		cmdpos = 0;
		const char *target = word;
		size_t nl = strlen(word);
		if (word[0] == '%' && nl > 2 && word[nl - 1] == '%') {
			target = shell_env_value(env, word + 1, nl - 2);
		} else if (word[0] == '$') {
			const char *name = word + 1;
			nl--;
			if (*name == '{' && nl > 1 && name[nl - 1] == '}') { name++; nl -= 2; }
			target = shell_env_value(env, name, nl);
		}
		if (target && *target && is_self_word(target)) return 1;
	}
	return 0;
}

// ---- the cluster worker's environment block (libuv's make_program_env) -------------------
// env_key_len is the length of an entry's name. A leading '=' belongs to the
// name: the hidden per-drive "=C:=C:\dir" variables.
static size_t env_key_len(const wchar_t *e) {
	const wchar_t *eq = wcschr(e[0] == L'=' ? e + 1 : e, L'=');
	return eq ? (size_t)(eq - e) : wcslen(e);
}
static int env_key_cmp(const wchar_t *a, size_t an, const wchar_t *b, size_t bn) {
	int r = CompareStringOrdinal(a, (int)an, b, (int)bn, TRUE);
	return r == CSTR_LESS_THAN ? -1 : r == CSTR_GREATER_THAN ? 1 : 0;
}
static int env_entry_cmp(const void *pa, const void *pb) {
	const wchar_t *a = *(const wchar_t *const *)pa, *b = *(const wchar_t *const *)pb;
	return env_key_cmp(a, env_key_len(a), b, env_key_len(b));
}

// build_env_block assembles the child's environment the way libuv's
// make_program_env does. The entries are either `custom` (a NULL-terminated
// UTF-8 "KEY=val" list that REPLACES the parent's, ADR-00762) or the parent's
// own block, plus this shim's `markers`. The list is de-duplicated
// case-insensitively, first spelling winning (Node does this before libuv sees
// it); the variables Windows programs cannot start without are carried over from
// the parent when a custom list omits them; and the block is sorted by name,
// case-insensitively, which CreateProcessW documents as a requirement. Returns a
// malloc'd double-NUL block, or NULL to inherit the parent's block untouched.
static wchar_t *build_env_block(char **custom, const wchar_t **markers, int nmarkers) {
	static const wchar_t *required[] = {
		L"HOMEDRIVE", L"HOMEPATH", L"LOGONSERVER", L"PATH", L"SYSTEMDRIVE", L"SYSTEMROOT",
		L"TEMP", L"USERDOMAIN", L"USERNAME", L"USERPROFILE", L"WINDIR",
	};
	enum { NREQ = sizeof required / sizeof required[0] };
	if (!custom && !nmarkers) return NULL;
	int ncustom = 0;
	if (custom) while (custom[ncustom]) ncustom++;
	wchar_t *parent = GetEnvironmentStringsW();
	int nparent = 0;
	if (!custom && parent) for (wchar_t *q = parent; *q; q += wcslen(q) + 1) nparent++;
	int cap = ncustom + nparent + nmarkers + NREQ + 1;
	wchar_t **ent = (wchar_t **)calloc((size_t)cap, sizeof *ent); /* scratch */
	char *owned = (char *)calloc((size_t)cap, 1); // entries this function malloc'd /* scratch */
	if (!ent || !owned) { free(ent); free(owned); if (parent) FreeEnvironmentStringsW(parent); return NULL; } /* scratch */
	int n = 0;
	// Markers first so that, under first-wins de-duplication, they override a
	// same-named entry from either source.
	for (int i = 0; i < nmarkers; i++) ent[n++] = (wchar_t *)markers[i];
	for (int i = 0; i < ncustom; i++) {
		wchar_t *w = wide_alloc(custom[i]);
		if (!w) continue;
		if (!wcschr(w[0] == L'=' ? w + 1 : w, L'=')) { free(w); continue; } // not KEY=val /* scratch */
		owned[n] = 1; ent[n++] = w;
	}
	if (!custom && parent) for (wchar_t *q = parent; *q; q += wcslen(q) + 1) ent[n++] = q;
	// De-duplicate, first occurrence wins.
	int m = 0;
	for (int i = 0; i < n; i++) {
		int dup = 0;
		size_t kl = env_key_len(ent[i]);
		for (int j = 0; j < m && !dup; j++) dup = env_key_cmp(ent[i], kl, ent[j], env_key_len(ent[j])) == 0;
		if (dup) { if (owned[i]) free(ent[i]); continue; } /* scratch */
		ent[m] = ent[i]; owned[m] = owned[i]; m++;
	}
	n = m;
	if (custom) {
		for (int r = 0; r < NREQ; r++) {
			size_t rl = wcslen(required[r]);
			int have = 0;
			for (int j = 0; j < n && !have; j++) have = env_key_cmp(required[r], rl, ent[j], env_key_len(ent[j])) == 0;
			if (have) continue;
			DWORD vn = GetEnvironmentVariableW(required[r], NULL, 0);
			if (!vn) continue; // the parent has none either
			wchar_t *e = (wchar_t *)malloc((rl + 1 + vn + 1) * sizeof(wchar_t)); /* scratch */
			if (!e) continue;
			memcpy(e, required[r], rl * sizeof(wchar_t));
			e[rl] = L'=';
			GetEnvironmentVariableW(required[r], e + rl + 1, vn);
			owned[n] = 1; ent[n++] = e;
		}
	}
	// qsort moves the pointers, so remember which to free by value.
	wchar_t **tofree = (wchar_t **)calloc((size_t)n + 1, sizeof *tofree); /* scratch */
	int nfree = 0;
	if (tofree) for (int i = 0; i < n; i++) if (owned[i]) tofree[nfree++] = ent[i];
	qsort(ent, (size_t)n, sizeof *ent, env_entry_cmp);
	size_t total = 1;
	for (int i = 0; i < n; i++) total += wcslen(ent[i]) + 1;
	wchar_t *block = (wchar_t *)malloc((total + 1) * sizeof(wchar_t)); /* scratch */
	if (block) {
		wchar_t *p = block;
		for (int i = 0; i < n; i++) { size_t l = wcslen(ent[i]) + 1; memcpy(p, ent[i], l * sizeof(wchar_t)); p += l; }
		if (n == 0) *p++ = 0; // an empty block is two NULs
		*p = 0;
	}
	for (int i = 0; i < nfree; i++) free(tofree[i]); /* scratch */
	free(tofree); free(ent); free(owned); /* scratch */
	if (parent) FreeEnvironmentStringsW(parent);
	return block;
}

// ---- spawn ------------------------------------------------------------------------------
// A child process is libuv's uv_spawn (klainpool.c, TDD-00252 Stage 3); what
// it asks of this layer is below. The one CreateProcessW left is the
// http.listen({ workers }) cluster re-spawn, which hands each worker the
// primary's listening sockets.

// __kml_win_is_batch_file: whether file names a .bat or .cmd, which Node
// refuses to spawn without a shell (EINVAL).
int __kml_win_is_batch_file(const char *file) {
	return is_batch_file(file);
}

// __kml_win_spawn_is_self: whether the spawn runs this executable, directly
// or through a shell's command line (ADR-00972, ADR-01581).
int __kml_win_spawn_is_self(const char *file, char **argv, char **env) {
	return (file && *file && is_self_word(file)) || shell_runs_self(argv, env);
}

// __kml_win_fd_crt: a CRT descriptor over a duplicate of fd's handle, the
// currency of libuv's Windows calls (uv_pipe_open, uv_tty_init,
// UV_INHERIT_FD), or -1 with errno set. A socket shared with a child that
// way releases only its own handle at its close.
int __kml_win_fd_crt(int fd) {
	HANDLE h = kfd_handle(fd), d;
	if (!h || h == INVALID_HANDLE_VALUE ||
	    !DuplicateHandle(GetCurrentProcess(), h, GetCurrentProcess(), &d, 0, FALSE, DUPLICATE_SAME_ACCESS)) {
		errno = L_EBADF;
		return -1;
	}
	int crt = _open_osfhandle((intptr_t)d, 0);
	if (crt < 0) {
		CloseHandle(d);
		errno = L_EMFILE;
		return -1;
	}
	if (kfd_kind_of(fd) == KFD_SOCKET) kfd_set_inherited(fd);
	return crt;
}

// __kml_win_spawn_worker(exe, argv): re-spawn this executable as a cluster
// worker (runtime_spawn_win.go). It inherits our stdio and every listening
// socket this process holds, each with the far end of a socket pair the
// primary deals that listener's connections over (win32io.c, "cluster
// round-robin"), plus the cluster's close-flag section. The handles reach
// the worker as CRT descriptors (STARTUPINFO's lpReserved2, the table the
// worker's CRT rebuilds before main, as libuv hands a child its descriptors);
// the environment names only their numbers. KML_WIN_RR is the primary's pid,
// then the listeners as fd:listener-descriptor:channel-descriptor:nonblock,
// the worker adopting each listener under the fd number it has here;
// KML_WIN_FLAG is the section's descriptor. Returns the pid, or -1.
int __kml_win_spawn_worker(const char *exe, char **argv) {
	size_t total = 1;
	int argc = 0;
	for (; argv[argc]; argc++) total += strlen(argv[argc]) * 2 + 4;
	wchar_t *cmd = (wchar_t *)malloc(total * sizeof(wchar_t) * 2); /* scratch */
	wchar_t *app = wide_alloc(exe);
	if (!cmd || !app) { free(cmd); free(app); errno = L_EINVAL; return -1; } /* scratch */
	wchar_t *p = cmd;
	for (int i = 0; i < argc; i++) {
		wchar_t *wa = wide_alloc(argv[i]);
		if (!wa) { free(cmd); free(app); errno = L_EINVAL; return -1; } /* scratch */
		if (i) *p++ = L' ';
		p = quote_arg(p, wa);
		free(wa); /* scratch */
	}
	*p = 0;

	STARTUPINFOEXW siex;
	memset(&siex, 0, sizeof siex);
	siex.StartupInfo.dwFlags = STARTF_USESTDHANDLES;
	HANDLE hin = inheritable_std(STD_INPUT_HANDLE, 1);
	HANDLE hout = inheritable_std(STD_OUTPUT_HANDLE, 0);
	HANDLE herr = inheritable_std(STD_ERROR_HANDLE, 0);
	siex.StartupInfo.hStdInput = hin; siex.StartupInfo.hStdOutput = hout; siex.StartupInfo.hStdError = herr;

	enum { RR_MAX = 16 };
	const wchar_t *markers[2];
	int nmarkers = 0;
	int rr_fd[RR_MAX], rr_near[RR_MAX], rr_n = 0;
	HANDLE rr_lh[RR_MAX], rr_ch[RR_MAX];
	wchar_t rrbuf[32 + RR_MAX * 64], flagbuf[64];
	HANDLE flag_h = INVALID_HANDLE_VALUE;
	// The worker's CRT descriptors: its stdio, then the section, then each
	// listener and its channel.
	HANDLE crt_h[4 + 2 * RR_MAX];
	int ncrt = 3;
	crt_h[0] = hin; crt_h[1] = hout; crt_h[2] = herr;
	// The close-flag section, inheritable, so a worker that is still starting
	// up when the cluster shuts down still shares the primary's page.
	if (__kml_win_cluster_flag() != (void *)(intptr_t)-1 &&
	    DuplicateHandle(GetCurrentProcess(), kml_flag_section, GetCurrentProcess(),
	                    &flag_h, 0, TRUE, DUPLICATE_SAME_ACCESS)) {
		wsprintfW(flagbuf, L"KML_WIN_FLAG=%d", ncrt);
		crt_h[ncrt++] = flag_h;
		markers[nmarkers++] = flagbuf;
	} else {
		flag_h = INVALID_HANDLE_VALUE;
	}
	int lfds[RR_MAX];
	int nl = kfd_listeners(lfds, RR_MAX);
	wchar_t *rp = rrbuf + wsprintfW(rrbuf, L"KML_WIN_RR=%lu|", (unsigned long)kml_cluster_primary());
	for (int i = 0; i < nl; i++) {
		int sv[2];
		if (socketpair(0, 0, 0, sv) != 0) continue;
		HANDLE lh = inheritable_dup(lfds[i]), ch = inheritable_dup(sv[1]);
		close(sv[1]);
		if (lh == INVALID_HANDLE_VALUE || ch == INVALID_HANDLE_VALUE) {
			if (lh != INVALID_HANDLE_VALUE) CloseHandle(lh);
			if (ch != INVALID_HANDLE_VALUE) CloseHandle(ch);
			close(sv[0]);
			continue;
		}
		rr_fd[rr_n] = lfds[i]; rr_near[rr_n] = sv[0]; rr_lh[rr_n] = lh; rr_ch[rr_n] = ch;
		rp += wsprintfW(rp, L"%s%d:%d:%d:%d", rr_n ? L";" : L"", lfds[i], ncrt, ncrt + 1, kfd_nonblock_of(lfds[i]));
		crt_h[ncrt++] = lh;
		crt_h[ncrt++] = ch;
		rr_n++;
	}
	markers[nmarkers++] = rrbuf;
	wchar_t *env = build_env_block(NULL, markers, nmarkers);
	// The CRT's inherited-descriptor block: a count, a flag byte per
	// descriptor (FOPEN | FPIPE: open, and taken as it is), then the handles.
	size_t crt_sz = sizeof(int) + (size_t)ncrt + (size_t)ncrt * sizeof(HANDLE);
	unsigned char *crt_blk = (unsigned char *)calloc(1, crt_sz); /* scratch */
	if (crt_blk) {
		memcpy(crt_blk, &ncrt, sizeof(int));
		for (int i = 0; i < ncrt; i++) {
			int open = crt_h[i] && crt_h[i] != INVALID_HANDLE_VALUE;
			crt_blk[sizeof(int) + i] = open ? 0x01 | 0x08 : 0;
			HANDLE h = open ? crt_h[i] : INVALID_HANDLE_VALUE;
			memcpy(crt_blk + sizeof(int) + ncrt + (size_t)i * sizeof(HANDLE), &h, sizeof(HANDLE));
		}
		siex.StartupInfo.cbReserved2 = (WORD)crt_sz;
		siex.StartupInfo.lpReserved2 = crt_blk;
	}

	// Exactly the handles meant for the worker (PROC_THREAD_ATTRIBUTE_HANDLE_LIST),
	// not every inheritable handle in the process (ADR-00737).
	HANDLE inherit_list[4 + 2 * RR_MAX];
	DWORD nlist = 0;
	if (hin && hin != INVALID_HANDLE_VALUE) inherit_list[nlist++] = hin;
	if (hout && hout != INVALID_HANDLE_VALUE) inherit_list[nlist++] = hout;
	if (herr && herr != INVALID_HANDLE_VALUE) inherit_list[nlist++] = herr;
	for (int i = 0; i < rr_n; i++) { inherit_list[nlist++] = rr_lh[i]; inherit_list[nlist++] = rr_ch[i]; }
	if (flag_h != INVALID_HANDLE_VALUE) inherit_list[nlist++] = flag_h;
	SIZE_T asz = 0;
	InitializeProcThreadAttributeList(NULL, 1, 0, &asz);
	LPPROC_THREAD_ATTRIBUTE_LIST attrs = asz ? (LPPROC_THREAD_ATTRIBUTE_LIST)malloc(asz) : NULL; /* scratch */
	BOOL attrok = attrs && nlist &&
	              InitializeProcThreadAttributeList(attrs, 1, 0, &asz) &&
	              UpdateProcThreadAttribute(attrs, 0, PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
	                                        inherit_list, nlist * sizeof(HANDLE), NULL, NULL);
	siex.lpAttributeList = attrok ? attrs : NULL;
	siex.StartupInfo.cb = attrok ? sizeof siex : sizeof siex.StartupInfo;

	PROCESS_INFORMATION pi;
	memset(&pi, 0, sizeof pi);
	BOOL ok = CreateProcessW(app, cmd, NULL, NULL, TRUE,
	                         CREATE_UNICODE_ENVIRONMENT | (attrok ? EXTENDED_STARTUPINFO_PRESENT : 0),
	                         env, NULL, &siex.StartupInfo, &pi);
	free(app); free(cmd); free(env); free(crt_blk); /* scratch */
	if (attrok) DeleteProcThreadAttributeList(attrs);
	free(attrs); /* scratch */
	if (hin && hin != INVALID_HANDLE_VALUE) CloseHandle(hin);
	if (hout && hout != INVALID_HANDLE_VALUE) CloseHandle(hout);
	if (herr && herr != INVALID_HANDLE_VALUE) CloseHandle(herr);
	for (int i = 0; i < rr_n; i++) { CloseHandle(rr_lh[i]); CloseHandle(rr_ch[i]); }
	if (flag_h != INVALID_HANDLE_VALUE) CloseHandle(flag_h);
	if (!ok) {
		for (int i = 0; i < rr_n; i++) close(rr_near[i]);
		errno = L_ENOENT;
		return -1;
	}
	CloseHandle(pi.hThread);
	CloseHandle(pi.hProcess);
	for (int i = 0; i < rr_n; i++) {
		kfd_set_inherited(rr_fd[i]);
		kfd_rr_add_worker(rr_fd[i], pi.dwProcessId, rr_near[i]);
	}
	return (int)pi.dwProcessId;
}

// __kml_win_getppid: process.ppid, the process that created this one (the
// ParentProcessId libuv's uv_os_getppid reads), or -1 if it cannot be read.
int __kml_win_getppid(void) {
	DWORD self = GetCurrentProcessId();
	HANDLE snap = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
	if (snap == INVALID_HANDLE_VALUE) return -1;
	PROCESSENTRY32W pe;
	pe.dwSize = sizeof pe;
	int ppid = -1;
	for (BOOL ok = Process32FirstW(snap, &pe); ok; ok = Process32NextW(snap, &pe)) {
		if (pe.th32ProcessID == self) {
			ppid = (int)pe.th32ParentProcessID;
			break;
		}
	}
	CloseHandle(snap);
	return ppid;
}

// A cluster worker does not outlive its primary: Node's worker exits when its
// channel to the primary disconnects (process.exit(0) on 'disconnect'), and a
// worker left behind keeps the listening port open through its inherited
// handle. The hand-off channel's EOF is not the signal — a primary that closes
// one server closes that channel too and lives on — so the worker waits on the
// primary's process handle itself. A pid that no longer names the primary (it
// died before this ran, or the number was reused by a process younger than this
// one) counts as gone.
static VOID CALLBACK kml_primary_gone(PVOID ctx, BOOLEAN timed_out) {
	(void)ctx; (void)timed_out;
	ExitProcess(0);
}
static void kml_watch_primary(DWORD pid) {
	HANDLE h = OpenProcess(SYNCHRONIZE | PROCESS_QUERY_LIMITED_INFORMATION, FALSE, pid);
	if (!h) ExitProcess(0);
	FILETIME pc, mc, ex, kt, ut;
	if (GetProcessTimes(h, &pc, &ex, &kt, &ut) && GetProcessTimes(GetCurrentProcess(), &mc, &ex, &kt, &ut) &&
	    CompareFileTime(&pc, &mc) > 0) ExitProcess(0);
	HANDLE wait;
	if (!RegisterWaitForSingleObject(&wait, h, kml_primary_gone, NULL, INFINITE, WT_EXECUTEONLYONCE)) CloseHandle(h);
}

// Child side of the cluster-worker spawn: adopt every inherited listener under
// the primary's fd number first (so no channel takes one of those numbers),
// then give each its hand-off channel.
__attribute__((constructor)) static void kml_win_adopt_cluster(void) {
	char buf[32 + 16 * 64];
	if (!GetEnvironmentVariableA("KML_WIN_RR", buf, sizeof buf)) return;
	ws_init();
	int fds[16], nbs[16], n = 0;
	HANDLE chans[16];
	kml_cluster_primary_pid = (DWORD)strtoul(buf, NULL, 10);
	kml_watch_primary(kml_cluster_primary_pid);
	char fb[64];
	if (GetEnvironmentVariableA("KML_WIN_FLAG", fb, sizeof fb)) {
		HANDLE fh = (HANDLE)_get_osfhandle(atoi(fb));
		if (fh != INVALID_HANDLE_VALUE) kml_flag_inherited = fh;
		SetEnvironmentVariableA("KML_WIN_FLAG", NULL);
		_putenv("KML_WIN_FLAG=");
	}
	char *list = strchr(buf, '|');
	for (char *q = list ? list + 1 : NULL; q && *q && n < 16;) {
		int fd = atoi(q);
		char *c1 = strchr(q, ':');
		char *c2 = c1 ? strchr(c1 + 1, ':') : NULL;
		if (!c2) break;
		HANDLE lh = (HANDLE)_get_osfhandle(atoi(c1 + 1));
		HANDLE ch = (HANDLE)_get_osfhandle(atoi(c2 + 1));
		char *c3 = strchr(c2 + 1, ':');
		if (kfd_adopt_socket(lh, fd) >= 0) { fds[n] = fd; chans[n] = ch; nbs[n] = c3 ? atoi(c3 + 1) : 0; n++; }
		q = strchr(c2, ';');
		if (q) q++;
	}
	for (int i = 0; i < n; i++) {
		int chan = kfd_register(chans[i], KFD_SOCKET);
		if (chan >= 0) kfd_rr_adopt(fds[i], chan, nbs[i]);
	}
	SetEnvironmentVariableA("KML_WIN_RR", NULL);
	_putenv("KML_WIN_RR=");
}

// __kml_win_cluster_flag: the cluster's shared close-flag page (two i64 words,
// zeroed), or MAP_FAILED. Under fork() this is an anonymous MAP_SHARED mapping
// every worker inherits; here it is a section whose *handle* each worker
// inherits at spawn, mapped on first use.
//
// Inherited rather than opened by name, which is what a first attempt did. A
// named section lives only while some process holds a handle or a view of it,
// and a worker still starting up when the cluster shuts down finds all of them
// gone: `CreateFileMappingW` then silently creates a *new*, zero-filled section
// under the same name, and that worker waits forever on a flag nobody will set
// again — it keeps the listening socket alive through its inherited handle, so
// the port never stops accepting. An inherited handle exists from the moment
// the worker is created and keeps the section alive by itself, which is the
// property fork's shared page has for free.
void *__kml_win_cluster_flag(void) {
	if (kml_flag_view) return kml_flag_view;
	HANDLE m = kml_flag_inherited;
	if (!m) m = CreateFileMappingW(INVALID_HANDLE_VALUE, NULL, PAGE_READWRITE, 0, 16, NULL);
	if (!m) return (void *)(intptr_t)-1;
	void *v = MapViewOfFile(m, FILE_MAP_ALL_ACCESS, 0, 0, 16);
	if (!v) return (void *)(intptr_t)-1;
	kml_flag_section = m;
	kml_flag_view = v;
	return v;
}

// __kml_win_self_exe: the running executable's path as a malloc'd UTF-8
// string, for re-spawning the program as a cluster worker.
char *__kml_win_self_exe(void) {
	wchar_t w[32768];
	DWORD n = GetModuleFileNameW(NULL, w, 32768);
	if (n == 0 || n >= 32768) return NULL;
	int len = WideCharToMultiByte(CP_UTF8, 0, w, -1, NULL, 0, NULL, NULL);
	char *s = (char *)malloc((size_t)len); /* scratch */
	if (s) WideCharToMultiByte(CP_UTF8, 0, w, -1, s, len, NULL, NULL);
	return s;
}

// ---- kill ---------------------------------------------------------------------------------
// Delivers a signal to our own process through the handler queue (defined with
// the signal machinery below); returns 1 when queued, 0 when there is no
// handler to run.
static int kml_deliver_self_signal(int sig);

int kill(int pid, int sig) {
	if (sig == 0) {
		// libuv's health check: a process that has exited but whose handle
		// is still held (by its parent, until reaped) opens fine, so the exit
		// code decides.
		HANDLE h = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, (DWORD)pid);
		if (!h) { errno = GetLastError() == ERROR_ACCESS_DENIED ? L_EACCES : L_ESRCH; return -1; }
		DWORD status = 0;
		BOOL got = GetExitCodeProcess(h, &status);
		CloseHandle(h);
		if (!got) { errno = L_EACCES; return -1; }
		if (status != STILL_ACTIVE) { errno = L_ESRCH; return -1; }
		return 0;
	}
	// A signal to OURSELVES with a registered handler is delivered to that
	// handler through the reactor (the same queue the console Ctrl handler
	// uses), mirroring POSIX kill(getpid(), sig)/raise — e.g.
	// process.kill(process.pid, 'SIGINT') for a graceful self-shutdown.
	// TerminateProcess(self) would instead kill the process before the handler
	// could run. With no handler it falls through to the default terminate.
	// A process.on() watcher is libuv's uv_signal_t: libuv's own dispatch
	// delivers it, as uv_kill does for the current process.
	if ((DWORD)pid == GetCurrentProcessId()) {
		extern int uv__signal_dispatch(int signum);
		if ((sig == 2 || sig == 1 || sig == 21 || sig == 28) && uv__signal_dispatch(sig)) return 0;
		if (kml_deliver_self_signal(sig)) return 0;
	}
	// libuv's uv_kill: SIGTERM, SIGKILL, SIGINT and SIGQUIT terminate the target
	// unconditionally; any other signal has no Windows meaning and is ENOSYS
	// (Node throws `kill ENOSYS`), rather than silently killing the process.
	if (sig != 15 && sig != 9 && sig != 2 && sig != 3) { errno = L_ENOSYS; return -1; }
	HANDLE h = OpenProcess(PROCESS_TERMINATE | PROCESS_QUERY_LIMITED_INFORMATION, FALSE, (DWORD)pid);
	if (!h) { errno = L_ESRCH; return -1; }
	int ok = TerminateProcess(h, 1);
	// As libuv: terminating a process that already exited is ESRCH, not a
	// permission error.
	DWORD status = 0;
	int gone = !ok && GetExitCodeProcess(h, &status) && status != STILL_ACTIVE;
	CloseHandle(h);
	if (ok) return 0;
	errno = gone ? L_ESRCH : L_EACCES;
	return -1;
}

// socketpair(AF_UNIX, SOCK_STREAM): a connected loopback TCP pair. libuv
// uses a named pipe for the cluster IPC channel on Windows; a socket pair
// keeps the IR's read/write/select path unchanged. The listener is open on
// 127.0.0.1 for a moment, so the accepted peer MUST be verified to be our
// own connecting socket — any local process could connect first
// (ADR-00737). Mismatched peers are dropped and the accept retried.
int socketpair(int domain, int type, int protocol, int sv[2]) {
	(void)domain; (void)type; (void)protocol;
	int lfd = socket(2, 1, 0);
	if (lfd < 0) return -1;
	struct kml_sa { uint16_t fam; uint16_t port; uint32_t addr; char zero[8]; };
	struct kml_sa sa = { 2, 0, 0x0100007f, {0} };
	int len = sizeof sa;
	if (bind(lfd, &sa, len) || listen(lfd, 1) || getsockname(lfd, &sa, &len)) { close(lfd); return -1; }
	int cfd = socket(2, 1, 0);
	if (cfd < 0) { close(lfd); return -1; }
	if (connect(cfd, &sa, len)) { close(lfd); close(cfd); return -1; }
	struct kml_sa self = { 0 };
	int slen = sizeof self;
	if (getsockname(cfd, &self, &slen)) { close(lfd); close(cfd); return -1; }
	int afd = -1;
	for (int tries = 0; tries < 16; tries++) {
		afd = accept(lfd, NULL, NULL);
		if (afd < 0) break;
		struct kml_sa peer = { 0 };
		int plen = sizeof peer;
		if (getpeername(afd, &peer, &plen) == 0 &&
		    peer.addr == 0x0100007f && peer.port == self.port) break;
		close(afd);
		afd = -1;
	}
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
// The reactor's wake packet (win32io.c): a blocked GetQueuedCompletionStatusEx
// wait ends immediately instead of noticing the raised flag on its next slice.
void __kml_win_loop_wake(void);

static BOOL WINAPI kml_ctrl_handler(DWORD ev) {
	int sig = ev == CTRL_C_EVENT ? 2 : ev == CTRL_BREAK_EVENT ? 21 : (ev == CTRL_CLOSE_EVENT || ev == CTRL_LOGOFF_EVENT || ev == CTRL_SHUTDOWN_EVENT) ? 1 : 0;
	if (!sig || !kml_sig_handlers[sig]) return FALSE;
	InterlockedExchange(&kml_sig_queued[sig], 1);
	InterlockedExchange(&__kml_win_sig_wake, 1);
	__kml_win_loop_wake();
	if (sig == 1) Sleep(3000); // give the program's SIGHUP listener its chance before the system ends the process
	return TRUE;
}

// __kml_win_sig_deliver runs the queued signals' handlers when called on the
// installing thread; returns the number delivered (0 on any other thread or
// with nothing queued). The waits in select()/nanosleep() call it and report
// EINTR when it delivered, as the POSIX shape the IR already handles.
// __kml_win_on_sig_thread: is the caller the thread that installed the handlers
// (the one __kml_win_sig_deliver will deliver on)? select() uses it to decide
// whether a pending signal should keep it from blocking — only the delivering
// thread should, so a worker's event loop never busy-polls on the global flag.
int __kml_win_on_sig_thread(void) { return __kml_win_sig_installed && GetCurrentThreadId() == kml_sig_thread; }
int __kml_win_sig_deliver(void) {
	if (!__kml_win_sig_installed || GetCurrentThreadId() != kml_sig_thread) return 0;
	if (!InterlockedExchange(&__kml_win_sig_wake, 0)) return 0;
	int n = 0;
	for (int s = 0; s < 32; s++) {
		if (InterlockedExchange(&kml_sig_queued[s], 0) && kml_sig_handlers[s]) { kml_sig_handlers[s](s); n++; }
	}
	return n;
}

// Queue a self-directed signal for the installing thread to run, the same path
// kml_ctrl_handler uses for a real Ctrl+C (see kill() above). Only the signals
// Windows can actually deliver to a handler qualify — the console controls
// SIGINT (2) / SIGHUP (1) / SIGBREAK (21) and SIGWINCH (28). Everything else,
// SIGTERM included, is terminate-only on Windows (a SIGTERM listener is accepted
// but never fires — nothing delivers it), so this returns 0 and kill() falls
// through to TerminateProcess. Returns 0 too when no handler is registered.
static int kml_deliver_self_signal(int sig) {
	if (sig != 2 && sig != 1 && sig != 21 && sig != 28) return 0;
	if (!kml_sig_handlers[sig]) return 0;
	InterlockedExchange(&kml_sig_queued[sig], 1);
	InterlockedExchange(&__kml_win_sig_wake, 1);
	__kml_win_loop_wake();
	return 1;
}

// SIGWINCH (28) has no Windows signal; libuv runs a console-resize watcher
// thread. Mirror it (ADR-00749): poll the console window size read-only
// (GetConsoleScreenBufferInfo consumes no input, so it never races the
// raw-mode reader), and on a change queue signal 28 through the same
// InterlockedExchange path the Ctrl handler uses — so __kml_win_sig_deliver
// runs the IR's handler on the main thread, exactly as for SIGINT. No
// console (piped/file) → the watcher exits at once, no resize is possible.
static DWORD WINAPI kml_winch_watcher(LPVOID arg) {
	(void)arg;
	HANDLE h = GetStdHandle(STD_OUTPUT_HANDLE);
	CONSOLE_SCREEN_BUFFER_INFO csbi;
	if (!GetConsoleScreenBufferInfo(h, &csbi)) return 0;
	SHORT w = csbi.srWindow.Right - csbi.srWindow.Left;
	SHORT ht = csbi.srWindow.Bottom - csbi.srWindow.Top;
	for (;;) {
		Sleep(200);
		if (!GetConsoleScreenBufferInfo(h, &csbi)) continue;
		SHORT nw = csbi.srWindow.Right - csbi.srWindow.Left;
		SHORT nh = csbi.srWindow.Bottom - csbi.srWindow.Top;
		if (nw != w || nh != ht) {
			w = nw; ht = nh;
			if (kml_sig_handlers[28]) {
				InterlockedExchange(&kml_sig_queued[28], 1);
				InterlockedExchange(&__kml_win_sig_wake, 1);
				__kml_win_loop_wake();
			}
		}
	}
	return 0;
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
	if (sig == 28 && handler) {
		static int winch_started;
		// Deliver runs only on the installing thread with the flag set; a
		// SIGWINCH-only program (no SIGINT) still needs both, like the ctrl path.
		kml_sig_thread = GetCurrentThreadId();
		__kml_win_sig_installed = 1;
		if (!winch_started) {
			HANDLE t = CreateThread(NULL, 0, kml_winch_watcher, NULL, 0, NULL);
			if (t) { CloseHandle(t); winch_started = 1; }
		}
	}
	return prev;
}
int sigfillset(void *set) { if (set) memset(set, 0xff, 128); return 0; }
int sigemptyset(void *set) { if (set) memset(set, 0, 128); return 0; }
int sigprocmask(int how, const void *set, void *old) { (void)how; (void)set; if (old) memset(old, 0, 128); return 0; }
int pthread_sigmask(int how, const void *set, void *old) { return sigprocmask(how, set, old); }
