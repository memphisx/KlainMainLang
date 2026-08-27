package parser

import "testing"

// Covers the Closure→TS type-string rewrites the JSDoc type sub-parser relies
// on (TDD-00125 Stages 4/6).
func TestNormalizeClosureType(t *testing.T) {
	cases := map[string]string{
		"?number":                           "number | null",
		"!number":                           "number",
		"*":                                 "any",
		"Array.<number>":                    "Array<number>",
		"Object.<string, number>":           "Record<string, number>",
		"function(number, string): boolean": "(arg0: number, arg1: string) => boolean",
		"function(): void":                  "() => void",
		"String":                            "string",
		`import("./shapes").Point`:          "Point",
		"number | string":                  "number | string",
	}
	for in, want := range cases {
		if got := normalizeClosureType(in); got != want {
			t.Errorf("normalizeClosureType(%q) = %q, want %q", in, got, want)
		}
	}
}
