/* ffi_native.c — node:ffi's natives for lib/node/ffi.ts: the library
 * registry (ffi_registry.c) behind ids, calls and callbacks through libffi
 * (Node's node_ffi.cc does the same), and the raw-memory helpers.
 *
 * A signature crosses as its kinds: a return kind and argument kinds, each a
 * number (see lib/node/ffi.ts FfiKind). Argument values cross as NaN-boxed
 * words and are validated with ffi_registry.c's validators, as Node's
 * per-type rules; results cross back boxed (a 64-bit integer or a pointer as
 * a bigint). */

#if defined(__APPLE__)
#include <ffi/ffi.h> /* the SDK's libffi */
#else
#include <ffi.h>
#endif
#include <pthread.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct KmlFfiLib KmlFfiLib;
typedef struct KmlFfiFn KmlFfiFn;
long long __kml_ffi_open(char *path, KmlFfiLib **out);
char *__kml_ffi_errmsg(void);
long long __kml_ffi_get_symbol(KmlFfiLib *lib, char *name, long long len, void **out);
long long __kml_ffi_prepare(KmlFfiLib *lib, char *name, long long len, const char *sig, KmlFfiFn **out, long long *fresh);
void __kml_ffi_commit(KmlFfiLib *lib, KmlFfiFn *fn);
long long __kml_ffi_is_closed(KmlFfiLib *lib);
void __kml_ffi_close(KmlFfiLib *lib);
long long __kml_ffi_count(KmlFfiLib *lib, long long functions);
void __kml_ffi_list(KmlFfiLib *lib, long long functions, char **names, void **vals);
long long __kml_ffi_v_int(long long v, long long kind, long long *out);
long long __kml_ffi_v_i64(long long v, long long unsig, long long *out);
long long __kml_ffi_v_f64(long long v, double *out);
long long __kml_ffi_v_ptr(long long v, void **out);
void *__kml_bigint_from_i64(long long);
void *__kml_bigint_from_u64(long long);
extern char *__kml_str_alloc(long long n);
extern void __kml_str_finalize(char *s);

enum {
    K_VOID = 0, K_I8, K_U8, K_I16, K_U16, K_I32, K_U32, K_I64, K_U64, K_F32, K_F64,
    K_POINTER, K_STRING, K_BUFFER, K_ARRAYBUFFER, K_FUNCTION, K_CHAR,
};

#define NB_NUM_OFFSET (1LL << 49)
#define NB_UNDEFINED 10
#define KML_BOXED_BIGINT_MAGIC 0x7FF40000B1616B16LL

static long long box_num(double d) {
    long long bits;
    memcpy(&bits, &d, 8);
    return bits + NB_NUM_OFFSET;
}

static long long box_bigint(void *b) {
    long long *cell = (long long *)malloc(16);
    cell[0] = KML_BOXED_BIGINT_MAGIC;
    cell[1] = (long long)(intptr_t)b;
    return (long long)(intptr_t)cell | 1;
}

static char *kml_string(const char *s, size_t n) {
    char *out = __kml_str_alloc((long long)n + 1);
    memcpy(out, s, n);
    out[n] = 0;
    __kml_str_finalize(out);
    return out;
}

static ffi_type *kind_type(int k) {
    switch (k) {
    case K_VOID: return &ffi_type_void;
    case K_I8: case K_CHAR: return &ffi_type_sint8;
    case K_U8: return &ffi_type_uint8;
    case K_I16: return &ffi_type_sint16;
    case K_U16: return &ffi_type_uint16;
    case K_I32: return &ffi_type_sint32;
    case K_U32: return &ffi_type_uint32;
    case K_I64: return &ffi_type_sint64;
    case K_U64: return &ffi_type_uint64;
    case K_F32: return &ffi_type_float;
    case K_F64: return &ffi_type_double;
    }
    return &ffi_type_pointer;
}

/* ---- libraries ---- */

static __thread KmlFfiLib **libs;
static __thread int nlibs, caplibs;
static __thread double last_addr;
static __thread int last_fresh;
static __thread KmlFfiFn *last_fn;
static __thread char **list_names;
static __thread void **list_vals;
static __thread long long list_n;

static KmlFfiLib *lib_get(double id) {
    int i = (int)id;
    return (i >= 0 && i < nlibs) ? libs[i] : NULL;
}

/* A library's id, or -1 (sqliteError's message: ffiError). A null path is
 * the process image. */
double __kml_native_ffi_open(char *path, _Bool isNull) {
    KmlFfiLib *lib = NULL;
    if (__kml_ffi_open(isNull ? NULL : path, &lib) != 0) return -1;
    if (nlibs == caplibs) {
        int nc = caplibs ? caplibs * 2 : 8;
        KmlFfiLib **nt = (KmlFfiLib **)calloc((size_t)nc, sizeof *nt);
        if (libs) memcpy(nt, libs, (size_t)caplibs * sizeof *nt);
        libs = nt;
        caplibs = nc;
    }
    libs[nlibs] = lib;
    return nlibs++;
}

char *__kml_native_ffi_error(void) {
    char *m = __kml_ffi_errmsg();
    return kml_string(m, strlen(m));
}

_Bool __kml_native_ffi_closed(double id) {
    KmlFfiLib *lib = lib_get(id);
    return !lib || __kml_ffi_is_closed(lib);
}

void __kml_native_ffi_close(double id) {
    KmlFfiLib *lib = lib_get(id);
    if (lib) __kml_ffi_close(lib);
}

/* A symbol's address (ffiLastAddr): 0, or the registry's status (1 closed,
 * 2 dlsym failed). */
double __kml_native_ffi_symbol(double id, char *name) {
    KmlFfiLib *lib = lib_get(id);
    if (!lib) return 1;
    void *p = NULL;
    long long st = __kml_ffi_get_symbol(lib, name, (long long)strlen(name), &p);
    if (st == 0) last_addr = (double)(uintptr_t)p;
    return (double)st;
}

double __kml_native_ffi_last_addr(void) { return last_addr; }

/* PrepareFunction: 0 (the symbol's address in ffiLastAddr, whether it is
 * new in ffiLastFresh), or the status (1 closed, 2 dlsym failed, 3 another
 * signature). commit records the prepared one. */
double __kml_native_ffi_prepare(double id, char *name, char *sig) {
    KmlFfiLib *lib = lib_get(id);
    if (!lib) return 1;
    long long fresh = 0;
    KmlFfiFn *fn = NULL;
    long long st = __kml_ffi_prepare(lib, name, (long long)strlen(name), strdup(sig), &fn, &fresh);
    if (st != 0) return (double)st;
    last_fn = fn;
    last_fresh = (int)fresh;
    last_addr = (double)(uintptr_t)(*(void **)fn);
    return 0;
}

_Bool __kml_native_ffi_last_fresh(void) { return last_fresh != 0; }

void __kml_native_ffi_commit(double id) {
    KmlFfiLib *lib = lib_get(id);
    if (lib && last_fn) __kml_ffi_commit(lib, last_fn);
}

/* The `symbols` (functions false) or `functions` table in its order: the
 * count (-1 when closed); ffiListName/ffiListAddr read entry i. */
double __kml_native_ffi_count(double id, _Bool functions) {
    KmlFfiLib *lib = lib_get(id);
    if (!lib) return -1;
    long long n = __kml_ffi_count(lib, functions ? 1 : 0);
    if (n < 0) return -1;
    free(list_names);
    free(list_vals);
    list_names = (char **)calloc((size_t)(n ? n : 1), sizeof *list_names);
    list_vals = (void **)calloc((size_t)(n ? n : 1), sizeof *list_vals);
    list_n = n;
    __kml_ffi_list(lib, functions ? 1 : 0, list_names, list_vals);
    return (double)n;
}

char *__kml_native_ffi_list_name(double i) {
    long long k = (long long)i;
    if (k < 0 || k >= list_n) return kml_string("", 0);
    char *n = list_names[k];
    return kml_string(n, (size_t)*(long long *)(n - 8));
}

/* A `symbols` entry's address, or a `functions` entry's symbol address. */
double __kml_native_ffi_list_addr(double i, _Bool functions) {
    long long k = (long long)i;
    if (k < 0 || k >= list_n) return 0;
    void *v = list_vals[k];
    if (functions) v = v ? *(void **)v : NULL;
    return (double)(uintptr_t)v;
}

/* ---- calls ---- */

typedef struct {
    ffi_cif cif;
    ffi_type **args;
    int nargs;
    int *kinds;
    int ret;
} KCif;

static pthread_mutex_t cif_mu = PTHREAD_MUTEX_INITIALIZER;
static KCif **cifs;
static int ncifs, capcifs;

/* A call interface for the kinds "ret:a,b,…": its id, or -1. */
double __kml_native_ffi_cif(char *spec) {
    KCif *c = (KCif *)calloc(1, sizeof *c);
    char *p = spec;
    c->ret = (int)strtol(p, &p, 10);
    int cap = 8;
    c->kinds = (int *)calloc((size_t)cap, sizeof *c->kinds);
    if (*p == ':') p++;
    while (*p) {
        if (c->nargs == cap) {
            cap *= 2;
            c->kinds = (int *)realloc(c->kinds, (size_t)cap * sizeof *c->kinds);
        }
        c->kinds[c->nargs++] = (int)strtol(p, &p, 10);
        if (*p == ',') p++;
        else break;
    }
    c->args = (ffi_type **)calloc((size_t)(c->nargs ? c->nargs : 1), sizeof *c->args);
    for (int i = 0; i < c->nargs; i++) c->args[i] = kind_type(c->kinds[i]);
    if (ffi_prep_cif(&c->cif, FFI_DEFAULT_ABI, (unsigned)c->nargs, kind_type(c->ret), c->args) != FFI_OK) {
        free(c->args);
        free(c->kinds);
        free(c);
        return -1;
    }
    pthread_mutex_lock(&cif_mu);
    if (ncifs == capcifs) {
        int nc = capcifs ? capcifs * 2 : 16;
        KCif **nt = (KCif **)calloc((size_t)nc, sizeof *nt);
        if (cifs) memcpy(nt, cifs, (size_t)capcifs * sizeof *nt);
        cifs = nt;
        capcifs = nc;
    }
    int id = ncifs++;
    cifs[id] = c;
    pthread_mutex_unlock(&cif_mu);
    return id;
}

static KCif *cif_get(double id) {
    int i = (int)id;
    KCif *c = NULL;
    pthread_mutex_lock(&cif_mu);
    if (i >= 0 && i < ncifs) c = cifs[i];
    pthread_mutex_unlock(&cif_mu);
    return c;
}

typedef union {
    int8_t i8; uint8_t u8; int16_t i16; uint16_t u16; int32_t i32; uint32_t u32;
    int64_t i64; uint64_t u64; float f32; double f64; void *p;
} KVal;

/* Convert a boxed word to kind k: 0, 1 when it is not a value of the kind,
 * 2 a pointer bigint out of range. */
static int convert(long long v, int k, KVal *out) {
    long long iv;
    double dv;
    void *pv;
    long long st;
    switch (k) {
    case K_I8: case K_CHAR: st = __kml_ffi_v_int(v, 0, &iv); out->i8 = (int8_t)iv; return (int)st;
    case K_U8: st = __kml_ffi_v_int(v, 1, &iv); out->u8 = (uint8_t)iv; return (int)st;
    case K_I16: st = __kml_ffi_v_int(v, 2, &iv); out->i16 = (int16_t)iv; return (int)st;
    case K_U16: st = __kml_ffi_v_int(v, 3, &iv); out->u16 = (uint16_t)iv; return (int)st;
    case K_I32: st = __kml_ffi_v_int(v, 4, &iv); out->i32 = (int32_t)iv; return (int)st;
    case K_U32: st = __kml_ffi_v_int(v, 5, &iv); out->u32 = (uint32_t)iv; return (int)st;
    case K_I64: st = __kml_ffi_v_i64(v, 0, &iv); out->i64 = iv; return (int)st;
    case K_U64: st = __kml_ffi_v_i64(v, 1, &iv); out->u64 = (uint64_t)iv; return (int)st;
    case K_F32: st = __kml_ffi_v_f64(v, &dv); out->f32 = (float)dv; return (int)st;
    case K_F64: st = __kml_ffi_v_f64(v, &dv); out->f64 = dv; return (int)st;
    }
    st = __kml_ffi_v_ptr(v, &pv);
    out->p = pv;
    return (int)st;
}

static long long box_value(const void *p, int k) {
    switch (k) {
    case K_VOID: return NB_UNDEFINED;
    case K_I8: case K_CHAR: return box_num(*(const int8_t *)p);
    case K_U8: return box_num(*(const uint8_t *)p);
    case K_I16: return box_num(*(const int16_t *)p);
    case K_U16: return box_num(*(const uint16_t *)p);
    case K_I32: return box_num(*(const int32_t *)p);
    case K_U32: return box_num(*(const uint32_t *)p);
    case K_I64: return box_bigint(__kml_bigint_from_i64(*(const int64_t *)p));
    case K_U64: return box_bigint(__kml_bigint_from_u64((long long)*(const uint64_t *)p));
    case K_F32: return box_num(*(const float *)p);
    case K_F64: return box_num(*(const double *)p);
    }
    return box_bigint(__kml_bigint_from_u64((long long)(uintptr_t)*(void *const *)p));
}

#define KFFI_MAX_ARGS 64
static __thread KVal call_args[KFFI_MAX_ARGS];

/* Argument i of the next call, of kind k: 0, 1 not a value of the kind, 2 a
 * pointer bigint out of range. */
double __kml_native_ffi_arg(double i, double k, long long v) {
    int n = (int)i;
    if (n < 0 || n >= KFFI_MAX_ARGS) return 1;
    return convert(v, (int)k, &call_args[n]);
}

/* Call fn through the interface with the arguments set: the boxed result. */
long long __kml_native_ffi_call(double cifId, double fn) {
    KCif *c = cif_get(cifId);
    if (!c) return NB_UNDEFINED;
    void *argp[KFFI_MAX_ARGS];
    for (int i = 0; i < c->nargs && i < KFFI_MAX_ARGS; i++) argp[i] = &call_args[i];
    union { ffi_arg a; ffi_sarg s; KVal v; uint64_t pad[2]; } rv;
    memset(&rv, 0, sizeof rv);
    ffi_call(&c->cif, FFI_FN((void *)(uintptr_t)fn), &rv, argp);
    KVal out;
    memset(&out, 0, sizeof out);
    /* libffi widens a small integral result to ffi_arg. */
    switch (c->ret) {
    case K_I8: case K_CHAR: out.i8 = (int8_t)rv.s; break;
    case K_U8: out.u8 = (uint8_t)rv.a; break;
    case K_I16: out.i16 = (int16_t)rv.s; break;
    case K_U16: out.u16 = (uint16_t)rv.a; break;
    case K_I32: out.i32 = (int32_t)rv.s; break;
    case K_U32: out.u32 = (uint32_t)rv.a; break;
    default: out = rv.v;
    }
    return box_value(&out, c->ret);
}

/* ---- callbacks (libffi closures) ---- */

typedef struct {
    KCif *cif;
    void *inv, *clo;
    ffi_closure *closure;
    void *code;
} KClosure;

typedef struct {
    KCif *cif;
    void **args;
    void *ret;
} KCall;

static __thread KCall *cb_cur;
static pthread_mutex_t clo_mu = PTHREAD_MUTEX_INITIALIZER;
static KClosure **clos;
static int nclos, capclos;

static void closure_handler(ffi_cif *cif, void *ret, void **args, void *user) {
    (void)cif;
    KClosure *k = (KClosure *)user;
    KCall call = {k->cif, args, ret};
    KCall *prev = cb_cur;
    cb_cur = &call;
    if (k->cif->ret != K_VOID) memset(ret, 0, sizeof(ffi_arg) > 8 ? sizeof(ffi_arg) : 8);
    ((void (*)(void *, double, double))k->inv)(k->clo, 0, 0);
    cb_cur = prev;
}

/* A C function pointer that calls onCall (reading ffiCallbackArg, answering
 * with ffiCallbackReturn): its address, or 0. */
double __kml_native_ffi_closure(double cifId, void *inv, void *clo) {
    KCif *c = cif_get(cifId);
    if (!c) return 0;
    KClosure *k = (KClosure *)calloc(1, sizeof *k);
    k->cif = c;
    k->inv = inv;
    k->clo = clo;
    k->closure = (ffi_closure *)ffi_closure_alloc(sizeof(ffi_closure), &k->code);
    if (!k->closure || ffi_prep_closure_loc(k->closure, &c->cif, closure_handler, k, k->code) != FFI_OK) {
        if (k->closure) ffi_closure_free(k->closure);
        free(k);
        return 0;
    }
    pthread_mutex_lock(&clo_mu);
    if (nclos == capclos) {
        int nc = capclos ? capclos * 2 : 16;
        KClosure **nt = (KClosure **)calloc((size_t)nc, sizeof *nt);
        if (clos) memcpy(nt, clos, (size_t)capclos * sizeof *nt);
        clos = nt;
        capclos = nc;
    }
    clos[nclos++] = k;
    pthread_mutex_unlock(&clo_mu);
    return (double)(uintptr_t)k->code;
}

/* Free the closure whose code address is code (unregisterCallback). */
void __kml_native_ffi_closure_free(double code) {
    KClosure *k = NULL;
    pthread_mutex_lock(&clo_mu);
    for (int i = 0; i < nclos; i++) {
        if ((double)(uintptr_t)clos[i]->code == code) {
            k = clos[i];
            clos[i] = clos[--nclos];
            break;
        }
    }
    pthread_mutex_unlock(&clo_mu);
    if (k) {
        ffi_closure_free(k->closure);
        free(k);
    }
}

double __kml_native_ffi_cb_argc(void) { return cb_cur ? cb_cur->cif->nargs : 0; }

long long __kml_native_ffi_cb_arg(double i) {
    int n = (int)i;
    if (!cb_cur || n < 0 || n >= cb_cur->cif->nargs) return NB_UNDEFINED;
    return box_value(cb_cur->args[n], cb_cur->cif->kinds[n]);
}

/* The callback's result: 0, or 1/2 as ffiArg's. */
double __kml_native_ffi_cb_return(long long v) {
    if (!cb_cur) return 1;
    int k = cb_cur->cif->ret;
    if (k == K_VOID) return 0;
    KVal out;
    int st = convert(v, k, &out);
    if (st) return st;
    switch (k) {
    case K_I8: case K_CHAR: *(ffi_sarg *)cb_cur->ret = out.i8; break;
    case K_U8: *(ffi_arg *)cb_cur->ret = out.u8; break;
    case K_I16: *(ffi_sarg *)cb_cur->ret = out.i16; break;
    case K_U16: *(ffi_arg *)cb_cur->ret = out.u16; break;
    case K_I32: *(ffi_sarg *)cb_cur->ret = out.i32; break;
    case K_U32: *(ffi_arg *)cb_cur->ret = out.u32; break;
    case K_I64: *(int64_t *)cb_cur->ret = out.i64; break;
    case K_U64: *(uint64_t *)cb_cur->ret = out.u64; break;
    case K_F32: *(float *)cb_cur->ret = out.f32; break;
    case K_F64: *(double *)cb_cur->ret = out.f64; break;
    default: *(void **)cb_cur->ret = out.p;
    }
    return 0;
}

/* ---- memory ---- */

/* A pointer argument: its address, -1 not a pointer value, -2 out of range. */
double __kml_native_ffi_ptr(long long v) {
    void *p = NULL;
    long long st = __kml_ffi_v_ptr(v, &p);
    if (st == 1) return -1;
    if (st == 2) return -2;
    return (double)(uintptr_t)p;
}

/* A value of kind k at addr + off, boxed. */
long long __kml_native_ffi_get(double k, double addr, double off) {
    const char *p = (const char *)(uintptr_t)addr + (long long)off;
    return box_value(p, (int)k);
}

/* Store v as kind k at addr + off: 0, or 1 when v is not a value of it. */
double __kml_native_ffi_set(double k, double addr, double off, long long v) {
    KVal out;
    int kind = (int)k;
    int st = convert(v, kind, &out);
    if (st) return st;
    char *p = (char *)(uintptr_t)addr + (long long)off;
    switch (kind) {
    case K_I8: case K_U8: case K_CHAR: memcpy(p, &out, 1); break;
    case K_I16: case K_U16: memcpy(p, &out, 2); break;
    case K_I32: case K_U32: case K_F32: memcpy(p, &out, 4); break;
    default: memcpy(p, &out, 8);
    }
    return 0;
}

/* The NUL-terminated string at addr. */
char *__kml_native_ffi_cstring(double addr) {
    const char *s = (const char *)(uintptr_t)addr;
    return kml_string(s, strlen(s));
}

/* Copy len bytes from addr into out; copy src's bytes to addr. */
void __kml_native_ffi_copy_in(double addr, uint8_t *out, int64_t len) {
    if (len > 0) memcpy(out, (const void *)(uintptr_t)addr, (size_t)len);
}

void __kml_native_ffi_copy_out(const uint8_t *src, int64_t len, double addr) {
    if (len > 0) memcpy((void *)(uintptr_t)addr, src, (size_t)len);
}
