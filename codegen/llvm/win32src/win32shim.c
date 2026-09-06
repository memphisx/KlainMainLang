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
#include <bcrypt.h>
#include <fenv.h>
#include <math.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <io.h>

// ---- stdin handle -----------------------------------------------------
// The IR loads a `FILE*` from a global (glibc: `stdin`, macOS: `__stdinp`);
// the UCRT has no such data symbol — `stdin` is a macro over
// __acrt_iob_func(0) — so expose one under this project's own name.
FILE *__kml_win_stdin;
__attribute__((constructor)) static void kml_win_shim_init(void) {
	__kml_win_stdin = stdin;
	// A crash (access violation, abort) must end the process at once with a
	// non-zero status, the way a signal does on POSIX. By default Windows
	// parks a faulting process in Windows Error Reporting's dialog / WerFault
	// hand-off, which looks like a hang to whatever spawned it (a test
	// harness, a supervisor). Node's own startup disables the same boxes.
	SetErrorMode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX | SEM_NOOPENFILEERRORBOX);
	_set_abort_behavior(0, _WRITE_ABORT_MSG | _CALL_REPORTFAULT);
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

int nanosleep(const kml_timespec *req, kml_timespec *rem) {
	(void)rem;
	int64_t ms = req->sec * 1000 + (req->nsec + 999999) / 1000000;
	if (ms < 0) ms = 0;
	Sleep((DWORD)ms);
	return 0;
}

// ---- stdio extensions ----------------------------------------------------
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
	int w = _write(fd, buf, (unsigned)n);
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
// _putenv_s updates the CRT copy *and* the Win32 process environment, so
// a child spawned later sees it — same observable effect as POSIX setenv.
int setenv(const char *name, const char *value, int overwrite) {
	if (!overwrite && getenv(name)) return 0;
	return _putenv_s(name, value) == 0 ? 0 : -1;
}

int unsetenv(const char *name) {
	return _putenv_s(name, "") == 0 ? 0 : -1;
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
	HANDLE h = (HANDLE)_get_osfhandle(fd);
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
