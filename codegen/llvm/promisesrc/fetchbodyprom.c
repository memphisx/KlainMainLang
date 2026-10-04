/* fetchbodyprom.c — a Response body accessor (text()/json()/arrayBuffer())
 * returns a pending promise settled off the fetch reactor's completion
 * (TDD-00186 Part B; in C per TDD-00240). A request is {pending, closure}:
 * `pending` the Response's in-flight fetch handle, `closure` a {fn, env} whose
 * settle runner builds the value from the now-complete body and settles or
 * rejects the promise. Requests park in a thread-local registry the curl
 * drain fires by `pending` on CURLMSG_DONE. The promise and Response are kept
 * reachable by the awaiting task's own stack (GC-scanned), so the registry is
 * plain system memory. kml_layout.h is prepended by the compiler. */
#include <stdlib.h>

typedef long long i64;
typedef void (*kml_fn)(void *);

#define F(p, off, T) (*(T *)((char *)(p) + (off)))

typedef struct {
    void *pending, *closure;
} fbp_req;

static _Thread_local fbp_req *fbp_data;
static _Thread_local i64 fbp_len, fbp_cap;

/* Call the settle runner fn(env), then free the env and closure (their GC
 * contents, the promise and Response, are rooted by the awaiting task, not
 * by this freed system memory). */
void __kml_fbp_invoke(void *closure) {
    kml_fn fn = F(closure, KML_CLOSURE_FN, kml_fn);
    void *env = F(closure, KML_CLOSURE_ENV, void *);
    fn(env);
    free(env);
    free(closure);
}

/* Settle now when the body is already complete (pending null, or done set),
 * else park the request. */
void __kml_fetch_bodyprom_register(void *pending, void *closure) {
    if (!pending || F(pending, KML_FETCHREQ_DONE, i64) != 0) {
        __kml_fbp_invoke(closure);
        return;
    }
    if (fbp_len + 1 > fbp_cap) {
        i64 nc = fbp_cap * 2 > 4 ? fbp_cap * 2 : 4;
        fbp_data = (fbp_req *)realloc(fbp_data, (size_t)nc * sizeof(fbp_req));
        fbp_cap = nc;
    }
    fbp_data[fbp_len].pending = pending;
    fbp_data[fbp_len].closure = closure;
    fbp_len++;
}

/* Fire and remove the request keyed to this completed fetch (at most one: a
 * second body read on one Response is a WHATWG "disturbed" violation).
 * Removal is swap-with-last; called from the curl drain on CURLMSG_DONE. */
void __kml_fetch_bodyprom_on_done(void *pending) {
    for (i64 i = 0; i < fbp_len; i++) {
        if (fbp_data[i].pending != pending) continue;
        void *clo = fbp_data[i].closure;
        fbp_data[i] = fbp_data[--fbp_len];
        __kml_fbp_invoke(clo);
        i--; /* the swapped-in entry sits at i now; the invoke may park more */
    }
}
