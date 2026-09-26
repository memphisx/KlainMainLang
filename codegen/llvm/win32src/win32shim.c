// Windows libc shim (TDD-00177 Stage 1). Compiled once per compiler build
// into a cached object that every clang invocation on Windows links (see
// hostclang.go). It provides the handful of POSIX/GNU libc functions the
// emitted IR calls by name that the mingw-w64 UCRT target does not have, so
// the IR emitters stay platform-agnostic for this layer.
//
// Everything here is a thin libc-level function with no Node-visible
// semantics of its own; the platform *semantics* layer (event loop, process
// model, fs, signals) is separate and written against Win32 directly, not
// through this file.
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <shellapi.h> // CommandLineToArgvW, for UTF-8 process.argv
#include <bcrypt.h>
#include <errno.h>
#include <fenv.h>
#include <math.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <io.h>

// ---- argv (UTF-8) -----------------------------------------------------
// The CRT's argv is decoded in the ANSI code page, so a non-ASCII argument
// arrives as '?'. Node reads GetCommandLineW and hands UTF-8 through; do the
// same (ADR-00745). Returns a malloc'd NULL-terminated char** of UTF-8
// arguments and writes the count to *out_argc; NULL on failure so the caller
// falls back to the CRT argv. argv[0] is the program, as CommandLineToArgvW
// gives it.
char **__kml_win_argv(int *out_argc) {
	int argc = 0;
	wchar_t **wargv = CommandLineToArgvW(GetCommandLineW(), &argc);
	if (!wargv) return NULL;
	char **argv = (char **)malloc(((size_t)argc + 1) * sizeof(char *));
	if (!argv) { LocalFree(wargv); return NULL; }
	for (int i = 0; i < argc; i++) {
		int n = WideCharToMultiByte(CP_UTF8, 0, wargv[i], -1, NULL, 0, NULL, NULL);
		argv[i] = (char *)malloc(n > 0 ? (size_t)n : 1);
		if (argv[i] && n > 0) WideCharToMultiByte(CP_UTF8, 0, wargv[i], -1, argv[i], n, NULL, NULL);
		else if (argv[i]) argv[i][0] = 0;
	}
	argv[argc] = NULL;
	LocalFree(wargv);
	if (out_argc) *out_argc = argc;
	return argv;
}

// Saved console code pages, restored at exit (see kml_win_shim_init).
static UINT kml_saved_out_cp, kml_saved_in_cp;
static void kml_restore_console_cp(void) {
	if (kml_saved_out_cp) SetConsoleOutputCP(kml_saved_out_cp);
	if (kml_saved_in_cp) SetConsoleCP(kml_saved_in_cp);
}
__attribute__((constructor)) static void kml_win_shim_init(void) {
	// Node's process.stdout is synchronous — writes reach the pipe/file as they
	// happen, not withheld until exit. C stdio full-buffers a non-TTY stream, and
	// the UCRT ignores _IOLBF (treats it as full buffering), so line-buffering
	// cannot give incremental delivery here; unbuffer stdout outright, the
	// faithful match (the POSIX startup path line-buffers instead, ADR-00867).
	// Without this a cluster worker's stdout can arrive out of order with the
	// primary's, or be lost entirely when the worker is killed before an implicit
	// flush.
	setvbuf(stdout, NULL, _IONBF, 0);
	// A crash (access violation, abort) must end the process at once with a
	// non-zero status, the way a signal does on POSIX. By default Windows
	// parks a faulting process in Windows Error Reporting's dialog / WerFault
	// hand-off, which looks like a hang to whatever spawned it (a test
	// harness, a supervisor). Node's own startup disables the same boxes.
	SetErrorMode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX | SEM_NOOPENFILEERRORBOX);
	_set_abort_behavior(0, _WRITE_ABORT_MSG | _CALL_REPORTFAULT);
	// Enable VT/ANSI output processing on the console at startup, as Node
	// does, so `console.log('\x1b[31m…')` renders colour instead of printing
	// the escape literally — without the program having to enter raw mode
	// first (ADR-00744). Harmless when stdout/stderr is a pipe or file
	// (GetConsoleMode fails and it is skipped). ENABLE_VIRTUAL_TERMINAL_
	// PROCESSING is 0x0004.
	for (DWORD which = STD_OUTPUT_HANDLE; ; which = STD_ERROR_HANDLE) {
		HANDLE h = GetStdHandle(which);
		DWORD m;
		if (h && h != INVALID_HANDLE_VALUE && GetConsoleMode(h, &m))
			SetConsoleMode(h, m | 0x0004);
		if (which == STD_ERROR_HANDLE) break;
	}
	// Non-ASCII console I/O: the runtime writes UTF-8 bytes (console.log via
	// printf, args via GetCommandLineW), but a Windows console interprets them
	// in its OEM code page, so `café`/CJK/emoji render as mojibake. Node uses
	// WriteConsoleW/ReadConsoleW to sidestep the code page; the printf-based
	// output path here has no single choke point to reroute, so instead set
	// the attached console's code pages to UTF-8 (65001) — observably the same
	// for whole-line output — and restore them at exit so a later program in
	// the same real console is unaffected (ADR-00747). No-op without a console
	// (piped/file/GUI), so the redirect path that the test harness reads is
	// untouched.
	kml_saved_out_cp = GetConsoleOutputCP();
	kml_saved_in_cp = GetConsoleCP();
	if (kml_saved_out_cp && kml_saved_out_cp != CP_UTF8) SetConsoleOutputCP(CP_UTF8);
	if (kml_saved_in_cp && kml_saved_in_cp != CP_UTF8) SetConsoleCP(CP_UTF8);
	if ((kml_saved_out_cp && kml_saved_out_cp != CP_UTF8) ||
	    (kml_saved_in_cp && kml_saved_in_cp != CP_UTF8))
		atexit(kml_restore_console_cp);
}

// ---- time --------------------------------------------------------------
// The IR's timespec is { i64 sec, i64 nsec }. mingw's own struct timespec
// has a 32-bit tv_nsec followed by padding, so a winpthreads clock_gettime
// would leave the IR reading padding in the high half. Define both calls
// against the IR's layout instead; the clock ids match Linux's numbering
// (CLOCK_REALTIME 0, CLOCK_MONOTONIC 1), which is what the IR passes.
typedef struct { int64_t sec; int64_t nsec; } kml_timespec;

int clock_gettime(int clk, kml_timespec *ts) {
	if (clk == 0) {
		// GetSystemTimePreciseAsFileTime: 100ns ticks since 1601-01-01.
		FILETIME ft;
		GetSystemTimePreciseAsFileTime(&ft);
		uint64_t t = ((uint64_t)ft.dwHighDateTime << 32) | ft.dwLowDateTime;
		t -= 116444736000000000ULL; // 1601 -> 1970 epoch
		ts->sec = (int64_t)(t / 10000000ULL);
		ts->nsec = (int64_t)(t % 10000000ULL) * 100;
		return 0;
	}
	static LARGE_INTEGER freq;
	if (freq.QuadPart == 0) QueryPerformanceFrequency(&freq);
	LARGE_INTEGER c;
	QueryPerformanceCounter(&c);
	ts->sec = c.QuadPart / freq.QuadPart;
	ts->nsec = (int64_t)((double)(c.QuadPart % freq.QuadPart) * 1e9 / (double)freq.QuadPart);
	return 0;
}

// Signal wake-up (win32proc.c, ADR-00728): with a console control handler
// installed, the sleep is taken in slices and a raised flag ends it early
// with EINTR and the remaining time in rem, as a POSIX signal would.
extern int __kml_win_sig_installed;
int __kml_win_sig_deliver(void);
void __kml_win_hr_sleep_us(int64_t us); // win32io.c: high-resolution waitable-timer sleep
int nanosleep(const kml_timespec *req, kml_timespec *rem) {
	int64_t us = req->sec * 1000000 + (req->nsec + 999) / 1000; // sub-ms precision
	if (us < 0) us = 0;
	if (!__kml_win_sig_installed) { __kml_win_hr_sleep_us(us); return 0; }
	// Slices of at most 50 ms between signal checks, each a high-resolution
	// sleep against a monotonic (QPC) deadline, so an installed handler does
	// not drop the sleep back to the ~15.6 ms system tick.
	kml_timespec now;
	clock_gettime(1, &now);
	int64_t deadline = now.sec * 1000000 + now.nsec / 1000 + us;
	for (;;) {
		clock_gettime(1, &now);
		int64_t left = deadline - (now.sec * 1000000 + now.nsec / 1000);
		if (left < 0) left = 0;
		if (__kml_win_sig_deliver()) {
			if (rem) { rem->sec = left / 1000000; rem->nsec = (left % 1000000) * 1000; }
			errno = EINTR;
			return -1;
		}
		if (left == 0) return 0;
		__kml_win_hr_sleep_us(left > 50000 ? 50000 : left);
	}
}

// ---- stdio extensions ----------------------------------------------------
int64_t kml_fd_write(int fd, const void *buf, size_t n) __asm__("write"); // win32io.c's write(); io.h declares a different one
HANDLE kfd_handle(int fd);                         // win32io.c
int dprintf(int fd, const char *fmt, ...) {
	va_list ap;
	va_start(ap, fmt);
	char stackbuf[512];
	va_list ap2;
	va_copy(ap2, ap);
	int n = vsnprintf(stackbuf, sizeof stackbuf, fmt, ap);
	va_end(ap);
	if (n < 0) { va_end(ap2); return n; }
	char *buf = stackbuf;
	if ((size_t)n >= sizeof stackbuf) {
		buf = (char *)malloc((size_t)n + 1);
		if (!buf) { va_end(ap2); return -1; }
		vsnprintf(buf, (size_t)n + 1, fmt, ap2);
	}
	va_end(ap2);
	int w = (int)kml_fd_write(fd, buf, (size_t)n); // the fd layer's write: fd may be a socket or an owned pipe end, which no CRT fd wraps
	if (buf != stackbuf) free(buf);
	return w;
}

int64_t getline(char **lineptr, size_t *n, FILE *stream) {
	if (!lineptr || !n || !stream) return -1;
	if (!*lineptr || *n == 0) {
		*n = 128;
		*lineptr = (char *)malloc(*n);
		if (!*lineptr) return -1;
	}
	size_t len = 0;
	int c;
	while ((c = fgetc(stream)) != EOF) {
		if (len + 2 > *n) {
			size_t nn = *n * 2;
			char *p = (char *)realloc(*lineptr, nn);
			if (!p) return -1;
			*lineptr = p;
			*n = nn;
		}
		(*lineptr)[len++] = (char)c;
		if (c == '\n') break;
	}
	if (len == 0 && c == EOF) return -1;
	(*lineptr)[len] = '\0';
	return (int64_t)len;
}

// ---- environment ---------------------------------------------------------
// process.env is the live Win32 environment block, read and written through
// the wide API as Node does (libuv's uv_os_getenv/uv_os_setenv), not the UCRT's
// startup snapshot: a variable another layer set with SetEnvironmentVariableW
// (chdir's "=X:" drive directories, the spawn shim) is visible, a non-ASCII
// value round-trips as UTF-8 instead of through the ANSI code page, and a child
// spawned later inherits every write. Lookup is case-insensitive, as the OS's is.
static wchar_t *env_wide(const char *u) {
	int n = MultiByteToWideChar(CP_UTF8, 0, u, -1, NULL, 0);
	if (n <= 0) return NULL;
	wchar_t *w = (wchar_t *)malloc((size_t)n * sizeof(wchar_t));
	if (w) MultiByteToWideChar(CP_UTF8, 0, u, -1, w, n);
	return w;
}

// The C contract lets a later getenv call reuse the storage, and every caller
// here copies the result at once; one buffer per thread keeps Workers apart.
char *getenv(const char *name) {
	static _Thread_local char *out;
	if (!name || !*name) return NULL;
	wchar_t *wn = env_wide(name);
	if (!wn) return NULL;
	wchar_t small[512], *wv = small;
	SetLastError(0);
	DWORD n = GetEnvironmentVariableW(wn, wv, 512);
	if (n >= 512) {
		wv = (wchar_t *)malloc((size_t)(n + 1) * sizeof(wchar_t));
		if (!wv) { free(wn); return NULL; }
		n = GetEnvironmentVariableW(wn, wv, n + 1);
	}
	char *res = NULL;
	if (n != 0 || GetLastError() != ERROR_ENVVAR_NOT_FOUND) {
		wv[n] = 0;
		int un = WideCharToMultiByte(CP_UTF8, 0, wv, -1, NULL, 0, NULL, NULL);
		char *nb = un > 0 ? (char *)realloc(out, (size_t)un) : NULL;
		if (nb) {
			out = nb;
			WideCharToMultiByte(CP_UTF8, 0, wv, -1, out, un, NULL, NULL);
			res = out;
		}
	}
	if (wv != small) free(wv);
	free(wn);
	return res;
}

int setenv(const char *name, const char *value, int overwrite) {
	if (!name || !*name || strchr(name + 1, '=')) { errno = 22; return -1; } // EINVAL ("=X:" names keep their leading '=')
	if (!overwrite && getenv(name)) return 0;
	wchar_t *wn = env_wide(name), *wv = env_wide(value ? value : "");
	int r = -1;
	if (wn && wv) {
		// The wide CRT call keeps the CRT's own copy in step for C libraries that
		// read it directly (the narrow one would re-encode through the ANSI code
		// page); it rejects an empty value and the "=X:" names, which go to the
		// OS block alone.
		_wputenv_s(wn, wv);
		if (SetEnvironmentVariableW(wn, wv)) r = 0;
	}
	free(wn); free(wv);
	return r;
}

int unsetenv(const char *name) {
	if (!name || !*name) { errno = 22; return -1; }
	wchar_t *wn = env_wide(name);
	if (!wn) return -1;
	_wputenv_s(wn, L"");
	SetEnvironmentVariableW(wn, NULL);
	free(wn);
	return 0;
}

// ---- sysconf ---------------------------------------------------------------
// Only the ids the IR emits (Linux numbering): _SC_CLK_TCK 2, _SC_PAGESIZE
// 30, _SC_NPROCESSORS_ONLN 84, _SC_PHYS_PAGES 85, _SC_AVPHYS_PAGES 86.
int64_t sysconf(int name) {
	SYSTEM_INFO si;
	GetSystemInfo(&si);
	switch (name) {
	case 2: return 100;
	case 30: return (int64_t)si.dwPageSize;
	case 84: return (int64_t)si.dwNumberOfProcessors;
	case 85:
	case 86: {
		MEMORYSTATUSEX ms;
		ms.dwLength = sizeof ms;
		if (!GlobalMemoryStatusEx(&ms)) return -1;
		uint64_t bytes = name == 85 ? ms.ullTotalPhys : ms.ullAvailPhys;
		return (int64_t)(bytes / si.dwPageSize);
	}
	default: return -1;
	}
}

// ---- math ------------------------------------------------------------------
// llvm.roundeven.f64 lowers to a libm `roundeven` call; UCRT lacks it.
double roundeven(double x) {
	int save = fegetround();
	fesetround(FE_TONEAREST);
	double r = nearbyint(x);
	fesetround(save);
	return r;
}

// ---- CSPRNG --------------------------------------------------------------
// getrandom(buf, n, flags) → BCryptGenRandom, the OS CSPRNG Node itself
// uses on Windows. Returns n on success, -1 on failure, like the Linux call.
int64_t getrandom(void *buf, size_t n, unsigned flags) {
	(void)flags;
	NTSTATUS st = BCryptGenRandom(NULL, (PUCHAR)buf, (ULONG)n, BCRYPT_USE_SYSTEM_PREFERRED_RNG);
	return st == 0 ? (int64_t)n : -1;
}

// ---- process ---------------------------------------------------------------
// isatty: the UCRT's _isatty() answers true for any character device,
// including NUL (what a Go test harness hands a child as stdin), so it is
// not "is a terminal". Node asks the console subsystem, which is the
// faithful test: GetConsoleMode succeeds only on a real console handle.
int isatty(int fd) {
	HANDLE h = kfd_handle(fd); // any fd: a pool fd (socket, pipe end) has no CRT handle to ask for
	if (h == INVALID_HANDLE_VALUE) return 0;
	DWORD mode;
	return GetConsoleMode(h, &mode) ? 1 : 0;
}

// process.memoryUsage().rss — the instantaneous working set, which is what
// Node reports on Windows (uv_resident_set_memory → GetProcessMemoryInfo).
#include <psapi.h>
int64_t __kml_current_rss_bytes(void) {
	PROCESS_MEMORY_COUNTERS pmc;
	pmc.cb = sizeof pmc;
	if (!GetProcessMemoryInfo(GetCurrentProcess(), &pmc, sizeof pmc)) return 0;
	return (int64_t)pmc.WorkingSetSize;
}

// ---- os.cpus() ----------------------------------------------------------------
// Per-CPU model/speed/times the way Node's uv_cpu_info gets them on Windows:
// model and MHz from the registry (HARDWARE\DESCRIPTION\System\
// CentralProcessor\<n>: ProcessorNameString, ~MHz) and times from
// NtQuerySystemInformation(SystemProcessorPerformanceInformation), reported
// in milliseconds. `nice` is always 0 there, as in Node.
typedef struct { int64_t idle, kernel, user, dpc, irq; uint32_t interrupts; } kml_sppi;
typedef int (WINAPI *kml_ntqsi_t)(int, void *, unsigned long, unsigned long *);

int __kml_win_cpu_count(void) {
	SYSTEM_INFO si;
	GetSystemInfo(&si);
	return (int)si.dwNumberOfProcessors;
}

// Fills model (UTF-8, cap bytes), *mhz, and times[5] = user, nice, sys,
// idle, irq (ms) for CPU i. Returns 0 on success.
int __kml_win_cpu_info(int i, char *model, int cap, int64_t *mhz, int64_t times[5]) {
	char key[96];
	snprintf(key, sizeof key, "HARDWARE\\DESCRIPTION\\System\\CentralProcessor\\%d", i);
	HKEY h;
	strncpy(model, "unknown", (size_t)cap);
	model[cap - 1] = 0;
	*mhz = 0;
	if (RegOpenKeyExA(HKEY_LOCAL_MACHINE, key, 0, KEY_QUERY_VALUE, &h) == ERROR_SUCCESS) {
		DWORD type, n = (DWORD)cap;
		if (RegQueryValueExA(h, "ProcessorNameString", NULL, &type, (LPBYTE)model, &n) != ERROR_SUCCESS) strncpy(model, "unknown", (size_t)cap);
		model[cap - 1] = 0;
		// Node trims the registry's padded name.
		char *e = model + strlen(model);
		while (e > model && (e[-1] == ' ' || e[-1] == '\t')) *--e = 0;
		DWORD m = 0; n = sizeof m;
		if (RegQueryValueExA(h, "~MHz", NULL, &type, (LPBYTE)&m, &n) == ERROR_SUCCESS) *mhz = (int64_t)m;
		RegCloseKey(h);
	}
	times[0] = times[1] = times[2] = times[3] = times[4] = 0;
	static kml_ntqsi_t ntqsi;
	if (!ntqsi) *(FARPROC *)&ntqsi = GetProcAddress(GetModuleHandleA("ntdll.dll"), "NtQuerySystemInformation");
	if (!ntqsi) return 0;
	int n = __kml_win_cpu_count();
	kml_sppi *arr = (kml_sppi *)calloc((size_t)n, sizeof *arr);
	if (!arr) return 0;
	unsigned long got = 0;
	if (ntqsi(8 /* SystemProcessorPerformanceInformation */, arr, (unsigned long)(sizeof *arr * (size_t)n), &got) == 0 && i < n) {
		// 100ns ticks → ms. KernelTime includes idle and DPC/IRQ time, like libuv subtracts.
		times[0] = arr[i].user / 10000;
		times[2] = (arr[i].kernel - arr[i].idle) / 10000;
		times[3] = arr[i].idle / 10000;
		times[4] = arr[i].irq / 10000;
	}
	free(arr);
	return 0;
}

// ---- string extensions -----------------------------------------------------
// strcasestr (GNU/BSD): case-insensitive substring search, ASCII case folding
// like glibc's in the C locale.
char *strcasestr(const char *hay, const char *needle) {
	if (!*needle) return (char *)hay;
	for (; *hay; hay++) {
		const char *h = hay, *n = needle;
		while (*h && *n) {
			int a = *h, b = *n;
			if (a >= 'A' && a <= 'Z') a += 32;
			if (b >= 'A' && b <= 'Z') b += 32;
			if (a != b) break;
			h++; n++;
		}
		if (!*n) return (char *)hay;
	}
	return NULL;
}

// os.homedir() (Windows): libuv's uv_os_homedir — a set USERPROFILE wins;
// without one (a service, a stripped environment) the profile directory comes
// from the process token via GetUserProfileDirectoryW, so the call still
// answers instead of throwing. userenv.dll is bound at run time so programs that
// never ask carry no import for it. Returns a malloc'd UTF-8 string or NULL.
char *__kml_os_homedir(void) {
	const char *p = getenv("USERPROFILE");
	if (p && *p) {
		char *out = (char *)malloc(strlen(p) + 1);
		if (out) strcpy(out, p);
		return out;
	}
	HMODULE ue = LoadLibraryW(L"userenv.dll");
	if (!ue) return NULL;
	typedef BOOL (WINAPI *gupd_fn)(HANDLE, LPWSTR, LPDWORD);
	gupd_fn gupd = (gupd_fn)(void *)GetProcAddress(ue, "GetUserProfileDirectoryW");
	HANDLE tok = NULL;
	char *out = NULL;
	if (gupd && OpenProcessToken(GetCurrentProcess(), TOKEN_READ, &tok)) {
		DWORD n = 0;
		gupd(tok, NULL, &n); // sizing call: fails with ERROR_INSUFFICIENT_BUFFER, n = cells
		wchar_t *w = n ? (wchar_t *)malloc((size_t)n * sizeof(wchar_t)) : NULL;
		if (w && gupd(tok, w, &n)) {
			int un = WideCharToMultiByte(CP_UTF8, 0, w, -1, NULL, 0, NULL, NULL);
			if (un > 0 && (out = (char *)malloc((size_t)un))) WideCharToMultiByte(CP_UTF8, 0, w, -1, out, un, NULL, NULL);
		}
		free(w);
		CloseHandle(tok);
	}
	FreeLibrary(ue);
	return out;
}

// os.tmpdir() (Windows): TEMP, then TMP, then <SystemRoot|windir>\temp, with
// one trailing backslash stripped unless it names a drive root — Node's
// exact algorithm (lib/os.js) (ADR-00739). Returns a malloc'd UTF-8 string.
char *__kml_os_tmpdir(void) {
	const char *p = getenv("TEMP");
	if (!p || !*p) p = getenv("TMP");
	char *out;
	if (p && *p) {
		out = (char *)malloc(strlen(p) + 1);
		if (!out) return NULL;
		strcpy(out, p);
	} else {
		const char *root = getenv("SystemRoot");
		if (!root || !*root) root = getenv("windir");
		if (!root || !*root) root = "C:\\Windows";
		out = (char *)malloc(strlen(root) + 6);
		if (!out) return NULL;
		strcpy(out, root);
		strcat(out, "\\temp");
	}
	size_t n = strlen(out);
	if (n > 1 && out[n - 1] == '\\' && !(n >= 2 && out[n - 2] == ':')) out[n - 1] = 0;
	return out;
}
