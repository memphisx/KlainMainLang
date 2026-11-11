package llvm

import _ "embed"

// buffill_c.go — the embedded Buffer fill runtime (buffillsrc/buffill.c):
// Node's _fill argument reading and validation over boxed arguments,
// compiled in when a program fills a Buffer.

//go:embed buffillsrc/buffill.c
var bufFillSource string
