package llvm

import (
	_ "embed"
	"fmt"
)

//go:embed coresrc/core.c
var coreSource string

// CoreSource is the core numeric/string runtime's C source (TDD-00240), behind
// kml_layout.h; its math calls need -lm (LibmLibs).
func CoreSource() string { return layoutHeader() + coreSource }

// ensureStdoutGlobal declares the libc `FILE *stdout` extern global (symbol per
// platform, stdoutGlobalSymbol) exactly once — shared by the startup setvbuf and
// the per-write fflush (ADR-00867). Never used on Windows.
func (e *Emitter) ensureStdoutGlobal() {
	if !e.usedStdoutGlobal {
		e.emitGlobal(fmt.Sprintf("@%s = external"+irGlobalAS+" global "+irPtr, e.stdoutGlobalSymbol()))
		e.usedStdoutGlobal = true
	}
}

// floatInfLiteral / floatNegInfLiteral: ±Infinity as an LLVM constant for the
// given float IR type (a `float` constant is spelled as the exactly-representable
// double bit pattern).
func floatInfLiteral(irTy string) string    { return "0x7FF0000000000000" }
func floatNegInfLiteral(irTy string) string { return "0xFFF0000000000000" }

// emitFloatToI64 converts a float register to i64 with JS semantics for the
// non-finite cases. `irTy` is "double" or "float".
func (e *Emitter) emitFloatToI64(irTy, ref string) string {
	r := e.freshReg()
	suffix := "f64"
	if irTy == "float" {
		suffix = "f32"
	}
	e.emitInstr(fmt.Sprintf("%s = call i64 @llvm.fptosi.sat.i64.%s(%s %s)", r, suffix, irTy, ref))
	return r
}
