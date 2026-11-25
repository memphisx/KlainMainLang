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
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "casemap_tables.h"
#include "normalize_tables.h"

static char *str_alloc(long long n) {
    char *base = (char *)kml_alloc(KML_HEAP_RAW, (size_t)n + 9);
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

static unsigned int norm_ccc(unsigned int cp);

// The language-sensitive mappings of SpecialCasing.txt (ICU's, which
// toLocaleUpperCase/toLocaleLowerCase follow): lang 1 is Turkish/Azeri,
// 2 Lithuanian. Each context test walks the UTF-8 past combining marks.
static int cp_at(const unsigned char *s, long long i, long long n, unsigned int *cp) {
    int valid;
    int l = decode(s, i, n, cp, &valid);
    if (!valid) *cp = 0xFFFFFFFFu;
    return l;
}
static long long cp_before(const unsigned char *s, long long i) {
    long long k = i - 1;
    while (k > 0 && (s[k] & 0xC0) == 0x80) k--;
    return k;
}
// Before_Dot: a U+0307 follows, with no character of class 0 or 230 between.
static int before_dot(const unsigned char *s, long long j, long long n) {
    while (j < n) {
        unsigned int cp;
        int l = cp_at(s, j, n, &cp);
        if (cp == 0x307) return 1;
        unsigned int c = norm_ccc(cp);
        if (c == 0 || c == 230) return 0;
        j += l;
    }
    return 0;
}
// More_Above: a class-230 character follows, with no class-0 one between.
static int more_above(const unsigned char *s, long long j, long long n) {
    while (j < n) {
        unsigned int cp;
        int l = cp_at(s, j, n, &cp);
        unsigned int c = norm_ccc(cp);
        if (c == 230) return 1;
        if (c == 0) return 0;
        j += l;
    }
    return 0;
}
static int soft_dotted(unsigned int cp) {
    static const unsigned int sd[] = {0x69, 0x6A, 0x12F, 0x249, 0x268, 0x29D, 0x2B2, 0x3F3, 0x456, 0x458, 0x1D62, 0x1D96, 0x1DA4, 0x1DA8, 0x1E2D, 0x1ECB, 0x2071, 0x2148, 0x2149, 0x2C7C,
        0x1D422, 0x1D423, 0x1D456, 0x1D457, 0x1D48A, 0x1D48B, 0x1D4BE, 0x1D4BF, 0x1D4F2, 0x1D4F3, 0x1D526, 0x1D527, 0x1D55A, 0x1D55B, 0x1D58E, 0x1D58F, 0x1D5C2, 0x1D5C3,
        0x1D5F6, 0x1D5F7, 0x1D62A, 0x1D62B, 0x1D65E, 0x1D65F, 0x1D692, 0x1D693, 0x1DF1A, 0x1E04C, 0x1E04D, 0x1E068};
    for (unsigned k = 0; k < sizeof sd / sizeof sd[0]; k++)
        if (sd[k] == cp) return 1;
    return 0;
}
// After the character `want` (After_I) or a Soft_Dotted one (want 0), with
// no character of class 0 or 230 between.
static int after_base(const unsigned char *s, long long i, long long n, unsigned int want) {
    while (i > 0) {
        long long k = cp_before(s, i);
        unsigned int cp;
        cp_at(s, k, n, &cp);
        unsigned int c = norm_ccc(cp);
        if (c == 0 || c == 230) return want ? cp == want : soft_dotted(cp);
        i = k;
    }
    return 0;
}
// The language mapping of the code point at s[i], into m (its count), or
// -1 for none.
static int lang_map(int lang, int upper, const unsigned char *s, long long i, long long n, int len, unsigned int cp, unsigned int *m) {
    if (lang == 1) {
        if (upper) {
            if (cp == 0x69) { m[0] = 0x130; return 1; }
            return -1;
        }
        if (cp == 0x130) { m[0] = 0x69; return 1; }
        if (cp == 0x307 && after_base(s, i, n, 0x49)) return 0;
        if (cp == 0x49) { m[0] = before_dot(s, i + len, n) ? 0x69 : 0x131; return 1; }
        return -1;
    }
    if (lang == 2) {
        if (upper) return cp == 0x307 && after_base(s, i, n, 0) ? 0 : -1;
        switch (cp) {
        case 0x49: case 0x4A: case 0x12E:
            m[0] = cp == 0x49 ? 0x69 : cp == 0x4A ? 0x6A : 0x12F;
            if (!more_above(s, i + len, n)) return 1;
            m[1] = 0x307;
            return 2;
        case 0xCC: m[0] = 0x69; m[1] = 0x307; m[2] = 0x300; return 3;
        case 0xCD: m[0] = 0x69; m[1] = 0x307; m[2] = 0x301; return 3;
        case 0x128: m[0] = 0x69; m[1] = 0x307; m[2] = 0x303; return 3;
        }
    }
    return -1;
}

static char *convert_lang(const char *src, long long n, int upper, int lang);
static char *convert(const char *src, long long n, int upper) { return convert_lang(src, n, upper, 0); }

static char *convert_lang(const char *src, long long n, int upper, int lang) {
    const unsigned char *s = (const unsigned char *)src;
    // Worst case: every code point expands to three (ﬃ → FFI is 3 bytes → 3
    // ASCII bytes; ᾀ (3 bytes) → ἈΙ (5 bytes)); 2x plus slack covers it all.
    long long cap = n * 2 + 8;
    char *buf = (char *)malloc((size_t)cap); /* scratch */
    long long o = 0;
    for (long long i = 0; i < n;) {
        unsigned int cp; int valid;
        int len = decode(s, i, n, &cp, &valid);
        if (o + 16 > cap) { cap = cap * 2 + 16; buf = (char *)realloc(buf, (size_t)cap); } /* scratch */
        if (!valid) { buf[o++] = (char)s[i]; i += len; continue; }
        if (lang) {
            unsigned int lm[3];
            int c = lang_map(lang, upper, s, i, n, len, cp, lm);
            if (c >= 0) {
                for (int k = 0; k < c; k++) o += encode(lm[k], buf + o);
                i += len;
                continue;
            }
        }
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
    free(buf); /* scratch */
    return out;
}

/* A string's byte length: its header's (an embedded NUL is a character). */
static long long str_bytes(const char *s) { return *(const long long *)(s - 8) & 0xFFFFFFFFLL; }

/* String.prototype.toUpperCase/toLowerCase, the entry points the builtin
   declarations lower to. */
char *__kml_String_toUpperCase(const char *s) { return convert(s, str_bytes(s), 1); }
char *__kml_String_toLowerCase(const char *s) { return convert(s, str_bytes(s), 0); }

/* toLocaleUpperCase/toLocaleLowerCase's core: lang 0 (the default), 1
   (tr, az) or 2 (lt), from the requested locale's language. */
char *__kml_native_locale_case(const char *s, double lang, double upper) {
    return convert_lang(s, str_bytes(s), upper != 0, (int)lang);
}

// ---- String.prototype.normalize (UAX #15) ----
//
// Decompose (canonically, or with the compatibility mappings for NFKC/NFKD;
// Hangul syllables arithmetically), put each run of combining marks in
// canonical order, and for NFC/NFKC recompose the primary composites. A byte
// that is not valid UTF-8 is carried through as itself, a starter nothing
// composes with. Tables in normalize_tables.h (generated, see
// tools/gencasemap/gennormalize.py).

#define NORM_RAW 0x80000000u /* a raw byte carried through */
#define HS_BASE 0xAC00u
#define HL_BASE 0x1100u
#define HV_BASE 0x1161u
#define HT_BASE 0x11A7u
#define HL_COUNT 19u
#define HV_COUNT 21u
#define HT_COUNT 28u
#define HN_COUNT (HV_COUNT * HT_COUNT)
#define HS_COUNT (HL_COUNT * HN_COUNT)

static unsigned int norm_ccc(unsigned int cp) {
    if (cp & NORM_RAW) return 0;
    int lo = 0, hi = KML_NORM_CCC_N - 1;
    while (lo <= hi) {
        int mid = (lo + hi) / 2;
        if (cp < kml_norm_ccc[mid].lo) hi = mid - 1;
        else if (cp > kml_norm_ccc[mid].hi) lo = mid + 1;
        else return kml_norm_ccc[mid].ccc;
    }
    return 0;
}

static const KmlDecomp *norm_find(const KmlDecomp *t, int n, unsigned int cp) {
    int lo = 0, hi = n - 1;
    while (lo <= hi) {
        int mid = (lo + hi) / 2;
        if (cp < t[mid].cp) hi = mid - 1;
        else if (cp > t[mid].cp) lo = mid + 1;
        else return &t[mid];
    }
    return 0;
}

static unsigned int norm_compose(unsigned int a, unsigned int b) {
    if ((a | b) & NORM_RAW) return 0;
    if (a >= HL_BASE && a < HL_BASE + HL_COUNT && b >= HV_BASE && b < HV_BASE + HV_COUNT)
        return HS_BASE + ((a - HL_BASE) * HV_COUNT + (b - HV_BASE)) * HT_COUNT;
    if (a >= HS_BASE && a < HS_BASE + HS_COUNT && (a - HS_BASE) % HT_COUNT == 0 &&
        b > HT_BASE && b < HT_BASE + HT_COUNT)
        return a + (b - HT_BASE);
    int lo = 0, hi = KML_NORM_COMP_N - 1;
    while (lo <= hi) {
        int mid = (lo + hi) / 2;
        const KmlComp *c = &kml_norm_comp[mid];
        if (a < c->a || (a == c->a && b < c->b)) hi = mid - 1;
        else if (a > c->a || (a == c->a && b > c->b)) lo = mid + 1;
        else return c->c;
    }
    return 0;
}

typedef struct { unsigned int *v; long long n, cap; } CpBuf;

static void cp_push(CpBuf *b, unsigned int cp) {
    if (b->n == b->cap) {
        b->cap = b->cap * 2 + 16;
        b->v = (unsigned int *)realloc(b->v, (size_t)b->cap * sizeof(unsigned int)); /* scratch */
    }
    b->v[b->n++] = cp;
}

static void norm_decompose(CpBuf *b, unsigned int cp, int compat) {
    if (!(cp & NORM_RAW) && cp >= HS_BASE && cp < HS_BASE + HS_COUNT) {
        unsigned int si = cp - HS_BASE;
        cp_push(b, HL_BASE + si / HN_COUNT);
        cp_push(b, HV_BASE + (si % HN_COUNT) / HT_COUNT);
        if (si % HT_COUNT) cp_push(b, HT_BASE + si % HT_COUNT);
        return;
    }
    const KmlDecomp *d = 0;
    if (!(cp & NORM_RAW)) {
        if (compat) d = norm_find(kml_norm_compat, KML_NORM_COMPAT_N, cp);
        if (!d) d = norm_find(kml_norm_canon, KML_NORM_CANON_N, cp);
    }
    if (!d) { cp_push(b, cp); return; }
    for (int k = 0; k < d->len; k++) cp_push(b, kml_norm_pool[d->off + k]);
}

// The form, or -1: 0 NFC, 1 NFD, 2 NFKC, 3 NFKD.
static int norm_form(const char *f) {
    static const char *forms[] = {"NFC", "NFD", "NFKC", "NFKD"};
    for (int i = 0; i < 4; i++) if (!strcmp(f, forms[i])) return i;
    return -1;
}

extern void __kml_throw_range_error(const char *msg) __attribute__((noreturn));

char *__kml_String_normalize(const char *src, _Bool hasForm, const char *form) {
    int f = 0;
    if (hasForm) {
        f = norm_form(form ? form : "");
        if (f < 0) {
            static const char msg[] = "The normalization form should be one of NFC, NFD, NFKC, NFKD.";
            char *m = str_alloc((long long)sizeof msg - 1);
            memcpy(m, msg, sizeof msg);
            __kml_throw_range_error(m);
        }
    }
    int compat = f >= 2, compose = f == 0 || f == 2;
    const unsigned char *s = (const unsigned char *)src;
    long long n = str_bytes(src);
    // ASCII is already in every form.
    long long i = 0;
    while (i < n && s[i] < 0x80) i++;
    if (i == n) {
        char *out = str_alloc(n);
        memcpy(out, src, (size_t)n);
        return out;
    }
    CpBuf b = {0, 0, 0};
    for (i = 0; i < n;) {
        unsigned int cp; int valid;
        int len = decode(s, i, n, &cp, &valid);
        norm_decompose(&b, valid ? cp : (NORM_RAW | s[i]), compat);
        i += len;
    }
    // Canonical order: each run of nonzero classes stably sorted.
    for (long long k = 1; k < b.n; k++) {
        unsigned int c = norm_ccc(b.v[k]);
        if (c == 0) continue;
        long long j = k;
        while (j > 0) {
            unsigned int p = norm_ccc(b.v[j - 1]);
            if (p <= c) break;
            unsigned int t = b.v[j - 1]; b.v[j - 1] = b.v[j]; b.v[j] = t;
            j--;
        }
    }
    long long m = b.n;
    if (compose && b.n > 0) {
        long long starter = 0, out = 1;
        unsigned int last = norm_ccc(b.v[0]);
        int have = last == 0; // a starter to compose onto (none before the first)
        for (long long k = 1; k < b.n; k++) {
            unsigned int ch = b.v[k], cc = norm_ccc(ch);
            unsigned int comp = have ? norm_compose(b.v[starter], ch) : 0;
            // Not blocked: adjacent to the starter, or every mark between
            // has a lower class.
            if (comp && (last < cc || last == 0)) {
                b.v[starter] = comp;
                continue;
            }
            if (cc == 0) {
                starter = out;
                have = 1;
            }
            last = cc;
            b.v[out++] = ch;
        }
        m = out;
    }
    char *buf = (char *)malloc((size_t)(m * 4 + 1)); /* scratch */
    long long o = 0;
    for (long long k = 0; k < m; k++) {
        if (b.v[k] & NORM_RAW) buf[o++] = (char)(b.v[k] & 0xFF);
        else o += encode(b.v[k], buf + o);
    }
    char *res = str_alloc(o);
    memcpy(res, buf, (size_t)o);
    free(buf); /* scratch */
    free(b.v); /* scratch */
    return res;
}

// String.fromCharCode / String.fromCodePoint over n numeric arguments, as
// UTF-8. fromCharCode (codePoint 0) takes each argument's ToUint16 and joins
// a high+low surrogate pair into its code point; an unpaired surrogate is
// encoded as itself (3 bytes). fromCodePoint takes each as a code point and,
// for one that is not an integer in [0, 0x10FFFF], returns NULL with *err
// set to the RangeError message ("Invalid code point <n>").
extern void __kml_dtoa(char *buf, double v);

static void fmt_number(double d, char *out) {
    __kml_dtoa(out, d);
}

char *__kml_String_fromCodes(const double *codes, long long n, int codePoint, char **err) {
    unsigned int *cps = (unsigned int *)malloc((size_t)(n > 0 ? n : 1) * sizeof(unsigned int)); /* scratch */
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
                free(cps); /* scratch */
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
    free(cps); /* scratch */
    *err = 0;
    return s;
}
