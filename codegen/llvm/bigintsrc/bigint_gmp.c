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
