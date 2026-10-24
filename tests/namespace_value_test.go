package tests

import "testing"

// A namespace import read as a value is the module namespace object: its
// value exports in sorted key order; types are not members.
func TestE2ENamespaceImportValue(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"m.ts": `export const b = 2
export function a() { return "a" }
export interface T { x: number }
export type U = string
`,
		"main.ts": `import * as m from "./m"
import * as path from "path"
console.log(typeof m, Object.keys(m).join(","), m.a(), m.b)
const ns = m
console.log(ns.a(), ns.b, Object.isFrozen(m))
console.log(typeof path, path.basename("/x/y.ts"))
function f(x: typeof m) { return x.a() + x.b }
const z: typeof m = m
console.log(m["b"], f(m), "a" in m, z.b)
`,
	}, "main.ts", "object a,b a 2\na 2 false\nobject y.ts\n2 a2 true 2\n")
}

// A namespace object prints, tags and rejects writes as Node's module
// namespace object does.
func TestE2ENamespaceImportObject(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"lib.ts": `export const b = 2
export function add(a: number, c: number) { return a + c }
`,
		"main.ts": `import * as lib from "./lib"
console.log(lib)
console.log(Object.prototype.toString.call(lib), Object.keys(lib), Object.isFrozen(lib), Object.isSealed(lib), Object.getPrototypeOf(lib))
try { (lib as any).b = 5 } catch (e: any) { console.log(e.message) }
try { (lib as any).zz = 5 } catch (e: any) { console.log(e.message) }
try { delete (lib as any).b } catch (e: any) { console.log(e.message) }
console.log(JSON.stringify(Object.getOwnPropertyDescriptor(lib, "b")), lib.b)
`,
	}, "main.ts", `[Module: null prototype] { add: [Function: add], b: 2 }
[object Module] [ 'add', 'b' ] false true null
Cannot assign to read only property 'b' of object '[object Module]'
Cannot add property zz, object is not extensible
Cannot delete property 'b' of [object Module]
{"value":2,"writable":true,"enumerable":true,"configurable":false} 2
`)
}
