/* bigint_gmp.c — GMP backend for the __kml_bigint_* ABI (TDD-00074).
 *
 * GMP is LGPL/GPL; selected only with -bigint=gmp (never the default), for
 * programs that want its speed and can absorb the licensing. Each bigint value
 * is an opaque pointer to a heap-allocated mpz_t, reached only through the
 * functions below — the exact same ABI bigint_tommath.c implements, so the
 * emitter is backend-agnostic. Compiled/linked (-lgmp) alongside the .ll only
 * when actually used.
 */
#include <gmp.h>
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <math.h>

static mpz_ptr bi_new(void) {
	mpz_ptr r = (mpz_ptr)malloc(sizeof(mpz_t));
	mpz_init(r);
	return r;
}

static void bi_die(const char *msg) {
	fprintf(stderr, "BigInt: %s\n", msg);
	exit(1);
}

void *__kml_bigint_from_str(const char *digits, long long len, int radix) {
	(void)len;
	mpz_ptr r = bi_new();
	if (mpz_set_str(r, digits, radix) != 0) bi_die("invalid BigInt literal");
	return r;
}

void *__kml_bigint_from_i64(long long v) {
	mpz_ptr r = bi_new();
	mpz_set_si(r, (long)v);
	return r;
}

long long __kml_bigint_to_i64(void *a) {
	return (long long)mpz_get_si((mpz_srcptr)a);
}

/* Number(bigint): the nearest double (Infinity when out of range), like JS. */
double __kml_bigint_to_double(void *a) {
	return mpz_get_d((mpz_srcptr)a);
}

void *__kml_bigint_from_u64(unsigned long long v) {
	mpz_ptr r = bi_new();
	mpz_set_ui(r, (unsigned long)v);
	return r;
}

/* Value mod 2^64 (the spec's ToBigUint64 wrap): fdiv_r_2exp is a floor mod,
 * always in [0, 2^64), so get_ui is exact. */
unsigned long long __kml_bigint_to_u64(void *a) {
	mpz_t t;
	mpz_init(t);
	mpz_fdiv_r_2exp(t, (mpz_srcptr)a, 64);
	unsigned long long r = (unsigned long long)mpz_get_ui(t);
	mpz_clear(t);
	return r;
}

char *__kml_bigint_to_str(void *a, int radix) {
	return mpz_get_str(NULL, radix, (mpz_srcptr)a); /* GMP mallocs the result */
}

#define BI_BINOP(name, call)                          \
	void *name(void *a, void *b) {                     \
		mpz_ptr r = bi_new();                          \
		call(r, (mpz_srcptr)a, (mpz_srcptr)b);         \
		return r;                                      \
	}

BI_BINOP(__kml_bigint_add, mpz_add)
BI_BINOP(__kml_bigint_sub, mpz_sub)
BI_BINOP(__kml_bigint_mul, mpz_mul)
BI_BINOP(__kml_bigint_and, mpz_and)
BI_BINOP(__kml_bigint_or, mpz_ior)
BI_BINOP(__kml_bigint_xor, mpz_xor)

void *__kml_bigint_tdiv(void *a, void *b) {
	if (mpz_sgn((mpz_srcptr)b) == 0) bi_die("Division by zero");
	mpz_ptr r = bi_new();
	mpz_tdiv_q(r, (mpz_srcptr)a, (mpz_srcptr)b); /* truncate toward zero, JS `/` */
	return r;
}

void *__kml_bigint_mod(void *a, void *b) {
	if (mpz_sgn((mpz_srcptr)b) == 0) bi_die("Division by zero");
	mpz_ptr r = bi_new();
	mpz_tdiv_r(r, (mpz_srcptr)a, (mpz_srcptr)b); /* remainder w/ dividend's sign, JS `%` */
	return r;
}

void *__kml_bigint_pow(void *a, void *b) {
	long e = mpz_get_si((mpz_srcptr)b);
	if (e < 0) bi_die("Exponent must be non-negative");
	mpz_ptr r = bi_new();
	mpz_pow_ui(r, (mpz_srcptr)a, (unsigned long)e);
	return r;
}

void *__kml_bigint_neg(void *a) {
	mpz_ptr r = bi_new();
	mpz_neg(r, (mpz_srcptr)a);
	return r;
}

/* ~a == -(a+1): GMP's mpz_com is exactly the two's-complement NOT. */
void *__kml_bigint_not(void *a) {
	mpz_ptr r = bi_new();
	mpz_com(r, (mpz_srcptr)a);
	return r;
}

void *__kml_bigint_shl(void *a, void *b) {
	unsigned long n = (unsigned long)mpz_get_si((mpz_srcptr)b);
	mpz_ptr r = bi_new();
	mpz_mul_2exp(r, (mpz_srcptr)a, n);
	return r;
}

/* Floor division by 2^n == arithmetic (sign-propagating) right shift, JS `>>`. */
void *__kml_bigint_shr(void *a, void *b) {
	unsigned long n = (unsigned long)mpz_get_si((mpz_srcptr)b);
	mpz_ptr r = bi_new();
	mpz_fdiv_q_2exp(r, (mpz_srcptr)a, n);
	return r;
}

/* BigInt.asUintN(bits, x): x modulo 2^bits, taken non-negative (the low `bits`
 * bits). mpz_fdiv_r_2exp yields a non-negative remainder. */
void *__kml_bigint_as_uintn(long long bits, void *x) {
	mpz_ptr r = bi_new();
	if (bits <= 0) return r; /* bi_new is 0 */
	mpz_fdiv_r_2exp(r, (mpz_srcptr)x, (mp_bitcnt_t)bits);
	return r;
}

/* BigInt.asIntN(bits, x): the low `bits` bits interpreted as two's complement.
 * Reduce mod 2^bits, then subtract 2^bits when the sign bit (bit bits-1) is set. */
void *__kml_bigint_as_intn(long long bits, void *x) {
	mpz_ptr r = bi_new();
	if (bits <= 0) return r;
	mpz_fdiv_r_2exp(r, (mpz_srcptr)x, (mp_bitcnt_t)bits);
	if (mpz_tstbit(r, (mp_bitcnt_t)(bits - 1))) {
		mpz_t full;
		mpz_init(full);
		mpz_setbit(full, (mp_bitcnt_t)bits); /* full = 2^bits */
		mpz_sub(r, r, full);
		mpz_clear(full);
	}
	return r;
}

int __kml_bigint_cmp(void *a, void *b) {
	return mpz_cmp((mpz_srcptr)a, (mpz_srcptr)b);
}

/* Exact bigint-vs-double comparison for -compat=js (TDD-00075): -1/0/1 for
 * a <=> d, or 2 when d is NaN. See bigint_tommath.c for the frexp decomposition
 * rationale (d = M * 2^e, exact). GMP's own mpz_cmp_d has version-dependent
 * truncation semantics, so this uses the same explicit decomposition to stay
 * exact and identical across backends. */
int __kml_bigint_cmp_double(void *a, double d) {
	mpz_srcptr ai = (mpz_srcptr)a;
	if (isnan(d)) return 2;
	if (isinf(d)) return d > 0 ? -1 : 1;
	int exp;
	double mant = frexp(d, &exp);
	long long M = (long long)ldexp(mant, 53);
	int e = exp - 53;
	mpz_t bd, tmp;
	mpz_init(bd);
	mpz_init(tmp);
	mpz_set_si(bd, (long)M);
	int cmp;
	if (e >= 0) {
		mpz_mul_2exp(tmp, bd, (unsigned long)e);
		cmp = mpz_cmp(ai, tmp);
	} else {
		mpz_mul_2exp(tmp, ai, (unsigned long)(-e));
		cmp = mpz_cmp(tmp, bd);
	}
	mpz_clear(bd);
	mpz_clear(tmp);
	return cmp > 0 ? 1 : (cmp < 0 ? -1 : 0);
}

/* StringToBigInt (ECMA-262 §7.1.14), for BigInt(string): surrounding white
 * space trimmed, an empty string 0n, a 0x/0o/0b prefix with no sign, or a
 * signed decimal integer; NULL when the string is none of these (the
 * caller throws SyntaxError). */
static int kml_bi_space(unsigned char c) {
	return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r';
}

void *__kml_bigint_parse(const char *s) {
	size_t n = strlen(s);
	size_t a = 0, b = n;
	while (a < b && kml_bi_space((unsigned char)s[a])) a++;
	while (b > a && kml_bi_space((unsigned char)s[b - 1])) b--;
	if (a == b) return __kml_bigint_from_str("0", 1, 10);
	int radix = 10;
	size_t start = a;
	int neg = 0;
	if (b - a > 2 && s[a] == '0' && (s[a + 1] == 'x' || s[a + 1] == 'X' || s[a + 1] == 'o' || s[a + 1] == 'O' || s[a + 1] == 'b' || s[a + 1] == 'B')) {
		char p = s[a + 1];
		radix = (p == 'x' || p == 'X') ? 16 : (p == 'o' || p == 'O') ? 8 : 2;
		start = a + 2;
	} else if (s[a] == '+' || s[a] == '-') {
		neg = s[a] == '-';
		start = a + 1;
		if (start == b) return NULL;
	}
	char *buf = (char *)malloc(b - start + 2);
	size_t k = 0;
	if (neg) buf[k++] = '-';
	for (size_t i = start; i < b; i++) {
		unsigned char c = (unsigned char)s[i];
		int d = c >= '0' && c <= '9' ? c - '0' : c >= 'a' && c <= 'f' ? c - 'a' + 10 : c >= 'A' && c <= 'F' ? c - 'A' + 10 : 99;
		if (d >= radix) { free(buf); return NULL; }
		buf[k++] = (char)c;
	}
	buf[k] = 0;
	void *r = __kml_bigint_from_str(buf, (long long)k, radix);
	free(buf);
	return r;
}
