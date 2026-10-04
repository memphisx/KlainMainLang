/* strheader.c — length-prefixed string buffers (TDD-00120 Stage 1; in C per
 * TDD-00240). Every heap string is [ i64 byteLength ][ bytes ][ \0 ] and the
 * string value points at the bytes (base+8). The retained NUL keeps strlen
 * consumers working; the header gives binary-safe lengths via ptr-8. */
#ifndef _WIN32
#define _GNU_SOURCE
#endif
#include <stdlib.h>
#include <string.h>

typedef long long i64;

#ifdef _WIN32
/* memmem is defined by the generated module on Windows (ensureMemmem). */
void *memmem(const void *hay, size_t hlen, const void *needle, size_t nlen);
#endif

/* The allocator, length read and free stay in the generated module, where
 * every call site inlines them. */
extern void *__kml_str_alloc(i64 n);
extern i64 __kml_str_len(const char *s);

void __kml_str_finalize(char *s) { *(i64 *)(s - 8) = (i64)strlen(s); }

void *__kml_str_from_cstr(const char *c) {
    if (!c) return NULL;
    i64 len = (i64)strlen(c);
    char *dst = (char *)__kml_str_alloc(len);
    memcpy(dst, c, (size_t)len + 1);
    return dst;
}

/* strcmp-shaped (<0/0/>0) lexicographic compare by header length + memcmp,
 * so an embedded NUL does not stop it early. */
int __kml_str_cmp(const char *a, const char *b) {
    i64 la = __kml_str_len(a), lb = __kml_str_len(b);
    int c = memcmp(a, b, (size_t)(la < lb ? la : lb));
    if (c != 0) return c;
    return la < lb ? -1 : la > lb ? 1 : 0;
}

_Bool __kml_str_startswith_at(const char *hay, const char *needle, i64 pos) {
    i64 lh = __kml_str_len(hay), ln = __kml_str_len(needle);
    i64 p = pos < 0 ? 0 : pos;
    if (p > lh) p = lh;
    if (p + ln > lh) return 0;
    return memcmp(hay + p, needle, (size_t)ln) == 0;
}

_Bool __kml_str_endswith_at(const char *hay, const char *needle, i64 endpos) {
    i64 lh = __kml_str_len(hay), ln = __kml_str_len(needle);
    i64 e = endpos < 0 ? 0 : endpos;
    if (e > lh) e = lh;
    i64 start = e - ln;
    if (start < 0) return 0;
    return memcmp(hay + start, needle, (size_t)ln) == 0;
}

static i64 last_from(const char *hay, const char *needle, i64 ln, i64 off) {
    for (; off >= 0; off--)
        if (memcmp(hay + off, needle, (size_t)ln) == 0) return off;
    return -1;
}

i64 __kml_str_lastindexof(const char *hay, const char *needle) {
    i64 ln = __kml_str_len(needle);
    return last_from(hay, needle, ln, __kml_str_len(hay) - ln);
}

/* start = min(from, len-needle); a negative from falls through to -1. */
i64 __kml_str_lastindexof_from(const char *hay, const char *needle, i64 from) {
    i64 ln = __kml_str_len(needle), maxstart = __kml_str_len(hay) - ln;
    return last_from(hay, needle, ln, from > maxstart ? maxstart : from);
}

/* argc+1 slots: the array is also handed to execv(), which needs the
 * trailing NULL. */
void *__kml_argv_headerize(i64 argc, char **argv) {
    void **arr = (void **)malloc((size_t)(argc + 1) * 8);
    arr[argc] = NULL;
    for (i64 i = 0; i < argc; i++) arr[i] = __kml_str_from_cstr(argv[i]);
    return arr;
}

/* The Node-shaped process.argv: [a[0], a[0], a[1], ... a[argc-1], NULL]. The
 * executable path sits at both argv[0] and argv[1], so user arguments start
 * at index 2. The raw argv keeps its OS shape for execv/fork. argc >= 1. */
void *__kml_argv_node_shape(i64 argc, void **hdr) {
    void **arr = (void **)malloc((size_t)(argc + 2) * 8);
    arr[argc + 1] = NULL;
    arr[0] = hdr[0];
    arr[1] = hdr[0];
    for (i64 i = 1; i < argc; i++) arr[i + 1] = hdr[i];
    return arr;
}
