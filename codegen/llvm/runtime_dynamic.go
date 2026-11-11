package llvm

import (
	_ "embed"
	"fmt"
)

//go:embed boxsrc/strict_eq.c
var strictEqSource string

// StrictEqSource is the strict-equality C source (boxsrc/strict_eq.c).
func StrictEqSource() string {
	return bigintBoxDefines() + fmt.Sprintf("#define KML_HDR_MAGIC %dLL\n#define KML_HDR_MAGIC_MASK %dLL\n#define KML_HOST_TYPEID_FLAG %dLL\n#define KML_HOST_INLINE_FLAG %dLL\n",
		kmlHdrMagic, kmlHdrMagicMask, hostTypeIDFlag, hostInlineFlag) + strictEqSource
}
