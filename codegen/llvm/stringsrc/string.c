// string.c — String.prototype methods the builtin declarations (lib/es.d.ts)
// lower to. A string is a length-headered byte buffer: the i64 length sits in
// the 8 bytes before the pointer. Positions are byte offsets, as everywhere in
// the string layer. An optional parameter arrives as a presence flag and a
// value.
#include <math.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>
#include <time.h>

static long long str_len(const char *s) { return *(const long long *)(s - 8); }

// A new headered string holding n bytes of p.
static char *str_new(const char *p, long long n) {
    char *base = (char *)malloc((size_t)n + 9);
    *(long long *)base = n;
    if (n > 0) memcpy(base + 8, p, (size_t)n);
    base[n + 8] = 0;
    return base + 8;
}

// The IR runtime's RangeError thrower (ensureRangeErrorThrow).
extern void __kml_throw_range_error(const char *msg) __attribute__((noreturn));

// maxStringLength is the longest string Node (V8) makes; a longer result is
// a RangeError, as there.
static const long long maxStringLength = 536870888;

// ToIntegerOrInfinity: NaN is 0, a finite value truncates toward zero.
static double to_integer(double v) {
    if (isnan(v)) return 0;
    return trunc(v);
}

// clamp is v clamped to [lo, hi], as a byte offset.
static long long clamp(double v, long long lo, long long hi) {
    if (v <= (double)lo) return lo;
    if (v >= (double)hi) return hi;
    return (long long)v;
}

// ToIntegerOrInfinity(pos) clamped to [0, len]; an absent position is 0.
static long long clamp_pos(bool has, double pos, long long len) {
    if (!has || isnan(pos) || pos <= 0) return 0;
    if (pos >= (double)len) return len;
    return (long long)pos;
}

// The first occurrence of search at or after from, or -1. Binary-safe, and
// portable: memmem is not in every C runtime (Windows has none).
static long long index_from(const char *s, const char *search, long long from) {
    long long n = str_len(s), m = str_len(search);
    if (m == 0) return from;
    for (long long i = from; i + m <= n; i++) {
        const char *p = memchr(s + i, search[0], (size_t)(n - m - i + 1));
        if (!p) return -1;
        i = p - s;
        if (memcmp(p, search, (size_t)m) == 0) return i;
    }
    return -1;
}

double __kml_String_indexOf(const char *s, const char *search, bool has, double pos) {
    return (double)index_from(s, search, clamp_pos(has, pos, str_len(s)));
}

bool __kml_String_includes(const char *s, const char *search, bool has, double pos) {
    return index_from(s, search, clamp_pos(has, pos, str_len(s))) >= 0;
}

// String.prototype.charAt(pos): the byte at ToIntegerOrInfinity(pos), or "".
char *__kml_String_charAt(const char *s, double pos) {
    double p = to_integer(pos);
    long long n = str_len(s);
    if (p < 0 || p >= (double)n) return str_new("", 0);
    return str_new(s + (long long)p, 1);
}

// String.prototype.charCodeAt(index): the byte's value, or NaN.
double __kml_String_charCodeAt(const char *s, double index) {
    double p = to_integer(index);
    if (p < 0 || p >= (double)str_len(s)) return NAN;
    return (double)(unsigned char)s[(long long)p];
}

// String.prototype.slice(start, end): a negative bound counts from the end.
char *__kml_String_slice(const char *s, bool hasStart, double start, bool hasEnd, double end) {
    long long n = str_len(s);
    double a = hasStart ? to_integer(start) : 0, b = hasEnd ? to_integer(end) : (double)n;
    long long from = a < 0 ? clamp((double)n + a, 0, n) : clamp(a, 0, n);
    long long to = b < 0 ? clamp((double)n + b, 0, n) : clamp(b, 0, n);
    return from >= to ? str_new("", 0) : str_new(s + from, to - from);
}

// String.prototype.substring(start, end): bounds clamped to the string and
// taken in order.
char *__kml_String_substring(const char *s, double start, bool hasEnd, double end) {
    long long n = str_len(s);
    long long a = clamp(to_integer(start), 0, n), b = hasEnd ? clamp(to_integer(end), 0, n) : n;
    if (a > b) {
        long long t = a;
        a = b;
        b = t;
    }
    return str_new(s + a, b - a);
}

// String.prototype.substr(start, length) (Annex B).
char *__kml_String_substr(const char *s, double start, bool hasLength, double length) {
    long long n = str_len(s);
    double a = to_integer(start);
    long long from = a < 0 ? clamp((double)n + a, 0, n) : clamp(a, 0, n);
    long long len = hasLength ? clamp(to_integer(length), 0, n) : n;
    long long to = from + len > n ? n : from + len;
    return from >= to ? str_new("", 0) : str_new(s + from, to - from);
}

// String.prototype.startsWith(search, position).
bool __kml_String_startsWith(const char *s, const char *search, bool has, double pos) {
    long long n = str_len(s), m = str_len(search);
    long long from = has ? clamp(to_integer(pos), 0, n) : 0;
    return from + m <= n && memcmp(s + from, search, (size_t)m) == 0;
}

// String.prototype.endsWith(search, endPosition).
bool __kml_String_endsWith(const char *s, const char *search, bool has, double end) {
    long long n = str_len(s), m = str_len(search);
    long long to = has && !isnan(end) ? clamp(to_integer(end), 0, n) : n;
    return to - m >= 0 && memcmp(s + to - m, search, (size_t)m) == 0;
}

// String.prototype.lastIndexOf(search, position): the last occurrence at or
// before position (NaN or absent: the end).
double __kml_String_lastIndexOf(const char *s, const char *search, bool has, double pos) {
    long long n = str_len(s), m = str_len(search);
    long long start = has && !isnan(pos) ? clamp(to_integer(pos), 0, n) : n;
    if (start > n - m) start = n - m;
    for (long long i = start; i >= 0; i--) {
        if (memcmp(s + i, search, (size_t)m) == 0) return (double)i;
    }
    return -1;
}

// pad fills s to maxLength with fill (absent: a space), before or after it.
static char *pad(const char *s, double maxLength, bool hasFill, const char *fill, bool atStart) {
    long long n = str_len(s);
    double target = to_integer(maxLength);
    const char *f = hasFill ? fill : " ";
    long long fl = hasFill ? str_len(fill) : 1;
    if (target <= (double)n || fl == 0) return str_new(s, n);
    if (target > (double)maxStringLength) __kml_throw_range_error(str_new("Invalid string length", 21));
    long long total = (long long)target, add = total - n;
    char *base = (char *)malloc((size_t)total + 9);
    *(long long *)base = total;
    char *out = base + 8, *fillAt = atStart ? out : out + n;
    for (long long i = 0; i < add; i++) fillAt[i] = f[i % fl];
    memcpy(atStart ? out + add : out, s, (size_t)n);
    out[total] = 0;
    return out;
}

// String.prototype.padStart / padEnd(maxLength, fillString).
char *__kml_String_padStart(const char *s, double maxLength, bool hasFill, const char *fill) {
    return pad(s, maxLength, hasFill, fill, true);
}

char *__kml_String_padEnd(const char *s, double maxLength, bool hasFill, const char *fill) {
    return pad(s, maxLength, hasFill, fill, false);
}

// String.prototype.at(index): the byte at the index, a negative one counting
// from the end; absent past either end.
bool __kml_String_at(const char *s, double index, char **out) {
    long long n = str_len(s);
    double k = to_integer(index);
    if (k < 0) k += (double)n;
    if (k < 0 || k >= (double)n) return false;
    *out = str_new(s + (long long)k, 1);
    return true;
}

// String.prototype.codePointAt(pos): the code point of the UTF-8 character
// starting at the byte offset (positions are byte offsets in this
// compiler's strings); a byte that starts no valid sequence reads as
// itself. Absent out of range.
bool __kml_String_codePointAt(const char *s, double pos, double *out) {
    long long n = str_len(s);
    double k = to_integer(pos);
    if (k < 0 || k >= (double)n) return false;
    const unsigned char *u = (const unsigned char *)s;
    long long i = (long long)k;
    unsigned char c = u[i];
    int len = c >= 0xF0 && c < 0xF8 ? 4 : c >= 0xE0 ? (c < 0xF0 ? 3 : 0) : c >= 0xC2 ? 2 : 0;
    unsigned int v = c;
    if (len > 0 && i + len <= n) {
        v = c & (len == 2 ? 0x1F : len == 3 ? 0x0F : 0x07);
        for (int j = 1; j < len; j++) {
            if ((u[i + j] & 0xC0) != 0x80) {
                v = c;
                len = 0;
                break;
            }
            v = (v << 6) | (u[i + j] & 0x3F);
        }
    }
    *out = (double)v;
    return true;
}

extern void __kml_dtoa(char *buf, double v);

// js_number writes v the way JavaScript's Number::toString does (shortest
// round-trip digits, `Infinity`, `e+21`), for an error message.
static void js_number(double v, char *buf, size_t size) {
    if (isnan(v)) {
        snprintf(buf, size, "NaN");
        return;
    }
    if (isinf(v)) {
        snprintf(buf, size, v < 0 ? "-Infinity" : "Infinity");
        return;
    }
    (void)size; // buf holds at least 32 bytes
    __kml_dtoa(buf, v);
}

// String.prototype.repeat(count): a negative or infinite count is a
// RangeError naming it; a result longer than Node makes is too.
char *__kml_String_repeat(const char *s, double count) {
    double k = to_integer(count);
    if (k < 0 || isinf(k)) {
        char num[40], msg[80];
        js_number(count, num, sizeof num);
        snprintf(msg, sizeof msg, "Invalid count value: %s", num);
        __kml_throw_range_error(str_new(msg, (long long)strlen(msg)));
    }
    long long n = str_len(s), times = (long long)k;
    if (n == 0 || times == 0) return str_new("", 0);
    if ((double)n * (double)times > (double)maxStringLength) __kml_throw_range_error(str_new("Invalid string length", 21));
    long long total = n * times;
    char *base = (char *)malloc((size_t)total + 9);
    *(long long *)base = total;
    char *out = base + 8;
    for (long long i = 0; i < times; i++) memcpy(out + i * n, s, (size_t)n);
    out[total] = 0;
    return out;
}

// An array's header: the cell a JavaScript array value refers to, its
// elements and their count.
typedef struct {
    void *data;
    long long len;
} array_header;

// String.prototype.concat(...strings): the receiver followed by each
// argument, already converted by ToString.
char *__kml_String_concat(const char *s, const array_header *strings) {
    const char **xs = (const char **)strings->data;
    long long n = str_len(s);
    for (long long i = 0; i < strings->len; i++) {
        n += str_len(xs[i]);
        if (n > maxStringLength) __kml_throw_range_error("Invalid string length");
    }
    char *base = (char *)malloc((size_t)n + 9);
    *(long long *)base = n;
    char *out = base + 8, *p = out;
    memcpy(p, s, (size_t)str_len(s));
    p += str_len(s);
    for (long long i = 0; i < strings->len; i++) {
        memcpy(p, xs[i], (size_t)str_len(xs[i]));
        p += str_len(xs[i]);
    }
    *p = 0;
    return out;
}

// ToUint32(v).
static unsigned long long to_uint32(double v) {
    if (isnan(v) || isinf(v)) return 0;
    double m = fmod(trunc(v), 4294967296.0);
    if (m < 0) m += 4294967296.0;
    return (unsigned long long)m;
}

// utf8_len is the byte length of the UTF-8 sequence starting with lead byte c.
static long long utf8_len(unsigned char c) {
    if (c < 0x80) return 1;
    if ((c & 0xE0) == 0xC0) return 2;
    if ((c & 0xF0) == 0xE0) return 3;
    if ((c & 0xF8) == 0xF0) return 4;
    return 1;
}

// String.prototype.split(separator, limit) with a string separator (NULL:
// undefined, which leaves the string whole): the pieces between
// occurrences, at most limit (ToUint32) of them. An empty
// separator splits into characters (UTF-8 sequences: one per UTF-16 unit
// for the Basic Multilingual Plane).
array_header *__kml_String_split(const char *s, const char *sep, bool hasLimit, double limit) {
    array_header *h = (array_header *)malloc(sizeof(array_header));
    h->data = NULL;
    h->len = 0;
    unsigned long long lim = hasLimit ? to_uint32(limit) : 4294967295ULL;
    if (lim == 0) return h;
    if (sep == NULL) {
        // An undefined separator: the whole string, unsplit.
        char **one = (char **)malloc(sizeof(char *));
        one[0] = str_new(s, str_len(s));
        h->data = one;
        h->len = 1;
        return h;
    }
    long long n = str_len(s), m = str_len(sep);
    if (n == 0) {
        if (m == 0) return h;
        char **one = (char **)malloc(sizeof(char *));
        one[0] = str_new("", 0);
        h->data = one;
        h->len = 1;
        return h;
    }
    long long cap = 8, count = 0;
    char **out = (char **)malloc((size_t)cap * sizeof(char *));
    long long start = 0;
    while ((unsigned long long)count < lim) {
        long long at;
        if (m == 0) {
            if (start >= n) break;
            long long k = utf8_len((unsigned char)s[start]);
            if (start + k > n) k = n - start;
            at = start + k;
        } else {
            at = index_from(s, sep, start);
            if (at < 0) at = n;
        }
        if (count == cap) {
            cap *= 2;
            out = (char **)realloc(out, (size_t)cap * sizeof(char *));
        }
        out[count++] = str_new(s + start, at - start);
        if (m == 0) {
            start = at;
            continue;
        }
        if (at >= n) break;
        start = at + m;
    }
    h->data = out;
    h->len = count;
    return h;
}

// __kml_url_whatwg_encode prepares a URL string for the URL parser the way
// the WHATWG URL Standard reads it: leading and trailing C0 controls and
// spaces are stripped, tabs and newlines removed, and the path, query and
// fragment percent-encoded with their encode sets (uppercase hex; a non-ASCII
// byte always). The authority is left for the parser. A plain C string.
static bool url_in_set(unsigned char c, int part, bool special) {
    if (c < 0x20 || c == 0x7f || c >= 0x80) return true; // C0 control set
    switch (part) {
    case 4: // an opaque path (`mailto:a b`)
        return false;
    case 3: // fragment
        return c == ' ' || c == '"' || c == '<' || c == '>' || c == '`';
    case 2: // query
        return c == ' ' || c == '"' || c == '#' || c == '<' || c == '>' || (special && c == '\'');
    default: // path
        return c == ' ' || c == '"' || c == '#' || c == '<' || c == '>' || c == '?' ||
               c == '^' || c == '`' || c == '{' || c == '}';
    }
}

static bool url_scheme_is(const char *p, size_t n, const char *name) {
    if (strlen(name) != n) return false;
    for (size_t i = 0; i < n; i++)
        if ((p[i] | 32) != name[i]) return false;
    return true;
}

char *__kml_url_whatwg_encode(const char *in) {
    size_t n = strlen(in), a = 0, b = n;
    while (a < b && (unsigned char)in[a] <= 0x20) a++;
    while (b > a && (unsigned char)in[b - 1] <= 0x20) b--;
    char *out = (char *)malloc((b - a) * 3 + 1), *o = out;
    // The scheme, and whether it is a special one.
    size_t i = a, s = a;
    bool special = false;
    if (i < b && ((in[i] | 32) >= 'a' && (in[i] | 32) <= 'z')) {
        while (s < b && (((in[s] | 32) >= 'a' && (in[s] | 32) <= 'z') || (in[s] >= '0' && in[s] <= '9') || in[s] == '+' || in[s] == '-' || in[s] == '.')) s++;
        if (s < b && in[s] == ':') {
            static const char *specials[] = {"http", "https", "ws", "wss", "ftp", "file"};
            for (size_t k = 0; k < sizeof specials / sizeof *specials; k++)
                if (url_scheme_is(in + a, s - a, specials[k])) special = true;
        } else {
            s = a; // no scheme: a relative reference
        }
    }
    int part = 0; // 0 scheme/authority, 1 path, 2 query, 3 fragment
    bool authority = false;
    if (s > a || (b - a >= 2 && in[a] == '/' && in[a + 1] == '/')) {
        // Copy the scheme; an authority follows `//`.
        for (; i < s + (s > a ? 1 : 0); i++) *o++ = in[i];
        if (i + 1 < b && in[i] == '/' && in[i + 1] == '/') authority = true;
        else if (s > a) part = special || (i < b && in[i] == '/') ? 1 : 4;
    } else {
        part = 1;
    }
    if (authority) {
        *o++ = '/', *o++ = '/', i += 2;
        size_t ae = i, at = 0;
        while (ae < b && in[ae] != '/' && in[ae] != '?' && in[ae] != '#' && !(special && in[ae] == '\\')) ae++;
        for (size_t k = i; k < ae; k++) if (in[k] == '@') at = k + 1; // past the userinfo
        static const char hex[] = "0123456789ABCDEF";
        for (; i < ae; i++) {
            unsigned char c = (unsigned char)in[i];
            if (c == '\t' || c == '\n' || c == '\r') continue;
            // The userinfo's controls, spaces and non-ASCII bytes are
            // percent-encoded; so are a non-special (opaque) host's. A
            // special host's non-ASCII is left for the IDNA mapping.
            bool enc = i < at ? (c <= 0x20 || c >= 0x7f || c == '"' || c == '<' || c == '>' || c == '`')
                              : (!special && (c < 0x20 || c >= 0x7f));
            if (enc) *o++ = '%', *o++ = hex[c >> 4], *o++ = hex[c & 15];
            else *o++ = (char)c;
        }
        part = 1;
    }
    for (; i < b; i++) {
        unsigned char c = (unsigned char)in[i];
        if (c == '\t' || c == '\n' || c == '\r') continue;
        if (c == '?' && (part == 1 || part == 4)) part = 2, *o++ = '?';
        else if (c == '#' && part < 3) part = 3, *o++ = '#';
        else if (c != '%' && url_in_set(c, part, special)) {
            static const char hex[] = "0123456789ABCDEF";
            *o++ = '%', *o++ = hex[c >> 4], *o++ = hex[c & 15];
        } else {
            *o++ = (char)c;
        }
    }
    *o = 0;
    return out;
}

// An opaque URL (`mailto:a@b`, `urn:x`, `data:,hi`): a non-special scheme not
// followed by `/`, whose path is opaque. The URL parser handles hierarchical
// URLs only, so these are split here. enc is __kml_url_whatwg_encode's result.
static size_t url_opaque_scheme(const char *enc) {
    size_t s = 0;
    if (!(((enc[0] | 32) >= 'a' && (enc[0] | 32) <= 'z'))) return 0;
    while (((enc[s] | 32) >= 'a' && (enc[s] | 32) <= 'z') || (enc[s] >= '0' && enc[s] <= '9') || enc[s] == '+' || enc[s] == '-' || enc[s] == '.') s++;
    if (enc[s] != ':' || enc[s + 1] == '/') return 0;
    static const char *specials[] = {"http", "https", "ws", "wss", "ftp", "file"};
    for (size_t k = 0; k < sizeof specials / sizeof *specials; k++)
        if (url_scheme_is(enc, s, specials[k])) return 0;
    return s;
}

bool __kml_url_is_opaque(const char *enc) { return url_opaque_scheme(enc) > 0; }

// __kml_url_opaque_part is one field of an opaque URL: 0 href, 1 protocol,
// 5 pathname, 6 search, 7 hash, 8 origin, 9 the raw query (NULL when there is
// none); any other part is "".
char *__kml_url_opaque_part(const char *enc, int part) {
    size_t s = url_opaque_scheme(enc), n = strlen(enc);
    const char *rest = enc + s + 1, *q = strchr(rest, '?'), *h = strchr(rest, '#');
    if (q && h && q > h) q = NULL;
    const char *pathEnd = q ? q : h ? h : enc + n;
    char *out;
    switch (part) {
    case 0:
    case 1: {
        size_t len = part == 0 ? n : s + 1;
        out = str_new(enc, (long long)len);
        for (size_t i = 0; i < s; i++) out[i] = (char)(out[i] | (out[i] >= 'A' && out[i] <= 'Z' ? 32 : 0));
        return out;
    }
    case 5:
        return str_new(rest, (long long)(pathEnd - rest));
    case 6: {
        if (!q) return str_new("", 0);
        const char *e = h ? h : enc + n;
        return e - q > 1 ? str_new(q, (long long)(e - q)) : str_new("", 0);
    }
    case 7:
        return h && enc + n - h > 1 ? str_new(h, (long long)(enc + n - h)) : str_new("", 0);
    case 8:
        return str_new("null", 4);
    case 9: {
        if (!q) return NULL;
        const char *e = h ? h : enc + n;
        return str_new(q + 1, (long long)(e - q - 1));
    }
    }
    return str_new("", 0);
}

// __kml_url_opaque_set is an opaque URL's href with one component set, as the
// WHATWG setters do: 6 search, 7 hash (the leading `?`/`#` already stripped;
// empty removes it). A pathname set on an opaque path, or any other part, is
// a no-op.
char *__kml_url_opaque_set(const char *href, int part, const char *value) {
    size_t n = strlen(href), s = url_opaque_scheme(href);
    if (s == 0 || (part != 6 && part != 7)) return str_new(href, (long long)n);
    const char *rest = href + s + 1, *q = strchr(rest, '?'), *h = strchr(rest, '#');
    if (q && h && q > h) q = NULL;
    const char *pathEnd = q ? q : h ? h : href + n;
    const char *qEnd = h ? h : href + n;
    size_t vn = strlen(value);
    char *buf = (char *)malloc(n + vn * 3 + 3), *o = buf;
    memcpy(o, href, (size_t)(pathEnd - href)), o += pathEnd - href;
    static const char hex[] = "0123456789ABCDEF";
    if (part == 6) {
        if (vn > 0) {
            *o++ = '?';
            for (size_t i = 0; i < vn; i++) {
                unsigned char c = (unsigned char)value[i];
                if (url_in_set(c, 2, false)) *o++ = '%', *o++ = hex[c >> 4], *o++ = hex[c & 15];
                else *o++ = (char)c;
            }
        }
        if (h) memcpy(o, h, (size_t)(href + n - h)), o += href + n - h;
    } else {
        if (q) memcpy(o, q, (size_t)(qEnd - q)), o += qEnd - q;
        if (vn > 0) {
            *o++ = '#';
            for (size_t i = 0; i < vn; i++) {
                unsigned char c = (unsigned char)value[i];
                if (url_in_set(c, 3, false)) *o++ = '%', *o++ = hex[c >> 4], *o++ = hex[c & 15];
                else *o++ = (char)c;
            }
        }
    }
    char *out = str_new(buf, (long long)(o - buf));
    free(buf);
    return out;
}

// __kml_url_tuple_origin reports whether a URL of this protocol (`http:`) has
// a tuple origin: http, https, ws, wss and ftp; any other's is "null".
bool __kml_url_tuple_origin(const char *protocol) {
    size_t n = strlen(protocol);
    if (n > 0 && protocol[n - 1] == ':') n--;
    static const char *tuples[] = {"http", "https", "ws", "wss", "ftp"};
    for (size_t k = 0; k < sizeof tuples / sizeof *tuples; k++)
        if (url_scheme_is(protocol, n, tuples[k])) return true;
    return false;
}

// A non-special URL's host is opaque: WHATWG keeps it percent-encoded, where
// the URL parser hands it back decoded. __kml_url_opaque_host_pct re-encodes
// a hostname's controls and non-ASCII bytes, and __kml_url_opaque_href the
// href's authority (the encoder is idempotent), for a non-special protocol;
// a special one's are returned as they are.
static bool url_special_protocol(const char *protocol) {
    size_t n = strlen(protocol);
    if (n > 0 && protocol[n - 1] == ':') n--;
    static const char *specials[] = {"http", "https", "ws", "wss", "ftp", "file"};
    for (size_t k = 0; k < sizeof specials / sizeof *specials; k++)
        if (url_scheme_is(protocol, n, specials[k])) return true;
    return false;
}

char *__kml_url_opaque_host_pct(const char *protocol, const char *host) {
    if (url_special_protocol(protocol)) return (char *)host;
    size_t n = strlen(host);
    char *buf = (char *)malloc(n * 3 + 1), *o = buf;
    static const char hex[] = "0123456789ABCDEF";
    for (size_t i = 0; i < n; i++) {
        unsigned char c = (unsigned char)host[i];
        if (c < 0x20 || c >= 0x7f) *o++ = '%', *o++ = hex[c >> 4], *o++ = hex[c & 15];
        else *o++ = (char)c;
    }
    char *out = str_new(buf, (long long)(o - buf));
    free(buf);
    return out;
}

char *__kml_url_opaque_href(const char *protocol, const char *href) {
    if (url_special_protocol(protocol)) return (char *)href;
    char *enc = __kml_url_whatwg_encode(href);
    char *out = str_new(enc, (long long)strlen(enc));
    free(enc);
    return out;
}

/* ---- Date local time (the host's time zone, as a JavaScript engine reads
 * it). A Date stores UTC milliseconds; the local getters, the component
 * constructor, the local setters and toString go through these. ---- */

/* Days since 1970-01-01 of a proleptic Gregorian date (Hinnant's
 * days_from_civil). */
static long long kml_days_from_civil(long long y, long long m, long long d) {
	y -= m <= 2;
	long long era = (y >= 0 ? y : y - 399) / 400;
	long long yoe = y - era * 400;
	long long doy = (153 * (m + (m > 2 ? -3 : 9)) + 2) / 5 + d - 1;
	long long doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
	return era * 146097 + doe - 719468;
}

/* localtime of the whole seconds holding t_ms; false when out of range. */
static int kml_localtime(long long t_ms, struct tm *out) {
	long long secs = t_ms >= 0 ? t_ms / 1000 : -((-t_ms + 999) / 1000);
	time_t t = (time_t)secs;
#ifdef _WIN32
	return localtime_s(out, &t) == 0;
#else
	return localtime_r(&t, out) != NULL;
#endif
}

/* The local zone's offset from UTC, in milliseconds, at UTC instant t_ms. */
long long __kml_tz_offset_ms(long long t_ms) {
	struct tm lt;
	if (!kml_localtime(t_ms, &lt)) {
		return 0;
	}
	long long secs = t_ms >= 0 ? t_ms / 1000 : -((-t_ms + 999) / 1000);
	long long days = kml_days_from_civil(lt.tm_year + 1900LL, lt.tm_mon + 1LL, lt.tm_mday);
	long long local = ((days * 24 + lt.tm_hour) * 60 + lt.tm_min) * 60 + lt.tm_sec;
	return (local - secs) * 1000;
}

/* The UTC time of a local wall-clock time (its fields composed as if UTC):
 * the offset in force there, found as an engine does, from the offset at
 * the naive reading. */
long long __kml_local_to_utc_ms(long long local_ms) {
	long long guess = local_ms - __kml_tz_offset_ms(local_ms);
	return local_ms - __kml_tz_offset_ms(guess);
}

/* The long names Node prints in Date.prototype.toString for the zone
 * abbreviations the C library gives, by abbreviation and offset (minutes
 * east of UTC; 9999 matches any); any other prints as the abbreviation. */
static const char *kml_tz_long_name(const char *abbr, long long off) {
	static const struct { const char *abbr; int off; const char *name; } names[] = {
		{"UTC", 9999, "Coordinated Universal Time"}, {"GMT", 9999, "Greenwich Mean Time"},
		{"BST", 60, "British Summer Time"}, {"IST", 60, "Irish Standard Time"},
		{"IST", 330, "India Standard Time"}, {"IST", 120, "Israel Standard Time"},
		{"IDT", 180, "Israel Daylight Time"},
		{"WET", 9999, "Western European Standard Time"}, {"WEST", 9999, "Western European Summer Time"},
		{"CET", 9999, "Central European Standard Time"}, {"CEST", 9999, "Central European Summer Time"},
		{"EET", 9999, "Eastern European Standard Time"}, {"EEST", 9999, "Eastern European Summer Time"},
		{"MSK", 9999, "Moscow Standard Time"},
		{"EST", -300, "Eastern Standard Time"}, {"EDT", -240, "Eastern Daylight Time"},
		{"CST", -360, "Central Standard Time"}, {"CDT", -300, "Central Daylight Time"},
		{"CST", 480, "China Standard Time"},
		{"MST", -420, "Mountain Standard Time"}, {"MDT", -360, "Mountain Daylight Time"},
		{"PST", -480, "Pacific Standard Time"}, {"PDT", -420, "Pacific Daylight Time"},
		{"PST", 480, "Philippine Standard Time"},
		{"AKST", 9999, "Alaska Standard Time"}, {"AKDT", 9999, "Alaska Daylight Time"},
		{"HST", 9999, "Hawaii-Aleutian Standard Time"},
		{"AST", -240, "Atlantic Standard Time"}, {"ADT", -180, "Atlantic Daylight Time"},
		{"NST", 9999, "Newfoundland Standard Time"}, {"NDT", 9999, "Newfoundland Daylight Time"},
		{"JST", 9999, "Japan Standard Time"}, {"KST", 9999, "Korean Standard Time"},
		{"HKT", 9999, "Hong Kong Standard Time"}, {"PKT", 9999, "Pakistan Standard Time"},
		{"WIB", 9999, "Western Indonesia Time"}, {"SAST", 9999, "South Africa Standard Time"},
		{"EAT", 9999, "East Africa Time"}, {"WAT", 9999, "West Africa Standard Time"},
		{"CAT", 9999, "Central Africa Time"},
		{"AEST", 9999, "Australian Eastern Standard Time"}, {"AEDT", 9999, "Australian Eastern Daylight Time"},
		{"ACST", 9999, "Australian Central Standard Time"}, {"ACDT", 9999, "Australian Central Daylight Time"},
		{"AWST", 9999, "Australian Western Standard Time"},
		{"NZST", 9999, "New Zealand Standard Time"}, {"NZDT", 9999, "New Zealand Daylight Time"},
	};
	for (size_t i = 0; i < sizeof(names) / sizeof(names[0]); i++) {
		if (strcmp(abbr, names[i].abbr) == 0 && (names[i].off == 9999 || names[i].off == off)) {
			return names[i].name;
		}
	}
	return abbr;
}

/* Writes " GMT+0300 (Eastern European Summer Time)" for UTC instant t_ms
 * into buf (at least 80 bytes). */
void __kml_tz_suffix(long long t_ms, char *buf) {
	long long east = __kml_tz_offset_ms(t_ms) / 60000;
	long long off = east < 0 ? -east : east;
	char sign = east < 0 ? '-' : '+';
	char abbr[64] = "UTC";
	struct tm lt;
	if (kml_localtime(t_ms, &lt)) {
		if (strftime(abbr, sizeof abbr, "%Z", &lt) == 0) {
			strcpy(abbr, "UTC");
		}
	}
	snprintf(buf, 80, " GMT%c%02lld%02lld (%s)", sign, off / 60, off % 60, kml_tz_long_name(abbr, east));
}

/* ---- Date.parse: the ECMAScript date-time string format (a date-only form
 * is UTC, a date-time without an offset local time) and the legacy forms
 * V8 accepts beside it (toString's and toUTCString's output, "Jul 15 2024",
 * "2024/07/15 10:30", "July 15, 2024"). NaN when unparseable. ---- */

static long long kml_compose_ms(long long y, long long mon1, long long d, long long h, long long mi, long long s, long long ms) {
	/* month carries into the year, as MakeDay does */
	long long m0 = mon1 - 1;
	y += m0 >= 0 ? m0 / 12 : -((-m0 + 11) / 12);
	m0 = ((m0 % 12) + 12) % 12;
	long long days = kml_days_from_civil(y, m0 + 1, 1) + (d - 1);
	return ((days * 24 + h) * 60 + mi) * 60000 + s * 1000 + ms;
}

static int kml_digits(const char **p, int min, int max, long long *out) {
	int n = 0;
	long long v = 0;
	while (n < max && (*p)[n] >= '0' && (*p)[n] <= '9') {
		v = v * 10 + ((*p)[n] - '0');
		n++;
	}
	if (n < min) {
		return 0;
	}
	*p += n;
	*out = v;
	return 1;
}

/* The ECMAScript format; returns 1 and sets *out, or 0. */
static int kml_parse_iso(const char *p, double *out) {
	long long y, mon = 1, d = 1, h = 0, mi = 0, s = 0, ms = 0;
	int sign = 1;
	if (*p == '+' || *p == '-') {
		sign = *p == '-' ? -1 : 1;
		p++;
		if (!kml_digits(&p, 6, 6, &y)) return 0;
		if (sign < 0 && y == 0) return 0; /* -000000 is invalid */
		y *= sign;
	} else if (!kml_digits(&p, 4, 4, &y)) {
		return 0;
	}
	int has_time = 0;
	if (*p == '-') {
		p++;
		if (!kml_digits(&p, 2, 2, &mon) || mon < 1 || mon > 12) return 0;
		if (*p == '-') {
			p++;
			if (!kml_digits(&p, 2, 2, &d) || d < 1 || d > 31) return 0;
		}
	}
	if (*p == 'T' || *p == 't') {
		p++;
		has_time = 1;
		if (!kml_digits(&p, 2, 2, &h) || *p != ':') return 0;
		p++;
		if (!kml_digits(&p, 2, 2, &mi)) return 0;
		if (*p == ':') {
			p++;
			if (!kml_digits(&p, 2, 2, &s)) return 0;
			if (*p == '.' || *p == ',') {
				p++;
				long long frac;
				const char *start = p;
				if (!kml_digits(&p, 1, 9, &frac)) return 0;
				int n = (int)(p - start);
				while (n < 3) { frac *= 10; n++; }
				while (n > 3) { frac /= 10; n--; }
				ms = frac;
			}
		}
		if (h > 24 || mi > 59 || s > 59 || (h == 24 && (mi || s || ms))) return 0;
	}
	int has_offset = 0;
	long long off_min = 0;
	if (*p == 'Z' || *p == 'z') {
		p++;
		has_offset = 1;
	} else if (has_time && (*p == '+' || *p == '-')) {
		int osign = *p == '-' ? -1 : 1;
		p++;
		long long oh, om;
		if (!kml_digits(&p, 2, 2, &oh)) return 0;
		if (*p == ':') p++;
		if (!kml_digits(&p, 2, 2, &om)) return 0;
		has_offset = 1;
		off_min = osign * (oh * 60 + om);
	}
	if (*p != '\0') return 0;
	long long t = kml_compose_ms(y, mon, d, h, mi, s, ms);
	if (has_offset) {
		t -= off_min * 60000;
	} else if (has_time) {
		t = __kml_local_to_utc_ms(t);
	}
	*out = (double)t;
	return 1;
}

static int kml_month_of(const char *w, int n) {
	static const char *const months[] = {"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"};
	if (n < 3) return -1;
	for (int i = 0; i < 12; i++) {
		int eq = 1;
		for (int k = 0; k < 3; k++) {
			char c = w[k] | 0x20;
			if (c != months[i][k]) { eq = 0; break; }
		}
		if (eq) return i + 1;
	}
	return -1;
}

/* The legacy forms: words, numbers, a time h:m[:s], a zone (GMT, UTC, Z,
 * ±hhmm). Local time unless a zone is given. */
static int kml_parse_legacy(const char *p, double *out) {
	long long nums[3];
	int nn = 0, month = -1, has_zone = 0, pm = -1, has_slash = 0;
	long long h = 0, mi = 0, s = 0, off_min = 0;
	while (*p) {
		char c = *p;
		if (c == ' ' || c == ',' || c == '\t') { p++; continue; }
		if (c == '(') { /* a zone name comment */
			while (*p && *p != ')') p++;
			if (*p) p++;
			continue;
		}
		if ((c | 0x20) >= 'a' && (c | 0x20) <= 'z') {
			const char *w = p;
			while (((*p | 0x20) >= 'a' && (*p | 0x20) <= 'z') || *p == '.') p++;
			int n = (int)(p - w);
			int m = kml_month_of(w, n);
			if (m > 0) { month = m; continue; }
			if ((n == 3 && strncmp(w, "GMT", 3) == 0) || (n == 3 && strncmp(w, "UTC", 3) == 0) || (n == 1 && (*w == 'Z' || *w == 'z'))) {
				has_zone = 1;
				continue;
			}
			if (n == 2 && ((w[0] | 0x20) == 'a' || (w[0] | 0x20) == 'p') && (w[1] | 0x20) == 'm') {
				pm = (w[0] | 0x20) == 'p';
				continue;
			}
			continue; /* a weekday or other word */
		}
		if ((c == '+' || c == '-') && p[1] >= '0' && p[1] <= '9' && (has_zone || nn >= 2)) {
			int osign = c == '-' ? -1 : 1;
			p++;
			long long v;
			const char *start = p;
			if (!kml_digits(&p, 1, 4, &v)) return 0;
			long long oh = (p - start) <= 2 ? v : v / 100, om = (p - start) <= 2 ? 0 : v % 100;
			if (*p == ':') { p++; if (!kml_digits(&p, 2, 2, &om)) return 0; }
			off_min = osign * (oh * 60 + om);
			has_zone = 1;
			continue;
		}
		if (c >= '0' && c <= '9') {
			long long v;
			if (!kml_digits(&p, 1, 6, &v)) return 0;
			if (*p == ':') { /* a time */
				h = v;
				p++;
				if (!kml_digits(&p, 1, 2, &mi)) return 0;
				if (*p == ':') { p++; if (!kml_digits(&p, 1, 2, &s)) return 0; }
				if (*p == '.') { long long f; p++; kml_digits(&p, 1, 9, &f); }
				continue;
			}
			if (nn == 3) return 0;
			nums[nn++] = v;
			if (*p == '/' || *p == '-') { has_slash = 1; p++; }
			continue;
		}
		return 0;
	}
	long long y, mon, d;
	if (month > 0) {
		if (nn != 2) return 0;
		mon = month;
		if (nums[0] > 31) { y = nums[0]; d = nums[1]; } else { d = nums[0]; y = nums[1]; }
	} else {
		if (nn != 3 || !has_slash) return 0;
		if (nums[0] > 31) { y = nums[0]; mon = nums[1]; d = nums[2]; }
		else { mon = nums[0]; d = nums[1]; y = nums[2]; }
	}
	if (y < 50) y += 2000; else if (y < 100) y += 1900;
	if (pm >= 0) { if (h == 12) h = 0; if (pm) h += 12; }
	if (mon < 1 || mon > 12 || d < 1 || d > 31 || h > 24 || mi > 59 || s > 59) return 0;
	long long t = kml_compose_ms(y, mon, d, h, mi, s, 0);
	if (has_zone) {
		t -= off_min * 60000;
	} else {
		t = __kml_local_to_utc_ms(t);
	}
	*out = (double)t;
	return 1;
}

double __kml_date_parse_str(const char *s) {
	double t;
	while (*s == ' ' || *s == '\t' || *s == '\n') s++;
	if (kml_parse_iso(s, &t) || kml_parse_legacy(s, &t)) {
		/* the time value range, ±8.64e15 ms */
		if (t < -8.64e15 || t > 8.64e15) return NAN;
		return t;
	}
	return NAN;
}

/* ---- RegExp exec's `groups`: a null-prototype dictionary of the named
 * capture groups, an unmatched one undefined (the null pointer); NULL when
 * the pattern names none (groups: undefined). ---- */

extern void *__kml_map_str_create(void);
extern void __kml_map_str_set(void *map, char *key, long long val);
extern char *__kml_str_alloc(long long n);

#define KML_PCRE2_UNSET_OFF (~(size_t)0)

static char *kml_headered_copy(const char *s, size_t n) {
	char *d = __kml_str_alloc((long long)n);
	memcpy(d, s, n);
	d[n] = '\0';
	return d;
}

/* count, esize and table are the pattern's PCRE2_INFO_NAMECOUNT,
 * NAMEENTRYSIZE and NAMETABLE, which the caller reads (so this file needs
 * no PCRE2 of its own). */
void *__kml_regex_groups(unsigned int count, unsigned int esize, const unsigned char *table, const char *subject, const size_t *ovec, int rc) {
	if (count == 0 || table == NULL) {
		return NULL;
	}
	void *map = __kml_map_str_create();
	*(long long *)((char *)map + 56) |= 1; /* a null-prototype dictionary */
	/* The name table is sorted by name; JavaScript lists the groups in the
	 * pattern's order, so insert by group number. */
	unsigned int maxn = 0;
	for (unsigned int i = 0; i < count; i++) {
		const unsigned char *e = table + (size_t)i * esize;
		unsigned int n = ((unsigned int)e[0] << 8) | e[1];
		if (n > maxn) {
			maxn = n;
		}
	}
	for (unsigned int want = 1; want <= maxn; want++) {
		for (unsigned int i = 0; i < count; i++) {
			const unsigned char *e = table + (size_t)i * esize;
			unsigned int n = ((unsigned int)e[0] << 8) | e[1];
			if (n != want) {
				continue;
			}
			char *name = kml_headered_copy((const char *)e + 2, strlen((const char *)e + 2));
			long long val = 0;
			if ((int)n < rc && ovec[2 * n] != KML_PCRE2_UNSET_OFF) {
				val = (long long)(intptr_t)kml_headered_copy(subject + ovec[2 * n], ovec[2 * n + 1] - ovec[2 * n]);
			}
			__kml_map_str_set(map, name, val);
		}
	}
	return map;
}
