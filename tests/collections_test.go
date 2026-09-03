package tests

import (
	"testing"
)

// --- Map<K,V> ---

func TestE2EMapStringKey(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set('alice', 95)
m.set('bob', 87)
console.log(m.size)
console.log(m.get('alice'))
console.log(m.has('bob'))
console.log(m.has('dave'))
`, "2\n95\ntrue\nfalse")
}

func TestE2EMapDelete(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set('x', 1)
m.set('y', 2)
m.set('z', 3)
console.log(m.size)
m.delete('y')
console.log(m.size)
console.log(m.has('y'))
console.log(m.get('x'))
`, "3\n2\nfalse\n1")
}

func TestE2EMapNumberKey(t *testing.T) {
	assertOutput(t, `
const m = new Map<number, number>()
m.set(1, 100)
m.set(2, 200)
m.set(3, 300)
console.log(m.get(2))
console.log(m.has(4))
console.log(m.size)
`, "200\nfalse\n3")
}

func TestE2EMapOverwrite(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set('k', 10)
console.log(m.get('k'))
m.set('k', 99)
console.log(m.get('k'))
console.log(m.size)
`, "10\n99\n1")
}

func TestE2EMapForEach(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set('a', 1)
m.set('b', 2)
m.set('c', 3)
m.forEach((v, k) => {
    console.log(k + '=' + v)
})
`, "a=1\nb=2\nc=3")
}

func TestE2EMapForEachThirdMapArg(t *testing.T) {
	// The 3rd `map` callback argument is the map being iterated (ADR-00573).
	assertOutput(t, `
const m = new Map<string, number>()
m.set('a', 1)
m.set('b', 2)
m.forEach((v, k, mm) => {
    console.log(k + '=' + v + ' size=' + mm.size + ' get=' + mm.get(k))
})
`, "a=1 size=2 get=1\nb=2 size=2 get=2")
}

func TestE2EMapForEachSingleArg(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set('x', 10)
m.set('y', 20)
m.forEach((v) => {
    console.log(v)
})
`, "10\n20")
}

func TestE2EMapForEachNumberKey(t *testing.T) {
	assertOutput(t, `
const m = new Map<number, string>()
m.set(1, 'one')
m.set(2, 'two')
m.forEach((v, k) => {
    console.log(k + ':' + v)
})
`, "1:one\n2:two")
}

func TestE2EMapEntries(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set('a', 1)
m.set('b', 2)
for (const [k, v] of m.entries()) {
    console.log(k + ':' + v)
}
`, "a:1\nb:2")
}

func TestE2EMapEntriesNumberKey(t *testing.T) {
	assertOutput(t, `
const m = new Map<number, number>()
m.set(1, 100)
m.set(2, 200)
for (const [k, v] of m.entries()) {
    console.log(k + '=' + v)
}
`, "1=100\n2=200")
}

func TestE2EMapClear(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set('a', 1)
m.set('b', 2)
console.log(m.size)
m.clear()
console.log(m.size)
console.log(m.has('a'))
m.set('c', 3)
console.log(m.size)
console.log(m.get('c'))
`, "2\n0\nfalse\n1\n3")
}

// Bug #3 (TDD-00064): a scalar-valued Map's get() distinguishes a missing key
// (null) from a present value of 0 — it used to return a bare 0 for both.
func TestE2EMapGetScalarMissingVsZero(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set('a', 0)
m.set('b', 5)
console.log(m.get('a'))
console.log(m.get('missing'))
console.log(m.get('a') ?? 99)
console.log(m.get('missing') ?? 99)
console.log(m.get('missing') === null)
console.log(m.get('a') === null)
`, "0\nnull\n0\n99\ntrue\nfalse")
}

// --- new Map(entries) — the [K, V][] initial-entries constructor overload ---

func TestE2ENewMapFromEntriesStringKey(t *testing.T) {
	assertOutput(t, `
const m = new Map([['a', 1], ['b', 2], ['c', 3]])
console.log(m.size)
console.log(m.get('a'))
console.log(m.get('c'))
console.log(m.has('b'))
`, "3\n1\n3\ntrue")
}

func TestE2ENewMapFromEntriesExplicitTypeArgs(t *testing.T) {
	// Explicit <K, V> drives the key/value types even when they differ from
	// the string-key/number-value inference defaults.
	assertOutput(t, `
const m = new Map<number, string>([[1, 'one'], [2, 'two']])
console.log(m.get(1))
console.log(m.get(2))
`, "one\ntwo")
}

func TestE2ENewMapFromEntriesVariable(t *testing.T) {
	// Same resolver-rename bug the NewSetFromArrayVariable test documents:
	// the rename pass had no case for NewMapExpression.Init, so a reference
	// to an already-declared [K, V][] variable inside new Map(pairs) was
	// never rewritten to pairs's mangled top-level name.
	assertOutput(t, `
const pairs: [string, number][] = [['x', 10], ['y', 20]]
const m = new Map(pairs)
console.log(m.get('y'))
console.log(m.size)
`, "20\n2")
}

func TestE2ENewMapFromEntriesEmpty(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>([])
console.log(m.size)
`, "0")
}

func TestE2ENewMapFromEntriesDuplicateKeyLastWins(t *testing.T) {
	assertOutput(t, `
const m = new Map([['a', 1], ['a', 2]])
console.log(m.get('a'))
console.log(m.size)
`, "2\n1")
}

func TestE2ENewMapFromEntriesIterate(t *testing.T) {
	assertOutput(t, `
const m = new Map([['x', 10], ['y', 20]])
for (const [k, v] of m.entries()) {
  console.log(k + '=' + v)
}
`, "x=10\ny=20")
}

// --- WeakMap / WeakSet / WeakRef (TDD-00112) — manual-mode semantics ---
// (Under -mm=manual a weak reference is strong: nothing is collected, so
// .deref() never nulls and keys persist. The -mm=gc disappearing-link path is
// exercised separately.)

func TestE2EWeakMapBasic(t *testing.T) {
	assertOutput(t, `
class N { id: number; constructor(id: number) { this.id = id } }
const a = new N(1)
const b = new N(2)
const c = new N(3)
const wm = new WeakMap<N, string>()
wm.set(a, 'alpha')
wm.set(b, 'beta')
console.log(wm.get(a))
console.log(wm.get(b))
console.log(wm.has(c))
console.log(wm.has(a))
wm.delete(a)
console.log(wm.has(a))
`, "alpha\nbeta\nfalse\ntrue\nfalse")
}

func TestE2EWeakSetBasic(t *testing.T) {
	assertOutput(t, `
class N { id: number; constructor(id: number) { this.id = id } }
const a = new N(1)
const b = new N(2)
const ws = new WeakSet<N>()
ws.add(a)
console.log(ws.has(a))
console.log(ws.has(b))
ws.delete(a)
console.log(ws.has(a))
`, "true\nfalse\nfalse")
}

func TestE2EWeakRefDeref(t *testing.T) {
	assertOutput(t, `
class N { id: number; constructor(id: number) { this.id = id } }
const b = new N(42)
const ref = new WeakRef(b)
const got = ref.deref()
console.log(got.id)
`, "42")
}

func TestE2EWeakMapPrimitiveKeyRejected(t *testing.T) {
	mustCompileError(t, `
const wm = new WeakMap<string, number>()
wm.set('x', 1)
`, "must be an object")
}

// --- Set<T> ---

func TestE2ESetString(t *testing.T) {
	assertOutput(t, `
const s = new Set<string>()
s.add('apple')
s.add('banana')
s.add('apple')
console.log(s.size)
console.log(s.has('apple'))
console.log(s.has('cherry'))
`, "2\ntrue\nfalse")
}

func TestE2ESetDelete(t *testing.T) {
	assertOutput(t, `
const s = new Set<string>()
s.add('a')
s.add('b')
s.add('c')
console.log(s.size)
s.delete('b')
console.log(s.size)
console.log(s.has('b'))
`, "3\n2\nfalse")
}

func TestE2ESetForEach(t *testing.T) {
	assertOutput(t, `
const s = new Set<number>()
s.add(10)
s.add(20)
s.add(30)
s.forEach((v) => {
    console.log(v)
})
`, "10\n20\n30")
}

func TestE2ESetForEachTwoArgs(t *testing.T) {
	// Real JS calls back(value, value, set) for a Set — verify the 2nd
	// callback parameter (when declared) receives the same value as the 1st.
	assertOutput(t, `
const s = new Set<string>()
s.add('x')
s.add('y')
s.forEach((v, v2) => {
    console.log(v === v2)
})
`, "true\ntrue")
}

func TestE2ESetClear(t *testing.T) {
	assertOutput(t, `
const s = new Set<string>()
s.add('a')
s.add('b')
console.log(s.size)
s.clear()
console.log(s.size)
console.log(s.has('a'))
s.add('c')
console.log(s.size)
console.log(s.has('c'))
`, "2\n0\nfalse\n1\ntrue")
}

// --- for...of over Set/Map values ---

func TestE2ESetNumber(t *testing.T) {
	assertOutput(t, `
const s = new Set<number>()
s.add(10)
s.add(20)
s.add(10)
console.log(s.size)
console.log(s.has(20))
console.log(s.has(30))
`, "2\ntrue\nfalse")
}

// --- new Set(iterable) — ADR-00159 ---

func TestE2ENewSetFromArrayLiteralNumber(t *testing.T) {
	assertOutput(t, `
const s = new Set([1, 2, 3, 2, 1])
console.log(s.size)
console.log(s.has(2))
console.log(s.has(9))
`, "3\ntrue\nfalse")
}
func TestE2ENewSetFromArrayLiteralString(t *testing.T) {
	assertOutput(t, `
const s = new Set(["a", "b", "a"])
console.log(s.size)
`, "2")
}
func TestE2ENewSetFromArrayVariable(t *testing.T) {
	// Real bug found building this feature: the resolver's rename pass
	// (TDD-00041, per-file name mangling) had no case for
	// NewSetExpression.Init, so a reference to an already-declared array
	// variable inside `new Set(arr)` was never rewritten to match arr's
	// own mangled top-level name — 'arr' is not an array at codegen time
	// even though arr was declared correctly one statement earlier.
	assertOutput(t, `
const arr: number[] = [5, 6, 7]
const s = new Set(arr)
console.log(s.size)
console.log(s.has(6))
`, "3\ntrue")
}
func TestE2ENewSetFromArrayExplicitTypeArg(t *testing.T) {
	assertOutput(t, `
const arr: number[] = [1, 2]
const s = new Set<number>(arr)
console.log(s.size)
`, "2")
}
func TestE2ENewSetFromEmptyArray(t *testing.T) {
	assertOutput(t, `
const s = new Set<number>([])
console.log(s.size)
`, "0")
}
func TestE2ENewSetNoArgumentStillWorks(t *testing.T) {
	assertOutput(t, `
const s = new Set<string>()
s.add("a")
console.log(s.size)
`, "1")
}
func TestE2ENewSetFromArrayCapturedByClosure(t *testing.T) {
	assertOutput(t, `
function make(): () => boolean {
  let target: number = 2
  let arr: number[] = [1, 2, 3]
  let s = new Set(arr)
  return () => s.has(target)
}
const fn = make()
console.log(fn())
`, "true")
}

func TestE2EForOfSet(t *testing.T) {
	assertOutput(t, `
const s = new Set<number>()
s.add(10)
s.add(20)
s.add(30)
for (const v of s) {
    console.log(v)
}
`, "10\n20\n30")
}
func TestE2EForOfSetString(t *testing.T) {
	assertOutput(t, `
const s = new Set<string>()
s.add('a')
s.add('b')
for (const v of s) {
    console.log(v)
}
`, "a\nb")
}
func TestE2EForOfMapValues(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set('x', 1)
m.set('y', 2)
for (const v of m) {
    console.log(v)
}
`, "1\n2")
}
func TestE2EForOfMapValuesExplicit(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set('x', 1)
m.set('y', 2)
for (const v of m.values()) {
    console.log(v)
}
for (const k of m.keys()) {
    console.log(k)
}
`, "1\n2\nx\ny")
}
func TestE2EForOfEmptySet(t *testing.T) {
	assertOutput(t, `
const s = new Set<number>()
for (const v of s) {
    console.log('should not print')
}
console.log('done')
`, "done")
}
func TestE2EForOfMapLabeledBreak(t *testing.T) {
	assertOutput(t, `
const m = new Map<string, number>()
m.set('a', 1)
m.set('b', 2)
m.set('c', 3)
outer: for (const v of m) {
    if (v === 2) break outer;
    console.log(v)
}
`, "1")
}

// --- Map<K,V> / Set<T> as a plain type annotation (not new Map<K,V>()/new Set<T>()) ---
//
// Regression test: the parser used to silently discard the <K,V>/<T> for any
// generic other than Promise<T>, so a parameter/return type annotated
// Map<K,V> or Set<T> resolved to i64 instead of the real collection type —
// see ADR-00058.

func TestE2EMapGenericTypeAnnotationParamAndReturn(t *testing.T) {
	assertOutput(t, `
function identity(x: Map<string, number>): Map<string, number> {
    return x
}
const m = new Map<string, number>()
m.set('a', 1)
const m2 = identity(m)
console.log(m2.get('a'))
console.log(m2.size)
`, "1\n1")
}

func TestE2ESetGenericTypeAnnotationReturnType(t *testing.T) {
	assertOutput(t, `
function makeSet(): Set<string> {
    const s = new Set<string>()
    s.add('x')
    return s
}
const s2 = makeSet()
console.log(s2.has('x'))
console.log(s2.size)
`, "true\n1")
}

func TestE2EMapGenericTypeAnnotationNumberKey(t *testing.T) {
	assertOutput(t, `
function identity(x: Map<number, string>): Map<number, string> {
    return x
}
const m = new Map<number, string>()
m.set(1, 'one')
const m2 = identity(m)
console.log(m2.get(1))
`, "one")
}

// --- Map<K,V>/Set<T> method calls, .size, and for...of through a
// non-identifier receiver (an interface field, an array index) — see
// ADR-00059. Dispatch used to only recognize a plain named variable
// (`m.get(...)`), so `c.scores.get(...)` fell through to an unrelated,
// confusing error instead of working or failing cleanly.

func TestE2EMapFieldAccessMethodCalls(t *testing.T) {
	assertOutput(t, `
interface Container {
    scores: Map<string, number>
}
const m = new Map<string, number>()
const c: Container = { scores: m }
c.scores.set('a', 1)
console.log(c.scores.get('a'))
console.log(c.scores.has('a'))
console.log(c.scores.size)
`, "1\ntrue\n1")
}

// TestE2ETernaryOnChainedMapFieldGet is a regression test for a bug found
// while implementing ADR-00072 (http.listen's req.query/req.headers):
// inferExprType's CallExpression case only resolved a Map/Set method call's
// return type when the receiver was a bare identifier looked up via
// e.lookup — c.scores.get(...) worked, but a ternary using a *chained*
// member expression as the receiver (c.scores here, not a plain identifier)
// had no fallback, so emitConditional's `ty := inferExprType(ex.Consequent)`
// silently picked the wrong (zero-value i64/number) type, producing an IR
// type mismatch ("defined with type 'ptr' but expected 'i64'") the moment
// clang tried to verify it — i.e. a compile-time crash for any ternary
// whose Map-typed branch came from a chained field access.
func TestE2ETernaryOnChainedMapFieldGet(t *testing.T) {
	assertOutput(t, `
interface Container {
    scores: Map<string, string>
}
const m = new Map<string, string>()
m.set('a', 'present')
const c: Container = { scores: m }
const found: string = c.scores.has('a') ? c.scores.get('a') : 'missing'
const notFound: string = c.scores.has('z') ? c.scores.get('z') : 'missing'
console.log(found)
console.log(notFound)
`, "present\nmissing")
}

func TestE2ESetFieldAccessMethodCalls(t *testing.T) {
	assertOutput(t, `
interface Container {
    tags: Set<string>
}
const s = new Set<string>()
const c: Container = { tags: s }
c.tags.add('x')
console.log(c.tags.has('x'))
console.log(c.tags.size)
`, "true\n1")
}

func TestE2EMapFieldAccessForEach(t *testing.T) {
	assertOutput(t, `
interface Container {
    scores: Map<string, number>
}
const m = new Map<string, number>()
m.set('a', 1)
m.set('b', 2)
const c: Container = { scores: m }
c.scores.forEach((v, k) => {
    console.log(k + '=' + v)
})
`, "a=1\nb=2")
}

func TestE2ESetFieldAccessForOf(t *testing.T) {
	assertOutput(t, `
interface Container {
    tags: Set<string>
}
const s = new Set<string>()
s.add('x')
s.add('y')
const c: Container = { tags: s }
for (const t of c.tags) {
    console.log(t)
}
`, "x\ny")
}

func TestE2EMapArrayIndexMethodCall(t *testing.T) {
	assertOutput(t, `
interface Container {
    scores: Map<string, number>
}
const m = new Map<string, number>()
m.set('a', 1)
const c: Container = { scores: m }
const arr: Container[] = [c]
console.log(arr[0].scores.get('a'))
`, "1")
}

func TestE2EMapFieldAccessClear(t *testing.T) {
	assertOutput(t, `
interface Container {
    scores: Map<string, number>
}
const m = new Map<string, number>()
m.set('a', 1)
const c: Container = { scores: m }
c.scores.clear()
console.log(c.scores.size)
`, "0")
}

func TestE2EForOfMapEntriesDecomposition(t *testing.T) {
	// ADR-00481: `for (const [k, v] of map)` decomposes entries (clearing
	// the ADR-00011 values-only caveat); bare-variable iteration still
	// yields values.
	assertOutput(t, `
const m = new Map<string, number>();
m.set("a", 1);
m.set("b", 2);
for (const [k, v] of m) { console.log(k, v); }
const n = new Map<number, string>();
n.set(10, "x");
for (const [key, val] of n) { console.log(key, val); }
for (const v of m) { console.log("val", v); }
`, "a 1\nb 2\n10 x\nval 1\nval 2")
}

func TestE2EMapFromHeterogeneousEntriesRejected(t *testing.T) {
	// ADR-00650: new Map(...) infers its key/value types from the first entry
	// pair; a later pair with a different key or value type (a mixed-type map
	// that would need any-typed keys/values, not yet supported) is a clean
	// compile error rather than storing the mismatched scalar raw into a ptr
	// field (invalid IR).
	cases := []string{
		`const m = new Map([['a', 'b'], [1, 1]]);`,       // key string→number
		`const m = new Map([['a', 1], ['b', 'c']]);`,     // value number→string
	}
	for _, src := range cases {
		if _, err := parseAndCompile(src); err == nil {
			t.Fatalf("expected a clean rejection for a heterogeneous Map entries array, got none for: %s", src)
		}
	}
	// The homogeneous forms still compile and run.
	assertOutput(t, `
const s = new Map([['foo', 'bar'], ['baz', 'qux']]);
console.log(s.get('foo'));
const n = new Map([[1, 10], [2, 20]]);
console.log(n.get(2));
`, "bar\n20")
}

func TestE2EArrayOfHeterogeneousRejected(t *testing.T) {
	// ADR-00650: Array.of(...) builds a homogeneous array (element type from
	// the first argument), same as an `[...]` literal; a mixed argument set
	// (`Array.of(undefined, false, null)` — an any[] the compiler doesn't yet
	// support) is rejected cleanly rather than storing a scalar raw into a ptr
	// slot (invalid IR).
	cases := []string{
		`const a = Array.of(1, 'x');`,
		`const a = Array.of(undefined, false, null);`,
		`const a = Array.of('s', 3);`,
	}
	for _, src := range cases {
		if _, err := parseAndCompile(src); err == nil {
			t.Fatalf("expected a clean rejection for a heterogeneous Array.of, got none for: %s", src)
		}
	}
	// Homogeneous Array.of still compiles and runs.
	assertOutput(t, `
const a = Array.of(1, 2, 3);
console.log(a[0] + a[2]);
const b = Array.of('x', 'y');
console.log(b[1]);
`, "4\ny")
}
