/* streams.c — the WHATWG stream state machines (TDD-00097; in C per
 * TDD-00240): ReadableStream (one struct fuses stream and default
 * controller), WritableStream (stream, controller and writer), pipeTo,
 * tee() and the TransformStream coupling, all driven by promise reactions
 * (no fiber). Chunks travel as two raw i64 words, so the runtime stays
 * type-agnostic; the program supplies per-site closures (fulfill thunk,
 * source/sink callbacks, per-chunk-type decode). kml_layout.h is prepended
 * by the compiler. */
#include <stdlib.h>
#include <string.h>

typedef long long i64;
typedef unsigned char u8;
typedef void *ptr;

/* The generated program's / promise runtime's side. */
extern ptr __kml_task_alloc_promise(void);
extern void __kml_promise_settle(ptr p, i64 state);
extern void __kml_promise_reject_err(ptr p, i64 err);
extern void __kml_promise_reject_box(ptr p, i64 w);
extern void __kml_promise_mark_handled(ptr p);
extern void __kml_microtask_enqueue(ptr clo);
extern u8 __kml_nb_tag(i64 w);
extern i64 __kml_nb_pay(i64 w);
/* Calls a compiler-emitted per-chunk-type decoder ({i64,i64,i64}(ptr rec),
 * returned in registers, not a C aggregate) and stores its three words. */
extern void __kml_stream_decode(ptr decode, ptr rec, i64 *out);

#define F(p, off, T) (*(T *)((char *)(p) + (off)))
#define ST(p) F(p, KML_PROMISE_STATE, i64)
#define PV0(p) F(p, KML_PROMISE_V0, i64)

typedef struct {
    void *fn;
    void *env;
} clo_t;
_Static_assert(KML_CLOSURE_FN == 0 && KML_CLOSURE_ENV == 8 && KML_CLOSURE_SIZE == 16, "closure layout");

typedef ptr (*fn_env)(ptr env);
typedef ptr (*fn_env_i64)(ptr env, i64 a);
typedef ptr (*fn_env_2i64)(ptr env, i64 a, i64 b);
typedef double (*fn_size)(ptr env, i64 a, i64 b);
typedef void (*fn_ff)(ptr prom, i64 v0, i64 v1, i64 done);
typedef void (*fn_react)(ptr env);

/* A {fn, env} closure header. */
ptr __kml_mkclo(ptr fn, ptr env) {
    clo_t *c = (clo_t *)malloc(sizeof(clo_t));
    c->fn = fn;
    c->env = env;
    return c;
}

/* Attach a reaction closure to a promise: enqueued as a microtask at once
 * when the promise is already settled, appended to its reaction list
 * otherwise. */
void __kml_promise_add_reaction(ptr p, ptr clo) {
    __kml_promise_mark_handled(p);
    if (ST(p) != 0) {
        __kml_microtask_enqueue(clo);
        return;
    }
    char *node = (char *)malloc(KML_REACTION_SIZE);
    F(node, KML_REACTION_CLOSURE, ptr) = clo;
    F(node, KML_REACTION_NEXT, ptr) = F(p, KML_PROMISE_REACTIONS, ptr);
    F(p, KML_PROMISE_REACTIONS, ptr) = node;
}

/* Reaction closure for fn(env). */
static void react(ptr p, fn_react fn, ptr env) {
    __kml_promise_add_reaction(p, __kml_mkclo((ptr)fn, env));
}

/* Head-index FIFO of esz-byte entries: compact to the front before
 * growing, then double (min 8). Returns the slot to write at len++. */
static char *fifo_slot(ptr *data, i64 *cap, i64 *head, i64 *len, i64 esz) {
    if (*len >= *cap) {
        if (*head > 0) {
            char *d = (char *)*data;
            i64 live = *len - *head;
            memmove(d, d + *head * esz, (size_t)(live * esz));
            *len = live;
            *head = 0;
        }
        if (*len >= *cap) {
            i64 nc = *cap * 2 > 8 ? *cap * 2 : 8;
            *data = realloc(*data, (size_t)(nc * esz));
            *cap = nc;
        }
    }
    return (char *)*data + *len * esz;
}

/* ---- ReadableStream ---------------------------------------------------
 * state 0 readable · 1 closed · 2 errored. flags: 1 started · 2 pulling ·
 * 4 pullAgain · 8 closeRequested · 16 disturbed · 32 locked. */
typedef struct {
    i64 state, err;
    ptr qData;
    i64 qCap, qHead, qLen;
    double total, hwm;
    ptr sizeClo, pullClo, cancelClo;
    i64 flags;
    ptr rdData;
    i64 rdCap, rdHead, rdLen;
    ptr closedProm, fulfillFn; /* fulfillFn: void (prom, v0, v1, done) */
} rstream;
_Static_assert(sizeof(rstream) == KML_RSTREAM_SIZE, "rstream layout");

typedef struct {
    i64 v0, v1;
    double size;
} rentry;

#define RS_STARTED 1
#define RS_PULLING 2
#define RS_PULLAGAIN 4
#define RS_CLOSEREQ 8
#define RS_DISTURBED 16
#define RS_LOCKED 32

static void rs_pull_if_needed(rstream *s);
void __kml_rs_error(ptr s, i64 err);

/* A fresh readable stream; closure fields are stored by the construction
 * site. The closed promise is marked handled (SetPromiseIsHandledToTrue). */
ptr __kml_rs_alloc(double hwm, ptr ff) {
    rstream *s = (rstream *)malloc(sizeof(rstream));
    memset(s, 0, sizeof(rstream));
    s->hwm = hwm;
    s->closedProm = __kml_task_alloc_promise();
    __kml_promise_mark_handled(s->closedProm);
    s->fulfillFn = ff;
    return s;
}

void __kml_rs_qpush(ptr sp, i64 v0, i64 v1, double sz) {
    rstream *s = (rstream *)sp;
    rentry *e = (rentry *)fifo_slot(&s->qData, &s->qCap, &s->qHead, &s->qLen, sizeof(rentry));
    e->v0 = v0;
    e->v1 = v1;
    e->size = sz;
    s->qLen++;
    s->total += sz;
}

/* Push a chunk back onto the FRONT of the queue (Readable.unshift): reuses
 * the head slot when one is free, else grows and shifts the live region
 * forward one slot. */
void __kml_rs_qunshift(ptr sp, i64 v0, i64 v1, double sz) {
    rstream *s = (rstream *)sp;
    rentry *e;
    if (s->qHead > 0) {
        s->qHead--;
        e = (rentry *)s->qData + s->qHead;
    } else {
        if (s->qLen >= s->qCap) {
            i64 nc = s->qCap * 2 > 8 ? s->qCap * 2 : 8;
            s->qData = realloc(s->qData, (size_t)(nc * (i64)sizeof(rentry)));
            s->qCap = nc;
        }
        memmove((rentry *)s->qData + 1, s->qData, (size_t)(s->qLen * (i64)sizeof(rentry)));
        s->qLen++;
        e = (rentry *)s->qData;
    }
    e->v0 = v0;
    e->v1 = v1;
    e->size = sz;
    s->total += sz;
}

/* Dequeue the head entry (the caller guarantees non-empty). */
static rentry qpop(rstream *s) {
    rentry e = ((rentry *)s->qData)[s->qHead];
    if (++s->qHead >= s->qLen) s->qHead = s->qLen = 0;
    s->total -= e.size;
    return e;
}

/* The synchronous Readable.read() core: {has, v0, v1}, {0,0,0} when empty.
 * (The IR wrapper __kml_rs_tryread keeps the aggregate-return signature.) */
void __kml_rs_tryread_out(ptr sp, i64 *out) {
    rstream *s = (rstream *)sp;
    if (s->qLen > s->qHead) {
        rentry e = qpop(s);
        out[0] = 1;
        out[1] = e.v0;
        out[2] = e.v1;
    } else {
        out[0] = out[1] = out[2] = 0;
    }
}

/* The pending-read promise FIFO (8-byte ptr entries). */
void __kml_rs_rdpush(ptr sp, ptr prom) {
    rstream *s = (rstream *)sp;
    *(ptr *)fifo_slot(&s->rdData, &s->rdCap, &s->rdHead, &s->rdLen, 8) = prom;
    s->rdLen++;
}

ptr __kml_rs_rdpop(ptr sp) {
    rstream *s = (rstream *)sp;
    if (s->rdHead >= s->rdLen) return NULL;
    ptr prom = ((ptr *)s->rdData)[s->rdHead];
    if (++s->rdHead >= s->rdLen) s->rdHead = s->rdLen = 0;
    return prom;
}

static void rs_pull_done(rstream *s) {
    i64 f = s->flags;
    if (f & RS_PULLAGAIN) {
        s->flags = f & ~(i64)(RS_PULLING | RS_PULLAGAIN);
        rs_pull_if_needed(s);
    } else {
        s->flags = f & ~(i64)RS_PULLING;
    }
}
void __kml_rs_pull_done(ptr s) { rs_pull_done((rstream *)s); }

/* env {promise, stream}: an async pull settled. */
void __kml_rs_pull_settled(ptr env) {
    ptr p = ((ptr *)env)[0];
    rstream *s = (rstream *)((ptr *)env)[1];
    if (ST(p) == 2) __kml_rs_error(s, PV0(p));
    rs_pull_done(s);
}

/* CallPullIfNeeded: shouldPull <=> readable && started && !closeRequested
 * && (a read is pending || the queue is under its high-water mark).
 * Re-entrant pulls coalesce via the pulling/pullAgain flags; an async
 * pull's settlement re-runs the check via a pull_settled reaction. */
static void rs_pull_if_needed(rstream *s) {
    clo_t *pc = (clo_t *)s->pullClo;
    if (!pc) return;
    if (s->state != 0) return;
    i64 f = s->flags;
    if (!(f & RS_STARTED) || (f & RS_CLOSEREQ)) return;
    int rdPending = s->rdHead < s->rdLen;
    if (!(rdPending || s->hwm > s->total)) return;
    if (f & RS_PULLING) {
        s->flags = f | RS_PULLAGAIN;
        return;
    }
    s->flags = f | RS_PULLING;
    ptr p = ((fn_env)pc->fn)(pc->env);
    if (!p) {
        rs_pull_done(s);
        return;
    }
    ptr *env = (ptr *)malloc(16);
    env[0] = p;
    env[1] = s;
    react(p, __kml_rs_pull_settled, env);
}
void __kml_rs_pull_if_needed(ptr s) { rs_pull_if_needed((rstream *)s); }

void __kml_rs_started(ptr sp) {
    rstream *s = (rstream *)sp;
    s->flags |= RS_STARTED;
    rs_pull_if_needed(s);
}

/* controller.enqueue's core: 1, or 0 when the stream is no longer readable
 * / close was already requested (the compile site turns 0 into a thrown
 * TypeError). A pending read is fulfilled directly (the queue is
 * necessarily empty then); otherwise the chunk is queued with its size. */
i64 __kml_rs_enqueue(ptr sp, i64 v0, i64 v1) {
    rstream *s = (rstream *)sp;
    if (s->state != 0 || (s->flags & RS_CLOSEREQ)) return 0;
    ptr prom = __kml_rs_rdpop(s);
    if (prom) {
        ((fn_ff)s->fulfillFn)(prom, v0, v1, 0);
    } else {
        double sz = 1.0;
        clo_t *sc = (clo_t *)s->sizeClo;
        if (sc) sz = ((fn_size)sc->fn)(sc->env, v0, v1);
        __kml_rs_qpush(s, v0, v1, sz);
    }
    rs_pull_if_needed(s);
    return 1;
}

/* Transition to closed: resolve every pending read with {done: true} and
 * settle the closed promise fulfilled. */
void __kml_rs_finalize_close(ptr sp) {
    rstream *s = (rstream *)sp;
    if (s->state != 0) return;
    s->state = 1;
    fn_ff ff = (fn_ff)s->fulfillFn;
    ptr prom;
    while ((prom = __kml_rs_rdpop(s)) != NULL) ff(prom, 0, 0, 1);
    __kml_promise_settle(s->closedProm, 1);
}

/* controller.close(): 1, or 0 when not allowed (already closed/errored or
 * close already requested). */
i64 __kml_rs_close(ptr sp) {
    rstream *s = (rstream *)sp;
    if (s->state != 0 || (s->flags & RS_CLOSEREQ)) return 0;
    s->flags |= RS_CLOSEREQ;
    if (s->qHead >= s->qLen) __kml_rs_finalize_close(s);
    return 1;
}

/* controller.error(e) / a rejected pull: drop the queue, reject every
 * pending read and the closed promise with the stored error. */
void __kml_rs_error(ptr sp, i64 err) {
    rstream *s = (rstream *)sp;
    if (s->state != 0) return;
    s->state = 2;
    s->err = err;
    s->qHead = s->qLen = 0;
    s->total = 0.0;
    ptr prom;
    while ((prom = __kml_rs_rdpop(s)) != NULL) __kml_promise_reject_err(prom, err);
    __kml_promise_reject_err(s->closedProm, err);
}

/* reader.read()'s core: the read promise. */
ptr __kml_rs_read(ptr sp) {
    rstream *s = (rstream *)sp;
    s->flags |= RS_DISTURBED;
    if (s->state == 2) {
        ptr ep = __kml_task_alloc_promise();
        __kml_promise_reject_err(ep, s->err);
        return ep;
    }
    if (s->state == 1) {
        ptr cp = __kml_task_alloc_promise();
        ((fn_ff)s->fulfillFn)(cp, 0, 0, 1);
        return cp;
    }
    if (s->qHead < s->qLen) {
        rentry e = qpop(s);
        ptr pm = __kml_task_alloc_promise();
        ((fn_ff)s->fulfillFn)(pm, e.v0, e.v1, 0);
        if ((s->flags & RS_CLOSEREQ) && s->qHead >= s->qLen)
            __kml_rs_finalize_close(s);
        else
            rs_pull_if_needed(s);
        return pm;
    }
    ptr pp = __kml_task_alloc_promise();
    __kml_rs_rdpush(s, pp);
    rs_pull_if_needed(s);
    return pp;
}

/* controller.desiredSize: hwm - total while readable; 0 once
 * closed/errored (JS's null-when-errored is a documented caveat). */
double __kml_rs_desired(ptr sp) {
    rstream *s = (rstream *)sp;
    return s->state == 0 ? s->hwm - s->total : 0.0;
}

/* stream/reader cancel(reason): drop the queue, close, invoke the
 * underlying source's cancel callback; the returned promise is the
 * callback's own when it yields one, else already-fulfilled. */
ptr __kml_rs_cancel(ptr sp, i64 reason) {
    rstream *s = (rstream *)sp;
    s->flags |= RS_DISTURBED;
    if (s->state == 2) {
        ptr ep = __kml_task_alloc_promise();
        __kml_promise_reject_err(ep, s->err);
        return ep;
    }
    if (s->state == 1) {
        ptr ap = __kml_task_alloc_promise();
        __kml_promise_settle(ap, 1);
        return ap;
    }
    s->qHead = s->qLen = 0;
    s->total = 0.0;
    __kml_rs_finalize_close(s);
    clo_t *cc = (clo_t *)s->cancelClo;
    if (cc) {
        ptr p = ((fn_env_i64)cc->fn)(cc->env, reason);
        if (p) return p;
    }
    ptr fp = __kml_task_alloc_promise();
    __kml_promise_settle(fp, 1);
    return fp;
}

/* The reader lock (getReader/releaseLock). */
i64 __kml_rs_lock(ptr sp) {
    rstream *s = (rstream *)sp;
    if (s->flags & RS_LOCKED) return 0;
    s->flags |= RS_LOCKED;
    return 1;
}

void __kml_rs_unlock(ptr sp) { ((rstream *)sp)->flags &= ~(i64)RS_LOCKED; }

/* ---- WritableStream ---------------------------------------------------
 * Every write() enqueues a {chunk words, size, per-write promise} entry; a
 * drain loop dequeues one at a time, invokes the sink's write and advances
 * on its settlement via a promise reaction (sink writes never overlap).
 * Backpressure is the writer.ready promise: re-created pending when
 * desiredSize drops to 0 or below, settled fulfilled when it recovers.
 * state 0 writable · 1 closed · 2 errored. flags: 1 started · 2 inFlight ·
 * 8 closeRequested · 32 locked. */
typedef struct {
    i64 state, err;
    ptr qData;
    i64 qCap, qHead, qLen;
    double total, hwm;
    ptr sizeClo;
    ptr writeClo; /* (env, v0, v1) -> promise|null */
    ptr closeClo; /* (env) -> promise|null, or null */
    ptr abortClo; /* (env, reason) -> promise|null, or null */
    i64 flags;
    ptr readyProm;  /* current writer.ready promise */
    ptr closedProm; /* backs writer.closed */
    ptr closeProm;  /* writer.close()'s promise (null until requested) */
} wstream;
_Static_assert(sizeof(wstream) == KML_WSTREAM_SIZE, "wstream layout");

typedef struct {
    i64 v0, v1;
    double size;
    ptr prom;
} wentry;

#define WS_STARTED 1
#define WS_INFLIGHT 2
#define WS_CLOSEREQ 8
#define WS_LOCKED 32

void __kml_ws_error(ptr s, i64 err);
void __kml_ws_advance(ptr s);

/* A fresh writable stream: ready starts fulfilled (no backpressure while
 * the queue is empty), closed starts pending; both marked handled. */
ptr __kml_ws_alloc(double hwm) {
    wstream *s = (wstream *)malloc(sizeof(wstream));
    memset(s, 0, sizeof(wstream));
    s->hwm = hwm;
    ptr rdy = __kml_task_alloc_promise();
    __kml_promise_mark_handled(rdy);
    __kml_promise_settle(rdy, 1);
    s->readyProm = rdy;
    s->closedProm = __kml_task_alloc_promise();
    __kml_promise_mark_handled(s->closedProm);
    return s;
}

/* Re-arm or settle writer.ready to track backpressure (desiredSize <= 0
 * while writable => pending ready). */
void __kml_ws_update_ready(ptr sp) {
    wstream *s = (wstream *)sp;
    if (s->state != 0) return;
    int bp = s->hwm <= s->total;
    int settled = ST(s->readyProm) != 0;
    if (bp) {
        if (settled) {
            ptr np = __kml_task_alloc_promise();
            __kml_promise_mark_handled(np);
            s->readyProm = np;
        }
    } else if (!settled) {
        __kml_promise_settle(s->readyProm, 1);
    }
}

void __kml_ws_qpush(ptr sp, i64 v0, i64 v1, double sz, ptr prom) {
    wstream *s = (wstream *)sp;
    wentry *e = (wentry *)fifo_slot(&s->qData, &s->qCap, &s->qHead, &s->qLen, sizeof(wentry));
    e->v0 = v0;
    e->v1 = v1;
    e->size = sz;
    e->prom = prom;
    s->qLen++;
    s->total += sz;
    __kml_ws_update_ready(s);
}

void __kml_ws_finish_close(ptr sp) {
    wstream *s = (wstream *)sp;
    s->state = 1;
    s->flags &= ~(i64)WS_INFLIGHT;
    __kml_promise_settle(s->closedProm, 1);
    if (s->closeProm) __kml_promise_settle(s->closeProm, 1);
}

/* env {write promise, stream, per-write promise}. */
void __kml_ws_write_settled(ptr env) {
    ptr wp = ((ptr *)env)[0];
    wstream *s = (wstream *)((ptr *)env)[1];
    ptr wprom = ((ptr *)env)[2];
    if (ST(wp) == 2) {
        i64 err = PV0(wp);
        __kml_promise_reject_err(wprom, err);
        __kml_ws_error(s, err);
        return;
    }
    __kml_promise_settle(wprom, 1);
    s->flags &= ~(i64)WS_INFLIGHT;
    __kml_ws_advance(s);
}

/* env {close promise, stream}. */
void __kml_ws_close_settled(ptr env) {
    ptr cp = ((ptr *)env)[0];
    ptr s = ((ptr *)env)[1];
    if (ST(cp) == 2)
        __kml_ws_error(s, PV0(cp));
    else
        __kml_ws_finish_close(s);
}

/* The drain loop: while writable, started and no write in flight, dequeue
 * one entry and run the sink's write; when the queue empties with close
 * requested, run the sink's close. */
void __kml_ws_advance(ptr sp) {
    wstream *s = (wstream *)sp;
    for (;;) {
        if (s->state != 0) return;
        i64 f = s->flags;
        if (!(f & WS_STARTED) || (f & WS_INFLIGHT)) return;
        if (s->qHead < s->qLen) {
            wentry e = ((wentry *)s->qData)[s->qHead];
            if (++s->qHead >= s->qLen) s->qHead = s->qLen = 0;
            s->total -= e.size;
            __kml_ws_update_ready(s);
            s->flags = f | WS_INFLIGHT;
            clo_t *wc = (clo_t *)s->writeClo;
            ptr wp = wc ? ((fn_env_2i64)wc->fn)(wc->env, e.v0, e.v1) : NULL;
            if (!wp) {
                __kml_promise_settle(e.prom, 1);
                s->flags &= ~(i64)WS_INFLIGHT;
                continue;
            }
            ptr *env = (ptr *)malloc(24);
            env[0] = wp;
            env[1] = s;
            env[2] = e.prom;
            react(wp, __kml_ws_write_settled, env);
            return;
        }
        if (!(f & WS_CLOSEREQ)) return;
        s->flags = f | WS_INFLIGHT;
        clo_t *cc = (clo_t *)s->closeClo;
        ptr cp2 = cc ? ((fn_env)cc->fn)(cc->env) : NULL;
        if (!cp2) {
            __kml_ws_finish_close(s);
            return;
        }
        ptr *env2 = (ptr *)malloc(16);
        env2[0] = cp2;
        env2[1] = s;
        react(cp2, __kml_ws_close_settled, env2);
        return;
    }
}

/* Move to errored: reject every queued write promise, the ready and closed
 * promises, and any pending close() promise. */
void __kml_ws_error(ptr sp, i64 err) {
    wstream *s = (wstream *)sp;
    if (s->state != 0) return;
    s->state = 2;
    s->err = err;
    wentry *d = (wentry *)s->qData;
    while (s->qHead < s->qLen) {
        __kml_promise_reject_err(d[s->qHead].prom, err);
        s->qHead++;
    }
    s->qHead = s->qLen = 0;
    s->total = 0.0;
    __kml_promise_reject_err(s->readyProm, err);
    __kml_promise_reject_err(s->closedProm, err);
    if (s->closeProm) __kml_promise_reject_err(s->closeProm, err);
}

/* The per-write promise (null when the stream is closed / closing). */
ptr __kml_ws_write(ptr sp, i64 v0, i64 v1) {
    wstream *s = (wstream *)sp;
    ptr prom = __kml_task_alloc_promise();
    if (s->state == 2) {
        __kml_promise_reject_err(prom, s->err);
        return prom;
    }
    if (s->state == 1 || (s->flags & WS_CLOSEREQ)) return NULL;
    double sz = 1.0;
    clo_t *sc = (clo_t *)s->sizeClo;
    if (sc) sz = ((fn_size)sc->fn)(sc->env, v0, v1);
    __kml_ws_qpush(s, v0, v1, sz, prom);
    __kml_ws_advance(s);
    return prom;
}

/* The close promise (null when close is not allowed). */
ptr __kml_ws_close(ptr sp) {
    wstream *s = (wstream *)sp;
    if (s->state == 2) {
        ptr p0 = __kml_task_alloc_promise();
        __kml_promise_reject_err(p0, s->err);
        return p0;
    }
    if (s->state == 1 || (s->flags & WS_CLOSEREQ)) return NULL;
    ptr prom = __kml_task_alloc_promise();
    s->closeProm = prom;
    s->flags |= WS_CLOSEREQ;
    __kml_ws_advance(s);
    return prom;
}

/* The abort promise: error the stream with the reason, run the sink's
 * abort, fulfill when it completes. */
ptr __kml_ws_abort(ptr sp, i64 reason) {
    wstream *s = (wstream *)sp;
    if (s->state != 0) {
        ptr ap = __kml_task_alloc_promise();
        __kml_promise_settle(ap, 1);
        return ap;
    }
    __kml_ws_error(s, reason);
    clo_t *ac = (clo_t *)s->abortClo;
    if (ac) {
        ptr p2 = ((fn_env_i64)ac->fn)(ac->env, reason);
        if (p2) return p2;
    }
    ptr fp = __kml_task_alloc_promise();
    __kml_promise_settle(fp, 1);
    return fp;
}

double __kml_ws_desired(ptr sp) {
    wstream *s = (wstream *)sp;
    return s->state == 0 ? s->hwm - s->total : 0.0;
}

void __kml_ws_started(ptr sp) {
    wstream *s = (wstream *)sp;
    s->flags |= WS_STARTED;
    __kml_ws_advance(s);
}

i64 __kml_ws_lock(ptr sp) {
    wstream *s = (wstream *)sp;
    if (s->flags & WS_LOCKED) return 0;
    s->flags |= WS_LOCKED;
    return 1;
}

void __kml_ws_unlock(ptr sp) { ((wstream *)sp)->flags &= ~(i64)WS_LOCKED; }

/* ---- pipeTo -----------------------------------------------------------
 * flags: 1 preventClose · 2 preventAbort · 4 preventCancel. sigA points at
 * the AbortSignal's aborted flag byte (or null), sigR at its reason slot. */
typedef struct {
    rstream *src;
    wstream *dst;
    ptr prom;   /* the pipe promise */
    ptr decode; /* per-chunk-type decoder */
    i64 flags;
    u8 *sigA;
    i64 *sigR;
    i64 v0, v1; /* pending chunk words */
    ptr cur;    /* the promise the current reaction fires on */
} pipectx;
_Static_assert(sizeof(pipectx) == KML_PIPE_SIZE, "pipe layout");

#define PIPE_NOCLOSE 1
#define PIPE_NOABORT 2
#define PIPE_NOCANCEL 4

static void pipe_step(pipectx *c);
static void pipe_on_read(ptr env);
static void pipe_on_ready(ptr env);
static void pipe_on_written(ptr env);
static void pipe_on_closed(ptr env);

ptr __kml_pipe_to(ptr src, ptr dst, ptr decode, i64 flags, ptr sigA, ptr sigR) {
    pipectx *c = (pipectx *)malloc(sizeof(pipectx));
    memset(c, 0, sizeof(pipectx));
    c->src = (rstream *)src;
    c->dst = (wstream *)dst;
    c->prom = __kml_task_alloc_promise();
    c->decode = decode;
    c->flags = flags;
    c->sigA = (u8 *)sigA;
    c->sigR = (i64 *)sigR;
    pipe_step(c);
    return c->prom;
}

/* #reason is a boxed any: the source and sink get its error object (or
 * none); the pipe's promise rejects with the value itself. */
static void pipe_signal_abort(pipectx *c) {
    i64 box = c->sigR ? *c->sigR : KML_NB_UNDEFINED;
    i64 reason = __kml_nb_tag(box) == KML_TAG_ERROR ? __kml_nb_pay(box) : 0;
    if (!(c->flags & PIPE_NOCANCEL)) __kml_rs_cancel(c->src, reason);
    if (!(c->flags & PIPE_NOABORT)) __kml_ws_abort(c->dst, reason);
    __kml_promise_reject_box(c->prom, box);
}

static void pipe_step(pipectx *c) {
    if (c->sigA && *c->sigA) {
        pipe_signal_abort(c);
        return;
    }
    c->cur = __kml_rs_read(c->src);
    react(c->cur, pipe_on_read, c);
}

static void pipe_dest_error(pipectx *c, i64 err) {
    if (!(c->flags & PIPE_NOCANCEL)) __kml_rs_cancel(c->src, err);
    __kml_promise_reject_err(c->prom, err);
}

static void pipe_on_read(ptr env) {
    pipectx *c = (pipectx *)env;
    ptr p = c->cur;
    if (ST(p) == 2) {
        i64 err = PV0(p);
        if (!(c->flags & PIPE_NOABORT)) __kml_ws_abort(c->dst, err);
        __kml_promise_reject_err(c->prom, err);
        return;
    }
    i64 dv[3];
    __kml_stream_decode(c->decode, (ptr)PV0(p), dv);
    if (dv[2] != 0) {
        if (!(c->flags & PIPE_NOCLOSE)) {
            ptr cp = __kml_ws_close(c->dst);
            if (cp) {
                c->cur = cp;
                react(cp, pipe_on_closed, c);
                return;
            }
        }
        __kml_promise_settle(c->prom, 1);
        return;
    }
    c->v0 = dv[0];
    c->v1 = dv[1];
    ptr rdy = c->dst->readyProm;
    if (ST(rdy) == 1)
        pipe_on_ready(c);
    else
        react(rdy, pipe_on_ready, c);
}

static void pipe_on_ready(ptr env) {
    pipectx *c = (pipectx *)env;
    if (c->dst->state == 2) {
        pipe_dest_error(c, c->dst->err);
        return;
    }
    ptr wp = __kml_ws_write(c->dst, c->v0, c->v1);
    if (!wp) {
        __kml_promise_settle(c->prom, 1);
        return;
    }
    c->cur = wp;
    react(wp, pipe_on_written, c);
}

static void pipe_on_written(ptr env) {
    pipectx *c = (pipectx *)env;
    if (ST(c->cur) == 2) {
        pipe_dest_error(c, PV0(c->cur));
        return;
    }
    pipe_step(c);
}

static void pipe_on_closed(ptr env) {
    pipectx *c = (pipectx *)env;
    if (ST(c->cur) == 2)
        __kml_promise_reject_err(c->prom, PV0(c->cur));
    else
        __kml_promise_settle(c->prom, 1);
}

/* ---- tee(): two branches share one source read at a time. ------------ */
typedef struct {
    rstream *src, *b1, *b2;
    ptr decode;
    i64 reading, canceled1, canceled2, reason1, reason2;
} teectx;
_Static_assert(sizeof(teectx) == KML_TEE_SIZE, "tee layout");

/* env {read promise, tee ctx}. */
static void tee_on_read(ptr env) {
    ptr p = ((ptr *)env)[0];
    teectx *c = (teectx *)((ptr *)env)[1];
    c->reading = 0;
    if (ST(p) == 2) {
        i64 err = PV0(p);
        __kml_rs_error(c->b1, err);
        __kml_rs_error(c->b2, err);
        return;
    }
    i64 dv[3];
    __kml_stream_decode(c->decode, (ptr)PV0(p), dv);
    int alive1 = c->canceled1 == 0, alive2 = c->canceled2 == 0;
    if (dv[2] != 0) {
        if (alive1) __kml_rs_close(c->b1);
        if (alive2) __kml_rs_close(c->b2);
        return;
    }
    if (alive1) __kml_rs_enqueue(c->b1, dv[0], dv[1]);
    if (alive2) __kml_rs_enqueue(c->b2, dv[0], dv[1]);
}

void __kml_tee_pullhook(ptr cp) {
    teectx *c = (teectx *)cp;
    if (c->reading) return;
    c->reading = 1;
    ptr p = __kml_rs_read(c->src);
    ptr *env = (ptr *)malloc(16);
    env[0] = p;
    env[1] = c;
    react(p, tee_on_read, env);
}

ptr __kml_tee_pull(ptr ctx) {
    __kml_tee_pullhook(ctx);
    return NULL;
}

/* env {tee ctx, branch number (1 or 2) as a pointer}. */
ptr __kml_tee_cancel(ptr env, i64 reason) {
    teectx *c = (teectx *)((ptr *)env)[0];
    i64 which = (i64)(size_t)((ptr *)env)[1];
    if (which == 1) {
        c->canceled1 = 1;
        c->reason1 = reason;
    } else {
        c->canceled2 = 1;
        c->reason2 = reason;
    }
    if (c->canceled1 & c->canceled2) __kml_rs_cancel(c->src, reason);
    return NULL;
}

/* ---- TransformStream: the writable sink's write runs the transform when
 * the readable side has capacity, else parks the chunk; the readable
 * side's pull resumes a parked chunk. ---------------------------------- */
typedef struct {
    rstream *readable;
    ptr writable;
    ptr transClo, flushClo;
    i64 parked, pv0, pv1;
    ptr parkedProm; /* the in-flight writable write promise while a chunk waits */
    ptr closeProm;  /* settled once flush + readable close complete */
} tsctx;
_Static_assert(sizeof(tsctx) == KML_TS_SIZE, "ts layout");

ptr __kml_ts_run_transform(ptr cp, i64 v0, i64 v1) {
    tsctx *c = (tsctx *)cp;
    clo_t *tc = (clo_t *)c->transClo;
    if (!tc) {
        __kml_rs_enqueue(c->readable, v0, v1);
        return NULL;
    }
    return ((fn_env_2i64)tc->fn)(tc->env, v0, v1);
}

ptr __kml_ts_sink_write(ptr cp, i64 v0, i64 v1) {
    tsctx *c = (tsctx *)cp;
    if (__kml_rs_desired(c->readable) > 0.0) return __kml_ts_run_transform(c, v0, v1);
    c->parked = 1;
    c->pv0 = v0;
    c->pv1 = v1;
    c->parkedProm = __kml_task_alloc_promise();
    /* A read may already be parked on the readable (its pull ran before
     * this chunk arrived): re-kick pull so the parked chunk is picked up. */
    rs_pull_if_needed(c->readable);
    return c->parkedProm;
}

/* env {transform promise, target promise}: mirror its settlement. */
void __kml_ts_mirror_settle(ptr env) {
    ptr p = ((ptr *)env)[0];
    ptr tgt = ((ptr *)env)[1];
    if (ST(p) == 2)
        __kml_promise_reject_err(tgt, PV0(p));
    else
        __kml_promise_settle(tgt, 1);
}

ptr __kml_ts_pull(ptr cp) {
    tsctx *c = (tsctx *)cp;
    if (!c->parked) return NULL;
    c->parked = 0;
    ptr wprom = c->parkedProm;
    ptr p = __kml_ts_run_transform(c, c->pv0, c->pv1);
    if (!p) {
        __kml_promise_settle(wprom, 1);
        return NULL;
    }
    ptr *env = (ptr *)malloc(16);
    env[0] = p;
    env[1] = wprom;
    react(p, __kml_ts_mirror_settle, env);
    return NULL;
}

/* env {flush promise, ts ctx}. */
void __kml_ts_flush_done(ptr env) {
    ptr p = ((ptr *)env)[0];
    tsctx *c = (tsctx *)((ptr *)env)[1];
    if (ST(p) == 2) {
        i64 err = PV0(p);
        __kml_rs_error(c->readable, err);
        __kml_promise_reject_err(c->closeProm, err);
    } else {
        __kml_rs_close(c->readable);
        __kml_promise_settle(c->closeProm, 1);
    }
}

ptr __kml_ts_sink_close(ptr cp) {
    tsctx *c = (tsctx *)cp;
    clo_t *fc = (clo_t *)c->flushClo;
    ptr p = fc ? ((fn_env)fc->fn)(fc->env) : NULL;
    if (!p) {
        __kml_rs_close(c->readable);
        return NULL;
    }
    c->closeProm = __kml_task_alloc_promise();
    ptr *env = (ptr *)malloc(16);
    env[0] = p;
    env[1] = c;
    react(p, __kml_ts_flush_done, env);
    return c->closeProm;
}

ptr __kml_ts_sink_abort(ptr cp, i64 reason) {
    __kml_rs_error(((tsctx *)cp)->readable, reason);
    return NULL;
}
