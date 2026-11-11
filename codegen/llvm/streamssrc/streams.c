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
extern void __kml_promise_reject_box(ptr p, i64 w);
extern void __kml_promise_mark_handled(ptr p);
extern void __kml_microtask_enqueue(ptr clo);
extern u8 __kml_nb_tag(i64 w);
extern i64 __kml_nb_pay(i64 w);
extern i64 __kml_nb_pack(u8 tag, i64 pay);

/* A rejected promise's reason, boxed (a stream keeps its error as the
 * value given: controller.error(42) is 42). */
static i64 prom_reason(ptr p);

/* Calls a compiler-emitted per-chunk-type decoder ({i64,i64,i64}(ptr rec),
 * returned in registers, not a C aggregate) and stores its three words. */
extern void __kml_stream_decode(ptr decode, ptr rec, i64 *out);

#define F(p, off, T) (*(T *)((char *)(p) + (off)))
#define ST(p) F(p, KML_PROMISE_STATE, i64)
#define PV0(p) F(p, KML_PROMISE_V0, i64)
/* A thrown or rejected value's box; a raw Error (__kml_promise_reject_err,
 * a throw) is its object box. */
static i64 tagged_box(i64 tag, i64 pay) {
    if (tag == KML_TAG_ERROR) return pay ? (pay | 1) : KML_NB_UNDEFINED;
    return __kml_nb_pack((u8)tag, pay);
}

static i64 prom_reason(ptr p) { return tagged_box(F(p, KML_PROMISE_V1, i64), PV0(p)); }

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
typedef i64 (*fn_box)(i64 v0, i64 v1);

/* A {fn, env} closure header. */
ptr __kml_mkclo(ptr fn, ptr env) {
    clo_t *c = (clo_t *)kml_alloc(KML_HEAP_WORDS, sizeof(clo_t));
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
    char *node = (char *)kml_alloc(KML_HEAP_WORDS, KML_REACTION_SIZE);
    F(node, KML_REACTION_CLOSURE, ptr) = clo;
    F(node, KML_REACTION_NEXT, ptr) = F(p, KML_PROMISE_REACTIONS, ptr);
    F(p, KML_PROMISE_REACTIONS, ptr) = node;
}

/* Reaction closure for fn(env). */
static void react(ptr p, fn_react fn, ptr env) {
    __kml_promise_add_reaction(p, __kml_mkclo((ptr)fn, env));
}

extern void __kml_promise_adopt(ptr q, ptr h);

/* A source callback's result as Node's `async (arg) => fn(arg)` wrapper of
 * it gives it: a fresh promise, fulfilled now for a synchronous callback
 * (p null), else adopting p (NewPromiseResolveThenableJob). */
static ptr as_async(ptr p) {
    ptr a = __kml_task_alloc_promise();
    if (!p) __kml_promise_settle(a, 1);
    else __kml_promise_adopt(a, p);
    return a;
}

/* An underlying source's or sink's method result (its wrapper's return) as
 * the `async (...args) => method.call(obj, ...args)` wrapper Node makes of
 * the method gives it: fulfilled now for a synchronous return (null),
 * rejected now for a throw (the rejected promise, low bit set), else
 * adopting the promise returned. */
static ptr cb_promise(ptr p) {
    if ((size_t)p & 1) return (ptr)((size_t)p & ~(size_t)1);
    /* Bit 1: the wrapper's promise itself (a method read at run time, called
     * through that very async wrapper): reacted to as it is. */
    if ((size_t)p & 2) return (ptr)((size_t)p & ~(size_t)2);
    return as_async(p);
}

/* env {source promise, result promise}: the result settles as
 * `source.then(() => {})` would. */
static void then_void_settled(ptr env) {
    ptr src = ((ptr *)env)[0], dst = ((ptr *)env)[1];
    if (ST(src) == 2) {
        PV0(dst) = PV0(src);
        F(dst, KML_PROMISE_V1, i64) = F(src, KML_PROMISE_V1, i64);
        __kml_promise_settle(dst, 2);
        return;
    }
    __kml_promise_settle(dst, 1);
}

static ptr then_void(ptr src) {
    ptr dst = __kml_task_alloc_promise();
    ptr *env = (ptr *)kml_alloc(KML_HEAP_WORDS, 16);
    env[0] = src;
    env[1] = dst;
    __kml_promise_mark_handled(src);
    react(src, then_void_settled, env);
    return dst;
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
            *data = kml_realloc(KML_HEAP_WORDS, *data, (size_t)(nc * esz));
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
    /* A chunk in the producer's own two words boxed as an `any` (null when
     * the chunks already are), and the fulfill routine of a reader that reads
     * the chunks as `any` (__kml_rs_read_any). */
    ptr boxFn, anyFulfillFn;
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
ptr __kml_rs_alloc(double hwm, ptr ff, ptr box) {
    /* Its host header word precedes it. */
    i64 *base = (i64 *)kml_alloc(KML_HEAP_WORDS, 8 + sizeof(rstream));
    base[0] = KML_RS_HDR;
    rstream *s = (rstream *)(base + 1);
    memset(s, 0, sizeof(rstream));
    s->hwm = hwm;
    s->closedProm = __kml_task_alloc_promise();
    __kml_promise_mark_handled(s->closedProm);
    s->fulfillFn = ff;
    s->boxFn = box;
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
            s->qData = kml_realloc(KML_HEAP_WORDS, s->qData, (size_t)(nc * (i64)sizeof(rentry)));
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

/* Settle read promise prom with a chunk (or {done: true}). A promise tagged
 * with its low bit came from __kml_rs_read_any: its reader takes the chunk
 * boxed, through the reader's own routine. */
typedef void (*fn_rdreq)(ptr clo, double kind, i64 value);
typedef struct { ptr inv, clo; } rdreq;

/* A read request tagged with bit 2 is a callback, not a promise
 * (__kml_native_rs_read_request): called at once with kind 0 and the chunk
 * boxed, 1 at the end, 2 and the error. */
static void rdreq_call(ptr tagged, double kind, i64 value) {
    rdreq *r = (rdreq *)((size_t)tagged & ~(size_t)3);
    ((fn_rdreq)r->inv)(r->clo, kind, value);
}

/* A pending read rejected with err (a boxed reason). */
static void rd_reject(ptr prom, i64 err) {
    if ((size_t)prom & 2) {
        rdreq_call(prom, 2, err);
        return;
    }
    __kml_promise_reject_box((ptr)((size_t)prom & ~(size_t)1), err);
}

static void rs_fulfill(rstream *s, ptr prom, i64 v0, i64 v1, i64 done) {
    if ((size_t)prom & 2) {
        if (!done && s->boxFn) v0 = ((fn_box)s->boxFn)(v0, v1);
        rdreq_call(prom, done ? 1 : 0, done ? KML_NB_UNDEFINED : v0);
        return;
    }
    if ((size_t)prom & 1) {
        prom = (ptr)((size_t)prom & ~(size_t)1);
        if (!done && s->boxFn) {
            v0 = ((fn_box)s->boxFn)(v0, v1);
            v1 = 0;
        }
        ((fn_ff)s->anyFulfillFn)(prom, v0, v1, done);
        return;
    }
    ((fn_ff)s->fulfillFn)(prom, v0, v1, done);
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
    if (ST(p) == 2) __kml_rs_error(s, prom_reason(p));
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
    ptr p = cb_promise(((fn_env)pc->fn)(pc->env));
    ptr *env = (ptr *)kml_alloc(KML_HEAP_WORDS, 16);
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

/* env {start promise, stream}: a rejected start errors the stream
 * (ReadableStreamDefaultControllerError), a fulfilled one starts it. */
static void rs_start_settled(ptr env) {
    ptr p = ((ptr *)env)[0];
    rstream *s = (rstream *)((ptr *)env)[1];
    if (ST(p) == 2) {
        __kml_rs_error(s, prom_reason(p));
        return;
    }
    __kml_rs_started(s);
}

/* The source's start returned promise p: the stream starts (or errors)
 * when it settles, resolved through `new Promise((r) => r(p))` as Node's
 * setup does; bit 1 marks p as that promise already. */
void __kml_rs_start_promise(ptr sp, ptr p) {
    ptr *env = (ptr *)kml_alloc(KML_HEAP_WORDS, 16);
    p = ((size_t)p & 2) ? (ptr)((size_t)p & ~(size_t)2) : as_async(p);
    env[0] = p;
    env[1] = sp;
    react(p, rs_start_settled, env);
}

/* controller.enqueue's core: 1, or 0 when the stream is no longer readable
 * / close was already requested (the compile site turns 0 into a thrown
 * TypeError). A pending read is fulfilled directly (the queue is
 * necessarily empty then); otherwise the chunk is queued with its size. */
/* A runtime-made byte chunk: the Uint8Array header over n bytes at data, as
 * an enqueued array chunk's first word. */
i64 __kml_rs_chunk_hdr(ptr data, i64 n) {
    i64 *base = (i64 *)kml_alloc(KML_HEAP_WORDS, 32);
    base[0] = KML_ARRAY_HDR_WORD;
    ((ptr *)base)[1] = data;
    base[2] = n;
    base[3] = 0; /* packed */
    return (i64)(size_t)(base + 1);
}

#if defined(_WIN32)
extern int _setjmp(void *, void *) __attribute__((returns_twice));
#define KML_SETJMP(b) _setjmp((b), 0)
#else
extern int setjmp(void *) __attribute__((returns_twice));
#define KML_SETJMP(b) setjmp(b)
#endif
extern void *__kml_push_jmpbuf(void);
extern void __kml_pop_jmpbuf(void);
extern i64 __kml_get_thrown_pay(void);
extern unsigned char __kml_get_thrown_tag(void);
extern void __kml_throw_any(int tag, i64 pay) __attribute__((noreturn));
extern void __kml_throw_range_error(const char *msg) __attribute__((noreturn));
extern void __kml_dtoa(char *buf, double v);

/* A chunk's size by the strategy's size (sc): 1 for none. A size that
 * throws returns 0, one that is no finite non-negative number -1 (Node's
 * RangeError, thrown in the same region), the thrown value boxed in *err. */
static int stream_measure(clo_t *sc, i64 v0, i64 v1, double *out, i64 *err) {
    *out = 1.0;
    if (!sc) return 1;
    volatile int invalid = 0;
    void *jb = __kml_push_jmpbuf();
    if (KML_SETJMP(jb) != 0) {
        *err = tagged_box(__kml_get_thrown_tag(), __kml_get_thrown_pay());
        return invalid ? -1 : 0;
    }
    double sz = ((fn_size)sc->fn)(sc->env, v0, v1);
    if (!(sz >= 0) || sz == __builtin_inf()) {
        static const char pre[] = "The argument 'size' is invalid. Received ";
        char num[40];
        __kml_dtoa(num, sz);
        size_t n = sizeof pre - 1 + strlen(num);
        char *base = (char *)kml_alloc(KML_HEAP_RAW, n + 9);
        *(i64 *)base = (i64)n;
        memcpy(base + 8, pre, sizeof pre - 1);
        memcpy(base + 8 + sizeof pre - 1, num, strlen(num) + 1);
        invalid = 1;
        __kml_throw_range_error(base + 8);
    }
    __kml_pop_jmpbuf();
    *out = sz;
    return 1;
}

i64 __kml_rs_enqueue(ptr sp, i64 v0, i64 v1) {
    rstream *s = (rstream *)sp;
    if (s->state != 0 || (s->flags & RS_CLOSEREQ)) return 0;
    ptr prom = __kml_rs_rdpop(s);
    if (prom) {
        rs_fulfill(s, prom, v0, v1, 0);
    } else {
        double sz;
        i64 err;
        if (stream_measure((clo_t *)s->sizeClo, v0, v1, &sz, &err) != 1) {
            /* ReadableStreamDefaultControllerEnqueue: error, then rethrow. */
            __kml_rs_error(s, err);
            __kml_throw_any(__kml_nb_tag(err), __kml_nb_pay(err));
        }
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
    ptr prom;
    while ((prom = __kml_rs_rdpop(s)) != NULL) rs_fulfill(s, prom, 0, 0, 1);
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
    while ((prom = __kml_rs_rdpop(s)) != NULL) rd_reject(prom, err);
    /* The reader's closed promise is rejected handled (ReadableStreamError). */
    __kml_promise_mark_handled(s->closedProm);
    __kml_promise_reject_box(s->closedProm, err);
}

/* reader.read()'s core: the read promise. tag 1: the reader reads the
 * chunks as `any` (rs_fulfill). */
static ptr rs_read(rstream *s, size_t tag) {
    s->flags |= RS_DISTURBED;
    if (s->state == 2) {
        ptr ep = __kml_task_alloc_promise();
        __kml_promise_reject_box(ep, s->err);
        return ep;
    }
    if (s->state == 1) {
        ptr cp = __kml_task_alloc_promise();
        rs_fulfill(s, (ptr)((size_t)cp | tag), 0, 0, 1);
        return cp;
    }
    if (s->qHead < s->qLen) {
        rentry e = qpop(s);
        ptr pm = __kml_task_alloc_promise();
        rs_fulfill(s, (ptr)((size_t)pm | tag), e.v0, e.v1, 0);
        if ((s->flags & RS_CLOSEREQ) && s->qHead >= s->qLen)
            __kml_rs_finalize_close(s);
        else
            rs_pull_if_needed(s);
        return pm;
    }
    ptr pp = __kml_task_alloc_promise();
    __kml_rs_rdpush(s, (ptr)((size_t)pp | tag));
    rs_pull_if_needed(s);
    return pp;
}

ptr __kml_rs_read(ptr sp) { return rs_read((rstream *)sp, 0); }

/* A read whose request is a callback (Node's ReadableStreamAsyncIteratorReadRequest):
 * inv(clo, kind, value) runs when the chunk, the end or the error arrives,
 * at once when one already has. */
void __kml_native_rs_read_request(ptr sp, ptr inv, ptr clo) {
    rstream *s = (rstream *)sp;
    rdreq *r = (rdreq *)kml_alloc(KML_HEAP_WORDS, sizeof(rdreq));
    r->inv = inv;
    r->clo = clo;
    ptr req = (ptr)((size_t)r | 2);
    s->flags |= RS_DISTURBED;
    if (s->state == 2) {
        rd_reject(req, s->err);
        return;
    }
    if (s->state == 1) {
        rs_fulfill(s, req, 0, 0, 1);
        return;
    }
    if (s->qHead < s->qLen) {
        rentry e = qpop(s);
        rs_fulfill(s, req, e.v0, e.v1, 0);
        if ((s->flags & RS_CLOSEREQ) && s->qHead >= s->qLen)
            __kml_rs_finalize_close(s);
        else
            rs_pull_if_needed(s);
        return;
    }
    __kml_rs_rdpush(s, req);
    rs_pull_if_needed(s);
}

/* The async iterator's buffered fast path (Node's values().next()): a
 * readable stream with a chunk queued gives it at once, dequeued as read()
 * dequeues it, boxed. */
_Bool __kml_native_rs_has_queued(ptr sp) {
    rstream *s = (rstream *)sp;
    return s->state == 0 && s->qHead < s->qLen;
}

i64 __kml_native_rs_take_queued(ptr sp) {
    rstream *s = (rstream *)sp;
    s->flags |= RS_DISTURBED;
    rentry e = qpop(s);
    i64 v = s->boxFn ? ((fn_box)s->boxFn)(e.v0, e.v1) : e.v0;
    if ((s->flags & RS_CLOSEREQ) && s->qHead >= s->qLen)
        __kml_rs_finalize_close(s);
    else
        rs_pull_if_needed(s);
    return v;
}

/* read() by a reader whose chunk type is `any` (a ReadableStream<any> view
 * of a typed stream): ff is that reader's routine, the chunks go to it boxed
 * by the stream's own boxFn. */
ptr __kml_rs_read_any(ptr sp, ptr ff) {
    rstream *s = (rstream *)sp;
    s->anyFulfillFn = ff;
    return rs_read(s, 1);
}

/* The fulfill routine a pipe reading s as `any` (PIPE_SRCANY) uses. */
void __kml_rs_set_any_fulfill(ptr sp, ptr ff) { ((rstream *)sp)->anyFulfillFn = ff; }

/* controller.desiredSize: hwm - total while readable; 0 once
 * closed/errored (JS's null-when-errored is a documented caveat). */
double __kml_rs_desired(ptr sp) {
    rstream *s = (rstream *)sp;
    return s->state == 0 ? s->hwm - s->total : 0.0;
}

/* stream/reader cancel(reason): drop the queue, close, invoke the
 * underlying source's cancel callback with reason (a boxed any); the
 * returned promise is the callback's own when it yields one, else
 * already-fulfilled. */
ptr __kml_rs_cancel(ptr sp, i64 reason) {
    rstream *s = (rstream *)sp;
    s->flags |= RS_DISTURBED;
    if (s->state == 2) {
        ptr ep = __kml_task_alloc_promise();
        __kml_promise_reject_box(ep, s->err);
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
    /* The source's cancel, as its async wrapper gives it, then(() => {}). */
    clo_t *cc = (clo_t *)s->cancelClo;
    ptr p = cc ? ((fn_env_i64)cc->fn)(cc->env, reason) : NULL;
    return then_void(cb_promise(p));
}

/* The reader lock (getReader/releaseLock). */
i64 __kml_rs_lock(ptr sp) {
    rstream *s = (rstream *)sp;
    if (s->flags & RS_LOCKED) return 0;
    s->flags |= RS_LOCKED;
    return 1;
}

void __kml_rs_unlock(ptr sp) { ((rstream *)sp)->flags &= ~(i64)RS_LOCKED; }

/* A promise for a released reader's or writer's slot: p rejected with err
 * when still pending, else a fresh rejected one (marked handled). */
static ptr released_promise(ptr p, i64 err) {
    if (ST(p) != 0) p = __kml_task_alloc_promise();
    __kml_promise_mark_handled(p);
    __kml_promise_reject_box(p, err);
    return p;
}

/* reader.releaseLock() (h: the reader's {stream, closed} slots): its closed
 * rejects with closedErr, its pending reads with readErr; a readable stream
 * gets a fresh closed promise for its next reader, and is unlocked. */
void __kml_rs_release(ptr sp, ptr *h, i64 closedErr, i64 readErr) {
    rstream *s = (rstream *)sp;
    ptr old = s->closedProm;
    int pending = ST(old) == 0;
    h[1] = released_promise(old, closedErr);
    if (pending) {
        s->closedProm = __kml_task_alloc_promise();
        __kml_promise_mark_handled(s->closedProm);
    }
    ptr prom;
    while ((prom = __kml_rs_rdpop(s)) != NULL) rd_reject(prom, readErr);
    s->flags &= ~(i64)RS_LOCKED;
}

/* A reader's pending read requests (console.log's readRequests). */
i64 __kml_rs_read_requests(ptr sp) {
    rstream *s = (rstream *)sp;
    return s->rdLen - s->rdHead;
}

/* console.log's view of a readable stream: its state, plus 4 when locked. */
i64 __kml_rs_inspect_bits(ptr sp) {
    rstream *s = (rstream *)sp;
    return s->state | ((s->flags & RS_LOCKED) ? 4 : 0);
}

/* ---- WritableStream ---------------------------------------------------
 * Every write() enqueues a {chunk words, size, per-write promise} entry; a
 * drain loop dequeues one at a time, invokes the sink's write and advances
 * on its settlement via a promise reaction (sink writes never overlap).
 * Backpressure is the writer.ready promise: re-created pending when
 * desiredSize drops to 0 or below, settled fulfilled when it recovers.
 * state 0 writable · 1 closed · 2 errored · 3 erroring (an error stored,
 * the queue not yet rejected: the start or the write or close in flight
 * still to finish). flags: 1 started · 2 inFlight · 8 closeRequested ·
 * 32 locked · 64 the pending abort came while erroring. */
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
    ptr abortProm;  /* the pending abort request's promise, or null */
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
#define WS_ABORT_WASERR 64

void __kml_ws_error(ptr s, i64 err);
void __kml_ws_advance(ptr s);
static void ws_finish_erroring(wstream *s);

/* A fresh writable stream: ready starts fulfilled (no backpressure while
 * the queue is empty), closed starts pending; both marked handled. */
ptr __kml_ws_alloc(double hwm) {
    /* Its host header word precedes it. */
    i64 *base = (i64 *)kml_alloc(KML_HEAP_WORDS, 8 + sizeof(wstream));
    base[0] = KML_WS_HDR;
    wstream *s = (wstream *)(base + 1);
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
    /* WritableStreamFinishInFlightClose: an erroring stream closes after
     * all, its pending abort fulfilled. */
    if (s->abortProm) {
        __kml_promise_settle(s->abortProm, 1);
        s->abortProm = NULL;
    }
    s->state = 1;
    s->err = KML_NB_UNDEFINED;
    s->flags &= ~(i64)WS_INFLIGHT;
    __kml_promise_settle(s->closedProm, 1);
    if (s->closeProm) __kml_promise_settle(s->closeProm, 1);
}

/* env {write promise, stream, per-write promise}. */
void __kml_ws_write_settled(ptr env) {
    ptr wp = ((ptr *)env)[0];
    wstream *s = (wstream *)((ptr *)env)[1];
    ptr wprom = ((ptr *)env)[2];
    s->flags &= ~(i64)WS_INFLIGHT;
    if (ST(wp) == 2) {
        i64 err = prom_reason(wp);
        __kml_promise_reject_box(wprom, err);
        __kml_ws_error(s, err);
        return;
    }
    __kml_promise_settle(wprom, 1);
    __kml_ws_advance(s);
}

/* env {close promise, stream}. */
void __kml_ws_close_settled(ptr env) {
    ptr cp = ((ptr *)env)[0];
    ptr s = ((ptr *)env)[1];
    if (ST(cp) == 2) {
        /* WritableStreamFinishInFlightCloseWithError. */
        wstream *w = (wstream *)s;
        i64 err = prom_reason(cp);
        w->flags &= ~(i64)WS_INFLIGHT;
        if (w->closeProm) {
            __kml_promise_reject_box(w->closeProm, err);
            w->closeProm = NULL;
        }
        if (w->abortProm) {
            __kml_promise_reject_box(w->abortProm, err);
            w->abortProm = NULL;
        }
        __kml_ws_error(s, err);
    } else
        __kml_ws_finish_close(s);
}

/* The drain loop: while writable, started and no write in flight, dequeue
 * one entry and run the sink's write; when the queue empties with close
 * requested, run the sink's close. */
void __kml_ws_advance(ptr sp) {
    wstream *s = (wstream *)sp;
    for (;;) {
        i64 f = s->flags;
        if (!(f & WS_STARTED) || (f & WS_INFLIGHT)) return;
        if (s->state == 3) {
            ws_finish_erroring(s);
            return;
        }
        if (s->state != 0) return;
        if (s->qHead < s->qLen) {
            wentry e = ((wentry *)s->qData)[s->qHead];
            if (++s->qHead >= s->qLen) s->qHead = s->qLen = 0;
            s->total -= e.size;
            __kml_ws_update_ready(s);
            s->flags = f | WS_INFLIGHT;
            clo_t *wc = (clo_t *)s->writeClo;
            ptr wp = cb_promise(wc ? ((fn_env_2i64)wc->fn)(wc->env, e.v0, e.v1) : NULL);
            ptr *env = (ptr *)kml_alloc(KML_HEAP_WORDS, 24);
            env[0] = wp;
            env[1] = s;
            env[2] = e.prom;
            react(wp, __kml_ws_write_settled, env);
            return;
        }
        if (!(f & WS_CLOSEREQ)) return;
        s->flags = f | WS_INFLIGHT;
        clo_t *cc = (clo_t *)s->closeClo;
        ptr cp2 = cb_promise(cc ? ((fn_env)cc->fn)(cc->env) : NULL);
        ptr *env2 = (ptr *)kml_alloc(KML_HEAP_WORDS, 16);
        env2[0] = cp2;
        env2[1] = s;
        react(cp2, __kml_ws_close_settled, env2);
        return;
    }
}

/* Move to errored: reject every queued write promise, the ready and closed
 * promises, and any pending close() promise. */
/* WritableStreamRejectCloseAndClosedPromiseIfNeeded. */
static void ws_reject_close_and_closed(wstream *s) {
    if (s->closeProm && ST(s->closeProm) == 0) {
        __kml_promise_reject_box(s->closeProm, s->err);
    }
    __kml_promise_reject_box(s->closedProm, s->err);
}

/* env {abort algorithm's promise, stream, abort request's promise}. */
static void ws_abort_settled(ptr env) {
    ptr p = ((ptr *)env)[0];
    wstream *s = (wstream *)((ptr *)env)[1];
    ptr ap = ((ptr *)env)[2];
    if (ST(p) == 2)
        __kml_promise_reject_box(ap, prom_reason(p));
    else
        __kml_promise_settle(ap, 1);
    ws_reject_close_and_closed(s);
}

/* WritableStreamFinishErroring: the queue rejected with the stored error,
 * then the pending abort's algorithm run (the sink's abort). */
static void ws_finish_erroring(wstream *s) {
    s->state = 2;
    wentry *d = (wentry *)s->qData;
    while (s->qHead < s->qLen) {
        __kml_promise_reject_box(d[s->qHead].prom, s->err);
        s->qHead++;
    }
    s->qHead = s->qLen = 0;
    s->total = 0.0;
    ptr ap = s->abortProm;
    if (!ap) {
        ws_reject_close_and_closed(s);
        return;
    }
    s->abortProm = NULL;
    if (s->flags & WS_ABORT_WASERR) {
        __kml_promise_reject_box(ap, s->err);
        ws_reject_close_and_closed(s);
        return;
    }
    clo_t *ac = (clo_t *)s->abortClo;
    ptr p = cb_promise(ac ? ((fn_env_i64)ac->fn)(ac->env, s->err) : NULL);
    ptr *env = (ptr *)kml_alloc(KML_HEAP_WORDS, 24);
    env[0] = p;
    env[1] = s;
    env[2] = ap;
    react(p, ws_abort_settled, env);
}

/* WritableStreamStartErroring: the error stored, writer.ready rejected,
 * and the stream errored at once when nothing is in flight. */
static void ws_start_erroring(wstream *s, i64 err) {
    s->state = 3;
    s->err = err;
    if (ST(s->readyProm) != 0) {
        s->readyProm = __kml_task_alloc_promise();
    }
    __kml_promise_mark_handled(s->readyProm);
    __kml_promise_reject_box(s->readyProm, err);
    if ((s->flags & WS_STARTED) && !(s->flags & WS_INFLIGHT)) ws_finish_erroring(s);
}

/* WritableStreamDealWithRejection (a sink or controller error): a
 * writable stream starts erroring, an erroring one finishes. */
void __kml_ws_error(ptr sp, i64 err) {
    wstream *s = (wstream *)sp;
    if (s->state == 0)
        ws_start_erroring(s, err);
    else if (s->state == 3 && !(s->flags & WS_INFLIGHT))
        ws_finish_erroring(s);
}

/* The per-write promise (null when the stream is closed / closing). */
ptr __kml_ws_write(ptr sp, i64 v0, i64 v1) {
    wstream *s = (wstream *)sp;
    ptr prom = __kml_task_alloc_promise();
    if (s->state == 2 || s->state == 3) {
        __kml_promise_reject_box(prom, s->err);
        return prom;
    }
    if (s->state == 1 || (s->flags & WS_CLOSEREQ)) return NULL;
    double sz;
    i64 err;
    int m = stream_measure((clo_t *)s->sizeClo, v0, v1, &sz, &err);
    if (m == 0) {
        /* GetChunkSize's ErrorIfNeeded; the write then rejects with the
         * stream's stored error at once. */
        if (s->state == 0) ws_start_erroring(s, err);
        __kml_promise_reject_box(prom, s->err);
        return prom;
    }
    if (m < 0) {
        /* EnqueueValueWithSize refused the size: the write request stays,
         * rejected when the stream finishes erroring. */
        __kml_ws_qpush(s, v0, v1, 0, prom);
        if (s->state == 0) ws_start_erroring(s, err);
        return prom;
    }
    __kml_ws_qpush(s, v0, v1, sz, prom);
    __kml_ws_advance(s);
    return prom;
}

/* The close promise (null when close is not allowed). */
ptr __kml_ws_close(ptr sp) {
    wstream *s = (wstream *)sp;
    if (s->state == 2) {
        ptr p0 = __kml_task_alloc_promise();
        __kml_promise_reject_box(p0, s->err);
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
    if (s->state == 1 || s->state == 2) {
        ptr ap = __kml_task_alloc_promise();
        __kml_promise_settle(ap, 1);
        return ap;
    }
    if (s->abortProm) return s->abortProm;
    int was = s->state == 3;
    ptr ap = __kml_task_alloc_promise();
    s->abortProm = ap;
    if (was) {
        s->flags |= WS_ABORT_WASERR;
    } else {
        s->flags &= ~(i64)WS_ABORT_WASERR;
        ws_start_erroring(s, reason);
    }
    return ap;
}

double __kml_ws_desired(ptr sp) {
    wstream *s = (wstream *)sp;
    return s->state == 0 ? s->hwm - s->total : 0.0;
}

/* env {start promise, stream}: a rejected start errors the stream
 * (WritableStreamDealWithRejection), then it has started either way. */
static void ws_start_settled(ptr env) {
    ptr p = ((ptr *)env)[0];
    wstream *s = (wstream *)((ptr *)env)[1];
    s->flags |= WS_STARTED;
    if (ST(p) == 2) {
        __kml_ws_error(s, prom_reason(p));
        return;
    }
    __kml_ws_advance(s);
}

/* The sink's start returned promise p: the stream starts when it settles,
 * resolved through `new Promise((r) => r(p))` as Node's setup does. */
void __kml_ws_start_promise(ptr sp, ptr p) {
    ptr *env = (ptr *)kml_alloc(KML_HEAP_WORDS, 16);
    /* Bit 1: p is that `new Promise((r) => r(result))` already. */
    p = ((size_t)p & 2) ? (ptr)((size_t)p & ~(size_t)2) : as_async(p);
    env[0] = p;
    env[1] = sp;
    react(p, ws_start_settled, env);
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

/* writer.releaseLock() (h: the writer's {stream, closed, ready} slots): its
 * ready and closed reject with err; the stream keeps a fresh promise in place
 * of each one that was pending, for its next writer, and is unlocked. */
void __kml_ws_release(ptr sp, ptr *h, i64 err) {
    wstream *s = (wstream *)sp;
    ptr rdy = s->readyProm, cl = s->closedProm;
    int rdyPending = ST(rdy) == 0, clPending = ST(cl) == 0;
    h[2] = released_promise(rdy, err);
    h[1] = released_promise(cl, err);
    if (rdyPending) {
        s->readyProm = __kml_task_alloc_promise();
        __kml_promise_mark_handled(s->readyProm);
        if (s->state == 0 && s->hwm > s->total) __kml_promise_settle(s->readyProm, 1);
    }
    if (clPending) {
        s->closedProm = __kml_task_alloc_promise();
        __kml_promise_mark_handled(s->closedProm);
    }
    s->flags &= ~(i64)WS_LOCKED;
}

/* console.log's view of a writable stream: its state, plus 4 when locked. */
i64 __kml_ws_inspect_bits(ptr sp) {
    wstream *s = (wstream *)sp;
    return s->state | ((s->flags & WS_LOCKED) ? 4 : 0);
}

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
#define PIPE_SRCANY 8 /* the source viewed as ReadableStream<any>: read boxed */

static void pipe_step(pipectx *c);
static void pipe_on_read(ptr env);
static void pipe_on_ready(ptr env);
static void pipe_on_written(ptr env);
static void pipe_on_closed(ptr env);

ptr __kml_pipe_to(ptr src, ptr dst, ptr decode, i64 flags, ptr sigA, ptr sigR) {
    pipectx *c = (pipectx *)kml_alloc(KML_HEAP_WORDS, sizeof(pipectx));
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

/* #reason is a boxed any: the source, the sink and the pipe's promise
 * get the value itself. */
static void pipe_signal_abort(pipectx *c) {
    i64 box = c->sigR ? *c->sigR : KML_NB_UNDEFINED;
    if (!(c->flags & PIPE_NOCANCEL)) __kml_rs_cancel(c->src, box);
    if (!(c->flags & PIPE_NOABORT)) __kml_ws_abort(c->dst, box);
    __kml_promise_reject_box(c->prom, box);
}

static void pipe_step(pipectx *c) {
    if (c->sigA && *c->sigA) {
        pipe_signal_abort(c);
        return;
    }
    c->cur = (c->flags & PIPE_SRCANY) ? rs_read(c->src, 1) : __kml_rs_read(c->src);
    react(c->cur, pipe_on_read, c);
}

static void pipe_dest_error(pipectx *c, i64 err) {
    if (!(c->flags & PIPE_NOCANCEL)) __kml_rs_cancel(c->src, err);
    __kml_promise_reject_box(c->prom, err);
}

static void pipe_on_read(ptr env) {
    pipectx *c = (pipectx *)env;
    ptr p = c->cur;
    if (ST(p) == 2) {
        i64 err = prom_reason(p);
        if (!(c->flags & PIPE_NOABORT)) __kml_ws_abort(c->dst, err);
        __kml_promise_reject_box(c->prom, err);
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
        pipe_dest_error(c, prom_reason(c->cur));
        return;
    }
    pipe_step(c);
}

static void pipe_on_closed(ptr env) {
    pipectx *c = (pipectx *)env;
    if (ST(c->cur) == 2)
        __kml_promise_reject_box(c->prom, prom_reason(c->cur));
    else
        __kml_promise_settle(c->prom, 1);
}

/* ---- tee(): two branches share one source read at a time. ------------ */
typedef struct {
    rstream *src, *b1, *b2;
    ptr decode;
    i64 reading, canceled1, canceled2, reason1, reason2;
    ptr cancelProm; /* the promise both branches' cancel() wait on */
} teectx;

/* Settle the branches' cancel promise when the source ends first. */
static void tee_cancel_resolve(teectx *c) {
    if (c->cancelProm && !(c->canceled1 & c->canceled2) && ST(c->cancelProm) == 0)
        __kml_promise_settle(c->cancelProm, 1);
}
_Static_assert(sizeof(teectx) == KML_TEE_SIZE, "tee layout");

/* env {read promise, tee ctx}. */
static void tee_on_read(ptr env) {
    ptr p = ((ptr *)env)[0];
    teectx *c = (teectx *)((ptr *)env)[1];
    c->reading = 0;
    if (ST(p) == 2) {
        i64 err = prom_reason(p);
        __kml_rs_error(c->b1, err);
        __kml_rs_error(c->b2, err);
        tee_cancel_resolve(c);
        return;
    }
    i64 dv[3];
    __kml_stream_decode(c->decode, (ptr)PV0(p), dv);
    int alive1 = c->canceled1 == 0, alive2 = c->canceled2 == 0;
    if (dv[2] != 0) {
        if (alive1) __kml_rs_close(c->b1);
        if (alive2) __kml_rs_close(c->b2);
        tee_cancel_resolve(c);
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
    ptr *env = (ptr *)kml_alloc(KML_HEAP_WORDS, 16);
    env[0] = p;
    env[1] = c;
    react(p, tee_on_read, env);
}

ptr __kml_tee_pull(ptr ctx) {
    __kml_tee_pullhook(ctx);
    return NULL;
}

extern void *__kml_dynarr_new(i64 cap);
extern void __kml_dynarr_push(void *arr, i64 v);

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
    /* Each branch's cancel waits on one promise, settled by the source's
     * cancel with [reason1, reason2] once both branches are cancelled. */
    if (!c->cancelProm) c->cancelProm = __kml_task_alloc_promise();
    if (c->canceled1 & c->canceled2) {
        void *both = __kml_dynarr_new(2);
        __kml_dynarr_push(both, c->reason1);
        __kml_dynarr_push(both, c->reason2);
        __kml_promise_adopt(c->cancelProm, __kml_rs_cancel(c->src, (i64)both | 6));
    }
    return c->cancelProm;
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

/* A transformer callback's result with its wrapper's tag bits cleared: the
 * promise (a throw's rejected one, bit 0; the async wrapper's own, bit 1),
 * or null for a synchronous return. */
static ptr ts_promise(ptr p) { return (ptr)((size_t)p & ~(size_t)3); }

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
        __kml_promise_reject_box(tgt, prom_reason(p));
    else
        __kml_promise_settle(tgt, 1);
}

ptr __kml_ts_pull(ptr cp) {
    tsctx *c = (tsctx *)cp;
    if (!c->parked) return NULL;
    c->parked = 0;
    ptr wprom = c->parkedProm;
    ptr p = ts_promise(__kml_ts_run_transform(c, c->pv0, c->pv1));
    if (!p) {
        __kml_promise_settle(wprom, 1);
        return NULL;
    }
    ptr *env = (ptr *)kml_alloc(KML_HEAP_WORDS, 16);
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
        i64 err = prom_reason(p);
        __kml_rs_error(c->readable, err);
        __kml_promise_reject_box(c->closeProm, err);
    } else {
        __kml_rs_close(c->readable);
        __kml_promise_settle(c->closeProm, 1);
    }
}

ptr __kml_ts_sink_close(ptr cp) {
    tsctx *c = (tsctx *)cp;
    clo_t *fc = (clo_t *)c->flushClo;
    ptr p = fc ? ts_promise(((fn_env)fc->fn)(fc->env)) : NULL;
    if (!p) {
        __kml_rs_close(c->readable);
        return NULL;
    }
    c->closeProm = __kml_task_alloc_promise();
    ptr *env = (ptr *)kml_alloc(KML_HEAP_WORDS, 16);
    env[0] = p;
    env[1] = c;
    react(p, __kml_ts_flush_done, env);
    return c->closeProm;
}

/* controller.terminate(): the readable side closes (when it still can),
 * and a TransformStream's writable side errors with err, as Node's
 * TransformStreamDefaultControllerTerminate does. */
void __kml_rs_terminate(ptr sp, i64 err) {
    rstream *s = (rstream *)sp;
    __kml_rs_close(s);
    clo_t *pc = (clo_t *)s->pullClo;
    if (pc && pc->fn == (void *)__kml_ts_pull) __kml_ws_error(((tsctx *)pc->env)->writable, err);
}

ptr __kml_ts_sink_abort(ptr cp, i64 reason) {
    __kml_rs_error(((tsctx *)cp)->readable, reason);
    return NULL;
}
