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
