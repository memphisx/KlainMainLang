/* fetchdrive.c — the bridge from a raw fetch handle to a task promise
 * (TDD-00090, TDD-00097 Stage 4; in C per TDD-00240). env is {slot, prom}: it
 * waits for the response headers (__kml_await_fetch_headers, the same wait
 * `await` takes), builds the Response, stores it into prom's value slot and
 * settles prom fulfilled. A transport-level failure, which the wait throws, is
 * caught and rejects prom — `fetch(u).catch(e => …)` recovers it; an HTTP
 * 4xx/5xx is a fulfilled Response, per WHATWG. The fetch slot is NOT freed: a
 * fetch Promise<Response> is a reusable value (`const p = fetch(u);
 * p.then(f); await p` must still read a live slot). kml_layout.h is prepended
 * by the compiler. */
#include <stdlib.h>

typedef long long i64;
typedef unsigned char u8;

#define F(p, off, T) (*(T *)((char *)(p) + (off)))

#if defined(_WIN32)
extern int _setjmp(void *, void *) __attribute__((returns_twice));
#define KML_SETJMP(b) _setjmp((b), 0)
#else
extern int setjmp(void *) __attribute__((returns_twice));
#define KML_SETJMP(b) setjmp(b)
#endif

extern void *__kml_push_jmpbuf(void);
extern void __kml_pop_jmpbuf(void);
extern i64 __kml_await_fetch_headers(void *);
extern i64 __kml_get_thrown_pay(void);
extern u8 __kml_get_thrown_tag(void);
extern void __kml_promise_settle(void *, i64);
extern void __kml_promise_reject_with(void *, i64, i64);

void __kml_fetch_drive_run(void *env) {
    void *slot = ((void **)env)[0];
    void *prom = ((void **)env)[1];
    void *jb = __kml_push_jmpbuf();
    if (KML_SETJMP(jb) != 0) {
        /* Reject with the thrown value itself — payload in v0, tag in v1, the
         * shape every rejection reader expects. A fetch aborted with a
         * non-Error reason (abort(42), abort("stop")) rejects with exactly
         * that value, not a pointer. */
        i64 pay = __kml_get_thrown_pay();
        i64 tag = (i64)__kml_get_thrown_tag();
        __kml_promise_reject_with(prom, tag, pay);
        return;
    }
    void *pending = *(void **)slot;
    /* Resolve at headers-complete, like await does — the Response carries the
     * pending handle; body reads drive the rest lazily. */
    i64 status = __kml_await_fetch_headers(pending);
    __kml_pop_jmpbuf();
    /* Zeroed: the constructed-Response slots (headers, statusText, type,
     * stream) are null on a fetched one. */
    void *resp = calloc(1, KML_RESP_SIZE);
    F(resp, KML_RESP_STATUS, double) = (double)status;
    F(resp, KML_RESP_OK, u8) = status >= 200 && status < 300;
    F(resp, KML_RESP_PENDING, void *) = pending;
    F(prom, KML_PROMISE_V0, i64) = (i64)resp;
    __kml_promise_settle(prom, 1);
}
