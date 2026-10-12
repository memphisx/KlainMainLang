package ast

import (
	"math"
	"strconv"
	"strings"
)

// NumberKey is the property key a numeric literal names (`{ 0x10: 1 }` is
// key "16"): the literal's value as JavaScript's Number::toString writes it.
// A literal it cannot read is returned as written.
func NumberKey(raw string) string {
	s := strings.ReplaceAll(raw, "_", "")
	var v float64
	if len(s) > 2 && s[0] == '0' && strings.ContainsRune("xXoObB", rune(s[1])) {
		n, err := strconv.ParseUint(s, 0, 64)
		if err != nil {
			return raw
		}
		v = float64(n)
	} else if len(s) > 1 && s[0] == '0' && !strings.ContainsAny(s, ".eE") && strings.Trim(s, "01234567") == "" {
		n, err := strconv.ParseUint(s[1:], 8, 64) // a legacy octal literal (`010`)
		if err != nil {
			return raw
		}
		v = float64(n)
	} else {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil && !math.IsInf(f, 0) {
			return raw
		}
		v = f
	}
	return JSNumberString(v)
}

// JSNumberString is v as JavaScript's Number::toString(10) writes it.
func JSNumberString(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "Infinity"
	case math.IsInf(v, -1):
		return "-Infinity"
	case v == 0:
		return "0"
	}
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	// The shortest digits that round-trip, and n, the decimal point's place
	// after the first of them (the value is 0.d1d2… × 10^n).
	e := strconv.FormatFloat(v, 'e', -1, 64)
	mant, exp, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	x, _ := strconv.Atoi(exp)
	n, k := x+1, len(digits)
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	m := digits[:1]
	if k > 1 {
		m += "." + digits[1:]
	}
	es := strconv.Itoa(n - 1)
	if n-1 >= 0 {
		es = "+" + es
	}
	return sign + m + "e" + es
}
