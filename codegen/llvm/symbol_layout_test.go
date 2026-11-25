package llvm

import "testing"

// collections.c's Symbol.for (ADR-00488) builds a 16-byte Symbol.
func TestSymbolForLayout(t *testing.T) {
	if n := SymbolType().StructSize(); n != 16 {
		t.Fatalf("Symbol is %d bytes; collections.c's Symbol.for builds 16", n)
	}
}
