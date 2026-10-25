/* strheader.c — length-prefixed string buffers (TDD-00120 Stage 1; in C per
 * TDD-00240). Every heap string is [ i64 byteLength ][ bytes ][ \0 ] and the
 * string value points at the bytes (base+8). The retained NUL keeps strlen
 * consumers working; the header gives binary-safe lengths via ptr-8. */
#ifndef _WIN32
#define _GNU_SOURCE
#endif
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <io.h>
#define write _write
#else
#include <unistd.h>
#endif

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

/* ---- UTF-16 indexing over UTF-8 bytes ----
 * A string holds UTF-8, with a lone surrogate as its own three-byte form
 * (WTF-8: ED A0..BF xx), and JavaScript indexes it by UTF-16 code units: a
 * character of four bytes is two units (a surrogate pair), every other
 * character one. The header caches the unit count (kml_heap.h); an ASCII
 * string's units are its bytes, so only other text walks. A walk resumes
 * from the last position this thread found in the same string, so reading
 * a string front to back stays linear. */

static _Thread_local struct { const char *s; i64 unit, byte; } u16_cur;

/* The bytes of the character a lead byte starts. */
static inline int u8_width(unsigned char b) { return b < 0x80 ? 1 : b < 0xE0 ? 2 : b < 0xF0 ? 3 : 4; }

i64 __kml_str_u16len_slow(const char *s) {
    i64 n = KML_STR_LEN(s), u = 0;
    const unsigned char *p = (const unsigned char *)s;
    for (i64 i = 0; i < n; i++) {
        unsigned char b = p[i];
        u += (b & 0xC0) != 0x80; /* a lead byte starts a unit */
        u += b >= 0xF0;          /* a four-byte one starts two */
    }
    if (u16_cur.s == s) u16_cur.s = NULL; /* a new string at a reused address */
    if (u < (1LL << 30) - 1) *(i64 *)(s - 8) = n | ((u + 1) << 33);
    return u;
}

/* The byte offset of UTF-16 unit idx (0 <= idx <= the unit count). *mid is
 * set when idx is the second unit of a four-byte character, whose start
 * the offset then is. */
i64 __kml_u16_off(const char *s, i64 idx, int *mid) {
    *mid = 0;
    if (kml_str_ascii(s)) return idx;
    const unsigned char *p = (const unsigned char *)s;
    i64 n = KML_STR_LEN(s), k = 0, i = 0;
    if (u16_cur.s == s && u16_cur.unit <= idx) {
        k = u16_cur.unit;
        i = u16_cur.byte;
    }
    while (k < idx && i < n) {
        int w = u8_width(p[i]);
        if (w == 4) {
            if (k + 1 == idx) {
                *mid = 1;
                return i;
            }
            k += 2;
        } else {
            k += 1;
        }
        i += w;
    }
    u16_cur.s = s;
    u16_cur.unit = k;
    u16_cur.byte = i;
    return i;
}

/* The UTF-16 index of byte offset off (a character's start, or the end). */
i64 __kml_u16_index(const char *s, i64 off) {
    if (kml_str_ascii(s)) return off;
    const unsigned char *p = (const unsigned char *)s;
    i64 k = 0, i = 0;
    if (u16_cur.s == s && u16_cur.byte <= off) {
        k = u16_cur.unit;
        i = u16_cur.byte;
    }
    while (i < off) {
        int w = u8_width(p[i]);
        k += w == 4 ? 2 : 1;
        i += w;
    }
    u16_cur.s = s;
    u16_cur.unit = k;
    u16_cur.byte = i;
    return k;
}

/* The code point of the character at byte offset i. */
static unsigned u8_decode(const unsigned char *p, int w) {
    switch (w) {
    case 1: return p[0];
    case 2: return (unsigned)(p[0] & 0x1F) << 6 | (p[1] & 0x3F);
    case 3: return (unsigned)(p[0] & 0x0F) << 12 | (unsigned)(p[1] & 0x3F) << 6 | (p[2] & 0x3F);
    default: return (unsigned)(p[0] & 0x07) << 18 | (unsigned)(p[1] & 0x3F) << 12 | (unsigned)(p[2] & 0x3F) << 6 | (p[3] & 0x3F);
    }
}

/* The UTF-16 code unit at idx (0 <= idx < the unit count). */
unsigned __kml_u16_unit_at(const char *s, i64 idx) {
    int mid;
    i64 o = __kml_u16_off(s, idx, &mid);
    const unsigned char *p = (const unsigned char *)s + o;
    int w = u8_width(p[0]);
    unsigned cp = u8_decode(p, w);
    if (w < 4) return cp;
    cp -= 0x10000;
    return mid ? 0xDC00 + (cp & 0x3FF) : 0xD800 + (cp >> 10);
}

/* The code point at unit idx, as codePointAt reads it: a pair's whole
 * code point at its first unit, a lone or second surrogate's own unit. */
unsigned __kml_u16_code_point_at(const char *s, i64 idx) {
    int mid;
    i64 o = __kml_u16_off(s, idx, &mid);
    const unsigned char *p = (const unsigned char *)s + o;
    int w = u8_width(p[0]);
    unsigned cp = u8_decode(p, w);
    if (w == 4 && mid) return 0xDC00 + ((cp - 0x10000) & 0x3FF);
    return cp;
}

/* A surrogate unit's three WTF-8 bytes. */
static void put_surrogate(char *o, unsigned u) {
    o[0] = (char)(0xE0 | (u >> 12));
    o[1] = (char)(0x80 | ((u >> 6) & 0x3F));
    o[2] = (char)(0x80 | (u & 0x3F));
}

/* The units [a, b) of s as a new string (0 <= a <= b <= the unit count).
 * A bound inside a surrogate pair keeps that half as a lone surrogate. */
char *__kml_u16_sub(const char *s, i64 a, i64 b) {
    if (a >= b) {
        char *e = (char *)__kml_str_alloc(0);
        e[0] = 0;
        return e;
    }
    int ma, mb;
    i64 oa = __kml_u16_off(s, a, &ma);
    i64 ob = __kml_u16_off(s, b, &mb);
    const unsigned char *p = (const unsigned char *)s;
    i64 start = ma ? oa + 4 : oa, end = ob;
    i64 n = (ma ? 3 : 0) + (end > start ? end - start : 0) + (mb ? 3 : 0);
    char *out = (char *)__kml_str_alloc(n), *w = out;
    if (ma) {
        unsigned cp = u8_decode(p + oa, 4) - 0x10000;
        put_surrogate(w, 0xDC00 + (cp & 0x3FF));
        w += 3;
    }
    if (end > start) {
        memcpy(w, s + start, (size_t)(end - start));
        w += end - start;
    }
    if (mb) {
        unsigned cp = u8_decode(p + ob, 4) - 0x10000;
        put_surrogate(w, 0xD800 + (cp >> 10));
        w += 3;
    }
    *w = 0;
    return out;
}

/* Joins a high surrogate ending s's first `at` bytes with a low surrogate
 * starting the rest into the pair's four-byte character, in place (s was
 * just built). A string never holds a pair as two surrogates, so equal
 * text is equal bytes. */
void __kml_wtf8_join(char *s, i64 at);

/* a + b, a new string: a's bytes then b's, a surrogate pair split between
 * them joined (WTF-8, when b starts with a surrogate's lead byte). What
 * every string `+` calls. */
char *__kml_str_concat(const char *a, const char *b) {
    i64 n1 = __kml_str_len(a), n2 = __kml_str_len(b);
    char *out = (char *)__kml_str_alloc(n1 + n2);
    memcpy(out, a, (size_t)n1);
    memcpy(out + n1, b, (size_t)n2 + 1);
    if (__builtin_expect((unsigned char)out[n1] == 0xED, 0)) __kml_wtf8_join(out, n1);
    return out;
}

void __kml_wtf8_join(char *s, i64 at) {
    unsigned char *p = (unsigned char *)s;
    i64 n = KML_STR_LEN(s);
    if (at < 3 || at + 3 > n) return;
    if (!(p[at - 3] == 0xED && p[at - 2] >= 0xA0 && p[at - 2] <= 0xAF)) return;
    if (!(p[at] == 0xED && p[at + 1] >= 0xB0)) return;
    unsigned hi = 0xD000 | (unsigned)(p[at - 2] & 0x3F) << 6 | (p[at - 1] & 0x3F);
    unsigned lo = 0xD000 | (unsigned)(p[at + 1] & 0x3F) << 6 | (p[at + 2] & 0x3F);
    unsigned cp = 0x10000 + ((hi - 0xD800) << 10) + (lo - 0xDC00);
    unsigned char *o = p + at - 3;
    o[0] = (unsigned char)(0xF0 | (cp >> 18));
    o[1] = (unsigned char)(0x80 | ((cp >> 12) & 0x3F));
    o[2] = (unsigned char)(0x80 | ((cp >> 6) & 0x3F));
    o[3] = (unsigned char)(0x80 | (cp & 0x3F));
    memmove(o + 4, p + at + 3, (size_t)(n - at - 3) + 1);
    *(i64 *)(s - 8) = n - 2;
}

/* A string's n bytes copied to p, made UTF-8: each lone surrogate becomes
 * U+FFFD, the same three bytes long, as Node's encoder writes it. */
void __kml_wtf8_to_utf8(char *p, i64 n) {
    unsigned char *b = (unsigned char *)p;
    for (unsigned char *q = memchr(b, 0xED, (size_t)n); q; q = memchr(q, 0xED, (size_t)(n - (q - b)))) {
        if (q + 2 < b + n && q[1] >= 0xA0) {
            q[0] = 0xEF, q[1] = 0xBF, q[2] = 0xBD;
            q += 3;
        } else {
            q++;
        }
        if (q >= b + n) break;
    }
}

/* A string's own index keys, "0" up to its UTF-16 length less one, as
 * Object.keys and for...in list them; *n their count. */
char **__kml_str_index_keys(const char *s, i64 *n) {
    i64 len = kml_str_u16len(s);
    *n = len;
    if (len == 0) return NULL;
    char **keys = (char **)kml_alloc(KML_HEAP_WORDS, (size_t)len * sizeof(char *));
    for (i64 i = 0; i < len; i++) {
        char tmp[24];
        int w = snprintf(tmp, sizeof tmp, "%lld", i);
        char *k = (char *)__kml_str_alloc(w);
        memcpy(k, tmp, (size_t)w + 1);
        KML_STORE_PTR(&keys[i], k);
    }
    return keys;
}

/* s[i]: the unit at index i as a string, or NULL (undefined) past the
 * end. */
char *__kml_str_index(const char *s, i64 i) {
    if (i < 0) return NULL;
    if (kml_str_ascii(s)) {
        if (i >= KML_STR_LEN(s)) return NULL;
        char *out = (char *)__kml_str_alloc(1);
        out[0] = s[i];
        out[1] = 0;
        return out;
    }
    if (i >= kml_str_u16len(s)) return NULL;
    return __kml_u16_sub(s, i, i + 1);
}

/* console.log's write of a string: all of its bytes, an embedded NUL
 * included. stdout goes through its stdio buffer, which printf shares; fd 2
 * is written directly, as dprintf does. */
static void write_bytes(i64 fd, const char *s, size_t n) {
    if (fd != 2) {
        fwrite(s, 1, n, stdout);
        return;
    }
    while (n > 0) {
        long w = (long)write(2, s, n);
        if (w <= 0) return;
        s += w;
        n -= (size_t)w;
    }
}

/* A lone surrogate's WTF-8 bytes start ED A0..ED BF. */
static inline int lone_surrogate(const unsigned char *p, size_t left) {
    return left >= 3 && p[0] == 0xED && p[1] >= 0xA0;
}

void __kml_write_str(i64 fd, const char *s) {
    size_t n = (size_t)__kml_str_len(s);
    /* Text out is UTF-8: a lone surrogate is written as U+FFFD, as Node's
       encoder does. */
    const char *run = s;
    for (const char *p = memchr(s, 0xED, n); p; p = memchr(p, 0xED, n - (size_t)(p - s))) {
        if (!lone_surrogate((const unsigned char *)p, n - (size_t)(p - s))) {
            p++;
            if ((size_t)(p - s) >= n) break;
            continue;
        }
        write_bytes(fd, run, (size_t)(p - run));
        write_bytes(fd, "\xEF\xBF\xBD", 3);
        p += 3;
        run = p;
        if ((size_t)(p - s) >= n) break;
    }
    write_bytes(fd, run, n - (size_t)(run - s));
}

/* Array.prototype.join over n already-converted parts: the lengths summed,
 * one allocation, one copy each. Concatenating into an accumulator instead
 * allocated a new string per element (quadratic bytes, none freed). */
void *__kml_str_join(char *const *parts, i64 n, const char *sep) {
    i64 ls = __kml_str_len(sep), total = n > 1 ? ls * (n - 1) : 0;
    for (i64 i = 0; i < n; i++) total += __kml_str_len(parts[i]);
    char *out = (char *)__kml_str_alloc(total), *w = out;
    int joins = 0;
    for (i64 i = 0; i < n; i++) {
        if (i > 0) { memcpy(w, sep, (size_t)ls); w += ls; }
        i64 lp = __kml_str_len(parts[i]);
        joins |= lp > 0 && (unsigned char)parts[i][0] == 0xED;
        memcpy(w, parts[i], (size_t)lp);
        w += lp;
    }
    *w = '\0';
    if (joins || (ls > 0 && (unsigned char)sep[0] == 0xED)) {
        /* Join every surrogate pair split across the parts. */
        for (i64 i = 3; i + 3 <= KML_STR_LEN(out); i++)
            if ((unsigned char)out[i] == 0xED) __kml_wtf8_join(out, i);
    }
    return out;
}

void *__kml_str_from_cstr(const char *c) {
    if (!c) return NULL;
    i64 len = (i64)strlen(c);
    char *dst = (char *)__kml_str_alloc(len);
    memcpy(dst, c, (size_t)len + 1);
    return dst;
}

/* strcmp-shaped (<0/0/>0) compare of two strings in JavaScript's order, by
 * UTF-16 code units (IsLessThan), over the header lengths so an embedded NUL
 * does not stop it early. UTF-8's byte order is code-point order, which
 * differs from UTF-16's in one place: U+E000..U+FFFF (lead byte EE or EF)
 * comes after the supplementary characters (lead F0..F4), whose first unit
 * is a surrogate. The strings agree up to the first differing byte, so that
 * byte starts a character in both. */
int __kml_str_cmp(const char *a, const char *b) {
    i64 la = __kml_str_len(a), lb = __kml_str_len(b), n = la < lb ? la : lb;
    for (i64 i = 0; i < n; i++) {
        unsigned char ca = (unsigned char)a[i], cb = (unsigned char)b[i];
        if (ca == cb) continue;
        if ((ca == 0xEE || ca == 0xEF) && cb >= 0xF0) return 1;
        if (ca >= 0xF0 && (cb == 0xEE || cb == 0xEF)) return -1;
        return ca < cb ? -1 : 1;
    }
    return la < lb ? -1 : la > lb ? 1 : 0;
}

/* The program's null of a three-state pointer (emit_undefined.go nullRef),
 * stored before a string array's sort, and the default string sort: by
 * __kml_str_cmp, with that null compared as "null", String(null). */
_Thread_local void *__kml_sort_nullref;

static const struct {
    i64 n;
    char b[5];
} __attribute__((aligned(8))) sort_null = {KML_STR_ASCII_HDR(4), "null"};

static const char *sort_str(char *p) { return p == __kml_sort_nullref ? sort_null.b : p; }

int __kml_cmp_str(char *const *pa, char *const *pb) { return __kml_str_cmp(sort_str(*pa), sort_str(*pb)); }

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
    void **arr = (void **)kml_alloc(KML_HEAP_WORDS, (size_t)(argc + 1) * 8);
    arr[argc] = NULL;
    for (i64 i = 0; i < argc; i++) arr[i] = __kml_str_from_cstr(argv[i]);
    return arr;
}

/* The Node-shaped process.argv: [a[0], a[0], a[1], ... a[argc-1], NULL]. The
 * executable path sits at both argv[0] and argv[1], so user arguments start
 * at index 2. The raw argv keeps its OS shape for execv/fork. argc >= 1. */
void *__kml_argv_node_shape(i64 argc, void **hdr) {
    void **arr = (void **)kml_alloc(KML_HEAP_WORDS, (size_t)(argc + 2) * 8);
    arr[argc + 1] = NULL;
    arr[0] = hdr[0];
    arr[1] = hdr[0];
    for (i64 i = 1; i < argc; i++) arr[i + 1] = hdr[i];
    return arr;
}
