/* dynimport.c — the dlopen shim of -dynamic-import=lazy islands (TDD-00056, in C per TDD-00240). */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#if defined(_WIN32)
/* TDD-00177 Stage 5: islands are DLLs beside the executable, loaded with
   LoadLibrary; GetModuleFileName locates the executable. */
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#define KML_ISLAND_EXT ".dll"
#define RTLD_NOW 0
#define RTLD_LOCAL 0
/* Paths are UTF-8 on both sides of the wide boundary, never the ANSI code
   page: an install directory with non-ASCII characters still loads. */
static void *dlopen(const char *p, int f) {
  (void)f;
  int n = MultiByteToWideChar(CP_UTF8, 0, p, -1, NULL, 0);
  if (n <= 0) return NULL;
  wchar_t *w = (wchar_t *)malloc((size_t)n * sizeof(wchar_t));
  if (!w) return NULL;
  MultiByteToWideChar(CP_UTF8, 0, p, -1, w, n);
  HMODULE m = LoadLibraryW(w);
  DWORD err = GetLastError();
  free(w);
  SetLastError(err);
  return (void *)m;
}
static void *dlsym(void *h, const char *s) { return (void *)GetProcAddress((HMODULE)h, s); }
static const char *dlerror(void) { static char b[64]; snprintf(b, sizeof b, "error %lu", (unsigned long)GetLastError()); return b; }
#elif defined(__APPLE__)
#include <dlfcn.h>
#include <mach-o/dyld.h>
#define KML_ISLAND_EXT ".dylib"
#else
#include <dlfcn.h>
#include <unistd.h>
#define KML_ISLAND_EXT ".so"
#endif

static int kml_self_path(char *buf, unsigned long cap) {
#if defined(_WIN32)
  DWORD wcap = 32768;
  wchar_t *w = (wchar_t *)malloc(wcap * sizeof(wchar_t));
  if (!w) return -1;
  DWORD n = GetModuleFileNameW(NULL, w, wcap);
  int ok = n != 0 && n < wcap && WideCharToMultiByte(CP_UTF8, 0, w, -1, buf, (int)cap, NULL, NULL) != 0;
  free(w);
  return ok ? 0 : -1;
#elif defined(__APPLE__)
  unsigned int size = (unsigned int)cap;
  if (_NSGetExecutablePath(buf, &size) != 0) return -1;
  return 0;
#else
  ssize_t n = readlink("/proc/self/exe", buf, cap - 1);
  if (n < 0) return -1;
  buf[n] = '\0';
  return 0;
#endif
}

// Open the shared library for the given hash beside the executable
// (idempotent: dlopen refcounts). Aborts with a clear message on failure —
// a missing library is a deployment error worth surfacing loudly.
void *__kml_dynimport_open(const char *hash) {
  char exe[4096];
  if (kml_self_path(exe, sizeof(exe)) != 0) {
    fprintf(stderr, "dynamic import: cannot locate the running executable\n");
    abort();
  }
  char so[4200];
  snprintf(so, sizeof(so), "%s.d/%s%s", exe, hash, KML_ISLAND_EXT);
  void *h = dlopen(so, RTLD_NOW | RTLD_LOCAL);
  if (!h) {
    fprintf(stderr, "dynamic import: cannot load island %s: %s\n", so, dlerror());
    abort();
  }
  return h;
}

// Load the isolated island for the given hash, run its top-level once (init
// self-guards), and return the dlopen handle so the caller can dlsym its
// export accessors.
void *__kml_dynimport_load(const char *hash) {
  void *h = __kml_dynimport_open(hash);
  char sym[128];
  snprintf(sym, sizeof(sym), "__kml_dynmod_%s_init", hash);
  void (*init)(void) = (void (*)(void))dlsym(h, sym);
  if (!init) {
    fprintf(stderr, "dynamic import: island %s missing %s\n", hash, sym);
    abort();
  }
  init();
  return h;
}

// Resolve one export accessor symbol out of a loaded island handle.
void *__kml_dynimport_sym(void *handle, const char *symname) {
  void *p = dlsym(handle, symname);
  if (!p) {
    fprintf(stderr, "dynamic import: island export %s not found\n", symname);
    abort();
  }
  return p;
}
