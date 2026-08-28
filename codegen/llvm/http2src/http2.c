// http2.c — the HTTP/2 server session driver backing `http.listen` over h2
// (TDD-00111 Stage 3). Compiled alongside the program and linked -lnghttp2 only
// when the program uses the h2 server path (UsesHTTP2). Wraps nghttp2's
// callback-driven session API behind a small C ABI the event loop drives, and
// feeds each completed request into the shared handler-invocation core
// (__kml_h2_dispatch, emitted IR) — so routing and the Request/Response shapes
// are reused unchanged. Keeps all nghttp2/*.h out of the emitted LLVM IR.
//
// V1 (S3a): h2c (cleartext, prior-knowledge) and h2-over-TLS (S3b) share this
// driver; the caller supplies the read/write transport. Synchronous handlers
// only — the handler runs inline from the on_frame_recv(END_STREAM) callback, so
// a handler that awaits under h2 is out of V1 scope (the 1.1 fiber path keeps
// full async support).

#include <nghttp2/nghttp2.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <stdint.h>
#include <stdio.h>
#include <fcntl.h>
#include <poll.h>
#include <errno.h>

// --- IR-side symbols (emitted by the compiler when UsesHTTP2) ---------------
// A string-keyed Map<string,string> built with the same runtime the 1.1 path
// uses; header values are ptrtoint'd char* payloads (the IR string convention).
extern void *__kml_map_str_create(void);
extern void __kml_map_str_set(void *m, const char *k, int64_t v);
// The shared handler-invocation core: builds the HttpRequest, calls the user
// handler, returns the response object pointer. Reads of its fields go through
// the typed getters below (specialized to the handler's return type).
extern void *__kml_h2_dispatch(const char *method, const char *path,
                               void *headers, const char *body, int64_t body_len);
extern int64_t __kml_h2_resp_status(void *resp);
extern const char *__kml_h2_resp_body(void *resp);
extern int64_t __kml_h2_resp_bodylen(void *resp);
extern int64_t __kml_h2_resp_hdr_count(void *resp);
extern const char *__kml_h2_resp_hdr_name(void *resp, int64_t i);
extern const char *__kml_h2_resp_hdr_val(void *resp, int64_t i);

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
	long (*rd)(void *io, void *buf, size_t n);  // transport read  (NULL => raw fd)
	long (*wr)(void *io, const void *buf, size_t n); // transport write
	void *io;                                    // transport handle (SSL*) or NULL
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
	(void)user_data;
	if (!(frame->hd.flags & NGHTTP2_FLAG_END_STREAM)) return 0;
	if (frame->hd.type != NGHTTP2_HEADERS && frame->hd.type != NGHTTP2_DATA) {
		return 0;
	}
	int32_t sid = frame->hd.stream_id;
	h2_req *r = nghttp2_session_get_stream_user_data(session, sid);
	if (!r) return 0;

	// Run the shared handler core inline (V1: synchronous handlers).
	void *resp = __kml_h2_dispatch(r->method ? r->method : "GET",
	                               r->path ? r->path : "/",
	                               r->headers,
	                               r->body ? r->body : "",
	                               (int64_t)r->body_len);
	int64_t status = __kml_h2_resp_status(resp);
	const char *body = __kml_h2_resp_body(resp);
	int64_t blen = __kml_h2_resp_bodylen(resp);

	char statusbuf[16];
	snprintf(statusbuf, sizeof(statusbuf), "%lld", (long long)status);
	/* :status plus any response headers the handler set (TDD-00139 Stage 2). */
	int64_t hn = __kml_h2_resp_hdr_count(resp);
	size_t nvn = (size_t)(1 + (hn > 0 ? hn : 0));
	nghttp2_nv *nva = calloc(nvn, sizeof(nghttp2_nv));
	if (!nva) return 0;
	submit_number_header(&nva[0], ":status", statusbuf);
	for (int64_t i = 0; i < hn; i++) {
		const char *hname = __kml_h2_resp_hdr_name(resp, i);
		const char *hval = __kml_h2_resp_hdr_val(resp, i);
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
                                  long (*rd)(void *, void *, size_t),
                                  long (*wr)(void *, const void *, size_t)) {
	h2_conn *c = calloc(1, sizeof(h2_conn));
	if (!c) return NULL;
	c->fd = fd;
	c->io = io;
	c->rd = rd;
	c->wr = wr;

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

static long conn_read(h2_conn *c, void *buf, size_t n) {
	if (c->rd) return c->rd(c->io, buf, n);
	return (long)read(c->fd, buf, n);
}

static long conn_write(h2_conn *c, const void *buf, size_t n) {
	if (c->wr) return c->wr(c->io, buf, n);
	return (long)write(c->fd, buf, n);
}

// __kml_h2_session_feed hands the session bytes the caller already read (e.g.
// the h2 preface the 1.1 path consumed while detecting it), before the recv loop
// takes over reading the socket.
void __kml_h2_session_feed(void *sess, const void *buf, int64_t len) {
	h2_conn *c = (h2_conn *)sess;
	nghttp2_session_mem_recv(c->session, (const uint8_t *)buf, (size_t)len);
}

// __kml_h2_session_recv drains readable socket bytes into the session. Returns
// 0 on success, <0 to close the connection (EOF or protocol error).
int64_t __kml_h2c_pump_all(void);

int __kml_h2_session_recv(void *sess) {
	h2_conn *c = (h2_conn *)sess;
	/* The server drive waits for frames between requests. While in-process
	   client sessions have open streams (TDD-00139 Stage 3), alternate
	   pumping them with a short poll instead of blocking outright — loopback
	   delivery is asynchronous, so a single pump-then-block races the
	   response bytes and deadlocks a same-process server+client pair. */
	if (!c->rd) {
		for (;;) {
			int64_t act = __kml_h2c_pump_all();
			struct pollfd pfd;
			pfd.fd = c->fd;
			pfd.events = POLLIN;
			pfd.revents = 0;
			int pr = poll(&pfd, 1, act > 0 ? 2 : -1);
			if (pr < 0) {
				if (errno == EINTR) continue;
				return -1;
			}
			if (pr > 0) break; /* server bytes readable */
			/* timeout — client work may have produced/consumed frames; loop */
		}
	} else {
		__kml_h2c_pump_all();
	}
	uint8_t buf[16384];
	long n = conn_read(c, buf, sizeof(buf));
	if (n == 0) return -1; // peer closed
	if (n < 0) return 0;   // no data right now (non-blocking) — not an error
	ssize_t rv = nghttp2_session_mem_recv(c->session, buf, (size_t)n);
	if (rv < 0) return -1;
	return 0;
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
		long w = conn_write(c, data, (size_t)len);
		if (w < 0) return -1;
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

// __kml_h2_set_blocking clears O_NONBLOCK on the fd — V1 drives an h2c session
// with blocking reads in the connection fiber (the accept path leaves the fd
// non-blocking for the 1.1 fiber; a non-blocking h2 recv would busy-spin). A
// blocking h2 drive stalls other fibers while one client is mid-request — the
// same V1 simplification the 1.1 path's blocking response write makes.
void __kml_h2_set_blocking(int fd) {
	int fl = fcntl(fd, F_GETFL, 0);
	if (fl >= 0) fcntl(fd, F_SETFL, fl & ~O_NONBLOCK);
}

void __kml_h2_session_del(void *sess) {
	h2_conn *c = (h2_conn *)sess;
	if (!c) return;
	nghttp2_session_del(c->session);
	free(c);
}

// ===========================================================================
// HTTP/2 client sessions (TDD-00139 Stage 3) — h2c prior-knowledge only.
// The IR side registers per-stream callback contexts; frames arriving during
// a pump fire the __kml_h2c_on_* IR bridge. Request bodies are V1-out (GET-
// shaped requests: HEADERS with END_STREAM at submit).
// ===========================================================================
#include <netdb.h>
#include <sys/socket.h>
#include <errno.h>

extern void __kml_h2c_on_header(void *ctx, const char *name, const char *value);
extern void __kml_h2c_on_response(void *ctx);
extern void __kml_h2c_on_data(void *ctx, const char *buf, int64_t len);
extern void __kml_h2c_on_end(void *ctx);

typedef struct h2c_sess {
	nghttp2_session *session;
	int fd;
	int active;     /* open client streams */
	int want_close; /* close() called: terminate once active==0 */
	int dead;
	char authority[256];
	struct h2c_sess *next;
} h2c_sess;

static h2c_sess *h2c_list = NULL;

static int h2c_on_header(nghttp2_session *session, const nghttp2_frame *frame,
                         const uint8_t *name, size_t namelen,
                         const uint8_t *value, size_t valuelen,
                         uint8_t flags, void *user_data) {
	(void)flags; (void)user_data;
	if (frame->hd.type != NGHTTP2_HEADERS ||
	    frame->headers.cat != NGHTTP2_HCAT_RESPONSE) {
		return 0;
	}
	void *ctx = nghttp2_session_get_stream_user_data(session, frame->hd.stream_id);
	if (!ctx) return 0;
	char *n = dupn((const char *)name, namelen);
	char *v = dupn((const char *)value, valuelen);
	if (n && v) __kml_h2c_on_header(ctx, n, v);
	free(n);
	free(v);
	return 0;
}

static int h2c_on_frame_recv(nghttp2_session *session, const nghttp2_frame *frame,
                             void *user_data) {
	(void)user_data;
	if (frame->hd.type != NGHTTP2_HEADERS ||
	    frame->headers.cat != NGHTTP2_HCAT_RESPONSE) {
		return 0;
	}
	void *ctx = nghttp2_session_get_stream_user_data(session, frame->hd.stream_id);
	if (ctx) __kml_h2c_on_response(ctx);
	return 0;
}

static int h2c_on_data_chunk(nghttp2_session *session, uint8_t flags,
                             int32_t stream_id, const uint8_t *data, size_t len,
                             void *user_data) {
	(void)flags; (void)user_data;
	void *ctx = nghttp2_session_get_stream_user_data(session, stream_id);
	if (ctx) __kml_h2c_on_data(ctx, (const char *)data, (int64_t)len);
	return 0;
}

static int h2c_on_stream_close(nghttp2_session *session, int32_t stream_id,
                               uint32_t error_code, void *user_data) {
	(void)error_code;
	h2c_sess *s = (h2c_sess *)user_data;
	void *ctx = nghttp2_session_get_stream_user_data(session, stream_id);
	if (ctx) __kml_h2c_on_end(ctx);
	if (s && s->active > 0) s->active--;
	return 0;
}

// __kml_h2c_connect_url: parse an h2c authority ("http://host:port[/…]"),
// TCP-connect (blocking), start a client-mode session (preface + SETTINGS
// sent), register it for pumping. NULL on any failure — including an
// https:// authority (TLS client sessions are not supported).
void *__kml_h2c_connect_url(const char *url) {
	if (!url || strncmp(url, "http://", 7) != 0) return NULL;
	const char *hostp = url + 7;
	char host[224];
	char port[16] = "80";
	size_t hi = 0;
	while (*hostp && *hostp != ':' && *hostp != '/' && hi < sizeof(host) - 1) {
		host[hi++] = *hostp++;
	}
	host[hi] = '\0';
	if (*hostp == ':') {
		hostp++;
		size_t pi = 0;
		while (*hostp && *hostp != '/' && pi < sizeof(port) - 1) {
			port[pi++] = *hostp++;
		}
		port[pi] = '\0';
	}
	if (hi == 0) return NULL;

	struct addrinfo hints, *res = NULL;
	memset(&hints, 0, sizeof(hints));
	hints.ai_family = AF_INET;
	hints.ai_socktype = SOCK_STREAM;
	if (getaddrinfo(host, port, &hints, &res) != 0 || !res) return NULL;
	int fd = socket(res->ai_family, res->ai_socktype, res->ai_protocol);
	if (fd < 0) { freeaddrinfo(res); return NULL; }
	if (connect(fd, res->ai_addr, res->ai_addrlen) != 0) {
		freeaddrinfo(res);
		close(fd);
		return NULL;
	}
	freeaddrinfo(res);
	int fl = fcntl(fd, F_GETFL, 0);
	if (fl >= 0) fcntl(fd, F_SETFL, fl | O_NONBLOCK);

	h2c_sess *s = calloc(1, sizeof(h2c_sess));
	if (!s) { close(fd); return NULL; }
	s->fd = fd;
	snprintf(s->authority, sizeof(s->authority), "%s:%s", host, port);

	nghttp2_session_callbacks *cbs;
	nghttp2_session_callbacks_new(&cbs);
	nghttp2_session_callbacks_set_on_header_callback(cbs, h2c_on_header);
	nghttp2_session_callbacks_set_on_frame_recv_callback(cbs, h2c_on_frame_recv);
	nghttp2_session_callbacks_set_on_data_chunk_recv_callback(cbs, h2c_on_data_chunk);
	nghttp2_session_callbacks_set_on_stream_close_callback(cbs, h2c_on_stream_close);
	nghttp2_session_client_new(&s->session, cbs, s);
	nghttp2_session_callbacks_del(cbs);

	nghttp2_settings_entry iv[1] = {
	    {NGHTTP2_SETTINGS_MAX_CONCURRENT_STREAMS, 100}};
	nghttp2_submit_settings(s->session, NGHTTP2_FLAG_NONE, iv, 1);

	s->next = h2c_list;
	h2c_list = s;
	return s;
}

static void h2c_nv(nghttp2_nv *nv, const char *name, const char *value) {
	nv->name = (uint8_t *)name;
	nv->namelen = strlen(name);
	nv->value = (uint8_t *)value;
	nv->valuelen = strlen(value);
	nv->flags = NGHTTP2_NV_FLAG_NONE;
}

// __kml_h2c_request: submit a body-less request (END_STREAM at submit) with
// optional extra literal headers. ctx is the IR-side stream object the frame
// callbacks fire into. Returns the stream id, or -1.
int32_t __kml_h2c_request(void *sp, void *ctx, const char *method,
                          const char *path, const char **hnames,
                          const char **hvals, int64_t nh) {
	h2c_sess *s = (h2c_sess *)sp;
	if (!s || s->dead) return -1;
	size_t nvn = 4 + (size_t)(nh > 0 ? nh : 0);
	nghttp2_nv *nva = calloc(nvn, sizeof(nghttp2_nv));
	if (!nva) return -1;
	h2c_nv(&nva[0], ":method", method && *method ? method : "GET");
	h2c_nv(&nva[1], ":path", path && *path ? path : "/");
	h2c_nv(&nva[2], ":scheme", "http");
	h2c_nv(&nva[3], ":authority", s->authority);
	for (int64_t i = 0; i < nh; i++) {
		h2c_nv(&nva[4 + i], hnames[i], hvals[i]);
	}
	int32_t sid = nghttp2_submit_request(s->session, NULL, nva, nvn, NULL, ctx);
	free(nva);
	if (sid < 0) return -1;
	s->active++;
	return sid;
}

// One nonblocking send+recv round for one session.
static void h2c_pump_one(h2c_sess *s) {
	if (s->dead) return;
	for (;;) {
		const uint8_t *data = NULL;
		ssize_t len = nghttp2_session_mem_send(s->session, &data);
		if (len < 0) { s->dead = 1; return; }
		if (len == 0) break;
		long w = (long)write(s->fd, data, (size_t)len);
		if (w < 0 && errno != EAGAIN && errno != EWOULDBLOCK) { s->dead = 1; return; }
		if (w < 0) break;
	}
	for (;;) {
		uint8_t buf[16384];
		long n = (long)read(s->fd, buf, sizeof(buf));
		if (n == 0) { s->dead = 1; return; } /* peer closed */
		if (n < 0) break;                    /* EAGAIN — nothing more now */
		if (nghttp2_session_mem_recv(s->session, buf, (size_t)n) < 0) {
			s->dead = 1;
			return;
		}
	}
	if (s->want_close && s->active == 0) {
		nghttp2_session_terminate_session(s->session, NGHTTP2_NO_ERROR);
		const uint8_t *data = NULL;
		ssize_t len;
		while ((len = nghttp2_session_mem_send(s->session, &data)) > 0) {
			if (write(s->fd, data, (size_t)len) < 0) break;
		}
		close(s->fd);
		s->dead = 1;
	}
}

// __kml_h2c_pump_all: one round over every live session; returns the number
// of still-open client streams (the loop's keepalive signal).
int64_t __kml_h2c_pump_all(void) {
	int64_t total = 0;
	for (h2c_sess *s = h2c_list; s; s = s->next) {
		h2c_pump_one(s);
		if (!s->dead) total += s->active;
	}
	return total;
}

// void-typed tick for the event loop's per-iteration hook.
void __kml_h2c_pump_tick(void) { (void)__kml_h2c_pump_all(); }

// __kml_h2c_flush: drive every session until no client streams remain (or
// everything is dead). Called after the event loop exits, mirroring the http/1
// client's post-loop reaction flush. Bounded so a wedged peer can't hang exit.
void __kml_h2c_flush(void) {
	for (int i = 0; i < 20000; i++) {
		if (__kml_h2c_pump_all() <= 0) return;
		usleep(500);
	}
}

void __kml_h2c_close(void *sp) {
	h2c_sess *s = (h2c_sess *)sp;
	if (s) s->want_close = 1;
}

void __kml_h2c_destroy(void *sp) {
	h2c_sess *s = (h2c_sess *)sp;
	if (!s || s->dead) return;
	close(s->fd);
	s->dead = 1;
}
