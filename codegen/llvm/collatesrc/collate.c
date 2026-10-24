/* collate.c — String.prototype.localeCompare: the Unicode Collation
 * Algorithm's three-level comparison over the weights collate_tables.h holds
 * (generated from Node's ICU, root order, see tools/gencollate). A string
 * compares first by the primary weights of its characters (letters, ignoring
 * accents and case), then by the secondary (accents), then by the tertiary
 * (case: lowercase first). A Han ideograph takes its radical-stroke rank
 * after every table entry, a Hangul syllable its jamo; any other character
 * the table does not cover sorts after those, by code point. One ICU
 * ignores adds nothing. */
#include <stdlib.h>
#include <string.h>

typedef long long i64;
typedef struct { unsigned short p, s, t, c; } KmlCE;
typedef struct { unsigned cp; unsigned off; unsigned char n; } KmlCEIndex;
#include "collate_tables.h"

/* The table's elements for code point cp: *n of them at the returned
 * pointer, or NULL when cp is not covered. */
static const KmlCE *ce_lookup(unsigned cp, int *n) {
    size_t lo = 0, hi = sizeof kml_ce_index / sizeof kml_ce_index[0];
    while (lo < hi) {
        size_t mid = (lo + hi) / 2;
        if (kml_ce_index[mid].cp < cp) lo = mid + 1;
        else hi = mid;
    }
    if (lo < sizeof kml_ce_index / sizeof kml_ce_index[0] && kml_ce_index[lo].cp == cp) {
        *n = kml_ce_index[lo].n;
        return kml_ce_pool + kml_ce_index[lo].off;
    }
    return NULL;
}

/* A string's collation elements in order, one at a time. */
typedef struct {
    const unsigned char *p, *end;
    const KmlCE *els;
    int n, k;
    KmlCE one;    /* a computed element: Han or uncovered (case 0) */
    unsigned big; /* its primary, past the table's */
    KmlCE jamo[3]; /* a Hangul syllable's elements */
    int numeric;  /* digit runs are one element, compared by value */
    const unsigned char *run; /* the last such run, leading zeros dropped */
    i64 runlen;
} CEIter;

/* A Han ideograph's radical-stroke rank (1-based), or 0. */
static unsigned han_rank(unsigned cp) {
    if (cp >= 0x3400 && cp <= 0x4DBF) return kml_han_a[cp - 0x3400];
    if (cp >= 0x4E00 && cp <= 0x9FFF) return kml_han_u[cp - 0x4E00];
    if (cp >= 0xF900 && cp <= 0xFAFF) return kml_han_c[cp - 0xF900];
    return 0;
}

/* The flag marking a numeric digit run's weight, above every primary. */
#define KML_RUN 0x80000000u

/* The primary weight digits share under numeric collation: '0''s. */
static unsigned digit_primary(void) {
    int n;
    return ce_lookup('0', &n)->p;
}

static unsigned next_cp(CEIter *it) {
    const unsigned char *p = it->p;
    unsigned c = p[0];
    int w = c < 0x80 ? 1 : c < 0xE0 ? 2 : c < 0xF0 ? 3 : 4;
    if (p + w > it->end) w = (int)(it->end - p);
    unsigned cp = c;
    if (w == 2) cp = (c & 0x1Fu) << 6 | (p[1] & 0x3Fu);
    else if (w == 3) cp = (c & 0x0Fu) << 12 | (p[1] & 0x3Fu) << 6 | (p[2] & 0x3Fu);
    else if (w == 4) cp = (c & 0x07u) << 18 | (p[1] & 0x3Fu) << 12 | (p[2] & 0x3Fu) << 6 | (p[3] & 0x3Fu);
    it->p += w;
    return cp;
}

/* The next element at a level (0 primary, 1 secondary, 2 tertiary, 3 case)
 * whose weight there is not zero: its weight, or 0 at the end. The case level
 * weighs a primary element 1 (lower) or 2 (upper), and others not at all. */
static unsigned ce_next(CEIter *it, int level) {
    for (;;) {
        while (it->k < it->n) {
            const KmlCE *e = &it->els[it->k++];
            int big = e == &it->one;
            unsigned w = level == 0 ? (big ? it->big : e->p) : level == 1 ? e->s : level == 2 ? e->t : (e->p || big) ? 1u + e->c : 0;
            if (w) return w;
        }
        if (it->p >= it->end) return 0;
        if (it->numeric && level == 0 && *it->p >= '0' && *it->p <= '9') {
            while (it->p < it->end && *it->p == '0') it->p++;
            it->run = it->p;
            while (it->p < it->end && *it->p >= '0' && *it->p <= '9') it->p++;
            it->runlen = it->p - it->run;
            return digit_primary() | KML_RUN;
        }
        unsigned cp = next_cp(it);
        int n;
        const KmlCE *els = ce_lookup(cp, &n);
        unsigned han = han_rank(cp);
        if (els) {
            it->els = els;
            it->n = n;
        } else if (cp >= 0xAC00 && cp <= 0xD7A3) {
            /* L V (T) jamo, each the table's element. */
            unsigned si = cp - 0xAC00, jc[3] = {0x1100 + si / 588, 0x1161 + si % 588 / 28, 0x11A7 + si % 28};
            it->n = 0;
            for (int k = 0; k < (si % 28 ? 3 : 2); k++) {
                const KmlCE *j = ce_lookup(jc[k], &n);
                if (j) it->jamo[it->n++] = j[0];
            }
            it->els = it->jamo;
        } else if (han) {
            it->one.p = 0;
            it->one.s = 1;
            it->one.t = 4;
            it->one.c = 0;
            it->big = KML_CE_MAX_PRIMARY + han;
            it->els = &it->one;
            it->n = 1;
        } else {
            it->one.p = 0;
            it->one.s = 1;
            it->one.t = 4;
            it->one.c = 0;
            it->big = KML_CE_MAX_PRIMARY + KML_HAN_COUNT + 1 + cp;
            it->els = &it->one;
            it->n = 1;
        }
        it->k = 0;
    }
}

static int level_cmp(const char *a, i64 na, const char *b, i64 nb, int level, int numeric) {
    CEIter x = {.p = (const unsigned char *)a, .end = (const unsigned char *)a + na, .numeric = numeric};
    CEIter y = {.p = (const unsigned char *)b, .end = (const unsigned char *)b + nb, .numeric = numeric};
    for (;;) {
        unsigned wa = ce_next(&x, level), wb = ce_next(&y, level);
        /* A digit run's weight is '0''s, so it orders against anything else
         * as a digit; two runs compare by value: length, then digits. */
        unsigned pa = wa & ~KML_RUN, pb = wb & ~KML_RUN;
        if (pa != pb) return pa < pb ? -1 : 1;
        if ((wa | wb) & KML_RUN) {
            if (!(wa & wb & KML_RUN)) return wa & KML_RUN ? -1 : 1;
            if (x.runlen != y.runlen) return x.runlen < y.runlen ? -1 : 1;
            int c = memcmp(x.run, y.run, (size_t)x.runlen);
            if (c) return c < 0 ? -1 : 1;
            continue;
        }
        if (wa == 0) return 0;
    }
}

/* Comparison levels: primary (letters), secondary (accents), tertiary
 * (variants, case among them) or case alone, as Intl.Collator's sensitivity
 * selects them. */
enum { KML_COLL_P = 1, KML_COLL_S = 2, KML_COLL_T = 4, KML_COLL_NUMERIC = 8, KML_COLL_CASE = 16 };

/* a.localeCompare(b, locales, options): -1, 0 or 1. flags holds the levels
 * compared and KML_COLL_NUMERIC. */
i64 __kml_collate(const char *a, const char *b, i64 flags) {
    i64 na = KML_STR_LEN(a), nb = KML_STR_LEN(b);
    for (int level = 0; level < 4; level++) {
        if (!(flags & (level == 3 ? KML_COLL_CASE : 1 << level))) continue;
        int r = level_cmp(a, na, b, nb, level, (int)(flags & KML_COLL_NUMERIC) != 0);
        if (r) return r;
    }
    return 0;
}
