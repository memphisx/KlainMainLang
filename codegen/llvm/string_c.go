package llvm

import _ "embed"

// string_c.go — the embedded C runtime behind the String.prototype methods
// whose declarations lower to it (`@link string`). Compiled alongside the
// program only when one of them is used.

//go:embed stringsrc/string.c
var stringSource string

// StringSource returns the C source of the string runtime.
func StringSource() string { return stringSource }

// UsesStringC reports whether a lowered string method reached codegen, so
// the build compiles and links the string runtime.
func (e *Emitter) UsesStringC() bool { return e.usedStringC }

// ensureStringC marks the program as needing the string runtime compiled in,
// with the IR runtime it calls back into.
func (e *Emitter) ensureStringC() {
	if e.usedStringC {
		return
	}
	e.usedStringC = true
	e.ensureRangeErrorThrow()
}
