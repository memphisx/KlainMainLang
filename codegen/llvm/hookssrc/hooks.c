/* hooks.c — what the runtime's C reads from or calls into the program
 * (TDD-00251 Stage 4). Every program links it, so a runtime object never
 * names a symbol only some programs generate:
 *
 *   - The data the C reads (the connection fibers' state, the current task,
 *     the curl multi handle, ...) is defined here; the program's IR declares
 *     it external.
 *   - A routine the program generates and the C calls is a hook: the C
 *     calls the symbol defined here, which calls through a pointer the
 *     program's constructor sets to its own (internal) definition. Before
 *     that, or in a program without it, the default runs: what a program
 *     without the feature observes, or an internal error where the C cannot
 *     be reached without it.
 *   - A per-program value the C reads (an interned string, a type id) is
 *     stored here by that constructor.
 *   - The NaN-box and string-header primitives, which the IR keeps as its
 *     own internal copies for inlining, have their C definitions here. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef long long i64;
typedef unsigned long long u64;
typedef unsigned char u8;

/* ---- data the runtime owns ------------------------------------------------ */
#ifndef KML_MAIN_CTX_SIZE
#error "KML_MAIN_CTX_SIZE: the ucontext size (ucontextLayout)"
#endif
_Thread_local char __kml_main_ctx[KML_MAIN_CTX_SIZE] __attribute__((aligned(16)));
_Thread_local void *__kml_curl_multi;
_Thread_local void *__kml_current_task;
_Thread_local void *__kml_root_async_ctx;
void *__kml_cluster_close_flag;
int __kml_cluster_primary_pid;
_Bool __kml_curl_inited;
/* Per-program values, stored by the program's constructor. */
void *__kml_fetch_strs[10];

/* ---- NaN-box and string-header primitives (runtime_nanbox.go,
 * runtime_strheader.go: the same arithmetic) -------------------------------- */
#define NB_DOUBLE_OFFSET 562949953421312ULL /* 1 << 49 */

i64 __kml_nb_pack(u8 tag, i64 pay) {
    switch (tag) {
    case 0: {
        double d = (double)pay;
        u64 b;
        memcpy(&b, &d, 8);
        return (i64)(b + NB_DOUBLE_OFFSET);
    }
    case 1: {
        double d;
        memcpy(&d, &pay, 8);
        u64 b = d != d ? 9221120237041090560ULL : (u64)pay;
        return (i64)(b + NB_DOUBLE_OFFSET);
    }
    case 3: return pay != 0 ? 7 : 6;
    case 2: return pay;
    case 6: return pay | 1;
    case 7: return pay | 2;
    case 8: return pay | 3;
    case 9: return pay | 4;
    case 10: return pay | 5;
    case 11: return pay | 6;
    case 12: return pay | 7;
    }
    return tag == 4 ? 2 : 10;
}

u8 __kml_nb_tag(i64 v) {
    u64 u = (u64)v;
    if (u >= NB_DOUBLE_OFFSET) return 1;
    if (u < 65536) {
        if (u == 2) return 4;
        if (u == 6 || u == 7) return 3;
        return 5;
    }
    switch (u & 7) {
    case 1: return 6;
    case 2: return 7;
    case 3: return 8;
    case 4: return 9;
    case 5: return 10;
    case 6: return 11;
    case 7: return 12;
    }
    return 2;
}

i64 __kml_nb_pay(i64 v) {
    u64 u = (u64)v;
    if (u >= NB_DOUBLE_OFFSET) return (i64)(u - NB_DOUBLE_OFFSET);
    if (u < 65536) return u == 7;
    return (u & 7) == 0 ? v : (i64)(u & ~(u64)7);
}

char *__kml_str_alloc(i64 n) {
    char *base = (char *)kml_alloc(KML_HEAP_RAW, (size_t)n + 9);
    *(i64 *)base = n;
    return base + 8;
}
i64 __kml_str_len(char *s) { return KML_STR_LEN(s); }
void __kml_str_free(char *s) { kml_free(KML_HEAP_RAW, s - 8); }

/* ---- hooks ---------------------------------------------------------------- */
static void kml_hook_missing(const char *name) {
    fprintf(stderr, "internal error: the runtime called %s, which this program does not define\n", name);
    abort();
}

#define HOOK(ret, name, params, args, dflt)                                  \
    static ret dflt_##name params dflt                                     \
    void *__kml_hookp_##name = (void *)dflt_##name;                        \
    ret __kml_##name params { return ((ret(*) params)__kml_hookp_##name)args; }
#define VHOOK(name, params, args, dflt)                                      \
    static void dflt_##name params dflt                                    \
    void *__kml_hookp_##name = (void *)dflt_##name;                        \
    void __kml_##name params { ((void(*) params)__kml_hookp_##name)args; }
#define MISSING(name) { kml_hook_missing("__kml_" #name); }

/* Dynamic values (emit_anyops.go, runtime_dynamic.go). */
HOOK(u8, any_eq, (i64 a, i64 b), (a, b), { (void)a; (void)b; MISSING(any_eq) return 0; })
HOOK(u8, any_tobool, (i64 v), (v), { (void)v; MISSING(any_tobool) return 0; })
HOOK(double, any_tonum, (i64 v), (v), { (void)v; MISSING(any_tonum) return 0; })
/* A class value's statics, for inspection (emit_classref_rows.go). */
HOOK(const char *, classref_shown, (void *p), (p), { (void)p; return 0; })
/* Object.prototype's own methods, installed into its bag when it is made;
 * a program that never reads them leaves it empty. */
VHOOK(objproto_fill, (void *b), (b), { (void)b; })
/* A number's or boolean's class prototype (its bag, boxed), 0 when the
 * program reads none of its methods. */
HOOK(i64, prim_proto_of, (i64 v), (v), { (void)v; return 0; })
HOOK(i64, classref_nstatic, (void *p), (p), { (void)p; return 0; })
HOOK(const char *, classref_skey, (void *p, i64 i), (p, i), { (void)p; (void)i; return 0; })
HOOK(i64, classref_sget, (void *p, i64 i), (p, i), { (void)p; (void)i; return 0; })
/* Inspection of an Error's own keys and a Promise's state (emit_record_view.go). */
HOOK(char *, error_inspect, (void *o), (o), { (void)o; return 0; })
HOOK(i64, promise_inspect_parts, (void *o, i64 *out), (o, out), { (void)o; (void)out; return 0; })
/* fs errors (runtime_fs.go). */
HOOK(void *, fs_error_new, (int e, const char *syscall, const char *path, const char *dest), (e, syscall, path, dest),
     { (void)e; (void)syscall; (void)path; (void)dest; MISSING(fs_error_new) return 0; })
/* The event loop's per-pass callback into the HTTP client
 * (emit_http_client.go): none without it. */
VHOOK(httpc_fire_ready, (void), (), { })
/* Promise<any> (emit_anyprom.go). */
VHOOK(pdyn_attach, (void *src, i64 onF, i64 onR, i64 onFin, void *q, void *boxer, i64 kind),
     (src, onF, onR, onFin, q, boxer, kind),
     { (void)src; (void)onF; (void)onR; (void)onFin; (void)q; (void)boxer; (void)kind; MISSING(pdyn_attach) })
HOOK(void *, promise_wrapper_of, (i64 v), (v), { (void)v; MISSING(promise_wrapper_of) return 0; })
/* process 'uncaughtException' / 'unhandledRejection' (runtime_process.go):
 * no listener, not handled. */
HOOK(u8, process_uncaught, (u8 tag, i64 pay, _Bool rejection), (tag, pay, rejection),
     { (void)tag; (void)pay; (void)rejection; return 0; })
VHOOK(unhandled_report, (void *p), (p), { (void)p; MISSING(unhandled_report) })
/* AbortSignal (emit_signal.go): a program without one aborts nothing. */
HOOK(u8, signal_aborted, (void *sig), (sig), { (void)sig; return 0; })
HOOK(i64, signal_reason, (void *sig), (sig), { (void)sig; return 0; })
HOOK(u8, signal_timeout, (void *sig), (sig), { (void)sig; return 0; })
/* A TransformStream's decode step (runtime_streams.go). */
VHOOK(stream_decode, (void *decode, void *rec, i64 *out), (decode, rec, out),
     { (void)decode; (void)rec; (void)out; MISSING(stream_decode) })
/* Throws (emit_nullderef.go). */
VHOOK(throw_nullderef, (const char *msg), (msg), { fprintf(stderr, "TypeError: %s\n", msg); exit(1); })
VHOOK(throw_range_error, (const char *msg), (msg), { fprintf(stderr, "RangeError: %s\n", msg); exit(1); })
/* Workers (emit_worker.go, emitter.go). */
HOOK(i64, worker_run_loop, (void), (), { MISSING(worker_run_loop) return 0; })
VHOOK(worker_uncaught, (u8 tag, i64 pay), (tag, pay), { (void)tag; (void)pay; })

/* C routines another runtime file may not be linked with: the definer
 * registers it from its constructor. */
static void dflt_task_gc_restore(void) {}
void (*__kml_hook_task_gc_restore)(void) = dflt_task_gc_restore;
static void dflt_fetch_bodyprom_on_done(void *pend) { (void)pend; }
void (*__kml_hook_fetch_bodyprom_on_done)(void *) = dflt_fetch_bodyprom_on_done;
