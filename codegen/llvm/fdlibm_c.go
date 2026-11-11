package llvm

import _ "embed"

// fdlibm_c.go — the embedded fdlibm (fdlibmsrc/fdlibm.c), V8's ieee754
// port, so Math's transcendental functions return Node's bits rather than
// the platform libm's.

//go:embed fdlibmsrc/fdlibm.c
var fdlibmSource string
