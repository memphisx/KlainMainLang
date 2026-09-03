/* crypto_commoncrypto.c — the Apple CommonCrypto (+ Security.framework for
 * the asymmetric work) implementation of the __kml_crypto_* subtle-crypto
 * ABI (TDD-00104). macOS only; symmetric primitives live in libSystem so no
 * -l flag is needed, RSA/EC use SecKey. Error contract shared by every
 * backend: 0 = ok, -1 = OperationError, -2 = DataError,
 * -3 = NotSupportedError. */

#include <stddef.h>
#include <stdlib.h>
#include <string.h>
#include <CommonCrypto/CommonDigest.h>
#include <CommonCrypto/CommonHMAC.h>
#include <CommonCrypto/CommonCryptor.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

/* CC_SHA*_Update takes a CC_LONG (uint32) length, so hash in chunks to keep
 * the ABI's full i64 length honest for very large inputs. */
#define KML_CC_DIGEST(CTX, INIT, UPDATE, FINAL, DIGLEN)                        \
    do {                                                                       \
        CTX ctx;                                                               \
        long long off = 0;                                                     \
        INIT(&ctx);                                                            \
        while (off < len) {                                                    \
            long long chunk = len - off;                                       \
            if (chunk > 0x40000000LL) chunk = 0x40000000LL;                    \
            UPDATE(&ctx, data + off, (CC_LONG)chunk);                          \
            off += chunk;                                                      \
        }                                                                      \
        FINAL(out, &ctx);                                                      \
        *outLen = (DIGLEN);                                                    \
        return 0;                                                              \
    } while (0)

long long __kml_crypto_digest(long long hashId, const unsigned char *data,
                              long long len, unsigned char *out,
                              long long *outLen) {
    switch (hashId) {
    case 1:
        KML_CC_DIGEST(CC_SHA1_CTX, CC_SHA1_Init, CC_SHA1_Update, CC_SHA1_Final,
                      CC_SHA1_DIGEST_LENGTH);
    case 2:
        KML_CC_DIGEST(CC_SHA256_CTX, CC_SHA256_Init, CC_SHA256_Update,
                      CC_SHA256_Final, CC_SHA256_DIGEST_LENGTH);
    case 3:
        KML_CC_DIGEST(CC_SHA512_CTX, CC_SHA384_Init, CC_SHA384_Update,
                      CC_SHA384_Final, CC_SHA384_DIGEST_LENGTH);
    case 4:
        KML_CC_DIGEST(CC_SHA512_CTX, CC_SHA512_Init, CC_SHA512_Update,
                      CC_SHA512_Final, CC_SHA512_DIGEST_LENGTH);
    case 5: /* crypto.createHash('md5') — TDD-00159 */
        KML_CC_DIGEST(CC_MD5_CTX, CC_MD5_Init, CC_MD5_Update,
                      CC_MD5_Final, CC_MD5_DIGEST_LENGTH);
    }
    return -3;
}

/* Streaming digest — crypto.createHash's Hash object (ADR-00637): a tagged
 * union of the CC context types, so update()/digest() hash incrementally. */
struct kml_cc_hash {
    long long algo;
    union {
        CC_SHA1_CTX s1;
        CC_SHA256_CTX s256;
        CC_SHA512_CTX s512; /* SHA-384 shares the 512 context, per the map above */
        CC_MD5_CTX md5;
    } u;
};

void *__kml_crypto_hash_new(long long hashId) {
    struct kml_cc_hash *h;
    if (hashId < 1 || hashId > 5) return NULL;
    h = (struct kml_cc_hash *)malloc(sizeof(*h));
    if (!h) return NULL;
    h->algo = hashId;
    switch (hashId) {
    case 1: CC_SHA1_Init(&h->u.s1); break;
    case 2: CC_SHA256_Init(&h->u.s256); break;
    case 3: CC_SHA384_Init(&h->u.s512); break;
    case 4: CC_SHA512_Init(&h->u.s512); break;
    case 5: CC_MD5_Init(&h->u.md5); break;
    }
    return h;
}

long long __kml_crypto_hash_update(void *ctx, const unsigned char *data,
                                   long long len) {
    struct kml_cc_hash *h = (struct kml_cc_hash *)ctx;
    long long off = 0;
    if (!h) return -1;
    while (off < len) {
        long long chunk = len - off;
        if (chunk > 0x40000000LL) chunk = 0x40000000LL;
        switch (h->algo) {
        case 1: CC_SHA1_Update(&h->u.s1, data + off, (CC_LONG)chunk); break;
        case 2: CC_SHA256_Update(&h->u.s256, data + off, (CC_LONG)chunk); break;
        case 3: CC_SHA384_Update(&h->u.s512, data + off, (CC_LONG)chunk); break;
        case 4: CC_SHA512_Update(&h->u.s512, data + off, (CC_LONG)chunk); break;
        case 5: CC_MD5_Update(&h->u.md5, data + off, (CC_LONG)chunk); break;
        }
        off += chunk;
    }
    return 0;
}

long long __kml_crypto_hash_final(void *ctx, unsigned char *out,
                                  long long *outLen) {
    struct kml_cc_hash *h = (struct kml_cc_hash *)ctx;
    if (!h) return -1;
    switch (h->algo) {
    case 1: CC_SHA1_Final(out, &h->u.s1); *outLen = CC_SHA1_DIGEST_LENGTH; break;
    case 2: CC_SHA256_Final(out, &h->u.s256); *outLen = CC_SHA256_DIGEST_LENGTH; break;
    case 3: CC_SHA384_Final(out, &h->u.s512); *outLen = CC_SHA384_DIGEST_LENGTH; break;
    case 4: CC_SHA512_Final(out, &h->u.s512); *outLen = CC_SHA512_DIGEST_LENGTH; break;
    case 5: CC_MD5_Final(out, &h->u.md5); *outLen = CC_MD5_DIGEST_LENGTH; break;
    }
    free(h);
    return 0;
}

/* Streaming HMAC — crypto.createHmac's Hmac object (ADR-00637). */
struct kml_cc_hmac_stream {
    CCHmacContext ctx;
    long long dlen;
};

void *__kml_crypto_hmac_new(long long hashId, const unsigned char *key,
                            long long keyLen) {
    CCHmacAlgorithm alg;
    long long dlen;
    struct kml_cc_hmac_stream *h;
    switch (hashId) {
    case 1: alg = kCCHmacAlgSHA1; dlen = CC_SHA1_DIGEST_LENGTH; break;
    case 2: alg = kCCHmacAlgSHA256; dlen = CC_SHA256_DIGEST_LENGTH; break;
    case 3: alg = kCCHmacAlgSHA384; dlen = CC_SHA384_DIGEST_LENGTH; break;
    case 4: alg = kCCHmacAlgSHA512; dlen = CC_SHA512_DIGEST_LENGTH; break;
    case 5: alg = kCCHmacAlgMD5; dlen = CC_MD5_DIGEST_LENGTH; break;
    default: return NULL;
    }
    h = (struct kml_cc_hmac_stream *)malloc(sizeof(*h));
    if (!h) return NULL;
    CCHmacInit(&h->ctx, alg, key, (size_t)keyLen);
    h->dlen = dlen;
    return h;
}

long long __kml_crypto_hmac_update(void *ctx, const unsigned char *data,
                                   long long len) {
    struct kml_cc_hmac_stream *h = (struct kml_cc_hmac_stream *)ctx;
    if (!h) return -1;
    CCHmacUpdate(&h->ctx, data, (size_t)len);
    return 0;
}

long long __kml_crypto_hmac_final(void *ctx, unsigned char *out,
                                  long long *outLen) {
    struct kml_cc_hmac_stream *h = (struct kml_cc_hmac_stream *)ctx;
    if (!h) return -1;
    CCHmacFinal(&h->ctx, out);
    *outLen = h->dlen;
    free(h);
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

long long __kml_crypto_hmac_sign(long long hashId, const unsigned char *key,
                                 long long keyLen, const unsigned char *data,
                                 long long len, unsigned char *out,
                                 long long *outLen) {
    switch (hashId) {
    case 1:
        CCHmac(kCCHmacAlgSHA1, key, (size_t)keyLen, data, (size_t)len, out);
        *outLen = CC_SHA1_DIGEST_LENGTH;
        return 0;
    case 2:
        CCHmac(kCCHmacAlgSHA256, key, (size_t)keyLen, data, (size_t)len, out);
        *outLen = CC_SHA256_DIGEST_LENGTH;
        return 0;
    case 3:
        CCHmac(kCCHmacAlgSHA384, key, (size_t)keyLen, data, (size_t)len, out);
        *outLen = CC_SHA384_DIGEST_LENGTH;
        return 0;
    case 4:
        CCHmac(kCCHmacAlgSHA512, key, (size_t)keyLen, data, (size_t)len, out);
        *outLen = CC_SHA512_DIGEST_LENGTH;
        return 0;
    }
    return -3;
}

long long __kml_crypto_aes_cbc(long long encrypt, const unsigned char *key,
                               long long keyLen, const unsigned char *iv,
                               const unsigned char *in, long long inLen,
                               unsigned char **out, long long *outLen) {
    unsigned char *buf;
    size_t moved = 0;
    CCCryptorStatus st;
    if (keyLen != 16 && keyLen != 24 && keyLen != 32) return -2;
    buf = (unsigned char *)malloc((size_t)inLen + 16 + 1);
    if (!buf) return -1;
    st = CCCrypt(encrypt ? kCCEncrypt : kCCDecrypt, kCCAlgorithmAES,
                 kCCOptionPKCS7Padding, key, (size_t)keyLen, iv, in,
                 (size_t)inLen, buf, (size_t)inLen + 16, &moved);
    if (st != kCCSuccess) { free(buf); return -1; }
    *out = buf;
    *outLen = (long long)moved;
    return 0;
}

/* ── key derivation: PBKDF2 (CCKeyDerivationPBKDF) / HKDF (RFC 5869 over
 * CCHmac — CommonCrypto's public API has no HKDF) ─────────────────────────── */

#include <CommonCrypto/CommonKeyDerivation.h>

static int kml_cc_hmac_alg(long long hashId, CCHmacAlgorithm *alg,
                           long long *hlen) {
    switch (hashId) {
    case 1: *alg = kCCHmacAlgSHA1; *hlen = CC_SHA1_DIGEST_LENGTH; return 1;
    case 2: *alg = kCCHmacAlgSHA256; *hlen = CC_SHA256_DIGEST_LENGTH; return 1;
    case 3: *alg = kCCHmacAlgSHA384; *hlen = CC_SHA384_DIGEST_LENGTH; return 1;
    case 4: *alg = kCCHmacAlgSHA512; *hlen = CC_SHA512_DIGEST_LENGTH; return 1;
    }
    return 0;
}

long long __kml_crypto_pbkdf2(long long hashId, const unsigned char *pw,
                              long long pwLen, const unsigned char *salt,
                              long long saltLen, long long iterations,
                              unsigned char *out, long long outLen) {
    CCPseudoRandomAlgorithm prf;
    switch (hashId) {
    case 1: prf = kCCPRFHmacAlgSHA1; break;
    case 2: prf = kCCPRFHmacAlgSHA256; break;
    case 3: prf = kCCPRFHmacAlgSHA384; break;
    case 4: prf = kCCPRFHmacAlgSHA512; break;
    default: return -3;
    }
    if (iterations <= 0 || outLen <= 0) return -1;
    if (CCKeyDerivationPBKDF(kCCPBKDF2, (const char *)pw, (size_t)pwLen, salt,
                             (size_t)saltLen, prf, (unsigned)iterations, out,
                             (size_t)outLen) != kCCSuccess)
        return -1;
    return 0;
}

long long __kml_crypto_hkdf(long long hashId, const unsigned char *ikm,
                            long long ikmLen, const unsigned char *salt,
                            long long saltLen, const unsigned char *info,
                            long long infoLen, unsigned char *out,
                            long long outLen) {
    CCHmacAlgorithm alg;
    long long hlen;
    unsigned char prk[64], t[64];
    unsigned char zeros[64] = {0};
    long long tLen = 0, done = 0;
    unsigned char counter = 1;
    if (!kml_cc_hmac_alg(hashId, &alg, &hlen)) return -3;
    if (outLen <= 0 || outLen > 255 * hlen) return -1;
    /* extract: PRK = HMAC(salt or zeros, IKM) */
    if (saltLen > 0)
        CCHmac(alg, salt, (size_t)saltLen, ikm, (size_t)ikmLen, prk);
    else
        CCHmac(alg, zeros, (size_t)hlen, ikm, (size_t)ikmLen, prk);
    /* expand: T(i) = HMAC(PRK, T(i-1) || info || i) */
    while (done < outLen) {
        CCHmacContext ctx;
        long long n;
        CCHmacInit(&ctx, alg, prk, (size_t)hlen);
        if (tLen > 0) CCHmacUpdate(&ctx, t, (size_t)tLen);
        if (infoLen > 0) CCHmacUpdate(&ctx, info, (size_t)infoLen);
        CCHmacUpdate(&ctx, &counter, 1);
        CCHmacFinal(&ctx, t);
        tLen = hlen;
        n = outLen - done < hlen ? outLen - done : hlen;
        memcpy(out + done, t, (size_t)n);
        done += n;
        counter++;
    }
    return 0;
}

/* ── asymmetric: SecKey (Security.framework) + a mini-DER layer ─────────────
 * SecKey's external representations are PKCS#1 (RSA) and X9.63 (EC: raw
 * 04||X||Y[||K]); the Web Crypto formats are PKCS#8/SPKI DER. The mini-DER
 * reader/writer below wraps/unwraps between the two and also feeds the JWK
 * component bridge (TDD-00104). */

typedef long long ll;

/* -- DER writer: append-into-growing-buffer -- */
typedef struct {
    unsigned char *buf;
    size_t len, cap;
} kml_der;

static int kml_der_put(kml_der *d, const unsigned char *p, size_t n) {
    if (d->len + n > d->cap) {
        size_t nc = (d->cap ? d->cap * 2 : 256);
        unsigned char *nb;
        while (nc < d->len + n) nc *= 2;
        nb = (unsigned char *)realloc(d->buf, nc);
        if (!nb) return 0;
        d->buf = nb;
        d->cap = nc;
    }
    memcpy(d->buf + d->len, p, n);
    d->len += n;
    return 1;
}

static int kml_der_hdr(kml_der *d, unsigned char tag, size_t len) {
    unsigned char h[6];
    size_t n = 0;
    h[n++] = tag;
    if (len < 128) {
        h[n++] = (unsigned char)len;
    } else if (len < 256) {
        h[n++] = 0x81;
        h[n++] = (unsigned char)len;
    } else if (len < 65536) {
        h[n++] = 0x82;
        h[n++] = (unsigned char)(len >> 8);
        h[n++] = (unsigned char)len;
    } else {
        h[n++] = 0x83;
        h[n++] = (unsigned char)(len >> 16);
        h[n++] = (unsigned char)(len >> 8);
        h[n++] = (unsigned char)len;
    }
    return kml_der_put(d, h, n);
}

static size_t kml_der_hdr_size(size_t len) {
    if (len < 128) return 2;
    if (len < 256) return 3;
    if (len < 65536) return 4;
    return 5;
}

/* DER INTEGER from unsigned big-endian bytes (strips leading zeros, adds a
 * 00 pad when the high bit is set). */
static int kml_der_uint(kml_der *d, const unsigned char *v, size_t n) {
    while (n > 1 && v[0] == 0) { v++; n--; }
    if (v[0] & 0x80) {
        unsigned char z = 0;
        if (!kml_der_hdr(d, 0x02, n + 1) || !kml_der_put(d, &z, 1)) return 0;
    } else {
        if (!kml_der_hdr(d, 0x02, n)) return 0;
    }
    return kml_der_put(d, v, n);
}

static size_t kml_der_uint_size(const unsigned char *v, size_t n) {
    while (n > 1 && v[0] == 0) { v++; n--; }
    if (v[0] & 0x80) n++;
    return kml_der_hdr_size(n) + n;
}

/* -- DER reader -- */
static int kml_der_read(const unsigned char **p, const unsigned char *end,
                        unsigned char *tag, const unsigned char **content,
                        size_t *clen) {
    size_t len = 0;
    if (*p >= end) return 0;
    *tag = *(*p)++;
    if (*p >= end) return 0;
    if (**p < 128) {
        len = *(*p)++;
    } else {
        int nb = **p & 0x7f;
        (*p)++;
        if (nb < 1 || nb > 4 || *p + nb > end) return 0;
        while (nb--) len = len << 8 | *(*p)++;
    }
    if (*p + len > end) return 0;
    *content = *p;
    *clen = len;
    *p += len;
    return 1;
}

/* Read an INTEGER's unsigned value (strips the 00 pad). */
static int kml_der_read_uint(const unsigned char **p, const unsigned char *end,
                             const unsigned char **v, size_t *vlen) {
    unsigned char tag;
    if (!kml_der_read(p, end, &tag, v, vlen) || tag != 0x02 || *vlen == 0)
        return 0;
    while (*vlen > 1 && (*v)[0] == 0) { (*v)++; (*vlen)--; }
    return 1;
}

static const unsigned char kml_oid_rsa[] = {
    0x06, 0x09, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x01};
static const unsigned char kml_oid_ec[] = {
    0x06, 0x07, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02, 0x01};
static const unsigned char kml_oid_p256[] = {
    0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07};
static const unsigned char kml_oid_p384[] = {0x06, 0x05, 0x2b, 0x81, 0x04, 0x00, 0x22};
static const unsigned char kml_oid_p521[] = {0x06, 0x05, 0x2b, 0x81, 0x04, 0x00, 0x23};

static const unsigned char *kml_curve_oid(ll curveId, size_t *n) {
    switch (curveId) {
    case 1: *n = sizeof(kml_oid_p256); return kml_oid_p256;
    case 2: *n = sizeof(kml_oid_p384); return kml_oid_p384;
    case 3: *n = sizeof(kml_oid_p521); return kml_oid_p521;
    }
    return NULL;
}

static ll kml_curve_bytes(ll curveId) {
    switch (curveId) {
    case 1: return 32;
    case 2: return 48;
    case 3: return 66;
    }
    return 0;
}

/* RSA: PKCS#1 body → PKCS#8 (private) / SPKI (public). */
static ll kml_rsa_wrap(const unsigned char *pkcs1, size_t p1len, int isPriv,
                       unsigned char **out, ll *outLen) {
    kml_der d = {0};
    size_t algLen = sizeof(kml_oid_rsa) + 2; /* oid + NULL 05 00 */
    static const unsigned char derNull[] = {0x05, 0x00};
    int ok;
    if (isPriv) {
        /* SEQ{ INT 0, SEQ{oid,NULL}, OCTSTR{pkcs1} } */
        size_t body = 3 + kml_der_hdr_size(algLen) + algLen +
                      kml_der_hdr_size(p1len) + p1len;
        static const unsigned char ver0[] = {0x02, 0x01, 0x00};
        ok = kml_der_hdr(&d, 0x30, body) && kml_der_put(&d, ver0, 3) &&
             kml_der_hdr(&d, 0x30, algLen) &&
             kml_der_put(&d, kml_oid_rsa, sizeof(kml_oid_rsa)) &&
             kml_der_put(&d, derNull, 2) && kml_der_hdr(&d, 0x04, p1len) &&
             kml_der_put(&d, pkcs1, p1len);
    } else {
        /* SEQ{ SEQ{oid,NULL}, BITSTR{00, pkcs1} } */
        size_t body = kml_der_hdr_size(algLen) + algLen +
                      kml_der_hdr_size(p1len + 1) + p1len + 1;
        unsigned char zero = 0;
        ok = kml_der_hdr(&d, 0x30, body) && kml_der_hdr(&d, 0x30, algLen) &&
             kml_der_put(&d, kml_oid_rsa, sizeof(kml_oid_rsa)) &&
             kml_der_put(&d, derNull, 2) && kml_der_hdr(&d, 0x03, p1len + 1) &&
             kml_der_put(&d, &zero, 1) && kml_der_put(&d, pkcs1, p1len);
    }
    if (!ok) { free(d.buf); return -1; }
    *out = d.buf;
    *outLen = (ll)d.len;
    return 0;
}

/* PKCS#8/SPKI → the inner PKCS#1 body (pointers into the input). */
static ll kml_rsa_unwrap(const unsigned char *der, ll derLen, int isPriv,
                         const unsigned char **body, size_t *bodyLen) {
    const unsigned char *p = der, *end = der + derLen, *c;
    unsigned char tag;
    size_t clen;
    if (!kml_der_read(&p, end, &tag, &c, &clen) || tag != 0x30) return -2;
    p = c;
    end = c + clen;
    if (isPriv) {
        if (!kml_der_read(&p, end, &tag, &c, &clen) || tag != 0x02) return -2;
        if (!kml_der_read(&p, end, &tag, &c, &clen) || tag != 0x30) return -2;
        if (!kml_der_read(&p, end, &tag, &c, &clen) || tag != 0x04) return -2;
        *body = c;
        *bodyLen = clen;
    } else {
        if (!kml_der_read(&p, end, &tag, &c, &clen) || tag != 0x30) return -2;
        if (!kml_der_read(&p, end, &tag, &c, &clen) || tag != 0x03 || clen < 2)
            return -2;
        *body = c + 1; /* skip unused-bits byte */
        *bodyLen = clen - 1;
    }
    return 0;
}

/* EC: X9.63 (04||X||Y[||K]) → PKCS#8/SPKI. */
static ll kml_ec_wrap(ll curveId, const unsigned char *x963, size_t xlen,
                      int isPriv, unsigned char **out, ll *outLen) {
    kml_der d = {0};
    size_t oidn;
    const unsigned char *oid = kml_curve_oid(curveId, &oidn);
    ll cb = kml_curve_bytes(curveId);
    size_t ptLen = (size_t)(1 + 2 * cb);
    size_t algLen;
    int ok;
    if (!oid) return -3;
    algLen = sizeof(kml_oid_ec) + oidn;
    if (isPriv) {
        /* ECPrivateKey = SEQ{ INT 1, OCTSTR{k}, [1]{ BITSTR{00, point} } } */
        size_t kn = (size_t)cb;
        size_t bit = kml_der_hdr_size(ptLen + 1) + ptLen + 1;
        size_t ctx1 = kml_der_hdr_size(bit) + bit;
        size_t eck = 3 + kml_der_hdr_size(kn) + kn + ctx1;
        size_t eckTL = kml_der_hdr_size(eck) + eck;
        size_t body = 3 + kml_der_hdr_size(algLen) + algLen +
                      kml_der_hdr_size(eckTL) + eckTL;
        static const unsigned char ver0[] = {0x02, 0x01, 0x00};
        static const unsigned char ver1[] = {0x02, 0x01, 0x01};
        unsigned char zero = 0;
        if (xlen != ptLen + kn) return -2;
        ok = kml_der_hdr(&d, 0x30, body) && kml_der_put(&d, ver0, 3) &&
             kml_der_hdr(&d, 0x30, algLen) &&
             kml_der_put(&d, kml_oid_ec, sizeof(kml_oid_ec)) &&
             kml_der_put(&d, oid, oidn) && kml_der_hdr(&d, 0x04, eckTL) &&
             kml_der_hdr(&d, 0x30, eck) && kml_der_put(&d, ver1, 3) &&
             kml_der_hdr(&d, 0x04, kn) && kml_der_put(&d, x963 + ptLen, kn) &&
             kml_der_hdr(&d, 0xa1, bit) && kml_der_hdr(&d, 0x03, ptLen + 1) &&
             kml_der_put(&d, &zero, 1) && kml_der_put(&d, x963, ptLen);
    } else {
        size_t body = kml_der_hdr_size(algLen) + algLen +
                      kml_der_hdr_size(ptLen + 1) + ptLen + 1;
        unsigned char zero = 0;
        if (xlen != ptLen) return -2;
        ok = kml_der_hdr(&d, 0x30, body) && kml_der_hdr(&d, 0x30, algLen) &&
             kml_der_put(&d, kml_oid_ec, sizeof(kml_oid_ec)) &&
             kml_der_put(&d, oid, oidn) && kml_der_hdr(&d, 0x03, ptLen + 1) &&
             kml_der_put(&d, &zero, 1) && kml_der_put(&d, x963, ptLen);
    }
    if (!ok) { free(d.buf); return -1; }
    *out = d.buf;
    *outLen = (ll)d.len;
    return 0;
}

/* PKCS#8/SPKI → X9.63 (malloc'd: point, or point||K for private). */
static ll kml_ec_unwrap(ll curveId, const unsigned char *der, ll derLen,
                        int isPriv, unsigned char **x963, size_t *xlen) {
    const unsigned char *p = der, *end = der + derLen, *c;
    unsigned char tag;
    size_t clen;
    ll cb = kml_curve_bytes(curveId);
    size_t ptLen = (size_t)(1 + 2 * cb);
    if (cb == 0) return -3;
    if (!kml_der_read(&p, end, &tag, &c, &clen) || tag != 0x30) return -2;
    p = c;
    end = c + clen;
    if (isPriv) {
        const unsigned char *k = NULL, *pt = NULL;
        size_t kn = 0;
        if (!kml_der_read(&p, end, &tag, &c, &clen) || tag != 0x02) return -2;
        if (!kml_der_read(&p, end, &tag, &c, &clen) || tag != 0x30) return -2;
        if (!kml_der_read(&p, end, &tag, &c, &clen) || tag != 0x04) return -2;
        /* inside: ECPrivateKey */
        {
            const unsigned char *ip = c, *iend = c + clen, *ic;
            size_t iclen;
            if (!kml_der_read(&ip, iend, &tag, &ic, &iclen) || tag != 0x30)
                return -2;
            ip = ic;
            iend = ic + iclen;
            if (!kml_der_read(&ip, iend, &tag, &ic, &iclen) || tag != 0x02)
                return -2;
            if (!kml_der_read(&ip, iend, &tag, &ic, &iclen) || tag != 0x04)
                return -2;
            k = ic;
            kn = iclen;
            while (ip < iend) {
                if (!kml_der_read(&ip, iend, &tag, &ic, &iclen)) return -2;
                if (tag == 0xa1) {
                    const unsigned char *bp = ic, *bend = ic + iclen, *bc;
                    size_t bclen;
                    if (kml_der_read(&bp, bend, &tag, &bc, &bclen) &&
                        tag == 0x03 && bclen == ptLen + 1)
                        pt = bc + 1;
                }
            }
            if (!pt || kn > (size_t)cb) return -2;
        }
        *x963 = (unsigned char *)malloc(ptLen + (size_t)cb);
        if (!*x963) return -1;
        memcpy(*x963, pt, ptLen);
        memset(*x963 + ptLen, 0, (size_t)cb - kn); /* left-pad the scalar */
        memcpy(*x963 + ptLen + ((size_t)cb - kn), k, kn);
        *xlen = ptLen + (size_t)cb;
    } else {
        if (!kml_der_read(&p, end, &tag, &c, &clen) || tag != 0x30) return -2;
        if (!kml_der_read(&p, end, &tag, &c, &clen) || tag != 0x03 ||
            clen != ptLen + 1)
            return -2;
        *x963 = (unsigned char *)malloc(ptLen);
        if (!*x963) return -1;
        memcpy(*x963, c + 1, ptLen);
        *xlen = ptLen;
    }
    return 0;
}

/* -- SecKey helpers -- */

static SecKeyRef kml_seckey_import(const unsigned char *raw, size_t rawLen,
                                   int isRSA, int isPriv) {
    CFDataRef data = CFDataCreate(NULL, raw, (CFIndex)rawLen);
    CFMutableDictionaryRef attrs = CFDictionaryCreateMutable(
        NULL, 2, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    SecKeyRef key = NULL;
    if (data && attrs) {
        CFDictionarySetValue(attrs, kSecAttrKeyType,
                             isRSA ? kSecAttrKeyTypeRSA
                                   : kSecAttrKeyTypeECSECPrimeRandom);
        CFDictionarySetValue(attrs, kSecAttrKeyClass,
                             isPriv ? kSecAttrKeyClassPrivate
                                    : kSecAttrKeyClassPublic);
        key = SecKeyCreateWithData(data, attrs, NULL);
    }
    if (data) CFRelease(data);
    if (attrs) CFRelease(attrs);
    return key;
}

/* Import from the CryptoKey header's PKCS#8/SPKI DER. */
static SecKeyRef kml_seckey_from_der(const unsigned char *der, ll derLen,
                                     int isRSA, int isPriv, ll curveId) {
    SecKeyRef key = NULL;
    if (isRSA) {
        const unsigned char *body;
        size_t bodyLen;
        if (kml_rsa_unwrap(der, derLen, isPriv, &body, &bodyLen) != 0)
            return NULL;
        key = kml_seckey_import(body, bodyLen, 1, isPriv);
    } else {
        unsigned char *x963;
        size_t xlen;
        if (kml_ec_unwrap(curveId, der, derLen, isPriv, &x963, &xlen) != 0)
            return NULL;
        key = kml_seckey_import(x963, xlen, 0, isPriv);
        free(x963);
    }
    return key;
}

static ll kml_cfdata_out(CFDataRef d, unsigned char **out, ll *outLen) {
    size_t n;
    if (!d) return -1;
    n = (size_t)CFDataGetLength(d);
    *out = (unsigned char *)malloc(n + 1);
    if (!*out) { CFRelease(d); return -1; }
    memcpy(*out, CFDataGetBytePtr(d), n);
    *outLen = (ll)n;
    CFRelease(d);
    return 0;
}

long long __kml_crypto_gen_rsa(long long modulusBits, unsigned char **pkcs8,
                               long long *pkcs8Len, unsigned char **spki,
                               long long *spkiLen) {
    CFMutableDictionaryRef attrs = CFDictionaryCreateMutable(
        NULL, 2, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFNumberRef bits = CFNumberCreate(NULL, kCFNumberLongLongType, &modulusBits);
    SecKeyRef priv = NULL, pub = NULL;
    CFDataRef privRep = NULL, pubRep = NULL;
    ll rc = -1;
    if (attrs && bits) {
        CFDictionarySetValue(attrs, kSecAttrKeyType, kSecAttrKeyTypeRSA);
        CFDictionarySetValue(attrs, kSecAttrKeySizeInBits, bits);
        priv = SecKeyCreateRandomKey(attrs, NULL);
    }
    if (priv) pub = SecKeyCopyPublicKey(priv);
    if (priv && pub) {
        privRep = SecKeyCopyExternalRepresentation(priv, NULL);
        pubRep = SecKeyCopyExternalRepresentation(pub, NULL);
    }
    if (privRep && pubRep &&
        kml_rsa_wrap(CFDataGetBytePtr(privRep),
                     (size_t)CFDataGetLength(privRep), 1, pkcs8, pkcs8Len) == 0 &&
        kml_rsa_wrap(CFDataGetBytePtr(pubRep), (size_t)CFDataGetLength(pubRep),
                     0, spki, spkiLen) == 0)
        rc = 0;
    if (privRep) CFRelease(privRep);
    if (pubRep) CFRelease(pubRep);
    if (pub) CFRelease(pub);
    if (priv) CFRelease(priv);
    if (bits) CFRelease(bits);
    if (attrs) CFRelease(attrs);
    return rc;
}

long long __kml_crypto_gen_ec(long long curveId, unsigned char **pkcs8,
                              long long *pkcs8Len, unsigned char **spki,
                              long long *spkiLen) {
    ll bits;
    CFMutableDictionaryRef attrs;
    CFNumberRef bitsNum;
    SecKeyRef priv = NULL, pub = NULL;
    CFDataRef privRep = NULL, pubRep = NULL;
    ll rc = -1;
    switch (curveId) {
    case 1: bits = 256; break;
    case 2: bits = 384; break;
    case 3: bits = 521; break;
    default: return -3;
    }
    attrs = CFDictionaryCreateMutable(NULL, 2, &kCFTypeDictionaryKeyCallBacks,
                                      &kCFTypeDictionaryValueCallBacks);
    bitsNum = CFNumberCreate(NULL, kCFNumberLongLongType, &bits);
    if (attrs && bitsNum) {
        CFDictionarySetValue(attrs, kSecAttrKeyType,
                             kSecAttrKeyTypeECSECPrimeRandom);
        CFDictionarySetValue(attrs, kSecAttrKeySizeInBits, bitsNum);
        priv = SecKeyCreateRandomKey(attrs, NULL);
    }
    if (priv) pub = SecKeyCopyPublicKey(priv);
    if (priv && pub) {
        privRep = SecKeyCopyExternalRepresentation(priv, NULL);
        pubRep = SecKeyCopyExternalRepresentation(pub, NULL);
    }
    if (privRep && pubRep &&
        kml_ec_wrap(curveId, CFDataGetBytePtr(privRep),
                    (size_t)CFDataGetLength(privRep), 1, pkcs8, pkcs8Len) == 0 &&
        kml_ec_wrap(curveId, CFDataGetBytePtr(pubRep),
                    (size_t)CFDataGetLength(pubRep), 0, spki, spkiLen) == 0)
        rc = 0;
    if (privRep) CFRelease(privRep);
    if (pubRep) CFRelease(pubRep);
    if (pub) CFRelease(pub);
    if (priv) CFRelease(priv);
    if (bitsNum) CFRelease(bitsNum);
    if (attrs) CFRelease(attrs);
    return rc;
}

static SecKeyAlgorithm kml_oaep_alg(ll hashId) {
    switch (hashId) {
    case 1: return kSecKeyAlgorithmRSAEncryptionOAEPSHA1;
    case 2: return kSecKeyAlgorithmRSAEncryptionOAEPSHA256;
    case 3: return kSecKeyAlgorithmRSAEncryptionOAEPSHA384;
    case 4: return kSecKeyAlgorithmRSAEncryptionOAEPSHA512;
    }
    return NULL;
}

long long __kml_crypto_rsa_oaep(long long encrypt, long long hashId,
                                const unsigned char *keyDer, long long keyDerLen,
                                long long isPriv, const unsigned char *label,
                                long long labelLen, const unsigned char *in,
                                long long inLen, unsigned char **out,
                                long long *outLen) {
    SecKeyAlgorithm alg = kml_oaep_alg(hashId);
    SecKeyRef key;
    CFDataRef inData, outData = NULL;
    (void)label;
    if (!alg) return -3;
    /* SecKey has no OAEP-label parameter — a caveat of this backend. */
    if (labelLen > 0) return -3;
    key = kml_seckey_from_der(keyDer, keyDerLen, 1, (int)isPriv, 0);
    if (!key) return -2;
    inData = CFDataCreate(NULL, in, (CFIndex)inLen);
    if (inData) {
        outData = encrypt ? SecKeyCreateEncryptedData(key, alg, inData, NULL)
                          : SecKeyCreateDecryptedData(key, alg, inData, NULL);
        CFRelease(inData);
    }
    CFRelease(key);
    return kml_cfdata_out(outData, out, outLen);
}

static SecKeyAlgorithm kml_pss_alg(ll hashId) {
    switch (hashId) {
    case 1: return kSecKeyAlgorithmRSASignatureMessagePSSSHA1;
    case 2: return kSecKeyAlgorithmRSASignatureMessagePSSSHA256;
    case 3: return kSecKeyAlgorithmRSASignatureMessagePSSSHA384;
    case 4: return kSecKeyAlgorithmRSASignatureMessagePSSSHA512;
    }
    return NULL;
}

static ll kml_hash_len(ll hashId) {
    switch (hashId) {
    case 1: return 20;
    case 2: return 32;
    case 3: return 48;
    case 4: return 64;
    }
    return 0;
}

long long __kml_crypto_rsa_pss_sign(long long hashId, long long saltLen,
                                    const unsigned char *pkcs8, long long pkcs8Len,
                                    const unsigned char *data, long long len,
                                    unsigned char **sig, long long *sigLen) {
    SecKeyAlgorithm alg = kml_pss_alg(hashId);
    SecKeyRef key;
    CFDataRef inData, sigData = NULL;
    if (!alg) return -3;
    /* SecKey's PSS fixes saltLen == hash length — a caveat of this backend. */
    if (saltLen != kml_hash_len(hashId)) return -3;
    key = kml_seckey_from_der(pkcs8, pkcs8Len, 1, 1, 0);
    if (!key) return -2;
    inData = CFDataCreate(NULL, data, (CFIndex)len);
    if (inData) {
        sigData = SecKeyCreateSignature(key, alg, inData, NULL);
        CFRelease(inData);
    }
    CFRelease(key);
    return kml_cfdata_out(sigData, sig, sigLen);
}

long long __kml_crypto_rsa_pss_verify(long long hashId, long long saltLen,
                                      const unsigned char *spki, long long spkiLen,
                                      const unsigned char *data, long long len,
                                      const unsigned char *sig, long long sigLen) {
    SecKeyAlgorithm alg = kml_pss_alg(hashId);
    SecKeyRef key;
    CFDataRef inData, sigData;
    Boolean ok = 0;
    if (!alg) return -3;
    if (saltLen != kml_hash_len(hashId)) return -3;
    key = kml_seckey_from_der(spki, spkiLen, 1, 0, 0);
    if (!key) return -2;
    inData = CFDataCreate(NULL, data, (CFIndex)len);
    sigData = CFDataCreate(NULL, sig, (CFIndex)sigLen);
    if (inData && sigData)
        ok = SecKeyVerifySignature(key, alg, inData, sigData, NULL);
    if (inData) CFRelease(inData);
    if (sigData) CFRelease(sigData);
    CFRelease(key);
    return ok ? 1 : 0;
}

static SecKeyAlgorithm kml_ecdsa_alg(ll hashId) {
    switch (hashId) {
    case 1: return kSecKeyAlgorithmECDSASignatureMessageX962SHA1;
    case 2: return kSecKeyAlgorithmECDSASignatureMessageX962SHA256;
    case 3: return kSecKeyAlgorithmECDSASignatureMessageX962SHA384;
    case 4: return kSecKeyAlgorithmECDSASignatureMessageX962SHA512;
    }
    return NULL;
}

/* DER ECDSA-Sig-Value (SEQ{INT r, INT s}) → raw r||s at curve width. */
static ll kml_ecdsa_der_to_raw(const unsigned char *der, size_t derLen, ll cb,
                               unsigned char *raw) {
    const unsigned char *p = der, *end = der + derLen, *c, *r, *s;
    unsigned char tag;
    size_t clen, rl, sl;
    if (!kml_der_read(&p, end, &tag, &c, &clen) || tag != 0x30) return -1;
    p = c;
    end = c + clen;
    if (!kml_der_read_uint(&p, end, &r, &rl) || rl > (size_t)cb) return -1;
    if (!kml_der_read_uint(&p, end, &s, &sl) || sl > (size_t)cb) return -1;
    memset(raw, 0, (size_t)(2 * cb));
    memcpy(raw + cb - rl, r, rl);
    memcpy(raw + 2 * cb - sl, s, sl);
    return 0;
}

/* raw r||s → DER ECDSA-Sig-Value. */
static ll kml_ecdsa_raw_to_der(const unsigned char *raw, ll cb,
                               unsigned char **der, size_t *derLen) {
    kml_der d = {0};
    size_t body = kml_der_uint_size(raw, (size_t)cb) +
                  kml_der_uint_size(raw + cb, (size_t)cb);
    if (!kml_der_hdr(&d, 0x30, body) || !kml_der_uint(&d, raw, (size_t)cb) ||
        !kml_der_uint(&d, raw + cb, (size_t)cb)) {
        free(d.buf);
        return -1;
    }
    *der = d.buf;
    *derLen = d.len;
    return 0;
}

long long __kml_crypto_ecdsa_sign(long long curveId, long long hashId,
                                  const unsigned char *pkcs8, long long pkcs8Len,
                                  const unsigned char *data, long long len,
                                  unsigned char **sig, long long *sigLen) {
    SecKeyAlgorithm alg = kml_ecdsa_alg(hashId);
    ll cb = kml_curve_bytes(curveId);
    SecKeyRef key;
    CFDataRef inData, sigData = NULL;
    ll rc = -1;
    if (!alg || cb == 0) return -3;
    key = kml_seckey_from_der(pkcs8, pkcs8Len, 0, 1, curveId);
    if (!key) return -2;
    inData = CFDataCreate(NULL, data, (CFIndex)len);
    if (inData) {
        sigData = SecKeyCreateSignature(key, alg, inData, NULL);
        CFRelease(inData);
    }
    CFRelease(key);
    if (sigData) {
        unsigned char *raw = (unsigned char *)malloc((size_t)(2 * cb) + 1);
        if (raw && kml_ecdsa_der_to_raw(CFDataGetBytePtr(sigData),
                                        (size_t)CFDataGetLength(sigData), cb,
                                        raw) == 0) {
            *sig = raw;
            *sigLen = 2 * cb;
            rc = 0;
        } else {
            free(raw);
        }
        CFRelease(sigData);
    }
    return rc;
}

long long __kml_crypto_ecdsa_verify(long long curveId, long long hashId,
                                    const unsigned char *spki, long long spkiLen,
                                    const unsigned char *data, long long len,
                                    const unsigned char *sig, long long sigLen) {
    SecKeyAlgorithm alg = kml_ecdsa_alg(hashId);
    ll cb = kml_curve_bytes(curveId);
    SecKeyRef key;
    unsigned char *der = NULL;
    size_t derLen = 0;
    CFDataRef inData, sigData = NULL;
    Boolean ok = 0;
    if (!alg || cb == 0) return -3;
    if (sigLen != 2 * cb) return 0;
    key = kml_seckey_from_der(spki, spkiLen, 0, 0, curveId);
    if (!key) return -2;
    if (kml_ecdsa_raw_to_der(sig, cb, &der, &derLen) != 0) {
        CFRelease(key);
        return -1;
    }
    inData = CFDataCreate(NULL, data, (CFIndex)len);
    sigData = CFDataCreate(NULL, der, (CFIndex)derLen);
    if (inData && sigData)
        ok = SecKeyVerifySignature(key, alg, inData, sigData, NULL);
    if (inData) CFRelease(inData);
    if (sigData) CFRelease(sigData);
    free(der);
    CFRelease(key);
    return ok ? 1 : 0;
}

long long __kml_crypto_ec_raw_to_spki(long long curveId,
                                      const unsigned char *raw, long long rawLen,
                                      unsigned char **spki, long long *spkiLen) {
    return kml_ec_wrap(curveId, raw, (size_t)rawLen, 0, spki, spkiLen);
}

long long __kml_crypto_ec_spki_to_raw(long long curveId,
                                      const unsigned char *spki, long long spkiLen,
                                      unsigned char **raw, long long *rawLen) {
    unsigned char *pt;
    size_t ptLen;
    ll rc = kml_ec_unwrap(curveId, spki, spkiLen, 0, &pt, &ptLen);
    if (rc != 0) return rc;
    *raw = pt;
    *rawLen = (ll)ptLen;
    return 0;
}

/* ── JWK component bridge (PKCS#1 / X9.63 parsing via the mini-DER) ──────── */

static char *kml_uint_b64u(const unsigned char *v, size_t n) {
    char *out = NULL;
    ll outLen;
    if (__kml_crypto_b64url_encode(v, (ll)n, &out, &outLen) != 0) return NULL;
    return out;
}

long long __kml_crypto_jwk_export_rsa(long long isPriv,
                                      const unsigned char *der, long long derLen,
                                      char **n, char **e, char **d, char **p,
                                      char **q, char **dp, char **dq, char **qi) {
    const unsigned char *body, *ip, *iend, *c;
    size_t bodyLen, clen;
    unsigned char tag;
    const unsigned char *vals[9];
    size_t lens[9];
    int i, count = 0;
    if (kml_rsa_unwrap(der, derLen, (int)isPriv, &body, &bodyLen) != 0)
        return -2;
    ip = body;
    iend = body + bodyLen;
    if (!kml_der_read(&ip, iend, &tag, &c, &clen) || tag != 0x30) return -2;
    ip = c;
    iend = c + clen;
    while (count < 9 && kml_der_read_uint(&ip, iend, &vals[count], &lens[count]))
        count++;
    *n = *e = *d = *p = *q = *dp = *dq = *qi = NULL;
    if (isPriv) {
        /* PKCS#1 RSAPrivateKey: ver, n, e, d, p, q, dp, dq, qi */
        if (count < 9) return -2;
        *n = kml_uint_b64u(vals[1], lens[1]);
        *e = kml_uint_b64u(vals[2], lens[2]);
        *d = kml_uint_b64u(vals[3], lens[3]);
        *p = kml_uint_b64u(vals[4], lens[4]);
        *q = kml_uint_b64u(vals[5], lens[5]);
        *dp = kml_uint_b64u(vals[6], lens[6]);
        *dq = kml_uint_b64u(vals[7], lens[7]);
        *qi = kml_uint_b64u(vals[8], lens[8]);
    } else {
        /* PKCS#1 RSAPublicKey: n, e */
        if (count < 2) return -2;
        *n = kml_uint_b64u(vals[0], lens[0]);
        *e = kml_uint_b64u(vals[1], lens[1]);
    }
    for (i = 0; i < (isPriv ? 8 : 2); i++) {
        /* all requested components must have encoded */
    }
    return (*n && *e) ? 0 : -1;
}

static unsigned char *kml_b64u_bytes(const char *s, size_t *n) {
    unsigned char *b;
    ll bl;
    if (!s) return NULL;
    if (__kml_crypto_b64url_decode(s, (ll)strlen(s), &b, &bl) != 0) return NULL;
    *n = (size_t)bl;
    return b;
}

long long __kml_crypto_jwk_import_rsa(const char *n, const char *e,
                                      const char *d, const char *p,
                                      const char *q, const char *dp,
                                      const char *dq, const char *qi,
                                      unsigned char **der, long long *derLen,
                                      long long *kindOut) {
    int isPriv = d != NULL;
    const char *strs[8];
    unsigned char *bytes[8] = {0};
    size_t lens[8] = {0};
    int count = isPriv ? 8 : 2, i, ok = 1;
    kml_der body = {0};
    ll rc = -2;
    strs[0] = n; strs[1] = e; strs[2] = d; strs[3] = p;
    strs[4] = q; strs[5] = dp; strs[6] = dq; strs[7] = qi;
    if (!n || !e) return -2;
    if (isPriv && (!p || !q || !dp || !dq || !qi)) return -2;
    for (i = 0; i < count; i++) {
        bytes[i] = kml_b64u_bytes(strs[i], &lens[i]);
        if (!bytes[i]) ok = 0;
    }
    if (ok) {
        size_t seqLen = 0;
        static const unsigned char zero = 0;
        if (isPriv) seqLen += 3; /* INT 0 */
        for (i = 0; i < count; i++) seqLen += kml_der_uint_size(bytes[i], lens[i]);
        ok = kml_der_hdr(&body, 0x30, seqLen);
        if (ok && isPriv) {
            ok = kml_der_hdr(&body, 0x02, 1) && kml_der_put(&body, &zero, 1);
        }
        for (i = 0; ok && i < count; i++)
            ok = kml_der_uint(&body, bytes[i], lens[i]);
        if (ok)
            rc = kml_rsa_wrap(body.buf, body.len, isPriv, der, derLen);
        if (rc == 0) *kindOut = isPriv ? 2 : 1;
    }
    free(body.buf);
    for (i = 0; i < count; i++) free(bytes[i]);
    return rc;
}

long long __kml_crypto_jwk_export_ec(long long curveId, long long isPriv,
                                     const unsigned char *der, long long derLen,
                                     char **x, char **y, char **d) {
    unsigned char *x963;
    size_t xlen;
    ll cb = kml_curve_bytes(curveId);
    ll rc;
    *x = *y = *d = NULL;
    if (cb == 0) return -3;
    rc = kml_ec_unwrap(curveId, der, derLen, (int)isPriv, &x963, &xlen);
    if (rc != 0) return rc;
    *x = kml_uint_b64u(x963 + 1, (size_t)cb);
    *y = kml_uint_b64u(x963 + 1 + cb, (size_t)cb);
    if (isPriv) *d = kml_uint_b64u(x963 + 1 + 2 * cb, (size_t)cb);
    free(x963);
    return (*x && *y && (!isPriv || *d)) ? 0 : -1;
}

long long __kml_crypto_jwk_import_ec(long long curveId, const char *x,
                                     const char *y, const char *d,
                                     unsigned char **der, long long *derLen,
                                     long long *kindOut) {
    ll cb = kml_curve_bytes(curveId);
    unsigned char *xb = NULL, *yb = NULL, *db = NULL, *x963 = NULL;
    size_t xl = 0, yl = 0, dl = 0;
    ll rc = -2;
    if (cb == 0) return -3;
    if (!x || !y) return -2;
    xb = kml_b64u_bytes(x, &xl);
    yb = kml_b64u_bytes(y, &yl);
    if (d) db = kml_b64u_bytes(d, &dl);
    if (xb && yb && xl == (size_t)cb && yl == (size_t)cb &&
        (!d || (db && dl == (size_t)cb))) {
        size_t total = (size_t)(1 + 2 * cb) + (d ? (size_t)cb : 0);
        x963 = (unsigned char *)malloc(total);
        if (x963) {
            x963[0] = 4;
            memcpy(x963 + 1, xb, (size_t)cb);
            memcpy(x963 + 1 + cb, yb, (size_t)cb);
            if (d) memcpy(x963 + 1 + 2 * cb, db, (size_t)cb);
            rc = kml_ec_wrap(curveId, x963, total, d != NULL, der, derLen);
            if (rc == 0) *kindOut = d ? 2 : 1;
        }
    }
    free(xb); free(yb); free(db); free(x963);
    return rc;
}

/* ── AES-GCM = public-API AES-CTR + an in-shim GHASH ────────────────────────
 * CommonCrypto's own GCM entry points are private SPI (TDD-00104), so GCM is
 * composed from public kCCModeCTR plus a GHASH over GF(2^128) implemented
 * here (table-free shift-based multiply — slow but constant-shape).
 * Validated against the NIST GCM vectors in the E2E suite. */

static void kml_ghash_mul(unsigned char x[16], const unsigned char h[16]) {
    unsigned char z[16] = {0}, v[16];
    int i, bit;
    memcpy(v, h, 16);
    for (i = 0; i < 128; i++) {
        int byteIdx = i / 8;
        bit = (x[byteIdx] >> (7 - i % 8)) & 1;
        if (bit) {
            int j;
            for (j = 0; j < 16; j++) z[j] ^= v[j];
        }
        /* v = v >> 1 (in GCM's reflected bit order), conditionally xor R */
        {
            int lsb = v[15] & 1, j;
            for (j = 15; j > 0; j--) v[j] = (unsigned char)(v[j] >> 1 | v[j - 1] << 7);
            v[0] >>= 1;
            if (lsb) v[0] ^= 0xe1;
        }
    }
    memcpy(x, z, 16);
}

static void kml_ghash(const unsigned char h[16], const unsigned char *aad,
                      long long aadLen, const unsigned char *ct,
                      long long ctLen, unsigned char out[16]) {
    unsigned char y[16] = {0}, block[16];
    long long i;
    for (i = 0; i < aadLen; i += 16) {
        long long n = aadLen - i < 16 ? aadLen - i : 16;
        int j;
        memset(block, 0, 16);
        memcpy(block, aad + i, (size_t)n);
        for (j = 0; j < 16; j++) y[j] ^= block[j];
        kml_ghash_mul(y, h);
    }
    for (i = 0; i < ctLen; i += 16) {
        long long n = ctLen - i < 16 ? ctLen - i : 16;
        int j;
        memset(block, 0, 16);
        memcpy(block, ct + i, (size_t)n);
        for (j = 0; j < 16; j++) y[j] ^= block[j];
        kml_ghash_mul(y, h);
    }
    {
        unsigned long long ab = (unsigned long long)aadLen * 8;
        unsigned long long cb = (unsigned long long)ctLen * 8;
        int j;
        for (j = 0; j < 8; j++) block[j] = (unsigned char)(ab >> (56 - 8 * j));
        for (j = 0; j < 8; j++) block[8 + j] = (unsigned char)(cb >> (56 - 8 * j));
        for (j = 0; j < 16; j++) y[j] ^= block[j];
        kml_ghash_mul(y, h);
    }
    memcpy(out, y, 16);
}

static long long kml_aes_ecb_block(const unsigned char *key, long long keyLen,
                                   const unsigned char in[16],
                                   unsigned char out[16]) {
    size_t moved = 0;
    CCCryptorStatus st = CCCrypt(kCCEncrypt, kCCAlgorithmAES,
                                 kCCOptionECBMode, key, (size_t)keyLen, NULL,
                                 in, 16, out, 16, &moved);
    return (st == kCCSuccess && moved == 16) ? 0 : -1;
}

static void kml_inc32(unsigned char ctr[16]) {
    int j;
    for (j = 15; j >= 12; j--) {
        if (++ctr[j] != 0) break;
    }
}

long long __kml_crypto_aes_gcm(long long encrypt, const unsigned char *key,
                               long long keyLen, const unsigned char *iv,
                               long long ivLen, const unsigned char *aad,
                               long long aadLen, long long tagBits,
                               const unsigned char *in, long long inLen,
                               unsigned char **out, long long *outLen) {
    unsigned char h[16] = {0}, zero[16] = {0}, j0[16], ctr[16], ektj0[16];
    unsigned char tag[16];
    unsigned char *buf = NULL;
    long long tagBytes = tagBits / 8;
    long long ctLen, rc = -1;
    const unsigned char *ct;
    CCCryptorRef cryptor = NULL;
    size_t moved = 0;
    int j;

    if (keyLen != 16 && keyLen != 24 && keyLen != 32) return -2;
    if (tagBytes < 4 || tagBytes > 16) return -1;
    if (!encrypt && inLen < tagBytes) return -1;
    if (kml_aes_ecb_block(key, keyLen, zero, h) != 0) return -1;

    if (ivLen == 12) {
        memcpy(j0, iv, 12);
        j0[12] = j0[13] = j0[14] = 0;
        j0[15] = 1;
    } else {
        kml_ghash(h, NULL, 0, iv, ivLen, j0);
    }

    ctLen = encrypt ? inLen : inLen - tagBytes;
    buf = (unsigned char *)malloc((size_t)(encrypt ? inLen + tagBytes : ctLen) + 1);
    if (!buf) return -1;

    memcpy(ctr, j0, 16);
    kml_inc32(ctr);
    if (CCCryptorCreateWithMode(kCCEncrypt, kCCModeCTR, kCCAlgorithmAES,
                                ccNoPadding, ctr, key, (size_t)keyLen, NULL, 0,
                                0, kCCModeOptionCTR_BE,
                                &cryptor) != kCCSuccess) {
        free(buf);
        return -1;
    }
    if (CCCryptorUpdate(cryptor, in, (size_t)ctLen, buf, (size_t)ctLen + 1,
                        &moved) != kCCSuccess ||
        (long long)moved != ctLen) {
        CCCryptorRelease(cryptor);
        free(buf);
        return -1;
    }
    CCCryptorRelease(cryptor);

    ct = encrypt ? buf : in;
    kml_ghash(h, aad, aadLen, ct, ctLen, tag);
    if (kml_aes_ecb_block(key, keyLen, j0, ektj0) != 0) {
        free(buf);
        return -1;
    }
    for (j = 0; j < 16; j++) tag[j] ^= ektj0[j];

    if (encrypt) {
        memcpy(buf + ctLen, tag, (size_t)tagBytes);
        *out = buf;
        *outLen = ctLen + tagBytes;
        rc = 0;
    } else {
        if (__kml_crypto_memeq(tag, in + ctLen, tagBytes) == 1) {
            *out = buf;
            *outLen = ctLen;
            rc = 0;
        } else {
            free(buf);
            rc = -1;
        }
    }
    return rc;
}
