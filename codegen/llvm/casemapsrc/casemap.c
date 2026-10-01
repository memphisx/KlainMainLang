// casemap.c — String.prototype.toUpperCase / toLowerCase: the Unicode Default
// Case Conversion (ECMA-262 §22.1.3.29/§22.1.3.27) over a UTF-8 header string.
// Tables in casemap_tables.h (generated, see tools/gencasemap): the simple
// single-code-point mappings as delta ranges, the SpecialCasing expansions
// (ß → SS, İ → i̇, ﬁ → FI, ᾳ → ΑΙ …), and the Cased / Case_Ignorable ranges
// the one context-sensitive mapping needs — Final_Sigma: Σ lowercases to ς
// when preceded by a cased letter and not followed by one, skipping
// case-ignorable characters on both sides ("ΟΔΥΣΣΕΥΣ" → "οδυσσευς").
//
// Length-driven (the 8-byte header before the data, as every runtime string
// carries — embedded NULs are ordinary characters), so a string holding "\0"
// no longer comes back empty. Bytes that are not valid UTF-8 pass through
// unchanged. The ASCII-only strlen-bounded @__kml_toupper/@__kml_tolower in
// runtime_strings.go stay for the runtime's own raw buffers (HTTP header
// names, URL hosts, WebSocket tokens), which are never user strings.
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "casemap_tables.h"

static char *str_alloc(long long n) {
    char *base = (char *)malloc((size_t)n + 9);
    *(long long *)base = n;
    base[n + 8] = 0;
    return base + 8;
}

static int in_ranges(const KmlCpRange *r, int n, unsigned int cp) {
    int lo = 0, hi = n - 1;
    while (lo <= hi) {
        int mid = (lo + hi) / 2;
        if (cp < r[mid].lo) hi = mid - 1;
        else if (cp > r[mid].hi) lo = mid + 1;
        else return 1;
    }
    return 0;
}

static const KmlCaseRange *find_range(unsigned int cp) {
    int lo = 0, hi = KML_CASE_RANGES_N - 1;
    while (lo <= hi) {
        int mid = (lo + hi) / 2;
        if (cp < kml_case_ranges[mid].lo) hi = mid - 1;
        else if (cp > kml_case_ranges[mid].hi) lo = mid + 1;
        else return &kml_case_ranges[mid];
    }
    return NULL;
}

static const KmlCaseSpecial *find_special(unsigned int cp) {
    int lo = 0, hi = KML_CASE_SPECIAL_N - 1;
    while (lo <= hi) {
        int mid = (lo + hi) / 2;
        if (cp < kml_case_special[mid].cp) hi = mid - 1;
        else if (cp > kml_case_special[mid].cp) lo = mid + 1;
        else return &kml_case_special[mid];
    }
    return NULL;
}

// Decode one code point at s[i]; returns its byte length (1 for an invalid
// lead/truncated sequence, which is passed through as the raw byte).
static int decode(const unsigned char *s, long long i, long long n, unsigned int *cp, int *valid) {
    unsigned char c = s[i];
    *valid = 1;
    if (c < 0x80) { *cp = c; return 1; }
    int len = c >= 0xF0 ? 4 : c >= 0xE0 ? 3 : c >= 0xC0 ? 2 : 0;
    if (len == 0 || i + len > n) { *valid = 0; *cp = c; return 1; }
    unsigned int v = c & (len == 2 ? 0x1F : len == 3 ? 0x0F : 0x07);
    for (int k = 1; k < len; k++) {
        if ((s[i + k] & 0xC0) != 0x80) { *valid = 0; *cp = c; return 1; }
        v = (v << 6) | (s[i + k] & 0x3F);
    }
    *cp = v;
    return len;
}

static int encode(unsigned int cp, char *out) {
    if (cp < 0x80) { out[0] = (char)cp; return 1; }
    if (cp < 0x800) { out[0] = (char)(0xC0 | (cp >> 6)); out[1] = (char)(0x80 | (cp & 0x3F)); return 2; }
    if (cp < 0x10000) {
        out[0] = (char)(0xE0 | (cp >> 12)); out[1] = (char)(0x80 | ((cp >> 6) & 0x3F));
        out[2] = (char)(0x80 | (cp & 0x3F)); return 3;
    }
    out[0] = (char)(0xF0 | (cp >> 18)); out[1] = (char)(0x80 | ((cp >> 12) & 0x3F));
    out[2] = (char)(0x80 | ((cp >> 6) & 0x3F)); out[3] = (char)(0x80 | (cp & 0x3F)); return 4;
}

// Final_Sigma context: a cased letter before (skipping case-ignorables) and
// no cased letter after (skipping case-ignorables). Walks the UTF-8 directly.
static int final_sigma(const unsigned char *s, long long i, long long n, int len) {
    // backwards
    long long j = i;
    int before = 0;
    while (j > 0) {
        long long k = j - 1;
        while (k > 0 && (s[k] & 0xC0) == 0x80) k--;
        unsigned int cp; int valid;
        decode(s, k, n, &cp, &valid);
        if (!valid) break;
        if (in_ranges(kml_case_ignorable, KML_CASE_IGNORABLE_N, cp)) { j = k; continue; }
        before = in_ranges(kml_cased, KML_CASED_N, cp);
        break;
    }
    if (!before) return 0;
    // forwards
    j = i + len;
    while (j < n) {
        unsigned int cp; int valid;
        int l = decode(s, j, n, &cp, &valid);
        if (!valid) break;
        if (in_ranges(kml_case_ignorable, KML_CASE_IGNORABLE_N, cp)) { j += l; continue; }
        return !in_ranges(kml_cased, KML_CASED_N, cp);
    }
    return 1;
}

static char *convert(const char *src, long long n, int upper) {
    const unsigned char *s = (const unsigned char *)src;
    // Worst case: every code point expands to three (ﬃ → FFI is 3 bytes → 3
    // ASCII bytes; ᾀ (3 bytes) → ἈΙ (5 bytes)); 2x plus slack covers it all.
    long long cap = n * 2 + 8;
    char *buf = (char *)malloc((size_t)cap);
    long long o = 0;
    for (long long i = 0; i < n;) {
        unsigned int cp; int valid;
        int len = decode(s, i, n, &cp, &valid);
        if (o + 16 > cap) { cap = cap * 2 + 16; buf = (char *)realloc(buf, (size_t)cap); }
        if (!valid) { buf[o++] = (char)s[i]; i += len; continue; }
        const KmlCaseSpecial *sp = find_special(cp);
        if (sp) {
            const unsigned int *m = upper ? sp->up : sp->lo;
            int cnt = upper ? sp->nu : sp->nl;
            for (int k = 0; k < cnt; k++) o += encode(m[k], buf + o);
        } else if (!upper && cp == 0x03A3 && final_sigma(s, i, n, len)) {
            o += encode(0x03C2, buf + o);
        } else {
            const KmlCaseRange *r = find_range(cp);
            if (r) cp = (unsigned int)((int)cp + (upper ? r->du : r->dl));
            o += encode(cp, buf + o);
        }
        i += len;
    }
    char *out = str_alloc(o);
    memcpy(out, buf, (size_t)o);
    free(buf);
    return out;
}

/* String.prototype.toUpperCase/toLowerCase, the entry points the builtin
   declarations lower to. strlen-bounded, as every string boundary is: a
   sidecar-produced string carries no length header. */
char *__kml_String_toUpperCase(const char *s) { return convert(s, (long long)strlen(s), 1); }
char *__kml_String_toLowerCase(const char *s) { return convert(s, (long long)strlen(s), 0); }

// String.fromCharCode / String.fromCodePoint over n numeric arguments, as
// UTF-8. fromCharCode (codePoint 0) takes each argument's ToUint16 and joins
// a high+low surrogate pair into its code point; an unpaired surrogate is
// encoded as itself (3 bytes). fromCodePoint takes each as a code point and,
// for one that is not an integer in [0, 0x10FFFF], returns NULL with *err
// set to the RangeError message ("Invalid code point <n>").
static void fmt_number(double d, char *out) {
    if (d != d) { strcpy(out, "NaN"); return; }
    if (d == 1.0 / 0.0) { strcpy(out, "Infinity"); return; }
    if (d == -1.0 / 0.0) { strcpy(out, "-Infinity"); return; }
    for (int p = 1; p <= 17; p++) {
        snprintf(out, 40, "%.*g", p, d);
        if (strtod(out, 0) == d) return;
    }
}

char *__kml_String_fromCodes(const double *codes, long long n, int codePoint, char **err) {
    unsigned int *cps = (unsigned int *)malloc((size_t)(n > 0 ? n : 1) * sizeof(unsigned int));
    long long m = 0;
    for (long long i = 0; i < n; i++) {
        double d = codes[i];
        if (codePoint) {
            if (!(d >= 0 && d <= 0x10FFFF) || d != (double)(long long)d) {
                char num[48], *msg;
                fmt_number(d, num);
                msg = str_alloc((long long)strlen("Invalid code point ") + (long long)strlen(num));
                strcpy(msg, "Invalid code point ");
                strcat(msg, num);
                free(cps);
                *err = msg;
                return 0;
            }
            cps[m++] = (unsigned int)d;
            continue;
        }
        // ToUint16: NaN/±Infinity are 0; else truncate toward zero, mod 2^16.
        unsigned int u = 0;
        if (d == d && d != 1.0 / 0.0 && d != -1.0 / 0.0) {
            double t = d < 0 ? -__builtin_floor(-d) : __builtin_floor(d);
            /* t mod 2^16, non-negative, without libm (Linux links none):
               exact, t being integral and 2^16 a power of two. */
            double r = t - 65536.0 * __builtin_floor(t / 65536.0);
            u = (unsigned int)r;
        }
        if (m > 0 && u >= 0xDC00 && u <= 0xDFFF && cps[m - 1] >= 0xD800 && cps[m - 1] <= 0xDBFF) {
            cps[m - 1] = 0x10000 + ((cps[m - 1] - 0xD800) << 10) + (u - 0xDC00);
            continue;
        }
        cps[m++] = u;
    }
    long long len = 0;
    char tmp[4];
    for (long long i = 0; i < m; i++) len += encode(cps[i], tmp);
    char *s = str_alloc(len);
    long long o = 0;
    for (long long i = 0; i < m; i++) o += encode(cps[i], s + o);
    free(cps);
    *err = 0;
    return s;
}

/* IDNA for a URL's host (the WHATWG host parser's domain-to-ASCII): a host
   with a non-ASCII byte is lowercased and each non-ASCII label becomes
   `xn--` + its Punycode (RFC 3492). The UTS 46 mapping beyond case folding
   (NFC, width folding) is not applied. */
static char puny_digit(unsigned int d) { return (char)(d < 26 ? 'a' + d : '0' + d - 26); }

static unsigned int puny_adapt(unsigned int delta, unsigned int numpoints, int first) {
    delta = first ? delta / 700 : delta / 2;
    delta += delta / numpoints;
    unsigned int k = 0;
    while (delta > ((36 - 1) * 26) / 2) { delta /= 36 - 1; k += 36; }
    return k + (36 * delta) / (delta + 38);
}

/* Appends the Punycode of cps[0..n) to out (at least 8n + 8 bytes free). */
static long long punycode(const unsigned int *cps, long long n, char *out) {
    long long o = 0, b = 0;
    for (long long i = 0; i < n; i++) if (cps[i] < 0x80) out[o++] = (char)cps[i], b++;
    long long h = b;
    if (b > 0) out[o++] = '-';
    unsigned int nn = 128, delta = 0, bias = 72;
    while (h < n) {
        unsigned int m = 0xFFFFFFFF;
        for (long long i = 0; i < n; i++) if (cps[i] >= nn && cps[i] < m) m = cps[i];
        delta += (m - nn) * (unsigned int)(h + 1);
        nn = m;
        for (long long i = 0; i < n; i++) {
            if (cps[i] < nn) delta++;
            if (cps[i] == nn) {
                unsigned int q = delta;
                for (unsigned int k = 36;; k += 36) {
                    unsigned int t = k <= bias ? 1 : k >= bias + 26 ? 26 : k - bias;
                    if (q < t) break;
                    out[o++] = puny_digit(t + (q - t) % (36 - t));
                    q = (q - t) / (36 - t);
                }
                out[o++] = puny_digit(q);
                bias = puny_adapt(delta, (unsigned int)(h + 1), h == b);
                delta = 0;
                h++;
            }
        }
        delta++, nn++;
    }
    return o;
}

char *__kml_url_idna(const char *url) {
    long long n = (long long)strlen(url);
    const char *sep = strstr(url, "://");
    if (!sep) return (char *)url;
    /* Only a special scheme's host is a domain; any other's is opaque. */
    static const char *specials[] = {"http", "https", "ws", "wss", "ftp"};
    int special = 0;
    for (size_t k = 0; k < sizeof specials / sizeof *specials; k++) {
        size_t sl = strlen(specials[k]);
        if ((size_t)(sep - url) != sl) continue;
        size_t q = 0;
        while (q < sl && (url[q] | 32) == specials[k][q]) q++;
        if (q == sl) special = 1;
    }
    if (!special) return (char *)url;
    long long hs = sep - url + 3, he = hs;
    while (he < n && url[he] != '/' && url[he] != '?' && url[he] != '#' && url[he] != '\\') he++;
    for (long long i = he - 1; i >= hs; i--) if (url[i] == '@') { hs = i + 1; break; } /* past userinfo */
    if (hs < he && url[hs] == '[') return (char *)url; /* an IPv6 literal */
    long long pe = hs;
    while (pe < he && url[pe] != ':') pe++;
    int wide = 0;
    for (long long i = hs; i < pe; i++) if ((unsigned char)url[i] >= 0x80) wide = 1;
    if (!wide) return (char *)url;
    char *host = convert(url + hs, pe - hs, 0); /* lowercased */
    long long hn = (long long)strlen(host);
    char *buf = (char *)malloc((size_t)(n + hn * 10 + 16));
    long long o = 0;
    memcpy(buf, url, (size_t)hs), o = hs;
    unsigned int *cps = (unsigned int *)malloc(sizeof(unsigned int) * (size_t)(hn + 1));
    for (long long i = 0; i <= hn;) {
        long long j = i;
        while (j < hn && host[j] != '.') j++;
        int labelWide = 0;
        for (long long k = i; k < j; k++) if ((unsigned char)host[k] >= 0x80) labelWide = 1;
        if (labelWide) {
            long long c = 0;
            for (long long k = i; k < j;) {
                unsigned int cp; int valid;
                k += decode((const unsigned char *)host, k, j, &cp, &valid);
                cps[c++] = cp;
            }
            memcpy(buf + o, "xn--", 4), o += 4;
            o += punycode(cps, c, buf + o);
        } else {
            memcpy(buf + o, host + i, (size_t)(j - i)), o += j - i;
        }
        if (j < hn) buf[o++] = '.';
        i = j + 1;
    }
    memcpy(buf + o, url + pe, (size_t)(n - pe)), o += n - pe;
    char *out = str_alloc(o);
    memcpy(out, buf, (size_t)o);
    free(buf), free(cps);
    return out;
}

/* __kml_idna_to_unicode is a domain with each `xn--` label decoded from
   Punycode (RFC 3492), the other labels as they are. An undecodable label is
   kept as written. */
static int puny_decode(const char *in, long long n, unsigned int *out, long long *outn, long long cap) {
    long long b = -1;
    for (long long i = 0; i < n; i++) if (in[i] == '-') b = i;
    long long o = 0;
    for (long long i = 0; i < b; i++) { if ((unsigned char)in[i] >= 0x80 || o >= cap) return 0; out[o++] = (unsigned char)in[i]; }
    unsigned int nn = 128, bias = 72, i = 0;
    for (long long p = b > 0 ? b + 1 : 0; p < n;) {
        unsigned int oldi = i, w = 1;
        for (unsigned int k = 36;; k += 36) {
            if (p >= n) return 0;
            char c = in[p++];
            unsigned int d = c >= '0' && c <= '9' ? (unsigned int)(c - '0' + 26) : (c | 32) >= 'a' && (c | 32) <= 'z' ? (unsigned int)((c | 32) - 'a') : 36;
            if (d >= 36) return 0;
            i += d * w;
            unsigned int t = k <= bias ? 1 : k >= bias + 26 ? 26 : k - bias;
            if (d < t) break;
            w *= 36 - t;
        }
        bias = puny_adapt(i - oldi, (unsigned int)(o + 1), oldi == 0);
        nn += i / (unsigned int)(o + 1);
        i %= (unsigned int)(o + 1);
        if (o >= cap) return 0;
        memmove(out + i + 1, out + i, sizeof(unsigned int) * (size_t)(o - i));
        out[i++] = nn;
        o++;
    }
    *outn = o;
    return 1;
}

char *__kml_idna_to_unicode(const char *host) {
    long long n = (long long)strlen(host);
    char *buf = (char *)malloc((size_t)(n * 4 + 8));
    unsigned int *cps = (unsigned int *)malloc(sizeof(unsigned int) * (size_t)(n + 1));
    long long o = 0;
    for (long long i = 0; i <= n;) {
        long long j = i;
        while (j < n && host[j] != '.') j++;
        long long cn = 0;
        if (j - i > 4 && (host[i] | 32) == 'x' && (host[i + 1] | 32) == 'n' && host[i + 2] == '-' && host[i + 3] == '-' &&
            puny_decode(host + i + 4, j - i - 4, cps, &cn, n)) {
            for (long long k = 0; k < cn; k++) o += encode(cps[k], buf + o);
        } else {
            memcpy(buf + o, host + i, (size_t)(j - i)), o += j - i;
        }
        if (j < n) buf[o++] = '.';
        i = j + 1;
    }
    char *out = str_alloc(o);
    memcpy(out, buf, (size_t)o);
    free(buf), free(cps);
    return out;
}
