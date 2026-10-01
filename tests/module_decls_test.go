package tests

import (
	"strings"
	"testing"
)

// `assert` is declared as @types/node does: `export = assert`, a function
// merged with a namespace. The default import is callable and narrows as
// an assertion function; named and namespace imports read the namespace.
func TestE2EAssertModuleDeclared(t *testing.T) {
	assertSameAsNodeImports(t, `
import assert from 'assert';
import { strictEqual, deepStrictEqual } from 'assert';
import * as A from 'assert';
function len(x: string | number): number {
    assert(typeof x === "string");
    return x.length;
}
assert(1 === 1, "one");
assert.ok(true);
strictEqual(2, 2);
deepStrictEqual([1, 2], [1, 2]);
A.notStrictEqual(1, 2);
assert.throws(() => { throw new Error("x"); });
assert.match("abc", /b/);
console.log(len("abcd"), "done");
`)
	for _, c := range []struct{ src, want string }{
		{"import assert from \"assert\"\nassert.strictEqual(1)\n", "expected 2-3 arguments, but got 1"},
		{"import assert from \"assert\"\nconst n: number = assert.ok\n", "is not assignable to type 'number'"},
		{"import { equal } from \"assert\"\nequal(1)\n", "expected 2-3 arguments, but got 1"},
	} {
		_, err := resolveAndCompile(t, c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: expected %q, got %v", c.src, c.want, err)
		}
	}
}

// A generic function's argument count is checked (TS2554).
func TestE2ECheckerGenericArity(t *testing.T) {
	mustCompileError(t, `
function g<T>(a: unknown, b: T): void {}
g(1);
`, "expected 2 arguments, but got 1")
}

// `dns` and `zlib` are declared: their members are typed.
func TestE2EDnsZlibDeclared(t *testing.T) {
	assertSameAsNodeImports(t, `
import dns from 'dns';
import { gzipSync, gunzipSync, deflateSync, inflateSync } from 'zlib';
const z = gzipSync("hello hello hello");
console.log(gunzipSync(z).toString(), inflateSync(deflateSync("abc", { level: 9 })).toString());
dns.lookup("localhost", (err, address, family) => { console.log(err, typeof address, family); });
`)
	for _, c := range []struct{ src, want string }{
		{"import { gzipSync } from \"zlib\"\nconst n: number = gzipSync(\"x\")\n", "is not assignable to type 'number'"},
		{"import dns from \"dns\"\ndns.lookup(\"localhost\", (err, address: number) => {})\n", "not assignable"},
	} {
		_, err := resolveAndCompile(t, c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: expected %q, got %v", c.src, c.want, err)
		}
	}
}
