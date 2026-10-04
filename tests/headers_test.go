package tests

import "testing"

// Headers as undici has it (TDD-00237 Stage 3): names kept as first given
// for inspect, lowercased, sorted and combined for iteration, set-cookie's
// values listed apart, webidl's errors. Node is the oracle.
func TestE2EHeadersListSemantics(t *testing.T) {
	assertSameAsNode(t, `
const h = new Headers({ 'B': ' 2 ', 'a': '1' })
h.append('Set-Cookie', 'x=1'); h.append('set-cookie', 'y=2'); h.append('a', 'z')
console.log(h)
console.log([...h], [...h.keys()], h.get('a'), h.getSetCookie(), h.get('set-cookie'))
console.log(h.entries())
console.log(new Headers())
h.forEach((v, k, p) => console.log(k, v, p === h))
console.log(Object.prototype.toString.call(h), String(h), JSON.stringify(h), Object.keys(h))
console.log({ h }, [h])
console.log(new Headers([['x', '']]).get('x') === '', h.has('A'), h.get('nope'))
h.delete('A'); console.log([...h])
h.set('B', 'three'); console.log(h, h.get('b'))
const copy = new Headers(h); copy.append('c', '4'); console.log([...copy].length, [...h].length)
const big = new Headers(); for (let i = 0; i < 6; i++) big.set('header-name-' + i, 'some-long-value-' + i); console.log(big)
for (const [k, v] of new Headers([['z', '1'], ['m', '2']])) console.log(k, v)
`)
}

func TestE2EHeadersErrors(t *testing.T) {
	assertSameAsNode(t, `
const h = new Headers()
try { h.append('bad name', 'v') } catch (e: any) { console.log(e.name, e.message) }
try { h.set('x', 'a\nb') } catch (e: any) { console.log(e.name, e.message) }
try { new Headers([['a']] as any) } catch (e: any) { console.log(e.name, e.message) }
try { new Headers(5 as any) } catch (e: any) { console.log(e.name, e.message) }
try { h.set('a', '€') } catch (e: any) { console.log(e.name, e.message) }
try { Response.error().headers.set('a', 'b') } catch (e: any) { console.log(e.name, e.message) }
`)
}

func TestE2EHeadersSubclass(t *testing.T) {
	assertSameAsNode(t, `
class MyHeaders extends Headers {
  tag(): string { return this.get('x-tag') ?? 'none' }
}
const m = new MyHeaders({ 'X-Tag': 'yes' })
console.log(m.tag(), m instanceof Headers, typeof Headers)
const H = Headers
console.log(new H([['a', '1']]).get('a'))
`)
}
