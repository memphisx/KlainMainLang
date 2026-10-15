/* core.c — small numeric/string runtime routines nearly every program can
 * reach (TDD-00240): process RSS, Math.random, ** and cbrt, and the
 * string->number parsers behind Number/parseInt/parseFloat. Symbols and ABI
 * are those the emitter declares; no float contraction, so the arithmetic
 * rounds exactly as the IR it replaces did. */
#pragma clang fp contract(off)
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#if defined(__APPLE__)
#include <mach/mach.h>
#endif

typedef long long i64;
typedef unsigned long long u64;
typedef unsigned int u32;

static double bits2d(u64 b) {
    double d;
    memcpy(&d, &b, 8);
    return d;
}
static u64 d2bits(double d) {
    u64 b;
    memcpy(&b, &d, 8);
    return b;
}

#if !defined(_WIN32)
/* The instantaneous resident set (process.memoryUsage().rss). Windows gets
 * this from win32shim.c. */
i64 __kml_current_rss_bytes(void) {
#if defined(__APPLE__)
    mach_task_basic_info_data_t info;
    mach_msg_type_number_t cnt = MACH_TASK_BASIC_INFO_COUNT;
    if (task_info(mach_task_self(), MACH_TASK_BASIC_INFO, (task_info_t)&info, &cnt) != KERN_SUCCESS) return 0;
    return (i64)info.resident_size;
#else
    FILE *f = fopen("/proc/self/statm", "r");
    if (!f) return 0;
    long pages = 0;
    int n = fscanf(f, "%*ld %ld", &pages);
    fclose(f);
    return n == 1 ? (i64)pages * 4096 : 0;
#endif
}
#endif

/* Math.random on libcs without arc4random: seeded once per thread. */
static _Thread_local int rand_seeded;
double __klain_math_random(void) {
    if (!rand_seeded) {
        srand((unsigned)time(NULL));
        rand_seeded = 1;
    }
    return (double)rand() / 2147483647.0;
}

/* pow, except |base| == 1 with an infinite exponent is NaN in JS. */
double __kml_js_pow(double b, double x) {
    /* An operand holding undefined (TDD-00241, 0xFFF4000000000001) is NaN:
     * pow(x, 1) returns x itself. */
    if (isnan(b)) b = bits2d(0x7FF8000000000000ULL);
    if (isnan(x)) x = bits2d(0x7FF8000000000000ULL);
    double r = pow(b, x);
    if (fabs(b) == 1.0 && fabs(x) == INFINITY) return bits2d(0x7FF8000000000000ULL);
    return r;
}

extern char *__kml_str_alloc(i64 n);

/* An integer as Node's ERR_OUT_OF_RANGE prints a received value: past 2**32
 * in magnitude its digits are grouped by underscores (addNumericalSeparator,
 * 5_000_000_000). A length-prefixed string. */
char *__kml_received_int(i64 v) {
    char d[24];
    int n = 0, neg = v < 0;
    unsigned long long m = neg ? 0ULL - (unsigned long long)v : (unsigned long long)v;
    int sep = m > 4294967296ULL;
    do { d[n++] = (char)('0' + m % 10); m /= 10; } while (m);
    char out[40];
    int k = 0;
    if (neg) out[k++] = '-';
    for (int i = n - 1; i >= 0; i--) {
        out[k++] = d[i];
        if (sep && i > 0 && i % 3 == 0) out[k++] = '_';
    }
    char *s = __kml_str_alloc(k);
    memcpy(s, out, (size_t)k);
    s[k] = 0;
    return s;
}

/* An ArrayBuffer's zeroed data block, or NULL when it cannot be created. Out
 * of line on purpose: the optimizer may delete an unused calloc it can see and
 * assume it succeeded, which would drop the RangeError a failed one owes. */
void *__kml_byte_block_alloc(i64 n) {
    if (n < 0) return NULL;
    return calloc((size_t)n, 1);
}

/* Exact i64 base**exp by squaring; a negative exponent is 0; overflow wraps. */
i64 __kml_ipow(i64 base, i64 exp) {
    if (exp < 0) return 0;
    u64 result = 1, b = (u64)base, e = (u64)exp;
    while (e != 0) {
        if (e & 1) result *= b;
        b *= b;
        e >>= 1;
    }
    return (i64)result;
}

/* Correctly-rounded cbrt (fdlibm/musl), so Math.cbrt agrees across libms. */
double __kml_cbrt(double x) {
    const double P0 = bits2d(0x3FFE03E60F61E692ULL), P1 = bits2d(0xBFFE28E092F02420ULL),
                 P2 = bits2d(0x3FF9F1604A49D6C2ULL), P3 = bits2d(0xBFE844CBBEE751D9ULL),
                 P4 = bits2d(0x3FC2B000D4E4EDD7ULL);
    u64 xi = d2bits(x);
    u32 hx = (u32)((xi >> 32) & 0x7fffffff);
    u32 newhx;
    if (hx >= 0x7ff00000) return x + x;
    if (hx < 0x00100000) {
        double xs = x * bits2d(0x4350000000000000ULL); /* 2^54 */
        u32 hxs = (u32)((d2bits(xs) >> 32) & 0x7fffffff);
        if (hxs == 0) return x;
        newhx = hxs / 3 + 696219795;
    } else {
        newhx = hx / 3 + 715094163;
    }
    double t0 = bits2d((xi & 0x8000000000000000ULL) | ((u64)newhx << 32));
    double r = (t0 * t0) * (t0 / x);
    double poly1 = P0 + r * (P1 + r * P2);
    double rrr = (r * r) * r;
    double term2 = rrr * (P3 + r * P4);
    double t1 = t0 * (poly1 + term2);
    double t2 = bits2d((d2bits(t1) + 0x80000000ULL) & 0xffffffffc0000000ULL);
    double s = t2 * t2;
    double rn = x / s;
    double w = t2 + t2;
    double rfin = (rn - t2) / (w + rn);
    return t2 + t2 * rfin;
}

static int is_ws(unsigned char c) { return c == 32 || (c >= 9 && c <= 13); }

/* parseInt's default radix: 16 for a 0x/0X prefix (past whitespace and a
 * sign), else 10 — no octal auto-detect. */
int __kml_parseint_base(const char *s) {
    const unsigned char *p = (const unsigned char *)s;
    while (is_ws(*p)) p++;
    if (*p == '+' || *p == '-') p++;
    if (p[0] == '0' && (p[1] == 'x' || p[1] == 'X')) return 16;
    return 10;
}

/* strtod where "Infinity" is the only accepted infinity word (C also takes
 * inf/infinity in any case); a rejected word rewinds endp to the start. */
double __kml_strtod_js(const char *s, char **endp) {
    double v = strtod(s, endp);
    if (v == INFINITY || v == -INFINITY) {
        const char *p = s;
        while (is_ws((unsigned char)*p)) p++;
        if (*p == '+' || *p == '-') p++;
        unsigned char u = (unsigned char)*p & 223;
        if (u >= 65 && u <= 90 && strncmp(p, "Infinity", 8) != 0) *endp = (char *)s;
    }
    return v;
}

/* parseFloat's variant: a 0x prefix is not a decimal literal, so it reads
 * the leading "0" only. */
double __kml_strtod_parsefloat(const char *s, char **endp) {
    const char *p = s;
    while (is_ws((unsigned char)*p)) p++;
    int minus = *p == '-';
    if (*p == '+' || *p == '-') p++;
    if (p[0] == '0' && (p[1] & 223) == 88) {
        *endp = (char *)(p + 1);
        return minus ? -0.0 : 0.0;
    }
    return __kml_strtod_js(s, endp);
}

/* ToNumber of a string: 0b/0o prefixes (unsigned) via strtoll, else strtod;
 * the tail must be whitespace only, and a blank string is 0. */
double __kml_to_number(const char *s) {
    const char *rp = s;
    while (is_ws((unsigned char)*rp)) rp++;
    if (rp[0] == '0') {
        char c1l = (char)(rp[1] | 32);
        if (c1l == 'b' || c1l == 'o') {
            const char *digits = rp + 2;
            char *rend;
            i64 rn = strtoll(digits, &rend, c1l == 'b' ? 2 : 8);
            if (rend != digits) {
                const char *t = rend;
                while (*t && is_ws((unsigned char)*t)) t++;
                if (!*t) return (double)rn;
            }
        }
    }
    char *end;
    double v = __kml_strtod_js(s, &end);
    const char *t = end;
    while (*t && is_ws((unsigned char)*t)) t++;
    if (end == s) return *t ? bits2d(0x7FF8000000000000ULL) : 0.0;
    return *t ? bits2d(0x7FF8000000000000ULL) : v;
}

/* A DOMException's legacy `code` (WebIDL's error names table): its name's
 * code, 0 for a name without one. */
double __kml_domexc_code(const char *name) {
    static const char *const names[] = {
        "IndexSizeError", "DOMStringSizeError", "HierarchyRequestError", "WrongDocumentError",
        "InvalidCharacterError", "NoDataAllowedError", "NoModificationAllowedError", "NotFoundError",
        "NotSupportedError", "InUseAttributeError", "InvalidStateError", "SyntaxError",
        "InvalidModificationError", "NamespaceError", "InvalidAccessError", "ValidationError",
        "TypeMismatchError", "SecurityError", "NetworkError", "AbortError", "URLMismatchError",
        "QuotaExceededError", "TimeoutError", "InvalidNodeTypeError", "DataCloneError"};
    if (!name) return 0;
    for (int i = 0; i < (int)(sizeof names / sizeof names[0]); i++)
        if (strcmp(names[i], name) == 0) return i + 1;
    return 0;
}

/* Array.prototype.sort (ECMA-262 §23.1.3.30): V8's sort, so a program sorts
 * as Node does — the same result for any comparator, an inconsistent one
 * (`(a, b) => a < b ? -1 : 1`) included, since the result then depends on
 * which pairs are compared in which order. V8 runs TimSort
 * (third_party/v8/builtins/array-sort.tq, a port of CPython's listsort):
 * runs found or extended by binary insertion to a minimum length, kept on a
 * stack whose lengths stay balanced, merged with galloping. The sort works on
 * a copy, written back once it is sorted, as V8 sorts a work array: a
 * comparator that throws leaves the array as it was (and leaks the copy).
 * The C library's qsort is no substitute: it is not stable on Windows or
 * macOS. */

typedef int (*kml_sort_cmp)(const void *, const void *);

typedef struct {
    char *a;     /* the work array */
    size_t sz;   /* element size */
    kml_sort_cmp cmp;
    long long min_gallop;
    long long run_base[85], run_len[85];
    int nruns;
    char *tmp;   /* merge buffer, n elements */
    char *key;   /* one element */
} kml_ts;

#define TS_AT(s, arr, i) ((arr) + (size_t)(i) * (s)->sz)
#define TS_MIN_GALLOP 7

static void ts_copy(kml_ts *s, char *dst, long long di, const char *src, long long si, long long n) {
    if (n > 0)
        memmove(TS_AT(s, dst, di), TS_AT(s, src, si), (size_t)n * s->sz);
}

static long long ts_min_run(long long n) {
    long long r = 0;
    while (n >= 64) {
        r |= n & 1;
        n >>= 1;
    }
    return n + r;
}

static void ts_binary_insertion(kml_ts *s, long long low, long long start, long long high) {
    if (start == low)
        start++;
    for (; start < high; start++) {
        long long left = low, right = start;
        memcpy(s->key, TS_AT(s, s->a, start), s->sz);
        while (left < right) {
            long long mid = left + ((right - left) >> 1);
            if (s->cmp(s->key, TS_AT(s, s->a, mid)) < 0)
                right = mid;
            else
                left = mid + 1;
        }
        ts_copy(s, s->a, left + 1, s->a, left, start - left);
        memcpy(TS_AT(s, s->a, left), s->key, s->sz);
    }
}

static long long ts_count_run(kml_ts *s, long long low_arg, long long high) {
    long long low = low_arg + 1;
    if (low == high)
        return 1;
    long long run = 2;
    int descending = s->cmp(TS_AT(s, s->a, low), TS_AT(s, s->a, low - 1)) < 0;
    for (long long i = low + 1; i < high; i++) {
        int order = s->cmp(TS_AT(s, s->a, i), TS_AT(s, s->a, i - 1));
        if (descending ? order >= 0 : order < 0)
            break;
        run++;
    }
    if (descending) {
        for (long long lo = low_arg, hi = low_arg + run - 1; lo < hi; lo++, hi--) {
            memcpy(s->key, TS_AT(s, s->a, lo), s->sz);
            memcpy(TS_AT(s, s->a, lo), TS_AT(s, s->a, hi), s->sz);
            memcpy(TS_AT(s, s->a, hi), s->key, s->sz);
        }
    }
    return run;
}

/* Where key belongs in arr[base, base+len): before every element equal to
 * it (left) or after (right), galloping from hint. */
static long long ts_gallop_left(kml_ts *s, char *arr, const char *key, long long base, long long len, long long hint) {
    long long last = 0, ofs = 1;
    if (s->cmp(TS_AT(s, arr, base + hint), key) < 0) {
        long long max = len - hint;
        while (ofs < max) {
            if (s->cmp(TS_AT(s, arr, base + hint + ofs), key) >= 0)
                break;
            last = ofs;
            ofs = (ofs << 1) + 1;
        }
        if (ofs > max)
            ofs = max;
        last += hint;
        ofs += hint;
    } else {
        long long max = hint + 1;
        while (ofs < max) {
            if (s->cmp(TS_AT(s, arr, base + hint - ofs), key) < 0)
                break;
            last = ofs;
            ofs = (ofs << 1) + 1;
        }
        if (ofs > max)
            ofs = max;
        long long t = last;
        last = hint - ofs;
        ofs = hint - t;
    }
    last++;
    while (last < ofs) {
        long long m = last + ((ofs - last) >> 1);
        if (s->cmp(TS_AT(s, arr, base + m), key) < 0)
            last = m + 1;
        else
            ofs = m;
    }
    return ofs;
}

static long long ts_gallop_right(kml_ts *s, char *arr, const char *key, long long base, long long len, long long hint) {
    long long last = 0, ofs = 1;
    if (s->cmp(key, TS_AT(s, arr, base + hint)) < 0) {
        long long max = hint + 1;
        while (ofs < max) {
            if (s->cmp(key, TS_AT(s, arr, base + hint - ofs)) >= 0)
                break;
            last = ofs;
            ofs = (ofs << 1) + 1;
        }
        if (ofs > max)
            ofs = max;
        long long t = last;
        last = hint - ofs;
        ofs = hint - t;
    } else {
        long long max = len - hint;
        while (ofs < max) {
            if (s->cmp(key, TS_AT(s, arr, base + hint + ofs)) < 0)
                break;
            last = ofs;
            ofs = (ofs << 1) + 1;
        }
        if (ofs > max)
            ofs = max;
        last += hint;
        ofs += hint;
    }
    last++;
    while (last < ofs) {
        long long m = last + ((ofs - last) >> 1);
        if (s->cmp(key, TS_AT(s, arr, base + m)) < 0)
            ofs = m;
        else
            last = m + 1;
    }
    return ofs;
}

static void ts_merge_low(kml_ts *s, long long base_a, long long len_a, long long base_b, long long len_b) {
    char *a = s->a, *t = s->tmp;
    ts_copy(s, t, 0, a, base_a, len_a);
    long long dest = base_a, ct = 0, cb = base_b;
    memcpy(TS_AT(s, a, dest++), TS_AT(s, a, cb++), s->sz);
    if (--len_b == 0)
        goto succeed;
    if (len_a == 1)
        goto copy_b;
    long long min_gallop = s->min_gallop;
    for (;;) {
        long long wins_a = 0, wins_b = 0;
        for (;;) {
            if (s->cmp(TS_AT(s, a, cb), TS_AT(s, t, ct)) < 0) {
                memcpy(TS_AT(s, a, dest++), TS_AT(s, a, cb++), s->sz);
                wins_b++;
                len_b--;
                wins_a = 0;
                if (len_b == 0)
                    goto succeed;
                if (wins_b >= min_gallop)
                    break;
            } else {
                memcpy(TS_AT(s, a, dest++), TS_AT(s, t, ct++), s->sz);
                wins_a++;
                len_a--;
                wins_b = 0;
                if (len_a == 1)
                    goto copy_b;
                if (wins_a >= min_gallop)
                    break;
            }
        }
        min_gallop++;
        int first = 1;
        while (wins_a >= TS_MIN_GALLOP || wins_b >= TS_MIN_GALLOP || first) {
            first = 0;
            min_gallop = min_gallop - 1 > 1 ? min_gallop - 1 : 1;
            s->min_gallop = min_gallop;
            wins_a = ts_gallop_right(s, t, TS_AT(s, a, cb), ct, len_a, 0);
            if (wins_a > 0) {
                ts_copy(s, a, dest, t, ct, wins_a);
                dest += wins_a;
                ct += wins_a;
                len_a -= wins_a;
                if (len_a == 1)
                    goto copy_b;
                if (len_a == 0)
                    goto succeed;
            }
            memcpy(TS_AT(s, a, dest++), TS_AT(s, a, cb++), s->sz);
            if (--len_b == 0)
                goto succeed;
            wins_b = ts_gallop_left(s, a, TS_AT(s, t, ct), cb, len_b, 0);
            if (wins_b > 0) {
                ts_copy(s, a, dest, a, cb, wins_b);
                dest += wins_b;
                cb += wins_b;
                len_b -= wins_b;
                if (len_b == 0)
                    goto succeed;
            }
            memcpy(TS_AT(s, a, dest++), TS_AT(s, t, ct++), s->sz);
            if (--len_a == 1)
                goto copy_b;
        }
        min_gallop++;
        s->min_gallop = min_gallop;
    }
succeed:
    if (len_a > 0)
        ts_copy(s, a, dest, t, ct, len_a);
    return;
copy_b:
    ts_copy(s, a, dest, a, cb, len_b);
    memcpy(TS_AT(s, a, dest + len_b), TS_AT(s, t, ct), s->sz);
}

static void ts_merge_high(kml_ts *s, long long base_a, long long len_a, long long base_b, long long len_b) {
    char *a = s->a, *t = s->tmp;
    ts_copy(s, t, 0, a, base_b, len_b);
    long long dest = base_b + len_b - 1, ct = len_b - 1, ca = base_a + len_a - 1;
    memcpy(TS_AT(s, a, dest--), TS_AT(s, a, ca--), s->sz);
    if (--len_a == 0)
        goto succeed;
    if (len_b == 1)
        goto copy_a;
    long long min_gallop = s->min_gallop;
    for (;;) {
        long long wins_a = 0, wins_b = 0;
        for (;;) {
            if (s->cmp(TS_AT(s, t, ct), TS_AT(s, a, ca)) < 0) {
                memcpy(TS_AT(s, a, dest--), TS_AT(s, a, ca--), s->sz);
                wins_a++;
                len_a--;
                wins_b = 0;
                if (len_a == 0)
                    goto succeed;
                if (wins_a >= min_gallop)
                    break;
            } else {
                memcpy(TS_AT(s, a, dest--), TS_AT(s, t, ct--), s->sz);
                wins_b++;
                len_b--;
                wins_a = 0;
                if (len_b == 1)
                    goto copy_a;
                if (wins_b >= min_gallop)
                    break;
            }
        }
        min_gallop++;
        int first = 1;
        while (wins_a >= TS_MIN_GALLOP || wins_b >= TS_MIN_GALLOP || first) {
            first = 0;
            min_gallop = min_gallop - 1 > 1 ? min_gallop - 1 : 1;
            s->min_gallop = min_gallop;
            long long k = ts_gallop_right(s, a, TS_AT(s, t, ct), base_a, len_a, len_a - 1);
            wins_a = len_a - k;
            if (wins_a > 0) {
                dest -= wins_a;
                ca -= wins_a;
                ts_copy(s, a, dest + 1, a, ca + 1, wins_a);
                len_a -= wins_a;
                if (len_a == 0)
                    goto succeed;
            }
            memcpy(TS_AT(s, a, dest--), TS_AT(s, t, ct--), s->sz);
            if (--len_b == 1)
                goto copy_a;
            k = ts_gallop_left(s, t, TS_AT(s, a, ca), 0, len_b, len_b - 1);
            wins_b = len_b - k;
            if (wins_b > 0) {
                dest -= wins_b;
                ct -= wins_b;
                ts_copy(s, a, dest + 1, t, ct + 1, wins_b);
                len_b -= wins_b;
                if (len_b == 1)
                    goto copy_a;
                if (len_b == 0)
                    goto succeed;
            }
            memcpy(TS_AT(s, a, dest--), TS_AT(s, a, ca--), s->sz);
            if (--len_a == 0)
                goto succeed;
        }
        min_gallop++;
        s->min_gallop = min_gallop;
    }
succeed:
    if (len_b > 0)
        ts_copy(s, a, dest - (len_b - 1), t, 0, len_b);
    return;
copy_a:
    dest -= len_a;
    ca -= len_a;
    ts_copy(s, a, dest + 1, a, ca + 1, len_a);
    memcpy(TS_AT(s, a, dest), TS_AT(s, t, ct), s->sz);
}

static void ts_merge_at(kml_ts *s, int i) {
    long long base_a = s->run_base[i], len_a = s->run_len[i];
    long long base_b = s->run_base[i + 1], len_b = s->run_len[i + 1];
    s->run_len[i] = len_a + len_b;
    if (i == s->nruns - 3) {
        s->run_base[i + 1] = s->run_base[i + 2];
        s->run_len[i + 1] = s->run_len[i + 2];
    }
    s->nruns--;
    memcpy(s->key, TS_AT(s, s->a, base_b), s->sz);
    long long k = ts_gallop_right(s, s->a, s->key, base_a, len_a, 0);
    base_a += k;
    len_a -= k;
    if (len_a == 0)
        return;
    memcpy(s->key, TS_AT(s, s->a, base_a + len_a - 1), s->sz);
    len_b = ts_gallop_left(s, s->a, s->key, base_b, len_b, len_b - 1);
    if (len_b == 0)
        return;
    if (len_a <= len_b)
        ts_merge_low(s, base_a, len_a, base_b, len_b);
    else
        ts_merge_high(s, base_a, len_a, base_b, len_b);
}

static int ts_invariant(kml_ts *s, int n) {
    if (n < 2)
        return 1;
    return s->run_len[n - 2] > s->run_len[n - 1] + s->run_len[n];
}

static void ts_merge_collapse(kml_ts *s) {
    while (s->nruns > 1) {
        int n = s->nruns - 2;
        if (!ts_invariant(s, n + 1) || !ts_invariant(s, n)) {
            if (s->run_len[n - 1] < s->run_len[n + 1])
                n--;
            ts_merge_at(s, n);
        } else if (s->run_len[n] <= s->run_len[n + 1]) {
            ts_merge_at(s, n);
        } else {
            break;
        }
    }
}

static void ts_merge_force_collapse(kml_ts *s) {
    while (s->nruns > 1) {
        int n = s->nruns - 2;
        if (n > 0 && s->run_len[n - 1] < s->run_len[n + 1])
            n--;
        ts_merge_at(s, n);
    }
}

void __kml_stable_sort(void *base, long long n, long long size, kml_sort_cmp cmp) {
    if (n < 2)
        return;
    kml_ts s;
    s.sz = (size_t)size;
    s.cmp = cmp;
    s.min_gallop = TS_MIN_GALLOP;
    s.nruns = 0;
    size_t total = (size_t)n * s.sz;
    s.a = (char *)malloc(total);
    s.tmp = (char *)malloc(total);
    s.key = (char *)malloc(s.sz);
    if (!s.a || !s.tmp || !s.key) {
        free(s.a);
        free(s.tmp);
        free(s.key);
        qsort(base, (size_t)n, s.sz, cmp); /* out of memory: sorted, if not stably */
        return;
    }
    memcpy(s.a, base, total);
    long long low = 0, remaining = n, min_run = ts_min_run(n);
    while (remaining != 0) {
        long long run = ts_count_run(&s, low, low + remaining);
        if (run < min_run) {
            long long forced = min_run < remaining ? min_run : remaining;
            ts_binary_insertion(&s, low, low + run, low + forced);
            run = forced;
        }
        s.run_base[s.nruns] = low;
        s.run_len[s.nruns] = run;
        s.nruns++;
        ts_merge_collapse(&s);
        low += run;
        remaining -= run;
    }
    ts_merge_force_collapse(&s);
    memcpy(base, s.a, total);
    free(s.a);
    free(s.tmp);
    free(s.key);
}

/* An any[]'s sort (SortIndexedProperties): the undefined elements are set
 * aside and go last, never passed to the comparator; the rest keep their
 * order into the sort. */
void __kml_sort_any(long long *base, long long n, kml_sort_cmp cmp) {
    long long k = 0;
    for (long long i = 0; i < n; i++)
        if (base[i] != KML_NB_UNDEFINED)
            base[k++] = base[i];
    for (long long i = k; i < n; i++)
        base[i] = KML_NB_UNDEFINED;
    __kml_stable_sort(base, k, sizeof(long long), cmp);
}

/* A string or object array's sort: its undefined elements (null pointers)
 * are set aside and go last, as an any[]'s. */
void __kml_sort_ptr(void **base, long long n, kml_sort_cmp cmp) {
    long long k = 0;
    for (long long i = 0; i < n; i++)
        if (base[i])
            base[k++] = base[i];
    for (long long i = k; i < n; i++)
        base[i] = 0;
    __kml_stable_sort(base, k, sizeof(void *), cmp);
}
