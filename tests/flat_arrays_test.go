package tests

// `/** @value */` flat value-type array E2E tests (TDD-00134 Stage 2): AoS
// inline element layout, value-copy on slot writes, interior-pointer views on
// reads, the supported V1 surface (literal construction, index read/write,
// .length, for...of, .push), and the clean rejection of everything else.
// No compiler flag involved — the layout is opted into per binding.

import (
	"os/exec"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/parser"
)

func TestE2EFlatArrayValueSemantics(t *testing.T) {
	const src = `
interface Point { x: number; y: number }
function run(): void {
  const src: Point = { x: 10, y: 20 }
  /** @value */
  const ps: Point[] = [src, { x: 3, y: 4 }]
  src.x = 999
  console.log(ps[0].x)
  console.log(ps[1].y)
  console.log(ps.length)
  ps[0].x = 77
  console.log(ps[0].x)
  const v = ps[1]
  v.y = 40
  console.log(ps[1].y)
  const w: Point = { x: 5, y: 6 }
  ps[0] = w
  w.x = 111
  console.log(ps[0].x)
}
run()
`
	// src.x=999 is invisible (copied on construction); ps[0].x= and the taken
	// view both mutate the buffer; ps[0]=w copies w's fields, so w.x=111 after
	// the store is invisible too.
	assertOutput(t, src, "10\n4\n2\n77\n40\n5")
}

func TestE2EFlatArrayForOfAndPush(t *testing.T) {
	const src = `
interface Point { x: number; y: number }
function run(): void {
  /** @value */
  const ps: Point[] = [{ x: 5, y: 6 }, { x: 3, y: 40 }]
  let sum = 0
  for (const p of ps) {
    sum += p.x + p.y
  }
  console.log(sum)
  for (const p of ps) {
    p.x = p.x * 2
  }
  console.log(ps[0].x, ps[1].x)
  const n = ps.push({ x: 100, y: 200 })
  console.log(n, ps.length)
  console.log(ps[2].y)
}
run()
`
	// The for-of binding is a view: writing p.x mutates the array in place.
	assertOutput(t, src, "54\n10 6\n3 3\n200")
}

func TestFlatArrayInlineLayoutIR(t *testing.T) {
	// The buffer is one malloc of n*StructSize with struct-strided GEPs and
	// memcpy element writes — no per-element pointer boxes.
	prog, err := parser.Parse(`
interface P { x: number; y: number }
function f(): void {
  /** @value */
  const a: P[] = [{ x: 1, y: 2 }, { x: 3, y: 4 }]
  console.log(a[1].y)
}
f()
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ir, err := llvm.NewEmitter().EmitProgram(prog)
	if err != nil {
		t.Fatalf("codegen: %v", err)
	}
	if !strings.Contains(ir, "@malloc(i64 32)") {
		t.Errorf("expected one 2*16-byte inline buffer malloc:\n%s", ir)
	}
	if !strings.Contains(ir, "getelementptr { double, double }, ptr") {
		t.Errorf("expected struct-strided element GEPs")
	}
}

func TestFlatArrayRejectsUnsupportedSurface(t *testing.T) {
	const header = `
interface P { x: number }
function f(): void {
/** @value */
const a: P[] = [{ x: 1 }]
`
	cases := []struct{ body, wantSubstr string }{
		{`const b = a.map((p) => p.x)`, "'map' needs a regular"},
		{`a.sort()`, "'sort' needs a regular"},
		{`const b = a.slice(0)`, "'slice' needs a regular"},
		{`a.forEach((p) => console.log(p.x))`, "'forEach' needs a regular"},
		{`a.pop()`, "'pop' needs a regular"},
		{`const b = [...a]`, "spreading 'a' needs a regular"},
		{`const [p] = a`, "destructuring 'a' needs a regular"},
	}
	for _, c := range cases {
		assertCodegenError(t, header+c.body+"\n}\nf()\n", c.wantSubstr)
	}
}

func TestFlatArrayRejectsBadTargets(t *testing.T) {
	assertCodegenError(t, `
function f(): void {
/** @value */
const n: number = 1
console.log(n)
}
f()
`, "@value applies to an array declaration")
	assertCodegenError(t, `
class C { x: number = 1 }
function f(): void {
/** @value */
const a: C[] = [new C()]
console.log(a.length)
}
f()
`, "fixed-shape object element type")
}

func TestE2EFlatArrayAutoModeFreesBuffer(t *testing.T) {
	// -mm=auto: a local flat array is an implicit auto-free candidate; its
	// single inline buffer is freed through the {data,len} header at block
	// exit (the freeSymbol array path, shared with regular arrays) and the
	// program behaves identically.
	const src = `
interface Point { x: number; y: number }
function run(): void {
  /** @value */
  const ps: Point[] = [{ x: 1, y: 2 }, { x: 3, y: 4 }]
  ps.push({ x: 5, y: 6 })
  let sum = 0
  for (const p of ps) { sum += p.x + p.y }
  console.log(sum)
}
run()
run()
`
	binFile := buildBinaryOptimizeMemory(t, src, "auto")
	out, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(out), "\n"), "21\n21")
}

func TestE2EFlatArrayPlainArraysUnchanged(t *testing.T) {
	// A neighboring plain object array keeps reference semantics.
	const src = `
interface P { x: number }
function run(): void {
  const plain: P[] = [{ x: 1 }]
  const q = plain[0]
  q.x = 8
  console.log(plain[0].x)
  const evens = plain.map((p) => p.x * 2)
  console.log(evens[0])
}
run()
`
	binFile := buildBinary(t, src)
	out, err := exec.Command(binFile).Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	compareLines(t, strings.TrimRight(string(out), "\n"), "8\n16")
}
