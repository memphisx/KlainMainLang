/* dynimportwatch.c — the importer-side bridge to a dynamic-import island's
 * module task (TDD-00225, in C per TDD-00240). An island with a top-level
 * await is driven by this program's event loop through four exports it
 * defines: state, poll (one non-blocking turn of its own loop; 0x100 set when
 * it reported idle), next_deadline_ns and error. The import() promise is a
 * pending task promise settled from the island's module promise.
 *
 * The loop hooks (dispatch, keepalive, next_deadline_ns) are the runtime's
 * fixed names; a program without import() gets no-op stubs from the compiler. */
#include <stdlib.h>

typedef long long i64;
typedef i64 (*kml_poll_fn)(void);
typedef void *(*kml_err_fn)(void);
typedef void (*kml_settle_fn)(void *handle, void *prom);

/* Defined by the compiler. */
extern void __kml_promise_reject_err(void *p, i64 err);
extern i64 __kml_monotonic_ns(void);

typedef struct {
    void *handle, *prom;
    kml_settle_fn settle;
    kml_poll_fn poll;
    kml_err_fn err;
    kml_poll_fn deadline;
    i64 idle, done;
} Watch;

static Watch *watches;
static i64 nwatches;

static void settle_from(void *handle, void *prom, kml_settle_fn settle, kml_err_fn err, i64 st) {
    if (st == 1) settle(handle, prom);
    else __kml_promise_reject_err(prom, (i64)(size_t)err());
}

/* Settles the import() promise at once when the island's module promise is
 * already settled (no top-level await, or one that settled during init),
 * else appends a watcher. */
void __kml_dynimport_watch(void *handle, void *prom, kml_settle_fn settle, kml_poll_fn state,
                           kml_poll_fn poll, kml_err_fn err, kml_poll_fn deadline) {
    i64 st = state();
    if (st != 0) { settle_from(handle, prom, settle, err, st); return; }
    watches = (Watch *)realloc(watches, (size_t)(nwatches + 1) * sizeof(Watch));
    watches[nwatches++] = (Watch){handle, prom, settle, poll, err, deadline, 0, 0};
}

/* Polls every pending island once per loop turn and settles its promise when
 * the island's module promise has. */
void __kml_dynimport_dispatch(void) {
    i64 len = nwatches;
    for (i64 i = 0; i < len; i++) {
        if (watches[i].done) continue;
        i64 r = watches[i].poll();
        i64 st = r & 255;
        /* the poll ran island code; the list may have grown (realloc), index afresh */
        watches[i].idle = r >> 8;
        if (st == 0) continue;
        watches[i].done = 1;
        Watch w = watches[i];
        settle_from(w.handle, w.prom, w.settle, w.err, st);
    }
}

/* Holds the loop while a pending island still has something that can wake it
 * (its last poll did not report idle). */
_Bool __kml_dynimport_keepalive(void) {
    for (i64 i = 0; i < nwatches; i++)
        if (!watches[i].done && watches[i].idle == 0) return 1;
    return 0;
}

/* Folds the islands' earliest timer into the loop's select() wait; while a
 * pending, non-idle island reports no timer, the wait is capped at 10 ms —
 * its sockets are invisible to this loop's fd sets (the V1 blind spot). */
i64 __kml_dynimport_next_deadline_ns(void) {
    i64 best = 0;
    int cap = 0;
    for (i64 i = 0; i < nwatches; i++) {
        if (watches[i].done) continue;
        i64 dl = watches[i].deadline();
        if (dl != 0 && (best == 0 || dl < best)) best = dl;
        if (watches[i].idle == 0 && dl == 0) cap = 1;
    }
    if (!cap) return best;
    /* a pending island with no timer but something alive: look again in 10 ms,
     * or sooner if some island's timer is due before that */
    i64 soon = __kml_monotonic_ns() + 10000000;
    return (best != 0 && best < soon) ? best : soon;
}
