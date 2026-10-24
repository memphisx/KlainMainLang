package tests

import "testing"

// Object.defineProperty gives a typed object's declared field its
// attributes: a non-writable one rejects writes, a non-enumerable one is
// not listed, a non-configurable one is not deleted or redefined (strict
// mode, as a module runs).
func TestE2EStaticDefinePropertyAttributes(t *testing.T) {
	assertSameAsNode(t, `
interface P { a: number; b: number; c?: number }
const p: P = { a: 1, b: 2 };
Object.defineProperty(p, 'a', { writable: false });
try { p.a = 9; } catch (e) { console.log((e as Error).message); }
console.log(p.a, Object.getOwnPropertyDescriptor(p, 'a'));
Object.defineProperty(p, 'b', { enumerable: false });
console.log(Object.keys(p), JSON.stringify(p), p, { ...p });
for (const k in p) console.log('key', k);
Object.defineProperty(p, 'c', { value: 7 });
console.log(p.c, Object.getOwnPropertyDescriptor(p, 'c'), Object.keys(p));
try { delete p.c; } catch (e) { console.log((e as Error).message); }
try { Object.defineProperty(p, 'c', { value: 8 }); } catch (e) { console.log((e as Error).message); }
class K { x = 1; y?: number; }
const k = new K();
Object.defineProperty(k, 'x', { configurable: false });
try { Object.defineProperty(k, 'x', { value: 3, configurable: true }); console.log("ok"); } catch (e) { console.log((e as Error).constructor.name, (e as Error).message); }
Object.defineProperty(k, 'x', { value: 4 });
console.log(k.x, k);
export {};
`)
}

// An Error's own message, cause and stack have descriptors, and
// defineProperty's attributes hold on them.
func TestE2EErrorPropertyAttributes(t *testing.T) {
	assertSameAsNode(t, `
const e = new Error("m", { cause: 5 });
console.log(Object.getOwnPropertyDescriptor(e, 'cause'));
console.log(Object.getOwnPropertyDescriptor(e, 'message'));
console.log(Object.getOwnPropertyDescriptor(new Error("q"), 'cause'));
const f = new Error("x");
Object.defineProperty(f, 'message', { writable: false });
try { f.message = "changed"; } catch (err) { console.log((err as Error).message); }
console.log(f.message);
const g = new Error("y");
Object.defineProperty(g, 'message', { configurable: false });
try { Object.defineProperty(g, 'message', { value: 1, writable: false, configurable: true }); console.log("redef ok"); } catch (err) { console.log((err as Error).constructor.name, (err as Error).message); }
try { console.log(delete (g as any).message); } catch (err) { console.log((err as Error).message); }
console.log(g.message);
export {};
`)
}

func TestE2EDefinePropertyBesideLayout(t *testing.T) {
	// A static object's or class instance's new property, its attributes
	// and accessors (ADR-01824).
	assertSameAsNode(t, `
const o = { a: 1 };
Object.defineProperty(o, "b", { value: 2, enumerable: true });
Object.defineProperty(o, "h", { value: 3 });
Object.defineProperty(o, "g", { get() { return this.a * 10; }, enumerable: true });
console.log(o, Object.keys(o).join(), Object.getOwnPropertyNames(o).join(), JSON.stringify(o));
(o as any).h = 9;
console.log((o as any).h, (o as any).g, JSON.stringify(Object.getOwnPropertyDescriptor(o, "h")));
class P { x = 1; }
const p = new P();
let seen = 0;
Object.defineProperty(p, "y", { get() { return this.x + 4; }, set(v: number) { seen = v; }, enumerable: true, configurable: true });
(p as any).y = 8;
console.log((p as any).y, seen, p, Object.keys(p).join(), { ...p });
Object.defineProperties(o, { c: { value: 3, enumerable: true } });
console.log((o as any).c);
try { Object.defineProperty(o, "h", { value: 4 }); } catch (e) { console.log((e as Error).constructor.name); }
`)
}
