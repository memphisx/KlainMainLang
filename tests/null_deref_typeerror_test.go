package tests

import (
	"strings"
	"testing"
)

// null_deref_typeerror_test.go — a property access on a run-time
// undefined/null base throws Node's TypeError instead of dereferencing the
// null pointer (ADR-01038).

// Every access shape, inside try/catch: the error is a TypeError and the
// message names the absent base (`undefined` vs `null`) and the property,
// exactly as Node prints it.
func TestE2ENullDerefThrowsTypeError(t *testing.T) {
	assertOutput(t, `
interface Row { name: string; n: number; next?: Row }
class P { x: number = 3; q: P | null = null; hi(): string { return "hi" } }

function t(label: string, fn: () => void) {
  try { fn(); console.log(label, "ok") } catch (err) {
    console.log(label, err instanceof TypeError, (err as Error).message)
  }
}
const row: Row = { name: "a", n: 1 }
let k = 7
function key(): number { console.log("key evaluated"); return k }

t("str-length", () => { const f = (s?: string) => s.length; f() })
t("chain-end", () => { const f = (r?: Row) => (r?.name).length; f() })
t("field-null", () => { const f = (r: Row | null) => r.name; f(null) })
t("method", () => { const f = (p?: P) => p.hi(); f() })
t("str-method", () => { const f = (s: string | null) => s.toUpperCase(); f(null) })
t("set", () => { const f = (p: P | null) => { p.x = 5 }; f(null) })
t("map-method", () => { const f = (m?: Map<string, number>) => m.get("a"); f() })
t("nested", () => console.log(row.next.next.name))
t("str-index", () => { const f = (s?: string) => s[0]; f() })
t("dyn-key", () => { const f = (s?: string) => s[key()]; f() })
t("scalar", () => { const f = (n?: number) => n.toFixed(2); console.log(f(1.5)); f() })
t("update", () => { const p = new P(); p.q.x++ })
t("compound", () => { const p = new P(); p.q.x += 2 })
t("call-chain", () => { const p = new P(); console.log(p.q.hi().length) })
t("map-miss", () => { const m = new Map<string, Row>(); console.log(m.get("z").name) })
t("non-null-assert", () => { const p = new P(); console.log(p.q!.x) })
t("narrowed", () => { const p = new P(); if (p.q) { console.log(p.q.x) } else { console.log("none") } })
t("optional", () => { const p = new P(); console.log(p.q?.x, p.q?.hi()) })
t("present", () => { const p = new P(); p.q = new P(); console.log(p.q.x, p.q.hi(), row.name.length) })
`, strings.Join([]string{
		"str-length true Cannot read properties of undefined (reading 'length')",
		"chain-end true Cannot read properties of undefined (reading 'length')",
		"field-null true Cannot read properties of null (reading 'name')",
		"method true Cannot read properties of undefined (reading 'hi')",
		"str-method true Cannot read properties of null (reading 'toUpperCase')",
		"set true Cannot set properties of null (setting 'x')",
		"map-method true Cannot read properties of undefined (reading 'get')",
		"nested true Cannot read properties of undefined (reading 'next')",
		"str-index true Cannot read properties of undefined (reading '0')",
		"key evaluated",
		"dyn-key true Cannot read properties of undefined (reading '7')",
		"1.50",
		"scalar true Cannot read properties of undefined (reading 'toFixed')",
		"update true Cannot read properties of null (reading 'x')",
		"compound true Cannot read properties of null (reading 'x')",
		"call-chain true Cannot read properties of null (reading 'hi')",
		"map-miss true Cannot read properties of undefined (reading 'name')",
		"non-null-assert true Cannot read properties of null (reading 'x')",
		"none",
		"narrowed ok",
		"undefined undefined",
		"optional ok",
		"3 hi 1",
		"present ok",
	}, "\n"))
}

// Outside a try the program reports the error and exits 1. It used to
// segfault — and the second shape *hung*: the load through a provably-null
// pointer is undefined behaviour, so the optimiser dropped that path and
// control fell into an unrelated loop.
func TestE2ENullDerefUncaughtExitsCleanly(t *testing.T) {
	for _, src := range []string{
		`function q(s?: string) { console.log(s.length) }
console.log("before")
q()
console.log("after")`,
		`interface Row { name: string }
function q(r?: Row) { console.log((r?.name).length) }
console.log("before")
q()
console.log("after")`,
	} {
		out, code := compileAndRunExpectExitImports(t, src)
		if code != 1 {
			t.Fatalf("exit code = %d, want 1; output:\n%s", code, out)
		}
		if !strings.Contains(out, "before") || strings.Contains(out, "after") ||
			!strings.Contains(out, "Cannot read properties of undefined (reading 'length')") {
			t.Fatalf("unexpected output:\n%s", out)
		}
	}
}

// An expression-bodied arrow's return type is inferred with its parameters in
// scope: `s => s.length` is a number and `s => s[0]` a string. Unresolved, the
// binding was typed i64 while the closure returned double / ptr, so the call
// printed the raw bits.
func TestE2EArrowExprBodyParamTypedReturn(t *testing.T) {
	assertOutput(t, `
const len = (s: string) => s.length
const first = (s: string) => s[0]
const opt = (s?: string) => s[1]
const xs = ["p", "q"]
const at = (i: number) => xs[i]
console.log(len("ab"), first("ab"), opt("ab"), at(1))
`, "2 a b q")
}
