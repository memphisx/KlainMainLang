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

/* Streaming HMAC — crypto.createHmac's Hmac object (ADR-00637). */
struct kml_cc_hmac_stream {
    CCHmacContext ctx;
    long long dlen;
};

/* base64url (RFC 4648 §5, no padding) — the JWK `k`/component codec. Kept
 * in the backend file (duplicated across backends) so each stays a single
 * self-contained TU. */
static const char kml_b64u[] =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";

/* ── key derivation: PBKDF2 (CCKeyDerivationPBKDF) / HKDF (RFC 5869 over
 * CCHmac — CommonCrypto's public API has no HKDF) ─────────────────────────── */

#include <CommonCrypto/CommonKeyDerivation.h>

/* MD5 is part of Node's surface; its CC entry points are marked deprecated. */
#pragma clang diagnostic ignored "-Wdeprecated-declarations"

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

/* ── JWK component bridge (PKCS#1 / X9.63 parsing via the mini-DER) ──────── */

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

static void kml_inc32(unsigned char ctr[16]) {
    int j;
    for (j = 15; j >= 12; j--) {
        if (++ctr[j] != 0) break;
    }
}

/* ---- node:crypto natives (lib/node/crypto.ts) ---------------------------------
 * Node's crypto module over CommonCrypto + Security.framework, with the same
 * ABI, return codes and error strings as the libcrypto backend
 * (crypto_openssl.c). Hashes/HMAC are CC_* contexts; AES CBC/ECB/CTR run
 * over CCCryptor (block buffering and PKCS#7 padding done here, so update()
 * output sizes match EVP's); GCM is CTR + the GHASH above, streamed; scrypt
 * (RFC 7914) and HKDF (RFC 5869) are implemented here; RSA/EC keys use
 * SecKey; Ed25519/X25519 use the curve arithmetic below. Error strings are
 * libcrypto's (what Node surfaces), kept per thread like its error queue. */

#include <stdint.h>
#include <stdio.h>
#include <CommonCrypto/CommonKeyDerivation.h>

extern void __kml_pool_job(void (*work)(void *, long long *, double *), void *job, void *inv, void *clo);
extern char *__kml_str_alloc(long long n);
extern void __kml_str_finalize(char *s);

static char *kncc_str(const char *s) {
    long long n = (long long)strlen(s);
    char *out = __kml_str_alloc(n + 1);
    memcpy(out, s, (size_t)n + 1);
    __kml_str_finalize(out);
    return out;
}

static __thread char kncc_err[160];

static void kncc_set_err(const char *s) {
    snprintf(kncc_err, sizeof kncc_err, "%s", s);
}

#define KNCC_E_BAD_DECRYPT "error:1C800064:Provider routines::bad decrypt"
#define KNCC_E_FINAL_LEN "error:1C80006B:Provider routines::wrong final block length"
#define KNCC_E_MEMLIMIT "error:030000AC:digital envelope routines::memory limit exceeded"
#define KNCC_E_DECODER "error:1E08010C:DECODER routines::unsupported"
#define KNCC_E_KEY_SMALL "error:1C8000AB:Provider routines::key size too small"
#define KNCC_E_INVALID_DIGEST "error:1C80007A:Provider routines::invalid digest"
#define KNCC_E_KEYTYPE "error:03000096:digital envelope routines::operation not supported for this keytype"
#define KNCC_E_INTERRUPTED "error:07880109:common libcrypto routines::interrupted or cancelled"
#define KNCC_E_UNKNOWN_OBJ "error:04000067:object identifier routines::unknown object name"

/* ---- digests by name ---- */
enum { KD_MD5 = 0, KD_SHA1, KD_SHA224, KD_SHA256, KD_SHA384, KD_SHA512, KD_COUNT };

static const struct { const char *name; int md; } kncc_md_names[] = {
    {"md5", KD_MD5}, {"rsa-md5", KD_MD5}, {"md5withrsaencryption", KD_MD5}, {"ssl3-md5", KD_MD5},
    {"sha1", KD_SHA1}, {"sha-1", KD_SHA1}, {"rsa-sha1", KD_SHA1}, {"sha1withrsaencryption", KD_SHA1},
    {"rsa-sha1-2", KD_SHA1}, {"ssl3-sha1", KD_SHA1}, {"ecdsa-with-sha1", KD_SHA1},
    {"sha224", KD_SHA224}, {"sha-224", KD_SHA224}, {"sha2-224", KD_SHA224}, {"rsa-sha224", KD_SHA224},
    {"sha224withrsaencryption", KD_SHA224},
    {"sha256", KD_SHA256}, {"sha-256", KD_SHA256}, {"sha2-256", KD_SHA256}, {"rsa-sha256", KD_SHA256},
    {"sha256withrsaencryption", KD_SHA256},
    {"sha384", KD_SHA384}, {"sha-384", KD_SHA384}, {"sha2-384", KD_SHA384}, {"rsa-sha384", KD_SHA384},
    {"sha384withrsaencryption", KD_SHA384},
    {"sha512", KD_SHA512}, {"sha-512", KD_SHA512}, {"sha2-512", KD_SHA512}, {"rsa-sha512", KD_SHA512},
    {"sha512withrsaencryption", KD_SHA512},
};

/* The names getHashes() reports, in libcrypto's sorted listing style. */
static const char *kncc_hash_list =
    "RSA-MD5,RSA-SHA1,RSA-SHA1-2,RSA-SHA224,RSA-SHA256,RSA-SHA384,RSA-SHA512,"
    "ecdsa-with-SHA1,md5,md5WithRSAEncryption,sha1,sha1WithRSAEncryption,"
    "sha224,sha224WithRSAEncryption,sha256,sha256WithRSAEncryption,"
    "sha384,sha384WithRSAEncryption,sha512,sha512WithRSAEncryption,ssl3-md5,ssl3-sha1";

static int kncc_md(const char *name) {
    if (!name || !*name) return -1;
    for (size_t i = 0; i < sizeof kncc_md_names / sizeof kncc_md_names[0]; i++)
        if (strcasecmp(name, kncc_md_names[i].name) == 0) return kncc_md_names[i].md;
    return -1;
}

static const size_t kncc_md_len[KD_COUNT] = {16, 20, 28, 32, 48, 64};
static const CCHmacAlgorithm kncc_md_hmac[KD_COUNT] = {kCCHmacAlgMD5, kCCHmacAlgSHA1, kCCHmacAlgSHA224,
                                                       kCCHmacAlgSHA256, kCCHmacAlgSHA384, kCCHmacAlgSHA512};

typedef struct {
    int md;
    union {
        CC_MD5_CTX md5;
        CC_SHA1_CTX s1;
        CC_SHA256_CTX s256; /* SHA-224 too */
        CC_SHA512_CTX s512; /* SHA-384 too */
    } u;
} kncc_md_ctx;

static void kncc_md_init(kncc_md_ctx *c, int md) {
    c->md = md;
    switch (md) {
    case KD_MD5: CC_MD5_Init(&c->u.md5); break;
    case KD_SHA1: CC_SHA1_Init(&c->u.s1); break;
    case KD_SHA224: CC_SHA224_Init(&c->u.s256); break;
    case KD_SHA256: CC_SHA256_Init(&c->u.s256); break;
    case KD_SHA384: CC_SHA384_Init(&c->u.s512); break;
    case KD_SHA512: CC_SHA512_Init(&c->u.s512); break;
    }
}

static void kncc_md_update(kncc_md_ctx *c, const void *data, long long len) {
    const unsigned char *p = (const unsigned char *)data;
    while (len > 0) {
        CC_LONG n = len > 0x40000000LL ? 0x40000000U : (CC_LONG)len;
        switch (c->md) {
        case KD_MD5: CC_MD5_Update(&c->u.md5, p, n); break;
        case KD_SHA1: CC_SHA1_Update(&c->u.s1, p, n); break;
        case KD_SHA224: CC_SHA224_Update(&c->u.s256, p, n); break;
        case KD_SHA256: CC_SHA256_Update(&c->u.s256, p, n); break;
        case KD_SHA384: CC_SHA384_Update(&c->u.s512, p, n); break;
        case KD_SHA512: CC_SHA512_Update(&c->u.s512, p, n); break;
        }
        p += n;
        len -= n;
    }
}

static void kncc_md_final(kncc_md_ctx *c, unsigned char *out) {
    switch (c->md) {
    case KD_MD5: CC_MD5_Final(out, &c->u.md5); break;
    case KD_SHA1: CC_SHA1_Final(out, &c->u.s1); break;
    case KD_SHA224: CC_SHA224_Final(out, &c->u.s256); break;
    case KD_SHA256: CC_SHA256_Final(out, &c->u.s256); break;
    case KD_SHA384: CC_SHA384_Final(out, &c->u.s512); break;
    case KD_SHA512: CC_SHA512_Final(out, &c->u.s512); break;
    }
}

static void kncc_md_oneshot(int md, const void *data, long long len, unsigned char *out) {
    kncc_md_ctx c;
    kncc_md_init(&c, md);
    kncc_md_update(&c, data, len);
    kncc_md_final(&c, out);
}

static void kncc_hmac_update(CCHmacContext *c, const void *data, long long len) {
    const unsigned char *p = (const unsigned char *)data;
    while (len > 0) {
        size_t n = len > 0x40000000LL ? 0x40000000U : (size_t)len;
        CCHmacUpdate(c, p, n);
        p += n;
        len -= (long long)n;
    }
}

/* ---- the handle table ---- */
enum { KNCC_FREE = 0, KNCC_HASH, KNCC_HMAC, KNCC_CIPHER };

enum { KC_CBC = 0, KC_ECB, KC_CTR, KC_GCM };

typedef struct {
    int mode, encrypt, padding, keylen;
    CCCryptorRef cc;          /* CBC/ECB/CTR chain, or GCM's ECB block cipher */
    unsigned char buf[16];    /* CBC/ECB pending bytes */
    int buflen;
    /* GCM */
    unsigned char h[16], j0[16], ctr[16], ks[16], x[16], gbuf[16], tag[16];
    int ksused, gbuflen, taglen, in_data, tag_set, finished, ctr_started;
    unsigned long long aadlen, ctlen;
} kncc_cipher;

typedef struct {
    int kind;
    int md;
    kncc_md_ctx hash;
    CCHmacContext hmac;
    kncc_cipher *cipher;
} kncc_handle;

static kncc_handle *kncc_tab = NULL;
static long long kncc_cap = 0;

static long long kncc_alloc(void) {
    for (long long i = 0; i < kncc_cap; i++)
        if (kncc_tab[i].kind == KNCC_FREE) return i;
    long long ncap = kncc_cap ? kncc_cap * 2 : 16;
    kncc_handle *t = (kncc_handle *)realloc(kncc_tab, (size_t)ncap * sizeof *t);
    if (!t) return -1;
    memset(t + kncc_cap, 0, (size_t)(ncap - kncc_cap) * sizeof *t);
    long long id = kncc_cap;
    kncc_tab = t;
    kncc_cap = ncap;
    return id;
}

static kncc_handle *kncc_get(double id) {
    long long i = (long long)id;
    if (i < 0 || i >= kncc_cap || kncc_tab[i].kind == KNCC_FREE) return NULL;
    return &kncc_tab[i];
}

static void kncc_release(kncc_handle *h) {
    if (h->cipher) {
        if (h->cipher->cc) CCCryptorRelease(h->cipher->cc);
        memset(h->cipher, 0, sizeof *h->cipher);
        free(h->cipher);
    }
    memset(h, 0, sizeof *h);
}

void __kml_native_crypto_free(double id) {
    kncc_handle *h = kncc_get(id);
    if (h) kncc_release(h);
}

/* createHash: the id, or -1 for a digest this backend does not provide
 * (outputLength only applies to an XOF, and there is none here). */
double __kml_native_crypto_hash_new(const char *alg, double xofLen) {
    (void)xofLen;
    int md = kncc_md(alg);
    if (md < 0) return -1;
    long long id = kncc_alloc();
    if (id < 0) return -1;
    kncc_tab[id].kind = KNCC_HASH;
    kncc_tab[id].md = md;
    kncc_md_init(&kncc_tab[id].hash, md);
    return (double)id;
}

double __kml_native_crypto_hmac_new(const char *alg, void *key, long long keyLen) {
    int md = kncc_md(alg);
    if (md < 0) return -1;
    long long id = kncc_alloc();
    if (id < 0) return -1;
    static const unsigned char empty = 0;
    kncc_tab[id].kind = KNCC_HMAC;
    kncc_tab[id].md = md;
    CCHmacInit(&kncc_tab[id].hmac, kncc_md_hmac[md], keyLen > 0 ? key : &empty, keyLen > 0 ? (size_t)keyLen : 0);
    return (double)id;
}

double __kml_native_crypto_hash_update(double id, void *data, long long len) {
    kncc_handle *h = kncc_get(id);
    if (!h) return 0;
    if (h->kind == KNCC_HASH) {
        kncc_md_update(&h->hash, data, len);
        return 1;
    }
    if (h->kind == KNCC_HMAC) {
        kncc_hmac_update(&h->hmac, data, len);
        return 1;
    }
    return 0;
}

double __kml_native_crypto_hash_size(double id) {
    kncc_handle *h = kncc_get(id);
    if (!h || (h->kind != KNCC_HASH && h->kind != KNCC_HMAC)) return 0;
    return (double)kncc_md_len[h->md];
}

double __kml_native_crypto_hash_digest(double id, void *out, long long outLen) {
    kncc_handle *h = kncc_get(id);
    if (!h) return -1;
    double r = -1;
    unsigned char buf[64];
    if (h->kind == KNCC_HASH || h->kind == KNCC_HMAC) {
        if (h->kind == KNCC_HASH) kncc_md_final(&h->hash, buf);
        else CCHmacFinal(&h->hmac, buf);
        size_t n = kncc_md_len[h->md];
        if ((long long)n <= outLen) {
            memcpy(out, buf, n);
            r = (double)n;
        }
    }
    kncc_release(h);
    return r;
}

double __kml_native_crypto_hash_copy(double id) {
    kncc_handle *h = kncc_get(id);
    if (!h || h->kind != KNCC_HASH) return -1;
    long long nid = kncc_alloc();
    if (nid < 0) return -1;
    h = kncc_get(id); /* the table may have moved */
    kncc_tab[nid] = *h;
    return (double)nid;
}

static const char *kncc_cipher_list =
    "aes-128-cbc,aes-128-ctr,aes-128-ecb,aes-128-gcm,aes-192-cbc,aes-192-ctr,aes-192-ecb,aes-192-gcm,"
    "aes-256-cbc,aes-256-ctr,aes-256-ecb,aes-256-gcm,aes128,aes192,aes256,"
    "id-aes128-GCM,id-aes192-GCM,id-aes256-GCM";

/* getHashes()/getCiphers(): the names joined by ','; 2 is the OpenSSL
 * version number, and there is no OpenSSL here. */
char *__kml_native_crypto_names(double which) {
    if (which == 2) return kncc_str("0");
    if (which == 3) return kncc_str("prime256v1,secp384r1,secp521r1"); /* SecKey's curves */
    return kncc_str(which == 0 ? kncc_hash_list : kncc_cipher_list);
}

/* ---- random ---- */

double __kml_native_crypto_random_fill(void *data, long long size, double offset, double length) {
    long long off = (long long)offset, len = (long long)length;
    if (off < 0 || len < 0 || off + len > size) return -1;
    if (len == 0) return 0;
    return SecRandomCopyBytes(kSecRandomDefault, (size_t)len, (unsigned char *)data + off) == errSecSuccess ? 0 : -1;
}

typedef struct { unsigned char *p; long long len; } kncc_rand_job;

static void kncc_rand_work(void *job, long long *err, double *res) {
    kncc_rand_job *j = (kncc_rand_job *)job;
    *err = (j->len == 0 || SecRandomCopyBytes(kSecRandomDefault, (size_t)j->len, j->p) == errSecSuccess) ? 0 : 1;
    *res = 0;
    free(j);
}

void __kml_native_crypto_random_fill_async(void *data, long long size, double offset, double length, void *inv, void *clo) {
    long long off = (long long)offset, len = (long long)length;
    kncc_rand_job *j = (kncc_rand_job *)malloc(sizeof *j);
    if (off < 0 || len < 0 || off + len > size) len = 0, off = 0;
    j->p = (unsigned char *)data + off;
    j->len = len;
    __kml_pool_job(kncc_rand_work, j, inv, clo);
}

/* ---- key derivation: pbkdf2, scrypt, hkdf ---- */

static void kncc_pbkdf2(int md, const unsigned char *pw, size_t pwlen, const unsigned char *salt, size_t saltlen,
                        unsigned long long iter, unsigned char *out, size_t outlen) {
    CCHmacContext base, c;
    unsigned char u[64], t[64], be[4];
    size_t hl = kncc_md_len[md];
    CCHmacInit(&base, kncc_md_hmac[md], pw, pwlen);
    for (uint32_t blk = 1; outlen > 0; blk++) {
        be[0] = (unsigned char)(blk >> 24); be[1] = (unsigned char)(blk >> 16);
        be[2] = (unsigned char)(blk >> 8); be[3] = (unsigned char)blk;
        c = base;
        CCHmacUpdate(&c, salt, saltlen);
        CCHmacUpdate(&c, be, 4);
        CCHmacFinal(&c, u);
        memcpy(t, u, hl);
        for (unsigned long long i = 1; i < iter; i++) {
            c = base;
            CCHmacUpdate(&c, u, hl);
            CCHmacFinal(&c, u);
            for (size_t k = 0; k < hl; k++) t[k] ^= u[k];
        }
        size_t n = outlen < hl ? outlen : hl;
        memcpy(out, t, n);
        out += n;
        outlen -= n;
    }
}

static long long kncc_pbkdf2_run(int md, const unsigned char *pw, size_t pwlen, const unsigned char *salt, size_t saltlen,
                                 double iter, unsigned char *out, size_t outlen) {
    static const CCPseudoRandomAlgorithm prf[KD_COUNT] = {0, kCCPRFHmacAlgSHA1, kCCPRFHmacAlgSHA224,
                                                         kCCPRFHmacAlgSHA256, kCCPRFHmacAlgSHA384, kCCPRFHmacAlgSHA512};
    if (iter < 1) return 1;
    if (outlen == 0) return 0;
    if (md != KD_MD5 && iter <= 4294967295.0) {
        return CCKeyDerivationPBKDF(kCCPBKDF2, (const char *)pw, pwlen, salt, saltlen, prf[md], (unsigned)iter, out,
                                    outlen) == kCCSuccess ? 0 : 1;
    }
    kncc_pbkdf2(md, pw, pwlen, salt, saltlen, (unsigned long long)iter, out, outlen);
    return 0;
}

/* RFC 5869 over CCHmac; 1 (no error text) when the length exceeds 255 * HashLen. */
static long long kncc_hkdf(int md, const unsigned char *ikm, size_t ikmlen, const unsigned char *salt, size_t saltlen,
                           const unsigned char *info, size_t infolen, unsigned char *out, size_t outlen) {
    size_t hl = kncc_md_len[md];
    unsigned char prk[64], t[64];
    static const unsigned char zeros[64] = {0};
    CCHmacContext c;
    if (outlen > 255 * hl) return 1;
    CCHmac(kncc_md_hmac[md], saltlen ? salt : zeros, saltlen ? saltlen : hl, ikm, ikmlen, prk);
    size_t tl = 0, done = 0;
    unsigned char ctr = 1;
    while (done < outlen) {
        CCHmacInit(&c, kncc_md_hmac[md], prk, hl);
        if (tl) CCHmacUpdate(&c, t, tl);
        if (infolen) CCHmacUpdate(&c, info, infolen);
        CCHmacUpdate(&c, &ctr, 1);
        CCHmacFinal(&c, t);
        tl = hl;
        size_t n = outlen - done < hl ? outlen - done : hl;
        memcpy(out + done, t, n);
        done += n;
        ctr++;
    }
    return 0;
}

/* scrypt (RFC 7914): Salsa20/8, BlockMix, ROMix over PBKDF2-HMAC-SHA256. */
#define KNCC_R(a, b) (((a) << (b)) | ((a) >> (32 - (b))))

static void kncc_salsa8(uint32_t B[16]) {
    uint32_t x[16];
    memcpy(x, B, sizeof x);
    for (int i = 0; i < 8; i += 2) {
        x[4] ^= KNCC_R(x[0] + x[12], 7);  x[8] ^= KNCC_R(x[4] + x[0], 9);
        x[12] ^= KNCC_R(x[8] + x[4], 13); x[0] ^= KNCC_R(x[12] + x[8], 18);
        x[9] ^= KNCC_R(x[5] + x[1], 7);   x[13] ^= KNCC_R(x[9] + x[5], 9);
        x[1] ^= KNCC_R(x[13] + x[9], 13); x[5] ^= KNCC_R(x[1] + x[13], 18);
        x[14] ^= KNCC_R(x[10] + x[6], 7); x[2] ^= KNCC_R(x[14] + x[10], 9);
        x[6] ^= KNCC_R(x[2] + x[14], 13); x[10] ^= KNCC_R(x[6] + x[2], 18);
        x[3] ^= KNCC_R(x[15] + x[11], 7); x[7] ^= KNCC_R(x[3] + x[15], 9);
        x[11] ^= KNCC_R(x[7] + x[3], 13); x[15] ^= KNCC_R(x[11] + x[7], 18);
        x[1] ^= KNCC_R(x[0] + x[3], 7);   x[2] ^= KNCC_R(x[1] + x[0], 9);
        x[3] ^= KNCC_R(x[2] + x[1], 13);  x[0] ^= KNCC_R(x[3] + x[2], 18);
        x[6] ^= KNCC_R(x[5] + x[4], 7);   x[7] ^= KNCC_R(x[6] + x[5], 9);
        x[4] ^= KNCC_R(x[7] + x[6], 13);  x[5] ^= KNCC_R(x[4] + x[7], 18);
        x[11] ^= KNCC_R(x[10] + x[9], 7); x[8] ^= KNCC_R(x[11] + x[10], 9);
        x[9] ^= KNCC_R(x[8] + x[11], 13); x[10] ^= KNCC_R(x[9] + x[8], 18);
        x[12] ^= KNCC_R(x[15] + x[14], 7); x[13] ^= KNCC_R(x[12] + x[15], 9);
        x[14] ^= KNCC_R(x[13] + x[12], 13); x[15] ^= KNCC_R(x[14] + x[13], 18);
    }
    for (int i = 0; i < 16; i++) B[i] += x[i];
}

/* B (2r 64-byte blocks, as words) → Y, then B = Y reordered (evens, odds). */
static void kncc_blockmix(uint32_t *B, uint32_t *Y, uint64_t r) {
    uint32_t X[16];
    memcpy(X, &B[(2 * r - 1) * 16], 64);
    for (uint64_t i = 0; i < 2 * r; i++) {
        for (int k = 0; k < 16; k++) X[k] ^= B[i * 16 + k];
        kncc_salsa8(X);
        memcpy(&Y[i * 16], X, 64);
    }
    for (uint64_t i = 0; i < r; i++) memcpy(&B[i * 16], &Y[(2 * i) * 16], 64);
    for (uint64_t i = 0; i < r; i++) memcpy(&B[(r + i) * 16], &Y[(2 * i + 1) * 16], 64);
}

static void kncc_romix(unsigned char *b, uint64_t r, uint64_t N, uint32_t *V, uint32_t *X, uint32_t *Y) {
    uint64_t words = 32 * r;
    for (uint64_t k = 0; k < words; k++)
        X[k] = (uint32_t)b[4 * k] | (uint32_t)b[4 * k + 1] << 8 | (uint32_t)b[4 * k + 2] << 16 | (uint32_t)b[4 * k + 3] << 24;
    for (uint64_t i = 0; i < N; i++) {
        memcpy(&V[i * words], X, words * 4);
        kncc_blockmix(X, Y, r);
    }
    for (uint64_t i = 0; i < N; i++) {
        uint64_t j = X[(2 * r - 1) * 16] & (N - 1);
        for (uint64_t k = 0; k < words; k++) X[k] ^= V[j * words + k];
        kncc_blockmix(X, Y, r);
    }
    for (uint64_t k = 0; k < words; k++) {
        b[4 * k] = (unsigned char)X[k]; b[4 * k + 1] = (unsigned char)(X[k] >> 8);
        b[4 * k + 2] = (unsigned char)(X[k] >> 16); b[4 * k + 3] = (unsigned char)(X[k] >> 24);
    }
}

/* libcrypto's EVP_PBE_scrypt parameter and memory checks, then the KDF. */
static long long kncc_scrypt(const unsigned char *pw, size_t pwlen, const unsigned char *salt, size_t saltlen,
                             double dN, double dr, double dp, double dmaxmem, unsigned char *out, size_t outlen) {
    uint64_t N = (uint64_t)dN, r = (uint64_t)dr, p = (uint64_t)dp, maxmem = (uint64_t)dmaxmem;
    const uint64_t PR_MAX = (1ULL << 30) - 1;
    if (r == 0 || p == 0 || N < 2 || (N & (N - 1))) return 1;
    if (p > PR_MAX / r) { kncc_set_err(KNCC_E_MEMLIMIT); return 1; }
    if (16 * r <= 63 && N >= (1ULL << (16 * r))) { kncc_set_err(KNCC_E_MEMLIMIT); return 1; }
    uint64_t Blen = p * 128 * r;
    if (Blen > 2147483647ULL) { kncc_set_err(KNCC_E_MEMLIMIT); return 1; }
    uint64_t lim = UINT64_MAX / (32 * sizeof(uint32_t));
    if (N + 2 > lim / r) { kncc_set_err(KNCC_E_MEMLIMIT); return 1; }
    uint64_t Vlen = 32 * r * (N + 2) * sizeof(uint32_t);
    if (Blen > UINT64_MAX - Vlen) { kncc_set_err(KNCC_E_MEMLIMIT); return 1; }
    if (maxmem == 0) maxmem = 1024 * 1024 * 32;
    if (Blen + Vlen > maxmem) { kncc_set_err(KNCC_E_MEMLIMIT); return 1; }
    if (outlen == 0) return 0;
    unsigned char *B = (unsigned char *)malloc((size_t)Blen);
    uint32_t *V = (uint32_t *)malloc((size_t)(32 * r * N * 4));
    uint32_t *XY = (uint32_t *)malloc((size_t)(64 * r * 4));
    long long rc = 1;
    if (B && V && XY &&
        CCKeyDerivationPBKDF(kCCPBKDF2, (const char *)pw, pwlen, salt, saltlen, kCCPRFHmacAlgSHA256, 1, B, (size_t)Blen) ==
            kCCSuccess) {
        for (uint64_t i = 0; i < p; i++) kncc_romix(B + 128 * r * i, r, N, V, XY, XY + 32 * r);
        if (CCKeyDerivationPBKDF(kCCPBKDF2, (const char *)pw, pwlen, B, (size_t)Blen, kCCPRFHmacAlgSHA256, 1, out,
                                 outlen) == kCCSuccess)
            rc = 0;
    }
    if (B) memset(B, 0, (size_t)Blen);
    free(B); free(V); free(XY);
    return rc;
}

enum { KNCC_PBKDF2 = 0, KNCC_SCRYPT, KNCC_HKDF };
typedef struct {
    int op;
    unsigned char *a, *b, *c;
    long long alen, blen, clen;
    double n1, n2, n3, n4;
    char digest[64];
    unsigned char *out;
    long long outlen;
} kncc_kdf;

static long long kncc_kdf_run(kncc_kdf *k) {
    switch (k->op) {
    case KNCC_PBKDF2: {
        int md = kncc_md(k->digest);
        if (md < 0) return 2;
        return kncc_pbkdf2_run(md, k->a, (size_t)k->alen, k->b, (size_t)k->blen, k->n1, k->out, (size_t)k->outlen);
    }
    case KNCC_SCRYPT:
        return kncc_scrypt(k->a, (size_t)k->alen, k->b, (size_t)k->blen, k->n1, k->n2, k->n3, k->n4, k->out,
                           (size_t)k->outlen);
    case KNCC_HKDF: {
        int md = kncc_md(k->digest);
        if (md < 0) return 2;
        return kncc_hkdf(md, k->a, (size_t)k->alen, k->b, (size_t)k->blen, k->c, (size_t)k->clen, k->out,
                         (size_t)k->outlen);
    }
    }
    return 1;
}

static unsigned char *kncc_dup(const void *p, long long n) {
    unsigned char *d = (unsigned char *)malloc((size_t)(n > 0 ? n : 1));
    if (n > 0) memcpy(d, p, (size_t)n);
    return d;
}

static kncc_kdf *kncc_kdf_new(double op, void *a, long long alen, void *b, long long blen, void *c, long long clen,
                              double n1, double n2, double n3, double n4, const char *digest, void *out, long long outlen) {
    kncc_kdf *k = (kncc_kdf *)calloc(1, sizeof *k);
    k->op = (int)op;
    k->a = kncc_dup(a, alen); k->alen = alen;
    k->b = kncc_dup(b, blen); k->blen = blen;
    k->c = kncc_dup(c, clen); k->clen = clen;
    k->n1 = n1; k->n2 = n2; k->n3 = n3; k->n4 = n4;
    snprintf(k->digest, sizeof k->digest, "%s", digest ? digest : "");
    k->out = (unsigned char *)out;
    k->outlen = outlen;
    return k;
}

static void kncc_kdf_free(kncc_kdf *k) {
    free(k->a); free(k->b); free(k->c); free(k);
}

/* 0 ok, 1 failed, 2 an unknown digest. */
double __kml_native_crypto_kdf(double op, void *a, long long alen, void *b, long long blen, void *c, long long clen,
                               double n1, double n2, double n3, double n4, const char *digest, void *out, long long outlen) {
    kncc_kdf *k = kncc_kdf_new(op, a, alen, b, blen, c, clen, n1, n2, n3, n4, digest, out, outlen);
    long long rc = kncc_kdf_run(k);
    kncc_kdf_free(k);
    return (double)rc;
}

static void kncc_kdf_work(void *job, long long *err, double *res) {
    kncc_kdf *k = (kncc_kdf *)job;
    *err = kncc_kdf_run(k);
    *res = 0;
    kncc_kdf_free(k);
}

void __kml_native_crypto_kdf_async(double op, void *a, long long alen, void *b, long long blen, void *c, long long clen,
                                   double n1, double n2, double n3, double n4, const char *digest, void *out, long long outlen,
                                   void *inv, void *clo) {
    kncc_kdf *k = kncc_kdf_new(op, a, alen, b, blen, c, clen, n1, n2, n3, n4, digest, out, outlen);
    __kml_pool_job(kncc_kdf_work, k, inv, clo);
}

/* ---- ciphers: AES CBC/ECB/CTR over CCCryptor, GCM = CTR + GHASH ---- */

static int kncc_cipher_lookup(const char *alg, int *mode, int *keylen) {
    static const struct { const char *name; int mode, keylen; } tab[] = {
        {"aes-128-cbc", KC_CBC, 16}, {"aes-192-cbc", KC_CBC, 24}, {"aes-256-cbc", KC_CBC, 32},
        {"aes128", KC_CBC, 16}, {"aes192", KC_CBC, 24}, {"aes256", KC_CBC, 32},
        {"aes-128-ecb", KC_ECB, 16}, {"aes-192-ecb", KC_ECB, 24}, {"aes-256-ecb", KC_ECB, 32},
        {"aes-128-ctr", KC_CTR, 16}, {"aes-192-ctr", KC_CTR, 24}, {"aes-256-ctr", KC_CTR, 32},
        {"aes-128-gcm", KC_GCM, 16}, {"aes-192-gcm", KC_GCM, 24}, {"aes-256-gcm", KC_GCM, 32},
        {"id-aes128-gcm", KC_GCM, 16}, {"id-aes192-gcm", KC_GCM, 24}, {"id-aes256-gcm", KC_GCM, 32},
    };
    if (!alg) return 0;
    for (size_t i = 0; i < sizeof tab / sizeof tab[0]; i++)
        if (strcasecmp(alg, tab[i].name) == 0) {
            *mode = tab[i].mode;
            *keylen = tab[i].keylen;
            return 1;
        }
    return 0;
}

static void kncc_gcm_absorb(kncc_cipher *c, const unsigned char *p, size_t n) {
    while (n > 0) {
        size_t take = 16 - (size_t)c->gbuflen;
        if (take > n) take = n;
        memcpy(c->gbuf + c->gbuflen, p, take);
        c->gbuflen += (int)take;
        p += take;
        n -= take;
        if (c->gbuflen == 16) {
            for (int j = 0; j < 16; j++) c->x[j] ^= c->gbuf[j];
            kml_ghash_mul(c->x, c->h);
            c->gbuflen = 0;
        }
    }
}

static void kncc_gcm_flush(kncc_cipher *c) {
    if (c->gbuflen) {
        memset(c->gbuf + c->gbuflen, 0, (size_t)(16 - c->gbuflen));
        for (int j = 0; j < 16; j++) c->x[j] ^= c->gbuf[j];
        kml_ghash_mul(c->x, c->h);
        c->gbuflen = 0;
    }
}

static int kncc_ecb(kncc_cipher *c, const unsigned char *in, unsigned char *out, size_t n) {
    size_t moved = 0;
    return CCCryptorUpdate(c->cc, in, n, out, n, &moved) == kCCSuccess && moved == n;
}

/* CTR keystream (inc32) xor, n bytes. */
static int kncc_gcm_ctr(kncc_cipher *c, const unsigned char *in, unsigned char *out, size_t n) {
    for (size_t i = 0; i < n; i++) {
        if (c->ksused == 16) {
            kml_inc32(c->ctr);
            if (!kncc_ecb(c, c->ctr, c->ks, 16)) return 0;
            c->ksused = 0;
        }
        out[i] = in[i] ^ c->ks[c->ksused++];
    }
    return 1;
}

/* AES-CTR keystream xor with a 128-bit big-endian counter, n bytes. */
static int kncc_ctr128(kncc_cipher *c, const unsigned char *in, unsigned char *out, size_t n) {
    for (size_t i = 0; i < n; i++) {
        if (c->ksused == 16) {
            if (c->ctr_started)
                for (int j = 15; j >= 0 && ++c->ctr[j] == 0; j--) {}
            c->ctr_started = 1;
            if (!kncc_ecb(c, c->ctr, c->ks, 16)) return 0;
            c->ksused = 0;
        }
        out[i] = in[i] ^ c->ks[c->ksused++];
    }
    return 1;
}

double __kml_native_crypto_cipher_new(const char *alg, void *key, long long keyLen, void *iv, long long ivLen,
                                      double encrypt, double authTagLen) {
    (void)authTagLen;
    int mode, kl;
    if (!kncc_cipher_lookup(alg, &mode, &kl)) return -1;
    if (keyLen != kl) return -2;
    if (mode == KC_ECB && ivLen != 0) return -3;
    if ((mode == KC_CBC || mode == KC_CTR) && ivLen != 16) return -3;
    if (mode == KC_GCM && ivLen <= 0) return -3;
    kncc_cipher *c = (kncc_cipher *)calloc(1, sizeof *c);
    if (!c) return -1;
    c->mode = mode;
    c->encrypt = encrypt != 0;
    c->padding = 1;
    c->keylen = kl;
    CCCryptorStatus st;
    if (mode == KC_CBC) {
        st = CCCryptorCreateWithMode(c->encrypt ? kCCEncrypt : kCCDecrypt, kCCModeCBC, kCCAlgorithmAES, ccNoPadding, iv,
                                     key, (size_t)kl, NULL, 0, 0, 0, &c->cc);
    } else if (mode == KC_ECB) {
        st = CCCryptorCreateWithMode(c->encrypt ? kCCEncrypt : kCCDecrypt, kCCModeECB, kCCAlgorithmAES, ccNoPadding, NULL,
                                     key, (size_t)kl, NULL, 0, 0, 0, &c->cc);
    } else if (mode == KC_CTR) {
        /* The counter is a full 128-bit big-endian one, as in libcrypto;
         * CCCryptor's CTR mode wraps differently, so it is driven here. */
        st = CCCryptorCreateWithMode(kCCEncrypt, kCCModeECB, kCCAlgorithmAES, ccNoPadding, NULL, key, (size_t)kl, NULL, 0,
                                     0, 0, &c->cc);
        memcpy(c->ctr, iv, 16);
        c->ksused = 16;
        c->ctr_started = 0;
    } else {
        st = CCCryptorCreateWithMode(kCCEncrypt, kCCModeECB, kCCAlgorithmAES, ccNoPadding, NULL, key, (size_t)kl, NULL, 0,
                                     0, 0, &c->cc);
        if (st == kCCSuccess) {
            unsigned char zero[16] = {0};
            if (!kncc_ecb(c, zero, c->h, 16)) st = kCCUnspecifiedError;
        }
        if (st == kCCSuccess) {
            if (ivLen == 12) {
                memcpy(c->j0, iv, 12);
                c->j0[15] = 1;
            } else {
                unsigned char lenblk[16] = {0};
                unsigned long long bits = (unsigned long long)ivLen * 8;
                kncc_gcm_absorb(c, (const unsigned char *)iv, (size_t)ivLen);
                kncc_gcm_flush(c);
                for (int j = 0; j < 8; j++) lenblk[8 + j] = (unsigned char)(bits >> (56 - 8 * j));
                kncc_gcm_absorb(c, lenblk, 16);
                memcpy(c->j0, c->x, 16);
                memset(c->x, 0, 16);
            }
            memcpy(c->ctr, c->j0, 16);
            c->ksused = 16;
        }
    }
    if (st != kCCSuccess) {
        if (c->cc) CCCryptorRelease(c->cc);
        free(c);
        return -1;
    }
    long long id = kncc_alloc();
    if (id < 0) {
        CCCryptorRelease(c->cc);
        free(c);
        return -1;
    }
    kncc_tab[id].kind = KNCC_CIPHER;
    kncc_tab[id].cipher = c;
    return (double)id;
}

static kncc_cipher *kncc_cipher_get(double id) {
    kncc_handle *h = kncc_get(id);
    return h && h->kind == KNCC_CIPHER ? h->cipher : NULL;
}

static int kncc_block_size(kncc_cipher *c) {
    return c->mode == KC_CBC || c->mode == KC_ECB ? 16 : 1;
}

double __kml_native_crypto_cipher_block_size(double id) {
    kncc_cipher *c = kncc_cipher_get(id);
    return c ? kncc_block_size(c) : 0;
}

double __kml_native_crypto_cipher_update(double id, void *in, long long inLen, void *out, long long outLen) {
    kncc_cipher *c = kncc_cipher_get(id);
    if (!c) return -1;
    if (inLen + kncc_block_size(c) > outLen) return -1;
    const unsigned char *ip = (const unsigned char *)in;
    unsigned char *op = (unsigned char *)out;
    if (c->mode == KC_CTR) {
        return kncc_ctr128(c, ip, op, (size_t)inLen) ? (double)inLen : -1;
    }
    if (c->mode == KC_GCM) {
        if (c->finished) return -1;
        if (!c->in_data) {
            kncc_gcm_flush(c);
            c->in_data = 1;
        }
        if (c->encrypt) {
            if (!kncc_gcm_ctr(c, ip, op, (size_t)inLen)) return -1;
            kncc_gcm_absorb(c, op, (size_t)inLen);
        } else {
            kncc_gcm_absorb(c, ip, (size_t)inLen);
            if (!kncc_gcm_ctr(c, ip, op, (size_t)inLen)) return -1;
        }
        c->ctlen += (unsigned long long)inLen;
        return (double)inLen;
    }
    /* CBC/ECB: whole blocks out; a padded decrypt holds back the last one. */
    long long total = c->buflen + inLen, written = 0;
    long long full = total / 16 * 16;
    if (!c->encrypt && c->padding && full == total && full > 0) full -= 16;
    long long need = full;
    long long ipos = 0;
    if (need > 0 && c->buflen > 0) {
        long long take = 16 - c->buflen;
        memcpy(c->buf + c->buflen, ip, (size_t)take);
        if (!kncc_ecb(c, c->buf, op, 16)) return -1;
        ipos = take;
        written = 16;
        c->buflen = 0;
        need -= 16;
    }
    if (need > 0) {
        if (!kncc_ecb(c, ip + ipos, op + written, (size_t)need)) return -1;
        ipos += need;
        written += need;
    }
    memcpy(c->buf + c->buflen, ip + ipos, (size_t)(inLen - ipos));
    c->buflen += (int)(inLen - ipos);
    return (double)written;
}

double __kml_native_crypto_cipher_final(double id, void *out, long long outLen) {
    kncc_cipher *c = kncc_cipher_get(id);
    if (!c) return -1;
    if (outLen < kncc_block_size(c)) return -1;
    unsigned char *op = (unsigned char *)out;
    if (c->mode == KC_CTR) return 0;
    if (c->mode == KC_GCM) {
        unsigned char lenblk[16], ekj0[16];
        unsigned long long ab = c->aadlen * 8, cb = c->ctlen * 8;
        if (c->finished) return -1;
        if (!c->encrypt && !c->tag_set) return -1;
        kncc_gcm_flush(c);
        for (int j = 0; j < 8; j++) lenblk[j] = (unsigned char)(ab >> (56 - 8 * j));
        for (int j = 0; j < 8; j++) lenblk[8 + j] = (unsigned char)(cb >> (56 - 8 * j));
        kncc_gcm_absorb(c, lenblk, 16);
        if (!kncc_ecb(c, c->j0, ekj0, 16)) return -1;
        unsigned char tag[16];
        for (int j = 0; j < 16; j++) tag[j] = c->x[j] ^ ekj0[j];
        c->finished = 1;
        if (c->encrypt) {
            memcpy(c->tag, tag, 16);
            return 0;
        }
        unsigned char d = 0;
        for (int j = 0; j < c->taglen; j++) d |= (unsigned char)(tag[j] ^ c->tag[j]);
        return d == 0 ? 0 : -1;
    }
    if (c->encrypt) {
        if (!c->padding) {
            if (c->buflen) {
                kncc_set_err(KNCC_E_FINAL_LEN);
                return -1;
            }
            return 0;
        }
        unsigned char pad = (unsigned char)(16 - c->buflen);
        memset(c->buf + c->buflen, pad, pad);
        c->buflen = 0;
        return kncc_ecb(c, c->buf, op, 16) ? 16 : -1;
    }
    if (!c->padding) {
        if (c->buflen) {
            kncc_set_err(KNCC_E_FINAL_LEN);
            return -1;
        }
        return 0;
    }
    if (c->buflen != 16) {
        kncc_set_err(KNCC_E_FINAL_LEN);
        return -1;
    }
    unsigned char blk[16];
    c->buflen = 0;
    if (!kncc_ecb(c, c->buf, blk, 16)) return -1;
    unsigned char pad = blk[15];
    if (pad == 0 || pad > 16) {
        kncc_set_err(KNCC_E_BAD_DECRYPT);
        return -1;
    }
    for (int j = 16 - pad; j < 16; j++)
        if (blk[j] != pad) {
            kncc_set_err(KNCC_E_BAD_DECRYPT);
            return -1;
        }
    memcpy(op, blk, (size_t)(16 - pad));
    return 16 - pad;
}

double __kml_native_crypto_cipher_set_padding(double id, double on) {
    kncc_cipher *c = kncc_cipher_get(id);
    if (!c) return 0;
    c->padding = on != 0;
    return 1;
}

double __kml_native_crypto_cipher_set_aad(double id, void *aad, long long len) {
    kncc_cipher *c = kncc_cipher_get(id);
    if (!c || c->mode != KC_GCM || c->in_data || c->finished) return 0;
    kncc_gcm_absorb(c, (const unsigned char *)aad, (size_t)len);
    c->aadlen += (unsigned long long)len;
    return 1;
}

double __kml_native_crypto_cipher_get_tag(double id, void *out, long long len) {
    kncc_cipher *c = kncc_cipher_get(id);
    if (!c || c->mode != KC_GCM || !c->encrypt || !c->finished || len < 1 || len > 16) return -1;
    memcpy(out, c->tag, (size_t)len);
    return (double)len;
}

double __kml_native_crypto_cipher_set_tag(double id, void *tag, long long len) {
    kncc_cipher *c = kncc_cipher_get(id);
    if (!c || c->mode != KC_GCM || c->encrypt || c->finished || len < 1 || len > 16) return 0;
    memcpy(c->tag, tag, (size_t)len);
    c->taglen = (int)len;
    c->tag_set = 1;
    return 1;
}

/* ---- Curve25519 / Ed25519 (compact field arithmetic, radix 2^16) ---- */

typedef int64_t kncc_gf[16];
static const kncc_gf kncc_gf0 = {0}, kncc_gf1 = {1}, kncc_121665 = {0xDB41, 1};
static const kncc_gf kncc_D = {0x78a3, 0x1359, 0x4dca, 0x75eb, 0xd8ab, 0x4141, 0x0a4d, 0x0070,
                               0xe898, 0x7779, 0x4079, 0x8cc7, 0xfe73, 0x2b6f, 0x6cee, 0x5203};
static const kncc_gf kncc_D2 = {0xf159, 0x26b2, 0x9b94, 0xebd6, 0xb156, 0x8283, 0x149a, 0x00e0,
                                0xd130, 0xeef3, 0x80f2, 0x198e, 0xfce7, 0x56df, 0xd9dc, 0x2406};
static const kncc_gf kncc_X = {0xd51a, 0x8f25, 0x2d60, 0xc956, 0xa7b2, 0x9525, 0xc760, 0x692c,
                               0xdc5c, 0xfdd6, 0xe231, 0xc0a4, 0x53fe, 0xcd6e, 0x36d3, 0x2169};
static const kncc_gf kncc_Y = {0x6658, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666,
                               0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666};
static const kncc_gf kncc_I = {0xa0b0, 0x4a0e, 0x1b27, 0xc4ee, 0xe478, 0xad2f, 0x1806, 0x2f43,
                               0xd7a7, 0x3dfb, 0x0099, 0x2b4d, 0xdf0b, 0x4fc1, 0x2480, 0x2b83};

static void kncc_set(kncc_gf r, const kncc_gf a) { for (int i = 0; i < 16; i++) r[i] = a[i]; }

static void kncc_car(kncc_gf o) {
    for (int i = 0; i < 16; i++) {
        o[i] += (1LL << 16);
        int64_t c = o[i] >> 16;
        o[(i + 1) * (i < 15)] += c - 1 + 37 * (c - 1) * (i == 15);
        o[i] -= c * 65536;
    }
}

static void kncc_sel(kncc_gf p, kncc_gf q, int b) {
    int64_t t, c = ~(int64_t)(b - 1);
    for (int i = 0; i < 16; i++) {
        t = c & (p[i] ^ q[i]);
        p[i] ^= t;
        q[i] ^= t;
    }
}

static void kncc_pack(unsigned char *o, const kncc_gf n) {
    kncc_gf m, t;
    int b;
    for (int i = 0; i < 16; i++) t[i] = n[i];
    kncc_car(t); kncc_car(t); kncc_car(t);
    for (int j = 0; j < 2; j++) {
        m[0] = t[0] - 0xffed;
        for (int i = 1; i < 15; i++) {
            m[i] = t[i] - 0xffff - ((m[i - 1] >> 16) & 1);
            m[i - 1] &= 0xffff;
        }
        m[15] = t[15] - 0x7fff - ((m[14] >> 16) & 1);
        b = (int)((m[15] >> 16) & 1);
        m[14] &= 0xffff;
        kncc_sel(t, m, 1 - b);
    }
    for (int i = 0; i < 16; i++) {
        o[2 * i] = (unsigned char)(t[i] & 0xff);
        o[2 * i + 1] = (unsigned char)(t[i] >> 8);
    }
}

static int kncc_neq(const kncc_gf a, const kncc_gf b) {
    unsigned char c[32], d[32];
    kncc_pack(c, a);
    kncc_pack(d, b);
    return memcmp(c, d, 32) != 0;
}

static int kncc_par(const kncc_gf a) {
    unsigned char d[32];
    kncc_pack(d, a);
    return d[0] & 1;
}

static void kncc_unpack(kncc_gf o, const unsigned char *n) {
    for (int i = 0; i < 16; i++) o[i] = n[2 * i] + ((int64_t)n[2 * i + 1] << 8);
    o[15] &= 0x7fff;
}

static void kncc_A(kncc_gf o, const kncc_gf a, const kncc_gf b) { for (int i = 0; i < 16; i++) o[i] = a[i] + b[i]; }
static void kncc_Z(kncc_gf o, const kncc_gf a, const kncc_gf b) { for (int i = 0; i < 16; i++) o[i] = a[i] - b[i]; }

static void kncc_M(kncc_gf o, const kncc_gf a, const kncc_gf b) {
    int64_t t[31];
    for (int i = 0; i < 31; i++) t[i] = 0;
    for (int i = 0; i < 16; i++)
        for (int j = 0; j < 16; j++) t[i + j] += a[i] * b[j];
    for (int i = 0; i < 15; i++) t[i] += 38 * t[i + 16];
    for (int i = 0; i < 16; i++) o[i] = t[i];
    kncc_car(o);
    kncc_car(o);
}

static void kncc_S(kncc_gf o, const kncc_gf a) { kncc_M(o, a, a); }

static void kncc_inv(kncc_gf o, const kncc_gf i) {
    kncc_gf c;
    kncc_set(c, i);
    for (int a = 253; a >= 0; a--) {
        kncc_S(c, c);
        if (a != 2 && a != 4) kncc_M(c, c, i);
    }
    kncc_set(o, c);
}

static void kncc_pow2523(kncc_gf o, const kncc_gf i) {
    kncc_gf c;
    kncc_set(c, i);
    for (int a = 250; a >= 0; a--) {
        kncc_S(c, c);
        if (a != 1) kncc_M(c, c, i);
    }
    kncc_set(o, c);
}

/* X25519(n, p). */
static void kncc_x25519(unsigned char *q, const unsigned char *n, const unsigned char *p) {
    unsigned char z[32];
    int64_t r;
    kncc_gf a, b, c, d, e, f, x;
    for (int i = 0; i < 31; i++) z[i] = n[i];
    z[31] = (unsigned char)((n[31] & 127) | 64);
    z[0] &= 248;
    kncc_unpack(x, p);
    for (int i = 0; i < 16; i++) {
        b[i] = x[i];
        d[i] = a[i] = c[i] = 0;
    }
    a[0] = d[0] = 1;
    for (int i = 254; i >= 0; --i) {
        r = (z[i >> 3] >> (i & 7)) & 1;
        kncc_sel(a, b, (int)r); kncc_sel(c, d, (int)r);
        kncc_A(e, a, c); kncc_Z(a, a, c); kncc_A(c, b, d); kncc_Z(b, b, d);
        kncc_S(d, e); kncc_S(f, a); kncc_M(a, c, a); kncc_M(c, b, e);
        kncc_A(e, a, c); kncc_Z(a, a, c); kncc_S(b, a); kncc_Z(c, d, f);
        kncc_M(a, c, kncc_121665); kncc_A(a, a, d); kncc_M(c, c, a); kncc_M(a, d, f); kncc_M(d, b, x); kncc_S(b, e);
        kncc_sel(a, b, (int)r); kncc_sel(c, d, (int)r);
    }
    kncc_inv(c, c);
    kncc_M(a, a, c);
    kncc_pack(q, a);
}

static void kncc_padd(kncc_gf p[4], kncc_gf q[4]) {
    kncc_gf a, b, c, d, t, e, f, g, h;
    kncc_Z(a, p[1], p[0]); kncc_Z(t, q[1], q[0]); kncc_M(a, a, t);
    kncc_A(b, p[0], p[1]); kncc_A(t, q[0], q[1]); kncc_M(b, b, t);
    kncc_M(c, p[3], q[3]); kncc_M(c, c, kncc_D2);
    kncc_M(d, p[2], q[2]); kncc_A(d, d, d);
    kncc_Z(e, b, a); kncc_Z(f, d, c); kncc_A(g, d, c); kncc_A(h, b, a);
    kncc_M(p[0], e, f); kncc_M(p[1], h, g); kncc_M(p[2], g, f); kncc_M(p[3], e, h);
}

static void kncc_cswap(kncc_gf p[4], kncc_gf q[4], int b) {
    for (int i = 0; i < 4; i++) kncc_sel(p[i], q[i], b);
}

static void kncc_ppack(unsigned char *r, kncc_gf p[4]) {
    kncc_gf tx, ty, zi;
    kncc_inv(zi, p[2]);
    kncc_M(tx, p[0], zi);
    kncc_M(ty, p[1], zi);
    kncc_pack(r, ty);
    r[31] ^= (unsigned char)(kncc_par(tx) << 7);
}

static void kncc_scalarmult(kncc_gf p[4], kncc_gf q[4], const unsigned char *s) {
    kncc_set(p[0], kncc_gf0); kncc_set(p[1], kncc_gf1); kncc_set(p[2], kncc_gf1); kncc_set(p[3], kncc_gf0);
    for (int i = 255; i >= 0; --i) {
        int b = (s[i / 8] >> (i & 7)) & 1;
        kncc_cswap(p, q, b);
        kncc_padd(q, p);
        kncc_padd(p, p);
        kncc_cswap(p, q, b);
    }
}

static void kncc_scalarbase(kncc_gf p[4], const unsigned char *s) {
    kncc_gf q[4];
    kncc_set(q[0], kncc_X); kncc_set(q[1], kncc_Y); kncc_set(q[2], kncc_gf1); kncc_M(q[3], kncc_X, kncc_Y);
    kncc_scalarmult(p, q, s);
}

static const int64_t kncc_L[32] = {0xed, 0xd3, 0xf5, 0x5c, 0x1a, 0x63, 0x12, 0x58, 0xd6, 0x9c, 0xf7,
                                   0xa2, 0xde, 0xf9, 0xde, 0x14, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x10};

static void kncc_modL(unsigned char *r, int64_t x[64]) {
    int64_t carry;
    int i, j;
    for (i = 63; i >= 32; --i) {
        carry = 0;
        for (j = i - 32; j < i - 12; ++j) {
            x[j] += carry - 16 * x[i] * kncc_L[j - (i - 32)];
            carry = (x[j] + 128) >> 8;
            x[j] -= carry * 256;
        }
        x[j] += carry;
        x[i] = 0;
    }
    carry = 0;
    for (j = 0; j < 32; j++) {
        x[j] += carry - (x[31] >> 4) * kncc_L[j];
        carry = x[j] >> 8;
        x[j] &= 255;
    }
    for (j = 0; j < 32; j++) x[j] -= carry * kncc_L[j];
    for (i = 0; i < 32; i++) {
        x[i + 1] += x[i] >> 8;
        r[i] = (unsigned char)(x[i] & 255);
    }
}

static void kncc_reduce(unsigned char *r) {
    int64_t x[64];
    for (int i = 0; i < 64; i++) x[i] = (int64_t)r[i];
    for (int i = 0; i < 64; i++) r[i] = 0;
    kncc_modL(r, x);
}

static void kncc_ed_pub(const unsigned char seed[32], unsigned char pk[32]) {
    unsigned char d[64];
    kncc_gf p[4];
    CC_SHA512(seed, 32, d);
    d[0] &= 248;
    d[31] &= 127;
    d[31] |= 64;
    kncc_scalarbase(p, d);
    kncc_ppack(pk, p);
}

static void kncc_ed_sign(const unsigned char seed[32], const unsigned char *m, long long n, unsigned char sig[64]) {
    unsigned char d[64], h[64], r[64], pk[32];
    int64_t x[64];
    kncc_gf p[4];
    CC_SHA512_CTX c;
    kncc_ed_pub(seed, pk);
    CC_SHA512(seed, 32, d);
    d[0] &= 248;
    d[31] &= 127;
    d[31] |= 64;
    CC_SHA512_Init(&c);
    CC_SHA512_Update(&c, d + 32, 32);
    { kncc_md_ctx w; w.md = KD_SHA512; w.u.s512 = c; kncc_md_update(&w, m, n); c = w.u.s512; }
    CC_SHA512_Final(r, &c);
    kncc_reduce(r);
    kncc_scalarbase(p, r);
    kncc_ppack(sig, p);
    CC_SHA512_Init(&c);
    CC_SHA512_Update(&c, sig, 32);
    CC_SHA512_Update(&c, pk, 32);
    { kncc_md_ctx w; w.md = KD_SHA512; w.u.s512 = c; kncc_md_update(&w, m, n); c = w.u.s512; }
    CC_SHA512_Final(h, &c);
    kncc_reduce(h);
    for (int i = 0; i < 64; i++) x[i] = 0;
    for (int i = 0; i < 32; i++) x[i] = (int64_t)r[i];
    for (int i = 0; i < 32; i++)
        for (int j = 0; j < 32; j++) x[i + j] += h[i] * (int64_t)d[j];
    kncc_modL(sig + 32, x);
}

static int kncc_unpackneg(kncc_gf r[4], const unsigned char p[32]) {
    kncc_gf t, chk, num, den, den2, den4, den6;
    kncc_set(r[2], kncc_gf1);
    kncc_unpack(r[1], p);
    kncc_S(num, r[1]); kncc_M(den, num, kncc_D); kncc_Z(num, num, r[2]); kncc_A(den, r[2], den);
    kncc_S(den2, den); kncc_S(den4, den2); kncc_M(den6, den4, den2); kncc_M(t, den6, num); kncc_M(t, t, den);
    kncc_pow2523(t, t); kncc_M(t, t, num); kncc_M(t, t, den); kncc_M(t, t, den); kncc_M(r[0], t, den);
    kncc_S(chk, r[0]); kncc_M(chk, chk, den);
    if (kncc_neq(chk, num)) kncc_M(r[0], r[0], kncc_I);
    kncc_S(chk, r[0]); kncc_M(chk, chk, den);
    if (kncc_neq(chk, num)) return -1;
    if (kncc_par(r[0]) == (p[31] >> 7)) kncc_Z(r[0], kncc_gf0, r[0]);
    kncc_M(r[3], r[0], r[1]);
    return 0;
}

static int kncc_ed_verify(const unsigned char pk[32], const unsigned char *m, long long n, const unsigned char *sig,
                          long long siglen) {
    unsigned char h[64], t[32];
    kncc_gf p[4], q[4];
    CC_SHA512_CTX c;
    if (siglen != 64) return 0;
    /* S must be canonical (< L), as libcrypto requires. */
    for (int i = 31; i >= 0; i--) {
        if (sig[32 + i] < kncc_L[i]) break;
        if (sig[32 + i] > kncc_L[i] || i == 0) return 0;
    }
    if (kncc_unpackneg(q, pk)) return 0;
    CC_SHA512_Init(&c);
    CC_SHA512_Update(&c, sig, 32);
    CC_SHA512_Update(&c, pk, 32);
    { kncc_md_ctx w; w.md = KD_SHA512; w.u.s512 = c; kncc_md_update(&w, m, n); c = w.u.s512; }
    CC_SHA512_Final(h, &c);
    kncc_reduce(h);
    kncc_scalarmult(p, q, h);
    kncc_scalarbase(q, sig + 32);
    kncc_padd(p, q);
    kncc_ppack(t, p);
    return memcmp(t, sig, 32) == 0;
}

/* ---- PEM <-> DER ---- */

static const unsigned char kncc_oid_ed25519[] = {0x06, 0x03, 0x2b, 0x65, 0x70};
static const unsigned char kncc_oid_x25519[] = {0x06, 0x03, 0x2b, 0x65, 0x6e};

static void kncc_pem_append(kml_der *d, const char *label, const unsigned char *der, size_t n) {
    static const char b64[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    char line[80];
    snprintf(line, sizeof line, "-----BEGIN %s-----\n", label);
    kml_der_put(d, (const unsigned char *)line, strlen(line));
    size_t col = 0;
    for (size_t i = 0; i < n; i += 3) {
        unsigned v = (unsigned)der[i] << 16 | (i + 1 < n ? (unsigned)der[i + 1] << 8 : 0) | (i + 2 < n ? der[i + 2] : 0);
        char q[4] = {b64[v >> 18 & 63], b64[v >> 12 & 63], i + 1 < n ? b64[v >> 6 & 63] : '=', i + 2 < n ? b64[v & 63] : '='};
        kml_der_put(d, (const unsigned char *)q, 4);
        col += 4;
        if (col == 64) {
            kml_der_put(d, (const unsigned char *)"\n", 1);
            col = 0;
        }
    }
    if (col) kml_der_put(d, (const unsigned char *)"\n", 1);
    snprintf(line, sizeof line, "-----END %s-----\n", label);
    kml_der_put(d, (const unsigned char *)line, strlen(line));
}

static int kncc_b64val(char c) {
    if (c >= 'A' && c <= 'Z') return c - 'A';
    if (c >= 'a' && c <= 'z') return c - 'a' + 26;
    if (c >= '0' && c <= '9') return c - '0' + 52;
    if (c == '+') return 62;
    if (c == '/') return 63;
    return -1;
}

/* The first PEM block: its label (into label) and DER (malloc'd). */
static unsigned char *kncc_pem_decode(const char *pem, char *label, size_t labelcap, size_t *derlen, char *dek,
                                      size_t dekcap) {
    const char *b = pem ? strstr(pem, "-----BEGIN ") : NULL;
    if (!b) return NULL;
    b += 11;
    const char *e = strstr(b, "-----");
    if (!e || (size_t)(e - b) >= labelcap) return NULL;
    memcpy(label, b, (size_t)(e - b));
    label[e - b] = 0;
    const char *body = e + 5;
    char endmark[96];
    snprintf(endmark, sizeof endmark, "-----END %s-----", label);
    const char *end = strstr(body, endmark);
    if (!end) return NULL;
    /* A legacy encrypted PEM: RFC 1421 headers, then a blank line. */
    dek[0] = 0;
    if (memchr(body, ':', (size_t)(end - body))) {
        const char *d = strstr(body, "DEK-Info:");
        const char *blank = strstr(body, "\n\n");
        const char *blank2 = strstr(body, "\r\n\r\n");
        if (blank2 && (!blank || blank2 < blank)) blank = blank2 + 2;
        if (!d || d > end || !blank || blank > end || !strstr(body, "Proc-Type:")) return NULL;
        d += 9;
        while (*d == ' ') d++;
        size_t k = 0;
        while (d < end && *d != '\r' && *d != '\n' && k + 1 < dekcap) dek[k++] = *d++;
        dek[k] = 0;
        body = blank + 2;
    }
    unsigned char *out = (unsigned char *)malloc((size_t)(end - body) + 1);
    size_t n = 0;
    unsigned acc = 0;
    int bits = 0;
    for (const char *p = body; p < end; p++) {
        if (*p == '=') break;
        int v = kncc_b64val(*p);
        if (v < 0) {
            if (*p == '\n' || *p == '\r' || *p == ' ' || *p == '\t') continue;
            free(out);
            return NULL;
        }
        acc = acc << 6 | (unsigned)v;
        bits += 6;
        if (bits >= 8) {
            bits -= 8;
            out[n++] = (unsigned char)(acc >> bits);
        }
    }
    *derlen = n;
    return out;
}

/* ---- encrypted private keys ---- */

static int kncc_der_eq(const unsigned char *c, size_t clen, const unsigned char *oid, size_t oidlen);

/* CBC decrypt with PKCS#7 padding: 0 ok, 1 a bad decrypt. */
static int kncc_cbc_decrypt(CCAlgorithm alg, const unsigned char *key, size_t keylen, const unsigned char *iv,
                            const unsigned char *in, size_t n, unsigned char **out, size_t *outn) {
    size_t moved = 0;
    unsigned char *buf = (unsigned char *)malloc(n + 16);
    if (!buf) return 2;
    /* The padding is checked here: CCCrypt does not reject a bad one. */
    size_t bs = alg == kCCAlgorithmAES ? 16 : 8;
    if (n == 0 || n % bs || CCCrypt(kCCDecrypt, alg, 0, key, keylen, iv, in, n, buf, n + 16, &moved) != kCCSuccess ||
        moved != n) {
        free(buf);
        return 1;
    }
    unsigned char pad = buf[n - 1];
    int bad = pad == 0 || pad > bs;
    for (size_t i = 0; !bad && i < pad; i++) bad = buf[n - 1 - i] != pad;
    if (bad) {
        free(buf);
        return 1;
    }
    moved = n - pad;
    *out = buf;
    *outn = moved;
    return 0;
}

static int kncc_hexval(char c) {
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    return -1;
}

/* Proc-Type: 4,ENCRYPTED / DEK-Info: <cipher>,<iv hex>; the key is
 * EVP_BytesToKey(MD5, salt = iv[0..8], count 1). 0 ok, 1 bad decrypt, 2
 * unsupported. */
static int kncc_decrypt_legacy(const char *dek, const unsigned char *der, size_t n, const unsigned char *pass,
                               size_t passLen, unsigned char **out, size_t *outn) {
    CCAlgorithm alg;
    size_t keylen, ivlen;
    const char *comma = strchr(dek, ',');
    if (!comma) return 2;
    size_t nl = (size_t)(comma - dek);
    if (nl == 11 && !strncasecmp(dek, "AES-128-CBC", nl)) { alg = kCCAlgorithmAES; keylen = 16; ivlen = 16; }
    else if (nl == 11 && !strncasecmp(dek, "AES-192-CBC", nl)) { alg = kCCAlgorithmAES; keylen = 24; ivlen = 16; }
    else if (nl == 11 && !strncasecmp(dek, "AES-256-CBC", nl)) { alg = kCCAlgorithmAES; keylen = 32; ivlen = 16; }
    else if (nl == 12 && !strncasecmp(dek, "DES-EDE3-CBC", nl)) { alg = kCCAlgorithm3DES; keylen = 24; ivlen = 8; }
    else return 2;
    unsigned char iv[16], key[32], d[16];
    const char *h = comma + 1;
    for (size_t i = 0; i < ivlen; i++) {
        int a = kncc_hexval(h[2 * i]), b = a < 0 ? -1 : kncc_hexval(h[2 * i + 1]);
        if (b < 0) return 2;
        iv[i] = (unsigned char)(a << 4 | b);
    }
    size_t got = 0, dl = 0;
    while (got < keylen) {
        kncc_md_ctx c;
        kncc_md_init(&c, KD_MD5);
        if (dl) kncc_md_update(&c, d, (long long)dl);
        kncc_md_update(&c, pass, (long long)passLen);
        kncc_md_update(&c, iv, 8);
        kncc_md_final(&c, d);
        dl = 16;
        size_t take = keylen - got < 16 ? keylen - got : 16;
        memcpy(key + got, d, take);
        got += take;
    }
    int rc = kncc_cbc_decrypt(alg, key, keylen, iv, der, n, out, outn);
    memset(key, 0, sizeof key);
    return rc;
}

static const unsigned char kncc_oid_pbes2[] = {0x06, 0x09, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x05, 0x0d};
static const unsigned char kncc_oid_pbkdf2[] = {0x06, 0x09, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x05, 0x0c};
static const unsigned char kncc_oid_hmac_pfx[] = {0x06, 0x08, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x02};
static const unsigned char kncc_oid_aes_pfx[] = {0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x01};
static const unsigned char kncc_oid_des3[] = {0x06, 0x08, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x03, 0x07};

/* EncryptedPrivateKeyInfo with PBES2 (PBKDF2-HMAC + AES-CBC / 3DES-CBC) →
 * the PKCS#8 DER. 0 ok, 1 bad decrypt, 2 unsupported/malformed. */
static int kncc_decrypt_pkcs8(const unsigned char *der, size_t n, const unsigned char *pass, size_t passLen,
                              unsigned char **out, size_t *outn) {
    const unsigned char *p = der, *end = der + n, *c, *a, *oc, *kp, *ep, *sc;
    unsigned char tag;
    size_t clen, alen, oclen, kplen, eplen, sclen;
    if (!kml_der_read(&p, end, &tag, &c, &clen) || tag != 0x30) return 2;
    p = c; end = c + clen;
    if (!kml_der_read(&p, end, &tag, &a, &alen) || tag != 0x30) return 2;
    const unsigned char *data; size_t datalen;
    if (!kml_der_read(&p, end, &tag, &data, &datalen) || tag != 0x04) return 2;
    /* AlgorithmIdentifier { pbes2, { kdf, enc } } */
    const unsigned char *q = a, *qe = a + alen;
    if (!kml_der_read(&q, qe, &tag, &oc, &oclen) || tag != 0x06 ||
        !kncc_der_eq(oc, oclen, kncc_oid_pbes2, sizeof kncc_oid_pbes2)) return 2;
    if (!kml_der_read(&q, qe, &tag, &c, &clen) || tag != 0x30) return 2;
    q = c; qe = c + clen;
    if (!kml_der_read(&q, qe, &tag, &kp, &kplen) || tag != 0x30) return 2;
    if (!kml_der_read(&q, qe, &tag, &ep, &eplen) || tag != 0x30) return 2;
    /* PBKDF2 { salt, iterations, [keyLength], [prf] } */
    const unsigned char *r = kp, *re = kp + kplen;
    if (!kml_der_read(&r, re, &tag, &oc, &oclen) || tag != 0x06 ||
        !kncc_der_eq(oc, oclen, kncc_oid_pbkdf2, sizeof kncc_oid_pbkdf2)) return 2;
    if (!kml_der_read(&r, re, &tag, &c, &clen) || tag != 0x30) return 2;
    r = c; re = c + clen;
    const unsigned char *salt; size_t saltlen;
    if (!kml_der_read(&r, re, &tag, &salt, &saltlen) || tag != 0x04) return 2;
    const unsigned char *iv; size_t ivn;
    if (!kml_der_read_uint(&r, re, &iv, &ivn) || ivn > 4) return 2;
    unsigned long long iter = 0;
    for (size_t i = 0; i < ivn; i++) iter = iter << 8 | iv[i];
    int md = KD_SHA1;
    while (r < re) {
        if (!kml_der_read(&r, re, &tag, &sc, &sclen)) return 2;
        if (tag == 0x30) {
            const unsigned char *s = sc, *o; size_t ol;
            if (!kml_der_read(&s, sc + sclen, &tag, &o, &ol) || tag != 0x06) return 2;
            if (ol != 8 || memcmp(o, kncc_oid_hmac_pfx + 2, 7) != 0) return 2;
            switch (o[7]) {
            case 0x07: md = KD_SHA1; break;
            case 0x08: md = KD_SHA224; break;
            case 0x09: md = KD_SHA256; break;
            case 0x0a: md = KD_SHA384; break;
            case 0x0b: md = KD_SHA512; break;
            default: return 2;
            }
        }
    }
    /* encryption scheme { oid, iv } */
    const unsigned char *s = ep, *se = ep + eplen;
    if (!kml_der_read(&s, se, &tag, &oc, &oclen) || tag != 0x06) return 2;
    CCAlgorithm alg;
    size_t keylen, ivlen;
    if (oclen == 9 && memcmp(oc, kncc_oid_aes_pfx + 2, 8) == 0 && (oc[8] == 0x02 || oc[8] == 0x16 || oc[8] == 0x2a)) {
        alg = kCCAlgorithmAES;
        keylen = oc[8] == 0x02 ? 16 : oc[8] == 0x16 ? 24 : 32;
        ivlen = 16;
    } else if (kncc_der_eq(oc, oclen, kncc_oid_des3, sizeof kncc_oid_des3)) {
        alg = kCCAlgorithm3DES;
        keylen = 24;
        ivlen = 8;
    } else {
        return 2;
    }
    const unsigned char *eiv; size_t eivlen;
    if (!kml_der_read(&s, se, &tag, &eiv, &eivlen) || tag != 0x04 || eivlen != ivlen) return 2;
    unsigned char key[32];
    if (kncc_pbkdf2_run(md, pass, passLen, salt, saltlen, (double)iter, key, keylen) != 0) return 2;
    int rc = kncc_cbc_decrypt(alg, key, keylen, eiv, data, datalen, out, outn);
    memset(key, 0, sizeof key);
    return rc;
}

enum { KK_RSA = 0, KK_EC, KK_ED25519, KK_X25519 };

typedef struct {
    int type, priv;
    ll curve;              /* EC: 1/2/3 */
    SecKeyRef sk;          /* RSA/EC */
    unsigned char raw[32]; /* Ed25519 seed or public key */
    int raw_has_pub;
    unsigned char pub[32];
} kncc_key;

static int kncc_der_eq(const unsigned char *c, size_t clen, const unsigned char *oid, size_t oidlen) {
    /* c/clen: an OID's content; oid: tag+len+content. */
    return clen == oidlen - 2 && memcmp(c, oid + 2, clen) == 0;
}

static ll kncc_curve_from_oid(const unsigned char *c, size_t clen) {
    for (ll id = 1; id <= 3; id++) {
        size_t n;
        const unsigned char *o = kml_curve_oid(id, &n);
        if (kncc_der_eq(c, clen, o, n)) return id;
    }
    return 0;
}

/* AlgorithmIdentifier SEQ content → key type (+ curve). */
static int kncc_algid(const unsigned char *c, size_t clen, int *type, ll *curve) {
    const unsigned char *p = c, *end = c + clen, *oc, *pc;
    unsigned char tag;
    size_t oclen, pclen;
    if (!kml_der_read(&p, end, &tag, &oc, &oclen) || tag != 0x06) return 0;
    if (kncc_der_eq(oc, oclen, kml_oid_rsa, sizeof kml_oid_rsa)) { *type = KK_RSA; return 1; }
    if (kncc_der_eq(oc, oclen, kncc_oid_ed25519, sizeof kncc_oid_ed25519)) { *type = KK_ED25519; return 1; }
    if (kncc_der_eq(oc, oclen, kncc_oid_x25519, sizeof kncc_oid_x25519)) { *type = KK_X25519; return 1; }
    if (kncc_der_eq(oc, oclen, kml_oid_ec, sizeof kml_oid_ec)) {
        if (!kml_der_read(&p, end, &tag, &pc, &pclen) || tag != 0x06) return 0;
        *curve = kncc_curve_from_oid(pc, pclen);
        *type = KK_EC;
        return *curve != 0;
    }
    return 0;
}

/* SEC1 ECPrivateKey (content of its SEQ) → SecKey; curve may come from [0]. */
static SecKeyRef kncc_ec_priv(const unsigned char *c, size_t clen, ll *curve) {
    const unsigned char *p = c, *end = c + clen, *ic, *k = NULL, *pt = NULL;
    unsigned char tag;
    size_t iclen, kn = 0, ptlen = 0;
    if (!kml_der_read(&p, end, &tag, &ic, &iclen) || tag != 0x02) return NULL;
    if (!kml_der_read(&p, end, &tag, &ic, &iclen) || tag != 0x04) return NULL;
    k = ic;
    kn = iclen;
    while (p < end) {
        if (!kml_der_read(&p, end, &tag, &ic, &iclen)) return NULL;
        const unsigned char *bp = ic, *bend = ic + iclen, *bc;
        size_t bclen;
        unsigned char t2;
        if (tag == 0xa0 && kml_der_read(&bp, bend, &t2, &bc, &bclen) && t2 == 0x06) {
            ll cv = kncc_curve_from_oid(bc, bclen);
            if (*curve == 0) *curve = cv;
        } else if (tag == 0xa1 && kml_der_read(&bp, bend, &t2, &bc, &bclen) && t2 == 0x03 && bclen > 1) {
            pt = bc + 1;
            ptlen = bclen - 1;
        }
    }
    ll cb = kml_curve_bytes(*curve);
    if (!cb || !pt || ptlen != (size_t)(1 + 2 * cb) || kn > (size_t)cb) return NULL;
    size_t xl = ptlen + (size_t)cb;
    unsigned char *x = (unsigned char *)calloc(1, xl);
    memcpy(x, pt, ptlen);
    memcpy(x + ptlen + ((size_t)cb - kn), k, kn);
    SecKeyRef sk = kml_seckey_import(x, xl, 0, 1);
    free(x);
    return sk;
}

static int kncc_parse_key(const char *pem, int wantPriv, const unsigned char *pass, long long passLen, kncc_key *key) {
    char label[64], dek[128];
    size_t n = 0;
    unsigned char *der = kncc_pem_decode(pem, label, sizeof label, &n, dek, sizeof dek);
    int ok = 0;
    memset(key, 0, sizeof *key);
    kncc_err[0] = 0;
    if (!der) return 0;
    if (dek[0] || strcmp(label, "ENCRYPTED PRIVATE KEY") == 0) {
        /* A public-key read first skips an encrypted block, as libcrypto's
         * PUBKEY decoder does; then the private read asks for the phrase. */
        if (passLen < 0) {
            kncc_set_err(KNCC_E_INTERRUPTED);
            free(der);
            return 0;
        }
        unsigned char *plain = NULL;
        size_t plen = 0;
        int rc = dek[0] ? kncc_decrypt_legacy(dek, der, n, pass, (size_t)passLen, &plain, &plen)
                        : kncc_decrypt_pkcs8(der, n, pass, (size_t)passLen, &plain, &plen);
        free(der);
        if (rc != 0) {
            if (rc == 1) kncc_set_err(KNCC_E_BAD_DECRYPT);
            return 0;
        }
        der = plain;
        n = plen;
        if (!dek[0]) snprintf(label, sizeof label, "PRIVATE KEY");
    }
    const unsigned char *p = der, *end = der + n, *c, *ac, *oc;
    unsigned char tag;
    size_t clen, aclen, oclen;
    if (!kml_der_read(&p, end, &tag, &c, &clen) || tag != 0x30) goto done;
    p = c;
    end = c + clen;
    if (strcmp(label, "PRIVATE KEY") == 0) {
        if (!kml_der_read(&p, end, &tag, &oc, &oclen) || tag != 0x02) goto done;
        if (!kml_der_read(&p, end, &tag, &ac, &aclen) || tag != 0x30) goto done;
        if (!kncc_algid(ac, aclen, &key->type, &key->curve)) goto done;
        if (!kml_der_read(&p, end, &tag, &oc, &oclen) || tag != 0x04) goto done;
        key->priv = 1;
        if (key->type == KK_RSA) {
            key->sk = kml_seckey_import(oc, oclen, 1, 1);
        } else if (key->type == KK_EC) {
            const unsigned char *q = oc, *qc;
            size_t qclen;
            if (!kml_der_read(&q, oc + oclen, &tag, &qc, &qclen) || tag != 0x30) goto done;
            key->sk = kncc_ec_priv(qc, qclen, &key->curve);
        } else {
            const unsigned char *q = oc, *qc;
            size_t qclen;
            if (!kml_der_read(&q, oc + oclen, &tag, &qc, &qclen) || tag != 0x04 || qclen != 32) goto done;
            memcpy(key->raw, qc, 32);
            ok = 1;
            goto done;
        }
        ok = key->sk != NULL;
    } else if (strcmp(label, "RSA PRIVATE KEY") == 0) {
        key->type = KK_RSA;
        key->priv = 1;
        key->sk = kml_seckey_import(der, n, 1, 1);
        ok = key->sk != NULL;
    } else if (strcmp(label, "EC PRIVATE KEY") == 0) {
        key->type = KK_EC;
        key->priv = 1;
        key->sk = kncc_ec_priv(c, clen, &key->curve);
        ok = key->sk != NULL;
    } else if (!wantPriv && strcmp(label, "PUBLIC KEY") == 0) {
        if (!kml_der_read(&p, end, &tag, &ac, &aclen) || tag != 0x30) goto done;
        if (!kncc_algid(ac, aclen, &key->type, &key->curve)) goto done;
        if (!kml_der_read(&p, end, &tag, &oc, &oclen) || tag != 0x03 || oclen < 2 || oc[0] != 0) goto done;
        if (key->type == KK_RSA || key->type == KK_EC) {
            key->sk = kml_seckey_import(oc + 1, oclen - 1, key->type == KK_RSA, 0);
            ok = key->sk != NULL;
        } else if (oclen - 1 == 32) {
            memcpy(key->raw, oc + 1, 32);
            ok = 1;
        }
    } else if (!wantPriv && strcmp(label, "RSA PUBLIC KEY") == 0) {
        key->type = KK_RSA;
        key->sk = kml_seckey_import(der, n, 1, 0);
        ok = key->sk != NULL;
    }
done:
    free(der);
    if (!ok && key->sk) {
        CFRelease(key->sk);
        key->sk = NULL;
    }
    return ok;
}

static void kncc_key_free(kncc_key *k) {
    if (k->sk) CFRelease(k->sk);
    memset(k, 0, sizeof *k);
}

/* ---- key pairs ---- */

static ll kncc_curve_by_name(const char *name) {
    if (!name) return 0;
    if (!strcasecmp(name, "P-256") || !strcmp(name, "prime256v1") || !strcmp(name, "secp256r1")) return 1;
    if (!strcasecmp(name, "P-384") || !strcmp(name, "secp384r1")) return 2;
    if (!strcasecmp(name, "P-521") || !strcmp(name, "secp521r1")) return 3;
    return 0;
}

/* The PEM pair (public SPKI then private PKCS#8), malloc'd, or NULL with the
 * thread's error text set where libcrypto would set one. */
static char *kncc_keygen_pem(double type, double bits, double exponent, const char *curve) {
    unsigned char *pkcs8 = NULL, *spki = NULL;
    ll p8len = 0, splen = 0;
    int t = (int)type;
    if (t == 0) {
        if (bits < 512) {
            kncc_set_err(KNCC_E_KEY_SMALL);
            return NULL;
        }
        /* SecKey generates only the F4 exponent. */
        if (exponent > 0 && exponent != 65537) return NULL;
        if (__kml_crypto_gen_rsa((long long)bits, &pkcs8, &p8len, &spki, &splen) != 0) return NULL;
    } else if (t == 1) {
        ll cv = kncc_curve_by_name(curve);
        if (!cv) {
            kncc_set_err(KNCC_E_UNKNOWN_OBJ);
            return NULL;
        }
        if (__kml_crypto_gen_ec(cv, &pkcs8, &p8len, &spki, &splen) != 0) return NULL;
    } else if (t == 2 || t == 3) {
        const unsigned char *oid = t == 2 ? kncc_oid_ed25519 : kncc_oid_x25519;
        unsigned char seed[32], pub[32];
        static const unsigned char nine[32] = {9};
        if (SecRandomCopyBytes(kSecRandomDefault, 32, seed) != errSecSuccess) return NULL;
        if (t == 2) kncc_ed_pub(seed, pub);
        else kncc_x25519(pub, seed, nine);
        spki = (unsigned char *)malloc(44);
        pkcs8 = (unsigned char *)malloc(48);
        static const unsigned char sp[] = {0x30, 0x2a, 0x30, 0x05};
        static const unsigned char pk[] = {0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05};
        memcpy(spki, sp, 4);
        memcpy(spki + 4, oid, 5);
        spki[9] = 0x03; spki[10] = 0x21; spki[11] = 0x00;
        memcpy(spki + 12, pub, 32);
        memcpy(pkcs8, pk, 7);
        memcpy(pkcs8 + 7, oid, 5);
        pkcs8[12] = 0x04; pkcs8[13] = 0x22; pkcs8[14] = 0x04; pkcs8[15] = 0x20;
        memcpy(pkcs8 + 16, seed, 32);
        splen = 44;
        p8len = 48;
        memset(seed, 0, sizeof seed);
    } else {
        return NULL;
    }
    kml_der d = {0};
    kncc_pem_append(&d, "PUBLIC KEY", spki, (size_t)splen);
    kncc_pem_append(&d, "PRIVATE KEY", pkcs8, (size_t)p8len);
    kml_der_put(&d, (const unsigned char *)"", 1);
    memset(pkcs8, 0, (size_t)p8len);
    free(pkcs8);
    free(spki);
    return (char *)d.buf;
}

char *__kml_native_crypto_keygen(double type, double bits, double exponent, const char *curve) {
    char *pem = kncc_keygen_pem(type, bits, exponent, curve);
    char *out = kncc_str(pem ? pem : "");
    free(pem);
    return out;
}

typedef struct { double type, bits, exponent; char curve[64]; } kncc_keygen_job;

static void kncc_keygen_work(void *job, long long *err, double *res) {
    kncc_keygen_job *j = (kncc_keygen_job *)job;
    char *pem = kncc_keygen_pem(j->type, j->bits, j->exponent, j->curve);
    *err = pem ? 0 : 1;
    *res = (double)(intptr_t)pem;
    free(j);
}

void __kml_native_crypto_keygen_async(double type, double bits, double exponent, const char *curve, void *inv, void *clo) {
    kncc_keygen_job *j = (kncc_keygen_job *)calloc(1, sizeof *j);
    j->type = type; j->bits = bits; j->exponent = exponent;
    snprintf(j->curve, sizeof j->curve, "%s", curve ? curve : "");
    __kml_pool_job(kncc_keygen_work, j, inv, clo);
}

char *__kml_native_crypto_keygen_take(double result) {
    char *pem = (char *)(intptr_t)result;
    char *out = kncc_str(pem ? pem : "");
    free(pem);
    return out;
}

/* ---- sign / verify ---- */

static const unsigned char kncc_digestinfo[KD_COUNT][19] = {
    {0x30, 0x20, 0x30, 0x0c, 0x06, 0x08, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x02, 0x05, 0x05, 0x00, 0x04, 0x10},
    {0x30, 0x21, 0x30, 0x09, 0x06, 0x05, 0x2b, 0x0e, 0x03, 0x02, 0x1a, 0x05, 0x00, 0x04, 0x14},
    {0x30, 0x2d, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x04, 0x05, 0x00, 0x04, 0x1c},
    {0x30, 0x31, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x01, 0x05, 0x00, 0x04, 0x20},
    {0x30, 0x41, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x02, 0x05, 0x00, 0x04, 0x30},
    {0x30, 0x51, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x03, 0x05, 0x00, 0x04, 0x40},
};
static const size_t kncc_digestinfo_len[KD_COUNT] = {18, 15, 19, 19, 19, 19};

/* What SecKey signs for (digest, key): RSA gets DigestInfo || H(m) under
 * PKCS#1 v1.5 raw, ECDSA gets H(m). An absent or unknown digest name is
 * libcrypto's default for RSA/EC keys, SHA-256. */
static CFDataRef kncc_sig_input(const kncc_key *k, const char *digest, void *data, long long len, SecKeyAlgorithm *alg) {
    int md = kncc_md(digest);
    if (md < 0) md = KD_SHA256;
    unsigned char buf[19 + 64];
    size_t off = 0;
    if (k->type == KK_RSA) {
        memcpy(buf, kncc_digestinfo[md], kncc_digestinfo_len[md]);
        off = kncc_digestinfo_len[md];
        *alg = kSecKeyAlgorithmRSASignatureDigestPKCS1v15Raw;
    } else {
        *alg = kSecKeyAlgorithmECDSASignatureDigestX962;
    }
    kncc_md_oneshot(md, data, len, buf + off);
    return CFDataCreate(NULL, buf, (CFIndex)(off + kncc_md_len[md]));
}

double __kml_native_crypto_sign(const char *digest, const char *pem, void *pass, long long passLen, double hasPass,
                                double padding, double saltLength, double dsaEncoding,
                                void *data, long long len, void *out, long long outLen) {
    kncc_key k;
    /* SecKey has no padding, salt length or P1363 choice to make. */
    if (padding >= 0 || saltLength != 1e9 || dsaEncoding != 0) {
        kncc_set_err(KNCC_E_KEYTYPE);
        return -1;
    }
    if (!kncc_parse_key(pem, 1, (const unsigned char *)pass, hasPass != 0 ? passLen : -1, &k)) {
        if (!kncc_err[0]) kncc_set_err(KNCC_E_DECODER);
        return -1;
    }
    double r = -1;
    if (k.type == KK_ED25519) {
        /* A one-shot key: a digest name is an error, as in libcrypto. */
        if (digest && *digest) {
            kncc_set_err(KNCC_E_INVALID_DIGEST);
        } else if (outLen < 64) {
            r = -2 - 64.0;
        } else {
            kncc_ed_sign(k.raw, (const unsigned char *)data, len, (unsigned char *)out);
            r = 64;
        }
    } else if (k.type == KK_X25519) {
        kncc_set_err(KNCC_E_KEYTYPE);
    } else if (k.type == KK_RSA || k.type == KK_EC) {
        SecKeyAlgorithm alg;
        CFDataRef in = kncc_sig_input(&k, digest, data, len, &alg);
        CFDataRef sig = in ? SecKeyCreateSignature(k.sk, alg, in, NULL) : NULL;
        if (sig) {
            long long n = (long long)CFDataGetLength(sig);
            if (n > outLen) r = -2 - (double)n;
            else {
                memcpy(out, CFDataGetBytePtr(sig), (size_t)n);
                r = (double)n;
            }
            CFRelease(sig);
        }
        if (in) CFRelease(in);
    }
    kncc_key_free(&k);
    return r;
}

double __kml_native_crypto_verify(const char *digest, const char *pem, void *pass, long long passLen, double hasPass,
                                  double padding, double saltLength, double dsaEncoding,
                                  void *data, long long len, void *sig, long long sigLen) {
    kncc_key k;
    if (padding >= 0 || saltLength != 1e9 || dsaEncoding != 0) {
        kncc_set_err(KNCC_E_KEYTYPE);
        return -1;
    }
    if (!kncc_parse_key(pem, 0, (const unsigned char *)pass, hasPass != 0 ? passLen : -1, &k)) {
        if (!kncc_err[0]) kncc_set_err(KNCC_E_DECODER);
        return -1;
    }
    double r = 0;
    if (k.type == KK_ED25519) {
        unsigned char pub[32];
        if (k.priv) kncc_ed_pub(k.raw, pub);
        else memcpy(pub, k.raw, 32);
        if (!(digest && *digest))
            r = kncc_ed_verify(pub, (const unsigned char *)data, len, (const unsigned char *)sig, sigLen) ? 1 : 0;
    } else if (k.type == KK_RSA || k.type == KK_EC) {
        SecKeyRef pub = k.priv ? SecKeyCopyPublicKey(k.sk) : (SecKeyRef)CFRetain(k.sk);
        SecKeyAlgorithm alg;
        CFDataRef in = kncc_sig_input(&k, digest, data, len, &alg);
        CFDataRef s = CFDataCreate(NULL, (const unsigned char *)sig, (CFIndex)sigLen);
        if (pub && in && s) r = SecKeyVerifySignature(pub, alg, in, s, NULL) ? 1 : 0;
        if (s) CFRelease(s);
        if (in) CFRelease(in);
        if (pub) CFRelease(pub);
    }
    kncc_key_free(&k);
    return r;
}

double __kml_native_crypto_timing_equal(void *a, long long alen, void *b, long long blen) {
    if (alen != blen) return 0;
    volatile unsigned char d = 0;
    for (long long i = 0; i < alen; i++) d |= ((unsigned char *)a)[i] ^ ((unsigned char *)b)[i];
    return d == 0 ? 1 : 0;
}

/* The last error as libcrypto words it, or ""; cleared on read. */
char *__kml_native_crypto_last_error(void) {
    char *out = kncc_str(kncc_err);
    kncc_err[0] = 0;
    return out;
}

/* KeyObject's PEM/DER/JWK conversions need OpenSSL's encoders and
 * decoders: on this backend every one fails, which createPrivateKey,
 * createPublicKey and export report as an unsupported key. */
char *__kml_native_crypto_key_parse(void *data, long long len, double format, double type, double wantPriv,
                                    void *pass, long long passLen, double hasPass) {
    (void)data; (void)len; (void)format; (void)type; (void)wantPriv; (void)pass; (void)passLen; (void)hasPass;
    return kncc_str("");
}

double __kml_native_crypto_key_export(const char *pem, double priv, double format, double type, const char *cipher,
                                      void *pass, long long passLen, double hasPass, void *out, long long outLen) {
    (void)pem; (void)priv; (void)format; (void)type; (void)cipher; (void)pass; (void)passLen; (void)hasPass; (void)out; (void)outLen;
    return -1;
}

char *__kml_native_crypto_key_info(const char *pem) {
    (void)pem;
    return kncc_str("");
}

char *__kml_native_crypto_key_jwk(const char *pem, double priv) {
    (void)pem; (void)priv;
    return kncc_str("!type");
}

char *__kml_native_crypto_key_from_jwk(double kty, const char *crv, const char *n, const char *e, const char *d,
                                       const char *p, const char *q, const char *dp, const char *dq, const char *qi,
                                       const char *x, const char *y, double wantPriv) {
    (void)kty; (void)crv; (void)n; (void)e; (void)d; (void)p; (void)q; (void)dp; (void)dq; (void)qi; (void)x; (void)y; (void)wantPriv;
    return kncc_str("");
}

/* publicEncrypt/privateDecrypt/privateEncrypt/publicDecrypt need padding
 * and OAEP choices SecKey does not expose: each fails here. */
double __kml_native_crypto_pkey_crypt(double op, const char *pem, void *pass, long long passLen, double hasPass,
                                      double padding, const char *oaepHash, void *label, long long labelLen,
                                      void *data, long long len, void *out, long long outLen) {
    (void)op; (void)pem; (void)pass; (void)passLen; (void)hasPass; (void)padding; (void)oaepHash;
    (void)label; (void)labelLen; (void)data; (void)len; (void)out; (void)outLen;
    kncc_set_err(KNCC_E_KEYTYPE);
    return -1;
}

/* ECDH and diffieHellman need EC point arithmetic and key derivation this
 * backend does not wire: each fails here. */
double __kml_native_crypto_ecdh(double op, const char *curve, void *priv, long long privLen, void *pub, long long pubLen,
                                double format, void *out, long long outLen) {
    (void)op; (void)curve; (void)priv; (void)privLen; (void)pub; (void)pubLen; (void)format; (void)out; (void)outLen;
    return -8;
}

double __kml_native_crypto_derive_secret(const char *privPem, const char *pubPem, void *out, long long outLen) {
    (void)privPem; (void)pubPem; (void)out; (void)outLen;
    kncc_set_err(KNCC_E_KEYTYPE);
    return -1;
}
