package tests

import (
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/parser"
)

// TDD-00209: --no-any bans the any/unknown escape hatch (strict lane only).
// These compile with SetNoAny(true) and assert the outcome.

func compileNoAny(src string) error {
	prog, err := parser.Parse(src)
	if err != nil {
		return err
	}
	em := llvm.NewEmitter()
	em.SetNoAny(true)
	_, err = em.EmitProgram(prog)
	return err
}

func mustNoAnyReject(t *testing.T, src string) {
	t.Helper()
	err := compileNoAny(src)
	if err == nil {
		t.Fatalf("expected --no-any to reject, but it compiled:\n%s", src)
	}
	msg := err.Error()
	if !strings.Contains(msg, "banned") && !strings.Contains(msg, "a ptr") && !strings.Contains(msg, "inferred as 'any'") {
		t.Fatalf("expected a --no-any / cross-type rejection, got: %v", err)
	}
}

func mustNoAnyAccept(t *testing.T, src string) {
	t.Helper()
	if err := compileNoAny(src); err != nil {
		t.Fatalf("expected --no-any to accept concrete code, got: %v\n%s", err, src)
	}
}

func TestNoAnyRejectsExplicitAny(t *testing.T) {
	mustNoAnyReject(t, `let x: any = 5; console.log(x)`)
}

func TestNoAnyRejectsUnknown(t *testing.T) {
	mustNoAnyReject(t, `let x: unknown = 5; console.log(x)`)
}

func TestNoAnyRejectsAnyArray(t *testing.T) {
	mustNoAnyReject(t, `const a: any[] = [1]; console.log(a[0])`)
}

func TestNoAnyRejectsAnyParam(t *testing.T) {
	mustNoAnyReject(t, `function f(p: any): number { return 1 } console.log(f(2))`)
}

func TestNoAnyRejectsAnyField(t *testing.T) {
	mustNoAnyReject(t, `class C { v: any = 0 } const c = new C(); console.log(c.v)`)
}

func TestNoAnyRejectsEvolvingNull(t *testing.T) {
	mustNoAnyReject(t, `let x = null; x = 5; console.log(x)`)
}

// Concrete, fully-typed code still compiles under --no-any.
func TestNoAnyAcceptsConcrete(t *testing.T) {
	mustNoAnyAccept(t, `
let n: number = 5
n = 7
const s: string = "hi"
class Box { v: number = 0 }
const b = new Box(); b.v = 3
console.log(n, s, b.v)
`)
}

// A constrained union is a statically checked type, not the `any` escape hatch —
// it stays allowed under --no-any.
func TestNoAnyAcceptsConstrainedUnion(t *testing.T) {
	mustNoAnyAccept(t, `
interface Item { value: string | number }
const a: Item = { value: 42 }
console.log(a.value)
`)
}

// Stage 2: value-level any — an unannotated binding whose value is `any`
// (e.g. JSON.parse without an `as T`) is rejected too.
func TestNoAnyRejectsValueLevelAny(t *testing.T) {
	mustNoAnyReject(t, `const x = JSON.parse("[1,2]"); console.log(x)`)
}

// With a concrete `as T` projection it is accepted.
func TestNoAnyAcceptsAnnotatedJSONParse(t *testing.T) {
	mustNoAnyAccept(t, `const x: number[] = JSON.parse("[1,2]") as number[]; console.log(x[0])`)
}
