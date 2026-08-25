// tls.c — the OpenSSL (libssl) client helper backing the `tls` module
// (TDD-00109). Compiled alongside the program (and linked -lssl -lcrypto) only
// when the program uses `tls`, exactly like the crypto backend's C file. Keeps
// all SSL_*/openssl headers out of the emitted LLVM IR; the runtime calls this
// tiny ABI:
//
//   void* __kml_tls_client_connect(int fd, const char* host,
//                                  int reject_unauthorized, char** errout);
//   long  __kml_tls_read (void* ssl, void* buf, long n);   //  >0 bytes · 0 close · <0 none
//   long  __kml_tls_write(void* ssl, const void* buf, long n);
//   void  __kml_tls_free (void* ssl);
//
// The TCP connect is done by the caller (the net client path); this wraps the
// already-connected fd in TLS. A blocking SSL_connect handshake (matching
// net.connect's blocking connect) — the fd is switched to non-blocking by the
// caller only after this returns.

#include <openssl/ssl.h>
#include <openssl/err.h>
#include <openssl/x509v3.h>
#include <openssl/pem.h>
#include <openssl/bio.h>
#include <fcntl.h>
#include <stdlib.h>
#include <string.h>

static SSL_CTX *g_ctx = NULL;

static SSL_CTX *tls_ctx(void) {
	if (!g_ctx) {
		g_ctx = SSL_CTX_new(TLS_client_method());
		if (g_ctx) {
			SSL_CTX_set_default_verify_paths(g_ctx);
			SSL_CTX_set_min_proto_version(g_ctx, TLS1_2_VERSION);
		}
	}
	return g_ctx;
}

static char *dup_err(const char *fallback) {
	unsigned long e = ERR_get_error();
	if (e != 0) {
		char buf[256];
		ERR_error_string_n(e, buf, sizeof(buf));
		return strdup(buf);
	}
	return strdup(fallback);
}

// __kml_tls_client_connect wraps a connected fd in TLS. Returns the SSL* on
// success; on failure returns NULL and (if errout) stores a malloc'd message.
void *__kml_tls_client_connect(int fd, const char *host, int reject_unauthorized, char **errout) {
	SSL_CTX *ctx = tls_ctx();
	if (!ctx) {
		if (errout) *errout = dup_err("TLS: SSL_CTX_new failed");
		return NULL;
	}
	SSL *ssl = SSL_new(ctx);
	if (!ssl) {
		if (errout) *errout = dup_err("TLS: SSL_new failed");
		return NULL;
	}
	SSL_set_fd(ssl, fd);
	// SNI — send the server name in the handshake (many hosts require it).
	SSL_set_tlsext_host_name(ssl, host);
	if (reject_unauthorized) {
		// Verify the peer certificate and that it matches `host`.
		SSL_set1_host(ssl, host);
		SSL_set_verify(ssl, SSL_VERIFY_PEER, NULL);
	}
	if (SSL_connect(ssl) != 1) {
		if (errout) *errout = dup_err("TLS handshake failed");
		SSL_free(ssl);
		return NULL;
	}
	if (reject_unauthorized && SSL_get_verify_result(ssl) != X509_V_OK) {
		if (errout) *errout = strdup("TLS certificate verification failed");
		SSL_free(ssl);
		return NULL;
	}
	return ssl;
}

// __kml_tls_read: >0 = bytes read; 0 = clean TLS close_notify; <0 = no data now
// (WANT_READ/WANT_WRITE) or a fatal error — the net dispatch treats <0 like a
// non-blocking EAGAIN and moves on.
long __kml_tls_read(void *ssl, void *buf, long n) {
	int r = SSL_read((SSL *)ssl, buf, (int)n);
	if (r > 0) return r;
	int e = SSL_get_error((SSL *)ssl, r);
	if (e == SSL_ERROR_ZERO_RETURN) return 0;
	return -1;
}

long __kml_tls_write(void *ssl, const void *buf, long n) {
	int r = SSL_write((SSL *)ssl, buf, (int)n);
	return r > 0 ? r : -1;
}

void __kml_tls_free(void *ssl) {
	if (ssl) SSL_free((SSL *)ssl);
}

// --- server (tls.createServer, TDD-00110) ---

// alpn_select_cb negotiates ALPN on the server side (TDD-00111 Stage 2): prefer
// "h2" (HTTP/2), else "http/1.1", scanning the client's length-prefixed protocol
// list. Returns NOACK when neither is offered (the connection proceeds without a
// negotiated protocol — a plain TLS line-protocol client that offers no ALPN
// never triggers this, so tls.createServer is behavior-unchanged for them).
static int alpn_select_cb(SSL *ssl, const unsigned char **out, unsigned char *outlen,
                          const unsigned char *in, unsigned int inlen, void *arg) {
	(void)ssl;
	(void)arg;
	for (unsigned int i = 0; i + 1 <= inlen;) {
		unsigned int l = in[i];
		if (i + 1 + l > inlen) break;
		if (l == 2 && memcmp(&in[i + 1], "h2", 2) == 0) {
			*out = &in[i + 1];
			*outlen = 2;
			return SSL_TLSEXT_ERR_OK;
		}
		i += 1 + l;
	}
	for (unsigned int i = 0; i + 1 <= inlen;) {
		unsigned int l = in[i];
		if (i + 1 + l > inlen) break;
		if (l == 8 && memcmp(&in[i + 1], "http/1.1", 8) == 0) {
			*out = &in[i + 1];
			*outlen = 8;
			return SSL_TLSEXT_ERR_OK;
		}
		i += 1 + l;
	}
	return SSL_TLSEXT_ERR_NOACK;
}

// __kml_tls_alpn_selected returns the negotiated ALPN protocol (e.g. "h2" or
// "http/1.1") as a malloc'd NUL-terminated string, or NULL if none was
// negotiated. The caller frees it. Lets the accept path branch h2 vs 1.1 after
// the handshake (TDD-00111 Stage 2).
char *__kml_tls_alpn_selected(void *ssl) {
	const unsigned char *proto = NULL;
	unsigned int len = 0;
	SSL_get0_alpn_selected((SSL *)ssl, &proto, &len);
	if (!proto || len == 0) return NULL;
	char *s = malloc((size_t)len + 1);
	if (!s) return NULL;
	memcpy(s, proto, len);
	s[len] = '\0';
	return s;
}

// __kml_tls_server_ctx builds a server SSL_CTX from a PEM certificate and
// private key (as strings, the way Node's { cert, key } are). Returns the ctx,
// or NULL + a message in *errout on failure.
void *__kml_tls_server_ctx(const char *cert_pem, const char *key_pem, char **errout) {
	SSL_CTX *ctx = SSL_CTX_new(TLS_server_method());
	if (!ctx) {
		if (errout) *errout = dup_err("TLS: SSL_CTX_new (server) failed");
		return NULL;
	}
	SSL_CTX_set_min_proto_version(ctx, TLS1_2_VERSION);
	SSL_CTX_set_alpn_select_cb(ctx, alpn_select_cb, NULL);
	BIO *cbio = BIO_new_mem_buf(cert_pem, -1);
	X509 *cert = cbio ? PEM_read_bio_X509(cbio, NULL, NULL, NULL) : NULL;
	if (cbio) BIO_free(cbio);
	if (!cert || SSL_CTX_use_certificate(ctx, cert) != 1) {
		if (errout) *errout = dup_err("TLS: invalid server certificate");
		if (cert) X509_free(cert);
		SSL_CTX_free(ctx);
		return NULL;
	}
	X509_free(cert);
	BIO *kbio = BIO_new_mem_buf(key_pem, -1);
	EVP_PKEY *pkey = kbio ? PEM_read_bio_PrivateKey(kbio, NULL, NULL, NULL) : NULL;
	if (kbio) BIO_free(kbio);
	if (!pkey || SSL_CTX_use_PrivateKey(ctx, pkey) != 1) {
		if (errout) *errout = dup_err("TLS: invalid server private key");
		if (pkey) EVP_PKEY_free(pkey);
		SSL_CTX_free(ctx);
		return NULL;
	}
	EVP_PKEY_free(pkey);
	return ctx;
}

// __kml_tls_server_accept wraps a freshly-accepted (blocking) fd in TLS: a
// blocking SSL_accept server handshake. Returns the SSL* or NULL on failure.
void *__kml_tls_server_accept(void *ctx, int fd) {
	// The accepted fd may have inherited O_NONBLOCK from the non-blocking listen
	// socket (platform-dependent); force it blocking for the handshake — the
	// caller (the net accept path) sets it non-blocking again afterwards.
	int fl = fcntl(fd, F_GETFL, 0);
	if (fl >= 0) fcntl(fd, F_SETFL, fl & ~O_NONBLOCK);
	SSL *ssl = SSL_new((SSL_CTX *)ctx);
	if (!ssl) return NULL;
	SSL_set_fd(ssl, fd);
	if (SSL_accept(ssl) != 1) {
		SSL_free(ssl);
		return NULL;
	}
	return ssl;
}
