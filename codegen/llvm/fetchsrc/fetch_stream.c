/* fetch_stream.c — `Response.body` as a ReadableStream<Uint8Array> fed
 * straight from libcurl's write callback (TDD-00097 Stage 4; in C per
 * TDD-00240). Appended to fetch.c's unit (its types and externs) when the
 * program touches `.body`; without it the generated module defines no-op
 * hooks and the fully-buffered behavior is unchanged.
 *
 *   __kml_fetch_body_write(pending, chunk, total) -> 0 buffer, 1 consumed,
 *     2 pause (CURL_WRITEFUNC_PAUSE: the chunk is redelivered on unpause,
 *     chosen when the stream's queue is at its high-water mark with no read
 *     pending, so the queue stays bounded)
 *   __kml_fetch_body_on_done(pending) closes (or errors, on a transfer-level
 *     failure) the activated stream when the transfer completes
 * The stream's pull closure is __kml_fetch_body_pull: clear the paused flag
 * and curl_easy_pause(CONT) so the transfer resumes. KML_RS_* are the stream
 * struct's field offsets (FetchCFlags). */
#ifdef KML_FETCH_STREAM
extern int curl_easy_pause(void *, int);
extern double __kml_rs_desired(void *s);
extern i64 __kml_rs_enqueue(void *s, i64 v0, i64 v1);
extern i64 __kml_rs_close(void *s);
extern void __kml_rs_error(void *s, i64 err);
extern _Bool __kml_fetch_pump(void);

i64 __kml_fetch_body_write(void *pending, const char *chunk, i64 total) {
    pend_t *p = (pend_t *)pending;
    void *bs = p->bstream;
    if (!bs) return 0;
    /* Not readable any more: drop the chunk. */
    if (F(bs, KML_RS_STATE, i64) != 0) return 1;
    _Bool room = __kml_rs_desired(bs) > 0.0;
    _Bool read_pending = F(bs, KML_RS_RH, i64) < F(bs, KML_RS_RL, i64);
    if (!(room || read_pending)) {
        p->paused = 1;
        return 2;
    }
    void *copy = malloc((size_t)total);
    memcpy(copy, chunk, (size_t)total);
    __kml_rs_enqueue(bs, (i64)(intptr_t)copy, total);
    return 1;
}

void __kml_fetch_body_on_done(void *pending) {
    pend_t *p = (pend_t *)pending;
    void *bs = p->bstream;
    if (!bs) return;
    if (p->result != 0)
        __kml_rs_error(bs, (i64)(intptr_t)fetch_error(p->result));
    else
        __kml_rs_close(bs);
}

/* An aborted fetch errors its body stream with the abort reason. */
void __kml_fetch_body_abort(void *pending, void *err) {
    void *bs = ((pend_t *)pending)->bstream;
    if (bs) __kml_rs_error(bs, (i64)(intptr_t)err);
}

void *__kml_fetch_body_pull(void *pending) {
    pend_t *p = (pend_t *)pending;
    if (p->paused) {
        p->paused = 0;
        if (!p->done) {
            curl_easy_pause(p->easy, 0);
            __kml_fetch_pump();
        }
    }
    return 0;
}

/* Combinator-built / already-finished Responses arrive with pending == null:
 * the buffered body replays as one chunk and closes. Otherwise the stream
 * activates on the pending fetch (once): whatever arrived before activation
 * flushes as the first chunk, and an already-finished transfer closes now. */
void *__kml_fetch_body_stream(void *pending, void *s, void *bodyPtr, i64 bodyLen) {
    pend_t *p = (pend_t *)pending;
    if (!p) {
        if (bodyLen > 0) __kml_rs_enqueue(s, (i64)(intptr_t)bodyPtr, bodyLen);
        __kml_rs_close(s);
        return s;
    }
    if (p->bstream) return p->bstream;
    p->bstream = s;
    if (p->buf->len > 0) __kml_rs_enqueue(s, (i64)(intptr_t)p->buf->data, p->buf->len);
    if (p->done) __kml_fetch_body_on_done(p);
    return s;
}
#endif /* KML_FETCH_STREAM */
