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
