package llvm

import _ "embed"

// number_c.go — the embedded C runtime behind the Number.prototype methods
// whose declarations lower to it (`@link number`). Compiled alongside the
// program only when one of them is used.

//go:embed numbersrc/number.c
var numberSource string

// NumberSource returns the C source of the number runtime.
func NumberSource() string { return numberSource }

// UsesNumberC reports whether a lowered Number method reached codegen, so
// the build compiles and links the number runtime.
func (e *Emitter) UsesNumberC() bool { return e.usedNumberC }

// ensureNumberC marks the program as needing the number runtime compiled in,
// with the IR runtime it calls back into.
func (e *Emitter) ensureNumberC() {
	if e.usedNumberC {
		return
	}
	e.usedNumberC = true
	e.ensureRangeErrorThrow()
}
