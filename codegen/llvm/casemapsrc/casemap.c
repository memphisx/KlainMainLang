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
