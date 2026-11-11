package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
	"KlainMainLang/resolver"
)

// `globalThis.X` aliases the ambient global X — the leading `globalThis.` peels
// off and the rest dispatches exactly as the bare form would (ADR-00508).

func TestE2EGlobalThisNamespaceMethod(t *testing.T) {
	assertOutput(t, `console.log(globalThis.JSON.stringify({ a: 1, b: 2 }))`, `{"a":1,"b":2}`)
}

func TestE2EGlobalThisConsole(t *testing.T) {
	assertOutput(t, `globalThis.console.log("hi")`, "hi")
}

func TestE2EGlobalThisMath(t *testing.T) {
	assertOutput(t, `console.log(globalThis.Math.max(3, 7, 5))`, "7")
}

func TestE2EGlobalThisGlobalFunction(t *testing.T) {
	assertOutput(t, `console.log(globalThis.parseInt("42", 10))`, "42")
}

func TestE2EGlobalThisTimer(t *testing.T) {
	assertOutput(t, `globalThis.setTimeout(() => { console.log("fired") }, 0)`, "fired")
}

func TestE2EGlobalThisUnknownGlobalRejected(t *testing.T) {
	_, err := parseAndCompile(`console.log(globalThis.totallyNotAGlobal)`)
	if err == nil {
		t.Fatal("expected a compile error for an unknown globalThis member, got none")
	}
}

// The global object carries a native global the program can read off it:
// every one when the object is used in a way no name shows (enumerated, or
// indexed by a computed key), as Node's does, in Node's order.
func TestE2EGlobalObjectEscapeCarriesEveryGlobal(t *testing.T) {
	t.Parallel()
	assertSameAsNodeCLIJS(t, `
const g = globalThis;
console.log(Object.keys(g).join(' '));
const names = Object.getOwnPropertyNames(g);
for (const n of ['process', 'Buffer', 'ReadableStream', 'Request', 'Response', 'fetch', 'Headers', 'URL', 'crypto', 'CryptoKey', 'DOMException']) {
  console.log(n, names.includes(n), typeof g[n]);
}
`)
}

// Read only by name, the global object links just the globals it names: a
// program reading globalThis.setTimeout through an alias carries no crypto
// or stream module, and Math read as a value no global object at all.
func TestGlobalObjectByNameLinksOnlyNamed(t *testing.T) {
	t.Parallel()
	for _, src := range []string{
		"const g: any = globalThis; console.log(typeof g.setTimeout, typeof g.Buffer)",
		"const m: any = Math; console.log(typeof m.max)",
	} {
		srcFile := filepath.Join(tempDir(t), "main.ts")
		if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
			t.Fatalf("write source: %v", err)
		}
		prog, err := resolver.ResolveProgram(srcFile)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		ir, err := llvm.NewEmitter().EmitProgram(prog)
		if err != nil {
			t.Fatalf("codegen: %v", err)
		}
		for _, mod := range []string{"node_crypto", "node_stream", "node_net", "node_worker_threads"} {
			if strings.Contains(ir, "__kml_modL"+fmt.Sprint(len(mod))+mod+"_") {
				t.Errorf("%q links %s", src, mod)
			}
		}
		if strings.Contains(src, "Math") && strings.Contains(ir, "_kmlGlobalThis") {
			t.Errorf("%q builds the global object", src)
		}
	}
	assertSameAsNodeCLIJS(t, "const g = globalThis; console.log(typeof g.setTimeout, typeof g.Buffer, g.Buffer === Buffer, typeof g.Math.max)")
}

// A native global read off an alias of the global object by name is the
// global itself (process is its emitter object).
func TestE2EGlobalObjectAliasReadsNatives(t *testing.T) {
	t.Parallel()
	assertSameAsNode(t, `
const g: any = globalThis;
console.log(typeof g.process, g.process === process, typeof g.Buffer, typeof g.ReadableStream, typeof g.DOMException, typeof g.Request, typeof g.CryptoKey);
`)
}

// A callback reading its own this, given a primitive thisArg: the global
// object's every builtin is linked, and its result is the boxed primitive.
func TestE2ECallbackThisValueOfGlobalObject(t *testing.T) {
	t.Parallel()
	assertSameAsNodeCLIJS(t, `
function callbackfn(val, idx, obj) { return this.valueOf() === 101; }
console.log([11].every(callbackfn, 101), globalThis.valueOf() === globalThis);
console.log(Object.getPrototypeOf(Int8Array) === Object.getPrototypeOf(Uint8Array), Object.getPrototypeOf(Map) === Function.prototype);
`)
}
