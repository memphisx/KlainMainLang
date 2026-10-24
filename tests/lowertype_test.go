package tests

import (
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
)

// A `@lowerType` class exports its layout for C code to build instances
// through: size, header word, each field's offset (a private field as
// priv_<name>) and a pointer to its field initializers (TDD-00255 Stage 3).
func TestLowerTypeClassExportsLayout(t *testing.T) {
	src := `
/** @lowerType probe */
class Probe {
  a: string = "x";
  #b: number[] = [];
  n = 3;
  m(): number { return this.#b.length + this.n }
}
const p = new Probe()
console.log(p.a, p.m())
`
	prog, err := parseStrict(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ir, err := llvm.NewEmitter().EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	for _, want := range []string{
		"@__kml_lt_probe_size = weak_odr constant i64 48",
		"@__kml_lt_probe_a = weak_odr constant i64 8",
		"@__kml_lt_probe_priv_b = weak_odr constant i64 16",
		"@__kml_lt_probe_n = weak_odr constant i64 24",
		"@__kml_lt_probe_tag = weak_odr constant i64 ",
		"@__kml_lt_probe_init = weak_odr constant ptr @",
		"@__kml_lt_probe_vtable_at = weak_odr constant i64 0",
	} {
		if !strings.Contains(ir, want) {
			t.Errorf("IR lacks %q", want)
		}
	}
	// The class still constructs and behaves as written.
	if got := compileAndRun(t, src); got != "x 3" {
		t.Errorf("got %q, want %q", got, "x 3")
	}
}

// A @lowerType class has no base: C builds the whole instance.
func TestLowerTypeClassRejectsExtends(t *testing.T) {
	prog, err := parseStrict(`
class Base { x = 1 }
/** @lowerType bad */
class D extends Base { y = 2 }
console.log(new D().y)
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := llvm.NewEmitter().EmitProgram(prog); err == nil || !strings.Contains(err.Error(), "@lowerType class cannot extend") {
		t.Fatalf("want the extends rejection, got %v", err)
	}
}
