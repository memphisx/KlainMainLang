// WebAssembly's natives (lib/node/kml_wasm.ts) over the engine interface
// klainwasm_wasmtime.c declares (kml_wasm_*). Compiled into the pool's unit
// only with -DKLAINPOOL_WASM (ThreadPoolCFlags). Modules are ids in one
// table every thread reads (a module crosses to a worker as its id); the
// externs (functions, memories, tables, globals) and instances an engine
// makes are ids in the making thread's tables. A value crosses as a kind and
// a number, an i64 as its decimal string. A call's arguments are pushed
// first (wasmArg) and its results read after (wasmResult*).
#ifdef KLAINPOOL_WASM

static char *kw_string(const char *src) {
    if (!src) src = "";
    int64_t n = (int64_t)strlen(src);
    char *out = __kml_str_alloc(n + 1);
    memcpy(out, src, (size_t)n);
    out[n] = 0;
    __kml_str_finalize(out);
    return out;
}

static __thread char *kw_err = NULL;
static __thread char kw_desc[3][1024];

static void kw_set_err(char *msg) {
    free(kw_err); /* scratch */
    kw_err = msg;
}

char *__kml_native_wasm_error(void) { return kw_string(kw_err); }

// The last import or export description read: 0 its module, 1 its name, 2
// its type.
char *__kml_native_wasm_desc(double which) {
    int w = (int)which;
    return kw_string(w >= 0 && w < 3 ? kw_desc[w] : "");
}

// ---- modules ----

static kml_wasm_module **kw_mods = NULL;
static int kw_mods_n = 0, kw_mods_cap = 0;
static pthread_mutex_t kw_mods_lock = PTHREAD_MUTEX_INITIALIZER;

static kml_wasm_module *kw_mod(double id) {
    pthread_mutex_lock(&kw_mods_lock);
    int i = (int)id;
    kml_wasm_module *m = (i >= 0 && i < kw_mods_n) ? kw_mods[i] : NULL;
    pthread_mutex_unlock(&kw_mods_lock);
    return m;
}

// Compile bytes: the module's id, or -1 (wasmError).
double __kml_native_wasm_compile(const uint8_t *bytes, int64_t len) {
    char *err = NULL;
    kml_wasm_module *m = kml_wasm_compile(bytes, (size_t)len, &err);
    if (!m) {
        kw_set_err(err);
        return -1;
    }
    pthread_mutex_lock(&kw_mods_lock);
    if (kw_mods_n == kw_mods_cap) {
        kw_mods_cap = kw_mods_cap ? kw_mods_cap * 2 : 8;
        kw_mods = (kml_wasm_module **)realloc(kw_mods, (size_t)kw_mods_cap * sizeof *kw_mods); /* scratch */
    }
    int id = kw_mods_n++;
    kw_mods[id] = m;
    pthread_mutex_unlock(&kw_mods_lock);
    return id;
}

_Bool __kml_native_wasm_validate(const uint8_t *bytes, int64_t len) { return kml_wasm_validate(bytes, (size_t)len) != 0; }

// The module's i-th import: its kind (0 function, 1 global, 2 table, 3
// memory, 4 tag), its strings as wasmDesc; -1 past the last.
double __kml_native_wasm_import(double mod, double i) {
    kml_wasm_module *m = kw_mod(mod);
    size_t count = 0;
    if (!m || i < 0) return -1;
    return kml_wasm_import(m, (size_t)i, kw_desc[0], kw_desc[1], sizeof kw_desc[0], kw_desc[2], sizeof kw_desc[2], &count);
}

double __kml_native_wasm_export(double mod, double i) {
    kml_wasm_module *m = kw_mod(mod);
    size_t count = 0;
    if (!m || i < 0) return -1;
    kw_desc[0][0] = 0;
    return kml_wasm_export(m, (size_t)i, kw_desc[1], sizeof kw_desc[1], kw_desc[2], sizeof kw_desc[2], &count);
}

// ---- externs and instances ----

static __thread kml_wasm_extern **kw_ext = NULL;
static __thread int kw_ext_n = 0, kw_ext_cap = 0;

// x's id: the id it already has, when it is a function, memory, table or
// global the thread has an id for (one JS object per wasm object).
static double kw_ext_id(kml_wasm_extern *x) {
    if (!x) return -1;
    for (int i = 0; i < kw_ext_n; i++) {
        if (kml_wasm_same(kw_ext[i], x)) {
            if (kw_ext[i] != x) free(x); /* scratch */
            return i;
        }
    }
    if (kw_ext_n == kw_ext_cap) {
        kw_ext_cap = kw_ext_cap ? kw_ext_cap * 2 : 16;
        kw_ext = (kml_wasm_extern **)realloc(kw_ext, (size_t)kw_ext_cap * sizeof *kw_ext); /* scratch */
    }
    kw_ext[kw_ext_n] = x;
    return kw_ext_n++;
}

static kml_wasm_extern *kw_ext_get(double id) {
    int i = (int)id;
    return (i >= 0 && i < kw_ext_n) ? kw_ext[i] : NULL;
}

double __kml_native_wasm_extern_kind(double h) {
    kml_wasm_extern *x = kw_ext_get(h);
    return x ? kml_wasm_extern_kind(x) : -1;
}

char *__kml_native_wasm_extern_type(double h) {
    kml_wasm_extern *x = kw_ext_get(h);
    char buf[1024] = "";
    if (x) kml_wasm_extern_type(x, buf, sizeof buf);
    return kw_string(buf);
}

// ---- values ----

#define KW_MAXVALS 1024
static __thread kml_wasm_val kw_args[KW_MAXVALS];
static __thread int kw_nargs = 0;
static __thread kml_wasm_val kw_res[KW_MAXVALS];
static __thread int kw_nres = 0;

static kml_wasm_val kw_val(double kind, double num, const char *dec) {
    kml_wasm_val v;
    v.kind = (int)kind;
    v.i = 0;
    v.f = 0;
    switch (v.kind) {
    case KW_I32: v.i = (int32_t)(int64_t)num; break;
    case KW_I64: v.i = (int64_t)strtoull(dec ? dec : "0", NULL, 10); break;
    case KW_F32:
    case KW_F64: v.f = num; break;
    case KW_EXTERNREF: v.i = (int64_t)num; break;
    case KW_FUNCREF: {
        kml_wasm_extern *x = num < 0 ? NULL : kw_ext_get(num);
        v.i = (int64_t)(intptr_t)x;
        break;
    }
    default: break;
    }
    return v;
}

// A value read back: its number (an i64's is 0: read its decimal), an
// externref's slot, a funcref's extern id (-1 null).
static double kw_num(kml_wasm_val *v) {
    switch (v->kind) {
    case KW_I32: return (double)(int32_t)v->i;
    case KW_F32:
    case KW_F64: return v->f;
    case KW_EXTERNREF: return (double)v->i;
    case KW_FUNCREF:
        if (v->i == 0) return -1;
        double id = kw_ext_id((kml_wasm_extern *)(intptr_t)v->i);
        v->i = (int64_t)(intptr_t)kw_ext_get(id); // the id's own extern, the copy freed
        return id;
    default: return 0;
    }
}

static char *kw_dec(const kml_wasm_val *v) {
    char buf[32];
    snprintf(buf, sizeof buf, "%lld", (long long)v->i);
    return kw_string(buf);
}

void __kml_native_wasm_arg(double kind, double num, const char *dec) {
    if (kw_nargs < KW_MAXVALS) kw_args[kw_nargs++] = kw_val(kind, num, dec);
}

double __kml_native_wasm_result_count(void) { return kw_nres; }

double __kml_native_wasm_result_kind(double i) {
    int k = (int)i;
    return (k >= 0 && k < kw_nres) ? kw_res[k].kind : -1;
}

double __kml_native_wasm_result_num(double i) {
    int k = (int)i;
    return (k >= 0 && k < kw_nres) ? kw_num(&kw_res[k]) : 0;
}

char *__kml_native_wasm_result_dec(double i) {
    int k = (int)i;
    kml_wasm_val zero = { KW_I64, 0, 0 };
    return kw_dec((k >= 0 && k < kw_nres) ? &kw_res[k] : &zero);
}

static int kw_take_args(kml_wasm_val *out) {
    int n = kw_nargs;
    memcpy(out, kw_args, (size_t)n * sizeof *out);
    kw_nargs = 0;
    return n;
}

// ---- host functions ----

typedef struct kw_frame {
    const kml_wasm_val *args;
    size_t nargs;
    kml_wasm_val *results;
    size_t nresults;
    int threw;
} kw_frame;

static __thread void *kw_host_inv = NULL, *kw_host_clo = NULL;
static __thread kw_frame *kw_top = NULL;

// The dispatcher every host function's call runs: (id, argument count).
void __kml_native_wasm_set_host(void *inv, void *clo) {
    kw_host_inv = inv;
    kw_host_clo = clo;
}

static int kml_wasm_host_call(double id, const kml_wasm_val *args, size_t nargs, kml_wasm_val *results, size_t nresults) {
    if (!kw_host_inv) return 1;
    kw_frame f = { args, nargs, results, nresults, 0 };
    kw_frame *prev = kw_top;
    kw_top = &f;
    // The JS function may call into wasm again: the call buffers are its.
    int saved_nargs = kw_nargs;
    kml_wasm_val *saved = NULL;
    if (saved_nargs) {
        saved = (kml_wasm_val *)malloc((size_t)saved_nargs * sizeof *saved); /* scratch */
        memcpy(saved, kw_args, (size_t)saved_nargs * sizeof *saved);
    }
    kw_nargs = 0;
    ((void (*)(void *, double, double))kw_host_inv)(kw_host_clo, id, (double)nargs);
    if (saved) {
        memcpy(kw_args, saved, (size_t)saved_nargs * sizeof *saved);
        free(saved); /* scratch */
    }
    kw_nargs = saved_nargs;
    kw_top = prev;
    return f.threw;
}

double __kml_native_wasm_host_arg_kind(double i) {
    int k = (int)i;
    return (kw_top && k >= 0 && (size_t)k < kw_top->nargs) ? kw_top->args[k].kind : -1;
}

double __kml_native_wasm_host_arg_num(double i) {
    int k = (int)i;
    if (!kw_top || k < 0 || (size_t)k >= kw_top->nargs) return 0;
    kml_wasm_val v = kw_top->args[k];
    return kw_num(&v);
}

char *__kml_native_wasm_host_arg_dec(double i) {
    int k = (int)i;
    kml_wasm_val zero = { KW_I64, 0, 0 };
    return kw_dec((kw_top && k >= 0 && (size_t)k < kw_top->nargs) ? &kw_top->args[k] : &zero);
}

// The host call's result count and i-th result's kind.
double __kml_native_wasm_host_result_kind(double i) {
    int k = (int)i;
    if (!kw_top) return -1;
    if (k < 0) return (double)kw_top->nresults;
    return (size_t)k < kw_top->nresults ? kw_top->results[k].kind : -1;
}

void __kml_native_wasm_host_result(double i, double num, const char *dec) {
    int k = (int)i;
    if (!kw_top || k < 0 || (size_t)k >= kw_top->nresults) return;
    kw_top->results[k] = kw_val(kw_top->results[k].kind, num, dec);
}

void __kml_native_wasm_host_throw(void) {
    if (kw_top) kw_top->threw = 1;
}

double __kml_native_wasm_host_func(const char *type, double id) { return kw_ext_id(kml_wasm_host_func(type, id)); }

// ---- externrefs ----

static __thread int64_t *kw_released = NULL;
static __thread int kw_released_n = 0, kw_released_cap = 0;

static void kml_wasm_ref_release(int64_t slot) {
    if (kw_released_n == kw_released_cap) {
        kw_released_cap = kw_released_cap ? kw_released_cap * 2 : 16;
        kw_released = (int64_t *)realloc(kw_released, (size_t)kw_released_cap * sizeof *kw_released); /* scratch */
    }
    kw_released[kw_released_n++] = slot;
}

// An externref slot the engine let go of, or 0.
double __kml_native_wasm_released(void) { return kw_released_n ? (double)kw_released[--kw_released_n] : 0; }

// ---- calls ----

// Call function h with the pushed arguments: 0 (its results as wasmResult*),
// 1 a trap, 3 a stack overflow (wasmError), 2 the JS function it called
// threw, 5 another failure (wasmError).
double __kml_native_wasm_call(double h, double nresults) {
    kml_wasm_extern *f = kw_ext_get(h);
    kml_wasm_val args[KW_MAXVALS];
    int n = kw_take_args(args);
    if (!f) return 5;
    int nr = (int)nresults;
    if (nr < 0 || nr > KW_MAXVALS) nr = 0;
    kml_wasm_val res[KW_MAXVALS];
    char *err = NULL;
    int out = kml_wasm_call(f, args, (size_t)n, res, (size_t)nr, &err);
    if (out == KWR_OK) {
        memcpy(kw_res, res, (size_t)nr * sizeof *res);
        kw_nres = nr;
    } else if (err) {
        kw_set_err(err);
    }
    return out;
}

static __thread kml_wasm_extern **kw_imports = NULL;
static __thread int kw_imports_n = 0, kw_imports_cap = 0;

void __kml_native_wasm_inst_import(double h) {
    if (kw_imports_n == kw_imports_cap) {
        kw_imports_cap = kw_imports_cap ? kw_imports_cap * 2 : 16;
        kw_imports = (kml_wasm_extern **)realloc(kw_imports, (size_t)kw_imports_cap * sizeof *kw_imports); /* scratch */
    }
    kw_imports[kw_imports_n++] = kw_ext_get(h);
}

static __thread kml_wasm_instance **kw_inst = NULL;
static __thread int kw_inst_n = 0, kw_inst_cap = 0;

// Instantiate mod with the imports wasmInstImport pushed: the instance's
// id, or -(outcome) (-4 link, -1 trap, -3 stack, -2 the start function's JS
// call threw, -5 other; wasmError).
double __kml_native_wasm_instantiate(double mod) {
    kml_wasm_module *m = kw_mod(mod);
    int n = kw_imports_n;
    kw_imports_n = 0;
    if (!m) return -5;
    for (int i = 0; i < n; i++) {
        if (!kw_imports[i]) return -5;
    }
    char *err = NULL;
    int outcome = KWR_OK;
    kml_wasm_instance *inst = kml_wasm_instantiate(m, kw_imports, (size_t)n, &err, &outcome);
    if (!inst) {
        if (err) kw_set_err(err);
        return -(double)outcome;
    }
    if (kw_inst_n == kw_inst_cap) {
        kw_inst_cap = kw_inst_cap ? kw_inst_cap * 2 : 8;
        kw_inst = (kml_wasm_instance **)realloc(kw_inst, (size_t)kw_inst_cap * sizeof *kw_inst); /* scratch */
    }
    kw_inst[kw_inst_n] = inst;
    return kw_inst_n++;
}

// The instance's i-th export's extern id (its name as wasmDesc(1)), or -1.
double __kml_native_wasm_inst_export(double inst, double i) {
    int k = (int)inst;
    if (k < 0 || k >= kw_inst_n || i < 0) return -1;
    kml_wasm_extern *x = kml_wasm_instance_export(kw_inst[k], (size_t)i, kw_desc[1], sizeof kw_desc[1]);
    return kw_ext_id(x);
}

// ---- memories ----

double __kml_native_wasm_memory_new(double initial, double maximum, _Bool shared) {
    char *err = NULL;
    kml_wasm_extern *x = kml_wasm_memory_new((uint64_t)initial, (int64_t)maximum, shared, &err);
    if (!x) {
        kw_set_err(err);
        return -1;
    }
    return kw_ext_id(x);
}

double __kml_native_wasm_memory_addr(double h) {
    kml_wasm_extern *x = kw_ext_get(h);
    return x ? (double)(uintptr_t)kml_wasm_memory_data(x) : 0;
}

double __kml_native_wasm_memory_bytes(double h) {
    kml_wasm_extern *x = kw_ext_get(h);
    return x ? (double)kml_wasm_memory_bytes(x) : 0;
}

_Bool __kml_native_wasm_memory_shared(double h) {
    kml_wasm_extern *x = kw_ext_get(h);
    return x && kml_wasm_memory_shared(x);
}

// Shared memory h, for another thread: a token (wasmMemoryAdopt there), or
// -1 for a memory that is not shared.
double __kml_native_wasm_memory_share(double h) {
    kml_wasm_extern *x = kw_ext_get(h);
    kml_wasm_extern *c = x ? kml_wasm_memory_share(x) : NULL;
    return c ? (double)(uintptr_t)c : -1;
}

// The shared memory a token names, as this thread's extern id.
double __kml_native_wasm_memory_adopt(double token) {
    if (token < 0) return -1;
    return kw_ext_id((kml_wasm_extern *)(uintptr_t)token);
}

double __kml_native_wasm_memory_grow(double h, double delta) {
    kml_wasm_extern *x = kw_ext_get(h);
    return x ? (double)kml_wasm_memory_grow(x, (uint64_t)delta) : -1;
}

// ---- globals ----

// A global of value type kind ('i', 'I', 'f', 'F', 'e', 'r' as a char code)
// holding the pushed value: its id, or -1 (wasmError).
double __kml_native_wasm_global_new(double kind, _Bool mutable_) {
    kml_wasm_val args[KW_MAXVALS];
    int n = kw_take_args(args);
    if (n < 1) return -1;
    char *err = NULL;
    kml_wasm_extern *x = kml_wasm_global_new((char)(int)kind, mutable_, &args[0], &err);
    if (!x) {
        kw_set_err(err);
        return -1;
    }
    return kw_ext_id(x);
}

// The global's value, as wasmResult 0.
void __kml_native_wasm_global_get(double h) {
    kml_wasm_extern *x = kw_ext_get(h);
    kw_nres = 0;
    if (!x) return;
    kml_wasm_global_get(x, &kw_res[0]);
    kw_nres = 1;
}

double __kml_native_wasm_global_set(double h) {
    kml_wasm_val args[KW_MAXVALS];
    int n = kw_take_args(args);
    kml_wasm_extern *x = kw_ext_get(h);
    if (!x || n < 1) return -1;
    return kml_wasm_global_set(x, &args[0]);
}

// ---- tables ----

double __kml_native_wasm_table_new(double kind, double initial, double maximum) {
    kml_wasm_val args[KW_MAXVALS];
    int n = kw_take_args(args);
    if (n < 1) return -1;
    char *err = NULL;
    kml_wasm_extern *x = kml_wasm_table_new((char)(int)kind, (uint32_t)initial, (int64_t)maximum, &args[0], &err);
    if (!x) {
        kw_set_err(err);
        return -1;
    }
    return kw_ext_id(x);
}

double __kml_native_wasm_table_size(double h) {
    kml_wasm_extern *x = kw_ext_get(h);
    return x ? (double)kml_wasm_table_size(x) : 0;
}

// Element i as wasmResult 0: 0, or -1 out of range.
double __kml_native_wasm_table_get(double h, double i) {
    kml_wasm_extern *x = kw_ext_get(h);
    kw_nres = 0;
    if (!x || i < 0) return -1;
    if (kml_wasm_table_get(x, (uint64_t)i, &kw_res[0]) != 0) return -1;
    kw_nres = 1;
    return 0;
}

double __kml_native_wasm_table_set(double h, double i) {
    kml_wasm_val args[KW_MAXVALS];
    int n = kw_take_args(args);
    kml_wasm_extern *x = kw_ext_get(h);
    if (!x || n < 1 || i < 0) return -1;
    return kml_wasm_table_set(x, (uint64_t)i, &args[0]);
}

double __kml_native_wasm_table_grow(double h, double delta) {
    kml_wasm_val args[KW_MAXVALS];
    int n = kw_take_args(args);
    kml_wasm_extern *x = kw_ext_get(h);
    if (!x || n < 1) return -1;
    return (double)kml_wasm_table_grow(x, (uint64_t)delta, &args[0]);
}
#endif
