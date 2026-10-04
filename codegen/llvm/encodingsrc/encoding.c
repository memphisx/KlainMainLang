/* encoding.c — base64 (btoa/atob), the UTF-8 label check and the URI
 * percent encoders/decoders (in C per TDD-00240). Results are string-header
 * strings from __kml_str_alloc; each decoder stores its real length in the
 * header word before the data. */
#include <stdlib.h>
#include <string.h>

typedef long long i64;
typedef unsigned char u8;

extern char *__kml_str_alloc(i64 n);
extern void *__kml_str_from_cstr(const char *c);
extern void __kml_throw(void *err) __attribute__((noreturn));

static void set_len(char *s, i64 n) { *(i64 *)(s - 8) = n; }

/* An Error object {kind, message, name} (errorObjType) thrown as-is. */
static void throw_err(i64 kind, const char *msg, const char *name) {
    void **o = (void **)malloc(24);
    *(i64 *)o = kind;
    o[1] = __kml_str_from_cstr(msg);
    o[2] = __kml_str_from_cstr(name);
    __kml_throw(o);
}

#define KML_URIERROR_KIND 281474976710661LL
#define KML_INVALIDCHAR_KIND 281474976710663LL

static const char b64[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

/* The 6-bit value of a base64 alphabet char; -1 outside it ('=' too). */
static int b64_val(u8 c) {
    if (c >= 'A' && c <= 'Z') return c - 'A';
    if (c >= 'a' && c <= 'z') return c - 'a' + 26;
    if (c >= '0' && c <= '9') return c - '0' + 52;
    if (c == '+') return 62;
    if (c == '/') return 63;
    return -1;
}

/* Standard '='-padded base64 of the first len bytes. */
static char *b64_encode(const u8 *s, i64 len) {
    char *out = __kml_str_alloc((len + 2) / 3 * 4);
    i64 o = 0;
    for (i64 i = 0; i < len; i += 3) {
        int has1 = i + 1 < len, has2 = i + 2 < len;
        unsigned n = (unsigned)s[i] << 16 | (has1 ? (unsigned)s[i + 1] << 8 : 0) | (has2 ? s[i + 2] : 0u);
        out[o++] = b64[n >> 18 & 63];
        out[o++] = b64[n >> 12 & 63];
        out[o++] = has1 ? b64[n >> 6 & 63] : '=';
        out[o++] = has2 ? b64[n & 63] : '=';
    }
    out[o] = 0;
    return out;
}

char *__kml_btoa(const char *str) { return b64_encode((const u8 *)str, (i64)strlen(str)); }

/* The binary-safe form: the input need not be NUL-terminated (a SHA-1
 * digest can hold a 0x00). */
char *__kml_base64_encode_bytes(const char *str, i64 len) { return b64_encode((const u8 *)str, len); }

/* WHATWG forgiving-base64 (ADR-00458/00550/00563): ASCII whitespace is
 * stripped, up to two trailing '=' removed, a remaining length = 1 (mod 4)
 * throws InvalidCharacterError, a short length is re-padded, and every
 * remaining char must be in the alphabet ('=' is not). */
char *__kml_atob(const char *str) {
    i64 rawlen = (i64)strlen(str), nw = 0;
    char *norm = (char *)malloc((size_t)rawlen + 5);
    for (i64 i = 0; i < rawlen; i++) {
        char c = str[i];
        if (c != 9 && c != 10 && c != 12 && c != 13 && c != 32) norm[nw++] = c;
    }
    for (int stripped = 0; stripped < 2 && nw > 0 && norm[nw - 1] == '='; stripped++) nw--;
    i64 rem = nw % 4;
    if (rem != 1) {
        i64 pad = (4 - rem) % 4;
        for (i64 i = 0; i < pad; i++) norm[nw + i] = '=';
        norm[nw + pad] = 0;
        i64 len = nw + pad;
        i64 i = 0;
        for (; i < nw; i++)
            if (b64_val((u8)norm[i]) < 0) break;
        if (i == nw) {
            char *out = __kml_str_alloc(len / 4 * 3);
            i64 o = 0;
            for (i64 j = 0; j + 4 <= len; j += 4) {
                int c2eq = norm[j + 2] == '=', c3eq = norm[j + 3] == '=';
                int v0 = b64_val((u8)norm[j]), v1 = b64_val((u8)norm[j + 1]);
                int v2 = c2eq ? 0 : b64_val((u8)norm[j + 2]), v3 = c3eq ? 0 : b64_val((u8)norm[j + 3]);
                unsigned n = (unsigned)v0 << 18 | (unsigned)v1 << 12 | (unsigned)v2 << 6 | (unsigned)v3;
                out[o] = (char)(n >> 16);
                out[o + 1] = (char)(n >> 8);
                out[o + 2] = (char)n;
                o += c2eq ? 1 : c3eq ? 2 : 3;
            }
            out[o] = 0;
            set_len(out, o);
            free(norm);
            return out;
        }
    }
    throw_err(KML_INVALIDCHAR_KIND, "The string to be decoded contains invalid characters.", "InvalidCharacterError");
    return 0;
}

static int is_ws(u8 c) { return c == 9 || c == 10 || c == 12 || c == 13 || c == 32; }

/* WHATWG "get an encoding" restricted to UTF-8's labels (ADR-00567): trim
 * ASCII whitespace, ASCII-lowercase, match one of the six aliases. */
_Bool __kml_is_utf8_label(const char *s) {
    i64 st = 0, en = (i64)strlen(s);
    while (st < en && is_ws((u8)s[st])) st++;
    while (en > st && is_ws((u8)s[en - 1])) en--;
    i64 n = en - st;
    if (n > 39) return 0;
    char buf[40];
    for (i64 i = 0; i < n; i++) {
        char c = s[st + i];
        buf[i] = (c >= 'A' && c <= 'Z') ? (char)(c + 32) : c;
    }
    buf[n] = 0;
    return !strcmp(buf, "utf-8") || !strcmp(buf, "utf8") || !strcmp(buf, "unicode-1-1-utf-8") ||
           !strcmp(buf, "unicode11utf8") || !strcmp(buf, "unicode20utf8") || !strcmp(buf, "x-unicode20utf8");
}

static int in_set(const char *set, u8 c) { return c && strchr(set, c) != 0; }

#define UNRESERVED "-_.!~*'()"
#define RESERVED ";/?:@&=+$,#"

/* Percent-encode every byte not alnum and not in extra (ASCII only
 * alnum). file_url selects Node's pathToFileURL set instead: printable
 * ASCII except `" # % < > ? [ ] ^ { | } ~`, the backtick and the backslash. */
static char *pct_encode(const char *str, const char *extra, int file_url) {
    static const char hex[] = "0123456789ABCDEF";
    i64 len = (i64)strlen(str), o = 0;
    char *out = __kml_str_alloc(len * 3);
    for (i64 i = 0; i < len; i++) {
        u8 c = (u8)str[i];
        int safe = file_url ? (c >= 0x21 && c < 0x7F && !in_set("\"#%<>?[]^`{|}~\\", c))
                            : ((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || in_set(extra, c));
        if (safe) {
            out[o++] = (char)c;
        } else {
            out[o++] = '%';
            out[o++] = hex[c >> 4];
            out[o++] = hex[c & 15];
        }
    }
    out[o] = 0;
    set_len(out, o);
    return out;
}

char *__kml_encode_uri_component(const char *str) { return pct_encode(str, UNRESERVED, 0); }
char *__kml_encode_uri(const char *str) { return pct_encode(str, UNRESERVED RESERVED, 0); }
char *__kml_encode_file_url_path(const char *str) { return pct_encode(str, "", 1); }

static int hex_val(u8 c) {
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    return -1;
}

/* Percent-decode. Lenient: a malformed or truncated "%" passes through as
 * a literal; strict: it throws URIError (ADR-00556). keep_reserved is
 * decodeURI's difference: an escape decoding to a reserved URI character
 * stays as its literal "%XX" text. */
static char *pct_decode(const char *str, int keep_reserved, int strict) {
    i64 len = (i64)strlen(str), o = 0;
    char *out = __kml_str_alloc(len);
    for (i64 i = 0; i < len;) {
        if (str[i] != '%') {
            out[o++] = str[i++];
            continue;
        }
        int h1 = i + 1 < len ? hex_val((u8)str[i + 1]) : -1;
        int h2 = i + 2 < len ? hex_val((u8)str[i + 2]) : -1;
        if (h1 < 0 || h2 < 0) {
            if (strict) throw_err(KML_URIERROR_KIND, "URI malformed", "URIError");
            out[o++] = str[i++];
            continue;
        }
        u8 b = (u8)(h1 << 4 | h2);
        if (keep_reserved && in_set(RESERVED, b)) {
            out[o++] = '%';
            out[o++] = str[i + 1];
            out[o++] = str[i + 2];
        } else {
            out[o++] = (char)b;
        }
        i += 3;
    }
    out[o] = 0;
    set_len(out, o);
    return out;
}

char *__kml_decode_uri_component(const char *str) { return pct_decode(str, 0, 0); }
char *__kml_decode_uri_component_strict(const char *str) { return pct_decode(str, 0, 1); }
char *__kml_decode_uri_strict(const char *str) { return pct_decode(str, 1, 1); }
