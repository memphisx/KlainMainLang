// h2node.c — the nghttp2 session natives behind lib/node/http2.ts, Node's
// node_http2.cc in C: one nghttp2 session per Http2Session, fed the bytes its
// socket reads and drained of the bytes to write. Nothing here does I/O.
//
// nghttp2 reports frames through callbacks. They queue events (headers, data,
// a stream's close, settings, ping, goaway …) that the session's TypeScript
// side takes one at a time after each feed, as Node's binding calls back into
// JavaScript. A stream's outgoing body is a chunk queue its data provider
// reads, deferring while the queue is empty and the writable side is open.

#include <nghttp2/nghttp2.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

extern void __kml_native_set_last_string(const char *s);

enum {
	H2E_NONE = 0,
	H2E_HEADERS = 1,      // a: stream, b: category, c: flags; headers in text
	H2E_DATA = 2,         // a: stream; bytes
	H2E_STREAM_END = 3,   // a: stream (END_STREAM on DATA)
	H2E_STREAM_CLOSE = 4, // a: stream, b: error code
	H2E_SETTINGS = 5,     // b: 1 for an ACK
	H2E_PING = 6,         // b: 1 for an ACK; bytes: the 8-byte payload
	H2E_GOAWAY = 7,       // a: last stream, b: error code; bytes: opaque data
	H2E_PUSH = 8,         // a: stream, b: promised stream; headers in text
	H2E_FRAME_ERROR = 9,  // a: stream, b: frame type, c: nghttp2 error
	H2E_WANT_TRAILERS = 10, // a: stream
	H2E_PRIORITY = 11,    // a: stream
	H2E_ALTSVC = 12,
};

typedef struct h2ev {
	int type;
	int32_t a;
	int64_t b, c;
	char *text;        // headers: name '\n' value '\n' …
	uint8_t *bytes;
	size_t len;
	struct h2ev *next;
} h2ev;

typedef struct h2chunk {
	uint8_t *p;
	size_t len, off;
	struct h2chunk *next;
} h2chunk;

typedef struct h2stream {
	int32_t id;
	struct h2stream *pnext; // the session's list of submitted streams
	h2chunk *head, *tail;
	int eof;            // the writable side ended
	int want_trailers;  // end without END_STREAM, then the trailers
	char *hbuf;         // headers being received
	size_t hlen, hcap;
} h2stream;

typedef struct {
	nghttp2_session *s;
	h2ev *qh, *qt;
	h2ev *cur;          // the event last taken
	uint8_t *out;       // bytes to write
	size_t outlen, outcap;
	int32_t lastid;
	h2stream *pend;     // streams submitted here: nghttp2 knows their user
	                    // data only once their HEADERS frame is sent
} h2sess;

#define H2_MAX 4096
static h2sess *h2tab[H2_MAX];

static h2sess *sess_of(double id) {
	long i = (long)id;
	if (i < 1 || i >= H2_MAX) return NULL;
	return h2tab[i];
}

static void push_ev(h2sess *h, h2ev *e) {
	e->next = NULL;
	if (h->qt) h->qt->next = e; else h->qh = e;
	h->qt = e;
}

static h2ev *new_ev(int type, int32_t a, int64_t b, int64_t c) {
	h2ev *e = calloc(1, sizeof(h2ev));
	e->type = type; e->a = a; e->b = b; e->c = c;
	return e;
}

static void free_ev(h2ev *e) {
	if (!e) return;
	free(e->text);
	free(e->bytes);
	free(e);
}

static h2stream *stream_of(h2sess *h, int32_t sid) {
	h2stream *st = (h2stream *)nghttp2_session_get_stream_user_data(h->s, sid);
	if (st) return st;
	for (st = h->pend; st; st = st->pnext)
		if (st->id == sid) return st;
	return NULL;
}

static void pend_add(h2sess *h, h2stream *st) {
	st->pnext = h->pend;
	h->pend = st;
}

static void pend_remove(h2sess *h, h2stream *st) {
	for (h2stream **p = &h->pend; *p; p = &(*p)->pnext) {
		if (*p == st) {
			*p = st->pnext;
			return;
		}
	}
}

static h2stream *stream_new(h2sess *h, int32_t sid) {
	h2stream *st = calloc(1, sizeof(h2stream));
	st->id = sid;
	nghttp2_session_set_stream_user_data(h->s, sid, st);
	return st;
}

static void stream_free(h2stream *st) {
	if (!st) return;
	h2chunk *c = st->head;
	while (c) { h2chunk *n = c->next; free(c->p); free(c); c = n; }
	free(st->hbuf);
	free(st);
}

static void hbuf_add(h2stream *st, const uint8_t *p, size_t n) {
	if (st->hlen + n + 1 > st->hcap) {
		size_t cap = st->hcap ? st->hcap * 2 : 256;
		while (cap < st->hlen + n + 1) cap *= 2;
		st->hbuf = realloc(st->hbuf, cap);
		st->hcap = cap;
	}
	memcpy(st->hbuf + st->hlen, p, n);
	st->hlen += n;
	st->hbuf[st->hlen] = 0;
}

static char *hbuf_take(h2stream *st) {
	char *t = st->hbuf ? st->hbuf : calloc(1, 1);
	st->hbuf = NULL; st->hlen = st->hcap = 0;
	return t;
}

// ---- nghttp2 callbacks ----

static int on_begin_headers(nghttp2_session *s, const nghttp2_frame *f, void *ud) {
	h2sess *h = ud;
	(void)s;
	int32_t sid = f->hd.type == NGHTTP2_PUSH_PROMISE ? f->push_promise.promised_stream_id : f->hd.stream_id;
	if (!stream_of(h, sid)) stream_new(h, sid);
	return 0;
}

static int on_header(nghttp2_session *s, const nghttp2_frame *f, const uint8_t *name, size_t namelen,
                     const uint8_t *value, size_t valuelen, uint8_t flags, void *ud) {
	h2sess *h = ud;
	(void)s; (void)flags;
	int32_t sid = f->hd.type == NGHTTP2_PUSH_PROMISE ? f->push_promise.promised_stream_id : f->hd.stream_id;
	h2stream *st = stream_of(h, sid);
	if (!st) st = stream_new(h, sid);
	hbuf_add(st, name, namelen);
	hbuf_add(st, (const uint8_t *)"\n", 1);
	hbuf_add(st, value, valuelen);
	hbuf_add(st, (const uint8_t *)"\n", 1);
	return 0;
}

static int on_frame_recv(nghttp2_session *s, const nghttp2_frame *f, void *ud) {
	h2sess *h = ud;
	(void)s;
	int32_t sid = f->hd.stream_id;
	switch (f->hd.type) {
	case NGHTTP2_HEADERS: {
		h2stream *st = stream_of(h, sid);
		h2ev *e = new_ev(H2E_HEADERS, sid, f->headers.cat, f->hd.flags);
		e->text = st ? hbuf_take(st) : calloc(1, 1);
		push_ev(h, e);
		break;
	}
	case NGHTTP2_DATA:
		if (f->hd.flags & NGHTTP2_FLAG_END_STREAM) push_ev(h, new_ev(H2E_STREAM_END, sid, 0, 0));
		break;
	case NGHTTP2_SETTINGS:
		push_ev(h, new_ev(H2E_SETTINGS, 0, (f->hd.flags & NGHTTP2_FLAG_ACK) ? 1 : 0, 0));
		break;
	case NGHTTP2_PING: {
		h2ev *e = new_ev(H2E_PING, 0, (f->hd.flags & NGHTTP2_FLAG_ACK) ? 1 : 0, 0);
		e->bytes = malloc(8);
		memcpy(e->bytes, f->ping.opaque_data, 8);
		e->len = 8;
		push_ev(h, e);
		break;
	}
	case NGHTTP2_GOAWAY: {
		h2ev *e = new_ev(H2E_GOAWAY, f->goaway.last_stream_id, f->goaway.error_code, 0);
		if (f->goaway.opaque_data_len > 0) {
			e->bytes = malloc(f->goaway.opaque_data_len);
			memcpy(e->bytes, f->goaway.opaque_data, f->goaway.opaque_data_len);
			e->len = f->goaway.opaque_data_len;
		}
		push_ev(h, e);
		break;
	}
	case NGHTTP2_PUSH_PROMISE: {
		int32_t pid = f->push_promise.promised_stream_id;
		h2stream *st = stream_of(h, pid);
		h2ev *e = new_ev(H2E_PUSH, sid, pid, 0);
		e->text = st ? hbuf_take(st) : calloc(1, 1);
		push_ev(h, e);
		break;
	}
	case NGHTTP2_PRIORITY:
		push_ev(h, new_ev(H2E_PRIORITY, sid, 0, 0));
		break;
	}
	return 0;
}

static int on_data_chunk(nghttp2_session *s, uint8_t flags, int32_t sid, const uint8_t *data, size_t len, void *ud) {
	h2sess *h = ud;
	(void)s; (void)flags;
	h2ev *e = new_ev(H2E_DATA, sid, 0, 0);
	e->bytes = malloc(len ? len : 1);
	memcpy(e->bytes, data, len);
	e->len = len;
	push_ev(h, e);
	return 0;
}

static int on_stream_close(nghttp2_session *s, int32_t sid, uint32_t code, void *ud) {
	h2sess *h = ud;
	h2stream *st = stream_of(h, sid);
	if (st) pend_remove(h, st);
	stream_free(st);
	nghttp2_session_set_stream_user_data(s, sid, NULL);
	push_ev(h, new_ev(H2E_STREAM_CLOSE, sid, code, 0));
	return 0;
}

static int on_frame_not_send(nghttp2_session *s, const nghttp2_frame *f, int lib_error, void *ud) {
	h2sess *h = ud;
	(void)s;
	push_ev(h, new_ev(H2E_FRAME_ERROR, f->hd.stream_id, f->hd.type, lib_error));
	return 0;
}

static ssize_t read_data(nghttp2_session *s, int32_t sid, uint8_t *buf, size_t length, uint32_t *flags,
                         nghttp2_data_source *src, void *ud) {
	h2sess *h = ud;
	(void)s; (void)src;
	h2stream *st = stream_of(h, sid);
	if (!st) {
		*flags |= NGHTTP2_DATA_FLAG_EOF;
		return 0;
	}
	size_t n = 0;
	while (st->head && n < length) {
		h2chunk *c = st->head;
		size_t take = c->len - c->off;
		if (take > length - n) take = length - n;
		memcpy(buf + n, c->p + c->off, take);
		n += take;
		c->off += take;
		if (c->off == c->len) {
			st->head = c->next;
			if (!st->head) st->tail = NULL;
			free(c->p);
			free(c);
		}
	}
	if (!st->head && st->eof) {
		*flags |= NGHTTP2_DATA_FLAG_EOF;
		if (st->want_trailers) {
			*flags |= NGHTTP2_DATA_FLAG_NO_END_STREAM;
			push_ev(h, new_ev(H2E_WANT_TRAILERS, sid, 0, 0));
		}
		return (ssize_t)n;
	}
	if (n == 0) return NGHTTP2_ERR_DEFERRED;
	return (ssize_t)n;
}

// ---- headers: "name\nvalue\n…" → nghttp2_nv[] ----

static nghttp2_nv *parse_nv(const char *text, size_t *count, char **store) {
	size_t n = 0;
	for (const char *p = text; *p; p++) if (*p == '\n') n++;
	n /= 2;
	char *buf = strdup(text);
	nghttp2_nv *nv = calloc(n ? n : 1, sizeof(nghttp2_nv));
	char *p = buf;
	for (size_t i = 0; i < n; i++) {
		char *name = p;
		char *nl = strchr(p, '\n');
		*nl = 0;
		char *value = nl + 1;
		char *nl2 = strchr(value, '\n');
		*nl2 = 0;
		p = nl2 + 1;
		nv[i].name = (uint8_t *)name;
		nv[i].namelen = strlen(name);
		nv[i].value = (uint8_t *)value;
		nv[i].valuelen = strlen(value);
		// A name ending in '\x01' is a sensitive header (never indexed).
		nv[i].flags = NGHTTP2_NV_FLAG_NONE;
		if (nv[i].namelen > 0 && name[nv[i].namelen - 1] == '\x01') {
			nv[i].namelen--;
			nv[i].flags = NGHTTP2_NV_FLAG_NO_INDEX;
		}
	}
	*count = n;
	*store = buf;
	return nv;
}

// ---- natives ----

// h2New(type): a session, server (0) or client (1), with the options
// (maxHeaderListPairs, maxReservedRemoteStreams, peerMaxConcurrentStreams:
// -1 for the default). Its first SETTINGS frame is the session's settings()
// call, as in Node.
double __kml_native_h2_new(double type, double maxHeaderListPairs, double maxReservedRemoteStreams, double peerMaxConcurrentStreams) {
	int slot = 0;
	for (int i = 1; i < H2_MAX; i++) if (!h2tab[i]) { slot = i; break; }
	if (!slot) return -1;
	h2sess *h = calloc(1, sizeof(h2sess));
	nghttp2_session_callbacks *cb;
	nghttp2_session_callbacks_new(&cb);
	nghttp2_session_callbacks_set_on_begin_headers_callback(cb, on_begin_headers);
	nghttp2_session_callbacks_set_on_header_callback(cb, on_header);
	nghttp2_session_callbacks_set_on_frame_recv_callback(cb, on_frame_recv);
	nghttp2_session_callbacks_set_on_data_chunk_recv_callback(cb, on_data_chunk);
	nghttp2_session_callbacks_set_on_stream_close_callback(cb, on_stream_close);
	nghttp2_session_callbacks_set_on_frame_not_send_callback(cb, on_frame_not_send);
	nghttp2_option *opt;
	nghttp2_option_new(&opt);
	// Node sends its own WINDOW_UPDATEs as data is consumed; nghttp2's
	// automatic ones are the same while the reader keeps up.
	if (maxReservedRemoteStreams >= 0) nghttp2_option_set_max_reserved_remote_streams(opt, (uint32_t)maxReservedRemoteStreams);
	if (peerMaxConcurrentStreams >= 0) nghttp2_option_set_peer_max_concurrent_streams(opt, (uint32_t)peerMaxConcurrentStreams);
	(void)maxHeaderListPairs;
	int rv = type == 0 ? nghttp2_session_server_new2(&h->s, cb, h, opt) : nghttp2_session_client_new2(&h->s, cb, h, opt);
	nghttp2_option_del(opt);
	nghttp2_session_callbacks_del(cb);
	if (rv != 0) { free(h); return rv; }
	h2tab[slot] = h;
	return slot;
}

// h2Feed: the bytes the socket read. The result is the bytes consumed, or
// an nghttp2 error (negative).
double __kml_native_h2_feed(double id, void *data, long long len, double off, double n) {
	h2sess *h = sess_of(id);
	if (!h) return NGHTTP2_ERR_INVALID_STATE;
	ssize_t rv = nghttp2_session_mem_recv(h->s, (const uint8_t *)data + (long long)off, (size_t)n);
	(void)len;
	return (double)rv;
}

// h2Flush: serializes every frame nghttp2 has ready; the result is the byte
// count h2Take copies out, or an nghttp2 error.
double __kml_native_h2_flush(double id) {
	h2sess *h = sess_of(id);
	if (!h) return 0;
	for (;;) {
		const uint8_t *p;
		ssize_t n = nghttp2_session_mem_send(h->s, &p);
		if (n < 0) return (double)n;
		if (n == 0) break;
		if (h->outlen + (size_t)n > h->outcap) {
			size_t cap = h->outcap ? h->outcap * 2 : 16384;
			while (cap < h->outlen + (size_t)n) cap *= 2;
			h->out = realloc(h->out, cap);
			h->outcap = cap;
		}
		memcpy(h->out + h->outlen, p, (size_t)n);
		h->outlen += (size_t)n;
	}
	return (double)h->outlen;
}

double __kml_native_h2_take(double id, void *out, long long len) {
	h2sess *h = sess_of(id);
	if (!h) return 0;
	size_t n = h->outlen < (size_t)len ? h->outlen : (size_t)len;
	memcpy(out, h->out, n);
	memmove(h->out, h->out + n, h->outlen - n);
	h->outlen -= n;
	return (double)n;
}

// h2Next: takes the next event; its type, 0 when there is none. Its fields
// read through h2Event, its headers as lastString, its bytes through
// h2EventBytes.
double __kml_native_h2_next(double id) {
	h2sess *h = sess_of(id);
	if (!h) return 0;
	free_ev(h->cur);
	h->cur = h->qh;
	if (!h->cur) return 0;
	h->qh = h->cur->next;
	if (!h->qh) h->qt = NULL;
	__kml_native_set_last_string(h->cur->text ? h->cur->text : "");
	return h->cur->type;
}

// which: 0 a, 1 b, 2 c, 3 the byte count.
double __kml_native_h2_event(double id, double which) {
	h2sess *h = sess_of(id);
	if (!h || !h->cur) return 0;
	switch ((int)which) {
	case 0: return h->cur->a;
	case 1: return (double)h->cur->b;
	case 2: return (double)h->cur->c;
	case 3: return (double)h->cur->len;
	}
	return 0;
}

double __kml_native_h2_event_bytes(double id, void *out, long long len) {
	h2sess *h = sess_of(id);
	if (!h || !h->cur) return 0;
	size_t n = h->cur->len < (size_t)len ? h->cur->len : (size_t)len;
	memcpy(out, h->cur->bytes, n);
	return (double)n;
}

// h2Request: a client stream. The result is its id, or an nghttp2 error.
double __kml_native_h2_request(double id, const char *headers, double endStream, double waitForTrailers) {
	h2sess *h = sess_of(id);
	if (!h) return NGHTTP2_ERR_INVALID_STATE;
	size_t n;
	char *store;
	nghttp2_nv *nv = parse_nv(headers, &n, &store);
	h2stream *st = calloc(1, sizeof(h2stream));
	st->want_trailers = waitForTrailers != 0;
	nghttp2_data_provider dp = { .source = { .ptr = NULL }, .read_callback = read_data };
	int32_t sid = nghttp2_submit_request(h->s, NULL, nv, n, endStream != 0 ? NULL : &dp, st);
	free(nv);
	free(store);
	if (sid < 0) { free(st); return sid; }
	st->id = sid;
	pend_add(h, st);
	return sid;
}

// h2Respond: the response headers (a HEADERS frame), with the body to come
// from h2Write unless endStream.
double __kml_native_h2_respond(double id, double sid, const char *headers, double endStream, double waitForTrailers) {
	h2sess *h = sess_of(id);
	if (!h) return NGHTTP2_ERR_INVALID_STATE;
	size_t n;
	char *store;
	nghttp2_nv *nv = parse_nv(headers, &n, &store);
	h2stream *st = stream_of(h, (int32_t)sid);
	if (!st) st = stream_new(h, (int32_t)sid);
	st->want_trailers = waitForTrailers != 0;
	nghttp2_data_provider dp = { .source = { .ptr = NULL }, .read_callback = read_data };
	int rv = nghttp2_submit_response(h->s, (int32_t)sid, nv, n, endStream != 0 ? NULL : &dp);
	free(nv);
	free(store);
	return rv;
}

// h2Headers: an additional (informational) HEADERS frame, or trailers when
// trailers is 1 (END_STREAM).
double __kml_native_h2_headers(double id, double sid, const char *headers, double trailers) {
	h2sess *h = sess_of(id);
	if (!h) return NGHTTP2_ERR_INVALID_STATE;
	size_t n;
	char *store;
	nghttp2_nv *nv = parse_nv(headers, &n, &store);
	int rv;
	if (trailers != 0 && n == 0) {
		// No trailers: an empty DATA frame ends the stream (Node's
		// Http2Stream::SubmitTrailers).
		h2stream *st = stream_of(h, (int32_t)sid);
		if (st) {
			st->want_trailers = 0;
			st->eof = 1;
		}
		nghttp2_data_provider dp = { .source = { .ptr = NULL }, .read_callback = read_data };
		rv = nghttp2_submit_data(h->s, NGHTTP2_FLAG_END_STREAM, (int32_t)sid, &dp);
	} else if (trailers != 0) {
		rv = nghttp2_submit_trailer(h->s, (int32_t)sid, nv, n);
	} else {
		rv = nghttp2_submit_headers(h->s, NGHTTP2_FLAG_NONE, (int32_t)sid, NULL, nv, n, NULL);
	}
	free(nv);
	free(store);
	return rv;
}

// h2Push: a PUSH_PROMISE on sid; the result is the promised stream's id.
double __kml_native_h2_push(double id, double sid, const char *headers) {
	h2sess *h = sess_of(id);
	if (!h) return NGHTTP2_ERR_INVALID_STATE;
	size_t n;
	char *store;
	nghttp2_nv *nv = parse_nv(headers, &n, &store);
	h2stream *st = calloc(1, sizeof(h2stream));
	int32_t pid = nghttp2_submit_push_promise(h->s, NGHTTP2_FLAG_NONE, (int32_t)sid, nv, n, st);
	free(nv);
	free(store);
	if (pid < 0) { free(st); return pid; }
	st->id = pid;
	pend_add(h, st);
	return pid;
}

// h2Write: queues body bytes on a stream (end: the writable side ended).
double __kml_native_h2_write(double id, double sid, void *data, long long len, double off, double n, double end) {
	h2sess *h = sess_of(id);
	if (!h) return NGHTTP2_ERR_INVALID_STATE;
	h2stream *st = stream_of(h, (int32_t)sid);
	if (!st) return NGHTTP2_ERR_STREAM_CLOSED;
	(void)len;
	if (n > 0) {
		h2chunk *c = calloc(1, sizeof(h2chunk));
		c->len = (size_t)n;
		c->p = malloc(c->len);
		memcpy(c->p, (const uint8_t *)data + (long long)off, c->len);
		if (st->tail) st->tail->next = c; else st->head = c;
		st->tail = c;
	}
	if (end != 0) st->eof = 1;
	return nghttp2_session_resume_data(h->s, (int32_t)sid) == 0 ? 0 : 0;
}

// h2Queued: the body bytes a stream has not sent yet.
double __kml_native_h2_queued(double id, double sid) {
	h2sess *h = sess_of(id);
	if (!h) return 0;
	h2stream *st = stream_of(h, (int32_t)sid);
	if (!st) return 0;
	double n = 0;
	for (h2chunk *c = st->head; c; c = c->next) n += (double)(c->len - c->off);
	return n;
}

double __kml_native_h2_rst(double id, double sid, double code) {
	h2sess *h = sess_of(id);
	if (!h) return NGHTTP2_ERR_INVALID_STATE;
	return nghttp2_submit_rst_stream(h->s, NGHTTP2_FLAG_NONE, (int32_t)sid, (uint32_t)code);
}

double __kml_native_h2_settings(double id, void *settings, long long len) {
	h2sess *h = sess_of(id);
	if (!h) return NGHTTP2_ERR_INVALID_STATE;
	size_t n = (size_t)len / 6;
	nghttp2_settings_entry *iv = calloc(n ? n : 1, sizeof(nghttp2_settings_entry));
	const uint8_t *p = settings;
	for (size_t i = 0; i < n; i++) {
		iv[i].settings_id = (int32_t)((p[i * 6] << 8) | p[i * 6 + 1]);
		iv[i].value = ((uint32_t)p[i * 6 + 2] << 24) | ((uint32_t)p[i * 6 + 3] << 16) | ((uint32_t)p[i * 6 + 4] << 8) | p[i * 6 + 5];
	}
	int rv = nghttp2_submit_settings(h->s, NGHTTP2_FLAG_NONE, iv, n);
	free(iv);
	return rv;
}

double __kml_native_h2_ping(double id, void *payload, long long len) {
	h2sess *h = sess_of(id);
	if (!h) return NGHTTP2_ERR_INVALID_STATE;
	uint8_t data[8] = {0};
	memcpy(data, payload, len < 8 ? (size_t)len : 8);
	return nghttp2_submit_ping(h->s, NGHTTP2_FLAG_NONE, data);
}

double __kml_native_h2_goaway(double id, double code, double lastStreamId, void *data, long long len) {
	h2sess *h = sess_of(id);
	if (!h) return NGHTTP2_ERR_INVALID_STATE;
	int32_t last = lastStreamId < 0 ? nghttp2_session_get_last_proc_stream_id(h->s) : (int32_t)lastStreamId;
	return nghttp2_submit_goaway(h->s, NGHTTP2_FLAG_NONE, last, (uint32_t)code, (const uint8_t *)data, (size_t)len);
}

// h2State: which: 0 effectiveLocalWindowSize, 1 effectiveRecvDataLength,
// 2 nextStreamID, 3 localWindowSize, 4 lastProcStreamID, 5 remoteWindowSize,
// 6 outboundQueueSize, 7 deflateDynamicTableSize, 8 inflateDynamicTableSize,
// 9 want read, 10 want write, 20+id: a remote setting, 40+id: a local one.
double __kml_native_h2_state(double id, double which) {
	h2sess *h = sess_of(id);
	if (!h) return 0;
	int w = (int)which;
	if (w >= 40) return nghttp2_session_get_local_settings(h->s, (nghttp2_settings_id)(w - 40));
	if (w >= 20) return nghttp2_session_get_remote_settings(h->s, (nghttp2_settings_id)(w - 20));
	switch (w) {
	case 0: return nghttp2_session_get_effective_local_window_size(h->s);
	case 1: return nghttp2_session_get_effective_recv_data_length(h->s);
	case 2: return nghttp2_session_get_next_stream_id(h->s);
	case 3: return nghttp2_session_get_local_window_size(h->s);
	case 4: return nghttp2_session_get_last_proc_stream_id(h->s);
	case 5: return nghttp2_session_get_remote_window_size(h->s);
	case 6: return (double)nghttp2_session_get_outbound_queue_size(h->s);
	case 7: return (double)nghttp2_session_get_hd_deflate_dynamic_table_size(h->s);
	case 8: return (double)nghttp2_session_get_hd_inflate_dynamic_table_size(h->s);
	case 9: return nghttp2_session_want_read(h->s);
	case 10: return nghttp2_session_want_write(h->s);
	}
	return 0;
}

// h2StreamState: which: 0 state (nghttp2_stream_proto_state), 1 weight,
// 2 sumDependencyWeight, 3 localClose, 4 remoteClose, 5 localWindowSize.
double __kml_native_h2_stream_state(double id, double sid, double which) {
	h2sess *h = sess_of(id);
	if (!h) return 0;
	nghttp2_stream *st = nghttp2_session_find_stream(h->s, (int32_t)sid);
	switch ((int)which) {
	case 0: return st ? nghttp2_stream_get_state(st) : NGHTTP2_STREAM_STATE_CLOSED;
	case 1: return st ? nghttp2_stream_get_weight(st) : 0;
	case 2: return st ? nghttp2_stream_get_sum_dependency_weight(st) : 0;
	case 3: return nghttp2_session_get_stream_local_close(h->s, (int32_t)sid);
	case 4: return nghttp2_session_get_stream_remote_close(h->s, (int32_t)sid);
	case 5: return nghttp2_session_get_stream_local_window_size(h->s, (int32_t)sid);
	}
	return 0;
}

// h2ErrorText: nghttp2's text for a library error (NGHTTP2_ERR_*).
void __kml_native_h2_error_text(double code) {
	__kml_native_set_last_string(nghttp2_strerror((int)code));
}

void __kml_native_h2_free(double id) {
	h2sess *h = sess_of(id);
	if (!h) return;
	h2tab[(long)id] = NULL;
	free_ev(h->cur);
	for (h2ev *e = h->qh; e;) { h2ev *n = e->next; free_ev(e); e = n; }
	nghttp2_session_del(h->s);
	free(h->out);
	free(h);
}
