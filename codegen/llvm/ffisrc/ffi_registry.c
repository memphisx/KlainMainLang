/* ffi_registry.c — node:ffi DynamicLibrary runtime state (TDD-00229 Stage C).
 *
 * Mirrors Node's DynamicLibrary (src/node_ffi.cc): a `symbols` table
 * (name -> address) and a `functions` table (name -> resolved function),
 * both std::unordered_map-ordered through the kml_umap C API
 * (ffi_umap_stl.cc on POSIX, ffi_umap_msvc.c on Windows), so enumeration
 * order is Node's. Entry points follow Node's ResolveSymbol /
 * PrepareFunction / GetSymbol / Close step for step — including which table
 * each operation inserts into — and report failures as status codes; the
 * compiler turns those into the matching JS errors (emit_ffi.go), since the
 * error objects are built on the IR side.
 *
 * The first two words of KmlFfiLib are the compiler-visible `__kml_handle`
 * and `path` fields of the DynamicLibrary object type. */

#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* The libdl surface — libdl/libSystem on POSIX, the LoadLibrary shim on
   Windows (runtime_ffi.go), which has no <dlfcn.h>. RTLD_LAZY is 1 on Linux
   and macOS alike; the Windows shim ignores the flags. */
void *dlopen(const char *path, int flags);
void *dlsym(void *handle, const char *name);
int dlclose(void *handle);
char *dlerror(void);
#define KFFI_RTLD_LAZY 1

typedef struct kml_umap kml_umap;
kml_umap *kml_umap_new(void);
int kml_umap_emplace(kml_umap *u, const char *key, size_t len, void *val);
int kml_umap_find(kml_umap *u, const char *key, size_t len, void **out);
size_t kml_umap_size(kml_umap *u);
void kml_umap_each(kml_umap *u, void (*cb)(void *ctx, const char *key, size_t len, void *val), void *ctx);
void kml_umap_clear(kml_umap *u);

typedef struct KmlFfiLib {
    void *handle;      /* field 0: `__kml_handle` */
    char *path;        /* field 1: `path` (a KML string; "" for the process image) */
    long long closed;
    kml_umap *syms;    /* name -> address */
    kml_umap *fns;     /* name -> KmlFfiFn* */
} KmlFfiLib;

typedef struct KmlFfiFn {
    void *cfn;         /* the resolved C symbol */
    const char *sig;   /* canonical signature key (a compile-time constant) */
    void *rec;         /* the function object handed to JS (set at commit) */
    KmlFfiLib *lib;
    char *name;        /* KML string */
} KmlFfiFn;

/* Status codes, matched in emit_ffi.go (ffiStatus*). */
enum {
    KFFI_OK = 0,
    KFFI_CLOSED = 1,   /* Error ERR_FFI_LIBRARY_CLOSED */
    KFFI_DLSYM = 2,    /* Error ERR_FFI_CALL_FAILED "dlsym failed: …" */
    KFFI_SIGDIFF = 3,  /* TypeError ERR_INVALID_ARG_VALUE */
    KFFI_DLOPEN = 4,   /* Error ERR_FFI_CALL_FAILED "dlopen failed: …" */
};

static _Thread_local char *kffi_err;

static char *kml_str(const char *s, size_t n) {
    char *base = (char *)malloc(8 + n + 1);
    *(long long *)base = (long long)n;
    memcpy(base + 8, s, n);
    base[8 + n] = 0;
    return base + 8;
}

static void set_err(const char *prefix, const char *detail) {
    size_t n = strlen(prefix) + strlen(detail);
    char *buf = (char *)malloc(n + 1);
    snprintf(buf, n + 1, "%s%s", prefix, detail);
    kffi_err = kml_str(buf, n);
    free(buf);
}

/* The message of the last failing call on this thread, as a KML string. */
char *__kml_ffi_errmsg(void) { return kffi_err ? kffi_err : kml_str("", 0); }

/* uv_dlopen: dlopen(path, RTLD_LAZY) (Node's flags, which also appear in
 * macOS's dlerror text). A null path opens the process image. */
long long __kml_ffi_open(char *path, KmlFfiLib **out) {
    void *h = dlopen(path, KFFI_RTLD_LAZY);
    if (!h) {
        const char *d = dlerror();
        set_err("dlopen failed: ", d ? d : "unknown error");
        return KFFI_DLOPEN;
    }
    KmlFfiLib *lib = (KmlFfiLib *)calloc(1, sizeof(KmlFfiLib));
    lib->handle = h;
    lib->path = path ? path : kml_str("", 0);
    lib->syms = kml_umap_new();
    lib->fns = kml_umap_new();
    *out = lib;
    return KFFI_OK;
}

/* ResolveSymbol: the cached address, else a fresh dlsym. Like uv_dlsym, a
 * NULL address is only a failure when dlerror() reports one. */
static long long resolve(KmlFfiLib *lib, const char *name, size_t len, void **out) {
    if (lib->closed) return KFFI_CLOSED;
    if (kml_umap_find(lib->syms, name, len, out)) return KFFI_OK;
    dlerror();
    void *p = dlsym(lib->handle, name);
#ifdef _WIN32
    /* uv_dlsym on Windows: GetProcAddress failure is a NULL result (the
       shim's self-image scan can leave a stale GetLastError on success). */
    const char *d = p ? NULL : dlerror();
    if (!p) {
        set_err("dlsym failed: ", d ? d : "symbol not found");
        return KFFI_DLSYM;
    }
#else
    const char *d = dlerror();
    if (d) {
        set_err("dlsym failed: ", d);
        return KFFI_DLSYM;
    }
#endif
    *out = p;
    return KFFI_OK;
}

/* GetSymbol: resolve, then record in `symbols`. */
long long __kml_ffi_get_symbol(KmlFfiLib *lib, char *name, long long len, void **out) {
    long long st = resolve(lib, name, (size_t)len, out);
    if (st != KFFI_OK) return st;
    kml_umap_emplace(lib->syms, name, (size_t)len, *out);
    return KFFI_OK;
}

/* PrepareFunction: an already-requested symbol must carry the same
 * signature; a new one is resolved into a fresh (not yet recorded) entry.
 * *fresh reports which, so the caller builds the function object once. */
long long __kml_ffi_prepare(KmlFfiLib *lib, char *name, long long len, const char *sig,
                            KmlFfiFn **out, long long *fresh) {
    void *existing;
    if (kml_umap_find(lib->fns, name, (size_t)len, &existing)) {
        KmlFfiFn *fn = (KmlFfiFn *)existing;
        if (strcmp(fn->sig, sig) != 0) {
            size_t n = (size_t)len + 64;
            char *buf = (char *)malloc(n);
            int w = snprintf(buf, n, "Function %s was already requested with a different signature", name);
            kffi_err = kml_str(buf, (size_t)(w < 0 ? 0 : w));
            free(buf);
            return KFFI_SIGDIFF;
        }
        *out = fn;
        *fresh = 0;
        return KFFI_OK;
    }
    void *p;
    long long st = resolve(lib, name, (size_t)len, &p);
    if (st != KFFI_OK) return st;
    KmlFfiFn *fn = (KmlFfiFn *)calloc(1, sizeof(KmlFfiFn));
    fn->cfn = p;
    fn->sig = sig;
    fn->lib = lib;
    fn->name = kml_str(name, (size_t)len);
    *out = fn;
    *fresh = 1;
    return KFFI_OK;
}

/* Record a prepared function (GetFunction/GetFunctions' caching step):
 * `symbols` first, then `functions` — both no-ops for a present name. */
void __kml_ffi_commit(KmlFfiLib *lib, KmlFfiFn *fn) {
    size_t len = (size_t)*(long long *)(fn->name - 8);
    kml_umap_emplace(lib->syms, fn->name, len, fn->cfn);
    kml_umap_emplace(lib->fns, fn->name, len, fn);
}

long long __kml_ffi_is_closed(KmlFfiLib *lib) { return lib->closed; }

/* Close: idempotent; drops both tables, as Node does. */
void __kml_ffi_close(KmlFfiLib *lib) {
    if (lib->closed) return;
    dlclose(lib->handle);
    lib->closed = 1;
    kml_umap_clear(lib->syms);
    kml_umap_clear(lib->fns);
}

/* Enumeration in container order: n entries into caller-owned arrays of
 * names (KML strings) and values; -1 when the library is closed. */
typedef struct {
    char **names;
    void **vals;
    long long i;
} ListCtx;

static void collect(void *ctx, const char *key, size_t len, void *val) {
    ListCtx *c = (ListCtx *)ctx;
    c->names[c->i] = kml_str(key, len);
    c->vals[c->i] = val;
    c->i++;
}

long long __kml_ffi_count(KmlFfiLib *lib, long long functions) {
    if (lib->closed) return -1;
    return (long long)kml_umap_size(functions ? lib->fns : lib->syms);
}

void __kml_ffi_list(KmlFfiLib *lib, long long functions, char **names, void **vals) {
    ListCtx c = {names, vals, 0};
    kml_umap_each(functions ? lib->fns : lib->syms, collect, &c);
}

/* ---- Argument validation (Node's per-type rules, verified by probe) ----
 *
 * Each validator takes one NaN-boxed argument word (runtime_nanbox.go) and
 * returns 0 with the C value in *out, or nonzero when Node would throw
 * "Argument i must be …" (the compiler supplies the message). */

#define NB_NUM_OFFSET (1LL << 49)
#define NB_NULL 2
#define NB_UNDEFINED 10
#define KML_BOXED_BIGINT_MAGIC 0x7FF40000B1616B16LL

void *__kml_bigint_from_i64(long long);
void *__kml_bigint_from_u64(long long);
long long __kml_bigint_to_i64(void *);
long long __kml_bigint_to_u64(void *);
int __kml_bigint_cmp(void *, void *);

static int nb_is_num(long long v) { return (unsigned long long)v >= (unsigned long long)NB_NUM_OFFSET; }
static double nb_num(long long v) {
    long long bits = v - NB_NUM_OFFSET;
    double d;
    memcpy(&d, &bits, 8);
    return d;
}
static void *nb_bigint(long long v) {
    if ((unsigned long long)v < 65536 || nb_is_num(v) || (v & 7) != 1) return NULL;
    long long *cell = (long long *)(v & ~7LL);
    return cell[0] == KML_BOXED_BIGINT_MAGIC ? (void *)cell[1] : NULL;
}

/* int8/uint8/int16/uint16/int32/uint32 (kind 0..5): an integral number in
 * range. -0 passes the 8/16-bit checks but not the 32-bit ones, as in Node. */
long long __kml_ffi_v_int(long long v, long long kind, long long *out) {
    static const double lo[6] = {-128, 0, -32768, 0, -2147483648.0, 0};
    static const double hi[6] = {127, 255, 32767, 65535, 2147483647.0, 4294967295.0};
    if (!nb_is_num(v)) return 1;
    double d = nb_num(v);
    if (d != d || d < lo[kind] || d > hi[kind] || d != (double)(long long)d) return 1;
    if (kind >= 4 && d == 0 && signbit(d)) return 1;
    *out = (long long)d;
    return 0;
}

static void *bound(int which) {
    static void *b[3];
    if (!b[which]) {
        switch (which) {
        case 0: b[0] = __kml_bigint_from_i64((long long)(-9223372036854775807LL - 1)); break;
        case 1: b[1] = __kml_bigint_from_i64(9223372036854775807LL); break;
        case 2: b[2] = __kml_bigint_from_u64((long long)0xFFFFFFFFFFFFFFFFULL); break;
        }
    }
    return b[which];
}

/* int64/uint64: a bigint in range (a number is rejected). */
long long __kml_ffi_v_i64(long long v, long long unsig, long long *out) {
    void *b = nb_bigint(v);
    if (!b) return 1;
    if (unsig) {
        void *zero = __kml_bigint_from_i64(0);
        if (__kml_bigint_cmp(b, zero) < 0 || __kml_bigint_cmp(b, bound(2)) > 0) return 1;
        *out = __kml_bigint_to_u64(b);
    } else {
        if (__kml_bigint_cmp(b, bound(0)) < 0 || __kml_bigint_cmp(b, bound(1)) > 0) return 1;
        *out = __kml_bigint_to_i64(b);
    }
    return 0;
}

/* float/double: any number (NaN and ±Infinity included). */
long long __kml_ffi_v_f64(long long v, double *out) {
    if (!nb_is_num(v)) return 1;
    *out = nb_num(v);
    return 0;
}

/* pointer/string/buffer/arraybuffer/function: null/undefined -> NULL, a
 * bigint in [0, 2^64) -> that address (2 when out of range), a string -> its
 * bytes, a boxed array/view/Buffer -> its data; anything else is 1. */
long long __kml_ffi_v_ptr(long long v, void **out) {
    if (v == NB_NULL || v == NB_UNDEFINED) {
        *out = NULL;
        return 0;
    }
    if (nb_is_num(v) || (unsigned long long)v < 65536) return 1;
    switch (v & 7) {
    case 0: /* string */
        *out = (void *)v;
        return 0;
    case 1: { /* object: only a boxed bigint qualifies */
        void *b = nb_bigint(v);
        if (!b) return 1;
        void *zero = __kml_bigint_from_i64(0);
        if (__kml_bigint_cmp(b, zero) < 0 || __kml_bigint_cmp(b, bound(2)) > 0) return 2;
        *out = (void *)__kml_bigint_to_u64(b);
        return 0;
    }
    case 2: { /* any-array box: field 0 is the live {data, len} header */
        void **box = (void **)(v & ~7LL);
        void **hdr = (void **)box[0];
        *out = hdr ? hdr[0] : NULL;
        return 0;
    }
    }
    return 1;
}
