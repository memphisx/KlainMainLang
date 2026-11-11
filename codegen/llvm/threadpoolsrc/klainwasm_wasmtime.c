// WebAssembly's engine interface (kml_wasm_*), the part of the WebAssembly
// natives (klainwasm.c) a backend implements, and its wasmtime backend.
// Compiled into the pool's unit only with -DKLAINPOOL_WASM
// (ThreadPoolCFlags). A module is shareable by every thread, as a V8
// WasmModuleObject's native module is; everything an instance makes
// (functions, memories, tables, globals) belongs to the thread that made it,
// in that thread's store, as a Node object belongs to its isolate.
#ifdef KLAINPOOL_WASM
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <pthread.h>

// ---- the interface ----

// A value crossing the interface. An i64 is i; an f32 or f64 is f; an
// externref is i, the natives' slot for the JS value (0 null); a funcref is
// i, a kml_wasm_extern pointer (0 null).
typedef struct { int kind; int64_t i; double f; } kml_wasm_val;
enum { KW_I32, KW_I64, KW_F32, KW_F64, KW_EXTERNREF, KW_FUNCREF, KW_V128 };
enum { KWX_FUNC, KWX_GLOBAL, KWX_TABLE, KWX_MEMORY, KWX_TAG };
// kml_wasm_call / kml_wasm_instantiate's outcome.
enum { KWR_OK, KWR_TRAP, KWR_THREW, KWR_STACK, KWR_LINK, KWR_ERROR };

typedef struct kml_wasm_module kml_wasm_module;
typedef struct kml_wasm_extern kml_wasm_extern;
typedef struct kml_wasm_instance kml_wasm_instance;

// The natives' half: a host function's call (id, as kml_wasm_host_func
// took it). 0, or 1 when the JS function threw.
static int kml_wasm_host_call(double id, const kml_wasm_val *args, size_t nargs, kml_wasm_val *results, size_t nresults);
// An externref's JS slot is dropped (the engine let go of the reference).
static void kml_wasm_ref_release(int64_t slot);

// ---- wasmtime ----
#include <wasmtime.h>

struct kml_wasm_module { wasmtime_module_t *m; };
struct kml_wasm_extern { wasmtime_extern_t x; };
struct kml_wasm_instance { wasmtime_instance_t inst; };

static wasm_engine_t *kw_engine = NULL;
static pthread_once_t kw_engine_once = PTHREAD_ONCE_INIT;
static __thread wasmtime_store_t *kw_store = NULL;
// Set when a host function's JS function threw: the trap it raised is that
// exception, not the engine's.
static __thread int kw_host_threw = 0;

static void kw_engine_init(void) {
    wasm_config_t *cfg = wasm_config_new();
    // The proposals V8 ships enabled (Node 24): reference types, bulk
    // memory, multi-value, SIMD, tail calls, sign extension, saturating
    // conversions are wasmtime's defaults too; threads (shared memories,
    // atomics) are V8's as well.
    wasmtime_config_wasm_threads_set(cfg, true);
    wasmtime_config_shared_memory_set(cfg, true);
    kw_engine = wasm_engine_new_with_config(cfg);
}

static wasmtime_context_t *kw_cx(void) {
    pthread_once(&kw_engine_once, kw_engine_init);
    if (!kw_store) kw_store = wasmtime_store_new(kw_engine, NULL, NULL);
    return wasmtime_store_context(kw_store);
}

static char *kw_message(wasmtime_error_t *err, wasm_trap_t *trap) {
    wasm_name_t msg;
    if (err) {
        wasmtime_error_message(err, &msg);
        wasmtime_error_delete(err);
    } else {
        wasm_trap_message(trap, &msg);
    }
    char *out = (char *)malloc(msg.size + 1); /* scratch */
    memcpy(out, msg.data, msg.size);
    out[msg.size] = 0;
    // wasm_trap_message's text is NUL-terminated and counts the NUL.
    if (msg.size > 0 && out[msg.size - 1] == 0) out[msg.size - 1] = 0;
    wasm_byte_vec_delete(&msg);
    return out;
}

static char kw_valchar(wasm_valkind_t k) {
    switch (k) {
    case WASM_I32: return 'i';
    case WASM_I64: return 'I';
    case WASM_F32: return 'f';
    case WASM_F64: return 'F';
    case WASM_EXTERNREF: return 'e';
    case WASM_FUNCREF: return 'r';
    default: return 'v';
    }
}

static wasm_valkind_t kw_charkind(char c) {
    switch (c) {
    case 'i': return WASM_I32;
    case 'I': return WASM_I64;
    case 'f': return WASM_F32;
    case 'F': return WASM_F64;
    case 'e': return WASM_EXTERNREF;
    case 'r': return WASM_FUNCREF;
    default: return WASM_I32; // v128 never reaches a host function JS made
    }
}

static int kw_kind(wasm_valkind_t k) {
    switch (k) {
    case WASM_I32: return KW_I32;
    case WASM_I64: return KW_I64;
    case WASM_F32: return KW_F32;
    case WASM_F64: return KW_F64;
    case WASM_EXTERNREF: return KW_EXTERNREF;
    case WASM_FUNCREF: return KW_FUNCREF;
    default: return KW_V128;
    }
}

// A function type as "<params>><results>" (i i32, I i64, f f32, F f64,
// e externref, r funcref, v v128).
static void kw_functype_str(const wasm_functype_t *ft, char *out, size_t cap) {
    size_t n = 0;
    const wasm_valtype_vec_t *ps = wasm_functype_params(ft);
    const wasm_valtype_vec_t *rs = wasm_functype_results(ft);
    for (size_t i = 0; i < ps->size && n + 2 < cap; i++) out[n++] = kw_valchar(wasm_valtype_kind(ps->data[i]));
    if (n + 1 < cap) out[n++] = '>';
    for (size_t i = 0; i < rs->size && n + 1 < cap; i++) out[n++] = kw_valchar(wasm_valtype_kind(rs->data[i]));
    out[n] = 0;
}

static void kw_limits_str(char *out, size_t cap, char lead, uint64_t min, int hasmax, uint64_t max) {
    if (hasmax) snprintf(out, cap, "%c%llu,%llu", lead, (unsigned long long)min, (unsigned long long)max);
    else snprintf(out, cap, "%c%llu,-1", lead, (unsigned long long)min);
}

// An extern type: a function's type; a global "m<k>" or "c<k>"; a memory
// "m<min>,<max>" (max -1 none, s prefix shared); a table "<k><min>,<max>".
static int kw_externtype_str(const wasm_externtype_t *et, char *out, size_t cap) {
    switch (wasm_externtype_kind(et)) {
    case WASM_EXTERN_FUNC:
        kw_functype_str(wasm_externtype_as_functype_const(et), out, cap);
        return KWX_FUNC;
    case WASM_EXTERN_GLOBAL: {
        const wasm_globaltype_t *g = wasm_externtype_as_globaltype_const(et);
        snprintf(out, cap, "%c%c", wasm_globaltype_mutability(g) == WASM_VAR ? 'm' : 'c',
                 kw_valchar(wasm_valtype_kind(wasm_globaltype_content(g))));
        return KWX_GLOBAL;
    }
    case WASM_EXTERN_TABLE: {
        const wasm_tabletype_t *t = wasm_externtype_as_tabletype_const(et);
        const wasm_limits_t *l = wasm_tabletype_limits(t);
        kw_limits_str(out, cap, kw_valchar(wasm_valtype_kind(wasm_tabletype_element(t))), l->min,
                      l->max != wasm_limits_max_default, l->max);
        return KWX_TABLE;
    }
    case WASM_EXTERN_MEMORY: {
        const wasm_memorytype_t *m = wasm_externtype_as_memorytype_const(et);
        uint64_t max = 0;
        int hasmax = wasmtime_memorytype_maximum(m, &max);
        kw_limits_str(out, cap, wasmtime_memorytype_isshared(m) ? 's' : 'm', wasmtime_memorytype_minimum(m), hasmax, max);
        return KWX_MEMORY;
    }
    default:
        out[0] = 0;
        return KWX_TAG;
    }
}

static kml_wasm_module *kml_wasm_compile(const uint8_t *bytes, size_t n, char **err) {
    kw_cx();
    wasmtime_module_t *m = NULL;
    wasmtime_error_t *e = wasmtime_module_new(kw_engine, bytes, n, &m);
    if (e) {
        *err = kw_message(e, NULL);
        return NULL;
    }
    kml_wasm_module *out = (kml_wasm_module *)calloc(1, sizeof *out); /* scratch */
    out->m = m;
    return out;
}

static int kml_wasm_validate(const uint8_t *bytes, size_t n) {
    kw_cx();
    wasmtime_error_t *e = wasmtime_module_validate(kw_engine, bytes, n);
    if (e) {
        wasmtime_error_delete(e);
        return 0;
    }
    return 1;
}

// The module's i-th import (its module and name, NUL-terminated copies in
// mod/name of cap bytes) and its type: the extern kind.
static int kml_wasm_import(kml_wasm_module *m, size_t i, char *mod, char *name, size_t cap, char *type, size_t tcap, size_t *count) {
    wasm_importtype_vec_t v;
    wasmtime_module_imports(m->m, &v);
    *count = v.size;
    int kind = -1;
    if (i < v.size) {
        const wasm_name_t *a = wasm_importtype_module(v.data[i]);
        const wasm_name_t *b = wasm_importtype_name(v.data[i]);
        size_t an = a->size < cap - 1 ? a->size : cap - 1, bn = b->size < cap - 1 ? b->size : cap - 1;
        memcpy(mod, a->data, an);
        mod[an] = 0;
        memcpy(name, b->data, bn);
        name[bn] = 0;
        kind = kw_externtype_str(wasm_importtype_type(v.data[i]), type, tcap);
    }
    wasm_importtype_vec_delete(&v);
    return kind;
}

static int kml_wasm_export(kml_wasm_module *m, size_t i, char *name, size_t cap, char *type, size_t tcap, size_t *count) {
    wasm_exporttype_vec_t v;
    wasmtime_module_exports(m->m, &v);
    *count = v.size;
    int kind = -1;
    if (i < v.size) {
        const wasm_name_t *b = wasm_exporttype_name(v.data[i]);
        size_t bn = b->size < cap - 1 ? b->size : cap - 1;
        memcpy(name, b->data, bn);
        name[bn] = 0;
        kind = kw_externtype_str(wasm_exporttype_type(v.data[i]), type, tcap);
    }
    wasm_exporttype_vec_delete(&v);
    return kind;
}

static kml_wasm_extern *kw_wrap(const wasmtime_extern_t *x) {
    kml_wasm_extern *out = (kml_wasm_extern *)calloc(1, sizeof *out); /* scratch */
    out->x = *x;
    return out;
}

static void kw_ref_finalize(void *data) { kml_wasm_ref_release((int64_t)(intptr_t)data); }

// A kml_wasm_val as wasmtime's: an externref is a new rooted reference
// the caller unroots (wasmtime_val_unroot).
static void kw_to_val(wasmtime_context_t *cx, const kml_wasm_val *v, wasmtime_val_t *out) {
    switch (v->kind) {
    case KW_I32: out->kind = WASMTIME_I32; out->of.i32 = (int32_t)v->i; break;
    case KW_I64: out->kind = WASMTIME_I64; out->of.i64 = v->i; break;
    case KW_F32: out->kind = WASMTIME_F32; out->of.f32 = (float)v->f; break;
    case KW_F64: out->kind = WASMTIME_F64; out->of.f64 = v->f; break;
    case KW_EXTERNREF:
        out->kind = WASMTIME_EXTERNREF;
        if (v->i == 0 || !wasmtime_externref_new(cx, (void *)(intptr_t)v->i, kw_ref_finalize, &out->of.externref)) {
            memset(&out->of.externref, 0, sizeof out->of.externref); // null
        }
        break;
    case KW_FUNCREF:
        out->kind = WASMTIME_FUNCREF;
        if (v->i == 0) memset(&out->of.funcref, 0, sizeof out->of.funcref);
        else out->of.funcref = ((kml_wasm_extern *)(intptr_t)v->i)->x.of.func;
        break;
    default:
        out->kind = WASMTIME_I32;
        out->of.i32 = 0;
    }
}

// A wasmtime value as a kml_wasm_val. An externref's slot is the natives'
// (the reference's data); a funcref is a new kml_wasm_extern.
static void kw_from_val(wasmtime_context_t *cx, const wasmtime_val_t *v, kml_wasm_val *out) {
    out->i = 0;
    out->f = 0;
    switch (v->kind) {
    case WASMTIME_I32: out->kind = KW_I32; out->i = v->of.i32; break;
    case WASMTIME_I64: out->kind = KW_I64; out->i = v->of.i64; break;
    case WASMTIME_F32: out->kind = KW_F32; out->f = v->of.f32; break;
    case WASMTIME_F64: out->kind = KW_F64; out->f = v->of.f64; break;
    case WASMTIME_EXTERNREF:
        out->kind = KW_EXTERNREF;
        if (v->of.externref.store_id != 0) out->i = (int64_t)(intptr_t)wasmtime_externref_data(cx, &v->of.externref);
        break;
    case WASMTIME_FUNCREF:
        out->kind = KW_FUNCREF;
        if (v->of.funcref.store_id != 0) {
            wasmtime_extern_t x;
            x.kind = WASMTIME_EXTERN_FUNC;
            x.of.func = v->of.funcref;
            out->i = (int64_t)(intptr_t)kw_wrap(&x);
        }
        break;
    default:
        out->kind = KW_V128;
    }
}

static wasm_trap_t *kw_host_trampoline(void *env, wasmtime_caller_t *caller, const wasmtime_val_t *args, size_t nargs,
                                       wasmtime_val_t *results, size_t nresults) {
    wasmtime_context_t *cx = wasmtime_caller_context(caller);
    kml_wasm_val *a = (kml_wasm_val *)calloc(nargs + 1, sizeof *a); /* scratch */
    kml_wasm_val *r = (kml_wasm_val *)calloc(nresults + 1, sizeof *r); /* scratch */
    for (size_t i = 0; i < nargs; i++) kw_from_val(cx, &args[i], &a[i]);
    for (size_t i = 0; i < nresults; i++) r[i].kind = kw_kind(results[i].kind);
    int threw = kml_wasm_host_call(*(double *)env, a, nargs, r, nresults);
    wasm_trap_t *trap = NULL;
    if (threw) {
        kw_host_threw = 1;
        trap = wasmtime_trap_new("kml: host function threw", strlen("kml: host function threw"));
    } else {
        for (size_t i = 0; i < nresults; i++) kw_to_val(cx, &r[i], &results[i]);
    }
    free(a); /* scratch */
    free(r); /* scratch */
    return trap;
}

// A host function of type ("ii>i"): each call runs kml_wasm_host_call(id).
static kml_wasm_extern *kml_wasm_host_func(const char *type, double id) {
    wasmtime_context_t *cx = kw_cx();
    const char *gt = strchr(type, '>');
    size_t np = gt ? (size_t)(gt - type) : strlen(type);
    size_t nr = gt ? strlen(gt + 1) : 0;
    wasm_valtype_vec_t ps, rs;
    wasm_valtype_vec_new_uninitialized(&ps, np);
    wasm_valtype_vec_new_uninitialized(&rs, nr);
    for (size_t i = 0; i < np; i++) ps.data[i] = wasm_valtype_new(kw_charkind(type[i]));
    for (size_t i = 0; i < nr; i++) rs.data[i] = wasm_valtype_new(kw_charkind(gt[1 + i]));
    wasm_functype_t *ft = wasm_functype_new(&ps, &rs);
    double *env = (double *)malloc(sizeof(double)); /* scratch */
    *env = id;
    wasmtime_extern_t x;
    x.kind = WASMTIME_EXTERN_FUNC;
    wasmtime_func_new(cx, ft, kw_host_trampoline, env, free, &x.of.func);
    wasm_functype_delete(ft);
    return kw_wrap(&x);
}

static int kw_shared(const kml_wasm_extern *x) { return x->x.kind == WASMTIME_EXTERN_SHAREDMEMORY; }

static int kml_wasm_extern_kind(const kml_wasm_extern *x) {
    switch (x->x.kind) {
    case WASMTIME_EXTERN_FUNC: return KWX_FUNC;
    case WASMTIME_EXTERN_GLOBAL: return KWX_GLOBAL;
    case WASMTIME_EXTERN_TABLE: return KWX_TABLE;
    case WASMTIME_EXTERN_MEMORY:
    case WASMTIME_EXTERN_SHAREDMEMORY: return KWX_MEMORY;
    default: return KWX_TAG;
    }
}

// Whether two externs are the same function, memory, table or global.
static int kml_wasm_same(const kml_wasm_extern *a, const kml_wasm_extern *b) {
    if (a->x.kind != b->x.kind) return 0;
    switch (a->x.kind) {
    case WASMTIME_EXTERN_FUNC: return memcmp(&a->x.of.func, &b->x.of.func, sizeof a->x.of.func) == 0;
    case WASMTIME_EXTERN_GLOBAL: return memcmp(&a->x.of.global, &b->x.of.global, sizeof a->x.of.global) == 0;
    case WASMTIME_EXTERN_TABLE: return memcmp(&a->x.of.table, &b->x.of.table, sizeof a->x.of.table) == 0;
    case WASMTIME_EXTERN_MEMORY: return memcmp(&a->x.of.memory, &b->x.of.memory, sizeof a->x.of.memory) == 0;
    case WASMTIME_EXTERN_SHAREDMEMORY:
        // Each handle on a shared memory is its own: the same memory has
        // the same bytes.
        return wasmtime_sharedmemory_data(a->x.of.sharedmemory) == wasmtime_sharedmemory_data(b->x.of.sharedmemory);
    default: return 0;
    }
}

static void kml_wasm_extern_type(const kml_wasm_extern *x, char *out, size_t cap) {
    wasmtime_context_t *cx = kw_cx();
    if (kw_shared(x)) {
        wasm_memorytype_t *mt = wasmtime_sharedmemory_type(x->x.of.sharedmemory);
        uint64_t max = 0;
        int hasmax = wasmtime_memorytype_maximum(mt, &max);
        kw_limits_str(out, cap, 's', wasmtime_memorytype_minimum(mt), hasmax, max);
        wasm_memorytype_delete(mt);
        return;
    }
    wasm_externtype_t *et = wasmtime_extern_type(cx, (wasmtime_extern_t *)&x->x);
    kw_externtype_str(et, out, cap);
    wasm_externtype_delete(et);
}

static int kw_trap_outcome(wasm_trap_t *trap, char **err) {
    wasmtime_trap_code_t code;
    int has = wasmtime_trap_code(trap, &code);
    char *msg = kw_message(NULL, trap);
    wasm_trap_delete(trap);
    // V8's RuntimeError messages (src/common/message-template.h kTrap*).
    const char *v8 = NULL;
    if (has) {
        switch (code) {
        case WASMTIME_TRAP_CODE_STACK_OVERFLOW: free(msg); *err = strdup("Maximum call stack size exceeded"); return KWR_STACK; /* scratch */
        case WASMTIME_TRAP_CODE_MEMORY_OUT_OF_BOUNDS: v8 = "memory access out of bounds"; break;
        case WASMTIME_TRAP_CODE_HEAP_MISALIGNED: v8 = "operation does not support unaligned accesses"; break;
        case WASMTIME_TRAP_CODE_TABLE_OUT_OF_BOUNDS: v8 = "table index is out of bounds"; break;
        case WASMTIME_TRAP_CODE_INDIRECT_CALL_TO_NULL: v8 = "null function or function signature mismatch"; break;
        case WASMTIME_TRAP_CODE_BAD_SIGNATURE: v8 = "null function or function signature mismatch"; break;
        case WASMTIME_TRAP_CODE_INTEGER_OVERFLOW: v8 = "divide result unrepresentable"; break;
        case WASMTIME_TRAP_CODE_INTEGER_DIVISION_BY_ZERO: v8 = "divide by zero"; break;
        case WASMTIME_TRAP_CODE_BAD_CONVERSION_TO_INTEGER: v8 = "float unrepresentable in integer range"; break;
        case WASMTIME_TRAP_CODE_UNREACHABLE_CODE_REACHED: v8 = "unreachable"; break;
        default: break;
        }
    }
    if (v8) {
        free(msg); /* scratch */
        *err = strdup(v8);
    } else {
        *err = msg;
    }
    return KWR_TRAP;
}

// Call function f: KWR_OK with nresults results, KWR_TRAP / KWR_STACK
// (err) or KWR_THREW (the JS function it called threw).
static int kml_wasm_call(const kml_wasm_extern *f, const kml_wasm_val *args, size_t nargs, kml_wasm_val *results, size_t nresults, char **err) {
    wasmtime_context_t *cx = kw_cx();
    wasmtime_val_t *a = (wasmtime_val_t *)calloc(nargs + 1, sizeof *a); /* scratch */
    wasmtime_val_t *r = (wasmtime_val_t *)calloc(nresults + 1, sizeof *r); /* scratch */
    for (size_t i = 0; i < nargs; i++) kw_to_val(cx, &args[i], &a[i]);
    wasm_trap_t *trap = NULL;
    int outer = kw_host_threw;
    kw_host_threw = 0;
    wasmtime_error_t *e = wasmtime_func_call(cx, &f->x.of.func, a, nargs, r, nresults, &trap);
    int threw = kw_host_threw;
    kw_host_threw = outer;
    int out = KWR_OK;
    if (threw && (e || trap)) {
        if (e) wasmtime_error_delete(e);
        if (trap) wasm_trap_delete(trap);
        out = KWR_THREW;
    } else if (e) {
        *err = kw_message(e, NULL);
        out = KWR_ERROR;
    } else if (trap) {
        out = kw_trap_outcome(trap, err);
    } else {
        for (size_t i = 0; i < nresults; i++) {
            kw_from_val(cx, &r[i], &results[i]);
            wasmtime_val_unroot(&r[i]);
        }
    }
    for (size_t i = 0; i < nargs; i++) wasmtime_val_unroot(&a[i]);
    free(a); /* scratch */
    free(r); /* scratch */
    return out;
}

// Instantiate m with its imports in order: the instance, or NULL with
// *outcome KWR_LINK (an import does not match), KWR_TRAP / KWR_STACK (the
// start function trapped) or KWR_THREW, and err.
static kml_wasm_instance *kml_wasm_instantiate(kml_wasm_module *m, kml_wasm_extern **imports, size_t n, char **err, int *outcome) {
    wasmtime_context_t *cx = kw_cx();
    wasmtime_extern_t *xs = (wasmtime_extern_t *)calloc(n + 1, sizeof *xs); /* scratch */
    for (size_t i = 0; i < n; i++) xs[i] = imports[i]->x;
    kml_wasm_instance *inst = (kml_wasm_instance *)calloc(1, sizeof *inst); /* scratch */
    wasm_trap_t *trap = NULL;
    int outer = kw_host_threw;
    kw_host_threw = 0;
    wasmtime_error_t *e = wasmtime_instance_new(cx, m->m, xs, n, &inst->inst, &trap);
    int threw = kw_host_threw;
    kw_host_threw = outer;
    free(xs); /* scratch */
    if (threw && (e || trap)) {
        if (e) wasmtime_error_delete(e);
        if (trap) wasm_trap_delete(trap);
        *outcome = KWR_THREW;
        free(inst); /* scratch */
        return NULL;
    }
    if (e) {
        *err = kw_message(e, NULL);
        *outcome = KWR_LINK;
        free(inst); /* scratch */
        return NULL;
    }
    if (trap) {
        *outcome = kw_trap_outcome(trap, err);
        free(inst); /* scratch */
        return NULL;
    }
    *outcome = KWR_OK;
    return inst;
}

// The instance's i-th export (its name copied into name), or NULL.
static kml_wasm_extern *kml_wasm_instance_export(kml_wasm_instance *inst, size_t i, char *name, size_t cap) {
    wasmtime_context_t *cx = kw_cx();
    char *nm = NULL;
    size_t nl = 0;
    wasmtime_extern_t x;
    if (!wasmtime_instance_export_nth(cx, &inst->inst, i, &nm, &nl, &x)) return NULL;
    size_t k = nl < cap - 1 ? nl : cap - 1;
    memcpy(name, nm, k);
    name[k] = 0;
    return kw_wrap(&x);
}

// ---- memories ----

static kml_wasm_extern *kml_wasm_memory_new(uint64_t min, int64_t max, int shared, char **err) {
    wasmtime_context_t *cx = kw_cx();
    wasm_memorytype_t *ty = NULL;
    wasmtime_error_t *e = wasmtime_memorytype_new(min, max >= 0, max >= 0 ? (uint64_t)max : 0, false, shared != 0, 16, &ty);
    if (e) {
        *err = kw_message(e, NULL);
        return NULL;
    }
    wasmtime_extern_t x;
    if (shared) {
        x.kind = WASMTIME_EXTERN_SHAREDMEMORY;
        e = wasmtime_sharedmemory_new(kw_engine, ty, &x.of.sharedmemory);
    } else {
        x.kind = WASMTIME_EXTERN_MEMORY;
        e = wasmtime_memory_new(cx, ty, &x.of.memory);
    }
    wasm_memorytype_delete(ty);
    if (e) {
        *err = kw_message(e, NULL);
        return NULL;
    }
    return kw_wrap(&x);
}

static uint8_t *kml_wasm_memory_data(const kml_wasm_extern *x) {
    if (kw_shared(x)) return wasmtime_sharedmemory_data(x->x.of.sharedmemory);
    return wasmtime_memory_data(kw_cx(), &x->x.of.memory);
}

static size_t kml_wasm_memory_bytes(const kml_wasm_extern *x) {
    if (kw_shared(x)) return wasmtime_sharedmemory_data_size(x->x.of.sharedmemory);
    return wasmtime_memory_data_size(kw_cx(), &x->x.of.memory);
}

// Whether the memory is shared (its buffer a SharedArrayBuffer).
static int kml_wasm_memory_shared(const kml_wasm_extern *x) { return kw_shared(x); }

// Another handle on shared memory x, for another thread (kml_wasm_extern
// of the thread that adopts it), or NULL for a memory that is not shared.
static kml_wasm_extern *kml_wasm_memory_share(const kml_wasm_extern *x) {
    if (!kw_shared(x)) return NULL;
    wasmtime_extern_t c;
    c.kind = WASMTIME_EXTERN_SHAREDMEMORY;
    c.of.sharedmemory = wasmtime_sharedmemory_clone(x->x.of.sharedmemory);
    return kw_wrap(&c);
}

// Grow by delta pages: the old size in pages, or -1.
static int64_t kml_wasm_memory_grow(const kml_wasm_extern *x, uint64_t delta) {
    uint64_t prev = 0;
    wasmtime_error_t *e = kw_shared(x) ? wasmtime_sharedmemory_grow(x->x.of.sharedmemory, delta, &prev)
                                       : wasmtime_memory_grow(kw_cx(), &x->x.of.memory, delta, &prev);
    if (e) {
        wasmtime_error_delete(e);
        return -1;
    }
    return (int64_t)prev;
}

// ---- globals ----

static kml_wasm_extern *kml_wasm_global_new(char kind, int mutable_, const kml_wasm_val *v, char **err) {
    wasmtime_context_t *cx = kw_cx();
    wasm_globaltype_t *ty = wasm_globaltype_new(wasm_valtype_new(kw_charkind(kind)), mutable_ ? WASM_VAR : WASM_CONST);
    wasmtime_val_t val;
    kw_to_val(cx, v, &val);
    wasmtime_extern_t x;
    x.kind = WASMTIME_EXTERN_GLOBAL;
    wasmtime_error_t *e = wasmtime_global_new(cx, ty, &val, &x.of.global);
    wasmtime_val_unroot(&val);
    wasm_globaltype_delete(ty);
    if (e) {
        *err = kw_message(e, NULL);
        return NULL;
    }
    return kw_wrap(&x);
}

static void kml_wasm_global_get(const kml_wasm_extern *x, kml_wasm_val *out) {
    wasmtime_context_t *cx = kw_cx();
    wasmtime_val_t v;
    wasmtime_global_get(cx, &x->x.of.global, &v);
    kw_from_val(cx, &v, out);
    wasmtime_val_unroot(&v);
}

static int kml_wasm_global_set(const kml_wasm_extern *x, const kml_wasm_val *v) {
    wasmtime_context_t *cx = kw_cx();
    wasmtime_val_t val;
    kw_to_val(cx, v, &val);
    wasmtime_error_t *e = wasmtime_global_set(cx, &x->x.of.global, &val);
    wasmtime_val_unroot(&val);
    if (e) {
        wasmtime_error_delete(e);
        return -1;
    }
    return 0;
}

// ---- tables ----

static kml_wasm_extern *kml_wasm_table_new(char kind, uint32_t min, int64_t max, const kml_wasm_val *init, char **err) {
    wasmtime_context_t *cx = kw_cx();
    wasm_limits_t lim = { min, max >= 0 ? (uint32_t)max : wasm_limits_max_default };
    wasm_tabletype_t *ty = wasm_tabletype_new(wasm_valtype_new(kw_charkind(kind)), &lim);
    wasmtime_val_t val;
    kw_to_val(cx, init, &val);
    wasmtime_extern_t x;
    x.kind = WASMTIME_EXTERN_TABLE;
    wasmtime_error_t *e = wasmtime_table_new(cx, ty, &val, &x.of.table);
    wasmtime_val_unroot(&val);
    wasm_tabletype_delete(ty);
    if (e) {
        *err = kw_message(e, NULL);
        return NULL;
    }
    return kw_wrap(&x);
}

static uint64_t kml_wasm_table_size(const kml_wasm_extern *x) { return wasmtime_table_size(kw_cx(), &x->x.of.table); }

static int kml_wasm_table_get(const kml_wasm_extern *x, uint64_t i, kml_wasm_val *out) {
    wasmtime_context_t *cx = kw_cx();
    wasmtime_val_t v;
    if (!wasmtime_table_get(cx, &x->x.of.table, i, &v)) return -1;
    kw_from_val(cx, &v, out);
    wasmtime_val_unroot(&v);
    return 0;
}

static int kml_wasm_table_set(const kml_wasm_extern *x, uint64_t i, const kml_wasm_val *v) {
    wasmtime_context_t *cx = kw_cx();
    wasmtime_val_t val;
    kw_to_val(cx, v, &val);
    wasmtime_error_t *e = wasmtime_table_set(cx, &x->x.of.table, i, &val);
    wasmtime_val_unroot(&val);
    if (e) {
        wasmtime_error_delete(e);
        return -1;
    }
    return 0;
}

static int64_t kml_wasm_table_grow(const kml_wasm_extern *x, uint64_t delta, const kml_wasm_val *init) {
    wasmtime_context_t *cx = kw_cx();
    wasmtime_val_t val;
    kw_to_val(cx, init, &val);
    uint64_t prev = 0;
    wasmtime_error_t *e = wasmtime_table_grow(cx, &x->x.of.table, delta, &val, &prev);
    wasmtime_val_unroot(&val);
    if (e) {
        wasmtime_error_delete(e);
        return -1;
    }
    return (int64_t)prev;
}
#endif
