/* settle.c — settling a bare promise: the first settle wins, the reasons a
 * rejection carries, and the microtask that adopts a thenable (TDD-00087,
 * TDD-00091, TDD-00207; in C per TDD-00240). kml_layout.h is prepended by the
 * compiler. */
typedef long long i64;
typedef unsigned char u8;

#define F(p, off, T) (*(T *)((char *)(p) + (off)))
#define P_STATE(p) F(p, KML_PROMISE_STATE, i64)
#define P_V0(p) F(p, KML_PROMISE_V0, i64)
#define P_V1(p) F(p, KML_PROMISE_V1, i64)

extern void __kml_promise_note_rejected(void *);
extern void __kml_promise_drain_reactions(void *);
extern u8 __kml_nb_tag(i64);
extern i64 __kml_nb_pay(i64);

/* Settle p (state 1 fulfilled / 2 rejected; the value is already in v0/v1):
 * wake a parked awaiter and enqueue the reactions. A later settle is a no-op. */
void __kml_promise_settle(void *p, i64 state) {
    if (P_STATE(p) != 0) return;
    P_STATE(p) = state;
    if (state == 2) __kml_promise_note_rejected(p);
    void *w = F(p, KML_PROMISE_WAITER, void *);
    if (w) F(w, KML_TASK_PENDING_PROMISE, void *) = 0;
    __kml_promise_drain_reactions(p);
}

/* The one way anything rejects a promise: both reason slots (the payload and
 * its tag) and the settle. The others are its forms. */
void __kml_promise_reject_with(void *p, i64 tag, i64 pay) {
    P_V0(p) = pay;
    P_V1(p) = tag;
    __kml_promise_settle(p, 2);
}

/* Reject p with an error object (err is its pointer bits, 0 for none). */
void __kml_promise_reject_err(void *p, i64 err) {
    __kml_promise_reject_with(p, err == 0 ? KML_TAG_UNDEFINED : KML_TAG_ERROR, err);
}

/* Reject p with src's reason (a rejection passing through). */
void __kml_promise_reject_from(void *p, void *src) {
    __kml_promise_reject_with(p, P_V1(src), P_V0(src));
}

/* Reject p with a boxed reason (an AbortSignal's): its tag and payload. */
void __kml_promise_reject_box(void *p, i64 w) {
    __kml_promise_reject_with(p, (i64)__kml_nb_tag(w), __kml_nb_pay(w));
}

/* The microtask of thenable adoption; env is {src, tgt}: tgt takes src's
 * settlement unless a resolve/reject already settled it. */
void __kml_promise_adopt_runner(void *env) {
    void *src = ((void **)env)[0];
    void *tgt = ((void **)env)[1];
    if (P_STATE(tgt) != 0) return;
    P_V0(tgt) = P_V0(src);
    P_V1(tgt) = P_V1(src);
    __kml_promise_settle(tgt, P_STATE(src));
}
