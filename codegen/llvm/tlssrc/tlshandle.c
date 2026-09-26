// tlshandle.c — TLS over the thread pool's TCP handles (klainpool.c), the
// native side of the `tls` module written in TypeScript (Node's tls_wrap):
// secure contexts, a session on a handle, its handshake and I/O. Compiled
// (and linked -lssl -lcrypto) when a program's TypeScript uses them.

#include <stdint.h>
#include <stdio.h>
#include <openssl/ssl.h>
#include <openssl/err.h>
#include <openssl/x509v3.h>
#include <openssl/pem.h>
#include <openssl/bio.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>
#ifdef _WIN32
#include "kml_posix_compat.h"
intptr_t __kml_win_fd_socket(int fd);
static void tls_attach_fd(SSL *ssl, int fd) {
	SSL_set_app_data(ssl, (void *)(intptr_t)fd);
	SSL_set_fd(ssl, (int)__kml_win_fd_socket(fd));
}
#else
static void tls_attach_fd(SSL *ssl, int fd) { SSL_set_fd(ssl, fd); }
#endif

static char *dup_err(const char *fallback) {
	unsigned long e = ERR_get_error();
	if (e != 0) {
		char buf[256];
		ERR_error_string_n(e, buf, sizeof(buf));
		return strdup(buf);
	}
	return strdup(fallback);
}
// Node's tls_wrap: a TLSSocket's handle is a TCP handle with a session on it.
// klainpool.c drives the handshake and the I/O through these operations; the
// program starts them with __kml_native_tls_start.

typedef struct kml_tls_ops {
	int (*handshake)(void *tls);
	int64_t (*read)(void *tls, char *buf, int64_t n);
	int64_t (*write)(void *tls, const char *buf, int64_t n);
	int (*want_write)(void *tls);
	int (*pending)(void *tls);
	void (*shutdown)(void *tls);
	void (*free)(void *tls);
} kml_tls_ops;

void __kml_tcp_set_tls_ops(const void *ops);
int __kml_tcp_fd(double id);
int __kml_tcp_attach_tls(double id, void *tls, void *inv, void *clo);
void *__kml_tcp_tls(double id);
extern char *__kml_str_alloc(int64_t n);
extern void __kml_str_finalize(char *s);

typedef struct kml_tls_sess {
	SSL *ssl;
	int server, want_write, reject_unauthorized;
	long verify;          // X509 verify result after the handshake
	char *servername;
	char err[256];        // the failed handshake's reason
} kml_tls_sess;

static int tls_sess_handshake(void *p) {
	kml_tls_sess *s = (kml_tls_sess *)p;
	ERR_clear_error();
	int rc = s->server ? SSL_accept(s->ssl) : SSL_connect(s->ssl);
	s->want_write = 0;
	if (rc == 1) {
		s->verify = SSL_get_verify_result(s->ssl);
		return 1;
	}
	int e = SSL_get_error(s->ssl, rc);
	if (e == SSL_ERROR_WANT_READ) return 0;
	if (e == SSL_ERROR_WANT_WRITE) { s->want_write = 1; return 0; }
	unsigned long ec = ERR_peek_last_error();
	if (ec) {
		const char *r = ERR_reason_error_string(ec);
		snprintf(s->err, sizeof s->err, "%s", r ? r : "handshake failure");
	} else if (e == SSL_ERROR_SYSCALL || e == SSL_ERROR_ZERO_RETURN) {
		snprintf(s->err, sizeof s->err, "Client network socket disconnected before secure TLS connection was established");
	} else {
		snprintf(s->err, sizeof s->err, "handshake failure");
	}
	return -1;
}

static int64_t tls_sess_read(void *p, char *buf, int64_t n) {
	kml_tls_sess *s = (kml_tls_sess *)p;
	ERR_clear_error();
	int r = SSL_read(s->ssl, buf, (int)(n > 0x7fffffff ? 0x7fffffff : n));
	if (r > 0) return r;
	int e = SSL_get_error(s->ssl, r);
	if (e == SSL_ERROR_WANT_READ) return -EAGAIN;
	if (e == SSL_ERROR_WANT_WRITE) { s->want_write = 1; return -EAGAIN; }
	if (e == SSL_ERROR_ZERO_RETURN) return 0;
	if (e == SSL_ERROR_SYSCALL && errno == 0) return 0; // EOF without close_notify
	return e == SSL_ERROR_SYSCALL && errno ? -errno : -ECONNRESET;
}

static int64_t tls_sess_write(void *p, const char *buf, int64_t n) {
	kml_tls_sess *s = (kml_tls_sess *)p;
	if (n == 0) return 0;
	ERR_clear_error();
	int r = SSL_write(s->ssl, buf, (int)(n > 0x7fffffff ? 0x7fffffff : n));
	s->want_write = 0;
	if (r > 0) return r;
	int e = SSL_get_error(s->ssl, r);
	if (e == SSL_ERROR_WANT_WRITE) { s->want_write = 1; return -EAGAIN; }
	if (e == SSL_ERROR_WANT_READ) return -EAGAIN;
	return e == SSL_ERROR_SYSCALL && errno ? -errno : -EPIPE;
}

static int tls_sess_want_write(void *p) { return ((kml_tls_sess *)p)->want_write; }
static int tls_sess_pending(void *p) { return SSL_pending(((kml_tls_sess *)p)->ssl); }
static void tls_sess_shutdown(void *p) { SSL_shutdown(((kml_tls_sess *)p)->ssl); }
static void tls_sess_free(void *p) {
	kml_tls_sess *s = (kml_tls_sess *)p;
	SSL_free(s->ssl);
	free(s->servername);
	free(s);
}

static const kml_tls_ops tls_sess_ops = {
	tls_sess_handshake, tls_sess_read, tls_sess_write, tls_sess_want_write,
	tls_sess_pending, tls_sess_shutdown, tls_sess_free,
};

// Secure contexts, by id.
static SSL_CTX **tls_ctx_tab = NULL;
static int tls_ctx_n = 0, tls_ctx_cap = 0;
static char *tls_last_err = NULL;

static char *tls_str(const char *s) {
	if (!s) s = "";
	int64_t n = (int64_t)strlen(s);
	char *out = __kml_str_alloc(n + 1);
	memcpy(out, s, (size_t)n + 1);
	__kml_str_finalize(out);
	return out;
}

// The ALPN protocols a server offers, as "h2,http/1.1".
static int tls_alpn_select_list(SSL *ssl, const unsigned char **out, unsigned char *outlen,
                                const unsigned char *in, unsigned int inlen, void *arg) {
	const unsigned char *prefs = (const unsigned char *)arg; // wire format
	if (!prefs) return SSL_TLSEXT_ERR_NOACK;
	unsigned char *sel = NULL;
	if (SSL_select_next_proto(&sel, outlen, prefs + 1, prefs[0], in, inlen) == OPENSSL_NPN_NEGOTIATED) {
		*out = sel;
		return SSL_TLSEXT_ERR_OK;
	}
	return SSL_TLSEXT_ERR_NOACK;
}

// ALPN "a,b" as the wire format, length-prefixed with the total in byte 0.
static unsigned char *tls_alpn_wire(const char *list) {
	if (!list || !*list) return NULL;
	size_t n = strlen(list);
	unsigned char *w = (unsigned char *)calloc(1, n + 3);
	size_t o = 1;
	const char *p = list;
	while (*p) {
		const char *c = strchr(p, ',');
		size_t len = c ? (size_t)(c - p) : strlen(p);
		if (len > 0 && len < 256) {
			w[o++] = (unsigned char)len;
			memcpy(w + o, p, len);
			o += len;
		}
		if (!c) break;
		p = c + 1;
	}
	w[0] = (unsigned char)(o - 1);
	return w;
}

// A secure context: a server's certificate and key, a client's extra CA,
// the ALPN protocols. Returns its id, or -1 (the reason via tlsLastError).
double __kml_native_tls_context(_Bool server, const char *cert, const char *key, const char *ca, const char *alpn) {
	SSL_CTX *ctx = SSL_CTX_new(server ? TLS_server_method() : TLS_client_method());
	free(tls_last_err);
	tls_last_err = NULL;
	if (!ctx) {
		tls_last_err = dup_err("SSL_CTX_new failed");
		return -1;
	}
	SSL_CTX_set_min_proto_version(ctx, TLS1_2_VERSION);
	SSL_CTX_set_mode(ctx, SSL_MODE_ACCEPT_MOVING_WRITE_BUFFER | SSL_MODE_ENABLE_PARTIAL_WRITE);
	if (cert && *cert) {
		BIO *b = BIO_new_mem_buf(cert, -1);
		X509 *x = b ? PEM_read_bio_X509(b, NULL, NULL, NULL) : NULL;
		int ok = x && SSL_CTX_use_certificate(ctx, x) == 1;
		// Any further certificates in the PEM are the chain.
		X509 *extra;
		while (ok && b && (extra = PEM_read_bio_X509(b, NULL, NULL, NULL)) != NULL) SSL_CTX_add_extra_chain_cert(ctx, extra);
		if (x) X509_free(x);
		if (b) BIO_free(b);
		ERR_clear_error();
		if (!ok) {
			tls_last_err = strdup("error:0A00007F:SSL routines::invalid certificate");
			SSL_CTX_free(ctx);
			return -1;
		}
	}
	if (key && *key) {
		BIO *b = BIO_new_mem_buf(key, -1);
		EVP_PKEY *k = b ? PEM_read_bio_PrivateKey(b, NULL, NULL, NULL) : NULL;
		if (b) BIO_free(b);
		if (!k || SSL_CTX_use_PrivateKey(ctx, k) != 1) {
			if (k) EVP_PKEY_free(k);
			tls_last_err = dup_err("invalid private key");
			SSL_CTX_free(ctx);
			return -1;
		}
		EVP_PKEY_free(k);
	}
	if (ca && *ca) {
		X509_STORE *st = SSL_CTX_get_cert_store(ctx);
		BIO *b = BIO_new_mem_buf(ca, -1);
		X509 *x;
		while (b && (x = PEM_read_bio_X509(b, NULL, NULL, NULL)) != NULL) {
			X509_STORE_add_cert(st, x);
			X509_free(x);
		}
		if (b) BIO_free(b);
		ERR_clear_error();
	} else if (!server) {
		SSL_CTX_set_default_verify_paths(ctx);
	}
	unsigned char *wire = tls_alpn_wire(alpn);
	if (wire) {
		if (server) SSL_CTX_set_alpn_select_cb(ctx, tls_alpn_select_list, wire);
		else SSL_CTX_set_alpn_protos(ctx, wire + 1, wire[0]);
	}
	if (tls_ctx_n == tls_ctx_cap) {
		tls_ctx_cap = tls_ctx_cap ? tls_ctx_cap * 2 : 8;
		tls_ctx_tab = (SSL_CTX **)realloc(tls_ctx_tab, (size_t)tls_ctx_cap * sizeof *tls_ctx_tab);
	}
	tls_ctx_tab[tls_ctx_n] = ctx;
	return tls_ctx_n++;
}

char *__kml_native_tls_last_error(void) { return tls_str(tls_last_err); }

// Start TLS on the TCP handle with the context; the handshake runs as the
// socket becomes ready, then onSecure(0, 0) or onSecure(1, 0) (the reason
// via tlsInfo(handle, 3)).
double __kml_native_tls_start(double handle, double ctxId, _Bool server, const char *servername, void *inv, void *clo) {
	int ci = (int)ctxId;
	int fd = __kml_tcp_fd(handle);
	if (ci < 0 || ci >= tls_ctx_n || fd < 0) return -EBADF;
	__kml_tcp_set_tls_ops(&tls_sess_ops);
	kml_tls_sess *s = (kml_tls_sess *)calloc(1, sizeof *s);
	s->ssl = SSL_new(tls_ctx_tab[ci]);
	s->server = server;
	tls_attach_fd(s->ssl, fd);
	if (!server) {
		SSL_set_verify(s->ssl, SSL_VERIFY_NONE, NULL);
		if (servername && *servername) {
			s->servername = strdup(servername);
			SSL_set_tlsext_host_name(s->ssl, servername);
			X509_VERIFY_PARAM *param = SSL_get0_param(s->ssl);
			X509_VERIFY_PARAM_set_hostflags(param, X509_CHECK_FLAG_NO_PARTIAL_WILDCARDS);
			if (!X509_VERIFY_PARAM_set1_host(param, servername, 0)) X509_VERIFY_PARAM_set1_ip_asc(param, servername);
		}
		SSL_set_connect_state(s->ssl);
	} else {
		SSL_set_accept_state(s->ssl);
	}
	return __kml_tcp_attach_tls(handle, s, inv, clo);
}

// 0 authorized (1/0), 1 the verify error's code, 2 its message, 3 the failed
// handshake's reason, 4 the negotiated ALPN protocol, 5 the protocol
// version, 6 the cipher's name. A string is the last string; a number is
// returned.
double __kml_native_tls_info(double handle, double which) {
	kml_tls_sess *s = (kml_tls_sess *)__kml_tcp_tls(handle);
	extern void __kml_native_set_last_string(const char *);
	if (!s) {
		__kml_native_set_last_string("");
		return 0;
	}
	switch ((int)which) {
	case 0:
		return s->verify == X509_V_OK;
	case 1: {
		const char *code = "UNABLE_TO_VERIFY_LEAF_SIGNATURE";
		switch (s->verify) {
		case X509_V_OK: code = ""; break;
		case X509_V_ERR_DEPTH_ZERO_SELF_SIGNED_CERT: code = "DEPTH_ZERO_SELF_SIGNED_CERT"; break;
		case X509_V_ERR_SELF_SIGNED_CERT_IN_CHAIN: code = "SELF_SIGNED_CERT_IN_CHAIN"; break;
		case X509_V_ERR_UNABLE_TO_GET_ISSUER_CERT_LOCALLY: code = "UNABLE_TO_GET_ISSUER_CERT_LOCALLY"; break;
		case X509_V_ERR_CERT_HAS_EXPIRED: code = "CERT_HAS_EXPIRED"; break;
		case X509_V_ERR_CERT_NOT_YET_VALID: code = "CERT_NOT_YET_VALID"; break;
		case X509_V_ERR_HOSTNAME_MISMATCH: code = "ERR_TLS_CERT_ALTNAME_INVALID"; break;
		case X509_V_ERR_IP_ADDRESS_MISMATCH: code = "ERR_TLS_CERT_ALTNAME_INVALID"; break;
		}
		__kml_native_set_last_string(code);
		return 0;
	}
	case 2: {
		const char *m = s->verify == X509_V_OK ? "" : X509_verify_cert_error_string(s->verify);
		int system_ca_hint = 1;
		if (s->verify == X509_V_ERR_DEPTH_ZERO_SELF_SIGNED_CERT) m = "self-signed certificate";
		else if (s->verify == X509_V_ERR_SELF_SIGNED_CERT_IN_CHAIN) m = "self-signed certificate in certificate chain";
		else if (s->verify == X509_V_ERR_UNABLE_TO_GET_ISSUER_CERT_LOCALLY) m = "unable to get local issuer certificate";
		else if (s->verify == X509_V_ERR_UNABLE_TO_VERIFY_LEAF_SIGNATURE) m = "unable to verify the first certificate";
		else system_ca_hint = 0;
		// Node's hint for the errors a system CA would cure.
		char buf[256];
		snprintf(buf, sizeof buf, "%s%s", m, system_ca_hint ? "; if the root CA is installed locally, try running Node.js with --use-system-ca" : "");
		__kml_native_set_last_string(buf);
		return 0;
	}
	case 3:
		__kml_native_set_last_string(s->err);
		return 0;
	case 4: {
		const unsigned char *p = NULL;
		unsigned int n = 0;
		SSL_get0_alpn_selected(s->ssl, &p, &n);
		char buf[256] = {0};
		if (p && n < sizeof buf) memcpy(buf, p, n);
		__kml_native_set_last_string(buf);
		return n;
	}
	case 5:
		__kml_native_set_last_string(SSL_get_version(s->ssl));
		return 0;
	case 6: {
		const SSL_CIPHER *c = SSL_get_current_cipher(s->ssl);
		__kml_native_set_last_string(c ? SSL_CIPHER_get_name(c) : "");
		return 0;
	}
	}
	return 0;
}
