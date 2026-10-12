package tests

import "testing"

// reduce with an array accumulator: an array seed, and no seed over an
// array of arrays (the accumulator is then an element).
func TestE2EReduceArrayAccumulator(t *testing.T) {
	assertSameAsNode(t, `
const r = ["a", "b"].reduce((es: string[], e) => es.concat(e, e.toUpperCase()), [] as string[])
const q = [[1], [2, 3]].reduce((a, b) => a.concat(b))
const n = [1, 2, 3].reduce((a, b) => a + b)
console.log(r, q, n)
`)
}

// -compat=js: JavaScript's empty `[]` seed holds whatever the callback puts
// in it.
func TestE2EReduceEmptySeedCompatJS(t *testing.T) {
	assertJSFileSameAsNode(t, `
const r = ["a", "b"].reduce((es, e) => es.concat(e, e.toUpperCase()), []);
const p = [1, 2, 3].reduce((acc, x) => { acc.push(x * 2); return acc; }, []);
const m = ["x", "y"].reduce((acc, s, i) => acc.concat([s, i]), []);
console.log(r, p, m);
`)
}
