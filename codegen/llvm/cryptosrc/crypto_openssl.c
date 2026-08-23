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
    }
    return NULL;
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
