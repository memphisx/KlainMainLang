package llvm

import _ "embed"

// fdlibm_c.go — the embedded fdlibm (fdlibmsrc/fdlibm.c), V8's ieee754
// port, so Math's transcendental functions return Node's bits rather than
// the platform libm's.

//go:embed fdlibmsrc/fdlibm.c
var fdlibmSource string

// ensureFdlibm links fdlibm.c; each Math function's declaration lowers to
// its __kml_fd_ entry point.
func (e *Emitter) ensureFdlibm() {
	e.usedFdlibm = true
}
