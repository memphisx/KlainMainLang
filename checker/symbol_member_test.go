package checker_test

import "testing"

// A well-known symbol member of a string is String's, and a spread of the
// iterator it returns is an array of its elements, as tsc types them.
func TestStringSymbolMember(t *testing.T) {
	for src, want := range map[string]string{
		"\"ab\"[Symbol.iterator]":                                                   "() => StringIterator<string>",
		"const si = \"ab\"[Symbol.iterator]();\nconst x = [...si];\nx":              "string[]",
		"const m = new Map([[\"a\", 1]]);\nconst x = [...m[Symbol.iterator]()];\nx": "[string, number][]",
		// `arguments` iterates as `any` (IArguments' [Symbol.iterator]).
		"function q(a: number) { const x = [...arguments]; return x; }\nq(1)": "any[]",
	} {
		if got := typeOfLastLib(t, src); got != want {
			t.Errorf("%q: got %s, want %s", src, got, want)
		}
	}
}
