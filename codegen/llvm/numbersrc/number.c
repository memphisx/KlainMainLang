// number.c — Number.prototype methods the builtin declarations (lib/es.d.ts)
// lower to: the spec's formatting algorithms over a double's exact decimal
// expansion, and V8's radix conversion. Results are length-headered strings;
// an out-of-range argument raises the RangeError Node raises.
#include <math.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// The IR runtime's RangeError thrower (ensureRangeErrorThrow).
extern void __kml_throw_range_error(const char *msg) __attribute__((noreturn));

static char *str_new(const char *p, long long n) {
    char *base = (char *)malloc((size_t)n + 9);
    *(long long *)base = n;
    if (n > 0) memcpy(base + 8, p, (size_t)n);
    base[n + 8] = 0;
    return base + 8;
}

static char *str_of(const char *p) { return str_new(p, (long long)strlen(p)); }

static void range_error(const char *msg) { __kml_throw_range_error(str_of(msg)); }

static double to_integer(double v) { return isnan(v) ? 0 : trunc(v); }

// decimal is |v| as significant digits d[0..n) and an exponent: v is
// d[0].d[1]d[2]… × 10^exp. Exact: every double has a finite decimal expansion,
// which printf writes in full at enough precision.
typedef struct {
    char d[800];
    int n;
    int exp;
} decimal;

static void exact_decimal(double v, decimal *out) {
    char buf[900];
    snprintf(buf, sizeof buf, "%.780e", fabs(v));
    out->n = 0;
    char *p = buf;
    for (; *p && *p != 'e'; p++) {
        if (*p >= '0' && *p <= '9') out->d[out->n++] = *p;
    }
    out->exp = atoi(p + 1);
    while (out->n > 1 && out->d[out->n - 1] == '0') out->n--;
}

// round_to keeps k significant digits, a tie rounding up (the spec's
// "if there are two such n, pick the larger": x is non-negative here). k may
// be 0 (everything below the first digit) or negative (all of it).
static void round_to(decimal *x, int k) {
    if (k >= x->n) {
        for (int i = x->n; i < k; i++) x->d[i] = '0';
        x->n = k;
        return;
    }
    bool up = k >= 0 && x->d[k] >= '5';
    if (k < 0) up = false;
    if (k <= 0) {
        // Nothing kept: rounds to one unit at the first kept place, or zero.
        x->n = 1;
        x->d[0] = up ? '1' : '0';
        if (up) x->exp += 1 - k;
        else x->exp = 0;
        return;
    }
    x->n = k;
    if (!up) return;
    int i = k - 1;
    while (i >= 0 && x->d[i] == '9') x->d[i--] = '0';
    if (i >= 0) {
        x->d[i]++;
    } else {
        memmove(x->d + 1, x->d, (size_t)k);
        x->d[0] = '1';
        x->exp++;
    }
}

// shortest writes Number::toString(v) for radix 10: the shortest digits that
// round-trip, laid out per the spec (fixed below 1e21, exponent otherwise).
static void shortest(double v, char *out, size_t size) {
    if (isnan(v)) { snprintf(out, size, "NaN"); return; }
    if (isinf(v)) { snprintf(out, size, v < 0 ? "-Infinity" : "Infinity"); return; }
    if (v == 0) { snprintf(out, size, "0"); return; }
    char sci[40];
    int prec = 1;
    for (; prec < 17; prec++) {
        snprintf(sci, sizeof sci, "%.*e", prec - 1, fabs(v));
        if (strtod(sci, NULL) == fabs(v)) break;
    }
    if (prec == 17) snprintf(sci, sizeof sci, "%.16e", fabs(v));
    char d[24];
    int n = 0;
    char *p = sci;
    for (; *p && *p != 'e'; p++) if (*p >= '0' && *p <= '9') d[n++] = *p;
    while (n > 1 && d[n - 1] == '0') n--;
    int e = atoi(p + 1) + 1; // the spec's n: the decimal point after e digits
    char *o = out;
    if (v < 0) *o++ = '-';
    if (n <= e && e <= 21) {
        memcpy(o, d, (size_t)n); o += n;
        for (int i = n; i < e; i++) *o++ = '0';
    } else if (0 < e && e <= 21) {
        memcpy(o, d, (size_t)e); o += e;
        *o++ = '.';
        memcpy(o, d + e, (size_t)(n - e)); o += n - e;
    } else if (-6 < e && e <= 0) {
        *o++ = '0'; *o++ = '.';
        for (int i = 0; i < -e; i++) *o++ = '0';
        memcpy(o, d, (size_t)n); o += n;
    } else {
        *o++ = d[0];
        if (n > 1) { *o++ = '.'; memcpy(o, d + 1, (size_t)(n - 1)); o += n - 1; }
        o += sprintf(o, "e%c%d", e - 1 < 0 ? '-' : '+', abs(e - 1));
    }
    *o = 0;
}

static char *str_shortest(double v) {
    char buf[64];
    shortest(v, buf, sizeof buf);
    return str_of(buf);
}

// Number.prototype.toFixed(fractionDigits).
char *__kml_Number_toFixed(double x, bool has, double digits) {
    double f = has ? to_integer(digits) : 0;
    if (!isfinite(f) || f < 0 || f > 100) range_error("toFixed() digits argument must be between 0 and 100");
    if (!isfinite(x) || fabs(x) >= 1e21) return str_shortest(x);
    int fd = (int)f;
    decimal dec;
    char out[1300];
    char *o = out;
    if (x < 0) *o++ = '-';
    if (x == 0) {
        dec.n = 1; dec.d[0] = '0'; dec.exp = 0;
    } else {
        exact_decimal(x, &dec);
        round_to(&dec, dec.exp + fd + 1); // the digits down to 10^-fd
    }
    // n is dec's digits scaled to the integer at 10^-fd.
    int intDigits = dec.exp + 1; // digits before the point
    if (intDigits > 0) {
        for (int i = 0; i < intDigits; i++) *o++ = i < dec.n ? dec.d[i] : '0';
    } else {
        *o++ = '0';
    }
    if (fd > 0) {
        *o++ = '.';
        for (int i = 0; i < fd; i++) {
            int k = intDigits + i;
            *o++ = k >= 0 && k < dec.n ? dec.d[k] : '0';
        }
    }
    *o = 0;
    return str_of(out);
}

// exponent_form writes d[0][.d[1..n)]e±exp.
static char *exponent_form(bool negative, decimal *dec) {
    char out[1300];
    char *o = out;
    if (negative) *o++ = '-';
    *o++ = dec->d[0];
    if (dec->n > 1) { *o++ = '.'; memcpy(o, dec->d + 1, (size_t)(dec->n - 1)); o += dec->n - 1; }
    sprintf(o, "e%c%d", dec->exp < 0 ? '-' : '+', abs(dec->exp));
    return str_of(out);
}

// Number.prototype.toExponential(fractionDigits).
char *__kml_Number_toExponential(double x, bool has, double digits) {
    double f = to_integer(digits);
    if (!isfinite(x)) return str_shortest(x);
    if (has && (!isfinite(f) || f < 0 || f > 100)) range_error("toExponential() argument must be between 0 and 100");
    decimal dec;
    if (x == 0) {
        dec.exp = 0;
        dec.n = has ? (int)f + 1 : 1;
        memset(dec.d, '0', (size_t)dec.n);
    } else if (!has) {
        // As many digits as the shortest round-trip needs.
        char sci[40];
        int prec = 1;
        for (; prec < 17; prec++) {
            snprintf(sci, sizeof sci, "%.*e", prec - 1, fabs(x));
            if (strtod(sci, NULL) == fabs(x)) break;
        }
        exact_decimal(x, &dec);
        round_to(&dec, prec);
        while (dec.n > 1 && dec.d[dec.n - 1] == '0') dec.n--;
    } else {
        exact_decimal(x, &dec);
        round_to(&dec, (int)f + 1);
    }
    return exponent_form(x < 0, &dec);
}

// Number.prototype.toPrecision(precision).
char *__kml_Number_toPrecision(double x, bool has, double precision) {
    if (!has) return str_shortest(x);
    double p = to_integer(precision);
    if (!isfinite(x)) return str_shortest(x);
    if (!isfinite(p) || p < 1 || p > 100) range_error("toPrecision() argument must be between 1 and 100");
    int pd = (int)p;
    decimal dec;
    if (x == 0) {
        dec.exp = 0;
        dec.n = pd;
        memset(dec.d, '0', (size_t)pd);
    } else {
        exact_decimal(x, &dec);
        round_to(&dec, pd);
    }
    int e = dec.exp;
    if (e < -6 || e >= pd) return exponent_form(x < 0, &dec);
    char out[1300];
    char *o = out;
    if (x < 0) *o++ = '-';
    if (e >= 0) {
        memcpy(o, dec.d, (size_t)(e + 1)); o += e + 1;
        if (pd > e + 1) { *o++ = '.'; memcpy(o, dec.d + e + 1, (size_t)(pd - e - 1)); o += pd - e - 1; }
    } else {
        *o++ = '0'; *o++ = '.';
        for (int i = 0; i < -(e + 1); i++) *o++ = '0';
        memcpy(o, dec.d, (size_t)pd); o += pd;
    }
    *o = 0;
    return str_of(out);
}

// next_double is the double after v (towards +Infinity).
static double next_double(double v) { return nextafter(v, INFINITY); }

// radix_string is V8's DoubleToRadixCString: the integer digits exactly, the
// fraction digits only as far as the double's own precision reaches.
static char *radix_string(double value, int radix) {
    static const char chars[] = "0123456789abcdefghijklmnopqrstuvwxyz";
    enum { size = 2200 };
    char buffer[size];
    int integer_cursor = size / 2, fraction_cursor = integer_cursor;
    bool negative = value < 0;
    if (negative) value = -value;
    double integer = floor(value), fraction = value - integer;
    double delta = 0.5 * (next_double(value) - value);
    if (delta < next_double(0.0)) delta = next_double(0.0);
    if (fraction >= delta) {
        buffer[fraction_cursor++] = '.';
        do {
            fraction *= radix;
            delta *= radix;
            int digit = (int)fraction;
            buffer[fraction_cursor++] = chars[digit];
            fraction -= digit;
            if (fraction > 0.5 || (fraction == 0.5 && (digit & 1))) {
                if (fraction + delta > 1) {
                    for (;;) {
                        fraction_cursor--;
                        if (fraction_cursor == size / 2) {
                            integer += 1; // carry into the integer part
                            break;
                        }
                        char c = buffer[fraction_cursor];
                        int d = c > '9' ? c - 'a' + 10 : c - '0';
                        if (d + 1 < radix) {
                            buffer[fraction_cursor++] = chars[d + 1];
                            break;
                        }
                    }
                    break;
                }
            }
        } while (fraction >= delta);
    }
    // Integer digits past the double's precision are zeros.
    while (integer / radix >= 9007199254740992.0) {
        integer /= radix;
        buffer[--integer_cursor] = '0';
    }
    do {
        double remainder = fmod(integer, radix);
        buffer[--integer_cursor] = chars[(int)remainder];
        integer = (integer - remainder) / radix;
    } while (integer > 0);
    if (negative) buffer[--integer_cursor] = '-';
    return str_new(buffer + integer_cursor, fraction_cursor - integer_cursor);
}

// Number.prototype.toString(radix).
char *__kml_Number_toString(double x, bool has, double radix) {
    double r = has ? to_integer(radix) : 10;
    if (r < 2 || r > 36) range_error("toString() radix argument must be between 2 and 36");
    if (r == 10 || !isfinite(x)) return str_shortest(x);
    if (x == 0) return str_of("0");
    return radix_string(x, (int)r);
}
