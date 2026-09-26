package tests

import "testing"

// Null-prototype objects and Object.prototype (TDD-00229 Stage B).

func TestE2ENullPrototypeObjects(t *testing.T) {
	assertSameAsNode(t, `
const a: any = Object.create(null);
console.log(a);
a.x = 1;
console.log(a);
const k: any = Object.create(null);
const j: any = Object.create(null);
j.q = 1;
console.log({ n: { m: { k: k, j: j } } });
const b = { __proto__: null, y: 2 };
console.log(b);
const d: any = {}; Object.setPrototypeOf(d, null); console.log(d);
const e: any = JSON.parse('{"__proto__": 5}'); console.log(e);
console.log([Object.create(null)]);
const p: any = { z: 1 };
const c: any = Object.create(p);
console.log(c);
const wide: any = Object.create(null);
wide.abcdefghij = 'klmnopqrstuvwxyz0123456789';
wide.second = 'klmnopqrstuvwxyz';
console.log(wide);
`)
}

func TestE2EObjectPrototypeIdentity(t *testing.T) {
	assertSameAsNode(t, `
const op: any = Object.prototype;
console.log(op);
const o: any = {};
console.log(Object.getPrototypeOf(o) === Object.prototype, Object.getPrototypeOf(o) === null);
console.log(o.__proto__ === Object.prototype, Object.getPrototypeOf(Object.prototype));
const n: any = Object.create(null);
console.log(Object.getPrototypeOf(n) === null);
const c: any = Object.create(Object.prototype);
console.log(c, Object.getPrototypeOf(c) === Object.prototype);
`)
}

func TestE2EStaticObjectIntegrityLevels(t *testing.T) {
	assertSameAsNode(t, `
const f = Object.freeze({ a: 1 });
const g = { a: 1 };
console.log(Object.isFrozen(f), Object.isFrozen(g), Object.isSealed(f), Object.isSealed(g), Object.isExtensible(g), Object.isExtensible(f));
const h = { a: 1 };
Object.seal(h);
console.log(Object.isSealed(h), Object.isFrozen(h), Object.isExtensible(h));
h.a = 5;
console.log(h.a);
const i = { a: 1 };
Object.preventExtensions(i);
console.log(Object.isSealed(i), Object.isFrozen(i), Object.isExtensible(i));
`)
}
