/* regexp.c — RegExp helpers around PCRE2: flag parsing and validation, the
 * byte <-> UTF-16 offset converters, the ECMAScript source rewrite (in C per
 * TDD-00240). The pcre2_* calls themselves stay in generated code. */
#include <stdlib.h>
#include <string.h>

typedef long long i64;
typedef unsigned char u8;

extern void *__kml_str_alloc(i64 n);
extern void __kml_str_finalize(char *s);

/* Bytes (1-4) of the UTF-8 code point at str[off], from the lead byte alone; a
 * continuation or malformed lead counts as 1. */
i64 __kml_regex_utf8_width(const u8 *str, i64 off) {
    u8 b = str[off];
    if (b < 0x80) return 1;
    if ((b & 0xE0) == 0xC0) return 2;
    if ((b & 0xF0) == 0xE0) return 3;
    if ((b & 0xF8) == 0xF0) return 4;
    return 1;
}

/* UTF-16 code units in str[0:bytelen]; bytelen is non-negative. */
i64 __kml_regex_byte_to_utf16(const u8 *str, i64 bytelen) {
    i64 i = 0, u = 0;
    while (i < bytelen) {
        i64 w = __kml_regex_utf8_width(str, i);
        u += w == 4 ? 2 : 1;
        i += w;
    }
    return u;
}

/* Byte offset of the target-th UTF-16 unit. Past the NUL it extends one byte
 * per missing unit instead of clamping: the global empty-match advance relies
 * on a strlen+1 target mapping beyond the end so PCRE2 rejects it and the loop
 * ends. */
i64 __kml_regex_utf16_to_byte(const u8 *str, i64 target) {
    i64 i = 0, u = 0;
    while (u < target) {
        if (str[i] == 0) return i + (target - u);
        i64 w = __kml_regex_utf8_width(str, i);
        u += w == 4 ? 2 : 1;
        i += w;
    }
    return i;
}

/* -regex=ecmascript: rewrite an unescaped top-level `.` (outside a class, and
 * only without the s flag) to the class of everything but the four ES line
 * terminators. Everything else is copied verbatim. A malloc'd result; the
 * longest expansion is 19 bytes per input byte. */
void *__kml_regex_es_normalize(const char *pat, unsigned char dotall) {
    static const char repl[20] = "[^\\n\\r\\u2028\\u2029]";
    i64 len = (i64)strlen(pat), s = 0, d = 0;
    char *out = (char *)malloc((size_t)len * 19 + 1);
    int incls = 0;
    while (s < len) {
        char c = pat[s];
        if (c == '\\') {
            out[d++] = '\\';
            if (s + 1 < len) {
                out[d++] = pat[s + 1];
                s += 2;
            } else {
                s += 1;
            }
        } else if (c == '.' && !incls && !(dotall & 1)) {
            memcpy(out + d, repl, 19);
            d += 19;
            s += 1;
        } else {
            if (c == '[' && !incls) incls = 1;
            else if (c == ']' && incls) incls = 0;
            out[d++] = c;
            s += 1;
        }
    }
    out[d] = 0;
    return out;
}

/* ECMAScript's two class forms PCRE reads otherwise, rewritten for every ES
 * dialect: `[]` matches nothing, `(?!)`, and `[^]` anything, `[\s\S]` (PCRE
 * reads a `]` right after `[` or `[^` as a literal member). The pattern
 * itself when neither occurs, else a fresh malloc'd copy. */
void *__kml_regex_es_classes(const char *pat) {
    i64 len = (i64)strlen(pat), s = 0, d = 0;
    char *out = NULL;
    int incls = 0;
    while (s < len) {
        char c = pat[s];
        if (c == '\\') {
            if (out) {
                out[d++] = c;
                if (s + 1 < len) out[d++] = pat[s + 1];
            }
            s += s + 1 < len ? 2 : 1;
            continue;
        }
        if (!incls && c == '[' && (pat[s + 1] == ']' || (pat[s + 1] == '^' && pat[s + 2] == ']'))) {
            if (!out) {
                out = (char *)malloc((size_t)len * 3 + 1);
                memcpy(out, pat, (size_t)s);
                d = s;
            }
            if (pat[s + 1] == ']') {
                memcpy(out + d, "(?!)", 4);
                d += 4;
                s += 2;
            } else {
                memcpy(out + d, "[\\s\\S]", 6);
                d += 6;
                s += 3;
            }
            continue;
        }
        if (c == '[' && !incls) {
            incls = 1;
            if (out) out[d++] = c;
            s++;
            /* A leading `^` and then a leading `]` are class members here. */
            if (s < len && pat[s] == '^') {
                if (out) out[d++] = '^';
                s++;
            }
            continue;
        }
        if (c == ']' && incls) incls = 0;
        if (out) out[d++] = c;
        s++;
    }
    if (!out) return (void *)pat;
    out[d] = 0;
    return out;
}

/* The flags (already validated, duplicate-free) in canonical d,g,i,m,s,u,v,y
 * order, as a fresh headered string. */
void *__kml_regex_flags_canon(const char *f) {
    static const char order[8] = {'d', 'g', 'i', 'm', 's', 'u', 'v', 'y'};
    i64 len = (i64)strlen(f), d = 0;
    char *out = (char *)__kml_str_alloc(len);
    for (int k = 0; k < 8; k++)
        for (i64 s = 0; s < len; s++)
            if (f[s] == order[k]) out[d++] = order[k];
    out[d] = 0;
    __kml_str_finalize(out);
    return out;
}

/* `.source` of an empty pattern is "(?:)". */
void *__kml_regex_source_norm(char *s) {
    if (strlen(s) != 0) return s;
    char *out = (char *)__kml_str_alloc(4);
    memcpy(out, "(?:)", 5);
    return out;
}

/* 0 for a valid flags string, 1 for an unknown letter or a repeated one. */
int __kml_regex_validate_flags(const char *flags) {
    int seen = 0;
    for (i64 i = 0; flags[i]; i++) {
        const char *p = strchr("dgimsuvy", flags[i]);
        if (!p) return 1;
        int bit = 1 << (p - "dgimsuvy");
        if (seen & bit) return 1;
        seen |= bit;
    }
    return 0;
}

/* Combined PCRE2 compile options (i=8, m=1024, s=32) plus the g/i/m/s/y
 * booleans, in one pass; unknown letters are ignored. */
void __kml_regex_parse_flags(const char *flags, int *opt, u8 *g, u8 *i, u8 *m, u8 *s, u8 *y) {
    *opt = 0;
    *g = *y = *i = *m = *s = 0;
    for (; *flags; flags++) {
        switch (*flags) {
        case 'g': *g = 1; break;
        case 'i': *i = 1; *opt |= 8; break;
        case 'm': *m = 1; *opt |= 1024; break;
        case 's': *s = 1; *opt |= 32; break;
        case 'y': *y = 1; break;
        }
    }
}
