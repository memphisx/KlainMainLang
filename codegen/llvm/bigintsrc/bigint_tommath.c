/* bigint_tommath.c — libtommath backend for the __kml_bigint_* ABI (TDD-00074).
 *
 * libtommath is public domain. Each bigint value is an opaque pointer to a
 * heap-allocated mp_int, reached only through the functions below; the emitter
 * never inspects the struct. Compiled and linked (-ltommath) alongside the
 * generated .ll ONLY when a program actually uses bigint with the default
 * -bigint=libtommath backend, the same "one C file per backend" shape the
 * -mm=gc allocator shim (gcsrc/gcshim.c) already uses.
 *
 * Allocation goes through plain malloc/free, so under -mm=gc it is transparently
 * routed to the collector by the same global malloc override the shim installs.
 */
#include <tommath.h>
#include <stdlib.h>
#include <stdio.h>
#include <math.h>

static mp_int *bi_new(void) {
	mp_int *r = (mp_int *)malloc(sizeof(mp_int));
	mp_init(r);
	return r;
}

static void bi_die(const char *msg) {
	fprintf(stderr, "BigInt: %s\n", msg);
	exit(1);
}

/* digits is prefix-stripped by the emitter; radix is 2/8/10/16. len unused
 * (the string is NUL-terminated) but kept in the ABI for backends that want it. */
void *__kml_bigint_from_str(const char *digits, long long len, int radix) {
	(void)len;
	mp_int *r = bi_new();
	if (mp_read_radix(r, digits, radix) != MP_OKAY) bi_die("invalid BigInt literal");
	return r;
}

void *__kml_bigint_from_i64(long long v) {
	char buf[32];
	snprintf(buf, sizeof(buf), "%lld", v);
	mp_int *r = bi_new();
	mp_read_radix(r, buf, 10);
	return r;
}

long long __kml_bigint_to_i64(void *a) {
	return (long long)mp_get_i64((const mp_int *)a);
}

char *__kml_bigint_to_str(void *a, int radix) {
	int size = 0;
	mp_radix_size((const mp_int *)a, radix, &size);
	char *s = (char *)malloc((size_t)size + 1);
	mp_to_radix((const mp_int *)a, s, (size_t)size + 1, NULL, radix);
	// libtommath emits A–F… uppercase; JS's toString(radix) is lowercase.
	for (char *p = s; *p; p++) {
		if (*p >= 'A' && *p <= 'Z') *p += 32;
	}
	return s;
}

#define BI_BINOP(name, call)                       \
	void *name(void *a, void *b) {                  \
		mp_int *r = bi_new();                       \
		call((const mp_int *)a, (const mp_int *)b, r); \
		return r;                                   \
	}

BI_BINOP(__kml_bigint_add, mp_add)
BI_BINOP(__kml_bigint_sub, mp_sub)
BI_BINOP(__kml_bigint_mul, mp_mul)
BI_BINOP(__kml_bigint_and, mp_and)
BI_BINOP(__kml_bigint_or, mp_or)
BI_BINOP(__kml_bigint_xor, mp_xor)

/* JS `/` on bigint truncates toward zero; tommath's mp_div does exactly that,
 * and its remainder carries the dividend's sign — which is also JS `%`. So both
 * come out of one mp_div, not mp_mod (mp_mod would take the divisor's sign). */
void *__kml_bigint_tdiv(void *a, void *b) {
	if (mp_iszero((const mp_int *)b)) bi_die("Division by zero");
	mp_int *q = bi_new();
	mp_int rem;
	mp_init(&rem);
	mp_div((const mp_int *)a, (const mp_int *)b, q, &rem);
	mp_clear(&rem);
	return q;
}

void *__kml_bigint_mod(void *a, void *b) {
	if (mp_iszero((const mp_int *)b)) bi_die("Division by zero");
	mp_int *r = bi_new();
	mp_int quo;
	mp_init(&quo);
	mp_div((const mp_int *)a, (const mp_int *)b, &quo, r);
	mp_clear(&quo);
	return r;
}

void *__kml_bigint_pow(void *a, void *b) {
	long long e = (long long)mp_get_i64((const mp_int *)b);
	if (e < 0) bi_die("Exponent must be non-negative");
	mp_int *r = bi_new();
	mp_expt_n((const mp_int *)a, (int)e, r);
	return r;
}

void *__kml_bigint_neg(void *a) {
	mp_int *r = bi_new();
	mp_neg((const mp_int *)a, r);
	return r;
}

/* ~a == -(a+1): tommath's two's-complement mp_complement is exactly that. */
void *__kml_bigint_not(void *a) {
	mp_int *r = bi_new();
	mp_complement((const mp_int *)a, r);
	return r;
}

void *__kml_bigint_shl(void *a, void *b) {
	long long n = (long long)mp_get_i64((const mp_int *)b);
	mp_int *r = bi_new();
	mp_mul_2d((const mp_int *)a, (int)n, r);
	return r;
}

/* Arithmetic (sign-propagating) right shift, so -5n >> 1n == -3n, matching JS. */
void *__kml_bigint_shr(void *a, void *b) {
	long long n = (long long)mp_get_i64((const mp_int *)b);
	mp_int *r = bi_new();
	mp_signed_rsh((const mp_int *)a, (int)n, r);
	return r;
}

int __kml_bigint_cmp(void *a, void *b) {
	return (int)mp_cmp((const mp_int *)a, (const mp_int *)b);
}

/* Exact comparison of a bigint to a double, for -compat=js's bigint↔float
 * comparison (TDD-00075). Returns -1/0/1 for a <=> d exactly (no rounding), or
 * 2 when d is NaN ("unordered"). A finite double is exactly M * 2^e for integer
 * M (<= 53 bits) and integer e, via frexp — so the comparison reduces to an
 * exact bigint compare with no precision loss, even past 2^53. */
int __kml_bigint_cmp_double(void *a, double d) {
	const mp_int *ai = (const mp_int *)a;
	if (isnan(d)) return 2;
	if (isinf(d)) return d > 0 ? -1 : 1; /* a < +inf, a > -inf */
	int exp;
	double mant = frexp(d, &exp);            /* d = mant * 2^exp, mant in [0.5,1) */
	long long M = (long long)ldexp(mant, 53); /* integer mantissa */
	int e = exp - 53;                         /* d = M * 2^e */
	char buf[32];
	snprintf(buf, sizeof(buf), "%lld", M);
	mp_int bd, tmp;
	mp_init_multi(&bd, &tmp, NULL);
	mp_read_radix(&bd, buf, 10);
	int cmp;
	if (e >= 0) {
		mp_mul_2d(&bd, e, &tmp);       /* tmp = M << e == d */
		cmp = mp_cmp(ai, &tmp);
	} else {
		mp_mul_2d(ai, -e, &tmp);       /* compare a*2^-e to M (== a vs d) */
		cmp = mp_cmp(&tmp, &bd);
	}
	mp_clear_multi(&bd, &tmp, NULL);
	return cmp; /* MP_LT=-1, MP_EQ=0, MP_GT=1 */
}
