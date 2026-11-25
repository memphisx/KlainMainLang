package llvm

import _ "embed"

// number_c.go — the embedded C runtime behind the Number.prototype methods
// whose declarations lower to it (`@link number`). Compiled alongside the
// program only when one of them is used.

//go:embed numbersrc/number.c
var numberSource string

// NumberSource returns the C source of the number runtime.
func NumberSource() string { return numberSource }
