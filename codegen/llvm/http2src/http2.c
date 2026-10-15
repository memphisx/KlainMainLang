// http2.c — the HTTP/2 server session driver backing `http.listen` over h2
// (TDD-00111 Stage 3). Compiled alongside the program and linked -lnghttp2 only
// when the program uses the h2 server path (UsesHTTP2). Wraps nghttp2's
// callback-driven session API behind a small C ABI the event loop drives, and
// feeds each completed request into the shared handler-invocation core
// (__kml_h2_dispatch, emitted IR) — so routing and the Request/Response shapes
// are reused unchanged. Keeps all nghttp2/*.h out of the emitted LLVM IR.
//
// h2c (cleartext, prior-knowledge). The session runs on the connection's
// fiber with the socket non-blocking: a read or write that would block yields
// the fiber to the loop until the socket is ready. Synchronous handlers only —
// the handler runs inline from the on_frame_recv(END_STREAM) callback, so a
// handler that awaits under h2 is out of scope (the 1.1 fiber path keeps full
// async support).

#include <nghttp2/nghttp2.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <stdio.h>
#include <errno.h>
#ifdef _WIN32
#include "kml_posix_compat.h"
#else
#include <unistd.h>
#include <fcntl.h>
#endif

// --- IR-side symbols (emitted by the compiler when UsesHTTP2) ---------------
// A string-keyed Map<string,string> built with the same runtime the 1.1 path
// uses; header values are ptrtoint'd char* payloads (the IR string convention).
extern void *__kml_map_str_create(void);
extern void __kml_map_str_set(void *m, const char *k, int64_t v);
// The shared handler-invocation core: builds the HttpRequest, calls the user
// handler, returns the response object pointer, and reads its fields through the
// typed getters — all specialized per-server to the handler's return type. To
// let more than one h2 server (TDD-00191 Stage 4) route to its own handler, the
// IR emits a suffixed set per server and passes a vtable of their addresses into
// __kml_h2_session_server_new; on_frame_recv calls through the connection's
// vtable rather than fixed global symbols.
typedef struct {
	void *(*dispatch)(const char *method, const char *path, void *headers,
	                  const char *body, int64_t body_len);
	int64_t (*resp_status)(void *resp);
	const char *(*resp_body)(void *resp);
	int64_t (*resp_bodylen)(void *resp);
	int64_t (*resp_hdr_count)(void *resp);
	const char *(*resp_hdr_name)(void *resp, int64_t i);
	const char *(*resp_hdr_val)(void *resp, int64_t i);
} h2_disp_vtbl;

// --- per-stream request accumulation ----------------------------------------
typedef struct {
	char *method;
	char *path;
	void *headers; // Map<string,string>
	char *body;
	size_t body_len;
	size_t body_cap;
} h2_req;

typedef struct {
	nghttp2_session *session;
	int fd;
	int64_t (*rd)(void *io, void *buf, size_t n);  // transport read  (NULL => raw fd)
	int64_t (*wr)(void *io, const void *buf, size_t n); // transport write
	void *io;                                    // transport handle (SSL*) or NULL
	const h2_disp_vtbl *disp;                    // per-server handler bridge (TDD-00191 Stage 4)
} h2_conn;

static char *dupn(const char *s, size_t n) {
	char *p = malloc(n + 1);
	if (!p) return NULL;
	memcpy(p, s, n);
	p[n] = '\0';
	return p;
}

/* A length-prefixed string in the IR runtime's layout ([i64 len][bytes][NUL],
   value pointer = base+8) — required for anything handed to string-typed IR
   code, whose binary-safe ops read the header at ptr-8. */
static char *kml_strn(const char *s, size_t n) {
	char *b = malloc(n + 9);
	if (!b) return NULL;
	*(int64_t *)b = (int64_t)n;
	memcpy(b + 8, s, n);
	b[8 + n] = '\0';
	return b + 8;
}

// --- nghttp2 callbacks ------------------------------------------------------

static int on_begin_headers(nghttp2_session *session, const nghttp2_frame *frame,
                            void *user_data) {
	(void)user_data;
	if (frame->hd.type != NGHTTP2_HEADERS ||
	    frame->headers.cat != NGHTTP2_HCAT_REQUEST) {
		return 0;
	}
	h2_req *r = calloc(1, sizeof(h2_req));
	if (!r) return NGHTTP2_ERR_CALLBACK_FAILURE;
	r->headers = __kml_map_str_create();
	nghttp2_session_set_stream_user_data(session, frame->hd.stream_id, r);
	return 0;
}

static int on_header(nghttp2_session *session, const nghttp2_frame *frame,
                     const uint8_t *name, size_t namelen,
                     const uint8_t *value, size_t valuelen,
                     uint8_t flags, void *user_data) {
	(void)flags;
	(void)user_data;
	h2_req *r = nghttp2_session_get_stream_user_data(session, frame->hd.stream_id);
	if (!r) return 0;
	if (namelen == 7 && memcmp(name, ":method", 7) == 0) {
		free(r->method);
		r->method = dupn((const char *)value, valuelen);
	} else if (namelen == 5 && memcmp(name, ":path", 5) == 0) {
		free(r->path);
		r->path = dupn((const char *)value, valuelen);
	} else if (namelen > 0 && name[0] == ':') {
		// other pseudo-headers (:scheme/:authority) — not surfaced to the handler
	} else {
		/* Length-prefixed (IR string layout) and stored as-is — the map does
		   NOT copy, so both must stay alive for the request (they leak in
		   manual mode, the standing convention). An earlier free(k) here
		   left a dangling key that shadowed every real header. */
		char *k = kml_strn((const char *)name, namelen);
		char *v = kml_strn((const char *)value, valuelen);
		if (k && v) __kml_map_str_set(r->headers, k, (int64_t)(intptr_t)v);
	}
	return 0;
}

static int on_data_chunk(nghttp2_session *session, uint8_t flags,
                         int32_t stream_id, const uint8_t *data, size_t len,
                         void *user_data) {
	(void)flags;
	(void)user_data;
	h2_req *r = nghttp2_session_get_stream_user_data(session, stream_id);
	if (!r) return 0;
	if (r->body_len + len + 1 > r->body_cap) {
		size_t cap = r->body_cap ? r->body_cap * 2 : 256;
		while (cap < r->body_len + len + 1) cap *= 2;
		char *nb = realloc(r->body, cap);
		if (!nb) return NGHTTP2_ERR_CALLBACK_FAILURE;
		r->body = nb;
		r->body_cap = cap;
	}
	memcpy(r->body + r->body_len, data, len);
	r->body_len += len;
	r->body[r->body_len] = '\0';
	return 0;
}

// A data source that streams a fixed response body buffer back to nghttp2.
typedef struct { const char *p; size_t len, off; } h2_body_src;

static ssize_t body_read_cb(nghttp2_session *session, int32_t stream_id,
                            uint8_t *buf, size_t length, uint32_t *data_flags,
                            nghttp2_data_source *source, void *user_data) {
	(void)session; (void)stream_id; (void)user_data;
	h2_body_src *b = (h2_body_src *)source->ptr;
	size_t remain = b->len - b->off;
	size_t n = remain < length ? remain : length;
	if (n) memcpy(buf, b->p + b->off, n);
	b->off += n;
	if (b->off >= b->len) {
		*data_flags |= NGHTTP2_DATA_FLAG_EOF;
		free(b);
	}
	return (ssize_t)n;
}

static void submit_number_header(nghttp2_nv *nv, const char *name, char *valbuf) {
	nv->name = (uint8_t *)name;
	nv->namelen = strlen(name);
	nv->value = (uint8_t *)valbuf;
	nv->valuelen = strlen(valbuf);
	nv->flags = NGHTTP2_NV_FLAG_NONE;
}

static int on_frame_recv(nghttp2_session *session, const nghttp2_frame *frame,
                         void *user_data) {
	h2_conn *c = (h2_conn *)user_data;
	if (!(frame->hd.flags & NGHTTP2_FLAG_END_STREAM)) return 0;
	if (frame->hd.type != NGHTTP2_HEADERS && frame->hd.type != NGHTTP2_DATA) {
		return 0;
	}
	int32_t sid = frame->hd.stream_id;
	h2_req *r = nghttp2_session_get_stream_user_data(session, sid);
	if (!r) return 0;

	// Run the shared handler core inline (V1: synchronous handlers), routed
	// through this connection's per-server bridge vtable (TDD-00191 Stage 4).
	const h2_disp_vtbl *d = c->disp;
	void *resp = d->dispatch(r->method ? r->method : "GET",
	                         r->path ? r->path : "/",
	                         r->headers,
	                         r->body ? r->body : "",
	                         (int64_t)r->body_len);
	int64_t status = d->resp_status(resp);
	const char *body = d->resp_body(resp);
	int64_t blen = d->resp_bodylen(resp);

	char statusbuf[16];
	snprintf(statusbuf, sizeof(statusbuf), "%lld", (long long)status);
	/* :status plus any response headers the handler set (TDD-00139 Stage 2). */
	int64_t hn = d->resp_hdr_count(resp);
	size_t nvn = (size_t)(1 + (hn > 0 ? hn : 0));
	nghttp2_nv *nva = calloc(nvn, sizeof(nghttp2_nv));
	if (!nva) return 0;
	submit_number_header(&nva[0], ":status", statusbuf);
	for (int64_t i = 0; i < hn; i++) {
		const char *hname = d->resp_hdr_name(resp, i);
		const char *hval = d->resp_hdr_val(resp, i);
		if (!hname || !hval) { hname = ""; hval = ""; }
		nva[1 + i].name = (uint8_t *)hname;
		nva[1 + i].namelen = strlen(hname);
		nva[1 + i].value = (uint8_t *)hval;
		nva[1 + i].valuelen = strlen(hval);
		nva[1 + i].flags = NGHTTP2_NV_FLAG_NONE;
	}

	h2_body_src *bsrc = calloc(1, sizeof(h2_body_src));
	nghttp2_data_provider prov;
	memset(&prov, 0, sizeof(prov));
	if (bsrc && body && blen > 0) {
		bsrc->p = body;
		bsrc->len = (size_t)blen;
		prov.source.ptr = bsrc;
		prov.read_callback = body_read_cb;
		nghttp2_submit_response(session, sid, nva, nvn, &prov);
	} else {
		free(bsrc);
		nghttp2_submit_response(session, sid, nva, nvn, NULL);
	}
	free(nva);
	return 0;
}

static int on_stream_close(nghttp2_session *session, int32_t stream_id,
                           uint32_t error_code, void *user_data) {
	(void)error_code; (void)user_data;
	h2_req *r = nghttp2_session_get_stream_user_data(session, stream_id);
	if (r) {
		free(r->method);
		free(r->path);
		free(r->body);
		free(r);
	}
	return 0;
}

// --- session ABI (driven by the event loop) ---------------------------------

// __kml_h2_session_server_new wraps an accepted fd in a server nghttp2 session.
// io/rd/wr describe the transport: for h2c pass io=NULL (raw fd read/write);
// for h2-over-TLS pass the SSL* and the SSL read/write shims. Sends the initial
// SETTINGS frame. Returns the h2_conn* or NULL.
void *__kml_h2_session_server_new(int fd, void *io,
                                  int64_t (*rd)(void *, void *, size_t),
                                  int64_t (*wr)(void *, const void *, size_t),
                                  const h2_disp_vtbl *disp) {
	h2_conn *c = calloc(1, sizeof(h2_conn));
	if (!c) return NULL;
	c->fd = fd;
	c->io = io;
	c->rd = rd;
	c->wr = wr;
	c->disp = disp;

	nghttp2_session_callbacks *cbs;
	nghttp2_session_callbacks_new(&cbs);
	nghttp2_session_callbacks_set_on_begin_headers_callback(cbs, on_begin_headers);
	nghttp2_session_callbacks_set_on_header_callback(cbs, on_header);
	nghttp2_session_callbacks_set_on_data_chunk_recv_callback(cbs, on_data_chunk);
	nghttp2_session_callbacks_set_on_frame_recv_callback(cbs, on_frame_recv);
	nghttp2_session_callbacks_set_on_stream_close_callback(cbs, on_stream_close);
	nghttp2_session_server_new(&c->session, cbs, c);
	nghttp2_session_callbacks_del(cbs);

	nghttp2_settings_entry iv[1] = {
	    {NGHTTP2_SETTINGS_MAX_CONCURRENT_STREAMS, 100}};
	nghttp2_submit_settings(c->session, NGHTTP2_FLAG_NONE, iv, 1);
	return c;
}

static int64_t conn_read(h2_conn *c, void *buf, size_t n) {
	if (c->rd) return c->rd(c->io, buf, n);
	return (int64_t)read(c->fd, buf, n);
}

static int64_t conn_write(h2_conn *c, const void *buf, size_t n) {
	if (c->wr) return c->wr(c->io, buf, n);
	return (int64_t)write(c->fd, buf, n);
}

// __kml_h2_session_feed hands the session bytes the caller already read (e.g.
// the h2 preface the 1.1 path consumed while detecting it), before the recv loop
// takes over reading the socket.
void __kml_h2_session_feed(void *sess, const void *buf, int64_t len) {
	h2_conn *c = (h2_conn *)sess;
	nghttp2_session_mem_recv(c->session, (const uint8_t *)buf, (size_t)len);
}

/* The connection fiber this session runs on yields to the loop until its
 * descriptor is ready (loop.c resumes it): reading or, with want_write,
 * writing. */
extern _Thread_local int64_t __kml_current_conn_idx;
extern _Thread_local char *__kml_conn_data;
extern _Thread_local unsigned char __kml_main_ctx[];
extern void __kml_conn_park_write(int fd, int on);
#ifndef _WIN32
int swapcontext(void *, void *);
#endif

static void yield_fiber(h2_conn *c, int want_write) {
	int64_t idx = __kml_current_conn_idx;
	if (want_write) __kml_conn_park_write(c->fd, 1);
	char *slot = __kml_conn_data + idx * KML_CONN_SIZE;
	swapcontext(*(void **)(slot + KML_CONN_CTX), (void *)__kml_main_ctx);
	if (want_write) __kml_conn_park_write(c->fd, 0);
}

// __kml_h2_session_recv reads the next bytes into the session, yielding the
// connection fiber while none are ready. Returns 0 on success, <0 to close
// the connection (EOF or protocol error).
int __kml_h2_session_recv(void *sess) {
	h2_conn *c = (h2_conn *)sess;
	uint8_t buf[16384];
	for (;;) {
		int64_t n = conn_read(c, buf, sizeof(buf));
		if (n == 0) return -1; // peer closed
		if (n < 0) {
			if (errno == EAGAIN || errno == EWOULDBLOCK || errno == EINTR) {
				yield_fiber(c, 0);
				continue;
			}
			return -1;
		}
		return nghttp2_session_mem_recv(c->session, buf, (size_t)n) < 0 ? -1 : 0;
	}
}

// __kml_h2_session_send flushes any pending session output to the socket.
// Returns 0 on success, <0 on write/protocol error.
int __kml_h2_session_send(void *sess) {
	h2_conn *c = (h2_conn *)sess;
	for (;;) {
		const uint8_t *data = NULL;
		ssize_t len = nghttp2_session_mem_send(c->session, &data);
		if (len < 0) return -1;
		if (len == 0) break;
		while (len > 0) {
			int64_t w = conn_write(c, data, (size_t)len);
			if (w < 0) {
				if (errno == EAGAIN || errno == EWOULDBLOCK || errno == EINTR) {
					yield_fiber(c, 1);
					continue;
				}
				return -1;
			}
			data += w;
			len -= w;
		}
	}
	return 0;
}

int __kml_h2_session_want_read(void *sess) {
	return nghttp2_session_want_read(((h2_conn *)sess)->session) ? 1 : 0;
}

int __kml_h2_session_want_write(void *sess) {
	return nghttp2_session_want_write(((h2_conn *)sess)->session) ? 1 : 0;
}

int __kml_h2_session_fd(void *sess) { return ((h2_conn *)sess)->fd; }

void __kml_h2_session_del(void *sess) {
	h2_conn *c = (h2_conn *)sess;
	if (!c) return;
	nghttp2_session_del(c->session);
	free(c);
}
