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
    kml_copy(KML_HEAP_RAW, b, p, (size_t)n);
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

/* A string's byte length, its header's: a NUL is a character. */
static i64 hdr_len(const char *s) { return *(const i64 *)(s - 8) & 0xFFFFFFFFLL; }

/* Forward scan tracking the exclusive end of the last non-whitespace byte, so
 * multi-byte whitespace needs no backwards UTF-8 decoder. */
static i64 scan_end(const char *s, i64 from) {
    i64 j = from, end = from, n = hdr_len(s);
    while (j < n) {
        i64 n = __kml_ws_span((const u8 *)s + j);
        j += n ? n : 1;
        if (!n) end = j;
    }
    return end;
}

static i64 skip_lead(const char *s) {
    i64 i = 0, n, len = hdr_len(s);
    while (i < len && (n = __kml_ws_span((const u8 *)s + i)) != 0) i += n;
    return i;
}

void *__kml_trim(const char *s) {
    i64 i = skip_lead(s);
    return str_of(s + i, scan_end(s, i) - i);
}

void *__kml_trim_start(const char *s) {
    i64 i = skip_lead(s);
    return str_of(s + i, hdr_len(s) - i);
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
    kml_copy(KML_HEAP_RAW, buf, s, (size_t)pre);
    kml_copy(KML_HEAP_RAW, buf + pre, rep, (size_t)rep_len);
    kml_copy(KML_HEAP_RAW, buf + pre + rep_len, found + search_len, (size_t)suf);
    buf[pre + rep_len + suf] = 0;
    return buf;
}

/* The bytes of the UTF-8 character a lead byte starts (clamped to left). */
static i64 u8_char(const u8 *p, i64 left) {
    i64 w = p[0] < 0x80 ? 1 : p[0] < 0xE0 ? 2 : p[0] < 0xF0 ? 3 : 4;
    return w > left ? left : w;
}

/* The UTF-16 units of the character at p (n bytes), each as its own bytes:
 * a four-byte character's two surrogates in their three-byte (WTF-8) form,
 * any other character whole. Returns the count (1 or 2). */
static int u16_units(const u8 *p, i64 n, u8 out[2][4], i64 len[2]) {
    if (n < 4) {
        memcpy(out[0], p, (size_t)n); /* scratch */
        len[0] = n;
        return 1;
    }
    unsigned cp = ((unsigned)(p[0] & 7) << 18 | (unsigned)(p[1] & 0x3F) << 12 | (unsigned)(p[2] & 0x3F) << 6 | (p[3] & 0x3F)) - 0x10000;
    unsigned u[2] = {0xD800 + (cp >> 10), 0xDC00 + (cp & 0x3FF)};
    for (int k = 0; k < 2; k++) {
        out[k][0] = (u8)(0xE0 | (u[k] >> 12));
        out[k][1] = (u8)(0x80 | ((u[k] >> 6) & 0x3F));
        out[k][2] = (u8)(0x80 | (u[k] & 0x3F));
        len[k] = 3;
    }
    return 2;
}

/* One left-to-right pass over the ORIGINAL string, never rescanning written
 * replacement text. An empty search inserts rep between every UTF-16 unit:
 * "abc".replaceAll("", "-") is "-a-b-c-". */
void *__kml_replace_all(const char *s, i64 slen, const char *search, i64 search_len, const char *rep, i64 rep_len) {
    if (search_len == 0) {
        /* At most one unit per byte, a pair's two halves 3 bytes each. */
        char *buf = (char *)__kml_str_alloc(slen * 2 + rep_len * (slen + 1));
        char *out = buf;
        kml_copy(KML_HEAP_RAW, out, rep, (size_t)rep_len);
        out += rep_len;
        for (i64 i = 0; i < slen;) {
            i64 w = u8_char((const u8 *)s + i, slen - i), len[2];
            u8 unit[2][4];
            int k = u16_units((const u8 *)s + i, w, unit, len);
            for (int j = 0; j < k; j++) {
                kml_copy(KML_HEAP_RAW, out, unit[j], (size_t)len[j]);
                out += len[j];
                kml_copy(KML_HEAP_RAW, out, rep, (size_t)rep_len);
                out += rep_len;
            }
            i += w;
        }
        *out = 0;
        *(i64 *)KML_PREFIX(buf) = out - buf;
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
        kml_copy(KML_HEAP_RAW, out, cur, (size_t)part);
        out += part;
        kml_copy(KML_HEAP_RAW, out, rep, (size_t)rep_len);
        out += rep_len;
        rem -= part + search_len;
        cur = f + search_len;
    }
    kml_copy(KML_HEAP_RAW, out, cur, (size_t)rem);
    out[rem] = 0;
    return buf;
}

/* GetSubstitution (ECMA-262 22.1.3.19.1) for a string search, which has no
 * captures: $$ is $, $& the match, $` the text before it, $' the text after
 * it; $n and $<name> stay as written. Writes into out when it is non-null;
 * returns the length. */
static i64 subst(char *out, const char *rep, i64 rep_len, const char *s, i64 slen, i64 pos, i64 mlen) {
    i64 n = 0;
    for (i64 i = 0; i < rep_len; i++) {
        const char *piece = rep + i;
        i64 plen = 1;
        if (rep[i] == '$' && i + 1 < rep_len) {
            switch (rep[i + 1]) {
            case '$': piece = "$"; plen = 1; i++; break;
            case '&': piece = s + pos; plen = mlen; i++; break;
            case '`': piece = s; plen = pos; i++; break;
            case '\'': piece = s + pos + mlen; plen = slen - pos - mlen; i++; break;
            default: break;
            }
        }
        if (out) kml_copy(KML_HEAP_RAW, out + n, piece, (size_t)plen);
        n += plen;
    }
    return n;
}

/* String.prototype.replace / replaceAll with a string search and a string
 * replacement, its $ patterns expanded. */
void *__kml_replace_subst(const char *s, i64 slen, const char *search, i64 search_len, const char *rep, i64 rep_len) {
    if (!memchr(rep, '$', (size_t)rep_len)) return __kml_replace(s, slen, search, search_len, rep, rep_len);
    const char *found = search_len == 0 ? s : (const char *)memmem(s, (size_t)slen, search, (size_t)search_len);
    if (!found) return str_of(s, slen);
    i64 pre = found - s, suf = slen - (pre + search_len);
    i64 rl = subst(0, rep, rep_len, s, slen, pre, search_len);
    char *buf = (char *)__kml_str_alloc(pre + rl + suf);
    kml_copy(KML_HEAP_RAW, buf, s, (size_t)pre);
    subst(buf + pre, rep, rep_len, s, slen, pre, search_len);
    kml_copy(KML_HEAP_RAW, buf + pre + rl, found + search_len, (size_t)suf);
    buf[pre + rl + suf] = 0;
    return buf;
}

void __kml_wtf8_join(char *s, i64 at);

void *__kml_replace_all_subst(const char *s, i64 slen, const char *search, i64 search_len, const char *rep, i64 rep_len) {
    if (!memchr(rep, '$', (size_t)rep_len)) return __kml_replace_all(s, slen, search, search_len, rep, rep_len);
    if (search_len == 0) {
        /* An empty search matches at every UTF-16 unit, a pair's two halves
         * apart: substitute over the string's units ($` and $' may end or
         * start between them), then join each pair the result holds. */
        int astral = 0;
        for (i64 i = 0; i < slen && !astral; i++) astral = ((const u8 *)s)[i] >= 0xF0;
        if (astral) {
            char *x = (char *)malloc((size_t)slen * 2 + 1); /* scratch: the units */
            i64 xn = 0;
            for (i64 i = 0; i < slen;) {
                i64 w = u8_char((const u8 *)s + i, slen - i), len[2];
                u8 unit[2][4];
                int k = u16_units((const u8 *)s + i, w, unit, len);
                for (int j = 0; j < k; j++) { memcpy(x + xn, unit[j], (size_t)len[j]); xn += len[j]; } /* scratch */
                i += w;
            }
            x[xn] = 0;
            char *buf = (char *)__kml_replace_all_subst(x, xn, search, 0, rep, rep_len);
            free(x); /* scratch */
            for (i64 at = 3; at + 3 <= *(i64 *)KML_PREFIX(buf); at++)
                if ((u8)buf[at] == 0xED && (u8)buf[at + 1] >= 0xB0) __kml_wtf8_join(buf, at);
            return buf;
        }
    }
    /* Match positions: every search_len step, or each UTF-16 unit boundary
     * for an empty search. */
    i64 cap = 16, cnt = 0;
    i64 *at = (i64 *)malloc((size_t)cap * sizeof(i64)); /* scratch: match positions */
    if (search_len == 0) {
        for (i64 i = 0;; ) {
            if (cnt == cap) { cap *= 2; at = (i64 *)realloc(at, (size_t)cap * sizeof(i64)); } /* scratch */
            at[cnt++] = i;
            if (i >= slen) break;
            i64 w = u8_char((const u8 *)s + i, slen - i);
            i += w; /* (a pair's halves are not split: WTF-8 positions only) */
        }
    } else {
        const char *cur = s, *f;
        i64 rem = slen;
        while ((f = (const char *)memmem(cur, (size_t)rem, search, (size_t)search_len))) {
            if (cnt == cap) { cap *= 2; at = (i64 *)realloc(at, (size_t)cap * sizeof(i64)); } /* scratch */
            at[cnt++] = f - s;
            rem -= (f - cur) + search_len;
            cur = f + search_len;
        }
    }
    i64 total = slen - cnt * search_len;
    for (i64 k = 0; k < cnt; k++) total += subst(0, rep, rep_len, s, slen, at[k], search_len);
    char *buf = (char *)__kml_str_alloc(total);
    char *out = buf;
    i64 prev = 0;
    for (i64 k = 0; k < cnt; k++) {
        kml_copy(KML_HEAP_RAW, out, s + prev, (size_t)(at[k] - prev));
        out += at[k] - prev;
        out += subst(out, rep, rep_len, s, slen, at[k], search_len);
        prev = at[k] + search_len;
    }
    kml_copy(KML_HEAP_RAW, out, s + prev, (size_t)(slen - prev));
    out += slen - prev;
    *out = 0;
    free(at); /* scratch */
    return buf;
}

/* The string array behind split(): the pointer to the parts, count in *outlen.
 * __kml_split (a generated wrapper) returns them as {ptr, i64}; a 16-byte
 * struct return differs between the Windows and SysV C ABIs. */
void *__kml_split_parts(const char *s, i64 slen, const char *sep, i64 sep_len, i64 *outlen) {
    if (sep_len == 0) {
        /* One part per UTF-16 unit. */
        char **arr = (char **)kml_alloc(KML_HEAP_WORDS, (size_t)(slen > 0 ? slen : 1) * 8);
        i64 n = 0;
        for (i64 i = 0; i < slen;) {
            i64 w = u8_char((const u8 *)s + i, slen - i), len[2];
            u8 unit[2][4];
            int k = u16_units((const u8 *)s + i, w, unit, len);
            for (int j = 0; j < k; j++) KML_STORE_PTR(&arr[n++], str_of((const char *)unit[j], len[j]));
            i += w;
        }
        *outlen = n;
        return arr;
    }
    i64 cnt = 0, rem = slen;
    const char *cur = s, *f;
    while ((f = (const char *)memmem(cur, (size_t)rem, sep, (size_t)sep_len))) {
        cnt++;
        rem -= (f - cur) + sep_len;
        cur = f + sep_len;
    }
    char **arr = (char **)kml_alloc(KML_HEAP_WORDS, (size_t)(cnt + 1) * 8);
    i64 idx = 0;
    cur = s;
    rem = slen;
    while ((f = (const char *)memmem(cur, (size_t)rem, sep, (size_t)sep_len))) {
        i64 part = f - cur;
        KML_STORE_PTR(&arr[idx++], str_of(cur, part));
        rem -= part + sep_len;
        cur = f + sep_len;
    }
    KML_STORE_PTR(&arr[idx], str_of(cur, rem));
    *outlen = cnt + 1;
    return arr;
}
