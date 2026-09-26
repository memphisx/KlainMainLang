package tests

import "testing"

// Walkers built on the generated AST traversal (TDD-00230 P1.4, ADR-01102)
// reach every statement kind; the hand-written ones they replace skipped some.

func TestE2EReturnInsideLabeledLoopSetsReturnType(t *testing.T) {
	// The only valued returns sit under a label: the former return scan never
	// entered a LabeledStatement, inferred `number`, and emitted invalid IR.
	assertOutput(t, `
function sign(n: number) {
  probe: while (true) {
    if (n > 0) return "positive"
    if (n < 0) return "negative"
    break probe
  }
  throw new Error("zero")
}
console.log(sign(3), sign(-2))
`, "positive negative")
}

func TestE2EGeneratorYieldsInsideArrayLiteral(t *testing.T) {
	// Both yields sit inside an array literal: the former yield collector never
	// entered one, mistyped the element and rejected the program.
	assertOutput(t, `
function* pairs() {
  const got = [yield "a", yield "b"]
  console.log(got.length)
}
const it = pairs()
console.log(it.next().value, it.next("x").value)
`, "a b")
}

func TestPureRejectsEffectsUnderAnyStatement(t *testing.T) {
	// The @pure checker used to accept any statement kind it did not name
	// (try/catch, do-while, labeled, throw) without looking inside.
	for _, body := range []string{
		`try { console.log("x") } catch (e) {}`,
		`do { console.log("x") } while (false)`,
		`outer: for (let i = 0; i < 1; i++) { console.log("x") }`,
	} {
		assertCodegenError(t, `
/** @pure */
function add(a: number, b: number): number {
  `+body+`
  return a + b
}
console.log(add(1, 2))
`, "performs I/O")
	}
}
