/* dtoa.c — JS-faithful double-to-string (TDD-00080).
 *
 * Writes into buf (caller guarantees >= 32 bytes) the shortest decimal string
 * for v that round-trips to the same IEEE-754 double, laid out per ECMAScript
 * Number::toString (spec 6.1.6.1.20). Replaces the old bare-%g print, which used
 * C's 6-significant-digit default and %g's precision-coupled fixed/exponential
 * switch (so 1.1 + 2.2 printed "3.3", not "3.3000000000000003").
 *
 * libc only; compiled+linked alongside the program (the bigint/JSON embedded-C
 * pattern) only when a program actually prints a float.
 */
#include <math.h>
#include <string.h>
#include <stdio.h>
#include <stdlib.h>

void __kml_dtoa(char *buf, double v) {
	if (isnan(v)) { strcpy(buf, "NaN"); return; }
	if (isinf(v)) { strcpy(buf, v < 0 ? "-Infinity" : "Infinity"); return; }
	if (v == 0.0) { strcpy(buf, "0"); return; } /* +0 and -0 both → "0" */

	char *p = buf;
	if (v < 0) { *p++ = '-'; v = -v; }

	/* Shortest significant-digit count (1..17) that round-trips. %e (not %g)
	 * exposes the digit string and decimal exponent uniformly across magnitudes. */
	char sci[40];
	int prec = 1;
	for (; prec < 17; prec++) {
		snprintf(sci, sizeof(sci), "%.*e", prec - 1, v);
		if (strtod(sci, NULL) == v) break;
	}
	if (prec == 17) snprintf(sci, sizeof(sci), "%.*e", prec - 1, v);

	/* Parse "D.DDDe±XX" (or "De±XX" when prec==1) into digits + exponent. */
	char digits[24];
	int nd = 0;
	char *s = sci;
	digits[nd++] = *s++;
	if (*s == '.') {
		s++;
		while (*s != 'e' && *s != 'E') digits[nd++] = *s++;
	}
	s++;                 /* skip 'e' */
	int exp = atoi(s);   /* signed */
	/* Trim any trailing zeros the formatter may have left. */
	while (nd > 1 && digits[nd - 1] == '0') nd--;

	int k = nd;          /* significant digit count */
	int n = exp + 1;     /* 10^(n-1) <= v < 10^n; value = digits * 10^(n-k) */

	if (k <= n && n <= 21) {
		memcpy(p, digits, k); p += k;
		for (int i = 0; i < n - k; i++) *p++ = '0';
		*p = '\0';
	} else if (0 < n && n <= 21) {
		memcpy(p, digits, n); p += n;
		*p++ = '.';
		memcpy(p, digits + n, k - n); p += k - n;
		*p = '\0';
	} else if (-6 < n && n <= 0) {
		*p++ = '0'; *p++ = '.';
		for (int i = 0; i < -n; i++) *p++ = '0';
		memcpy(p, digits, k); p += k;
		*p = '\0';
	} else {
		/* Exponential: d0 [.rest] 'e' sign |n-1|, JS style (sign always, no pad). */
		*p++ = digits[0];
		if (k > 1) {
			*p++ = '.';
			memcpy(p, digits + 1, k - 1); p += k - 1;
		}
		*p++ = 'e';
		int e = n - 1;
		if (e >= 0) { *p++ = '+'; } else { *p++ = '-'; e = -e; }
		char ebuf[8];
		int el = 0;
		if (e == 0) ebuf[el++] = '0';
		while (e > 0) { ebuf[el++] = (char)('0' + e % 10); e /= 10; }
		while (el > 0) *p++ = ebuf[--el];
		*p = '\0';
	}
}

/* __kml_num_tolocalestring — Number.prototype.toLocaleString() with no arguments:
 * the en-US default (Intl locale/options are out of scope, rejected at the call
 * site). Groups the integer part with commas every three digits and keeps at
 * most 3 fraction digits (rounded, trailing zeros stripped), matching Node's
 * default. Writes into buf (caller guarantees >= 512 bytes for the widest
 * double). */
void __kml_num_tolocalestring(char *buf, double v) {
	if (isnan(v)) { strcpy(buf, "NaN"); return; }
	if (isinf(v)) { strcpy(buf, v < 0 ? "-\xe2\x88\x9e" : "\xe2\x88\x9e"); return; }
	char *p = buf;
	if (v < 0.0) { *p++ = '-'; v = -v; } /* -0 has v==0, no sign, per JS */

	char tmp[400];
	snprintf(tmp, sizeof(tmp), "%.3f", v); /* "1234.568" / "1000000.000" */
	char *dot = strchr(tmp, '.');
	char *intpart = tmp;
	char *frac = NULL;
	if (dot) { *dot = '\0'; frac = dot + 1; }
	if (frac) {
		int fl = (int)strlen(frac);
		while (fl > 0 && frac[fl - 1] == '0') frac[--fl] = '\0';
		if (fl == 0) frac = NULL;
	}

	int il = (int)strlen(intpart);
	int firstGroup = il % 3;
	if (firstGroup == 0) firstGroup = 3;
	for (int i = 0; i < il; i++) {
		if (i > 0 && (i - firstGroup) % 3 == 0) *p++ = ',';
		*p++ = intpart[i];
	}
	if (frac) {
		*p++ = '.';
		strcpy(p, frac);
		p += strlen(frac);
	}
	*p = '\0';
}

/* __kml_dtoa_exp — Number.prototype.toExponential() with no fractionDigits
 * argument: always exponential notation, with the minimum number of mantissa
 * digits that round-trips to the same double (ECMAScript 21.1.3.3 step 10, the
 * "f is undefined" path). Shares dtoa's shortest-prec loop and JS exponent
 * style (sign always, no zero-padding). Zero renders "0e+0". */
void __kml_dtoa_exp(char *buf, double v) {
	if (isnan(v)) { strcpy(buf, "NaN"); return; }
	if (isinf(v)) { strcpy(buf, v < 0 ? "-Infinity" : "Infinity"); return; }
	char *p = buf;
	if (v == 0.0) { strcpy(buf, "0e+0"); return; } /* +0 and -0 both → "0e+0" */
	if (v < 0) { *p++ = '-'; v = -v; }

	char sci[40];
	int prec = 1;
	for (; prec < 17; prec++) {
		snprintf(sci, sizeof(sci), "%.*e", prec - 1, v);
		if (strtod(sci, NULL) == v) break;
	}
	if (prec == 17) snprintf(sci, sizeof(sci), "%.*e", prec - 1, v);

	char digits[24];
	int nd = 0;
	char *s = sci;
	digits[nd++] = *s++;
	if (*s == '.') {
		s++;
		while (*s != 'e' && *s != 'E') digits[nd++] = *s++;
	}
	s++;                /* skip 'e' */
	int exp = atoi(s);  /* the exponential exponent directly */
	while (nd > 1 && digits[nd - 1] == '0') nd--;
	int k = nd;

	*p++ = digits[0];
	if (k > 1) {
		*p++ = '.';
		memcpy(p, digits + 1, k - 1); p += k - 1;
	}
	*p++ = 'e';
	int e = exp;
	if (e >= 0) { *p++ = '+'; } else { *p++ = '-'; e = -e; }
	char ebuf[8];
	int el = 0;
	if (e == 0) ebuf[el++] = '0';
	while (e > 0) { ebuf[el++] = (char)('0' + e % 10); e /= 10; }
	while (el > 0) *p++ = ebuf[--el];
	*p = '\0';
}
