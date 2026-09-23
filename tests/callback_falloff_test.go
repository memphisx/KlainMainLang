package tests

import (
	"strings"
	"testing"
)

// ADR-01065: a closure whose block body can fall off the end returns
// `T | undefined` on every path that emits it — the arrow/function-expression
// emitter, the HOF callback typing and inferExprType now agree (they used to
// disagree, so an indirect call read a `{ i1, T }` from a define returning a
// bare T). A `T | undefined` value also travels through a promise (payload in
// v0, presence in v1) and an async function's unwritten result slot is
// `undefined`, not heap garbage.

func TestE2ECallbackFallOffReturnsUndefined(t *testing.T) {
	src := `
const a = [1, 2, 3]
const cb = (x: number) => { if (x > 1) return x * 2 }
console.log(cb(1), cb(2))
const fe = function (x: number) { if (x > 1) return x * 2 }
console.log(fe(1), fe(2))
console.log(a.map(x => { if (x > 1) return x * 2 }))
console.log(a.filter(x => { if (x > 1) return true }))
console.log(a.find(x => { if (x > 2) return true }), a.findIndex(x => { if (x > 2) return true }))
console.log(a.some(x => { if (x > 2) return true }), a.every(x => { if (x > 0) return true }))
console.log(a.map(x => { if (x > 1) return "s" + x }))
console.log(a.map(x => { if (x > 1) return { v: x } }))
a.forEach(x => { if (x > 1) return; console.log("fe", x) })
console.log([3, 1, 2].sort((p, q) => { if (p !== q) return p - q }))
console.log("abc".replace(/b/, (m) => { if (m === "b") return "B" }))
console.log(Array.from([1, 2], x => { if (x > 1) return x }))
const nested = (x: number) => { if (x > 1) { return [x] } }
console.log(nested(1), nested(2))
const objs = [{ k: 1 }, { k: 2 }]
console.log(objs.map(o => { if (o.k > 1) return o.k }))
`
	assertOutput(t, src, strings.Join([]string{
		"undefined 4",
		"undefined 4",
		"[ undefined, 4, 6 ]",
		"[ 2, 3 ]",
		"3 2",
		"true true",
		"[ undefined, 's2', 's3' ]",
		"[ undefined, { v: 2 }, { v: 3 } ]",
		"fe 1",
		"[ 1, 2, 3 ]",
		"aBc",
		"[ undefined, 2 ]",
		"undefined [ 2 ]",
		"[ undefined, 2 ]",
	}, "\n"))
	assertSameAsNode(t, src)
}

// A `T | undefined` through a promise: a then-callback that falls off, an
// async function/arrow that falls off, an annotated `Promise<T | undefined>`.
func TestE2EFallOffThroughPromise(t *testing.T) {
	src := `
async function f(x: number) { if (x > 1) return x * 2 }
async function g(x: number): Promise<string | undefined> { if (x > 1) return "s" }
const af = async (x: number) => { if (x > 1) return x * 2 }
console.log(await f(1), await f(2), await g(1), await g(2), await af(1), await af(2))
const r1 = await Promise.resolve(2).then(x => { if (x > 1) return x * 3 })
const r2 = await Promise.resolve(1).then(x => { if (x > 1) return x * 3 })
console.log(r1, r2)
Promise.resolve(2).then(x => { if (x > 1) return x * 3 }).then(v => console.log("then", v))
Promise.resolve(1).then(x => { if (x > 1) return x * 3 }).then(v => console.log("then", v))
f(1).then(v => console.log("t", v, v === undefined))
`
	assertOutput(t, src, "undefined 4 undefined s undefined 4\n6 undefined\nt undefined true\nthen 6\nthen undefined")
	assertSameAsNode(t, src)
}

// tsc rejects a `T | undefined` closure where a `(...) => T` is declared, and a
// `T | undefined` reduce callback against a bare-`T` accumulator; the two return
// ABIs differ, so these are clean compile errors rather than misread words.
func TestFallOffClosureAgainstDeclaredTypeRejected(t *testing.T) {
	for _, src := range []string{
		"function apply(f: (x: number) => number): number { return f(2) }\nconsole.log(apply(x => { if (x > 1) return x * 2 }))\n",
		"const g: (x: number) => number = x => { if (x > 1) return x * 2 }\nconsole.log(g(3))\n",
		"interface H { f: (x: number) => number }\nconst h: H = { f: x => { if (x > 1) return x * 2 } }\nconsole.log(h.f(3))\n",
		"console.log([1, 2, 3].reduce((acc, x) => { if (x > 1) return acc + x }, 0))\n",
		"console.log([1, 2, 3].flatMap(x => { if (x > 1) return [x, x] }))\n",
	} {
		_, err := parseAndCompile(src)
		if err == nil {
			t.Errorf("expected a compile-time rejection for:\n%s", src)
			continue
		}
		if !strings.Contains(err.Error(), "falls off the end") {
			t.Errorf("unexpected error for:\n%s\n%v", src, err)
		}
	}
	// A void or `T | undefined` slot accepts the closure.
	if _, err := parseAndCompile("const g: (x: number) => number | undefined = x => { if (x > 1) return x * 2 }\nconsole.log(g(3), g(0))\n"); err != nil {
		t.Errorf("a `number | undefined` slot must accept a fall-off closure: %v", err)
	}
}
