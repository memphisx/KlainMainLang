/* crypto_openssl.c — the OpenSSL (libcrypto 3.x) implementation of the
 * __kml_crypto_* subtle-crypto ABI (TDD-00104). EVP-family APIs only; no
 * deprecated 1.x calls. Error contract shared by every backend:
 *   0 = ok, -1 = OperationError, -2 = DataError, -3 = NotSupportedError.
 * Variable-size outputs are malloc'd here and handed to the program; the
 * compiled program frees (or its GC collects) them like any other
 * allocation. */

#include <stddef.h>
#include <stdlib.h>
#include <string.h>
#include <openssl/evp.h>
#include <openssl/params.h>
#include <openssl/param_build.h>
#include <openssl/core_names.h>
#include <openssl/ec.h>
#include <openssl/kdf.h>
#include <openssl/x509.h>
#include <openssl/rsa.h>
#include <openssl/dsa.h>

/* base64url (RFC 4648 §5, no padding) — the JWK `k`/component codec. Kept
 * in the backend file (duplicated across backends) so each stays a single
 * self-contained TU. */
static const char kml_b64u[] =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";

long long __kml_crypto_b64url_encode(const unsigned char *in, long long len,
                                     char **out, long long *outLen) {
    long long olen = (len + 2) / 3 * 4, i, o = 0;
    char *buf = (char *)malloc((size_t)olen + 1);
    if (!buf) return -1;
    for (i = 0; i + 2 < len; i += 3) {
        unsigned v = (unsigned)in[i] << 16 | (unsigned)in[i + 1] << 8 | in[i + 2];
        buf[o++] = kml_b64u[v >> 18];
        buf[o++] = kml_b64u[(v >> 12) & 63];
        buf[o++] = kml_b64u[(v >> 6) & 63];
        buf[o++] = kml_b64u[v & 63];
    }
    if (i < len) {
        unsigned v = (unsigned)in[i] << 16;
        if (i + 1 < len) v |= (unsigned)in[i + 1] << 8;
        buf[o++] = kml_b64u[v >> 18];
        buf[o++] = kml_b64u[(v >> 12) & 63];
        if (i + 1 < len) buf[o++] = kml_b64u[(v >> 6) & 63];
    }
    buf[o] = 0;
    *out = buf;
    *outLen = o;
    return 0;
}

static int kml_b64u_val(char c) {
    if (c >= 'A' && c <= 'Z') return c - 'A';
    if (c >= 'a' && c <= 'z') return c - 'a' + 26;
    if (c >= '0' && c <= '9') return c - '0' + 52;
    if (c == '-') return 62;
    if (c == '_') return 63;
    return -1;
}

long long __kml_crypto_b64url_decode(const char *in, long long len,
                                     unsigned char **out, long long *outLen) {
    unsigned char *buf;
    long long i, o = 0;
    unsigned acc = 0;
    int bits = 0;
    if (len % 4 == 1) return -2;
    buf = (unsigned char *)malloc((size_t)(len / 4 * 3 + 3) + 1);
    if (!buf) return -1;
    for (i = 0; i < len; i++) {
        int v = kml_b64u_val(in[i]);
        if (v < 0) { free(buf); return -2; }
        acc = acc << 6 | (unsigned)v;
        bits += 6;
        if (bits >= 8) {
            bits -= 8;
            buf[o++] = (unsigned char)(acc >> bits);
        }
    }
    *out = buf;
    *outLen = o;
    return 0;
}

/* Streaming HMAC — crypto.createHmac's Hmac object (ADR-00637). Wrap the
 * EVP_MAC and its ctx so both free together at final(). */
struct kml_hmac_stream { EVP_MAC *mac; EVP_MAC_CTX *ctx; };

/* ── key derivation: PBKDF2 / HKDF ────────────────────────────────────────── */

/* ── asymmetric: RSA-OAEP / RSA-PSS / ECDSA + key formats (TDD-00104) ─────── */

static const char *kml_curve_name(long long curveId) {
    switch (curveId) {
    case 1: return "P-256";
    case 2: return "P-384";
    case 3: return "P-521";
    }
    return NULL;
}

static long long kml_curve_bytes(long long curveId) {
    switch (curveId) {
    case 1: return 32;
    case 2: return 48;
    case 3: return 66;
    }
    return 0;
}

/* DER-encode pkey as PKCS#8 (private) and SPKI (public) into malloc'd bufs. */
static long long kml_pkey_to_der(EVP_PKEY *pkey, unsigned char **pkcs8,
                                 long long *pkcs8Len, unsigned char **spki,
                                 long long *spkiLen) {
    unsigned char *p;
    int n;
    if (pkcs8) {
        PKCS8_PRIV_KEY_INFO *p8 = EVP_PKEY2PKCS8(pkey);
        if (!p8) return -1;
        n = i2d_PKCS8_PRIV_KEY_INFO(p8, NULL);
        if (n <= 0) { PKCS8_PRIV_KEY_INFO_free(p8); return -1; }
        *pkcs8 = (unsigned char *)malloc((size_t)n);
        p = *pkcs8;
        i2d_PKCS8_PRIV_KEY_INFO(p8, &p);
        *pkcs8Len = n;
        PKCS8_PRIV_KEY_INFO_free(p8);
    }
    if (spki) {
        n = i2d_PUBKEY(pkey, NULL);
        if (n <= 0) return -1;
        *spki = (unsigned char *)malloc((size_t)n);
        p = *spki;
        i2d_PUBKEY(pkey, &p);
        *spkiLen = n;
    }
    return 0;
}

/* Parse a key from the CryptoKey header's DER (PKCS#8 or SPKI per kind). */
static EVP_PKEY *kml_pkey_from_der(const unsigned char *der, long long derLen,
                                   long long isPriv) {
    const unsigned char *p = der;
    if (isPriv) {
        PKCS8_PRIV_KEY_INFO *p8 = d2i_PKCS8_PRIV_KEY_INFO(NULL, &p, (long)derLen);
        EVP_PKEY *pkey;
        if (!p8) return NULL;
        pkey = EVP_PKCS82PKEY(p8);
        PKCS8_PRIV_KEY_INFO_free(p8);
        return pkey;
    }
    return d2i_PUBKEY(NULL, &p, (long)derLen);
}

/* ── JWK component bridge: DER ↔ base64url component strings ──────────────
 * The emitter surfaces JWKs as Map<string,string>; the shim converts between
 * the key DER and malloc'd base64url component strings (NULL when absent). */

long long __kml_crypto_b64url_encode(const unsigned char *, long long,
                                     char **, long long *);
long long __kml_crypto_b64url_decode(const char *, long long,
                                     unsigned char **, long long *);

static char *kml_bn_b64u(const BIGNUM *bn) {
    int n = BN_num_bytes(bn);
    unsigned char *tmp;
    char *out = NULL;
    long long outLen;
    if (n <= 0) n = 1;
    tmp = (unsigned char *)malloc((size_t)n);
    if (!tmp) return NULL;
    BN_bn2binpad(bn, tmp, n);
    if (__kml_crypto_b64url_encode(tmp, n, &out, &outLen) != 0) out = NULL;
    free(tmp);
    return out;
}

static char *kml_pkey_bn_b64u(EVP_PKEY *pkey, const char *param) {
    BIGNUM *bn = NULL;
    char *out;
    if (!EVP_PKEY_get_bn_param(pkey, param, &bn) || !bn) return NULL;
    out = kml_bn_b64u(bn);
    BN_free(bn);
    return out;
}

static BIGNUM *kml_b64u_bn(const char *s) {
    unsigned char *bytes;
    long long len;
    BIGNUM *bn;
    if (!s) return NULL;
    if (__kml_crypto_b64url_decode(s, (long long)strlen(s), &bytes, &len) != 0)
        return NULL;
    bn = BN_bin2bn(bytes, (int)len, NULL);
    free(bytes);
    return bn;
}

long long __kml_crypto_jwk_import_rsa(const char *n, const char *e,
                                      const char *d, const char *p,
                                      const char *q, const char *dp,
                                      const char *dq, const char *qi,
                                      unsigned char **der, long long *derLen,
                                      long long *kindOut) {
    OSSL_PARAM_BLD *bld = OSSL_PARAM_BLD_new();
    OSSL_PARAM *params = NULL;
    EVP_PKEY_CTX *ctx = NULL;
    EVP_PKEY *pkey = NULL;
    BIGNUM *bn[8] = {0};
    int isPriv = d != NULL;
    long long rc = -2;
    int ok = 1, i;
    static const char *names[8] = {
        OSSL_PKEY_PARAM_RSA_N, OSSL_PKEY_PARAM_RSA_E, OSSL_PKEY_PARAM_RSA_D,
        OSSL_PKEY_PARAM_RSA_FACTOR1, OSSL_PKEY_PARAM_RSA_FACTOR2,
        OSSL_PKEY_PARAM_RSA_EXPONENT1, OSSL_PKEY_PARAM_RSA_EXPONENT2,
        OSSL_PKEY_PARAM_RSA_COEFFICIENT1
    };
    const char *vals[8];
    vals[0] = n; vals[1] = e; vals[2] = d; vals[3] = p;
    vals[4] = q; vals[5] = dp; vals[6] = dq; vals[7] = qi;
    if (!bld || !n || !e) { OSSL_PARAM_BLD_free(bld); return -2; }
    for (i = 0; i < (isPriv ? 8 : 2); i++) {
        if (!vals[i]) continue;
        bn[i] = kml_b64u_bn(vals[i]);
        if (!bn[i] || !OSSL_PARAM_BLD_push_BN(bld, names[i], bn[i])) ok = 0;
    }
    if (ok) {
        params = OSSL_PARAM_BLD_to_param(bld);
        ctx = EVP_PKEY_CTX_new_from_name(NULL, "RSA", NULL);
        if (ctx && params && EVP_PKEY_fromdata_init(ctx) > 0 &&
            EVP_PKEY_fromdata(ctx, &pkey, isPriv ? EVP_PKEY_KEYPAIR
                                                 : EVP_PKEY_PUBLIC_KEY,
                              params) > 0 && pkey) {
            rc = isPriv ? kml_pkey_to_der(pkey, der, derLen, NULL, NULL)
                        : kml_pkey_to_der(pkey, NULL, NULL, der, derLen);
            *kindOut = isPriv ? 2 : 1;
        }
    }
    EVP_PKEY_free(pkey);
    EVP_PKEY_CTX_free(ctx);
    OSSL_PARAM_free(params);
    OSSL_PARAM_BLD_free(bld);
    for (i = 0; i < 8; i++) BN_free(bn[i]);
    return rc;
}

long long __kml_crypto_jwk_import_ec(long long curveId, const char *x,
                                     const char *y, const char *d,
                                     unsigned char **der, long long *derLen,
                                     long long *kindOut) {
    const char *curve = kml_curve_name(curveId);
    long long cb = kml_curve_bytes(curveId);
    unsigned char *xb = NULL, *yb = NULL, *db = NULL, *pt = NULL;
    long long xl, yl, dl;
    OSSL_PARAM_BLD *bld = NULL;
    OSSL_PARAM *params = NULL;
    EVP_PKEY_CTX *ctx = NULL;
    EVP_PKEY *pkey = NULL;
    BIGNUM *priv = NULL;
    long long rc = -2;
    if (!curve || !x || !y) return -2;
    if (__kml_crypto_b64url_decode(x, (long long)strlen(x), &xb, &xl) != 0 ||
        __kml_crypto_b64url_decode(y, (long long)strlen(y), &yb, &yl) != 0 ||
        xl != cb || yl != cb)
        goto done;
    pt = (unsigned char *)malloc((size_t)(1 + 2 * cb));
    if (!pt) goto done;
    pt[0] = 4;
    memcpy(pt + 1, xb, (size_t)cb);
    memcpy(pt + 1 + cb, yb, (size_t)cb);
    bld = OSSL_PARAM_BLD_new();
    if (!bld) goto done;
    OSSL_PARAM_BLD_push_utf8_string(bld, OSSL_PKEY_PARAM_GROUP_NAME, curve, 0);
    OSSL_PARAM_BLD_push_octet_string(bld, OSSL_PKEY_PARAM_PUB_KEY, pt,
                                     (size_t)(1 + 2 * cb));
    if (d) {
        if (__kml_crypto_b64url_decode(d, (long long)strlen(d), &db, &dl) != 0 ||
            dl != cb)
            goto done;
        priv = BN_bin2bn(db, (int)cb, NULL);
        if (!priv || !OSSL_PARAM_BLD_push_BN(bld, OSSL_PKEY_PARAM_PRIV_KEY, priv))
            goto done;
    }
    params = OSSL_PARAM_BLD_to_param(bld);
    ctx = EVP_PKEY_CTX_new_from_name(NULL, "EC", NULL);
    if (ctx && params && EVP_PKEY_fromdata_init(ctx) > 0 &&
        EVP_PKEY_fromdata(ctx, &pkey, d ? EVP_PKEY_KEYPAIR : EVP_PKEY_PUBLIC_KEY,
                          params) > 0 && pkey) {
        rc = d ? kml_pkey_to_der(pkey, der, derLen, NULL, NULL)
               : kml_pkey_to_der(pkey, NULL, NULL, der, derLen);
        *kindOut = d ? 2 : 1;
    }
done:
    EVP_PKEY_free(pkey);
    EVP_PKEY_CTX_free(ctx);
    OSSL_PARAM_free(params);
    OSSL_PARAM_BLD_free(bld);
    BN_free(priv);
    free(xb); free(yb); free(db); free(pt);
    return rc;
}

/* ---- node:crypto natives (lib/node/crypto.ts) ---------------------------------
 * Node's crypto module over libcrypto, as node_crypto's bindings are: hash and
 * HMAC handles by name, ciphers, the KDFs, random fill, key-pair generation and
 * PEM-key signing. A handle is an id into a table the module frees. A pooled
 * form runs its work on the thread pool (__kml_pool_job) and calls back with
 * (status, result) on the loop thread; its output region stays reachable
 * through the callback's closure. */

#include <stdint.h>
#include <stdio.h>
#include <openssl/rand.h>
#include <openssl/pem.h>
#include <openssl/bio.h>
#include <openssl/err.h>
#include <openssl/objects.h>

extern void __kml_pool_job(void (*work)(void *, long long *, double *), void *job, void *inv, void *clo);
extern char *__kml_str_alloc(long long n);
extern void __kml_str_finalize(char *s);

static char *kml_ncrypto_str(const char *s) {
    long long n = (long long)strlen(s);
    char *out = __kml_str_alloc(n + 1);
    memcpy(out, s, (size_t)n + 1);
    __kml_str_finalize(out);
    return out;
}

/* A hash, an HMAC or a cipher, by id. */
enum { KNC_FREE = 0, KNC_HASH, KNC_HMAC, KNC_CIPHER };
typedef struct {
    int kind;
    EVP_MD_CTX *md;
    EVP_MAC_CTX *mac;
    EVP_CIPHER_CTX *cipher;
    size_t xof_len;   /* a hash's outputLength (XOF), or 0 */
} knc_handle;

static knc_handle *knc_tab = NULL;
static long long knc_cap = 0;

static long long knc_alloc(void) {
    for (long long i = 0; i < knc_cap; i++)
        if (knc_tab[i].kind == KNC_FREE) return i;
    long long ncap = knc_cap ? knc_cap * 2 : 16;
    knc_handle *t = (knc_handle *)realloc(knc_tab, (size_t)ncap * sizeof *t);
    if (!t) return -1;
    memset(t + knc_cap, 0, (size_t)(ncap - knc_cap) * sizeof *t);
    long long id = knc_cap;
    knc_tab = t;
    knc_cap = ncap;
    return id;
}

static knc_handle *knc_get(double id) {
    long long i = (long long)id;
    if (i < 0 || i >= knc_cap || knc_tab[i].kind == KNC_FREE) return NULL;
    return &knc_tab[i];
}

static void knc_release(knc_handle *h) {
    if (h->md) EVP_MD_CTX_free(h->md);
    if (h->mac) EVP_MAC_CTX_free(h->mac);
    if (h->cipher) EVP_CIPHER_CTX_free(h->cipher);
    memset(h, 0, sizeof *h);
}

void __kml_native_crypto_free(double id) {
    knc_handle *h = knc_get(id);
    if (h) knc_release(h);
}

/* createHash(algorithm[, { outputLength }]): the id, or -1 for a digest
 * libcrypto does not know. */
double __kml_native_crypto_hash_new(const char *alg, double xofLen) {
    const EVP_MD *md = EVP_get_digestbyname(alg ? alg : "");
    if (!md) return -1;
    long long id = knc_alloc();
    if (id < 0) return -1;
    EVP_MD_CTX *ctx = EVP_MD_CTX_new();
    if (!ctx || !EVP_DigestInit_ex(ctx, md, NULL)) {
        EVP_MD_CTX_free(ctx);
        return -1;
    }
    knc_tab[id].kind = KNC_HASH;
    knc_tab[id].md = ctx;
    knc_tab[id].xof_len = xofLen > 0 ? (size_t)xofLen : 0;
    return (double)id;
}

/* createHmac(algorithm, key): the id, or -1 for an unknown digest. */
double __kml_native_crypto_hmac_new(const char *alg, void *key, long long keyLen) {
    if (!alg || !EVP_get_digestbyname(alg)) return -1;
    EVP_MAC *mac = EVP_MAC_fetch(NULL, "HMAC", NULL);
    if (!mac) return -1;
    EVP_MAC_CTX *ctx = EVP_MAC_CTX_new(mac);
    EVP_MAC_free(mac);
    if (!ctx) return -1;
    const EVP_MD *md = EVP_get_digestbyname(alg);
    OSSL_PARAM params[2];
    params[0] = OSSL_PARAM_construct_utf8_string(OSSL_MAC_PARAM_DIGEST, (char *)EVP_MD_get0_name(md), 0);
    params[1] = OSSL_PARAM_construct_end();
    static const unsigned char empty = 0;
    if (!EVP_MAC_init(ctx, keyLen > 0 ? (const unsigned char *)key : &empty, (size_t)keyLen, params)) {
        EVP_MAC_CTX_free(ctx);
        return -1;
    }
    long long id = knc_alloc();
    if (id < 0) {
        EVP_MAC_CTX_free(ctx);
        return -1;
    }
    knc_tab[id].kind = KNC_HMAC;
    knc_tab[id].mac = ctx;
    return (double)id;
}

double __kml_native_crypto_hash_update(double id, void *data, long long len) {
    knc_handle *h = knc_get(id);
    if (!h) return 0;
    if (h->kind == KNC_HASH) return EVP_DigestUpdate(h->md, data, (size_t)len) ? 1 : 0;
    if (h->kind == KNC_HMAC) return EVP_MAC_update(h->mac, data, (size_t)len) ? 1 : 0;
    return 0;
}

/* The digest's size: an XOF's requested length, else the algorithm's. */
double __kml_native_crypto_hash_size(double id) {
    knc_handle *h = knc_get(id);
    if (!h) return 0;
    if (h->kind == KNC_HASH) {
        if (h->xof_len) return (double)h->xof_len;
        return (double)EVP_MD_CTX_get_size(h->md);
    }
    if (h->kind == KNC_HMAC) return (double)EVP_MAC_CTX_get_mac_size(h->mac);
    return 0;
}

/* The digest into out; its length, or -1. The handle is spent. */
double __kml_native_crypto_hash_digest(double id, void *out, long long outLen) {
    knc_handle *h = knc_get(id);
    if (!h) return -1;
    double r = -1;
    if (h->kind == KNC_HASH) {
        if (h->xof_len) {
            if ((long long)h->xof_len <= outLen && EVP_DigestFinalXOF(h->md, out, h->xof_len)) r = (double)h->xof_len;
        } else {
            unsigned char buf[EVP_MAX_MD_SIZE];
            unsigned int n = 0;
            if (EVP_DigestFinal_ex(h->md, buf, &n) && (long long)n <= outLen) {
                memcpy(out, buf, n);
                r = n;
            }
        }
    } else if (h->kind == KNC_HMAC) {
        unsigned char buf[EVP_MAX_MD_SIZE];
        size_t n = 0;
        if (EVP_MAC_final(h->mac, buf, &n, sizeof buf) && (long long)n <= outLen) {
            memcpy(out, buf, n);
            r = (double)n;
        }
    }
    knc_release(h);
    return r;
}

/* hash.copy(): a new handle in the same state, or -1. */
double __kml_native_crypto_hash_copy(double id) {
    knc_handle *h = knc_get(id);
    if (!h || h->kind != KNC_HASH) return -1;
    size_t xof = h->xof_len;
    EVP_MD_CTX *ctx = EVP_MD_CTX_new();
    if (!ctx || !EVP_MD_CTX_copy_ex(ctx, h->md)) {
        EVP_MD_CTX_free(ctx);
        return -1;
    }
    long long nid = knc_alloc();
    if (nid < 0) {
        EVP_MD_CTX_free(ctx);
        return -1;
    }
    knc_tab[nid].kind = KNC_HASH;
    knc_tab[nid].md = ctx;
    knc_tab[nid].xof_len = xof;
    return (double)nid;
}

/* getHashes()/getCiphers(): the names, joined by ','. */
typedef struct { char *buf; size_t len, cap; } knc_names;

static void knc_names_add(knc_names *n, const char *name) {
    size_t l = strlen(name);
    if (n->len + l + 2 > n->cap) {
        size_t nc = (n->cap ? n->cap * 2 : 1024) + l;
        char *b = (char *)realloc(n->buf, nc);
        if (!b) return;
        n->buf = b;
        n->cap = nc;
    }
    if (n->len) n->buf[n->len++] = ',';
    memcpy(n->buf + n->len, name, l);
    n->len += l;
    n->buf[n->len] = 0;
}

/* Node's array_push_back (crypto_util.cc): a name is listed when the
 * algorithm it names can be fetched from a provider (an alias is fetched by
 * its real name). */
static void knc_md_push(const EVP_MD *m, const char *from, const char *to, void *arg) {
    (void)m; (void)to;
    if (!from) return;
    const EVP_MD *real = EVP_get_digestbyname(from);
    const char *name = real ? EVP_MD_get0_name(real) : NULL;
    EVP_MD *fetched = name ? EVP_MD_fetch(NULL, name, NULL) : NULL;
    if (!fetched) { ERR_clear_error(); return; }
    EVP_MD_free(fetched);
    knc_names_add((knc_names *)arg, from);
}

static void knc_cipher_push(const EVP_CIPHER *c, const char *from, const char *to, void *arg) {
    (void)c; (void)to;
    if (!from) return;
    const EVP_CIPHER *real = EVP_get_cipherbyname(from);
    const char *name = real ? EVP_CIPHER_get0_name(real) : NULL;
    EVP_CIPHER *fetched = name ? EVP_CIPHER_fetch(NULL, name, NULL) : NULL;
    if (!fetched) { ERR_clear_error(); return; }
    EVP_CIPHER_free(fetched);
    knc_names_add((knc_names *)arg, from);
}

/* which: 0 digests, 1 ciphers, 2 the linked OpenSSL's version number, 3
 * the built-in EC curves' short names. */
char *__kml_native_crypto_names(double which) {
    if (which == 2) {
        char v[32];
        snprintf(v, sizeof v, "%lu", (unsigned long)OpenSSL_version_num());
        return kml_ncrypto_str(v);
    }
    knc_names n = {0};
    if (which == 3) {
        size_t count = EC_get_builtin_curves(NULL, 0);
        EC_builtin_curve *curves = (EC_builtin_curve *)malloc(sizeof(EC_builtin_curve) * (count ? count : 1));
        EC_get_builtin_curves(curves, count);
        for (size_t i = 0; i < count; i++) {
            const char *sn = OBJ_nid2sn(curves[i].nid);
            if (sn) knc_names_add(&n, sn);
        }
        free(curves);
        char *out = kml_ncrypto_str(n.buf ? n.buf : "");
        free(n.buf);
        return out;
    }
    if (which == 0) EVP_MD_do_all_sorted(knc_md_push, &n);
    else EVP_CIPHER_do_all_sorted(knc_cipher_push, &n);
    char *out = kml_ncrypto_str(n.buf ? n.buf : "");
    free(n.buf);
    return out;
}

/* crypto.randomFillSync: size bytes of the CSPRNG at offset. */
double __kml_native_crypto_random_fill(void *data, long long size, double offset, double length) {
    long long off = (long long)offset, len = (long long)length;
    if (off < 0 || len < 0 || off + len > size) return -1;
    return RAND_bytes((unsigned char *)data + off, (int)len) == 1 ? 0 : -1;
}

typedef struct { unsigned char *p; long long len; } knc_rand_job;

static void knc_rand_work(void *job, long long *err, double *res) {
    knc_rand_job *j = (knc_rand_job *)job;
    *err = RAND_bytes(j->p, (int)j->len) == 1 ? 0 : 1;
    *res = 0;
    free(j);
}

void __kml_native_crypto_random_fill_async(void *data, long long size, double offset, double length, void *inv, void *clo) {
    long long off = (long long)offset, len = (long long)length;
    knc_rand_job *j = (knc_rand_job *)malloc(sizeof *j);
    if (off < 0 || len < 0 || off + len > size) len = 0, off = 0;
    j->p = (unsigned char *)data + off;
    j->len = len;
    __kml_pool_job(knc_rand_work, j, inv, clo);
}

/* ---- key derivation: pbkdf2, scrypt, hkdf (sync or pooled) ---------------- */
enum { KNC_PBKDF2 = 0, KNC_SCRYPT, KNC_HKDF };
typedef struct {
    int op;
    unsigned char *a, *b, *c;  /* password/salt/(info), copied */
    long long alen, blen, clen;
    double n1, n2, n3, n4;     /* iterations | N r p maxmem */
    char digest[64];
    unsigned char *out;
    long long outlen;
} knc_kdf;

static long long knc_kdf_run(knc_kdf *k) {
    switch (k->op) {
    case KNC_PBKDF2: {
        const EVP_MD *md = EVP_get_digestbyname(k->digest);
        if (!md) return 2;
        return PKCS5_PBKDF2_HMAC((const char *)k->a, (int)k->alen, k->b, (int)k->blen, (int)k->n1, md, (int)k->outlen, k->out) == 1 ? 0 : 1;
    }
    case KNC_SCRYPT:
        return EVP_PBE_scrypt((const char *)k->a, (size_t)k->alen, k->b, (size_t)k->blen, (uint64_t)k->n1,
                              (uint64_t)k->n2, (uint64_t)k->n3, (uint64_t)k->n4, k->out, (size_t)k->outlen) == 1 ? 0 : 1;
    case KNC_HKDF: {
        const EVP_MD *md = EVP_get_digestbyname(k->digest);
        if (!md) return 2;
        EVP_PKEY_CTX *pctx = EVP_PKEY_CTX_new_id(EVP_PKEY_HKDF, NULL);
        size_t outlen = (size_t)k->outlen;
        long long rc = 1;
        if (pctx && EVP_PKEY_derive_init(pctx) > 0 && EVP_PKEY_CTX_set_hkdf_md(pctx, md) > 0 &&
            EVP_PKEY_CTX_set1_hkdf_key(pctx, k->a, (int)k->alen) > 0 &&
            EVP_PKEY_CTX_set1_hkdf_salt(pctx, k->b, (int)k->blen) > 0 &&
            EVP_PKEY_CTX_add1_hkdf_info(pctx, k->c, (int)k->clen) > 0 &&
            EVP_PKEY_derive(pctx, k->out, &outlen) > 0)
            rc = 0;
        EVP_PKEY_CTX_free(pctx);
        return rc;
    }
    }
    return 1;
}

static unsigned char *knc_dup(const void *p, long long n) {
    unsigned char *d = (unsigned char *)malloc((size_t)(n > 0 ? n : 1));
    if (n > 0) memcpy(d, p, (size_t)n);
    return d;
}

static knc_kdf *knc_kdf_new(double op, void *a, long long alen, void *b, long long blen, void *c, long long clen,
                            double n1, double n2, double n3, double n4, const char *digest, void *out, long long outlen) {
    knc_kdf *k = (knc_kdf *)calloc(1, sizeof *k);
    k->op = (int)op;
    k->a = knc_dup(a, alen); k->alen = alen;
    k->b = knc_dup(b, blen); k->blen = blen;
    k->c = knc_dup(c, clen); k->clen = clen;
    k->n1 = n1; k->n2 = n2; k->n3 = n3; k->n4 = n4;
    snprintf(k->digest, sizeof k->digest, "%s", digest ? digest : "");
    k->out = (unsigned char *)out;
    k->outlen = outlen;
    return k;
}

static void knc_kdf_free(knc_kdf *k) {
    free(k->a); free(k->b); free(k->c); free(k);
}

/* 0 ok, 1 failed, 2 an unknown digest. */
double __kml_native_crypto_kdf(double op, void *a, long long alen, void *b, long long blen, void *c, long long clen,
                               double n1, double n2, double n3, double n4, const char *digest, void *out, long long outlen) {
    knc_kdf *k = knc_kdf_new(op, a, alen, b, blen, c, clen, n1, n2, n3, n4, digest, out, outlen);
    long long rc = knc_kdf_run(k);
    knc_kdf_free(k);
    return (double)rc;
}

static void knc_kdf_work(void *job, long long *err, double *res) {
    knc_kdf *k = (knc_kdf *)job;
    *err = knc_kdf_run(k);
    *res = 0;
    knc_kdf_free(k);
}

void __kml_native_crypto_kdf_async(double op, void *a, long long alen, void *b, long long blen, void *c, long long clen,
                                   double n1, double n2, double n3, double n4, const char *digest, void *out, long long outlen,
                                   void *inv, void *clo) {
    knc_kdf *k = knc_kdf_new(op, a, alen, b, blen, c, clen, n1, n2, n3, n4, digest, out, outlen);
    __kml_pool_job(knc_kdf_work, k, inv, clo);
}

/* ---- ciphers ---------------------------------------------------------------- */

/* createCipheriv/createDecipheriv: the id, or -1 unknown cipher, -2 invalid
 * key length, -3 invalid IV length. */
double __kml_native_crypto_cipher_new(const char *alg, void *key, long long keyLen, void *iv, long long ivLen,
                                      double encrypt, double authTagLen) {
    const EVP_CIPHER *c = EVP_get_cipherbyname(alg ? alg : "");
    if (!c) return -1;
    if (keyLen != EVP_CIPHER_get_key_length(c)) return -2;
    int mode = EVP_CIPHER_get_mode(c);
    int aead = mode == EVP_CIPH_GCM_MODE || mode == EVP_CIPH_CCM_MODE || mode == EVP_CIPH_OCB_MODE ||
               EVP_CIPHER_get_nid(c) == NID_chacha20_poly1305;
    int ivl = EVP_CIPHER_get_iv_length(c);
    if (!aead && ivLen != ivl && !(ivl == 0 && ivLen == 0)) return -3;
    EVP_CIPHER_CTX *ctx = EVP_CIPHER_CTX_new();
    if (!ctx || !EVP_CipherInit_ex(ctx, c, NULL, NULL, NULL, encrypt != 0)) {
        EVP_CIPHER_CTX_free(ctx);
        return -1;
    }
    if (aead && ivLen != ivl && EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_AEAD_SET_IVLEN, (int)ivLen, NULL) <= 0) {
        EVP_CIPHER_CTX_free(ctx);
        return -3;
    }
    if (aead && mode == EVP_CIPH_CCM_MODE && authTagLen > 0)
        EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_AEAD_SET_TAG, (int)authTagLen, NULL);
    if (!EVP_CipherInit_ex(ctx, NULL, NULL, (const unsigned char *)key, ivLen > 0 ? (const unsigned char *)iv : NULL, encrypt != 0)) {
        EVP_CIPHER_CTX_free(ctx);
        return -1;
    }
    long long id = knc_alloc();
    if (id < 0) {
        EVP_CIPHER_CTX_free(ctx);
        return -1;
    }
    knc_tab[id].kind = KNC_CIPHER;
    knc_tab[id].cipher = ctx;
    return (double)id;
}

double __kml_native_crypto_cipher_block_size(double id) {
    knc_handle *h = knc_get(id);
    return h && h->kind == KNC_CIPHER ? (double)EVP_CIPHER_CTX_get_block_size(h->cipher) : 0;
}

/* update: the bytes written to out (sized input + block size), or -1. */
double __kml_native_crypto_cipher_update(double id, void *in, long long inLen, void *out, long long outLen) {
    knc_handle *h = knc_get(id);
    if (!h || h->kind != KNC_CIPHER) return -1;
    int n = 0;
    if (inLen + EVP_CIPHER_CTX_get_block_size(h->cipher) > outLen) return -1;
    if (!EVP_CipherUpdate(h->cipher, (unsigned char *)out, &n, (const unsigned char *)in, (int)inLen)) return -1;
    return n;
}

/* final: the last bytes into out, or -1 (a bad decrypt / auth failure). */
double __kml_native_crypto_cipher_final(double id, void *out, long long outLen) {
    knc_handle *h = knc_get(id);
    if (!h || h->kind != KNC_CIPHER) return -1;
    int n = 0;
    if (outLen < EVP_CIPHER_CTX_get_block_size(h->cipher)) return -1;
    return EVP_CipherFinal_ex(h->cipher, (unsigned char *)out, &n) ? n : -1;
}

double __kml_native_crypto_cipher_set_padding(double id, double on) {
    knc_handle *h = knc_get(id);
    return h && h->kind == KNC_CIPHER && EVP_CIPHER_CTX_set_padding(h->cipher, on != 0) ? 1 : 0;
}

double __kml_native_crypto_cipher_set_aad(double id, void *aad, long long len) {
    knc_handle *h = knc_get(id);
    int n = 0;
    return h && h->kind == KNC_CIPHER && EVP_CipherUpdate(h->cipher, NULL, &n, (const unsigned char *)aad, (int)len) ? 1 : 0;
}

/* getAuthTag (after final): the tag's length written to out, or -1. */
double __kml_native_crypto_cipher_get_tag(double id, void *out, long long len) {
    knc_handle *h = knc_get(id);
    return h && h->kind == KNC_CIPHER && EVP_CIPHER_CTX_ctrl(h->cipher, EVP_CTRL_AEAD_GET_TAG, (int)len, out) > 0 ? len : -1;
}

double __kml_native_crypto_cipher_set_tag(double id, void *tag, long long len) {
    knc_handle *h = knc_get(id);
    return h && h->kind == KNC_CIPHER && EVP_CIPHER_CTX_ctrl(h->cipher, EVP_CTRL_AEAD_SET_TAG, (int)len, tag) > 0 ? 1 : 0;
}

/* ---- key pairs and PEM keys -------------------------------------------------- */

/* generateKeyPair: type 0 rsa (bits, exponent), 1 ec (curve name), 2
 * ed25519, 3 x25519. The result is both halves' PEM in one string: the
 * public key's (SPKI), then the private key's (PKCS#8). */
static EVP_PKEY *knc_keygen(double type, double bits, double exponent, const char *curve) {
    EVP_PKEY *pkey = NULL;
    EVP_PKEY_CTX *ctx = NULL;
    int t = (int)type;
    if (t == 0) {
        ctx = EVP_PKEY_CTX_new_from_name(NULL, "RSA", NULL);
        if (!ctx || EVP_PKEY_keygen_init(ctx) <= 0) goto done;
        if (EVP_PKEY_CTX_set_rsa_keygen_bits(ctx, (int)bits) <= 0) goto done;
        if (exponent > 0) {
            BIGNUM *e = BN_new();
            BN_set_word(e, (unsigned long)exponent);
            EVP_PKEY_CTX_set1_rsa_keygen_pubexp(ctx, e);
            BN_free(e);
        }
    } else if (t == 1) {
        ctx = EVP_PKEY_CTX_new_from_name(NULL, "EC", NULL);
        if (!ctx || EVP_PKEY_keygen_init(ctx) <= 0) goto done;
        int nid = OBJ_txt2nid(curve ? curve : "");
        if (nid == NID_undef) nid = EC_curve_nist2nid(curve ? curve : "");
        if (nid == NID_undef || EVP_PKEY_CTX_set_ec_paramgen_curve_nid(ctx, nid) <= 0) goto done;
    } else if (t == 2 || t == 3 || t == 6 || t == 7) {
        ctx = EVP_PKEY_CTX_new_from_name(NULL, t == 2 ? "ED25519" : t == 3 ? "X25519" : t == 6 ? "ED448" : "X448", NULL);
        if (!ctx || EVP_PKEY_keygen_init(ctx) <= 0) goto done;
    } else if (t == 4) {
        /* RSA-PSS: curve carries "hash\tmgf1Hash\tsaltLength" (each may be
         * empty), the key's restrictions. */
        ctx = EVP_PKEY_CTX_new_from_name(NULL, "RSA-PSS", NULL);
        if (!ctx || EVP_PKEY_keygen_init(ctx) <= 0) goto done;
        if (EVP_PKEY_CTX_set_rsa_keygen_bits(ctx, (int)bits) <= 0) goto done;
        if (exponent > 0) {
            BIGNUM *e = BN_new();
            BN_set_word(e, (unsigned long)exponent);
            EVP_PKEY_CTX_set1_rsa_keygen_pubexp(ctx, e);
            BN_free(e);
        }
        char spec[200], *hash = spec, *mgf1 = NULL, *salt = NULL;
        snprintf(spec, sizeof spec, "%s", curve ? curve : "");
        if ((mgf1 = strchr(hash, '\t'))) { *mgf1++ = 0; if ((salt = strchr(mgf1, '\t'))) *salt++ = 0; }
        if (*hash) {
            const EVP_MD *md = EVP_get_digestbyname(hash);
            if (!md || EVP_PKEY_CTX_set_rsa_pss_keygen_md(ctx, md) <= 0) goto done;
        }
        if (mgf1 && *mgf1) {
            const EVP_MD *md = EVP_get_digestbyname(mgf1);
            if (!md || EVP_PKEY_CTX_set_rsa_pss_keygen_mgf1_md(ctx, md) <= 0) goto done;
        }
        if (salt && *salt && EVP_PKEY_CTX_set_rsa_pss_keygen_saltlen(ctx, atoi(salt)) <= 0) goto done;
    } else if (t == 5) {
        /* DSA: bits for p, exponent carries the divisor (q) length. */
        EVP_PKEY_CTX *pctx = EVP_PKEY_CTX_new_from_name(NULL, "DSA", NULL);
        EVP_PKEY *params = NULL;
        if (!pctx || EVP_PKEY_paramgen_init(pctx) <= 0 || EVP_PKEY_CTX_set_dsa_paramgen_bits(pctx, (int)bits) <= 0 ||
            (exponent > 0 && EVP_PKEY_CTX_set_dsa_paramgen_q_bits(pctx, (int)exponent) <= 0) ||
            EVP_PKEY_paramgen(pctx, &params) <= 0) {
            EVP_PKEY_CTX_free(pctx);
            goto done;
        }
        EVP_PKEY_CTX_free(pctx);
        ctx = EVP_PKEY_CTX_new_from_pkey(NULL, params, NULL);
        EVP_PKEY_free(params);
        if (!ctx || EVP_PKEY_keygen_init(ctx) <= 0) goto done;
    } else {
        goto done;
    }
    EVP_PKEY_keygen(ctx, &pkey);
done:
    EVP_PKEY_CTX_free(ctx);
    return pkey;
}

static char *knc_pem_pair(EVP_PKEY *pkey) {
    BIO *pub = BIO_new(BIO_s_mem()), *priv = BIO_new(BIO_s_mem());
    char *out = NULL;
    if (pub && priv && PEM_write_bio_PUBKEY(pub, pkey) && PEM_write_bio_PKCS8PrivateKey(priv, pkey, NULL, NULL, 0, NULL, NULL)) {
        char *a, *b;
        long al = BIO_get_mem_data(pub, &a), bl = BIO_get_mem_data(priv, &b);
        out = (char *)malloc((size_t)(al + bl + 1));
        memcpy(out, a, (size_t)al);
        memcpy(out + al, b, (size_t)bl);
        out[al + bl] = 0;
    }
    BIO_free(pub);
    BIO_free(priv);
    return out;
}

/* The PEM pair (public then private), or "" when generation fails. */
char *__kml_native_crypto_keygen(double type, double bits, double exponent, const char *curve) {
    EVP_PKEY *pkey = knc_keygen(type, bits, exponent, curve);
    char *pem = pkey ? knc_pem_pair(pkey) : NULL;
    EVP_PKEY_free(pkey);
    char *out = kml_ncrypto_str(pem ? pem : "");
    free(pem);
    return out;
}

typedef struct { double type, bits, exponent; char curve[64]; } knc_keygen_job;

static void knc_keygen_work(void *job, long long *err, double *res) {
    knc_keygen_job *j = (knc_keygen_job *)job;
    EVP_PKEY *pkey = knc_keygen(j->type, j->bits, j->exponent, j->curve);
    char *pem = pkey ? knc_pem_pair(pkey) : NULL;
    EVP_PKEY_free(pkey);
    /* The PEM rides back as the result's address; the loop reads it. */
    *err = pem ? 0 : 1;
    *res = (double)(intptr_t)pem;
    free(j);
}

void __kml_native_crypto_keygen_async(double type, double bits, double exponent, const char *curve, void *inv, void *clo) {
    knc_keygen_job *j = (knc_keygen_job *)calloc(1, sizeof *j);
    j->type = type; j->bits = bits; j->exponent = exponent;
    snprintf(j->curve, sizeof j->curve, "%s", curve ? curve : "");
    __kml_pool_job(knc_keygen_work, j, inv, clo);
}

/* The PEM a pooled keygen produced (its callback's result), freed. */
char *__kml_native_crypto_keygen_take(double result) {
    char *pem = (char *)(intptr_t)result;
    char *out = kml_ncrypto_str(pem ? pem : "");
    free(pem);
    return out;
}

/* An encrypted key's passphrase, as Node's PasswordCallback supplies it:
 * none fails the read ("interrupted or cancelled") rather than prompting. */
static int knc_pass_cb(char *buf, int size, int rwflag, void *u) {
    (void)rwflag;
    const char *pass = (const char *)u;
    if (!pass) return -1;
    int n = (int)strlen(pass);
    if (n > size) n = size;
    memcpy(buf, pass, (size_t)n);
    return n;
}

/* passLen < 0: no passphrase. */
static EVP_PKEY *knc_read_key(const char *pem, int priv, const char *pass, long long passLen) {
    BIO *bio = BIO_new_mem_buf(pem, -1);
    EVP_PKEY *k = NULL;
    if (!bio) return NULL;
    char *pw = NULL;
    if (passLen >= 0) {
        pw = malloc((size_t)passLen + 1);
        memcpy(pw, pass, (size_t)passLen);
        pw[passLen] = 0;
    }
    if (priv) {
        k = PEM_read_bio_PrivateKey(bio, NULL, knc_pass_cb, pw);
    } else {
        k = PEM_read_bio_PUBKEY(bio, NULL, knc_pass_cb, pw);
        if (!k) {
            /* A private key also verifies (its public half), as in Node. */
            ERR_clear_error();
            BIO_free(bio);
            bio = BIO_new_mem_buf(pem, -1);
            k = PEM_read_bio_PrivateKey(bio, NULL, knc_pass_cb, pw);
        }
    }
    free(pw);
    BIO_free(bio);
    return k;
}

/* sign: the signature's length in out, or -1 (a bad key), -2 (out too
 * small; the needed size is then the negated result - 2). digest "" for a
 * one-shot key type (Ed25519). */
/* The byte length of each of r and s in a key's IEEE P1363 signature (EC:
 * the field size; DSA: q's), 0 for a key that signs another way. */
static int knc_p1363_half(EVP_PKEY *k) {
    int base = EVP_PKEY_get_base_id(k);
    if (base == EVP_PKEY_EC) return (EVP_PKEY_get_bits(k) + 7) / 8;
    if (base == EVP_PKEY_DSA) {
        BIGNUM *q = NULL;
        int n = 0;
        if (EVP_PKEY_get_bn_param(k, OSSL_PKEY_PARAM_FFC_Q, &q) && q) n = (BN_num_bits(q) + 7) / 8;
        BN_free(q);
        return n;
    }
    return 0;
}

/* Node's sign options on the signing context: padding (< 0 the key's
 * default), saltLength (KNC_NO_SALT none) for RSA-PSS. */
#define KNC_NO_SALT 1e9
static int knc_sig_opts(EVP_PKEY_CTX *pctx, EVP_PKEY *k, double padding, double saltLength) {
    int base = EVP_PKEY_get_base_id(k);
    if (base != EVP_PKEY_RSA && base != EVP_PKEY_RSA_PSS) return 1;
    if (padding >= 0 && EVP_PKEY_CTX_set_rsa_padding(pctx, (int)padding) <= 0) return 0;
    if (saltLength != KNC_NO_SALT && (padding == RSA_PKCS1_PSS_PADDING || base == EVP_PKEY_RSA_PSS) &&
        EVP_PKEY_CTX_set_rsa_pss_saltlen(pctx, (int)saltLength) <= 0)
        return 0;
    return 1;
}

/* sign: the signature's length in out, -2 - n when out is too small, -1 on
 * failure. dsaEncoding 1: IEEE P1363 (r || s) for EC and DSA keys. */
double __kml_native_crypto_sign(const char *digest, const char *pem, void *pass, long long passLen, double hasPass,
                                double padding, double saltLength, double dsaEncoding,
                                void *data, long long len, void *out, long long outLen) {
    EVP_PKEY *k = knc_read_key(pem, 1, (const char *)pass, hasPass != 0 ? passLen : -1);
    if (!k) return -1;
    const EVP_MD *md = digest && *digest ? EVP_get_digestbyname(digest) : NULL;
    EVP_MD_CTX *ctx = EVP_MD_CTX_new();
    EVP_PKEY_CTX *pctx = NULL;
    size_t n = 0;
    double r = -1;
    int half = dsaEncoding == 1 ? knc_p1363_half(k) : 0;
    if (ctx && EVP_DigestSignInit(ctx, &pctx, md, NULL, k) > 0 && knc_sig_opts(pctx, k, padding, saltLength) &&
        EVP_DigestSign(ctx, NULL, &n, data, (size_t)len) > 0) {
        unsigned char *sig = (unsigned char *)malloc(n);
        if (sig && EVP_DigestSign(ctx, sig, &n, data, (size_t)len) > 0) {
            if (half > 0) {
                const unsigned char *p = sig;
                ECDSA_SIG *es = d2i_ECDSA_SIG(NULL, &p, (long)n);
                n = (size_t)(2 * half);
                if (!es) {
                    r = -1;
                } else if ((long long)n > outLen) {
                    r = -2 - (double)n;
                } else if (BN_bn2binpad(ECDSA_SIG_get0_r(es), out, half) == half &&
                           BN_bn2binpad(ECDSA_SIG_get0_s(es), (unsigned char *)out + half, half) == half) {
                    r = (double)n;
                }
                ECDSA_SIG_free(es);
            } else if ((long long)n > outLen) {
                r = -2 - (double)n;
            } else {
                memcpy(out, sig, n);
                r = (double)n;
            }
        }
        free(sig);
    }
    EVP_MD_CTX_free(ctx);
    EVP_PKEY_free(k);
    return r;
}

/* verify: 1 valid, 0 invalid, -1 a bad key. */
double __kml_native_crypto_verify(const char *digest, const char *pem, void *pass, long long passLen, double hasPass,
                                  double padding, double saltLength, double dsaEncoding,
                                  void *data, long long len, void *sig, long long sigLen) {
    EVP_PKEY *k = knc_read_key(pem, 0, (const char *)pass, hasPass != 0 ? passLen : -1);
    if (!k) return -1;
    const EVP_MD *md = digest && *digest ? EVP_get_digestbyname(digest) : NULL;
    EVP_MD_CTX *ctx = EVP_MD_CTX_new();
    EVP_PKEY_CTX *pctx = NULL;
    double r = 0;
    unsigned char *der = NULL;
    int half = dsaEncoding == 1 ? knc_p1363_half(k) : 0;
    if (half > 0) {
        /* r || s back to the DER libcrypto verifies; a wrong length is a
         * bad signature. */
        ECDSA_SIG *es = ECDSA_SIG_new();
        BIGNUM *br = NULL, *bs = NULL;
        int dl = 0;
        if (es && sigLen == 2 * half && (br = BN_bin2bn(sig, half, NULL)) &&
            (bs = BN_bin2bn((unsigned char *)sig + half, half, NULL)) && ECDSA_SIG_set0(es, br, bs)) {
            br = bs = NULL;
            dl = i2d_ECDSA_SIG(es, &der);
        }
        BN_free(br);
        BN_free(bs);
        ECDSA_SIG_free(es);
        if (dl <= 0) {
            EVP_MD_CTX_free(ctx);
            EVP_PKEY_free(k);
            ERR_clear_error();
            return 0;
        }
        sig = der;
        sigLen = dl;
    }
    if (ctx && EVP_DigestVerifyInit(ctx, &pctx, md, NULL, k) > 0 && knc_sig_opts(pctx, k, padding, saltLength))
        r = EVP_DigestVerify(ctx, sig, (size_t)sigLen, data, (size_t)len) == 1 ? 1 : 0;
    OPENSSL_free(der);
    EVP_MD_CTX_free(ctx);
    EVP_PKEY_free(k);
    ERR_clear_error();
    return r;
}

/* timingSafeEqual over two equal-length regions. */
double __kml_native_crypto_timing_equal(void *a, long long alen, void *b, long long blen) {
    if (alen != blen) return 0;
    return CRYPTO_memcmp(a, b, (size_t)alen) == 0 ? 1 : 0;
}

/* The libcrypto error, as Node reports it ("error:1C800064:Provider
 * routines::bad decrypt"), or "" when there is none; the queue is cleared. */
char *__kml_native_crypto_last_error(void) {
    /* The queue's earliest error, as Node's ThrowCryptoError(ERR_get_error()). */
    unsigned long e = ERR_peek_error();
    char buf[256];
    buf[0] = 0;
    if (e) ERR_error_string_n(e, buf, sizeof buf);
    ERR_clear_error();
    return kml_ncrypto_str(buf);
}

/* ── KeyObject (lib/internal/crypto/keys.js) ───────────────────────────────
 * A KeyObject's asymmetric key travels as canonical PEM: PKCS#8 for a
 * private key, SPKI for a public one. These parse any input form into it,
 * export it in Node's encodings, describe it, and convert JWKs. */
#include <openssl/decoder.h>
#include <openssl/encoder.h>

/* The PEM (PKCS#8 or SPKI) of pkey, malloc'd; NULL when it has no such
 * half (a public key has no PKCS#8). */
static char *knc_pkey_pem(EVP_PKEY *pkey, int priv) {
    OSSL_ENCODER_CTX *ec = OSSL_ENCODER_CTX_new_for_pkey(pkey, priv ? EVP_PKEY_KEYPAIR : EVP_PKEY_PUBLIC_KEY,
                                                         "PEM", priv ? "PrivateKeyInfo" : "SubjectPublicKeyInfo", NULL);
    unsigned char *data = NULL;
    size_t len = 0;
    char *out = NULL;
    if (ec && OSSL_ENCODER_CTX_get_num_encoders(ec) > 0 && OSSL_ENCODER_to_data(ec, &data, &len)) {
        out = (char *)malloc(len + 1);
        memcpy(out, data, len);
        out[len] = 0;
    }
    OPENSSL_free(data);
    OSSL_ENCODER_CTX_free(ec);
    return out;
}

/* Decodes a key: format 0 PEM, 1 DER; type 0 any, 1 pkcs1, 2 spki, 3 pkcs8,
 * 4 sec1. passLen < 0: no passphrase. */
static EVP_PKEY *knc_decode_key(const unsigned char *data, long long len, int format, int type,
                                const unsigned char *pass, long long passLen) {
    EVP_PKEY *pkey = NULL;
    const char *structure = NULL, *keytype = NULL;
    switch (type) {
    case 1: structure = "type-specific"; keytype = "RSA"; break;
    case 2: structure = "SubjectPublicKeyInfo"; break;
    case 4: structure = "type-specific"; keytype = "EC"; break;
    }
    ERR_clear_error();
    OSSL_DECODER_CTX *dc = OSSL_DECODER_CTX_new_for_pkey(&pkey, format == 1 ? "DER" : "PEM", structure, keytype, 0, NULL, NULL);
    if (!dc) return NULL;
    /* An encrypted key's passphrase as Node's PasswordCallback supplies it:
     * none cancels the read rather than prompting. */
    char *pw = NULL;
    if (passLen >= 0) {
        pw = (char *)malloc((size_t)passLen + 1);
        memcpy(pw, pass, (size_t)passLen);
        pw[passLen] = 0;
    }
    OSSL_DECODER_CTX_set_pem_password_cb(dc, knc_pass_cb, pw);
    const unsigned char *p = data;
    size_t n = (size_t)len;
    if (!OSSL_DECODER_from_data(dc, &p, &n)) {
        EVP_PKEY_free(pkey);
        pkey = NULL;
    }
    OSSL_DECODER_CTX_free(dc);
    free(pw);
    return pkey;
}

static EVP_PKEY *knc_pem_key(const char *pem) {
    return knc_decode_key((const unsigned char *)pem, (long long)strlen(pem), 0, 0, NULL, -1);
}

/* createPrivateKey / createPublicKey: the key's canonical PEM, or "" when
 * the input is no such key ("!pass" when an encrypted key had no
 * passphrase). A public key may come from a private one. */
char *__kml_native_crypto_key_parse(void *data, long long len, double format, double type, double wantPriv,
                                    void *pass, long long passLen, double hasPass) {
    EVP_PKEY *pkey = knc_decode_key((const unsigned char *)data, len, (int)format, (int)type,
                                    (const unsigned char *)pass, hasPass != 0 ? passLen : -1);
    char *pem = pkey ? knc_pkey_pem(pkey, wantPriv != 0) : NULL;
    EVP_PKEY_free(pkey);
    char *out = kml_ncrypto_str(pem ? pem : "");
    free(pem);
    return out;
}

/* keyObject.export({ format, type, cipher, passphrase }) into out: the
 * length, -2 - n when out is too small, -1 when the key cannot be encoded
 * so. format 0 PEM, 1 DER; type as knc_decode_key. */
double __kml_native_crypto_key_export(const char *pem, double priv, double format, double type, const char *cipher,
                                      void *pass, long long passLen, double hasPass, void *out, long long outLen) {
    ERR_clear_error();
    EVP_PKEY *pkey = knc_pem_key(pem);
    if (!pkey) return -1;
    const char *structure = "SubjectPublicKeyInfo";
    switch ((int)type) {
    case 1: case 4: structure = "type-specific"; break;
    case 3: structure = "PrivateKeyInfo"; break;
    }
    if (((int)type == 1 && !EVP_PKEY_is_a(pkey, "RSA")) || ((int)type == 4 && !EVP_PKEY_is_a(pkey, "EC"))) {
        EVP_PKEY_free(pkey);
        return -1;
    }
    OSSL_ENCODER_CTX *ec = OSSL_ENCODER_CTX_new_for_pkey(pkey, priv != 0 ? EVP_PKEY_KEYPAIR : EVP_PKEY_PUBLIC_KEY,
                                                         (int)format == 1 ? "DER" : "PEM", structure, NULL);
    double r = -1;
    unsigned char *buf = NULL;
    size_t n = 0;
    if (ec && OSSL_ENCODER_CTX_get_num_encoders(ec) > 0) {
        int ok = 1;
        if (cipher && *cipher) {
            ok = OSSL_ENCODER_CTX_set_cipher(ec, cipher, NULL) &&
                 OSSL_ENCODER_CTX_set_passphrase(ec, (const unsigned char *)pass, hasPass != 0 ? (size_t)passLen : 0);
        }
        if (ok && OSSL_ENCODER_to_data(ec, &buf, &n)) {
            if ((long long)n > outLen) {
                r = -2 - (double)n;
            } else {
                memcpy(out, buf, n);
                r = (double)n;
            }
        }
    }
    OPENSSL_free(buf);
    OSSL_ENCODER_CTX_free(ec);
    EVP_PKEY_free(pkey);
    return r;
}

/* The key's Node type name ("" for one Node has no name for). */
static const char *knc_key_type(EVP_PKEY *pkey) {
    switch (EVP_PKEY_get_base_id(pkey)) {
    case EVP_PKEY_RSA: return "rsa";
    case EVP_PKEY_RSA_PSS: return "rsa-pss";
    case EVP_PKEY_DSA: return "dsa";
    case EVP_PKEY_DH: case EVP_PKEY_DHX: return "dh";
    case EVP_PKEY_EC: return "ec";
    case EVP_PKEY_ED25519: return "ed25519";
    case EVP_PKEY_ED448: return "ed448";
    case EVP_PKEY_X25519: return "x25519";
    case EVP_PKEY_X448: return "x448";
    }
    return "";
}

/* A digest name as Node shows it (OpenSSL's long name: "sha256"). */
static void knc_md_longname(const char *name, char *out, size_t cap) {
    const EVP_MD *md = name && *name ? EVP_get_digestbyname(name) : NULL;
    const char *ln = md ? OBJ_nid2ln(EVP_MD_get_type(md)) : name;
    snprintf(out, cap, "%s", ln ? ln : "");
}

/* asymmetricKeyType and asymmetricKeyDetails, tab-separated: type,
 * modulusLength, publicExponent (decimal), namedCurve, hashAlgorithm,
 * mgf1HashAlgorithm, saltLength, divisorLength — empty where absent. "" for
 * a key that does not parse. */
char *__kml_native_crypto_key_info(const char *pem) {
    EVP_PKEY *pkey = knc_pem_key(pem);
    if (!pkey) return kml_ncrypto_str("");
    char bits[24] = "", exp[1100] = "", curve[80] = "", hash[64] = "", mgf1[64] = "", salt[24] = "", div[24] = "";
    const char *type = knc_key_type(pkey);
    int base = EVP_PKEY_get_base_id(pkey);
    if (base == EVP_PKEY_RSA || base == EVP_PKEY_RSA_PSS || base == EVP_PKEY_DSA) {
        snprintf(bits, sizeof bits, "%d", EVP_PKEY_get_bits(pkey));
    }
    if (base == EVP_PKEY_RSA || base == EVP_PKEY_RSA_PSS) {
        BIGNUM *e = NULL;
        if (EVP_PKEY_get_bn_param(pkey, OSSL_PKEY_PARAM_RSA_E, &e) && e) {
            char *d = BN_bn2dec(e);
            snprintf(exp, sizeof exp, "%s", d ? d : "");
            OPENSSL_free(d);
            BN_free(e);
        }
    }
    if (base == EVP_PKEY_RSA_PSS) {
        char name[64] = "";
        if (EVP_PKEY_get_utf8_string_param(pkey, OSSL_PKEY_PARAM_RSA_DIGEST, name, sizeof name, NULL)) {
            knc_md_longname(name, hash, sizeof hash);
            name[0] = 0;
            if (EVP_PKEY_get_utf8_string_param(pkey, OSSL_PKEY_PARAM_RSA_MGF1_DIGEST, name, sizeof name, NULL))
                knc_md_longname(name, mgf1, sizeof mgf1);
            else
                snprintf(mgf1, sizeof mgf1, "%s", hash);
            int sl = 0;
            if (EVP_PKEY_get_int_param(pkey, OSSL_PKEY_PARAM_RSA_PSS_SALTLEN, &sl)) snprintf(salt, sizeof salt, "%d", sl);
        }
    }
    if (base == EVP_PKEY_EC) {
        EVP_PKEY_get_utf8_string_param(pkey, OSSL_PKEY_PARAM_GROUP_NAME, curve, sizeof curve, NULL);
    }
    if (base == EVP_PKEY_DSA) {
        BIGNUM *q = NULL;
        if (EVP_PKEY_get_bn_param(pkey, OSSL_PKEY_PARAM_FFC_Q, &q) && q) {
            snprintf(div, sizeof div, "%d", BN_num_bits(q));
            BN_free(q);
        }
    }
    EVP_PKEY_free(pkey);
    size_t cap = strlen(type) + strlen(bits) + strlen(exp) + strlen(curve) + strlen(hash) + strlen(mgf1) + strlen(salt) + strlen(div) + 16;
    char *buf = (char *)malloc(cap);
    snprintf(buf, cap, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s", type, bits, exp, curve, hash, mgf1, salt, div);
    char *out = kml_ncrypto_str(buf);
    free(buf);
    return out;
}

static void knc_append(char **buf, size_t *len, const char *key, const char *val) {
    if (!val) return;
    size_t add = strlen(key) + strlen(val) + 2;
    *buf = (char *)realloc(*buf, *len + add + 1);
    *len += (size_t)snprintf(*buf + *len, add + 1, "%s%s=%s", *len ? "\t" : "", key, val);
}

static char *knc_b64u(const unsigned char *p, long long n) {
    char *out = NULL;
    long long ol;
    if (__kml_crypto_b64url_encode(p, n, &out, &ol) != 0) return NULL;
    return out;
}

/* keyObject.export({ format: 'jwk' }): the members in Node's order,
 * tab-separated name=value; "!type" for a key type JWK has no form for,
 * "!curve" for an EC curve it has no name for. */
char *__kml_native_crypto_key_jwk(const char *pem, double priv) {
    EVP_PKEY *pkey = knc_pem_key(pem);
    char *buf = NULL;
    size_t len = 0;
    const char *err = "!type";
    if (!pkey) return kml_ncrypto_str("!type");
    int base = EVP_PKEY_get_base_id(pkey);
    if (base == EVP_PKEY_RSA) {
        static const char *names[8] = {"n", "e", "d", "p", "q", "dp", "dq", "qi"};
        static const char *params[8] = {OSSL_PKEY_PARAM_RSA_N, OSSL_PKEY_PARAM_RSA_E, OSSL_PKEY_PARAM_RSA_D,
                                        OSSL_PKEY_PARAM_RSA_FACTOR1, OSSL_PKEY_PARAM_RSA_FACTOR2,
                                        OSSL_PKEY_PARAM_RSA_EXPONENT1, OSSL_PKEY_PARAM_RSA_EXPONENT2,
                                        OSSL_PKEY_PARAM_RSA_COEFFICIENT1};
        knc_append(&buf, &len, "kty", "RSA");
        for (int i = 0; i < (priv != 0 ? 8 : 2); i++) {
            char *v = kml_pkey_bn_b64u(pkey, params[i]);
            knc_append(&buf, &len, names[i], v);
            free(v);
        }
        err = NULL;
    } else if (base == EVP_PKEY_EC) {
        char group[80] = "";
        const char *crv = NULL;
        EVP_PKEY_get_utf8_string_param(pkey, OSSL_PKEY_PARAM_GROUP_NAME, group, sizeof group, NULL);
        if (!strcmp(group, "prime256v1")) crv = "P-256";
        else if (!strcmp(group, "secp384r1")) crv = "P-384";
        else if (!strcmp(group, "secp521r1")) crv = "P-521";
        else if (!strcmp(group, "secp256k1")) crv = "secp256k1";
        err = "!curve";
        if (crv) {
            long long cb = (EVP_PKEY_get_bits(pkey) + 7) / 8;
            unsigned char pt[200];
            size_t ptLen = 0;
            if (EVP_PKEY_get_octet_string_param(pkey, OSSL_PKEY_PARAM_PUB_KEY, pt, sizeof pt, &ptLen) &&
                ptLen == (size_t)(1 + 2 * cb) && pt[0] == 4) {
                char *x = knc_b64u(pt + 1, cb), *y = knc_b64u(pt + 1 + cb, cb);
                knc_append(&buf, &len, "kty", "EC");
                knc_append(&buf, &len, "x", x);
                knc_append(&buf, &len, "y", y);
                knc_append(&buf, &len, "crv", crv);
                free(x);
                free(y);
                err = NULL;
                if (priv != 0) {
                    BIGNUM *bn = NULL;
                    err = "!type";
                    if (EVP_PKEY_get_bn_param(pkey, OSSL_PKEY_PARAM_PRIV_KEY, &bn) && bn) {
                        unsigned char tmp[80];
                        if (BN_bn2binpad(bn, tmp, (int)cb) == (int)cb) {
                            char *d = knc_b64u(tmp, cb);
                            knc_append(&buf, &len, "d", d);
                            free(d);
                            err = NULL;
                        }
                        BN_free(bn);
                    }
                }
            }
        }
    } else if (base == EVP_PKEY_ED25519 || base == EVP_PKEY_ED448 || base == EVP_PKEY_X25519 || base == EVP_PKEY_X448) {
        const char *crv = base == EVP_PKEY_ED25519 ? "Ed25519" : base == EVP_PKEY_ED448 ? "Ed448"
                        : base == EVP_PKEY_X25519 ? "X25519" : "X448";
        unsigned char raw[64];
        size_t n = sizeof raw;
        knc_append(&buf, &len, "crv", crv);
        if (priv != 0 && EVP_PKEY_get_raw_private_key(pkey, raw, &n)) {
            char *d = knc_b64u(raw, (long long)n);
            knc_append(&buf, &len, "d", d);
            free(d);
        }
        n = sizeof raw;
        if (EVP_PKEY_get_raw_public_key(pkey, raw, &n)) {
            char *x = knc_b64u(raw, (long long)n);
            knc_append(&buf, &len, "x", x);
            free(x);
        }
        knc_append(&buf, &len, "kty", "OKP");
        err = NULL;
    }
    EVP_PKEY_free(pkey);
    char *out = kml_ncrypto_str(err ? err : buf ? buf : "");
    free(buf);
    return out;
}

/* createPrivateKey/createPublicKey({ key: jwk, format: 'jwk' }): the key's
 * canonical PEM, or "" when the members describe no such key. kty 0 RSA,
 * 1 EC, 2 OKP; for EC, crv names the curve; for OKP, Ed25519/Ed448/X25519/
 * X448. A private key needs d. */
char *__kml_native_crypto_key_from_jwk(double kty, const char *crv, const char *n, const char *e, const char *d,
                                       const char *p, const char *q, const char *dp, const char *dq, const char *qi,
                                       const char *x, const char *y, double wantPriv) {
#define KNC_OPT(s) ((s) && *(s) ? (s) : NULL)
    unsigned char *der = NULL;
    long long derLen = 0, kind = 0;
    EVP_PKEY *pkey = NULL;
    int priv = wantPriv != 0;
    if (priv && !KNC_OPT(d)) return kml_ncrypto_str("");
    if ((int)kty == 0) {
        if (__kml_crypto_jwk_import_rsa(KNC_OPT(n), KNC_OPT(e), priv ? KNC_OPT(d) : NULL, KNC_OPT(p), KNC_OPT(q),
                                        KNC_OPT(dp), KNC_OPT(dq), KNC_OPT(qi), &der, &derLen, &kind) == 0)
            pkey = kml_pkey_from_der(der, derLen, priv);
    } else if ((int)kty == 1) {
        long long id = !strcmp(crv, "P-256") ? 1 : !strcmp(crv, "P-384") ? 2 : !strcmp(crv, "P-521") ? 3 : 0;
        if (id && __kml_crypto_jwk_import_ec(id, KNC_OPT(x), KNC_OPT(y), priv ? KNC_OPT(d) : NULL, &der, &derLen, &kind) == 0)
            pkey = kml_pkey_from_der(der, derLen, priv);
    } else if ((int)kty == 2) {
        int nid = !strcmp(crv, "Ed25519") ? EVP_PKEY_ED25519 : !strcmp(crv, "Ed448") ? EVP_PKEY_ED448
                : !strcmp(crv, "X25519") ? EVP_PKEY_X25519 : !strcmp(crv, "X448") ? EVP_PKEY_X448 : 0;
        unsigned char *raw = NULL;
        long long rawLen = 0;
        const char *src = priv ? d : x;
        if (nid && KNC_OPT(src) && __kml_crypto_b64url_decode(src, (long long)strlen(src), &raw, &rawLen) == 0) {
            pkey = priv ? EVP_PKEY_new_raw_private_key(nid, NULL, raw, (size_t)rawLen)
                        : EVP_PKEY_new_raw_public_key(nid, NULL, raw, (size_t)rawLen);
            free(raw);
        }
    }
#undef KNC_OPT
    free(der);
    char *pem = pkey ? knc_pkey_pem(pkey, priv) : NULL;
    EVP_PKEY_free(pkey);
    char *out = kml_ncrypto_str(pem ? pem : "");
    free(pem);
    ERR_clear_error();
    return out;
}

/* publicEncrypt (op 0), privateDecrypt (1), privateEncrypt (2) and
 * publicDecrypt (3) — Node's RSA cipher jobs (crypto_cipher.cc): the result's
 * length in out, -2 - n when out is too small, -1 on failure, -3 for an
 * unknown OAEP digest. padding is the RSA_* padding; oaepHash and label
 * apply to OAEP. */
double __kml_native_crypto_pkey_crypt(double op, const char *pem, void *pass, long long passLen, double hasPass,
                                      double padding, const char *oaepHash, void *label, long long labelLen,
                                      void *data, long long len, void *out, long long outLen) {
    int o = (int)op;
    ERR_clear_error();
    EVP_PKEY *k = knc_read_key(pem, o == 1 || o == 2, (const char *)pass, hasPass != 0 ? passLen : -1);
    if (!k) return -1;
    double r = -1;
    EVP_PKEY_CTX *ctx = EVP_PKEY_CTX_new(k, NULL);
    int ok = ctx != NULL;
    if (ok) {
        switch (o) {
        case 0: ok = EVP_PKEY_encrypt_init(ctx) > 0; break;
        case 1: ok = EVP_PKEY_decrypt_init(ctx) > 0; break;
        case 2: ok = EVP_PKEY_sign_init(ctx) > 0; break;
        default: ok = EVP_PKEY_verify_recover_init(ctx) > 0; break;
        }
    }
    if (ok) ok = EVP_PKEY_CTX_set_rsa_padding(ctx, (int)padding) > 0;
    if (ok && (int)padding == RSA_PKCS1_OAEP_PADDING) {
        const EVP_MD *md = EVP_get_digestbyname(oaepHash && *oaepHash ? oaepHash : "sha1");
        if (!md) {
            EVP_PKEY_CTX_free(ctx);
            EVP_PKEY_free(k);
            return -3;
        }
        ok = EVP_PKEY_CTX_set_rsa_oaep_md(ctx, md) > 0;
        if (ok && labelLen > 0) {
            unsigned char *l = (unsigned char *)OPENSSL_malloc((size_t)labelLen);
            memcpy(l, label, (size_t)labelLen);
            ok = EVP_PKEY_CTX_set0_rsa_oaep_label(ctx, l, (int)labelLen) > 0;
            if (!ok) OPENSSL_free(l);
        }
    }
    size_t n = 0;
    if (ok) {
        const unsigned char *in = (const unsigned char *)data;
        switch (o) {
        case 0: ok = EVP_PKEY_encrypt(ctx, NULL, &n, in, (size_t)len) > 0; break;
        case 1: ok = EVP_PKEY_decrypt(ctx, NULL, &n, in, (size_t)len) > 0; break;
        case 2: ok = EVP_PKEY_sign(ctx, NULL, &n, in, (size_t)len) > 0; break;
        default: ok = EVP_PKEY_verify_recover(ctx, NULL, &n, in, (size_t)len) > 0; break;
        }
    }
    if (ok) {
        unsigned char *buf = (unsigned char *)malloc(n ? n : 1);
        const unsigned char *in = (const unsigned char *)data;
        switch (o) {
        case 0: ok = EVP_PKEY_encrypt(ctx, buf, &n, in, (size_t)len) > 0; break;
        case 1: ok = EVP_PKEY_decrypt(ctx, buf, &n, in, (size_t)len) > 0; break;
        case 2: ok = EVP_PKEY_sign(ctx, buf, &n, in, (size_t)len) > 0; break;
        default: ok = EVP_PKEY_verify_recover(ctx, buf, &n, in, (size_t)len) > 0; break;
        }
        if (ok) {
            if ((long long)n > outLen) {
                r = -2 - (double)n;
            } else {
                memcpy(out, buf, n);
                r = (double)n;
            }
        }
        free(buf);
    }
    EVP_PKEY_CTX_free(ctx);
    EVP_PKEY_free(k);
    return r;
}

/* ECDH (lib/internal/crypto/diffiehellman.js's ECDH over node's ECDH
 * binding), stateless: op 0 a new private key; 1 the public point of priv
 * in format (2 compressed, 4 uncompressed, 6 hybrid); 2 the shared secret of
 * priv and the peer point pub; 3 pub re-encoded in format; 4 priv checked
 * (0 valid). The result's length in out, -1 on failure, -6 an invalid
 * point, -7 an invalid private key, -8 an unknown curve. */
double __kml_native_crypto_ecdh(double op, const char *curve, void *priv, long long privLen, void *pub, long long pubLen,
                                double format, void *out, long long outLen) {
    int nid = OBJ_sn2nid(curve ? curve : "");
    if (nid == NID_undef) nid = EC_curve_nist2nid(curve ? curve : "");
    EC_GROUP *group = nid != NID_undef ? EC_GROUP_new_by_curve_name(nid) : NULL;
    if (!group) return -8;
    double r = -1;
    BN_CTX *bc = BN_CTX_new();
    BIGNUM *k = NULL;
    EC_POINT *pt = NULL;
    const BIGNUM *order = EC_GROUP_get0_order(group);
    int fieldBytes = (EC_GROUP_get_degree(group) + 7) / 8;
    int o = (int)op;
    if (o == 0) {
        k = BN_new();
        do {
            if (!BN_rand_range(k, order)) goto done;
        } while (BN_is_zero(k));
    } else if (o == 1 || o == 2 || o == 4) {
        k = BN_bin2bn((const unsigned char *)priv, (int)privLen, NULL);
        if (!k || BN_is_zero(k) || BN_cmp(k, order) >= 0) { r = -7; goto done; }
        if (o == 4) { r = 0; goto done; }
    }
    if (o == 0) {
        if (fieldBytes > outLen) goto done;
        int nb = BN_num_bytes(order);
        if (BN_bn2binpad(k, out, nb) != nb) goto done;
        r = nb;
        goto done;
    }
    pt = EC_POINT_new(group);
    if (!pt) goto done;
    if (o == 1) {
        if (!EC_POINT_mul(group, pt, k, NULL, NULL, bc)) goto done;
    } else {
        if (!EC_POINT_oct2point(group, pt, (const unsigned char *)pub, (size_t)pubLen, bc) ||
            EC_POINT_is_on_curve(group, pt, bc) != 1) {
            r = -6;
            goto done;
        }
    }
    if (o == 2) {
        EC_POINT *s = EC_POINT_new(group);
        BIGNUM *x = BN_new();
        if (s && x && EC_POINT_mul(group, s, NULL, pt, k, bc) && !EC_POINT_is_at_infinity(group, s) &&
            EC_POINT_get_affine_coordinates(group, s, x, NULL, bc) && fieldBytes <= outLen &&
            BN_bn2binpad(x, out, fieldBytes) == fieldBytes)
            r = fieldBytes;
        EC_POINT_free(s);
        BN_free(x);
        goto done;
    }
    {
        point_conversion_form_t form = (int)format == 2 ? POINT_CONVERSION_COMPRESSED
                                     : (int)format == 6 ? POINT_CONVERSION_HYBRID : POINT_CONVERSION_UNCOMPRESSED;
        size_t n = EC_POINT_point2oct(group, pt, form, NULL, 0, bc);
        if (n > 0 && (long long)n <= outLen && EC_POINT_point2oct(group, pt, form, out, n, bc) == n) r = (double)n;
    }
done:
    EC_POINT_free(pt);
    BN_clear_free(k);
    BN_CTX_free(bc);
    EC_GROUP_free(group);
    ERR_clear_error();
    return r;
}

/* crypto.diffieHellman({ privateKey, publicKey }): the shared secret of two
 * PEM keys in out; -2 - n when out is too small, -1 on failure (the error
 * queue says why). */
double __kml_native_crypto_derive_secret(const char *privPem, const char *pubPem, void *out, long long outLen) {
    ERR_clear_error();
    EVP_PKEY *priv = knc_pem_key(privPem);
    if (!priv) return -1;
    EVP_PKEY *pub = knc_pem_key(pubPem);
    double r = -1;
    EVP_PKEY_CTX *ctx = pub ? EVP_PKEY_CTX_new(priv, NULL) : NULL;
    size_t n = 0;
    if (ctx && EVP_PKEY_derive_init(ctx) > 0 && EVP_PKEY_derive_set_peer(ctx, pub) > 0 &&
        EVP_PKEY_derive(ctx, NULL, &n) > 0) {
        if ((long long)n > outLen) {
            r = -2 - (double)n;
        } else if (EVP_PKEY_derive(ctx, out, &n) > 0) {
            r = (double)n;
        }
    }
    EVP_PKEY_CTX_free(ctx);
    EVP_PKEY_free(pub);
    EVP_PKEY_free(priv);
    return r;
}
