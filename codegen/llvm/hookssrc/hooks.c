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
/* The interned "Error", an error's name (compared by address). */
void *__kml_error_name;
/* The interned "TypeError" and "RangeError", for exceptions.c. */
void *__kml_error_names[2];
/* The entry file (require.main.filename; entrypath.c). */
const char *__kml_entry_path = "";
/* A boxed promise's wrapper header word (promisebox.c). */
long long __kml_promise_box_hdr;

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
void __kml_str_free(char *s) { kml_free(KML_HEAP_RAW, KML_PREFIX(s)); }

/* ---- the Node-interpreter guard (runtime_interp_guard.go) -----------------
 * A compiled binary cannot act as the Node interpreter. Run before the
 * program body when the program reads process.execPath: a self-spawn (the
 * child env carries KML_KLAIN_REEXEC) or an interpreter flag (-p/--print,
 * -e/--eval, -i/--interactive) exits 9 with a diagnostic instead of re-running
 * the program body, which would re-spawn without end. */
void __kml_reject_node_interp_flags(int argc, char **argv) {
    static const char *const flags[] = {"-p", "--print", "-e", "--eval", "-i", "--interactive"};
    int reject = getenv("KML_KLAIN_REEXEC") != NULL;
    for (int i = 1; !reject && i < argc; i++) {
        if (!argv[i]) continue;
        for (size_t f = 0; f < sizeof flags / sizeof flags[0]; f++)
            if (strcmp(argv[i], flags[f]) == 0) reject = 1;
    }
    if (!reject) return;
    fputs("error: this compiled binary cannot act as the Node interpreter (a self-spawn via process.execPath, "
          "or an interpreter flag -p/-e/-i); refusing to re-run its own program body\n", stderr);
    exit(9);
}

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
__attribute__((weak)) u8 __kml_any_tobool(i64 v) { (void)v; MISSING(any_tobool) return 0; }
__attribute__((weak)) double __kml_any_tonum(i64 v) { (void)v; MISSING(any_tonum) return 0; }
/* A boxed bigint's digits and equalities (boxsrc/bigintbox.c, bigintrel.c):
 * a program without the bigint runtime has no bigint cell to ask about. */
__attribute__((weak)) void *__kml_boxed_bigint_str(void *cell) {
    (void)cell;
    char *s = __kml_str_alloc(0);
    s[0] = 0;
    return s;
}
__attribute__((weak)) _Bool __kml_boxed_bigint_eq(void *ca, void *cb) { (void)ca; (void)cb; return 0; }
__attribute__((weak)) _Bool __kml_boxed_bigint_eq_num(void *cell, double d) { (void)cell; (void)d; return 0; }
__attribute__((weak)) _Bool __kml_boxed_bigint_eq_str(void *cell, const char *s) { (void)cell; (void)s; return 0; }
/* The event loop's libcurl step (fetch.c): a program without fetch has no
 * transfers. */
__attribute__((weak)) int __kml_curl_inflight(void) { return 0; }
__attribute__((weak)) void __kml_curl_kick(void) {}
__attribute__((weak)) void __kml_curl_drain_messages(void) {}
/* No task parks on a fetch group without fetch.c. */
__attribute__((weak)) _Bool __kml_group_satisfied(void *group) { (void)group; return 1; }
/* Without fetch.c: no transfer to pump, and libcurl to initialize before a
 * worker thread starts. */
__attribute__((weak)) _Bool __kml_fetch_pump(void) { return 0; }
/* No promise boxed as `any` (promisebox.c otherwise). */
__attribute__((weak)) i64 __kml_promise_inspect_parts(void *o, i64 *out) { (void)o; (void)out; return -1; }
__attribute__((weak)) void *__kml_promise_wrapper_of(i64 v) { (void)v; return 0; }
/* No property can be added to a static object (objextra.c otherwise). */
__attribute__((weak)) int __kml_obj_has_extra(void *o) { (void)o; return 0; }
__attribute__((weak)) char *__kml_obj_inspect_dyn(void *o, long long d) { (void)o; (void)d; return 0; }
__attribute__((weak)) char *__kml_obj_json_dyn(void *o, const char *ind, long long base) { (void)o; (void)ind; (void)base; return 0; }
__attribute__((weak)) void __kml_obj_keys_dyn(void *o, void **out) { (void)o; out[0] = 0; out[1] = 0; }
__attribute__((weak)) void __kml_worker_curl_preinit(void) {}
/* Without worker.c: the task runtime's swap sites have no abort to check. */
__attribute__((weak)) void __kml_worker_abort_check(void) {}
/* Without the thread pool: the crypto backend's pooled natives are never
 * called. */
__attribute__((weak)) void __kml_pool_job(void *work, void *job, void *inv, void *clo) {
    (void)work;
    (void)job;
    (void)inv;
    (void)clo;
}
__attribute__((weak)) int __kml_any_bigint_rel(i64 a, i64 b) { (void)a; (void)b; return 0; }

/* Object.prototype's own methods, installed into its bag when it is made;
 * a program that never reads them leaves it empty. */
VHOOK(objproto_fill, (void *b), (b), { (void)b; })
/* A number's or boolean's class prototype (its bag, boxed), 0 when the
 * program reads none of its methods. */
HOOK(i64, prim_proto_of, (i64 v), (v), { (void)v; return 0; })
/* Inspection of an Error's own keys and a Promise's state (emit_record_view.go). */
HOOK(i64, proxy_tget, (i64 t, const char *k), (t, k), { (void)t; (void)k; return 10; })
HOOK(i64, proxy_tset, (i64 t, const char *k, i64 v), (t, k, v), { (void)t; (void)k; (void)v; return 0; })
HOOK(char *, error_inspect, (void *o), (o), { (void)o; return (char *)"[Error]"; })
/* fs errors (runtime_fs.go). */
__attribute__((weak)) void *__kml_fs_error_new(int e, const char *syscall, const char *path, const char *dest) {
    (void)e; (void)syscall; (void)path; (void)dest;
    MISSING(fs_error_new) return 0;
}
/* Promise<any> (emit_anyprom.go). */
VHOOK(pdyn_attach, (void *src, i64 onF, i64 onR, i64 onFin, void *q, void *boxer, i64 kind),
     (src, onF, onR, onFin, q, boxer, kind),
     { (void)src; (void)onF; (void)onR; (void)onFin; (void)q; (void)boxer; (void)kind; MISSING(pdyn_attach) })
/* process 'uncaughtException' / 'unhandledRejection' (runtime_process.go):
 * no listener, not handled. */
HOOK(u8, process_uncaught, (u8 tag, i64 pay, _Bool rejection), (tag, pay, rejection),
     { (void)tag; (void)pay; (void)rejection; return 0; })
VHOOK(unhandled_report, (void *p), (p), { (void)p; })
/* A TransformStream's decode step (runtime_streams.go). */
VHOOK(stream_decode, (void *decode, void *rec, i64 *out), (decode, rec, out),
     { (void)decode; (void)rec; (void)out; MISSING(stream_decode) })
/* Throws (emit_nullderef.go). */
/* exceptions.c's, when the program links it. */
__attribute__((weak)) void __kml_throw_nullderef(const char *msg) { fprintf(stderr, "TypeError: %s\n", msg); exit(1); }
__attribute__((weak)) void __kml_throw_range_error(const char *msg) { fprintf(stderr, "RangeError: %s\n", msg); exit(1); }
/* Workers (emit_worker.go, emitter.go). */
HOOK(i64, worker_run_loop, (void), (), { MISSING(worker_run_loop) return 0; })
VHOOK(worker_uncaught, (u8 tag, i64 pay), (tag, pay), { (void)tag; (void)pay; })

/* C routines another runtime file may not be linked with: the definer
 * registers it from its constructor. */
static void dflt_task_gc_restore(void) {}
void (*__kml_hook_task_gc_restore)(void) = dflt_task_gc_restore;
static void dflt_fetch_bodyprom_on_done(void *pend) { (void)pend; }
void (*__kml_hook_fetch_bodyprom_on_done)(void *) = dflt_fetch_bodyprom_on_done;
