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

static const EVP_MD *kml_md(long long hashId) {
    switch (hashId) {
    case 1: return EVP_sha1();
    case 2: return EVP_sha256();
    case 3: return EVP_sha384();
    case 4: return EVP_sha512();
    case 5: return EVP_md5(); /* crypto.createHash('md5') — TDD-00159 */
    }
    return NULL;
}

long long __kml_crypto_digest(long long hashId, const unsigned char *data,
                              long long len, unsigned char *out,
                              long long *outLen) {
    const EVP_MD *md = kml_md(hashId);
    unsigned int n = 0;
    if (!md) return -3;
    if (!EVP_Digest(data, (size_t)len, out, &n, md, NULL)) return -1;
    *outLen = (long long)n;
    return 0;
}

long long __kml_crypto_memeq(const unsigned char *a, const unsigned char *b,
                             long long len) {
    unsigned char diff = 0;
    long long i;
    for (i = 0; i < len; i++) diff |= (unsigned char)(a[i] ^ b[i]);
    return diff == 0 ? 1 : 0;
}

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

static const char *kml_md_name(long long hashId) {
    switch (hashId) {
    case 1: return "SHA1";
    case 2: return "SHA256";
    case 3: return "SHA384";
    case 4: return "SHA512";
    case 5: return "MD5"; /* crypto.createHmac('md5') — TDD-00159 */
    }
    return NULL;
}

/* Streaming digest — crypto.createHash's Hash object (ADR-00637): a real
 * EVP_MD_CTX so update()/digest() hash incrementally rather than buffering. */
void *__kml_crypto_hash_new(long long hashId) {
    const EVP_MD *md = kml_md(hashId);
    EVP_MD_CTX *ctx;
    if (!md) return NULL;
    ctx = EVP_MD_CTX_new();
    if (!ctx) return NULL;
    if (!EVP_DigestInit_ex(ctx, md, NULL)) { EVP_MD_CTX_free(ctx); return NULL; }
    return ctx;
}

long long __kml_crypto_hash_update(void *ctx, const unsigned char *data,
                                   long long len) {
    if (!ctx) return -1;
    return EVP_DigestUpdate((EVP_MD_CTX *)ctx, data, (size_t)len) ? 0 : -1;
}

long long __kml_crypto_hash_final(void *ctx, unsigned char *out,
                                  long long *outLen) {
    unsigned int n = 0;
    int ok;
    if (!ctx) return -1;
    ok = EVP_DigestFinal_ex((EVP_MD_CTX *)ctx, out, &n);
    EVP_MD_CTX_free((EVP_MD_CTX *)ctx);
    if (!ok) return -1;
    *outLen = (long long)n;
    return 0;
}

/* Streaming HMAC — crypto.createHmac's Hmac object (ADR-00637). Wrap the
 * EVP_MAC and its ctx so both free together at final(). */
struct kml_hmac_stream { EVP_MAC *mac; EVP_MAC_CTX *ctx; };

void *__kml_crypto_hmac_new(long long hashId, const unsigned char *key,
                            long long keyLen) {
    const char *mdName = kml_md_name(hashId);
    struct kml_hmac_stream *w;
    OSSL_PARAM params[2];
    if (!mdName) return NULL;
    w = (struct kml_hmac_stream *)malloc(sizeof(*w));
    if (!w) return NULL;
    w->mac = EVP_MAC_fetch(NULL, "HMAC", NULL);
    if (!w->mac) { free(w); return NULL; }
    w->ctx = EVP_MAC_CTX_new(w->mac);
    if (!w->ctx) { EVP_MAC_free(w->mac); free(w); return NULL; }
    params[0] = OSSL_PARAM_construct_utf8_string(OSSL_MAC_PARAM_DIGEST,
                                                 (char *)mdName, 0);
    params[1] = OSSL_PARAM_construct_end();
    if (!EVP_MAC_init(w->ctx, key, (size_t)keyLen, params)) {
        EVP_MAC_CTX_free(w->ctx);
        EVP_MAC_free(w->mac);
        free(w);
        return NULL;
    }
    return w;
}

long long __kml_crypto_hmac_update(void *h, const unsigned char *data,
                                   long long len) {
    struct kml_hmac_stream *w = (struct kml_hmac_stream *)h;
    if (!w) return -1;
    return EVP_MAC_update(w->ctx, data, (size_t)len) ? 0 : -1;
}

long long __kml_crypto_hmac_final(void *h, unsigned char *out,
                                  long long *outLen) {
    struct kml_hmac_stream *w = (struct kml_hmac_stream *)h;
    size_t n = 0;
    int ok;
    if (!w) return -1;
    ok = EVP_MAC_final(w->ctx, out, &n, 64);
    EVP_MAC_CTX_free(w->ctx);
    EVP_MAC_free(w->mac);
    free(w);
    if (!ok) return -1;
    *outLen = (long long)n;
    return 0;
}

long long __kml_crypto_hmac_sign(long long hashId, const unsigned char *key,
                                 long long keyLen, const unsigned char *data,
                                 long long len, unsigned char *out,
                                 long long *outLen) {
    const char *mdName = kml_md_name(hashId);
    EVP_MAC *mac;
    EVP_MAC_CTX *ctx;
    OSSL_PARAM params[2];
    size_t n = 0;
    long long rc = -1;
    if (!mdName) return -3;
    mac = EVP_MAC_fetch(NULL, "HMAC", NULL);
    if (!mac) return -1;
    ctx = EVP_MAC_CTX_new(mac);
    if (!ctx) { EVP_MAC_free(mac); return -1; }
    params[0] = OSSL_PARAM_construct_utf8_string(OSSL_MAC_PARAM_DIGEST,
                                                 (char *)mdName, 0);
    params[1] = OSSL_PARAM_construct_end();
    if (EVP_MAC_init(ctx, key, (size_t)keyLen, params) &&
        EVP_MAC_update(ctx, data, (size_t)len) &&
        EVP_MAC_final(ctx, out, &n, 64)) {
        *outLen = (long long)n;
        rc = 0;
    }
    EVP_MAC_CTX_free(ctx);
    EVP_MAC_free(mac);
    return rc;
}

static const EVP_CIPHER *kml_aes(long long keyLen, int gcm) {
    switch (keyLen) {
    case 16: return gcm ? EVP_aes_128_gcm() : EVP_aes_128_cbc();
    case 24: return gcm ? EVP_aes_192_gcm() : EVP_aes_192_cbc();
    case 32: return gcm ? EVP_aes_256_gcm() : EVP_aes_256_cbc();
    }
    return NULL;
}

long long __kml_crypto_aes_gcm(long long encrypt, const unsigned char *key,
                               long long keyLen, const unsigned char *iv,
                               long long ivLen, const unsigned char *aad,
                               long long aadLen, long long tagBits,
                               const unsigned char *in, long long inLen,
                               unsigned char **out, long long *outLen) {
    const EVP_CIPHER *ciph = kml_aes(keyLen, 1);
    EVP_CIPHER_CTX *ctx;
    unsigned char *buf;
    int outl = 0, finl = 0;
    long long tagBytes = tagBits / 8;
    long long rc = -1;
    if (!ciph) return -2;
    if (tagBytes < 4 || tagBytes > 16) return -1;
    if (!encrypt && inLen < tagBytes) return -1;
    ctx = EVP_CIPHER_CTX_new();
    if (!ctx) return -1;
    if (encrypt) {
        buf = (unsigned char *)malloc((size_t)(inLen + tagBytes) + 1);
        if (buf &&
            EVP_EncryptInit_ex(ctx, ciph, NULL, NULL, NULL) &&
            EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_GCM_SET_IVLEN, (int)ivLen, NULL) &&
            EVP_EncryptInit_ex(ctx, NULL, NULL, key, iv) &&
            (aadLen == 0 ||
             EVP_EncryptUpdate(ctx, NULL, &outl, aad, (int)aadLen)) &&
            EVP_EncryptUpdate(ctx, buf, &outl, in, (int)inLen) &&
            EVP_EncryptFinal_ex(ctx, buf + outl, &finl) &&
            EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_GCM_GET_TAG, (int)tagBytes,
                                buf + inLen)) {
            *out = buf;
            *outLen = inLen + tagBytes;
            rc = 0;
        } else {
            free(buf);
        }
    } else {
        long long ctLen = inLen - tagBytes;
        buf = (unsigned char *)malloc((size_t)ctLen + 1);
        if (buf &&
            EVP_DecryptInit_ex(ctx, ciph, NULL, NULL, NULL) &&
            EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_GCM_SET_IVLEN, (int)ivLen, NULL) &&
            EVP_DecryptInit_ex(ctx, NULL, NULL, key, iv) &&
            (aadLen == 0 ||
             EVP_DecryptUpdate(ctx, NULL, &outl, aad, (int)aadLen)) &&
            EVP_DecryptUpdate(ctx, buf, &outl, in, (int)ctLen) &&
            EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_GCM_SET_TAG, (int)tagBytes,
                                (void *)(in + ctLen)) &&
            EVP_DecryptFinal_ex(ctx, buf + outl, &finl)) {
            *out = buf;
            *outLen = ctLen;
            rc = 0;
        } else {
            free(buf);
        }
    }
    EVP_CIPHER_CTX_free(ctx);
    return rc;
}

/* ── key derivation: PBKDF2 / HKDF ────────────────────────────────────────── */

long long __kml_crypto_pbkdf2(long long hashId, const unsigned char *pw,
                              long long pwLen, const unsigned char *salt,
                              long long saltLen, long long iterations,
                              unsigned char *out, long long outLen) {
    const EVP_MD *md = kml_md(hashId);
    if (!md) return -3;
    if (iterations <= 0 || outLen <= 0) return -1;
    if (!PKCS5_PBKDF2_HMAC((const char *)pw, (int)pwLen, salt, (int)saltLen,
                           (int)iterations, md, (int)outLen, out))
        return -1;
    return 0;
}

long long __kml_crypto_hkdf(long long hashId, const unsigned char *ikm,
                            long long ikmLen, const unsigned char *salt,
                            long long saltLen, const unsigned char *info,
                            long long infoLen, unsigned char *out,
                            long long outLen) {
    const char *mdName = kml_md_name(hashId);
    EVP_KDF *kdf;
    EVP_KDF_CTX *ctx;
    OSSL_PARAM params[5];
    int i = 0;
    long long rc = -1;
    if (!mdName) return -3;
    if (outLen <= 0) return -1;
    kdf = EVP_KDF_fetch(NULL, "HKDF", NULL);
    if (!kdf) return -1;
    ctx = EVP_KDF_CTX_new(kdf);
    if (!ctx) { EVP_KDF_free(kdf); return -1; }
    params[i++] = OSSL_PARAM_construct_utf8_string(OSSL_KDF_PARAM_DIGEST,
                                                   (char *)mdName, 0);
    params[i++] = OSSL_PARAM_construct_octet_string(OSSL_KDF_PARAM_KEY,
                                                    (void *)ikm, (size_t)ikmLen);
    params[i++] = OSSL_PARAM_construct_octet_string(OSSL_KDF_PARAM_SALT,
                                                    (void *)salt, (size_t)saltLen);
    params[i++] = OSSL_PARAM_construct_octet_string(OSSL_KDF_PARAM_INFO,
                                                    (void *)info, (size_t)infoLen);
    params[i] = OSSL_PARAM_construct_end();
    if (EVP_KDF_derive(ctx, out, (size_t)outLen, params) > 0) rc = 0;
    EVP_KDF_CTX_free(ctx);
    EVP_KDF_free(kdf);
    return rc;
}

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

long long __kml_crypto_gen_rsa(long long modulusBits, unsigned char **pkcs8,
                               long long *pkcs8Len, unsigned char **spki,
                               long long *spkiLen) {
    EVP_PKEY *pkey = EVP_PKEY_Q_keygen(NULL, NULL, "RSA", (size_t)modulusBits);
    long long rc;
    if (!pkey) return -1;
    rc = kml_pkey_to_der(pkey, pkcs8, pkcs8Len, spki, spkiLen);
    EVP_PKEY_free(pkey);
    return rc;
}

long long __kml_crypto_gen_ec(long long curveId, unsigned char **pkcs8,
                              long long *pkcs8Len, unsigned char **spki,
                              long long *spkiLen) {
    const char *curve = kml_curve_name(curveId);
    EVP_PKEY *pkey;
    long long rc;
    if (!curve) return -3;
    pkey = EVP_PKEY_Q_keygen(NULL, NULL, "EC", curve);
    if (!pkey) return -1;
    rc = kml_pkey_to_der(pkey, pkcs8, pkcs8Len, spki, spkiLen);
    EVP_PKEY_free(pkey);
    return rc;
}

long long __kml_crypto_rsa_oaep(long long encrypt, long long hashId,
                                const unsigned char *keyDer, long long keyDerLen,
                                long long isPriv, const unsigned char *label,
                                long long labelLen, const unsigned char *in,
                                long long inLen, unsigned char **out,
                                long long *outLen) {
    const EVP_MD *md = kml_md(hashId);
    EVP_PKEY *pkey;
    EVP_PKEY_CTX *ctx;
    size_t olen = 0;
    unsigned char *buf = NULL;
    long long rc = -1;
    if (!md) return -3;
    pkey = kml_pkey_from_der(keyDer, keyDerLen, isPriv);
    if (!pkey) return -2;
    ctx = EVP_PKEY_CTX_new(pkey, NULL);
    if (ctx &&
        (encrypt ? EVP_PKEY_encrypt_init(ctx) : EVP_PKEY_decrypt_init(ctx)) > 0 &&
        EVP_PKEY_CTX_set_rsa_padding(ctx, RSA_PKCS1_OAEP_PADDING) > 0 &&
        EVP_PKEY_CTX_set_rsa_oaep_md(ctx, md) > 0 &&
        EVP_PKEY_CTX_set_rsa_mgf1_md(ctx, md) > 0) {
        int labelOK = 1;
        if (labelLen > 0) {
            /* set0 takes ownership of an OPENSSL_malloc'd copy */
            unsigned char *lc = (unsigned char *)OPENSSL_malloc((size_t)labelLen);
            if (lc) memcpy(lc, label, (size_t)labelLen);
            labelOK = lc &&
                EVP_PKEY_CTX_set0_rsa_oaep_label(ctx, lc, (int)labelLen) > 0;
        }
        if (labelOK &&
            (encrypt ? EVP_PKEY_encrypt(ctx, NULL, &olen, in, (size_t)inLen)
                     : EVP_PKEY_decrypt(ctx, NULL, &olen, in, (size_t)inLen)) > 0) {
            buf = (unsigned char *)malloc(olen + 1);
            if (buf &&
                (encrypt ? EVP_PKEY_encrypt(ctx, buf, &olen, in, (size_t)inLen)
                         : EVP_PKEY_decrypt(ctx, buf, &olen, in, (size_t)inLen)) > 0) {
                *out = buf;
                *outLen = (long long)olen;
                rc = 0;
            } else {
                free(buf);
            }
        }
    }
    EVP_PKEY_CTX_free(ctx);
    EVP_PKEY_free(pkey);
    return rc;
}

long long __kml_crypto_rsa_pss_sign(long long hashId, long long saltLen,
                                    const unsigned char *pkcs8, long long pkcs8Len,
                                    const unsigned char *data, long long len,
                                    unsigned char **sig, long long *sigLen) {
    const EVP_MD *md = kml_md(hashId);
    EVP_PKEY *pkey;
    EVP_MD_CTX *mctx;
    EVP_PKEY_CTX *pctx = NULL;
    size_t slen = 0;
    unsigned char *buf = NULL;
    long long rc = -1;
    if (!md) return -3;
    pkey = kml_pkey_from_der(pkcs8, pkcs8Len, 1);
    if (!pkey) return -2;
    mctx = EVP_MD_CTX_new();
    if (mctx &&
        EVP_DigestSignInit(mctx, &pctx, md, NULL, pkey) > 0 &&
        EVP_PKEY_CTX_set_rsa_padding(pctx, RSA_PKCS1_PSS_PADDING) > 0 &&
        EVP_PKEY_CTX_set_rsa_pss_saltlen(pctx, (int)saltLen) > 0 &&
        EVP_DigestSign(mctx, NULL, &slen, data, (size_t)len) > 0) {
        buf = (unsigned char *)malloc(slen + 1);
        if (buf && EVP_DigestSign(mctx, buf, &slen, data, (size_t)len) > 0) {
            *sig = buf;
            *sigLen = (long long)slen;
            rc = 0;
        } else {
            free(buf);
        }
    }
    EVP_MD_CTX_free(mctx);
    EVP_PKEY_free(pkey);
    return rc;
}

long long __kml_crypto_rsa_pss_verify(long long hashId, long long saltLen,
                                      const unsigned char *spki, long long spkiLen,
                                      const unsigned char *data, long long len,
                                      const unsigned char *sig, long long sigLen) {
    const EVP_MD *md = kml_md(hashId);
    EVP_PKEY *pkey;
    EVP_MD_CTX *mctx;
    EVP_PKEY_CTX *pctx = NULL;
    long long rc = -1;
    if (!md) return -3;
    pkey = kml_pkey_from_der(spki, spkiLen, 0);
    if (!pkey) return -2;
    mctx = EVP_MD_CTX_new();
    if (mctx &&
        EVP_DigestVerifyInit(mctx, &pctx, md, NULL, pkey) > 0 &&
        EVP_PKEY_CTX_set_rsa_padding(pctx, RSA_PKCS1_PSS_PADDING) > 0 &&
        EVP_PKEY_CTX_set_rsa_pss_saltlen(pctx, (int)saltLen) > 0) {
        rc = EVP_DigestVerify(mctx, sig, (size_t)sigLen, data, (size_t)len) == 1
                 ? 1 : 0;
    }
    EVP_MD_CTX_free(mctx);
    EVP_PKEY_free(pkey);
    return rc;
}

/* Web Crypto ECDSA signatures are raw r||s (2 × curve bytes); OpenSSL
 * produces/consumes DER — convert both ways here. */
long long __kml_crypto_ecdsa_sign(long long curveId, long long hashId,
                                  const unsigned char *pkcs8, long long pkcs8Len,
                                  const unsigned char *data, long long len,
                                  unsigned char **sig, long long *sigLen) {
    const EVP_MD *md = kml_md(hashId);
    long long cb = kml_curve_bytes(curveId);
    EVP_PKEY *pkey;
    EVP_MD_CTX *mctx;
    unsigned char der[256];
    size_t derLen = sizeof(der);
    long long rc = -1;
    if (!md || cb == 0) return -3;
    pkey = kml_pkey_from_der(pkcs8, pkcs8Len, 1);
    if (!pkey) return -2;
    mctx = EVP_MD_CTX_new();
    if (mctx && EVP_DigestSignInit(mctx, NULL, md, NULL, pkey) > 0 &&
        EVP_DigestSign(mctx, der, &derLen, data, (size_t)len) > 0) {
        const unsigned char *p = der;
        ECDSA_SIG *es = d2i_ECDSA_SIG(NULL, &p, (long)derLen);
        if (es) {
            unsigned char *raw = (unsigned char *)malloc((size_t)(2 * cb) + 1);
            if (raw &&
                BN_bn2binpad(ECDSA_SIG_get0_r(es), raw, (int)cb) == (int)cb &&
                BN_bn2binpad(ECDSA_SIG_get0_s(es), raw + cb, (int)cb) == (int)cb) {
                *sig = raw;
                *sigLen = 2 * cb;
                rc = 0;
            } else {
                free(raw);
            }
            ECDSA_SIG_free(es);
        }
    }
    EVP_MD_CTX_free(mctx);
    EVP_PKEY_free(pkey);
    return rc;
}

long long __kml_crypto_ecdsa_verify(long long curveId, long long hashId,
                                    const unsigned char *spki, long long spkiLen,
                                    const unsigned char *data, long long len,
                                    const unsigned char *sig, long long sigLen) {
    const EVP_MD *md = kml_md(hashId);
    long long cb = kml_curve_bytes(curveId);
    EVP_PKEY *pkey;
    EVP_MD_CTX *mctx;
    ECDSA_SIG *es;
    BIGNUM *r, *s;
    unsigned char *der = NULL;
    int derLen;
    long long rc = -1;
    if (!md || cb == 0) return -3;
    if (sigLen != 2 * cb) return 0;
    pkey = kml_pkey_from_der(spki, spkiLen, 0);
    if (!pkey) return -2;
    es = ECDSA_SIG_new();
    r = BN_bin2bn(sig, (int)cb, NULL);
    s = BN_bin2bn(sig + cb, (int)cb, NULL);
    if (es && r && s && ECDSA_SIG_set0(es, r, s)) {
        derLen = i2d_ECDSA_SIG(es, &der);
        if (derLen > 0) {
            mctx = EVP_MD_CTX_new();
            if (mctx && EVP_DigestVerifyInit(mctx, NULL, md, NULL, pkey) > 0) {
                rc = EVP_DigestVerify(mctx, der, (size_t)derLen, data,
                                      (size_t)len) == 1 ? 1 : 0;
            }
            EVP_MD_CTX_free(mctx);
            OPENSSL_free(der);
        }
        ECDSA_SIG_free(es); /* owns r/s after set0 */
    } else {
        if (!es || !r || !s) { BN_free(r); BN_free(s); }
        ECDSA_SIG_free(es);
    }
    EVP_PKEY_free(pkey);
    return rc;
}

/* Build an EC public EVP_PKEY from an uncompressed point (04||X||Y). */
static EVP_PKEY *kml_ec_pub_from_point(long long curveId,
                                       const unsigned char *pt, long long ptLen) {
    const char *curve = kml_curve_name(curveId);
    EVP_PKEY_CTX *ctx;
    EVP_PKEY *pkey = NULL;
    OSSL_PARAM_BLD *bld;
    OSSL_PARAM *params;
    if (!curve) return NULL;
    bld = OSSL_PARAM_BLD_new();
    if (!bld) return NULL;
    OSSL_PARAM_BLD_push_utf8_string(bld, OSSL_PKEY_PARAM_GROUP_NAME, curve, 0);
    OSSL_PARAM_BLD_push_octet_string(bld, OSSL_PKEY_PARAM_PUB_KEY, pt,
                                     (size_t)ptLen);
    params = OSSL_PARAM_BLD_to_param(bld);
    ctx = EVP_PKEY_CTX_new_from_name(NULL, "EC", NULL);
    if (ctx && params && EVP_PKEY_fromdata_init(ctx) > 0)
        EVP_PKEY_fromdata(ctx, &pkey, EVP_PKEY_PUBLIC_KEY, params);
    EVP_PKEY_CTX_free(ctx);
    OSSL_PARAM_free(params);
    OSSL_PARAM_BLD_free(bld);
    return pkey;
}

long long __kml_crypto_ec_raw_to_spki(long long curveId,
                                      const unsigned char *raw, long long rawLen,
                                      unsigned char **spki, long long *spkiLen) {
    EVP_PKEY *pkey = kml_ec_pub_from_point(curveId, raw, rawLen);
    long long rc;
    if (!pkey) return -2;
    rc = kml_pkey_to_der(pkey, NULL, NULL, spki, spkiLen);
    EVP_PKEY_free(pkey);
    return rc;
}

long long __kml_crypto_ec_spki_to_raw(long long curveId,
                                      const unsigned char *spki, long long spkiLen,
                                      unsigned char **raw, long long *rawLen) {
    EVP_PKEY *pkey = kml_pkey_from_der(spki, spkiLen, 0);
    size_t n = 0;
    long long rc = -2;
    (void)curveId;
    if (!pkey) return -2;
    if (EVP_PKEY_get_octet_string_param(pkey, OSSL_PKEY_PARAM_PUB_KEY, NULL, 0,
                                        &n) &&
        n > 0) {
        unsigned char *buf = (unsigned char *)malloc(n + 1);
        if (buf && EVP_PKEY_get_octet_string_param(
                       pkey, OSSL_PKEY_PARAM_PUB_KEY, buf, n, &n)) {
            *raw = buf;
            *rawLen = (long long)n;
            rc = 0;
        } else {
            free(buf);
            rc = -1;
        }
    }
    EVP_PKEY_free(pkey);
    return rc;
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

long long __kml_crypto_jwk_export_rsa(long long isPriv,
                                      const unsigned char *der, long long derLen,
                                      char **n, char **e, char **d, char **p,
                                      char **q, char **dp, char **dq, char **qi) {
    EVP_PKEY *pkey = kml_pkey_from_der(der, derLen, isPriv);
    if (!pkey) return -2;
    *n = kml_pkey_bn_b64u(pkey, OSSL_PKEY_PARAM_RSA_N);
    *e = kml_pkey_bn_b64u(pkey, OSSL_PKEY_PARAM_RSA_E);
    *d = *p = *q = *dp = *dq = *qi = NULL;
    if (isPriv) {
        *d = kml_pkey_bn_b64u(pkey, OSSL_PKEY_PARAM_RSA_D);
        *p = kml_pkey_bn_b64u(pkey, OSSL_PKEY_PARAM_RSA_FACTOR1);
        *q = kml_pkey_bn_b64u(pkey, OSSL_PKEY_PARAM_RSA_FACTOR2);
        *dp = kml_pkey_bn_b64u(pkey, OSSL_PKEY_PARAM_RSA_EXPONENT1);
        *dq = kml_pkey_bn_b64u(pkey, OSSL_PKEY_PARAM_RSA_EXPONENT2);
        *qi = kml_pkey_bn_b64u(pkey, OSSL_PKEY_PARAM_RSA_COEFFICIENT1);
    }
    EVP_PKEY_free(pkey);
    return (*n && *e) ? 0 : -1;
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

long long __kml_crypto_jwk_export_ec(long long curveId, long long isPriv,
                                     const unsigned char *der, long long derLen,
                                     char **x, char **y, char **d) {
    EVP_PKEY *pkey = kml_pkey_from_der(der, derLen, isPriv);
    long long cb = kml_curve_bytes(curveId);
    unsigned char *pt = NULL;
    size_t ptLen = 0;
    long long rc = -1;
    *x = *y = *d = NULL;
    if (!pkey) return -2;
    if (cb == 0) { EVP_PKEY_free(pkey); return -3; }
    if (EVP_PKEY_get_octet_string_param(pkey, OSSL_PKEY_PARAM_PUB_KEY, NULL, 0,
                                        &ptLen) && ptLen == (size_t)(1 + 2 * cb)) {
        pt = (unsigned char *)malloc(ptLen);
        if (pt && EVP_PKEY_get_octet_string_param(
                      pkey, OSSL_PKEY_PARAM_PUB_KEY, pt, ptLen, &ptLen) &&
            pt[0] == 4) {
            long long xl, yl;
            if (__kml_crypto_b64url_encode(pt + 1, cb, x, &xl) == 0 &&
                __kml_crypto_b64url_encode(pt + 1 + cb, cb, y, &yl) == 0)
                rc = 0;
        }
        free(pt);
    }
    if (rc == 0 && isPriv) {
        BIGNUM *bn = NULL;
        if (EVP_PKEY_get_bn_param(pkey, OSSL_PKEY_PARAM_PRIV_KEY, &bn) && bn) {
            unsigned char *tmp = (unsigned char *)malloc((size_t)cb);
            long long dl;
            if (tmp && BN_bn2binpad(bn, tmp, (int)cb) == (int)cb &&
                __kml_crypto_b64url_encode(tmp, cb, d, &dl) == 0) {
                /* ok */
            } else {
                rc = -1;
            }
            free(tmp);
            BN_free(bn);
        } else {
            rc = -1;
        }
    }
    EVP_PKEY_free(pkey);
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

long long __kml_crypto_aes_cbc(long long encrypt, const unsigned char *key,
                               long long keyLen, const unsigned char *iv,
                               const unsigned char *in, long long inLen,
                               unsigned char **out, long long *outLen) {
    const EVP_CIPHER *ciph = kml_aes(keyLen, 0);
    EVP_CIPHER_CTX *ctx;
    unsigned char *buf;
    int outl = 0, finl = 0;
    long long rc = -1;
    if (!ciph) return -2;
    ctx = EVP_CIPHER_CTX_new();
    if (!ctx) return -1;
    buf = (unsigned char *)malloc((size_t)inLen + 16 + 1);
    if (buf) {
        if (encrypt
                ? (EVP_EncryptInit_ex(ctx, ciph, NULL, key, iv) &&
                   EVP_EncryptUpdate(ctx, buf, &outl, in, (int)inLen) &&
                   EVP_EncryptFinal_ex(ctx, buf + outl, &finl))
                : (EVP_DecryptInit_ex(ctx, ciph, NULL, key, iv) &&
                   EVP_DecryptUpdate(ctx, buf, &outl, in, (int)inLen) &&
                   EVP_DecryptFinal_ex(ctx, buf + outl, &finl))) {
            *out = buf;
            *outLen = (long long)outl + finl;
            rc = 0;
        } else {
            free(buf);
        }
    }
    EVP_CIPHER_CTX_free(ctx);
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

static void knc_md_name(const OBJ_NAME *o, void *arg) {
    knc_names_add((knc_names *)arg, o->name);
}

/* which: 0 digests, 1 ciphers, 2 the linked OpenSSL's version number. */
char *__kml_native_crypto_names(double which) {
    if (which == 2) {
        char v[32];
        snprintf(v, sizeof v, "%lu", (unsigned long)OpenSSL_version_num());
        return kml_ncrypto_str(v);
    }
    knc_names n = {0};
    OBJ_NAME_do_all_sorted(which == 0 ? OBJ_NAME_TYPE_MD_METH : OBJ_NAME_TYPE_CIPHER_METH, knc_md_name, &n);
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
    } else if (t == 2 || t == 3) {
        ctx = EVP_PKEY_CTX_new_from_name(NULL, t == 2 ? "ED25519" : "X25519", NULL);
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
double __kml_native_crypto_sign(const char *digest, const char *pem, void *pass, long long passLen, double hasPass,
                                void *data, long long len, void *out, long long outLen) {
    EVP_PKEY *k = knc_read_key(pem, 1, (const char *)pass, hasPass != 0 ? passLen : -1);
    if (!k) return -1;
    const EVP_MD *md = digest && *digest ? EVP_get_digestbyname(digest) : NULL;
    EVP_MD_CTX *ctx = EVP_MD_CTX_new();
    size_t n = 0;
    double r = -1;
    if (ctx && EVP_DigestSignInit(ctx, NULL, md, NULL, k) > 0 && EVP_DigestSign(ctx, NULL, &n, data, (size_t)len) > 0) {
        if ((long long)n > outLen) {
            r = -2 - (double)n;
        } else if (EVP_DigestSign(ctx, out, &n, data, (size_t)len) > 0) {
            r = (double)n;
        }
    }
    EVP_MD_CTX_free(ctx);
    EVP_PKEY_free(k);
    return r;
}

/* verify: 1 valid, 0 invalid, -1 a bad key. */
double __kml_native_crypto_verify(const char *digest, const char *pem, void *pass, long long passLen, double hasPass,
                                  void *data, long long len, void *sig, long long sigLen) {
    EVP_PKEY *k = knc_read_key(pem, 0, (const char *)pass, hasPass != 0 ? passLen : -1);
    if (!k) return -1;
    const EVP_MD *md = digest && *digest ? EVP_get_digestbyname(digest) : NULL;
    EVP_MD_CTX *ctx = EVP_MD_CTX_new();
    double r = 0;
    if (ctx && EVP_DigestVerifyInit(ctx, NULL, md, NULL, k) > 0)
        r = EVP_DigestVerify(ctx, sig, (size_t)sigLen, data, (size_t)len) == 1 ? 1 : 0;
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
