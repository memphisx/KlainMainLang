/* holes.c — an array's holes (TDD-00226): the elements a gap write,
 * `new Array(n)` or an elided literal element left unset. A header's holes
 * word is null while the array is packed; the first hole points it at a
 * bitmap { i64 nwords, i64 tailFrom, i64 tailTo, u64 bits[nwords] }, one
 * bit per element, set for a hole, and one run of holes [tailFrom, tailTo)
 * held as its bounds: `length = 4294967295` makes 2^32 holes without
 * writing their bits. An index past both is no hole, so appending needs no
 * growth. */
#include <stdio.h>
#include <string.h>

typedef long long i64;
typedef unsigned long long u64;

typedef struct {
    void *data;
    i64 len;
    i64 *holes;
} Hdr;

#define BM_WORDS(bm) ((bm)[0])
#define BM_T0(bm) ((bm)[1])
#define BM_T1(bm) ((bm)[2])
#define BM_BITS(bm) ((u64 *)((bm) + 3))
/* A run of holes at least this long is held as the bitmap's run. */
#define BM_RUN_MIN 4096

/* The bitmap of hdr grown to cover n elements' bits. */
static i64 *holes_cover(Hdr *h, i64 n) {
    i64 need = (n + 63) / 64, have = h->holes ? BM_WORDS(h->holes) : 0;
    if (h->holes && need <= have) return h->holes;
    if (need < have) need = have;
    i64 *bm = (i64 *)kml_alloc(KML_HEAP_RAW, (size_t)(need + 3) * 8);
    memset(bm, 0, (size_t)(need + 3) * 8);
    if (h->holes) {
        memcpy(BM_BITS(bm), BM_BITS(h->holes), (size_t)have * 8);
        BM_T0(bm) = BM_T0(h->holes);
        BM_T1(bm) = BM_T1(h->holes);
    }
    BM_WORDS(bm) = need;
    h->holes = bm;
    return bm;
}

/* Sets (on) or clears bits [from, to) of a bitmap's words, a word at a
 * time between its partial ends. */
static void bits_range(u64 *w, i64 from, i64 to, int on) {
    if (from >= to) return;
    i64 a = from >> 6, b = (to - 1) >> 6;
    u64 lo = ~0ULL << (from & 63), hi = ~0ULL >> (63 - ((to - 1) & 63));
    if (a == b) {
        if (on) w[a] |= lo & hi; else w[a] &= ~(lo & hi);
        return;
    }
    if (on) w[a] |= lo; else w[a] &= ~lo;
    if (b > a + 1) memset(w + a + 1, on ? 0xff : 0, (size_t)(b - a - 1) * 8);
    if (on) w[b] |= hi; else w[b] &= ~hi;
}

/* Bits [from, to) of hdr set. */
static void bits_mark(Hdr *h, i64 from, i64 to) {
    if (from >= to) return;
    i64 *bm = holes_cover(h, to);
    bits_range(BM_BITS(bm), from, to, 1);
}

/* Makes elements [from, to) of hdr holes. */
void __kml_holes_mark(void *hdr, i64 from, i64 to) {
    Hdr *h = (Hdr *)hdr;
    if (from >= to) return;
    i64 *bm = h->holes ? h->holes : holes_cover(h, 0);
    i64 t0 = BM_T0(bm), t1 = BM_T1(bm);
    if (t0 >= t1 && to - from >= BM_RUN_MIN) { /* no run yet: this one */
        BM_T0(bm) = from;
        BM_T1(bm) = to;
        return;
    }
    if (t0 < t1 && from <= t1 && to >= t0) { /* touching the run: grow it */
        BM_T0(bm) = from < t0 ? from : t0;
        BM_T1(bm) = to > t1 ? to : t1;
        return;
    }
    bits_mark(h, from, to);
}

/* Makes elements [from, to) of hdr no holes. */
static void holes_unmark(void *hdr, i64 from, i64 to) {
    Hdr *h = (Hdr *)hdr;
    i64 *bm = h->holes;
    if (!bm || from >= to) return;
    if (from < 0) from = 0;
    i64 t0 = BM_T0(bm), t1 = BM_T1(bm);
    if (t0 < t1 && from < t1 && to > t0) {
        if (from <= t0 && to >= t1) {
            BM_T0(bm) = BM_T1(bm) = 0; /* all of it */
        } else if (from <= t0) {
            BM_T0(bm) = to; /* its front */
        } else if (to >= t1) {
            BM_T1(bm) = from; /* its back */
        } else {
            /* Inside: the part before becomes bits, the run goes on after. */
            bits_mark(h, t0, from);
            bm = h->holes;
            BM_T0(bm) = to;
        }
    }
    i64 cap = BM_WORDS(bm) * 64;
    if (to > cap) to = cap;
    bits_range(BM_BITS(bm), from, to, 0);
}

/* Whether element i of hdr's holes bit is set: a hole or an accessor
 * element. */
static int hole_bit(void *hdr, i64 i) {
    i64 *bm = ((Hdr *)hdr)->holes;
    if (!bm || i < 0) return 0;
    if (i >= BM_T0(bm) && i < BM_T1(bm)) return 1;
    if ((i >> 6) >= BM_WORDS(bm)) return 0;
    return (BM_BITS(bm)[i >> 6] >> (i & 63)) & 1;
}

/* An accessor element's slot made to hold its getter's result, as a read
 * of the element gets it; 0 when element i is no accessor element. The
 * array runtime (dynjson.c) defines it when a program can make one. */
__attribute__((weak)) int __kml_elem_refresh(void *hdr, i64 i) {
    (void)hdr; (void)i;
    return 0;
}

/* Whether element i of hdr is a hole. An accessor element is none: the
 * test that guards every element read gets its value into its slot. */
int __kml_holes_is(void *hdr, i64 i) {
    return hole_bit(hdr, i) && !__kml_elem_refresh(hdr, i);
}

/* Whether element i of hdr is a hole or an accessor element (no getter
 * run). */
int __kml_holes_raw(void *hdr, i64 i) { return hole_bit(hdr, i); }

/* Element i of hdr is set: no longer a hole. */
void __kml_holes_fill(void *hdr, i64 i) { holes_unmark(hdr, i, i + 1); }

/* Elements [from, from+n) of src's holes become those of [at, at+n) of dst. */
void __kml_holes_copy(void *dst, i64 at, void *src, i64 from, i64 n) {
    if (!((Hdr *)src)->holes) return;
    for (i64 k = 0; k < n; k++)
        if (__kml_holes_is(src, from + k)) __kml_holes_mark(dst, at + k, at + k + 1);
}

/* The length of the run of holes starting at element i of hdr, up to n. */
i64 __kml_holes_run(void *hdr, i64 i, i64 n) {
    i64 j = i;
    while (j < n && __kml_holes_is(hdr, j)) j++;
    return j - i;
}

/* The number of holes in elements [0, n) of hdr. */
i64 __kml_holes_count(void *hdr, i64 n) {
    i64 *bm = ((Hdr *)hdr)->holes, c = 0;
    if (!bm) return 0;
    i64 end = BM_WORDS(bm) * 64;
    if (BM_T0(bm) < BM_T1(bm) && BM_T1(bm) > end) end = BM_T1(bm);
    if (end > n) end = n;
    for (i64 i = 0; i < end; i++) c += __kml_holes_is(hdr, i);
    return c;
}

/* An array of oldLen elements had `removed` elements at `at` replaced by
 * `inserted` set ones (splice, shift, unshift, pop): the holes after them
 * move along. */
void __kml_holes_splice(void *hdr, i64 at, i64 removed, i64 inserted, i64 oldLen) {
    Hdr *h = (Hdr *)hdr;
    if (!h->holes) return;
    i64 newLen = oldLen - removed + inserted, tail = oldLen - at - removed;
    unsigned char *was = (unsigned char *)kml_alloc(KML_HEAP_RAW, (size_t)(tail > 0 ? tail : 1));
    for (i64 k = 0; k < tail; k++) was[k] = (unsigned char)__kml_holes_is(hdr, at + removed + k);
    holes_unmark(hdr, at, oldLen);
    if (newLen > 0) holes_cover(h, newLen);
    for (i64 k = 0; k < tail; k++)
        if (was[k]) __kml_holes_mark(hdr, at + inserted + k, at + inserted + k + 1);
}

/* The length of an array went from old to n: a shorter array drops its
 * holes past n, a longer one gains holes up to n. */
void __kml_holes_setlen(void *hdr, i64 old, i64 n) {
    if (n > old) { __kml_holes_mark(hdr, old, n); return; }
    holes_unmark(hdr, n, old);
}

/* Elements [from, to) were all set (fill). */
void __kml_holes_clear(void *hdr, i64 from, i64 to) {
    holes_unmark(hdr, from, to);
}

/* The n elements were reversed: so are their holes. */
void __kml_holes_reverse(void *hdr, i64 n) {
    if (!((Hdr *)hdr)->holes) return;
    for (i64 i = 0, j = n - 1; i < j; i++, j--) {
        int a = __kml_holes_is(hdr, i), b = __kml_holes_is(hdr, j);
        if (a != b) {
            if (a) { __kml_holes_fill(hdr, i); __kml_holes_mark(hdr, j, j + 1); }
            else { __kml_holes_mark(hdr, i, i + 1); __kml_holes_fill(hdr, j); }
        }
    }
}

/* Moves the set elements of an array of n elements of esize bytes to its
 * front, in order, and makes the rest holes (sort orders the set elements
 * and leaves its holes last); returns the number of set elements. */
i64 __kml_holes_compact(void *hdr, i64 esize, i64 n) {
    Hdr *h = (Hdr *)hdr;
    if (!h->holes) return n;
    char *d = (char *)h->data;
    i64 k = 0;
    for (i64 i = 0; i < n; i++) {
        if (__kml_holes_is(hdr, i)) continue;
        if (k != i) memcpy(d + k * esize, d + i * esize, (size_t)esize);
        k++;
    }
    holes_unmark(hdr, 0, k);
    __kml_holes_mark(hdr, k, n);
    return k;
}

/* The first set element of an array of n elements (the last, fromRight):
 * reduce's seed without an initial value; -1 when every element is a hole. */
i64 __kml_holes_seed(void *hdr, i64 n, int fromRight) {
    for (i64 k = 0; k < n; k++) {
        i64 i = fromRight ? n - 1 - k : k;
        if (!__kml_holes_is(hdr, i)) return i;
    }
    return -1;
}

/* n elements moved from `from` to `to` within one array (copyWithin): their
 * holes move with them. */
void __kml_holes_move(void *hdr, i64 to, i64 from, i64 n) {
    if (!((Hdr *)hdr)->holes || n <= 0) return;
    unsigned char *was = (unsigned char *)kml_alloc(KML_HEAP_RAW, (size_t)n);
    for (i64 k = 0; k < n; k++) was[k] = (unsigned char)__kml_holes_is(hdr, from + k);
    for (i64 k = 0; k < n; k++) {
        if (was[k]) __kml_holes_mark(hdr, to + k, to + k + 1);
        else __kml_holes_fill(hdr, to + k);
    }
}

/* An accessor element (Object.defineProperty with get/set on an index) is
 * a hole whose index has an accessor in the added-property table (shape.c;
 * these weak defaults stand in when a program has none). */
__attribute__((weak)) int __kml_shape_expando_attrs(void *obj, const char *key) { (void)obj; (void)key; return -1; }
__attribute__((weak)) int __kml_shape_expando_get_recv(void *obj, const char *key, i64 recv, i64 *out) {
    (void)obj; (void)key; (void)recv; (void)out;
    return 0;
}
__attribute__((weak)) int __kml_shape_expando_put(void *obj, const char *key, i64 v, i64 recv) {
    (void)obj; (void)key; (void)v; (void)recv;
    return -1;
}

/* Element i of hdr as a hole reads it: its getter's result (this = the
 * array), else undefined. */
i64 __kml_holes_elem(void *hdr, i64 i) {
    if (!hole_bit(hdr, i)) return 10;
    char key[24];
    i64 v = 10;
    snprintf(key, sizeof key, "%lld", i);
    __kml_shape_expando_get_recv(hdr, key, 0, &v);
    return v;
}

__attribute__((weak)) void __kml_shape_expando_set(void *obj, const char *key, i64 v) {
    (void)obj; (void)key; (void)v;
}

/* Property i of hdr for an i no array index (negative, or 2^32-1 and up):
 * an ordinary own property named by its decimal form. */
i64 __kml_holes_prop(void *hdr, i64 i) {
    char key[24];
    i64 v = 10;
    snprintf(key, sizeof key, "%lld", i);
    __kml_shape_expando_get_recv(hdr, key, 0, &v);
    return v;
}

/* A write of the boxed v to property i of hdr, i no array index. */
void __kml_holes_prop_set(void *hdr, i64 i, i64 v) {
    char key[24];
    snprintf(key, sizeof key, "%lld", i);
    if (__kml_shape_expando_put(hdr, key, v, 0) < 0) __kml_shape_expando_set(hdr, key, v);
}

/* Whether element i of hdr is its own property: set, or an accessor. */
int __kml_holes_own(void *hdr, i64 i) {
    if (!hole_bit(hdr, i)) return 1;
    char key[24];
    snprintf(key, sizeof key, "%lld", i);
    return __kml_shape_expando_attrs(hdr, key) >= 0;
}

/* A write of the boxed v to element i of hdr: 1 when an accessor took it
 * (its setter run, or nothing for one without), 0 to store it. */
int __kml_holes_elem_set(void *hdr, i64 i, i64 v) {
    if (!hole_bit(hdr, i)) return 0;
    char key[24];
    snprintf(key, sizeof key, "%lld", i);
    return __kml_shape_expando_put(hdr, key, v, 0) >= 0;
}

__attribute__((weak)) i64 __kml_shape_expando_raw(void *obj, const char *key) { (void)obj; (void)key; return 10; }

/* util.inspect's entry for element i of hdr when it is an accessor element
 * ("[Getter]", "[Setter]", "[Getter/Setter]"; its getter not run), else
 * NULL. */
const char *__kml_holes_acc_label(void *hdr, i64 i) {
    static const struct { i64 n; char s[16]; } g = {KML_STR_ASCII_HDR(8), "[Getter]"}, s = {KML_STR_ASCII_HDR(8), "[Setter]"}, gs = {KML_STR_ASCII_HDR(15), "[Getter/Setter]"};
    if (!hole_bit(hdr, i)) return NULL;
    char key[24];
    snprintf(key, sizeof key, "%lld", i);
    int a = __kml_shape_expando_attrs(hdr, key);
    if (a < 0 || !(a & 8)) return NULL;
    void **pair = (void **)__kml_shape_expando_raw(hdr, key);
    return pair[0] && pair[1] ? gs.s : pair[0] ? g.s : s.s;
}
