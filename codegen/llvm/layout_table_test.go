package llvm

import (
	"reflect"
	"testing"
)

// TestLayoutWordKinds: a layout's pointer map classifies each word by its
// field's type (ADR-01943).
func TestLayoutWordKinds(t *testing.T) {
	inner := ObjectType([]Field{{Name: "v", Ty: TypeF64}})
	ty := ObjectType([]Field{
		{Name: "n", Ty: TypeF64},
		{Name: "s", Ty: TypePtr},
		{Name: "a", Ty: ArrayOf(TypeF64)},
		{Name: "o", Ty: inner},
		{Name: "x", Ty: TypeAny},
		{Name: "b", Ty: TypeBool},
		{Name: "i", Ty: TypeI32},
	})
	got := layoutWordKinds(ty)
	want := []byte{wordHeader, wordData, wordPrefixed, wordPrefixed, wordObject, wordBoxed, wordData}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("kinds = %v, want %v (layout %s)", got, want, ty.StructIR())
	}
}
