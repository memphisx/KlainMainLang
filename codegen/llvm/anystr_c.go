package llvm

import (
	_ "embed"
	"fmt"
)

//go:embed anystrsrc/anystr.c
var anyStrSource string

// AnyStrSource is String(v) of a boxed value's C source (anystrsrc/anystr.c).
func AnyStrSource() string {
	sym := SymbolType()
	idx, _, _ := sym.FieldIndex("description")
	return layoutHeader() + bigintBoxDefines() + fmt.Sprintf(
		"#define KML_HDR_MAGIC %dLL\n#define KML_HDR_MAGIC_MASK %dLL\n#define KML_HOST_TYPEID_FLAG %dLL\n#define KML_SYMBOL_TYPEID %dLL\n#define KML_SYMBOL_DESC %d\n",
		kmlHdrMagic, kmlHdrMagicMask, hostTypeIDFlag, symbolTypeIDFlag, sym.FieldOffset(idx)) + anyStrSource
}

// emitAnyToStr is String(v) of a boxed value: anystr.c, with the program
// tables its arms read (function metadata, the object and host rows, the
// boxed-bigint hook).
func (e *Emitter) emitAnyToStr(v Value) Value {
	e.ensureAnyStr()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_any_tostr(i64 %s)", r, v.Ref))
	return Value{Ref: r, Ty: TypePtr}
}

// ensureAnyStr brings the program tables anystr.c's arms read.
func (e *Emitter) ensureAnyStr() {
	e.ensureDynJSONC()
}
