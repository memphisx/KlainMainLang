package llvm

import _ "embed"

// string_c.go — the embedded C runtime behind the String.prototype methods
// whose declarations lower to it (`@link string`). Compiled alongside the
// program only when one of them is used.

//go:embed stringsrc/string.c
var stringSource string

// StringSource returns the C source of the string runtime.
func StringSource() string { return stringSource }
