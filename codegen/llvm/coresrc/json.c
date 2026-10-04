/* json.c — JSON.stringify's number/string fragments and the accumulator
 * concat (TDD-00240). Strings are header strings: a length word sits 8
 * bytes before the data (see runtime_strheader.go). */
#include <stdio.h>
#include <string.h>

typedef long long i64;

extern char *__kml_str_alloc(i64 n);
extern void __kml_str_finalize(char *s);
extern i64 __kml_str_len(const char *s);
extern void __kml_str_free(char *s);

char *__kml_json_str_num(i64 n) {
    char *buf = __kml_str_alloc(32);
    snprintf(buf, 32, "%lld", n);
    __kml_str_finalize(buf);
    return buf;
}

/* Concatenate two header strings into a fresh one, freeing each input whose
 * flag is set (callers pass a bare i1: only bit 0 counts). */
char *__kml_json_concat2(const char *a, const char *b, int fa, int fb) {
    i64 la = __kml_str_len(a), lb = __kml_str_len(b);
    char *dst = __kml_str_alloc(la + lb);
    memcpy(dst, a, (size_t)la);
    memcpy(dst + la, b, (size_t)lb + 1);
    if (fa & 1) __kml_str_free((char *)a);
    if (fb & 1) __kml_str_free((char *)b);
    return dst;
}

/* A quoted JSON string; null pointer serializes as the literal null. Bounded
 * by the string's length header, so an embedded NUL is escaped (\u0000);
 * worst case \u00XX for every byte. */
char *__kml_json_str_str(const char *s) {
    if (!s) {
        char *eb = __kml_str_alloc(4);
        memcpy(eb, "null", 4);
        eb[4] = 0;
        return eb;
    }
    i64 len = __kml_str_len(s);
    char *buf = __kml_str_alloc(len * 6 + 3);
    i64 j = 0;
    buf[j++] = '"';
    for (i64 i = 0; i < len; i++) {
        unsigned char c = (unsigned char)s[i];
        char e = 0;
        switch (c) {
        case '"': e = '"'; break;
        case '\\': e = '\\'; break;
        case '\n': e = 'n'; break;
        case '\r': e = 'r'; break;
        case '\t': e = 't'; break;
        case '\b': e = 'b'; break;
        case '\f': e = 'f'; break;
        }
        if (e) {
            buf[j++] = '\\';
            buf[j++] = e;
        } else if (c < 32) {
            static const char hex[] = "0123456789abcdef";
            buf[j++] = '\\';
            buf[j++] = 'u';
            buf[j++] = '0';
            buf[j++] = '0';
            buf[j++] = hex[c >> 4];
            buf[j++] = hex[c & 15];
        } else {
            buf[j++] = (char)c;
        }
    }
    buf[j++] = '"';
    buf[j] = 0;
    memcpy(buf - 8, &j, 8);
    return buf;
}
