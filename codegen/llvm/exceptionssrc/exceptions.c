/* exceptions.c — the thrown value and the throw path (TDD-00202; in C per
 * TDD-00240). The thrown value is an unpacked (tag, payload) pair: the
 * logical kmlTag* value plus its payload (a NaN-box payload for primitives; a
 * ptrtoint'd Error object for tag 13). __kml_thrown stays set to the Error
 * object when the tag is 13, so the internal Error-only catch paths
 * (async/fs) and the uncaught printer read it directly; it is null for a
 * non-Error throw. The setjmp buffers a throw unwinds through are
 * jmpstack.c's. kml_layout.h is prepended by the compiler. */
#include <stdio.h>
#include <stdlib.h>

typedef long long i64;
typedef unsigned long long u64;
typedef unsigned char u8;

#define F(p, off, T) (*(T *)((char *)(p) + (off)))

/* Declared as the emitted IR declares them; no system header, so the
 * prototypes cannot clash with libc's. */
#pragma clang diagnostic ignored "-Wincompatible-library-redeclaration"
extern void longjmp(void *, int) __attribute__((noreturn));

extern void __kml_dtoa(char *buf, double v);
extern void *__kml_jmp_unwind_slot(void);
extern _Thread_local int __kml_jmp_top;

/* The process-lifecycle hooks and the worker thread's uncaught hook are
 * emitted with the program. */
extern u8 __kml_process_uncaught(u8 tag, i64 pay, _Bool rejection);
extern void __kml_run_exit_handlers(i64 code);
extern void __kml_run_before_exit(void (*run)(void), _Bool (*alive)(void));
extern _Thread_local i64 __kml_process_exit_code;
/* The event loop (loop.c); a program without one has nothing to run. */
__attribute__((weak)) void __kml_event_loop_run(void) {}
__attribute__((weak)) _Bool __kml_event_loop_alive(void) { return 0; }

/* hooks.c's: on a worker thread an uncaught error goes to the parent's
 * 'error' listener and ends only that thread; without Workers, a no-op. */
extern void __kml_worker_uncaught(u8 tag, i64 pay);
/* process.exit's end: the thread on a worker, the process elsewhere. */
__attribute__((weak, noreturn)) void __kml_thread_exit(int code) { exit(code); }
#define KML_EXIT(c) __kml_thread_exit(c)

_Thread_local void *__kml_thrown;
static _Thread_local u8 thrown_tag = KML_TAG_ERROR;
static _Thread_local i64 thrown_pay;

#ifdef _WIN32
/* A stack overflow spends the thread's guard page (stackguard.c), which the
 * C runtime re-arms only once the stack has unwound. A throw from within the
 * last SG_DEEP bytes of the stack marks it, and the catch that reads the
 * value re-arms the page, so the next overflow throws too. */
__declspec(dllimport) void __stdcall GetCurrentThreadStackLimits(unsigned long long *low, unsigned long long *high);
int _resetstkoflw(void);
#define SG_DEEP (128 * 1024)
static _Thread_local u8 rearm_stack;
static void note_deep_throw(void) {
    unsigned long long low = 0, high = 0;
    GetCurrentThreadStackLimits(&low, &high);
    if ((unsigned long long)(size_t)__builtin_frame_address(0) < low + SG_DEEP) rearm_stack = 1;
}
static void rearm(void) {
    if (rearm_stack) {
        rearm_stack = 0;
        _resetstkoflw();
    }
}
#else
static void note_deep_throw(void) {}
static void rearm(void) {}
#endif

void *__kml_get_thrown(void) { rearm(); return __kml_thrown; }
u8 __kml_get_thrown_tag(void) { rearm(); return thrown_tag; }
i64 __kml_get_thrown_pay(void) { rearm(); return thrown_pay; }

/* The tag a thrown or rejected value keeps. An object box whose field 0
 * carries the boxed-object Error type-id (TDD-00222: flag bit set, low bits
 * below the subclass tag base) IS a built-in Error: it becomes tag 13 so a
 * catch handler sees the full Error shape (.name/.message/instanceof),
 * exactly as if thrown unboxed. Subclass instances (different struct layout)
 * stay boxed. */
u8 __kml_caught_tag(int tag, i64 pay) {
    tag &= 0xff;
    if (tag == KML_TAG_OBJECT) {
        u64 f0 = *(u64 *)pay;
        if ((f0 & KML_ERROR_TYPE_FLAG) != 0 && (f0 & (KML_ERROR_TYPE_FLAG - 1)) < KML_ERROR_SUBCLASS_BASE)
            return KML_TAG_ERROR;
    }
    return (u8)tag;
}

/* util.inspect of a boxed value (dynjson.c); NULL when that file is not
 * linked. */
__attribute__((weak)) char *__kml_any_inspect_at(long long v, long long depth) {
    (void)v; (void)depth;
    return NULL;
}
extern i64 __kml_nb_pack(u8 tag, i64 pay);

/* The message an uncaught throw prints: an Error yields its .message; a
 * string is itself; a number is dtoa'd; booleans, null and undefined their
 * literals; any other value a generic placeholder. NUL-terminated. */
void *__kml_caught_unc_msg(int tag, i64 pay) {
    switch (tag & 0xff) {
    case KML_TAG_ERROR: return F((void *)pay, KML_ERROR_MSG, void *);
    case 2: return (void *)pay;
    case 1: {
        char *buf = (char *)malloc(32); /* scratch */
        double d;
        __builtin_memcpy(&d, &pay, sizeof d);
        __kml_dtoa(buf, d);
        return buf;
    }
    case 3: return pay != 0 ? "true" : "false";
    case 4: return "null";
    case 5: return "undefined";
    default: {
        /* Any other value as util.inspect shows it, when dynjson.c (which
         * a throw of an object links) is in the program. */
        char *s = __kml_any_inspect_at(__kml_nb_pack((u8)tag, pay), 0);
        return s ? s : "[thrown value]";
    }
    }
}

/* The uncaught-error report goes to stderr, as Node's does, after whatever
 * the program had written to stdout. */
extern void *__kml_error_stack(void *err);

void __kml_print_uncaught(int tag, i64 pay) {
    fflush(stdout);
    /* An error's stack (Node's report, short of its source excerpt and
     * version line); any other value as it prints. */
    if ((tag & 0xff) == KML_TAG_ERROR && pay && (*(i64 *)pay & (1LL << 48)))
        fprintf(stderr, "%s\n", (char *)__kml_error_stack((void *)pay));
    else
        fprintf(stderr, "%s\n", (char *)__kml_caught_unc_msg(tag, pay));
    fflush(stderr);
}

/* Set while the 'uncaughtException' listeners run. */
static _Thread_local int in_uncaught;

/* The 'uncaughtException' listeners, for a throw and for an unhandled
 * rejection; true when one ran. */
_Bool __kml_process_uncaught_guarded(u8 tag, i64 pay, _Bool rejection) {
    in_uncaught = 1;
    _Bool handled = __kml_process_uncaught(tag, pay, rejection) & 1;
    in_uncaught = 0;
    return handled;
}

/* The core: record the value, then unwind to the nearest try frame, or end
 * the program (or the worker thread) when there is none. */
extern void __kml_error_capture(void *err);

void __kml_throw_any(int tag, i64 pay) {
    note_deep_throw();
    u8 t = __kml_caught_tag(tag, pay);
    thrown_tag = t;
    thrown_pay = pay;
    __kml_thrown = t == KML_TAG_ERROR ? (void *)pay : NULL;
    /* An error the runtime made (a null dereference, an fs failure) has
     * no frames yet: those where it is thrown, which is where it was made. */
    if (__kml_thrown && (*(i64 *)__kml_thrown & (1LL << 48)) /* the header's error flag */ && !*(void **)((char *)__kml_thrown + KML_ERR_FRAMES) &&
        !*(void **)((char *)__kml_thrown + KML_ERR_STACK))
        __kml_error_capture(__kml_thrown);
    if (__kml_jmp_top == 0) {
        /* A listener of 'uncaughtException' threw: Node reports that error
         * and exits 7 rather than calling the listeners again. */
        if (in_uncaught) {
            __kml_print_uncaught(t, pay);
            KML_EXIT(7);
        }
        /* The process 'uncaughtException' event: when a listener handles
         * it, the rest of the module is abandoned but the program goes on
         * as Node's does: the event loop runs, then 'beforeExit' and 'exit',
         * and it exits with process.exitCode. */
        if (__kml_process_uncaught_guarded(t, pay, 0)) {
            __kml_event_loop_run();
            __kml_run_before_exit(__kml_event_loop_run, __kml_event_loop_alive);
            __kml_run_exit_handlers(__kml_process_exit_code);
            KML_EXIT((int)__kml_process_exit_code);
        }
        /* On a worker thread this does not return: the error goes to the
         * parent's 'error' listener and only that thread ends. */
        __kml_worker_uncaught(t, pay);
        __kml_print_uncaught(t, pay);
        KML_EXIT(1);
    }
    longjmp(__kml_jmp_unwind_slot(), 1);
}

/* The Error-object shim kept for the internal throw sites (assert/fs/
 * encoding/...) and every `throw new Error(...)`. */
void __kml_throw(void *errObj) { __kml_throw_any(KML_TAG_ERROR, (i64)errObj); }

/* Node's MakeCallback for a loop callback (timers, immediates): a throw it
 * does not catch goes to 'uncaughtException', and when a listener handles it
 * the loop goes on (the result is 1); unhandled, it ends the program as at
 * top level. */
#ifdef _WIN32
extern int _setjmp(void *, void *) __attribute__((returns_twice));
#define KML_SETJMP(b) _setjmp((b), 0)
#else
extern int setjmp(void *) __attribute__((returns_twice));
#define KML_SETJMP(b) setjmp(b)
#endif
extern void *__kml_push_jmpbuf(void);
extern void __kml_pop_jmpbuf(void);

_Bool __kml_make_callback(void (*inv)(void *, double, double), void *clo, double a, double b) {
    void *jb = __kml_push_jmpbuf();
    if (KML_SETJMP(jb) != 0) {
        u8 t = thrown_tag;
        i64 pay = thrown_pay;
        if (in_uncaught) {
            __kml_print_uncaught(t, pay);
            KML_EXIT(7);
        }
        if (__kml_process_uncaught_guarded(t, pay, 0))
            return 1;
        __kml_worker_uncaught(t, pay);
        __kml_print_uncaught(t, pay);
        KML_EXIT(1);
    }
    inv(clo, a, b);
    __kml_pop_jmpbuf();
    return 0;
}

/* The runtime's own TypeError (a null dereference, ToPrimitive's failure)
 * and RangeError (string.c's "Invalid string length"), around a headered
 * message. The names are the program's interned strings (hooks.c), compared
 * by address. */
extern void *__kml_error_names[2];

static __attribute__((noreturn, cold)) void throw_kind(i64 kind, void *name, const char *msg) {
    void *o = kml_calloc(KML_HEAP_WORDS, 1, KML_ERROR_SIZE);
    F(o, KML_ERROR_KIND, i64) = kind;
    F(o, KML_ERR_MESSAGE, const char *) = msg;
    F(o, KML_ERR_NAME, void *) = name;
    F(o, KML_ERR_CAUSE, i64) = KML_NB_UNDEFINED;
    __kml_throw(o);
    __builtin_unreachable();
}

void __kml_throw_nullderef(const char *msg) { throw_kind(KML_KIND_TYPEERR, __kml_error_names[0], msg); }
void __kml_throw_range_error(const char *msg) { throw_kind(KML_KIND_RANGEERR, __kml_error_names[1], msg); }
