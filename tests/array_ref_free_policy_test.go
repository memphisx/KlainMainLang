package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/parser"
)

// TDD-00213 Stage 4 — shared-buffer free policy guard.
//
// With array reference semantics (Stages 1-3) an array's {data,len} buffer can
// be shared across bindings, object/class fields, function returns, and closure
// captures. The chosen free policy is (a): never auto-free a shared buffer under
// the non-GC modes — it leaks (the documented perf tradeoff; GC mode reclaims
// it) rather than risking a double-free/use-after-free. That policy is realized
// by the conservative escape + interior-alias analysis (escape_check.go): any
// binding whose value (or interior array) is aliased/returned/captured/stored
// elsewhere is marked escaping and left un-freed, so only a genuinely-owned,
// un-aliased buffer is ever freed.
//
// This test builds the deep-free-eligible mode (-mm=auto, and again with
// -optimize-memory) under AddressSanitizer/UBSan and runs aliasing scenarios
// that share array buffers across every Stage 1-3 boundary — including interior
// aliases that escape their owner's block — asserting no sanitizer error and a
// clean exit. A regression that freed a shared buffer would surface here as a
// heap-use-after-free or double-free.
func TestE2EArrayRefSharedBufferFreePolicyASan(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}
	skipSanitizersOnWindows(t)

	cases := map[string]string{
		"local-alias": `
let b = [1, 2, 3];
let a = b;
b.push(4);
console.log(a.join(","));
`,
		"return-field-alias": `
const g = { arr: [1, 2, 3] };
function get(): number[] { return g.arr; }
const x = get();
x.push(4);
console.log(g.arr.join(","));
`,
		"closure-capture": `
function make() {
  const a: number[] = [];
  const add = (v: number) => { a.push(v); };
  const get = () => a;
  return { add, get };
}
const m = make();
m.add(1); m.add(2);
console.log(m.get().join(","));
`,
		"json-obj-array-alias": `
const o = JSON.parse('{"items":[1,2,3]}') as { items: number[] };
const a = o.items;
console.log(a.join(","));
console.log(o.items.length);
`,
		"json-array-return": `
function load(): number[] {
  const a = JSON.parse('[1,2,3]') as number[];
  return a;
}
console.log(load().join(","));
`,
		"interior-escapes-block": `
let a: number[] = [];
{
  const o = JSON.parse('{"items":[1,2,3]}') as { items: number[] };
  a = o.items;
}
console.log(a.join(","));
`,
		"element-escapes-block": `
let e: number[] = [];
{
  const arr = JSON.parse('[[1,2],[3,4]]') as number[][];
  e = arr[0];
}
console.log(e.join(","));
`,
		"shared-into-field": `
const shared = [7, 8, 9];
{
  const o = JSON.parse('{"items":[1,2,3]}') as { items: number[] };
  o.items = shared;
  console.log(o.items[0]);
}
shared.push(10);
console.log(shared.join(","));
`,
	}

	for _, optMem := range []bool{false, true} {
		for name, src := range cases {
			bin := buildArrayRefASanAuto(t, src, optMem)
			out, err := exec.Command(bin).CombinedOutput()
			so := string(out)
			if strings.Contains(so, "ERROR: AddressSanitizer") ||
				strings.Contains(so, "runtime error:") ||
				strings.Contains(so, "double-free") {
				t.Errorf("[optMem=%v %s] sanitizer error:\n%s", optMem, name, so)
			}
			if err != nil {
				t.Errorf("[optMem=%v %s] run failed: %v\n%s", optMem, name, err, so)
			}
		}
	}
}

// buildArrayRefASanAuto compiles src under -mm=auto (optionally +optimize-memory)
// with ASan/UBSan and returns the binary path — a focused variant of
// buildBinaryASan pinned to the deep-free-eligible auto mode.
func buildArrayRefASanAuto(t *testing.T, src string, optMem bool) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	em := llvm.NewEmitter()
	em.SetMemMode("auto")
	if optMem {
		em.SetOptimizeMemory(true)
	}
	ir, err := em.EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	dir := t.TempDir()
	llFile := filepath.Join(dir, "prog.ll")
	binFile := filepath.Join(dir, "prog"+llvm.HostExeSuffix())
	if err := os.WriteFile(llFile, []byte(ir), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	args := []string{"-O1", "-g", "-fno-omit-frame-pointer",
		"-fsanitize=address", "-fsanitize=undefined", llFile, "-o", binFile}
	if em.UsesJSONParse() {
		f := filepath.Join(dir, "jsontree.c")
		if err := os.WriteFile(f, []byte(llvm.JSONParseTreeSource()), 0644); err != nil {
			t.Fatal(err)
		}
		args = append(args, f)
	}
	if em.UsesFloatFmt() {
		f := filepath.Join(dir, "dtoa.c")
		if err := os.WriteFile(f, []byte(llvm.DtoaSource()), 0644); err != nil {
			t.Fatal(err)
		}
		args = append(args, f)
	}
	if out, err := llvm.ClangCommand(args...).CombinedOutput(); err != nil {
		t.Fatalf("clang: %v\n%s", err, out)
	}
	return binFile
}
