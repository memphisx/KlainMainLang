package llvm

import (
	"strings"
	"testing"
)

// kml_layout.h's offsets are the emitter's: the promise's size is the one
// the allocator uses, and a known field sits where the IR puts it.
func TestLayoutHeaderMatchesEmitter(t *testing.T) {
	if _, size := irStructOffsets(promiseStructIR); size != promiseStructSize {
		t.Errorf("promise size %d, emitter allocates %d", size, promiseStructSize)
	}
	if offs, _ := irStructOffsets("{ i8, i64, { i1, double }, [3 x i32] }"); offs[1] != 8 || offs[2] != 16 || offs[3] != 32 {
		t.Errorf("offsets %v", offs)
	}
	h := layoutHeader()
	for _, want := range []string{"#define KML_PROMISE_REACTIONS 32", "#define KML_PROMISE_FLAGS 48", "#define KML_CLOSURE_ENV 8"} {
		if !strings.Contains(h, want) {
			t.Errorf("kml_layout.h lacks %q", want)
		}
	}
}
