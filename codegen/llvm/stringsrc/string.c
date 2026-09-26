// string.c — String.prototype methods the builtin declarations (lib/es.d.ts)
// lower to. A string is a length-headered byte buffer: the i64 length sits in
// the 8 bytes before the pointer. Positions are byte offsets, as everywhere in
// the string layer. An optional parameter arrives as a presence flag and a
// value.
#include <math.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

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

// String.prototype.codePointAt(pos): the byte's value; absent out of range.
bool __kml_String_codePointAt(const char *s, double pos, double *out) {
    double k = to_integer(pos);
    if (k < 0 || k >= (double)str_len(s)) return false;
    *out = (double)(unsigned char)s[(long long)k];
    return true;
}

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
    if (v == trunc(v) && fabs(v) < 1e21) {
        snprintf(buf, size, "%.0f", v == 0 ? 0.0 : v);
        return;
    }
    for (int p = 1; p <= 17; p++) {
        snprintf(buf, size, "%.*g", p, v);
        if (strtod(buf, NULL) == v) break;
    }
    // C's exponent is `e-07`/`e+21`; JavaScript's `e-7`/`e+21`.
    char *e = strchr(buf, 'e');
    if (e) {
        char sign = e[1], *d = e + 2;
        while (*d == '0' && d[1]) d++;
        memmove(e + 2, d, strlen(d) + 1);
        e[1] = sign;
    }
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
