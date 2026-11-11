package tests

import "testing"

// A destructuring's annotation names an imported type, by name or through a
// namespace import, as a declaration's does: a default in its pattern applies
// to the type's optional member.
func TestE2EDestructuringAnnotationImportedType(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"mod.ts": "export interface Pt { x: number; y?: string }\nexport type Pair = [number, string];\n",
		"main.ts": "import { Pt, Pair } from './mod';\nimport * as M from './mod';\n" +
			"const { x, y }: Pt = JSON.parse('{\"x\": 1}');\n" +
			"const [n, s]: Pair = JSON.parse('[2, \"b\"]');\n" +
			"const { x: x2 }: M.Pt = JSON.parse('{\"x\": 3.5}');\n" +
			"function f() { const { y = \"dflt\" }: M.Pt = { x: 1 }; return y; }\n" +
			"console.log(x + 1, y, n * 2, s, x2 + 1, f());\n",
	}, "main.ts", "2 undefined 4 b 4.5 dflt")
}

// An annotated pattern over a value nothing checked binds what is there: a
// missing element reads undefined and a property of it throws a TypeError,
// as in Node, the annotation notwithstanding.
func TestE2EDestructuringAnnotationShortSource(t *testing.T) {
	assertOutput(t, "const [m, n]: [number, string] = JSON.parse('[4]');\n"+
		"console.log(m * 2, typeof n);\n"+
		"try { console.log(n.length) } catch (e: any) { console.log(e.constructor.name) }\n"+
		"const { a, s }: { a: number; s: string } = JSON.parse('{\"a\": 1}');\n"+
		"try { console.log(a, s.length) } catch (e: any) { console.log(e.constructor.name) }\n",
		"8 undefined\nTypeError\nTypeError")
}
