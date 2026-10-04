package tests

import "testing"

// --- D1 Stage 5 (TDD-00155): property descriptors on dynamic objects —
// defineProperty, accessors, enumerability, freeze/seal, strict-mode
// TypeErrors. Semantics are module-strict JS (Node ESM), verified against
// `node` on .mjs sources. ---

func TestE2EDescDefinePropertyBasics(t *testing.T) {
	assertOutputCompatJS(t, `
const o = { visible: 1 }
Object.defineProperty(o, "hidden", { value: 42, enumerable: false })
Object.defineProperty(o, "locked", { value: "L", writable: false, enumerable: true })
console.log(o.hidden, o.locked)
console.log(Object.keys(o))
console.log(Object.getOwnPropertyNames(o))
console.log(JSON.stringify(o))
`, "42 L\n[ 'visible', 'locked' ]\n[ 'visible', 'hidden', 'locked' ]\n{\"visible\":1,\"locked\":\"L\"}")
}

func TestE2EDescReadOnlyAndDeleteThrow(t *testing.T) {
	assertOutputCompatJS(t, `
const o: any = {}
Object.defineProperty(o, "k", { value: 9, enumerable: true })
try { o.k = 1 } catch (e) { console.log("caught:", e.name) }
console.log(o.k)
try { delete o.k } catch (e) { console.log("caught delete") }
console.log(o.k)
`, "caught: TypeError\n9\ncaught delete\n9")
}

// ADR-00999: the non-configurable-delete TypeError embeds the offending
// property name, matching V8's exact wording `Cannot delete property 'x' of
// #<Object>` (was missing the `'x'`).
func TestE2EDescDeleteMessageHasPropertyName(t *testing.T) {
	assertOutputCompatJS(t, `
const o: any = {}
Object.defineProperty(o, "x", { value: 1, configurable: false })
try { delete o.x } catch (e) { console.log(e.message) }
Object.defineProperty(o, "long name", { value: 2, configurable: false })
try { delete o["long name"] } catch (e) { console.log(e.message) }
`, "Cannot delete property 'x' of #<Object>\nCannot delete property 'long name' of #<Object>")
}

func TestE2EDescGetOwnPropertyDescriptor(t *testing.T) {
	assertOutputCompatJS(t, `
const o: any = { plain: 5 }
const d = Object.getOwnPropertyDescriptor(o, "plain")
console.log(d.value, d.writable, d.enumerable, d.configurable)
console.log(Object.getOwnPropertyDescriptor(o, "nope"))
`, "5 true true true\nundefined")
}

func TestE2EDescStaticObjectDescriptor(t *testing.T) {
	assertOutput(t, `
const o = { a: 1, b: "x" }
console.log(Object.getOwnPropertyDescriptor(o, "b"))
const s = Object.seal({ q: true })
console.log(Object.getOwnPropertyDescriptor(s, "q"))
const f: any = Object.freeze({ c: 2 })
console.log(Object.getOwnPropertyDescriptor(f, "c"))
class P { x = 3; m() { return 1 } }
const p = new P()
console.log(Object.getOwnPropertyDescriptor(p, "x"), Object.getOwnPropertyDescriptor(p, "m"))
console.log(Object.getOwnPropertyNames(o), Object.getOwnPropertyNames(p))
`, `{ value: 'x', writable: true, enumerable: true, configurable: true }
{ value: true, writable: true, enumerable: true, configurable: false }
{ value: 2, writable: false, enumerable: true, configurable: false }
{ value: 3, writable: true, enumerable: true, configurable: true } undefined
[ 'a', 'b' ] [ 'x' ]`)
}

func TestE2EObjectGroupByNullPrototype(t *testing.T) {
	assertOutput(t, `
const g = Object.groupBy([1, 2, 3], (x: number) => (x % 2 ? "odd" : "even"))
console.log(g)
console.log(g.odd, g["none"])
const m: number[][] = [[1, 2], [3], [4, 5]]
console.log(Object.groupBy(m, (row) => row.length))
console.log(Object.groupBy([], (x: number) => "k"))
`, `[Object: null prototype] { odd: [ 1, 3 ], even: [ 2 ] }
[ 1, 3 ] undefined
[Object: null prototype] {
  '1': [ [ 3 ] ],
  '2': [ [ 1, 2 ], [ 4, 5 ] ]
}
[Object: null prototype] {}`)
}

func TestE2EDescLiteralAccessors(t *testing.T) {
	assertOutputCompatJS(t, `
const t = { _x: 1,
  get x() { return this._x * 10 },
  set x(v) { this._x = v + 1 }
}
console.log(t.x)
t.x = 4
console.log(t._x, t.x)
const td = Object.getOwnPropertyDescriptor(t, "x")
console.log(typeof td.get, typeof td.set, td.enumerable, td.configurable)
console.log(JSON.stringify(t))
const spread = { ...t }
console.log(spread.x, JSON.stringify(spread))
`, "10\n5 50\nfunction function true true\n{\"_x\":5,\"x\":50}\n50 {\"_x\":5,\"x\":50}")
}

func TestE2EDescInheritedGetterReceiver(t *testing.T) {
	assertOutputCompatJS(t, `
const proto = { get tag() { return "via-proto:" + this.name } }
const child = Object.create(proto)
child.name = "kid"
console.log(child.tag)
`, "via-proto:kid")
}

func TestE2EDescFreezeSealExtensible(t *testing.T) {
	assertOutputCompatJS(t, `
const frozen = { a: 1 }
Object.freeze(frozen)
console.log(Object.isFrozen(frozen), Object.isSealed(frozen), Object.isExtensible(frozen))
try { frozen.a = 2 } catch (e) { console.log("frozen write caught") }
try { frozen.b = 3 } catch (e) { console.log("frozen add caught") }
console.log(frozen.a, frozen.b)
const sealed = { s: 1 }
Object.seal(sealed)
sealed.s = 2
console.log(sealed.s, Object.isSealed(sealed), Object.isFrozen(sealed))
try { delete sealed.s } catch (e) { console.log("sealed delete caught") }
const pe = { p: 1 }
Object.preventExtensions(pe)
pe.p = 5
try { pe.q = 1 } catch (e) { console.log("nonext add caught") }
console.log(pe.p, Object.isExtensible(pe), Object.isSealed(pe))
`, "true true false\nfrozen write caught\nfrozen add caught\n1 undefined\n2 true false\nsealed delete caught\nnonext add caught\n5 false false")
}

func TestE2EDescGetterOnlyAndDefinedAccessor(t *testing.T) {
	assertOutputCompatJS(t, `
const g: any = {}
Object.defineProperty(g, "v", { get: function() { return 7 } })
console.log(g.v)
try { g.v = 1 } catch (e) { console.log("caught:", e.name) }
Object.defineProperty(g, "w", { set: function(x) { this.stored = x }, enumerable: true })
g.w = 5
console.log(g.stored, g.w)
`, "7\ncaught: TypeError\n5 undefined")
}

func TestE2EDescRedefineNonConfigurable(t *testing.T) {
	assertOutputCompatJS(t, `
const o: any = {}
Object.defineProperty(o, "k", { value: 1, writable: true })
Object.defineProperty(o, "k", { value: 2 })
console.log(o.k)
Object.defineProperty(o, "k", { value: 3, writable: false })
try { Object.defineProperty(o, "k", { value: 4 }) } catch (e) { console.log("caught:", e.name) }
console.log(o.k)
`, "2\ncaught: TypeError\n3")
}

func TestE2EDescUntypedLiteralIsDynamicUnderJS(t *testing.T) {
	// TDD-00022 break shape 4, completed: an untyped literal binding under
	// -compat=js is a dynamic object — ad-hoc property addition works.
	assertOutputCompatJS(t, `
const obj = { x: 1 }
obj.y = 2
console.log(obj.x + obj.y, Object.keys(obj))
delete obj.x
console.log(obj.x, JSON.stringify(obj))
`, "3 [ 'x', 'y' ]\nundefined {\"y\":2}")
}

func TestE2EArrayIntegrity(t *testing.T) {
	assertOutput(t, `
const arr = [1, 2];
Object.freeze(arr);
console.log(Object.isFrozen(arr), Object.isSealed(arr), Object.isExtensible(arr));
const tries: (() => void)[] = [
  () => { arr[0] = 9; },
  () => { arr.push(3); },
  () => { arr.pop(); },
  () => { arr.shift(); },
  () => { arr.sort(); },
  () => { arr[0] += 1; },
  () => { arr.length = 0; },
  () => { arr.splice(0, 1); },
  () => { arr.reverse(); },
];
for (const t of tries) { try { t(); console.log("no throw"); } catch (e) { console.log((e as Error).name, (e as Error).message); } }
console.log(arr);
const s = Object.seal([1, 2]);
s[0] = 5;
try { s.push(1); } catch (e) { console.log((e as Error).message); }
try { s.pop(); } catch (e) { console.log((e as Error).message); }
console.log(s, Object.isSealed(s), Object.isFrozen(s));
const p = Object.preventExtensions([7, 8]);
p.pop();
try { p.push(1); } catch (e) { console.log((e as Error).message); }
console.log(p, Object.isExtensible(p), Object.isFrozen(Object.preventExtensions([])));
const alias = arr;
try { alias[1] = 0; } catch (e) { console.log("alias", (e as Error).message); }
try { Object.freeze(new Uint8Array(1)); } catch (e) { console.log((e as Error).message); }
console.log(Object.isFrozen(Object.freeze(new Uint8Array(0))));
const free = [1];
free.push(2);
console.log(free);
`, `true true false
TypeError Cannot assign to read only property '0' of object '[object Array]'
TypeError Cannot add property 2, object is not extensible
TypeError Cannot delete property '1' of [object Array]
TypeError Cannot assign to read only property '0' of object '[object Array]'
TypeError Cannot assign to read only property '0' of object '[object Array]'
TypeError Cannot assign to read only property '0' of object '[object Array]'
TypeError Cannot assign to read only property 'length' of object '[object Array]'
TypeError Cannot assign to read only property '0' of object '[object Array]'
TypeError Cannot assign to read only property '0' of object '[object Array]'
[ 1, 2 ]
Cannot add property 2, object is not extensible
Cannot delete property '1' of [object Array]
[ 5, 2 ] true false
Cannot add property 1, object is not extensible
[ 7 ] false true
alias Cannot assign to read only property '1' of object '[object Array]'
Cannot freeze array buffer views with elements
true
[ 1, 2 ]`)
}

func TestE2EArrayIntegrityAliases(t *testing.T) {
	assertOutput(t, `
interface Box { list: number[] }
const b: Box = { list: [1, 2] };
Object.freeze(b.list);
function add(xs: number[]) { xs.push(9); }
try { add(b.list); } catch (e) { console.log((e as Error).message); }
try { b.list[0] = 3; } catch (e) { console.log((e as Error).message); }
const a: any = b.list;
try { a.push(4); } catch (e) { console.log((e as Error).message); }
try { a[0] = 4; } catch (e) { console.log((e as Error).message); }
console.log(b.list, Object.isFrozen(a));
`, `Cannot add property 2, object is not extensible
Cannot assign to read only property '0' of object '[object Array]'
Cannot add property 2, object is not extensible
Cannot assign to read only property '0' of object '[object Array]'
[ 1, 2 ] true`)
}
