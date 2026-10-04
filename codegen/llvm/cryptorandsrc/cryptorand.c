/* cryptorand.c — crypto.* randomness (in C per TDD-00240): a real CSPRNG
 * (arc4random_buf on the BSDs and macOS, getrandom() on Linux, the shim's
 * BCryptGenRandom wrapper on Windows), not the C89 rand() Math.random's
 * fallback uses. KML_CRYPTO_UUID adds randomUUID, which needs the string
 * runtime's __kml_str_alloc; KML_CRYPTO_FILL_ANY the fill of a value held in
 * `any`, which needs the dynamic-value runtime. */
#include <stdlib.h>
#if defined(__APPLE__) || defined(__FreeBSD__) || defined(__OpenBSD__) || defined(__NetBSD__) || defined(__DragonFly__)
#define KML_ARC4 1
#elif defined(_WIN32)
long long getrandom(void *buf, size_t n, unsigned flags);
#else
#include <sys/random.h>
#endif

typedef long long i64;

void __kml_crypto_random_bytes(void *buf, i64 n) {
#ifdef KML_ARC4
    arc4random_buf(buf, (size_t)n);
#else
    long long r = (long long)getrandom(buf, (size_t)n, 0);
    (void)r;
#endif
}

#ifdef KML_CRYPTO_FILL_ANY
/* crypto.getRandomValues on a value known only at run time: an integer
 * TypedArray (or Buffer) held in `any` is filled; 1 for any other value
 * (TypeMismatchError), 2 past the 65,536-byte quota (QuotaExceededError). */
extern i64 __kml_typed_int_view(i64 word, void **data);
i64 __kml_crypto_fill_any(i64 word) {
    void *data = 0;
    i64 n = __kml_typed_int_view(word, &data);
    if (n < 0) return 1;
    if (n > 65536) return 2;
    if (n > 0) __kml_crypto_random_bytes(data, n);
    return 0;
}
#endif

#ifdef KML_CRYPTO_UUID
extern char *__kml_str_alloc(i64 n);

/* RFC 4122 version 4: "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx". */
char *__kml_crypto_random_uuid(void) {
    unsigned char b[16];
    __kml_crypto_random_bytes(b, 16);
    b[6] = (unsigned char)((b[6] & 15) | 64);
    b[8] = (unsigned char)((b[8] & 63) | 128);
    static const char hex[] = "0123456789abcdef";
    char *out = __kml_str_alloc(36);
    int o = 0;
    for (int i = 0; i < 16; i++) {
        if (i == 4 || i == 6 || i == 8 || i == 10) out[o++] = '-';
        out[o++] = hex[b[i] >> 4];
        out[o++] = hex[b[i] & 15];
    }
    out[o] = 0;
    return out;
}
#endif
