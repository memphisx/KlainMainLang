package llvm

// builtin_consts.go — the Math and Number constants: each one's IR double
// literal, by owner and name.
var builtinNumberConsts = map[string]map[string]string{
	"Math": {
		"PI":      "3.141592653589793e+00",
		"E":       "2.718281828459045e+00",
		"LN2":     "6.931471805599453e-01",
		"LN10":    "2.302585092994046e+00",
		"SQRT2":   "1.4142135623730951e+00",
		"SQRT1_2": "0x3FE6A09E667F3BCD", // 0.7071067811865476
		"LOG2E":   "1.4426950408889634e+00",
		"LOG10E":  "4.342944819032518e-01",
	},
	"Number": {
		"MAX_SAFE_INTEGER":  "9.007199254740991e+15",
		"MIN_SAFE_INTEGER":  "-9.007199254740991e+15",
		"EPSILON":           "2.220446049250313e-16",
		"MAX_VALUE":         "1.7976931348623157e+308",
		"MIN_VALUE":         "5.0e-324",
		"POSITIVE_INFINITY": "0x7FF0000000000000",
		"NEGATIVE_INFINITY": "0xFFF0000000000000",
		"NaN":               "0x7FF8000000000000",
	},
}

// builtinNumberConst is owner.prop's literal when it is a Math or Number
// constant.
func builtinNumberConst(owner, prop string) (string, bool) {
	lit, ok := builtinNumberConsts[owner][prop]
	return lit, ok
}
