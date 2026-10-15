/* crypto_cng.c — the Windows CNG (bcrypt.dll: BCrypt* primitives)
 * implementation of node:crypto's natives (TDD-00177 Stage 5), next to
 * crypto_openssl.c (the reference) and crypto_commoncrypto.c (macOS). Same
 * ABI, return codes and libcrypto error strings as those two. Error contract
 * shared by every backend: 0 = ok, -1 = OperationError, -2 = DataError,
 * -3 = NotSupportedError.
 *
 * CNG speaks its own key blobs (BCRYPT_RSA*_BLOB, BCRYPT_ECC*_BLOB) rather
 * than DER, so — as in the CommonCrypto backend — a mini-DER layer below
 * converts between them and PKCS#1/PKCS#8/SPKI/SEC1. Keys travel as their
 * components (RSA n/e/d/p/q/dp/dq/qi, EC X/Y/d) and are imported into a CNG
 * handle only for the operation at hand. What CNG has no primitive for is
 * composed here: SHA-224 (a software SHA-256 core), HMAC/HKDF/scrypt over the
 * digests, AES-CTR and AES-GCM over AES-ECB (any IV length, streamed exactly
 * as EVP does), the RSA paddings (PKCS#1 v1.5, PSS, OAEP) over CNG's raw RSA,
 * point (de)compression for ECDH, and Ed25519/X25519 (no CNG Ed25519). */

#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <bcrypt.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifndef BCRYPT_SUCCESS
#define BCRYPT_SUCCESS(s) (((NTSTATUS)(s)) >= 0)
#endif
#ifndef BCRYPT_KDF_RAW_SECRET
#define BCRYPT_KDF_RAW_SECRET L"TRUNCATE"
#endif

typedef long long ll;

extern void __kml_pool_job(void (*work)(void *, long long *, double *), void *job, void *inv, void *clo);
extern char *__kml_str_alloc(long long n);
extern void __kml_str_finalize(char *s);

static char *kncg_str(const char *s) {
    long long n = (long long)strlen(s);
    char *out = __kml_str_alloc(n + 1);
    memcpy(out, s, (size_t)n + 1);
    __kml_str_finalize(out);
    return out;
}

static int kncg_casecmp(const char *a, const char *b) {
    for (;; a++, b++) {
        int x = (unsigned char)*a, y = (unsigned char)*b;
        if (x >= 'A' && x <= 'Z') x += 32;
        if (y >= 'A' && y <= 'Z') y += 32;
        if (x != y || !x) return x - y;
    }
}

static int kncg_ncasecmp(const char *a, const char *b, size_t n) {
    for (size_t i = 0; i < n; i++) {
        int x = (unsigned char)a[i], y = (unsigned char)b[i];
        if (x >= 'A' && x <= 'Z') x += 32;
        if (y >= 'A' && y <= 'Z') y += 32;
        if (x != y || !x) return x - y;
    }
    return 0;
}

static __thread char kncg_err[160];

static void kncg_set_err(const char *s) {
    snprintf(kncg_err, sizeof kncg_err, "%s", s);
}

#define KNCG_E_BAD_DECRYPT "error:1C800064:Provider routines::bad decrypt"
#define KNCG_E_FINAL_LEN "error:1C80006B:Provider routines::wrong final block length"
#define KNCG_E_MEMLIMIT "error:030000AC:digital envelope routines::memory limit exceeded"
#define KNCG_E_DECODER "error:1E08010C:DECODER routines::unsupported"
#define KNCG_E_KEY_SMALL "error:1C8000AB:Provider routines::key size too small"
#define KNCG_E_INVALID_DIGEST "error:1C80007A:Provider routines::invalid digest"
#define KNCG_E_KEYTYPE "error:03000096:digital envelope routines::operation not supported for this keytype"
#define KNCG_E_INTERRUPTED "error:07880109:common libcrypto routines::interrupted or cancelled"
#define KNCG_E_UNKNOWN_OBJ "error:04000067:object identifier routines::unknown object name"
#define KNCG_E_OAEP "error:02000079:rsa routines::oaep decoding error"
#define KNCG_E_DATA_LARGE "error:0200006E:rsa routines::data too large for key size"

/* ── CNG algorithm providers: opened once, shared by every thread (CNG
 * algorithm handles are safe to use concurrently). ──────────────────────── */

enum { KD_MD5 = 0, KD_SHA1, KD_SHA224, KD_SHA256, KD_SHA384, KD_SHA512, KD_COUNT };

static BCRYPT_ALG_HANDLE kncg_alg_md[KD_COUNT];   /* plain digests (no SHA-224 in CNG) */
static BCRYPT_ALG_HANDLE kncg_alg_prf[KD_COUNT];  /* HMAC-flagged, for BCryptDeriveKeyPBKDF2 */
static BCRYPT_ALG_HANDLE kncg_alg_ecb, kncg_alg_cbc, kncg_alg_3des, kncg_alg_rsa;
static BCRYPT_ALG_HANDLE kncg_alg_ecdsa[4], kncg_alg_ecdh[4]; /* by curve id 1..3 */
static INIT_ONCE kncg_once = INIT_ONCE_STATIC_INIT;

static BOOL CALLBACK kncg_init_cb(PINIT_ONCE o, PVOID p, PVOID *ctx) {
    static const LPCWSTR md[KD_COUNT] = {BCRYPT_MD5_ALGORITHM, BCRYPT_SHA1_ALGORITHM, NULL,
                                         BCRYPT_SHA256_ALGORITHM, BCRYPT_SHA384_ALGORITHM, BCRYPT_SHA512_ALGORITHM};
    static const LPCWSTR ecdsa[4] = {NULL, BCRYPT_ECDSA_P256_ALGORITHM, BCRYPT_ECDSA_P384_ALGORITHM,
                                     BCRYPT_ECDSA_P521_ALGORITHM};
    static const LPCWSTR ecdh[4] = {NULL, BCRYPT_ECDH_P256_ALGORITHM, BCRYPT_ECDH_P384_ALGORITHM,
                                    BCRYPT_ECDH_P521_ALGORITHM};
    (void)o; (void)p; (void)ctx;
    for (int i = 0; i < KD_COUNT; i++) {
        if (!md[i]) continue;
        BCryptOpenAlgorithmProvider(&kncg_alg_md[i], md[i], NULL, 0);
        BCryptOpenAlgorithmProvider(&kncg_alg_prf[i], md[i], NULL, BCRYPT_ALG_HANDLE_HMAC_FLAG);
    }
    if (BCRYPT_SUCCESS(BCryptOpenAlgorithmProvider(&kncg_alg_ecb, BCRYPT_AES_ALGORITHM, NULL, 0)))
        BCryptSetProperty(kncg_alg_ecb, BCRYPT_CHAINING_MODE, (PUCHAR)BCRYPT_CHAIN_MODE_ECB,
                          sizeof(BCRYPT_CHAIN_MODE_ECB), 0);
    if (BCRYPT_SUCCESS(BCryptOpenAlgorithmProvider(&kncg_alg_cbc, BCRYPT_AES_ALGORITHM, NULL, 0)))
        BCryptSetProperty(kncg_alg_cbc, BCRYPT_CHAINING_MODE, (PUCHAR)BCRYPT_CHAIN_MODE_CBC,
                          sizeof(BCRYPT_CHAIN_MODE_CBC), 0);
    if (BCRYPT_SUCCESS(BCryptOpenAlgorithmProvider(&kncg_alg_3des, BCRYPT_3DES_ALGORITHM, NULL, 0)))
        BCryptSetProperty(kncg_alg_3des, BCRYPT_CHAINING_MODE, (PUCHAR)BCRYPT_CHAIN_MODE_CBC,
                          sizeof(BCRYPT_CHAIN_MODE_CBC), 0);
    BCryptOpenAlgorithmProvider(&kncg_alg_rsa, BCRYPT_RSA_ALGORITHM, NULL, 0);
    for (int i = 1; i <= 3; i++) {
        BCryptOpenAlgorithmProvider(&kncg_alg_ecdsa[i], ecdsa[i], NULL, 0);
        BCryptOpenAlgorithmProvider(&kncg_alg_ecdh[i], ecdh[i], NULL, 0);
    }
    return TRUE;
}

static void kncg_init(void) {
    InitOnceExecuteOnce(&kncg_once, kncg_init_cb, NULL, NULL);
}

static int kncg_random(void *p, size_t n) {
    unsigned char *b = (unsigned char *)p;
    while (n > 0) {
        ULONG c = n > 0x40000000U ? 0x40000000U : (ULONG)n;
        if (!BCRYPT_SUCCESS(BCryptGenRandom(NULL, b, c, BCRYPT_USE_SYSTEM_PREFERRED_RNG))) return 0;
        b += c;
        n -= c;
    }
    return 1;
}

/* ── digests by name: CNG hash objects, plus a software SHA-224 ─────────── */

static const struct { const char *name; int md; } kncg_md_names[] = {
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
static const char *kncg_hash_list =
    "RSA-MD5,RSA-SHA1,RSA-SHA1-2,RSA-SHA224,RSA-SHA256,RSA-SHA384,RSA-SHA512,"
    "ecdsa-with-SHA1,md5,md5WithRSAEncryption,sha1,sha1WithRSAEncryption,"
    "sha224,sha224WithRSAEncryption,sha256,sha256WithRSAEncryption,"
    "sha384,sha384WithRSAEncryption,sha512,sha512WithRSAEncryption,ssl3-md5,ssl3-sha1";

static int kncg_md(const char *name) {
    if (!name || !*name) return -1;
    for (size_t i = 0; i < sizeof kncg_md_names / sizeof kncg_md_names[0]; i++)
        if (kncg_casecmp(name, kncg_md_names[i].name) == 0) return kncg_md_names[i].md;
    return -1;
}

static const size_t kncg_md_len[KD_COUNT] = {16, 20, 28, 32, 48, 64};
static const size_t kncg_md_block[KD_COUNT] = {64, 64, 64, 64, 128, 128};

/* SHA-224 = the SHA-256 compression with its own IV, truncated. */
typedef struct {
    uint32_t h[8];
    uint64_t bytes;
    unsigned char buf[64];
    size_t n;
} kncg_sha224;

static const uint32_t kncg_k256[64] = {
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2};

#define KNCG_ROR(x, n) (((x) >> (n)) | ((x) << (32 - (n))))

static void kncg_sha256_block(uint32_t h[8], const unsigned char *p) {
    uint32_t w[64], a, b, c, d, e, f, g, hh;
    for (int i = 0; i < 16; i++)
        w[i] = (uint32_t)p[4 * i] << 24 | (uint32_t)p[4 * i + 1] << 16 | (uint32_t)p[4 * i + 2] << 8 | p[4 * i + 3];
    for (int i = 16; i < 64; i++) {
        uint32_t s0 = KNCG_ROR(w[i - 15], 7) ^ KNCG_ROR(w[i - 15], 18) ^ (w[i - 15] >> 3);
        uint32_t s1 = KNCG_ROR(w[i - 2], 17) ^ KNCG_ROR(w[i - 2], 19) ^ (w[i - 2] >> 10);
        w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }
    a = h[0]; b = h[1]; c = h[2]; d = h[3]; e = h[4]; f = h[5]; g = h[6]; hh = h[7];
    for (int i = 0; i < 64; i++) {
        uint32_t t1 = hh + (KNCG_ROR(e, 6) ^ KNCG_ROR(e, 11) ^ KNCG_ROR(e, 25)) + ((e & f) ^ (~e & g)) + kncg_k256[i] + w[i];
        uint32_t t2 = (KNCG_ROR(a, 2) ^ KNCG_ROR(a, 13) ^ KNCG_ROR(a, 22)) + ((a & b) ^ (a & c) ^ (b & c));
        hh = g; g = f; f = e; e = d + t1; d = c; c = b; b = a; a = t1 + t2;
    }
    h[0] += a; h[1] += b; h[2] += c; h[3] += d; h[4] += e; h[5] += f; h[6] += g; h[7] += hh;
}

static void kncg_sha224_init(kncg_sha224 *s) {
    static const uint32_t iv[8] = {0xc1059ed8, 0x367cd507, 0x3070dd17, 0xf70e5939,
                                   0xffc00b31, 0x68581511, 0x64f98fa7, 0xbefa4fa4};
    memcpy(s->h, iv, sizeof iv);
    s->bytes = 0;
    s->n = 0;
}

static void kncg_sha224_update(kncg_sha224 *s, const unsigned char *p, size_t n) {
    s->bytes += n;
    while (n > 0) {
        size_t take = 64 - s->n;
        if (take > n) take = n;
        memcpy(s->buf + s->n, p, take);
        s->n += take;
        p += take;
        n -= take;
        if (s->n == 64) {
            kncg_sha256_block(s->h, s->buf);
            s->n = 0;
        }
    }
}

static void kncg_sha224_final(kncg_sha224 *s, unsigned char *out) {
    uint64_t bits = s->bytes * 8;
    unsigned char pad = 0x80, zero = 0, len[8];
    kncg_sha224_update(s, &pad, 1);
    while (s->n != 56) kncg_sha224_update(s, &zero, 1);
    for (int i = 0; i < 8; i++) len[i] = (unsigned char)(bits >> (56 - 8 * i));
    kncg_sha224_update(s, len, 8);
    for (int i = 0; i < 7; i++) {
        out[4 * i] = (unsigned char)(s->h[i] >> 24); out[4 * i + 1] = (unsigned char)(s->h[i] >> 16);
        out[4 * i + 2] = (unsigned char)(s->h[i] >> 8); out[4 * i + 3] = (unsigned char)s->h[i];
    }
}

typedef struct {
    int md;
    BCRYPT_HASH_HANDLE h; /* every digest but SHA-224 */
    kncg_sha224 s;
} kncg_md_ctx;

static int kncg_md_init(kncg_md_ctx *c, int md) {
    kncg_init();
    memset(c, 0, sizeof *c);
    c->md = md;
    if (md == KD_SHA224) {
        kncg_sha224_init(&c->s);
        return 1;
    }
    return BCRYPT_SUCCESS(BCryptCreateHash(kncg_alg_md[md], &c->h, NULL, 0, NULL, 0, 0));
}

static void kncg_md_update(kncg_md_ctx *c, const void *data, long long len) {
    const unsigned char *p = (const unsigned char *)data;
    while (len > 0) {
        ULONG n = len > 0x40000000LL ? 0x40000000U : (ULONG)len;
        if (c->md == KD_SHA224) kncg_sha224_update(&c->s, p, n);
        else if (c->h) BCryptHashData(c->h, (PUCHAR)p, n, 0);
        p += n;
        len -= n;
    }
}

/* The digest into out; the context is spent. */
static void kncg_md_final(kncg_md_ctx *c, unsigned char *out) {
    if (c->md == KD_SHA224) {
        kncg_sha224_final(&c->s, out);
        return;
    }
    if (c->h) {
        BCryptFinishHash(c->h, out, (ULONG)kncg_md_len[c->md], 0);
        BCryptDestroyHash(c->h);
        c->h = NULL;
    }
}

static void kncg_md_free(kncg_md_ctx *c) {
    if (c->h) BCryptDestroyHash(c->h);
    c->h = NULL;
}

static int kncg_md_dup(const kncg_md_ctx *from, kncg_md_ctx *to) {
    *to = *from;
    to->h = NULL;
    if (from->md == KD_SHA224) return 1;
    return from->h && BCRYPT_SUCCESS(BCryptDuplicateHash(from->h, &to->h, NULL, 0, 0));
}

static void kncg_md_oneshot(int md, const void *data, long long len, unsigned char *out) {
    kncg_md_ctx c;
    kncg_md_init(&c, md);
    kncg_md_update(&c, data, len);
    kncg_md_final(&c, out);
}

/* HMAC (RFC 2104) over the digests: inner and outer contexts keyed up front,
 * so a copy or a PBKDF2 round is two context duplicates. */
typedef struct {
    int md;
    kncg_md_ctx in, out;
} kncg_hmac;

static int kncg_hmac_init(kncg_hmac *h, int md, const unsigned char *key, size_t keylen) {
    unsigned char k0[128], pad[128], kh[64];
    size_t bl = kncg_md_block[md];
    memset(k0, 0, sizeof k0);
    if (keylen > bl) {
        kncg_md_oneshot(md, key, (long long)keylen, kh);
        memcpy(k0, kh, kncg_md_len[md]);
    } else if (keylen) {
        memcpy(k0, key, keylen);
    }
    h->md = md;
    if (!kncg_md_init(&h->in, md)) return 0;
    if (!kncg_md_init(&h->out, md)) {
        kncg_md_free(&h->in);
        return 0;
    }
    for (size_t i = 0; i < bl; i++) pad[i] = k0[i] ^ 0x36;
    kncg_md_update(&h->in, pad, (long long)bl);
    for (size_t i = 0; i < bl; i++) pad[i] = k0[i] ^ 0x5c;
    kncg_md_update(&h->out, pad, (long long)bl);
    memset(k0, 0, sizeof k0);
    memset(pad, 0, sizeof pad);
    return 1;
}

static void kncg_hmac_final(kncg_hmac *h, unsigned char *out) {
    unsigned char t[64];
    kncg_md_final(&h->in, t);
    kncg_md_update(&h->out, t, (long long)kncg_md_len[h->md]);
    kncg_md_final(&h->out, out);
}

static void kncg_hmac_free(kncg_hmac *h) {
    kncg_md_free(&h->in);
    kncg_md_free(&h->out);
}

static int kncg_hmac_dup(const kncg_hmac *from, kncg_hmac *to) {
    to->md = from->md;
    if (!kncg_md_dup(&from->in, &to->in)) return 0;
    if (!kncg_md_dup(&from->out, &to->out)) {
        kncg_md_free(&to->in);
        return 0;
    }
    return 1;
}

static void kncg_hmac_oneshot(int md, const unsigned char *key, size_t keylen, const unsigned char *data, size_t n,
                              unsigned char *out) {
    kncg_hmac h;
    kncg_hmac_init(&h, md, key, keylen);
    kncg_md_update(&h.in, data, (long long)n);
    kncg_hmac_final(&h, out);
}

/* ── the mini-DER layer (as in the CommonCrypto backend) ───────────────── */

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
    if (n) memcpy(d->buf + d->len, p, n);
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

/* tag + length + inner's bytes; inner is freed. */
static int kncg_der_wrap(kml_der *d, unsigned char tag, kml_der *inner) {
    int ok = kml_der_hdr(d, tag, inner->len) && kml_der_put(d, inner->buf, inner->len);
    free(inner->buf);
    memset(inner, 0, sizeof *inner);
    return ok;
}

/* An unsigned INTEGER (big-endian magnitude, leading zeros stripped). */
static int kncg_der_uint(kml_der *d, const unsigned char *p, size_t n) {
    static const unsigned char zero = 0;
    while (n > 0 && p[0] == 0) { p++; n--; }
    if (n == 0) return kml_der_hdr(d, 0x02, 1) && kml_der_put(d, &zero, 1);
    if (p[0] & 0x80) return kml_der_hdr(d, 0x02, n + 1) && kml_der_put(d, &zero, 1) && kml_der_put(d, p, n);
    return kml_der_hdr(d, 0x02, n) && kml_der_put(d, p, n);
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
    if (len > (size_t)(end - *p)) return 0;
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
static const unsigned char kncg_oid_ed25519[] = {0x06, 0x03, 0x2b, 0x65, 0x70};
static const unsigned char kncg_oid_x25519[] = {0x06, 0x03, 0x2b, 0x65, 0x6e};

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

static int kncg_der_eq(const unsigned char *c, size_t clen, const unsigned char *oid, size_t oidlen) {
    /* c/clen: an OID's content; oid: tag+len+content. */
    return clen == oidlen - 2 && memcmp(c, oid + 2, clen) == 0;
}

static ll kncg_curve_from_oid(const unsigned char *c, size_t clen) {
    for (ll id = 1; id <= 3; id++) {
        size_t n;
        const unsigned char *o = kml_curve_oid(id, &n);
        if (kncg_der_eq(c, clen, o, n)) return id;
    }
    return 0;
}

/* ── P-256/P-384/P-521 field arithmetic: just enough for point
 * decompression (CNG takes only uncompressed points) and the private-key
 * range check. Little-endian 32-bit limbs; bit-serial modular multiply. ── */

#define KNCG_BN 17 /* 544 bits */
typedef uint32_t kncg_bn[KNCG_BN];

static void kncg_bn_from(kncg_bn r, const unsigned char *be, size_t n) {
    memset(r, 0, sizeof(kncg_bn));
    for (size_t i = 0; i < n && i < 4 * KNCG_BN; i++)
        r[i / 4] |= (uint32_t)be[n - 1 - i] << (8 * (i % 4));
}

static void kncg_bn_to(unsigned char *be, size_t n, const kncg_bn a) {
    for (size_t i = 0; i < n; i++) be[n - 1 - i] = i < 4 * KNCG_BN ? (unsigned char)(a[i / 4] >> (8 * (i % 4))) : 0;
}

static int kncg_bn_cmp(const kncg_bn a, const kncg_bn b) {
    for (int i = KNCG_BN - 1; i >= 0; i--)
        if (a[i] != b[i]) return a[i] < b[i] ? -1 : 1;
    return 0;
}

static int kncg_bn_zero(const kncg_bn a) {
    for (int i = 0; i < KNCG_BN; i++)
        if (a[i]) return 0;
    return 1;
}

static void kncg_bn_add(kncg_bn r, const kncg_bn a, const kncg_bn b) {
    uint64_t c = 0;
    for (int i = 0; i < KNCG_BN; i++) {
        c += (uint64_t)a[i] + b[i];
        r[i] = (uint32_t)c;
        c >>= 32;
    }
}

static void kncg_bn_sub(kncg_bn r, const kncg_bn a, const kncg_bn b) {
    int64_t c = 0;
    for (int i = 0; i < KNCG_BN; i++) {
        c += (int64_t)a[i] - b[i];
        r[i] = (uint32_t)c;
        c >>= 32;
    }
}

static void kncg_bn_modadd(kncg_bn r, const kncg_bn a, const kncg_bn b, const kncg_bn p) {
    kncg_bn_add(r, a, b);
    if (kncg_bn_cmp(r, p) >= 0) kncg_bn_sub(r, r, p);
}

static void kncg_bn_modsub(kncg_bn r, const kncg_bn a, const kncg_bn b, const kncg_bn p) {
    if (kncg_bn_cmp(a, b) >= 0) {
        kncg_bn_sub(r, a, b);
    } else {
        kncg_bn t;
        kncg_bn_add(t, a, p);
        kncg_bn_sub(r, t, b);
    }
}

static void kncg_bn_modmul(kncg_bn r, const kncg_bn a, const kncg_bn b, const kncg_bn p) {
    kncg_bn acc;
    memset(acc, 0, sizeof acc);
    for (int i = 32 * KNCG_BN - 1; i >= 0; i--) {
        kncg_bn_modadd(acc, acc, acc, p);
        if ((b[i / 32] >> (i % 32)) & 1) kncg_bn_modadd(acc, acc, a, p);
    }
    memcpy(r, acc, sizeof acc);
}

static void kncg_bn_modexp(kncg_bn r, const kncg_bn a, const kncg_bn e, const kncg_bn p) {
    kncg_bn acc;
    memset(acc, 0, sizeof acc);
    acc[0] = 1;
    for (int i = 32 * KNCG_BN - 1; i >= 0; i--) {
        kncg_bn_modmul(acc, acc, acc, p);
        if ((e[i / 32] >> (i % 32)) & 1) kncg_bn_modmul(acc, acc, a, p);
    }
    memcpy(r, acc, sizeof acc);
}

static void kncg_bn_hex(kncg_bn r, const char *hex) {
    unsigned char b[68];
    size_t n = strlen(hex) / 2;
    for (size_t i = 0; i < n; i++) {
        unsigned v = 0;
        sscanf(hex + 2 * i, "%2x", &v);
        b[i] = (unsigned char)v;
    }
    kncg_bn_from(r, b, n);
}

/* The curve's p, b and group order n (y^2 = x^3 - 3x + b over GF(p)). */
static const char *const kncg_curve_p[4] = {
    NULL, "ffffffff00000001000000000000000000000000ffffffffffffffffffffffff",
    "fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffeffffffff0000000000000000ffffffff",
    "01ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"};
static const char *const kncg_curve_b[4] = {
    NULL, "5ac635d8aa3a93e7b3ebbd55769886bc651d06b0cc53b0f63bce3c3e27d2604b",
    "b3312fa7e23ee7e4988e056be3f82d19181d9c6efe8141120314088f5013875ac656398d8a2ed19d2a85c8edd3ec2aef",
    "0051953eb9618e1c9a1f929a21a0b68540eea2da725b99b315f3b8b489918ef109e156193951ec7e937b1652c0bd3bb1bf073573df883d2c34f1ef451fd46b503f00"};
static const char *const kncg_curve_n[4] = {
    NULL, "ffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551",
    "ffffffffffffffffffffffffffffffffffffffffffffffffc7634d81f4372ddf581a0db248b0a77aecec196accc52973",
    "01fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffa51868783bf2f966b7fcc0148f709a5d03bb5c9b8899c47aebb6fb71e91386409"};

/* y for x with the given parity, or 0 when x is on no point of the curve. */
static int kncg_ec_decompress(ll curve, const unsigned char *xb, int odd, unsigned char *yb) {
    kncg_bn p, b, x, rhs, t, e, y, one, three;
    size_t cb = (size_t)kml_curve_bytes(curve);
    kncg_bn_hex(p, kncg_curve_p[curve]);
    kncg_bn_hex(b, kncg_curve_b[curve]);
    kncg_bn_from(x, xb, cb);
    if (kncg_bn_cmp(x, p) >= 0) return 0;
    memset(three, 0, sizeof three);
    three[0] = 3;
    kncg_bn_modmul(t, x, x, p);
    kncg_bn_modmul(rhs, t, x, p);       /* x^3 */
    kncg_bn_modmul(t, three, x, p);
    kncg_bn_modsub(rhs, rhs, t, p);     /* - 3x */
    kncg_bn_modadd(rhs, rhs, b, p);     /* + b */
    /* p = 3 (mod 4) on all three curves: y = rhs^((p+1)/4). */
    memset(one, 0, sizeof one);
    one[0] = 1;
    kncg_bn_add(e, p, one);
    for (int i = 0; i < KNCG_BN; i++) e[i] = e[i] >> 2 | (i + 1 < KNCG_BN ? e[i + 1] << 30 : 0);
    kncg_bn_modexp(y, rhs, e, p);
    kncg_bn_modmul(t, y, y, p);
    if (kncg_bn_cmp(t, rhs) != 0) return 0;
    if ((int)(y[0] & 1) != odd) {
        if (kncg_bn_zero(y)) return 0;
        kncg_bn_sub(y, p, y);
    }
    kncg_bn_to(yb, cb, y);
    return 1;
}

/* 1 when 0 < k < n for the curve's order n; k is cb big-endian bytes. */
static int kncg_ec_priv_in_range(ll curve, const unsigned char *k) {
    kncg_bn n, v;
    kncg_bn_hex(n, kncg_curve_n[curve]);
    kncg_bn_from(v, k, (size_t)kml_curve_bytes(curve));
    return !kncg_bn_zero(v) && kncg_bn_cmp(v, n) < 0;
}

/* ── asymmetric keys: components, CNG blobs, DER ───────────────────────── */

enum { KK_RSA = 0, KK_EC, KK_ED25519, KK_X25519 };
enum { KR_N = 0, KR_E, KR_D, KR_P, KR_Q, KR_DP, KR_DQ, KR_QI };

typedef struct {
    int type, priv;
    ll curve;                /* EC: 1/2/3 */
    unsigned char *rsa[8];   /* RSA: n e d p q dp dq qi, big-endian, unpadded */
    size_t rsalen[8];
    unsigned char x[66], y[66], d[66]; /* EC: each curve-size bytes */
    unsigned char raw[32];   /* Ed25519/X25519: the seed (private) or public key */
} kncg_key;

static void kncg_key_free(kncg_key *k) {
    for (int i = 0; i < 8; i++) {
        if (k->rsa[i]) {
            memset(k->rsa[i], 0, k->rsalen[i]);
            free(k->rsa[i]);
        }
    }
    memset(k, 0, sizeof *k);
}

static void kncg_rsa_set(kncg_key *k, int i, const unsigned char *p, size_t n) {
    while (n > 0 && p[0] == 0) { p++; n--; }
    free(k->rsa[i]);
    k->rsa[i] = (unsigned char *)malloc(n ? n : 1);
    if (n) memcpy(k->rsa[i], p, n);
    k->rsalen[i] = n;
}

static int kncg_rsa_bits(const kncg_key *k) {
    const unsigned char *n = k->rsa[KR_N];
    size_t l = k->rsalen[KR_N];
    if (!n || !l) return 0;
    int bits = (int)(l * 8);
    for (unsigned char top = n[0]; !(top & 0x80); top <<= 1) bits--;
    return bits;
}

/* A CNG handle for the key: public (n, e) or private (n, e, p, q). */
static BCRYPT_KEY_HANDLE kncg_rsa_handle(const kncg_key *k, int priv) {
    BCRYPT_KEY_HANDLE h = NULL;
    size_t nl = k->rsalen[KR_N], el = k->rsalen[KR_E], pl = k->rsalen[KR_P], ql = k->rsalen[KR_Q];
    if (k->type != KK_RSA || !nl || !el) return NULL;
    if (priv && (!k->priv || !pl || !ql)) return NULL;
    size_t total = sizeof(BCRYPT_RSAKEY_BLOB) + el + nl + (priv ? pl + ql : 0);
    unsigned char *blob = (unsigned char *)calloc(1, total);
    if (!blob) return NULL;
    BCRYPT_RSAKEY_BLOB *hd = (BCRYPT_RSAKEY_BLOB *)blob;
    hd->Magic = priv ? BCRYPT_RSAPRIVATE_MAGIC : BCRYPT_RSAPUBLIC_MAGIC;
    hd->BitLength = (ULONG)kncg_rsa_bits(k);
    hd->cbPublicExp = (ULONG)el;
    hd->cbModulus = (ULONG)nl;
    hd->cbPrime1 = priv ? (ULONG)pl : 0;
    hd->cbPrime2 = priv ? (ULONG)ql : 0;
    unsigned char *p = blob + sizeof *hd;
    memcpy(p, k->rsa[KR_E], el); p += el;
    memcpy(p, k->rsa[KR_N], nl); p += nl;
    if (priv) {
        memcpy(p, k->rsa[KR_P], pl); p += pl;
        memcpy(p, k->rsa[KR_Q], ql);
    }
    kncg_init();
    if (!BCRYPT_SUCCESS(BCryptImportKeyPair(kncg_alg_rsa, NULL, priv ? BCRYPT_RSAPRIVATE_BLOB : BCRYPT_RSAPUBLIC_BLOB,
                                            &h, blob, (ULONG)total, 0)))
        h = NULL;
    memset(blob, 0, total);
    free(blob);
    return h;
}

/* A CNG RSAFULLPRIVATE blob → the key's components. */
static int kncg_rsa_from_full(const unsigned char *blob, ULONG n, kncg_key *k) {
    const BCRYPT_RSAKEY_BLOB *hd = (const BCRYPT_RSAKEY_BLOB *)blob;
    if (n < sizeof *hd) return 0;
    size_t el = hd->cbPublicExp, ml = hd->cbModulus, p1 = hd->cbPrime1, p2 = hd->cbPrime2;
    if (sizeof *hd + el + 2 * ml + 3 * p1 + 2 * p2 > n) return 0;
    const unsigned char *p = blob + sizeof *hd;
    k->type = KK_RSA;
    k->priv = 1;
    kncg_rsa_set(k, KR_E, p, el); p += el;
    kncg_rsa_set(k, KR_N, p, ml); p += ml;
    kncg_rsa_set(k, KR_P, p, p1); p += p1;
    kncg_rsa_set(k, KR_Q, p, p2); p += p2;
    kncg_rsa_set(k, KR_DP, p, p1); p += p1;
    kncg_rsa_set(k, KR_DQ, p, p2); p += p2;
    kncg_rsa_set(k, KR_QI, p, p1); p += p1;
    kncg_rsa_set(k, KR_D, p, ml);
    return 1;
}

static const ULONG kncg_ec_magic[2][2][4] = {
    /* [ecdh][priv][curve] */
    {{0, BCRYPT_ECDSA_PUBLIC_P256_MAGIC, BCRYPT_ECDSA_PUBLIC_P384_MAGIC, BCRYPT_ECDSA_PUBLIC_P521_MAGIC},
     {0, BCRYPT_ECDSA_PRIVATE_P256_MAGIC, BCRYPT_ECDSA_PRIVATE_P384_MAGIC, BCRYPT_ECDSA_PRIVATE_P521_MAGIC}},
    {{0, BCRYPT_ECDH_PUBLIC_P256_MAGIC, BCRYPT_ECDH_PUBLIC_P384_MAGIC, BCRYPT_ECDH_PUBLIC_P521_MAGIC},
     {0, BCRYPT_ECDH_PRIVATE_P256_MAGIC, BCRYPT_ECDH_PRIVATE_P384_MAGIC, BCRYPT_ECDH_PRIVATE_P521_MAGIC}},
};

/* A CNG handle for an EC key (ECDSA or ECDH); CNG checks the point is on
 * the curve, so this is also the key's validation. X/Y all-zero with a
 * private d makes CNG derive the public point. */
static BCRYPT_KEY_HANDLE kncg_ec_raw_handle(ll curve, const unsigned char *x, const unsigned char *y,
                                            const unsigned char *d, int ecdh) {
    BCRYPT_KEY_HANDLE h = NULL;
    size_t cb = (size_t)kml_curve_bytes(curve);
    unsigned char blob[sizeof(BCRYPT_ECCKEY_BLOB) + 3 * 66];
    BCRYPT_ECCKEY_BLOB *hd = (BCRYPT_ECCKEY_BLOB *)blob;
    if (!cb) return NULL;
    hd->dwMagic = kncg_ec_magic[ecdh][d != NULL][curve];
    hd->cbKey = (ULONG)cb;
    unsigned char *p = blob + sizeof *hd;
    if (x) memcpy(p, x, cb); else memset(p, 0, cb);
    if (y) memcpy(p + cb, y, cb); else memset(p + cb, 0, cb);
    if (d) memcpy(p + 2 * cb, d, cb);
    kncg_init();
    if (!BCRYPT_SUCCESS(BCryptImportKeyPair(ecdh ? kncg_alg_ecdh[curve] : kncg_alg_ecdsa[curve], NULL,
                                            d ? BCRYPT_ECCPRIVATE_BLOB : BCRYPT_ECCPUBLIC_BLOB, &h, blob,
                                            (ULONG)(sizeof *hd + (d ? 3 : 2) * cb), 0)))
        h = NULL;
    memset(blob, 0, sizeof blob);
    return h;
}

static BCRYPT_KEY_HANDLE kncg_ec_handle(const kncg_key *k, int priv, int ecdh) {
    if (k->type != KK_EC || (priv && !k->priv)) return NULL;
    return kncg_ec_raw_handle(k->curve, k->x, k->y, priv ? k->d : NULL, ecdh);
}

/* The public point of private scalar d (cb bytes) into x/y, via CNG. */
static int kncg_ec_pub_from_d(ll curve, const unsigned char *d, unsigned char *x, unsigned char *y) {
    BCRYPT_KEY_HANDLE h;
    unsigned char blob[sizeof(BCRYPT_ECCKEY_BLOB) + 2 * 66];
    ULONG n = 0;
    size_t cb = (size_t)kml_curve_bytes(curve);
    if (!cb || !kncg_ec_priv_in_range(curve, d)) return 0;
    h = kncg_ec_raw_handle(curve, NULL, NULL, d, 1);
    if (!h) return 0;
    int ok = BCRYPT_SUCCESS(BCryptExportKey(h, NULL, BCRYPT_ECCPUBLIC_BLOB, blob, sizeof blob, &n, 0)) &&
             n == sizeof(BCRYPT_ECCKEY_BLOB) + 2 * cb;
    if (ok) {
        memcpy(x, blob + sizeof(BCRYPT_ECCKEY_BLOB), cb);
        memcpy(y, blob + sizeof(BCRYPT_ECCKEY_BLOB) + cb, cb);
    }
    BCryptDestroyKey(h);
    return ok;
}

/* An encoded point (04||X||Y, 02/03||X, 06/07||X||Y) → x/y; 0 when it is
 * malformed or on no point of the curve. */
static int kncg_ec_point(ll curve, const unsigned char *pt, size_t n, unsigned char *x, unsigned char *y) {
    size_t cb = (size_t)kml_curve_bytes(curve);
    if (!cb || n == 0) return 0;
    if ((pt[0] == 4 || pt[0] == 6 || pt[0] == 7) && n == 1 + 2 * cb) {
        if (pt[0] != 4 && (pt[2 * cb] & 1) != (pt[0] & 1)) return 0;
        memcpy(x, pt + 1, cb);
        memcpy(y, pt + 1 + cb, cb);
    } else if ((pt[0] == 2 || pt[0] == 3) && n == 1 + cb) {
        memcpy(x, pt + 1, cb);
        if (!kncg_ec_decompress(curve, x, pt[0] & 1, y)) return 0;
    } else {
        return 0;
    }
    BCRYPT_KEY_HANDLE h = kncg_ec_raw_handle(curve, x, y, NULL, 0);
    if (!h) return 0;
    BCryptDestroyKey(h);
    return 1;
}

/* x/y in OpenSSL's point formats: 2 compressed, 4 uncompressed, 6 hybrid. */
static size_t kncg_ec_encode(ll curve, const unsigned char *x, const unsigned char *y, int format, unsigned char *out) {
    size_t cb = (size_t)kml_curve_bytes(curve);
    int odd = y[cb - 1] & 1;
    if (format == 2) {
        out[0] = (unsigned char)(2 + odd);
        memcpy(out + 1, x, cb);
        return 1 + cb;
    }
    out[0] = format == 6 ? (unsigned char)(6 + odd) : 4;
    memcpy(out + 1, x, cb);
    memcpy(out + 1 + cb, y, cb);
    return 1 + 2 * cb;
}

/* -- DER encoders -- */

/* RSA PKCS#1: RSAPrivateKey (all eight components) or RSAPublicKey. */
static int kncg_rsa_pkcs1(const kncg_key *k, int priv, kml_der *out) {
    kml_der in = {0};
    static const unsigned char zero = 0;
    int ok = 1;
    if (priv) {
        ok = kncg_der_uint(&in, &zero, 1);
        for (int i = 0; i < 8 && ok; i++) ok = kncg_der_uint(&in, k->rsa[i] ? k->rsa[i] : &zero, k->rsa[i] ? k->rsalen[i] : 1);
    } else {
        ok = kncg_der_uint(&in, k->rsa[KR_N], k->rsalen[KR_N]) && kncg_der_uint(&in, k->rsa[KR_E], k->rsalen[KR_E]);
    }
    if (!ok) { free(in.buf); return 0; }
    return kncg_der_wrap(out, 0x30, &in);
}

/* AlgorithmIdentifier for the key. */
static int kncg_algid(const kncg_key *k, kml_der *out) {
    kml_der in = {0};
    static const unsigned char derNull[] = {0x05, 0x00};
    int ok;
    if (k->type == KK_RSA) {
        ok = kml_der_put(&in, kml_oid_rsa, sizeof kml_oid_rsa) && kml_der_put(&in, derNull, 2);
    } else if (k->type == KK_EC) {
        size_t on;
        const unsigned char *oid = kml_curve_oid(k->curve, &on);
        ok = kml_der_put(&in, kml_oid_ec, sizeof kml_oid_ec) && kml_der_put(&in, oid, on);
    } else {
        ok = kml_der_put(&in, k->type == KK_ED25519 ? kncg_oid_ed25519 : kncg_oid_x25519, 5);
    }
    if (!ok) { free(in.buf); return 0; }
    return kncg_der_wrap(out, 0x30, &in);
}

/* SEC1 ECPrivateKey; withParams adds the [0] curve OID (the standalone
 * "EC PRIVATE KEY" form — inside PKCS#8 the OID is in the AlgorithmIdentifier). */
static int kncg_ec_sec1(const kncg_key *k, int withParams, kml_der *out) {
    kml_der in = {0}, ctx = {0}, bit = {0};
    static const unsigned char ver1[] = {0x02, 0x01, 0x01}, zero = 0;
    size_t cb = (size_t)kml_curve_bytes(k->curve), on;
    unsigned char pt[1 + 2 * 66];
    const unsigned char *oid = kml_curve_oid(k->curve, &on);
    int ok = kml_der_put(&in, ver1, 3) && kml_der_hdr(&in, 0x04, cb) && kml_der_put(&in, k->d, cb);
    if (ok && withParams) {
        ok = kml_der_put(&ctx, oid, on) && kncg_der_wrap(&in, 0xa0, &ctx);
    }
    kncg_ec_encode(k->curve, k->x, k->y, 4, pt);
    ok = ok && kml_der_put(&bit, &zero, 1) && kml_der_put(&bit, pt, 1 + 2 * cb) && kncg_der_wrap(&ctx, 0x03, &bit) &&
         kncg_der_wrap(&in, 0xa1, &ctx);
    free(ctx.buf);
    free(bit.buf);
    if (!ok) { free(in.buf); return 0; }
    return kncg_der_wrap(out, 0x30, &in);
}

/* SubjectPublicKeyInfo. */
static int kncg_spki(const kncg_key *k, kml_der *out) {
    kml_der in = {0}, bit = {0};
    static const unsigned char zero = 0;
    int ok = kncg_algid(k, &in) && kml_der_put(&bit, &zero, 1);
    if (ok) {
        if (k->type == KK_RSA) {
            ok = kncg_rsa_pkcs1(k, 0, &bit);
        } else if (k->type == KK_EC) {
            unsigned char pt[1 + 2 * 66];
            size_t n = kncg_ec_encode(k->curve, k->x, k->y, 4, pt);
            ok = kml_der_put(&bit, pt, n);
        } else {
            ok = kml_der_put(&bit, k->raw, 32);
        }
    }
    ok = ok && kncg_der_wrap(&in, 0x03, &bit);
    free(bit.buf);
    if (!ok) { free(in.buf); return 0; }
    return kncg_der_wrap(out, 0x30, &in);
}

/* PrivateKeyInfo (PKCS#8). */
static int kncg_pkcs8(const kncg_key *k, kml_der *out) {
    kml_der in = {0}, body = {0};
    static const unsigned char ver0[] = {0x02, 0x01, 0x00};
    int ok = kml_der_put(&in, ver0, 3) && kncg_algid(k, &in);
    if (ok) {
        if (k->type == KK_RSA) {
            ok = kncg_rsa_pkcs1(k, 1, &body);
        } else if (k->type == KK_EC) {
            ok = kncg_ec_sec1(k, 0, &body);
        } else {
            ok = kml_der_hdr(&body, 0x04, 32) && kml_der_put(&body, k->raw, 32);
        }
    }
    ok = ok && kncg_der_wrap(&in, 0x04, &body);
    free(body.buf);
    if (!ok) { free(in.buf); return 0; }
    return kncg_der_wrap(out, 0x30, &in);
}

/* ── Curve25519 / Ed25519 (compact field arithmetic, radix 2^16; CNG has
 * X25519 but no Ed25519, so both use this — as the CommonCrypto backend). ── */

typedef int64_t kncg_gf[16];
static const kncg_gf kncg_gf0 = {0}, kncg_gf1 = {1}, kncg_121665 = {0xDB41, 1};
static const kncg_gf kncg_D = {0x78a3, 0x1359, 0x4dca, 0x75eb, 0xd8ab, 0x4141, 0x0a4d, 0x0070,
                               0xe898, 0x7779, 0x4079, 0x8cc7, 0xfe73, 0x2b6f, 0x6cee, 0x5203};
static const kncg_gf kncg_D2 = {0xf159, 0x26b2, 0x9b94, 0xebd6, 0xb156, 0x8283, 0x149a, 0x00e0,
                                0xd130, 0xeef3, 0x80f2, 0x198e, 0xfce7, 0x56df, 0xd9dc, 0x2406};
static const kncg_gf kncg_X = {0xd51a, 0x8f25, 0x2d60, 0xc956, 0xa7b2, 0x9525, 0xc760, 0x692c,
                               0xdc5c, 0xfdd6, 0xe231, 0xc0a4, 0x53fe, 0xcd6e, 0x36d3, 0x2169};
static const kncg_gf kncg_Y = {0x6658, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666,
                               0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666, 0x6666};
static const kncg_gf kncg_I = {0xa0b0, 0x4a0e, 0x1b27, 0xc4ee, 0xe478, 0xad2f, 0x1806, 0x2f43,
                               0xd7a7, 0x3dfb, 0x0099, 0x2b4d, 0xdf0b, 0x4fc1, 0x2480, 0x2b83};

static void kncg_set(kncg_gf r, const kncg_gf a) { for (int i = 0; i < 16; i++) r[i] = a[i]; }

static void kncg_car(kncg_gf o) {
    for (int i = 0; i < 16; i++) {
        o[i] += (1LL << 16);
        int64_t c = o[i] >> 16;
        o[(i + 1) * (i < 15)] += c - 1 + 37 * (c - 1) * (i == 15);
        o[i] -= c * 65536;
    }
}

static void kncg_sel(kncg_gf p, kncg_gf q, int b) {
    int64_t t, c = ~(int64_t)(b - 1);
    for (int i = 0; i < 16; i++) {
        t = c & (p[i] ^ q[i]);
        p[i] ^= t;
        q[i] ^= t;
    }
}

static void kncg_pack(unsigned char *o, const kncg_gf n) {
    kncg_gf m, t;
    int b;
    for (int i = 0; i < 16; i++) t[i] = n[i];
    kncg_car(t); kncg_car(t); kncg_car(t);
    for (int j = 0; j < 2; j++) {
        m[0] = t[0] - 0xffed;
        for (int i = 1; i < 15; i++) {
            m[i] = t[i] - 0xffff - ((m[i - 1] >> 16) & 1);
            m[i - 1] &= 0xffff;
        }
        m[15] = t[15] - 0x7fff - ((m[14] >> 16) & 1);
        b = (int)((m[15] >> 16) & 1);
        m[14] &= 0xffff;
        kncg_sel(t, m, 1 - b);
    }
    for (int i = 0; i < 16; i++) {
        o[2 * i] = (unsigned char)(t[i] & 0xff);
        o[2 * i + 1] = (unsigned char)(t[i] >> 8);
    }
}

static int kncg_neq(const kncg_gf a, const kncg_gf b) {
    unsigned char c[32], d[32];
    kncg_pack(c, a);
    kncg_pack(d, b);
    return memcmp(c, d, 32) != 0;
}

static int kncg_par(const kncg_gf a) {
    unsigned char d[32];
    kncg_pack(d, a);
    return d[0] & 1;
}

static void kncg_unpack(kncg_gf o, const unsigned char *n) {
    for (int i = 0; i < 16; i++) o[i] = n[2 * i] + ((int64_t)n[2 * i + 1] << 8);
    o[15] &= 0x7fff;
}

static void kncg_A(kncg_gf o, const kncg_gf a, const kncg_gf b) { for (int i = 0; i < 16; i++) o[i] = a[i] + b[i]; }
static void kncg_Z(kncg_gf o, const kncg_gf a, const kncg_gf b) { for (int i = 0; i < 16; i++) o[i] = a[i] - b[i]; }

static void kncg_M(kncg_gf o, const kncg_gf a, const kncg_gf b) {
    int64_t t[31];
    for (int i = 0; i < 31; i++) t[i] = 0;
    for (int i = 0; i < 16; i++)
        for (int j = 0; j < 16; j++) t[i + j] += a[i] * b[j];
    for (int i = 0; i < 15; i++) t[i] += 38 * t[i + 16];
    for (int i = 0; i < 16; i++) o[i] = t[i];
    kncg_car(o);
    kncg_car(o);
}

static void kncg_S(kncg_gf o, const kncg_gf a) { kncg_M(o, a, a); }

static void kncg_inv(kncg_gf o, const kncg_gf i) {
    kncg_gf c;
    kncg_set(c, i);
    for (int a = 253; a >= 0; a--) {
        kncg_S(c, c);
        if (a != 2 && a != 4) kncg_M(c, c, i);
    }
    kncg_set(o, c);
}

static void kncg_pow2523(kncg_gf o, const kncg_gf i) {
    kncg_gf c;
    kncg_set(c, i);
    for (int a = 250; a >= 0; a--) {
        kncg_S(c, c);
        if (a != 1) kncg_M(c, c, i);
    }
    kncg_set(o, c);
}

/* X25519(n, p). */
static void kncg_x25519(unsigned char *q, const unsigned char *n, const unsigned char *p) {
    unsigned char z[32];
    int64_t r;
    kncg_gf a, b, c, d, e, f, x;
    for (int i = 0; i < 31; i++) z[i] = n[i];
    z[31] = (unsigned char)((n[31] & 127) | 64);
    z[0] &= 248;
    kncg_unpack(x, p);
    for (int i = 0; i < 16; i++) {
        b[i] = x[i];
        d[i] = a[i] = c[i] = 0;
    }
    a[0] = d[0] = 1;
    for (int i = 254; i >= 0; --i) {
        r = (z[i >> 3] >> (i & 7)) & 1;
        kncg_sel(a, b, (int)r); kncg_sel(c, d, (int)r);
        kncg_A(e, a, c); kncg_Z(a, a, c); kncg_A(c, b, d); kncg_Z(b, b, d);
        kncg_S(d, e); kncg_S(f, a); kncg_M(a, c, a); kncg_M(c, b, e);
        kncg_A(e, a, c); kncg_Z(a, a, c); kncg_S(b, a); kncg_Z(c, d, f);
        kncg_M(a, c, kncg_121665); kncg_A(a, a, d); kncg_M(c, c, a); kncg_M(a, d, f); kncg_M(d, b, x); kncg_S(b, e);
        kncg_sel(a, b, (int)r); kncg_sel(c, d, (int)r);
    }
    kncg_inv(c, c);
    kncg_M(a, a, c);
    kncg_pack(q, a);
}

static void kncg_padd(kncg_gf p[4], kncg_gf q[4]) {
    kncg_gf a, b, c, d, t, e, f, g, h;
    kncg_Z(a, p[1], p[0]); kncg_Z(t, q[1], q[0]); kncg_M(a, a, t);
    kncg_A(b, p[0], p[1]); kncg_A(t, q[0], q[1]); kncg_M(b, b, t);
    kncg_M(c, p[3], q[3]); kncg_M(c, c, kncg_D2);
    kncg_M(d, p[2], q[2]); kncg_A(d, d, d);
    kncg_Z(e, b, a); kncg_Z(f, d, c); kncg_A(g, d, c); kncg_A(h, b, a);
    kncg_M(p[0], e, f); kncg_M(p[1], h, g); kncg_M(p[2], g, f); kncg_M(p[3], e, h);
}

static void kncg_cswap(kncg_gf p[4], kncg_gf q[4], int b) {
    for (int i = 0; i < 4; i++) kncg_sel(p[i], q[i], b);
}

static void kncg_ppack(unsigned char *r, kncg_gf p[4]) {
    kncg_gf tx, ty, zi;
    kncg_inv(zi, p[2]);
    kncg_M(tx, p[0], zi);
    kncg_M(ty, p[1], zi);
    kncg_pack(r, ty);
    r[31] ^= (unsigned char)(kncg_par(tx) << 7);
}

static void kncg_scalarmult(kncg_gf p[4], kncg_gf q[4], const unsigned char *s) {
    kncg_set(p[0], kncg_gf0); kncg_set(p[1], kncg_gf1); kncg_set(p[2], kncg_gf1); kncg_set(p[3], kncg_gf0);
    for (int i = 255; i >= 0; --i) {
        int b = (s[i / 8] >> (i & 7)) & 1;
        kncg_cswap(p, q, b);
        kncg_padd(q, p);
        kncg_padd(p, p);
        kncg_cswap(p, q, b);
    }
}

static void kncg_scalarbase(kncg_gf p[4], const unsigned char *s) {
    kncg_gf q[4];
    kncg_set(q[0], kncg_X); kncg_set(q[1], kncg_Y); kncg_set(q[2], kncg_gf1); kncg_M(q[3], kncg_X, kncg_Y);
    kncg_scalarmult(p, q, s);
}

static const int64_t kncg_L[32] = {0xed, 0xd3, 0xf5, 0x5c, 0x1a, 0x63, 0x12, 0x58, 0xd6, 0x9c, 0xf7,
                                   0xa2, 0xde, 0xf9, 0xde, 0x14, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x10};

static void kncg_modL(unsigned char *r, int64_t x[64]) {
    int64_t carry;
    int i, j;
    for (i = 63; i >= 32; --i) {
        carry = 0;
        for (j = i - 32; j < i - 12; ++j) {
            x[j] += carry - 16 * x[i] * kncg_L[j - (i - 32)];
            carry = (x[j] + 128) >> 8;
            x[j] -= carry * 256;
        }
        x[j] += carry;
        x[i] = 0;
    }
    carry = 0;
    for (j = 0; j < 32; j++) {
        x[j] += carry - (x[31] >> 4) * kncg_L[j];
        carry = x[j] >> 8;
        x[j] &= 255;
    }
    for (j = 0; j < 32; j++) x[j] -= carry * kncg_L[j];
    for (i = 0; i < 32; i++) {
        x[i + 1] += x[i] >> 8;
        r[i] = (unsigned char)(x[i] & 255);
    }
}

static void kncg_reduce(unsigned char *r) {
    int64_t x[64];
    for (int i = 0; i < 64; i++) x[i] = (int64_t)r[i];
    for (int i = 0; i < 64; i++) r[i] = 0;
    kncg_modL(r, x);
}

static void kncg_ed_pub(const unsigned char seed[32], unsigned char pk[32]) {
    unsigned char d[64];
    kncg_gf p[4];
    kncg_md_oneshot(KD_SHA512, seed, 32, d);
    d[0] &= 248;
    d[31] &= 127;
    d[31] |= 64;
    kncg_scalarbase(p, d);
    kncg_ppack(pk, p);
}

static void kncg_ed_sign(const unsigned char seed[32], const unsigned char *m, long long n, unsigned char sig[64]) {
    unsigned char d[64], h[64], r[64], pk[32];
    int64_t x[64];
    kncg_gf p[4];
    kncg_md_ctx c;
    kncg_ed_pub(seed, pk);
    kncg_md_oneshot(KD_SHA512, seed, 32, d);
    d[0] &= 248;
    d[31] &= 127;
    d[31] |= 64;
    kncg_md_init(&c, KD_SHA512);
    kncg_md_update(&c, d + 32, 32);
    kncg_md_update(&c, m, n);
    kncg_md_final(&c, r);
    kncg_reduce(r);
    kncg_scalarbase(p, r);
    kncg_ppack(sig, p);
    kncg_md_init(&c, KD_SHA512);
    kncg_md_update(&c, sig, 32);
    kncg_md_update(&c, pk, 32);
    kncg_md_update(&c, m, n);
    kncg_md_final(&c, h);
    kncg_reduce(h);
    for (int i = 0; i < 64; i++) x[i] = 0;
    for (int i = 0; i < 32; i++) x[i] = (int64_t)r[i];
    for (int i = 0; i < 32; i++)
        for (int j = 0; j < 32; j++) x[i + j] += h[i] * (int64_t)d[j];
    kncg_modL(sig + 32, x);
}

static int kncg_unpackneg(kncg_gf r[4], const unsigned char p[32]) {
    kncg_gf t, chk, num, den, den2, den4, den6;
    kncg_set(r[2], kncg_gf1);
    kncg_unpack(r[1], p);
    kncg_S(num, r[1]); kncg_M(den, num, kncg_D); kncg_Z(num, num, r[2]); kncg_A(den, r[2], den);
    kncg_S(den2, den); kncg_S(den4, den2); kncg_M(den6, den4, den2); kncg_M(t, den6, num); kncg_M(t, t, den);
    kncg_pow2523(t, t); kncg_M(t, t, num); kncg_M(t, t, den); kncg_M(t, t, den); kncg_M(r[0], t, den);
    kncg_S(chk, r[0]); kncg_M(chk, chk, den);
    if (kncg_neq(chk, num)) kncg_M(r[0], r[0], kncg_I);
    kncg_S(chk, r[0]); kncg_M(chk, chk, den);
    if (kncg_neq(chk, num)) return -1;
    if (kncg_par(r[0]) == (p[31] >> 7)) kncg_Z(r[0], kncg_gf0, r[0]);
    kncg_M(r[3], r[0], r[1]);
    return 0;
}

static int kncg_ed_verify(const unsigned char pk[32], const unsigned char *m, long long n, const unsigned char *sig,
                          long long siglen) {
    unsigned char h[64], t[32];
    kncg_gf p[4], q[4];
    kncg_md_ctx c;
    if (siglen != 64) return 0;
    /* S must be canonical (< L), as libcrypto requires. */
    for (int i = 31; i >= 0; i--) {
        if (sig[32 + i] < kncg_L[i]) break;
        if (sig[32 + i] > kncg_L[i] || i == 0) return 0;
    }
    if (kncg_unpackneg(q, pk)) return 0;
    kncg_md_init(&c, KD_SHA512);
    kncg_md_update(&c, sig, 32);
    kncg_md_update(&c, pk, 32);
    kncg_md_update(&c, m, n);
    kncg_md_final(&c, h);
    kncg_reduce(h);
    kncg_scalarmult(p, q, h);
    kncg_scalarbase(q, sig + 32);
    kncg_padd(p, q);
    kncg_ppack(t, p);
    return memcmp(t, sig, 32) == 0;
}

/* ── key generation ─────────────────────────────────────────────────────── */

static int kncg_gen_rsa_key(ll bits, kncg_key *k) {
    BCRYPT_KEY_HANDLE h = NULL;
    ULONG n = 0;
    int ok = 0;
    kncg_init();
    memset(k, 0, sizeof *k);
    if (!BCRYPT_SUCCESS(BCryptGenerateKeyPair(kncg_alg_rsa, &h, (ULONG)bits, 0))) return 0;
    if (BCRYPT_SUCCESS(BCryptFinalizeKeyPair(h, 0)) &&
        BCRYPT_SUCCESS(BCryptExportKey(h, NULL, BCRYPT_RSAFULLPRIVATE_BLOB, NULL, 0, &n, 0))) {
        unsigned char *blob = (unsigned char *)malloc(n);
        if (blob && BCRYPT_SUCCESS(BCryptExportKey(h, NULL, BCRYPT_RSAFULLPRIVATE_BLOB, blob, n, &n, 0)))
            ok = kncg_rsa_from_full(blob, n, k);
        if (blob) memset(blob, 0, n);
        free(blob);
    }
    BCryptDestroyKey(h);
    return ok;
}

static int kncg_gen_ec_key(ll curve, kncg_key *k) {
    static const ULONG bits[4] = {0, 256, 384, 521};
    BCRYPT_KEY_HANDLE h = NULL;
    unsigned char blob[sizeof(BCRYPT_ECCKEY_BLOB) + 3 * 66];
    ULONG n = 0;
    size_t cb = (size_t)kml_curve_bytes(curve);
    int ok = 0;
    kncg_init();
    memset(k, 0, sizeof *k);
    if (!cb || !BCRYPT_SUCCESS(BCryptGenerateKeyPair(kncg_alg_ecdsa[curve], &h, bits[curve], 0))) return 0;
    if (BCRYPT_SUCCESS(BCryptFinalizeKeyPair(h, 0)) &&
        BCRYPT_SUCCESS(BCryptExportKey(h, NULL, BCRYPT_ECCPRIVATE_BLOB, blob, sizeof blob, &n, 0)) &&
        n == sizeof(BCRYPT_ECCKEY_BLOB) + 3 * cb) {
        k->type = KK_EC;
        k->priv = 1;
        k->curve = curve;
        memcpy(k->x, blob + sizeof(BCRYPT_ECCKEY_BLOB), cb);
        memcpy(k->y, blob + sizeof(BCRYPT_ECCKEY_BLOB) + cb, cb);
        memcpy(k->d, blob + sizeof(BCRYPT_ECCKEY_BLOB) + 2 * cb, cb);
        ok = 1;
    }
    memset(blob, 0, sizeof blob);
    BCryptDestroyKey(h);
    return ok;
}

static ll kncg_key_pair_der(const kncg_key *k, unsigned char **pkcs8, long long *pkcs8Len, unsigned char **spki,
                            long long *spkiLen) {
    kml_der a = {0}, b = {0};
    if (!kncg_pkcs8(k, &a) || !kncg_spki(k, &b)) {
        free(a.buf);
        free(b.buf);
        return -1;
    }
    *pkcs8 = a.buf;
    *pkcs8Len = (ll)a.len;
    *spki = b.buf;
    *spkiLen = (ll)b.len;
    return 0;
}

long long __kml_crypto_gen_rsa(long long modulusBits, unsigned char **pkcs8,
                               long long *pkcs8Len, unsigned char **spki,
                               long long *spkiLen) {
    kncg_key k;
    ll rc = -1;
    if (kncg_gen_rsa_key(modulusBits, &k)) rc = kncg_key_pair_der(&k, pkcs8, pkcs8Len, spki, spkiLen);
    kncg_key_free(&k);
    return rc;
}

long long __kml_crypto_gen_ec(long long curveId, unsigned char **pkcs8,
                              long long *pkcs8Len, unsigned char **spki,
                              long long *spkiLen) {
    kncg_key k;
    ll rc = -1;
    if (!kml_curve_bytes(curveId)) return -3;
    if (kncg_gen_ec_key(curveId, &k)) rc = kncg_key_pair_der(&k, pkcs8, pkcs8Len, spki, spkiLen);
    kncg_key_free(&k);
    return rc;
}

/* ── AES-GCM / AES-CTR over AES-ECB ─────────────────────────────────────────
 * CNG's own GCM (BCRYPT_AUTHENTICATED_CIPHER_MODE_INFO chaining) takes only
 * a 12-byte nonce and whole blocks between chained calls, while Node streams
 * arbitrary update() sizes and accepts any IV length; so GCM is CTR + a GHASH
 * over GF(2^128) here, and CTR (no CNG mode at all) a 128-bit big-endian
 * counter over ECB, as in the CommonCrypto backend. */

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

/* ---- the handle table ---- */
enum { KNCG_FREE = 0, KNCG_HASH, KNCG_HMAC, KNCG_CIPHER };

enum { KC_CBC = 0, KC_ECB, KC_CTR, KC_GCM };

typedef struct {
    int mode, encrypt, padding, keylen;
    BCRYPT_KEY_HANDLE k;      /* CBC/ECB in their direction, or CTR/GCM's ECB encryptor */
    unsigned char iv[16];     /* CBC: the chaining value CNG updates in place */
    unsigned char buf[16];    /* CBC/ECB pending bytes */
    int buflen;
    /* GCM / CTR */
    unsigned char h[16], j0[16], ctr[16], ks[16], x[16], gbuf[16], tag[16];
    int ksused, gbuflen, taglen, in_data, tag_set, finished, ctr_started;
    unsigned long long aadlen, ctlen;
} kncg_cipher;

typedef struct {
    int kind;
    int md;
    kncg_md_ctx hash;
    kncg_hmac hmac;
    kncg_cipher *cipher;
} kncg_handle;

static kncg_handle *kncg_tab = NULL;
static long long kncg_cap = 0;

static long long kncg_alloc(void) {
    for (long long i = 0; i < kncg_cap; i++)
        if (kncg_tab[i].kind == KNCG_FREE) return i;
    long long ncap = kncg_cap ? kncg_cap * 2 : 16;
    kncg_handle *t = (kncg_handle *)realloc(kncg_tab, (size_t)ncap * sizeof *t);
    if (!t) return -1;
    memset(t + kncg_cap, 0, (size_t)(ncap - kncg_cap) * sizeof *t);
    long long id = kncg_cap;
    kncg_tab = t;
    kncg_cap = ncap;
    return id;
}

static kncg_handle *kncg_get(double id) {
    long long i = (long long)id;
    if (i < 0 || i >= kncg_cap || kncg_tab[i].kind == KNCG_FREE) return NULL;
    return &kncg_tab[i];
}

static void kncg_release(kncg_handle *h) {
    if (h->kind == KNCG_HASH) kncg_md_free(&h->hash);
    if (h->kind == KNCG_HMAC) kncg_hmac_free(&h->hmac);
    if (h->cipher) {
        if (h->cipher->k) BCryptDestroyKey(h->cipher->k);
        memset(h->cipher, 0, sizeof *h->cipher);
        free(h->cipher);
    }
    memset(h, 0, sizeof *h);
}

void __kml_native_crypto_free(double id) {
    kncg_handle *h = kncg_get(id);
    if (h) kncg_release(h);
}

/* createHash: the id, or -1 for a digest this backend does not provide
 * (outputLength only applies to an XOF, and there is none here). */
double __kml_native_crypto_hash_new(const char *alg, double xofLen) {
    (void)xofLen;
    int md = kncg_md(alg);
    if (md < 0) return -1;
    long long id = kncg_alloc();
    if (id < 0) return -1;
    if (!kncg_md_init(&kncg_tab[id].hash, md)) return -1;
    kncg_tab[id].kind = KNCG_HASH;
    kncg_tab[id].md = md;
    return (double)id;
}

double __kml_native_crypto_hmac_new(const char *alg, void *key, long long keyLen) {
    int md = kncg_md(alg);
    if (md < 0) return -1;
    long long id = kncg_alloc();
    if (id < 0) return -1;
    if (!kncg_hmac_init(&kncg_tab[id].hmac, md, (const unsigned char *)key, keyLen > 0 ? (size_t)keyLen : 0))
        return -1;
    kncg_tab[id].kind = KNCG_HMAC;
    kncg_tab[id].md = md;
    return (double)id;
}

double __kml_native_crypto_hash_update(double id, void *data, long long len) {
    kncg_handle *h = kncg_get(id);
    if (!h) return 0;
    if (h->kind == KNCG_HASH) {
        kncg_md_update(&h->hash, data, len);
        return 1;
    }
    if (h->kind == KNCG_HMAC) {
        kncg_md_update(&h->hmac.in, data, len);
        return 1;
    }
    return 0;
}

double __kml_native_crypto_hash_size(double id) {
    kncg_handle *h = kncg_get(id);
    if (!h || (h->kind != KNCG_HASH && h->kind != KNCG_HMAC)) return 0;
    return (double)kncg_md_len[h->md];
}

double __kml_native_crypto_hash_digest(double id, void *out, long long outLen) {
    kncg_handle *h = kncg_get(id);
    if (!h) return -1;
    double r = -1;
    unsigned char buf[64];
    if (h->kind == KNCG_HASH || h->kind == KNCG_HMAC) {
        if (h->kind == KNCG_HASH) kncg_md_final(&h->hash, buf);
        else kncg_hmac_final(&h->hmac, buf);
        size_t n = kncg_md_len[h->md];
        if ((long long)n <= outLen) {
            memcpy(out, buf, n);
            r = (double)n;
        }
    }
    kncg_release(h);
    return r;
}

double __kml_native_crypto_hash_copy(double id) {
    kncg_handle *h = kncg_get(id);
    if (!h || h->kind != KNCG_HASH) return -1;
    long long nid = kncg_alloc();
    if (nid < 0) return -1;
    h = kncg_get(id); /* the table may have moved */
    if (!kncg_md_dup(&h->hash, &kncg_tab[nid].hash)) return -1;
    kncg_tab[nid].kind = KNCG_HASH;
    kncg_tab[nid].md = h->md;
    return (double)nid;
}

static const char *kncg_cipher_list =
    "aes-128-cbc,aes-128-ctr,aes-128-ecb,aes-128-gcm,aes-192-cbc,aes-192-ctr,aes-192-ecb,aes-192-gcm,"
    "aes-256-cbc,aes-256-ctr,aes-256-ecb,aes-256-gcm,aes128,aes192,aes256,"
    "id-aes128-GCM,id-aes192-GCM,id-aes256-GCM";

/* getHashes()/getCiphers(): the names joined by ','; 2 is the OpenSSL
 * version number, and there is no OpenSSL here. */
char *__kml_native_crypto_names(double which) {
    if (which == 2) return kncg_str("0");
    if (which == 3) return kncg_str("prime256v1,secp384r1,secp521r1"); /* CNG's named NIST curves */
    return kncg_str(which == 0 ? kncg_hash_list : kncg_cipher_list);
}

/* ---- random ---- */

double __kml_native_crypto_random_fill(void *data, long long size, double offset, double length) {
    long long off = (long long)offset, len = (long long)length;
    if (off < 0 || len < 0 || off + len > size) return -1;
    if (len == 0) return 0;
    return kncg_random((unsigned char *)data + off, (size_t)len) ? 0 : -1;
}

typedef struct { unsigned char *p; long long len; } kncg_rand_job;

static void kncg_rand_work(void *job, long long *err, double *res) {
    kncg_rand_job *j = (kncg_rand_job *)job;
    *err = (j->len == 0 || kncg_random(j->p, (size_t)j->len)) ? 0 : 1;
    *res = 0;
    free(j);
}

void __kml_native_crypto_random_fill_async(void *data, long long size, double offset, double length, void *inv, void *clo) {
    long long off = (long long)offset, len = (long long)length;
    kncg_rand_job *j = (kncg_rand_job *)malloc(sizeof *j);
    if (off < 0 || len < 0 || off + len > size) len = 0, off = 0;
    j->p = (unsigned char *)data + off;
    j->len = len;
    __kml_pool_job(kncg_rand_work, j, inv, clo);
}

/* ---- key derivation: pbkdf2 (BCryptDeriveKeyPBKDF2), scrypt, hkdf ---- */

/* PBKDF2 over the in-shim HMAC (SHA-224, which CNG lacks). */
static void kncg_pbkdf2_soft(int md, const unsigned char *pw, size_t pwlen, const unsigned char *salt, size_t saltlen,
                             unsigned long long iter, unsigned char *out, size_t outlen) {
    kncg_hmac base, c;
    unsigned char u[64], t[64], be[4];
    size_t hl = kncg_md_len[md];
    kncg_hmac_init(&base, md, pw, pwlen);
    for (uint32_t blk = 1; outlen > 0; blk++) {
        be[0] = (unsigned char)(blk >> 24); be[1] = (unsigned char)(blk >> 16);
        be[2] = (unsigned char)(blk >> 8); be[3] = (unsigned char)blk;
        kncg_hmac_dup(&base, &c);
        kncg_md_update(&c.in, salt, (long long)saltlen);
        kncg_md_update(&c.in, be, 4);
        kncg_hmac_final(&c, u);
        memcpy(t, u, hl);
        for (unsigned long long i = 1; i < iter; i++) {
            kncg_hmac_dup(&base, &c);
            kncg_md_update(&c.in, u, (long long)hl);
            kncg_hmac_final(&c, u);
            for (size_t k = 0; k < hl; k++) t[k] ^= u[k];
        }
        size_t n = outlen < hl ? outlen : hl;
        memcpy(out, t, n);
        out += n;
        outlen -= n;
    }
    kncg_hmac_free(&base);
}

static long long kncg_pbkdf2_run(int md, const unsigned char *pw, size_t pwlen, const unsigned char *salt, size_t saltlen,
                                 double iter, unsigned char *out, size_t outlen) {
    if (iter < 1) return 1;
    if (outlen == 0) return 0;
    kncg_init();
    if (md != KD_SHA224 && pwlen <= 0xffffffffU && saltlen <= 0xffffffffU && outlen <= 0xffffffffU) {
        static const unsigned char empty = 0;
        return BCRYPT_SUCCESS(BCryptDeriveKeyPBKDF2(kncg_alg_prf[md], (PUCHAR)(pwlen ? pw : &empty), (ULONG)pwlen,
                                                    (PUCHAR)(saltlen ? salt : &empty), (ULONG)saltlen,
                                                    (ULONGLONG)iter, out, (ULONG)outlen, 0))
                   ? 0
                   : 1;
    }
    kncg_pbkdf2_soft(md, pw, pwlen, salt, saltlen, (unsigned long long)iter, out, outlen);
    return 0;
}

/* RFC 5869 over the HMAC; 1 (no error text) when the length exceeds 255 * HashLen. */
static long long kncg_hkdf(int md, const unsigned char *ikm, size_t ikmlen, const unsigned char *salt, size_t saltlen,
                           const unsigned char *info, size_t infolen, unsigned char *out, size_t outlen) {
    size_t hl = kncg_md_len[md];
    unsigned char prk[64], t[64];
    static const unsigned char zeros[64] = {0};
    kncg_hmac c;
    if (outlen > 255 * hl) return 1;
    kncg_hmac_oneshot(md, saltlen ? salt : zeros, saltlen ? saltlen : hl, ikm, ikmlen, prk);
    size_t tl = 0, done = 0;
    unsigned char ctr = 1;
    while (done < outlen) {
        kncg_hmac_init(&c, md, prk, hl);
        if (tl) kncg_md_update(&c.in, t, (long long)tl);
        if (infolen) kncg_md_update(&c.in, info, (long long)infolen);
        kncg_md_update(&c.in, &ctr, 1);
        kncg_hmac_final(&c, t);
        tl = hl;
        size_t n = outlen - done < hl ? outlen - done : hl;
        memcpy(out + done, t, n);
        done += n;
        ctr++;
    }
    memset(prk, 0, sizeof prk);
    return 0;
}

/* scrypt (RFC 7914): Salsa20/8, BlockMix, ROMix over PBKDF2-HMAC-SHA256. */
#define KNCG_R(a, b) (((a) << (b)) | ((a) >> (32 - (b))))

static void kncg_salsa8(uint32_t B[16]) {
    uint32_t x[16];
    memcpy(x, B, sizeof x);
    for (int i = 0; i < 8; i += 2) {
        x[4] ^= KNCG_R(x[0] + x[12], 7);  x[8] ^= KNCG_R(x[4] + x[0], 9);
        x[12] ^= KNCG_R(x[8] + x[4], 13); x[0] ^= KNCG_R(x[12] + x[8], 18);
        x[9] ^= KNCG_R(x[5] + x[1], 7);   x[13] ^= KNCG_R(x[9] + x[5], 9);
        x[1] ^= KNCG_R(x[13] + x[9], 13); x[5] ^= KNCG_R(x[1] + x[13], 18);
        x[14] ^= KNCG_R(x[10] + x[6], 7); x[2] ^= KNCG_R(x[14] + x[10], 9);
        x[6] ^= KNCG_R(x[2] + x[14], 13); x[10] ^= KNCG_R(x[6] + x[2], 18);
        x[3] ^= KNCG_R(x[15] + x[11], 7); x[7] ^= KNCG_R(x[3] + x[15], 9);
        x[11] ^= KNCG_R(x[7] + x[3], 13); x[15] ^= KNCG_R(x[11] + x[7], 18);
        x[1] ^= KNCG_R(x[0] + x[3], 7);   x[2] ^= KNCG_R(x[1] + x[0], 9);
        x[3] ^= KNCG_R(x[2] + x[1], 13);  x[0] ^= KNCG_R(x[3] + x[2], 18);
        x[6] ^= KNCG_R(x[5] + x[4], 7);   x[7] ^= KNCG_R(x[6] + x[5], 9);
        x[4] ^= KNCG_R(x[7] + x[6], 13);  x[5] ^= KNCG_R(x[4] + x[7], 18);
        x[11] ^= KNCG_R(x[10] + x[9], 7); x[8] ^= KNCG_R(x[11] + x[10], 9);
        x[9] ^= KNCG_R(x[8] + x[11], 13); x[10] ^= KNCG_R(x[9] + x[8], 18);
        x[12] ^= KNCG_R(x[15] + x[14], 7); x[13] ^= KNCG_R(x[12] + x[15], 9);
        x[14] ^= KNCG_R(x[13] + x[12], 13); x[15] ^= KNCG_R(x[14] + x[13], 18);
    }
    for (int i = 0; i < 16; i++) B[i] += x[i];
}

/* B (2r 64-byte blocks, as words) → Y, then B = Y reordered (evens, odds). */
static void kncg_blockmix(uint32_t *B, uint32_t *Y, uint64_t r) {
    uint32_t X[16];
    memcpy(X, &B[(2 * r - 1) * 16], 64);
    for (uint64_t i = 0; i < 2 * r; i++) {
        for (int k = 0; k < 16; k++) X[k] ^= B[i * 16 + k];
        kncg_salsa8(X);
        memcpy(&Y[i * 16], X, 64);
    }
    for (uint64_t i = 0; i < r; i++) memcpy(&B[i * 16], &Y[(2 * i) * 16], 64);
    for (uint64_t i = 0; i < r; i++) memcpy(&B[(r + i) * 16], &Y[(2 * i + 1) * 16], 64);
}

static void kncg_romix(unsigned char *b, uint64_t r, uint64_t N, uint32_t *V, uint32_t *X, uint32_t *Y) {
    uint64_t words = 32 * r;
    for (uint64_t k = 0; k < words; k++)
        X[k] = (uint32_t)b[4 * k] | (uint32_t)b[4 * k + 1] << 8 | (uint32_t)b[4 * k + 2] << 16 | (uint32_t)b[4 * k + 3] << 24;
    for (uint64_t i = 0; i < N; i++) {
        memcpy(&V[i * words], X, words * 4);
        kncg_blockmix(X, Y, r);
    }
    for (uint64_t i = 0; i < N; i++) {
        uint64_t j = X[(2 * r - 1) * 16] & (N - 1);
        for (uint64_t k = 0; k < words; k++) X[k] ^= V[j * words + k];
        kncg_blockmix(X, Y, r);
    }
    for (uint64_t k = 0; k < words; k++) {
        b[4 * k] = (unsigned char)X[k]; b[4 * k + 1] = (unsigned char)(X[k] >> 8);
        b[4 * k + 2] = (unsigned char)(X[k] >> 16); b[4 * k + 3] = (unsigned char)(X[k] >> 24);
    }
}

/* libcrypto's EVP_PBE_scrypt parameter and memory checks, then the KDF. */
static long long kncg_scrypt(const unsigned char *pw, size_t pwlen, const unsigned char *salt, size_t saltlen,
                             double dN, double dr, double dp, double dmaxmem, unsigned char *out, size_t outlen) {
    uint64_t N = (uint64_t)dN, r = (uint64_t)dr, p = (uint64_t)dp, maxmem = (uint64_t)dmaxmem;
    const uint64_t PR_MAX = (1ULL << 30) - 1;
    if (r == 0 || p == 0 || N < 2 || (N & (N - 1))) return 1;
    if (p > PR_MAX / r) { kncg_set_err(KNCG_E_MEMLIMIT); return 1; }
    if (16 * r <= 63 && N >= (1ULL << (16 * r))) { kncg_set_err(KNCG_E_MEMLIMIT); return 1; }
    uint64_t Blen = p * 128 * r;
    if (Blen > 2147483647ULL) { kncg_set_err(KNCG_E_MEMLIMIT); return 1; }
    uint64_t lim = UINT64_MAX / (32 * sizeof(uint32_t));
    if (N + 2 > lim / r) { kncg_set_err(KNCG_E_MEMLIMIT); return 1; }
    uint64_t Vlen = 32 * r * (N + 2) * sizeof(uint32_t);
    if (Blen > UINT64_MAX - Vlen) { kncg_set_err(KNCG_E_MEMLIMIT); return 1; }
    if (maxmem == 0) maxmem = 1024 * 1024 * 32;
    if (Blen + Vlen > maxmem) { kncg_set_err(KNCG_E_MEMLIMIT); return 1; }
    if (outlen == 0) return 0;
    unsigned char *B = (unsigned char *)malloc((size_t)Blen);
    uint32_t *V = (uint32_t *)malloc((size_t)(32 * r * N * 4));
    uint32_t *XY = (uint32_t *)malloc((size_t)(64 * r * 4));
    long long rc = 1;
    if (B && V && XY && kncg_pbkdf2_run(KD_SHA256, pw, pwlen, salt, saltlen, 1, B, (size_t)Blen) == 0) {
        for (uint64_t i = 0; i < p; i++) kncg_romix(B + 128 * r * i, r, N, V, XY, XY + 32 * r);
        if (kncg_pbkdf2_run(KD_SHA256, pw, pwlen, B, (size_t)Blen, 1, out, outlen) == 0) rc = 0;
    }
    if (B) memset(B, 0, (size_t)Blen);
    free(B); free(V); free(XY);
    return rc;
}

enum { KNCG_PBKDF2 = 0, KNCG_SCRYPT, KNCG_HKDF };
typedef struct {
    int op;
    unsigned char *a, *b, *c;
    long long alen, blen, clen;
    double n1, n2, n3, n4;
    char digest[64];
    unsigned char *out;
    long long outlen;
} kncg_kdf;

static long long kncg_kdf_run(kncg_kdf *k) {
    switch (k->op) {
    case KNCG_PBKDF2: {
        int md = kncg_md(k->digest);
        if (md < 0) return 2;
        return kncg_pbkdf2_run(md, k->a, (size_t)k->alen, k->b, (size_t)k->blen, k->n1, k->out, (size_t)k->outlen);
    }
    case KNCG_SCRYPT:
        return kncg_scrypt(k->a, (size_t)k->alen, k->b, (size_t)k->blen, k->n1, k->n2, k->n3, k->n4, k->out,
                           (size_t)k->outlen);
    case KNCG_HKDF: {
        int md = kncg_md(k->digest);
        if (md < 0) return 2;
        return kncg_hkdf(md, k->a, (size_t)k->alen, k->b, (size_t)k->blen, k->c, (size_t)k->clen, k->out,
                         (size_t)k->outlen);
    }
    }
    return 1;
}

static unsigned char *kncg_dup(const void *p, long long n) {
    unsigned char *d = (unsigned char *)malloc((size_t)(n > 0 ? n : 1));
    if (n > 0) memcpy(d, p, (size_t)n);
    return d;
}

static kncg_kdf *kncg_kdf_new(double op, void *a, long long alen, void *b, long long blen, void *c, long long clen,
                              double n1, double n2, double n3, double n4, const char *digest, void *out, long long outlen) {
    kncg_kdf *k = (kncg_kdf *)calloc(1, sizeof *k);
    k->op = (int)op;
    k->a = kncg_dup(a, alen); k->alen = alen;
    k->b = kncg_dup(b, blen); k->blen = blen;
    k->c = kncg_dup(c, clen); k->clen = clen;
    k->n1 = n1; k->n2 = n2; k->n3 = n3; k->n4 = n4;
    snprintf(k->digest, sizeof k->digest, "%s", digest ? digest : "");
    k->out = (unsigned char *)out;
    k->outlen = outlen;
    return k;
}

static void kncg_kdf_free(kncg_kdf *k) {
    free(k->a); free(k->b); free(k->c); free(k);
}

/* 0 ok, 1 failed, 2 an unknown digest. */
double __kml_native_crypto_kdf(double op, void *a, long long alen, void *b, long long blen, void *c, long long clen,
                               double n1, double n2, double n3, double n4, const char *digest, void *out, long long outlen) {
    kncg_kdf *k = kncg_kdf_new(op, a, alen, b, blen, c, clen, n1, n2, n3, n4, digest, out, outlen);
    long long rc = kncg_kdf_run(k);
    kncg_kdf_free(k);
    return (double)rc;
}

static void kncg_kdf_work(void *job, long long *err, double *res) {
    kncg_kdf *k = (kncg_kdf *)job;
    *err = kncg_kdf_run(k);
    *res = 0;
    kncg_kdf_free(k);
}

void __kml_native_crypto_kdf_async(double op, void *a, long long alen, void *b, long long blen, void *c, long long clen,
                                   double n1, double n2, double n3, double n4, const char *digest, void *out, long long outlen,
                                   void *inv, void *clo) {
    kncg_kdf *k = kncg_kdf_new(op, a, alen, b, blen, c, clen, n1, n2, n3, n4, digest, out, outlen);
    __kml_pool_job(kncg_kdf_work, k, inv, clo);
}

/* ---- ciphers: AES CBC/ECB over CNG, CTR and GCM over its ECB ---- */

static int kncg_cipher_lookup(const char *alg, int *mode, int *keylen) {
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
        if (kncg_casecmp(alg, tab[i].name) == 0) {
            *mode = tab[i].mode;
            *keylen = tab[i].keylen;
            return 1;
        }
    return 0;
}

static void kncg_gcm_absorb(kncg_cipher *c, const unsigned char *p, size_t n) {
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

static void kncg_gcm_flush(kncg_cipher *c) {
    if (c->gbuflen) {
        memset(c->gbuf + c->gbuflen, 0, (size_t)(16 - c->gbuflen));
        for (int j = 0; j < 16; j++) c->x[j] ^= c->gbuf[j];
        kml_ghash_mul(c->x, c->h);
        c->gbuflen = 0;
    }
}

/* n (a multiple of 16) bytes through the key in the cipher's direction;
 * CBC chains through c->iv, which CNG updates in place. */
static int kncg_ecb(kncg_cipher *c, const unsigned char *in, unsigned char *out, size_t n) {
    ULONG moved = 0;
    NTSTATUS st;
    int cbc = c->mode == KC_CBC;
    int enc = c->mode == KC_CTR || c->mode == KC_GCM || c->encrypt;
    if (n == 0) return 1;
    if (enc)
        st = BCryptEncrypt(c->k, (PUCHAR)in, (ULONG)n, NULL, cbc ? c->iv : NULL, cbc ? 16 : 0, out, (ULONG)n, &moved, 0);
    else
        st = BCryptDecrypt(c->k, (PUCHAR)in, (ULONG)n, NULL, cbc ? c->iv : NULL, cbc ? 16 : 0, out, (ULONG)n, &moved, 0);
    return BCRYPT_SUCCESS(st) && moved == n;
}

/* CTR keystream (inc32) xor, n bytes. */
static int kncg_gcm_ctr(kncg_cipher *c, const unsigned char *in, unsigned char *out, size_t n) {
    for (size_t i = 0; i < n; i++) {
        if (c->ksused == 16) {
            kml_inc32(c->ctr);
            if (!kncg_ecb(c, c->ctr, c->ks, 16)) return 0;
            c->ksused = 0;
        }
        out[i] = in[i] ^ c->ks[c->ksused++];
    }
    return 1;
}

/* AES-CTR keystream xor with a 128-bit big-endian counter, n bytes. */
static int kncg_ctr128(kncg_cipher *c, const unsigned char *in, unsigned char *out, size_t n) {
    for (size_t i = 0; i < n; i++) {
        if (c->ksused == 16) {
            if (c->ctr_started)
                for (int j = 15; j >= 0 && ++c->ctr[j] == 0; j--) {}
            c->ctr_started = 1;
            if (!kncg_ecb(c, c->ctr, c->ks, 16)) return 0;
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
    if (!kncg_cipher_lookup(alg, &mode, &kl)) return -1;
    if (keyLen != kl) return -2;
    if (mode == KC_ECB && ivLen != 0) return -3;
    if ((mode == KC_CBC || mode == KC_CTR) && ivLen != 16) return -3;
    if (mode == KC_GCM && ivLen <= 0) return -3;
    kncg_cipher *c = (kncg_cipher *)calloc(1, sizeof *c);
    if (!c) return -1;
    c->mode = mode;
    c->encrypt = encrypt != 0;
    c->padding = 1;
    c->keylen = kl;
    kncg_init();
    int ok = BCRYPT_SUCCESS(BCryptGenerateSymmetricKey(mode == KC_CBC ? kncg_alg_cbc : kncg_alg_ecb, &c->k, NULL, 0,
                                                       (PUCHAR)key, (ULONG)kl, 0));
    if (ok && mode == KC_CBC) {
        memcpy(c->iv, iv, 16);
    } else if (ok && mode == KC_CTR) {
        /* The counter is a full 128-bit big-endian one, as in libcrypto. */
        memcpy(c->ctr, iv, 16);
        c->ksused = 16;
        c->ctr_started = 0;
    } else if (ok && mode == KC_GCM) {
        unsigned char zero[16] = {0};
        ok = kncg_ecb(c, zero, c->h, 16);
        if (ok) {
            if (ivLen == 12) {
                memcpy(c->j0, iv, 12);
                c->j0[15] = 1;
            } else {
                unsigned char lenblk[16] = {0};
                unsigned long long bits = (unsigned long long)ivLen * 8;
                kncg_gcm_absorb(c, (const unsigned char *)iv, (size_t)ivLen);
                kncg_gcm_flush(c);
                for (int j = 0; j < 8; j++) lenblk[8 + j] = (unsigned char)(bits >> (56 - 8 * j));
                kncg_gcm_absorb(c, lenblk, 16);
                memcpy(c->j0, c->x, 16);
                memset(c->x, 0, 16);
            }
            memcpy(c->ctr, c->j0, 16);
            c->ksused = 16;
        }
    }
    if (!ok) {
        if (c->k) BCryptDestroyKey(c->k);
        free(c);
        return -1;
    }
    long long id = kncg_alloc();
    if (id < 0) {
        BCryptDestroyKey(c->k);
        free(c);
        return -1;
    }
    kncg_tab[id].kind = KNCG_CIPHER;
    kncg_tab[id].cipher = c;
    return (double)id;
}

static kncg_cipher *kncg_cipher_get(double id) {
    kncg_handle *h = kncg_get(id);
    return h && h->kind == KNCG_CIPHER ? h->cipher : NULL;
}

static int kncg_block_size(kncg_cipher *c) {
    return c->mode == KC_CBC || c->mode == KC_ECB ? 16 : 1;
}

double __kml_native_crypto_cipher_block_size(double id) {
    kncg_cipher *c = kncg_cipher_get(id);
    return c ? kncg_block_size(c) : 0;
}

double __kml_native_crypto_cipher_update(double id, void *in, long long inLen, void *out, long long outLen) {
    kncg_cipher *c = kncg_cipher_get(id);
    if (!c) return -1;
    if (inLen + kncg_block_size(c) > outLen) return -1;
    const unsigned char *ip = (const unsigned char *)in;
    unsigned char *op = (unsigned char *)out;
    if (c->mode == KC_CTR) {
        return kncg_ctr128(c, ip, op, (size_t)inLen) ? (double)inLen : -1;
    }
    if (c->mode == KC_GCM) {
        if (c->finished) return -1;
        if (!c->in_data) {
            kncg_gcm_flush(c);
            c->in_data = 1;
        }
        if (c->encrypt) {
            if (!kncg_gcm_ctr(c, ip, op, (size_t)inLen)) return -1;
            kncg_gcm_absorb(c, op, (size_t)inLen);
        } else {
            kncg_gcm_absorb(c, ip, (size_t)inLen);
            if (!kncg_gcm_ctr(c, ip, op, (size_t)inLen)) return -1;
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
        if (!kncg_ecb(c, c->buf, op, 16)) return -1;
        ipos = take;
        written = 16;
        c->buflen = 0;
        need -= 16;
    }
    if (need > 0) {
        if (!kncg_ecb(c, ip + ipos, op + written, (size_t)need)) return -1;
        ipos += need;
        written += need;
    }
    memcpy(c->buf + c->buflen, ip + ipos, (size_t)(inLen - ipos));
    c->buflen += (int)(inLen - ipos);
    return (double)written;
}

double __kml_native_crypto_cipher_final(double id, void *out, long long outLen) {
    kncg_cipher *c = kncg_cipher_get(id);
    if (!c) return -1;
    if (outLen < kncg_block_size(c)) return -1;
    unsigned char *op = (unsigned char *)out;
    if (c->mode == KC_CTR) return 0;
    if (c->mode == KC_GCM) {
        unsigned char lenblk[16], ekj0[16];
        unsigned long long ab = c->aadlen * 8, cb = c->ctlen * 8;
        if (c->finished) return -1;
        if (!c->encrypt && !c->tag_set) return -1;
        kncg_gcm_flush(c);
        for (int j = 0; j < 8; j++) lenblk[j] = (unsigned char)(ab >> (56 - 8 * j));
        for (int j = 0; j < 8; j++) lenblk[8 + j] = (unsigned char)(cb >> (56 - 8 * j));
        kncg_gcm_absorb(c, lenblk, 16);
        if (!kncg_ecb(c, c->j0, ekj0, 16)) return -1;
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
                kncg_set_err(KNCG_E_FINAL_LEN);
                return -1;
            }
            return 0;
        }
        unsigned char pad = (unsigned char)(16 - c->buflen);
        memset(c->buf + c->buflen, pad, pad);
        c->buflen = 0;
        return kncg_ecb(c, c->buf, op, 16) ? 16 : -1;
    }
    if (!c->padding) {
        if (c->buflen) {
            kncg_set_err(KNCG_E_FINAL_LEN);
            return -1;
        }
        return 0;
    }
    if (c->buflen != 16) {
        kncg_set_err(KNCG_E_FINAL_LEN);
        return -1;
    }
    unsigned char blk[16];
    c->buflen = 0;
    if (!kncg_ecb(c, c->buf, blk, 16)) return -1;
    unsigned char pad = blk[15];
    if (pad == 0 || pad > 16) {
        kncg_set_err(KNCG_E_BAD_DECRYPT);
        return -1;
    }
    for (int j = 16 - pad; j < 16; j++)
        if (blk[j] != pad) {
            kncg_set_err(KNCG_E_BAD_DECRYPT);
            return -1;
        }
    memcpy(op, blk, (size_t)(16 - pad));
    return 16 - pad;
}

double __kml_native_crypto_cipher_set_padding(double id, double on) {
    kncg_cipher *c = kncg_cipher_get(id);
    if (!c) return 0;
    c->padding = on != 0;
    return 1;
}

double __kml_native_crypto_cipher_set_aad(double id, void *aad, long long len) {
    kncg_cipher *c = kncg_cipher_get(id);
    if (!c || c->mode != KC_GCM || c->in_data || c->finished) return 0;
    kncg_gcm_absorb(c, (const unsigned char *)aad, (size_t)len);
    c->aadlen += (unsigned long long)len;
    return 1;
}

double __kml_native_crypto_cipher_get_tag(double id, void *out, long long len) {
    kncg_cipher *c = kncg_cipher_get(id);
    if (!c || c->mode != KC_GCM || !c->encrypt || !c->finished || len < 1 || len > 16) return -1;
    memcpy(out, c->tag, (size_t)len);
    return (double)len;
}

double __kml_native_crypto_cipher_set_tag(double id, void *tag, long long len) {
    kncg_cipher *c = kncg_cipher_get(id);
    if (!c || c->mode != KC_GCM || c->encrypt || c->finished || len < 1 || len > 16) return 0;
    memcpy(c->tag, tag, (size_t)len);
    c->taglen = (int)len;
    c->tag_set = 1;
    return 1;
}

/* ---- PEM <-> DER, base64url ---- */

static const char kncg_b64[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
static const char kncg_b64u[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";

static void kncg_pem_append(kml_der *d, const char *label, const unsigned char *der, size_t n) {
    char line[80];
    snprintf(line, sizeof line, "-----BEGIN %s-----\n", label);
    kml_der_put(d, (const unsigned char *)line, strlen(line));
    size_t col = 0;
    for (size_t i = 0; i < n; i += 3) {
        unsigned v = (unsigned)der[i] << 16 | (i + 1 < n ? (unsigned)der[i + 1] << 8 : 0) | (i + 2 < n ? der[i + 2] : 0);
        char q[4] = {kncg_b64[v >> 18 & 63], kncg_b64[v >> 12 & 63], i + 1 < n ? kncg_b64[v >> 6 & 63] : '=',
                     i + 2 < n ? kncg_b64[v & 63] : '='};
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

static int kncg_b64val(char c) {
    if (c >= 'A' && c <= 'Z') return c - 'A';
    if (c >= 'a' && c <= 'z') return c - 'a' + 26;
    if (c >= '0' && c <= '9') return c - '0' + 52;
    if (c == '+' || c == '-') return 62;
    if (c == '/' || c == '_') return 63;
    return -1;
}

/* base64url, no padding (JWK members); malloc'd. */
static char *kncg_b64u_encode(const unsigned char *p, size_t n) {
    char *out = (char *)malloc(n / 3 * 4 + 5);
    size_t o = 0;
    for (size_t i = 0; i < n; i += 3) {
        unsigned v = (unsigned)p[i] << 16 | (i + 1 < n ? (unsigned)p[i + 1] << 8 : 0) | (i + 2 < n ? p[i + 2] : 0);
        out[o++] = kncg_b64u[v >> 18 & 63];
        out[o++] = kncg_b64u[v >> 12 & 63];
        if (i + 1 < n) out[o++] = kncg_b64u[v >> 6 & 63];
        if (i + 2 < n) out[o++] = kncg_b64u[v & 63];
    }
    out[o] = 0;
    return out;
}

static unsigned char *kncg_b64u_decode(const char *s, size_t *outn) {
    size_t n = strlen(s);
    unsigned char *out = (unsigned char *)malloc(n * 3 / 4 + 3);
    unsigned acc = 0;
    int bits = 0;
    size_t o = 0;
    for (size_t i = 0; i < n; i++) {
        if (s[i] == '=') break;
        int v = kncg_b64val(s[i]);
        if (v < 0) {
            free(out);
            return NULL;
        }
        acc = acc << 6 | (unsigned)v;
        bits += 6;
        if (bits >= 8) {
            bits -= 8;
            out[o++] = (unsigned char)(acc >> bits);
        }
    }
    *outn = o;
    return out;
}

/* The first PEM block: its label (into label) and DER (malloc'd). */
static unsigned char *kncg_pem_decode(const char *pem, char *label, size_t labelcap, size_t *derlen, char *dek,
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
        int v = (*p == '-' || *p == '_') ? -1 : kncg_b64val(*p);
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

/* CBC decrypt with PKCS#7 padding (AES or 3DES): 0 ok, 1 a bad decrypt. */
static int kncg_cbc_decrypt(int des3, const unsigned char *key, size_t keylen, const unsigned char *iv,
                            const unsigned char *in, size_t n, unsigned char **out, size_t *outn) {
    BCRYPT_KEY_HANDLE k = NULL;
    unsigned char ivc[16];
    ULONG moved = 0;
    size_t bs = des3 ? 8 : 16;
    unsigned char *buf = (unsigned char *)malloc(n + 16);
    if (!buf) return 2;
    kncg_init();
    memcpy(ivc, iv, bs);
    /* The padding is checked here, as libcrypto's EVP does. */
    int ok = n > 0 && n % bs == 0 &&
             BCRYPT_SUCCESS(BCryptGenerateSymmetricKey(des3 ? kncg_alg_3des : kncg_alg_cbc, &k, NULL, 0, (PUCHAR)key,
                                                       (ULONG)keylen, 0)) &&
             BCRYPT_SUCCESS(BCryptDecrypt(k, (PUCHAR)in, (ULONG)n, NULL, ivc, (ULONG)bs, buf, (ULONG)n, &moved, 0)) &&
             moved == n;
    if (k) BCryptDestroyKey(k);
    if (!ok) {
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
    *out = buf;
    *outn = n - pad;
    return 0;
}

static int kncg_hexval(char c) {
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    return -1;
}

/* Proc-Type: 4,ENCRYPTED / DEK-Info: <cipher>,<iv hex>; the key is
 * EVP_BytesToKey(MD5, salt = iv[0..8], count 1). 0 ok, 1 bad decrypt, 2
 * unsupported. */
static int kncg_decrypt_legacy(const char *dek, const unsigned char *der, size_t n, const unsigned char *pass,
                               size_t passLen, unsigned char **out, size_t *outn) {
    int des3 = 0;
    size_t keylen, ivlen;
    const char *comma = strchr(dek, ',');
    if (!comma) return 2;
    size_t nl = (size_t)(comma - dek);
    if (nl == 11 && !kncg_ncasecmp(dek, "AES-128-CBC", nl)) { keylen = 16; ivlen = 16; }
    else if (nl == 11 && !kncg_ncasecmp(dek, "AES-192-CBC", nl)) { keylen = 24; ivlen = 16; }
    else if (nl == 11 && !kncg_ncasecmp(dek, "AES-256-CBC", nl)) { keylen = 32; ivlen = 16; }
    else if (nl == 12 && !kncg_ncasecmp(dek, "DES-EDE3-CBC", nl)) { des3 = 1; keylen = 24; ivlen = 8; }
    else return 2;
    unsigned char iv[16], key[32], d[16];
    const char *h = comma + 1;
    for (size_t i = 0; i < ivlen; i++) {
        int a = kncg_hexval(h[2 * i]), b = a < 0 ? -1 : kncg_hexval(h[2 * i + 1]);
        if (b < 0) return 2;
        iv[i] = (unsigned char)(a << 4 | b);
    }
    size_t got = 0, dl = 0;
    while (got < keylen) {
        kncg_md_ctx c;
        kncg_md_init(&c, KD_MD5);
        if (dl) kncg_md_update(&c, d, (long long)dl);
        kncg_md_update(&c, pass, (long long)passLen);
        kncg_md_update(&c, iv, 8);
        kncg_md_final(&c, d);
        dl = 16;
        size_t take = keylen - got < 16 ? keylen - got : 16;
        memcpy(key + got, d, take);
        got += take;
    }
    int rc = kncg_cbc_decrypt(des3, key, keylen, iv, der, n, out, outn);
    memset(key, 0, sizeof key);
    return rc;
}

static const unsigned char kncg_oid_pbes2[] = {0x06, 0x09, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x05, 0x0d};
static const unsigned char kncg_oid_pbkdf2[] = {0x06, 0x09, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x05, 0x0c};
static const unsigned char kncg_oid_hmac_pfx[] = {0x06, 0x08, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x02};
static const unsigned char kncg_oid_aes_pfx[] = {0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x01};
static const unsigned char kncg_oid_des3[] = {0x06, 0x08, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x03, 0x07};

/* EncryptedPrivateKeyInfo with PBES2 (PBKDF2-HMAC + AES-CBC / 3DES-CBC) →
 * the PKCS#8 DER. 0 ok, 1 bad decrypt, 2 unsupported/malformed. */
static int kncg_decrypt_pkcs8(const unsigned char *der, size_t n, const unsigned char *pass, size_t passLen,
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
        !kncg_der_eq(oc, oclen, kncg_oid_pbes2, sizeof kncg_oid_pbes2)) return 2;
    if (!kml_der_read(&q, qe, &tag, &c, &clen) || tag != 0x30) return 2;
    q = c; qe = c + clen;
    if (!kml_der_read(&q, qe, &tag, &kp, &kplen) || tag != 0x30) return 2;
    if (!kml_der_read(&q, qe, &tag, &ep, &eplen) || tag != 0x30) return 2;
    /* PBKDF2 { salt, iterations, [keyLength], [prf] } */
    const unsigned char *r = kp, *re = kp + kplen;
    if (!kml_der_read(&r, re, &tag, &oc, &oclen) || tag != 0x06 ||
        !kncg_der_eq(oc, oclen, kncg_oid_pbkdf2, sizeof kncg_oid_pbkdf2)) return 2;
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
            if (ol != 8 || memcmp(o, kncg_oid_hmac_pfx + 2, 7) != 0) return 2;
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
    int des3 = 0;
    size_t keylen, ivlen;
    if (oclen == 9 && memcmp(oc, kncg_oid_aes_pfx + 2, 8) == 0 && (oc[8] == 0x02 || oc[8] == 0x16 || oc[8] == 0x2a)) {
        keylen = oc[8] == 0x02 ? 16 : oc[8] == 0x16 ? 24 : 32;
        ivlen = 16;
    } else if (kncg_der_eq(oc, oclen, kncg_oid_des3, sizeof kncg_oid_des3)) {
        des3 = 1;
        keylen = 24;
        ivlen = 8;
    } else {
        return 2;
    }
    const unsigned char *eiv; size_t eivlen;
    if (!kml_der_read(&s, se, &tag, &eiv, &eivlen) || tag != 0x04 || eivlen != ivlen) return 2;
    unsigned char key[32];
    if (kncg_pbkdf2_run(md, pass, passLen, salt, saltlen, (double)iter, key, keylen) != 0) return 2;
    int rc = kncg_cbc_decrypt(des3, key, keylen, eiv, data, datalen, out, outn);
    memset(key, 0, sizeof key);
    return rc;
}

/* ---- key parsing (any DER structure → components) ---- */

/* AlgorithmIdentifier SEQ content → key type (+ curve). */
static int kncg_algid_read(const unsigned char *c, size_t clen, int *type, ll *curve) {
    const unsigned char *p = c, *end = c + clen, *oc, *pc;
    unsigned char tag;
    size_t oclen, pclen;
    if (!kml_der_read(&p, end, &tag, &oc, &oclen) || tag != 0x06) return 0;
    if (kncg_der_eq(oc, oclen, kml_oid_rsa, sizeof kml_oid_rsa)) { *type = KK_RSA; return 1; }
    if (kncg_der_eq(oc, oclen, kncg_oid_ed25519, sizeof kncg_oid_ed25519)) { *type = KK_ED25519; return 1; }
    if (kncg_der_eq(oc, oclen, kncg_oid_x25519, sizeof kncg_oid_x25519)) { *type = KK_X25519; return 1; }
    if (kncg_der_eq(oc, oclen, kml_oid_ec, sizeof kml_oid_ec)) {
        if (!kml_der_read(&p, end, &tag, &pc, &pclen) || tag != 0x06) return 0;
        *curve = kncg_curve_from_oid(pc, pclen);
        *type = KK_EC;
        return *curve != 0;
    }
    return 0;
}

/* PKCS#1 RSAPrivateKey / RSAPublicKey (content of its SEQ). */
static int kncg_rsa_read(const unsigned char *c, size_t clen, int priv, kncg_key *k) {
    const unsigned char *p = c, *end = c + clen, *v;
    size_t vl;
    k->type = KK_RSA;
    k->priv = priv;
    if (priv) {
        if (!kml_der_read_uint(&p, end, &v, &vl)) return 0; /* version */
        for (int i = 0; i < 8; i++) {
            if (!kml_der_read_uint(&p, end, &v, &vl)) return 0;
            kncg_rsa_set(k, i, v, vl);
        }
    } else {
        for (int i = 0; i < 2; i++) {
            if (!kml_der_read_uint(&p, end, &v, &vl)) return 0;
            kncg_rsa_set(k, i, v, vl);
        }
    }
    return k->rsalen[KR_N] > 0 && k->rsalen[KR_E] > 0;
}

/* SEC1 ECPrivateKey (content of its SEQ); curve may come from [0], the
 * public point from [1] (or is derived from d). */
static int kncg_ec_priv_read(const unsigned char *c, size_t clen, kncg_key *k) {
    const unsigned char *p = c, *end = c + clen, *ic, *kd = NULL, *pt = NULL;
    unsigned char tag;
    size_t iclen, kn = 0, ptlen = 0;
    if (!kml_der_read(&p, end, &tag, &ic, &iclen) || tag != 0x02) return 0;
    if (!kml_der_read(&p, end, &tag, &ic, &iclen) || tag != 0x04) return 0;
    kd = ic;
    kn = iclen;
    while (p < end) {
        if (!kml_der_read(&p, end, &tag, &ic, &iclen)) return 0;
        const unsigned char *bp = ic, *bend = ic + iclen, *bc;
        size_t bclen;
        unsigned char t2;
        if (tag == 0xa0 && kml_der_read(&bp, bend, &t2, &bc, &bclen) && t2 == 0x06) {
            ll cv = kncg_curve_from_oid(bc, bclen);
            if (k->curve == 0) k->curve = cv;
        } else if (tag == 0xa1 && kml_der_read(&bp, bend, &t2, &bc, &bclen) && t2 == 0x03 && bclen > 1) {
            pt = bc + 1;
            ptlen = bclen - 1;
        }
    }
    ll cb = kml_curve_bytes(k->curve);
    if (!cb || kn > (size_t)cb) return 0;
    k->type = KK_EC;
    k->priv = 1;
    memset(k->d, 0, sizeof k->d);
    memcpy(k->d + ((size_t)cb - kn), kd, kn);
    if (!kncg_ec_priv_in_range(k->curve, k->d)) return 0;
    if (pt) return kncg_ec_point(k->curve, pt, ptlen, k->x, k->y);
    return kncg_ec_pub_from_d(k->curve, k->d, k->x, k->y);
}

/* A DER body under its PEM label ("PRIVATE KEY", "PUBLIC KEY", "RSA PRIVATE
 * KEY", "RSA PUBLIC KEY", "EC PRIVATE KEY") → key. */
static int kncg_parse_der(const char *label, const unsigned char *der, size_t n, kncg_key *key) {
    const unsigned char *p = der, *end = der + n, *c, *ac, *oc;
    unsigned char tag;
    size_t clen, aclen, oclen;
    memset(key, 0, sizeof *key);
    if (!kml_der_read(&p, end, &tag, &c, &clen) || tag != 0x30) return 0;
    p = c;
    end = c + clen;
    if (strcmp(label, "PRIVATE KEY") == 0) {
        if (!kml_der_read(&p, end, &tag, &oc, &oclen) || tag != 0x02) return 0;
        if (!kml_der_read(&p, end, &tag, &ac, &aclen) || tag != 0x30) return 0;
        if (!kncg_algid_read(ac, aclen, &key->type, &key->curve)) return 0;
        if (!kml_der_read(&p, end, &tag, &oc, &oclen) || tag != 0x04) return 0;
        const unsigned char *q = oc, *qc;
        size_t qclen;
        if (key->type == KK_RSA) {
            if (!kml_der_read(&q, oc + oclen, &tag, &qc, &qclen) || tag != 0x30) return 0;
            return kncg_rsa_read(qc, qclen, 1, key);
        }
        if (key->type == KK_EC) {
            if (!kml_der_read(&q, oc + oclen, &tag, &qc, &qclen) || tag != 0x30) return 0;
            return kncg_ec_priv_read(qc, qclen, key);
        }
        if (!kml_der_read(&q, oc + oclen, &tag, &qc, &qclen) || tag != 0x04 || qclen != 32) return 0;
        key->priv = 1;
        memcpy(key->raw, qc, 32);
        return 1;
    }
    if (strcmp(label, "RSA PRIVATE KEY") == 0) return kncg_rsa_read(c, clen, 1, key);
    if (strcmp(label, "RSA PUBLIC KEY") == 0) return kncg_rsa_read(c, clen, 0, key);
    if (strcmp(label, "EC PRIVATE KEY") == 0) return kncg_ec_priv_read(c, clen, key);
    if (strcmp(label, "PUBLIC KEY") == 0) {
        if (!kml_der_read(&p, end, &tag, &ac, &aclen) || tag != 0x30) return 0;
        if (!kncg_algid_read(ac, aclen, &key->type, &key->curve)) return 0;
        if (!kml_der_read(&p, end, &tag, &oc, &oclen) || tag != 0x03 || oclen < 2 || oc[0] != 0) return 0;
        if (key->type == KK_RSA) {
            const unsigned char *q = oc + 1, *qc;
            size_t qclen;
            if (!kml_der_read(&q, oc + oclen, &tag, &qc, &qclen) || tag != 0x30) return 0;
            return kncg_rsa_read(qc, qclen, 0, key);
        }
        if (key->type == KK_EC) return kncg_ec_point(key->curve, oc + 1, oclen - 1, key->x, key->y);
        if (oclen - 1 != 32) return 0;
        memcpy(key->raw, oc + 1, 32);
        return 1;
    }
    return 0;
}

/* Decrypts an encrypted body (legacy DEK-Info PEM or PBES2 PKCS#8) in
 * place of *der; 0 when it fails, with libcrypto's error text set. */
static int kncg_decrypt_body(const char *dek, unsigned char **der, size_t *n, const unsigned char *pass,
                             long long passLen) {
    unsigned char *plain = NULL;
    size_t plen = 0;
    /* No passphrase: libcrypto's PasswordCallback cancels the read. */
    if (passLen < 0) {
        kncg_set_err(KNCG_E_INTERRUPTED);
        return 0;
    }
    int rc = dek[0] ? kncg_decrypt_legacy(dek, *der, *n, pass, (size_t)passLen, &plain, &plen)
                    : kncg_decrypt_pkcs8(*der, *n, pass, (size_t)passLen, &plain, &plen);
    if (rc != 0) {
        if (rc == 1) kncg_set_err(KNCG_E_BAD_DECRYPT);
        return 0;
    }
    free(*der);
    *der = plain;
    *n = plen;
    return 1;
}

/* A PEM key (passLen < 0: no passphrase) → key. */
static int kncg_parse_key(const char *pem, const unsigned char *pass, long long passLen, kncg_key *key) {
    char label[64], dek[128];
    size_t n = 0;
    unsigned char *der = kncg_pem_decode(pem, label, sizeof label, &n, dek, sizeof dek);
    int ok = 0;
    memset(key, 0, sizeof *key);
    kncg_err[0] = 0;
    if (!der) return 0;
    if (dek[0] || strcmp(label, "ENCRYPTED PRIVATE KEY") == 0) {
        if (!kncg_decrypt_body(dek, &der, &n, pass, passLen)) {
            free(der);
            return 0;
        }
        if (!dek[0]) snprintf(label, sizeof label, "PRIVATE KEY");
    }
    ok = kncg_parse_der(label, der, n, key);
    memset(der, 0, n);
    free(der);
    if (!ok) kncg_key_free(key);
    return ok;
}

/* The key's PEM in a structure: type 1 pkcs1, 2 spki, 3 pkcs8, 4 sec1;
 * format 0 PEM, 1 DER. malloc'd (PEM NUL-terminated), NULL when the key has
 * no such encoding. */
static unsigned char *kncg_key_encode(const kncg_key *k, int priv, int type, int format, size_t *outn) {
    kml_der d = {0}, out = {0};
    const char *label = NULL;
    int ok = 0;
    if (priv && !k->priv) return NULL;
    switch (type) {
    case 1:
        if (k->type != KK_RSA) return NULL;
        ok = kncg_rsa_pkcs1(k, priv, &d);
        label = priv ? "RSA PRIVATE KEY" : "RSA PUBLIC KEY";
        break;
    case 3:
        if (!priv) return NULL;
        ok = kncg_pkcs8(k, &d);
        label = "PRIVATE KEY";
        break;
    case 4:
        if (k->type != KK_EC || !priv) return NULL;
        ok = kncg_ec_sec1(k, 1, &d);
        label = "EC PRIVATE KEY";
        break;
    default:
        if (priv) return NULL;
        ok = kncg_spki(k, &d);
        label = "PUBLIC KEY";
        break;
    }
    if (!ok) {
        free(d.buf);
        return NULL;
    }
    if (format == 1) {
        *outn = d.len;
        return d.buf;
    }
    kncg_pem_append(&out, label, d.buf, d.len);
    memset(d.buf, 0, d.len);
    free(d.buf);
    *outn = out.len;
    kml_der_put(&out, (const unsigned char *)"", 1);
    return out.buf;
}

/* The canonical PEM: PKCS#8 for a private key, SPKI for a public one. */
static char *kncg_key_pem(const kncg_key *k, int priv) {
    size_t n;
    return (char *)kncg_key_encode(k, priv, priv ? 3 : 2, 0, &n);
}

/* ---- key pairs ---- */

static ll kncg_curve_by_name(const char *name) {
    if (!name) return 0;
    if (!kncg_casecmp(name, "P-256") || !strcmp(name, "prime256v1") || !strcmp(name, "secp256r1")) return 1;
    if (!kncg_casecmp(name, "P-384") || !strcmp(name, "secp384r1")) return 2;
    if (!kncg_casecmp(name, "P-521") || !strcmp(name, "secp521r1")) return 3;
    return 0;
}

/* The PEM pair (public SPKI then private PKCS#8), malloc'd, or NULL with the
 * thread's error text set where libcrypto would set one. */
static char *kncg_keygen_pem(double type, double bits, double exponent, const char *curve) {
    kncg_key k;
    int t = (int)type;
    memset(&k, 0, sizeof k);
    if (t == 0) {
        if (bits < 512) {
            kncg_set_err(KNCG_E_KEY_SMALL);
            return NULL;
        }
        /* CNG generates only the F4 exponent. */
        if (exponent > 0 && exponent != 65537) return NULL;
        if (!kncg_gen_rsa_key((ll)bits, &k)) return NULL;
    } else if (t == 1) {
        ll cv = kncg_curve_by_name(curve);
        if (!cv) {
            kncg_set_err(KNCG_E_UNKNOWN_OBJ);
            return NULL;
        }
        if (!kncg_gen_ec_key(cv, &k)) return NULL;
    } else if (t == 2 || t == 3) {
        k.type = t == 2 ? KK_ED25519 : KK_X25519;
        k.priv = 1;
        if (!kncg_random(k.raw, 32)) return NULL;
    } else {
        /* RSA-PSS, DSA, Ed448/X448: no CNG implementation here. */
        return NULL;
    }
    kml_der d = {0};
    size_t n;
    unsigned char *spki, *pkcs8;
    if (k.type == KK_ED25519 || k.type == KK_X25519) {
        /* The public half rides in raw only once the private half is out. */
        kncg_key pub = k;
        static const unsigned char nine[32] = {9};
        pub.priv = 0;
        if (k.type == KK_ED25519) kncg_ed_pub(k.raw, pub.raw);
        else kncg_x25519(pub.raw, k.raw, nine);
        spki = kncg_key_encode(&pub, 0, 2, 1, &n);
        if (spki) kncg_pem_append(&d, "PUBLIC KEY", spki, n);
    } else {
        spki = kncg_key_encode(&k, 0, 2, 1, &n);
        if (spki) kncg_pem_append(&d, "PUBLIC KEY", spki, n);
    }
    pkcs8 = kncg_key_encode(&k, 1, 3, 1, &n);
    if (pkcs8) {
        kncg_pem_append(&d, "PRIVATE KEY", pkcs8, n);
        memset(pkcs8, 0, n);
    }
    kml_der_put(&d, (const unsigned char *)"", 1);
    int ok = spki && pkcs8;
    free(spki);
    free(pkcs8);
    kncg_key_free(&k);
    if (!ok) {
        free(d.buf);
        return NULL;
    }
    return (char *)d.buf;
}

char *__kml_native_crypto_keygen(double type, double bits, double exponent, const char *curve) {
    char *pem = kncg_keygen_pem(type, bits, exponent, curve);
    char *out = kncg_str(pem ? pem : "");
    free(pem);
    return out;
}

typedef struct { double type, bits, exponent; char curve[64]; } kncg_keygen_job;

static void kncg_keygen_work(void *job, long long *err, double *res) {
    kncg_keygen_job *j = (kncg_keygen_job *)job;
    char *pem = kncg_keygen_pem(j->type, j->bits, j->exponent, j->curve);
    *err = pem ? 0 : 1;
    *res = (double)(intptr_t)pem;
    free(j);
}

void __kml_native_crypto_keygen_async(double type, double bits, double exponent, const char *curve, void *inv, void *clo) {
    kncg_keygen_job *j = (kncg_keygen_job *)calloc(1, sizeof *j);
    j->type = type; j->bits = bits; j->exponent = exponent;
    snprintf(j->curve, sizeof j->curve, "%s", curve ? curve : "");
    __kml_pool_job(kncg_keygen_work, j, inv, clo);
}

char *__kml_native_crypto_keygen_take(double result) {
    char *pem = (char *)(intptr_t)result;
    char *out = kncg_str(pem ? pem : "");
    free(pem);
    return out;
}

/* ---- RSA over CNG's raw (BCRYPT_PAD_NONE) operation; the paddings are
 * EMSA-PKCS1-v1_5, EMSA-PSS, RSAES-OAEP and RSAES-PKCS1-v1_5 (RFC 8017),
 * done here so every libcrypto option (salt lengths, OAEP labels, raw
 * private encrypt / public decrypt) behaves as there. ---- */

#define KNCG_NO_SALT 1e9
#define KNCG_RSA_PKCS1 1
#define KNCG_RSA_NONE 3
#define KNCG_RSA_OAEP 4
#define KNCG_RSA_PSS 6

static const unsigned char kncg_digestinfo[KD_COUNT][19] = {
    {0x30, 0x20, 0x30, 0x0c, 0x06, 0x08, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x02, 0x05, 0x05, 0x00, 0x04, 0x10},
    {0x30, 0x21, 0x30, 0x09, 0x06, 0x05, 0x2b, 0x0e, 0x03, 0x02, 0x1a, 0x05, 0x00, 0x04, 0x14},
    {0x30, 0x2d, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x04, 0x05, 0x00, 0x04, 0x1c},
    {0x30, 0x31, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x01, 0x05, 0x00, 0x04, 0x20},
    {0x30, 0x41, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x02, 0x05, 0x00, 0x04, 0x30},
    {0x30, 0x51, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x03, 0x05, 0x00, 0x04, 0x40},
};
static const size_t kncg_digestinfo_len[KD_COUNT] = {18, 15, 19, 19, 19, 19};

/* The raw RSA primitive: in (k bytes, < n) → out (k bytes). */
static int kncg_rsa_raw(const kncg_key *k, int priv, const unsigned char *in, size_t inlen, unsigned char *out) {
    size_t kb = k->rsalen[KR_N];
    unsigned char *buf;
    ULONG moved = 0;
    int ok;
    if (inlen > kb) return 0;
    buf = (unsigned char *)calloc(1, kb);
    if (!buf) return 0;
    memcpy(buf + (kb - inlen), in, inlen);
    if (memcmp(buf, k->rsa[KR_N], kb) >= 0) {
        free(buf);
        return 0;
    }
    BCRYPT_KEY_HANDLE h = kncg_rsa_handle(k, priv);
    if (!h) {
        free(buf);
        return 0;
    }
    if (priv)
        ok = BCRYPT_SUCCESS(BCryptDecrypt(h, buf, (ULONG)kb, NULL, NULL, 0, out, (ULONG)kb, &moved, BCRYPT_PAD_NONE));
    else
        ok = BCRYPT_SUCCESS(BCryptEncrypt(h, buf, (ULONG)kb, NULL, NULL, 0, out, (ULONG)kb, &moved, BCRYPT_PAD_NONE));
    if (ok && moved < kb) {
        memmove(out + (kb - moved), out, moved);
        memset(out, 0, kb - moved);
    }
    BCryptDestroyKey(h);
    memset(buf, 0, kb);
    free(buf);
    return ok;
}

/* MGF1(seed) xor'd into out. */
static void kncg_mgf1_xor(int md, const unsigned char *seed, size_t seedlen, unsigned char *out, size_t n) {
    unsigned char t[64], c[4];
    size_t hl = kncg_md_len[md], done = 0;
    for (uint32_t ctr = 0; done < n; ctr++) {
        kncg_md_ctx h;
        c[0] = (unsigned char)(ctr >> 24); c[1] = (unsigned char)(ctr >> 16);
        c[2] = (unsigned char)(ctr >> 8); c[3] = (unsigned char)ctr;
        kncg_md_init(&h, md);
        kncg_md_update(&h, seed, (long long)seedlen);
        kncg_md_update(&h, c, 4);
        kncg_md_final(&h, t);
        for (size_t i = 0; i < hl && done < n; i++) out[done++] ^= t[i];
    }
}

/* EMSA-PSS-ENCODE into em (emLen bytes); salt length per libcrypto's
 * specials: -1 the digest's, -2/-3 (and unset) the maximum. */
static int kncg_pss_encode(int md, const unsigned char *mhash, int modBits, double saltLength, unsigned char *em,
                           size_t emLen) {
    size_t hl = kncg_md_len[md];
    long long sl;
    if (saltLength == -1) sl = (long long)hl;
    else if (saltLength == -2 || saltLength == -3 || saltLength == KNCG_NO_SALT) sl = (long long)emLen - (long long)hl - 2;
    else if (saltLength < 0) return 0;
    else sl = (long long)saltLength;
    if (sl < 0 || (long long)emLen < (long long)hl + sl + 2) return 0;
    unsigned char salt[1024], mp[8 + 64 + 1024], hh[64];
    if (sl > (long long)sizeof salt || !kncg_random(salt, (size_t)sl)) return 0;
    memset(mp, 0, 8);
    memcpy(mp + 8, mhash, hl);
    memcpy(mp + 8 + hl, salt, (size_t)sl);
    kncg_md_oneshot(md, mp, (long long)(8 + hl + (size_t)sl), hh);
    size_t dbLen = emLen - hl - 1;
    memset(em, 0, dbLen);
    em[dbLen - (size_t)sl - 1] = 0x01;
    memcpy(em + dbLen - (size_t)sl, salt, (size_t)sl);
    kncg_mgf1_xor(md, hh, hl, em, dbLen);
    em[0] &= (unsigned char)(0xff >> (8 * emLen - (size_t)(modBits - 1)));
    memcpy(em + dbLen, hh, hl);
    em[emLen - 1] = 0xbc;
    return 1;
}

/* EMSA-PSS-VERIFY; -2 (and unset) recovers the salt length, as libcrypto's
 * auto does on verify. */
static int kncg_pss_verify(int md, const unsigned char *mhash, int modBits, double saltLength, const unsigned char *emIn,
                           size_t emLen) {
    size_t hl = kncg_md_len[md];
    unsigned char em[1024], mp[8 + 64 + 1024], hh[64];
    if (emLen > sizeof em || emLen < hl + 2 || emIn[emLen - 1] != 0xbc) return 0;
    memcpy(em, emIn, emLen);
    size_t dbLen = emLen - hl - 1;
    unsigned char topmask = (unsigned char)(0xff >> (8 * emLen - (size_t)(modBits - 1)));
    if (em[0] & ~topmask) return 0;
    kncg_mgf1_xor(md, em + dbLen, hl, em, dbLen);
    em[0] &= topmask;
    size_t i = 0;
    while (i < dbLen && em[i] == 0) i++;
    if (i == dbLen || em[i] != 0x01) return 0;
    size_t sl = dbLen - i - 1;
    if (saltLength == -1 && sl != hl) return 0;
    if (saltLength >= 0 && saltLength != KNCG_NO_SALT && (double)sl != saltLength) return 0;
    if (sl > 1024) return 0;
    memset(mp, 0, 8);
    memcpy(mp + 8, mhash, hl);
    memcpy(mp + 8 + hl, em + i + 1, sl);
    kncg_md_oneshot(md, mp, (long long)(8 + hl + sl), hh);
    return memcmp(hh, em + dbLen, hl) == 0;
}

/* The signature in out (2 * half bytes) as r || s, from CNG's ECDSA. */
static int kncg_ecdsa_sign_raw(const kncg_key *k, const unsigned char *hash, size_t hl, unsigned char *out) {
    BCRYPT_KEY_HANDLE h = kncg_ec_handle(k, 1, 0);
    ULONG n = 0;
    size_t cb = (size_t)kml_curve_bytes(k->curve);
    int ok = h && BCRYPT_SUCCESS(BCryptSignHash(h, NULL, (PUCHAR)hash, (ULONG)hl, out, (ULONG)(2 * cb), &n, 0)) &&
             n == 2 * cb;
    if (h) BCryptDestroyKey(h);
    return ok;
}

/* r || s → DER ECDSA-Sig-Value. */
static int kncg_rs_to_der(const unsigned char *rs, size_t half, kml_der *out) {
    kml_der in = {0};
    if (!kncg_der_uint(&in, rs, half) || !kncg_der_uint(&in, rs + half, half)) {
        free(in.buf);
        return 0;
    }
    return kncg_der_wrap(out, 0x30, &in);
}

/* DER ECDSA-Sig-Value → r || s (each half bytes); 0 when malformed. */
static int kncg_der_to_rs(const unsigned char *sig, size_t n, size_t half, unsigned char *rs) {
    const unsigned char *p = sig, *end = sig + n, *c, *v;
    unsigned char tag;
    size_t clen, vl;
    if (!kml_der_read(&p, end, &tag, &c, &clen) || tag != 0x30 || p != end) return 0;
    p = c;
    end = c + clen;
    memset(rs, 0, 2 * half);
    for (int i = 0; i < 2; i++) {
        if (!kml_der_read_uint(&p, end, &v, &vl) || vl > half) return 0;
        memcpy(rs + i * half + (half - vl), v, vl);
    }
    return p == end;
}

/* An RSA/EC signature over data's digest into out (cap outLen): its length,
 * -2 - n when out is too small, -1 on failure. */
static double kncg_sign_key(const kncg_key *k, const char *digest, double padding, double saltLength,
                            double dsaEncoding, const unsigned char *data, long long len, unsigned char *out,
                            long long outLen) {
    int md = kncg_md(digest);
    unsigned char mh[64];
    if (md < 0) md = KD_SHA256;
    kncg_md_oneshot(md, data, len, mh);
    size_t hl = kncg_md_len[md];
    if (k->type == KK_EC) {
        size_t cb = (size_t)kml_curve_bytes(k->curve);
        unsigned char rs[132];
        if (!kncg_ecdsa_sign_raw(k, mh, hl, rs)) return -1;
        if (dsaEncoding == 1) {
            if ((long long)(2 * cb) > outLen) return -2 - (double)(2 * cb);
            memcpy(out, rs, 2 * cb);
            return (double)(2 * cb);
        }
        kml_der d = {0};
        if (!kncg_rs_to_der(rs, cb, &d)) return -1;
        double r;
        if ((long long)d.len > outLen) {
            r = -2 - (double)d.len;
        } else {
            memcpy(out, d.buf, d.len);
            r = (double)d.len;
        }
        free(d.buf);
        return r;
    }
    /* RSA */
    size_t kb = k->rsalen[KR_N];
    int bits = kncg_rsa_bits(k);
    int pad = padding < 0 ? KNCG_RSA_PKCS1 : (int)padding;
    unsigned char *em = (unsigned char *)calloc(1, kb ? kb : 1);
    int ok = 0;
    if (!em) return -1;
    if (pad == KNCG_RSA_PKCS1) {
        size_t tl = kncg_digestinfo_len[md] + hl;
        if (kb >= tl + 11) {
            em[0] = 0;
            em[1] = 1;
            memset(em + 2, 0xff, kb - tl - 3);
            em[kb - tl - 1] = 0;
            memcpy(em + kb - tl, kncg_digestinfo[md], kncg_digestinfo_len[md]);
            memcpy(em + kb - hl, mh, hl);
            ok = 1;
        }
    } else if (pad == KNCG_RSA_PSS) {
        size_t emLen = (size_t)(bits - 1 + 7) / 8;
        ok = kncg_pss_encode(md, mh, bits, saltLength, em + (kb - emLen), emLen);
    }
    double r = -1;
    if (ok) {
        if ((long long)kb > outLen) {
            r = -2 - (double)kb;
        } else if (kncg_rsa_raw(k, 1, em, kb, out)) {
            r = (double)kb;
        }
    }
    memset(em, 0, kb);
    free(em);
    return r;
}

static double kncg_verify_key(const kncg_key *k, const char *digest, double padding, double saltLength,
                              double dsaEncoding, const unsigned char *data, long long len, const unsigned char *sig,
                              long long sigLen) {
    int md = kncg_md(digest);
    unsigned char mh[64];
    if (md < 0) md = KD_SHA256;
    kncg_md_oneshot(md, data, len, mh);
    size_t hl = kncg_md_len[md];
    if (k->type == KK_EC) {
        size_t cb = (size_t)kml_curve_bytes(k->curve);
        unsigned char rs[132];
        if (dsaEncoding == 1) {
            if (sigLen != (long long)(2 * cb)) return 0;
            memcpy(rs, sig, 2 * cb);
        } else if (!kncg_der_to_rs(sig, (size_t)sigLen, cb, rs)) {
            return 0;
        }
        BCRYPT_KEY_HANDLE h = kncg_ec_handle(k, 0, 0);
        int ok = h && BCRYPT_SUCCESS(BCryptVerifySignature(h, NULL, mh, (ULONG)hl, rs, (ULONG)(2 * cb), 0));
        if (h) BCryptDestroyKey(h);
        return ok ? 1 : 0;
    }
    size_t kb = k->rsalen[KR_N];
    int bits = kncg_rsa_bits(k);
    int pad = padding < 0 ? KNCG_RSA_PKCS1 : (int)padding;
    if (sigLen != (long long)kb) return 0;
    unsigned char *em = (unsigned char *)calloc(1, kb);
    double r = 0;
    if (em && kncg_rsa_raw(k, 0, sig, kb, em)) {
        if (pad == KNCG_RSA_PKCS1) {
            size_t tl = kncg_digestinfo_len[md] + hl;
            int good = kb >= tl + 11 && em[0] == 0 && em[1] == 1 && em[kb - tl - 1] == 0;
            for (size_t i = 2; good && i < kb - tl - 1; i++) good = em[i] == 0xff;
            good = good && memcmp(em + kb - tl, kncg_digestinfo[md], kncg_digestinfo_len[md]) == 0 &&
                   memcmp(em + kb - hl, mh, hl) == 0;
            r = good ? 1 : 0;
        } else if (pad == KNCG_RSA_PSS) {
            size_t emLen = (size_t)(bits - 1 + 7) / 8;
            int good = 1;
            for (size_t i = 0; i < kb - emLen; i++) good = good && em[i] == 0;
            r = good && kncg_pss_verify(md, mh, bits, saltLength, em + (kb - emLen), emLen) ? 1 : 0;
        }
    }
    free(em);
    return r;
}

double __kml_native_crypto_sign(const char *digest, const char *pem, void *pass, long long passLen, double hasPass,
                                double padding, double saltLength, double dsaEncoding,
                                void *data, long long len, void *out, long long outLen) {
    kncg_key k;
    if (!kncg_parse_key(pem, (const unsigned char *)pass, hasPass != 0 ? passLen : -1, &k)) {
        if (!kncg_err[0]) kncg_set_err(KNCG_E_DECODER);
        return -1;
    }
    double r = -1;
    if (!k.priv) {
        kncg_set_err(KNCG_E_DECODER);
    } else if (k.type == KK_ED25519) {
        /* A one-shot key: a digest name is an error, as in libcrypto. */
        if (digest && *digest) {
            kncg_set_err(KNCG_E_INVALID_DIGEST);
        } else if (outLen < 64) {
            r = -2 - 64.0;
        } else {
            kncg_ed_sign(k.raw, (const unsigned char *)data, len, (unsigned char *)out);
            r = 64;
        }
    } else if (k.type == KK_X25519) {
        kncg_set_err(KNCG_E_KEYTYPE);
    } else {
        r = kncg_sign_key(&k, digest, padding, saltLength, dsaEncoding, (const unsigned char *)data, len,
                          (unsigned char *)out, outLen);
    }
    kncg_key_free(&k);
    return r;
}

double __kml_native_crypto_verify(const char *digest, const char *pem, void *pass, long long passLen, double hasPass,
                                  double padding, double saltLength, double dsaEncoding,
                                  void *data, long long len, void *sig, long long sigLen) {
    kncg_key k;
    if (!kncg_parse_key(pem, (const unsigned char *)pass, hasPass != 0 ? passLen : -1, &k)) {
        if (!kncg_err[0]) kncg_set_err(KNCG_E_DECODER);
        return -1;
    }
    double r = 0;
    if (k.type == KK_ED25519) {
        unsigned char pub[32];
        if (k.priv) kncg_ed_pub(k.raw, pub);
        else memcpy(pub, k.raw, 32);
        if (!(digest && *digest))
            r = kncg_ed_verify(pub, (const unsigned char *)data, len, (const unsigned char *)sig, sigLen) ? 1 : 0;
    } else if (k.type == KK_RSA || k.type == KK_EC) {
        r = kncg_verify_key(&k, digest, padding, saltLength, dsaEncoding, (const unsigned char *)data, len,
                            (const unsigned char *)sig, sigLen);
    }
    kncg_key_free(&k);
    kncg_err[0] = 0;
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
    char *out = kncg_str(kncg_err);
    kncg_err[0] = 0;
    return out;
}

/* ── KeyObject: parse any input form into the canonical PEM, export it in
 * Node's encodings, describe it, and convert JWKs (all over the mini-DER;
 * an encrypted export is the one form this backend does not write). ───── */

/* Decodes a key: format 0 PEM, 1 DER; type 0 any, 1 pkcs1, 2 spki, 3 pkcs8,
 * 4 sec1. passLen < 0: no passphrase. */
static int kncg_decode_key(const unsigned char *data, long long len, int format, int type, const unsigned char *pass,
                           long long passLen, kncg_key *k) {
    memset(k, 0, sizeof *k);
    if (format != 1) {
        char *pem = (char *)malloc((size_t)len + 1);
        memcpy(pem, data, (size_t)len);
        pem[len] = 0;
        int ok = kncg_parse_key(pem, pass, passLen, k);
        free(pem);
        return ok;
    }
    static const char *const any[] = {"PRIVATE KEY", "PUBLIC KEY", "RSA PRIVATE KEY", "RSA PUBLIC KEY", "EC PRIVATE KEY"};
    const char *const *labels = any;
    size_t nl = 5;
    static const char *const pkcs1[] = {"RSA PRIVATE KEY", "RSA PUBLIC KEY"};
    static const char *const spki[] = {"PUBLIC KEY"};
    static const char *const pkcs8[] = {"PRIVATE KEY"};
    static const char *const sec1[] = {"EC PRIVATE KEY"};
    switch (type) {
    case 1: labels = pkcs1; nl = 2; break;
    case 2: labels = spki; nl = 1; break;
    case 3: labels = pkcs8; nl = 1; break;
    case 4: labels = sec1; nl = 1; break;
    }
    for (size_t i = 0; i < nl; i++) {
        if (kncg_parse_der(labels[i], data, (size_t)len, k)) return 1;
        kncg_key_free(k);
    }
    /* An EncryptedPrivateKeyInfo. */
    if (type == 0 || type == 3) {
        unsigned char *der = kncg_dup(data, len);
        size_t n = (size_t)len;
        int ok = kncg_decrypt_body("", &der, &n, pass, passLen) && kncg_parse_der("PRIVATE KEY", der, n, k);
        free(der);
        if (!ok) kncg_key_free(k);
        return ok;
    }
    return 0;
}

/* createPrivateKey / createPublicKey: the key's canonical PEM, or "" when
 * the input is no such key. A public key may come from a private one. */
char *__kml_native_crypto_key_parse(void *data, long long len, double format, double type, double wantPriv,
                                    void *pass, long long passLen, double hasPass) {
    kncg_key k;
    char *pem = NULL;
    if (kncg_decode_key((const unsigned char *)data, len, (int)format, (int)type, (const unsigned char *)pass,
                        hasPass != 0 ? passLen : -1, &k)) {
        pem = kncg_key_pem(&k, wantPriv != 0);
        kncg_key_free(&k);
    }
    char *out = kncg_str(pem ? pem : "");
    free(pem);
    return out;
}

/* keyObject.export({ format, type, cipher, passphrase }) into out: the
 * length, -2 - n when out is too small, -1 when the key cannot be encoded
 * so (including any cipher: PBES2 encryption is not written here). */
double __kml_native_crypto_key_export(const char *pem, double priv, double format, double type, const char *cipher,
                                      void *pass, long long passLen, double hasPass, void *out, long long outLen) {
    (void)pass; (void)passLen; (void)hasPass;
    kncg_key k;
    if (cipher && *cipher) return -1;
    if (!kncg_parse_key(pem, NULL, -1, &k)) return -1;
    size_t n = 0;
    unsigned char *buf = kncg_key_encode(&k, priv != 0, (int)type, (int)format, &n);
    kncg_key_free(&k);
    if (!buf) return -1;
    double r;
    if ((long long)n > outLen) {
        r = -2 - (double)n;
    } else {
        memcpy(out, buf, n);
        r = (double)n;
    }
    memset(buf, 0, n);
    free(buf);
    return r;
}

/* A big-endian magnitude in decimal. */
static void kncg_decimal(const unsigned char *p, size_t n, char *out, size_t cap) {
    unsigned char *v = (unsigned char *)malloc(n ? n : 1);
    char *digits = (char *)malloc(n * 3 + 2);
    size_t nd = 0;
    memcpy(v, p, n);
    for (;;) {
        int nonzero = 0;
        unsigned rem = 0;
        for (size_t i = 0; i < n; i++) {
            unsigned cur = rem << 8 | v[i];
            v[i] = (unsigned char)(cur / 10);
            rem = cur % 10;
            if (v[i]) nonzero = 1;
        }
        digits[nd++] = (char)('0' + rem);
        if (!nonzero) break;
    }
    size_t o = 0;
    while (nd > 0 && o + 1 < cap) out[o++] = digits[--nd];
    out[o] = 0;
    free(v);
    free(digits);
}

/* asymmetricKeyType and asymmetricKeyDetails, tab-separated: type,
 * modulusLength, publicExponent (decimal), namedCurve, hashAlgorithm,
 * mgf1HashAlgorithm, saltLength, divisorLength — empty where absent. "" for
 * a key that does not parse. */
char *__kml_native_crypto_key_info(const char *pem) {
    kncg_key k;
    if (!kncg_parse_key(pem, NULL, -1, &k)) return kncg_str("");
    char bits[24] = "", exp[1100] = "", buf[1300];
    const char *type = "", *curve = "";
    switch (k.type) {
    case KK_RSA:
        type = "rsa";
        snprintf(bits, sizeof bits, "%d", kncg_rsa_bits(&k));
        kncg_decimal(k.rsa[KR_E], k.rsalen[KR_E], exp, sizeof exp);
        break;
    case KK_EC:
        type = "ec";
        curve = k.curve == 1 ? "prime256v1" : k.curve == 2 ? "secp384r1" : "secp521r1";
        break;
    case KK_ED25519: type = "ed25519"; break;
    case KK_X25519: type = "x25519"; break;
    }
    snprintf(buf, sizeof buf, "%s\t%s\t%s\t%s\t\t\t\t", type, bits, exp, curve);
    kncg_key_free(&k);
    return kncg_str(buf);
}

static void kncg_append(char **buf, size_t *len, const char *key, const char *val) {
    if (!val) return;
    size_t add = strlen(key) + strlen(val) + 2;
    *buf = (char *)realloc(*buf, *len + add + 1);
    *len += (size_t)snprintf(*buf + *len, add + 1, "%s%s=%s", *len ? "\t" : "", key, val);
}

static void kncg_append_b64u(char **buf, size_t *len, const char *key, const unsigned char *p, size_t n) {
    static const unsigned char zero = 0;
    char *v = n ? kncg_b64u_encode(p, n) : kncg_b64u_encode(&zero, 1);
    kncg_append(buf, len, key, v);
    free(v);
}

/* keyObject.export({ format: 'jwk' }): the members in Node's order,
 * tab-separated name=value; "!type" for a key type JWK has no form for. */
char *__kml_native_crypto_key_jwk(const char *pem, double priv) {
    kncg_key k;
    char *buf = NULL;
    size_t len = 0;
    const char *err = NULL;
    if (!kncg_parse_key(pem, NULL, -1, &k)) return kncg_str("!type");
    int wantPriv = priv != 0 && k.priv;
    if (k.type == KK_RSA) {
        static const char *names[8] = {"n", "e", "d", "p", "q", "dp", "dq", "qi"};
        kncg_append(&buf, &len, "kty", "RSA");
        for (int i = 0; i < (wantPriv ? 8 : 2); i++) kncg_append_b64u(&buf, &len, names[i], k.rsa[i], k.rsalen[i]);
    } else if (k.type == KK_EC) {
        size_t cb = (size_t)kml_curve_bytes(k.curve);
        kncg_append(&buf, &len, "kty", "EC");
        kncg_append_b64u(&buf, &len, "x", k.x, cb);
        kncg_append_b64u(&buf, &len, "y", k.y, cb);
        kncg_append(&buf, &len, "crv", k.curve == 1 ? "P-256" : k.curve == 2 ? "P-384" : "P-521");
        if (wantPriv) kncg_append_b64u(&buf, &len, "d", k.d, cb);
    } else {
        unsigned char pub[32];
        static const unsigned char nine[32] = {9};
        kncg_append(&buf, &len, "crv", k.type == KK_ED25519 ? "Ed25519" : "X25519");
        if (wantPriv) kncg_append_b64u(&buf, &len, "d", k.raw, 32);
        if (!k.priv) memcpy(pub, k.raw, 32);
        else if (k.type == KK_ED25519) kncg_ed_pub(k.raw, pub);
        else kncg_x25519(pub, k.raw, nine);
        kncg_append_b64u(&buf, &len, "x", pub, 32);
        kncg_append(&buf, &len, "kty", "OKP");
    }
    kncg_key_free(&k);
    char *out = kncg_str(err ? err : buf ? buf : "");
    free(buf);
    return out;
}

/* createPrivateKey/createPublicKey({ key: jwk, format: 'jwk' }): the key's
 * canonical PEM, or "" when the members describe no such key. kty 0 RSA,
 * 1 EC, 2 OKP. A private key needs d. */
char *__kml_native_crypto_key_from_jwk(double kty, const char *crv, const char *n, const char *e, const char *d,
                                       const char *p, const char *q, const char *dp, const char *dq, const char *qi,
                                       const char *x, const char *y, double wantPriv) {
#define KNCG_OPT(s) ((s) && *(s) ? (s) : NULL)
    kncg_key k;
    int priv = wantPriv != 0, ok = 0;
    char *pem = NULL;
    memset(&k, 0, sizeof k);
    if (priv && !KNCG_OPT(d)) return kncg_str("");
    if ((int)kty == 0) {
        const char *vals[8] = {n, e, d, p, q, dp, dq, qi};
        k.type = KK_RSA;
        k.priv = priv;
        ok = 1;
        for (int i = 0; i < (priv ? 8 : 2) && ok; i++) {
            size_t bl = 0;
            unsigned char *b = KNCG_OPT(vals[i]) ? kncg_b64u_decode(vals[i], &bl) : NULL;
            ok = b != NULL;
            if (b) {
                kncg_rsa_set(&k, i, b, bl);
                free(b);
            }
        }
        ok = ok && k.rsalen[KR_N] > 0 && k.rsalen[KR_E] > 0;
    } else if ((int)kty == 1) {
        ll id = !strcmp(crv, "P-256") ? 1 : !strcmp(crv, "P-384") ? 2 : !strcmp(crv, "P-521") ? 3 : 0;
        size_t cb = (size_t)kml_curve_bytes(id), xl = 0, yl = 0, dl = 0;
        unsigned char *xb = id && KNCG_OPT(x) ? kncg_b64u_decode(x, &xl) : NULL;
        unsigned char *yb = id && KNCG_OPT(y) ? kncg_b64u_decode(y, &yl) : NULL;
        unsigned char *db = id && priv ? kncg_b64u_decode(d, &dl) : NULL;
        if (xb && yb && xl == cb && yl == cb && (!priv || (db && dl == cb))) {
            unsigned char pt[1 + 2 * 66];
            k.type = KK_EC;
            k.curve = id;
            k.priv = priv;
            pt[0] = 4;
            memcpy(pt + 1, xb, cb);
            memcpy(pt + 1 + cb, yb, cb);
            ok = kncg_ec_point(id, pt, 1 + 2 * cb, k.x, k.y);
            if (ok && priv) {
                unsigned char cx[66], cy[66];
                memcpy(k.d, db, cb);
                /* The private scalar must match the public point. */
                ok = kncg_ec_pub_from_d(id, k.d, cx, cy) && !memcmp(cx, k.x, cb) && !memcmp(cy, k.y, cb);
            }
        }
        free(xb); free(yb);
        if (db) { memset(db, 0, dl); free(db); }
    } else if ((int)kty == 2) {
        int t = !strcmp(crv, "Ed25519") ? KK_ED25519 : !strcmp(crv, "X25519") ? KK_X25519 : -1;
        const char *src = priv ? d : x;
        size_t rl = 0;
        unsigned char *raw = t >= 0 && KNCG_OPT(src) ? kncg_b64u_decode(src, &rl) : NULL;
        if (raw && rl == 32) {
            k.type = t;
            k.priv = priv;
            memcpy(k.raw, raw, 32);
            ok = 1;
        }
        free(raw);
    }
#undef KNCG_OPT
    if (ok) pem = kncg_key_pem(&k, priv);
    kncg_key_free(&k);
    char *out = kncg_str(pem ? pem : "");
    free(pem);
    return out;
}

/* publicEncrypt (op 0), privateDecrypt (1), privateEncrypt (2) and
 * publicDecrypt (3): the result's length in out, -2 - n when out is too
 * small, -1 on failure, -3 for an unknown OAEP digest. */
double __kml_native_crypto_pkey_crypt(double op, const char *pem, void *pass, long long passLen, double hasPass,
                                      double padding, const char *oaepHash, void *label, long long labelLen,
                                      void *data, long long len, void *out, long long outLen) {
    int o = (int)op, pad = (int)padding;
    kncg_key k;
    kncg_err[0] = 0;
    if (!kncg_parse_key(pem, (const unsigned char *)pass, hasPass != 0 ? passLen : -1, &k)) return -1;
    if (k.type != KK_RSA || ((o == 1 || o == 2) && !k.priv)) {
        kncg_set_err(KNCG_E_KEYTYPE);
        kncg_key_free(&k);
        return -1;
    }
    int md = KD_SHA1;
    if (pad == KNCG_RSA_OAEP) {
        md = kncg_md(oaepHash && *oaepHash ? oaepHash : "sha1");
        if (md < 0) {
            kncg_key_free(&k);
            return -3;
        }
    }
    size_t kb = k.rsalen[KR_N], hl = kncg_md_len[md], n = 0;
    const unsigned char *in = (const unsigned char *)data;
    unsigned char *em = (unsigned char *)calloc(1, kb), *res = (unsigned char *)calloc(1, kb);
    int ok = 0;
    if (!em || !res) goto done;
    if (o == 0 || o == 2) {
        /* Encode, then the public (0) or private (2) primitive. */
        if (pad == KNCG_RSA_NONE && o <= 2) {
            if ((size_t)len != kb) { kncg_set_err(KNCG_E_DATA_LARGE); goto done; }
            memcpy(em, in, kb);
        } else if (pad == KNCG_RSA_PKCS1) {
            if ((size_t)len + 11 > kb) { kncg_set_err(KNCG_E_DATA_LARGE); goto done; }
            size_t ps = kb - (size_t)len - 3;
            em[1] = o == 0 ? 2 : 1;
            if (o == 0) {
                if (!kncg_random(em + 2, ps)) goto done;
                for (size_t i = 0; i < ps; i++)
                    while (em[2 + i] == 0)
                        if (!kncg_random(em + 2 + i, 1)) goto done;
            } else {
                memset(em + 2, 0xff, ps);
            }
            memcpy(em + kb - (size_t)len, in, (size_t)len);
        } else if (pad == KNCG_RSA_OAEP && o == 0) {
            if (kb < 2 * hl + 2 || (size_t)len > kb - 2 * hl - 2) { kncg_set_err(KNCG_E_DATA_LARGE); goto done; }
            unsigned char *seed = em + 1, *db = em + 1 + hl;
            size_t dbLen = kb - hl - 1;
            kncg_md_oneshot(md, label, labelLen, db);
            db[dbLen - (size_t)len - 1] = 0x01;
            memcpy(db + dbLen - (size_t)len, in, (size_t)len);
            if (!kncg_random(seed, hl)) goto done;
            kncg_mgf1_xor(md, seed, hl, db, dbLen);
            kncg_mgf1_xor(md, db, dbLen, seed, hl);
        } else {
            goto done;
        }
        ok = kncg_rsa_raw(&k, o == 2, em, kb, res);
        n = kb;
    } else {
        /* The private (1) or public (3) primitive, then decode. */
        if ((size_t)len != kb || !kncg_rsa_raw(&k, o == 1, in, kb, em)) goto done;
        if (pad == KNCG_RSA_NONE) {
            memcpy(res, em, kb);
            n = kb;
            ok = 1;
        } else if (pad == KNCG_RSA_PKCS1) {
            size_t i = 2;
            int good = em[0] == 0 && em[1] == (o == 1 ? 2 : 1);
            if (o == 1) while (i < kb && em[i] != 0) i++;
            else while (i < kb && em[i] == 0xff) i++;
            good = good && i < kb && em[i] == 0 && i >= 10;
            if (good) {
                n = kb - i - 1;
                memcpy(res, em + i + 1, n);
                ok = 1;
            }
        } else if (pad == KNCG_RSA_OAEP && o == 1 && kb >= 2 * hl + 2) {
            unsigned char lh[64];
            unsigned char *seed = em + 1, *db = em + 1 + hl;
            size_t dbLen = kb - hl - 1, i = hl;
            kncg_md_oneshot(md, label, labelLen, lh);
            kncg_mgf1_xor(md, db, dbLen, seed, hl);
            kncg_mgf1_xor(md, seed, hl, db, dbLen);
            int good = em[0] == 0 && memcmp(db, lh, hl) == 0;
            while (i < dbLen && db[i] == 0) i++;
            good = good && i < dbLen && db[i] == 0x01;
            if (good) {
                n = dbLen - i - 1;
                memcpy(res, db + i + 1, n);
                ok = 1;
            } else {
                kncg_set_err(KNCG_E_OAEP);
            }
        }
    }
done:;
    double r = -1;
    if (ok) {
        if ((long long)n > outLen) {
            r = -2 - (double)n;
        } else {
            memcpy(out, res, n);
            r = (double)n;
        }
    }
    if (em) { memset(em, 0, kb); free(em); }
    if (res) { memset(res, 0, kb); free(res); }
    kncg_key_free(&k);
    return r;
}

/* ECDH (stateless, as the libcrypto backend): op 0 a new private key; 1 the
 * public point of priv in format (2 compressed, 4 uncompressed, 6 hybrid);
 * 2 the shared secret of priv and the peer point pub; 3 pub re-encoded in
 * format; 4 priv checked (0 valid). The result's length in out, -1 on
 * failure, -6 an invalid point, -7 an invalid private key, -8 an unknown
 * curve (CNG names only the three NIST prime curves). */
static ll kncg_ecdh_curve(const char *name) {
    if (!name) return 0;
    if (!strcmp(name, "prime256v1") || !strcmp(name, "P-256")) return 1;
    if (!strcmp(name, "secp384r1") || !strcmp(name, "P-384")) return 2;
    if (!strcmp(name, "secp521r1") || !strcmp(name, "P-521")) return 3;
    return 0;
}

/* The shared X coordinate of d and the peer point, via CNG's ECDH. CNG
 * hands back the raw secret little-endian; it is reversed here. */
static int kncg_ecdh_secret(ll curve, const unsigned char *d, const unsigned char *px, const unsigned char *py,
                            unsigned char *out) {
    size_t cb = (size_t)kml_curve_bytes(curve);
    BCRYPT_KEY_HANDLE priv = kncg_ec_raw_handle(curve, NULL, NULL, d, 1);
    BCRYPT_KEY_HANDLE pub = kncg_ec_raw_handle(curve, px, py, NULL, 1);
    BCRYPT_SECRET_HANDLE s = NULL;
    ULONG n = 0;
    unsigned char raw[66];
    int ok = priv && pub && BCRYPT_SUCCESS(BCryptSecretAgreement(priv, pub, &s, 0)) &&
             BCRYPT_SUCCESS(BCryptDeriveKey(s, BCRYPT_KDF_RAW_SECRET, NULL, raw, (ULONG)cb, &n, 0)) && n == cb;
    if (ok)
        for (size_t i = 0; i < cb; i++) out[i] = raw[cb - 1 - i];
    if (s) BCryptDestroySecret(s);
    if (priv) BCryptDestroyKey(priv);
    if (pub) BCryptDestroyKey(pub);
    memset(raw, 0, sizeof raw);
    return ok;
}

double __kml_native_crypto_ecdh(double op, const char *curve, void *priv, long long privLen, void *pub, long long pubLen,
                                double format, void *out, long long outLen) {
    ll cv = kncg_ecdh_curve(curve);
    if (!cv) return -8;
    size_t cb = (size_t)kml_curve_bytes(cv);
    int o = (int)op;
    unsigned char d[66], x[66], y[66];
    double r = -1;
    if (o == 0) {
        kncg_key k;
        if ((long long)cb > outLen || !kncg_gen_ec_key(cv, &k)) return -1;
        memcpy(out, k.d, cb);
        kncg_key_free(&k);
        return (double)cb;
    }
    if (o == 1 || o == 2 || o == 4) {
        const unsigned char *pp = (const unsigned char *)priv;
        size_t pl = privLen > 0 ? (size_t)privLen : 0;
        while (pl > 0 && pp[0] == 0) { pp++; pl--; }
        if (pl > cb) return -7;
        memset(d, 0, cb);
        memcpy(d + (cb - pl), pp, pl);
        if (!kncg_ec_priv_in_range(cv, d)) return -7;
        if (o == 4) return 0;
    }
    if (o == 1) {
        if (!kncg_ec_pub_from_d(cv, d, x, y)) goto done;
    } else if (!kncg_ec_point(cv, (const unsigned char *)pub, pubLen > 0 ? (size_t)pubLen : 0, x, y)) {
        r = -6;
        goto done;
    }
    if (o == 2) {
        if ((long long)cb <= outLen && kncg_ecdh_secret(cv, d, x, y, (unsigned char *)out)) r = (double)cb;
        goto done;
    }
    {
        unsigned char enc[1 + 2 * 66];
        size_t n = kncg_ec_encode(cv, x, y, (int)format, enc);
        if ((long long)n <= outLen) {
            memcpy(out, enc, n);
            r = (double)n;
        }
    }
done:
    memset(d, 0, sizeof d);
    return r;
}

/* crypto.diffieHellman({ privateKey, publicKey }): the shared secret of two
 * PEM keys in out; -2 - n when out is too small, -1 on failure. */
double __kml_native_crypto_derive_secret(const char *privPem, const char *pubPem, void *out, long long outLen) {
    kncg_key a, b;
    double r = -1;
    kncg_err[0] = 0;
    if (!kncg_parse_key(privPem, NULL, -1, &a)) return -1;
    if (!kncg_parse_key(pubPem, NULL, -1, &b)) {
        kncg_key_free(&a);
        return -1;
    }
    unsigned char s[66];
    size_t n = 0;
    if (a.priv && a.type == KK_EC && b.type == KK_EC && a.curve == b.curve) {
        n = (size_t)kml_curve_bytes(a.curve);
        if (!kncg_ecdh_secret(a.curve, a.d, b.x, b.y, s)) n = 0;
    } else if (a.priv && a.type == KK_X25519 && b.type == KK_X25519) {
        unsigned char peer[32];
        static const unsigned char nine[32] = {9}, zero[32] = {0};
        if (b.priv) kncg_x25519(peer, b.raw, nine);
        else memcpy(peer, b.raw, 32);
        kncg_x25519(s, a.raw, peer);
        n = memcmp(s, zero, 32) ? 32 : 0; /* an all-zero result is a small-order peer */
    } else {
        kncg_set_err(KNCG_E_KEYTYPE);
    }
    if (n) {
        if ((long long)n > outLen) {
            r = -2 - (double)n;
        } else {
            memcpy(out, s, n);
            r = (double)n;
        }
    }
    memset(s, 0, sizeof s);
    kncg_key_free(&a);
    kncg_key_free(&b);
    return r;
}
