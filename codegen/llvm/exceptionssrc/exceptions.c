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
extern void __kml_worker_uncaught(u8 tag, i64 pay);

#ifdef KML_WORKERS
/* process.exit's end: the thread on a worker, the process elsewhere. */
extern void __kml_thread_exit(int code) __attribute__((noreturn));
#define KML_EXIT(c) __kml_thread_exit(c)
#else
#define KML_EXIT(c) exit(c)
#endif

_Thread_local void *__kml_thrown;
static _Thread_local u8 thrown_tag = KML_TAG_ERROR;
static _Thread_local i64 thrown_pay;

void *__kml_get_thrown(void) { return __kml_thrown; }
u8 __kml_get_thrown_tag(void) { return thrown_tag; }
i64 __kml_get_thrown_pay(void) { return thrown_pay; }

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

/* The message an uncaught throw prints: an Error yields its .message; a
 * string is itself; a number is dtoa'd; booleans, null and undefined their
 * literals; any other value a generic placeholder. NUL-terminated. */
void *__kml_caught_unc_msg(int tag, i64 pay) {
    switch (tag & 0xff) {
    case KML_TAG_ERROR: return F((void *)pay, KML_ERROR_MSG, void *);
    case 2: return (void *)pay;
    case 1: {
        char *buf = (char *)malloc(32);
        double d;
        __builtin_memcpy(&d, &pay, sizeof d);
        __kml_dtoa(buf, d);
        return buf;
    }
    case 3: return pay != 0 ? "true" : "false";
    case 4: return "null";
    case 5: return "undefined";
    default: return "[thrown value]\n";
    }
}

/* The core: record the value, then unwind to the nearest try frame, or end
 * the program (or the worker thread) when there is none. */
void __kml_throw_any(int tag, i64 pay) {
    u8 t = __kml_caught_tag(tag, pay);
    thrown_tag = t;
    thrown_pay = pay;
    __kml_thrown = t == KML_TAG_ERROR ? (void *)pay : NULL;
    if (__kml_jmp_top == 0) {
        /* The process 'uncaughtException' event: if a listener runs the
         * default print is skipped, but we still exit (the stack has
         * unwound to the top-level catch-all). Emits 'exit' on the way out,
         * like Node. */
        if (__kml_process_uncaught(t, pay, 0) & 1) {
            __kml_run_exit_handlers(1);
            KML_EXIT(1);
        }
        /* On a worker thread this does not return: the error goes to the
         * parent's 'error' listener and only that thread ends. */
        __kml_worker_uncaught(t, pay);
        printf("Uncaught: %s\n", (char *)__kml_caught_unc_msg(t, pay));
        KML_EXIT(1);
    }
    longjmp(__kml_jmp_unwind_slot(), 1);
}

/* The Error-object shim kept for the internal throw sites (assert/fs/
 * encoding/...) and every `throw new Error(...)`. */
void __kml_throw(void *errObj) { __kml_throw_any(KML_TAG_ERROR, (i64)errObj); }
