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
`, "2\n95\n1\n0")
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
`, "3\n2\n0\n1")
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
`, "200\n0\n3")
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
for (const e of m.entries()) {
    console.log(e.key + ':' + e.value)
}
`, "a:1\nb:2")
}

func TestE2EMapEntriesNumberKey(t *testing.T) {
	assertOutput(t, `
const m = new Map<number, number>()
m.set(1, 100)
m.set(2, 200)
for (const e of m.entries()) {
    console.log(e.key + '=' + e.value)
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
`, "2\n0\n0\n1\n3")
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
`, "2\n1\n0")
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
`, "3\n2\n0")
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
`, "1\n1")
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
`, "2\n0\n0\n1\n1")
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
`, "2\n1\n0")
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
`, "1\n1")
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
`, "1\n1\n1")
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
`, "1\n1")
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
