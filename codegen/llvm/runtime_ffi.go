package llvm

// runtime_ffi.go — libdl decls for node:ffi (TDD-00164). dlopen/dlsym/dlclose
// are the exact ensure*()-declared-libc-primitive shape os/fs already use. On
// Linux the historical -ldl is still requested (a no-op stub archive on glibc
// ≥ 2.34, required on older ones); on macOS the symbols live in libSystem.
// Windows has no libdl, so FFIWinDlShimSource provides these four symbols on
// top of LoadLibrary/GetProcAddress (wired in below).

// ffiRTLDFlags returns the host's RTLD_NOW|RTLD_LOCAL value for dlopen(2).
// RTLD_NOW is 0x2 on both Linux (glibc/musl) and macOS; RTLD_LOCAL is 0 on
// Linux and 0x4 on macOS (both are the default there anyway — passed
// explicitly so the emitted IR states the intended semantics). Windows's shim
// ignores the flags (LoadLibrary has no lazy/global-scope knobs), so 0.
func ffiRTLDFlags() int {
	switch targetGOOS() {
	case "darwin":
		return 0x2 | 0x4
	case "windows":
		return 0
	}
	return 0x2
}

// ensureFFIDl declares the libdl surface exactly once. On Windows the four
// symbols are provided by FFIWinDlShimSource (flagged for linking here); on
// POSIX they come from libdl/libSystem.
func (e *Emitter) ensureFFIDl() {
	if e.usedFFIDl {
		return
	}
	e.usedFFIDl = true
	if targetGOOS() == "linux" {
		e.requireLink("dl")
	}
	e.ensureStrHeaderRuntime() // __kml_str_from_cstr for dlerror()/string returns
	e.emitGlobal("declare ptr @dlopen(ptr noundef, i32 noundef)")
	e.emitGlobal("declare ptr @dlsym(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @dlclose(ptr noundef)")
	e.emitGlobal("declare ptr @dlerror()")
}

// FFIWinDlShimSource is the Windows implementation of the four libdl symbols
// node:ffi calls, backed by LoadLibrary/GetProcAddress/FreeLibrary. dlopen(NULL)
// — the "current process image" handle a POSIX program uses to reach libc — has
// no direct Windows equivalent (there is no global symbol scope), so it returns
// a sentinel whose dlsym searches the loaded CRT + core system modules, where a
// program's libc-level symbols (qsort, strlen, …) actually live.
func FFIWinDlShimSource() string {
	return `#include <windows.h>
#include <stdint.h>
#include <string.h>

#define KML_DL_SELF ((void *)(intptr_t)-1)

void *dlopen(const char *path, int flags) {
	(void)flags;
	if (!path) return KML_DL_SELF; // dlopen(NULL): the process image (see dlsym)
	// The path is UTF-8 (every string in a compiled program is), so it crosses
	// the wide boundary rather than the ANSI code page: a library under a
	// non-ASCII directory loads.
	int wn = MultiByteToWideChar(CP_UTF8, 0, path, -1, NULL, 0);
	if (wn <= 0) { SetLastError(ERROR_INVALID_NAME); return NULL; }
	wchar_t *w = (wchar_t *)HeapAlloc(GetProcessHeap(), 0, (SIZE_T)wn * sizeof(wchar_t));
	if (!w) { SetLastError(ERROR_NOT_ENOUGH_MEMORY); return NULL; }
	MultiByteToWideChar(CP_UTF8, 0, path, -1, w, wn);
	HMODULE m = LoadLibraryW(w);
	DWORD err = GetLastError();
	HeapFree(GetProcessHeap(), 0, w);
	SetLastError(err);
	return (void *)m;
}

void *dlsym(void *handle, const char *name) {
	if (handle == KML_DL_SELF) {
		// The modules that together make up "the process image" a POSIX
		// dlopen(NULL) resolves against: the UCRT and legacy CRT (qsort, strlen,
		// malloc, …), the exe itself, and the core system DLLs.
		static const char *mods[] = { "ucrtbase.dll", "msvcrt.dll", "api-ms-win-crt-utility-l1-1-0.dll", NULL, "kernel32.dll", "user32.dll" };
		for (int i = 0; i < (int)(sizeof mods / sizeof mods[0]); i++) {
			HMODULE m = mods[i] ? GetModuleHandleA(mods[i]) : GetModuleHandleA(NULL);
			if (!m) continue;
			FARPROC p = GetProcAddress(m, name);
			if (p) return (void *)(uintptr_t)p;
		}
		// The POSIX names the CRT only exports underscored (getpid, strdup,
		// fileno, …): a C program reaches them through the import library's
		// alias, so the process-image lookup honours the same alias.
		size_t nl = strlen(name);
		if (name[0] != '_' && nl < 126) {
			char alias[128];
			alias[0] = '_';
			memcpy(alias + 1, name, nl + 1);
			for (int i = 0; i < 2; i++) {
				HMODULE m = GetModuleHandleA(mods[i]);
				if (!m) continue;
				FARPROC p = GetProcAddress(m, alias);
				if (p) return (void *)(uintptr_t)p;
			}
		}
		return NULL;
	}
	return (void *)(uintptr_t)GetProcAddress((HMODULE)handle, name);
}

int dlclose(void *handle) {
	if (handle == KML_DL_SELF) return 0; // the process image is never unloaded
	return FreeLibrary((HMODULE)handle) ? 0 : -1;
}

static char kml_dlerr[256];
char *dlerror(void) {
	DWORD e = GetLastError();
	if (!e) return NULL;
	DWORD n = FormatMessageA(FORMAT_MESSAGE_FROM_SYSTEM | FORMAT_MESSAGE_IGNORE_INSERTS,
	                         NULL, e, 0, kml_dlerr, sizeof kml_dlerr, NULL);
	if (!n) strcpy(kml_dlerr, "unknown error");
	SetLastError(0);
	return kml_dlerr;
}
`
}
