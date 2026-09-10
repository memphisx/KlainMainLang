package llvm

import (
	"strings"
	"testing"
)

// TestMalformedStoreValueOperand pins the cluster-B invalid-IR rail: a store
// whose value operand is empty (an unimplemented expression formatted a zero
// Value{}'s empty Ref straight into the instruction) must be detected, while
// every well-formed store value token must pass untouched.
func TestMalformedStoreValueOperand(t *testing.T) {
	malformed := []string{
		"store i64 , ptr %x, align 8",
		"store ptr , ptr %fld, align 8",
		"store double , ptr %d, align 8",
		"store i64  , ptr %x", // extra space then bare comma
	}
	for _, s := range malformed {
		if !malformedStoreValueOperand(s) {
			t.Errorf("expected malformed store to be caught: %q", s)
		}
	}

	wellFormed := []string{
		"store i64 0, ptr %x, align 8",
		"store i64 %tmp, ptr %x, align 8",
		"store ptr null, ptr %fld, align 8",
		"store ptr %r, ptr %fld, align 8",
		"store double 0x3FF0000000000000, ptr %d, align 8",
		"store ptr getelementptr inbounds ({ i64, [4 x i8] }, ptr @.s0, i32 0, i32 1), ptr %s, align 8",
		"store atomic i64 %x, ptr %p seq_cst, align 8",
	}
	for _, s := range wellFormed {
		if malformedStoreValueOperand(s) {
			t.Errorf("well-formed store wrongly flagged as malformed: %q", s)
		}
	}
}

// TestEmitInstrStoreGuardPanics verifies the integration wiring: emitInstr turns
// a malformed empty-operand store into an emitGuardPanic (recovered at the
// EmitProgram boundary into a clean compile error), while a well-formed store is
// written through untouched.
func TestEmitInstrStoreGuardPanics(t *testing.T) {
	t.Run("malformed store panics with the sentinel", func(t *testing.T) {
		e := NewEmitter()
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected emitInstr to panic on an empty-operand store")
			}
			if _, ok := r.(emitGuardPanic); !ok {
				t.Fatalf("expected emitGuardPanic, got %T: %v", r, r)
			}
		}()
		e.emitInstr("store i64 , ptr %x, align 8")
	})

	t.Run("well-formed store is written through", func(t *testing.T) {
		e := NewEmitter()
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("well-formed store must not panic, got: %v", r)
			}
		}()
		e.emitInstr("store i64 %tmp, ptr %x, align 8")
		if !strings.Contains(e.body.String(), "store i64 %tmp, ptr %x, align 8") {
			t.Error("well-formed store was not emitted to the body")
		}
	})
}
