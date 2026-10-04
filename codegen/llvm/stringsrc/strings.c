/* strings.c — String.prototype trim/toLowerCase/replace/replaceAll/split
 * runtime (in C per TDD-00240). Strings are length-headered buffers
 * (strheader.c); results are fresh headered strings. */
#ifndef _WIN32
#define _GNU_SOURCE
#endif
#include <stdlib.h>
#include <string.h>

typedef long long i64;
typedef unsigned char u8;

#ifdef _WIN32
void *memmem(const void *hay, size_t hlen, const void *needle, size_t nlen);
#endif

extern void *__kml_str_alloc(i64 n);

/* A headered string holding n bytes of p. */
static char *str_of(const char *p, i64 n) {
    char *b = (char *)__kml_str_alloc(n);
    memcpy(b, p, (size_t)n);
    b[n] = 0;
    return b;
}

/* Byte length of the JS WhiteSpace/LineTerminator character at p (0 when p
 * starts none): ASCII \t \n \v \f \r space, U+00A0, U+1680, U+2000-200A,
 * U+2028/9, U+202F, U+205F, U+3000, U+FEFF in UTF-8. A byte past p[0] is read
 * only once the earlier one is known non-NUL. */
i64 __kml_ws_span(const u8 *p) {
    u8 c0 = p[0];
    if (c0 == 32 || (c0 >= 9 && c0 <= 13)) return 1;
    if (c0 != 0xC2 && c0 != 0xE1 && c0 != 0xE2 && c0 != 0xE3 && c0 != 0xEF) return 0;
    u8 c1 = p[1];
    if (c1 == 0) return 0;
    if (c0 == 0xC2) return c1 == 0xA0 ? 2 : 0;
    u8 c2 = p[2];
    if (c2 == 0) return 0;
    if (c0 == 0xE1) return c1 == 0x9A && c2 == 0x80 ? 3 : 0;
    if (c0 == 0xE2) {
        if (c1 == 0x80) return (c2 >= 0x80 && c2 <= 0x8A) || c2 == 0xA8 || c2 == 0xA9 || c2 == 0xAF ? 3 : 0;
        return c1 == 0x81 && c2 == 0x9F ? 3 : 0;
    }
    if (c0 == 0xE3) return c1 == 0x80 && c2 == 0x80 ? 3 : 0;
    return c1 == 0xBB && c2 == 0xBF ? 3 : 0;
}

/* Forward scan tracking the exclusive end of the last non-whitespace byte, so
 * multi-byte whitespace needs no backwards UTF-8 decoder. */
static i64 scan_end(const char *s, i64 from) {
    i64 j = from, end = from;
    while (s[j]) {
        i64 n = __kml_ws_span((const u8 *)s + j);
        j += n ? n : 1;
        if (!n) end = j;
    }
    return end;
}

static i64 skip_lead(const char *s) {
    i64 i = 0, n;
    while ((n = __kml_ws_span((const u8 *)s + i)) != 0) i += n;
    return i;
}

void *__kml_trim(const char *s) {
    i64 i = skip_lead(s);
    return str_of(s + i, scan_end(s, i) - i);
}

void *__kml_trim_start(const char *s) {
    i64 i = skip_lead(s);
    return str_of(s + i, (i64)strlen(s + i));
}

void *__kml_trim_end(const char *s) { return str_of(s, scan_end(s, 0)); }

void *__kml_tolower(const char *s) {
    i64 len = (i64)strlen(s);
    char *buf = (char *)__kml_str_alloc(len);
    for (i64 i = 0; i < len; i++) buf[i] = s[i] >= 'A' && s[i] <= 'Z' ? (char)(s[i] + 32) : s[i];
    buf[len] = 0;
    return buf;
}

/* Lengths are explicit: internal callers (HTTP header/query parsing, SSE) pass
 * strlen() of a raw headerless buffer, so the header is never read. An empty
 * search matches at 0 (memmem's empty-needle result is not portable). */
void *__kml_replace(const char *s, i64 slen, const char *search, i64 search_len, const char *rep, i64 rep_len) {
    const char *found = search_len == 0 ? s : (const char *)memmem(s, (size_t)slen, search, (size_t)search_len);
    if (!found) return str_of(s, slen);
    i64 pre = found - s, suf = slen - (pre + search_len);
    char *buf = (char *)__kml_str_alloc(pre + rep_len + suf);
    memcpy(buf, s, (size_t)pre);
    memcpy(buf + pre, rep, (size_t)rep_len);
    memcpy(buf + pre + rep_len, found + search_len, (size_t)suf);
    buf[pre + rep_len + suf] = 0;
    return buf;
}

/* One left-to-right pass over the ORIGINAL string, never rescanning written
 * replacement text. An empty search inserts rep between every char:
 * "abc".replaceAll("", "-") is "-a-b-c-". */
void *__kml_replace_all(const char *s, i64 slen, const char *search, i64 search_len, const char *rep, i64 rep_len) {
    if (search_len == 0) {
        char *buf = (char *)__kml_str_alloc(slen + rep_len * (slen + 1));
        char *out = buf;
        memcpy(out, rep, (size_t)rep_len);
        out += rep_len;
        for (i64 i = 0; i < slen; i++) {
            *out++ = s[i];
            memcpy(out, rep, (size_t)rep_len);
            out += rep_len;
        }
        *out = 0;
        return buf;
    }
    i64 cnt = 0, rem = slen;
    const char *cur = s, *f;
    while ((f = (const char *)memmem(cur, (size_t)rem, search, (size_t)search_len))) {
        cnt++;
        rem -= (f - cur) + search_len;
        cur = f + search_len;
    }
    char *buf = (char *)__kml_str_alloc(slen - cnt * search_len + cnt * rep_len);
    char *out = buf;
    cur = s;
    rem = slen;
    while ((f = (const char *)memmem(cur, (size_t)rem, search, (size_t)search_len))) {
        i64 part = f - cur;
        memcpy(out, cur, (size_t)part);
        out += part;
        memcpy(out, rep, (size_t)rep_len);
        out += rep_len;
        rem -= part + search_len;
        cur = f + search_len;
    }
    memcpy(out, cur, (size_t)rem);
    out[rem] = 0;
    return buf;
}

/* The string array behind split(): the pointer to the parts, count in *outlen.
 * __kml_split (a generated wrapper) returns them as {ptr, i64}; a 16-byte
 * struct return differs between the Windows and SysV C ABIs. */
void *__kml_split_parts(const char *s, i64 slen, const char *sep, i64 sep_len, i64 *outlen) {
    if (sep_len == 0) {
        char **arr = (char **)malloc((size_t)slen * 8);
        for (i64 i = 0; i < slen; i++) arr[i] = str_of(s + i, 1);
        *outlen = slen;
        return arr;
    }
    i64 cnt = 0, rem = slen;
    const char *cur = s, *f;
    while ((f = (const char *)memmem(cur, (size_t)rem, sep, (size_t)sep_len))) {
        cnt++;
        rem -= (f - cur) + sep_len;
        cur = f + sep_len;
    }
    char **arr = (char **)malloc((size_t)(cnt + 1) * 8);
    i64 idx = 0;
    cur = s;
    rem = slen;
    while ((f = (const char *)memmem(cur, (size_t)rem, sep, (size_t)sep_len))) {
        i64 part = f - cur;
        arr[idx++] = str_of(cur, part);
        rem -= part + sep_len;
        cur = f + sep_len;
    }
    arr[idx] = str_of(cur, rem);
    *outlen = cnt + 1;
    return arr;
}
