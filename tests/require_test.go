package tests

import "testing"

// Static CommonJS require('<literal>') desugars to the equivalent ES import at
// the top level, so Node-style code compiles unchanged. See ADR-00369.

// `const x = require('mod')` binds the whole module object — the namespace-import
// mapping — so member access on it works.
func TestE2ERequireNamespaceBuiltin(t *testing.T) {
	assertOutputImports(t, `
const path = require('path')
console.log(path.basename('/a/b/c.txt'))
console.log(path.extname('c.txt'))
`, "c.txt\n.txt")
}

// `const { a, b } = require('mod')` maps to a named import.
func TestE2ERequireNamedDestructure(t *testing.T) {
	assertOutputImports(t, `
const { basename, extname } = require('path')
console.log(basename('/x/y/z.md'))
console.log(extname('z.md'))
`, "z.md\n.md")
}

// A bare `require('mod')` is a side-effect-only import and must not break the
// program.
func TestE2ERequireBareSideEffect(t *testing.T) {
	assertOutputImports(t, `
require('path')
console.log('ok')
`, "ok")
}

// require and import interoperate in the same file over the same builtin.
func TestE2ERequireMixedWithImport(t *testing.T) {
	assertOutputImports(t, `
import assert from 'assert'
const path = require('path')
assert.strictEqual(path.basename('/a/b.js', '.js'), 'b')
console.log('interop')
`, "interop")
}

// A dynamic specifier (non-string-literal) is a clean compile error, not a
// silent miscompile — runtime/lazy module loading is a separate capability.
func TestE2ERequireDynamicRejected(t *testing.T) {
	_, err := parseAndCompileImports(t, `
const m = 'path'
const path = require(m)
console.log(path.basename('/a/b'))
`)
	if err == nil {
		t.Fatal("expected a compile error for require(variable), got none")
	}
}
